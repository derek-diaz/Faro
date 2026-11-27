package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
	"github.com/derek/faro/internal/protectiontime"
)

const maxProtectionProfiles = 12

var protectionIcons = map[string]struct{}{
	"house": {}, "users": {}, "baby": {}, "guest": {}, "tv": {}, "gamepad": {},
	"smartphone": {}, "laptop": {}, "briefcase": {}, "lightbulb": {}, "cpu": {}, "shield": {},
}

type protectionInput struct {
	Name         string                  `json:"name"`
	Icon         string                  `json:"icon"`
	BlocklistIDs []int64                 `json:"blocklist_ids"`
	AllowDomains []string                `json:"allow_domains"`
	BlockDomains []string                `json:"block_domains"`
	DeviceIPs    []string                `json:"device_ips"`
	DeviceIDs    []int64                 `json:"device_ids"`
	Schedule     protectionScheduleInput `json:"schedule"`
}

type protectionScheduleInput struct {
	Enabled  bool   `json:"enabled"`
	Days     string `json:"days"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"`
}

type protectionSnapshot struct {
	ID          int64
	Name        string
	Icon        string
	IsDefault   bool
	CreatedAt   string
	UpdatedAt   string
	PausedUntil string
	Input       protectionInput
}

type deviceProtectionAssignment struct {
	DeviceID     int64
	ProtectionID int64
}

func (handler *Handler) protections(responseWriter http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		handler.listProtections(responseWriter, request)
	case http.MethodPost:
		handler.configMu.Lock()
		defer handler.configMu.Unlock()
		if scalarInt(request.Context(), handler.store.DB, `SELECT COUNT(*) FROM protection_profiles`) >= maxProtectionProfiles {
			writeBadRequest(responseWriter, fmt.Errorf("faro supports up to %d protection setups", maxProtectionProfiles))
			return
		}
		var input protectionInput
		if !decode(responseWriter, request, &input) {
			return
		}
		normalized, err := handler.normalizeProtectionInput(request.Context(), input)
		if err != nil {
			writeBadRequest(responseWriter, err)
			return
		}
		assignments := readDeviceProtectionAssignments(request.Context(), handler.store.DB)
		id, err := handler.insertProtection(request.Context(), normalized)
		if err != nil {
			writeBadRequest(responseWriter, err)
			return
		}
		if err := handler.reloader.Apply(request.Context()); err != nil {
			rollbackCtx := context.WithoutCancel(request.Context())
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `DELETE FROM protection_profiles WHERE id = ?`, id)
			restoreDeviceProtectionAssignments(rollbackCtx, handler.store.DB, assignments)
			_ = handler.reloader.Apply(rollbackCtx)
			writeError(responseWriter, fmt.Errorf("protection was not created because CoreDNS rejected the configuration: %w", err))
			return
		}
		handler.recordEvent(request.Context(), eventInput{Type: "protection.created", Severity: "success", Title: "Protection created", Description: normalized.Name + " is ready to assign to devices.", Metadata: map[string]any{"protection_id": id, "icon": normalized.Icon}, Source: "protection"})
		writeJSON(responseWriter, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(responseWriter)
	}
}

func (handler *Handler) protection(responseWriter http.ResponseWriter, request *http.Request) {
	trimmedPath := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/protections/"), "/")
	if idText, action, ok := strings.Cut(trimmedPath, "/"); ok {
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id <= 0 {
			writeBadRequest(responseWriter, errors.New("invalid protection id"))
			return
		}
		switch action {
		case "pause":
			handler.pauseProtection(responseWriter, request, id)
		case "preview":
			handler.previewProtection(responseWriter, request, id)
		default:
			http.NotFound(responseWriter, request)
		}
		return
	}
	id, ok := idFromPath(responseWriter, request, "/api/protections/")
	if !ok {
		return
	}
	if request.Method != http.MethodPut && request.Method != http.MethodDelete {
		methodNotAllowed(responseWriter)
		return
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	previous, err := handler.readProtection(request.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeBadRequest(responseWriter, errors.New("protection does not exist"))
		} else {
			writeError(responseWriter, err)
		}
		return
	}
	assignments := readDeviceProtectionAssignments(request.Context(), handler.store.DB)
	if request.Method == http.MethodDelete {
		handler.deleteProtection(responseWriter, request, id, previous, assignments)
		return
	}
	handler.updateProtection(responseWriter, request, id, previous, assignments)
}

func (handler *Handler) deleteProtection(responseWriter http.ResponseWriter, request *http.Request, id int64, previous protectionSnapshot, assignments []deviceProtectionAssignment) {
	if previous.IsDefault {
		writeBadRequest(responseWriter, errors.New("home is Faro's default protection and cannot be deleted"))
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `DELETE FROM protection_profiles WHERE id = ?`, id); err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		_ = handler.restoreProtection(rollbackCtx, previous)
		restoreDeviceProtectionAssignments(rollbackCtx, handler.store.DB, assignments)
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("protection was not deleted because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) updateProtection(responseWriter http.ResponseWriter, request *http.Request, id int64, previous protectionSnapshot, assignments []deviceProtectionAssignment) {
	var input protectionInput
	if !decode(responseWriter, request, &input) {
		return
	}
	normalized, err := handler.normalizeProtectionInput(request.Context(), input)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	if previous.IsDefault {
		normalized.Name = "Home"
	}
	if err := handler.replaceProtection(request.Context(), id, previous.IsDefault, normalized); err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		_ = handler.restoreProtection(rollbackCtx, previous)
		restoreDeviceProtectionAssignments(rollbackCtx, handler.store.DB, assignments)
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("protection was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	handler.recordEvent(request.Context(), eventInput{Type: "protection.updated", Severity: "success", Title: "Protection updated", Description: normalized.Name + " settings were applied to DNS.", Metadata: map[string]any{"protection_id": id, "schedule_enabled": normalized.Schedule.Enabled}, Source: "protection"})
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) pauseProtection(responseWriter http.ResponseWriter, request *http.Request, id int64) {
	if request.Method != http.MethodPut {
		methodNotAllowed(responseWriter)
		return
	}
	var input struct {
		Until string `json:"until"`
	}
	if !decode(responseWriter, request, &input) {
		return
	}
	input.Until = strings.TrimSpace(input.Until)
	if input.Until != "" {
		until, err := time.Parse(time.RFC3339, input.Until)
		if err != nil || !until.After(time.Now()) {
			writeBadRequest(responseWriter, errors.New("pause end must be a future RFC3339 timestamp"))
			return
		}
		if until.After(time.Now().Add(7 * 24 * time.Hour)) {
			writeBadRequest(responseWriter, errors.New("a protection can be paused for at most 7 days"))
			return
		}
		input.Until = until.UTC().Format(time.RFC3339)
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	var name, previous string
	if err := handler.store.DB.QueryRowContext(request.Context(), `SELECT name, paused_until FROM protection_profiles WHERE id = ?`, id).Scan(&name, &previous); err != nil {
		writeBadRequest(responseWriter, errors.New("protection does not exist"))
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `UPDATE protection_profiles SET paused_until = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, input.Until, id); err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		_, _ = handler.store.DB.ExecContext(rollbackCtx, `UPDATE protection_profiles SET paused_until = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, previous, id)
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("protection pause was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	if input.Until == "" {
		handler.recordEvent(request.Context(), eventInput{Type: "protection.resumed", Severity: "success", Title: "Domain blocking turned on", Description: name + " is blocking domains again.", Metadata: map[string]any{"protection_id": id}, Source: "protection"})
	} else {
		handler.recordEvent(request.Context(), eventInput{Type: "protection.paused", Severity: "warning", Title: "Domain blocking temporarily off", Description: name + " is allowing domains it would normally block; devices remain online.", Metadata: map[string]any{"protection_id": id, "paused_until": input.Until}, Source: "protection"})
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true, "paused_until": nullableTimeString(input.Until)})
}

func nullableTimeString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (handler *Handler) listProtections(responseWriter http.ResponseWriter, request *http.Request) {
	rows, err := handler.store.DB.QueryContext(request.Context(), `
		SELECT id, name, icon, is_default, paused_until, schedule_enabled, schedule_days,
		       schedule_start, schedule_end, schedule_timezone, created_at, updated_at
		FROM protection_profiles ORDER BY is_default DESC, name`)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	defer closeRows(rows)
	type protectionRow struct {
		id                                                            int64
		name, icon                                                    string
		isDefault                                                     bool
		pausedUntil, days, start, end, timezone, createdAt, updatedAt string
		scheduleEnabled                                               bool
	}
	base := make([]protectionRow, 0)
	for rows.Next() {
		var item protectionRow
		if err := rows.Scan(&item.id, &item.name, &item.icon, &item.isDefault, &item.pausedUntil,
			&item.scheduleEnabled, &item.days, &item.start, &item.end, &item.timezone, &item.createdAt, &item.updatedAt); err != nil {
			writeError(responseWriter, err)
			return
		}
		base = append(base, item)
	}
	if err := rows.Err(); err != nil {
		writeError(responseWriter, err)
		return
	}
	items := make([]map[string]any, 0, len(base))
	for _, item := range base {
		paused := protectiontime.PausedAt(item.pausedUntil, time.Now())
		scheduledActive := protectiontime.ActiveAt(protectiontime.Schedule{Enabled: item.scheduleEnabled, Days: item.days, Start: item.start, End: item.end, Timezone: item.timezone}, time.Now())
		state := "active"
		if paused {
			state = "paused"
		} else if !scheduledActive {
			state = "scheduled_off"
		}
		items = append(items, map[string]any{
			"id": item.id, "name": item.name, "icon": item.icon, "is_default": item.isDefault,
			"blocklist_ids": protectionIDs(request.Context(), handler.store.DB, `SELECT blocklist_id FROM protection_blocklists WHERE protection_id = ? ORDER BY blocklist_id`, item.id),
			"allow_entries": protectionDomains(request.Context(), handler.store.DB, `SELECT id, domain, created_at FROM protection_allow_entries WHERE protection_id = ? ORDER BY domain`, item.id),
			"block_entries": protectionDomains(request.Context(), handler.store.DB, `SELECT id, domain, created_at FROM protection_block_entries WHERE protection_id = ? ORDER BY domain`, item.id),
			"device_ips": protectionStrings(request.Context(), handler.store.DB, `
				SELECT address FROM device_addresses a JOIN device_protection_memberships m ON m.device_id = a.device_id WHERE m.protection_id = ?
				UNION SELECT client_ip FROM device_protection_assignments WHERE protection_id = ? ORDER BY 1`, item.id, item.id),
			"device_ids": protectionIDs(request.Context(), handler.store.DB, `SELECT device_id FROM device_protection_memberships WHERE protection_id = ? ORDER BY device_id`, item.id),
			"created_at": item.createdAt, "updated_at": item.updatedAt,
			"paused_until": nullableTimeString(item.pausedUntil), "state": state, "is_active": !paused && scheduledActive,
			"schedule": map[string]any{"enabled": item.scheduleEnabled, "days": item.days, "start": item.start, "end": item.end, "timezone": item.timezone},
		})
	}
	writeJSON(responseWriter, http.StatusOK, items)
}

func (handler *Handler) normalizeProtectionInput(ctx context.Context, input protectionInput) (protectionInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 40 {
		return input, errors.New("name must be between 1 and 40 characters")
	}
	input.Icon = strings.ToLower(strings.TrimSpace(input.Icon))
	if _, ok := protectionIcons[input.Icon]; !ok {
		return input, errors.New("choose one of Faro's protection icons")
	}
	var err error
	input, err = handler.normalizeProtectionRules(ctx, input)
	if err != nil {
		return input, err
	}
	if input.DeviceIDs != nil {
		input.DeviceIPs = nil
		if len(input.DeviceIDs) > 1024 {
			return input, errors.New("assign at most 1024 devices at once")
		}
		for _, id := range input.DeviceIDs {
			if id <= 0 {
				return input, errors.New("device ids must be positive integers")
			}
		}
		input.DeviceIDs = uniqueInt64s(input.DeviceIDs)
		for _, id := range input.DeviceIDs {
			var exists bool
			if err := handler.store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id = ?)`, id).Scan(&exists); err != nil {
				return input, err
			}
			if id <= 0 || !exists {
				return input, fmt.Errorf("device %d does not exist", id)
			}
		}
	} else {
		input.DeviceIPs, err = normalizeProtectionDevices(input.DeviceIPs)
		if err != nil {
			return input, err
		}
	}
	schedule, err := protectiontime.Validate(protectiontime.Schedule{Enabled: input.Schedule.Enabled, Days: input.Schedule.Days, Start: input.Schedule.Start, End: input.Schedule.End, Timezone: input.Schedule.Timezone})
	if err != nil {
		return input, err
	}
	input.Schedule = protectionScheduleInput{Enabled: schedule.Enabled, Days: schedule.Days, Start: schedule.Start, End: schedule.End, Timezone: schedule.Timezone}
	return input, nil
}

func (handler *Handler) normalizeProtectionRules(ctx context.Context, input protectionInput) (protectionInput, error) {
	var err error
	input.BlocklistIDs, err = handler.normalizeProtectionBlocklists(ctx, input.BlocklistIDs)
	if err != nil {
		return input, err
	}
	input.AllowDomains, input.BlockDomains, err = normalizeProtectionDomains(input.AllowDomains, input.BlockDomains)
	return input, err
}

func (handler *Handler) normalizeProtectionBlocklists(ctx context.Context, values []int64) ([]int64, error) {
	values = uniqueInt64s(values)
	if len(values) > 20 {
		return values, errors.New("choose at most 20 blocklists")
	}
	for _, id := range values {
		var exists bool
		if err := handler.store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM blocklists WHERE id = ?)`, id).Scan(&exists); err != nil || !exists {
			return values, fmt.Errorf("blocklist %d is not installed", id)
		}
	}
	return values, nil
}

func normalizeProtectionDomains(allowed, blocked []string) ([]string, []string, error) {
	var err error
	allowed, err = normalizeDomains(allowed)
	if err != nil {
		return allowed, blocked, fmt.Errorf("allowed domain: %w", err)
	}
	blocked, err = normalizeDomains(blocked)
	if err != nil {
		return allowed, blocked, fmt.Errorf("blocked domain: %w", err)
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, domain := range allowed {
		allowedSet[domain] = struct{}{}
	}
	for _, domain := range blocked {
		if _, conflict := allowedSet[domain]; conflict {
			return allowed, blocked, fmt.Errorf("%s cannot be both allowed and blocked", domain)
		}
	}
	return allowed, blocked, nil
}

func normalizeProtectionDevices(values []string) ([]string, error) {
	if len(values) > 1024 {
		return values, errors.New("assign at most 1024 devices at once")
	}
	values = uniqueStrings(values)
	for index, address := range values {
		parsed := net.ParseIP(address)
		if parsed == nil {
			return values, fmt.Errorf("invalid device address %q", address)
		}
		values[index] = parsed.String()
	}
	return uniqueStrings(values), nil
}

func (handler *Handler) insertProtection(ctx context.Context, input protectionInput) (int64, error) {
	if input.DeviceIDs == nil {
		if err := handler.resolveProtectionDevices(ctx, input.DeviceIPs); err != nil {
			return 0, err
		}
	}
	tx, err := handler.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTransaction(tx)
	result, err := tx.ExecContext(ctx, `INSERT INTO protection_profiles(name, icon, schedule_enabled, schedule_days, schedule_start, schedule_end, schedule_timezone) VALUES(?, ?, ?, ?, ?, ?, ?)`, input.Name, input.Icon, input.Schedule.Enabled, input.Schedule.Days, input.Schedule.Start, input.Schedule.End, input.Schedule.Timezone)
	if err != nil {
		return 0, errors.New("a protection with that name already exists")
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := writeProtectionChildren(ctx, tx, id, false, input); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (handler *Handler) replaceProtection(ctx context.Context, id int64, isDefault bool, input protectionInput) error {
	if input.DeviceIDs == nil {
		if err := handler.resolveProtectionDevices(ctx, input.DeviceIPs); err != nil {
			return err
		}
	}
	tx, err := handler.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE protection_profiles SET name = ?, icon = ?, schedule_enabled = ?, schedule_days = ?, schedule_start = ?, schedule_end = ?, schedule_timezone = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, input.Name, input.Icon, input.Schedule.Enabled, input.Schedule.Days, input.Schedule.Start, input.Schedule.End, input.Schedule.Timezone, id); err != nil {
		return errors.New("a protection with that name already exists")
	}
	if err := clearProtectionChildren(ctx, tx, id); err != nil {
		return err
	}
	if err := writeProtectionChildren(ctx, tx, id, isDefault, input); err != nil {
		return err
	}
	return tx.Commit()
}

func (handler *Handler) readProtection(ctx context.Context, id int64) (protectionSnapshot, error) {
	var snapshot protectionSnapshot
	var schedule protectionScheduleInput
	if err := handler.store.DB.QueryRowContext(ctx, `SELECT id, name, icon, is_default, paused_until, created_at, updated_at, schedule_enabled, schedule_days, schedule_start, schedule_end, schedule_timezone FROM protection_profiles WHERE id = ?`, id).Scan(&snapshot.ID, &snapshot.Name, &snapshot.Icon, &snapshot.IsDefault, &snapshot.PausedUntil, &snapshot.CreatedAt, &snapshot.UpdatedAt, &schedule.Enabled, &schedule.Days, &schedule.Start, &schedule.End, &schedule.Timezone); err != nil {
		return snapshot, err
	}
	snapshot.Input = protectionInput{
		Name: snapshot.Name, Icon: snapshot.Icon, Schedule: schedule,
		BlocklistIDs: protectionIDs(ctx, handler.store.DB, `SELECT blocklist_id FROM protection_blocklists WHERE protection_id = ?`, id),
		AllowDomains: protectionDomainStrings(ctx, handler.store.DB, `SELECT domain FROM protection_allow_entries WHERE protection_id = ?`, id),
		BlockDomains: protectionDomainStrings(ctx, handler.store.DB, `SELECT domain FROM protection_block_entries WHERE protection_id = ?`, id),
		DeviceIDs:    protectionIDs(ctx, handler.store.DB, `SELECT device_id FROM device_protection_memberships WHERE protection_id = ? ORDER BY device_id`, id),
		DeviceIPs: protectionStrings(ctx, handler.store.DB, `
			SELECT address FROM device_addresses a JOIN device_protection_memberships m ON m.device_id = a.device_id WHERE m.protection_id = ?
			UNION SELECT client_ip FROM device_protection_assignments WHERE protection_id = ?`, id, id),
	}
	return snapshot, nil
}

func (handler *Handler) restoreProtection(ctx context.Context, snapshot protectionSnapshot) error {
	tx, err := handler.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx)
	if _, err := tx.ExecContext(ctx, `INSERT INTO protection_profiles(id, name, icon, is_default, paused_until, schedule_enabled, schedule_days, schedule_start, schedule_end, schedule_timezone, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, icon=excluded.icon, is_default=excluded.is_default, paused_until=excluded.paused_until, schedule_enabled=excluded.schedule_enabled, schedule_days=excluded.schedule_days, schedule_start=excluded.schedule_start, schedule_end=excluded.schedule_end, schedule_timezone=excluded.schedule_timezone, created_at=excluded.created_at, updated_at=excluded.updated_at`, snapshot.ID, snapshot.Name, snapshot.Icon, snapshot.IsDefault, snapshot.PausedUntil, snapshot.Input.Schedule.Enabled, snapshot.Input.Schedule.Days, snapshot.Input.Schedule.Start, snapshot.Input.Schedule.End, snapshot.Input.Schedule.Timezone, snapshot.CreatedAt, snapshot.UpdatedAt); err != nil {
		return err
	}
	if err := clearProtectionChildren(ctx, tx, snapshot.ID); err != nil {
		return err
	}
	if err := writeProtectionChildren(ctx, tx, snapshot.ID, snapshot.IsDefault, snapshot.Input); err != nil {
		return err
	}
	return tx.Commit()
}

func clearProtectionChildren(ctx context.Context, tx *sql.Tx, id int64) error {
	for _, statement := range []string{
		`DELETE FROM protection_blocklists WHERE protection_id = ?`,
		`DELETE FROM protection_allow_entries WHERE protection_id = ?`,
		`DELETE FROM protection_block_entries WHERE protection_id = ?`,
		`DELETE FROM device_protection_memberships WHERE protection_id = ?`,
		`DELETE FROM device_protection_assignments WHERE protection_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	return nil
}

func writeProtectionChildren(ctx context.Context, tx *sql.Tx, id int64, isDefault bool, input protectionInput) error {
	if err := insertProtectionBlocklists(ctx, tx, id, input.BlocklistIDs); err != nil {
		return err
	}
	if err := insertProtectionDomains(ctx, tx, id, "protection_allow_entries", input.AllowDomains); err != nil {
		return err
	}
	if err := insertProtectionDomains(ctx, tx, id, "protection_block_entries", input.BlockDomains); err != nil {
		return err
	}
	if isDefault {
		return nil
	}
	if input.DeviceIDs != nil {
		return insertProtectionDeviceIDs(ctx, tx, id, input.DeviceIDs)
	}
	return insertProtectionDevices(ctx, tx, id, input.DeviceIPs)
}

func insertProtectionBlocklists(ctx context.Context, tx *sql.Tx, id int64, blocklistIDs []int64) error {
	for _, blocklistID := range blocklistIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO protection_blocklists(protection_id, blocklist_id) VALUES(?, ?)`, id, blocklistID); err != nil {
			return err
		}
	}
	return nil
}

func insertProtectionDomains(ctx context.Context, tx *sql.Tx, id int64, table string, domains []string) error {
	statement := `INSERT INTO ` + table + `(protection_id, domain) VALUES(?, ?)`
	for _, domain := range domains {
		if _, err := tx.ExecContext(ctx, statement, id, domain); err != nil {
			return err
		}
	}
	return nil
}

func insertProtectionDevices(ctx context.Context, tx *sql.Tx, id int64, addresses []string) error {
	deviceIDs := make([]int64, 0, len(addresses))
	for _, address := range addresses {
		var deviceID int64
		if err := tx.QueryRowContext(ctx, `SELECT device_id FROM device_addresses WHERE address = ?`, address).Scan(&deviceID); err != nil {
			return err
		}
		deviceIDs = append(deviceIDs, deviceID)
	}
	return insertProtectionDeviceIDs(ctx, tx, id, deviceIDs)
}

func insertProtectionDeviceIDs(ctx context.Context, tx *sql.Tx, id int64, deviceIDs []int64) error {
	for _, deviceID := range deviceIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM device_protection_assignments WHERE client_ip IN (SELECT address FROM device_addresses WHERE device_id = ?)`, deviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_protection_memberships(device_id,protection_id) VALUES(?,?) ON CONFLICT(device_id) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, deviceID, id); err != nil {
			return err
		}
	}
	return nil
}

func (handler *Handler) resolveProtectionDevices(ctx context.Context, addresses []string) error {
	for _, address := range addresses {
		if _, err := deviceidentity.ResolveAddress(ctx, handler.store, address, "assignment"); err != nil {
			return fmt.Errorf("identify device %s: %w", address, err)
		}
	}
	return nil
}

func normalizeDomains(values []string) ([]string, error) {
	if len(values) > 500 {
		return nil, errors.New("use at most 500 custom domains")
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		domain, err := db.NormalizeDomain(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[domain]; !exists {
			seen[domain] = struct{}{}
			result = append(result, domain)
		}
	}
	sort.Strings(result)
	return result, nil
}

func uniqueInt64s(values []int64) []int64 {
	seen := map[int64]struct{}{}
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value > 0 {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	slices.Sort(result)
	return result
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	sort.Strings(result)
	return result
}

func protectionIDs(ctx context.Context, database *sql.DB, query string, args ...any) []int64 {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return make([]int64, 0)
	}
	defer closeRows(rows)
	result := make([]int64, 0)
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			continue
		}
		result = append(result, value)
	}
	return result
}

func protectionStrings(ctx context.Context, database *sql.DB, query string, args ...any) []string {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return make([]string, 0)
	}
	defer closeRows(rows)
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			continue
		}
		result = append(result, value)
	}
	return result
}

func protectionDomainStrings(ctx context.Context, database *sql.DB, query string, args ...any) []string {
	return protectionStrings(ctx, database, query, args...)
}

func protectionDomains(ctx context.Context, database *sql.DB, query string, args ...any) []map[string]any {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return make([]map[string]any, 0)
	}
	defer closeRows(rows)
	result := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var domain, createdAt string
		if err := rows.Scan(&id, &domain, &createdAt); err != nil {
			continue
		}
		result = append(result, map[string]any{"id": id, "domain": domain, "created_at": createdAt})
	}
	return result
}

func readDeviceProtectionAssignments(ctx context.Context, database *sql.DB) []deviceProtectionAssignment {
	rows, err := database.QueryContext(ctx, `
		SELECT m.device_id, m.protection_id
		FROM device_protection_memberships m ORDER BY m.device_id`)
	if err != nil {
		return nil
	}
	defer closeRows(rows)
	result := make([]deviceProtectionAssignment, 0)
	for rows.Next() {
		var assignment deviceProtectionAssignment
		if err := rows.Scan(&assignment.DeviceID, &assignment.ProtectionID); err != nil {
			continue
		}
		result = append(result, assignment)
	}
	return result
}

func restoreDeviceProtectionAssignments(ctx context.Context, database *sql.DB, assignments []deviceProtectionAssignment) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer rollbackTransaction(tx)
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_protection_memberships WHERE protection_id IS NOT NULL; DELETE FROM device_protection_assignments WHERE protection_id IS NOT NULL`); err != nil {
		return
	}
	for _, assignment := range assignments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?)`, assignment.DeviceID, assignment.ProtectionID); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

func protectionForClient(ctx context.Context, database *sql.DB, clientIP string) (int64, string, string) {
	var deviceID int64
	_ = database.QueryRowContext(ctx, `SELECT device_id FROM device_addresses WHERE address = ?`, clientIP).Scan(&deviceID)
	return protectionForDevice(ctx, database, deviceID, clientIP)
}

func protectionForDevice(ctx context.Context, database *sql.DB, deviceID int64, clientIP string) (int64, string, string) {
	var id int64
	var name, icon string
	err := database.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.icon
		FROM protection_profiles p
		LEFT JOIN device_protection_memberships m ON m.protection_id = p.id AND m.device_id = ?
		LEFT JOIN device_protection_assignments legacy ON legacy.protection_id = p.id AND legacy.client_ip = ?
		  AND (? = 0 OR ? = (SELECT device_id FROM device_addresses WHERE address = legacy.client_ip))
		WHERE m.device_id IS NOT NULL OR legacy.client_ip IS NOT NULL OR p.is_default = 1
		ORDER BY CASE WHEN m.device_id IS NOT NULL THEN 0 WHEN legacy.client_ip IS NOT NULL THEN 1 ELSE 2 END
		LIMIT 1
	`, deviceID, clientIP, deviceID, deviceID).Scan(&id, &name, &icon)
	if err != nil {
		return 0, "Home", "house"
	}
	return id, name, icon
}

func (handler *Handler) assignDeviceProtection(responseWriter http.ResponseWriter, request *http.Request, clientIP string) {
	if request.Method != http.MethodPut {
		methodNotAllowed(responseWriter)
		return
	}
	var input struct {
		ProtectionID int64 `json:"protection_id"`
	}
	if !decode(responseWriter, request, &input) {
		return
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	var isDefault bool
	if err := handler.store.DB.QueryRowContext(request.Context(), `SELECT is_default FROM protection_profiles WHERE id = ?`, input.ProtectionID).Scan(&isDefault); err != nil {
		writeBadRequest(responseWriter, errors.New("protection does not exist"))
		return
	}
	deviceID, _, err := handler.resolveDeviceReference(request.Context(), clientIP, "assignment")
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	tx, err := handler.store.DB.BeginTx(request.Context(), nil)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	defer rollbackTransaction(tx)
	var previousID sql.NullInt64
	if err := tx.QueryRowContext(request.Context(), `SELECT protection_id FROM device_protection_memberships WHERE device_id = ?`, deviceID).Scan(&previousID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(responseWriter, err)
		return
	}
	// Membership is authoritative; remove legacy IP overrides for every current address.
	if _, err := tx.ExecContext(request.Context(), `DELETE FROM device_protection_assignments WHERE client_ip IN (SELECT address FROM device_addresses WHERE device_id = ?)`, deviceID); err != nil {
		writeError(responseWriter, err)
		return
	}
	if isDefault {
		_, err = tx.ExecContext(request.Context(), `DELETE FROM device_protection_memberships WHERE device_id = ?`, deviceID)
	} else {
		_, err = tx.ExecContext(request.Context(), `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?) ON CONFLICT(device_id) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, deviceID, input.ProtectionID)
	}
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		if previousID.Valid {
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?) ON CONFLICT(device_id) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, deviceID, previousID.Int64)
		} else {
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `DELETE FROM device_protection_memberships WHERE device_id = ?`, deviceID)
		}
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("device protection was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

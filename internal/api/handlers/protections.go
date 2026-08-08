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
	"strings"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
)

const maxProtectionProfiles = 12

var protectionIcons = map[string]struct{}{
	"house": {}, "users": {}, "baby": {}, "guest": {}, "tv": {}, "gamepad": {},
	"smartphone": {}, "laptop": {}, "briefcase": {}, "lightbulb": {}, "cpu": {}, "shield": {},
}

type protectionInput struct {
	Name         string   `json:"name"`
	Icon         string   `json:"icon"`
	BlocklistIDs []int64  `json:"blocklist_ids"`
	AllowDomains []string `json:"allow_domains"`
	BlockDomains []string `json:"block_domains"`
	DeviceIPs    []string `json:"device_ips"`
}

type protectionSnapshot struct {
	ID        int64
	Name      string
	Icon      string
	IsDefault bool
	CreatedAt string
	UpdatedAt string
	Input     protectionInput
}

type deviceProtectionAssignment struct {
	DeviceID     int64
	ClientIP     string
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
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) listProtections(responseWriter http.ResponseWriter, request *http.Request) {
	rows, err := handler.store.DB.QueryContext(request.Context(), `SELECT id, name, icon, is_default, created_at, updated_at FROM protection_profiles ORDER BY is_default DESC, name`)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	defer closeRows(rows)
	type protectionRow struct {
		id                   int64
		name, icon           string
		isDefault            bool
		createdAt, updatedAt string
	}
	base := make([]protectionRow, 0)
	for rows.Next() {
		var item protectionRow
		if err := rows.Scan(&item.id, &item.name, &item.icon, &item.isDefault, &item.createdAt, &item.updatedAt); err != nil {
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
		items = append(items, map[string]any{
			"id": item.id, "name": item.name, "icon": item.icon, "is_default": item.isDefault,
			"blocklist_ids": protectionIDs(request.Context(), handler.store.DB, `SELECT blocklist_id FROM protection_blocklists WHERE protection_id = ? ORDER BY blocklist_id`, item.id),
			"allow_entries": protectionDomains(request.Context(), handler.store.DB, `SELECT id, domain, created_at FROM protection_allow_entries WHERE protection_id = ? ORDER BY domain`, item.id),
			"block_entries": protectionDomains(request.Context(), handler.store.DB, `SELECT id, domain, created_at FROM protection_block_entries WHERE protection_id = ? ORDER BY domain`, item.id),
			"device_ips": protectionStrings(request.Context(), handler.store.DB, `
				SELECT address FROM device_addresses a JOIN device_protection_memberships m ON m.device_id = a.device_id WHERE m.protection_id = ?
				UNION SELECT client_ip FROM device_protection_assignments WHERE protection_id = ? ORDER BY 1`, item.id, item.id),
			"created_at": item.createdAt, "updated_at": item.updatedAt,
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
	input.BlocklistIDs, err = handler.normalizeProtectionBlocklists(ctx, input.BlocklistIDs)
	if err != nil {
		return input, err
	}
	input.AllowDomains, input.BlockDomains, err = normalizeProtectionDomains(input.AllowDomains, input.BlockDomains)
	if err != nil {
		return input, err
	}
	input.DeviceIPs, err = normalizeProtectionDevices(input.DeviceIPs)
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
	if err := handler.resolveProtectionDevices(ctx, input.DeviceIPs); err != nil {
		return 0, err
	}
	tx, err := handler.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTransaction(tx)
	result, err := tx.ExecContext(ctx, `INSERT INTO protection_profiles(name, icon) VALUES(?, ?)`, input.Name, input.Icon)
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
	if err := handler.resolveProtectionDevices(ctx, input.DeviceIPs); err != nil {
		return err
	}
	tx, err := handler.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE protection_profiles SET name = ?, icon = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, input.Name, input.Icon, id); err != nil {
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
	if err := handler.store.DB.QueryRowContext(ctx, `SELECT id, name, icon, is_default, created_at, updated_at FROM protection_profiles WHERE id = ?`, id).Scan(&snapshot.ID, &snapshot.Name, &snapshot.Icon, &snapshot.IsDefault, &snapshot.CreatedAt, &snapshot.UpdatedAt); err != nil {
		return snapshot, err
	}
	snapshot.Input = protectionInput{
		Name: snapshot.Name, Icon: snapshot.Icon,
		BlocklistIDs: protectionIDs(ctx, handler.store.DB, `SELECT blocklist_id FROM protection_blocklists WHERE protection_id = ?`, id),
		AllowDomains: protectionDomainStrings(ctx, handler.store.DB, `SELECT domain FROM protection_allow_entries WHERE protection_id = ?`, id),
		BlockDomains: protectionDomainStrings(ctx, handler.store.DB, `SELECT domain FROM protection_block_entries WHERE protection_id = ?`, id),
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO protection_profiles(id, name, icon, is_default, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, icon=excluded.icon, is_default=excluded.is_default, created_at=excluded.created_at, updated_at=excluded.updated_at`, snapshot.ID, snapshot.Name, snapshot.Icon, snapshot.IsDefault, snapshot.CreatedAt, snapshot.UpdatedAt); err != nil {
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
	for _, address := range addresses {
		if _, err := tx.ExecContext(ctx, `
				INSERT INTO device_protection_memberships(device_id, protection_id, updated_at)
				SELECT device_id, ?, CURRENT_TIMESTAMP FROM device_addresses WHERE address = ?
				ON CONFLICT(device_id) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, id, address); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES(?, ?) ON CONFLICT(client_ip) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, address, id); err != nil {
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
		SELECT m.device_id, COALESCE((SELECT address FROM device_addresses WHERE device_id = m.device_id ORDER BY last_seen_at DESC, id DESC LIMIT 1), ''), m.protection_id
		FROM device_protection_memberships m ORDER BY m.device_id`)
	if err != nil {
		return nil
	}
	defer closeRows(rows)
	result := make([]deviceProtectionAssignment, 0)
	for rows.Next() {
		var assignment deviceProtectionAssignment
		if err := rows.Scan(&assignment.DeviceID, &assignment.ClientIP, &assignment.ProtectionID); err != nil {
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
		if assignment.ClientIP != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES(?, ?)`, assignment.ClientIP, assignment.ProtectionID); err != nil {
				return
			}
		}
	}
	_ = tx.Commit()
}

func protectionForClient(ctx context.Context, database *sql.DB, clientIP string) (int64, string, string) {
	var id int64
	var name, icon string
	err := database.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.icon
		FROM protection_profiles p
		LEFT JOIN device_addresses da ON da.address = ?
		LEFT JOIN device_protection_memberships m ON m.protection_id = p.id AND m.device_id = da.device_id
		LEFT JOIN device_protection_assignments legacy ON legacy.protection_id = p.id AND legacy.client_ip = ?
		WHERE m.device_id IS NOT NULL OR legacy.client_ip IS NOT NULL OR p.is_default = 1
		ORDER BY CASE WHEN m.device_id IS NOT NULL THEN 0 WHEN legacy.client_ip IS NOT NULL THEN 1 ELSE 2 END
		LIMIT 1
	`, clientIP, clientIP).Scan(&id, &name, &icon)
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
	parsedClientIP := net.ParseIP(clientIP)
	if parsedClientIP == nil {
		writeBadRequest(responseWriter, errors.New("invalid client ip"))
		return
	}
	clientIP = parsedClientIP.String()
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
	deviceID, err := deviceidentity.ResolveAddress(request.Context(), handler.store, clientIP, "assignment")
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	var previousID sql.NullInt64
	_ = handler.store.DB.QueryRowContext(request.Context(), `SELECT protection_id FROM device_protection_memberships WHERE device_id = ?`, deviceID).Scan(&previousID)
	if isDefault {
		_, _ = handler.store.DB.ExecContext(request.Context(), `DELETE FROM device_protection_memberships WHERE device_id = ?`, deviceID)
		_, _ = handler.store.DB.ExecContext(request.Context(), `DELETE FROM device_protection_assignments WHERE client_ip = ?`, clientIP)
	} else {
		_, _ = handler.store.DB.ExecContext(request.Context(), `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?) ON CONFLICT(device_id) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, deviceID, input.ProtectionID)
		_, _ = handler.store.DB.ExecContext(request.Context(), `INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES(?, ?) ON CONFLICT(client_ip) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, clientIP, input.ProtectionID)
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		if previousID.Valid {
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?) ON CONFLICT(device_id) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, deviceID, previousID.Int64)
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES(?, ?) ON CONFLICT(client_ip) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, clientIP, previousID.Int64)
		} else {
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `DELETE FROM device_protection_memberships WHERE device_id = ?`, deviceID)
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `DELETE FROM device_protection_assignments WHERE client_ip = ?`, clientIP)
		}
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("device protection was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

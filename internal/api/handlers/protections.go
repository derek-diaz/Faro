package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
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

func (s *Handler) protections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listProtections(w, r)
	case http.MethodPost:
		s.configMu.Lock()
		defer s.configMu.Unlock()
		if scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM protection_profiles`) >= maxProtectionProfiles {
			writeBadRequest(w, fmt.Errorf("Faro supports up to %d protection setups", maxProtectionProfiles))
			return
		}
		var input protectionInput
		if !decode(w, r, &input) {
			return
		}
		normalized, err := s.normalizeProtectionInput(r.Context(), input)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		assignments := readDeviceProtectionAssignments(r.Context(), s.store.DB)
		id, err := s.insertProtection(r.Context(), normalized)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
			rollbackCtx := context.WithoutCancel(r.Context())
			_, _ = s.store.DB.ExecContext(rollbackCtx, `DELETE FROM protection_profiles WHERE id = ?`, id)
			restoreDeviceProtectionAssignments(rollbackCtx, s.store.DB, assignments)
			_ = s.reloader.Apply(rollbackCtx)
			writeError(w, fmt.Errorf("protection was not created because CoreDNS rejected the configuration: %w", err))
			return
		}
		s.recordEvent(r.Context(), eventInput{Type: "protection.created", Severity: "success", Title: "Protection created", Description: normalized.Name + " is ready to assign to devices.", Metadata: map[string]any{"protection_id": id, "icon": normalized.Icon}, Source: "protection"})
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) protection(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(w, r, "/api/protections/")
	if !ok {
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	previous, err := s.readProtection(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeBadRequest(w, errors.New("protection does not exist"))
		} else {
			writeError(w, err)
		}
		return
	}
	assignments := readDeviceProtectionAssignments(r.Context(), s.store.DB)
	if r.Method == http.MethodDelete {
		if previous.IsDefault {
			writeBadRequest(w, errors.New("Home is Faro's default protection and cannot be deleted"))
			return
		}
		if _, err := s.store.DB.ExecContext(r.Context(), `DELETE FROM protection_profiles WHERE id = ?`, id); err != nil {
			writeError(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
			rollbackCtx := context.WithoutCancel(r.Context())
			_ = s.restoreProtection(rollbackCtx, previous)
			restoreDeviceProtectionAssignments(rollbackCtx, s.store.DB, assignments)
			_ = s.reloader.Apply(rollbackCtx)
			writeError(w, fmt.Errorf("protection was not deleted because CoreDNS rejected the configuration: %w", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	var input protectionInput
	if !decode(w, r, &input) {
		return
	}
	normalized, err := s.normalizeProtectionInput(r.Context(), input)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	if previous.IsDefault {
		normalized.Name = "Home"
	}
	if err := s.replaceProtection(r.Context(), id, previous.IsDefault, normalized); err != nil {
		writeBadRequest(w, err)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(r.Context())
		_ = s.restoreProtection(rollbackCtx, previous)
		restoreDeviceProtectionAssignments(rollbackCtx, s.store.DB, assignments)
		_ = s.reloader.Apply(rollbackCtx)
		writeError(w, fmt.Errorf("protection was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Handler) listProtections(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id, name, icon, is_default, created_at, updated_at FROM protection_profiles ORDER BY is_default DESC, name`)
	if err != nil {
		writeError(w, err)
		return
	}
	type protectionRow struct {
		id                   int64
		name, icon           string
		isDefault            bool
		createdAt, updatedAt string
	}
	base := []protectionRow{}
	for rows.Next() {
		var item protectionRow
		if err := rows.Scan(&item.id, &item.name, &item.icon, &item.isDefault, &item.createdAt, &item.updatedAt); err != nil {
			_ = rows.Close()
			writeError(w, err)
			return
		}
		base = append(base, item)
	}
	if err := rows.Close(); err != nil {
		writeError(w, err)
		return
	}
	items := []map[string]any{}
	for _, item := range base {
		items = append(items, map[string]any{
			"id": item.id, "name": item.name, "icon": item.icon, "is_default": item.isDefault,
			"blocklist_ids": protectionIDs(r.Context(), s.store.DB, `SELECT blocklist_id FROM protection_blocklists WHERE protection_id = ? ORDER BY blocklist_id`, item.id),
			"allow_entries": protectionDomains(r.Context(), s.store.DB, `SELECT id, domain, created_at FROM protection_allow_entries WHERE protection_id = ? ORDER BY domain`, item.id),
			"block_entries": protectionDomains(r.Context(), s.store.DB, `SELECT id, domain, created_at FROM protection_block_entries WHERE protection_id = ? ORDER BY domain`, item.id),
			"device_ips": protectionStrings(r.Context(), s.store.DB, `
				SELECT address FROM device_addresses a JOIN device_protection_memberships m ON m.device_id = a.device_id WHERE m.protection_id = ?
				UNION SELECT client_ip FROM device_protection_assignments WHERE protection_id = ? ORDER BY 1`, item.id, item.id),
			"created_at": item.createdAt, "updated_at": item.updatedAt,
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Handler) normalizeProtectionInput(ctx context.Context, input protectionInput) (protectionInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 40 {
		return input, errors.New("name must be between 1 and 40 characters")
	}
	input.Icon = strings.ToLower(strings.TrimSpace(input.Icon))
	if _, ok := protectionIcons[input.Icon]; !ok {
		return input, errors.New("choose one of Faro's protection icons")
	}
	input.BlocklistIDs = uniqueInt64s(input.BlocklistIDs)
	if len(input.BlocklistIDs) > 20 {
		return input, errors.New("choose at most 20 blocklists")
	}
	for _, id := range input.BlocklistIDs {
		var exists bool
		if err := s.store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM blocklists WHERE id = ?)`, id).Scan(&exists); err != nil || !exists {
			return input, fmt.Errorf("blocklist %d is not installed", id)
		}
	}
	var err error
	if input.AllowDomains, err = normalizeDomains(input.AllowDomains); err != nil {
		return input, fmt.Errorf("allowed domain: %w", err)
	}
	if input.BlockDomains, err = normalizeDomains(input.BlockDomains); err != nil {
		return input, fmt.Errorf("blocked domain: %w", err)
	}
	allowed := map[string]struct{}{}
	for _, domain := range input.AllowDomains {
		allowed[domain] = struct{}{}
	}
	for _, domain := range input.BlockDomains {
		if _, conflict := allowed[domain]; conflict {
			return input, fmt.Errorf("%s cannot be both allowed and blocked", domain)
		}
	}
	if len(input.DeviceIPs) > 1024 {
		return input, errors.New("assign at most 1024 devices at once")
	}
	input.DeviceIPs = uniqueStrings(input.DeviceIPs)
	for index, address := range input.DeviceIPs {
		parsed := net.ParseIP(address)
		if parsed == nil {
			return input, fmt.Errorf("invalid device address %q", address)
		}
		input.DeviceIPs[index] = parsed.String()
	}
	input.DeviceIPs = uniqueStrings(input.DeviceIPs)
	return input, nil
}

func (s *Handler) insertProtection(ctx context.Context, input protectionInput) (int64, error) {
	if err := s.resolveProtectionDevices(ctx, input.DeviceIPs); err != nil {
		return 0, err
	}
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
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

func (s *Handler) replaceProtection(ctx context.Context, id int64, isDefault bool, input protectionInput) error {
	if err := s.resolveProtectionDevices(ctx, input.DeviceIPs); err != nil {
		return err
	}
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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

func (s *Handler) readProtection(ctx context.Context, id int64) (protectionSnapshot, error) {
	var snapshot protectionSnapshot
	if err := s.store.DB.QueryRowContext(ctx, `SELECT id, name, icon, is_default, created_at, updated_at FROM protection_profiles WHERE id = ?`, id).Scan(&snapshot.ID, &snapshot.Name, &snapshot.Icon, &snapshot.IsDefault, &snapshot.CreatedAt, &snapshot.UpdatedAt); err != nil {
		return snapshot, err
	}
	snapshot.Input = protectionInput{
		Name: snapshot.Name, Icon: snapshot.Icon,
		BlocklistIDs: protectionIDs(ctx, s.store.DB, `SELECT blocklist_id FROM protection_blocklists WHERE protection_id = ?`, id),
		AllowDomains: protectionDomainStrings(ctx, s.store.DB, `SELECT domain FROM protection_allow_entries WHERE protection_id = ?`, id),
		BlockDomains: protectionDomainStrings(ctx, s.store.DB, `SELECT domain FROM protection_block_entries WHERE protection_id = ?`, id),
		DeviceIPs: protectionStrings(ctx, s.store.DB, `
			SELECT address FROM device_addresses a JOIN device_protection_memberships m ON m.device_id = a.device_id WHERE m.protection_id = ?
			UNION SELECT client_ip FROM device_protection_assignments WHERE protection_id = ?`, id, id),
	}
	return snapshot, nil
}

func (s *Handler) restoreProtection(ctx context.Context, snapshot protectionSnapshot) error {
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	for _, blocklistID := range input.BlocklistIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO protection_blocklists(protection_id, blocklist_id) VALUES(?, ?)`, id, blocklistID); err != nil {
			return err
		}
	}
	for _, domain := range input.AllowDomains {
		if _, err := tx.ExecContext(ctx, `INSERT INTO protection_allow_entries(protection_id, domain) VALUES(?, ?)`, id, domain); err != nil {
			return err
		}
	}
	for _, domain := range input.BlockDomains {
		if _, err := tx.ExecContext(ctx, `INSERT INTO protection_block_entries(protection_id, domain) VALUES(?, ?)`, id, domain); err != nil {
			return err
		}
	}
	if !isDefault {
		for _, address := range input.DeviceIPs {
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
	}
	return nil
}

func (s *Handler) resolveProtectionDevices(ctx context.Context, addresses []string) error {
	for _, address := range addresses {
		if _, err := deviceidentity.ResolveAddress(ctx, s.store, address, "assignment"); err != nil {
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
	result := []string{}
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
	result := []int64{}
	for _, value := range values {
		if value > 0 {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
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
		return []int64{}
	}
	defer rows.Close()
	result := []int64{}
	for rows.Next() {
		var value int64
		if rows.Scan(&value) == nil {
			result = append(result, value)
		}
	}
	return result
}

func protectionStrings(ctx context.Context, database *sql.DB, query string, args ...any) []string {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var value string
		if rows.Scan(&value) == nil {
			result = append(result, value)
		}
	}
	return result
}

func protectionDomainStrings(ctx context.Context, database *sql.DB, query string, args ...any) []string {
	return protectionStrings(ctx, database, query, args...)
}

func protectionDomains(ctx context.Context, database *sql.DB, query string, args ...any) []map[string]any {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id int64
		var domain, createdAt string
		if rows.Scan(&id, &domain, &createdAt) == nil {
			result = append(result, map[string]any{"id": id, "domain": domain, "created_at": createdAt})
		}
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
	defer rows.Close()
	result := []deviceProtectionAssignment{}
	for rows.Next() {
		var assignment deviceProtectionAssignment
		if rows.Scan(&assignment.DeviceID, &assignment.ClientIP, &assignment.ProtectionID) == nil {
			result = append(result, assignment)
		}
	}
	return result
}

func restoreDeviceProtectionAssignments(ctx context.Context, database *sql.DB, assignments []deviceProtectionAssignment) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_protection_memberships; DELETE FROM device_protection_assignments`); err != nil {
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

func (s *Handler) assignDeviceProtection(w http.ResponseWriter, r *http.Request, clientIP string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	parsedClientIP := net.ParseIP(clientIP)
	if parsedClientIP == nil {
		writeBadRequest(w, errors.New("invalid client ip"))
		return
	}
	clientIP = parsedClientIP.String()
	var input struct {
		ProtectionID int64 `json:"protection_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var isDefault bool
	if err := s.store.DB.QueryRowContext(r.Context(), `SELECT is_default FROM protection_profiles WHERE id = ?`, input.ProtectionID).Scan(&isDefault); err != nil {
		writeBadRequest(w, errors.New("protection does not exist"))
		return
	}
	deviceID, err := deviceidentity.ResolveAddress(r.Context(), s.store, clientIP, "assignment")
	if err != nil {
		writeError(w, err)
		return
	}
	var previousID sql.NullInt64
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT protection_id FROM device_protection_memberships WHERE device_id = ?`, deviceID).Scan(&previousID)
	if isDefault {
		_, _ = s.store.DB.ExecContext(r.Context(), `DELETE FROM device_protection_memberships WHERE device_id = ?`, deviceID)
		_, _ = s.store.DB.ExecContext(r.Context(), `DELETE FROM device_protection_assignments WHERE client_ip = ?`, clientIP)
	} else {
		_, _ = s.store.DB.ExecContext(r.Context(), `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?) ON CONFLICT(device_id) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, deviceID, input.ProtectionID)
		_, _ = s.store.DB.ExecContext(r.Context(), `INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES(?, ?) ON CONFLICT(client_ip) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, clientIP, input.ProtectionID)
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(r.Context())
		if previousID.Valid {
			_, _ = s.store.DB.ExecContext(rollbackCtx, `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?) ON CONFLICT(device_id) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, deviceID, previousID.Int64)
			_, _ = s.store.DB.ExecContext(rollbackCtx, `INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES(?, ?) ON CONFLICT(client_ip) DO UPDATE SET protection_id=excluded.protection_id, updated_at=CURRENT_TIMESTAMP`, clientIP, previousID.Int64)
		} else {
			_, _ = s.store.DB.ExecContext(rollbackCtx, `DELETE FROM device_protection_memberships WHERE device_id = ?`, deviceID)
			_, _ = s.store.DB.ExecContext(rollbackCtx, `DELETE FROM device_protection_assignments WHERE client_ip = ?`, clientIP)
		}
		_ = s.reloader.Apply(rollbackCtx)
		writeError(w, fmt.Errorf("device protection was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

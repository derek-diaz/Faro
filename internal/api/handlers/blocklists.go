package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/derek/faro/internal/db"
)

func (s *Handler) blocklists(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listBlocklists(w, r)
	case http.MethodPost:
		s.createBlocklist(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) listBlocklists(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB.QueryContext(r.Context(), `
		SELECT b.id, b.name, b.url, b.enabled, b.last_refreshed_at, b.created_at, b.updated_at,
			COUNT(e.id) AS entry_count,
			(SELECT COUNT(*) FROM protection_blocklists pb WHERE pb.blocklist_id = b.id) AS protection_count
		FROM blocklists b
		LEFT JOIN blocklist_entries e ON e.blocklist_id = b.id
		GROUP BY b.id, b.name, b.url, b.enabled, b.last_refreshed_at, b.created_at, b.updated_at
		ORDER BY b.name
	`)
	if err != nil {
		writeError(w, err)
		return
	}
	defer closeRows(rows)
	writeRows(w, rows)
}

func (s *Handler) createBlocklist(w http.ResponseWriter, r *http.Request) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var input blocklistInput
	if !decode(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || strings.TrimSpace(input.URL) == "" {
		writeBadRequest(w, errors.New("name and url are required"))
		return
	}
	sourceURL, err := normalizeBlocklistURL(input.URL)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	var existingID int64
	err = s.store.DB.QueryRowContext(r.Context(), `
		SELECT id FROM blocklists
		WHERE lower(name) = lower(?) OR lower(url) = lower(?)
		LIMIT 1
	`, name, sourceURL).Scan(&existingID)
	if err == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "that blocklist is already installed"})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, err)
		return
	}
	enabled := boolInt(input.Enabled == nil || *input.Enabled)
	result, err := s.store.DB.ExecContext(r.Context(), `INSERT INTO blocklists(name, url, enabled) VALUES(?, ?, ?)`, name, sourceURL, enabled)
	if err != nil {
		writeError(w, err)
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		writeError(w, err)
		return
	}
	assignToDefault := input.AssignToDefault == nil || *input.AssignToDefault
	if enabled == 1 && assignToDefault {
		if _, err := s.store.DB.ExecContext(r.Context(), `
			INSERT OR IGNORE INTO protection_blocklists(protection_id, blocklist_id)
			SELECT id, ? FROM protection_profiles WHERE is_default = 1
		`, id); err != nil {
			_, _ = s.store.DB.ExecContext(context.WithoutCancel(r.Context()), `DELETE FROM blocklists WHERE id = ?`, id)
			writeError(w, err)
			return
		}
	}
	s.recordEvent(r.Context(), eventInput{
		Type:        "blocklist.installed",
		Severity:    "success",
		Title:       "Blocklist installed",
		Description: name + " is ready to use.",
		Metadata:    map[string]any{"blocklist_id": id, "url": strings.TrimSpace(input.URL)},
		Source:      "blocklists",
	})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Handler) blocklist(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
		s.configMu.Lock()
		defer s.configMu.Unlock()
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/blocklists/")
	if strings.Trim(path, "/") == "refresh" {
		s.refreshAllBlocklists(w, r)
		return
	}
	if idText, ok := strings.CutSuffix(path, "/refresh"); ok {
		s.refreshBlocklist(w, r, idText)
		return
	}

	id, err := strconv.ParseInt(strings.Trim(path, "/"), 10, 64)
	if err != nil {
		writeBadRequest(w, errors.New("invalid id"))
		return
	}
	switch r.Method {
	case http.MethodPut:
		s.updateBlocklist(w, r, id)
	case http.MethodDelete:
		s.deleteBlocklist(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) refreshAllBlocklists(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id FROM blocklists WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		writeError(w, err)
		return
	}
	defer closeRows(rows)
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			writeError(w, err)
			return
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		writeError(w, err)
		return
	}
	totalEntries := 0
	updated := 0
	for _, id := range ids {
		count, err := s.refresher.RefreshAndApply(r.Context(), id, s.reloader.Apply)
		if err != nil {
			writeError(w, fmt.Errorf("refresh blocklist %d: %w", id, err))
			return
		}
		totalEntries += count
		updated++
	}
	s.recordEvent(r.Context(), eventInput{
		Type:        "blocklist.updated",
		Severity:    "success",
		Title:       "Blocklists updated",
		Description: fmt.Sprintf("Refreshed %d enabled lists with %d domains.", updated, totalEntries),
		Metadata:    map[string]any{"blocklist_count": updated, "entry_count": totalEntries},
		Source:      "blocklists",
	})
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated, "entry_count": totalEntries})
}

func (s *Handler) refreshBlocklist(w http.ResponseWriter, r *http.Request, idText string) {
	id, err := strconv.ParseInt(strings.Trim(idText, "/"), 10, 64)
	if err != nil {
		writeBadRequest(w, errors.New("invalid id"))
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	count, err := s.refresher.RefreshAndApply(r.Context(), id, s.reloader.Apply)
	if err != nil {
		writeError(w, err)
		return
	}
	s.recordEvent(r.Context(), eventInput{
		Type:        "blocklist.updated",
		Severity:    "success",
		Title:       "Blocklist updated",
		Description: fmt.Sprintf("Refreshed %d domains.", count),
		Metadata:    map[string]any{"blocklist_id": id, "entry_count": count},
		Source:      "blocklists",
	})
	writeJSON(w, http.StatusOK, map[string]any{"entry_count": count})
}

func (s *Handler) updateBlocklist(w http.ResponseWriter, r *http.Request, id int64) {
	var input blocklistInput
	if !decode(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || strings.TrimSpace(input.URL) == "" {
		writeBadRequest(w, errors.New("name and url are required"))
		return
	}
	sourceURL, err := normalizeBlocklistURL(input.URL)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	enabled := boolInt(input.Enabled == nil || *input.Enabled)
	previous, err := s.readBlocklist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `UPDATE blocklists SET name = ?, url = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, sourceURL, enabled, id); err != nil {
		writeError(w, err)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(r.Context())
		s.restoreBlocklist(rollbackCtx, previous)
		_ = s.reloader.Apply(rollbackCtx)
		writeError(w, fmt.Errorf("blocklist was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Handler) deleteBlocklist(w http.ResponseWriter, r *http.Request, id int64) {
	previous, err := s.readBlocklist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `UPDATE blocklists SET enabled = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		writeError(w, err)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(r.Context())
		s.restoreBlocklist(rollbackCtx, previous)
		_ = s.reloader.Apply(rollbackCtx)
		writeError(w, fmt.Errorf("blocklist was not deleted because CoreDNS rejected the configuration: %w", err))
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `DELETE FROM blocklists WHERE id = ?`, id); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func normalizeBlocklistURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return "", errors.New("blocklist URL must be an http or https URL without embedded credentials")
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

type storedBlocklist struct {
	ID              int64
	Name            string
	URL             string
	Enabled         int
	LastRefreshedAt sql.NullString
	CreatedAt       string
	UpdatedAt       string
}

func (s *Handler) readBlocklist(ctx context.Context, id int64) (storedBlocklist, error) {
	var blocklist storedBlocklist
	err := s.store.DB.QueryRowContext(ctx, `SELECT id, name, url, enabled, last_refreshed_at, created_at, updated_at FROM blocklists WHERE id = ?`, id).
		Scan(&blocklist.ID, &blocklist.Name, &blocklist.URL, &blocklist.Enabled, &blocklist.LastRefreshedAt, &blocklist.CreatedAt, &blocklist.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return blocklist, fmt.Errorf("blocklist %d does not exist", id)
	}
	return blocklist, err
}

func (s *Handler) restoreBlocklist(ctx context.Context, blocklist storedBlocklist) {
	_, _ = s.store.DB.ExecContext(ctx, `UPDATE blocklists SET name=?, url=?, enabled=?, last_refreshed_at=?, created_at=?, updated_at=? WHERE id=?`,
		blocklist.Name, blocklist.URL, blocklist.Enabled, blocklist.LastRefreshedAt, blocklist.CreatedAt, blocklist.UpdatedAt, blocklist.ID)
}

func (s *Handler) allowlist(w http.ResponseWriter, r *http.Request) {
	s.domainCollection(w, r, "protection_allow_entries")
}

func (s *Handler) allowlistEntry(w http.ResponseWriter, r *http.Request) {
	s.domainDelete(w, r, "/api/allowlist/", "protection_allow_entries")
}

func (s *Handler) manualBlocklist(w http.ResponseWriter, r *http.Request) {
	s.domainCollection(w, r, "protection_block_entries")
}

func (s *Handler) manualBlockEntry(w http.ResponseWriter, r *http.Request) {
	s.domainDelete(w, r, "/api/blocklist-domains/", "protection_block_entries")
}

func (s *Handler) domainCollection(w http.ResponseWriter, r *http.Request, table string) {
	protectionID := scalarInt(r.Context(), s.store.DB, `SELECT id FROM protection_profiles WHERE is_default = 1`)
	switch r.Method {
	case http.MethodGet:
		rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id, domain, created_at FROM `+table+` WHERE protection_id = ? ORDER BY domain`, protectionID)
		if err != nil {
			writeError(w, err)
			return
		}
		defer closeRows(rows)
		writeRows(w, rows)
	case http.MethodPost:
		s.configMu.Lock()
		defer s.configMu.Unlock()
		var input domainInput
		if !decode(w, r, &input) {
			return
		}
		domain, err := db.NormalizeDomain(input.Domain)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		result, err := s.store.DB.ExecContext(r.Context(), `INSERT OR IGNORE INTO `+table+`(protection_id, domain) VALUES(?, ?)`, protectionID, domain)
		if err != nil {
			writeError(w, err)
			return
		}
		inserted, _ := result.RowsAffected()
		id, _ := result.LastInsertId()
		if inserted > 0 {
			if err := s.reloader.Apply(r.Context()); err != nil {
				rollbackCtx := context.WithoutCancel(r.Context())
				_, _ = s.store.DB.ExecContext(rollbackCtx, `DELETE FROM `+table+` WHERE id = ? AND protection_id = ?`, id, protectionID)
				_ = s.reloader.Apply(rollbackCtx)
				writeError(w, fmt.Errorf("rule was not saved because CoreDNS rejected the configuration: %w", err))
				return
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) domainDelete(w http.ResponseWriter, r *http.Request, prefix, table string) {
	id, ok := idFromPath(w, r, prefix)
	if !ok {
		return
	}
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	protectionID := scalarInt(r.Context(), s.store.DB, `SELECT id FROM protection_profiles WHERE is_default = 1`)
	var domain, createdAt string
	if err := s.store.DB.QueryRowContext(r.Context(), `SELECT domain, created_at FROM `+table+` WHERE id = ? AND protection_id = ?`, id, protectionID).Scan(&domain, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeBadRequest(w, errors.New("rule does not exist"))
		} else {
			writeError(w, err)
		}
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE id = ? AND protection_id = ?`, id, protectionID); err != nil {
		writeError(w, err)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(r.Context())
		_, _ = s.store.DB.ExecContext(rollbackCtx, `INSERT INTO `+table+`(id, protection_id, domain, created_at) VALUES(?, ?, ?, ?)`, id, protectionID, domain, createdAt)
		_ = s.reloader.Apply(rollbackCtx)
		writeError(w, fmt.Errorf("rule was not deleted because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

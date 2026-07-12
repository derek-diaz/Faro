package handlers

import (
	"errors"
	"fmt"
	"github.com/derek/faro/internal/db"
	"net/http"
	"strconv"
	"strings"
)

func (s *Handler) blocklists(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := s.store.DB.QueryContext(r.Context(), `
			SELECT b.id, b.name, b.url, b.enabled, b.last_refreshed_at, b.created_at, b.updated_at, COUNT(e.id) AS entry_count
			FROM blocklists b
			LEFT JOIN blocklist_entries e ON e.blocklist_id = b.id
			GROUP BY b.id
			ORDER BY b.name
		`)
		if err != nil {
			writeError(w, err)
			return
		}
		defer rows.Close()
		writeRows(w, rows)
	case http.MethodPost:
		var input blocklistInput
		if !decode(w, r, &input) {
			return
		}
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.URL) == "" {
			writeBadRequest(w, errors.New("name and url are required"))
			return
		}
		enabled := boolInt(input.Enabled == nil || *input.Enabled)
		result, err := s.store.DB.ExecContext(r.Context(), `INSERT INTO blocklists(name, url, enabled) VALUES(?, ?, ?)`, strings.TrimSpace(input.Name), strings.TrimSpace(input.URL), enabled)
		if err != nil {
			writeError(w, err)
			return
		}
		id, _ := result.LastInsertId()
		s.recordEvent(r.Context(), eventInput{
			Type:        "blocklist.installed",
			Severity:    "success",
			Title:       "Blocklist installed",
			Description: strings.TrimSpace(input.Name) + " is ready to use.",
			Metadata:    map[string]any{"blocklist_id": id, "url": strings.TrimSpace(input.URL)},
			Source:      "blocklists",
		})
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) blocklist(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/blocklists/")
	if strings.Trim(path, "/") == "refresh" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id FROM blocklists WHERE enabled = 1 ORDER BY id`)
		if err != nil {
			writeError(w, err)
			return
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				writeError(w, err)
				return
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			writeError(w, err)
			return
		}

		totalEntries := 0
		for _, id := range ids {
			count, err := s.refresher.Refresh(r.Context(), id)
			if err != nil {
				writeError(w, fmt.Errorf("refresh blocklist %d: %w", id, err))
				return
			}
			totalEntries += count
		}
		if len(ids) > 0 {
			if err := s.reloader.Apply(r.Context()); err != nil {
				writeError(w, err)
				return
			}
		}
		s.recordEvent(r.Context(), eventInput{
			Type:        "blocklist.updated",
			Severity:    "success",
			Title:       "Blocklists updated",
			Description: fmt.Sprintf("Refreshed %d enabled lists with %d domains.", len(ids), totalEntries),
			Metadata:    map[string]any{"blocklist_count": len(ids), "entry_count": totalEntries},
			Source:      "blocklists",
		})
		writeJSON(w, http.StatusOK, map[string]any{"updated": len(ids), "entry_count": totalEntries})
		return
	}
	if strings.HasSuffix(path, "/refresh") {
		idText := strings.TrimSuffix(path, "/refresh")
		id, err := strconv.ParseInt(strings.Trim(idText, "/"), 10, 64)
		if err != nil {
			writeBadRequest(w, errors.New("invalid id"))
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		count, err := s.refresher.Refresh(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
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
		return
	}

	id, err := strconv.ParseInt(strings.Trim(path, "/"), 10, 64)
	if err != nil {
		writeBadRequest(w, errors.New("invalid id"))
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input blocklistInput
		if !decode(w, r, &input) {
			return
		}
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.URL) == "" {
			writeBadRequest(w, errors.New("name and url are required"))
			return
		}
		enabled := boolInt(input.Enabled == nil || *input.Enabled)
		if _, err := s.store.DB.ExecContext(r.Context(), `UPDATE blocklists SET name = ?, url = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, strings.TrimSpace(input.Name), strings.TrimSpace(input.URL), enabled, id); err != nil {
			writeError(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		if _, err := s.store.DB.ExecContext(r.Context(), `DELETE FROM blocklists WHERE id = ?`, id); err != nil {
			writeError(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) allowlist(w http.ResponseWriter, r *http.Request) {
	s.domainCollection(w, r, "allowlist_entries")
}

func (s *Handler) allowlistEntry(w http.ResponseWriter, r *http.Request) {
	s.domainDelete(w, r, "/api/allowlist/", "allowlist_entries")
}

func (s *Handler) manualBlocklist(w http.ResponseWriter, r *http.Request) {
	s.domainCollection(w, r, "manual_block_entries")
}

func (s *Handler) manualBlockEntry(w http.ResponseWriter, r *http.Request) {
	s.domainDelete(w, r, "/api/blocklist-domains/", "manual_block_entries")
}

func (s *Handler) domainCollection(w http.ResponseWriter, r *http.Request, table string) {
	switch r.Method {
	case http.MethodGet:
		rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id, domain, created_at FROM `+table+` ORDER BY domain`)
		if err != nil {
			writeError(w, err)
			return
		}
		defer rows.Close()
		writeRows(w, rows)
	case http.MethodPost:
		var input domainInput
		if !decode(w, r, &input) {
			return
		}
		domain, err := db.NormalizeDomain(input.Domain)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		result, err := s.store.DB.ExecContext(r.Context(), `INSERT OR IGNORE INTO `+table+`(domain) VALUES(?)`, domain)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		id, _ := result.LastInsertId()
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
	if _, err := s.store.DB.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE id = ?`, id); err != nil {
		writeError(w, err)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

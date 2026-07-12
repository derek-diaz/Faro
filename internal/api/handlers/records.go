package handlers

import (
	"fmt"
	"github.com/derek/faro/internal/db"
	"net/http"
	"strings"
)

func (s *Handler) dnsRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id, hostname, type, value, description, created_at, updated_at FROM dns_records ORDER BY hostname`)
		if err != nil {
			writeError(w, err)
			return
		}
		defer rows.Close()
		writeRows(w, rows)
	case http.MethodPost:
		var input dnsRecordInput
		if !decode(w, r, &input) {
			return
		}
		host, typ, value, err := db.NormalizeRecord(input.Hostname, input.Type, input.Value)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		result, err := s.store.DB.ExecContext(r.Context(), `INSERT INTO dns_records(hostname, type, value, description) VALUES(?, ?, ?, ?)`, host, typ, value, strings.TrimSpace(input.Description))
		if err != nil {
			writeError(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
			writeError(w, fmt.Errorf("record saved but CoreDNS reload failed: %w", err))
			return
		}
		id, _ := result.LastInsertId()
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) dnsRecord(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(w, r, "/api/dns-records/")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input dnsRecordInput
		if !decode(w, r, &input) {
			return
		}
		host, typ, value, err := db.NormalizeRecord(input.Hostname, input.Type, input.Value)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		if _, err := s.store.DB.ExecContext(r.Context(), `UPDATE dns_records SET hostname = ?, type = ?, value = ?, description = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, host, typ, value, strings.TrimSpace(input.Description), id); err != nil {
			writeError(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		if _, err := s.store.DB.ExecContext(r.Context(), `DELETE FROM dns_records WHERE id = ?`, id); err != nil {
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

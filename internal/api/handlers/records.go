package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/derek/faro/internal/db"
)

func (s *Handler) dnsRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id, hostname, type, value, description, created_at, updated_at FROM dns_records ORDER BY hostname, type, value`)
		if err != nil {
			writeError(w, err)
			return
		}
		defer logActionError("close DNS record rows", rows.Close)
		writeRows(w, rows)
	case http.MethodPost:
		s.configMu.Lock()
		defer s.configMu.Unlock()
		var input dnsRecordInput
		if !decode(w, r, &input) {
			return
		}
		host, typ, value, err := s.normalizeLocalRecord(r, input)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		result, err := s.store.DB.ExecContext(r.Context(), `INSERT INTO dns_records(hostname, type, value, description) VALUES(?, ?, ?, ?)`, host, typ, value, strings.TrimSpace(input.Description))
		if err != nil {
			writeError(w, err)
			return
		}
		id, _ := result.LastInsertId()
		if err := s.reloader.Apply(r.Context()); err != nil {
			rollbackCtx := context.WithoutCancel(r.Context())
			_, _ = s.store.DB.ExecContext(rollbackCtx, `DELETE FROM dns_records WHERE id = ?`, id)
			_ = s.reloader.Apply(rollbackCtx)
			writeError(w, fmt.Errorf("record was not saved because CoreDNS rejected the configuration: %w", err))
			return
		}
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
		s.updateDNSRecord(w, r, id)
	case http.MethodDelete:
		s.deleteDNSRecord(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) updateDNSRecord(w http.ResponseWriter, r *http.Request, id int64) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var input dnsRecordInput
	if !decode(w, r, &input) {
		return
	}
	host, typ, value, err := s.normalizeLocalRecord(r, input)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	previous, err := s.readDNSRecord(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `UPDATE dns_records SET hostname = ?, type = ?, value = ?, description = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, host, typ, value, strings.TrimSpace(input.Description), id); err != nil {
		writeError(w, err)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(r.Context())
		s.restoreDNSRecord(rollbackCtx, previous)
		_ = s.reloader.Apply(rollbackCtx)
		writeError(w, fmt.Errorf("record was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Handler) deleteDNSRecord(w http.ResponseWriter, r *http.Request, id int64) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	previous, err := s.readDNSRecord(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `DELETE FROM dns_records WHERE id = ?`, id); err != nil {
		writeError(w, err)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(r.Context())
		s.restoreDNSRecord(rollbackCtx, previous)
		_ = s.reloader.Apply(rollbackCtx)
		writeError(w, fmt.Errorf("record was not deleted because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type storedDNSRecord struct {
	ID          int64
	Hostname    string
	Type        string
	Value       string
	Description string
	CreatedAt   string
	UpdatedAt   string
}

func (s *Handler) normalizeLocalRecord(r *http.Request, input dnsRecordInput) (string, string, string, error) {
	hostname := strings.TrimSpace(input.Hostname)
	if !strings.Contains(strings.TrimSuffix(hostname, "."), ".") {
		suffix := strings.Trim(strings.TrimSpace(settingValue(r.Context(), s.store.DB, "local_domain_suffix")), ".")
		if suffix != "" {
			hostname = strings.TrimSuffix(hostname, ".") + "." + suffix
		}
	}
	return db.NormalizeRecord(hostname, input.Type, input.Value)
}

func (s *Handler) readDNSRecord(ctx context.Context, id int64) (storedDNSRecord, error) {
	var record storedDNSRecord
	err := s.store.DB.QueryRowContext(ctx, `SELECT id, hostname, type, value, description, created_at, updated_at FROM dns_records WHERE id = ?`, id).
		Scan(&record.ID, &record.Hostname, &record.Type, &record.Value, &record.Description, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return record, fmt.Errorf("DNS record %d does not exist", id)
	}
	return record, err
}

func (s *Handler) restoreDNSRecord(ctx context.Context, record storedDNSRecord) {
	_, _ = s.store.DB.ExecContext(ctx, `
		INSERT INTO dns_records(id, hostname, type, value, description, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET hostname=excluded.hostname, type=excluded.type, value=excluded.value,
			description=excluded.description, created_at=excluded.created_at, updated_at=excluded.updated_at
	`, record.ID, record.Hostname, record.Type, record.Value, record.Description, record.CreatedAt, record.UpdatedAt)
}

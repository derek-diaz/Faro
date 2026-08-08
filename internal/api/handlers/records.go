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

func (handler *Handler) dnsRecords(responseWriter http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		rows, err := handler.store.DB.QueryContext(request.Context(), `SELECT id, hostname, type, value, description, created_at, updated_at FROM dns_records ORDER BY hostname, type, value`)
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		defer logActionError("close DNS record rows", rows.Close)
		writeRows(responseWriter, rows)
	case http.MethodPost:
		handler.configMu.Lock()
		defer handler.configMu.Unlock()
		var input dnsRecordInput
		if !decode(responseWriter, request, &input) {
			return
		}
		host, typ, value, err := handler.normalizeLocalRecord(request, input)
		if err != nil {
			writeBadRequest(responseWriter, err)
			return
		}
		result, err := handler.store.DB.ExecContext(request.Context(), `INSERT INTO dns_records(hostname, type, value, description) VALUES(?, ?, ?, ?)`, host, typ, value, strings.TrimSpace(input.Description))
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		id, _ := result.LastInsertId()
		if err := handler.reloader.Apply(request.Context()); err != nil {
			rollbackCtx := context.WithoutCancel(request.Context())
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `DELETE FROM dns_records WHERE id = ?`, id)
			_ = handler.reloader.Apply(rollbackCtx)
			writeError(responseWriter, fmt.Errorf("record was not saved because CoreDNS rejected the configuration: %w", err))
			return
		}
		writeJSON(responseWriter, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(responseWriter)
	}
}

func (handler *Handler) dnsRecord(responseWriter http.ResponseWriter, request *http.Request) {
	id, ok := idFromPath(responseWriter, request, "/api/dns-records/")
	if !ok {
		return
	}
	switch request.Method {
	case http.MethodPut:
		handler.updateDNSRecord(responseWriter, request, id)
	case http.MethodDelete:
		handler.deleteDNSRecord(responseWriter, request, id)
	default:
		methodNotAllowed(responseWriter)
	}
}

func (handler *Handler) updateDNSRecord(responseWriter http.ResponseWriter, request *http.Request, id int64) {
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	var input dnsRecordInput
	if !decode(responseWriter, request, &input) {
		return
	}
	host, typ, value, err := handler.normalizeLocalRecord(request, input)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	previous, err := handler.readDNSRecord(request.Context(), id)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `UPDATE dns_records SET hostname = ?, type = ?, value = ?, description = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, host, typ, value, strings.TrimSpace(input.Description), id); err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		handler.restoreDNSRecord(rollbackCtx, previous)
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("record was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) deleteDNSRecord(responseWriter http.ResponseWriter, request *http.Request, id int64) {
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	previous, err := handler.readDNSRecord(request.Context(), id)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `DELETE FROM dns_records WHERE id = ?`, id); err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		handler.restoreDNSRecord(rollbackCtx, previous)
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("record was not deleted because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
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

func (handler *Handler) normalizeLocalRecord(request *http.Request, input dnsRecordInput) (string, string, string, error) {
	hostname := strings.TrimSpace(input.Hostname)
	if !strings.Contains(strings.TrimSuffix(hostname, "."), ".") {
		suffix := strings.Trim(strings.TrimSpace(settingValue(request.Context(), handler.store.DB, "local_domain_suffix")), ".")
		if suffix != "" {
			hostname = strings.TrimSuffix(hostname, ".") + "." + suffix
		}
	}
	return db.NormalizeRecord(hostname, input.Type, input.Value)
}

func (handler *Handler) readDNSRecord(ctx context.Context, id int64) (storedDNSRecord, error) {
	var record storedDNSRecord
	err := handler.store.DB.QueryRowContext(ctx, `SELECT id, hostname, type, value, description, created_at, updated_at FROM dns_records WHERE id = ?`, id).
		Scan(&record.ID, &record.Hostname, &record.Type, &record.Value, &record.Description, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return record, fmt.Errorf("DNS record %d does not exist", id)
	}
	return record, err
}

func (handler *Handler) restoreDNSRecord(ctx context.Context, record storedDNSRecord) {
	_, _ = handler.store.DB.ExecContext(ctx, `
		INSERT INTO dns_records(id, hostname, type, value, description, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET hostname=excluded.hostname, type=excluded.type, value=excluded.value,
			description=excluded.description, created_at=excluded.created_at, updated_at=excluded.updated_at
	`, record.ID, record.Hostname, record.Type, record.Value, record.Description, record.CreatedAt, record.UpdatedAt)
}

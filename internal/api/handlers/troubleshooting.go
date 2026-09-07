package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
)

type trialEntry struct {
	DeviceID       int64  `json:"device_id"`
	ProtectionName string `json:"protection_name"`
	ID             int64  `json:"id"`
	Token          string `json:"token"`
	ClientIP       string `json:"client_ip"`
	ProtectionID   int64  `json:"protection_id"`
	Domain         string `json:"domain"`
	ExpiresAt      string `json:"expires_at"`
}

type troubleshootingInput struct {
	DeviceID     int64    `json:"device_id"`
	Action       string   `json:"action"`
	ClientIP     string   `json:"client_ip"`
	ProtectionID int64    `json:"protection_id"`
	Domains      []string `json:"domains"`
	Token        string   `json:"token"`
}

type trialReplacedBlock struct {
	ID, ProtectionID  int64
	Domain, CreatedAt string
}

func (handler *Handler) troubleshooting(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		handler.readTroubleshooting(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	var input troubleshootingInput
	if !decode(w, r, &input) {
		return
	}
	if input.Action != "test" && input.Action != "keep" && input.Action != "undo" {
		writeBadRequest(w, errors.New("choose test, keep, or undo"))
		return
	}
	if input.Action == "test" {
		handler.startTroubleshooting(w, r, input)
		return
	}
	handler.finishTroubleshooting(w, r, input)
}

func (handler *Handler) readTroubleshooting(w http.ResponseWriter, r *http.Request) {
	ip := net.ParseIP(r.URL.Query().Get("client_ip"))
	if ip == nil {
		writeBadRequest(w, errors.New("select a device"))
		return
	}
	clientIP := ip.String()
	var deviceID int64
	if err := handler.store.DB.QueryRowContext(r.Context(), `SELECT device_id FROM device_addresses WHERE address = ?`, clientIP).Scan(&deviceID); err != nil {
		writeBadRequest(w, errors.New("device is no longer in the inventory"))
		return
	}
	since := time.Now().UTC().Add(-15 * time.Minute)
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || parsed.Before(time.Now().Add(-24*time.Hour)) || parsed.After(time.Now().Add(time.Minute)) {
			writeBadRequest(w, errors.New("choose a capture time within the last 24 hours"))
			return
		}
		since = parsed.UTC()
	}
	rows, err := handler.store.DB.QueryContext(r.Context(), `SELECT domain, COUNT(*), SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), SUM(CASE WHEN rcode IN ('SERVFAIL','REFUSED') THEN 1 ELSE 0 END), MAX(timestamp)
		FROM dns_queries WHERE device_id = ? AND timestamp >= ? GROUP BY domain
		ORDER BY SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END) DESC, MAX(timestamp) DESC LIMIT 101`, deviceID, since.Format(time.RFC3339Nano))
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]map[string]any, 0)
	for rows.Next() {
		var domain, last string
		var total, blocked, failed int
		if err := rows.Scan(&domain, &total, &blocked, &failed, &last); err != nil {
			rows.Close()
			writeError(w, err)
			return
		}
		items = append(items, map[string]any{"domain": domain, "requests": total, "blocked": blocked, "failed": failed, "last_seen": last})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, err)
		return
	}
	truncated := len(items) > 100
	if truncated {
		items = items[:100]
	}
	for _, item := range items {
		item["decision"] = coredns.ExplainDomainForClient(r.Context(), handler.store, item["domain"].(string), clientIP)
	}
	protectionID, name, _ := protectionForClient(r.Context(), handler.store.DB, clientIP)
	trials, err := handler.trialEntries(r.Context(), "t.device_id = ?", deviceID)
	if err != nil {
		writeError(w, err)
		return
	}
	var role string
	if err := handler.store.DB.QueryRowContext(r.Context(), `SELECT role FROM redundancy_state WHERE id = 1`).Scan(&role); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client_ip": clientIP, "device_id": deviceID, "protection_id": protectionID, "protection_name": name, "since": since.Format(time.RFC3339Nano), "items": items, "trials": trials, "truncated": truncated, "temporary_tests_available": role == "standalone"})
}

func (handler *Handler) startTroubleshooting(w http.ResponseWriter, r *http.Request, input troubleshootingInput) {
	ip := net.ParseIP(input.ClientIP)
	if ip == nil || len(input.Domains) == 0 || len(input.Domains) > 20 {
		writeBadRequest(w, errors.New("select a device and between 1 and 20 exact domains"))
		return
	}
	clientIP := ip.String()
	var deviceID int64
	if err := handler.store.DB.QueryRowContext(r.Context(), `SELECT device_id FROM device_addresses WHERE address = ?`, clientIP).Scan(&deviceID); err != nil {
		writeBadRequest(w, errors.New("device is no longer in the inventory"))
		return
	}
	protectionID, _, _ := protectionForClient(r.Context(), handler.store.DB, clientIP)
	if deviceID != input.DeviceID || protectionID == 0 || protectionID != input.ProtectionID {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This device's protection changed. Refresh the investigation before testing."})
		return
	}
	var role string
	if err := handler.store.DB.QueryRowContext(r.Context(), `SELECT role FROM redundancy_state WHERE id = 1`).Scan(&role); err != nil {
		writeError(w, err)
		return
	}
	if role != "standalone" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Temporary tests require standalone Faro. A disconnected replica cannot guarantee expiry. You can still inspect requests and edit Protection."})
		return
	}
	domains := make([]string, 0, len(input.Domains))
	seen := map[string]bool{}
	for _, raw := range input.Domains {
		domain, err := db.NormalizeDomain(raw)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		if !seen[domain] {
			domains = append(domains, domain)
			seen[domain] = true
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		writeError(w, err)
		return
	}
	token := hex.EncodeToString(random[:])
	expires := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)
	tx, err := handler.store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback()
	// Expired trials are kept briefly so they can still be explicitly kept or undone.
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM troubleshooting_exceptions WHERE julianday(expires_at) < julianday('now','-1 day')`); err != nil {
		writeError(w, err)
		return
	}
	var active int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(DISTINCT token) FROM troubleshooting_exceptions WHERE julianday(expires_at) > julianday('now')`).Scan(&active); err != nil {
		writeError(w, err)
		return
	}
	if active >= 10 {
		writeBadRequest(w, errors.New("finish or undo an existing test before starting another"))
		return
	}
	for _, domain := range domains {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO troubleshooting_exceptions(token,client_ip,device_id,protection_id,domain,expires_at) VALUES(?,?,?,?,?,?)`, token, clientIP, deviceID, protectionID, domain, expires); err != nil {
			writeError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, err)
		return
	}
	if err := handler.reloader.Apply(r.Context()); err != nil {
		ctx := context.WithoutCancel(r.Context())
		_, rollbackErr := handler.store.DB.ExecContext(ctx, `DELETE FROM troubleshooting_exceptions WHERE token = ?`, token)
		if rollbackErr == nil {
			rollbackErr = handler.reloader.Apply(ctx)
		}
		writeError(w, fmt.Errorf("temporary test was not applied: %w", errors.Join(err, rollbackErr)))
		return
	}
	handler.recordEvent(r.Context(), eventInput{Type: "troubleshooting.started", Severity: "info", Title: "Temporary domain test started", Description: "Selected domains are allowed for ten minutes in this protection.", ClientIP: clientIP, Source: "protection"})
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires_at": expires})
}

func (handler *Handler) trialEntries(ctx context.Context, where string, arg any) ([]trialEntry, error) {
	rows, err := handler.store.DB.QueryContext(ctx, `SELECT t.id, t.token, t.client_ip, t.device_id, t.protection_id, p.name, t.domain, t.expires_at FROM troubleshooting_exceptions t JOIN protection_profiles p ON p.id = t.protection_id WHERE `+where+` ORDER BY t.id`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]trialEntry, 0)
	for rows.Next() {
		var item trialEntry
		if err := rows.Scan(&item.ID, &item.Token, &item.ClientIP, &item.DeviceID, &item.ProtectionID, &item.ProtectionName, &item.Domain, &item.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (handler *Handler) finishTroubleshooting(w http.ResponseWriter, r *http.Request, input troubleshootingInput) {
	if len(input.Token) != 32 || strings.TrimSpace(input.Token) != input.Token {
		writeBadRequest(w, errors.New("invalid test identifier"))
		return
	}
	entries, err := handler.trialEntries(r.Context(), "t.token = ?", input.Token)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This test has already ended. Refresh the investigation."})
		return
	}
	tx, err := handler.store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback()
	var inserted []int64
	var replaced []trialReplacedBlock
	if input.Action == "keep" {
		for _, entry := range entries {
			block := trialReplacedBlock{ProtectionID: entry.ProtectionID, Domain: entry.Domain}
			err := tx.QueryRowContext(r.Context(), `SELECT id, created_at FROM protection_block_entries WHERE protection_id = ? AND domain = ?`, entry.ProtectionID, entry.Domain).Scan(&block.ID, &block.CreatedAt)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				writeError(w, err)
				return
			}
			if err == nil {
				if _, err := tx.ExecContext(r.Context(), `DELETE FROM protection_block_entries WHERE id = ?`, block.ID); err != nil {
					writeError(w, err)
					return
				}
				replaced = append(replaced, block)
			}
			result, err := tx.ExecContext(r.Context(), `INSERT OR IGNORE INTO protection_allow_entries(protection_id,domain) VALUES(?,?)`, entry.ProtectionID, entry.Domain)
			if err != nil {
				writeError(w, err)
				return
			}
			if count, _ := result.RowsAffected(); count > 0 {
				id, _ := result.LastInsertId()
				inserted = append(inserted, id)
			}
		}
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM troubleshooting_exceptions WHERE token = ?`, input.Token); err != nil {
		writeError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, err)
		return
	}
	if err := handler.reloader.Apply(r.Context()); err != nil {
		ctx := context.WithoutCancel(r.Context())
		rollbackErr := handler.restoreTrial(ctx, entries, inserted, replaced)
		if rollbackErr == nil {
			rollbackErr = handler.reloader.Apply(ctx)
		}
		writeError(w, fmt.Errorf("test change was not applied: %w", errors.Join(err, rollbackErr)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (handler *Handler) restoreTrial(ctx context.Context, entries []trialEntry, inserted []int64, replaced []trialReplacedBlock) error {
	tx, err := handler.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, block := range replaced {
		if _, err := tx.ExecContext(ctx, `INSERT INTO protection_block_entries(id, protection_id, domain, created_at) VALUES(?,?,?,?)`, block.ID, block.ProtectionID, block.Domain, block.CreatedAt); err != nil {
			return err
		}
	}
	for _, id := range inserted {
		if _, err = tx.ExecContext(ctx, `DELETE FROM protection_allow_entries WHERE id = ?`, id); err != nil {
			return err
		}
	}
	for _, e := range entries {
		if _, err = tx.ExecContext(ctx, `INSERT INTO troubleshooting_exceptions(id,token,client_ip,device_id,protection_id,domain,expires_at) VALUES(?,?,?,?,?,?,?)`, e.ID, e.Token, e.ClientIP, e.DeviceID, e.ProtectionID, e.Domain, e.ExpiresAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

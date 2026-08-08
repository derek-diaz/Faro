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

func (handler *Handler) blocklists(responseWriter http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		handler.listBlocklists(responseWriter, request)
	case http.MethodPost:
		handler.createBlocklist(responseWriter, request)
	default:
		methodNotAllowed(responseWriter)
	}
}

func (handler *Handler) listBlocklists(responseWriter http.ResponseWriter, request *http.Request) {
	rows, err := handler.store.DB.QueryContext(request.Context(), `
		SELECT b.id, b.name, b.url, b.enabled, b.last_refreshed_at, b.created_at, b.updated_at,
			COUNT(e.id) AS entry_count,
			(SELECT COUNT(*) FROM protection_blocklists pb WHERE pb.blocklist_id = b.id) AS protection_count
		FROM blocklists b
		LEFT JOIN blocklist_entries e ON e.blocklist_id = b.id
		GROUP BY b.id, b.name, b.url, b.enabled, b.last_refreshed_at, b.created_at, b.updated_at
		ORDER BY b.name
	`)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	defer closeRows(rows)
	writeRows(responseWriter, rows)
}

func (handler *Handler) createBlocklist(responseWriter http.ResponseWriter, request *http.Request) {
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	var input blocklistInput
	if !decode(responseWriter, request, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || strings.TrimSpace(input.URL) == "" {
		writeBadRequest(responseWriter, errors.New("name and url are required"))
		return
	}
	sourceURL, err := normalizeBlocklistURL(input.URL)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	var existingID int64
	err = handler.store.DB.QueryRowContext(request.Context(), `
		SELECT id FROM blocklists
		WHERE lower(name) = lower(?) OR lower(url) = lower(?)
		LIMIT 1
	`, name, sourceURL).Scan(&existingID)
	if err == nil {
		writeJSON(responseWriter, http.StatusConflict, map[string]any{"error": "that blocklist is already installed"})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(responseWriter, err)
		return
	}
	enabled := boolInt(input.Enabled == nil || *input.Enabled)
	result, err := handler.store.DB.ExecContext(request.Context(), `INSERT INTO blocklists(name, url, enabled) VALUES(?, ?, ?)`, name, sourceURL, enabled)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	assignToDefault := input.AssignToDefault == nil || *input.AssignToDefault
	if enabled == 1 && assignToDefault {
		if _, err := handler.store.DB.ExecContext(request.Context(), `
			INSERT OR IGNORE INTO protection_blocklists(protection_id, blocklist_id)
			SELECT id, ? FROM protection_profiles WHERE is_default = 1
		`, id); err != nil {
			_, _ = handler.store.DB.ExecContext(context.WithoutCancel(request.Context()), `DELETE FROM blocklists WHERE id = ?`, id)
			writeError(responseWriter, err)
			return
		}
	}
	handler.recordEvent(request.Context(), eventInput{
		Type:        "blocklist.installed",
		Severity:    "success",
		Title:       "Blocklist installed",
		Description: name + " is ready to use.",
		Metadata:    map[string]any{"blocklist_id": id, "url": strings.TrimSpace(input.URL)},
		Source:      "blocklists",
	})
	writeJSON(responseWriter, http.StatusCreated, map[string]any{"id": id})
}

func (handler *Handler) blocklist(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodDelete {
		handler.configMu.Lock()
		defer handler.configMu.Unlock()
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/blocklists/")
	if strings.Trim(path, "/") == "refresh" {
		handler.refreshAllBlocklists(responseWriter, request)
		return
	}
	if idText, ok := strings.CutSuffix(path, "/refresh"); ok {
		handler.refreshBlocklist(responseWriter, request, idText)
		return
	}

	id, err := strconv.ParseInt(strings.Trim(path, "/"), 10, 64)
	if err != nil {
		writeBadRequest(responseWriter, errors.New("invalid id"))
		return
	}
	switch request.Method {
	case http.MethodPut:
		handler.updateBlocklist(responseWriter, request, id)
	case http.MethodDelete:
		handler.deleteBlocklist(responseWriter, request, id)
	default:
		methodNotAllowed(responseWriter)
	}
}

func (handler *Handler) refreshAllBlocklists(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	rows, err := handler.store.DB.QueryContext(request.Context(), `SELECT id FROM blocklists WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	defer closeRows(rows)
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			writeError(responseWriter, err)
			return
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		writeError(responseWriter, err)
		return
	}
	totalEntries := 0
	updated := 0
	for _, id := range ids {
		count, err := handler.refresher.RefreshAndApply(request.Context(), id, handler.reloader.Apply)
		if err != nil {
			writeError(responseWriter, fmt.Errorf("refresh blocklist %d: %w", id, err))
			return
		}
		totalEntries += count
		updated++
	}
	handler.recordEvent(request.Context(), eventInput{
		Type:        "blocklist.updated",
		Severity:    "success",
		Title:       "Blocklists updated",
		Description: fmt.Sprintf("Refreshed %d enabled lists with %d domains.", updated, totalEntries),
		Metadata:    map[string]any{"blocklist_count": updated, "entry_count": totalEntries},
		Source:      "blocklists",
	})
	writeJSON(responseWriter, http.StatusOK, map[string]any{"updated": updated, "entry_count": totalEntries})
}

func (handler *Handler) refreshBlocklist(responseWriter http.ResponseWriter, request *http.Request, idText string) {
	id, err := strconv.ParseInt(strings.Trim(idText, "/"), 10, 64)
	if err != nil {
		writeBadRequest(responseWriter, errors.New("invalid id"))
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	count, err := handler.refresher.RefreshAndApply(request.Context(), id, handler.reloader.Apply)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	handler.recordEvent(request.Context(), eventInput{
		Type:        "blocklist.updated",
		Severity:    "success",
		Title:       "Blocklist updated",
		Description: fmt.Sprintf("Refreshed %d domains.", count),
		Metadata:    map[string]any{"blocklist_id": id, "entry_count": count},
		Source:      "blocklists",
	})
	writeJSON(responseWriter, http.StatusOK, map[string]any{"entry_count": count})
}

func (handler *Handler) updateBlocklist(responseWriter http.ResponseWriter, request *http.Request, id int64) {
	var input blocklistInput
	if !decode(responseWriter, request, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || strings.TrimSpace(input.URL) == "" {
		writeBadRequest(responseWriter, errors.New("name and url are required"))
		return
	}
	sourceURL, err := normalizeBlocklistURL(input.URL)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	enabled := boolInt(input.Enabled == nil || *input.Enabled)
	previous, err := handler.readBlocklist(request.Context(), id)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `UPDATE blocklists SET name = ?, url = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, sourceURL, enabled, id); err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.applyBlocklistChange(request.Context(), previous); err != nil {
		writeError(responseWriter, fmt.Errorf("blocklist was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) deleteBlocklist(responseWriter http.ResponseWriter, request *http.Request, id int64) {
	previous, err := handler.readBlocklist(request.Context(), id)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `UPDATE blocklists SET enabled = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.applyBlocklistChange(request.Context(), previous); err != nil {
		writeError(responseWriter, fmt.Errorf("blocklist was not deleted because CoreDNS rejected the configuration: %w", err))
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `DELETE FROM blocklists WHERE id = ?`, id); err != nil {
		writeError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) applyBlocklistChange(ctx context.Context, previous storedBlocklist) error {
	if err := handler.reloader.Apply(ctx); err != nil {
		rollbackCtx := context.WithoutCancel(ctx)
		handler.restoreBlocklist(rollbackCtx, previous)
		_ = handler.reloader.Apply(rollbackCtx)
		return err
	}
	return nil
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

const domainTablePlaceholder = "faro_domain_table"

const (
	domainListQuery    = `SELECT id, domain, created_at FROM faro_domain_table WHERE protection_id = ? ORDER BY domain`
	domainInsertQuery  = `INSERT OR IGNORE INTO faro_domain_table(protection_id, domain) VALUES(?, ?)`
	domainFindQuery    = `SELECT domain, created_at FROM faro_domain_table WHERE id = ? AND protection_id = ?`
	domainDeleteQuery  = `DELETE FROM faro_domain_table WHERE id = ? AND protection_id = ?`
	domainRestoreQuery = `INSERT INTO faro_domain_table(id, protection_id, domain, created_at) VALUES(?, ?, ?, ?)`
)

type domainQueries struct {
	list    string
	insert  string
	find    string
	delete  string
	restore string
}

func domainQueriesFor(table string) (domainQueries, bool) {
	if table != "protection_allow_entries" && table != "protection_block_entries" {
		return domainQueries{}, false
	}
	replaceTable := func(query string) string {
		return strings.Replace(query, domainTablePlaceholder, table, 1)
	}
	return domainQueries{
		list:    replaceTable(domainListQuery),
		insert:  replaceTable(domainInsertQuery),
		find:    replaceTable(domainFindQuery),
		delete:  replaceTable(domainDeleteQuery),
		restore: replaceTable(domainRestoreQuery),
	}, true
}

func (handler *Handler) readBlocklist(ctx context.Context, id int64) (storedBlocklist, error) {
	var blocklist storedBlocklist
	err := handler.store.DB.QueryRowContext(ctx, `SELECT id, name, url, enabled, last_refreshed_at, created_at, updated_at FROM blocklists WHERE id = ?`, id).
		Scan(&blocklist.ID, &blocklist.Name, &blocklist.URL, &blocklist.Enabled, &blocklist.LastRefreshedAt, &blocklist.CreatedAt, &blocklist.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return blocklist, fmt.Errorf("blocklist %d does not exist", id)
	}
	return blocklist, err
}

func (handler *Handler) restoreBlocklist(ctx context.Context, blocklist storedBlocklist) {
	_, _ = handler.store.DB.ExecContext(ctx, `UPDATE blocklists SET name=?, url=?, enabled=?, last_refreshed_at=?, created_at=?, updated_at=? WHERE id=?`,
		blocklist.Name, blocklist.URL, blocklist.Enabled, blocklist.LastRefreshedAt, blocklist.CreatedAt, blocklist.UpdatedAt, blocklist.ID)
}

func (handler *Handler) allowlist(responseWriter http.ResponseWriter, request *http.Request) {
	handler.domainCollection(responseWriter, request, "protection_allow_entries")
}

func (handler *Handler) allowlistEntry(responseWriter http.ResponseWriter, request *http.Request) {
	handler.domainDelete(responseWriter, request, "/api/allowlist/", "protection_allow_entries")
}

func (handler *Handler) manualBlocklist(responseWriter http.ResponseWriter, request *http.Request) {
	handler.domainCollection(responseWriter, request, "protection_block_entries")
}

func (handler *Handler) manualBlockEntry(responseWriter http.ResponseWriter, request *http.Request) {
	handler.domainDelete(responseWriter, request, "/api/blocklist-domains/", "protection_block_entries")
}

func (handler *Handler) domainCollection(responseWriter http.ResponseWriter, request *http.Request, table string) {
	queries, ok := domainQueriesFor(table)
	if !ok {
		writeError(responseWriter, errors.New("unsupported domain collection"))
		return
	}
	protectionID := scalarInt(request.Context(), handler.store.DB, `SELECT id FROM protection_profiles WHERE is_default = 1`)
	switch request.Method {
	case http.MethodGet:
		rows, err := handler.store.DB.QueryContext(request.Context(), queries.list, protectionID)
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		defer closeRows(rows)
		writeRows(responseWriter, rows)
	case http.MethodPost:
		handler.configMu.Lock()
		defer handler.configMu.Unlock()
		var input domainInput
		if !decode(responseWriter, request, &input) {
			return
		}
		domain, err := db.NormalizeDomain(input.Domain)
		if err != nil {
			writeBadRequest(responseWriter, err)
			return
		}
		result, err := handler.store.DB.ExecContext(request.Context(), queries.insert, protectionID, domain)
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		inserted, _ := result.RowsAffected()
		id, _ := result.LastInsertId()
		if inserted > 0 {
			if err := handler.reloader.Apply(request.Context()); err != nil {
				rollbackCtx := context.WithoutCancel(request.Context())
				_, _ = handler.store.DB.ExecContext(rollbackCtx, queries.delete, id, protectionID)
				_ = handler.reloader.Apply(rollbackCtx)
				writeError(responseWriter, fmt.Errorf("rule was not saved because CoreDNS rejected the configuration: %w", err))
				return
			}
		}
		writeJSON(responseWriter, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(responseWriter)
	}
}

func (handler *Handler) domainDelete(responseWriter http.ResponseWriter, request *http.Request, prefix, table string) {
	queries, ok := domainQueriesFor(table)
	if !ok {
		writeError(responseWriter, errors.New("unsupported domain collection"))
		return
	}
	id, ok := idFromPath(responseWriter, request, prefix)
	if !ok {
		return
	}
	if request.Method != http.MethodDelete {
		methodNotAllowed(responseWriter)
		return
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	protectionID := scalarInt(request.Context(), handler.store.DB, `SELECT id FROM protection_profiles WHERE is_default = 1`)
	var domain, createdAt string
	if err := handler.store.DB.QueryRowContext(request.Context(), queries.find, id, protectionID).Scan(&domain, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeBadRequest(responseWriter, errors.New("rule does not exist"))
		} else {
			writeError(responseWriter, err)
		}
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), queries.delete, id, protectionID); err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		_, _ = handler.store.DB.ExecContext(rollbackCtx, queries.restore, id, protectionID, domain, createdAt)
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("rule was not deleted because CoreDNS rejected the configuration: %w", err))
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

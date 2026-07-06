package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/derek/faro/internal/blocklists"
	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
)

type CoreDNSManager interface {
	Apply(context.Context) error
}

type Server struct {
	store      *db.Store
	reloader   CoreDNSManager
	refresher  blocklists.Refresher
	faviconDir string
}

func NewServer(store *db.Store, reloader CoreDNSManager) http.Handler {
	server := &Server{
		store:      store,
		reloader:   reloader,
		refresher:  blocklists.Refresher{Store: store},
		faviconDir: env("FARO_FAVICON_DIR", "/data/favicons"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.health)
	mux.HandleFunc("/metrics", server.metrics)
	mux.HandleFunc("/api/dns-records", server.dnsRecords)
	mux.HandleFunc("/api/dns-records/", server.dnsRecord)
	mux.HandleFunc("/api/blocklists", server.blocklists)
	mux.HandleFunc("/api/blocklists/", server.blocklist)
	mux.HandleFunc("/api/allowlist", server.allowlist)
	mux.HandleFunc("/api/allowlist/", server.allowlistEntry)
	mux.HandleFunc("/api/blocklist-domains", server.manualBlocklist)
	mux.HandleFunc("/api/blocklist-domains/", server.manualBlockEntry)
	mux.HandleFunc("/api/queries", server.queries)
	mux.HandleFunc("/api/dashboard", server.dashboard)
	mux.HandleFunc("/api/favicons/", server.favicon)
	mux.HandleFunc("/api/settings", server.settings)
	mux.HandleFunc("/api/reload", server.reload)
	return cors(mux)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "faro-api"})
}

func (s *Server) dnsRecords(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) dnsRecord(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) blocklists(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) blocklist(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/blocklists/")
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

func (s *Server) allowlist(w http.ResponseWriter, r *http.Request) {
	s.domainCollection(w, r, "allowlist_entries")
}

func (s *Server) allowlistEntry(w http.ResponseWriter, r *http.Request) {
	s.domainDelete(w, r, "/api/allowlist/", "allowlist_entries")
}

func (s *Server) manualBlocklist(w http.ResponseWriter, r *http.Request) {
	s.domainCollection(w, r, "manual_block_entries")
}

func (s *Server) manualBlockEntry(w http.ResponseWriter, r *http.Request) {
	s.domainDelete(w, r, "/api/blocklist-domains/", "manual_block_entries")
}

func (s *Server) domainCollection(w http.ResponseWriter, r *http.Request, table string) {
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

func (s *Server) domainDelete(w http.ResponseWriter, r *http.Request, prefix, table string) {
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

func (s *Server) queries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	query := `SELECT id, timestamp, client_ip, domain, query_type, action, source, latency_ms FROM dns_queries`
	args := []any{}
	if search != "" {
		query += ` WHERE domain LIKE ? OR client_ip LIKE ?`
		like := "%" + search + "%"
		args = append(args, like, like)
	}
	query += ` ORDER BY timestamp DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.store.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	writeRows(w, rows)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	start := time.Now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339)
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ?`, start)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ? AND action = 'blocked'`, start)
	enabledBlocklists := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklists WHERE enabled = 1`)
	blockEntries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklist_entries`)

	writeJSON(w, http.StatusOK, map[string]any{
		"total_queries_today":      total,
		"blocked_queries_today":    blocked,
		"block_percentage":         percentage(blocked, total),
		"enabled_blocklists":       enabledBlocklists,
		"blocklist_entries":        blockEntries,
		"top_queried_domains":      grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 5`, start),
		"top_blocked_domains":      grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE timestamp >= ? AND action = 'blocked' GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 5`, start),
		"top_clients":              grouped(r.Context(), s.store.DB, `SELECT client_ip, COUNT(*) FROM dns_queries WHERE timestamp >= ? GROUP BY client_ip ORDER BY COUNT(*) DESC LIMIT 5`, start),
		"recent_activity":          recentQueries(r.Context(), s.store.DB),
		"upstream_health":          "Not checked yet",
		"upstream_health_status":   "placeholder",
		"favicon_fetching_enabled": settingValue(r.Context(), s.store.DB, "favicon_fetching_enabled"),
	})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := s.store.DB.QueryContext(r.Context(), `SELECT key, value, updated_at FROM settings ORDER BY key`)
		if err != nil {
			writeError(w, err)
			return
		}
		defer rows.Close()
		writeRows(w, rows)
	case http.MethodPut:
		var input map[string]string
		if !decode(w, r, &input) {
			return
		}
		tx, err := s.store.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback()
		for key, value := range input {
			switch key {
			case "upstream_dns", "local_domain_suffix", "retention_days", "favicon_fetching_enabled":
				if _, err := tx.ExecContext(r.Context(), `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value); err != nil {
					writeError(w, err)
					return
				}
			}
		}
		if err := tx.Commit(); err != nil {
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

func (s *Server) favicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if settingValue(r.Context(), s.store.DB, "favicon_fetching_enabled") != "true" {
		http.NotFound(w, r)
		return
	}
	domain, err := db.NormalizeDomain(strings.TrimPrefix(r.URL.Path, "/api/favicons/"))
	if err != nil || !isSafeFaviconDomain(domain) {
		http.NotFound(w, r)
		return
	}

	localPath, err := s.cachedFaviconPath(r.Context(), domain)
	if err == nil && localPath != "" {
		http.ServeFile(w, r, localPath)
		return
	}

	localPath, err = s.fetchFavicon(r.Context(), domain)
	if err != nil {
		serveFaviconPlaceholder(w, domain)
		return
	}
	http.ServeFile(w, r, localPath)
}

func (s *Server) cachedFaviconPath(ctx context.Context, domain string) (string, error) {
	var localPath string
	err := s.store.DB.QueryRowContext(ctx, `SELECT local_path FROM domain_favicons WHERE domain = ? AND local_path != ''`, domain).Scan(&localPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(localPath); err != nil {
		return "", err
	}
	return localPath, nil
}

func (s *Server) fetchFavicon(ctx context.Context, domain string) (string, error) {
	if err := os.MkdirAll(s.faviconDir, 0o755); err != nil {
		return "", err
	}
	candidates := []string{
		"https://" + domain + "/favicon.ico",
		"https://www." + domain + "/favicon.ico",
	}
	client := http.Client{Timeout: 5 * time.Second}
	for _, candidate := range candidates {
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, candidate, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("User-Agent", "Faro favicon fetcher")
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		contentType := resp.Header.Get("Content-Type")
		if resp.StatusCode < 200 || resp.StatusCode > 299 || !strings.HasPrefix(contentType, "image/") {
			_ = resp.Body.Close()
			cancel()
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		_ = resp.Body.Close()
		cancel()
		if err != nil || len(body) == 0 {
			continue
		}
		localPath := filepath.Join(s.faviconDir, safeFaviconFilename(domain))
		if err := os.WriteFile(localPath, body, 0o644); err != nil {
			return "", err
		}
		if _, err := s.store.DB.ExecContext(ctx, `
			INSERT INTO domain_favicons(domain, favicon_url, local_path, last_checked_at, updated_at)
			VALUES(?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			ON CONFLICT(domain) DO UPDATE SET
				favicon_url = excluded.favicon_url,
				local_path = excluded.local_path,
				last_checked_at = CURRENT_TIMESTAMP,
				updated_at = CURRENT_TIMESTAMP
		`, domain, candidate, localPath); err != nil {
			return "", err
		}
		return localPath, nil
	}
	_, _ = s.store.DB.ExecContext(ctx, `
		INSERT INTO domain_favicons(domain, favicon_url, local_path, last_checked_at, updated_at)
		VALUES(?, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(domain) DO UPDATE SET last_checked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
	`, domain)
	return "", errors.New("favicon not found")
}

var publicDomainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$`)

func isSafeFaviconDomain(domain string) bool {
	if !publicDomainPattern.MatchString(domain) {
		return false
	}
	if strings.HasSuffix(domain, ".home") || strings.HasSuffix(domain, ".local") || strings.HasSuffix(domain, ".lan") {
		return false
	}
	parsed, err := url.Parse("https://" + domain)
	return err == nil && parsed.Hostname() == domain
}

func safeFaviconFilename(domain string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(domain) + ".ico"
}

func serveFaviconPlaceholder(w http.ResponseWriter, domain string) {
	initial := "?"
	if domain != "" {
		initial = strings.ToUpper(domain[:1])
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#e8eef5"/><text x="16" y="21" text-anchor="middle" font-family="Arial, sans-serif" font-size="14" font-weight="700" fill="#617085">%s</text></svg>`, initial)
}

func (s *Server) reload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	reloads, reloadFailures := coredns.ReloadTotals()
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries`)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE action = 'blocked'`)
	enabled := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklists WHERE enabled = 1`)
	entries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklist_entries`)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_queries_total counter\nfaro_dns_queries_total %d\n", total)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_blocked_queries_total counter\nfaro_dns_blocked_queries_total %d\n", blocked)
	_, _ = fmt.Fprintf(w, "# TYPE faro_blocklists_enabled_total gauge\nfaro_blocklists_enabled_total %d\n", enabled)
	_, _ = fmt.Fprintf(w, "# TYPE faro_blocklist_entries_total gauge\nfaro_blocklist_entries_total %d\n", entries)
	_, _ = fmt.Fprintf(w, "# TYPE faro_coredns_reload_total counter\nfaro_coredns_reload_total %d\n", reloads)
	_, _ = fmt.Fprintf(w, "# TYPE faro_coredns_reload_failed_total counter\nfaro_coredns_reload_failed_total %d\n", reloadFailures)
}

type dnsRecordInput struct {
	Hostname    string `json:"hostname"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

type blocklistInput struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

type domainInput struct {
	Domain string `json:"domain"`
}

func writeRows(w http.ResponseWriter, rows *sql.Rows) {
	columns, err := rows.Columns()
	if err != nil {
		writeError(w, err)
		return
	}
	items := []map[string]any{}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			writeError(w, err)
			return
		}
		row := map[string]any{}
		for i, column := range columns {
			switch value := values[i].(type) {
			case []byte:
				row[column] = string(value)
			case int64:
				if column == "enabled" {
					row[column] = value == 1
				} else {
					row[column] = value
				}
			default:
				row[column] = value
			}
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func grouped(ctx context.Context, database *sql.DB, query string, args ...any) []map[string]any {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			return result
		}
		result = append(result, map[string]any{"label": label, "count": count})
	}
	return result
}

func recentQueries(ctx context.Context, database *sql.DB) []map[string]any {
	rows, err := database.QueryContext(ctx, `SELECT timestamp, client_ip, domain, query_type, action, source, latency_ms FROM dns_queries ORDER BY timestamp DESC LIMIT 8`)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	items := []map[string]any{}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return items
		}
		item := map[string]any{}
		for i, column := range columns {
			if bytes, ok := values[i].([]byte); ok {
				item[column] = string(bytes)
			} else {
				item[column] = values[i]
			}
		}
		items = append(items, item)
	}
	return items
}

func scalarInt(ctx context.Context, database *sql.DB, query string, args ...any) int {
	var count int
	_ = database.QueryRowContext(ctx, query, args...).Scan(&count)
	return count
}

func settingValue(ctx context.Context, database *sql.DB, key string) string {
	var value string
	_ = database.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	return value
}

func percentage(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeBadRequest(w, err)
		return false
	}
	return true
}

func idFromPath(w http.ResponseWriter, r *http.Request, prefix string) (int64, bool) {
	id, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/"), 10, 64)
	if err != nil {
		writeBadRequest(w, errors.New("invalid id"))
		return 0, false
	}
	return id, true
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeBadRequest(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

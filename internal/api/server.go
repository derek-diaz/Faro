package api

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	metricsURL string
}

func NewServer(store *db.Store, reloader CoreDNSManager) http.Handler {
	server := &Server{
		store:      store,
		reloader:   reloader,
		refresher:  blocklists.Refresher{Store: store},
		faviconDir: env("FARO_FAVICON_DIR", "/data/favicons"),
		metricsURL: env("FARO_COREDNS_METRICS_URL", "http://coredns:9153/metrics"),
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
	mux.HandleFunc("/api/events", server.events)
	mux.HandleFunc("/api/notifications", server.notifications)
	mux.HandleFunc("/api/upstreams/probe", server.upstreamProbes)
	mux.HandleFunc("/api/devices", server.devices)
	mux.HandleFunc("/api/devices/", server.device)
	mux.HandleFunc("/api/domains/", server.domainSummary)
	mux.HandleFunc("/api/search", server.search)
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

func (s *Server) upstreamProbes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var input struct {
		Addresses []string `json:"addresses"`
	}
	if !decode(w, r, &input) {
		return
	}
	addresses := []string{}
	seen := map[string]bool{}
	for _, rawAddress := range input.Addresses {
		address := strings.TrimSpace(rawAddress)
		if address == "" || seen[address] {
			continue
		}
		if net.ParseIP(address) == nil {
			writeBadRequest(w, fmt.Errorf("invalid upstream IP address: %s", address))
			return
		}
		seen[address] = true
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		writeBadRequest(w, errors.New("at least one upstream IP address is required"))
		return
	}
	if len(addresses) > 32 {
		writeBadRequest(w, errors.New("a maximum of 32 upstreams can be probed at once"))
		return
	}

	type indexedProbe struct {
		index int
		probe map[string]any
	}
	results := make([]map[string]any, len(addresses))
	probes := make(chan indexedProbe, len(addresses))
	for index, address := range addresses {
		go func(index int, address string) {
			probes <- indexedProbe{index: index, probe: probeDNSUpstream(r.Context(), address)}
		}(index, address)
	}
	for range addresses {
		result := <-probes
		results[result.index] = result.probe
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": results})
}

func probeDNSUpstream(ctx context.Context, address string) map[string]any {
	checkedAt := time.Now().UTC()
	result := map[string]any{
		"address":    address,
		"status":     "unavailable",
		"latency_ms": nil,
		"checked_at": checkedAt.Format(time.RFC3339),
	}
	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, "udp", net.JoinHostPort(address, "53"))
	if err != nil {
		result["error"] = compactProbeError(err)
		return result
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(1500 * time.Millisecond))

	queryID := uint16(time.Now().UnixNano())
	query := dnsProbeQuery(queryID, "example.com")
	started := time.Now()
	if _, err := connection.Write(query); err != nil {
		result["error"] = compactProbeError(err)
		return result
	}
	response := make([]byte, 1232)
	n, err := connection.Read(response)
	if err != nil {
		result["error"] = compactProbeError(err)
		return result
	}
	if n < 12 || binary.BigEndian.Uint16(response[0:2]) != queryID || response[2]&0x80 == 0 {
		result["error"] = "invalid DNS response"
		return result
	}
	latency := float64(time.Since(started).Microseconds()) / 1000
	result["status"] = "online"
	result["latency_ms"] = float64(int(latency*10+0.5)) / 10
	delete(result, "error")
	return result
}

func dnsProbeQuery(id uint16, hostname string) []byte {
	packet := make([]byte, 12)
	binary.BigEndian.PutUint16(packet[0:2], id)
	binary.BigEndian.PutUint16(packet[2:4], 0x0100)
	binary.BigEndian.PutUint16(packet[4:6], 1)
	for _, label := range strings.Split(strings.TrimSuffix(hostname, "."), ".") {
		packet = append(packet, byte(len(label)))
		packet = append(packet, label...)
	}
	packet = append(packet, 0)
	packet = binary.BigEndian.AppendUint16(packet, 1)
	packet = binary.BigEndian.AppendUint16(packet, 1)
	return packet
}

func compactProbeError(err error) string {
	if networkError, ok := err.(net.Error); ok && networkError.Timeout() {
		return "DNS query timed out"
	}
	return "DNS query failed"
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

func (s *Server) blocklist(w http.ResponseWriter, r *http.Request) {
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
	query := `SELECT id, timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms FROM dns_queries`
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

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	limit := 120
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	events := localEvents(r.Context(), s.store.DB, limit, search)
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) notifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	allEvents := localEvents(r.Context(), s.store.DB, 80, "")
	notifications := []map[string]any{}
	attentionCount := 0
	for _, event := range allEvents {
		eventType, _ := event["type"].(string)
		severity, _ := event["severity"].(string)
		if !isUsefulNotification(eventType, severity) {
			continue
		}
		notifications = append(notifications, event)
		if severity == "warning" || severity == "critical" {
			attentionCount++
		}
		if len(notifications) == 10 {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"attention_count": attentionCount,
		"unread_count":    attentionCount,
		"items":           notifications,
	})
}

func isUsefulNotification(eventType, severity string) bool {
	switch eventType {
	case "dns.query", "dns.blocked", "dns.reload", "device.alias_updated":
		return false
	case "device.first_seen", "dns.reload_failed", "blocklist.installed", "blocklist.updated", "upstream.changed":
		return true
	default:
		return severity == "warning" || severity == "critical"
	}
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	start := todayStart()
	rows, err := s.store.DB.QueryContext(r.Context(), `
		WITH clients AS (
			SELECT client_ip FROM dns_queries
			UNION
			SELECT client_ip FROM device_aliases
		)
		SELECT
			clients.client_ip,
			COALESCE(a.name, '') AS name,
			a.location,
			a.notes,
			COUNT(CASE WHEN q.timestamp >= ? THEN 1 END) AS total_queries_today,
			COALESCE(SUM(CASE WHEN q.timestamp >= ? AND q.action = 'blocked' THEN 1 ELSE 0 END), 0) AS blocked_queries_today,
			MAX(q.timestamp) AS last_seen
		FROM clients
		LEFT JOIN device_aliases a ON a.client_ip = clients.client_ip
		LEFT JOIN dns_queries q ON q.client_ip = clients.client_ip
		GROUP BY clients.client_ip
		ORDER BY last_seen DESC, clients.client_ip
	`, start, start)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()

	type baseDevice struct {
		clientIP string
		name     string
		location any
		notes    any
		total    int
		blocked  int
		lastSeen any
	}
	baseDevices := []baseDevice{}
	for rows.Next() {
		var clientIP, name string
		var location, notes, lastSeen sql.NullString
		var total, blocked int
		if err := rows.Scan(&clientIP, &name, &location, &notes, &total, &blocked, &lastSeen); err != nil {
			writeError(w, err)
			return
		}
		baseDevices = append(baseDevices, baseDevice{
			clientIP: clientIP,
			name:     name,
			location: nullableString(location),
			notes:    nullableString(notes),
			total:    total,
			blocked:  blocked,
			lastSeen: nullableString(lastSeen),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, err)
		return
	}
	rows.Close()

	items := []map[string]any{}
	for _, device := range baseDevices {
		items = append(items, map[string]any{
			"client_ip":             device.clientIP,
			"name":                  device.name,
			"location":              device.location,
			"notes":                 device.notes,
			"device_type":           inferDeviceType(r.Context(), s.store.DB, device.clientIP, device.name),
			"total_queries_today":   device.total,
			"blocked_queries_today": device.blocked,
			"block_percentage":      percentage(device.blocked, device.total),
			"top_domains":           grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE client_ip = ? AND timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 5`, device.clientIP, start),
			"last_seen":             device.lastSeen,
			"profile":               "Default",
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if path == "" {
		writeBadRequest(w, errors.New("client ip is required"))
		return
	}
	if strings.HasSuffix(path, "/alias") {
		rawClientIP := strings.TrimSuffix(path, "/alias")
		clientIP, err := url.PathUnescape(strings.Trim(rawClientIP, "/"))
		if err != nil || strings.TrimSpace(clientIP) == "" {
			writeBadRequest(w, errors.New("invalid client ip"))
			return
		}
		s.deviceAlias(w, r, clientIP)
		return
	}
	if strings.HasSuffix(path, "/replay") {
		rawClientIP := strings.TrimSuffix(path, "/replay")
		clientIP, err := url.PathUnescape(strings.Trim(rawClientIP, "/"))
		if err != nil || strings.TrimSpace(clientIP) == "" {
			writeBadRequest(w, errors.New("invalid client ip"))
			return
		}
		s.deviceReplay(w, r, clientIP)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	clientIP, err := url.PathUnescape(path)
	if err != nil || strings.TrimSpace(clientIP) == "" {
		writeBadRequest(w, errors.New("invalid client ip"))
		return
	}
	start := todayStart()
	var name string
	var location, notes sql.NullString
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT name, location, notes FROM device_aliases WHERE client_ip = ?`, clientIP).Scan(&name, &location, &notes)
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE client_ip = ? AND timestamp >= ?`, clientIP, start)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE client_ip = ? AND timestamp >= ? AND action = 'blocked'`, clientIP, start)
	var firstSeen, lastSeen sql.NullString
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT MIN(timestamp), MAX(timestamp) FROM dns_queries WHERE client_ip = ?`, clientIP).Scan(&firstSeen, &lastSeen)
	writeJSON(w, http.StatusOK, map[string]any{
		"client_ip":             clientIP,
		"name":                  name,
		"location":              nullableString(location),
		"notes":                 nullableString(notes),
		"device_type":           inferDeviceType(r.Context(), s.store.DB, clientIP, name),
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"block_percentage":      percentage(blocked, total),
		"top_domains":           grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE client_ip = ? AND timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 8`, clientIP, start),
		"first_seen":            nullableString(firstSeen),
		"last_seen":             nullableString(lastSeen),
		"profile":               "Default",
		"recent_activity":       recentQueriesFor(r.Context(), s.store.DB, `client_ip = ?`, clientIP),
	})
}

func (s *Server) deviceReplay(w http.ResponseWriter, r *http.Request, clientIP string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	from, to, bucket, rangeLabel, err := replayWindow(r.Context(), s.store.DB, clientIP, r)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	fromText := from.UTC().Format(time.RFC3339)
	toText := to.UTC().Format(time.RFC3339)

	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE client_ip = ? AND timestamp >= ? AND timestamp <= ?`, clientIP, fromText, toText)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE client_ip = ? AND timestamp >= ? AND timestamp <= ? AND action = 'blocked'`, clientIP, fromText, toText)
	uniqueDomains := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(DISTINCT domain) FROM dns_queries WHERE client_ip = ? AND timestamp >= ? AND timestamp <= ?`, clientIP, fromText, toText)

	bucketCount := int((to.Sub(from) + bucket - 1) / bucket)
	if bucketCount < 1 {
		bucketCount = 1
	}
	buckets := make([]map[string]any, bucketCount)
	for index := range buckets {
		buckets[index] = map[string]any{
			"timestamp": from.Add(time.Duration(index) * bucket).UTC().Format(time.RFC3339),
			"total":     0,
			"blocked":   0,
		}
	}
	rows, queryErr := s.store.DB.QueryContext(r.Context(), `
		SELECT
			CAST((strftime('%s', timestamp) - strftime('%s', ?)) / ? AS INTEGER) AS bucket_index,
			COUNT(*),
			COALESCE(SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), 0)
		FROM dns_queries
		WHERE client_ip = ? AND timestamp >= ? AND timestamp <= ?
		GROUP BY bucket_index
		ORDER BY bucket_index
	`, fromText, int(bucket.Seconds()), clientIP, fromText, toText)
	if queryErr == nil {
		defer rows.Close()
		for rows.Next() {
			var index, count, blockedCount int
			if scanErr := rows.Scan(&index, &count, &blockedCount); scanErr != nil {
				break
			}
			if index >= 0 && index < len(buckets) {
				buckets[index]["total"] = count
				buckets[index]["blocked"] = blockedCount
			}
		}
	}

	events, truncated := replayQueries(r.Context(), s.store.DB, clientIP, fromText, toText, 2500)
	durationMinutes := to.Sub(from).Minutes()
	queriesPerMinute := 0.0
	if durationMinutes > 0 {
		queriesPerMinute = float64(total) / durationMinutes
	}
	var firstSeen, lastSeen sql.NullString
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT MIN(timestamp), MAX(timestamp) FROM dns_queries WHERE client_ip = ? AND timestamp >= ? AND timestamp <= ?`, clientIP, fromText, toText).Scan(&firstSeen, &lastSeen)

	writeJSON(w, http.StatusOK, map[string]any{
		"client_ip":          clientIP,
		"range":              rangeLabel,
		"from":               fromText,
		"to":                 toText,
		"bucket_seconds":     int(bucket.Seconds()),
		"total_queries":      total,
		"blocked_queries":    blocked,
		"unique_domains":     uniqueDomains,
		"queries_per_minute": queriesPerMinute,
		"first_seen":         nullableString(firstSeen),
		"last_seen":          nullableString(lastSeen),
		"buckets":            buckets,
		"top_domains": grouped(r.Context(), s.store.DB, `
			SELECT domain, COUNT(*) FROM dns_queries
			WHERE client_ip = ? AND timestamp >= ? AND timestamp <= ?
			GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 10
		`, clientIP, fromText, toText),
		"sources": grouped(r.Context(), s.store.DB, `
			SELECT source, COUNT(*) FROM dns_queries
			WHERE client_ip = ? AND timestamp >= ? AND timestamp <= ?
			GROUP BY source ORDER BY COUNT(*) DESC, source
		`, clientIP, fromText, toText),
		"events":    events,
		"truncated": truncated,
	})
}

func replayWindow(ctx context.Context, database *sql.DB, clientIP string, r *http.Request) (time.Time, time.Time, time.Duration, string, error) {
	now := time.Now().UTC()
	if fromRaw, toRaw := strings.TrimSpace(r.URL.Query().Get("from")), strings.TrimSpace(r.URL.Query().Get("to")); fromRaw != "" || toRaw != "" {
		from, fromErr := time.Parse(time.RFC3339, fromRaw)
		to, toErr := time.Parse(time.RFC3339, toRaw)
		if fromErr != nil || toErr != nil || !to.After(from) {
			return time.Time{}, time.Time{}, 0, "", errors.New("from and to must be valid RFC3339 timestamps with to after from")
		}
		return from.UTC(), to.UTC(), replayBucketSize(to.Sub(from)), "custom", nil
	}

	rangeLabel := strings.TrimSpace(r.URL.Query().Get("range"))
	if rangeLabel == "" {
		rangeLabel = "7d"
	}
	var from time.Time
	switch rangeLabel {
	case "1h":
		from = now.Add(-time.Hour)
	case "24h":
		from = now.Add(-24 * time.Hour)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	case "all":
		var firstSeen sql.NullString
		_ = database.QueryRowContext(ctx, `SELECT MIN(timestamp) FROM dns_queries WHERE client_ip = ?`, clientIP).Scan(&firstSeen)
		if firstSeen.Valid {
			if parsed, parseErr := time.Parse(time.RFC3339, firstSeen.String); parseErr == nil {
				from = parsed.UTC()
			}
		}
		if from.IsZero() {
			from = now.Add(-7 * 24 * time.Hour)
		}
	default:
		return time.Time{}, time.Time{}, 0, "", errors.New("range must be one of 1h, 24h, 7d, 30d, or all")
	}
	return from, now, replayBucketSize(now.Sub(from)), rangeLabel, nil
}

func replayBucketSize(duration time.Duration) time.Duration {
	switch {
	case duration <= 2*time.Hour:
		return 5 * time.Minute
	case duration <= 2*24*time.Hour:
		return 30 * time.Minute
	case duration <= 8*24*time.Hour:
		return 3 * time.Hour
	case duration <= 32*24*time.Hour:
		return 12 * time.Hour
	default:
		hours := int(duration.Hours()/72) + 1
		if hours < 1 {
			hours = 1
		}
		return time.Duration(hours) * time.Hour
	}
}

func replayQueries(ctx context.Context, database *sql.DB, clientIP, from, to string, limit int) ([]map[string]any, bool) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms
		FROM dns_queries
		WHERE client_ip = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC, id ASC
		LIMIT ?
	`, clientIP, from, to, limit+1)
	if err != nil {
		return []map[string]any{}, false
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var timestamp, rowClientIP, domain, queryType, action, source, upstream string
		var latency sql.NullFloat64
		if scanErr := rows.Scan(&id, &timestamp, &rowClientIP, &domain, &queryType, &action, &source, &upstream, &latency); scanErr != nil {
			break
		}
		items = append(items, map[string]any{
			"id":         id,
			"timestamp":  timestamp,
			"client_ip":  rowClientIP,
			"domain":     domain,
			"query_type": queryType,
			"action":     action,
			"source":     source,
			"upstream":   upstream,
			"latency_ms": nullableFloat(latency),
		})
	}
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	return items, truncated
}

func (s *Server) deviceAlias(w http.ResponseWriter, r *http.Request, clientIP string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	var input deviceAliasInput
	if !decode(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		writeBadRequest(w, errors.New("name is required"))
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `
		INSERT INTO device_aliases(client_ip, name, location, notes, updated_at)
		VALUES(?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(client_ip) DO UPDATE SET
			name = excluded.name,
			location = excluded.location,
			notes = excluded.notes,
			updated_at = CURRENT_TIMESTAMP
	`, clientIP, name, nullableInput(input.Location), nullableInput(input.Notes)); err != nil {
		writeError(w, err)
		return
	}
	s.recordEvent(r.Context(), eventInput{
		Type:        "device.alias_updated",
		Severity:    "info",
		Title:       "Device name updated",
		Description: fmt.Sprintf("%s is now known as %s.", clientIP, name),
		ClientIP:    clientIP,
		Metadata:    map[string]any{"name": name, "location": strings.TrimSpace(input.Location)},
		Source:      "devices",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) domainSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/domains/"), "/")
	if !strings.HasSuffix(path, "/summary") {
		http.NotFound(w, r)
		return
	}
	rawDomain, err := url.PathUnescape(strings.TrimSuffix(path, "/summary"))
	if err != nil {
		writeBadRequest(w, errors.New("invalid domain"))
		return
	}
	domain, err := db.NormalizeDomain(rawDomain)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	start := todayStart()
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE domain = ? AND timestamp >= ?`, domain, start)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE domain = ? AND timestamp >= ? AND action = 'blocked'`, domain, start)
	var firstSeen, lastSeen sql.NullString
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT MIN(timestamp), MAX(timestamp) FROM dns_queries WHERE domain = ?`, domain).Scan(&firstSeen, &lastSeen)
	allowedAll := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE domain = ? AND action = 'allowed'`, domain)
	blockedAll := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE domain = ? AND action = 'blocked'`, domain)
	status := "Allowed"
	if allowedAll > 0 && blockedAll > 0 {
		status = "Mixed"
	} else if blockedAll > 0 {
		status = "Blocked"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domain":                domain,
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"first_seen":            nullableString(firstSeen),
		"last_seen":             nullableString(lastSeen),
		"clients":               grouped(r.Context(), s.store.DB, `SELECT client_ip, COUNT(*) FROM dns_queries WHERE domain = ? GROUP BY client_ip ORDER BY COUNT(*) DESC, client_ip LIMIT 8`, domain),
		"query_types":           grouped(r.Context(), s.store.DB, `SELECT query_type, COUNT(*) FROM dns_queries WHERE domain = ? GROUP BY query_type ORDER BY COUNT(*) DESC, query_type`, domain),
		"status":                status,
		"recent_queries":        recentQueriesFor(r.Context(), s.store.DB, `domain = ?`, domain),
		"recent_events":         localEvents(r.Context(), s.store.DB, 12, domain),
	})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"domains":       []map[string]any{},
			"devices":       []map[string]any{},
			"events":        []map[string]any{},
			"local_records": []map[string]any{},
			"rules":         []map[string]any{},
			"blocklists":    []map[string]any{},
		})
		return
	}
	like := "%" + q + "%"
	writeJSON(w, http.StatusOK, map[string]any{
		"domains": grouped(r.Context(), s.store.DB, `
			SELECT domain, COUNT(*) FROM dns_queries
			WHERE domain LIKE ?
			GROUP BY domain
			ORDER BY MAX(timestamp) DESC
			LIMIT 8
		`, like),
		"devices": searchRows(r.Context(), s.store.DB, `
			WITH clients AS (
				SELECT client_ip FROM dns_queries
				UNION
				SELECT client_ip FROM device_aliases
			)
			SELECT clients.client_ip AS label, COALESCE(a.name, '') AS subtitle
			FROM clients
			LEFT JOIN device_aliases a ON a.client_ip = clients.client_ip
			WHERE clients.client_ip LIKE ? OR a.name LIKE ?
			ORDER BY COALESCE(a.name, clients.client_ip)
			LIMIT 8
		`, like, like),
		"events": searchRows(r.Context(), s.store.DB, `
			SELECT title AS label, type || ' · ' || description AS subtitle
			FROM events
			WHERE title LIKE ? OR description LIKE ? OR type LIKE ? OR domain LIKE ? OR client_ip LIKE ?
			ORDER BY timestamp DESC
			LIMIT 8
		`, like, like, like, like, like),
		"local_records": searchRows(r.Context(), s.store.DB, `
			SELECT hostname AS label, type || ' ' || value AS subtitle
			FROM dns_records
			WHERE hostname LIKE ? OR value LIKE ? OR description LIKE ?
			ORDER BY hostname
			LIMIT 8
		`, like, like, like),
		"rules": searchRows(r.Context(), s.store.DB, `
			SELECT domain AS label, 'Allowed manually' AS subtitle FROM allowlist_entries WHERE domain LIKE ?
			UNION ALL
			SELECT domain AS label, 'Blocked manually' AS subtitle FROM manual_block_entries WHERE domain LIKE ?
			ORDER BY label
			LIMIT 8
		`, like, like),
		"blocklists": searchRows(r.Context(), s.store.DB, `
			SELECT name AS label, url AS subtitle
			FROM blocklists
			WHERE name LIKE ? OR url LIKE ?
			ORDER BY name
			LIMIT 8
		`, like, like),
	})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	start := todayStart()
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ?`, start)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ? AND action = 'blocked'`, start)
	enabledBlocklists := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklists WHERE enabled = 1`)
	blockEntries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklist_entries`)
	topClients := grouped(r.Context(), s.store.DB, `SELECT client_ip, COUNT(*) FROM dns_queries WHERE timestamp >= ? GROUP BY client_ip ORDER BY COUNT(*) DESC LIMIT 5`, start)
	topBlocked := grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE timestamp >= ? AND action = 'blocked' GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 5`, start)
	deviceCount := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(DISTINCT client_ip) FROM dns_queries`)
	reloadFailures := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM events WHERE type = 'dns.reload_failed' AND timestamp >= ?`, start)
	cacheHits := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ? AND source = 'cache'`, start)
	upstreamQueries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ? AND source = 'upstream'`, start)
	cacheLatency := scalarFloat(r.Context(), s.store.DB, `SELECT COALESCE(AVG(latency_ms), 0) FROM dns_queries WHERE timestamp >= ? AND source = 'cache'`, start)
	upstreamLatency := scalarFloat(r.Context(), s.store.DB, `SELECT COALESCE(AVG(latency_ms), 0) FROM dns_queries WHERE timestamp >= ? AND source = 'upstream'`, start)
	liveCache := s.coreDNSCacheMetrics(r.Context())

	writeJSON(w, http.StatusOK, map[string]any{
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"block_percentage":      percentage(blocked, total),
		"enabled_blocklists":    enabledBlocklists,
		"blocklist_entries":     blockEntries,
		"cache": map[string]any{
			"enabled":                     settingValue(r.Context(), s.store.DB, "dns_cache_enabled") != "false",
			"metrics_available":           liveCache.available,
			"entries":                     liveCache.entries,
			"hits_since_restart":          liveCache.hits,
			"requests_since_restart":      liveCache.requests,
			"hit_rate_since_restart":      percentage64(liveCache.hits, liveCache.requests),
			"hits_today":                  cacheHits,
			"upstream_queries_today":      upstreamQueries,
			"hit_rate_today":              percentage(cacheHits, cacheHits+upstreamQueries),
			"average_cache_latency_ms":    cacheLatency,
			"average_upstream_latency_ms": upstreamLatency,
		},
		"network_summary":          networkSummary(r.Context(), s.store.DB, start, blocked, topClients, topBlocked),
		"health_cards":             healthCards(r.Context(), s.store.DB, total, blocked, enabledBlocklists, blockEntries, deviceCount, reloadFailures),
		"stories":                  dashboardStories(r.Context(), s.store.DB, start, blocked, topClients, topBlocked, reloadFailures),
		"whats_new":                whatsNew(r.Context(), s.store.DB, start),
		"sparklines":               dashboardSparklines(r.Context(), s.store.DB),
		"top_queried_domains":      grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 5`, start),
		"top_blocked_domains":      topBlocked,
		"top_clients":              topClients,
		"recent_activity":          recentQueries(r.Context(), s.store.DB),
		"upstream_health":          "Not checked yet",
		"upstream_health_status":   "placeholder",
		"favicon_fetching_enabled": settingValue(r.Context(), s.store.DB, "favicon_fetching_enabled"),
	})
}

type cacheMetrics struct {
	available bool
	entries   float64
	hits      float64
	requests  float64
}

func (s *Server) coreDNSCacheMetrics(ctx context.Context) cacheMetrics {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, s.metricsURL, nil)
	if err != nil {
		return cacheMetrics{}
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return cacheMetrics{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return cacheMetrics{}
	}

	result := cacheMetrics{available: true}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseFloat(fields[len(fields)-1], 64)
		if parseErr != nil {
			continue
		}
		name := fields[0]
		if label := strings.Index(name, "{"); label >= 0 {
			name = name[:label]
		}
		switch name {
		case "coredns_cache_entries":
			result.entries += value
		case "coredns_cache_hits_total":
			result.hits += value
		case "coredns_cache_requests_total":
			result.requests += value
		}
	}
	return result
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
		oldUpstream := settingValue(r.Context(), s.store.DB, "upstream_dns")
		tx, err := s.store.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback()
		for key, value := range input {
			switch key {
			case "upstream_dns", "local_domain_suffix", "retention_days", "favicon_fetching_enabled", "dns_cache_enabled", "dns_cache_ttl":
				if key == "dns_cache_enabled" && value != "true" && value != "false" {
					writeBadRequest(w, errors.New("dns_cache_enabled must be true or false"))
					return
				}
				if key == "dns_cache_ttl" {
					ttl, parseErr := strconv.Atoi(value)
					if parseErr != nil || ttl < 30 || ttl > 3600 {
						writeBadRequest(w, errors.New("dns_cache_ttl must be between 30 and 3600 seconds"))
						return
					}
				}
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
			s.recordEvent(r.Context(), eventInput{
				Type:        "dns.reload_failed",
				Severity:    "critical",
				Title:       "DNS reload failed",
				Description: err.Error(),
				Source:      "settings",
			})
			writeError(w, err)
			return
		}
		if nextUpstream, ok := input["upstream_dns"]; ok && strings.TrimSpace(nextUpstream) != strings.TrimSpace(oldUpstream) {
			s.recordEvent(r.Context(), eventInput{
				Type:        "upstream.changed",
				Severity:    "info",
				Title:       "Upstreams changed",
				Description: "DNS upstream servers were updated.",
				Metadata:    map[string]any{"from": oldUpstream, "to": nextUpstream},
				Source:      "settings",
			})
		}
		s.recordEvent(r.Context(), eventInput{
			Type:        "dns.reload",
			Severity:    "success",
			Title:       "DNS reloaded",
			Description: "Configuration successfully reloaded.",
			Source:      "settings",
		})
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
		s.recordEvent(r.Context(), eventInput{
			Type:        "dns.reload_failed",
			Severity:    "critical",
			Title:       "DNS reload failed",
			Description: err.Error(),
			Source:      "dns",
		})
		writeError(w, err)
		return
	}
	s.recordEvent(r.Context(), eventInput{
		Type:        "dns.reload",
		Severity:    "success",
		Title:       "DNS reloaded",
		Description: "Configuration successfully reloaded.",
		Source:      "dns",
	})
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
	cacheHits := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE source = 'cache'`)
	upstreamQueries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE source = 'upstream'`)
	cache := s.coreDNSCacheMetrics(r.Context())
	enabled := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklists WHERE enabled = 1`)
	entries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklist_entries`)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_queries_total counter\nfaro_dns_queries_total %d\n", total)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_blocked_queries_total counter\nfaro_dns_blocked_queries_total %d\n", blocked)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_cache_hits_total counter\nfaro_dns_cache_hits_total %d\n", cacheHits)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_upstream_queries_total counter\nfaro_dns_upstream_queries_total %d\n", upstreamQueries)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_cache_entries gauge\nfaro_dns_cache_entries %.0f\n", cache.entries)
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

type deviceAliasInput struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Notes    string `json:"notes"`
}

type eventInput struct {
	Type        string
	Severity    string
	Title       string
	Description string
	ClientIP    string
	Domain      string
	Metadata    map[string]any
	Source      string
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
	rows, err := database.QueryContext(ctx, `SELECT timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms FROM dns_queries ORDER BY timestamp DESC LIMIT 8`)
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

func recentQueriesFor(ctx context.Context, database *sql.DB, where string, args ...any) []map[string]any {
	query := `SELECT id, timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms FROM dns_queries WHERE ` + where + ` ORDER BY timestamp DESC LIMIT 12`
	rows, err := database.QueryContext(ctx, query, args...)
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

func searchRows(ctx context.Context, database *sql.DB, query string, args ...any) []map[string]any {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var label string
		var subtitle sql.NullString
		if err := rows.Scan(&label, &subtitle); err != nil {
			return items
		}
		items = append(items, map[string]any{"label": label, "subtitle": nullableString(subtitle)})
	}
	return items
}

func (s *Server) recordEvent(ctx context.Context, event eventInput) {
	severity := strings.TrimSpace(event.Severity)
	if severity == "" {
		severity = "info"
	}
	source := strings.TrimSpace(event.Source)
	if source == "" {
		source = "faro"
	}
	metadata := "{}"
	if event.Metadata != nil {
		if encoded, err := json.Marshal(event.Metadata); err == nil {
			metadata = string(encoded)
		}
	}
	_, _ = s.store.DB.ExecContext(ctx, `
		INSERT INTO events(type, severity, title, description, client_ip, domain, metadata, source)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, event.Type, severity, event.Title, event.Description, nullableInput(event.ClientIP), nullableInput(event.Domain), metadata, source)
}

func localEvents(ctx context.Context, database *sql.DB, limit int, search string) []map[string]any {
	events := persistedEvents(ctx, database, limit, search)
	events = append(events, queryEvents(ctx, database, limit, search)...)
	events = append(events, firstSeenDeviceEvents(ctx, database, limit, search)...)
	sortEvents(events)
	if len(events) > limit {
		return events[:limit]
	}
	return events
}

func persistedEvents(ctx context.Context, database *sql.DB, limit int, search string) []map[string]any {
	query := `SELECT id, timestamp, type, severity, title, description, client_ip, domain, metadata, source FROM events`
	args := []any{}
	if search != "" {
		query += ` WHERE title LIKE ? OR description LIKE ? OR type LIKE ? OR domain LIKE ? OR client_ip LIKE ?`
		like := "%" + search + "%"
		args = append(args, like, like, like, like, like)
	}
	query += ` ORDER BY timestamp DESC LIMIT ?`
	args = append(args, limit)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var timestamp, eventType, severity, title, description, metadata, source string
		var clientIP, domain sql.NullString
		if err := rows.Scan(&id, &timestamp, &eventType, &severity, &title, &description, &clientIP, &domain, &metadata, &source); err != nil {
			return items
		}
		items = append(items, map[string]any{
			"id":          fmt.Sprintf("event-%d", id),
			"timestamp":   timestamp,
			"type":        eventType,
			"severity":    severity,
			"title":       title,
			"description": description,
			"client_ip":   nullableString(clientIP),
			"domain":      nullableString(domain),
			"metadata":    metadataMap(metadata),
			"source":      source,
		})
	}
	return items
}

func queryEvents(ctx context.Context, database *sql.DB, limit int, search string) []map[string]any {
	query := `
		SELECT q.id, q.timestamp, q.client_ip, q.domain, q.query_type, q.action, q.source, q.upstream, q.latency_ms, COALESCE(a.name, '')
		FROM dns_queries q
		LEFT JOIN device_aliases a ON a.client_ip = q.client_ip
	`
	args := []any{}
	if search != "" {
		query += ` WHERE q.domain LIKE ? OR q.client_ip LIKE ? OR q.query_type LIKE ? OR q.action LIKE ? OR q.source LIKE ? OR a.name LIKE ?`
		like := "%" + search + "%"
		args = append(args, like, like, like, like, like, like)
	}
	query += ` ORDER BY q.timestamp DESC LIMIT ?`
	args = append(args, limit)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var timestamp, clientIP, domain, queryType, action, source, upstream, alias string
		var latency sql.NullFloat64
		if err := rows.Scan(&id, &timestamp, &clientIP, &domain, &queryType, &action, &source, &upstream, &latency, &alias); err != nil {
			return items
		}
		deviceName := clientIP
		if alias != "" {
			deviceName = alias
		}
		eventType := "dns.query"
		severity := "info"
		title := domain + " resolved"
		description := "Requested by " + deviceName + "."
		if source == "cache" {
			description = "Served from Faro's cache for " + deviceName + "."
		} else if source == "upstream" && upstream != "" {
			description = "Resolved through " + upstream + " for " + deviceName + "."
		} else if source == "local" {
			description = "Answered by Local DNS for " + deviceName + "."
		}
		if action == "blocked" {
			eventType = "dns.blocked"
			severity = "warning"
			title = "Domain blocked"
			description = domain + " was blocked by " + source + "."
		}
		items = append(items, map[string]any{
			"id":          fmt.Sprintf("query-%d", id),
			"timestamp":   timestamp,
			"type":        eventType,
			"severity":    severity,
			"title":       title,
			"description": description,
			"client_ip":   clientIP,
			"domain":      domain,
			"metadata": map[string]any{
				"query_type": queryType,
				"action":     action,
				"latency_ms": nullableFloat(latency),
				"upstream":   upstream,
			},
			"source": source,
		})
	}
	return items
}

func firstSeenDeviceEvents(ctx context.Context, database *sql.DB, limit int, search string) []map[string]any {
	query := `
		SELECT q.client_ip, MIN(q.timestamp) AS first_seen, COALESCE(a.name, '') AS name, a.location
		FROM dns_queries q
		LEFT JOIN device_aliases a ON a.client_ip = q.client_ip
		GROUP BY q.client_ip
	`
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	needle := strings.ToLower(search)
	for rows.Next() {
		var clientIP, timestamp, name string
		var location sql.NullString
		if err := rows.Scan(&clientIP, &timestamp, &name, &location); err != nil {
			return items
		}
		title := "Device first seen"
		deviceName := clientIP
		if name != "" {
			deviceName = name
		}
		description := deviceName + " joined the network."
		if location.Valid && location.String != "" {
			description = deviceName + " joined from " + location.String + "."
		}
		if needle != "" && !strings.Contains(strings.ToLower(clientIP+" "+deviceName+" "+description), needle) {
			continue
		}
		items = append(items, map[string]any{
			"id":          "device-first-seen-" + clientIP,
			"timestamp":   timestamp,
			"type":        "device.first_seen",
			"severity":    "info",
			"title":       title,
			"description": description,
			"client_ip":   clientIP,
			"domain":      nil,
			"metadata":    map[string]any{"device_name": deviceName, "location": nullableString(location)},
			"source":      "devices",
		})
		if len(items) >= limit {
			break
		}
	}
	return items
}

func sortEvents(events []map[string]any) {
	for i := 0; i < len(events); i++ {
		for j := i + 1; j < len(events); j++ {
			left, _ := events[i]["timestamp"].(string)
			right, _ := events[j]["timestamp"].(string)
			if right > left {
				events[i], events[j] = events[j], events[i]
			}
		}
	}
}

func networkSummary(ctx context.Context, database *sql.DB, start string, blocked int, topClients, topBlocked []map[string]any) map[string]any {
	headline := "Everything looks normal."
	messages := []string{}
	if blocked > 0 {
		headline = fmt.Sprintf("Faro blocked %d requests today.", blocked)
		messages = append(messages, headline)
	} else {
		messages = append(messages, "Everything looks normal.")
	}
	if len(topClients) > 0 {
		if label, ok := topClients[0]["label"].(string); ok && label != "" {
			messages = append(messages, "Top active device today: "+label+".")
		}
	}
	if len(topBlocked) > 0 {
		if label, ok := topBlocked[0]["label"].(string); ok && label != "" {
			messages = append(messages, "Most blocked domain: "+label+".")
		}
	}
	newDevices := scalarInt(ctx, database, `
		SELECT COUNT(*) FROM (
			SELECT client_ip, MIN(timestamp) AS first_seen
			FROM dns_queries
			GROUP BY client_ip
			HAVING first_seen >= ?
		)
	`, start)
	if newDevices == 0 {
		messages = append(messages, "No new devices seen today.")
	} else if newDevices == 1 {
		messages = append(messages, "1 new device seen today.")
	} else {
		messages = append(messages, fmt.Sprintf("%d new devices seen today.", newDevices))
	}
	return map[string]any{"headline": headline, "messages": messages}
}

func dashboardStories(ctx context.Context, database *sql.DB, start string, blocked int, topClients, topBlocked []map[string]any, reloadFailures int) []map[string]any {
	stories := []map[string]any{}
	if reloadFailures == 0 {
		stories = append(stories, story("Everything looks healthy today.", "No DNS reload failures detected.", "success"))
	} else {
		stories = append(stories, story("DNS needs attention.", fmt.Sprintf("%d reload failures detected today.", reloadFailures), "critical"))
	}
	newDevices := scalarInt(ctx, database, `SELECT COUNT(*) FROM (SELECT client_ip, MIN(timestamp) first_seen FROM dns_queries GROUP BY client_ip HAVING first_seen >= ?)`, start)
	if newDevices > 0 {
		stories = append(stories, story(fmt.Sprintf("%d new devices joined today.", newDevices), "New clients are now visible in Devices.", "info"))
	}
	if blocked > 0 {
		stories = append(stories, story(fmt.Sprintf("Filtering blocked %d requests today.", blocked), firstLabelSentence("Most blocked domain", topBlocked), "warning"))
	}
	if len(topClients) > 0 {
		stories = append(stories, story("Busiest device today.", firstLabelSentence("Top active device", topClients), "info"))
	}
	return stories
}

func story(title, body, tone string) map[string]any {
	return map[string]any{"title": title, "body": body, "tone": tone}
}

func firstLabelSentence(prefix string, items []map[string]any) string {
	if len(items) == 0 {
		return ""
	}
	label, _ := items[0]["label"].(string)
	if label == "" {
		return ""
	}
	return prefix + ": " + label + "."
}

func healthCards(ctx context.Context, database *sql.DB, total, blocked, enabledBlocklists, blockEntries, deviceCount, reloadFailures int) []map[string]any {
	upstreams := strings.Split(settingValue(ctx, database, "upstream_dns"), ",")
	upstreamCount := 0
	for _, upstream := range upstreams {
		if strings.TrimSpace(upstream) != "" {
			upstreamCount++
		}
	}
	cacheAnswers := scalarInt(ctx, database, `SELECT COUNT(*) FROM dns_queries WHERE source = 'cache'`)
	forwardedAnswers := scalarInt(ctx, database, `SELECT COUNT(*) FROM dns_queries WHERE source = 'upstream'`)
	return []map[string]any{
		{"label": "DNS", "value": ternary(reloadFailures == 0, "Healthy", "Needs attention"), "detail": ternary(reloadFailures == 0, "Engine running", "Reload failures detected"), "status": ternary(reloadFailures == 0, "healthy", "critical")},
		{"label": "Upstreams", "value": fmt.Sprintf("%d configured", upstreamCount), "detail": "Ready for resolution", "status": "healthy"},
		{"label": "Devices", "value": fmt.Sprintf("%d observed", deviceCount), "detail": "From local query data", "status": "healthy"},
		{"label": "Filtering", "value": fmt.Sprintf("%d domains", blockEntries), "detail": fmt.Sprintf("%d enabled lists", enabledBlocklists), "status": ternary(blockEntries > 0, "healthy", "warning")},
		{"label": "Cache", "value": fmt.Sprintf("%.1f%% hit rate", percentage(cacheAnswers, cacheAnswers+forwardedAnswers)), "detail": fmt.Sprintf("%d upstream calls avoided", cacheAnswers), "status": "info"},
		{"label": "Blocked", "value": fmt.Sprintf("%d today", blocked), "detail": fmt.Sprintf("%.1f%% of activity", percentage(blocked, total)), "status": ternary(blocked > 0, "warning", "healthy")},
	}
}

func whatsNew(ctx context.Context, database *sql.DB, start string) map[string]any {
	return map[string]any{
		"devices":       searchRows(ctx, database, `SELECT client_ip AS label, 'First seen today' AS subtitle FROM (SELECT client_ip, MIN(timestamp) first_seen FROM dns_queries GROUP BY client_ip HAVING first_seen >= ?) LIMIT 5`, start),
		"domains":       searchRows(ctx, database, `SELECT domain AS label, 'First time observed' AS subtitle FROM (SELECT domain, MIN(timestamp) first_seen FROM dns_queries GROUP BY domain HAVING first_seen >= ?) LIMIT 5`, start),
		"blocklists":    searchRows(ctx, database, `SELECT name AS label, 'Installed today' AS subtitle FROM blocklists WHERE created_at >= ? ORDER BY created_at DESC LIMIT 5`, start),
		"local_records": searchRows(ctx, database, `SELECT hostname AS label, 'Local DNS record' AS subtitle FROM dns_records WHERE created_at >= ? ORDER BY created_at DESC LIMIT 5`, start),
	}
}

func dashboardSparklines(ctx context.Context, database *sql.DB) map[string]any {
	return map[string]any{
		"activity": hourlyCounts(ctx, database, `SELECT strftime('%H', timestamp) AS hour, COUNT(*) FROM dns_queries WHERE timestamp >= datetime('now', '-24 hours') GROUP BY hour`),
		"blocked":  hourlyCounts(ctx, database, `SELECT strftime('%H', timestamp) AS hour, COUNT(*) FROM dns_queries WHERE timestamp >= datetime('now', '-24 hours') AND action = 'blocked' GROUP BY hour`),
	}
}

func hourlyCounts(ctx context.Context, database *sql.DB, query string) []int {
	values := make([]int, 24)
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return values
	}
	defer rows.Close()
	nowHour := time.Now().UTC().Hour()
	for rows.Next() {
		var hourText string
		var count int
		if err := rows.Scan(&hourText, &count); err != nil {
			return values
		}
		hour, err := strconv.Atoi(hourText)
		if err != nil {
			continue
		}
		index := (hour - nowHour + 23 + 24) % 24
		values[index] = count
	}
	return values
}

func inferDeviceType(ctx context.Context, database *sql.DB, clientIP, name string) string {
	probe := strings.ToLower(name + " " + clientIP + " " + strings.Join(topLabels(ctx, database, `SELECT domain, COUNT(*) FROM dns_queries WHERE client_ip = ? GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 12`, clientIP), " "))
	switch {
	case strings.Contains(probe, "tesla"):
		return "Tesla"
	case strings.Contains(probe, "synology") || strings.Contains(probe, "nas") || strings.Contains(probe, "plex"):
		return "NAS"
	case strings.Contains(probe, "android") || strings.Contains(probe, "googleapis"):
		return "Android Phone"
	case strings.Contains(probe, "apple") || strings.Contains(probe, "icloud") || strings.Contains(probe, "itunes"):
		if strings.Contains(probe, "tv") {
			return "Apple TV"
		}
		return "Mac"
	case strings.Contains(probe, "windows") || strings.Contains(probe, "microsoft"):
		return "Windows PC"
	case strings.Contains(probe, "ubuntu") || strings.Contains(probe, "debian") || strings.Contains(probe, "docker") || clientIP == "127.0.0.1":
		return "Linux Server"
	default:
		return "Unknown"
	}
}

func topLabels(ctx context.Context, database *sql.DB, query string, args ...any) []string {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	labels := []string{}
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			return labels
		}
		labels = append(labels, label)
	}
	return labels
}

func scalarInt(ctx context.Context, database *sql.DB, query string, args ...any) int {
	var count int
	_ = database.QueryRowContext(ctx, query, args...).Scan(&count)
	return count
}

func scalarFloat(ctx context.Context, database *sql.DB, query string, args ...any) float64 {
	var value float64
	_ = database.QueryRowContext(ctx, query, args...).Scan(&value)
	return float64(int(value*100+0.5)) / 100
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

func percentage64(part, total float64) float64 {
	if total == 0 {
		return 0
	}
	return float64(int(part/total*1000+0.5)) / 10
}

func todayStart() string {
	return time.Now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339)
}

func nullableString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func nullableFloat(value sql.NullFloat64) any {
	if value.Valid {
		return value.Float64
	}
	return nil
}

func nullableInput(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func metadataMap(raw string) map[string]any {
	result := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return result
	}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func displayDeviceName(ctx context.Context, database *sql.DB, clientIP string) string {
	var name string
	if err := database.QueryRowContext(ctx, `SELECT name FROM device_aliases WHERE client_ip = ?`, clientIP).Scan(&name); err == nil && strings.TrimSpace(name) != "" {
		return name
	}
	return clientIP
}

func ternary(condition bool, truthy, falsy string) string {
	if condition {
		return truthy
	}
	return falsy
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

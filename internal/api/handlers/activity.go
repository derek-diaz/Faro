package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/derek/faro/internal/auth"
)

func (s *Handler) queries(w http.ResponseWriter, r *http.Request) {
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
	query := `SELECT id, timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms, rcode, decision_reason, decision_metadata FROM dns_queries`
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

func (s *Handler) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	page := positiveInt(r.URL.Query().Get("page"), 1, 1000000)
	pageSize := positiveInt(r.URL.Query().Get("page_size"), 50, 200)
	scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
	if !validActivityScope(scope) {
		scope = "all"
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	counts := activityCounts(r.Context(), s.store.DB, search)
	total := counts[scope]
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       pagedActivityEvents(r.Context(), s.store.DB, page, pageSize, search, scope),
		"page":        page,
		"page_size":   pageSize,
		"total":       total,
		"total_pages": totalPages,
		"counts":      counts,
	})
}

func positiveInt(raw string, fallback, maximum int) int {
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 || parsed > maximum {
		return fallback
	}
	return parsed
}

func validActivityScope(scope string) bool {
	switch scope {
	case "all", "dns", "cache", "upstream", "blocked", "system":
		return true
	default:
		return false
	}
}

func (s *Handler) notifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	userID, ok := auth.UserID(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	allEvents := notificationCandidates(r.Context(), s.store.DB, 1000)
	states, readAllAt, err := loadNotificationStates(r.Context(), s.store.DB, userID)
	if err != nil {
		writeError(w, err)
		return
	}
	notifications := []map[string]any{}
	attentionCount := 0
	unreadCount := 0
	for _, event := range allEvents {
		eventType, _ := event["type"].(string)
		severity, _ := event["severity"].(string)
		if !isUsefulNotification(eventType, severity) {
			continue
		}
		eventKey, _ := event["id"].(string)
		state := states[eventKey]
		if state.dismissed {
			continue
		}
		isRead := state.read || eventAtOrBefore(event, readAllAt)
		if severity == "warning" || severity == "critical" {
			attentionCount++
		}
		if !isRead {
			unreadCount++
		}
		if len(notifications) < 10 {
			event["is_read"] = isRead
			notifications = append(notifications, event)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"attention_count": attentionCount,
		"unread_count":    unreadCount,
		"items":           notifications,
	})
}

type storedNotificationState struct {
	read      bool
	dismissed bool
}

func (s *Handler) notificationState(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/notifications/"), "/")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if path == "read-all" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		_, err := s.store.DB.ExecContext(r.Context(), `
			INSERT INTO notification_states(user_id, event_key, read_at, updated_at) VALUES(?, '*', ?, ?)
			ON CONFLICT(user_id, event_key) DO UPDATE SET read_at = excluded.read_at, updated_at = excluded.updated_at
		`, userID, now, now)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	markRead := strings.HasSuffix(path, "/read")
	rawKey := strings.TrimSuffix(path, "/read")
	eventKey, err := url.PathUnescape(strings.Trim(rawKey, "/"))
	if err != nil || !validNotificationKey(eventKey) {
		writeBadRequest(w, fmt.Errorf("invalid notification id"))
		return
	}
	if markRead {
		if r.Method != http.MethodPut {
			methodNotAllowed(w)
			return
		}
		_, err = s.store.DB.ExecContext(r.Context(), `
			INSERT INTO notification_states(user_id, event_key, read_at, updated_at) VALUES(?, ?, ?, ?)
			ON CONFLICT(user_id, event_key) DO UPDATE SET read_at = excluded.read_at, updated_at = excluded.updated_at
		`, userID, eventKey, now, now)
	} else {
		if r.Method != http.MethodDelete {
			methodNotAllowed(w)
			return
		}
		_, err = s.store.DB.ExecContext(r.Context(), `
			INSERT INTO notification_states(user_id, event_key, read_at, dismissed_at, updated_at) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(user_id, event_key) DO UPDATE SET
				read_at = COALESCE(notification_states.read_at, excluded.read_at),
				dismissed_at = excluded.dismissed_at,
				updated_at = excluded.updated_at
		`, userID, eventKey, now, now, now)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func notificationCandidates(ctx context.Context, database *sql.DB, limit int) []map[string]any {
	events := persistedEvents(ctx, database, limit, "")
	events = append(events, firstSeenDeviceEvents(ctx, database, limit, "")...)
	sortEvents(events)
	return events
}

func loadNotificationStates(ctx context.Context, database *sql.DB, userID int64) (map[string]storedNotificationState, time.Time, error) {
	rows, err := database.QueryContext(ctx, `SELECT event_key, read_at, dismissed_at FROM notification_states WHERE user_id = ?`, userID)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	states := map[string]storedNotificationState{}
	var readAllAt time.Time
	for rows.Next() {
		var eventKey string
		var readAt, dismissedAt sql.NullString
		if err := rows.Scan(&eventKey, &readAt, &dismissedAt); err != nil {
			return nil, time.Time{}, err
		}
		if eventKey == "*" {
			readAllAt = parseNotificationTime(readAt.String)
			continue
		}
		states[eventKey] = storedNotificationState{read: readAt.Valid, dismissed: dismissedAt.Valid}
	}
	return states, readAllAt, rows.Err()
}

func eventAtOrBefore(event map[string]any, watermark time.Time) bool {
	if watermark.IsZero() {
		return false
	}
	timestamp, _ := event["timestamp"].(string)
	eventTime := parseNotificationTime(timestamp)
	return !eventTime.IsZero() && !eventTime.After(watermark)
}

func parseNotificationTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func validNotificationKey(value string) bool {
	return len(value) > 0 && len(value) <= 512 && (strings.HasPrefix(value, "event-") || strings.HasPrefix(value, "device-first-seen-"))
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

func (s *Handler) recordEvent(ctx context.Context, event eventInput) {
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
	return activityEvents(ctx, database, limit, search, "all")
}

func pagedActivityEvents(ctx context.Context, database *sql.DB, page, pageSize int, search, scope string) []map[string]any {
	end := page * pageSize
	events := activityEvents(ctx, database, end, search, scope)
	start := (page - 1) * pageSize
	if start >= len(events) {
		return []map[string]any{}
	}
	if end > len(events) {
		end = len(events)
	}
	return events[start:end]
}

func activityEvents(ctx context.Context, database *sql.DB, limit int, search, scope string) []map[string]any {
	events := []map[string]any{}
	if scope == "all" || scope == "system" {
		events = append(events, persistedEvents(ctx, database, limit, search)...)
		events = append(events, firstSeenDeviceEvents(ctx, database, limit, search)...)
	}
	if scope != "system" {
		events = append(events, queryEvents(ctx, database, limit, search, scope)...)
	}
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
	query += ` ORDER BY timestamp DESC, id DESC LIMIT ?`
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

func queryEvents(ctx context.Context, database *sql.DB, limit int, search, scope string) []map[string]any {
	query := `
		SELECT q.id, q.timestamp, q.client_ip, q.domain, q.query_type, q.action, q.source, q.upstream, q.latency_ms,
		       q.rcode, q.decision_reason, q.decision_metadata, COALESCE(a.name, '')
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id
	`
	clauses := []string{}
	args := []any{}
	if search != "" {
		clauses = append(clauses, `(q.domain LIKE ? OR q.client_ip LIKE ? OR q.query_type LIKE ? OR q.action LIKE ? OR q.source LIKE ? OR a.name LIKE ?)`)
		like := "%" + search + "%"
		args = append(args, like, like, like, like, like, like)
	}
	switch scope {
	case "cache":
		clauses = append(clauses, `q.source = 'cache'`)
	case "upstream":
		clauses = append(clauses, `q.source = 'upstream'`)
	case "blocked":
		clauses = append(clauses, `q.action = 'blocked'`)
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	query += ` ORDER BY q.timestamp DESC, q.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var timestamp, clientIP, domain, queryType, action, source, upstream, rcode, decisionReason, decisionMetadata, alias string
		var latency sql.NullFloat64
		if err := rows.Scan(&id, &timestamp, &clientIP, &domain, &queryType, &action, &source, &upstream, &latency, &rcode, &decisionReason, &decisionMetadata, &alias); err != nil {
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
		if decisionReason != "" {
			description = decisionReason
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
				"rcode":      rcode,
				"decision":   metadataMap(decisionMetadata),
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
		LEFT JOIN devices a ON a.id = q.device_id
		GROUP BY q.client_ip
	`
	args := []any{}
	if search != "" {
		query += ` HAVING q.client_ip LIKE ? OR COALESCE(a.name, '') LIKE ? OR COALESCE(a.location, '') LIKE ?`
		like := "%" + search + "%"
		args = append(args, like, like, like)
	}
	query += ` ORDER BY first_seen DESC LIMIT ?`
	args = append(args, limit)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
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
	}
	return items
}

func activityCounts(ctx context.Context, database *sql.DB, search string) map[string]int {
	query := `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN q.source = 'cache' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN q.source = 'upstream' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN q.action = 'blocked' THEN 1 ELSE 0 END), 0)
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id
	`
	args := []any{}
	if search != "" {
		query += ` WHERE q.domain LIKE ? OR q.client_ip LIKE ? OR q.query_type LIKE ? OR q.action LIKE ? OR q.source LIKE ? OR a.name LIKE ?`
		like := "%" + search + "%"
		args = append(args, like, like, like, like, like, like)
	}
	var dns, cache, upstream, blocked int
	_ = database.QueryRowContext(ctx, query, args...).Scan(&dns, &cache, &upstream, &blocked)
	system := persistedEventCount(ctx, database, search) + firstSeenDeviceEventCount(ctx, database, search)
	return map[string]int{
		"all":      dns + system,
		"dns":      dns,
		"cache":    cache,
		"upstream": upstream,
		"blocked":  blocked,
		"system":   system,
	}
}

func persistedEventCount(ctx context.Context, database *sql.DB, search string) int {
	query := `SELECT COUNT(*) FROM events`
	args := []any{}
	if search != "" {
		query += ` WHERE title LIKE ? OR description LIKE ? OR type LIKE ? OR domain LIKE ? OR client_ip LIKE ?`
		like := "%" + search + "%"
		args = append(args, like, like, like, like, like)
	}
	return scalarInt(ctx, database, query, args...)
}

func firstSeenDeviceEventCount(ctx context.Context, database *sql.DB, search string) int {
	query := `
		SELECT COUNT(*) FROM (
			SELECT q.client_ip
			FROM dns_queries q
			LEFT JOIN devices a ON a.id = q.device_id
			GROUP BY q.client_ip
	`
	args := []any{}
	if search != "" {
		query += ` HAVING q.client_ip LIKE ? OR COALESCE(a.name, '') LIKE ? OR COALESCE(a.location, '') LIKE ?`
		like := "%" + search + "%"
		args = append(args, like, like, like)
	}
	query += `)`
	return scalarInt(ctx, database, query, args...)
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

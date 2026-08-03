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
	counts := s.cachedActivityCounts(r.Context(), search)
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
	return activityRecordsToMaps(activityRecords(ctx, database, limit, 0, "", "system"))
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
	s.invalidateActivityCounts()
}

func localEvents(ctx context.Context, database *sql.DB, limit int, search string) []map[string]any {
	return activityEvents(ctx, database, limit, search, "all")
}

func pagedActivityEvents(ctx context.Context, database *sql.DB, page, pageSize int, search, scope string) []map[string]any {
	offset := (page - 1) * pageSize
	return activityRecordsToMaps(activityRecords(ctx, database, pageSize, offset, search, scope))
}

func activityEvents(ctx context.Context, database *sql.DB, limit int, search, scope string) []map[string]any {
	return activityRecordsToMaps(activityRecords(ctx, database, limit, 0, search, scope))
}

type activityRecord struct {
	kind             string
	recordID         int64
	recordKey        string
	timestamp        string
	eventType        string
	severity         string
	title            string
	description      string
	clientIP         string
	domain           string
	metadata         string
	source           string
	queryType        string
	action           string
	upstream         string
	rcode            string
	latency          sql.NullFloat64
	decisionReason   string
	decisionMetadata string
	deviceName       string
	location         string
}

type activityCountCacheEntry struct {
	counts    map[string]int
	expiresAt time.Time
}

// Counts do not need to change at query-ingest cadence. Keeping them for two
// live refresh intervals prevents the activity page from rescanning history
// on every five-second refresh while keeping the UI close to current.
const activityCountCacheTTL = 10 * time.Second

func activityRecords(ctx context.Context, database *sql.DB, limit, offset int, search, scope string) []activityRecord {
	query, args := activityRecordsQuery(search, scope)
	args = append(args, limit, offset)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return []activityRecord{}
	}
	defer rows.Close()

	items := make([]activityRecord, 0, limit)
	for rows.Next() {
		var item activityRecord
		if err := rows.Scan(
			&item.kind, &item.recordID, &item.recordKey, &item.timestamp, &item.eventType, &item.severity,
			&item.title, &item.description, &item.clientIP, &item.domain, &item.metadata, &item.source,
			&item.queryType, &item.action, &item.upstream, &item.rcode, &item.latency,
			&item.decisionReason, &item.decisionMetadata, &item.deviceName, &item.location,
		); err != nil {
			return items
		}
		items = append(items, item)
	}
	return items
}

func activityRecordsQuery(search, scope string) (string, []any) {
	parts := make([]string, 0, 3)
	args := []any{}

	if scope == "all" || scope == "system" {
		eventPart := `
			SELECT 'event' AS kind, e.id AS record_id, CAST(e.id AS TEXT) AS record_key, e.timestamp,
			       e.type AS event_type, e.severity, e.title, e.description,
			       COALESCE(e.client_ip, '') AS client_ip, COALESCE(e.domain, '') AS domain,
			       COALESCE(e.metadata, '{}') AS metadata, COALESCE(e.source, '') AS source,
			       '' AS query_type, '' AS action, '' AS upstream, '' AS rcode, NULL AS latency_ms,
			       '' AS decision_reason, '' AS decision_metadata, '' AS device_name, '' AS location
			FROM events e`
		if clause, values := activityLikeClause(search, "e.title", "e.description", "e.type", "e.domain", "e.client_ip"); clause != "" {
			eventPart += ` WHERE ` + clause
			args = append(args, values...)
		}
		parts = append(parts, eventPart)

		devicePart := `
			SELECT 'device' AS kind, 0 AS record_id, q.client_ip AS record_key, MIN(q.timestamp) AS timestamp,
			       'device.first_seen' AS event_type, 'info' AS severity, '' AS title, '' AS description,
			       q.client_ip AS client_ip, '' AS domain, '' AS metadata, 'devices' AS source,
			       '' AS query_type, '' AS action, '' AS upstream, '' AS rcode, NULL AS latency_ms,
			       '' AS decision_reason, '' AS decision_metadata,
			       COALESCE(MAX(a.name), '') AS device_name, COALESCE(MAX(a.location), '') AS location
			FROM dns_queries q
			LEFT JOIN devices a ON a.id = q.device_id
			GROUP BY q.client_ip`
		if clause, values := activityLikeClause(search, "q.client_ip", "COALESCE(MAX(a.name), '')", "COALESCE(MAX(a.location), '')"); clause != "" {
			devicePart += ` HAVING ` + clause
			args = append(args, values...)
		}
		parts = append(parts, devicePart)
	}

	if scope != "system" {
		queryPart := `
			SELECT 'query' AS kind, q.id AS record_id, CAST(q.id AS TEXT) AS record_key, q.timestamp,
			       '' AS event_type, '' AS severity, '' AS title, '' AS description,
			       q.client_ip AS client_ip, q.domain AS domain,
			       COALESCE(q.decision_metadata, '{}') AS metadata, q.source AS source,
			       q.query_type AS query_type, q.action AS action, q.upstream AS upstream, q.rcode AS rcode,
			       q.latency_ms AS latency_ms, q.decision_reason AS decision_reason,
			       COALESCE(q.decision_metadata, '{}') AS decision_metadata,
			       COALESCE(a.name, '') AS device_name, '' AS location
			FROM dns_queries q
			LEFT JOIN devices a ON a.id = q.device_id`
		clauses := []string{}
		if clause, values := activityLikeClause(search, "q.domain", "q.client_ip", "q.query_type", "q.action", "q.source", "a.name"); clause != "" {
			clauses = append(clauses, clause)
			args = append(args, values...)
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
			queryPart += ` WHERE ` + strings.Join(clauses, ` AND `)
		}
		parts = append(parts, queryPart)
	}

	query := `
		SELECT kind, record_id, record_key, timestamp, event_type, severity, title, description,
		       client_ip, domain, metadata, source, query_type, action, upstream, rcode, latency_ms,
		       decision_reason, decision_metadata, device_name, location
		FROM (` + strings.Join(parts, ` UNION ALL `) + `) activity
		ORDER BY julianday(timestamp) DESC,
		         CASE kind WHEN 'event' THEN 0 WHEN 'device' THEN 1 ELSE 2 END,
		         record_id DESC, record_key DESC
		LIMIT ? OFFSET ?`
	return query, args
}

func activityLikeClause(search string, fields ...string) (string, []any) {
	if strings.TrimSpace(search) == "" {
		return "", nil
	}
	clauses := make([]string, 0, len(fields))
	args := make([]any, 0, len(fields))
	like := "%" + search + "%"
	for _, field := range fields {
		clauses = append(clauses, field+` LIKE ?`)
		args = append(args, like)
	}
	return strings.Join(clauses, ` OR `), args
}

func activityRecordsToMaps(records []activityRecord) []map[string]any {
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		switch record.kind {
		case "event":
			items = append(items, map[string]any{
				"id":          fmt.Sprintf("event-%d", record.recordID),
				"timestamp":   record.timestamp,
				"type":        record.eventType,
				"severity":    record.severity,
				"title":       record.title,
				"description": record.description,
				"client_ip":   nullableInput(record.clientIP),
				"domain":      nullableInput(record.domain),
				"metadata":    metadataMap(record.metadata),
				"source":      record.source,
			})
		case "device":
			deviceName := record.clientIP
			if record.deviceName != "" {
				deviceName = record.deviceName
			}
			description := deviceName + " joined the network."
			if record.location != "" {
				description = deviceName + " joined from " + record.location + "."
			}
			items = append(items, map[string]any{
				"id":          "device-first-seen-" + record.clientIP,
				"timestamp":   record.timestamp,
				"type":        "device.first_seen",
				"severity":    "info",
				"title":       "Device first seen",
				"description": description,
				"client_ip":   record.clientIP,
				"domain":      nil,
				"metadata":    map[string]any{"device_name": deviceName, "location": nullableInput(record.location)},
				"source":      "devices",
			})
		default:
			deviceName := record.clientIP
			if record.deviceName != "" {
				deviceName = record.deviceName
			}
			eventType := "dns.query"
			severity := "info"
			title := record.domain + " resolved"
			description := "Requested by " + deviceName + "."
			if record.source == "cache" {
				description = "Served from Faro's cache for " + deviceName + "."
			} else if record.source == "upstream" && record.upstream != "" {
				description = "Resolved through " + record.upstream + " for " + deviceName + "."
			} else if record.source == "local" {
				description = "Answered by Local DNS for " + deviceName + "."
			}
			if record.action == "blocked" {
				eventType = "dns.blocked"
				severity = "warning"
				title = "Domain blocked"
				description = record.domain + " was blocked by " + record.source + "."
			}
			if record.decisionReason != "" {
				description = record.decisionReason
			}
			items = append(items, map[string]any{
				"id":          fmt.Sprintf("query-%d", record.recordID),
				"timestamp":   record.timestamp,
				"type":        eventType,
				"severity":    severity,
				"title":       title,
				"description": description,
				"client_ip":   record.clientIP,
				"domain":      record.domain,
				"metadata": map[string]any{
					"query_type": record.queryType,
					"action":     record.action,
					"latency_ms": nullableFloat(record.latency),
					"upstream":   record.upstream,
					"rcode":      record.rcode,
					"decision":   metadataMap(record.decisionMetadata),
				},
				"source": record.source,
			})
		}
	}
	return items
}

func activityCounts(ctx context.Context, database *sql.DB, search string) map[string]int {
	parts := make([]string, 0, 3)
	args := []any{}

	eventPart := `SELECT 'event' AS kind, '' AS source, '' AS action FROM events e`
	if clause, values := activityLikeClause(search, "e.title", "e.description", "e.type", "e.domain", "e.client_ip"); clause != "" {
		eventPart += ` WHERE ` + clause
		args = append(args, values...)
	}
	parts = append(parts, eventPart)

	devicePart := `
		SELECT 'device' AS kind, '' AS source, '' AS action
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id
		GROUP BY q.client_ip`
	if clause, values := activityLikeClause(search, "q.client_ip", "COALESCE(MAX(a.name), '')", "COALESCE(MAX(a.location), '')"); clause != "" {
		devicePart += ` HAVING ` + clause
		args = append(args, values...)
	}
	parts = append(parts, devicePart)

	queryPart := `
		SELECT 'query' AS kind, q.source, q.action
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id`
	if clause, values := activityLikeClause(search, "q.domain", "q.client_ip", "q.query_type", "q.action", "q.source", "a.name"); clause != "" {
		queryPart += ` WHERE ` + clause
		args = append(args, values...)
	}
	parts = append(parts, queryPart)

	query := `
		SELECT
			COALESCE(SUM(CASE WHEN kind = 'query' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'query' AND source = 'cache' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'query' AND source = 'upstream' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'query' AND action = 'blocked' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind IN ('event', 'device') THEN 1 ELSE 0 END), 0)
		FROM (` + strings.Join(parts, ` UNION ALL `) + `) activity`

	var dns, cache, upstream, blocked, system int
	if err := database.QueryRowContext(ctx, query, args...).Scan(&dns, &cache, &upstream, &blocked, &system); err != nil {
		return map[string]int{"all": 0, "dns": 0, "cache": 0, "upstream": 0, "blocked": 0, "system": 0}
	}
	return map[string]int{
		"all":      dns + system,
		"dns":      dns,
		"cache":    cache,
		"upstream": upstream,
		"blocked":  blocked,
		"system":   system,
	}
}

func (s *Handler) cachedActivityCounts(ctx context.Context, search string) map[string]int {
	now := time.Now()
	s.activityCountsMu.Lock()
	if entry, ok := s.activityCountsCache[search]; ok && now.Before(entry.expiresAt) {
		counts := cloneActivityCounts(entry.counts)
		s.activityCountsMu.Unlock()
		return counts
	}
	counts := activityCounts(ctx, s.store.DB, search)
	if s.activityCountsCache == nil {
		s.activityCountsCache = map[string]activityCountCacheEntry{}
	}
	if len(s.activityCountsCache) >= 32 {
		for key := range s.activityCountsCache {
			delete(s.activityCountsCache, key)
			break
		}
	}
	s.activityCountsCache[search] = activityCountCacheEntry{counts: counts, expiresAt: now.Add(activityCountCacheTTL)}
	s.activityCountsMu.Unlock()
	return cloneActivityCounts(counts)
}

func (s *Handler) invalidateActivityCounts() {
	s.activityCountsMu.Lock()
	s.activityCountsCache = nil
	s.activityCountsMu.Unlock()
}

func cloneActivityCounts(counts map[string]int) map[string]int {
	result := make(map[string]int, len(counts))
	for key, value := range counts {
		result[key] = value
	}
	return result
}

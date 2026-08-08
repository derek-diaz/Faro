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

const (
	maxActivitySearchLength = 100
	maxActivityPage         = 10000
	maxActivityRange        = 90 * 24 * time.Hour
)

type activityWindow struct {
	enabled       bool
	from          time.Time
	to            time.Time
	bucketSeconds int
}

type activityTimelineBucket struct {
	Timestamp string `json:"timestamp"`
	Total     int    `json:"total"`
	Blocked   int    `json:"blocked"`
}

type activityTimeline struct {
	From          string                   `json:"from"`
	To            string                   `json:"to"`
	BucketSeconds int                      `json:"bucket_seconds"`
	Buckets       []activityTimelineBucket `json:"buckets"`
}

func (handler *Handler) queries(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	search := strings.TrimSpace(request.URL.Query().Get("search"))
	if len(search) > maxActivitySearchLength {
		writeBadRequest(responseWriter, fmt.Errorf("search must be %d characters or fewer", maxActivitySearchLength))
		return
	}
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	query := `
		SELECT id, timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms, rcode, decision_reason, decision_metadata
		FROM dns_queries
		WHERE ? = '' OR domain LIKE ? OR client_ip LIKE ?
		ORDER BY timestamp DESC
		LIMIT ?`
	like := "%" + search + "%"
	rows, err := handler.store.DB.QueryContext(request.Context(), query, search, like, like, limit)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	defer closeRows(rows)
	writeRows(responseWriter, rows)
}

func (handler *Handler) events(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	page := positiveInt(request.URL.Query().Get("page"), 1, maxActivityPage)
	pageSize := positiveInt(request.URL.Query().Get("page_size"), 50, 200)
	scope := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("scope")))
	if !validActivityScope(scope) {
		scope = "all"
	}
	search := strings.TrimSpace(request.URL.Query().Get("search"))
	if len(search) > maxActivitySearchLength {
		writeBadRequest(responseWriter, fmt.Errorf("search must be %d characters or fewer", maxActivitySearchLength))
		return
	}
	window, err := parseActivityWindow(request.URL.Query())
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	counts := handler.cachedActivityCounts(request.Context(), search, window)
	total := counts[scope]
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"items":       pagedActivityEvents(request.Context(), handler.store.DB, page, pageSize, search, scope, window),
		"page":        page,
		"page_size":   pageSize,
		"total":       total,
		"total_pages": totalPages,
		"counts":      counts,
		"timeline":    activityTimelineFor(request.Context(), handler.store.DB, search, scope, window),
	})
}

func parseActivityWindow(values url.Values) (activityWindow, error) {
	preset := strings.ToLower(strings.TrimSpace(values.Get("range")))
	if preset == "" || preset == "all" {
		return activityWindow{}, nil
	}

	now := time.Now().UTC()
	if preset == "custom" {
		from, err := parseActivityTime(values.Get("from"))
		if err != nil {
			return activityWindow{}, fmt.Errorf("invalid activity range start")
		}
		to, err := parseActivityTime(values.Get("to"))
		if err != nil {
			return activityWindow{}, fmt.Errorf("invalid activity range end")
		}
		return newActivityWindow(from, to)
	}

	durations := map[string]time.Duration{
		"15m": 15 * time.Minute,
		"1h":  time.Hour,
		"4h":  4 * time.Hour,
		"24h": 24 * time.Hour,
		"7d":  7 * 24 * time.Hour,
		"30d": 30 * 24 * time.Hour,
		"90d": 90 * 24 * time.Hour,
	}
	duration, ok := durations[preset]
	if !ok {
		return activityWindow{}, fmt.Errorf("unsupported activity range %q", preset)
	}
	return newActivityWindow(now.Add(-duration), now)
}

func parseActivityTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, fmt.Errorf("activity time is required")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func newActivityWindow(from, to time.Time) (activityWindow, error) {
	from = from.UTC()
	to = to.UTC()
	span := to.Sub(from)
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return activityWindow{}, fmt.Errorf("activity range must have a start before its end")
	}
	if span > maxActivityRange {
		return activityWindow{}, fmt.Errorf("activity range cannot exceed %d days", int(maxActivityRange/(24*time.Hour)))
	}
	return activityWindow{
		enabled:       true,
		from:          from,
		to:            to,
		bucketSeconds: activityBucketSeconds(span),
	}, nil
}

func activityBucketSeconds(span time.Duration) int {
	switch {
	case span <= 30*time.Minute:
		return 60
	case span <= 2*time.Hour:
		return 5 * 60
	case span <= 12*time.Hour:
		return 15 * 60
	case span <= 48*time.Hour:
		return 30 * 60
	case span <= 14*24*time.Hour:
		return 6 * 60 * 60
	default:
		return 24 * 60 * 60
	}
}

func (window activityWindow) cacheKey() string {
	if !window.enabled {
		return "all"
	}
	return window.from.Format(time.RFC3339Nano) + "|" + window.to.Format(time.RFC3339Nano)
}

func activityTimeClause(field string, window activityWindow) (string, []any) {
	if !window.enabled {
		return "", nil
	}
	return fmt.Sprintf("julianday(%s) >= julianday(?) AND julianday(%s) < julianday(?)", field, field), []any{
		window.from.Format(time.RFC3339Nano),
		window.to.Format(time.RFC3339Nano),
	}
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

func (handler *Handler) notifications(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	userID, ok := auth.UserID(request.Context())
	if !ok {
		writeJSON(responseWriter, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	allEvents := notificationCandidates(request.Context(), handler.store.DB, 1000)
	states, readAllAt, err := loadNotificationStates(request.Context(), handler.store.DB, userID)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	notifications, attentionCount, unreadCount := collectNotifications(allEvents, states, readAllAt)
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"attention_count": attentionCount,
		"unread_count":    unreadCount,
		"items":           notifications,
	})
}

type storedNotificationState struct {
	read      bool
	dismissed bool
}

func (handler *Handler) notificationState(responseWriter http.ResponseWriter, request *http.Request) {
	userID, ok := auth.UserID(request.Context())
	if !ok {
		writeJSON(responseWriter, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/notifications/"), "/")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if path == "read-all" {
		if request.Method != http.MethodPost {
			methodNotAllowed(responseWriter)
			return
		}
		_, err := handler.store.DB.ExecContext(request.Context(), `
			INSERT INTO notification_states(user_id, event_key, read_at, updated_at) VALUES(?, '*', ?, ?)
			ON CONFLICT(user_id, event_key) DO UPDATE SET read_at = excluded.read_at, updated_at = excluded.updated_at
		`, userID, now, now)
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
		return
	}

	markRead := strings.HasSuffix(path, "/read")
	rawKey := strings.TrimSuffix(path, "/read")
	eventKey, err := url.PathUnescape(strings.Trim(rawKey, "/"))
	if err != nil || !validNotificationKey(eventKey) {
		writeBadRequest(responseWriter, fmt.Errorf("invalid notification id"))
		return
	}
	if markRead {
		if request.Method != http.MethodPut {
			methodNotAllowed(responseWriter)
			return
		}
		_, err = handler.store.DB.ExecContext(request.Context(), `
			INSERT INTO notification_states(user_id, event_key, read_at, updated_at) VALUES(?, ?, ?, ?)
			ON CONFLICT(user_id, event_key) DO UPDATE SET read_at = excluded.read_at, updated_at = excluded.updated_at
		`, userID, eventKey, now, now)
	} else {
		if request.Method != http.MethodDelete {
			methodNotAllowed(responseWriter)
			return
		}
		_, err = handler.store.DB.ExecContext(request.Context(), `
			INSERT INTO notification_states(user_id, event_key, read_at, dismissed_at, updated_at) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(user_id, event_key) DO UPDATE SET
				read_at = COALESCE(notification_states.read_at, excluded.read_at),
				dismissed_at = excluded.dismissed_at,
				updated_at = excluded.updated_at
		`, userID, eventKey, now, now, now)
	}
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func collectNotifications(events []map[string]any, states map[string]storedNotificationState, readAllAt time.Time) ([]map[string]any, int, int) {
	notifications := make([]map[string]any, 0, 10)
	attentionCount := 0
	unreadCount := 0
	for _, event := range events {
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
		if len(notifications) < cap(notifications) {
			event["is_read"] = isRead
			notifications = append(notifications, event)
		}
	}
	return notifications, attentionCount, unreadCount
}

func notificationCandidates(ctx context.Context, database *sql.DB, limit int) []map[string]any {
	return activityRecordsToMaps(activityRecords(ctx, database, limit, 0, "", "system", activityWindow{}))
}

func loadNotificationStates(ctx context.Context, database *sql.DB, userID int64) (map[string]storedNotificationState, time.Time, error) {
	rows, err := database.QueryContext(ctx, `SELECT event_key, read_at, dismissed_at FROM notification_states WHERE user_id = ?`, userID)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer closeRows(rows)
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

func (handler *Handler) recordEvent(ctx context.Context, event eventInput) {
	if !handler.store.ActivityStorageWriteAllowed() {
		return
	}
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
	_, err := handler.store.DB.ExecContext(ctx, `
		INSERT INTO events(type, severity, title, description, client_ip, domain, metadata, source)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, event.Type, severity, event.Title, event.Description, nullableInput(event.ClientIP), nullableInput(event.Domain), metadata, source)
	if err != nil {
		handler.store.ReportActivityWriteFailure(err)
		return
	}
	handler.store.ReportActivityWriteSuccess()
	handler.invalidateActivityCounts()
}

func localEvents(ctx context.Context, database *sql.DB, limit int, search string) []map[string]any {
	return activityEvents(ctx, database, limit, search, "all")
}

func pagedActivityEvents(ctx context.Context, database *sql.DB, page, pageSize int, search, scope string, window activityWindow) []map[string]any {
	offset := (page - 1) * pageSize
	return activityRecordsToMaps(activityRecords(ctx, database, pageSize, offset, search, scope, window))
}

func activityEvents(ctx context.Context, database *sql.DB, limit int, search, scope string) []map[string]any {
	return activityRecordsToMaps(activityRecords(ctx, database, limit, 0, search, scope, activityWindow{}))
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

const (
	queryClientIPField       = "q.client_ip"
	eventTitleField          = "e.title"
	eventDescriptionField    = "e.description"
	eventTypeField           = "e.type"
	eventDomainField         = "e.domain"
	eventClientIPField       = "e.client_ip"
	eventTimestampField      = "e.timestamp"
	queryDomainField         = "q.domain"
	queryTypeField           = "q.query_type"
	queryActionField         = "q.action"
	querySourceField         = "q.source"
	queryTimestampField      = "q.timestamp"
	queryDeviceNameField     = "a.name"
	deviceNameAggregate      = "COALESCE(MAX(a.name), '')"
	deviceLocationAggregate  = "COALESCE(MAX(a.location), '')"
	deviceTimestampAggregate = "MIN(q.timestamp)"
)

func activityRecords(ctx context.Context, database *sql.DB, limit, offset int, search, scope string, window activityWindow) []activityRecord {
	query, args := activityRecordsQuery(search, scope, window)
	args = append(args, limit, offset)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return make([]activityRecord, 0)
	}
	defer closeRows(rows)

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

func activityRecordsQuery(search, scope string, window activityWindow) (string, []any) {
	parts := make([]string, 0, 3)
	args := make([]any, 0, 6)

	if scope == "all" || scope == "system" {
		eventPart, eventArgs := activityEventRecordsPart(search, window)
		parts = append(parts, eventPart)
		args = append(args, eventArgs...)

		devicePart, deviceArgs := activityDeviceRecordsPart(search, window)
		parts = append(parts, devicePart)
		args = append(args, deviceArgs...)
	}

	if scope != "system" {
		queryPart, queryArgs := activityQueryRecordsPart(search, scope, window)
		parts = append(parts, queryPart)
		args = append(args, queryArgs...)
	}

	query := activityRecordsUnionQuery(parts)
	return query, args
}

func activityEventRecordsPart(search string, window activityWindow) (string, []any) {
	query := `
		SELECT 'event' AS kind, e.id AS record_id, CAST(e.id AS TEXT) AS record_key, e.timestamp,
		       e.type AS event_type, e.severity, e.title, e.description,
		       COALESCE(e.client_ip, '') AS client_ip, COALESCE(e.domain, '') AS domain,
		       e.metadata AS metadata, e.source AS source,
		       '' AS query_type, '' AS action, '' AS upstream, '' AS rcode, NULL AS latency_ms,
		       '' AS decision_reason, '' AS decision_metadata, '' AS device_name, '' AS location
		FROM events e`
	clauses, args := activityFilterClauses(search, []string{eventTitleField, eventDescriptionField, eventTypeField, eventDomainField, eventClientIPField}, eventTimestampField, window)
	return activityWithClauses(query, "WHERE", clauses), args
}

func activityDeviceRecordsPart(search string, window activityWindow) (string, []any) {
	query := `
		SELECT 'device' AS kind, 0 AS record_id, q.client_ip AS record_key, MIN(q.timestamp) AS timestamp,
		       'device.first_seen' AS event_type, 'info' AS severity, '' AS title, '' AS description,
		       q.client_ip AS client_ip, '' AS domain, '' AS metadata, 'devices' AS source,
		       '' AS query_type, '' AS action, '' AS upstream, '' AS rcode, NULL AS latency_ms,
		       '' AS decision_reason, '' AS decision_metadata,
		       COALESCE(MAX(a.name), '') AS device_name, COALESCE(MAX(a.location), '') AS location
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id
		GROUP BY q.client_ip`
	clauses, args := activityFilterClauses(search, []string{queryClientIPField, deviceNameAggregate, deviceLocationAggregate}, deviceTimestampAggregate, window)
	return activityWithClauses(query, "HAVING", clauses), args
}

func activityQueryRecordsPart(search, scope string, window activityWindow) (string, []any) {
	query := `
		SELECT 'query' AS kind, q.id AS record_id, CAST(q.id AS TEXT) AS record_key, q.timestamp,
		       '' AS event_type, '' AS severity, '' AS title, '' AS description,
		       q.client_ip AS client_ip, q.domain AS domain,
		       q.decision_metadata AS metadata, q.source AS source,
		       q.query_type AS query_type, q.action AS action, q.upstream AS upstream, q.rcode AS rcode,
		       q.latency_ms AS latency_ms, q.decision_reason AS decision_reason,
		       q.decision_metadata AS decision_metadata,
		       COALESCE(a.name, '') AS device_name, '' AS location
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id`
	clauses, args := activityFilterClauses(search, []string{queryDomainField, queryClientIPField, queryTypeField, queryActionField, querySourceField, queryDeviceNameField}, queryTimestampField, window)
	switch scope {
	case "cache":
		clauses = append(clauses, `q.source = 'cache'`)
	case "upstream":
		clauses = append(clauses, `q.source = 'upstream'`)
	case "blocked":
		clauses = append(clauses, `q.action = 'blocked'`)
	}
	return activityWithClauses(query, "WHERE", clauses), args
}

func activityFilterClauses(search string, fields []string, timeField string, window activityWindow) ([]string, []any) {
	clauses := make([]string, 0, 2)
	args := make([]any, 0, len(fields)+2)
	if clause, values := activityLikeClause(search, fields...); clause != "" {
		clauses = append(clauses, clause)
		args = append(args, values...)
	}
	if clause, values := activityTimeClause(timeField, window); clause != "" {
		clauses = append(clauses, clause)
		args = append(args, values...)
	}
	return clauses, args
}

func activityWithClauses(query, keyword string, clauses []string) string {
	if len(clauses) == 0 {
		return query
	}
	return query + " " + keyword + " " + strings.Join(clauses, ` AND `)
}

func activityRecordsUnionQuery(parts []string) string {
	const placeholder = "faro_activity_records"
	query := `
		SELECT kind, record_id, record_key, timestamp, event_type, severity, title, description,
		       client_ip, domain, metadata, source, query_type, action, upstream, rcode, latency_ms,
		       decision_reason, decision_metadata, device_name, location
		FROM faro_activity_records activity
		ORDER BY julianday(timestamp) DESC,
		         CASE kind WHEN 'event' THEN 0 WHEN 'device' THEN 1 ELSE 2 END,
		         record_id DESC, record_key DESC
		LIMIT ? OFFSET ?`
	return strings.Replace(query, placeholder, "("+strings.Join(parts, ` UNION ALL `)+")", 1)
}

func activityTimelineFor(ctx context.Context, database *sql.DB, search, scope string, window activityWindow) *activityTimeline {
	if !window.enabled {
		return nil
	}

	from := window.from.Format(time.RFC3339Nano)
	bucketSeconds := window.bucketSeconds
	parts, args := activityTimelineParts(search, scope, window, from, bucketSeconds)
	query := activityTimelineUnionQuery(parts)

	bucketDuration := time.Duration(window.bucketSeconds) * time.Second
	bucketCount := int(window.to.Sub(window.from) / bucketDuration)
	if window.to.Sub(window.from)%bucketDuration != 0 {
		bucketCount++
	}
	if bucketCount < 1 {
		bucketCount = 1
	}
	timeline := &activityTimeline{
		From:          window.from.Format(time.RFC3339Nano),
		To:            window.to.Format(time.RFC3339Nano),
		BucketSeconds: window.bucketSeconds,
		Buckets:       make([]activityTimelineBucket, bucketCount),
	}
	for index := range timeline.Buckets {
		timeline.Buckets[index].Timestamp = window.from.Add(time.Duration(index) * bucketDuration).Format(time.RFC3339Nano)
	}

	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return timeline
	}
	defer closeRows(rows)
	for rows.Next() {
		var index, total, blocked int
		if err := rows.Scan(&index, &total, &blocked); err != nil {
			return timeline
		}
		if index >= 0 && index < len(timeline.Buckets) {
			timeline.Buckets[index].Total = total
			timeline.Buckets[index].Blocked = blocked
		}
	}
	return timeline
}

func activityTimelineParts(search, scope string, window activityWindow, from string, bucketSeconds int) ([]string, []any) {
	parts := make([]string, 0, 3)
	args := make([]any, 0, 12)
	if scope == "all" || scope == "system" {
		eventPart, eventArgs := activityTimelineEventPart(search, window, from, bucketSeconds)
		parts = append(parts, eventPart)
		args = append(args, eventArgs...)
		devicePart, deviceArgs := activityTimelineDevicePart(search, window, from, bucketSeconds)
		parts = append(parts, devicePart)
		args = append(args, deviceArgs...)
	}
	if scope != "system" {
		queryPart, queryArgs := activityTimelineQueryPart(search, scope, window, from, bucketSeconds)
		parts = append(parts, queryPart)
		args = append(args, queryArgs...)
	}
	return parts, args
}

func activityTimelineEventPart(search string, window activityWindow, from string, bucketSeconds int) (string, []any) {
	query := `SELECT faro_activity_bucket AS bucket_index, 1 AS total, 0 AS blocked FROM events e`
	query = strings.Replace(query, "faro_activity_bucket", activityTimelineBucketExpression(eventTimestampField), 1)
	args := []any{from, bucketSeconds}
	clauses, clauseArgs := activityFilterClauses(search, []string{eventTitleField, eventDescriptionField, eventTypeField, eventDomainField, eventClientIPField}, eventTimestampField, window)
	args = append(args, clauseArgs...)
	return activityWithClauses(query, "WHERE", clauses), args
}

func activityTimelineDevicePart(search string, window activityWindow, from string, bucketSeconds int) (string, []any) {
	query := `
		SELECT faro_activity_bucket AS bucket_index, 1 AS total, 0 AS blocked
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id
		GROUP BY q.client_ip`
	query = strings.Replace(query, "faro_activity_bucket", activityTimelineBucketExpression(deviceTimestampAggregate), 1)
	args := []any{from, bucketSeconds}
	clauses, clauseArgs := activityFilterClauses(search, []string{queryClientIPField, deviceNameAggregate, deviceLocationAggregate}, deviceTimestampAggregate, window)
	args = append(args, clauseArgs...)
	return activityWithClauses(query, "HAVING", clauses), args
}

func activityTimelineQueryPart(search, scope string, window activityWindow, from string, bucketSeconds int) (string, []any) {
	query := `
		SELECT faro_activity_bucket AS bucket_index, 1 AS total,
		       CASE WHEN q.action = 'blocked' THEN 1 ELSE 0 END AS blocked
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id`
	query = strings.Replace(query, "faro_activity_bucket", activityTimelineBucketExpression(queryTimestampField), 1)
	args := []any{from, bucketSeconds}
	clauses, clauseArgs := activityFilterClauses(search, []string{queryDomainField, queryClientIPField, queryTypeField, queryActionField, querySourceField, queryDeviceNameField}, queryTimestampField, window)
	args = append(args, clauseArgs...)
	switch scope {
	case "cache":
		clauses = append(clauses, `q.source = 'cache'`)
	case "upstream":
		clauses = append(clauses, `q.source = 'upstream'`)
	case "blocked":
		clauses = append(clauses, `q.action = 'blocked'`)
	}
	return activityWithClauses(query, "WHERE", clauses), args
}

func activityTimelineUnionQuery(parts []string) string {
	const placeholder = "faro_activity_timeline"
	query := `
		SELECT bucket_index, COALESCE(SUM(total), 0), COALESCE(SUM(blocked), 0)
		FROM faro_activity_timeline timeline
		GROUP BY bucket_index
		ORDER BY bucket_index`
	return strings.Replace(query, placeholder, "("+strings.Join(parts, ` UNION ALL `)+")", 1)
}

func activityTimelineBucketExpression(field string) string {
	return fmt.Sprintf("CAST(((julianday(%s) - julianday(?)) * 86400.0) / ? AS INTEGER)", field)
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
		var item map[string]any
		switch record.kind {
		case "event":
			item = eventRecordMap(record)
		case "device":
			item = deviceRecordMap(record)
		default:
			item = queryRecordMap(record)
		}
		items = append(items, item)
	}
	return items
}

func eventRecordMap(record activityRecord) map[string]any {
	return map[string]any{
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
	}
}

func deviceRecordMap(record activityRecord) map[string]any {
	deviceName := record.clientIP
	if record.deviceName != "" {
		deviceName = record.deviceName
	}
	description := deviceName + " joined the network."
	if record.location != "" {
		description = deviceName + " joined from " + record.location + "."
	}
	return map[string]any{
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
	}
}

func queryRecordMap(record activityRecord) map[string]any {
	deviceName := record.clientIP
	if record.deviceName != "" {
		deviceName = record.deviceName
	}
	eventType, severity, title := "dns.query", "info", record.domain+" resolved"
	description := "Requested by " + deviceName + "."
	switch {
	case record.source == "cache":
		description = "Served from Faro's cache for " + deviceName + "."
	case record.source == "upstream" && record.upstream != "":
		description = "Resolved through " + record.upstream + " for " + deviceName + "."
	case record.source == "local":
		description = "Answered by Local DNS for " + deviceName + "."
	}
	if record.action == "blocked" {
		eventType, severity, title = "dns.blocked", "warning", "Domain blocked"
		description = record.domain + " was blocked by " + record.source + "."
	}
	if record.decisionReason != "" {
		description = record.decisionReason
	}
	return map[string]any{
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
	}
}

func activityCounts(ctx context.Context, database *sql.DB, search string, window activityWindow) map[string]int {
	parts := make([]string, 0, 3)
	args := make([]any, 0, 6)

	eventPart := `SELECT 'event' AS kind, '' AS source, '' AS action FROM events e`
	eventClauses, eventArgs := activityFilterClauses(search, []string{eventTitleField, eventDescriptionField, eventTypeField, eventDomainField, eventClientIPField}, eventTimestampField, window)
	eventPart = activityWithClauses(eventPart, "WHERE", eventClauses)
	parts = append(parts, eventPart)
	args = append(args, eventArgs...)

	devicePart := `
		SELECT 'device' AS kind, '' AS source, '' AS action
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id
		GROUP BY q.client_ip`
	deviceClauses, deviceArgs := activityFilterClauses(search, []string{queryClientIPField, deviceNameAggregate, deviceLocationAggregate}, deviceTimestampAggregate, window)
	devicePart = activityWithClauses(devicePart, "HAVING", deviceClauses)
	parts = append(parts, devicePart)
	args = append(args, deviceArgs...)

	queryPart := `
		SELECT 'query' AS kind, q.source, q.action
		FROM dns_queries q
		LEFT JOIN devices a ON a.id = q.device_id`
	queryClauses, queryArgs := activityFilterClauses(search, []string{queryDomainField, queryClientIPField, queryTypeField, queryActionField, querySourceField, queryDeviceNameField}, queryTimestampField, window)
	queryPart = activityWithClauses(queryPart, "WHERE", queryClauses)
	parts = append(parts, queryPart)
	args = append(args, queryArgs...)

	query := activityCountsQuery(parts)

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

func activityCountsQuery(parts []string) string {
	const placeholder = "faro_activity_counts"
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN kind = 'query' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'query' AND source = 'cache' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'query' AND source = 'upstream' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'query' AND action = 'blocked' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind IN ('event', 'device') THEN 1 ELSE 0 END), 0)
		FROM faro_activity_counts activity`
	return strings.Replace(query, placeholder, "("+strings.Join(parts, ` UNION ALL `)+")", 1)
}

func (handler *Handler) cachedActivityCounts(ctx context.Context, search string, window activityWindow) map[string]int {
	now := time.Now()
	cacheKey := search + "\x00" + window.cacheKey()
	handler.activityCountsMu.Lock()
	if entry, ok := handler.activityCountsCache[cacheKey]; ok && now.Before(entry.expiresAt) {
		counts := cloneActivityCounts(entry.counts)
		handler.activityCountsMu.Unlock()
		return counts
	}
	counts := activityCounts(ctx, handler.store.DB, search, window)
	if handler.activityCountsCache == nil {
		handler.activityCountsCache = map[string]activityCountCacheEntry{}
	}
	if len(handler.activityCountsCache) >= 32 {
		for key := range handler.activityCountsCache {
			delete(handler.activityCountsCache, key)
			break
		}
	}
	handler.activityCountsCache[cacheKey] = activityCountCacheEntry{counts: counts, expiresAt: now.Add(activityCountCacheTTL)}
	handler.activityCountsMu.Unlock()
	return cloneActivityCounts(counts)
}

func (handler *Handler) invalidateActivityCounts() {
	handler.activityCountsMu.Lock()
	handler.activityCountsCache = nil
	handler.activityCountsMu.Unlock()
}

func cloneActivityCounts(counts map[string]int) map[string]int {
	result := make(map[string]int, len(counts))
	for key, value := range counts {
		result[key] = value
	}
	return result
}

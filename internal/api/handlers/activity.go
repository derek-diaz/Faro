package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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

func (s *Handler) notifications(w http.ResponseWriter, r *http.Request) {
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
		SELECT q.id, q.timestamp, q.client_ip, q.domain, q.query_type, q.action, q.source, q.upstream, q.latency_ms,
		       q.rcode, q.decision_reason, q.decision_metadata, COALESCE(a.name, '')
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

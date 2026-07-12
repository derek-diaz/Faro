package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

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

func ternary(condition bool, truthy, falsy string) string {
	if condition {
		return truthy
	}
	return falsy
}

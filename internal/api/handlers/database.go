package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata"
)

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("close database rows: %v", err)
	}
}

func rollbackTransaction(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.Printf("rollback transaction: %v", err)
	}
}

func writeRows(w http.ResponseWriter, rows *sql.Rows) {
	items, err := scanRows(rows)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0)
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		items = append(items, databaseRow(columns, values))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func databaseRow(columns []string, values []any) map[string]any {
	row := make(map[string]any, len(columns))
	for i, column := range columns {
		if column == "decision_metadata" {
			row["decision"] = metadataMap(decisionMetadataString(values[i]))
			continue
		}
		switch value := values[i].(type) {
		case []byte:
			row[column] = string(value)
		case int64:
			row[column] = value
			if column == "enabled" {
				row[column] = value == 1
			}
		default:
			row[column] = value
		}
	}
	return row
}

func grouped(ctx context.Context, database *sql.DB, query string, args ...any) []map[string]any {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return make([]map[string]any, 0)
	}
	defer closeRows(rows)
	result := make([]map[string]any, 0)
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
	rows, err := database.QueryContext(ctx, `SELECT timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms, rcode, decision_reason, decision_metadata FROM dns_queries ORDER BY timestamp DESC LIMIT 8`)
	if err != nil {
		return make([]map[string]any, 0)
	}
	defer closeRows(rows)
	items, err := scanRows(rows)
	if err != nil {
		return make([]map[string]any, 0)
	}
	return items
}

func recentQueriesFor(ctx context.Context, database *sql.DB, where string, args ...any) []map[string]any {
	query := `SELECT id, timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms, rcode, decision_reason, decision_metadata FROM dns_queries WHERE ` + where + ` ORDER BY timestamp DESC LIMIT 12`
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return make([]map[string]any, 0)
	}
	defer closeRows(rows)
	items, err := scanRows(rows)
	if err != nil {
		return make([]map[string]any, 0)
	}
	return items
}

func decisionMetadataString(value any) string {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case string:
		return typed
	default:
		return "{}"
	}
}

func searchRows(ctx context.Context, database *sql.DB, query string, args ...any) []map[string]any {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return make([]map[string]any, 0)
	}
	defer closeRows(rows)
	items := make([]map[string]any, 0)
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
		return make([]string, 0)
	}
	defer closeRows(rows)
	labels := make([]string, 0)
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

func todayStart(r *http.Request) string {
	timezone := ""
	if r != nil {
		timezone = r.Header.Get("X-Faro-Timezone")
	}
	return localDayStart(time.Now(), timezone)
}

func localDayStart(now time.Time, timezone string) string {
	location := time.UTC
	if requested := strings.TrimSpace(timezone); requested != "" {
		if parsed, err := time.LoadLocation(requested); err == nil {
			location = parsed
		}
	}
	localNow := now.In(location)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	return start.UTC().Format(time.RFC3339)
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

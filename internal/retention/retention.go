package retention

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/derek/faro/internal/db"
)

const (
	DefaultDays = 30
	MinDays     = 1
	MaxDays     = 3650
)

type Stats struct {
	DatabaseBytes            int64  `json:"database_bytes"`
	DatabaseUsedBytes        int64  `json:"database_used_bytes"`
	DatabaseReclaimableBytes int64  `json:"database_reclaimable_bytes"`
	QueryCount               int64  `json:"query_count"`
	EventCount               int64  `json:"event_count"`
	OldestQuery              string `json:"oldest_query,omitempty"`
	OldestEvent              string `json:"oldest_event,omitempty"`
	RetentionDays            int    `json:"retention_days"`
	RetentionCutoff          string `json:"retention_cutoff"`
	LastPrunedAt             string `json:"last_pruned_at,omitempty"`
	LastQueriesDeleted       int64  `json:"last_queries_deleted"`
	LastEventsDeleted        int64  `json:"last_events_deleted"`
}

type Result struct {
	QueriesDeleted int64  `json:"queries_deleted"`
	EventsDeleted  int64  `json:"events_deleted"`
	BeforeBytes    int64  `json:"before_bytes"`
	AfterBytes     int64  `json:"after_bytes"`
	ReclaimedBytes int64  `json:"reclaimed_bytes"`
	RetentionDays  int    `json:"retention_days"`
	Cutoff         string `json:"cutoff"`
	Compacted      bool   `json:"compacted"`
	CompletedAt    string `json:"completed_at"`
}

func ConfiguredDays(ctx context.Context, store *db.Store) int {
	var raw string
	if err := store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'retention_days'`).Scan(&raw); err != nil {
		return DefaultDays
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < MinDays || days > MaxDays {
		return DefaultDays
	}
	return days
}

func Snapshot(ctx context.Context, store *db.Store) (Stats, error) {
	days := ConfiguredDays(ctx, store)
	stats := Stats{
		RetentionDays:   days,
		RetentionCutoff: time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339),
	}
	var pageCount, pageSize, freePages int64
	if err := store.DB.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return stats, err
	}
	if err := store.DB.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return stats, err
	}
	if err := store.DB.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return stats, err
	}
	stats.DatabaseBytes = pageCount * pageSize
	stats.DatabaseReclaimableBytes = freePages * pageSize
	stats.DatabaseUsedBytes = stats.DatabaseBytes - stats.DatabaseReclaimableBytes

	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(timestamp), '') FROM dns_queries`).Scan(&stats.QueryCount, &stats.OldestQuery); err != nil {
		return stats, err
	}
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(timestamp), '') FROM events`).Scan(&stats.EventCount, &stats.OldestEvent); err != nil {
		return stats, err
	}
	stats.LastPrunedAt = setting(ctx, store.DB, "last_retention_pruned_at")
	stats.LastQueriesDeleted = parseInt(setting(ctx, store.DB, "last_retention_queries_deleted"))
	stats.LastEventsDeleted = parseInt(setting(ctx, store.DB, "last_retention_events_deleted"))
	return stats, nil
}

func Prune(ctx context.Context, store *db.Store, days int, compact bool) (Result, error) {
	if days < MinDays || days > MaxDays {
		return Result{}, fmt.Errorf("retention days must be between %d and %d", MinDays, MaxDays)
	}
	before, err := Snapshot(ctx, store)
	if err != nil {
		return Result{}, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	queryResult, err := store.DB.ExecContext(ctx, `DELETE FROM dns_queries WHERE datetime(timestamp) < datetime(?)`, cutoff.Format(time.RFC3339))
	if err != nil {
		return Result{}, err
	}
	eventResult, err := store.DB.ExecContext(ctx, `DELETE FROM events WHERE datetime(timestamp) < datetime(?)`, cutoff.Format(time.RFC3339))
	if err != nil {
		return Result{}, err
	}
	queriesDeleted, _ := queryResult.RowsAffected()
	eventsDeleted, _ := eventResult.RowsAffected()
	completedAt := time.Now().UTC().Format(time.RFC3339)
	if err := storePruneResult(ctx, store.DB, completedAt, queriesDeleted, eventsDeleted); err != nil {
		return Result{}, err
	}
	if compact {
		if _, err := store.DB.ExecContext(ctx, `VACUUM`); err != nil {
			return Result{}, err
		}
	}
	after, err := Snapshot(ctx, store)
	if err != nil {
		return Result{}, err
	}
	return Result{
		QueriesDeleted: queriesDeleted,
		EventsDeleted:  eventsDeleted,
		BeforeBytes:    before.DatabaseBytes,
		AfterBytes:     after.DatabaseBytes,
		ReclaimedBytes: max(0, before.DatabaseBytes-after.DatabaseBytes),
		RetentionDays:  days,
		Cutoff:         cutoff.Format(time.RFC3339),
		Compacted:      compact,
		CompletedAt:    completedAt,
	}, nil
}

func setting(ctx context.Context, database *sql.DB, key string) string {
	var value string
	_ = database.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	return value
}

func parseInt(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func storePruneResult(ctx context.Context, database *sql.DB, completedAt string, queries, events int64) error {
	values := map[string]string{
		"last_retention_pruned_at":       completedAt,
		"last_retention_queries_deleted": strconv.FormatInt(queries, 10),
		"last_retention_events_deleted":  strconv.FormatInt(events, 10),
	}
	for key, value := range values {
		if _, err := database.ExecContext(ctx, `
			INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
		`, key, value); err != nil {
			return err
		}
	}
	return nil
}

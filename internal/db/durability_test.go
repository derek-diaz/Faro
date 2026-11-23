package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mattn/go-sqlite3"
)

func TestReadThenWriteTransactionKeepsSnapshot(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	writer, err := store.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(ctx, `PRAGMA busy_timeout = 25`); err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var value string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'dns_cache_ttl'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	// Readers must remain available, but another writer cannot invalidate this snapshot.
	var readValue string
	if err := writer.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'dns_cache_ttl'`).Scan(&readValue); err != nil || readValue != value {
		t.Fatalf("concurrent read = %q, %v; want %q", readValue, err, value)
	}
	_, err = writer.ExecContext(ctx, `UPDATE settings SET value = '301' WHERE key = 'dns_cache_ttl'`)
	var busy sqlite3.Error
	if !errors.As(err, &busy) || busy.Code != sqlite3.ErrBusy {
		t.Fatalf("concurrent writer invalidated the transaction's snapshot: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = '302' WHERE key = 'dns_cache_ttl'`); err != nil {
		t.Fatalf("read-then-write transaction failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, `UPDATE settings SET value = '303' WHERE key = 'dns_cache_ttl'`); err != nil {
		t.Fatalf("writer remained blocked after commit: %v", err)
	}
}

func TestTrafficSummariesUseCoveringIndexes(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, query := range []string{
		`SELECT device_id, COUNT(*), SUM(action = 'blocked'), MAX(timestamp) FROM dns_queries WHERE timestamp >= '2026-01-01' AND device_id IS NOT NULL GROUP BY device_id`,
		`SELECT COUNT(*), SUM(action = 'blocked'), SUM(source = 'cache'), AVG(latency_ms) FROM dns_queries WHERE timestamp >= '2026-01-01'`,
	} {
		rows, err := store.DB.Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !strings.Contains(plan.String(), "COVERING INDEX") {
			t.Errorf("traffic summary reads full history rows instead of its index: %s", plan.String())
		}
	}
}

func TestSQLiteDurabilityPragmasAndForeignKeys(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var journalMode string
	if err := store.DB.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeys, busyTimeout int
	if err := store.DB.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000ms", busyTimeout)
	}

	if _, err := store.DB.Exec(`INSERT INTO blocklist_entries(blocklist_id, domain) VALUES(999999, 'orphan.example')`); err == nil {
		t.Fatal("foreign-key enforcement accepted an orphan blocklist entry")
	}
}

func TestActivityStorageReportsDiskFullWithoutAffectingControlPlane(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.ReportActivityWriteFailure(sqlite3.Error{Code: sqlite3.ErrFull})
	status := store.ActivityStorageStatus()
	if status.Status != ActivityStoragePaused || status.Reason != "Insufficient disk space" {
		t.Fatalf("unexpected disk-full status: %+v", status)
	}
	if store.ActivityStorageWriteAllowed() {
		t.Fatal("activity writes were not paused after a disk-full error")
	}

	store.ReportActivityWriteSuccess()
	status = store.ActivityStorageStatus()
	if status.Status != ActivityStorageHealthy || !store.ActivityStorageWriteAllowed() {
		t.Fatalf("activity storage did not recover: %+v", status)
	}
}

// TestQueryHistoryScale is intentionally opt-in because the largest run can
// create tens of millions of rows and consume substantial disk space. It is
// the release-gate benchmark for 1M, 10M, and 50M-row history databases.
func TestQueryHistoryScale(t *testing.T) {
	rawRows := os.Getenv("FARO_QUERY_SCALE_ROWS")
	if rawRows == "" {
		t.Skip("set FARO_QUERY_SCALE_ROWS to 1000000, 10000000, or 50000000")
	}
	rows, err := strconv.ParseInt(rawRows, 10, 64)
	if err != nil || (rows != 1_000_000 && rows != 10_000_000 && rows != 50_000_000) {
		t.Fatalf("FARO_QUERY_SCALE_ROWS = %q; want 1000000, 10000000, or 50000000", rawRows)
	}

	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const batchSize int64 = 10_000
	for start := int64(0); start < rows; start += batchSize {
		end := start + batchSize
		if end > rows {
			end = rows
		}
		tx, err := store.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		statement, err := tx.Prepare(`
			INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, upstream, rcode)
			VALUES(?, '192.0.2.10', ?, 'A', ?, ?, '', 'NOERROR')`)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		for index := start; index < end; index++ {
			timestamp := fmt.Sprintf("2026-01-%02dT%02d:%02d:%02dZ", 1+(index/86400)%28, (index/3600)%24, (index/60)%60, index%60)
			action := "allowed"
			if index%10 == 0 {
				action = "blocked"
			}
			source := "upstream"
			if index%4 == 0 {
				source = "cache"
			}
			if _, err := statement.Exec(timestamp, fmt.Sprintf("scale-%d.example", index), action, source); err != nil {
				_ = statement.Close()
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
		if err := statement.Close(); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	var count int64
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != rows {
		t.Fatalf("query history count = %d, want %d", count, rows)
	}

	for _, indexName := range []string{
		"idx_dns_queries_timestamp_action",
		"idx_dns_queries_timestamp_source",
		"idx_dns_queries_device_timestamp_id",
		"idx_dns_queries_domain_timestamp",
		"idx_dns_queries_activity_order",
		"idx_dns_queries_retention",
		"idx_events_activity_order",
		"idx_events_retention",
	} {
		var count int
		if err := store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, indexName).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("expected history index %q", indexName)
		}
	}

	for _, query := range []string{
		`EXPLAIN QUERY PLAN SELECT id FROM dns_queries WHERE timestamp >= '2026-01-15T00:00:00Z' AND action = 'blocked' ORDER BY timestamp DESC LIMIT 100`,
		`EXPLAIN QUERY PLAN SELECT id FROM dns_queries WHERE domain = 'scale-123.example' ORDER BY timestamp DESC LIMIT 10`,
		`EXPLAIN QUERY PLAN SELECT id FROM dns_queries WHERE datetime(timestamp) < datetime('2026-02-01T00:00:00Z') ORDER BY id LIMIT 100`,
	} {
		rows, err := store.DB.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		var plan string
		if rows.Next() {
			var id, parent, notUsed int
			if err := rows.Scan(&id, &parent, &notUsed, &plan); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if plan == "" {
			t.Fatalf("empty query plan for %s", query)
		}
	}

	var integrity string
	if err := store.DB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q", integrity)
	}
}

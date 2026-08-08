package retention

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

func TestPruneDeletesOnlyExpiredLogs(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Now().UTC().AddDate(0, 0, -31).Format(time.RFC3339)
	recent := time.Now().UTC().AddDate(0, 0, -2).Format(time.RFC3339)
	for _, timestamp := range []string{old, recent} {
		if _, err := store.DB.Exec(`INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source) VALUES(?, '192.0.2.10', 'example.com', 'A', 'allowed', 'upstream')`, timestamp); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.Exec(`INSERT INTO events(timestamp, type, title) VALUES(?, 'test.event', 'Test')`, timestamp); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Prune(context.Background(), store, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.QueriesDeleted != 1 || result.EventsDeleted != 1 {
		t.Fatalf("unexpected prune result: %#v", result)
	}
	stats, err := Snapshot(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if stats.QueryCount != 1 || stats.EventCount != 1 {
		t.Fatalf("unexpected remaining rows: %#v", stats)
	}
	if stats.LastPrunedAt == "" || stats.LastQueriesDeleted != 1 || stats.LastEventsDeleted != 1 {
		t.Fatalf("missing persisted prune status: %#v", stats)
	}
}

func TestPruneLeavesDNSConfigurationUntouched(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value) VALUES('router.home', 'A', '192.168.1.1')`); err != nil {
		t.Fatal(err)
	}
	result, err := store.DB.Exec(`INSERT INTO blocklists(name, url, enabled) VALUES('Privacy', 'https://example.test/list', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	listID, _ := result.LastInsertId()
	if _, err := store.DB.Exec(`INSERT INTO blocklist_entries(blocklist_id, domain) VALUES(?, 'ads.example')`, listID); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -31).Format(time.RFC3339)
	if _, err := store.DB.Exec(`INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source) VALUES(?, '192.0.2.10', 'old.example', 'A', 'allowed', 'upstream')`, old); err != nil {
		t.Fatal(err)
	}

	if _, err := Prune(context.Background(), store, 30, true); err != nil {
		t.Fatal(err)
	}
	var hostname, address, domain string
	if err := store.DB.QueryRow(`SELECT hostname, value FROM dns_records WHERE hostname = 'router.home'`).Scan(&hostname, &address); err != nil {
		t.Fatal(err)
	}
	if hostname != "router.home" || address != "192.168.1.1" {
		t.Fatalf("DNS record changed during cleanup: %q -> %q", hostname, address)
	}
	if err := store.DB.QueryRow(`SELECT domain FROM blocklist_entries WHERE blocklist_id = ?`, listID).Scan(&domain); err != nil || domain != "ads.example" {
		t.Fatalf("blocklist entry changed during cleanup: %q, err=%v", domain, err)
	}
}

func TestConfiguredDaysFallsBackForInvalidValue(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`UPDATE settings SET value = '0' WHERE key = 'retention_days'`); err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredDays(context.Background(), store); got != DefaultDays {
		t.Fatalf("ConfiguredDays() = %d, want %d", got, DefaultDays)
	}
}

func TestPruneBatchesHistoryWithoutBlockingDNSWrites(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Now().UTC().AddDate(0, 0, -31).Format(time.RFC3339)
	tx, err := store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source)
		VALUES(?, '192.0.2.10', ?, 'A', 'allowed', 'upstream')`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	const expiredRows = 15_001
	for index := 0; index < expiredRows; index++ {
		if _, err := statement.Exec(old, "expired-"+fmt.Sprint(index)+".example"); err != nil {
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

	writerDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := store.DB.ExecContext(ctx, `INSERT INTO dns_records(hostname, type, value) VALUES('during-retention.home', 'A', '192.0.2.44')`)
		writerDone <- err
	}()

	result, err := Prune(context.Background(), store, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.QueriesDeleted != expiredRows {
		t.Fatalf("pruned %d rows, want %d", result.QueriesDeleted, expiredRows)
	}
	if err := <-writerDone; err != nil {
		t.Fatalf("DNS configuration write was blocked by retention cleanup: %v", err)
	}
	var records int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE hostname = 'during-retention.home'`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if records != 1 {
		t.Fatalf("DNS configuration write count = %d, want 1", records)
	}
}

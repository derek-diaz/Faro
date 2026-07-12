package retention

import (
	"context"
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

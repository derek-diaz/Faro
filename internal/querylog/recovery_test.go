package querylog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derek/faro/internal/db"
)

func logFixture(t *testing.T, content string) (*Tailer, *db.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	path := filepath.Join(dir, "query.log")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return NewTailer(store, path), store
}

func TestFailedBatchRetriesWithoutSkippingOrReplayingCommittedRows(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&content, "FARO|192.0.2.1|A|site-%d.example.|NOERROR|1ms|-\n", i)
	}
	tailer, store := logFixture(t, content.String())
	if _, err := store.DB.Exec(`CREATE TRIGGER fail_batch BEFORE INSERT ON dns_queries WHEN NEW.domain = 'site-270.example' BEGIN SELECT RAISE(ABORT, 'temporary write failure'); END`); err != nil {
		t.Fatal(err)
	}
	next, err := tailer.readAvailable(context.Background(), logCursor{})
	if err == nil {
		t.Fatal("failed batch was acknowledged")
	}
	assertQueryCount(t, store, 256)
	if next.Offset <= 0 || next.Offset >= int64(content.Len()) {
		t.Fatalf("invalid committed offset: %d", next.Offset)
	}
	if _, err := store.DB.Exec(`DROP TRIGGER fail_batch`); err != nil {
		t.Fatal(err)
	}
	store.ReportActivityWriteSuccess()
	// Simulate a restart with a sidecar that predates the successful batch.
	restarted := NewTailer(store, tailer.Path)
	if _, err := restarted.readAvailable(context.Background(), logCursor{}); err != nil {
		t.Fatal(err)
	}
	assertQueryCount(t, store, 300)
	// A second restart must not duplicate any history.
	if _, err := restarted.readAvailable(context.Background(), logCursor{}); err != nil {
		t.Fatal(err)
	}
	assertQueryCount(t, store, 300)
}

func TestExpiredCheckpointRecoversRetainedLogsOnceInOrder(t *testing.T) {
	line := func(name string) string { return "FARO|192.0.2.1|A|" + name + ".example.|NOERROR|1ms|-\n" }
	tailer, store := logFixture(t, line("expired"))
	ctx := context.Background()
	if _, err := tailer.readAvailable(ctx, logCursor{}); err != nil {
		t.Fatal(err)
	}
	for path, name := range map[string]string{".2": "oldest", ".1": "newer", "": "active"} {
		if err := os.WriteFile(tailer.Path+path, []byte(line(name)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := tailer.readAvailable(ctx, logCursor{}); err != nil {
			t.Fatal(err)
		}
	}
	var domains string
	if err := store.DB.QueryRow(`SELECT group_concat(domain, ',') FROM (SELECT domain FROM dns_queries ORDER BY id)`).Scan(&domains); err != nil {
		t.Fatal(err)
	}
	if domains != "expired.example,oldest.example,newer.example,active.example" {
		t.Fatalf("recovered history = %s", domains)
	}
}

func TestCheckpointFailureRollsBackQueries(t *testing.T) {
	tailer, store := logFixture(t, "FARO|192.0.2.1|A|example.com.|NOERROR|1ms|-\n")
	if _, err := store.DB.Exec(`CREATE TRIGGER fail_checkpoint BEFORE INSERT ON query_log_progress BEGIN SELECT RAISE(ABORT, 'checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	next, err := tailer.readAvailable(context.Background(), logCursor{})
	if err == nil || next.Offset != 0 {
		t.Fatalf("checkpoint failure acknowledged: %+v, %v", next, err)
	}
	assertQueryCount(t, store, 0)
	var devices int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&devices); err != nil || devices != 0 {
		t.Fatalf("devices after failed batch = %d, err = %v", devices, err)
	}
}

func TestPartialLineWaitsForCompletion(t *testing.T) {
	prefix := "FARO|192.0.2.1|A|example.com.|NOERROR|"
	tailer, store := logFixture(t, prefix)
	cursor, err := tailer.readAvailable(context.Background(), logCursor{})
	if err != nil || cursor.Offset != 0 {
		t.Fatalf("partial line consumed: %+v, %v", cursor, err)
	}
	file, err := os.OpenFile(tailer.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("1ms|-\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := tailer.readAvailable(context.Background(), cursor); err != nil {
		t.Fatal(err)
	}
	assertQueryCount(t, store, 1)
}

func TestOversizedLineDoesNotBlockFollowingQueries(t *testing.T) {
	tailer, store := logFixture(t, strings.Repeat("x", 128<<10)+"\nFARO|invalid-client|A|invalid.example.|NOERROR|1ms|-\nFARO|192.0.2.1|A|example.com.|NOERROR|1ms|-\n")
	if _, err := tailer.readAvailable(context.Background(), logCursor{}); err != nil {
		t.Fatal(err)
	}
	assertQueryCount(t, store, 1)
}

func TestIdentityObservationIsCoalescedPerBatch(t *testing.T) {
	tailer, store := logFixture(t, "")
	if _, err := store.DB.Exec(`CREATE TABLE observation_count (n INTEGER); INSERT INTO observation_count VALUES(0); CREATE TRIGGER count_observations AFTER UPDATE OF last_seen_at ON devices BEGIN UPDATE observation_count SET n=n+1; END`); err != nil {
		t.Fatal(err)
	}
	entries := make([]logEntry, 256)
	for i := range entries {
		entries[i] = logEntry{clientIP: "192.0.2.1", domain: "example.com", queryType: "A", rcode: "NOERROR"}
	}
	if err := tailer.insertBatch(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB.QueryRow(`SELECT n FROM observation_count`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("device updated %d times in one batch", count)
	}
	assertQueryCount(t, store, 256)
}

func TestBatchAttributesEarlierAddressToMergedDevice(t *testing.T) {
	tailer, store := logFixture(t, "")
	_, err := store.DB.Exec(`
		INSERT INTO devices(id) VALUES(11),(22);
		INSERT INTO device_addresses(device_id, address, family)
			VALUES(22, '192.0.2.1', 'ipv4'), (22, '192.0.2.2', 'ipv4');
		INSERT INTO device_identifiers(device_id, kind, value, source, confidence)
			VALUES(11, 'local_dns', 'printer.home', 'local_dns', 'strong');
		INSERT INTO dns_records(hostname, type, value) VALUES('printer.home', 'A', '192.0.2.2');`)
	if err != nil {
		t.Fatal(err)
	}
	entries := []logEntry{
		{clientIP: "192.0.2.1", domain: "one.example", queryType: "A"},
		{clientIP: "192.0.2.2", domain: "two.example", queryType: "A"},
	}
	if err := tailer.insertBatch(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries WHERE device_id = 11`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("queries linked to merged device = %d, err = %v", count, err)
	}
}

func TestBatchDecisionsStaySeparateAndRefreshBetweenBatches(t *testing.T) {
	tailer, store := logFixture(t, "")
	if _, err := store.DB.Exec(`
		INSERT INTO protection_profiles(id, name, icon) VALUES(2, 'Children', 'baby');
		INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES('192.0.2.2', 2);
		INSERT INTO protection_block_entries(protection_id, domain) VALUES(2, 'games.example');`); err != nil {
		t.Fatal(err)
	}
	entries := []logEntry{
		{clientIP: "192.0.2.1", domain: "games.example", queryType: "A", rcode: "NOERROR", observed: true},
		{clientIP: "192.0.2.2", domain: "games.example", queryType: "A", rcode: "NOERROR", observed: true},
		{clientIP: "192.0.2.2", domain: "other.example", queryType: "A", rcode: "NOERROR", observed: true},
		{clientIP: "192.0.2.2", domain: "games.example", queryType: "AAAA", rcode: "SERVFAIL", upstream: "9.9.9.9", observed: true},
		{clientIP: "192.0.2.2", domain: "games.example", queryType: "A", rcode: "NOERROR", observed: true, latencyMS: 2},
	}
	if err := tailer.insertBatch(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`DELETE FROM protection_block_entries WHERE protection_id = 2`); err != nil {
		t.Fatal(err)
	}
	if err := tailer.insertBatch(context.Background(), entries[1:2]); err != nil {
		t.Fatal(err)
	}
	rows, err := store.DB.Query(`SELECT action, rcode, upstream, latency_ms FROM dns_queries ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for index, want := range []string{"allowed", "blocked", "allowed", "allowed", "blocked", "allowed"} {
		if !rows.Next() {
			t.Fatalf("missing query %d", index)
		}
		var action, rcode, upstream string
		var latency float64
		if err := rows.Scan(&action, &rcode, &upstream, &latency); err != nil {
			t.Fatal(err)
		}
		if action != want {
			t.Fatalf("query %d action = %s, want %s", index, action, want)
		}
		if index == 3 && (rcode != "SERVFAIL" || upstream != "9.9.9.9") {
			t.Fatal("observed response was overwritten by shared policy")
		}
		if index == 4 && (rcode != "NOERROR" || upstream != "" || latency != 2) {
			t.Fatal("query-specific fields leaked between entries")
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra query")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertQueryCount(t *testing.T, store *db.Store, want int) {
	t.Helper()
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("query count = %d, want %d", count, want)
	}
}

func BenchmarkIngest256(b *testing.B) {
	store, err := db.Open(filepath.Join(b.TempDir(), "faro.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	tailer := NewTailer(store, "")
	entries := make([]logEntry, 256)
	for i := range entries {
		entries[i] = logEntry{clientIP: "192.0.2.1", domain: "example.com", queryType: "A", rcode: "NOERROR", observed: true}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := tailer.insertBatch(context.Background(), entries); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries`).Scan(&count); err != nil {
		b.Fatal(err)
	}
	if count != b.N*256 {
		b.Fatalf("missing history: %d", count)
	}
}

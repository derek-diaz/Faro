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

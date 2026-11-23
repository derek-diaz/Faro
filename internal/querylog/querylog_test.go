package querylog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
	"github.com/mattn/go-sqlite3"
)

func TestParseObservableLogLine(t *testing.T) {
	entry, ok := parseLine(`[INFO] FARO|192.168.7.10|A|example.com.|NOERROR|0.012345s|udp://9.9.9.9:53`)
	if !ok {
		t.Fatal("expected log line to parse")
	}
	if entry.clientIP != "192.168.7.10" || entry.domain != "example.com" || entry.queryType != "A" {
		t.Fatalf("unexpected parsed entry: %#v", entry)
	}
	if entry.upstream != "9.9.9.9" {
		t.Fatalf("expected normalized upstream, got %q", entry.upstream)
	}
	if entry.latencyMS != 12.345 {
		t.Fatalf("expected 12.345ms, got %v", entry.latencyMS)
	}
	if entry.rcode != "NOERROR" {
		t.Fatalf("expected response code, got %q", entry.rcode)
	}
}

func TestBatchLocalDecisionsRespectQuestionType(t *testing.T) {
	tailer, store := logFixture(t, "")
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value) VALUES
		('printer.home', 'A', '192.0.2.20'), ('printer.home', 'AAAA', '2001:db8::20')`); err != nil {
		t.Fatal(err)
	}
	var entries []logEntry
	for _, queryType := range []string{"A", "AAAA", "A", "AAAA", "TXT"} {
		entries = append(entries, logEntry{clientIP: "192.0.2.1", domain: "printer.home", queryType: queryType, rcode: "NOERROR", observed: true})
	}
	if err := tailer.insertBatch(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	assertQueryCount(t, store, len(entries))
	rows, err := store.DB.Query(`SELECT query_type, decision_metadata FROM dns_queries ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var queryType, metadata string
		if err := rows.Scan(&queryType, &metadata); err != nil {
			t.Fatal(err)
		}
		var decision coredns.DomainDecision
		if err := json.Unmarshal([]byte(metadata), &decision); err != nil {
			t.Fatal(err)
		}
		if queryType == "TXT" {
			if decision.LocalRecord != nil {
				t.Fatalf("TXT question matched address record: %+v", decision.LocalRecord)
			}
			continue
		}
		want := map[string]string{"A": "192.0.2.20", "AAAA": "2001:db8::20"}[queryType]
		if decision.LocalRecord == nil || decision.LocalRecord.Type != queryType || decision.LocalRecord.Value != want {
			t.Fatalf("%s question matched wrong local record: %+v", queryType, decision.LocalRecord)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestBufferedForwardedQueryKeepsObservedOutcomeAndTime(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO protection_block_entries(protection_id, domain) VALUES(1, 'previously-allowed.example')`); err != nil {
		t.Fatal(err)
	}
	const recordedAt = "2026-01-02T03:04:05.123456789Z"
	tailer := NewTailer(store, "")
	tailer.insert(context.Background(), recordedAt+` [INFO] FARO|192.0.2.20|A|previously-allowed.example.|NOERROR|1ms|9.9.9.9:53`)
	var action, source, timestamp, metadata string
	if err := store.DB.QueryRow(`SELECT action, source, timestamp, decision_metadata FROM dns_queries`).Scan(&action, &source, &timestamp, &metadata); err != nil {
		t.Fatal(err)
	}
	if action != "allowed" || source != "upstream" {
		t.Fatalf("observed forwarded query reclassified: %s / %s", action, source)
	}
	when, err := time.Parse(time.RFC3339Nano, timestamp)
	want, _ := time.Parse(time.RFC3339Nano, recordedAt)
	if err != nil || !when.Equal(want.Truncate(time.Second)) {
		t.Fatalf("timestamp = %s (%v), want original %s", timestamp, err, recordedAt)
	}
	var snapshot coredns.DomainDecision
	if err := json.Unmarshal([]byte(metadata), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ManualBlock != nil || len(snapshot.Blocklists) != 0 || snapshot.Confidence != "observed" {
		t.Fatalf("current rules leaked into historical outcome: %+v", snapshot)
	}
	if snapshot.CapturedAt != recordedAt {
		t.Fatalf("captured_at = %q, want %q", snapshot.CapturedAt, recordedAt)
	}
}

func TestParseCacheHitLogLine(t *testing.T) {
	entry, ok := parseLine(`[INFO] FARO|[::1]|AAAA|example.com.|NOERROR|250µs|-`)
	if !ok {
		t.Fatal("expected log line to parse")
	}
	if entry.clientIP != "::1" || entry.upstream != "" || !entry.observed {
		t.Fatalf("unexpected cache entry: %#v", entry)
	}
	if entry.latencyMS != 0.25 {
		t.Fatalf("expected 0.25ms, got %v", entry.latencyMS)
	}
}

func TestParseEncryptedGatewayLogLine(t *testing.T) {
	entry, ok := parseLine(`[INFO] FARO|192.168.7.10|A|example.com.|NOERROR|0.018s|udp://127.0.0.1:5053`)
	if !ok {
		t.Fatal("expected encrypted gateway log line to parse")
	}
	if entry.upstream != "doh" {
		t.Fatalf("encrypted gateway normalized as %q", entry.upstream)
	}
}

func TestParseLegacyLogLine(t *testing.T) {
	entry, ok := parseLine(`[INFO] 127.0.0.1:42130 - 1234 "A IN example.com. udp 40 false 1232" NOERROR qr,rd,ra 56 0.005s`)
	if !ok || entry.observed {
		t.Fatalf("expected legacy line, got %#v, %v", entry, ok)
	}
	if entry.latencyMS != 5 {
		t.Fatalf("expected 5ms, got %v", entry.latencyMS)
	}
}

func TestInsertPersistsDecisionSnapshot(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	result, err := store.DB.Exec(`INSERT INTO blocklists(name, url, enabled) VALUES('Telemetry protection', 'https://example.test/hosts', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	listID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO blocklist_entries(blocklist_id, domain) VALUES(?, 'telemetry.example')`, listID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO protection_blocklists(protection_id, blocklist_id) SELECT id, ? FROM protection_profiles WHERE is_default = 1`, listID); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(store, "")
	tailer.insert(context.Background(), `[INFO] FARO|192.168.7.22|A|telemetry.example.|NOERROR|0.000250s|-`)

	var action, source, rcode, reason, rawMetadata string
	var deviceID int64
	if err := store.DB.QueryRow(`
		SELECT action, source, rcode, decision_reason, decision_metadata, device_id
		FROM dns_queries WHERE domain = 'telemetry.example'
	`).Scan(&action, &source, &rcode, &reason, &rawMetadata, &deviceID); err != nil {
		t.Fatal(err)
	}
	if action != "blocked" || source != "blocklist" || rcode != "NOERROR" {
		t.Fatalf("unexpected persisted result: action=%q source=%q rcode=%q", action, source, rcode)
	}
	if deviceID == 0 {
		t.Fatal("DNS query was not attached to a stable device")
	}
	if !strings.Contains(reason, "Telemetry protection") {
		t.Fatalf("expected specific decision reason, got %q", reason)
	}

	var snapshot coredns.DomainDecision
	if err := json.Unmarshal([]byte(rawMetadata), &snapshot); err != nil {
		t.Fatalf("invalid decision metadata: %v", err)
	}
	if len(snapshot.Blocklists) != 1 || snapshot.Blocklists[0].ID != listID {
		t.Fatalf("unexpected decision snapshot: %#v", snapshot)
	}
	if snapshot.Confidence != "configuration_snapshot" || snapshot.CapturedAt == "" {
		t.Fatalf("missing provenance metadata: %#v", snapshot)
	}
}

func TestActivityStorageFailureDoesNotStopDNSDecisionEvaluation(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.ReportActivityWriteFailure(sqlite3.Error{Code: sqlite3.ErrFull})
	tailer := NewTailer(store, "")
	tailer.insert(context.Background(), `[INFO] FARO|192.168.7.22|A|still-resolves.example.|NOERROR|0.001s|-`)

	var historyRows int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries WHERE domain = 'still-resolves.example'`).Scan(&historyRows); err != nil {
		t.Fatal(err)
	}
	if historyRows != 0 {
		t.Fatalf("activity history write succeeded while storage was paused: %d rows", historyRows)
	}

	decision := coredns.ExplainDomainForClient(context.Background(), store, "still-resolves.example", "192.168.7.22")
	if decision.Action != "allowed" {
		t.Fatalf("DNS decision changed while activity storage was paused: %+v", decision)
	}
	status := store.ActivityStorageStatus()
	if status.Status != db.ActivityStoragePaused || status.Reason != "Insufficient disk space" {
		t.Fatalf("unexpected activity storage status: %+v", status)
	}
}

func TestTailerFinishesRotatedLogBeforeReadingCurrentLog(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	logPath := filepath.Join(t.TempDir(), "query.log")
	oldLine := "[INFO] FARO|192.168.7.20|A|already-seen.example.|NOERROR|1ms|udp://1.1.1.1:53\n"
	if err := os.WriteFile(logPath, []byte(oldLine), 0o644); err != nil {
		t.Fatal(err)
	}
	cursor := cursorAtEnd(logPath)
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	newOldLine := "[INFO] FARO|192.168.7.20|A|before-rotation.example.|NOERROR|2ms|udp://1.1.1.1:53\n"
	if _, err := file.WriteString(newOldLine); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(logPath, logPath+".2"); err != nil {
		t.Fatal(err)
	}
	intermediateLine := "[INFO] FARO|192.168.7.20|A|between-rotations.example.|NOERROR|2.5ms|udp://1.1.1.1:53\n"
	if err := os.WriteFile(logPath+".1", []byte(intermediateLine), 0o644); err != nil {
		t.Fatal(err)
	}
	currentLine := "[INFO] FARO|192.168.7.20|A|after-rotation.example.|NOERROR|3ms|udp://1.1.1.1:53\n"
	if err := os.WriteFile(logPath, []byte(currentLine), 0o644); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(store, logPath)
	if _, err := tailer.readAvailable(context.Background(), cursor); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("ingested query count = %d, want 3", count)
	}
	var skipped int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries WHERE domain = 'already-seen.example'`).Scan(&skipped); err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Fatal("tailer re-ingested data from before its saved offset")
	}
}

func TestPersistedCursorIngestsOnlyQueriesWrittenDuringRestart(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logPath := filepath.Join(t.TempDir(), "query.log")
	before := "[INFO] FARO|192.168.7.20|A|before-restart.example.|NOERROR|1ms|udp://1.1.1.1:53\n"
	if err := os.WriteFile(logPath, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveCursor(logPath+".cursor", cursorAtEnd(logPath)); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("[INFO] FARO|192.168.7.20|A|during-restart.example.|NOERROR|2ms|udp://1.1.1.1:53\n")
	_ = file.Close()
	cursor, ok := loadCursor(logPath + ".cursor")
	if !ok {
		t.Fatal("persisted cursor could not be loaded")
	}
	tailer := NewTailer(store, logPath)
	if _, err := tailer.readAvailable(context.Background(), cursor); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ingested count = %d, err = %v", count, err)
	}
}

func TestRotationWithRepeatedFirstQueryPreservesAllRows(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := filepath.Join(t.TempDir(), "query.log")
	line := "[INFO] FARO|192.0.2.20|A|repeat.example.|NOERROR|1ms|9.9.9.9:53\n"
	if err := os.WriteFile(path, []byte("# FARO-LOG first\n"+line), 0o600); err != nil {
		t.Fatal(err)
	}
	tailer := NewTailer(store, path)
	cursor, err := tailer.readAvailable(context.Background(), logCursor{})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(line); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# FARO-LOG second\n"+line+line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tailer.readAvailable(context.Background(), cursor); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("rows after rotation = %d, want 4", count)
	}
}

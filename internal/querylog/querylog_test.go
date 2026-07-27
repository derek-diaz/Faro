package querylog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
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

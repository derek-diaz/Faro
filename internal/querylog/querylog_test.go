package querylog

import (
	"context"
	"encoding/json"
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

	tailer := NewTailer(store, "")
	tailer.insert(context.Background(), `[INFO] FARO|192.168.7.22|A|telemetry.example.|NOERROR|0.000250s|-`)

	var action, source, rcode, reason, rawMetadata string
	if err := store.DB.QueryRow(`
		SELECT action, source, rcode, decision_reason, decision_metadata
		FROM dns_queries WHERE domain = 'telemetry.example'
	`).Scan(&action, &source, &rcode, &reason, &rawMetadata); err != nil {
		t.Fatal(err)
	}
	if action != "blocked" || source != "blocklist" || rcode != "NOERROR" {
		t.Fatalf("unexpected persisted result: action=%q source=%q rcode=%q", action, source, rcode)
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

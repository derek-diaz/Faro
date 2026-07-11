package querylog

import "testing"

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

package coredns

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/derek/faro/internal/db"
)

func TestExplainDomainCapturesMatchingRulesAndPrecedence(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	result, err := store.DB.Exec(`INSERT INTO blocklists(name, url, enabled) VALUES('Privacy list', 'https://example.test/hosts', 1)`)
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

	decision := ExplainDomain(context.Background(), store, "Telemetry.Example.")
	if decision.Action != "blocked" || len(decision.Blocklists) != 1 {
		t.Fatalf("expected blocklist decision, got %#v", decision)
	}
	if decision.Blocklists[0].ID != listID || decision.Blocklists[0].Name != "Privacy list" {
		t.Fatalf("unexpected blocklist match: %#v", decision.Blocklists[0])
	}

	if _, err := store.DB.Exec(`INSERT INTO allowlist_entries(domain) VALUES('telemetry.example')`); err != nil {
		t.Fatal(err)
	}
	decision = ExplainDomain(context.Background(), store, "telemetry.example")
	if decision.Action != "allowed" || decision.Allowlist == nil {
		t.Fatalf("expected allowlist to take precedence, got %#v", decision)
	}
	if len(decision.Blocklists) != 1 {
		t.Fatalf("expected audit snapshot to retain lower-priority matches, got %#v", decision)
	}
}

func TestExplainDomainCapturesLocalRecord(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value, description) VALUES('plex.home', 'A', '192.168.7.50', 'Media server')`); err != nil {
		t.Fatal(err)
	}

	decision := ExplainDomain(context.Background(), store, "plex.home")
	if decision.Action != "allowed" || decision.LocalRecord == nil {
		t.Fatalf("expected local record decision, got %#v", decision)
	}
	if decision.LocalRecord.Type != "A" || decision.LocalRecord.Value != "192.168.7.50" {
		t.Fatalf("unexpected local record: %#v", decision.LocalRecord)
	}
}

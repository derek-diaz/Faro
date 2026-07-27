package coredns

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
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
	if _, err := store.DB.Exec(`INSERT INTO protection_blocklists(protection_id, blocklist_id) SELECT id, ? FROM protection_profiles WHERE is_default = 1`, listID); err != nil {
		t.Fatal(err)
	}

	decision := ExplainDomain(context.Background(), store, "Telemetry.Example.")
	if decision.Action != "blocked" || len(decision.Blocklists) != 1 {
		t.Fatalf("expected blocklist decision, got %#v", decision)
	}
	if decision.Blocklists[0].ID != listID || decision.Blocklists[0].Name != "Privacy list" {
		t.Fatalf("unexpected blocklist match: %#v", decision.Blocklists[0])
	}

	if _, err := store.DB.Exec(`INSERT INTO protection_allow_entries(protection_id, domain) SELECT id, 'telemetry.example' FROM protection_profiles WHERE is_default = 1`); err != nil {
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

func TestRenderIncludesClientACLAndDualStackRecords(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, statement := range []string{
		`INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'A', '192.168.1.20')`,
		`INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'AAAA', '2001:db8::20')`,
	} {
		if _, err := store.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	rendered, err := NewManager(store, t.TempDir()).render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"acl {", "allow net 127.0.0.0/8", "192.168.1.20 nas.home", "2001:db8::20 nas.home"} {
		if !strings.Contains(rendered.Corefile+rendered.LocalHosts, expected) {
			t.Fatalf("rendered configuration is missing %q", expected)
		}
	}
}

func TestRenderRoutesEncryptedUpstreamsOnlyThroughLoopbackGateway(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`UPDATE settings SET value = 'encrypted' WHERE key = 'upstream_transport'`); err != nil {
		t.Fatal(err)
	}
	rendered, err := NewManager(store, t.TempDir()).render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Corefile, "forward . 127.0.0.1:5053") {
		t.Fatalf("encrypted Corefile does not use Faro's loopback gateway:\n%s", rendered.Corefile)
	}
	if strings.Contains(rendered.Corefile, "forward . 1.1.1.1") || strings.Contains(rendered.Corefile, "forward . 9.9.9.9") {
		t.Fatalf("encrypted Corefile contains a plaintext public resolver:\n%s", rendered.Corefile)
	}
}

func TestRenderRoutesAssignedClientsBeforeHome(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := store.DB.Exec(`INSERT INTO protection_profiles(name, icon) VALUES('Children', 'baby')`)
	if err != nil {
		t.Fatal(err)
	}
	protectionID, _ := result.LastInsertId()
	if _, err := store.DB.Exec(`INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES('192.168.7.23', ?)`, protectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO protection_block_entries(protection_id, domain) VALUES(?, 'games.example')`, protectionID); err != nil {
		t.Fatal(err)
	}

	rendered, err := NewManager(store, t.TempDir()).render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	view := "view protection_" + fmt.Sprint(protectionID)
	if !strings.Contains(rendered.Corefile, view) || !strings.Contains(rendered.Corefile, "incidr(client_ip(), '192.168.7.23/32')") {
		t.Fatalf("custom client view missing from Corefile:\n%s", rendered.Corefile)
	}
	if strings.Index(rendered.Corefile, view) > strings.LastIndex(rendered.Corefile, ".:53 {") {
		t.Fatal("custom client view must be rendered before the Home catch-all")
	}
	hosts := rendered.ProtectionHosts[fmt.Sprintf("protection-%d.hosts", protectionID)]
	if !strings.Contains(hosts, "0.0.0.0 games.example") {
		t.Fatalf("custom protection hosts file missing rule: %s", hosts)
	}

	decision := ExplainDomainForClient(context.Background(), store, "games.example", "192.168.7.23")
	if decision.Action != "blocked" || decision.Protection == nil || decision.Protection.ID != protectionID {
		t.Fatalf("unexpected per-client decision: %#v", decision)
	}
	homeDecision := ExplainDomainForClient(context.Background(), store, "games.example", "192.168.7.24")
	if homeDecision.Action != "allowed" || homeDecision.Protection == nil || homeDecision.Protection.Name != "Home" {
		t.Fatalf("Home should not inherit the custom rule: %#v", homeDecision)
	}
}

func TestProtectionFollowsCorrelatedAddress(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		INSERT INTO dns_records(hostname, type, value) VALUES('tablet.home', 'A', '192.168.7.30');
		INSERT INTO dns_records(hostname, type, value) VALUES('tablet.home', 'AAAA', '2001:db8::30')`); err != nil {
		t.Fatal(err)
	}
	deviceID, err := deviceidentity.ResolveAddress(context.Background(), store, "192.168.7.30", "dns")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.DB.Exec(`INSERT INTO protection_profiles(name, icon) VALUES('Children', 'baby')`)
	if err != nil {
		t.Fatal(err)
	}
	protectionID, _ := result.LastInsertId()
	if _, err := store.DB.Exec(`INSERT INTO protection_block_entries(protection_id, domain) VALUES(?, 'games.example'); INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?)`, protectionID, deviceID, protectionID); err != nil {
		t.Fatal(err)
	}
	correlatedID, err := deviceidentity.ResolveAddress(context.Background(), store, "2001:db8::30", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if correlatedID != deviceID {
		t.Fatalf("correlated address belongs to device %d, want %d", correlatedID, deviceID)
	}
	decision := ExplainDomainForClient(context.Background(), store, "games.example", "2001:db8::30")
	if decision.Action != "blocked" || decision.Protection == nil || decision.Protection.ID != protectionID {
		t.Fatalf("protection did not follow correlated address: %#v", decision)
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

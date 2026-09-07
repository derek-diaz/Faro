package coredns

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
)

func TestTemporaryExceptionsExpireInGeneratedPolicyAndTemporalState(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	deviceID, err := deviceidentity.ResolveAddress(ctx, store, "192.0.2.10", "dns")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.DB.Exec(`INSERT INTO protection_profiles(name,icon) VALUES('Office','shield')`)
	if err != nil {
		t.Fatal(err)
	}
	protectionID, _ := result.LastInsertId()
	for _, statement := range []string{
		fmt.Sprintf(`INSERT INTO device_protection_memberships(device_id,protection_id) VALUES(%d,%d)`, deviceID, protectionID),
		fmt.Sprintf(`INSERT INTO protection_block_entries(protection_id,domain) VALUES(%d,'broken.example')`, protectionID),
		`INSERT INTO protection_block_entries(protection_id,domain) SELECT id,'broken.example' FROM protection_profiles WHERE is_default=1`,
	} {
		if _, err := store.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	expiry := now.Add(10 * time.Minute)
	if _, err := store.DB.Exec(`INSERT INTO troubleshooting_exceptions(token,client_ip,device_id,protection_id,domain,expires_at) VALUES('test','192.0.2.10',?,?,'broken.example',?)`, deviceID, protectionID, expiry.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, t.TempDir())
	active, err := manager.currentTemporalSignature(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := manager.currentTemporalSignature(ctx, expiry.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if active == expired {
		t.Fatal("expiry did not change reload signature")
	}
	rendered, err := manager.render(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("protection-%d.hosts", protectionID)
	if strings.Contains(rendered.ProtectionHosts[key], "broken.example") {
		t.Fatal("active test still blocked")
	}
	if !strings.Contains(rendered.BlockHosts, "broken.example") {
		t.Fatal("test changed Home")
	}
	if _, err := store.DB.Exec(`UPDATE troubleshooting_exceptions SET expires_at=?`, now.Add(-time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	rendered, err = manager.render(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.ProtectionHosts[key], "broken.example") {
		t.Fatal("expired test still allowed after rendering")
	}
	decision := ExplainDomainForClient(ctx, store, "broken.example", "192.0.2.10")
	if decision.Action != "blocked" || decision.Allowlist != nil {
		t.Fatalf("expired test still explained as allowed: %#v", decision)
	}
}

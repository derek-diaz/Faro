package devices

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/derek/faro/internal/db"
)

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestResolveAddressIsStable(t *testing.T) {
	store := newTestStore(t)
	first, err := ResolveAddress(context.Background(), store, "192.168.1.20", "dns")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveAddress(context.Background(), store, "192.168.1.20", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("device IDs differ: %d and %d", first, second)
	}
}

func TestReassignedAddressDoesNotMergeDevicesOrMoveHistory(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "new device", true: "known device"}[known], func(t *testing.T) {
			store := newTestStore(t)
			ctx := context.Background()
			observe := func(address, mac string) int64 {
				t.Helper()
				id, err := ObserveAddress(ctx, store, address, "unifi", []Identifier{{Kind: "mac", Value: mac}})
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			oldID := observe("192.0.2.20", "02:11:22:33:44:55")
			var newID int64
			if known {
				newID = observe("192.0.2.21", "02:aa:bb:cc:dd:ee")
			}
			// An old DNS name must not outweigh the new owner's hardware identity.
			if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value) VALUES('old-device.home', 'A', '192.0.2.20');
				INSERT INTO device_identifiers(device_id, kind, value, source, confidence) VALUES(?, 'local_dns', 'old-device.home', 'local_dns', 'strong')`, oldID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB.Exec(`
				INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, 1);
				INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES('192.0.2.20', 1);
				INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source) VALUES(CURRENT_TIMESTAMP, '192.0.2.20', ?, 'old.example', 'A', 'allowed', 'upstream')`, oldID, oldID); err != nil {
				t.Fatal(err)
			}
			got := observe("192.0.2.20", "02:aa:bb:cc:dd:ee")
			if got == oldID || (known && got != newID) {
				t.Fatalf("reassigned address resolved to %d (old %d, known %d)", got, oldID, newID)
			}
			var historyID, membershipID int64
			if err := store.DB.QueryRow(`SELECT device_id FROM dns_queries`).Scan(&historyID); err != nil {
				t.Fatal(err)
			}
			if err := store.DB.QueryRow(`SELECT device_id FROM device_protection_memberships`).Scan(&membershipID); err != nil {
				t.Fatal(err)
			}
			if historyID != oldID || membershipID != oldID {
				t.Fatal("reassignment moved old history or protection")
			}
			var legacy int
			if err := store.DB.QueryRow(`SELECT COUNT(*) FROM device_protection_assignments WHERE client_ip = '192.0.2.20'`).Scan(&legacy); err != nil {
				t.Fatal(err)
			}
			if legacy != 0 {
				t.Fatal("new owner inherited old IP protection")
			}
		})
	}
}

func TestResolveAddressCorrelatesDualStackLocalDNS(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.DB.Exec(`
		INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'A', '192.168.1.20');
		INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'AAAA', '2001:db8::20')`); err != nil {
		t.Fatal(err)
	}
	first, err := ResolveAddress(context.Background(), store, "192.168.1.20", "dns")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveAddress(context.Background(), store, "2001:db8::20", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("dual-stack addresses resolved to %d and %d", first, second)
	}
	addresses, err := Addresses(context.Background(), store, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 2 {
		t.Fatalf("address history has %d addresses, want 2", len(addresses))
	}
}

func TestResolveAddressDoesNotMergeConflictingConfirmedDevices(t *testing.T) {
	store := newTestStore(t)
	first, err := ResolveAddress(context.Background(), store, "192.168.1.20", "dns")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveAddress(context.Background(), store, "192.168.1.21", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE devices SET name = 'NAS', confirmed = 1 WHERE id = ?; UPDATE devices SET name = 'TV', confirmed = 1 WHERE id = ?`, first, second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO dns_records(hostname, type, value) VALUES('shared.home', 'A', '192.168.1.20');
		INSERT INTO dns_records(hostname, type, value) VALUES('shared.home', 'A', '192.168.1.21')`); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveAddress(context.Background(), store, "192.168.1.20", "dns"); err != nil {
		t.Fatal(err)
	}
	resolvedSecond, err := ResolveAddress(context.Background(), store, "192.168.1.21", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedSecond != second {
		t.Fatalf("conflicting confirmed device merged into %d, want %d", resolvedSecond, second)
	}
}

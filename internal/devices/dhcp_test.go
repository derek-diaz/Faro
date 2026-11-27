package devices

import (
	"context"
	"slices"
	"testing"

	"github.com/derek/faro/internal/coredns"
)

func TestUniFiMoveRetiresAddressWithoutMovingHistoryOrProtection(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	identifiers := []Identifier{{Kind: "mac", Value: "02:11:22:33:44:55"}, {Kind: "unifi_client", Value: "site/client"}}
	if _, err := store.DB.Exec(`INSERT INTO integration_configs(kind,enabled) VALUES('unifi',1)`); err != nil {
		t.Fatal(err)
	}
	// Devices can be discovered through DNS before the first controller sync.
	deviceID, err := ResolveAddress(ctx, store, "192.0.2.20", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ObserveAddress(ctx, store, "192.0.2.20", "unifi", identifiers); err != nil {
		t.Fatal(err)
	}
	if _, err = ObserveAddress(ctx, store, "2001:db8::20", "dns", identifiers[:1]); err != nil {
		t.Fatal(err)
	}
	// Older Faro versions kept DNS-discovered leases without marking them as controller-owned.
	if _, err = store.DB.Exec(`INSERT INTO device_addresses(device_id,address,family,source) VALUES(?,'192.0.2.19','ipv4','dns')`, deviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec(`
		UPDATE devices SET name='Living room', confirmed=1 WHERE id=?;
		INSERT INTO device_aliases(client_ip,name) VALUES('192.0.2.20','Living room');
		INSERT INTO protection_profiles(name,icon) VALUES('Kids','shield');
		INSERT INTO device_protection_memberships(device_id,protection_id) SELECT ?,id FROM protection_profiles WHERE name='Kids';
		INSERT INTO device_protection_assignments(client_ip,protection_id) SELECT '192.0.2.20',id FROM protection_profiles WHERE name='Kids';
		INSERT INTO protection_block_entries(protection_id,domain) SELECT id,'blocked.example' FROM protection_profiles WHERE name='Kids';
		INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(CURRENT_TIMESTAMP,'192.0.2.20',?,'past.example','A','allowed','upstream');
		INSERT INTO dns_records(hostname,type,value) VALUES('speaker.home','A','192.0.2.20');
		INSERT INTO device_identifiers(device_id,kind,value,source) VALUES(?,'local_dns','speaker.home','local_dns');
	`, deviceID, deviceID, deviceID, deviceID); err != nil {
		t.Fatal(err)
	}
	got, err := ObserveAddress(ctx, store, "192.0.2.21", "unifi", identifiers)
	if err != nil || got != deviceID {
		t.Fatalf("moved identity = %d, err=%v", got, err)
	}
	addresses, err := Addresses(ctx, store, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 2 || slices.Contains(addresses, "192.0.2.20") || !slices.Contains(addresses, "192.0.2.21") || !slices.Contains(addresses, "2001:db8::20") {
		t.Errorf("current addresses after DHCP move = %v", addresses)
	}
	for _, table := range []string{"device_aliases", "device_protection_assignments"} {
		var count int
		if err := store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE client_ip='192.0.2.20'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("old address retained %s", table)
		}
	}
	var historyID int64
	if err := store.DB.QueryRow(`SELECT device_id FROM dns_queries WHERE domain='past.example'`).Scan(&historyID); err != nil {
		t.Fatal(err)
	}
	if historyID != deviceID {
		t.Fatal("past queries changed owner")
	}
	if decision := coredns.ExplainDomainForClient(ctx, store, "blocked.example", "192.0.2.21"); decision.Action != "blocked" {
		t.Fatalf("new IP lost protection: %#v", decision)
	}
	if decision := coredns.ExplainDomainForClient(ctx, store, "blocked.example", "192.0.2.20"); decision.Action != "allowed" {
		t.Fatalf("old IP inherited protection: %#v", decision)
	}
	// A DNS query at the vacated address must not revive the old owner through a stale hostname.
	newID, err := ResolveAddress(ctx, store, "192.0.2.20", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if newID == deviceID {
		t.Fatal("DNS observation revived retired ownership")
	}
}

func TestReassignedAddressClearsLegacyName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := ObserveAddress(ctx, store, "192.0.2.20", "unifi", []Identifier{{Kind: "mac", Value: "02:11:22:33:44:55"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO device_aliases(client_ip,name) VALUES('192.0.2.20','Old device')`); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveAddress(ctx, store, "192.0.2.20", "unifi", []Identifier{{Kind: "mac", Value: "02:aa:bb:cc:dd:ee"}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM device_aliases`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("reused address retained the old manual alias")
	}
}

func TestKnownHardwareDoesNotAdoptPreviousAddressOwner(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	ids := []Identifier{{Kind: "mac", Value: "02:11:22:33:44:55"}, {Kind: "unifi_client", Value: "site/one"}}
	knownID, err := ObserveAddress(ctx, store, "192.0.2.20", "unifi", ids)
	if err != nil {
		t.Fatal(err)
	}
	previousID, err := ResolveAddress(ctx, store, "192.0.2.21", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE devices SET name='Original',confirmed=1 WHERE id=?;
		UPDATE devices SET name='Previous owner',confirmed=1 WHERE id=?;
		INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(CURRENT_TIMESTAMP,'192.0.2.21',?,'previous.example','A','allowed','upstream')`, knownID, previousID, previousID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname,type,value) VALUES('previous.home','A','192.0.2.21');
		INSERT INTO device_identifiers(device_id,kind,value,source) VALUES(?,'local_dns','previous.home','local_dns')`, previousID); err != nil {
		t.Fatal(err)
	}
	got, err := ObserveAddress(ctx, store, "192.0.2.21", "unifi", ids)
	if err != nil || got != knownID {
		t.Fatalf("hardware identity lost to previous address owner: got=%d want=%d err=%v", got, knownID, err)
	}
	if _, err := store.DB.Exec(`UPDATE devices SET name='',confirmed=0 WHERE id=?`, previousID); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveAddress(ctx, store, "192.0.2.21", "dns")
	if err != nil || got != knownID {
		t.Fatalf("later DNS revived the stale hostname's owner: got=%d want=%d err=%v", got, knownID, err)
	}
	var historyID int64
	if err := store.DB.QueryRow(`SELECT device_id FROM dns_queries WHERE domain='previous.example'`).Scan(&historyID); err != nil {
		t.Fatal(err)
	}
	if historyID != previousID {
		t.Fatal("previous owner's history was moved")
	}
}

func TestDisconnectedControllerAllowsLocalDNSCorrelation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname,type,value) VALUES('speaker.home','A','192.0.2.20'),('speaker.home','A','192.0.2.21')`); err != nil {
		t.Fatal(err)
	}
	knownID, err := ObserveAddress(ctx, store, "192.0.2.20", "unifi", []Identifier{{Kind: "mac", Value: "02:11:22:33:44:55"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveAddress(ctx, store, "192.0.2.20", "dns"); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveAddress(ctx, store, "192.0.2.21", "dns")
	if err != nil || got != knownID {
		t.Fatalf("disconnected controller blocked passive correlation: got=%d want=%d err=%v", got, knownID, err)
	}
}

func TestStaleNeighborCannotReclaimReassignedAddress(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.DB.Exec(`INSERT INTO integration_configs(kind,enabled) VALUES('unifi',1)`); err != nil {
		t.Fatal(err)
	}
	for _, mac := range []string{"02:11:22:33:44:55", "02:aa:bb:cc:dd:ee"} {
		if _, err := ObserveAddress(ctx, store, "192.0.2.20", "unifi", []Identifier{{Kind: "mac", Value: mac}}); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, check := range []struct {
		mac   string
		stale bool
	}{{"02:11:22:33:44:55", true}, {"02:aa:bb:cc:dd:ee", false}} {
		got, err := neighborMACIsStale(ctx, tx, "192.0.2.20", check.mac)
		if err != nil || got != check.stale {
			t.Errorf("neighbor %s: stale=%v want=%v err=%v", check.mac, got, check.stale, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM integration_configs WHERE kind='unifi'`); err != nil {
		t.Fatal(err)
	}
	if got, err := neighborMACIsStale(ctx, tx, "192.0.2.20", "02:11:22:33:44:55"); err != nil || got {
		t.Fatalf("disconnected controller still overrides neighbor: %v %v", got, err)
	}
}

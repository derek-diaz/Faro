package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenDoesNotSeedDemoDNSRecords(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_records`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("fresh database contains %d DNS records, want 0", count)
	}
}

func TestProtectionMigrationCreatesHomeAndCopiesExistingRules(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`DELETE FROM settings WHERE key = 'protection_migration_completed'`); err != nil {
		t.Fatal(err)
	}
	result, err := store.DB.Exec(`INSERT INTO blocklists(name, url, enabled) VALUES('Legacy', 'https://example.test/hosts', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	listID, _ := result.LastInsertId()
	if _, err := store.DB.Exec(`INSERT INTO allowlist_entries(domain) VALUES('allowed.example'); INSERT INTO manual_block_entries(domain) VALUES('blocked.example')`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateProtection(context.Background()); err != nil {
		t.Fatal(err)
	}

	var homeID int64
	if err := store.DB.QueryRow(`SELECT id FROM protection_profiles WHERE name = 'Home' AND is_default = 1`).Scan(&homeID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`SELECT COUNT(*) FROM protection_blocklists WHERE protection_id = ? AND blocklist_id = ?`, []any{homeID, listID}},
		{`SELECT COUNT(*) FROM protection_allow_entries WHERE protection_id = ? AND domain = 'allowed.example'`, []any{homeID}},
		{`SELECT COUNT(*) FROM protection_block_entries WHERE protection_id = ? AND domain = 'blocked.example'`, []any{homeID}},
	} {
		var count int
		if err := store.DB.QueryRow(query.sql, query.args...).Scan(&count); err != nil || count != 1 {
			t.Fatalf("migration check %q count=%d err=%v", query.sql, count, err)
		}
	}
}

func TestRemoveLegacyDemoRecordsPreservesModifiedRecords(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.DB.Exec(`DELETE FROM settings WHERE key = 'legacy_demo_records_removed'`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO dns_records(hostname, type, value, description) VALUES('router.home', 'A', '192.168.7.1', 'Home gateway')`,
		`INSERT INTO dns_records(hostname, type, value, description) VALUES('plex.home', 'A', '192.168.1.50', 'Media server')`,
		`INSERT INTO dns_records(hostname, type, value, description) VALUES('nas.home', 'A', '192.168.7.20', 'My storage server')`,
	} {
		if _, err := store.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.removeLegacyDemoRecords(context.Background()); err != nil {
		t.Fatal(err)
	}

	var exactDemoCount int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE hostname = 'router.home'`).Scan(&exactDemoCount); err != nil {
		t.Fatal(err)
	}
	if exactDemoCount != 0 {
		t.Fatalf("exact legacy demo record was not removed")
	}
	var modifiedCount int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE hostname IN ('plex.home', 'nas.home')`).Scan(&modifiedCount); err != nil {
		t.Fatal(err)
	}
	if modifiedCount != 2 {
		t.Fatalf("modified DNS records remaining = %d, want 2", modifiedCount)
	}
}

func TestNormalizeRecordRejectsCNAME(t *testing.T) {
	_, _, _, err := NormalizeRecord("media.home", "CNAME", "server.home")
	if err == nil {
		t.Fatal("expected CNAME record to be rejected")
	}
	if !strings.Contains(err.Error(), `unsupported record type "CNAME"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeRecordAcceptsAddressRecords(t *testing.T) {
	tests := []struct {
		name  string
		typ   string
		value string
	}{
		{name: "IPv4", typ: "A", value: "192.168.1.20"},
		{name: "IPv6", typ: "AAAA", value: "2001:db8::20"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, typ, value, err := NormalizeRecord("Media.Home.", test.typ, test.value)
			if err != nil {
				t.Fatalf("NormalizeRecord returned an error: %v", err)
			}
			if host != "media.home" || typ != test.typ || value != test.value {
				t.Fatalf("unexpected normalized record: host=%q type=%q value=%q", host, typ, value)
			}
		})
	}
}

func TestDNSRecordsAllowDualStackAnswers(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, statement := range []string{
		`INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'A', '192.168.1.20')`,
		`INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'AAAA', '2001:db8::20')`,
	} {
		if _, err := store.DB.Exec(statement); err != nil {
			t.Fatalf("insert dual-stack record: %v", err)
		}
	}
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE hostname = 'nas.home'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("dual-stack record count = %d, err = %v", count, err)
	}
}

func TestOpenMigratesHostnameOnlyRecordConstraint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "faro.db")
	legacy, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE dns_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT, hostname TEXT NOT NULL UNIQUE, type TEXT NOT NULL DEFAULT 'A',
		value TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'A', '192.168.1.20')`); err != nil {
		t.Fatal(err)
	}
	_ = legacy.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'AAAA', '2001:db8::20')`); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
}

func TestNormalizeRecordRejectsMismatchedAddressFamilies(t *testing.T) {
	for _, test := range []struct{ typ, value string }{{"A", "2001:db8::20"}, {"AAAA", "192.168.1.20"}} {
		if _, _, _, err := NormalizeRecord("nas.home", test.typ, test.value); err == nil {
			t.Fatalf("expected %s record with %s to be rejected", test.typ, test.value)
		}
	}
}

func TestDeviceIdentityMigrationPreservesLegacyData(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var protectionID int64
	if err := store.DB.QueryRow(`SELECT id FROM protection_profiles WHERE is_default = 1`).Scan(&protectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO device_aliases(client_ip, name, location, notes) VALUES('192.168.1.44', 'Living room TV', 'Living room', 'OLED');
		INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES('192.168.1.44', ?);
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source)
		VALUES('2026-07-18T10:00:00Z', '192.168.1.44', 'stream.example', 'A', 'allowed', 'upstream');
		DELETE FROM settings WHERE key = 'device_identity_migration_completed'`, protectionID); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateDeviceIdentities(context.Background()); err != nil {
		t.Fatal(err)
	}

	var deviceID, queryDeviceID, membershipID int64
	var name, location, notes string
	if err := store.DB.QueryRow(`
		SELECT d.id, d.name, d.location, d.notes
		FROM devices d JOIN device_addresses a ON a.device_id = d.id
		WHERE a.address = '192.168.1.44'`).Scan(&deviceID, &name, &location, &notes); err != nil {
		t.Fatal(err)
	}
	if name != "Living room TV" || location != "Living room" || notes != "OLED" {
		t.Fatalf("legacy alias was not preserved: name=%q location=%q notes=%q", name, location, notes)
	}
	if err := store.DB.QueryRow(`SELECT device_id FROM dns_queries WHERE client_ip = '192.168.1.44'`).Scan(&queryDeviceID); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`SELECT device_id FROM device_protection_memberships WHERE protection_id = ?`, protectionID).Scan(&membershipID); err != nil {
		t.Fatal(err)
	}
	if queryDeviceID != deviceID || membershipID != deviceID {
		t.Fatalf("stable references = query %d membership %d, want device %d", queryDeviceID, membershipID, deviceID)
	}
}

package db

import (
	"context"
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

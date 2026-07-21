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

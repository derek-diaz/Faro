package blocklists

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/derek/faro/internal/db"
)

func TestParseDNSAndAdblockRules(t *testing.T) {
	domains, err := Parse(strings.NewReader(`
# hosts and plain domain formats
0.0.0.0 hosts.example
plain.example
||adblock.example^
||options.example^$important
@@||allowed.example^
||path.example/path.js
page.example##.advert
/regular-expression/
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"adblock.example", "hosts.example", "options.example", "plain.example"}
	if !reflect.DeepEqual(domains, want) {
		t.Fatalf("Parse() = %#v, want %#v", domains, want)
	}
}

func TestEmptyRefreshPreservesLastKnownGoodEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := store.DB.Exec(`INSERT INTO blocklists(name, url, enabled) VALUES('test', ?, 1)`, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	if _, err := store.DB.Exec(`INSERT INTO blocklist_entries(blocklist_id, domain) VALUES(?, 'ads.example')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := (Refresher{Store: store}).Refresh(context.Background(), id); err == nil {
		t.Fatal("empty blocklist refresh unexpectedly succeeded")
	}
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM blocklist_entries WHERE blocklist_id = ?`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("last-known-good entry count = %d, err = %v", count, err)
	}
}

func TestApplyFailureRestoresPreviousEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("new.example\n"))
	}))
	defer server.Close()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := store.DB.Exec(`INSERT INTO blocklists(name, url, enabled) VALUES('test', ?, 1)`, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	if _, err := store.DB.Exec(`INSERT INTO blocklist_entries(blocklist_id, domain) VALUES(?, 'old.example')`, id); err != nil {
		t.Fatal(err)
	}
	_, err = (Refresher{Store: store}).RefreshAndApply(context.Background(), id, func(context.Context) error {
		return errors.New("disk full")
	})
	if err == nil {
		t.Fatal("refresh unexpectedly succeeded")
	}
	var domain string
	if err := store.DB.QueryRow(`SELECT domain FROM blocklist_entries WHERE blocklist_id = ?`, id).Scan(&domain); err != nil || domain != "old.example" {
		t.Fatalf("restored domain = %q, err = %v", domain, err)
	}
}

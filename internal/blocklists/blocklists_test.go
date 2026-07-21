package blocklists

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestRefreshUsesExplicitDNSUpstream(t *testing.T) {
	dnsServer, queries := startBlocklistTestDNSServer(t)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ads.example\ntelemetry.example\n"))
	}))
	defer httpServer.Close()
	parsed, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}

	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := store.DB.Exec(`INSERT INTO blocklists(name, url, enabled) VALUES('test', ?, 1)`, "http://blocklist.example:"+port)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()

	count, err := (Refresher{Store: store, DNSUpstreams: []string{dnsServer}}).Refresh(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("refreshed entry count = %d, want 2", count)
	}
	select {
	case query := <-queries:
		if query != "blocklist.example." {
			t.Fatalf("unexpected DNS query: %q", query)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("configured DNS upstream did not receive the blocklist lookup")
	}
}

func TestBlocklistDNSUpstreamsExcludeFaro(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`UPDATE settings SET value = '192.168.7.228,1.1.1.1' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE settings SET value = '192.168.7.228' WHERE key = 'faro_lan_ip'`); err != nil {
		t.Fatal(err)
	}
	got := (Refresher{Store: store}).blocklistDNSUpstreams(context.Background())
	want := []string{"1.1.1.1:53"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected blocklist DNS upstreams: got %v want %v", got, want)
	}
}

func TestRefreshDueReportsFailuresForEarlyRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO blocklists(name, url, enabled) VALUES('test', ?, 1)`, server.URL); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, nil)
	if !manager.refreshDue(context.Background()) {
		t.Fatal("failed automatic refresh was not reported for an early retry")
	}
}

func startBlocklistTestDNSServer(t *testing.T) (string, <-chan string) {
	t.Helper()
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	queries := make(chan string, 4)
	go func() {
		for {
			packet := make([]byte, 1232)
			n, client, err := connection.ReadFromUDP(packet)
			if err != nil {
				return
			}
			packet = packet[:n]
			name, questionEnd, queryType, ok := parseBlocklistTestQuestion(packet)
			if !ok {
				continue
			}
			select {
			case queries <- name:
			default:
			}
			response := append([]byte(nil), packet[:questionEnd]...)
			response[2] = 0x81
			response[3] = 0x80
			binary.BigEndian.PutUint16(response[4:6], 1)
			answerCount := uint16(0)
			if queryType == 1 {
				answerCount = 1
			}
			binary.BigEndian.PutUint16(response[6:8], answerCount)
			binary.BigEndian.PutUint16(response[8:10], 0)
			binary.BigEndian.PutUint16(response[10:12], 0)
			if answerCount == 1 {
				response = append(response,
					0xc0, 0x0c,
					0x00, 0x01,
					0x00, 0x01,
					0x00, 0x00, 0x00, 0x3c,
					0x00, 0x04,
					127, 0, 0, 1,
				)
			}
			_, _ = connection.WriteToUDP(response, client)
		}
	}()
	return connection.LocalAddr().String(), queries
}

func parseBlocklistTestQuestion(packet []byte) (string, int, uint16, bool) {
	if len(packet) < 17 {
		return "", 0, 0, false
	}
	labels := []string{}
	position := 12
	for position < len(packet) {
		length := int(packet[position])
		position++
		if length == 0 {
			break
		}
		if position+length > len(packet) {
			return "", 0, 0, false
		}
		labels = append(labels, string(packet[position:position+length]))
		position += length
	}
	if position+4 > len(packet) {
		return "", 0, 0, false
	}
	queryType := binary.BigEndian.Uint16(packet[position : position+2])
	return strings.Join(labels, ".") + ".", position + 4, queryType, true
}

package dohproxy

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derek/faro/internal/db"
)

func TestEndpointsForAddressesCollapsesProviderPair(t *testing.T) {
	endpoints, err := EndpointsForAddresses([]string{"1.1.1.1", "1.0.0.1", "9.9.9.9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(endpoints))
	}
	if endpoints[0].URL != "https://cloudflare-dns.com/dns-query" || endpoints[1].URL != "https://dns.quad9.net/dns-query" {
		t.Fatalf("unexpected endpoints: %#v", endpoints)
	}
}

func TestEndpointsForAddressesRejectsUnsupportedResolver(t *testing.T) {
	_, err := EndpointsForAddresses([]string{"192.0.2.53"})
	if err == nil || !strings.Contains(err.Error(), "Standard DNS") {
		t.Fatalf("expected actionable unsupported resolver error, got %v", err)
	}
}

func TestExchangeUsesRFC8484WireFormat(t *testing.T) {
	query := probeQuery(0x1234)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != dnsMessageContentType || r.Header.Get("Accept") != dnsMessageContentType {
			t.Errorf("unexpected DoH headers: %#v", r.Header)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		response := append([]byte(nil), body...)
		response[2] |= 0x80
		response[3] |= 0x80
		w.Header().Set("Content-Type", dnsMessageContentType)
		_, _ = w.Write(response)
	}))
	defer server.Close()

	response, err := exchangeWithClient(context.Background(), server.URL, query, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(response[:2]) != 0x1234 || response[2]&0x80 == 0 {
		t.Fatalf("invalid DNS response: %x", response[:12])
	}
}

func TestExchangeRejectsNonDNSResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer server.Close()
	if _, err := exchangeWithClient(context.Background(), server.URL, probeQuery(1), server.Client()); err == nil {
		t.Fatal("expected non-DNS response to be rejected")
	}
}

func TestReloadRejectsEncryptedCustomResolverWithoutReplacingState(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		UPDATE settings SET value = 'encrypted' WHERE key = 'upstream_transport';
		UPDATE settings SET value = '1.1.1.1,1.0.0.1' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}
	proxy := New(store, "127.0.0.1:0")
	if err := proxy.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(proxy.state.Load().clients); got != 1 {
		t.Fatalf("clients = %d, want 1", got)
	}
	if _, err := store.DB.Exec(`UPDATE settings SET value = '192.0.2.53' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Reload(context.Background()); err == nil {
		t.Fatal("expected custom encrypted resolver to be rejected")
	}
	if got := len(proxy.state.Load().clients); got != 1 {
		t.Fatalf("failed reload replaced working state: clients = %d", got)
	}
}

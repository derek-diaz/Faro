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

func TestCatalogReturnsIndependentCopy(t *testing.T) {
	first := Catalog()
	if len(first) == 0 || len(first[0].BootstrapIPs) == 0 {
		t.Fatal("expected encrypted DNS catalog entries")
	}
	original := first[0].BootstrapIPs[0]
	first[0].BootstrapIPs[0] = "192.0.2.1"
	second := Catalog()
	if second[0].BootstrapIPs[0] != original {
		t.Fatal("catalog result mutated shared endpoint data")
	}
}

func TestExchangeUsesRFC8484WireFormat(t *testing.T) {
	query := probeQuery(0x1234)
	server := httptest.NewTLSServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.Header.Get("Content-Type") != dnsMessageContentType || request.Header.Get("Accept") != dnsMessageContentType {
			t.Errorf("unexpected DoH headers: %#v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		response := append([]byte(nil), body...)
		response[2] |= 0x80
		response[3] |= 0x80
		responseWriter.Header().Set("Content-Type", dnsMessageContentType)
		_, _ = responseWriter.Write(response)
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
	server := httptest.NewTLSServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Content-Type", "text/html")
		_, _ = responseWriter.Write([]byte("<html></html>"))
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

func TestReloadRejectsUnknownTransportWithoutReplacingState(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		UPDATE settings SET value = 'encrypted' WHERE key = 'upstream_transport';
		UPDATE settings SET value = '1.1.1.1' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}
	proxy := New(store, "127.0.0.1:0")
	if err := proxy.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE settings SET value = 'invalid' WHERE key = 'upstream_transport'`); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Reload(context.Background()); err == nil {
		t.Fatal("unknown upstream transport was accepted")
	}
	if got := len(proxy.state.Load().clients); got != 1 {
		t.Fatalf("invalid reload replaced working encrypted state: clients = %d", got)
	}
}

func TestAllSelectedDoHFailuresReturnVisibleSERVFAIL(t *testing.T) {
	servers := make([]*httptest.Server, 0, 2)
	clients := make([]*endpointClient, 0, 2)
	for serverIndex := 0; serverIndex < 2; serverIndex++ {
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
			http.Error(responseWriter, "upstream unavailable", http.StatusServiceUnavailable)
		}))
		servers = append(servers, server)
		clients = append(clients, &endpointClient{
			endpoint: Endpoint{URL: server.URL},
			client:   server.Client(),
		})
	}
	for _, server := range servers {
		defer server.Close()
	}

	proxy := &Proxy{concurrent: make(chan struct{}, 1)}
	proxy.state.Store(&resolverState{clients: clients})
	query := probeQuery(0x4321)
	response := proxy.response(context.Background(), query)
	if len(response) < 12 {
		t.Fatalf("all-provider failure returned no DNS response: %x", response)
	}
	if binary.BigEndian.Uint16(response[:2]) != 0x4321 {
		t.Fatalf("SERVFAIL changed the query ID: %x", response[:12])
	}
	if response[2]&0x80 == 0 || response[3]&0x0f != 2 {
		t.Fatalf("all-provider failure was not visible as SERVFAIL: %x", response[:12])
	}
}

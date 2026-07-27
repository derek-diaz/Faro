package handlers

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

func TestCachedFaviconHonorsRecentFailure(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	handler := &Handler{store: store}
	if _, err := store.DB.Exec(`
		INSERT INTO domain_favicons(domain, favicon_url, local_path, last_checked_at)
		VALUES('missing.example', '', '', CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatal(err)
	}

	path, cached, err := handler.cachedFavicon(context.Background(), "missing.example")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || !cached {
		t.Fatalf("expected a recent failed lookup to be cached, got path=%q cached=%v", path, cached)
	}

	if _, err := store.DB.Exec(`
		UPDATE domain_favicons
		SET last_checked_at = datetime('now', '-16 minutes')
		WHERE domain = 'missing.example'
	`); err != nil {
		t.Fatal(err)
	}
	path, cached, err = handler.cachedFavicon(context.Background(), "missing.example")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || cached {
		t.Fatalf("expected an expired failed lookup to be retried, got path=%q cached=%v", path, cached)
	}
}

func TestFaviconDNSUpstreamsExcludeFaro(t *testing.T) {
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

	handler := &Handler{store: store}
	got := handler.faviconDNSUpstreams(context.Background())
	want := []string{"1.1.1.1:53"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected favicon DNS upstreams: got %v want %v", got, want)
	}
}

func TestUpstreamResolverUsesExplicitServer(t *testing.T) {
	server, queries := startFaviconTestDNSServer(t)
	resolver := newUpstreamResolver([]string{server})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addresses, err := resolver.LookupNetIP(ctx, "ip4", "favicon.example.")
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 1 || addresses[0] != netip.MustParseAddr("203.0.113.10") {
		t.Fatalf("unexpected resolved addresses: %v", addresses)
	}

	select {
	case query := <-queries:
		if query != "favicon.example." {
			t.Fatalf("unexpected DNS query: %q", query)
		}
	case <-ctx.Done():
		t.Fatal("explicit DNS server did not receive a query")
	}
}

func TestPublicFaviconIPRejectsHostAndSharedAddresses(t *testing.T) {
	tests := map[string]bool{
		"1.1.1.1":         true,
		"2606:4700::1111": true,
		"127.0.0.1":       false,
		"192.168.7.228":   false,
		"100.64.0.1":      false,
		"::1":             false,
	}
	for raw, expected := range tests {
		if got := isPublicFaviconIP(netip.MustParseAddr(raw)); got != expected {
			t.Errorf("isPublicFaviconIP(%s) = %v, want %v", raw, got, expected)
		}
	}
}

func TestSafeFaviconDomainRejectsRecursiveLabels(t *testing.T) {
	tests := map[string]bool{
		"features.plex.tv":              true,
		"www.features.plex.tv":          true,
		"www.www.www.example.com":       false,
		"example.com.hooli.hooli.hooli": false,
	}
	for domain, expected := range tests {
		if got := isSafeFaviconDomain(domain); got != expected {
			t.Errorf("isSafeFaviconDomain(%q) = %v, want %v", domain, got, expected)
		}
	}
}

func TestFaviconCandidatesFallBackToRegistrableDomain(t *testing.T) {
	direct, pages := faviconCandidates("settings-win.data.microsoft.com")
	wantDirect := []string{
		"https://settings-win.data.microsoft.com/favicon.ico",
		"https://microsoft.com/favicon.ico",
		"https://www.microsoft.com/favicon.ico",
	}
	if !reflect.DeepEqual(direct, wantDirect) {
		t.Fatalf("direct candidates = %#v, want %#v", direct, wantDirect)
	}
	if len(pages) == 0 || pages[0] != "https://microsoft.com/" {
		t.Fatalf("page candidates = %#v", pages)
	}
}

func TestFaviconLinksResolveDeclaredIconsSafely(t *testing.T) {
	base, err := url.Parse("https://www.example.com/products/")
	if err != nil {
		t.Fatal(err)
	}
	links := faviconLinks(base, []byte(`
		<html><head>
			<link rel="icon" href="/assets/icon.svg">
			<link rel="apple-touch-icon" href="https://cdn.example.com/touch.png">
			<link rel="icon" href="http://insecure.example.com/icon.png">
			<link rel="icon" href="https://example.com:8443/private.png">
		</head></html>
	`))
	want := []string{
		"https://www.example.com/assets/icon.svg",
		"https://cdn.example.com/touch.png",
	}
	if !reflect.DeepEqual(links, want) {
		t.Fatalf("discovered links = %#v, want %#v", links, want)
	}
}

func TestFaviconDiscoveryUsesHeadOfLargePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><link rel="icon" href="https://cdn.example.com/icon.png"></head><body>`))
		_, _ = w.Write([]byte(strings.Repeat("x", maxFaviconPageBytes*2)))
	}))
	defer server.Close()
	candidates := discoverFaviconCandidates(context.Background(), server.Client(), server.URL)
	want := []string{"https://cdn.example.com/icon.png"}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("large-page candidates = %#v, want %#v", candidates, want)
	}
}

func TestDownloadFaviconAcceptsDetectedImageWithGenericContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	}))
	defer server.Close()
	body, _, err := downloadFavicon(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatal("downloaded favicon was empty")
	}
}

func TestDownloadFaviconRejectsHTMLMislabeledAsImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("<html><body>not an icon</body></html>"))
	}))
	defer server.Close()
	if _, _, err := downloadFavicon(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("HTML response was accepted as a favicon")
	}
}

func startFaviconTestDNSServer(t *testing.T) (string, <-chan string) {
	t.Helper()
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	queries := make(chan string, 1)
	go func() {
		packet := make([]byte, 1232)
		n, client, err := connection.ReadFromUDP(packet)
		if err != nil {
			return
		}
		packet = packet[:n]
		name, questionEnd, ok := parseFaviconTestQuestion(packet)
		if !ok {
			return
		}
		queries <- name

		response := append([]byte(nil), packet[:questionEnd]...)
		response[2] = 0x81
		response[3] = 0x80
		binary.BigEndian.PutUint16(response[4:6], 1)
		binary.BigEndian.PutUint16(response[6:8], 1)
		binary.BigEndian.PutUint16(response[8:10], 0)
		binary.BigEndian.PutUint16(response[10:12], 0)
		response = append(response,
			0xc0, 0x0c, // compressed query name
			0x00, 0x01, // A
			0x00, 0x01, // IN
			0x00, 0x00, 0x00, 0x3c, // 60 second TTL
			0x00, 0x04,
			203, 0, 113, 10,
		)
		_, _ = connection.WriteToUDP(response, client)
	}()
	return connection.LocalAddr().String(), queries
}

func parseFaviconTestQuestion(packet []byte) (string, int, bool) {
	if len(packet) < 17 {
		return "", 0, false
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
			return "", 0, false
		}
		labels = append(labels, string(packet[position:position+length]))
		position += length
	}
	if position+4 > len(packet) {
		return "", 0, false
	}
	return strings.Join(labels, ".") + ".", position + 4, true
}

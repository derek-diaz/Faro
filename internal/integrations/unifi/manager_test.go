package unifi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
)

type fakeUniFi struct {
	mu      sync.Mutex
	clients []Client
}

func (f *fakeUniFi) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-Key") != "local-api-key" {
		http.Error(w, `{"message":"Forbidden"}`, http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/proxy/network/integration/v1/sites":
		_ = json.NewEncoder(w).Encode(page[Site]{
			Offset: 0, Limit: 200, Count: 1, TotalCount: 1,
			Data: []Site{{ID: "site-1", Name: "Home"}},
		})
	case "/proxy/network/integration/v1/sites/site-1/clients":
		f.mu.Lock()
		clients := append([]Client(nil), f.clients...)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(page[Client]{
			Offset: 0, Limit: 200, Count: len(clients), TotalCount: len(clients), Data: clients,
		})
	default:
		http.NotFound(w, r)
	}
}

func TestManagerRequiresExplicitTrustThenKeepsIdentityAcrossIPChanges(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	fake := &fakeUniFi{clients: []Client{{
		ID: "client-1", Name: "Living Room Speaker", IPAddress: "192.168.7.20",
		MACAddress: "02:11:22:33:44:55", Type: "WIRELESS",
	}}}
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)
	manager := NewManager(store, filepath.Join(t.TempDir(), "integration.key"))

	testResult, err := manager.Test(context.Background(), TestInput{BaseURL: server.URL, APIKey: "local-api-key"})
	if err != nil {
		t.Fatalf("test untrusted connection: %v", err)
	}
	if !testResult.RequiresCertificateTrust || testResult.Certificate == nil {
		t.Fatalf("expected certificate review, got %#v", testResult)
	}

	sum := sha256.Sum256(server.Certificate().Raw)
	fingerprint := hex.EncodeToString(sum[:])
	testResult, err = manager.Test(context.Background(), TestInput{
		BaseURL: server.URL, APIKey: "local-api-key", TLSFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("test pinned connection: %v", err)
	}
	if !testResult.OK || len(testResult.Sites) != 1 || testResult.Sites[0].Name != "Home" {
		t.Fatalf("unexpected site result: %#v", testResult)
	}

	deviceID, err := deviceidentity.ResolveAddress(context.Background(), store, "192.168.7.20", "dns")
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if _, err := store.DB.Exec(`UPDATE devices SET name = 'Kitchen Echo', confirmed = 1 WHERE id = ?`, deviceID); err != nil {
		t.Fatalf("confirm device: %v", err)
	}

	status, err := manager.Configure(context.Background(), ConfigureInput{
		BaseURL: server.URL, APIKey: "local-api-key", SiteID: "site-1", TLSFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if !status.Configured || status.SyncedDevices != 1 || status.SiteName != "Home" {
		t.Fatalf("unexpected status: %#v", status)
	}
	var ciphertext string
	if err := store.DB.QueryRow(`SELECT secret_ciphertext FROM integration_configs WHERE kind = 'unifi'`).Scan(&ciphertext); err != nil {
		t.Fatalf("read stored credential: %v", err)
	}
	if ciphertext == "" || strings.Contains(ciphertext, "local-api-key") {
		t.Fatal("API key was not encrypted at rest")
	}

	fake.mu.Lock()
	fake.clients[0].IPAddress = "192.168.7.21"
	fake.mu.Unlock()
	if _, err := manager.Sync(context.Background()); err != nil {
		t.Fatalf("sync changed address: %v", err)
	}
	resolvedID, found, err := deviceidentity.DeviceIDForAddress(context.Background(), store, "192.168.7.21")
	if err != nil || !found {
		t.Fatalf("new address not observed: found=%v err=%v", found, err)
	}
	if resolvedID != deviceID {
		t.Fatalf("new address belongs to device %d, want %d", resolvedID, deviceID)
	}
	var name string
	var confirmed int
	if err := store.DB.QueryRow(`SELECT name, confirmed FROM devices WHERE id = ?`, deviceID).Scan(&name, &confirmed); err != nil {
		t.Fatalf("read device: %v", err)
	}
	if name != "Kitchen Echo" || confirmed != 1 {
		t.Fatalf("UniFi overwrote confirmed device: name=%q confirmed=%d", name, confirmed)
	}
	var externalName string
	if err := store.DB.QueryRow(`SELECT name FROM device_names WHERE device_id = ? AND source = 'unifi'`, deviceID).Scan(&externalName); err != nil {
		t.Fatalf("read external name: %v", err)
	}
	if externalName != "Living Room Speaker" {
		t.Fatalf("external name = %q", externalName)
	}
}

func TestLocalNetworkDialPolicy(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "192.168.1.1", "10.0.0.1", "172.16.1.1", "100.64.0.10", "fe80::1", "fd00::1"} {
		if !isLocalAddress(net.ParseIP(address)) {
			t.Errorf("%s should be accepted as local", address)
		}
	}
	for _, address := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if isLocalAddress(net.ParseIP(address)) {
			t.Errorf("%s should not be accepted as local", address)
		}
	}
}

func TestNormalizeBaseURLRejectsInsecureOrApplicationPaths(t *testing.T) {
	if _, err := normalizeBaseURL("http://192.168.1.1"); err == nil {
		t.Fatal("expected HTTP URL to be rejected")
	}
	if _, err := normalizeBaseURL("https://192.168.1.1/custom"); err == nil {
		t.Fatal("expected custom path to be rejected")
	}
	got, err := normalizeBaseURL("192.168.1.1/proxy/network/integration/v1")
	if err != nil || got != "https://192.168.1.1" {
		t.Fatalf("normalized URL = %q, err=%v", got, err)
	}
}

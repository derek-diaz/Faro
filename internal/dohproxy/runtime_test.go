package dohproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeSnapshotRejectsInvalidReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "faro-doh.json")
	accepted := RuntimeConfig{
		Transport:   "encrypted",
		UpstreamDNS: "1.1.1.1,1.0.0.1",
		Generation:  "accepted",
	}
	if err := WriteRuntimeConfig(path, accepted); err != nil {
		t.Fatal(err)
	}

	invalid := RuntimeConfig{
		Transport:   "encrypted",
		UpstreamDNS: "192.0.2.53",
		Generation:  "rejected",
	}
	if err := WriteRuntimeConfig(path, invalid); err == nil {
		t.Fatal("invalid encrypted runtime configuration was written")
	}

	got, err := ReadRuntimeConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != accepted.Generation || got.UpstreamDNS != accepted.UpstreamDNS {
		t.Fatalf("invalid snapshot replaced accepted state: %#v", got)
	}

	replacement := accepted
	replacement.Generation = "replacement"
	if err := WriteRuntimeConfig(path, replacement); err != nil {
		t.Fatal(err)
	}
	got, err = ReadRuntimeConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != replacement.Generation {
		t.Fatalf("valid runtime snapshot was not published: %#v", got)
	}
}

func TestReadRuntimeSnapshotRejectsTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "faro-doh.json")
	if err := os.WriteFile(path, []byte(`{"transport":"standard","upstream_dns":"8.8.8.8","generation":"one"} {"transport":"standard"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRuntimeConfig(path); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("expected trailing data rejection, got %v", err)
	}
}

func TestReloadConfigRejectsInvalidSnapshotWithoutReplacingState(t *testing.T) {
	proxy := New(nil, "127.0.0.1:0")
	accepted := RuntimeConfig{
		Transport:   "encrypted",
		UpstreamDNS: "1.1.1.1",
		Generation:  "accepted",
	}
	if err := proxy.ReloadConfig(accepted); err != nil {
		t.Fatal(err)
	}
	if err := proxy.ReloadConfig(RuntimeConfig{
		Transport:   "encrypted",
		UpstreamDNS: "192.0.2.53",
		Generation:  "rejected",
	}); err == nil {
		t.Fatal("invalid encrypted runtime configuration was applied")
	}
	if state := proxy.state.Load(); state == nil || len(state.clients) != 1 {
		t.Fatalf("invalid snapshot replaced accepted resolver state: %#v", state)
	}
}

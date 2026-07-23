package coredns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derek/faro/internal/db"
)

func TestValidationFilesUseStagedPathsAndEphemeralDNSPort(t *testing.T) {
	files := map[string][]byte{
		"Corefile": []byte(`.:53 {
    prometheus 0.0.0.0:9153
    hosts /etc/coredns/protection-1.hosts
    hosts /etc/coredns/local.hosts
    forward . 1.1.1.1
}`),
		"local.hosts":        []byte("127.0.0.1 router.home\n"),
		"protection-1.hosts": []byte("0.0.0.0 ads.example\n"),
	}
	stagingDir := filepath.Join(t.TempDir(), "validation")
	staged, err := validationFiles(stagingDir, files)
	if err != nil {
		t.Fatal(err)
	}
	corefile := string(staged["Corefile"])
	if strings.Contains(corefile, "/etc/coredns/") {
		t.Fatalf("validation Corefile still references live paths:\n%s", corefile)
	}
	if !strings.Contains(corefile, ".:0 {") {
		t.Fatalf("validation Corefile does not use an ephemeral DNS port:\n%s", corefile)
	}
	if !strings.Contains(corefile, "prometheus 127.0.0.1:0") || strings.Contains(corefile, "0.0.0.0:9153") {
		t.Fatalf("validation Corefile could conflict with live metrics:\n%s", corefile)
	}
	if !strings.Contains(corefile, filepath.ToSlash(stagingDir)+"/protection-1.hosts") {
		t.Fatalf("validation Corefile does not reference staged hosts files:\n%s", corefile)
	}
	if strings.Contains(string(files["Corefile"]), ".:0 {") {
		t.Fatal("validation mutated the live Corefile bytes")
	}
}

func TestGeneratedValidationRejectsMissingHostsFile(t *testing.T) {
	files := map[string][]byte{
		"Corefile": []byte(`.:53 {
    hosts /etc/coredns/protection-1.hosts
    forward . 1.1.1.1
}`),
	}
	err := validateGeneratedFiles(files)
	if err == nil || !strings.Contains(err.Error(), "missing hosts file") {
		t.Fatalf("validation error = %v; want missing hosts file", err)
	}
}

func TestReloadHashFromMetrics(t *testing.T) {
	expected := strings.Repeat("a", 128)
	metrics := `# HELP coredns_reload_version_info Record hash value during reload.
# TYPE coredns_reload_version_info gauge
coredns_reload_version_info{hash="sha512",value="` + expected + `"} 1
`
	hash, ok, err := reloadHashFromMetrics(strings.NewReader(metrics))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || hash != expected {
		t.Fatalf("reload hash = %q, %v; want %q, true", hash, ok, expected)
	}
}

func TestApplyDoesNotReplaceFilesWhenCoreDNSValidationFails(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := writeTestFile(corefilePath, "last known good"); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, configDir)
	manager.validateGenerated = func(context.Context, map[string][]byte) error {
		return errors.New("unknown plugin")
	}
	manager.readLiveHash = func(context.Context) (string, error) {
		t.Fatal("live CoreDNS should not be contacted after staged validation fails")
		return "", nil
	}

	err := manager.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unknown plugin") {
		t.Fatalf("Apply error = %v; want staged validation failure", err)
	}
	content, readErr := readTestFile(corefilePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if content != "last known good" {
		t.Fatalf("Corefile changed after validation failure: %q", content)
	}
}

func TestApplyRestoresPreviousFilesWhenLiveReloadIsNotAccepted(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := writeTestFile(corefilePath, "last known good"); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, configDir)
	manager.validateGenerated = func(context.Context, map[string][]byte) error { return nil }
	manager.readLiveHash = func(context.Context) (string, error) { return "previous-live-hash", nil }
	waits := 0
	manager.waitForLiveHash = func(_ context.Context, expected string) error {
		waits++
		if waits == 1 {
			return errors.New("reload hash stayed unchanged")
		}
		if expected != "previous-live-hash" {
			t.Fatalf("rollback waited for hash %q", expected)
		}
		return nil
	}

	err := manager.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "previous configuration was restored") {
		t.Fatalf("Apply error = %v; want verified rollback failure", err)
	}
	if waits != 2 {
		t.Fatalf("reload verification calls = %d, want 2", waits)
	}
	content, readErr := readTestFile(corefilePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if content != "last known good" {
		t.Fatalf("Corefile was not restored: %q", content)
	}
}

func openValidationStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func readTestFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	return string(content), err
}

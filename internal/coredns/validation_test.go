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

func TestCorefileHashMatchesCoreDNSReloadMetric(t *testing.T) {
	corefile := []byte(`.:0 {
    errors
    prometheus 127.0.0.1:0
    reload 2s
    forward . 1.1.1.1
}
`)
	hash, err := corefileHash("/config/Corefile", corefile)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "5b5ac0a6f04113e28173c8430db83019b6e6fe65ad4d87b4af4c3bb63bb04df071593012f779d20cde521cc3971017d33b1ab12f4bcd224ecb80a8ca06dee414"
	if hash != expected {
		t.Fatalf("Corefile hash = %s; want CoreDNS reload metric %s", hash, expected)
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
	manager.BeforeApply = func(context.Context) error {
		t.Fatal("transport must not be prepared before staged CoreDNS validation")
		return nil
	}
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

func TestApplyRestoresPreparedTransportWhenLiveReloadFails(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := writeTestFile(corefilePath, "last known good"); err != nil {
		t.Fatal(err)
	}

	prepared := false
	restored := false
	manager := NewManager(store, configDir)
	manager.BeforeApply = func(context.Context) error {
		prepared = true
		return nil
	}
	manager.RollbackApply = func(context.Context) error {
		restored = true
		prepared = false
		return nil
	}
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

	if err := manager.Apply(context.Background()); err == nil {
		t.Fatal("Apply unexpectedly succeeded after live reload failure")
	}
	if !restored || prepared {
		t.Fatalf("prepared transport was not restored: prepared=%v restored=%v", prepared, restored)
	}
	content, err := readTestFile(corefilePath)
	if err != nil {
		t.Fatal(err)
	}
	if content != "last known good" {
		t.Fatalf("Corefile was not restored with prepared transport: %q", content)
	}
}

func TestApplyRestoresFilesAndTransportWhenAcceptedStateCommitFails(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := writeTestFile(corefilePath, "last known good"); err != nil {
		t.Fatal(err)
	}

	prepared := false
	restored := false
	manager := NewManager(store, configDir)
	manager.BeforeApply = func(context.Context) error {
		prepared = true
		return nil
	}
	manager.RollbackApply = func(context.Context) error {
		restored = true
		prepared = false
		return nil
	}
	manager.CommitApply = func(context.Context) error {
		return errors.New("runtime snapshot filesystem is unavailable")
	}
	manager.validateGenerated = func(context.Context, map[string][]byte) error { return nil }
	manager.readLiveHash = func(context.Context) (string, error) { return "previous-live-hash", nil }
	waits := 0
	manager.waitForLiveHash = func(_ context.Context, expected string) error {
		waits++
		if waits == 2 && expected != "previous-live-hash" {
			t.Fatalf("rollback waited for hash %q", expected)
		}
		return nil
	}

	err := manager.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "runtime snapshot filesystem is unavailable") {
		t.Fatalf("Apply error = %v; want accepted-state commit failure", err)
	}
	if waits != 2 {
		t.Fatalf("reload verification calls = %d, want apply and rollback", waits)
	}
	if !restored || prepared {
		t.Fatalf("prepared transport was not restored after commit failure: prepared=%v restored=%v", prepared, restored)
	}
	content, err := readTestFile(corefilePath)
	if err != nil {
		t.Fatal(err)
	}
	if content != "last known good" {
		t.Fatalf("Corefile was not restored after accepted-state commit failure: %q", content)
	}
}

func TestApplyDoesNotPrepareTransportWhenRenderFails(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := writeTestFile(corefilePath, "last known good"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE settings SET value = 'invalid' WHERE key = 'upstream_transport'`); err != nil {
		t.Fatal(err)
	}

	prepared := false
	manager := NewManager(store, configDir)
	manager.BeforeApply = func(context.Context) error {
		prepared = true
		return nil
	}
	manager.RollbackApply = func(context.Context) error {
		t.Fatal("transport rollback should not run when rendering fails")
		return nil
	}

	if err := manager.Apply(context.Background()); err == nil {
		t.Fatal("Apply unexpectedly accepted an invalid control-plane setting")
	}
	if prepared {
		t.Fatal("transport was prepared before the generated configuration was known to be renderable")
	}
	content, err := readTestFile(corefilePath)
	if err != nil {
		t.Fatal(err)
	}
	if content != "last known good" {
		t.Fatalf("Corefile changed after render failure: %q", content)
	}
}

func TestApplyRejectsInvalidStoredLocalRecordWithoutReplacingDNS(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := writeTestFile(corefilePath, "last known good"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value) VALUES('invalid host', 'A', '192.168.1.20')`); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, configDir)
	manager.BeforeApply = func(context.Context) error {
		t.Fatal("invalid stored records must fail before transport preparation")
		return nil
	}
	if err := manager.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid stored DNS record") {
		t.Fatalf("Apply error = %v; want invalid stored record", err)
	}
	content, err := readTestFile(corefilePath)
	if err != nil {
		t.Fatal(err)
	}
	if content != "last known good" {
		t.Fatalf("Corefile changed after invalid stored record: %q", content)
	}
}

func TestApplyBootstrapsMissingLiveReloadHash(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := writeTestFile(corefilePath, "previous Corefile"); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, configDir)
	manager.bootstrapped = true
	manager.validateGenerated = func(context.Context, map[string][]byte) error { return nil }
	manager.readLiveHash = func(context.Context) (string, error) {
		return "", errReloadHashUnavailable
	}
	waits := 0
	manager.waitForLiveHash = func(_ context.Context, expected string) error {
		waits++
		if expected == "" {
			t.Fatal("expected generated Corefile hash")
		}
		return nil
	}

	if err := manager.Apply(context.Background()); err != nil {
		t.Fatalf("Apply returned bootstrap error: %v", err)
	}
	if waits != 1 {
		t.Fatalf("reload verification calls = %d, want 1", waits)
	}
}

func TestApplyAllowsUnchangedCorefileBeforeReloadHashIsInitialized(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()

	manager := NewManager(store, configDir)
	state, err := manager.render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(filepath.Join(configDir, "Corefile"), state.Corefile); err != nil {
		t.Fatal(err)
	}
	manager.bootstrapped = true
	manager.validateGenerated = func(context.Context, map[string][]byte) error { return nil }
	manager.readLiveHash = func(context.Context) (string, error) {
		return "", errReloadHashUnavailable
	}
	manager.waitForLiveHash = func(context.Context, string) error {
		t.Fatal("unchanged Corefile should not require a reload hash")
		return nil
	}

	if err := manager.Apply(context.Background()); err != nil {
		t.Fatalf("Apply returned unchanged-Corefile error: %v", err)
	}
}

func TestApplyReplicaRestoresRuntimeSettingsAndFilesAfterValidationFailure(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := writeTestFile(corefilePath, "last known good"); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, configDir)
	manager.validateGenerated = func(context.Context, map[string][]byte) error {
		return errors.New("replicated configuration is invalid")
	}
	manager.readLiveHash = func(context.Context) (string, error) {
		t.Fatal("live CoreDNS should not be contacted after staged validation fails")
		return "", nil
	}
	files := map[string][]byte{
		"Corefile":   []byte(".:53 {\n  hosts /etc/coredns/faro.hosts\n  forward . 127.0.0.1:5053\n}\n"),
		"faro.hosts": []byte(""),
	}
	err := manager.ApplyReplica(context.Background(), files, map[string]string{
		"upstream_dns":       "8.8.8.8",
		"upstream_transport": "standard",
	})
	if err == nil || !strings.Contains(err.Error(), "replicated configuration is invalid") {
		t.Fatalf("ApplyReplica error = %v", err)
	}
	content, readErr := readTestFile(corefilePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if content != "last known good" {
		t.Fatalf("replica replaced last-known-good Corefile: %q", content)
	}
	var upstream, transport string
	if err := store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&upstream); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'upstream_transport'`).Scan(&transport); err != nil {
		t.Fatal(err)
	}
	if upstream != "1.1.1.1,9.9.9.9" || transport != "standard" {
		t.Fatalf("runtime settings were not restored: upstream=%q transport=%q", upstream, transport)
	}
}

func TestApplyInstallsOnlyCorefileReferencedFilesAndRemovesStaleManagedFiles(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	manager := NewManager(store, configDir)
	if _, err := store.DB.Exec(`INSERT INTO protection_profiles(name, icon) VALUES('Children', 'baby')`); err != nil {
		t.Fatal(err)
	}
	state, err := manager.render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	allFiles := filesFromRenderedState(state)
	writeDiagnosticFiles(t, configDir, allFiles)
	manager.validateGenerated = func(context.Context, map[string][]byte) error { return nil }
	manager.readLiveHash = func(context.Context) (string, error) { return "", errReloadHashUnavailable }

	if err := manager.Apply(context.Background()); err != nil {
		t.Fatalf("Apply returned cleanup error: %v", err)
	}
	runtimeFiles, err := runtimeFilesFromRenderedState(state)
	if err != nil {
		t.Fatal(err)
	}
	for name := range runtimeFiles {
		if _, err := os.Stat(filepath.Join(configDir, name)); err != nil {
			t.Fatalf("runtime file %q was not installed: %v", name, err)
		}
	}
	for name := range allFiles {
		if _, ok := runtimeFiles[name]; ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(configDir, name)); !os.IsNotExist(err) {
			t.Fatalf("stale generated file %q still exists; stat error = %v", name, err)
		}
	}
}

func TestApplyRestoresStaleManagedFilesWhenReloadFails(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	manager := NewManager(store, configDir)
	state, err := manager.render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	allFiles := filesFromRenderedState(state)
	writeDiagnosticFiles(t, configDir, allFiles)
	manager.validateGenerated = func(context.Context, map[string][]byte) error { return nil }
	manager.readLiveHash = func(context.Context) (string, error) { return "previous-live-hash", nil }
	manager.waitForLiveHash = func(_ context.Context, expected string) error {
		if expected == "previous-live-hash" {
			return nil
		}
		return errors.New("new reload hash was not observed")
	}

	if err := manager.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "previous configuration was restored") {
		t.Fatalf("Apply error = %v; want verified rollback", err)
	}
	for name, expected := range allFiles {
		content, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			t.Fatalf("restored file %q could not be read: %v", name, err)
		}
		if string(content) != string(expected) {
			t.Fatalf("restored file %q changed during failed cleanup", name)
		}
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

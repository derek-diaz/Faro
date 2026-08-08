package coredns

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsReportsHealthyStateAndDetectsDrift(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	manager := NewManager(store, configDir)

	state, err := manager.render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeDiagnosticFiles(t, configDir, filesFromRenderedState(state))

	diagnostics, err := manager.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.Status != "healthy" {
		t.Fatalf("diagnostics status = %q; want healthy", diagnostics.Status)
	}
	if len(diagnostics.Files) == 0 || diagnostics.ActiveCorefileHash == "" || diagnostics.GeneratedCorefileHash == "" {
		t.Fatalf("diagnostics did not include the active and generated Corefile hashes: %#v", diagnostics)
	}
	for _, file := range diagnostics.Files {
		if file.Referenced && !file.Matches {
			t.Fatalf("file %q did not match its generated candidate", file.Name)
		}
	}
	for _, file := range diagnostics.Files {
		if file.Name == "local.hosts" || file.Name == "blocklist.hosts" || file.Name == "faro.hosts" {
			if file.Referenced {
				t.Fatalf("generated artifact %q was marked as CoreDNS-referenced", file.Name)
			}
			if file.Active != "" {
				t.Fatalf("unreferenced generated artifact %q was reported as active", file.Name)
			}
		}
	}

	corefilePath := filepath.Join(configDir, "Corefile")
	corefile, err := os.ReadFile(corefilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corefilePath, append(corefile, []byte("\n# external drift\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = manager.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.Status != "drifted" {
		t.Fatalf("drifted diagnostics status = %q; want drifted", diagnostics.Status)
	}
	if diagnostics.ActiveCorefileHash == diagnostics.GeneratedCorefileHash {
		t.Fatal("drifted Corefile kept the generated hash")
	}
}

func TestDiagnosticsKeepsAcceptedFilesVisibleWhenGenerationFails(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	manager := NewManager(store, configDir)
	state, err := manager.render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeDiagnosticFiles(t, configDir, filesFromRenderedState(state))
	if _, err := store.DB.Exec(`UPDATE settings SET value = 'invalid' WHERE key = 'upstream_transport'`); err != nil {
		t.Fatal(err)
	}

	diagnostics, err := manager.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.Status != "generator_error" {
		t.Fatalf("generation failure status = %q; want generator_error", diagnostics.Status)
	}
	if diagnostics.Error == "" || diagnostics.ActiveCorefileHash == "" {
		t.Fatalf("generation failure did not preserve diagnostics context: %#v", diagnostics)
	}
	if len(diagnostics.Files) == 0 || diagnostics.Files[0].Active == "" {
		t.Fatalf("accepted files were not returned after generation failure: %#v", diagnostics.Files)
	}
}

func TestDiagnosticsPreservesGeneratedArtifactsWithoutTreatingThemAsActive(t *testing.T) {
	store := openValidationStore(t)
	defer store.Close()
	configDir := t.TempDir()
	manager := NewManager(store, configDir)
	state, err := manager.render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeDiagnosticFiles(t, configDir, filesFromRenderedState(state))
	diagnostics, err := manager.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var blocklist DiagnosticFile
	for _, file := range diagnostics.Files {
		if file.Name == "blocklist.hosts" {
			blocklist = file
			break
		}
	}
	if blocklist.Referenced {
		t.Fatalf("blocklist artifact was marked as active: %#v", blocklist)
	}
	if blocklist.Active != "" || blocklist.ActiveBytes != 0 {
		t.Fatalf("unreferenced blocklist artifact was reported as active: %#v", blocklist)
	}
	if blocklist.Generated == "" || blocklist.GeneratedBytes == 0 || blocklist.Matches {
		t.Fatalf("generated blocklist artifact was not preserved: %#v", blocklist)
	}
}

func TestSnapshotDiagnosticBytesBoundsLargeGeneratedArtifact(t *testing.T) {
	large := bytes.Repeat([]byte("0.0.0.0 ads.example\n"), maxDiagnosticContentBytes/len("0.0.0.0 ads.example\n")+2)
	snapshot := snapshotDiagnosticBytes(large)
	if !snapshot.truncated || snapshot.bytes != int64(len(large)) {
		t.Fatalf("large generated artifact metadata = %#v", snapshot)
	}
	if len(snapshot.content) != maxDiagnosticContentBytes {
		t.Fatalf("large generated artifact preview length = %d; want %d", len(snapshot.content), maxDiagnosticContentBytes)
	}
	if snapshot.hash == "" || strings.Contains(snapshot.hash, " ") {
		t.Fatalf("large generated artifact hash = %q", snapshot.hash)
	}
}

func writeDiagnosticFiles(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotatingWriterBoundsFilesAndRetainsBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.log")
	writer, err := newRotatingWriter(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"12345678", "abcde", "uvwxyz", "ABCDEFG"} {
		if _, err := writer.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	assertFileContents(t, path, "ABCDEFG")
	assertFileContents(t, path+".1", "uvwxyz")
	assertFileContents(t, path+".2", "abcde")
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: %v", err)
	}
}

func TestRotatingWriterReclaimsOversizedLegacyFilesAtStartup(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "query.log")
	for name, contents := range map[string]string{
		"query.log":   "legacy-active-log-that-is-far-too-large",
		"query.log.1": "legacy-backup-that-is-also-too-large",
		"query.log.2": "small",
		"query.log.9": "stale-out-of-range-backup",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writer, err := newRotatingWriter(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("new-line\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	assertFileContents(t, path, "new-line\n")
	assertFileContents(t, path+".2", "small")
	for _, removed := range []string{path + ".1", path + ".9"} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("legacy file %s still exists: %v", removed, err)
		}
	}
}

func TestRotatingWriterDropsRawLinesWhileStorageRetryIsPaused(t *testing.T) {
	writer := &rotatingWriter{retryAt: time.Now().Add(time.Minute)}
	line := []byte("query that cannot currently be persisted\n")
	written, err := writer.Write(line)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(line) {
		t.Fatalf("written = %d, want %d", written, len(line))
	}
}

func assertFileContents(t *testing.T, path, expected string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != expected {
		t.Fatalf("%s contents = %q, want %q", path, contents, expected)
	}
}

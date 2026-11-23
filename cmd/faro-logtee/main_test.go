package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogFilesHaveUniqueIdentityAndCaptureTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.log")
	line := "[INFO] FARO|192.0.2.20|A|repeat.example.|NOERROR|1ms|9.9.9.9:53\n"
	var stdout bytes.Buffer
	before := time.Now().UTC()
	if err := run(strings.NewReader(line+line), &stdout, path, 100, 2); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	currentID, _, _ := strings.Cut(string(current), "\n")
	previousID, _, _ := strings.Cut(string(previous), "\n")
	if !strings.HasPrefix(currentID, "# FARO-LOG ") || currentID == previousID {
		t.Fatal("rotation reused the file identity")
	}
	for _, output := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		stamp, _, _ := strings.Cut(output, " ")
		when, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || when.Before(before) || when.After(time.Now()) {
			t.Fatalf("missing capture timestamp: %q (%v)", output, err)
		}
	}
}

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
	if strings.HasPrefix(string(contents), "# FARO-LOG ") {
		_, payload, _ := strings.Cut(string(contents), "\n")
		contents = []byte(payload)
	}
	if string(contents) != expected {
		t.Fatalf("%s contents = %q, want %q", path, contents, expected)
	}
}

package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	farobackup "github.com/derek/faro/internal/backup"
	"github.com/derek/faro/internal/db"
)

type restoreObservingReloader struct {
	store     *db.Store
	upstreams []string
}

func (r *restoreObservingReloader) Apply(context.Context) error {
	var upstream string
	if err := r.store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&upstream); err != nil {
		return err
	}
	r.upstreams = append(r.upstreams, upstream)
	if len(r.upstreams) == 1 {
		return errors.New("restored DNS configuration was rejected")
	}
	return nil
}

func TestBackupRestoreRollsBackDatabaseWhenDNSRejectsRestore(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := farobackup.NewService(store)
	const passphrase = "correct horse backup staple"
	backupPath, _, cleanup, err := service.Create(context.Background(), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := store.DB.Exec(`UPDATE settings SET value = '8.8.4.4' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("passphrase", passphrase); err != nil {
		t.Fatal(err)
	}
	filePart, err := writer.CreateFormFile("backup", filepath.Base(backupPath))
	if err != nil {
		t.Fatal(err)
	}
	backupFile, err := os.Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(filePart, backupFile); err != nil {
		_ = backupFile.Close()
		t.Fatal(err)
	}
	_ = backupFile.Close()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reloader := &restoreObservingReloader{store: store}
	handler := &Handler{
		store:    store,
		reloader: reloader,
		backups:  service,
	}
	request := httptest.NewRequest(http.MethodPost, "/api/backups/restore", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.backupRestore(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("restore status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "restore was rolled back") {
		t.Fatalf("restore response did not explain rollback: %s", response.Body.String())
	}
	if len(reloader.upstreams) != 2 {
		t.Fatalf("DNS apply states = %#v, want restored then previous", reloader.upstreams)
	}
	if reloader.upstreams[0] != "1.1.1.1,9.9.9.9" || reloader.upstreams[1] != "8.8.4.4" {
		t.Fatalf("DNS apply states = %#v", reloader.upstreams)
	}
	var upstream string
	if err := store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&upstream); err != nil {
		t.Fatal(err)
	}
	if upstream != "8.8.4.4" {
		t.Fatalf("database kept restored upstream %q", upstream)
	}
	var eventType string
	if err := store.DB.QueryRow(`SELECT type FROM events ORDER BY id DESC LIMIT 1`).Scan(&eventType); err != nil {
		t.Fatal(err)
	}
	if eventType != "backup.restore_rolled_back" {
		t.Fatalf("restore failure event = %q", eventType)
	}
}

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	farobackup "github.com/derek/faro/internal/backup"
)

type backupPassphraseInput struct {
	Passphrase string `json:"passphrase"`
}

func (s *Handler) backupExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	defer logActionError("close backup request body", r.Body.Close)
	var input backupPassphraseInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if err := decoder.Decode(&input); err != nil {
		writeBadRequest(w, errors.New("invalid backup request"))
		return
	}
	path, manifest, cleanup, err := s.backups.Create(r.Context(), input.Passphrase)
	if err != nil {
		if len(input.Passphrase) < farobackup.MinPassphraseLength || len(input.Passphrase) > farobackup.MaxPassphraseLength {
			writeBadRequest(w, err)
			return
		}
		writeError(w, err)
		return
	}
	defer cleanup()
	file, err := os.Open(path)
	if err != nil {
		writeError(w, err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("close backup export file: %v", err)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		writeError(w, err)
		return
	}
	createdAt, _ := time.Parse(time.RFC3339, manifest.CreatedAt)
	filename := fmt.Sprintf("faro-backup-%s.faro-backup", createdAt.Format("2006-01-02-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", contentTypeOptionsNoSniff)
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func (s *Handler) backupRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, farobackup.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeBadRequest(w, errors.New("backup upload is invalid or too large"))
		return
	}
	if r.MultipartForm != nil {
		defer logActionError("remove temporary backup upload files", r.MultipartForm.RemoveAll)
	}
	passphrase := r.FormValue("passphrase")
	if len(passphrase) < farobackup.MinPassphraseLength || len(passphrase) > farobackup.MaxPassphraseLength {
		writeBadRequest(w, fmt.Errorf("backup passphrase must be between %d and %d characters", farobackup.MinPassphraseLength, farobackup.MaxPassphraseLength))
		return
	}
	file, _, err := r.FormFile("backup")
	if err != nil {
		writeBadRequest(w, errors.New("select a Faro backup file"))
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("close backup upload: %v", err)
		}
	}()
	manifest, restore, err := s.backups.BeginRestore(r.Context(), file, passphrase)
	if err != nil {
		if errors.Is(err, farobackup.ErrInvalidBackup) {
			writeBadRequest(w, farobackup.ErrInvalidBackup)
			return
		}
		writeError(w, err)
		return
	}
	restoreFinished := false
	defer rollbackUnfinishedRestore(r.Context(), restore, &restoreFinished)

	if err := s.reloader.Apply(r.Context()); err != nil {
		restoreFinished = true
		s.handleRestoreApplyFailure(w, r, restore, err)
		return
	}
	restore.Commit()
	restoreFinished = true
	s.recordEvent(r.Context(), eventInput{
		Type:        "backup.restored",
		Severity:    "success",
		Title:       "Backup restored",
		Description: "Faro restored its configuration and database from an encrypted backup.",
		Metadata:    map[string]any{"backup_created_at": manifest.CreatedAt},
		Source:      "backup",
	})
	if s.upstreams != nil {
		s.upstreams.Trigger()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"restored_at":    time.Now().UTC().Format(time.RFC3339),
		"backup_created": manifest.CreatedAt,
		"dns_reloaded":   true,
		"warning":        "",
		"requires_login": true,
	})
}

func rollbackUnfinishedRestore(ctx context.Context, restore *farobackup.RestoreTransaction, finished *bool) {
	if *finished {
		return
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	logActionError("rollback incomplete backup restore", func() error {
		return restore.Rollback(rollbackCtx)
	})
}

func (s *Handler) handleRestoreApplyFailure(w http.ResponseWriter, r *http.Request, restore *farobackup.RestoreTransaction, applyErr error) {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	rollbackErr := restore.Rollback(rollbackCtx)
	var reapplyErr error
	if rollbackErr == nil {
		reapplyErr = s.reloader.Apply(rollbackCtx)
	}
	restoreFailure, title, severity := backupRestoreFailure(applyErr, rollbackErr, reapplyErr)
	s.recordEvent(context.WithoutCancel(r.Context()), eventInput{
		Type:        "backup.restore_rolled_back",
		Severity:    severity,
		Title:       title,
		Description: restoreFailure.Error(),
		Source:      "backup",
	})
	writeError(w, restoreFailure)
}

func backupRestoreFailure(applyErr, rollbackErr, reapplyErr error) (error, string, string) {
	restoreFailure := fmt.Errorf("backup restore was rolled back because DNS rejected the restored configuration: %w", applyErr)
	if rollbackErr != nil {
		return fmt.Errorf("DNS rejected the restored configuration (%v) and the previous database could not be recovered: %w", applyErr, rollbackErr), "Backup restore rollback failed", "critical"
	}
	if reapplyErr != nil {
		return fmt.Errorf("backup restore was rolled back after DNS rejected it (%v), but the previous DNS configuration could not be verified: %w", applyErr, reapplyErr), "DNS recovery after backup restore failed", "critical"
	}
	return restoreFailure, "Backup restore rolled back", "warning"
}

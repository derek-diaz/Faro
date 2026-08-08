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

func (handler *Handler) backupExport(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	defer logActionError("close backup request body", request.Body.Close)
	var input backupPassphraseInput
	decoder := json.NewDecoder(http.MaxBytesReader(responseWriter, request.Body, 4<<10))
	if err := decoder.Decode(&input); err != nil {
		writeBadRequest(responseWriter, errors.New("invalid backup request"))
		return
	}
	path, manifest, cleanup, err := handler.backups.Create(request.Context(), input.Passphrase)
	if err != nil {
		if len(input.Passphrase) < farobackup.MinPassphraseLength || len(input.Passphrase) > farobackup.MaxPassphraseLength {
			writeBadRequest(responseWriter, err)
			return
		}
		writeError(responseWriter, err)
		return
	}
	defer cleanup()
	file, err := os.Open(path)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("close backup export file: %v", err)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	createdAt, _ := time.Parse(time.RFC3339, manifest.CreatedAt)
	filename := fmt.Sprintf("faro-backup-%s.faro-backup", createdAt.Format("2006-01-02-150405"))
	responseWriter.Header().Set("Content-Type", "application/octet-stream")
	responseWriter.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	responseWriter.Header().Set("Cache-Control", "no-store")
	responseWriter.Header().Set("X-Content-Type-Options", contentTypeOptionsNoSniff)
	http.ServeContent(responseWriter, request, filename, info.ModTime(), file)
}

func (handler *Handler) backupRestore(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	request.Body = http.MaxBytesReader(responseWriter, request.Body, farobackup.MaxUploadBytes+(1<<20))
	if err := request.ParseMultipartForm(32 << 20); err != nil {
		writeBadRequest(responseWriter, errors.New("backup upload is invalid or too large"))
		return
	}
	if request.MultipartForm != nil {
		defer logActionError("remove temporary backup upload files", request.MultipartForm.RemoveAll)
	}
	passphrase := request.FormValue("passphrase")
	if len(passphrase) < farobackup.MinPassphraseLength || len(passphrase) > farobackup.MaxPassphraseLength {
		writeBadRequest(responseWriter, fmt.Errorf("backup passphrase must be between %d and %d characters", farobackup.MinPassphraseLength, farobackup.MaxPassphraseLength))
		return
	}
	file, _, err := request.FormFile("backup")
	if err != nil {
		writeBadRequest(responseWriter, errors.New("select a Faro backup file"))
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("close backup upload: %v", err)
		}
	}()
	manifest, restore, err := handler.backups.BeginRestore(request.Context(), file, passphrase)
	if err != nil {
		if errors.Is(err, farobackup.ErrInvalidBackup) {
			writeBadRequest(responseWriter, farobackup.ErrInvalidBackup)
			return
		}
		writeError(responseWriter, err)
		return
	}
	restoreFinished := false
	defer rollbackUnfinishedRestore(request.Context(), restore, &restoreFinished)

	if err := handler.reloader.Apply(request.Context()); err != nil {
		restoreFinished = true
		handler.handleRestoreApplyFailure(responseWriter, request, restore, err)
		return
	}
	restore.Commit()
	restoreFinished = true
	handler.recordEvent(request.Context(), eventInput{
		Type:        "backup.restored",
		Severity:    "success",
		Title:       "Backup restored",
		Description: "Faro restored its configuration and database from an encrypted backup.",
		Metadata:    map[string]any{"backup_created_at": manifest.CreatedAt},
		Source:      "backup",
	})
	if handler.upstreams != nil {
		handler.upstreams.Trigger()
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
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

func (handler *Handler) handleRestoreApplyFailure(responseWriter http.ResponseWriter, request *http.Request, restore *farobackup.RestoreTransaction, applyErr error) {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 5*time.Minute)
	defer cancel()
	rollbackErr := restore.Rollback(rollbackCtx)
	var reapplyErr error
	if rollbackErr == nil {
		reapplyErr = handler.reloader.Apply(rollbackCtx)
	}
	restoreFailure, title, severity := backupRestoreFailure(applyErr, rollbackErr, reapplyErr)
	handler.recordEvent(context.WithoutCancel(request.Context()), eventInput{
		Type:        "backup.restore_rolled_back",
		Severity:    severity,
		Title:       title,
		Description: restoreFailure.Error(),
		Source:      "backup",
	})
	writeError(responseWriter, restoreFailure)
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

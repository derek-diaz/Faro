package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
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
	defer r.Body.Close()
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
	defer file.Close()
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
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
		defer r.MultipartForm.RemoveAll()
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
	defer file.Close()
	manifest, err := s.backups.Restore(r.Context(), file, passphrase)
	if err != nil {
		if errors.Is(err, farobackup.ErrInvalidBackup) {
			writeBadRequest(w, farobackup.ErrInvalidBackup)
			return
		}
		writeError(w, err)
		return
	}

	dnsReloaded := true
	warning := ""
	if err := s.reloader.Apply(r.Context()); err != nil {
		dnsReloaded = false
		warning = "The database was restored, but DNS configuration could not be reloaded: " + err.Error()
		s.recordEvent(r.Context(), eventInput{
			Type:        "dns.reload_failed",
			Severity:    "critical",
			Title:       "DNS reload failed after restore",
			Description: err.Error(),
			Source:      "backup",
		})
	} else {
		s.recordEvent(r.Context(), eventInput{
			Type:        "backup.restored",
			Severity:    "success",
			Title:       "Backup restored",
			Description: "Faro restored its configuration and database from an encrypted backup.",
			Metadata:    map[string]any{"backup_created_at": manifest.CreatedAt},
			Source:      "backup",
		})
	}
	if s.upstreams != nil {
		s.upstreams.Trigger()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"restored_at":    time.Now().UTC().Format(time.RFC3339),
		"backup_created": manifest.CreatedAt,
		"dns_reloaded":   dnsReloaded,
		"warning":        warning,
		"requires_login": true,
	})
}

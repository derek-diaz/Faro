package handlers

import (
	"errors"
	"net/http"
	"runtime"
	"time"

	"github.com/derek/faro/internal/retention"
)

type pruneInput struct {
	RetentionDays int  `json:"retention_days"`
	Compact       bool `json:"compact"`
}

func (s *Handler) maintenance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		stats, err := retention.Snapshot(r.Context(), s.store)
		if err != nil {
			writeError(w, err)
			return
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":               "healthy",
			"process_memory_bytes": memory.Alloc,
			"uptime_seconds":       int64(time.Since(s.startedAt).Seconds()),
			"storage":              stats,
		})
	case http.MethodPost:
		input := pruneInput{RetentionDays: retention.ConfiguredDays(r.Context(), s.store), Compact: true}
		if !decode(w, r, &input) {
			return
		}
		if input.RetentionDays < retention.MinDays || input.RetentionDays > retention.MaxDays {
			writeBadRequest(w, errors.New("retention_days must be between 1 and 3650"))
			return
		}
		result, err := retention.Prune(r.Context(), s.store, input.RetentionDays, input.Compact)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		methodNotAllowed(w)
	}
}

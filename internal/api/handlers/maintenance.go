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

func (handler *Handler) maintenance(responseWriter http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		stats, err := retention.Snapshot(request.Context(), handler.store)
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		writeJSON(responseWriter, http.StatusOK, map[string]any{
			"status":               "healthy",
			"process_memory_bytes": memory.Alloc,
			"uptime_seconds":       int64(time.Since(handler.startedAt).Seconds()),
			"storage":              stats,
		})
	case http.MethodPost:
		input := pruneInput{RetentionDays: retention.ConfiguredDays(request.Context(), handler.store), Compact: true}
		if !decode(responseWriter, request, &input) {
			return
		}
		if input.RetentionDays < retention.MinDays || input.RetentionDays > retention.MaxDays {
			writeBadRequest(responseWriter, errors.New("retention_days must be between 1 and 3650"))
			return
		}
		result, err := retention.Prune(request.Context(), handler.store, input.RetentionDays, input.Compact)
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		writeJSON(responseWriter, http.StatusOK, result)
	default:
		methodNotAllowed(responseWriter)
	}
}

package handlers

import (
	"errors"
	"net/http"

	"github.com/derek/faro/internal/integrations/unifi"
)

const unifiUnavailableMessage = "UniFi integration is unavailable"

func (s *Handler) unifiIntegration(w http.ResponseWriter, r *http.Request) {
	if s.unifi == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": unifiUnavailableMessage})
		return
	}
	switch r.Method {
	case http.MethodGet:
		status, err := s.unifi.Status(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodPut:
		var input unifi.ConfigureInput
		if !decode(w, r, &input) {
			return
		}
		status, err := s.unifi.Configure(r.Context(), input)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodDelete:
		if err := s.unifi.Disconnect(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) unifiTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.unifi == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": unifiUnavailableMessage})
		return
	}
	var input unifi.TestInput
	if !decode(w, r, &input) {
		return
	}
	result, err := s.unifi.Test(r.Context(), input)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Handler) unifiSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.unifi == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": unifiUnavailableMessage})
		return
	}
	result, err := s.unifi.Sync(r.Context())
	if errors.Is(err, unifi.ErrNotConfigured) {
		writeBadRequest(w, err)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

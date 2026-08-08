package handlers

import (
	"errors"
	"net/http"

	"github.com/derek/faro/internal/integrations/unifi"
)

const unifiUnavailableMessage = "UniFi integration is unavailable"

func (handler *Handler) unifiIntegration(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.unifi == nil {
		writeJSON(responseWriter, http.StatusServiceUnavailable, map[string]any{"error": unifiUnavailableMessage})
		return
	}
	switch request.Method {
	case http.MethodGet:
		status, err := handler.unifi.Status(request.Context())
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		writeJSON(responseWriter, http.StatusOK, status)
	case http.MethodPut:
		var input unifi.ConfigureInput
		if !decode(responseWriter, request, &input) {
			return
		}
		status, err := handler.unifi.Configure(request.Context(), input)
		if err != nil {
			writeBadRequest(responseWriter, err)
			return
		}
		writeJSON(responseWriter, http.StatusOK, status)
	case http.MethodDelete:
		if err := handler.unifi.Disconnect(request.Context()); err != nil {
			writeError(responseWriter, err)
			return
		}
		writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(responseWriter)
	}
}

func (handler *Handler) unifiTest(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.unifi == nil {
		writeJSON(responseWriter, http.StatusServiceUnavailable, map[string]any{"error": unifiUnavailableMessage})
		return
	}
	var input unifi.TestInput
	if !decode(responseWriter, request, &input) {
		return
	}
	result, err := handler.unifi.Test(request.Context(), input)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (handler *Handler) unifiSync(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.unifi == nil {
		writeJSON(responseWriter, http.StatusServiceUnavailable, map[string]any{"error": unifiUnavailableMessage})
		return
	}
	result, err := handler.unifi.Sync(request.Context())
	if errors.Is(err, unifi.ErrNotConfigured) {
		writeBadRequest(responseWriter, err)
		return
	}
	if err != nil {
		writeJSON(responseWriter, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

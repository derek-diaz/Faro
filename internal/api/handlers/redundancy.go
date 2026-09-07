package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/derek/faro/internal/redundancy"
)

func (handler *Handler) redundancyPublic(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.redundancy == nil {
		writeJSON(responseWriter, http.StatusOK, redundancy.PublicStatus{Role: redundancy.RoleStandalone})
		return
	}
	status, err := handler.redundancy.PublicStatus(request.Context())
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, status)
}

func (handler *Handler) redundancyStatus(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodDelete {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.redundancy == nil {
		writeError(responseWriter, errors.New("redundancy manager is unavailable"))
		return
	}
	if request.Method == http.MethodDelete {
		status, err := handler.redundancy.Leave(request.Context())
		if err != nil {
			writeBadRequest(responseWriter, err)
			return
		}
		writeJSON(responseWriter, http.StatusOK, map[string]any{"status": status})
		return
	}
	status, err := handler.redundancy.Status(request.Context())
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, status)
}

func (handler *Handler) redundancyPairing(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	var activeTrials int
	if err := handler.store.DB.QueryRowContext(request.Context(), `SELECT COUNT(*) FROM troubleshooting_exceptions WHERE julianday(expires_at) > julianday('now')`).Scan(&activeTrials); err != nil {
		writeError(responseWriter, err)
		return
	}
	if activeTrials > 0 {
		writeBadRequest(responseWriter, errors.New("finish or undo temporary troubleshooting tests before pairing DNS replicas"))
		return
	}
	if handler.redundancy == nil {
		writeError(responseWriter, errors.New("redundancy manager is unavailable"))
		return
	}
	var input struct {
		NodeName string `json:"node_name"`
	}
	if !decodeLimitedJSON(responseWriter, request, &input, 4096) {
		return
	}
	pairing, err := handler.redundancy.StartPairing(request.Context(), input.NodeName)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, pairing)
}

func (handler *Handler) redundancyJoin(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.redundancy == nil {
		writeError(responseWriter, errors.New("redundancy manager is unavailable"))
		return
	}
	var input redundancy.JoinInput
	if !decodeLimitedJSON(responseWriter, request, &input, 16<<10) {
		return
	}
	result, err := handler.redundancy.Join(request.Context(), input)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (handler *Handler) redundancyPair(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.redundancy == nil {
		writeError(responseWriter, errors.New("redundancy manager is unavailable"))
		return
	}
	var input redundancy.PairRequest
	if !decodeLimitedJSON(responseWriter, request, &input, 32<<10) {
		return
	}
	result, err := handler.redundancy.AcceptPair(request.Context(), input)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (handler *Handler) redundancySnapshot(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.redundancy == nil {
		writeError(responseWriter, errors.New("redundancy manager is unavailable"))
		return
	}
	nodeID, secret, err := handler.redundancy.AuthenticateNodeRequest(request.Context(), request, nil)
	if err != nil {
		http.Error(responseWriter, `{"error":"replica authentication failed"}`, http.StatusUnauthorized)
		return
	}
	_ = nodeID
	since, err := strconv.ParseInt(request.URL.Query().Get("since"), 10, 64)
	if err != nil || since < 0 {
		writeBadRequest(responseWriter, errors.New("snapshot revision is invalid"))
		return
	}
	envelope, revision, err := handler.redundancy.SnapshotEnvelope(request.Context(), since, secret)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	responseWriter.Header().Set("X-Faro-Revision", strconv.FormatInt(revision, 10))
	if envelope == nil {
		responseWriter.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(responseWriter, http.StatusOK, envelope)
}

func (handler *Handler) redundancyAck(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.redundancy == nil {
		writeError(responseWriter, errors.New("redundancy manager is unavailable"))
		return
	}
	defer logActionError("close redundancy acknowledgement body", request.Body.Close)
	body, err := io.ReadAll(http.MaxBytesReader(responseWriter, request.Body, 16<<10))
	if err != nil {
		writeBadRequest(responseWriter, errors.New("invalid acknowledgement"))
		return
	}
	nodeID, _, err := handler.redundancy.AuthenticateNodeRequest(request.Context(), request, body)
	if err != nil {
		http.Error(responseWriter, `{"error":"replica authentication failed"}`, http.StatusUnauthorized)
		return
	}
	var ack redundancy.SyncAck
	if err := json.Unmarshal(body, &ack); err != nil {
		writeBadRequest(responseWriter, errors.New("invalid acknowledgement"))
		return
	}
	if err := handler.redundancy.RecordAcknowledgement(request.Context(), nodeID, ack); err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) redundancyNode(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodDelete {
		methodNotAllowed(responseWriter)
		return
	}
	if handler.redundancy == nil {
		writeError(responseWriter, errors.New("redundancy manager is unavailable"))
		return
	}
	nodeID := strings.TrimSpace(strings.TrimPrefix(request.URL.Path, "/api/redundancy/nodes/"))
	if len(nodeID) != 32 || strings.Contains(nodeID, "/") {
		writeBadRequest(responseWriter, errors.New("replica server is invalid"))
		return
	}
	if err := handler.redundancy.RemoveNode(request.Context(), nodeID); err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func decodeLimitedJSON(responseWriter http.ResponseWriter, request *http.Request, target any, limit int64) bool {
	defer logActionError("close limited request body", request.Body.Close)
	decoder := json.NewDecoder(http.MaxBytesReader(responseWriter, request.Body, limit))
	if err := decoder.Decode(target); err != nil {
		writeBadRequest(responseWriter, errors.New("invalid request"))
		return false
	}
	return true
}

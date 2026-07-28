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

func (s *Handler) redundancyPublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.redundancy == nil {
		writeJSON(w, http.StatusOK, redundancy.PublicStatus{Role: redundancy.RoleStandalone})
		return
	}
	status, err := s.redundancy.PublicStatus(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Handler) redundancyStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.redundancy == nil {
		writeError(w, errors.New("redundancy manager is unavailable"))
		return
	}
	status, err := s.redundancy.Status(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Handler) redundancyPairing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.redundancy == nil {
		writeError(w, errors.New("redundancy manager is unavailable"))
		return
	}
	var input struct {
		NodeName string `json:"node_name"`
	}
	if !decodeLimitedJSON(w, r, &input, 4096) {
		return
	}
	pairing, err := s.redundancy.StartPairing(r.Context(), input.NodeName)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, pairing)
}

func (s *Handler) redundancyJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.redundancy == nil {
		writeError(w, errors.New("redundancy manager is unavailable"))
		return
	}
	var input redundancy.JoinInput
	if !decodeLimitedJSON(w, r, &input, 16<<10) {
		return
	}
	result, err := s.redundancy.Join(r.Context(), input)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Handler) redundancyPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.redundancy == nil {
		writeError(w, errors.New("redundancy manager is unavailable"))
		return
	}
	var input redundancy.PairRequest
	if !decodeLimitedJSON(w, r, &input, 32<<10) {
		return
	}
	result, err := s.redundancy.AcceptPair(r.Context(), input)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Handler) redundancySnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.redundancy == nil {
		writeError(w, errors.New("redundancy manager is unavailable"))
		return
	}
	nodeID, secret, err := s.redundancy.AuthenticateNodeRequest(r.Context(), r, nil)
	if err != nil {
		http.Error(w, `{"error":"replica authentication failed"}`, http.StatusUnauthorized)
		return
	}
	_ = nodeID
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil || since < 0 {
		writeBadRequest(w, errors.New("snapshot revision is invalid"))
		return
	}
	envelope, revision, err := s.redundancy.SnapshotEnvelope(r.Context(), since, secret)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("X-Faro-Revision", strconv.FormatInt(revision, 10))
	if envelope == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, envelope)
}

func (s *Handler) redundancyAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.redundancy == nil {
		writeError(w, errors.New("redundancy manager is unavailable"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeBadRequest(w, errors.New("invalid acknowledgement"))
		return
	}
	nodeID, _, err := s.redundancy.AuthenticateNodeRequest(r.Context(), r, body)
	if err != nil {
		http.Error(w, `{"error":"replica authentication failed"}`, http.StatusUnauthorized)
		return
	}
	var ack redundancy.SyncAck
	if err := json.Unmarshal(body, &ack); err != nil {
		writeBadRequest(w, errors.New("invalid acknowledgement"))
		return
	}
	if err := s.redundancy.RecordAcknowledgement(r.Context(), nodeID, ack); err != nil {
		writeBadRequest(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Handler) redundancyNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	if s.redundancy == nil {
		writeError(w, errors.New("redundancy manager is unavailable"))
		return
	}
	nodeID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/redundancy/nodes/"))
	if len(nodeID) != 32 || strings.Contains(nodeID, "/") {
		writeBadRequest(w, errors.New("replica server is invalid"))
		return
	}
	if err := s.redundancy.RemoveNode(r.Context(), nodeID); err != nil {
		writeBadRequest(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func decodeLimitedJSON(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := decoder.Decode(target); err != nil {
		writeBadRequest(w, errors.New("invalid request"))
		return false
	}
	return true
}

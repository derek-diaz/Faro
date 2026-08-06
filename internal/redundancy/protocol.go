package redundancy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/derek/faro/internal/secrets"
)

const faroNodeHeader = "X-Faro-Node"

func (m *Manager) AcceptPair(ctx context.Context, input PairRequest) (PairResponse, error) {
	state, err := m.readState(ctx)
	if err != nil {
		return PairResponse{}, err
	}
	if state.Role != RoleController {
		return PairResponse{}, errors.New("this Faro server is not accepting replicas")
	}
	state, err = m.ensureControllerSnapshot(ctx, state)
	if err != nil {
		return PairResponse{}, err
	}
	input, err = normalizePairRequest(input)
	if err != nil {
		return PairResponse{}, err
	}
	session, err := m.pairingSession(input.PairingID)
	if err != nil {
		return PairResponse{}, err
	}
	publicKey, err := decodePairingPublicKey(input.PublicKey)
	if err != nil {
		return PairResponse{}, err
	}
	if err := verifyPairingProof(session, input); err != nil {
		return PairResponse{}, err
	}
	nodeSecret, err := randomBytes(32)
	if err != nil {
		return PairResponse{}, err
	}
	masterKey, err := secrets.LoadOrCreateKey(m.secretKeyPath)
	if err != nil {
		return PairResponse{}, err
	}
	ciphertext, err := secrets.Encrypt(masterKey, nodeSecret)
	if err != nil {
		return PairResponse{}, err
	}
	if err := m.storePairedNode(ctx, input, ciphertext); err != nil {
		return PairResponse{}, err
	}
	key, err := pairingKey(session.PrivateKey, publicKey, session.Token)
	if err != nil {
		return PairResponse{}, err
	}
	payload, err := json.Marshal(pairPayload{
		HomeID: state.HomeID, ControllerNodeID: state.NodeID,
		NodeSecret:     base64.RawURLEncoding.EncodeToString(nodeSecret),
		ConfigRevision: state.ConfigRevision,
	})
	if err != nil {
		return PairResponse{}, fmt.Errorf("encode pairing response: %w", err)
	}
	envelope, err := sealEnvelope(key, payload, "faro-pairing-response-v1")
	if err != nil {
		return PairResponse{}, err
	}
	m.consumePairing(input.PairingID)
	return PairResponse(envelope), nil
}

func (m *Manager) ensureControllerSnapshot(ctx context.Context, state localState) (localState, error) {
	if state.ConfigRevision != 0 {
		return state, nil
	}
	if err := m.captureSnapshot(ctx, state); err != nil {
		return localState{}, fmt.Errorf("prepare controller configuration: %w", err)
	}
	return m.readState(ctx)
}

func normalizePairRequest(input PairRequest) (PairRequest, error) {
	input.NodeID = strings.TrimSpace(input.NodeID)
	input.NodeName = strings.TrimSpace(input.NodeName)
	input.LANAddress = strings.TrimSpace(input.LANAddress)
	if len(input.NodeID) != 32 || input.NodeName == "" || len([]rune(input.NodeName)) > 40 {
		return PairRequest{}, errors.New("replica identity is invalid")
	}
	if netParsePrivate(input.LANAddress) == nil {
		return PairRequest{}, errors.New("replica address must be a private LAN IP")
	}
	return input, nil
}

func (m *Manager) pairingSession(pairingID string) (pairingSession, error) {
	m.mu.Lock()
	session, exists := m.pairings[pairingID]
	m.mu.Unlock()
	if !exists || !session.ExpiresAt.After(m.now()) {
		return pairingSession{}, errors.New("pairing code has expired")
	}
	return session, nil
}

func decodePairingPublicKey(encoded string) ([]byte, error) {
	publicKey, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(publicKey) != 32 {
		return nil, errors.New("replica pairing key is invalid")
	}
	return publicKey, nil
}

func verifyPairingProof(session pairingSession, input PairRequest) error {
	proof, err := base64.RawURLEncoding.DecodeString(input.Proof)
	if err != nil || !hmac.Equal(proof, pairingProof(session.Token, input)) {
		return errors.New("pairing code was not accepted")
	}
	return nil
}

func (m *Manager) storePairedNode(ctx context.Context, input PairRequest, ciphertext string) error {
	_, err := m.store.DB.ExecContext(ctx, `
		INSERT INTO redundancy_nodes(node_id, name, lan_address, secret_ciphertext, config_revision, last_seen_at, updated_at)
		VALUES(?, ?, ?, ?, 0, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(node_id) DO UPDATE SET
			name=excluded.name, lan_address=excluded.lan_address,
			secret_ciphertext=excluded.secret_ciphertext, config_revision=0,
			last_seen_at=excluded.last_seen_at, last_sync_at=NULL, last_error='',
			updated_at=CURRENT_TIMESTAMP`,
		input.NodeID, input.NodeName, input.LANAddress, ciphertext, m.now().UTC().Format(time.RFC3339))
	return err
}

func (m *Manager) consumePairing(pairingID string) {
	m.mu.Lock()
	delete(m.pairings, pairingID)
	m.mu.Unlock()
}

func (m *Manager) AuthenticateNodeRequest(ctx context.Context, request *http.Request, body []byte) (string, []byte, error) {
	state, err := m.readState(ctx)
	if err != nil {
		return "", nil, err
	}
	if state.Role != RoleController {
		return "", nil, errors.New("this Faro server is not a redundancy controller")
	}
	nodeID := strings.TrimSpace(request.Header.Get(faroNodeHeader))
	timestampText := request.Header.Get("X-Faro-Timestamp")
	nonce := request.Header.Get("X-Faro-Nonce")
	signature, err := decodeSignature(request.Header.Get("X-Faro-Signature"))
	if len(nodeID) != 32 || err != nil || nonce == "" {
		return "", nil, errors.New("replica authentication is invalid")
	}
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || absDuration(m.now().Sub(time.Unix(timestamp, 0))) > 2*time.Minute {
		return "", nil, errors.New("replica request timestamp is invalid")
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil || len(nonceBytes) != 16 {
		return "", nil, errors.New("replica request nonce is invalid")
	}
	masterKey, err := secrets.LoadOrCreateKey(m.secretKeyPath)
	if err != nil {
		return "", nil, err
	}
	var secretCiphertext string
	if err := m.store.DB.QueryRowContext(ctx, `SELECT secret_ciphertext FROM redundancy_nodes WHERE node_id = ?`, nodeID).Scan(&secretCiphertext); err != nil {
		return "", nil, errors.New("replica is not paired")
	}
	secret, err := secrets.Decrypt(masterKey, secretCiphertext)
	if err != nil {
		return "", nil, err
	}
	expected := requestSignature(secret, request.Method, request.URL.RequestURI(), timestampText, nonce, body)
	if !hmac.Equal(signature, expected) {
		return "", nil, errors.New("replica authentication failed")
	}
	m.mu.Lock()
	if m.replays[nodeID] == nil {
		m.replays[nodeID] = map[string]time.Time{}
	}
	if _, replayed := m.replays[nodeID][nonce]; replayed {
		m.mu.Unlock()
		return "", nil, errors.New("replica request was already used")
	}
	m.replays[nodeID][nonce] = m.now()
	m.mu.Unlock()
	if _, err := m.store.DB.ExecContext(ctx, `
		UPDATE redundancy_nodes SET last_seen_at = ?, updated_at = CURRENT_TIMESTAMP WHERE node_id = ?`,
		m.now().UTC().Format(time.RFC3339), nodeID); err != nil {
		log.Printf("record redundancy node activity: %v", err)
	}
	return nodeID, secret, nil
}

func (m *Manager) SnapshotEnvelope(ctx context.Context, since int64, secret []byte) (*EncryptedEnvelope, int64, error) {
	state, err := m.readState(ctx)
	if err != nil {
		return nil, 0, err
	}
	if since >= state.ConfigRevision {
		return nil, state.ConfigRevision, nil
	}
	var payload []byte
	if err := m.store.DB.QueryRowContext(ctx, `
		SELECT payload FROM redundancy_snapshots WHERE revision = ?`, state.ConfigRevision).Scan(&payload); err != nil {
		return nil, 0, errors.New("controller configuration snapshot is unavailable")
	}
	envelope, err := sealEnvelope(secret, payload, "faro-configuration-snapshot-v1")
	return &envelope, state.ConfigRevision, err
}

func (m *Manager) RecordAcknowledgement(ctx context.Context, nodeID string, ack SyncAck) error {
	if ack.Revision < 0 || len(ack.Error) > 500 {
		return errors.New("replica acknowledgement is invalid")
	}
	now := m.now().UTC().Format(time.RFC3339)
	_, err := m.store.DB.ExecContext(ctx, `
		UPDATE redundancy_nodes
		SET config_revision = CASE WHEN ? = '' THEN ? ELSE config_revision END,
		    last_sync_at = CASE WHEN ? = '' THEN ? ELSE last_sync_at END,
		    last_error = ?, last_seen_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE node_id = ?`,
		ack.Error, ack.Revision, ack.Error, now, ack.Error, now, nodeID)
	return err
}

func (m *Manager) syncReplica(ctx context.Context) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()

	state, err := m.readState(ctx)
	if err != nil {
		return err
	}
	if state.Role != RoleReplica {
		return nil
	}
	nodeSecret, err := m.loadReplicaSecret(state)
	if err != nil {
		return err
	}
	response, err := m.requestReplicaSnapshot(ctx, state, nodeSecret)
	if err != nil {
		return err
	}
	defer closeRedundancyResource("close redundancy snapshot response body", response.Body)
	if response.StatusCode == http.StatusNoContent {
		return m.markReplicaSynchronized(ctx)
	}
	if response.StatusCode != http.StatusOK {
		return controllerSyncError(response)
	}
	snapshot, err := decodeReplicaSnapshot(response, nodeSecret, state)
	if err != nil {
		return err
	}
	files, err := snapshotFiles(snapshot.Files)
	if err != nil {
		return err
	}
	return m.applyReplicaSnapshot(ctx, state, nodeSecret, snapshot, files)
}

func (m *Manager) loadReplicaSecret(state localState) ([]byte, error) {
	masterKey, err := secrets.LoadOrCreateKey(m.secretKeyPath)
	if err != nil {
		return nil, err
	}
	return secrets.Decrypt(masterKey, state.SecretCiphertext)
}

func (m *Manager) requestReplicaSnapshot(ctx context.Context, state localState, secret []byte) (*http.Response, error) {
	target := state.ControllerURL + "/api/redundancy/replica/snapshot?since=" + strconv.FormatInt(state.ConfigRevision, 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(faroNodeHeader, state.NodeID)
	if err := signRequest(request, nil, secret, m.now()); err != nil {
		return nil, err
	}
	response, err := m.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("reach controller: %w", err)
	}
	return response, nil
}

func (m *Manager) markReplicaSynchronized(ctx context.Context) error {
	_, err := m.store.DB.ExecContext(ctx, `
		UPDATE redundancy_state SET last_sync_at = ?, last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = 1 AND role = ?`,
		m.now().UTC().Format(time.RFC3339), RoleReplica)
	return err
}

func controllerSyncError(response *http.Response) error {
	message, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return fmt.Errorf("controller sync failed: %s", response.Status)
	}
	return fmt.Errorf("controller sync failed: %s", cleanHTTPError(message, response.Status))
}

func decodeReplicaSnapshot(response *http.Response, secret []byte, state localState) (ConfigSnapshot, error) {
	var envelope EncryptedEnvelope
	if err := json.NewDecoder(io.LimitReader(response.Body, maxSnapshotEnvelopeBytes+1)).Decode(&envelope); err != nil {
		return ConfigSnapshot{}, errors.New("controller returned an invalid encrypted snapshot")
	}
	plaintext, err := openEnvelope(secret, envelope, "faro-configuration-snapshot-v1")
	if err != nil {
		return ConfigSnapshot{}, err
	}
	if len(plaintext) > maxSnapshotTransportBytes {
		return ConfigSnapshot{}, errors.New("controller configuration snapshot is too large")
	}
	snapshot, err := decodeSnapshot(plaintext)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	if snapshot.SchemaVersion != snapshotSchemaVersion || snapshot.HomeID != state.HomeID || snapshot.Revision <= state.ConfigRevision {
		return ConfigSnapshot{}, errors.New("controller configuration snapshot has invalid identity or revision")
	}
	return snapshot, nil
}

func snapshotFiles(snapshot map[string]string) (map[string][]byte, error) {
	files := make(map[string][]byte, len(snapshot))
	var total int
	for name, content := range snapshot {
		total += len(content)
		if total > maxSnapshotUncompressedBytes {
			return nil, errors.New("controller configuration snapshot is too large")
		}
		files[name] = []byte(content)
	}
	return files, nil
}

func (m *Manager) applyReplicaSnapshot(ctx context.Context, state localState, secret []byte, snapshot ConfigSnapshot, files map[string][]byte) error {
	if err := m.applier.ApplyReplica(ctx, files, snapshot.RuntimeSettings); err != nil {
		m.acknowledgeReplica(context.WithoutCancel(ctx), state, secret, SyncAck{Revision: snapshot.Revision, Error: truncate(err.Error(), 500)})
		return fmt.Errorf("apply controller configuration: %w", err)
	}
	now := m.now().UTC().Format(time.RFC3339)
	if _, err := m.store.DB.ExecContext(ctx, `
		UPDATE redundancy_state
		SET config_revision = ?, last_sync_at = ?, last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = 1 AND role = ?`, snapshot.Revision, now, RoleReplica); err != nil {
		return err
	}
	m.acknowledgeReplica(context.WithoutCancel(ctx), state, secret, SyncAck{Revision: snapshot.Revision})
	return nil
}

func (m *Manager) acknowledgeReplica(ctx context.Context, state localState, secret []byte, ack SyncAck) {
	if err := m.sendAcknowledgement(ctx, state, secret, ack); err != nil {
		log.Printf("send redundancy acknowledgement: %v", err)
	}
}

func (m *Manager) sendAcknowledgement(ctx context.Context, state localState, secret []byte, ack SyncAck) error {
	body, err := json.Marshal(ack)
	if err != nil {
		return fmt.Errorf("encode sync acknowledgement: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, state.ControllerURL+"/api/redundancy/replica/ack", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(faroNodeHeader, state.NodeID)
	if err := signRequest(request, body, secret, m.now()); err != nil {
		return err
	}
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer closeRedundancyResource("close redundancy acknowledgement response body", response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("controller rejected sync acknowledgement: %s", response.Status)
	}
	return nil
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func netParsePrivate(value string) []byte {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || !isPrivateAddress(ip) {
		return nil
	}
	return ip
}

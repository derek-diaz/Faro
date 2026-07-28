package redundancy

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/secrets"
)

func NewManager(store *db.Store, applier ReplicaApplier, configDir, secretKeyPath string) *Manager {
	return &Manager{
		store:         store,
		applier:       applier,
		configDir:     configDir,
		secretKeyPath: secretKeyPath,
		client: &http.Client{
			Timeout:   20 * time.Second,
			Transport: &http.Transport{Proxy: nil, DialContext: dialPrivateNetwork},
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				return errors.New("redundancy controller redirected unexpectedly")
			},
		},
		now:          time.Now,
		syncInterval: 5 * time.Second,
		pairings:     map[string]pairingSession{},
		replays:      map[string]map[string]time.Time{},
		syncNow:      make(chan struct{}, 1),
	}
}

func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.syncInterval)
	defer ticker.Stop()
	m.triggerSync()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.expirePairings()
			if state, err := m.readState(ctx); err == nil && state.Role == RoleReplica {
				if err := m.syncReplica(ctx); err != nil {
					m.recordReplicaError(context.WithoutCancel(ctx), err)
				}
			}
		case <-m.syncNow:
			if state, err := m.readState(ctx); err == nil && state.Role == RoleReplica {
				if err := m.syncReplica(ctx); err != nil {
					m.recordReplicaError(context.WithoutCancel(ctx), err)
				}
			}
		}
	}
}

func (m *Manager) ConfigurationApplied(ctx context.Context) {
	state, err := m.readState(ctx)
	if err != nil || state.Role != RoleController {
		return
	}
	if err := m.captureSnapshot(ctx, state); err != nil {
		log.Printf("capture redundancy configuration: %v", err)
	}
}

func (m *Manager) PublicStatus(ctx context.Context) (PublicStatus, error) {
	state, err := m.readState(ctx)
	if err != nil {
		return PublicStatus{}, err
	}
	return publicStatus(state), nil
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	state, err := m.readState(ctx)
	if err != nil {
		return Status{}, err
	}
	result := Status{PublicStatus: publicStatus(state), Nodes: []NodeInfo{}}
	_ = m.store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'faro_lan_ip'`).Scan(&result.LANAddress)
	if state.Role == RoleController {
		result.ControllerName = state.NodeName
		result.Nodes = append(result.Nodes, NodeInfo{
			NodeID: state.NodeID, Name: state.NodeName, LANAddress: result.LANAddress,
			Role: RoleController, Online: true, ConfigRevision: state.ConfigRevision,
			LastSeenAt: m.now().UTC().Format(time.RFC3339), LastSyncAt: m.now().UTC().Format(time.RFC3339),
		})
		rows, queryErr := m.store.DB.QueryContext(ctx, `
			SELECT node_id, name, lan_address, config_revision,
			       COALESCE(last_seen_at, ''), COALESCE(last_sync_at, ''), last_error
			FROM redundancy_nodes ORDER BY name COLLATE NOCASE`)
		if queryErr != nil {
			return Status{}, queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var node NodeInfo
			node.Role = RoleReplica
			if err := rows.Scan(&node.NodeID, &node.Name, &node.LANAddress, &node.ConfigRevision, &node.LastSeenAt, &node.LastSyncAt, &node.LastError); err != nil {
				return Status{}, err
			}
			node.Online = recentlySeen(node.LastSeenAt, m.now(), 3*m.syncInterval+5*time.Second)
			result.Nodes = append(result.Nodes, node)
		}
		if err := rows.Err(); err != nil {
			return Status{}, err
		}
		result.Healthy = len(result.Nodes) > 1
		for _, node := range result.Nodes {
			if !node.Online || node.ConfigRevision != state.ConfigRevision || node.LastError != "" {
				result.Healthy = false
			}
		}
	} else if state.Role == RoleReplica {
		result.Healthy = state.LastError == "" && recentlySeen(state.LastSyncAt, m.now(), 3*m.syncInterval+5*time.Second)
	} else {
		result.Healthy = true
	}
	return result, nil
}

func (m *Manager) StartPairing(ctx context.Context, nodeName string) (PairingCode, error) {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		nodeName = "Primary Faro"
	}
	if len([]rune(nodeName)) > 40 {
		return PairingCode{}, errors.New("server name must be 40 characters or fewer")
	}
	state, err := m.readState(ctx)
	if err != nil {
		return PairingCode{}, err
	}
	if state.Role == RoleReplica {
		return PairingCode{}, errors.New("a replica cannot add other Faro servers")
	}
	if state.Role == RoleStandalone {
		homeID, err := randomID(16)
		if err != nil {
			return PairingCode{}, err
		}
		if _, err := m.store.DB.ExecContext(ctx, `
			UPDATE redundancy_state
			SET role = ?, home_id = ?, node_name = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = 1`, RoleController, homeID, nodeName); err != nil {
			return PairingCode{}, err
		}
		state, err = m.readState(ctx)
		if err != nil {
			return PairingCode{}, err
		}
		if err := m.captureSnapshot(ctx, state); err != nil {
			return PairingCode{}, err
		}
	} else if state.NodeName != nodeName {
		if _, err := m.store.DB.ExecContext(ctx, `UPDATE redundancy_state SET node_name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`, nodeName); err != nil {
			return PairingCode{}, err
		}
	}
	id, err := randomID(8)
	if err != nil {
		return PairingCode{}, err
	}
	token, err := randomBytes(32)
	if err != nil {
		return PairingCode{}, err
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return PairingCode{}, err
	}
	expires := m.now().UTC().Add(10 * time.Minute)
	m.mu.Lock()
	m.pairings[id] = pairingSession{
		Token: token, PrivateKey: private.Bytes(), PublicKey: private.PublicKey().Bytes(), ExpiresAt: expires,
	}
	m.mu.Unlock()
	return PairingCode{Code: encodePairingCode(id, token, private.PublicKey().Bytes()), ExpiresAt: expires.Format(time.RFC3339)}, nil
}

func (m *Manager) Join(ctx context.Context, input JoinInput) (JoinResult, error) {
	state, err := m.readState(ctx)
	if err != nil {
		return JoinResult{}, err
	}
	if state.Role != RoleStandalone {
		return JoinResult{}, errors.New("this Faro server already belongs to a redundancy setup")
	}
	controllerURL, err := normalizeControllerURL(ctx, input.ControllerURL)
	if err != nil {
		return JoinResult{}, err
	}
	code, err := parsePairingCode(input.PairingCode)
	if err != nil {
		return JoinResult{}, err
	}
	nodeName := strings.TrimSpace(input.NodeName)
	if nodeName == "" || len([]rune(nodeName)) > 40 {
		return JoinResult{}, errors.New("choose a server name between 1 and 40 characters")
	}
	lanAddress := strings.TrimSpace(input.LANAddress)
	if ip := net.ParseIP(lanAddress); ip == nil || !isPrivateAddress(ip) {
		return JoinResult{}, errors.New("this server address must be a private LAN IP")
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return JoinResult{}, err
	}
	requestBody := PairRequest{
		PairingID: code.ID, NodeID: state.NodeID, NodeName: nodeName, LANAddress: lanAddress,
		PublicKey: base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()),
	}
	requestBody.Proof = base64.RawURLEncoding.EncodeToString(pairingProof(code.Token, requestBody))
	encoded, _ := json.Marshal(requestBody)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, controllerURL+"/api/redundancy/pair", bytes.NewReader(encoded))
	if err != nil {
		return JoinResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return JoinResult{}, fmt.Errorf("connect to the Faro controller: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return JoinResult{}, fmt.Errorf("controller rejected pairing: %s", cleanHTTPError(message, response.Status))
	}
	var pairResult PairResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&pairResult); err != nil {
		return JoinResult{}, errors.New("controller returned an invalid pairing response")
	}
	key, err := pairingKey(private.Bytes(), code.ControllerKey, code.Token)
	if err != nil {
		return JoinResult{}, err
	}
	plaintext, err := openEnvelope(key, encryptedEnvelope(pairResult), "faro-pairing-response-v1")
	if err != nil {
		return JoinResult{}, err
	}
	var payload pairPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return JoinResult{}, errors.New("controller returned invalid pairing details")
	}
	nodeSecret, err := base64.RawURLEncoding.DecodeString(payload.NodeSecret)
	if err != nil || len(nodeSecret) != 32 || payload.HomeID == "" {
		return JoinResult{}, errors.New("controller returned invalid pairing details")
	}
	masterKey, err := secrets.LoadOrCreateKey(m.secretKeyPath)
	if err != nil {
		return JoinResult{}, err
	}
	ciphertext, err := secrets.Encrypt(masterKey, nodeSecret)
	if err != nil {
		return JoinResult{}, err
	}
	if _, err := m.store.DB.ExecContext(ctx, `
		UPDATE redundancy_state
		SET role = ?, home_id = ?, node_name = ?, controller_url = ?, secret_ciphertext = ?,
		    config_revision = 0, last_sync_at = NULL, last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		RoleReplica, payload.HomeID, nodeName, controllerURL, ciphertext); err != nil {
		return JoinResult{}, err
	}
	if err := m.syncReplica(ctx); err != nil {
		m.recordReplicaError(context.WithoutCancel(ctx), err)
	}
	status, statusErr := m.PublicStatus(ctx)
	return JoinResult{Status: status}, statusErr
}

func (m *Manager) RemoveNode(ctx context.Context, nodeID string) error {
	state, err := m.readState(ctx)
	if err != nil {
		return err
	}
	if state.Role != RoleController {
		return errors.New("only the controller can remove replica servers")
	}
	result, err := m.store.DB.ExecContext(ctx, `DELETE FROM redundancy_nodes WHERE node_id = ?`, strings.TrimSpace(nodeID))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return errors.New("replica server does not exist")
	}
	return nil
}

func (m *Manager) captureSnapshot(ctx context.Context, state localState) error {
	files, err := readGeneratedFiles(m.configDir)
	if err != nil {
		return err
	}
	runtime := map[string]string{}
	for _, key := range []string{"upstream_dns", "upstream_transport"} {
		var value string
		if err := m.store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
			return err
		}
		runtime[key] = value
	}
	revision := state.ConfigRevision + 1
	snapshot := ConfigSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		HomeID:        state.HomeID, Revision: revision, CreatedAt: m.now().UTC().Format(time.RFC3339),
		RuntimeSettings: runtime, Files: files,
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(payload) > maxSnapshotBytes {
		return errors.New("generated redundancy snapshot is too large")
	}
	tx, err := m.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE redundancy_state
		SET config_revision = ?, last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = 1 AND role = ?`, revision, RoleController); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO redundancy_snapshots(revision, payload) VALUES(?, ?)`, revision, payload); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM redundancy_snapshots WHERE revision <> ?`, revision); err != nil {
		return err
	}
	return tx.Commit()
}

func readGeneratedFiles(configDir string) (map[string]string, error) {
	names := []string{"Corefile"}
	matches, err := filepath.Glob(filepath.Join(configDir, "*.hosts"))
	if err != nil {
		return nil, err
	}
	for _, match := range matches {
		names = append(names, filepath.Base(match))
	}
	sort.Strings(names)
	files := map[string]string{}
	var total int64
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			return nil, fmt.Errorf("read generated DNS file %s: %w", name, err)
		}
		total += int64(len(content))
		if total > maxSnapshotBytes {
			return nil, errors.New("generated DNS configuration is too large to synchronize")
		}
		files[name] = string(content)
	}
	return files, nil
}

func (m *Manager) readState(ctx context.Context) (localState, error) {
	var state localState
	err := m.store.DB.QueryRowContext(ctx, `
		SELECT role, home_id, node_id, node_name, controller_url, secret_ciphertext,
		       config_revision, COALESCE(last_sync_at, ''), last_error
		FROM redundancy_state WHERE id = 1`).Scan(
		&state.Role, &state.HomeID, &state.NodeID, &state.NodeName, &state.ControllerURL,
		&state.SecretCiphertext, &state.ConfigRevision, &state.LastSyncAt, &state.LastError)
	return state, err
}

func publicStatus(state localState) PublicStatus {
	return PublicStatus{
		Role: state.Role, HomeID: state.HomeID, NodeID: state.NodeID, NodeName: state.NodeName,
		ControllerURL: state.ControllerURL, ConfigRevision: state.ConfigRevision,
		LastSyncAt: state.LastSyncAt, LastError: state.LastError,
	}
}

func (m *Manager) triggerSync() {
	select {
	case m.syncNow <- struct{}{}:
	default:
	}
}

func (m *Manager) expirePairings() {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, pairing := range m.pairings {
		if !pairing.ExpiresAt.After(now) {
			delete(m.pairings, id)
		}
	}
	for nodeID, nonces := range m.replays {
		for nonce, seen := range nonces {
			if now.Sub(seen) > 5*time.Minute {
				delete(nonces, nonce)
			}
		}
		if len(nonces) == 0 {
			delete(m.replays, nodeID)
		}
	}
}

func recentlySeen(value string, now time.Time, maximumAge time.Duration) bool {
	seen, err := time.Parse(time.RFC3339, value)
	return err == nil && now.Sub(seen) <= maximumAge
}

func normalizeControllerURL(ctx context.Context, raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("controller address must be an http or https Faro URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("controller address cannot contain credentials, a path, query, or fragment")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return "", errors.New("controller address could not be resolved")
	}
	for _, address := range addresses {
		if !isPrivateAddress(address.IP) {
			return "", errors.New("controller must resolve only to a private network address")
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func dialPrivateNetwork(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("controller network address is invalid")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("controller address could not be resolved")
	}
	for _, resolved := range addresses {
		if !isPrivateAddress(resolved.IP) {
			return nil, errors.New("controller must resolve only to a private network address")
		}
	}
	target := net.JoinHostPort(addresses[0].IP.String(), port)
	return (&net.Dialer{}).DialContext(ctx, network, target)
}

func isPrivateAddress(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || carrierGradeNAT(ip)
}

func carrierGradeNAT(ip net.IP) bool {
	value := ip.To4()
	return value != nil && value[0] == 100 && value[1]&0xc0 == 0x40
}

func cleanHTTPError(body []byte, fallback string) string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error != "" {
		return payload.Error
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return text
	}
	return fallback
}

func (m *Manager) recordReplicaError(ctx context.Context, err error) {
	_, _ = m.store.DB.ExecContext(ctx, `
		UPDATE redundancy_state SET last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1 AND role = ?`, truncate(err.Error(), 500), RoleReplica)
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

package redundancy

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
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

func (manager *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(manager.syncInterval)
	defer ticker.Stop()
	manager.triggerSync()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			manager.expirePairings()
			if state, err := manager.readState(ctx); err == nil && state.Role == RoleReplica {
				if err := manager.syncReplica(ctx); err != nil {
					manager.recordReplicaError(context.WithoutCancel(ctx), err)
				}
			}
		case <-manager.syncNow:
			if state, err := manager.readState(ctx); err == nil && state.Role == RoleReplica {
				if err := manager.syncReplica(ctx); err != nil {
					manager.recordReplicaError(context.WithoutCancel(ctx), err)
				}
			}
		}
	}
}

func (manager *Manager) ConfigurationApplied(ctx context.Context) {
	state, err := manager.readState(ctx)
	if err != nil || state.Role != RoleController {
		return
	}
	if err := manager.captureSnapshot(ctx, state); err != nil {
		log.Printf("capture redundancy configuration: %v", err)
	}
}

func (manager *Manager) PublicStatus(ctx context.Context) (PublicStatus, error) {
	state, err := manager.readState(ctx)
	if err != nil {
		return PublicStatus{}, err
	}
	return publicStatus(state), nil
}

func (manager *Manager) Status(ctx context.Context) (Status, error) {
	state, err := manager.readState(ctx)
	if err != nil {
		return Status{}, err
	}
	result := Status{PublicStatus: publicStatus(state), Nodes: []NodeInfo{}}
	result.LANAddress = manager.lanAddress(ctx)
	switch state.Role {
	case RoleController:
		return manager.controllerStatus(ctx, state, result)
	case RoleReplica:
		result.Healthy = state.LastError == "" && recentlySeen(state.LastSyncAt, manager.now(), 3*manager.syncInterval+5*time.Second)
	default:
		result.Healthy = true
	}
	return result, nil
}

func (manager *Manager) lanAddress(ctx context.Context) string {
	var address string
	if err := manager.store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'faro_lan_ip'`).Scan(&address); err != nil {
		return ""
	}
	return address
}

func (manager *Manager) controllerStatus(ctx context.Context, state localState, result Status) (Status, error) {
	now := manager.now().UTC().Format(time.RFC3339)
	result.ControllerName = state.NodeName
	result.Nodes = append(result.Nodes, NodeInfo{
		NodeID: state.NodeID, Name: state.NodeName, LANAddress: result.LANAddress,
		Role: RoleController, Online: true, ConfigRevision: state.ConfigRevision,
		LastSeenAt: now, LastSyncAt: now,
	})
	rows, err := manager.store.DB.QueryContext(ctx, `
		SELECT node_id, name, lan_address, config_revision,
		       COALESCE(last_seen_at, ''), COALESCE(last_sync_at, ''), last_error
		FROM redundancy_nodes ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return Status{}, err
	}
	defer closeRedundancyResource("close redundancy status rows", rows)
	for rows.Next() {
		var node NodeInfo
		node.Role = RoleReplica
		if err := rows.Scan(&node.NodeID, &node.Name, &node.LANAddress, &node.ConfigRevision, &node.LastSeenAt, &node.LastSyncAt, &node.LastError); err != nil {
			return Status{}, err
		}
		node.Online = recentlySeen(node.LastSeenAt, manager.now(), 3*manager.syncInterval+5*time.Second)
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
	return result, nil
}

func (manager *Manager) StartPairing(ctx context.Context, nodeName string) (PairingCode, error) {
	nodeName, err := normalizePairingNodeName(nodeName)
	if err != nil {
		return PairingCode{}, err
	}
	state, err := manager.preparePairingState(ctx, nodeName)
	if err != nil {
		return PairingCode{}, err
	}
	if state.ConfigRevision < 1 {
		return PairingCode{}, errors.New("DNS configuration could not be prepared for synchronization")
	}
	return manager.createPairingCode()
}

func normalizePairingNodeName(nodeName string) (string, error) {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		nodeName = "Primary Faro"
	}
	if len([]rune(nodeName)) > 40 {
		return "", errors.New("server name must be 40 characters or fewer")
	}
	return nodeName, nil
}

func (manager *Manager) preparePairingState(ctx context.Context, nodeName string) (localState, error) {
	state, err := manager.readState(ctx)
	if err != nil {
		return localState{}, err
	}
	if state.Role == RoleReplica {
		return localState{}, errors.New("a replica cannot add other Faro servers")
	}
	if state.Role == RoleStandalone {
		homeID, err := randomID(16)
		if err != nil {
			return localState{}, err
		}
		if err := manager.initializeController(ctx, state, homeID, nodeName); err != nil {
			return localState{}, err
		}
		return manager.readState(ctx)
	}
	// Faro versions before compressed snapshots could leave the role set to
	// controller while revision 0 had never actually been captured. Repair
	// that state before issuing another pairing code.
	if state.ConfigRevision == 0 {
		if err := manager.captureSnapshot(ctx, state); err != nil {
			return localState{}, err
		}
		state, err = manager.readState(ctx)
		if err != nil {
			return localState{}, err
		}
	}
	if state.NodeName != nodeName {
		if _, err := manager.store.DB.ExecContext(ctx, `UPDATE redundancy_state SET node_name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`, nodeName); err != nil {
			return localState{}, err
		}
		state.NodeName = nodeName
	}
	return state, nil
}

func (manager *Manager) createPairingCode() (PairingCode, error) {
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
	expires := manager.now().UTC().Add(10 * time.Minute)
	manager.mu.Lock()
	manager.pairings[id] = pairingSession{
		Token: token, PrivateKey: private.Bytes(), PublicKey: private.PublicKey().Bytes(), ExpiresAt: expires,
	}
	manager.mu.Unlock()
	return PairingCode{Code: encodePairingCode(id, token, private.PublicKey().Bytes()), ExpiresAt: expires.Format(time.RFC3339)}, nil
}

func (manager *Manager) initializeController(ctx context.Context, state localState, homeID, nodeName string) error {
	state.Role = RoleController
	state.HomeID = homeID
	state.NodeName = nodeName
	state.ConfigRevision = 0
	revision := int64(1)
	payload, err := manager.buildSnapshot(ctx, state, revision)
	if err != nil {
		return err
	}

	tx, err := manager.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackRedundancyTransaction(tx)
	result, err := tx.ExecContext(ctx, `
		UPDATE redundancy_state
		SET role = ?, home_id = ?, node_name = ?, config_revision = ?,
		    last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = 1 AND role = ?`,
		RoleController, homeID, nodeName, revision, RoleStandalone)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("redundancy mode changed while DNS configuration was being prepared")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM redundancy_snapshots WHERE revision IS NOT NULL`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO redundancy_snapshots(revision, payload) VALUES(?, ?)`, revision, payload); err != nil {
		return err
	}
	return tx.Commit()
}

func (manager *Manager) buildSnapshot(ctx context.Context, state localState, revision int64) ([]byte, error) {
	files, err := readGeneratedFiles(manager.configDir)
	if err != nil {
		return nil, err
	}
	runtime := map[string]string{}
	for _, key := range []string{"upstream_dns", "upstream_transport"} {
		var value string
		if err := manager.store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
			return nil, err
		}
		runtime[key] = value
	}
	snapshot := ConfigSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		HomeID:        state.HomeID, Revision: revision, CreatedAt: manager.now().UTC().Format(time.RFC3339),
		RuntimeSettings: runtime, Files: files,
	}
	return encodeSnapshot(snapshot, maxSnapshotTransportBytes)
}

func (manager *Manager) captureSnapshot(ctx context.Context, state localState) error {
	revision := state.ConfigRevision + 1
	payload, err := manager.buildSnapshot(ctx, state, revision)
	if err != nil {
		return err
	}
	tx, err := manager.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackRedundancyTransaction(tx)
	result, err := tx.ExecContext(ctx, `
		UPDATE redundancy_state
		SET config_revision = ?, last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = 1 AND role = ? AND config_revision = ?`, revision, RoleController, state.ConfigRevision)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("redundancy configuration changed while the snapshot was being prepared")
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
	corefile, err := os.ReadFile(filepath.Join(configDir, "Corefile"))
	if err != nil {
		return nil, fmt.Errorf("read generated DNS file Corefile: %w", err)
	}
	files := map[string]string{"Corefile": string(corefile)}
	names := map[string]struct{}{}
	for _, line := range strings.Split(string(corefile), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "hosts" || !strings.HasPrefix(fields[1], "/etc/coredns/") {
			continue
		}
		name := strings.TrimPrefix(fields[1], "/etc/coredns/")
		if filepath.Base(name) != name || !strings.HasSuffix(name, ".hosts") {
			return nil, fmt.Errorf("generated Corefile contains unsafe hosts file %q", name)
		}
		names[name] = struct{}{}
	}
	if len(names) == 0 {
		return nil, errors.New("generated Corefile has no Faro hosts files")
	}
	sortedNames := make([]string, 0, len(names))
	for name := range names {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)
	total := int64(len(corefile))
	for _, name := range sortedNames {
		content, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			return nil, fmt.Errorf("read generated DNS file %s: %w", name, err)
		}
		total += int64(len(content))
		if total > maxSnapshotUncompressedBytes {
			return nil, errors.New("generated DNS configuration is too large to synchronize")
		}
		files[name] = string(content)
	}
	return files, nil
}

func (manager *Manager) Join(ctx context.Context, input JoinInput) (JoinResult, error) {
	state, err := manager.readState(ctx)
	if err != nil {
		return JoinResult{}, err
	}
	if state.Role != RoleStandalone {
		return JoinResult{}, errors.New("this Faro server already belongs to a redundancy setup")
	}
	parameters, err := prepareJoinParameters(ctx, input, state)
	if err != nil {
		return JoinResult{}, err
	}
	pairResult, err := manager.requestPairing(ctx, parameters)
	if err != nil {
		return JoinResult{}, err
	}
	payload, nodeSecret, err := decodePairingResponse(pairResult, parameters.privateKey, parameters.code)
	if err != nil {
		return JoinResult{}, err
	}
	if err := manager.persistReplicaState(ctx, parameters, payload, nodeSecret); err != nil {
		return JoinResult{}, err
	}
	if err := manager.syncReplica(ctx); err != nil {
		manager.recordReplicaError(context.WithoutCancel(ctx), err)
	}
	status, statusErr := manager.PublicStatus(ctx)
	return JoinResult{Status: status}, statusErr
}

type joinParameters struct {
	controllerURL string
	code          parsedPairingCode
	nodeName      string
	privateKey    *ecdh.PrivateKey
	request       PairRequest
}

func prepareJoinParameters(ctx context.Context, input JoinInput, state localState) (joinParameters, error) {
	controllerURL, err := normalizeControllerURL(ctx, input.ControllerURL)
	if err != nil {
		return joinParameters{}, err
	}
	code, err := parsePairingCode(input.PairingCode)
	if err != nil {
		return joinParameters{}, err
	}
	nodeName := strings.TrimSpace(input.NodeName)
	if nodeName == "" || len([]rune(nodeName)) > 40 {
		return joinParameters{}, errors.New("choose a server name between 1 and 40 characters")
	}
	lanAddress := strings.TrimSpace(input.LANAddress)
	if ip := net.ParseIP(lanAddress); ip == nil || !isPrivateAddress(ip) {
		return joinParameters{}, errors.New("this server address must be a private LAN IP")
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return joinParameters{}, err
	}
	request := PairRequest{
		PairingID: code.ID, NodeID: state.NodeID, NodeName: nodeName, LANAddress: lanAddress,
		PublicKey: base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()),
	}
	request.Proof = base64.RawURLEncoding.EncodeToString(pairingProof(code.Token, request))
	return joinParameters{controllerURL: controllerURL, code: code, nodeName: nodeName, privateKey: private, request: request}, nil
}

func (manager *Manager) requestPairing(ctx context.Context, parameters joinParameters) (PairResponse, error) {
	encoded, err := json.Marshal(parameters.request)
	if err != nil {
		return PairResponse{}, fmt.Errorf("encode pairing request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parameters.controllerURL+"/api/redundancy/pair", bytes.NewReader(encoded))
	if err != nil {
		return PairResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := manager.client.Do(request)
	if err != nil {
		return PairResponse{}, fmt.Errorf("connect to the Faro controller: %w", err)
	}
	defer closeRedundancyResource("close pairing response body", response.Body)
	if response.StatusCode != http.StatusOK {
		message, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return PairResponse{}, fmt.Errorf("controller rejected pairing: %s", response.Status)
		}
		return PairResponse{}, fmt.Errorf("controller rejected pairing: %s", cleanHTTPError(message, response.Status))
	}
	var pairResult PairResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&pairResult); err != nil {
		return PairResponse{}, errors.New("controller returned an invalid pairing response")
	}
	return pairResult, nil
}

func decodePairingResponse(pairResult PairResponse, private *ecdh.PrivateKey, code parsedPairingCode) (pairPayload, []byte, error) {
	key, err := pairingKey(private.Bytes(), code.ControllerKey, code.Token)
	if err != nil {
		return pairPayload{}, nil, err
	}
	plaintext, err := openEnvelope(key, EncryptedEnvelope(pairResult), "faro-pairing-response-v1")
	if err != nil {
		return pairPayload{}, nil, err
	}
	var payload pairPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return pairPayload{}, nil, errors.New("controller returned invalid pairing details")
	}
	nodeSecret, err := base64.RawURLEncoding.DecodeString(payload.NodeSecret)
	if err != nil || len(nodeSecret) != 32 || payload.HomeID == "" {
		return pairPayload{}, nil, errors.New("controller returned invalid pairing details")
	}
	return payload, nodeSecret, nil
}

func (manager *Manager) persistReplicaState(ctx context.Context, parameters joinParameters, payload pairPayload, nodeSecret []byte) error {
	masterKey, err := secrets.LoadOrCreateKey(manager.secretKeyPath)
	if err != nil {
		return err
	}
	ciphertext, err := secrets.Encrypt(masterKey, nodeSecret)
	if err != nil {
		return err
	}
	_, err = manager.store.DB.ExecContext(ctx, `
		UPDATE redundancy_state
		SET role = ?, home_id = ?, node_name = ?, controller_url = ?, secret_ciphertext = ?,
		    config_revision = 0, last_sync_at = NULL, last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		RoleReplica, payload.HomeID, parameters.nodeName, parameters.controllerURL, ciphertext)
	return err
}

func (manager *Manager) RemoveNode(ctx context.Context, nodeID string) error {
	state, err := manager.readState(ctx)
	if err != nil {
		return err
	}
	if state.Role != RoleController {
		return errors.New("only the controller can remove replica servers")
	}
	result, err := manager.store.DB.ExecContext(ctx, `DELETE FROM redundancy_nodes WHERE node_id = ?`, strings.TrimSpace(nodeID))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return errors.New("replica server does not exist")
	}
	return nil
}

// Leave returns this installation to standalone mode. A replica first renders
// and validates its own database-backed DNS configuration so the UI and the
// running DNS engine cannot be left describing different configurations.
func (manager *Manager) Leave(ctx context.Context) (PublicStatus, error) {
	manager.operationMu.Lock()
	defer manager.operationMu.Unlock()

	previous, err := manager.readState(ctx)
	if err != nil {
		return PublicStatus{}, err
	}
	if previous.Role == RoleStandalone {
		return PublicStatus{}, errors.New("this Faro server is not using redundancy")
	}
	if previous.Role == RoleReplica && manager.applier == nil {
		return PublicStatus{}, errors.New("DNS configuration manager is unavailable")
	}

	if err := manager.setStandalone(ctx); err != nil {
		return PublicStatus{}, err
	}
	if previous.Role == RoleReplica {
		if err := manager.applier.Apply(ctx); err != nil {
			if restoreErr := manager.restoreState(context.WithoutCancel(ctx), previous); restoreErr != nil {
				return PublicStatus{}, fmt.Errorf("restore standalone DNS configuration: %v; restore redundancy state: %w", err, restoreErr)
			}
			return PublicStatus{}, fmt.Errorf("restore standalone DNS configuration: %w", err)
		}
	}

	manager.mu.Lock()
	manager.pairings = map[string]pairingSession{}
	manager.replays = map[string]map[string]time.Time{}
	manager.mu.Unlock()
	state, err := manager.readState(ctx)
	if err != nil {
		return PublicStatus{}, err
	}
	return publicStatus(state), nil
}

func (manager *Manager) setStandalone(ctx context.Context) error {
	tx, err := manager.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackRedundancyTransaction(tx)
	if _, err := tx.ExecContext(ctx, `
		UPDATE redundancy_state
		SET role = ?, home_id = '', controller_url = '', secret_ciphertext = '',
		    config_revision = 0, last_sync_at = NULL, last_error = '',
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`, RoleStandalone); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM redundancy_nodes WHERE node_id IS NOT NULL`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM redundancy_snapshots WHERE revision IS NOT NULL`); err != nil {
		return err
	}
	return tx.Commit()
}

func (manager *Manager) restoreState(ctx context.Context, state localState) error {
	_, err := manager.store.DB.ExecContext(ctx, `
		UPDATE redundancy_state
		SET role = ?, home_id = ?, node_id = ?, node_name = ?, controller_url = ?,
		    secret_ciphertext = ?, config_revision = ?,
		    last_sync_at = NULLIF(?, ''), last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		state.Role, state.HomeID, state.NodeID, state.NodeName, state.ControllerURL,
		state.SecretCiphertext, state.ConfigRevision, state.LastSyncAt, state.LastError)
	return err
}

func (manager *Manager) readState(ctx context.Context) (localState, error) {
	var state localState
	err := manager.store.DB.QueryRowContext(ctx, `
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

func (manager *Manager) triggerSync() {
	select {
	case manager.syncNow <- struct{}{}:
	default:
	}
}

func (manager *Manager) expirePairings() {
	now := manager.now()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for id, pairing := range manager.pairings {
		if !pairing.ExpiresAt.After(now) {
			delete(manager.pairings, id)
		}
	}
	for nodeID, nonces := range manager.replays {
		for nonce, seen := range nonces {
			if now.Sub(seen) > 5*time.Minute {
				delete(nonces, nonce)
			}
		}
		if len(nonces) == 0 {
			delete(manager.replays, nodeID)
		}
	}
}

func recentlySeen(value string, now time.Time, maximumAge time.Duration) bool {
	seen, err := time.Parse(time.RFC3339, value)
	return err == nil && now.Sub(seen) <= maximumAge
}

func normalizeControllerURL(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("controller address must be an http or https Faro URL")
	}
	// The join screen accepts a LAN hostname or IP for convenience. Keep the
	// API compatible with that input too, including requests from older UI
	// bundles that did not add the scheme and Faro port in the browser.
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("controller address must be an http or https Faro URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("controller address cannot contain credentials, a path, query, or fragment")
	}
	if parsed.Hostname() == "" {
		return "", errors.New("controller address must include a hostname or IP address")
	}
	if parsed.Port() == "" {
		parsed.Host = net.JoinHostPort(parsed.Hostname(), defaultControllerPort)
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

func (manager *Manager) recordReplicaError(ctx context.Context, err error) {
	if _, updateErr := manager.store.DB.ExecContext(ctx, `
		UPDATE redundancy_state SET last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1 AND role = ?`, truncate(err.Error(), 500), RoleReplica); updateErr != nil {
		log.Printf("record redundancy replica error: %v", updateErr)
	}
}

func closeRedundancyResource(operation string, resource io.Closer) {
	if err := resource.Close(); err != nil {
		log.Printf("%s: %v", operation, err)
	}
}

func rollbackRedundancyTransaction(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.Printf("rollback redundancy transaction: %v", err)
	}
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

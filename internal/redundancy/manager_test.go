package redundancy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/secrets"
)

type recordingApplier struct {
	mu       sync.Mutex
	files    map[string][]byte
	settings map[string]string
	err      error
	localErr error
	local    int
}

func (applier *recordingApplier) Apply(_ context.Context) error {
	applier.mu.Lock()
	defer applier.mu.Unlock()
	applier.local++
	return applier.localErr
}

func (applier *recordingApplier) ApplyReplica(_ context.Context, files map[string][]byte, settings map[string]string) error {
	applier.mu.Lock()
	defer applier.mu.Unlock()
	if applier.err != nil {
		return applier.err
	}
	applier.files = files
	applier.settings = settings
	return nil
}

func TestReplicaCanLeaveAndRestoreStandaloneDNS(t *testing.T) {
	ctx := context.Background()
	store := openRedundancyStore(t)
	if _, err := store.DB.Exec(`
		UPDATE redundancy_state
		SET role = ?, home_id = ?, node_name = ?, controller_url = ?,
		    secret_ciphertext = ?, config_revision = ?, last_sync_at = ?, last_error = ?
		WHERE id = 1`,
		RoleReplica, "home-1", "Backup Faro", "http://192.168.1.20:1787",
		"encrypted-secret", 7, "2026-07-29T12:00:00Z", "temporary error"); err != nil {
		t.Fatal(err)
	}
	applier := &recordingApplier{}
	manager := NewManager(store, applier, t.TempDir(), filepath.Join(t.TempDir(), "key"))

	status, err := manager.Leave(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Role != RoleStandalone || status.HomeID != "" || status.ControllerURL != "" || status.ConfigRevision != 0 {
		t.Fatalf("unexpected standalone status: %#v", status)
	}
	if applier.local != 1 {
		t.Fatalf("standalone DNS apply count = %d, want 1", applier.local)
	}
	var secret, lastSync, lastError string
	if err := store.DB.QueryRow(`
		SELECT secret_ciphertext, COALESCE(last_sync_at, ''), last_error
		FROM redundancy_state WHERE id = 1`).Scan(&secret, &lastSync, &lastError); err != nil {
		t.Fatal(err)
	}
	if secret != "" || lastSync != "" || lastError != "" {
		t.Fatalf("replica credentials or status were retained: secret=%q last_sync=%q error=%q", secret, lastSync, lastError)
	}
}

func TestReplicaLeaveRollsBackMembershipWhenStandaloneDNSFails(t *testing.T) {
	ctx := context.Background()
	store := openRedundancyStore(t)
	if _, err := store.DB.Exec(`
		UPDATE redundancy_state
		SET role = ?, home_id = ?, node_name = ?, controller_url = ?,
		    secret_ciphertext = ?, config_revision = ?, last_sync_at = ?
		WHERE id = 1`,
		RoleReplica, "home-2", "Backup Faro", "http://192.168.1.20:1787",
		"encrypted-secret", 4, "2026-07-29T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	applier := &recordingApplier{localErr: errors.New("invalid local DNS configuration")}
	manager := NewManager(store, applier, t.TempDir(), filepath.Join(t.TempDir(), "key"))

	if _, err := manager.Leave(ctx); err == nil || !strings.Contains(err.Error(), "invalid local DNS configuration") {
		t.Fatalf("leave error = %v", err)
	}
	state, err := manager.readState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Role != RoleReplica || state.HomeID != "home-2" || state.ConfigRevision != 4 || state.SecretCiphertext != "encrypted-secret" {
		t.Fatalf("replica state was not restored: %#v", state)
	}
}

func TestControllerCanTurnOffRedundancy(t *testing.T) {
	ctx := context.Background()
	store := openRedundancyStore(t)
	if _, err := store.DB.Exec(`UPDATE redundancy_state SET role = ?, home_id = ?, config_revision = ? WHERE id = 1`, RoleController, "home-3", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO redundancy_nodes(node_id, name, secret_ciphertext)
		VALUES('0123456789abcdef0123456789abcdef', 'Backup Faro', 'secret')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO redundancy_snapshots(revision, payload) VALUES(2, 'snapshot')`); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, nil, t.TempDir(), filepath.Join(t.TempDir(), "key"))

	status, err := manager.Leave(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Role != RoleStandalone {
		t.Fatalf("role = %q, want standalone", status.Role)
	}
	for _, table := range []string{"redundancy_nodes", "redundancy_snapshots"} {
		var count int
		if err := store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}
}

func TestControllerPairsAndSynchronizesReplica(t *testing.T) {
	ctx := context.Background()
	controllerStore := openRedundancyStore(t)
	replicaStore := openRedundancyStore(t)
	controllerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(controllerDir, "Corefile"), []byte(".:53 {\n  hosts /etc/coredns/local.hosts\n  forward . 1.1.1.1\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controllerDir, "local.hosts"), []byte("192.168.1.1 router.home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller := NewManager(controllerStore, nil, controllerDir, filepath.Join(t.TempDir(), "controller.key"))
	code, err := controller.StartPairing(ctx, "Main Faro")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(controllerProtocolHandler(controller))
	defer server.Close()
	applier := &recordingApplier{}
	replica := NewManager(replicaStore, applier, t.TempDir(), filepath.Join(t.TempDir(), "replica.key"))
	result, err := replica.Join(ctx, JoinInput{
		ControllerURL: strings.TrimPrefix(server.URL, "http://"),
		PairingCode:   code.Code,
		NodeName:      "Utility room Faro",
		LANAddress:    "192.168.1.21",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Role != RoleReplica || result.Status.ConfigRevision != 1 {
		t.Fatalf("unexpected replica status: %#v", result.Status)
	}
	if string(applier.files["local.hosts"]) != "192.168.1.1 router.home\n" {
		t.Fatalf("replica files were not synchronized: %#v", applier.files)
	}
	if applier.settings["upstream_dns"] != "1.1.1.1,9.9.9.9" {
		t.Fatalf("runtime settings were not synchronized: %#v", applier.settings)
	}

	status, err := controller.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Nodes) != 2 || status.Nodes[1].ConfigRevision != 1 || status.Nodes[1].LastError != "" {
		t.Fatalf("controller did not record replica acknowledgement: %#v", status.Nodes)
	}

	var storedSecret string
	if err := controllerStore.DB.QueryRow(`SELECT secret_ciphertext FROM redundancy_nodes WHERE node_id = ?`, result.Status.NodeID).Scan(&storedSecret); err != nil {
		t.Fatal(err)
	}
	if storedSecret == "" || len(storedSecret) == 32 {
		t.Fatalf("replica secret was not stored as encrypted ciphertext: %q", storedSecret)
	}

	if err := os.WriteFile(filepath.Join(controllerDir, "local.hosts"), []byte("192.168.1.2 router.home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller.ConfigurationApplied(ctx)
	applier.err = errors.New("CoreDNS rejected staged configuration")
	if err := replica.syncReplica(ctx); err == nil {
		t.Fatal("replica accepted a configuration its DNS engine rejected")
	}
	replicaStatus, err := replica.PublicStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replicaStatus.ConfigRevision != 1 || string(applier.files["local.hosts"]) != "192.168.1.1 router.home\n" {
		t.Fatalf("replica abandoned its last-known-good configuration: %#v", replicaStatus)
	}
	controllerStatus, err := controller.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if controllerStatus.Nodes[1].LastError == "" {
		t.Fatal("controller did not surface the replica apply failure")
	}
}

func TestFailedInitialSnapshotDoesNotChangeRedundancyRole(t *testing.T) {
	ctx := context.Background()
	store := openRedundancyStore(t)
	configDir := t.TempDir()
	manager := NewManager(store, nil, configDir, filepath.Join(t.TempDir(), "key"))

	if _, err := manager.StartPairing(ctx, "Main Faro"); err == nil {
		t.Fatal("pairing started without a generated CoreDNS configuration")
	}
	status, err := manager.PublicStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Role != RoleStandalone || status.ConfigRevision != 0 {
		t.Fatalf("failed initialization left partial controller state: %#v", status)
	}

	writeRedundancyFiles(t, configDir)
	if _, err := manager.StartPairing(ctx, "Main Faro"); err != nil {
		t.Fatal(err)
	}
	status, err = manager.PublicStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Role != RoleController || status.ConfigRevision != 1 {
		t.Fatalf("controller was not initialized atomically: %#v", status)
	}
}

func TestStartPairingRepairsLegacyRevisionZeroController(t *testing.T) {
	ctx := context.Background()
	store := openRedundancyStore(t)
	configDir := t.TempDir()
	writeRedundancyFiles(t, configDir)
	if _, err := store.DB.Exec(`
		UPDATE redundancy_state
		SET role = ?, home_id = 'legacy-home', config_revision = 0
		WHERE id = 1`, RoleController); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, nil, configDir, filepath.Join(t.TempDir(), "key"))

	if _, err := manager.StartPairing(ctx, "Main Faro"); err != nil {
		t.Fatal(err)
	}
	status, err := manager.PublicStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigRevision != 1 {
		t.Fatalf("legacy controller was not repaired: %#v", status)
	}
	var snapshots int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM redundancy_snapshots WHERE revision = 1`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 {
		t.Fatalf("expected one repaired snapshot, got %d", snapshots)
	}
}

func TestReadGeneratedFilesOnlyIncludesCorefileReferences(t *testing.T) {
	configDir := t.TempDir()
	corefile := ".:53 {\n  hosts /etc/coredns/protection-1.hosts\n  forward . 1.1.1.1\n}\n"
	for name, content := range map[string]string{
		"Corefile":           corefile,
		"protection-1.hosts": "0.0.0.0 active.example\n",
		"protection-2.hosts": "0.0.0.0 inactive.example\n",
		"faro.hosts":         strings.Repeat("0.0.0.0 duplicate.example\n", 100),
		"blocklist.hosts":    strings.Repeat("0.0.0.0 duplicate.example\n", 100),
		"local.hosts":        "192.168.1.1 router.home\n",
	} {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := readGeneratedFiles(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files["protection-1.hosts"] == "" || files["Corefile"] != corefile {
		t.Fatalf("unexpected synchronized files: %#v", files)
	}
	for _, omitted := range []string{"protection-2.hosts", "faro.hosts", "blocklist.hosts", "local.hosts"} {
		if _, exists := files[omitted]; exists {
			t.Fatalf("unused generated file %q was included", omitted)
		}
	}
}

func TestSnapshotCompressionRoundTrip(t *testing.T) {
	snapshot := ConfigSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		HomeID:        "home",
		Revision:      1,
		Files: map[string]string{
			"Corefile":           ".:53 {\n  hosts /etc/coredns/protection-1.hosts\n  forward . 1.1.1.1\n}\n",
			"protection-1.hosts": strings.Repeat("0.0.0.0 advertising.example\n", 200),
		},
		RuntimeSettings: map[string]string{"upstream_dns": "1.1.1.1", "upstream_transport": "standard"},
	}
	payload, err := encodeSnapshot(snapshot, 512)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(payload), string(gzipHeader)) {
		t.Fatal("oversized snapshot was not compressed")
	}
	decoded, err := decodeSnapshot(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.HomeID != snapshot.HomeID || decoded.Revision != snapshot.Revision ||
		decoded.Files["protection-1.hosts"] != snapshot.Files["protection-1.hosts"] {
		t.Fatalf("compressed snapshot changed during round trip: %#v", decoded)
	}
}

func writeRedundancyFiles(t *testing.T, configDir string) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(configDir, "Corefile"),
		[]byte(".:53 {\n  hosts /etc/coredns/protection-1.hosts\n  forward . 1.1.1.1\n}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "protection-1.hosts"), []byte("0.0.0.0 ads.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticatedNodeRequestRejectsReplay(t *testing.T) {
	ctx := context.Background()
	store := openRedundancyStore(t)
	manager := NewManager(store, nil, t.TempDir(), filepath.Join(t.TempDir(), "key"))
	secret := make([]byte, 32)
	for index := range secret {
		secret[index] = byte(index + 1)
	}
	masterKey, err := secrets.LoadOrCreateKey(manager.secretKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := secrets.Encrypt(masterKey, secret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB.Exec(`UPDATE redundancy_state SET role = ?, home_id = 'home' WHERE id = 1`, RoleController)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB.Exec(`INSERT INTO redundancy_nodes(node_id, name, secret_ciphertext) VALUES(?, 'Replica', ?)`, "0123456789abcdef0123456789abcdef", ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/redundancy/replica/snapshot?since=0", nil)
	request.Header.Set("X-Faro-Node", "0123456789abcdef0123456789abcdef")
	if err := signRequest(request, nil, secret, manager.now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AuthenticateNodeRequest(ctx, request, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AuthenticateNodeRequest(ctx, request, nil); err == nil {
		t.Fatal("authenticated request nonce was accepted twice")
	}
}

func openRedundancyStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func controllerProtocolHandler(manager *Manager) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/redundancy/pair":
			var input PairRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				http.Error(responseWriter, err.Error(), http.StatusBadRequest)
				return
			}
			result, err := manager.AcceptPair(request.Context(), input)
			writeTestJSON(responseWriter, result, err)
		case "/api/redundancy/replica/snapshot":
			_, secret, err := manager.AuthenticateNodeRequest(request.Context(), request, nil)
			if err != nil {
				http.Error(responseWriter, err.Error(), http.StatusUnauthorized)
				return
			}
			since, err := strconv.ParseInt(request.URL.Query().Get("since"), 10, 64)
			if err != nil {
				http.Error(responseWriter, err.Error(), http.StatusBadRequest)
				return
			}
			envelope, _, err := manager.SnapshotEnvelope(request.Context(), since, secret)
			if err != nil {
				http.Error(responseWriter, err.Error(), http.StatusInternalServerError)
				return
			}
			if envelope == nil {
				responseWriter.WriteHeader(http.StatusNoContent)
				return
			}
			writeTestJSON(responseWriter, envelope, nil)
		case "/api/redundancy/replica/ack":
			var ack SyncAck
			var payload json.RawMessage
			decoder := json.NewDecoder(request.Body)
			if err := decoder.Decode(&payload); err != nil {
				http.Error(responseWriter, err.Error(), http.StatusBadRequest)
				return
			}
			nodeID, _, err := manager.AuthenticateNodeRequest(request.Context(), request, payload)
			if err != nil {
				http.Error(responseWriter, err.Error(), http.StatusUnauthorized)
				return
			}
			if err := json.Unmarshal(payload, &ack); err != nil {
				http.Error(responseWriter, err.Error(), http.StatusBadRequest)
				return
			}
			if err := manager.RecordAcknowledgement(request.Context(), nodeID, ack); err != nil {
				http.Error(responseWriter, err.Error(), http.StatusInternalServerError)
				return
			}
			writeTestJSON(responseWriter, map[string]bool{"ok": true}, nil)
		default:
			http.NotFound(responseWriter, request)
		}
	})
}

func writeTestJSON(responseWriter http.ResponseWriter, payload any, err error) {
	if err != nil {
		http.Error(responseWriter, err.Error(), http.StatusBadRequest)
		return
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(payload)
}

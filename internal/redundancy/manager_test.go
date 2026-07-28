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
}

func (a *recordingApplier) ApplyReplica(_ context.Context, files map[string][]byte, settings map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.files = files
	a.settings = settings
	return nil
}

func TestControllerPairsAndSynchronizesReplica(t *testing.T) {
	ctx := context.Background()
	controllerStore := openRedundancyStore(t)
	replicaStore := openRedundancyStore(t)
	controllerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(controllerDir, "Corefile"), []byte(".:53 {\n  forward . 1.1.1.1\n}\n"), 0o600); err != nil {
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
		ControllerURL: server.URL,
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
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/redundancy/pair":
			var input PairRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			result, err := manager.AcceptPair(request.Context(), input)
			writeTestJSON(w, result, err)
		case "/api/redundancy/replica/snapshot":
			_, secret, err := manager.AuthenticateNodeRequest(request.Context(), request, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			since, err := strconv.ParseInt(request.URL.Query().Get("since"), 10, 64)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			envelope, _, err := manager.SnapshotEnvelope(request.Context(), since, secret)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if envelope == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeTestJSON(w, envelope, nil)
		case "/api/redundancy/replica/ack":
			var ack SyncAck
			var payload json.RawMessage
			decoder := json.NewDecoder(request.Body)
			if err := decoder.Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			nodeID, _, err := manager.AuthenticateNodeRequest(request.Context(), request, payload)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			if err := json.Unmarshal(payload, &ack); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := manager.RecordAcknowledgement(request.Context(), nodeID, ack); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeTestJSON(w, map[string]bool{"ok": true}, nil)
		default:
			http.NotFound(w, request)
		}
	})
}

func writeTestJSON(w http.ResponseWriter, payload any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

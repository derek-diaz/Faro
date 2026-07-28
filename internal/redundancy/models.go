package redundancy

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/derek/faro/internal/db"
)

const (
	RoleStandalone = "standalone"
	RoleController = "controller"
	RoleReplica    = "replica"

	snapshotSchemaVersion = 1
	maxSnapshotBytes      = 128 << 20
)

type ReplicaApplier interface {
	ApplyReplica(context.Context, map[string][]byte, map[string]string) error
}

type Manager struct {
	store         *db.Store
	applier       ReplicaApplier
	configDir     string
	secretKeyPath string
	client        *http.Client
	now           func() time.Time
	syncInterval  time.Duration

	mu       sync.Mutex
	pairings map[string]pairingSession
	replays  map[string]map[string]time.Time
	syncNow  chan struct{}
}

type localState struct {
	Role             string
	HomeID           string
	NodeID           string
	NodeName         string
	ControllerURL    string
	SecretCiphertext string
	ConfigRevision   int64
	LastSyncAt       string
	LastError        string
}

type PublicStatus struct {
	Role           string `json:"role"`
	HomeID         string `json:"home_id,omitempty"`
	NodeID         string `json:"node_id"`
	NodeName       string `json:"node_name"`
	ControllerURL  string `json:"controller_url,omitempty"`
	ConfigRevision int64  `json:"config_revision"`
	LastSyncAt     string `json:"last_sync_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type Status struct {
	PublicStatus
	Healthy        bool       `json:"healthy"`
	ControllerName string     `json:"controller_name,omitempty"`
	LANAddress     string     `json:"lan_address,omitempty"`
	Nodes          []NodeInfo `json:"nodes"`
}

type NodeInfo struct {
	NodeID         string `json:"node_id"`
	Name           string `json:"name"`
	LANAddress     string `json:"lan_address,omitempty"`
	Role           string `json:"role"`
	Online         bool   `json:"online"`
	ConfigRevision int64  `json:"config_revision"`
	LastSeenAt     string `json:"last_seen_at,omitempty"`
	LastSyncAt     string `json:"last_sync_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type PairingCode struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
}

type JoinInput struct {
	ControllerURL string `json:"controller_url"`
	PairingCode   string `json:"pairing_code"`
	NodeName      string `json:"node_name"`
	LANAddress    string `json:"lan_address"`
}

type JoinResult struct {
	Status PublicStatus `json:"status"`
}

type ConfigSnapshot struct {
	SchemaVersion   int               `json:"schema_version"`
	HomeID          string            `json:"home_id"`
	Revision        int64             `json:"revision"`
	CreatedAt       string            `json:"created_at"`
	RuntimeSettings map[string]string `json:"runtime_settings"`
	Files           map[string]string `json:"files"`
}

type pairingSession struct {
	Token      []byte
	PrivateKey []byte
	PublicKey  []byte
	ExpiresAt  time.Time
}

type PairRequest struct {
	PairingID  string `json:"pairing_id"`
	NodeID     string `json:"node_id"`
	NodeName   string `json:"node_name"`
	LANAddress string `json:"lan_address"`
	PublicKey  string `json:"public_key"`
	Proof      string `json:"proof"`
}

type PairResponse struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type pairPayload struct {
	HomeID           string `json:"home_id"`
	ControllerNodeID string `json:"controller_node_id"`
	NodeSecret       string `json:"node_secret"`
	ConfigRevision   int64  `json:"config_revision"`
}

type encryptedEnvelope struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type SyncAck struct {
	Revision int64  `json:"revision"`
	Error    string `json:"error,omitempty"`
}

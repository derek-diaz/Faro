package unifi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
)

const (
	integrationKind = "unifi"
	syncInterval    = time.Minute
)

type Manager struct {
	store   *db.Store
	keyPath string
	syncMu  sync.Mutex
}

type TestInput struct {
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	TLSFingerprint string `json:"tls_fingerprint"`
}

type TestResult struct {
	OK                       bool         `json:"ok"`
	Sites                    []Site       `json:"sites"`
	RequiresCertificateTrust bool         `json:"requires_certificate_trust"`
	Certificate              *Certificate `json:"certificate,omitempty"`
}

type ConfigureInput struct {
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	SiteID         string `json:"site_id"`
	TLSFingerprint string `json:"tls_fingerprint"`
}

type Status struct {
	Configured       bool   `json:"configured"`
	Enabled          bool   `json:"enabled"`
	BaseURL          string `json:"base_url"`
	SiteID           string `json:"site_id"`
	SiteName         string `json:"site_name"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	TLSMode          string `json:"tls_mode"`
	TLSFingerprint   string `json:"tls_fingerprint,omitempty"`
	LastSyncAt       string `json:"last_sync_at,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	SyncedDevices    int    `json:"synced_devices"`
}

type SyncResult struct {
	SyncedDevices int    `json:"synced_devices"`
	Skipped       int    `json:"skipped"`
	CompletedAt   string `json:"completed_at"`
}

type storedConfig struct {
	enabled          bool
	baseURL          string
	secretCiphertext string
	siteID           string
	siteName         string
	tlsFingerprint   string
	lastSyncAt       string
	lastError        string
	syncedDevices    int
}

func NewManager(store *db.Store, keyPath string) *Manager {
	return &Manager{store: store, keyPath: keyPath}
}

func (m *Manager) Run(ctx context.Context) {
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		if _, err := m.Sync(ctx); err != nil && !errors.Is(err, ErrNotConfigured) {
			log.Printf("UniFi device sync failed: %v", err)
		}
	}
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.Sync(ctx); err != nil && !errors.Is(err, ErrNotConfigured) {
				log.Printf("UniFi device sync failed: %v", err)
			}
		}
	}
}

var ErrNotConfigured = errors.New("UniFi is not connected")
var errUnusableClient = errors.New("UniFi client has no stable usable network identity")

func (m *Manager) Test(ctx context.Context, input TestInput) (TestResult, error) {
	client, err := newAPIClient(input.BaseURL, input.APIKey, input.TLSFingerprint)
	if err != nil {
		return TestResult{}, err
	}
	sites, err := client.listSites(ctx)
	if err == nil {
		return TestResult{OK: true, Sites: sites}, nil
	}
	if strings.TrimSpace(input.TLSFingerprint) == "" && isCertificateValidationError(err) {
		certificate, inspectErr := certificateForAddress(ctx, input.BaseURL)
		if inspectErr != nil {
			return TestResult{}, err
		}
		return TestResult{RequiresCertificateTrust: true, Certificate: certificate}, nil
	}
	return TestResult{}, friendlyConnectionError(err)
}

func (m *Manager) Configure(ctx context.Context, input ConfigureInput) (Status, error) {
	test, err := m.Test(ctx, TestInput{
		BaseURL:        input.BaseURL,
		APIKey:         input.APIKey,
		TLSFingerprint: input.TLSFingerprint,
	})
	if err != nil {
		return Status{}, err
	}
	if test.RequiresCertificateTrust {
		return Status{}, errors.New("review and trust the UniFi console certificate before connecting")
	}
	var selected *Site
	for index := range test.Sites {
		if test.Sites[index].ID == strings.TrimSpace(input.SiteID) {
			selected = &test.Sites[index]
			break
		}
	}
	if selected == nil {
		return Status{}, errors.New("select a site returned by this UniFi console")
	}
	key, err := loadOrCreateKey(m.keyPath)
	if err != nil {
		return Status{}, fmt.Errorf("prepare integration credentials: %w", err)
	}
	ciphertext, err := encryptSecret(key, strings.TrimSpace(input.APIKey))
	if err != nil {
		return Status{}, err
	}
	baseURL, _ := normalizeBaseURL(input.BaseURL)
	fingerprint := normalizeFingerprint(input.TLSFingerprint)
	if _, err := m.store.DB.ExecContext(ctx, `
		INSERT INTO integration_configs(kind, enabled, base_url, secret_ciphertext, site_id, site_name, tls_fingerprint, last_error, updated_at)
		VALUES(?, 1, ?, ?, ?, ?, ?, '', CURRENT_TIMESTAMP)
		ON CONFLICT(kind) DO UPDATE SET
			enabled = 1,
			base_url = excluded.base_url,
			secret_ciphertext = excluded.secret_ciphertext,
			site_id = excluded.site_id,
			site_name = excluded.site_name,
			tls_fingerprint = excluded.tls_fingerprint,
			last_error = '',
			updated_at = CURRENT_TIMESTAMP`,
		integrationKind, baseURL, ciphertext, selected.ID, selected.Name, fingerprint); err != nil {
		return Status{}, err
	}
	_, syncErr := m.Sync(ctx)
	status, statusErr := m.Status(ctx)
	if statusErr != nil {
		return Status{}, statusErr
	}
	if syncErr != nil {
		status.LastError = syncErr.Error()
	}
	return status, nil
}

func (m *Manager) Disconnect(ctx context.Context) error {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	if _, err := m.store.DB.ExecContext(ctx, `DELETE FROM integration_configs WHERE kind = ?`, integrationKind); err != nil {
		return err
	}
	if _, err := m.store.DB.ExecContext(ctx, `DELETE FROM unifi_client_snapshots`); err != nil {
		return err
	}
	if _, err := m.store.DB.ExecContext(ctx, `DELETE FROM device_names WHERE source = 'unifi'`); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	config, err := m.readConfig(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	tlsMode := "verified"
	fingerprint := ""
	if config.tlsFingerprint != "" {
		tlsMode = "pinned"
		fingerprint = displayFingerprint(config.tlsFingerprint)
	}
	return Status{
		Configured:       config.secretCiphertext != "" && config.siteID != "",
		Enabled:          config.enabled,
		BaseURL:          config.baseURL,
		SiteID:           config.siteID,
		SiteName:         config.siteName,
		APIKeyConfigured: config.secretCiphertext != "",
		TLSMode:          tlsMode,
		TLSFingerprint:   fingerprint,
		LastSyncAt:       config.lastSyncAt,
		LastError:        config.lastError,
		SyncedDevices:    config.syncedDevices,
	}, nil
}

func (m *Manager) Sync(ctx context.Context) (SyncResult, error) {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	config, err := m.readConfig(ctx)
	if errors.Is(err, sql.ErrNoRows) || !config.enabled || config.secretCiphertext == "" || config.siteID == "" {
		return SyncResult{}, ErrNotConfigured
	}
	if err != nil {
		return SyncResult{}, err
	}
	key, err := loadOrCreateKey(m.keyPath)
	if err != nil {
		return SyncResult{}, m.recordSyncError(ctx, fmt.Errorf("load integration credentials: %w", err))
	}
	apiKey, err := decryptSecret(key, config.secretCiphertext)
	if err != nil {
		return SyncResult{}, m.recordSyncError(ctx, errors.New("Faro could not read the saved UniFi API key; reconnect the integration"))
	}
	client, err := newAPIClient(config.baseURL, apiKey, config.tlsFingerprint)
	if err != nil {
		return SyncResult{}, m.recordSyncError(ctx, err)
	}
	clients, err := client.listClients(ctx, config.siteID)
	if err != nil {
		return SyncResult{}, m.recordSyncError(ctx, friendlyConnectionError(err))
	}
	startedAt := time.Now().UTC()
	synced, skipped := 0, 0
	for _, observed := range clients {
		if err := m.reconcileClient(ctx, config.siteID, observed, startedAt); err != nil {
			if errors.Is(err, errUnusableClient) {
				log.Printf("skip UniFi client %q: %v", observed.ID, err)
				skipped++
				continue
			}
			return SyncResult{}, m.recordSyncError(ctx, err)
		}
		synced++
	}
	if _, err := m.store.DB.ExecContext(ctx, `
		DELETE FROM unifi_client_snapshots
		WHERE site_id = ? AND last_synced_at < ?`, config.siteID, startedAt.Format(time.RFC3339Nano)); err != nil {
		return SyncResult{}, m.recordSyncError(ctx, err)
	}
	completedAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := m.store.DB.ExecContext(ctx, `
		UPDATE integration_configs
		SET last_sync_at = ?, last_error = '', synced_devices = ?, updated_at = CURRENT_TIMESTAMP
		WHERE kind = ?`, completedAt, synced, integrationKind); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{SyncedDevices: synced, Skipped: skipped, CompletedAt: completedAt}, nil
}

func (m *Manager) reconcileClient(ctx context.Context, siteID string, client Client, observedAt time.Time) error {
	ip := net.ParseIP(strings.TrimSpace(client.IPAddress))
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return fmt.Errorf("%w: missing IP address", errUnusableClient)
	}
	mac, err := net.ParseMAC(strings.TrimSpace(client.MACAddress))
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || allZeroMAC(mac) {
		return fmt.Errorf("%w: missing MAC address", errUnusableClient)
	}
	clientID := strings.TrimSpace(client.ID)
	if clientID == "" {
		return fmt.Errorf("%w: missing client ID", errUnusableClient)
	}
	identifiers := []deviceidentity.Identifier{
		{Kind: "mac", Value: strings.ToLower(mac.String()), Source: "unifi", Confidence: "strong"},
		{Kind: "unifi_client", Value: siteID + "/" + clientID, Source: "unifi", Confidence: "strong"},
	}
	deviceID, err := deviceidentity.ObserveAddress(ctx, m.store, ip.String(), "unifi", identifiers)
	if err != nil {
		return err
	}
	name := cleanClientName(client.Name, ip.String(), mac.String())
	if name != "" {
		if _, err := m.store.DB.ExecContext(ctx, `
			INSERT INTO device_names(device_id, source, name)
			VALUES(?, 'unifi', ?)
			ON CONFLICT(device_id, source) DO UPDATE SET
				name = excluded.name,
				last_seen_at = CURRENT_TIMESTAMP,
				updated_at = CURRENT_TIMESTAMP`, deviceID, name); err != nil {
			return err
		}
	}
	var connectedAt any
	if strings.TrimSpace(client.ConnectedAt) != "" {
		connectedAt = client.ConnectedAt
	}
	_, err = m.store.DB.ExecContext(ctx, `
		INSERT INTO unifi_client_snapshots(
			client_id, site_id, device_id, mac_address, ip_address, name,
			connection_type, uplink_device_id, connected_at, last_synced_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(site_id, client_id) DO UPDATE SET
			device_id = excluded.device_id,
			mac_address = excluded.mac_address,
			ip_address = excluded.ip_address,
			name = excluded.name,
			connection_type = excluded.connection_type,
			uplink_device_id = excluded.uplink_device_id,
			connected_at = excluded.connected_at,
			last_synced_at = excluded.last_synced_at`,
		clientID, siteID, deviceID, strings.ToLower(mac.String()), ip.String(), name,
		strings.ToUpper(strings.TrimSpace(client.Type)), strings.TrimSpace(client.UplinkDeviceID), connectedAt, observedAt.Format(time.RFC3339Nano))
	return err
}

func allZeroMAC(mac net.HardwareAddr) bool {
	for _, value := range mac {
		if value != 0 {
			return false
		}
	}
	return true
}

func (m *Manager) readConfig(ctx context.Context) (storedConfig, error) {
	var config storedConfig
	var enabled int
	var lastSync sql.NullString
	err := m.store.DB.QueryRowContext(ctx, `
		SELECT enabled, base_url, secret_ciphertext, site_id, site_name, tls_fingerprint,
		       last_sync_at, last_error, synced_devices
		FROM integration_configs WHERE kind = ?`, integrationKind).Scan(
		&enabled, &config.baseURL, &config.secretCiphertext, &config.siteID, &config.siteName,
		&config.tlsFingerprint, &lastSync, &config.lastError, &config.syncedDevices)
	config.enabled = enabled == 1
	if lastSync.Valid {
		config.lastSyncAt = lastSync.String
	}
	return config, err
}

func (m *Manager) recordSyncError(ctx context.Context, err error) error {
	message := strings.TrimSpace(err.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	_, _ = m.store.DB.ExecContext(ctx, `
		UPDATE integration_configs SET last_error = ?, updated_at = CURRENT_TIMESTAMP WHERE kind = ?`,
		message, integrationKind)
	return err
}

func friendlyConnectionError(err error) error {
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "does not resolve to a private or local network address") {
		return errors.New("use a private IP address or local hostname for your UniFi console")
	}
	var apiErr *responseError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return errors.New("UniFi rejected the API key; create a local Network API key with permission to read clients")
		case 404:
			return errors.New("this console does not expose the official UniFi Network integration API")
		}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		var networkErr net.Error
		if errors.As(urlErr.Err, &networkErr) && networkErr.Timeout() {
			return errors.New("Faro timed out while contacting the UniFi console; check its address and firewall")
		}
		return fmt.Errorf("Faro could not reach the UniFi console: %v", urlErr.Err)
	}
	return err
}

func cleanClientName(name, ip, mac string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, ip) || strings.EqualFold(name, mac) {
		return ""
	}
	if len(name) > 160 {
		name = name[:160]
	}
	return name
}

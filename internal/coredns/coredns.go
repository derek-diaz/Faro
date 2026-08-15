package coredns

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/coredns/caddy/caddyfile"
	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/protectiontime"
)

var reloadTotal atomic.Uint64
var reloadFailedTotal atomic.Uint64
var errReloadHashUnavailable = errors.New("CoreDNS reload hash is unavailable")

const coreDNSPathPrefix = "/etc/coredns/"

type Manager struct {
	Store         *db.Store
	ConfigDir     string
	CoreDNSBinary string
	MetricsURL    string
	BeforeApply   func(context.Context) error
	// RollbackApply restores state prepared by BeforeApply when the staged
	// CoreDNS files cannot be installed or the running resolver rejects them.
	// It is used by the encrypted DNS gateway to keep its live transport in
	// step with the last-known-good Corefile.
	RollbackApply func(context.Context) error
	// CommitApply publishes dependent runtime state only after CoreDNS has
	// accepted the new files. A failure rolls CoreDNS back before Apply returns.
	CommitApply       func(context.Context) error
	AfterApply        func(context.Context)
	ValidationTimeout time.Duration
	ReloadTimeout     time.Duration
	HTTPClient        *http.Client
	applyMu           sync.Mutex
	temporalMu        sync.Mutex
	temporalSignature string
	bootstrapped      bool
	validateGenerated func(context.Context, map[string][]byte) error
	readLiveHash      func(context.Context) (string, error)
	waitForLiveHash   func(context.Context, string) error
}

type renderState struct {
	Upstreams    []string
	CacheEnabled bool
	CacheTTL     int
	DenialTTL    int
	AllowedCIDRs []string
}

type protectionRender struct {
	ID         int64
	Name       string
	IsDefault  bool
	ClientIPs  []string
	HostsFile  string
	BlockHosts string
	Active     bool
}

type pausedDeviceRender struct {
	ID        int64
	ClientIPs []string
}

type RuleMatch struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type LocalRecordMatch struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DomainDecision struct {
	Action       string            `json:"action"`
	Reason       string            `json:"reason"`
	Protection   *RuleMatch        `json:"protection,omitempty"`
	Allowlist    *RuleMatch        `json:"allowlist,omitempty"`
	ManualBlock  *RuleMatch        `json:"manual_block,omitempty"`
	Blocklists   []RuleMatch       `json:"blocklists,omitempty"`
	LocalRecord  *LocalRecordMatch `json:"local_record,omitempty"`
	Confidence   string            `json:"confidence,omitempty"`
	CapturedAt   string            `json:"captured_at,omitempty"`
	Upstream     string            `json:"upstream,omitempty"`
	ResponseCode string            `json:"response_code,omitempty"`
}

func NewManager(store *db.Store, configDir string) *Manager {
	manager := &Manager{
		Store:             store,
		ConfigDir:         configDir,
		CoreDNSBinary:     env("FARO_COREDNS_BINARY", "/usr/local/bin/coredns"),
		MetricsURL:        env("FARO_COREDNS_METRICS_URL", "http://dns:9153/metrics"),
		ValidationTimeout: 15 * time.Second,
		// CoreDNS temporarily stops serving metrics while a reload parses large
		// hosts files. Ten seconds is too short even for the starter OISD list on
		// slower disks, causing Faro to roll back a configuration CoreDNS accepts.
		ReloadTimeout: 45 * time.Second,
		HTTPClient:    &http.Client{Timeout: 2 * time.Second},
	}
	manager.validateGenerated = manager.validateWithCoreDNS
	manager.readLiveHash = manager.liveCorefileHash
	manager.waitForLiveHash = manager.waitUntilLiveHash
	return manager
}

func (manager *Manager) Apply(ctx context.Context) error {
	manager.applyMu.Lock()
	defer manager.applyMu.Unlock()
	reloadTotal.Add(1)
	state, err := manager.render(ctx)
	if err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	if err := os.MkdirAll(manager.ConfigDir, 0o755); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}

	files, err := runtimeFilesFromRenderedState(state)
	if err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	var prepared bool
	var prepare func() error
	if manager.BeforeApply != nil {
		prepare = func() error {
			if err := manager.BeforeApply(ctx); err != nil {
				return fmt.Errorf("prepare DNS transport: %w", err)
			}
			prepared = true
			return nil
		}
	}
	if err := manager.applyFilesLocked(ctx, files, prepare, manager.CommitApply); err != nil {
		if prepared && manager.RollbackApply != nil {
			if rollbackErr := manager.RollbackApply(context.WithoutCancel(ctx)); rollbackErr != nil {
				return fmt.Errorf("%w; restore previous DNS transport: %v", err, rollbackErr)
			}
		}
		return err
	}
	if manager.AfterApply != nil {
		manager.AfterApply(context.WithoutCancel(ctx))
	}
	manager.rememberTemporalState(context.WithoutCancel(ctx))
	return nil
}

// RunTemporalReloads keeps the generated DNS policy aligned with protection
// schedules and temporary pauses even when nobody has the Faro UI open.
func (manager *Manager) RunTemporalReloads(ctx context.Context) {
	manager.rememberTemporalState(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			signature, err := manager.currentTemporalSignature(ctx, time.Now())
			if err != nil {
				continue
			}
			manager.temporalMu.Lock()
			previous := manager.temporalSignature
			manager.temporalMu.Unlock()
			if previous != "" && previous != signature {
				_ = manager.Apply(ctx)
			}
		}
	}
}

// ApplyReplica installs a controller-produced, already rendered configuration.
// Only the upstream transport settings are written locally because the DNS
// gateway needs them at runtime; the remaining replicated state is represented
// by the exact generated CoreDNS files.
func (manager *Manager) ApplyReplica(ctx context.Context, files map[string][]byte, runtimeSettings map[string]string) error {
	manager.applyMu.Lock()
	defer manager.applyMu.Unlock()
	reloadTotal.Add(1)
	if err := validateReplicaFiles(files); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	previous, err := readRuntimeSettings(ctx, manager.Store)
	if err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	if err := writeRuntimeSettings(ctx, manager.Store, runtimeSettings); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	restoreRuntime := func() {
		_ = writeRuntimeSettings(context.WithoutCancel(ctx), manager.Store, previous)
		if manager.BeforeApply != nil {
			_ = manager.BeforeApply(context.WithoutCancel(ctx))
		}
	}
	prepared := false
	var prepare func() error
	if manager.BeforeApply != nil {
		prepare = func() error {
			if err := manager.BeforeApply(ctx); err != nil {
				return fmt.Errorf("prepare replicated DNS transport: %w", err)
			}
			prepared = true
			return nil
		}
	}
	if err := manager.applyFilesLocked(ctx, cloneFiles(files), prepare, manager.CommitApply); err != nil {
		var rollbackErr error
		if prepared && manager.RollbackApply != nil {
			rollbackErr = manager.RollbackApply(context.WithoutCancel(ctx))
		}
		restoreRuntime()
		if rollbackErr != nil {
			return fmt.Errorf("%w; restore previous DNS transport: %v", err, rollbackErr)
		}
		return err
	}
	if manager.AfterApply != nil {
		manager.AfterApply(context.WithoutCancel(ctx))
	}
	return nil
}

func (manager *Manager) applyFilesLocked(ctx context.Context, files map[string][]byte, prepare func() error, commit func(context.Context) error) error {
	if err := os.MkdirAll(manager.ConfigDir, 0o755); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	if err := validateGeneratedFiles(files); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	if err := manager.validateGenerated(ctx, files); err != nil {
		reloadFailedTotal.Add(1)
		return fmt.Errorf("CoreDNS rejected the staged configuration: %w", err)
	}

	previousHash, liveErr := manager.readLiveHash(ctx)
	hashNotInitialized := errors.Is(liveErr, errReloadHashUnavailable)
	if manager.bootstrapped && liveErr != nil && !hashNotInitialized {
		reloadFailedTotal.Add(1)
		return fmt.Errorf("could not verify the running DNS engine before applying configuration: %w", liveErr)
	}
	staleFiles, err := staleManagedFiles(manager.ConfigDir, files)
	if err != nil {
		reloadFailedTotal.Add(1)
		return fmt.Errorf("find stale Faro DNS files: %w", err)
	}
	backups, err := snapshotFiles(manager.ConfigDir, append(fileNames(files), staleFiles...))
	if err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	touchedFiles := append(fileNames(files), staleFiles...)
	corefilePath := filepath.Join(manager.ConfigDir, "Corefile")
	expectedHash, err := corefileHash(corefilePath, files["Corefile"])
	if err != nil {
		reloadFailedTotal.Add(1)
		return fmt.Errorf("calculate generated CoreDNS reload hash: %w", err)
	}
	previousFileHash, previousFileHashErr := corefileHash(corefilePath, backups["Corefile"])
	corefileChanged := previousFileHashErr != nil || previousFileHash != expectedHash
	if prepare != nil {
		if err := prepare(); err != nil {
			reloadFailedTotal.Add(1)
			return err
		}
	}
	if err := replaceWithRollback(manager.ConfigDir, files, staleFiles, backups, touchedFiles); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	if liveErr == nil || (manager.bootstrapped && hashNotInitialized && corefileChanged) {
		if err := manager.waitForLiveHash(ctx, expectedHash); err != nil {
			reloadFailedTotal.Add(1)
			return manager.handleReloadFailure(ctx, backups, touchedFiles, previousHash, err)
		}
	}
	if commit != nil {
		if err := commit(context.WithoutCancel(ctx)); err != nil {
			reloadFailedTotal.Add(1)
			return manager.handleReloadFailure(ctx, backups, touchedFiles, previousHash, fmt.Errorf("commit accepted DNS state: %w", err))
		}
	}
	manager.bootstrapped = true
	return nil
}

func (manager *Manager) handleReloadFailure(ctx context.Context, backups map[string][]byte, touchedFiles []string, previousHash string, reloadErr error) error {
	rollback(manager.ConfigDir, backups, touchedFiles)
	if previousHash == "" {
		return fmt.Errorf("CoreDNS did not accept the new configuration; the previous files were restored but no prior reload hash was available: %w", reloadErr)
	}
	if rollbackErr := manager.waitForLiveHash(context.WithoutCancel(ctx), previousHash); rollbackErr != nil {
		return fmt.Errorf("CoreDNS did not accept the new configuration and rollback could not be verified: %v; rollback verification: %w", reloadErr, rollbackErr)
	}
	return fmt.Errorf("CoreDNS did not accept the new configuration; the previous configuration was restored: %w", reloadErr)
}

func ReloadTotals() (uint64, uint64) {
	return reloadTotal.Load(), reloadFailedTotal.Load()
}

type renderedFiles struct {
	Corefile        string
	LocalHosts      string
	BlockHosts      string
	ProtectionHosts map[string]string
}

func (manager *Manager) render(ctx context.Context) (renderedFiles, error) {
	settings, err := settingsMap(ctx, manager.Store)
	if err != nil {
		return renderedFiles{}, err
	}
	state, err := renderStateForSettings(settings)
	if err != nil {
		return renderedFiles{}, err
	}

	localHosts, err := manager.localHosts(ctx)
	if err != nil {
		return renderedFiles{}, err
	}
	protections, err := manager.protections(ctx)
	if err != nil {
		return renderedFiles{}, err
	}
	if len(protections) == 0 {
		return renderedFiles{}, errors.New("home protection is missing")
	}
	pausedDevices, err := manager.pausedDevices(ctx, time.Now())
	if err != nil {
		return renderedFiles{}, err
	}

	blockTemplate := template.Must(template.New("serverBlock").Parse(`.:53 {
    {{ if not .Protection.IsDefault }}view protection_{{ .Protection.ID }} {
        expr {{ .ViewExpression }}
    }
    {{ end }}
    errors
    metadata
    log . "FARO|{remote}|{type}|{name}|{rcode}|{duration}|{/forward/upstream}"
    {{ if .Protection.IsDefault }}
    prometheus 0.0.0.0:9153
    reload 2s
	{{ end }}
	acl {
		allow net {{ range .AllowedCIDRs }}{{ . }} {{ end }}
		block
	}
	    hosts ` + coreDNSPathPrefix + `{{ .Protection.HostsFile }} {
        ttl 60
        fallthrough
    }
    {{ if .CacheEnabled }}cache {{ .CacheTTL }} {
        success 9984 {{ .CacheTTL }} 5
        denial 4096 {{ .DenialTTL }} 5
    }
    {{ end }}forward . {{ range .Upstreams }}{{ . }} {{ end }}
}
`))
	var core bytes.Buffer
	for _, device := range pausedDevices {
		if _, err := fmt.Fprintf(&core, `.:53 {
    view device_pause_%d {
        expr %s
    }
    errors
    metadata
    log . "FARO|{remote}|{type}|{name}|{rcode}|{duration}|{/forward/upstream}"
    acl {
        allow net %s
        block
    }
    template ANY ANY {
        rcode REFUSED
    }
}
`, device.ID, protectionViewExpression(device.ClientIPs), strings.Join(state.AllowedCIDRs, " ")); err != nil {
			return renderedFiles{}, err
		}
	}
	protectionHosts, defaultBlocks, err := renderProtectionBlocks(&core, blockTemplate, state, localHosts, protections)
	if err != nil {
		return renderedFiles{}, err
	}
	return renderedFiles{
		Corefile:        core.String(),
		LocalHosts:      localHosts,
		BlockHosts:      defaultBlocks,
		ProtectionHosts: protectionHosts,
	}, nil
}

func renderStateForSettings(settings map[string]string) (renderState, error) {
	upstreams := splitCSV(settings["upstream_dns"])
	if len(upstreams) == 0 {
		upstreams = []string{"1.1.1.1", "9.9.9.9"}
	}
	if len(upstreams) > 15 {
		return renderState{}, fmt.Errorf("at most 15 upstream resolvers are supported")
	}
	for _, upstream := range upstreams {
		if net.ParseIP(upstream) == nil {
			return renderState{}, fmt.Errorf("invalid upstream resolver %q", upstream)
		}
	}
	upstreamTransport := strings.TrimSpace(settings["upstream_transport"])
	if upstreamTransport == "" {
		upstreamTransport = "standard"
	}
	switch upstreamTransport {
	case "standard":
	case "encrypted":
		upstreams = []string{"127.0.0.1:5053"}
	default:
		return renderState{}, fmt.Errorf("invalid upstream transport %q", upstreamTransport)
	}

	cacheTTL := 300
	if parsed, parseErr := strconv.Atoi(settings["dns_cache_ttl"]); parseErr == nil {
		cacheTTL = max(30, min(parsed, 3600))
	}
	allowedCIDRs := splitCSV(settings["allowed_client_cidrs"])
	if len(allowedCIDRs) == 0 {
		allowedCIDRs = []string{"127.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10"}
	}
	for _, allowedCIDR := range allowedCIDRs {
		if _, _, parseErr := net.ParseCIDR(allowedCIDR); parseErr != nil {
			return renderState{}, fmt.Errorf("invalid allowed client CIDR %q", allowedCIDR)
		}
	}
	return renderState{
		Upstreams:    upstreams,
		CacheEnabled: settings["dns_cache_enabled"] != "false",
		CacheTTL:     cacheTTL,
		DenialTTL:    min(cacheTTL, 60),
		AllowedCIDRs: allowedCIDRs,
	}, nil
}

func renderProtectionBlocks(core *bytes.Buffer, blockTemplate *template.Template, state renderState, localHosts string, protections []protectionRender) (map[string]string, string, error) {
	protectionHosts := make(map[string]string, len(protections))
	defaultBlocks := ""
	for _, protection := range protections {
		protectionHosts[protection.HostsFile] = localHosts + "\n" + protection.BlockHosts
		if protection.IsDefault {
			defaultBlocks = protection.BlockHosts
		}
		if !protection.IsDefault && len(protection.ClientIPs) == 0 {
			continue
		}
		data := struct {
			renderState
			Protection     protectionRender
			ViewExpression string
		}{renderState: state, Protection: protection, ViewExpression: protectionViewExpression(protection.ClientIPs)}
		if err := blockTemplate.Execute(core, data); err != nil {
			return nil, "", err
		}
	}
	return protectionHosts, defaultBlocks, nil
}

func (manager *Manager) localHosts(ctx context.Context) (string, error) {
	rows, err := manager.Store.DB.QueryContext(ctx, `SELECT hostname, type, value FROM dns_records ORDER BY hostname`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	var b strings.Builder
	b.WriteString("# Generated by Faro. Manual edits will be replaced.\n")
	for rows.Next() {
		var host, typ, value string
		if err := rows.Scan(&host, &typ, &value); err != nil {
			return "", err
		}
		normalizedHost, normalizedType, normalizedValue, normalizeErr := db.NormalizeRecord(host, typ, value)
		if normalizeErr != nil {
			return "", fmt.Errorf("invalid stored DNS record %q: %w", host, normalizeErr)
		}
		if normalizedType == "A" || normalizedType == "AAAA" {
			if _, err := fmt.Fprintf(&b, "%s %s\n", normalizedValue, normalizedHost); err != nil {
				return "", err
			}
		}
	}
	return b.String(), rows.Err()
}

func (manager *Manager) blockHostsForProtection(ctx context.Context, protectionID int64) (string, error) {
	allowlist, err := domains(ctx, manager.Store, `SELECT domain FROM protection_allow_entries WHERE protection_id = ?`, protectionID)
	if err != nil {
		return "", err
	}
	blocked, err := domains(ctx, manager.Store, `
		SELECT domain FROM protection_block_entries WHERE protection_id = ?
		UNION
		SELECT e.domain
		FROM blocklist_entries e
		JOIN blocklists b ON b.id = e.blocklist_id
		JOIN protection_blocklists p ON p.blocklist_id = b.id
		WHERE b.enabled = 1 AND p.protection_id = ?
	`, protectionID, protectionID)
	if err != nil {
		return "", err
	}

	allowed := map[string]struct{}{}
	for _, rawDomain := range allowlist {
		domain, normalizeErr := db.NormalizeDomain(rawDomain)
		if normalizeErr != nil {
			return "", fmt.Errorf("invalid stored allowlist domain %q: %w", rawDomain, normalizeErr)
		}
		allowed[domain] = struct{}{}
	}

	var b strings.Builder
	b.WriteString("# Generated by Faro. Allowlist entries are excluded.\n")
	for _, rawDomain := range blocked {
		domain, normalizeErr := db.NormalizeDomain(rawDomain)
		if normalizeErr != nil {
			return "", fmt.Errorf("invalid stored block domain %q: %w", rawDomain, normalizeErr)
		}
		if _, ok := allowed[domain]; ok {
			continue
		}
		if _, err := fmt.Fprintf(&b, "0.0.0.0 %s\n", domain); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func (manager *Manager) protections(ctx context.Context) ([]protectionRender, error) {
	rows, err := manager.Store.DB.QueryContext(ctx, `
		SELECT id, name, is_default, paused_until, schedule_enabled, schedule_days,
		       schedule_start, schedule_end, schedule_timezone
		FROM protection_profiles ORDER BY is_default, id`)
	if err != nil {
		return nil, err
	}
	var protections []protectionRender
	for rows.Next() {
		var protection protectionRender
		var pausedUntil, days, start, end, timezone string
		var scheduleEnabled bool
		if err := rows.Scan(&protection.ID, &protection.Name, &protection.IsDefault, &pausedUntil,
			&scheduleEnabled, &days, &start, &end, &timezone); err != nil {
			_ = rows.Close()
			return nil, err
		}
		protection.Active = !protectiontime.PausedAt(pausedUntil, time.Now()) && protectiontime.ActiveAt(protectiontime.Schedule{
			Enabled: scheduleEnabled, Days: days, Start: start, End: end, Timezone: timezone,
		}, time.Now())
		protections = append(protections, protection)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range protections {
		protection := &protections[index]
		protection.HostsFile = fmt.Sprintf("protection-%d.hosts", protection.ID)
		protection.ClientIPs, err = domains(ctx, manager.Store, `
			SELECT address FROM device_addresses a
			JOIN device_protection_memberships m ON m.device_id = a.device_id
			WHERE m.protection_id = ?
			UNION
			SELECT client_ip FROM device_protection_assignments WHERE protection_id = ?`, protection.ID, protection.ID)
		if err != nil {
			return nil, err
		}
		if protection.Active {
			protection.BlockHosts, err = manager.blockHostsForProtection(ctx, protection.ID)
		}
		if err != nil {
			return nil, err
		}
	}
	return protections, nil
}

func (manager *Manager) pausedDevices(ctx context.Context, now time.Time) ([]pausedDeviceRender, error) {
	rows, err := manager.Store.DB.QueryContext(ctx, `SELECT device_id, paused_until FROM device_dns_pauses ORDER BY device_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []pausedDeviceRender
	for rows.Next() {
		var device pausedDeviceRender
		var pausedUntil string
		if err := rows.Scan(&device.ID, &pausedUntil); err != nil {
			return nil, err
		}
		if !protectiontime.PausedAt(pausedUntil, now) {
			continue
		}
		device.ClientIPs, err = domains(ctx, manager.Store, `SELECT address FROM device_addresses WHERE device_id = ?`, device.ID)
		if err != nil {
			return nil, err
		}
		if len(device.ClientIPs) > 0 {
			result = append(result, device)
		}
	}
	return result, rows.Err()
}

func (manager *Manager) rememberTemporalState(ctx context.Context) {
	signature, err := manager.currentTemporalSignature(ctx, time.Now())
	if err != nil {
		return
	}
	manager.temporalMu.Lock()
	manager.temporalSignature = signature
	manager.temporalMu.Unlock()
}

func (manager *Manager) currentTemporalSignature(ctx context.Context, now time.Time) (string, error) {
	// Temporal checks run every 15 seconds. Keep this query limited to the
	// fields that determine whether policy is active; rendering blocklists here
	// would repeatedly scan and sort the entire installed lists just to discover
	// that no schedule boundary was crossed.
	rows, err := manager.Store.DB.QueryContext(ctx, `
		SELECT id, paused_until, schedule_enabled, schedule_days, schedule_start, schedule_end, schedule_timezone
		FROM protection_profiles ORDER BY id`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	var value strings.Builder
	for rows.Next() {
		var id int64
		var pausedUntil, days, start, end, timezone string
		var scheduleEnabled bool
		if err := rows.Scan(&id, &pausedUntil, &scheduleEnabled, &days, &start, &end, &timezone); err != nil {
			return "", err
		}
		active := !protectiontime.PausedAt(pausedUntil, now) && protectiontime.ActiveAt(protectiontime.Schedule{
			Enabled: scheduleEnabled, Days: days, Start: start, End: end, Timezone: timezone,
		}, now)
		_, _ = fmt.Fprintf(&value, "p:%d:%t;", id, active)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if err := rows.Close(); err != nil {
		return "", err
	}

	pausedRows, err := manager.Store.DB.QueryContext(ctx, `SELECT device_id, paused_until FROM device_dns_pauses ORDER BY device_id`)
	if err != nil {
		return "", err
	}
	defer func() { _ = pausedRows.Close() }()
	for pausedRows.Next() {
		var deviceID int64
		var pausedUntil string
		if err := pausedRows.Scan(&deviceID, &pausedUntil); err != nil {
			return "", err
		}
		if protectiontime.PausedAt(pausedUntil, now) {
			_, _ = fmt.Fprintf(&value, "d:%d;", deviceID)
		}
	}
	if err := pausedRows.Err(); err != nil {
		return "", err
	}
	return value.String(), nil
}

func protectionViewExpression(clientIPs []string) string {
	parts := make([]string, 0, len(clientIPs))
	for _, clientIP := range clientIPs {
		bits := 32
		if parsed := net.ParseIP(clientIP); parsed != nil && parsed.To4() == nil {
			bits = 128
		}
		parts = append(parts, fmt.Sprintf("incidr(client_ip(), '%s/%d')", clientIP, bits))
	}
	return strings.Join(parts, " || ")
}

func settingsMap(ctx context.Context, store *db.Store) (map[string]string, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, rows.Err()
}

func domains(ctx context.Context, store *db.Store, query string, args ...any) ([]string, error) {
	rows, err := store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []string
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return nil, err
		}
		result = append(result, domain)
	}
	sort.Strings(result)
	return result, rows.Err()
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	var result []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func validateGeneratedFiles(files map[string][]byte) error {
	corefile := string(files["Corefile"])
	if !strings.Contains(corefile, ".:53") || !strings.Contains(corefile, "forward .") {
		return fmt.Errorf("generated Corefile is missing required server or forward block")
	}
	hostsFiles, err := corefileHostFiles(corefile)
	if err != nil {
		return err
	}
	if len(hostsFiles) == 0 {
		return errors.New("generated Corefile has no Faro hosts files")
	}
	for _, name := range hostsFiles {
		if _, ok := files[name]; !ok {
			return fmt.Errorf("generated Corefile references missing hosts file %q", name)
		}
	}
	for name := range files {
		if name == "Corefile" {
			continue
		}
		if !containsString(hostsFiles, name) {
			return fmt.Errorf("generated Corefile does not reference hosts file %q", name)
		}
	}
	return nil
}

func corefileHostFiles(corefile string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, line := range strings.Split(corefile, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "hosts" {
			continue
		}
		name, ok := strings.CutPrefix(fields[1], coreDNSPathPrefix)
		if !ok {
			continue
		}
		if filepath.Base(name) != name || !strings.HasSuffix(name, ".hosts") {
			return nil, fmt.Errorf("Corefile contains unsafe hosts file %q", name)
		}
		seen[name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func isManagedDNSFileName(name string) bool {
	return name == "Corefile" || name == "faro.hosts" || name == "local.hosts" || name == "blocklist.hosts" ||
		(strings.HasPrefix(name, "protection-") && strings.HasSuffix(name, ".hosts") && filepath.Base(name) == name)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateReplicaFiles(files map[string][]byte) error {
	if len(files) < 2 || len(files) > 64 {
		return errors.New("replicated DNS configuration has an invalid file count")
	}
	var total int64
	for name, content := range files {
		if name != "Corefile" && name != "faro.hosts" && name != "local.hosts" && name != "blocklist.hosts" &&
			!(strings.HasPrefix(name, "protection-") && strings.HasSuffix(name, ".hosts")) {
			return fmt.Errorf("replicated DNS configuration contains unexpected file %q", name)
		}
		if filepath.Base(name) != name {
			return fmt.Errorf("replicated DNS configuration contains unsafe file name %q", name)
		}
		total += int64(len(content))
		if total > 512<<20 {
			return errors.New("replicated DNS configuration is too large")
		}
	}
	return validateGeneratedFiles(files)
}

func readRuntimeSettings(ctx context.Context, store *db.Store) (map[string]string, error) {
	result := map[string]string{}
	for _, key := range []string{"upstream_dns", "upstream_transport"} {
		var value string
		if err := store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

func writeRuntimeSettings(ctx context.Context, store *db.Store, values map[string]string) error {
	upstreams := splitCSV(values["upstream_dns"])
	if len(upstreams) == 0 || len(upstreams) > 15 {
		return errors.New("replicated upstream configuration is invalid")
	}
	for _, upstream := range upstreams {
		if net.ParseIP(upstream) == nil {
			return fmt.Errorf("replicated upstream resolver %q is invalid", upstream)
		}
	}
	transport := strings.TrimSpace(values["upstream_transport"])
	if transport != "standard" && transport != "encrypted" {
		return errors.New("replicated upstream transport is invalid")
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, key := range []string{"upstream_dns", "upstream_transport"} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`, key, values[key]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func cloneFiles(files map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(files))
	for name, content := range files {
		result[name] = append([]byte(nil), content...)
	}
	return result
}

// validateWithCoreDNS starts the same CoreDNS binary used by the DNS service
// against a private staged copy of every generated file. CoreDNS only prints
// its startup banner after the complete plugin chain has parsed and initialized,
// which gives Faro a real syntax and startup check without touching live files.
func (manager *Manager) validateWithCoreDNS(ctx context.Context, files map[string][]byte) error {
	stagingDir, err := os.MkdirTemp("", "faro-coredns-validation-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	staged, err := validationFiles(stagingDir, files)
	if err != nil {
		return err
	}
	for name, content := range staged {
		if err := os.WriteFile(filepath.Join(stagingDir, name), content, 0o600); err != nil {
			return err
		}
	}

	timeout := manager.ValidationTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	validationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(validationCtx, manager.CoreDNSBinary, "-conf", filepath.Join(stagingDir, "Corefile"))
	output := &lockedBuffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %s: %w", manager.CoreDNSBinary, err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return commandExitError(err, output.String())
		case <-ticker.C:
			if strings.Contains(output.String(), "CoreDNS-") {
				_ = command.Process.Kill()
				<-done
				return nil
			}
		case <-validationCtx.Done():
			_ = command.Process.Kill()
			<-done
			return fmt.Errorf("CoreDNS validation timed out: %s", compactOutput(output.String()))
		}
	}
}

func validationFiles(stagingDir string, files map[string][]byte) (map[string][]byte, error) {
	if err := validateGeneratedFiles(files); err != nil {
		return nil, err
	}
	staged := make(map[string][]byte, len(files))
	for name, content := range files {
		staged[name] = append([]byte(nil), content...)
	}
	corefile := string(staged["Corefile"])
	stagedPath := filepath.ToSlash(stagingDir) + "/"
	corefile = strings.ReplaceAll(corefile, coreDNSPathPrefix, stagedPath)
	corefile = strings.ReplaceAll(corefile, ".:53 {", ".:0 {")
	corefile = strings.ReplaceAll(corefile, "prometheus 0.0.0.0:9153", "prometheus 127.0.0.1:0")
	staged["Corefile"] = []byte(corefile)
	return staged, nil
}

func commandExitError(err error, output string) error {
	detail := compactOutput(output)
	if detail == "" {
		detail = "CoreDNS exited before completing startup"
	}
	if err == nil {
		return errors.New(detail)
	}
	return fmt.Errorf("%s: %w", detail, err)
}

func compactOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) > 4096 {
		output = output[len(output)-4096:]
	}
	return output
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (buffer *lockedBuffer) Write(input []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.Write(input)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.String()
}

func (manager *Manager) liveCorefileHash(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manager.MetricsURL, nil)
	if err != nil {
		return "", err
	}
	client := manager.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("CoreDNS metrics returned %s", response.Status)
	}
	hash, ok, err := reloadHashFromMetrics(response.Body)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errReloadHashUnavailable
	}
	return hash, nil
}

func (manager *Manager) waitUntilLiveHash(ctx context.Context, expected string) error {
	timeout := manager.ReloadTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastHash string
	var lastErr error
	for {
		hash, err := manager.liveCorefileHash(waitCtx)
		if err == nil {
			lastHash = hash
			if strings.EqualFold(hash, expected) {
				return nil
			}
		} else {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for CoreDNS reload: %w", lastErr)
			}
			return fmt.Errorf("timed out waiting for CoreDNS reload (running %s, expected %s)", shortHash(lastHash), shortHash(expected))
		case <-ticker.C:
		}
	}
}

func reloadHashFromMetrics(reader io.Reader) (string, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 4<<20))
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "coredns_reload_version_info{") {
			continue
		}
		labels, _, found := strings.Cut(line, "}")
		if !found {
			continue
		}
		if !strings.EqualFold(metricLabel(labels, "hash"), "sha512") {
			continue
		}
		value := metricLabel(labels, "value")
		if value != "" {
			return value, true, nil
		}
	}
	return "", false, nil
}

func metricLabel(labels, name string) string {
	needle := name + "=\""
	start := strings.Index(labels, needle)
	if start < 0 {
		return ""
	}
	start += len(needle)
	value, _, found := strings.Cut(labels[start:], `"`)
	if !found {
		return ""
	}
	return value
}

func corefileHash(filename string, corefile []byte) (string, error) {
	serverBlocks, err := caddyfile.Parse(filename, bytes.NewReader(corefile), nil)
	if err != nil {
		return "", err
	}
	parsed, err := json.Marshal(serverBlocks)
	if err != nil {
		return "", err
	}
	sum := sha512.Sum512(parsed)
	return hex.EncodeToString(sum[:]), nil
}

func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func snapshotFiles(dir string, names []string) (map[string][]byte, error) {
	backups := map[string][]byte{}
	seen := map[string]struct{}{}
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			backups[name] = content
			continue
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return backups, nil
}

func staleManagedFiles(dir string, desired map[string][]byte) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, entry := range entries {
		name := entry.Name()
		if !isManagedDNSFileName(name) {
			continue
		}
		if _, ok := desired[name]; ok {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			continue
		}
		stale = append(stale, name)
	}
	sort.Strings(stale)
	return stale, nil
}

func fileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func replaceWithRollback(dir string, files map[string][]byte, remove []string, backups map[string][]byte, touched []string) error {
	writeNames := fileNames(files)
	if len(writeNames) > 1 {
		for index, name := range writeNames {
			if name != "Corefile" {
				continue
			}
			writeNames = append(writeNames[:index], append(writeNames[index+1:], name)...)
			break
		}
	}
	for _, name := range writeNames {
		content := files[name]
		tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
		if err != nil {
			rollback(dir, backups, touched)
			return err
		}
		tmpName := tmp.Name()
		if _, err := tmp.Write(content); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			rollback(dir, backups, touched)
			return err
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpName)
			rollback(dir, backups, touched)
			return err
		}
		if err := os.Chmod(tmpName, 0o644); err != nil {
			_ = os.Remove(tmpName)
			rollback(dir, backups, touched)
			return err
		}
		target := filepath.Join(dir, name)
		if err := os.Rename(tmpName, target); err != nil {
			_ = os.Remove(tmpName)
			rollback(dir, backups, touched)
			return err
		}
	}
	for _, name := range remove {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			rollback(dir, backups, touched)
			return err
		}
	}
	return nil
}

func rollback(dir string, backups map[string][]byte, written []string) {
	for _, name := range written {
		path := filepath.Join(dir, name)
		if content, ok := backups[name]; ok {
			_ = os.WriteFile(path, content, 0o644)
		} else {
			_ = os.Remove(path)
		}
	}
}

func ExplainDomain(ctx context.Context, store *db.Store, domain string) DomainDecision {
	return ExplainDomainForClient(ctx, store, domain, "")
}

func ExplainDomainForClient(ctx context.Context, store *db.Store, domain, clientIP string) DomainDecision {
	normalized, err := db.NormalizeDomain(domain)
	if err != nil {
		return DomainDecision{Action: "allowed"}
	}
	decision := DomainDecision{Action: "allowed"}
	if clientIP != "" {
		var pausedUntil string
		if store.DB.QueryRowContext(ctx, `
			SELECT p.paused_until FROM device_dns_pauses p
			JOIN device_addresses a ON a.device_id = p.device_id
			WHERE a.address = ? LIMIT 1`, clientIP).Scan(&pausedUntil) == nil && protectiontime.PausedAt(pausedUntil, time.Now()) {
			decision.Action = "blocked"
			decision.Reason = "DNS access is temporarily paused for this device."
			return decision
		}
	}
	var protectionID int64
	var protectionName string
	var pausedUntil, scheduleDays, scheduleStart, scheduleEnd, scheduleTimezone string
	var scheduleEnabled bool
	var allowID, manualID int64
	var localID int64
	var localType, localValue string
	err = store.DB.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.paused_until, p.schedule_enabled, p.schedule_days, p.schedule_start, p.schedule_end, p.schedule_timezone,
		       COALESCE((SELECT id FROM protection_allow_entries WHERE protection_id = p.id AND domain = ? LIMIT 1), 0),
		       COALESCE((SELECT id FROM protection_block_entries WHERE protection_id = p.id AND domain = ? LIMIT 1), 0),
		       COALESCE(local.id, 0), COALESCE(local.type, ''), COALESCE(local.value, '')
		FROM protection_profiles p
		LEFT JOIN (SELECT id, type, value FROM dns_records WHERE hostname = ? ORDER BY id LIMIT 1) local ON 1 = 1
		LEFT JOIN device_addresses da ON da.address = ?
		LEFT JOIN device_protection_memberships m ON m.protection_id = p.id AND m.device_id = da.device_id
		LEFT JOIN device_protection_assignments legacy ON legacy.protection_id = p.id AND legacy.client_ip = ?
		WHERE m.device_id IS NOT NULL OR legacy.client_ip IS NOT NULL OR p.is_default = 1
		ORDER BY CASE WHEN m.device_id IS NOT NULL THEN 0 WHEN legacy.client_ip IS NOT NULL THEN 1 ELSE 2 END
		LIMIT 1
	`, normalized, normalized, normalized, clientIP, clientIP).Scan(&protectionID, &protectionName, &pausedUntil, &scheduleEnabled, &scheduleDays, &scheduleStart, &scheduleEnd, &scheduleTimezone, &allowID, &manualID, &localID, &localType, &localValue)
	if err != nil {
		var local LocalRecordMatch
		if lookupErr := store.DB.QueryRowContext(ctx, `SELECT id, type, value FROM dns_records WHERE hostname = ? ORDER BY id LIMIT 1`, normalized).Scan(&local.ID, &local.Type, &local.Value); lookupErr == nil {
			decision.LocalRecord = &local
		}
		return decision
	}
	decision.Protection = &RuleMatch{Kind: "protection", ID: protectionID, Name: protectionName}
	if localID != 0 {
		decision.LocalRecord = &LocalRecordMatch{ID: localID, Type: localType, Value: localValue}
	}
	if allowID != 0 {
		decision.Allowlist = &RuleMatch{Kind: "allowlist", ID: allowID, Name: protectionName + " exception"}
	}
	if manualID != 0 {
		decision.ManualBlock = &RuleMatch{Kind: "manual_block", ID: manualID, Name: protectionName + " custom block"}
	}

	rows, err := store.DB.QueryContext(ctx, `
		SELECT b.id, b.name
		FROM blocklist_entries e
		JOIN blocklists b ON b.id = e.blocklist_id
		JOIN protection_blocklists p ON p.blocklist_id = b.id
		WHERE b.enabled = 1 AND p.protection_id = ? AND e.domain = ?
		ORDER BY b.name
	`, protectionID, normalized)
	if err == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var match RuleMatch
			match.Kind = "blocklist"
			if rows.Scan(&match.ID, &match.Name) == nil {
				decision.Blocklists = append(decision.Blocklists, match)
			}
		}
	}

	filteringActive := !protectiontime.PausedAt(pausedUntil, time.Now()) && protectiontime.ActiveAt(protectiontime.Schedule{Enabled: scheduleEnabled, Days: scheduleDays, Start: scheduleStart, End: scheduleEnd, Timezone: scheduleTimezone}, time.Now())
	switch {
	case !filteringActive:
		decision.Reason = "Filtering is currently bypassed by " + protectionName + " time controls."
	case decision.Allowlist != nil:
		decision.Reason = "An exception in " + protectionName + " bypassed filtering."
	case decision.ManualBlock != nil:
		decision.Action = "blocked"
		decision.Reason = "Matched a custom block in " + protectionName + "."
	case len(decision.Blocklists) == 1:
		decision.Action = "blocked"
		decision.Reason = "Matched the " + decision.Blocklists[0].Name + " blocklist in " + protectionName + "."
	case len(decision.Blocklists) > 1:
		decision.Action = "blocked"
		decision.Reason = fmt.Sprintf("Matched %d blocklists in %s.", len(decision.Blocklists), protectionName)
	case decision.LocalRecord != nil:
		decision.Reason = "Matched a Faro Local DNS record."
	}
	return decision
}

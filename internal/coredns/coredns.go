package coredns

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"

	"github.com/derek/faro/internal/db"
)

var reloadTotal atomic.Uint64
var reloadFailedTotal atomic.Uint64

type Manager struct {
	Store     *db.Store
	ConfigDir string
	applyMu   sync.Mutex
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
	return &Manager{Store: store, ConfigDir: configDir}
}

func (m *Manager) Apply(ctx context.Context) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	reloadTotal.Add(1)
	state, err := m.render(ctx)
	if err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	if err := os.MkdirAll(m.ConfigDir, 0o755); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}

	files := map[string][]byte{
		"Corefile":        []byte(state.Corefile),
		"faro.hosts":      []byte(state.LocalHosts + "\n" + state.BlockHosts),
		"local.hosts":     []byte(state.LocalHosts),
		"blocklist.hosts": []byte(state.BlockHosts),
	}
	for name, content := range state.ProtectionHosts {
		files[name] = []byte(content)
	}
	if err := validate(files); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	if err := replaceWithRollback(m.ConfigDir, files); err != nil {
		reloadFailedTotal.Add(1)
		return err
	}
	return nil
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

func (m *Manager) render(ctx context.Context) (renderedFiles, error) {
	settings, err := settingsMap(ctx, m.Store)
	if err != nil {
		return renderedFiles{}, err
	}
	upstreams := splitCSV(settings["upstream_dns"])
	if len(upstreams) == 0 {
		upstreams = []string{"1.1.1.1", "9.9.9.9"}
	}
	if len(upstreams) > 15 {
		return renderedFiles{}, fmt.Errorf("at most 15 upstream resolvers are supported")
	}
	for _, upstream := range upstreams {
		if net.ParseIP(upstream) == nil {
			return renderedFiles{}, fmt.Errorf("invalid upstream resolver %q", upstream)
		}
	}
	cacheEnabled := settings["dns_cache_enabled"] != "false"
	cacheTTL := 300
	if parsed, parseErr := strconv.Atoi(settings["dns_cache_ttl"]); parseErr == nil {
		cacheTTL = max(30, min(parsed, 3600))
	}
	denialTTL := min(cacheTTL, 60)
	allowedCIDRs := splitCSV(settings["allowed_client_cidrs"])
	if len(allowedCIDRs) == 0 {
		allowedCIDRs = []string{"127.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10"}
	}
	for _, allowedCIDR := range allowedCIDRs {
		if _, _, parseErr := net.ParseCIDR(allowedCIDR); parseErr != nil {
			return renderedFiles{}, fmt.Errorf("invalid allowed client CIDR %q", allowedCIDR)
		}
	}

	localHosts, err := m.localHosts(ctx)
	if err != nil {
		return renderedFiles{}, err
	}
	protections, err := m.protections(ctx)
	if err != nil {
		return renderedFiles{}, err
	}
	if len(protections) == 0 {
		return renderedFiles{}, errors.New("Home protection is missing")
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
    hosts /etc/coredns/{{ .Protection.HostsFile }} {
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
	state := renderState{Upstreams: upstreams, CacheEnabled: cacheEnabled, CacheTTL: cacheTTL, DenialTTL: denialTTL, AllowedCIDRs: allowedCIDRs}
	protectionHosts := map[string]string{}
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
		if err := blockTemplate.Execute(&core, data); err != nil {
			return renderedFiles{}, err
		}
	}
	return renderedFiles{
		Corefile:        core.String(),
		LocalHosts:      localHosts,
		BlockHosts:      defaultBlocks,
		ProtectionHosts: protectionHosts,
	}, nil
}

func (m *Manager) localHosts(ctx context.Context) (string, error) {
	rows, err := m.Store.DB.QueryContext(ctx, `SELECT hostname, type, value FROM dns_records ORDER BY hostname`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString("# Generated by Faro. Manual edits will be replaced.\n")
	for rows.Next() {
		var host, typ, value string
		if err := rows.Scan(&host, &typ, &value); err != nil {
			return "", err
		}
		if typ == "A" || typ == "AAAA" {
			fmt.Fprintf(&b, "%s %s\n", value, host)
		}
	}
	return b.String(), rows.Err()
}

func (m *Manager) blockHosts(ctx context.Context) (string, error) {
	var protectionID int64
	if err := m.Store.DB.QueryRowContext(ctx, `SELECT id FROM protection_profiles WHERE is_default = 1`).Scan(&protectionID); err != nil {
		return "", err
	}
	return m.blockHostsForProtection(ctx, protectionID)
}

func (m *Manager) blockHostsForProtection(ctx context.Context, protectionID int64) (string, error) {
	allowlist, err := domains(ctx, m.Store, `SELECT domain FROM protection_allow_entries WHERE protection_id = ?`, protectionID)
	if err != nil {
		return "", err
	}
	blocked, err := domains(ctx, m.Store, `
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
	for _, domain := range allowlist {
		allowed[domain] = struct{}{}
	}

	var b strings.Builder
	b.WriteString("# Generated by Faro. Allowlist entries are excluded.\n")
	for _, domain := range blocked {
		if _, ok := allowed[domain]; ok {
			continue
		}
		fmt.Fprintf(&b, "0.0.0.0 %s\n", domain)
	}
	return b.String(), nil
}

func (m *Manager) protections(ctx context.Context) ([]protectionRender, error) {
	rows, err := m.Store.DB.QueryContext(ctx, `SELECT id, name, is_default FROM protection_profiles ORDER BY is_default, id`)
	if err != nil {
		return nil, err
	}
	var protections []protectionRender
	for rows.Next() {
		var protection protectionRender
		if err := rows.Scan(&protection.ID, &protection.Name, &protection.IsDefault); err != nil {
			_ = rows.Close()
			return nil, err
		}
		protections = append(protections, protection)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range protections {
		protection := &protections[index]
		protection.HostsFile = fmt.Sprintf("protection-%d.hosts", protection.ID)
		protection.ClientIPs, err = domains(ctx, m.Store, `
			SELECT address FROM device_addresses a
			JOIN device_protection_memberships m ON m.device_id = a.device_id
			WHERE m.protection_id = ?
			UNION
			SELECT client_ip FROM device_protection_assignments WHERE protection_id = ?`, protection.ID, protection.ID)
		if err != nil {
			return nil, err
		}
		protection.BlockHosts, err = m.blockHostsForProtection(ctx, protection.ID)
		if err != nil {
			return nil, err
		}
	}
	return protections, nil
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
	defer rows.Close()
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
	defer rows.Close()
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

func validate(files map[string][]byte) error {
	corefile := string(files["Corefile"])
	if !strings.Contains(corefile, ".:53") || !strings.Contains(corefile, "forward .") {
		return fmt.Errorf("generated Corefile is missing required server or forward block")
	}
	// TODO: Run `coredns -conf <temp Corefile> -dns.port=0` when the binary is available in the API container.
	return nil
}

func replaceWithRollback(dir string, files map[string][]byte) error {
	backups := map[string][]byte{}
	for name := range files {
		path := filepath.Join(dir, name)
		if existing, err := os.ReadFile(path); err == nil {
			backups[name] = existing
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	written := []string{}
	for name, content := range files {
		tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
		if err != nil {
			rollback(dir, backups, written)
			return err
		}
		tmpName := tmp.Name()
		if _, err := tmp.Write(content); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			rollback(dir, backups, written)
			return err
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpName)
			rollback(dir, backups, written)
			return err
		}
		if err := os.Chmod(tmpName, 0o644); err != nil {
			_ = os.Remove(tmpName)
			rollback(dir, backups, written)
			return err
		}
		target := filepath.Join(dir, name)
		if err := os.Rename(tmpName, target); err != nil {
			_ = os.Remove(tmpName)
			rollback(dir, backups, written)
			return err
		}
		written = append(written, name)
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

func IsBlocked(ctx context.Context, store *db.Store, domain string) (bool, string) {
	decision := ExplainDomain(ctx, store, domain)
	if decision.Action != "blocked" {
		if decision.Allowlist != nil {
			return false, "allowlist"
		}
		return false, "unknown"
	}
	if decision.ManualBlock != nil {
		return true, "manual"
	}
	return true, "blocklist"
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
	var protectionID int64
	var protectionName string
	err = store.DB.QueryRowContext(ctx, `
		SELECT p.id, p.name
		FROM protection_profiles p
		LEFT JOIN device_addresses da ON da.address = ?
		LEFT JOIN device_protection_memberships m ON m.protection_id = p.id AND m.device_id = da.device_id
		LEFT JOIN device_protection_assignments legacy ON legacy.protection_id = p.id AND legacy.client_ip = ?
		WHERE m.device_id IS NOT NULL OR legacy.client_ip IS NOT NULL OR p.is_default = 1
		ORDER BY CASE WHEN m.device_id IS NOT NULL THEN 0 WHEN legacy.client_ip IS NOT NULL THEN 1 ELSE 2 END
		LIMIT 1
	`, clientIP, clientIP).Scan(&protectionID, &protectionName)
	if err != nil {
		return decision
	}
	decision.Protection = &RuleMatch{Kind: "protection", ID: protectionID, Name: protectionName}
	var allowID int64
	if err := store.DB.QueryRowContext(ctx, `SELECT id FROM protection_allow_entries WHERE protection_id = ? AND domain = ?`, protectionID, normalized).Scan(&allowID); err == nil {
		decision.Allowlist = &RuleMatch{Kind: "allowlist", ID: allowID, Name: protectionName + " exception"}
	}

	var manualID int64
	if err := store.DB.QueryRowContext(ctx, `SELECT id FROM protection_block_entries WHERE protection_id = ? AND domain = ?`, protectionID, normalized).Scan(&manualID); err == nil {
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
		defer rows.Close()
		for rows.Next() {
			var match RuleMatch
			match.Kind = "blocklist"
			if rows.Scan(&match.ID, &match.Name) == nil {
				decision.Blocklists = append(decision.Blocklists, match)
			}
		}
	}

	var local LocalRecordMatch
	if err := store.DB.QueryRowContext(ctx, `SELECT id, type, value FROM dns_records WHERE hostname = ? ORDER BY id LIMIT 1`, normalized).Scan(&local.ID, &local.Type, &local.Value); err == nil {
		decision.LocalRecord = &local
	}

	switch {
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

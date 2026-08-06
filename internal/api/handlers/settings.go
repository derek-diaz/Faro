package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/derek/faro/internal/db"
)

func (s *Handler) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.readSettings(w, r)
	case http.MethodPut:
		s.updateSettings(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (s *Handler) readSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB.QueryContext(r.Context(), `SELECT key, value, updated_at FROM settings ORDER BY key`)
	if err != nil {
		writeError(w, err)
		return
	}
	defer logActionError("close settings rows", rows.Close)
	writeRows(w, rows)
}

func (s *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var input map[string]string
	if !decode(w, r, &input) {
		return
	}
	oldUpstream := settingValue(r.Context(), s.store.DB, "upstream_dns")
	oldTransport := settingValue(r.Context(), s.store.DB, "upstream_transport")
	previous := make(map[string]*string, len(input))
	tx, err := s.store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	requiresReload := false
	for key, rawValue := range input {
		previous[key], err = previousSetting(r.Context(), tx, key)
		if err != nil {
			writeError(w, err)
			return
		}
		value, normalizeErr := normalizeSettingValue(key, rawValue)
		if normalizeErr != nil {
			writeBadRequest(w, normalizeErr)
			return
		}
		requiresReload = requiresReload || settingRequiresReload(key)
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value); err != nil {
			writeError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, err)
		return
	}
	if requiresReload && !s.applySettingsReload(w, r, previous) {
		return
	}
	s.recordUpstreamChange(r.Context(), input, oldUpstream, oldTransport)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func previousSetting(ctx context.Context, tx *sql.Tx, key string) (*string, error) {
	var oldValue string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&oldValue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &oldValue, nil
}

func normalizeSettingValue(key, value string) (string, error) {
	switch key {
	case "upstream_dns":
		return normalizeUpstreamAddresses(value)
	case "upstream_transport":
		if value != "encrypted" && value != "standard" {
			return "", errors.New("upstream_transport must be encrypted or standard")
		}
		return value, nil
	case "local_domain_suffix":
		suffix := strings.Trim(strings.TrimSpace(value), ".")
		if suffix == "" || strings.Contains(suffix, ".") {
			return "", errors.New("local_domain_suffix must be one DNS label such as home or lan")
		}
		if _, err := db.NormalizeDomain("host." + suffix); err != nil {
			return "", errors.New("local_domain_suffix must be a valid DNS label")
		}
		return strings.ToLower(suffix), nil
	case "retention_days":
		days, err := strconv.Atoi(value)
		if err != nil || days < 1 || days > 3650 {
			return "", errors.New("retention_days must be between 1 and 3650")
		}
		return value, nil
	case "dns_cache_ttl":
		ttl, err := strconv.Atoi(value)
		if err != nil || ttl < 30 || ttl > 3600 {
			return "", errors.New("dns_cache_ttl must be between 30 and 3600 seconds")
		}
		return value, nil
	case "faro_lan_ip":
		ip := net.ParseIP(strings.TrimSpace(value))
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
			return "", errors.New("faro_lan_ip must be a usable LAN IP address")
		}
		return value, nil
	case "allowed_client_cidrs":
		return normalizeClientCIDRs(value)
	case "favicon_fetching_enabled", "dns_cache_enabled", "onboarding_completed":
		if value != "true" && value != "false" {
			return "", fmt.Errorf("%s must be true or false", key)
		}
		return value, nil
	default:
		return "", fmt.Errorf("unknown setting %q", key)
	}
}

func settingRequiresReload(key string) bool {
	switch key {
	case "upstream_dns", "upstream_transport", "local_domain_suffix", "dns_cache_enabled", "dns_cache_ttl", "allowed_client_cidrs":
		return true
	default:
		return false
	}
}

func (s *Handler) applySettingsReload(w http.ResponseWriter, r *http.Request, previous map[string]*string) bool {
	if err := s.reloader.Apply(r.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(r.Context())
		s.restoreSettings(rollbackCtx, previous)
		_ = s.reloader.Apply(rollbackCtx)
		s.recordEvent(r.Context(), eventInput{
			Type:        "dns.reload_failed",
			Severity:    "critical",
			Title:       "DNS reload failed",
			Description: err.Error(),
			Source:      "settings",
		})
		writeError(w, err)
		return false
	}
	s.recordEvent(r.Context(), eventInput{
		Type:        "dns.reload",
		Severity:    "success",
		Title:       "DNS reloaded",
		Description: "Configuration successfully reloaded.",
		Source:      "settings",
	})
	return true
}

func (s *Handler) recordUpstreamChange(ctx context.Context, input map[string]string, oldUpstream, oldTransport string) {
	nextUpstream, upstreamChanged := input["upstream_dns"]
	nextTransport, transportChanged := input["upstream_transport"]
	upstreamChanged = upstreamChanged && strings.TrimSpace(nextUpstream) != strings.TrimSpace(oldUpstream)
	transportChanged = transportChanged && strings.TrimSpace(nextTransport) != strings.TrimSpace(oldTransport)
	if !upstreamChanged && !transportChanged {
		return
	}
	if !upstreamChanged {
		nextUpstream = oldUpstream
	}
	if !transportChanged {
		nextTransport = oldTransport
	}
	s.recordEvent(ctx, eventInput{
		Type:        "upstream.changed",
		Severity:    "info",
		Title:       "Upstreams changed",
		Description: "DNS providers or connection privacy were updated.",
		Metadata: map[string]any{
			"from":           oldUpstream,
			"to":             nextUpstream,
			"transport_from": oldTransport,
			"transport_to":   nextTransport,
		},
		Source: "settings",
	})
	if s.upstreams != nil {
		s.upstreams.Trigger()
	}
}

func normalizeClientCIDRs(value string) (string, error) {
	seen := map[string]bool{}
	var normalized []string
	for _, raw := range strings.Split(value, ",") {
		candidate := strings.TrimSpace(raw)
		if candidate == "" || seen[candidate] {
			continue
		}
		_, network, err := net.ParseCIDR(candidate)
		if err != nil {
			return "", errors.New("allowed_client_cidrs must contain valid comma-separated CIDR ranges")
		}
		canonical := network.String()
		if !seen[canonical] {
			seen[canonical] = true
			normalized = append(normalized, canonical)
		}
	}
	if len(normalized) == 0 {
		return "", errors.New("allowed_client_cidrs must contain at least one CIDR range")
	}
	return strings.Join(normalized, ","), nil
}

func normalizeUpstreamAddresses(value string) (string, error) {
	seen := map[string]bool{}
	var normalized []string
	for _, raw := range strings.Split(value, ",") {
		address := net.ParseIP(strings.TrimSpace(raw))
		if address == nil {
			return "", errors.New("upstream_dns must contain valid comma-separated IPv4 or IPv6 addresses")
		}
		canonical := address.String()
		if !seen[canonical] {
			seen[canonical] = true
			normalized = append(normalized, canonical)
		}
	}
	if len(normalized) == 0 || len(normalized) > 15 {
		return "", errors.New("upstream_dns must contain between 1 and 15 addresses")
	}
	return strings.Join(normalized, ","), nil
}

func (s *Handler) restoreSettings(ctx context.Context, previous map[string]*string) {
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	for key, value := range previous {
		if value == nil {
			_, _ = tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
			continue
		}
		_, _ = tx.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`, key, *value)
	}
	_ = tx.Commit()
}

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
		rows, err := s.store.DB.QueryContext(r.Context(), `SELECT key, value, updated_at FROM settings ORDER BY key`)
		if err != nil {
			writeError(w, err)
			return
		}
		defer rows.Close()
		writeRows(w, rows)
	case http.MethodPut:
		s.configMu.Lock()
		defer s.configMu.Unlock()
		var input map[string]string
		if !decode(w, r, &input) {
			return
		}
		oldUpstream := settingValue(r.Context(), s.store.DB, "upstream_dns")
		oldTransport := settingValue(r.Context(), s.store.DB, "upstream_transport")
		previous := map[string]*string{}
		requiresReload := false
		tx, err := s.store.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback()
		for key, value := range input {
			switch key {
			case "upstream_dns", "upstream_transport", "local_domain_suffix", "faro_lan_ip", "retention_days", "favicon_fetching_enabled", "dns_cache_enabled", "dns_cache_ttl", "allowed_client_cidrs", "onboarding_completed":
				var oldValue string
				if scanErr := tx.QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key = ?`, key).Scan(&oldValue); scanErr == nil {
					copyValue := oldValue
					previous[key] = &copyValue
				} else if scanErr == sql.ErrNoRows {
					previous[key] = nil
				} else {
					writeError(w, scanErr)
					return
				}
				if key == "onboarding_completed" && value != "true" && value != "false" {
					writeBadRequest(w, errors.New("onboarding_completed must be true or false"))
					return
				}
				if key == "retention_days" {
					days, parseErr := strconv.Atoi(value)
					if parseErr != nil || days < 1 || days > 3650 {
						writeBadRequest(w, errors.New("retention_days must be between 1 and 3650"))
						return
					}
				}
				if key == "dns_cache_enabled" && value != "true" && value != "false" {
					writeBadRequest(w, errors.New("dns_cache_enabled must be true or false"))
					return
				}
				if key == "dns_cache_ttl" {
					ttl, parseErr := strconv.Atoi(value)
					if parseErr != nil || ttl < 30 || ttl > 3600 {
						writeBadRequest(w, errors.New("dns_cache_ttl must be between 30 and 3600 seconds"))
						return
					}
				}
				if key == "faro_lan_ip" {
					ip := net.ParseIP(strings.TrimSpace(value))
					if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
						writeBadRequest(w, errors.New("faro_lan_ip must be a usable LAN IP address"))
						return
					}
				}
				if key == "upstream_dns" {
					normalized, upstreamErr := normalizeUpstreamAddresses(value)
					if upstreamErr != nil {
						writeBadRequest(w, upstreamErr)
						return
					}
					value = normalized
				}
				if key == "upstream_transport" && value != "encrypted" && value != "standard" {
					writeBadRequest(w, errors.New("upstream_transport must be encrypted or standard"))
					return
				}
				if key == "local_domain_suffix" {
					suffix := strings.Trim(strings.TrimSpace(value), ".")
					if suffix == "" || strings.Contains(suffix, ".") {
						writeBadRequest(w, errors.New("local_domain_suffix must be one DNS label such as home or lan"))
						return
					}
					if _, normalizeErr := db.NormalizeDomain("host." + suffix); normalizeErr != nil {
						writeBadRequest(w, errors.New("local_domain_suffix must be a valid DNS label"))
						return
					}
					value = strings.ToLower(suffix)
				}
				if key == "allowed_client_cidrs" {
					normalized, cidrErr := normalizeClientCIDRs(value)
					if cidrErr != nil {
						writeBadRequest(w, cidrErr)
						return
					}
					value = normalized
				}
				if key == "upstream_dns" || key == "upstream_transport" || key == "local_domain_suffix" || key == "dns_cache_enabled" || key == "dns_cache_ttl" || key == "allowed_client_cidrs" {
					requiresReload = true
				}
				if _, err := tx.ExecContext(r.Context(), `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value); err != nil {
					writeError(w, err)
					return
				}
			default:
				writeBadRequest(w, fmt.Errorf("unknown setting %q", key))
				return
			}
		}
		if err := tx.Commit(); err != nil {
			writeError(w, err)
			return
		}
		if requiresReload {
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
				return
			}
			s.recordEvent(r.Context(), eventInput{
				Type:        "dns.reload",
				Severity:    "success",
				Title:       "DNS reloaded",
				Description: "Configuration successfully reloaded.",
				Source:      "settings",
			})
		}
		nextUpstream, upstreamChanged := input["upstream_dns"]
		nextTransport, transportChanged := input["upstream_transport"]
		upstreamChanged = upstreamChanged && strings.TrimSpace(nextUpstream) != strings.TrimSpace(oldUpstream)
		transportChanged = transportChanged && strings.TrimSpace(nextTransport) != strings.TrimSpace(oldTransport)
		if upstreamChanged || transportChanged {
			if !upstreamChanged {
				nextUpstream = oldUpstream
			}
			if !transportChanged {
				nextTransport = oldTransport
			}
			s.recordEvent(r.Context(), eventInput{
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
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
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
	defer tx.Rollback()
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

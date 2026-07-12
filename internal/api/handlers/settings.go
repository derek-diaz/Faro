package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
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
		var input map[string]string
		if !decode(w, r, &input) {
			return
		}
		oldUpstream := settingValue(r.Context(), s.store.DB, "upstream_dns")
		tx, err := s.store.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback()
		for key, value := range input {
			switch key {
			case "upstream_dns", "local_domain_suffix", "retention_days", "favicon_fetching_enabled", "dns_cache_enabled", "dns_cache_ttl":
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
				if _, err := tx.ExecContext(r.Context(), `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value); err != nil {
					writeError(w, err)
					return
				}
			}
		}
		if err := tx.Commit(); err != nil {
			writeError(w, err)
			return
		}
		if err := s.reloader.Apply(r.Context()); err != nil {
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
		if nextUpstream, ok := input["upstream_dns"]; ok && strings.TrimSpace(nextUpstream) != strings.TrimSpace(oldUpstream) {
			s.recordEvent(r.Context(), eventInput{
				Type:        "upstream.changed",
				Severity:    "info",
				Title:       "Upstreams changed",
				Description: "DNS upstream servers were updated.",
				Metadata:    map[string]any{"from": oldUpstream, "to": nextUpstream},
				Source:      "settings",
			})
		}
		s.recordEvent(r.Context(), eventInput{
			Type:        "dns.reload",
			Severity:    "success",
			Title:       "DNS reloaded",
			Description: "Configuration successfully reloaded.",
			Source:      "settings",
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

package handlers

import (
	"fmt"
	"github.com/derek/faro/internal/coredns"
	"net/http"
)

func (s *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "faro-api"})
}

func (s *Handler) reload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := s.reloader.Apply(r.Context()); err != nil {
		s.recordEvent(r.Context(), eventInput{
			Type:        "dns.reload_failed",
			Severity:    "critical",
			Title:       "DNS reload failed",
			Description: err.Error(),
			Source:      "dns",
		})
		writeError(w, err)
		return
	}
	s.recordEvent(r.Context(), eventInput{
		Type:        "dns.reload",
		Severity:    "success",
		Title:       "DNS reloaded",
		Description: "Configuration successfully reloaded.",
		Source:      "dns",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	reloads, reloadFailures := coredns.ReloadTotals()
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries`)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE action = 'blocked'`)
	cacheHits := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE source = 'cache'`)
	upstreamQueries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE source = 'upstream'`)
	cache := s.coreDNSCacheMetrics(r.Context())
	enabled := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklists WHERE enabled = 1`)
	entries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklist_entries`)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_queries_total counter\nfaro_dns_queries_total %d\n", total)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_blocked_queries_total counter\nfaro_dns_blocked_queries_total %d\n", blocked)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_cache_hits_total counter\nfaro_dns_cache_hits_total %d\n", cacheHits)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_upstream_queries_total counter\nfaro_dns_upstream_queries_total %d\n", upstreamQueries)
	_, _ = fmt.Fprintf(w, "# TYPE faro_dns_cache_entries gauge\nfaro_dns_cache_entries %.0f\n", cache.entries)
	_, _ = fmt.Fprintf(w, "# TYPE faro_blocklists_enabled_total gauge\nfaro_blocklists_enabled_total %d\n", enabled)
	_, _ = fmt.Fprintf(w, "# TYPE faro_blocklist_entries_total gauge\nfaro_blocklist_entries_total %d\n", entries)
	_, _ = fmt.Fprintf(w, "# TYPE faro_coredns_reload_total counter\nfaro_coredns_reload_total %d\n", reloads)
	_, _ = fmt.Fprintf(w, "# TYPE faro_coredns_reload_failed_total counter\nfaro_coredns_reload_failed_total %d\n", reloadFailures)
}

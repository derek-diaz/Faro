package handlers

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
	faroversion "github.com/derek/faro/internal/version"
)

func (handler *Handler) health(responseWriter http.ResponseWriter, _ *http.Request) {
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true, "service": "faro-api", "version": faroversion.Display})
}

func (handler *Handler) version(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]string{
		"name":    "Faro",
		"version": faroversion.Number,
		"display": faroversion.Display,
	})
}

func (handler *Handler) versionCheck(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	var latest *faroversion.Release
	if handler.releaseChecker != nil {
		latest = handler.releaseChecker.Latest(request.Context())
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"name":    "Faro",
		"version": faroversion.Number,
		"display": faroversion.Display,
		"latest":  latest,
	})
}

func (handler *Handler) upgrade(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	state, err := db.ReadUpgradeState(handler.store.Path)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"application_version": faroversion.Number,
		"schema_version":      db.CurrentSchemaVersion,
		"state":               state,
	})
}

type coreDNSDiagnosticsProvider interface {
	Diagnostics(context.Context) (coredns.Diagnostics, error)
}

func (handler *Handler) corednsDiagnostics(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	provider, ok := handler.reloader.(coreDNSDiagnosticsProvider)
	if !ok {
		writeJSON(responseWriter, http.StatusServiceUnavailable, map[string]string{"error": "CoreDNS diagnostics are unavailable"})
		return
	}
	diagnostics, err := provider.Diagnostics(request.Context())
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, diagnostics)
}

func (handler *Handler) reload(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	if err := handler.reloader.Apply(request.Context()); err != nil {
		handler.recordEvent(request.Context(), eventInput{
			Type:        "dns.reload_failed",
			Severity:    "critical",
			Title:       "DNS reload failed",
			Description: err.Error(),
			Source:      "dns",
		})
		writeError(responseWriter, err)
		return
	}
	handler.recordEvent(request.Context(), eventInput{
		Type:        "dns.reload",
		Severity:    "success",
		Title:       "DNS reloaded",
		Description: "Configuration successfully reloaded.",
		Source:      "dns",
	})
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
}

func (handler *Handler) metrics(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	reloads, reloadFailures := coredns.ReloadTotals()
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	history, err := handler.cachedHistoryMetrics(ctx)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	total, blocked, cacheHits, upstreamQueries, enabled, entries := history.total, history.blocked, history.cacheHits, history.upstreamQueries, history.enabled, history.entries
	cache, _ := handler.cachedCoreDNSCacheMetrics()
	responseWriter.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_dns_queries_total gauge\nfaro_dns_queries_total %d\n", total)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_dns_blocked_queries_total gauge\nfaro_dns_blocked_queries_total %d\n", blocked)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_dns_cache_hits_total gauge\nfaro_dns_cache_hits_total %d\n", cacheHits)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_dns_upstream_queries_total gauge\nfaro_dns_upstream_queries_total %d\n", upstreamQueries)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_dns_cache_entries gauge\nfaro_dns_cache_entries %.0f\n", cache.entries)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_blocklists_enabled_total gauge\nfaro_blocklists_enabled_total %d\n", enabled)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_blocklist_entries_total gauge\nfaro_blocklist_entries_total %d\n", entries)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_coredns_reload_total counter\nfaro_coredns_reload_total %d\n", reloads)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_coredns_reload_failed_total counter\nfaro_coredns_reload_failed_total %d\n", reloadFailures)
	stats := handler.store.DB.Stats()
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_database_connections_in_use gauge\nfaro_database_connections_in_use %d\n", stats.InUse)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_database_connection_waits_total counter\nfaro_database_connection_waits_total %d\n", stats.WaitCount)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_database_connection_wait_seconds_total counter\nfaro_database_connection_wait_seconds_total %g\n", stats.WaitDuration.Seconds())
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_history_metrics_age_seconds gauge\nfaro_history_metrics_age_seconds %g\n", max(0, time.Since(history.expiresAt.Add(-15*time.Second)).Seconds()))
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_api_heap_alloc_bytes gauge\nfaro_api_heap_alloc_bytes %d\n", memory.HeapAlloc)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_api_heap_inuse_bytes gauge\nfaro_api_heap_inuse_bytes %d\n", memory.HeapInuse)
	_, _ = fmt.Fprintf(responseWriter, "# TYPE faro_api_goroutines gauge\nfaro_api_goroutines %d\n", runtime.NumGoroutine())

}

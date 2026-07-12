package handlers

import (
	"context"
	"net/http"
	"os"

	"github.com/derek/faro/internal/blocklists"
	"github.com/derek/faro/internal/db"
)

type CoreDNSManager interface {
	Apply(context.Context) error
}

type Handler struct {
	store      *db.Store
	reloader   CoreDNSManager
	refresher  blocklists.Refresher
	faviconDir string
	metricsURL string
}

func New(store *db.Store, reloader CoreDNSManager) http.Handler {
	handler := &Handler{
		store:      store,
		reloader:   reloader,
		refresher:  blocklists.Refresher{Store: store},
		faviconDir: env("FARO_FAVICON_DIR", "/data/favicons"),
		metricsURL: env("FARO_COREDNS_METRICS_URL", "http://coredns:9153/metrics"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handler.health)
	mux.HandleFunc("/metrics", handler.metrics)
	mux.HandleFunc("/api/dns-records", handler.dnsRecords)
	mux.HandleFunc("/api/dns-records/", handler.dnsRecord)
	mux.HandleFunc("/api/blocklists", handler.blocklists)
	mux.HandleFunc("/api/blocklists/", handler.blocklist)
	mux.HandleFunc("/api/allowlist", handler.allowlist)
	mux.HandleFunc("/api/allowlist/", handler.allowlistEntry)
	mux.HandleFunc("/api/blocklist-domains", handler.manualBlocklist)
	mux.HandleFunc("/api/blocklist-domains/", handler.manualBlockEntry)
	mux.HandleFunc("/api/queries", handler.queries)
	mux.HandleFunc("/api/events", handler.events)
	mux.HandleFunc("/api/notifications", handler.notifications)
	mux.HandleFunc("/api/upstreams/probe", handler.upstreamProbes)
	mux.HandleFunc("/api/devices", handler.devices)
	mux.HandleFunc("/api/devices/", handler.device)
	mux.HandleFunc("/api/domains/", handler.domainSummary)
	mux.HandleFunc("/api/search", handler.search)
	mux.HandleFunc("/api/dashboard", handler.dashboard)
	mux.HandleFunc("/api/favicons/", handler.favicon)
	mux.HandleFunc("/api/settings", handler.settings)
	mux.HandleFunc("/api/reload", handler.reload)
	return cors(mux)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

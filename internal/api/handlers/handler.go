package handlers

import (
	"context"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/derek/faro/internal/auth"
	farobackup "github.com/derek/faro/internal/backup"
	"github.com/derek/faro/internal/blocklists"
	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/upstreamhealth"
)

type CoreDNSManager interface {
	Apply(context.Context) error
}

type Handler struct {
	store        *db.Store
	reloader     CoreDNSManager
	refresher    blocklists.Refresher
	deviceNames  *deviceNameResolver
	faviconDir   string
	faviconLocks [32]sync.Mutex
	metricsURL   string
	upstreams    *upstreamhealth.Monitor
	backups      *farobackup.Service
	startedAt    time.Time
}

func New(store *db.Store, reloader CoreDNSManager, upstreams *upstreamhealth.Monitor) http.Handler {
	authManager := auth.NewManager(store)
	handler := &Handler{
		store:       store,
		reloader:    reloader,
		refresher:   blocklists.Refresher{Store: store},
		deviceNames: newDeviceNameResolver(),
		faviconDir:  env("FARO_FAVICON_DIR", "/data/favicons"),
		metricsURL:  env("FARO_COREDNS_METRICS_URL", "http://coredns:9153/metrics"),
		upstreams:   upstreams,
		backups:     farobackup.NewService(store),
		startedAt:   time.Now(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handler.health)
	mux.HandleFunc("/metrics", handler.metrics)
	mux.HandleFunc("/api/auth/status", authManager.Status)
	mux.HandleFunc("/api/auth/setup", authManager.Setup)
	mux.HandleFunc("/api/auth/login", authManager.Login)
	mux.HandleFunc("/api/auth/logout", authManager.Logout)
	mux.HandleFunc("/api/auth/password", authManager.ChangePassword)
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
	mux.HandleFunc("/api/notifications/", handler.notificationState)
	mux.HandleFunc("/api/upstreams/probe", handler.upstreamProbes)
	mux.HandleFunc("/api/devices", handler.devices)
	mux.HandleFunc("/api/devices/", handler.device)
	mux.HandleFunc("/api/domains/", handler.domainSummary)
	mux.HandleFunc("/api/search", handler.search)
	mux.HandleFunc("/api/dashboard", handler.dashboard)
	mux.HandleFunc("/api/favicons/", handler.favicon)
	mux.HandleFunc("/api/settings", handler.settings)
	mux.HandleFunc("/api/maintenance", handler.maintenance)
	mux.HandleFunc("/api/backups", handler.backupExport)
	mux.HandleFunc("/api/backups/restore", handler.backupRestore)
	mux.HandleFunc("/api/reload", handler.reload)
	return cors(authManager.Require(mux))
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

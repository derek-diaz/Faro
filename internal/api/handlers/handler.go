package handlers

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/derek/faro/internal/auth"
	farobackup "github.com/derek/faro/internal/backup"
	"github.com/derek/faro/internal/blocklists"
	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/devicecatalog"
	"github.com/derek/faro/internal/integrations/unifi"
	"github.com/derek/faro/internal/redundancy"
	"github.com/derek/faro/internal/upstreamhealth"
	faroversion "github.com/derek/faro/internal/version"
)

type CoreDNSManager interface {
	Apply(context.Context) error
}

type Handler struct {
	store               *db.Store
	reloader            CoreDNSManager
	refresher           blocklists.Refresher
	deviceNames         *deviceNameResolver
	deviceCatalog       *devicecatalog.Manager
	faviconDir          string
	faviconLocks        [32]sync.Mutex
	metricsURL          string
	upstreams           *upstreamhealth.Monitor
	backups             *farobackup.Service
	unifi               *unifi.Manager
	classifier          *devicecatalog.Classifier
	redundancy          *redundancy.Manager
	startedAt           time.Time
	releaseChecker      *faroversion.Checker
	configMu            sync.Mutex
	activityCountsMu    sync.Mutex
	activityCountsCache map[string]activityCountCacheEntry
	dashboardMu         sync.Mutex
	dashboardCache      dashboardCacheEntry
}

func New(store *db.Store, reloader CoreDNSManager, upstreams *upstreamhealth.Monitor, dependencies ...any) http.Handler {
	authManager := auth.NewManager(store)
	var unifiManager *unifi.Manager
	var classifier *devicecatalog.Classifier
	var redundancyManager *redundancy.Manager
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case *unifi.Manager:
			unifiManager = value
		case *devicecatalog.Classifier:
			classifier = value
		case *redundancy.Manager:
			redundancyManager = value
		}
	}
	catalog := devicecatalog.NewManager(env("FARO_DEVICE_CATALOG_PATH", ""))
	if classifier != nil {
		catalog = classifier.Catalog()
	}
	handler := &Handler{
		store:          store,
		reloader:       reloader,
		refresher:      blocklists.Refresher{Store: store},
		deviceNames:    newDeviceNameResolver(),
		deviceCatalog:  catalog,
		faviconDir:     env("FARO_FAVICON_DIR", "/data/favicons"),
		metricsURL:     env("FARO_COREDNS_METRICS_URL", "http://coredns:9153/metrics"),
		upstreams:      upstreams,
		backups:        farobackup.NewService(store),
		unifi:          unifiManager,
		classifier:     classifier,
		redundancy:     redundancyManager,
		startedAt:      time.Now(),
		releaseChecker: faroversion.NewChecker(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handler.health)
	mux.HandleFunc("/api/version", handler.version)
	mux.HandleFunc("/api/version/check", handler.versionCheck)
	mux.HandleFunc("/api/upgrade", handler.upgrade)
	mux.HandleFunc("/api/diagnostics/coredns", handler.corednsDiagnostics)
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
	mux.HandleFunc("/api/protections", handler.protections)
	mux.HandleFunc("/api/protections/", handler.protection)
	mux.HandleFunc("/api/allowlist", handler.allowlist)
	mux.HandleFunc("/api/allowlist/", handler.allowlistEntry)
	mux.HandleFunc("/api/blocklist-domains", handler.manualBlocklist)
	mux.HandleFunc("/api/blocklist-domains/", handler.manualBlockEntry)
	mux.HandleFunc("/api/queries", handler.queries)
	mux.HandleFunc("/api/events", handler.events)
	mux.HandleFunc("/api/notifications", handler.notifications)
	mux.HandleFunc("/api/notifications/", handler.notificationState)
	mux.HandleFunc("/api/upstreams/catalog", handler.upstreamCatalog)
	mux.HandleFunc("/api/upstreams/probe", handler.upstreamProbes)
	mux.HandleFunc("/api/devices", handler.devices)
	mux.HandleFunc("/api/devices/", handler.device)
	mux.HandleFunc("/api/device-catalog", handler.deviceCatalogInfo)
	mux.HandleFunc("/api/domains/", handler.domainSummary)
	mux.HandleFunc("/api/search", handler.search)
	mux.HandleFunc("/api/dashboard", handler.dashboard)
	mux.HandleFunc("/api/favicons/", handler.favicon)
	mux.HandleFunc("/api/settings", handler.settings)
	mux.HandleFunc("/api/integrations/unifi", handler.unifiIntegration)
	mux.HandleFunc("/api/integrations/unifi/test", handler.unifiTest)
	mux.HandleFunc("/api/integrations/unifi/sync", handler.unifiSync)
	mux.HandleFunc("/api/maintenance", handler.maintenance)
	mux.HandleFunc("/api/backups", handler.backupExport)
	mux.HandleFunc("/api/backups/restore", handler.backupRestore)
	mux.HandleFunc("/api/reload", handler.reload)
	mux.HandleFunc("/api/redundancy/public", handler.redundancyPublic)
	mux.HandleFunc("/api/redundancy", handler.redundancyStatus)
	mux.HandleFunc("/api/redundancy/pairing", handler.redundancyPairing)
	mux.HandleFunc("/api/redundancy/join", handler.redundancyJoin)
	mux.HandleFunc("/api/redundancy/pair", handler.redundancyPair)
	mux.HandleFunc("/api/redundancy/replica/snapshot", handler.redundancySnapshot)
	mux.HandleFunc("/api/redundancy/replica/ack", handler.redundancyAck)
	mux.HandleFunc("/api/redundancy/nodes/", handler.redundancyNode)
	trustProxy := strings.EqualFold(os.Getenv("FARO_TRUST_PROXY"), "true")
	return cors(trustProxy, authManager.OnboardingComplete, replicaReadOnly(store, authManager.Require(mux)))
}

func replicaReadOnly(store *db.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
			next.ServeHTTP(responseWriter, request)
			return
		}
		// Leaving redundancy is the one authenticated local mutation a replica
		// must be able to perform for recovery or reuse as a standalone server.
		if request.Method == http.MethodDelete && request.URL.Path == "/api/redundancy" {
			next.ServeHTTP(responseWriter, request)
			return
		}
		if request.Method == http.MethodPost && (request.URL.Path == "/api/auth/login" || request.URL.Path == "/api/auth/logout") {
			next.ServeHTTP(responseWriter, request)
			return
		}
		var role string
		if err := store.DB.QueryRowContext(request.Context(), `SELECT role FROM redundancy_state WHERE id = 1`).Scan(&role); err != nil {
			writeJSON(responseWriter, http.StatusInternalServerError, map[string]string{"error": "could not verify this Faro server's role"})
			return
		}
		if role == redundancy.RoleReplica {
			writeJSON(responseWriter, http.StatusConflict, map[string]string{"error": "replica servers are read-only and managed by the primary Faro server"})
			return
		}
		next.ServeHTTP(responseWriter, request)
	})
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

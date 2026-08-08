package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/derek/faro/internal/api"
	"github.com/derek/faro/internal/blocklists"
	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/devicecatalog"
	"github.com/derek/faro/internal/dohproxy"
	"github.com/derek/faro/internal/integrations/unifi"
	"github.com/derek/faro/internal/querylog"
	"github.com/derek/faro/internal/redundancy"
	"github.com/derek/faro/internal/retention"
	"github.com/derek/faro/internal/upstreamhealth"
)

func main() {
	dbPath := env("FARO_DB_PATH", "/data/faro.db")
	configDir := env("FARO_COREDNS_CONFIG_DIR", "/coredns")
	logPath := env("FARO_COREDNS_LOG_PATH", "/var/log/coredns/query.log")
	addr := env("FARO_API_ADDR", ":8080")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Fatalf("create db dir: %v", err)
	}

	store, err := db.Open(dbPath)
	if err != nil {
		var incompatible *db.IncompatibleVersionError
		var migration *db.MigrationError
		switch {
		case errors.As(err, &incompatible):
			log.Fatalf("database upgrade blocked by incompatible schema: %v; inspect %s", err, upgradeStatePath(dbPath))
		case errors.As(err, &migration):
			log.Fatalf("database upgrade failed: %v; inspect %s", err, upgradeStatePath(dbPath))
		default:
			log.Fatalf("open db: %v; inspect %s", err, upgradeStatePath(dbPath))
		}
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close db: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dohRuntimePath := env("FARO_DOH_RUNTIME_PATH", filepath.Join(filepath.Dir(dbPath), "faro-doh.json"))
	encryptedDNS := dohproxy.New(store, dohproxy.DefaultAddress)

	reloader := coredns.NewManager(store, configDir)
	reloader.BeforeApply = encryptedDNS.Reload
	reloader.RollbackApply = encryptedDNS.RestorePrevious
	secretKeyPath := env("FARO_SECRET_KEY_PATH", filepath.Join(filepath.Dir(dbPath), "faro-secrets.key"))
	redundancyManager := redundancy.NewManager(store, reloader, configDir, secretKeyPath)
	reloader.CommitApply = func(ctx context.Context) error {
		return dohproxy.WriteRuntimeConfigFromStore(ctx, dohRuntimePath, store)
	}
	reloader.AfterApply = func(ctx context.Context) {
		redundancyManager.ConfigurationApplied(ctx)
	}
	if err := reloader.Apply(context.Background()); err != nil {
		log.Printf("initial coredns render failed: %v", err)
	}
	go redundancyManager.Run(ctx)

	tailer := querylog.NewTailer(store, logPath)
	go tailer.Run(ctx)
	retentionManager := retention.NewManager(store)
	go retentionManager.Run(ctx)
	blocklistManager := blocklists.NewManager(store, reloader.Apply)
	go blocklistManager.Run(ctx)
	upstreamMonitor := upstreamhealth.NewMonitor(store, upstreamhealth.DefaultInterval, nil)
	go upstreamMonitor.Run(ctx)
	unifiManager := unifi.NewManager(store, secretKeyPath)
	go unifiManager.Run(ctx)
	deviceCatalog := devicecatalog.NewManager(env("FARO_DEVICE_CATALOG_PATH", ""))
	deviceClassifier := devicecatalog.NewClassifier(store, deviceCatalog)
	go deviceClassifier.Run(ctx)

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.NewServer(store, reloader, upstreamMonitor, unifiManager, deviceClassifier, redundancyManager),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shut down api server: %v", err)
		}
	}()

	log.Printf("faro api listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("api server: %v", err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func upgradeStatePath(dbPath string) string {
	if value := os.Getenv("FARO_UPGRADE_STATE_PATH"); value != "" {
		return value
	}
	return filepath.Join(filepath.Dir(dbPath), "faro-upgrade.json")
}

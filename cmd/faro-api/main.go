package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
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
	if err := run(); err != nil {
		log.Printf("api server: %v", err)
		os.Exit(1)
	}
}

func run() error {
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
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	startWorker := func(run func(context.Context)) {
		workers.Add(1)
		go func() { defer workers.Done(); run(workerCtx) }()
	}
	defer func() { cancelWorkers(); workers.Wait() }()

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
	startWorker(reloader.RunTemporalReloads)
	startWorker(redundancyManager.Run)

	tailer := querylog.NewTailer(store, logPath)
	startWorker(tailer.Run)
	retentionManager := retention.NewManager(store)
	startWorker(retentionManager.Run)
	blocklistManager := blocklists.NewManager(store, reloader.Apply)
	startWorker(blocklistManager.Run)
	upstreamMonitor := upstreamhealth.NewMonitor(store, upstreamhealth.DefaultInterval, nil)
	startWorker(upstreamMonitor.Run)
	unifiManager := unifi.NewManager(store, secretKeyPath)
	startWorker(unifiManager.Run)
	deviceCatalog := devicecatalog.NewManager(env("FARO_DEVICE_CATALOG_PATH", ""))
	deviceClassifier := devicecatalog.NewClassifier(store, deviceCatalog)
	startWorker(deviceClassifier.Run)

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.NewServer(store, reloader, upstreamMonitor, unifiManager, deviceClassifier, redundancyManager),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	log.Printf("faro api listening on %s", addr)
	// Returning runs worker cleanup before the deferred database close.
	return serveUntilStopped(ctx, srv, srv.ListenAndServe)
}

// The caller must not close shared resources until Shutdown has finished.
func serveUntilStopped(ctx context.Context, srv *http.Server, serve func() error) error {
	stopped := make(chan error, 1)
	go func() { stopped <- serve() }()
	select {
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = srv.Close()
	}
	serveErr := <-stopped
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(shutdownErr, serveErr)
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

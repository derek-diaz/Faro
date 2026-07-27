package main

import (
	"context"
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
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	encryptedDNS := dohproxy.New(store, dohproxy.DefaultAddress)
	if err := encryptedDNS.Start(ctx); err != nil {
		log.Fatalf("start encrypted DNS gateway: %v", err)
	}

	reloader := coredns.NewManager(store, configDir)
	reloader.BeforeApply = encryptedDNS.Reload
	if err := reloader.Apply(context.Background()); err != nil {
		log.Printf("initial coredns render failed: %v", err)
	}

	tailer := querylog.NewTailer(store, logPath)
	go tailer.Run(ctx)
	retentionManager := retention.NewManager(store)
	go retentionManager.Run(ctx)
	blocklistManager := blocklists.NewManager(store, reloader.Apply)
	go blocklistManager.Run(ctx)
	upstreamMonitor := upstreamhealth.NewMonitor(store, upstreamhealth.DefaultInterval, nil)
	go upstreamMonitor.Run(ctx)
	unifiManager := unifi.NewManager(store, env("FARO_SECRET_KEY_PATH", filepath.Join(filepath.Dir(dbPath), "faro-secrets.key")))
	go unifiManager.Run(ctx)
	deviceCatalog := devicecatalog.NewManager(env("FARO_DEVICE_CATALOG_PATH", ""))
	deviceClassifier := devicecatalog.NewClassifier(store, deviceCatalog)
	go deviceClassifier.Run(ctx)

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.NewServer(store, reloader, upstreamMonitor, unifiManager, deviceClassifier),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("faro api listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("api server: %v", err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

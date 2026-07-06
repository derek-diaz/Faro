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
	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/querylog"
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

	reloader := coredns.NewManager(store, configDir)
	if err := reloader.Apply(context.Background()); err != nil {
		log.Printf("initial coredns render failed: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tailer := querylog.NewTailer(store, logPath)
	go tailer.Run(ctx)

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.NewServer(store, reloader),
		ReadHeaderTimeout: 5 * time.Second,
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

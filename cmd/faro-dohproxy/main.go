package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/dohproxy"
)

const runtimeReloadInterval = 2 * time.Second

func main() {
	dbPath := env("FARO_DB_PATH", "/config/faro.db")
	runtimePath := env("FARO_DOH_RUNTIME_PATH", filepath.Join(filepath.Dir(dbPath), "faro-doh.json"))
	address := env("FARO_DOH_ADDR", dohproxy.DefaultAddress)

	store, err := db.OpenReadOnly(dbPath)
	if err != nil {
		log.Fatalf("open Faro database read-only: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	config, err := initialRuntimeConfig(ctx, store, runtimePath)
	if err != nil {
		log.Fatalf("load accepted encrypted DNS configuration: %v", err)
	}
	proxy := dohproxy.New(nil, address)
	if err := proxy.StartWithConfig(ctx, config); err != nil {
		log.Fatalf("start encrypted DNS gateway: %v", err)
	}

	go watchRuntimeConfig(ctx, proxy, runtimePath, config.Generation)
	<-ctx.Done()
}

func initialRuntimeConfig(ctx context.Context, store *db.Store, path string) (dohproxy.RuntimeConfig, error) {
	config, err := dohproxy.ReadRuntimeConfig(path)
	if err == nil {
		return config, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return dohproxy.RuntimeConfig{}, err
	}
	// The first API apply creates the accepted snapshot. This fallback only
	// covers a fresh volume before that file exists.
	return dohproxy.RuntimeConfigFromStore(ctx, store)
}

func watchRuntimeConfig(ctx context.Context, proxy *dohproxy.Proxy, path, generation string) {
	ticker := time.NewTicker(runtimeReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			config, err := dohproxy.ReadRuntimeConfig(path)
			if err != nil {
				log.Printf("encrypted DNS runtime configuration is not usable; keeping last-known-good gateway: %v", err)
				continue
			}
			if config.Generation == generation {
				continue
			}
			if err := proxy.ReloadConfig(config); err != nil {
				log.Printf("encrypted DNS runtime reload rejected; keeping last-known-good gateway: %v", err)
				continue
			}
			generation = config.Generation
		}
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

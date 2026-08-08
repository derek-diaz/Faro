package dohproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/derek/faro/internal/db"
)

// RuntimeConfig is the last configuration accepted by Faro's CoreDNS apply
// transaction. The generation changes on every accepted apply so the
// standalone gateway can notice a successful control-plane commit without
// watching uncommitted database edits.
type RuntimeConfig struct {
	Transport   string `json:"transport"`
	UpstreamDNS string `json:"upstream_dns"`
	Generation  string `json:"generation"`
}

func RuntimeConfigFromStore(ctx context.Context, store *db.Store) (RuntimeConfig, error) {
	if store == nil || store.DB == nil {
		return RuntimeConfig{}, errors.New("DNS runtime configuration requires a database")
	}
	transport, addresses, err := configuredTransport(ctx, store)
	if err != nil {
		return RuntimeConfig{}, err
	}
	return RuntimeConfig{
		Transport:   transport,
		UpstreamDNS: strings.Join(addresses, ","),
	}, nil
}

func (config RuntimeConfig) validate() (string, []string, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		transport = "standard"
	}
	if transport != "standard" && transport != "encrypted" {
		return "", nil, fmt.Errorf("invalid upstream transport %q", transport)
	}
	addresses := runtimeAddresses(config.UpstreamDNS)
	if len(addresses) == 0 || len(addresses) > 15 {
		return "", nil, errors.New("DNS runtime configuration requires between 1 and 15 upstream addresses")
	}
	for _, address := range addresses {
		if net.ParseIP(address) == nil {
			return "", nil, fmt.Errorf("invalid upstream resolver %q", address)
		}
	}
	if transport == "encrypted" {
		if _, err := EndpointsForAddresses(addresses); err != nil {
			return "", nil, err
		}
	}
	return transport, addresses, nil
}

func runtimeAddresses(value string) []string {
	addresses := make([]string, 0)
	for _, raw := range strings.Split(value, ",") {
		if address := strings.TrimSpace(raw); address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

func ReadRuntimeConfig(path string) (RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RuntimeConfig{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config RuntimeConfig
	if err := decoder.Decode(&config); err != nil {
		return RuntimeConfig{}, fmt.Errorf("decode DNS runtime configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return RuntimeConfig{}, errors.New("DNS runtime configuration has trailing data")
		}
		return RuntimeConfig{}, fmt.Errorf("decode DNS runtime configuration: %w", err)
	}
	if strings.TrimSpace(config.Generation) == "" {
		return RuntimeConfig{}, errors.New("DNS runtime configuration has no generation")
	}
	if _, _, err := config.validate(); err != nil {
		return RuntimeConfig{}, err
	}
	return config, nil
}

// WriteRuntimeConfig atomically publishes a new accepted runtime snapshot.
// The temporary file is created beside the target so a reader sees either
// the previous complete JSON document or the new complete document.
func WriteRuntimeConfig(path string, config RuntimeConfig) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("DNS runtime configuration path is required")
	}
	if strings.TrimSpace(config.Generation) == "" {
		config.Generation = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if _, _, err := config.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(config)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		// Linux containers replace the target atomically. Windows does not allow
		// Rename over an existing file, so use the safest available fallback for
		// local development there.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		if retryErr := os.Rename(temporaryName, path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func WriteRuntimeConfigFromStore(ctx context.Context, path string, store *db.Store) error {
	config, err := RuntimeConfigFromStore(ctx, store)
	if err != nil {
		return err
	}
	config.Generation = fmt.Sprintf("%d", time.Now().UnixNano())
	return WriteRuntimeConfig(path, config)
}

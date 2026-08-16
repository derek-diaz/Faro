package handlers

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
)

func TestDeviceReplaySanitizesNonFiniteLatency(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	deviceID, err := deviceidentity.ResolveAddress(context.Background(), store, "192.168.1.99", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source, latency_ms)
		VALUES(?, '192.168.1.99', ?, 'apple.example', 'A', 'allowed', 'upstream', ?)`, time.Now().UTC().Format(time.RFC3339), deviceID, math.Inf(1)); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{store: store, deviceNames: newDeviceNameResolver()}
	response := httptest.NewRecorder()
	handler.device(response, httptest.NewRequest(http.MethodGet, "/api/devices/192.168.1.99/replay?range=24h", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("replay status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Events []struct {
			Latency any `json:"latency_ms"`
		} `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("replay response is not valid JSON: %v; body=%s", err, response.Body.String())
	}
	if len(payload.Events) != 1 || payload.Events[0].Latency != nil {
		t.Fatalf("unexpected replay events: %#v", payload.Events)
	}
}

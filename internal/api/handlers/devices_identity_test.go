package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
)

func TestDevicesCombineCorrelatedAddresses(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'A', '192.168.1.20');
		INSERT INTO dns_records(hostname, type, value) VALUES('nas.home', 'AAAA', '2001:db8::20')`); err != nil {
		t.Fatal(err)
	}
	deviceID, err := deviceidentity.ResolveAddress(context.Background(), store, "192.168.1.20", "dns")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deviceidentity.ResolveAddress(context.Background(), store, "2001:db8::20", "dns"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := store.DB.Exec(`
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source) VALUES(?, '192.168.1.20', ?, 'one.example', 'A', 'allowed', 'upstream');
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source) VALUES(?, '2001:db8::20', ?, 'two.example', 'AAAA', 'blocked', 'blocklist')`, now, deviceID, now, deviceID); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{store: store, deviceNames: newDeviceNameResolver()}
	response := httptest.NewRecorder()
	handler.devices(response, httptest.NewRequest(http.MethodGet, "/api/devices", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", response.Code, response.Body.String())
	}
	var items []struct {
		DeviceID    int64    `json:"device_id"`
		Addresses   []string `json:"addresses"`
		Total       int      `json:"total_queries_today"`
		Blocked     int      `json:"blocked_queries_today"`
		DisplayName string   `json:"display_name"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].DeviceID != deviceID || len(items[0].Addresses) != 2 || items[0].Total != 2 || items[0].Blocked != 1 {
		t.Fatalf("unexpected stable device summary: %#v", items)
	}
	if items[0].DisplayName != "nas" {
		t.Fatalf("display name = %q, want nas", items[0].DisplayName)
	}
}

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

func TestDeviceInventoryIsPaginatedAndConditionallyCached(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for index := 1; index <= 118; index++ {
		result, err := store.DB.Exec(`
			INSERT INTO devices(name, first_seen_at, last_seen_at)
			VALUES(?, ?, ?)
		`, fmt.Sprintf("Device %03d", index), now, now)
		if err != nil {
			t.Fatal(err)
		}
		deviceID, _ := result.LastInsertId()
		address := fmt.Sprintf("10.0.%d.%d", index/250, index%250+1)
		if _, err := store.DB.Exec(`
			INSERT INTO device_addresses(device_id, address, family, source, confidence)
			VALUES(?, ?, 'ipv4', 'dns', 'observed')
		`, deviceID, address); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.Exec(`
			INSERT INTO dns_records(hostname, type, value, description)
			VALUES(?, 'A', ?, 'test device')
		`, fmt.Sprintf("device-%03d.home", index), address); err != nil {
			t.Fatal(err)
		}
	}

	handler := &Handler{store: store, deviceNames: newDeviceNameResolver()}
	request := httptest.NewRequest(http.MethodGet, "/api/devices?format=page&page=2&page_size=50&sort=device&direction=asc", nil)
	response := httptest.NewRecorder()
	handler.devices(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("inventory status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Items      []map[string]any `json:"items"`
		Page       int              `json:"page"`
		Total      int              `json:"total"`
		TotalPages int              `json:"total_pages"`
		Summary    struct {
			Observed int `json:"observed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 50 || payload.Page != 2 || payload.Total != 118 || payload.TotalPages != 3 || payload.Summary.Observed != 118 {
		t.Fatalf("inventory page = %#v, item count %d", payload, len(payload.Items))
	}
	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("inventory response did not include an ETag")
	}

	conditional := httptest.NewRequest(http.MethodGet, request.URL.String(), nil)
	conditional.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	handler.devices(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional inventory status = %d body=%q", notModified.Code, notModified.Body.String())
	}
}

func TestDeviceInventoryReadDoesNotClassifyDevices(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := store.DB.Exec(`INSERT INTO devices(name) VALUES('Test device')`)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, _ := result.LastInsertId()
	if _, err := store.DB.Exec(`
		INSERT INTO device_addresses(device_id, address, family, source, confidence)
		VALUES(?, '10.0.0.5', 'ipv4', 'dns', 'observed')
	`, deviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value) VALUES('test.home', 'A', '10.0.0.5')`); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{store: store, deviceNames: newDeviceNameResolver()}
	response := httptest.NewRecorder()
	handler.devices(response, httptest.NewRequest(http.MethodGet, "/api/devices?format=page", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("inventory status = %d body=%s", response.Code, response.Body.String())
	}
	var classifications int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM device_classifications`).Scan(&classifications); err != nil {
		t.Fatal(err)
	}
	if classifications != 0 {
		t.Fatalf("GET /api/devices wrote %d classifications", classifications)
	}
}

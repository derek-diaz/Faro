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

	// A client's day boundary affects the totals even without a database write.
	conditional.Header.Set("X-Faro-Timezone", "America/La_Paz")
	changedDay := httptest.NewRecorder()
	handler.devices(changedDay, conditional)
	if changedDay.Code != http.StatusOK {
		t.Fatalf("different day boundary returned %d, want a fresh page", changedDay.Code)
	}

	for page, wantItems := range map[int]int{1: 1, 2: 0} {
		filtered := httptest.NewRecorder()
		path := fmt.Sprintf("/api/devices?page=%d&page_size=10&search=Device+118", page)
		handler.devices(filtered, httptest.NewRequest(http.MethodGet, path, nil))
		if err := json.Unmarshal(filtered.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Total != 1 || len(payload.Items) != wantItems {
			t.Fatalf("filtered page %d: total=%d items=%d", page, payload.Total, len(payload.Items))
		}
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

func TestDNSHistoryInventoryUsesRecordedQueries(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	const lastQuery = "2020-02-03T04:05:06Z"
	if _, err := h.store.DB.Exec(`UPDATE devices SET name='Quiet client', last_seen_at=CURRENT_TIMESTAMP WHERE id=?;
		INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(?,?,?,'quiet.example','A','allowed','upstream');
		INSERT INTO devices(name,last_seen_at) VALUES('Imported client',CURRENT_TIMESTAMP)`, input.DeviceID, lastQuery, input.ClientIP, input.DeviceID); err != nil {
		t.Fatal(err)
	}
	read := func(query string) (int, []map[string]any, string) {
		t.Helper()
		w := httptest.NewRecorder()
		h.devices(w, httptest.NewRequest(http.MethodGet, "/api/devices?format=page&"+query, nil))
		var p struct {
			Total int
			Items []map[string]any
		}
		if w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p.Total, p.Items, w.Header().Get("ETag")
	}
	total, rows, historyTag := read("with_history=true")
	if total != 1 || len(rows) != 1 || rows[0]["name"] != "Quiet client" || rows[0]["last_seen"] != lastQuery || rows[0]["total_queries_today"] != float64(0) {
		t.Fatalf("DNS history view: %#v", rows)
	}
	if total, _, _ := read("active_today=true"); total != 0 {
		t.Fatalf("active today = %d", total)
	}
	total, rows, allTag := read("with_history=false")
	if total != 2 || len(rows) != 2 || rows[0]["name"] != "Imported client" || rows[0]["last_seen"] != nil || allTag == historyTag {
		t.Fatalf("all known devices: %#v, total=%d", rows, total)
	}
	if _, err := h.store.DB.Exec(`
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source)
		SELECT ?, '192.0.2.20', id, 'new.example', 'A', 'allowed', 'upstream' FROM devices WHERE name = 'Imported client'
	`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	total, _, beforePurge := read("with_history=true")
	if total != 2 {
		t.Fatalf("after the imported client's first request, got %d devices", total)
	}
	if _, err := h.store.DB.Exec(`DELETE FROM dns_queries WHERE timestamp = ?`, lastQuery); err != nil {
		t.Fatal(err)
	}
	total, rows, afterPurge := read("with_history=true")
	if total != 1 || len(rows) != 1 || rows[0]["name"] != "Imported client" || beforePurge == afterPurge {
		t.Fatalf("after history expired: %#v, total=%d", rows, total)
	}
}

func TestDeviceInventoryCanFilterToActiveDevicesToday(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var activeDeviceID int64
	for index, name := range []string{"Quiet device", "Active device", "Another quiet device"} {
		result, err := store.DB.Exec(`INSERT INTO devices(name) VALUES(?)`, name)
		if err != nil {
			t.Fatal(err)
		}
		deviceID, _ := result.LastInsertId()
		if index == 1 {
			activeDeviceID = deviceID
		}
		if _, err := store.DB.Exec(`
			INSERT INTO device_addresses(device_id, address, family, source, confidence)
			VALUES(?, ?, 'ipv4', 'unifi', 'observed')
		`, deviceID, fmt.Sprintf("10.0.0.%d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB.Exec(`
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, latency_ms, device_id)
		VALUES(?, '10.0.0.2', 'example.com', 'A', 'allowed', 'upstream', 1, ?)
	`, time.Now().UTC().Format(time.RFC3339), activeDeviceID); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{store: store, deviceNames: newDeviceNameResolver()}
	request := httptest.NewRequest(http.MethodGet, "/api/devices?format=page&active_today=true", nil)
	response := httptest.NewRecorder()
	handler.devices(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("inventory status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Items   []map[string]any `json:"items"`
		Total   int              `json:"total"`
		Summary struct {
			Observed    int `json:"observed"`
			ActiveToday int `json:"active_today"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Total != 1 {
		t.Fatalf("active inventory returned total=%d items=%d, want 1", payload.Total, len(payload.Items))
	}
	if payload.Items[0]["name"] != "Active device" {
		t.Fatalf("active inventory returned %#v", payload.Items[0])
	}
	if payload.Summary.Observed != 3 || payload.Summary.ActiveToday != 1 {
		t.Fatalf("inventory summary = %#v, want observed=3 active_today=1", payload.Summary)
	}
}

func TestAddresslessHistoryDoesNotUseDatabaseIDOrNewOwnersName(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	if _, err := h.store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(?,?,?,'quiet.example','A','allowed','upstream');
		DELETE FROM device_addresses WHERE device_id=?;
		INSERT INTO dns_records(hostname,type,value) VALUES('new-owner.home','A',?)`, time.Now().UTC().Format(time.RFC3339), input.ClientIP, input.DeviceID, input.DeviceID, input.ClientIP); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.devices(w, httptest.NewRequest(http.MethodGet, "/api/devices?format=page&with_history=true", nil))
	var page struct {
		Items   []map[string]any
		Summary deviceInventorySummary
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatal(w.Body.String())
	}
	item := page.Items[0]
	if item["display_name"] != "Unnamed device" || item["client_ip"] != "" || item["last_query_ip"] != input.ClientIP || page.Summary.MostActiveName != "Unnamed device" {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	h.device(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/devices/id:%d", input.DeviceID), nil))
	var detail map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["display_name"] != "Unnamed device" || detail["client_ip"] != "" {
		t.Fatal(w.Body.String())
	}
}

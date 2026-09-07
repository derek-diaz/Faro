package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDashboardCountsAndRanksStableDevices(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	if _, err := h.store.DB.Exec(`UPDATE devices SET name='Office laptop' WHERE id=?`, input.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DB.Exec(`INSERT INTO device_addresses(device_id,address,family,source,confidence) VALUES(?,'2001:db8::10','ipv6','dns','observed')`, input.DeviceID); err != nil {
		t.Fatal(err)
	}
	result, err := h.store.DB.Exec(`INSERT INTO devices(name) VALUES('Runner up')`)
	if err != nil {
		t.Fatal(err)
	}
	otherID, _ := result.LastInsertId()
	if _, err := h.store.DB.Exec(`INSERT INTO device_addresses(device_id,address,family,source,confidence) VALUES(?,'192.0.2.20','ipv4','dns','observed')`, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DB.Exec(`INSERT INTO devices(name) VALUES('Inactive device')`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		ip    string
		id    int64
		count int
	}{{input.ClientIP, input.DeviceID, 6}, {"2001:db8::10", input.DeviceID, 6}, {"192.0.2.20", otherID, 8}} {
		for i := 0; i < row.count; i++ {
			if _, err := h.store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(?,?,?,'example.test','A','allowed','upstream')`, time.Now().UTC().Format(time.RFC3339Nano), row.ip, row.id); err != nil {
				t.Fatal(err)
			}
		}
	}
	response := httptest.NewRecorder()
	h.dashboard(response, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var report struct {
		Active   int `json:"active_devices_today"`
		Observed int `json:"observed_devices"`
		Top      []struct {
			Label    string
			Count    int
			DeviceID int64  `json:"device_id"`
			ClientIP string `json:"client_ip"`
		} `json:"top_clients"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Active != 2 || report.Observed != 3 || len(report.Top) != 2 {
		t.Fatalf("wrong device counts: %s", response.Body.String())
	}
	if report.Top[0].DeviceID != input.DeviceID || report.Top[0].Count != 12 || report.Top[0].Label != "Office laptop" || report.Top[0].ClientIP == "" {
		t.Fatalf("dashboard did not aggregate device addresses: %#v", report.Top)
	}
	inventory, err := h.inventorySummary(httptest.NewRequest(http.MethodGet, "/api/devices", nil), todayStart(httptest.NewRequest(http.MethodGet, "/", nil)))
	if err != nil {
		t.Fatal(err)
	}
	if inventory.ActiveToday != report.Active || inventory.MostActiveName != report.Top[0].Label {
		t.Fatalf("dashboard disagrees with Devices: %#v", inventory)
	}
	recent := recentQueries(context.Background(), h.store.DB)
	if len(recent) == 0 || recent[0]["device_name"] != "Runner up" {
		t.Fatalf("recent activity lost friendly name: %#v", recent)
	}
}

func TestDomainSummarySeparatesHistoryFromCurrentPolicy(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	if _, err := h.store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(?,?,?,'broken.example','A','blocked','manual')`, time.Now().UTC().Format(time.RFC3339Nano), input.ClientIP, input.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DB.Exec(`INSERT INTO protection_allow_entries(protection_id,domain) SELECT id,'broken.example' FROM protection_profiles WHERE is_default=1`); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.domainSummary(response, httptest.NewRequest(http.MethodGet, "/api/domains/broken.example/summary", nil))
	var report struct {
		Status    string
		HomeAllow bool                    `json:"home_allow_exception"`
		Decision  struct{ Action string } `json:"current_home_decision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "Blocked" || report.Decision.Action != "allowed" || !report.HomeAllow {
		t.Fatalf("history and current rule confused: %s", response.Body.String())
	}
}

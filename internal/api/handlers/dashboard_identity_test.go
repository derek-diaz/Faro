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
	recent, err := recentQueries(context.Background(), h.store.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) == 0 || recent[0]["device_name"] != "Runner up" {
		t.Fatalf("recent activity lost friendly name: %#v", recent)
	}
}

func TestWhatsNewDomainsUsesFirstVisit(t *testing.T) {
	h, _, _ := troubleshootingFixture(t)
	for _, visit := range []struct{ domain, timestamp string }{
		{"returning.example", "2026-03-01T23:59:59Z"},
		{"returning.example", "2026-03-02T12:00:00Z"},
		{"early.example", "2026-03-02T00:00:00Z"},
		{"early.example", "2026-03-02T13:00:00Z"},
		{"later.example", "2026-03-02T10:00:00Z"},
	} {
		if _, err := h.store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,domain,query_type,action,source)
			VALUES(?, '192.0.2.1', ?, 'A', 'allowed', 'cache')`, visit.timestamp, visit.domain); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := h.whatsNew(httptest.NewRequest(http.MethodGet, "/api/dashboard", nil), "2026-03-02T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	domains := recent["domains"].([]map[string]any)
	if len(domains) != 2 || domains[0]["label"] != "later.example" || domains[1]["label"] != "early.example" {
		t.Fatalf("new domains = %v", domains)
	}
}

func TestWhatsNewLinksEachNewDeviceOnce(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	_, err := h.store.DB.Exec(`
		UPDATE devices SET first_seen_at = '2026-03-01T12:00:00Z' WHERE id = ?;
		INSERT INTO device_addresses(device_id,address,family,first_seen_at) VALUES(?, '192.0.2.55', 'ipv4', '2026-03-02T12:00:00Z');
		INSERT INTO devices(id,name,first_seen_at) VALUES(77, 'Office laptop', '2026-03-02T12:00:00Z'), (78, '', '2026-03-02T10:00:00Z');
		INSERT INTO device_names(device_id,source,name) VALUES(77,'unifi','factory-name');
		INSERT INTO device_addresses(device_id,address,family,last_seen_at) VALUES
			(77, '192.0.2.77', 'ipv4', '2026-03-02T16:00:00Z'), (77, '2001:db8::77', 'ipv6', '2026-03-02T17:00:00Z'),
			(78, '192.0.2.78', 'ipv4', '2026-03-02T16:00:00Z');
		INSERT INTO dns_records(id,hostname,type,value,created_at) VALUES(88,'kitchen.home.arpa','A','192.0.2.78','2026-03-02 12:30:00');
		INSERT INTO blocklists(id,name,url,created_at) VALUES(99,'Office filter','https://example.test/office','2026-03-02 12:35:00');
	`, input.DeviceID, input.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	recent, err := h.whatsNew(httptest.NewRequest(http.MethodGet, "/api/dashboard", nil), "2026-03-02T04:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	devices := recent["devices"].([]map[string]any)
	if len(devices) != 2 || devices[0]["device_id"] != int64(77) || devices[0]["label"] != "Office laptop" ||
		devices[0]["subtitle"] != "2001:db8::77" || devices[1]["device_id"] != int64(78) || devices[1]["label"] != "kitchen" {
		t.Fatalf("new devices = %v", devices)
	}
	records, lists := recent["local_records"].([]map[string]any), recent["blocklists"].([]map[string]any)
	if len(records) != 1 || records[0]["id"] != int64(88) || len(lists) != 1 || lists[0]["id"] != int64(99) {
		t.Fatalf("new records = %v, new blocklists = %v", records, lists)
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

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derek/faro/internal/db"
)

func TestProtectionPreview(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reloader := &testReloader{}
	handler := &Handler{store: store, reloader: reloader}
	exec := func(statement string) {
		t.Helper()
		if _, err := store.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	exec(`
		INSERT INTO protection_profiles(id,name) VALUES(42,'Living room');
		INSERT INTO devices(id,name) VALUES(7,'Living room TV'),(8,'Laptop');
		INSERT INTO device_addresses(device_id,address,family) VALUES(7,'192.0.2.7','ipv4'),(7,'2001:db8::7','ipv6'),(8,'192.0.2.8','ipv4');
		INSERT INTO device_protection_memberships(device_id,protection_id) VALUES(7,42);
		INSERT INTO device_protection_assignments(client_ip,protection_id) VALUES('192.0.2.42',42),('192.0.2.7',42);
		INSERT INTO blocklists(id,name,url,enabled) VALUES(10,'Current','one',1),(20,'Proposed','two',1),(30,'Disabled','three',0);
		INSERT INTO protection_blocklists(protection_id,blocklist_id) VALUES(42,10);
		INSERT INTO blocklist_entries(blocklist_id,domain) VALUES(10,'already.test'),(20,'already.test'),(20,'ads.test'),
			(20,'allowed.test'),(30,'disabled.test'),(20,'trial.test'),(10,'removed.test');
		INSERT INTO protection_block_entries(protection_id,domain) VALUES(42,'old.test');
		INSERT INTO troubleshooting_exceptions(token,client_ip,device_id,protection_id,domain,expires_at)
			VALUES('test','192.0.2.7',7,42,'trial.test',datetime('now','+10 minutes'));
		WITH traffic(domain,ip,device,age) AS (VALUES
			('ADS.TEST.','192.0.2.7',7,'-1 minute'),('ads.test','2001:db8::7',7,'-2 minutes'),
			('ads.test','192.0.2.42',NULL,'-3 minutes'),('already.test','192.0.2.7',7,'-1 minute'),
			('allowed.test','192.0.2.7',7,'-1 minute'),('new.test','192.0.2.7',NULL,'-1 minute'),
			('old.test','192.0.2.7',7,'-1 minute'),('removed.test','192.0.2.7',7,'-1 minute'),
			('disabled.test','192.0.2.7',7,'-1 minute'),('trial.test','192.0.2.7',7,'-1 minute'),
			('ads.test','192.0.2.7',7,'-25 hours'),('ads.test','192.0.2.8',8,'-1 minute'),
			('ads.test','192.0.2.7',8,'-1 minute'))
		INSERT INTO dns_queries(timestamp,domain,client_ip,device_id,query_type,source,action)
			SELECT datetime('now', age),domain,ip,device,'A','upstream','allowed' FROM traffic;`)
	const draft = `{"blocklist_ids":[20,30],"allow_domains":[" ALLOWED.TEST. "],"block_domains":["new.test"]}`
	request := func(method, path, body string, status int) rulePreview {
		t.Helper()
		response := httptest.NewRecorder()
		handler.protection(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		if response.Code != status {
			t.Fatalf("%s %s: status %d, body %s", method, path, response.Code, response.Body.String())
		}
		var got rulePreview
		if status == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
		}
		return got
	}
	const path = "/api/protections/42/preview"
	got := request("POST", path, draft, 200)
	if got.Requests != 10 || got.Truncated || got.NewlyBlocked.Total != 4 || got.NewlyAllowed.Total != 2 || got.StillBlocked.Total != 1 || got.Devices.Total != 2 {
		t.Fatalf("unexpected counts: %+v", got)
	}
	if got.NewlyBlocked.Items[0] != (previewItem{Label: "ads.test", Count: 3}) || got.Devices.Items[0] != (previewItem{Label: "Living room TV", Address: "192.0.2.7", Count: 5}) {
		t.Fatalf("unexpected rankings: %+v", got)
	}
	home := request("POST", "/api/protections/1/preview", draft, 200)
	if home.Requests != 2 || home.NewlyBlocked.Total != 2 || home.Devices.Total != 1 || home.Devices.Items[0].Label != "Laptop" {
		t.Fatalf("unexpected Home scope: %+v", home)
	}
	request("POST", path, `{"allow_domains":["site.test"],"block_domains":["site.test"]}`, 400)
	request("POST", path, `{"blocklist_ids":[999]}`, 400)
	request("POST", "/api/protections/999/preview", draft, 400)
	request("GET", path, "", 405)
	if reloader.calls != 0 || scalarInt(context.Background(), store.DB, `SELECT count(*) FROM protection_blocklists WHERE protection_id=42 AND blocklist_id=10`) != 1 {
		t.Fatal("preview changed the saved protection")
	}
	exec(`DELETE FROM dns_queries`)
	empty := request("POST", path, draft, 200)
	if empty.Requests != 0 || empty.Truncated || empty.Devices.Items == nil || len(empty.StillBlocked.Items) != 0 {
		t.Fatalf("unexpected empty preview: %+v", empty)
	}

	// Timestamp ties use newest ID first. The omitted request must not enter counts or ranks.
	exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<7)
		INSERT INTO dns_queries(timestamp,domain,client_ip,device_id,query_type,source,action)
		SELECT CURRENT_TIMESTAMP,'site-'||i||'.test','192.0.2.7',7,'A','upstream','allowed' FROM n;`)
	rules, err := handler.normalizeProtectionRules(context.Background(), protectionInput{BlockDomains: []string{
		"site-1.test", "site-2.test", "site-3.test", "site-4.test", "site-5.test", "site-6.test", "site-7.test",
	}})
	if err != nil {
		t.Fatal(err)
	}
	clipped, err := compareProtectionRules(context.Background(), store.DB, 42, rules, 6)
	if err != nil || !clipped.Truncated || clipped.Requests != 6 || clipped.NewlyBlocked.Total != 6 || len(clipped.NewlyBlocked.Items) != 5 || clipped.NewlyBlocked.Items[0].Label != "site-2.test" {
		t.Fatalf("unexpected limited preview: %+v, %v", clipped, err)
	}
}

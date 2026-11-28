package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

func TestActivitySummaryCountsAndTimelinesShareSearchAndTimeBounds(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		INSERT INTO devices(id, name) VALUES(1, 'Kitchen sensor');
		INSERT INTO device_addresses(device_id, address, family, first_seen_at)
		VALUES(1, '192.0.2.1', 'ipv4', '2026-07-12 12:00:00');
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source) VALUES
		('2026-07-12T12:00:00Z', '192.0.2.1', 1, 'a.test', 'A', 'blocked', 'cache'),
		('2026-07-12T12:00:01Z', '192.0.2.1', 1, 'b.test', 'A', 'allowed', 'cache'),
		('2026-07-12T12:01:00Z', '192.0.2.1', 1, 'c.test', 'A', 'blocked', 'upstream'),
		('2026-07-12T12:02:00Z', '192.0.2.1', 1, 'd.test', 'A', 'blocked', 'manual'),
		('2026-07-12T12:03:00Z', '192.0.2.1', 1, 'outside.test', 'A', 'allowed', 'upstream'),
		('2026-07-12T12:01:00Z', '192.0.2.2', NULL, 'unmatched.test', 'A', 'allowed', 'upstream');
		INSERT INTO events(timestamp, type, severity, title, description, source)
		VALUES('2026-07-12T12:01:08Z', 'setting.changed', 'info', 'Kitchen updated', '', 'faro');
	`); err != nil {
		t.Fatal(err)
	}
	h := &Handler{store: store}
	counts := map[string]int{"all": 6, "dns": 4, "cache": 2, "upstream": 1, "blocked": 3, "system": 2}
	for _, check := range []struct {
		scope          string
		total, blocked [3]int
	}{
		{"all", [3]int{3, 2, 1}, [3]int{1, 1, 1}},
		{"dns", [3]int{2, 1, 1}, [3]int{1, 1, 1}},
		{"cache", [3]int{2, 0, 0}, [3]int{1, 0, 0}},
		{"upstream", [3]int{0, 1, 0}, [3]int{0, 1, 0}},
		{"blocked", [3]int{1, 1, 1}, [3]int{1, 1, 1}},
		{"system", [3]int{1, 1, 0}, [3]int{}},
	} {
		t.Run(check.scope, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.events(response, httptest.NewRequest(http.MethodGet, "/api/events?detail=summary&range=custom&from=2026-07-12T12:00:00Z&to=2026-07-12T12:03:00Z&search=kitchen&scope="+check.scope, nil))
			var result struct {
				Counts   map[string]int
				Total    int
				Timeline activityTimeline
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK {
				t.Fatalf("summary: %s (%v)", response.Body.String(), err)
			}
			if !reflect.DeepEqual(result.Counts, counts) || result.Total != counts[check.scope] || len(result.Timeline.Buckets) != 3 {
				t.Fatalf("unexpected totals for %s: %s", check.scope, response.Body.String())
			}
			for i, bucket := range result.Timeline.Buckets {
				if bucket.Total != check.total[i] || bucket.Blocked != check.blocked[i] {
					t.Errorf("bucket %d has %d total and %d blocked, want %d and %d", i, bucket.Total, bucket.Blocked, check.total[i], check.blocked[i])
				}
			}
		})
	}
}

func TestActivitySummaryFractionalBucketBoundaries(t *testing.T) {
	h, _, device := troubleshootingFixture(t)
	_, err := h.store.DB.Exec(`
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source) VALUES
		('2026-01-01T12:00:00.124Z', ?1, ?2, 'early.test', 'A', 'allowed', 'upstream'),
		('2026-01-01T12:00:00.125Z', ?1, ?2, 'at-start.test', 'A', 'allowed', 'upstream'),
		('2026-01-01T12:01:00.124Z', ?1, ?2, 'before-bucket.test', 'A', 'allowed', 'cache'),
		('2026-01-01T12:01:00.125Z', ?1, ?2, 'at-bucket.test', 'A', 'blocked', 'cache'),
		('2026-01-01T12:02:00.125Z', ?1, ?2, 'at-end.test', 'A', 'blocked', 'cache');
		INSERT INTO events(timestamp, type, severity, title, description, source)
		VALUES('2026-01-01T12:01:00.125Z', 'setting.changed', 'info', 'Updated', '', 'faro');
	`, device.ClientIP, device.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 12, 0, 0, 125000000, time.UTC)
	window, err := newActivityWindow(from, from.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := loadActivitySummary(context.Background(), h.store.DB, "", window)
	if err != nil {
		t.Fatal(err)
	}
	want := []activityTimelineBucket{
		{Timestamp: "2026-01-01T12:00:00.125Z", Total: 2},
		{Timestamp: "2026-01-01T12:01:00.125Z", Total: 2, Blocked: 1},
	}
	if got := summary.timeline("all").Buckets; !reflect.DeepEqual(got, want) || summary.counts["all"] != 4 {
		t.Errorf("unexpected timeline buckets %v and counts %v", got, summary.counts)
	}
}

func TestFirstSeenActivityAndNotificationsIncludeLegacyAddressesOnce(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		INSERT INTO devices(id,name) VALUES(1,'Desk');
		INSERT INTO device_addresses(device_id,address,family,first_seen_at) VALUES
		(1,'192.0.2.1','ipv4','2026-01-01T01:00:00Z'),
		(1,'192.0.2.2','ipv4','2026-01-01T02:00:00Z');
		INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES
		('2026-01-02T01:00:00Z','192.0.2.1',1,'a.test','A','allowed','cache'),
		('2026-01-03T01:00:00Z','192.0.2.1',1,'b.test','A','allowed','cache'),
		('2026-01-02T01:00:00Z','2001:db8::1',1,'a.test','A','allowed','cache'),
		('2026-01-01T03:00:00Z','2001:db8::1',NULL,'b.test','A','allowed','cache');
	`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	items, err := activityEvents(ctx, store.DB, 10, "", "system")
	if err != nil {
		t.Fatal(err)
	}
	bell, err := notificationCandidates(ctx, store.DB, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(items, bell) || len(items) != 2 {
		t.Fatalf("activity = %v, notification candidates = %v", items, bell)
	}
	for i, want := range []struct{ ip, ts string }{{"2001:db8::1", "2026-01-01T03:00:00Z"}, {"192.0.2.1", "2026-01-01T01:00:00Z"}} {
		if row := items[i]; row["client_ip"] != want.ip || row["timestamp"] != want.ts || row["device_name"] != "Desk" {
			t.Errorf("unexpected first seen event: %v", row)
		}
	}
}

func TestActivitySearchUsesEffectiveDeviceName(t *testing.T) {
	h, _, device := troubleshootingFixture(t)
	_, err := h.store.DB.Exec(`
		UPDATE devices SET name = '' WHERE id = ?;
		INSERT INTO device_names(device_id, source, name) VALUES(?, 'unifi', 'Office tablet');
		INSERT INTO dns_records(hostname, value) VALUES('shadowed.test', ?), ('living-room.test', '192.0.2.250');
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source) VALUES
		('2026-01-01T00:01:00Z', ?, ?, 'lookup.test', 'A', 'allowed', 'cache'),
		('2026-01-01T00:01:01Z', '192.0.2.250', NULL, 'lookup.test', 'A', 'allowed', 'cache');
	`, device.DeviceID, device.DeviceID, device.ClientIP, device.ClientIP, device.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for term, expected := range map[string]string{"TABLET": "Office tablet", "living-room": "living-room.test", "shadowed": ""} {
		records, err := activityRecords(ctx, h.store.DB, 50, 0, term, "dns", activityWindow{})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := loadActivitySummary(ctx, h.store.DB, term, activityWindow{})
		if err != nil {
			t.Fatal(err)
		}
		if summary.counts["dns"] != len(records) || (expected == "" && len(records) != 0) ||
			(expected != "" && (len(records) != 1 || records[0].deviceName != expected)) {
			t.Errorf("search %q matched records %+v and counts %v", term, records, summary.counts)
		}
	}
}

func TestEventsPaginationUsesFullActivityHistory(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for index := 1; index <= 5; index++ {
		source := "upstream"
		action := "allowed"
		if index%2 == 0 {
			source = "cache"
		}
		if index == 5 {
			action = "blocked"
		}
		_, err := store.DB.Exec(`
			INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source)
			VALUES(?, '192.168.7.20', ?, 'A', ?, ?)
		`, fmt.Sprintf("2026-07-12T12:00:%02dZ", index), fmt.Sprintf("domain%d.example", index), action, source)
		if err != nil {
			t.Fatal(err)
		}
	}

	handler := &Handler{store: store}
	request := httptest.NewRequest(http.MethodGet, "/api/events?scope=dns&page=2&page_size=2", nil)
	response := httptest.NewRecorder()
	handler.events(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []struct {
			Domain string `json:"domain"`
		} `json:"items"`
		Page       int            `json:"page"`
		PageSize   int            `json:"page_size"`
		Total      int            `json:"total"`
		TotalPages int            `json:"total_pages"`
		Counts     map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Page != 2 || payload.PageSize != 2 || payload.Total != 5 || payload.TotalPages != 3 {
		t.Fatalf("unexpected pagination: %#v", payload)
	}
	if len(payload.Items) != 2 || payload.Items[0].Domain != "domain3.example" || payload.Items[1].Domain != "domain2.example" {
		t.Fatalf("unexpected page: %#v", payload.Items)
	}
	if payload.Counts["dns"] != 5 || payload.Counts["cache"] != 2 || payload.Counts["upstream"] != 3 || payload.Counts["blocked"] != 1 {
		t.Fatalf("unexpected counts: %#v", payload.Counts)
	}
}

func TestEventsPaginationMergesPersistedAndDerivedActivity(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.DB.Exec(`
		INSERT INTO events(timestamp, type, severity, title, description, source)
		VALUES('2026-07-12T12:00:03Z', 'dns.reload_failed', 'critical', 'Reload failed', 'CoreDNS rejected a configuration.', 'dns')
	`); err != nil {
		t.Fatal(err)
	}
	for index, timestamp := range []string{"2026-07-12T12:00:02Z", "2026-07-12T12:00:01Z"} {
		if _, err := store.DB.Exec(`
			INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source)
			VALUES(?, '192.168.7.21', ?, 'A', 'allowed', 'upstream')
		`, timestamp, fmt.Sprintf("merged%d.example", index)); err != nil {
			t.Fatal(err)
		}
	}

	handler := &Handler{store: store}
	requestPage := func(page int) struct {
		Items []struct {
			ID string `json:"id"`
		}
		Page       int            `json:"page"`
		Total      int            `json:"total"`
		TotalPages int            `json:"total_pages"`
		Counts     map[string]int `json:"counts"`
	} {
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/events?scope=all&page=%d&page_size=2", page), nil)
		response := httptest.NewRecorder()
		handler.events(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("page %d status = %d, body = %s", page, response.Code, response.Body.String())
		}
		var payload struct {
			Items []struct {
				ID string `json:"id"`
			}
			Page       int            `json:"page"`
			Total      int            `json:"total"`
			TotalPages int            `json:"total_pages"`
			Counts     map[string]int `json:"counts"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}

	first := requestPage(1)
	second := requestPage(2)
	if first.Page != 1 || first.Total != 4 || first.TotalPages != 2 {
		t.Fatalf("unexpected first-page metadata: %#v", first)
	}
	if len(first.Items) != 2 || first.Items[0].ID != "event-1" || first.Items[1].ID != "query-1" {
		t.Fatalf("unexpected first page: %#v", first.Items)
	}
	if second.Page != 2 || len(second.Items) != 2 || second.Items[0].ID != "device-first-seen-192.168.7.21" || second.Items[1].ID != "query-2" {
		t.Fatalf("unexpected second page: %#v", second.Items)
	}
	if first.Counts["all"] != 4 || first.Counts["dns"] != 2 || first.Counts["system"] != 2 {
		t.Fatalf("unexpected merged counts: %#v", first.Counts)
	}
}

func TestEventsRangeFiltersRowsAndBuildsTimeline(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	queries := []struct {
		timestamp string
		domain    string
		action    string
	}{
		{timestamp: "2026-07-12T12:05:00Z", domain: "inside-allowed.example", action: "allowed"},
		{timestamp: "2026-07-12T12:15:00Z", domain: "inside-blocked.example", action: "blocked"},
		{timestamp: "2026-07-12T12:30:00Z", domain: "outside.example", action: "allowed"},
	}
	for _, query := range queries {
		if _, err := store.DB.Exec(`
			INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source)
			VALUES(?, '192.168.7.22', ?, 'A', ?, 'upstream')
		`, query.timestamp, query.domain, query.action); err != nil {
			t.Fatal(err)
		}
	}

	handler := &Handler{store: store}
	request := httptest.NewRequest(http.MethodGet, "/api/events?scope=dns&range=custom&from=2026-07-12T12:00:00Z&to=2026-07-12T12:30:00Z", nil)
	response := httptest.NewRecorder()
	handler.events(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []struct {
			Domain string `json:"domain"`
		} `json:"items"`
		Total    int            `json:"total"`
		Counts   map[string]int `json:"counts"`
		Timeline *struct {
			BucketSeconds int `json:"bucket_seconds"`
			Buckets       []struct {
				Total   int `json:"total"`
				Blocked int `json:"blocked"`
			} `json:"buckets"`
		} `json:"timeline"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 2 || payload.Counts["dns"] != 2 || payload.Counts["blocked"] != 1 {
		t.Fatalf("unexpected ranged activity metadata: %#v", payload)
	}
	if len(payload.Items) != 2 || payload.Items[0].Domain != "inside-blocked.example" || payload.Items[1].Domain != "inside-allowed.example" {
		t.Fatalf("unexpected ranged activity: %#v", payload.Items)
	}
	if payload.Timeline == nil || payload.Timeline.BucketSeconds != 60 || len(payload.Timeline.Buckets) != 30 {
		t.Fatalf("unexpected timeline shape: %#v", payload.Timeline)
	}
	if payload.Timeline.Buckets[5].Total != 1 || payload.Timeline.Buckets[15].Blocked != 1 {
		t.Fatal("queries at exact minute boundaries landed in the wrong timeline buckets")
	}
	total, blocked := 0, 0
	for _, bucket := range payload.Timeline.Buckets {
		total += bucket.Total
		blocked += bucket.Blocked
	}
	if total != 2 || blocked != 1 {
		t.Fatalf("unexpected timeline totals: total=%d blocked=%d", total, blocked)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/events?search=example&scope=blocked&range=custom&from=2026-07-12T12:00:00Z&to=2026-07-12T12:30:00Z", nil)
	response = httptest.NewRecorder()
	handler.events(response, request)
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Counts["dns"] != 2 || payload.Total != 1 || len(payload.Items) != 1 || payload.Items[0].Domain != "inside-blocked.example" {
		t.Fatalf("search ignored scope or time bounds: %s", response.Body.String())
	}
}

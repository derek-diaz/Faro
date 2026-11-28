package handlers

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

func TestDailyDomainRankingsAndDeviceTotals(t *testing.T) {
	h, _, device := troubleshootingFixture(t)
	const start = "2026-01-02T00:00:00Z"
	_, err := h.store.DB.Exec(`
		WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<5)
		INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source)
		SELECT ?, '192.0.2.10', ?, 'site-'||i, 'A', 'blocked', 'upstream' FROM n;
		INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES
			(?, '192.0.2.10', ?, 'site-5', 'A', 'blocked', 'upstream'),
			(?, '192.0.2.20', NULL, 'allowed', 'A', 'allowed', 'upstream'),
			(?, '192.0.2.20', NULL, 'allowed', 'A', 'allowed', 'upstream'),
			(?, '192.0.2.20', NULL, 'allowed', 'AAAA', 'allowed', 'cache'),
			('2026-01-01T23:59:59Z', '192.0.2.10', ?, 'site-3', 'A', 'blocked', 'upstream')
	`, start, device.DeviceID, start, device.DeviceID, start, start, start, device.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"fresh", "analyzed"} {
		t.Run(state, func(t *testing.T) {
			if state == "analyzed" {
				if _, err := h.store.DB.Exec("ANALYZE"); err != nil {
					t.Fatal(err)
				}
			}
			wantQueried := []map[string]any{
				{"label": "allowed", "count": 3}, {"label": "site-5", "count": 2},
				{"label": "site-0", "count": 1}, {"label": "site-1", "count": 1}, {"label": "site-2", "count": 1},
			}
			wantBlocked := []map[string]any{
				{"label": "site-5", "count": 2}, {"label": "site-0", "count": 1}, {"label": "site-1", "count": 1},
				{"label": "site-2", "count": 1}, {"label": "site-3", "count": 1},
			}
			for _, narrowDay := range []bool{true, false} {
				queried, blocked, err := dashboardDomainRankings(context.Background(), h.store.DB, start, narrowDay)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(queried, wantQueried) || !reflect.DeepEqual(blocked, wantBlocked) {
					t.Fatalf("narrow=%t: queried=%v blocked=%v", narrowDay, queried, blocked)
				}
			}
			request := httptest.NewRequest("GET", "/api/devices", nil)
			summary, err := h.inventorySummary(request, start)
			if err != nil || summary.RequestsToday != 7 || summary.BlockedToday != 7 || summary.ActiveToday != 1 {
				t.Fatalf("inventory summary = %+v, error = %v", summary, err)
			}
			items, total, err := h.loadDeviceInventory(request, start, deviceInventoryOptions{page: 1, pageSize: 5, sort: "requests", direction: "desc", activeToday: true})
			if err != nil || total != 1 || len(items) != 1 || items[0]["total_queries_today"] != 7 || items[0]["blocked_queries_today"] != 7 {
				t.Fatalf("inventory rows = %+v, total = %d, error = %v", items, total, err)
			}
		})
	}
}

func TestDashboardTrafficHoursMatchDNSActivityAcrossMidnight(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "traffic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 11, 1, 1, 47, 30, 500000000, time.UTC)
	for _, row := range []struct{ stamp, action string }{
		{"2026-10-31T01:55:00Z", "blocked"}, // Same clock hour yesterday.
		{"2026-10-31T02:00:00.250Z", "allowed"},
		{"2026-10-31 23:59:59", "blocked"},
		{"2026-11-01T00:00:00Z", "allowed"},
		{"2026-11-01T01:00:00.001Z", "blocked"},
		{"2026-11-01 01:47:30", "allowed"},
		{"2026-11-01T01:47:31Z", "blocked"}, // At the exclusive snapshot end.
		{"2026-11-01 02:00:00", "blocked"},
	} {
		if _, err := store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,domain,query_type,action,source)
			VALUES(?,'192.0.2.10','example.test','A',?,'upstream')`, row.stamp, row.action); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	hours, err := dashboardTrafficHours(ctx, store.DB, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 24 || !hours[0].Start.Equal(now.Truncate(time.Hour).Add(-23*time.Hour)) || !hours[23].End.Equal(now.Truncate(time.Second).Add(time.Second)) {
		t.Fatalf("unexpected bounds: %+v", hours)
	}
	wantTotal := map[int]int{0: 1, 21: 1, 22: 1, 23: 2}
	wantBlocked := map[int]int{21: 1, 23: 1}
	lines := dashboardSparklines(hours)
	for i, hour := range hours {
		if i > 0 && !hour.Start.Equal(hours[i-1].End) {
			t.Fatalf("gap or overlap at hour %d", i)
		}
		if hour.Total != wantTotal[i] || hour.Blocked != wantBlocked[i] || lines["activity"][i] != hour.Total || lines["blocked"][i] != hour.Blocked {
			t.Fatalf("unexpected traffic at hour %d: %+v", i, hour)
		}
		window, err := newActivityWindow(hour.Start, hour.End)
		if err != nil {
			t.Fatal(err)
		}
		items, err := activityRecords(ctx, store.DB, 50, 0, "", "dns", window)
		if err != nil {
			t.Fatal(err)
		}
		blocked := 0
		for _, item := range items {
			if item.action == "blocked" {
				blocked++
			}
		}
		if len(items) != hour.Total || blocked != hour.Blocked {
			t.Fatalf("drill-down differs at hour %d: %+v, rows=%d blocked=%d", i, hour, len(items), blocked)
		}
	}
}

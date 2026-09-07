package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

// A repeatable household-history workload; run with -bench BenchmarkHistoryReads -benchtime=3x.
func BenchmarkHistoryReads(b *testing.B) {
	store, err := db.Open(filepath.Join(b.TempDir(), "faro.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	for i := 1; i <= 10; i++ {
		if _, err := store.DB.Exec(`INSERT INTO devices(id,name) VALUES(?,?); INSERT INTO device_addresses(device_id,address,family,source,confidence) VALUES(?,?,'ipv4','dns','observed'); INSERT INTO dns_records(hostname,type,value) VALUES(?,'A',?)`, i, fmt.Sprintf("Device %d", i), i, fmt.Sprintf("192.0.2.%d", i), fmt.Sprintf("device-%d.home", i), fmt.Sprintf("192.0.2.%d", i)); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := store.DB.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<200000)
 INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source)
 SELECT strftime('%Y-%m-%dT%H:%M:%SZ','now','-' || (i%80000) || ' seconds'),'192.0.2.'||(i%10+1),i%10+1,'site-'||(i%100)||'.example','A',CASE WHEN i%5=0 THEN 'blocked' ELSE 'allowed' END,'upstream' FROM n; ANALYZE`); err != nil {
		b.Fatal(err)
	}
	h := &Handler{store: store, deviceNames: newDeviceNameResolver(), reloader: &testReloader{}}
	ctx := context.Background()
	window, _ := newActivityWindow(time.Now().Add(-24*time.Hour), time.Now())
	b.Run("activity_rows", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if len(activityRecords(ctx, store.DB, 50, 0, "", "all", window)) != 50 {
				b.Fatal("missing records")
			}
		}
	})
	b.Run("activity_counts", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			activityCounts(ctx, store.DB, "", window)
		}
	})
	b.Run("activity_timeline", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			activityTimelineFor(ctx, store.DB, "", "all", window)
		}
	})
	b.Run("domain_sidebar", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := httptest.NewRecorder()
			h.domainSummary(r, httptest.NewRequest(http.MethodGet, "/api/domains/site-1.example/summary?include_events=false", nil))
			if r.Code != 200 {
				b.Fatal(r.Body.String())
			}
		}
	})
	b.Run("device_sidebar", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := httptest.NewRecorder()
			h.deviceDetails(r, httptest.NewRequest(http.MethodGet, "/api/devices/192.0.2.1", nil), "192.0.2.1")
			if r.Code != 200 {
				b.Fatal(r.Body.String())
			}
		}
	})
	b.Run("dashboard_uncached", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			h.dashboardCache = dashboardCacheEntry{}
			r := httptest.NewRecorder()
			h.dashboard(r, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
			if r.Code != 200 {
				b.Fatal(r.Body.String())
			}
		}
	})
}

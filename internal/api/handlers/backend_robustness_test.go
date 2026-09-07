package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

func TestSummaryFailuresAreNotReportedOrCachedAsZeroTraffic(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	handler := &Handler{store: store}
	for _, test := range []struct {
		path   string
		handle http.HandlerFunc
	}{
		{"/api/dashboard", handler.dashboard},
		{"/api/events?detail=summary&range=24h", handler.events},
		{"/api/events?detail=rows", handler.events},
		{"/api/domains/example.com/summary", handler.domainSummary},
		{"/metrics", handler.metrics},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.handle(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("failed query returned %d: %s", response.Code, response.Body.String())
			}
		})
	}
	if handler.dashboardCache.payload != nil || len(handler.activityCountsCache) != 0 || !handler.historyMetrics.expiresAt.IsZero() {
		t.Fatal("a failed query populated a cache")
	}
}

func TestDashboardWaiterRechecksCacheAfterRefresh(t *testing.T) {
	handler := &Handler{}
	request := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	key := todayStart(request)
	release, err := handler.readGate.acquire(request.Context(), "dashboard:"+key)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { response := httptest.NewRecorder(); handler.dashboard(response, request); done <- response }()
	// Holding the refresh gate must keep this request from touching the nil DB.
	select {
	case <-done:
		release()
		t.Fatal("request bypassed refresh gate")
	case <-time.After(20 * time.Millisecond):
	}
	handler.rememberDashboard(key, map[string]any{"shared": true})
	release()
	select {
	case response := <-done:
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"shared":true`) {
			t.Fatalf("did not reuse completed refresh: %s", response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not finish")
	}
}

func TestReadGateWaitCanBeCanceled(t *testing.T) {
	gate := &readGate{}
	release, err := gate.acquire(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		unlock, err := gate.acquire(ctx, "key")
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter blocked")
	}
}

func TestMetricsReuseRetainedCountsAndExposeGauges(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,domain,query_type,action,source) VALUES('2026-09-07T00:00:00Z','192.0.2.1','example.com','A','allowed','upstream')`); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: store}
	first, err := handler.cachedHistoryMetrics(context.Background())
	if err != nil || first.total != 1 {
		t.Fatalf("metrics: %+v %v", first, err)
	}
	// Retention does not force every subsequent scraper to scan the table.
	if _, err := store.DB.Exec(`DELETE FROM dns_queries`); err != nil {
		t.Fatal(err)
	}
	second, err := handler.cachedHistoryMetrics(context.Background())
	if err != nil || second.total != 1 {
		t.Fatalf("cache missed: %+v %v", second, err)
	}
	handler.historyMetrics.expiresAt = time.Time{}
	response := httptest.NewRecorder()
	handler.metrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "# TYPE faro_dns_queries_total gauge\nfaro_dns_queries_total 0") {
		t.Fatalf("retention metrics wrong: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "faro_database_connection_wait_seconds_total") {
		t.Fatal("pool contention metrics missing")
	}
}

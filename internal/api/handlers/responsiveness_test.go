package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestActivityRowsCanLoadWithoutSummaryAndPaginateWithoutGaps(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	for i := 0; i < 9; i++ {
		if _, err := h.store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(?,?,?,?,'A','blocked','manual')`, time.Now().UTC().Format(time.RFC3339Nano), input.ClientIP, input.DeviceID, fmt.Sprintf("site-%d.example", i)); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for page := 1; page <= 3; page++ {
		response := httptest.NewRecorder()
		h.events(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/events?scope=dns&detail=rows&page=%d&page_size=3", page), nil))
		var data struct {
			Items   []struct{ ID string }
			HasMore bool `json:"has_more"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if len(data.Items) != 3 || data.HasMore != (page < 3) {
			t.Fatalf("wrong page: %s", response.Body.String())
		}
		for _, item := range data.Items {
			ids = append(ids, item.ID)
		}
	}
	if h.activityCountsCache != nil {
		t.Fatal("rows unnecessarily loaded aggregate counts")
	}
	full := activityEvents(context.Background(), h.store.DB, 20, "", "dns")
	var expected []string
	for _, item := range full {
		expected = append(expected, item["id"].(string))
	}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("rows split lost ordering: got %v want %v", ids, expected)
	}
	summary := httptest.NewRecorder()
	h.events(summary, httptest.NewRequest(http.MethodGet, "/api/events?scope=dns&detail=summary&range=24h", nil))
	var data struct {
		Items    []any
		Total    int
		Timeline *activityTimeline
	}
	if err := json.Unmarshal(summary.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Items) != 0 || data.Total != 9 || data.Timeline == nil {
		t.Fatalf("invalid independent summary: %s", summary.Body.String())
	}
}

func TestRelativeActivityWindowsReuseCountsWithoutRoundingRows(t *testing.T) {
	first, err := parseActivityWindow(url.Values{"range": {"24h"}})
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.from = second.from.Add(time.Second)
	second.to = second.to.Add(time.Second)
	if first.cacheKey() != second.cacheKey() {
		t.Fatal("live count cache changes every request")
	}
	first.relativeDuration = 0
	second.relativeDuration = 0
	if first.cacheKey() == second.cacheKey() {
		t.Fatal("custom ranges lost exact boundaries")
	}
}

func TestSlowHealthProbeDoesNotBlockDashboardData(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprintln(w, "coredns_cache_entries 42")
	}))
	defer server.Close()
	defer close(release)
	h := &Handler{metricsURL: server.URL}
	returned := make(chan bool, 1)
	go func() { _, pending := h.cachedCoreDNSCacheMetrics(); returned <- pending }()
	select {
	case pending := <-returned:
		if !pending {
			t.Fatal("first probe should be pending")
		}
	case <-time.After(time.Second):
		t.Fatal("view waited for a blocked health probe")
	}
	<-started
	_, pending := h.cachedCoreDNSCacheMetrics()
	if !pending {
		t.Fatal("pending probe incorrectly reported as complete")
	}
	card := dnsHealthCard(false, 0, true)
	if card["status"] != "info" {
		t.Fatalf("unfinished probe shown as outage: %#v", card)
	}
}

func TestReverseNamesResolveWithoutBlockingOrDuplicateLookups(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	resolver := newDeviceNameResolver()
	resolver.lookup = func(ctx context.Context, ip string) ([]string, error) {
		calls.Add(1)
		select {
		case <-release:
			return []string{"laptop.home."}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	returned := make(chan struct{})
	go func() {
		resolveReverseDeviceNames(context.Background(), []string{"192.0.2.10", "192.0.2.10"}, map[string]deviceIdentity{}, resolver)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("sidebar waited for reverse DNS")
	}
	resolveReverseDeviceNames(context.Background(), []string{"192.0.2.10"}, map[string]deviceIdentity{}, resolver)
	if calls.Load() > 1 {
		t.Fatalf("duplicate lookups: %d", calls.Load())
	}
	resolver.store("192.0.2.20", "Known name")
	identities := map[string]deviceIdentity{}
	resolveReverseDeviceNames(context.Background(), []string{"192.0.2.20"}, identities, resolver)
	if identities["192.0.2.20"].DisplayName != "Known name" {
		t.Fatalf("cached name unavailable: %#v", identities)
	}
}

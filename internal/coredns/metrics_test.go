package coredns

import (
	"strings"
	"testing"
)

func TestNodeMetricsAggregateViewsAndTransports(t *testing.T) {
	metrics, err := parseMetrics(strings.NewReader(`# TYPE coredns_dns_requests_total counter
coredns_dns_requests_total{proto="udp",view="home"} 1.2e2
coredns_dns_requests_total{proto="tcp",view="guests"} 30
coredns_dns_responses_total{rcode="SERVFAIL",view="home"} 3
coredns_dns_responses_total{rcode="NXDOMAIN",view="home"} 7
coredns_dns_request_duration_seconds_sum{view="home"} 0.24
coredns_dns_request_duration_seconds_count{view="home"} 120
coredns_dns_request_duration_seconds_sum{view="guests"} 0.21
coredns_dns_request_duration_seconds_count{view="guests"} 30
coredns_cache_requests_total{view="home"} 60
coredns_cache_hits_total{view="home"} 40
coredns_cache_entries{type="success"} 20
`))
	if err != nil || metrics.DNS.Requests != 150 || metrics.DNS.ServerFailures != 3 ||
		metrics.DNS.MeanResponseMS == nil || *metrics.DNS.MeanResponseMS < 2.999 || *metrics.DNS.MeanResponseMS > 3.001 ||
		metrics.CacheRequests != 60 || metrics.CacheHits != 40 || metrics.CacheEntries != 20 {
		t.Fatalf("unexpected metrics: %+v, %v", metrics, err)
	}
	idle, err := parseMetrics(strings.NewReader("coredns_build_info{version=\"1.11.3\"} 1\n"))
	if err != nil || idle.DNS.Requests != 0 || idle.DNS.MeanResponseMS != nil {
		t.Fatalf("unexpected idle report: %+v, %v", idle, err)
	}
	for _, body := range []string{"<html>OK</html>", "coredns_dns_requests_total NaN", "coredns_dns_request_duration_seconds_sum +Inf"} {
		if _, err := parseMetrics(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted invalid metrics %q", body)
		}
	}
}

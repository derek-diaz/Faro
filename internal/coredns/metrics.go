package coredns

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DNSTotals describes the current CoreDNS process across transports and views.
type DNSTotals struct {
	Requests       uint64   `json:"requests"`
	ServerFailures uint64   `json:"server_failures"`
	MeanResponseMS *float64 `json:"mean_response_ms,omitempty"`
}

type Metrics struct {
	DNS                                    DNSTotals
	ProcessStart                           float64
	CacheEntries, CacheHits, CacheRequests float64
}

func ReadMetrics(ctx context.Context, url string) (Metrics, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Metrics{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Metrics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Metrics{}, fmt.Errorf("CoreDNS metrics: %s", resp.Status)
	}
	return parseMetrics(resp.Body)
}

func parseMetrics(r io.Reader) (Metrics, error) {
	var result Metrics
	var durationSum, durationCount float64
	seen := false
	limited := &io.LimitedReader{R: r, N: 8 << 20}
	scanner := bufio.NewScanner(limited)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "coredns_") && !strings.HasPrefix(line, "process_start_time_seconds ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, labels, _ := strings.Cut(fields[0], "{")
		switch name {
		case "process_start_time_seconds", "coredns_build_info", "coredns_dns_requests_total", "coredns_dns_responses_total",
			"coredns_dns_request_duration_seconds_sum", "coredns_dns_request_duration_seconds_count",
			"coredns_cache_entries", "coredns_cache_hits_total", "coredns_cache_requests_total":
		default:
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || value >= math.MaxUint64 {
			return Metrics{}, fmt.Errorf("invalid value for %s", name)
		}
		if name == "process_start_time_seconds" {
			result.ProcessStart = value
			continue
		}
		seen = true
		switch name {
		case "coredns_dns_requests_total":
			result.DNS.Requests += uint64(value)
		case "coredns_dns_responses_total":
			if metricLabel(labels, "rcode") == "SERVFAIL" {
				result.DNS.ServerFailures += uint64(value)
			}
		case "coredns_dns_request_duration_seconds_sum":
			durationSum += value
		case "coredns_dns_request_duration_seconds_count":
			durationCount += value
		case "coredns_cache_entries":
			result.CacheEntries += value
		case "coredns_cache_hits_total":
			result.CacheHits += value
		case "coredns_cache_requests_total":
			result.CacheRequests += value
		}
	}
	if err := scanner.Err(); err != nil {
		return Metrics{}, err
	}
	if !seen || limited.N == 0 {
		return Metrics{}, fmt.Errorf("missing or incomplete CoreDNS metrics")
	}
	if durationCount > 0 {
		ms := durationSum / durationCount * 1000
		result.DNS.MeanResponseMS = &ms
	}
	return result, nil
}

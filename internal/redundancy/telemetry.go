package redundancy

import (
	"context"
	"time"

	"github.com/derek/faro/internal/coredns"
)

const dnsSampleInterval = 15 * time.Second

// Sampling runs independently of both snapshot application and UI requests.
func (manager *Manager) RunDNSMetrics(ctx context.Context, url string) {
	ticker := time.NewTicker(dnsSampleInterval)
	defer ticker.Stop()
	for {
		if state, err := manager.readState(ctx); err == nil && state.Role != RoleStandalone {
			manager.sampleDNS(ctx, url)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (manager *Manager) sampleDNS(ctx context.Context, url string) {
	metrics, err := coredns.ReadMetrics(ctx, url)
	report := &DNSReport{CheckedAt: manager.now().UTC()}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err == nil {
		report.processStart = metrics.ProcessStart
		report.Totals = &DNSActivity{DNSTotals: metrics.DNS}
		prev := manager.localDNS
		if prev != nil && prev.Totals != nil && report.processStart > 0 && prev.processStart == report.processStart && metrics.DNS.Requests >= prev.Totals.Requests {
			elapsed := report.CheckedAt.Sub(prev.CheckedAt)
			if elapsed > 0 && elapsed <= 3*dnsSampleInterval {
				rate := float64(metrics.DNS.Requests-prev.Totals.Requests) / elapsed.Seconds()
				report.Totals.RequestsPerSecond = &rate
			}
		}
	}
	manager.localDNS = report
}

func (manager *Manager) latestDNS() *DNSReport {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.localDNS
}

func dnsStatus(report *DNSReport, online bool, now time.Time) string {
	switch {
	case report == nil:
		return "unreported"
	case !online || now.Sub(report.CheckedAt) > 3*dnsSampleInterval:
		return "stale"
	case report.Totals == nil:
		return "unavailable"
	default:
		return "available"
	}
}

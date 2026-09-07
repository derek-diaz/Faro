package handlers

import (
	"context"
	"time"
)

type historyMetrics struct {
	total, blocked, cacheHits, upstreamQueries, enabled, entries int
	expiresAt                                                    time.Time
}

func (handler *Handler) cachedHistoryMetrics(ctx context.Context) (historyMetrics, error) {
	release, err := handler.readGate.acquire(ctx, "metrics")
	if err != nil {
		return historyMetrics{}, err
	}
	defer release()
	if time.Now().Before(handler.historyMetrics.expiresAt) {
		return handler.historyMetrics, nil
	}
	var result historyMetrics
	err = handler.store.DB.QueryRowContext(ctx, `SELECT COUNT(*),
 COALESCE(SUM(action = 'blocked'),0),
 COALESCE(SUM(source = 'cache'),0),
 COALESCE(SUM(source = 'upstream'),0),
 (SELECT COUNT(*) FROM blocklists WHERE enabled = 1),
 (SELECT COUNT(*) FROM blocklist_entries)
 FROM dns_queries`).Scan(&result.total, &result.blocked, &result.cacheHits, &result.upstreamQueries, &result.enabled, &result.entries)
	if err != nil {
		return historyMetrics{}, err
	}
	result.expiresAt = time.Now().Add(15 * time.Second)
	handler.historyMetrics = result
	return result, nil
}

package handlers

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/derek/faro/internal/upstreamhealth"
)

const dashboardCacheTTL = 5 * time.Second

type dashboardCacheEntry struct {
	key       string
	payload   map[string]any
	expiresAt time.Time
}

func (handler *Handler) cachedDashboard(key string) (map[string]any, bool) {
	handler.dashboardMu.Lock()
	defer handler.dashboardMu.Unlock()
	if handler.dashboardCache.payload == nil || handler.dashboardCache.key != key || time.Now().After(handler.dashboardCache.expiresAt) {
		return nil, false
	}
	return handler.dashboardCache.payload, true
}

func (handler *Handler) rememberDashboard(key string, payload map[string]any) {
	handler.dashboardMu.Lock()
	handler.dashboardCache = dashboardCacheEntry{key: key, payload: payload, expiresAt: time.Now().Add(dashboardCacheTTL)}
	handler.dashboardMu.Unlock()
}

func (handler *Handler) dashboard(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	start := todayStart(request)
	if payload, ok := handler.cachedDashboard(start); ok {
		writeJSON(responseWriter, http.StatusOK, payload)
		return
	}
	traffic := dashboardTrafficSummary(request.Context(), handler.store.DB, start)
	counts := dashboardCountsSummary(request.Context(), handler.store.DB, start)
	settings := dashboardSettingsSummary(request.Context(), handler.store.DB)
	total := traffic.total
	blocked := traffic.blocked
	topClients := grouped(request.Context(), handler.store.DB, `SELECT client_ip, COUNT(*) FROM dns_queries WHERE timestamp >= ? GROUP BY client_ip ORDER BY COUNT(*) DESC LIMIT 5`, start)
	topBlocked := grouped(request.Context(), handler.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE timestamp >= ? AND action = 'blocked' GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 5`, start)
	liveCache := handler.coreDNSCacheMetrics(request.Context())
	upstreamSnapshot := upstreamhealth.Snapshot{Status: "unknown", Summary: "Upstream health has not been checked yet.", Items: make([]upstreamhealth.Probe, 0)}
	if handler.upstreams != nil {
		upstreamSnapshot = handler.upstreams.Snapshot()
	}

	payload := map[string]any{
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"block_percentage":      percentage(blocked, total),
		"enabled_blocklists":    counts.enabledBlocklists,
		"blocklist_entries":     counts.blockEntries,
		"cache": map[string]any{
			"enabled":                     settings.cacheEnabled != "false",
			"metrics_available":           liveCache.available,
			"entries":                     liveCache.entries,
			"hits_since_restart":          liveCache.hits,
			"requests_since_restart":      liveCache.requests,
			"hit_rate_since_restart":      percentage64(liveCache.hits, liveCache.requests),
			"hits_today":                  traffic.cacheHits,
			"upstream_queries_today":      traffic.upstreamQueries,
			"hit_rate_today":              percentage(traffic.cacheHits, traffic.cacheHits+traffic.upstreamQueries),
			"average_cache_latency_ms":    traffic.cacheLatency,
			"average_upstream_latency_ms": traffic.upstreamLatency,
		},
		"network_summary": networkSummary(request.Context(), handler.store.DB, networkSummaryInput{
			start: start, blocked: blocked, newDevices: counts.newDevices, newDevicesKnown: true, topClients: topClients, topBlocked: topBlocked,
			upstreams: upstreamSnapshot, dnsMetricsAvailable: liveCache.available,
		}),
		"health_cards": healthCards(request.Context(), handler.store.DB, healthCardsInput{
			total: total, blocked: blocked, enabledBlocklists: counts.enabledBlocklists, blockEntries: counts.blockEntries,
			deviceCount: counts.deviceCount, reloadFailures: counts.reloadFailures,
			cacheAnswers: traffic.cacheHits, forwardedAnswers: traffic.upstreamQueries,
			trafficCountsKnown:  true,
			upstreams:           upstreamSnapshot,
			dnsMetricsAvailable: liveCache.available,
		}),
		"stories": dashboardStories(request.Context(), handler.store.DB, dashboardStoriesInput{
			start: start, blocked: blocked, newDevices: counts.newDevices, newDevicesKnown: true, topClients: topClients, topBlocked: topBlocked,
			reloadFailures: counts.reloadFailures, upstreams: upstreamSnapshot, dnsMetricsAvailable: liveCache.available,
		}),
		"whats_new":                whatsNew(request.Context(), handler.store.DB, start),
		"sparklines":               dashboardSparklines(request.Context(), handler.store.DB),
		"top_queried_domains":      grouped(request.Context(), handler.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 5`, start),
		"top_blocked_domains":      topBlocked,
		"top_clients":              topClients,
		"recent_activity":          recentQueries(request.Context(), handler.store.DB),
		"upstream_health":          upstreamSnapshot.Summary,
		"upstream_health_status":   upstreamSnapshot.Status,
		"upstream_checked_at":      upstreamSnapshot.CheckedAt,
		"upstream_probes":          upstreamSnapshot.Items,
		"favicon_fetching_enabled": settings.faviconFetchingEnabled,
	}
	handler.rememberDashboard(start, payload)
	writeJSON(responseWriter, http.StatusOK, payload)
}

type dashboardTraffic struct {
	total, blocked, cacheHits, upstreamQueries int
	cacheLatency, upstreamLatency              float64
}

func dashboardTrafficSummary(ctx context.Context, database *sql.DB, start string) dashboardTraffic {
	var traffic dashboardTraffic
	_ = database.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN source = 'cache' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN source = 'upstream' THEN 1 ELSE 0 END), 0),
			COALESCE(AVG(CASE WHEN source = 'cache' THEN latency_ms END), 0),
			COALESCE(AVG(CASE WHEN source = 'upstream' THEN latency_ms END), 0)
		FROM dns_queries
		WHERE timestamp >= ?
	`, start).Scan(
		&traffic.total, &traffic.blocked, &traffic.cacheHits, &traffic.upstreamQueries,
		&traffic.cacheLatency, &traffic.upstreamLatency,
	)
	traffic.cacheLatency = roundedFloat(traffic.cacheLatency)
	traffic.upstreamLatency = roundedFloat(traffic.upstreamLatency)
	return traffic
}

type dashboardCounts struct {
	enabledBlocklists, blockEntries, deviceCount, reloadFailures, newDevices int
}

func dashboardCountsSummary(ctx context.Context, database *sql.DB, start string) dashboardCounts {
	var counts dashboardCounts
	_ = database.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM blocklists WHERE enabled = 1),
			(SELECT COUNT(*) FROM blocklist_entries),
			(SELECT COUNT(*) FROM devices),
			(SELECT COUNT(*) FROM events WHERE type = 'dns.reload_failed' AND timestamp >= ?),
			(SELECT COUNT(*) FROM devices WHERE first_seen_at >= ?)
	`, start, start).Scan(
		&counts.enabledBlocklists, &counts.blockEntries, &counts.deviceCount,
		&counts.reloadFailures, &counts.newDevices,
	)
	return counts
}

type dashboardSettings struct {
	cacheEnabled, faviconFetchingEnabled string
}

func dashboardSettingsSummary(ctx context.Context, database *sql.DB) dashboardSettings {
	var settings dashboardSettings
	_ = database.QueryRowContext(ctx, `
		SELECT
			COALESCE((SELECT value FROM settings WHERE key = 'dns_cache_enabled'), ''),
			COALESCE((SELECT value FROM settings WHERE key = 'favicon_fetching_enabled'), '')
	`).Scan(&settings.cacheEnabled, &settings.faviconFetchingEnabled)
	return settings
}

func roundedFloat(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}

type cacheMetrics struct {
	available bool
	entries   float64
	hits      float64
	requests  float64
}

func (handler *Handler) coreDNSCacheMetrics(ctx context.Context) cacheMetrics {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, handler.metricsURL, nil)
	if err != nil {
		return cacheMetrics{}
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return cacheMetrics{}
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("close CoreDNS metrics response body: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return cacheMetrics{}
	}

	result := cacheMetrics{available: true}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseFloat(fields[len(fields)-1], 64)
		if parseErr != nil {
			continue
		}
		name := fields[0]
		if label := strings.Index(name, "{"); label >= 0 {
			name = name[:label]
		}
		switch name {
		case "coredns_cache_entries":
			result.entries += value
		case "coredns_cache_hits_total":
			result.hits += value
		case "coredns_cache_requests_total":
			result.requests += value
		}
	}
	return result
}

type networkSummaryInput struct {
	start               string
	blocked             int
	newDevices          int
	newDevicesKnown     bool
	topClients          []map[string]any
	topBlocked          []map[string]any
	upstreams           upstreamhealth.Snapshot
	dnsMetricsAvailable bool
}

func networkSummary(ctx context.Context, database *sql.DB, input networkSummaryInput) map[string]any {
	headline, messages := networkHeadline(input.blocked, input.upstreams, input.dnsMetricsAvailable)
	messages = appendNetworkHighlights(messages, input.topClients, input.topBlocked)
	newDevices := input.newDevices
	if !input.newDevicesKnown {
		newDevices = scalarInt(ctx, database, `
			SELECT COUNT(*) FROM (
				SELECT client_ip, MIN(timestamp) AS first_seen
				FROM dns_queries
				GROUP BY client_ip
				HAVING first_seen >= ?
			)
		`, input.start)
	}
	if newDevices == 0 {
		messages = append(messages, "No new devices seen today.")
	} else if input.newDevices == 1 {
		messages = append(messages, "1 new device seen today.")
	} else {
		messages = append(messages, fmt.Sprintf("%d new devices seen today.", input.newDevices))
	}
	return map[string]any{"headline": headline, "messages": messages}
}

func networkHeadline(blocked int, upstreams upstreamhealth.Snapshot, dnsMetricsAvailable bool) (string, []string) {
	headline := "Everything looks normal."
	messages := make([]string, 0, 4)
	switch {
	case !dnsMetricsAvailable:
		headline = "The DNS engine health check is unavailable."
		messages = append(messages, "Faro could not reach the CoreDNS metrics endpoint.")
	case upstreams.Status == "critical":
		headline = "All configured upstream resolvers are unavailable."
		messages = append(messages, upstreams.Summary)
	case upstreams.Status == "degraded":
		headline = "Upstream DNS is degraded."
		messages = append(messages, upstreams.Summary)
	case upstreams.Status == "unknown":
		headline = "Checking upstream DNS health."
		messages = append(messages, upstreams.Summary)
	case blocked > 0:
		headline = fmt.Sprintf("Faro blocked %d requests today.", blocked)
		messages = append(messages, headline)
	default:
		messages = append(messages, "Everything looks normal.")
	}
	return headline, messages
}

func appendNetworkHighlights(messages []string, topClients, topBlocked []map[string]any) []string {
	if label := firstLabel(topClients); label != "" {
		messages = append(messages, "Top active device today: "+label+".")
	}
	if label := firstLabel(topBlocked); label != "" {
		messages = append(messages, "Most blocked domain: "+label+".")
	}
	return messages
}

func firstLabel(items []map[string]any) string {
	if len(items) == 0 {
		return ""
	}
	label, _ := items[0]["label"].(string)
	return label
}

type dashboardStoriesInput struct {
	start               string
	blocked             int
	newDevices          int
	newDevicesKnown     bool
	topClients          []map[string]any
	topBlocked          []map[string]any
	reloadFailures      int
	upstreams           upstreamhealth.Snapshot
	dnsMetricsAvailable bool
}

func dashboardStories(ctx context.Context, database *sql.DB, input dashboardStoriesInput) []map[string]any {
	stories := make([]map[string]any, 0, 4)
	if !input.dnsMetricsAvailable {
		stories = append(stories, story("DNS engine health is unavailable.", "Faro could not reach CoreDNS metrics.", "critical"))
	} else if input.upstreams.Status == "critical" {
		stories = append(stories, story("Upstream DNS is unavailable.", input.upstreams.Summary, "critical"))
	} else if input.upstreams.Status == "degraded" {
		stories = append(stories, story("Upstream DNS is degraded.", input.upstreams.Summary, "warning"))
	} else if input.reloadFailures == 0 {
		stories = append(stories, story("Everything looks healthy today.", "No DNS reload failures detected.", "success"))
	} else {
		stories = append(stories, story("DNS needs attention.", fmt.Sprintf("%d reload failures detected today.", input.reloadFailures), "critical"))
	}
	newDevices := input.newDevices
	if !input.newDevicesKnown {
		newDevices = scalarInt(ctx, database, `SELECT COUNT(*) FROM (SELECT client_ip, MIN(timestamp) first_seen FROM dns_queries GROUP BY client_ip HAVING first_seen >= ?)`, input.start)
	}
	if newDevices > 0 {
		stories = append(stories, story(countedStory(newDevices, "new device joined today.", "new devices joined today."), "New clients are now visible in Devices.", "info"))
	}
	if input.blocked > 0 {
		stories = append(stories, story("Filtering blocked "+countedStory(input.blocked, "request today.", "requests today."), firstLabelSentence("Most blocked domain", input.topBlocked), "warning"))
	}
	if len(input.topClients) > 0 {
		stories = append(stories, story("Busiest device today.", firstLabelSentence("Top active device", input.topClients), "info"))
	}
	return stories
}

func story(title, body, tone string) map[string]any {
	return map[string]any{"title": title, "body": body, "tone": tone}
}

func countedStory(count int, singular, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}

func firstLabelSentence(prefix string, items []map[string]any) string {
	if len(items) == 0 {
		return ""
	}
	label, _ := items[0]["label"].(string)
	if label == "" {
		return ""
	}
	return prefix + ": " + label + "."
}

type healthCardsInput struct {
	total               int
	blocked             int
	enabledBlocklists   int
	blockEntries        int
	deviceCount         int
	reloadFailures      int
	cacheAnswers        int
	forwardedAnswers    int
	trafficCountsKnown  bool
	upstreams           upstreamhealth.Snapshot
	dnsMetricsAvailable bool
}

func healthCards(ctx context.Context, database *sql.DB, input healthCardsInput) []map[string]any {
	cacheAnswers, forwardedAnswers := input.cacheAnswers, input.forwardedAnswers
	if !input.trafficCountsKnown {
		cacheAnswers = scalarInt(ctx, database, `SELECT COUNT(*) FROM dns_queries WHERE source = 'cache'`)
		forwardedAnswers = scalarInt(ctx, database, `SELECT COUNT(*) FROM dns_queries WHERE source = 'upstream'`)
	}
	return []map[string]any{
		dnsHealthCard(input.dnsMetricsAvailable, input.reloadFailures),
		upstreamHealthCard(input.upstreams),
		{"label": "Devices", "value": fmt.Sprintf("%d observed", input.deviceCount), "detail": "From local query data", "status": "healthy"},
		{"label": "Filtering", "value": fmt.Sprintf("%d domains", input.blockEntries), "detail": fmt.Sprintf("%d enabled lists", input.enabledBlocklists), "status": ternary(input.blockEntries > 0, "healthy", "warning")},
		{"label": "Cache", "value": fmt.Sprintf("%.1f%% hit rate", percentage(cacheAnswers, cacheAnswers+forwardedAnswers)), "detail": fmt.Sprintf("%d upstream calls avoided", cacheAnswers), "status": "info"},
		{"label": "Blocked", "value": fmt.Sprintf("%d today", input.blocked), "detail": fmt.Sprintf("%.1f%% of activity", percentage(input.blocked, input.total)), "status": ternary(input.blocked > 0, "warning", "healthy")},
	}
}

func dnsHealthCard(metricsAvailable bool, reloadFailures int) map[string]any {
	if !metricsAvailable {
		return map[string]any{"label": "DNS", "value": "Unavailable", "detail": "CoreDNS metrics could not be reached", "status": "critical"}
	}
	return map[string]any{
		"label":  "DNS",
		"value":  ternary(reloadFailures == 0, "Healthy", "Needs attention"),
		"detail": ternary(reloadFailures == 0, "Engine running", "Reload failures detected"),
		"status": ternary(reloadFailures == 0, "healthy", "critical"),
	}
}

func upstreamHealthCard(snapshot upstreamhealth.Snapshot) map[string]any {
	online := 0
	for _, item := range snapshot.Items {
		if item.Status == "online" {
			online++
		}
	}
	value := "Checking"
	detail := snapshot.Summary
	status := snapshot.Status
	if snapshot.Status == "healthy" {
		value = fmt.Sprintf("%d online", online)
	} else if snapshot.Status == "degraded" {
		value = fmt.Sprintf("%d of %d online", online, len(snapshot.Items))
	} else if snapshot.Status == "critical" {
		value = "Unavailable"
	} else {
		status = "info"
	}
	return map[string]any{"label": "Upstreams", "value": value, "detail": detail, "status": status}
}

func whatsNew(ctx context.Context, database *sql.DB, start string) map[string]any {
	return map[string]any{
		"devices":       searchRows(ctx, database, `SELECT address AS label, 'First seen today' AS subtitle FROM device_addresses WHERE first_seen_at >= ? ORDER BY first_seen_at DESC, id DESC LIMIT 5`, start),
		"domains":       searchRows(ctx, database, `SELECT q.domain AS label, 'First time observed' AS subtitle FROM dns_queries q WHERE q.timestamp >= ? AND NOT EXISTS (SELECT 1 FROM dns_queries prior WHERE prior.domain = q.domain AND prior.timestamp < ?) GROUP BY q.domain ORDER BY MIN(q.timestamp) DESC LIMIT 5`, start, start),
		"blocklists":    searchRows(ctx, database, `SELECT name AS label, 'Installed today' AS subtitle FROM blocklists WHERE created_at >= ? ORDER BY created_at DESC LIMIT 5`, start),
		"local_records": searchRows(ctx, database, `SELECT hostname AS label, 'Local DNS record' AS subtitle FROM dns_records WHERE created_at >= ? ORDER BY created_at DESC LIMIT 5`, start),
	}
}

func dashboardSparklines(ctx context.Context, database *sql.DB) map[string]any {
	activity, blocked := hourlyActivityCounts(ctx, database)
	return map[string]any{
		"activity": activity,
		"blocked":  blocked,
	}
}

func hourlyActivityCounts(ctx context.Context, database *sql.DB) ([]int, []int) {
	activity, blocked := make([]int, 24), make([]int, 24)
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	rows, err := database.QueryContext(ctx, `
		SELECT strftime('%H', timestamp) AS hour,
		       COUNT(*),
		       COALESCE(SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), 0)
		FROM dns_queries
		WHERE timestamp >= ?
		GROUP BY hour
	`, cutoff)
	if err != nil {
		return activity, blocked
	}
	defer closeRows(rows)
	nowHour := time.Now().UTC().Hour()
	for rows.Next() {
		var hourText string
		var count, blockedCount int
		if err := rows.Scan(&hourText, &count, &blockedCount); err != nil {
			return activity, blocked
		}
		hour, err := strconv.Atoi(hourText)
		if err != nil {
			continue
		}
		index := (hour - nowHour + 23 + 24) % 24
		activity[index] = count
		blocked[index] = blockedCount
	}
	return activity, blocked
}

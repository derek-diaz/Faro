package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/derek/faro/internal/coredns"
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
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	start := todayStart(request)
	if payload, ok := handler.cachedDashboard(start); ok {
		writeJSON(responseWriter, http.StatusOK, payload)
		return
	}
	release, err := handler.readGate.acquire(ctx, "dashboard:"+start)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	defer release()
	if payload, ok := handler.cachedDashboard(start); ok {
		writeJSON(responseWriter, http.StatusOK, payload)
		return
	}
	traffic, err := dashboardTrafficSummary(ctx, handler.store.DB, start)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	counts, err := dashboardCountsSummary(ctx, handler.store.DB, start)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	settings, err := dashboardSettingsSummary(ctx, handler.store.DB)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	total := traffic.total
	blocked := traffic.blocked
	devices, activeDevices, err := handler.loadDeviceInventory(request, start, deviceInventoryOptions{page: 1, pageSize: 5, sort: "requests", direction: "desc", activeToday: true})
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	topClients := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		topClients = append(topClients, map[string]any{"label": device["display_name"], "count": device["total_queries_today"], "client_ip": device["client_ip"], "device_id": device["device_id"]})
	}
	topQueried, topBlocked, err := dashboardDomainRankings(ctx, handler.store.DB, start, traffic.narrowDay)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	changes, err := handler.whatsNew(request, start)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	hours, err := dashboardTrafficHours(ctx, handler.store.DB, time.Now())
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	recent, err := recentQueries(ctx, handler.store.DB)
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	liveCache, metricsPending := handler.cachedCoreDNSCacheMetrics()
	upstreamSnapshot := upstreamhealth.Snapshot{Status: "unknown", Summary: "Upstream health has not been checked yet.", Items: make([]upstreamhealth.Probe, 0)}
	if handler.upstreams != nil {
		upstreamSnapshot = handler.upstreams.Snapshot()
	}

	highlights := dashboardHighlights{
		replicasBehind: counts.replicasBehind, behindName: counts.behindName,
		blocked: blocked, newDevices: counts.newDevices, topClients: topClients, topBlocked: topBlocked,
		reloadFailures: counts.reloadFailures, upstreams: upstreamSnapshot,
		dnsMetricsAvailable: liveCache.available, dnsMetricsPending: metricsPending,
	}
	payload := map[string]any{
		"total_queries_today":   total,
		"active_devices_today":  activeDevices,
		"observed_devices":      counts.deviceCount,
		"blocked_queries_today": blocked,
		"block_percentage":      percentage(blocked, total),
		"enabled_blocklists":    counts.enabledBlocklists,
		"blocklist_entries":     counts.blockEntries,
		"cache": map[string]any{
			"enabled":                     settings.cacheEnabled != "false",
			"metrics_available":           liveCache.available,
			"metrics_pending":             metricsPending,
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
		"network_summary": networkSummary(highlights),
		"health_cards": healthCards(healthCardsInput{
			total: total, blocked: blocked, enabledBlocklists: counts.enabledBlocklists, blockEntries: counts.blockEntries,
			deviceCount: counts.deviceCount, reloadFailures: counts.reloadFailures,
			cacheAnswers: traffic.cacheHits, forwardedAnswers: traffic.upstreamQueries,
			upstreams:           upstreamSnapshot,
			dnsMetricsAvailable: liveCache.available, dnsMetricsPending: metricsPending,
		}),
		"stories":                  dashboardStories(highlights),
		"whats_new":                changes,
		"traffic_hours":            hours,
		"sparklines":               dashboardSparklines(hours),
		"top_queried_domains":      topQueried,
		"top_blocked_domains":      topBlocked,
		"top_clients":              topClients,
		"recent_activity":          recent,
		"upstream_health":          upstreamSnapshot.Summary,
		"upstream_health_status":   upstreamSnapshot.Status,
		"upstream_checked_at":      upstreamSnapshot.CheckedAt,
		"upstream_probes":          upstreamSnapshot.Items,
		"favicon_fetching_enabled": settings.faviconFetchingEnabled,
	}
	if err := ctx.Err(); err != nil {
		writeError(responseWriter, err)
		return
	}
	handler.rememberDashboard(start, payload)
	writeJSON(responseWriter, http.StatusOK, payload)
}

func dashboardDomainRankings(ctx context.Context, database *sql.DB, start string, narrowDay bool) ([]map[string]any, []map[string]any, error) {
	allIndex, blockedIndex := "", ""
	if narrowDay {
		allIndex = " INDEXED BY idx_dns_queries_timestamp"
		blockedIndex = " INDEXED BY idx_dns_queries_action_timestamp"
	}
	const selectDomain = "SELECT domain, COUNT(*) FROM dns_queries"
	const ranking = " GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 5"
	queried, err := groupedResult(ctx, database, selectDomain+allIndex+" WHERE timestamp >= ?"+ranking, start)
	if err != nil {
		return nil, nil, err
	}
	blocked, err := groupedResult(ctx, database, selectDomain+blockedIndex+" WHERE action = 'blocked' AND timestamp >= ?"+ranking, start)
	return queried, blocked, err
}

type dashboardTraffic struct {
	total, blocked, cacheHits, upstreamQueries int
	cacheLatency, upstreamLatency              float64
	narrowDay                                  bool
}

func dashboardTrafficSummary(ctx context.Context, database *sql.DB, start string) (dashboardTraffic, error) {
	var traffic dashboardTraffic
	err := database.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN source = 'cache' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN source = 'upstream' THEN 1 ELSE 0 END), 0),
			COALESCE(AVG(CASE WHEN source = 'cache' THEN latency_ms END), 0),
			COALESCE(AVG(CASE WHEN source = 'upstream' THEN latency_ms END), 0),
			-- The end IDs estimate retained rows with two index seeks. Prefer the
			-- time range below one eighth of history, and covering domain indexes
			-- on busy days. Gaps in the ID span only affect the plan choice.
			COUNT(*) * 8 < COALESCE((SELECT MAX(id) FROM dns_queries) - (SELECT MIN(id) FROM dns_queries) + 1, 0)
		FROM dns_queries
		WHERE timestamp >= ?
	`, start).Scan(
		&traffic.total, &traffic.blocked, &traffic.cacheHits, &traffic.upstreamQueries,
		&traffic.cacheLatency, &traffic.upstreamLatency, &traffic.narrowDay,
	)
	traffic.cacheLatency = roundedFloat(traffic.cacheLatency)
	traffic.upstreamLatency = roundedFloat(traffic.upstreamLatency)
	return traffic, err
}

type dashboardCounts struct {
	enabledBlocklists, blockEntries, deviceCount, reloadFailures, newDevices int
	replicasBehind                                                           int
	behindName                                                               string
}

func dashboardCountsSummary(ctx context.Context, database *sql.DB, start string) (dashboardCounts, error) {
	var counts dashboardCounts
	err := database.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM blocklists WHERE enabled = 1),
			(SELECT COUNT(*) FROM blocklist_entries),
			(SELECT COUNT(*) FROM devices),
			(SELECT COUNT(*) FROM events WHERE type = 'dns.reload_failed' AND timestamp >= ?),
			(SELECT COUNT(*) FROM devices WHERE first_seen_at >= ?),
			COUNT(*), COALESCE(MIN(n.name), '') FROM redundancy_nodes n JOIN redundancy_state s ON s.id=1
			 WHERE s.role='controller' AND n.config_revision < s.config_revision
	`, start, start).Scan(
		&counts.enabledBlocklists, &counts.blockEntries, &counts.deviceCount,
		&counts.reloadFailures, &counts.newDevices, &counts.replicasBehind, &counts.behindName,
	)
	return counts, err
}

type dashboardSettings struct {
	cacheEnabled, faviconFetchingEnabled string
}

func dashboardSettingsSummary(ctx context.Context, database *sql.DB) (dashboardSettings, error) {
	var settings dashboardSettings
	err := database.QueryRowContext(ctx, `
		SELECT
			COALESCE((SELECT value FROM settings WHERE key = 'dns_cache_enabled'), ''),
			COALESCE((SELECT value FROM settings WHERE key = 'favicon_fetching_enabled'), '')
	`).Scan(&settings.cacheEnabled, &settings.faviconFetchingEnabled)
	return settings, err
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

// Health probes must not delay traffic views when the resolver is unavailable.
func (handler *Handler) cachedCoreDNSCacheMetrics() (cacheMetrics, bool) {
	if strings.TrimSpace(handler.metricsURL) == "" {
		return cacheMetrics{}, false
	}
	handler.cacheMetricsMu.Lock()
	defer handler.cacheMetricsMu.Unlock()
	if !handler.cacheMetricsRefreshing && (handler.cacheMetricsCheckedAt.IsZero() || time.Since(handler.cacheMetricsCheckedAt) > 15*time.Second) {
		handler.cacheMetricsRefreshing = true
		go func() {
			result := handler.coreDNSCacheMetrics(context.Background())
			handler.cacheMetricsMu.Lock()
			handler.cacheMetricsValue = result
			handler.cacheMetricsCheckedAt = time.Now()
			handler.cacheMetricsRefreshing = false
			handler.cacheMetricsMu.Unlock()
			handler.dashboardMu.Lock()
			handler.dashboardCache = dashboardCacheEntry{}
			handler.dashboardMu.Unlock()
		}()
	}
	return handler.cacheMetricsValue, handler.cacheMetricsCheckedAt.IsZero()
}

func (handler *Handler) coreDNSCacheMetrics(ctx context.Context) cacheMetrics {
	metrics, err := coredns.ReadMetrics(ctx, handler.metricsURL)
	if err != nil {
		return cacheMetrics{}
	}
	return cacheMetrics{available: true, entries: metrics.CacheEntries, hits: metrics.CacheHits, requests: metrics.CacheRequests}
}

type dashboardHighlights struct {
	replicasBehind      int
	behindName          string
	blocked             int
	newDevices          int
	reloadFailures      int
	topClients          []map[string]any
	topBlocked          []map[string]any
	upstreams           upstreamhealth.Snapshot
	dnsMetricsAvailable bool
	dnsMetricsPending   bool
}

func networkSummary(input dashboardHighlights) map[string]any {
	headline, messages := networkHeadline(input.blocked, input.upstreams, input.dnsMetricsAvailable, input.dnsMetricsPending)
	messages = appendNetworkHighlights(messages, input.topClients, input.topBlocked)
	if input.newDevices == 0 {
		messages = append(messages, "No new devices seen today.")
	} else if input.newDevices == 1 {
		messages = append(messages, "1 new device seen today.")
	} else {
		messages = append(messages, fmt.Sprintf("%d new devices seen today.", input.newDevices))
	}
	return map[string]any{"headline": headline, "messages": messages}
}

func networkHeadline(blocked int, upstreams upstreamhealth.Snapshot, dnsMetricsAvailable bool, pending ...bool) (string, []string) {
	headline := "Everything looks normal."
	messages := make([]string, 0, 4)
	switch {
	case len(pending) > 0 && pending[0]:
		return "Checking network health", []string{"Traffic is ready while Faro checks the DNS engine."}
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

func dashboardStories(input dashboardHighlights) []map[string]any {
	stories := make([]map[string]any, 0, 4)
	if input.replicasBehind > 0 {
		title := countedStory(input.replicasBehind, "backup server is behind.", "backup servers are behind.")
		if input.replicasBehind == 1 && input.behindName != "" {
			title = "Backup server " + input.behindName + " is behind."
		}
		warning := story(title, "Devices using a backup may still get older DNS rules.", "warning")
		warning["section"] = "redundancy"
		stories = append(stories, warning)
	}
	if input.dnsMetricsPending {
		stories = append(stories, story("Checking DNS engine health.", "Traffic is ready; the engine health check is still running.", "info"))
	} else if !input.dnsMetricsAvailable {
		stories = append(stories, story("DNS engine health is unavailable.", "Faro could not reach CoreDNS metrics.", "critical"))
	} else if input.upstreams.Status == "critical" {
		stories = append(stories, story("Upstream DNS is unavailable.", input.upstreams.Summary, "critical"))
	} else if input.upstreams.Status == "degraded" {
		stories = append(stories, story("Upstream DNS is degraded.", input.upstreams.Summary, "warning"))
	} else if input.reloadFailures > 0 {
		stories = append(stories, story("DNS needs attention.", fmt.Sprintf("%d reload failures detected today.", input.reloadFailures), "critical"))
	} else if input.replicasBehind == 0 {
		stories = append(stories, story("Everything looks healthy today.", "No DNS reload failures detected.", "success"))
	}
	if input.newDevices > 0 {
		stories = append(stories, story(countedStory(input.newDevices, "new device joined today.", "new devices joined today."), "New clients are now visible in Devices.", "info"))
	}
	if input.blocked > 0 {
		stories = append(stories, story("Filtering blocked "+countedStory(input.blocked, "request today.", "requests today."), firstLabelSentence("Most blocked domain", input.topBlocked), "success"))
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
	upstreams           upstreamhealth.Snapshot
	dnsMetricsAvailable bool
	dnsMetricsPending   bool
}

func healthCards(input healthCardsInput) []map[string]any {
	cacheAnswers, forwardedAnswers := input.cacheAnswers, input.forwardedAnswers
	return []map[string]any{
		dnsHealthCard(input.dnsMetricsAvailable, input.reloadFailures, input.dnsMetricsPending),
		upstreamHealthCard(input.upstreams),
		{"label": "Devices", "value": fmt.Sprintf("%d observed", input.deviceCount), "detail": "From local query data", "status": "healthy"},
		{"label": "Filtering", "value": fmt.Sprintf("%d domains", input.blockEntries), "detail": fmt.Sprintf("%d enabled lists", input.enabledBlocklists), "status": ternary(input.blockEntries > 0, "healthy", "warning")},
		{"label": "Cache", "value": fmt.Sprintf("%.1f%% hit rate", percentage(cacheAnswers, cacheAnswers+forwardedAnswers)), "detail": fmt.Sprintf("%d upstream calls avoided", cacheAnswers), "status": "info"},
		{"label": "Blocked", "value": fmt.Sprintf("%d today", input.blocked), "detail": fmt.Sprintf("%.1f%% of activity", percentage(input.blocked, input.total)), "status": ternary(input.blocked > 0, "warning", "healthy")},
	}
}

func dnsHealthCard(metricsAvailable bool, reloadFailures int, pending ...bool) map[string]any {
	if len(pending) > 0 && pending[0] {
		return map[string]any{"label": "DNS", "value": "Checking", "detail": "Engine health check in progress", "status": "info"}
	}
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

func (handler *Handler) whatsNew(request *http.Request, start string) (map[string]any, error) {
	devices, err := handler.newDashboardDevices(request, start)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"devices": devices}
	for _, item := range []struct {
		key, query string
		args       []any
	}{
		{"domains", `
			SELECT q.domain AS label, 'First time observed' AS subtitle
			FROM dns_queries q
			WHERE q.timestamp >= ?
			GROUP BY q.domain
			HAVING NOT EXISTS (
				SELECT 1 FROM dns_queries prior WHERE prior.domain = q.domain AND prior.timestamp < ?
			)
			ORDER BY MIN(q.timestamp) DESC, q.domain LIMIT 5`, []any{start, start}},
		{"blocklists", `SELECT id, name AS label, 'Installed today' AS subtitle FROM blocklists WHERE created_at >= datetime(?) ORDER BY created_at DESC, id DESC LIMIT 5`, []any{start}},
		{"local_records", `SELECT id, hostname AS label, 'Local DNS record' AS subtitle FROM dns_records WHERE created_at >= datetime(?) ORDER BY created_at DESC, id DESC LIMIT 5`, []any{start}},
	} {
		values, err := searchRowsResult(request.Context(), handler.store.DB, item.query, item.args...)
		if err != nil {
			return nil, err
		}
		result[item.key] = values
	}
	return result, nil
}

func (handler *Handler) newDashboardDevices(request *http.Request, start string) ([]map[string]any, error) {
	rows, err := handler.store.DB.QueryContext(request.Context(), `
		SELECT d.id, COALESCE(NULLIF(TRIM(d.name), ''), TRIM(n.name), '')
		FROM devices d
		LEFT JOIN device_names n ON n.device_id = d.id AND n.source = 'unifi'
		WHERE d.first_seen_at >= ?
		ORDER BY d.first_seen_at DESC, d.id DESC LIMIT 5`, start)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var devices []inventoryBaseDevice
	for rows.Next() {
		var device inventoryBaseDevice
		if err := rows.Scan(&device.deviceID, &device.name); err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	addressesByDevice, clientIPs, err := handler.inventoryAddresses(request, devices)
	if err != nil {
		return nil, err
	}
	discovered := handler.discoverDeviceNames(request.Context(), clientIPs)
	items := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		addresses := addressesByDevice[device.deviceID]
		address := ""
		if len(addresses) > 0 {
			address = addresses[0]
		}
		label := device.name
		if label == "" {
			label = bestDeviceIdentity(discovered, addresses, address).DisplayName
		}
		subtitle := "First seen today"
		if address != "" && address != label {
			subtitle = address
		}
		items = append(items, map[string]any{"device_id": device.deviceID, "client_ip": address, "label": label, "subtitle": subtitle})
	}
	return items, nil
}

type dashboardTrafficHour struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Total   int       `json:"total"`
	Blocked int       `json:"blocked"`
}

func dashboardSparklines(hours []dashboardTrafficHour) map[string][]int {
	activity, blocked := make([]int, len(hours)), make([]int, len(hours))
	for i, hour := range hours {
		activity[i], blocked[i] = hour.Total, hour.Blocked
	}
	return map[string][]int{"activity": activity, "blocked": blocked}
}

func dashboardTrafficHours(ctx context.Context, database *sql.DB, now time.Time) ([]dashboardTrafficHour, error) {
	const hourKey = "2006-01-02T15"
	start := now.UTC().Truncate(time.Hour).Add(-23 * time.Hour)
	// DNS history is collected to the second. Keep the current second, and
	// freeze the end so a drill-down uses the same snapshot interval.
	end := now.UTC().Truncate(time.Second).Add(time.Second)
	hours := make([]dashboardTrafficHour, 24)
	index := make(map[string]int, len(hours))
	for i := range hours {
		from := start.Add(time.Duration(i) * time.Hour)
		to := from.Add(time.Hour)
		if to.After(end) {
			to = end
		}
		hours[i] = dashboardTrafficHour{Start: from, End: to}
		index[from.Format(hourKey)] = i
	}
	clause, args := activityTimeClause("timestamp", activityWindow{enabled: true, from: start, to: end})
	rows, err := database.QueryContext(ctx, `
		SELECT replace(substr(timestamp, 1, 13), ' ', 'T') AS hour,
		       COUNT(*),
		       COALESCE(SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), 0)
		FROM dns_queries
		WHERE `+clause+` GROUP BY hour`, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	for rows.Next() {
		var hour string
		var total, blocked int
		if err := rows.Scan(&hour, &total, &blocked); err != nil {
			return nil, err
		}
		if i, ok := index[hour]; ok {
			hours[i].Total, hours[i].Blocked = total, blocked
		}
	}
	return hours, rows.Err()
}

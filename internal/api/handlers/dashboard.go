package handlers

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	start := todayStart(r)
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ?`, start)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ? AND action = 'blocked'`, start)
	enabledBlocklists := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklists WHERE enabled = 1`)
	blockEntries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM blocklist_entries`)
	topClients := grouped(r.Context(), s.store.DB, `SELECT client_ip, COUNT(*) FROM dns_queries WHERE timestamp >= ? GROUP BY client_ip ORDER BY COUNT(*) DESC LIMIT 5`, start)
	topBlocked := grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE timestamp >= ? AND action = 'blocked' GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 5`, start)
	deviceCount := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(DISTINCT client_ip) FROM dns_queries`)
	reloadFailures := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM events WHERE type = 'dns.reload_failed' AND timestamp >= ?`, start)
	cacheHits := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ? AND source = 'cache'`, start)
	upstreamQueries := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE timestamp >= ? AND source = 'upstream'`, start)
	cacheLatency := scalarFloat(r.Context(), s.store.DB, `SELECT COALESCE(AVG(latency_ms), 0) FROM dns_queries WHERE timestamp >= ? AND source = 'cache'`, start)
	upstreamLatency := scalarFloat(r.Context(), s.store.DB, `SELECT COALESCE(AVG(latency_ms), 0) FROM dns_queries WHERE timestamp >= ? AND source = 'upstream'`, start)
	liveCache := s.coreDNSCacheMetrics(r.Context())

	writeJSON(w, http.StatusOK, map[string]any{
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"block_percentage":      percentage(blocked, total),
		"enabled_blocklists":    enabledBlocklists,
		"blocklist_entries":     blockEntries,
		"cache": map[string]any{
			"enabled":                     settingValue(r.Context(), s.store.DB, "dns_cache_enabled") != "false",
			"metrics_available":           liveCache.available,
			"entries":                     liveCache.entries,
			"hits_since_restart":          liveCache.hits,
			"requests_since_restart":      liveCache.requests,
			"hit_rate_since_restart":      percentage64(liveCache.hits, liveCache.requests),
			"hits_today":                  cacheHits,
			"upstream_queries_today":      upstreamQueries,
			"hit_rate_today":              percentage(cacheHits, cacheHits+upstreamQueries),
			"average_cache_latency_ms":    cacheLatency,
			"average_upstream_latency_ms": upstreamLatency,
		},
		"network_summary":          networkSummary(r.Context(), s.store.DB, start, blocked, topClients, topBlocked),
		"health_cards":             healthCards(r.Context(), s.store.DB, total, blocked, enabledBlocklists, blockEntries, deviceCount, reloadFailures),
		"stories":                  dashboardStories(r.Context(), s.store.DB, start, blocked, topClients, topBlocked, reloadFailures),
		"whats_new":                whatsNew(r.Context(), s.store.DB, start),
		"sparklines":               dashboardSparklines(r.Context(), s.store.DB),
		"top_queried_domains":      grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 5`, start),
		"top_blocked_domains":      topBlocked,
		"top_clients":              topClients,
		"recent_activity":          recentQueries(r.Context(), s.store.DB),
		"upstream_health":          "Not checked yet",
		"upstream_health_status":   "placeholder",
		"favicon_fetching_enabled": settingValue(r.Context(), s.store.DB, "favicon_fetching_enabled"),
	})
}

type cacheMetrics struct {
	available bool
	entries   float64
	hits      float64
	requests  float64
}

func (s *Handler) coreDNSCacheMetrics(ctx context.Context) cacheMetrics {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, s.metricsURL, nil)
	if err != nil {
		return cacheMetrics{}
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return cacheMetrics{}
	}
	defer resp.Body.Close()
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

func networkSummary(ctx context.Context, database *sql.DB, start string, blocked int, topClients, topBlocked []map[string]any) map[string]any {
	headline := "Everything looks normal."
	messages := []string{}
	if blocked > 0 {
		headline = fmt.Sprintf("Faro blocked %d requests today.", blocked)
		messages = append(messages, headline)
	} else {
		messages = append(messages, "Everything looks normal.")
	}
	if len(topClients) > 0 {
		if label, ok := topClients[0]["label"].(string); ok && label != "" {
			messages = append(messages, "Top active device today: "+label+".")
		}
	}
	if len(topBlocked) > 0 {
		if label, ok := topBlocked[0]["label"].(string); ok && label != "" {
			messages = append(messages, "Most blocked domain: "+label+".")
		}
	}
	newDevices := scalarInt(ctx, database, `
		SELECT COUNT(*) FROM (
			SELECT client_ip, MIN(timestamp) AS first_seen
			FROM dns_queries
			GROUP BY client_ip
			HAVING first_seen >= ?
		)
	`, start)
	if newDevices == 0 {
		messages = append(messages, "No new devices seen today.")
	} else if newDevices == 1 {
		messages = append(messages, "1 new device seen today.")
	} else {
		messages = append(messages, fmt.Sprintf("%d new devices seen today.", newDevices))
	}
	return map[string]any{"headline": headline, "messages": messages}
}

func dashboardStories(ctx context.Context, database *sql.DB, start string, blocked int, topClients, topBlocked []map[string]any, reloadFailures int) []map[string]any {
	stories := []map[string]any{}
	if reloadFailures == 0 {
		stories = append(stories, story("Everything looks healthy today.", "No DNS reload failures detected.", "success"))
	} else {
		stories = append(stories, story("DNS needs attention.", fmt.Sprintf("%d reload failures detected today.", reloadFailures), "critical"))
	}
	newDevices := scalarInt(ctx, database, `SELECT COUNT(*) FROM (SELECT client_ip, MIN(timestamp) first_seen FROM dns_queries GROUP BY client_ip HAVING first_seen >= ?)`, start)
	if newDevices > 0 {
		stories = append(stories, story(fmt.Sprintf("%d new devices joined today.", newDevices), "New clients are now visible in Devices.", "info"))
	}
	if blocked > 0 {
		stories = append(stories, story(fmt.Sprintf("Filtering blocked %d requests today.", blocked), firstLabelSentence("Most blocked domain", topBlocked), "warning"))
	}
	if len(topClients) > 0 {
		stories = append(stories, story("Busiest device today.", firstLabelSentence("Top active device", topClients), "info"))
	}
	return stories
}

func story(title, body, tone string) map[string]any {
	return map[string]any{"title": title, "body": body, "tone": tone}
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

func healthCards(ctx context.Context, database *sql.DB, total, blocked, enabledBlocklists, blockEntries, deviceCount, reloadFailures int) []map[string]any {
	upstreams := strings.Split(settingValue(ctx, database, "upstream_dns"), ",")
	upstreamCount := 0
	for _, upstream := range upstreams {
		if strings.TrimSpace(upstream) != "" {
			upstreamCount++
		}
	}
	cacheAnswers := scalarInt(ctx, database, `SELECT COUNT(*) FROM dns_queries WHERE source = 'cache'`)
	forwardedAnswers := scalarInt(ctx, database, `SELECT COUNT(*) FROM dns_queries WHERE source = 'upstream'`)
	return []map[string]any{
		{"label": "DNS", "value": ternary(reloadFailures == 0, "Healthy", "Needs attention"), "detail": ternary(reloadFailures == 0, "Engine running", "Reload failures detected"), "status": ternary(reloadFailures == 0, "healthy", "critical")},
		{"label": "Upstreams", "value": fmt.Sprintf("%d configured", upstreamCount), "detail": "Ready for resolution", "status": "healthy"},
		{"label": "Devices", "value": fmt.Sprintf("%d observed", deviceCount), "detail": "From local query data", "status": "healthy"},
		{"label": "Filtering", "value": fmt.Sprintf("%d domains", blockEntries), "detail": fmt.Sprintf("%d enabled lists", enabledBlocklists), "status": ternary(blockEntries > 0, "healthy", "warning")},
		{"label": "Cache", "value": fmt.Sprintf("%.1f%% hit rate", percentage(cacheAnswers, cacheAnswers+forwardedAnswers)), "detail": fmt.Sprintf("%d upstream calls avoided", cacheAnswers), "status": "info"},
		{"label": "Blocked", "value": fmt.Sprintf("%d today", blocked), "detail": fmt.Sprintf("%.1f%% of activity", percentage(blocked, total)), "status": ternary(blocked > 0, "warning", "healthy")},
	}
}

func whatsNew(ctx context.Context, database *sql.DB, start string) map[string]any {
	return map[string]any{
		"devices":       searchRows(ctx, database, `SELECT client_ip AS label, 'First seen today' AS subtitle FROM (SELECT client_ip, MIN(timestamp) first_seen FROM dns_queries GROUP BY client_ip HAVING first_seen >= ?) LIMIT 5`, start),
		"domains":       searchRows(ctx, database, `SELECT domain AS label, 'First time observed' AS subtitle FROM (SELECT domain, MIN(timestamp) first_seen FROM dns_queries GROUP BY domain HAVING first_seen >= ?) LIMIT 5`, start),
		"blocklists":    searchRows(ctx, database, `SELECT name AS label, 'Installed today' AS subtitle FROM blocklists WHERE created_at >= ? ORDER BY created_at DESC LIMIT 5`, start),
		"local_records": searchRows(ctx, database, `SELECT hostname AS label, 'Local DNS record' AS subtitle FROM dns_records WHERE created_at >= ? ORDER BY created_at DESC LIMIT 5`, start),
	}
}

func dashboardSparklines(ctx context.Context, database *sql.DB) map[string]any {
	return map[string]any{
		"activity": hourlyCounts(ctx, database, `SELECT strftime('%H', timestamp) AS hour, COUNT(*) FROM dns_queries WHERE timestamp >= datetime('now', '-24 hours') GROUP BY hour`),
		"blocked":  hourlyCounts(ctx, database, `SELECT strftime('%H', timestamp) AS hour, COUNT(*) FROM dns_queries WHERE timestamp >= datetime('now', '-24 hours') AND action = 'blocked' GROUP BY hour`),
	}
}

func hourlyCounts(ctx context.Context, database *sql.DB, query string) []int {
	values := make([]int, 24)
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return values
	}
	defer rows.Close()
	nowHour := time.Now().UTC().Hour()
	for rows.Next() {
		var hourText string
		var count int
		if err := rows.Scan(&hourText, &count); err != nil {
			return values
		}
		hour, err := strconv.Atoi(hourText)
		if err != nil {
			continue
		}
		index := (hour - nowHour + 23 + 24) % 24
		values[index] = count
	}
	return values
}

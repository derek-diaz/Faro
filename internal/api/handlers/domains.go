package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/derek/faro/internal/db"
)

func (handler *Handler) domainSummary(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/domains/"), "/")
	if !strings.HasSuffix(path, "/summary") {
		http.NotFound(responseWriter, request)
		return
	}
	rawDomain, err := url.PathUnescape(strings.TrimSuffix(path, "/summary"))
	if err != nil {
		writeBadRequest(responseWriter, errors.New("invalid domain"))
		return
	}
	domain, err := db.NormalizeDomain(rawDomain)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	start := todayStart(request)
	var total, blocked, allowedAll, blockedAll int
	var firstSeen, lastSeen sql.NullString
	_ = handler.store.DB.QueryRowContext(request.Context(), `
		SELECT
			COALESCE(SUM(CASE WHEN timestamp >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN timestamp >= ? AND action = 'blocked' THEN 1 ELSE 0 END), 0),
			MIN(timestamp),
			MAX(timestamp),
			COALESCE(SUM(CASE WHEN action = 'allowed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), 0)
		FROM dns_queries
		WHERE domain = ?
	`, start, start, domain).Scan(&total, &blocked, &firstSeen, &lastSeen, &allowedAll, &blockedAll)
	status := "Allowed"
	if allowedAll > 0 && blockedAll > 0 {
		status = "Mixed"
	} else if blockedAll > 0 {
		status = "Blocked"
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"domain":                domain,
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"first_seen":            nullableString(firstSeen),
		"last_seen":             nullableString(lastSeen),
		"clients":               grouped(request.Context(), handler.store.DB, `SELECT client_ip, COUNT(*) FROM dns_queries WHERE domain = ? GROUP BY client_ip ORDER BY COUNT(*) DESC, client_ip LIMIT 8`, domain),
		"query_types":           grouped(request.Context(), handler.store.DB, `SELECT query_type, COUNT(*) FROM dns_queries WHERE domain = ? GROUP BY query_type ORDER BY COUNT(*) DESC, query_type`, domain),
		"status":                status,
		"recent_queries":        recentQueriesFor(request.Context(), handler.store.DB, `domain = ?`, domain),
		"recent_events":         localEvents(request.Context(), handler.store.DB, 12, domain),
	})
}

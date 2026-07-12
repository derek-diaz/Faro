package handlers

import (
	"database/sql"
	"errors"
	"github.com/derek/faro/internal/db"
	"net/http"
	"net/url"
	"strings"
)

func (s *Handler) domainSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/domains/"), "/")
	if !strings.HasSuffix(path, "/summary") {
		http.NotFound(w, r)
		return
	}
	rawDomain, err := url.PathUnescape(strings.TrimSuffix(path, "/summary"))
	if err != nil {
		writeBadRequest(w, errors.New("invalid domain"))
		return
	}
	domain, err := db.NormalizeDomain(rawDomain)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	start := todayStart()
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE domain = ? AND timestamp >= ?`, domain, start)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE domain = ? AND timestamp >= ? AND action = 'blocked'`, domain, start)
	var firstSeen, lastSeen sql.NullString
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT MIN(timestamp), MAX(timestamp) FROM dns_queries WHERE domain = ?`, domain).Scan(&firstSeen, &lastSeen)
	allowedAll := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE domain = ? AND action = 'allowed'`, domain)
	blockedAll := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE domain = ? AND action = 'blocked'`, domain)
	status := "Allowed"
	if allowedAll > 0 && blockedAll > 0 {
		status = "Mixed"
	} else if blockedAll > 0 {
		status = "Blocked"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domain":                domain,
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"first_seen":            nullableString(firstSeen),
		"last_seen":             nullableString(lastSeen),
		"clients":               grouped(r.Context(), s.store.DB, `SELECT client_ip, COUNT(*) FROM dns_queries WHERE domain = ? GROUP BY client_ip ORDER BY COUNT(*) DESC, client_ip LIMIT 8`, domain),
		"query_types":           grouped(r.Context(), s.store.DB, `SELECT query_type, COUNT(*) FROM dns_queries WHERE domain = ? GROUP BY query_type ORDER BY COUNT(*) DESC, query_type`, domain),
		"status":                status,
		"recent_queries":        recentQueriesFor(r.Context(), s.store.DB, `domain = ?`, domain),
		"recent_events":         localEvents(r.Context(), s.store.DB, 12, domain),
	})
}

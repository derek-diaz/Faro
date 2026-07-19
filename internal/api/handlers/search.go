package handlers

import (
	"net/http"
	"strings"
)

func (s *Handler) search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"domains":       []map[string]any{},
			"devices":       []map[string]any{},
			"events":        []map[string]any{},
			"local_records": []map[string]any{},
			"rules":         []map[string]any{},
			"blocklists":    []map[string]any{},
		})
		return
	}
	like := "%" + q + "%"
	writeJSON(w, http.StatusOK, map[string]any{
		"domains": grouped(r.Context(), s.store.DB, `
			SELECT domain, COUNT(*) FROM dns_queries
			WHERE domain LIKE ?
			GROUP BY domain
			ORDER BY MAX(timestamp) DESC
			LIMIT 8
		`, like),
		"devices": searchRows(r.Context(), s.store.DB, `
			WITH clients AS (
				SELECT client_ip FROM dns_queries
				UNION
				SELECT client_ip FROM device_aliases
			)
			SELECT clients.client_ip AS label, COALESCE(a.name, '') AS subtitle
			FROM clients
			LEFT JOIN device_aliases a ON a.client_ip = clients.client_ip
			WHERE clients.client_ip LIKE ? OR a.name LIKE ?
			ORDER BY COALESCE(a.name, clients.client_ip)
			LIMIT 8
		`, like, like),
		"events": searchRows(r.Context(), s.store.DB, `
			SELECT title AS label, type || ' · ' || description AS subtitle
			FROM events
			WHERE title LIKE ? OR description LIKE ? OR type LIKE ? OR domain LIKE ? OR client_ip LIKE ?
			ORDER BY timestamp DESC
			LIMIT 8
		`, like, like, like, like, like),
		"local_records": searchRows(r.Context(), s.store.DB, `
			SELECT hostname AS label, type || ' ' || value AS subtitle
			FROM dns_records
			WHERE hostname LIKE ? OR value LIKE ? OR description LIKE ?
			ORDER BY hostname
			LIMIT 8
		`, like, like, like),
		"rules": searchRows(r.Context(), s.store.DB, `
			SELECT e.domain AS label, 'Allowed in ' || p.name AS subtitle
			FROM protection_allow_entries e JOIN protection_profiles p ON p.id = e.protection_id
			WHERE e.domain LIKE ?
			UNION ALL
			SELECT e.domain AS label, 'Blocked in ' || p.name AS subtitle
			FROM protection_block_entries e JOIN protection_profiles p ON p.id = e.protection_id
			WHERE e.domain LIKE ?
			ORDER BY label
			LIMIT 8
		`, like, like),
		"blocklists": searchRows(r.Context(), s.store.DB, `
			SELECT name AS label, url AS subtitle
			FROM blocklists
			WHERE name LIKE ? OR url LIKE ?
			ORDER BY name
			LIMIT 8
		`, like, like),
	})
}

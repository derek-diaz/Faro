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
			SELECT
				(SELECT address FROM device_addresses WHERE device_id = d.id ORDER BY last_seen_at DESC, id DESC LIMIT 1) AS label,
				COALESCE(NULLIF(d.name, ''), 'Observed device') AS subtitle
			FROM devices d
			WHERE d.name LIKE ? OR EXISTS (SELECT 1 FROM device_addresses a WHERE a.device_id = d.id AND a.address LIKE ?)
			ORDER BY COALESCE(NULLIF(d.name, ''), label)
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

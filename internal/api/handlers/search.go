package handlers

import (
	"errors"
	"net/http"
	"strings"
)

func (handler *Handler) search(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	if len(query) > maxActivitySearchLength {
		writeBadRequest(responseWriter, errors.New("search must be 100 characters or fewer"))
		return
	}
	if query == "" {
		writeJSON(responseWriter, http.StatusOK, map[string]any{
			"domains":       []map[string]any{},
			"devices":       []map[string]any{},
			"events":        []map[string]any{},
			"local_records": []map[string]any{},
			"rules":         []map[string]any{},
			"blocklists":    []map[string]any{},
		})
		return
	}
	like := "%" + query + "%"
	queries := []struct{ group, sql string }{
		{"domains", `
			-- Seek to each distinct name, skipping repeated queries in the domain index.
			WITH RECURSIVE names(domain) AS (
				SELECT MIN(domain) FROM dns_queries
				UNION ALL
				SELECT (SELECT MIN(domain) FROM dns_queries WHERE domain > names.domain)
				FROM names WHERE domain IS NOT NULL
			), recent AS MATERIALIZED (
				SELECT domain, (SELECT MAX(timestamp) FROM dns_queries WHERE domain = names.domain) AS latest
				FROM names WHERE domain LIKE ?1
				ORDER BY latest DESC, domain
				LIMIT 8
			)
			SELECT domain AS label, (SELECT COUNT(*) FROM dns_queries WHERE domain = recent.domain) AS count
			FROM recent ORDER BY latest DESC, domain
		`},
		{"devices", `
			SELECT d.id,
				COALESCE((SELECT address FROM device_addresses WHERE device_id = d.id ORDER BY last_seen_at DESC, id DESC LIMIT 1), NULLIF(d.name, ''), 'Device ' || d.id) AS label,
				COALESCE(NULLIF(d.name, ''), 'Observed device') AS subtitle
			FROM devices d
			WHERE d.name LIKE ?1 OR EXISTS (SELECT 1 FROM device_addresses a WHERE a.device_id = d.id AND a.address LIKE ?1)
			ORDER BY COALESCE(NULLIF(d.name, ''), label)
			LIMIT 8
		`},
		{"events", `
			SELECT id, title AS label, type || ' · ' || description AS subtitle
			FROM events
			WHERE title LIKE ?1 OR description LIKE ?1 OR type LIKE ?1 OR domain LIKE ?1 OR client_ip LIKE ?1
			ORDER BY timestamp DESC
			LIMIT 8
		`},
		{"local_records", `
			SELECT id, hostname AS label, type || ' ' || value AS subtitle
			FROM dns_records
			WHERE hostname LIKE ?1 OR value LIKE ?1 OR description LIKE ?1
			ORDER BY hostname
			LIMIT 8
		`},
		{"rules", `
			SELECT p.id, 'allow' AS kind, e.domain AS label, 'Allowed in ' || p.name AS subtitle
			FROM protection_allow_entries e JOIN protection_profiles p ON p.id = e.protection_id
			WHERE e.domain LIKE ?1
			UNION ALL
			SELECT p.id, 'block' AS kind, e.domain AS label, 'Blocked in ' || p.name AS subtitle
			FROM protection_block_entries e JOIN protection_profiles p ON p.id = e.protection_id
			WHERE e.domain LIKE ?1
			ORDER BY label
			LIMIT 8
		`},
		{"blocklists", `
			SELECT id, name AS label, url AS subtitle
			FROM blocklists
			WHERE name LIKE ?1 OR url LIKE ?1
			ORDER BY name
			LIMIT 8
		`},
	}
	results := make(map[string][]map[string]any, len(queries))
	for _, group := range queries {
		items, err := searchRowsResult(request.Context(), handler.store.DB, group.sql, like)
		if err != nil {
			writeError(responseWriter, err)
			return
		}
		results[group.group] = items
	}
	writeJSON(responseWriter, http.StatusOK, results)
}

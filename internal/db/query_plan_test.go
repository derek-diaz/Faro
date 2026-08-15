package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryHotReadPlansUseRangeAndLookupIndexes(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for index := 0; index < 200; index++ {
		if _, err := store.DB.Exec(`
			INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source)
			VALUES(?, ?, ?, 'A', ?, ?)
		`,
			"2026-08-12T12:00:00Z", "192.0.2.10", "plan-"+string(rune('a'+index%26))+".example", "allowed", "upstream"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value) VALUES('router.home', 'A', '192.0.2.1')`); err != nil {
		t.Fatal(err)
	}

	plans := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "timestamp range",
			query: `EXPLAIN QUERY PLAN SELECT id FROM dns_queries WHERE timestamp >= '2026-08-01T00:00:00Z' AND timestamp < '2026-08-13T00:00:00Z' ORDER BY timestamp DESC, id DESC LIMIT 50`,
			want:  "SEARCH dns_queries USING COVERING INDEX idx_dns_queries_timestamp",
		},
		{
			name:  "blocked range",
			query: `EXPLAIN QUERY PLAN SELECT id FROM dns_queries WHERE action = 'blocked' AND timestamp >= '2026-08-01T00:00:00Z' ORDER BY timestamp DESC, id DESC LIMIT 50`,
			want:  "SEARCH dns_queries USING COVERING INDEX idx_dns_queries_action_timestamp",
		},
		{
			name:  "local record lookup",
			query: `EXPLAIN QUERY PLAN SELECT 1 FROM dns_records WHERE value = '192.0.2.1' AND type IN ('A', 'AAAA') LIMIT 1`,
			want:  "SEARCH dns_records USING COVERING INDEX idx_dns_records_value_type_hostname",
		},
	}

	for _, plan := range plans {
		rows, err := store.DB.Query(plan.query)
		if err != nil {
			t.Fatalf("%s plan: %v", plan.name, err)
		}
		var details []string
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				_ = rows.Close()
				t.Fatalf("%s plan scan: %v", plan.name, err)
			}
			details = append(details, detail)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("%s plan close: %v", plan.name, err)
		}
		joined := strings.Join(details, "\n")
		if !strings.Contains(joined, plan.want) {
			t.Fatalf("%s plan = %q, want %q", plan.name, joined, plan.want)
		}
		if plan.name == "timestamp range" && strings.Contains(joined, "USE TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("%s plan still sorts into a temporary B-tree: %q", plan.name, joined)
		}
	}
}

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestDomainSearchRanksDistinctMatchesByLatestRequest(t *testing.T) {
	h, _, _ := troubleshootingFixture(t)
	_, err := h.store.DB.Exec(`
		WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<9)
		INSERT INTO dns_queries(timestamp,client_ip,domain,query_type,action,source)
		SELECT '2026-01-02', '192.0.2.1', 'www.match-'||i||'.example', 'A', 'allowed', 'upstream' FROM n;
		INSERT INTO dns_queries(timestamp,client_ip,domain,query_type,action,source) VALUES
			('2026-01-03','192.0.2.2','www.match-9.example','A','blocked','upstream'),
			('2026-01-01','192.0.2.2','www.match-0.example','AAAA','allowed','cache'),
			('2026-01-01','192.0.2.3','www.match-0.example','A','blocked','upstream'),
			('2026-01-04','192.0.2.1','unrelated.example','A','allowed','upstream')
	`)
	if err != nil {
		t.Fatal(err)
	}
	type domainCount struct {
		Label string
		Count int
	}
	want := []domainCount{
		{"www.match-9.example", 2}, {"www.match-0.example", 3}, {"www.match-1.example", 1}, {"www.match-2.example", 1},
		{"www.match-3.example", 1}, {"www.match-4.example", 1}, {"www.match-5.example", 1}, {"www.match-6.example", 1},
	}
	for _, state := range []string{"fresh", "analyzed"} {
		t.Run(state, func(t *testing.T) {
			if state == "analyzed" {
				if _, err := h.store.DB.Exec("ANALYZE"); err != nil {
					t.Fatal(err)
				}
			}
			response := httptest.NewRecorder()
			h.search(response, httptest.NewRequest(http.MethodGet, "/api/search?q=AtCh-", nil))
			var results struct{ Domains []domainCount }
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &results) != nil {
				t.Fatal(response.Body.String())
			}
			if !slices.Equal(results.Domains, want) {
				t.Fatalf("domains = %+v, want %+v", results.Domains, want)
			}
		})
	}
}

func TestSearchLinksToSpecificRulesAndEvents(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	result, err := h.store.DB.Exec(`INSERT INTO events(type, severity, title, description) VALUES('test', 'info', 'broken.example', 'Renamed device')`)
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.store.DB.Exec(`INSERT INTO protection_allow_entries(protection_id,domain)
		SELECT id, 'broken.example' FROM protection_profiles WHERE is_default=1`)
	if err != nil {
		t.Fatal(err)
	}
	search := httptest.NewRecorder()
	h.search(search, httptest.NewRequest(http.MethodGet, "/api/search?q=broken.example", nil))
	var matches struct {
		Rules []struct {
			ID          int64
			Kind, Label string
		}
		Events []struct{ ID int64 }
	}
	if err := json.Unmarshal(search.Body.Bytes(), &matches); err != nil || search.Code != http.StatusOK {
		t.Fatalf("search returned %d: %s", search.Code, search.Body.String())
	}
	if len(matches.Rules) != 2 || len(matches.Events) != 1 || matches.Events[0].ID != eventID {
		t.Fatalf("unexpected destinations: %+v", matches)
	}
	for _, rule := range matches.Rules {
		if rule.ID <= 0 || rule.Label != "broken.example" || (rule.ID == input.ProtectionID && rule.Kind != "block") || (rule.ID != input.ProtectionID && rule.Kind != "allow") {
			t.Fatalf("incorrect destination for rule: %+v", rule)
		}
	}
	selected := httptest.NewRecorder()
	h.event(selected, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/events/%d", eventID), nil))
	var event struct{ ID, Title, Description string }
	if err := json.Unmarshal(selected.Body.Bytes(), &event); err != nil || selected.Code != http.StatusOK || event.ID != fmt.Sprintf("event-%d", eventID) || event.Title != "broken.example" || event.Description != "Renamed device" {
		t.Fatalf("incorrect event: %d %s", selected.Code, selected.Body.String())
	}
	missing := httptest.NewRecorder()
	h.event(missing, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/events/%d", eventID+1), nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing event returned %d", missing.Code)
	}
}

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/derek/faro/internal/db"
)

func TestEventsPaginationUsesFullActivityHistory(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for index := 1; index <= 5; index++ {
		source := "upstream"
		action := "allowed"
		if index%2 == 0 {
			source = "cache"
		}
		if index == 5 {
			action = "blocked"
		}
		_, err := store.DB.Exec(`
			INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source)
			VALUES(?, '192.168.7.20', ?, 'A', ?, ?)
		`, fmt.Sprintf("2026-07-12T12:00:%02dZ", index), fmt.Sprintf("domain%d.example", index), action, source)
		if err != nil {
			t.Fatal(err)
		}
	}

	handler := &Handler{store: store}
	request := httptest.NewRequest(http.MethodGet, "/api/events?scope=dns&page=2&page_size=2", nil)
	response := httptest.NewRecorder()
	handler.events(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []struct {
			Domain string `json:"domain"`
		} `json:"items"`
		Page       int            `json:"page"`
		PageSize   int            `json:"page_size"`
		Total      int            `json:"total"`
		TotalPages int            `json:"total_pages"`
		Counts     map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Page != 2 || payload.PageSize != 2 || payload.Total != 5 || payload.TotalPages != 3 {
		t.Fatalf("unexpected pagination: %#v", payload)
	}
	if len(payload.Items) != 2 || payload.Items[0].Domain != "domain3.example" || payload.Items[1].Domain != "domain2.example" {
		t.Fatalf("unexpected page: %#v", payload.Items)
	}
	if payload.Counts["dns"] != 5 || payload.Counts["cache"] != 2 || payload.Counts["upstream"] != 3 || payload.Counts["blocked"] != 1 {
		t.Fatalf("unexpected counts: %#v", payload.Counts)
	}
}

func TestEventsPaginationMergesPersistedAndDerivedActivity(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.DB.Exec(`
		INSERT INTO events(timestamp, type, severity, title, description, source)
		VALUES('2026-07-12T12:00:03Z', 'dns.reload_failed', 'critical', 'Reload failed', 'CoreDNS rejected a configuration.', 'dns')
	`); err != nil {
		t.Fatal(err)
	}
	for index, timestamp := range []string{"2026-07-12T12:00:02Z", "2026-07-12T12:00:01Z"} {
		if _, err := store.DB.Exec(`
			INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source)
			VALUES(?, '192.168.7.21', ?, 'A', 'allowed', 'upstream')
		`, timestamp, fmt.Sprintf("merged%d.example", index)); err != nil {
			t.Fatal(err)
		}
	}

	handler := &Handler{store: store}
	requestPage := func(page int) struct {
		Items []struct {
			ID string `json:"id"`
		}
		Page       int            `json:"page"`
		Total      int            `json:"total"`
		TotalPages int            `json:"total_pages"`
		Counts     map[string]int `json:"counts"`
	} {
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/events?scope=all&page=%d&page_size=2", page), nil)
		response := httptest.NewRecorder()
		handler.events(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("page %d status = %d, body = %s", page, response.Code, response.Body.String())
		}
		var payload struct {
			Items []struct {
				ID string `json:"id"`
			}
			Page       int            `json:"page"`
			Total      int            `json:"total"`
			TotalPages int            `json:"total_pages"`
			Counts     map[string]int `json:"counts"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}

	first := requestPage(1)
	second := requestPage(2)
	if first.Page != 1 || first.Total != 4 || first.TotalPages != 2 {
		t.Fatalf("unexpected first-page metadata: %#v", first)
	}
	if len(first.Items) != 2 || first.Items[0].ID != "event-1" || first.Items[1].ID != "query-1" {
		t.Fatalf("unexpected first page: %#v", first.Items)
	}
	if second.Page != 2 || len(second.Items) != 2 || second.Items[0].ID != "device-first-seen-192.168.7.21" || second.Items[1].ID != "query-2" {
		t.Fatalf("unexpected second page: %#v", second.Items)
	}
	if first.Counts["all"] != 4 || first.Counts["dns"] != 2 || first.Counts["system"] != 2 {
		t.Fatalf("unexpected merged counts: %#v", first.Counts)
	}
}

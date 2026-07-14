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

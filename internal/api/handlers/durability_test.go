package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
	"github.com/mattn/go-sqlite3"
)

func TestMaintenanceSeparatesPausedActivityStorageFromDNSHealth(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.ReportActivityWriteFailure(sqlite3.Error{Code: sqlite3.ErrFull})

	handler := &Handler{store: store, startedAt: time.Now()}
	healthResponse := httptest.NewRecorder()
	handler.health(healthResponse, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", healthResponse.Code, healthResponse.Body.String())
	}
	var healthPayload struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(healthResponse.Body.Bytes(), &healthPayload); err != nil {
		t.Fatal(err)
	}
	if !healthPayload.OK {
		t.Fatal("DNS/API health was not healthy while activity storage was paused")
	}

	maintenanceResponse := httptest.NewRecorder()
	handler.maintenance(maintenanceResponse, httptest.NewRequest(http.MethodGet, "/api/maintenance", nil))
	if maintenanceResponse.Code != http.StatusOK {
		t.Fatalf("maintenance status = %d, body = %s", maintenanceResponse.Code, maintenanceResponse.Body.String())
	}
	var maintenancePayload struct {
		Status  string `json:"status"`
		Storage struct {
			ActivityStorage db.ActivityStorageStatus `json:"activity_storage"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(maintenanceResponse.Body.Bytes(), &maintenancePayload); err != nil {
		t.Fatal(err)
	}
	if maintenancePayload.Status != "healthy" {
		t.Fatalf("maintenance status = %q, want healthy", maintenancePayload.Status)
	}
	if maintenancePayload.Storage.ActivityStorage.Status != db.ActivityStoragePaused || maintenancePayload.Storage.ActivityStorage.Reason != "Insufficient disk space" {
		t.Fatalf("maintenance did not surface activity storage pause: %+v", maintenancePayload.Storage.ActivityStorage)
	}
}

func TestActivityEndpointsRejectOversizedSearches(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := &Handler{store: store}
	search := strings.Repeat("x", maxActivitySearchLength+1)

	for _, endpoint := range []string{
		"/api/queries?search=" + search,
		"/api/events?search=" + search,
		"/api/search?q=" + search,
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, endpoint, nil)
		switch {
		case strings.HasPrefix(endpoint, "/api/queries"):
			handler.queries(response, request)
		case strings.HasPrefix(endpoint, "/api/events"):
			handler.events(response, request)
		default:
			handler.search(response, request)
		}
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d", endpoint, response.Code, http.StatusBadRequest)
		}
	}
}

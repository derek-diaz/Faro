package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
)

type diagnosticsTestReloader struct {
	diagnostics coredns.Diagnostics
}

func (reloader *diagnosticsTestReloader) Apply(context.Context) error {
	return nil
}

func (reloader *diagnosticsTestReloader) Diagnostics(context.Context) (coredns.Diagnostics, error) {
	return reloader.diagnostics, nil
}

func TestCoreDNSDiagnosticsEndpointReturnsReadOnlyState(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close test database: %v", closeErr)
		}
	}()
	reloader := &diagnosticsTestReloader{diagnostics: coredns.Diagnostics{
		Status:       "healthy",
		GeneratedAt:  "2026-08-08T00:00:00Z",
		ReloadsTotal: 4,
		Files: []coredns.DiagnosticFile{{
			Name:       "Corefile",
			Kind:       "corefile",
			Active:     ".:53 {}",
			Referenced: true,
			Matches:    true,
		}},
	}}
	handler := New(store, reloader, nil)
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/diagnostics/coredns", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated diagnostics status = %d; want %d", unauthenticated.Code, http.StatusUnauthorized)
	}

	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"test-admin","password":"correct-horse-battery-staple"}`))
	setup.Header.Set("Content-Type", "application/json")
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, setup)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", setupResponse.Code, setupResponse.Body.String())
	}
	cookies := setupResponse.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("setup did not return a session cookie")
	}

	request := httptest.NewRequest(http.MethodGet, "/api/diagnostics/coredns", nil)
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("diagnostics status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload coredns.Diagnostics
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "healthy" || len(payload.Files) != 1 || payload.Files[0].Name != "Corefile" {
		t.Fatalf("unexpected diagnostics payload: %#v", payload)
	}

	writeRequest := httptest.NewRequest(http.MethodPost, "/api/diagnostics/coredns", nil)
	writeRequest.AddCookie(cookies[0])
	writeResponse := httptest.NewRecorder()
	handler.ServeHTTP(writeResponse, writeRequest)
	if writeResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("diagnostics write status = %d; want %d", writeResponse.Code, http.StatusMethodNotAllowed)
	}
}

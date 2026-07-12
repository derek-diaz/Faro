package handlers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/derek/faro/internal/db"
)

type testReloader struct {
	calls int
}

func (r *testReloader) Apply(context.Context) error {
	r.calls++
	return nil
}

func newTestServer(t *testing.T) (http.Handler, *testReloader) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reloader := &testReloader{}
	return New(store, reloader), reloader
}

func TestHealth(t *testing.T) {
	handler, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["service"] != "faro-api" || payload["ok"] != true {
		t.Fatalf("unexpected health payload: %#v", payload)
	}
}

func TestAllowlistLifecycle(t *testing.T) {
	handler, reloader := newTestServer(t)
	create := httptest.NewRequest(http.MethodPost, "/api/allowlist", bytes.NewBufferString(`{"domain":" Example.COM. "}`))
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/allowlist", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d", listResponse.Code)
	}
	var entries []map[string]any
	if err := json.Unmarshal(listResponse.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode allowlist: %v", err)
	}
	if len(entries) != 1 || entries[0]["domain"] != "example.com" {
		t.Fatalf("unexpected allowlist: %#v", entries)
	}
	if reloader.calls != 1 {
		t.Fatalf("reload calls = %d, want 1", reloader.calls)
	}
}

func TestDNSProbeQuery(t *testing.T) {
	const id = uint16(0x4a3c)
	packet := dnsProbeQuery(id, "example.com")
	if len(packet) < 12 {
		t.Fatalf("packet too short: %d", len(packet))
	}
	if got := binary.BigEndian.Uint16(packet[:2]); got != id {
		t.Fatalf("query id = %#x, want %#x", got, id)
	}
	if got := binary.BigEndian.Uint16(packet[4:6]); got != 1 {
		t.Fatalf("question count = %d, want 1", got)
	}
	wantQuestion := []byte{7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	if !bytes.Contains(packet, wantQuestion) {
		t.Fatalf("query does not contain encoded hostname: %v", packet)
	}
}

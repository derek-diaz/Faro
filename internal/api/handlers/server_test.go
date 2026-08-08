package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/derek/faro/internal/db"
	faroversion "github.com/derek/faro/internal/version"
)

type testReloader struct {
	calls int
	err   error
}

func (reloader *testReloader) Apply(context.Context) error {
	reloader.calls++
	return reloader.err
}

func TestCrossOriginRequestsAreRejected(t *testing.T) {
	handler, _ := newTestServer(t)
	complete := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"onboarding_completed":"true"}`))
	complete.Header.Set("Content-Type", "application/json")
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, complete)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete onboarding status = %d, body = %s", completeResponse.Code, completeResponse.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestSameOriginUsesTrustedForwardedHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://api:8080/api/settings", nil)
	request.Host = "api:8080"
	request.Header.Set("X-Forwarded-Host", "localhost:1787")
	request.Header.Set("X-Forwarded-Proto", "http")
	const origin = "http://localhost:1787"
	if !sameOrigin(request, origin, true) {
		t.Fatal("trusted forwarded host should match the browser-facing origin")
	}
	if sameOrigin(request, origin, false) {
		t.Fatal("forwarded host must be ignored when proxy trust is disabled")
	}
}

func TestCrossOriginRequestsAreAllowedDuringOnboarding(t *testing.T) {
	handler, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"faro_lan_ip":"192.168.1.20"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://setup-device.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("onboarding settings status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestFirstRunSetupDoesNotRequireSameOrigin(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler := New(store, &testReloader{}, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"test-admin","password":"correct-horse-battery-staple"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://setup-device.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("first-run setup status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestReplicaRejectsConfigurationWritesAtServerBoundary(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`UPDATE redundancy_state SET role = 'replica' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	called := false
	handler := replicaReadOnly(store, http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		called = true
		responseWriter.WriteHeader(http.StatusNoContent)
	}))

	writeResponse := httptest.NewRecorder()
	handler.ServeHTTP(writeResponse, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{}`)))
	if writeResponse.Code != http.StatusConflict || called {
		t.Fatalf("replica write status = %d, downstream called = %v", writeResponse.Code, called)
	}
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if readResponse.Code != http.StatusNoContent || !called {
		t.Fatalf("replica read status = %d, downstream called = %v", readResponse.Code, called)
	}

	called = false
	leaveResponse := httptest.NewRecorder()
	handler.ServeHTTP(leaveResponse, httptest.NewRequest(http.MethodDelete, "/api/redundancy", nil))
	if leaveResponse.Code != http.StatusNoContent || !called {
		t.Fatalf("replica leave status = %d, downstream called = %v", leaveResponse.Code, called)
	}

	called = false
	lookalikeResponse := httptest.NewRecorder()
	handler.ServeHTTP(lookalikeResponse, httptest.NewRequest(http.MethodDelete, "/api/redundancy/nodes/example", nil))
	if lookalikeResponse.Code != http.StatusConflict || called {
		t.Fatalf("replica lookalike write status = %d, downstream called = %v", lookalikeResponse.Code, called)
	}

	called = false
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{}`)))
	if loginResponse.Code != http.StatusNoContent || !called {
		t.Fatalf("replica login status = %d, downstream called = %v", loginResponse.Code, called)
	}
}

func TestUnconfiguredReplicaExitStillRejectsCrossOriginRequests(t *testing.T) {
	handler := cors(false, func(context.Context) bool { return false }, http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodDelete, "http://faro.local/api/redundancy", nil)
	request.Host = "faro.local"
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin replica exit status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestFailedDNSReloadRestoresPreviousSettings(t *testing.T) {
	handler, reloader := newTestServer(t)
	reloader.err = errors.New("invalid generated configuration")
	request := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"upstream_dns":"8.8.8.8"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("settings status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if !bytes.Contains(read.Body.Bytes(), []byte(`"value":"1.1.1.1,9.9.9.9"`)) {
		t.Fatalf("previous upstream setting was not restored: %s", read.Body.String())
	}
}

func newTestServer(t *testing.T) (http.Handler, *testReloader) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reloader := &testReloader{}
	handler := New(store, reloader, nil)
	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"test-admin","password":"correct-horse-battery-staple"}`))
	setup.Header.Set("Content-Type", "application/json")
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, setup)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup authentication: status = %d, body = %s", setupResponse.Code, setupResponse.Body.String())
	}
	cookies := setupResponse.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("setup authentication did not return a session cookie")
	}
	sessionCookie := cookies[0]
	authenticated := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if _, err := request.Cookie(sessionCookie.Name); err != nil {
			request.AddCookie(sessionCookie)
		}
		handler.ServeHTTP(responseWriter, request)
	})
	return authenticated, reloader
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
	if payload["service"] != "faro-api" || payload["version"] != faroversion.Display || payload["ok"] != true {
		t.Fatalf("unexpected health payload: %#v", payload)
	}
}

func TestVersionIsPublic(t *testing.T) {
	handler, _ := newTestServer(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("version status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Display string `json:"display"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	if payload.Name != "Faro" || payload.Version != faroversion.Number || payload.Display != faroversion.Display {
		t.Fatalf("unexpected version payload: %#v", payload)
	}
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw version response: %v", err)
	}
	if _, ok := raw["commit"]; ok {
		t.Fatal("version response should expose only the application version")
	}
}

func TestUpgradeStatusIsPublic(t *testing.T) {
	handler, _ := newTestServer(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/upgrade", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("upgrade status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		ApplicationVersion string          `json:"application_version"`
		SchemaVersion      int             `json:"schema_version"`
		State              db.UpgradeState `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode upgrade response: %v", err)
	}
	if payload.ApplicationVersion != faroversion.Number || payload.SchemaVersion != db.CurrentSchemaVersion || payload.State.Status != "complete" {
		t.Fatalf("unexpected upgrade payload: %#v", payload)
	}
}

func TestUpstreamCatalogPublishesEncryptedEndpoints(t *testing.T) {
	handler, _ := newTestServer(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/upstreams/catalog", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("catalog endpoint = %d, body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"url":"https://cloudflare-dns.com/dns-query"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"bootstrap_ips":["1.1.1.1","1.0.0.1"]`)) {
		t.Fatalf("catalog response missing encrypted endpoint details: %s", response.Body.String())
	}
}

func TestCreateBlocklistRejectsDuplicateSource(t *testing.T) {
	handler, _ := newTestServer(t)
	create := func(name, source string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/blocklists", bytes.NewBufferString(fmt.Sprintf(`{"name":%q,"url":%q,"assign_to_default":false}`, name, source)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	first := create("Privacy list", "https://example.test/privacy.txt")
	if first.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, body = %s", first.Code, first.Body.String())
	}
	duplicate := create("Another name", "https://example.test/privacy.txt")
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, want %d, body = %s", duplicate.Code, http.StatusConflict, duplicate.Body.String())
	}
	if !bytes.Contains(duplicate.Body.Bytes(), []byte("already installed")) {
		t.Fatalf("duplicate response did not explain the conflict: %s", duplicate.Body.String())
	}
}

func TestEncryptedBackupExportAndRestore(t *testing.T) {
	handler, reloader := newTestServer(t)
	passphrase := "correct horse backup staple"
	exportRequest := httptest.NewRequest(http.MethodPost, "/api/backups", bytes.NewBufferString(`{"passphrase":"`+passphrase+`"}`))
	exportRequest.Header.Set("Content-Type", "application/json")
	exportResponse := httptest.NewRecorder()
	handler.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", exportResponse.Code, exportResponse.Body.String())
	}
	if contentType := exportResponse.Header().Get("Content-Type"); contentType != "application/octet-stream" {
		t.Fatalf("export content type = %q", contentType)
	}
	if !bytes.HasPrefix(exportResponse.Body.Bytes(), []byte("FAROBKP1")) {
		t.Fatal("export did not return a Faro encrypted backup")
	}

	var restoreBody bytes.Buffer
	writer := multipart.NewWriter(&restoreBody)
	if err := writer.WriteField("passphrase", passphrase); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("backup", "backup.faro-backup")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(exportResponse.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	restoreRequest := httptest.NewRequest(http.MethodPost, "/api/backups/restore", &restoreBody)
	restoreRequest.Header.Set("Content-Type", writer.FormDataContentType())
	restoreResponse := httptest.NewRecorder()
	handler.ServeHTTP(restoreResponse, restoreRequest)
	if restoreResponse.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body = %s", restoreResponse.Code, restoreResponse.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(restoreResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != true || payload["requires_login"] != true || payload["dns_reloaded"] != true {
		t.Fatalf("unexpected restore payload: %#v", payload)
	}
	if reloader.calls != 1 {
		t.Fatalf("DNS reload calls = %d, want 1", reloader.calls)
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

func TestProtectionLifecycle(t *testing.T) {
	handler, reloader := newTestServer(t)
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/protections", nil))
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"name":"Home"`)) {
		t.Fatalf("default protection response: status=%d body=%s", list.Code, list.Body.String())
	}

	create := httptest.NewRequest(http.MethodPost, "/api/protections", bytes.NewBufferString(`{"name":"Children","icon":"baby","blocklist_ids":[],"allow_domains":["school.example"],"block_domains":["games.example"],"device_ips":["192.168.7.23"]}`))
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create protection: status=%d body=%s", created.Code, created.Body.String())
	}
	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || result.ID == 0 {
		t.Fatalf("invalid create response: %s", created.Body.String())
	}

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/protections", nil))
	if !bytes.Contains(read.Body.Bytes(), []byte(`"name":"Children"`)) || !bytes.Contains(read.Body.Bytes(), []byte(`"device_ips":["192.168.7.23"]`)) {
		t.Fatalf("created protection missing: %s", read.Body.String())
	}
	if reloader.calls != 1 {
		t.Fatalf("reload calls=%d want 1", reloader.calls)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/protections/%d", result.ID), nil)
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete protection: status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if reloader.calls != 2 {
		t.Fatalf("reload calls=%d want 2", reloader.calls)
	}
}

func TestNotificationReadDismissAndReadAllLifecycle(t *testing.T) {
	handler, _ := newTestServer(t)
	update := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"upstream_dns":"8.8.8.8"}`))
	update.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update settings: status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}

	readNotifications := func() struct {
		Attention int `json:"attention_count"`
		Unread    int `json:"unread_count"`
		Items     []struct {
			ID     string `json:"id"`
			IsRead bool   `json:"is_read"`
		} `json:"items"`
	} {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("get notifications: status = %d, body = %s", response.Code, response.Body.String())
		}
		var payload struct {
			Attention int `json:"attention_count"`
			Unread    int `json:"unread_count"`
			Items     []struct {
				ID     string `json:"id"`
				IsRead bool   `json:"is_read"`
			} `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}

	payload := readNotifications()
	if payload.Attention != 0 || payload.Unread != 1 || len(payload.Items) != 1 || payload.Items[0].IsRead {
		t.Fatalf("unexpected initial notifications: %#v", payload)
	}
	notificationID := payload.Items[0].ID
	markRead := httptest.NewRecorder()
	handler.ServeHTTP(markRead, httptest.NewRequest(http.MethodPut, "/api/notifications/"+notificationID+"/read", nil))
	if markRead.Code != http.StatusOK {
		t.Fatalf("mark read: status = %d, body = %s", markRead.Code, markRead.Body.String())
	}
	payload = readNotifications()
	if payload.Unread != 0 || len(payload.Items) != 1 || !payload.Items[0].IsRead {
		t.Fatalf("notification was not marked read: %#v", payload)
	}

	dismiss := httptest.NewRecorder()
	handler.ServeHTTP(dismiss, httptest.NewRequest(http.MethodDelete, "/api/notifications/"+notificationID, nil))
	if dismiss.Code != http.StatusOK {
		t.Fatalf("dismiss: status = %d, body = %s", dismiss.Code, dismiss.Body.String())
	}
	if payload = readNotifications(); len(payload.Items) != 0 || payload.Unread != 0 {
		t.Fatalf("notification was not dismissed: %#v", payload)
	}

	secondUpdate := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"upstream_dns":"1.1.1.1"}`))
	secondUpdate.Header.Set("Content-Type", "application/json")
	secondUpdateResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondUpdateResponse, secondUpdate)
	if secondUpdateResponse.Code != http.StatusOK {
		t.Fatalf("second settings update: status = %d, body = %s", secondUpdateResponse.Code, secondUpdateResponse.Body.String())
	}
	markAll := httptest.NewRecorder()
	handler.ServeHTTP(markAll, httptest.NewRequest(http.MethodPost, "/api/notifications/read-all", nil))
	if markAll.Code != http.StatusOK {
		t.Fatalf("mark all read: status = %d, body = %s", markAll.Code, markAll.Body.String())
	}
	if payload = readNotifications(); payload.Unread != 0 || len(payload.Items) != 1 || !payload.Items[0].IsRead {
		t.Fatalf("mark all did not update notifications: %#v", payload)
	}
}

func TestMaintenanceStatusAndValidation(t *testing.T) {
	handler, _ := newTestServer(t)
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/api/maintenance", nil))
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d, body = %s", statusResponse.Code, statusResponse.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["status"] != "healthy" || status["storage"] == nil {
		t.Fatalf("unexpected maintenance status: %#v", status)
	}

	pruneResponse := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/maintenance", bytes.NewBufferString(`{"retention_days":0,"compact":false}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(pruneResponse, request)
	if pruneResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid prune status = %d, want %d", pruneResponse.Code, http.StatusBadRequest)
	}
}

func TestRetentionSettingDoesNotReloadDNS(t *testing.T) {
	handler, reloader := newTestServer(t)
	request := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"retention_days":"45"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if reloader.calls != 0 {
		t.Fatalf("retention-only update reloaded DNS %d times", reloader.calls)
	}
}

func TestOnboardingCompletionDoesNotReloadDNS(t *testing.T) {
	handler, reloader := newTestServer(t)
	request := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"onboarding_completed":"true"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if reloader.calls != 0 {
		t.Fatalf("onboarding-only update reloaded DNS %d times", reloader.calls)
	}
}

func TestFaroLANIPSettingAcceptsUsableAddressWithoutReload(t *testing.T) {
	handler, reloader := newTestServer(t)
	request := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"faro_lan_ip":"192.168.7.20"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if reloader.calls != 0 {
		t.Fatalf("LAN address update reloaded DNS %d times", reloader.calls)
	}
}

func TestFaroLANIPSettingRejectsUnusableAddresses(t *testing.T) {
	for _, address := range []string{"localhost", "127.0.0.1", "0.0.0.0", "224.0.0.1"} {
		t.Run(address, func(t *testing.T) {
			handler, _ := newTestServer(t)
			request := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"faro_lan_ip":"`+address+`"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("settings status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
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
	authenticated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(sessionCookie.Name); err != nil {
			r.AddCookie(sessionCookie)
		}
		handler.ServeHTTP(w, r)
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
	if payload["service"] != "faro-api" || payload["ok"] != true {
		t.Fatalf("unexpected health payload: %#v", payload)
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

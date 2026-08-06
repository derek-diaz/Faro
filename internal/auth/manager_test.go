package auth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewManager(store)
}

func TestSetupCreatesAuthenticatedSession(t *testing.T) {
	manager := newTestManager(t)
	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"correct-horse-battery-staple"}`))
	response := httptest.NewRecorder()
	manager.Setup(response, setup)
	if response.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected session cookie: %#v", cookies)
	}

	protected := manager.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	unauthorized := httptest.NewRecorder()
	protected.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	authorizedRequest.AddCookie(cookies[0])
	authorized := httptest.NewRecorder()
	protected.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d", authorized.Code)
	}
}

func TestSetupIsRejectedOnReplica(t *testing.T) {
	manager := newTestManager(t)
	if _, err := manager.store.DB.Exec(`UPDATE redundancy_state SET role = 'replica' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"correct-horse-battery-staple"}`))
	response := httptest.NewRecorder()
	manager.Setup(response, setup)
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte("managed by the primary")) {
		t.Fatalf("replica setup status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestUnconfiguredInstallationCanLeaveRedundancyWithoutSession(t *testing.T) {
	manager := newTestManager(t)
	protected := manager.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/redundancy", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("unconfigured redundancy exit status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestConfiguredInstallationRequiresSessionToLeaveRedundancy(t *testing.T) {
	manager := newTestManager(t)
	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"correct-horse-battery-staple"}`))
	setupResponse := httptest.NewRecorder()
	manager.Setup(setupResponse, setup)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", setupResponse.Code, setupResponse.Body.String())
	}
	protected := manager.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/redundancy", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("configured redundancy exit status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestHealthAndMetricsRemainPublic(t *testing.T) {
	manager := newTestManager(t)
	protected := manager.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, path := range []string{"/healthz", "/metrics", "/api/version", "/api/version/check"} {
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusNoContent)
		}
	}
}

func TestStatusTracksOnboardingCompletion(t *testing.T) {
	manager := newTestManager(t)
	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"correct-horse-battery-staple"}`))
	setupResponse := httptest.NewRecorder()
	manager.Setup(setupResponse, setup)
	cookie := setupResponse.Result().Cookies()[0]

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	statusRequest.AddCookie(cookie)
	statusResponse := httptest.NewRecorder()
	manager.Status(statusResponse, statusRequest)
	if !bytes.Contains(statusResponse.Body.Bytes(), []byte(`"onboarding_complete":false`)) {
		t.Fatalf("unexpected initial status: %s", statusResponse.Body.String())
	}
	if _, err := manager.store.DB.Exec(`INSERT INTO settings(key, value) VALUES('onboarding_completed', 'true')`); err != nil {
		t.Fatal(err)
	}
	statusResponse = httptest.NewRecorder()
	manager.Status(statusResponse, statusRequest)
	if !bytes.Contains(statusResponse.Body.Bytes(), []byte(`"onboarding_complete":true`)) {
		t.Fatalf("unexpected completed status: %s", statusResponse.Body.String())
	}
}

func TestSetupCanOnlyCreateOneAdministrator(t *testing.T) {
	manager := newTestManager(t)
	for index, username := range []string{"admin-one", "admin-two"} {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"`+username+`","password":"correct-horse-battery-staple"}`))
		response := httptest.NewRecorder()
		manager.Setup(response, request)
		want := http.StatusCreated
		if index == 1 {
			want = http.StatusConflict
		}
		if response.Code != want {
			t.Fatalf("setup %d status = %d, want %d", index, response.Code, want)
		}
	}
}

func TestPasswordMinimumIsEightCharacters(t *testing.T) {
	if err := validateCredentials(credentials{Username: "admin", Password: "1234567"}); err == nil {
		t.Fatal("expected seven-character password to be rejected")
	}
	if err := validateCredentials(credentials{Username: "admin", Password: "12345678"}); err != nil {
		t.Fatalf("expected eight-character password to be accepted: %v", err)
	}
}

func TestLoginThrottlesRepeatedFailures(t *testing.T) {
	manager := newTestManager(t)
	manager.now = func() time.Time { return time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC) }
	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"correct-horse-battery-staple"}`))
	manager.Setup(httptest.NewRecorder(), setup)

	for attempt := 0; attempt < maxFailures; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"wrong-password"}`))
		request.RemoteAddr = "192.0.2.10:1234"
		response := httptest.NewRecorder()
		manager.Login(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d", attempt, response.Code)
		}
	}
	blockedRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"wrong-password"}`))
	blockedRequest.RemoteAddr = "192.0.2.10:1234"
	blockedResponse := httptest.NewRecorder()
	manager.Login(blockedResponse, blockedRequest)
	if blockedResponse.Code != http.StatusTooManyRequests || blockedResponse.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked status = %d, retry-after = %q", blockedResponse.Code, blockedResponse.Header().Get("Retry-After"))
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	manager := newTestManager(t)
	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"correct-horse-battery-staple"}`))
	setupResponse := httptest.NewRecorder()
	manager.Setup(setupResponse, setup)
	cookie := setupResponse.Result().Cookies()[0]

	logout := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logout.AddCookie(cookie)
	logoutResponse := httptest.NewRecorder()
	manager.Logout(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout status = %d", logoutResponse.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	request.AddCookie(cookie)
	protected := manager.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out session status = %d", response.Code)
	}
}

func TestChangePasswordVerifiesCurrentPasswordAndClosesOtherSessions(t *testing.T) {
	manager := newTestManager(t)
	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"old-password"}`))
	setupResponse := httptest.NewRecorder()
	manager.Setup(setupResponse, setup)
	currentSession := setupResponse.Result().Cookies()[0]

	secondLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"old-password"}`))
	secondResponse := httptest.NewRecorder()
	manager.Login(secondResponse, secondLogin)
	otherSession := secondResponse.Result().Cookies()[0]

	wrong := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(`{"current_password":"wrong-password","new_password":"new-password"}`))
	wrong.AddCookie(currentSession)
	wrongResponse := httptest.NewRecorder()
	manager.ChangePassword(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusForbidden {
		t.Fatalf("wrong current password status = %d, body = %s", wrongResponse.Code, wrongResponse.Body.String())
	}

	change := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(`{"current_password":"old-password","new_password":"new-password"}`))
	change.AddCookie(currentSession)
	changeResponse := httptest.NewRecorder()
	manager.ChangePassword(changeResponse, change)
	if changeResponse.Code != http.StatusOK {
		t.Fatalf("change password status = %d, body = %s", changeResponse.Code, changeResponse.Body.String())
	}

	protected := manager.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	currentRequest := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	currentRequest.AddCookie(currentSession)
	currentResponse := httptest.NewRecorder()
	protected.ServeHTTP(currentResponse, currentRequest)
	if currentResponse.Code != http.StatusNoContent {
		t.Fatalf("current session status = %d", currentResponse.Code)
	}
	otherRequest := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	otherRequest.AddCookie(otherSession)
	otherResponse := httptest.NewRecorder()
	protected.ServeHTTP(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusUnauthorized {
		t.Fatalf("other session status = %d, want %d", otherResponse.Code, http.StatusUnauthorized)
	}

	oldLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"old-password"}`))
	oldResponse := httptest.NewRecorder()
	manager.Login(oldResponse, oldLogin)
	if oldResponse.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d", oldResponse.Code)
	}
	newLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"new-password"}`))
	newResponse := httptest.NewRecorder()
	manager.Login(newResponse, newLogin)
	if newResponse.Code != http.StatusOK {
		t.Fatalf("new password login status = %d, body = %s", newResponse.Code, newResponse.Body.String())
	}
}

func TestChangePasswordRequiresAuthenticationAndEightCharacters(t *testing.T) {
	manager := newTestManager(t)
	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(`{"current_password":"old-password","new_password":"new-password"}`))
	unauthenticatedResponse := httptest.NewRecorder()
	manager.ChangePassword(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticatedResponse.Code)
	}

	setup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"old-password"}`))
	setupResponse := httptest.NewRecorder()
	manager.Setup(setupResponse, setup)
	request := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(`{"current_password":"old-password","new_password":"short"}`))
	request.AddCookie(setupResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	manager.ChangePassword(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %d, body = %s", response.Code, response.Body.String())
	}
}

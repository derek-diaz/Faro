package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
)

func troubleshootingFixture(t *testing.T) (*Handler, *testReloader, troubleshootingInput) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	id, err := deviceidentity.ResolveAddress(context.Background(), store, "192.0.2.10", "dns")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.DB.Exec(`INSERT INTO protection_profiles(name,icon) VALUES('Office','shield')`)
	if err != nil {
		t.Fatal(err)
	}
	protectionID, _ := result.LastInsertId()
	if _, err := store.DB.Exec(`INSERT INTO device_protection_memberships(device_id,protection_id) VALUES(?,?)`, id, protectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO protection_block_entries(protection_id,domain) VALUES(?,'broken.example')`, protectionID); err != nil {
		t.Fatal(err)
	}
	reloader := &testReloader{}
	return &Handler{store: store, reloader: reloader, deviceNames: newDeviceNameResolver()}, reloader, troubleshootingInput{Action: "test", ClientIP: "192.0.2.10", DeviceID: id, ProtectionID: protectionID, Domains: []string{"broken.example"}}
}

func trialRequest(t *testing.T, h *Handler, input troubleshootingInput, want int) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/troubleshooting", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.troubleshooting(response, request)
	if response.Code != want {
		t.Fatalf("%s status=%d body=%s", input.Action, response.Code, response.Body.String())
	}
	return response
}

func startTrial(t *testing.T, h *Handler, input troubleshootingInput) string {
	t.Helper()
	response := trialRequest(t, h, input, http.StatusCreated)
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Token
}

func TestTroubleshootingLifecyclePreservesPermanentRulesAndOtherTests(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	if _, err := h.store.DB.Exec(`INSERT INTO protection_allow_entries(protection_id,domain) VALUES(?,'existing.example')`, input.ProtectionID); err != nil {
		t.Fatal(err)
	}
	input.Domains = append(input.Domains, "existing.example")
	first := startTrial(t, h, input)
	second := startTrial(t, h, input)
	decision := coredns.ExplainDomainForClient(context.Background(), h.store, "broken.example", input.ClientIP)
	if decision.Action != "allowed" || decision.Allowlist == nil {
		t.Fatalf("temporary exception not effective: %#v", decision)
	}
	trialRequest(t, h, troubleshootingInput{Action: "undo", Token: first}, http.StatusOK)
	if got := scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM troubleshooting_exceptions WHERE token = ?`, second); got != 2 {
		t.Fatalf("other test lost: %d", got)
	}
	if got := scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM protection_allow_entries WHERE protection_id = ?`, input.ProtectionID); got != 1 {
		t.Fatalf("permanent rules changed on undo: %d", got)
	}
	trialRequest(t, h, troubleshootingInput{Action: "keep", Token: second}, http.StatusOK)
	if got := scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM protection_block_entries WHERE protection_id = ?`, input.ProtectionID); got != 0 {
		t.Fatal("keep left contradictory custom block")
	}
	if got := scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM protection_allow_entries WHERE protection_id = ?`, input.ProtectionID); got != 2 {
		t.Fatalf("keep failed: %d", got)
	}
	if got := scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM protection_allow_entries a JOIN protection_profiles p ON p.id=a.protection_id WHERE p.is_default=1`); got != 0 {
		t.Fatalf("Home was changed: %d", got)
	}
	trialRequest(t, h, troubleshootingInput{Action: "undo", Token: second}, http.StatusNotFound)
}

func TestTroubleshootingReloadFailureRollsBackEachAction(t *testing.T) {
	for _, action := range []string{"test", "keep", "undo"} {
		t.Run(action, func(t *testing.T) {
			h, reloader, input := troubleshootingFixture(t)
			if _, err := h.store.DB.Exec(`INSERT INTO protection_allow_entries(protection_id,domain) VALUES(?,'existing.example')`, input.ProtectionID); err != nil {
				t.Fatal(err)
			}
			input.Domains = append(input.Domains, "existing.example")
			expectedTrials := 0
			if action != "test" {
				input.Token = startTrial(t, h, input)
				expectedTrials = 2
			}
			input.Action = action
			reloader.err = errors.New("reload rejected")
			trialRequest(t, h, input, http.StatusInternalServerError)
			if got := scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM protection_block_entries WHERE protection_id = ?`, input.ProtectionID); got != 1 {
				t.Fatal("failed change lost original custom block")
			}
			if got := scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM troubleshooting_exceptions`); got != expectedTrials {
				t.Fatalf("rollback left %d trials, want %d", got, expectedTrials)
			}
			if got := scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM protection_allow_entries WHERE protection_id = ?`, input.ProtectionID); got != 1 {
				t.Fatalf("rollback changed permanent rules: %d", got)
			}
		})
	}
}

func TestTroubleshootingRejectsStaleScopeAndRedundancy(t *testing.T) {
	h, reloader, input := troubleshootingFixture(t)
	stale := input
	stale.DeviceID++
	trialRequest(t, h, stale, http.StatusConflict)
	stale = input
	stale.ProtectionID++
	trialRequest(t, h, stale, http.StatusConflict)
	invalid := input
	invalid.Domains = []string{"https://broken.example/path"}
	trialRequest(t, h, invalid, http.StatusBadRequest)
	for _, role := range []string{"controller", "replica"} {
		if _, err := h.store.DB.Exec(`UPDATE redundancy_state SET role = ? WHERE id=1`, role); err != nil {
			t.Fatal(err)
		}
		trialRequest(t, h, input, http.StatusConflict)
	}
	if reloader.calls != 0 {
		t.Fatalf("invalid input reloaded DNS %d times", reloader.calls)
	}
}

func TestTroubleshootingCaptureUsesStableIdentityAndRetainsTests(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	token := startTrial(t, h, input)
	if _, err := h.store.DB.Exec(`INSERT INTO device_addresses(device_id,address,family,source,confidence) VALUES(?,'2001:db8::10','ipv6','dns','observed')`, input.DeviceID); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{input.ClientIP, "2001:db8::10"} {
		if _, err := h.store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source,rcode) VALUES(?,?,?,'broken.example','A','blocked','manual','NOERROR')`, time.Now().UTC().Format(time.RFC3339Nano), ip, input.DeviceID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(?,?,?,'old.example','A','blocked','manual')`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), input.ClientIP, input.DeviceID); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.troubleshooting(response, httptest.NewRequest(http.MethodGet, "/api/troubleshooting?client_ip=2001:db8::10", nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var report struct {
		Items []struct {
			Domain   string
			Requests int
			Blocked  int
		}
		Trials []trialEntry
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 1 || report.Items[0].Requests != 2 || report.Items[0].Blocked != 2 {
		t.Fatalf("capture not isolated: %s", response.Body.String())
	}
	if len(report.Trials) != 1 || report.Trials[0].Token != token || report.Trials[0].ProtectionName != "Office" {
		t.Fatalf("test did not follow device: %s", response.Body.String())
	}
}

func TestTroubleshootingRequiresAuthentication(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	server := New(h.store, h.reloader, nil)
	response := httptest.NewRecorder()
	body, _ := json.Marshal(input)
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/troubleshooting", bytes.NewReader(body)))
	if response.Code == http.StatusCreated || scalarInt(context.Background(), h.store.DB, `SELECT COUNT(*) FROM troubleshooting_exceptions`) != 0 {
		t.Fatal("unauthenticated trial was created")
	}
}

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	deviceidentity "github.com/derek/faro/internal/devices"
)

func TestDeviceReferenceSurvivesDHCPReuse(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	ctx := context.Background()
	ids := []deviceidentity.Identifier{{Kind: "mac", Value: "02:11:22:33:44:55"}}
	if _, err := deviceidentity.ObserveAddress(ctx, h.store, input.ClientIP, "unifi", ids); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DB.Exec(`UPDATE devices SET name='Original' WHERE id=?;
		INSERT INTO dns_queries(timestamp,client_ip,device_id,domain,query_type,action,source) VALUES(?,?,?,'history.example','A','allowed','upstream')`, input.DeviceID, time.Now().UTC().Format(time.RFC3339), input.ClientIP, input.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceidentity.ObserveAddress(ctx, h.store, "192.0.2.11", "unifi", ids); err != nil {
		t.Fatal(err)
	}
	otherID, err := deviceidentity.ObserveAddress(ctx, h.store, input.ClientIP, "unifi", []deviceidentity.Identifier{{Kind: "mac", Value: "02:aa:bb:cc:dd:ee"}})
	if err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprintf("/api/devices/id:%d", input.DeviceID)
	call := func(method, path, body string) map[string]any {
		t.Helper()
		response := httptest.NewRecorder()
		h.device(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	detail := call(http.MethodGet, ref, "")
	if detail["display_name"] != "Original" || detail["client_ip"] != "192.0.2.11" || detail["protection_id"] != float64(input.ProtectionID) {
		t.Fatalf("wrong device after DHCP: %#v", detail)
	}
	history := detail["address_history"].([]any)
	if len(history) != 2 {
		t.Fatalf("missing previous address: %#v", history)
	}
	previousAddress := history[1].(map[string]any)
	if previousAddress["address"] != input.ClientIP || previousAddress["current"] != false {
		t.Fatalf("retired address shown as current: %#v", history)
	}
	if replay := call(http.MethodGet, ref+"/replay?range=24h", ""); replay["total_queries"] != float64(1) {
		t.Fatalf("lost history: %#v", replay)
	}
	call(http.MethodPut, ref+"/alias", `{"name":"Renamed","device_type":"Phone"}`)
	call(http.MethodPut, ref+"/pause", fmt.Sprintf(`{"until":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)))
	call(http.MethodPut, ref+"/protection", `{"protection_id":1}`)
	var otherName string
	if err := h.store.DB.QueryRow(`SELECT name FROM devices WHERE id=?`, otherID).Scan(&otherName); err != nil {
		t.Fatal(err)
	}
	if otherName != "" {
		t.Fatal("edit changed new IP owner")
	}
	var pausedID int64
	if err := h.store.DB.QueryRow(`SELECT device_id FROM device_dns_pauses`).Scan(&pausedID); err != nil {
		t.Fatal(err)
	}
	if pausedID != input.DeviceID {
		t.Fatal("pause affected new IP owner")
	}
	if got := call(http.MethodGet, ref, ""); got["display_name"] != "Renamed" || got["protection_id"] != float64(1) {
		t.Fatalf("edits lost: %#v", got)
	}
}

func TestAddresslessDeviceKeepsUniFiNameAndProtection(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	if _, err := h.store.DB.Exec(`INSERT INTO device_names(device_id,source,name) VALUES(?,'unifi','Disconnected speaker');
		DELETE FROM device_addresses WHERE device_id=?`, input.DeviceID, input.DeviceID); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.device(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/devices/id:%d", input.DeviceID), nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["display_name"] != "Disconnected speaker" || detail["client_ip"] != "" || detail["protection_id"] != float64(input.ProtectionID) {
		t.Fatalf("lost addressless identity: %#v", detail)
	}
	response = httptest.NewRecorder()
	h.devices(response, httptest.NewRequest(http.MethodGet, "/api/devices", nil))
	var inventory []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 1 || inventory[0]["display_name"] != "Disconnected speaker" {
		t.Fatalf("inventory lost device name: %#v", inventory)
	}
}

func TestDeviceProtectionIgnoresReassignedAddress(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	if _, err := h.store.DB.Exec(`DELETE FROM device_protection_memberships WHERE device_id=?;
		DELETE FROM device_addresses WHERE device_id=?;
		INSERT INTO devices(name) VALUES('New owner');
		INSERT INTO device_addresses(device_id,address,family) VALUES(last_insert_rowid(),?,'ipv4');
		INSERT INTO device_protection_assignments(client_ip,protection_id) VALUES(?,?)`, input.DeviceID, input.DeviceID, input.ClientIP, input.ClientIP, input.ProtectionID); err != nil {
		t.Fatal(err)
	}
	protectionID, name, _ := protectionForDevice(context.Background(), h.store.DB, input.DeviceID, input.ClientIP)
	if protectionID != 1 || name != "Home" {
		t.Fatalf("old device inherited reused address's policy: %d %s", protectionID, name)
	}
}

func TestProtectionEditorKeepsDeviceAfterAddressReuse(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	ctx := context.Background()
	if _, err := deviceidentity.ObserveAddress(ctx, h.store, input.ClientIP, "unifi", []deviceidentity.Identifier{{Kind: "mac", Value: "02:11:22:33:44:55"}}); err != nil {
		t.Fatal(err)
	}
	otherID, err := deviceidentity.ObserveAddress(ctx, h.store, input.ClientIP, "unifi", []deviceidentity.Identifier{{Kind: "mac", Value: "02:aa:bb:cc:dd:ee"}})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":"Office","icon":"shield","device_ids":[%d],"device_ips":[%q],"block_domains":["broken.example"]}`, input.DeviceID, input.ClientIP)
	response := httptest.NewRecorder()
	h.protection(response, httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/protections/%d", input.ProtectionID), strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("profile update: %d %s", response.Code, response.Body.String())
	}
	var deviceID int64
	if err := h.store.DB.QueryRow(`SELECT device_id FROM device_protection_memberships WHERE protection_id=?`, input.ProtectionID).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	if deviceID != input.DeviceID || deviceID == otherID {
		t.Fatal("profile edit assigned protection to the new IP owner")
	}
	response = httptest.NewRecorder()
	h.protections(response, httptest.NewRequest(http.MethodGet, "/api/protections", nil))
	var profiles []struct {
		ID        int64
		DeviceIDs []int64 `json:"device_ids"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &profiles); err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if profile.ID == input.ProtectionID && (len(profile.DeviceIDs) != 1 || profile.DeviceIDs[0] != input.DeviceID) {
			t.Fatalf("profile response lost offline member: %#v", profile)
		}
	}
}

func TestProtectionRollbackDoesNotRestoreStaleIPOverride(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	ctx := context.Background()
	if _, err := deviceidentity.ObserveAddress(ctx, h.store, input.ClientIP, "unifi", []deviceidentity.Identifier{{Kind: "mac", Value: "02:11:22:33:44:55"}}); err != nil {
		t.Fatal(err)
	}
	snapshot := readDeviceProtectionAssignments(ctx, h.store.DB)
	if _, err := deviceidentity.ObserveAddress(ctx, h.store, input.ClientIP, "unifi", []deviceidentity.Identifier{{Kind: "mac", Value: "02:aa:bb:cc:dd:ee"}}); err != nil {
		t.Fatal(err)
	}
	restoreDeviceProtectionAssignments(ctx, h.store.DB, snapshot)
	protectionID, _, _ := protectionForClient(ctx, h.store.DB, input.ClientIP)
	if protectionID != 1 {
		t.Fatal("rollback applied the old device's protection to the new IP owner")
	}
}

func TestUnnamedUniFiDeviceDoesNotUsePreviousIPsName(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	ctx := context.Background()
	if _, err := h.store.DB.Exec(`INSERT INTO integration_configs(kind,enabled) VALUES('unifi',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceidentity.ObserveAddress(ctx, h.store, input.ClientIP, "unifi", []deviceidentity.Identifier{{Kind: "mac", Value: "02:11:22:33:44:55"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DB.Exec(`INSERT INTO dns_records(hostname,type,value) VALUES('previous.home','A',?)`, input.ClientIP); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceidentity.ObserveAddress(ctx, h.store, input.ClientIP, "unifi", []deviceidentity.Identifier{{Kind: "mac", Value: "02:aa:bb:cc:dd:ee"}}); err != nil {
		t.Fatal(err)
	}
	h.deviceNames.store(input.ClientIP, "Previous router name")
	response := httptest.NewRecorder()
	h.device(response, httptest.NewRequest(http.MethodGet, "/api/devices/"+input.ClientIP, nil))
	var detail map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["display_name"] != input.ClientIP {
		t.Fatalf("new owner inherited previous name: %v", detail["display_name"])
	}
	if _, err := h.store.DB.Exec(`DELETE FROM integration_configs WHERE kind='unifi'`); err != nil {
		t.Fatal(err)
	}
	if got := h.discoverDeviceNames(ctx, []string{input.ClientIP})[input.ClientIP]; got.NameSource != "local_dns" {
		t.Fatalf("disconnected controller suppressed local naming: %#v", got)
	}
}

func TestProtectionRejectsInvalidDeviceIDs(t *testing.T) {
	h, _, input := troubleshootingFixture(t)
	for _, id := range []int64{0, -1, 999999} {
		body := fmt.Sprintf(`{"name":"Office","icon":"shield","device_ids":[%d]}`, id)
		response := httptest.NewRecorder()
		h.protection(response, httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/protections/%d", input.ProtectionID), strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid id %d: status %d", id, response.Code)
		}
	}
	var count int
	if err := h.store.DB.QueryRow(`SELECT COUNT(*) FROM device_protection_memberships WHERE device_id=? AND protection_id=?`, input.DeviceID, input.ProtectionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("invalid IDs cleared membership")
	}
}

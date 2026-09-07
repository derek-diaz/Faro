package auth

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFailureTrackerIsBoundedAndExpiresWithoutRevisitingKeys(t *testing.T) {
	now := time.Now()
	manager := &Manager{now: func() time.Time { return now }, failures: make(map[string]failureState)}
	for i := 0; i < maxFailures; i++ {
		manager.recordFailure("locked")
	}
	for i := 0; i < maxFailureEntries*2; i++ {
		manager.recordFailure(fmt.Sprintf("attempt-%d", i))
	}
	if len(manager.failures) != maxFailureEntries {
		t.Fatalf("unbounded failures: %d", len(manager.failures))
	}
	if _, blocked := manager.blocked("locked"); !blocked {
		t.Fatal("key churn evicted an active lockout")
	}
	if _, blocked := manager.blocked("new-key"); !blocked {
		t.Fatal("full tracker admitted more unknown keys")
	}
	now = now.Add(lockoutDuration + time.Second)
	if _, blocked := manager.blocked("new-key"); blocked {
		t.Fatal("expired failures prevented recovery")
	}
	if len(manager.failures) != 0 {
		t.Fatalf("dormant failures retained: %d", len(manager.failures))
	}
	manager.recordFailure("new-key")
	if manager.failures["new-key"].Count != 1 {
		t.Fatal("failure count did not restart after expiry")
	}
}

func TestFailureKeyStorageDoesNotGrowWithUsernameLength(t *testing.T) {
	manager := &Manager{}
	request := httptest.NewRequest("POST", "/api/auth/login", nil)
	short := manager.failureKey(request, "Admin")
	if short != manager.failureKey(request, " admin ") {
		t.Fatal("normalized names must share a lockout")
	}
	if long := manager.failureKey(request, strings.Repeat("a", 1<<20)); len(long) != 64 || len(short) != 64 {
		t.Fatal("failure keys are not fixed-size")
	}
}

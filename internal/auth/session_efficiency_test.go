package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionReadsCoalesceWritesAndRespectRevocation(t *testing.T) {
	manager := newTestManager(t)
	now := time.Now().UTC().Truncate(time.Second)
	manager.now = func() time.Time { return now }
	if _, err := manager.store.DB.Exec(`INSERT INTO users(id,username,password_hash) VALUES(1,'admin','test');
 CREATE TABLE session_writes(n INTEGER); INSERT INTO session_writes VALUES(0);
 CREATE TRIGGER count_session_writes AFTER UPDATE ON auth_sessions BEGIN UPDATE session_writes SET n=n+1; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.store.DB.Exec(`INSERT INTO auth_sessions(user_id,token_hash,expires_at,last_seen_at) VALUES(1,?,?,?)`, tokenHash("test-token"), now.Add(time.Hour).Format(time.RFC3339), now.Format("2006-01-02 15:04:05")); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: cookieName, Value: "test-token"})
	for i := 0; i < 10; i++ {
		if _, ok := manager.authenticate(request); !ok {
			t.Fatal("valid session rejected")
		}
	}
	var writes int
	manager.store.DB.QueryRow(`SELECT n FROM session_writes`).Scan(&writes)
	if writes != 0 {
		t.Fatalf("fresh session caused %d writes", writes)
	}
	now = now.Add(time.Minute)
	for i := 0; i < 10; i++ {
		if _, ok := manager.authenticate(request); !ok {
			t.Fatal("valid session rejected")
		}
	}
	manager.store.DB.QueryRow(`SELECT n FROM session_writes`).Scan(&writes)
	if writes != 1 {
		t.Fatalf("expected one touch per minute, got %d", writes)
	}
	if _, err := manager.store.DB.Exec(`DELETE FROM auth_sessions`); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.authenticate(request); ok {
		t.Fatal("revoked session accepted")
	}
}

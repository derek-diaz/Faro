package db

import (
	"path/filepath"
	"testing"
)

func TestQueryLogProgressUpgradePreservesExistingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "faro.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO dns_queries(timestamp,client_ip,domain,query_type,action,source) VALUES('2026-09-07T00:00:00Z','192.0.2.1','existing.example','A','allowed','upstream');
 DROP TABLE query_log_progress;
 DELETE FROM faro_schema_migrations WHERE version=14;
 PRAGMA user_version=13;
 UPDATE settings SET value='13' WHERE key='database_schema_version'`); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var count int
	if err := upgraded.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries WHERE domain='existing.example'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("history lost: %d %v", count, err)
	}
	if _, err := upgraded.DB.Exec(`INSERT INTO query_log_progress(path,identity,offset) VALUES('query.log','file-id',123)`); err != nil {
		t.Fatal(err)
	}
	state, err := ReadUpgradeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.FromVersion != 13 || state.ToVersion != CurrentSchemaVersion || state.BackupPath == "" || state.Status != "complete" {
		t.Fatalf("upgrade did not back up and finish: %+v", state)
	}
}

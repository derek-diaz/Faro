package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	faroversion "github.com/derek/faro/internal/version"
)

func TestMigrateSupportedPriorReleases(t *testing.T) {
	for _, release := range []string{"v0.1.0", "v0.1.1", "v0.2.0"} {
		release := release
		t.Run(release, func(t *testing.T) {
			path := createLegacyReleaseDatabase(t, release)
			store, err := Open(path)
			if err != nil {
				t.Fatalf("upgrade %s: %v", release, err)
			}
			defer func() { _ = store.Close() }()

			var sqliteVersion int
			if err := store.DB.QueryRow(`PRAGMA user_version`).Scan(&sqliteVersion); err != nil {
				t.Fatal(err)
			}
			if sqliteVersion != CurrentSchemaVersion {
				t.Fatalf("SQLite user_version = %d, want %d", sqliteVersion, CurrentSchemaVersion)
			}

			var migrationCount int
			if err := store.DB.QueryRow(`SELECT COUNT(*) FROM faro_schema_migrations`).Scan(&migrationCount); err != nil {
				t.Fatal(err)
			}
			if migrationCount != CurrentSchemaVersion {
				t.Fatalf("migration ledger has %d rows, want %d", migrationCount, CurrentSchemaVersion)
			}

			if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value) VALUES('legacy.home', 'AAAA', '2001:db8::20')`); err != nil {
				t.Fatalf("dual-stack DNS record was not preserved: %v", err)
			}
			var recordCount int
			if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE hostname = 'legacy.home'`).Scan(&recordCount); err != nil {
				t.Fatal(err)
			}
			if recordCount != 2 {
				t.Fatalf("legacy hostname has %d records, want 2", recordCount)
			}

			var homeID int64
			if err := store.DB.QueryRow(`SELECT id FROM protection_profiles WHERE name = 'Home' AND is_default = 1`).Scan(&homeID); err != nil {
				t.Fatalf("Home protection was not created: %v", err)
			}
			var copiedRules int
			if err := store.DB.QueryRow(`SELECT COUNT(*) FROM protection_allow_entries WHERE protection_id = ? AND domain = 'allowed.example'`, homeID).Scan(&copiedRules); err != nil {
				t.Fatal(err)
			}
			if copiedRules != 1 {
				t.Fatalf("legacy allowlist was not copied into Home")
			}

			var deviceCount int
			if err := store.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE name = 'Living Room'`).Scan(&deviceCount); err != nil {
				t.Fatal(err)
			}
			if deviceCount != 1 {
				t.Fatalf("legacy device identity was not migrated")
			}

			state, err := ReadUpgradeState(path)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status != "complete" || state.FromVersion != 0 || state.ToVersion != CurrentSchemaVersion {
				t.Fatalf("unexpected upgrade state: %+v", state)
			}
			if state.BackupPath == "" {
				t.Fatal("successful upgrade did not retain its automatic backup path")
			}
			if _, err := os.Stat(state.BackupPath); err != nil {
				t.Fatalf("automatic migration backup is missing: %v", err)
			}
		})
	}
}

func TestMigrateUnversionedCurrentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "faro.db")
	database, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	legacyStore := &Store{DB: database, Path: path, UpgradeStatePath: upgradeStatePath(path)}
	if err := legacyStore.ensureSchema(context.Background()); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err := ReadUpgradeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "complete" || state.FromVersion != 0 {
		t.Fatalf("unexpected current-schema upgrade state: %+v", state)
	}
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM faro_schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != CurrentSchemaVersion {
		t.Fatalf("current unversioned schema ledger has %d rows, want %d", count, CurrentSchemaVersion)
	}
}

func TestMigrationFailureRestoresPreMigrationDatabase(t *testing.T) {
	path := createLegacyReleaseDatabase(t, "v0.2.0")
	database, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	store := &Store{
		DB:               database,
		Path:             path,
		UpgradeStatePath: upgradeStatePath(path),
		migrationHook: func(version int, _ string) error {
			if version == 6 {
				return errors.New("injected migration failure")
			}
			return nil
		},
	}
	err = store.migrate(context.Background())
	if err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) {
		t.Fatalf("error = %T %v, want MigrationError", err, err)
	}
	if migrationErr.Version != 6 || !migrationErr.Restored || migrationErr.BackupPath == "" {
		t.Fatalf("unexpected migration error: %+v", migrationErr)
	}
	if !strings.Contains(err.Error(), "previous database was restored") {
		t.Fatalf("migration error does not explain rollback: %v", err)
	}

	check, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var sqliteVersion int
	if err := check.QueryRow(`PRAGMA user_version`).Scan(&sqliteVersion); err != nil {
		t.Fatal(err)
	}
	if sqliteVersion != 0 {
		t.Fatalf("rolled-back SQLite user_version = %d, want 0", sqliteVersion)
	}
	var hostname string
	if err := check.QueryRow(`SELECT hostname FROM dns_records WHERE id = 1`).Scan(&hostname); err != nil {
		t.Fatal(err)
	}
	if hostname != "legacy.home" {
		t.Fatalf("rolled-back DNS record hostname = %q", hostname)
	}
	var migrationTableCount int
	if err := check.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'faro_schema_migrations'`).Scan(&migrationTableCount); err != nil {
		t.Fatal(err)
	}
	if migrationTableCount != 0 {
		t.Fatal("migration metadata remained active after rollback")
	}

	state, err := ReadUpgradeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "failed" || !strings.Contains(state.Error, "injected migration failure") {
		t.Fatalf("unexpected failed upgrade state: %+v", state)
	}
	if _, err := os.Stat(migrationErr.BackupPath); err != nil {
		t.Fatalf("rollback backup is missing: %v", err)
	}
}

func TestIncompatibleDatabaseVersionIsRejectedWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "faro.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE sentinel(value TEXT NOT NULL); INSERT INTO sentinel(value) VALUES('keep'); PRAGMA user_version = 99`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(path)
	if err == nil {
		t.Fatal("newer database schema was accepted")
	}
	var incompatible *IncompatibleVersionError
	if !errors.As(err, &incompatible) || incompatible.Found != 99 {
		t.Fatalf("error = %T %v, want incompatible version 99", err, err)
	}
	if !errors.Is(err, ErrIncompatibleVersion) {
		t.Fatalf("incompatible version error does not unwrap to ErrIncompatibleVersion: %v", err)
	}
	if !strings.Contains(err.Error(), "use a newer image or restore a compatible backup") {
		t.Fatalf("incompatible error is not actionable: %v", err)
	}

	check, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var value string
	if err := check.QueryRow(`SELECT value FROM sentinel`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "keep" {
		t.Fatalf("sentinel value changed to %q", value)
	}
	var state UpgradeState
	state, err = ReadUpgradeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "incompatible" {
		t.Fatalf("unexpected incompatible state: %+v", state)
	}
}

func TestInterruptedMigrationRestoresBackupBeforeRetry(t *testing.T) {
	path := createLegacyReleaseDatabase(t, "v0.2.0")
	backupPath := filepath.Join(t.TempDir(), "pre-upgrade.db")
	database, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshotDatabase(context.Background(), database, backupPath); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP TABLE dns_records`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeUpgradeState(upgradeStatePath(path), UpgradeState{
		Status:             "in_progress",
		ApplicationVersion: faroversion.Number,
		FromVersion:        0,
		ToVersion:          CurrentSchemaVersion,
		BackupPath:         backupPath,
	}); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var recordCount int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE hostname = 'legacy.home'`).Scan(&recordCount); err != nil {
		t.Fatal(err)
	}
	if recordCount != 1 {
		t.Fatalf("interrupted-upgrade recovery restored %d legacy records, want 1", recordCount)
	}
	state, err := ReadUpgradeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "complete" {
		t.Fatalf("unexpected post-recovery state: %+v", state)
	}
}

func createLegacyReleaseDatabase(t *testing.T, release string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("faro-%s.db", strings.TrimPrefix(release, "v")))
	database, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	statements := []string{
		`CREATE TABLE dns_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hostname TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL DEFAULT 'A',
			value TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE blocklists (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_refreshed_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE blocklist_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			blocklist_id INTEGER NOT NULL,
			domain TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(blocklist_id) REFERENCES blocklists(id) ON DELETE CASCADE,
			UNIQUE(blocklist_id, domain)
		)`,
		`CREATE TABLE allowlist_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE manual_block_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE dns_queries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			client_ip TEXT NOT NULL,
			domain TEXT NOT NULL,
			query_type TEXT NOT NULL,
			action TEXT NOT NULL,
			source TEXT NOT NULL,
			upstream TEXT NOT NULL DEFAULT '',
			latency_ms REAL,
			rcode TEXT NOT NULL DEFAULT '',
			decision_reason TEXT NOT NULL DEFAULT '',
			decision_metadata TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE device_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			client_ip TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			location TEXT,
			notes TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			type TEXT NOT NULL,
			severity TEXT NOT NULL DEFAULT 'info',
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			client_ip TEXT,
			domain TEXT,
			metadata TEXT NOT NULL DEFAULT '{}',
			source TEXT NOT NULL DEFAULT 'faro',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE domain_favicons (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL UNIQUE,
			favicon_url TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			last_checked_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			singleton INTEGER NOT NULL DEFAULT 1 UNIQUE CHECK(singleton = 1),
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE auth_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			database.Close()
			t.Fatalf("create %s fixture schema: %v", release, err)
		}
	}
	if _, err := database.Exec(`
		INSERT INTO dns_records(hostname, type, value, description) VALUES('legacy.home', 'A', '192.168.1.20', 'legacy record');
		INSERT INTO blocklists(name, url, enabled) VALUES('Legacy list', 'https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/pro.txt', 1);
		INSERT INTO blocklist_entries(blocklist_id, domain) VALUES(1, 'ads.example');
		INSERT INTO allowlist_entries(domain) VALUES('allowed.example');
		INSERT INTO manual_block_entries(domain) VALUES('blocked.example');
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, upstream) VALUES(CURRENT_TIMESTAMP, '192.168.1.20', 'legacy.example', 'A', 'allowed', 'upstream', '1.1.1.1');
		INSERT INTO device_aliases(client_ip, name, location, notes) VALUES('192.168.1.20', 'Living Room', 'Home', 'legacy identity');
	`); err != nil {
		database.Close()
		t.Fatalf("seed %s fixture: %v", release, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

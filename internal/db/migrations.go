package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	faroversion "github.com/derek/faro/internal/version"
	"github.com/mattn/go-sqlite3"
)

const (
	// CurrentSchemaVersion is the highest SQLite schema version understood by
	// this Faro release. It is deliberately independent of the application
	// version so a future release can make multiple schema changes safely.
	CurrentSchemaVersion = 12

	upgradeStateFilename = "faro-upgrade.json"
	migrationBackupDir   = "migrations"
)

var (
	ErrIncompatibleVersion = errors.New("Faro database schema is incompatible with this application")
)

// UpgradeState is written beside the database so it remains readable even
// when the database cannot be opened after an interrupted upgrade.
type UpgradeState struct {
	Status               string `json:"status"`
	ApplicationVersion   string `json:"application_version"`
	FromVersion          int    `json:"from_version"`
	ToVersion            int    `json:"to_version"`
	CurrentMigration     int    `json:"current_migration,omitempty"`
	CurrentMigrationName string `json:"current_migration_name,omitempty"`
	BackupPath           string `json:"backup_path,omitempty"`
	Error                string `json:"error,omitempty"`
	StartedAt            string `json:"started_at,omitempty"`
	FinishedAt           string `json:"finished_at,omitempty"`
}

// IncompatibleVersionError is returned before any schema mutation when a
// database was created by a newer Faro release.
type IncompatibleVersionError struct {
	Path      string
	Found     int
	Supported int
}

func (versionError *IncompatibleVersionError) Error() string {
	return fmt.Sprintf(
		"Faro cannot open %s: database schema version %d is newer than the supported version %d; use a newer image or restore a compatible backup",
		versionError.Path, versionError.Found, versionError.Supported,
	)
}

func (versionError *IncompatibleVersionError) Unwrap() error { return ErrIncompatibleVersion }

// MigrationError identifies the migration that failed and whether Faro was
// able to restore the pre-migration database image.
type MigrationError struct {
	Version    int
	Name       string
	BackupPath string
	Restored   bool
	Cause      error
}

func (migrationError *MigrationError) Error() string {
	if migrationError.Restored {
		return fmt.Sprintf(
			"Faro database migration %d (%s) failed; the previous database was restored from %s: %v",
			migrationError.Version, migrationError.Name, migrationError.BackupPath, migrationError.Cause,
		)
	}
	if migrationError.BackupPath != "" {
		return fmt.Sprintf(
			"Faro database migration %d (%s) failed and automatic restore did not complete; preserve %s and follow the recovery procedure: %v",
			migrationError.Version, migrationError.Name, migrationError.BackupPath, migrationError.Cause,
		)
	}
	return fmt.Sprintf("Faro database migration %d (%s) failed before a backup was available: %v", migrationError.Version, migrationError.Name, migrationError.Cause)
}

func (migrationError *MigrationError) Unwrap() error { return migrationError.Cause }

type migrationDefinition struct {
	version int
	name    string
	apply   func(context.Context, *Store) error
}

var migrationDefinitions = []migrationDefinition{
	{version: 1, name: "base-schema", apply: func(ctx context.Context, store *Store) error {
		return store.ensureSchema(ctx)
	}},
	{version: 2, name: "dns-record-types", apply: func(ctx context.Context, store *Store) error {
		return store.migrateDNSRecords(ctx)
	}},
	{version: 3, name: "protection-profiles", apply: func(ctx context.Context, store *Store) error {
		return store.migrateProtection(ctx)
	}},
	{version: 4, name: "blocklist-source-urls", apply: func(ctx context.Context, store *Store) error {
		return store.migrateBlocklistSources(ctx)
	}},
	{version: 5, name: "dns-query-upstream", apply: func(ctx context.Context, store *Store) error {
		return addColumnIfMissing(ctx, store.DB, `ALTER TABLE dns_queries ADD COLUMN upstream TEXT NOT NULL DEFAULT ''`)
	}},
	{version: 6, name: "query-decision-and-classification-columns", apply: func(ctx context.Context, store *Store) error {
		for _, statement := range []string{
			`ALTER TABLE dns_queries ADD COLUMN rcode TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE dns_queries ADD COLUMN decision_reason TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE dns_queries ADD COLUMN decision_metadata TEXT NOT NULL DEFAULT '{}'`,
			`ALTER TABLE dns_queries ADD COLUMN device_id INTEGER`,
			`ALTER TABLE device_classifications ADD COLUMN classified_query_id INTEGER NOT NULL DEFAULT 0`,
		} {
			if err := addColumnIfMissing(ctx, store.DB, statement); err != nil {
				return err
			}
		}
		_, err := store.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_dns_queries_device_timestamp ON dns_queries(device_id, timestamp)`)
		return err
	}},
	{version: 7, name: "stable-device-identities", apply: func(ctx context.Context, store *Store) error {
		return store.migrateDeviceIdentities(ctx)
	}},
	{version: 8, name: "application-version-metadata", apply: func(ctx context.Context, store *Store) error {
		for _, setting := range []struct {
			key   string
			value string
		}{
			{key: "database_schema_version", value: strconv.Itoa(CurrentSchemaVersion)},
			{key: "database_application_version", value: faroversion.Number},
		} {
			if _, err := store.DB.ExecContext(ctx, `
				INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, setting.key, setting.value); err != nil {
				return err
			}
		}
		return nil
	}},
	{version: 9, name: "history-query-indexes", apply: func(ctx context.Context, store *Store) error {
		return store.ensureHistoryIndexes(ctx)
	}},
	{version: 10, name: "history-query-performance", apply: func(ctx context.Context, store *Store) error {
		if err := store.ensureHistoryIndexes(ctx); err != nil {
			return err
		}
		if err := store.normalizeTimestampFormats(ctx); err != nil {
			return err
		}
		_, err := store.DB.ExecContext(ctx, `UPDATE settings SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = 'database_schema_version'`, strconv.Itoa(CurrentSchemaVersion))
		return err
	}},
	{version: 11, name: "time-aware-protection", apply: func(ctx context.Context, store *Store) error {
		for _, statement := range []string{
			`ALTER TABLE protection_profiles ADD COLUMN paused_until TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE protection_profiles ADD COLUMN schedule_enabled INTEGER NOT NULL DEFAULT 0 CHECK(schedule_enabled IN (0, 1))`,
			`ALTER TABLE protection_profiles ADD COLUMN schedule_days TEXT NOT NULL DEFAULT '1,2,3,4,5,6,7'`,
			`ALTER TABLE protection_profiles ADD COLUMN schedule_start TEXT NOT NULL DEFAULT '00:00'`,
			`ALTER TABLE protection_profiles ADD COLUMN schedule_end TEXT NOT NULL DEFAULT '23:59'`,
			`ALTER TABLE protection_profiles ADD COLUMN schedule_timezone TEXT NOT NULL DEFAULT 'UTC'`,
		} {
			if err := addColumnIfMissing(ctx, store.DB, statement); err != nil {
				return err
			}
		}
		_, err := store.DB.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS device_dns_pauses (
				device_id INTEGER PRIMARY KEY,
				paused_until TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE CASCADE
			);
			CREATE INDEX IF NOT EXISTS idx_device_dns_pauses_until ON device_dns_pauses(paused_until);
			UPDATE settings SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = 'database_schema_version';
		`, strconv.Itoa(CurrentSchemaVersion))
		return err
	}},
	{version: 12, name: "blocklist-source-mirror", apply: func(ctx context.Context, store *Store) error {
		return store.migrateBlocklistSources(ctx)
	}},
}

func upgradeStatePath(dbPath string) string {
	if configured := os.Getenv("FARO_UPGRADE_STATE_PATH"); configured != "" {
		return configured
	}
	return filepath.Join(filepath.Dir(dbPath), upgradeStateFilename)
}

// ReadUpgradeState reads the durable upgrade status associated with dbPath.
// A missing status file means this database predates the upgrade-state
// feature, not that an upgrade is currently running.
func ReadUpgradeState(dbPath string) (UpgradeState, error) {
	state, err := readUpgradeStateFile(upgradeStatePath(dbPath))
	if errors.Is(err, os.ErrNotExist) {
		return UpgradeState{Status: "unknown", ApplicationVersion: faroversion.Number}, nil
	}
	return state, err
}

func readUpgradeStateFile(path string) (UpgradeState, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return UpgradeState{}, err
	}
	var state UpgradeState
	if err := json.Unmarshal(contents, &state); err != nil {
		return UpgradeState{}, fmt.Errorf("decode upgrade state %s: %w", path, err)
	}
	return state, nil
}

func writeUpgradeState(path string, state UpgradeState) error {
	if path == "" {
		return errors.New("upgrade state path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".faro-upgrade-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		// Windows does not replace an existing file with Rename. The state file
		// is a small derived status artifact, so retry after replacing it.
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if retryErr := os.Rename(temporaryPath, path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (store *Store) setUpgradeState(ctx context.Context, state UpgradeState) error {
	if err := writeUpgradeState(store.UpgradeStatePath, state); err != nil {
		return err
	}
	if store.DB == nil {
		return nil
	}
	_, err := store.DB.ExecContext(ctx, `
		INSERT INTO faro_upgrade_state(
			id, status, application_version, from_version, to_version,
			current_migration, current_migration_name, backup_path, error,
			started_at, finished_at, updated_at
		) VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			application_version = excluded.application_version,
			from_version = excluded.from_version,
			to_version = excluded.to_version,
			current_migration = excluded.current_migration,
			current_migration_name = excluded.current_migration_name,
			backup_path = excluded.backup_path,
			error = excluded.error,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			updated_at = CURRENT_TIMESTAMP`,
		state.Status,
		state.ApplicationVersion,
		state.FromVersion,
		state.ToVersion,
		state.CurrentMigration,
		state.CurrentMigrationName,
		state.BackupPath,
		state.Error,
		state.StartedAt,
		state.FinishedAt,
	)
	return err
}

func (store *Store) migrate(ctx context.Context) error {
	userVersion, err := sqliteUserVersion(ctx, store.DB)
	if err != nil {
		return store.failWithoutRollback("could not read SQLite schema version", err)
	}
	if userVersion > CurrentSchemaVersion {
		return store.incompatibleVersion(userVersion)
	}
	metadataExists, err := migrationMetadataExists(ctx, store.DB)
	if err != nil {
		return store.failWithoutRollback("could not inspect Faro migration metadata", err)
	}
	currentVersion := 0
	if metadataExists {
		currentVersion, err = migrationLedgerVersion(ctx, store.DB, userVersion)
		if err != nil {
			return store.failWithoutRollback("could not read Faro migration ledger", err)
		}
	} else if userVersion != 0 {
		// Databases created before Faro kept no migration ledger. Re-run the
		// idempotent migrations so the first ledger is complete and auditable.
		currentVersion = 0
	}
	if currentVersion > CurrentSchemaVersion {
		return store.incompatibleVersion(currentVersion)
	}
	if currentVersion == CurrentSchemaVersion {
		if err := ensureMigrationMetadata(ctx, store.DB); err != nil {
			return store.failWithoutRollback("could not create migration metadata", err)
		}
		return nil
	}

	backupPath := ""
	if store.Path != ":memory:" {
		backupPath, err = store.createMigrationBackup(ctx, currentVersion)
		if err != nil {
			return store.failWithoutRollback("could not create automatic pre-migration backup", err)
		}
	}
	if err := ensureMigrationMetadata(ctx, store.DB); err != nil {
		if backupPath != "" {
			state := UpgradeState{
				Status:             "in_progress",
				ApplicationVersion: faroversion.Number,
				FromVersion:        currentVersion,
				ToVersion:          CurrentSchemaVersion,
				BackupPath:         backupPath,
				StartedAt:          time.Now().UTC().Format(time.RFC3339Nano),
			}
			return store.rollbackMigration(state, migrationDefinition{version: 0, name: "migration-metadata"}, backupPath, fmt.Errorf("create migration metadata: %w", err))
		}
		return store.failWithoutRollback("could not create migration metadata", err)
	}
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	state := UpgradeState{
		Status:             "in_progress",
		ApplicationVersion: faroversion.Number,
		FromVersion:        currentVersion,
		ToVersion:          CurrentSchemaVersion,
		BackupPath:         backupPath,
		StartedAt:          startedAt,
	}
	if err := store.setUpgradeState(ctx, state); err != nil {
		if backupPath != "" {
			return store.rollbackMigration(state, migrationDefinition{version: 0, name: "upgrade-state"}, backupPath, fmt.Errorf("persist upgrade-in-progress state: %w", err))
		}
		return store.failWithoutRollback("could not persist upgrade-in-progress state", err)
	}

	for _, migration := range migrationDefinitions {
		if migration.version <= currentVersion {
			continue
		}
		state.CurrentMigration = migration.version
		state.CurrentMigrationName = migration.name
		if err := store.setUpgradeState(ctx, state); err != nil {
			return store.rollbackMigration(state, migration, backupPath, fmt.Errorf("record migration progress: %w", err))
		}
		if store.migrationHook != nil {
			if err := store.migrationHook(migration.version, migration.name); err != nil {
				return store.rollbackMigration(state, migration, backupPath, err)
			}
		}
		if err := migration.apply(ctx, store); err != nil {
			return store.rollbackMigration(state, migration, backupPath, err)
		}
		if err := recordMigration(ctx, store.DB, migration.version, migration.name); err != nil {
			return store.rollbackMigration(state, migration, backupPath, fmt.Errorf("record migration completion: %w", err))
		}
	}

	state.Status = "complete"
	state.CurrentMigration = 0
	state.CurrentMigrationName = ""
	state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.setUpgradeState(ctx, state); err != nil {
		return store.rollbackMigration(state, migrationDefinitions[len(migrationDefinitions)-1], backupPath, fmt.Errorf("persist upgrade completion state: %w", err))
	}
	return nil
}

func ensureMigrationMetadata(ctx context.Context, database *sql.DB) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS faro_schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			application_version TEXT NOT NULL,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS faro_upgrade_state (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			status TEXT NOT NULL,
			application_version TEXT NOT NULL,
			from_version INTEGER NOT NULL DEFAULT 0,
			to_version INTEGER NOT NULL DEFAULT 0,
			current_migration INTEGER NOT NULL DEFAULT 0,
			current_migration_name TEXT NOT NULL DEFAULT '',
			backup_path TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL DEFAULT '',
			finished_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func sqliteUserVersion(ctx context.Context, database *sql.DB) (int, error) {
	var version int
	if err := database.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func migrationMetadataExists(ctx context.Context, database *sql.DB) (bool, error) {
	var count int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'faro_schema_migrations'`).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

func migrationLedgerVersion(ctx context.Context, database *sql.DB, userVersion int) (int, error) {
	rows, err := database.QueryContext(ctx, `SELECT version FROM faro_schema_migrations ORDER BY version`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	seen := make(map[int]bool)
	maximum := 0
	for rows.Next() {
		var migrationVersion int
		if err := rows.Scan(&migrationVersion); err != nil {
			return 0, err
		}
		if migrationVersion <= 0 {
			return 0, fmt.Errorf("Faro migration ledger contains invalid version %d", migrationVersion)
		}
		seen[migrationVersion] = true
		if migrationVersion > maximum {
			maximum = migrationVersion
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for migrationVersion := 1; migrationVersion <= maximum; migrationVersion++ {
		if !seen[migrationVersion] {
			return 0, fmt.Errorf("Faro migration ledger is incomplete: version %d is missing", migrationVersion)
		}
	}
	if userVersion != 0 && userVersion < maximum {
		return 0, fmt.Errorf("SQLite user_version %d is behind migration ledger version %d", userVersion, maximum)
	}
	version := userVersion
	if maximum > version {
		version = maximum
	}
	if version < 0 {
		return 0, fmt.Errorf("SQLite schema version cannot be negative: %d", version)
	}
	return version, nil
}

func recordMigration(ctx context.Context, database *sql.DB, version int, name string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO faro_schema_migrations(version, name, application_version, applied_at)
		VALUES(?, ?, ?, CURRENT_TIMESTAMP)`, version, name, faroversion.Number); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return err
	}
	return tx.Commit()
}

func addColumnIfMissing(ctx context.Context, database *sql.DB, statement string) error {
	if _, err := database.ExecContext(ctx, statement); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	return nil
}

func (store *Store) createMigrationBackup(ctx context.Context, fromVersion int) (string, error) {
	if store.Path == ":memory:" {
		return "", nil
	}
	backupDir := os.Getenv("FARO_MIGRATION_BACKUP_DIR")
	if backupDir == "" {
		backupDir = filepath.Join(filepath.Dir(store.Path), migrationBackupDir)
	}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return "", err
	}
	backupPath := filepath.Join(backupDir, fmt.Sprintf(
		"faro-pre-migration-%s-from-%d-to-%d.db",
		time.Now().UTC().Format("20060102T150405.000000000Z"),
		fromVersion,
		CurrentSchemaVersion,
	))
	if err := snapshotDatabase(ctx, store.DB, backupPath); err != nil {
		_ = os.Remove(backupPath)
		return "", err
	}
	if err := verifySQLiteSnapshot(backupPath); err != nil {
		_ = os.Remove(backupPath)
		return "", err
	}
	return backupPath, nil
}

func snapshotDatabase(ctx context.Context, source *sql.DB, destinationPath string) error {
	destination, err := sql.Open("sqlite3", destinationPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer func() { _ = destination.Close() }()
	sourceConn, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = sourceConn.Close() }()
	destinationConn, err := destination.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = destinationConn.Close() }()
	return sourceConn.Raw(func(sourceDriver any) error {
		src, ok := sourceDriver.(*sqlite3.SQLiteConn)
		if !ok {
			return errors.New("unexpected SQLite source connection")
		}
		return destinationConn.Raw(func(destinationDriver any) error {
			dst, ok := destinationDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return errors.New("unexpected SQLite destination connection")
			}
			backup, err := dst.Backup("main", src, "main")
			if err != nil {
				return err
			}
			done, stepErr := backup.Step(-1)
			finishErr := backup.Finish()
			if stepErr != nil {
				return stepErr
			}
			if finishErr != nil {
				return finishErr
			}
			if !done {
				return errors.New("SQLite backup did not complete")
			}
			return nil
		})
	})
}

func verifySQLiteSnapshot(path string) error {
	database, err := sql.Open("sqlite3", path+"?mode=ro&_query_only=1&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	if err := database.Ping(); err != nil {
		return err
	}
	var result string
	if err := database.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite migration backup integrity check failed: %s", result)
	}
	return nil
}

func (store *Store) rollbackMigration(state UpgradeState, migration migrationDefinition, backupPath string, cause error) error {
	if store.DB != nil {
		_ = store.DB.Close()
	}
	restored := false
	rollbackError := error(nil)
	if backupPath != "" {
		_, rollbackError = restoreDatabaseFile(store.Path, backupPath)
		restored = rollbackError == nil
	}
	state.Status = "failed"
	state.CurrentMigration = migration.version
	state.CurrentMigrationName = migration.name
	state.Error = cause.Error()
	state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if rollbackError != nil {
		state.Error = errors.Join(cause, fmt.Errorf("restore pre-migration database: %w", rollbackError)).Error()
	}
	_ = writeUpgradeState(store.UpgradeStatePath, state)
	if rollbackError != nil {
		cause = errors.Join(cause, rollbackError)
	}
	return &MigrationError{
		Version:    migration.version,
		Name:       migration.name,
		BackupPath: backupPath,
		Restored:   restored,
		Cause:      cause,
	}
}

func (store *Store) failWithoutRollback(message string, cause error) error {
	state := UpgradeState{
		Status:             "failed",
		ApplicationVersion: faroversion.Number,
		ToVersion:          CurrentSchemaVersion,
		Error:              fmt.Errorf("%s: %w", message, cause).Error(),
		FinishedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
	_ = writeUpgradeState(store.UpgradeStatePath, state)
	return fmt.Errorf("%s: %w", message, cause)
}

func (store *Store) incompatibleVersion(found int) error {
	state := UpgradeState{
		Status:             "incompatible",
		ApplicationVersion: faroversion.Number,
		FromVersion:        found,
		ToVersion:          CurrentSchemaVersion,
		Error:              fmt.Sprintf("database schema version %d is newer than supported version %d", found, CurrentSchemaVersion),
		FinishedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
	_ = writeUpgradeState(store.UpgradeStatePath, state)
	return &IncompatibleVersionError{Path: store.Path, Found: found, Supported: CurrentSchemaVersion}
}

func restoreDatabaseFile(databasePath, backupPath string) (string, error) {
	if databasePath == "" || backupPath == "" || filepath.Clean(databasePath) == filepath.Clean(backupPath) {
		return "", errors.New("database and migration backup paths must be different")
	}
	if _, err := os.Stat(backupPath); err != nil {
		return "", err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(databasePath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	failedPath := ""
	if _, err := os.Stat(databasePath); err == nil {
		failedPath = fmt.Sprintf("%s.failed-%s", databasePath, time.Now().UTC().Format("20060102T150405.000000000Z"))
		if err := os.Rename(databasePath, failedPath); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := copyFile(backupPath, databasePath); err != nil {
		return failedPath, err
	}
	return failedPath, nil
}

func copyFile(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	if syncErr := destination.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := destination.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func recoverInterruptedMigration(dbPath string) error {
	state, err := readUpgradeStateFile(upgradeStatePath(dbPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.Status != "in_progress" {
		return nil
	}
	if state.BackupPath == "" {
		state.Status = "failed"
		state.Error = "an interrupted upgrade has no recovery backup; preserve the database and follow the documented manual recovery procedure"
		state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		_ = writeUpgradeState(upgradeStatePath(dbPath), state)
		return fmt.Errorf("Faro database upgrade was interrupted and has no recovery backup; preserve %s and follow the documented manual recovery procedure", dbPath)
	}
	if _, err := restoreDatabaseFile(dbPath, state.BackupPath); err != nil {
		state.Status = "failed"
		state.Error = fmt.Sprintf("automatic recovery of interrupted upgrade failed: %v", err)
		state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		_ = writeUpgradeState(upgradeStatePath(dbPath), state)
		return fmt.Errorf("recover interrupted Faro database upgrade: %w", err)
	}
	state.Status = "recovered"
	state.Error = "an interrupted upgrade was restored from the pre-migration backup; Faro will retry the upgrade"
	state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writeUpgradeState(upgradeStatePath(dbPath), state)
}

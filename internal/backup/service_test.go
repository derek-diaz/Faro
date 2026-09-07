package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/redundancy"
)

const testPassphrase = "correct horse backup staple"

func TestPortableBackupDoesNotReactivateTemporaryTests(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO devices(id,name) VALUES(1,'Trial device');
		INSERT INTO troubleshooting_exceptions(token,client_ip,device_id,protection_id,domain,expires_at)
		SELECT 'trial','192.0.2.10',1,id,'temporary.example',datetime('now','+10 minutes') FROM protection_profiles WHERE is_default=1`); err != nil {
		t.Fatal(err)
	}
	service := NewService(store)
	path, _, cleanup, err := service.Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, transaction, err := service.BeginRestore(context.Background(), input, testPassphrase)
	_ = input.Close()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM troubleshooting_exceptions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("portable restore reactivated a temporary exception")
	}
	if err := transaction.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM troubleshooting_exceptions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("failed restore did not recover the original test")
	}
}

func TestEncryptedBackupRestoreLifecycle(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO users(username, password_hash) VALUES('backup-admin', 'secret-hash')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO auth_sessions(user_id, token_hash, expires_at) VALUES(1, 'old-session-token', datetime('now', '+1 day'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO dns_records(hostname, type, value, description) VALUES('saved.home', 'A', '192.168.1.20', 'backup fixture')`); err != nil {
		t.Fatal(err)
	}
	protectionResult, err := store.DB.Exec(`INSERT INTO protection_profiles(name, icon) VALUES('Children', 'baby')`)
	if err != nil {
		t.Fatal(err)
	}
	protectionID, _ := protectionResult.LastInsertId()
	if _, err := store.DB.Exec(`INSERT INTO protection_block_entries(protection_id, domain) VALUES(?, 'games.example'); INSERT INTO device_protection_assignments(client_ip, protection_id) VALUES('192.168.7.23', ?)`, protectionID, protectionID); err != nil {
		t.Fatal(err)
	}

	service := NewService(store)
	path, manifest, cleanup, err := service.Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if manifest.FormatVersion != FormatVersion || manifest.DatabaseBytes == 0 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	encrypted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range [][]byte{[]byte("saved.home"), []byte("secret-hash"), []byte("old-session-token")} {
		if bytes.Contains(encrypted, secret) {
			t.Fatalf("encrypted backup exposed plaintext %q", secret)
		}
	}

	if _, err := store.DB.Exec(`DELETE FROM dns_records WHERE hostname = 'saved.home'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`DELETE FROM protection_profiles WHERE id = ?`, protectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE settings SET value = '8.8.8.8' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	restoredManifest, err := service.Restore(context.Background(), input, testPassphrase)
	_ = input.Close()
	if err != nil {
		t.Fatal(err)
	}
	if restoredManifest.CreatedAt != manifest.CreatedAt {
		t.Fatalf("restored manifest = %#v, want %#v", restoredManifest, manifest)
	}
	var address string
	if err := store.DB.QueryRow(`SELECT value FROM dns_records WHERE hostname = 'saved.home'`).Scan(&address); err != nil {
		t.Fatal(err)
	}
	if address != "192.168.1.20" {
		t.Fatalf("restored address = %q", address)
	}
	var upstream string
	if err := store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&upstream); err != nil {
		t.Fatal(err)
	}
	if upstream != "1.1.1.1,9.9.9.9" {
		t.Fatalf("restored upstream = %q", upstream)
	}
	var restoredProtection, restoredDomain string
	if err := store.DB.QueryRow(`
		SELECT p.name, b.domain
		FROM protection_profiles p
		JOIN protection_block_entries b ON b.protection_id = p.id
		JOIN device_protection_assignments a ON a.protection_id = p.id
		WHERE a.client_ip = '192.168.7.23'
	`).Scan(&restoredProtection, &restoredDomain); err != nil {
		t.Fatal(err)
	}
	if restoredProtection != "Children" || restoredDomain != "games.example" {
		t.Fatalf("restored protection=%q domain=%q", restoredProtection, restoredDomain)
	}
	var sessions int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM auth_sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("restored auth sessions = %d, want 0", sessions)
	}
}

func TestRestoreOntoFreshInstallation(t *testing.T) {
	source, err := db.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.DB.Exec(`
		INSERT INTO users(username, password_hash) VALUES('source-admin', 'source-hash');
		INSERT INTO dns_records(hostname, type, value, description) VALUES('fresh-restore.home', 'A', '192.168.1.44', 'portable source');
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, upstream) VALUES(CURRENT_TIMESTAMP, '192.168.1.44', 'source.example', 'A', 'allowed', 'upstream', '1.1.1.1');
	`); err != nil {
		t.Fatal(err)
	}

	backupService := NewService(source)
	backupPath, manifest, cleanup, err := backupService.Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if manifest.ApplicationVersion == "" || manifest.SchemaVersion != db.CurrentSchemaVersion {
		t.Fatalf("backup manifest did not identify its source schema: %#v", manifest)
	}

	fresh, err := db.Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	input, err := os.Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(fresh).Restore(context.Background(), input, testPassphrase); err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}

	var address, username, domain string
	if err := fresh.DB.QueryRow(`SELECT value FROM dns_records WHERE hostname = 'fresh-restore.home'`).Scan(&address); err != nil {
		t.Fatal(err)
	}
	if err := fresh.DB.QueryRow(`SELECT username FROM users WHERE username = 'source-admin'`).Scan(&username); err != nil {
		t.Fatal(err)
	}
	if err := fresh.DB.QueryRow(`SELECT domain FROM dns_queries WHERE domain = 'source.example'`).Scan(&domain); err != nil {
		t.Fatal(err)
	}
	if address != "192.168.1.44" || username != "source-admin" || domain != "source.example" {
		t.Fatalf("fresh restore lost source data: address=%q username=%q domain=%q", address, username, domain)
	}
}

func TestRestoreLargeDatabase(t *testing.T) {
	source, err := db.Open(filepath.Join(t.TempDir(), "large-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	const queryCount = 20000
	tx, err := source.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, upstream)
		VALUES(CURRENT_TIMESTAMP, ?, ?, 'A', 'allowed', 'upstream', '1.1.1.1')`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	for queryIndex := 0; queryIndex < queryCount; queryIndex++ {
		if _, err := statement.Exec("192.168.20.10", fmt.Sprintf("large-%05d.example", queryIndex)); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	path, manifest, cleanup, err := NewService(source).Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if manifest.DatabaseBytes <= 1<<20 {
		t.Fatalf("large fixture database is only %d bytes", manifest.DatabaseBytes)
	}

	target, err := db.Open(filepath.Join(t.TempDir(), "large-target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(target).Restore(context.Background(), input, testPassphrase); err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	var restored int
	if err := target.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries WHERE domain LIKE 'large-%'`).Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if restored != queryCount {
		t.Fatalf("restored large query history = %d, want %d", restored, queryCount)
	}
}

func TestRestoreWithAndWithoutQueryHistory(t *testing.T) {
	source, err := db.Open(filepath.Join(t.TempDir(), "history-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.DB.Exec(`
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, upstream) VALUES(CURRENT_TIMESTAMP, '192.168.1.50', 'history.example', 'A', 'allowed', 'upstream', '1.1.1.1')`); err != nil {
		t.Fatal(err)
	}
	service := NewService(source)
	withHistoryPath, _, withHistoryCleanup, err := service.Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer withHistoryCleanup()
	if _, err := source.DB.Exec(`DELETE FROM dns_queries`); err != nil {
		t.Fatal(err)
	}
	withoutHistoryPath, _, withoutHistoryCleanup, err := service.Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer withoutHistoryCleanup()

	target, err := db.Open(filepath.Join(t.TempDir(), "history-target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	withHistory, err := os.Open(withHistoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(target).Restore(context.Background(), withHistory, testPassphrase); err != nil {
		_ = withHistory.Close()
		t.Fatal(err)
	}
	if err := withHistory.Close(); err != nil {
		t.Fatal(err)
	}
	var historyCount int
	if err := target.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries WHERE domain = 'history.example'`).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 {
		t.Fatalf("restore with query history returned %d rows, want 1", historyCount)
	}

	withoutHistory, err := os.Open(withoutHistoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(target).Restore(context.Background(), withoutHistory, testPassphrase); err != nil {
		_ = withoutHistory.Close()
		t.Fatal(err)
	}
	if err := withoutHistory.Close(); err != nil {
		t.Fatal(err)
	}
	if err := target.DB.QueryRow(`SELECT COUNT(*) FROM dns_queries`).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 0 {
		t.Fatalf("restore without query history returned %d rows, want 0", historyCount)
	}
}

func TestWrongPassphraseDoesNotMutateDatabase(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(store)
	path, _, cleanup, err := service.Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := store.DB.Exec(`UPDATE settings SET value = '8.8.4.4' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, restoreErr := service.Restore(context.Background(), input, "definitely the wrong password")
	_ = input.Close()
	if !errors.Is(restoreErr, ErrInvalidBackup) {
		t.Fatalf("restore error = %v, want ErrInvalidBackup", restoreErr)
	}
	var upstream string
	if err := store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&upstream); err != nil {
		t.Fatal(err)
	}
	if upstream != "8.8.4.4" {
		t.Fatalf("database changed after rejected restore: upstream = %q", upstream)
	}
}

func TestCorruptAndTruncatedBackupsAreRejected(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path, _, cleanup, err := NewService(store).Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(original) < 2 {
		t.Fatal("encrypted backup fixture is unexpectedly short")
	}

	corrupt := append([]byte(nil), original...)
	corrupt[len(corrupt)/2] ^= 0x80
	if _, err := NewService(store).Restore(context.Background(), bytes.NewReader(corrupt), testPassphrase); !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("corrupt archive restore error = %v, want ErrInvalidBackup", err)
	}
	truncated := original[:len(original)-1]
	if _, err := NewService(store).Restore(context.Background(), bytes.NewReader(truncated), testPassphrase); !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("truncated archive restore error = %v, want ErrInvalidBackup", err)
	}
}

func TestUnsupportedFutureBackupVersionIsRejected(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshotPath := filepath.Join(t.TempDir(), "future-snapshot.db")
	if err := snapshotDatabase(context.Background(), store.DB, snapshotPath); err != nil {
		t.Fatal(err)
	}
	if err := scrubSnapshot(snapshotPath); err != nil {
		t.Fatal(err)
	}
	futureManifest := Manifest{
		FormatVersion:      FormatVersion + 1,
		ApplicationVersion: "1.1.0",
		SchemaVersion:      db.CurrentSchemaVersion,
		CreatedAt:          "2026-08-06T00:00:00Z",
	}
	path := createEncryptedArchive(t, snapshotPath, futureManifest)
	if _, err := NewService(store).Restore(context.Background(), mustOpenBackup(t, path), testPassphrase); err == nil || !strings.Contains(err.Error(), "unsupported backup format version") {
		t.Fatalf("future manifest restore error = %v, want unsupported format version", err)
	}

	currentPath, _, cleanup, err := NewService(store).Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	currentBytes, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(currentBytes) <= len(magic) {
		t.Fatal("encrypted backup fixture has no version byte")
	}
	currentBytes[len(magic)] = byte(FormatVersion + 1)
	if _, err := NewService(store).Restore(context.Background(), bytes.NewReader(currentBytes), testPassphrase); !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("future encrypted version restore error = %v, want ErrInvalidBackup", err)
	}
}

func TestRestoreAcrossMinorFaroVersion(t *testing.T) {
	legacyPath := createLegacyEncryptedBackup(t)
	target, err := db.Open(filepath.Join(t.TempDir(), "minor-target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	manifest, err := NewService(target).Restore(context.Background(), mustOpenBackup(t, legacyPath), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ApplicationVersion != "0.2.0" || manifest.SchemaVersion != 0 {
		t.Fatalf("legacy manifest changed unexpectedly: %#v", manifest)
	}
	var address, domain string
	if err := target.DB.QueryRow(`SELECT value FROM dns_records WHERE hostname = 'legacy-restore.home'`).Scan(&address); err != nil {
		t.Fatal(err)
	}
	if err := target.DB.QueryRow(`SELECT domain FROM dns_queries WHERE domain = 'legacy-history.example'`).Scan(&domain); err != nil {
		t.Fatal(err)
	}
	if address != "192.168.1.77" || domain != "legacy-history.example" {
		t.Fatalf("minor-version restore lost migrated data: address=%q domain=%q", address, domain)
	}
}

func TestRestoreKeepsConnectedReplicaUsable(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		UPDATE redundancy_state
		SET role = 'controller', home_id = 'home-connected', config_revision = 1
		WHERE id = 1;
		INSERT INTO redundancy_nodes(node_id, name, secret_ciphertext, config_revision)
		VALUES('0123456789abcdef0123456789abcdef', 'Connected replica', 'replica-secret', 1);
		INSERT INTO redundancy_snapshots(revision, payload) VALUES(1, 'connected-snapshot');
	`); err != nil {
		t.Fatal(err)
	}
	path, _, cleanup, err := NewService(store).Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := store.DB.Exec(`UPDATE settings SET value = '8.8.8.8' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(store).Restore(context.Background(), mustOpenBackup(t, path), testPassphrase); err != nil {
		t.Fatal(err)
	}

	var role string
	var nodes int
	if err := store.DB.QueryRow(`SELECT role FROM redundancy_state WHERE id = 1`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM redundancy_nodes WHERE node_id = '0123456789abcdef0123456789abcdef'`).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if role != redundancy.RoleController || nodes != 1 {
		t.Fatalf("restore changed connected replica relationship: role=%q nodes=%d", role, nodes)
	}
	manager := redundancy.NewManager(store, nil, "", "")
	envelope, revision, err := manager.SnapshotEnvelope(context.Background(), 0, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if envelope == nil || revision != 1 {
		t.Fatalf("connected replica could not request its snapshot: envelope=%v revision=%d", envelope != nil, revision)
	}
}

func TestPortableSnapshotRemovesRedundancyMembership(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		UPDATE redundancy_state
		SET role = 'controller', home_id = 'home-secret', secret_ciphertext = 'node-secret', config_revision = 7
		WHERE id = 1;
		INSERT INTO redundancy_nodes(node_id, name, secret_ciphertext)
		VALUES('0123456789abcdef0123456789abcdef', 'Replica', 'replica-secret');
		INSERT INTO redundancy_snapshots(revision, payload) VALUES(7, 'snapshot-secret');
	`); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(t.TempDir(), "portable.db")
	if err := snapshotDatabase(context.Background(), store.DB, snapshotPath); err != nil {
		t.Fatal(err)
	}
	if err := scrubSnapshot(snapshotPath); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Open(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var role, homeID, secret string
	var revision, nodes, snapshots int
	if err := snapshot.DB.QueryRow(`
		SELECT role, home_id, secret_ciphertext, config_revision
		FROM redundancy_state WHERE id = 1`).Scan(&role, &homeID, &secret, &revision); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.DB.QueryRow(`SELECT COUNT(*) FROM redundancy_nodes`).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.DB.QueryRow(`SELECT COUNT(*) FROM redundancy_snapshots`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if role != "standalone" || homeID != "" || secret != "" || revision != 0 || nodes != 0 || snapshots != 0 {
		t.Fatalf("portable snapshot retained redundancy state: role=%q home=%q secret=%q revision=%d nodes=%d snapshots=%d",
			role, homeID, secret, revision, nodes, snapshots)
	}
}

func TestRestoreTransactionRollbackRecoversPreviousDatabase(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO users(username, password_hash) VALUES('current-admin', 'current-hash')`); err != nil {
		t.Fatal(err)
	}
	service := NewService(store)
	path, _, cleanup, err := service.Create(context.Background(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if _, err := store.DB.Exec(`UPDATE settings SET value = '8.8.4.4' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO auth_sessions(user_id, token_hash, expires_at) VALUES(1, 'current-session', datetime('now', '+1 day'))`); err != nil {
		t.Fatal(err)
	}
	deviceResult, err := store.DB.Exec(`INSERT INTO devices(name, device_type) VALUES('Current device', 'Camera')`)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, _ := deviceResult.LastInsertId()
	if _, err := store.DB.Exec(`
		INSERT INTO device_names(device_id, source, name) VALUES(?, 'unifi', 'Current camera');
		INSERT INTO unifi_client_snapshots(client_id, site_id, device_id, name) VALUES('client-1', 'default', ?, 'Current camera');
		INSERT INTO device_classifications(
			device_id, catalog_version, definition_id, predicted_type, category, icon,
			confidence, score, signal_hash, evidence_json, evaluated_at
		) VALUES(?, 'test', 'camera', 'Camera', 'iot', 'camera', 'high', 100, 'signal', '[]', CURRENT_TIMESTAMP);
	`, deviceID, deviceID, deviceID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.DB.Exec(`INSERT INTO troubleshooting_exceptions(token,client_ip,device_id,protection_id,domain,expires_at) SELECT 'rollback-test','192.0.2.10',?,id,'temporary.example',datetime('now','+10 minutes') FROM protection_profiles WHERE is_default=1`, deviceID); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, transaction, err := service.BeginRestore(context.Background(), input, testPassphrase)
	_ = input.Close()
	if err != nil {
		t.Fatal(err)
	}
	var restoredUpstream string
	if err := store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&restoredUpstream); err != nil {
		t.Fatal(err)
	}
	if restoredUpstream != "1.1.1.1,9.9.9.9" {
		t.Fatalf("staged upstream = %q", restoredUpstream)
	}

	if err := transaction.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	var currentUpstream string
	if err := store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&currentUpstream); err != nil {
		t.Fatal(err)
	}
	if currentUpstream != "8.8.4.4" {
		t.Fatalf("rolled back upstream = %q, want current value", currentUpstream)
	}
	for table, want := range map[string]int{
		"troubleshooting_exceptions": 1,
		"auth_sessions":              1,
		"device_names":               1,
		"unifi_client_snapshots":     1,
		"device_classifications":     1,
	} {
		var count int
		if err := store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s rows after rollback = %d, want %d", table, count, want)
		}
	}
}

func TestTruncatedEncryptedStreamIsRejected(t *testing.T) {
	var encrypted bytes.Buffer
	if err := encrypt(&encrypted, bytes.NewBufferString("complete payload"), testPassphrase); err != nil {
		t.Fatal(err)
	}
	data := encrypted.Bytes()
	var plaintext bytes.Buffer
	if err := decrypt(&plaintext, bytes.NewReader(data[:len(data)-1]), testPassphrase); !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("decrypt truncated stream error = %v", err)
	}
}

func mustOpenBackup(t *testing.T, path string) *os.File {
	t.Helper()
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	return input
}

func createEncryptedArchive(t *testing.T, databasePath string, manifest Manifest) string {
	t.Helper()
	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DatabaseBytes == 0 {
		manifest.DatabaseBytes = info.Size()
	}
	archivePath := filepath.Join(t.TempDir(), "payload.zip")
	if err := writeArchive(archivePath, databasePath, manifest); err != nil {
		t.Fatal(err)
	}
	encryptedPath := filepath.Join(t.TempDir(), "backup.faro-backup")
	if err := encryptFile(archivePath, encryptedPath, testPassphrase); err != nil {
		t.Fatal(err)
	}
	return encryptedPath
}

func createLegacyEncryptedBackup(t *testing.T) string {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "faro-0.2.0.db")
	database, err := sql.Open("sqlite3", databasePath+"?_foreign_keys=on&_busy_timeout=5000")
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
			_ = database.Close()
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	if _, err := database.Exec(`
		INSERT INTO dns_records(hostname, type, value, description) VALUES('legacy-restore.home', 'A', '192.168.1.77', 'legacy release');
		INSERT INTO blocklists(name, url, enabled) VALUES('Legacy list', 'https://lists.example/legacy.txt', 1);
		INSERT INTO blocklist_entries(blocklist_id, domain) VALUES(1, 'ads.legacy.example');
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, upstream) VALUES(CURRENT_TIMESTAMP, '192.168.1.77', 'legacy-history.example', 'A', 'allowed', 'upstream', '1.1.1.1');
		INSERT INTO device_aliases(client_ip, name, location, notes) VALUES('192.168.1.77', 'Legacy device', 'Home', 'minor release');
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	return createEncryptedArchive(t, databasePath, Manifest{
		FormatVersion:      FormatVersion,
		ApplicationVersion: "0.2.0",
		SchemaVersion:      0,
		CreatedAt:          "2025-02-01T00:00:00Z",
		DatabaseBytes:      info.Size(),
	})
}

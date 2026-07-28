package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/derek/faro/internal/db"
)

const testPassphrase = "correct horse backup staple"

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
		"auth_sessions":          1,
		"device_names":           1,
		"unifi_client_snapshots": 1,
		"device_classifications": 1,
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

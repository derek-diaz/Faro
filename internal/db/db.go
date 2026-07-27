package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	database, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)

	store := &Store{DB: database}
	if err := store.migrate(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := store.seed(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS dns_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hostname TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'A',
			value TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(hostname, type, value)
		);`,
		`CREATE TABLE IF NOT EXISTS blocklists (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_refreshed_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS blocklist_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			blocklist_id INTEGER NOT NULL,
			domain TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(blocklist_id) REFERENCES blocklists(id) ON DELETE CASCADE,
			UNIQUE(blocklist_id, domain)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_blocklist_entries_domain ON blocklist_entries(domain);`,
		`CREATE TABLE IF NOT EXISTS protection_profiles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			icon TEXT NOT NULL DEFAULT 'shield',
			is_default INTEGER NOT NULL DEFAULT 0 CHECK(is_default IN (0, 1)),
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_protection_profiles_default ON protection_profiles(is_default) WHERE is_default = 1;`,
		`CREATE TABLE IF NOT EXISTS protection_blocklists (
			protection_id INTEGER NOT NULL,
			blocklist_id INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(protection_id, blocklist_id),
			FOREIGN KEY(protection_id) REFERENCES protection_profiles(id) ON DELETE CASCADE,
			FOREIGN KEY(blocklist_id) REFERENCES blocklists(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS protection_allow_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			protection_id INTEGER NOT NULL,
			domain TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(protection_id) REFERENCES protection_profiles(id) ON DELETE CASCADE,
			UNIQUE(protection_id, domain)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_protection_allow_domain ON protection_allow_entries(protection_id, domain);`,
		`CREATE TABLE IF NOT EXISTS protection_block_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			protection_id INTEGER NOT NULL,
			domain TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(protection_id) REFERENCES protection_profiles(id) ON DELETE CASCADE,
			UNIQUE(protection_id, domain)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_protection_block_domain ON protection_block_entries(protection_id, domain);`,
		`CREATE TABLE IF NOT EXISTS device_protection_assignments (
			client_ip TEXT PRIMARY KEY,
			protection_id INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(protection_id) REFERENCES protection_profiles(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_device_protection_profile ON device_protection_assignments(protection_id);`,
		`CREATE TABLE IF NOT EXISTS devices (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL DEFAULT '',
			location TEXT,
			notes TEXT,
			device_type TEXT NOT NULL DEFAULT '',
			type_source TEXT NOT NULL DEFAULT '',
			confirmed INTEGER NOT NULL DEFAULT 0 CHECK(confirmed IN (0, 1)),
			first_seen_at TEXT,
			last_seen_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS device_addresses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id INTEGER NOT NULL,
			address TEXT NOT NULL UNIQUE,
			family TEXT NOT NULL CHECK(family IN ('ipv4', 'ipv6')),
			source TEXT NOT NULL DEFAULT 'dns',
			confidence TEXT NOT NULL DEFAULT 'observed',
			first_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_device_addresses_device ON device_addresses(device_id);`,
		`CREATE TABLE IF NOT EXISTS device_identifiers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			value TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			confidence TEXT NOT NULL DEFAULT 'observed',
			first_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE CASCADE,
			UNIQUE(kind, value)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_device_identifiers_device ON device_identifiers(device_id);`,
		`CREATE TABLE IF NOT EXISTS device_names (
			device_id INTEGER NOT NULL,
			source TEXT NOT NULL,
			name TEXT NOT NULL,
			first_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(device_id, source),
			FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS integration_configs (
			kind TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0, 1)),
			base_url TEXT NOT NULL DEFAULT '',
			secret_ciphertext TEXT NOT NULL DEFAULT '',
			site_id TEXT NOT NULL DEFAULT '',
			site_name TEXT NOT NULL DEFAULT '',
			tls_fingerprint TEXT NOT NULL DEFAULT '',
			last_sync_at TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			synced_devices INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS unifi_client_snapshots (
			client_id TEXT NOT NULL,
			site_id TEXT NOT NULL,
			device_id INTEGER NOT NULL,
			mac_address TEXT NOT NULL DEFAULT '',
			ip_address TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			connection_type TEXT NOT NULL DEFAULT '',
			uplink_device_id TEXT NOT NULL DEFAULT '',
			connected_at TEXT,
			last_synced_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(site_id, client_id),
			FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_unifi_client_snapshots_device ON unifi_client_snapshots(device_id);`,
		`CREATE TABLE IF NOT EXISTS device_classifications (
			device_id INTEGER PRIMARY KEY,
			catalog_version TEXT NOT NULL,
			definition_id TEXT NOT NULL DEFAULT '',
			predicted_type TEXT NOT NULL DEFAULT 'Unknown',
			category TEXT NOT NULL DEFAULT 'unknown',
			icon TEXT NOT NULL DEFAULT 'monitor',
			confidence TEXT NOT NULL DEFAULT 'unknown',
			score INTEGER NOT NULL DEFAULT 0,
			signal_hash TEXT NOT NULL,
			evidence_json TEXT NOT NULL DEFAULT '[]',
			evaluated_at TEXT NOT NULL,
			classified_query_id INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_device_classifications_catalog ON device_classifications(catalog_version);`,
		`CREATE TABLE IF NOT EXISTS device_protection_memberships (
			device_id INTEGER PRIMARY KEY,
			protection_id INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE CASCADE,
			FOREIGN KEY(protection_id) REFERENCES protection_profiles(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_device_membership_protection ON device_protection_memberships(protection_id);`,
		`CREATE TABLE IF NOT EXISTS allowlist_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS manual_block_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS dns_queries (
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
		);`,
		`CREATE INDEX IF NOT EXISTS idx_dns_queries_timestamp ON dns_queries(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_dns_queries_domain ON dns_queries(domain);`,
		`CREATE INDEX IF NOT EXISTS idx_dns_queries_client_ip ON dns_queries(client_ip);`,
		`CREATE INDEX IF NOT EXISTS idx_dns_queries_client_timestamp ON dns_queries(client_ip, timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_dns_queries_action ON dns_queries(action);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS device_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			client_ip TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			location TEXT,
			notes TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS events (
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
		);`,
		`CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_events_type ON events(type);`,
		`CREATE INDEX IF NOT EXISTS idx_events_severity ON events(severity);`,
		`CREATE INDEX IF NOT EXISTS idx_events_domain ON events(domain);`,
		`CREATE INDEX IF NOT EXISTS idx_events_client_ip ON events(client_ip);`,
		`CREATE TABLE IF NOT EXISTS domain_favicons (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL UNIQUE,
			favicon_url TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			last_checked_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			singleton INTEGER NOT NULL DEFAULT 1 UNIQUE CHECK(singleton = 1),
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_expires_at ON auth_sessions(expires_at);`,
		`CREATE TABLE IF NOT EXISTS notification_states (
			user_id INTEGER NOT NULL,
			event_key TEXT NOT NULL,
			read_at TEXT,
			dismissed_at TEXT,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(user_id, event_key),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_notification_states_updated_at ON notification_states(updated_at);`,
	}
	for _, stmt := range schema {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.migrateDNSRecords(ctx); err != nil {
		return err
	}
	if err := s.migrateProtection(ctx); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `ALTER TABLE dns_queries ADD COLUMN upstream TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	for _, column := range []string{
		`ALTER TABLE dns_queries ADD COLUMN rcode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE dns_queries ADD COLUMN decision_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE dns_queries ADD COLUMN decision_metadata TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE dns_queries ADD COLUMN device_id INTEGER`,
		`ALTER TABLE device_classifications ADD COLUMN classified_query_id INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := s.DB.ExecContext(ctx, column); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_dns_queries_device_timestamp ON dns_queries(device_id, timestamp)`); err != nil {
		return err
	}
	if err := s.migrateDeviceIdentities(ctx); err != nil {
		return err
	}
	return nil
}

type legacyDeviceIdentity struct {
	address   string
	name      string
	location  sql.NullString
	notes     sql.NullString
	firstSeen sql.NullString
	lastSeen  sql.NullString
}

// migrateDeviceIdentities moves the original IP-keyed device data into stable
// device records. The original tables deliberately remain available so older
// backups and clients can still be restored while the application transitions.
func (s *Store) migrateDeviceIdentities(ctx context.Context) error {
	var completed string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'device_identity_migration_completed'`).Scan(&completed)
	if err == nil && completed == "true" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	rows, err := s.DB.QueryContext(ctx, `
		WITH known_addresses AS (
			SELECT client_ip FROM dns_queries
			UNION SELECT client_ip FROM device_aliases
			UNION SELECT client_ip FROM device_protection_assignments
		)
		SELECT k.client_ip,
		       COALESCE(a.name, ''), a.location, a.notes,
		       MIN(q.timestamp), MAX(q.timestamp)
		FROM known_addresses k
		LEFT JOIN device_aliases a ON a.client_ip = k.client_ip
		LEFT JOIN dns_queries q ON q.client_ip = k.client_ip
		WHERE TRIM(k.client_ip) <> ''
		GROUP BY k.client_ip, a.name, a.location, a.notes
		ORDER BY k.client_ip`)
	if err != nil {
		return err
	}
	var identities []legacyDeviceIdentity
	for rows.Next() {
		var identity legacyDeviceIdentity
		if err := rows.Scan(&identity.address, &identity.name, &identity.location, &identity.notes, &identity.firstSeen, &identity.lastSeen); err != nil {
			_ = rows.Close()
			return err
		}
		identities = append(identities, identity)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, identity := range identities {
		family := "ipv4"
		if strings.Contains(identity.address, ":") {
			family = "ipv6"
		}
		var deviceID int64
		err := tx.QueryRowContext(ctx, `SELECT device_id FROM device_addresses WHERE address = ?`, identity.address).Scan(&deviceID)
		if err == sql.ErrNoRows {
			result, insertErr := tx.ExecContext(ctx, `
				INSERT INTO devices(name, location, notes, confirmed, first_seen_at, last_seen_at)
				VALUES(?, ?, ?, CASE WHEN ? <> '' THEN 1 ELSE 0 END, COALESCE(?, CURRENT_TIMESTAMP), COALESCE(?, CURRENT_TIMESTAMP))`,
				identity.name, identity.location, identity.notes, identity.name, identity.firstSeen, identity.lastSeen)
			if insertErr != nil {
				return insertErr
			}
			deviceID, err = result.LastInsertId()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO device_addresses(device_id, address, family, source, confidence, first_seen_at, last_seen_at)
				VALUES(?, ?, ?, 'migration', 'observed', COALESCE(?, CURRENT_TIMESTAMP), COALESCE(?, CURRENT_TIMESTAMP))`,
				deviceID, identity.address, family, identity.firstSeen, identity.lastSeen); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE dns_queries
		SET device_id = (SELECT device_id FROM device_addresses WHERE address = dns_queries.client_ip)
		WHERE device_id IS NULL`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO device_protection_memberships(device_id, protection_id, created_at, updated_at)
		SELECT da.device_id, legacy.protection_id, legacy.created_at, legacy.updated_at
		FROM device_protection_assignments legacy
		JOIN device_addresses da ON da.address = legacy.client_ip
		ON CONFLICT(device_id) DO UPDATE SET
			protection_id = excluded.protection_id,
			updated_at = excluded.updated_at`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings(key, value, updated_at)
		VALUES('device_identity_migration_completed', 'true', CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) migrateProtection(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO protection_profiles(name, icon, is_default) VALUES('Home', 'house', 1)`); err != nil {
		return err
	}
	var completed string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'protection_migration_completed'`).Scan(&completed)
	if err == nil && completed == "true" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var homeID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM protection_profiles WHERE is_default = 1`).Scan(&homeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO protection_blocklists(protection_id, blocklist_id) SELECT ?, id FROM blocklists WHERE enabled = 1`, homeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO protection_allow_entries(protection_id, domain, created_at) SELECT ?, domain, created_at FROM allowlist_entries`, homeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO protection_block_entries(protection_id, domain, created_at) SELECT ?, domain, created_at FROM manual_block_entries`, homeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('protection_migration_completed', 'true') ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) seed(ctx context.Context) error {
	defaults := map[string]string{
		"upstream_dns": "1.1.1.1,9.9.9.9",
		// Keep upgrades on their existing transport. New onboarding explicitly
		// chooses encrypted DNS after the user selects supported providers.
		"upstream_transport":       "standard",
		"local_domain_suffix":      "home",
		"faro_lan_ip":              "",
		"retention_days":           "30",
		"favicon_fetching_enabled": "false",
		"dns_cache_enabled":        "true",
		"dns_cache_ttl":            "300",
		"allowed_client_cidrs":     "127.0.0.0/8,10.0.0.0/8,100.64.0.0/10,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7,fe80::/10",
	}
	for key, value := range defaults {
		if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key, value) VALUES(?, ?)`, key, value); err != nil {
			return err
		}
	}
	if err := s.removeLegacyDemoRecords(ctx); err != nil {
		return err
	}
	if err := s.removeLegacyDemoQueries(ctx); err != nil {
		return err
	}
	if err := s.removeLegacyDemoRules(ctx); err != nil {
		return err
	}

	if os.Getenv("FARO_SEED_DEMO_QUERIES") == "true" {
		return s.seedDemoQueries(ctx)
	}
	return nil
}

func (s *Store) removeLegacyDemoRecords(ctx context.Context) error {
	var marker string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'legacy_demo_records_removed'`).Scan(&marker)
	if err == nil && marker == "true" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM dns_records
		WHERE (hostname = 'router.home' AND type = 'A' AND value = '192.168.7.1' AND description = 'Home gateway')
		   OR (hostname = 'plex.home' AND type = 'A' AND value = '192.168.7.50' AND description = 'Media server')
		   OR (hostname = 'nas.home' AND type = 'A' AND value = '192.168.7.20' AND description = 'Storage')
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES('legacy_demo_records_removed', 'true', CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) removeLegacyDemoRules(ctx context.Context) error {
	var marker string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'legacy_demo_rules_removed'`).Scan(&marker)
	if err == nil && marker == "true" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM manual_block_entries WHERE domain IN ('ads.example.com', 'tracker.example.net')`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM blocklists WHERE name = 'Faro sample blocklist' AND url = 'file:///app/coredns/sample-blocklist.txt'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES('legacy_demo_rules_removed', 'true', CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) removeLegacyDemoQueries(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `
		DELETE FROM dns_queries
		WHERE domain IN ('plex.home', 'nas.home', 'ads.example.com', 'tracker.example.net', 'api.github.com')
		  AND client_ip IN ('192.168.7.44', '192.168.7.21', '192.168.7.36', '192.168.7.55', '127.0.0.1')
	`)
	return err
}

func (s *Store) seedDemoQueries(ctx context.Context) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM dns_queries`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	seed := []struct {
		client  string
		domain  string
		qtype   string
		action  string
		source  string
		latency float64
	}{
		{"192.168.7.44", "plex.home", "A", "allowed", "local", 1.2},
		{"192.168.7.21", "api.github.com", "A", "allowed", "upstream", 16.5},
		{"192.168.7.36", "ads.example.com", "A", "blocked", "blocklist", 0.8},
		{"192.168.7.44", "nas.home", "A", "allowed", "local", 1.1},
		{"192.168.7.55", "tracker.example.net", "AAAA", "blocked", "blocklist", 0.7},
	}
	for _, q := range seed {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, latency_ms) VALUES(datetime('now'), ?, ?, ?, ?, ?, ?)`,
			q.client, q.domain, q.qtype, q.action, q.source, q.latency); err != nil {
			return err
		}
	}
	return nil
}

func NormalizeDomain(domain string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(domain))
	normalized = strings.TrimSuffix(normalized, ".")
	if normalized == "" {
		return "", errors.New("domain is required")
	}
	if len(normalized) > 253 || strings.ContainsAny(normalized, " \t\r\n/") {
		return "", fmt.Errorf("invalid domain %q", domain)
	}
	for _, label := range strings.Split(normalized, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain %q", domain)
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", fmt.Errorf("invalid domain %q", domain)
			}
		}
	}
	return normalized, nil
}

func NormalizeRecord(hostname, typ, value string) (string, string, string, error) {
	host, err := NormalizeDomain(hostname)
	if err != nil {
		return "", "", "", err
	}
	recordType := strings.ToUpper(strings.TrimSpace(typ))
	if recordType == "" {
		recordType = "A"
	}
	switch recordType {
	case "A", "AAAA":
	default:
		return "", "", "", fmt.Errorf("unsupported record type %q", recordType)
	}
	recordValue := strings.TrimSpace(value)
	if recordValue == "" {
		return "", "", "", errors.New("record value is required")
	}
	parsedIP := net.ParseIP(recordValue)
	if parsedIP == nil {
		return "", "", "", errors.New("A and AAAA records require an IP address")
	}
	if recordType == "A" && parsedIP.To4() == nil {
		return "", "", "", errors.New("A records require an IPv4 address")
	}
	if recordType == "AAAA" && parsedIP.To4() != nil {
		return "", "", "", errors.New("AAAA records require an IPv6 address")
	}
	return host, recordType, parsedIP.String(), nil
}

// migrateDNSRecords replaces the original hostname-only uniqueness constraint
// with record-level uniqueness so a hostname can be genuinely dual-stack.
func (s *Store) migrateDNSRecords(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA index_list(dns_records)`)
	if err != nil {
		return err
	}
	var uniqueIndexes []string
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return err
		}
		if unique == 1 {
			uniqueIndexes = append(uniqueIndexes, name)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	hasHostnameOnlyUnique := false
	for _, name := range uniqueIndexes {
		columns, columnErr := s.DB.QueryContext(ctx, `PRAGMA index_info(`+quoteIdentifier(name)+`)`)
		if columnErr != nil {
			return columnErr
		}
		var names []string
		for columns.Next() {
			var rank, cid int
			var columnName string
			if err := columns.Scan(&rank, &cid, &columnName); err != nil {
				_ = columns.Close()
				return err
			}
			names = append(names, columnName)
		}
		_ = columns.Close()
		if len(names) == 1 && names[0] == "hostname" {
			hasHostnameOnlyUnique = true
		}
	}
	if !hasHostnameOnlyUnique {
		return nil
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE dns_records_migrated (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hostname TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'A',
			value TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(hostname, type, value)
		)`,
		`INSERT INTO dns_records_migrated(id, hostname, type, value, description, created_at, updated_at)
		 SELECT id, hostname, type, value, description, created_at, updated_at FROM dns_records`,
		`DROP TABLE dns_records`,
		`ALTER TABLE dns_records_migrated RENAME TO dns_records`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

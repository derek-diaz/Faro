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
			hostname TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL DEFAULT 'A',
			value TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
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
	if _, err := s.DB.ExecContext(ctx, `ALTER TABLE dns_queries ADD COLUMN upstream TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	for _, column := range []string{
		`ALTER TABLE dns_queries ADD COLUMN rcode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE dns_queries ADD COLUMN decision_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE dns_queries ADD COLUMN decision_metadata TEXT NOT NULL DEFAULT '{}'`,
	} {
		if _, err := s.DB.ExecContext(ctx, column); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	return nil
}

func (s *Store) seed(ctx context.Context) error {
	defaults := map[string]string{
		"upstream_dns":             "1.1.1.1,9.9.9.9",
		"local_domain_suffix":      "home",
		"faro_lan_ip":              "",
		"retention_days":           "30",
		"favicon_fetching_enabled": "false",
		"dns_cache_enabled":        "true",
		"dns_cache_ttl":            "300",
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
	if strings.ContainsAny(normalized, " \t\r\n/") {
		return "", fmt.Errorf("invalid domain %q", domain)
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
	if net.ParseIP(recordValue) == nil {
		return "", "", "", errors.New("A and AAAA records require an IP address")
	}
	return host, recordType, recordValue, nil
}

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
			latency_ms REAL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_dns_queries_timestamp ON dns_queries(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_dns_queries_domain ON dns_queries(domain);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS domain_favicons (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL UNIQUE,
			favicon_url TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			last_checked_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
	}
	for _, stmt := range schema {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) seed(ctx context.Context) error {
	defaults := map[string]string{
		"upstream_dns":             "1.1.1.1,9.9.9.9",
		"local_domain_suffix":      "home",
		"retention_days":           "30",
		"favicon_fetching_enabled": "false",
	}
	for key, value := range defaults {
		if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key, value) VALUES(?, ?)`, key, value); err != nil {
			return err
		}
	}
	if err := s.removeLegacyDemoQueries(ctx); err != nil {
		return err
	}

	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM dns_records`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		records := []struct {
			host, typ, value, description string
		}{
			{"router.home", "A", "192.168.7.1", "Home gateway"},
			{"plex.home", "A", "192.168.7.50", "Media server"},
			{"nas.home", "A", "192.168.7.20", "Storage"},
		}
		for _, record := range records {
			if _, err := s.DB.ExecContext(ctx, `INSERT INTO dns_records(hostname, type, value, description) VALUES(?, ?, ?, ?)`, record.host, record.typ, record.value, record.description); err != nil {
				return err
			}
		}
	}

	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_block_entries`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		for _, domain := range []string{"ads.example.com", "tracker.example.net"} {
			if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO manual_block_entries(domain) VALUES(?)`, domain); err != nil {
				return err
			}
		}
	}

	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocklists`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		result, err := s.DB.ExecContext(ctx, `INSERT INTO blocklists(name, url, enabled, last_refreshed_at) VALUES(?, ?, 1, CURRENT_TIMESTAMP)`, "Faro sample blocklist", "file:///app/coredns/sample-blocklist.txt")
		if err != nil {
			return err
		}
		id, _ := result.LastInsertId()
		for _, domain := range []string{"ads.example.com", "badtelemetry.example.org", "tracker.example.net"} {
			if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO blocklist_entries(blocklist_id, domain) VALUES(?, ?)`, id, domain); err != nil {
				return err
			}
		}
	}

	if os.Getenv("FARO_SEED_DEMO_QUERIES") == "true" {
		return s.seedDemoQueries(ctx)
	}
	return nil
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
	case "A", "AAAA", "CNAME":
	default:
		return "", "", "", fmt.Errorf("unsupported record type %q", recordType)
	}
	recordValue := strings.TrimSpace(value)
	if recordValue == "" {
		return "", "", "", errors.New("record value is required")
	}
	if recordType == "A" || recordType == "AAAA" {
		if net.ParseIP(recordValue) == nil {
			return "", "", "", errors.New("A and AAAA records require an IP address")
		}
	}
	if recordType == "CNAME" {
		if _, err := NormalizeDomain(recordValue); err != nil {
			return "", "", "", errors.New("CNAME records require a valid target hostname")
		}
	}
	return host, recordType, recordValue, nil
}

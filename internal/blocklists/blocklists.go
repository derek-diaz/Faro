package blocklists

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/derek/faro/internal/db"
)

const (
	maxDownloadBytes       = 64 << 20
	defaultRefreshInterval = 6 * time.Hour
	defaultRefreshAge      = 24 * time.Hour
	defaultStartupDelay    = 15 * time.Second
	defaultRetryInterval   = 5 * time.Minute
)

type Refresher struct {
	Store        *db.Store
	DNSUpstreams []string
}

type listSnapshot struct {
	domains         []string
	lastRefreshedAt sql.NullString
	updatedAt       string
}

func (refresher Refresher) RefreshAndApply(ctx context.Context, id int64, apply func(context.Context) error) (int, error) {
	snapshot, err := refresher.snapshot(ctx, id)
	if err != nil {
		return 0, err
	}
	count, err := refresher.Refresh(ctx, id)
	if err != nil {
		return 0, err
	}
	if apply == nil {
		return count, nil
	}
	if err := apply(ctx); err != nil {
		rollbackCtx := context.WithoutCancel(ctx)
		if restoreErr := refresher.restore(rollbackCtx, id, snapshot); restoreErr != nil {
			return 0, fmt.Errorf("apply refreshed blocklist: %w; restore previous entries: %v", err, restoreErr)
		}
		_ = apply(rollbackCtx)
		return 0, fmt.Errorf("apply refreshed blocklist: %w; previous entries were restored", err)
	}
	return count, nil
}

func (refresher Refresher) Refresh(ctx context.Context, id int64) (int, error) {
	var source string
	if err := refresher.Store.DB.QueryRowContext(ctx, `SELECT url FROM blocklists WHERE id = ?`, id).Scan(&source); err != nil {
		return 0, err
	}

	body, err := refresher.openSource(ctx, source)
	if err != nil {
		return 0, err
	}
	defer func() { _ = body.Close() }()

	limited := &io.LimitedReader{R: body, N: maxDownloadBytes + 1}
	domains, err := Parse(limited)
	if err != nil {
		return 0, err
	}
	if limited.N <= 0 {
		return 0, fmt.Errorf("blocklist download exceeds %d MiB", maxDownloadBytes>>20)
	}
	if len(domains) == 0 {
		return 0, errors.New("blocklist contained no valid domains; keeping the last known good version")
	}

	tx, err := refresher.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM blocklist_entries WHERE blocklist_id = ?`, id); err != nil {
		return 0, err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO blocklist_entries(blocklist_id, domain) VALUES(?, ?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = insert.Close() }()
	for _, domain := range domains {
		if _, err := insert.ExecContext(ctx, id, domain); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE blocklists SET last_refreshed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(domains), nil
}

func (refresher Refresher) snapshot(ctx context.Context, id int64) (listSnapshot, error) {
	var snapshot listSnapshot
	if err := refresher.Store.DB.QueryRowContext(ctx, `SELECT last_refreshed_at, updated_at FROM blocklists WHERE id = ?`, id).Scan(&snapshot.lastRefreshedAt, &snapshot.updatedAt); err != nil {
		return snapshot, err
	}
	rows, err := refresher.Store.DB.QueryContext(ctx, `SELECT domain FROM blocklist_entries WHERE blocklist_id = ? ORDER BY domain`, id)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return snapshot, err
		}
		snapshot.domains = append(snapshot.domains, domain)
	}
	return snapshot, rows.Err()
}

func (refresher Refresher) restore(ctx context.Context, id int64, snapshot listSnapshot) error {
	tx, err := refresher.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM blocklist_entries WHERE blocklist_id = ?`, id); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO blocklist_entries(blocklist_id, domain) VALUES(?, ?)`)
	if err != nil {
		return err
	}
	for _, domain := range snapshot.domains {
		if _, err := insert.ExecContext(ctx, id, domain); err != nil {
			_ = insert.Close()
			return err
		}
	}
	if err := insert.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE blocklists SET last_refreshed_at = ?, updated_at = ? WHERE id = ?`, snapshot.lastRefreshedAt, snapshot.updatedAt, id); err != nil {
		return err
	}
	return tx.Commit()
}

type Manager struct {
	Store         *db.Store
	Refresher     Refresher
	Apply         func(context.Context) error
	Interval      time.Duration
	RefreshAge    time.Duration
	StartupDelay  time.Duration
	RetryInterval time.Duration
}

func NewManager(store *db.Store, apply func(context.Context) error) *Manager {
	return &Manager{
		Store:         store,
		Refresher:     Refresher{Store: store},
		Apply:         apply,
		Interval:      defaultRefreshInterval,
		RefreshAge:    defaultRefreshAge,
		StartupDelay:  defaultStartupDelay,
		RetryInterval: defaultRetryInterval,
	}
}

func (manager *Manager) Run(ctx context.Context) {
	interval := manager.Interval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	startupDelay := manager.StartupDelay
	if startupDelay == 0 {
		startupDelay = defaultStartupDelay
	} else if startupDelay < 0 {
		startupDelay = 0
	}
	retryInterval := manager.RetryInterval
	if retryInterval <= 0 {
		retryInterval = defaultRetryInterval
	}
	timer := time.NewTimer(startupDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			next := interval
			if manager.refreshDue(ctx) {
				next = retryInterval
				log.Printf("automatic blocklist refresh will retry in %s", retryInterval)
			}
			timer.Reset(next)
		}
	}
}

func (manager *Manager) refreshDue(ctx context.Context) (failed bool) {
	age := manager.RefreshAge
	if age <= 0 {
		age = defaultRefreshAge
	}
	cutoff := time.Now().UTC().Add(-age).Format(time.RFC3339)
	rows, err := manager.Store.DB.QueryContext(ctx, `
		SELECT id FROM blocklists
		WHERE enabled = 1 AND (last_refreshed_at IS NULL OR datetime(last_refreshed_at) < datetime(?))
		ORDER BY id
	`, cutoff)
	if err != nil {
		log.Printf("automatic blocklist scan failed: %v", err)
		return true
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("automatic blocklist scan failed: %v", err)
		failed = true
	}
	_ = rows.Close()

	for _, id := range ids {
		if _, err := manager.Refresher.RefreshAndApply(ctx, id, manager.Apply); err != nil {
			log.Printf("automatic blocklist refresh %d failed: %v", id, err)
			failed = true
		}
	}
	return failed
}

func Parse(reader io.Reader) ([]string, error) {
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if domain, ok := parseBlocklistLine(scanner.Text()); ok {
			seen[domain] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	domains := make([]string, 0, len(seen))
	for domain := range seen {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains, nil
}

func parseBlocklistLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
		return "", false
	}
	if strings.Contains(line, "##") || strings.Contains(line, "#@#") || strings.Contains(line, "#$#") || strings.Contains(line, "#?#") {
		return "", false
	}
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false
	}

	candidate := fields[0]
	switch {
	case len(fields) > 1 && (candidate == "0.0.0.0" || candidate == "127.0.0.1" || candidate == "::1"):
		candidate = fields[1]
	case strings.HasPrefix(candidate, "@@"):
		// Allow rules are not blocklist entries.
		return "", false
	case strings.HasPrefix(candidate, "||"):
		// Adblock-style DNS rules may include an anchor and options, for
		// example ||example.com^$important. DNS can enforce the hostname,
		// while browser-only path, cosmetic, and scriptlet rules are ignored.
		candidate, _ = strings.CutPrefix(candidate, "||")
		end := strings.Index(candidate, "^")
		if end <= 0 || strings.Contains(candidate[:end], "/") {
			return "", false
		}
		candidate = candidate[:end]
	case strings.ContainsAny(candidate, "|*$=~[](){}"):
		return "", false
	}
	candidate = strings.TrimPrefix(candidate, ".")
	domain, err := db.NormalizeDomain(candidate)
	return domain, err == nil
}

func (refresher Refresher) openSource(ctx context.Context, source string) (io.ReadCloser, error) {
	if filePath, ok := strings.CutPrefix(source, "file://"); ok {
		return os.Open(filePath)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	// Resolve downloads directly through Faro's configured public DNS upstreams.
	// Docker's host resolver may point back to Faro and cannot answer while the
	// DNS container is still coming up during installation or upgrade.
	resolver := newBlocklistResolver(refresher.blocklistDNSUpstreams(ctx))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = blocklistDialContext(resolver)
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	return resp.Body, nil
}

func (refresher Refresher) blocklistDNSUpstreams(ctx context.Context) []string {
	if len(refresher.DNSUpstreams) > 0 {
		return refresher.DNSUpstreams
	}
	var configured, faroIP string
	_ = refresher.Store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&configured)
	_ = refresher.Store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'faro_lan_ip'`).Scan(&faroIP)
	faroAddress := net.ParseIP(strings.TrimSpace(faroIP))
	upstreams := make([]string, 0, 4)
	for _, raw := range strings.Split(configured, ",") {
		address := strings.TrimSpace(raw)
		parsedAddress := net.ParseIP(address)
		if parsedAddress == nil || faroAddress != nil && parsedAddress.Equal(faroAddress) {
			continue
		}
		upstreams = append(upstreams, net.JoinHostPort(address, "53"))
	}
	if len(upstreams) == 0 {
		return []string{"1.1.1.1:53", "9.9.9.9:53"}
	}
	return upstreams
}

func newBlocklistResolver(upstreams []string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var lastErr error
			for _, upstream := range upstreams {
				connection, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, upstream)
				if err == nil {
					return connection, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = errors.New("no DNS upstream is configured for blocklist downloads")
			}
			return nil, lastErr
		},
	}
}

func blocklistDialContext(resolver *net.Resolver) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || net.ParseIP(strings.Trim(host, "[]")) != nil {
			return dialer.DialContext(ctx, network, address)
		}
		addresses, lookupErr := resolver.LookupNetIP(ctx, "ip", strings.TrimSuffix(host, ".")+".")
		if lookupErr != nil {
			// Custom sources may use a local hostname known only to Faro or the
			// host resolver. Preserve that path when public upstreams return NXDOMAIN.
			return dialer.DialContext(ctx, network, address)
		}
		var lastErr error
		for _, candidate := range addresses {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("blocklist domain %s did not resolve", host)
		}
		return nil, lastErr
	}
}

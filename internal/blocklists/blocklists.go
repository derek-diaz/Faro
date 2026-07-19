package blocklists

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
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
)

type Refresher struct {
	Store *db.Store
}

type listSnapshot struct {
	domains         []string
	lastRefreshedAt sql.NullString
	updatedAt       string
}

func (r Refresher) RefreshAndApply(ctx context.Context, id int64, apply func(context.Context) error) (int, error) {
	snapshot, err := r.snapshot(ctx, id)
	if err != nil {
		return 0, err
	}
	count, err := r.Refresh(ctx, id)
	if err != nil {
		return 0, err
	}
	if apply == nil {
		return count, nil
	}
	if err := apply(ctx); err != nil {
		rollbackCtx := context.WithoutCancel(ctx)
		if restoreErr := r.restore(rollbackCtx, id, snapshot); restoreErr != nil {
			return 0, fmt.Errorf("apply refreshed blocklist: %w; restore previous entries: %v", err, restoreErr)
		}
		_ = apply(rollbackCtx)
		return 0, fmt.Errorf("apply refreshed blocklist: %w; previous entries were restored", err)
	}
	return count, nil
}

func (r Refresher) Refresh(ctx context.Context, id int64) (int, error) {
	var source string
	if err := r.Store.DB.QueryRowContext(ctx, `SELECT url FROM blocklists WHERE id = ?`, id).Scan(&source); err != nil {
		return 0, err
	}

	body, err := openSource(ctx, source)
	if err != nil {
		return 0, err
	}
	defer body.Close()

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

	tx, err := r.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM blocklist_entries WHERE blocklist_id = ?`, id); err != nil {
		return 0, err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO blocklist_entries(blocklist_id, domain) VALUES(?, ?)`)
	if err != nil {
		return 0, err
	}
	defer insert.Close()
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

func (r Refresher) snapshot(ctx context.Context, id int64) (listSnapshot, error) {
	var snapshot listSnapshot
	if err := r.Store.DB.QueryRowContext(ctx, `SELECT last_refreshed_at, updated_at FROM blocklists WHERE id = ?`, id).Scan(&snapshot.lastRefreshedAt, &snapshot.updatedAt); err != nil {
		return snapshot, err
	}
	rows, err := r.Store.DB.QueryContext(ctx, `SELECT domain FROM blocklist_entries WHERE blocklist_id = ? ORDER BY domain`, id)
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return snapshot, err
		}
		snapshot.domains = append(snapshot.domains, domain)
	}
	return snapshot, rows.Err()
}

func (r Refresher) restore(ctx context.Context, id int64, snapshot listSnapshot) error {
	tx, err := r.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	Store      *db.Store
	Refresher  Refresher
	Apply      func(context.Context) error
	Interval   time.Duration
	RefreshAge time.Duration
}

func NewManager(store *db.Store, apply func(context.Context) error) *Manager {
	return &Manager{Store: store, Refresher: Refresher{Store: store}, Apply: apply, Interval: defaultRefreshInterval, RefreshAge: defaultRefreshAge}
}

func (m *Manager) Run(ctx context.Context) {
	m.refreshDue(ctx)
	interval := m.Interval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.refreshDue(ctx)
		}
	}
}

func (m *Manager) refreshDue(ctx context.Context) {
	age := m.RefreshAge
	if age <= 0 {
		age = defaultRefreshAge
	}
	cutoff := time.Now().UTC().Add(-age).Format(time.RFC3339)
	rows, err := m.Store.DB.QueryContext(ctx, `
		SELECT id FROM blocklists
		WHERE enabled = 1 AND (last_refreshed_at IS NULL OR datetime(last_refreshed_at) < datetime(?))
		ORDER BY id
	`, cutoff)
	if err != nil {
		log.Printf("automatic blocklist scan failed: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()

	for _, id := range ids {
		if _, err := m.Refresher.RefreshAndApply(ctx, id, m.Apply); err != nil {
			log.Printf("automatic blocklist refresh %d failed: %v", id, err)
		}
	}
}

func Parse(reader io.Reader) ([]string, error) {
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		if strings.Contains(line, "##") || strings.Contains(line, "#@#") || strings.Contains(line, "#$#") || strings.Contains(line, "#?#") {
			continue
		}
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		candidate := fields[0]
		if len(fields) > 1 && (candidate == "0.0.0.0" || candidate == "127.0.0.1" || candidate == "::1") {
			candidate = fields[1]
		} else if strings.HasPrefix(candidate, "@@") {
			// Allow rules are not blocklist entries.
			continue
		} else if strings.HasPrefix(candidate, "||") {
			// Adblock-style DNS rules may include an anchor and options, for
			// example ||example.com^$important. DNS can enforce the hostname,
			// while browser-only path, cosmetic, and scriptlet rules are ignored.
			candidate = strings.TrimPrefix(candidate, "||")
			end := strings.Index(candidate, "^")
			if end <= 0 || strings.Contains(candidate[:end], "/") {
				continue
			}
			candidate = candidate[:end]
		} else if strings.ContainsAny(candidate, "|*$=~[](){}") {
			continue
		}
		candidate = strings.TrimPrefix(candidate, ".")

		domain, err := db.NormalizeDomain(candidate)
		if err != nil {
			continue
		}
		seen[domain] = struct{}{}
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

func openSource(ctx context.Context, source string) (io.ReadCloser, error) {
	if strings.HasPrefix(source, "file://") {
		return os.Open(strings.TrimPrefix(source, "file://"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
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

package blocklists

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/derek/faro/internal/db"
)

type Refresher struct {
	Store *db.Store
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

	domains, err := Parse(body)
	if err != nil {
		return 0, err
	}

	tx, err := r.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM blocklist_entries WHERE blocklist_id = ?`, id); err != nil {
		return 0, err
	}
	for _, domain := range domains {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blocklist_entries(blocklist_id, domain) VALUES(?, ?)`, id, domain); err != nil {
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

func Parse(reader io.Reader) ([]string, error) {
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
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
		}
		candidate = strings.TrimPrefix(candidate, "||")
		candidate = strings.TrimPrefix(candidate, ".")
		candidate = strings.TrimSuffix(candidate, "^")

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

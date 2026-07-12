package handlers

import (
	"context"
	"errors"
	"fmt"
	"github.com/derek/faro/internal/db"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func (s *Handler) favicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if settingValue(r.Context(), s.store.DB, "favicon_fetching_enabled") != "true" {
		http.NotFound(w, r)
		return
	}
	domain, err := db.NormalizeDomain(strings.TrimPrefix(r.URL.Path, "/api/favicons/"))
	if err != nil || !isSafeFaviconDomain(domain) {
		http.NotFound(w, r)
		return
	}

	localPath, err := s.cachedFaviconPath(r.Context(), domain)
	if err == nil && localPath != "" {
		http.ServeFile(w, r, localPath)
		return
	}

	localPath, err = s.fetchFavicon(r.Context(), domain)
	if err != nil {
		serveFaviconPlaceholder(w, domain)
		return
	}
	http.ServeFile(w, r, localPath)
}

func (s *Handler) cachedFaviconPath(ctx context.Context, domain string) (string, error) {
	var localPath string
	err := s.store.DB.QueryRowContext(ctx, `SELECT local_path FROM domain_favicons WHERE domain = ? AND local_path != ''`, domain).Scan(&localPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(localPath); err != nil {
		return "", err
	}
	return localPath, nil
}

func (s *Handler) fetchFavicon(ctx context.Context, domain string) (string, error) {
	if err := os.MkdirAll(s.faviconDir, 0o755); err != nil {
		return "", err
	}
	candidates := []string{
		"https://" + domain + "/favicon.ico",
		"https://www." + domain + "/favicon.ico",
	}
	client := http.Client{Timeout: 5 * time.Second}
	for _, candidate := range candidates {
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, candidate, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("User-Agent", "Faro favicon fetcher")
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		contentType := resp.Header.Get("Content-Type")
		if resp.StatusCode < 200 || resp.StatusCode > 299 || !strings.HasPrefix(contentType, "image/") {
			_ = resp.Body.Close()
			cancel()
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		_ = resp.Body.Close()
		cancel()
		if err != nil || len(body) == 0 {
			continue
		}
		localPath := filepath.Join(s.faviconDir, safeFaviconFilename(domain))
		if err := os.WriteFile(localPath, body, 0o644); err != nil {
			return "", err
		}
		if _, err := s.store.DB.ExecContext(ctx, `
			INSERT INTO domain_favicons(domain, favicon_url, local_path, last_checked_at, updated_at)
			VALUES(?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			ON CONFLICT(domain) DO UPDATE SET
				favicon_url = excluded.favicon_url,
				local_path = excluded.local_path,
				last_checked_at = CURRENT_TIMESTAMP,
				updated_at = CURRENT_TIMESTAMP
		`, domain, candidate, localPath); err != nil {
			return "", err
		}
		return localPath, nil
	}
	_, _ = s.store.DB.ExecContext(ctx, `
		INSERT INTO domain_favicons(domain, favicon_url, local_path, last_checked_at, updated_at)
		VALUES(?, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(domain) DO UPDATE SET last_checked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
	`, domain)
	return "", errors.New("favicon not found")
}

var publicDomainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$`)

func isSafeFaviconDomain(domain string) bool {
	if !publicDomainPattern.MatchString(domain) {
		return false
	}
	if strings.HasSuffix(domain, ".home") || strings.HasSuffix(domain, ".local") || strings.HasSuffix(domain, ".lan") {
		return false
	}
	parsed, err := url.Parse("https://" + domain)
	return err == nil && parsed.Hostname() == domain
}

func safeFaviconFilename(domain string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(domain) + ".ico"
}

func serveFaviconPlaceholder(w http.ResponseWriter, domain string) {
	initial := "?"
	if domain != "" {
		initial = strings.ToUpper(domain[:1])
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#e8eef5"/><text x="16" y="21" text-anchor="middle" font-family="Arial, sans-serif" font-size="14" font-weight="700" fill="#617085">%s</text></svg>`, initial)
}

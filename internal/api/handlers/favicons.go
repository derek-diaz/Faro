package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/derek/faro/internal/db"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

const (
	faviconFailureCacheWindow = "-15 minutes"
	maxFaviconBytes           = 512 * 1024
	maxFaviconPageBytes       = 512 * 1024
)

var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

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

	localPath, cached, err := s.cachedFavicon(r.Context(), domain)
	if err == nil && cached {
		serveCachedFavicon(w, r, domain, localPath)
		return
	}

	lock := s.faviconLock(domain)
	lock.Lock()
	defer lock.Unlock()

	// Multiple rows can request the same icon at once. Recheck after taking the
	// per-domain shard lock so only one request performs network I/O.
	localPath, cached, err = s.cachedFavicon(r.Context(), domain)
	if err == nil && cached {
		serveCachedFavicon(w, r, domain, localPath)
		return
	}

	localPath, err = s.fetchFavicon(r.Context(), domain)
	if err != nil {
		serveFaviconPlaceholder(w, domain)
		return
	}
	serveFaviconFile(w, r, localPath, "fetched")
}

func (s *Handler) cachedFavicon(ctx context.Context, domain string) (string, bool, error) {
	var localPath string
	var recentlyChecked int
	err := s.store.DB.QueryRowContext(ctx, `
		SELECT local_path, COALESCE(last_checked_at >= datetime('now', ?), 0)
		FROM domain_favicons
		WHERE domain = ?
	`, faviconFailureCacheWindow, domain).Scan(&localPath, &recentlyChecked)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if localPath == "" {
		return "", recentlyChecked == 1, nil
	}
	if _, err := os.Stat(localPath); err != nil {
		return "", false, nil
	}
	return localPath, true, nil
}

func (s *Handler) fetchFavicon(ctx context.Context, domain string) (string, error) {
	if err := os.MkdirAll(s.faviconDir, 0o755); err != nil {
		return "", err
	}
	fetchCtx, cancelFetch := context.WithTimeout(ctx, 12*time.Second)
	defer cancelFetch()
	client := s.faviconHTTPClient(ctx)
	candidates, pages := faviconCandidates(domain)
	for _, candidate := range candidates {
		if localPath, err := s.downloadAndCacheFavicon(fetchCtx, &client, domain, candidate); err == nil {
			return localPath, nil
		}
	}
	for _, page := range pages {
		for _, candidate := range discoverFaviconCandidates(fetchCtx, &client, page) {
			if localPath, err := s.downloadAndCacheFavicon(fetchCtx, &client, domain, candidate); err == nil {
				return localPath, nil
			}
		}
	}
	_, _ = s.store.DB.ExecContext(ctx, `
		INSERT INTO domain_favicons(domain, favicon_url, local_path, last_checked_at, updated_at)
		VALUES(?, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(domain) DO UPDATE SET last_checked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
	`, domain)
	return "", errors.New("favicon not found")
}

func (s *Handler) downloadAndCacheFavicon(ctx context.Context, client *http.Client, domain, candidate string) (string, error) {
	body, resolvedURL, err := downloadFavicon(ctx, client, candidate)
	if err != nil {
		return "", err
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
	`, domain, resolvedURL, localPath); err != nil {
		_ = os.Remove(localPath)
		return "", err
	}
	return localPath, nil
}

func faviconCandidates(domain string) ([]string, []string) {
	hosts := []string{domain}
	if registrable, err := publicsuffix.EffectiveTLDPlusOne(domain); err == nil && registrable != domain {
		hosts = append(hosts, registrable)
	}
	direct := make([]string, 0, len(hosts)+1)
	for _, host := range hosts {
		direct = append(direct, "https://"+host+"/favicon.ico")
	}
	registrable := hosts[len(hosts)-1]
	if !strings.HasPrefix(registrable, "www.") {
		direct = append(direct, "https://www."+registrable+"/favicon.ico")
	}
	pages := []string{"https://" + registrable + "/"}
	if domain != registrable {
		pages = append(pages, "https://"+domain+"/")
	}
	if !strings.HasPrefix(registrable, "www.") {
		pages = append(pages, "https://www."+registrable+"/")
	}
	return uniqueStringsInOrder(direct), uniqueStringsInOrder(pages)
}

func downloadFavicon(ctx context.Context, client *http.Client, candidate string) ([]byte, string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, candidate, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.9,*/*;q=0.1")
	request.Header.Set("User-Agent", "Faro favicon fetcher")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		return nil, "", fmt.Errorf("favicon returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFaviconBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 || len(body) > maxFaviconBytes {
		return nil, "", errors.New("favicon is empty or too large")
	}
	if !isFaviconImage(response.Header.Get("Content-Type"), body) {
		return nil, "", errors.New("favicon response is not a supported image")
	}
	resolvedURL := candidate
	if response.Request != nil && response.Request.URL != nil {
		resolvedURL = response.Request.URL.String()
	}
	return body, resolvedURL, nil
}

func discoverFaviconCandidates(ctx context.Context, client *http.Client, page string) []string {
	requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, page, nil)
	if err != nil {
		return nil
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")
	request.Header.Set("User-Agent", "Faro favicon fetcher")
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		return nil
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentType != "text/html" && contentType != "application/xhtml+xml" {
		return nil
	}
	// The icon declarations live in the document head. Parse a bounded prefix
	// instead of rejecting otherwise valid sites with large home pages.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFaviconPageBytes))
	if err != nil || len(body) == 0 {
		return nil
	}
	baseURL := request.URL
	if response.Request != nil && response.Request.URL != nil {
		baseURL = response.Request.URL
	}
	return faviconLinks(baseURL, body)
}

func faviconLinks(baseURL *url.URL, body []byte) []string {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	candidates := make([]string, 0, 4)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "link" {
			var relation, href string
			for _, attribute := range node.Attr {
				switch strings.ToLower(attribute.Key) {
				case "rel":
					relation = strings.ToLower(attribute.Val)
				case "href":
					href = strings.TrimSpace(attribute.Val)
				}
			}
			if href != "" && slices.ContainsFunc(strings.Fields(relation), func(value string) bool {
				return value == "icon" || value == "shortcut" || value == "apple-touch-icon" || value == "mask-icon"
			}) {
				if reference, parseErr := url.Parse(href); parseErr == nil {
					resolved := baseURL.ResolveReference(reference)
					if validDiscoveredFaviconURL(resolved) {
						candidates = append(candidates, resolved.String())
					}
				}
			}
		}
		if len(candidates) >= 8 {
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
			if len(candidates) >= 8 {
				return
			}
		}
	}
	walk(document)
	return uniqueStringsInOrder(candidates)
}

func validDiscoveredFaviconURL(candidate *url.URL) bool {
	if candidate == nil || !strings.EqualFold(candidate.Scheme, "https") || candidate.User != nil {
		return false
	}
	if candidate.Port() != "" && candidate.Port() != "443" {
		return false
	}
	return isSafeFaviconDomain(strings.ToLower(candidate.Hostname()))
}

func isFaviconImage(contentType string, body []byte) bool {
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(body))
	if strings.HasPrefix(detected, "image/") {
		return true
	}
	declared, _, _ := mime.ParseMediaType(contentType)
	return declared == "image/svg+xml" && looksLikeSVG(body)
}

func looksLikeSVG(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 512 {
		trimmed = trimmed[:512]
	}
	lower := bytes.ToLower(trimmed)
	return bytes.HasPrefix(lower, []byte("<svg")) ||
		bytes.HasPrefix(lower, []byte("<?xml")) && bytes.Contains(lower, []byte("<svg"))
}

func uniqueStringsInOrder(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func serveCachedFavicon(w http.ResponseWriter, r *http.Request, domain, localPath string) {
	if localPath == "" {
		serveFaviconPlaceholder(w, domain)
		return
	}
	serveFaviconFile(w, r, localPath, "cache")
}

func serveFaviconFile(w http.ResponseWriter, r *http.Request, localPath, source string) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Faro-Favicon", source)
	http.ServeFile(w, r, localPath)
}

func (s *Handler) faviconLock(domain string) *sync.Mutex {
	var hash uint32 = 2166136261
	for index := range domain {
		hash ^= uint32(domain[index])
		hash *= 16777619
	}
	return &s.faviconLocks[hash%uint32(len(s.faviconLocks))]
}

// faviconHTTPClient keeps Faro's own favicon lookups out of the monitored DNS
// path. Resolving them through the host resolver would create a query-log row,
// which renders another favicon and can recursively generate www/search labels.
func (s *Handler) faviconHTTPClient(ctx context.Context) http.Client {
	resolver := newUpstreamResolver(s.faviconDNSUpstreams(ctx))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = faviconDialContext(resolver)
	return http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func (s *Handler) faviconDNSUpstreams(ctx context.Context) []string {
	configured := strings.Split(settingValue(ctx, s.store.DB, "upstream_dns"), ",")
	faroIP := strings.TrimSpace(settingValue(ctx, s.store.DB, "faro_lan_ip"))
	upstreams := make([]string, 0, len(configured))
	for _, raw := range configured {
		address := strings.TrimSpace(raw)
		if net.ParseIP(address) == nil || address == faroIP {
			continue
		}
		upstreams = append(upstreams, net.JoinHostPort(address, "53"))
	}
	if len(upstreams) == 0 {
		return []string{"1.1.1.1:53", "9.9.9.9:53"}
	}
	return upstreams
}

func newUpstreamResolver(upstreams []string) *net.Resolver {
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
				lastErr = errors.New("no DNS upstream is configured for favicon fetching")
			}
			return nil, lastErr
		},
	}
}

func faviconDialContext(resolver *net.Resolver) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if parsed, parseErr := netip.ParseAddr(strings.Trim(host, "[]")); parseErr == nil {
			if !isPublicFaviconIP(parsed) {
				return nil, fmt.Errorf("favicon address %s is not public", parsed)
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(parsed.String(), port))
		}

		// The trailing dot makes the lookup absolute, preventing host/Docker DNS
		// search suffixes from being appended to public domain names.
		addresses, err := resolver.LookupNetIP(ctx, "ip", strings.TrimSuffix(host, ".")+".")
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, candidate := range addresses {
			if !isPublicFaviconIP(candidate) {
				continue
			}
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("favicon domain %s did not resolve to a public address", host)
		}
		return nil, lastErr
	}
}

func isPublicFaviconIP(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsPrivate() && !sharedAddressSpace.Contains(address)
}

var publicDomainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$`)

func isSafeFaviconDomain(domain string) bool {
	if !publicDomainPattern.MatchString(domain) {
		return false
	}
	if len(domain) > 253 {
		return false
	}
	repeatedLabels := 1
	labels := strings.Split(domain, ".")
	for index, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if index > 0 && label == labels[index-1] {
			repeatedLabels++
			if repeatedLabels >= 3 {
				return false
			}
		} else {
			repeatedLabels = 1
		}
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
	w.Header().Set("X-Faro-Favicon", "placeholder")
	_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#e8eef5"/><text x="16" y="21" text-anchor="middle" font-family="Arial, sans-serif" font-size="14" font-weight="700" fill="#617085">%s</text></svg>`, initial)
}

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
	maxDiscoveredFaviconLinks = 8
	contentTypeHeader         = "Content-Type"
	httpsScheme               = "https://"
)

var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

func (handler *Handler) favicon(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	if settingValue(request.Context(), handler.store.DB, "favicon_fetching_enabled") != "true" {
		http.NotFound(responseWriter, request)
		return
	}
	domain, err := db.NormalizeDomain(strings.TrimPrefix(request.URL.Path, "/api/favicons/"))
	if err != nil || !isSafeFaviconDomain(domain) {
		http.NotFound(responseWriter, request)
		return
	}

	localPath, cached, err := handler.cachedFavicon(request.Context(), domain)
	if err == nil && cached {
		serveCachedFavicon(responseWriter, request, domain, localPath)
		return
	}

	lock := handler.faviconLock(domain)
	lock.Lock()
	defer lock.Unlock()

	// Multiple rows can request the same icon at once. Recheck after taking the
	// per-domain shard lock so only one request performs network I/O.
	localPath, cached, err = handler.cachedFavicon(request.Context(), domain)
	if err == nil && cached {
		serveCachedFavicon(responseWriter, request, domain, localPath)
		return
	}

	localPath, err = handler.fetchFavicon(request.Context(), domain)
	if err != nil {
		serveFaviconPlaceholder(responseWriter, domain)
		return
	}
	serveFaviconFile(responseWriter, request, localPath, "fetched")
}

func (handler *Handler) cachedFavicon(ctx context.Context, domain string) (string, bool, error) {
	var localPath string
	var recentlyChecked int
	err := handler.store.DB.QueryRowContext(ctx, `
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

func (handler *Handler) fetchFavicon(ctx context.Context, domain string) (string, error) {
	if err := os.MkdirAll(handler.faviconDir, 0o755); err != nil {
		return "", err
	}
	fetchCtx, cancelFetch := context.WithTimeout(ctx, 12*time.Second)
	defer cancelFetch()
	client := handler.faviconHTTPClient(ctx)
	candidates, pages := faviconCandidates(domain)
	for _, candidate := range candidates {
		if localPath, err := handler.downloadAndCacheFavicon(fetchCtx, &client, domain, candidate); err == nil {
			return localPath, nil
		}
	}
	for _, page := range pages {
		for _, candidate := range discoverFaviconCandidates(fetchCtx, &client, page) {
			if localPath, err := handler.downloadAndCacheFavicon(fetchCtx, &client, domain, candidate); err == nil {
				return localPath, nil
			}
		}
	}
	_, _ = handler.store.DB.ExecContext(ctx, `
		INSERT INTO domain_favicons(domain, favicon_url, local_path, last_checked_at, updated_at)
		VALUES(?, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(domain) DO UPDATE SET last_checked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
	`, domain)
	return "", errors.New("favicon not found")
}

func (handler *Handler) downloadAndCacheFavicon(ctx context.Context, client *http.Client, domain, candidate string) (string, error) {
	body, resolvedURL, err := downloadFavicon(ctx, client, candidate)
	if err != nil {
		return "", err
	}
	localPath := filepath.Join(handler.faviconDir, safeFaviconFilename(domain))
	if err := os.WriteFile(localPath, body, 0o644); err != nil {
		return "", err
	}
	if _, err := handler.store.DB.ExecContext(ctx, `
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
		direct = append(direct, httpsScheme+host+"/favicon.ico")
	}
	registrable := hosts[len(hosts)-1]
	if !strings.HasPrefix(registrable, "www.") {
		direct = append(direct, httpsScheme+"www."+registrable+"/favicon.ico")
	}
	pages := []string{httpsScheme + registrable + "/"}
	if domain != registrable {
		pages = append(pages, httpsScheme+domain+"/")
	}
	if !strings.HasPrefix(registrable, "www.") {
		pages = append(pages, httpsScheme+"www."+registrable+"/")
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
	defer logActionError("close favicon response body", response.Body.Close)
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
	if !isFaviconImage(response.Header.Get(contentTypeHeader), body) {
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
	defer logActionError("close favicon page response body", response.Body.Close)
	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		return nil
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get(contentTypeHeader))
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
	collectFaviconLinks(baseURL, document, &candidates)
	return uniqueStringsInOrder(candidates)
}

func collectFaviconLinks(baseURL *url.URL, node *html.Node, candidates *[]string) {
	if len(*candidates) >= maxDiscoveredFaviconLinks {
		return
	}
	if candidate, ok := faviconLinkCandidate(baseURL, node); ok {
		*candidates = append(*candidates, candidate)
	}
	for child := node.FirstChild; child != nil && len(*candidates) < maxDiscoveredFaviconLinks; child = child.NextSibling {
		collectFaviconLinks(baseURL, child, candidates)
	}
}

func faviconLinkCandidate(baseURL *url.URL, node *html.Node) (string, bool) {
	if node.Type != html.ElementNode || node.Data != "link" {
		return "", false
	}
	relation, href := faviconLinkAttributes(node)
	if href == "" || !isFaviconRelation(relation) {
		return "", false
	}
	reference, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	resolved := baseURL.ResolveReference(reference)
	if !validDiscoveredFaviconURL(resolved) {
		return "", false
	}
	return resolved.String(), true
}

func faviconLinkAttributes(node *html.Node) (string, string) {
	var relation, href string
	for _, attribute := range node.Attr {
		switch strings.ToLower(attribute.Key) {
		case "rel":
			relation = strings.ToLower(attribute.Val)
		case "href":
			href = strings.TrimSpace(attribute.Val)
		}
	}
	return relation, href
}

func isFaviconRelation(relation string) bool {
	return slices.ContainsFunc(strings.Fields(relation), isFaviconRelationValue)
}

func isFaviconRelationValue(value string) bool {
	return value == "icon" || value == "shortcut" || value == "apple-touch-icon" || value == "mask-icon"
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

func serveCachedFavicon(responseWriter http.ResponseWriter, request *http.Request, domain, localPath string) {
	if localPath == "" {
		serveFaviconPlaceholder(responseWriter, domain)
		return
	}
	serveFaviconFile(responseWriter, request, localPath, "cache")
}

func serveFaviconFile(responseWriter http.ResponseWriter, request *http.Request, localPath, source string) {
	responseWriter.Header().Set("Cache-Control", "public, max-age=86400")
	responseWriter.Header().Set("X-Faro-Favicon", source)
	http.ServeFile(responseWriter, request, localPath)
}

func (handler *Handler) faviconLock(domain string) *sync.Mutex {
	var hash uint32 = 2166136261
	for index := range domain {
		hash ^= uint32(domain[index])
		hash *= 16777619
	}
	return &handler.faviconLocks[hash%uint32(len(handler.faviconLocks))]
}

// faviconHTTPClient keeps Faro's own favicon lookups out of the monitored DNS
// path. Resolving them through the host resolver would create a query-log row,
// which renders another favicon and can recursively generate www/search labels.
func (handler *Handler) faviconHTTPClient(ctx context.Context) http.Client {
	resolver := newUpstreamResolver(handler.faviconDNSUpstreams(ctx))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = faviconDialContext(resolver)
	return http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func (handler *Handler) faviconDNSUpstreams(ctx context.Context) []string {
	configured := strings.Split(settingValue(ctx, handler.store.DB, "upstream_dns"), ",")
	faroIP := strings.TrimSpace(settingValue(ctx, handler.store.DB, "faro_lan_ip"))
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
			return dialPublicFaviconIP(ctx, dialer, network, parsed, port)
		}
		return dialPublicFaviconHost(ctx, resolver, dialer, network, host, port)
	}
}

func dialPublicFaviconIP(ctx context.Context, dialer *net.Dialer, network string, address netip.Addr, port string) (net.Conn, error) {
	if !isPublicFaviconIP(address) {
		return nil, fmt.Errorf("favicon address %s is not public", address)
	}
	return dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
}

func dialPublicFaviconHost(ctx context.Context, resolver *net.Resolver, dialer *net.Dialer, network, host, port string) (net.Conn, error) {
	// The trailing dot makes the lookup absolute, preventing host/Docker DNS
	// search suffixes from being appended to public domain names.
	addresses, err := resolver.LookupNetIP(ctx, "ip", strings.TrimSuffix(host, ".")+".")
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, address := range addresses {
		if !isPublicFaviconIP(address) {
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("favicon domain %s did not resolve to a public address", host)
	}
	return nil, lastErr
}

func isPublicFaviconIP(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsPrivate() && !sharedAddressSpace.Contains(address)
}

var publicDomainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$`)

func isSafeFaviconDomain(domain string) bool {
	if !publicDomainPattern.MatchString(domain) || len(domain) > 253 {
		return false
	}
	labels := strings.Split(domain, ".")
	if !validFaviconLabels(labels) || hasRepeatedFaviconLabels(labels) || hasUnsafeFaviconSuffix(domain) {
		return false
	}
	parsed, err := url.Parse(httpsScheme + domain)
	return err == nil && parsed.Hostname() == domain
}

func validFaviconLabels(labels []string) bool {
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
	}
	return true
}

func hasRepeatedFaviconLabels(labels []string) bool {
	repeatedLabels := 1
	for index := 1; index < len(labels); index++ {
		if labels[index] == labels[index-1] {
			repeatedLabels++
			if repeatedLabels >= 3 {
				return true
			}
		} else {
			repeatedLabels = 1
		}
	}
	return false
}

func hasUnsafeFaviconSuffix(domain string) bool {
	return strings.HasSuffix(domain, ".home") || strings.HasSuffix(domain, ".local") || strings.HasSuffix(domain, ".lan")
}

func safeFaviconFilename(domain string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(domain) + ".ico"
}

func serveFaviconPlaceholder(responseWriter http.ResponseWriter, domain string) {
	initial := "?"
	if domain != "" {
		initial = strings.ToUpper(domain[:1])
	}
	responseWriter.Header().Set(contentTypeHeader, "image/svg+xml")
	responseWriter.Header().Set("Cache-Control", "public, max-age=3600")
	responseWriter.Header().Set("X-Faro-Favicon", "placeholder")
	_, _ = fmt.Fprintf(responseWriter, `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#e8eef5"/><text x="16" y="21" text-anchor="middle" font-family="Arial, sans-serif" font-size="14" font-weight="700" fill="#617085">%s</text></svg>`, initial)
}

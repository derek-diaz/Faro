package handlers

import (
	"context"
	"database/sql"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

type deviceIdentity struct {
	DisplayName    string
	NameSource     string
	DeviceType     string
	TypeConfidence string
}

type cachedDeviceName struct {
	name      string
	expiresAt time.Time
}

type deviceNameResolver struct {
	mu      sync.Mutex
	entries map[string]cachedDeviceName
	lookup  func(context.Context, string) ([]string, error)
}

func newDeviceNameResolver() *deviceNameResolver {
	return &deviceNameResolver{
		entries: map[string]cachedDeviceName{},
		lookup:  net.DefaultResolver.LookupAddr,
	}
}

// discoverDeviceNames uses explicit Local DNS records first, then bounded reverse
// DNS lookups. Reverse lookups run concurrently so a router without PTR records
// cannot make a large inventory take one timeout per device to load.
func (s *Handler) discoverDeviceNames(ctx context.Context, clientIPs []string) map[string]deviceIdentity {
	identities := make(map[string]deviceIdentity, len(clientIPs))
	rows, err := s.store.DB.QueryContext(ctx, `
		SELECT value, hostname FROM dns_records
		WHERE type IN ('A', 'AAAA')
		ORDER BY updated_at DESC, id DESC
	`)
	if err == nil {
		for rows.Next() {
			var clientIP, hostname string
			if rows.Scan(&clientIP, &hostname) == nil {
				if _, exists := identities[clientIP]; !exists {
					identities[clientIP] = deviceIdentity{DisplayName: friendlyHostname(hostname), NameSource: "local_dns"}
				}
			}
		}
		_ = rows.Close()
	}

	resolver := s.deviceNames
	if resolver == nil {
		resolver = newDeviceNameResolver()
	}
	type result struct{ clientIP, name string }
	results := make(chan result, len(clientIPs))
	var wait sync.WaitGroup
	for _, clientIP := range clientIPs {
		if identities[clientIP].DisplayName != "" || net.ParseIP(clientIP) == nil {
			continue
		}
		if cached, ok := resolver.cached(clientIP); ok {
			if cached != "" {
				identities[clientIP] = deviceIdentity{DisplayName: cached, NameSource: "reverse_dns"}
			}
			continue
		}
		wait.Add(1)
		go func(ip string) {
			defer wait.Done()
			lookupCtx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
			defer cancel()
			names, lookupErr := resolver.lookup(lookupCtx, ip)
			name := ""
			if lookupErr == nil && len(names) > 0 {
				name = friendlyHostname(names[0])
				if !usefulHostname(name, ip) {
					name = ""
				}
			}
			resolver.store(ip, name)
			results <- result{clientIP: ip, name: name}
		}(clientIP)
	}
	go func() {
		wait.Wait()
		close(results)
	}()
	for resolved := range results {
		if resolved.name != "" {
			identities[resolved.clientIP] = deviceIdentity{DisplayName: resolved.name, NameSource: "reverse_dns"}
		}
	}
	return identities
}

func (r *deviceNameResolver) cached(clientIP string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[clientIP]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(r.entries, clientIP)
		return "", false
	}
	return entry.name, true
}

func (r *deviceNameResolver) store(clientIP, name string) {
	ttl := 12 * time.Hour
	if name == "" {
		ttl = 30 * time.Minute
	}
	r.mu.Lock()
	r.entries[clientIP] = cachedDeviceName{name: name, expiresAt: time.Now().Add(ttl)}
	r.mu.Unlock()
}

func friendlyHostname(hostname string) string {
	name := strings.TrimSpace(strings.TrimSuffix(hostname, "."))
	lower := strings.ToLower(name)
	for _, suffix := range []string{".home.arpa", ".localdomain", ".local", ".lan", ".home"} {
		if strings.HasSuffix(lower, suffix) {
			name = name[:len(name)-len(suffix)]
			break
		}
	}
	return name
}

func usefulHostname(hostname, clientIP string) bool {
	if hostname == "" || strings.EqualFold(hostname, clientIP) || net.ParseIP(hostname) != nil {
		return false
	}
	compactName := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(hostname))
	compactIP := strings.NewReplacer(".", "", ":", "").Replace(strings.ToLower(clientIP))
	return compactName != compactIP && compactName != "ip"+compactIP
}

type deviceTypeRule struct {
	name       string
	nameTokens []string
	domains    map[string]int
}

var deviceTypeRules = []deviceTypeRule{
	{name: "Tesla", nameTokens: []string{"tesla"}, domains: weightedDomains(4, "tesla.com", "teslamotors.com")},
	{name: "NAS", nameTokens: []string{"synology", "diskstation", "nas", "plex"}, domains: weightedDomains(4, "synology.com", "quickconnect.to")},
	{name: "Roku", nameTokens: []string{"roku"}, domains: weightedDomains(4, "roku.com", "rokutime.com")},
	{name: "Smart TV", nameTokens: []string{"tv", "smarttv", "webos", "tizen"}, domains: mergeWeightedDomains(weightedDomains(4, "samsungacr.com", "samsungcloudsolution.com", "lgtvsdp.com"), weightedDomains(2, "samsung.com", "lg.com"))},
	{name: "PlayStation", nameTokens: []string{"playstation", "ps4", "ps5"}, domains: weightedDomains(4, "playstation.net", "playstation.com")},
	{name: "Xbox", nameTokens: []string{"xbox"}, domains: weightedDomains(4, "xboxlive.com", "xbox.com")},
	{name: "Nintendo", nameTokens: []string{"nintendo", "switch"}, domains: weightedDomains(4, "nintendo.net", "nintendo.com")},
	{name: "Sonos", nameTokens: []string{"sonos"}, domains: weightedDomains(4, "sonos.com")},
	{name: "Android Device", nameTokens: []string{"android", "pixel"}, domains: weightedDomains(3, "android.clients.google.com", "connectivitycheck.gstatic.com", "connectivitycheck.android.com", "mtalk.google.com")},
	{name: "Apple Device", nameTokens: []string{"iphone", "ipad", "mac", "macbook", "imac", "apple"}, domains: mergeWeightedDomains(weightedDomains(4, "mesu.apple.com", "gdmf.apple.com"), weightedDomains(3, "xp.apple.com", "captive.apple.com"))},
	{name: "Windows PC", nameTokens: []string{"windows", "winpc"}, domains: weightedDomains(3, "windowsupdate.com", "microsoftconnecttest.com", "msftconnecttest.com", "settings-win.data.microsoft.com")},
	{name: "Linux Server", nameTokens: []string{"ubuntu", "debian", "fedora", "linux"}, domains: weightedDomains(3, "archive.ubuntu.com", "security.ubuntu.com", "connectivity-check.ubuntu.com", "deb.debian.org", "mirrors.fedoraproject.org")},
}

func inferDeviceType(ctx context.Context, database *sql.DB, clientIP, name string) (string, string) {
	domains := topLabels(ctx, database, `SELECT domain, COUNT(*) FROM dns_queries WHERE client_ip = ? GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 80`, clientIP)
	return inferDeviceTypeFromSignals(name, clientIP, domains)
}

func inferDeviceTypeFromSignals(name, clientIP string, domains []string) (string, string) {
	tokens := signalTokens(name)
	if tokens["appletv"] || (tokens["apple"] && tokens["tv"]) {
		return "Apple TV", "high"
	}
	type score struct {
		name  string
		value int
	}
	scores := make([]score, 0, len(deviceTypeRules))
	for _, rule := range deviceTypeRules {
		value := 0
		for _, token := range rule.nameTokens {
			if tokens[token] {
				value += 7
			}
		}
		for _, domain := range domains {
			for signature, weight := range rule.domains {
				if domainMatches(domain, signature) {
					value += weight
					break
				}
			}
		}
		if value > 0 {
			scores = append(scores, score{name: rule.name, value: value})
		}
	}
	if clientIP == "127.0.0.1" {
		return "Linux Server", "high"
	}
	if len(scores) == 0 {
		return "Unknown", "unknown"
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].value > scores[j].value })
	if scores[0].value < 3 || (len(scores) > 1 && scores[0].value == scores[1].value) {
		return "Unknown", "unknown"
	}
	if scores[0].value >= 7 {
		return scores[0].name, "high"
	}
	return scores[0].name, "medium"
}

func signalTokens(value string) map[string]bool {
	tokens := map[string]bool{}
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		tokens[token] = true
	}
	return tokens
}

func domainMatches(domain, signature string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	return domain == signature || strings.HasSuffix(domain, "."+signature)
}

func weightedDomains(weight int, domains ...string) map[string]int {
	result := make(map[string]int, len(domains))
	for _, domain := range domains {
		result[domain] = weight
	}
	return result
}

func mergeWeightedDomains(groups ...map[string]int) map[string]int {
	result := map[string]int{}
	for _, group := range groups {
		for domain, weight := range group {
			result[domain] = weight
		}
	}
	return result
}

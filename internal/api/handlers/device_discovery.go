package handlers

import (
	"context"
	"database/sql"
	"net"
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

// noinspection SpellCheckingInspection
const localDomainSuffix = ".localdomain"

func newDeviceNameResolver() *deviceNameResolver {
	return &deviceNameResolver{
		entries: map[string]cachedDeviceName{},
		lookup:  net.DefaultResolver.LookupAddr,
	}
}

// discoverDeviceNames prefers explicit Local DNS and UniFi names before bounded
// reverse-DNS lookups. Concurrent lookups keep routers without PTR records from
// adding one timeout per device to a large inventory load.
func (handler *Handler) discoverDeviceNames(ctx context.Context, clientIPs []string) map[string]deviceIdentity {
	identities := make(map[string]deviceIdentity, len(clientIPs))
	requested := uniqueDeviceAddresses(clientIPs)
	loadLocalDeviceNames(ctx, handler.store.DB, requested, identities)
	loadUniFiDeviceNames(ctx, handler.store.DB, requested, identities)
	resolveReverseDeviceNames(ctx, clientIPs, identities, handler.deviceNames)
	return identities
}

func loadLocalDeviceNames(ctx context.Context, database *sql.DB, requested []string, identities map[string]deviceIdentity) {
	for start := 0; start < len(requested); start += 400 {
		end := min(start+400, len(requested))
		arguments, placeholders := stringQueryArguments(requested[start:end])
		rows, err := database.QueryContext(ctx, `
			SELECT value, hostname FROM dns_records
			WHERE type IN ('A', 'AAAA') AND value IN (`+placeholders+`)
			ORDER BY updated_at DESC, id DESC
		`, arguments...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var clientIP, hostname string
			if err := rows.Scan(&clientIP, &hostname); err != nil {
				continue
			}
			if _, exists := identities[clientIP]; !exists {
				identities[clientIP] = deviceIdentity{DisplayName: friendlyHostname(hostname), NameSource: "local_dns"}
			}
		}
		closeRows(rows)
	}
}

func loadUniFiDeviceNames(ctx context.Context, database *sql.DB, requested []string, identities map[string]deviceIdentity) {
	for start := 0; start < len(requested); start += 400 {
		end := min(start+400, len(requested))
		arguments, placeholders := stringQueryArguments(requested[start:end])
		rows, err := database.QueryContext(ctx, `
			SELECT a.address, n.name
			FROM device_names n
			JOIN device_addresses a ON a.device_id = n.device_id
			WHERE n.source = 'unifi' AND TRIM(n.name) <> '' AND a.address IN (`+placeholders+`)
			ORDER BY n.last_seen_at DESC
		`, arguments...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var clientIP, name string
			if err := rows.Scan(&clientIP, &name); err != nil {
				continue
			}
			if _, exists := identities[clientIP]; !exists && usefulHostname(name, clientIP) {
				identities[clientIP] = deviceIdentity{DisplayName: name, NameSource: "unifi"}
			}
		}
		closeRows(rows)
	}
}

func resolveReverseDeviceNames(ctx context.Context, clientIPs []string, identities map[string]deviceIdentity, resolver *deviceNameResolver) {
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
}

func uniqueDeviceAddresses(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func stringQueryArguments(values []string) ([]any, string) {
	arguments := make([]any, len(values))
	placeholders := make([]string, len(values))
	for index, value := range values {
		arguments[index] = value
		placeholders[index] = "?"
	}
	return arguments, strings.Join(placeholders, ",")
}

func (resolver *deviceNameResolver) cached(clientIP string) (string, bool) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	entry, ok := resolver.entries[clientIP]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(resolver.entries, clientIP)
		return "", false
	}
	return entry.name, true
}

func (resolver *deviceNameResolver) store(clientIP, name string) {
	ttl := 12 * time.Hour
	if name == "" {
		ttl = 30 * time.Minute
	}
	resolver.mu.Lock()
	resolver.entries[clientIP] = cachedDeviceName{name: name, expiresAt: time.Now().Add(ttl)}
	resolver.mu.Unlock()
}

func friendlyHostname(hostname string) string {
	name := strings.TrimSpace(strings.TrimSuffix(hostname, "."))
	lower := strings.ToLower(name)
	for _, suffix := range []string{".home.arpa", localDomainSuffix, ".local", ".lan", ".home"} {
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

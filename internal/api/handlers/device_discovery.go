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
	mu         sync.Mutex
	entries    map[string]cachedDeviceName
	lookup     func(context.Context, string) ([]string, error)
	pending    map[string]bool
	slots      chan struct{}
	generation uint64
}

// noinspection SpellCheckingInspection
const localDomainSuffix = ".localdomain"

func newDeviceNameResolver() *deviceNameResolver {
	return &deviceNameResolver{
		entries: map[string]cachedDeviceName{},
		pending: map[string]bool{},
		slots:   make(chan struct{}, 8),
		lookup:  net.DefaultResolver.LookupAddr,
	}
}

// discoverDeviceNames prefers explicit Local DNS and UniFi names before bounded
// cached reverse-DNS names. Missing PTR records are resolved in the background
// so a router timeout never delays a device panel.
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
	for _, clientIP := range uniqueDeviceAddresses(clientIPs) {
		if identities[clientIP].DisplayName != "" || net.ParseIP(clientIP) == nil {
			continue
		}
		if name, ok := resolver.cached(clientIP); ok {
			if name != "" {
				identities[clientIP] = deviceIdentity{DisplayName: name, NameSource: "reverse_dns"}
			}
			continue
		}
		resolver.resolveInBackground(clientIP)
	}
}

func (resolver *deviceNameResolver) resolveInBackground(ip string) {
	resolver.mu.Lock()
	if resolver.pending == nil {
		resolver.pending = map[string]bool{}
	}
	if resolver.slots == nil {
		resolver.slots = make(chan struct{}, 8)
	}
	if resolver.pending[ip] {
		resolver.mu.Unlock()
		return
	}
	select {
	case resolver.slots <- struct{}{}:
	default:
		resolver.mu.Unlock()
		return
	}
	resolver.pending[ip] = true
	resolver.mu.Unlock()
	go func() {
		defer func() { resolver.mu.Lock(); delete(resolver.pending, ip); resolver.mu.Unlock(); <-resolver.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		names, err := resolver.lookup(ctx, ip)
		name := ""
		if err == nil && len(names) > 0 {
			name = friendlyHostname(names[0])
			if !usefulHostname(name, ip) {
				name = ""
			}
		}
		resolver.store(ip, name)
	}()
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
	resolver.generation++
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

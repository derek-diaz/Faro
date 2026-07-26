package handlers

import (
	"context"
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
	requested := uniqueDeviceAddresses(clientIPs)
	for start := 0; start < len(requested); start += 400 {
		end := start + 400
		if end > len(requested) {
			end = len(requested)
		}
		arguments, placeholders := stringQueryArguments(requested[start:end])
		rows, err := s.store.DB.QueryContext(ctx, `
			SELECT value, hostname FROM dns_records
			WHERE type IN ('A', 'AAAA') AND value IN (`+placeholders+`)
			ORDER BY updated_at DESC, id DESC
		`, arguments...)
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
	}

	for start := 0; start < len(requested); start += 400 {
		end := start + 400
		if end > len(requested) {
			end = len(requested)
		}
		arguments, placeholders := stringQueryArguments(requested[start:end])
		rows, err := s.store.DB.QueryContext(ctx, `
			SELECT a.address, n.name
			FROM device_names n
			JOIN device_addresses a ON a.device_id = n.device_id
			WHERE n.source = 'unifi' AND TRIM(n.name) <> '' AND a.address IN (`+placeholders+`)
			ORDER BY n.last_seen_at DESC
		`, arguments...)
		if err == nil {
			for rows.Next() {
				var clientIP, name string
				if rows.Scan(&clientIP, &name) == nil {
					if _, exists := identities[clientIP]; !exists && usefulHostname(name, clientIP) {
						identities[clientIP] = deviceIdentity{DisplayName: name, NameSource: "unifi"}
					}
				}
			}
			_ = rows.Close()
		}
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

func inferDeviceTypeFromSignals(name, clientIP string, domains []string) (string, string) {
	prediction := bundledDeviceCatalog.Predict(name, clientIP, domains)
	return prediction.DeviceType, prediction.Confidence
}

package dohproxy

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Endpoint describes a public RFC 8484 DNS-over-HTTPS service and the IP
// addresses Faro may use to bootstrap its TLS connection without consulting
// the DNS service it is in the process of configuring.
type Endpoint struct {
	Name         string
	URL          string
	BootstrapIPs []string
}

var catalog = []Endpoint{
	{Name: "Cloudflare Standard", URL: "https://cloudflare-dns.com/dns-query", BootstrapIPs: []string{"1.1.1.1", "1.0.0.1"}},
	{Name: "Cloudflare Malware Blocking", URL: "https://security.cloudflare-dns.com/dns-query", BootstrapIPs: []string{"1.1.1.2", "1.0.0.2"}},
	{Name: "Cloudflare Family", URL: "https://family.cloudflare-dns.com/dns-query", BootstrapIPs: []string{"1.1.1.3", "1.0.0.3"}},
	{Name: "Google Public DNS", URL: "https://dns.google/dns-query", BootstrapIPs: []string{"8.8.8.8", "8.8.4.4"}},
	{Name: "Quad9 Secure", URL: "https://dns.quad9.net/dns-query", BootstrapIPs: []string{"9.9.9.9", "149.112.112.112"}},
	{Name: "Quad9 Unfiltered", URL: "https://dns10.quad9.net/dns-query", BootstrapIPs: []string{"9.9.9.10", "149.112.112.10"}},
	{Name: "Quad9 Secure with ECS", URL: "https://dns11.quad9.net/dns-query", BootstrapIPs: []string{"9.9.9.11", "149.112.112.11"}},
	{Name: "AdGuard Default", URL: "https://dns.adguard-dns.com/dns-query", BootstrapIPs: []string{"94.140.14.14", "94.140.15.15"}},
	{Name: "AdGuard Unfiltered", URL: "https://unfiltered.adguard-dns.com/dns-query", BootstrapIPs: []string{"94.140.14.140", "94.140.14.141"}},
	{Name: "AdGuard Family", URL: "https://family.adguard-dns.com/dns-query", BootstrapIPs: []string{"94.140.14.15", "94.140.15.16"}},
	{Name: "OpenDNS Standard", URL: "https://doh.opendns.com/dns-query", BootstrapIPs: []string{"208.67.222.222", "208.67.220.220"}},
	{Name: "OpenDNS FamilyShield", URL: "https://doh.familyshield.opendns.com/dns-query", BootstrapIPs: []string{"208.67.222.123", "208.67.220.123"}},
}

var endpointByAddress = buildAddressIndex(catalog)

func buildAddressIndex(endpoints []Endpoint) map[string]Endpoint {
	index := make(map[string]Endpoint)
	for _, endpoint := range endpoints {
		for _, rawAddress := range endpoint.BootstrapIPs {
			address := net.ParseIP(rawAddress)
			if address != nil {
				index[address.String()] = cloneEndpoint(endpoint)
			}
		}
	}
	return index
}

// EndpointForAddress returns the encrypted service corresponding to a public
// resolver IP shown in Faro's provider catalog.
func EndpointForAddress(address string) (Endpoint, bool) {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil {
		return Endpoint{}, false
	}
	endpoint, ok := endpointByAddress[ip.String()]
	return cloneEndpoint(endpoint), ok
}

// EndpointsForAddresses preserves provider order while collapsing the primary
// and secondary IPs that represent the same encrypted endpoint.
func EndpointsForAddresses(addresses []string) ([]Endpoint, error) {
	seen := make(map[string]bool)
	endpoints := make([]Endpoint, 0, len(addresses))
	for _, address := range addresses {
		endpoint, ok := EndpointForAddress(address)
		if !ok {
			return nil, fmt.Errorf("encrypted DNS is unavailable for custom resolver %q; choose Standard DNS or remove it", strings.TrimSpace(address))
		}
		if seen[endpoint.URL] {
			continue
		}
		seen[endpoint.URL] = true
		endpoints = append(endpoints, endpoint)
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("encrypted DNS requires at least one supported provider")
	}
	return endpoints, nil
}

func validateEndpoint(endpoint Endpoint) error {
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Port() != "" || parsed.User != nil {
		return fmt.Errorf("invalid encrypted DNS endpoint %q", endpoint.URL)
	}
	if len(endpoint.BootstrapIPs) == 0 {
		return fmt.Errorf("encrypted DNS endpoint %q has no bootstrap addresses", endpoint.URL)
	}
	for _, address := range endpoint.BootstrapIPs {
		if net.ParseIP(address) == nil {
			return fmt.Errorf("encrypted DNS endpoint %q has invalid bootstrap address %q", endpoint.URL, address)
		}
	}
	return nil
}

func cloneEndpoint(endpoint Endpoint) Endpoint {
	endpoint.BootstrapIPs = append([]string(nil), endpoint.BootstrapIPs...)
	return endpoint
}

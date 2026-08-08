package upstreamhealth

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/dohproxy"
)

const DefaultInterval = 30 * time.Second

type Probe struct {
	Address   string   `json:"address"`
	Status    string   `json:"status"`
	LatencyMS *float64 `json:"latency_ms"`
	CheckedAt string   `json:"checked_at"`
	Error     string   `json:"error,omitempty"`
}

type Snapshot struct {
	Status    string  `json:"status"`
	Summary   string  `json:"summary"`
	CheckedAt string  `json:"checked_at,omitempty"`
	Items     []Probe `json:"items"`
}

type ProbeFunc func(context.Context, string) Probe

type Monitor struct {
	store    *db.Store
	interval time.Duration
	probe    ProbeFunc
	trigger  chan struct{}
	checkMu  sync.Mutex
	mu       sync.RWMutex
	snapshot Snapshot
}

func NewMonitor(store *db.Store, interval time.Duration, probe ProbeFunc) *Monitor {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Monitor{
		store:    store,
		interval: interval,
		probe:    probe,
		trigger:  make(chan struct{}, 1),
		snapshot: Snapshot{Status: "unknown", Summary: "Upstream health has not been checked yet.", Items: make([]Probe, 0)},
	}
}

func (monitor *Monitor) Run(ctx context.Context) {
	monitor.CheckNow(ctx)
	ticker := time.NewTicker(monitor.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			monitor.CheckNow(ctx)
		case <-monitor.trigger:
			monitor.CheckNow(ctx)
		}
	}
}

func (monitor *Monitor) Trigger() {
	select {
	case monitor.trigger <- struct{}{}:
	default:
	}
}

func (monitor *Monitor) CheckNow(ctx context.Context) Snapshot {
	monitor.checkMu.Lock()
	defer monitor.checkMu.Unlock()

	addresses := configuredAddresses(ctx, monitor.store)
	probe := monitor.probe
	if probe == nil {
		if configuredTransport(ctx, monitor.store) == "encrypted" {
			probe = ProbeEncryptedAddress
		} else {
			probe = ProbeAddress
		}
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	items := ProbeAddresses(ctx, addresses, probe)
	next := summarize(items, checkedAt)
	monitor.mu.Lock()
	monitor.snapshot = next
	monitor.mu.Unlock()
	return cloneSnapshot(next)
}

func (monitor *Monitor) Snapshot() Snapshot {
	monitor.mu.RLock()
	defer monitor.mu.RUnlock()
	return cloneSnapshot(monitor.snapshot)
}

func ProbeAddresses(ctx context.Context, addresses []string, probe ProbeFunc) []Probe {
	if probe == nil {
		probe = ProbeAddress
	}
	type indexedProbe struct {
		index int
		probe Probe
	}
	results := make([]Probe, len(addresses))
	probes := make(chan indexedProbe, len(addresses))
	for index, address := range addresses {
		go func(index int, address string) {
			probes <- indexedProbe{index: index, probe: probe(ctx, address)}
		}(index, address)
	}
	for range addresses {
		result := <-probes
		results[result.index] = result.probe
	}
	return results
}

func ProbeAddress(ctx context.Context, address string) Probe {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	result := Probe{Address: address, Status: "unavailable", CheckedAt: checkedAt}
	if net.ParseIP(address) == nil {
		result.Error = "invalid DNS server address"
		return result
	}
	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, "udp", net.JoinHostPort(address, "53"))
	if err != nil {
		result.Error = compactProbeError(err)
		return result
	}
	defer closeProbeConnection(connection)
	if err := connection.SetDeadline(time.Now().Add(1500 * time.Millisecond)); err != nil {
		result.Error = compactProbeError(err)
		return result
	}

	queryID := uint16(time.Now().UnixNano())
	query := dnsProbeQuery(queryID, "example.com")
	started := time.Now()
	if _, err := connection.Write(query); err != nil {
		result.Error = compactProbeError(err)
		return result
	}
	response := make([]byte, 1232)
	n, err := connection.Read(response)
	if err != nil {
		result.Error = compactProbeError(err)
		return result
	}
	if n < 12 || binary.BigEndian.Uint16(response[0:2]) != queryID || response[2]&0x80 == 0 {
		result.Error = "invalid DNS response"
		return result
	}
	latency := float64(time.Since(started).Microseconds()) / 1000
	rounded := float64(int(latency*10+0.5)) / 10
	result.Status = "online"
	result.LatencyMS = &rounded
	return result
}

func ProbeEncryptedAddress(ctx context.Context, address string) Probe {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	result := Probe{Address: address, Status: "unavailable", CheckedAt: checkedAt}
	latency, err := dohproxy.ProbeAddress(ctx, address)
	if err != nil {
		result.Error = compactProbeError(err)
		return result
	}
	latencyMS := float64(latency.Microseconds()) / 1000
	rounded := float64(int(latencyMS*10+0.5)) / 10
	result.Status = "online"
	result.LatencyMS = &rounded
	return result
}

func configuredAddresses(ctx context.Context, store *db.Store) []string {
	var raw string
	if err := store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'upstream_dns'`).Scan(&raw); err != nil {
		return make([]string, 0)
	}
	seen := map[string]bool{}
	addresses := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		address := strings.TrimSpace(value)
		if address == "" || seen[address] {
			continue
		}
		seen[address] = true
		addresses = append(addresses, address)
	}
	return addresses
}

func closeProbeConnection(connection net.Conn) {
	if err := connection.Close(); err != nil {
		log.Printf("close upstream health probe connection: %v", err)
	}
}

func configuredTransport(ctx context.Context, store *db.Store) string {
	var transport string
	if err := store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'upstream_transport'`).Scan(&transport); err != nil {
		return "standard"
	}
	if strings.TrimSpace(transport) == "encrypted" {
		return "encrypted"
	}
	return "standard"
}

func summarize(items []Probe, checkedAt string) Snapshot {
	if len(items) == 0 {
		return Snapshot{Status: "unknown", Summary: "No upstream resolvers are configured.", CheckedAt: checkedAt, Items: make([]Probe, 0)}
	}
	online := 0
	for _, item := range items {
		if item.Status == "online" {
			online++
		}
	}
	status := "degraded"
	summary := fmt.Sprintf("%d of %d upstream resolvers are online.", online, len(items))
	if online == len(items) {
		status = "healthy"
		summary = fmt.Sprintf("All %d upstream resolvers are online.", len(items))
	} else if online == 0 {
		status = "critical"
		summary = fmt.Sprintf("All %d upstream resolvers are unavailable.", len(items))
	}
	return Snapshot{Status: status, Summary: summary, CheckedAt: checkedAt, Items: items}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	items := make([]Probe, len(snapshot.Items))
	copy(items, snapshot.Items)
	snapshot.Items = items
	return snapshot
}

func dnsProbeQuery(id uint16, hostname string) []byte {
	packet := make([]byte, 12)
	binary.BigEndian.PutUint16(packet[0:2], id)
	binary.BigEndian.PutUint16(packet[2:4], 0x0100)
	binary.BigEndian.PutUint16(packet[4:6], 1)
	for _, label := range strings.Split(strings.TrimSuffix(hostname, "."), ".") {
		packet = append(packet, byte(len(label)))
		packet = append(packet, label...)
	}
	packet = append(packet, 0)
	packet = binary.BigEndian.AppendUint16(packet, 1)
	packet = binary.BigEndian.AppendUint16(packet, 1)
	return packet
}

func compactProbeError(err error) string {
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "DNS query timed out"
	}
	return "DNS query failed"
}

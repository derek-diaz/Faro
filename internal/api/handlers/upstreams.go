package handlers

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

func (s *Handler) upstreamProbes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var input struct {
		Addresses []string `json:"addresses"`
	}
	if !decode(w, r, &input) {
		return
	}
	addresses := []string{}
	seen := map[string]bool{}
	for _, rawAddress := range input.Addresses {
		address := strings.TrimSpace(rawAddress)
		if address == "" || seen[address] {
			continue
		}
		if net.ParseIP(address) == nil {
			writeBadRequest(w, fmt.Errorf("invalid upstream IP address: %s", address))
			return
		}
		seen[address] = true
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		writeBadRequest(w, errors.New("at least one upstream IP address is required"))
		return
	}
	if len(addresses) > 32 {
		writeBadRequest(w, errors.New("a maximum of 32 upstreams can be probed at once"))
		return
	}

	type indexedProbe struct {
		index int
		probe map[string]any
	}
	results := make([]map[string]any, len(addresses))
	probes := make(chan indexedProbe, len(addresses))
	for index, address := range addresses {
		go func(index int, address string) {
			probes <- indexedProbe{index: index, probe: probeDNSUpstream(r.Context(), address)}
		}(index, address)
	}
	for range addresses {
		result := <-probes
		results[result.index] = result.probe
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": results})
}

func probeDNSUpstream(ctx context.Context, address string) map[string]any {
	checkedAt := time.Now().UTC()
	result := map[string]any{
		"address":    address,
		"status":     "unavailable",
		"latency_ms": nil,
		"checked_at": checkedAt.Format(time.RFC3339),
	}
	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, "udp", net.JoinHostPort(address, "53"))
	if err != nil {
		result["error"] = compactProbeError(err)
		return result
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(1500 * time.Millisecond))

	queryID := uint16(time.Now().UnixNano())
	query := dnsProbeQuery(queryID, "example.com")
	started := time.Now()
	if _, err := connection.Write(query); err != nil {
		result["error"] = compactProbeError(err)
		return result
	}
	response := make([]byte, 1232)
	n, err := connection.Read(response)
	if err != nil {
		result["error"] = compactProbeError(err)
		return result
	}
	if n < 12 || binary.BigEndian.Uint16(response[0:2]) != queryID || response[2]&0x80 == 0 {
		result["error"] = "invalid DNS response"
		return result
	}
	latency := float64(time.Since(started).Microseconds()) / 1000
	result["status"] = "online"
	result["latency_ms"] = float64(int(latency*10+0.5)) / 10
	delete(result, "error")
	return result
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
	if networkError, ok := err.(net.Error); ok && networkError.Timeout() {
		return "DNS query timed out"
	}
	return "DNS query failed"
}

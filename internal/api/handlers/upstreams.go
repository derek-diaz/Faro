package handlers

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/derek/faro/internal/dohproxy"
	"github.com/derek/faro/internal/upstreamhealth"
)

func (s *Handler) upstreamCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"encrypted_endpoints": dohproxy.Catalog(),
	})
}

func (s *Handler) upstreamProbes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var input struct {
		Addresses []string `json:"addresses"`
		Transport string   `json:"transport"`
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

	probe := upstreamhealth.ProbeAddress
	if input.Transport == "encrypted" {
		probe = upstreamhealth.ProbeEncryptedAddress
	} else if input.Transport != "" && input.Transport != "standard" {
		writeBadRequest(w, errors.New("transport must be encrypted or standard"))
		return
	}
	results := upstreamhealth.ProbeAddresses(r.Context(), addresses, probe)
	writeJSON(w, http.StatusOK, map[string]any{"items": results})
}

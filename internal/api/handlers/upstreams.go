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

func (handler *Handler) upstreamCatalog(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"encrypted_endpoints": dohproxy.Catalog(),
	})
}

func (handler *Handler) upstreamProbes(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(responseWriter)
		return
	}
	var input struct {
		Addresses []string `json:"addresses"`
		Transport string   `json:"transport"`
	}
	if !decode(responseWriter, request, &input) {
		return
	}
	addresses := make([]string, 0, len(input.Addresses))
	seen := map[string]bool{}
	for _, rawAddress := range input.Addresses {
		address := strings.TrimSpace(rawAddress)
		if address == "" || seen[address] {
			continue
		}
		if net.ParseIP(address) == nil {
			writeBadRequest(responseWriter, fmt.Errorf("invalid upstream IP address: %s", address))
			return
		}
		seen[address] = true
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		writeBadRequest(responseWriter, errors.New("at least one upstream IP address is required"))
		return
	}
	if len(addresses) > 32 {
		writeBadRequest(responseWriter, errors.New("a maximum of 32 upstreams can be probed at once"))
		return
	}

	probe := upstreamhealth.ProbeAddress
	if input.Transport == "encrypted" {
		probe = upstreamhealth.ProbeEncryptedAddress
	} else if input.Transport != "" && input.Transport != "standard" {
		writeBadRequest(responseWriter, errors.New("transport must be encrypted or standard"))
		return
	}
	results := upstreamhealth.ProbeAddresses(request.Context(), addresses, probe)
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": results})
}

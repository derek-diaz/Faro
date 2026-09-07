package dohproxy

import "slices"

// replaceState is called with reloadMu held. Keep one rollback generation,
// retiring transports only once neither accepted generation needs them.
func (proxy *Proxy) replaceState(next *resolverState) {
	current := proxy.state.Load()
	closeUnusedClients(proxy.previous, current, next)
	proxy.previous = current
	proxy.state.Store(next)
}

func matchingClient(endpoint Endpoint, states ...*resolverState) *endpointClient {
	for _, state := range states {
		if state == nil {
			continue
		}
		for _, client := range state.clients {
			if client.endpoint.URL == endpoint.URL && slices.Equal(client.endpoint.BootstrapIPs, endpoint.BootstrapIPs) {
				return client
			}
		}
	}
	return nil
}

func closeUnusedClients(retired *resolverState, keep ...*resolverState) {
	if retired == nil {
		return
	}
	retained := make(map[*endpointClient]bool)
	for _, state := range keep {
		if state != nil {
			for _, client := range state.clients {
				retained[client] = true
			}
		}
	}
	for _, client := range retired.clients {
		if !retained[client] && client.client != nil {
			client.client.CloseIdleConnections()
			retained[client] = true
		}
	}
}

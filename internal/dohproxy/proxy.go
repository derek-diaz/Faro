package dohproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/derek/faro/internal/db"
)

const DefaultAddress = "127.0.0.1:5053"

type resolverState struct {
	clients []*endpointClient
}

// Proxy is a loopback-only DNS gateway. CoreDNS sends ordinary DNS packets to
// it, and Proxy exchanges them with the selected providers using RFC 8484.
type Proxy struct {
	store      *db.Store
	address    string
	state      atomic.Pointer[resolverState]
	next       atomic.Uint64
	reloadMu   sync.Mutex
	serveOnce  sync.Once
	concurrent chan struct{}
}

func New(store *db.Store, address string) *Proxy {
	if strings.TrimSpace(address) == "" {
		address = DefaultAddress
	}
	return &Proxy{
		store:      store,
		address:    address,
		concurrent: make(chan struct{}, 256),
	}
}

func (p *Proxy) Reload(ctx context.Context) error {
	p.reloadMu.Lock()
	defer p.reloadMu.Unlock()
	transport, addresses, err := configuredTransport(ctx, p.store)
	if err != nil {
		return err
	}
	if transport != "encrypted" {
		p.state.Store(&resolverState{})
		return nil
	}
	endpoints, err := EndpointsForAddresses(addresses)
	if err != nil {
		return err
	}
	clients := make([]*endpointClient, 0, len(endpoints))
	for _, endpoint := range endpoints {
		client, clientErr := newEndpointClient(endpoint)
		if clientErr != nil {
			return clientErr
		}
		clients = append(clients, client)
	}
	p.state.Store(&resolverState{clients: clients})
	return nil
}

func (p *Proxy) Start(ctx context.Context) error {
	var startErr error
	p.serveOnce.Do(func() {
		if err := p.Reload(ctx); err != nil {
			startErr = err
			return
		}
		tcpListener, err := net.Listen("tcp", p.address)
		if err != nil {
			startErr = fmt.Errorf("start encrypted DNS TCP gateway: %w", err)
			return
		}
		udpAddress, err := net.ResolveUDPAddr("udp", tcpListener.Addr().String())
		if err != nil {
			_ = tcpListener.Close()
			startErr = err
			return
		}
		udpConnection, err := net.ListenUDP("udp", udpAddress)
		if err != nil {
			_ = tcpListener.Close()
			startErr = fmt.Errorf("start encrypted DNS UDP gateway: %w", err)
			return
		}
		go func() {
			<-ctx.Done()
			_ = udpConnection.Close()
			_ = tcpListener.Close()
		}()
		go p.serveUDP(ctx, udpConnection)
		go p.serveTCP(ctx, tcpListener)
	})
	return startErr
}

func (p *Proxy) serveUDP(ctx context.Context, connection *net.UDPConn) {
	buffer := make([]byte, maxDNSMessageBytes)
	for {
		n, client, err := connection.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				log.Printf("encrypted DNS UDP gateway stopped: %v", err)
			}
			return
		}
		query := append([]byte(nil), buffer[:n]...)
		go func() {
			if !p.acquire(ctx) {
				return
			}
			defer p.release()
			queryCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			response, exchangeErr := p.exchange(queryCtx, query)
			if exchangeErr != nil {
				response = serverFailure(query)
			}
			if len(response) > 0 {
				_, _ = connection.WriteToUDP(response, client)
			}
		}()
	}
}

func (p *Proxy) serveTCP(ctx context.Context, listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				log.Printf("encrypted DNS TCP gateway stopped: %v", err)
			}
			return
		}
		go p.handleTCP(ctx, connection)
	}
}

func (p *Proxy) handleTCP(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	for {
		_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
		var length uint16
		if err := binary.Read(connection, binary.BigEndian, &length); err != nil {
			if !errors.Is(err, io.EOF) {
				return
			}
			return
		}
		if length < 12 {
			return
		}
		query := make([]byte, int(length))
		if _, err := io.ReadFull(connection, query); err != nil {
			return
		}
		if !p.acquire(ctx) {
			return
		}
		queryCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		response, exchangeErr := p.exchange(queryCtx, query)
		cancel()
		p.release()
		if exchangeErr != nil {
			response = serverFailure(query)
		}
		if len(response) == 0 || len(response) > maxDNSMessageBytes {
			return
		}
		if err := binary.Write(connection, binary.BigEndian, uint16(len(response))); err != nil {
			return
		}
		if _, err := connection.Write(response); err != nil {
			return
		}
	}
}

func (p *Proxy) exchange(ctx context.Context, query []byte) ([]byte, error) {
	state := p.state.Load()
	if state == nil || len(state.clients) == 0 {
		return nil, errors.New("encrypted DNS has no configured providers")
	}
	start := int(p.next.Add(1)-1) % len(state.clients)
	var lastErr error
	for offset := range state.clients {
		client := state.clients[(start+offset)%len(state.clients)]
		response, err := client.exchange(ctx, query)
		if err == nil && dnsResponseCode(response) != 2 {
			return response, nil
		}
		if err == nil {
			err = errors.New("encrypted DNS provider returned SERVFAIL")
		}
		lastErr = err
	}
	return nil, lastErr
}

func (p *Proxy) acquire(ctx context.Context) bool {
	select {
	case p.concurrent <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *Proxy) release() {
	<-p.concurrent
}

func configuredTransport(ctx context.Context, store *db.Store) (string, []string, error) {
	settings := map[string]string{}
	rows, err := store.DB.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN ('upstream_transport', 'upstream_dns')`)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return "", nil, err
		}
		settings[key] = value
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	transport := strings.TrimSpace(settings["upstream_transport"])
	if transport == "" {
		transport = "standard"
	}
	addresses := make([]string, 0)
	for _, raw := range strings.Split(settings["upstream_dns"], ",") {
		if value := strings.TrimSpace(raw); value != "" {
			addresses = append(addresses, value)
		}
	}
	return transport, addresses, nil
}

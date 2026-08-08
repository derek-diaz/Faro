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
	previous   *resolverState
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

func (proxy *Proxy) Reload(ctx context.Context) error {
	config, err := RuntimeConfigFromStore(ctx, proxy.store)
	if err != nil {
		return err
	}
	return proxy.ReloadConfig(config)
}

// ReloadConfig applies an already accepted runtime snapshot without reading
// the control-plane database. The standalone gateway uses this path so a
// failed API transaction cannot publish uncommitted provider settings.
func (proxy *Proxy) ReloadConfig(config RuntimeConfig) error {
	proxy.reloadMu.Lock()
	defer proxy.reloadMu.Unlock()
	transport, addresses, err := config.validate()
	if err != nil {
		return err
	}
	if transport != "encrypted" {
		proxy.previous = proxy.state.Load()
		proxy.state.Store(&resolverState{})
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
	proxy.previous = proxy.state.Load()
	proxy.state.Store(&resolverState{clients: clients})
	return nil
}

// RestorePrevious returns the gateway to the state it had before the most
// recent successful Reload. CoreDNS uses this when a staged configuration is
// rejected after the encrypted transport was prepared.
func (proxy *Proxy) RestorePrevious(_ context.Context) error {
	proxy.reloadMu.Lock()
	defer proxy.reloadMu.Unlock()
	proxy.state.Store(proxy.previous)
	return nil
}

func (proxy *Proxy) Start(ctx context.Context) error {
	if proxy.store == nil {
		return errors.New("encrypted DNS gateway has no configuration store")
	}
	config, err := RuntimeConfigFromStore(ctx, proxy.store)
	if err != nil {
		return err
	}
	return proxy.StartWithConfig(ctx, config)
}

func (proxy *Proxy) StartWithConfig(ctx context.Context, config RuntimeConfig) error {
	var startErr error
	proxy.serveOnce.Do(func() {
		if err := proxy.ReloadConfig(config); err != nil {
			startErr = err
			return
		}
		tcpListener, err := net.Listen("tcp", proxy.address)
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
		go proxy.serveUDP(ctx, udpConnection)
		go proxy.serveTCP(ctx, tcpListener)
	})
	return startErr
}

func (proxy *Proxy) serveUDP(ctx context.Context, connection *net.UDPConn) {
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
		go proxy.handleUDP(ctx, connection, client, query)
	}
}

func (proxy *Proxy) handleUDP(ctx context.Context, connection *net.UDPConn, client *net.UDPAddr, query []byte) {
	response := proxy.response(ctx, query)
	if len(response) == 0 {
		return
	}
	if _, err := connection.WriteToUDP(response, client); err != nil && ctx.Err() == nil {
		log.Printf("encrypted DNS UDP response failed: %v", err)
	}
}

func (proxy *Proxy) serveTCP(ctx context.Context, listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				log.Printf("encrypted DNS TCP gateway stopped: %v", err)
			}
			return
		}
		go proxy.handleTCP(ctx, connection)
	}
}

func (proxy *Proxy) handleTCP(ctx context.Context, connection net.Conn) {
	defer func() {
		if err := connection.Close(); err != nil && ctx.Err() == nil {
			log.Printf("encrypted DNS TCP connection close failed: %v", err)
		}
	}()
	for {
		if err := connection.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return
		}
		query, err := readTCPQuery(connection)
		if err != nil {
			return
		}
		if err := writeTCPResponse(connection, proxy.response(ctx, query)); err != nil {
			return
		}
	}
}

func readTCPQuery(connection net.Conn) ([]byte, error) {
	var length uint16
	if err := binary.Read(connection, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	if length < 12 {
		return nil, errors.New("encrypted DNS TCP query is too short")
	}
	query := make([]byte, int(length))
	_, err := io.ReadFull(connection, query)
	return query, err
}

func writeTCPResponse(connection net.Conn, response []byte) error {
	if len(response) == 0 || len(response) > maxDNSMessageBytes {
		return errors.New("encrypted DNS response is invalid")
	}
	if err := binary.Write(connection, binary.BigEndian, uint16(len(response))); err != nil {
		return err
	}
	_, err := connection.Write(response)
	return err
}

func (proxy *Proxy) response(ctx context.Context, query []byte) []byte {
	if !proxy.acquire(ctx) {
		return nil
	}
	defer proxy.release()
	queryCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	response, err := proxy.exchange(queryCtx, query)
	if err != nil {
		return serverFailure(query)
	}
	return response
}

func (proxy *Proxy) exchange(ctx context.Context, query []byte) ([]byte, error) {
	state := proxy.state.Load()
	if state == nil || len(state.clients) == 0 {
		return nil, errors.New("encrypted DNS has no configured providers")
	}
	start := int(proxy.next.Add(1)-1) % len(state.clients)
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

func (proxy *Proxy) acquire(ctx context.Context) bool {
	select {
	case proxy.concurrent <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (proxy *Proxy) release() {
	<-proxy.concurrent
}

func configuredTransport(ctx context.Context, store *db.Store) (transport string, addresses []string, err error) {
	settings := map[string]string{}
	rows, err := store.DB.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN ('upstream_transport', 'upstream_dns')`)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
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
	transport = strings.TrimSpace(settings["upstream_transport"])
	if transport == "" {
		transport = "standard"
	}
	addresses = make([]string, 0)
	for _, raw := range strings.Split(settings["upstream_dns"], ",") {
		if value := strings.TrimSpace(raw); value != "" {
			addresses = append(addresses, value)
		}
	}
	return transport, addresses, nil
}

package dohproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOverloadedUDPReturnsSERVFAILWithoutWaiting(t *testing.T) {
	proxy := New(nil, "")
	for i := 0; i < cap(proxy.concurrent); i++ {
		proxy.concurrent <- struct{}{}
	}
	socket, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { proxy.serveUDP(ctx, socket); close(done) }()
	client, err := net.DialUDP("udp", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	for i := 0; i < 20; i++ {
		if _, err := client.Write(probeQuery(uint16(i))); err != nil {
			t.Fatal(err)
		}
		response := make([]byte, 512)
		n, err := client.Read(response)
		if err != nil {
			t.Fatal(err)
		}
		if dnsResponseCode(response[:n]) != 2 {
			t.Fatal("overload did not return SERVFAIL")
		}
	}
	if len(proxy.concurrent) != cap(proxy.concurrent) {
		t.Fatal("overloaded packets consumed admission slots")
	}
	cancel()
	socket.Close()
	<-done
}

func TestCanceledRequestDoesNotAcquireSlot(t *testing.T) {
	proxy := New(nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if proxy.acquire(ctx) {
		t.Fatal("canceled request admitted")
	}
}

func TestSlowProviderLeavesTimeForFallback(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, _ := io.ReadAll(r.Body)
		query[2] |= 0x80
		w.Header().Set("Content-Type", dnsMessageContentType)
		w.Write(query)
	}))
	defer fast.Close()
	proxy := New(nil, "")
	proxy.state.Store(&resolverState{clients: []*endpointClient{
		{endpoint: Endpoint{URL: slow.URL}, client: slow.Client()},
		{endpoint: Endpoint{URL: fast.URL}, client: fast.Client()},
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	response, err := proxy.exchange(ctx, probeQuery(5))
	if err != nil {
		t.Fatal(err)
	}
	if dnsResponseCode(response) != 0 {
		t.Fatal("healthy fallback was not used")
	}
}

func TestTCPConnectionLimitAndCancellation(t *testing.T) {
	proxy := New(nil, "")
	proxy.tcpConnections = make(chan struct{}, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go proxy.serveTCP(ctx, listener)
	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	// Send a framed query and read the response to establish acceptance.
	if err := writeTCPResponse(first, probeQuery(1)); err != nil {
		t.Fatal(err)
	}
	first.SetDeadline(time.Now().Add(time.Second))
	if _, err := readTCPQuery(first); err != nil {
		t.Fatal(err)
	}
	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.SetDeadline(time.Now().Add(time.Second))
	_, err = second.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("excess connection remained usable")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("excess connection was queued instead of closed")
	}
	cancel()
	_, err = first.Read(make([]byte, 1))
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("cancellation did not close active TCP connection")
	}
}

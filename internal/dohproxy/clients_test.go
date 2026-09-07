package dohproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type closeTracker struct{ closes int }

func (tracker *closeTracker) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected request")
}
func (tracker *closeTracker) CloseIdleConnections() { tracker.closes++ }

func TestIdenticalReloadsReuseEncryptedClients(t *testing.T) {
	proxy := New(nil, "")
	config := RuntimeConfig{Transport: "encrypted", UpstreamDNS: "1.1.1.1"}
	if err := proxy.ReloadConfig(config); err != nil {
		t.Fatal(err)
	}
	first := proxy.state.Load().clients[0]
	defer first.client.CloseIdleConnections()
	for i := 0; i < 1000; i++ {
		config.Generation = time.Now().String()
		if err := proxy.ReloadConfig(config); err != nil {
			t.Fatal(err)
		}
		if proxy.state.Load().clients[0] != first {
			t.Fatal("unchanged provider created another transport")
		}
	}
	transport := first.client.Transport.(*http.Transport)
	if transport.IdleConnTimeout <= 0 || transport.MaxIdleConns <= 0 {
		t.Fatal("idle transports are not bounded")
	}
}

func trackedClient(address string) (*endpointClient, *closeTracker) {
	endpoint, _ := EndpointForAddress(address)
	tracker := &closeTracker{}
	return &endpointClient{endpoint: endpoint, client: &http.Client{Transport: tracker}}, tracker
}

func TestReloadRetainsRollbackClientAndRetiresOlderGeneration(t *testing.T) {
	proxy := New(nil, "")
	old, oldTracker := trackedClient("1.1.1.1")
	current, currentTracker := trackedClient("9.9.9.9")
	proxy.previous = &resolverState{clients: []*endpointClient{old}}
	proxy.state.Store(&resolverState{clients: []*endpointClient{current}})
	if err := proxy.ReloadConfig(RuntimeConfig{Transport: "encrypted", UpstreamDNS: "8.8.8.8"}); err != nil {
		t.Fatal(err)
	}
	defer closeUnusedClients(proxy.state.Load())
	if oldTracker.closes != 1 || currentTracker.closes != 0 {
		t.Fatalf("wrong retirement: old=%d rollback=%d", oldTracker.closes, currentTracker.closes)
	}
	if err := proxy.RestorePrevious(context.Background()); err != nil {
		t.Fatal(err)
	}
	if proxy.state.Load().clients[0] != current || currentTracker.closes != 0 {
		t.Fatal("rollback client was not preserved")
	}
}

func TestRollbackClosesFailedClientWithoutClosingSharedClient(t *testing.T) {
	proxy := New(nil, "")
	accepted, acceptedTracker := trackedClient("1.1.1.1")
	failed, failedTracker := trackedClient("9.9.9.9")
	proxy.previous = &resolverState{clients: []*endpointClient{accepted}}
	proxy.state.Store(&resolverState{clients: []*endpointClient{accepted, failed}})
	for i := 0; i < 2; i++ {
		if err := proxy.RestorePrevious(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if acceptedTracker.closes != 0 || failedTracker.closes != 1 {
		t.Fatalf("wrong rollback retirement: accepted=%d failed=%d", acceptedTracker.closes, failedTracker.closes)
	}
}

func TestRetiredGenerationsReleaseRealConnections(t *testing.T) {
	var opened, closed atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
		if state == http.StateClosed {
			closed.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	proxy := New(nil, "")
	for i := 0; i < 100; i++ {
		transport := &http.Transport{} // Deliberately no idle timeout: retirement must close it.
		client := &http.Client{Transport: transport, Timeout: time.Second}
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		proxy.replaceState(&resolverState{clients: []*endpointClient{{client: client}}})
	}
	proxy.replaceState(&resolverState{})
	proxy.replaceState(&resolverState{})
	deadline := time.Now().Add(2 * time.Second)
	for closed.Load() != opened.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if opened.Load() != 100 || closed.Load() != 100 {
		t.Fatalf("connections leaked: opened=%d closed=%d", opened.Load(), closed.Load())
	}
}

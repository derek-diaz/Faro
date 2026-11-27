package redundancy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrimaryObservesReplicaLagDNSAndRecovery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeRedundancyFiles(t, dir)
	primary := NewManager(openRedundancyStore(t), nil, dir, filepath.Join(t.TempDir(), "key"))
	applier := &recordingApplier{}
	replica := NewManager(openRedundancyStore(t), applier, t.TempDir(), filepath.Join(t.TempDir(), "key"))
	var clock atomic.Int64
	clock.Store(time.Now().Unix())
	primary.now = func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	replica.now = func() time.Time { return primary.now().Add(10 * time.Second) }

	pairing, err := primary.StartPairing(ctx, "Primary")
	if err != nil {
		t.Fatal(err)
	}
	var loseAck atomic.Bool
	loseAck.Store(true)
	protocol := controllerProtocolHandler(primary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/redundancy/replica/ack" && loseAck.Swap(false) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		protocol.ServeHTTP(w, r)
	}))
	defer server.Close()
	status := func() Status {
		t.Helper()
		s, err := primary.Status(ctx)
		if err != nil || len(s.Nodes) != 2 {
			t.Fatalf("read status: %+v, %v", s, err)
		}
		return s
	}
	syncOK := func() {
		t.Helper()
		if err := replica.syncReplica(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := replica.Join(ctx, JoinInput{ControllerURL: server.URL, PairingCode: pairing.Code, NodeName: "Kitchen", LANAddress: "192.168.1.12"}); err != nil {
		t.Fatal(err)
	}
	if node := status().Nodes[1]; node.ConfigRevision != 0 || node.RevisionsBehind != 1 || node.DNSStatus != "unreported" {
		t.Fatalf("lost ack not visible: %+v", node)
	}

	exporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		count := 42
		if r.URL.Path == "/replica" {
			count = 17
		}
		fmt.Fprintf(w, "coredns_dns_requests_total %d\ncoredns_dns_responses_total{rcode=\"SERVFAIL\"} 2\n", count)
	}))
	defer exporter.Close()
	primary.sampleDNS(ctx, exporter.URL)
	replica.sampleDNS(ctx, exporter.URL+"/replica")
	clock.Add(5)
	syncOK() // Unchanged snapshot repairs the missing acknowledgement.
	s := status()
	node := s.Nodes[1]
	if !s.Healthy || node.ConfigRevision != 1 || node.RevisionsBehind != 0 || node.DNS.Totals.Requests != 17 ||
		s.Nodes[0].DNS.Totals.Requests != 42 || !node.DNS.CheckedAt.Equal(primary.now().Add(-5*time.Second)) {
		t.Fatalf("unexpected current status: %+v", s)
	}
	// A later update must not restart the timer for an outstanding revision gap.
	primary.ConfigurationApplied(ctx)
	clock.Add(12)
	primary.ConfigurationApplied(ctx)
	applier.err = errors.New("configuration rejected")
	if err := replica.syncReplica(ctx); err == nil {
		t.Fatal("expected failed update")
	}
	node = status().Nodes[1]
	if !node.Online || node.LastError == "" || node.ConfigRevision != 1 || node.RevisionsBehind != 2 || node.BehindSeconds != 12 {
		t.Fatalf("failed update misreported: %+v", node)
	}
	clock.Add(60)
	node = status().Nodes[1]
	if node.Online || node.DNSStatus != "stale" || node.DNS.Totals.Requests != 17 || node.BehindSeconds != 72 {
		t.Fatalf("missing stale warning: %+v", node)
	}
	restarted := NewManager(primary.store, nil, dir, primary.secretKeyPath)
	restarted.now = primary.now
	afterRestart, err := restarted.Status(ctx)
	if err != nil || afterRestart.Nodes[1].BehindSeconds != 72 || afterRestart.Nodes[1].DNSStatus != "stale" {
		t.Fatalf("lost last report or lag on restart: %+v, %v", afterRestart, err)
	}
	applier.err = nil
	replica.sampleDNS(ctx, exporter.URL+"/down")
	syncOK()
	node = status().Nodes[1]
	if !node.Online || node.LastError != "" || node.RevisionsBehind != 0 || node.DNSStatus != "unavailable" || node.DNS.Totals != nil {
		t.Fatalf("DNS availability conflated with sync: %+v", node)
	}
	primary.sampleDNS(ctx, exporter.URL)
	replica.sampleDNS(ctx, exporter.URL+"/replica")
	syncOK()
	if !status().Healthy {
		t.Fatal("health did not recover")
	}
	// A failed sync can still confirm a prior update whose acknowledgement was lost.
	loseAck.Store(true)
	primary.ConfigurationApplied(ctx)
	syncOK()
	primary.ConfigurationApplied(ctx)
	applier.err = errors.New("new configuration rejected")
	if err := replica.syncReplica(ctx); err == nil {
		t.Fatal("expected failed update after a lost acknowledgement")
	}
	node = status().Nodes[1]
	if node.ConfigRevision != 4 || node.RevisionsBehind != 1 || node.LastError == "" {
		t.Fatalf("missed the last applied revision: %+v", node)
	}
	// Older replicas reported the attempted revision on failure.
	if err := primary.RecordAcknowledgement(ctx, node.NodeID, SyncAck{Revision: 5, Error: "could not apply"}); err != nil {
		t.Fatal(err)
	}
	if node := status().Nodes[1]; node.ConfigRevision != 4 || node.RevisionsBehind != 1 {
		t.Fatalf("accepted an unsuccessful legacy update: %+v", node)
	}
}

func TestDNSRateRequiresConsecutiveSamplesFromSameProcess(t *testing.T) {
	m := &Manager{}
	now := time.Now()
	m.now = func() time.Time { return now }
	var count, start atomic.Int64
	start.Store(111)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "process_start_time_seconds %d\ncoredns_dns_requests_total %d\n", start.Load(), count.Load())
	}))
	defer server.Close()
	sample := func(queries int64, gap time.Duration) *float64 {
		t.Helper()
		now = now.Add(gap)
		count.Store(queries)
		m.sampleDNS(context.Background(), server.URL)
		return m.latestDNS().Totals.RequestsPerSecond
	}
	if rate := sample(100, 0); rate != nil {
		t.Fatalf("initial rate = %v", *rate)
	}
	if rate := sample(130, dnsSampleInterval); rate == nil || *rate != 2 {
		t.Fatalf("rate = %v", rate)
	}
	if rate := sample(130, dnsSampleInterval); rate == nil || *rate != 0 {
		t.Fatalf("idle rate = %v", rate)
	}
	start.Store(222)
	if rate := sample(180, dnsSampleInterval); rate != nil {
		t.Fatalf("rate crossed a process restart")
	}
	if rate := sample(300, time.Minute); rate != nil {
		t.Fatalf("rate spanned a sample outage")
	}
}

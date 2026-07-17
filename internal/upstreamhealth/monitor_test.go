package upstreamhealth

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
)

func TestMonitorSummarizesConfiguredUpstreamHealth(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`UPDATE settings SET value = '1.1.1.1,9.9.9.9' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}

	probe := func(_ context.Context, address string) Probe {
		result := Probe{Address: address, Status: "unavailable", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
		if address == "1.1.1.1" {
			latency := 12.5
			result.Status = "online"
			result.LatencyMS = &latency
		}
		return result
	}
	monitor := NewMonitor(store, time.Minute, probe)
	snapshot := monitor.CheckNow(context.Background())
	if snapshot.Status != "degraded" || len(snapshot.Items) != 2 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.Items[0].Status != "online" || snapshot.Items[1].Status != "unavailable" {
		t.Fatalf("unexpected probe results: %#v", snapshot.Items)
	}

	snapshot.Items[0].Status = "changed"
	if monitor.Snapshot().Items[0].Status != "online" {
		t.Fatal("Snapshot returned mutable monitor state")
	}
}

func TestDNSProbeQuery(t *testing.T) {
	const id = uint16(0x4a3c)
	packet := dnsProbeQuery(id, "example.com")
	if len(packet) < 12 {
		t.Fatalf("packet too short: %d", len(packet))
	}
	if got := binary.BigEndian.Uint16(packet[:2]); got != id {
		t.Fatalf("query id = %#x, want %#x", got, id)
	}
	if got := binary.BigEndian.Uint16(packet[4:6]); got != 1 {
		t.Fatalf("question count = %d, want 1", got)
	}
}

func TestMonitorRunsImmediatelyAndRespondsToTrigger(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`UPDATE settings SET value = '1.1.1.1' WHERE key = 'upstream_dns'`); err != nil {
		t.Fatal(err)
	}

	calls := make(chan struct{}, 2)
	probe := func(_ context.Context, address string) Probe {
		calls <- struct{}{}
		latency := 5.0
		return Probe{Address: address, Status: "online", LatencyMS: &latency, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	monitor := NewMonitor(store, time.Hour, probe)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitor.Run(ctx)

	waitForProbe(t, calls)
	monitor.Trigger()
	waitForProbe(t, calls)
}

func waitForProbe(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream health probe")
	}
}

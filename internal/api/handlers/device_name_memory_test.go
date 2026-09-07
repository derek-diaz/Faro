package handlers

import (
	"fmt"
	"testing"
	"time"
)

func TestDeviceNameCacheRemainsBoundedDuringAddressChurn(t *testing.T) {
	resolver := newDeviceNameResolver()
	for i := 0; i < maxDeviceNameEntries*3; i++ {
		resolver.store(fmt.Sprintf("address-%d", i), "device")
	}
	if len(resolver.entries) != maxDeviceNameEntries {
		t.Fatalf("unbounded names: %d", len(resolver.entries))
	}
	last := fmt.Sprintf("address-%d", maxDeviceNameEntries*3-1)
	if name, ok := resolver.cached(last); !ok || name != "device" {
		t.Fatal("newest lookup was not cached")
	}
	// Expired addresses must be reclaimed without looking each address up again.
	for ip, entry := range resolver.entries {
		entry.expiresAt = time.Now().Add(-time.Second)
		resolver.entries[ip] = entry
	}
	resolver.store("new-address", "new device")
	if len(resolver.entries) != 1 {
		t.Fatalf("expired names retained: %d", len(resolver.entries))
	}
}

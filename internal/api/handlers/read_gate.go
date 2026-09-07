package handlers

import (
	"context"
	"errors"
	"sync"
)

// readGate coalesces cache misses without keeping canceled requests waiting
// for a mutex or launching detached database work. Callers recheck their cache
// after acquiring a key. Unrelated keys can still proceed concurrently.
type readGate struct {
	mu     sync.Mutex
	active map[string]chan struct{}
}

func (gate *readGate) acquire(ctx context.Context, key string) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		gate.mu.Lock()
		wait, busy := gate.active[key]
		if !busy {
			if len(gate.active) >= 32 {
				gate.mu.Unlock()
				return nil, errors.New("too many concurrent summary requests")
			}
			if gate.active == nil {
				gate.active = make(map[string]chan struct{})
			}
			wait = make(chan struct{})
			gate.active[key] = wait
			gate.mu.Unlock()
			return func() {
				gate.mu.Lock()
				delete(gate.active, key)
				close(wait)
				gate.mu.Unlock()
			}, nil
		}
		gate.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

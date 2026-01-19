package executer

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type KeyedGater struct {
	mu    sync.Mutex
	gates map[string]chan struct{}
}

func NewKeyedGater() *KeyedGater {
	return &KeyedGater{
		gates: make(map[string]chan struct{}),
	}
}


// Acquire attempts to reserve a slot for a specific key with a custom timeout.
// key: The identifier (e.g., VolumeID or "global-udev-lock")
// maxRuns: Max concurrency for this specific key
// timeout: How long to wait for a free slot
func (g *KeyedGater) Acquire(key string, maxRuns int, timeout time.Duration) error {
	g.mu.Lock()
	if g.gates == nil {
		g.gates = make(map[string]chan struct{})
	}
	ch, exists := g.gates[key]
	if !exists {
		ch = make(chan struct{}, maxRuns)
		g.gates[key] = ch
	}
	g.mu.Unlock()

	// In 2026, use a Timer instead of context.WithTimeout if you want to 
	// reduce garbage collector pressure for high-frequency locks.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case ch <- struct{}{}:
		return nil
	case <-timer.C:
		return fmt.Errorf("gater: timeout (%v) key=%s", timeout, key)
	}
}

func (g *KeyedGater) Release(key string) {
	g.mu.RLock() // Use RLock to allow concurrent releases
	ch, exists := g.gates[key]
	g.mu.RUnlock()

	if exists {
		select {
		case <-ch:
		default:
			// Safety: Prevent blocking if Release is called wrongly
		}
	}
}

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
	// 1. Thread-safe initialization of the specific gate
	g.mu.Lock()
	ch, exists := g.gates[key]
	if !exists {
		ch = make(chan struct{}, maxRuns)
		g.gates[key] = ch
	}
	g.mu.Unlock()

	// 2. Create a context for the wait
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 3. The Wait
	select {
	case ch <- struct{}{}:
		// Successfully acquired slot
		return nil
	case <-ctx.Done():
		// Wait timed out or context was cancelled
		return fmt.Errorf("gater: timeout (%v) waiting for slot: key=%s, limit=%d", timeout, key, maxRuns)
	}
}

// Release frees a slot for the specific key.
func (g *KeyedGater) Release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if ch, exists := g.gates[key]; exists {
		select {
		case <-ch:
			// Slot successfully released
		default:
			// Safety: Release called without an active Acquire
		}

		// Optional: Cleanup empty channels to save memory
		if len(ch) == 0 {
			delete(g.gates, key)
		}
	}
}

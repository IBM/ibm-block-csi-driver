package executer

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type KeyedGater struct {
	mu    sync.RWMutex
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

	// Use time.NewTimer to minimize GC pressure on high-frequency calls
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
	g.mu.RLock() // Allows multiple goroutines to release different keys simultaneously
	ch, exists := g.gates[key]
	g.mu.RUnlock()

	if exists {
		select {
		case <-ch:
		default:
			// Safety: prevents a panic if Release is called without a matching Acquire
		}
	}
}

package executer

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type gate struct {
	ch      chan struct{}
	refCount int // Track active acquires to determine when to delete the key
}

type KeyedGater struct {
	mu    sync.Mutex
	gates map[string]*gate
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
        g.gates = make(map[string]*gate)
    }

    gt, exists := g.gates[key]
    if !exists {
        gt = &gate{ch: make(chan struct{}, maxRuns)}
        g.gates[key] = gt
    }
    gt.refCount++ // Increment before unlocking
    g.mu.Unlock()

    // Using context for timeout is idiomatic in 2026
    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    select {
    case gt.ch <- struct{}{}:
        return nil
    case <-ctx.Done():
        // Cleanup: decrement refCount if we timeout
        g.decrementRef(key)
        return fmt.Errorf("gater: timeout (%v) key=%s", timeout, key)
    }
}

func (g *KeyedGater) Release(key string) {
    g.mu.Lock()
    gt, exists := g.gates[key]
    g.mu.Unlock()

    if exists {
        select {
        case <-gt.ch:
            // Successfully released a slot
        default:
            // Safety: Avoid panic if Release is called extra times
        }
        g.decrementRef(key)
    }
}

func (g *KeyedGater) decrementRef(key string) {
    g.mu.Lock()
    defer g.mu.Unlock()
    if gt, ok := g.gates[key]; ok {
        gt.refCount--
        if gt.refCount <= 0 {
            delete(g.gates, key) // Reclaims memory
        }
    }
}

package executer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type ResourcePool struct {
	running   chan struct{}
	spare     chan struct{}
	activeOps atomic.Int32
}

type gate struct {
	ch      chan struct{}
	refCount int // Track active acquires to determine when to delete the key
}

type KeyedGater struct {
	mu    sync.Mutex
	gates map[string]*gate
	resources sync.Map // Map[string]*ResourcePool
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


// ExecuteInterruptible is for workers that handle their own context/timeouts (e.g., Socket with Deadlines)
func (g *KeyedGater) ExecuteInterruptible(
	resourceName string,
	maxRunning, maxSpare int,
	handoffTimeout time.Duration,
	worker func(ctx context.Context) error,
) error {
	return g.baseExecute(resourceName, maxRunning, maxSpare, handoffTimeout, false, 0, func(ctx context.Context) error {
		return worker(ctx)
	})
}

// ExecuteUninterruptible is for synchronous syscalls/ioctls that might hang in D-state.
// It wraps the worker in a goroutine and provides an abandonment signal.
func (g *KeyedGater) ExecuteUninterruptible(
	resourceName string,
	maxRunning, maxSpare int,
	handoffTimeout time.Duration,
	hardTimeout time.Duration,
	worker func() error,
) error {
	return g.baseExecute(resourceName, maxRunning, maxSpare, handoffTimeout, true, hardTimeout, func(ctx context.Context) error {
		return worker()
	})
}

func (s *SemaphoreManager) baseExecute(
	resourceName string,
	maxRunning, maxSpare int,
	handoffTimeout time.Duration,
	isSync bool,
	hardTimeout time.Duration,
	task func(ctx context.Context) error,
) error {
	val, _ := g.resources.LoadOrStore(resourceName, &ResourcePool{
		running: make(chan struct{}, maxRunning),
		spare:   make(chan struct{}, maxSpare),
	})
	pool := val.(*ResourcePool)

	// 1. Acquire Running Slot
	select {
	case pool.running <- struct{}{}:
	case <-time.After(30 * time.Second):
		return fmt.Errorf("resource %s: queue congestion", resourceName)
	}

	pool.activeOps.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	switched := make(chan struct{})
	var once sync.Once

	// 2. Worker Launch
	go func() {
		defer pool.activeOps.Add(-1)
		var err error
		if isSync {
			err = task(nil) // Block until kernel returns
		} else {
			err = task(ctx) // Respects context
		}
		done <- err

		// Release logic
		once.Do(func() {
			select {
			case <-switched:
				<-pool.spare
			default:
				<-pool.running
			}
		})
	}()

	// 3. Monitor Handoff & Hard Timeout
	timer := time.NewTimer(handoffTimeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		// Attempt Handoff to Spare Pool
		select {
		case pool.spare <- struct{}{}:
			once.Do(func() {
				close(switched)
				<-pool.running // Free slot for healthy ops
			})

			// If it's a sync/uninterruptible worker, wait for hardTimeout
			if isSync && hardTimeout > 0 {
				select {
				case err := <-done:
					return err
				case <-time.After(hardTimeout):
					return fmt.Errorf("resource %s: abandoned after hard timeout (%v)", resourceName, hardTimeout)
				}
			}
			return fmt.Errorf("resource %s: handoff to spare pool after %v", resourceName, handoffTimeout)
		default:
			// Spare pool is full
			return fmt.Errorf("resource %s: critical saturation (spare pool full)", resourceName)
		}
	}
}


//data, err := sm.ExecuteUninterruptible(...)
//if err != nil {
//    // If err is 'handoff' or 'timeout', DO NOT TRUST 'data'.
//    // The worker might still be writing to it in the background.
//    return nil, err
//}
//return data, nil

package executer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type ResourcePool struct {
	running chan struct{}
	spare   chan struct{}
	// Use sync.Once to ensure channels are initialized exactly once
	// even with concurrent LoadOrStore calls.
	once    sync.Once
	activeOps atomic.Int32
}

func (p *ResourcePool) init(maxRunning, maxSpare int) {
	p.once.Do(func() {
		p.running = make(chan struct{}, maxRunning)
		p.spare = make(chan struct{}, maxSpare)
	})
}

type gate struct {
	ch      chan struct{}
	refCount int // Track active acquires to determine when to delete the key
}

type KeyedGater struct {
	// Keyed semaphore Acquire/Release
	mu    sync.Mutex
	gates map[string]*gate

	// Time limited worker functions
	resources sync.Map // Map[string]*ResourcePool
}


func NewKeyedGater() *KeyedGater {
	return &KeyedGater{
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
//Usage example:
//data, err := sm.ExecuteUninterruptible(...)
//if err != nil {
//    // If err is 'handoff' or 'timeout', DO NOT TRUST 'data'.
//    // The worker might still be writing to it in the background.
//    return nil, err
//}
//return data, nil
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

func (g *KeyedGater) baseExecute(
	resourceName string,
	maxRunning, maxSpare int,
	handoffTimeout time.Duration,
	isSync bool,
	hardTimeout time.Duration,
	task func(ctx context.Context) error,
) error {
	// 1. ATOMIC INITIALIZATION
	// We load or store a pointer, then initialize its internal channels safely.
	val, _ := g.resources.LoadOrStore(resourceName, &ResourcePool{})
	pool := val.(*ResourcePool)
	pool.init(maxRunning, maxSpare)

	// 2. ACQUIRE RUNNING SLOT (Memory-safe timer)
	// Replaced time.After with NewTimer to prevent heap bloat during bursts.
	qTimer := time.NewTimer(30 * time.Second)
	defer qTimer.Stop()

	select {
	case pool.running <- struct{}{}:
	case <-qTimer.C:
		return fmt.Errorf("resource %s: queue congestion (30s limit reached)", resourceName)
	}

	pool.activeOps.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	switched := make(chan struct{})
	var once sync.Once

	// 3. WORKER LAUNCH
	go func() {
		defer pool.activeOps.Add(-1)
		var err error
		if isSync {
			err = task(nil) // Block on syscall (D-state possible)
		} else {
			err = task(ctx) // Interruptible via context
		}
		done <- err

		// Release logic: ensure we release from the correct pool
		once.Do(func() {
			select {
			case <-switched:
				<-pool.spare
			default:
				<-pool.running
			}
		})
	}()

	// 4. MONITOR HANDOFF & HARD TIMEOUT
	hTimer := time.NewTimer(handoffTimeout)
	defer hTimer.Stop()

	select {
	case err := <-done:
		return err
	case <-hTimer.C:
		// Attempt Handoff to Spare Pool to free up 'running' for healthy volumes
		select {
		case pool.spare <- struct{}{}:
			once.Do(func() {
				close(switched)
				<-pool.running // Success: Slot returned to main pool
			})

			if isSync && hardTimeout > 0 {
				hdt := time.NewTimer(hardTimeout)
				defer hdt.Stop()
				select {
				case err := <-done:
					return err
				case <-hdt.C:
					// CRITICAL: Return error to CSI, but the goroutine and
					// spare slot remain occupied (leaked) until kernel returns.
					return fmt.Errorf("resource %s: abandoned (thread leaked in D-state) after %v", resourceName, hardTimeout)
				}
			}
			return fmt.Errorf("resource %s: handoff to spare pool after %v", resourceName, handoffTimeout)
		default:
			// Spare pool is full: The node is likely saturated with D-state threads
			return fmt.Errorf("resource %s: critical saturation (spare pool full)", resourceName)
		}
	}
}

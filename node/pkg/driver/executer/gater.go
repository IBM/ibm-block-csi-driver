package executer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type semaphoreGate struct {
	ch       chan struct{}
	refCount int // Track active acquires to determine when to delete the key
}

type KeyedGater struct {
	// Keyed semaphore Acquire/Release
	mu             sync.Mutex
	semaphoreGates map[string]*semaphoreGate

	resources    sync.Map // map[string]*ResourcePool
	globalLeaked atomic.Int64
	maxGlobal    int64
	lastWarnTime atomic.Int64
}

func NewKeyedGater(maxGlobalLeaks int64) *KeyedGater {
	return &KeyedGater{
		semaphoreGates: make(map[string]*semaphoreGate),
		maxGlobal:      maxGlobalLeaks,
	}
}

// Acquire attempts to reserve a slot for a specific key with a custom timeout.
// key: The identifier (e.g., VolumeID or "global-udev-lock")
// maxRuns: Max concurrency for this specific key
// timeout: How long to wait for a free slot
func (g *KeyedGater) Acquire(ctx context.Context, key string, maxRuns int, timeout time.Duration) error {
	g.mu.Lock()
	gt, exists := g.semaphoreGates[key]
	if !exists {
		gt = &semaphoreGate{ch: make(chan struct{}, maxRuns)}
		g.semaphoreGates[key] = gt
	}
	gt.refCount++
	g.mu.Unlock()

	// REQUIREMENT 8: Respect the CSI API context + local timeout
	// This ensures we stop waiting if either the request is canceled
	// OR we hit our local threshold.
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case gt.ch <- struct{}{}:
		return nil
	case <-waitCtx.Done():
		// Cleanup: must use the thread-safe Release fixed in Bug #1
		g.Release(key)

		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("gater: timeout (%v) key=%s", timeout, key)
		}
		return fmt.Errorf("gater: %w key=%s", waitCtx.Err(), key)
	}
}

func (g *KeyedGater) Release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	gt, exists := g.semaphoreGates[key]
	if !exists {
		return
	}

	// Attempt to drain a slot, but don't block if Release was
	// called due to an Acquire timeout before a slot was taken.
	select {
	case <-gt.ch:
	default:
	}

	gt.refCount--
	if gt.refCount <= 0 {
		delete(g.semaphoreGates, key)
	}
}

type Result[T any] struct {
	Data T
	Err  error
}

type ResourcePool struct {
	once      sync.Once
	running   chan struct{}
	spare     chan struct{}
	activeOps atomic.Int64
}

func (p *ResourcePool) init(maxRunning, maxSpare int) {
	p.once.Do(func() {
		p.running = make(chan struct{}, maxRunning)
		p.spare = make(chan struct{}, maxSpare)
	})
}

// suicideIfLeaked protects the Node from PID exhaustion.
func (g *KeyedGater) suicideIfLeaked() {
	leaks := g.globalLeaked.Load()
	if leaks <= 0 {
		return
	}

	// 1. FATAL EXIT
	if leaks >= g.maxGlobal {
		fmt.Fprintf(os.Stderr, "FATAL: Global thread leak limit (%d) reached. Terminating to protect Node.\n", g.maxGlobal)
		time.Sleep(500 * time.Millisecond)
		os.Exit(1)
	}

	// 2. THROTTLED WARNING (Every 30s)
	if leaks > (g.maxGlobal / 2) {
		now := time.Now().UnixNano()
		last := g.lastWarnTime.Load()
		if now-last > int64(30*time.Second) {
			if g.lastWarnTime.CompareAndSwap(last, now) {
				// Replace with your actual logger
				fmt.Printf("WARNING: High thread leak detected (%d/%d).\n", leaks, g.maxGlobal)
			}
		}
	}
}

// ExecuteUninterruptible handles tasks that might hang in D-state (kernel).
func ExecuteUninterruptible[T any](
	ctx context.Context,
	g *KeyedGater,
	resourceName string,
	maxRunning, maxSpare int,
	handoffTimeout time.Duration,
	hardTimeout time.Duration,
	worker func(ctx context.Context) (T, error),
) (T, error) {
	// This delegates to the generic logic
	return baseExecute(ctx, g, resourceName, maxRunning, maxSpare, handoffTimeout, hardTimeout, worker)
}

func baseExecute[T any](
	ctx context.Context, // Requirement 8
	g *KeyedGater,
	resourceName string,
	maxRunning, maxSpare int,
	handoffTimeout time.Duration,
	hardTimeout time.Duration,
	worker func(ctx context.Context) (T, error),
) (T, error) {
	g.suicideIfLeaked()

	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}

	// 1. ATOMIC INITIALIZATION
	val, _ := g.resources.LoadOrStore(resourceName, &ResourcePool{})
	pool := val.(*ResourcePool)
	pool.init(maxRunning, maxSpare)

	// 2. Queue for slot (Respecting CSI Context)
	select {
	case pool.running <- struct{}{}:
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-time.After(30 * time.Second):
		var zero T
		return zero, fmt.Errorf("queue congestion")
	}

	pool.activeOps.Add(1)
	done := make(chan Result[T], 1)
	switched := make(chan struct{})
	var once sync.Once

	// Create a context to signal the worker to stop if we timeout
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

	// 3. WORKER LAUNCH
	go func() {
		defer pool.activeOps.Add(-1)

		// The task should check workerCtx.Err() to be "cooperative"
		data, err := worker(workerCtx)
		done <- Result[T]{Data: data, Err: err}

		once.Do(func() {
			select {
			case <-switched:
				<-pool.spare
				g.globalLeaked.Add(-1) // Recovered
			default:
				<-pool.running
			}
		})
	}()

	// 4. MONITOR HANDOFF & HARD TIMEOUT
	hTimer := time.NewTimer(handoffTimeout)
	defer hTimer.Stop()

	select {
	case res := <-done:
		return res.Data, res.Err
	case <-hTimer.C:
		select {
		case pool.spare <- struct{}{}:
			once.Do(func() {
				close(switched)
				<-pool.running
			})

			if hardTimeout <= 0 {
				res := <-done
				return res.Data, res.Err
			}

			hdt := time.NewTimer(hardTimeout)
			defer hdt.Stop()

			select {
			case res := <-done:
				return res.Data, res.Err
			case <-hdt.C: // Use the timer channel directly
				g.globalLeaked.Add(1)
				var zero T
				return zero, fmt.Errorf("resource %s: abandoned after hard timeout %v", resourceName, hardTimeout)
			}
		default:
			var zero T
			return zero, fmt.Errorf("resource %s: critical saturation (spare pool full)", resourceName)
		}
	}
}

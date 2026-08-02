package executer

import (
	"github.com/ibm/ibm-block-csi-driver/node/goid_info"
	"github.com/ibm/ibm-block-csi-driver/node/util"
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

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case gt.ch <- struct{}{}:
		return nil
	case <-waitCtx.Done():
		g.cleanupFailedAcquire(key)
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			if ctx.Err() != nil {
				return fmt.Errorf("gater: API context canceled/expired key=%s: %w", key, ctx.Err())
			}
			return fmt.Errorf("gater: local operation timeout (%v) key=%s", timeout, key)
		}
		return fmt.Errorf("gater: %w key=%s", waitCtx.Err(), key)
	}
}


func (g *KeyedGater) cleanupFailedAcquire(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	gt, exists := g.semaphoreGates[key]
	if !exists {
		return
	}

	gt.refCount--
	if gt.refCount <= 0 {
		delete(g.semaphoreGates, key)
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


// Execute Binds acquisition, execution, and automatic release into a single method call.
// The caller provides the business logic via the 'action' callback.
func (g *KeyedGater) Execute(ctx context.Context, key string, maxRuns int, timeout time.Duration, action func() error) error {
	// 1. Acquire the slot
	if err := g.Acquire(ctx, key, maxRuns, timeout); err != nil {
		return err
	}
	
	// 2. Ensure release ALWAYS runs after the action completes, completely hidden from the caller
	defer g.Release(key)

	// 3. Run the caller's business logic
	return action()
}

func (g *KeyedGater) ExecuteicsiFabric(ctx context.Context, action func() error) error {
	return g.Execute(ctx, "iscsi-fabric-ops", 1, 30*time.Second, action)
}

func (g *KeyedGater) ExecuteNvmeFabric(ctx context.Context, action func() error) error {
	return g.Execute(ctx, "nvme-fabric-ops", 2, 30*time.Second, action)
}

func (g *KeyedGater) ExecuteFcScsiFabric(ctx context.Context, action func() error) error {
	return g.Execute(ctx, "fc-scsi-fabric-ops", 2, 15*time.Second, action)
}

func (g *KeyedGater) ExecuteNodeFs(ctx context.Context, action func() error) error {
	return g.Execute(ctx, "node-fs", 2, 60*time.Second, action)
}

func (g *KeyedGater) ExecutePathTeardown(ctx context.Context, action func() error) error {
	return g.Execute(ctx, "path-teardown-ops", 2, 5*time.Second, action)
}

func (g *KeyedGater) ExecuteMultipathd(ctx context.Context, action func() error) error {
	return g.Execute(ctx, "multipathd-socket", 1, 10*time.Second, action)
}

func (g *KeyedGater) ExecuteTopologyReads(ctx context.Context, action func() error) error {
	return g.Execute(ctx, "topology-reads", 4, 5*time.Second, action)
}


type Result[T any] struct {
	Data T
	Err  error
}

// ResourcePool manages concurrency tokens for a single resource.
type ResourcePool struct {
	running    chan struct{}
	spare      chan struct{}
	activeOps  atomic.Int64
	initOnce   sync.Once
}

// Init guarantees the pools channels are created exactly once safely.
func (p *ResourcePool) Init(maxRunning, maxSpare int) {
	p.initOnce.Do(func() {
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
	ctx context.Context, 
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

	// 1. SAFE ATOMIC INITIALIZATION
	val, _ := g.resources.LoadOrStore(resourceName, &ResourcePool{})
	pool := val.(*ResourcePool)
	pool.Init(maxRunning, maxSpare) // Safe once-execution
	
	// 2. Queue for slot respecting CSI Context & Deadlines
	select {
	case pool.running <- struct{}{}:
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
	
	pool.activeOps.Add(1)
	
	// FIXED: Buffer capacity must handle the "abandoned" path to prevent goroutine leak
	done := make(chan Result[T], 1) 
	switched := make(chan struct{})
	var once sync.Once

	// FIXED: Link the worker context lifecycle to the incoming RPC context
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()

	parentAdditionalID, _ := goid_info.GetAdditionalIDInfo()

	// 3. WORKER LAUNCH
	go func() {
		defer pool.activeOps.Add(-1)

		if parentAdditionalID != "" && parentAdditionalID != "-" {
			// goid_info.SetAdditionalIDInfo(parentAdditionalID)
			_ = parentAdditionalID 
		}

		data, err := worker(workerCtx)
		
		// This write will never block now, even if baseExecute has already returned
		done <- Result[T]{Data: data, Err: err}

		once.Do(func() {
			select {
			case <-switched:
				<-pool.spare
				g.globalLeaked.Add(-1) 
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

			hdTimer := time.NewTimer(hardTimeout)
			defer hdTimer.Stop()

			select {
			case res := <-done:
				return res.Data, res.Err
			case <-hdTimer.C: 
				g.globalLeaked.Add(1)
				var zero T
				// We return immediately; the worker goroutine will exit cleanly when done 
				// without hanging because the 'done' channel is buffered.
				return zero, fmt.Errorf("resource %s: abandoned after hard timeout %v", resourceName, hardTimeout)
			}
		default:
			// If spare pool is full, we must free the token we are holding before erroring out
			once.Do(func() {
				<-pool.running
			})
			var zero T
			return zero, fmt.Errorf("resource %s: critical saturation (spare pool full)", resourceName)
		}
	}
}

// BatchResult wraps the output for an indexed batch worker.
type BatchResult[T any] struct {
	Index int
	Data  T
	Err   error
}

func ExecuteUninterruptibleBatch[Param any, T any](
	ctx context.Context,
	g *KeyedGater,
	resourceName string,
	maxRunning, maxSpare int,
	handoffTimeout time.Duration,
	hardTimeout time.Duration,
	parameters []Param,
	worker func(ctx context.Context, index int, p Param, cancelBatch func()) (T, error),
) ([]BatchResult[T], error) {
	g.suicideIfLeaked()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(parameters) == 0 {
		return nil, nil
	}

	// 1. SAFE ATOMIC INITIALIZATION
	val, _ := g.resources.LoadOrStore(resourceName, &ResourcePool{})
	pool := val.(*ResourcePool)
	pool.Init(maxRunning, maxSpare) // Thread-safe sync.Once setup

	// Create a unified batch context that allows sibling cancellation
	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()

	parentAdditionalID, _ := goid_info.GetAdditionalIDInfo()

	resultsChan := make(chan BatchResult[T], len(parameters))
	var wg sync.WaitGroup

	for idx, param := range parameters {
		wg.Add(1)
		go func(index int, p Param) {
			defer wg.Done()

			// 2. Queue for slot respecting the active batch context
			select {
			case pool.running <- struct{}{}:
			case <-batchCtx.Done():
				resultsChan <- BatchResult[T]{Index: index, Err: batchCtx.Err()}
				return
			}

			pool.activeOps.Add(1)
			done := make(chan Result[T], 1) // Safe 1-capacity buffer
			switched := make(chan struct{})
			var once sync.Once

			// FIXED: Derive from batchCtx so cancelBatch() immediately cancels other workers
			workerCtx, cancelWorker := context.WithCancel(batchCtx)
			defer cancelWorker()

			// 3. INNER WORKER LAUNCH
			go func() {
				defer pool.activeOps.Add(-1)

				if parentAdditionalID != "" && parentAdditionalID != "-" {
					// goid_info.SetAdditionalIDInfo(parentAdditionalID)
					_ = parentAdditionalID 
				}

				data, err := worker(workerCtx, index, p, cancelBatch)
				
				// Guaranteed never to block even if the parent monitor times out
				done <- Result[T]{Data: data, Err: err}

				once.Do(func() {
					select {
					case <-switched:
						<-pool.spare
						g.globalLeaked.Add(-1)
					default:
						<-pool.running
					}
				})
			}()

			// 4. MONITOR HANDOFF & HARD TIMEOUT FOR THIS ELEMENT
			hTimer := time.NewTimer(handoffTimeout)
			defer hTimer.Stop()

			select {
			case res := <-done:
				resultsChan <- BatchResult[T]{Index: index, Data: res.Data, Err: res.Err}
			case <-hTimer.C:
				select {
				case pool.spare <- struct{}{}:
					once.Do(func() {
						close(switched)
						<-pool.running
					})

					if hardTimeout <= 0 {
						res := <-done
						resultsChan <- BatchResult[T]{Index: index, Data: res.Data, Err: res.Err}
						return
					}

					hdTimer := time.NewTimer(hardTimeout)
					defer hdTimer.Stop()

					select {
					case res := <-done:
						resultsChan <- BatchResult[T]{Index: index, Data: res.Data, Err: res.Err}
					case <-hdTimer.C:
						g.globalLeaked.Add(1)
						resultsChan <- BatchResult[T]{
							Index: index,
							Err:   fmt.Errorf("batch item %d abandoned after hard timeout %v", index, hardTimeout),
						}
					}
				default:
					// FIXED: Must release token if exiting due to saturation to prevent permanent deadlock
					once.Do(func() {
						<-pool.running
					})
					resultsChan <- BatchResult[T]{Index: index, Err: fmt.Errorf("batch item %d: critical saturation", index)}
				}
			}
		}(idx, param)
	}

	wg.Wait()
	close(resultsChan)

	// Collect aggregated data in execution order or collection order
	aggregatedResults := make([]BatchResult[T], 0, len(parameters))
	for res := range resultsChan {
		aggregatedResults = append(aggregatedResults, res)
	}

	return aggregatedResults, nil
}

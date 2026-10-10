package executer

import (
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/goid_info"
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

type singleflightCall[T any] struct {
	wg  sync.WaitGroup
	val T
	err error
}

type SingleflightGroup[T any] struct {
	mu sync.Mutex
	m  map[string]*singleflightCall[T]
}

func NewSingleflightGroup[T any]() *SingleflightGroup[T] {
	return &SingleflightGroup[T]{
		m: make(map[string]*singleflightCall[T]),
	}
}

// Do executes and returns the results of the given function, making
// sure that only one execution is in-flight for a given key at a time.
// If a duplicate comes in while an execution is in-flight, it waits for the
// original to complete and receives the same results with zero introduced delay.
// As soon as the in-flight execution completes, it is deleted immediately (no retention window).
func (g *SingleflightGroup[T]) Do(key string, fn func() (T, error)) (T, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*singleflightCall[T])
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}

	c := new(singleflightCall[T])
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err
}

type KeyedGater struct {
	// Keyed semaphore Acquire/Release
	mu             sync.Mutex
	semaphoreGates map[string]*semaphoreGate

	resMu           sync.Mutex
	resources       map[string]*ResourcePool
	DevSingleflight *SingleflightGroup[[]string]
	globalLeaked    atomic.Int64
	maxGlobal       int64
	lastWarnTime    atomic.Int64
}

func NewKeyedGater(maxGlobalLeaks int64) *KeyedGater {
	return &KeyedGater{
		semaphoreGates:  make(map[string]*semaphoreGate),
		resources:       make(map[string]*ResourcePool),
		DevSingleflight: NewSingleflightGroup[[]string](),
		maxGlobal:       maxGlobalLeaks,
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
	running   chan struct{}
	spare     chan struct{}
	activeOps atomic.Int64
	refCount  int // Number of operations currently holding or waiting on this pool
}

// getOrCreatePool retrieves or constructs a ResourcePool for resourceName with proper ref counting.
func (g *KeyedGater) getOrCreatePool(resourceName string, maxRunning, maxSpare int) *ResourcePool {
	g.resMu.Lock()
	defer g.resMu.Unlock()

	pool, exists := g.resources[resourceName]
	if !exists {
		pool = &ResourcePool{
			running: make(chan struct{}, maxRunning),
			spare:   make(chan struct{}, maxSpare),
		}
		g.resources[resourceName] = pool
	}
	pool.refCount++
	return pool
}

// releasePool decrements the pool refCount and deletes the key if no callers are using it.
func (g *KeyedGater) releasePool(resourceName string) {
	g.resMu.Lock()
	defer g.resMu.Unlock()

	pool, exists := g.resources[resourceName]
	if !exists {
		return
	}
	pool.refCount--
	if pool.refCount <= 0 {
		delete(g.resources, resourceName)
	}
}


// suicideIfLeaked protects the Node from PID exhaustion.
// Hardened: Fixed the duration scale collision and aligned atomic checks to explicit nanosecond barriers.
func (g *KeyedGater) suicideIfLeaked() {
	leaks := g.globalLeaked.Load()
	if leaks <= 0 {
		return
	}

	// 1. FATAL EXIT (The Absolute Circuit-Breaker)
	if leaks >= g.maxGlobal {
		fmt.Fprintf(os.Stderr, "FATAL: Global thread leak limit (%d) reached. Terminating process to protect Node PID tracks.\n", g.maxGlobal)
		// Give standard I/O channels a brief window to flush buffers before hard termination
		time.Sleep(500 * time.Millisecond)
		os.Exit(1)
	}

	// 2. THROTTLED WARNING (Every 30 seconds exactly)
	if leaks > (g.maxGlobal / 2) {
		nowNano := time.Now().UnixNano()
		lastNano := g.lastWarnTime.Load()
		
		// FIXED: Explicitly use standard duration thresholds to eliminate numeric scale drift
		if nowNano-lastNano > (30 * time.Second).Nanoseconds() {
			if g.lastWarnTime.CompareAndSwap(lastNano, nowNano) {
				// Aligned to use your primary logging engine infrastructure cleanly
				logger.Warningf("CRITICAL HEALTH ALERT: High kernel D-state or ghost thread leaks detected on host node (%d/%d active leaks).", leaks, g.maxGlobal)
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

	pool := g.getOrCreatePool(resourceName, maxRunning, maxSpare)
	defer g.releasePool(resourceName)

	select {
	case pool.running <- struct{}{}:
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
	
	pool.activeOps.Add(1)
	
	done := make(chan Result[T], 1) 
	switched := make(chan struct{})
	monitorDone := make(chan struct{}) // FIXED: Symmetrical escape gateway handshake
	var once sync.Once

	workerCtx, cancelWorker := context.WithCancel(ctx)

	parentAdditionalID, _ := goid_info.GetAdditionalIDInfo()

	// 3. WORKER LAUNCH
	go func() {
		defer pool.activeOps.Add(-1)
		defer cancelWorker()

		if parentAdditionalID != "" && parentAdditionalID != "-" {
			goid_info.SetAdditionalIDInfo(parentAdditionalID)
		}

		data, err := worker(workerCtx)
		done <- Result[T]{Data: data, Err: err}

		// FIXED: Evaluate whether the parent monitoring thread aborted due to saturation
		once.Do(func() {
			select {
			case <-switched:
				<-pool.spare
				g.globalLeaked.Add(-1) 
			case <-monitorDone:
				// The monitor thread already freed the pool.running slot; exit immediately
				return
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
		
   case <-ctx.Done():
	   // Context was cancelled by caller before worker finished or timed out
	   select {
	   case pool.spare <- struct{}{}:
			   once.Do(func() {
					   close(switched)
					   <-pool.running
			   })
			   g.globalLeaked.Add(1) // Worker is now executing in background as a tracked leak
			   var zero T
			   return zero, ctx.Err()
	   default:
			   once.Do(func() {
					   close(monitorDone)
					   <-pool.running
			   })
			   g.globalLeaked.Add(1) // Worker is abandoned due to complete saturation
			   var zero T
			   return zero, fmt.Errorf("resource %s: context cancelled and spare pool full", resourceName)
	   }
		
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
				return zero, fmt.Errorf("resource %s: abandoned after hard timeout %v", resourceName, hardTimeout)
			}
		default:
			// FIXED: Microsecond-safe release. Notify the worker goroutine via close(monitorDone)
			// that it should skip its own pool extraction block before reclaiming the token.
			once.Do(func() {
				close(monitorDone)
				<-pool.running
			})
			g.globalLeaked.Add(1)
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

// taskEnvelope couples the specific item data with its original execution position.
type taskEnvelope[Param any] struct {
	index int
	param Param
}

// ExecuteUninterruptibleBatch handles parallel batch operations safely insulated from kernel D-state stalls.
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

	totalItems := len(parameters)
	if totalItems == 0 {
		return nil, nil
	}

	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()

	// 1. Establish concurrency bounds based on the requested execution configurations
	numWorkers := maxRunning
	if numWorkers > totalItems {
		numWorkers = totalItems
	}
	if numWorkers <= 0 {
		numWorkers = 1
	}

	// 2. Thread-safe data pipes for dispatching tasks and gathering aggregated responses
	tasksChan := make(chan taskEnvelope[Param], totalItems)
	resultsChan := make(chan BatchResult[T], totalItems)

	// Hydrate the tasks pipeline upfront
	for idx, param := range parameters {
		tasksChan <- taskEnvelope[Param]{index: idx, param: param}
	}
	close(tasksChan)

	var wg sync.WaitGroup

	// 3. Launch a controlled worker pool limited exactly to your execution bounds
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for task := range tasksChan {
				// Fast path context cancellation check before acquiring tokens
				if err := batchCtx.Err(); err != nil {
					resultsChan <- BatchResult[T]{Index: task.index, Err: err}
					continue
				}

				// Leverage the single-item baseExecute logic underneath to cleanly reuse
				// token tracking, memory safety, and leak metrics without duplicating blocks.
				data, err := baseExecute(
					batchCtx,
					g,
					resourceName,
					maxRunning,
					maxSpare,
					handoffTimeout,
					hardTimeout,
					func(wCtx context.Context) (T, error) {
						return worker(wCtx, task.index, task.param, cancelBatch)
					},
				)

				resultsChan <- BatchResult[T]{
					Index: task.index,
					Data:  data,
					Err:   err,
				}
			}
		}()
	}

	// Wait for processing nodes to yield execution slots completely
	wg.Wait()
	close(resultsChan)

	// 4. Drain output queue and prepare payload response
	aggregatedResults := make([]BatchResult[T], totalItems)
	for res := range resultsChan {
		aggregatedResults[res.Index] = res
	}

	return aggregatedResults, nil
}

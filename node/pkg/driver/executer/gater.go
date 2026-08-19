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

	val, _ := g.resources.LoadOrStore(resourceName, &ResourcePool{})
	pool := val.(*ResourcePool)
	pool.Init(maxRunning, maxSpare) 
	
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
	defer cancelWorker()

	parentAdditionalID, _ := goid_info.GetAdditionalIDInfo()

	// 3. WORKER LAUNCH
	go func() {
		defer pool.activeOps.Add(-1)

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

	val, _ := g.resources.LoadOrStore(resourceName, &ResourcePool{})
	pool := val.(*ResourcePool)
	pool.Init(maxRunning, maxSpare) 

	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()

	parentAdditionalID, _ := goid_info.GetAdditionalIDInfo()

	resultsChan := make(chan BatchResult[T], len(parameters))
	var wg sync.WaitGroup

	for idx, param := range parameters {
		wg.Add(1)
		go func(index int, p Param) {
			defer wg.Done()

			select {
			case pool.running <- struct{}{}:
			case <-batchCtx.Done():
				resultsChan <- BatchResult[T]{Index: index, Err: batchCtx.Err()}
				return
			}

			pool.activeOps.Add(1)
			done := make(chan Result[T], 1) 
			switched := make(chan struct{})
			monitorDone := make(chan struct{}) // FIXED: Symmetrical handshake channel
			var once sync.Once

			workerCtx, cancelWorker := context.WithCancel(batchCtx)
			defer cancelWorker()

			// 3. INNER WORKER LAUNCH
			go func() {
				// Guarantees activeOps is ALWAYS decremented under any exit trajectory
				defer pool.activeOps.Add(-1)

				if parentAdditionalID != "" && parentAdditionalID != "-" {
					goid_info.SetAdditionalIDInfo(parentAdditionalID)
				}

				data, err := worker(workerCtx, index, p, cancelBatch)
				done <- Result[T]{Data: data, Err: err}

				// FIXED: Safe execution state machine checks if the monitor exited due to saturation
				once.Do(func() {
					select {
					case <-switched:
						<-pool.spare
						g.globalLeaked.Add(-1)
					case <-monitorDone:
						// Monitor thread already handled pool.running token release during a saturation miss
						return
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
					// FIXED: Microsecond-safe saturation release using the close-channel handshake
					once.Do(func() {
						close(monitorDone)
						<-pool.running
					})
					resultsChan <- BatchResult[T]{Index: index, Err: fmt.Errorf("batch item %d: critical saturation", index)}
				}
			}
		}(idx, param)
	}

	wg.Wait()
	close(resultsChan)

	aggregatedResults := make([]BatchResult[T], 0, len(parameters))
	for res := range resultsChan {
		aggregatedResults = append(aggregatedResults, res)
	}

	return aggregatedResults, nil
}

/**
 * Copyright 2019 IBM Corp.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package mount

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"golang.org/x/sys/unix"
	mount "k8s.io/mount-utils"
)

const (
       PrefixChrootOfHostRoot            = "/host"
)

// default mount/unmount timeout interval, 30s
var timeout time.Duration = 30 * time.Second

// MountState tracks the specific lifecycle stage of an active unmount operation
type MountState string

const (
	StateGracefulPending MountState = "GracefulPending"
	StateForcePending    MountState = "ForcePending"
	StateLazyPending     MountState = "LazyPending"
)

type mountSession struct {
	target    string
	startTime time.Time
}

type TrackedUnmount struct {
	mu             sync.Mutex
	FirstAttempt   time.Time
	LastState      MountState
	SyncDone       bool
	SyncInProgress bool
}

// Map to track the current state of a mount target
// Mounter is a warpper of mount.Mounter which has the ability to cancel
// a comand when timeout.
type Mounter struct {
	*mount.Mounter
	executer   executer.ExecuterInterface
	KeyedGater *executer.KeyedGater
	// Key: targetPath or volumeID, Value: startTime
	unmountTracker sync.Map // map[string]*TrackedUnmount

	stuckMounts   sync.Map // Key: *mountSession, Value: bool
	stuckCount    atomic.Int32
	maxStuckLimit int32
}

var _ mount.Interface = &Mounter{}
//type MounterBridge struct {
//	*mount.Mounter
//	ctx context.Context	*apiCtx
//	m *Mounter
//}

//var _ mount.Interface = &MounterBridge{}

func New(mounterPath string, g *executer.KeyedGater, limit int32) *Mounter {
	return &Mounter{
		Mounter:       mount.New(mounterPath).(*mount.Mounter),
		executer:      &executer.Executer{},
		KeyedGater:    g,
		maxStuckLimit: limit,
	}
}

func NewWithExecutor(mounterPath string, e executer.ExecuterInterface, g *executer.KeyedGater, limit int32) *Mounter {
	return &Mounter{
		Mounter:       mount.New(mounterPath).(*mount.Mounter),
		executer:      e,
		KeyedGater:    g,
		maxStuckLimit: limit,
	}
}

// NOT REPLACED: UnmountWithForce, MountSensitiveWithoutSystemd, MountSensitiveWithoutSystemdWithMountFlags, CanSafelySkipMountPointCheck

// 1. OVERRIDE: IsLikelyNotMountPoint
func (m *Mounter) IsLikelyNotMountPoint(file string) (bool, error) {
	isMounted, err := m.isMountedInProc(file)
	if err != nil {
		return true, err
	}
	return !isMounted, nil
}

// 3. OVERRIDE: IsMountPoint
func (m *Mounter) IsMountPoint(file string) (bool, error) {
	return m.isMountedInProc(file)
}

// 3. OVERRIDE: GetMountRefs
func (m *Mounter) GetMountRefs(pathname string) ([]string, error) {
	// Standard implementation is okay, but our inner.GetMountsForPath
	// is safer against D-state hangs.
	mounts, err := m.GetMountsForPath(pathname)
	if err != nil {
		return nil, err
	}

	var refs []string
	for _, mnt := range mounts {
		refs = append(refs, mnt.MountPoint)
	}
	return refs, nil
}

// 3. OVERRIDE: Mount

func (m *Mounter) Mount(source string, target string, fstype string, options []string) error {
	ctx, _ := context.WithTimeout(context.Background(), time.Duration(30)*time.Second)
	return m.MountNative(ctx, source, target, fstype, options)
}

// 3. OVERRIDE: Unmount

func (m *Mounter) Unmount(target string) error {
	ctx := context.Background()
	return m.UnmountWithTimeout(ctx, target, 30*time.Second)
}

// 3. OVERRIDE: List
// List retrieves all mount points by calling our common low-level GetMounts
func (m *Mounter) List() ([]mount.MountPoint, error) {
	// 1. Call our common low-level function that handles
	// octal unescaping and Major:Minor splitting.
	rawMounts, err := GetMounts("")
	if err != nil {
		return nil, err
	}

	var results []mount.MountPoint
	for _, rm := range rawMounts {
		// 2. Differentiate source based on your requirements
		device := rm.MountSource
		if strings.HasPrefix(device, "/dev/") {
			// For block devices, return the base name (e.g., "sda1")
			device = filepath.Base(device)
		}
		// Note: For NFS/CIFS, the full unescaped source is preserved in rm.MountSource

		// 3. Map to the library-defined MountPoint struct
		mp := mount.MountPoint{
			Device: device,
			Path:   rm.MountPoint, // Already unescaped by our common function
			Type:   rm.FilesystemType,
			Opts:   strings.Split(rm.MountOptions, ","),
		}

		// 4. Merge SuperOptions (FS-specific) into the Opts slice
		if rm.SuperOptions != "" {
			superOpts := strings.Split(rm.SuperOptions, ",")
			mp.Opts = append(mp.Opts, superOpts...)
		}

		results = append(results, mp)
	}

	return results, nil
}

const (
    // Standard placeholder for sensitive data in k8s logs
    sensitiveOptionsRemoved = "<masked>"
)

// MountSensitive implements k8s.io/mount-utils Interface
func (m *Mounter) MountSensitive(source, target, fstype string, options, sensitiveOptions []string) error {
    // 1. Log safely using the k8s standard <masked> placeholder
    if len(sensitiveOptions) > 0 {
        logger.Infof("Mounting %s to %s with options %v and sensitive options %s", 
            source, target, options, sensitiveOptionsRemoved)
    } else {
        logger.Infof("Mounting %s to %s with options %v", source, target, options)
    }
	
    // 2. Combine all options for the system call
    allOptions := append(options, sensitiveOptions...)
    
    // 3. Translate flags and perform the syscall
    flags, data := m.parseMountOptions(allOptions)
    
    // Note: fstype can be empty for bind mounts or remounts
    return unix.Mount(source, GetPodPath(target), fstype, flags, data)
}



func (m *Mounter) SearchForLongerMountPoints(targetPath string, _ []string, _ bool) ([]mount.MountPoint, error) {
	// 1. Get the "best" (longest) mount for this path
	mi, err := findBestMount(targetPath)
	if err != nil {
		return nil, nil // No mount found is not an error here
	}

	// 2. Return it as the library's MountPoint struct
	return []mount.MountPoint{
		{
			Device: mi.MountSource,
			Path:   mi.MountPoint,
			Type:   mi.FilesystemType,
			Opts:   strings.Split(mi.MountOptions, ","),
		},
	}, nil
}

// DeviceOpened checks if a block device is currently opened/mounted.
// It uses our safe, unescaped MountInfo list to avoid D-state hangs.
func (m *Mounter) DeviceOpened(ctx context.Context, pathname string) (bool, error) {
	// devName := filepath.Base(pathname)
	// 1. Get the actual Device ID from the host filesystem
	var st unix.Stat_t
	if err := unix.Stat(pathname, &st); err != nil {
		return false, fmt.Errorf("failed to stat device %s: %w", pathname, err)
	}

	// st.Rdev contains the combined major/minor for block devices
	targetMajor := unix.Major(st.Rdev)
	targetMinor := unix.Minor(st.Rdev)

	// 2. Get all current mounts using our safe /proc parser
	mounts, err := GetMounts("")
	if err != nil {
		return false, err
	}

	// 3. Precision Match: Compare Major/Minor numbers
	for _, mnt := range mounts {
		if mnt.Major == targetMajor && mnt.Minor == targetMinor {
			return true, nil
		}
		// We check both the full path and the base name for robustness
		//if mnt.MountSource == pathname || filepath.Base(mnt.MountSource) == devName {
		//	return true, nil
		//}
		
	}

	// 4. Fallback: Check if it's held open by a process (optional/advanced)
	// Some implementations try to open with O_EXCL, but that can be flaky.
	// For SafeFormatAndMount, checking the mount table is the standard requirement.
	return false, nil
}






// PathExists checks if the given path exists on the system.
// This is used to ensure the mount point directory is ready.
func (m *Mounter) PathExists(pathname string) (bool, error) {
	_, err := os.Stat(pathname)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	// Return the error for other cases (like permission denied)
	return false, err
}

// MakeDir creates the target directory if it doesn't exist.
// Often required by the SafeFormatAndMount logic flow.
func (m *Mounter) MakeDir(pathname string) error {
	err := os.MkdirAll(pathname, 0750)
	if err != nil {
		return fmt.Errorf("failed to create directory %s: %v", pathname, err)
	}
	return nil
}

func (m *Mounter) UnmountWithTimeout(ctx context.Context, target string, timeout time.Duration) error {
	now := time.Now()
	
	device, _ := m.GetDeviceFromMount(target)
	isDeviceStuck := false

	if device != "" {
		if m.executer.IsDeviceStillStuck(device) {
			logger.Warningf("Safety-Gate: Device %s is stuck. Skipping cache flush and cascading to Tier 3 (MNT_DETACH).", device)
			isDeviceStuck = true
			return m.EscalateToLazy(target)
		}

		isDM := strings.HasPrefix(filepath.Base(device), "dm-")
		isNVMe := strings.HasPrefix(filepath.Base(device), "nvme")

		if isDM || isNVMe {
			if _, err := m.executer.IsMultipathdAlive(ctx); err != nil && strings.Contains(err.Error(), "deadlock") {
				logger.Warning("multipathd deadlock; unmount aborted to prevent hang")
				return fmt.Errorf("safety-gate: multipathd deadlock; unmount aborted to prevent hang")
			}
		}
	}

	if mounted, _ := m.IsMounted(target); !mounted {
		logger.Warning("Not mounted - immediate exit")
		m.unmountTracker.Delete(target)
		return nil
	}

	val, _ := m.unmountTracker.LoadOrStore(target, &TrackedUnmount{
		FirstAttempt: now,
		LastState:    StateGracefulPending,
	})
	mInfo := val.(*TrackedUnmount)

	mInfo.mu.Lock()
	elapsed := now.Sub(mInfo.FirstAttempt)

	if mInfo.LastState == StateGracefulPending && !mInfo.SyncDone && !mInfo.SyncInProgress && !isDeviceStuck {
		
		// FIXED: Check layout attributes prior to firing the Sync-Gate.
		// Bypasses the os.Open file descriptor leak completely for Block Mode PVCs.
		fi, statErr := os.Stat(GetPodPath(target))
		if statErr == nil && !fi.IsDir() {
			logger.Infof("[Sync-Gate] Target %s is a Raw Block Volume node. Bypassing page cache sync cleanly.", target)
			mInfo.SyncDone = true
			goto SkipSyncGate
		}

		mInfo.SyncInProgress = true
		mInfo.mu.Unlock() 

		logger.Infof("[Sync-Gate] Synchronously flushing memory maps to storage backend for %s", target)
		
		// Wrap inside your ExecuteUninterruptible engine. If the sync system call blocks, 
		// the worker is cleanly isolated in the background, preventing Kubelet from hanging.
_, syncErr := executer.ExecuteUninterruptible[struct{}](
        ctx, m.KeyedGater, "syncfs-"+filepath.Base(target), 1, 10, 2*time.Second, 15*time.Second,
        func(wCtx context.Context) (struct{}, error) {
                f, err := os.Open(GetPodPath(target))
                if err != nil {
                        return struct{}{}, err
                }
                defer f.Close()

                // Natively forces the kernel file subsystem to flush dirty cache pages down to the iSCSI/NVMe fabrics
                // FIX: Invoke the raw syncfs system call trap
                err = unix.Syncfs(int(f.Fd()))
                return struct{}{}, err
        },
)


		mInfo.mu.Lock()
		mInfo.SyncInProgress = false
		if syncErr != nil {
			logger.Warningf("[Sync-Gate] Cache flush completed with a warning (proceeding to unmount): %v", syncErr)
		} else {
			logger.Infof("[Sync-Gate] Cache sync verified successful. Buffers fully committed.")
			mInfo.SyncDone = true
		}
	}
SkipSyncGate:
	mInfo.mu.Unlock()

	var err error

	switch {
	case elapsed < 2*time.Minute:
		err = m.tryUnmount(ctx, target, 0, timeout)
	case elapsed < 4*time.Minute:
		m.updateState(target, mInfo, StateForcePending)
		err = m.tryUnmount(ctx, target, syscall.MNT_FORCE, timeout)
	default:
		m.updateState(target, mInfo, StateLazyPending)
		err = m.tryUnmount(ctx, target, syscall.MNT_DETACH, timeout)
	}

	if err != nil {
		if err == syscall.ENOENT || err == syscall.EINVAL {
			logger.Warningf("already gone %v", err)
			m.unmountTracker.Delete(target)
			return nil
		}

		mInfo.mu.Lock()
		tierState := mInfo.LastState
		mInfo.mu.Unlock()

		if err == syscall.EBUSY || os.IsTimeout(err) {
			if tierState == StateGracefulPending || tierState == StateForcePending {
				logger.Warningf("target %s is busy under %s tier (waiting for Kubelet retry): %v", target, tierState, err)
				return fmt.Errorf("target %s is busy under %s tier (waiting for Kubelet retry): %w", target, tierState, err)
			}
			logger.Warningf("Terminal unmount failure (%v) in lazy tier. Safety windows exhausted. Forcing hardware rescue.", err)
			m.unmountTracker.Delete(target)
			return nil 
		}
		return err
	}

	if m.PollMountDeleted(ctx, target, 2*time.Second) {
		logger.Warningf("target %s poll ok", target)
		m.unmountTracker.Delete(target)
		return nil
	}

	mInfo.mu.Lock()
	finalTierCheck := mInfo.LastState
	mInfo.mu.Unlock()

	if finalTierCheck != StateLazyPending {
		return fmt.Errorf("unmount reported success but %s remains in mountinfo (waiting for retry)", target)
	}

	logger.Warningf("Mount point %s persists in mountinfo after lazy tier. Proceeding to hardware cleanup.", target)
	m.unmountTracker.Delete(target)
	return nil
}

func (m *Mounter) tryUnmount(ctx context.Context, target string, flags int, timeout time.Duration) error {
	ch := make(chan error, 1)

	// CONDITIONAL LOOKUP GATE: Preserves the cumulative cross-call history clock
	var session *mountSession
	if existing, found := m.stuckMounts.Load(target); found {
		session = existing.(*mountSession)
		logger.Warningf("[Mounter-Gate] Resuming stuck unmount pass for %s. Cumulative elapsed: %v", 
			target, time.Since(session.startTime))
	} else {
		session = &mountSession{target: target, startTime: time.Now()}
		m.stuckMounts.Store(target, session)
		logger.Infof("[Mounter-Gate] Target path %s initiating minute zero tracking session.", target)
	}

	go func(wCtx context.Context, path string, initialFlags int, s *mountSession) {
		podPath := GetPodPath(path)
		retryDelay := 100 * time.Millisecond
		var lastErr error

		for {
			totalElapsed := time.Since(s.startTime)
			currentFlags := initialFlags

			// 1. DYNAMIC ESCALATION MATRIX BASED ON CUMULATIVE TIME
			if totalElapsed >= 4*time.Minute {
				logger.Errorf("[Mounter-Gate] Critical age exceeded for %s (%v). Forcing syscall.MNT_DETACH.", path, totalElapsed)
				currentFlags = syscall.MNT_DETACH
			} else if totalElapsed >= 2*time.Minute && currentFlags == 0 {
				logger.Warningf("[Mounter-Gate] Path %s has been stuck for %v. Escalating to lazy unmount.", path, totalElapsed)
				currentFlags = syscall.MNT_DETACH
			}

			// 2. NATIVE CONTEXT DEADLINE CHECK
			if deadline, ok := wCtx.Deadline(); ok {
				if time.Until(deadline) < (500 * time.Millisecond) {
					if currentFlags == 0 {
						logger.Errorf("[Mounter-Gate] Context expiring for %s. Escalating to final MNT_DETACH sweep pass...", path)
						currentFlags = syscall.MNT_DETACH
						err := syscall.Unmount(podPath, currentFlags)
						if err == nil || err == syscall.ENOENT || err == syscall.EINVAL {
							m.stuckMounts.Delete(path)
							m.stuckCount.Add(-1)
							ch <- nil
							return
						}
						lastErr = err
					}

					logger.Warningf("[Mounter-Gate] Context deadline imminent for %s. Yielding back to Kubernetes.", path)
					ch <- syscall.EBUSY 
					return
				}
			}

			// 3. RUN THE SYSCALL
			err := syscall.Unmount(podPath, currentFlags)
			if err == nil {
				logger.Infof("[Mounter-Gate] Success! Path %s unmounted cleanly after %v total time.", path, totalElapsed)
				m.stuckMounts.Delete(path) // Clear historical footprint only on definitive victory
				m.stuckCount.Add(-1)
				ch <- nil
				return
			}

			lastErr = err

			// =========================================================================
			// NATIVE IDEMPOTENCY LOCK: INTERCEPT EINVAL & ENOENT
			// =========================================================================
			// ENOENT = Path directory does not exist on disk.
			// EINVAL = Path is physically present, but it is already not a mount point.
			// Both indicate total success for an unmount attempt. Exit cleanly with nil.
			if err == syscall.ENOENT || err == syscall.EINVAL {
				logger.Infof("[Mounter-Gate] Target %s already unmounted or missing (Syscall code: %v). Clearing tracking state.", path, err)
				m.stuckMounts.Delete(path)
				ch <- nil
				return
			}

			// Handle busy contention (EBUSY) with local backoff delays
			if err == syscall.EBUSY {
				select {
				case <-wCtx.Done():
					logger.Warningf("[Mounter-Gate] Context interrupted while path %s was waiting on EBUSY clear. Last recorded OS error: %v", path, lastErr)
					
					// FIX: If the context is canceled while we are actively fighting an EBUSY or EIO block,
					// return the actual system failure (lastErr) so the upper layers understand why it timed out.
					if lastErr != nil {
						ch <- lastErr
					} else {
						ch <- wCtx.Err()
					}
					return
				case <-time.After(retryDelay):
					retryDelay *= 2
					if retryDelay > 1*time.Second {
						retryDelay = 1 * time.Second 
					}
					continue
				}
			}

			// Any other structural system fault breaks processing immediately
			m.stuckMounts.Delete(path)
			ch <- err
			return
		}
	}(ctx, target, flags, session)

	select {
	case <-ctx.Done():
		logger.Errorf("[Mounter-Gate] Parent context canceled before unmount completed: %v", ctx.Err())
		return ctx.Err()

	case err := <-ch:
		// FIX: Restored explicit idempotency checks on the returned channel error.
		// If the background goroutine returned nil, ENOENT, or EINVAL, the path is clear.
		if err == nil || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EINVAL) {
			logger.Infof("[Mounter-Gate] Target %s tryUnmount verified gone or cleared (Error payload: %v).", target, err)
			return nil
		}
		
		if errors.Is(err, syscall.EBUSY) {
			return fmt.Errorf("target %s is busy: %w", target, err)
		}
		
		logger.Errorf("[Mounter-Gate] Target %s tryUnmount exiting with unhandled error: %v", target, err)
		return err

	case <-time.After(timeout):
		logger.Errorf("[Mounter-Gate] Target %s hit absolute timeout boundary.", target)
		m.stuckCount.Add(1)
		return fmt.Errorf("unmount timeout (%v) reached for %s", timeout, target)
	}
}

type SyncResult struct {
	Success bool
}

func (m *Mounter) backgroundSyncfs(ctx context.Context, target string, info *TrackedUnmount) {
	device, _ := m.GetDeviceFromMount(target)
	if device != "" && m.executer.IsDeviceStillStuck(device) {
		logger.Warningf("Background Sync aborted for %s: device %s is already wedged", target, device)
	}

	targetPath := target
	
	// FIXED: Direct structural check. If it is a block file device, bypass 
	// the syncfs layer instantly and flag it clean so lower tiers can execute.
	fi, statErr := os.Stat(GetPodPath(targetPath))
	if statErr == nil && !fi.IsDir() {
		logger.Warningf("target %s backgroundSyncfs - Bypassing syncfs for Raw Block Volume file", target)
		info.mu.Lock()
		info.SyncInProgress = false
		info.SyncDone = true 
		info.mu.Unlock()
		return
	}

	logger.Warningf("target %s backgroundSyncfs", target)

	res, err := executer.ExecuteUninterruptible[SyncResult](
		ctx,
		m.KeyedGater,
		"syncfs-"+targetPath,
		1, 
		5, 
		5*time.Second,
		30*time.Second,
		func(wCtx context.Context) (SyncResult, error) {
			fd, err := unix.Open(GetPodPath(targetPath), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
			if err != nil {
				return SyncResult{Success: false}, err
			}
			defer unix.Close(fd)

			if err := unix.Syncfs(fd); err != nil {
				return SyncResult{Success: false}, err
			}

			return SyncResult{Success: true}, nil
		},
	)
	
	logger.Warningf("target %s backgroundSyncfs - sync done", target)

	info.mu.Lock()
	defer info.mu.Unlock()

	info.SyncInProgress = false
	if err == nil {
		info.SyncDone = res.Success
	} else {
		info.SyncDone = false
		logger.Errorf("Background Syncfs failed for %s: %v", targetPath, err)
	}
}

func (m *Mounter) updateState(target string, info *TrackedUnmount, newState MountState) {
	// info is a pointer (*TrackedUnmount)
	info.mu.Lock()
	defer info.mu.Unlock()

	// Modifying the field via the pointer
	info.LastState = newState

	logger.Infof("Unmount state for %s updated to %v", target, newState)
}

func (m *Mounter) EscalateToLazy(target string) error {
	// MNT_DETACH is the "Nuclear Option": it decouples the VFS from the
	// broken hardware immediately, allowing the Kubelet path to clear.
	err := syscall.Unmount(GetPodPath(target), syscall.MNT_DETACH)
	if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
		m.unmountTracker.Delete(target)
		return nil
	}
	return err
}

// ImmediateDetach skips all graceful tiers and immediately executes a Lazy Unmount.
// This is used for cleanup of rogue volumes or when hardware is known to be dead.
// ImmediateDetach bypasses the temporal matrix and runs a lazy detach instantly.
// This is used as a fast-fail fallback when hardware layers are explicitly confirmed dead.
func (m *Mounter) ImmediateDetach(ctx context.Context, target string) error {
	m.unmountTracker.Delete(target)

	// SYSCALL: MNT_DETACH decouples the mount from the VFS view instantly
	err := syscall.Unmount(GetPodPath(target), syscall.MNT_DETACH)

	if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
		logger.Infof("ImmediateDetach: %s successfully detached from VFS", target)

		if m.PollMountDeleted(ctx, target, 5*time.Second) {
			return nil
		}
		return fmt.Errorf("detach reported success but %s still in mountinfo", target)
	}

	return fmt.Errorf("immediate detach failed for %s: %w", target, err)
}

func (m *Mounter) GetDeviceFromMount(target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("get-device: empty lookup path provided")
	}

	// Parse /proc/self/mountinfo to find the device source for the target
	mounts, err := m.List()
	if err != nil {
		return "", err
	}

	// FIXED: Normalize the lookup target path upfront to eliminate trailing slashes or relative directory dots
	cleanTarget := filepath.Clean(target)
	if absTarget, errAbs := filepath.Abs(cleanTarget); errAbs == nil {
		cleanTarget = absTarget
	}

	for _, mnt := range mounts {
		// FIXED: Normalize the mount path entry explicitly to guarantee a symmetrical comparison matrix
		cleanMntPath := filepath.Clean(mnt.Path)
		if absMntPath, errMntAbs := filepath.Abs(cleanMntPath); errMntAbs == nil {
			cleanMntPath = absMntPath
		}

		if cleanMntPath == cleanTarget {
			logger.Infof("[Mounter] Successful normalized match: Target '%s' maps to backing device: %s", target, mnt.Device)
			return mnt.Device, nil
		}
	}
	
	return "", fmt.Errorf("not found")
}

// IsMounted check with heuristics to avoid unnecessary procfs scans.
func (m *Mounter) IsMounted(target string) (bool, error) {
	// 1. Tier 0: Check if path exists
	stat, err := os.Lstat(GetPodPath(target))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // Path doesn't exist, cannot be mounted
		}
		return false, err
	}

	// 2. Tier 1: Device ID Heuristic (ProbablyNotMountPoint logic)
	// Compare the Device ID of the target with its parent.
	parentStat, err := os.Lstat(GetPodPath(filepath.Dir(strings.TrimSuffix(target, "/"))))
	if err == nil {
		if stat.Sys().(*syscall.Stat_t).Dev != parentStat.Sys().(*syscall.Stat_t).Dev {
			// Device IDs differ: This is DEFINITELY a mount point (standard or cross-device)
			return true, nil
		}
	}

	// 3. Tier 2: Ambiguity Handling (The "Bind Mount" Problem)
	// If Device IDs are the same, it could be a Bind Mount or just a normal directory.
	// In 2026, we MUST scan mountinfo to be certain.
	return m.isMountedInProc(target)
}

// PollMountDeleted safe-monitors the host mount tables until the target link completely vanishes.
// PollMountDeleted safe-monitors the host mount tables until the target link completely vanishes.
// Returns true if the mount cleared cleanly, or false if it timed out or hit an error.
func (m *Mounter) PollMountDeleted(ctx context.Context, target string, timeout time.Duration) bool {
	cleanName := filepath.Base(target)
	
	// FIX: DYNAMIC LOCK QUEUES
	// Grouping the lock namespace dynamically by target path isolates concurrent lookups,
	// allowing separate volumes to scan /proc without blocking each other.
	gaterKey := fmt.Sprintf("mountinfo-poll-%s", cleanName)

	res, err := executer.ExecuteUninterruptible[bool](
		ctx,
		m.KeyedGater,
		gaterKey,
		10,  // Expand maxRunning capacity dynamically for parallel multi-volume passes
		50,  
		100*time.Millisecond,
		timeout, 
		func(wCtx context.Context) (bool, error) {
			retryDelay := 100 * time.Millisecond
			start := time.Now()

			for time.Since(start) < timeout {
				// Cooperative gRPC/Kubelet cancellation verification
				if errCtx := wCtx.Err(); errCtx != nil {
					return false, errCtx
				}

				// Query the host table registry securely via our trimmed secureReadSysfs architecture
				mounted, errMount := m.IsMounted(target)
				if errMount != nil {
					logger.Warningf("[Mounter-Poll] [%s] Table read contention encountered: %v. Continuing...", cleanName, errMount)
				}

				// Success: The VFS link has completely vanished from the node host tables.
				if errMount == nil && !mounted {
					return true, nil
				}

				// Context-bounded interruptible micro-sleep cadence
				select {
				case <-wCtx.Done():
					return false, wCtx.Err()
				case <-time.After(retryDelay):
					continue
				}
			}
			
			return false, nil // Timeout reached within the worker thread
		},
	)

	if err != nil {
		// If a hard timeout or a gater capacity drop triggers, res evaluates to false.
		return false
	}

	return res
}

// Wrapper for MountNative the handles tracking of stuck mounts
func (m *Mounter) MountNativeWithContext(ctx context.Context, source, target, fstype string, options []string) error {
	m.reapRecoveredMounts(ctx)

	// 1. Requirement 8: Respect the incoming CSI context immediately
	if err := ctx.Err(); err != nil {
		return err
	}

	// 2. Guards (Requirements 4, 6)
	if m.IsPathStuck(target) {
		return fmt.Errorf("mount-safety: target %s is already wedged", target)
	}

	// 2. Hardware Guard: If the source is a device (e.g., /dev/sdX, /dev/dm-X)
	if strings.HasPrefix(source, "/dev/") {
		// Check for stuck kernel workers (jbd2, xfsaild, etc.)
		if m.executer.IsDeviceStillStuck(source) {
			return fmt.Errorf("mount-safety: device %s is in D-state; skipping mount to prevent thread leak", source)
		}

		// Check for Multipath health (if applicable)
		if strings.HasPrefix(filepath.Base(source), "dm-") {
			// We use our circuit-broken limiter to check liveness
			if _, err := m.executer.IsMultipathdAlive(ctx); err != nil {
				if strings.Contains(err.Error(), "deadlock") {
					return fmt.Errorf("mount-safety: multipathd deadlock detected; blocking mount on %s", source)
				}
			}
		}
	}

	session := &mountSession{
		target:    target,
		startTime: time.Now(),
	}

	done := make(chan error, 1)
	go func() {
		// Pass the context down to the lower level
		err := m.MountNative(ctx, source, target, fstype, options)
		m.clearSession(session)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// REQUIREMENT 8: The gRPC call was canceled or timed out.
		// We abandon the goroutine and track it as "stuck".
		m.stuckMounts.Store(session, true)
		m.stuckCount.Add(1)
		return fmt.Errorf("mount-safety: context canceled/timed out for %s: %w", target, ctx.Err())
	}
}

func (m *Mounter) MountNative(ctx context.Context, source, target, fstype string, options []string) error {
	// 1. Directory Preparation
	if err := os.MkdirAll(target, 0750); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	flags, data := m.parseMountOptions(options)
	
	target = GetPodPath(target)

	// Logic for the SOURCE:
	// 1. If it's a Bind Mount, the source is an existing directory on the host.
	// 2. If it's a standard absolute path (starts with /) but NOT a device (/dev).
	isBind := (flags & unix.MS_BIND) != 0
	isAbsolutePath := strings.HasPrefix(source, "/")
	isDevice := strings.HasPrefix(source, "/dev/")

	if isBind || (isAbsolutePath && !isDevice) {
		source = GetPodPath(source)
	}
	

	// 2. Initial Mount (Legacy Compatibility)
	// We use the Gater here because 'mount' is an uninterruptible syscall.
	_, err := executer.ExecuteUninterruptible[struct{}](
		ctx,
		m.KeyedGater,
		"mount-"+target,
		10,             // maxRunning: Limit concurrent mounts to 10
		50,             // maxSpare: Budget for threads stuck in kernel D-state
		2*time.Second,  // handoffTimeout: move to spare pool if kernel stalls
		20*time.Second, // hardTimeout: return error to caller
		func(ctx context.Context) (struct{}, error) {
			// Classic mount syscall - Standard for RHEL 7 (Kernel 3.10)
			// Note: mount(2) is notoriously prone to D-state hangs on stale fabrics
			err := unix.Mount(source, target, fstype, flags, data)
			return struct{}{}, err
		},
	)

	if err != nil {
		// The error will reflect either the syscall error (e.g., EBUSY)
		// or the Gater timeout error.
		return fmt.Errorf("initial mount failed for %s: %w", target, err)
	}

	// 3. Propagation & Read-Only Logic (The RHEL 7 "Two-Step")
	// Legacy kernels cannot apply MS_RDONLY during a MS_BIND.
	// Also applies shared/slave propagation.
	if (flags & (unix.MS_BIND | unix.MS_RDONLY | unix.MS_SHARED | unix.MS_PRIVATE | unix.MS_SLAVE)) != 0 {
		remountFlags := flags | unix.MS_REMOUNT
		_, err := executer.ExecuteUninterruptible[struct{}](
			ctx,
			m.KeyedGater,
			"remount-"+target,
			5,              // maxRunning: lower concurrency for remounts
			20,             // maxSpare: budget for stuck kernel threads
			1*time.Second,  // handoffTimeout: move to spare if kernel stalls
			10*time.Second, // hardTimeout: return error to caller
			func(ctx context.Context) (struct{}, error) {
				// RHEL 7 (Kernel 3.10) uses the classic mount(2) for remounts
				err := unix.Mount(source, target, fstype, remountFlags, data)
				return struct{}{}, err
			},
		)

		if err != nil {
			// SECURITY: Unmount immediately if we cannot lock the mount settings.
			// We use MNT_DETACH (lazy unmount) to ensure the unmount happens even if
			// the kernel is currently busy.
			_ = unix.Unmount(target, unix.MNT_DETACH)
			return fmt.Errorf("failed to apply remount/propagation flags for %s: %w", target, err)
		}
	}
	// TODO review:
	// Legacy kernels cannot apply MS_RDONLY during a MS_BIND in a single step.
	// We must apply a remount to lock the path to Read-Only.
	//if (flags&unix.MS_BIND) != 0 && (flags&unix.MS_RDONLY) != 0 {
	//	remountFlags := flags | unix.MS_REMOUNT
	//	if err := unix.Mount(source, target, fstype, remountFlags, data); err != nil {
	//		// CRITICAL: If remount fails, the path is currently Read-Write.
	//		// We must unmount immediately to maintain CSI security contracts.
	//		_ = unix.Unmount(target, unix.MNT_DETACH)
	//		return fmt.Errorf("failed to transition bind mount to read-only: %w", err)
	//	}
	//}

	return nil
}

func (m *Mounter) IsPathStuck(target string) bool {
	found := false
	m.stuckMounts.Range(func(key, value any) bool {
		if key.(*mountSession).target == target {
			found = true
			return false // stop iteration
		}
		return true
	})
	return found
}

func (m *Mounter) reapRecoveredMounts(ctx context.Context) {
	// use bufio.Scanner as in GetMounts
	if m.stuckCount.Load() == 0 {
		return
	}

	// 1. Get Live Mounts (Gated read of /proc/self/mountinfo)
	foundMounts := m.getLiveMounts()

	m.stuckMounts.Range(func(key, value any) bool {
		session := key.(*mountSession)

		// Check A: Is it now in the mount table?
		if _, recovered := foundMounts[session.target]; recovered {
			m.clearSession(session)
			return true
		}

		// Check B: Is the directory gone?
		// We use a Gater to prevent Lstat from hanging on a dead parent filesystem.
		_, err := executer.ExecuteUninterruptible[os.FileInfo](
			ctx,
			m.KeyedGater,
			"reap-stat-"+session.target,
			1,             // maxRunning: Only 1 reaper check per specific target
			1,             // maxSpare: Tight budget for reaper hangs
			1*time.Second, // handoffTimeout: move to spare if kernel blocks
			5*time.Second, // hardTimeout: return error to caller
			func(ctx context.Context) (os.FileInfo, error) {
				// os.Lstat is safer than os.Stat because it doesn't follow symlinks,
				// reducing the risk of traversing a hung mount point.
				return os.Lstat(session.target)
			},
		)

		if err != nil {
			// If the error is os.ErrNotExist, the "reap" was successful (it's gone).
			// If it's a timeout, the kernel is likely hung on that path.
			logger.Debugf("Reap check for %s: %v", session.target, err)
		}
		if os.IsNotExist(err) {
			m.clearSession(session)
		}
		return true
	})
}

// The helper itself
func (m *Mounter) getLiveMounts() map[string]struct{} {
	rawMounts, err := GetMounts("")
	found := make(map[string]struct{})
	if err != nil {
		return found
	}

	for _, rm := range rawMounts {
		found[unescapeMountString(rm.MountPoint)] = struct{}{}
	}
	return found
}

func (m *Mounter) clearSession(session *mountSession) {
	// LoadAndDelete using the pointer address ensures atomicity.
	// If the background goroutine finishes BEFORE the timeout,
	// LoadAndDelete fails (key not in map yet), and count doesn't drop.
	// Atomic check: did this specific pointer-session exist in our stuck map?
	if _, deleted := m.stuckMounts.LoadAndDelete(session); deleted {
		m.stuckCount.Add(-1)
		logger.Infof("Mount session for %s recovered/cleared", session.target)
	}
}

func (m *Mounter) clearStuck(target string) {
	if _, deleted := m.stuckMounts.LoadAndDelete(target); deleted {
		m.stuckCount.Add(-1)
	}
}

func (m *Mounter) parseMountOptions(options []string) (uintptr, string) {
	var flags uintptr
	var data []string

	for _, opt := range options {
		switch opt {
		// ALIASES & HELPERS (Skip to avoid kernel EINVAL)
		case "defaults":
			continue // Handled by kernel default behaviors
		case "_netdev", "nofail", "auto", "noauto", "user", "nouser":
			continue // System-level flags the kernel doesn't process

		// READ/WRITE
		case "ro":
			flags |= unix.MS_RDONLY
		case "rw":
			flags &= ^uintptr(unix.MS_RDONLY)

		// BIND & RECURSIVE
		case "bind":
			flags |= unix.MS_BIND
		case "rbind":
			flags |= (unix.MS_BIND | unix.MS_REC)
		case "remount":
			flags |= unix.MS_REMOUNT

		// SECURITY & EXECUTION
		case "nosuid":
			flags |= unix.MS_NOSUID
		case "suid":
			flags &= ^uintptr(unix.MS_NOSUID)
		case "nodev":
			flags |= unix.MS_NODEV
		case "dev":
			flags &= ^uintptr(unix.MS_NODEV)
		case "noexec":
			flags |= unix.MS_NOEXEC
		case "exec":
			flags &= ^uintptr(unix.MS_NOEXEC)

		// PROPAGATION (Critical for CSI drivers)
		case "shared":
			flags |= unix.MS_SHARED
		case "rshared":
			flags |= (unix.MS_SHARED | unix.MS_REC)
		case "slave":
			flags |= unix.MS_SLAVE
		case "rslave":
			flags |= (unix.MS_SLAVE | unix.MS_REC)
		case "private":
			flags |= unix.MS_PRIVATE
		case "rprivate":
			flags |= (unix.MS_PRIVATE | unix.MS_REC)

		// PERFORMANCE & ATIME
		case "sync":
			flags |= unix.MS_SYNCHRONOUS
		case "async":
			flags &= ^uintptr(unix.MS_SYNCHRONOUS)
		case "noatime":
			flags |= unix.MS_NOATIME
		case "atime":
			flags &= ^uintptr(unix.MS_NOATIME)
		case "relatime":
			flags |= unix.MS_RELATIME
		case "strictatime":
			flags |= unix.MS_STRICTATIME

		default:
			// Custom filesystem data (e.g., "mode=0755", "uid=1000")
			data = append(data, opt)
		}
	}
	return flags, strings.Join(data, ",")
}


// isMountedInProc is the Single Source of Truth
func (m *Mounter) isMountedInProc(target string) (bool, error) {
	mounts, err := m.GetMountsForPath(target)
	if err != nil {
		return false, err
	}
	return len(mounts) > 0, nil
}






// GetMountsForPath returns all MountInfo entries matching the target path.
func (m *Mounter) GetMountsForPath(target string) ([]MountInfo, error) {
	// Clean the path to handle trailing slashes or relative segments.
	targetPath, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return nil, err
	}

	allMounts, err := GetMounts(targetPath) // Our common low-level function
	if err != nil {
		return nil, err
	}

	return allMounts, nil
}

// Block Devices: You want the clean kernel name (e.g., sda1 instead of /dev/sda1).
// Network Mounts: You want the remote export path (e.g., 192.168.1.10:/exports/data).
// GetDeviceFromPath targets both constraints perfectly:
// 1. Block Devices: Returns the clean kernel name (e.g., "sda1" or "dm-2" instead of "/dev/sda1").
// 2. Network Mounts: Returns the raw remote export path (e.g., "192.168.1.10:/exports/data").
func GetDeviceFromPath(targetPath string) (string, error) {
	mi, err := findBestMount(targetPath)
	if err != nil {
		logger.Warningf("Failed to find mount for %s: %v", targetPath, err)
		return "", err
	}

	source := mi.MountSource
	fstype := mi.FilesystemType

	logger.Warningf("source %s type %s", source, fstype)

	// =========================================================================
	// CONSTRAINT 2: NETWORK / PSEUDO FILE SYSTEMS
	// =========================================================================
	// We check for network protocols immediately. If matched, we bypass 
	// all block-level sanitization and return the raw remote export path string intact.
	switch fstype {
	case "nfs", "nfs4", "cifs", "ceph", "glusterfs", "fuse.sshfs", "tmpfs", "devtmpfs":
		logger.Infof("Network/Pseudo FS detected. Returning raw remote export path: %s", source)
		return source, nil
	}

	// =========================================================================
	// CONSTRAINT 1: BLOCK DEVICES (CLEAN KERNEL NAME RESOLUTION)
	// =========================================================================
	// For standard storage block layers, we resolve the true kernel identifier.
	// Instead of calling the risky filepath.EvalSymlinks or stripping prefixes,
	// we safely read the static /sys/dev/block symlink using Major:Minor tokens.
	if mi.Major > 0 {
		sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", mi.Major, mi.Minor)
		if realPath, linkErr := os.Readlink(sysPath); linkErr == nil {
			// filepath.Base extracts the terminal node parameter from the link.
			// e.g., converts "../../devices/virtual/block/dm-2" down to "dm-2"
			// e.g., converts "../../devices/pci0000:00/.../block/sda/sda1" down to "sda1"
			cleanKernelName := filepath.Base(realPath)
			logger.Infof("Resolved block device to clean kernel name: %s", cleanKernelName)
			return cleanKernelName, nil
		}
	}

	// Fallback Strategy: If sysfs links are unreadable, process standard layout cuts
	if strings.HasPrefix(source, "/dev/") {
		cleanName := strings.TrimPrefix(source, "/dev/")
		if strings.HasPrefix(cleanName, "mapper/") {
			cleanName = strings.TrimPrefix(cleanName, "mapper/")
		}
		return cleanName, nil
	}

	return source, nil
}



func GetMajorMinorFromSysfs(targetPath string) (uint32, uint32, error) {
	mi, err := findBestMount(targetPath)
	if err != nil {
		return 0, 0, err
	}
	return mi.Major, mi.Minor, nil
}

func findBestMount(targetPath string) (*MountInfo, error) {
	mounts, err := GetMounts("")
	if err != nil {
		return nil, err
	}

	cleanedTarget := filepath.Clean(targetPath)

	var bestMatch *MountInfo
	maxLen := -1
	for i := range mounts {
		m := &mounts[i]
		
		if cleanedTarget == m.MountPoint || strings.HasPrefix(cleanedTarget, m.MountPoint+string(filepath.Separator)) {
			if len(m.MountPoint) > maxLen {
				maxLen = len(m.MountPoint)
				bestMatch = m
			}
		}
	}

	if bestMatch == nil {
		return nil, fmt.Errorf("no mount found for %s", targetPath)
	}

	// FIXED: Updated the root fallback safety interceptor. 
	// We only abort execution if the target path is a typical directory that went missing.
	// If the path contains the Kubernetes "volumeDevices/publish" block pattern, we allow 
	// the mapping lookup to continue since block volumes naturally live on the root filesystem.
	if bestMatch.MountPoint == "/" && cleanedTarget != "/" {
		if !strings.Contains(cleanedTarget, "volumeDevices/publish") {
			logger.Warningf("Safety-Gate Block: findBestMount intercepted a loose fallback to the host root device for missing path %s. Aborting lookup.", targetPath)
			return nil, fmt.Errorf("mount point for path %s has vanished from mountinfo", targetPath)
		}
		logger.Debugf("findBestMount: Permitting root fallback for Raw Block volume handle path: %s", cleanedTarget)
	}

	return bestMatch, nil
}

type MountInfo struct {
	MountID        int
	ParentID       int
	Major          uint32 // Integer major device number
	Minor          uint32 // Integer minor device number
	Root           string
	MountPoint     string
	MountOptions   string
	OptionalFields string
	FilesystemType string
	MountSource    string
	SuperOptions   string
}


func GetMounts(targetPath string) ([]MountInfo, error) {
	absTarget := ""
	if targetPath != "" {
		absTarget, _ = filepath.Abs(targetPath)
		absTarget = filepath.Clean(absTarget)
	}

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var mounts []MountInfo
	scanner := bufio.NewScanner(f)
	const maxCapacity = 1024 * 1024 
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		// FIXED: Replaced loose space matching with structural split rules
		// to protect against truncated rows in environments running high volume densities.
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}

		mountPoint := unescapeMountString(fields[4])

		if absTarget != "" && filepath.Clean(mountPoint) != absTarget {
			continue
		}

		devParts := strings.Split(fields[2], ":")
		if len(devParts) != 2 {
			continue 
		}

		major, errMajor := strconv.Atoi(devParts[0])
		minor, errMinor := strconv.Atoi(devParts[1])
		if errMajor != nil || errMinor != nil {
			continue
		}

		sepIdx := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sepIdx = i
				break
			}
		}
		if sepIdx == -1 || sepIdx+3 >= len(fields) {
			continue
		}

		mounts = append(mounts, MountInfo{
			MountID:        parseInt(fields[0]),
			ParentID:       parseInt(fields[1]),
			Major:          uint32(major),
			Minor:          uint32(minor),
			Root:           unescapeMountString(fields[3]),
			MountPoint:     mountPoint,
			MountOptions:   fields[5],
			FilesystemType: fields[sepIdx+1],
			MountSource:    unescapeMountString(fields[sepIdx+2]),
			SuperOptions:   fields[sepIdx+3],
		})
	}
	return mounts, scanner.Err()
}





func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

func unescapeMountString(path string) string {
	if !strings.Contains(path, "\\") {
		return path
	}
	var res strings.Builder
	res.Grow(len(path)) // Optimization: pre-allocate memory
	for i := 0; i < len(path); i++ {
		if path[i] == '\\' && i+3 < len(path) {
			// Try to parse the next 3 chars as octal
			if val, err := strconv.ParseUint(path[i+1:i+4], 8, 8); err == nil {
				res.WriteByte(byte(val))
				i += 3
				continue
			}
		}
		res.WriteByte(path[i])
	}
	return res.String()
}

func GetPodPath(origPath string) string {
    return path.Join(PrefixChrootOfHostRoot, origPath)
}

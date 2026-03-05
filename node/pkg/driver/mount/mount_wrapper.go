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

type MountState int

const (
	StateGracefulPending MountState = iota // Tier 1 active
	StateForcePending                      // Tier 2 active
	StateDetached                          // Tier 3 called; kernel background cleanup
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

// 1. OVERRIDE: IsLikelyNotMountPoint
func (m *Mounter) IsLikelyNotMountPoint(file string) (bool, error) {
	logger.Warningf("IsLikely %s", file)
	isMounted, err := m.isMountedInProc(file)
	if err != nil {
		return true, err
	}
	return !isMounted, nil
}

func (m *Mounter) IsMountPoint(file string) (bool, error) {
	logger.Warningf("IsMountPoint %s", file)
	return m.isMountedInProc(file)
}

// 3. OVERRIDE: GetMountRefs
func (m *Mounter) GetMountRefs(pathname string) ([]string, error) {
	logger.Warningf("GetMountRefs %s", pathname)
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
	logger.Warningf("Mount %s %s %s", source, target, fstype)
	return m.MountNativeWithTimeout(source, target, fstype, options, 30*time.Second)
}

// 3. OVERRIDE: Unmount

func (m *Mounter) Unmount(target string) error {
	logger.Warning("Unmount %s", target)
	return m.UnmountWithTimeout(target, 30*time.Second)
}

// 3. OVERRIDE: List
// List retrieves all mount points by calling our common low-level GetMounts
func (m *Mounter) List() ([]mount.MountPoint, error) {
	logger.Warningf("List")
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
		logger.Warningf("Check entry %s", device)
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

//Unstage / Unpublish
// check if there are any mounts inside the target path
// longerMounts, err := mount.SearchForLongerMountPoints(targetPath, mounter)
//if err != nil {
//    return err
//}
//if len(longerMounts) > 0 {
//   return fmt.Errorf("cannot cleanup %s because it contains active sub-mounts: %v", targetPath, longerMounts)
//}

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
func (m *Mounter) DeviceOpened(pathname string) (bool, error) {
	// 1. Get the kernel name (e.g., /dev/sdb -> sdb)
	devName := filepath.Base(pathname)

	// 2. Get all current mounts using our safe low-level function
	mounts, err := GetMounts("")
	if err != nil {
		return false, fmt.Errorf("failed to get mounts: %w", err)
	}

	// 3. Scan for any mount point using this device
	for _, mnt := range mounts {
		// We check both the full path and the base name for robustness
		if mnt.MountSource == pathname || filepath.Base(mnt.MountSource) == devName {
			return true, nil
		}
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

func (m *Mounter) UnmountWithTimeout(target string, timeout time.Duration) error {
	now := time.Now()
	device, _ := m.getDeviceFromMount(target)

	logger.Warning("Unmount 1")

	// 1. THE PATIENT GATE
	if device != "" && m.executer.IsDeviceStillStuck(device) {
		// We do NOT call ImmediateDetach here.
		// Instead, we log the status and return a "Retryable" error.
		logger.Infof("Device %s is in D-state. Waiting for IBM storage recovery before unmount.", device)

		// Return a specific error that Kubelet interprets as "Still working, retry."
		// We avoid calling syscall.Unmount entirely to prevent thread leakage.
		return fmt.Errorf("storage-wait: hardware %s is unresponsive; holding for recovery", device)
	}

	logger.Warning("Unmount 2")

	// 1. Resolve Device and Perform Safety Gate Checks
	device, _ = m.getDeviceFromMount(target)
	if device != "" {
		// HARDWARE GATE: If the kernel workers (jbd2/xfs) are already wedged,
		// any further I/O (like unmount or sync) will deadlock the thread.
		if m.executer.IsDeviceStillStuck(device) {
			logger.Warningf("Safety-Gate: Device %s is stuck. Skipping to Tier 3 (MNT_DETACH).", device)
			return m.EscalateToLazy(target)
		}

		// DAEMON GATE: If multipathd is deadlocked, the socket query will timeout.
		isDM := strings.HasPrefix(filepath.Base(device), "dm-")
		isNVMe := strings.HasPrefix(filepath.Base(device), "nvme")

		if isDM || isNVMe {
			if _, err := m.executer.IsMultipathdAlive(); err != nil && strings.Contains(err.Error(), "deadlock") {
				return fmt.Errorf("safety-gate: multipathd deadlock; unmount aborted to prevent hang")
			}
		}
	}

	logger.Warning("Unmount 3")

	// 2. Idempotency Check
	if mounted, _ := m.IsMounted(target); !mounted {
		m.unmountTracker.Delete(target)
		return nil
	}

	logger.Warning("Unmount 4")

	// 3. Manage State Tracking
	val, _ := m.unmountTracker.LoadOrStore(target, &TrackedUnmount{
		FirstAttempt: now,
		LastState:    StateGracefulPending,
	})
	mInfo := val.(*TrackedUnmount)

	mInfo.mu.Lock()
	elapsed := now.Sub(mInfo.FirstAttempt)

	logger.Warning("Unmount 5")

	// Tier 0: Background Sync (Only if hardware is healthy)
	if mInfo.LastState == StateGracefulPending && !mInfo.SyncDone && !mInfo.SyncInProgress {
		mInfo.SyncInProgress = true
		go m.backgroundSyncfs(target, mInfo)
	}
	mInfo.mu.Unlock()

	var err error

	// 4. Escalation Logic (RHEL 7 Tiered Approach)
	switch {
	case elapsed < 2*time.Minute:
		err = m.tryUnmount(target, 0, timeout)

	case elapsed < 4*time.Minute:
		m.updateState(target, mInfo, StateForcePending)
		err = m.tryUnmount(target, syscall.MNT_FORCE, timeout)

	default:
		err = m.ImmediateDetach(target)
		//return m.escalateToLazy(target)
	}

	if err == nil {
		logger.Warning("polling")
		if m.PollMountDeleted(target, 2*time.Second) {
			m.unmountTracker.Delete(target)
			return nil
		}
		return fmt.Errorf("unmount reported success but %s still remains in mountinfo", target)
	}

	return err
}

type SyncResult struct {
	Success bool
}

func (m *Mounter) backgroundSyncfs(target string, info *TrackedUnmount) {
	logger.Warning("sync")
	// 1. RE-VERIFY Hardware inside Goroutine
	device, _ := m.getDeviceFromMount(target)
	if device != "" && m.executer.IsDeviceStillStuck(device) {
		logger.Warningf("Background Sync aborted for %s: device %s is already wedged", target, device)
	}

	// In Go 1.22+, "targetPath := target" is no longer strictly required for
	// loop safety, but keeping it as a local copy for the closure is fine.
	targetPath := target

	// Use the generic SyncResult we defined earlier
	res, err := executer.ExecuteUninterruptible[SyncResult](
		m.KeyedGater,
		"syncfs-"+targetPath,
		1, // maxRunning: 1 sync per path
		5, // INCREASED: Give a bit more budget for background hangs
		5*time.Second,
		30*time.Second,
		func(ctx context.Context) (SyncResult, error) {
			// unix.O_NONBLOCK is critical for RHEL 7 on dead iSCSI/FC
			// unix.O_DIRECTORY ensures we don't accidentally open a file
			// unix.O_NONBLOCK prevents the open() itself from hanging on dead fabrics
			fd, err := unix.Open(GetPodPath(targetPath), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
			if err != nil {
				return SyncResult{Success: false}, err
			}
			defer unix.Close(fd)

			// Syncfs flushes the entire filesystem containing this FD
			if err := unix.Syncfs(fd); err != nil {
				return SyncResult{Success: false}, err
			}

			return SyncResult{Success: true}, nil
		},
	)

	// 2. ALWAYS UPDATE STATE
	// We wrap this in a defer-like pattern to ensure SyncInProgress is
	// set to false even if ExecuteUninterruptible returns an error.
	info.mu.Lock()
	defer info.mu.Unlock()

	info.SyncInProgress = false
	if err == nil {
		info.SyncDone = res.Success
	} else {
		// If it timed out or hit a hard error, mark it failed so
		// the teardown logic knows the sync didn't complete.
		info.SyncDone = false
		logger.Errorf("Background Syncfs failed for %s: %v", targetPath, err)
	}
}

func (m *Mounter) updateState(target string, info *TrackedUnmount, newState MountState) {
	logger.Warningf("update statd %s %d", target, newState)
	// info is a pointer (*TrackedUnmount)
	info.mu.Lock()
	defer info.mu.Unlock()

	// Modifying the field via the pointer
	info.LastState = newState

	logger.Infof("Unmount state for %s updated to %v", target, newState)
}

func (m *Mounter) EscalateToLazy(target string) error {
	logger.Warningf("escalate %s", target)
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
func (m *Mounter) ImmediateDetach(target string) error {
	logger.Warningf("Immediate %s", target)
	// 1. Resolve target to a session if tracking exists
	// This ensures we clean up the tracker even if this wasn't a "timed" escalation.
	m.unmountTracker.Delete(target)

	// 2. Perform the Lazy Unmount (MNT_DETACH)
	// On RHEL 7, this returns immediately regardless of hardware state.
	err := syscall.Unmount(GetPodPath(target), syscall.MNT_DETACH)

	// 3. Evaluate results
	// EINVAL/ENOENT mean it's already unmounted (Idempotent Success)
	if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
		logger.Infof("ImmediateDetach: %s successfully detached", target)

		// 4. Verify disappearance from mountinfo (Source of Truth)
		if m.PollMountDeleted(target, 5*time.Second) {
			return nil
		}
		return fmt.Errorf("detach reported success but %s still in mountinfo", target)
	}

	return fmt.Errorf("immediate detach failed for %s: %w", target, err)
}

func (m *Mounter) tryUnmount(target string, flags int, timeout time.Duration) error {
	logger.Warningf("tryUnmount %s", target)
	ch := make(chan error, 1)

	// Create a session to track this specific attempt
	session := &mountSession{target: target, startTime: time.Now()}
	m.stuckMounts.Store(target, session)

	go func(path string, f int) {
		// SYSCALL: May hang indefinitely in D-state
		err := syscall.Unmount(GetPodPath(path), f)

		// Cleanup: If we ever return, remove ourselves from the stuck tracker
		// TODO should track with pointer? or is the existing entry enough
		m.stuckMounts.Delete(path)
		if err == nil {
			m.stuckCount.Add(-1)
		}

		ch <- err
	}(target, flags) // Pass as arguments to avoid closure race

	select {
	case err := <-ch:
		if err == nil || err == syscall.ENOENT || err == syscall.EINVAL {
			return nil
		}
		if err == syscall.EBUSY {
			// Log this specifically: "Target is busy, waiting for K8S retry to escalate tiers"
			return fmt.Errorf("target %s is busy: %w", target, err)
		}
		return err
	case <-time.After(timeout):
		// LEAK ACKNOWLEDGED: Thread is now in D-state
		m.stuckCount.Add(1)
		return fmt.Errorf("unmount timeout (D-state) for %s - thread leaked", target)
	}
	// TODO verify disappearance
}

func (m *Mounter) getDeviceFromMount(target string) (string, error) {
	logger.Warningf("getDevice %s", target)
	// Parse /proc/self/mountinfo to find the device source for the target
	// This is standard for CSI mounter implementations
	mounts, err := m.Mounter.List()
	if err != nil {
		return "", err
	}
	for _, mnt := range mounts {
		if mnt.Path == target {
			return mnt.Device, nil
		}
	}
	return "", fmt.Errorf("not found")
}

// IsMounted check with heuristics to avoid unnecessary procfs scans.
func (m *Mounter) IsMounted(target string) (bool, error) {
	logger.Warningf("IsMount %s", target)
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
	parentStat, err := os.Lstat(filepath.Dir(GetPodPath(strings.TrimSuffix(target, "/"))))
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

//func (m *Mounter) IsStaged(targetPath string) (bool, error) {
//    // 1. Check if the directory exists
//    notMnt, err := m.IsLikelyNotMountPoint(targetPath)
//    if err != nil {
//        if os.IsNotExist(err) {
//            return false, nil // Not staged if path doesn't exist
//        }
//        return false, err
//    }

// 2. If it is a mount point, it is staged
//    return !notMnt, nil
//}

func (m *Mounter) PollMountDeleted(target string, timeout time.Duration) bool {
	logger.Warningf("PollMountDeleted %s", target)
	res, err := executer.ExecuteUninterruptible[bool](
		m.KeyedGater,
		"mountinfo-read",
		5,  // maxRunning: limit concurrent mountinfo scans to 5
		10, // maxSpare: budget for threads stuck reading /proc
		500*time.Millisecond,
		timeout, // hardTimeout: matches the polling window
		func(ctx context.Context) (bool, error) {
			start := time.Now()
			for time.Since(start) < timeout {
				// 1. Cooperative cancellation check
				if ctx.Err() != nil {
					return false, ctx.Err()
				}

				// 2. The Check (Reads /proc/self/mountinfo)
				mounted, err := m.IsMounted(target)
				if err != nil || !mounted {
					return true, nil // Success: mount is gone
				}

				// 3. Sleep with context awareness
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
					return false, ctx.Err()
				}
			}
			return false, nil // Timeout: mount still exists
		},
	)

	if err != nil {
		// In Go 1.22 generics, if a timeout or error occurs, res is false
		logger.Errorf("Mountinfo poll for %s failed or timed out: %v", target, err)
		return false
	}

	return res
}

func (m *Mounter) MountNativeWithTimeout(source, target, fstype string, options []string, timeout time.Duration) error {
	logger.Warningf("MountNative %s %s %s", source, target, fstype)
	m.reapRecoveredMounts()

	// 1. Path Guard: Is this mount point already undergoing a hung operation?
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
			if _, err := m.executer.IsMultipathdAlive(); err != nil {
				if strings.Contains(err.Error(), "deadlock") {
					return fmt.Errorf("mount-safety: multipathd deadlock detected; blocking mount on %s", source)
				}
			}
		}
	}

	// 2. Create a unique session for THIS specific attempt
	session := &mountSession{
		target:    target,
		startTime: time.Now(),
	}

	done := make(chan error, 1)
	go func() {
		err := m.MountNative(source, target, fstype, options)
		// Only clear THIS specific session
		m.clearSession(session)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		// Store the pointer. The pointer uniqueness prevents the race.
		m.stuckMounts.Store(session, true)
		m.stuckCount.Add(1)
		return fmt.Errorf("mount-safety: timeout on %s", target)
	}
}

func (m *Mounter) MountNative(source, target, fstype string, options []string) error {
	logger.Warningf("MountNative %s %s %s", source, target, fstype)

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

	logger.Warningf("MountNative %s %s %s", source, target, fstype)

	// 2. Initial Mount (Legacy Compatibility)
	// We use the Gater here because 'mount' is an uninterruptible syscall.
	_, err := executer.ExecuteUninterruptible[struct{}](
		m.KeyedGater,
		"mount-"+target,
		10,             // maxRunning: Limit concurrent mounts to 10
		50,             // maxSpare: Budget for threads stuck in kernel D-state
		2*time.Second,  // handoffTimeout: move to spare pool if kernel stalls
		20*time.Second, // hardTimeout: return error to caller
		func(ctx context.Context) (struct{}, error) {
			logger.Warning("Call unix mount")
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

func (m *Mounter) reapRecoveredMounts() {
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
	logger.Warning("getLiveMounts")
	found := make(map[string]struct{})

	// Read directly from the kernel's mount table
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return found
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		logger.Warningf("line %s", line)
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			// Field 5 (index 4) is the mount point absolute path
			found[unescapeMountString(fields[4])] = struct{}{}
		}
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
		case "ro":
			flags |= unix.MS_RDONLY
		case "bind":
			flags |= unix.MS_BIND
		case "shared":
			flags |= unix.MS_SHARED
		case "slave":
			flags |= unix.MS_SLAVE
		case "private":
			flags |= unix.MS_PRIVATE
		case "rbind":
			flags |= (unix.MS_BIND | unix.MS_REC)
		case "nosuid":
			flags |= unix.MS_NOSUID
		case "nodev":
			flags |= unix.MS_NODEV
		case "noexec":
			flags |= unix.MS_NOEXEC
		case "remount":
			flags |= unix.MS_REMOUNT
		default:
			data = append(data, opt)
		}
	}
	return flags, strings.Join(data, ",")
}

// isMountedInProc is the Single Source of Truth
func (m *Mounter) isMountedInProc(target string) (bool, error) {
	logger.Warningf("isMountInProc %s", target)
	mounts, err := m.GetMountsForPath(target)
	if err != nil {
		return false, err
	}
	return len(mounts) > 0, nil
}

// GetMountsForPath returns all MountInfo entries matching the target path.
func (m *Mounter) GetMountsForPath(target string) ([]MountInfo, error) {
	logger.Warningf("GetMountForPath %s", target)
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
func GetDeviceFromPath(targetPath string) (string, error) {
	logger.Warningf("Device from path %s", targetPath)
	mi, err := findBestMount(targetPath)
	if err != nil {
		logger.Warning("cannot find best")
		return "", err
	}

	source := mi.MountSource
	fstype := mi.FilesystemType

	logger.Warningf("source %s type %s", source, fstype)

	// 1. Handle Block Devices
	// If it's a standard /dev/ path, return just the base (e.g., "nvme0n1p3")
	if strings.HasPrefix(source, "/dev/") {
		return source, nil
	}

	// 2. Handle Network/Pseudo Filesystems
	// For these, the "source" is often an IP, a hostname, or a specific string
	switch fstype {
	case "nfs", "nfs4", "cifs", "ceph", "glusterfs", "fuse.sshfs":
		return source, nil // Keep the full remote path/address
	case "tmpfs", "devtmpfs":
		return fstype, nil // Usually better to know it's RAM than "tmpfs"
	default:
		// Fallback: If it's not a /dev path but we don't recognize the FS,
		// use the base name as a safe bet.
		return source, nil
	}
}

func GetMajorMinorFromSysfs(targetPath string) (uint32, uint32, error) {
	mi, err := findBestMount(targetPath)
	if err != nil {
		return 0, 0, err
	}
	return mi.Major, mi.Minor, nil
}

func findBestMount(targetPath string) (*MountInfo, error) {
	logger.Warningf("Best mount %s", targetPath)
	mounts, err := GetMounts("")
	if err != nil {
		return nil, err
	}

	var bestMatch *MountInfo
	maxLen := -1
	for _, m := range mounts {
		if strings.HasPrefix(targetPath, m.MountPoint) {
			if len(m.MountPoint) > maxLen {
				maxLen = len(m.MountPoint)
				bestMatch = &m
			}
		}
	}

	if bestMatch == nil {
		return nil, fmt.Errorf("no mount found for %s", targetPath)
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
	logger.Warningf("GetMounts %s", targetPath)
	absTarget := ""
	if targetPath != "" {
		absTarget, _ = filepath.Abs(targetPath)
		absTarget = filepath.Clean(absTarget)
	}

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		logger.Warning("Cannot open")
		return nil, err
	}
	defer f.Close()

	var mounts []MountInfo
	scanner := bufio.NewScanner(f)
	const maxCapacity = 1024 * 1024 // 1MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		mountPoint := unescapeMountString(fields[4])

		if absTarget != "" && filepath.Clean(mountPoint) != absTarget {
			continue
		}

		devParts := strings.Split(fields[2], ":")
		if len(devParts) != 2 {
			continue // Or handle as malformed line
		}

		major, errMajor := strconv.Atoi(devParts[0])
		minor, errMinor := strconv.Atoi(devParts[1])
		if errMajor != nil || errMinor != nil {
			continue
		}

		// Find the separator "-"
		sepIdx := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sepIdx = i
				break
			}
		}
		if sepIdx == -1 {
			continue
		}

		// CRITICAL: Unescape Root, MountPoint, and MountSource
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


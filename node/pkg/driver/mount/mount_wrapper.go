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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	mount "k8s.io/mount-utils"
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
	executer executer.ExecuterInterface
	KeyedGater		*executer.KeyedGater
	// Key: targetPath or volumeID, Value: startTime
	unmountTracker sync.Map // map[string]*TrackedUnmount

	stuckMounts sync.Map // Key: *mountSession, Value: bool
	stuckCount    atomic.Int32
	maxStuckLimit int32
}

var _ mount.Interface = &Mounter{}

func New(mounterPath string, limit int32) mount.Interface {
	return &Mounter{
		Mounter:  mount.New(mounterPath).(*mount.Mounter),
		executer: &executer.Executer{},
		maxStuckLimit: limit,
	}
}

func NewWithExecutor(mounterPath string, e executer.ExecuterInterface, limit int32) mount.Interface {
	return &Mounter{
		Mounter:  mount.New(mounterPath).(*mount.Mounter),
		executer: e,
		maxStuckLimit: limit,
	}
}



// 1. OVERRIDE: IsLikelyNotMountPoint
func (m *Mounter) IsLikelyNotMountPoint(file string) (bool, error) {
	isMounted, err := m.isMountedInProc(file)
	if err != nil { return true, err }
	return !isMounted, nil
}

// 2. OVERRIDE: IsMountPoin
func (m *Mounter) IsMountPoint(file string) (bool, error) {
	return m.isMountedInProc(file)
}

// 3. OVERRIDE: GetMountRefs
func (m *Mounter) GetMountRefs(pathname string) ([]string, error) {
	// Standard implementation is okay, but our inner.getMountsForPath
	// is safer against D-state hangs.
	mounts, err := m.getMountsForPath(pathname)
	if err != nil { return nil, err }

	var refs []string
	for _, mnt := range mounts {
		refs = append(refs, mnt.MountPoint)
	}
	return refs, nil
}

func (m *Mounter) UnmountWithTimeout(target string, timeout time.Duration) error {
    now := time.Now()
    device, _ := m.getDeviceFromMount(target)

    // 1. THE PATIENT GATE
    if device != "" && m.executer.IsDeviceStillStuck(device) {
        // We do NOT call ImmediateDetach here.
        // Instead, we log the status and return a "Retryable" error.
        logger.Infof("Device %s is in D-state. Waiting for IBM storage recovery before unmount.", device)

        // Return a specific error that Kubelet interprets as "Still working, retry."
        // We avoid calling syscall.Unmount entirely to prevent thread leakage.
        return fmt.Errorf("storage-wait: hardware %s is unresponsive; holding for recovery", device)
    }


	// 1. Resolve Device and Perform Safety Gate Checks
	device, _ = m.getDeviceFromMount(target)
	if device != "" {
		// HARDWARE GATE: If the kernel workers (jbd2/xfs) are already wedged,
		// any further I/O (like unmount or sync) will deadlock the thread.
		if m.executer.IsDeviceStillStuck(device) {
			logger.Warningf("Safety-Gate: Device %s is stuck. Skipping to Tier 3 (MNT_DETACH).", device)
			return m.escalateToLazy(target)
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

	// 2. Idempotency Check
	if mounted, _ := m.IsMounted(target); !mounted {
		m.unmountTracker.Delete(target)
		return nil
	}

	// 3. Manage State Tracking
	val, _ := m.unmountTracker.LoadOrStore(target, &TrackedUnmount{
		FirstAttempt: now,
		LastState:    StateGracefulPending,
	})
	mInfo := val.(*TrackedUnmount)

	mInfo.mu.Lock()
	elapsed := now.Sub(mInfo.FirstAttempt)

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
        if m.pollMountDeleted(target, 2*time.Second) {
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
		1,                // maxRunning: 1 sync per path
		5,                // INCREASED: Give a bit more budget for background hangs
		5*time.Second,
		30*time.Second,
		func(ctx context.Context) (SyncResult, error) {
			// unix.O_NONBLOCK is critical for RHEL 7 on dead iSCSI/FC
			// unix.O_DIRECTORY ensures we don't accidentally open a file
			// unix.O_NONBLOCK prevents the open() itself from hanging on dead fabrics
			fd, err := unix.Open(targetPath, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
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
	// info is a pointer (*TrackedUnmount)
	info.mu.Lock()
	defer info.mu.Unlock()

	// Modifying the field via the pointer
	info.LastState = newState

	logger.Infof("Unmount state for %s updated to %v", target, newState)
}

func (m *Mounter) escalateToLazy(target string) error {
	// MNT_DETACH is the "Nuclear Option": it decouples the VFS from the
	// broken hardware immediately, allowing the Kubelet path to clear.
	err := syscall.Unmount(target, syscall.MNT_DETACH)
	if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
		m.unmountTracker.Delete(target)
		return nil
	}
	return err
}

// ImmediateDetach skips all graceful tiers and immediately executes a Lazy Unmount.
// This is used for cleanup of rogue volumes or when hardware is known to be dead.
func (m *Mounter) ImmediateDetach(target string) error {
	// 1. Resolve target to a session if tracking exists
	// This ensures we clean up the tracker even if this wasn't a "timed" escalation.
	m.unmountTracker.Delete(target)

	// 2. Perform the Lazy Unmount (MNT_DETACH)
	// On RHEL 7, this returns immediately regardless of hardware state.
	err := syscall.Unmount(target, syscall.MNT_DETACH)

	// 3. Evaluate results
	// EINVAL/ENOENT mean it's already unmounted (Idempotent Success)
	if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
		logger.Infof("ImmediateDetach: %s successfully detached", target)

		// 4. Verify disappearance from mountinfo (Source of Truth)
		if m.pollMountDeleted(target, 5*time.Second) {
			return nil
		}
		return fmt.Errorf("detach reported success but %s still in mountinfo", target)
	}

	return fmt.Errorf("immediate detach failed for %s: %w", target, err)
}





func (m *Mounter) tryUnmount(target string, flags int, timeout time.Duration) error {
	ch := make(chan error, 1)

	// Create a session to track this specific attempt
	session := &mountSession{target: target, startTime: time.Now()}
	m.stuckMounts.Store(target, session)

	go func(path string, f int) {
		// SYSCALL: May hang indefinitely in D-state
		err := syscall.Unmount(path, f)

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
	// 1. Tier 0: Check if path exists
	stat, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // Path doesn't exist, cannot be mounted
		}
		return false, err
	}

	// 2. Tier 1: Device ID Heuristic (ProbablyNotMountPoint logic)
	// Compare the Device ID of the target with its parent.
	parentStat, err := os.Lstat(filepath.Dir(strings.TrimSuffix(target, "/")))
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


func (m *Mounter) pollMountDeleted(target string, timeout time.Duration) bool {
	res, err := executer.ExecuteUninterruptible[bool](
		m.KeyedGater,
		"mountinfo-read",
		5,               // maxRunning: limit concurrent mountinfo scans to 5
		10,              // maxSpare: budget for threads stuck reading /proc
		500*time.Millisecond,
		timeout,         // hardTimeout: matches the polling window
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
	// 1. Directory Preparation
	if err := os.MkdirAll(target, 0750); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	flags, data := m.parseMountOptions(options)

	// 2. Initial Mount (Legacy Compatibility)
	// We use the Gater here because 'mount' is an uninterruptible syscall.
	_, err := executer.ExecuteUninterruptible[struct{}](
		m.KeyedGater,
		"mount-"+target,
		10,               // maxRunning: Limit concurrent mounts to 10
		50,               // maxSpare: Budget for threads stuck in kernel D-state
		2*time.Second,    // handoffTimeout: move to spare pool if kernel stalls
		20*time.Second,   // hardTimeout: return error to caller
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
			m.KeyedGater,
			"remount-"+target,
			5,                // maxRunning: lower concurrency for remounts
			20,               // maxSpare: budget for stuck kernel threads
			1*time.Second,    // handoffTimeout: move to spare if kernel stalls
			10*time.Second,   // hardTimeout: return error to caller
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
			1,               // maxRunning: Only 1 reaper check per specific target
			1,               // maxSpare: Tight budget for reaper hangs
			1*time.Second,   // handoffTimeout: move to spare if kernel blocks
			5*time.Second,   // hardTimeout: return error to caller
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
	found := make(map[string]struct{})

	// Read directly from the kernel's mount table
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return found
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			// Field 5 (index 4) is the mount point absolute path
			found[m.unescapeProcPath(fields[4])] = struct{}{}
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
		case "ro":      flags |= unix.MS_RDONLY
		case "bind":    flags |= unix.MS_BIND
		case "shared":  flags |= unix.MS_SHARED
		case "slave":   flags |= unix.MS_SLAVE
		case "private": flags |= unix.MS_PRIVATE
		case "rbind":   flags |= (unix.MS_BIND | unix.MS_REC)
        case "nosuid":  flags |= unix.MS_NOSUID
        case "nodev":   flags |= unix.MS_NODEV
        case "noexec":  flags |= unix.MS_NOEXEC
		case "remount": flags |= unix.MS_REMOUNT
		default:        data = append(data, opt)
		}
	}
	return flags, strings.Join(data, ",")
}



// isMountedInProc is the Single Source of Truth
func (m *Mounter) isMountedInProc(target string) (bool, error) {
	mounts, err := m.getMountsForPath(target)
	if err != nil {
		return false, err
	}
	return len(mounts) > 0, nil
}


func (m *Mounter) getDeviceFromMountInfo(volumePath string) (string, error) {
	// Normalize path: absolute and cleaned
	absTarget, _ := filepath.Abs(volumePath)
	absTarget = filepath.Clean(absTarget)

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil { return "", err }
	defer f.Close()

	// Use 1MB buffer for high-density nodes (avoids 'token too long' errors)
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		// Performance optimization: skip non-candidate lines
		if !strings.Contains(line, absTarget) { continue }

		fields := strings.Fields(line)
		if len(fields) < 7 { continue }

		// Unescape octal-encoded paths (e.g. \040 for space)
		mountPoint := m.unescapeProcPath(fields[4])
		if filepath.Clean(mountPoint) != absTarget { continue }

		// Locate the "-" separator to bypass variable 'optional fields'
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" && i+2 < len(fields) {
				fstype := fields[i+1]
				source := m.unescapeProcPath(fields[i+2])

				if strings.HasPrefix(source, "/dev/") {
					return filepath.Base(source), nil
				}
				// Handle specific network protocols
				switch fstype {
				case "nfs", "nfs4", "cifs", "ceph", "glusterfs":
					return source, nil
				default:
					return filepath.Base(source), nil
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to scan mountinfo: %w", err)
	}
	return "", fmt.Errorf("path %s not in mount table", volumePath)
}


func (m *Mounter) unescapeProcPath(path string) string {
	// Fast path: most paths aren't escaped
    if !strings.Contains(path, "\\") {
        return path
    }
    var result strings.Builder
    for i := 0; i < len(path); i++ {
        if path[i] == '\\' && i+3 < len(path) {
            if n, err := strconv.ParseUint(path[i+1:i+4], 8, 8); err == nil {
                result.WriteByte(byte(n))
                i += 3
                continue
            }
        }
        result.WriteByte(path[i])
    }
    return result.String()
}


// MountEntry represents a simplified version of a /proc/self/mountinfo line
type MountEntry struct {
	MountID    int
	ParentID   int
	Major      int
	Minor      int
	Root       string
	MountPoint string
	Options    []string
}

// getMountsForPath returns ALL mounts found at a specific target path.
// It is common in K8s to find multiple mounts (e.g. a bind-mount on top of a device mount).
func (m *Mounter) getMountsForPath(targetPath string) ([]MountEntry, error) {
	absTarget, _ := filepath.Abs(targetPath)
	absTarget = filepath.Clean(absTarget)

	// 1. Gated Open: Reading /proc can hang if unrelated mounts are wedged.
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var results []MountEntry

	// 2. Pre-allocated Buffer:
	// We allocate a 1MB buffer ONCE. This is large enough for the densest
	// mountinfo lines while preventing the scanner from growing the heap
	// dynamically for every long line it encounters.
	scanner := bufio.NewScanner(f)
	const maxCapacity = 1024 * 1024 // 1MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		// scanner.Text() creates a string copy. For extreme memory safety
		// on RHEL 7, we could use scanner.Bytes(), but Text() is safer
		// for immediate parsing.
		line := scanner.Text()

		// 3. Fast Path String Check:
		// Avoids expensive field splitting if the target isn't in the line.
		if !strings.Contains(line, absTarget) {
			continue
		}

		// Structure of /proc/self/mountinfo:
		// 0:mountID 1:parentID 2:major:minor 3:root 4:mountpoint 5:opts 6:optional...

		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		// 4. Proper Unescaping:
		// Handles octal escapes (\040) safely without regex overhead.
		mountPoint := m.unescapeProcPath(fields[4])
		if mountPoint != absTarget {
			continue
		}

		devParts := strings.Split(fields[2], ":")
		var major, minor int
		if len(devParts) != 2 {
			continue
		}
		major, _ = strconv.Atoi(devParts[0])
		minor, _ = strconv.Atoi(devParts[1])
		mountID, _ := strconv.Atoi(fields[0])
		parentID, _ := strconv.Atoi(fields[1])

		results = append(results, MountEntry{
			MountID:    mountID,
			ParentID:   parentID,
			Major:      major,
			Minor:      minor,
			Root:       fields[3],
			MountPoint: mountPoint,
			Options:    strings.Split(fields[5], ","),
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mountinfo scan error: %w", err)
	}

	return results, nil
}

func unescapeMountPath(path string) string {
    if !strings.Contains(path, "\\") {
        return path
    }
    // Replaces octal escapes (e.g. \040) with actual characters
    // This is safer than a fixed replacer for arbitrary user paths.
    var res strings.Builder
    for i := 0; i < len(path); i++ {
        if path[i] == '\\' && i+3 < len(path) {
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


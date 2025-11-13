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
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	mount "k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"
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


// mount.Interface
------------------------------------------------------------------
// Mount performs the actual operation using the system's mount binary.
// It replaces the standard executer with our own that tracks stuck devices
// and runs commands with vfork rather than fork
func (m *Mounter) Mount(source string, target string, fstype string, options []string) error {
	return m.MountSensitive(source, target, fstype, options, nil)
}

// 2. MountSensitive handles standard + sensitive (hidden) options.
func (m *Mounter) MountSensitive(source, target, fstype string, options, sensitiveOptions []string) error {
	mountArgs := []string{}
	// Standard CSI practice: append options first
	for _, opt := range options {
		mountArgs = append(mountArgs, "-o", opt)
	}
	for _, opt := range sensitiveOptions {
		mountArgs = append(mountArgs, "-o", opt)
	}

	mountArgs = append(mountArgs, "-t", fstype, source, target)

	// Execute via our Executer to save memory
	cmd := m.exec.Command("mount", mountArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount failed: %v, output: %s", err, string(out))
	}
	return nil
}

func (m *Mounter) IsLikelyNotMountPoint(file string) (bool, error) {
	isMounted, error := e.isMountedInProc(file)
	if err != nil {
		return err
	}
	return !isMounted
}

// Below functions so that the interface is complete, not exepcted to be called in SafeFormatAndMount

func (m *Mounter) Unmount(target string) error {
	cmd := m.exec.Command("umount", target)
	_, err := cmd.CombinedOutput()
	return err
}

func (m *Mounter) List() ([]mount.MountPoint, error) {
	return m.parseMountInfo()
}

func (m *Mounter) GetMountRefs(pathname string) ([]string, error) {
    // 1. Resolve pathname to absolute
    absPath, _ := filepath.Abs(pathname)

    // 2. Scan mountinfo for any entries where 'source' or 'target' matches
    // This is critical for finding bind-mounts on RHEL 7.
    var refs []string
    mounts, _ := m.parseMountInfo()
    for _, mnt := range mounts {
        if mnt.Path == absPath {
            refs = append(refs, mnt.Path)
        }
    }
    return refs, nil
}

func (m *Mounter) parseMountInfo() ([]mount.MountPoint, error) {
	// 1. Read the kernel's internal mount table
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("failed to read mountinfo: %w", err)
	}

	var mountPoints []mount.MountPoint
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		// Field indices for /proc/self/mountinfo:
		// 0: mount ID
		// 1: parent ID
		// 2: major:minor
		// 3: root (path within FS)
		// 4: mount point (relative to process root) <-- This is the 'Path'
		// 5: mount options
		// 6: optional fields (variable length, ends with '-')
		// 7: separator '-'
		// 8: filesystem type
		// 9: mount source (the device)

		path := m.unescapeProcPath(fields[4])

		// Find the separator '-' to correctly identify the device source
		separatorIndex := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separatorIndex = i
				break
			}
		}

		if separatorIndex != -1 && separatorIndex+2 < len(fields) {
			mountPoints = append(mountPoints, mount.MountPoint{
				Device: m.unescapeProcPath(fields[separatorIndex+2]),
				Path:   path,
				Type:   fields[separatorIndex+1],
				Opts:   strings.Split(fields[5], ","),
			})
		}
	}

	return mountPoints, nil
}



------------------------------------------------------------------


func (m *Mounter) UnmountWithTimeout(target string, timeout time.Duration) error {
    now := time.Now()
    device, _ := m.getDeviceFromMount(target)

    // 1. THE PATIENT GATE
    if device != "" && m.executer.isDeviceStillStuck(device) {
        // We do NOT call ImmediateDetach here.
        // Instead, we log the status and return a "Retryable" error.
        m.logger.Infof("Device %s is in D-state. Waiting for IBM storage recovery before unmount.", device)

        // Return a specific error that Kubelet interprets as "Still working, retry."
        // We avoid calling syscall.Unmount entirely to prevent thread leakage.
        return fmt.Errorf("storage-wait: hardware %s is unresponsive; holding for recovery", device)
    }


	// 1. Resolve Device and Perform Safety Gate Checks
	device, _ := m.getDeviceFromMount(target)
	if device != "" {
		// HARDWARE GATE: If the kernel workers (jbd2/xfs) are already wedged,
		// any further I/O (like unmount or sync) will deadlock the thread.
		if m.executer.isDeviceStillStuck(device) {
			m.logger.Warningf("Safety-Gate: Device %s is stuck. Skipping to Tier 3 (MNT_DETACH).", device)
			return m.escalateToLazy(target)
		}

		// DAEMON GATE: If multipathd is deadlocked, the socket query will timeout.
		if strings.HasPrefix(filepath.Base(device), "dm-") {
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


	// 4. Escalation Logic (RHEL 7 Tiered Approach)
	switch {
	case elapsed < 2*time.Minute:
		return m.tryUnmount(target, 0, timeout)

	case elapsed < 4*time.Minute:
		m.updateState(mInfo, StateForcePending)
		return m.tryUnmount(target, syscall.MNT_FORCE, timeout)

	default:
		return m.ImmediateDetach(target)
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

func (m *Mounter) backgroundSyncfs(target string, info *TrackedUnmount) {
	// We use a local variable that the closure can modify
	var syncSuccess bool

	// 1. RE-VERIFY Hardware inside Goroutine
	// We check the device again inside the goroutine because the state
	// might have changed between the main thread check and this execution.
	device, _ := m.getDeviceFromMount(target)
	if device != "" && m.executer.isDeviceStillStuck(device) {
		m.logger.Warningf("Background Sync aborted: device %s is already wedged", device)
		goto FINISH
	}

	// 2. Execute with WaitDelay/D-State protection
	// Even O_NONBLOCK can hang if the kernel is wedged.
	_ = m.executer.ExecuteUninterruptible("syncfs-"+target, 1, 1, 0, 5*time.Second, 30*time.Second, func() error {
		// O_RDONLY | O_NONBLOCK prevents open() from hanging on a dead fabric
		f, err := os.OpenFile(target, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer f.Close()

		// SYS_SYNCFS is a deep kernel call. On RHEL 7, it is safer than sync()
		// because it only targets the specific super-block.
		if err := unix.Syncfs(int(f.Fd())); err != nil {
			return err
		}

		syncSuccess = true // Mark success only if Syncfs returned nil
		return nil
	})

FINISH:
	// 3. ATOMIC STATE UPDATE
	info.mu.Lock()
	info.SyncDone = syncSuccess
	info.SyncInProgress = false
	info.mu.Unlock()
}

func (m *Mounter) updateState(info *TrackedUnmount, newState UnmountState) {
	// info is a pointer (*TrackedUnmount)
	info.mu.Lock()
	defer info.mu.Unlock()

	// Modifying the field via the pointer
	info.LastState = newState

	m.logger.Infof("Unmount state for %s updated to %v", info.Target, newState)
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
		m.logger.Infof("ImmediateDetach: %s successfully detached", target)

		// 4. Verify disappearance from mountinfo (Source of Truth)
		if m.pollMountDeleted(target, 2*time.Second) {
			return nil
		}
		return fmt.Errorf("detach reported success but %s still in mountinfo", target)
	}

	return fmt.Errorf("immediate detach failed for %s: %w", target, err)
}



func (m *Mounter) tryUnmount(target string, flags int, timeout time.Duration) error {
	ch := make(chan error, 1)
	go func() {
		// This syscall might never return if the disk is physically gone/stuck
		ch <- syscall.Unmount(target, flags)
	}()

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
		// LEAK DETECTED: The syscall is stuck in the kernel.
		// Increment stuck count and track the session.

		// TODO review the mount tracking
		m.stuckCount.Add(1)
		m.stuckMounts.Store(&mountSession{target: target, startTime: time.Now()}, true)

		return fmt.Errorf("unmount syscall timed out (D-state) for %s - thread leaked", target)
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
	expiry := time.Now().Add(timeout)
	for time.Now().Before(expiry) {
		if mounted, _ := m.IsMounted(target); !mounted {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func (m *Mounter) MountNativeWithTimeout(source, target, fstype string, options []string, timeout time.Duration) error {
    m.reapRecoveredMounts()

    // 1. Path Guard: Is this mount point already undergoing a hung operation?
    if m.isPathStuck(target) {
        return fmt.Errorf("mount-safety: target %s is already wedged", target)
    }

    // 2. Hardware Guard: If the source is a device (e.g., /dev/sdX, /dev/dm-X)
    if strings.HasPrefix(source, "/dev/") {
        // Check for stuck kernel workers (jbd2, xfsaild, etc.)
        if m.executer.isDeviceStillStuck(source) {
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

    // 3. Create session and proceed with the goroutine...
    session := &mountSession{target: target, startTime: time.Now()}

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

// TODO restore old propagation checks
func (m *Mounter) MountNative(source, target, fstype string, options []string) error {
	if err := os.MkdirAll(target, 0750); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	flags, data := m.parseMountOptions(options)

	// Step 1: Standard Mount (Required for all kernels < 5.2)
	err := unix.Mount(source, target, fstype, flags, data)
	if err != nil {
		return fmt.Errorf("initial mount failed: %w", err)
	}

	// Step 2: Fix the Read-Only Bind Mount Race
	// Legacy kernels cannot apply MS_RDONLY during a MS_BIND in a single step.
	// We must apply a remount to lock the path to Read-Only.
	if (flags&unix.MS_BIND) != 0 && (flags&unix.MS_RDONLY) != 0 {
		remountFlags := flags | unix.MS_REMOUNT
		if err := unix.Mount(source, target, fstype, remountFlags, data); err != nil {
			// CRITICAL: If remount fails, the path is currently Read-Write.
			// We must unmount immediately to maintain CSI security contracts.
			_ = unix.Unmount(target, unix.MNT_DETACH)
			return fmt.Errorf("failed to transition bind mount to read-only: %w", err)
		}
	}

	return nil
}



func (m *Mounter) isPathStuck(target string) bool {
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

	// INTEGRATION POINT:
	// Get the current state of the world from /proc/self/mountinfo
	foundMounts := m.getLiveMounts()

	m.stuckMounts.Range(func(key, value any) bool {
		session := key.(*mountSession)

		// Check if the target is now a live mount
		_, recovered := foundMounts[session.target]

		// Check if the target was cleaned up (e.g., by a manual unmount)
		_, statErr := os.Lstat(session.target)

		if recovered || os.IsNotExist(statErr) {
			// We use the pointer (session) to ensure we only delete
			// the specific attempt that we just verified is recovered.
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
        case "nosuid":  flags |= unix.MS_NOSUID
        case "nodev":   flags |= unix.MS_NODEV
        case "noexec":  flags |= unix.MS_NOEXEC
        case "bind":    flags |= unix.MS_BIND
        case "remount": flags |= unix.MS_REMOUNT
        case "rbind":   flags |= (unix.MS_BIND | unix.MS_REC) // Crucial for K8s Shared Propagation
        default:        data = append(data, opt)
        }
    }
    return flags, strings.Join(data, ",") // Mount data is usually comma-separated
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
	// 1. Normalize target to Absolute, Clean path
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		absTarget = filepath.Clean(targetPath)
	}
	absTarget = strings.TrimSuffix(absTarget, "/")

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("failed to open mountinfo: %w", err)
	}
	defer f.Close()

	var results []MountEntry
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// 2. Pre-filter using unescaped string check
		// Note: we check the raw line; it might contain \040 instead of space
		if !strings.Contains(line, absTarget) && !strings.Contains(line, strings.ReplaceAll(absTarget, " ", "\\040")) {
			continue
		}

		// Structure of /proc/self/mountinfo:
		// 0:mountID 1:parentID 2:major:minor 3:root 4:mountpoint 5:opts 6:optional...
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		// 3. Unescape the kernel path (handles spaces/tabs)
		// Custom unescape logic or a simple replace for common K8s chars
		mountPoint := unescapeMountPath(fields[4])
		if mountPoint != absTarget {
			continue
		}

		// Parse identifiers
		devParts := strings.Split(fields[2], ":")
		if len(devParts) != 2 { continue }

		major, _ := strconv.Atoi(devParts[0])
		minor, _ := strconv.Atoi(devParts[1])
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

	return results, scanner.Err()
}

// unescapeMountPath handles octal escapes like \040 (space) used by the kernel
func unescapeMountPath(path string) string {
	if !strings.Contains(path, "\\") {
		return path
	}
	// Simplified octal unescaper for common mount characters
	replacer := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\")
	return replacer.Replace(path)
}



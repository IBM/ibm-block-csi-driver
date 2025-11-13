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





func (m *Mounter) UnmountWithTimeout(target string, timeout time.Duration) error {
    now := time.Now()

    // 1. Get the underlying device for the target path
    // You need to resolve /var/lib/kubelet/pods/... to /dev/dm-X or /dev/sdX
    device, _ := m.getDeviceFromMount(target)

    // 2. Safety Guard: Check for hardware/kernel hangs before touching the syscall
    if device != "" {
        // Check if the specific device has a stuck jbd2/xfsaild worker
        if m.executer.isDeviceStillStuck(device) {
            return fmt.Errorf("safety-gate: underlying device %s is stuck in D-state, skipping unmount to prevent thread hang", device)
        }

        // Check if management daemon is alive (if multipath is in use)
        if strings.HasPrefix(filepath.Base(device), "dm-") {
            if _, err := m.executer.IsMultipathdAlive(); err != nil {
                if strings.Contains(err.Error(), "deadlock") {
                    return fmt.Errorf("safety-gate: multipathd is deadlocked, unmount aborted for safety")
                }
                // If it's just "down", we proceed as per your "legitimate state" rule
            }
        }
    }

    // 1. Double-Check Idempotency
    if mounted, _ := m.IsMounted(target); !mounted {
        m.unmountTracker.Delete(target)
        return nil
    }

    // Load or create the tracking object
    val, _ := m.unmountTracker.LoadOrStore(target, &TrackedUnmount{
        FirstAttempt: now,
        LastState:    StateGracefulPending,
    })
    mInfo := val.(*TrackedUnmount)

    // Critical Section: Protect against concurrent escalation and sync starts
    mInfo.mu.Lock()
    elapsed := now.Sub(mInfo.FirstAttempt)

    // Tier 0: Background Sync (Trigger only once)
    if mInfo.LastState == StateGracefulPending && !mInfo.SyncDone && !mInfo.SyncInProgress {
        mInfo.SyncInProgress = true
        // Pass the mInfo pointer directly to avoid re-loading it in the goroutine
        go m.backgroundSyncfs(target, mInfo)
    }
    currentState := mInfo.LastState
    mInfo.mu.Unlock()

    // 2. Escalation Logic
    switch {
    case elapsed < 2*time.Minute:
        return m.tryUnmount(target, 0, timeout)

    case elapsed < 4*time.Minute:
        mInfo.mu.Lock()
        mInfo.LastState = StateForcePending
        mInfo.mu.Unlock()
        return m.tryUnmount(target, syscall.MNT_FORCE, timeout)

    default:
		mInfo.mu.Lock()
		mInfo.LastState = StateDetached
		mInfo.mu.Unlock()
        err := syscall.Unmount(target, syscall.MNT_DETACH)
        if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
			// TODO perhaps don't delete and fallback to chekc disappeance
            m.unmountTracker.Delete(target)
            return nil
        }
        return err
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
	// 1. Perform the heavy I/O WITHOUT holding the lock
	// O_NONBLOCK prevents the open itself from hanging in some kernel versions
	// TODO if the underlying IBM storage path is already "gone" (e.g., a forced detach at the storage array), 
	// even an open() can hang. Consider wrapping the os.OpenFile inside a timeout-protected goroutine as well
	f, err := os.OpenFile(target, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	success := false
	if err == nil {
		// TODO  Check other places to apply
		defer runtime.KeepAlive(f)
		// SYS_SYNCFS is more efficient than global sync() as it only flushes one FS
		if err = unix.Syncfs(int(f.Fd())); err == nil {
			success = true
		}
		f.Close()
	}

	// 2. Lock ONLY to update the pointer's fields
	info.mu.Lock()
	info.SyncDone = success
	info.SyncInProgress = false // Now the main thread can safely see this is over
	info.mu.Unlock()
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

func (m *Mounter) GetDiskFormatNative(device string) (string, error) {
	// 1. Safety Guard: Never attempt to read a device that is already wedged in the kernel
	if m.executer.isDeviceStillStuck(device) {
		return "unknown", fmt.Errorf("safety-gate: device %s is stuck in D-state", device)
	}

	// TODO Use O_RDONLY | O_DIRECT (optional) for raw block devices
    // 2. Open with O_NONBLOCK (Optional but recommended)
    // For some block devices, opening can hang if the driver is waiting on a resource.
	// TODO review
    f, err := os.OpenFile(device, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// 68KB covers Btrfs at 64KB and ZFS
	buf := make([]byte, 68*1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}
	buf = buf[:n]

	// --- 1. LUKS Detection ---
	if len(buf) >= 8 && bytes.HasPrefix(buf, []byte("LUKS\xba\xbe")) {
		version := binary.BigEndian.Uint16(buf[6:8])
		if version == 1 {
			return "luks1", nil
		} else if version == 2 {
			return "luks2", nil
		}
		return "luks", nil
	}

	// --- 2. XFS (Offset 0) ---
	if len(buf) >= 4 && bytes.HasPrefix(buf, []byte("XFSB")) {
		return "xfs", nil
	}

	// --- 3. NTFS (Offset 0x03) ---
	if len(buf) >= 0x03+8 && bytes.Equal(buf[0x03:0x03+8], []byte("NTFS    ")) {
		return "ntfs", nil
	}

	// --- 4. EXT Family (Offset 0x438) ---
	if len(buf) >= 0x438+2 {
		magic := binary.LittleEndian.Uint16(buf[0x438 : 0x438+2])
		if magic == 0xEF53 {
			// Check for Ext4 features (Offset 0x460)
			if len(buf) >= 0x460+4 {
				incompat := binary.LittleEndian.Uint32(buf[0x460 : 0x460+4])
				if incompat&0x40 != 0 {
					return "ext4", nil
				}
			}
			return "ext3", nil
		}
	}

	// --- 5. Btrfs (Offset 0x10040) ---
	if len(buf) >= 0x10040+8 {
		if string(buf[0x10040:0x10048]) == "_BHRfS_M" {
			return "btrfs", nil
		}
	}

	// --- 6. Swap (Offset 4086) ---
	if len(buf) >= 4086+10 && string(buf[4086:4086+10]) == "SWAPSPACE2" {
		return "swap", nil
	}

	// --- 7. LVM PV (Check first 1024 bytes) ---
	limit := 1024
	if len(buf) < limit { limit = len(buf) }
	if limit >= 8 && bytes.Contains(buf[:limit], []byte("LABELONE")) && bytes.Contains(buf[:limit], []byte("LVM2")) {
		return "lvm_pv", nil
	}

	// --- 8. ZFS (vdev labels at 8KB, 16KB) ---
	for _, offset := range []int{0x2000, 0x4000} {
		if len(buf) >= offset+8 {
			magic := binary.LittleEndian.Uint64(buf[offset : offset+8])
			// Check both Little and Big Endian for ZFS
			if magic == 0x00bab10c || magic == 0x0cb1ba00 {
				return "zfs", nil
			}
		}
	}

	// --- 9. Zero Check (Verify first 4KB) ---
	if len(buf) >= 4096 {
		isZero := true
		for i := 0; i < 4096; i++ {
			if buf[i] != 0 {
				isZero = false
				break
			}
		}
		if isZero {
			return "", nil // Unformatted
		}
	}

	return "unknown", nil
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


func (m Mounter) unescapeProcPath(path string) string {
	// Fast path: most paths aren't escaped
	if !strings.Contains(path, "\\") {
		return path
	}

	// Manual octal decoding is safer and faster for /proc strings than NewReplacer
	// for specific 3-digit octal sequences (\040, \011, etc)
	var result strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] == '\\' && i+3 < len(path) {
			// Potential octal sequence
			octal := path[i+1 : i+4]
			if n, err := strconv.ParseInt(octal, 8, 16); err == nil {
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



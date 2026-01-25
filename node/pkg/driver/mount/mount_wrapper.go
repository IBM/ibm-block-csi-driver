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
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
		maxStuckLimit: limit
	}
}

func NewWithExecutor(mounterPath string, e executer.ExecuterInterface, limit int32) mount.Interface {
	return &Mounter{
		Mounter:  mount.New(mounterPath).(*mount.Mounter),
		executer: e,
		maxStuckLimit: limit
	}
}



func (m *Mounter) UnmountWithTimeout(target string, timeout time.Duration) error {
	now := time.Now()

	// 1. Initial Idempotency - Check if actually mounted
	if !m.isMounted(target) {
		m.unmountTracker.Delete(target)
		return nil
	}

	// Use LoadOrStore with a pointer to TrackedUnmountunt to avoid identity races
	val, loaded := m.unmountTracker.LoadOrStore(target, &TrackedUnmount{
		FirstAttempt: now,
		LastState:    StateGracefulPending,
	})
	mInfo := val.(*TrackedUnmountount)

	// If we just stored it (loaded == false), we use the 'now' we just created.
	// If it was already there, we use the original FirstAttempt.
	mInfo.mu.Lock()
	elapsed := now.Sub(mInfo.FirstAttempt)
	currentState := mInfo.LastState

	// Tier 0: Background Sync
	// We do this early to try and flush data before the first unmount attempt.
	if currentState == StateGracefulPending && !mInfo.SyncDone && !mInfo.SyncInProgress {
		mInfo.SyncInProgress = true
		go m.backgroundSyncfs(target)
	}
	mInfo.mu.Unlock()

	// 2. Tiered Escalation Logic
	var err error
	switch {
	case elapsed < 2*time.Minute:
		// Attempt 1: Standard Graceful Unmount
		err = m.tryUnmount(target, 0, timeout)

	case elapsed < 4*time.Minute:
		// Attempt 2: MNT_FORCE
		// This is vital for network FS (NFS/SMB) that are unreachable.
		mInfo.mu.Lock()
		mInfo.LastState = StateForcePending
		mInfo.mu.Unlock()
		err = m.tryUnmount(target, syscall.MNT_FORCE, timeout)

	default:
		// Attempt 3: MNT_DETACH (Lazy Unmount)
		// Immediately removes the mount from the namespace.
		err = syscall.Unmount(target, syscall.MNT_DETACH)
		if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
			mInfo.mu.Lock()
			mInfo.LastState = StateDetached
			mInfo.mu.Unlock()
			m.unmountTracker.Delete(target)
			return nil
		}
	}

	// 3. Post-Action Verification
	if err == nil {
		if m.pollMountDeleted(target, 2*time.Second) {
			m.unmountTracker.Delete(target)
			return nil
		}
		return fmt.Errorf("unmount reported success but %s still remains in mountinfo", target)
	}

	return err
}

func (m *Mounter) backgroundSyncfs(target string) {
	success := false
	// O_NONBLOCK prevents the open itself from hanging in some kernel versions
	f, err := os.OpenFile(target, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err == nil {
		// SYS_SYNCFS is more efficient than global sync() as it only flushes one FS
		if _, _, errno := syscall.Syscall(syscall.SYS_SYNCFS, f.Fd(), 0, 0); errno == 0 {
			success = true
		}
		f.Close()
	}

	if val, ok := m.unmountTracker.Load(target); ok {
		info := val.(*TrackedUnmount)
		info.mu.Lock()
		info.SyncDone = success
		info.SyncInProgress = false
		info.mu.Unlock()
	}
}

func (m *Mounter) tryUnmount(target string, flags int, timeout time.Duration) error {
	ch := make(chan error, 1)
	go func() { ch <- syscall.Unmount(target, flags) }()

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
		return fmt.Errorf("unmount syscall timed out (D-state)")
	}
	// TODO verify disappearance
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



// isMountedInProc is the Single Source of Truth fallback
func (m *Mounter) isMountedInProc(target string) (bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	defer f.Close()

	// Optimized scanning with large buffer for 2026 density
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	// Clean target for exact comparison
	targetClean := filepath.Clean(target)

	for scanner.Scan() {
		line := scanner.Text()
		// Performance optimization: Avoid Fields() on lines that don't match the path
		if !strings.Contains(line, targetClean) {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[4] == targetClean {
			return true, nil
		}
	}
	return false, nil
}

func (m *Mounter) pollMountDeleted(target string, timeout time.Duration) bool {
	expiry := time.Now().Add(timeout)
	for time.Now().Before(expiry) {
		if !isMounted(target) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}


func (m *Mounter) GetDiskFormatNative(device string) (string, error) {
	// Use O_RDONLY | O_DIRECT (optional) for raw block devices
	f, err := os.OpenFile(device, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// 68KB covers Btrfs at 64KB and ZFS at 8KB/16KB
	buf := make([]byte, 68*1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}
	buf = buf[:n] // Ensure we don't index past what was actually read

	if bytes.HasPrefix(buf, []byte("LUKS\xba\xbe")) {
		if len(buf) >= 8 {
			version := binary.BigEndian.Uint16(buf[6:8])
			if version == 1 {
				return "luks1", nil
			} else if version == 2 {
				return "luks2", nil
			}
		}
		return "luks", nil
	}

	// 1. XFS (Offset 0): "XFSB"
	if bytes.HasPrefix(buf, []byte("XFSB")) {
		return "xfs", nil
	}

	// 2. NTFS (Offset 0x03): "NTFS    "
	if len(buf) > 0x03+4 && bytes.Equal(buf[0x03:0x03+7], []byte("NTFS   ")) {
		return "ntfs", nil
	}

	// 3. EXT Family (Offset 0x438 = 1080 bytes)
	// Magic 0xEF53 is shared by ext2, ext3, and ext4
	if len(buf) > 0x438+2 {
		magic := binary.LittleEndian.Uint16(buf[0x438 : 0x438+2])
		if magic == 0xEF53 {
			// Offset 0x460 (1120): s_feature_incompat
			// EXT4_FEATURE_INCOMPAT_EXTENTS = 0x0040
			if len(buf) > 0x460+4 {
				incompat := binary.LittleEndian.Uint32(buf[0x460 : 0x460+4])
				if incompat&0x40 != 0 {
					return "ext4", nil
				}
			}
			return "ext3", nil
		}
	}

	// 4. Btrfs (Offset 0x10040 = 65600 bytes)
	if len(buf) > 0x10040+8 {
		if string(buf[0x10040:0x10048]) == "_BHRfS_M" {
			return "btrfs", nil
		}
	}

	// 5. Swap (Offset 4086 is for 4K pages)
	if len(buf) > 4086+10 && string(buf[4086:4086+10]) == "SWAPSPACE2" {
		return "swap", nil
	}

	// 6. LVM PV (Offset 0x218 usually has "LVM2 001")
	if bytes.Contains(buf[:1024], []byte("LABELONE")) && bytes.Contains(buf[:1024], []byte("LVM2")) {
		return "lvm_pv", nil
	}

	// 7. ZFS (vdev labels at 8KB, 16KB, etc.)
	// Magic: 0x00bab10c (Little Endian) or 0x0cb1ba00 (Big Endian)
	for _, offset := range []int{0x2000, 0x4000} {
		if len(buf) > offset+8 {
			magic := binary.LittleEndian.Uint64(buf[offset : offset+8])
			if magic == 0x00bab10c || magic == 0x0cb1ba00 {
				return "zfs", nil
			}
		}
	}

	// 8. Zero Check (Verify first 4KB to confirm unformatted)
	isZero := true
	for i := 0; i < 4096 && i < len(buf); i++ {
		if buf[i] != 0 {
			isZero = false
			break
		}
	}
	if isZero {
		return "", nil // Unformatted
	}

	return "unknown", nil
}




func (m *Mounter) MountNativeWithTimeout(source, target, fstype string, options []string, timeout time.Duration) error {
	m.reapRecoveredMounts()

	// 1. Safety Check: Is THIS path already stuck?
	if m.isPathStuck(target) {
		return fmt.Errorf("mount-safety: target %s is already wedged", target)
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
	// For 2026 VFS API, the target must exist.
	if err := os.MkdirAll(target, 0750); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	flags, _ := m.parseMountOptions(options)

	// 2. Open the source tree (Clone if bind mount)
	// OPEN_TREE_CLONE is the modern equivalent of MS_BIND
	var treeFlags uint = unix.OPEN_TREE_CLOEXEC
	if (flags & unix.MS_BIND) != 0 {
		treeFlags |= unix.OPEN_TREE_CLONE | unix.AT_RECURSIVE
	}

	fd, err := unix.OpenTree(unix.AT_FDCWD, source, treeFlags)
	if err != nil {
		return fmt.Errorf("open_tree failed: %w", err)
	}
	defer unix.Close(fd)

	// 3. Apply modern mount attributes (RO, Nosuid, etc.)
	// This is where we fix the "Read-Only window" race.
	attr := &unix.MountAttr{}
	if (flags & unix.MS_RDONLY) != 0 {
		attr.Attr_set |= unix.MOUNT_ATTR_RDONLY
	}
	if (flags & unix.MS_NOSUID) != 0 {
		attr.Attr_set |= unix.MOUNT_ATTR_NOSUID
	}
	if (flags & unix.MS_NODEV) != 0 {
		attr.Attr_set |= unix.MOUNT_ATTR_NODEV
	}
	if (flags & unix.MS_NOEXEC) != 0 {
		attr.Attr_set |= unix.MOUNT_ATTR_NOEXEC
	}

	if attr.Attr_set != 0 {
		// Apply attributes to the detached tree handle
		if err := unix.MountSetattr(fd, "", unix.AT_EMPTY_PATH|unix.AT_RECURSIVE, attr); err != nil {
			return fmt.Errorf("mount_setattr failed: %w", err)
		}
	}

	// 4. Attach the tree to the destination (MoveMount)
	// We use MOVE_MOUNT_F_EMPTY_PATH because fd refers directly to the tree
	err = unix.MoveMount(fd, "", unix.AT_FDCWD, target, unix.MOVE_MOUNT_F_EMPTY_PATH)
	if err != nil {
		return fmt.Errorf("move_mount failed: %w", err)
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

	foundMounts := make(map[string]struct{})
	f, err := os.Open("/proc/self/mountinfo")
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		// Support for very long mountinfo files in 2026
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) > 4 {
				foundMounts[fields[4]] = struct{}{}
			}
		}
	}

	m.stuckMounts.Range(func(key, value any) bool {
		session := key.(*mountSession)

		_, recovered := foundMounts[session.target]
		_, statErr := os.Lstat(session.target)

		if recovered || os.IsNotExist(statErr) {
			// We use the pointer (session) to ensure we only delete
			// the specific attempt that we just verified is recovered.
			m.clearSession(session)
		}
		return true
	})
}

func (m *Mounter) clearSession(session *mountSession) {
	// LoadAndDelete using the pointer address ensures atomicity.
	// If the background goroutine finishes BEFORE the timeout,
	// LoadAndDelete fails (key not in map yet), and count doesn't drop.
	// If it finishes AFTER, it clears exactly its own session.
	if _, deleted := m.stuckMounts.LoadAndDelete(session); deleted {
		m.stuckCount.Add(-1)
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
		default:        data = append(data, opt)
		}
	}
	return flags, strings.Join(data, ",")
}

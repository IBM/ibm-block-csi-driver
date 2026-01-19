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
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

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

// Map to track the current state of a mount target
// Mounter is a warpper of mount.Mounter which has the ability to cancel
// a comand when timeout.
type Mounter struct {
	*mount.Mounter
	executer executer.ExecuterInterface
	// Key: targetPath or volumeID, Value: startTime
	mountTracker sync.Map // map[string]MountState
	stuckUnmounts sync.Map
}

var _ mount.Interface = &Mounter{}

func New(mounterPath string) mount.Interface {
	return &Mounter{
		Mounter:  mount.New(mounterPath).(*mount.Mounter),
		executer: &executer.Executer{},
	}
}

func NewWithExecutor(mounterPath string, e executer.ExecuterInterface) mount.Interface {
	return &Mounter{
		Mounter:  mount.New(mounterPath).(*mount.Mounter),
		executer: e,
	}
}

type MountState int

const (
	StateGraceful MountState = iota // Initial attempts
	StateForced                    // Network hang escalation
	StateDetached                  // Final safety valve
)

type TrackedMount struct {
	FirstAttempt time.Time
	LastState    MountState
}

var mountTracker sync.Map // map[string]TrackedMount


func (m *Mounter) UnmountWithTimeout(target string, timeout time.Duration) error {
	now := time.Now()
	
	// 1. Load or Initialize tracking state
	val, loaded := mountTracker.LoadOrStore(target, TrackedMount{
		FirstAttempt: now,
		LastState:    StateGraceful,
	})
	mInfo := val.(TrackedMount)
	elapsed := now.Sub(mInfo.FirstAttempt)

	// 2. Pre-check: If already detached, return success (Idempotency)
	if mInfo.LastState == StateDetached {
		return nil 
	}

	// 3. Data Integrity Tier: Syncfs
	// Flushes dirty buffers to disk before we attempt to break the connection.
	// Only attempt if we aren't already in a "Force" state.
	if mInfo.LastState == StateGraceful {
		f, err := os.OpenFile(target, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = syscall.Syncfs(int(f.Fd())) 
			f.Close()
		}
	}

	// 4. Helper for asynchronous unmount to protect against D-state hangs
	tryUnmount := func(flags int) <-chan error {
		ch := make(chan error, 1)
		go func() {
			ch <- syscall.Unmount(target, flags)
		}()
		return ch
	}

	// 5. ESCALATION LOGIC
	// Tier 1: Graceful (0-2 Minutes) - Protects against transient blips
	if elapsed < 2*time.Minute {
		select {
		case err := <-tryUnmount(0):
			if err == nil || err == syscall.ENOENT || err == syscall.EINVAL {
				mountTracker.Delete(target)
				return nil
			}
			return fmt.Errorf("graceful unmount failed (will retry): %w", err)
		case <-time.After(timeout):
			return fmt.Errorf("graceful unmount timed out (waiting for retry)")
		}
	}

	// Tier 2: Forced (2-4 Minutes) - Aggressive abort for network hangs
	if elapsed < 4*time.Minute {
		mountTracker.Store(target, TrackedMount{FirstAttempt: mInfo.FirstAttempt, LastState: StateForced})
		select {
		case err := <-tryUnmount(syscall.MNT_FORCE):
			if err == nil || err == syscall.ENOENT || err == syscall.EINVAL {
				mountTracker.Delete(target)
				return nil
			}
			return fmt.Errorf("force unmount failed: %w", err)
		case <-time.After(timeout):
			return fmt.Errorf("force unmount timed out in D-state")
		}
	}

	// Tier 3: Lazy (4+ Minutes) - The Nuclear Option
	// Called when we are close to the K8s 6-minute "Force Detach" limit.
	// Executed on main thread because MNT_DETACH is non-blocking.
	err := syscall.Unmount(target, syscall.MNT_DETACH)
	if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
		mountTracker.Store(target, TrackedMount{FirstAttempt: mInfo.FirstAttempt, LastState: StateDetached})
		return nil
	}

	return fmt.Errorf("all unmount tiers failed: %w", err)
}




func (m *Mounter) UnmountWithTimeout(target string, timeout time.Duration) error {
	// 1. STATE CHECK: Skip tiers if already detached or stuck
	if val, ok := mountTracker.Load(target); ok {
		state := val.(MountState)
		
		if state == StateDetached {
			logger.Infof("Target %s already lazily unmounted; verifying status", target)
			// Return nil (success) if the path is no longer a mount point
			err := syscall.Unmount(target, syscall.MNT_DETACH)
			if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
				return nil 
			}
			return err
		}
		// If StateForcePending, we could either wait or retry MNT_FORCE
	}

	// Helper to execute any blocking unmount in a goroutine
	tryUnmount := func(flags int) <-chan error {
		ch := make(chan error, 1)
		go func() {
			// This call might stay trapped in D-state forever; 
			// the channel buffer ensures the goroutine can eventually exit if it unblocks.
			ch <- syscall.Unmount(target, flags)
		}()
		return ch
	}

	// Optional: Syncfs to flush data before unmounting
	f, err := os.OpenFile(target, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err == nil {
        // syscall.Syncfs flushes all dirty data and metadata for the specific filesystem
        // It's good practice to ensure Syncfs runs even if the mount is already gone
		err = syscall.Syncfs(int(f.Fd()))
		f.Close() // Close the file handle after the syncfs call
		if err != nil && err != syscall.EINVAL { // EINVAL means the mount point might be invalid/gone
			logger.Warnf("syncfs failed for %s: %v", mountPath, err)
		}
	} else {
        logger.Debugf("Could not open mount path %s for syncfs: %v", mountPath, err)
    }
	

	// TIER 1: Graceful
	mountTracker.Store(target, StateGracefulPending)
	select {
	case err := <-tryUnmount(0):
		if err == nil || err == syscall.ENOENT || err == syscall.EINVAL {
			mountTracker.Delete(target)
			return nil
		}
		if err == syscall.EBUSY {
			return m.escalateToLazy(target) // Jump to Tier 3
		}
	case <-time.After(timeout):
		// Graceful timeout; fall through to Tier 2
	}

	// TIER 2: Force
	mountTracker.Store(target, StateForcePending)
	select {
	case err := <-tryUnmount(syscall.MNT_FORCE):
		if err == nil {
			mountTracker.Delete(target)
			return nil
		}
	case <-time.After(timeout):
		// Force timeout; fall through to Tier 3
	}

	// TIER 3: Lazy (Final Fallback)
	return m.escalateToLazy(target)
}

func (m *Mounter) escalateToLazy(target string) error {
	logger.Warnf("Escalating %s to MNT_DETACH", target)
	err := syscall.Unmount(target, syscall.MNT_DETACH)
	if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
		mountTracker.Store(target, StateDetached)
		return nil
	}
	return err
}



func (mounter *Mounter) Unmount(target string) error {
	logger.Infof("Unmounting %s using syscall", target)

	// Open the mount point directory (used for Syncfs FD)
	// Note: mountPath variable needs to be accessible in this scope or passed in
	f, err := os.OpenFile(target, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err == nil {
        // syscall.Syncfs flushes all dirty data and metadata for the specific filesystem
        // It's good practice to ensure Syncfs runs even if the mount is already gone
		err = syscall.Syncfs(int(f.Fd()))
		f.Close() // Close the file handle after the syncfs call
		if err != nil && err != syscall.EINVAL { // EINVAL means the mount point might be invalid/gone
			logger.Warnf("syncfs failed for %s: %v", mountPath, err)
		}
	} else {
        logger.Debugf("Could not open mount path %s for syncfs: %v", mountPath, err)
    }
	

	// Replace exec "umount" with syscall.Unmount
	// The standard syscall performs the necessary kernel operations directly.
	// You can add the MNT_DETACH flag for lazy unmounting if the simple unmount fails.

    err = syscall.Unmount(target, 0)
    if err != nil {
        // Handle a common error for busy devices gracefully by trying lazy unmount
		switch err {
		case syscall.EINVAL, syscall.ENOENT:
			// Already unmounted or target is gone - Success!
			return nil
		case syscall.EBUSY:
			// SAN volumes often stay busy due to monitoring agents or logs
			logger.Warnf("Target %s busy, forcing lazy unmount", target)
            err = syscall.Unmount(target, syscall.MNT_DETACH)
            if err != nil {
                return fmt.Errorf("lazy unmount failed for %s: %w", target, err)
            }
            logger.Infof("Lazy unmount successful for %s", target)
            return nil
		}
        return fmt.Errorf("unmount failed for %s: %w", target, err)
    }

	return nil
}



// Tries to flush in go routine in case flush is stuck
// Also explicit check that mount removed
func (mounter *Mounter) Unmount(target string) error {
	logger.Infof("Unmounting %s using syscall", target)

	// 1. ASYNC SYNCFS: Attempt to flush but don't hang the driver
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		f, err := os.OpenFile(target, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = syscall.Syncfs(int(f.Fd()))
			f.Close()
		}
	}()

	// Wait only 2 seconds for Syncfs; if it takes longer, the fabric is likely dead
	select {
	case <-syncDone:
	case <-time.After(2 * time.Second):
		logger.Warnf("Syncfs timed out for %s; proceeding with unmount", target)
	}

	// 2. PRIMARY UNMOUNT
	err := syscall.Unmount(target, 0)
	if err != nil {
		switch err {
		case syscall.EINVAL, syscall.ENOENT:
			return nil // Already gone
		case syscall.EBUSY:
			logger.Warnf("Target %s busy, applying MNT_DETACH", target)
			if lErr := syscall.Unmount(target, syscall.MNT_DETACH); lErr != nil {
				return fmt.Errorf("lazy unmount failed: %w", lErr)
			}
		default:
			return fmt.Errorf("unmount failed: %w", err)
		}
	}

	// 3. VERIFICATION: Ensure the OS actually dropped the mount
	// Crucial for 2026 CSI to prevent 'NodeUnstage' race conditions
	if !mounter.pollMountDeleted(target, 5*time.Second) {
		return fmt.Errorf("unmount verification failed: %s still in mountinfo", target)
	}

	return nil
}

func (mounter *Mounter) pollMountDeleted(target string, timeout time.Duration) bool {
	expiry := time.Now().Add(timeout)
	for time.Now().Before(expiry) {
		// Use a native mountinfo parser to check for the target path
		if !isMounted(target) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}


func GetDiskFormatNative(device string) (string, error) {
	f, err := os.Open(device)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Increased to 68KB to capture Btrfs superblock at 64KB + offset
	buf := make([]byte, 68*1024)
	// (5) Ensure exact byte count for offset safety
	if _, err := io.ReadFull(f, buf); err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}

	// (2) XFS Detection (Offset 0)
	if bytes.HasPrefix(buf, []byte("XFSB")) {
		// Verification: Check blocksize (log2) at offset 0x4 (usually 12 for 4096)
		if buf[0x4] >= 9 && buf[0x4] <= 16 {
			return "xfs", nil
		}
	}

	// (4) NTFS Detection (Offset 0x03)
	if len(buf) > 0x03+4 && string(buf[0x03:0x03+4]) == "NTFS" {
		return "ntfs", nil
	}

	// (1) EXT4 Detection (Offset 0x438)
	if len(buf) > 0x438+2 {
		magic := binary.LittleEndian.Uint16(buf[0x438 : 0x438+2])
		if magic == 0xEF53 {
			// Verification: Superblock state at 0x436 (1=Clean, 2=Errors)
			state := binary.LittleEndian.Uint16(buf[0x436 : 0x436+2])
			if state == 1 || state == 2 {
				return "ext4", nil
			}
		}
	}

	// (3) Btrfs Detection (Offset 0x10040)
	// Superblock is at 64KiB (0x10000), Magic is at offset 0x40 within it
	if len(buf) > 0x10040+8 {
		if string(buf[0x10040:0x10048]) == "_BHRfS_M" {
			return "btrfs", nil
		}
	}

	// (3) Swap Detection (Offset 4086)
	if len(buf) > 4086+10 && string(buf[4086:4086+10]) == "SWAPSPACE2" {
		return "swap", nil
	}

	// Final check: Is it unformatted or just unknown?
	// Check the first 4KB (standard sector/page size) for any data
	isZero := true
	for _, b := range buf[:4096] {
		if b != 0 {
			isZero = false
			break
		}
	}

	if isZero {
		return "", nil // Definitely unformatted
	}

	return "unknown", nil
}


// NEWER:
func GetDiskFormatNative(device string) (string, error) {
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



var (
	stuckMounts      sync.Map   // map[string]stuckInfo
	stuckCount       int32      // Atomic counter of orphaned mounts
	maxStuckLimit    int32 = 50 // Standard 2026 safety threshold
)

type stuckInfo struct {
	at     time.Now
	source string
}



func (mounter *Mounter) MountNativeWithTimeout(source, target, fstype string, options []string, timeout time.Duration) error {
	// 1. LAZY REAPER: Scan all stuck mounts to see if any recovered
	mounter.reapRecoveredMounts()

	// 2. GATER: Check global health
	if atomic.LoadInt32(&stuckCount) >= maxStuckLimit {
		return fmt.Errorf("node-safety: refused mount; %d wedged (limit %d)", atomic.LoadInt32(&stuckCount), maxStuckLimit)
	}

	// 3. IDEMPOTENCY: Check if this specific target is still stuck
	if _, stuck := stuckMounts.Load(target); stuck {
		// Specific check for this target to be double-sure
		if mounter.isMounted(target) {
			mounter.clearStuck(target)
			return nil
		}
		return fmt.Errorf("mount-safety: target %s is still wedged", target)
	}

	done := make(chan error, 1)
	timer := time.NewTimer(timeout) // More efficient than time.After
	defer timer.Stop()

	go func() {
		err := mounter.MountNative(source, target, fstype, options)
		
		// If the goroutine returns, cleanup the tracking
		mounter.clearStuck(target)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		// 3. ORPHAN: The syscall is stuck in the kernel.
		// We record it so we don't try again on this target.
		stuckMounts.Store(target, time.Now())
		newCount := atomic.AddInt32(&stuckCount, 1)
		
		return fmt.Errorf("mount-safety: timeout after %v; process left in kernel D-state (global: %d)", timeout, newCount)
	}
}



func (mounter *Mounter) reapRecoveredMounts() {
	// Load all currently stuck targets from /proc/self/mountinfo
	// We do this ONCE per entry to avoid O(N^2) complexity
	activeMounts, err := mounter.listAllMounts()
	if err != nil {
		return // If we can't read mountinfo, skip reaping this turn
	}

	stuckMounts.Range(func(key, value interface{}) bool {
		target := key.(string)
		if _, found := activeMounts[target]; found {
			// Kernel finally finished the mount!
			mounter.clearStuck(target)
		}
		return true
	})
}

func (mounter *Mounter) reapRecoveredMounts() {
    f, err := os.Open("/proc/self/mountinfo")
    if err != nil {
        return
    }
    defer f.Close()

    // Create a map to track what we find this pass
    foundMounts := make(map[string]struct{})
    
    scanner := bufio.NewScanner(f)
    // Reuse a single buffer to avoid per-line allocations
    buf := make([]byte, 1024)
    scanner.Buffer(buf, 1024*1024)

    for scanner.Scan() {
        line := scanner.Text()
        // /proc/self/mountinfo format: [ID] [ParentID] [Major:Minor] [Root] [MountPoint] ...
        // We only need the 5th field (index 4)
        fields := strings.Fields(line)
        if len(fields) > 4 {
            foundMounts[fields[4]] = struct{}{}
        }
    }

    // Lazy cleanup: Cross-reference our "stuck" map with the actual mounts
    mounter.stuckMounts.Range(func(key, value interface{}) bool {
        target := key.(string)
        if _, found := foundMounts[target]; found {
            // Kernel finally finished the mount—clear it!
            mounter.clearStuck(target)
        } else {
            // Optionally: If the target dir doesn't exist anymore, it's also not stuck
            if _, err := os.Lstat(target); os.IsNotExist(err) {
                 mounter.clearStuck(target)
            }
        }
        return true
    })
}


// isMounted uses the cached/proc results or a direct check
func (mounter *Mounter) isMounted(target string) bool {
	mounts, _ := mounter.listAllMounts()
	_, found := mounts[target]
	return found
}






// clearStuck ensures the counter and map stay in sync
func (mounter *Mounter) clearStuck(target string) {
	if _, exists := stuckMounts.LoadAndDelete(target); exists {
		atomic.AddInt32(&stuckCount, -1)
	}
}


func (mounter *Mounter) MountNative(source, target, fstype string, options []string) error {
	// 1. Preparation
	if fstype == "bind" {
		if err := mounter.prepareBindMountTarget(target); err != nil {
			return err
		}
	} else {
		if err := os.MkdirAll(target, 0750); err != nil {
			return err
		}
	}

	flags, data := mounter.parseMountOptions(options)

	// 2. Primary Mount
	// For NodeStage: source is /dev/xxx
	// For NodePublish: source is the staging path
	if err := syscall.Mount(source, target, fstype, flags, data); err != nil {
		return fmt.Errorf("initial mount failed: %w", err)
	}
	
	// 2. Publish-Specific Logic (Bind Mounts)
	if (flags & syscall.MS_BIND) != 0 {
		// Pass 2: Apply Read-Only if requested
		// 3. The "Read-Only Bind" Hack
		// Required because Linux ignores MS_RDONLY during the initial MS_BIND
		if (flags & syscall.MS_RDONLY) != 0 {
			remountFlags := flags | syscall.MS_REMOUNT
			if err := syscall.Mount("", target, "", remountFlags, ""); err != nil {
				return fmt.Errorf("ro-remount failed: %w", err)
			}
		}

		// Pass 3: Set Propagation (Publish only)
		// This ensures the mount "leaves" the CSI pod namespace
		// This makes the mount visible to the Kubelet and other Pods.
		// Use MS_SHARED for bidirectional or MS_SLAVE if you want one-way.
		
		if err := syscall.Mount("", target, "", syscall.MS_SHARED, ""); err != nil {
			return fmt.Errorf("failed to set MS_SHARED: %w", err)
		}
	}

	return nil
}

func (mounter *Mounter)) prepareBindMountTarget(target string) error {
	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return err
	}
	// Create the empty file that will act as the mount point
	f, err := os.OpenFile(target, os.O_CREATE, 0660)
	if err != nil {
		return err
	}
	return f.Close()
}

func (mounter *Mounter) parseMountOptions(options []string) (uintptr, string) {
	var flags uintptr
	var data []string

	for _, opt := range options {
		switch opt {
		case "ro":
			flags |= syscall.MS_RDONLY
		// adding syscall.MS_BIND and syscall.MS_RDONLY in a single call will often ignore the read-only flag.
		// The Fix: To reliably bind-mount as read-only, you must perform two syscalls:
        // Create the bind mount (with MS_BIND).
        // Remount it as read-only (with MS_BIND | MS_REMOUNT | MS_RDONLY). 
		case "nosuid":
			flags |= syscall.MS_NOSUID
		case "nodev":
			flags |= syscall.MS_NODEV
		case "noexec":
			flags |= syscall.MS_NOEXEC
		case "bind":
			flags |= syscall.MS_BIND
		// Remounting via syscall.Mount often requires passing the original mount flags along with the new ones. The kernel usually doesn't "merge" them; it replaces them.			
		case "remount":
			flags |= syscall.MS_REMOUNT
		default:
			// Options like 'noatime' or filesystem-specific ones (e.g. 'user_xattr')
			// go into the 'data' string argument.
			data = append(data, opt)
		}
	}
	return flags, strings.Join(data, ",")
}

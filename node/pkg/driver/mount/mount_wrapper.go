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


type TrackedMount struct {
	mu             sync.Mutex
	FirstAttempt   time.Time
	LastState      MountState
	SyncDone       bool
	SyncInProgress bool
}

var mountTracker sync.Map // map[string]*TrackedMount

func (m *Mounter) UnmountWithTimeout(target string, timeout time.Duration) error {
	now := time.Now()

	// 1. Initial Idempotency & Tracker Fetch
	if !isMounted(target) {
		mountTracker.Delete(target)
		return nil
	}

	val, _ := mountTracker.LoadOrStore(target, &TrackedMount{
		FirstAttempt: now,
		LastState:    StateGraceful,
	})
	mInfo := val.(*TrackedMount)

	// 2. State-Locked Pre-check
	mInfo.mu.Lock()
	elapsed := now.Sub(mInfo.FirstAttempt)
	currentState := mInfo.LastState
	shouldStartSync := (currentState == StateGraceful && !mInfo.SyncDone && !mInfo.SyncInProgress)
	if shouldStartSync {
		mInfo.SyncInProgress = true
	}
	mInfo.mu.Unlock()

	// 3. Async Flush with "Ghost" Check
	if shouldStartSync {
		go func(tPath string) {
			// Step A: Attempt the operation
			success := false
			f, err := os.OpenFile(tPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err == nil {
				_ = syscall.Syncfs(int(f.Fd()))
				f.Close()
				success = true
			}

			// Step B: Update state only if the mount is still being tracked
			if currentVal, ok := mountTracker.Load(tPath); ok {
				info := currentVal.(*TrackedMount)
				info.mu.Lock()
				defer info.mu.Unlock()
				
				if success {
					info.SyncDone = true
					info.SyncInProgress = false
				} else {
					// Open failed (e.g., target busy or transient error).
					// Reset InProgress so the NEXT K8s retry will trigger shouldStartSync again.
					info.SyncInProgress = false 
				}
			}
		}(target)
	}

	// 4. Escalation Logic
	var err error
	switch {
	case elapsed < 2*time.Minute:
		err = m.tryUnmount(target, 0, timeout)
	case elapsed < 4*time.Minute:
		mInfo.mu.Lock()
		mInfo.LastState = StateForced
		mInfo.mu.Unlock()
		err = m.tryUnmount(target, syscall.MNT_FORCE, timeout)
	default:
		// Tier 3: Lazy - Kernel handles this asynchronously
		err = syscall.Unmount(target, syscall.MNT_DETACH)
		if err == nil || err == syscall.EINVAL || err == syscall.ENOENT {
			mInfo.mu.Lock()
			mInfo.LastState = StateDetached
			mInfo.mu.Unlock()
		}
	}

	// 5. Final Sanity Check
	if err == nil {
		if m.pollMountDeleted(target, 2*time.Second) {
			mountTracker.Delete(target)
			return nil
		}
		return fmt.Errorf("unmount reported success but %s still in mountinfo", target)
	}

	return err
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
}



// isMounted checks /proc/self/mountinfo for the target path
func isMounted(target string) bool {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 && fields[4] == target {
			return true
		}
	}
	return false
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

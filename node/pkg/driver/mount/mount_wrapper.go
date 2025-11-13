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
    info, ok := mountTracker.Load(target)
    now := time.Now()

    if !ok {
        mountTracker.Store(target, MountInfo{Start: now, Tries: 1})
    } else {
        mInfo := info.(MountInfo)
        mInfo.Tries++
        
        // 1. Transient Zone (0-2 mins): Only try Graceful
        if now.Sub(mInfo.Start) < 2*time.Minute {
             return m.tryGraceful(target, timeout) 
        }

        // 2. Persistent Hang Zone (2-4 mins): Try Force
        if now.Sub(mInfo.Start) < 4*time.Minute {
             return m.tryForce(target, timeout)
        }
        
        // 3. Last Resort Zone (>4 mins): Lazy Unmount
        return m.escalateToLazy(target)
    }
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

func (mounter *Mounter) MountNativeWithTimeout(source, target, fstype string, flags uintptr, data string, options []string, timeout time.Duration) error {
	// 1. Pre-check: Is this volume already wedged?
	if _, stuck := stuckMounts.Load(target); stuck {
		return fmt.Errorf("mount-safety: blocking attempt; previous syscall is still wedged in kernel for %s", target)
	}

	// If the map has an entry, it means the goroutine hasn't returned yet.
	if _, stuck := stuckMounts.Load(target); stuck {
		// Optional: Check if the mount actually finished in the background
		if isMounted(target) {
			 stuckMounts.Delete(target)
			 return nil // Success! It finished while we weren't looking.
		}
		return status.Error(codes.Aborted, "previous mount effort still pending")
	}
	

	done := make(chan error, 1)

	// 2. Start the syscall in a goroutine
	go func() {
		// This thread will hang indefinitely if the kernel hangs
		err := MountNative(source, target, fstype, options)
		
		// If it ever returns, cleanup the tracking
		stuckMounts.Delete(target)
		done <- err
	}()

	// 3. The Wait
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		// 4. THE ORPHAN: We "cut the rope"
		stuckMounts.Store(target, time.Now())
		
		// We return a "retriable" error to K8s. 
		// The goroutine above stays alive in the background (leaked).
		return fmt.Errorf("mount syscall timed out after %v and is orphaned in kernel", timeout)
	}
}

func (mounter *Mounter) MountNative(source, target, fstype string, options []string) error {
	// 1. Ensure target directory (or file for block) exists
	if fstype == "bind" {
		// For raw block, the target must be an empty file, not a directory
		if err := m.prepareBindMountTarget(target); err != nil {
			return err
		}
	} else {
		if err := os.MkdirAll(target, 0750); err != nil {
			return err
		}
	}

	// 2. Parse Options into Flags
	// Standard CSI flags usually include MS_NODEV, MS_NOSUID, etc.
	flags, data := parseMountOptions(options)

	// 3. Handle Bind Mount vs Filesystem Mount
	if fstype == "bind" {
		flags |= syscall.MS_BIND
		// For bind mounts, fstype and data are ignored by the kernel
		fstype = ""
		data = ""
	}

	// 4. Direct Kernel Call
	err := syscall.Mount(source, target, fstype, flags, data)
	if err != nil {
		return fmt.Errorf("syscall.Mount(source=%s, target=%s, type=%s) failed: %w", 
            source, target, fstype, err)
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

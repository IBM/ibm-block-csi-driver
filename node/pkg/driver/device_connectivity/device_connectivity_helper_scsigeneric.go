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

package device_connectivity


import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"sync"
	"time"
	"unicode"
	"unsafe"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"golang.org/x/sys/unix"
)

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperScsiGenericInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperScsiGenericInterface

type OsDeviceConnectivityHelperScsiGenericInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityHelperScsiGenericInterface.
		Mainly for writing clean unit testing, so we can Mock this interface in order to unit test logic.
	*/
	RescanDevicesGetHostIds(lunId int, arrayIdentifiers []string) (map[int]bool, error)
	RescanDevices(lunId int, arrayIdentifiers []string, hostIDs map[int]bool) error
	GetMpathDevice(volumeId string) (string, error)
	FlushMultipathDevice(mpathDevice string) error
	RemovePhysicalDevice(sysDevices []string) error
	RemoveGhostDevice(lun int) error
	IsVolumePathMatchesVolumeId(volumeId string, volumePath string) (bool, error)
}

type OsDeviceConnectivityHelperScsiGeneric struct {
	Executer        executer.ExecuterInterface
	Helper          OsDeviceConnectivityHelperInterface
	MutexMultipathF *sync.Mutex
	CleanScsiDevice bool
}

type WaitForMpathResult struct {
	devicesPaths []string
	err          error
}

var (
	TimeOutMultipathCmd                     = 60 * 1000
	TimeOutMultipathdCmd                    = 10 * 1000
	TimeOutBlockDevCmd                      = 10 * 1000
	TimeOutSgInqCmd                         = 3 * 1000
	MultipathdWildcardsVolumeIdAndMpath     = []string{"%w", "%d"}
	MultipathdWildcardsMpathNameAndVolumeId = []string{"%n", "%w"}
	multipathdWildcardsMpathAndVolumeId     = []string{"%d", "%w"}
)

const (
	DevPath                     = "/dev"
	DevMapperPath               = "/dev/mapper"
	WaitForMpathRetries         = 5
	WaitForMpathWaitIntervalSec = 1
	FcHostSysfsPath             = "/sys/class/fc_remote_ports/rport-*/port_name"
	IscsiHostRexExPath          = "/sys/class/iscsi_host/host*/device/session*/iscsi_session/session*/targetname"
	sysDeviceSymLinkFormat      = "/sys/block/%s/device"
	sysDeviceDeletePathFormat   = sysDeviceSymLinkFormat + "/delete"
	blockDevCmd                 = "blockdev"
	flushBufsFlag               = "--flushbufs"
	mpathdSeparator             = ","
	multipathdCmd               = "multipathd"
	multipathCmd                = "multipath"
	dmsetupCmd                  = "dmsetup"
	WwnOuiEnd                   = 7
	WwnVendorIdentifierEnd      = 16
	procMountsFilePath          = "/proc/mounts"
)

func NewOsDeviceConnectivityHelperScsiGeneric(executer executer.ExecuterInterface, clean_scsi_device bool) OsDeviceConnectivityHelperScsiGenericInterface {
	return &OsDeviceConnectivityHelperScsiGeneric{
		Executer:        executer,
		Helper:          NewOsDeviceConnectivityHelperGeneric(executer),
		MutexMultipathF: &sync.Mutex{},
		CleanScsiDevice: clean_scsi_device,
	}
}

func (r OsDeviceConnectivityHelperScsiGeneric) IsVolumePathMatchesVolumeId(volumeUuid string, volumePath string) (bool, error) {
	logger.Infof("IsVolumePathMatchesVolumeId: Searching matching volume id for volume path: [%s] ", volumePath)
	volumeIdNormalized := r.Helper.normalizeVolume(volumeUuid)

	mpathDeviceName, err := r.Helper.GetMpathDeviceName(volumePath)
	if err != nil {
		return false, err
	}

	SgInqWwn, err := o.GetWwnByScsiInq(mpathDeviceName)
	if err != nil {
		return "", err
	}

	if !isSameId(SgInqWwn, volumeUuid) {
		return false, &ErrorWrongDeviceFound{dmPath, mpathVolumeId, SgInqWwn}
	}

	return true, ""
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevicesGetHostIds(lunId int, arrayIdentifiers []string) (map[int]bool, error) {
	logger.Debugf("Rescan : Start rescan on specific lun, on lun : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	var hostIDs = make(map[int]bool)
	var errStrings []string
	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		logger.Errorf("%s", e.Error())
		return nil, e
	}

	for _, arrayIdentifier := range arrayIdentifiers {
		hostsId, e := r.Helper.GetHostsIdByArrayIdentifier(arrayIdentifier)
		if e != nil {
			logger.Errorf("%s", e.Error())
			errStrings = append(errStrings, e.Error())
		}
		for _, hostId := range hostsId {
			hostIDs[hostId] = true
		}
	}
	if len(hostIDs) == 0 && len(errStrings) != 0 {
		err := errors.New(strings.Join(errStrings, ","))
		return nil, err
	}
	return hostIDs, nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevices(lunId int, arrayIdentifiers []string, hostIDs map[int]bool) error {
	for hostNumber := range hostIDs {

		filename := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", hostNumber)
		f, err := r.Executer.OsOpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200)
		if err != nil {
			logger.Errorf("Rescan Error: could not open filename : {%v}. err : {%v}", filename, err)
			return err
		}

		defer f.Close()

		scanCmd := fmt.Sprintf("- - %d", lunId)
		logger.Debugf("Rescan host device : echo %s > %s", scanCmd, filename)
		if written, err := r.Executer.FileWriteString(f, scanCmd); err != nil {
			logger.Errorf("Rescan Error: could not write to rescan file :{%v}, error : {%v}", filename, err)
			return err
		} else if written == 0 {
			e := &ErrorNothingWasWrittenToScanFileError{filename}
			logger.Errorf("%s", e.Error())
			return e
		}

	}

	logger.Debugf("Rescan : finish rescan lun on lun id : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	return nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(volumeId string) (string, error) {
	logger.Infof("GetMpathDevice: Searching multipath devices for volume : [%s] ", volumeId)

	dmPath, _ := r.Helper.GetMpathDeviceName(volumeId)

	if dmPath != "" {
		SgInqWwn, _ := r.Helper.GetWwnByScsiInq(dmPath)
		if isSameId(SgInqWwn, volumeIdVariations) {
			return dmPath, nil
		}
		logger.Warningf("Expected {%v} but got {%v} from sg_inq", volumeId, SgInqWwn)
	}

	return "", &ErrorWrongDeviceFound{dmPath, volumeIdVariations[0], SgInqWwn}
}

func (r OsDeviceConnectivityHelperScsiGeneric) flushDeviceBuffers(deviceName string) error {
	devicePath := filepath.Join(DevPath, deviceName)
	_, err := r.Executer.ExecuteWithTimeoutSilently(TimeOutBlockDevCmd, blockDevCmd, []string{flushBufsFlag, devicePath})
	if err != nil {
		logger.Errorf("%v %v {%v} did not succeed to flush the device buffers. err={%v}", blockDevCmd, flushBufsFlag, devicePath,
			err.Error())
		return err
	}
	return nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) flushDevicesBuffers(deviceNames []string) error {
	logger.Debugf("executing commands : {%v %v} on devices : {%v} and timeout : {%v} mseconds", blockDevCmd, flushBufsFlag, deviceNames, TimeOutBlockDevCmd)
	for _, deviceName := range deviceNames {
		err := r.flushDeviceBuffers(deviceName)
		if err != nil {
			return err
		}
	}
	logger.Debugf("Finished executing commands: {%v %v}", blockDevCmd, flushBufsFlag)
	return nil
}

// DmIoctl corresponds to struct dm_ioctl in <linux/dm-ioctl.h>
type DmIoctl struct {
    Version      [3]uint32
    DataSize     uint32
    DataStart    uint32
    TargetCount  uint32
    OpenCount    int32
    Flags        uint32
    EventNr      uint32
    Padding      uint32
    Dev          uint64
    Name         [128]byte
    Uuid         [129]byte
    Data         [7]byte // Padding to align
}



const (
    // Correct OpCode for DM_DEV_REMOVE (Cmd 0x04)
    // _IOWR(0xfd, 0x04, 312 bytes)
    DM_DEV_REMOVE = 0xc138fd04 
    
    DM_VERSION_MAJOR = 4
    DM_VERSION_MINOR = 0
    DM_VERSION_PATCH = 0

	// Equivalent to --noflush: drops pending I/O on remove
    // Prevent hanging on dead SAN paths
    DMF_NOFLUSH_FLAG = 1 << 11 
)

// GRACEFUL FLUSH LOGIC
func CleanUnpublish(dmName string) error {
    // 1. Flush buffers
    _ = ioctl(BLKFLSBUF)

    // 2. Try Multipathd Socket
    err := socketWrite("del map " + dmName)
    if err == nil && verifyDeleted(dmName) {
        return nil
    }

    // 3. Socket failed or timed out - Safe Escalation to Kernel
    // We use a standard remove or a deferred remove.
    // We DO NOT set DM_NOFLUSH_FLAG here.
    err = NativeIoctlRemove(dmName, DM_DEFERRED_REMOVE) 
    if err != nil {
        // If the kernel says EBUSY, it means the device is truly in use.
        // Returning an error here allows K8S to retry later.
        return fmt.Errorf("kernel refused removal: %w", err)
    }

    return nil
}

func GracefulUnpublish(dmName, mountPath string) error {
    // 1. Ensure Mount is gone first
    // (Assuming umount was already called by the caller)
    if !pollMountDeleted(mountPath, 10*time.Second) {
        return fmt.Errorf("graceful flush aborted: mount %s still busy", mountPath)
    }

    // 2. Commit data
    _ = ioctl(BLKFLSBUF)

    // 3. Try graceful socket removal
    _ = writeSocket("del map " + dmName)
    
    // 4. Verification Poll
    if pollSysfsDeleted(dmName, 30*time.Second) {
        return nil
    }

    // 5. Safe Escalation (No No-Flush)
    // Using Deferred Remove is best practice here
    _ = NativeIoctlRemove(dmName, 0x1) // 0x1 = DM_DEFERRED_REMOVE

    if pollSysfsDeleted(dmName, 10*time.Second) {
        return nil
    }

    return fmt.Errorf("graceful flush failed: device %s is still present in sysfs", dmName)
}


// FORCEFUL FLUSH LOGIC
func ForcedFlushRogueDevice(ctx context.Context, dmName string, wwid string) error {
	// 1. Fire-and-forget buffer flush
	go func() {
		f, _ := os.Open("/dev/" + dmName)
		_ = ioctl(f.Fd(), BLKFLSBUF) // May hang indefinitely; we don't care
		f.Close()
	}()

	// 2. Unstick via Socket
	_ = writeSocket("disablequeueing map " + dmName)
	_ = writeSocket("fail path " + wwid)
	_ = writeSocket("del map " + dmName)

	// 3. Short poll for success
	if pollSysfsDeleted(dmName, 5*time.Second) {
		return nil
	}

	// 4. NUCLEAR OPTION: Direct Ioctl with No-Flush
	// Escalation: DMF_NOFLUSH_FLAG | DM_DEFERRED_REMOVE
	err := NativeIoctlRemove(dmName, 0x100 | 0x1) 
	if err != nil {
		return fmt.Errorf("nuclear flush failed for %s: %w", dmName, err)
	}

	// 5. Final daemon sync
	_ = writeSocket("del map " + dmName)

	return nil
}

// multipathd process is a wrapper for the socket write - which also handle retries (which we implement here explicitly)
func FlushMultipathDeviceForceNative(ctx context.Context, wfunc FlushMultipathDeviceForceNative(ctx context.Context, wwid string, dmName string) error {
	// 1. Unblock the I/O queue to allow pending I/Os to fail
	// This prevents the 'del map' from hanging indefinitely
	_ = e.multipathdSocketCmd("disablequeueing map " + dmName)
	
	// 2. Explicitly fail all currently queued paths
	// In 2026, this is faster than waiting for transport timeouts
	if wwid != "" {
		_ = e.multipathdSocketCmd("fail path " + wwid) // Optional: only if you have the path IDs
	}

	// 3. Attempt Socket Delete
	// Try the standard management layer first
	if err := e.multipathdSocketCmd("del map " + dmName); err == nil {
		// Wait up to 2 seconds for the sysfs entry to disappear (Nuclear verification)
		for i := 0; i < 20; i++ {
			if _, err := os.Stat("/sys/class/block/" + dmName); os.IsNotExist(err) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// 4. Direct Kernel Remove (The Nuclear Option)
	// If the socket command failed or the device is a zombie, we go direct.
	// NoFlush is critical here: it tells the kernel NOT to wait for 
	// pending I/O completion before removing the map.
	logger.Warnf("Socket delete timed out or failed for %s, using direct ioctl remove (no-flush)", dmName)
	if err := e.NativeIoctlRemoveWithNoFlush(dmName); err != nil {
		return fmt.Errorf("nuclear flush failed for %s: %w", dmName, err)
	}

	return nil
}


func FlushMultipathDeviceForceNative(ctx context.Context, wwid string, dmName string) error {
	// 1. Unblock I/O: Force pending I/O to return errors instead of queuing
	// This is vital for fabrics like iSCSI/FC when the target is gone.
	_ = e.multipathdSocketCmd(ctx, "disablequeueing map "+dmName)

	// 2. Attempt Management Layer Delete
	// We give multipathd a chance to clean up its internal state gracefully.
	err := e.multipathdSocketCmd(ctx, "del map "+dmName)
	if err == nil {
		// Verification loop: Check if the device is actually gone from sysfs
		for i := 0; i < 10; i++ {
			if _, err := os.Stat("/sys/class/block/" + dmName); os.IsNotExist(err) {
				logger.Infof("Multipath map %s successfully deleted via socket", dmName)
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}

	// 3. NUCLEAR OPTION: Direct Kernel Ioctl
	// If the socket is dead, busy, or timed out, we force the kernel to drop the map.
	logger.Warnf("Multipathd socket failed to remove %s, applying direct No-Flush Ioctl", dmName)
	
	// Ensure dmName is just the base (e.g., "dm-5"), not a path
	dmBase := filepath.Base(dmName)
	if err := e.NativeIoctlRemoveWithNoFlush(dmBase); err != nil {
		// If it's EBUSY, the kernel is telling us something still has a reference (mount/open)
		return fmt.Errorf("nuclear flush failed for %s: %w", dmBase, err)
	}

	logger.Infof("Successfully forced removal of %s via direct kernel ioctl", dmBase)
	return nil
}




func pollSysfsDeleted(dmName string, timeout time.Duration) bool {
	sysPath := filepath.Join("/sys/block", dmName)
	expiry := time.Now().Add(timeout)

	for time.Now().Before(expiry) {
		if _, err := os.Stat(sysPath); os.IsNotExist(err) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func ForcedFlushRogueDevice(ctx context.Context, dmName string, wwid string, mountPath string) error {
	// ... [Steps 1-2: Socket Unsticking] ...

	// 3. Block Device & Mount Verification
	if pollSysfsAndMountDeleted(dmName, mountPath, 5*time.Second) {
		return nil
	}

	// 4. NUCLEAR OPTION: Direct Ioctl
	// We use No-Flush to break the I/O hang
	_ = NativeIoctlRemove(dmName, 0x100 | 0x1)

	// 5. Final Mount Cleanup (Lazy Unmount)
	// If the mount is still visible to the OS, we force it out of the VFS
	if isMounted(mountPath) {
		_ = syscall.Unmount(mountPath, syscall.MNT_DETACH) // MNT_DETACH is 'lazy'
	}

	return nil
}

func pollSysfsAndMountDeleted(dmName, mountPath string, timeout time.Duration) bool {
	expiry := time.Now().Add(timeout)
	for time.Now().Before(expiry) {
		devGone := !deviceExists(dmName)
		mountGone := !isMounted(mountPath)
		
		if devGone && mountGone {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}



// GRACEFULL FLUSH -
// IOCTL FLUSH
// SOCKET WRITE
// (IOCTRL WRITE)?)

// FORCE:
// TIMED IOCTL FLUSH
// TRY SOCKET WRITE
// IF FAILS - TRY IOCTL WRITE

// TODO DMF_NOFLUSH_FLAG only for forced flush

func NativeFlushMultipath(dmName string) error {
    // Verify struct size at compile/runtime to prevent EINVAL
	const expectedSize = 312
    if size := unsafe.Sizeof(DmIoctl{}); size != expectedSize {
        return fmt.Errorf("invalid DmIoctl size: expected 312, got %d", size)
    }

    control, err := os.OpenFile("/dev/mapper/control", os.O_RDWR, 0)
    if err != nil {
        return fmt.Errorf("failed to open /dev/mapper/control: %w", err)
    }
    defer control.Close()

    data := DmIoctl{
        Version:   [3]uint32{DM_VERSION_MAJOR, DM_VERSION_MINOR, DM_VERSION_PATCH},
		DataSize:  uint32(expectedSize),
		DataStart: uint32(expectedSize), // Points to end of struct as there is no extra payload
		// DMF_NOFLUSH_FLAG (0x1) prevents the kernel from trying to sync 
		// dirty buffers on a dead fabric, avoiding D-state hangs.		
		// TODO DMF_NOFLUSH_FLAG is 1 < 11
        Flags:     DMF_NOFLUSH_FLAG,
    }
    copy(data.Name[:], dmName)

    // Execute DM_DEV_REMOVE
    _, _, errno := syscall.Syscall(
        syscall.SYS_IOCTL,
        control.Fd(),
        uintptr(DM_DEV_REMOVE),
        uintptr(unsafe.Pointer(&data)),
    )

    if errno != 0 {
        // Handle EBUSY (Device in use)
        if errno == syscall.EBUSY {
            // After the ioctl call, the kernel updates data.OpenCount 
            // even if it returns EBUSY.
            return fmt.Errorf("dm_dev_remove EBUSY: %s still has %d open references", 
                dmName, data.OpenCount)
        }
        
        // Handle ENOENT (Already removed)
        if errno == syscall.ENOENT {
            return nil 
        }

        return fmt.Errorf("dm_dev_remove ioctl failed for %s: %w", dmName, errno)
    }

    return nil
}










const (
	// IOCTL Command IDs (Standard x86_64)
	DM_DEV_REMOVE    = 0xc138fd04
	DM_DEV_SUSPEND   = 0xc138fd06
	
	// IOCTL Flags
	DM_SUSPEND_FLAG    = 1 << 1  // Used to freeze I/O
	DM_NOFLUSH_FLAG    = 1 << 8  // Critical: do not hang on dead paths
	DM_DEFERRED_REMOVE = 1 << 17 // Standard for CSI Unstage
)


func (o *OsDeviceConnectivityHelperScsiGeneric) DestroyDevice(dmName string, force bool) error {
	// 1. CONTROL PLANE: Talk to the daemon
	// Replaces 'multipathd disablequeueing map ...'
	_, _ = o.MultipathdSocketCmd(fmt.Sprintf("disablequeueing map %s", dmName))

	if force {
		// Replaces 'multipathd fail path ...'
		slaves, _ := o.GetSlaves(dmName)
		for _, slave := range slaves {
			_, _ = o.MultipathdSocketCmd(fmt.Sprintf("fail path %s", slave))
		}
	}

	// 2. DATA PLANE: The IOCTL removal
	var flags uint32
	if force {
		// Use NOFLUSH to prevent hanging on dead array ports
		flags = DM_NOFLUSH_FLAG 
		// Optional: Pre-suspend to break any pending bio locks
		_ = o.ExecuteDmIoctl(DM_DEV_SUSPEND, dmName, DM_SUSPEND_FLAG|DM_NOFLUSH_FLAG)
	} else {
		// Standard CSI teardown
		flags = DM_DEFERRED_REMOVE
	}

	err := o.ExecuteDmIoctl(DM_DEV_REMOVE, dmName, flags)
	
	// 3. SYNC: Final socket call to ensure daemon is clean
	_, _ = o.MultipathdSocketCmd(fmt.Sprintf("del map %s", dmName))
	
	return err
}




func (o *OsDeviceConnectivityHelperScsiGeneric) SafeFlushBuffer(devPath string) error {
	f, err := os.OpenFile(devPath, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	// Use a channel to handle the potential D-state hang
	done := make(chan error, 1)
	go func() {
		// BLKFLSBUF = 0x1261
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x1261, 0)
		if errno != 0 {
			done <- errno
		}
		done <- nil
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		logger.Warningf("BLKFLSBUF timed out for %s - continuing without flush", devPath)
		return fmt.Errorf("flush timeout")
	}
}

func (o *OsDeviceConnectivityHelperScsiGeneric) DestroyDmSequence(dmName string, isForce bool) {
	devPath := filepath.Join("/dev", dmName)

	// 1. PRE-FLIGHT: Stop the queueing behavior first
	_, _ = o.MultipathdSocketCmd(fmt.Sprintf("disablequeueing map %s", dmName))

	// 2. FLUSH: Only if graceful
	if !isForce {
		_ = o.SafeFlushBuffer(devPath)
	}

	// 3. REMOVE: Targeted IOCTLs
	var flags uint32 = DM_NOFLUSH_FLAG
	if !isForce {
		flags |= DM_DEFERRED_REMOVE
	}

	err := o.ExecuteDmIoctl(DM_DEV_REMOVE, dmName, flags)
	
	// 4. CLEANUP: Final socket and orphan scan
	_, _ = o.MultipathdSocketCmd(fmt.Sprintf("del map %s", dmName))
	if isForce {
		o.PruneAllOrphanPaths(expectedLun, arrayIdentifiers)
	}
}




func (o *OsDeviceConnectivityHelperScsiGeneric) PruneAllOrphanPaths(expectedLun int, arrayIdentifiers []string) {
    // 1. Get all SCSI devices currently known to the OS
    devices, _ := filepath.Glob("/sys/class/scsi_device/*")

    for _, devPath := range devices {
        // devPath is like /sys/class/scsi_device/1:0:0:1
        hctl := filepath.Base(devPath)
        
        // 2. Only target devices belonging to OUR LUN and OUR Array
        if !o.isDeviceOurs(hctl, expectedLun, arrayIdentifiers) {
            continue
        }

        // 3. Identify SD name (e.g., sdb)
        sdName := o.getSdNameFromHctl(hctl)

        // 4. Force failure in Multipathd Socket
        // This is better than relying on the DM slave list
        _, _ = o.MultipathdSocketCmd(fmt.Sprintf("fail path %s", sdName))

        // 5. Final Kernel Deletion
        deletePath := filepath.Join(devPath, "device/delete")
        _ = os.WriteFile(deletePath, []byte("1"), 0644)
    }
}


func (o *OsDeviceConnectivityHelperScsiGeneric) PruneAllOrphanPaths(expectedWWID string) {
	// 1. Get all SCSI devices
	devices, _ := filepath.Glob("/sys/class/scsi_device/*")

	for _, devPath := range devices {
		// 2. Read the WWID from sysfs (This is the kernel's cached UUID)
		// Path: /sys/class/scsi_device/H:C:T:L/device/wwid
		wwidBytes, err := os.ReadFile(filepath.Join(devPath, "device/wwid"))
		if err != nil {
			continue
		}

		currentWWID := strings.TrimSpace(string(wwidBytes))

		// 3. Compare UUIDs (case-insensitive)
		if strings.EqualFold(currentWWID, expectedWWID) {
			hctl := filepath.Base(devPath)
			sdName := o.getSdNameFromHctl(hctl)

			logger.Infof("Found orphan path %s (HCTL: %s) for WWID %s. Pruning...", sdName, hctl, expectedWWID)

			// 4. Socket: Fail the path first so multipathd drops it
			if sdName != "" {
				_, _ = o.MultipathdSocketCmd(fmt.Sprintf("fail path %s", sdName))
			}

			// 5. Kernel: Immediate Deletion
			deletePath := filepath.Join(devPath, "device/delete")
			_ = os.WriteFile(deletePath, []byte("1"), 0644)
		}
	}
}







func (r OsDeviceConnectivityHelperScsiGeneric) FlushMultipathDevice(mpathDevice string) error {
	err := r.flushDeviceBuffers(mpathDevice)
	if err != nil {
		return err
	}

	// mpathdevice is dm-4 for example
	logger.Debugf("Flushing mpath device : {%v}", mpathDevice)

	fullDevice := filepath.Join(DevPath, mpathDevice)

	logger.Debugf("Try to acquire lock for running the command multipath -f {%v} (to avoid concurrent multipath commands)", mpathDevice)
	r.MutexMultipathF.Lock()
	logger.Debugf("Acquired lock for multipath -f command")
	_, err = r.Executer.ExecuteWithTimeout(TimeOutMultipathCmd, multipathCmd, []string{"-f", fullDevice})
	r.MutexMultipathF.Unlock()

	if err != nil {
		if _, e := os.Stat(fullDevice); os.IsNotExist(e) {
			logger.Debugf("Mpath device {%v} was deleted", fullDevice)
		} else {
			logger.Errorf("multipath -f {%v} did not succeed to delete the device. err={%v}", fullDevice, err.Error())
			return err
		}
	}

	logger.Debugf("Finished flushing mpath device : {%v}", mpathDevice)
	return nil
}

// flushDeviceBuffers sends the BLKFLSBUF ioctl (0x1261)
func flushDeviceBuffers(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	// 0x1261 is BLKFLSBUF
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x1261, 0)
	if errno != 0 && errno != syscall.ENOTTY {
		return errno
	}
	return nil
}

// verifyDisappearance checks sysfs to ensure the DM device is actually gone.
func verifyDisappearance(dmName string) bool {
	sysPath := filepath.Join("/sys/block", dmName)
	for i := 0; i < 10; i++ { // Wait up to 1s for udev/kernel sync
		if _, err := os.Stat(sysPath); os.IsNotExist(err) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// ForcedFlushDM performs a high-reliabili
// A wrapper for ioctrl flush that tries to use a time limited flush
func (e *Executer) ForcedFlushWithPotentialSync(dmName string) error {
	devicePath := filepath.Join("/dev/mapper", dmName)
	
	// 1. ASYNC SYNC: Try to flush buffers in a goroutine
	done := make(chan struct{}, 1)
	go func() {
		f, err := os.OpenFile(devicePath, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err == nil {
			// BLKFLSBUF = 0x1261
			_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x1261, 0)
			f.Close()
		}
		close(done)
	}()

	// 2. WAIT WITH PATIENCE
	select {
	case <-done:
		// Flush finished or failed quickly. Perfect.
	case <-time.After(2 * time.Second):
		// It's hanging! The path is likely dead.
		// We ABANDON the flush and move straight to the nuclear option.
		logger.Warnf("Graceful flush hanging for %s, abandoning and forcing removal", dmName)
	}

	// 3. THE NUCLEAR OPTION: Direct ioctl with NOFLUSH
	// This will succeed immediately even if the goroutine above is still stuck in the kernel.
	return e.NativeIoctlRemoveWithNoFlush(dmName)
}



















import (
    "bufio"
    "os"
    "strings"
)

// IsDeviceMounted checks if the specific DM device is still in use as a mount point
func IsDeviceMounted(dmName string) (bool, error) {
    file, err := os.Open("/proc/self/mountinfo")
    if err != nil {
        return false, err
    }
    defer file.Close()

    scanner := bufio.NewScanner(file)
    for scanner.Scan() {
        line := scanner.Text()
        // Format: [id] [parent] [major:minor] [root] [mountpoint] [options] ...
        if strings.Contains(line, dmName) {
            return true, nil
        }
    }
    return false, scanner.Err()
}


import (
    "os/exec"
    "strconv"
    "strings"
)

// GetDMOpenCount returns the number of active openers for a DM device
func GetDMOpenCount(dmName string) (int, error) {
    // dmsetup info -c --noheadings -o open <name>
    out, err := exec.Command("dmsetup", "info", "-c", "--noheadings", "-o", "open", dmName).Output()
    if err != nil {
        return 0, err
    }
    
    countStr := strings.TrimSpace(string(out))
    return strconv.Atoi(countStr)
}


func CleanupGhostDevice(dmName string) error {
    isMounted, _ := IsDeviceMounted(dmName)
    openCount, _ := GetDMOpenCount(dmName)

    if !isMounted && openCount == 0 {
        // Safe ghost: Try normal removal first
        return exec.Command("dmsetup", "remove", dmName).Run()
    }

    if isMounted {
        // Still mounted? This isn't a ghost; it's a conflict.
        return fmt.Errorf("device %s is still mounted; cannot treat as ghost", dmName)
    }

    // Ghost with open_count > 0 or stuck I/O
    // ESCALATE IMMEDIATELY: No 2-minute wait for ghost cleanup
    return exec.Command("dmsetup", "remove", "--force", "--retry", dmName).Run()
}









func (r OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(sysDevices []string) error {
	flushErr := r.flushDevicesBuffers(sysDevices)
	if flushErr != nil {
		return flushErr
	}

	// sysDevices  = sdb, sda,...
	logger.Debugf(`Removing scsi device : {%v} by writing "1" to the delete file of each device: {%v}`, sysDevices, fmt.Sprintf(sysDeviceDeletePathFormat, "<deviceName>"))
	// NOTE: this func could be also relevant for SCSI (not only for iSCSI)
	var (
		f   *os.File
		err error
	)

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}

		filename := fmt.Sprintf(sysDeviceDeletePathFormat, deviceName)

		if f, err = os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200); err != nil {
			if os.IsNotExist(err) {
				logger.Warningf("Idempotency: Block device {%v} was not found on the system, so skip deleting it", deviceName)
				continue
			} else {
				logger.Errorf("Error while opening file : {%v}. error: {%v}", filename, err.Error())
				return err
			}
		}

		defer f.Close()

		if _, err := f.WriteString("1"); err != nil {
			logger.Errorf("Error while writing to file : {%v}. error: {%v}", filename, err.Error())
			return err // TODO: maybe we need to just swallow the error and continnue??
		}

		//    if err != nil && !errors.Is(err, os.ErrNotExist) {
		//return fmt.Errorf("failed to delete stale path %s: %w", deviceName, err)
		//}

	}
	logger.Debugf("Finished removing SCSI devices : {%v}", sysDevices)
	return nil
}

// normalizeLun converts sysfs LUN strings (hex or decimal) to a standard decimal string.
// Example: "0x0001000000000000" -> "1"
// Example: "1" -> "1"
func normalizeLun(lunStr string) string {
	lunStr = strings.TrimSpace(lunStr)
	if lunStr == "" {
		return ""
	}

	// If it starts with 0x, it's a hexadecimal representation (Common in 2025 Kernels)
	if strings.HasPrefix(lunStr, "0x") {
		// Parse the hex string to a 64-bit unsigned integer
		val, err := strconv.ParseUint(lunStr, 0, 64)
		if err != nil {
			return lunStr // Fallback to raw string if parsing fails
		}

		// SCSI LUNs in sysfs often use the "Peripheral Device Addressing" format.
		// For LUNs 0-255, the integer value is exactly what we expect.
		// For larger/hierarchical LUNs, we return the full integer string.
		return fmt.Sprintf("%d", val)
	}

	// If it's already a decimal string, return it as-is
	return lunStr
}

func (m *Mounter) IsStaged(targetPath string) (bool, error) {
    // 1. Check if the directory exists
    notMnt, err := m.IsLikelyNotMountPoint(targetPath)
    if err != nil {
        if os.IsNotExist(err) {
            return false, nil // Not staged if path doesn't exist
        }
        return false, err
    }

    // 2. If it is a mount point, it is staged
    return !notMnt, nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) VerifyStagingMount(stagingPath string, expectedSerial string) error {
	// 1. Check if the staging path is currently a mount point
	// Note: We use a mounter helper or parse /proc/mounts
	isMounted, devicePath, err := r.Mounter.IsStaged(stagingPath)
	if err != nil {
		return false, fmt.Errorf("failed to check mount status of %s: %w", stagingPath, err)
	}
	if !isMounted {
		logger.Debugf("Staging path %s is clean (not mounted).", stagingPath)
		return false, nil
	}

	// 2. Identify the device currently at that mount point
	// If the mount exists, we MUST verify its hardware identity (WWN)
	actualSerial, err := r.GetDeviceWWN(devicePath)
	if err != nil {
		// If we can't query the hardware, the mount is likely a "zombie" (dead paths)
		logger.Warnf("Mount exists at %s on %s but hardware is unreachable. Unmounting stale mount.", stagingPath, devicePath)
		return false, r.Mounter.Unmount(stagingPath)
	}

	// 3. Compare Serial (Case-insensitive match for 2025 standards)
	if !strings.Contains(strings.ToLower(actualSerial), strings.ToLower(expectedSerial)) {
		logger.Warnf("STALE MOUNT DETECTED: Path %s is mounted to %s (Serial: %s), but expected %s. Remediation required.",
			stagingPath, devicePath, actualSerial, expectedSerial)

		// 4. Action: Clear the path so the subsequent scan/stage has a clean environment
		if err := r.Mounter.Unmount(stagingPath); err != nil {
			return fmt.Errorf("failed to unmount stale volume at %s: %w", stagingPath, err)
		}
		logger.Infof("Successfully cleared stale mount from %s", stagingPath)
		return false, nil
	}

	return true, nil
}

// VerifyAndGetDmDevice performs a global conflict audit and internal triple-check.
// Returns the confirmed dm device path (e.g., "/dev/dm-5") or an error.
func (r *OsDeviceConnectivityHelperScsiGeneric) VerifyAndGetDmDevice(volumeUuid string, lun int) (string, error) {
	expectedSerial := strings.ToLower(volumeUuid)
	expectedLunStr := fmt.Sprintf("%d", lun)
	expectedMpathUuid := "mpath-" + expectedSerial

	// 1. GLOBAL CONFLICT AUDIT
	// Ensure the serial isn't already claimed by a DIFFERENT dm device.
	allDmDirs, _ := filepath.Glob("/sys/block/dm-*")
	for _, dmDir := range allDmDirs {
		if dm == currentDmName {
			continue
		}

		uuidContent, err := os.ReadFile(filepath.Join(dmDir, "dm/uuid"))
		if err != nil {
			continue
		}

		actualUuid := strings.TrimSpace(string(uuidContent))
		// Check for identity match (handling common mpath-3 / mpath- prefix variations)
		if r.IsSerialMatch(actualUuid, expectedSerial) {
			dmName := filepath.Base(dmDir)

			// Check if this is a "zombie" or an active device
			holders, _ := os.ReadDir(filepath.Join(dmDir, "holders"))
			if len(holders) > 0 {
				// If holders exist and it's NOT our current target, we have a fatal safety conflict
				return "", fmt.Errorf("FATAL: Serial %s is already in use by active device %s", volumeUuid, dmName)
			}

			// If it's a stale map for our serial, clean it up before proceeding
			logger.Warnf("Found stale multipath map %s for serial %s. Removing.", dmName, volumeUuid)
			forceFlushDM(..)
			//_, _ = r.Executer.Execute("dmsetup", "remove", "-f", dmName)
		}
	}

	// TODO Discover based on uuid (this version) OR discover baese on dm name (and then cleanup is pre-scan step)

	// 2. DISCOVERY & INTERNAL TRIPLE-CHECK
	// Re-scan dm devices to find the one we just verified/cleaned
	targetDm := ""
	for _, dmDir := range allDmDirs {
		uuidContent, _ := os.ReadFile(filepath.Join(dmDir, "dm/uuid"))
		if r.IsSerialMatch(string(uuidContent), expectedSerial) {
			targetDm = filepath.Base(dmDir)
			break
		}
	}

	if targetDm == "" {
		return "", fmt.Errorf("no multipath device found in kernel for serial %s", volumeUuid)
	}

	// 3. SLAVE (PATH) AUDIT
	slaveDir := fmt.Sprintf("/sys/block/%s/slaves", targetDm)
	slaves, _ := os.ReadDir(slaveDir)
	validPathsFound := 0

	for _, slave := range slaves {
		name := slave.Name()
		devPath := filepath.Join(slaveDir, name, "device")


		// readSysfs:		lunBytes, err := os.ReadFile(filepath.Join(slavePath, "lun"))
		// if err != nil {
		//	logger.Errorf("Cannot read LUN for %s: %v", sdName, err)
		//	continue // Skip path if metadata cannot be read
		//}
		//actualLun := strings.TrimSpace(string(lunBytes))


		var actualLun, sysfsId, hwId string
		if strings.HasPrefix(name, "nvme") {
			// NVMe Path Logic
			actualLun = readSysfs(fmt.Sprintf("/sys/block/%s/device/nsid", name))
			sysfsId = readSysfs(fmt.Sprintf("/sys/block/%s/nguid", name))
			hwId = sysfsId // NVMe ID is authoritative in sysfs
		} else {
			// SCSI Path Logic
			actualLun = normalizeLun(readSysfs(fmt.Sprintf("/sys/block/%s/device/lun", name)))
			sysfsId = readSysfs(fmt.Sprintf("/sys/block/%s/device/wwid", name))
			hwId, err = r.GetDeviceWWN("/dev/" + name) // Direct SG_IO IOCTL
		}

		isBad := false
		if err != nil || !r.IsSerialMatch(hwId, expectedSerial) || actualLun != normLun {
			isBad = true
		} else if sysfsId != "" && !r.IsSerialMatch(sysfsId, hwId) {
			isBad = true // Identity Split: Kernel believes one thing, Hardware says another
		}

//"Hardware inquiry failed"
			//reason = fmt.Sprintf("LUN Mismatch (got %s, exp %s)", actualLun, expectedLun)
			//reason = fmt.Sprintf("Hardware Serial mismatch (got %s, exp %s)", hwSerial, expectedSerial)
			//reason = fmt.Sprintf("Kernel/Hardware Identity Split (Sysfs: %s, HW: %s)", sysfsSerial, hwSerial)

		if isBad {
			logger.Warnf("Pruning unsafe path %s from %s (Serial/LUN mismatch)", name, targetDm)
			deletePath := fmt.Sprintf("/sys/block/%s/device/delete", name)
			if strings.HasPrefix(name, "nvme") {
				deletePath = fmt.Sprintf("/sys/block/%s/device/device/remove", name)
			}
			_ = os.WriteFile(deletePath, []byte("1"), 0644)


			//if err != nil && !errors.Is(err, os.ErrNotExist) {
			//return fmt.Errorf("failed to delete stale path %s: %w", deviceName, err)

			continue
		}

		if strings.TrimSpace(string(stateBytes)) == "offline" {

			// D. Recovery: Force offline paths to running
			// Revive paths
			statePath := fmt.Sprintf("/sys/block/%s/device/state", name)
			_ = os.WriteFile(statePath, []byte("running"), 0644)
		}
		validPathsFound++
	}

	if validPathsFound == 0 {
		return "", fmt.Errorf("all paths for device %s failed safety verification", targetDm)
	}

	return "/dev/" + targetDm, nil
}

func (o *OsDeviceConnectivityHelperScsiGeneric) FullPreScanSanityCleanup(targetWWID string) {
    // 1. THE DM SCAN (O(N_dm)): Find any hidden DM maps
    // Even if udev is dead, sysfs knows the truth.
    dmUUIDs, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
    for _, path := range dmUUIDs {
        if data, _ := os.ReadFile(path); strings.Contains(string(data), targetWWID) {
            // Found a stale/hidden DM. Flush it.
            dmName := strings.Split(path, "/")[3] // e.g., dm-5
            _ = forceFlushDM(targetWWID, dmName)
        }
    }
}


func (o *OsDeviceConnectivityHelperScsiGeneric) cleanupVolumeDevices(volUuid) {
	expectedSerial := strings.ToLower(volUuid)

	// 2. DEVICE LOOKUP
	targetDm := r.findDmByUuid(expectedSerial)
	if targetDm == "" { return nil }
	slaves := r.getSlaves(targetDm)

	// 3. HOLDERS CHECK (Pre-Removal)
	holders, _ := os.ReadDir(fmt.Sprintf("/sys/block/%s/holders", targetDm))
	if len(holders) > 0 {
		return fmt.Errorf("UNSTAGE FAILURE: Cannot remove %s, active holders exist", targetDm)
	}

	// 4. QUEUE FLUSH & MAP REMOVAL
	// Fail the path group immediately to stop retries during deletion
	_ = r.Executer.Execute("dmsetup", "message", targetDm, "0", "fail_if_no_path")
	// If this fails, it might not support the message; we proceed anyway.

	_ = r.Executer.Execute(dmsetupCmd, "remove", "-f", targetDm)

	// 5. SLAVE CLEANUP (Ghost Prevention)
	for _, sd := range slaves {
		// Final buffer flush to the physical disk
		_ = r.Executer.Execute("blockdev", "--flushbufs", "/dev/"+sd)
		// Delete the SCSI structure from kernel
		delPath := fmt.Sprintf("/sys/block/%s/device/delete", sd)
		if strings.HasPrefix(sd, "nvme") {
			delPath = fmt.Sprintf("/sys/block/%s/device/device/remove", sd)
		}
		_ = os.WriteFile(delPath, []byte("1"), 0644)
		//if err != nil && !errors.Is(err, os.ErrNotExist) {
		//return fmt.Errorf("failed to delete stale path %s: %w", deviceName, err)
	}
}

func (o *OsDeviceConnectivityHelperScsiGeneric) RemoveGhostDevice(expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	if !o.CleanScsiDevice {
		logger.Debugf("Clean devices disabled, skipping removeGhostDevice") //Can be omitted, debug only.
		return nil
	}
	sgEntries, err := os.ReadDir("/sys/class/scsi_generic")
	if err != nil {
		if os.IsNotExist(err) { return nil }
		return fmt.Errorf("failed to read scsi_generic: %w", err)
	}

	var (
		deleted int = 0
		notLun  int = 0
		notPQ   int = 0
	)

	normLun := normalizeLun(fmt.Sprintf("%d", expectedLun))
	
	

	for _, entry := range sgEntries {
		sgName := entry.Name()
		deviceDir := filepath.Join("/sys/class/scsi_generic", sgName, "device")
		
		// 1. Get HCTL and Path
		hctl, err := GetHCTLFromSg(sgName)
		if err != nil {
			continue 
		}

		// 2. Validate LUN Match
		lunBytes, _ := os.ReadFile(filepath.Join(deviceDir, "lun"))
		if normalizeLun(string(lunBytes)) != normLun {
			notLun++
			continue
		}

		// 1. TRANSPORT SCOPE CHECK
		realPath, err := filepath.EvalSymlinks(deviceDir)
		if err != nil { continue }
		
		isOurPath := isPathOwnedByMyArray(hctl, arrayIdentifiers)

		// 2. VENDOR CHECK (IBM)
		vendorBytes, _ := os.ReadFile(filepath.Join(deviceDir, "vendor"))
		vendor := strings.TrimSpace(string(vendorBytes))
		isIBM := strings.Contains(vendor, "IBM")




		// 5. Ghost/Serial Logic
		// Read the actual hardware serial from the device (e.g., from 'wwid' file)
		wwidBytes, _ := os.ReadFile(filepath.Join(deviceDir, "wwid"))
		hwSerial := string(wwidBytes)
		
		isGhost := IsGhostDevice(sgName) // Ensure this checks PQ=1 via sysfs 'type' or inquiry
		
		// REMEDIATION: Delete if it's a ghost from IBM, or if it's our path but the serial is wrong
		shouldDelete := (isGhost && isIBM) || (isIBM && !o.IsSerialMatch(hwSerial, expectedSerial))


		

		// 6. REMEDIATION LOGIC
		// We delete if it's a Ghost OR if the hardware identity is wrong
		shouldDelete := (isGhost && isIBM) || (isOurPath && (!isIBM || !o.IsSerialMatch(hwSerial, expectedSerial)))

		if shouldDelete {
			reason := "serial mismatch"
			if isGhost {
				reason = "IBM PQ=1 Ghost (No block device)"
			} else if hwErr != nil {
				reason = fmt.Sprintf("IBM path inquiry failed: %v", hwErr)
			}

			logger.Warnf("Pruning stale IBM device %s. Reason: %s", sgName, reason)

			deletePath := filepath.Join(deviceDir, "delete")
			if err := os.WriteFile(deletePath, []byte("1"), 0644); err != nil {
				return fmt.Errorf("failed to delete IBM stale device %s: %w", sgName, err)
				// Perhaps skip
			}
			else {
				deleted += 1
			}
		}
		else {
			notPQ += 1
		}
	}
	if deleted != 0 {
		logger.Debugf("Deleted %d devices. Found %d not-our-lun devices, and %d our lun but non ghost", deleted, notLun, notPQ)
	}
	return nil
}

func GetHCTLFromSg(sgName string) (string, error) {
	// sysPath: /sys/class/scsi_generic/sg5
	// This symlink is provided by the kernel specifically to link the generic 
	// interface to the underlying SCSI device object.
	deviceLink := filepath.Join("/sys/class/scsi_generic", sgName, "device")

	realPath, err := filepath.EvalSymlinks(deviceLink)
	if err != nil {
		return "", fmt.Errorf("failed to resolve SCSI device link for %s: %w", sgName, err)
	}

	// The HCTL (Host:Channel:Target:LUN) is the base name of the leaf directory.
	// Example: /sys/devices/pci0000:00/.../target4:0:0/4:0:0:1 -> "4:0:0:1"
	hctl := filepath.Base(realPath)

	// 3. Validation
	// Standard SCSI HCTL always has exactly 3 colons (4 parts).
	if strings.Count(hctl, ":") != 3 {
		// Fallback: If it's a newer NVMe-oF device, the naming might differ.
		// For CSI/SCSI, we treat anything without 3 colons as an error.
		return "", fmt.Errorf("invalid HCTL format '%s' for device %s", hctl, sgName)
	}

	return hctl, nil
}

func isPathOwnedByMyArray(hctl string, arrayIdentifiers []string) bool {
    // Resolve the real path once: /sys/devices/pci.../session1/target1:0:0/1:0:0:1
    realPath, _ := filepath.EvalSymlinks(fmt.Sprintf("/sys/class/scsi_device/%s/device", hctl))
    for _, id := range arrayIdentifiers {
        if strings.Contains(strings.ToLower(realPath), strings.ToLower(id)) {
            return true
        }
    }
    return false
}

// sgIoHdr is the Linux SG_IO ioctl structure
type sgIoHdr struct {
	interface_id    int32
	dxfer_direction int32
	cmd_len         uint8
	mx_sb_len       uint8
	iovec_count     uint16
	dxfer_len       uint32
	dxferp          uintptr
	cmdp            uintptr
	sbp             uintptr
	timeout         uint32
	flags           uint32
	pack_id         int32
	usr_ptr         uintptr
	status          uint8
	masked_status   uint8
	msg_status      uint8
	sb_len_wr       uint8
	host_status     uint16
	driver_status   uint16
	resid           int32
	duration        uint32
	info            uint32
}



const (
	SG_IO             = 0x2285
	SG_DXFER_FROM_DEV = -3
)





// IsGhostDevice returns true if the device is identified as PQ=1 (Hardware not connected).
func IsGhostDevice(sgName string) (bool, error) {
	// 1. SYSFS STATE CHECK (The "Pre-flight")
	// States: running, offline, cancelled, deleting, blocked
	// TODO double check offline
	deviceBase := fmt.Sprintf("/sys/class/scsi_generic/%s/device", sgName)
	if state, err := os.ReadFile(filepath.Join(deviceBase, "state")); err == nil {
		s := strings.TrimSpace(string(state))
		if s == "offline" || s == "cancelled" || s == "deleting" {
			// Kernel has already given up on the hardware
			return true, nil
		}
	}
	// 1. FAST CHECK: Sysfs Type 31 (Direct Mapping of PQ=1)
	typePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/type", sgName)
	if data, err := os.ReadFile(typePath); err == nil {
		// Kernel maps PQ=1 to Type 31 (0x1f)
		if strings.TrimSpace(string(data)) == "31" {
			return true, nil
		}
	}

	// 2. FAST CHECK: Missing Block Directory
	// For sd devices, if the 'block' symlink is missing, it's a ghost.
	blockPath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/block", sgName)
	
	_, err := os.Stat(blockPath)
	isMissingBlock := os.IsNotExist(err)

	typeData, _ := os.ReadFile(filepath.Join(deviceBase, "type"))
	isDiskType := strings.TrimSpace(string(typeData)) == "0"

	if isDiskType && isMissingBlock {
		// It's supposed to be a disk, but the kernel hasn't created a block device.
		// This is a common "stale path" symptom.
		return true
	}	

	// 3. THE TRUTH: SCSI Inquiry PQ Bits
	return checkPQviaIoctl(sgName)
}


func checkPQviaIoctl(sgName string) (bool, error) {

	// 1. Avoid opening if sysfs already tells us the path is blocked
	if isHardwareBlocked(sgName) {
		return false, fmt.Errorf("device %s is in blocked/quiesce state, skipping ioctl", sgName)
	}

	devPath := filepath.Join("/dev", sgName)
	
	// 2. Use O_RDWR | O_NONBLOCK
	// Note: O_RDWR is often required for SG_IO even if only sending INQUIRY
	fd, err := syscall.Open(devPath, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		// ENXIO: Device is gone or fabric is dead
		if err == syscall.ENXIO || err == syscall.ENODEV {
			return true, nil 
		}
		return false, err
	}
	defer syscall.Close(fd)

	// 1. Check Subsystem (Avoid sending SCSI Inquiry to non-SCSI devices)
	subsystem, _ := os.Readlink(fmt.Sprintf("/sys/class/scsi_generic/%s/device/subsystem", sgName))
	if strings.Contains(subsystem, "nvme") {
		// NVMe 'ghosts' are handled differently (Namespace state)
		return false, nil 
	}


	const allocationLen := 36
	inqResp := make([]byte, allocationLen)
	senseBuf := make([]byte, 32)
	cdb := [6]byte{0x12, 0, 0, 0, uint8(allocationLen), 0}

	header := sgIoHdr{
		interface_id:    'S',
		dxfer_direction: SG_DXFER_FROM_DEV,
		cmd_len:         uint8(len(cdb)),
		mx_sb_len:       uint8(len(senseBuf)),
		sbp:             uintptr(unsafe.Pointer(&senseBuf[0])),
		dxfer_len:       uint32(len(inqResp)),
		dxferp:          uintptr(unsafe.Pointer(&inqResp[0])),
		cmdp:            uintptr(unsafe.Pointer(&cdb[0])),
		timeout:         1000, // 1 second is standard
	}
	
	
	// Syscall with retry on EAGAIN
	var errno syscall.Errno
	for i := 0; i < 3; i++ {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), SG_IO, uintptr(unsafe.Pointer(&header)))
		if errno != syscall.EAGAIN && errno != syscall.EBUSY {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if errno != 0 {
		if errno == syscall.ENXIO || errno == syscall.ENODEV {
			return true, nil
		}
		return false, fmt.Errorf("ioctl failed: %v", errno)
	}
	
	// 4. Validate Transport Health (Host Status)
	// If the fabric is down, we cannot claim it's a ghost device.
	if header.host_status != 0 {
		return false, fmt.Errorf("transport failure (host: 0x%x): path is down, not a ghost", header.host_status)
	}
	
	if header.status != 0x00 && header.status != 0x02 {
		return false, fmt.Errorf("scsi device error (status=0x%x)", header.status)
	}
	
	
// 4. Evaluate SCSI Status
switch header.status {
case 0x00: // GOOD: Proceed to check PQ bits
    pq := (inqResp[0] >> 5) & 0x07
    devType := inqResp[0] & 0x1f
    return (pq == 1 || pq == 3 || devType == 0x1f), nil

case 0x02: // CHECK CONDITION: Inspect Sense Data
    // Sense data format: Byte 2 contains the Sense Key
    // Bytes 12-13 contain ASC/ASCQ
    if header.sb_len_wr >= 14 {
        senseKey := senseBuf[2] & 0x0f
        asc := senseBuf[12]
        ascq := senseBuf[13]

        // Sense Key 0x05 (Illegal Request) + ASC/ASCQ 0x25/0x00 (LU Not Supported)
        // This is the "Gold Standard" for a Ghost device.
        if senseKey == 0x05 && asc == 0x25 && ascq == 0x00 {
            logger.Debugf("Confirmed Ghost via Sense Data: LU Not Supported")
            return true, nil
        }
        
        // Sense Key 0x02 (Not Ready) + ASC 0x3A (Medium Not Present)
        if senseKey == 0x02 && asc == 0x3A {
            return true, nil
        }
    }
    // If it's a different error, the path might just be transiently failing
    return false, fmt.Errorf("scsi check condition: sense key 0x%02x", senseBuf[2]&0x0f)

default:
    return false, fmt.Errorf("unexpected scsi status: 0x%02x", header.status)
}
	
	
	
	
	// 5. Evaluate Peripheral Qualifier
	// PQ is bits 7-5 of byte 0.
	// 000b = Connected
	// 001b = Supported but not connected (GHOST)
	// 011b = Not supported
	pq := (inqResp[0] >> 5) & 0x07
	
	// Also check byte 0 bits 4-0 (Device Type)
	// Type 0x1f (31) is the standard "no device" type.
	devType := inqResp[0] & 0x1f

	// TODO verify 3
	// 0x01 = PQ 1 (Logical unit is capable of being supported, but not connected)
	// 0x03 = PQ 3 (The device server is not capable of supporting a device on this 	
	if pq == 1 || pq == 3 || devType == 0x1f {
		logger.Debugf("SCSI Inquiry confirmed ghost: PQ=%d, Type=%d", pq, devType)
		return true, nil
	}	
}




func isHardwareBlocked(sgName string) bool {
	statePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName)
	state, err := os.ReadFile(statePath)
	if err != nil {
		return true // Assume blocked if we can't read state
	}
	s := strings.TrimSpace(string(state))
	// 'blocked' means the transport layer has paused queues (e.g., FC cable pulled)
	// 'quiesce' means the driver is busy. 
	// Both will cause an ioctl to hang despite O_NONBLOCK.
	return s == "blocked" || s == "quiesce"
}



// ============== OsDeviceConnectivityHelperInterface ==========================

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperInterface

type OsDeviceConnectivityHelperInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityScsiGeneric.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test OsDeviceConnectivityHelperGeneric logic.
	*/
	GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error)
	GetWwnByScsiInq(dev string) (string, error)
	GetMpathDeviceName(volumePath string) (string, error)
	GetMpathVolumeId(mpathdOutput string, mpathDeviceName string, dmDirectory string) (string, error)
}

type OsDeviceConnectivityHelperGeneric struct {
	Executer executer.ExecuterInterface
	Helper   GetDmsPathHelperInterface
}

func NewOsDeviceConnectivityHelperGeneric(executer executer.ExecuterInterface) OsDeviceConnectivityHelperInterface {
	return &OsDeviceConnectivityHelperGeneric{
		Executer: executer,
		Helper:   NewGetDmsPathHelperGeneric(executer),
	}
}

func (o OsDeviceConnectivityHelperGeneric) GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error) {
	/*
		Description:
			This function find all the hosts IDs under directory /sys/class/fc_host/ or /sys/class/iscsi_host"
			So the function goes over all the above hosts and return back only the host numbers as a list.
	*/
	//arrayIdentifier is wwn, value is 500507680b25c0aa
	var targetFilePath string
	var regexpValue string

	//IQN format is iqn.yyyy-mm.naming-authority:unique name
	//For example: iqn.1986-03.com.ibm:2145.v7k194.node2
	iscsiMatchRex := `^iqn\.(\d{4}-\d{2})\.([^:]+)(:)([^,:\s']+)`
	isIscsi, err := regexp.MatchString(iscsiMatchRex, arrayIdentifier)
	if isIscsi {
		targetFilePath = IscsiHostRexExPath
		regexpValue = "host([0-9]+)"
	} else {
		targetFilePath = FcHostSysfsPath
		regexpValue = "rport-([0-9]+)"
	}

	var HostIDs []int
	matches, err := o.Executer.FilepathGlob(targetFilePath)
	if err != nil {
		logger.Errorf("Error while Glob targetFilePath : {%v}. err : {%v}", targetFilePath, err)
		return nil, err
	}

	logger.Debugf("{%v} targetname files matches were found", len(matches))

	re := regexp.MustCompile(regexpValue)
	logger.Debugf("Check if any match is relevant for storage target (%s)", arrayIdentifier)
	for _, targetPath := range matches {
		targetName, err := o.Executer.IoutilReadFile(targetPath)
		if err != nil {
			logger.Warningf("Could not read target name from file : {%v}, error : {%v}", targetPath, err)
			continue
		}
		identifierFromHost := strings.TrimSpace(string(targetName))
		//For FC WWNs from the host, the value will like this: 0x500507680b26c0aa, but the arrayIdentifier doesn't have this prefix
		if strings.HasPrefix(identifierFromHost, "0x") {
			identifierFromHost = strings.TrimLeft(identifierFromHost, "0x")
		}
		if strings.EqualFold(identifierFromHost, arrayIdentifier) {
			regexMatch := re.FindStringSubmatch(targetPath)
			logger.Tracef("Found regex matches : {%v}", regexMatch)
			hostNumber := -1

			if len(regexMatch) < 2 {
				logger.Warningf("Could not find host number for targetFilePath : {%v}", targetPath)
				continue
			} else {
				hostNumber, err = strconv.Atoi(regexMatch[1])
				if err != nil {
					logger.Warningf("Host number in for target file was not valid : {%v}", regexMatch[1])
					continue
				}
			}

			HostIDs = append(HostIDs, hostNumber)
			logger.Debugf("portState path (%s) was found. Adding host ID {%v} to the id list", targetPath, hostNumber)
		}
	}

	if len(HostIDs) == 0 {
		return []int{}, &ConnectivityIdentifierStorageTargetNotFoundError{StorageTargetName: arrayIdentifier, DirectoryPath: targetFilePath}
	}

	return HostIDs, nil

}


func (o *OsDeviceConnectivityHelperGeneric) GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error) {
	//arrayIdentifier is wwn, value is 500507680b25c0aa
	arrayIdentifier = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(arrayIdentifier), "0x"))

	var targetFilePath string
	var hostRegex *regexp.Regexp

	isIscsi := strings.HasPrefix(arrayIdentifier, "iqn.") || strings.HasPrefix(arrayIdentifier, "nqn.")
	
	if isIscsi {
		targetFilePath = "/sys/class/iscsi_host/host*/targetname"
		hostRegex = regexp.MustCompile(`host([0-9]+)`)
	} else {
		// Note: FC rport numbers don't always match host numbers 1:1. 
		// You may need to read the 'node_name' or 'port_name' in /sys/class/fc_host/host*/
		targetFilePath = "/sys/class/fc_remote_ports/rport-*/port_name"
		hostRegex = regexp.MustCompile(`rport-([0-9]+)`) 
	}

	matches, err := filepath.Glob(targetFilePath)
	if err != nil {
		logger.Errorf("Error while Glob targetFilePath : {%v}. err : {%v}", targetFilePath, err)
	}
	
	hostMap := make(map[int]struct{})
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil { continue }

		//For FC WWNs from the host, the value will like this: 0x500507680b26c0aa, but the arrayIdentifier doesn't have this prefix
		identifierFromHost := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(string(data)), "0x"))

		if identifierFromHost == arrayIdentifier {
			regexMatch := hostRegex.FindStringSubmatch(path)
			logger.Tracef("Found regex matches : {%v}", regexMatch)
			if len(regexMatch) >= 2 {
				if hostNum, err := strconv.Atoi(regexMatch[1]); err == nil {
					logger.Debugf("portState path (%s) was found. Adding host ID {%v} to the id list", targetPath, hostNum)
					hostMap[hostNum] = struct{}{}
				} else {
					logger.Warningf("Host number in for target file was not valid : {%v}", regexMatch[1])
				}
			} else {
				logger.Warningf("Could not find host number for targetFilePath : {%v}", path)
			}
		}
	}

	if len(hostMap) == 0 {
		return []int{}, &ConnectivityIdentifierStorageTargetNotFoundError{StorageTargetName: arrayIdentifier, DirectoryPath: targetFilePath}
	}

	hostIDs := make([]int, 0, len(hostMap))
	for id := range hostMap {
		hostIDs = append(hostIDs, id)
	}
	return hostIDs, nil
}


var (
	// Captures the number from 'host[NUMBER]' in a path
	hostPathRegex = regexp.MustCompile(`host([0-9]+)`)
)

func (o *OsDeviceConnectivityHelperGeneric) GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error) {
	// Standardize: lower, trim 0x and spaces
	cleanID := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(arrayIdentifier), "0x"))

	var targetFilePath string
	isIscsi := strings.HasPrefix(cleanID, "iqn.") || strings.HasPrefix(cleanID, "nqn.")
	
	if isIscsi {
		targetFilePath = "/sys/class/iscsi_host/host*/targetname"
	} else {
		// WWNs in sysfs are found in port_name files
		targetFilePath = "/sys/class/fc_remote_ports/rport-*/port_name"
	}

	matches, err := filepath.Glob(targetFilePath)
	if err != nil {
		logger.Errorf("Glob failed for %s: %v", targetFilePath, err)
		return nil, err
	}
	
	hostMap := make(map[int]struct{})
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil { continue }

		// Standardize sysfs data (remove 0x and whitespace)
		idFromHost := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(string(data)), "0x"))

		if idFromHost == cleanID {
			// Resolve the symlink (e.g., /sys/class/fc_remote_ports/rport-2:0-0 -> .../host2/...)
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				logger.Warningf("Could not resolve symlink for %s: %v", path, err)
				continue
			}

			// Extract host number from the absolute path
			regexMatch := hostPathRegex.FindStringSubmatch(realPath)
			if len(regexMatch) >= 2 {
				if hostNum, err := strconv.Atoi(regexMatch[1]); err == nil {
					hostMap[hostNum] = struct{}{}
					logger.Debugf("Matched WWN %s to Host ID %d", cleanID, hostNum)
				}
			}
		}
	}

	if len(hostMap) == 0 {
		return []int{}, &ConnectivityIdentifierStorageTargetNotFoundError{
			StorageTargetName: arrayIdentifier, 
			DirectoryPath: targetFilePath,
		}
	}

	hostIDs := make([]int, 0, len(hostMap))
	for id := range hostMap {
		hostIDs = append(hostIDs, id)
	}
	return hostIDs, nil
}



func (o *OsDeviceConnectivityHelperGeneric) RescanHosts(hostIDs []int) error {
	var errs []error
	for _, id := range hostIDs {
		scanPath := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", id)
		
		// Writing "- - -" triggers a full scan of all channels, targets, and LUNs
		err := os.WriteFile(scanPath, []byte("- - -"), 0644)
		if err != nil {
			logger.Errorf("Failed to rescan host %d: %v", id, err)
			errs = append(errs, err)
			continue
		}
		logger.Infof("Successfully triggered rescan for host %d", id)
	}
	
	if len(errs) > 0 {
		return fmt.Errorf("failed to rescan %d hosts", len(errs))
	}
	return nil
}



func (o *OsDeviceConnectivityHelperGeneric) GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error) {
	// 1. Standardize input (WWN or IQN)
	arrayIdentifier = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(arrayIdentifier), "0x"))

	iscsiMatchRex := `^iqn\.(\d{4}-\d{2})\.([^:]+)(:)([^,:\s']+)`
	isIscsi, _ := regexp.MatchString(iscsiMatchRex, arrayIdentifier)

	var targetFilePath string
	var hostRegex = regexp.MustCompile(`host([0-9]+)`) // Extract hostX regardless of path depth

	if isIscsi {
		targetFilePath = "/sys/class/iscsi_host/host*/targetname"
	} else {
		targetFilePath = "/sys/class/fc_remote_ports/rport-*/port_name"
	}

	matches, err := o.Executer.FilepathGlob(targetFilePath)
	if err != nil {
		return nil, err
	}

	// 2. Use a Map to prevent redundant rescans on the same Host
	hostMap := make(map[int]struct{})

	for _, targetPath := range matches {
		data, err := o.Executer.IoutilReadFile(targetPath)
		if err != nil {
			continue
		}

		// 3. Robustly strip "0x" and whitespace
		idFromHost := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(string(data)), "0x"))

		if idFromHost == arrayIdentifier {
			// Find hostX in the path (works for both iSCSI and FC sysfs structures)
			regexMatch := hostRegex.FindStringSubmatch(targetPath)
			if len(regexMatch) >= 2 {
				if hostNum, err := strconv.Atoi(regexMatch[1]); err == nil {
					hostMap[hostNum] = struct{}{}
				}
			}
		}
	}

	if len(hostMap) == 0 {
		return []int{}, &ConnectivityIdentifierStorageTargetNotFoundError{
            StorageTargetName: arrayIdentifier, 
            DirectoryPath: targetFilePath,
        }
	}

	// 4. Convert unique map keys to slice
	hostIDs := make([]int, 0, len(hostMap))
	for id := range hostMap {
		hostIDs = append(hostIDs, id)
	}

	return hostIDs, nil
}


const (
	SG_IO             = 0x2285
	SG_DXFER_FROM_DEV = -3
)

// SgIoHeader matches the C struct sg_io_hdr_t for Linux ioctl
type SgIoHeader struct {
	InterfaceID    int32
	DxferDirection int32
	CmdLen         uint8
	MxSbpLen       uint8
	IovecCount     uint16
	DxferLen       uint32
	Dxferp         uintptr
	Cmdp           uintptr
	Sbp            uintptr
	Timeout        uint32
	Flags          uint32
	PackID         int32
	UsrPtr         uintptr
	Status         uint8
	MaskedStatus   uint8
	MsgStatus      uint8
	SbLenIv        uint8
	HostStatus     uint16
	DriverStatus   uint16
	Resid          int32
	Duration       uint32
	Info           uint32
}

func (o OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(dev string) ([]string, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// SCSI INQUIRY CDB: 12h=Cmd, 01h=EVPD, 83h=DeviceIDPage, 00h=Res, FFh=Len, 00h=Ctrl
	cdb := [6]byte{0x12, 0x01, 0x83, 0x00, 0xFF, 0x00}
	respBuf := make([]byte, 256)
	senseBuf := make([]byte, 32)

	header := SgIoHeader{
		InterfaceID:    'S',
		DxferDirection: SG_DXFER_FROM_DEV,
		CmdLen:         uint8(len(cdb)),
		MxSbpLen:       uint8(len(senseBuf)),
		DxferLen:       uint32(len(respBuf)),
		Dxferp:         uintptr(unsafe.Pointer(&respBuf[0])),
		Cmdp:           uintptr(unsafe.Pointer(&cdb[0])),
		Sbp:            uintptr(unsafe.Pointer(&senseBuf[0])),
		Timeout:        uint32(TimeOutSgInqCmd.Milliseconds()),
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), SG_IO, uintptr(unsafe.Pointer(&header)))
	if errno != 0 {
		return nil, fmt.Errorf("ioctl SG_IO failed on %s: %v", dev, errno)
	}

    // Status 0x02 = Check Condition (Consult senseBuf for details)
    if header.Status != 0 {
        return nil, fmt.Errorf("SCSI status error: 0x%02x (Host: 0x%04x, Driver: 0x%04x)", 
            header.Status, header.HostStatus, header.DriverStatus)
    }
    // HostStatus 0x01 = No connect, 0x07 = Timeout, etc.
    if header.HostStatus != 0 {
        return nil, fmt.Errorf("SCSI host error: 0x%04x", header.HostStatus)
    }

	actualLen := int(header.DxferLen) - int(header.Resid)
	
    if actualLen < 4 {
        return nil, fmt.Errorf("response too short")
    }
    // respBuf[1] is the Page Code. It should be 0x83.
    if respBuf[1] != 0x83 {
        return nil, fmt.Errorf("unexpected VPD page: 0x%02x", respBuf[1])
    }
	
	return parseVPD83(respBuf[:actualLen])
}



func parseVPD83(data []byte) ([]string, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("invalid VPD data")
	}

	pageLen := int(binary.BigEndian.Uint16(data[2:4]))
	cursor := 4
	limit := 4 + pageLen
	if limit > len(data) {
		limit = len(data)
	}

	var candidates []string
	for cursor+4 <= limit {
		// Byte 1: [PIV (7) | Association (5:4) | Designator Type (3:0)]
		designatorType := int(data[cursor+1] & 0x0F)
		association := (data[cursor+1] >> 4) & 0x03
		// Byte 3 is the total length of the data following the header
		length := int(data[cursor+3])

		idStart := cursor + 4
		if idStart+length > len(data) || idStart+length > limit {
			break
		}

		// Only Association 0 (Logical Unit) is relevant for Volume IDs
		if association == 0 {
			idData := data[idStart : idStart+length]
			
			switch designatorType {
			case 3, 2, 1: // NAA, EUI-64, or T10
				// Convert binary hex to string and prepend the type digit (e.g., "3" + hex)
				candidates = append(candidates, fmt.Sprintf("%d%x", designatorType, idData))
			case 8: // SCSI Name String (often IQN or NQN)
				candidates = append(candidates, strings.ToLower(strings.TrimSpace(string(idData))))
			}			
		}
		cursor += 4 + length
	}
	
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no Association 0 identifiers found")
	}
	return candidates, nil
}




func NormalizeDmVolumeIdentifier(filename string) string {
	// 1. Initial cleanup
	id := strings.ToLower(strings.TrimSpace(filename))

	// 2. Handle DM-specific UUID format
	// Filenames in /dev/mapper/ can be aliases (mpatha), 
	// but the underlying UUIDs (found in /sys/block/dm-X/uuid) 
	// often look like: "mpath-3600601..." or "dm-uuid-mpath-3600601..."
	// prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "pci-", "nvme-", "naa.", "eui.", "wwn-0x", "wwn-"}
	prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "wwn-0x", "wwn-"}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			id = strings.TrimPrefix(id, p)
			break
		}
	}

	// 3. IQN/NQN Check (Network targets don't follow hex rules)
	if strings.HasPrefix(id, "iqn.") || strings.HasPrefix(id, "nqn.") {
		return id
	}

	// 4. Hex-only filter for standard WWIDs
	var b strings.Builder
	for _, r := range id {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	cleanID := b.String()

	// 5. The "Type Digit" Check
	// Your parseVPD83 returns "[type][hex]" (e.g. "3600...").
	// Linux dm-uuid usually includes that '3' (for NAA) automatically.
	// If it's 32 chars and lacks the prefix, we assume it's a raw NAA-6 hex string.
	if len(cleanID) == 32 && !strings.HasPrefix(cleanID, "3") {
		return "3" + cleanID
	}
	
	// If it's 16 chars and starts with '5', it's an NAA-5 (standard for many arrays).
	// We keep it as-is because udev and multipathd treat '5...' as the primary key.
	
	return cleanID
}


func NormalizeOsVolumeIdentifier(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))

	// 1. Convert udev-style hints to SCSI Type Digits
	if strings.HasPrefix(id, "naa.") {
		return "3" + strings.TrimPrefix(id, "naa.")
	}
	if strings.HasPrefix(id, "eui.") {
		return "2" + strings.TrimPrefix(id, "eui.")
	}

	// 2. Handle standard DM/Multipath prefixes
	prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "wwn-0x", "wwn-"}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			id = strings.TrimPrefix(id, p)
			break 
		}
	}
    
    // ... proceed with hex filtering ...
}


func MatchVolumeWWID(targetWWID string, candidates []string) bool {
	// Normalize target: lowercase and strip '0x' or 'scsi-' prefixes
	target := strings.ToLower(strings.TrimSpace(targetWWID))
	target = strings.TrimPrefix(target, "scsi-")
	target = strings.TrimPrefix(target, "0x")
	
	// Prepare a version for numeric comparison (strip leading zeros)
	targetBody := strings.TrimLeft(target, "0")

	for _, candidate := range candidates {
		// 1. Direct match (covers Type 8 / SCSI Name Strings and exact hex matches)
		if candidate == target {
			return true
		}

		// 2. Handle prefixed hex strings from your parseVPD83 (Types 1, 2, 3)
		if len(candidate) > 1 {
			prefix := candidate[0]
			if prefix == '1' || prefix == '2' || prefix == '3' {
				// Strip the type digit, then strip leading zeros
				candidateBody := strings.TrimLeft(candidate[1:], "0")

				// If the 'clean' hex bodies match, it's a hit.
				// This matches target "123" with candidate "20000123"
				if candidateBody == targetBody && targetBody != "" {
					return true
				}
			}
		}
	}
	return false
}



func (o OsDeviceConnectivityHelperGeneric) GetMpathdOutputForVolume(volumeIdVariations []string,
	multipathdCommandFormatArgs []string) (string, error) {
	mpathdOutput, err := o.Helper.WaitForDmToExist(volumeIdVariations, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec)
	if err != nil {
		return "", err
	}
	return mpathdOutput, nil
}

// GetMpathDeviceName identifies the underlying device for a given path.
// It uses an O(1) stat check for block devices and falls back to /proc/self/mountinfo.
// It's quicker to parse than /proc/mounts
func (o OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(volumePath string) (string, error) {
	var stat syscall.Stat_t
	// 1. O(1) Fast Query: Get the device ID from the VFS
	if err := syscall.Stat(volumePath, &stat); err != nil {
		return "", fmt.Errorf("failed to stat path %s: %w", volumePath, err)
	}

	major := unix.Major(uint64(stat.Dev))
	minor := unix.Minor(uint64(stat.Dev))

	// Major > 0: Likely a standard block device (SD, NVMe, DM)	
	if major > 0 {
		// Fast lookup via /sys/dev/block mapping
		sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
		realPath, err := os.Readlink(sysPath)
		if err == nil {
			return filepath.Base(realPath), nil
		}
		// If sysfs fails, fall through to mountinfo (handles deleted/stale block devices)
	}

	// 3. Fallback Logic: Virtual/Network Filesystems (Major 0)
	// For NFS, SMB, or tmpfs, we must parse the mount table to find the source.
	return o.getDeviceFromMountInfo(volumePath)
}


func (o OsDeviceConnectivityHelperGeneric) getDeviceFromMountInfo(volumePath string) (string, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	defer f.Close()

	target := filepath.Clean(volumePath)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// [0] mount ID | [1] parent ID | [2] major:minor | [3] root | [4] mount point ...
		// The mount point is typically field [4].
		// The source (device/server) is usually after a "-" separator.		
		if len(fields) < 7 { continue }

		// Field 4: Mount Point
		if filepath.Clean(unescapeProcPath(fields[4])) != target {
			continue
		}

		// Find separator "-" to identify optional fields end
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" && i+2 < len(fields) {
				fstype := fields[i+1]
				source := unescapeProcPath(fields[i+2])

				switch fstype {
				case "nfs", "nfs4":
					// NFS source is "server:/export/path"
					return source, nil 
				case "cifs":
					// SMB source is "//server/share"
					return source, nil
				default:
					// Block devices: return "dm-5" or "sdb"
					return filepath.Base(source), nil
				}
			}
		}
	}
	return "", fmt.Errorf("mount point %s not found", volumePath)
}

func unescapeProcPath(path string) string {
	// The kernel escapes exactly these four characters in octal
	replacer := strings.NewReplacer(
		"\\040", " ",  // space
		"\\011", "\t", // tab
		"\\012", "\n", // newline
		"\\134", "\\", // backslash
	)
	return replacer.Replace(path)
}


//go:generate mockgen -destination=../../../mocks/mock_GetDmsPathHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity GetDmsPathHelperInterface

type GetDmsPathHelperInterface interface {
	WaitForDmToExist(volumeIdVariations []string, maxRetries int, intervalSeconds int, multipathdCommandFormatArgs []string) (string, error)
	ExtractDmFieldValues(dmFilterValues []string, mpathdOutput string) map[string]bool
	IsIndicatorMatchesFilterValues(dmFilterValues []string, dmFieldValue string) bool
	GetMpathDeviceNameFromProcMounts(procMounts string, volumePath string) (string, error)
}

type GetDmsPathHelperGeneric struct {
	executer executer.ExecuterInterface
}

func NewGetDmsPathHelperGeneric(executer executer.ExecuterInterface) GetDmsPathHelperInterface {
	return &GetDmsPathHelperGeneric{executer: executer}
}

// FetchDeviceByWWID finds a healthy block device for the given volume ID.
func FetchDeviceByWWID(volumeWWID string) (string, error) {
	normalizedWWID := strings.ToLower(strings.ReplaceAll(volumeWWID, "-", ""))

	// 1. STRATEGY A: DM-Multipath (SCSI or NVMe via DM)
	// Check udev shortcut first (O(1))
	dmPath := fmt.Sprintf("/dev/disk/by-id/dm-uuid-mpath-%s", normalizedWWID)
	if dev, err := verifyDevice(dmPath); err == nil {
		return dev, nil
	}

	// Fallback: Scan DM list in sysfs (O(N_dm))
	// Catches cases where udev is slow or stale
	if dev, err := scanDMSubsystem(normalizedWWID); err == nil {
		return dev, nil
	}

	// 2. STRATEGY B: Native NVMe (NVMe-oF / TCP / RDMA)
	// Check udev shortcut: /dev/disk/by-id/nvme-<uuid>
	nvmePath := fmt.Sprintf("/dev/disk/by-id/nvme-%s", normalizedWWID)
	if dev, err := verifyDevice(nvmePath); err == nil {
		return dev, nil
	}

	// Fallback: Scan NVMe subsystems (O(N_nvme))
	if dev, err := scanNVMeSubsystem(normalizedWWID); err == nil {
		return ev, nil
	}

	// Fallback: Scan all SCSI blocks for WWID match
	if dev, err := scanSCSISubsystem(normalizedWWID); err == nil {
		return dev, nil
	}

	return nil, fmt.Errorf("device with WWID %s not found after exhaustive scan", volumeWWID)
}

// verifyDevice ensures the link exists and returns the canonical path
func verifyDevice(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	// Crucial for CSI: Resolve /dev/mapper/mpatha to /dev/dm-X
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return realPath, nil
}


// scanDMSubsystem finds the DM device using robust normalization.
func scanDMSubsystem(targetID string) (string, error) {
	matches, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
	target := normalize(targetID)

	for _, m := range matches {
		if content, err := os.ReadFile(m); err == nil {
			// normalize() strips 'mpath-', handles case, and removes newlines
			if normalize(string(content)) == target {
				// Safely get 'dm-X' regardless of path depth
				dmName := filepath.Base(filepath.Dir(filepath.Dir(m)))
				return filepath.Join("/dev", dmName), nil
			}
		}
	}
	return "", fmt.Errorf("dm device not found")
}


// scanNVMeSubsystem finds Native NVMe namespaces by UUID or NGUID.
func scanNVMeSubsystem(targetID string) (string, error) {
	// Look through all nvme head nodes (namespaces)
	matches, _ := filepath.Glob("/sys/block/nvme*n*")
	for _, m := range matches {
		// Check NGUID (Direct hex match)
		if nguid, err := os.ReadFile(filepath.Join(m, "nguid")); err == nil {
			if strings.TrimSpace(string(nguid)) == targetID {
				return filepath.Join("/dev", filepath.Base(m)), nil
			}
		}
		// Check UUID (requires normalization of hyphens)
		if uuid, err := os.ReadFile(filepath.Join(m, "uuid")); err == nil {
			norm := strings.ReplaceAll(strings.TrimSpace(string(uuid)), "-", "")
			if strings.ToLower(norm) == targetID {
				return filepath.Join("/dev", filepath.Base(m)), nil
			}
		}
	}
	return "", fmt.Errorf("not found")
}

func IsSafeNVMeSinglePath(nvmeDev string) (bool, error) {
    // Example: /dev/nvme0n1 -> /sys/block/nvme0n1
    base := filepath.Base(nvmeDev)
    sysPath := fmt.Sprintf("/sys/block/%s", base)

    // 1. Check if it's a hidden path (not a head node)
    // Multipath "Head" nodes have a 'subsystem' link in sysfs
    if _, err := os.Stat(filepath.Join(sysPath, "device/subsystem")); err != nil {
        // If it's a path belonging to a multipath group, 
        // it might be hidden or marked 'hidden' in sysfs
        return false, fmt.Errorf("device is a hidden path or inactive")
    }

    // 2. Check for 'holders' (just like SCSI/DM)
    // If native multipathing is NOT used but DM-multipath IS used 
    // (rare for NVMe but possible), holders will show 'dm-X'
    holders, _ := os.ReadDir(filepath.Join(sysPath, "holders"))
    if len(holders) > 0 {
        return false, nil // Claimed by DM or another layer
    }

    return true, nil
}


// scanSCSISubsystem finds raw /dev/sdX devices.
func scanSCSISubsystem(targetID string) (string, error) {
	matches, _ := filepath.Glob("/sys/block/sd*/device/wwid")
	target := normalize(targetID)

	for _, m := range matches {
		if content, err := os.ReadFile(m); err == nil {
			// SCSI wwid files often contain 'naa.' prefixes
			if strings.Contains(normalize(string(content)), target) {
				// /sys/block/sdX/device/wwid -> sdX is 3 levels up from wwid,
				// or 2 levels up from device.
				sdName := filepath.Base(filepath.Dir(filepath.Dir(m)))
				return filepath.Join("/dev", sdName), nil
			}
		}
	}
	return "", fmt.Errorf("scsi device not found")
}


const DefaultMultipathTimeout = 10 * time.Second

func getMultipathConfig() (time.Duration, bool) {
	const (
		timeoutAttr = "find_multipaths_timeout"
		findAttr    = "find_multipaths"
		mainConfig  = "/etc/multipath.conf"
		configDir   = "/etc/multipath/conf.d"
	)

	timeout := DefaultMultipathTimeout
	isSmartMode := false // find_multipaths defaults to "off" in many distros

	// 1. Gather all files in order (Main first, then conf.d)
	files := []string{mainConfig}
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".conf") {
				files = append(files, filepath.Join(configDir, entry.Name()))
			}
		}
	}

	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			// Standardize delimiters
			cleanLine := strings.ReplaceAll(line, "\t", " ")
			cleanLine = strings.ReplaceAll(cleanLine, "=", " ")
			cleanLine = strings.ReplaceAll(cleanLine, "\"", "")
			fields := strings.Fields(cleanLine)

			for i, field := range fields {
				if i+1 >= len(fields) {
					break
				}
				
				// Extract find_multipaths_timeout
				if field == timeoutAttr {
					if val, err := strconv.Atoi(fields[i+1]); err == nil {
						timeout = time.Duration(math.Abs(float64(val))) * time.Second
					}
				}

				// Extract find_multipaths mode
				if field == findAttr {
					mode := strings.ToLower(fields[i+1])
					// find_multipaths "smart" is the specific mode that utilizes the timeout
					isSmartMode = (mode == "smart" || mode == "1")
				}
			}
		}
	}

	return timeout, isSmartMode
}


func (m *Mounter) getEffectiveMultipathTimeout() time.Duration {
	baseTimeout, isSmart := getMultipathConfig()
	
	// If NOT in smart mode, find_multipaths_timeout is technically ignored 
	// by the daemon, but we keep a base grace period for udev/settle.
	if !isSmart {
		return 5 * time.Second 
	}
	
	// 2-second "CSI Grace Period" to ensure udev has processed the claim
	return baseTimeout + (2 * time.Second)
}


func (m *Mounter) ShouldWaitForMultipath(devicePath string) (bool, time.Duration) {
	timeout, isSmart := getMultipathConfig()
	
	// 1. If not in smart mode, multipathd either claims it immediately (yes/on)
	// or ignores it (off/no). No extra waiting logic needed here.
	if !isSmart {
		return false, 0
	}

	// 2. CHECK WWIDS: The "Gold Standard"
	// If the device WWID is already in /etc/multipath/wwids, multipathd 
	// claims it immediately regardless of 'smart' mode.
	wwid, _ := m.getDeviceWWID(devicePath)
	if m.isWWIDKnown(wwid) {
		return true, 2 * time.Second // Short grace for udev only
	}

	// 3. SMART MODE + UNKNOWN WWID: 
	// This is where we MUST wait. The OS is intentionally delaying 
	// to see if this is actually a multipath device.
	return true, timeout + (2 * time.Second)
}

func (m *Mounter) isWWIDKnown(wwid string) bool {
	if wwid == "" { return false }
	data, err := os.ReadFile("/etc/multipath/wwids")
	if err != nil { return false }
	
	// The wwids file is a simple list: "/3600.../"
	return strings.Contains(string(data), "/" + wwid + "/")
}

func IsDeviceUdevLocked(devicePath string) bool {
	major, minor, err := getMajorMinor(devicePath)
	if err != nil {
		return false 
	}

	udevFile := fmt.Sprintf("/run/udev/data/b%d:%d", major, minor)
	data, err := os.ReadFile(udevFile)
	if err != nil {
		return false 
	}

	isLocked := false
	isExplicitlyReady := false

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()	
		
		// Flag indicating a multipath "wait and see" claim
		if line == "E:SYSTEMD_READY=0" || 
		   line == "E:DM_MULTIPATH_DEVICE_PATH=1" || 
		   line == "E:ID_FS_USAGE=multipath" {
			isLocked = true
		}
		
		// Flag indicating udev has finished and released the device
		if line == "E:SYSTEMD_READY=1" {
			isExplicitlyReady = true
		}	
	}

	// Ready signal always overrides the lock signal
	return isLocked && !isExplicitlyReady
}

func (m *Mounter) WaitForDeviceReady(devicePath string) error {
	timeout, isSmart := getMultipathConfig()
	if !isSmart {
		return nil // No smart-wait logic active on host
	}

	// Max time we are willing to wait (Base timeout + CSI overhead)
	deadline := time.Now().Add(timeout + (2 * time.Second))

	for time.Now().Before(deadline) {
		if !isDeviceUdevLocked(devicePath) {
			// Device is released by udev! 
			// It's either now part of a /dev/dm-X or a verified single path.
			return nil
		}
		
		// Optional: Check if a DM device was already created for this WWID
		// if m.isMultipathDevicePresent(devicePath) { return nil }

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for udev to release device %s", devicePath)
}

func getMajorMinor(devicePath string) (uint64, uint64, error) {
	var stat unix.Stat_t
	if err := unix.Stat(devicePath, &stat); err != nil {
		return 0, 0, err
	}
	// Extract major/minor from the Rdev (device ID)
	major := uint64(stat.Rdev >> 8) & 0xfff
	minor := uint64(stat.Rdev & 0xff) | (uint64(stat.Rdev >> 12) & 0xfff00)
	return major, minor, nil
}

func HasHolders(devicePath string) bool {
	// devicePath e.g., "/dev/sdb" -> devName "sdb"
	devName := filepath.Base(devicePath)
	holdersPath := fmt.Sprintf("/sys/class/block/%s/holders", devName)
	
	entries, err := os.ReadDir(holdersPath)
	if err != nil || len(entries) == 0 {
		return false
	}
	// If it has holders, another block device (like dm-0) is using it
	return true
}




func (m *Mounter) GetMultipathWaitStatus(devicePath string) (shouldWait bool, remainingWait time.Duration) {
	// 1. Get Host Configuration
	timeout, isSmart := getMultipathConfig()
	if !isSmart {
		return false, 0
	}

	// 2. Identify Device Identity & Creation Time
	var stat unix.Stat_t
	if err := unix.Stat(devicePath, &stat); err != nil {
		return false, 0
	}
	
	// major:minor for udev data lookup
	major := uint64(stat.Rdev >> 8) & 0xfff
	minor := uint64(stat.Rdev & 0xff) | (uint64(stat.Rdev >> 12) & 0xfff00)
	
	// Device age determines how much of the timeout has already elapsed
	// stat.Ctim is Change time (metadata update/creation)
	createdAt := time.Unix(stat.Ctim.Unix())
	elapsed := time.Since(createdAt)
	maxWait := timeout + (2 * time.Second) // Add CSI grace period

	// 3. Check for Active Holders (DM already created)
	// If a dm-X device already exists in holders, we don't wait for the path—
	// we use the DM device instead.
	devName := filepath.Base(devicePath)
	holdersPath := fmt.Sprintf("/sys/class/block/%s/holders", devName)
	if entries, err := os.ReadDir(holdersPath); err == nil && len(entries) > 0 {
		return false, 0
	}

	// 4. Check Identity (Known WWIDs bypass the "Smart" delay)
	wwidData, _ := os.ReadFile(fmt.Sprintf("/sys/block/%s/device/wwid", devName))
	wwid := strings.TrimSpace(string(wwidData))
	if m.isWWIDKnown(wwid) {
		// Even if known, udev might still be processing. 
		// Give it a fixed small window.
		if elapsed < (2 * time.Second) {
			return true, (2 * time.Second) - elapsed
		}
		return false, 0
	}

	// 5. Check Udev Database for "Smart" Lock (SYSTEMD_READY=0)
	udevFile := fmt.Sprintf("/run/udev/data/b%d:%d", major, minor)
	isLocked := IsDeviceUdevLocked(udevFile)

	// 6. Final Decision
	if isLocked && elapsed < maxWait {
		return true, maxWait - elapsed
	}

	return false, 0
}









// **** TODO   ******    verify all holders are the same - this is the dm (in this case no need to query the link)

// **** TODO   ******    skip ghost sg devices

// IsSafeSinglePath determine if an sg device (e.g., /dev/sg0) is a standalone path.
func IsSafeSinglePath(sgDev string) (bool, error) {
	timeout := getEffectiveMultipathTimeout()
	sgBase := filepath.Base(sgDev)
	// 1. Resolve sg device to block device (sdX)
	// Path: /sys/class/scsi_generic/sgX/device/block/sdX
	blockDir := fmt.Sprintf("/sys/class/scsi_generic/%s/device/block", sgBase)
	files, err := os.ReadDir(blockDir)
	if err != nil || len(files) == 0 {
		return false, fmt.Errorf("failed to map %s to block device", sgDev)
	}
	sdName := files[0].Name()
	sysBlockPath := fmt.Sprintf("/sys/block/%s", sdName)

	// 2. Get the WWID of the device
	wwidBytes, err := os.ReadFile(filepath.Join(sysBlockPath, "device/wwid"))
	if err != nil {
		return false, fmt.Errorf("could not read WWID: %v", err)
	}
	wwid := strings.TrimSpace(string(wwidBytes))

	start := time.Now()
	for time.Since(start) < timeout {
		// A. Check if the device is already "held" (e.g., by dm-X)
		holders, _ := os.ReadDir(filepath.Join(sysBlockPath, "holders"))
		if len(holders) > 0 {
			for _, holder := range holders {
				holderName := holder.Name() // e.g., "dm-5"
				
				// Read the DM UUID (the source of truth)
				uuidPath := fmt.Sprintf("/sys/block/%s/dm/uuid", holderName)
				uuidBytes, err := os.ReadFile(uuidPath)
				if err != nil {
					continue
				}
				
				dmUUID := strings.ToLower(string(uuidBytes))
				
				// CRITICAL MATCH: Does this holder's UUID contain our WWID?
				// This handles "mpath-<WWID>" and "part1-mpath-<WWID>"
				
				// TODO normalizeWWID func to set normalizedWWID
				if strings.Contains(dmUUID, normalizedWWID) && strings.Contains(dmUUID, "mpath") {
					return false, nil
				}
			}
		}

		// B. Check if the WWID is in the multipath database
		// If it's in here, multipathd is guaranteed to claim it eventually.
		if isWWIDInMultipathDB(wwid) {
			// Wait and poll: the DM device is likely being created.
			time.Sleep(1 * time.Second)
			continue
		}

		// C. If no holders and no WWID intent found after a few checks, 
		// it is likely a single-path device.
		// C. CHECK UDEV SYSTEMD_READY:
		// In 'Smart' mode, udev keeps SYSTEMD_READY=0 while waiting for 2nd path.
		// If we are still in this window, we MUST NOT use the device yet.
		// We use a simple Sleep here to respect the kernel's "quarantine" period.
		time.Sleep(500 * time.Millisecond)
	}

	// Final check after the full multipath timeout has passed
	holders, _ := os.ReadDir(filepath.Join(sysBlockPath, "holders"))
	return len(holders) == 0, nil
}

// Helper to extract the core serial/WWID
func normalizeWWID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	// Remove common sysfs prefixes to match dm-uuid formatting
	prefixes := []string{"t10.", "naa.", "eui."}
	for _, p := range prefixes {
		raw = strings.TrimPrefix(raw, p)
	}
	return raw
}


func isWWIDInMultipathDB(targetWWID string) bool {
	file, err := os.Open("/etc/multipath/wwids")
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, targetWWID) {
			return true
		}
	}
	return false
}

func IsSafeSinglePath(sgDev string) (bool, error) {
	timeout := getMultipathTimeout()
	sgBase := filepath.Base(sgDev)
	
	// 1. Resolve sgX to sdX
	blockDir := fmt.Sprintf("/sys/class/scsi_generic/%s/device/block", sgBase)
	files, err := os.ReadDir(blockDir)
	if err != nil || len(files) == 0 {
		return false, fmt.Errorf("failed to map %s", sgDev)
	}
	sdName := files[0].Name()
	sysBlockPath := "/sys/block/" + sdName

	// 2. Get and Normalize WWID
	wwidBytes, _ := os.ReadFile(filepath.Join(sysBlockPath, "device/wwid"))
	cleanWWID := normalizeWWID(string(wwidBytes))

	start := time.Now()
	for time.Since(start) < timeout {
		// A. Check Holders (Already claimed by DM)
		holders, _ := os.ReadDir(filepath.Join(sysBlockPath, "holders"))
		for _, h := range holders {
			uuid, _ := os.ReadFile(fmt.Sprintf("/sys/block/%s/dm/uuid", h.Name()))
			if strings.Contains(strings.ToLower(string(uuid)), cleanWWID) {
				return false, nil // Claimed by multipath
			}
		}

		// B. Check Udev Quarantine (Smart Mode / find_multipaths)
		if isQuarantinedByUdev(sdName) {
			time.Sleep(1 * time.Second)
			continue
		}

		// C. Check Multipath DB (Atomic check for intent)
		if isWWIDInMultipathDB(cleanWWID) {
			time.Sleep(1 * time.Second)
			continue
		}

		// If we reach here, we've passed the "FindMultipaths" window
		break
	}

	// Final verification: No one claimed it during our wait.
	holders, _ := os.ReadDir(filepath.Join(sysBlockPath, "holders"))
	return len(holders) == 0, nil
}





func (m *Mounter) GetMultipathDeviceFromSd(sdName string) (string, error) {
	// 1. Path: /sys/block/sdX/holders/
	// If sdX is part of a multipath device, the dm-X name will be here.
	holdersPath := filepath.Join("/sys/block", sdName, "holders")
	
	entries, err := os.ReadDir(holdersPath)
	if err != nil {
		return "", fmt.Errorf("failed to read holders for %s: %w", sdName, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		// We are looking for "dm-X"
		if strings.HasPrefix(name, "dm-") {
			// 2. Verification: Check the DM UUID to ensure it's actually a multipath device
			// Multipath devices have a UUID starting with "mpath-"
			uuidPath := filepath.Join("/sys/block", name, "dm", "uuid")
			uuidBytes, err := os.ReadFile(uuidPath)
			if err != nil {
				// If we can't read the UUID, the device might be in flux (deleting)
				continue
			}

			uuid := string(uuidBytes)
			if strings.HasPrefix(uuid, "mpath-") {
				// Return the full dev path, e.g., /dev/dm-5
				return filepath.Join("/dev", name), nil
			}
		}
	}

	return "", fmt.Errorf("no multipath holder found for %s", sdName)
}


func isQuarantinedByUdev(sdName string) bool {
	// 1. Get Major:Minor for the device
	devPath := fmt.Sprintf("/sys/block/%s/dev", sdName)
	data, _ := os.ReadFile(devPath)
	
	// 2. Read udev data: /run/udev/data/b<Major>:<Minor>
	id := strings.TrimSpace(string(data))
	udevPath := "/run/udev/data/b" + id
	udevData, err := os.ReadFile(udevPath)
	if err != nil { return false }

	// ID_PART_TABLE_TYPE indicates the disk has a partition table; 
	// DM_MULTIPATH_DEVICE_PATH=1 means multipath has claimed it.
	// SYSTEMD_READY=0 means it's still being processed.
	content := string(udevData)
	return strings.Contains(content, "DM_MULTIPATH_DEVICE_PATH=1") || 
	       strings.Contains(content, "SYSTEMD_READY=0")
}

// Check if the DM device exists but has no active slave paths
func IsStale(dmName string) bool {
    // dmName e.g., "dm-5"
    slavesPath := fmt.Sprintf("/sys/class/block/%s/slaves", dmName)
    files, err := os.ReadDir(slavesPath)
    if err != nil || len(files) == 0 {
        return true // No underlying paths = Stale
    }
    return false
}

func (o GetDmsPathHelperGeneric) WaitForDmToExist(volumeWWID string, maxRetries int, intervalSeconds int) (string, error) {
	logger.Debugf("Waiting for dm to exist")
	for i := 0; i < maxRetries; i++ {
		deviceInfo, err := FetchDeviceByWWID(string)
		if err != nil {
			return "", err
		}
		return deviceInfo.Path

		time.Sleep(time.Second * time.Duration(intervalSeconds))
	}
	return "", &MultipathDeviceNotFoundForVolumeError{volumeWWID}
}


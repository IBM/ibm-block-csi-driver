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
	"net"
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
	var errStrings []string
	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		logger.Errorf("%s", e.Error())
		return nil, e
	}

	hostIDs := GetHostsByBatchIdentifiers(GetHostsIdByArrayIdentifier)

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
	DM_DEV_SUSPEND = 0xc138fd06
	DM_DEV_STATUS = 0xc138fd07


    DM_VERSION_MAJOR = 4
    DM_VERSION_MINOR = 0
    DM_VERSION_PATCH = 0


	DM_SUSPEND_FLAG    = 1 << 1  // Used to freeze I/O
	DM_NOFLUSH_FLAG    = 1 << 8  // Critical: do not hang on dead paths, Equivalent to --noflush: drops pending I/O on remove
	DM_DEFERRED_REMOVE = 1 << 17 // Standard for CSI Unstage
)



func (r *OsDeviceConnectivityHelperScsiGeneric) TeardownVolume(target string, expectedWWID string) error {

	// --- PHASE 1: UNMOUNT ---
	if r.Mounter.isMounted(target) {
		if err := r.Mounter.UnmountWithTimeout(target, 20); err != nil {
			return fmt.Errorf("unmount phase failed: %w", err)
		}
	}

	// --- PHASE 1: VFS LAYER (UNMOUNT STACK) ---
	mounts, _ := c.getMountsForPath(target)
	var major, minor int
	if len(mounts) > 0 {
		major, minor = mounts[0].Major, mounts[0].Minor
		for i := len(mounts) - 1; i >= 0; i-- {
			_ = c.escalatedUnmount(target)
			_ = c.pollLayerDeleted(target, mounts[i].MountID, 5*time.Second)
		}
	}

	// BARRIER: Mountinfo must be clean
	if !c.pollMountDeleted(target, 10*time.Second) {
		return fmt.Errorf("teardown: mountinfo not clean for %s", target)
	}

	// Resolve Hardware
	mpathName := c.findDMNameByWWID(expectedWWID)
	slaves := c.getSlavesForDevice(major, minor)

	// --- PHASE 2: BLOCK LAYER (IDENTITY & OPENCOUNT) ---
	if mpathName != "" {
		// Verify OpenCount to determine removal strategy
		openCount := c.getOpenCount(mpathName)

		needDeferRemove := false

		// TODO maybe check holders count?
		if openCount == 0 {
		    // --- PHASE 2: DEVICE-MAPPER LAYER ---
			// 1. Flush Virtual Cache (Uninterruptible, 5s Handoff)
		   _ = c.resourceManager.ExecuteUninterruptible("flush", 10, 50, 5*time.Second, 30*time.Second, func() error {
				   return c.flushBlockDevice(fmt.Sprintf("/dev/mapper/%s", mpathName))
		   })

			// SUCCESS PATH: No one is using the device. Immediate delete.
			err := c.resourceManager.ExecuteInterruptible("mpath-socket", 3, 20, 10*time.Second, func(ctx context.Context) error {
				return c.multipathdAction("del map " + mpathName)
			})
			if err != nil {
				needDeferRemove = true
				// Fallback if socket fails despite opencount being 0
			}
		} else if openCount > 0 {
			// STUBBORN PATH: Mount is gone, but a process has a raw FD open.
			// Do not block. Use Deferred Remove (Fire-and-Forget).

			// --force is Equivalent to DM_SKIP_BDGET_FLAG
			// return exec.Command("dmsetup", "remove", "--force", "--retry", dmName).Run()

			needDeferRemove = true
		}
		if needDeferRemove {
			_ = c.multipathdAction("disablequeueing map " + mpathName)
			for _, s := range slaves {
				_ = c.multipathdAction("fail path " + s)
			}
			_ = c.deferredRemove(mpathName)
		}
	}

	// --- PHASE 3 & 4: EMERGENCY & PHYSICAL ---
	// If mpathName still exists in sysfs after the above, Phase 3 triggers
	if len(slaves) > 0 {
		c.parallelPathTeardown(slaves)
	}

	return os.Remove(target)


	//	isNotMounted, err := d.NodeUtils.IsNotMountPoint(stagingPathWithHostPrefix)
	//	if err != nil {
	//		logger.Warningf("Failed to check if (%s), is mounted", stagingPathWithHostPrefix)
	//		return nil, status.Error(codes.Internal, err.Error())
	//	}
	//	if !isNotMounted {
	//		actualHw, err := r.GetDeviceWWN(devicePath)
	//		if err == nil && !r.IsSerialMatch(actualHw, volUuid) {
	//			return fmt.Errorf("UNSTAGE ABORT: Mount point %s belongs to Serial %s, not %s", stagingPath, actualHw, volUuid)
	//		}

	//		err = d.Mounter.UnmountWithTimeout(stagingTargetPath, 20*time.Second)
	//		if err != nil {
	//			logger.Errorf("Unmount failed. Target : %q, err : %v", stagingTargetPath, err.Error())
	//			return nil, status.Error(codes.Internal, err.Error())
	//		}
	//	}

	//	err = d.OsDeviceConnectivityHelper.cleanupVolumeDevices(volumeUuid)

}


func (r *OsDeviceConnectivityHelperScsiGeneric) IdentityAwarePreScan(targetPath string, expectedWWID string) error {
	mounts, _ := c.getMountsForPath(targetPath)
	mpathName := c.findDMNameByWWID(expectedWWID)

	// CASE 1: Path is mounted
	if len(mounts) > 0 {
		var currentWWID string
		// Safety Guard: Resolving WWID involves sysfs/ioctl, which can hang
		_ = c.resourceManager.ExecuteUninterruptible("wwid-check", 5, 20, 2*time.Second, 10*time.Second, func() error {
			currentWWID, _ = c.getWWIDByDev(mounts[0].Major, mounts[0].Minor)
			return nil
		})

		if strings.EqualFold(currentWWID, expectedWWID) {
			// Zombie Match: Same volume from a crashed attempt. Full cleanup.
			err := c.TeardownVolume(targetPath, expectedWWID)
			if err != nil {
				return fmt.Errorf("pre-scan: failed to clear zombie volume: %w", err)
			}
			return nil
		} else {
			// Collision: A DIFFERENT volume is here.
			// We MUST NOT delete the DM map (we don't own it), but we must rescue the path.
			for i := len(mounts) - 1; i >= 0; i-- {
				// MNT_DETACH is safe; it won't hang even if the rogue volume is in D-state
				_ = syscall.Unmount(targetPath, syscall.MNT_DETACH)
				// TODO duplicate check with IsMounted below
				if !c.pollLayerDeleted(targetPath, mounts[i].MountID, 5*time.Second) {
					return fmt.Errorf("pre-scan collision: failed to rescue path %s from rogue volume %s", targetPath, currentWWID)
				}
			}

			// After detaching, ensure the directory is clear for our new mount
			if mounted, _ := c.IsMounted(targetPath); mounted {
				return fmt.Errorf("pre-scan: collision at %s; failed to detach rogue volume", targetPath)
			}
		}
	}

	// CASE 2: Unmounted Zombie Device
	if mpathName != "" {
		// Before checking OpenCount, ensure we aren't queuing I/O to a dead map
		_ = c.multipathdAction("disablequeueing map " + mpathName)

		openCount := c.getOpenCount(mpathName)
		if openCount == 0 {
			// Clean fresh start
			_ = c.multipathdAction("del map " + mpathName)
		} else {
			// Device is busy (e.g. by an old LVM scan or udev).
			// Schedule for deletion the moment it's released.
			_ = c.deferredRemove(mpathName)
		}
	}

	// CASE 3: Final Directory Cleanup
	// If it's not a mount point but the directory exists, clear it.
	if _, err := os.Stat(targetPath); err == nil {
		// TODO RemoveAll or Remove
		_ = os.RemoveAll(targetPath) // Use RemoveAll for safety in pre-scan
	}

	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) FinalWwidPurge(expectedWWID string) error {
	// 1. Clean up Multipath Layer
	// Even if TeardownVolume ran, multipathd might have re-created the map
	// if a path event fired late.
	mpathName := c.findDMNameByWWID(expectedWWID)
	if mpathName != "" {
		_ = c.resourceManager.ExecuteUninterruptible("mpath-final-del", 1, 5, 2*time.Second, 10*time.Second, func() error {
			// Try graceful socket delete first
			err := c.multipathdAction("del map " + mpathName)
			if err != nil {
				// Force kernel-level removal if socket is unresponsive
				return c.deferredRemove(mpathName)
			}
			return nil
		})
	}

    dmUUIDs, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
    for _, path := range dmUUIDs {
        if data, _ := os.ReadFile(path); strings.Contains(string(data), targetWWID) {
            // Found a stale/hidden DM. Flush it.
            dmName := strings.Split(path, "/")[3] // e.g., dm-5
			_ = c.resourceManager.ExecuteUninterruptible("mpath-final-del", 1, 5, 2*time.Second, 10*time.Second, func() error {
				// Try graceful socket delete first
				err := c.multipathdAction("del map " + dmName)
				if err != nil {
					// Force kernel-level removal if socket is unresponsive
					return c.deferredRemove(dmName)
				}
				return nil
			})
        }
    }


	// 2. Identify and Sever all Physical SCSI Paths (sd/sg)
	// We iterate /sys/block to find any remaining slaves belonging to this WWID
	devices, err := os.ReadDir("/sys/block")
	if err != nil {
		return fmt.Errorf("failed to scan /sys/block: %w", err)
	}

	for _, dev := range devices {
		devName := dev.Name()
		// Only check scsi-based block devices (sd*)
		if !strings.HasPrefix(devName, "sd") {
			continue
		}

		// Perform identification within the Gater to prevent hanging on D-state paths
		_ = c.resourceManager.ExecuteUninterruptible("scsi-purge-"+devName, 5, 50, 2*time.Second, 15*time.Second, func() error {
			currentWWID, err := c.getWWIDByDevName(devName)
			if err != nil || !strings.EqualFold(currentWWID, expectedWWID) {
				return nil // Not our device or already gone
			}

			// MATCH FOUND: This is a stale path for our WWID.
			// 1. Flush buffers (best effort)
			_ = c.flushBlockDevice("/dev/" + devName)

			// 2. Sever the device from the SCSI subsystem
			// TODO does this? _, _ = o.MultipathdSocketCmd(fmt.Sprintf("fail path %s", sdName))
			deletePath := fmt.Sprintf("/sys/block/%s/device/delete", devName)
			// TOOD 0200 or 0644

			// TODO NVVE:
			//if strings.HasPrefix(sd, "nvme") {
			//	delPath = fmt.Sprintf("/sys/block/%s/device/device/remove", sd)
			//}

			return os.WriteFile(deletePath, []byte("1"), 0200)
		})
	}

	return nil
}



func (r *OsDeviceConnectivityHelperScsiGeneric) GetOpenCount(dmName string) (int32, error) {
	f, err := os.OpenFile("/dev/mapper/control", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("failed to open dm control: %w", err)
	}
	defer f.Close()

	//io := dmIoctl{
	//	VersionMajor: 4,
	//	VersionMinor: 0,
	//	VersionPatch: 0,
	//	DataSize:     uint32(unsafe.Sizeof(dmIoctl{})),
	//	Flags:        0,
	//}

	io := dmIoctl{
		VersionMajor: 4,
		DataSize:     uint32(unsafe.Sizeof(dmIoctl{})),
		DataStart:    uint32(unsafe.Sizeof(dmIoctl{})), // Standard practice
	}
	copy(io.Name[:], dmName)

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		f.Fd(),
		uintptr(DM_DEV_STATUS),
		uintptr(unsafe.Pointer(&io)),
	)

	if errno != 0 {
		// Both ENOENT and ENXIO mean the IBM LUN is already detached or missing
		if errno == unix.ENOENT || errno == unix.ENXIO {
			return -1, nil
		}
		return -1, fmt.Errorf("ioctl DM_DEV_STATUS failed: %w", errno)
	}

	// io.OpenCount is now updated by the kernel in-place
	return int32(io.OpenCount), nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) parallelPathTeardown(slaves []string) {
	// Use a 10-worker limit for physical path deletion
	for _, slave := range slaves {
		slaveName := slave
		go func() {
			_ = c.resourceManager.ExecuteUninterruptible("path-delete", 10, 100, 5*time.Second, 30*time.Second, func() error {
				// 1. Flush physical path
				_ = c.flushBlockDevice(fmt.Sprintf("/dev/%s", slaveName))
				// 2. Sever via sysfs
				return os.WriteFile(fmt.Sprintf("/sys/block/%s/device/delete", slaveName), []byte("1"), 0200)
			})
		}()
	}
}
//		if strings.HasPrefix(sd, "nvme") {
//			delPath = fmt.Sprintf("/sys/block/%s/device/device/remove", sd)
//		}


func (r *OsDeviceConnectivityHelperScsiGeneric) multipathdAction(cmd string) error {
	response, err := r.Executer.MultipathdCmd(cmd)

	// 5. Parse Response Content
	// Multipathd returns "ok" or "map deleted" on success.
	// It returns "fail [reason]" on logical errors.
	if strings.HasPrefix(response, "fail") {
		// Identify specific failure types
		if strings.Contains(response, "map in use") {
			return fmt.Errorf("map in use") // Caller should use deferredRemove
		}
		if strings.Contains(response, "not found") {
			return nil // Already deleted, treat as success
		}
		return fmt.Errorf("multipathd command failed: %s", response)
	}

	return nil // Success
}


// pollLayerDeleted verifies that a specific MountID has vanished from the system
func (r *OsDeviceConnectivityHelperScsiGeneric) pollLayerDeleted(target string, mountID int, timeout time.Duration) bool {
	expiry := time.Now().Add(timeout)
	for time.Now().Before(expiry) {
		currentMounts, _ := c.getMountsForPath(target)
		found := false
		for _, m := range currentMounts {
			if m.MountID == mountID {
				found = true
				break
			}
		}
		if !found {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func (r *OsDeviceConnectivityHelperScsiGeneric) deferredRemove(name string) error {
	// Flags: DEFERRED_REMOVE + NOFLUSH
	// DM_NOFLUSH_FLAG is critical: it prevents the ioctl from hanging if there is
	// pending I/O on a dead hardware path.
	// TODO verify flags
	// TODO should we return success if errno is syscall.ENXIO
	flags := uint32(DM_DEFERRED_REMOVE | DM_NOFLUSH_FLAG)

	err := r.ExecuteDmIoctl(uintptr(DM_DEV_REMOVE), name, flags)
	if err != nil {
		// If we get EBUSY even with deferred, it's a kernel-level lock issue
		return fmt.Errorf("deferred remove failed for %s: %w", name, err)
	}
	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) flushBlockDevice(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	// BLKFLSBUF = 0x1261
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x1261, 0)
	if errno != 0 {
		return errno
	}
	return nil
	//	if err != nil && strings.Contains(err.Error(), "kernel hang") {
	//	c.limiter.StuckOps.Add(1)
}


func (r *OsDeviceConnectivityHelperScsiGeneric) ExecuteDmIoctl(command uintptr, dmName string, flags uint32) error {
    // 1. Structural Validation
    const expectedSize = 312
    if size := unsafe.Sizeof(DmIoctl{}); size != expectedSize {
        return fmt.Errorf("invalid DmIoctl size: expected %d, got %d", expectedSize, size)
    }

    // 2. Open Control Device
    // O_CLOEXEC is recommended for modern 2026 Linux systems to prevent FD leaks to child processes
	// TODO verify O_CLOEXEC backward compatible
    control, err := os.OpenFile("/dev/mapper/control", os.O_RDWR|syscall.O_CLOEXEC, 0)
    if err != nil {
        return fmt.Errorf("failed to open /dev/mapper/control: %w", err)
    }
    defer control.Close()

    // 3. Prepare Data Structure
    data := DmIoctl{
        Version:   [3]uint32{DM_VERSION_MAJOR, DM_VERSION_MINOR, DM_VERSION_PATCH},
        DataSize:  uint32(unsafe.Sizeof(DmIoctl{})),
        DataStart: 0, // No extra payload follows this struct
        Flags:     flags,
    }
    copy(data.Name[:], dmName)

    // 4. Execute Syscall
    // WARNING: conversion to uintptr MUST happen inside the argument list to prevent GC moving data
    _, _, errno := syscall.Syscall(
        syscall.SYS_IOCTL,
        control.Fd(),
        command,
        uintptr(unsafe.Pointer(&data)),
    )

    // 5. Handle Results
    if errno != 0 {
        switch errno {
        case syscall.EBUSY:
            // Crucial for 2026 CSI drivers: identifies if a pod still holds a mount
            return fmt.Errorf("dm: %s is busy (OpenCount: %d)", dmName, data.OpenCount)
        case syscall.ENOENT:
            return nil // Already removed or doesn't exist
        case syscall.ENXIO:
            return fmt.Errorf("dm: device %s not found or invalid major/minor", dmName)
        case syscall.EINVAL:
            return fmt.Errorf("dm: invalid ioctl version or malformed DataSize")
        default:
            return fmt.Errorf("dm ioctl (0x%x) failed: %w", command, errno)
        }
    }

    return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) MountVolume(source, target, wwid string, options []string) error {
	// 1. SELF-HEAL: Clear rogue/zombie mounts using the 2026 Pre-Scan
	if err := r.IdentityAwarePreScan(target, wwid); err != nil {
		return fmt.Errorf("mount-safety: pre-scan failed: %w", err)
	}

	// 2. RESOURCE PROTECTION: Use the "Mount" pool (Limit 10, Handoff 15s)
	// We use ExecuteUninterruptible because mount(2) can hang in D-state.
	return r.Executer.ExecuteUninterruptible(
		"mount-ops", 10, 50, 15*time.Second, 2*time.Minute,
		func() error {
			// Modern VFS API implementation (OpenTree -> MountSetattr -> MoveMount)
			return m.MountNative(source, target, options)
		},
	)
}


func (r *OsDeviceConnectivityHelperScsiGeneric) UnmountVolume(target, wwid string) error {
	// 1. Use the "Teardown" pool (Limit 20, Handoff 10s)
	// Phase 3 Emergency logic is triggered internally if del-map hangs.
	return r.Executer.ExecuteUninterruptible(
		"teardown-ops", 20, 100, 10*time.Second, 6*time.Minute,
		func() error {
			return r.TeardownVolume(target, wwid)
		},
	)
}

func(r *OsDeviceConnectivityHelperScsiGeneric) GetStats(target, wwid string) (*VolumeStatistics, error) {
	// Statistics are read-only; use a higher limit (Limit 50, Handoff 2s)
	//var stats *VolumeStatistics
	//err := r.Executer.ExecuteUninterruptible(
	//	"stats-ops", 50, 100, 2*time.Second, 10*time.Second,
	//	func() error {
	//		var innerErr error
	//		stats, innerErr = m.CleanupManager.GetVolumeStats(target, wwid)
	//		return innerErr
	//	},
	//)
	return stats, err
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
func (r *OsDeviceConnectivityHelperScsiGeneric) normalizeLun(lunStr string) string {
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


func (r *OsDeviceConnectivityHelperScsiGeneric) RemoveGhostDevice(expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
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


		// We only prune devices that are either IBM or belong to our transport IDs
		//if !isIBM && !isOurPath {
		//	skipped++
		//	continue
		//}


		// 5. Ghost/Serial Logic
		// Read the actual hardware serial from the device (e.g., from 'wwid' file)
		hwSerial, err := o.getHardwareSerial(deviceDir)

		isGhost := IsGhostDevice(sgName) // Ensure this checks PQ=1 via sysfs 'type' or inquiry


		//if isGhost && isIBM {
		//	shouldDelete = true
		//	reason = "IBM PQ=1/3 Ghost (Stale Path)"
		}// else if isOurArray && err == nil && !o.IsSerialMatch(hwSerial, expectedSerial) {
		//	// If we successfully read a serial and it doesn't match our target,
		//	// this LUN has been remapped or we are seeing a different volume on our LUN ID.
		//	shouldDelete = true
		//	reason = fmt.Sprintf("Identity mismatch (Hardware: %s, Expected: %s)", hwSerial, expectedSerial)
		//}

		// REMEDIATION: Delete if it's a ghost from IBM, or if it's our path but the serial is wrong
		shouldDelete := (isGhost && isIBM) || (isIBM && !o.IsSerialMatch(hwSerial, expectedSerial))

		// 6. REMEDIATION LOGIC
		// We delete if it's a Ghost OR if the hardware identity is wrong
		shouldDelete := (isGhost && isIBM) || (isOurPath && (!isIBM || (hwSerial != "" && !o.IsSerialMatch(hwSerial, expectedSerial))))

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


func(r *OsDeviceConnectivityHelperScsiGeneric) GetHCTLFromSg(sgName string) (string, error) {
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



func (r *OsDeviceConnectivityHelperScsiGeneric) GetHCTLFromDevice(deviceName string) (string, error) {
	// deviceName example: "sg5" or "nvme1n1"

	// 1. Check for SCSI Generic
	if strings.HasPrefix(deviceName, "sg") {
		deviceLink := filepath.Join("/sys/class/scsi_generic", deviceName, "device")
		realPath, err := filepath.EvalSymlinks(deviceLink)
		if err != nil {
			return "", fmt.Errorf("failed to resolve SCSI device link for %s: %w", deviceName, err)
		}
		hctl := filepath.Base(realPath)

		// Standard SCSI Validation (H:C:T:L)
		if strings.Count(hctl, ":") == 3 {
			return hctl, nil
		}
		return "", fmt.Errorf("invalid HCTL format '%s' for SCSI device %s", hctl, deviceName)
	}

	// 2. Check for NVMe (Modern IBM FlashSystem/SVC)
	if strings.HasPrefix(deviceName, "nvme") {
		// For NVMe, the "HCTL" equivalent is usually the Controller ID and Namespace
		// Path: /sys/class/block/nvme1n1/device/
		deviceLink := filepath.Join("/sys/class/block", deviceName, "device")
		realPath, err := filepath.EvalSymlinks(deviceLink)
		if err != nil {
			return "", fmt.Errorf("failed to resolve NVMe device link for %s: %w", deviceName, err)
		}

		// NVMe devices usually identify by their subsystem/controller name (e.g., nvme1)
		// and their Namespace ID (NSID).
		// We return a "pseudo-HCTL" used for tracking: "nvme:controller:nsid"
		ctrl := filepath.Base(realPath) // e.g., "nvme1"

		nsidPath := filepath.Join("/sys/class/block", deviceName, "nsid")
		nsid, err := os.ReadFile(nsidPath)
		if err != nil {
			return "", fmt.Errorf("failed to read NSID for %s: %w", deviceName, err)
		}

		return fmt.Sprintf("nvme:%s:%s", ctrl, strings.TrimSpace(string(nsid))), nil
	}

	return "", fmt.Errorf("unsupported device type for identity resolution: %s", deviceName)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) isPathOwnedByMyArray(hctl string, arrayIdentifiers []string) bool {
    // 1. Identify the transport type by looking for rport or session directories
    deviceBase := fmt.Sprintf("/sys/class/scsi_device/%s/device", hctl)

	// TODO verify reading targetID is safer than relying on path (i.e. strings.Contains(realPathLower, strings.ToLower(id)))

    // 2. Read the actual Target Identifier from sysfs (O(1) lookup)
    targetID := ""

    // Check for Fibre Channel (FC)
    // Path: .../targetH:C:T/fc_transport/targetH:C:T/port_name
    fcPath := filepath.Join(deviceBase, "fc_transport", fmt.Sprintf("target%s", r.getHCT(hctl)), "port_name")
    if data, err := os.ReadFile(fcPath); err == nil {
        targetID = strings.TrimPrefix(strings.TrimSpace(string(data)), "0x")
    }

    // Check for iSCSI
    // Path: .../sessionX/targetname
    if targetID == "" {
        // You would need to traverse up to find the 'sessionX' directory
        targetID = r.getIscsiTargetName(deviceBase)
    }

    // 3. Exact matching is safer than strings.Contains
    for _, id := range arrayIdentifiers {
        if strings.EqualFold(targetID, strings.TrimPrefix(id, "0x")) {
            return true
        }
    }
    return false
}


// getHardwareSerial safely retrieves the serial, returning error if path is blocked
func (r *OsDeviceConnectivityHelperScsiGeneric) getHardwareSerial(deviceDir string) (string, error) {
	// Try the standard 'wwid' file first
	wwidBytes, err := os.ReadFile(filepath.Join(deviceDir, "wwid"))
	if err != nil || len(bytes.TrimSpace(wwidBytes)) == 0 {
		// Fallback: If 'wwid' is empty, path might be blocked or transitioning
		return "", fmt.Errorf("serial unavailable")
	}
	return strings.TrimSpace(string(wwidBytes)), nil
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


func (r *OsDeviceConnectivityHelperGeneric) IsGhostDevice(sgName string) (bool, error) {
	// 1. Sysfs Fast-Check (Kernel-Level Knowledge)
	deviceBase := fmt.Sprintf("/sys/class/scsi_generic/%s/device", sgName)
	stateBytes, err := os.ReadFile(filepath.Join(deviceBase, "state"))
	if err == nil {
		state := strings.TrimSpace(string(stateBytes))
		// 'offline' or 'deleting' means the kernel has already severed ties
		if state == "offline" || state == "cancelled" || state == "deleting" {
			return true, nil
		}
		// 'blocked' or 'quiesce' means we CANNOT run ioctl without hanging
		if state == "blocked" || state == "quiesce" {
			return false, fmt.Errorf("device %s is blocked; cannot verify ghost status", sgName)
		}
	}

	// 2. Type 31 Check (PQ=1 mapped to Type 31)
	typeBytes, err := os.ReadFile(filepath.Join(deviceBase, "type"))
	if err == nil && strings.TrimSpace(string(typeBytes)) == "31" {
		return true, nil
	}

	// 3. Disk vs Block Check
	// If it's a disk type (0) but has no block child, it's a stale/ghost entry
	if isDiskType(deviceBase) {
		blockPath := filepath.Join(deviceBase, "block")
		if _, err := os.Stat(blockPath); os.IsNotExist(err) {
			return true, nil
		}
	}

	// 4. The Hardware Truth (SCSI Inquiry)
	return checkPQviaIoctl(sgName)
}



func (r *OsDeviceConnectivityHelperScsiGeneric) isDiskType(deviceBase string) bool {
	data, err := os.ReadFile(filepath.Join(deviceBase, "type"))
	return err == nil && strings.TrimSpace(string(data)) == "0"
}

func (r *OsDeviceConnectivityHelperScsiGeneric) checkPQviaIoctl(sgName string) (bool, error) {

	// 1. Avoid opening if sysfs already tells us the path is blocked
	if isHardwareBlocked(sgName) {
		return false, fmt.Errorf("device %s is in blocked/quiesce state, skipping ioctl", sgName)
	}

	devPath := filepath.Join("/dev", sgName)

	// 2. Use O_RDWR | O_NONBLOCK
	// Note: O_RDWR is often required for SG_IO even if only sending INQUIRY
	fd, err := syscall.Open(devPath, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
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
		flags:           0, // Ensure no direct I/O or other flags are set by accident
	}

	// Use a retry loop specifically for transient SCSI errors like Unit Attention (UA)
	maxRetries := 2
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Reset sense buffer before each attempt
		for i := range senseBuf {
			senseBuf[i] = 0
		}
		header.sb_len_wr = 0 // Reset written sense length

		// Syscall with retry on EAGAIN/EBUSY
		var errno syscall.Errno
		for i := 0; i < 3; i++ {
			_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), SG_IO, uintptr(unsafe.Pointer(&header)))
			if errno != syscall.EAGAIN && errno != syscall.EBUSY {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		if errno != 0 {
			// syscall.ENOTTY - Inappropriate ioctl for device
			if errno == syscall.ENXIO || errno == syscall.ENODEV || errno == syscall.ENOTTY {
				return true, nil // Confirmed gone at kernel level
			}
			return false, fmt.Errorf("ioctl failed: %v", errno)
		}

		// Validate Transport Health (Host Status) - if fabric is down, this isn't a ghost
		if header.host_status != 0 {
			return false, fmt.Errorf("transport failure (host: 0x%x): path is down, not a ghost", header.host_status)
		}

		// Evaluate SCSI Status
		switch header.status {
		case 0x00: // GOOD: Break retry loop and process the PQ bits
			goto PROCESS_PQ

		case 0x02: // CHECK CONDITION: Inspect Sense Data
			if header.sb_len_wr >= 14 {
				senseKey := senseBuf[2] & 0x0f

				if senseKey == 0x06 { // UNIT ATTENTION
					logger.Debugf("UA detected on %s, retrying inquiry...", sgName)
					continue // Retry the main 'attempt' loop
				}

				// Handle other "ghost" conditions if they appear on the first try:
				asc := senseBuf[12]
				ascq := senseBuf[13]
				if senseKey == 0x05 && asc == 0x25 && ascq == 0x00 { // LU Not Supported
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
	} // End of 'attempt' loop

	// If we exhausted retries and are still here (very rare)
	return false, fmt.Errorf("failed to get reliable status after retries")


PROCESS_PQ:
	// 5. Evaluate Peripheral Qualifier
	// PQ is bits 7-5 of byte 0.
	// 000b = Connected
	// 001b = Supported but not connected (GHOST)
	// 011b = Not supported
	pq := (inqResp[0] >> 5) & 0x07

	// Also check byte 0 bits 4-0 (Device Type)
	// Type 0x1f (31) is the standard "no device" type.
	devType := inqResp[0] & 0x1f
	// device 0x1f - Unknown or no device type.

	// TODO verify 3
	// TODO verify if need to check inqResp[0] == 0x7f (or ask 0x1f as used)
	// 0x01 = PQ 1 (Logical unit is capable of being supported, but not connected)
	// 0x03 = PQ 3 (The device server is not capable of supporting a device on this
	if pq == 1 || pq == 3 || devType == 0x1f {
		logger.Debugf("SCSI Inquiry confirmed ghost: PQ=%d, Type=%d", pq, devType)
		return true, nil
	}
    return false, nil
}

State	isGhostDevice Result	Reasoning
//offline/deleting	true	Kernel has marked the path as dead.
//blocked/quiesce	Error	Path is frozen; ioctl may hang despite O_NONBLOCK.
//running + PQ=1	true	Target confirms LUN is configured but disconnected.
//running + PQ=3	true	Target confirms LUN is unsupported.
//running + Type=31

func (r *OsDeviceConnectivityHelperScsiGeneric) isHardwareBlocked(sgName string) bool {
	statePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName)
	state, err := os.ReadFile(statePath)
	if err != nil {
		return true // Assume blocked if we can't read state
	}
	s := strings.TrimSpace(string(state))
	// 'blocked' means the transport layer has paused queues (e.g., FC cable pulled)
	// 'quiesce' means the driver is busy.
	// Both will cause an ioctl to hang despite O_NONBLOCK.
	// "quiesce" may be used during upgrade / failover - add log
	return s == "blocked"
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


//TODO
//cleanID := strings.TrimSpace(strings.Trim(string(data), "\x00"))
//idFromSys := strings.ToLower(strings.TrimPrefix(cleanID, "0x"))
func (o *OsDeviceConnectivityHelperGeneric) GetHostsByBatchIdentifiers(identifiers []string) (map[string][]int, error) {
	cleanLookup := make(map[string]string)

	// Track which protocols we actually need to search
	var hasIscsi, hasFC, hasNVMe bool

	for _, id := range identifiers {
		clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "0x"))
		cleanLookup[clean] = id

		// Categorize by ID format
		if strings.HasPrefix(clean, "iqn.") {
			hasIscsi = true
		} else if strings.HasPrefix(clean, "nqn.") {
			hasNVMe = true
		} else if len(clean) == 16 {
			// Standard WWPN is 16 chars (64-bit)
			hasFC = true
		}
	}

	hostMap := make(map[string]map[int]struct{})

	// Only define search groups for protocols present in the input
	var searchGroups []struct {
		root   string
		suffix string
	}
	if hasIscsi {
		searchGroups = append(searchGroups, struct{ root, suffix string }{"/sys/class/iscsi_host", "targetname"})
	}
	if hasFC {
		searchGroups = append(searchGroups, struct{ root, suffix string }{"/sys/class/fc_remote_ports", "port_name"})
	}
	// TODO
	if hasNVMe {
		//searchGroups = append(searchGroups, struct{ root, suffix string }{
		//	"/sys/class/nvme-fabrics", "subsysnqn", // Simplified example
		}//)
	}

	// 2. Loop only over active groups
	for _, group := range searchGroups {
		entries, err := os.ReadDir(group.root)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			idPath := filepath.Join(group.root, entry.Name(), group.suffix)
			data, err := os.ReadFile(idPath)
			if err != nil {
				continue
			}

			idFromSys := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(string(data)), "0x"))

			if originalID, found := cleanLookup[idFromSys]; found {
				hostNum, err := o.extractHostNumber(entry.Name())
				if err == nil {
					if hostMap[originalID] == nil {
						hostMap[originalID] = make(map[int]struct{})
					}
					hostMap[originalID][hostNum] = struct{}{}
				}
			}
		}
	}

	// 3. Convert results...
	finalResults := make(map[string][]int)
	for id, hosts := range hostMap {
		for h := range hosts {
			finalResults[id] = append(finalResults[id], h)
		}
	}

	return finalResults, nil
}


// TODO perhaps strengthen prefix check
func (o *OsDeviceConnectivityHelperGeneric) extractHostNumber(entryName string) (int, error) {
    if strings.HasPrefix(entryName, "host") {
        return strconv.Atoi(strings.TrimPrefix(entryName, "host"))
    }
    // Handle both "rport-" and "remote_port-"
    if idx := strings.Index(entryName, "-"); idx != -1 {
        idPart := entryName[idx+1:]
        if colonIdx := strings.Index(idPart, ":"); colonIdx != -1 {
            return strconv.Atoi(idPart[:colonIdx])
        }
    }
    return 0, fmt.Errorf("unknown host format: %s", entryName)
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

// TODO double check reference
func (o OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(dev string) ([]string, error) {
	if o.willIoctl0x83Fail(filepath.Base(dev)) {
		return nil, fmt.Errorf("path %s in unsafe state", dev)
	}
	// Use O_RDONLY and O_NONBLOCK for safety and performance
	f, err := os.OpenFile(dev, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		// If the device is gone/dead, we want to know immediately
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

	maxRetries := 2
	for i := 0; i < maxRetries; i++ {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), SG_IO, uintptr(unsafe.Pointer(&header)))
		if errno != 0 {
			return nil, fmt.Errorf("ioctl failed: %v", errno)
		}

		// 1. Check for Host/HBA Errors (No point in retrying if the cable is pulled)
		if header.HostStatus != 0 {
			return nil, fmt.Errorf("SCSI host error: 0x%04x", header.HostStatus)
		}

		// 2. Check for Check Condition (0x02)
		if header.Status == 0x02 {
			// Sense data is usually in senseBuf. Sense Key is byte 2 (offset 2)
			// in fixed format sense data.
			senseKey := senseBuf[2] & 0x0f
			if senseKey == 0x06 { // 0x06 = UNIT ATTENTION
				logger.Infof("Unit Attention detected on %s, retrying...", dev)
				continue // Try again, the UA is now cleared
			}
			return nil, fmt.Errorf("SCSI Check Condition: SenseKey 0x%02x", senseKey)
		}

		// 3. Status is 0 (Good) - break and process result
		if header.Status == 0 {
			break
		}

		return nil, fmt.Errorf("Unexpected SCSI status: 0x%02x", header.Status)
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

func (r *OsDeviceConnectivityHelperScsiGeneric) willIoctl0x83Fail(sgName string) bool {
    statePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName)
    state, err := os.ReadFile(statePath)
    if err != nil {
        return true
    }

    s := strings.TrimSpace(string(state))
    switch s {
    case "running":
        return false // Best case for success
    case "blocked", "quiesce", "offline", "transport-offline", "deleting", "cancel":
        return true
    default:
        // Any unknown state should be treated as a failure for safety
        return true
    }
}
//blocked: Occurs during error recovery (e.g., a Fibre Channel rport is lost). The SCSI mid-layer queues all I/O, including SG_IO ioctls. Even with O_NONBLOCK, the ioctl call itself can block in the kernel until the timeout (dev_loss_tmo) expires.
//quiesce: Used when a device is being suspended or during certain driver-level resets. The device is temporarily not accepting commands.
//offline: The kernel has already determined the device is unusable after failed error recovery. Most ioctls will return an immediate -ENXIO (No such device or address) or -EIO (I/O error).
//transport-offline: Similar to offline but specifically indicates the transport layer (SAS/FC) has severed the link
//deleting/cancel - kernel is actively tearing down the device structures, and attempting an ioctl here is unreliable.


func (o *OsDeviceConnectivityHelperScsiGeneric) parseVPD83(data []byte) ([]string, error) {
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



// Wrapper for 0x83 query
func (o *OsDeviceConnectivityHelperGeneric) GetSafeWwn(dev string) ([]string, error) {
    // 1. Isolate by WWID (if known) or Device Name to prevent "thundering herd"
    return o.resourceManager.ExecuteGlobalScsi(dev, func() error {
        // 2. Perform the Sysfs State Guard
        if o.willIoctl0x83Fail(filepath.Base(dev)) {
            return fmt.Errorf("device-safety: path %s is in unsafe state", dev)
        }
        return o.GetWwnByScsiInq(dev)
    })
}



func (o *OsDeviceConnectivityHelperGeneric) NormalizeDmVolumeIdentifier(filename string) string {
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


func (o *OsDeviceConnectivityHelperGeneric) NormalizeOsVolumeIdentifier(id string) string {
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

	for _, r := range id {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	cleanID := b.String()

    return cleanID
}


func (o *OsDeviceConnectivityHelperGeneric) MatchVolumeWWID(targetWWID string, candidates []string) bool {
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


func (o *OsDeviceConnectivityHelperGeneric) GetMpathdOutputForVolume(volumeIdVariations []string,
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
func (o *OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(volumePath string) (string, error) {
	var stat syscall.Stat_t
	// 1. Get Device ID directly from the inode metadata
	if err := syscall.Stat(volumePath, &stat); err != nil {
		return "", fmt.Errorf("failed to stat path %s: %w", volumePath, err)
	}

	major := unix.Major(uint64(stat.Dev))
	minor := unix.Minor(uint64(stat.Dev))

	// 2. High-Speed Path: Block Device Lookup
	// Major 0 is reserved for non-block (virtual) devices.
	if major > 0 {
		sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
		if realPath, err := os.Readlink(sysPath); err == nil {
			// Returns "dm-5", "sda", "nvme0n1", etc.
			return filepath.Base(realPath), nil
		}
	}

	// 3. Fallback: Parse mountinfo for Bind Mounts, NFS, or SMB
	return o.getDeviceFromMountInfo(volumePath)
}


// findDMByWWID helper to map a WWID to a /dev/mapper name (e.g., mpatha)
func (o *OsDeviceConnectivityHelperGeneric) findDMByWWID(wwid string) string {
	files, err := os.ReadDir("/dev/mapper")
	if err != nil {
		return ""
	}

	for _, file := range files {
		name := file.Name()
		if name == "control" {
			continue
		}

		// 1. Resolve /dev/mapper/name to its kernel dm-X name
		// Aliases like 'mpatha' are symlinks to ../dm-X
		fullPath := filepath.Join("/dev/mapper", name)
		realPath, err := filepath.EvalSymlinks(fullPath)
		if err != nil {
			continue
		}
		dmKernelName := filepath.Base(realPath) // e.g., "dm-5"

		// 2. Read the UUID from sysfs
		// The UUID in dm/uuid follows the format "mpath-<WWID>"
		uuidPath := fmt.Sprintf("/sys/block/%s/dm/uuid", dmKernelName)
		content, err := os.ReadFile(uuidPath)
		if err == nil {
			// Multipath UUIDs are prefixed with 'mpath-'
			// We use strings.Contains to handle different prefix conventions (mpath, part, etc.)
			if strings.Contains(strings.ToLower(string(content)), strings.ToLower(wwid)) {
				return name // Return the alias (e.g. mpatha) for multipathd commands
			}
		}
	}
	return ""
}

// GetMountWWID retrieves the unique hardware identifier (WWID or DM UUID)
// for the block device currently mounted at targetPath.
func (o *OsDeviceConnectivityHelperGeneric) GetMountWWID(targetPath string) (string, error) {
	var st unix.Stat_t
	// 1. Stat (not Lstat) to follow symlinks to the actual mount
	if err := unix.Stat(targetPath, &st); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("identity-check: path %s does not exist", targetPath)
		}
		return "", fmt.Errorf("identity-check: failed to stat %s: %w", targetPath, err)
	}

	major, minor := int(unix.Major(st.Dev)), int(unix.Minor(st.Dev))

	var wwid string
	// Gated execution: Read from sysfs can block if the device is in error recovery
	err := c.resourceManager.ExecuteUninterruptible(
		fmt.Sprintf("id-check-%d:%d", major, minor),
		5, 20, 1*time.Second, 5*time.Second,
		func() error {
			var innerErr error
			wwid, innerErr = c.getWWIDByDev(major, minor)
			return innerErr
		},
	)

	return c.normalizeWWID(wwid), err
}
// Corresponding:
func (o *OsDeviceConnectivityHelperGeneric) normalizeWWID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	// Remove common prefixes
	prefixes := []string{"mpath-", "uuid.", "naa.", "nvme."}
	for _, p := range prefixes {
		raw = strings.TrimPrefix(raw, p)
	}
	return raw
}


func (o *OsDeviceConnectivityHelperGeneric) getMajorMinor(devicePath string) (uint64, uint64, error) {
	var stat unix.Stat_t
	if err := unix.Stat(devicePath, &stat); err != nil {
		return 0, 0, err
	}

	// Use the official macros to decode the Rdev field
	major := uint64(unix.Major(stat.Rdev))
	minor := uint64(unix.Minor(stat.Rdev))

	return major, minor, nil
}



func (o *OsDeviceConnectivityHelperGeneric) getWWIDByDev(major, minor int) (string, error) {
	basePath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)

	// Order of operations: DM -> NVMe -> SCSI
	// 1. Device Mapper (Multipath) - Most common for IBM Block
	if uuid, err := os.ReadFile(filepath.Join(basePath, "dm/uuid")); err == nil {
		return strings.TrimSpace(string(uuid)), nil
	}

	// 2. NVMe (Standardized in modern kernels)
	if wwid, err := os.ReadFile(filepath.Join(basePath, "wwid")); err == nil {
		return strings.TrimSpace(string(wwid)), nil
	}

	// 3. SCSI (Legacy/Standard)
	if wwid, err := os.ReadFile(filepath.Join(basePath, "device/wwid")); err == nil {
		return strings.TrimSpace(string(wwid)), nil
	}

	return "", fmt.Errorf("identity-check: could not resolve WWID for device %d:%d at %s", major, minor, basePath)
}


type OsDeviceConnectivityHelperGeneric struct{}


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
func (o GetDmsPathHelperGeneric) FetchDeviceByWWID(volumeWWID string) (string, error) {
    // Wrap the entire discovery in a global limit to prevent
    // multiple concurrent sysfs walks from saturating the kernel.
    var dev string
    err := o.resourceManager.ExecuteUninterruptible("global-discovery", 2, 0, 0, 45*time.Second, func() error {
        var innerErr error
        dev, innerErr = o.performDiscovery(volumeWWID)
        return innerErr
    })
    return dev, err
}

func (o GetDmsPathHelperGeneric) performDiscovery(volumeWWID string) (string, error) {
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
func (o GetDmsPathHelperGeneric) verifyDevice(path string) (string, error) {
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
func (o GetDmsPathHelperGeneric) scanDMSubsystem(targetID string) (string, error) {
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
// TODO corresponding
func normalize(input string) string {
    s := strings.ToLower(strings.TrimSpace(input))
    s = strings.TrimPrefix(s, "mpath-")
    s = strings.TrimPrefix(s, "naa.")
    s = strings.TrimPrefix(s, "uuid.")
    return strings.ReplaceAll(s, "-", "")
}



// scanNVMeSubsystem finds Native NVMe namespaces by UUID or NGUID.
func (o GetDmsPathHelperGeneric) scanNVMeSubsystem(targetID string) (string, error) {
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

func (o GetDmsPathHelperGeneric) IsSafeNVMeSinglePath(nvmeDev string) (bool, error) {
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
func (o GetDmsPathHelperGeneric) scanSCSISubsystem(targetID string) (string, error) {
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

func (o GetDmsPathHelperGeneric) getMultipathConfig() (time.Duration, bool) {
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
					// "smart" uses timeout; "on", "yes", "strict" also cause
					// udev/multipathd interaction that requires a grace period.
					isSmartMode = (mode == "smart" || mode == "1" ||
					               mode == "on" || mode == "yes" || mode == "strict")
				}
			}
		}
	}

	return timeout, isSmartMode
}


func (o GetDmsPathHelperGeneric) getEffectiveMultipathTimeout() time.Duration {
	baseTimeout, isSmart := getMultipathConfig()

	// If NOT in smart mode, find_multipaths_timeout is technically ignored
	// by the daemon, but we keep a base grace period for udev/settle.

	// TODO is this necessary - should we not skip the wait
	// 1. If not in smart mode, multipathd either claims it immediately (yes/on)
	// or ignores it (off/no). No extra waiting logic needed here.
	if !isSmart {
		return 5 * time.Second
	}

	// 2-second "CSI Grace Period" to ensure udev has processed the claim
	return baseTimeout + (2 * time.Second)
}


func (o GetDmsPathHelperGeneric) IsDeviceUdevLocked(devicePath string, startTime time.Time) bool {
	major, minor, err := getMajorMinor(devicePath)
	if err != nil { return false }

	udevFile := fmt.Sprintf("/run/udev/data/b%d:%d", major, minor)
	data, err := os.ReadFile(udevFile)
	if err != nil {
		// If the file doesn't exist yet, but the device just appeared (< 2s),
		// it is likely being processed. Assume locked.
		return time.Since(startTime) < 2*time.Second
	}

	isLocked := false
	isExplicitlyReady := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		// Indicators that multipathd or udev has claimed the device for probing
		if line == "E:SYSTEMD_READY=0" ||
		   line == "E:DM_MULTIPATH_DEVICE_PATH=1" ||
		   line == "E:ID_FS_USAGE=multipath" {
			isLocked = true
		}
		if line == "E:SYSTEMD_READY=1" {
			isExplicitlyReady = true
		}
	}
	// Ready signal always overrides the lock signal
	return isLocked && !isExplicitlyReady
}

// Helper to extract the core serial/WWID
func (o GetDmsPathHelperGeneric) normalizeWWID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	// Remove common sysfs prefixes to match dm-uuid formatting
	prefixes := []string{"t10.", "naa.", "eui."}
	for _, p := range prefixes {
		raw = strings.TrimPrefix(raw, p)
	}
	return raw
}


func (o GetDmsPathHelperGeneric) isWWIDInMultipathDB(wwid string) bool {
    // 1. If the daemon isn't even running, the file content is moot
    // for immediate claim logic.
    if !o.isMultipathdRunning() {
        return false
    }

    // 2. Resolve the WWID file path (handle the 0.1% custom config case)
    wwidsPath := o.getWWIDsFilePath() // Defaults to "/etc/multipath/wwids"

    f, err := os.Open(wwidsPath)
    if err != nil {
        if os.IsNotExist(err) {
            // FIX: File not found is a VALID state. It means NO devices
            // have been added to the multipath database yet.
            return false
        }
        // For other errors (Permission Denied, etc.), log and assume false
        logger.Warnf("Could not open %s: %v", wwidsPath, err)
        return false
    }
    defer f.Close()

    target := "/" + strings.TrimSpace(wwid) + "/"
	naatarget := "/naa." + strings.TrimSpace(wwid) + "/"
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        // Ignore comments and empty lines
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        // Exact match only to avoid substring collisions
        if line == target || line == naatarget {
            return true
        }
    }

    if err := scanner.Err(); err != nil {
        logger.Errorf("Error reading %s: %v", wwidsPath, err)
    }

    return false
}

func (o GetDmsPathHelperGeneric) GetWWIDsFilePath() string {
	// 1. Try querying the live daemon for the active configuration
	// The 'show config' command returns the entire effective configuration
	resp, err := o.MultipathdCmd("show config")
	if err == nil {
		scanner := bufio.NewScanner(strings.NewReader(resp))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			// Search for the wwids_file attribute
			if strings.HasPrefix(line, "wwids_file") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					// Remove quotes if present
					return strings.Trim(fields[1], "\"")
				}
			}
		}
	}

	// 2. Fallback: Standard Enterprise defaults if daemon is unreachable
	// Check standard locations in order of most common distros
	candidates := []string{
		"/etc/multipath/wwids",     // RHEL, Ubuntu, Proxmox standard
		"/var/lib/multipath/wwids", // Legacy or specific Debian defaults
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return "/etc/multipath/wwids" // Final best-guess default
}

func (o GetDmsPathHelperGeneric) IsSafeSinglePath(sgDev string) (bool, error) {
    sgBase := filepath.Base(sgDev)
    sdName, err := o.getSdFromSg(sgBase)
    if err != nil { return false, err }

    sysBlockPath := "/sys/block/" + sdName
    wwidBytes, _ := os.ReadFile(filepath.Join(sysBlockPath, "device/wwid"))
    cleanWWID := o.normalizeWWID(string(wwidBytes))

    start := time.Now()
    timeout := o.getEffectiveMultipathTimeout()

    for {
        // 1. Immediate Check: Is it ALREADY part of a Multipath map?
        // (Uses /sys/block/sdX/holders/dm-*)
        if mpath, _ := o.GetMultipathDeviceFromSd(sdName); mpath != "" {
            return false, nil
        }

        // 2. Intent Check: Is this device MARKED for Multipath in the DB?
        // If yes, we must NOT mount the single path.
        if o.isWWIDInMultipathDB(cleanWWID) {
            // We should return false here to force the caller to wait for the dm-device
            return false, nil
        }

        // 3. Probing Check: Is udev currently "locking" this device for scanning?
        if !o.IsDeviceUdevLocked(sdName, start) {
            // Final check: Are there any other kernel holders (LVM, etc.)?
            holders, _ := os.ReadDir(filepath.Join("/sys/block", sdName, "holders"))
            if len(holders) > 0 {
                for _, h := range holders {
                    // Even if it's not "dm-", any holder means the path is NOT "single/free"
                    logger.Debugf("Device %s has holder %s; not a safe single path", sdName, h.Name())
                }
				// TODO is this a bug? If the holder isn't DM it's not multipath
                return false, nil
            }
			return true, nil // It's a clean, unclaimed single path.
        }

        if time.Since(start) > timeout {
			logger.Warnf("Timed out waiting for multipath/udev settle on %s", sdName)
            break
        }
        time.Sleep(500 * time.Millisecond)
    }

    return false, fmt.Errorf("timeout or multipath claim detected on %s", sdName)
}


// TODO do we need gated execution
func (o GetDmsPathHelperGeneric) getSdFromSg(sgBase string) (string, error) {
	sgBase = filepath.Base(sgBase)
	sgPath := filepath.Join("/sys/class/scsi_generic", sgBase, "device")

	var sdPath string
	// Gated execution: sysfs traversal can block if the HBA is busy
	err := o.resourceManager.ExecuteUninterruptible(sgBase, 1, 5, 500*time.Millisecond, 5*time.Second, func() error {
		realPath, err := filepath.EvalSymlinks(sgPath)
		if err != nil { return err }

		blockPath := filepath.Join(realPath, "block")
		entries, err := os.ReadDir(blockPath)
		if err != nil { return err }

		for _, entry := range entries {
			// Filter out anything that isn't a directory-like entry (the sdX device)
			if entry.IsDir() || (entry.Type()&os.ModeSymlink != 0) {
				// Return the full path to avoid ambiguity in downstream Mounter logic
				return filepath.Join("/dev", entry.Name()), nil
			}
		}

		return fmt.Errorf("no block devices found")
	})

	return sdPath, err
}


// TODO is D protection needed
func (o GetDmsPathHelperGeneric) GetMultipathDeviceFromSd(sdName string) (string, error) {
	sdName = filepath.Base(sdName)
	holdersPath := filepath.Join("/sys/block", sdName, "holders")

	var result string
	// Gated execution to prevent D-state hangs during sysfs traversal
	err := o.resourceManager.ExecuteUninterruptible(sdName, 1, 5, 500*time.Millisecond, 5*time.Second, func() error {
		entries, err := os.ReadDir(holdersPath)
		if err != nil { return err }

		for _, entry := range entries {
			dmName := entry.Name() // e.g., "dm-5"
			if !strings.HasPrefix(dmName, "dm-") {
				continue
			}

			// 1. Verify it is a Multipath device via UUID
			uuidPath := filepath.Join("/sys/block", dmName, "dm", "uuid")
			uuidBytes, err := os.ReadFile(uuidPath)
			if err != nil {
				continue
			}

			// IBM/Linux Multipath UUIDs start with "mpath-"
			if bytes.HasPrefix(uuidBytes, []byte("mpath-")) {
				// 2. Resolve to a persistent /dev/mapper name if possible
				// This is critical for IBM storage consistency across node reboots.
				realNamePath := filepath.Join("/sys/block", dmName, "dm", "name")
				if nameBytes, err := os.ReadFile(realNamePath); err == nil {
					mapperName := strings.TrimSpace(string(nameBytes))
					result = filepath.Join("/dev/mapper", mapperName)
					return nil
				}

				// Fallback to the direct dm node
				result = filepath.Join("/dev", dmName)
				return nil
			}
			// Fallback: If it's a PARTITION of a multipath device (e.g., mpath-uuid-part1)
			// Usually CSI uses the whole disk, but this handles legacy cases.
			if bytes.HasPrefix(uuidBytes, []byte("part")) {
				// You may need to look up one level higher if this is a partition
				// but usually 'holders' for sdX points directly to the mpath dm-X.
			}
		}
		return fmt.Errorf("no multipath holder found")
	})

	return result, err
}


// Check if the DM device exists but has no active slave paths
func (o GetDmsPathHelperGeneric) IsStale(dmName string) bool {
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


func (o GetDmsPathHelperGeneric) HasHolders(devicePath string) bool {
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


func (o GetDmsPathHelperGeneric) getWWIDByDevName(devName string) (string, error) {
	// Modern Linux kernels (4.x+) expose the WWID in sysfs for SCSI devices
	wwidPath := fmt.Sprintf("/sys/block/%s/device/wwid", devName)
	data, err := os.ReadFile(wwidPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}


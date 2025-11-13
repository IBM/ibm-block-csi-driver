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

const (
    DM_IOCTL_COMMAND = 0xC130FD06 // DM_DEV_REMOVE code
    DM_VERSION_MAJOR = 4
    DM_VERSION_MINOR = 0
    DM_VERSION_PATCH = 0

    // Equivalent to --noflush: drops pending I/O on remove
    DMF_NOFLUSH_FLAG = 1 << 11 
)

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

    // Prevent hanging on dead SAN paths
    DMF_NOFLUSH_FLAG = 1 << 11 
)

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
    Data         [7]byte // Padding to reach 312 bytes
}





// TODO DMF_NOFLUSH_FLAG only for forced flush

func NativeFlushMultipath(dmName string) error {
    // 1. Open the Device Mapper control node
    control, err := os.OpenFile("/dev/mapper/control", os.O_RDWR, 0)
    if err != nil {
        return fmt.Errorf("failed to open /dev/mapper/control: %w", err)
    }
    defer control.Close()

    // 2. Prepare the ioctl structure
    data := DmIoctl{
        Version: [3]uint32{DM_VERSION_MAJOR, DM_VERSION_MINOR, DM_VERSION_PATCH},
        DataSize: uint32(unsafe.Sizeof(DmIoctl{})),
        // Add the NOFLUSH flag to prevent hangs on dead paths
        Flags: DMF_NOFLUSH_FLAG, 
    }
    
    // Copy the DM name (e.g., "mpatha") into the Name buffer
    copy(data.Name[:], dmName)

    // 3. Execute the ioctl (DM_DEV_REMOVE)
    // In Go, syscall.Syscall is used for direct ioctl calls
    _, _, errno := syscall.Syscall(
        syscall.SYS_IOCTL,
        control.Fd(),
        uintptr(DM_IOCTL_COMMAND),
        uintptr(unsafe.Pointer(&data)),
    )

    if errno != 0 {
        // errno 16 is EBUSY (Device or resource busy)
        return fmt.Errorf("dm_dev_remove ioctl failed for %s: %v", dmName, errno)
    }

    return nil
}




func NativeFlushMultipath(dmName string) error {
    // Verify struct size at compile/runtime to prevent EINVAL
    if size := unsafe.Sizeof(DmIoctl{}); size != 312 {
        return fmt.Errorf("invalid DmIoctl size: expected 312, got %d", size)
    }

    control, err := os.OpenFile("/dev/mapper/control", os.O_RDWR, 0)
    if err != nil {
        return fmt.Errorf("failed to open /dev/mapper/control: %w", err)
    }
    defer control.Close()

    data := DmIoctl{
        Version:   [3]uint32{DM_VERSION_MAJOR, DM_VERSION_MINOR, DM_VERSION_PATCH},
        DataSize:  312,
        DataStart: 312, // Points to end of struct as there is no extra payload
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

// ForcedFlushDM performs a high-reliability flush of a Device Mapper device.
// wwid: The volume's WWID (e.g., 3600605...)
// dmName: The kernel name (e.g., dm-5)
func FlushMultipathDeviceForce(ctx context.Context, wwid string, dmName string) error {
	// 1. Synchronous Buffer Flush (ioctl BLKFLSBUF)
	// Ensures the OS drops its buffer cache references to the device.
	devPath := filepath.Join("/dev", dmName)
	if err := flushDeviceBuffers(devPath); err != nil {
		// Log but continue; if paths are dead, flush might fail
		fmt.Printf("Warning: buffer flush failed for %s: %v\n", devPath, err)
	}

	// 2. Break I/O Queue (fail_if_no_path)
	// Prevents 'multipath -f' or 'dmsetup remove' from hanging in D-state
	// if the physical storage paths are currently unresponsive.
	_, err = r.Executer.ExecuteWithTimeout(TimeOutMultipathCmd, dmsetupCmd, []string{"message", dmName, "0", "fail_if_no_path"})
	// If this fails, the target may not be in 'queueing' mode.
	
	
	
	
	// TOTO ALWAYS replace fail_if_no_path:
	multipathd disablequeueing map <device>: Unblock the kernel I/O queue so the next commands don't hang.
    multipathd del map <device> (Optional but recommended): Ask the daemon to remove the device through its internal management logic.
	
	// multipathd del map OR Native Ioctl Remove (Remove the map)  (????)

	if err := FlushMultipathDevice(dmName); err == nil {
		if verifyDisappearance(dmName) {
			return nil // Success
		}
	}

	// 4. Emergency Fallback: Forced Removal (The "Hammer")
	// Direct kernel communication, bypassing the daemon.
	if output, err := r.Executer.ExecuteWithTimeout(TimeOutMultipathCmd, dmsetupCmd, []string{"remove", "-f", "--noflush", dmName}); err != nil {
		// If even this fails, check if it's already gone
		if !verifyDisappearance(dmName) {
			return fmt.Errorf("forced removal failed: %v, output: %s", err, string(output))
		}
	}

	return nil
}




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




// ForcedFlushDM performs a high-reliability flush of a Device Mapper device.
// wwid: The volume's WWID (e.g., 3600605...)
// dmName: The kernel name (e.g., dm-5)
func FlushMultipathDeviceForceNative(ctx context.Context, wwid string, dmName string) error {
	
	
	
    // This is safe and fast via the socket.
	// changes the policy to fail_if_no_path. This immediately clears the blocked I/O queue, which often "unblocks" the device-mapper control structures so the ioctl can actually enter the kernel and succeed
	// This is a "non-blocking" policy change. It tells the kernel to stop waiting and start failing I/O. This "unblocks" the device so it can be deleted
    _ = e.multipathdSocketCmd("disablequeueing map " + dmName)

    // 2. Try the daemon's delete (The "Clean" removal)
    err := e.multipathdSocketCmd("del map " + dmName)
    if err == nil {
        return nil
    }

    // 3. Fallback to Native Ioctl (The "Nuclear" removal)
    // Only if the daemon couldn't do it or is unresponsive.
    return e.NativeIoctlRemoveWithNoFlush(dmName)	
}










func FlushMultipathDeviceForceNative(ctx context.Context, wwid string, dmName string) error {
    // 1. Unblock the I/O queue
    _ = e.multipathdSocketCmd("disablequeueing map " + dmName)
    
    // Give the kernel a moment to fail the I/O and drop the locks
    time.Sleep(100 * time.Millisecond)

    // 2. Try the daemon-level delete
    if err := e.multipathdSocketCmd("del map " + dmName); err == nil {
        logger.Infof("Successfully removed map %s via socket", dmName)
        return nil
    }

    // 3. The Nuclear Option (Direct Kernel Ioctl)
    logger.Warnf("Socket delete failed for %s, falling back to direct ioctl", dmName)
    return e.NativeIoctlRemoveWithNoFlush(dmName)
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

func (o *OsDeviceConnectivityHelperScsiGeneric) RemoveGhostDevice(expectedSerial string, expectedLun int, isIscsi bool, targetHostIDs []int, targetRports []string) error {
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

		// 3. LUN SLOT CHECK
		lunBytes, _ := os.ReadFile(filepath.Join(deviceDir, "lun"))
		if normalizeLun(string(lunBytes)) != normLun {
			notLun += 1
			continue
		}

		// 1. TRANSPORT SCOPE CHECK
		realPath, err := filepath.EvalSymlinks(deviceDir)
		if err != nil { continue }

		isOurPath := isPathOwnedByMyArray(..)

		// 2. VENDOR CHECK (IBM)
		// Standard 2025 practice: ensures we don't prune other vendors' devices
		vendorBytes, _ := os.ReadFile(filepath.Join(deviceDir, "vendor"))
		isIBM := strings.Contains(string(vendorBytes), "IBM")

		// 4. GHOST DETECTION (PQ=1)
		// If 'block' is missing, it's a PQ=1 ghost (Kernel attached sg but not sd)
		_, err = os.Stat(filepath.Join(deviceDir, "block"))
		isGhost := os.IsNotExist(err)

		isGhost := IsGhostDevice(...)

		// 5. IDENTITY VERIFICATION (Hardware Truth)
		hwSerial := ""
		var hwErr error
		if !isGhost {
			// GetDeviceWWN uses the refactored SG_IO IOCTL
			hwSerial, hwErr = o.GetDeviceWWN("/dev/" + sgName)
		}

		// 6. REMEDIATION LOGIC
		// We delete if it's a Ghost OR if the hardware identity is wrong
		shouldDelete := (isGhost && isIBM) || (isOurPath && (!isIBM || hwErr != nil || !o.IsSerialMatch(hwSerial, expectedSerial)))

		// TODO Can also query the device cached uuid

		// return state == "running" || state == "offline"

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

// GetHCTLFromSg converts an sg name (e.g., "sg5") to its H:C:T:L address (e.g., "12:0:0:1").
func GetHCTLFromSg(sgName string) (string, error) {
	// Path to the symlink in sysfs
	// Example: /sys/class/scsi_generic/sg5
	sysPath := fmt.Sprintf("/sys/class/scsi_generic/%s", sgName)

	// EvalSymlinks resolves the link to the actual device path
	// Example: /sys/devices/pci0000:00/.../target12:0:0/12:0:0:1/scsi_generic/sg5
	realPath, err := filepath.EvalSymlinks(sysPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve sysfs path for %s: %w", sgName, err)
	}

	// The HCTL is the name of the directory three levels up from the sg5 folder
	// [realPath]             = /.../12:0:0:1/scsi_generic/sg5
	// filepath.Dir(realPath) = /.../12:0:0:1/scsi_generic
	// filepath.Dir(...)      = /.../12:0:0:1
	hctlDir := filepath.Dir(filepath.Dir(realPath))
	hctl := filepath.Base(hctlDir)

	// Validate format (Simple check for 3 colons)
	if len(filepath.SplitList(hctl)) == 0 && hctl != "" {
		// Verify it looks like an HCTL (e.g., 12:0:0:1)
		return hctl, nil
	}

	return "", fmt.Errorf("could not determine HCTL from path: %s", realPath)
}


// GetHCTLFromSg converts an sg name (e.g., "sg5") to its H:C:T:L address (e.g., "12:0:0:1").
func GetHCTLFromSg(sgName string) (string, error) {
	// Example: /sys/class/scsi_generic/sg5
	sysPath := fmt.Sprintf("/sys/class/scsi_generic/%s", sgName)

	// Resolves to e.g., /sys/devices/pci0000:00/.../target12:0:0/12:0:0:1/scsi_generic/sg5
	realPath, err := filepath.EvalSymlinks(sysPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve sysfs path for %s: %w", sgName, err)
	}

	// Move up two levels: /.../12:0:0:1/scsi_generic/sg5 -> /.../12:0:0:1
	hctlDir := filepath.Dir(filepath.Dir(realPath))
	hctl := filepath.Base(hctlDir)

	// Validate HCTL format: should have exactly 3 colons (e.g., "12:0:0:1")
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("invalid HCTL format '%s' derived from path: %s", hctl, realPath)
	}

	return hctl, nil
}


func isPathOwnedByMyArray(hctl string, myTargetIdentifier string) bool {
    // hctl e.g. "12:0:0:1"
    // For iSCSI: Check Target IQN
    sessionPath := fmt.Sprintf("/sys/class/scsi_device/%s/device/session*/iscsi_session/session*/targetname", hctl)
    matches, _ := filepath.Glob(sessionPath)
    for _, m := range matches {
        content, _ := os.ReadFile(m)
        if strings.TrimSpace(string(content)) == myTargetIdentifier {
            return true
        }
    }

    // For Fibre Channel: Check Target WWPN
    fcPath := fmt.Sprintf("/sys/class/scsi_device/%s/device/rport-*/fc_remote_ports/rport-*/port_name", hctl)
    fcMatches, _ := filepath.Glob(fcPath)
    for _, m := range fcMatches {
        content, _ := os.ReadFile(m)
        if strings.TrimSpace(string(content)) == myTargetIdentifier {
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
	// 1. FAST CHECK: Sysfs Type 31
	// The kernel maps PQ=1 to device type 31 (0x1f).
	typePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/type", sgName)
	data, err := os.ReadFile(typePath)
	if err == nil {
		if strings.TrimSpace(string(data)) == "31" {
			return true, nil
		}
	}

	// 2. FAST CHECK: Missing Block Device
	// If it's a disk (sd) but the block directory is missing, it's likely a ghost.
	blockPath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/block", sgName)
	if _, err := os.Stat(blockPath); os.IsNotExist(err) {
		// If we expect a disk but it's not there, we proceed to ioctl for truth.
	} else if err == nil {
		// If the block device exists, it's PQ=0 and healthy.
		return false, nil
	}

	// 3. THE TRUTH: Direct SCSI Inquiry (PQ Bits)
	return checkPQviaIoctl(sgName)
}


func checkPQviaIoctl(sgName string) (bool, error) {
	devPath := filepath.Join("/dev", sgName)

	// 2025 Standard: Use O_NONBLOCK to prevent hanging on dead hardware
	f, err := os.OpenFile(devPath, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()

	inqResp := make([]byte, 36)
    senseBuf := make([]byte, 32) // MUST allocate sense buffer	
	cdb := [6]byte{0x12, 0, 0, 0, 36, 0} // Standard INQUIRY
	
    header := sgIoHdr{
        interface_id:    'S',
        dxfer_direction: SG_DXFER_FROM_DEV,
        cmd_len:         uint8(len(cdb)),
        mx_sb_len:       uint8(len(senseBuf)),
        sbp:             uintptr(unsafe.Pointer(&senseBuf[0])), // Provide valid pointer
        dxfer_len:       uint32(len(inqResp)),
        dxferp:          uintptr(unsafe.Pointer(&inqResp[0])),
        cmdp:            uintptr(unsafe.Pointer(&cdb[0])),
        timeout:         2000, // 2s limit
	}
		
	// 0x2285 is the constant for SG_IO
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), SG_IO, uintptr(unsafe.Pointer(&header)))

	if errno != 0 {
		// If the ioctl returns EIO or ENOTTY, the device is likely too dead to
		// report PQ=1. In CSI, we treat these as candidates for cleanup.
		return false, fmt.Errorf("ioctl failed: %v", errno)
	}

	// Peripheral Qualifier is the top 3 bits of Byte 0
	// 001b (binary) = 1 (decimal) = Supported but not connected
	pq := (inqResp[0] >> 5) & 0x07

	return pq == 1, nil
}

func checkPQviaIoctl(sgName string) (bool, error) {
	devPath := filepath.Join("/dev", sgName)

	// O_NONBLOCK prevents hanging on fabrics with transport timeouts
	f, err := os.OpenFile(devPath, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		// (2) If the device file exists but returns ENXIO on open, it's a ghost
		if os.IsNotExist(err) || err == syscall.ENXIO {
			return true, nil
		}
		return false, err
	}
	defer f.Close()

	inqResp := make([]byte, 36)
	senseBuf := make([]byte, 32)
	cdb := [6]byte{0x12, 0, 0, 0, 36, 0}

	header := sgIoHdr{
		interface_id:    'S',
		dxfer_direction: SG_DXFER_FROM_DEV,
		cmd_len:         uint8(len(cdb)),
		mx_sb_len:       uint8(len(senseBuf)), // (4) Explicitly passes 32
		sbp:             uintptr(unsafe.Pointer(&senseBuf[0])),
		dxfer_len:       uint32(len(inqResp)),
		dxferp:          uintptr(unsafe.Pointer(&inqResp[0])),
		cmdp:            uintptr(unsafe.Pointer(&cdb[0])),
		timeout:         1000,
	}

	// (1) Handle transient states (EAGAIN/EBUSY)
	var errno syscall.Errno
	for i := 0; i < 3; i++ {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), SG_IO, uintptr(unsafe.Pointer(&header)))
		if errno != syscall.EAGAIN && errno != syscall.EBUSY {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if errno != 0 {
		// (2) Swallow ENXIO/ENODEV as "Ghost" (true)
		if errno == syscall.ENXIO || errno == syscall.ENODEV {
			return true, nil
		}
		return false, fmt.Errorf("ioctl failed: %v", errno)
	}

	// (3) Distinguish between "Ghost" (PQ=1) and "Path Failure" (Host Status)
	// DID_NO_CONNECT (0x01) or DID_BUS_BUSY (0x02) indicate transport issues.
	// We only return true (Ghost) if the transport is healthy enough to report PQ.
	if header.host_status != 0 {
		return false, fmt.Errorf("transport failure (host_status=0x%x)", header.host_status)
	}

	// PQ bits: 001b = 1 = Peripheral connected but not available (Ghost)
	pq := (inqResp[0] >> 5) & 0x07
	
	
// ... (previous ioctl code) ...

if errno != 0 {
    // ENXIO/ENODEV means the kernel already gave up on this device mapping
    if errno == syscall.ENXIO || errno == syscall.ENODEV {
        return true, nil
    }
    return false, fmt.Errorf("ioctl syscall error: %v", errno)
}

// 1. CHECK TRANSPORT HEALTH FIRST
// If host_status is non-zero, the PQ bit in inqResp is invalid garbage.
// common codes: 0x01 = DID_NO_CONNECT, 0x07 = DID_ERROR, 0x0e = DID_TRANSPORT_DISRUPTED
if header.host_status != 0 {
    return false, fmt.Errorf("transport layer error (host_status=0x%x): path may be down, not a ghost", header.host_status)
}

// 2. CHECK SCSI STATUS
// If the status isn't OK (0x00) or CHECK CONDITION (0x02), we can't trust the data.
if header.status != 0x00 && header.status != 0x02 {
    return false, fmt.Errorf("scsi device error (status=0x%x)", header.status)
}

// 3. NOW TRUST THE DATA
// Peripheral Qualifier is the top 3 bits of Byte 0.
// 0x20 (binary 001xxxxx) means PQ=1
pq := (inqResp[0] >> 5) & 0x07

if pq == 1 {
    // Successfully communicated with the target, and it confirmed the LUN is gone.
    return true, nil
}

return false, nil
	
	return pq == 1, nil
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

func (o OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(dev string) (string, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return "", err
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
		return "", fmt.Errorf("ioctl SG_IO failed on %s: %v", dev, errno)
	}

    // Status 0x02 = Check Condition (Consult senseBuf for details)
    if header.Status != 0 {
        return "", fmt.Errorf("SCSI status error: 0x%02x (Host: 0x%04x, Driver: 0x%04x)", 
            header.Status, header.HostStatus, header.DriverStatus)
    }
    // HostStatus 0x01 = No connect, 0x07 = Timeout, etc.
    if header.HostStatus != 0 {
        return "", fmt.Errorf("SCSI host error: 0x%04x", header.HostStatus)
    }

	actualLen := int(header.DxferLen) - int(header.Resid)
	if actualLen > 0 {
		return parseVPD83(respBuf[:actualLen])
	}
}


func parseVPD83(data []byte) (string, error) {
	if len(data) < 4 {
		return "", fmt.Errorf("invalid VPD data")
	}

	// Total length of descriptors is in bytes 2-3
	pageLen := int(binary.BigEndian.Uint16(data[2:4]))
	cursor := 4
	limit := 4 + pageLen

	var bestID string
	var bestType int = -1

	for cursor+4 <= limit {
		// --- DESCRIPTOR HEADER (4 Bytes) ---
		// Byte 0: [Protocol ID (7:4) | Code Set (3:0)]
		codeSet := data[cursor] & 0x0F

		// Byte 1: [PIV (7) | Association (5:4) | Designator Type (3:0)]
		// CORRECT: Designator Type is in Byte 1, not Byte 3
		association := (data[cursor+1] >> 4) & 0x03
		designatorType := int(data[cursor+1] & 0x0F)

		// Byte 3: Identifier Length
		// CORRECT: Byte 3 is the total length of the data following the header
		length := int(data[cursor+3])

		idStart := cursor + 4
		if idStart+length > len(data) || idStart+length > limit {
			break
		}

		// ASSOCIATION 0 = Logical Unit (The unique volume handle)
		if association == 0 {
			idData := data[idStart : idStart+length]
			
			// --- PRIORITY 1: NAA (Type 3) ---
			// Standard: Strictly Binary (CodeSet 1)
			if designatorType == 3 && codeSet == 1 {
				// Linux scsi_id prepends '3' to the hex representation
				return strings.ToLower(fmt.Sprintf("%x", idData)), nil
			}

			// --- PRIORITY 2: EUI-64 (Type 2) ---
			// Standard: Strictly Binary (CodeSet 1)
			if designatorType == 2 && codeSet == 1 && bestType < 2 {
				// Linux scsi_id prepends '2' to EUI hex
				bestID = strings.ToLower(fmt.Sprintf("%x", idData))
				bestType = 2
			}

			// --- PRIORITY 3: T10 Vendor ID (Type 1) ---
			// Standard: Mixed. Can be Binary (1) or ASCII/UTF-8 (2/3)
			if designatorType == 1 && bestType < 1 {
				var idString string
				if codeSet == 1 {
					idString = fmt.Sprintf("%x", idData)
				} else if codeSet == 2 || codeSet == 3 {
					idString = strings.TrimSpace(string(idData))
				}
				
				if idString != "" {
					bestID = idString
					bestType = 1
				}
			}

			// --- PRIORITY 4: SCSI Name String (Type 8) ---
			// Standard: Strictly String-based (CodeSet 2 or 3)
			// This is the fallback for IQN or NVMe names.
			if designatorType == 8 && (codeSet == 2 || codeSet == 3) && bestType < 0 {
				// Type 8 is already a string format (e.g., "iqn.2003-01.com.storage:...")
				// Linux does NOT prepend a '8' here; it uses the string as-is.
				rawName := strings.ToLower(strings.TrimSpace(string(idData)))
				
				// Normalize the name string by removing delimiters to match the FS function
				bestID = strings.NewReplacer("-", "", ":", "", ".", "", "_", "").Replace(raw				
				bestType = 0
			}
		}

		// Advance to next descriptor: 4-byte header + data length
		cursor += 4 + length
	}

	if bestID != "" {
		return strings.ToLower(bestID), nil
	}

	return "", fmt.Errorf("no suitable volume identifier found in Association 0")
}



func NormalizeOsVolumeIdentifier(id string) string {
	// 1. Standardize basics: lowercase and trim
	id = strings.ToLower(strings.TrimSpace(id))

	// 2. Strip Protocol Prefixes (The "Wrappers")
	// Order matters: longest first.
	prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "pci-", "nvme-", "naa.", "eui."}
	for _, p := range prefixes {
		id = strings.TrimPrefix(id, p)
	}

	// 3. Strip the Linux Designator Type Prefix (The "3", "2", or "1")
	// This is the bit added by scsi_id. We only strip it if it's followed 
	// by a valid hex string that matches standard NAA lengths.
	if len(id) > 0 {
		// Designator Type 3 = NAA
		// Standard NAA-6 (IEEE Registered Extended) is 32 hex chars.
		// If we see 33 chars starting with '3', it's a prefixed NAA-6.
		if id[0] == '3' && (len(id) == 33 || len(id) == 17) {
			id = id[1:]
		} else if id[0] == '2' && (len(id) == 17) {
			// Designator Type 2 = EUI-64 (16 hex chars + prefix '2')
			id = id[1:]
		} else if id[0] == '1' {
			// Designator Type 1 = T10 Vendor ID
			id = id[1:]
		}
	}

	// 4. Final Clean: Remove any remaining non-hex noise (hyphens, dots)
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'f') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	
	return b.String()
}

func NormalizeOsVolumeIdentifier(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))

	// 1. Strip the "OS Wrappers" (mpath-, scsi-, etc.)
	prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "nvme-"}
	for _, p := range prefixes {
		id = strings.TrimPrefix(id, p)
	}

	// 2. Extract Alphanumeric only (remove hyphens/dots/colons)
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	cleanID := b.String()

	// 3. Length-Based Type Validation
	// Linux 'scsi_id' prepends the type (3, 2, or 1).
	// We check if removing that first digit results in a standard identifier length.
	if len(cleanID) > 0 {
		firstChar := cleanID[0]
		remaining := cleanID[1:]
		length := len(remaining)

		switch firstChar {
		case '3': // NAA
			// NAA-6 (IEEE Registered Extended) is 32 hex chars
			// NAA-5 (IEEE Registered) is 16 hex chars
			if length == 32 || length == 16 {
				return cleanID // It's a valid Linux-prefixed NAA
			}
		case '2': // EUI-64
			// EUI-64 is 16 hex chars
			if length == 16 {
				return cleanID
			}
		case '1': // T10 Vendor ID
			// T10 is variable, but usually >= 16. 
			// If it's very long and starts with 1, it's likely a prefixed T10.
			if length >= 16 {
				return cleanID
			}
		}
	}

	// 4. Fallback: If it doesn't match standard Linux 'Type+Hex' lengths,
	// it's either an IQN (Type 8) or a raw HW hex. Return as-is.
	return cleanID
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

	// 2. Primary Logic: Block-Backed Filesystems (Major > 0)
	if major > 0 {
		// Fast lookup via /sys/dev/block mapping
		sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
		realPath, err := filepath.EvalSymlinks(sysPath)
		if err != nil {
			// If sysfs fails but major > 0, it might be a stale mount or udev lag
			return "", fmt.Errorf("major %d detected but not found in sysfs: %w", major, err)
		}
		// Returns "dm-5" or "sda"
		return filepath.Base(realPath), nil
	}

	// 3. Fallback Logic: Virtual/Network Filesystems (Major 0)
	// For NFS, SMB, or tmpfs, we must parse the mount table to find the source.
	return o.getDeviceFromMountInfo(volumePath)
}

func (o OsDeviceConnectivityHelperGeneric) getDeviceFromMountInfo(volumePath string) (string, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", fmt.Errorf("failed to open mountinfo: %w", err)
	}
	defer file.Close()

	targetPath := filepath.Clean(volumePath)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// [0] mount ID | [1] parent ID | [2] major:minor | [3] root | [4] mount point ...
		// The mount point is typically field [4].
		// The source (device/server) is usually after a "-" separator.
		if len(fields) < 5 {
			continue
		}

		// Unescape and clean the mount point from the file
		mntPoint := filepath.Clean(unescapeProcPath(fields[4]))

		if mntPoint == targetPath {
			// Find the hyphen separator index
			// Optional fields start at index 6 and end at the separator
			sepIdx := -1
			for i := 6; i < len(fields); i++ {
				if fields[i] == "-" {
					sepIdx = i
					break
				}
			}

			// The Mount Source is exactly one field after the separator
			if sepIdx != -1 && sepIdx+2 < len(fields) {
				return fields[sepIdx+2], nil // fields[sepIdx+1] is FS Type
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading mountinfo: %w", err)
	}

	return "", fmt.Errorf("no mount found for path %s", targetPath)
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

// getMultipathTimeout attempts to read find_multipaths_timeout from config
func getMultipathTimeout() time.Duration {
	file, err := os.Open("/etc/multipath.conf")
	if err != nil {
		return DefaultMultipathTimeout
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "find_multipaths_timeout") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if val, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
					return time.Duration(val) * time.Second
				}
			}
		}
	}
	return DefaultMultipathTimeout
}

func IsSafeSinglePath(sgDev string) (bool, error) {
	// 1. Resolve Timeout (Sync with multipathd's "Smart" window)
	timeout := getMultipathTimeout()
	
	sgBase := filepath.Base(sgDev)
	blockDir := fmt.Sprintf("/sys/class/scsi_generic/%s/device/block", sgBase)
	files, err := os.ReadDir(blockDir)
	if err != nil || len(files) == 0 {
		return false, fmt.Errorf("failed to map %s to block device", sgDev)
	}
	sdName := files[0].Name()
	sysBlockPath := fmt.Sprintf("/sys/block/%s", sdName)

	// 2. Get WWID to check against the "Intent" database
	wwidBytes, _ := os.ReadFile(filepath.Join(sysBlockPath, "device/wwid"))
	wwid := strings.TrimSpace(string(wwidBytes))

	start := time.Now()
	for time.Since(start) < timeout {
		// A. CHECK HOLDERS: If a DM device appears here, it's NOT a single path.
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
				if strings.Contains(dmUUID, normalizedWWID) && strings.Contains(dmUUID, "mpath") {
					return false, nil
				}
			}
		}

		// B. CHECK WWID DB: If it's in /etc/multipath/wwids, it's a DM device.
		if wwid != "" && isWWIDInMultipathDB(wwid) {
			// It is a multipath device, wait for the DM node to finish
			time.Sleep(1 * time.Second)
			continue
		}

		// C. CHECK UDEV SYSTEMD_READY:
		// In 'Smart' mode, udev keeps SYSTEMD_READY=0 while waiting for 2nd path.
		// If we are still in this window, we MUST NOT use the device yet.
		// We use a simple Sleep here to respect the kernel's "quarantine" period.
		time.Sleep(1 * time.Second)
	}

	// Final check after the full multipath timeout has passed
	holders, _ := os.ReadDir(filepath.Join(sysBlockPath, "holders"))
	return len(holders) == 0, nil
}


// **** TODO   ******    verify all holders are the same - this is the dm (in this case no need to query the link)

// **** TODO   ******    skip ghost sg devices

// IsSafeSinglePath determine if an sg device (e.g., /dev/sg0) is a standalone path.
func IsSafeSinglePath(sgDev string, timeout time.Duration) (bool, error) {
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
			// Device is claimed by a holder; it is NOT a raw single path.
			return false, nil
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
		time.Sleep(500 * time.Millisecond)
	}

	return true, nil
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


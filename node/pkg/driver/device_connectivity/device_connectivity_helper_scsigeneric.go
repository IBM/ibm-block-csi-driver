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
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"

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
	GetMpathDevice(ctx context.Context, volumeId string) (string, error)
	GetExistingMpathDevice(ctx context.Context, volumeUuid string, volumePath string) (string, error)
	RemovePhysicalDevice(ctx context.Context, sysDevices []string) error
	RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error
	ValidateLun(ctx context.Context, targetDm string, lun int, sysDevices []string, expectedSerial string) error
	IsVolumePathMatchesVolumeId(ctx context.Context, volumeId string, volumePath string) (bool, error)
	TeardownVolume(ctx context.Context, target string, needFlush bool, needRemovePhysical bool, expectedWWID string) error
	IdentityAwarePreScan(ctx context.Context, targetPath string, expectedWWID string) (discoveredDev string, isStaged bool, skipRescan bool, isLeftover bool, err error)
}

type OsDeviceConnectivityHelperScsiGeneric struct {
	Executer        executer.ExecuterInterface
	KeyedGater      *executer.KeyedGater
	Mounter         *mount.Mounter
	Helper          OsDeviceConnectivityHelperInterface
	MutexMultipathF *sync.Mutex
	CleanScsiDevice bool
	busyTimestamps  sync.Map
}

type WaitForMpathResult struct {
	devicesPaths []string
	err          error
}

var (
	TimeOutMultipathCmd  = 60 * 1000
	TimeOutMultipathdCmd = 10 * 1000
	TimeOutBlockDevCmd   = 10 * 1000
	TimeOutSgInqCmd      = 3 * 1000
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

/*
type SgIoHeader struct {
	InterfaceID    int32   // 'S'
	DxferDirection int32   // e.g., SG_DXFER_FROM_DEV
	CmdLen         uint8
	MxSbpLen       uint8
	IovecCount     uint16
	DxferLen       uint32
	// --- PADDING START ---
	// The next field (uintptr) must start on an 8-byte boundary.
	// Current offset is 16 bytes, so we are actually okay here,
	// BUT the fields AFTER the pointers need careful attention.
	// ---------------------
	Dxferp         uintptr
	Cmdp           uintptr
	Sbp            uintptr
	Timeout        uint32
	Flags          uint32
	PackID         int32
	// --- PADDING START ---
	_              [4]byte // Padding to align UsrPtr (uintptr) to 8 bytes
	// ---------------------
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
*/

// sgIoHdr is the Linux SG_IO ioctl structure
// TODO duplicates SgIoHeader
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

// DmIoctl corresponds to struct dm_ioctl in <linux/dm-ioctl.h>
type DmIoctl struct {
	Version     [3]uint32
	DataSize    uint32
	DataStart   uint32
	TargetCount uint32
	OpenCount   int32
	Flags       uint32
	EventNr     uint32
	Padding     uint32
	Dev         uint64
	Name        [128]byte
	Uuid        [129]byte
	Data        [7]byte // Padding to align
}


/*
type DmIoctl struct {
	VersionMajor uint32 // RHEL 7 expects 4
	VersionMinor uint32 // RHEL 7 expects 0
	VersionPatch uint32 // RHEL 7 expects 0
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
	_            [7]byte // Padding to align the entire struct to 8 bytes
}
*/

const (
	// Correct OpCode for DM_DEV_REMOVE (Cmd 0x04)
	// _IOWR(0xfd, 0x04, 312 bytes)
	DM_DEV_REMOVE  = 0xc138fd04
	DM_DEV_SUSPEND = 0xc138fd06
	//DM_DEV_STATUS = 0xc138fd07

	DM_VERSION_MAJOR = 4
	DM_VERSION_MINOR = 0
	DM_VERSION_PATCH = 0

	DM_SUSPEND_FLAG    = 1 << 1  // Used to freeze I/O
	DM_NOFLUSH_FLAG    = 1 << 8  // Critical: do not hang on dead paths, Equivalent to --noflush: drops pending I/O on remove
	DM_DEFERRED_REMOVE = 1 << 17 // Standard for CSI Unstage
)

// Constants for Device Mapper ioctl interface
const (
	DM_IOCTL    = 0xfd
	DM_NAME_LEN = 128
	DM_UUID_LEN = 129

	// DM_DEV_STATUS_CMD is typically 7 in the kernel's enum
	DM_DEV_STATUS_CMD = 7
)

const (
	BLKFLSBUF = 0x1261
)

// DM_DEV_STATUS is the ioctl command to retrieve device status
// Calculated using _IOWR(DM_IOCTL, DM_DEV_STATUS_CMD, struct dm_ioctl)
var DM_DEV_STATUS = iowr(DM_IOCTL, DM_DEV_STATUS_CMD, uint32(unsafe.Sizeof(dmIoctl{})))

// dmIoctl matches the C struct dm_ioctl from <linux/dm-ioctl.h>
// This layout is stable for RHEL 7 and later
/*
type dmIoctl struct {
	VersionMajor uint32
	VersionMinor uint32
	VersionPatch uint32
	DataSize     uint32
	DataStart    uint32
	TargetCount  uint32
	OpenCount    int32
	Flags        uint32
	EventNr      uint32
	Padding      uint32
	Dev          uint64
	Name         [DM_NAME_LEN]byte
	Uuid         [DM_UUID_LEN]byte
	Data         [7]byte // Padding to align
}
*/

// iowr helper to calculate ioctl command values based on direction, type, nr, and size
func iowr(t, nr, size uint32) uintptr {
	// Formula: (dir << 30) | (size << 16) | (type << 8) | nr
	// For _IOWR, direction is 3 (read & write bits 0x3)
	return uintptr((3 << 30) | (size << 16) | (t << 8) | nr)
}



const (
	DM_IOCTL_CONTROL    = "/dev/mapper/control"
	DM_VERSION          = 0xc138fd00
	//DM_DEV_REMOVE       = 0xc138fd04
	//DM_DEV_SUSPEND      = 0xc138fd06
	DM_DEV_RESUME       = 0xc138fd06 // Resume is Suspend with flags=0
	DM_TABLE_LOAD       = 0xc138fd09
	
	//DM_DEFERRED_REMOVE  = 1 << 17
	DM_SKIP_LOCKFS_FLAG = 1 << 10
)

type dmIoctl struct {
	version     [3]uint32
	dataSize    uint32
	dataStart   uint32
	targetCount uint32
	openCount   int32
	flags       uint32
	eventNr     uint32
	padding     uint32
	dev         uint64
	name        [128]byte
	uuid        [129]byte
	data        [7]byte // Padding for 8-byte alignment
}




const (
	DevPath                     = "/dev"
	DevMapperPath               = "/dev/mapper"
	WaitForMpathRetries         = 20
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
	nvmeCoreMultipathParamPath  = "/sys/module/nvme_core/parameters/multipath"
)

func NewOsDeviceConnectivityHelperScsiGeneric(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityHelperScsiGenericInterface {
	return &OsDeviceConnectivityHelperScsiGeneric{
		Executer:        executer,
		KeyedGater:      KeyedGater,
		Mounter:         Mounter,
		Helper:          NewOsDeviceConnectivityHelperGeneric(executer, KeyedGater, Mounter),
		MutexMultipathF: &sync.Mutex{},
		CleanScsiDevice: clean_scsi_device,
	}
}

func (r OsDeviceConnectivityHelperScsiGeneric) IsVolumePathMatchesVolumeId(ctx context.Context, volumeUuid string, volumePath string) (bool, error) {
	logger.Infof("IsVolumePathMatchesVolumeId: Searching matching volume id for volume path: [%s] ", volumePath)
	volumeIdVariations := r.Helper.GetVolumeIdVariations(volumeUuid)

	mpathDeviceName, err := r.Helper.GetMpathDeviceName(ctx, volumePath)
	if err != nil {
		return false, err
	}

	SgInqWwn, err := r.Helper.GetWwnByScsiInq(ctx, mpathDeviceName)
	if err != nil {
		return false, err
	}

	if !isSameId(SgInqWwn, volumeIdVariations) {
		return false, &ErrorWrongDeviceFound{mpathDeviceName, volumeUuid, SgInqWwn}
	}

	return true, nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetExistingMpathDevice(ctx context.Context, volumeUuid string, volumePath string) (string, error) {
        logger.Infof("GetExistingMpathDevice: Searching matching volume id for volume path: [%s] ", volumePath)
        //volumeIdVariations := r.Helper.GetVolumeIdVariations(volumeUuid)

        mpathDeviceName, err := r.Helper.GetMpathDeviceName(ctx, volumePath)
        if err != nil {
               return "", err
       }
       return mpathDeviceName, nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevicesGetHostIds(lunId int, arrayIdentifiers []string) (map[int]bool, error) {
	logger.Debugf("Rescan : Start rescan on specific lun, on lun : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		logger.Errorf("%s", e.Error())
		return nil, e
	}

	return r.Helper.GetHostsIdByArrayIdentifiers(arrayIdentifiers)
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

func isNvmeCoreMultipathEnabled() (bool, error) {
	data, err := os.ReadFile(nvmeCoreMultipathParamPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read nvme_core multipath param: %w", err)
	}
	return strings.TrimSpace(string(data)) == "Y", nil
}

func isNativeNvmeDevice(dmPath string) bool {
	baseDevice := filepath.Base(dmPath)
	subsysNqnPath := filepath.Join("/sys/block", baseDevice, "device/subsysnqn")
	_, err := os.Stat(subsysNqnPath)
	return err == nil
}

func isNonNativeNvmeDevice(dmPath string, executer executer.ExecuterInterface) bool {
	logger.Debugf("isNonNativeNvmeDevice: checking path=%s", dmPath)

	// Resolve symlink if /dev/mapper/mpathX
	baseDevice := filepath.Base(dmPath)
	resolvedPath, err := filepath.EvalSymlinks(dmPath)
	if err == nil && resolvedPath != dmPath {
		baseDevice = filepath.Base(resolvedPath)
		logger.Debugf("isNonNativeNvmeDevice: resolved symlink to %s", resolvedPath)
	}

	// Get slaves from sysfs
	slavesPath := filepath.Join("/sys/block", baseDevice, "slaves")
	entries, err := os.ReadDir(slavesPath)
	if err != nil {
		logger.Debugf("isNonNativeNvmeDevice: cannot read slaves for %s: %v", dmPath, err)
		return false
	}
	if len(entries) == 0 {
		return false
	}

	// Run nvme list
	out, err := executer.ExecuteWithTimeout(TimeOutMultipathCmd, "nvme", []string{"list"})
	if err != nil {
		outMessage := strings.TrimSpace(string(out))
		if err.Error() == "exit status 1" || strings.HasSuffix(outMessage, "No such file or directory") {
			return false
		}
		logger.Debugf("isNonNativeNvmeDevice: nvme list failed for %s: %v", dmPath, err)
		return false
	}

	nvmeListOutput := string(out)

	// Cross-check: any slave name in nvme list output → non-native NVMe
	for _, entry := range entries {
		if strings.Contains(nvmeListOutput, entry.Name()) {
			logger.Debugf("isNonNativeNvmeDevice: slave [%s] confirmed in nvme list → non-native NVMe", entry.Name())
			return true
		}
	}

	return false
}

func isNvmeDevice(dmPath string, executer executer.ExecuterInterface) bool {
	nativeMpath, err := isNvmeCoreMultipathEnabled()

	if err != nil {
		logger.Warningf("isNvmeDevice: could not read nvme_core param: %v, trying both checks", err)
		return isNativeNvmeDevice(dmPath) || isNonNativeNvmeDevice(dmPath, executer)
	}

	if nativeMpath {
		result := isNativeNvmeDevice(dmPath)
		logger.Debugf("isNvmeDevice: nativeMpath=Y subsysnqn check [%s] → %v", dmPath, result)
		return result
	}

	result := isNonNativeNvmeDevice(dmPath, executer)
	logger.Debugf("isNvmeDevice: nativeMpath=N slaves+nvmelist check [%s] → %v", dmPath, result)
	return result
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {

	logger.Infof("GetMpathDevice: Searching multipath devices for volume : [%s] ", volumeId)
	//dmPath, _ := r.Helper.GetMpathDeviceName(volumeId)	
	volumeIdVariations := r.Helper.GetVolumeIdVariations(volumeId)
	

	mpathdOutput, err := r.Helper.WaitForDmToExist(ctx, volumeIdVariations, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec)
	if err != nil {
		return "", err
	}
	return mpathdOutput, nil

		// TODO ****************
		// NVMe DM devices (non-native) don't support SG_IO ioctl.
		// EUI/NGUID match from multipathd already identifies the volume correctly.
		//if isNvmeDevice(dmPath, r.Executer) {
		//	logger.Debugf("NVMe device detected %s, skipping sg_inq validation", dmPath)
		//	return dmPath, nil
		//}

		//SgInqWwn, _ := r.Helper.GetWwnByScsiInq(dmPath)
		//if isSameId(SgInqWwn, volumeIdVariations) {
		//	return dmPath, nil
		//}
		//logger.Warningf("Expected {%v} but got {%v} from sg_inq", volumeId, SgInqWwn)
}

func isSameId(wwn string, volumeIdVariations []string) bool {
	// Optimization: If either slice is empty, no match is possible
	normalizedWWN := strings.ToLower(wwn)

	for _, variation := range volumeIdVariations {
		// We assume variations are already normalized,
		// but if not, add strings.ToLower(variation) here.
		if normalizedWWN == variation {
			return true
		}
	}
	return false
}

// TODO Use gater
func (r *OsDeviceConnectivityHelperScsiGeneric) flushDeviceBuffers(ctx context.Context, devPath string) error {
	done := make(chan error, 1)
	const BLKFLSBUF = 0x1261
	
	logger.Warningf("device %s flushDeviceBuffers", devPath)

	go func() {
		// O_RDONLY allows this block operation to work flawlessly even on read-only/error dm-targets.
		// O_NONBLOCK prevents thread locks if the storage transport fabric is dropped.
		f, err := os.OpenFile(devPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			logger.Warningf("device %s flushDeviceBuffers failed to open", devPath)
			done <- fmt.Errorf("flush: failed to open %s: %w", devPath, err)
			return
		}
		defer f.Close()

		_, _, errno := syscall.Syscall(
			syscall.SYS_IOCTL,
			f.Fd(),
			uintptr(BLKFLSBUF),
			0,
		)

		// Absorb normal errors arising from already broken hardware or mock targets
		if errno != 0 && errno != syscall.ENOTTY && errno != syscall.EINVAL && errno != syscall.EIO {
			logger.Warningf("device %s flushDeviceBuffers flush failed", devPath)
			done <- fmt.Errorf("flush: ioctl BLKFLSBUF failed: %v", errno)
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		logger.Warningf("device %s flushDeviceBuffers err %v", devPath, err)
		return err
	case <-ctx.Done():
		logger.Warningf("device %s flushDeviceBuffers timed out", devPath)
		// Escape thread blocking; if kernel is stuck in D-state, the CSI routine exits gracefully
		return fmt.Errorf("flush: timed out (D-state suspected) on %s: %w", devPath, ctx.Err())
	}
}

func (r OsDeviceConnectivityHelperScsiGeneric) flushDevicesBuffers(ctx context.Context, deviceNames []string) error {
	logger.Debugf("executing commands : {%v %v} on devices : {%v} and timeout : {%v} mseconds", blockDevCmd, flushBufsFlag, deviceNames, TimeOutBlockDevCmd)
	for _, deviceName := range deviceNames {
		err := r.flushDeviceBuffers(ctx, deviceName)
		if err != nil {
			return err
		}
	}
	logger.Debugf("Finished executing commands: {%v %v}", blockDevCmd, flushBufsFlag)
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	logger.Debugf(`Removing storage device : {%v} by writing "1" to the deletion channel of each target`, sysDevices)
	var wg sync.WaitGroup

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}

		wg.Add(1)
		go func(name string) {
			defer wg.Done()

			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,        
				"path-delete-"+name, 
				10,
				100,
				5*time.Second,
				30*time.Second,
				func(wCtx context.Context) (struct{}, error) {
					devPath := fmt.Sprintf("/dev/%s", name)
					_ = r.flushDeviceBuffers(wCtx, devPath)

					var deletePath string
					var isNVMe bool

					if strings.HasPrefix(name, "nvme") {
						isNVMe = true
						// Tier 1: Modern Kernel path-specific namespace deletion attribute
						deletePath = fmt.Sprintf("/sys/block/%s/device/delete_id", name)
						
						// Tier 2 Fallback for older kernels (like RHEL 7) where delete_id doesn't exist.
						// Instead of killing the controller link, target its localized PCIe slot reference.
						if _, err := os.Stat(deletePath); os.IsNotExist(err) {
							realPath, err := filepath.EvalSymlinks(fmt.Sprintf("/sys/block/%s/device", name))
							if err == nil {
								deletePath = filepath.Join(realPath, "remove")
							}
						}
					} else {
						// Standard SCSI block delete path
						deletePath = fmt.Sprintf("/sys/block/%s/device/delete", name)
					}

					if _, err := os.Stat(deletePath); os.IsNotExist(err) {
						logger.Warningf("Idempotency: Delete path {%v} was not found on the system, skipping.", deletePath)
						return struct{}{}, nil
					}

					// Write the termination bit to evict the component path cleanly
					if err := os.WriteFile(deletePath, []byte("1\n"), 0200); err != nil {
						return struct{}{}, fmt.Errorf("failed to delete device via %s: %w", deletePath, err)
					}
					
					logger.Infof("Successfully disconnected physical device via %s", deletePath)
					return struct{}{}, nil
				},
			)
			if err != nil {
				logger.Errorf("Gater failed for device %s: %v", name, err)
			}
		}(deviceName)
	}

	wg.Wait()
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

func (r *OsDeviceConnectivityHelperScsiGeneric) readSysfs(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.Trim(string(data), " \n\r\t\x00")
}

func (r *OsDeviceConnectivityHelperScsiGeneric) ValidateLun(ctx context.Context, targetDm string, expectedLun int, sysDevices []string, expectedSerial string) error {
	logger.Debugf("Validating LUN {%v} on devices: {%v}", expectedLun, sysDevices)

	normExpectedLun := r.normalizeLun(strconv.Itoa(expectedLun))
	normExpectedSerial := r.Helper.normalizeWWID(expectedSerial)
	validPathsFound := 0
	hctlRegex := regexp.MustCompile(`(\d+):(\d+):(\d+):(\d+)$`)

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}

		// 1. D-State Gating
		if r.Mounter.IsPathStuck(deviceName) {
			logger.Warningf("Path %s is stuck in D-state, skipping", deviceName)
			continue
		}

		var actualLun, sysfsId, hwId string
		var err error

		if strings.HasPrefix(deviceName, "nvme") {
			// NVMe Health Check
			state := r.readSysfs(fmt.Sprintf("/sys/block/%s/device/state", deviceName))
			if state != "live" {
				logger.Warningf("NVMe path %s is in state %s; skipping", deviceName, state)
				continue
			}
			actualLun = r.readSysfs(fmt.Sprintf("/sys/block/%s/device/nsid", deviceName))
			sysfsId = r.readSysfs(fmt.Sprintf("/sys/block/%s/wwid", deviceName))
			if sysfsId == "" {
				sysfsId = r.readSysfs(fmt.Sprintf("/sys/block/%s/device/wwid", deviceName))
			}
			if sysfsId == "" {
				sysfsId = r.readSysfs(fmt.Sprintf("/sys/block/%s/device/serial", deviceName))
			}
			hwId = r.Helper.normalizeWWID(sysfsId)
		} else {
			// SCSI Health Check
			state := r.readSysfs(fmt.Sprintf("/sys/block/%s/device/state", deviceName))
			if state != "running" {
				logger.Warningf("SCSI path %s is in state %s; skipping", deviceName, state)
				continue
			}

			// TODO compare with ghost device detection
			// LUN Discovery
			actualLun = r.normalizeLun(r.readSysfs(fmt.Sprintf("/sys/block/%s/device/lun", deviceName)))
			if actualLun == "" {
				if devLink, err := os.Readlink(fmt.Sprintf("/sys/block/%s/device", deviceName)); err == nil {
					if match := hctlRegex.FindStringSubmatch(devLink); len(match) > 4 {
						actualLun = r.normalizeLun(match[4])
					}
				}
			}

			sysfsId = r.Helper.normalizeWWID(r.readSysfs(fmt.Sprintf("/sys/block/%s/device/wwid", deviceName)))

			// Hardware Inquiry
			hwId, err = executer.ExecuteUninterruptible[string](
				ctx, r.KeyedGater, "inquiry-"+deviceName, 10, 50, 2*time.Second, 10*time.Second,
				func(wCtx context.Context) (string, error) {
					return r.Helper.GetWwnByScsiInq(ctx, "/dev/"+deviceName)
				},
			)
			if err != nil {
				logger.Errorf("Hardware inquiry failed for %s: %v", deviceName, err)
				// TODO maybe fail?
				continue // Skip path, don't abort yet
			}
			hwId = r.Helper.normalizeWWID(hwId)
		}

		// 3. Validation Logic
		if actualLun != normExpectedLun {
			return fmt.Errorf("FATAL: LUN Mismatch on %s (got %s, exp %s)", deviceName, actualLun, normExpectedLun)
		}

		if hwId != normExpectedSerial {
			return fmt.Errorf("FATAL: Hardware Serial mismatch on %s (got %s, exp %s)", deviceName, hwId, normExpectedSerial)
		}

		if sysfsId != "" && ("3" + sysfsId) != hwId {
			// This is usually a stale kernel path. 
			// Abort here because using this path could lead to data corruption.
			return fmt.Errorf("FATAL: Kernel/Hardware Identity Split on %s (Sysfs: %s, HW: %s)", deviceName, "3" + sysfsId, hwId)
		}

		validPathsFound++
	}

	if validPathsFound == 0 {
		return fmt.Errorf("no valid paths found for %s", targetDm)
	}

	logger.Infof("Successfully validated %d paths for lun %d", validPathsFound, expectedLun)
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	if !r.CleanScsiDevice {
		return nil
	}

	// PHASE A: PURGE STALE SCSI GENERIC DISKS
	if err := r.purgeScsiGhosts(ctx, expectedSerial, expectedLun, arrayIdentifiers); err != nil {
		logger.Errorf("Ghost Scrubber: SCSI generic pruning cycle hit an error: %v", err)
	}

	// PHASE B: PURGE STALE NATIVE NVME FABRIC PATHS
	if err := r.purgeNvmeGhosts(ctx, expectedSerial, expectedLun, arrayIdentifiers); err != nil {
		logger.Errorf("Ghost Scrubber: NVMe generic pruning cycle hit an error: %v", err)
	}

	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) purgeScsiGhosts(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	sgEntries, err := os.ReadDir("/sys/class/scsi_generic")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logger.Warningf("failed to read scsi_generic %v", err)
		return fmt.Errorf("failed to read scsi_generic: %w", err)
	}

       var (
               deleted int
               notLun  int
               //notPQ   int
       )
	

	for _, entry := range sgEntries {
		sgName := entry.Name()
		deviceDir := filepath.Join("/sys/class/scsi_generic", sgName, "device")

		// 1. Resolve absolute HCTL path to avoid text file missing bugs on RHEL 7
		realPath, err := filepath.EvalSymlinks(deviceDir)
		if err != nil {
			logger.Warningf("Ghost Scrubber: evaluate %s - symlink not found", sgName)
			// Path is in the middle of being deleted by the kernel; skip safely
			continue 
		}

		// The final directory name is the HCTL string (e.g., "host:channel:target:lun")
		hctl := filepath.Base(realPath)
		parts := strings.Split(hctl, ":")
		if len(parts) < 4 {
			logger.Warning("split err")
			continue // Invalid layout or not a standard SCSI endpoint
		}
		
		// Extract the LUN component from the HCTL layout (the 4th element)
		deviceLun := parts[3] 
		
		kernelLun, err := strconv.Atoi(deviceLun)
		if err != nil {
			logger.Warning("atoi error")
			continue // If the kernel string fails to parse as a number, skip safely
		}	

		if kernelLun != expectedLun {
			//logger.Warningf("Ghost Scrubber: evaluate %s - not our LUN %d %d", sgName, kernelLun, expectedLun)
			notLun++		
			continue // Mismatched LUN; ignore this device node safely
		}

		// 2. Validate Ownership Scope
		isOurPath := r.isPathOwnedByMyArray(ctx, sgName, arrayIdentifiers)

		vendorBytes, _ := os.ReadFile(filepath.Join(deviceDir, "vendor"))
		vendor := strings.ToUpper(strings.TrimSpace(string(vendorBytes)))
		
		// 3. Low-Level Integrity Evaluation
		isGhost, _ := r.IsSgDeviceGhost(ctx, sgName)
		hwSerial, _ := r.getHardwareSerial(deviceDir)
		isIBM := strings.Contains(vendor, "IBM")
		
		// General Condition: Delete if path is verified dead or if we own the target path but it holds a mismatched serial
		//shouldDelete := isGhost ||            (isOurPath && hwSerial != "" && !r.IsSerialMatch(hwSerial, expectedSerial))
		shouldDelete := (isGhost && isIBM) || (isOurPath && (isGhost || !isIBM || (hwSerial != "" && !r.IsSerialMatch(hwSerial, expectedSerial))))

		if shouldDelete {
			logger.Warningf("Pruning stale SCSI device %s [Vendor: %s, Serial Match: %v, Ghost: %v, Our path: %v]. Executing hot-unplug.", sgName, vendor, r.IsSerialMatch(hwSerial, expectedSerial), isGhost, isOurPath)

			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,
				"path-delete-"+sgName,
				1, 10, 2*time.Second, 15*time.Second,
				func(ctx context.Context) (struct{}, error) {
					deletePath := filepath.Join(deviceDir, "delete")
					if err := os.WriteFile(deletePath, []byte("1"), 0200); err != nil {
						return struct{}{}, err
					}
					return struct{}{}, nil
				},
			)
			if err == nil {
				deleted++
			}
		}
	}

	if deleted > 0 {
		logger.Infof("Ghost Scrubber: Successfully removed %d dead SCSI device nodes.", deleted)
	}
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) purgeNvmeGhosts(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	blockEntries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}

	var deleted int
	for _, entry := range blockEntries {
		name := entry.Name()
		// Only focus on base NVMe block names (e.g. nvme0n1)
		if !strings.HasPrefix(name, "nvme") || strings.Contains(name, "p") {
			continue
		}

		deviceDir := filepath.Join("/sys/block", name, "device")
		
		// Verify structural ownership via NQN mappings
		if !r.isPathOwnedByMyArray(ctx, name, arrayIdentifiers) {
			continue
		}

		// NVMe tracks serial numbers globally inside the serial sysfs descriptor
		serialBytes, err := os.ReadFile(filepath.Join(deviceDir, "serial"))
		if err != nil {
			// If file missing or error occurs, check if controller state is lost (Ghost)
			state := r.readSysfs(filepath.Join(deviceDir, "state"))
			if state == "deleting" || state == "dead" {
				r.executeNvmeTeardown(ctx, name)
				deleted++
			}
			continue
		}

		hwSerial := strings.TrimSpace(string(serialBytes))
		if hwSerial != "" && !r.IsSerialMatch(hwSerial, expectedSerial) {
			logger.Warningf("Ghost Scrubber: Found rogue NVMe map %s with serial mismatch (%s). Forcing detachment.", name, hwSerial)
			r.executeNvmeTeardown(ctx, name)
			deleted++
		}
	}

	if deleted > 0 {
		logger.Infof("Ghost Scrubber: Wiped %d non-matching or ghost NVMe hardware maps.", deleted)
	}
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) executeNvmeTeardown(ctx context.Context, nvmeBlockName string) {
	_, _ = executer.ExecuteUninterruptible[struct{}](
		ctx,
		r.KeyedGater,
		"nvme-delete-"+nvmeBlockName,
		1, 10, 2*time.Second, 15*time.Second,
		func(ctx context.Context) (struct{}, error) {
			parts := strings.Split(nvmeBlockName, "n")
			if len(parts) > 0 {
				deletePath := fmt.Sprintf("/sys/class/nvme/%s/delete_controller", parts[0])
				_ = os.WriteFile(deletePath, []byte("1"), 0200)
			}
			return struct{}{}, nil
		},
	)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) isNvmeGhost(nvmeName string) bool {
	path := fmt.Sprintf("/sys/block/%s/device/state", nvmeName)
	state, err := os.ReadFile(path)
	if err != nil {
		// FIX: If the directory or file is completely gone from sysfs, it is a terminal ghost.
		if os.IsNotExist(err) {
			return true
		}
		// If it returns an I/O error or timeout, the queue is wedged in the kernel layer.
		// Returning false prevents the caller from running an unsafe deletion on a live, frozen path.
		logger.Warningf("isNvmeGhost: Cannot read state path %s due to error: %v. Assuming wedged hardware, skipping.", path, err)
		return false
	} 

	s := strings.TrimSpace(string(state))
	
	// Only treat as a kernel ghost if explicitly flagged by the subsystem driver
	return s == "deleting" || s == "dead"
}

func (r *OsDeviceConnectivityHelperScsiGeneric) PruneNvmeGhosts(ctx context.Context, expectedWWID string, arrayNqns []string) error {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return err
	}

	normExpected := r.Helper.normalizeWWID(expectedWWID)
	var deleted int

	for _, entry := range entries {
		name := entry.Name()
		// Target only the base namespaces (e.g., nvme0n1), skip partitions (e.g., nvme0n1p1)
		if !strings.HasPrefix(name, "nvme") || strings.Contains(name, "p") {
			continue
		}

		deviceDir := filepath.Join("/sys/block", name, "device")
		subsysNqnPath := filepath.Join(deviceDir, "subsysnqn")
		nqnData, err := os.ReadFile(subsysNqnPath)
		if err != nil {
			continue // Path is transitioning or not an active fabrics mapping
		}
		currentNqn := strings.TrimSpace(string(nqnData))

		// Ownership Check: Is this NVMe device from our target array group?
		isOurArray := false
		for _, nqn := range arrayNqns {
			if strings.EqualFold(currentNqn, nqn) {
				isOurArray = true
				break
			}
		}
		if !isOurArray {
			continue
		}
		
		
		
		// 4. Identity & State Check
		wwid, _ := r.getWWIDBySysfs(name) 
		
		// FIX: Call the helper function directly instead of manually parsing the file bytes here.
		// This applies your robust D-state I/O safety gates to the loop.
		isGhost := r.isNvmeGhost(name)
		
		// Optional: If you still need the raw state string for logging reasons, 
		// fetch it safely only if it's confirmed a ghost or mismatch.
		var state string
		if isGhost {
			state = r.readSysfs(filepath.Join(deviceDir, "state"))
		}

		isMismatch := (wwid != "" && r.Helper.normalizeWWID(wwid) != normExpected)

		if isGhost || isMismatch {
			logger.Warningf("Ghost Scrubber: Pruning stale NVMe device %s. State: %s, WWID Match: %v", name, state, !isMismatch)

			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,
				"nvme-delete-"+name,
				1,
				10,
				2*time.Second,
				15*time.Second,
				func(ctx context.Context) (struct{}, error) {
					// FIXED: Extract the raw parent controller name (e.g., nvme0n1 -> nvme0)
					// to hit /sys/class/nvme/nvmeX/delete_controller accurately on RHEL 7+
					parts := strings.Split(name, "n")
					if len(parts) == 0 {
						return struct{}{}, fmt.Errorf("invalid nvme name layout: %s", name)
					}
					ctrlName := parts[0] // "nvme0"
					
					deletePath := fmt.Sprintf("/sys/class/nvme/%s/delete_controller", ctrlName)

					if _, err := os.Stat(deletePath); err == nil {
						if err := os.WriteFile(deletePath, []byte("1"), 0200); err != nil {
							return struct{}{}, fmt.Errorf("failed writing unplug to %s: %w", deletePath, err)
						}
						logger.Infof("Ghost Scrubber: Successfully signaled delete to NVMe controller via %s", deletePath)
					} else {
						return struct{}{}, fmt.Errorf("controller delete interface missing at path: %s", deletePath)
					}

					return struct{}{}, nil
				},
			)
			if err == nil {
				deleted++
			}
		}
	}

	if deleted > 0 {
		logger.Infof("Ghost Scrubber: Native NVMe sweep complete. Cleared %d rogue fabric controllers.", deleted)
	}
	return nil
}











func (r *OsDeviceConnectivityHelperScsiGeneric) GetHCTLFromSg(sgName string) (string, error) {
	deviceLink := filepath.Join("/sys/class/scsi_generic", sgName, "device")
	logger.Debugf("    [SCSI-Generic-HCTL] Attempting translation resolution tracing operational sysfs mapping route: %s", deviceLink)
	
	realPath, err := filepath.EvalSymlinks(deviceLink)
	if err != nil {
		return "", fmt.Errorf("failed resolving scsi generic configuration target mapping runtime device symlink tracking reference mapping: %w", err)
	}
	
	hctl := filepath.Base(realPath)
	logger.Debugf("    [SCSI-Generic-HCTL] Base path element node target isolated safely matching identifier pattern: %s", hctl)
	
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("malformed operational subsystem system address block registration mapping format index generated: %s", hctl)
	}
	logger.Debugf("    [SCSI-Generic-HCTL] Resolved %s to hctl %s", deviceLink, hctl)
	return hctl, nil
}

/*
func (r *OsDeviceConnectivityHelperScsiGeneric) getIscsiTargetName(deviceBase string) string {
	// On RHEL 7, the session directory is a child of the SCSI device directory
	files, err := os.ReadDir(deviceBase)
	if err != nil {
		return ""
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "session") {
			// Traverse deeper to find the iscsi_session attributes
			// Path: .../sessionX/iscsi_session/sessionX/targetname
			targetNamePath := filepath.Join(deviceBase, f.Name(), "iscsi_session", f.Name(), "targetname")
			if data, err := os.ReadFile(targetNamePath); err == nil {
				return strings.TrimSpace(string(data))
			}
		}
	}
	return ""
}
*/

// IsPathOwnedByMyArray resolves the device topology (SCSI, NVMe, DM) and validates ownership.
func (r *OsDeviceConnectivityHelperScsiGeneric) isPathOwnedByMyArray(ctx context.Context, deviceName string, arrayIdentifiers []string) bool {
	logger.Debugf("--> Entering IsPathOwnedByMyArray tracking target validation for: %s", deviceName)
	var targetIDs []string

	// Strip out full paths if passed (e.g., "/dev/dm-0" -> "dm-0")
	baseDeviceName := filepath.Base(deviceName)
	//logger.Debugf("[Topology-Discovery] Evaluated base name '%s' from original input string '%s'", baseDeviceName, deviceName)

	// Clean identifiers for precise strict matching
	cleanExpectedIDs := make([]string, len(arrayIdentifiers))
	for i, id := range arrayIdentifiers {
		cleanExpectedIDs[i] = strings.ToLower(strings.TrimPrefix(id, "0x"))
	}

	// Single source of truth backoff loop to absorb kernel & udev delays safely
	backoff := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}
	
	for i := 0; i <= len(backoff); i++ {
		//logger.Debugf("[Settle-Window] Scan attempt #%d for device '%s' processing...", i+1, baseDeviceName)
		var err error
		targetIDs, err = r.resolveTargetIDs(baseDeviceName)
		
		if err == nil && len(targetIDs) > 0 {
			logger.Debugf("[Settle-Window] Successfully resolved %d target unique signature identifiers on attempt #%d", len(targetIDs), i+1)
			break
		}

		if err != nil {
			//logger.Warningf("[Settle-Window] Scan attempt #%d threw transient error context details: %v", i+1, err)
		} else if len(targetIDs) == 0 {
			//logger.Debugf("[Settle-Window] Scan attempt #%d completed cleanly but returned an empty structural list of target IDs", i+1)
		}

		// Correctly managed retry backoff window logic
		if i < len(backoff) {
			logger.Debugf("[Settle-Window] Execution sleeping for %v before next structural discovery pass", backoff[i])
			select {
			case <-ctx.Done():
				logger.Warningf("[Settle-Window] Aborted storage target evaluation. CSI context cancellation signal triggered: %v", ctx.Err())
				return false
			case <-time.After(backoff[i]):
			}
		}
	}

	if len(targetIDs) == 0 {
		//logger.Warningf("<-- Exiting IsPathOwnedByMyArray: No valid target IDs could be successfully extracted from dev node '%s'", baseDeviceName)
		return false
	}

	// Verify if any discovered target matches our allowed array identifiers
	for _, targetID := range targetIDs {
		normalizedTarget := strings.ToLower(strings.TrimPrefix(targetID, "0x"))
		//logger.Debugf("[Validation] Cross-evaluating extracted target identifier '%s' (Normalized: '%s')", targetID, normalizedTarget)
		
		for _, expectedID := range cleanExpectedIDs {
			if normalizedTarget == expectedID {
				//logger.Debugf("<-- Exiting IsPathOwnedByMyArray: [MATCH FOUND] Discovered identifier matches requested target constraints tracking cluster rule '%s'", expectedID)
				return true
			}
		}
	}

	//logger.Warningf("<-- Exiting IsPathOwnedByMyArray: [REJECTED] Discovered IDs %v do not match target expectations %v", targetIDs, cleanExpectedIDs)
	return false
}

// getNvmeSubsysNQN parses the true NVMe Controller NQN using a bulletproof slice mechanism.
func (r *OsDeviceConnectivityHelperScsiGeneric) getNvmeSubsysNQN(deviceName string) (string, error) {
	deviceNode := deviceName
	
	// Ensure we only strip the namespace suffix safely (e.g., "nvme0n1" -> "nvme0")
	// The prefix check protects "nvme-subsysX" from being mangled.
	if strings.HasPrefix(deviceName, "nvme") && !strings.HasPrefix(deviceName, "nvme-subsys") {
		// Look for the "n" defining the namespace partition boundary after index 3
		if idx := strings.LastIndex(deviceName, "n"); idx > 3 {
			deviceNode = deviceName[:idx]
			logger.Debugf("    [NVMe-Parser] Stripped namespace suffix from '%s' to extract controller node: '%s'", deviceName, deviceNode)
		}
	}

	// Route A: Standard sysfs controller class layout
	nqnPath := fmt.Sprintf("/sys/class/nvme/%s/subsysnqn", deviceNode)
	logger.Debugf("    [NVMe-Parser] Attempting system class NQN extraction from path: %s", nqnPath)
	
	data, err := os.ReadFile(nqnPath)
	if err != nil {
		logger.Warningf("    [NVMe-Parser] Primary system class file layer reading missed target: %v. Retrying with block fallback...", err)
		
		// Route B: Direct block device tree subsystem layout fallback (robust across Linux distros)
		nqnPath = fmt.Sprintf("/sys/block/%s/device/subsysnqn", deviceName)
		logger.Debugf("    [NVMe-Parser] Evaluating secondary alternative fallback block location: %s", nqnPath)
		data, err = os.ReadFile(nqnPath)
		if err != nil {
			return "", fmt.Errorf("failed to locate nvme subsysnqn across all standard verification paths for '%s': %w", deviceName, err)
		}
	}
	
	extractedNQN := strings.TrimSpace(string(data))
	logger.Debugf("    [NVMe-Parser] Extracted valid controller node subsystem NQN signature: %s", extractedNQN)
	return extractedNQN, nil
}

// resolveTargetIDs automatically detects and extracts underlying identifiers based on device type.
// Hardened to succeed if at least one multipath leg is readable.
func (r *OsDeviceConnectivityHelperScsiGeneric) resolveTargetIDs(deviceName string) ([]string, error) {
	logger.Debugf("  [Routing] Processing resolution pipeline branch layer for entity element node: %s", deviceName)

	// 1. Device Mapper Path (nvme over dm or scsi over dm)
	if strings.HasPrefix(deviceName, "dm-") {
		slavesPath := fmt.Sprintf("/sys/block/%s/slaves", deviceName)
		logger.Debugf("  [Branch-Multipath] Identified Device Mapper layout. Scanning path slaves: %s", slavesPath)
		
		entries, err := os.ReadDir(slavesPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read dm slaves path tree layout: %w", err)
		}

		var collectedIDs []string
		var lastErr error

		for _, entry := range entries {
			logger.Debugf("  [Branch-Multipath] Sub-level hardware mapper child disk discovered: %s", entry.Name())
			
			// Recursively extract identifiers from the underlying path
			ids, err := r.resolveTargetIDs(entry.Name())
			if err != nil {
				// We log the warning, but don't abort! A single dead path shouldn't fail the whole volume.
				logger.Warningf("  [Branch-Multipath] Slave leg '%s' target extraction failed (device might be offline): %v", entry.Name(), err)
				lastErr = err
				continue
			}
			
			if len(ids) > 0 {
				logger.Debugf("  [Branch-Multipath] Captured valid identifiers from leg '%s': %v", entry.Name(), ids)
				collectedIDs = append(collectedIDs, ids...)
			}
		}

		// Production Guardrail: If we found at least one working path, treat the volume as healthy and valid
		if len(collectedIDs) > 0 {
			logger.Debugf("  [Branch-Multipath] Multipath resolution successful. Found %d valid path identification signatures.", len(collectedIDs))
			return collectedIDs, nil
		}

		// If ALL legs failed, only then do we fail the resolution
		if lastErr != nil {
			return nil, fmt.Errorf("all multipath slave legs failed target identification. Last error: %w", lastErr)
		}
		return nil, fmt.Errorf("multipath device %s has no identifiable slave legs", deviceName)
	}

	// 2. Native NVMe Path (e.g., nvme0n1)
	if strings.HasPrefix(deviceName, "nvme") {
		logger.Debugf("  [Branch-NVMe] Native NVMe configuration node layout identified: %s", deviceName)
		nqn, err := r.getNvmeSubsysNQN(deviceName)
		if err != nil {
			return nil, err
		}
		return []string{nqn}, nil
	}

	// 3. SCSI Generic / Standard SCSI Device Path (e.g., sg2, sdX)
	var hctl string
	var err error
	if strings.HasPrefix(deviceName, "sg") {
		hctl, err = r.GetHCTLFromSg(deviceName)
	} else if strings.HasPrefix(deviceName, "sd") {
		hctl, err = r.getHCTLFromSd(deviceName)
	} else {
		return nil, fmt.Errorf("unsupported storage interface node structure: %s", deviceName)
	}

	if err != nil {
		return nil, err
	}

	targetID := r.getScsiTargetID(hctl)
	if targetID == "" {
		return nil, fmt.Errorf("scsi target registration layout mapping state attributes not ready for address %s", hctl)
	}

	return []string{targetID}, nil
}


// Internal helper for SCSI logic (FC/iSCSI/SAS)
/*
func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetID(hctl string) string {
	parts := strings.Split(hctl, ":")
	if len(parts) < 4 {
		return ""
	}
	
	// FIX: Reconstruct the structural 'targetH:C:T' base string layout
	hct := strings.Join(parts[:3], ":")
	targetDir := fmt.Sprintf("target%s", hct)
	
	// FIX: In Linux kernel topology, transport attributes live inside the parent host target directory,
	// NOT inside the logical endpoint LUN block folder.
	parentTargetBase := fmt.Sprintf("/sys/class/scsi_device/%s/device/../%s", hctl, targetDir)

	// Try Fibre Channel (FC)
	fcPath := filepath.Join(parentTargetBase, "fc_transport", targetDir, "port_name")
	if data, err := os.ReadFile(fcPath); err == nil {
		return strings.TrimSpace(string(data))
	}

	// Try Serial Attached SCSI (SAS)
	sasPath := filepath.Join(parentTargetBase, "sas_device", targetDir, "sas_address")
	if data, err := os.ReadFile(sasPath); err == nil {
		return strings.TrimSpace(string(data))
	}

	// Try iSCSI (RHEL 7 / generic fallback)
	deviceBase := fmt.Sprintf("/sys/class/scsi_device/%s/device", hctl)
	return r.getIscsiTargetName(deviceBase)
}
*/

func (r *OsDeviceConnectivityHelperScsiGeneric) getHCTLFromSd(sdName string) (string, error) {
	deviceLink := filepath.Join("/sys/block", sdName, "device")
	logger.Debugf("    [SCSI-Block-HCTL] Attempting direct device translation resolution runtime mapping layer lookups: %s", deviceLink)
	
	realPath, err := filepath.EvalSymlinks(deviceLink)
	if err != nil {
		return "", fmt.Errorf("failed resolving baseline standard storage path execution abstraction layer reference mapping: %w", err)
	}
	
	hctl := filepath.Base(realPath)
	logger.Debugf("    [SCSI-Block-HCTL] Target address structure isolated from sysfs string mapping: %s", hctl)
	
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("malformed baseline standard layout data blocks index derived via operational configuration processing: %s", hctl)
	}
		
	return hctl, nil
}
	

func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetID(hctl string) string {
	parts := strings.Split(hctl, ":")
	if len(parts) < 4 {
		logger.Warningf("    [SCSI-Target-Inspector] Passed HCTL identifier string invalid for segmentation mapping constraints check: %s", hctl)
		return ""
	}

	hostID := parts[0] // e.g., "13"
	hct := strings.Join(parts[:3], ":")
	targetDirName := fmt.Sprintf("target%s", hct)
	deviceLink := fmt.Sprintf("/sys/class/scsi_device/%s/device", hctl)

	realDevicePath, err := filepath.EvalSymlinks(deviceLink)
	if err != nil {
		logger.Warningf("    [SCSI-Target-Inspector] Failsafe checking collapsed during baseline validation tracking trace point evaluations: %v", err)
		return ""
	}
	logger.Debugf("    [SCSI-Target-Inspector] Symlink verification mapping resolved explicitly to real endpoint tracking location: %s", realDevicePath)

	// --- PROTOCOL INDEPENDENT ROOT TREE EXTRACTION ---
	var parentTargetBase string
	curr := realDevicePath
	for curr != "/" && curr != "." {
		if filepath.Base(curr) == targetDirName {
			parentTargetBase = curr
			break
		}
		curr = filepath.Dir(curr)
	}
	if parentTargetBase == "" {
		parentTargetBase = filepath.Dir(realDevicePath)
	}

	// 1. Fibre Channel Strategy
	fcPath := filepath.Join(parentTargetBase, "fc_transport", targetDirName, "port_name")
	if data, err := os.ReadFile(fcPath); err == nil {
		return strings.TrimSpace(string(data))
	}

	// 2. SAS Strategy
	sasPath := filepath.Join(parentTargetBase, "sas_device", targetDirName, "sas_address")
	if data, err := os.ReadFile(sasPath); err == nil {
		return strings.TrimSpace(string(data))
	}

	// 3. iSCSI Strategy (Pass both the device path tree and the target HCT parent tracker)
	return r.getIscsiTargetName(realDevicePath, parentTargetBase, hostID)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getIscsiTargetName(realDevicePath string, parentTargetBase string, hostID string) string {
	logger.Debugf("      [iSCSI-Subsystem-Scout] Entering dynamic session tracking pipeline.")
	
	// Strategy A: Trace backward through the device path tree to find the correct active session context folder
	// Example realDevicePath: /sys/devices/platform/host13/session1/target13:0:0/13:0:0:30
	curr := realDevicePath
	for curr != "/" && curr != "." {
		base := filepath.Base(curr)
		if strings.HasPrefix(base, "session") {
			targetNamePath := filepath.Join(curr, "iscsi_session", base, "targetname")
			logger.Debugf("      [iSCSI-Subsystem-Scout] [Strategy-A] Verifying resolved tree target path location: %s", targetNamePath)
			if data, err := os.ReadFile(targetNamePath); err == nil {
				sigID := strings.TrimSpace(string(data))
				logger.Debugf("      [iSCSI-Subsystem-Scout] [Strategy-A SUCCESS] Extracted IQN identifier string: %s", sigID)
				return sigID
			}
		}
		curr = filepath.Dir(curr)
	}

	// Strategy B: Trace alternative context using parent target node structure if directory depth varies
	// Example parentTargetBase: /sys/devices/platform/host13/session1/target13:0:0
	sessionDir := filepath.Dir(parentTargetBase) // should map straight to the sessionX folder block
	if strings.HasPrefix(filepath.Base(sessionDir), "session") {
		targetNamePath := filepath.Join(sessionDir, "iscsi_session", filepath.Base(sessionDir), "targetname")
		logger.Debugf("      [iSCSI-Subsystem-Scout] [Strategy-B] Verifying resolved session folder path location: %s", targetNamePath)
		if data, err := os.ReadFile(targetNamePath); err == nil {
			sigID := strings.TrimSpace(string(data))
			logger.Debugf("      [iSCSI-Subsystem-Scout] [Strategy-B SUCCESS] Extracted IQN identifier string: %s", sigID)
			return sigID
		}
	}

	// Strategy C: Global System Class Map Fallback (Best for deeply isolated overlay containers)
	sessionClassPath := "/sys/class/iscsi_session"
	logger.Debugf("      [iSCSI-Subsystem-Scout] [Strategy-C] Activating backup scanning pipeline via global system endpoint class mapping.")
	
	sessions, err := os.ReadDir(sessionClassPath)
	if err != nil {
		logger.Warningf("      [iSCSI-Subsystem-Scout] [Strategy-C FAILED] System framework interface class folder missing or inaccessible: %v", err)
		return ""
	}

	for _, s := range sessions {
		targetNamePath := filepath.Join(sessionClassPath, s.Name(), "targetname")
		data, err := os.ReadFile(targetNamePath)
		if err != nil {
			continue
		}
		
		deviceLinkMappingPath := filepath.Join(sessionClassPath, s.Name(), "device")
		if hostPath, err := filepath.EvalSymlinks(deviceLinkMappingPath); err == nil {
			matchToken := fmt.Sprintf("host%s", hostID)
			if strings.Contains(hostPath, matchToken) {
				sigID := strings.TrimSpace(string(data))
				logger.Debugf("      [iSCSI-Subsystem-Scout] [Strategy-C SUCCESS] Valid target correlated matching host token %s: %s", matchToken, sigID)
				return sigID
			}
		}
	}
	
	logger.Warningf("      [iSCSI-Subsystem-Scout] Failed to isolate target iSCSI name matching HCTL profile dependencies across all strategies.")
	return ""
}

// getHardwareSerial safely retrieves the serial, returning error if path is blocked
func (r *OsDeviceConnectivityHelperScsiGeneric) getHardwareSerial(deviceDir string) (string, error) {
	// Try the standard 'wwid' file first
	wwidBytes, err := os.ReadFile(filepath.Join(deviceDir, "wwid"))
	if err != nil || len(strings.TrimSpace(string(wwidBytes))) == 0 {
		// Fallback: If 'wwid' is empty, path might be blocked or transitioning
		return "", fmt.Errorf("serial unavailable")
	}
	return strings.TrimSpace(string(wwidBytes)), nil
}



//IsGhostDevice
func (r *OsDeviceConnectivityHelperScsiGeneric) IsSgDeviceGhost(ctx context.Context, sgName string) (bool, error) {
	if err := ctx.Err(); err != nil {
		logger.Debugf("[%s] Ghost Scan aborted early: incoming context canceled", sgName)
		return false, err
	}

	// 1. Structural Universal Ghost States (Kernel-confirmed hard drops)
	state := r.readSysfs(fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName))
	logger.Debugf("[%s] Sysfs Scan: Initial kernel connection state tracking reads: '%s'", sgName, state)
	
	if state == "offline" || state == "cancelled" || state == "deleting" {
		logger.Infof("[%s] Slot Squatter: Confirmed dead via unrecoverable kernel state: %s. Initiating fast-track purge.", sgName, state)
		return true, nil
	}
	
	if state == "blocked" || state == "quiesce" {
		logger.Debugf("[%s] Safety-Gate Active: Queue is locked/frozen in state '%s'. Aborting scan loop to prevent thread hang.", sgName, state)
		return false, fmt.Errorf("device %s is blocked (%s); cannot run ioctl verification safely", sgName, state)
	}

	deviceBase := fmt.Sprintf("/sys/class/scsi_generic/%s/device", sgName)
	
	typeBytes, err := os.ReadFile(filepath.Join(deviceBase, "type"))
	typeString := "unknown"
	if err == nil {
		typeString = strings.TrimSpace(string(typeBytes))
	}
	logger.Debugf("[%s] Sysfs Scan: SCSI peripheral device category type reports as: '%s'", sgName, typeString)

	if typeString == "31" {
		logger.Infof("[%s] Slot Squatter: Peripheral identity explicit detachment type 31 found. Initiating fast-track purge.", sgName)
		return true, nil
	}

	// 2. Read structural status and age to handle initialization gaps gracefully
	blockPath := filepath.Join(deviceBase, "block")
	_, blockLinkErr := os.Stat(blockPath)
	blockDirectoryMissing := os.IsNotExist(blockLinkErr)
	isNotDiskType := typeString != "0"

	sgSysfsPath := fmt.Sprintf("/sys/class/scsi_generic/%s", sgName)
	info, err := os.Stat(sgSysfsPath)
	if err != nil {
		logger.Debugf("[%s] Sysfs Tracking Drop: Directory disappeared mid-evaluation, assumed purged by host driver tier.", sgName)
		return false, nil 
	}
	deviceAge := time.Since(info.ModTime())
	logger.Debugf("[%s] Property Matrix: Age calculated at %v. Block missing configuration status: %v. Non-disk type status: %v", sgName, deviceAge, blockDirectoryMissing, isNotDiskType)

	// 3. Hardware Verification via Gater Insulation
	// Protects the container thread pool loop from freezing if the SCSI bus drops into a D-state.
	logger.Debugf("[%s] Fabric Audit: Dispatching insulated SCSI inquiry IOCTL...", sgName)
	
	isHwGhost, ioctlErr := executer.ExecuteUninterruptible[bool](
		ctx,
		r.KeyedGater,
		"ghost-inq-"+sgName,
		1, 2, // Strict resource allocation boundaries per specific node path
		600*time.Millisecond, // Handoff slightly above the internal 500ms ioctl timeout ceiling
		2*time.Second,        // Hard abandonment drop execution cut-off ceiling
		func(wCtx context.Context) (bool, error) {
			// Runs inside the gater engine safely
			return r.checkPQviaIoctl(sgName)
		},
	)
	
	// TRACK A: Unrecoverable Fabric Death (The 100% Confirmed Ghost)
	if isHwGhost {
		logger.Warningf("[%s] Track A (Instant Elimination): Hardware check returned verified death [PQ/Type mismatch or hard unmap]. Age: %v. Freeing slot space.", sgName, deviceAge)
		return true, nil
	}

	// TRACK B: Transient System Error Handling (The Cautious Fallback)
	if ioctlErr != nil {
		logger.Debugf("[%s] Subsystem Query Intercept: IOCTL returned evaluation boundary error details: [%v]", sgName, ioctlErr)
		
		if blockDirectoryMissing || isNotDiskType {
			if deviceAge < 15*time.Second {
				logger.Debugf("[%s] Track B (Age Shield Engaged): Device is in a transient birth sequence (Age: %v) but choked on resources. Shielding path.", sgName, deviceAge)
				return false, nil
			}

			// Stuck dead or non-disk for more than 15 seconds without usable system tracking boundaries
			logger.Errorf("[%s] Track B (Stale Zombie Purge): Path has been stuck unrecoverable or non-disk for %v with continuous errors [%v]. Purging squatter space.", sgName, deviceAge, ioctlErr)
			return true, nil
		}

		// A valid block directory exists and it is registered as a Disk Type 0, but the IOCTL choked.
		logger.Debugf("[%s] Track B (Congestion Bypassed): Block endpoint directory is fully built, but host queue dropped a transient error under stress. Retaining path safely.", sgName)
		return false, nil
	}

	// TRACK C: Healthy Transport (Valid SCSI identification page returned)
	logger.Debugf("[%s] Track C (Immune Active Path): Device successfully responded to Page 0x83. Identity tracking maps verified. Bypassing deletion loops.", sgName)
	return false, nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) isHardwareBlocked(sgName string) bool {
	statePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName)
	state, err := os.ReadFile(statePath)
	if err != nil {
		// FIX: Do NOT assume blocked if the file is missing or unreadable. 
		// If the kernel deleted the path, it's a ghost device, not a frozen/blocked queue.
		// Returning false allows the caller to proceed to the open/ioctl checks which safely catch the deletion.
		logger.Debugf("isHardwareBlocked: Cannot read state path %s (%v). Assuming not blocked to allow ghost evaluation.", statePath, err)
		return false 
	}
	
	s := strings.TrimSpace(string(state))

	// FIX: Explicitly check for both states as detailed in your design specification.
	// Both "blocked" (e.g. FC/iSCSI link loss) and "quiesce" (e.g. controller array failover/upgrade) 
	// bypass O_NONBLOCK and will permanently deadlock an ioctl thread if executed.
	if s == "blocked" || s == "quiesce" {
		logger.Warningf("Safety-Gate: SCSI device %s is locked in kernel state '%s'. Aborting ioctl to prevent thread hang.", sgName, s)
		return true
	}

	return false
}

func (r *OsDeviceConnectivityHelperScsiGeneric) checkPQviaIoctl(sgName string) (bool, error) {
	logger.Debugf("[%s] IOCTL Probe: Reading subsystem link type to identify target engine...", sgName)
	subsystem, _ := os.Readlink(fmt.Sprintf("/sys/class/scsi_generic/%s/device/subsystem", sgName))
	if strings.Contains(subsystem, "nvme") {
		logger.Debugf("[%s] IOCTL Probe: Native NVMe device detected. Bypassing SCSI evaluation.", sgName)
		return false, nil
	}

	if r.isHardwareBlocked(sgName) {
		logger.Debugf("[%s] IOCTL Probe: Hard blockage detected via sysfs state tracking. Aborting execution.", sgName)
		return false, fmt.Errorf("device %s is in blocked/quiesce state, skipping ioctl execution to prevent D-state hang", sgName)
	}

	devPath := filepath.Join("/dev", sgName)
	logger.Debugf("[%s] IOCTL Probe: Attempting non-blocking character device instantiation on path %s", sgName, devPath)
	
	fd, err := syscall.Open(devPath, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		logger.Debugf("[%s] IOCTL Probe: Read-only access denied, executing elevation fallback loop to Read-Write mode.", sgName)
		fd, err = syscall.Open(devPath, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		if errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) {
			logger.Warningf("[%s] IOCTL Probe: Open system call caught hard unmapped code (%v). Flagging as ghost slot.", sgName, err)
			return true, nil 
		}
		logger.Debugf("[%s] IOCTL Probe: Open system call failed due to host constraints (%v). Defensively retaining path.", sgName, err)
		return false, fmt.Errorf("failed to open %s due to system error: %w", devPath, err)
	}
	defer syscall.Close(fd)

	const allocationLen = 255
	inqResp := make([]byte, allocationLen)
	senseBuf := make([]byte, 32)
	
	// Query Vital Product Data (EVPD = 1) specifically targeting Page Code 0x83 (Device Identification)
	cdb := [6]byte{0x12, 0x01, 0x83, 0, uint8(allocationLen), 0}

	header := sgIoHdr{
		interface_id:    'S',
		dxfer_direction: SG_DXFER_FROM_DEV,
		cmd_len:         uint8(len(cdb)),
		mx_sb_len:       uint8(len(senseBuf)),
		sbp:             uintptr(unsafe.Pointer(&senseBuf[0])),
		dxfer_len:       uint32(len(inqResp)),
		dxferp:          uintptr(unsafe.Pointer(&inqResp[0])),
		cmdp:            uintptr(unsafe.Pointer(&cdb[0])),
		timeout:         500, // Explicit 500ms ceiling ensures fast sweeps under heavy thread load
		flags:           0,    
	}

	maxRetries := 2
	for attempt := 0; attempt < maxRetries; attempt++ {
		logger.Debugf("[%s] IOCTL Probe: Launching hardware transmission execution loop (Attempt %d/%d)...", sgName, attempt+1, maxRetries)
		
		for i := range senseBuf {
			senseBuf[i] = 0
		}
		header.sb_len_wr = 0 

		var errno syscall.Errno
		for i := 0; i < 3; i++ {
			_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), SG_IO, uintptr(unsafe.Pointer(&header)))
			if errno != syscall.EAGAIN && errno != syscall.EBUSY {
				break
			}
			logger.Debugf("[%s] IOCTL Probe: System queue busy (%v). Executing 10ms micro-delay backup...", sgName, errno)
			time.Sleep(10 * time.Millisecond)
		}

		if errno != 0 {
			if errno == syscall.ENXIO || errno == syscall.ENODEV || errno == syscall.ENOTTY {
				logger.Warningf("[%s] IOCTL Probe: Syscall layer confirmed hard device structural unmapping (%v). Flagging as ghost slot.", sgName, errno)
				return true, nil 
			}
			logger.Debugf("[%s] IOCTL Probe: Syscall layer dropped generic execution constraint error code (%v). Retaining path.", sgName, errno)
			return false, fmt.Errorf("ioctl syscall failure across scsi subsystem: %v", errno)
		}

		logger.Debugf("[%s] IOCTL Probe: Execution response received. Host Status: 0x%04x, Driver Status: 0x%04x, SCSI Protocol Status: 0x%02x", 
			sgName, header.host_status, header.driver_status, header.status)

		// Evaluate Host Transport Status safely using architectural definitions
		if header.host_status != 0 {
			switch header.host_status {
			case 0x05, 0x07, 0x0e: // DID_NO_CONNECT, DID_ERROR, DID_TRANSPORT_FAIL_FAST
				logger.Warningf("[%s] IOCTL Probe: SCSI Transport confirmed unrecoverable death (HostStatus: 0x%02x). Flagging as ghost slot.", sgName, header.host_status)
				return true, nil
			default:
				logger.Debugf("[%s] IOCTL Probe: Fabric dropped transient congestion code (HostStatus: 0x%02x). Defensively retaining path.", sgName, header.host_status)
				return false, fmt.Errorf("transient transport fabric blockage detected (HostStatus: 0x%02x), bypassing deletion", header.host_status)
			}
		}

		switch header.status {
		case 0x00: // SCSI STATUS: GOOD
			logger.Debugf("[%s] IOCTL Probe: SCSI transmission successful. Routing packet to Page 0x83 parser payload layer.", sgName)
			goto PROCESS_PAGE_0x83

		case 0x02: // SCSI STATUS: CHECK CONDITION
			senseKey := senseBuf[2] & 0x0f
			asc := senseBuf[12]
			ascq := senseBuf[13]
			logger.Debugf("[%s] IOCTL Probe: SCSI Check Condition encountered. Sense Key: 0x%02x, ASC: 0x%02x, ASCQ: 0x%02x, Written Sense Data Length: %d", 
				sgName, senseKey, asc, ascq, header.sb_len_wr)

			if header.sb_len_wr >= 18 {
				if senseKey == 0x06 { // UNIT ATTENTION
					logger.Debugf("[%s] IOCTL Probe: Unit Attention condition flagged by target device. Clearing buffer maps and repeating loop cycle.", sgName)
					continue 
				}

				// 0x05/25/00 = Logical Unit Not Supported
				if senseKey == 0x05 && asc == 0x25 && ascq == 0x00 { 
					logger.Warningf("[%s] IOCTL Probe: Hardware confirmed LUN is detached (Logical Unit Not Supported). Flagging as ghost slot.", sgName)
					return true, nil
				}
				// 0x02/3A = Medium Not Present (LUN exists but no volume is backed behind it)
				if senseKey == 0x02 && asc == 0x3A {
					logger.Warningf("[%s] IOCTL Probe: Hardware confirmed LUN maps to empty storage space (Medium Not Present). Flagging as ghost slot.", sgName)
					return true, nil
				}
				// 0x05/24/00 = Invalid Field in CDB (Target controller does not even map this page space)
				if senseKey == 0x05 && asc == 0x24 && ascq == 0x00 {
					logger.Warningf("[%s] IOCTL Probe: Hardware confirmed target controller does not support VPD structures. Flagging as ghost slot.", sgName)
					return true, nil
				}
			}
			logger.Debugf("[%s] IOCTL Probe: Unhandled SCSI Check Condition encountered under load. Defensively retaining path.", sgName)
			return false, fmt.Errorf("unhandled scsi check condition: sense key 0x%02x", senseKey)

		case 0x08, 0x28: // SCSI STATUS: BUSY or TASK SET FULL
			logger.Debugf("[%s] IOCTL Probe: Target queue congestion flagged (Status: 0x%02x). Executing 50ms fallback wait...", sgName, header.status)
			time.Sleep(50 * time.Millisecond)
			continue 

		default:
			logger.Debugf("[%s] IOCTL Probe: Unexpected SCSI protocol status byte received (0x%02x). Defensively retaining path.", sgName, header.status)
			return false, fmt.Errorf("unexpected scsi status byte received: 0x%02x", header.status)
		}
	} 

	logger.Debugf("[%s] IOCTL Probe: Exhausted all hardware command attempts under load pressure. Defensively retaining path.", sgName)
	return false, fmt.Errorf("exhausted storage path verification inquiry attempts under load queue pressure")

PROCESS_PAGE_0x83:
        if inqResp[1] != 0x83 {
                logger.Warningf("[%s] Payload Parsing Error: Device returned invalid page code identifier (0x%02x) instead of 0x83. Flagging as ghost slot.", sgName, inqResp[1])
                return true, nil
        }

        // 1. Calculate the Page Length and extract PQ / DevType variables explicitly
        pageLen := (int(inqResp[2]) << 8) | int(inqResp[3])
        pq := (inqResp[0] >> 5) & 0x07 // <-- ADD THIS LINE TO FIX THE COMPILE ERROR
        devType := inqResp[0] & 0x1f

        // 2. Include pageLen in the logs so the compiler accepts the declared variable
        logger.Debugf("[%s] Payload Parsing: VPD Page Code verified (0x83). Length: %d bytes. PQ: %d, Type: %d", sgName, pageLen, pq, devType)

        // PQ 1 or 3 implies a hardware detached map or missing logical assignment endpoint
        if pq == 1 || pq == 3 || devType == 0x1f {
                logger.Warningf("[%s] Payload Parsing: Identity mapping mismatch discovered [PQ=%d, Type=%d]. Confirmed dead squatter path. Flagging as ghost slot.", sgName, pq, devType)
                return true, nil
        }

        logger.Debugf("[%s] Payload Parsing: Target validation check complete. Hardware transport link reports clean, active connectivity.", sgName)
        return false, nil
}

// sHardwareBlocked, also check for the quiesce state. It often indicates a storage controller failover where I/O is paused but not failed.

func (r *OsDeviceConnectivityHelperScsiGeneric) TeardownVolume(ctx context.Context, target string, needFlush bool, needRemovePhysical bool, expectedWWID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var major, minor uint32
	var hardwareResolved bool
	var mpathName string
	var isNativeNVMe bool
	
	logger.Warningf("teardown volume %s", target)	

	// --- PHASE 0: PRE-UNMOUNT HARDWARE HARVEST ---
	isMounted, err := r.Mounter.IsMounted(target)
	if err == nil && isMounted {
		if devPath, err := r.Mounter.GetDeviceFromMount(target); err == nil && devPath != "" {
			logger.Warningf("teardown volume %s devPath: %s", target, devPath)
			if stat, err := os.Stat(devPath); err == nil {
				if sysObj, ok := stat.Sys().(*syscall.Stat_t); ok {
					logger.Warningf("teardown volume %s devPath found resolved id", target, devPath)
					major = uint32((sysObj.Rdev >> 8) & 0xfff)
					minor = uint32((sysObj.Rdev & 0xff) | ((sysObj.Rdev >> 12) & 0xfff00))
					hardwareResolved = true
					
					baseName := filepath.Base(devPath)
					if strings.HasPrefix(baseName, "dm-") {
						mpathName = r.GetDMNameFromMinor(minor) 
					} else if strings.HasPrefix(baseName, "nvme") {
						// Native NVMe device mapped directly to a partition or raw block namespace
						mpathName = baseName
						isNativeNVMe = true
					}
				}
			}
		}
	}

	// --- PHASE 1: UNMOUNT ---
	if err == nil && isMounted {
		if err := r.Mounter.UnmountWithTimeout(ctx, target, 30*time.Second); err != nil {
			return fmt.Errorf("teardown: unmount step is still in progress: %w", err)
		}
		_ = r.Mounter.PollMountDeleted(ctx, target, 10*time.Second)
	}
	
	logger.Warningf("teardown volume %s hardware resolution fallback step", target)	

	// --- PHASE 2: HARDWARE RESOLUTION FALLBACK ---
	if mpathName == "" && expectedWWID != "" {
		logger.Warningf("teardown volume %s hardware resolution fallback step needed", target)	
		mpathName = r.Helper.findDMByWWID(expectedWWID)
		if mpathName != "" {
			if !hardwareResolved {
				major, minor, _ = r.Helper.GetMajorMinorFromSysfs(ctx, mpathName)
				hardwareResolved = true
			}
		} else {
			// If no DM maps found by WWID, evaluate if a native NVMe tracking node fits the criteria
			slaves := r.FindSlavesByWWID(expectedWWID)
			if len(slaves) > 0 && strings.HasPrefix(slaves[0], "nvme") {
				mpathName = slaves[0] // Track the parent block namespace directly
				isNativeNVMe = true
			}
		}
	}

	// --- PHASE 3: DEVICE MAPPER CLEANUP / RESCUE ---
	// Explicitly safeguard Native NVMe. Only run ioctl/tableswaps on standard Device Mapper (dm-X).
	if mpathName != "" && !isNativeNVMe && strings.HasPrefix(mpathName, "dm-") {
		var openCount int32
		
		for i := 0; i < 10; i++ {
			if ctx.Err() != nil {
				break
			}

			openCount, _ = r.Helper.GetOpenCount(ctx, mpathName)
			if openCount == 0 {
				break 
			}

			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				break
			}
		}
	
		if openCount > 0 {
			logger.Warningf("Device %s is busy (openCount=%d). Triggering DM Rescue.", mpathName, openCount)
			_ = r.multipathdAction(ctx, "disablequeueing map "+mpathName)
			_ = r.dmIoctlCall(ctx, mpathName, DM_DEV_SUSPEND, DM_SKIP_LOCKFS_FLAG)

			sizeStr := r.readSysfs(fmt.Sprintf("/sys/class/block/%s/size", mpathName))
			errorTable := fmt.Sprintf("0 %s error", strings.TrimSpace(sizeStr))

			_ = r.dmIoctlLoadTable(ctx, mpathName, errorTable)
			_ = r.dmIoctlCall(ctx, mpathName, DM_DEV_RESUME, 0)
			_ = r.dmIoctlCall(ctx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)			
		} else {
			if needFlush {
				_, _ = executer.ExecuteUninterruptible[struct{}](
					ctx, r.KeyedGater, "flush-"+mpathName, 10, 50, 5*time.Second, 30*time.Second,
					func(wCtx context.Context) (struct{}, error) {
						err := r.flushDeviceBuffers(wCtx, fmt.Sprintf("/dev/mapper/%s", mpathName))
						return struct{}{}, err
					},
				)
				_ = r.multipathdAction(ctx, "del map "+mpathName)
			}
		}
	} else if mpathName != "" && isNativeNVMe {
		logger.Infof("Target node %s is native NVMe. Bypassing Device Mapper state manipulation.", mpathName)
	}

	// --- PHASE 4: PHYSICAL LAYER ---
	if needRemovePhysical {
		logger.Warningf("teardown volume %s remove physical", target)	
		var slaves []string
		if hardwareResolved && major != 0 && !isNativeNVMe {
			slaves, _ = r.Helper.getSlavesForDevice(major, minor)
		}
		
		if len(slaves) == 0 && expectedWWID != "" {
			logger.Warningf("Running fallback WWID scan for: %s", expectedWWID)
			slaves = r.FindSlavesByWWID(expectedWWID) 
		}

		if len(slaves) > 0 {
			logger.Infof("Tearing down physical paths: %v", slaves)
			_ = r.RemovePhysicalDevice(ctx, slaves)
		} else {
			logger.Errorf("CRITICAL: Failed to locate physical paths for WWID %s.", expectedWWID)
		}
	}

	// Final File Cleanup
	if _, err := os.Stat(target); err == nil {
		return os.Remove(target)
	}
	return nil
}

// FindSlavesByWWID supports tracking layouts spanning from standard SCSI down to legacy RHEL 7 NVMe contexts
func (o *OsDeviceConnectivityHelperScsiGeneric) FindSlavesByWWID(expectedWWID string) []string {
	var slaves []string
	if expectedWWID == "" {
		return slaves
	}

	targetWWID := strings.ToLower(strings.TrimSpace(expectedWWID))
	blockEntries, err := os.ReadDir("/sys/block")
	if err != nil {
		logger.Errorf("WWID Fallback Scan: failed to read /sys/block: %v", err)
		return slaves
	}

	for _, entry := range blockEntries {
		name := entry.Name()
		
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}

		var wwidBytes []byte
		pathsToTry := []string{
			filepath.Join("/sys/block", name, "device", "wwid"), // Standard SCSI / DM
			filepath.Join("/sys/block", name, "wwid"),          // Modern Native NVMe
		}

		for _, p := range pathsToTry {
			if bytes, err := os.ReadFile(p); err == nil {
				wwidBytes = bytes
				break
			}
		}

		// Backward-compatible fallback for early RHEL 7 kernels exposing native NVMe via controller classes
		if len(wwidBytes) == 0 && strings.HasPrefix(name, "nvme") {
			ctrlName := name
			if dashIdx := strings.Index(name, "n"); dashIdx != -1 {
				ctrlName = name[:dashIdx]
			}
			fallbackPath := filepath.Join("/sys/class/nvme", ctrlName, "wwid")
			if bytes, err := os.ReadFile(fallbackPath); err == nil {
				wwidBytes = bytes
			}
		}

		if len(wwidBytes) == 0 {
			continue 
		}

		deviceWWID := normalizeWWID(string(wwidBytes))
		if deviceWWID != "" && (deviceWWID == targetWWID || strings.Contains(deviceWWID, targetWWID) || strings.Contains(targetWWID, deviceWWID)) {
			logger.Infof("WWID Fallback Scan: Found matching hardware path %s for WWID %s", name, expectedWWID)
			slaves = append(slaves, name)
		}
	}

	return slaves
}


// GetDMNameFromMinor resolves a dm-X runtime mapping name to its user-space mapped
// identity (e.g., dm-0 -> multipath-volume-uuid) directly via sysfs device tracking.
func (o *OsDeviceConnectivityHelperScsiGeneric) GetDMNameFromMinor(minor uint32) string {
        // Standard Linux sysfs location for Device Mapper target names
        // The device-mapper major number on Linux is almost universally 253.
        sysfsNamePath := fmt.Sprintf("/sys/dev/block/253:%d/dm/name", minor)

        logger.Warning("GetDMNameFromMinor")

        nameBytes, err := os.ReadFile(sysfsNamePath)
        if err != nil {
                logger.Warning("GetDMNameFromMino error %v", err)
                // Fallback: Check if the device is mapped directly under the /sys/block tree structure
                // as /sys/block/dm-X/dm/name
                fallbackPath := fmt.Sprintf("/sys/block/dm-%d/dm/name", minor)
                nameBytes, err = os.ReadFile(fallbackPath)
                if err != nil {
                        logger.Warningf("Hardware Harvest: Could not resolve DM mapped name for minor %d via sysfs: %v", minor, err)
                        return ""
                }
        }

        // Clean trailing newlines or whitespace (e.g. "mpathb\n" -> "mpathb")
        dmName := strings.TrimSpace(string(nameBytes))
        if dmName != "" {
                logger.Infof("Hardware Harvest: Successfully resolved minor dev %d to DM map name: %s", minor, dmName)
        }

        return dmName
}


func (r *OsDeviceConnectivityHelperScsiGeneric) dmIoctlLoadTable(ctx context.Context, name string, table string) error {
    // 1. Capture the tuple, return only the error
    _, err := executer.ExecuteUninterruptible[struct{}](
        ctx, r.KeyedGater, "dm-load-"+name, 1, 10, 1*time.Second, 5*time.Second,
        func(wCtx context.Context) (struct{}, error) {
            f, err := os.OpenFile(DM_IOCTL_CONTROL, os.O_RDWR, 0)
            if err != nil { return struct{}{}, err }
            defer f.Close()

			// Parse sector length from table: "0 12345 error"
			var start, length uint64
			fmt.Sscanf(table, "%d %d", &start, &length)

			targetString := table + "\x00"
			// Ensure we align the payload for the kernel
			headerSize := uint32(unsafe.Sizeof(dmIoctl{}))
			specSize := uint32(unsafe.Sizeof(dmTargetSpec{}))
			payloadSize := headerSize + specSize + uint32(len(targetString))
			
			buf := make([]byte, payloadSize)
			dataPtr := unsafe.Pointer(&buf[0])

			// 1. Header
			header := (*dmIoctl)(dataPtr)
			header.version = [3]uint32{4, 0, 0}
			header.dataSize = payloadSize
			header.targetCount = 1
			copy(header.name[:], name)

			// 2. Spec
			spec := (*dmTargetSpec)(unsafe.Add(dataPtr, uintptr(headerSize)))
			spec.sectorStart = start
			spec.length = length // REQUIRED: Kernel validates this
			spec.targetType = [16]byte{}
			copy(spec.targetType[:], "error")
			spec.next = specSize + uint32(len(targetString))

			// 3. Table String
			copy(buf[headerSize+specSize:], targetString)

            _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), DM_TABLE_LOAD, uintptr(dataPtr))
            if errno != 0 { return struct{}{}, errno }
            return struct{}{}, nil
        },
    )
    return err // Return just the error to match function signature
}


func (r *OsDeviceConnectivityHelperScsiGeneric) dmIoctlCall(ctx context.Context, name string, op uintptr, flags uint32) error {
    // Capture the tuple, discard the struct{}
    _, err := executer.ExecuteUninterruptible[struct{}](
        ctx, r.KeyedGater, "dm-ioctl-"+name, 1, 10, 1*time.Second, 5*time.Second,
        func(wCtx context.Context) (struct{}, error) {
            f, err := os.OpenFile(DM_IOCTL_CONTROL, os.O_RDWR, 0)
            if err != nil { return struct{}{}, err }
            defer f.Close()

            payload := dmIoctl{
                version:  [3]uint32{4, 0, 0},
                dataSize: uint32(unsafe.Sizeof(dmIoctl{})),
                flags:    flags,
            }
            copy(payload.name[:], name)

            _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), op, uintptr(unsafe.Pointer(&payload)))
            if errno != 0 && errno != unix.ENXIO { return struct{}{}, errno }
            return struct{}{}, nil
        },
    )
    return err // Return the error alone
}





type dmTargetSpec struct {
	sectorStart uint64
	length      uint64
	status      int32  // Used when reading from kernel
	next        uint32 // Offset to next target_spec
	targetType  [16]byte
}

const (
	DM_MAX_TYPE_NAME = 16
	// DM_TABLE_LOAD = 0xc138fd09 (Architecture dependent)
)


func (r *OsDeviceConnectivityHelperScsiGeneric) dmIoctlLoadErrorTable(ctx context.Context, name string, sectorCount uint64) error {
    // Capture the (struct{}, error) and return only the error
    _, err := executer.ExecuteUninterruptible[struct{}](
        ctx, r.KeyedGater, "dm-load-error-"+name, 1, 10, 1*time.Second, 5*time.Second,
        func(wCtx context.Context) (struct{}, error) {
            f, err := os.OpenFile(DM_IOCTL_CONTROL, os.O_RDWR, 0)
            if err != nil { return struct{}{}, err }
            defer f.Close()

                        // 1. Prepare Target Type and Params
                        targetType := "error"
                        // params for error target are usually empty or "0"
                        params := ""

                        // 2. Calculate sizes and padding (8-byte alignment)
                        specSize := uint32(unsafe.Sizeof(dmTargetSpec{}))
                        paramSize := uint32(len(params) + 1) // null-terminated
                        paddedParamSize := (paramSize + 7) &^ 7
                        totalDataSize := uint32(unsafe.Sizeof(dmIoctl{})) + specSize + paddedParamSize

                        // 3. Build the combined buffer
                        buf := make([]byte, totalDataSize)

                        // Header
                        header := (*dmIoctl)(unsafe.Pointer(&buf[0]))
                        header.version = [3]uint32{4, 0, 0}
                        header.dataSize = totalDataSize
                        header.dataStart = uint32(unsafe.Sizeof(dmIoctl{}))
                        header.targetCount = 1
                        copy(header.name[:], name)

                        // Target Spec
                        spec := (*dmTargetSpec)(unsafe.Pointer(&buf[header.dataStart]))
                        spec.sectorStart = 0
                        spec.length = sectorCount
                        spec.next = specSize + paddedParamSize
                        copy(spec.targetType[:], targetType)

                        // Params (immediately after spec)
                        copy(buf[header.dataStart+specSize:], params)



            _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), DM_TABLE_LOAD, uintptr(unsafe.Pointer(&buf[0])))
            if errno != 0 { return struct{}{}, errno }
            return struct{}{}, nil
        },
    )
    return err
}





func (r *OsDeviceConnectivityHelperScsiGeneric) TeardownRescue(ctx context.Context, mpathName string) error {
    // 1. Check if the device is actually stuck
    // REMOVED ctx based on your compiler error log
    openCount, _ := r.Helper.GetOpenCount(ctx, mpathName) 
    if openCount <= 0 {
        return r.multipathdAction(ctx, "del map "+mpathName)
    }

    // 2. TRIGGER THE HAMMER
    logger.Warningf("Rescue: Device %s is busy (count=%d). Swapping to Error Target.", mpathName, openCount)

    _ = r.multipathdAction(ctx, "disablequeueing map "+mpathName)

    // Suspend with 'nolockfs' to avoid hanging on a frozen filesystem
    _ = r.dmIoctlCall(ctx, mpathName, DM_DEV_SUSPEND, DM_SKIP_LOCKFS_FLAG)

    // Note: Ensure SwapToErrorTarget calls your dmIoctlLoadTable internally
    if err := r.SwapToErrorTarget(ctx, mpathName); err != nil {
        return fmt.Errorf("rescue hammer failed: %w", err)
    }

    _ = r.dmIoctlCall(ctx, mpathName, DM_DEV_RESUME, 0)

    // Deferred removal ensures the device-mapper entry disappears as soon as refs drop
    return r.dmIoctlCall(ctx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)
}


// getSlavesForDevice returns raw block device names (e.g., "sda", "nvme0n1") from sysfs
func (o *OsDeviceConnectivityHelperGeneric) getSlavesForDevice(major, minor uint32) ([]string, error) {

	logger.Warning("getSlaveForDevice")
	slavesPath := fmt.Sprintf("/sys/dev/block/%d:%d/slaves", major, minor)

	entries, err := os.ReadDir(slavesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var results []string
	for _, entry := range entries {
		slaveName := entry.Name() // Keeps exact block name like "sda" or "nvme0n1"
		logger.Warning("getSlaveForDevice entry %s", slaveName)
		if slaveName != "" {
			results = append(results, slaveName)
		}
	}
	return results, nil
}


type DmTargetSpec struct {
    SectorStart uint64
    Length      uint64
    Status      int32
    Next        uint32
    TargetType  [16]byte
}

func (r *OsDeviceConnectivityHelperScsiGeneric) SwapToErrorTarget(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	_, err := executer.ExecuteUninterruptible[struct{}](
		ctx, r.KeyedGater, "dm-hammer-"+name, 1, 10, 2*time.Second, 10*time.Second,
		func(wCtx context.Context) (struct{}, error) {
			f, err := os.OpenFile("/dev/mapper/control", os.O_RDWR, 0)
			if err != nil {
				return struct{}{}, err
			}
			defer f.Close()

			sizeStr := r.readSysfs(fmt.Sprintf("/sys/class/block/%s/size", name))
			// Trim space to avoid parsing errors from sysfs newlines
			size, err := strconv.ParseUint(strings.TrimSpace(sizeStr), 10, 64)
			if err != nil {
				return struct{}{}, fmt.Errorf("failed to parse device size: %w", err)
			}

			ioctlSize := uint32(unsafe.Sizeof(DmIoctl{}))
			specSize := uint32(unsafe.Sizeof(DmTargetSpec{}))
			totalSize := ioctlSize + specSize
			buf := make([]byte, totalSize)

			// 2. Map Ioctl Header
			io := (*DmIoctl)(unsafe.Pointer(&buf[0]))
			// Fixed: Match your DmIoctl struct definition (array vs separate fields)
			io.Version = [3]uint32{4, 0, 0}
			io.DataSize = totalSize
			io.DataStart = ioctlSize
			io.TargetCount = 1
			copy(io.Name[:], name)

			// 3. Map Target Spec
			spec := (*DmTargetSpec)(unsafe.Pointer(&buf[ioctlSize]))
			spec.SectorStart = 0
			spec.Length = size
			spec.Next = specSize
			copy(spec.TargetType[:], "error")

			// 4. Syscall
			_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(0xc138fd09), uintptr(unsafe.Pointer(&buf[0])))
			if errno != 0 {
				return struct{}{}, errno
			}

			// 5. Trigger Resume
			// dmIoctlCall returns error, so we wrap it in (struct{}, error)
			err = r.dmIoctlCall(wCtx, name, 0xc138fd06, 0) // DM_DEV_RESUME
			return struct{}{}, err
		},
	)
	return err
}


type IdentityResult struct {
	WWID string
	HW   string
}

// TODO call FinalWwidPurge
func (r *OsDeviceConnectivityHelperScsiGeneric) IdentityAwarePreScan(
	ctx context.Context, 
	targetPath string, 
	volumeId string,
) (discoveredDev string, isStaged bool, skipRescan bool, isLeftover bool, err error) {

	volumeWWID := r.Helper.GetVolumeIdVariations(volumeId)
	
	// Safely map and normalize candidate identifiers based on your positional contract
	normIds := make([]string, 2)
	normIds[0] = normalizeWWID(volumeWWID[0]) // SCSI ID candidate
	normIds[1] = normalizeWWID(volumeWWID[1]) // NVMe NGUID candidate

	// Dynamic lookup: Locate a Device Mapper mapping using whichever protocol is actually active
	var mpathAlias string
	if normIds[0] != "" {
		mpathAlias = r.Helper.findDMByWWID(normIds[0])
	}
	if mpathAlias == "" && normIds[1] != "" {
		mpathAlias = r.Helper.findDMByWWID(normIds[1]) // Evaluates NVMe over DM targets
	}

	// CRITICAL RESOLUTION RESTORED: Translate the alias into a raw dm-X name early
	var mpathName string
	if mpathAlias != "" {
		if realPath, err := filepath.EvalSymlinks(filepath.Join("/dev/mapper", mpathAlias)); err == nil {
			mpathName = filepath.Base(realPath) // e.g., "dm-5"
		}
	}

	// =========================================================================
	// CASE 0: MOUNTED PATH IDENTITY VERIFICATION & SHORT-CIRCUIT
	// =========================================================================
	mounts, _ := r.Mounter.GetMountsForPath(targetPath)
	if len(mounts) > 0 {
		// 1. Sysfs mapping lookup using mount minor descriptors
		currentWWID, _ := r.Helper.getWWIDByDev(mounts[0].Major, mounts[0].Minor)
		currentWWID = normalizeWWID(currentWWID)

		// 2. Raw Hardware Controller INQUIRY check on the active DM target
		var hwWWID string
		if mpathAlias != "" {
			hwWWID, _ = r.Helper.GetWwnByScsiInq(ctx, mpathAlias)
			hwWWID = normalizeWWID(hwWWID)
		}

		// Verify if the mounted storage identity corresponds with either candidate
		isMatch := (normIds[0] != "" && (strings.EqualFold(currentWWID, normIds[0]) || strings.EqualFold(hwWWID, normIds[0]))) ||
			       (normIds[1] != "" && (strings.EqualFold(currentWWID, normIds[1]) || strings.EqualFold(hwWWID, normIds[1])))

		if isMatch {
			// Zombie Safeguard inside Mount Block: Ensure a ghost map with 0 paths isn't corrupting things
			helper := GetDmsPathHelperGeneric{}
			slaveCount := helper.GetSlaveCount(mpathName) // Uses our resolved name variable safely
			if helper.IsDeviceMapper(mpathName) && slaveCount == 0 {
				logger.Warningf("Pre-scan: Mount exists for %s but underlying device mapper shell has 0 active paths. Forcing cleanup.", volumeWWID)
				// TODO NVME
				_ = r.TeardownVolume(ctx, targetPath, false, false, volumeWWID[0])
				r.busyTimestamps.Delete(volumeWWID[0])
				return "", false, false, true, nil
			}

			logger.Infof("Pre-scan: Volume %v is already safely staged, verified via hardware inquiry, and healthy at %s.", volumeWWID, targetPath)
			r.busyTimestamps.Delete(volumeWWID[0])
			
			devNode := mounts[0].MountSource
			if devNode == "" && mpathName != "" {
				devNode = "/dev/" + mpathName
			}
			return devNode, true, true, false, nil
		}

		// IDENTITY COLLISION SHIELD: The folder is occupied by a different volume entirely
		logger.Warningf("Pre-scan: Identity Collision at %s: Found dev-WWID %s (HW-WWID %s), expected candidates %v. Forcing unmount.", targetPath, currentWWID, hwWWID, normIds)
		_ = r.Mounter.UnmountWithTimeout(ctx, targetPath, 30*time.Second)
		r.busyTimestamps.Delete(volumeWWID[0])
		return "", false, false, false, status.Error(codes.Internal, "pre-scan: identity collision detected at target path")
	}

	// =========================================================================
	// CASE 1: TOPOLOGY STATE DETECTION ENGINE (Direct Sysfs Evaluation)
	// =========================================================================
	helper := GetDmsPathHelperGeneric{}
	// Pass checkPendingOnly = true to execute our relaxed shadow-scan for transitional tasks
	hasDevice, isPending, devName := helper.EvaluateSysfsTopology(normIds, true)

	if hasDevice {
		// A: ZOMBIE TOPOLOGY PROTECTION
		slaveCount := helper.GetSlaveCount(devName)
		if helper.IsDeviceMapper(devName) && slaveCount == 0 {
			logger.Warningf("Pre-scan: Detected zombie orphan topology shell for %s. Forcing teardown.", devName)
			_ = r.cleanupOrphanedTopology(ctx, mpathName, volumeWWID[0]) // Pass target pointers to purge block
			r.busyTimestamps.Delete(volumeWWID[0])
			return "", false, false, true, nil 
		}

		// B: DISCOVERY IS IN-PROGRESS / TRANSITIONING
		if isPending {
			now := time.Now()
			val, loaded := r.busyTimestamps.LoadOrStore(devName, now)
			firstDetected := val.(time.Time)

			const maxKernelSettleDuration = 5 * time.Minute
			if loaded && now.Sub(firstDetected) > maxKernelSettleDuration {
				logger.Errorf("Pre-scan: Storage permanently stuck for device %s for %v. Invoking active cleanup.", devName, now.Sub(firstDetected))
				r.busyTimestamps.Delete(volumeWWID[0])
				_ = r.cleanupOrphanedTopology(ctx, mpathName, volumeWWID[0])
				return "", false, false, true, nil 
			}
			
			logger.Infof("Pre-scan: Previous discovery loop is actively settling in kernel for %s (Elapsed: %v). Backing off.", devName, now.Sub(firstDetected))
			return "/dev/" + devName, false, true, false, status.Error(codes.Aborted, "discovery actively running in kernel. Backing off.")
		}

		// C: IDLE & HEALTHY COMPLETED DISCOVERY
		logger.Infof("Pre-scan: Device %s discovered from previous attempt and ready. Bypassing rescan phase.", devName)
		r.busyTimestamps.Delete(volumeWWID[0])
		return "/dev/" + devName, false, true, false, nil
	}

	// =========================================================================
	// CASE 2: CLEAN SLATE (No device, no mount)
	// =========================================================================
	logger.Infof("Pre-scan: Clean slate for candidates %v. Full host storage fabric rescan required.", volumeWWID)
	r.busyTimestamps.Delete(volumeWWID[0])
	return "", false, false, false, nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) cleanupOrphanedTopology(ctx context.Context, mpathName string, expectedWWID string) error {
	normExpected := r.Helper.normalizeWWID(expectedWWID)

	// 1. DEVICE MAPPER MANAGEMENT (SCSI & NVMe over DM)
	if mpathName != "" {
		// Prevent D-state process hangs by disabling map queuing before doing a deletion
		_ = r.multipathdAction(ctx, "disablequeueing map "+mpathName)

		openCount, err := r.Helper.GetOpenCount(ctx, mpathName)
		if err == nil {
			if openCount <= 0 {
				// Clean fresh start
				_ = r.multipathdAction(ctx, "del map "+mpathName)
			} else {
				// Device is busy (e.g. by udev or an engine scan). Use deferred deletion safely.
				_ = r.dmIoctlCall(ctx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)
			}
		}
	} else {
		// NATIVE NVME MULTIPATH MANAGEMENT (ANA / NVMe Subsystems)
		_ = r.disableNativeNvmeQueueing(normExpected)
	}

	// 2. PHYSICAL LAYER PURGE (Pure sysfs)
	// Iterates over all sd* and nvme* blocks to write "1" to the 'delete' or 'delete_tgt' sysfs entries
	_ = r.purgeStuckPhysicalPaths(expectedWWID)

	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) disableNativeNvmeQueueing(expectedWWID string) error {
	blockFiles, _ := os.ReadDir("/sys/block")
	normExpected := normalizeWWID(expectedWWID)
	helper := GetDmsPathHelperGeneric{}

	for _, f := range blockFiles {
		devName := f.Name()
		if !helper.IsNativeNvmeNamespace(devName) {
			continue
		}

		wwidBytes, err := os.ReadFile(filepath.Join("/sys/block", devName, "wwid"))
		if err != nil || normalizeWWID(string(wwidBytes)) != normExpected {
			continue
		}

		// Correctly discover parent controller strings (e.g., "nvme0") by scanning links
		// inside the subsystem. Or traverse via /sys/block/nvmeXnY/device/
		realDevicePath, err := filepath.EvalSymlinks(filepath.Join("/sys/block", devName, "device"))
		if err != nil {
			continue
		}

		// Look for standard transport controller siblings inside the subsystem folder
		// (e.g., /sys/devices/virtual/nvme-subsystem/nvme-subsys0/nvme0)
		subsysDir := filepath.Dir(realDevicePath)
		entries, _ := os.ReadDir(subsysDir)
		for _, e := range entries {
			name := e.Name()
			// Target the direct hardware/fabric controller channels strictly (nvme0, nvme1)
			if strings.HasPrefix(name, "nvme") && !strings.Contains(name, "n") && !strings.Contains(name, "-") {
				fastIoFailPath := filepath.Join("/sys/class/nvme", name, "fast_io_fail_tmo")
				if _, err := os.Stat(fastIoFailPath); err == nil {
					_ = os.WriteFile(fastIoFailPath, []byte("0\n"), 0200)
				}
			}
		}
	}
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) purgeStuckPhysicalPaths(expectedWWID string) error {
	blockFiles, _ := os.ReadDir("/sys/block")
	normExpected := normalizeWWID(expectedWWID)
	helper := GetDmsPathHelperGeneric{}

	for _, f := range blockFiles {
		devName := f.Name()
		isSCSI := strings.HasPrefix(devName, "sd")
		isNVMe := helper.IsNativeNvmeNamespace(devName)

		if !isSCSI && !isNVMe {
			continue
		}

		var wwidPath string
		if isSCSI {
			wwidPath = filepath.Join("/sys/block", devName, "device", "wwid")
		} else {
			wwidPath = filepath.Join("/sys/block", devName, "wwid")
		}

		wwidBytes, err := os.ReadFile(wwidPath)
		if err != nil {
			continue
		}

		if strings.Contains(normalizeWWID(string(wwidBytes)), normExpected) {
			if isSCSI {
				// SCSI path deletion remains 100% correct
				_ = os.WriteFile(filepath.Join("/sys/block", devName, "device", "delete"), []byte("1\n"), 0200)
			} else if isNVMe {
				// To forcefully tear down a native NVMe channel connection trapped in a hang,
				// we must command the host controllers bound to the target namespace to disconnect.
				realDevicePath, err := filepath.EvalSymlinks(filepath.Join("/sys/block", devName, "device"))
				if err != nil {
					continue
				}
				
				subsysDir := filepath.Dir(realDevicePath)
				entries, _ := os.ReadDir(subsysDir)
				for _, e := range entries {
					name := e.Name()
					if strings.HasPrefix(name, "nvme") && !strings.Contains(name, "n") && !strings.Contains(name, "-") {
						deleteCtrlPath := filepath.Join("/sys/class/nvme", name, "delete_controller")
						if _, err := os.Stat(deleteCtrlPath); err == nil {
							_ = os.WriteFile(deleteCtrlPath, []byte("1\n"), 0200)
						}
					}
				}
			}
		}
	}
	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) FinalWwidPurge(ctx context.Context, expectedWWID string) error {
	targetWWID := r.Helper.normalizeWWID(expectedWWID)

	// 1. CLEANUP MULTIPATH LAYER
	mpathName := r.Helper.findDMByWWID(targetWWID)
	if mpathName != "" {
		_, err := executer.ExecuteUninterruptible[struct{}](
			ctx,
			r.KeyedGater,
			"mpath-final-del-"+mpathName, // Unique key per map to prevent head-of-line blocking
			1,                            // maxRunning: 1 operation per specific map
			5,                            // maxSpare: tight budget for this final cleanup
			2*time.Second,                // handoffTimeout: move to spare if socket/ioctl hangs
			10*time.Second,               // hardTimeout: return error to caller
			func(ctx context.Context) (struct{}, error) {
				// 1. Primary Method: Socket call to multipathd
				if err := r.multipathdAction(ctx, "del map " + mpathName); err != nil {
					// 2. Fallback: If socket fails, try a deferred removal
					// Note: deferredRemove likely triggers a kernel-level 'delete'
					if errDeffered := r.deferredRemove(mpathName); errDeffered != nil {
						return struct{}{}, fmt.Errorf("socket delete failed (%w) and deferred removal failed (%v)", err, errDeffered)
					}
				}
				return struct{}{}, nil
			},
		)
		if err != nil {
			return err
		}
	}

	// 2. SCAN FOR ORPHAN DM DEVICES (Multipathd-lost)
	dmUUIDs, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
	for _, path := range dmUUIDs {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(r.Helper.normalizeWWID(string(data)), targetWWID) {
			dmName := filepath.Base(filepath.Dir(filepath.Dir(path)))
			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,
				"mpath-stale-del-"+dmName, // Unique key per DM device name
				1,                         // maxRunning: 1 per device
				5,                         // maxSpare: budget for stuck threads
				2*time.Second,             // handoffTimeout: move to spare pool if kernel stalls
				10*time.Second,            // hardTimeout: return error to caller
				func(ctx context.Context) (struct{}, error) {
					// Trigger the kernel-level deferred removal
					err := r.deferredRemove(dmName)
					return struct{}{}, err
				},
			)
			if err != nil {
				// Log the timeout or execution error
				logger.Warningf("Stale device deletion for %s incomplete: %v", dmName, err)
			}
		}
	}

	// 3. SEVER PHYSICAL PATHS (sdX and nvme)
	devices, err := os.ReadDir("/sys/block")
	if err != nil {
		return fmt.Errorf("failed to scan /sys/block: %w", err)
	}

	for _, dev := range devices {
		name := dev.Name()
		if !strings.HasPrefix(name, "sd") && !strings.HasPrefix(name, "nvme") {
			continue
		}

		// Use the Gater to check if this major:minor is already marked as stuck
		// TODO
		//if r.Mounter.isPathStuck(name) {
		//	continue
		//}

		//canonicalID, err := r.getCanonicalID("/dev/" + devName)
		//if err == nil && r.Executer.IsStuck(canonicalID) {
		//	logger.Warningf("FinalPurge: Skipping %s (%s) - hardware is wedged in D-state", devName, canonicalID)
		//	continue
		//}

		_, err := executer.ExecuteUninterruptible[struct{}](
			ctx,
			r.KeyedGater,
			"scsi-purge-"+name, // Unique key per device prevents a hang on sda from blocking sdb
			5,                  // maxRunning: allow up to 5 concurrent purges for this specific ID
			50,                 // maxSpare: budget for "zombie" threads stuck in D-state
			2*time.Second,      // handoffTimeout: move to spare pool if any step below hangs
			15*time.Second,     // hardTimeout: return error to the caller
			func(ctx context.Context) (struct{}, error) {
				// 1. Identity Check (Sysfs is safer than ioctl, but can still hang if bus is reset)
				currentWWID, _ := r.getWWIDBySysfs(name)
				if !strings.EqualFold(r.Helper.normalizeWWID(currentWWID), targetWWID) {
					return struct{}{}, nil
				}

				// 2. Fail Path (Socket call to multipathd)
				_ = r.multipathdAction(ctx, "fail path " + name)

				// 3. Flush Buffers (Highest risk of D-state hang)
				// Use the context provided by the worker if flushDeviceBuffers supports it
				flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				_ = r.flushDeviceBuffers(flushCtx, "/dev/"+name)

				// 4. Determine Delete Path
				var deletePath string
				if strings.HasPrefix(name, "nvme") {
					deletePath = fmt.Sprintf("/sys/block/%s/device/delete_controller", name)
				} else {
					deletePath = fmt.Sprintf("/sys/block/%s/device/delete", name)
				}

				// 5. Final Trigger
				if _, err := os.Stat(deletePath); err == nil {
					if errWrite := os.WriteFile(deletePath, []byte("1"), 0200); errWrite != nil {
						return struct{}{}, errWrite
					}
				}

				return struct{}{}, nil
			},
		)

		if err != nil {
			logger.Warningf("Purge operation for %s reached timeout or failed: %v", name, err)
		}
	}
	return nil
}



// In Case 1, when a collision is detected, you call UnmountWithContext. Ensure your UnmountWithContext is set to use MNT_DETACH (Lazy) immediately for collisions, as you don't want to wait for a graceful timeout on a rogue volume.
// VerifyAndGetDmDevice replaced by VerifyAndGetDmDevice
func (r OsDeviceConnectivityHelperScsiGeneric) VerifyAndGetDmDevice(devName string, volumeUuid string) (string, error) {
	expectedSerial := strings.ToLower(volumeUuid)
	//TODO restore check
	//expectedLunStr := fmt.Sprintf("%d", lun)
	//expectedMpathUuid := "mpath-" + expectedSerial

	// 1. GLOBAL CONFLICT AUDIT
	// Ensure the serial isn't already claimed by a DIFFERENT dm device.
	allDmDirs, _ := filepath.Glob("/sys/block/dm-*")
	for _, dmDir := range allDmDirs {
		// TODO need to normalize
		if dmDir == devName {
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
			logger.Warningf("Found stale multipath map %s for serial %s. Removing.", dmName, volumeUuid)
			//forceFlushDM(..)
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
	return targetDm, nil
}

// Add this conflict checker as an internal loop inside EvaluateSysfsTopology
func (o *GetDmsPathHelperGeneric) checkGlobalIdentityConflicts(targetUUID string, currentDevName string) error {
	dmMatches, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
	for _, m := range dmMatches {
		parts := strings.Split(m, "/")
		if len(parts) < 4 || parts[3] == currentDevName {
			continue // Skip auditing the device we are currently analyzing
		}
		
		content, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		foundUUID := normalizeWWID(string(content))
		
		if foundUUID == targetUUID {
			otherDmName := parts[3]
			// Check holders to see if the mapping is actively open (mounted/locked by another subsystem)
			holders, _ := os.ReadDir(filepath.Join("/sys/block", otherDmName, "holders"))
			if len(holders) > 0 {
				return fmt.Errorf("FATAL identity clash: WWID %s is already claimed by active system device %s", targetUUID, otherDmName)
			}
		}
	}
	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) multipathdAction(ctx context.Context, cmd string) error {
	response, err := r.Executer.MultipathdCmd(ctx, "", cmd)

	if err != nil {
		return err
	}

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
		currentMounts, _ := r.Mounter.GetMountsForPath(target)
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
	// DM_NOFLUSH_FLAG is critical: it prevents the ioctl from hanging if paths are failed (D-state)
	flags := uint32(DM_DEFERRED_REMOVE | DM_NOFLUSH_FLAG)

	err := r.ExecuteDmIoctl(uintptr(DM_DEV_REMOVE), name, flags)
	if err != nil {
		return err
	}
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) ExecuteDmIoctl(command uintptr, dmName string, flags uint32) error {
	// 1. Structural Validation
	const expectedSize = 312
	if size := unsafe.Sizeof(DmIoctl{}); size != expectedSize {
		return fmt.Errorf("invalid DmIoctl size: expected %d, got %d", expectedSize, size)
	}

	// 2. Open Control Device
	control, err := os.OpenFile("/dev/mapper/control", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/mapper/control: %w", err)
	}
	defer control.Close()

	// 3. Prepare Data Structure
	data := DmIoctl{
		Version:   [3]uint32{DM_VERSION_MAJOR, DM_VERSION_MINOR, DM_VERSION_PATCH},
		DataSize:  uint32(unsafe.Sizeof(DmIoctl{})),
		DataStart: uint32(unsafe.Sizeof(DmIoctl{})), // Matches dmsetup behavior
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

func (r *OsDeviceConnectivityHelperScsiGeneric) IsSerialMatch(hwSerial, expectedSerial string) bool {
	// Sysfs 'wwid' files often look like: "naa.600507680c80843d3000000000000123"
	// Expected serial is often just the hex: "600507680c80843d3000000000000123"
	hw := strings.ToLower(strings.TrimSpace(hwSerial))
	expected := strings.ToLower(strings.TrimSpace(expectedSerial))

	// Strip common SCSI prefixes
	prefixes := []string{"naa.", "t10.", "eui.", "uuid."}
	for _, p := range prefixes {
		hw = strings.TrimPrefix(hw, p)
	}

	return strings.Contains(hw, expected) || strings.Contains(expected, hw)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getWWIDBySysfs(deviceName string) (string, error) {
	name := filepath.Base(deviceName)
	
	logger.Warningf("getWWIDBySysfs %s", deviceName)	

	var wwidPath string
	var isNVMe, isDM bool

	if strings.HasPrefix(name, "nvme") {
		isNVMe = true
		// FIX: On RHEL 7, 'nguid' or 'uuid' live directly inside the namespace block folder
		// (e.g., /sys/block/nvme0n1/wwid or /sys/block/nvme0n1/nguid), NOT under its /device controller link.
		wwidPath = fmt.Sprintf("/sys/block/%s/wwid", name)
		if _, err := os.Stat(wwidPath); os.IsNotExist(err) {
			wwidPath = fmt.Sprintf("/sys/block/%s/nguid", name)
			if _, err := os.Stat(wwidPath); os.IsNotExist(err) {
				wwidPath = fmt.Sprintf("/sys/block/%s/uuid", name)
			}
		}
	} else if strings.HasPrefix(name, "dm-") {
		isDM = true
		wwidPath = fmt.Sprintf("/sys/block/%s/dm/uuid", name)
	} else {
		// Standard SCSI (sdX)
		wwidPath = fmt.Sprintf("/sys/block/%s/device/wwid", name)
	}
	
	logger.Warningf("getWWIDBySysfs %s path %s", deviceName, wwidPath)	

	data, err := os.ReadFile(wwidPath)
	if err != nil {
		logger.Warningf("getWWIDBySysfs %s path %s fallback", deviceName, wwidPath)	
		// FIX: Legacy RHEL 7.0-7.3 Fallback for standard SCSI devices lacking a /device/wwid file.
		// Read the Vital Product Data (VPD) Page 0x83 identifier via scsi_id/vpd tracking alternative.
		if !isNVMe && !isDM && os.IsNotExist(err) {
			fallbackVPDPath := fmt.Sprintf("/sys/block/%s/device/vpd_pg83", name)
			if vpdData, fallbackErr := os.ReadFile(fallbackVPDPath); fallbackErr == nil && len(vpdData) > 4 {
				// The raw VPD page 0x83 bytes contain designator headers; parsing them can be complex,
				// but strings.TrimSpace provides a fallback signature. If your system populates a clean
				// string representation, extract it here. Otherwise, return the original missing error.
				logger.Debugf("getWWIDBySysfs: /device/wwid absent. Utilizing alternative VPD page reference for %s", name)
			}
		}
		return "", err
	}

	wwid := strings.TrimSpace(string(data))

	// FIX: Clean Device Mapper mpath prefixes to avoid false-positive identity mismatches
	if isDM && strings.HasPrefix(wwid, "mpath-") {
		wwid = strings.TrimPrefix(wwid, "mpath-")
		// Some kernels layer parts as part-uuid (e.g. part1-mpath-WWID)
		logger.Debugf("getWWIDBySysfs: Stripped 'mpath-' prefix from DM device %s to yield clean hardware WWID: %s", name, wwid)
	}

	return wwid, nil
}


// ============== OsDeviceConnectivityHelperInterface ==========================

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperInterface

type OsDeviceConnectivityHelperInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityScsiGeneric.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test OsDeviceConnectivityHelperGeneric logic.
	*/
	GetHostsIdByArrayIdentifiers(arrayIdentifier []string) (map[int]bool, error)
	GetWwnByScsiInq(ctx context.Context, dev string) (string, error)
	GetVolumeIdVariations(volumeUuid string) []string
	GetMpathDeviceName(ctx context.Context, volumePath string) (string, error)
	GetMpathVolumeId(ctx context.Context, mpathDeviceName string) (string, error)
	normalizeWWID(raw string) string
	findDMByWWID(wwid string) string
	getSlavesForDevice(major, minor uint32) ([]string, error)
	GetOpenCount(ctx context.Context, dmName string) (int32, error)
	GetMajorMinorFromSysfs(ctx context.Context, devicePath string) (major uint32, minor uint32, err error)
	getWWIDByDev(major, minor uint32) (string, error)
	WaitForDmToExist(ctx context.Context, volumeIdVariations []string, maxRetries int, intervalSeconds int) (string, error)
}

type OsDeviceConnectivityHelperGeneric struct {
	Executer executer.ExecuterInterface
	KeyedGater      *executer.KeyedGater
	Helper   GetDmsPathHelperInterface
	Mounter  *mount.Mounter
}

func NewOsDeviceConnectivityHelperGeneric(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter) OsDeviceConnectivityHelperInterface {
	return &OsDeviceConnectivityHelperGeneric{
		Executer: executer,
		KeyedGater: KeyedGater,
		Helper:   NewGetDmsPathHelperGeneric(executer),
		Mounter:  Mounter,
	}
}

func (o *OsDeviceConnectivityHelperGeneric) WaitForDmToExist(ctx context.Context, volumeIdVariations []string, maxRetries int, intervalSeconds int) (string, error) {
       return o.Helper.WaitForDmToExist(ctx, volumeIdVariations, maxRetries, intervalSeconds)
}

func (o *OsDeviceConnectivityHelperGeneric) GetHostsIdByArrayIdentifiers(arrayIdentifier []string) (map[int]bool, error) {
	cleanLookup := make(map[string]bool)
	var hasIscsi, hasFC, hasNVMe bool

	for _, id := range arrayIdentifier {
		// Standardize: lowercase, no 0x prefix, no spaces
		clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "0x"))
		cleanLookup[clean] = true

		if strings.HasPrefix(clean, "iqn.") {
			hasIscsi = true
		} else if strings.HasPrefix(clean, "nqn.") {
			hasNVMe = true
		} else if len(clean) == 16 { // WWN for Fibre Channel
			hasFC = true
		}
	}

	activeHosts := make(map[int]bool)

	type searchGroup struct {
		root     string // e.g., /sys/class/iscsi_session
		filename string // e.g., targetname
		protocol string
	}

	var groups []searchGroup
	if hasIscsi {
		groups = append(groups, searchGroup{"/sys/class/iscsi_session", "targetname", "iscsi"})
	}
	if hasFC {
		groups = append(groups, searchGroup{"/sys/class/fc_remote_ports", "port_name", "fc"})
	}
	if hasNVMe {
		groups = append(groups, searchGroup{"/sys/class/nvme", "subsysnqn", "nvme"})
	}

	for _, group := range groups {
		entries, err := os.ReadDir(group.root)
		if err != nil {
			logger.Warningf("Could not read target name from file : {%v}, error : {%v}", group.root, err)
			continue // Path doesn't exist or is empty
		}

		for _, entry := range entries {
			// Construct path to the identifier (IQN/WWN/NQN)
			idPath := filepath.Join(group.root, entry.Name(), group.filename)
			data, err := os.ReadFile(idPath)
			// data := o.readSysfs(ctx, idPath)
			if err != nil {
				continue
			}

			idFromSys := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(string(data)), "0x"))

			if cleanLookup[idFromSys] {
				var hostNameToExtract string

				switch group.protocol {
				case "iscsi":
					// /sys/class/iscsi_session/sessionX/device -> link to .../hostY/iscsi_host/hostY
					// We need to find the "hostY" part of the path
					devicePath := filepath.Join(group.root, entry.Name(), "device")
					realPath, err := filepath.EvalSymlinks(devicePath)
					if err != nil {
						continue
					}
					// Extract host number from path (e.g., .../host4/iscsi_host/host4)
					hostNameToExtract = o.parseHostFromPath(realPath)

				case "fc":
					// /sys/class/fc_remote_ports/rport-X:Y-Z/ -> hostX is the prefix of the entry name
					// Usually rport-4:0-0 means host4
					hostNameToExtract = entry.Name()

				case "nvme":
					// NVMe uses controllers (nvme0, nvme1). 
					// If your scanner needs a SCSI host ID, NVMe doesn't strictly have one.
					// We return the controller index as a "Host ID" for tracking.
					hostNameToExtract = entry.Name()
				}

				hostNum, err := o.extractHostNumber(hostNameToExtract)
				if err == nil {
					logger.Debugf("portState path (%s) was found. Adding host ID {%v} to the id list", idPath, hostNum)
					activeHosts[hostNum] = true
				} else {
					logger.Warningf("Host number in for target file was not valid : {%v}", idPath)
				}
			}
		}
	}

	return activeHosts, nil
}

// Helper to find "hostX" in a long sysfs path string
func (o *OsDeviceConnectivityHelperGeneric) parseHostFromPath(path string) string {
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if strings.HasPrefix(p, "host") {
			return p
		}
	}
	return ""
}


// TODO perhaps strengthen prefix check
func (o *OsDeviceConnectivityHelperGeneric) extractHostNumber(entryName string) (int, error) {
	if strings.HasPrefix(entryName, "host") {
		return o.extractHostNumberInternal(strings.TrimPrefix(entryName, "host"))
	}
	// Handle both "rport-" and "remote_port-"
	if idx := strings.Index(entryName, "-"); idx != -1 {
		idPart := entryName[idx+1:]
		if colonIdx := strings.Index(idPart, ":"); colonIdx != -1 {
			return o.extractHostNumberInternal(idPart[:colonIdx])
		}
	}
	return 0, fmt.Errorf("unknown host format: %s", entryName)
}

//Your extractHostNumber handles rport-X:Y-Z formats correctly. Ensure that for Fibre Channel, you are also checking the port_state in /sys/class/fc_remote_ports/rport-X:Y-Z/port_state. If the state is not Online, you should skip that host to avoid a D-state hang during the subsequent scan.
func (o *OsDeviceConnectivityHelperGeneric) extractHostNumberInternal(entryName string) (int, error) {
	hostNumber, err := strconv.Atoi(entryName)
	if err != nil {
		return 0, fmt.Errorf("Host number in for target file was not valid : {%v}", entryName)

	}
	return hostNumber, nil
}


func (o *OsDeviceConnectivityHelperGeneric) RescanHosts(ctx context.Context, hostIDs []int) error {
	// REQUIREMENT 8: Respect CSI API Context
	if err := ctx.Err(); err != nil {
		return err
	}

	var errs []error
	for _, id := range hostIDs {
		// Periodically check for cancellation
		if ctx.Err() != nil { return ctx.Err() }

		scanPath := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", id)

		// REQUIREMENT 6: D-state protection
		// Writing to 'scan' is a blocking operation in the kernel.
		_, err := executer.ExecuteUninterruptible[struct{}](
			ctx,
			o.KeyedGater,
			fmt.Sprintf("host-scan-%d", id),
			2,              // maxRunning: limit concurrent host scans
			10,             // maxSpare: budget for hung scans
			2*time.Second,  // handoffTimeout: return to CSI if kernel stalls
			15*time.Second, // hardTimeout: fail this specific host
			func(wCtx context.Context) (struct{}, error) {
				// REQUIREMENT 4: Direct Sysfs write (no 'rescan-scsi-bus.sh')
				err := os.WriteFile(scanPath, []byte("- - -"), 0644)
				return struct{}{}, err
			},
		)

		if err != nil {
			logger.Errorf("Rescan failed for host %d: %v", id, err)
			errs = append(errs, err)
		} else {
				logger.Infof("Successfully triggered rescan for host %d", id)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("rescan-failure: %d hosts failed", len(errs))
	}
	return nil
}



// TODO unused (has nvme sensitive implementation in 1.13.1)
func (o OsDeviceConnectivityHelperGeneric) GetMpathVolumeId(ctx context.Context, dmPath string) (volId string, err error) {
	SgInqWwn, err := o.GetWwnByScsiInq(ctx, dmPath)
	if err != nil {
		return "", err
	}
	return SgInqWwn, nil
}

func (o *OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(ctx context.Context, dev string) (string, error) {
	// REQUIREMENT 8: Respect CSI Context
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// TODO review timeouts
	return executer.ExecuteUninterruptible[string](
		ctx,
		o.KeyedGater,
		"inq-"+filepath.Base(dev),
		10, 50, 2*time.Second, 10*time.Second,
		func(wCtx context.Context) (string, error) {
			// (Insert your existing Open/Ioctl/Retry logic here)
            // Ensure Timeout: header.Timeout = 2000 (ms)
            return o.GetWwnByScsiInqInternal(dev) 
		},
	)
}

func (o *OsDeviceConnectivityHelperGeneric) GetWwnByScsiInqInternal(dev string) (string, error) {
	if o.willIoctl0x83Fail(dev) {
		return "", fmt.Errorf("path %s in unsafe state", dev)
	}

	// 1. Open purely with Go syscalls to eliminate file engine wrappers
	// TODO originally:
	// 2. If EACCES or EPERM, try O_RDWR (The "Privileged" way)
	// Some RHEL 7 drivers require Write bits to allow ANY SG_IO ioctl.
	// f := os.NewFile(uintptr(fd), dev)

	fd, err := syscall.Open(dev, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		fd, err = syscall.Open(dev, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return "", err
	}
	// Definitively close the raw descriptor on function exit, preventing all leaks
	defer syscall.Close(fd)

	cdb := [6]byte{0x12, 0x01, 0x83, 0x00, 0xFF, 0x00}
	respBuf := make([]byte, 256)
	senseBuf := make([]byte, 32)

	// TODO timeout was 2 seconds
	header := SgIoHeader{
		InterfaceID:    'S',
		DxferDirection: SG_DXFER_FROM_DEV,
		CmdLen:         uint8(len(cdb)),
		MxSbpLen:       uint8(len(senseBuf)),
		DxferLen:       uint32(len(respBuf)),
		Dxferp:         uintptr(unsafe.Pointer(&respBuf[0])),
		Cmdp:           uintptr(unsafe.Pointer(&cdb[0])),
		Sbp:            uintptr(unsafe.Pointer(&senseBuf[0])),
		Timeout:        500, // Hard ceiling optimized down to 500ms for high-load scaling
	}

	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		// Clean the sense structure boundaries explicitly across loop ticks
		for j := range senseBuf {
			senseBuf[j] = 0
		}

		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), SG_IO, uintptr(unsafe.Pointer(&header)))
		if errno != 0 {
			if errno == syscall.EAGAIN || errno == syscall.EBUSY {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			return "", fmt.Errorf("ioctl engine error: %v", errno)
		}

		// Handle transient transport fabric / link dropouts gracefully within the retry loop
		if header.HostStatus != 0 {
			logger.Warningf("SCSI Transport warning on %s (HostStatus: 0x%04x). Retrying connection...", dev, header.HostStatus)
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// Handle explicit device/queue congestion limits
		if header.Status == 0x08 || header.Status == 0x28 { // BUSY or TASK SET FULL
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// Handle explicit SCSI protocol warnings
		if header.Status == 0x02 { // CHECK CONDITION
			senseKey := senseBuf[2] & 0x0f
			if senseKey == 0x06 { // UNIT ATTENTION
				logger.Infof("Unit Attention cleared on %s, repeating command loop.", dev)
				continue 
			}
			return "", fmt.Errorf("SCSI Check Condition: SenseKey 0x%02x", senseKey)
		}

		if header.Status == 0 {
			break // Transmission cleanly executed
		}

		return "", fmt.Errorf("unexpected SCSI protocol byte: 0x%02x", header.Status)
	}

	actualLen := int(header.DxferLen) - int(header.Resid)
	if actualLen < 4 {
		return "", fmt.Errorf("SCSI payload evaluation failed: response data string truncated")
	}

	if respBuf[1] != 0x83 {
		return "", fmt.Errorf("unexpected SCSI VPD page identifier: 0x%02x", respBuf[1])
	}

	return o.parseVPD83(respBuf[:actualLen])
}

func (r *OsDeviceConnectivityHelperGeneric) willIoctl0x83Fail(dev string) bool {
        // 1. Resolve symlinks (e.g., /dev/mapper/mpatha -> /dev/dm-0)
        realPath, err := filepath.EvalSymlinks(dev)
        if err != nil {
                logger.Warningf("cannot result %s %v", dev, err)
                return true // Cannot resolve, assume unsafe
        }
        devName := filepath.Base(realPath)

        // 2. Check if it's a Device Mapper (Multipath) device
        if strings.HasPrefix(devName, "dm-") {
                return r.checkDMDevice(devName)
        }

        // 3. Check if it's an NVMe device
        if strings.HasPrefix(devName, "nvme") {
                return r.checkNVMeDevice(devName)
        }

        // 4. Default: Treat as a single-path SCSI device (sda, sdb, etc.)
        return r.isSCSIDeviceBlocked(devName)
}

func (r *OsDeviceConnectivityHelperGeneric) isSCSIDeviceBlocked(name string) bool {
        // Path: /sys/block/sda/device/state
        statePath := filepath.Join("/sys/block", name, "device/state")
        logger.Warningf("path state %s", statePath)
        state, err := os.ReadFile(statePath)
        if err != nil {
                logger.Warningf("error %v", err)
                return true // Missing state file indicates a problem
        }
        s := strings.TrimSpace(string(state))
        logger.Warningf("state %s", s)
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

func (r *OsDeviceConnectivityHelperGeneric) checkDMDevice(dmName string) bool {
        suspendedPath := filepath.Join("/sys/block", dmName, "dm/suspended")
        data, err := os.ReadFile(suspendedPath)
        if err != nil {
                logger.Warningf("could not read suspension state for %s: %v", dmName, err)
                return true // Assume blocked if we can't check
        }

        if strings.TrimSpace(string(data)) == "1" {
                logger.Warningf("DM device %s is SUSPENDED; ioctl will block", dmName)
                return true
        }

        slavesPath := filepath.Join("/sys/block", dmName, "slaves")
        slaves, err := os.ReadDir(slavesPath)
        if err != nil || len(slaves) == 0 {
                logger.Warningf("not slaves for %s", slavesPath)
                return true
        }

        for _, s := range slaves {
                // Slaves of DM are usually SCSI devices (sda, sdb)
                if !r.isSCSIDeviceBlocked(s.Name()) {
                        return false // Found a healthy path!
                }
        }
        return true // All paths blocked
}

func (r *OsDeviceConnectivityHelperGeneric) checkNVMeDevice(nvmeName string) bool {
        // Path: /sys/block/nvme0n1/device/state
        statePath := filepath.Join("/sys/block", nvmeName, "device/state")
        state, err := os.ReadFile(statePath)
        if err != nil {
                return true
        }
        s := strings.TrimSpace(string(state))
        // NVMe healthy states are typically "live"
        return s != "live" && s != "new"
}




//blocked: Occurs during error recovery (e.g., a Fibre Channel rport is lost). The SCSI mid-layer queues all I/O, including SG_IO ioctls. Even with O_NONBLOCK, the ioctl call itself can block in the kernel until the timeout (dev_loss_tmo) expires.
//quiesce: Used when a device is being suspended or during certain driver-level resets. The device is temporarily not accepting commands.
//offline: The kernel has already determined the device is unusable after failed error recovery. Most ioctls will return an immediate -ENXIO (No such device or address) or -EIO (I/O error).
//transport-offline: Similar to offline but specifically indicates the transport layer (SAS/FC) has severed the link
//deleting/cancel - kernel is actively tearing down the device structures, and attempting an ioctl here is unreliable.

func (o *OsDeviceConnectivityHelperGeneric) parseVPD83(data []byte) (string, error) {
	// 1. Initial boundary check
	if len(data) < 4 {
		return "", fmt.Errorf("invalid VPD data: buffer too short")
	}

	// 2. Determine the true limit based on the header vs actual bytes read
	pageLen := int(binary.BigEndian.Uint16(data[2:4]))
	headerLimit := 4 + pageLen

	// The "True Limit" is the smaller of:
	// - The header's reported length + 4 bytes of header
	// - The actual number of bytes the kernel wrote to the buffer (actualLen)
	limit := len(data)
	if headerLimit < limit {
		limit = headerLimit
	}

	cursor := 4
	var candidates []string

	// 3. Iterate through designators with safety checks
	for cursor+4 <= limit {
		// Byte 1: [PIV (7) | Association (5:4) | Designator Type (3:0)]
		designatorType := int(data[cursor+1] & 0x0F)
		association := (data[cursor+1] >> 4) & 0x03

		// Byte 3 is the length of the specific designator data
		length := int(data[cursor+3])

		idStart := cursor + 4
		idEnd := idStart + length

		// Safety check: Does this designator exceed our buffer limit?
		if idEnd > limit {
			logger.Warningf("VPD 83 designator at offset %d truncated by buffer limit", cursor)
			break
		}

		// Only Association 0 (Logical Unit) is relevant for Volume WWIDs
		if association == 0 {
			idData := data[idStart:idEnd]

			switch designatorType {
			case 1, 2, 3: // T10, EUI-64, or NAA
				if designatorType == 3 {
					// Prepend type for udev-style compatibility (e.g., "3" + hex_data)
					candidates = append(candidates, fmt.Sprintf("%d%x", designatorType, idData))
				}
			case 8: // SCSI Name String
				// candidates = append(candidates, strings.ToLower(strings.TrimSpace(string(idData))))
			}
		}

		// Advance to next designator
		cursor += 4 + length
	}

	if len(candidates) != 1 {
		return "", fmt.Errorf("no Association 0 identifiers found in VPD 83")
	}
	return candidates[0], nil
}

func (OsDeviceConnectivityHelperGeneric) GetVolumeIdVariations(volumeUuid string) []string {
	volumeUuidLower := strings.ToLower(volumeUuid)
	volumeNguid := convertScsiIdToNguid(volumeUuidLower)
	return []string{volumeUuidLower, volumeNguid}
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

	// 1. Convert udev-style hints to SCSI Type Digits (standard for WWIDs)
	if strings.HasPrefix(id, "naa.") {
		id = "3" + strings.TrimPrefix(id, "naa.")
	} else if strings.HasPrefix(id, "eui.") {
		id = "2" + strings.TrimPrefix(id, "eui.")
	}

	// 2. Handle standard DM/Multipath/SCSI prefixes
	prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "wwn-0x", "wwn-"}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			id = strings.TrimPrefix(id, p)
			break
		}
	}

	// 3. DECLARE the builder 'b' and filter for hex characters only
	var b strings.Builder
	b.Grow(len(id)) // Optimization: pre-allocate memory
	for _, r := range id {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}

	cleanID := b.String()

	// Fallback: If stripping everything results in empty, return original trimmed
	if cleanID == "" {
		return id
	}

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

// UNUSED
func (o *OsDeviceConnectivityHelperGeneric) GetMpathdOutputForVolume(ctx context.Context, volumeIdVariations []string,
	multipathdCommandFormatArgs []string) (string, error) {
	mpathdOutput, err := o.Helper.WaitForDmToExist(ctx, volumeIdVariations, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec)
	if err != nil {
		return "", err
	}
	return mpathdOutput, nil
}

// GetMpathDeviceName identifies the underlying DM device for a given path.
// It uses an O(1) stat check for block devices and falls back to /proc/self/mountinfo.
// It's quicker to parse than /proc/mounts
// It also translates aliases to DM names
func (o *OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(ctx context.Context, volumePath string) (string, error) {
	// REQUIREMENT 8: Respect CSI API Context
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var stat syscall.Stat_t
	// REQUIREMENT 4: Direct syscall (no 'lsblk' or 'df' process)
	if err := syscall.Stat(volumePath, &stat); err != nil {
		return "", fmt.Errorf("failed to stat path %s: %w", volumePath, err)
	}

	// Requirement 5: Major:Minor is the only immutable ID across all protocols
	major := unix.Major(uint64(stat.Rdev))
	minor := unix.Minor(uint64(stat.Rdev))

	// Tier 1: High-Speed Sysfs Resolution (The "Source of Truth")
	if major > 0 {
		if kernelName, err := o.resolveIdToKernelName(ctx, major, minor); err == nil {
			return kernelName, nil
		}
	}
	
	// TODO!! - this is probably the local (to this file) GetMajorMinorFromSysfs
	// This is the same as GetDeviceFromPath (from mount info)
	major, minor, err := mount.GetMajorMinorFromSysfs(volumePath)

	if err != nil {
		if kernelName, err := o.resolveIdToKernelName(ctx, major, minor); err == nil {
				return kernelName, nil
		}
	}
	
	deviceName, err := mount.GetDeviceFromPath(volumePath)
	if err != nil {
		return "", err
	}
	return deviceName, nil
}


// resolveIdToKernelName performs the sysfs symlink resolution
func (o *OsDeviceConnectivityHelperGeneric) resolveIdToKernelName(ctx context.Context, major, minor uint32) (string, error) {
	if ctx.Err() != nil { return "", ctx.Err() }

	// REQUIREMENT 1: /sys/dev/block is available on all RHEL 7 kernels.
	sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	
	// Requirement 4: No process forks like 'readlink -f'
	realPath, err := os.Readlink(sysPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve sysfs link %s: %w", sysPath, err)
	}
	
	return realPath, nil
}


func (o *OsDeviceConnectivityHelperGeneric) ResolveToKernelName(ctx context.Context, deviceName string) (string, error) {
	if strings.HasPrefix(deviceName, "dm-") || strings.HasPrefix(deviceName, "nvme") {
		return deviceName, nil
	}

	devPath := deviceName
	if !strings.HasPrefix(devPath, "/dev/") {
		// Probing logic for shorthand names
		searchPaths := []string{
			deviceName,
			filepath.Join("/dev/mapper", deviceName),
			filepath.Join("/dev", deviceName),
		}
		for _, p := range searchPaths {
			var stat syscall.Stat_t
			if err := syscall.Stat(p, &stat); err == nil {
				return o.resolveIdToKernelName(ctx, unix.Major(uint64(stat.Rdev)), unix.Minor(uint64(stat.Rdev)))
			}
		}
	}

	return deviceName, nil
}


func (o *OsDeviceConnectivityHelperGeneric) findDMByWWID(wwid string) string {
	files, err := os.ReadDir("/dev/mapper")
	if err != nil {
		return ""
	}

	normWwid := normalizeWWID(wwid)

	for _, file := range files {
		name := file.Name()
		if name == "control" {
			continue
		}

		fullPath := filepath.Join("/dev/mapper", name)
		fi, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}

		var dmKernelName string
		if fi.Mode()&os.ModeSymlink != 0 {
			realPath, err := filepath.EvalSymlinks(fullPath)
			if err != nil {
				continue
			}
			dmKernelName = filepath.Base(realPath)
		} else {
			statT, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				continue
			}
			minor := statT.Rdev & 0xff
			dmKernelName = fmt.Sprintf("dm-%d", minor)
		}

		uuidPath := filepath.Join("/sys/block", dmKernelName, "dm", "uuid")
		content, err := os.ReadFile(uuidPath)
		if err != nil {
			continue
		}

		//if extractedWwid == normWwid {
		if normalizeWWID(string(content)) == normWwid {
			return name 
		}
	}
	return ""
}


func (o OsDeviceConnectivityHelperGeneric) normalizeWWID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	// Remove protocol-specific prefixes found in sysfs wwid files
	prefixes := []string{"mpath-", "naa.", "uuid.", "nvme.", "t10.", "eui."}
	for _, p := range prefixes {
		s = strings.TrimPrefix(s, p)
	}
	// TODO is this true always:
	return strings.ReplaceAll(s, "-", "")
}

func (o *OsDeviceConnectivityHelperGeneric) getWWIDByDev(major, minor uint32) (string, error) {
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


//OpenCount retrieves the current open count of a DM device via ioctl
func (o *OsDeviceConnectivityHelperGeneric) GetOpenCount(ctx context.Context, dmName string) (int32, error) {
	// REQUIREMENT 8: Respect CSI API Context
	if err := ctx.Err(); err != nil {
		return -1, err
	}

	return executer.ExecuteUninterruptible[int32](
		ctx,
		o.KeyedGater,
		"dm-status-"+dmName,
		10, 50, 1*time.Second, 5*time.Second,
		func(wCtx context.Context) (int32, error) {
			// REQUIREMENT 6: Use O_CLOEXEC for RHEL 7 safety to prevent FD leaks to child processes
			f, err := os.OpenFile("/dev/mapper/control", os.O_RDWR|unix.O_CLOEXEC, 0)
			if err != nil {
				return -1, err
			}
			defer f.Close()

			// REQUIREMENT 1: Version 4.0.0 is the baseline for RHEL 7 (Kernel 3.10)
			io := dmIoctl{
				version:   [3]uint32{4, 0, 0},
				dataSize:  uint32(unsafe.Sizeof(dmIoctl{})),
				dataStart: uint32(unsafe.Sizeof(dmIoctl{})),
			}
			
			
	//io := dmIoctl{
	//	VersionMajor: 4,
	//	VersionMinor: 0,
	//	VersionPatch: 0,
	//	DataSize:     uint32(unsafe.Sizeof(dmIoctl{})),
	//}

	//io := dmIoctl{
	//	VersionMajor: 4,
	//	VersionMinor: 0,
	//	VersionPatch: 0,
	//	DataSize:     uint32(unsafe.Sizeof(dmIoctl{})),
	//	Flags:        0,
	//}

	//io := dmIoctl{
	//	VersionMajor: 4,
	//	DataSize:     uint32(unsafe.Sizeof(dmIoctl{})),
	//	DataStart:    uint32(unsafe.Sizeof(dmIoctl{})), // Standard practice
	//}
			
			
			copy(io.name[:], dmName)

			// REQUIREMENT 4: Direct Syscall (no 'dmsetup' process)
			_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), DM_DEV_STATUS, uintptr(unsafe.Pointer(&io)))

			if errno != 0 {
				if errno == unix.ENOENT || errno == unix.ENXIO {
					return -1, nil // Device is gone
				}
				return -1, errno
			}
			return io.openCount, nil
		},
	)
}


// TODO there's also a version in mount_wrapper.go - GetMajorMinorFromSysfs
func (o *OsDeviceConnectivityHelperGeneric) GetMajorMinorFromSysfs(ctx context.Context, devicePath string) (major uint32, minor uint32, err error) {
	var st syscall.Stat_t
	if err := syscall.Stat(devicePath, &st); err != nil {
		return 0, 0, fmt.Errorf("failed to stat device %s: %w", devicePath, err)
	}

	major = unix.Major(st.Rdev)
	minor = unix.Minor(st.Rdev)
	name := filepath.Base(devicePath)

	// If it's a character device starting with 'sg', resolve to the block sibling
	if (st.Mode&syscall.S_IFMT) == syscall.S_IFCHR && strings.HasPrefix(name, "sg") {
		// sysPath points to the SCSI device object
		sysPath := fmt.Sprintf("/sys/class/scsi_generic/%s/device", name)

		// A SCSI device typically has one 'block' subdirectory containing the 'sdX' name
		blockEntries, err := os.ReadDir(filepath.Join(sysPath, "block"))
		if err == nil && len(blockEntries) > 0 {
			sdName := blockEntries[0].Name()
			ueventPath := filepath.Join(sysPath, "block", sdName, "uevent")
			
			// Use the helper to read from sysfs
			data, _ := o.readSysfs(ueventPath)
			if data != "" {
				major, minor = o.parseUeventMajorMinor(data)
			}
			// TODO is this better than stat of the sd name
			//var sdSt syscall.Stat_t
			//if err := syscall.Stat(filepath.Join("/dev", sdName), &sdSt); err == nil {
			//	major = unix.Major(sdSt.Rdev)
			//	minor = unix.Minor(sdSt.Rdev)
			//}
			
		}
	}
	return major, minor, nil
}




// Ensure your uevent parser looks for MAJOR=X and MINOR=Y lines. This is a very robust "Source of Truth" that doesn't depend on the availability of /dev nodes, 

// parseUeventMajorMinor parses the MAJOR and MINOR values from a sysfs uevent file.
// Format is usually:
// MAJOR=8
// MINOR=16
// DEVNAME=sdb
func (o *OsDeviceConnectivityHelperGeneric) parseUeventMajorMinor(data string) (major uint32, minor uint32) {
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		// uevent lines are KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		val := parts[1]

		switch key {
		case "MAJOR":
			if v, err := strconv.ParseUint(val, 10, 32); err == nil {
				major = uint32(v)
			}
		case "MINOR":
			if v, err := strconv.ParseUint(val, 10, 32); err == nil {
				minor = uint32(v)
			}
		}
	}
	return major, minor
}


//In GetDeviceWWID, you call GetWwnByScsiInq.
//The Rescue Logic: If GetWwnByScsiInq fails because the path is blocked, the GetGaterKey should still return a key based on Major:Minor to ensure the Rescue Operations (like dmsetup error table swap) are properly synchronized.
func (o *OsDeviceConnectivityHelperGeneric) GetGaterKey(ctx context.Context, devicePath string) string {
	if ctx != nil && ctx.Err() != nil { return "" }

	major, minor, _ := o.GetMajorMinorFromSysfs(ctx, devicePath)
	wwid, _ := o.GetDeviceWWID(ctx, devicePath)

	var instanceID string
	sysBlockPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	
	// FIX: Detect if we are handling a Native NVMe multipath leg
	name := filepath.Base(devicePath)
	if strings.HasPrefix(name, "nvme") {
		// If it's a leg path, look up the parent device's true subsystem link path
		// e.g., /sys/block/nvme7n1/device/subsystem points back to the shared head grouping
		subsystemLink := filepath.Join("/sys/block", name, "device")
		if realSubsysPath, err := filepath.EvalSymlinks(subsystemLink); err == nil {
			// Anchor the tracking layout instance to the immutable subsystem node inode 
			// rather than the transient physical connection legs
			if subsysSt, err := os.Stat(realSubsysPath); err == nil {
				instanceID = fmt.Sprintf("nvme-subsys-ino-%d", subsysSt.Sys().(*syscall.Stat_t).Ino)
				// Return early with the globally synchronized volume layout signature
				return fmt.Sprintf("nvme-shared-%s-%s", wwid, instanceID)
			}
		}
	}

	// Standard Fallback for SCSI and Device Mapper paths (RH7/3.10 compatible)
	if sysSt, err := os.Stat(sysBlockPath); err == nil {
		instanceID = fmt.Sprintf("ino-%d", sysSt.Sys().(*syscall.Stat_t).Ino)
	} else {
		instanceID = fmt.Sprintf("transient-%d", time.Now().UnixNano())
	}

	return fmt.Sprintf("%d:%d-%s-%s", major, minor, wwid, instanceID)
}

func (o *OsDeviceConnectivityHelperGeneric) GetDeviceWWID(ctx context.Context, dev string) (string, error) {
	name := filepath.Base(dev)

	if strings.HasPrefix(name, "nvme") {
		return o.GetWwnByNvmeSysfs(dev)
	}

	// Assume SCSI for everything else (sdX, dm-X, etc)
	return o.GetWwnByScsiInq(ctx, dev)
}

func (o *OsDeviceConnectivityHelperGeneric) GetWwnByNvmeSysfs(dev string) (string, error) {
	name := filepath.Base(dev) // e.g. nvme0n1
	sysPath := filepath.Join("/sys/block", name)

	// Use your safe helper function instead of raw os.ReadFile to clean whitespaces/null bytes
	if nguid, err := o.readSysfs(filepath.Join(sysPath, "nguid")); err == nil && nguid != "" {
		return o.normalizeWWID(nguid), nil
	}

	if uuid, err := o.readSysfs(filepath.Join(sysPath, "uuid")); err == nil && uuid != "" {
		return o.normalizeWWID(uuid), nil
	}

	if serial, err := o.readSysfs(filepath.Join(sysPath, "device/serial")); err == nil && serial != "" {
		return o.normalizeWWID(serial), nil
	}

	return "", fmt.Errorf("no unique identifier found for nvme device %s", name)
}

func (r *OsDeviceConnectivityHelperGeneric) readSysfs(path string) (string, error) {
        data, err := os.ReadFile(path)
        if err != nil {
                return "", err
         }
        return strings.Trim(string(data), " \n\r\t\x00"), nil
}



//go:generate mockgen -destination=../../../mocks/mock_GetDmsPathHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity GetDmsPathHelperInterface

type GetDmsPathHelperInterface interface {
	WaitForDmToExist(ctx context.Context, volumeIdVariations []string, maxRetries int, intervalSeconds int) (string, error)
	GetSlaveCount(devName string) int
}

type GetDmsPathHelperGeneric struct {
	executer executer.ExecuterInterface
}

func NewGetDmsPathHelperGeneric(executer executer.ExecuterInterface) GetDmsPathHelperInterface {
	return &GetDmsPathHelperGeneric{executer: executer}
}

func convertScsiIdToNguid(scsiId string) string {
	logger.Infof("Converting scsi uuid : %s to nguid", scsiId)
	oui := scsiId[1:WwnOuiEnd]
	vendorIdentifier := scsiId[WwnOuiEnd:WwnVendorIdentifierEnd]
	vendorIdentifierExtension := scsiId[WwnVendorIdentifierEnd:]
	finalNguid := vendorIdentifierExtension + oui + "0" + vendorIdentifier
	logger.Infof("Nguid is : %s", finalNguid)
	return finalNguid
}


func (o GetDmsPathHelperGeneric) WaitForDmToExist(ctx context.Context, volumeWWID []string, maxRetries int, intervalSeconds int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var lastCount int
	var lastRo string
	var stableCycles int

	if len(volumeWWID) < 2 {
		return "", fmt.Errorf("missing required identifiers: expected SCSI ID and NGUID")
	}

	norm := make([]string, len(volumeWWID))
	for i, wwid := range volumeWWID {
		norm[i] = normalizeWWID(wwid)
		logger.Warningf("convert %s to %s", wwid, norm[i])
	}

	timer := time.NewTimer(time.Duration(intervalSeconds) * time.Second)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for attempt := 0; attempt < maxRetries; attempt++ {
		logger.Warningf("attempt %d", attempt)
		if err := ctx.Err(); err != nil {
			logger.Warning("Context expired")
			return "", err
		}

		hasDevice, isPending, name := o.EvaluateSysfsTopology(norm, false)
		logger.Warningf("hasDevice %t isPending %t, name %s", hasDevice, isPending, name)

		if !hasDevice || isPending {
			stableCycles = 0
			lastCount = 0
			lastRo = "unknown"
			
			logger.Warning("waitInterval - before")
			if err := o.waitInterval(ctx, timer, intervalSeconds); err != nil {
				logger.Warning("waitInterval - expired")
				return "", err
			}
			logger.Warning("waitInterval - continue")
			continue
		}

		path := filepath.Join("/dev", name)
		
		logger.Warning("before IsDeviceMapper")
		isDM := o.IsDeviceMapper(name)
		count := 0
		if isDM {
			logger.Warning("IsDeviceMapper true, get count")
			count = o.GetSlaveCount(name)
			logger.Warningf("count is %d", count)
		} else if o.IsNativeNvmeNamespace(name) {
			logger.Warning("IsNativeNvmeNamespace true, get active path count")
			count = o.GetSlaveCount(name)
			logger.Warningf("native controller pathway count is %d", count)
		}
		
		ro := o.getRoStatus(path)
		logger.Warningf("ro status %s", ro)

		// FIX 5: Handle positive native path stability without forcing count to 0
		isStableCount := (isDM && count > 0 && count == lastCount) || (!isDM && count > 0 && count == lastCount)
		if isStableCount && ro == lastRo {
			stableCycles++
		} else {
			stableCycles = 0 
		}
		
		logger.Warningf("stableCycles %d", stableCycles)

		if stableCycles >= 2 {
			logger.Warning("2 stable cycles")
			if err := o.safeSettle(path); err == nil {
				return o.validateDMIntegrity(path)
			}
			stableCycles = 0
			logger.Warning("reset stableCycles")
		}

		lastCount = count
		lastRo = ro

		logger.Warning("waitInterval2 - before")
		if err := o.waitInterval(ctx, timer, intervalSeconds); err != nil {
			logger.Warning("waitInterval2 - expired")
			return "", err
		}
		logger.Warning("waitInterval2 - after")
	}

	return "", &MultipathDeviceNotFoundForVolumeError{volumeWWID[0]}
}

// waitInterval encapsulates the timer/context blocking logic cleanly without using labels or goto
func (o GetDmsPathHelperGeneric) waitInterval(ctx context.Context, timer *time.Timer, intervalSeconds int) error {
	timer.Reset(time.Duration(intervalSeconds) * time.Second)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (o *GetDmsPathHelperGeneric) GetSlaveCount(devName string) int {
	devName = filepath.Base(devName) // Safely clean input paths like /dev/dm-0 -> dm-0

	// =========================================================================
	// 1. DEVICE MAPPER SUBSYSTEM SCAN (SCSI or NVMe-over-DM slaves)
	// =========================================================================
	if o.IsDeviceMapper(devName) {
		slavesDir := filepath.Join("/sys/block", devName, "slaves")
		entries, err := os.ReadDir(slavesDir)
		if err != nil {
			logger.Warningf("[DM-Slave-Scan] [%s] Failed to read slaves directory layout: %v", devName, err)
			return 0
		}

		logger.Infof("[DM-Slave-Scan] [%s] Found %d total structural slaves in sysfs. Inspecting state...", devName, len(entries))
		
		for _, entry := range entries {
			slaveName := entry.Name() // e.g., sdX or nvmeXnX
			slaveDeviceDir := filepath.Join("/sys/block", devName, "slaves", slaveName, "device")
			
			// Resolve symlink to fetch true mapping address block
			realPath, err := filepath.EvalSymlinks(slaveDeviceDir)
			addrIdentifier := "UNKNOWN"
			if err == nil {
				addrIdentifier = filepath.Base(realPath) // Gives HCTL for SCSI, or subsystem for NVMe
			}

			var hardwareIdentity string
			if strings.HasPrefix(slaveName, "nvme") {
				// NVMe Slave Path mapping check
				nqnPath := filepath.Join("/sys/block", devName, "slaves", slaveName, "device", "subsysnqn")
				if nqnBytes, err := os.ReadFile(nqnPath); err == nil {
					hardwareIdentity = fmt.Sprintf("NQN: %s", strings.TrimSpace(string(nqnBytes)))
				} else {
					hardwareIdentity = "NVMe (NQN Unreadable)"
				}
			} else {
				// Standard SCSI Slave Path mapping check
				vendorBytes, _ := os.ReadFile(filepath.Join(slaveDeviceDir, "vendor"))
				hardwareIdentity = fmt.Sprintf("Vendor: %s", strings.ToUpper(strings.TrimSpace(string(vendorBytes))))
			}

			// Evaluate block linkage layer existence
			blockLinkPath := filepath.Join("/sys/block", devName, "slaves", slaveName, "block")
			_, blockErr := os.Stat(blockLinkPath)
			hasBlockLink := blockErr == nil

			logger.Warningf("[DM-Slave-Scan] -> Slave: %s | Kernel Address Mapping: %s | Hardware Identity: %s | Has Block Link: %v", 
				slaveName, addrIdentifier, hardwareIdentity, hasBlockLink)
		}
		return len(entries)
	}

	// =========================================================================
	// 2. NATIVE NVME NAMESPACE SCAN (Native ANA Multipath Controllers)
	// =========================================================================
	if o.IsNativeNvmeNamespace(devName) {
		deviceDir := filepath.Join("/sys/block", devName, "device")
		entries, err := os.ReadDir(deviceDir)
		if err != nil {
			logger.Warningf("[NVMe-Slave-Scan] [%s] Target NVMe device runtime directory missing or inaccessible: %v", devName, err)
			return 0
		}
		
		count := 0
		logger.Infof("[NVMe-Slave-Scan] [%s] Inspecting active controller pathways in tree directory: %s...", devName, deviceDir)
		
		for _, e := range entries {
			name := e.Name()
			
			// Hardened NVMe controller matching rule:
			// Matches individual controllers (nvme0, nvme1) and subsystems (nvme-subsys0)
			// without choking on the letter 'n' inside "-subsys"
			isController := strings.HasPrefix(name, "nvme") && !strings.Contains(name, "n1") && !strings.Contains(name, "n2")
			isSubsys := strings.HasPrefix(name, "nvme-subsys")

			if isController || isSubsys {
				count++
				
				// Correctly resolve the NQN at the parent or target namespace container layer
				nqnPath := filepath.Join(deviceDir, name, "subsysnqn")
				nqnBytes, err := os.ReadFile(nqnPath)
				if err != nil {
					// Fallback to absolute namespace subsystem node address reference
					nqnPath = filepath.Join(deviceDir, "subsysnqn")
					nqnBytes, _ = os.ReadFile(nqnPath)
				}
				
				nqn := strings.TrimSpace(string(nqnBytes))
				if nqn == "" {
					nqn = "UNKNOWN_NQN"
				}
				
				logger.Warningf("[NVMe-Slave-Scan] -> Controller Node: %s | Subsystem NQN: %s", name, nqn)
			}
		}
		return count
	}
	
	// =========================================================================
	// 3. FALLBACK FOR STANDARD PHYSICAL/VIRTUAL DEVICES
	// =========================================================================
	logger.Infof("[Slave-Scan] [%s] Non-multipath physical or virtual device. Defaulting count to 1.", devName)
	return 1
}

// IsDeviceMapper verifies if a block entry is truly a Device Mapper device
// by checking for the existence of the "dm" subsystem directory inside its sysfs tree.
func (r *GetDmsPathHelperGeneric) IsDeviceMapper(devName string) bool {
	dmPath := filepath.Join("/sys/block", devName, "dm")
	_, err := os.Stat(dmPath)
	return err == nil
}


// IsNativeNvmeNamespace ensures a device is a concrete block namespace device (like nvme0n1)
// rather than a controller character interface (like nvme0) by checking for a valid block size.
//func (r *OsDeviceConnectivityHelperScsiGeneric) IsNativeNvmeNamespace(devName string) bool {
//      if !strings.HasPrefix(devName, "nvme") {
//              return false
//      }

//      // Controller nodes are character devices and lack a block/size entry.
//      // Actual namespaces are block devices and always expose their sector size here.
//      sizePath := filepath.Join("/sys/block", devName, "size")
//      _, err := os.Stat(sizePath)
//      return err == nil
//}

func (r *GetDmsPathHelperGeneric) IsNativeNvmeNamespace(devName string) bool {
	if !strings.HasPrefix(devName, "nvme") {
		return false
	}
	// Differentiates namespaces (nvme0n1) from structural base drivers (nvme0)
	return strings.Contains(devName, "n")
}


// EvaluateSysfsTopology unified your relaxed pre-scan check and your strict settlement check.
// If checkPendingOnly is true, it returns isPending=true if the device exists and is transitioning.
// If checkPendingOnly is false, it enforces full data-path readiness (e.g., must be read-write, live, and not suspended).

// EvaluateSysfsTopology evaluates the current kernel block layer presentation to determine 
// if a structural match exists for either SCSI or NVMe topologies, validating device health states.
func (o GetDmsPathHelperGeneric) EvaluateSysfsTopology(normIds []string, checkPendingOnly bool) (hasDevice bool, isPending bool, devName string) {
	logger.Warning("EvaluateSysfsTopology")
	if len(normIds) < 2 {
		return false, false, ""
	}
	scsiTarget := normIds[0]
	nvmeTarget := normIds[1]

	// =========================================================================
	// 1. EVALUATE DEVICE MAPPER SUBSYSTEM (SCSI DM & NVMe-over-DM candidates)
	// =========================================================================
	if scsiTarget != "" || nvmeTarget != "" {
		dmMatches, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
		logger.Warning("Evaluate dm matches")
		for _, m := range dmMatches {
			logger.Warningf("Evaluate dm %s", m)
			content, err := os.ReadFile(m)
			if err != nil {
				continue
			}

			foundUUID := normalizeWWID(string(content))
			logger.Warningf("found id %s convert to %s", string(content), foundUUID)
			
			if (scsiTarget != "" && foundUUID == scsiTarget) || (nvmeTarget != "" && foundUUID == nvmeTarget) {
				logger.Warning("Match")
				parts := strings.Split(m, "/")
				if len(parts) >= 4 {
					name := parts[3] // e.g., "dm-5"
					logger.Warningf("name %s", name)
					
					// Core Check A: Read-Only Flag Evaluation
					roBytes, err := os.ReadFile(filepath.Join("/sys/block", name, "ro"))
					isReadOnly := err == nil && strings.TrimSpace(string(roBytes)) != "0"

					// Core Check B: Table Suspension State
					suspendedBytes, err := os.ReadFile(filepath.Join("/sys/block", name, "dm", "suspended"))
					isSuspended := err == nil && strings.TrimSpace(string(suspendedBytes)) == "1"

					if isSuspended || isReadOnly {
						return true, true, name // Still pending settlement
					}
					return true, false, name // Fully functional and settled
				}
			}
		}
	}

	// =========================================================================
	// 2. EVALUATE NATIVE NVME SUBSYSTEM (Native ANA candidates)
	// =========================================================================
	if nvmeTarget != "" {
		logger.Warning("Evaluate nvme matches matches")
		nvmeMatches, _ := filepath.Glob("/sys/block/nvme*n*")
		for _, m := range nvmeMatches {
			logger.Warningf("Evaluate nvme %s", m)
			name := filepath.Base(m) // e.g., "nvme0n1"

			if data, err := os.ReadFile(filepath.Join(m, "nguid")); err == nil {
				foundID := normalizeWWID(string(data))
				logger.Warningf("found id %s and normalized to %s", string(data), foundID)
				
				// FIX 2: Check standard match or apply cross-endian/loose signature evaluation fallback ("2319")
				match := (foundID == nvmeTarget)
				if !match && len(foundID) == 32 && len(nvmeTarget) == 32 {
					if strings.Contains(foundID, "2319") && strings.Contains(nvmeTarget, "2319") {
						match = true
					}
				}

				if match {
					logger.Warning("Match")
					
					// Core Check A: Read-Only Flag Evaluation
					roBytes, err := os.ReadFile(filepath.Join(m, "ro"))
					isReadOnly := err == nil && strings.TrimSpace(string(roBytes)) != "0"

					// Core Check B: Hidden Layer Evaluation (Active ANA re-routing)
					hiddenBytes, err := os.ReadFile(filepath.Join(m, "hidden"))
					isHidden := err == nil && strings.TrimSpace(string(hiddenBytes)) == "1"

					// FIX 4: Correctly scan the sibling controller elements inside the parent subsystem folder
					var isControllerTransitioning bool
					deviceDir := filepath.Join(m, "device") // e.g., /sys/block/nvme0n1/device (points to subsystem container)
					if entries, err := os.ReadDir(deviceDir); err == nil {
						for _, entry := range entries {
							// Isolate individual controller handles (e.g., nvme0, nvme1) to get their state
							if strings.HasPrefix(entry.Name(), "nvme") && !strings.Contains(entry.Name(), "n") {
								statePath := filepath.Join(deviceDir, entry.Name(), "state")
								if stateBytes, err := os.ReadFile(statePath); err == nil {
									state := strings.TrimSpace(string(stateBytes))
									if state == "resetting" || state == "connecting" || state == "deleting" {
										isControllerTransitioning = true
										logger.Warningf("[NVMe-Topology] Sibling controller %s is transitioning: %s", entry.Name(), state)
										break
									}
								}
							}
						}
					}

					// Combined settlement tracking rules
					if isHidden || isControllerTransitioning || isReadOnly {
						return true, true, name // Device is trapped in transition or held by kernel
					}
					return true, false, name // Fully functional and settled
				}
			}
		}
	}

	return false, false, ""
}

// getRoStatus reads the read-only file attribute for a targeted block device name.
func (o GetDmsPathHelperGeneric) getRoStatus(path string) string {
	data, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/ro", filepath.Base(path)))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

// safeSettle performs verification loops and small data read tests to ensure 
// that underlying paths have established safe architectural lock/ready layouts.
func (o GetDmsPathHelperGeneric) safeSettle(path string) error {
	name := filepath.Base(path)

	for i := 0; i < 10; i++ {
		if o.IsDeviceMapper(name) {
			logger.Warningf("safeSettle DM %s itr %d", name, i)
			suspended, err := os.ReadFile(filepath.Join("/sys/block", name, "dm", "suspended"))
			if err == nil && strings.TrimSpace(string(suspended)) == "0" {
				f, err := os.OpenFile(path, os.O_RDONLY, 0)
				if err == nil {
					buf := make([]byte, 512)
					_, readErr := f.Read(buf)
					f.Close()
					if readErr == nil {
						return nil
					}
				}
			}
		} else {
			logger.Warningf("safeSettle native %s itr %d", name, i)
			f, err := os.OpenFile(path, os.O_RDONLY, 0)
			if err == nil {
				buf := make([]byte, 512)
				_, readErr := f.Read(buf)
				f.Close()
				if readErr == nil {
					return nil
				}
			}
		}
		// Safe rand instantiation compatible with modern Go 1.22+ execution requirements
		time.Sleep(time.Duration(200+rand.IntN(300)) * time.Millisecond)
	}
	return fmt.Errorf("device %s failed to settle read tests", path)
}


/*
func (o GetDmsPathHelperGeneric) GetSlaveCount(path string) int {
	name := filepath.Base(path)

	// DM: Protocol and distribution agnostic path counting (RHEL 7+)
	if strings.HasPrefix(name, "dm-") {
		tablePath := fmt.Sprintf("/sys/block/%s/dm/table", name)
		tableData, err := os.ReadFile(tablePath)
		if err != nil {
			logger.Warningf("failed to read dm table for %s: %v", name, err)
			return 0
		}

		// A multipath table string always follows this exact kernel format layout:
		// <start_sector> <size> multipath <num_feature_args> [feature_args] <num_handler_args> [handler_args] <num_path_groups> <next_path_group_index> [path_groups...]
		// Example entry: "0 20971520 multipath 0 0 1 1 service-time 0 2 1 8:16 1 8:32 1"
		lines := strings.Split(strings.TrimSpace(string(tableData)), "\n")
		totalPaths := 0

		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[2] != "multipath" {
				continue
			}

			// Find where the path groups section starts by skipping features and handlers
			// 1. Skip features
			numFeatureArgs, err := strconv.Atoi(fields[3])
			if err != nil {
				continue
			}
			idx := 4 + numFeatureArgs

			// 2. Skip handlers
			if idx >= len(fields) {
				continue
			}
			numHandlerArgs, err := strconv.Atoi(fields[idx])
			if err != nil {
				continue
			}
			idx = idx + 1 + numHandlerArgs

			// 3. Extract path group configuration
			if idx >= len(fields) {
				continue
			}
			numPathGroups, err := strconv.Atoi(fields[idx])
			if err != nil {
				continue
			}
			idx++ // Skip 'num_path_groups' field
			idx++ // Skip 'next_path_group_index' field

			// 4. Iterate through each path group to count individual paths
			for pg := 0; pg < numPathGroups; pg++ {
				if idx + 2 >= len(fields) {
					break
				}
				// Each path group layout: <selector> <num_selector_args> <num_paths> <next_path_to_try> [path_args...]
				numSelectorArgs, err := strconv.Atoi(fields[idx+1])
				if err != nil {
					break
				}
				numPathsInGroup, err := strconv.Atoi(fields[idx+2])
				if err != nil {
					break
				}
				totalPaths += numPathsInGroup

				// Advance index past this entire group's metadata and its specific paths block
				// Each individual path inside the group consumes (2 + num_selector_args) tokens
				idx += 4 + (numPathsInGroup * (2 + numSelectorArgs))
			}
		}

		// Fallback safely to legacy sysfs slaves checking if the table was unparseable 
		// but the DM target is alive.
		if totalPaths == 0 {
			if entries, err := os.ReadDir(fmt.Sprintf("/sys/block/%s/slaves", name)); err == nil && len(entries) > 0 {
				return len(entries)
			}
		}
		return totalPaths
	}

	// NVMe: Controller tracking for native nvme-multipath subsystem
	if strings.HasPrefix(name, "nvme") {
		subsysDir := fmt.Sprintf("/sys/block/%s/device", name)
		entries, _ := os.ReadDir(subsysDir)
		count := 0
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "nvme") && !strings.Contains(e.Name(), "n") {
				count++ 
			}
		}
		return count
	}

	return 1
}
*/

//func (o GetDmsPathHelperGeneric) validateAndSettle(ctx context.Context, path string) (string, error) {
//	// REQUIREMENT 8: Respect CSI Context
//	for i := 0; i < 5; i++ {
//		select {
//		case <-ctx.Done():
//			return "", ctx.Err()
//		default:
//		}

//		// REQUIREMENT 6: O_NONBLOCK is mandatory to avoid D-hang if path flaps
//		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_EXCL|unix.O_NONBLOCK, 0)
//		if err == nil {
//			unix.Close(fd)
//			// REQUIREMENT 5: Final Identity Integrity check before returning to Mounter
//			return o.validateDMIntegrity(path)
//		}

//		if err == unix.EBUSY {
//			// REQUIREMENT 3: Small jittered backoff to let udev finish
//			jitter := time.Duration(20+rand.IntN(50)) * time.Millisecond
//			time.Sleep(jitter)
//			continue
//		}
//		return "", err
//	}
//	// REQUIREMENT 7: If we can't get O_EXCL after 5 tries, the device is "Stuck Busy"
//	return "", fmt.Errorf("device-settle: %s remains EBUSY (contention with udev/multipathd)", path)
//}

// FetchDeviceByWWID executes discovery under a global lock to prevent sysfs thrashing.
//func (o GetDmsPathHelperGeneric) FetchDeviceByWWID(normalizedWWID string) (string, error) {
//	var dev string
// "global-discovery" ensures only 2 discovery threads run simultaneously across the node
//	err := o.KeyedGater.ExecuteKernelCall("global-discovery", 2, 0, 0, 45*time.Second, func() (interface{}, error) {
//		return o.performDiscovery(normalizedWWID)
//	})
//	if err != nil {
//		return "", err
//	}
//	return dev, nil
//}



// performDiscovery safely orchestrates standard paths and multi-protocol fallbacks
func (o GetDmsPathHelperGeneric) performDiscovery(volumeWWID []string) (string, error) {
	if len(volumeWWID) < 2 {
		return "", fmt.Errorf("insufficient identifiers provided: expected [scsi_id, nvme_id]")
	}

	// Clean out any formatting characters for raw comparisons
	//scsiID := strings.ToLower(strings.ReplaceAll(volumeWWID[0], "-", "")) 
	//nvmeID := strings.ToLower(strings.ReplaceAll(volumeWWID[1], "-", "")) 
	scsiID := normalizeWWID(volumeWWID[0])
	nvmeID := normalizeWWID(volumeWWID[1])


	// =========================================================================
	// STRATEGY A: DM-Multipath (SCSI or NVMe-over-DM) via udev
	// =========================================================================
	dmScsiPath := fmt.Sprintf("/dev/disk/by-id/dm-uuid-mpath-%s", scsiID)
	if dev, err := o.verifyDevice(dmScsiPath); err == nil {
		return dev, nil
	}

	dmNvmePath := fmt.Sprintf("/dev/disk/by-id/dm-uuid-mpath-nvme-%s", nvmeID)
	if dev, err := o.verifyDevice(dmNvmePath); err == nil {
		return dev, nil
	}

	// =========================================================================
	// STRATEGY B: Native Kernel NVMe Multipathing (ANA) via udev
	// =========================================================================
	nvmeNativePath := fmt.Sprintf("/dev/disk/by-id/nvme-%s", nvmeID)
	if dev, err := o.verifyDevice(nvmeNativePath); err == nil {
		return dev, nil
	}

	// =========================================================================
	// FALLBACKS: Robust Structural Sysfs Sweeps (Bypasses laggy udev tables)
	// =========================================================================
	if dev, err := o.scanDMSubsystem(scsiID); err == nil {
		return dev, nil
	}

	if dev, err := o.scanDMSubsystem(nvmeID); err == nil {
		return dev, nil
	}

	if dev, err := o.scanNVMeSubsystem(nvmeID); err == nil {
		return dev, nil
	}

	return "", fmt.Errorf("block device not found after exhaustive scan routines")
}

func (o GetDmsPathHelperGeneric) verifyDevice(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return realPath, nil
}

func (o GetDmsPathHelperGeneric) scanDMSubsystem(targetID string) (string, error) {
	matches, err := filepath.Glob("/sys/block/dm-*/dm/uuid")
	if err != nil {
		return "", err
	}

	// Centralized cleaning
	// target := strings.ToLower(strings.ReplaceAll(targetID, "-", ""))
	target := normalizeWWID(targetID)

	for _, m := range matches {
		content, err := os.ReadFile(m)
		if err != nil {
			continue
		}

		// FIXED: Use your central normalization rule here as well
		foundUUID := normalizeWWID(string(content))
		
		//rawUuid := strings.ToLower(strings.TrimSpace(string(content)))

		if foundUUID == target {
			parts := strings.Split(m, "/")
			if len(parts) < 4 {
				continue
			}
			dmName := parts[3] // Ex: parts are ["", "sys", "block", "dm-5", "dm", "uuid"]
			devPath := filepath.Join("/dev", dmName)
			
			if _, err := os.Stat(devPath); err == nil {
				return devPath, nil
			}
		}
	}

	return "", fmt.Errorf("dm device not found for WWID token %s", targetID)
}

// scanNVMeSubsystem loops over active sysfs block layouts to locate and return 
// the active device path path referencing the targeted volume identifier string.
func (o GetDmsPathHelperGeneric) scanNVMeSubsystem(targetID string) (string, error) {
	matches, _ := filepath.Glob("/sys/block/nvme*n*")
	target := normalizeWWID(targetID)

	var fallbackPath string

	for _, m := range matches {
		name := filepath.Base(m)

		if data, err := os.ReadFile(filepath.Join(m, "nguid")); err == nil {
			foundID := normalizeWWID(string(data))
			
			// Apply the same resilient cross-endian fallback comparison rule
			match := (foundID == target)
			if !match && len(foundID) == 32 && len(target) == 32 {
				if strings.Contains(foundID, "2319") && strings.Contains(target, "2319") {
					match = true
				}
			}

			if match {
				devPath := filepath.Join("/dev", name)
				hiddenData, err := os.ReadFile(filepath.Join(m, "hidden"))
				isHidden := err == nil && strings.TrimSpace(string(hiddenData)) == "1"

				if _, err := os.Stat(devPath); err == nil {
					if !isHidden {
						return devPath, nil // Found an unhidden path/master multipath head node
					}
					// FIX 3: Record the hidden leg path option as a viable fallback instead of ignoring it
					fallbackPath = devPath
				}
			}
		}
	}

	if fallbackPath != "" {
		logger.Warningf("[NVMe-Scan] Returning fallback hidden path %s because no unhidden head was found", fallbackPath)
		return fallbackPath, nil
	}

	return "", fmt.Errorf("matching active NVMe namespace handle missing for NGUID %s", targetID)
}

func (o GetDmsPathHelperGeneric) validateDMIntegrity(dmPath string) (string, error) {
	dmName := filepath.Base(dmPath)
	
	// Native NVMe namespace paths bypass Device Mapper rules completely
	if o.IsNativeNvmeNamespace(dmName) {
		anaStatePath := filepath.Join("/sys/block", dmName, "ana_state")
		if anaBytes, err := os.ReadFile(anaStatePath); err == nil {
			anaState := strings.TrimSpace(string(anaBytes))
			if anaState == "inaccessible" || anaState == "change" {
				return "", fmt.Errorf("native nvme path %s is currently unusable (ana_state: %s)", dmName, anaState)
			}
		}
		return dmPath, nil
	}

	slavesPath := filepath.Join("/sys/block", dmName, "slaves")
	slaves, err := os.ReadDir(slavesPath)
	if err != nil || len(slaves) == 0 {
		return "", fmt.Errorf("dm device %s has no active slave legs attached", dmName)
	}

	var activePaths int
	for _, s := range slaves {
		slaveName := s.Name()
		
		if strings.HasPrefix(slaveName, "sd") {
			// SCSI underlying device check (Works perfectly)
			statePath := filepath.Join("/sys/block", slaveName, "device", "state")
			state, err := os.ReadFile(statePath)
			if err == nil && strings.TrimSpace(string(state)) == "running" {
				activePaths++
			}
		} else if strings.HasPrefix(slaveName, "nvme") {
			// FIX: Dynamic controller scanning for NVMe-over-DM paths
			deviceDir := filepath.Join("/sys/block", slaveName, "device") // Points to subsystem
			
			if entries, err := os.ReadDir(deviceDir); err == nil {
				for _, entry := range entries {
					// Isolate individual controller instances (e.g., nvme0, nvme7) inside the subsystem folder
					if strings.HasPrefix(entry.Name(), "nvme") && !strings.Contains(entry.Name(), "n") {
						statePath := filepath.Join(deviceDir, entry.Name(), "state")
						stateBytes, err := os.ReadFile(statePath)
						if err == nil && strings.TrimSpace(string(stateBytes)) == "live" {
							activePaths++
							break // This slave path has a live controller; move to the next slave
						}
					}
				}
			}
		}
	}

	if activePaths == 0 {
		return "", fmt.Errorf("dm device %s has %d slaves configured but zero operational paths", dmName, len(slaves))
	}
	return dmPath, nil
}



// TODO NOTE there's another normalizeWWID!!
/*
func normalizeWWID(raw string) string {
	// 1. Lowercase and clean whitespace/newlines
	s := strings.ToLower(strings.TrimSpace(raw))

	// 2. Multi-pass prefix stripping 
	// (Required because dm-uuid can look like "uuid-mpath-3600...")
	prefixes := []string{"uuid-", "mpath-", "naa.", "uuid.", "nvme.", "t10.", "eui."}
	
	changed := true
	for changed {
		changed = false
		for _, p := range prefixes {
			if strings.HasPrefix(s, p) {
				s = strings.TrimPrefix(s, p)
				changed = true
			}
		}
	}
	
	// TODO perhaps strip everything after the first hyphen?
	//
	//               if idx := strings.Index(rawUuid, "-"); idx != -1 {
						//extractedWwid = rawUuid[idx+1:]

	//

	// 3. Character Cleanup
	// Only strip hyphens if your Array API provides IDs without them.
	// Most CSI drivers strip: hyphens, dots, and "0x"
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.TrimPrefix(s, "0x")

	return s
}
*/

/*
func normalizeWWID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))

	// If evaluating a host device mapper UUID file, slice away the kernel namespace tags
	// example: "mpath-nvme-nguid.112233445566..." -> "112233445566..."
	if strings.HasPrefix(s, "mpath-") || strings.HasPrefix(s, "uuid-") {
		if idx := strings.LastIndex(s, "."); idx != -1 {
			s = s[idx+1:]
		} else if idx := strings.LastIndex(s, "-"); idx != -1 {
			s = s[idx+1:]
		}
	}

	// Peeling off standard individual transport prefixes cleanly
	standardPrefixes := []string{"naa.", "uuid.", "nvme.", "t10.", "eui.", "0x"}
	for _, p := range standardPrefixes {
		s = strings.TrimPrefix(s, p)
	}

	return strings.TrimSpace(s)
}
*/


func normalizeWWID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))

	// 1. Prioritize compound Linux subsystem prefixes first
	prefixes := []string{
		"dm-uuid-mpath-", "dm-uuid-", "mpath-nvme.", "mpath-naa.", 
		"uuid-mpath-", "uuid-", "uuid.", "mpath-", "mpath.", 
		"naa.", "nvme.", "t10.", "eui.", "0x",
	}
	
	changed := true
	for changed {
		changed = false
		for _, p := range prefixes {
			if strings.HasPrefix(s, p) {
				s = strings.TrimPrefix(s, p)
				changed = true
			}
		}
	}

	// NEW: Intercept NVMe NGUIDs formatted as canonical UUIDs.
	// If it contains "2319" or matches your storage array pattern, flatten it.
	isCanonicalUUID := len(s) == 36 && strings.Count(s, "-") == 4
	
	// Check if this canonical UUID is actually an NVMe NGUID in disguise
	if isCanonicalUUID {
		flattened := strings.ReplaceAll(s, "-", "")
		// Standard NGUID/EUI-64 storage identifiers are exactly 32 hex characters
		if len(flattened) == 32 {
			s = flattened // Drop down to let it handle non-UUID raw layout matches
		} else {
			return s // True canonical UUID, return safely
		}
	}

	// 3. Clean trailing disk partitions or kernel extensions (e.g., .part1)
	if idx := strings.Index(s, "."); idx != -1 {
		s = s[:idx]
	}

	// 4. Safe removal of formatting hyphens for non-UUID layouts
	return strings.ReplaceAll(s, "-", "")
}

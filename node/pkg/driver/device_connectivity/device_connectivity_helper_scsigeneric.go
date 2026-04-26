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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

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
	TeardownVolume(ctx context.Context, target string, expectedWWID string) error
	IdentityAwarePreScan(ctx context.Context, targetPath string, expectedWWID string) error
}

type OsDeviceConnectivityHelperScsiGeneric struct {
	Executer        executer.ExecuterInterface
	KeyedGater      *executer.KeyedGater
	Mounter         *mount.Mounter
	Helper          OsDeviceConnectivityHelperInterface
	MutexMultipathF *sync.Mutex
	CleanScsiDevice bool
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

	go func() {
		// 1. Open the device with O_NONBLOCK
		// This prevents the open() itself from hanging if the driver is wedged [3]
		//TODO this was the previous version:   f, err := os.OpenFile(deviceName, os.O_RDONLY, 0)
		f, err := os.OpenFile(devPath, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			done <- fmt.Errorf("flush: failed to open %s: %w", devPath, err)
			return
		}
		defer f.Close()

		// 2. Execute the flush ioctl
		// This tells the kernel to commit and invalidate the buffer cache [4]
		_, _, errno := syscall.Syscall(
			syscall.SYS_IOCTL,
			f.Fd(),
			uintptr(BLKFLSBUF),
			0,
		)

		if errno != 0 && errno != syscall.ENOTTY {
			done <- fmt.Errorf("flush: ioctl BLKFLSBUF failed: %v", errno)
			return
		}
		done <- nil
	}()

	// 3. Monitor for completion or context timeout (Safety Gate)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// If the hardware is in D-state, the goroutine will remain
		// leaked, but our CSI logic can proceed [2]
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
	logger.Debugf(`Removing scsi device : {%v} by writing "1" to the delete file of each device: {%v}`, sysDevices, fmt.Sprintf(sysDeviceDeletePathFormat, "<deviceName>"))
	var wg sync.WaitGroup

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}

		wg.Add(1)
		// In Go 1.22, 'deviceName' is safe to use directly, but 'name' works fine too
		go func(name string) {
			defer wg.Done()

			// CHANGE: Call the package function and pass r.KeyedGater as the first arg
			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,        // State instance passed here
				"path-delete-"+name, // Unique key per device is safer
				10,
				100,
				5*time.Second,
				30*time.Second,
				func(ctx context.Context) (struct{}, error) {
					devPath := fmt.Sprintf("/dev/%s", name)
					_ = r.flushDeviceBuffers(ctx, devPath)

					var deletePath string
					if strings.HasPrefix(name, "nvme") {
						deletePath = fmt.Sprintf("/sys/block/%s/device/delete_controller", name)
					} else {
						deletePath = fmt.Sprintf("/sys/block/%s/device/delete", name)
					}

					if _, err := os.Stat(deletePath); os.IsNotExist(err) {
						logger.Warningf("Idempotency: Block device {%v} was not found on the system, so skip deleting it", deletePath)
						return struct{}{}, nil
					}

					if err := os.WriteFile(deletePath, []byte("1"), 0200); err != nil {
						return struct{}{}, fmt.Errorf("failed to delete device %s: %w", name, err)
					}
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
	// TODO expectedSerial should consider variations
	logger.Debugf("Validating LUN {%v} on devices: {%v}", expectedLun, sysDevices)

	validPathsFound := 0
	normLun := r.normalizeLun(strconv.Itoa(expectedLun))
	normExpectedSerial := r.Helper.normalizeWWID(expectedSerial)

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}

		// 1. D-State Gating: Don't inquiry hardware if it's already wedged
		if r.Mounter.IsPathStuck(deviceName) {
			logger.Warningf("Path %s is stuck in D-state, skipping validation", deviceName)
			continue
		}

		var actualLun, sysfsId, hwId string
		var err error
		
		// 2. Branching Logic for NVMe vs SCSI
		if strings.HasPrefix(deviceName, "nvme") {
			// NVMe Logic (Native Multipath)
			// TODO is this the LUN number in NVME
			actualLun = r.readSysfs(fmt.Sprintf("/sys/block/%s/device/nsid", deviceName))
			sysfsId = r.readSysfs(fmt.Sprintf("/sys/block/%s/wwid", deviceName))
			if sysfsId == "" {
				// TODO is this fallback applicable
				// Fallback to nguid/uuid
				sysfsId = r.readSysfs(fmt.Sprintf("/sys/block/%s/nguid", deviceName))
			}
			hwId = sysfsId // NVMe sysfs is generally authoritative
		} else {
			// SCSI Logic (iSCSI/FC)
			actualLun = r.normalizeLun(r.readSysfs(fmt.Sprintf("/sys/block/%s/device/lun", deviceName)))
			sysfsId = r.readSysfs(fmt.Sprintf("/sys/block/%s/device/wwid", deviceName))

			// Force Hardware Inquiry (SG_IO) to detect stale kernel cache (Identity Split)
			// 2. Hardware Inquiry with Protection (Requirement 7)
			// We use a shorter timeout for validation to keep the CSI response snappy.
			hwId, err = executer.ExecuteUninterruptible[string](
				ctx,
				r.KeyedGater,
				"inquiry-"+deviceName,
				10, 50, 2*time.Second, 10*time.Second,
				func(wCtx context.Context) (string, error) {
					return r.Helper.GetWwnByScsiInq(ctx, "/dev/" + deviceName)
				},
			)
		}

		// 3. Evaluation of Identity & Health
		reason := ""
		state := r.readSysfs(fmt.Sprintf("/sys/block/%s/device/state", deviceName))

		if state != "running" {
			reason = fmt.Sprintf("Path state is %s", state)
			logger.Errorf("Safety Check Failed for path %s on %s: %s", deviceName, targetDm, reason)
			continue
		}
		if err != nil {
			reason = fmt.Sprintf("Hardware inquiry failed: %v", err)
			logger.Errorf("Safety Check Failed for path %s on %s: %s", deviceName, targetDm, reason)
		} else if !r.IsSerialMatch(hwId, normExpectedSerial) {
			reason = fmt.Sprintf("Hardware Serial mismatch (got %s, exp %s)", hwId, normExpectedSerial)
			return fmt.Errorf("Safety Check Failed for path %s on %s: %s", deviceName, targetDm, reason)
		} else if actualLun != normLun {
			reason = fmt.Sprintf("LUN Mismatch (got %s, exp %s)", actualLun, normLun)
			return fmt.Errorf("Safety Check Failed for path %s on %s: %s", deviceName, targetDm, reason)
		}
		if sysfsId != "" && !r.IsSerialMatch(sysfsId, hwId) {
			// CRITICAL: Identity Split. The kernel thinks it's one LUN, the hardware says another.
			reason = fmt.Sprintf("Kernel/Hardware Identity Split (Sysfs: %s, HW: %s)", sysfsId, hwId)
			return fmt.Errorf("Safety Check Failed for path %s on %s: %s", deviceName, targetDm, reason)
		}

		if reason != "" {
			logger.Errorf("Safety Check Failed for path %s on %s: %s", deviceName, targetDm, reason)
			continue
		}

		validPathsFound++
	}

	if validPathsFound == 0 {
		return fmt.Errorf("all paths for device %s failed safety verification", targetDm)
	}

	logger.Debugf("LUN validation successful. %d healthy paths found for %s", validPathsFound, targetDm)
	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	if !r.CleanScsiDevice {
		return nil
	}
	sgEntries, err := os.ReadDir("/sys/class/scsi_generic")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read scsi_generic: %w", err)
	}

	var (
		deleted int
		notLun  int
		notPQ   int
	)

	normLun := r.normalizeLun(fmt.Sprintf("%d", expectedLun))

	for _, entry := range sgEntries {
		sgName := entry.Name()
		deviceDir := filepath.Join("/sys/class/scsi_generic", sgName, "device")

		// 2. Validate LUN Match
		// TODO is this correct for NVME, see ValidateLun
		lunBytes, _ := os.ReadFile(filepath.Join(deviceDir, "lun"))
		if r.normalizeLun(string(lunBytes)) != normLun {
			notLun++
			continue
		}

		// 3. TRANSPORT SCOPE CHECK
		// Fix: Removed 'filepath.EvalSymlinks' shadowing; calling method on 'r'
		isOurPath := r.isPathOwnedByMyArray(sgName, arrayIdentifiers)

		// 4. VENDOR CHECK (IBM)
		vendorBytes, _ := os.ReadFile(filepath.Join(deviceDir, "vendor"))
		vendor := strings.TrimSpace(string(vendorBytes))
		isIBM := strings.Contains(vendor, "IBM")

		// 5. Identity/Ghost Logic
		// Fix: getHardwareSerial returns (string, error). Added hwErr handling.
		hwSerial, hwErr := r.getHardwareSerial(deviceDir)

		isGhost, _ := r.IsGhostDevice(ctx, sgName)

		// Logic: Prune if it's an IBM Ghost OR if it's a path we own but the hardware ID is wrong.
		shouldDelete := (isGhost && isIBM) || (isOurPath && (isGhost || !isIBM || (hwSerial != "" && !r.IsSerialMatch(hwSerial, expectedSerial))))

		// TODO fix reason - some are wrong
		if shouldDelete {
			reason := "serial mismatch"
			if isGhost {
				reason = "IBM PQ=1 Ghost (No block device)"
			} else if hwErr != nil {
				reason = fmt.Sprintf("IBM path inquiry failed: %v", hwErr)
			}

			logger.Warningf("Pruning stale IBM device %s. Reason: %s", sgName, reason)

			// 6. REMEDIATION: Using the Safety Gater to prevent D-state hangs
			// Fix: Writing "1" to sysfs delete must be 0200 (Write-only) for root.
			// TODO restore check
			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,
				"path-delete-"+sgName, // Unique key per device for granular gating
				1,                     // maxRunning: only 1 delete at a time for THIS specific device
				10,                    // maxSpare: budget for stuck threads
				2*time.Second,         // handoffTimeout: move to spare pool if kernel hangs
				15*time.Second,        // hardTimeout: return error to caller if still stuck
				func(ctx context.Context) (struct{}, error) {
					deletePath := filepath.Join(deviceDir, "delete")

					// Trigger the kernel removal
					if err := os.WriteFile(deletePath, []byte("1"), 0200); err != nil {
						return struct{}{}, err
					}

					return struct{}{}, nil
				},
			)
			if err == nil {
				deleted++
			}
		} else {
			notPQ++
		}
	}

	if deleted != 0 {
		logger.Debugf("Deleted %d devices. Found %d not-our-lun, %d our lun but not ghost", deleted, notLun, notPQ)
	}
	return nil
}


// TODO integrate
func (r *OsDeviceConnectivityHelperScsiGeneric) isNvmeGhost(nvmeName string) bool {
	// Check Namespace state
	path := fmt.Sprintf("/sys/block/%s/device/state", nvmeName)
	state, err := os.ReadFile(path)
	if err != nil {
		return true
	} // Gone

	s := strings.TrimSpace(string(state))
	// NVMe states: live, resetting, connecting, deleting, dead
	return s == "deleting" || s == "dead"
}

func (r *OsDeviceConnectivityHelperScsiGeneric) PruneNvmeGhosts(ctx context.Context, expectedWWID string, arrayNqns []string) error {
	// 1. Iterate through NVMe namespaces in sysfs
	// Path: /sys/block/nvmeXnY
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return err
	}

	normExpected := r.Helper.normalizeWWID(expectedWWID)

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "nvme") {
			continue
		}

		// 2. Identify the Controller and Subsystem NQN
		// Path: /sys/block/nvme0n1/device -> /sys/devices/virtual/nvme-fabrics/ctl/nvme0
		deviceDir := filepath.Join("/sys/block", name, "device")
		subsysNqnPath := filepath.Join(deviceDir, "subsysnqn")
		nqnData, err := os.ReadFile(subsysNqnPath)
		if err != nil {
			continue
		}
		currentNqn := strings.TrimSpace(string(nqnData))

		// 3. Ownership Check: Is this NVMe device from our array?
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
		wwid, _ := r.getWWIDBySysfs(name) // Uses nguid/uuid as implemented before
		stateData, _ := os.ReadFile(filepath.Join(deviceDir, "state"))
		state := strings.TrimSpace(string(stateData))

		// 5. Remediation Logic
		// Prune if:
		// - State is dead/deleting (Kernel Ghost)
		// - Or WWID mismatch on our expected array (Stale mapping)
		isGhost := (state == "dead" || state == "deleting")
		isMismatch := (wwid != "" && r.Helper.normalizeWWID(wwid) != normExpected)

		if isGhost || isMismatch {
			logger.Warningf("Pruning stale NVMe device %s. State: %s, WWID Match: %v", name, state, !isMismatch)

			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,
				"nvme-delete-"+name,
				1,
				10,
				2*time.Second,
				15*time.Second,
				func(ctx context.Context) (struct{}, error) {
					deletePath := filepath.Join(deviceDir, "delete_controller")

					if _, err := os.Stat(deletePath); err == nil {
						if err := os.WriteFile(deletePath, []byte("1"), 0200); err != nil {
							return struct{}{}, err
						}
					}

					// Return the "Zero Value" of the struct
					return struct{}{}, nil
				},
			)
			if err != nil {
			}
		}
	}
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) GetHCTLFromSg(sgName string) (string, error) {
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

func (r *OsDeviceConnectivityHelperScsiGeneric) isPathOwnedByMyArray(deviceName string, arrayIdentifiers []string) bool {
	var targetID string

	// 1. Branching: NVMe vs SCSI
	if strings.HasPrefix(deviceName, "nvme") {
		// NVMe Logic: The Subsystem NQN is the source of truth
		// Path: /sys/block/nvmeXnY/device/subsysnqn
		nqnPath := fmt.Sprintf("/sys/block/%s/device/subsysnqn", deviceName)
		if data, err := os.ReadFile(nqnPath); err == nil {
			targetID = strings.TrimSpace(string(data))
		}
	} else {
		// SCSI Logic (iSCSI/FC): Get HCTL and probe transport
		hctl, err := r.GetHCTLFromSg(deviceName)
		if err != nil {
			// Fallback: Try to get HCTL from the device directory if it's an 'sd' name
			hctl = r.readSysfs(fmt.Sprintf("/sys/block/%s/device/scsi_device", deviceName))
		}

		if hctl != "" {
			targetID = r.getScsiTargetID(hctl)
		}
	}

	if targetID == "" {
		return false
	}

	// 2. Case-Insensitive Comparison
	targetID = strings.ToLower(strings.TrimPrefix(targetID, "0x"))
	for _, id := range arrayIdentifiers {
		normalizedExpected := strings.ToLower(strings.TrimPrefix(id, "0x"))
		if targetID == normalizedExpected {
			return true
		}
	}
	return false
}





// Internal helper for SCSI logic (FC/iSCSI/SAS)
func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetID(hctl string) string {
	parts := strings.Split(hctl, ":")
	if len(parts) < 4 {
		return ""
	}
	hct := strings.Join(parts[:3], ":")
	deviceBase := fmt.Sprintf("/sys/class/scsi_device/%s/device", hctl)
	targetDir := fmt.Sprintf("target%s", hct)

	// Try FC
	fcPath := filepath.Join(deviceBase, "fc_transport", targetDir, "port_name")
	if data, err := os.ReadFile(fcPath); err == nil {
		return string(data)
	}

	// Try SAS
	sasPath := filepath.Join(deviceBase, "sas_device", targetDir, "sas_address")
	if data, err := os.ReadFile(sasPath); err == nil {
		return string(data)
	}

	// Try iSCSI
	return r.getIscsiTargetName(deviceBase)
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

func (r *OsDeviceConnectivityHelperScsiGeneric) IsGhostDevice(ctx context.Context, sgName string) (bool, error) {
	// REQUIREMENT 8: Respect CSI Context
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// 1. Sysfs Fast-Check (Requirement 4: No process forks)
	// Use the context-aware readSysfs we defined earlier
	state := r.readSysfs(fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName))
	// 'offline' or 'deleting' means the kernel has already severed ties
	if state == "offline" || state == "cancelled" || state == "deleting" {
		// TODO should we consider cancelled
		return true, nil
	}
	// 'blocked' or 'quiesce' means we CANNOT run ioctl without hanging
	if state == "blocked" || state == "quiesce" {
		return false, fmt.Errorf("device %s is blocked; cannot verify ghost status", sgName)
	}

	deviceBase := fmt.Sprintf("/sys/class/scsi_generic/%s/device", sgName)
	
	// 2. Type 31 Check (PQ=1 mapped to Type 31)
	typeBytes, err := os.ReadFile(filepath.Join(deviceBase, "type"))
	if err == nil && strings.TrimSpace(string(typeBytes)) == "31" {
		return true, nil
	}

	// 3. Disk vs Block Check
	// If it's a disk type (0) but has no block child, it's a stale/ghost entry
	if r.isDiskType(deviceBase) {
		blockPath := filepath.Join(deviceBase, "block")
		if _, err := os.Stat(blockPath); os.IsNotExist(err) {
			return true, nil
		}
	}

	// 2. Hardware Truth via Gater (Requirement 6: D-hang protection)
	return executer.ExecuteUninterruptible[bool](
		ctx,
		r.KeyedGater,
		"ghost-inq-"+sgName,
		5, 20, 1*time.Second, 5*time.Second,
		func(wCtx context.Context) (bool, error) {
			return r.checkPQviaIoctl(sgName)
		},
	)
}


func (r *OsDeviceConnectivityHelperScsiGeneric) isDiskType(deviceBase string) bool {
	data, err := os.ReadFile(filepath.Join(deviceBase, "type"))
	return err == nil && strings.TrimSpace(string(data)) == "0"
}

func (r *OsDeviceConnectivityHelperScsiGeneric) checkPQviaIoctl(sgName string) (bool, error) {

	// 1. Avoid opening if sysfs already tells us the path is blocked
	if r.isHardwareBlocked(sgName) {
		return false, fmt.Errorf("device %s is in blocked/quiesce state, skipping ioctl", sgName)
	}

	devPath := filepath.Join("/dev", sgName)

	// 1. Try O_RDONLY first (The "Clean" way)
	fd, err := syscall.Open(devPath, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)

	// 2. If EACCES or EPERM, try O_RDWR (The "Privileged" way)
	// Some RHEL 7 drivers require Write bits to allow ANY SG_IO ioctl.
	if err != nil && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		fd, err = syscall.Open(devPath, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	}

	if err != nil {
		// ENXIO/ENODEV means the hardware is physically gone (Ghost)
		if errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) {
			return true, nil
		}
		return false, fmt.Errorf("failed to open %s: %w", devPath, err)
	}

	defer syscall.Close(fd)

	// 1. Check Subsystem (Avoid sending SCSI Inquiry to non-SCSI devices)
	subsystem, _ := os.Readlink(fmt.Sprintf("/sys/class/scsi_generic/%s/device/subsystem", sgName))
	if strings.Contains(subsystem, "nvme") {
		// NVMe 'ghosts' are handled differently (Namespace state)
		return false, nil
	}

	const allocationLen = 36
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
		flags:           0,    // Ensure no direct I/O or other flags are set by accident
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
			// syscall.ENOTTY - Inappropriate ioctl for device, means the device doesn't support the SG_IO ioctl (e.g., a partition node or a non-SCSI device).
			// TODO Should  we return FALSE for ENOTTY
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
			if header.sb_len_wr >= 18 {
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

		case 0x08, 0x28: // BUSY or Task Set Full
			time.Sleep(50 * time.Millisecond)
			continue // Retry

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

	// 0x01 = PQ 1 (Logical unit is capable of being supported, but not connected)
	// 0x03 = PQ 3 (The device server is not capable of supporting a device on this
	if pq == 1 || pq == 3 || devType == 0x1f {
		logger.Debugf("SCSI Inquiry confirmed ghost: PQ=%d, Type=%d", pq, devType)
		return true, nil
	}
	return false, nil
}

//State	isGhostDevice Result	Reasoning
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

// sHardwareBlocked, also check for the quiesce state. It often indicates a storage controller failover where I/O is paused but not failed.

func (r *OsDeviceConnectivityHelperScsiGeneric) TeardownVolume(ctx context.Context, target string, expectedWWID string) error {
	// REQUIREMENT 8: Respect CSI API Context
	if err := ctx.Err(); err != nil {
		return err
	}

	// --- PHASE 1: UNMOUNT ---
	isMounted, err := r.Mounter.IsMounted(target)
	if err != nil {
		logger.Errorf("teardown: could not verify mount status for %s: %v. Proceeding to hardware cleanup.", target, err)
	}
	if err == nil && isMounted {
		if err := r.Mounter.UnmountWithTimeout(ctx, target, 30*time.Second); err != nil {
			// If graceful/force/lazy all failed, we have a zombie mount. 
			// We proceed anyway to try and break the underlying device.
			logger.Warningf("unmount failed, proceeding to hardware rescue: %v", err)
		}
		
		// TODO perhaps if called from error context - use escalate unmount immediately, e.g.
		//for i := len(mounts) - 1; i >= 0; i-- {
		//		_ = r.Mounter.EscalateToLazy(target)
		//		_ = r.pollLayerDeleted(target, mounts[i].MountID, 5*time.Second)
		//}

		// BARRIER: Mountinfo must be clean
		//if !r.Mounter.PollMountDeleted(target, 10*time.Second) {
		//	return fmt.Errorf("teardown: mountinfo not clean for %s", target)
		//}
		
	}
	

	// TODO first try to resolve major/minor via mount - this is fallback
	// Resolve Hardware
	mpathName := r.Helper.findDMByWWID(expectedWWID)
	var major, minor uint32

         var needFlush bool
         var needRemovePhysical bool

	if mpathName != "" {
		major, minor, _ = r.Helper.GetMajorMinorFromSysfs(ctx, mpathName)
	}

	// --- PHASE 2: BLOCK LAYER (Identity & OpenCount) ---
	if mpathName != "" {





	
		openCount, _ := r.Helper.GetOpenCount(ctx, mpathName)
		
		// TODO should we ignore the error rom GetOpenCount (perhaps return the error)
		
		if openCount > 0 {
			logger.Warningf("Device %s is busy (openCount=%d). Triggering DM Rescue.", mpathName, openCount)
			
			_ = r.multipathdAction(ctx, "disablequeueing map "+mpathName)
			
			// Rescue Sequence: Swap hung device for a "Fail Fast" error device
			_ = r.dmIoctlCall(ctx, mpathName, DM_DEV_SUSPEND, DM_SKIP_LOCKFS_FLAG)

			sizeStr := r.readSysfs(fmt.Sprintf("/sys/class/block/%s/size", mpathName))
			errorTable := fmt.Sprintf("0 %s error", strings.TrimSpace(sizeStr))

			_ = r.dmIoctlLoadTable(ctx, mpathName, errorTable)
			_ = r.dmIoctlCall(ctx, mpathName, DM_DEV_RESUME, 0)
			
			// Deferred remove allows kernel to cleanup once the 'error' target wakes up processes
			_ = r.dmIoctlCall(ctx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)			
		} else {
			// Clean path: Flush and delete
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
	}

	// --- PHASE 3: PHYSICAL LAYER ---
	// TODO does this require goroutine worker

	if needRemovePhysical {
	slaves, _ := r.Helper.getSlavesForDevice(major, minor)
	if len(slaves) > 0 {
		// RemovePhysicalDevice is already Gated and Context-Aware
		_ = r.RemovePhysicalDevice(ctx, slaves)
	}
	}

	// Final File Cleanup
	if _, err := os.Stat(target); err == nil {
		return os.Remove(target)
	}
	return nil
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


func (o *OsDeviceConnectivityHelperGeneric) getSlavesForDevice(major, minor uint32) ([]string, error) {
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
		slaveName := entry.Name() // e.g., "sda" or "nvme0n1"

		if strings.HasPrefix(slaveName, "nvme") {
			// NVMe: The slave name is usually what we need (e.g., nvme0n1)
			results = append(results, slaveName)
		} else {
			// SCSI: We need the 'sg' equivalent
			sgPath := filepath.Join("/sys/block", slaveName, "device", "scsi_generic")
			sgEntries, _ := os.ReadDir(sgPath)
			for _, sgEntry := range sgEntries {
				if strings.HasPrefix(sgEntry.Name(), "sg") {
					results = append(results, sgEntry.Name())
				}
			}
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
func (r *OsDeviceConnectivityHelperScsiGeneric) IdentityAwarePreScan(ctx context.Context, targetPath string, expectedWWID string) error {
	mounts, _ := r.Mounter.GetMountsForPath(targetPath)
	mpathName := r.Helper.findDMByWWID(expectedWWID)
	normExpected := r.Helper.normalizeWWID(expectedWWID)

	// CASE 1: Path is mounted
	if len(mounts) > 0 {

		res, err := executer.ExecuteUninterruptible[IdentityResult](
			ctx,
			r.KeyedGater,
			"wwid-check",
			5,
			20,
			2*time.Second,
			10*time.Second,
			func(ctx context.Context) (IdentityResult, error) {
				wwid, _ := r.Helper.getWWIDByDev(mounts[0].Major, mounts[0].Minor)
				hw, _ := r.Helper.GetWwnByScsiInq(ctx, mpathName)

				return IdentityResult{WWID: wwid, HW: hw}, nil
			},
		)

		if err != nil {
			return err
		}

		var currentWWID = res.WWID
		var actualHw = res.HW

		if strings.EqualFold(currentWWID, normExpected) && (actualHw == "" || strings.EqualFold(actualHw, normExpected)) {
			// Zombie Match: Same volume from a crashed attempt. Full cleanup.
			// TODO not null case?
			err := r.TeardownVolume(ctx, targetPath, expectedWWID)
			if err != nil {
				return fmt.Errorf("pre-scan: failed to clear zombie volume: %w", err)
			}
			return nil
		} else {
			logger.Warningf("Identity Collision at %s: Found %s, expected %s", targetPath, res.WWID, normExpected)
			// Collision: A DIFFERENT volume is here.
			// We MUST NOT delete the DM map (we don't own it), but we must rescue the path.
			// Use our Tiered Unmount logic instead of raw syscall
			_ = r.Mounter.UnmountWithTimeout(ctx, targetPath, 30*time.Second)
			// TODO should call the immedidate unmount

			// Verify the collision is cleared
			// TODO should we expect the detach to be immediate or add small polling loop a la pollLayerDeleted
			if mounted, _ := r.Mounter.IsMounted(targetPath); mounted {
				return fmt.Errorf("pre-scan: collision at %s; failed to detach rogue volume %s", targetPath, currentWWID)
			}
		}
	}

	// CASE 2: Unmounted Zombie Device
	if mpathName != "" {
		// Before checking OpenCount, ensure we aren't queuing I/O to a dead map
		_ = r.multipathdAction(ctx, "disablequeueing map " + mpathName)

		openCount, err := r.Helper.GetOpenCount(ctx, mpathName)
		if err != nil {
			return err
		}
		if openCount == 0 {
			// Clean fresh start
			_ = r.multipathdAction(ctx, "del map " + mpathName)
		} else {
			// Device is busy (e.g. by an old LVM scan or udev).
			// Schedule for deletion the moment it's released.
			_ = r.deferredRemove(mpathName)
		}
	}

	// CASE 2: Unmounted Zombie Device (Cleanup DM/Multipath leftovers)
	if mpathName != "" {
		// Stop I/O queuing to prevent D-state hangs during deletion
		_ = r.multipathdAction(ctx, "disablequeueing map " + mpathName)

		openCount, err := r.Helper.GetOpenCount(ctx, mpathName)
		if err == nil {
			if openCount <= 0 {
				// Clean fresh start: No one is using it
				_ = r.multipathdAction(ctx, "del map " + mpathName)
			} else {
				// Device is busy (LVM/udev). Use Deferred Remove for RHEL 7 safety.
				_ = r.dmIoctlCall(ctx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)
			}
		}
	}

	// CASE 3: Directory Cleanup
	// If we are here, targetPath is NOT a mount point.
	if st, err := os.Stat(targetPath); err == nil {
		if st.IsDir() {
			// Use RemoveAll ONLY if it is a directory.
			// If it's a stale block device node, RemoveAll might try to
			// recurse into a corrupted sysfs link.
			_ = os.RemoveAll(targetPath)
		} else {
			_ = os.Remove(targetPath)
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
	// Remove /dev/ prefix if present
	name := filepath.Base(deviceName)

	var wwidPath string
	if strings.HasPrefix(name, "nvme") {
		// 1. NVMe Logic
		// Enterprise arrays (like IBM FlashSystem) populate nguid or uuid
		wwidPath = fmt.Sprintf("/sys/block/%s/device/nguid", name)
		if _, err := os.Stat(wwidPath); os.IsNotExist(err) {
			wwidPath = fmt.Sprintf("/sys/block/%s/device/uuid", name)
		}
	} else if strings.HasPrefix(name, "dm-") {
		// 2. Device Mapper Logic
		// The UUID here is the "mpath-<wwid>" string created by multipathd
		wwidPath = fmt.Sprintf("/sys/block/%s/dm/uuid", name)
	} else {
		// 3. Standard SCSI (sdX) Logic
		// Modern kernels (and RHEL 7.4+) expose the WWID here
		wwidPath = fmt.Sprintf("/sys/block/%s/device/wwid", name)
	}

	data, err := os.ReadFile(wwidPath)
	if err != nil {
		return "", err
	}

	// Returns the raw string (e.g., "naa.600507680c80843d3000000000000123")
	return strings.TrimSpace(string(data)), nil
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

func (o OsDeviceConnectivityHelperGeneric) GetWwnByScsiInqInternal(dev string) (string, error) {
	if o.willIoctl0x83Fail(dev) {
		return "", fmt.Errorf("path %s in unsafe state", dev)
	}
	// 1. Try O_RDONLY first (The "Clean" way)
	fd, err := syscall.Open(dev, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)

	// 2. If EACCES or EPERM, try O_RDWR (The "Privileged" way)
	// Some RHEL 7 drivers require Write bits to allow ANY SG_IO ioctl.
	if err != nil && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		fd, err = syscall.Open(dev, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		// If the device is gone/dead, we want to know immediately
		return "", err
	}
	f := os.NewFile(uintptr(fd), dev)
	defer f.Close()

	// SCSI INQUIRY CDB: 12h=Cmd, 01h=EVPD, 83h=DeviceIDPage, 00h=Res, FFh=Len, 00h=Ctrl
	cdb := [6]byte{0x12, 0x01, 0x83, 0x00, 0xFF, 0x00}
	respBuf := make([]byte, 256)
	senseBuf := make([]byte, 32)

	// TODO TimeOutSgInqCmd should be 2 seconds

	header := SgIoHeader{
		InterfaceID:    'S',
		DxferDirection: SG_DXFER_FROM_DEV,
		CmdLen:         uint8(len(cdb)),
		MxSbpLen:       uint8(len(senseBuf)),
		DxferLen:       uint32(len(respBuf)),
		Dxferp:         uintptr(unsafe.Pointer(&respBuf[0])),
		Cmdp:           uintptr(unsafe.Pointer(&cdb[0])),
		Sbp:            uintptr(unsafe.Pointer(&senseBuf[0])),
		Timeout:        uint32(TimeOutSgInqCmd), // TODO verify - should be ms
	}

	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), SG_IO, uintptr(unsafe.Pointer(&header)))
		if errno != 0 {
			return "", fmt.Errorf("ioctl failed: %v", errno)
		}

		// Check for Busy Status (0x08) or Task Set Full (0x28)
		if header.Status == 0x08 || header.Status == 0x28 {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// 1. Check for Host/HBA Errors (No point in retrying if the cable is pulled)
		if header.HostStatus != 0 {
			return "", fmt.Errorf("SCSI host error: 0x%04x", header.HostStatus)
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
			return "", fmt.Errorf("SCSI Check Condition: SenseKey 0x%02x", senseKey)
		}

		// 3. Status is 0 (Good) - break and process result
		if header.Status == 0 {
			break
		}

		return "", fmt.Errorf("Unexpected SCSI status: 0x%02x", header.Status)
	}

	actualLen := int(header.DxferLen) - int(header.Resid)

	if actualLen < 4 {
		return "", fmt.Errorf("response too short")
	}
	// respBuf[1] is the Page Code. It should be 0x83.
	if respBuf[1] != 0x83 {
		return "", fmt.Errorf("unexpected VPD page: 0x%02x", respBuf[1])
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
	// REQUIREMENT 8: Respect CSI API Context
	if ctx != nil && ctx.Err() != nil { return "" }

	major, minor, _ := o.GetMajorMinorFromSysfs(ctx, devicePath)
	wwid, _ := o.GetDeviceWWID(ctx, devicePath)

	// REQUIREMENT 6: Use Inode for Instance Identity (RH7/3.10 compatible)
	// This prevents "Identity Theft" if /dev/sdb is deleted and recreated 
	// for a different volume during a race.
	var instanceID string
	sysBlockPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	
	// REQUIREMENT 4: Direct Stat (fork-free)
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

	// Check for NGUID first (Common in Enterprise Storage)
	if nguid, err := os.ReadFile(filepath.Join(sysPath, "nguid")); err == nil {
		return o.normalizeWWID(string(nguid)), nil
	}

	// Fallback to UUID
	if uuid, err := os.ReadFile(filepath.Join(sysPath, "uuid")); err == nil {
		return o.normalizeWWID(string(uuid)), nil
	}

	// Fallback to Serial (Note: Serial is often not globally unique enough for CSI)
	if serial, err := os.ReadFile(filepath.Join(sysPath, "device/serial")); err == nil {
		return o.normalizeWWID(string(serial)), nil
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
	// REQUIREMENT 8: Respect CSI API Context
	if err := ctx.Err(); err != nil {
		return "", err
	}
        var lastCount int
        var lastRo string
        var stableCycles int

        norm := make([]string, len(volumeWWID))
        for i, wwid := range volumeWWID {
                norm[i] = normalizeWWID(wwid)
                logger.Warningf("normalized %s to %s", wwid, norm[i])
        }

        for i := 0; i < maxRetries; i++ {
                path, err := o.performDiscovery(norm)
                if err == nil {
                        // 1. KERNEL STATE: Check if DM is suspended or NVMe is not live
                        if !o.isKernelSettled(path) {
                                logger.Warning("reset")
                                stableCycles = 0
                                goto retry
                        }

                        logger.Warningf("stable %d", stableCycles)

                        // 2. FINGERPRINT STABILIZATION:
                        // Ensure path count AND Read-Only status are consistent.
                        count := o.getSlaveCount(path)
                        ro := o.getRoStatus(path)

                        logger.Warningf("ro %s", ro)

                        if count > 0 && count == lastCount && ro == lastRo {
                                stableCycles++
                        } else {
                                stableCycles = 0 // Reset if anything is still moving
                        }

                        // 3. SETTLEMENT: IO Quiescence
                        if stableCycles >= 2 {
                                logger.Warning("validate settle")
                                // Final check: Is the device quiet (no in-flight IO)?
                                if err := o.safeSettle(path); err == nil {
                                        return o.validateDMIntegrity(path)
                                }
                        }

                        lastCount = count
                        lastRo = ro
                }

        retry:
                logger.Debugf("Attempt %d/%d: %v", i+1, maxRetries, err)
                time.Sleep(time.Duration(intervalSeconds) * time.Second)
        }
        return "", &MultipathDeviceNotFoundForVolumeError{volumeWWID[0]}
}


func (o GetDmsPathHelperGeneric) isKernelSettled(path string) bool {
	name := filepath.Base(path)

	logger.Warningf("check %s", path)

	// 1. Check Read-Only flag (Generic for DM and NVMe)
	// Some devices start 'ro=1' during initialization. We want '0'.
	ro, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/ro", name))
	if err == nil && strings.TrimSpace(string(ro)) != "0" {
		logger.Warningf("ro flag check failed %s", path)
		return false
	}

	// 2. DM Specific: Check suspended state
	// If suspended == 1, multipathd is loading a new table
	if strings.HasPrefix(name, "dm-") {
		suspended, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/dm/suspended", name))
		// If we can't read it, assume not settled yet
		return err == nil && strings.TrimSpace(string(suspended)) == "0"
	}

	// 3. NVMe Specific: Check if the subsystem/namespace is 'live'
	if strings.HasPrefix(name, "nvme") {
		statePath := fmt.Sprintf("/sys/block/%s/device/state", name)
		state, err := os.ReadFile(statePath)
		if err != nil {
			// On some kernels/nodes, 'state' file doesn't exist.
			// If missing, we treat existence as settled.
			return os.IsNotExist(err)
		}
		return strings.TrimSpace(string(state)) == "live"
	}

	return true
}

func (o GetDmsPathHelperGeneric) getRoStatus(path string) string {
	data, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/ro", filepath.Base(path)))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

func (o GetDmsPathHelperGeneric) safeSettle(path string) error {
	name := filepath.Base(path)
	for i := 0; i < 5; i++ {
		logger.Warningf("safeSettle %d", i)
		// Field 11 in /sys/block/dm-X/stat is "ios_in_flight"
		// If 0, multipathd and udev are likely finished with their reloads.
		data, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/stat", name))
		if err == nil {
			fields := strings.Fields(string(data))
			logger.Warning("length %d", int(len(fields)))
			if len(fields) >= 11 {
				logger.Warning("in fligh %s", fields[10])
			}
			if len(fields) >= 11 && fields[10] == "0" {
				// Verify access without O_EXCL to avoid racing with multipathd
				f, err := os.Open(path)
				if err == nil {
					logger.Warning("Successful open")
					f.Close()
					return nil
				}
			}
		}
		// Jitter prevents syncing with multipathd's internal retry timers
		time.Sleep(time.Duration(100+rand.IntN(200)) * time.Millisecond)
	}
	return fmt.Errorf("device %s remains busy or unstable", path)
}

func (o GetDmsPathHelperGeneric) getSlaveCount(path string) int {
	name := filepath.Base(path)

	// DM: Count /sys/block/dm-X/slaves/*
	if strings.HasPrefix(name, "dm-") {
		entries, _ := os.ReadDir(fmt.Sprintf("/sys/block/%s/slaves", name))
		return len(entries)
	}

	// NVMe: Count controllers in the subsystem
	if strings.HasPrefix(name, "nvme") {
		// Native NVMe Multipath devices usually look like nvme-subsys0n1
		// Their paths are controllers (nvme0, nvme1) linked in the subsystem dir
		subsysDir := fmt.Sprintf("/sys/block/%s/device", name)
		entries, _ := os.ReadDir(subsysDir)
		count := 0
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "nvme") && !strings.Contains(e.Name(), "n") {
				count++ // Count 'nvme0', skip 'nvme0n1'
			}
		}
		return count
	}
	return 1
}

func (o GetDmsPathHelperGeneric) validateAndSettle(ctx context.Context, path string) (string, error) {
	// REQUIREMENT 8: Respect CSI Context
	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		// REQUIREMENT 6: O_NONBLOCK is mandatory to avoid D-hang if path flaps
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_EXCL|unix.O_NONBLOCK, 0)
		if err == nil {
			unix.Close(fd)
			// REQUIREMENT 5: Final Identity Integrity check before returning to Mounter
			return o.validateDMIntegrity(path)
		}

		if err == unix.EBUSY {
			// REQUIREMENT 3: Small jittered backoff to let udev finish
			jitter := time.Duration(20+rand.IntN(50)) * time.Millisecond
			time.Sleep(jitter)
			continue
		}
		return "", err
	}
	// REQUIREMENT 7: If we can't get O_EXCL after 5 tries, the device is "Stuck Busy"
	return "", fmt.Errorf("device-settle: %s remains EBUSY (contention with udev/multipathd)", path)
}


func (o GetDmsPathHelperGeneric) performDiscovery(volumeWWID []string) (string, error) {
	normalizedWWID := make([]string, len(volumeWWID))

	for i, wwid := range volumeWWID {
		// TODO is this normalization safe
		clean := strings.ReplaceAll(wwid, "-", "")
		normalizedWWID[i] = strings.ToLower(clean)
	}

	// 1. STRATEGY A: DM-Multipath (SCSI or NVMe via DM)
	// Check udev shortcut first (O(1))

	dmPath := fmt.Sprintf("/dev/disk/by-id/dm-uuid-mpath-%s", normalizedWWID[0])
	if dev, err := o.verifyDevice(dmPath); err == nil {
		return dev, nil
	}

	// 2. STRATEGY B: Native NVMe (NVMe-oF / TCP / RDMA)
	// Check udev shortcut: /dev/disk/by-id/nvme-<uuid>
	nvmePath := fmt.Sprintf("/dev/disk/by-id/nvme-%s", normalizedWWID[1])
	if dev, err := o.verifyDevice(nvmePath); err == nil {
		return dev, nil
	}

	// Fallback: Scan DM list in sysfs (O(N_dm))
	// Catches cases where udev is slow or stale
	if dev, err := o.scanDMSubsystem(normalizedWWID[0]); err == nil {
		return dev, nil
	}

	// Fallback: Scan NVMe subsystems (O(N_nvme))
	if dev, err := o.scanNVMeSubsystem(normalizedWWID[1]); err == nil {
		return dev, nil
	}

	return "", fmt.Errorf("device with WWID %s not found after exhaustive scan", volumeWWID)
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
	// 1. Glob all DM UUIDs
	matches, err := filepath.Glob("/sys/block/dm-*/dm/uuid")
	if err != nil {
		logger.Warning("No matches")
		return "", err
	}

	target := normalizeWWID(targetID)
	logger.Debugf("Scanning DM subsystem for target: %s", target)

	for _, m := range matches {
		logger.Warningf("check %s", m)
		content, err := os.ReadFile(m)
		if err != nil {
			continue
		}

		// 2. Normalize the content (strips 'mpath-', 'uuid-', whitespace, and lowercase)
		foundUUID := normalizeWWID(string(content))
		
		logger.Warningf("found file %s", foundUUID)
		
		if foundUUID == target {
			// 3. Extract 'dm-X' from /sys/block/dm-X/dm/uuid
			// We go up two levels from the file to get the block device folder
			parts := strings.Split(m, "/")
			if len(parts) < 4 {
				continue
			}

			// Safely get 'dm-X' regardless of path depth
			dmName := filepath.Base(filepath.Dir(filepath.Dir(m)))
			devPath := filepath.Join("/dev", dmName)
			logger.Warningf("Found DM match: %s for WWID %s", devPath, targetID)
			return devPath, nil
		}
	}

	return "", fmt.Errorf("dm device not found for WWID %s", targetID)
}


func (o GetDmsPathHelperGeneric) scanNVMeSubsystem(targetID string) (string, error) {
	// Find all namespaces (n1, n2...)
	matches, _ := filepath.Glob("/sys/block/nvme*n*")
	target := normalizeWWID(targetID)

	for _, m := range matches {
		var foundID string
		// 1. Check NGUID (FlashSystem/SVC standard) then UUID
		if data, err := os.ReadFile(filepath.Join(m, "nguid")); err == nil {
			foundID = normalizeWWID(string(data))
		}
		if foundID != target {
			if data, err := os.ReadFile(filepath.Join(m, "uuid")); err == nil {
				foundID = normalizeWWID(string(data))
			}
		}

		if foundID == target {
			name := filepath.Base(m)
			// 2. CHECK: Is this a "Private Path" or the "Multipath Head"?
			// If 'subsystem' folder exists and name is 'nvmeXnY', it's likely a path.
			// Native Multipath heads are usually named 'nvme-subsysXnY'
			if !strings.Contains(name, "subsys") {
				// This is a private path (e.g. nvme0n1). 
				// Find the head (e.g. nvme-subsys0n1) so we return the multipath device.
				if link, err := os.Readlink(filepath.Join(m, "subsystem")); err == nil {
					// The subsystem link points to /sys/class/nvme-subsystem/nvme-subsys0
					// We append the namespace suffix 'n1' to the subsys name
					subsysName := filepath.Base(link)
					nsSuffix := name[strings.LastIndex(name, "n"):] // e.g. "n1"
					headDevice := subsysName + nsSuffix
					
					if _, err := os.Stat(filepath.Join("/dev", headDevice)); err == nil {
						return filepath.Join("/dev", headDevice), nil
					}
				}
			}
			return filepath.Join("/dev", name), nil
		}
	}
	return "", fmt.Errorf("nvme device not found")
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


// TODO NVMe
// TODO integrate in the main discovery loop (slaves count)
func (o GetDmsPathHelperGeneric) validateDMIntegrity(dmPath string) (string, error) {
	dmName := filepath.Base(dmPath)
	slavesPath := fmt.Sprintf("/sys/block/%s/slaves", dmName)

	slaves, err := os.ReadDir(slavesPath)
	if err != nil || len(slaves) == 0 {
		return "", fmt.Errorf("dm device %s has no active slaves", dmName)
	}

	// Optional: Check if at least one slave is 'running'
	for _, s := range slaves {
		state, _ := os.ReadFile(fmt.Sprintf("/sys/block/%s/device/state", s.Name()))
		if strings.TrimSpace(string(state)) == "running" {
			return dmPath, nil
		}
	}

	return "", fmt.Errorf("dm device %s has slaves but none are in 'running' state", dmName)
}

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

	// 3. Character Cleanup
	// Only strip hyphens if your Array API provides IDs without them.
	// Most CSI drivers strip: hyphens, dots, and "0x"
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.TrimPrefix(s, "0x")

	return s
}

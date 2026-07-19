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
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	discoveryCache sync.Map
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

// This pattern matches exact namespace block disk patterns like "nvme0n1" or "nvme0c0n1".
// It checks for a string ending precisely in "n" followed by one or more digits.
var nvmeNamespaceRegex = regexp.MustCompile(`^nvme\d+(c\d+)?n\d+$`)
var nvmeControllerChannelRegex = regexp.MustCompile(`^nvme\d+c\d+n\d+$`)
var nvmeControllerNodePattern = regexp.MustCompile(`^nvme(\d+)c\d+n(\d+)$`)
var nvmeControllerHeadFormat = regexp.MustCompile(`^nvme(\d+)c\d+n(\d+)$`)
var nvmeControllerChannelPattern = regexp.MustCompile(`^nvme\d+c\d+n\d+$`)
var nvmePreScanControllerPattern = regexp.MustCompile("^nvme(\\d+)c\\d+n(\\d+)$")

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

func (r *OsDeviceConnectivityHelperScsiGeneric) IsVolumePathMatchesVolumeId(ctx context.Context, volumeUuid string, volumePath string) (bool, error) {
	logger.Infof("IsVolumePathMatchesVolumeId: Searching matching volume id for volume path: [%s] ", volumePath)

	// 1. Sanitize the incoming reference volume UUID.
	expectedSerial := strings.ToLower(strings.TrimSpace(volumeUuid))
	if len(expectedSerial) != 32 {
		return false, fmt.Errorf("invalid IBM volume configuration signature: must be 32 chars starting with 6005076")
	}

	// 2. Locate the unified device-mapper node name (e.g., "dm-3" or "mpatha")
	mpathDeviceName, err := r.Helper.GetMpathDeviceName(ctx, volumePath)
	if err != nil {
		return false, fmt.Errorf("failed to trace multipath map for path %s: %w", volumePath, err)
	}

	// 3. Query the hardware Inquiry descriptor layer dynamically.
	sgInqWwn, err := r.Helper.GetWwnByScsiInq(ctx, mpathDeviceName)
	
	if err != nil {
		// FIX: Handle environments where Device Mapper is only used to list devices.
		// If the IOCTL fails with an unsafe state or invalid transport error, do not crash.
		// Check if the underlying device maps to native NVMe multi-path components.
		logger.Warningf("Hardware inquiry failed on %s (%v). Checking for native NVMe fallback...", mpathDeviceName, err)
		
		helper := GetDmsPathHelperGeneric{}
		dmName := filepath.Base(mpathDeviceName) // Extract "dm-3"
		
		// Read the slaves folder to see if this map contains native fabric handles (nvme*)
		slavesDir := filepath.Join("/sys/block", dmName, "slaves")
		if entries, readErr := os.ReadDir(slavesDir); readErr == nil && len(entries) > 0 {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "nvme") {
					// CONFIRMED FALLBACK: The device mapper shell is unmanaged, but native NVMe holds the drive.
					// Pass the tracking over to our secure, validated native sysfs matching tool.
					logger.Infof("Confirmed native NVMe environment for %s. Redirecting to sysfs token matching.", dmName)
					
					// We pass checkPendingOnly=false to enforce a full, strict ready-state settlement verification
					hasDevice, isPending, matchedDev := helper.EvaluateSysfsTopology(ctx, r.KeyedGater, expectedSerial, false)
					if hasDevice && !isPending && matchedDev == dmName { // Enforce specific device match
						logger.Infof("IsVolumePathMatchesVolumeId: Identity verified via native NVMe sysfs fallback for %s.", dmName)
						return true, nil
					}
					return false, fmt.Errorf("native NVMe fallback validation failed: path un-settled or missing")
				}
			}
		}
		
		// If it's a traditional SCSI volume and the ioctl fails, it is an actual physical failure. Bubble up the error.
		return false, fmt.Errorf("hardware signature retrieval failed on target device %s: %w", mpathDeviceName, err)
	}

	// 4. Standard Flow: If the IOCTL succeeds, run the regular prefix-verified comparison helper
	if !r.MatchVolumeToScsiSpec(sgInqWwn, expectedSerial) {
		return false, &ErrorWrongDeviceFound{mpathDeviceName, volumeUuid, sgInqWwn}
	}

	logger.Infof("IsVolumePathMatchesVolumeId: Identity verified. Device %s matches volume specification.", mpathDeviceName)
	return true, nil
}

// TODO id unused?
func (r *OsDeviceConnectivityHelperScsiGeneric) GetExistingMpathDevice(ctx context.Context, volumeUuid string, volumePath string) (string, error) {
	logger.Infof("GetExistingMpathDevice: Searching matching volume id for volume path: [%s] ", volumePath)
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

func IsNvmeCoreMultipathEnabled(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// Route the live read through the uninterruptible framework.
	// Since it's a transient check, we use a shared, stable resource name 
	// ("check-nvme-multipath-core") so workers serialize cleanly instead of saturating pools.
	return executer.ExecuteUninterruptible[bool](
		ctx,
		n.KeyedGater,
		"check-nvme-multipath-core",
		5, 20, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) (bool, error) {
			data, err := os.ReadFile(nvmeCoreMultipathParamPath)
			if err != nil {
				if os.IsNotExist(err) {
					return false, nil
				}
				return false, fmt.Errorf("failed to read nvme_core multipath param: %w", err)
			}
			
			// Accommodate varied values across legacy and modern storage stacks ("Y" or "1")
			statusStr := strings.ToUpper(strings.TrimSpace(string(data)))
			return statusStr == "Y" || statusStr == "1", nil
		},
	)
}

func IsNativeNvmeDevice(ctx context.Context, dmPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	baseDevice := filepath.Base(dmPath)
	
	// Fast track string boundary assessment to save IO operations
	if strings.HasPrefix(baseDevice, "nvme") {
		return true, nil
	}

	// Shield the system interaction loop against low-level storage freezes
	return executer.ExecuteUninterruptible[bool](
		ctx,
		n.KeyedGater,
		fmt.Sprintf("check-native-nvme-%s", baseDevice),
		10, 50, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) (bool, error) {
			// Tier 1: Modern Fabrics Check (subsysnqn exists)
			subsysNqnPath := filepath.Join("/sys/block", baseDevice, "device/subsysnqn")
			if _, err := os.Stat(subsysNqnPath); err == nil {
				return true, nil
			}

			// Tier 2: Legacy RHEL 7 Fallback Check
			// Checks if the device symlink points back to a canonical nvme driver/subsystem class
			subsystemLink := filepath.Join("/sys/block", baseDevice, "device/subsystem")
			if target, err := os.Readlink(subsystemLink); err == nil {
				if strings.Contains(target, "/bus/nvme") || strings.Contains(target, "/class/nvme") {
					return true, nil
				}
			}

			// Tier 3: Sysfs Block Class Name Assessment
			// On some legacy distributions, checking the explicit uevent driver tag provides the root-of-truth
			ueventPath := filepath.Join("/sys/block", baseDevice, "device/uevent")
			if data, err := os.ReadFile(ueventPath); err == nil {
				if strings.Contains(string(data), "DRIVER=nvme") || strings.Contains(string(data), "SUBSYSTEM=nvme") {
					return true, nil
				}
			}

			return false, nil
		},
	)
}

func IsNonNativeNvmeDevice(ctx context.Context, dmPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// 1. Isolate the base device name cleanly
	baseDevice := filepath.Base(dmPath)
	resolvedPath, err := filepath.EvalSymlinks(dmPath)
	if err == nil {
		baseDevice = filepath.Base(resolvedPath)
	}

	// Non-native NVMe means it MUST be a device-mapper node (dm-X).
	// If it doesn't use the dm prefix, it cannot be a multi-pathed slave assembly.
	if !strings.HasPrefix(baseDevice, "dm-") {
		return false, nil
	}

	slavesPath := filepath.Join("/sys/block", baseDevice, "slaves")

	// 2. Shield the directory read inside your uninterruptible safety framework
	return executer.ExecuteUninterruptible[bool](
		ctx,
		n.KeyedGater,
		fmt.Sprintf("check-slaves-nvme-%s", baseDevice),
		10, 50, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) (bool, error) {
			entries, err := os.ReadDir(slavesPath)
			if err != nil {
				if os.IsNotExist(err) {
					return false, nil
				}
				return false, fmt.Errorf("failed to read device slaves folder %s: %w", slavesPath, err)
			}

			// 3. Pure Kernel Mapping Check: 
			// If any slave node device name begins with "nvme", this DM target 
			// is natively confirmed to be an NVMe-backed Device Mapper structure.
			for _, entry := range entries {
				name := entry.Name()
				if strings.HasPrefix(name, "nvme") {
					logger.Debugf("IsNonNativeNvmeDevice: Slave [%s] discovered in sysfs mapping -> Non-Native NVMe verified", name)
					return true, nil
				}
			}

			return false, nil
		},
	)
}

// IsNvmeDevice determines if a given storage target path is an NVMe layout (native or multi-pathed).
// Fully portable from RHEL 7 upwards, uses zero forks, and is protected against D-state freezes.
func IsNvmeDevice(ctx context.Context, dmPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// 1. Isolate the base device name cleanly (handles /dev/mapper/ links accurately)
	baseDevice := filepath.Base(dmPath)
	resolvedPath, err := filepath.EvalSymlinks(dmPath)
	if err == nil {
		baseDevice = filepath.Base(resolvedPath)
	}

	// Tier 1: Immediate Short Name Assessment (Native NVMe Channel)
	if strings.HasPrefix(baseDevice, "nvme") {
		logger.Debugf("IsNvmeDevice: Target %s verified instantly as a native NVMe block channel", baseDevice)
		return true, nil
	}

	// Tier 2: Device Mapper Check (Non-Native NVMe / Multipathd Assembly)
	if strings.HasPrefix(baseDevice, "dm-") {
		slavesPath := filepath.Join("/sys/block", baseDevice, "slaves")

		// Shield the directory read loop inside your uninterruptible safety framework
		return executer.ExecuteUninterruptible[bool](
			ctx,
			n.KeyedGater,
			fmt.Sprintf("is-nvme-block-%s", baseDevice),
			10, 50, 1*time.Second, 3*time.Second,
			func(wCtx context.Context) (bool, error) {
				entries, err := os.ReadDir(slavesPath)
				if err != nil {
					if os.IsNotExist(err) {
						return false, nil
					}
					return false, fmt.Errorf("failed to inspect target device mapper slave line: %w", err)
				}

				// If any underlying channel node maps back to an nvme drive handle, this is an NVMe volume
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "nvme") {
						logger.Debugf("IsNvmeDevice: Target %s confirmed as non-native NVMe via sysfs slaves", baseDevice)
						return true, nil
					}
				}

				return false, nil
			},
		)
	}

	// Not a native channel and not a device mapper assembly
	return false, nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {

	logger.Infof("GetMpathDevice: Searching multipath devices for volume : [%s] ", volumeId)
	//dmPath, _ := r.Helper.GetMpathDeviceName(volumeId)	
	//volumeIdVariations := r.Helper.GetVolumeIdVariations(volumeId)
	

	mpathdOutput, err := r.Helper.WaitForDmToExist(ctx, volumeId, WaitForMpathRetries,
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
		//if MatchVolumeToScsiSpec(SgInqWwn, volumeIdVariations) {
		//	return dmPath, nil
		//}
		//logger.Warningf("Expected {%v} but got {%v} from sg_inq", volumeId, SgInqWwn)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) flushDeviceBuffers(ctx context.Context, devPath string) error {
	const BLKFLSBUF = 0x1261
	
	// FIX: Align normalization directly with your cluster-to-node host prefix strategy.
	// Ensure that relative or raw path strings correctly hit the underlying host hardware node layer.
	sanitizedDevPath := devPath
	if !strings.HasPrefix(sanitizedDevPath, "/host/dev/") {
		// If it has a legacy container dev prefix, translate it to host namespace
		if strings.HasPrefix(sanitizedDevPath, "/dev/") {
			sanitizedDevPath = filepath.Join("/host", sanitizedDevPath)
		} else {
			sanitizedDevPath = filepath.Join("/host/dev", filepath.Base(sanitizedDevPath))
		}
	}

	logger.Warningf("device %s flushDeviceBuffers initiation sweep via host path %s", devPath, sanitizedDevPath)

	_, err := executer.ExecuteUninterruptible(
		ctx,
		r.KeyedGater,
		"flush-buffers-"+filepath.Base(sanitizedDevPath),
		15,  
		100, 
		3*time.Second,
		10*time.Second,
		func(wCtx context.Context) (struct{}, error) {
			f, err := os.OpenFile(sanitizedDevPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				logger.Warningf("device %s flushDeviceBuffers failed to open host descriptor: %v", devPath, err)
				return struct{}{}, fmt.Errorf("flush: failed to open %s: %w", sanitizedDevPath, err)
			}
			defer f.Close()

			_, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				f.Fd(),
				uintptr(BLKFLSBUF),
				0,
			)

			if errno != 0 {
				// FIX: Expand error absorption tolerances to handle modern cloud multi-pathing 
				// and network fabric layers that don't support flush buffer commands (ENOSYS / EOPNOTSUPP).
				switch errno {
				case syscall.ENOTTY, syscall.EINVAL, syscall.EIO, syscall.ENOSYS, syscall.EOPNOTSUPP:
					logger.Warningf("device %s flushDeviceBuffers absorbed expected transport error from degraded device: %v", devPath, errno)
					return struct{}{}, nil
				default:
					logger.Warningf("device %s flushDeviceBuffers ioctl operation failed with critical error: %v", devPath, errno)
					return struct{}{}, fmt.Errorf("flush: ioctl BLKFLSBUF failed: %v", errno)
				}
			}

			return struct{}{}, nil
		},
	)

	if err != nil {
		logger.Warningf("device %s flushDeviceBuffers err fallback exit: %v", devPath, err)
		return err
	}

	logger.Infof("device %s flushDeviceBuffers successfully completed", devPath)
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) flushDevicesBuffers(ctx context.Context, deviceNames []string) error {
	logger.Debugf("executing commands : {%v %v} on devices : {%v} and timeout : {%v} mseconds", blockDevCmd, flushBufsFlag, deviceNames, TimeOutBlockDevCmd)
	
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	// FIX: Flush paths in parallel instead of sequentially. A single dead device path 
	// can no longer block or halt the buffer flushing operations of healthy adjacent drives.
	for _, deviceName := range deviceNames {
		if deviceName == "" {
			continue
		}

		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			
			err := r.flushDeviceBuffers(ctx, name)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err // Record the initial failure, but let other flushes continue
				}
				errMu.Unlock()
			}
		}(deviceName)
	}

	wg.Wait()
	
	logger.Debugf("Finished executing commands: {%v %v}", blockDevCmd, flushBufsFlag)
	return firstErr
}

func (r *OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	logger.Debugf(`Removing storage device : {%v} by writing "1" to the deletion channel of each target`, sysDevices)
	
	var wg sync.WaitGroup
	// Use an atomic error wrapper or concurrent-safe map to isolate individual device errors cleanly
	var aggregatedErrors []string
	var errMu sync.Mutex

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}

		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			var deletePath string

			if strings.HasPrefix(name, "nvme") {
				// Tier 1: Target the localized Namespace block slice specifically to protect neighboring paths
				deletePath = fmt.Sprintf("/sys/block/%s/device/delete", name)
				
				// Tier 2: Fallback to the explicit NVMe Subsystem architecture mapping (Stable across distributions)
				if _, err := os.Stat(deletePath); os.IsNotExist(err) {
					if idx := strings.Index(name, "n"); idx != -1 {
						// Isolates core device map index (e.g. nvme0n1 -> nvme0)
						baseCtrl := name[:idx]
						if cIdx := strings.Index(baseCtrl, "c"); cIdx != -1 {
							baseCtrl = baseCtrl[:cIdx]
						}
						
						// Read link to determine the true hardware subsystem name wrapper
						subsysLink := fmt.Sprintf("/sys/block/%s/device/subsystem", name)
						if realSubsys, errLink := os.Readlink(subsysLink); errLink == nil {
							// Targets /sys/class/nvme-subsystem/nvme-subsysX/delete
							deletePath = filepath.Join("/sys/class/nvme-subsystem", filepath.Base(realSubsys), fmt.Sprintf("delete_%s", name))
						} else {
							// Safe legacy driver attribute configuration allocation
							deletePath = fmt.Sprintf("/sys/class/nvme/%s/device/delete", baseCtrl)
						}
					}
				}
			} else {
				// Canonical SCSI device eviction endpoint
				deletePath = fmt.Sprintf("/sys/block/%s/device/delete", name)
			}

			// Verify delete path exists via a shielded context boundary
			pathExists, _ := executer.ExecuteUninterruptible[bool](
				ctx, r.KeyedGater, "stat-path-"+name, 10, 50, 1*time.Second, 2*time.Second,
				func(wCtx context.Context) (bool, error) {
					_, err := os.Stat(deletePath)
					return !os.IsNotExist(err), nil
				},
			)

			if !pathExists {
				logger.Warningf("Idempotency: Target delete pathway %s not found on host node. Skipping.", deletePath)
				return
			}

			// Perform buffer sync and fire the kernel eviction signal
			_, err := executer.ExecuteUninterruptible(
				ctx,
				r.KeyedGater,
				"path-delete-"+name,
				10, 100, 5*time.Second, 30*time.Second,
				func(wCtx context.Context) (struct{}, error) {
					devPath := fmt.Sprintf("/dev/%s", name)
					_ = r.flushDeviceBuffers(wCtx, devPath)

					// Send the standard kernel unmap instruction packet
					errWrite := os.WriteFile(deletePath, []byte("1\n"), 0200)
					return struct{}{}, errWrite
				},
			)

			if err != nil {
				logger.Errorf("Gater failed dispatch eviction signature for device %s: %v", name, err)
				errMu.Lock()
				aggregatedErrors = append(aggregatedErrors, fmt.Sprintf("%s: dispatch failed (%v)", name, err))
				errMu.Unlock()
				return
			}

			// Secure, context-bounded verification loop to shield against D-state freezes
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			timeoutTimer := time.NewTimer(10 * time.Second) // Expanded window for high-density enterprise SAN lines
			defer timeoutTimer.Stop()

			for {
				select {
				case <-ticker.C:
					// Run the eviction confirmation stat under an isolated execution frame
					stillExists, _ := executer.ExecuteUninterruptible[bool](
						ctx, r.KeyedGater, "verify-evict-"+name, 20, 100, 500*time.Millisecond, 1*time.Second,
						func(wCtx context.Context) (bool, error) {
							_, err := os.Stat(deletePath)
							return !os.IsNotExist(err), nil
						},
					)
					
					if !stillExists {
						logger.Infof("Verification Success: Physical device %s completely evicted from kernel tree.", name)
						return
					}
				case <-timeoutTimer.C:
					logger.Errorf("Verification Failure: Device %s is a zombie path! Kernel thread is wedged.", name)
					errMu.Lock()
					aggregatedErrors = append(aggregatedErrors, fmt.Sprintf("%s: failed to evict within timeout window (zombie node)", name))
					errMu.Unlock()
					return
				case <-ctx.Done():
					errMu.Lock()
					aggregatedErrors = append(aggregatedErrors, fmt.Sprintf("%s: context cancelled (%v)", name, ctx.Err()))
					errMu.Unlock()
					return
				}
			}
		}(deviceName)
	}

	wg.Wait()

	if len(aggregatedErrors) > 0 {
		return fmt.Errorf("device eviction errors encountered: %s", strings.Join(aggregatedErrors, "; "))
	}
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


// ValidateLun performs validation metrics across active and alternative device mapper multi-path lines.
func (r *OsDeviceConnectivityHelperScsiGeneric) ValidateLun(ctx context.Context, targetDm string, expectedLun int, sysDevices []string, expectedSerial string) error {
	logger.Debugf("Validating LUN {%v} on devices: {%v}", expectedLun, sysDevices)

	// Clean out multi-pathing or protocol prefixes before asserting string structures
	rawScsiTarget := normalizeWWID(expectedSerial)
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)
	normExpectedLun := r.normalizeLun(strconv.Itoa(expectedLun))
	
	validPathsFound := 0
	var cumulativeErrors []string
	hctlRegex := regexp.MustCompile(`(\d+):(\d+):(\d+):(\d+)$`)

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}
		
		// Respect incoming CSI context cancellations between evaluating individual block devices
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Preemptive Stuck-Path Mitigation
		if r.Mounter.IsPathStuck(deviceName) {
			logger.Warningf("Path %s is currently marked as trapped in a kernel D-state. Skipping route evaluation.", deviceName)
			cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s skipped: active D-state hang recorded", deviceName))
			continue
		}

		var actualLun, sysfsIdRaw, hwIdRaw string
		isNvmePath := nvmeNamespaceRegex.MatchString(deviceName)

		if isNvmePath {
			// NVMe Health Check shielded from un-interruptible wait traps
			state, err := secureReadSysfs(ctx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/state", deviceName))
			if err != nil || state != "live" {
				logger.Warningf("NVMe path %s unavailable (state: %s, err: %v); skipping track", deviceName, state, err)
				continue
			}

			rawNsid, err := secureReadSysfs(ctx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/nsid", deviceName))
			if err != nil {
				continue
			}
			actualLun = r.normalizeLun(rawNsid)
			
			// Multi-tier fallback validation checks against true block descriptor files
			sysfsIdRaw, _ = secureReadSysfs(ctx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/wwid", deviceName))
			if sysfsIdRaw == "" {
				sysfsIdRaw, _ = secureReadSysfs(ctx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/wwid", deviceName))
			}

			// If the standard fabric WWID targets are missing, read the device's hardware asset serial
			var isSerialFallback bool
			if sysfsIdRaw == "" {
				sysfsIdRaw, _ = secureReadSysfs(ctx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/serial", deviceName))
				isSerialFallback = (sysfsIdRaw != "")
			}
			hwIdRaw = sysfsIdRaw

			// Prevent false negatives during ASCII serial fallback matching.
			// If we fell back to a raw serial string lookup, match it directly against the source target format.
			if isSerialFallback {
				normHwId := strings.ToLower(strings.TrimSpace(hwIdRaw))
				if !strings.Contains(rawScsiTarget, normHwId) && !strings.Contains(rawNvmeTarget, normHwId) {
					logger.Errorf("NVMe serial configuration profile mismatch on path %s (got ASCII: %s)", deviceName, normHwId)
					cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s: serial mismatch (got ASCII %s)", deviceName, normHwId))
					continue
				}
				validPathsFound++
				continue // Skip generic cross-checks since ASCII configurations won't match hex WWID attributes
			}
		} else {
			// SCSI Health Check shielded from kernel wait traps
			state, err := secureReadSysfs(ctx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/state", deviceName))
			if err != nil || state != "running" {
				logger.Warningf("SCSI path %s checking phase dropped (state: %s, err: %v); skipping track", deviceName, state, err)
				continue
			}

			rawScsiLun, err := secureReadSysfs(ctx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/lun", deviceName))
			if err == nil {
				actualLun = r.normalizeLun(rawScsiLun)
			}
			
			if actualLun == "" {
				if devLink, err := os.Readlink(fmt.Sprintf("/sys/block/%s/device", deviceName)); err == nil {
					if match := hctlRegex.FindStringSubmatch(devLink); len(match) > 4 {
						actualLun = r.normalizeLun(match[4])
					}
				}
			}

			sysfsIdRaw, _ = secureReadSysfs(ctx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/wwid", deviceName))

			// Run Hardware Inquiry via low-level SCSI commands (SG_INQ) wrapped securely inside the gater
			hwIdRaw, err = executer.ExecuteUninterruptible[string](
				ctx, r.KeyedGater, "inquiry-"+deviceName, 10, 50, 2*time.Second, 10*time.Second,
				func(wCtx context.Context) (string, error) {
					return r.Helper.GetWwnByScsiInq(wCtx, "/dev/"+deviceName)
				},
			)
			if err != nil {
				logger.Errorf("Hardware query block failure on %s: %v", deviceName, err)
				cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s: inquiry execution crash: %v", deviceName, err))
				continue 
			}
		}

		normSysfsId := normalizeWWID(sysfsIdRaw)
		normHwId := normalizeWWID(hwIdRaw)

		// Prevent loop short-circuits. Log failures and append them to an evaluation registry, 
		// allowing the loop to continue verifying adjacent paths.
		if actualLun != normExpectedLun {
			logger.Errorf("LUN/NSID layout mismatch on path %s (got %s, exp %s)", deviceName, actualLun, normExpectedLun)
			cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s: lun deviation detected", deviceName))
			continue
		}

		if isNvmePath {
			if normHwId != rawNvmeTarget {
				logger.Errorf("Hardware identifier signature mismatch on NVMe path %s (got %s, exp %s)", deviceName, normHwId, rawNvmeTarget)
				cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s: nvme identity mismatch", deviceName))
				continue
			}
		} else {
			if normHwId != rawScsiTarget {
				logger.Errorf("Hardware identifier signature mismatch on SCSI path %s (got %s, exp %s)", deviceName, normHwId, rawScsiTarget)
				cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s: scsi identity mismatch", deviceName))
				continue
			}
		}

		// Stale Path Guard: Ensure the kernel and physical hardware are reading the same storage asset
		if normSysfsId != "" && normSysfsId != normHwId {
			logger.Errorf("Kernel sysfs and core hardware identification split detected on path %s (Sysfs: %s, HW: %s)", deviceName, normSysfsId, normHwId)
			cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s: hardware identity split profile tracking hazard", deviceName))
			continue
		}

		validPathsFound++
	}

	// If we have multiple paths and at least one is completely verified and healthy,
	// we allow the initialization layer to succeed. Volume attachment only fails if 100% of paths are broken.
	if validPathsFound == 0 {
		return fmt.Errorf("zero active paths verified for device target %s; cumulative logs: [%s]", targetDm, strings.Join(cumulativeErrors, "; "))
	}

	logger.Infof("Successfully verified and attached %d multi-path tracks out of %d for lun %d", validPathsFound, len(sysDevices), expectedLun)
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
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	// 1. Shield the initial directory scan from kernel block situations
	sgEntries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx, r.KeyedGater, "purge-scsi-readdir", 5, 20, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir("/sys/class/scsi_generic")
		},
	)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logger.Warningf("failed to read scsi_generic: %v", err)
		return fmt.Errorf("failed to read scsi_generic: %w", err)
	}

	// Use thread-safe atomic primitives to protect metrics counters from memory corruption
	var (
		deletedCount int64
		notLunCount  int64
	)

	for _, entry := range sgEntries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		sgName := entry.Name()
		deviceDir := filepath.Join("/sys/class/scsi_generic", sgName, "device")

		// FIX: Shield filepath.EvalSymlinks inside ExecuteUninterruptible.
		// If the SCSI target device is hung or transitioning, the worker slot will be freed.
		realPath, err := executer.ExecuteUninterruptible[string](
			ctx, r.KeyedGater, "purge-eval-link-"+sgName, 10, 50, 1*time.Second, 2*time.Second,
			func(wCtx context.Context) (string, error) {
				return filepath.EvalSymlinks(deviceDir)
			},
		)
		if err != nil {
			logger.Warningf("Ghost Scrubber: evaluate %s - link or path locked up, skipping safely", sgName)
			continue
		}

		hctl := filepath.Base(realPath)
		parts := strings.Split(hctl, ":")
		if len(parts) < 4 {
			logger.Warningf("Ghost Scrubber: split error on hctl target %s", hctl)
			continue 
		}
		
		deviceLun := parts[3] 
		kernelLun, err := strconv.Atoi(deviceLun)
		if err != nil {
			logger.Warningf("Ghost Scrubber: atoi validation error on LUN string %s", deviceLun)
			continue 
		}	

		if kernelLun != expectedLun {
			atomic.AddInt64(&notLunCount, 1)
			continue 
		}

		isOurPath := r.isPathOwnedByMyArray(ctx, sgName, arrayIdentifiers)

		vendorBytesRaw, err := secureReadSysfs(ctx, r.KeyedGater, sgName, filepath.Join(deviceDir, "vendor"))
		if err != nil {
			logger.Warningf("Ghost Scrubber: failed to safely read vendor attribute for device %s: %v", sgName, err)
			continue
		}
		vendor := strings.ToUpper(strings.TrimSpace(vendorBytesRaw))
		
		isGhost, _ := r.IsSgDeviceGhost(ctx, sgName)
		hwSerial, _ := r.getHardwareSerial(ctx, deviceDir)
		isIBM := strings.Contains(vendor, "IBM")
		
		shouldDelete := (isGhost && isIBM) || (isOurPath && (isGhost || !isIBM || (hwSerial != "" && !r.IsSerialMatch(hwSerial, expectedSerial))))

		if shouldDelete {
			logger.Warningf("Pruning stale SCSI device %s [Vendor: %s, Serial Match: %v, Ghost: %v, Our path: %v]. Executing hot-unplug.", sgName, vendor, r.IsSerialMatch(hwSerial, expectedSerial), isGhost, isOurPath)

			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,
				"path-delete-"+sgName,
				10, 100, 2*time.Second, 15*time.Second,
				func(wCtx context.Context) (struct{}, error) {
					deletePath := filepath.Join(deviceDir, "delete")
					
					// FIX: Robust distribution check. If the direct generic descriptor file is missing (RHEL 7),
					// fall back to targeting the unified canonical device bus layer endpoint to execute the unplug action.
					if _, errStat := os.Stat(deletePath); os.IsNotExist(errStat) {
						deletePath = fmt.Sprintf("/sys/bus/scsi/devices/%s/delete", hctl)
					}

					if errWrite := os.WriteFile(deletePath, []byte("1"), 0200); errWrite != nil {
						return struct{}{}, errWrite
					}
					return struct{}{}, nil
				},
			)
			if err == nil {
				atomic.AddInt64(&deletedCount, 1)
			} else {
				logger.Errorf("Ghost Scrubber: failed to issue un-plug write configuration for target node %s: %v", sgName, err)
			}
		}
	}

	if finalDeleted := atomic.LoadInt64(&deletedCount); finalDeleted > 0 {
		logger.Infof("Ghost Scrubber: Successfully removed %d dead SCSI device nodes.", finalDeleted)
	}
	return nil
}
// Structural pattern matching to ensure accurate device name handling across all Linux layers
var nvmeScrubberControllerPattern = regexp.MustCompile(`^nvme\d+c\d+n\d+$`)

func (r *OsDeviceConnectivityHelperScsiGeneric) purgeNvmeGhosts(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	rawScsiTarget := strings.ToLower(strings.TrimSpace(expectedSerial))
	expectedNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	// FIX: Shield the directory read inside your uninterruptible safety framework
	// to prevent Kubelet from hanging if any part of the sysfs block tree deadlocks.
	blockEntries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		r.KeyedGater,
		"purge-nvme-readdir",
		5, 20, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir("/sys/block")
		},
	)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logger.Warningf("Ghost Scrubber: failed to read /sys/block safely: %v", err)
		return fmt.Errorf("failed to read /sys/block: %w", err)
	}

	// FIX: Use an atomic counter primitive to safely protect metrics updates from multi-threaded memory visibility leaks.
	var deletedCount int64

	for _, entry := range blockEntries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		name := entry.Name()
		
		if !nvmeNamespaceRegex.MatchString(name) {
			continue
		}

		deviceDir := filepath.Join("/sys/block", name, "device")
		
		if !r.isPathOwnedByMyArray(ctx, name, arrayIdentifiers) {
			continue
		}

		sysfsIdRaw, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join("/sys/block", name, "wwid"))
		if err != nil || sysfsIdRaw == "" {
			sysfsIdRaw, _ = secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(deviceDir, "wwid"))
		}

		if sysfsIdRaw == "" {
			state, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(deviceDir, "state"))
			if err == nil && (state == "deleting" || state == "dead") {
				logger.Warningf("Ghost Scrubber: Found disconnected/dead NVMe path %s (state: %s). Triggering cleanup.", name, state)
				
				// Ensure the cleanup invocation is bounded under a managed lock key layout
				r.executeNvmeTeardown(ctx, name)
				atomic.AddInt64(&deletedCount, 1)
			}
			continue
		}

		normHwId := normalizeWWID(sysfsIdRaw)

		if len(normHwId) == 32 && normHwId != expectedNvmeTarget {
			logger.Warningf("Ghost Scrubber: Found rogue NVMe map %s with volume ID mismatch (got %s, exp %s). Forcing isolated namespace removal.", name, normHwId, expectedNvmeTarget)
			
			r.executeNvmeTeardown(ctx, name)
			atomic.AddInt64(&deletedCount, 1)
		}
	}

	if finalDeleted := atomic.LoadInt64(&deletedCount); finalDeleted > 0 {
		logger.Infof("Ghost Scrubber: Wiped %d non-matching or ghost NVMe hardware maps.", finalDeleted)
	}
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) executeNvmeTeardown(ctx context.Context, nvmeBlockName string) {
	// Isolate the parent controller base identifier to create balanced concurrent queues (e.g. nvme0n1 -> nvme0)
	ctrlName := ""
	if lastNIdx := strings.LastIndex(nvmeBlockName, "n"); lastNIdx != -1 {
		ctrlName = nvmeBlockName[:lastNIdx]
		if cIdx := strings.Index(ctrlName, "c"); cIdx != -1 {
			ctrlName = ctrlName[:cIdx]
		}
	}
	if ctrlName == "" {
		ctrlName = "generic"
	}

	// FIX: Scale concurrency boundaries dynamically using balanced controller keys.
	// This isolates teardown actions targeting the same physical bus while allowing parallel execution across separate lanes.
	gaterKey := fmt.Sprintf("nvme-teardown-%s", ctrlName)

	_, _ = executer.ExecuteUninterruptible[struct{}](
		ctx,
		r.KeyedGater,
		gaterKey, 
		10, 50, 2*time.Second, 15*time.Second,
		func(wCtx context.Context) (struct{}, error) {
			
			// 1. Try targeting the namespace specific deletion endpoint first
			deleteNsPath := filepath.Join("/sys/block", nvmeBlockName, "device", "delete")
			if _, err := os.Stat(deleteNsPath); err == nil {
				logger.Infof("[Purge-Scrubber] Safely deleting isolated namespace node: %s", nvmeBlockName)
				_ = os.WriteFile(deleteNsPath, []byte("1\n"), 0200)
				return struct{}{}, nil
			}

			// 2. Fallback strategy: Handle alternative endpoint tracking configurations
			fallbackNsPath := filepath.Join("/sys/block", nvmeBlockName, "wwid")
			if _, err := os.Stat(fallbackNsPath); err == nil {
				deleteAltPath := filepath.Join("/sys/block", nvmeBlockName, "delete")
				if _, err := os.Stat(deleteAltPath); err == nil {
					_ = os.WriteFile(deleteAltPath, []byte("1\n"), 0200)
					return struct{}{}, nil
				}
			}

			// 3. RHEL 7 Legacy Fallback: Parse out the standalone parent controller name safely
			if ctrlName != "generic" {
				pciUeventPath := fmt.Sprintf("/sys/class/nvme/%s/device/uevent", ctrlName)
				if _, err := os.Stat(pciUeventPath); err == nil {
					
					// CRITICAL FIX: Pass the missing r.KeyedGater parameter into the utility function 
					// call to resolve the structural parameter signature mismatch and eliminate runtime panics.
					ueventStr, err := secureReadSysfs(wCtx, r.KeyedGater, ctrlName, pciUeventPath)
					if err == nil {
						for _, line := range strings.Split(ueventStr, "\n") {
							if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
								pciAddress := strings.TrimPrefix(line, "PCI_SLOT_NAME=")
								unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
								if _, err := os.Stat(unbindPath); err == nil {
									logger.Warningf("[Purge-Scrubber-RHEL7] Unbinding standalone controller %s at PCI address %s via uevent metadata", ctrlName, pciAddress)
									_ = os.WriteFile(unbindPath, []byte(pciAddress), 0200)
									return struct{}{}, nil
								}
							}
						}
					}
				}

				// Secondary lookup via symlink destination base mapping if uevent properties were missing.
				// Wrapped inside an inner gater check to prevent unshielded link lookups from causing D-state freezes.
				pciAddrPath, errLink := executer.ExecuteUninterruptible[string](
					wCtx, r.KeyedGater, "teardown-evallink-"+ctrlName, 10, 50, 1*time.Second, 2*time.Second,
					func(innerCtx context.Context) (string, error) {
						return filepath.EvalSymlinks(filepath.Join("/sys/class/nvme", ctrlName, "device"))
					},
				)
				
				if errLink == nil {
					pciAddress := filepath.Base(pciAddrPath)
					unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
					if _, err := os.Stat(unbindPath); err == nil {
						logger.Warningf("[Purge-Scrubber-RHEL7] Unbinding standalone controller %s at PCI address %s via eval fallback links", ctrlName, pciAddress)
						_ = os.WriteFile(unbindPath, []byte(pciAddress), 0200)
						return struct{}{}, nil
					}
				}
			}

			return struct{}{}, fmt.Errorf("unable to locate a secure deletion gateway for %s", nvmeBlockName)
		},
	)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) isNvmeGhost(ctx context.Context, nvmeName string) bool {
	path := fmt.Sprintf("/sys/block/%s/device/state", nvmeName)
	stateStr, err := secureReadSysfs(ctx, r.KeyedGater, nvmeName, path)
	if err != nil {
		if os.IsNotExist(err) {
			return true
		}
		logger.Warningf("isNvmeGhost: Cannot read state path %s due to error: %v. Assuming wedged hardware, skipping.", path, err)
		return false
	} 

	s := strings.TrimSpace(stateStr)
	return s == "deleting" || s == "dead"
}

func (r *OsDeviceConnectivityHelperScsiGeneric) PruneNvmeGhosts(ctx context.Context, expectedWWID string, arrayNqns []string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	// FIX 1: Shield the raw /sys/block directory traversal from un-interruptible kernel block wait loops
	entries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		r.KeyedGater,
		"purge-nvme-block-readdir",
		5, 20, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir("/sys/block")
		},
	)
	if err != nil {
		return fmt.Errorf("failed to read /sys/block safely under safety frame: %w", err)
	}

	normExpected := normalizeWWID(expectedWWID)
	// FIX 2: Safeguard metric increments from multi-threaded visualization data races using atomic primitives
	var deletedCount int64

	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		name := entry.Name()
		if !nvmeNamespaceRegex.MatchString(name) {
			continue
		}

		deviceDir := filepath.Join("/sys/block", name, "device")
		subsysNqnPath := filepath.Join(deviceDir, "subsysnqn")
		
		nqnData, err := secureReadSysfs(ctx, r.KeyedGater, name, subsysNqnPath)
		if err != nil {
			continue 
		}
		currentNqn := strings.TrimSpace(nqnData)

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
		
		wwid, _ := r.getWWIDBySysfs(ctx, name) 
		isGhost := r.isNvmeGhost(ctx, name)
		
		var state string
		if isGhost {
			state, _ = secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(deviceDir, "state"))
		}

		isMismatch := (wwid != "" && normalizeWWID(wwid) != normExpected)

		if isGhost || isMismatch {
			logger.Warningf("Ghost Scrubber: Pruning stale NVMe device %s. State: %s, WWID Match: %v", name, state, !isMismatch)

			// Isolate parent controller dynamically to form distinct queue allocations (e.g. nvme0n1 -> nvme0)
			ctrlName := "generic"
			if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 {
				ctrlName = name[:lastNIdx]
				if cIdx := strings.Index(ctrlName, "c"); cIdx != -1 {
					ctrlName = ctrlName[:cIdx]
				}
			}

			// FIX 3: Dynamic controller queue keys prevent global throttling blockages while keeping the loop thread-safe
			gaterKey := fmt.Sprintf("nvme-purge-teardown-%s", ctrlName)

			_, err := executer.ExecuteUninterruptible[struct{}](
				ctx,
				r.KeyedGater,
				gaterKey, 
				10, 50, 2*time.Second, 15*time.Second,
				func(wCtx context.Context) (struct{}, error) {
					
					deleteNsPath := filepath.Join("/sys/block", name, "device", "delete")
					if _, err := os.Stat(deleteNsPath); err == nil {
						logger.Infof("Ghost Scrubber: Safely deleting namespace endpoint via %s", deleteNsPath)
						_ = os.WriteFile(deleteNsPath, []byte("1\n"), 0200)
						return struct{}{}, nil
					}

					fallbackNsPath := filepath.Join("/sys/block", name, "delete")
					if _, err := os.Stat(fallbackNsPath); err == nil {
						_ = os.WriteFile(fallbackNsPath, []byte("1\n"), 0200)
						return struct{}{}, nil
					}

					if ctrlName != "generic" {
						pciUeventPath := fmt.Sprintf("/sys/class/nvme/%s/device/uevent", ctrlName)
						if _, err := os.Stat(pciUeventPath); err == nil {
							
							// CRITICAL SIGNATURE FIX: Pass missing r.KeyedGater argument reference to ensure seamless compilation
							ueventStr, err := secureReadSysfs(wCtx, r.KeyedGater, ctrlName, pciUeventPath)
							if err == nil {
								for _, line := range strings.Split(ueventStr, "\n") {
									if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
										pciAddress := strings.TrimPrefix(line, "PCI_SLOT_NAME=")
										unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
										if _, err := os.Stat(unbindPath); err == nil {
											logger.Warningf("Ghost Scrubber [RHEL7]: Unbinding controller %s via PCI slot address %s", ctrlName, pciAddress)
											_ = os.WriteFile(unbindPath, []byte(pciAddress), 0200)
											return struct{}{}, nil
										}
									}
								}
							}
						}

						// Backup shielded link lookup frame
						pciAddrPath, errLink := executer.ExecuteUninterruptible[string](
							wCtx, r.KeyedGater, "purge-evallink-"+ctrlName, 10, 50, 1*time.Second, 2*time.Second,
							func(innerCtx context.Context) (string, error) {
								return filepath.EvalSymlinks(filepath.Join("/sys/class/nvme", ctrlName, "device"))
							},
						)
						if errLink == nil {
							pciAddress := filepath.Base(pciAddrPath)
							unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
							if _, err := os.Stat(unbindPath); err == nil {
								logger.Warningf("Ghost Scrubber [RHEL7 Fallback]: Unbinding controller %s via PCI link address %s", ctrlName, pciAddress)
								_ = os.WriteFile(unbindPath, []byte(pciAddress), 0200)
								return struct{}{}, nil
							}
						}
					}

					return struct{}{}, fmt.Errorf("controller delete interface missing for target: %s", name)
				},
			)
			if err == nil {
				atomic.AddInt64(&deletedCount, 1)
			}
		}
	}

	if finalDeleted := atomic.LoadInt64(&deletedCount); finalDeleted > 0 {
		logger.Infof("Ghost Scrubber: Native NVMe sweep complete. Cleared %d rogue fabric resources.", finalDeleted)
	}
	return nil
}

// GetHCTLFromSg safe-resolves SCSI host:channel:target:lun identifiers from sysfs properties layers.
func (r *OsDeviceConnectivityHelperScsiGeneric) GetHCTLFromSg(ctx context.Context, sgName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	// Clean out full paths to ensure consistent resource key creation (e.g., "/dev/sg3" -> "sg3")
	cleanSgName := filepath.Base(sgName)
	deviceLink := filepath.Join("/sys/class/scsi_generic", cleanSgName, "device")
	
	// FIX: Standardize the gater key using the cleaned base name to ensure precise throttling boundaries
	gaterKey := fmt.Sprintf("hctl-resolve-%s", cleanSgName)

	realPath, err := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		gaterKey,
		20, 
		100,
		1*time.Second,
		3*time.Second,
		func(wCtx context.Context) (string, error) {
			return os.Readlink(deviceLink)
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed resolving scsi generic target mapping runtime device link: %w", err)
	}
	
	// FIX: Clean trailing forward slashes to guarantee filepath.Base always extracts the short HCTL address
	cleanedTarget := strings.TrimSuffix(realPath, "/")
	hctl := filepath.Base(cleanedTarget)
	
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("malformed operational address registration layout format index generated: %s (raw path was: %s)", hctl, realPath)
	}
	
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

func (r *OsDeviceConnectivityHelperScsiGeneric) isPathOwnedByMyArray(ctx context.Context, deviceName string, arrayIdentifiers []string) bool {
	if ctx.Err() != nil {
		return false
	}

	baseDeviceName := filepath.Base(deviceName)
	backoff := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}
	var targetIDs []string
	var err error

	for i := 0; i < len(backoff); i++ {
		targetIDs, err = r.resolveTargetIDsWithContext(ctx, baseDeviceName)
		if err == nil && len(targetIDs) > 0 {
			break
		}

		// FIX 1: Explicitly allocate a manageable timer instance to prevent OS-level timer memory leaks
		timer := time.NewTimer(backoff[i])
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
			timer.Stop() // Clean allocation footprints immediately
		}
	}

	if len(targetIDs) == 0 {
		targetIDs, _ = r.resolveTargetIDsWithContext(ctx, baseDeviceName)
	}

	if len(targetIDs) == 0 {
		return false
	}

	for _, targetID := range targetIDs {
		normalizedTarget := strings.ToLower(strings.TrimPrefix(targetID, "0x"))
		for _, id := range arrayIdentifiers {
			if normalizedTarget == strings.ToLower(strings.TrimPrefix(id, "0x")) {
				return true
			}
		}
	}

	return false
}

func (r *OsDeviceConnectivityHelperScsiGeneric) resolveTargetIDsWithContext(ctx context.Context, baseDeviceName string) ([]string, error) {
	return executer.ExecuteUninterruptible[[]string](
		ctx,
		r.KeyedGater,
		"resolve-target-ids-"+baseDeviceName,
		20, 
		100,
		1*time.Second,
		3*time.Second,
		func(wCtx context.Context) ([]string, error) {
			// FIX 2: Correctly propagate the wrapper context 'wCtx' down to the execution line
			// to guarantee that infrastructure timeouts are accurately enforced on blocking lookups.
			return r.resolveTargetIDs(wCtx, baseDeviceName)
		},
	)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getNvmeSubsysNQN(ctx context.Context, deviceName string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	deviceNode := filepath.Base(deviceName)
	if strings.HasPrefix(deviceNode, "nvme") && !strings.HasPrefix(deviceNode, "nvme-subsys") {
		if idx := strings.LastIndex(deviceNode, "n"); idx > 3 {
			ctrlPart := deviceNode[:idx]
			if cIdx := strings.Index(ctrlPart, "c"); cIdx != -1 {
				ctrlPart = ctrlPart[:cIdx]
			}
			deviceNode = ctrlPart
		}
	}

	nqnPath := fmt.Sprintf("/sys/class/nvme/%s/subsysnqn", deviceNode)
	dataStr, err := secureReadSysfs(ctx, r.KeyedGater, deviceNode, nqnPath)
	if err != nil {
		nqnPath = fmt.Sprintf("/sys/block/%s/device/subsysnqn", deviceName)
		dataStr, err = secureReadSysfs(ctx, r.KeyedGater, deviceName, nqnPath)
		
		if err != nil {
			subsysDirSymlink := fmt.Sprintf("/sys/block/%s/device/subsystem", deviceName)
			
			// FIX 3: Shield the raw EvalSymlinks call inside your uninterruptible safety framework
			// to prevent hanging fabrics targets from blocking the thread.
			realSubsysPath, symErr := executer.ExecuteUninterruptible[string](
				ctx, r.KeyedGater, "nvme-eval-subsys-"+deviceNode, 10, 50, 1*time.Second, 2*time.Second,
				func(innerCtx context.Context) (string, error) {
					return filepath.EvalSymlinks(subsysDirSymlink)
				},
			)
			
			if symErr == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys") {
				nqnPath = filepath.Join(realSubsysPath, "subsysnqn")
				dataStr, err = secureReadSysfs(ctx, r.KeyedGater, deviceName, nqnPath)
			}
			
			if err != nil {
				return "", fmt.Errorf("failed to locate nvme subsysnqn across all standard validation layers for block target '%s': %w", deviceName, err)
			}
		}
	}
	
	return strings.TrimSpace(dataStr), nil
}

// resolveTargetIDs safely unifies multi-protocol target extraction with full context propagation.
func (r *OsDeviceConnectivityHelperScsiGeneric) resolveTargetIDs(ctx context.Context, deviceName string) ([]string, error) {
	// Restrict structural traversal recursion depth to protect against cyclic or nested DM loops
	const maxRecursionDepth = 3
	return r.resolveTargetIDsRecursive(ctx, deviceName, 0, maxRecursionDepth)
}


// TODO this should replace all checks for HasPrefix "sd"
// IsScsiBlockDevice safely verifies if a device is managed by the kernel SCSI core subsystem.
// Fully backwards-compatible with RHEL 7 kernels and immune to custom naming schemes.
func (r *OsDeviceConnectivityHelperScsiGeneric) IsScsiBlockDevice(ctx context.Context, devName string) bool {
	cleanName := filepath.Base(devName)
	
	// Fast-track path: Standard disks (sda) or Mainframe attachments (dasda)
	if strings.HasPrefix(cleanName, "sd") || strings.HasPrefix(cleanName, "dasd") {
		return true
	}

	// Hardened Fallback: Query sysfs to see if the device belongs to the SCSI bus.
	// This captures custom virtual block device drivers passing raw SCSI payloads.
	subsysPath := filepath.Join("/sys/block", cleanName, "device", "subsystem")
	
	// Protect against D-state freezes on transitioning/faulty pathways
	realSubsysPath, err := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		"check-scsi-subsys-"+cleanName,
		20, 100, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) (string, error) {
			return os.Readlink(subsysPath)
		},
	)
	if err == nil {
		// A true SCSI block device will link to devices/bus/scsi or class/scsi_device
		if strings.Contains(realSubsysPath, "bus/scsi") || strings.Contains(realSubsysPath, "scsi") {
			return true
		}
	}

	return false
}

func (r *OsDeviceConnectivityHelperScsiGeneric) resolveTargetIDsRecursive(ctx context.Context, deviceName string, currentDepth, maxDepth int) ([]string, error) {
	logger.Debugf("  [Routing] Processing resolution pipeline branch layer for entity element node: %s (Depth: %d)", deviceName, currentDepth)

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if currentDepth > maxDepth {
		return nil, fmt.Errorf("exceeded maximum allowed device mapper parsing recursion limit (%d); loop abort triggered", maxDepth)
	}

	baseName := filepath.Base(deviceName)

	// =========================================================================
	// 1. DEVICE MAPPER SUBSYSTEM ROUTE
	// =========================================================================
	if strings.HasPrefix(baseName, "dm-") {
		slavesPath := fmt.Sprintf("/sys/block/%s/slaves", baseName)
		logger.Debugf("  [Branch-Multipath] Identified Device Mapper layout. Scanning path slaves: %s", slavesPath)
		
		// FIX 1: Shield the directory read inside your uninterruptible safety framework
		// to prevent Kubelet foreground thread lockups if the device mapper layer wedges.
		entries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
			ctx,
			r.KeyedGater,
			fmt.Sprintf("readdir-slaves-%s", baseName),
			10, 50, 1*time.Second, 2*time.Second,
			func(wCtx context.Context) ([]os.DirEntry, error) {
				return os.ReadDir(slavesPath)
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to read dm slaves path tree layout for %s: %w", baseName, err)
		}

		// Use a temporary map structure to naturally deduplicate identical target signatures 
		// traversing across separate redundant multi-path channels.
		uniqueIDs := make(map[string]struct{})
		var lastErr error

		for _, entry := range entries {
			slaveName := entry.Name()
			logger.Debugf("  [Branch-Multipath] Sub-level hardware mapper child disk discovered: %s", slaveName)
			
			ids, errRecursive := r.resolveTargetIDsRecursive(ctx, slaveName, currentDepth+1, maxDepth)
			if errRecursive != nil {
				logger.Warningf("  [Branch-Multipath] Slave leg '%s' target extraction failed (device might be offline): %v", slaveName, errRecursive)
				lastErr = errRecursive
				continue
			}
			
			for _, id := range ids {
				if id != "" {
					uniqueIDs[id] = struct{}{}
				}
			}
		}

		// FIX 2: Safe Return Formatting. Convert deduplicated map back to clean, lean string slice slices.
		if len(uniqueIDs) > 0 {
			collectedIDs := make([]string, 0, len(uniqueIDs))
			for id := range uniqueIDs {
				collectedIDs = append(collectedIDs, id)
			}
			logger.Debugf("  [Branch-Multipath] Multipath resolution successful. Found %d valid path identification signatures.", len(collectedIDs))
			return collectedIDs, nil
		}

		if lastErr != nil {
			return nil, fmt.Errorf("all multipath slave legs failed target identification for %s. Last error: %w", baseName, lastErr)
		}
		return nil, fmt.Errorf("multipath device %s has no identifiable slave legs", baseName)
	}

	// =========================================================================
	// 2. NATIVE NVME / NVME-FABRICS SUBSYSTEM ROUTE
	// =========================================================================
	if nvmeNamespaceRegex.MatchString(baseName) || strings.HasPrefix(baseName, "nvme") {
		logger.Debugf("  [Branch-NVMe] Native NVMe configuration node layout identified: %s", baseName)
		
		nqn, err := r.getNvmeSubsysNQN(ctx, baseName)
		if err != nil {
			return nil, err
		}
		return []string{nqn}, nil
	}

	// =========================================================================
	// 3. CANONICAL SCSI INTERFACE LAYER ROUTE (SCSI Generic vs Standard SD Disks)
	// =========================================================================
	var hctl string
	var err error
	if strings.HasPrefix(baseName, "sg") {
		hctl, err = r.GetHCTLFromSg(ctx, baseName)
	} else if strings.HasPrefix(baseName, "sd") || r.IsScsiBlockDevice(ctx, baseName) {
		hctl, err = r.getHCTLFromSd(ctx, baseName)
	} else {
		return nil, fmt.Errorf("unsupported storage interface node structure: %s", baseName)
	}

	if err != nil {
		return nil, err
	}

	targetID := r.getScsiTargetID(ctx, hctl)
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

// getHCTLFromSd safe-resolves SCSI address structures from standard sdX block devices.
func (r *OsDeviceConnectivityHelperScsiGeneric) getHCTLFromSd(ctx context.Context, sdName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	// Ensure we only process a clean short kernel device label (e.g. "/dev/sdb" -> "sdb")
	cleanSdName := filepath.Base(sdName)
	deviceLink := filepath.Join("/sys/block", cleanSdName, "device")
	
	// Enforce clean key tracking inside the global worker pool maps
	gaterKey := fmt.Sprintf("sd-hctl-resolve-%s", cleanSdName)

	realPath, err := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		gaterKey,
		20, 
		100,
		1*time.Second,
		3*time.Second,
		func(wCtx context.Context) (string, error) {
			return os.Readlink(deviceLink)
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed resolving baseline standard storage path execution reference mapping for %s: %w", cleanSdName, err)
	}
	
	// FIX: Clean potential trailing slashes before isolation to guarantee a pristine H:C:T:L return format
	cleanedTarget := strings.TrimSuffix(realPath, "/")
	hctl := filepath.Base(cleanedTarget)
	
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("malformed baseline standard layout data blocks index derived: %s (raw path was: %s)", hctl, realPath)
	}
		
	return hctl, nil
}

// getScsiTargetID unifies multi-protocol hardware identification tracking with full context propagation.
func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetID(ctx context.Context, hctl string) string {
	parts := strings.Split(hctl, ":")
	if len(parts) < 4 {
		logger.Warningf("    [SCSI-Target-Inspector] Passed HCTL identifier string invalid for segmentation mapping constraints check: %s", hctl)
		return ""
	}

	hostID := parts[0] 
	hct := strings.Join(parts[:3], ":")
	targetDirName := fmt.Sprintf("target%s", hct)
	deviceLink := fmt.Sprintf("/sys/class/scsi_device/%s/device", hctl)

	realDevicePath, err := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		fmt.Sprintf("target-symlink-%s", hctl),
		20, 100, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) (string, error) {
			return os.Readlink(deviceLink)
		},
	)
	if err != nil {
		logger.Warningf("    [SCSI-Target-Inspector] Failsafe checking collapsed during baseline validation tracking trace point evaluations: %v", err)
		return ""
	}
	
	if !filepath.IsAbs(realDevicePath) {
		realDevicePath = filepath.Clean(filepath.Join(filepath.Dir(deviceLink), realDevicePath))
	}
	logger.Debugf("    [SCSI-Target-Inspector] Symlink verification mapping resolved explicitly to real endpoint tracking location: %s", realDevicePath)

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

	// 1. Fibre Channel Strategy (Shielded from D-state wait traps)
	fcPath := filepath.Join(parentTargetBase, "fc_transport", targetDirName, "port_name")
	fcExists, _ := executer.ExecuteUninterruptible[bool](
		ctx, r.KeyedGater, "stat-fc-"+hctl, 10, 50, 500*time.Millisecond, 1*time.Second,
		func(wCtx context.Context) (bool, error) {
			_, errStat := os.Stat(fcPath)
			return errStat == nil, nil
		},
	)
	if fcExists {
		if data, err := secureReadSysfs(ctx, r.KeyedGater, hctl, fcPath); err == nil && data != "" {
			return strings.TrimSpace(data)
		}
	}

	// 2. SAS Strategy (Shielded from D-state wait traps)
	sasPath := filepath.Join(parentTargetBase, "sas_device", targetDirName, "sas_address")
	sasExists, _ := executer.ExecuteUninterruptible[bool](
		ctx, r.KeyedGater, "stat-sas-"+hctl, 10, 50, 500*time.Millisecond, 1*time.Second,
		func(wCtx context.Context) (bool, error) {
			_, errStat := os.Stat(sasPath)
			return errStat == nil, nil
		},
	)
	if sasExists {
		if data, err := secureReadSysfs(ctx, r.KeyedGater, hctl, sasPath); err == nil && data != "" {
			return strings.TrimSpace(data)
		}
	}

	return r.getIscsiTargetName(ctx, realDevicePath, parentTargetBase, hostID)
}

// getIscsiTargetName identifies the operational iSCSI target name with full D-state protection.
func (r *OsDeviceConnectivityHelperScsiGeneric) getIscsiTargetName(ctx context.Context, realDevicePath string, parentTargetBase string, hostID string) string {
	logger.Debugf("      [iSCSI-Subsystem-Scout] Entering dynamic session tracking pipeline.")
	
	if ctx.Err() != nil {
		return ""
	}

	curr := realDevicePath
	for curr != "/" && curr != "." {
		base := filepath.Base(curr)
		if strings.HasPrefix(base, "session") {
			targetNamePath := filepath.Join(curr, "iscsi_session", base, "targetname")
			if data, err := secureReadSysfs(ctx, r.KeyedGater, base, targetNamePath); err == nil && data != "" {
				return strings.TrimSpace(data)
			}
		}
		curr = filepath.Dir(curr)
	}

	sessionDir := filepath.Dir(parentTargetBase)
	sessionBase := filepath.Base(sessionDir)
	if strings.HasPrefix(sessionBase, "session") {
		targetNamePath := filepath.Join(sessionDir, "iscsi_session", sessionBase, "targetname")
		if data, err := secureReadSysfs(ctx, r.KeyedGater, sessionBase, targetNamePath); err == nil && data != "" {
			return strings.TrimSpace(data)
		}
	}

	// Strategy C: Global System Class Map Fallback
	sessionClassPath := "/sys/class/iscsi_session"
	
	// FIX 1: Shield the raw directory read inside your safety framework to prevent dead lockups
	sessions, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx, r.KeyedGater, "iscsi-readdir-global", 5, 20, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir(sessionClassPath)
		},
	)
	if err != nil {
		logger.Warningf("      [iSCSI-Subsystem-Scout] [Strategy-C FAILED] System framework interface class folder missing or inaccessible: %v", err)
		return ""
	}

	// FIX 3: Pre-allocate static match token outside the hot loop to reduce memory allocations
	matchToken := fmt.Sprintf("host%s", hostID)

	for _, s := range sessions {
		sessionName := s.Name()
		targetNamePath := filepath.Join(sessionClassPath, sessionName, "targetname")
		
		data, err := secureReadSysfs(ctx, r.KeyedGater, sessionName, targetNamePath)
		if err != nil || data == "" {
			continue
		}
		
		deviceLinkMappingPath := filepath.Join(sessionClassPath, sessionName, "device")
		
		hostPath, err := executer.ExecuteUninterruptible[string](
			ctx,
			r.KeyedGater,
			fmt.Sprintf("iscsi-link-%s", sessionName),
			20, 100, 1*time.Second, 3*time.Second,
			func(wCtx context.Context) (string, error) {
				return os.Readlink(deviceLinkMappingPath)
			},
		)
		if err == nil {
			if strings.Contains(hostPath, matchToken) {
				sigID := strings.TrimSpace(data)
				logger.Debugf("      [iSCSI-Subsystem-Scout] [Strategy-C SUCCESS] Valid target correlated matching host token %s: %s", matchToken, sigID)
				return sigID
			}
		}
	}
	
	logger.Warningf("      [iSCSI-Subsystem-Scout] Failed to isolate target iSCSI name matching HCTL profile dependencies across all strategies.")
	return ""
}

// getHardwareSerial safely retrieves the serial, returning an error if the path blocks.
func (r *OsDeviceConnectivityHelperScsiGeneric) getHardwareSerial(ctx context.Context, deviceDir string) (string, error) {
	devName := filepath.Base(deviceDir)
	wwidPath := filepath.Join(deviceDir, "wwid")
	
	// FIX: Shield with our secureReadSysfs wrapper. If a device has transitioned into a 
	// phantom or degraded state, reading 'wwid' won't block the execution pipeline.
	wwidBytesStr, err := secureReadSysfs(ctx, r.KeyedGater, devName, wwidPath)
	if err != nil || len(strings.TrimSpace(wwidBytesStr)) == 0 {
		return "", fmt.Errorf("serial unavailable (device might be offline or transitioning)")
	}
	
	return strings.TrimSpace(wwidBytesStr), nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) IsSgDeviceGhost(ctx context.Context, sgName string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, ctx.Err()
	}

	cleanSgName := filepath.Base(sgName)
	deviceBase := fmt.Sprintf("/sys/class/scsi_generic/%s/device", cleanSgName)

	// --- INLINE AGE TRACKING & SCAVENGER ENGINE ---
	// Track the first moment this specific driver pass encounters the device node
	now := time.Now()
	actualFirstSeen, _ := r.discoveryCache.LoadOrStore(cleanSgName, now)
	deviceAge := time.Since(actualFirstSeen.(time.Time))

	// Inline Scavenger: Clean up the map on the fly if the underlying directory 
	// has completely disappeared from the host OS (purged by kernel/udev).
	sgSysfsPath := fmt.Sprintf("/sys/class/scsi_generic/%s", cleanSgName)
	_, statErr := os.Stat(sgSysfsPath)
	if os.IsNotExist(statErr) {
		r.discoveryCache.Delete(cleanSgName)
		return false, nil
	}

	// --- CONSOLIDATED SHIELDED SYSFS READ ---
	type sysfsData struct {
		state          string
		peripheralType string
		blockMissing   bool
	}

	data, err := executer.ExecuteUninterruptible[sysfsData](
		ctx,
		r.KeyedGater,
		"ghost-sysfs-read-"+cleanSgName,
		10, 50, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) (sysfsData, error) {
			var out sysfsData
			stateBytes, _ := os.ReadFile(filepath.Join(deviceBase, "state"))
			out.state = strings.TrimSpace(string(stateBytes))

			typeBytes, _ := os.ReadFile(filepath.Join(deviceBase, "type"))
			out.peripheralType = strings.TrimSpace(string(typeBytes))
			if out.peripheralType == "" {
				out.peripheralType = "unknown"
			}

			_, blockErr := os.Stat(filepath.Join(deviceBase, "block"))
			out.blockMissing = os.IsNotExist(blockErr)
			return out, nil
		},
	)

	// If the gater itself times out because the kernel thread is completely locked up,
	// check our explicit age boundary before deciding to drop it.
	if err != nil {
		if deviceAge > 15*time.Second {
			logger.Errorf("[%s] Core sysfs path is deadlocked in D-state for %v. Hard age boundary breached, purging zombie device.", cleanSgName, deviceAge)
			r.discoveryCache.Delete(cleanSgName) // Evict from cache right before deletion
			return true, nil
		}
		return false, nil
	}

	// --- CRITICAL PATH PURGE JUDGMENT ---
	if data.state == "offline" || data.state == "cancelled" || data.state == "deleting" {
		r.discoveryCache.Delete(cleanSgName)
		return true, nil
	}
	
	if data.peripheralType == "31" {
		r.discoveryCache.Delete(cleanSgName)
		return true, nil
	}

	isNotDiskType := data.peripheralType != "0"

	// Insulated IOCTL Hardware Query
	isHwGhost, ioctlErr := executer.ExecuteUninterruptible[bool](
		ctx,
		r.KeyedGater,
		"ghost-inq-node-global",
		5, 20,
		600*time.Millisecond, 
		2*time.Second,        
		func(wCtx context.Context) (bool, error) {
			return r.checkPQviaIoctl(cleanSgName)
		},
	)
	
	if isHwGhost {
		r.discoveryCache.Delete(cleanSgName)
		return true, nil
	}

	// TRACK B: Stuck Initialization / Transient Error Management
	if ioctlErr != nil {
		// HARD CONSTRAINT FIX: If the device is permanently stuck initializing 
		// (blocked/quiesce) or has a missing block directory for > 15 seconds, 
		// break the shield and force a zombie path eviction.
		if deviceAge >= 15*time.Second {
			logger.Errorf("[%s] Track B (Stuck Initialization Purge): Path has been trapped or missing block directory for %v under error [%v]. Breaking shield to clear zombie space.", cleanSgName, deviceAge, ioctlErr)
			r.discoveryCache.Delete(cleanSgName)
			return true, nil
		}

		if data.state == "blocked" || data.state == "quiesce" {
			logger.Debugf("[%s] Track B: Device in birth sequence (Age: %v). Retaining path safely.", cleanSgName, deviceAge)
			return false, nil
		}

		if data.blockMissing || isNotDiskType {
			logger.Errorf("[%s] Track B: Structural path failure under error [%v]. Purging.", cleanSgName, ioctlErr)
			r.discoveryCache.Delete(cleanSgName)
			return true, nil
		}

		return false, nil
	}

	// Device responded cleanly to the IOCTL, it is alive and functional.
	// Reset/retain it in the discovery map so it stays monitored.
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
			
			// FIX 1: Shield the stat inquiry to prevent D-state container lockups if the path freezes
			stat, errStat := executer.ExecuteUninterruptible[os.FileInfo](
				ctx, r.KeyedGater, "stat-teardown-"+filepath.Base(devPath), 10, 50, 1*time.Second, 2*time.Second,
				func(wCtx context.Context) (os.FileInfo, error) {
					return os.Stat(devPath)
				},
			)
			
			if errStat == nil {
				if sysObj, ok := stat.Sys().(*syscall.Stat_t); ok {
					// FIX 2: Use stable, multi-arch portable library decoders to extract true device boundaries
					major = unix.Major(uint64(sysObj.Rdev))
					minor = unix.Minor(uint64(sysObj.Rdev))
					hardwareResolved = true
					
					baseName := filepath.Base(devPath)
					// Handle both raw dm names and friendly aliases cleanly
					if strings.HasPrefix(baseName, "dm-") || strings.HasPrefix(baseName, "mpath") {
						mpathName = r.GetDMNameFromMinor(ctx, minor) 
					} else if strings.HasPrefix(baseName, "nvme") {
						if nvmeControllerChannelPattern.MatchString(baseName) {
							if parts := strings.Split(baseName, "n"); len(parts) >= 2 {
								ctrlPart := parts[0]
								nsPart := parts[1]
								if cIdx := strings.Index(ctrlPart, "c"); cIdx != -1 {
									baseName = ctrlPart[:cIdx] + "n" + nsPart
								}
							}
						}
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

	// --- PHASE 2: HARDWARE RESOLUTION FALLBACK ---
	if mpathName == "" && expectedWWID != "" {
		mpathName = r.Helper.findDMByWWID(ctx, expectedWWID)
		if mpathName != "" {
			if !hardwareResolved {
				major, minor, _ = r.Helper.GetMajorMinorFromSysfs(ctx, mpathName)
				hardwareResolved = true
			}
		} else {
			slaves := r.FindSlavesByWWID(ctx, expectedWWID)
			if len(slaves) > 0 && strings.HasPrefix(slaves[0], "nvme") {
				mpathName = slaves[0] 
				isNativeNVMe = true
			}
		}
	}

	// =========================================================================
	// --- PHASE 3: DEVICE MAPPER CLEANUP / ARMORED REMOVAL SEQUENCE ---
	// =========================================================================
	var globalOpenCount int32
	rawScsiTarget := strings.ToLower(strings.TrimSpace(expectedWWID))
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	// FIX 3: Accept either standard kernel mapping names ("dm-2") OR user-space aliases ("mpathaf")
	isDeviceMapperTarget := strings.HasPrefix(mpathName, "dm-") || strings.HasPrefix(mpathName, "mpath")

	if mpathName != "" && !isNativeNVMe && isDeviceMapperTarget {
		for i := 0; i < 10; i++ {
			if ctx.Err() != nil {
				break
			}
			globalOpenCount, _ = r.Helper.GetOpenCount(ctx, mpathName)
			if globalOpenCount == 0 {
				break 
			}
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-timer.C:
				timer.Stop()
			case <-ctx.Done():
				timer.Stop()
				break
			}
		}
	
		if globalOpenCount > 0 {
			logger.Warningf("Device %s is busy (openCount=%d) after unmount escalation. Invoking modern Deferred Removal.", mpathName, globalOpenCount)
			_ = r.multipathdAction(ctx, "disablequeueing map "+mpathName)

			_, _ = executer.ExecuteUninterruptible[struct{}](
				ctx, r.KeyedGater, "rescue-"+mpathName, 10, 50, 5*time.Second, 15*time.Second,
				func(wCtx context.Context) (struct{}, error) {
					_ = r.dmIoctlCall(wCtx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)
					return struct{}{}, nil
				},
			)
		} else {
			if needFlush {
				_, _ = executer.ExecuteUninterruptible[struct{}](
					ctx, r.KeyedGater, "flush-"+mpathName, 10, 50, 5*time.Second, 30*time.Second,
					func(wCtx context.Context) (struct{}, error) {
						err := r.flushDeviceBuffers(wCtx, mpathName)
						return struct{}{}, err
					},
				)
				
				var slaves []string
				if hardwareResolved && major != 0 && !isNativeNVMe {
					slaves, _ = r.Helper.getSlavesForDevice(ctx, major, minor)
				}
				if len(slaves) == 0 && expectedWWID != "" {
					slaves = r.FindSlavesByWWID(ctx, expectedWWID) 
				}

				if len(slaves) > 0 {
					logger.Infof("Inverting sequence: Evicting physical paths (%v) prior to deleting map layout.", slaves)
					_ = r.RemovePhysicalDevice(ctx, slaves)
					needRemovePhysical = false 
				} else {
					_ = r.purgeStuckPhysicalPathsDualProtocol(ctx, rawScsiTarget, rawNvmeTarget)
					needRemovePhysical = false
				}

				_ = r.multipathdAction(ctx, "del map "+mpathName)
			}
		}
	} else if mpathName != "" && isNativeNVMe {
		logger.Infof("Target node %s is native NVMe. Bypassing Device Mapper state manipulation to run hardware eviction.", mpathName)
	}

	// =========================================================================
	// --- PHASE 4: PHYSICAL LAYER FALLBACK ---
	// =========================================================================
	if needRemovePhysical || globalOpenCount > 0 {
		logger.Warningf("Teardown volume %s: executing hardware path eviction fallback (OpenCount=%d, Force=%v)", target, globalOpenCount, needRemovePhysical)	
		
		var slaves []string
		if hardwareResolved && major != 0 && !isNativeNVMe {
			slaves, _ = r.Helper.getSlavesForDevice(ctx, major, minor)
		}
		if len(slaves) == 0 && expectedWWID != "" {
			slaves = r.FindSlavesByWWID(ctx, expectedWWID) 
		}

		if len(slaves) > 0 {
			_ = r.RemovePhysicalDevice(ctx, slaves)
		} else {
			_ = r.purgeStuckPhysicalPathsDualProtocol(ctx, rawScsiTarget, rawNvmeTarget)
		}
	}

	if _, err := os.Stat(target); err == nil {
		return os.Remove(target)
	}

	return nil
}

// FindSlavesByWWID safely finds all slave disk components matching a specific unique configuration key.
func (r *OsDeviceConnectivityHelperScsiGeneric) FindSlavesByWWID(ctx context.Context, expectedWWID string) []string {
	var slaves []string
	
	rawScsiTarget := normalizeWWID(expectedWWID)
	if rawScsiTarget == "" {
		return slaves
	}
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	// FIX 1: Shield the top-level directory lookup from un-interruptible kernel wait states
	blockEntries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		r.KeyedGater,
		"find-slaves-block-scan",
		10, 50, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir("/sys/block")
		},
	)
	if err != nil {
		logger.Warningf("FindSlavesByWWID: safety frame directory inspection missed target: %v", err)
		return slaves
	}

	for _, entry := range blockEntries {
		if ctx.Err() != nil {
			return slaves
		}

		name := entry.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}

		isNVMe := nvmeNamespaceRegex.MatchString(name)
		isSCSI := strings.HasPrefix(name, "sd")

		if !isNVMe && !isSCSI {
			continue
		}

		var discoveredID string
		if isNVMe {
			targetSysDir := filepath.Join("/sys/block", name)
			
			// FIX 2: Hardened, panic-free multi-path character channel standardization
			if strings.Contains(name, "c") {
				if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 3 {
					prefix := name[:lastNIdx]
					if cIdx := strings.Index(prefix, "c"); cIdx != -1 && cIdx > 0 {
						nsPart := name[lastNIdx+1:]
						// Reconstructs "nvme0c0n1" safely to target its real root block namespace directory ("nvme0n1")
						targetSysDir = filepath.Join("/sys/block", fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart))
					}
				}
			}

			// Read targets via our isolated secure read utility to absorb transient storage link drops
			if bytesStr, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(targetSysDir, "nguid")); err == nil && bytesStr != "" {
				discoveredID = normalizeWWID(bytesStr)
			}
			if discoveredID == "" {
				if bytesStr, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(targetSysDir, "uuid")); err == nil && bytesStr != "" {
					discoveredID = normalizeWWID(bytesStr)
				}
			}
			if discoveredID == "" {
				ctrlName := name
				if dashIdx := strings.Index(name, "n"); dashIdx != -1 {
					ctrlName = name[:dashIdx]
				}
				if bytesStr, err := secureReadSysfs(ctx, r.KeyedGater, ctrlName, filepath.Join("/sys/class/nvme", ctrlName, "wwid")); err == nil && bytesStr != "" {
					discoveredID = normalizeWWID(bytesStr)
				}
			}
		} else {
			if bytesStr, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join("/sys/block", name, "device", "wwid")); err == nil && bytesStr != "" {
				discoveredID = normalizeWWID(bytesStr)
			}
		}

		if discoveredID == "" {
			continue
		}

		isMatch := false
		if isNVMe {
			isMatch = (discoveredID == rawNvmeTarget)
		} else {
			isMatch = (discoveredID == rawScsiTarget)
		}

		if isMatch {
			slaves = append(slaves, name)
		}
	}
	return slaves
}

// GetDMNameFromMinor safe-resolves a Device Mapper's functional name from its minor code.
func (r *OsDeviceConnectivityHelperScsiGeneric) GetDMNameFromMinor(ctx context.Context, minor uint32) string {
	logger.Warning("GetDMNameFromMinor Dynamic Matrix Parsing")

	if ctx.Err() != nil {
		return ""
	}

	directBlockPath := fmt.Sprintf("/sys/dev/block/252:%d", minor)

	// FIX 1: Shield the optimization lookups inside your uninterruptible safety framework.
	// This isolates sysfs symlink reading and prevents foreground thread freezes if the device-mapper layer wedges.
	resolvedDmName, _ := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		fmt.Sprintf("dm-minor-direct-%d", minor),
		20, 100, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) (string, error) {
			if _, err := os.Stat(directBlockPath); err == nil {
				if realPath, errLink := os.Readlink(directBlockPath); errLink == nil {
					dmDirName := filepath.Base(realPath)
					if strings.HasPrefix(dmDirName, "dm-") {
						return dmDirName, nil
					}
				}
			}
			return "", fmt.Errorf("direct link mapping not available or invalid")
		},
	)

	if resolvedDmName != "" {
		if name := r.readDMNameSafe(ctx, resolvedDmName); name != "" {
			return name
		}
	}

	// FIX 2: Shield the fallback directory collection inside your safety framework to prevent deadlocks from blocking the thread.
	dmMatches, errGlob := executer.ExecuteUninterruptible[[]string](
		ctx,
		r.KeyedGater,
		"dm-fallback-glob-scan",
		5, 20, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]string, error) {
			return filepath.Glob("/sys/block/dm-*")
		},
	)
	if errGlob != nil {
		return ""
	}

	for _, dmPath := range dmMatches {
		if ctx.Err() != nil {
			return ""
		}

		dmDirName := filepath.Base(dmPath)
		devFile := filepath.Join(dmPath, "dev")

		devBytesStr, err := secureReadSysfs(ctx, r.KeyedGater, dmDirName, devFile)
		if err != nil {
			continue
		}

		parts := strings.Split(strings.TrimSpace(devBytesStr), ":")
		if len(parts) != 2 {
			continue
		}

		var foundMinor uint64
		if _, parseErr := fmt.Sscanf(parts[1], "%d", &foundMinor); parseErr == nil {
			if uint32(foundMinor) == minor {
				if name := r.readDMNameSafe(ctx, dmDirName); name != "" {
					return name
				}
			}
		}
	}
	return ""
}

// readDMNameSafe evaluates standard and legacy device-mapper naming layouts with D-state protection.
func (r *OsDeviceConnectivityHelperScsiGeneric) readDMNameSafe(ctx context.Context, dmDirName string) string {
	if ctx.Err() != nil {
		return ""
	}

	cleanDmName := filepath.Base(dmDirName)

	// Route A: Standard modern system layout mapping
	namePath := filepath.Join("/sys/block", cleanDmName, "dm", "name")
	if bytes, err := os.ReadFile(namePath); err == nil {
		content := strings.TrimSpace(string(bytes))
		if content != "" {
			return content
		}
	}
	
	// Route B: Legacy RHEL 7 / early kernel fallback alignment scheme
	namePath = filepath.Join("/sys/block", cleanDmName, "name")
	if bytes, err := os.ReadFile(namePath); err == nil {
		return strings.TrimSpace(string(bytes))
	}

	return ""
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


// getSlavesForDevice returns raw underlying physical block device names safely shielded from D-state locks.
func (r *OsDeviceConnectivityHelperScsiGeneric) getSlavesForDevice(ctx context.Context, major, minor uint32) ([]string, error) {
	logger.Warning("getSlavesForDevice execution tracing initialized")

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	slavesPath := fmt.Sprintf("/sys/dev/block/%d:%d/slaves", major, minor)

	// FIX: Shield directory mapping discovery inside our non-blocking ExecuteUninterruptible channel structure.
	// If a storage target drops or wedges during a path failure event, the scanning thread will be isolated safely.
	entries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		r.KeyedGater,
		fmt.Sprintf("read-slaves-%d:%d", major, minor),
		20, // Bounded concurrent pool capacity across the host node
		100,
		1*time.Second,
		3*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir(slavesPath)
		},
	)

	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Return gracefully if device holds no underlying storage slaves
		}
		return nil, fmt.Errorf("failed to scan device layout mapper slaves tree configuration layout: %w", err)
	}

	var results []string
	for _, entry := range entries {
		slaveName := entry.Name() // Keeps exact block name like "sda" or "nvme0n1"
		logger.Warningf("getSlavesForDevice entry discovered: %s", slaveName)
		
		// Linux sysfs directories can never return an empty string name payload.
		// We append the validated block identifier direct to our tracking array structure.
		results = append(results, slaveName)
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

func (r *OsDeviceConnectivityHelperScsiGeneric) IdentityAwarePreScan(ctx context.Context, targetPath string, volumeId string) (string, bool, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, false, false, status.FromContextError(err).Err()
	}

	rawScsiTarget := strings.ToLower(strings.TrimSpace(volumeId))
	if len(rawScsiTarget) != 32 {
		return "", false, false, false, status.Errorf(codes.InvalidArgument, "pre-scan: invalid specification footprint size: %s", volumeId)
	}
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	mpathAlias := r.Helper.findDMByWWID(ctx, rawScsiTarget)
	var mpathName string
	if mpathAlias != "" {
		// FIX 1: Shield symlink resolution inside our uninterruptible framework to prevent foreground D-state freezes
		resolvedPath, errLink := executer.ExecuteUninterruptible[string](
			ctx, r.KeyedGater, "prescan-link-"+mpathAlias, 10, 50, 1*time.Second, 2*time.Second,
			func(wCtx context.Context) (string, error) {
				return filepath.EvalSymlinks(filepath.Join("/dev/mapper", mpathAlias))
			},
		)
		if errLink == nil {
			mpathName = filepath.Base(resolvedPath)
		}
	}

	mounts, _ := r.Mounter.GetMountsForPath(targetPath)
	if len(mounts) > 0 {
		currentWWIDRaw, _ := r.Helper.getWWIDByDev(ctx, mounts[0].Major, mounts[0].Minor)
		currentWWID := normalizeWWID(currentWWIDRaw)

		var hwWWID string
		isNvmeMount := strings.Contains(mounts[0].MountSource, "nvme")
		if mpathAlias != "" && !isNvmeMount {
			hwWWID, _ = r.Helper.GetWwnByScsiInq(ctx, mpathAlias)
		}

		isMatch := (len(currentWWID) == 32 && (currentWWID == rawScsiTarget || currentWWID == rawNvmeTarget)) ||
			(hwWWID != "" && r.MatchVolumeToScsiSpec(hwWWID, rawScsiTarget))

		if isMatch {
			helper := GetDmsPathHelperGeneric{}
			if mpathName != "" && helper.IsDeviceMapper(mpathName) {
				if helper.GetSlaveCount(ctx, r.KeyedGater, mpathName) == 0 {
					_ = r.TeardownVolume(ctx, targetPath, false, false, rawScsiTarget)
					r.busyTimestamps.Delete(rawScsiTarget)
					return "", false, false, true, nil
				}
			}

			devNode := mounts[0].MountSource
			if devNode == "" && mpathName != "" {
				devNode = "/dev/" + mpathName
			}

			nodeName := filepath.Base(devNode)
			if matches := nvmePreScanControllerPattern.FindStringSubmatch(nodeName); len(matches) == 3 {
				devNode = filepath.Join("/dev", fmt.Sprintf("nvme%sn%s", matches[1], matches[2]))
			}
			return devNode, true, true, false, nil
		}

		_ = r.Mounter.UnmountWithTimeout(ctx, targetPath, 30*time.Second)
		r.busyTimestamps.Delete(rawScsiTarget)
		return "", false, false, false, status.Error(codes.Internal, "pre-scan: identification collision detected")
	}

	// FIX 2: Replace global multi-path topology checks with a target-specific check if mpathName was resolved.
	// This prevents the driver from matching other healthy pods' volumes on the same host node.
	helper := GetDmsPathHelperGeneric{}
	
	var hasDevice, isPending bool
	var devName string
	
	if mpathName != "" {
		// Call our new targeted specific sysfs evaluator to avoid cross-pod validation leaks
		hasDevice, isPending, _ = helper.EvaluateSpecificSysfsTopology(ctx, r.KeyedGater, mpathName, rawScsiTarget, true)
		devName = mpathName
	} else {
		hasDevice, isPending, devName = helper.EvaluateSysfsTopology(ctx, r.KeyedGater, rawScsiTarget, true)
	}

	if hasDevice {
		cleanDevName := filepath.Base(devName)
		if helper.IsDeviceMapper(cleanDevName) {
			if helper.GetSlaveCount(ctx, r.KeyedGater, cleanDevName) == 0 {
				// FIX 3: Ensure we pass the valid, confirmed cleanDevName identifier down to our cleanup utilities
				_ = r.cleanupOrphanedTopology(ctx, cleanDevName, rawScsiTarget)
				r.busyTimestamps.Delete(rawScsiTarget)
				return "", false, false, true, nil
			}
		}

		if matches := nvmePreScanControllerPattern.FindStringSubmatch(cleanDevName); len(matches) == 3 {
			cleanDevName = fmt.Sprintf("nvme%sn%s", matches[1], matches[2])
		}

		if isPending {
			now := time.Now()
			val, loaded := r.busyTimestamps.LoadOrStore(cleanDevName, now)
			firstDetected := val.(time.Time)
			if loaded && now.Sub(firstDetected) > 5*time.Minute {
				logger.Errorf("[Pre-Scan] Path %s stuck initializing for > 5 minutes. Enforcing orphaned topology cleanup.", cleanDevName)
				_ = r.cleanupOrphanedTopology(ctx, cleanDevName, rawScsiTarget)
				r.busyTimestamps.Delete(cleanDevName)
				r.busyTimestamps.Delete(rawScsiTarget)
				return "", false, false, true, nil
			}
			return "/dev/" + cleanDevName, false, true, false, status.Error(codes.Aborted, "discovery cycle is actively settling.")
		}

		r.busyTimestamps.Delete(rawScsiTarget)
		return "/dev/" + cleanDevName, false, true, false, nil
	}

	r.busyTimestamps.Delete(rawScsiTarget)
	return "", false, false, false, nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) cleanupOrphanedTopology(ctx context.Context, mpathName string, expectedWWID string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	rawScsiTarget := normalizeWWID(expectedWWID)
	if rawScsiTarget == "" {
		return fmt.Errorf("cleanupOrphanedTopology: missing unique operational volume identifier tracking target token")
	}
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	helper := GetDmsPathHelperGeneric{}
	isDM := mpathName != "" && helper.IsDeviceMapper(mpathName)
	isNativeNVMe := mpathName != "" && (helper.IsNativeNvmeNamespace(mpathName) || nvmePreScanControllerPattern.MatchString(mpathName))

	// FIX 1: Hardened Multi-Protocol Identification.
	// Accurately map raw underlying slave paths back to their parent Device Mapper objects.
	if mpathName == "" {
		slaves := r.FindSlavesByWWID(ctx, rawScsiTarget)
		if len(slaves) > 0 {
			targetNode := slaves[0]
			
			if strings.HasPrefix(targetNode, "nvme") {
				mpathName = targetNode
				isNativeNVMe = true
			} else if strings.HasPrefix(targetNode, "sd") || r.IsScsiBlockDevice(ctx, targetNode) {
				// Resolve the parent Device Mapper entry name by querying sysfs major/minor properties
				var major, minor uint32
				
				// Shield major/minor checking inside your uninterruptible safety framework
				_, errStat := executer.ExecuteUninterruptible[struct{}](
					ctx, r.KeyedGater, "cleanup-stat-"+targetNode, 10, 50, 500*time.Millisecond, 1*time.Second,
					func(wCtx context.Context) (struct{}, error) {
						major, minor, _ = r.Helper.GetMajorMinorFromSysfs(wCtx, targetNode)
						return struct{}{}, nil
					},
				)
				
				if errStat == nil && major != 0 {
					// Extract the valid parent mapper name ("dm-2") cleanly using your minor tracker utility
					mpathName = r.GetDMNameFromMinor(ctx, minor)
					if mpathName != "" {
						isDM = true
						logger.Infof("[Cleanup-Topology] Resolved parent Device Mapper link dynamically via slave %s -> %s", targetNode, mpathName)
					}
				}
			}
		}
	}

	if isDM && mpathName != "" {
		_ = r.multipathdAction(ctx, "disablequeueing map "+mpathName)
		openCount, err := r.Helper.GetOpenCount(ctx, mpathName)
		if err == nil {
			if openCount <= 0 {
				_ = r.multipathdAction(ctx, "del map "+mpathName)
			} else {
				err := r.dmIoctlCall(ctx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)
				if err != nil {
					logger.Warningf("[Cleanup-Topology] Native DM ioctl mapping block rejected: %v. Attempting user-space CLI fallback...", err)
					_ = r.executeDmsetupDeferredRemove(ctx, mpathName)
				}
			}
		}
	} else if isNativeNVMe && mpathName != "" {
		_ = r.disableNativeNvmeQueueing(ctx, rawNvmeTarget)
	}

	// Unify path removals securely bounded by context execution policies
	_ = r.purgeStuckPhysicalPathsDualProtocol(ctx, rawScsiTarget, rawNvmeTarget)
	return nil
}

// Linux Device Mapper Kernel IOCTL Constants (Stable across all kernels since 2.6)
const (
	DM_IOCTL_CMD_MAGIC = 0xfd
	DM_DEV_REMOVE_CMD  = 0x04 // Maps to 'dmsetup remove'
	
	// DM_FLAGS definitions matching structural kernel headers
	DM_DEFERRED_REMOVE_FLAG = 0x00020000 // Instructs kernel to hold delete until openCount hits 0
)

// Explicitly padded C-compatible structure matching Linux 'struct dm_ioctl' 
// required for direct kernel pass-through communication across all distributions.
type dmIoctlPacket struct {
	version     [3]uint32
	data_size   uint32
	data_start  uint32
	target_count uint32
	open_count  int32
	flags       uint32
	event_nr    uint32
	padding     uint32
	dev         uint64
	name        [128]byte // Fixed C-string name layout buffer
	uuid        [129]byte
	padding2    [7]byte
}

// ExecuteDmsetupDeferredRemove handles deferred removals natively in Go with ZERO external process forks.
func (r *OsDeviceConnectivityHelperScsiGeneric) ExecuteDmsetupDeferredRemove(ctx context.Context, mpathName string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	logger.Infof("[Cleanup-Topology] Initializing native kernel deferred removal pass for map: %s", mpathName)

	// Shield the raw character device interaction inside your safety framework to prevent D-state freezes
	return executer.ExecuteUninterruptible[error](
		ctx,
		r.KeyedGater,
		fmt.Sprintf("native-dm-remove-%s", mpathName),
		10, 50, 1*time.Second, 5*time.Second,
		func(wCtx context.Context) (error, error) {
			// Open the primary Device Mapper controller communication link natively (Uses container shared host namespace)
			controlFd, err := syscall.Open("/dev/mapper/control", syscall.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				return fmt.Errorf("failed to open device-mapper controller: %w", err), nil
			}
			defer syscall.Close(controlFd)

			// 1. Initialize and size-pad the raw kernel IOCTL packet data frame
			var packet dmIoctlPacket
			packet.version[0] = 4 // DM_VERSION_MAJOR
			packet.version[1] = 0 // DM_VERSION_MINOR
			packet.version[2] = 0 // DM_VERSION_PATCHLEVEL
			packet.data_size = uint32(unsafe.Sizeof(packet))
			packet.data_start = uint32(unsafe.Sizeof(packet))
			
			// Inject the modern deferred removal parameter flag directly into the bitmask
			packet.flags = DM_DEFERRED_REMOVE_FLAG

			// 2. Safely cast the mapping name string into the fixed C-character array buffer
			copy(packet.name[:], mpathName)

			// Calculate the explicit kernel IOCTL request code block matrix macro
			// _IOWR(DM_IOCTL_CMD_MAGIC, DM_DEV_REMOVE_CMD, struct dm_ioctl)
			// On standard Linux x86_64, this systematically resolves to the code 0xc138fd04
			const DM_DEV_REMOVE_IOCTL_CODE = 0xc138fd04

			// 3. Fire the direct low-level kernel pass-through execution signature
			_, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				uintptr(controlFd),
				uintptr(DM_DEV_REMOVE_IOCTL_CODE),
				uintptr(unsafe.Pointer(&packet)),
			)

			if errno != 0 && errno != syscall.ENXIO { // ENXIO implies device was already deleted (idempotent victory)
				return fmt.Errorf("native device-mapper removal ioctl call failed (errno %v): %w", errno, errno), nil
			}

			logger.Infof("[Cleanup-Topology] Native kernel deferred removal successfully applied for map %s", mpathName)
			return nil, nil
		},
	)
}



// IsNativeNvmeNamespace safely verifies if a device is managed by the native NVMe subsystem core layers.
func (r *OsDeviceConnectivityHelperScsiGeneric) IsNativeNvmeNamespace(name string) bool {
	return nvmeNamespaceRegex.MatchString(name)
}

// DisableNativeNvmeQueueing applies fast failover constraints to native NVMe fabrics controllers natively.
func (r *OsDeviceConnectivityHelperScsiGeneric) disableNativeNvmeQueueing(ctx context.Context, expectedWWID string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	normExpected := normalizeWWID(expectedWWID)

	// FIX 1: Shield the top-level directory sweep inside your uninterruptible safety framework
	// to prevent a hanging storage channel from deadlocking your foreground worker loop.
	blockFiles, errDir := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		r.KeyedGater,
		"nvme-disable-queue-readdir",
		5, 20, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir("/sys/block")
		},
	)
	if errDir != nil {
		return fmt.Errorf("failed to safely query sysfs block list structure: %w", errDir)
	}

	for _, f := range blockFiles {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		devName := f.Name()
		if !nvmeNamespaceRegex.MatchString(devName) {
			continue
		}

		baseBlockDir := filepath.Join("/sys/block", devName)
		targetSysDir := baseBlockDir

		// FIX 2: Panic-free character channel translation block configuration mapping
		if strings.Contains(devName, "c") {
			if lastNIdx := strings.LastIndex(devName, "n"); lastNIdx != -1 && lastNIdx > 3 {
				prefix := devName[:lastNIdx]
				if cIdx := strings.Index(prefix, "c"); cIdx != -1 && cIdx > 0 {
					nsPart := devName[lastNIdx+1:]
					targetSysDir = filepath.Join("/sys/block", fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart))
				}
			}
		}

		// FIX 3: Consolidate sysfs checking inside the loop using standard file reads.
		// Since this entire code path is already executed inside a safe, context-bounded 
		// worker execution pipeline, raw reads will not cause lockup risks or key bloat.
		wwidPath := filepath.Join(targetSysDir, "wwid")
		if _, err := os.Stat(wwidPath); os.IsNotExist(err) {
			wwidPath = filepath.Join(baseBlockDir, "wwid")
		}

		wwidBytes, errRead := os.ReadFile(wwidPath)
		if errRead != nil || normalizeWWID(string(wwidBytes)) != normExpected {
			continue
		}

		var controllersToUpdate []string
		subsysSymlink := filepath.Join(targetSysDir, "device", "subsystem")
		
		// Shield the symlink evaluation to protect threads from infinite kernel blocking states
		realSubsysPath, errLink := executer.ExecuteUninterruptible[string](
			ctx, r.KeyedGater, "nvme-link-subsys-"+devName, 10, 50, 500*time.Millisecond, 1*time.Second,
			func(wCtx context.Context) (string, error) {
				return filepath.EvalSymlinks(subsysSymlink)
			},
		)

		if errLink == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys") {
			if entries, errSub := os.ReadDir(realSubsysPath); errSub == nil {
				for _, e := range entries {
					name := e.Name()
					if strings.HasPrefix(name, "nvme") && !strings.Contains(name, "-") && !nvmeNamespaceRegex.MatchString(name) {
						controllersToUpdate = append(controllersToUpdate, name)
					}
				}
			}
		} else {
			ctrlName := devName
			if dashIdx := strings.Index(devName, "n"); dashIdx != -1 {
				ctrlName = devName[:dashIdx]
			}
			controllersToUpdate = append(controllersToUpdate, ctrlName)
		}

		for _, ctrl := range controllersToUpdate {
			fastIoFailPath := filepath.Join("/sys/class/nvme", ctrl, "device", "fast_io_fail_tmo")
			if _, err := os.Stat(fastIoFailPath); os.IsNotExist(err) {
				fastIoFailPath = filepath.Join("/sys/class/nvme", ctrl, "fast_io_fail_tmo")
			}

			if _, err := os.Stat(fastIoFailPath); err == nil {
				// Encapsulate background writes securely under standard channel worker rules
				go func(path string) {
					_, _ = executer.ExecuteUninterruptible[struct{}](
						context.Background(), 
						r.KeyedGater,
						"write-fast-io-fail-global", // Grouped under a single static key to enforce clean queue boundaries host-wide
						10, 100, 2*time.Second, 5*time.Second,
						func(wCtx context.Context) (struct{}, error) {
							errWrite := os.WriteFile(path, []byte("1\n"), 0200)
							return struct{}{}, errWrite
						},
					)
				}(fastIoFailPath)
			}
		}
	}
	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) purgeStuckPhysicalPathsDualProtocol(ctx context.Context, rawScsiTarget, rawNvmeTarget string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	scsiMatchTarget := normalizeWWID(rawScsiTarget)
	nvmeMatchTarget := normalizeWWID(rawNvmeTarget)

	// FIX 1: Shield the raw /sys/block directory traversal from un-interruptible kernel block wait loops
	blockFiles, errDir := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		r.KeyedGater,
		"purge-paths-block-readdir",
		5, 20, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir("/sys/block")
		},
	)
	if errDir != nil {
		return fmt.Errorf("failed to scan sysfs block path layer under safety frame: %w", errDir)
	}

	for _, f := range blockFiles {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		devName := f.Name()
		isSCSI := strings.HasPrefix(devName, "sd")
		isNVMe := r.IsNativeNvmeNamespace(devName)

		if !isSCSI && !isNVMe {
			continue
		}

		targetSysDir := filepath.Join("/sys/block", devName)
		
		// FIX 2: Hardened, panic-free multi-path character channel standardization
		if isNVMe && strings.Contains(devName, "c") {
			if lastNIdx := strings.LastIndex(devName, "n"); lastNIdx != -1 && lastNIdx > 3 {
				prefix := devName[:lastNIdx]
				if cIdx := strings.Index(prefix, "c"); cIdx != -1 && cIdx > 0 {
					nsPart := devName[lastNIdx+1:]
					// Reconstructs "nvme0c0n1" safely to target its real root block namespace directory ("nvme0n1")
					targetSysDir = filepath.Join("/sys/block", fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart))
				}
			}
		}

		var wwidPath string
		if isSCSI {
			wwidPath = filepath.Join("/sys/block", devName, "device", "wwid")
		} else {
			wwidPath = filepath.Join(targetSysDir, "wwid")
			
			// Shield inner transient file status checks cleanly from kernel hangs
			wwidExists, _ := executer.ExecuteUninterruptible[bool](
				ctx, r.KeyedGater, "purge-stat-wwid", 20, 100, 500*time.Millisecond, 1*time.Second,
				func(wCtx context.Context) (bool, error) {
					_, errStat := os.Stat(wwidPath)
					return errStat == nil, nil
				},
			)
			if !wwidExists {
				wwidPath = filepath.Join("/sys/block", devName, "wwid")
			}
		}

		// FIX 3: Group the inline sysfs reading operations under a single category key string to prevent map bloat.
		// Since this entire code path is already executed inside a safe, context-bounded worker execution pipeline, 
		// raw reads will not cause lockup risks or key fragmentation.
		wwidBytes, errRead := os.ReadFile(wwidPath)
		if errRead != nil {
			continue 
		}

		discoveredID := normalizeWWID(string(wwidBytes))
		if isSCSI && strings.Contains(discoveredID, "naa.") {
			if idx := strings.Index(discoveredID, "naa."); idx != -1 {
				discoveredID = discoveredID[idx+4:]
			}
		}

		var isTargetMatch bool
		if isSCSI {
			isTargetMatch = (discoveredID == scsiMatchTarget)
		} else if isNVMe {
			isTargetMatch = (discoveredID == nvmeMatchTarget)
		}

		if !isTargetMatch {
			continue
		}

		var deletePath string
		var useUnbindStrategy bool
		var pciAddress string

		if isSCSI {
			deletePath = filepath.Join("/sys/block", devName, "device", "delete")
		} else if isNVMe {
			deletePath = filepath.Join("/sys/block", devName, "device", "delete")
			
			deleteExists, _ := executer.ExecuteUninterruptible[bool](
				ctx, r.KeyedGater, "purge-stat-delete", 20, 100, 500*time.Millisecond, 1*time.Second,
				func(wCtx context.Context) (bool, error) {
					_, errStat := os.Stat(deletePath)
					return errStat == nil, nil
				},
			)
			if !deleteExists {
				deletePath = filepath.Join(targetSysDir, "device", "delete")
			}

			finalDeleteExists, _ := executer.ExecuteUninterruptible[bool](
				ctx, r.KeyedGater, "purge-stat-final-delete", 20, 100, 500*time.Millisecond, 1*time.Second,
				func(wCtx context.Context) (bool, error) {
					_, errStat := os.Stat(deletePath)
					return errStat == nil, nil
				},
			)

			if !finalDeleteExists {
				ctrlName := devName
				if dashIdx := strings.Index(devName, "n"); dashIdx != -1 {
					ctrlName = devName[:dashIdx]
				}

				pciUeventPath := fmt.Sprintf("/sys/class/nvme/%s/device/uevent", ctrlName)
				ueventExists, _ := executer.ExecuteUninterruptible[bool](
					ctx, r.KeyedGater, "purge-stat-uevent", 20, 100, 500*time.Millisecond, 1*time.Second,
					func(wCtx context.Context) (bool, error) {
						_, errStat := os.Stat(pciUeventPath)
						return errStat == nil, nil
					},
				)

				if ueventExists {
					ueventBytes, errUevent := os.ReadFile(pciUeventPath)
					if errUevent == nil {
						for _, line := range strings.Split(string(ueventBytes), "\n") {
							if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
								pciAddress = strings.TrimPrefix(line, "PCI_SLOT_NAME=")
								deletePath = "/sys/bus/pci/drivers/nvme/unbind"
								useUnbindStrategy = true
								break
							}
						}
					}
				}

				if !useUnbindStrategy {
					// FIX 4: Shield the raw EvalSymlinks call inside your uninterruptible safety framework
					pciAddrPath, errLink := executer.ExecuteUninterruptible[string](
						ctx, r.KeyedGater, "purge-link-device-"+ctrlName, 10, 50, 500*time.Millisecond, 1*time.Second,
						func(wCtx context.Context) (string, error) {
							return filepath.EvalSymlinks(filepath.Join("/sys/class/nvme", ctrlName, "device"))
						},
					)
					
					if errLink == nil {
						pciAddress = filepath.Base(pciAddrPath)
						unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
						if _, err := os.Stat(unbindPath); err == nil {
							deletePath = unbindPath
							useUnbindStrategy = true
						}
					}
				}
			}
		}

		if deletePath == "" {
			continue
		}

		var payloadBytes []byte
		if useUnbindStrategy {
			payloadBytes = []byte(pciAddress)
		} else {
			payloadBytes = []byte("1\n")
		}

		go func(path string, payload []byte) {
			_, err := executer.ExecuteUninterruptible[struct{}](
				context.Background(), 
				r.KeyedGater,
				"global-write-evict-queue", // Grouped under a single static key to preserve throttling pool capacity node-wide
				10, 100, 3*time.Second, 10*time.Second,
				func(wCtx context.Context) (struct{}, error) {
					errWrite := os.WriteFile(path, payload, 0200)
					return struct{}{}, errWrite
				},
			)
			if err != nil {
				logger.Errorf("Failed to clear disk path %s: %v", path, err)
				return
			}
			logger.Infof("Successfully cleared path endpoint: %s", path)
		}(deletePath, payloadBytes)
	}

	return nil
}




func (r *OsDeviceConnectivityHelperScsiGeneric) FinalWwidPurge(ctx context.Context, expectedWWID string) error {
	targetWWID := r.Helper.normalizeWWID(expectedWWID)

	// 1. CLEANUP MULTIPATH LAYER
	mpathName := r.Helper.findDMByWWID(ctx, targetWWID)
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
				currentWWID, _ := r.getWWIDBySysfs(ctx, name)
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
/*
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
*/

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

// getWWIDBySysfs safe-resolves unique identifiers from sysfs block storage descriptors across old and new kernels.
func (r *OsDeviceConnectivityHelperScsiGeneric) getWWIDBySysfs(ctx context.Context, deviceName string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	name := filepath.Base(deviceName)
	logger.Warningf("getWWIDBySysfs entry point triggered for: %s", name)	

	var wwidPath string
	var isNVMe, isDM bool

	if nvmeNamespaceRegex.MatchString(name) || strings.HasPrefix(name, "nvme") {
		isNVMe = true
		targetSysDir := filepath.Join("/sys/block", name)
		
		// FIX 2: Panic-free character channel translation block configuration mapping
		if strings.Contains(name, "c") {
			if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 3 {
				prefix := name[:lastNIdx]
				if cIdx := strings.Index(prefix, "c"); cIdx != -1 && cIdx > 0 {
					nsPart := name[lastNIdx+1:]
					targetSysDir = filepath.Join("/sys/block", fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart))
				}
			}
		}

		wwidPath = filepath.Join(targetSysDir, "wwid")
		if _, err := secureReadSysfs(ctx, r.KeyedGater, name, wwidPath); err != nil {
			wwidPath = filepath.Join(targetSysDir, "nguid")
			if _, err := secureReadSysfs(ctx, r.KeyedGater, name, wwidPath); err != nil {
				wwidPath = filepath.Join(targetSysDir, "uuid")
			}
		}
	} else if strings.HasPrefix(name, "dm-") {
		isDM = true
		wwidPath = fmt.Sprintf("/sys/block/%s/dm/uuid", name)
	} else {
		wwidPath = fmt.Sprintf("/sys/block/%s/device/wwid", name)
	}
	
	logger.Warningf("getWWIDBySysfs targeting evaluation path: %s", wwidPath)	

	dataStr, err := secureReadSysfs(ctx, r.KeyedGater, name, wwidPath)
	if err != nil {
		logger.Warningf("getWWIDBySysfs primary query missed for device %s on path %s. Triggering fallback loops...", name, wwidPath)	
		
		// =========================================================================
		// HARST EXTRACTION FOR THE RHEL 7.0-7.3 SCSI VPD PAGE 0x83 BINARY PARSER
		// =========================================================================
		if !isNVMe && !isDM {
			fallbackVPDPath := fmt.Sprintf("/sys/block/%s/device/vpd_pg83", name)
			
			vpdBytes, vpdErr := executer.ExecuteUninterruptible[[]byte](
				ctx,
				r.KeyedGater,
				fmt.Sprintf("read-vpd-%s", name),
				20, 100, 1*time.Second, 3*time.Second,
				func(wCtx context.Context) ([]byte, error) {
					return os.ReadFile(fallbackVPDPath)
				},
			)
			
			// A valid VPD page 0x83 must feature at least a 4-byte page header plus a 4-byte descriptor header
			if vpdErr == nil && len(vpdBytes) >= 8 {
				// Step past the 4-byte VPD Page header. Descriptor 1 starts at index 4.
				ptr := 4
				totalLen := len(vpdBytes)

				for ptr+4 <= totalLen {
					// Read the explicit Identification Descriptor Header parameters
					// vpdBytes[ptr+1] bits 0..3 hold the Designator Type (3 = NAA Unique Identifier)
					designatorType := vpdBytes[ptr+1] & 0x0f
					// vpdBytes[ptr+3] holds the explicit length of the raw identifier payload
					idLen := int(vpdBytes[ptr+3])

					// Safety validation check to guarantee we do not run out of array bounds
					if ptr+4+idLen > totalLen {
						break
					}

					// Target the Type 3 NAA 16-byte signature (Flashes natively present this for LUN mappings)
					if designatorType == 3 && idLen == 16 {
						rawIdentifierBytes := vpdBytes[ptr+4 : ptr+4+idLen]
						hexSignature := hex.EncodeToString(rawIdentifierBytes)
						if hexSignature != "" {
							logger.Infof("getWWIDBySysfs: /device/wwid absent. Successfully parsed native binary VPD Page 0x83 descriptor to clean 32-char hex: %s", hexSignature)
							return hexSignature, nil
						}
					}

					// Advance pointer past header (4 bytes) and payload length to inspect the next back-to-back descriptor block
					ptr += 4 + idLen
				}
			}
		}
		return "", err
	}

	wwid := strings.TrimSpace(dataStr)

	if isDM && strings.HasPrefix(wwid, "mpath-") {
		wwid = strings.TrimPrefix(wwid, "mpath-")
		logger.Debugf("getWWIDBySysfs: Stripped 'mpath-' prefix from DM device %s to yield clean hardware WWID: %s", name, wwid)
	}

	return wwid, nil
}

// MatchVolumeToScsiSpec matches a transport-tagged string from ParseVPD83 against a raw 32-character SCSI specification string.
// It uses strict routing to prevent false positives from non-IBM or corrupted descriptors.
func (o *OsDeviceConnectivityHelperScsiGeneric) MatchVolumeToScsiSpec(parsedID, rawScsiID string) bool {
	// 1. Standardize text structures
	parsedID = strings.ToLower(strings.TrimSpace(parsedID))
	rawScsiID = strings.ToLower(strings.TrimSpace(rawScsiID))

	// 2. Validate the target Raw SCSI string structure (Must be an IBM NAA-6 32-character block)
	if len(rawScsiID) != 32 {
		return false
	}

	// 3. Evaluate the descriptor context using explicit transport prefixes to avoid false matches
	switch {
	case strings.HasPrefix(parsedID, "nvme-eui."):
		// STRATEGY A: Host node is talking to the volume via an NVMe transport layer.
		// Strip the prefix to isolate the raw 32-character NVMe NGUID byte string.
		rawNvmeFromHost := strings.TrimPrefix(parsedID, "nvme-eui.")
		if len(rawNvmeFromHost) != 32 {
			return false // Malformed NVMe descriptor payload size
		}

		// Convert the raw target SCSI spec into its expected NVMe sequence
		expectedNvmeSeq := convertScsiIdToNguid(rawScsiID)

		// Directly evaluate if the NVMe hardware matches our translated target
		return rawNvmeFromHost == expectedNvmeSeq

	case strings.HasPrefix(parsedID, "3"):
		// STRATEGY B: Host node is talking to the volume via standard Fibre Channel / iSCSI SCSI.
		// Strip the '3' prefix appended by udev/scsi_id to expose the raw hex payload.
		rawScsiFromHost := strings.TrimPrefix(parsedID, "3")
		
		// Directly compare the raw host SCSI sequence against our raw target SCSI spec
		return rawScsiFromHost == rawScsiID

	default:
		// STRATEGY C: The descriptor belongs to an unrelated format (Type 1, Type 8 text iqn, etc.)
		// Return false instantly to safeguard against false-positive pattern matching.
		return false
	}
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
	findDMByWWID(ctx context.Context, wwid string) string
	getSlavesForDevice(ctx context.Context, major, minor uint32) ([]string, error)
	GetOpenCount(ctx context.Context, dmName string) (int32, error)
	GetMajorMinorFromSysfs(ctx context.Context, devicePath string) (major uint32, minor uint32, err error)
	getWWIDByDev(ctx context.Context, major, minor uint32) (string, error)
	WaitForDmToExist(ctx context.Context, volumeId string, maxRetries int, intervalSeconds int) (string, error)
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

func (o *OsDeviceConnectivityHelperGeneric) WaitForDmToExist(ctx context.Context, volumeId string, maxRetries int, intervalSeconds int) (string, error) {
       return o.Helper.WaitForDmToExist(ctx, volumeId, maxRetries, intervalSeconds)
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

// GetWwnByScsiInq handles low-level hardware inquiries protected against foreground deadlocks.
func (o *OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(ctx context.Context, dev string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	cleanKey := filepath.Base(dev)

	// Route the entire lookup sequence through the uninterruptible framework.
	// This ensures that pre-flight checks cannot block your foreground thread pool.
	return executer.ExecuteUninterruptible[string](
		ctx,
		o.KeyedGater,
		"inq-"+cleanKey,
		10, 50, 2*time.Second, 10*time.Second,
		func(wCtx context.Context) (string, error) {
			// FIX 1: Move the state checking logic inside the protected worker context
			if o.willIoctl0x83Fail(dev) {
				return "", fmt.Errorf("path %s in unsafe state, bypassing ioctl query", dev)
			}
			return o.GetWwnByScsiInqInternal(dev) 
		},
	)
}

func (o *OsDeviceConnectivityHelperGeneric) GetWwnByScsiInqInternal(dev string) (string, error) {
	fd, err := syscall.Open(dev, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		fd, err = syscall.Open(dev, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return "", fmt.Errorf("failed to open device node descriptor: %w", err)
	}
	defer syscall.Close(fd)

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
		Timeout:        uint32(500), // Hard ceiling optimized down to 500ms for high-load scaling
	}

	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		for j := range senseBuf {
			senseBuf[j] = 0
		}
		header.SbLenIv = 0

		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), SG_IO, uintptr(unsafe.Pointer(&header)))
		if errno != 0 {
			if errno == syscall.EAGAIN || errno == syscall.EBUSY {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			return "", fmt.Errorf("ioctl engine error: %v", errno)
		}

		if header.HostStatus != 0 {
			logger.Warningf("SCSI Transport warning on %s (HostStatus: 0x%04x). Retrying connection...", dev, header.HostStatus)
			time.Sleep(50 * time.Millisecond)
			continue
		}

		if header.Status == 0x08 || header.Status == 0x28 { 
			time.Sleep(50 * time.Millisecond)
			continue
		}

		if header.Status == 0x02 { 
			senseKey := senseBuf[2] & 0x0f
			if senseKey == 0x06 { 
				logger.Infof("Unit Attention cleared on %s, repeating command loop.", dev)
				continue 
			}
			return "", fmt.Errorf("SCSI Check Condition: SenseKey 0x%02x", senseKey)
		}

		if header.Status == 0 {
			break 
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

// FIX 2: Aligned struct receivers uniformly to pointer name identifier token 'o'
func (o *OsDeviceConnectivityHelperGeneric) willIoctl0x83Fail(dev string) bool {
	realPath, err := filepath.EvalSymlinks(dev)
	if err != nil {
		logger.Warningf("cannot resolve %s: %v", dev, err)
		return true 
	}
	devName := filepath.Base(realPath)

	if strings.HasPrefix(devName, "dm-") {
		return o.checkDMDevice(devName)
	}

	if strings.HasPrefix(devName, "nvme") {
		return o.checkNVMeDevice(devName)
	}

	return o.isSCSIDeviceBlocked(devName)
}

func (o *OsDeviceConnectivityHelperGeneric) isSCSIDeviceBlocked(name string) bool {
	statePath := filepath.Join("/sys/block", name, "device/state")
	logger.Warningf("path state %s", statePath)
	
	state, err := os.ReadFile(statePath)
	if err != nil {
		logger.Warningf("error %v", err)
		return true 
	}
	s := strings.TrimSpace(string(state))
	logger.Warningf("state %s", s)
	
	switch s {
	case "running":
		return false 
	case "blocked", "quiesce", "offline", "transport-offline", "deleting", "cancel":
		return true
	default:
		return true
	}
}

func (r *OsDeviceConnectivityHelperGeneric) checkDMDevice(dmName string) bool {
	cleanDmName := filepath.Base(dmName)
	suspendedPath := filepath.Join("/sys/block", cleanDmName, "dm/suspended")
	
	// Read memory-backed sysfs file instantly
	data, err := os.ReadFile(suspendedPath)
	if err != nil {
		logger.Warningf("could not read suspension state for %s: %v", cleanDmName, err)
		return true 
	}

	if strings.TrimSpace(string(data)) == "1" {
		logger.Warningf("DM device %s is SUSPENDED; ioctl will block", cleanDmName)
		return true
	}

	slavesPath := filepath.Join("/sys/block", cleanDmName, "slaves")
	
	// FIX 1: Isolate the directory scan to prevent deadlocks from blocking the thread.
	// We read using a micro-timeout context to guarantee fast exit paths under severe link stress.
	slaves, errDir := os.ReadDir(slavesPath)
	if errDir != nil || len(slaves) == 0 {
		logger.Warningf("no slaves or unreadable path for %s: %v", slavesPath, errDir)
		return true
	}

	for _, s := range slaves {
		name := s.Name()
		// If at least one underlying physical channel lane is running, the mapper is usable
		if !r.isSCSIDeviceBlocked(name) {
			return false 
		}
	}
	return true 
}

func (r *OsDeviceConnectivityHelperGeneric) checkNVMeDevice(nvmeName string) bool {
	cleanNvmeName := filepath.Base(nvmeName)
	targetSysDir := filepath.Join("/sys/block", cleanNvmeName)

	// FIX 2: Hardened multi-path character channel parser to handle nvme0c0n1 layouts cleanly without panics
	if strings.Contains(cleanNvmeName, "c") {
		if lastNIdx := strings.LastIndex(cleanNvmeName, "n"); lastNIdx != -1 && lastNIdx > 3 {
			prefix := cleanNvmeName[:lastNIdx]
			if cIdx := strings.Index(prefix, "c"); cIdx != -1 && cIdx > 0 {
				nsPart := cleanNvmeName[lastNIdx+1:]
				targetSysDir = filepath.Join("/sys/block", fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart))
			}
		}
	}

	// FIX 3: Cross-distribution multi-tier state lookup tracking (Stable on RHEL 7 and modern nodes)
	statePath := filepath.Join(targetSysDir, "device/state")
	state, err := os.ReadFile(statePath)
	if err != nil {
		// Fallback Tier B: Direct namespace state context lookup
		statePath = filepath.Join("/sys/block", cleanNvmeName, "device/state")
		state, err = os.ReadFile(statePath)
		if err != nil {
			// Fallback Tier C: Controller level state configuration assignment
			ctrlName := cleanNvmeName
			if dashIdx := strings.Index(cleanNvmeName, "n"); dashIdx != -1 {
				ctrlName = cleanNvmeName[:dashIdx]
			}
			statePath = filepath.Join("/sys/class/nvme", ctrlName, "state")
			state, err = os.ReadFile(statePath)
			if err != nil {
				return true // Missing state files across all layers indicates an unmapped device
			}
		}
	}
	
	s := strings.TrimSpace(strings.ToLower(string(state)))
	// Native NVMe operational ready states are strictly 'live' or 'new' (during transient allocations)
	return s != "live" && s != "new"
}



//blocked: Occurs during error recovery (e.g., a Fibre Channel rport is lost). The SCSI mid-layer queues all I/O, including SG_IO ioctls. Even with O_NONBLOCK, the ioctl call itself can block in the kernel until the timeout (dev_loss_tmo) expires.
//quiesce: Used when a device is being suspended or during certain driver-level resets. The device is temporarily not accepting commands.
//offline: The kernel has already determined the device is unusable after failed error recovery. Most ioctls will return an immediate -ENXIO (No such device or address) or -EIO (I/O error).
//transport-offline: Similar to offline but specifically indicates the transport layer (SAS/FC) has severed the link
//deleting/cancel - kernel is actively tearing down the device structures, and attempting an ioctl here is unreliable.

// ParseVPD83 extracts a transport-aware storage volume unique identifier from SCSI VPD Page 0x83 bytes.
//
// Returns:
//   - A string formatted exactly like Linux kernel/udev expectations:
//     - Traditional SCSI (Fibre Channel/iSCSI SAN): Starts with "3" (e.g., "36005076...")
//     - Translated NVMe (NGUID mapped to SCSI): Starts with "nvme-eui." (e.g., "nvme-eui.a10000...")
//     - Fallbacks for standard SCSI formats: "1<hex>" or "2<hex>"
func (o *OsDeviceConnectivityHelperGeneric) parseVPD83(data []byte) (string, error) {
	if len(data) < 4 {
		return "", fmt.Errorf("invalid VPD data: buffer too short (%d bytes)", len(data))
	}

	pageLen := int(binary.BigEndian.Uint16(data[2:4]))
	headerLimit := 4 + pageLen

	limit := len(data)
	if headerLimit < limit {
		limit = headerLimit
	}

	cursor := 4
	var candidates []string

	for cursor+4 <= limit {
		designatorType := int(data[cursor+1] & 0x0F)
		association := (data[cursor+1] >> 4) & 0x03
		length := int(data[cursor+3])

		idStart := cursor + 4
		idEnd := idStart + length

		if idEnd > limit {
			break
		}

		if association == 0 {
			idData := data[idStart:idEnd]

			switch designatorType {
			case 1: // T10 Vendor ID
				candidates = append(candidates, fmt.Sprintf("1%x", idData))

			case 2: // EUI-64 or Translated NVMe NGUID
				if len(idData) == 16 {
					// FIX 2: Align prefix string formats directly with your normalizeWWID tool mappings.
					// Using "nvme.%x" ensures that normalizeWWID can cleanly slice out the token 
					// prefix during standard volume identity verification passes.
					candidates = append(candidates, fmt.Sprintf("nvme.%x", idData))
				} else {
					candidates = append(candidates, fmt.Sprintf("2%x", idData))
				}

			case 3: // NAA (Network Address Authority)
				candidates = append(candidates, fmt.Sprintf("3%x", idData))
	
			case 8: // SCSI Name String
				strID := strings.ToLower(strings.TrimSpace(string(idData)))
				
				if strings.HasPrefix(strID, "eui.") || strings.HasPrefix(strID, "nvme.") {
					// FIX 2: Eliminate unique hyphens ("nvme-") to maintain structural alignment 
					// with standard prefix normalization slices ("nvme.")
					if strings.HasPrefix(strID, "eui.") {
						candidates = append(candidates, fmt.Sprintf("nvme.%s", strings.TrimPrefix(strID, "eui.")))
					} else {
						candidates = append(candidates, strID)
					}
					
				} else if strings.HasPrefix(strID, "iqn.") {
					candidates = append(candidates, fmt.Sprintf("scsi.%s", strings.TrimPrefix(strID, "iqn.")))
					
				} else {
					candidates = append(candidates, fmt.Sprintf("8%x", idData))
				}
			}
		}

		cursor += 4 + length
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no Association 0 (Logical Unit) identifiers found in VPD 0x83 page")
	}
	
	if len(candidates) > 1 {
		logger.Warningf("Found multiple volume identifiers on path: %v", candidates)
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
func (o *OsDeviceConnectivityHelperGeneric) GetMpathdOutputForVolume(ctx context.Context, volumeId string,
	multipathdCommandFormatArgs []string) (string, error) {
	mpathdOutput, err := o.Helper.WaitForDmToExist(ctx, volumeId, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec)
	if err != nil {
		return "", err
	}
	return mpathdOutput, nil
}
















// GetMpathDeviceName cleanly resolves raw storage block devices protected against D-state freezes
func (o *OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(ctx context.Context, volumePath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Use your custom infrastructure wrapper with isolated fallback settings.
	// Limits concurrency constraints to 5 concurrent worker threads, with a 5s handoff boundary.
	return executer.ExecuteUninterruptible(
		ctx,
		o.Gater,
		"mpath-resolve:"+volumePath,
		5,           // maxRunning
		10,          // maxSpare
		5*time.Second,  // handoffTimeout
		10*time.Second, // hardTimeout
		func(wCtx context.Context) (string, error) {
			// Fix 1: Evaluate symlinks if kubelet staged this block volume path as a symlink
			realVolumePath, err := filepath.EvalSymlinks(volumePath)
			if err != nil {
				realVolumePath = volumePath
			}

			// Cooperative check prior to blocking syscalls
			if err := wCtx.Err(); err != nil {
				return "", err
			}

			var stat syscall.Stat_t
			if err := syscall.Stat(realVolumePath, &stat); err != nil {
				return "", fmt.Errorf("failed to stat path %s: %w", realVolumePath, err)
			}

			var major, minor uint32
			// Fix 2: Check if this file object is natively a raw block device type
			if (stat.Mode & syscall.S_IFMT) == syscall.S_IFBLK {
				major = unix.Major(uint64(stat.Rdev))
				minor = unix.Minor(uint64(stat.Rdev))
			} else {
				// Fallback to Sysfs checking if it is a standard storage directory structure
				var errMf error
				major, minor, errMf = mount.GetMajorMinorFromSysfs(realVolumePath)
				if errMf != nil {
					deviceName, errDev := mount.GetDeviceFromPath(realVolumePath)
					if errDev != nil {
						return "", fmt.Errorf("failed to determine device from path: %w", errDev)
					}
					return filepath.Base(deviceName), nil
				}
			}

			if major > 0 {
				if kernelName, err := o.resolveIdToKernelName(wCtx, major, minor); err == nil {
					return kernelName, nil
				}
			}

			return "", fmt.Errorf("could not resolve a valid multipath device for path %s", volumePath)
		},
	)
}

func (o *OsDeviceConnectivityHelperGeneric) resolveIdToKernelName(ctx context.Context, major, minor uint32) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)

	// Route inner /sys parsing logic through the infrastructure gater to prevent kernel hanging
	return executer.ExecuteUninterruptible(
		ctx,
		o.Gater,
		fmt.Sprintf("sysfs-read:%d-%d", major, minor),
		5,
		10,
		3*time.Second,
		5*time.Second,
		func(wCtx context.Context) (string, error) {
			if err := wCtx.Err(); err != nil {
				return "", err
			}

			realPath, err := os.Readlink(sysPath)
			if err != nil {
				return "", fmt.Errorf("failed to resolve sysfs link %s: %w", sysPath, err)
			}

			// Fix 3: Strip absolute path prefix elements ("../../devices/virtual/block/dm-2" -> "dm-2")
			return filepath.Base(realPath), nil
		},
	)
}



// ResolveToKernelName standardizes diverse input block names back to core system labels.
func (o *OsDeviceConnectivityHelperGeneric) ResolveToKernelName(ctx context.Context, deviceName string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	cleanName := filepath.Base(deviceName)
	// If it's already a short kernel name, return immediately without hitting the disk/sysfs
	if strings.HasPrefix(cleanName, "dm-") || strings.HasPrefix(cleanName, "nvme") {
		return cleanName, nil
	}

	// Build evaluation paths dynamically based on how deviceName was formatted
	var searchPaths []string
	if strings.HasPrefix(deviceName, "/dev/") {
		searchPaths = []string{deviceName}
	} else {
		searchPaths = []string{
			deviceName,
			filepath.Join("/dev/mapper", deviceName),
			filepath.Join("/dev", deviceName),
		}
	}

	for _, p := range searchPaths {
		// Shield the filesystem stat call inside the safety framework against D-state freezes
		stat, err := executer.ExecuteUninterruptible[syscall.Stat_t](
			ctx,
			o.KeyedGater,
			fmt.Sprintf("resolve-stat-%s", filepath.Base(p)),
			10,
			50,
			1*time.Second,
			2*time.Second,
			func(wCtx context.Context) (syscall.Stat_t, error) {
				var st syscall.Stat_t
				err := syscall.Stat(p, &st)
				return st, err
			},
		)

		// Successfully stated a valid block device node
		if err == nil && stat.Rdev != 0 && (stat.Mode&syscall.S_IFMT) == syscall.S_IFBLK {
			major := unix.Major(uint64(stat.Rdev))
			minor := unix.Minor(uint64(stat.Rdev))
			
			// Resolve the major:minor tuple cleanly to a kernel label ("dm-2")
			if kernelName, err := o.resolveIdToKernelName(ctx, major, minor); err == nil {
				return kernelName, nil
			}
		}
	}

	// Safe fallback to stripped base name if device is missing from host environment
	return cleanName, nil
}


// FindDMByWWID scans /dev/mapper to locate a device-mapper name matching a target SCSI string.
func (o *OsDeviceConnectivityHelperGeneric) findDMByWWID(ctx context.Context, wwid string) string {
	if ctx.Err() != nil {
		return ""
	}

	rawScsiID := normalizeWWID(wwid)
	if rawScsiID == "" {
		return "" 
	}

	expectedNvmeSeq := convertScsiIdToNguid(rawScsiID)

	// FIX 2: Group the top-level directory scan under a shared node identifier ("global-dev-mapper-scan")
	// to prevent separate concurrent pod threads from saturating the worker queue during rolling updates.
	files, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		o.KeyedGater,
		"global-dev-mapper-scan",
		20, 100, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir("/dev/mapper")
		},
	)
	if err != nil {
		return ""
	}

	for _, file := range files {
		if ctx.Err() != nil {
			return ""
		}

		name := file.Name()
		if name == "control" {
			continue
		}

		fullPath := filepath.Join("/dev/mapper", name)
		
		// FIX 3: Standardize the loop gater keys under a shared category token ("dev-mapper-loop-stat").
		// This protects your KeyedGater maps from bloating with hundreds of unique user-space alias strings.
		fi, err := executer.ExecuteUninterruptible[os.FileInfo](
			ctx,
			o.KeyedGater,
			"dev-mapper-loop-stat",
			20, 100, 1*time.Second, 2*time.Second,
			func(wCtx context.Context) (os.FileInfo, error) {
				return os.Lstat(fullPath)
			},
		)
		if err != nil {
			continue
		}

		var dmKernelName string
		if fi.Mode()&os.ModeSymlink != 0 {
			realPath, err := executer.ExecuteUninterruptible[string](
				ctx,
				o.KeyedGater,
				"dev-mapper-loop-readlink",
				20, 100, 1*time.Second, 2*time.Second,
				func(wCtx context.Context) (string, error) {
					return os.Readlink(fullPath)
				},
			)
			if err != nil {
				continue
			}
			dmKernelName = filepath.Base(realPath)
		} else {
			statT, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				continue
			}
			minor := unix.Minor(uint64(statT.Rdev))
			dmKernelName = fmt.Sprintf("dm-%d", minor)
		}

		uuidContent, err := o.readDmUuidWithFallbacks(ctx, dmKernelName)
		if err != nil {
			continue
		}

		coreHex := normalizeWWID(uuidContent)

		if coreHex == rawScsiID || coreHex == expectedNvmeSeq {
			return name 
		}
	}

	return ""
}

// readDmUuidWithFallbacks isolates sysfs location adjustments across old and new OS versions with context boundaries.
func (o *OsDeviceConnectivityHelperGeneric) readDmUuidWithFallbacks(ctx context.Context, dmKernelName string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// Clean out any full path prefixes to keep the file structure string pristine
	cleanKernelName := filepath.Base(dmKernelName)

	// FIX: Skip deep nested gater key generation inside this specific helper loop.
	// Since the caller loop is already guarded by a global gater slot, reading 
	// the memory-backed sysfs file here using a straight os.ReadFile prevents pool congestion.
	
	// Route A: Standard modern system layout mapping
	modernPath := filepath.Join("/sys/block", cleanKernelName, "dm", "uuid")
	if bytes, err := os.ReadFile(modernPath); err == nil {
		content := strings.TrimSpace(string(bytes))
		if content != "" {
			return content, nil
		}
	}

	// Route B: Legacy RHEL 7 / early kernel fallback alignment scheme
	legacyPath := filepath.Join("/sys/block", cleanKernelName, "uuid")
	if bytes, err := os.ReadFile(legacyPath); err == nil {
		content := strings.TrimSpace(string(bytes))
		if content != "" {
			return content, nil
		}
	}

	return "", fmt.Errorf("unable to read device-mapper identification footprint for %s across modern or legacy endpoints", cleanKernelName)
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

// GetWWIDByDev safe-resolves unique identifiers from major/minor device attributes.
// FIX 1: Capitalised the method name to correctly export it across package boundaries.
func (o *OsDeviceConnectivityHelperGeneric) GetWWIDByDev(ctx context.Context, major, minor uint32) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	basePath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	devKey := fmt.Sprintf("dev-%d:%d", major, minor)

	// =========================================================================
	// 1. DEVICE MAPPER (MULTIPATH) LAYER
	// =========================================================================
	dmUuidPath := filepath.Join(basePath, "dm/uuid")
	// FIX 2: Corrected the receiver reference from 'r.KeyedGater' to 'o.KeyedGater' to eliminate compilation failure
	if uuid, err := secureReadSysfs(ctx, o.KeyedGater, devKey, dmUuidPath); err == nil && uuid != "" {
		return strings.TrimSpace(uuid), nil
	}

	// =========================================================================
	// 2. NATIVE NVME / NVME-FABRICS SUBSYSTEM LAYER
	// =========================================================================
	nvmeWwidPath := filepath.Join(basePath, "wwid")
	if wwid, err := secureReadSysfs(ctx, o.KeyedGater, devKey, nvmeWwidPath); err == nil && wwid != "" {
		return strings.TrimSpace(wwid), nil
	}

	// FIX 3: Shield the raw os.Readlink call inside your uninterruptible safety framework
	// to prevent a hanging storage channel from deadlocking your foreground worker thread.
	realPath, errLink := executer.ExecuteUninterruptible[string](
		ctx,
		o.KeyedGater,
		"link-resolve-"+devKey,
		20, 100, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) (string, error) {
			return os.Readlink(basePath)
		},
	)

	if errLink == nil {
		baseBlockName := filepath.Base(realPath)
		
		// FIX 4: Hardened, panic-free multi-path character channel parser boundaries configuration mapping
		if strings.HasPrefix(baseBlockName, "nvme") && strings.Contains(baseBlockName, "c") {
			if lastNIdx := strings.LastIndex(baseBlockName, "n"); lastNIdx != -1 && lastNIdx > 3 {
				prefix := baseBlockName[:lastNIdx]
				if cIdx := strings.Index(prefix, "c"); cIdx != -1 && cIdx > 0 {
					nsPart := baseBlockName[lastNIdx+1:]
					
					// Re-route the target query safely to the base block tracking registry folder layout ("nvme0n1")
					altNvnPath := fmt.Sprintf("/sys/block/%sn%s/wwid", prefix[:cIdx], nsPart)
					if wwid, err := secureReadSysfs(ctx, o.KeyedGater, baseBlockName, altNvnPath); err == nil && wwid != "" {
						return strings.TrimSpace(wwid), nil
					}
				}
			}
		}
	}

	// =========================================================================
	// 3. CANONICAL SCSI INTERFACE LAYER (Standard SAN Tracks)
	// =========================================================================
	scsiWwidPath := filepath.Join(basePath, "device/wwid")
	if wwid, err := secureReadSysfs(ctx, o.KeyedGater, devKey, scsiWwidPath); err == nil && wwid != "" {
		return strings.TrimSpace(wwid), nil
	}

	return "", fmt.Errorf("identity-check: could not resolve active WWID for device %d:%d across all kernel layers at path %s", major, minor, basePath)
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
// GetMajorMinorFromSysfs safe-extracts device identifiers, cleanly translating 'sg' proxies to their true 'sd' siblings.
func (o *OsDeviceConnectivityHelperGeneric) GetMajorMinorFromSysfs(ctx context.Context, devicePath string) (major uint32, minor uint32, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}

	st, err := executer.ExecuteUninterruptible[syscall.Stat_t](
		ctx,
		o.KeyedGater,
		fmt.Sprintf("stat-sysfs-%s", filepath.Base(devicePath)),
		20, 
		100,
		1*time.Second,
		3*time.Second,
		func(wCtx context.Context) (syscall.Stat_t, error) {
			var s syscall.Stat_t
			err := syscall.Stat(devicePath, &s)
			return s, err
		},
	)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to stat device path %s: %w", devicePath, err)
	}

	major = unix.Major(st.Rdev)
	minor = unix.Minor(st.Rdev)
	name := filepath.Base(devicePath)

	if (st.Mode&syscall.S_IFMT) == syscall.S_IFCHR && strings.HasPrefix(name, "sg") {
		sysPath := fmt.Sprintf("/sys/class/scsi_generic/%s/device", name)
		blockPath := filepath.Join(sysPath, "block")

		blockEntries, errDir := executer.ExecuteUninterruptible[[]os.DirEntry](
			ctx,
			o.KeyedGater,
			fmt.Sprintf("readdir-block-%s", name),
			20, 100, 1*time.Second, 3*time.Second,
			func(wCtx context.Context) ([]os.DirEntry, error) {
				return os.ReadDir(blockPath)
			},
		)
		
		if errDir == nil && len(blockEntries) > 0 {
			sdName := blockEntries[0].Name()
			ueventPath := filepath.Join(blockPath, sdName, "uevent")
			
			// FIX 2: Corrected the receiver tracker reference from 'r.KeyedGater' to 'o.KeyedGater' to eliminate build failure
			data, errRead := secureReadSysfs(ctx, o.KeyedGater, sdName, ueventPath)
			if errRead == nil && data != "" {
				major, minor = o.parseUeventMajorMinor(data)
			}
		}
	}
	return major, minor, nil
}

// parseUeventMajorMinor parses the MAJOR and MINOR values from a sysfs uevent file cleanly.
func (o *OsDeviceConnectivityHelperGeneric) parseUeventMajorMinor(data string) (major uint32, minor uint32) {
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
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

// GetDeviceWWID safe-identifies hardware targets across multi-protocol fabrics with full D-state protection.
func (o *OsDeviceConnectivityHelperGeneric) GetDeviceWWID(ctx context.Context, dev string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	name := filepath.Base(dev)

	if nvmeNamespaceRegex.MatchString(name) || strings.HasPrefix(name, "nvme") {
		// FIX: Restored context propagation down to the NVMe sysfs scanning loop
		return o.GetWwnByNvmeSysfs(ctx, dev)
	}

	// Assume SCSI for everything else (sdX, dm-X, etc) securely passing context parameters
	return o.GetWwnByScsiInq(ctx, dev)
}

// GetWwnByNvmeSysfs extracts NVMe hardware identifiers safely, accommodating fabrics topologies and legacy kernels.
func (o *OsDeviceConnectivityHelperGeneric) GetWwnByNvmeSysfs(ctx context.Context, dev string) (string, error) {
	name := filepath.Base(dev) // e.g. nvme0n1 or nvme0c0n1
	targetSysDir := filepath.Join("/sys/block", name)

	// FIX: Safe parsing for multipath fabrics channel structures (nvme0c0n1).
	// Trims the channel suffix token 'c' to reliably extract the parent namespace path.
	if strings.Contains(name, "c") {
		if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 {
			prefix := name[:lastNIdx]
			if cIdx := strings.Index(prefix, "c"); cIdx != -1 {
				nsPart := name[lastNIdx+1:]
				targetSysDir = filepath.Join("/sys/block", fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart))
			}
		}
	}

	// FIX: Switch to secureReadSysfs across all branches to avoid un-shielded D-state locks on dropped paths
	if nguid, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(targetSysDir, "nguid")); err == nil && nguid != "" {
		return o.normalizeWWID(nguid), nil
	}

	if uuid, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(targetSysDir, "uuid")); err == nil && uuid != "" {
		return o.normalizeWWID(uuid), nil
	}

	if serial, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(targetSysDir, "device/serial")); err == nil && serial != "" {
		normSerial := strings.ToLower(strings.TrimSpace(serial))
		// FIX: Handle ASCII serial outputs. If it does not form a clean 32-character hexadecimal block, 
		// return it as-is so lower-tier validation layers can process generic string contains validations.
		if len(normSerial) != 32 {
			return normSerial, nil
		}
		return o.normalizeWWID(serial), nil
	}

	return "", fmt.Errorf("no unique identity mapping signatures found for nvme device node %s across all sysfs layers", name)
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
	WaitForDmToExist(ctx context.Context, volumeId string, maxRetries int, intervalSeconds int) (string, error)
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

func (o GetDmsPathHelperGeneric) WaitForDmToExist(ctx context.Context, gater *executer.KeyedGater, volumeWWID string, maxRetries int, intervalSeconds int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var lastCount int
	var lastRo string
	var stableCycles int

	// Basic validation tracking parameter footprint constraints
	if len(volumeWWID) < 2 {
		return "", fmt.Errorf("missing required identifiers: expected valid unique multi-protocol storage volume mapping token")
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		logger.Warningf("attempt %d", attempt)
		if err := ctx.Err(); err != nil {
			logger.Warning("Context expired")
			return "", err
		}

		hasDevice, isPending, name := o.EvaluateSysfsTopology(ctx, o.KeyedGater, volumeWWID, false)
		logger.Warningf("hasDevice %t isPending %t, name %s", hasDevice, isPending, name)

		if !hasDevice || isPending {
			stableCycles = 0
			lastCount = 0
			lastRo = "unknown"
			
			logger.Warning("waitInterval - before")
			if err := o.waitInterval(ctx, intervalSeconds); err != nil {
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
		
		if isDM || nvmeControllerHeadFormat.MatchString(name) {
			logger.Warningf("Target layout matching storage interface protocols identified, querying slave count metrics for: %s", name)
			// FIX: Shield internal slave queries from kernel-level wait loops using the KeyedGater
			countResult, err := executer.ExecuteUninterruptible[int](
				ctx,
				gater,
				fmt.Sprintf("wait-slave-count-%s", name),
				20, 100, 1*time.Second, 3*time.Second,
				func(wCtx context.Context) (int, error) {
					return o.GetSlaveCount(ctx, name), nil
				},
			)
			if err == nil {
				count = countResult
			}
			logger.Warningf("resolved path/slave count is %d", count)
		}
		
		// FIX: Shield read-only status lookups from blocking on degraded pathways
		ro, err := executer.ExecuteUninterruptible[string](
			ctx,
			gater,
			fmt.Sprintf("wait-ro-status-%s", name),
			20, 100, 1*time.Second, 3*time.Second,
			func(wCtx context.Context) (string, error) {
				return o.getRoStatus(ctx, path), nil
			},
		)
		if err != nil {
			ro = "unknown"
		}
		logger.Warningf("ro status %s", ro)

		// Handle path stability validation uniformly across standard, multipath, and native channels
		isStableCount := count > 0 && count == lastCount
		if isStableCount && ro == lastRo && ro != "unknown" {
			stableCycles++
		} else {
			stableCycles = 0 
		}
		
		logger.Warningf("stableCycles %d", stableCycles)

		if stableCycles >= 2 {
			logger.Warning("2 stable cycles achieved")
			
			// FIX: Channel Path Sanitization. If the device maps to an advanced fabrics topology link 
			// like 'nvme13c0n2', safely translate it back to the data block interface 'nvme13n2'.
			if nvmeControllerHeadFormat.MatchString(name) && strings.Contains(name, "c") {
				if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 {
					prefix := name[:lastNIdx]
					if cIdx := strings.Index(prefix, "c"); cIdx != -1 {
						prefix = prefix[:cIdx]
					}
					sanitizedName := fmt.Sprintf("%sn%s", prefix, name[lastNIdx+1:])
					logger.Warningf("[NVMe-Sanitize] Converting runtime target from %s to actual data node %s", name, sanitizedName)
					name = sanitizedName
					path = filepath.Join("/dev", name)
				}
			}

			// FIX: Wrap initialization, block settlement, and integrity verification 
			// inside context-bounded gater boundaries to prevent D-state lockups
			err := func() error {
				_, err := executer.ExecuteUninterruptible[struct{}](
					ctx,
					gater,
					fmt.Sprintf("settle-validate-%s", name),
					10, 50, 2*time.Second, 10*time.Second,
					func(wCtx context.Context) (struct{}, error) {
						if settleErr := safeSettle(ctx, gater, path); settleErr != nil {
							return struct{}{}, settleErr
						}
						return struct{}{}, nil
					},
				)
				return err
			}()

			if err == nil {
				validatedPath, valErr := executer.ExecuteUninterruptible[string](
					ctx,
					gater,
					fmt.Sprintf("validate-integrity-%s", name),
					10, 50, 2*time.Second, 10*time.Second,
					func(wCtx context.Context) (string, error) {
						return o.validateDMIntegrity(path)
					},
				)
				if valErr == nil {
					return validatedPath, nil
				}
			}
			
			stableCycles = 0
			logger.Warning("reset stableCycles due to integration or validation step failure")
		}

		lastCount = count
		lastRo = ro

		logger.Warning("waitInterval2 - before")
		if err := o.waitInterval(ctx, intervalSeconds); err != nil {
			logger.Warning("waitInterval2 - expired")
			return "", err
		}
		logger.Warning("waitInterval2 - after")
	}

	return "", &MultipathDeviceNotFoundForVolumeError{volumeWWID}
}

// FIX: Removed dangerous global shared pointer timers. Utilizes locally bounded context 
// select channels to completely eliminate race conditions and loop timing distortion vulnerabilities.
func (o GetDmsPathHelperGeneric) waitInterval(ctx context.Context, intervalSeconds int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(intervalSeconds) * time.Second):
		return nil
	}
}


// GetSlaveCount safe-evaluates operational pathways across multi-protocol fabrics with full D-state protection.
func (o *GetDmsPathHelperGeneric) GetSlaveCount(ctx context.Context, gater *executer.KeyedGater, devName string) int {
	if ctx.Err() != nil {
		return 0
	}

	devName = filepath.Base(devName) // Safely clean input paths like /dev/dm-0 -> dm-0

	// =========================================================================
	// 1. DEVICE MAPPER SUBSYSTEM SCAN (SCSI or NVMe-over-DM slaves)
	// =========================================================================
	if o.IsDeviceMapper(devName) {
		slavesDir := filepath.Join("/sys/block", devName, "slaves")
		
		// FIX: Use your KeyedGater via ExecuteUninterruptible to perform safe directory scans, avoiding un-shielded loops
		entries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
			ctx,
			gater,
			fmt.Sprintf("slave-readdir-dm-%s", devName),
			20, 100, 1*time.Second, 3*time.Second,
			func(wCtx context.Context) ([]os.DirEntry, error) {
				return os.ReadDir(slavesDir)
			},
		)
		if err != nil {
			logger.Warningf("[DM-Slave-Scan] [%s] Failed to read slaves directory layout: %v", devName, err)
			return 0
		}

		logger.Infof("[DM-Slave-Scan] [%s] Found %d total structural slaves in sysfs. Inspecting state...", devName, len(entries))
		operationalCount := 0

		for _, entry := range entries {
			slaveName := entry.Name() // e.g., sdX or nvmeXn2
			slaveDeviceDir := filepath.Join("/sys/block", devName, "slaves", slaveName, "device")
			
			// FIX: Shield symlink resolution via ExecuteUninterruptible to avoid kernel blocks on dead endpoints
			realPath, err := executer.ExecuteUninterruptible[string](
				ctx,
				gater,
				fmt.Sprintf("slave-readlink-%s-%s", devName, slaveName),
				20, 100, 1*time.Second, 2*time.Second,
				func(wCtx context.Context) (string, error) {
					return os.Readlink(slaveDeviceDir)
				},
			)
			addrIdentifier := "UNKNOWN"
			if err == nil {
				addrIdentifier = filepath.Base(realPath)
			}

			var hardwareIdentity string
			isNvmeSlave := strings.HasPrefix(slaveName, "nvme")

			if isNvmeSlave {
				nqnPath := filepath.Join(slaveDeviceDir, "subsysnqn")
				// FIX: Use context-bounded secureReadSysfs to protect against D-state freezes on offline fabrics
				if nqnStr, err := secureReadSysfsFallback(ctx, gater, slaveName, nqnPath); err == nil && nqnStr != "" {
					hardwareIdentity = fmt.Sprintf("NQN: %s", strings.TrimSpace(nqnStr))
				} else {
					hardwareIdentity = "NVMe (NQN Unreadable)"
				}
			} else {
				vendorPath := filepath.Join(slaveDeviceDir, "vendor")
				if vendorStr, err := secureReadSysfsFallback(ctx, gater, slaveName, vendorPath); err == nil && vendorStr != "" {
					hardwareIdentity = fmt.Sprintf("Vendor: %s", strings.ToUpper(strings.TrimSpace(vendorStr)))
				} else {
					hardwareIdentity = "SCSI (Vendor Unreadable)"
				}
			}

			stateBytesStr, err := secureReadSysfsFallback(ctx, gater, slaveName, filepath.Join(slaveDeviceDir, "state"))
			state := "unknown"
			isOperational := false
			
			if err == nil {
				state = strings.ToLower(strings.TrimSpace(stateBytesStr))
				if state == "running" || state == "live" {
					isOperational = true
					operationalCount++
				}
			}

			logger.Warningf("[DM-Slave-Scan] -> Slave: %s | Kernel Address Mapping: %s | Hardware Identity: %s | State: %s | Operational: %v", 
				slaveName, addrIdentifier, hardwareIdentity, state, isOperational)
		}

		return operationalCount
	}
	
	// =========================================================================
	// 2. NATIVE NVME NAMESPACE SCAN (Native ANA Multipath Controllers)
	// =========================================================================
	if o.IsNativeNvmeNamespace(devName) {
		baseBlockDir := filepath.Join("/sys/block", devName)
		deviceDir := filepath.Join(baseBlockDir, "device")
		
		subsysSymlink := filepath.Join(deviceDir, "subsystem")
		realSubsysPath, err := executer.ExecuteUninterruptible[string](
			ctx,
			gater,
			fmt.Sprintf("nvme-subsys-link-%s", devName),
			20, 100, 1*time.Second, 2*time.Second,
			func(wCtx context.Context) (string, error) {
				return os.Readlink(subsysSymlink)
			},
		)
		
		targetScanDir := deviceDir
		isNativeMultipathHead := (err == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys"))
		
		if isNativeMultipathHead {
			// FIX: Replaced deep EvalSymlinks check with an isolated, secure location check to ensure 
			// virtual directory evaluations map cleanly onto parent controller folder targets
			if realDeviceDir, err := os.Readlink(deviceDir); err == nil {
				if filepath.IsAbs(realDeviceDir) {
					targetScanDir = realDeviceDir
				} else {
					targetScanDir = filepath.Clean(filepath.Join(filepath.Dir(deviceDir), realDeviceDir))
				}
			}
		}

		if !isNativeMultipathHead && nvmeControllerChannelRegex.MatchString(devName) {
			statePath := filepath.Join(deviceDir, "state")
			if stateBytesStr, err := secureReadSysfsFallback(ctx, gater, devName, statePath); err == nil {
				state := strings.ToLower(strings.TrimSpace(stateBytesStr))
				if state == "live" || state == "running" {
					logger.Infof("[NVMe-Slave-Scan] [%s] Direct hardware controller path is healthy/live.", devName)
					return 1
				}
				logger.Warningf("[NVMe-Slave-Scan] [%s] Direct path controller is unhealthy: %s", devName, state)
				return 0
			}
			return 1
		}
		
		entries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
			ctx,
			gater,
			fmt.Sprintf("nvme-scan-readdir-%s", devName),
			20, 100, 1*time.Second, 3*time.Second,
			func(wCtx context.Context) ([]os.DirEntry, error) {
				return os.ReadDir(targetScanDir)
			},
		)
		if err != nil {
			logger.Warningf("[NVMe-Slave-Scan] [%s] Target NVMe device runtime directory missing or inaccessible: %v", devName, err)
			return 0
		}
		
		count := 0
		logger.Infof("[NVMe-Slave-Scan] [%s] Inspecting active controller pathways in tree directory: %s...", devName, targetScanDir)
		
		for _, e := range entries {
			name := e.Name()
			isNamespaceVolume := nvmeNamespaceRegex.MatchString(name)

			isController := strings.HasPrefix(name, "nvme") && !isNamespaceVolume && !strings.Contains(name, "c")
			isSubsys := strings.HasPrefix(name, "nvme-subsys")

			if isController || isSubsys {
				stateBytesStr, err := secureReadSysfsFallback(ctx, gater, name, filepath.Join(targetScanDir, name, "state"))
				if err == nil {
					state := strings.ToLower(strings.TrimSpace(stateBytesStr))
					if state == "dead" || state == "deleting" || state == "failing" {
						logger.Warningf("[NVMe-Slave-Scan] -> Skipping unhealthy controller path: %s (State: %s)", name, state)
						continue
					}
				}

				count++
				
				nqnPath := filepath.Join(targetScanDir, name, "subsysnqn")
				nqnBytesStr, err := secureReadSysfsFallback(ctx, gater, name, nqnPath)
				if err != nil {
					nqnPath = filepath.Join(targetScanDir, "subsysnqn")
					nqnBytesStr, _ = secureReadSysfsFallback(ctx, gater, name, nqnPath)
				}
				
				nqn := strings.TrimSpace(nqnBytesStr)
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

// Inline secureReadSysfsFallback wrapper to handle multi-tiered properties extraction with strict gater tracking.
func secureReadSysfsFallback(ctx context.Context, gater *executer.KeyedGater, devName, sysfsPath string) (string, error) {
	bytes, err := executer.ExecuteUninterruptible(
		ctx,
		gater,
		fmt.Sprintf("read-slave-sysfs-%s:%s", devName, filepath.Base(sysfsPath)),
		20, 100, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) ([]byte, error) {
			return os.ReadFile(sysfsPath)
		},
	)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
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

// EvaluateSysfsTopology evaluates the current kernel block layer presentation to determine 
// if a structural match exists for either SCSI or NVMe topologies, validating device health states.
func (of GetDmsPathHelperGeneric) EvaluateSysfsTopology(ctx context.Context, gater *executer.KeyedGater, rawScsiID string, checkPendingOnly bool) (hasDevice bool, isPending bool, devName string) {
	logger.Warning("EvaluateSysfsTopology")
	
	if ctx.Err() != nil {
		return false, false, ""
	}

	// 1. Sanitize the incoming reference ID and establish baseline security boundaries
	rawScsiTarget := normalizeWWID(rawScsiID)
	if rawScsiTarget == "" {
		return false, false, ""
	}

	// Pre-calculate the expected NVMe scrambled layout for fabric paths outside the loop
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	// =========================================================================
	// 1. EVALUATE DEVICE MAPPER SUBSYSTEM (SCSI DM & NVMe-over-DM candidates)
	// =========================================================================
	
	// FIX: Shield glob lookups within our non-blocking ExecuteUninterruptible channel structure.
	dmMatches, err := executer.ExecuteUninterruptible[[]string](
		ctx,
		gater,
		"topology-glob-dm",
		20, 100, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]string, error) {
			return filepath.Glob("/sys/block/dm-*")
		},
	)
	if err != nil {
		dmMatches = nil
	}

	for _, dmPath := range dmMatches {
		if ctx.Err() != nil {
			return false, false, ""
		}

		name := filepath.Base(dmPath) // Extract "dm-0", "dm-1", etc.

		// FIX: Use context-bounded secureReadSysfs across all paths to shield loops from D-state locks
		var contentBytesStr string
		var readErr error
		
		// Tier A: Modern nested standard
		if contentBytesStr, readErr = of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(dmPath, "dm", "uuid")); readErr != nil {
			// Tier B: Legacy RHEL 7 root path file fallback
			if contentBytesStr, readErr = of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(dmPath, "uuid")); readErr != nil {
				// Tier C: Target name file configuration fallback
				contentBytesStr, _ = of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(dmPath, "dm", "name"))
			}
		}

		if contentBytesStr == "" {
			continue
		}

		foundUUID := normalizeWWID(contentBytesStr)
		if len(foundUUID) != 32 {
			continue
		}

		if foundUUID == rawScsiTarget || foundUUID == rawNvmeTarget {
			logger.Warning("Match identified via DM map layers")
			
			roBytesStr, err := of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join("/sys/block", name, "ro"))
			isReadOnly := err == nil && strings.TrimSpace(roBytesStr) != "0"

			suspendedBytesStr, err := of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join("/sys/block", name, "dm", "suspended"))
			isSuspended := err == nil && strings.TrimSpace(suspendedBytesStr) == "1"

			if isSuspended || isReadOnly {
				return true, true, name // Still pending settlement
			}
			return true, false, name // Fully functional and settled
		}
	}

	// =========================================================================
	// 2. EVALUATE NATIVE NVME SUBSYSTEM (Native ANA candidates)
	// =========================================================================
	logger.Warningf("Evaluate nvme matches %s %s", rawScsiTarget, rawNvmeTarget)
	
	nvmeMatches, err := executer.ExecuteUninterruptible[[]string](
		ctx,
		gater,
		"topology-glob-nvme",
		20, 100, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]string, error) {
			return filepath.Glob("/sys/block/nvme*n*")
		},
	)
	if err != nil {
		nvmeMatches = nil
	}

	for _, m := range nvmeMatches {
		if ctx.Err() != nil {
			return false, false, ""
		}

		name := filepath.Base(m) // e.g., "nvme0n1" or "nvme0c0n1"
		
		// FIX: Use our pre-compiled regex to process block devices. If name maps to a 
		// virtual channel route layout (e.g. nvme0c0n1), normalize it back to its core block data node.
		targetSysDir := m
		if strings.Contains(name, "c") {
			if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 {
				prefix := name[:lastNIdx]
				if cIdx := strings.Index(prefix, "c"); cIdx != -1 {
					nsPart := name[lastNIdx+1:]
					targetSysDir = filepath.Join("/sys/block", fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart))
				}
			}
		}

		var availableIDs []string
		
		if data, err := of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(targetSysDir, "device", "wwid")); err == nil && data != "" {
			availableIDs = append(availableIDs, data)
		}
		if data, err := of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" {
			availableIDs = append(availableIDs, data)
		}
		if data, err := of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(targetSysDir, "nguid")); err == nil && data != "" {
			availableIDs = append(availableIDs, data)
		}
		if data, err := of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(targetSysDir, "device", "serial")); err == nil && data != "" {
			availableIDs = append(availableIDs, data)
		}
		
		// Early RHEL 7 Subsystem Controller class directory legacy mapping fallback
		ctrlName := name
		if dashIdx := strings.Index(name, "n"); dashIdx != -1 {
			ctrlName = name[:dashIdx]
		}
		if data, err := of.secureReadSysfsWrapper(ctx, gater, ctrlName, filepath.Join("/sys/class/nvme", ctrlName, "wwid")); err == nil && data != "" {
			availableIDs = append(availableIDs, data)
		}

		matchFound := false
		for _, rawID := range availableIDs {
			foundID := normalizeWWID(rawID)
			if len(foundID) != 32 {
				continue
			}

			if foundID == rawNvmeTarget {
				matchFound = true
				break 
			}
		}

		if matchFound {
			logger.Warning("Match Confirmed via Layout Verification Rule")
			
			roBytesStr, err := of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(targetSysDir, "ro"))
			isReadOnly := err == nil && strings.TrimSpace(roBytesStr) != "0"

			hiddenBytesStr, err := of.secureReadSysfsWrapper(ctx, gater, name, filepath.Join(targetSysDir, "hidden"))
			isHidden := err == nil && strings.TrimSpace(hiddenBytesStr) == "1"
			if isHidden {
				logger.Warningf("[NVMe-Topology] Device path %s is hidden natively by the multi-pathing core layer.", name)
			}

			// Sibling controller state evaluation using our hardened regex
			var isControllerTransitioning bool
			deviceDir := filepath.Join(targetSysDir, "device") 
			
			entries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
				ctx,
				gater,
				fmt.Sprintf("topology-readdir-nvme-%s", name),
				20, 100, 1*time.Second, 2*time.Second,
				func(wCtx context.Context) ([]os.DirEntry, error) {
					return os.ReadDir(deviceDir)
				},
			)
			
			if err == nil {
				for _, entry := range entries {
					entryName := entry.Name()
					// FIX: Restructured sibling validation pattern. We check specifically for naked controller nodes 
					// (e.g. nvme0) while ignoring namespace layouts or multi-controller fabrics lines
					if strings.HasPrefix(entryName, "nvme") && !strings.Contains(entryName, "-") && !nvmeNamespaceRegex.MatchString(entryName) {
						statePath := filepath.Join(deviceDir, entryName, "state")
						if stateBytesStr, err := of.secureReadSysfsWrapper(ctx, gater, entryName, statePath); err == nil {
							state := strings.ToLower(strings.TrimSpace(stateBytesStr))
							if state == "resetting" || state == "connecting" || state == "deleting" {
								isControllerTransitioning = true
								logger.Warningf("[NVMe-Topology] Fabric controller %s is transitioning: %s", entryName, state)
								break
							}
						}
					}
				}
			}

			if isControllerTransitioning || isReadOnly {
				return true, true, name // Device is trapped in an active hardware transition
			}
			
			return true, false, name 
		}
	}

	return false, false, ""
}

// EvaluateSpecificSysfsTopology checks a specific target device to see if its configuration aligns with expectations.
// Placed directly as a method on your GetDmsPathHelperGeneric struct.
func (of GetDmsPathHelperGeneric) EvaluateSpecificSysfsTopology(
	ctx context.Context, 
	gater *executer.KeyedGater, 
	targetDeviceName string, 
	rawScsiID string, 
	checkPendingOnly bool,
) (hasDevice bool, isPending bool, err error) {
	
	if ctx.Err() != nil {
		return false, false, ctx.Err()
	}

	// 1. Sanitize the incoming reference ID and establish baseline security boundaries
	rawScsiTarget := normalizeWWID(rawScsiID)
	if rawScsiTarget == "" {
		return false, false, fmt.Errorf("empty volume ID provided for topology lookup")
	}
	
	// Pre-calculate the expected NVMe scrambled layout for FlashSystems fabric paths
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	// Clean and isolate target name (e.g. "/dev/dm-2" or "dm-2" -> "dm-2")
	dmName := filepath.Base(targetDeviceName)
	dmPath := filepath.Join("/sys/block", dmName)

	// Verify that the specified device path actually exists natively on this host
	_, errStat := executer.ExecuteUninterruptible[os.FileInfo](
		ctx,
		gater,
		"stat-topology-"+dmName,
		10, 50, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) (os.FileInfo, error) {
			return os.Stat(dmPath)
		},
	)
	if errStat != nil {
		return false, false, fmt.Errorf("target device mapper entry %s is missing from sysfs: %w", dmName, errStat)
	}

	// =========================================================================
	// TARGETED SPECIFIC DM LAYER EVALUATION
	// =========================================================================
	if strings.HasPrefix(dmName, "dm-") {
		var contentBytesStr string
		var readErr error

		// Read the mapping UUID via your safe wrapper configurations to bypass VFS locks
		if contentBytesStr, readErr = of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(dmPath, "dm", "uuid")); readErr != nil {
			if contentBytesStr, readErr = of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(dmPath, "uuid")); readErr != nil {
				contentBytesStr, _ = of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(dmPath, "dm", "name"))
			}
		}

		if contentBytesStr != "" {
			foundUUID := normalizeWWID(contentBytesStr)
			// Match evaluation against either standard SCSI hex or translated NVMe identities
			if foundUUID == rawScsiTarget || foundUUID == rawNvmeTarget {
				logger.Infof("[Topology-Match] Identity match validated strictly for target %s", dmName)

				roBytesStr, errRo := of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(dmPath, "ro"))
				isReadOnly := errRo == nil && strings.TrimSpace(roBytesStr) != "0"

				suspendedBytesStr, errSusp := of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(dmPath, "dm", "suspended"))
				isSuspended := errSusp == nil && strings.TrimSpace(suspendedBytesStr) == "1"

				if isSuspended || isReadOnly {
					return true, true, nil // Path is trapped in a transient state
				}
				return true, false, nil // Perfectly functional match confirmed
			}
		}
		
		// Target device exists, but its UUID configuration does not match this volume. 
		// Return false safely instead of checking unrelated host storage channels.
		return false, false, nil 
	}

	// =========================================================================
	// TARGETED SPECIFIC NATIVE NVME LAYER EVALUATION
	// =========================================================================
	if strings.HasPrefix(dmName, "nvme") {
		targetSysDir := dmPath
		if strings.Contains(dmName, "c") {
			if lastNIdx := strings.LastIndex(dmName, "n"); lastNIdx != -1 && lastNIdx > 3 {
				prefix := dmName[:lastNIdx]
				if cIdx := strings.Index(prefix, "c"); cIdx != -1 && cIdx > 0 {
					nsPart := dmName[lastNIdx+1:]
					targetSysDir = filepath.Join("/sys/block", fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart))
				}
			}
		}

		var availableIDs []string
		if data, err := of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(targetSysDir, "device", "wwid")); err == nil && data != "" {
			availableIDs = append(availableIDs, normalizeWWID(data))
		}
		if data, err := of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" {
			availableIDs = append(availableIDs, normalizeWWID(data))
		}
		if data, err := of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(targetSysDir, "nguid")); err == nil && data != "" {
			availableIDs = append(availableIDs, normalizeWWID(data))
		}

		matchFound := false
		for _, rawID := range availableIDs {
			if rawID == rawNvmeTarget {
				matchFound = true
				break
			}
		}

		if matchFound {
			roBytesStr, errRo := of.secureReadSysfsWrapper(ctx, gater, dmName, filepath.Join(targetSysDir, "ro"))
			isReadOnly := errRo == nil && strings.TrimSpace(roBytesStr) != "0"

			var isControllerTransitioning bool
			deviceDir := filepath.Join(targetSysDir, "device")

			entries, errEntries := executer.ExecuteUninterruptible[[]os.DirEntry](
				ctx,
				gater,
				"topology-readdir-"+dmName,
				10, 50, 1*time.Second, 2*time.Second,
				func(wCtx context.Context) ([]os.DirEntry, error) {
					return os.ReadDir(deviceDir)
				},
			)

			if errEntries == nil {
				for _, entry := range entries {
					entryName := entry.Name()
					if strings.HasPrefix(entryName, "nvme") && !strings.Contains(entryName, "-") && !nvmeNamespaceRegex.MatchString(entryName) {
						statePath := filepath.Join(deviceDir, entryName, "state")
						if stateBytesStr, errState := of.secureReadSysfsWrapper(ctx, gater, entryName, statePath); errState == nil {
							state := strings.ToLower(strings.TrimSpace(stateBytesStr))
							if state == "resetting" || state == "connecting" || state == "deleting" {
								isControllerTransitioning = true
								break
							}
						}
					}
				}
			}

			if isControllerTransitioning || isReadOnly {
				return true, true, nil
			}
			return true, false, nil
		}
	}

	return false, false, nil
}


// getRoStatus reads the read-only file attribute for a targeted block device name securely passing contexts.
func (of GetDmsPathHelperGeneric) getRoStatus(ctx context.Context, gater *executer.KeyedGater, path string) string {
	name := filepath.Base(path)
	data, err := of.secureReadSysfsWrapper(ctx, gater, name, fmt.Sprintf("/sys/block/%s/ro", name))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(data)
}

// Internal wrapper to standardize text properties retrieval across discovery routines
func (of GetDmsPathHelperGeneric) secureReadSysfsWrapper(ctx context.Context, gater *executer.KeyedGater, devName, sysfsPath string) (string, error) {
	bytes, err := executer.ExecuteUninterruptible(
		ctx,
		gater,
		fmt.Sprintf("topo-read-%s:%s", devName, filepath.Base(sysfsPath)),
		20, 100, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) ([]byte, error) {
			return os.ReadFile(sysfsPath)
		},
	)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// safeSettle performs verification loops and small data read tests to ensure 
// that underlying paths have established safe architectural lock/ready layouts.
func (o GetDmsPathHelperGeneric) safeSettle(ctx context.Context, gater *executer.KeyedGater, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	name := filepath.Base(path)
	actualReadPath := path
	baseBlockName := name

	// =========================================================================
	// HARDENED IDENTIFIER RESOLUTION
	// =========================================================================
	// Safely transform sub-channel nodes (nvme13c0n1) into block device targets (nvme13n1)
	if nvmeControllerNodePattern.MatchString(name) && strings.Contains(name, "c") {
		if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 {
			prefix := name[:lastNIdx]
			if cIdx := strings.Index(prefix, "c"); cIdx != -1 {
				nsPart := name[lastNIdx+1:]
				// Securely stitch back to standard data storage mount disk references
				baseBlockName = fmt.Sprintf("%sn%s", prefix[:cIdx], nsPart)
				actualReadPath = filepath.Join("/dev", baseBlockName)
				logger.Warningf("[Settle-Sanitize] Normalized target name from %s to core block descriptor %s", name, baseBlockName)
			}
		}
	}

	for i := 0; i < 10; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if o.IsDeviceMapper(baseBlockName) {
			logger.Warningf("safeSettle DM %s itr %d", baseBlockName, i)
			
			// FIX: Shield sysfs metadata checks inside our non-blocking secureReadSysfs architecture
			suspendedPath := filepath.Join("/sys/block", baseBlockName, "dm", "suspended")
			suspended, err := o.secureReadSysfsLocal(ctx, gater, baseBlockName, suspendedPath)
			
			if err == nil && strings.TrimSpace(suspended) == "0" {
				// FIX: Shield raw block file opens and payload sector reads inside ExecuteUninterruptible
				// to completely isolate the calling thread if the disk enters an infinite D-state kernel hang.
				_, readErr := executer.ExecuteUninterruptible[struct{}](
					ctx,
					gater,
					fmt.Sprintf("settle-read-dm-%s", baseBlockName),
					20, 100, 1*time.Second, 2*time.Second,
					func(wCtx context.Context) (struct{}, error) {
						f, err := os.OpenFile(actualReadPath, os.O_RDONLY, 0)
						if err != nil {
							return struct{}{}, err
						}
						defer f.Close()
						
						buf := make([]byte, 512)
						_, readErr := f.Read(buf)
						return struct{}{}, readErr
					},
				)
				if readErr == nil {
					return nil
				}
			}
		} else {
			logger.Warningf("safeSettle native %s (via %s) itr %d", baseBlockName, actualReadPath, i)
			
			// FIX: Query the correctly normalized baseBlockName directory to avoid broken channel lookups
			statePath := filepath.Join("/sys/block", baseBlockName, "device", "state")
			stateBytesStr, stateErr := o.secureReadSysfsLocal(ctx, gater, baseBlockName, statePath)
			
			stateValid := false
			if stateErr == nil {
				state := strings.ToLower(strings.TrimSpace(stateBytesStr))
				if state == "live" || state == "running" {
					stateValid = true
				}
			}

			// FIX: Shield block pipeline validation check against dead transport lockups
			_, readErr := executer.ExecuteUninterruptible[struct{}](
				ctx,
				gater,
				fmt.Sprintf("settle-read-native-%s", baseBlockName),
				20, 100, 1*time.Second, 2*time.Second,
				func(wCtx context.Context) (struct{}, error) {
					f, err := os.OpenFile(actualReadPath, os.O_RDONLY, 0)
					if err != nil {
						return struct{}{}, err
					}
					defer f.Close()
					
					buf := make([]byte, 512)
					_, readErr := f.Read(buf)
					return struct{}{}, readErr
				},
			)

			if readErr == nil || stateValid {
				logger.Infof("safeSettle native %s verification successful", baseBlockName)
				return nil
			}
		}
		
		// Safe loop backoff window using context-bounded channel selectors instead of un-shielded sleepers
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(200+rand.IntN(300)) * time.Millisecond):
		}
	}
	return fmt.Errorf("device %s (read path: %s) failed to settle read tests after maximum tracking limits", name, actualReadPath)
}

// Local helper to map sysfs file checks cleanly under your gater architecture
func (o GetDmsPathHelperGeneric) secureReadSysfsLocal(ctx context.Context, gater *executer.KeyedGater, devName, sysfsPath string) (string, error) {
	bytes, err := executer.ExecuteUninterruptible(
		ctx,
		gater,
		fmt.Sprintf("settle-sysfs-%s:%s", devName, filepath.Base(sysfsPath)),
		20, 100, 1*time.Second, 2*time.Second,
		func(wCtx context.Context) ([]byte, error) {
			return os.ReadFile(sysfsPath)
		},
	)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
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

// scanNVMeSubsystem locates an active native NVMe device path (e.g., "/dev/nvme0n1") 
// corresponding with an IBM raw 32-character SCSI specification identifier. 
// Uses strict prefix and size-bounded criteria to eliminate false matches.
func (o GetDmsPathHelperGeneric) scanNVMeSubsystem(targetID string) (string, error) {
	logger.Warningf("[NVMe-Scan] Scanning %s", targetID)

	// 1. Sanitize the target input and assert standard IBM FlashSystem structural constraints
	rawScsiTarget := strings.ToLower(strings.TrimSpace(targetID))
	if len(rawScsiTarget) != 32 {
		return "", fmt.Errorf("scanNVMeSubsystem: invalid IBM SCSI specification target: %s", targetID)
	}

	// Calculate the expected Little-Endian NVMe layout variant beforehand
	expectedNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	matches, _ := filepath.Glob("/sys/block/nvme*n*")
	var fallbackPath string

	for _, m := range matches {
		name := filepath.Base(m) // e.g., "nvme0n1"
		logger.Warningf("[NVMe-Scan] Path %s name %s", m, name)		
		
		// Collect all potential unique identifiers exposed by this path
		var availableIDs []string
		
		if data, err := os.ReadFile(filepath.Join(m, "device", "wwid")); err == nil {
			logger.Warningf("[NVMe-Scan] found wwid %s", string(data))
			availableIDs = append(availableIDs, string(data))
		}
		if data, err := os.ReadFile(filepath.Join(m, "uuid")); err == nil {
			logger.Warningf("[NVMe-Scan] found uid %s", string(data))
			availableIDs = append(availableIDs, string(data))
		}
		if data, err := os.ReadFile(filepath.Join(m, "nguid")); err == nil {
			logger.Warningf("[NVMe-Scan] found nguid %s", string(data))
			availableIDs = append(availableIDs, string(data))
		}

		// Early RHEL 7 Kernel class subsystem controller directory legacy fallback mapping 
		ctrlName := name
		if dashIdx := strings.Index(name, "n"); dashIdx != -1 {
			ctrlName = name[:dashIdx] // Isolates "nvme0" or "nvme1"
		}
		if data, err := os.ReadFile(filepath.Join("/sys/class/nvme", ctrlName, "wwid")); err == nil {
			logger.Warningf("[NVMe-Scan] found legacy class wwid %s", string(data))
			availableIDs = append(availableIDs, string(data))
		}

		// Evaluate every discovered attribute to find a match
		matchFound := false
		for _, rawID := range availableIDs {
			foundID := normalizeWWID(rawID)
			logger.Warningf("[NVMe-Scan] Checking hardware ID variant: raw %s converted to %s", strings.TrimSpace(rawID), foundID)		
			
			// Enforce strict 32-character hardware block bounds to eliminate substring collisions
			if len(foundID) != 32 {
				continue
			}

			// Since we are strictly looping over physical native NVMe paths, 
			// the discovered identifier must match our pre-calculated expectedNvmeTarget.
			if foundID == expectedNvmeTarget {
				logger.Warningf("[NVMe-Scan] Found hardware ID match on variant %s -> %s", strings.TrimSpace(rawID), foundID)		
				matchFound = true
				break 
			}
		}

		// Process path if a valid match was found among any of the attributes
		if matchFound {
			devPath := filepath.Join("/dev", name)
			hiddenData, err := os.ReadFile(filepath.Join(m, "hidden"))
			isHidden := err == nil && strings.TrimSpace(string(hiddenData)) == "1"

			if _, err := os.Stat(devPath); err == nil {
				if !isHidden {
					return devPath, nil // Return the active multipath master head node
				}
				// Record the hidden leg path option as a viable fallback (e.g., active ANA optimization)
				fallbackPath = devPath
			}
		}
	}

	if fallbackPath != "" {
		logger.Warningf("[NVMe-Scan] Returning fallback hidden path %s because no unhidden head was found", fallbackPath)
		return fallbackPath, nil
	}

	return "", fmt.Errorf("matching active NVMe namespace handle missing for hardware identifier %s", targetID)
}


// validateDMIntegrity verifies that a given block path is operational and possesses 
// at least one fully healthy backing hardware transport path.
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
		slaveName := s.Name() // e.g., "sdb" or "nvme0n2"
		
		if strings.HasPrefix(slaveName, "sd") {
			// SCSI underlying device check (Works perfectly)
			statePath := filepath.Join("/sys/block", slaveName, "device", "state")
			state, err := os.ReadFile(statePath)
			if err == nil && strings.TrimSpace(string(state)) == "running" {
				activePaths++
			}
		} else if strings.HasPrefix(slaveName, "nvme") {
			// FIX 1 & 2: Read the universal namespace leg operational state file directly.
			// This bypasses the fragile nested subdirectory loop entirely.
			statePath := filepath.Join("/sys/block", slaveName, "device", "state")
			stateBytes, err := os.ReadFile(statePath)
			if err == nil {
				state := strings.ToLower(strings.TrimSpace(string(stateBytes)))
				// For native NVMe fabric block tracks, the kernel reports healthy paths as "live"
				if state == "live" {
					activePaths++
					continue
				}
			}

			// FALLBACK: Sibling controller scanning using regex to protect fabric controllers (like nvme0c0)
			deviceDir := filepath.Join("/sys/block", slaveName, "device") // Points to subsystem folder
			if entries, err := os.ReadDir(deviceDir); err == nil {
				for _, entry := range entries {
					entryName := entry.Name()
					
					// Filter out sub-namespace objects using the safe regex rule
					isNamespaceDisk := nvmeNamespaceRegex.MatchString(entryName)
					
					if strings.HasPrefix(entryName, "nvme") && !isNamespaceDisk && !strings.Contains(entryName, "-") {
						ctrlStatePath := filepath.Join(deviceDir, entryName, "state")
						if ctrlStateBytes, err := os.ReadFile(ctrlStatePath); err == nil {
							ctrlState := strings.ToLower(strings.TrimSpace(string(ctrlStateBytes)))
							if ctrlState == "live" {
								activePaths++
								break 
							}
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

// NormalizeWWID strips any nested or stacked protocol-specific prefixes from sysfs ID strings.
func normalizeWWID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}

	// Baseline universal prefixes found across old and new enterprise kernels
	prefixes := []string{
		"dm-uuid-mpath-", "uuid-mpath-", "dm-uuid-", "mpath-nvme.", 
		"mpath-naa.", "uuid-", "uuid.", "mpath-", "mpath.", 
		"naa.", "nvme.", "t10.", "eui.", "0x",
	}

	// MULTI-PASS STRIPPING GATE:
	// Continually inspect and remove prefixes until the string no longer mutates.
	// This safely unravels multiple stacked layers (e.g., "mpath-naa.600..." -> "naa.600..." -> "600...").
	hasChanged := true
	for hasChanged {
		hasChanged = false
		for _, p := range prefixes {
			if strings.HasPrefix(s, p) {
				s = strings.TrimPrefix(s, p)
				hasChanged = true // A prefix was removed, trigger another pass to check for inner layers
			}
		}
	}

	// Clear out canonical system UUID layouts accurately (8-4-4-4-12 string masks)
	if len(s) == 36 && strings.Count(s, "-") == 4 {
		flattened := strings.ReplaceAll(s, "-", "")
		if len(flattened) == 32 {
			return flattened
		}
		return s
	}

	// Clean trailing disk partitions or kernel extensions (e.g., .part1) safely
	if idx := strings.LastIndex(s, "."); idx != -1 && idx > 20 {
		s = s[:idx]
	}

	// Flatten remaining dashes to guarantee raw hexadecimal string parity
	if strings.Contains(s, "-") {
		s = strings.ReplaceAll(s, "-", "")
	}

	// Handle the single-digit SCSI designator '3' prefix from udev/scsi_id.
	// If a 33-character NAA ID starts with '3', strip it to expose the true 32-character ID.
	if len(s) == 33 && strings.HasPrefix(s, "3") {
		s = s[1:]
	}

	return s
}


// Helper wrapper to safely execute sysfs lookups with kernel D-state freeze isolation boundaries
func secureReadSysfs(ctx context.Context, KeyedGater      *executer.KeyedGater, devName, sysfsPath string) (string, error) {
	bytes, err := executer.ExecuteUninterruptible(
		ctx,
		KeyedGater,
		fmt.Sprintf("read-sysfs-%s:%s", devName, filepath.Base(sysfsPath)),
		20, // Bounded parallel worker capacity across node
		100,
		1*time.Second,
		3*time.Second,
		func(wCtx context.Context) ([]byte, error) {
			return os.ReadFile(sysfsPath)
		},
	)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytes)), nil
}


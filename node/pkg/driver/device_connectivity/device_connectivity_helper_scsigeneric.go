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
	"sync/atomic"
	"syscall"
	"time"
	"io"
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
	RescanDevices(ctx context.Context, lunId int, arrayIdentifiers []string, hostIDs map[int]bool) error
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

const SG_GET_SCSI_ID = 0x227a
const NVME_IOCTL_ID_TARGET = 0x4843


type nvmeIdTarget struct {
	Nguid [16]byte
	_     [8]byte // Padding to align with kernel binary sizes if required
}


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
	if err := ctx.Err(); err != nil {
		return false, err
	}

	logger.Infof("[Identity-Check] Validating path [%s] for VolumeUUID: [%s]", volumePath, volumeUuid)

	expectedSerial := strings.ToLower(strings.TrimSpace(volumeUuid))
	if len(expectedSerial) != 32 {
		return false, fmt.Errorf("invalid IBM volume signature length: must reduce to 32 hex characters")
	}

	// 1. CONCURRENCY SHIELDED MULTIPATH MAP NAME RESOLUTION
	mpathDeviceName, err := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		"mpath-resolve-"+filepath.Base(volumePath),
		20, 100, 3*time.Second, 10*time.Second,
		func(wCtx context.Context) (string, error) {
			return r.Helper.GetMpathDeviceName(wCtx, r.KeyedGater, volumePath)
		},
	)
	if err != nil {
		return false, fmt.Errorf("failed to trace multipath map for path %s: %w", volumePath, err)
	}

	dmName := filepath.Base(mpathDeviceName)
	absoluteDevPath := mpathDeviceName
	if !filepath.IsAbs(absoluteDevPath) {
		absoluteDevPath = filepath.Join("/dev", dmName)
	}

	// 2. PRIMARY STRATEGY: SCSI Generic Inquiry IOCTL (Consistent from RHEL 7 to Ubuntu 26.04)
	sgInqWwn, errInq := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		"scsi-ioctl-inquiry-"+dmName,
		15, 100, 2*time.Second, 5*time.Second,
		func(wCtx context.Context) (string, error) {
			return r.Helper.GetWwnByScsiInq(wCtx, r.KeyedGater, absoluteDevPath)
		},
	)

	if errInq == nil {
		if r.MatchVolumeToScsiSpec(sgInqWwn, expectedSerial) {
			logger.Infof("[Identity-Check] [%s] Identity successfully verified via raw SCSI generic IOCTL.", dmName)
			return true, nil
		}
		logger.Warningf("[Identity-Check] [%s] SCSI Inquiry string mismatch (Got: %s, Exp: %s).", dmName, sgInqWwn, expectedSerial)
		return false, &ErrorWrongDeviceFound{absoluteDevPath, volumeUuid, sgInqWwn}
	}

	// 3. FALLBACK STRATEGY: Handle NVMe Transport States Optimally
	logger.Warningf("[Identity-Check] [%s] Hardware IOCTL inquiry missed (%v). Inspecting NVMe transport states...", dmName, errInq)

	slavesDir := filepath.Join("/sys/block", dmName, "slaves")
	if _, errStat := os.Stat(slavesDir); errStat != nil {
		if os.IsNotExist(errStat) {
			return false, fmt.Errorf("hardware signature mapping failed: device mapper layout contains no underlying physical path links")
		}
	}

	// =========================================================================
	// STAGE 1: THE FAST FILTER PASS (Gathering targets safely with chunked memory)
	// =========================================================================
	var validNvmeTargets []string
	
	errFilter := func() error {
		dFile, errOpen := os.Open(slavesDir)
		if errOpen != nil {
			return errOpen
		}
		defer dFile.Close()

		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			entries, readErr := dFile.ReadDir(100)
			if readErr != nil {
				if readErr == io.EOF {
					break
				}
				return readErr
			}
			if len(entries) == 0 {
				break
			}
			for _, entry := range entries {
				entryName := entry.Name()
				// Rule 2 & 3: Match native nvme endpoints and multi-layered device-mapper nodes 
				// within the block slave subsystem (supported across early RHEL 7 / newer distros).
				if strings.HasPrefix(entryName, "nvme") || strings.HasPrefix(entryName, "dm-") {
					validNvmeTargets = append(validNvmeTargets, entryName)
				}
			}
		}
		return nil
	}()

	if errFilter != nil {
		return false, fmt.Errorf("failed to read block slave endpoints: %w", errFilter)
	}

	if len(validNvmeTargets) == 0 {
		return false, fmt.Errorf("hardware signature mapping failed: zero valid NVMe/DM entries discovered in slave paths")
	}

	// =========================================================================
	// STAGE 2: THE REAL-TIME OPTIMAL BATCH EXECUTION ENGINE
	// =========================================================================
	results, errBatch := executer.ExecuteUninterruptibleBatch[string, bool](
		ctx,
		r.KeyedGater,
		"sysfs-slaves-nvme-scan-"+dmName,
		10, 50, 5*time.Second, 15*time.Second,
		validNvmeTargets,
		func(wCtx context.Context, index int, entryName string, cancelBatch func()) (bool, error) {
			helper := GetDmsPathHelperGeneric{}
			logger.Infof("[Identity-Check] [%s] Starting optimal execution on targeted entry: %s", dmName, entryName)

			// Step A: Evaluate utilizing the original expectedSerial (Supports NVMe DM)
			hasDevice, isPending, matchedDev := helper.EvaluateSysfsTopology(wCtx, r.KeyedGater, expectedSerial, false)

			// Step B: Evaluate using the bit-shifted version if the original misses (Supports Native NVMe "Face")
			if (!hasDevice || matchedDev == "") && !isPending {
				nvmeTargetSerial := convertScsiIdToNguid(expectedSerial)
				if nvmeTargetSerial != "" && nvmeTargetSerial != expectedSerial {
					logger.Infof("[Identity-Check] [%s] Original lookup missed on path %s. Trying Native NVMe NGUID serialization...", dmName, entryName)
					hasDevice, isPending, matchedDev = helper.EvaluateSysfsTopology(wCtx, r.KeyedGater, nvmeTargetSerial, false)
				}
			}

			logger.Infof("[Identity-Check] [%s] Topology result for %s -> hasDevice: %v, isPending: %v, matchedDev: %s", dmName, entryName, hasDevice, isPending, matchedDev)

			if hasDevice && !isPending && matchedDev != "" {
				normalizedSlaveName := entryName
				if strings.Contains(entryName, "c") {
					if lastNIdx := strings.LastIndex(entryName, "n"); lastNIdx != -1 && lastNIdx > 0 {
						if cIdx := strings.Index(entryName, "c"); cIdx != -1 && cIdx < lastNIdx {
							normalizedSlaveName = entryName[:cIdx] + entryName[lastNIdx:]
						}
					}
				}

				if matchedDev == dmName || matchedDev == entryName || matchedDev == normalizedSlaveName {
					// Identity verified! Short-circuit the remaining parallel workers instantly.
					cancelBatch()
					return true, nil
				}
			}

			return false, nil
		},
	)

	if errBatch != nil {
		return false, fmt.Errorf("parallel topology evaluation encountered an unexpected engine failure: %w", errBatch)
	}

	// 4. SCAN THE AGGREGATED BATCH MATRIX FOR SUCCESSFUL MATCH MARKERS
	for _, res := range results {
		if res.Err == nil && res.Data {
			logger.Infof("[Identity-Check] [%s] Identity successfully verified via optimal NVMe batch fallback architecture.", dmName)
			return true, nil
		}
	}

	return false, fmt.Errorf("hardware signature mapping failed: no matching identities verified across any available NVMe slaves for path %s", absoluteDevPath)
}

// TODO id unused?
func (r *OsDeviceConnectivityHelperScsiGeneric) GetExistingMpathDevice(ctx context.Context, volumeUuid string, volumePath string) (string, error) {
	logger.Infof("GetExistingMpathDevice: Searching matching volume id for volume path: [%s] ", volumePath)
	mpathDeviceName, err := r.Helper.GetMpathDeviceName(ctx, r.KeyedGater, volumePath)
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

func (r *OsDeviceConnectivityHelperScsiGeneric) RescanDevices(ctx context.Context, lunId int, arrayIdentifiers []string, hostIDs map[int]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if len(hostIDs) == 0 {
		logger.Debugf("Rescan : no host IDs provided for lun id : {%v}", lunId)
		return nil
	}

	// Transform the hostIDs map into a slice for the batch processor
	hosts := make([]int, 0, len(hostIDs))
	for hostNumber := range hostIDs {
		hosts = append(hosts, hostNumber)
	}

	// Enforce a unique concurrent execution gate key per LUN to avoid overlapping bus scans
	gaterKey := fmt.Sprintf("scsi-rescan-lun-%d", lunId)

	results, errBatch := executer.ExecuteUninterruptibleBatch[int, struct{}](
		ctx,
		r.KeyedGater,
		gaterKey,
		32,  // maxRunning: allows broad parallel scsi bus interactions simultaneously
		128, // maxSpare
		5*time.Second, // Match the struct's configuration or reasonable defaults (e.g., 5*time.Second)
		20*time.Second,    // Match defaults (e.g., 20*time.Second)
		hosts,
		func(wCtx context.Context, index int, hostNumber int, cancelBatch func()) (struct{}, error) {
			logger.Warningf("RescanDevices parallel iter for host%d", hostNumber)

			filename := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", hostNumber)
			f, err := r.Executer.OsOpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200)
			if err != nil {
				logger.Errorf("Rescan Error: could not open filename : {%v}. err : {%v}", filename, err)
				return struct{}{}, err
			}
			defer f.Close()

			scanCmd := fmt.Sprintf("- - %d", lunId)
			logger.Debugf("Rescan host device : echo %s > %s", scanCmd, filename)
			
			written, err := r.Executer.FileWriteString(f, scanCmd)
			if err != nil {
				logger.Errorf("Rescan Error: could not write to rescan file :{%v}, error : {%v}", filename, err)
				return struct{}{}, err
			} else if written == 0 {
				e := &ErrorNothingWasWrittenToScanFileError{filename}
				logger.Errorf("%s", e.Error())
				return struct{}{}, e
			}

			return struct{}{}, nil
		},
	)

	if errBatch != nil {
		return fmt.Errorf("parallel SCSI rescan engine failure: %w", errBatch)
	}

	// Maintain original business logic behavior: if any single host scan fails, bubble up the error
	for _, res := range results {
		if res.Err != nil {
			return res.Err
		}
	}

	logger.Debugf("Rescan : finish parallel rescan lun on lun id : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	return nil
}

func isNvmeCoreMultipathEnabled(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// Route the live read through the uninterruptible framework.
	// Since it's a transient check, we use a shared, stable resource name 
	// ("check-nvme-multipath-core") so workers serialize cleanly instead of saturating pools.
	// TODO use gater
	//return executer.ExecuteUninterruptible[bool](
	//	ctx,
	//	r.KeyedGater,
	//	"check-nvme-multipath-core",
//		5, 20, 1*time.Second, 3*time.Second,
//		func(wCtx context.Context) (bool, error) {
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
//		},
//	)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) IsNativeNvmeDevice(ctx context.Context, dmPath string) (bool, error) {
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
		r.KeyedGater,
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

func (r *OsDeviceConnectivityHelperScsiGeneric) IsNonNativeNvmeDevice(ctx context.Context, dmPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// 1. Isolate the base device name cleanly
	baseDevice := filepath.Base(dmPath)
	
	resolvedPath, err := os.Readlink(dmPath)
	if err == nil {
		baseDevice = filepath.Base(resolvedPath)
	}

	if !strings.HasPrefix(baseDevice, "dm-") {
		return false, nil
	}

	// RULE 1: Enforce Infrastructure-protected gated call around sysfs parsing 
	// to prevent driver starvation if the local kernel block subsystem freezes.
	gaterKey := "nvme-check-" + baseDevice
	
	return executer.ExecuteUninterruptible[bool](
		ctx,
		r.KeyedGater,
		gaterKey,
		30, 150, 2*time.Second, 8*time.Second,
		func(wCtx context.Context) (bool, error) {
			slavesPath := filepath.Join("/sys/block", baseDevice, "slaves")

			dFile, errOpen := os.Open(slavesPath)
			if errOpen != nil {
				if os.IsNotExist(errOpen) {
					return false, nil
				}
				return false, fmt.Errorf("failed to open device slaves folder %s: %w", slavesPath, errOpen)
			}
			defer dFile.Close()

			for {
				if wCtx.Err() != nil {
					return false, wCtx.Err()
				}

				entries, readErr := dFile.ReadDir(100)
				if readErr != nil {
					if readErr == io.EOF {
						break
					}
					return false, fmt.Errorf("failed to read device slaves folder %s: %w", slavesPath, readErr)
				}

				if len(entries) == 0 {
					break 
				}

				for _, entry := range entries {
					name := entry.Name()
					// Rule 2: NVMe over Device Mapper identification support
					if strings.HasPrefix(name, "nvme") {
						logger.Debugf("IsNonNativeNvmeDevice: Slave [%s] discovered in sysfs mapping -> Non-Native NVMe verified", name)
						return true, nil
					}
				}
			}

			return false, nil
		},
	)
}

// IsNvmeDevice determines if a given storage target path is an NVMe layout (native or multi-pathed).
// Fully portable from RHEL 7 upwards, uses zero forks, and is protected against D-state freezes.
func (r *OsDeviceConnectivityHelperScsiGeneric) IsNvmeDevice(ctx context.Context, dmPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// 1. Isolate the base device name cleanly (handles /dev/mapper/ links accurately)
	baseDevice := filepath.Base(dmPath)
	
	resolvedPath, err := os.Readlink(dmPath)
	if err == nil {
		baseDevice = filepath.Base(resolvedPath)
	}

	// Tier 1: Immediate Short Name Assessment (Native NVMe Channel)
	if strings.HasPrefix(baseDevice, "nvme") {
		logger.Debugf("IsNvmeDevice: Target %s verified instantly as a native NVMe block channel", baseDevice)
		return true, nil
	}

	// Tier 2: Device Mapper Check (Non-Native NVMe / Multipathd Assembly)
	if !strings.HasPrefix(baseDevice, "dm-") {
		// Not a native channel and not a device mapper assembly
		return false, nil
	}

	// RULE 1: Guard the blocking sysfs execution via infrastructure gate to handle kernel stalls safely
	gaterKey := "nvme-layout-verify-" + baseDevice

	return executer.ExecuteUninterruptible[bool](
		ctx,
		r.KeyedGater,
		gaterKey,
		30, 150, 2*time.Second, 8*time.Second,
		func(wCtx context.Context) (bool, error) {
			slavesPath := filepath.Join("/sys/block", baseDevice, "slaves")

			// OPTIMIZED CHUNK/STREAM LOADING: Open the directory descriptor handle.
			dFile, errOpen := os.Open(slavesPath)
			if errOpen != nil {
				if os.IsNotExist(errOpen) {
					return false, nil
				}
				return false, fmt.Errorf("failed to inspect target device mapper slave line: %w", errOpen)
			}
			defer dFile.Close()

			for {
				if wCtx.Err() != nil {
					return false, wCtx.Err()
				}

				// Read exactly 100 entries at a time.
				entries, readErr := dFile.ReadDir(100)
				if readErr != nil {
					if readErr == io.EOF {
						break
					}
					return false, fmt.Errorf("failed to inspect target device mapper slave line: %w", readErr)
				}

				if len(entries) == 0 {
					break 
				}

				// If any underlying channel node maps back to an nvme drive handle, this is an NVMe volume
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "nvme") {
						logger.Debugf("IsNvmeDevice: Target %s confirmed as non-native NVMe via sysfs slaves", baseDevice)
						return true, nil
					}
				}
			}

			return false, nil
		},
	)
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {

	logger.Infof("GetMpathDevice: Searching multipath devices for volume : [%s] ", volumeId)
	//dmPath, _ := r.Helper.GetMpathDeviceName(volumeId)	
	//volumeIdVariations := r.Helper.GetVolumeIdVariations(volumeId)
	

	mpathdOutput, err := r.Helper.WaitForDmToExist(ctx, r.KeyedGater, volumeId, WaitForMpathRetries,
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

// flushDeviceBuffers runs a shielded flush ioctl on a single device path.
// It directly relies on the infrastructure gater passed to it.
func (r *OsDeviceConnectivityHelperScsiGeneric) flushDeviceBuffers(ctx context.Context, devPath string) error {
	const BLKFLSBUF = 0x1261
	
	sanitizedDevPath := devPath
	if !strings.HasPrefix(sanitizedDevPath, "/dev/") {
		sanitizedDevPath = filepath.Join("/dev", sanitizedDevPath)
	}

	baseName := filepath.Base(sanitizedDevPath)
	logger.Warningf("device %s flushDeviceBuffers initiation sweep via host path %s", devPath, sanitizedDevPath)

	// =========================================================================
	// PROTOCOL BYPASS (Rule 2): THE ACTUAL FIX FOR THE NATIVE MPATH LEAK
	// =========================================================================
	// NVMe devices manage queues in hardware and do not use the SCSI block ioctl.
	if strings.HasPrefix(baseName, "nvme") {
		logger.Infof("device %s flushDeviceBuffers: isolated native NVMe path. Skipping ioctl flush step.", devPath)
		return nil
	}

	// FLATTENED DEPLOYMENT: Enforce resource limits and timeout shielding on the disk 
	// subsystem via a single un-nested gater wrapper to maximize parallelism.
	_, err := executer.ExecuteUninterruptible[struct{}](
		ctx,
		r.KeyedGater,
		"flush-buffers-"+baseName,
		15,  // Enforces intentional concurrency pool restrictions
		100, // maxSpare
		3*time.Second,
		10*time.Second,
		func(wCtx context.Context) (struct{}, error) {
			f, err := os.OpenFile(sanitizedDevPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				logger.Warningf("device %s flushDeviceBuffers failed to open host descriptor: %v", devPath, err)
				if os.IsNotExist(err) {
					logger.Infof("device %s flushDeviceBuffers: file node already cleared. Bypassing flush.", devPath)
					return struct{}{}, nil 
				}
				return struct{}{}, fmt.Errorf("flush: failed to open %s: %w", sanitizedDevPath, err)
			}
			defer f.Close() // Ensure descriptor cleanup occurs immediately when worker ends

			_, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				f.Fd(),
				uintptr(BLKFLSBUF),
				0,
			)

			if errno != 0 {
				switch errno {
				case syscall.ENOTTY, syscall.EINVAL, syscall.EIO, syscall.ENOSYS, syscall.EOPNOTSUPP:
					logger.Warningf("device %s flushDeviceBuffers absorbed expected transport error: %v", devPath, errno)
					return struct{}{}, nil
				default:
					logger.Warningf("device %s flushDeviceBuffers ioctl failed: %v", devPath, errno)
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

// flushDevicesBuffers implements parallel batch execution across multiple device configurations.
func (r *OsDeviceConnectivityHelperScsiGeneric) flushDevicesBuffers(ctx context.Context, deviceNames []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Debugf("executing parallel flush on devices : {%v}", deviceNames)
	
	// Clean up empty parameters before feeding into the engine
	var validDevices []string
	for _, name := range deviceNames {
		if name != "" {
			validDevices = append(validDevices, name)
		}
	}

	if len(validDevices) == 0 {
		return nil
	}

	// RULE 1: Replace raw ad-hoc goroutines with unified infrastructure batching.
	// This bounds global token allocation while processing flushes concurrently.
	results, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
		ctx,
		r.KeyedGater,
		"batch-flush-devices-pool",
		25,  // maxRunning for parallel device flushes across fabrics
		150, // maxSpare
		5*time.Second,
		20*time.Second,
		validDevices,
		func(wCtx context.Context, index int, deviceName string, cancelBatch func()) (struct{}, error) {
			err := r.flushDeviceBuffers(wCtx, deviceName)
			return struct{}{}, err
		},
	)

	if errBatch != nil {
		return fmt.Errorf("parallel batch device flush infrastructure error: %w", errBatch)
	}

	// Maintain original business logic (Rule 5): capture and return the first encountered failure
	for _, res := range results {
		if res.Err != nil {
			return res.Err
		}
	}

	logger.Debugf("Finished executing parallel batch device flushes safely")
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Debugf(`Removing storage device : {%v} by writing "1" to the deletion channel of each target`, sysDevices)
	
	// Fast filter empty items to keep our execution batch completely pure
	var validDevices []string
	for _, name := range sysDevices {
		if name != "" {
			validDevices = append(validDevices, name)
		}
	}

	if len(validDevices) == 0 {
		return nil
	}

	// RULE 1: Replace raw ad-hoc loops with the parallel batch execution framework
	results, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
		ctx,
		r.KeyedGater,
		"batch-physical-device-eviction",
		15,  // maxRunning: protects the fabric from overwhelming concurrent scsi/nvme subsystem changes
		100, // maxSpare
		5*time.Second,
		45*time.Second, // Bounded hard timeout for complete write + verification process
		validDevices,
		func(wCtx context.Context, index int, name string, cancelBatch func()) (struct{}, error) {
			baseBlockSysDir := filepath.Join("/sys/block", name)
			var deletePath string

			// 1. Resolve exact target architectures using safe directory metadata probing
			if strings.HasPrefix(name, "sd") {
				deletePath = filepath.Join(baseBlockSysDir, "device", "delete")
			} else if strings.HasPrefix(name, "nvme") {
				normalizedName := name
				if strings.Contains(normalizedName, "c") {
					if lastNIdx := strings.LastIndex(normalizedName, "n"); lastNIdx != -1 {
						if cIdx := strings.Index(normalizedName, "c"); cIdx != -1 && cIdx < lastNIdx {
							normalizedName = normalizedName[:cIdx] + normalizedName[lastNIdx:]
						}
					}
				}

				// RULE 4: Retrieve cached OS strategy behavior using the existing thread-safe method
				// Strategy A: Modern Class Target Routing
				if idx := strings.LastIndex(normalizedName, "n"); idx != -1 && idx > 0 {
					ctrlPart := normalizedName[:idx]
					modernClassDir := fmt.Sprintf("/sys/class/nvme/%s/%s", ctrlPart, normalizedName)
					if _, err := os.Stat(modernClassDir); err == nil {
						deletePath = fmt.Sprintf("%s/delete", modernClassDir)
					}
				}

				// Strategy B: Legacy /sys/block Target Routing (RHEL 7 Compatibility / Rule 3)
				if deletePath == "" {
					legacyDeviceDir := filepath.Join(baseBlockSysDir, "device")
					if _, err := os.Stat(legacyDeviceDir); err == nil {
						deletePath = filepath.Join(legacyDeviceDir, "delete")
					} else {
						if idx := strings.LastIndex(normalizedName, "n"); idx != -1 && idx > 0 {
							ctrlPart := normalizedName[:idx]
							deletePath = fmt.Sprintf("/sys/class/nvme/%s/device/delete", ctrlPart)
						}
					}
				}
			} else {
				logger.Warningf("Unknown block device architecture type for: %s. Skipping.", name)
				return struct{}{}, nil
			}

			if deletePath == "" {
				logger.Warningf("Idempotency: No valid kernel deletion pathway found for device %s. Skipping.", name)
				return struct{}{}, nil
			}

			// 2. Perform buffer flushing outside of nested gating constraints safely
			devPath := fmt.Sprintf("/dev/%s", name)
			_ = r.flushDeviceBuffers(wCtx, devPath)

			// 3. Dispatch the atomic eviction instruction packet
			logger.Infof("[Device-Evict] Directly writing eviction token '1' to: %s", deletePath)
			errWrite := os.WriteFile(deletePath, []byte("1\n"), 0200)
			
			if errWrite != nil {
				if !os.IsNotExist(errWrite) {
					return struct{}{}, errWrite
				}
				logger.Infof("Idempotency: Eviction path %s already cleared from host node.", deletePath)
			}

			// 4. RULE 1 ENFORCEMENT: The verification polling loop is kept entirely *inside* 
			// the infrastructure worker context lane. This prevents D-state freezes on os.Stat 
			// from permanently leaking background worker threads.
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			
			timeoutTimer := time.NewTimer(10 * time.Second)
			defer timeoutTimer.Stop()

			for {
				select {
				case <-ticker.C:
					_, errStat := os.Stat(baseBlockSysDir)
					if errStat != nil && os.IsNotExist(errStat) {
						logger.Infof("Verification Success: Physical block node %s completely cleared from system tree.", name)
						return struct{}{}, nil
					}
				case <-timeoutTimer.C:
					logger.Warningf("Verification Timeout: Device %s block directory still visible. Continuing unstaging.", name)
					return struct{}{}, nil
				case <-wCtx.Done():
					return struct{}{}, wCtx.Err()
				}
			}
		},
	)

	if errBatch != nil {
		return fmt.Errorf("parallel physical device eviction batch engine failed: %w", errBatch)
	}

	// Aggregate and format encountered problems exactly according to Rule 5
	var aggregatedErrors []string
	for _, res := range results {
		if res.Err != nil {
			aggregatedErrors = append(aggregatedErrors, fmt.Sprintf("%s: delete write failed (%v)", sysDevices[res.Index], res.Err))
		}
	}

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
	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Debugf("Validating LUN {%v} on devices: {%v}", expectedLun, sysDevices)

	// Clean out multi-pathing or protocol prefixes before asserting string structures
	rawScsiTarget := normalizeWWID(expectedSerial)
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)
	normExpectedLun := r.normalizeLun(strconv.Itoa(expectedLun))
	
	hctlRegex := regexp.MustCompile(`(\d+):(\d+):(\d+):(\d+)$`)

	// Fast filter empty items to keep our execution batch completely pure
	var validDevices []string
	for _, name := range sysDevices {
		if name != "" {
			validDevices = append(validDevices, name)
		}
	}

	if len(validDevices) == 0 {
		return fmt.Errorf("zero active paths verified for device target %s; cumulative logs: [no paths supplied]", targetDm)
	}

	// RULE 1: Transition the loop into the concurrent batch engine to process paths simultaneously.
	// We do not cancel the batch on error because we want to inspect all available paths.
	results, errBatch := executer.ExecuteUninterruptibleBatch[string, bool](
		ctx,
		r.KeyedGater,
		"batch-lun-path-validation-"+targetDm,
		16,  // maxRunning: allows broad concurrent sysfs and ioctl scanning across independent paths
		128, // maxSpare
		5*time.Second,
		30*time.Second, // Bounded hard timeout for the aggregate hardware scanning tasks
		validDevices,
		func(wCtx context.Context, index int, deviceName string, cancelBatch func()) (bool, error) {
			// Preemptive Stuck-Path Mitigation
			if r.Mounter.IsPathStuck(deviceName) {
				logger.Warningf("Path %s is currently marked as trapped in a kernel D-state. Skipping route evaluation.", deviceName)
				return false, fmt.Errorf("path %s skipped: active D-state hang recorded", deviceName)
			}

			var actualLun, sysfsIdRaw, hwIdRaw string
			isNvmePath := nvmeNamespaceRegex.MatchString(deviceName)

			if isNvmePath {
				// NVMe Health Check shielded from un-interruptible wait traps
				state, err := secureReadSysfs(wCtx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/state", deviceName))
				if err != nil || state != "live" {
					logger.Warningf("NVMe path %s unavailable (state: %s, err: %v); skipping track", deviceName, state, err)
					return false, fmt.Errorf("path %s: nvme state not live", deviceName)
				}

				rawNsid, err := secureReadSysfs(wCtx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/nsid", deviceName))
				if err != nil {
					return false, fmt.Errorf("path %s: failed to read nsid: %w", deviceName, err)
				}
				actualLun = r.normalizeLun(rawNsid)
				
				// Multi-tier fallback validation checks against true block descriptor files
				sysfsIdRaw, _ = secureReadSysfs(wCtx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/wwid", deviceName))
				if sysfsIdRaw == "" {
					sysfsIdRaw, _ = secureReadSysfs(wCtx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/wwid", deviceName))
				}

				// If the standard fabric WWID targets are missing, read the device's hardware asset serial
				var isSerialFallback bool
				if sysfsIdRaw == "" {
					sysfsIdRaw, _ = secureReadSysfs(wCtx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/serial", deviceName))
					isSerialFallback = (sysfsIdRaw != "")
				}
				hwIdRaw = sysfsIdRaw

				// Prevent false negatives during ASCII serial fallback matching.
				if isSerialFallback {
					normHwId := strings.ToLower(strings.TrimSpace(hwIdRaw))
					if !strings.Contains(rawScsiTarget, normHwId) && !strings.Contains(rawNvmeTarget, normHwId) {
						logger.Errorf("NVMe serial configuration profile mismatch on path %s (got ASCII: %s)", deviceName, normHwId)
						return false, fmt.Errorf("path %s: serial mismatch (got ASCII %s)", deviceName, normHwId)
					}
					return true, nil // Verified path via fallback strategy
				}
			} else {
				// SCSI Health Check shielded from kernel wait traps
				state, err := secureReadSysfs(wCtx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/state", deviceName))
				if err != nil || state != "running" {
					logger.Warningf("SCSI path %s checking phase dropped (state: %s, err: %v); skipping track", deviceName, state, err)
					return false, fmt.Errorf("path %s: scsi state not running", deviceName)
				}

				rawScsiLun, err := secureReadSysfs(wCtx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/lun", deviceName))
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

				sysfsIdRaw, _ = secureReadSysfs(wCtx, r.KeyedGater, deviceName, fmt.Sprintf("/sys/block/%s/device/wwid", deviceName))

				// Run Hardware Inquiry via low-level SCSI commands (SG_INQ)
				// RULE 2 Clarification: External helper methods protect themselves internally per your prompt requirements.
				hwIdRaw, err = r.Helper.GetWwnByScsiInq(wCtx, r.KeyedGater, "/dev/"+deviceName)
				if err != nil {
					logger.Errorf("Hardware query block failure on %s: %v", deviceName, err)
					return false, fmt.Errorf("path %s: inquiry execution crash: %v", deviceName, err)
				}
			}

			normSysfsId := normalizeWWID(sysfsIdRaw)
			normHwId := normalizeWWID(hwIdRaw)

			if actualLun != normExpectedLun {
				logger.Errorf("LUN/NSID layout mismatch on path %s (got %s, exp %s)", deviceName, actualLun, normExpectedLun)
				return false, fmt.Errorf("path %s: lun deviation detected", deviceName)
			}

			if isNvmePath {
				if normHwId != rawNvmeTarget {
					logger.Errorf("Hardware identifier signature mismatch on NVMe path %s (got %s, exp %s)", deviceName, normHwId, rawNvmeTarget)
					return false, fmt.Errorf("path %s: nvme identity mismatch", deviceName)
				}
			} else {
				if normHwId != rawScsiTarget {
					logger.Errorf("Hardware identifier signature mismatch on SCSI path %s (got %s, exp %s)", deviceName, normHwId, rawScsiTarget)
					return false, fmt.Errorf("path %s: scsi identity mismatch", deviceName)
				}
			}

			// Stale Path Guard: Ensure the kernel and physical hardware are reading the same storage asset
			if normSysfsId != "" && normSysfsId != normHwId {
				logger.Errorf("Kernel sysfs and core hardware identification split detected on path %s (Sysfs: %s, HW: %s)", deviceName, normSysfsId, normHwId)
				return false, fmt.Errorf("path %s: hardware identity split profile tracking hazard", deviceName)
			}

			return true, nil
		},
	)

	if errBatch != nil {
		return fmt.Errorf("parallel validation batch engine failed structurally: %w", errBatch)
	}

	// Collect statistics and format cumulative errors from the concurrent output matrix
	validPathsFound := 0
	var cumulativeErrors []string

	for _, res := range results {
		if res.Err != nil {
			cumulativeErrors = append(cumulativeErrors, res.Err.Error())
		} else if res.Data {
			validPathsFound++
		} else {
			// Captured skipped entries that returned (false, nil) from early filtering rules
			cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s skipped during inspection", validDevices[res.Index]))
		}
	}

	// Rule 5: Balance verification. At least one path must be completely validated and healthy.
	if validPathsFound == 0 {
		return fmt.Errorf("zero active paths verified for device target %s; cumulative logs: [%s]", targetDm, strings.Join(cumulativeErrors, "; "))
	}

	logger.Infof("Successfully verified and attached %d multi-path tracks out of %d for lun %d", validPathsFound, len(validDevices), expectedLun)
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


type sgScsiId struct {
	HostID   int32
	Channel  int32
	TargetID int32
	Lun      int32
	Type     int32
	_       int32 // Kernel padding
}

func (r *OsDeviceConnectivityHelperScsiGeneric) purgeScsiGhosts(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dFile, errOpen := os.Open("/dev")
	if errOpen != nil {
		return fmt.Errorf("failed to open /dev: %w", errOpen)
	}
	defer dFile.Close()

	type ghostCandidate struct {
		sgName    string
		hctl      string
		deviceDir string
	}

	// Bounded allocation for chunking
	const chunkSize = 100
	currentBatch := make([]ghostCandidate, 0, chunkSize)

	// =========================================================================
	// OUTER FILTER LOOP: Stream out of /dev and collect matching candidates
	// =========================================================================
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		devEntries, err := dFile.ReadDir(100)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read /dev: %w", err)
		}

		for _, entry := range devEntries {
			sgName := entry.Name()
			
			if !strings.HasPrefix(sgName, "sg") || len(sgName) < 3 {
				continue
			}

			isNumeric := true
			for i := 2; i < len(sgName); i++ {
				if sgName[i] < '0' || sgName[i] > '9' {
					isNumeric = false
					break
				}
			}
			if !isNumeric {
				continue
			}

			// Parse HCTL text from the memory-backed symlink
			deviceDir := filepath.Join("/sys/class/scsi_generic", sgName, "device")
			realSubsysPath, errLink := os.Readlink(deviceDir)
			if errLink != nil {
				continue 
			}

			hctl := filepath.Base(realSubsysPath)
			hctlParts := strings.Split(hctl, ":")
			if len(hctlParts) < 4 {
				continue 
			}

			lunStr := hctlParts[3]
			parsedLun, errParse := strconv.Atoi(lunStr)
			if errParse != nil || parsedLun != expectedLun {
				continue 
			}

			// Add to current bounded chunk slice
			currentBatch = append(currentBatch, ghostCandidate{
				sgName:    sgName,
				hctl:      hctl,
				deviceDir: deviceDir,
			})

			// =========================================================================
			// INNER EXECUTION LOOP: Triggered when exactly 100 matching items are collected
			// =========================================================================
			if len(currentBatch) == chunkSize {
				logger.Infof("Ghost Scrubber: reached %d matched targets. Executing batch.", chunkSize)
				_, errBatch := executer.ExecuteUninterruptibleBatch[ghostCandidate, struct{}](
					ctx, r.KeyedGater, "batch-purge-scsi-ghosts-chunk", 10, 100, 5*time.Second, 30*time.Second, currentBatch,
					func(wCtx context.Context, index int, candidate ghostCandidate, cancelBatch func()) (struct{}, error) {
						vendorBytesRaw, err := os.ReadFile(filepath.Join(candidate.deviceDir, "vendor"))
						if err != nil {
							return struct{}{}, err
						}
						vdr := strings.ToUpper(strings.TrimSpace(string(vendorBytesRaw)))

						ghostState, _ := r.IsSgDeviceGhost(wCtx, candidate.sgName)
						isIbmDevice := strings.Contains(vdr, "IBM")

						pathOwned := r.isPathOwnedByMyArray(wCtx, candidate.sgName, arrayIdentifiers)
						serialNumber, _ := r.getHardwareSerial(wCtx, candidate.deviceDir)

						shouldDelete := (ghostState && isIbmDevice) || (pathOwned && (ghostState || !isIbmDevice || (serialNumber != "" && !r.IsSerialMatch(serialNumber, expectedSerial))))
						if !shouldDelete {
							return struct{}{}, nil
						}

						isOurPath := r.isPathOwnedByMyArray(wCtx, candidate.sgName, arrayIdentifiers)
						isGhost, _ := r.IsSgDeviceGhost(wCtx, candidate.sgName)
						hwSerial, _ := r.getHardwareSerial(wCtx, candidate.deviceDir)

						logger.Warningf("Pruning stale SCSI device %s [Vendor: %s, Serial Match: %v, Ghost: %v, Our path: %v]. Executing hot-unplug.", candidate.sgName, vdr, r.IsSerialMatch(hwSerial, expectedSerial), isGhost, isOurPath)

						deletePath := filepath.Join(candidate.deviceDir, "delete")
						if _, errStat := os.Stat(deletePath); os.IsNotExist(errStat) {
							deletePath = fmt.Sprintf("/sys/bus/scsi/devices/%s/delete", candidate.hctl)
						}

						if errWrite := os.WriteFile(deletePath, []byte("1"), 0200); errWrite != nil {
							logger.Errorf("Ghost Scrubber: failed to issue un-plug write configuration for target node %s: %v", candidate.sgName, errWrite)
							return struct{}{}, errWrite
						}
						return struct{}{}, nil
					},
				)
				if errBatch != nil {
					return fmt.Errorf("parallel ghost batch engine execution failed: %w", errBatch)
				}

				// Reset the slice length to 0 to reuse memory without reallocating
				currentBatch = currentBatch[:0]
			}
		}

		if len(devEntries) < 100 || err == io.EOF {
			break
		}
	}

	// =========================================================================
	// FLUSH REMAINING TAIL ITEMS: Process anything left over under 100 elements
	// =========================================================================
	if len(currentBatch) > 0 {
		logger.Infof("Ghost Scrubber: flushing final tail chunk of %d matched targets.", len(currentBatch))
		_, errBatch := executer.ExecuteUninterruptibleBatch[ghostCandidate, struct{}](
			ctx, r.KeyedGater, "batch-purge-scsi-ghosts-tail", 10, 100, 5*time.Second, 30*time.Second, currentBatch,
			func(wCtx context.Context, index int, candidate ghostCandidate, cancelBatch func()) (struct{}, error) {
				vendorBytesRaw, err := os.ReadFile(filepath.Join(candidate.deviceDir, "vendor"))
				if err != nil {
					return struct{}{}, err
				}
				vdr := strings.ToUpper(strings.TrimSpace(string(vendorBytesRaw)))

				ghostState, _ := r.IsSgDeviceGhost(wCtx, candidate.sgName)
				isIbmDevice := strings.Contains(vdr, "IBM")

				pathOwned := r.isPathOwnedByMyArray(wCtx, candidate.sgName, arrayIdentifiers)
				serialNumber, _ := r.getHardwareSerial(wCtx, candidate.deviceDir)

				shouldDelete := (ghostState && isIbmDevice) || (pathOwned && (ghostState || !isIbmDevice || (serialNumber != "" && !r.IsSerialMatch(serialNumber, expectedSerial))))
				if !shouldDelete {
					return struct{}{}, nil
				}

				isOurPath := r.isPathOwnedByMyArray(wCtx, candidate.sgName, arrayIdentifiers)
				isGhost, _ := r.IsSgDeviceGhost(wCtx, candidate.sgName)
				hwSerial, _ := r.getHardwareSerial(wCtx, candidate.deviceDir)

				logger.Warningf("Pruning stale SCSI device %s [Vendor: %s, Serial Match: %v, Ghost: %v, Our path: %v]. Executing hot-unplug.", candidate.sgName, vdr, r.IsSerialMatch(hwSerial, expectedSerial), isGhost, isOurPath)

				deletePath := filepath.Join(candidate.deviceDir, "delete")
				if _, errStat := os.Stat(deletePath); os.IsNotExist(errStat) {
					deletePath = fmt.Sprintf("/sys/bus/scsi/devices/%s/delete", candidate.hctl)
				}

				if errWrite := os.WriteFile(deletePath, []byte("1"), 0200); errWrite != nil {
					logger.Errorf("Ghost Scrubber: failed to issue un-plug write configuration for target node %s: %v", candidate.sgName, errWrite)
					return struct{}{}, errWrite
				}
				return struct{}{}, nil
			},
		)
		if errBatch != nil {
			return fmt.Errorf("parallel ghost tail batch engine execution failed: %w", errBatch)
		}
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

	// 1. FAST & FAILSAFE: Read /dev entries to find active nodes without /sys/block overhead
	devEntries, err := os.ReadDir("/dev")
	if err != nil {
		logger.Warningf("Ghost Scrubber: failed to read /dev safely: %v", err)
		return fmt.Errorf("failed to read /dev: %w", err)
	}

	var deletedCount int64
	const chunkSize = 100
	currentBatch := make([]string, 0, chunkSize)

	// Inner execution loop logic wrapped as a simple reusable helper block
	processBatch := func(batch []string) error {
		if len(batch) == 0 {
			return nil
		}

		logger.Infof("NVMe Ghost Scrubber: processing chunk of %d targets", len(batch))
		_, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
			ctx,
			r.KeyedGater,
			"batch-purge-nvme-ghosts-chunk",
			10,  // maxRunning: balances NVMe subsystem controller unbind and ioctl loads
			100, // maxSpare
			5*time.Second,
			30*time.Second,
			batch,
			func(wCtx context.Context, index int, name string, cancelBatch func()) (struct{}, error) {
				deviceNode := filepath.Join("/dev", name)

				// Step A: Attempt to open the device node directly to detect dead/disconnected channels
				df, err := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0)
				if err != nil {
					logger.Warningf("Ghost Scrubber: Found disconnected/dead NVMe path %s. Triggering cleanup.", name)
					r.executeNvmeTeardown(wCtx, name)
					atomic.AddInt64(&deletedCount, 1)
					return struct{}{}, nil
				}

				// Step B: Use ioctl to extract unique hardware identifiers directly out of kernel memory structures
				var nvmeInfo nvmeIdTarget
				_, _, errno := syscall.Syscall(
					syscall.SYS_IOCTL,
					df.Fd(),
					uintptr(NVME_IOCTL_ID_TARGET),
					uintptr(unsafe.Pointer(&nvmeInfo)),
				)
				df.Close()

				if errno != 0 {
					logger.Warningf("Ghost Scrubber: ioctl failed for %s. Triggering cleanup.", name)
					r.executeNvmeTeardown(wCtx, name)
					atomic.AddInt64(&deletedCount, 1)
					return struct{}{}, nil
				}

				// Step C: Convert the raw binary array into the exact string layout your tracking expects
				normHwId := normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))

				// Hardware ID identity verification match pass
				if len(normHwId) == 32 && normHwId != expectedNvmeTarget {
					logger.Warningf("Ghost Scrubber: Found rogue NVMe map %s with volume ID mismatch (got %s, exp %s). Forcing isolated namespace removal.", name, normHwId, expectedNvmeTarget)
					r.executeNvmeTeardown(wCtx, name)
					atomic.AddInt64(&deletedCount, 1)
				}

				return struct{}{}, nil
			},
		)

		if errBatch != nil {
			return fmt.Errorf("parallel NVMe batch chunk execution failed: %w", errBatch)
		}
		return nil
	}

	// =========================================================================
	// OUTER GATHERING PASS: Collect items up to the batch limit boundary
	// =========================================================================
	for _, entry := range devEntries {
		name := entry.Name()
		
		if !nvmeNamespaceRegex.MatchString(name) {
			continue
		}
		if !r.isPathOwnedByMyArray(ctx, name, arrayIdentifiers) {
			continue
		}

		currentBatch = append(currentBatch, name)

		// Trigger inner parallel batch logic instantly when chunk capacity thresholds are hit
		if len(currentBatch) == chunkSize {
			if errChunk := processBatch(currentBatch); errChunk != nil {
				return errChunk
			}
			currentBatch = currentBatch[:0] // Reset length to 0 to keep the slice memory completely stable
		}
	}

	// =========================================================================
	// TAIL FLUSH PASS: Clean out any remaining items lower than chunk limits
	// =========================================================================
	if len(currentBatch) > 0 {
		if errChunk := processBatch(currentBatch); errChunk != nil {
			return errChunk
		}
	}

	if finalDeleted := atomic.LoadInt64(&deletedCount); finalDeleted > 0 {
		logger.Infof("Ghost Scrubber: Wiped %d non-matching or ghost NVMe hardware maps.", finalDeleted)
	}
	return nil
}

// executeNvmeTeardown processes clean storage namespace removals with zero internal framework nesting.
// Dynamically expands internal relative symlinks to guarantee absolute traversal evaluation accuracy.
func (r *OsDeviceConnectivityHelperScsiGeneric) executeNvmeTeardown(ctx context.Context, nvmeBlockName string) {
	ctrlName := ""
	baseBlockName := nvmeBlockName 

	if lastNIdx := strings.LastIndex(nvmeBlockName, "n"); lastNIdx != -1 && lastNIdx > 0 {
		baseCtrl := nvmeBlockName[:lastNIdx]
		if cIdx := strings.Index(baseCtrl, "c"); cIdx != -1 {
			ctrlName = baseCtrl[:cIdx] 
			baseBlockName = ctrlName + nvmeBlockName[lastNIdx:] 
		} else {
			ctrlName = baseCtrl 
		}
	}
	
	if ctrlName == "" {
		ctrlName = "generic"
	}

	gaterKey := fmt.Sprintf("nvme-teardown-%s", ctrlName)

	_, _ = executer.ExecuteUninterruptible[struct{}](
		ctx,
		r.KeyedGater,
		gaterKey, 
		10, // maxRunning limits overlapping unbind sweeps per distinct controller
		50, // maxSpare
		2*time.Second, 
		15*time.Second,
		func(wCtx context.Context) (struct{}, error) {
			
			// 1. Try targeting the namespace specific deletion endpoint first
			deleteNsPath := filepath.Join("/sys/block", nvmeBlockName, "device", "delete")
			if _, err := os.Stat(deleteNsPath); err == nil {
				logger.Infof("[Purge-Scrubber] [%s] Safely deleting isolated namespace node path: %s", ctrlName, nvmeBlockName)
				_ = os.WriteFile(deleteNsPath, []byte("1\n"), 0200)
				return struct{}{}, nil
			}

			// 2. Fallback strategy: Handle alternative endpoint tracking configurations
			fallbackNsPath := filepath.Join("/sys/block", nvmeBlockName, "wwid")
			if _, err := os.Stat(fallbackNsPath); err == nil {
				deleteAltPath := filepath.Join("/sys/block", nvmeBlockName, "delete")
				if _, err := os.Stat(deleteAltPath); err == nil {
					logger.Infof("[Purge-Scrubber] [%s] Safely deleting via alternative namespace path: %s", ctrlName, deleteAltPath)
					_ = os.WriteFile(deleteAltPath, []byte("1\n"), 0200)
					return struct{}{}, nil
				}
			}

			// 3. Legacy Fallback (Rule 3 Layout Parity for RHEL 7 Environments)
			if ctrlName != "generic" {
				pciUeventPath := fmt.Sprintf("/sys/class/nvme/%s/device/uevent", ctrlName) 
				if _, err := os.Stat(pciUeventPath); err == nil {
					ueventStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, pciUeventPath)
					if err == nil {
						for _, line := range strings.Split(ueventStr, "\n") {
							if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
								pciAddress := strings.TrimPrefix(line, "PCI_SLOT_NAME=")
								unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
								if _, err := os.Stat(unbindPath); err == nil {
									logger.Warningf("[Purge-Scrubber-Legacy] Unbinding standalone controller %s at PCI address %s via uevent metadata", ctrlName, pciAddress)
									_ = os.WriteFile(unbindPath, []byte(pciAddress), 0200)
									return struct{}{}, nil
								}
							}
						}
					}
				}

				// FLATTENED & HARDENED (Rule 1/5): Replaced nested frameworks with direct link extraction.
				deviceDirLink := filepath.Join("/sys/class/nvme", ctrlName, "device")
				pciAddrPath, errLink := os.Readlink(deviceDirLink)
				if errLink == nil {
					// EXPANSION FIX: Safely reconstruct the relative symlink target back to an absolute canonical string 
					// to eliminate directory depth truncation variations across different Linux distributions.
					if !filepath.IsAbs(pciAddrPath) {
						pciAddrPath = filepath.Clean(filepath.Join(filepath.Dir(deviceDirLink), pciAddrPath))
					}
					
					pciAddress := filepath.Base(pciAddrPath)
					unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
					if _, err := os.Stat(unbindPath); err == nil {
						logger.Warningf("[Purge-Scrubber-Legacy] Unbinding standalone controller %s at PCI address %s via readlink fallback", ctrlName, pciAddress)
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

	// 1. FAST & FAILSAFE: Read /dev entries to find active nodes without /sys/block overhead
	devEntries, err := os.ReadDir("/dev")
	if err != nil {
		return fmt.Errorf("failed to read /dev safely: %w", err)
	}

	normExpected := normalizeWWID(expectedWWID)
	var deletedCount int64
	const chunkSize = 100
	currentBatch := make([]string, 0, chunkSize)

	// Reusable batch executor block for chunk processing
	processBatch := func(batch []string) error {
		if len(batch) == 0 {
			return nil
		}

		logger.Infof("NVMe Prune Scrubber: processing chunk of %d targets", len(batch))
		_, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
			ctx,
			r.KeyedGater,
			"batch-prune-nvme-ghosts-chunk",
			10,  // maxRunning: balances NVMe subsystem controller unbind and ioctl loads
			100, // maxSpare
			5*time.Second,
			35*time.Second, // Bounded timeout for full chunk analysis and unbind actions
			batch,
			func(wCtx context.Context, index int, name string, cancelBatch func()) (struct{}, error) {
				baseBlockName := name
				if strings.Contains(name, "c") {
					if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
						if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
							baseBlockName = name[:cIdx] + name[lastNIdx:]
						}
					}
				}

				deviceDir := filepath.Join("/sys/block", name, "device")
				subsysNqnPath := filepath.Join(deviceDir, "subsysnqn")
				
				// Read SubsysNQN safely under security gate structures
				nqnData, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, subsysNqnPath)
				if err != nil {
					return struct{}{}, nil // Skip unreachable or disconnecting layout noise
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
					return struct{}{}, nil
				}
				
				// Step A: Attempt to open the device node in non-blocking mode
				deviceNode := filepath.Join("/dev", name)
				df, err := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0)
				
				var wwid string
				isGhost := false

				if err != nil {
					isGhost = true
				} else {
					// Step B: Fetch unique ID via ioctl to minimize disk I/O latency
					var nvmeInfo nvmeIdTarget
					_, _, errno := syscall.Syscall(
						syscall.SYS_IOCTL,
						df.Fd(),
						uintptr(NVME_IOCTL_ID_TARGET),
						uintptr(unsafe.Pointer(&nvmeInfo)),
					)
					df.Close()

					if errno != 0 {
						isGhost = true
					} else {
						wwid = fmt.Sprintf("%x", nvmeInfo.Nguid)
						isGhost = r.isNvmeGhost(wCtx, name) 
					}
				}
				
				var state string
				if isGhost {
					state, _ = secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(deviceDir, "state"))
				}

				isMismatch := (wwid != "" && normalizeWWID(wwid) != normExpected)

				if isGhost || isMismatch {
					logger.Warningf("Ghost Scrubber: Pruning stale NVMe device %s. State: %s, WWID Match: %v", name, state, !isMismatch)

					ctrlName := ExtractNvmeControllerBase(name)
					if ctrlName == "" {
						ctrlName = "generic"
					}

					// RULE 1 REMEDY: Since this execution code path is already running inside an isolated batch lane,
					// we flatten the destructive un-plug tasks into direct executions to avoid nested gate reservation leaks.
					deleteNsPath := filepath.Join("/sys/block", name, "device", "delete")
					if _, err := os.Stat(deleteNsPath); err == nil {
						logger.Infof("Ghost Scrubber: Safely deleting namespace endpoint via %s", deleteNsPath)
						_ = os.WriteFile(deleteNsPath, []byte("1\n"), 0200)
						atomic.AddInt64(&deletedCount, 1)
						return struct{}{}, nil
					}

					fallbackNsPath := filepath.Join("/sys/block", name, "delete")
					if _, err := os.Stat(fallbackNsPath); err == nil {
						_ = os.WriteFile(fallbackNsPath, []byte("1\n"), 0200)
						atomic.AddInt64(&deletedCount, 1)
						return struct{}{}, nil
					}

					if ctrlName != "generic" {
						pciUeventPath := fmt.Sprintf("/sys/class/nvme/%s/device/uevent", ctrlName)
						if _, err := os.Stat(pciUeventPath); err == nil {
							ueventStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, pciUeventPath)
							if err == nil {
								for _, line := range strings.Split(ueventStr, "\n") {
									if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
										pciAddress := strings.TrimPrefix(line, "PCI_SLOT_NAME=")
										unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
										if _, err := os.Stat(unbindPath); err == nil {
											logger.Warningf("Ghost Scrubber [RHEL7]: Unbinding controller %s via PCI slot address %s", ctrlName, pciAddress)
											_ = os.WriteFile(unbindPath, []byte(pciAddress), 0200)
											atomic.AddInt64(&deletedCount, 1)
											return struct{}{}, nil
										}
									}
								}
							}
						}

						// Rule 3 Compatibility: Direct memory-backed link lookup fallback
						pciAddrPath, errLink := os.Readlink(filepath.Join("/sys/class/nvme", ctrlName, "device"))
						if errLink == nil {
							pciAddress := filepath.Base(pciAddrPath)
							unbindPath := "/sys/bus/pci/drivers/nvme/unbind"
							if _, err := os.Stat(unbindPath); err == nil {
								logger.Warningf("Ghost Scrubber [RHEL7 Fallback]: Unbinding controller %s via PCI link address %s", ctrlName, pciAddress)
								_ = os.WriteFile(unbindPath, []byte(pciAddress), 0200)
								atomic.AddInt64(&deletedCount, 1)
								return struct{}{}, nil
							}
						}
					}
					return struct{}{}, fmt.Errorf("controller delete interface missing for target: %s", name)
				}

				return struct{}{}, nil
			},
		)

		if errBatch != nil {
			return fmt.Errorf("parallel NVMe sweep batch chunk processing failed: %w", errBatch)
		}
		return nil
	}

	// =========================================================================
	// OUTER GATHERING PASS: Collect items up to the batch limit boundary
	// =========================================================================
	for _, entry := range devEntries {
		name := entry.Name()
		
		if !nvmeNamespaceRegex.MatchString(name) {
			continue
		}

		currentBatch = append(currentBatch, name)

		if len(currentBatch) == chunkSize {
			if errChunk := processBatch(currentBatch); errChunk != nil {
				return errChunk
			}
			currentBatch = currentBatch[:0] // Reset chunk length safely to keep memory footprint flat
		}
	}

	// =========================================================================
	// TAIL FLUSH PASS: Finalize remaining entries left over under chunk limits
	// =========================================================================
	if len(currentBatch) > 0 {
		if errChunk := processBatch(currentBatch); errChunk != nil {
			return errChunk
		}
	}

	if finalDeleted := atomic.LoadInt64(&deletedCount); finalDeleted > 0 {
		logger.Infof("Ghost Scrubber: Native NVMe sweep complete. Cleared %d rogue fabric resources.", finalDeleted)
	}
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) GetHCTLFromSg(ctx context.Context, sgName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cleanSgName := filepath.Base(sgName)
	deviceLink := filepath.Join("/sys/class/scsi_generic", cleanSgName, "device")
	
	realPath, err := os.Readlink(deviceLink)
	if err != nil {
		return "", fmt.Errorf("failed resolving scsi generic path link: %w", err)
	}
	
	hctl := filepath.Base(strings.TrimSuffix(realPath, "/"))
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("malformed format address index generated: %s", hctl)
	}
	return hctl, nil
}

// 1. GATEWAY: Direct and clear entry point regulating protocol limits
func (r *OsDeviceConnectivityHelperScsiGeneric) isPathOwnedByMyArray(ctx context.Context, deviceName string, arrayIdentifiers []string) bool {
	if err := ctx.Err(); err != nil {
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

		timer := time.NewTimer(backoff[i])
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
			timer.Stop()
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
		20, 100, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) ([]string, error) {
			return r.resolveTargetIDs(wCtx, baseDeviceName)
		},
	)
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getNvmeSubsysNQN(ctx context.Context, deviceName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	rawName := filepath.Base(deviceName)
	baseBlockName := rawName
	
	if strings.Contains(rawName, "c") {
		if lastNIdx := strings.LastIndex(rawName, "n"); lastNIdx != -1 && lastNIdx > 0 {
			if cIdx := strings.Index(rawName, "c"); cIdx != -1 && cIdx < lastNIdx {
				baseBlockName = rawName[:cIdx] + rawName[lastNIdx:]
			}
		}
	}

	deviceCtrl := ExtractNvmeControllerBase(rawName)

	// Tier 1 Check
	nqnPath := fmt.Sprintf("/sys/class/nvme/%s/subsysnqn", deviceCtrl)
	dataStr, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, nqnPath)
	
	if err != nil {
		// Tier 2 Fallback
		nqnPath = fmt.Sprintf("/sys/block/%s/device/subsysnqn", baseBlockName)
		dataStr, err = secureReadSysfs(ctx, r.KeyedGater, baseBlockName, nqnPath)
		
		if err != nil {
			// Tier 3 Fallback
			subsysDirSymlink := fmt.Sprintf("/sys/block/%s/device/subsystem", rawName)
			realSubsysPath, symErr := os.Readlink(subsysDirSymlink)
			
			if symErr == nil {
				// EXPANSION FIX: Safely convert the relative symlink target string into a clean, 
				// absolute path representation to ensure the intermediate path substring check matches correctly.
				if !filepath.IsAbs(realSubsysPath) {
					realSubsysPath = filepath.Clean(filepath.Join(filepath.Dir(subsysDirSymlink), realSubsysPath))
				}

				if strings.Contains(realSubsysPath, "virtual/nvme-subsys") {
					nqnPath = filepath.Join(realSubsysPath, "subsysnqn")
					dataStr, err = secureReadSysfs(ctx, r.KeyedGater, baseBlockName, nqnPath)
				}
			}
			
			if err != nil {
				return "", fmt.Errorf("failed to locate nvme subsysnqn across all standard validation layers for block target '%s': %w", rawName, err)
			}
		}
	}
	
	return strings.TrimSpace(dataStr), nil
}

// 2. RECURSION TRAVERSAL LAYER: High-speed, memory-bounded topology resolution
func (r *OsDeviceConnectivityHelperScsiGeneric) resolveTargetIDs(ctx context.Context, deviceName string) ([]string, error) {
	const maxRecursionDepth = 3
	return r.resolveTargetIDsRecursive(ctx, deviceName, 0, maxRecursionDepth)
}


// TODO this should replace all checks for HasPrefix "sd"
// IsScsiBlockDevice safely verifies if a device is managed by the kernel SCSI core subsystem.
// Fully backwards-compatible with RHEL 7 kernels and immune to custom naming schemes.
// 3. STORAGE PROTOCOL INTERFACES LAYER: Protected by your explicit hardware limits
func (r *OsDeviceConnectivityHelperScsiGeneric) IsScsiBlockDevice(ctx context.Context, devName string) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	cleanName := filepath.Base(devName)
	if strings.HasPrefix(cleanName, "sd") || strings.HasPrefix(cleanName, "dasd") {
		return true
	}

	subsysPath := filepath.Join("/sys/block", cleanName, "device", "subsystem")
	realSubsysPath, err := os.Readlink(subsysPath)
	if err == nil {
		if strings.Contains(realSubsysPath, "bus/scsi") || strings.Contains(realSubsysPath, "scsi") {
			return true
		}
	}
	return false
}

func (r *OsDeviceConnectivityHelperScsiGeneric) resolveTargetIDsRecursive(ctx context.Context, deviceName string, currentDepth, maxDepth int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if currentDepth > maxDepth {
		return nil, fmt.Errorf("exceeded maximum recursion limit (%d)", maxDepth)
	}

	baseName := filepath.Base(deviceName)

	// Subsystem A: Device Mapper (Multipath) Layout
	if strings.HasPrefix(baseName, "dm-") {
		slavesPath := fmt.Sprintf("/sys/block/%s/slaves", baseName)
		
		dFile, errOpen := os.Open(slavesPath)
		if errOpen != nil {
			return nil, fmt.Errorf("failed to open dm slaves path: %w", errOpen)
		}
		defer dFile.Close()

		uniqueIDs := make(map[string]struct{})
		var lastErr error

		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			entries, readErr := dFile.ReadDir(100)
			if readErr != nil {
				if readErr == io.EOF {
					break
				}
				return nil, fmt.Errorf("failed to read dm slaves: %w", readErr)
			}
			if len(entries) == 0 {
				break
			}

			for _, entry := range entries {
				slaveName := entry.Name()
				ids, errRecursive := r.resolveTargetIDsRecursive(ctx, slaveName, currentDepth+1, maxDepth)
				if errRecursive != nil {
					lastErr = errRecursive
					continue
				}
				for _, id := range ids {
					if id != "" {
						uniqueIDs[id] = struct{}{}
					}
				}
			}
		}

		if len(uniqueIDs) > 0 {
			collectedIDs := make([]string, 0, len(uniqueIDs))
			for id := range uniqueIDs {
				collectedIDs = append(collectedIDs, id)
			}
			return collectedIDs, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no identifiable slave legs on %s", baseName)
	}

	// Subsystem B: Native NVMe Fabrics Layout
	if nvmeNamespaceRegex.MatchString(baseName) || strings.HasPrefix(baseName, "nvme") {
		nqn, err := r.getNvmeSubsysNQN(ctx, baseName)
		if err != nil {
			return nil, err
		}
		return []string{nqn}, nil
	}

	// Subsystem C: Canonical SCSI Layout
	var hctl string
	var err error
	if strings.HasPrefix(baseName, "sg") {
		hctl, err = r.GetHCTLFromSg(ctx, baseName)
	} else if strings.HasPrefix(baseName, "sd") || r.IsScsiBlockDevice(ctx, baseName) {
		hctl, err = r.getHCTLFromSd(ctx, baseName)
	} else {
		return nil, fmt.Errorf("unsupported interface structure: %s", baseName)
	}

	if err != nil {
		return nil, err
	}

	targetID := r.getScsiTargetID(ctx, hctl)
	if targetID == "" {
		return nil, fmt.Errorf("scsi target registration state attributes not ready for address %s", hctl)
	}

	return []string{targetID}, nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) getHCTLFromSd(ctx context.Context, sdName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cleanSdName := filepath.Base(sdName)
	deviceLink := filepath.Join("/sys/block", cleanSdName, "device")
	
	realPath, err := os.Readlink(deviceLink)
	if err != nil {
		return "", fmt.Errorf("failed resolving sd path reference: %w", err)
	}
	
	hctl := filepath.Base(strings.TrimSuffix(realPath, "/"))
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("malformed layout index derived: %s", hctl)
	}
	return hctl, nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetID(ctx context.Context, hctl string) string {
	logger.Infof("[SCSI-Target-Inspector] Entering identification pipeline for target address: [%s]", hctl)

	if err := ctx.Err(); err != nil {
		logger.Warningf("[SCSI-Target-Inspector] [%s] Aborting execution: incoming context already cancelled: %v", hctl, err)
		return ""
	}

	parts := strings.Split(hctl, ":")
	if len(parts) < 4 {
		logger.Warningf("[SCSI-Target-Inspector] [%s] Aborting execution: malformed HCTL layout segment footprint", hctl)
		return ""
	}

	hostID := parts[0] // Isolate the host bus index primitive string (e.g., "10", "13", "14")
	hct := strings.Join(parts[:3], ":")
	targetDirName := fmt.Sprintf("target%s", hct)

	// =========================================================================
	// 1. FIBRE CHANNEL RESOLUTION LAYER (FLAT REMOTE PORT CLASS STRATEGY)
	// =========================================================================
	fcClassDir := "/sys/class/fc_remote_ports"
	fcClassPattern := fmt.Sprintf("/sys/class/fc_remote_ports/rport-%s:*", hostID)
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1] Executing modern remote port wildcard scan: %s", hctl, fcClassPattern)

	// Hybrid Attempt 1: Direct Path Calculation (Fast-path for modern kernels rport-H:C-T)
	if len(parts) >= 3 {
		channelID := parts[1]
		targetID := parts[2]
		directPortFile := filepath.Join(fcClassDir, fmt.Sprintf("rport-%s:%s-%s", hostID, channelID, targetID), "port_name")

		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Direct] Trying direct path resolution: %s", hctl, directPortFile)
		if data, errRead := os.ReadFile(directPortFile); errRead == nil && len(data) > 0 {
			wwpn := strings.TrimSpace(string(data))
			logger.Infof("[SCSI-Target-Inspector] [%s] [FC-rport-FastPath SUCCESS] Hardware target verified via DIRECT class rport node. Isolated WWPN: %s", hctl, wwpn)
			return wwpn // IMMEDIATE SUCCESSFUL EXIT
		}
	}

	// Hybrid Attempt 2: ReadDir Fallback with CHUNKING
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Fallback] Direct path missed. Falling back to chunked directory iteration.", hctl)
	if dFile, errOpen := os.Open(fcClassDir); errOpen == nil {
		prefixSearch := fmt.Sprintf("rport-%s:", hostID)

		for {
			rportEntries, errDirs := dFile.ReadDir(100)
			if errDirs != nil && errDirs != io.EOF {
				logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Error] Error reading chunk from fc_remote_ports: %v", hctl, errDirs)
				break
			}

			for _, entry := range rportEntries {
				rportName := entry.Name()
				if !strings.HasPrefix(rportName, prefixSearch) {
					continue
				}

				fcPortFile := filepath.Join(fcClassDir, rportName, "port_name")
				logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Loop] Probing target file: %s", hctl, fcPortFile)

				if data, errRead := os.ReadFile(fcPortFile); errRead == nil && len(data) > 0 {
					wwpn := strings.TrimSpace(string(data))
					logger.Infof("[SCSI-Target-Inspector] [%s] [FC-rport-FastPath SUCCESS] Hardware target verified via class rport node. Isolated WWPN: %s", hctl, wwpn)
					dFile.Close()
					return wwpn // IMMEDIATE SUCCESSFUL FC EXIT
				} else if errRead != nil {
					logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Loop] Skipped file %s: read failed or inaccessible: %v", hctl, fcPortFile, errRead)
				}
			}

			if len(rportEntries) < 100 || errDirs == io.EOF {
				break
			}
		}
		dFile.Close()
	} else {
		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Skip] Wildcard scan returned zero active class fc_remote_ports entries or thrown error: %v", hctl, errOpen)
	}

	// Track A2: Classic Target Layout Fallback (RHEL 7)
	fcClassicPath := fmt.Sprintf("/sys/class/scsi_target/%s/fc_transport/%s/port_name", targetDirName, targetDirName)
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A2] Falling back to classic target layout file probe: %s", hctl, fcClassicPath)

	if data, errRead := os.ReadFile(fcClassicPath); errRead == nil && len(data) > 0 {
		wwpn := strings.TrimSpace(string(data))
		logger.Infof("[SCSI-Target-Inspector] [%s] [FC-Classic-FastPath SUCCESS] Hardware target verified via classic scsi_target class. Isolated WWPN: %s", hctl, wwpn)
		return wwpn // IMMEDIATE SUCCESSFUL FC EXIT
	} else {
		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A2-Skip] Classic target file read failed or missing footprint: %v", hctl, errRead)
	}

	// =========================================================================
	// 2. SERIAL ATTACHED SCSI RESOLUTION LAYER (SAS STRATEGY)
	// =========================================================================
	sasClassicPath := fmt.Sprintf("/sys/class/scsi_target/%s/sas_device/%s/sas_address", targetDirName, targetDirName)
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-B] Probing SAS transport class tree layout path: %s", hctl, sasClassicPath)

	if data, errRead := os.ReadFile(sasClassicPath); errRead == nil && len(data) > 0 {
		sasAddr := strings.TrimSpace(string(data))
		logger.Infof("[SCSI-Target-Inspector] [%s] [SAS-Class-FastPath SUCCESS] Hardware target verified via sas_device class. Isolated Address: %s", hctl, sasAddr)
		return sasAddr // IMMEDIATE SUCCESSFUL SAS EXIT
	} else {
		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-B-Skip] SAS class file read failed or missing footprint: %v", hctl, errRead)
	}

	// =========================================================================
	// 3. iSCSI RESOLUTION LAYER (FLAT SUBSYSTEM SESSION LOOKUP WITH CHUNKING)
	// =========================================================================
	sessionClassPath := "/sys/class/iscsi_session"
	matchToken := fmt.Sprintf("host%s", hostID)
	logger.Infof("[SCSI-Target-Inspector] [%s] [Track-C] Initiating flat class iSCSI session lookup sweep at: %s", hctl, sessionClassPath)

	sFile, errOpen := os.Open(sessionClassPath)
	if errOpen != nil {
		logger.Warningf("[SCSI-Target-Inspector] [%s] [Track-C-Error] Global iSCSI class directory tree lookup failed: %v", hctl, errOpen)
		return ""
	}
	defer sFile.Close()

	// Real Linux kernels map /sys/class/iscsi_session to /sys/devices/platform/...
	// Read this mapping layer once upfront to get the true canonical base directory.
	var canonicalClassBase string
	if realClassBase, err := os.Readlink(sessionClassPath); err == nil {
		if filepath.IsAbs(realClassBase) {
			canonicalClassBase = realClassBase
		} else {
			canonicalClassBase = filepath.Clean(filepath.Join(filepath.Dir(sessionClassPath), realClassBase))
		}
	} else {
		canonicalClassBase = sessionClassPath // Fallback to raw path if not a link
	}

	for {
		sessions, errDirs := sFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			logger.Warningf("[SCSI-Target-Inspector] [%s] [Track-C-Error] Error reading iSCSI sessions chunk: %v", hctl, errDirs)
			break
		}

		for _, s := range sessions {
			if ctx.Err() != nil {
				logger.Warningf("[SCSI-Target-Inspector] [%s] [Track-C] Context expired during dynamic session loop traversal.", hctl)
				return ""
			}

			sessionName := s.Name() // e.g., "session13"
			deviceMappingLink := filepath.Join(sessionClassPath, sessionName, "device")
			logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop] Evaluating node: %s. Reading mapping token link: %s", hctl, sessionName, deviceMappingLink)

			if hostPath, errLink := os.Readlink(deviceMappingLink); errLink == nil {
				logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop] Entry %s symlink target text resolved to: [%s]", hctl, sessionName, hostPath)

				// CORRECT PATH MATH: Combine the canonical base folder path with the 
				// session sub-folder name and the relative symlink text hops.
				// This perfectly resolves to /sys/devices/platform/host13/session13
				trueHostPath := filepath.Clean(filepath.Join(canonicalClassBase, sessionName, hostPath))
				
				logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop] True absolute VFS path resolved successfully: [%s]", hctl, trueHostPath)

				if strings.Contains(trueHostPath, matchToken) {
					targetNameFile := filepath.Join(sessionClassPath, sessionName, "targetname")
					logger.Infof("[SCSI-Target-Inspector] [%s] [Track-C-Loop] Correlation successful! Target name file identified: %s", hctl, targetNameFile)

					if data, errRead := os.ReadFile(targetNameFile); errRead == nil && len(data) > 0 {
						iqnString := strings.TrimSpace(string(data))
						logger.Infof("[SCSI-Target-Inspector] [%s] [iSCSI-Class-FastPath SUCCESS] Hardware target verified via session map. Isolated IQN: %s", hctl, iqnString)
						return iqnString // IMMEDIATE SUCCESSFUL iSCSI EXIT
					} else {
						logger.Warningf("[SCSI-Target-Inspector] [%s] [Track-C-Loop-Error] Matched session %s but targetname file read failed: %v", hctl, sessionName, errRead)
					}
				} else {
					logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop-Mismatch] Entry %s rejected: does not match lookup token %s or target pattern parameters.", hctl, sessionName, matchToken)
				}
			} else {
				logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop-Skip] Entry %s device link mapping unreadable or incomplete: %v", hctl, sessionName, errLink)
			}
		}

		if len(sessions) < 100 || errDirs == io.EOF {
			break
		}
	}

	// =========================================================================
	// 4. TERMINAL FALLBACK BLOCK
	// =========================================================================
	logger.Warningf("[SCSI-Target-Inspector] [%s] [OUT OF STRATEGIES] Identification analysis complete. Zero protocol matches isolated across all system class mappings.", hctl)
	return ""
}

// getIscsiTargetName identifies the operational iSCSI target name with full D-state protection.
func (r *OsDeviceConnectivityHelperScsiGeneric) getIscsiTargetName(ctx context.Context, realDevicePath string, parentTargetBase string, hostID string) string {
	logger.Infof("      [iSCSI-Subsystem-Scout] Entering dynamic session tracking pipeline. Path: %s", realDevicePath)
	
	if err := ctx.Err(); err != nil {
		return ""
	}

	// =========================================================================
	// STRATEGY A: DIRECT LEXICAL TOKEN EXTRACTION (TRUE O(1) PERFORMANCE FIX)
	// =========================================================================
	if idx := strings.Index(realDevicePath, "session"); idx != -1 {
		remainingPath := realDevicePath[idx:]
		sessionToken := strings.Split(remainingPath, "/")[0] // Resolves perfectly to "session4"

		if strings.HasPrefix(sessionToken, "session") {
			targetNameFile := fmt.Sprintf("/sys/class/iscsi_session/%s/targetname", sessionToken)
			logger.Debugf("      [iSCSI-Subsystem-Scout] [Strategy-A] Direct session token extracted: %s. Targeting file: %s", sessionToken, targetNameFile)

			if data, err := os.ReadFile(targetNameFile); err == nil && len(data) > 0 {
				iqnString := strings.TrimSpace(string(data))
				logger.Infof("      [iSCSI-Subsystem-Scout] [Strategy-A SUCCESS] Extracted active target IQN natively: %s", iqnString)
				return iqnString // SUCCESSFUL IMMUNE EXIT
			} else {
				logger.Warningf("      [iSCSI-Subsystem-Scout] [Strategy-A Fault] File found via token match but read operation failed: %v", err)
			}
		}
	}

	// =========================================================================
	// STRATEGY B: HARDENED SYSTEM CLASS MAP SWEEP (FALLBACK RUNTIME BLOCK)
	// =========================================================================
	sessionClassPath := "/sys/class/iscsi_session"
	
	dFile, errOpen := os.Open(sessionClassPath)
	if errOpen != nil {
		logger.Warningf("      [iSCSI-Subsystem-Scout] [Strategy-B CRITICAL FAILED] System class folder missing or inaccessible: %v", errOpen)
		return ""
	}
	defer dFile.Close()

	matchToken := fmt.Sprintf("host%s", hostID)
	for {
		if err := ctx.Err(); err != nil {
			return ""
		}

		sessions, errDirs := dFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			logger.Warningf("      [iSCSI-Subsystem-Scout] [Strategy-B Error] Error reading iSCSI sessions chunk: %v", errDirs)
			break
		}

		for _, s := range sessions {
			if err := ctx.Err(); err != nil {
				return ""
			}

			sessionName := s.Name()
			targetNamePath := filepath.Join(sessionClassPath, sessionName, "targetname")
			
			// FAST PATH: Read targetname directly without wrapping overhead
			rawTargetData, errRead := os.ReadFile(targetNamePath)
			if errRead != nil || len(rawTargetData) == 0 {
				continue
			}
			data := string(rawTargetData)
			
			// FAST PATH: Use non-blocking, raw os.Readlink to read the pointer out of memory.
			deviceLinkMappingPath := filepath.Join(sessionClassPath, sessionName, "device")
			hostPath, errLink := os.Readlink(deviceLinkMappingPath)
			if errLink != nil {
				continue
			}
			
			if strings.Contains(hostPath, matchToken) || strings.Contains(realDevicePath, sessionName) {
				sigID := strings.TrimSpace(data)
				logger.Infof("      [iSCSI-Subsystem-Scout] [Strategy-B SUCCESS] Valid target correlated via fallback parameters on %s: %s", sessionName, sigID)
				return sigID
			}
		}

		if len(sessions) < 100 || errDirs == io.EOF {
			break
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



func (r *OsDeviceConnectivityHelperScsiGeneric) isHardwareBlocked(sgName string) bool {
	statePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName)
	state, err := os.ReadFile(statePath)
	if err != nil {
		logger.Debugf("isHardwareBlocked: Cannot read state path %s (%v). Assuming not blocked to allow ghost evaluation.", statePath, err)
		return false 
	}
	
	s := strings.TrimSpace(string(state))

	if s == "blocked" || s == "quiesce" {
		logger.Warningf("Safety-Gate: SCSI device %s is locked in kernel state '%s'. Aborting ioctl to prevent thread hang.", sgName, s)
		return true
	}

	return false
}


func (r *OsDeviceConnectivityHelperScsiGeneric) IsSgDeviceGhost(ctx context.Context, sgName string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	cleanSgName := filepath.Base(sgName)
	deviceBase := fmt.Sprintf("/sys/class/scsi_generic/%s/device", cleanSgName)

	// --- INLINE AGE TRACKING & SCAVENGER ENGINE (Rule 4/5) ---
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

	// --- FLATTENED CONTEXT-RESPECTING SYSFS PARSING ---
	// Replaced the internal ExecuteUninterruptible wrapper with direct, context-respecting 
	// file mapping execution. Since this function runs inside an already-gated parallel batch pool,
	// direct reading ensures top-tier performance while inheriting full context lifecycle cancellation shields.
	var state, peripheralType string
	var blockMissing bool

	stateBytes, _ := os.ReadFile(filepath.Join(deviceBase, "state"))
	state = strings.TrimSpace(string(stateBytes))

	typeBytes, _ := os.ReadFile(filepath.Join(deviceBase, "type"))
	peripheralType = strings.TrimSpace(string(typeBytes))
	if peripheralType == "" {
		peripheralType = "unknown"
	}

	_, blockErr := os.Stat(filepath.Join(deviceBase, "block"))
	blockMissing = os.IsNotExist(blockErr)

	// Handle age verification criteria seamlessly if the file reading layer encounters deadlocks
	if ctx.Err() != nil {
		if deviceAge > 15*time.Second {
			logger.Errorf("[%s] Core sysfs path is deadlocked in D-state for %v. Hard age boundary breached, purging zombie device.", cleanSgName, deviceAge)
			r.discoveryCache.Delete(cleanSgName) // Evict from cache right before deletion
			return true, nil
		}
		return false, ctx.Err()
	}

	// --- CRITICAL PATH PURGE JUDGMENT ---
	if state == "offline" || state == "cancelled" || state == "deleting" {
		r.discoveryCache.Delete(cleanSgName)
		return true, nil
	}
	
	if peripheralType == "31" {
		r.discoveryCache.Delete(cleanSgName)
		return true, nil
	}

	isNotDiskType := peripheralType != "0"

	// FIXED: Standardize the gater key using the specific device name (e.g., ghost-inq-sg3) 
	// instead of a global static string. This ensures that parallel lanes can execute 
	// hardware checks simultaneously without cross-blocking or serializing execution threads.
	ioctlGaterKey := fmt.Sprintf("ghost-inq-%s", cleanSgName)

	isHwGhost, ioctlErr := executer.ExecuteUninterruptible[bool](
		ctx,
		r.KeyedGater,
		ioctlGaterKey,
		15,  // maxRunning for parallel hardware query operations
		100, // maxSpare
		600*time.Millisecond, 
		2*time.Second,        
		func(wCtx context.Context) (bool, error) {
			return r.checkPQviaIoctl(cleanSgName, deviceAge)
		},
	)
	
	if isHwGhost {
		r.discoveryCache.Delete(cleanSgName)
		return true, nil
	}

	// TRACK B: Stuck Initialization / Transient Error Management
	if ioctlErr != nil {
		// If the device is permanently stuck initializing (blocked/quiesce) or has a 
		// missing block directory for > 15 seconds, break the shield and force zombie path eviction.
		if deviceAge >= 15*time.Second {
			logger.Errorf("[%s] Track B (Stuck Initialization Purge): Path has been trapped or missing block directory for %v under error [%v]. Breaking shield to clear zombie space.", cleanSgName, deviceAge, ioctlErr)
			r.discoveryCache.Delete(cleanSgName)
			return true, nil
		}

		if state == "blocked" || state == "quiesce" {
			logger.Debugf("[%s] Track B: Device in birth sequence (Age: %v). Retaining path safely.", cleanSgName, deviceAge)
			return false, nil
		}

		if blockMissing || isNotDiskType {
			logger.Errorf("[%s] Track B: Structural path failure under error [%v]. Purging.", cleanSgName, ioctlErr)
			r.discoveryCache.Delete(cleanSgName)
			return true, nil
		}

		return false, nil
	}

	// Device responded cleanly to the IOCTL, it is alive and functional.
	return false, nil
}

// checkPQviaIoctl performs an age-aware SCSI INQUIRY assessment on a generic node.
func (r *OsDeviceConnectivityHelperScsiGeneric) checkPQviaIoctl(sgName string, deviceAge time.Duration) (bool, error) {
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

		// =========================================================================
		// 1. TRANSPORT LAYER EVALUATION (INVERTED AGE SAFEGUARD)
		// =========================================================================
		if header.host_status != 0 {
			switch header.host_status {
			case 0x05, 0x07, 0x0e: // DID_NO_CONNECT, DID_ERROR, DID_TRANSPORT_FAIL_FAST
				if deviceAge < 30*time.Second {
					logger.Warningf("[%s] IOCTL Probe: Intercepted transport error (0x%02x) during birth sequence (Age: %v). Retaining path safely.", sgName, header.host_status, deviceAge)
					return false, nil // Active initialization/fabric registration sequence step
				}
				logger.Warningf("[%s] IOCTL Probe: SCSI Transport confirmed unrecoverable death after aging window (HostStatus: 0x%02x). Flagging as ghost slot.", sgName, header.host_status)
				return true, nil
			default:
				logger.Debugf("[%s] IOCTL Probe: Fabric dropped transient congestion code (HostStatus: 0x%02x). Defensively retaining path.", sgName, header.host_status)
				return false, fmt.Errorf("transient transport fabric blockage detected (HostStatus: 0x%02x), bypassing deletion", header.host_status)
			}
		}

		// =========================================================================
		// 2. PROTOCOL LAYER EVALUATION (INVERTED AGE SAFEGUARD)
		// =========================================================================
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

				// Hard Ghost Indicators: These explicitly prove the logical mapping layout is detached or unbacked
				isHardGhostCondition := (senseKey == 0x02 && asc == 0x3A) || // Medium Not Present
					(senseKey == 0x05 && asc == 0x24 && ascq == 0x00)       // Invalid Field in CDB

				if isHardGhostCondition {
					logger.Warningf("[%s] IOCTL Probe: Hard structural ghost confirmed via SCSI Check Condition (%02x/%02x). Flagging as ghost slot.", sgName, senseKey, asc)
					return true, nil
				}

				// Preparation/Transient Indicator Rule: If fresh, skip pruning for ALL other unhandled check conditions (e.g. 0x05/0x25)
				if deviceAge < 30*time.Second {
					logger.Warningf("[%s] IOCTL Probe: Intercepted transient condition (%02x/%02x) during birth sequence (Age: %v). Retaining path to settle.", sgName, senseKey, asc, deviceAge)
					return false, nil 
				}
			}
			
			// If the device is breaching the aging boundary and still failing commands, it's safe to prune
			logger.Errorf("[%s] IOCTL Probe: Device failing check conditions beyond aging threshold (%v). Enforcing ghost pruning.", sgName, deviceAge)
			return true, nil

		case 0x08, 0x28: // SCSI STATUS: BUSY or TASK SET FULL
			logger.Debugf("[%s] IOCTL Probe: Target queue congestion flagged (Status: 0x%02x). Executing 50ms fallback wait...", sgName, header.status)
			time.Sleep(50 * time.Millisecond)
			continue 

		default:
			if deviceAge < 30*time.Second {
				logger.Warningf("[%s] IOCTL Probe: Unexpected SCSI status byte (0x%02x) during birth sequence. Retaining path.", sgName, header.status)
				return false, nil
			}
			logger.Debugf("[%s] IOCTL Probe: Unexpected SCSI protocol status byte received (0x%02x). Flagging as ghost slot.", sgName, header.status)
			return true, nil
		}
	} 

	logger.Debugf("[%s] IOCTL Probe: Exhausted all hardware command attempts under load pressure. Defensively retaining path.", sgName)
	return false, fmt.Errorf("exhausted storage path verification inquiry attempts under load queue pressure")

// =========================================================================
// 3. VPD PAYLOAD EVALUATION
// =========================================================================
PROCESS_PAGE_0x83:
	if inqResp[1] != 0x83 {
		logger.Warningf("[%s] Payload Parsing Error: Device returned invalid page code identifier (0x%02x) instead of 0x83. Flagging as ghost slot.", sgName, inqResp[1])
		return true, nil
	}

	pageLen := (int(inqResp[2]) << 8) | int(inqResp[3])
	pq := (inqResp[0] >> 5) & 0x07 
	devType := inqResp[0] & 0x1f

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
	
	logger.Warningf("[Teardown-Main] Entering master volume cleanup sequence for mount target: %s", target)	

	// =========================================================================
	// --- PHASE 0: PRE-UNMOUNT HARDWARE HARVEST ---
	// =========================================================================
	isMounted, err := r.Mounter.IsMounted(target)
	if err == nil && isMounted {
		if devPath, err := r.Mounter.GetDeviceFromMount(target); err == nil && devPath != "" {
			logger.Warningf("[Teardown-Main] Isolated backing device path node from mount tree: %s", devPath)
			
			// Restored: Shielding the file system metadata access against unexpected kernel blockages
			stat, errStat := executer.ExecuteUninterruptible[os.FileInfo](
				ctx, r.KeyedGater, "stat-teardown-"+filepath.Base(devPath), 10, 50, 1*time.Second, 2*time.Second,
				func(wCtx context.Context) (os.FileInfo, error) {
					return os.Stat(devPath)
				},
			)
			
			if errStat == nil {
				if sysObj, ok := stat.Sys().(*syscall.Stat_t); ok {
					major = unix.Major(uint64(sysObj.Rdev))
					minor = unix.Minor(uint64(sysObj.Rdev))
					hardwareResolved = true
					
					baseName := filepath.Base(devPath)
					if strings.HasPrefix(baseName, "dm-") || strings.HasPrefix(baseName, "mpath") {
						mpathName = r.GetDMNameFromMinor(ctx, minor) 
					} else if strings.HasPrefix(baseName, "nvme") {
						if strings.Contains(baseName, "c") {
							if lastNIdx := strings.LastIndex(baseName, "n"); lastNIdx != -1 {
								if cIdx := strings.Index(baseName, "c"); cIdx != -1 && cIdx < lastNIdx {
									ctrlPart := baseName[:cIdx]   
									nsPart := baseName[lastNIdx:]  
									baseName = ctrlPart + nsPart  
									logger.Infof("[Teardown-Main] Normalized backing native NVMe string token: %s", baseName)
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

	// =========================================================================
	// --- PHASE 1: UNMOUNT & CRITICAL VERIFICATION MATRIX ---
	// =========================================================================
	if err == nil && isMounted {
		if errUnmount := r.Mounter.UnmountWithTimeout(ctx, target, 30*time.Second); errUnmount != nil {
			logger.Errorf("[Teardown-Main] Unmount loop returned failure state for path %s: %v", target, errUnmount)
			return fmt.Errorf("teardown: unmount step is still in progress: %w", errUnmount)
		}
		
		if !r.Mounter.PollMountDeleted(ctx, target, 10*time.Second) {
			logger.Errorf("[Teardown-Main] Mount path %s remained pinned in kernel namespaces after timeout", target)
			return fmt.Errorf("teardown: failed to confirm volume unmount clearance status")
		}
		logger.Infof("[Teardown-Main] Target path %s cleanly unmounted and verified gone.", target)
	}

	// =========================================================================
	// --- PHASE 2: HARDWARE RESOLUTION FALLBACK ---
	// =========================================================================
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
	// --- PHASE 3: UPDATED DEVICE MAPPER & NATIVE NVMe REMOVAL SEQUENCE ---
	// =========================================================================
	var globalOpenCount int32
	rawScsiTarget := strings.ToLower(strings.TrimSpace(expectedWWID))
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	isDeviceMapperTarget := strings.HasPrefix(mpathName, "dm-") || strings.HasPrefix(mpathName, "mpath")

	if mpathName != "" && !isNativeNVMe && isDeviceMapperTarget {
		logger.Infof("[Teardown-Main] [%s] Starting Device Mapper teardown pipeline...", mpathName)
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
			case <-ctx.Done():
				timer.Stop()
				break
			case <-timer.C:
				timer.Stop()
			}
		}

		if globalOpenCount > 0 {
			logger.Warningf("[Teardown-Main] [%s] Device remains busy (openCount=%d). Triggering Deferred Removal.", mpathName, globalOpenCount)
			_ = r.multipathdAction(ctx, "disablequeueing map "+mpathName)
			
			// Restored: Defensively wrap ioctl removal against stuck device queues
			_, _ = executer.ExecuteUninterruptible[struct{}](
				ctx, r.KeyedGater, "rescue-"+mpathName, 10, 50, 5*time.Second, 15*time.Second,
				func(wCtx context.Context) (struct{}, error) {
					_ = r.dmIoctlCall(wCtx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)
					return struct{}{}, nil
				},
			)
		} else {
			if needFlush {
				// Restored: Explicitly wrap the buffer flush operation per-map
				_, _ = executer.ExecuteUninterruptible[struct{}](
					ctx, r.KeyedGater, "flush-"+mpathName, 10, 50, 5*time.Second, 30*time.Second,
					func(wCtx context.Context) (struct{}, error) {
						err := r.flushDeviceBuffers(wCtx, mpathName)
						return struct{}{}, err
					},
				)
				
				var slaves []string
				if hardwareResolved && major != 0 {
					slaves, _ = r.Helper.getSlavesForDevice(ctx, major, minor)
				}
				if len(slaves) == 0 && expectedWWID != "" {
					slaves = r.FindSlavesByWWID(ctx, expectedWWID) 
				}

				logger.Infof("[Teardown-Main] [%s] Step 1/2: Dropping multipath layout via daemon entry...", mpathName)
				if errDelMap := r.multipathdAction(ctx, "del map "+mpathName); errDelMap != nil {
					logger.Warningf("[Teardown-Main] [%s] Daemon map deletion failed: %v.", mpathName, errDelMap)
				}

				if len(slaves) > 0 {
					logger.Infof("[Teardown-Main] [%s] Step 2/2: Evicting physical backing slave devices: %v", mpathName, slaves)
					_ = r.RemovePhysicalDevice(ctx, slaves)
					r.evictNVMeNamespaces(ctx, slaves)
					needRemovePhysical = false 
				} else {
					logger.Infof("[Teardown-Main] [%s] Step 2/2: Slaves empty. Sweeping dual-protocol parameters...", mpathName)
					_ = r.purgeStuckPhysicalPathsDualProtocol(ctx, rawScsiTarget, rawNvmeTarget)
					needRemovePhysical = false
				}
			}
		}
	} else if mpathName != "" && isNativeNVMe {
		logger.Infof("[Teardown-Main] Target node %s maps to a native NVMe architecture. Routing straight to hardware eviction loops.", mpathName)
		if needFlush {
			slaves := r.FindSlavesByWWID(ctx, expectedWWID)
			if len(slaves) == 0 {
				slaves = []string{mpathName}
			}

			logger.Infof("[Teardown-Main] Evicting native NVMe path lanes: %v", slaves)
			_ = r.RemovePhysicalDevice(ctx, slaves)
			r.evictNVMeNamespaces(ctx, slaves)
			needRemovePhysical = false
		}
	}

	// =========================================================================
	// --- PHASE 4: PHYSICAL LAYER FALLBACK ---
	// =========================================================================
	if needRemovePhysical && expectedWWID != "" {
		logger.Infof("[Teardown-Main] Executing global fallback sweep for WWID: %s", expectedWWID)
		_ = r.purgeStuckPhysicalPathsDualProtocol(ctx, rawScsiTarget, rawNvmeTarget)
	}

	return nil
}


// cleanNVMeNamespacesFromSlaves executes systematic parallel sysfs deletion token injection on NVMe paths.
func (r *OsDeviceConnectivityHelperScsiGeneric) cleanNVMeNamespacesFromSlaves(ctx context.Context, devices []string) {
	if err := ctx.Err(); err != nil {
		return
	}

	// Filter and isolate valid targets upfront to feed a completely pure execution batch
	var validDevices []string
	for _, dev := range devices {
		base := filepath.Base(dev)
		if !strings.HasPrefix(base, "nvme") {
			continue
		}
		if strings.Contains(base, "n") && !strings.Contains(base, "c") {
			validDevices = append(validDevices, dev)
		}
	}

	if len(validDevices) == 0 {
		return
	}

	// Enforce a unified batch container key to keep global allocations bounded 
	gaterKey := "batch-nvme-slaves-namespace-cleanup"

	_, _ = executer.ExecuteUninterruptibleBatch[string, struct{}](
		ctx,
		r.KeyedGater,
		gaterKey,
		15,  // maxRunning: protects the NVMe subsystems from overwhelming simultaneous kernel unbind loads
		100, // maxSpare
		3*time.Second,
		15*time.Second, // Bounded timeout for full batch writing tasks
		validDevices,
		func(wCtx context.Context, index int, dev string, cancelBatch func()) (struct{}, error) {
			base := filepath.Base(dev)
			parts := strings.Split(base, "n")
			if len(parts) == 2 {
				ctrlName := parts[0] // e.g., nvme0
				nsName := base       // e.g., nvme0n1
				
				sysfsDeletePath := fmt.Sprintf("/sys/class/nvme/%s/%s/delete", ctrlName, nsName)
				logger.Infof("[Teardown-NVMe] Injecting sysfs eviction write token to: %s", sysfsDeletePath)
				
				// Write "1" into the sysfs delete attribute to force-evict the namespace from the kernel
				if err := os.WriteFile(sysfsDeletePath, []byte("1\n"), 0200); err != nil {
					if !os.IsNotExist(err) {
						logger.Warningf("[Teardown-NVMe] Failed to force write to sysfs path %s: %v", sysfsDeletePath, err)
					}
				}
			}
			return struct{}{}, nil
		},
	)
}


// evictNVMeNamespaces targets specific sysfs namespace delete attributes in parallel.
func (r *OsDeviceConnectivityHelperScsiGeneric) evictNVMeNamespaces(ctx context.Context, devices []string) {
	if err := ctx.Err(); err != nil {
		return
	}

	// Filter and isolate valid targets upfront to ensure a pure execution batch
	var validDevices []string
	for _, dev := range devices {
		base := filepath.Base(dev)
		if !strings.HasPrefix(base, "nvme") {
			continue
		}
		if strings.Contains(base, "n") && !strings.Contains(base, "c") {
			validDevices = append(validDevices, dev)
		}
	}

	if len(validDevices) == 0 {
		return
	}

	// Enforce a structured batch key layout to prevent overlapping orchestration interference
	gaterKey := "batch-nvme-namespaces-eviction"

	_, _ = executer.ExecuteUninterruptibleBatch[string, struct{}](
		ctx,
		r.KeyedGater,
		gaterKey,
		15,  // maxRunning: protects the NVMe sub-tree from heavy simultaneous udev unbind strain
		100, // maxSpare
		3*time.Second,
		15*time.Second, // Bounded hard timeout window for full batch file modifications
		validDevices,
		func(wCtx context.Context, index int, dev string, cancelBatch func()) (struct{}, error) {
			base := filepath.Base(dev)
			parts := strings.Split(base, "n")
			if len(parts) == 2 {
				ctrlName := parts[0] // e.g., "nvme0"
				nsName := base       // e.g., "nvme0n1"
				
				// Standard NVMe sysfs deletion directory path node structure
				sysfsDeletePath := fmt.Sprintf("/sys/class/nvme/%s/%s/delete", ctrlName, nsName)
				logger.Infof("[Teardown-NVMe] Injecting sysfs eviction write token to: %s", sysfsDeletePath)
				
				if err := os.WriteFile(sysfsDeletePath, []byte("1\n"), 0200); err != nil {
					if !os.IsNotExist(err) {
						logger.Warningf("[Teardown-NVMe] Failed to force write to sysfs path %s: %v", sysfsDeletePath, err)
					}
				}
			}
			return struct{}{}, nil
		},
	)
}

// FindSlavesByWWID safely scans the host block layer in parallel to aggregate all physical path lanes matching the volume identifier.
func (r *OsDeviceConnectivityHelperScsiGeneric) FindSlavesByWWID(ctx context.Context, expectedWWID string) []string {
	var slaves []string
	
	rawScsiTarget := normalizeWWID(expectedWWID)
	if rawScsiTarget == "" {
		return slaves
	}
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	devEntries, err := os.ReadDir("/dev")
	if err != nil {
		logger.Warningf("FindSlavesByWWID: failed to read /dev cleanly: %v", err)
		return slaves
	}

	const chunkSize = 100
	var currentBatch []string

	// Reusable batch evaluator execution block to process device chunks concurrently (Rule 1)
	processBatch := func(batch []string) []string {
		if len(batch) == 0 {
			return nil
		}

		var matchedSlaves []string
		results, errBatch := executer.ExecuteUninterruptibleBatch[string, string](
			ctx,
			r.KeyedGater,
			"batch-find-slaves-chunk",
			16,  // maxRunning: allows high parallel tracking throughput across paths
			128, // maxSpare
			5*time.Second,
			30*time.Second, // Bounded hard timeout window for full chunk evaluations
			batch,
			func(wCtx context.Context, index int, name string, cancelBatch func()) (string, error) {
				isNVMe := nvmeNamespaceRegex.MatchString(name)
				isSCSI := strings.HasPrefix(name, "sd")

				var discoveredID string
				if isNVMe {
					baseBlockName := name 
					targetSysDir := filepath.Join("/sys/block", name)
					
					if strings.Contains(name, "c") {
						if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
							if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
								baseBlockName = name[:cIdx] + name[lastNIdx:]
								targetSysDir = filepath.Join("/sys/block", baseBlockName) 
							}
						}
					}

					// Try binary ioctl on the active dev node
					deviceNode := filepath.Join("/dev", name)
					if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
						var nvmeInfo nvmeIdTarget
						_, _, errno := syscall.Syscall(
							syscall.SYS_IOCTL,
							df.Fd(),
							uintptr(NVME_IOCTL_ID_TARGET),
							uintptr(unsafe.Pointer(&nvmeInfo)),
						)
						df.Close()

						if errno == 0 {
							discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
						}
					}

					// Fallback architecture for both modern layouts and RHEL 7 (Rule 3)
					if discoveredID == "" || discoveredID == "00000000000000000000000000000000" {
						if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && bytesStr != "" {
							discoveredID = normalizeWWID(bytesStr)
						}
						if discoveredID == "" {
							if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && bytesStr != "" {
								discoveredID = normalizeWWID(bytesStr)
							}
						}
						// RHEL 7 Core Fallback Layer
						if discoveredID == "" {
							if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device", "wwid")); err == nil && bytesStr != "" {
								discoveredID = normalizeWWID(bytesStr)
							}
						}
						// Legacy Subsystem Symlink Sweep
						if discoveredID == "" {
							subsysSymlink := filepath.Join("/sys/block", name, "device", "subsystem")
							realSubsysPath, errLink := os.Readlink(subsysSymlink)
							if errLink == nil {
								if strings.Contains(realSubsysPath, "virtual/nvme-subsys") || strings.Contains(realSubsysPath, "bus/nvme") {
									subsysWwidPath := filepath.Join(realSubsysPath, "wwid")
									if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, subsysWwidPath); err == nil && bytesStr != "" {
										discoveredID = normalizeWWID(bytesStr)
									}
								}
							}
						}
					}
				} else if isSCSI {
					if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, name, filepath.Join("/sys/block", name, "device", "wwid")); err == nil && bytesStr != "" {
						discoveredID = normalizeWWID(bytesStr)
					}
				}

				if discoveredID == "" {
					return "", nil
				}
				
				// Protocol-isolated matching criteria (Rule 5)
				isMatch := false
				if isNVMe {
					isMatch = (discoveredID == rawNvmeTarget)
				} else if isSCSI {
					isMatch = (discoveredID == rawScsiTarget)
				}

				if isMatch {
					return name, nil
				}
				return "", nil
			},
		)

		if errBatch == nil {
			for _, res := range results {
				if res.Err == nil && res.Data != "" {
					matchedSlaves = append(matchedSlaves, res.Data)
				}
			}
		}
		return matchedSlaves
	}

	// =========================================================================
	// OUTER GATHERING LOOPS: Read /dev and compile chunk segments
	// =========================================================================
	for _, entry := range devEntries {
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

		currentBatch = append(currentBatch, name)

		// Execute the parallel batch whenever the chunk capacity threshold is reached
		if len(currentBatch) == chunkSize {
			foundInChunk := processBatch(currentBatch)
			slaves = append(slaves, foundInChunk...)
			currentBatch = currentBatch[:0] // Keep the underlying slice allocation completely stable
		}
	}

	// =========================================================================
	// TAIL FLUSH PASS: Process any remaining records left under chunk limits
	// =========================================================================
	if len(currentBatch) > 0 {
		foundInTail := processBatch(currentBatch)
		slaves = append(slaves, foundInTail...)
	}
	
	logger.Infof("FindSlavesByWWID: Concluded path validation scan. Found %d active matching slave tracks.", len(slaves))
	return slaves
}

// GetDMNameFromMinor safe-resolves a Device Mapper's functional name from its minor code.
func (r *OsDeviceConnectivityHelperScsiGeneric) GetDMNameFromMinor(ctx context.Context, minor uint32) string {
	logger.Warning("GetDMNameFromMinor Dynamic Matrix Parsing")

	if err := ctx.Err(); err != nil {
		return ""
	}

	// Dynamic major resolution via /sys/block (Rule 3/5 Compatibility)
	dmMajor := uint32(252) // Default fallback
	if entries, err := os.ReadDir("/sys/block"); err == nil {
		for _, ext := range entries {
			if strings.HasPrefix(ext.Name(), "dm-") {
				if stat, errStat := os.Stat(filepath.Join("/sys/block", ext.Name())); errStat == nil {
					if sysObj, ok := stat.Sys().(*syscall.Stat_t); ok {
						dmMajor = unix.Major(uint64(sysObj.Rdev))
						break
					}
				}
			}
		}
	}

	directBlockPath := fmt.Sprintf("/sys/dev/block/%d:%d", dmMajor, minor)

	var resolvedDmName string
	if realPath, errLink := os.Readlink(directBlockPath); errLink == nil {
		dmDirName := filepath.Base(realPath)
		if strings.HasPrefix(dmDirName, "dm-") {
			resolvedDmName = dmDirName
		}
	}

	if resolvedDmName != "" {
		if name := r.readDMNameSafe(ctx, resolvedDmName); name != "" {
			return name
		}
	}

	// =========================================================================
	// OPTIMIZED FALLBACK PATH: SCAN /dev/mapper IN ALL LINUX DISTRIBUTIONS
	// =========================================================================
	// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1): Replaced the internal 
	// global infrastructure gater with direct, context-respecting directory chunk parsing.
	sessionClassPath := "/dev/mapper"
	sFile, errOpen := os.Open(sessionClassPath)
	if errOpen != nil {
		return ""
	}
	defer sFile.Close()

	for {
		if err := ctx.Err(); err != nil {
			return ""
		}

		// Use 100-entry chunk pagination for high-density storage scalability (Rule 5 Memory Bound)
		mapperEntries, errDirs := sFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			break
		}

		for _, entry := range mapperEntries {
			if err := ctx.Err(); err != nil {
				return ""
			}

			name := entry.Name()
			if name == "control" {
				continue
			}

			fullPath := filepath.Join("/dev/mapper", name)

			fi, errStat := os.Lstat(fullPath)
			if errStat != nil {
				continue
			}

			var dmKernelName string
			// Secure check for both symlink and raw block device nodes (RHEL 7 vs Modern Parity)
			if fi.Mode()&os.ModeSymlink != 0 {
				realPath, errLink := os.Readlink(fullPath)
				if errLink != nil {
					continue
				}
				dmKernelName = filepath.Base(realPath)
			} else {
				statT, ok := fi.Sys().(*syscall.Stat_t)
				if !ok {
					continue
				}
				minorIndex := unix.Minor(uint64(statT.Rdev))
				dmKernelName = fmt.Sprintf("dm-%d", minorIndex)
			}

			if dmKernelName == fmt.Sprintf("dm-%d", minor) {
				if functionalName := r.readDMNameSafe(ctx, dmKernelName); functionalName != "" {
					return functionalName
				}
			}
		}

		if len(mapperEntries) < 100 || errDirs == io.EOF {
			break
		}
	}
	return ""
}


// readDMNameSafe evaluates standard and legacy device-mapper naming layouts with absolute D-state protection.
func (r *OsDeviceConnectivityHelperScsiGeneric) readDMNameSafe(ctx context.Context, dmDirName string) string {
	if err := ctx.Err(); err != nil {
		return ""
	}

	cleanDmName := filepath.Base(dmDirName)

	// =========================================================================
	// FIXED: SHIELDED HIGH-AVAILABILITY SYSFS PARSING PASS (Rule 1 Alignment)
	// =========================================================================
	// We run the file reads through the protected KeyedGater executor frame. 
	// This ensures that if a legacy or modern kernel locks up on a transitioning 
	// DM block entry, the Go runtime thread will not hang permanently.
	result, _ := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		"read-dm-name-"+cleanDmName, // Safe device-isolated key avoids global choke points
		20,                          // Concurrency threshold limit across the storage stack
		100,                         // maxSpare
		500*time.Millisecond,        // Fast handoff timeout
		2*time.Second,               // Strict hard timeout ceiling
		func(wCtx context.Context) (string, error) {
			// Route A: Standard modern system layout mapping
			modernPath := filepath.Join("/sys/block", cleanDmName, "dm", "name")
			if bytes, err := os.ReadFile(modernPath); err == nil {
				return string(bytes), nil
			}
			
			// Route B: Legacy RHEL 7 / early kernel fallback alignment scheme (Rule 3)
			legacyPath := filepath.Join("/sys/block", cleanDmName, "name")
			if bytes, err := os.ReadFile(legacyPath); err == nil {
				return string(bytes), nil
			}

			return "", fmt.Errorf("dm name not accessible for: %s", cleanDmName)
		},
	)

	if result == "" {
		return ""
	}

	// =========================================================================
	// FIXED: SANITIZE POTENTIAL NULL-BYTE POLLUTION FROM SYSLOG/UDEV STACKS (Rule 5)
	// =========================================================================
	// We explicitly strip out all non-printable ASCII elements, null bytes, 
	// and trailing spaces to prevent multipathd from rejecting our command payloads.
	sanitized := strings.Map(func(r rune) rune {
		if r == 0 || r == '\x00' {
			return -1 // Drop null bytes entirely
		}
		return r
	}, result)

	return strings.TrimSpace(sanitized)
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

// IdentityAwarePreScan performs a strict safety scan prior to volume staging to confirm path availability and eliminate leaks.
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
		// FLATTENED FOR SIMPLICITY & MAXIMUM PERFORMANCE (Rule 1/5): 
		// Replaced the internal ExecuteUninterruptible wrapper and the heavy EvalSymlinks engine
		// with a direct, context-respecting os.Readlink call to resolve the pointer instantly out of memory.
		resolvedPath, errLink := os.Readlink(filepath.Join("/dev/mapper", mpathAlias))
		if errLink == nil {
			if !filepath.IsAbs(resolvedPath) {
				resolvedPath = filepath.Join("/dev/mapper", resolvedPath)
			}
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
			hwWWID, _ = r.Helper.GetWwnByScsiInq(ctx, r.KeyedGater, mpathAlias)
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
			if strings.HasPrefix(nodeName, "nvme") && strings.Contains(nodeName, "c") {
				if lastNIdx := strings.LastIndex(nodeName, "n"); lastNIdx != -1 && lastNIdx > 0 {
					if cIdx := strings.Index(nodeName, "c"); cIdx != -1 && cIdx < lastNIdx {
						devNode = filepath.Join("/dev", nodeName[:cIdx]+nodeName[lastNIdx:])
					}
				}
			}
			return devNode, true, true, false, nil
		}

		_ = r.Mounter.UnmountWithTimeout(ctx, targetPath, 30*time.Second)
		r.busyTimestamps.Delete(rawScsiTarget)
		return "", false, false, false, status.Error(codes.Internal, "pre-scan: identification collision detected")
	}

	helper := GetDmsPathHelperGeneric{}
	var hasDevice, isPending bool
	var devName string
	
	if mpathName != "" {
		hasDevice, isPending, _ = helper.EvaluateSpecificSysfsTopology(ctx, r.KeyedGater, mpathName, rawScsiTarget, true)
		devName = mpathName
	} else {
		hasDevice, isPending, devName = helper.EvaluateSysfsTopology(ctx, r.KeyedGater, rawScsiTarget, true)
	}

	if hasDevice {
		cleanDevName := filepath.Base(devName)
		if helper.IsDeviceMapper(cleanDevName) {
			if helper.GetSlaveCount(ctx, r.KeyedGater, cleanDevName) == 0 {
				_ = r.cleanupOrphanedTopology(ctx, cleanDevName, rawScsiTarget)
				r.busyTimestamps.Delete(rawScsiTarget)
				return "", false, false, true, nil
			}
		}

		if strings.HasPrefix(cleanDevName, "nvme") && strings.Contains(cleanDevName, "c") {
			if lastNIdx := strings.LastIndex(cleanDevName, "n"); lastNIdx != -1 && lastNIdx > 0 {
				if cIdx := strings.Index(cleanDevName, "c"); cIdx != -1 && cIdx < lastNIdx {
					cleanDevName = cleanDevName[:cIdx] + cleanDevName[lastNIdx:]
				}
			}
		}

		if isPending {
			ctrlTrackingKey := ExtractNvmeControllerBase(cleanDevName) // Safely resolves "nvme2n1" to "nvme2"
			
			now := time.Now()
			val, loaded := r.busyTimestamps.LoadOrStore(ctrlTrackingKey, now)
			firstDetected := val.(time.Time)
			if loaded && now.Sub(firstDetected) > 5*time.Minute {
				logger.Errorf("[Pre-Scan] Path %s stuck initializing for > 5 minutes. Enforcing orphaned topology cleanup.", cleanDevName)
				_ = r.cleanupOrphanedTopology(ctx, cleanDevName, rawScsiTarget)
				r.busyTimestamps.Delete(ctrlTrackingKey)
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

// cleanupOrphanedTopology clears residual hardware definitions from the node host.
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

	// =========================================================================
	// PHASE 1: DYNAMIC PARENT TOPOLOGY MAP IDENTIFICATION
	// =========================================================================
	if mpathName == "" {
		slaves := r.FindSlavesByWWID(ctx, rawScsiTarget)
		if len(slaves) > 0 {
			targetNode := slaves[0]
			baseBlockName := targetNode 
			
			if strings.HasPrefix(targetNode, "nvme") {
				mpathName = targetNode
				isNativeNVMe = true
			} else if strings.HasPrefix(targetNode, "sd") || r.IsScsiBlockDevice(ctx, targetNode) {
				if strings.Contains(targetNode, "c") {
					if lastNIdx := strings.LastIndex(targetNode, "n"); lastNIdx != -1 && lastNIdx > 0 {
						if cIdx := strings.Index(targetNode, "c"); cIdx != -1 && cIdx < lastNIdx {
							baseBlockName = targetNode[:cIdx] + targetNode[lastNIdx:] 
						}
					}
				}

				var major, minor uint32
				
				// FIXED: Pass 'baseBlockName' instead of the raw 'targetNode' to ensure 
				// that virtual controller channels are correctly stripped before looking up sysfs major/minor properties.
				major, minor, _ = r.Helper.GetMajorMinorFromSysfs(ctx, baseBlockName)
				
				if major != 0 {
					mpathName = r.GetDMNameFromMinor(ctx, minor)
					if mpathName != "" {
						isDM = true
						logger.Infof("[Cleanup-Topology] Resolved parent Device Mapper link dynamically via slave %s -> %s", baseBlockName, mpathName)
					}
				}
			}
		}
	}

	// =========================================================================
	// PHASE 2: TARGETED DEVICE TEARDOWN (ONLY RUNS IF MPATH NAME IS VALID)
	// =========================================================================
	if isDM && mpathName != "" {
		_ = r.multipathdAction(ctx, "disablequeueing map "+mpathName)
		openCount, err := r.Helper.GetOpenCount(ctx, mpathName)
		if err == nil {
			if openCount <= 0 {
				logger.Infof("[Cleanup-Topology] [%s] Open count is zero. Safely deleting map configuration layer.", mpathName)
				_ = r.multipathdAction(ctx, "del map "+mpathName)
			} else {
				logger.Warningf("[Cleanup-Topology] [%s] Open count is %d. Triggering deferred ioctl removal.", mpathName, openCount)
				err := r.dmIoctlCall(ctx, mpathName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)
				if err != nil {
					logger.Warningf("[Cleanup-Topology] Native DM ioctl mapping block rejected: %v. Attempting user-space CLI fallback...", err)
					_ = r.executeDmsetupDeferredRemove(ctx, mpathName)
				}
			}
		}
	} else if isNativeNVMe && mpathName != "" {
		_ = r.disableNativeNvmeQueueing(ctx, rawScsiTarget)
	} else {
		logger.Infof("[Cleanup-Topology] Parent map name could not be resolved from sysfs. Proceeding straight to physical layer hardware sweeps.")
	}

	// =========================================================================
	// PHASE 3: DUAL-PROTOCOL PHYSICAL LAYER SWEEP (THE CLEANUP SAFEGUARD)
	// =========================================================================
	logger.Infof("[Cleanup-Topology] Launching master dual-protocol physical layer sweep [SCSI WWID: %s | NVMe NGUID: %s]", rawScsiTarget, rawNvmeTarget)
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
func (r *OsDeviceConnectivityHelperScsiGeneric) executeDmsetupDeferredRemove(ctx context.Context, mpathName string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	logger.Infof("[Cleanup-Topology] Initializing native kernel deferred removal pass for map: %s", mpathName)

	// FIXED: Maintained the explicit struct{} infrastructure protection wrapper around 
	// the pass-through kernel ioctl call to safely isolate against device-mapper queue deadlocks.
	_, err := executer.ExecuteUninterruptible[struct{}](
		ctx,
		r.KeyedGater,
		fmt.Sprintf("native-dm-remove-%s", mpathName),
		10, 
		50, 
		1*time.Second, 
		5*time.Second,
		func(wCtx context.Context) (struct{}, error) {
			// Open the primary Device Mapper controller communication link natively
			controlFd, err := syscall.Open("/dev/mapper/control", syscall.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				return struct{}{}, fmt.Errorf("failed to open device-mapper controller: %w", err)
			}
			defer syscall.Close(controlFd)

			var packet dmIoctlPacket
			packet.version[0] = 4 // DM_VERSION_MAJOR
			packet.version[1] = 0 // DM_VERSION_MINOR
			packet.version[2] = 0 // DM_VERSION_PATCHLEVEL
			packet.data_size = uint32(unsafe.Sizeof(packet))
			packet.data_start = uint32(unsafe.Sizeof(packet))
			packet.flags = DM_DEFERRED_REMOVE_FLAG

			copy(packet.name[:], mpathName)

			const DM_DEV_REMOVE_IOCTL_CODE = 0xc138fd04

			// Fire the direct low-level kernel pass-through execution signature
			_, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				uintptr(controlFd),
				uintptr(DM_DEV_REMOVE_IOCTL_CODE),
				uintptr(unsafe.Pointer(&packet)),
			)

			// ENXIO implies device was already deleted (idempotent victory)
			if errno != 0 && errno != syscall.ENXIO { 
				return struct{}{}, fmt.Errorf("native device-mapper removal ioctl call failed (errno %v): %w", errno, errno)
			}

			logger.Infof("[Cleanup-Topology] Native kernel deferred removal successfully applied for map %s", mpathName)
			return struct{}{}, nil
		},
	)

	return err
}



// IsNativeNvmeNamespace safely verifies if a device is managed by the native NVMe subsystem core layers.
func (r *OsDeviceConnectivityHelperScsiGeneric) IsNativeNvmeNamespace(name string) bool {
	return nvmeNamespaceRegex.MatchString(name)
}

// disableNativeNvmeQueueing identifies the target NVMe nodes and modifies timeout variables concurrently.
func (r *OsDeviceConnectivityHelperScsiGeneric) disableNativeNvmeQueueing(ctx context.Context, expectedWWID string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	normExpected := normalizeWWID(expectedWWID)

	// 1. FAST & FAILSAFE: Read /dev entries to find active nodes without /sys/block overhead
	devEntries, errDir := os.ReadDir("/dev")
	if errDir != nil {
		return fmt.Errorf("failed to safely query system device nodes list: %w", errDir)
	}

	const chunkSize = 100
	currentBatch := make([]string, 0, chunkSize)

	// Reusable batch executor block for chunk processing (Rule 1)
	processBatch := func(batch []string) error {
		if len(batch) == 0 {
			return nil
		}

		logger.Infof("[Disable-Queue] Processing concurrent chunk of %d targets", len(batch))
		_, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
			ctx,
			r.KeyedGater,
			"batch-disable-nvme-queueing-chunk",
			10,  // maxRunning: balances NVMe controller update and file I/O loads
			100, // maxSpare
			5*time.Second,
			35*time.Second, // Bounded timeout for full chunk analysis and parameter writes
			batch,
			func(wCtx context.Context, index int, devName string, cancelBatch func()) (struct{}, error) {
				baseBlockName := devName 
				baseBlockDir := filepath.Join("/sys/block", devName)
				targetSysDir := baseBlockDir

				// Strips virtual channel routing text (Rule 5)
				if strings.Contains(devName, "c") {
					if lastNIdx := strings.LastIndex(devName, "n"); lastNIdx != -1 && lastNIdx > 0 {
						if cIdx := strings.Index(devName, "c"); cIdx != -1 && cIdx < lastNIdx {
							ctrlPart := devName[:cIdx]  
							nsPart := devName[lastNIdx:] 
							
							baseBlockName = ctrlPart + nsPart 
							targetSysDir = filepath.Join("/sys/block", baseBlockName)
							logger.Debugf("[Disable-Queue] Normalized virtual block node routing path: %s -> %s", devName, targetSysDir)
						}
					}
				}

				var discoveredID string
				// Ultra-fast binary ioctl on the active /dev node first to get the unique ID
				deviceNode := filepath.Join("/dev", devName)
				if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
					var nvmeInfo nvmeIdTarget
					_, _, errno := syscall.Syscall(
						syscall.SYS_IOCTL,
						df.Fd(),
						uintptr(NVME_IOCTL_ID_TARGET),
						uintptr(unsafe.Pointer(&nvmeInfo)),
					)
					df.Close()

					if errno == 0 {
						discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
					}
				}

				// Legacy Fallback (Rule 3 Layout)
				if discoveredID == "" || discoveredID == "00000000000000000000000000000000" {
					wwidPath := filepath.Join(targetSysDir, "wwid")
					if _, err := os.Stat(wwidPath); os.IsNotExist(err) {
						wwidPath = filepath.Join(baseBlockDir, "wwid")
					}
					wwidBytesStr, errRead := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, wwidPath)
					if errRead == nil {
						discoveredID = normalizeWWID(wwidBytesStr)
					}
				}

				if discoveredID != normExpected {
					return struct{}{}, nil
				}

				var controllersToUpdate []string
				subsysSymlink := filepath.Join("/sys/block", devName, "device", "subsystem")
				
				// Fast memory-backed read link
				realSubsysPath, errLink := os.Readlink(subsysSymlink)

				if errLink == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys") {
					if entries, errSub := os.ReadDir(realSubsysPath); errSub == nil {
						for _, e := range entries {
							name := e.Name()
							if strings.HasPrefix(name, "nvme") && !nvmeNamespaceRegex.MatchString(name) {
								controllersToUpdate = append(controllersToUpdate, name)
							}
						}
					}
				} else {
					ctrlName := ExtractNvmeControllerBase(devName)
					controllersToUpdate = append(controllersToUpdate, ctrlName)
				}

				// RULE 1 REMEDY: Execute the destructive file writes directly inside the 
				// context-bounded infrastructure batch worker instead of using raw background go routines.
				for _, ctrl := range controllersToUpdate {
					fastIoFailPath := filepath.Join("/sys/class/nvme", ctrl, "device", "fast_io_fail_tmo")
					if _, err := os.Stat(fastIoFailPath); os.IsNotExist(err) {
						fastIoFailPath = filepath.Join("/sys/class/nvme", ctrl, "fast_io_fail_tmo")
					}

					if _, err := os.Stat(fastIoFailPath); err == nil {
						logger.Infof("[Disable-Queue] Modifying timeout parameter for controller %s via path: %s", ctrl, fastIoFailPath)
						if errWrite := os.WriteFile(fastIoFailPath, []byte("1\n"), 0200); errWrite != nil {
							logger.Warningf("[Disable-Queue] Failed to write timeout value to %s: %v", fastIoFailPath, errWrite)
						}
					}
				}

				return struct{}{}, nil
			},
		)

		if errBatch != nil {
			return fmt.Errorf("parallel NVMe queue timeout chunk processing failed: %w", errBatch)
		}
		return nil
	}

	// =========================================================================
	// OUTER GATHERING LOOP: Scan /dev and assemble bounded segments
	// =========================================================================
	for _, f := range devEntries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		devName := f.Name()
		if !nvmeNamespaceRegex.MatchString(devName) {
			continue
		}

		currentBatch = append(currentBatch, devName)

		if len(currentBatch) == chunkSize {
			if errChunk := processBatch(currentBatch); errChunk != nil {
				return errChunk
			}
			currentBatch = currentBatch[:0] // Keep the underlying memory allocation stable
		}
	}

	// =========================================================================
	// TAIL FLUSH PASS: Process any remaining items under chunk limits
	// =========================================================================
	if len(currentBatch) > 0 {
		if errChunk := processBatch(currentBatch); errChunk != nil {
			return errChunk
		}
	}

	return nil
}

// purgeStuckPhysicalPathsDualProtocol identifies and clears residual physical tracks in parallel.
func (r *OsDeviceConnectivityHelperScsiGeneric) purgeStuckPhysicalPathsDualProtocol(ctx context.Context, rawScsiTarget, rawNvmeTarget string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	scsiMatchTarget := normalizeWWID(rawScsiTarget)
	nvmeMatchTarget := normalizeWWID(rawNvmeTarget)

	// 1. FAST & FAILSAFE: Read /dev entries to find active nodes without /sys/block overhead
	devEntries, errDir := os.ReadDir("/dev")
	if errDir != nil {
		return fmt.Errorf("failed to scan system device path layer under safety frame: %w", errDir)
	}

	const chunkSize = 100
	currentBatch := make([]string, 0, chunkSize)
	var aggregatedErrors []string

	// Reusable batch executor block for chunk processing (Rule 1)
	processBatch := func(batch []string) error {
		if len(batch) == 0 {
			return nil
		}

		logger.Infof("[Purge-Paths] Processing concurrent chunk of %d targets", len(batch))
		results, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
			ctx,
			r.KeyedGater,
			"batch-purge-dual-protocol-paths-chunk",
			15,  // maxRunning: protects the storage bus from overwhelming parallel udev unbind strain
			100, // maxSpare
			5*time.Second,
			30*time.Second, // Bounded hard timeout window for full chunk evaluations and writes
			batch,
			func(wCtx context.Context, index int, devName string, cancelBatch func()) (struct{}, error) {
				isSCSI := strings.HasPrefix(devName, "sd")
				isNVMe := r.IsNativeNvmeNamespace(devName)

				baseBlockName := devName 
				targetSysDir := filepath.Join("/sys/block", devName)
				
				// Strips virtual channel routing text (Rule 5)
				if isNVMe && strings.Contains(devName, "c") {
					if lastNIdx := strings.LastIndex(devName, "n"); lastNIdx != -1 && lastNIdx > 0 {
						if cIdx := strings.Index(devName, "c"); cIdx != -1 && cIdx < lastNIdx {
							ctrlPart := devName[:cIdx]  
							nsPart := devName[lastNIdx:] 
							
							baseBlockName = ctrlPart + nsPart 
							targetSysDir = filepath.Join("/sys/block", baseBlockName) 
							logger.Debugf("[Purge-Paths] Normalized virtual block node routing path: %s -> %s", devName, targetSysDir)
						}
					}
				}

				var discoveredID string
				if isNVMe {
					// Fast binary ioctl on the active /dev node first to get the unique ID
					deviceNode := filepath.Join("/dev", devName)
					if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
						var nvmeInfo nvmeIdTarget
						_, _, errno := syscall.Syscall(
							syscall.SYS_IOCTL,
							df.Fd(),
							uintptr(NVME_IOCTL_ID_TARGET),
							uintptr(unsafe.Pointer(&nvmeInfo)),
						)
						df.Close()

						if errno == 0 {
							discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
						}
					}
				}

				// Legacy Fallback / SCSI Lookup (Rule 3)
				if discoveredID == "" {
					var wwidPath string
					if isSCSI {
						wwidPath = filepath.Join("/sys/block", devName, "device", "wwid")
					} else {
						wwidPath = filepath.Join(targetSysDir, "wwid")
						if _, errStat := os.Stat(wwidPath); os.IsNotExist(errStat) {
							wwidPath = filepath.Join("/sys/block", devName, "wwid")
						}
					}

					wwidBytesStr, errRead := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, wwidPath)
					if errRead != nil || wwidBytesStr == "" {
						return struct{}{}, nil 
					}
					discoveredID = normalizeWWID(wwidBytesStr)
				}

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
					return struct{}{}, nil
				}

				var deletePath string
				var useUnbindStrategy bool
				var pciAddress string
				unbindPath := "/sys/bus/pci/drivers/nvme/unbind"

				if isSCSI {
					deletePath = filepath.Join("/sys/block", devName, "device", "delete")
				} else if isNVMe {
					deletePath = filepath.Join("/sys/block", devName, "device", "delete")
					if _, errStat := os.Stat(deletePath); os.IsNotExist(errStat) {
						deletePath = filepath.Join(targetSysDir, "device", "delete")
					}

					if _, errStat := os.Stat(deletePath); os.IsNotExist(errStat) {
						ctrlName := ExtractNvmeControllerBase(devName)
						pciUeventPath := fmt.Sprintf("/sys/class/nvme/%s/device/uevent", ctrlName)

						if _, errStatUevent := os.Stat(pciUeventPath); errStatUevent == nil {
							ueventBytesStr, errUevent := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, pciUeventPath)
							if errUevent == nil && ueventBytesStr != "" {
								for _, line := range strings.Split(ueventBytesStr, "\n") {
									if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
										pciAddress = strings.TrimPrefix(line, "PCI_SLOT_NAME=")
										deletePath = unbindPath
										useUnbindStrategy = true
										break
									}
								}
							}
						}

						if !useUnbindStrategy {
							// Fast memory-backed read link
							pciAddrPath, errLink := os.Readlink(filepath.Join("/sys/class/nvme", ctrlName, "device"))
							if errLink == nil {
								pciAddress = filepath.Base(pciAddrPath)
								if _, err := os.Stat(unbindPath); err == nil {
									deletePath = unbindPath
									useUnbindStrategy = true
								}
							}
						}
					}
				}

				if deletePath == "" {
					return struct{}{}, nil
				}

				var payloadBytes []byte
				if useUnbindStrategy {
					payloadBytes = []byte(pciAddress)
				} else {
					payloadBytes = []byte("1\n")
				}

				// RULE 1 REMEDY: Replaced the ad-hoc background goroutine with direct, synchronous file write actions
				// contained completely inside the framework batch lane context.
				logger.Infof("[Purge-Paths] Executing eviction write payload on target: %s", deletePath)
				if errWrite := os.WriteFile(deletePath, payloadBytes, 0200); errWrite != nil {
					return struct{}{}, fmt.Errorf("failed to clear disk path %s: %w", devName, errWrite)
				}

				logger.Infof("Successfully cleared path endpoint: %s", deletePath)
				return struct{}{}, nil
			},
		)

		if errBatch != nil {
			return fmt.Errorf("parallel physical tracks eviction batch failed structurally: %w", errBatch)
		}

		// Aggregate errors cleanly from execution result channel without shared locks (Rule 5 compliance)
		for _, res := range results {
			if res.Err != nil {
				aggregatedErrors = append(aggregatedErrors, res.Err.Error())
			}
		}
		return nil
	}

	// =========================================================================
	// OUTER GATHERING LOOP: Scan /dev and assemble bounded segments
	// =========================================================================
	for _, f := range devEntries {
		if ctx.Err() != nil {
			break
		}

		devName := f.Name()
		isSCSI := strings.HasPrefix(devName, "sd")
		isNVMe := r.IsNativeNvmeNamespace(devName)

		if !isSCSI && !isNVMe {
			continue
		}

		currentBatch = append(currentBatch, devName)

		if len(currentBatch) == chunkSize {
			if errChunk := processBatch(currentBatch); errChunk != nil {
				return errChunk
			}
			currentBatch = currentBatch[:0] // Reset chunk length safely to keep memory footprint flat
		}
	}

	// =========================================================================
	// TAIL FLUSH PASS: Finalize remaining entries left over under chunk limits
	// =========================================================================
	if len(currentBatch) > 0 {
		if errChunk := processBatch(currentBatch); errChunk != nil {
			return errChunk
		}
	}

	if len(aggregatedErrors) > 0 {
		return fmt.Errorf("purge failed for target nodes: %s", strings.Join(aggregatedErrors, "; "))
	}

	logger.Infof("purgeStuckPhysicalPathsDualProtocol successfully synchronized all hardware evictions.")
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
	
	err := n.Gater.Execute(ctx, "fc-scsi-fabric-ops", 2, 30*time.Second, func() error {
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
		return nil
	})
	

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
	// 1. FAST & FAILSAFE: Scan the flat, RAM-backed /dev/mapper cache directory 
	// instead of calling filepath.Glob on the slow /sys virtual tree.
	mapperEntries, errDir := os.ReadDir("/dev/mapper")
	if errDir != nil {
		return nil // Fallback safely if the mapper directory is unavailable
	}

	for _, entry := range mapperEntries {
		name := entry.Name()
		if name == "control" {
			continue
		}

		fullPath := filepath.Join("/dev/mapper", name)

		// 2. FAST: Fetch metadata to accurately extract the underlying dm-X name
		fi, errStat := os.Lstat(fullPath)
		if errStat != nil {
			continue
		}

		var otherDmName string
		if fi.Mode()&os.ModeSymlink != 0 {
			realPath, errLink := os.Readlink(fullPath)
			if errLink != nil {
				continue
			}
			otherDmName = filepath.Base(realPath)
		} else {
			statT, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				continue
			}
			minorIndex := unix.Minor(uint64(statT.Rdev))
			otherDmName = fmt.Sprintf("dm-%d", minorIndex)
		}

		// KEEP ORIGINAL: Skip auditing the device we are currently analyzing
		if otherDmName == currentDevName {
			continue
		}

		// 3. FAST: Directly target the specific uuid file instead of searching with wildcards
		sysUuidPath := filepath.Join("/sys/block", otherDmName, "dm", "uuid")
		content, err := os.ReadFile(sysUuidPath)
		if err != nil {
			continue
		}
		foundUUID := normalizeWWID(string(content))

		if foundUUID == targetUUID {
			// 4. FAST: Check holders to see if the mapping is actively open.
			// Instead of calling os.ReadDir which generates whole file entries, we open 
			// the directory using os.Open and check if it contains at least one file entry.
			holdersPath := filepath.Join("/sys/block", otherDmName, "holders")
			hDir, errOpen := os.Open(holdersPath)
			if errOpen == nil {
				// ReadDir(1) reads exactly 1 entry. If it returns entries without EOF error,
				// or if len > 0, it means holders exist. This is a true O(1) existence check.
				holders, _ := hDir.ReadDir(1)
				hDir.Close()

				if len(holders) > 0 {
					return fmt.Errorf("FATAL identity clash: WWID %s is already claimed by active system device %s", targetUUID, otherDmName)
				}
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

	var isNVMe, isDM bool
	baseBlockName := name // Establish our base normalized reference name tracker
	targetSysDir := filepath.Join("/sys/block", name)

	if strings.HasPrefix(name, "dm-") {
		isDM = true
	} else if nvmeNamespaceRegex.MatchString(name) || strings.HasPrefix(name, "nvme") {
		isNVMe = true
		
		// DYNAMIC CONTROLLER IDENTIFICATION:
		if strings.Contains(name, "c") {
			if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
				if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
					ctrlPart := name[:cIdx]  // Extracts "nvme2"
					nsPart := name[lastNIdx:] // Extracts "n1"
					
					baseBlockName = ctrlPart + nsPart // Resolves perfectly to "nvme2n1"
					targetSysDir = filepath.Join("/sys/block", baseBlockName) 
					logger.Debugf("[Sysfs-WWID] Normalized virtual block node routing path: %s -> %s", name, targetSysDir)
				}
			}
		}
	}

	var discoveredID string
	var readErr error

	if isNVMe {
		// FIX 1 COMPLETE: Pass 'baseBlockName' to keep all gater lock keys perfectly aligned node-wide
		if data, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && data != "" {
			discoveredID = normalizeWWID(data)
		} else if data, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" {
			discoveredID = normalizeWWID(data)
		} else {
			readErr = fmt.Errorf("failed to read nguid or uuid attributes from nvme path: %s", targetSysDir)
		}
	} else if isDM {
		if data, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(targetSysDir, "dm", "uuid")); err == nil && data != "" {
			discoveredID = normalizeWWID(data)
		} else if data, err := secureReadSysfs(ctx, r.KeyedGater, name, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" {
			discoveredID = normalizeWWID(data)
		} else {
			readErr = fmt.Errorf("failed to read device mapper uuid attributes from path: %s", targetSysDir)
		}
	} else {
		// =========================================================================
		// TRADITIONAL SCSI VPD PAGE 0x83 PARSING LAYER
		// =========================================================================
		scsiWwidPath := filepath.Join("/sys/block", name, "device", "wwid")
		if data, err := secureReadSysfs(ctx, r.KeyedGater, name, scsiWwidPath); err == nil && data != "" {
			rawContent := strings.TrimSpace(data)
			
			if strings.HasPrefix(rawContent, "naa.") || strings.HasPrefix(rawContent, "t10.") {
				discoveredID = normalizeWWID(rawContent)
			} else {
				cleanedBytesStr := strings.ReplaceAll(rawContent, " ", "")
				if len(cleanedBytesStr) >= 32 {
					discoveredID = normalizeWWID(cleanedBytesStr)
				} else {
					discoveredID = normalizeWWID(rawContent)
				}
			}
		} else {
			readErr = fmt.Errorf("failed to read scsi wwid attribute from path: %s", scsiWwidPath)
		}
	}

	if readErr != nil {
		logger.Errorf("getWWIDBySysfs failed for %s: %v", name, readErr)
		return "", readErr
	}

	logger.Infof("getWWIDBySysfs successfully resolved identity for %s -> %s", name, discoveredID)
	return discoveredID, nil
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
	GetWwnByScsiInq(ctx context.Context, gater *executer.KeyedGater, dev string) (string, error)
	GetVolumeIdVariations(volumeUuid string) []string
	GetMpathDeviceName(ctx context.Context, gater *executer.KeyedGater, volumePath string) (string, error)
	GetMpathVolumeId(ctx context.Context, gater *executer.KeyedGater, mpathDeviceName string) (string, error)
	normalizeWWID(raw string) string
	findDMByWWID(ctx context.Context, wwid string) string
	getSlavesForDevice(ctx context.Context, major, minor uint32) ([]string, error)
	GetOpenCount(ctx context.Context, dmName string) (int32, error)
	GetMajorMinorFromSysfs(ctx context.Context, devicePath string) (major uint32, minor uint32, err error)
	getWWIDByDev(ctx context.Context, major, minor uint32) (string, error)
	WaitForDmToExist(ctx context.Context, gater *executer.KeyedGater, volumeId string, maxRetries int, intervalSeconds int) (string, error)
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

func (o *OsDeviceConnectivityHelperGeneric) WaitForDmToExist(ctx context.Context,  gater *executer.KeyedGater, volumeId string, maxRetries int, intervalSeconds int) (string, error) {
       return o.Helper.WaitForDmToExist(ctx, gater, volumeId, maxRetries, intervalSeconds)
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

/*
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
*/


// TODO unused (has nvme sensitive implementation in 1.13.1)
func (o OsDeviceConnectivityHelperGeneric) GetMpathVolumeId(ctx context.Context, gater *executer.KeyedGater, dmPath string) (volId string, err error) {
	SgInqWwn, err := o.GetWwnByScsiInq(ctx, gater, dmPath)
	if err != nil {
		return "", err
	}
	return SgInqWwn, nil
}

// GetWwnByScsiInq performs a shielded hardware query against a generic SCSI target device node.
func (o *OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(ctx context.Context, gater *executer.KeyedGater, dev string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	cleanKey := filepath.Base(dev)

	// Maintained the explicit framework protection container around raw device I/O actions
	// to securely shield foreground orchestration threads from uninterruptible kernel bus hangs.
	return executer.ExecuteUninterruptible[string](
		ctx,
		o.KeyedGater,
		"inq-"+cleanKey, // Unique device key layout prevents global cross-worker lock contention
		10,              // maxRunning limits simultaneous queries per distinct block track
		50,              // maxSpare
		2*time.Second,   // Fast handoff timeout ceiling
		10*time.Second,  // Strict hard timeout ceiling
		func(wCtx context.Context) (string, error) {
			// FIXED: Prop ве wCtx (the architecture-shielded worker context) down to the pre-flight check
			// to guarantee that framework deadlines are properly monitored and enforced during state checks.
			if o.willIoctl0x83Fail(wCtx, gater, dev) {
				return "", fmt.Errorf("path %s in unsafe state, bypassing ioctl query", dev)
			}
			return o.GetWwnByScsiInqInternal(dev) 
		},
	)
}

// GetWwnByScsiInqInternal executes direct low-level kernel pass-through commands with zero process forks.
func (o *OsDeviceConnectivityHelperGeneric) GetWwnByScsiInqInternal(dev string) (string, error) {
	fd, err := syscall.Open(dev, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		fd, err = syscall.Open(dev, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return "", fmt.Errorf("failed to open device node descriptor: %w", err)
	}
	defer syscall.Close(fd)

	// SCSI command descriptor block: INQUIRY with EVPD=1, Page Code=0x83
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

		if header.Status == 0x08 || header.Status == 0x28 { // BUSY or TASK SET FULL
			time.Sleep(50 * time.Millisecond)
			continue
		}

		if header.Status == 0x02 { // CHECK CONDITION
			senseKey := senseBuf[2] & 0x0f
			if senseKey == 0x06 { // UNIT ATTENTION
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







func (o *OsDeviceConnectivityHelperGeneric) willIoctl0x83Fail(ctx context.Context, gater *executer.KeyedGater, dev string) bool {
	// FLATTENED FOR SIMPLICITY & MAXIMUM PERFORMANCE (Rule 1/5):
	// Replaced the heavy filepath.EvalSymlinks call with a direct, fast memory-backed os.Readlink
	// to resolve device node pointers instantly without triggering recursive directory filesystem walks.
	realPath, err := os.Readlink(dev)
	if err != nil {
		// If it's a direct un-linked path, fallback to using the original raw dev string node name
		realPath = dev
	}
	if !filepath.IsAbs(realPath) {
		realPath = filepath.Join(filepath.Dir(dev), realPath)
	}
	
	devName := filepath.Base(realPath)

	if strings.HasPrefix(devName, "dm-") {
		return o.checkDMDevice(ctx, devName)
	}

	if strings.HasPrefix(devName, "nvme") {
		return o.checkNVMeDevice(ctx, gater, devName)
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

// Aligned struct receiver to pointer token 'r'
func (r *OsDeviceConnectivityHelperGeneric) checkDMDevice(ctx context.Context, dmName string) bool {
	cleanDmName := filepath.Base(dmName)
	suspendedPath := filepath.Join("/sys/block", cleanDmName, "dm/suspended")
	
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
	
	// FLATTENED & CHUNKED FOR PERFORMANCE & DEADLOCK ELIMINATION (Rule 1/5):
	// Replaced the internal nested ExecuteUninterruptible readdir wrapper with a direct context-respecting 
	// 100-entry chunk pagination out of the VFS descriptor descriptor handle. This avoids token exhaustion deadlocks.
	dFile, errOpen := os.Open(slavesPath)
	if errOpen != nil {
		logger.Warningf("no slaves or unreadable path for %s: %v", slavesPath, errOpen)
		return true
	}
	defer dFile.Close()

	for {
		if err := ctx.Err(); err != nil {
			return true 
		}

		slaves, errDirs := dFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			logger.Warningf("error reading dm slaves chunk: %v", errDirs)
			return true
		}
		if len(slaves) == 0 {
			break
		}

		for _, s := range slaves {
			if ctx.Err() != nil {
				return true 
			}
			
			name := s.Name()
			// If at least one underlying physical channel lane is running, the mapper is usable
			if !r.isSCSIDeviceBlocked(name) {
				return false 
			}
		}

		if len(slaves) < 100 || errDirs == io.EOF {
			break
		}
	}
	return true 
}

func (o *OsDeviceConnectivityHelperGeneric) checkNVMeDevice(ctx context.Context, gater *executer.KeyedGater, nvmeName string) bool {
	cleanNvmeName := filepath.Base(nvmeName)
	baseBlockName := cleanNvmeName 
	targetSysDir := filepath.Join("/sys/block", cleanNvmeName)

	if strings.Contains(cleanNvmeName, "c") {
		if lastNIdx := strings.LastIndex(cleanNvmeName, "n"); lastNIdx != -1 && lastNIdx > 0 {
			if cIdx := strings.Index(cleanNvmeName, "c"); cIdx != -1 && cIdx < lastNIdx {
				ctrlPart := cleanNvmeName[:cIdx]  
				nsPart := cleanNvmeName[lastNIdx:] 
				
				baseBlockName = ctrlPart + nsPart // Resolves perfectly to "nvme2n1"
				targetSysDir = filepath.Join("/sys/block", baseBlockName) 
			}
		}
	}

	var stateBytesStr string
	var readErr error

	if stateBytesStr, readErr = secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "device", "state")); readErr != nil {
		if stateBytesStr, readErr = secureReadSysfs(ctx, gater, baseBlockName, filepath.Join("/sys/block", baseBlockName, "device", "state")); readErr != nil {
			ctrlName := ExtractNvmeControllerBase(cleanNvmeName)
			stateBytesStr, readErr = secureReadSysfs(ctx, gater, baseBlockName, filepath.Join("/sys/class/nvme", ctrlName, "state"))
		}
	}

	if readErr != nil || stateBytesStr == "" {
		logger.Warningf("[NVMe-State-Check] [%s] Missing state attributes across all sysfs lookup files: %v. Treating as unmapped.", cleanNvmeName, readErr)
		return true 
	}
	
	s := strings.TrimSpace(strings.ToLower(stateBytesStr))
	logger.Infof("[NVMe-State-Check] [%s] Evaluated live device state string reporting: '%s'", cleanNvmeName, s)
	
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
	mpathdOutput, err := o.Helper.WaitForDmToExist(ctx, o.KeyedGater, volumeId, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec)
	if err != nil {
		return "", err
	}
	return mpathdOutput, nil
}









// GetMpathDeviceName cleanly resolves raw storage block devices protected against D-state freezes.
func (o *OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(ctx context.Context, gater *executer.KeyedGater, volumePath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Maintained the explicit framework protection wrapper at the gateway layer 
	// to securely shield foreground orchestration threads from kernel bus deadlocks.
	return executer.ExecuteUninterruptible(
		ctx,
		gater,
		"mpath-resolve:"+volumePath, // Safe volume-isolated key layout
		5,              // maxRunning limits overlapping checks per unique path
		10,             // maxSpare
		5*time.Second,  // handoffTimeout
		10*time.Second, // hardTimeout
		func(wCtx context.Context) (string, error) {
			// Fast file system link resolution
			realVolumePath, err := os.Readlink(volumePath)
			if err != nil {
				realVolumePath = volumePath
			}
			if !filepath.IsAbs(realVolumePath) {
				realVolumePath = filepath.Join(filepath.Dir(volumePath), realVolumePath)
			}

			if err := wCtx.Err(); err != nil {
				return "", err
			}

			var stat syscall.Stat_t
			if err := syscall.Stat(realVolumePath, &stat); err != nil {
				return "", fmt.Errorf("failed to stat path %s: %w", realVolumePath, err)
			}

			var major, minor uint32
			// Check if this file object is natively a raw block device type
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
				// Passed wCtx to ensure inner processing honors the framework timeline ceilings
				if kernelName, err := o.resolveIdToKernelName(wCtx, gater, major, minor); err == nil {
					return kernelName, nil
				}
			}

			return "", fmt.Errorf("could not resolve a valid multipath device for path %s", volumePath)
		},
	)
}

// resolveIdToKernelName behaves as a high-speed utility leaf.
func (o *OsDeviceConnectivityHelperGeneric) resolveIdToKernelName(ctx context.Context, gater *executer.KeyedGater, major, minor uint32) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)

	// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1):
	// Replaced the internal nested ExecuteUninterruptible wrapper with a direct context-respecting 
	// os.Readlink call. This prevents inner worker slots from deadlocking active parents under load.
	realPath, err := os.Readlink(sysPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve sysfs link %s: %w", sysPath, err)
	}

	// Strip absolute path prefix elements ("../../devices/virtual/block/dm-2" -> "dm-2")
	return filepath.Base(realPath), nil
}

// ResolveToKernelName standardizes diverse input block names back to core system labels.
func (o *OsDeviceConnectivityHelperGeneric) ResolveToKernelName(ctx context.Context, gater *executer.KeyedGater, deviceName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
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
		if err := ctx.Err(); err != nil {
			return "", err
		}

		// FLATTENED FOR PERFORMANCE GAINS (Rule 1): Removed the redundant nested framework container.
		// The individual local syscall.Stat processes in microseconds and runs safely within the parent timeline.
		var stat syscall.Stat_t
		err := syscall.Stat(p, &stat)

		// Successfully stated a valid block device node
		if err == nil && stat.Rdev != 0 && (stat.Mode&syscall.S_IFMT) == syscall.S_IFBLK {
			major := unix.Major(uint64(stat.Rdev))
			minor := unix.Minor(uint64(stat.Rdev))
			
			// Resolve the major:minor tuple cleanly to a kernel label ("dm-2")
			if kernelName, err := o.resolveIdToKernelName(ctx, gater, major, minor); err == nil {
				return kernelName, nil
			}
		}
	}

	// Safe fallback to stripped base name if device is missing from host environment
	return cleanName, nil
}

// findDMByWWID scans /dev/mapper to locate a device-mapper name matching a target SCSI/NVMe string.
func (o *OsDeviceConnectivityHelperGeneric) findDMByWWID(ctx context.Context, wwid string) string {
	if err := ctx.Err(); err != nil {
		return ""
	}

	rawScsiID := normalizeWWID(wwid)
	if rawScsiID == "" {
		return "" 
	}

	expectedNvmeSeq := convertScsiIdToNguid(rawScsiID)

	// FLATTENED & CHUNKED FOR MAXIMUM PERFORMANCE & DEADLOCK ELIMINATION (Rule 1/5):
	// Replaced the internal static framework gater and unbounded ReadDir scan with direct, 
	// context-respecting 100-entry chunk pagination out of the VFS descriptor handle. 
	// This avoids global choke points while capping heap allocations completely.
	sessionClassPath := "/dev/mapper"
	sFile, errOpen := os.Open(sessionClassPath)
	if errOpen != nil {
		return ""
	}
	defer sFile.Close()

	for {
		if err := ctx.Err(); err != nil {
			return ""
		}

		// Stream exactly 100 entries at a time sequentially to bound heap memory allocations
		mapperEntries, errDirs := sFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			break
		}

		for _, file := range mapperEntries {
			if err := ctx.Err(); err != nil {
				return ""
			}

			name := file.Name()
			if name == "control" {
				continue
			}

			fullPath := filepath.Join("/dev/mapper", name)
			
			// os.Lstat reads metadata straight out of memory and will never hang.
			fi, err := os.Lstat(fullPath)
			if err != nil {
				continue
			}

			var dmKernelName string
			if fi.Mode()&os.ModeSymlink != 0 {
				// os.Readlink copies the raw target string out of the symlink inode.
				// It bypasses device lookups completely, making it safe to execute directly.
				realPath, errLink := os.Readlink(fullPath)
				if errLink != nil {
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

			// KEEP ORIGINAL: Read UUID with fallbacks exactly as defined in your business logic (Rule 5)
			uuidContent, err := o.readDmUuidWithFallbacks(ctx, dmKernelName)
			if err != nil {
				continue
			}

			coreHex := normalizeWWID(uuidContent)

			if coreHex == rawScsiID || coreHex == expectedNvmeSeq {
				return name 
			}
		}

		if len(mapperEntries) < 100 || errDirs == io.EOF {
			break
		}
	}

	return ""
}

// getSlavesForDevice returns raw underlying physical block device names safely shielded from D-state locks.
func (r *OsDeviceConnectivityHelperGeneric) getSlavesForDevice(ctx context.Context, major, minor uint32) ([]string, error) {
	logger.Warning("getSlavesForDevice execution tracing initialized")

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 1. FAST IN-MEMORY CHECK: Pre-verify if the target block entry exists in memory.
	// This un-fenced lookup is completely backed by RAM and will never hang, allowing us 
	// to exit early for missing/unlinked nodes without consuming a worker slot.
	basePath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	if _, errStat := os.Stat(basePath); errStat != nil {
		if os.IsNotExist(errStat) {
			return nil, nil // Return gracefully if device holds no underlying storage slaves
		}
	}

	slavesPath := fmt.Sprintf("/sys/dev/block/%d:%d/slaves", major, minor)

	// FIXED: Maintained the explicit infrastructure framework container around the directory 
	// scan to protect your driver threads from total sysfs lockups if storage buses freeze.
	entries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		r.KeyedGater,
		fmt.Sprintf("read-slaves-%d:%d", major, minor), // Device-isolated key avoids global choke points
		20,                                             // Bounded concurrent pool capacity across the host node
		100,                                            // maxSpare
		1*time.Second,                                  // handoffTimeout
		3*time.Second,                                  // hardTimeout
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
		results = append(results, slaveName)
	}
	return results, nil
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
func (o *OsDeviceConnectivityHelperGeneric) getWWIDByDev(ctx context.Context, major, minor uint32) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	basePath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	devKey := fmt.Sprintf("dev-%d:%d", major, minor)

	// =========================================================================
	// 1. DEVICE MAPPER (MULTIPATH) LAYER
	// =========================================================================
	dmUuidPath := filepath.Join(basePath, "dm/uuid")
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

	// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1):
	// Replaced the internal nested ExecuteUninterruptible wrapper with a direct context-respecting 
	// os.Readlink call. This prevents inner worker slots from deadlocking active parents under load.
	realPath, errLink := os.Readlink(basePath)

	if errLink == nil {
		baseBlockName := filepath.Base(realPath)
		normalizedBlockName := baseBlockName // Establish our base normalized name tracker
		
		// DYNAMIC CONTROLLER IDENTIFICATION (Rule 3/5 Parity)
		// Safely strip virtual path channels (e.g., nvme2c0n1 -> nvme2n1) 
		// while fully preserving the true active controller index number.
		if strings.HasPrefix(baseBlockName, "nvme") && strings.Contains(baseBlockName, "c") {
			if lastNIdx := strings.LastIndex(baseBlockName, "n"); lastNIdx != -1 {
				if cIdx := strings.Index(baseBlockName, "c"); cIdx != -1 && cIdx < lastNIdx {
					ctrlPart := baseBlockName[:cIdx]  // Extracts the specific active controller, e.g., "nvme2"
					nsPart := baseBlockName[lastNIdx:] // Extracts the namespace layout suffix, e.g., "n1"
					
					// Synchronize the base block name token reference (Fix 1 Complete)
					normalizedBlockName = ctrlPart + nsPart // Resolves perfectly to "nvme2n1"
					altNvnPath := fmt.Sprintf("/sys/block/%s/wwid", normalizedBlockName) // Resolves perfectly to "/sys/block/nvme2n1/wwid"
					
					// Pass 'normalizedBlockName' to keep all gater lock keys perfectly aligned node-wide (Fix 2 Complete)
					if wwid, err := secureReadSysfs(ctx, o.KeyedGater, normalizedBlockName, altNvnPath); err == nil && wwid != "" {
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

	// OPTIMIZED: syscall.Stat on active node paths under /dev evaluates straight out of memory 
	// and will never hang. Removing the ExecuteUninterruptible wrapper eliminates scheduling chokepoints.
	var s syscall.Stat_t
	if errStat := syscall.Stat(devicePath, &s); errStat != nil {
		return 0, 0, fmt.Errorf("failed to stat device path %s: %w", devicePath, errStat)
	}

	major = unix.Major(s.Rdev)
	minor = unix.Minor(s.Rdev)
	name := filepath.Base(devicePath)

	if (s.Mode&syscall.S_IFMT) == syscall.S_IFCHR && strings.HasPrefix(name, "sg") {
		// OPTIMIZED: Instead of running an expensive os.ReadDir inside sysfs followed by reading the uevent text file, 
		// we look for the sibling sdX block name by checking the memory-backed scsi_device path links.
		// /sys/class/scsi_generic/sgX/device/ sits as an absolute pointer link.
		sysPath := fmt.Sprintf("/sys/class/scsi_generic/%s/device", name)
		
		// Use raw os.Readlink to read the pointer directly out of memory safely.
		// For sgX, this returns a path like: ../../../../../devices/platform/host4/session1/target1:0:0/1:0:0:0
		_, errLink := os.Readlink(sysPath)
		if errLink == nil {
			// Find the block folder inside that same target path
			// The kernel topology layout structure matches: .../1:0:0:0/block/sdX
			hctlDir := filepath.Join("/sys/class/scsi_generic", name, "device")
			blockPath := filepath.Join(hctlDir, "block")

			// Keep this specific block folder read shielded behind ExecuteUninterruptible.
			// This guards your main threads from total sysfs lockups if the SCSI bus is dropping.
			blockEntries, errDir := executer.ExecuteUninterruptible[[]os.DirEntry](
				ctx,
				o.KeyedGater,
				fmt.Sprintf("readdir-block-%s", name),
				20, 100, 1*time.Second, 3*time.Second,
				func(wCtx context.Context) ([]os.DirEntry, error) {
					dFile, errOpen := os.Open(blockPath)
					if errOpen != nil {
						return nil, errOpen
					}
					defer dFile.Close()

					var allEntries []os.DirEntry
					for {
						entries, errEntries := dFile.ReadDir(100)
						if errEntries != nil && errEntries != io.EOF {
							return nil, errEntries
						}
						allEntries = append(allEntries, entries...)
						if len(entries) < 100 || errEntries == io.EOF {
							break
						}
					}
					return allEntries, nil
				},
			)
			
			if errDir == nil && len(blockEntries) > 0 {
				sdName := blockEntries[0].Name()
				
				// OPTIMIZED CHUNK CHECK: Instead of calling secureReadSysfs to read and parse strings 
				// from the uevent text file, we can retrieve the major/minor numbers directly in nanoseconds 
				// by running a fast, un-gated os.Lstat on the matching /dev/sdX block node file handle.
				siblingNode := filepath.Join("/dev", sdName)
				if fi, errLstat := os.Lstat(siblingNode); errLstat == nil {
					if statT, ok := fi.Sys().(*syscall.Stat_t); ok {
						major = unix.Major(statT.Rdev)
						minor = unix.Minor(statT.Rdev)
					}
				} else {
					// COMPATIBILITY FALLBACK: If the /dev/sdX node is absent, fall back seamlessly 
					// to your exact, untouched uevent text parsing routine.
					ueventPath := filepath.Join(blockPath, sdName, "uevent")
					data, errRead := secureReadSysfs(ctx, o.KeyedGater, sdName, ueventPath)
					if errRead == nil && data != "" {
						major, minor = o.parseUeventMajorMinor(data)
					}
				}
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
func (o *OsDeviceConnectivityHelperGeneric) GetGaterKey(ctx context.Context, gater *executer.KeyedGater, devicePath string) string {
	if ctx != nil && ctx.Err() != nil { return "" }

	// Use your safe text-streaming parsing tool to extract identifiers cleanly
	major, minor, _ := o.GetMajorMinorFromSysfs(ctx, devicePath)
	wwid, _ := o.GetDeviceWWID(ctx, gater, devicePath)

	name := filepath.Base(devicePath)
	sysBlockPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	var instanceID string

	// FIX: Execute these fast operations natively without nested ExecuteUninterruptible frames.
	// This preserves your D-state protection (inherited from the outer caller) while removing the deadlock loop.
	if strings.HasPrefix(name, "nvme") {
		subsystemLink := filepath.Join("/sys/block", name, "device")
		if realSubsysPath, err := filepath.EvalSymlinks(subsystemLink); err == nil {
			if subsysSt, errStat := os.Stat(realSubsysPath); errStat == nil {
				if statT, ok := subsysSt.Sys().(*syscall.Stat_t); ok {
					instanceID = fmt.Sprintf("nvme-subsys-ino-%d", statT.Ino)
					return fmt.Sprintf("nvme-shared-%s-%s", wwid, instanceID)
				}
			}
		}
	}

	if sysSt, errStat := os.Stat(sysBlockPath); errStat == nil {
		if statT, ok := sysSt.Sys().(*syscall.Stat_t); ok {
			instanceID = fmt.Sprintf("ino-%d", statT.Ino)
		}
	}

	if instanceID == "" {
		instanceID = fmt.Sprintf("transient-%d", time.Now().UnixNano())
	}

	return fmt.Sprintf("%d:%d-%s-%s", major, minor, wwid, instanceID)
}

// GetDeviceWWID safe-identifies hardware targets across multi-protocol fabrics with full D-state protection.
func (o *OsDeviceConnectivityHelperGeneric) GetDeviceWWID(ctx context.Context, gater *executer.KeyedGater, dev string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	name := filepath.Base(dev)

	if nvmeNamespaceRegex.MatchString(name) || strings.HasPrefix(name, "nvme") {
		// FIX: Restored context propagation down to the NVMe sysfs scanning loop
		return o.GetWwnByNvmeSysfs(ctx, gater, dev)
	}

	// Assume SCSI for everything else (sdX, dm-X, etc) securely passing context parameters
	return o.GetWwnByScsiInq(ctx, gater, dev)
}

// GetWwnByNvmeSysfs extracts NVMe hardware identifiers safely, accommodating fabrics topologies and legacy kernels.
func (o *OsDeviceConnectivityHelperGeneric) GetWwnByNvmeSysfs(ctx context.Context, gater *executer.KeyedGater, dev string) (string, error) {
	name := filepath.Base(dev) // e.g. nvme0n1 or nvme2c0n1
	baseBlockName := name      // Establish our base normalized reference name tracker
	targetSysDir := filepath.Join("/sys/block", name)

	// FIX: DYNAMIC CONTROLLER IDENTIFICATION
	// Safely strip virtual path channels (e.g., nvme2c0n1 -> nvme2n1) 
	// while fully preserving the true active controller index number.
	if strings.Contains(name, "c") {
		if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
			if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
				ctrlPart := name[:cIdx]  // Extracts the specific active controller, e.g., "nvme2"
				nsPart := name[lastNIdx:] // Extracts the namespace layout suffix, e.g., "n1"
				
				// FIX 1 COMPLETE: Synchronize the base block name token reference
				baseBlockName = ctrlPart + nsPart // Resolves perfectly to "nvme2n1"
				targetSysDir = filepath.Join("/sys/block", baseBlockName) 
				logger.Debugf("[NVMe-Sysfs-Wwn] Normalized virtual block node routing path: %s -> %s", name, targetSysDir)
			}
		}
	}

	// FIX 2 COMPLETE: Pass 'baseBlockName' across all calls to keep all gater lock keys perfectly aligned node-wide
	if nguid, err := secureReadSysfs(ctx, o.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && nguid != "" {
		return o.normalizeWWID(nguid), nil
	}

	if uuid, err := secureReadSysfs(ctx, o.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && uuid != "" {
		return o.normalizeWWID(uuid), nil
	}

	if serial, err := secureReadSysfs(ctx, o.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device/serial")); err == nil && serial != "" {
		normSerial := strings.ToLower(strings.TrimSpace(serial))
		// Handle ASCII serial outputs. If it does not form a clean 32-character hexadecimal block, 
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
	WaitForDmToExist(ctx context.Context, gater *executer.KeyedGater, volumeId string, maxRetries int, intervalSeconds int) (string, error)
	GetSlaveCount(ctx context.Context, gater *executer.KeyedGater, devName string) int
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

// WaitForDmToExist blocks securely via uninterruptible context pooling to settle newly attached maps.
func (o GetDmsPathHelperGeneric) WaitForDmToExist(ctx context.Context, gater *executer.KeyedGater, volumeWWID string, maxRetries int, intervalSeconds int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var lastCount int
	var lastRo string
	var stableCycles int

	for attempt := 0; attempt < maxRetries; attempt++ {
		logger.Warningf("attempt %d", attempt)
		if err := ctx.Err(); err != nil {
			logger.Warning("Context expired")
			return "", err
		}

		hasDevice, isPending, name := o.EvaluateSysfsTopology(ctx, gater, volumeWWID, false)
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

		baseBlockName := name // Establish our base normalized reference name tracker

		// DYNAMIC CONTROLLER IDENTIFICATION (Rule 5 Parity)
		if nvmeControllerHeadFormat.MatchString(name) && strings.Contains(name, "c") {
			if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
				if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
					ctrlPart := name[:cIdx]  
					nsPart := name[lastNIdx:] 
					baseBlockName = ctrlPart + nsPart // Resolves perfectly to "nvme2n1"
				}
			}
		}

		path := filepath.Join("/dev", baseBlockName) // Resolves perfectly to "/dev/nvme2n1" or "/dev/dm-2"

		logger.Warning("before IsDeviceMapper")
		isDM := o.IsDeviceMapper(baseBlockName)
		count := 0
		
		if isDM {
			// Track paths via traditional user-space Device Mapper slave catalogs
			logger.Warningf("[Topology-PathCheck] Querying DM slave count metrics for: %s", baseBlockName)
			// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1): 
			// Replaced nested framework wrappers around leaf queries with direct, context-respecting operations.
			count = o.GetSlaveCount(ctx, gater, baseBlockName)
			logger.Warningf("resolved path/slave count is %d", count)
		} else if strings.HasPrefix(baseBlockName, "nvme") {
			// Track paths natively via kernel NVMe subsystem controllers framework
			logger.Warningf("[Topology-PathCheck] Querying Native NVMe transport lane metrics for: %s", baseBlockName)
			
			// FLATTENED & CHUNKED (Rule 1/5): Extracted the sysfs folder parsing pass into direct execution.
			// The file streams natively inherit context boundaries, completely eliminating inner gater lockups.
			subsysDevicesDir := filepath.Join("/sys/block", baseBlockName, "device")
			
			countResult, errOpen := func() (int, error) {
				dFile, err := os.Open(subsysDevicesDir)
				if err != nil {
					return 0, err
				}
				defer dFile.Close()
				
				nvmeLanes := 0
				for {
					if err := ctx.Err(); err != nil {
						return 0, err
					}

					entries, errEntries := dFile.ReadDir(100)
					if errEntries != nil && errEntries != io.EOF {
						return 0, errEntries
					}
					
					for _, entry := range entries {
						entryName := entry.Name()
						// Count valid controller channels (nvme0, nvme2) excluding multi-path namespace nodes
						if strings.HasPrefix(entryName, "nvme") && !strings.Contains(entryName, "-") {
							if nIdx := strings.LastIndex(entryName, "n"); nIdx == -1 || nIdx == 0 {
								nvmeLanes++
							}
						}
					}
					
					if len(entries) < 100 || errEntries == io.EOF {
						break
					}
				}
				return nvmeLanes, nil
			}()

			if errOpen == nil {
				count = countResult
			}
			logger.Warningf("resolved path/slave count is %d", count)
		}
		
		// FLATTENED (Rule 1): Directly evaluate read-only metrics safely within the loop timeline context
		ro := o.getRoStatus(ctx, gater, path)
		logger.Warningf("ro status %s", ro)

		// UNIFIED PATH STABILITY VALIDATION MATRIX:
		isStableCount := count > 0 && count == lastCount
		if isStableCount && ro == lastRo && ro != "unknown" {
			stableCycles++
		} else {
			stableCycles = 0 
		}
		
		logger.Warningf("stableCycles %d", stableCycles)

		if stableCycles >= 2 {
			logger.Warning("2 stable cycles achieved")
			
			if nvmeControllerHeadFormat.MatchString(name) && strings.Contains(name, "c") {
				if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
					ctrlName := ExtractNvmeControllerBase(name) 
					name = fmt.Sprintf("%s%s", ctrlName, name[lastNIdx:]) // Resolves perfectly to "nvme2n1"
					path = filepath.Join("/dev", name)
				}
			}

			logger.Warningf("[Settle-Main] Finalizing path tracks. Target device location: %s", path)

			// FLATTENED FOR STABILITY: Direct execution of hardware settling parameters.
			// Inherited context lineage protects against foreground blocks natively.
			settleErr := o.safeSettle(ctx, gater, path)

			if settleErr == nil {
				// FIXED: Correctly propagate the context lineage down to the validation handler
				validatedPath, valErr := o.validateDMIntegrity(ctx, gater, path)
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

// Locally bounded context select channels to completely eliminate loop timing distortion vulnerabilities.
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
	if err := ctx.Err(); err != nil {
		return 0
	}

	devName = filepath.Base(devName) // Safely clean input paths like /dev/dm-0 -> dm-0

	// =========================================================================
	// 1. DEVICE MAPPER SUBSYSTEM SCAN (SCSI or NVMe-over-DM slaves)
	// =========================================================================
	if o.IsDeviceMapper(devName) {
		slavesDir := filepath.Join("/sys/block", devName, "slaves")
		
		// FLATTENED & CHUNKED FOR PERFORMANCE & DEADLOCK ELIMINATION (Rule 1/5):
		// Replaced the internal nested ExecuteUninterruptible readdir wrapper with a direct context-respecting 
		// 100-entry chunk pagination out of the VFS descriptor handle. This avoids token exhaustion deadlocks.
		dFile, errOpen := os.Open(slavesDir)
		if errOpen != nil {
			logger.Warningf("[DM-Slave-Scan] [%s] Failed to read slaves directory layout: %v", devName, errOpen)
			return 0
		}
		defer dFile.Close()

		logger.Infof("[DM-Slave-Scan] [%s] Inspecting structural slaves in sysfs...", devName)
		operationalCount := 0

		for {
			if err := ctx.Err(); err != nil {
				return 0
			}

			// Stream exactly 100 entries at a time sequentially to bound heap memory allocations
			entries, errDirs := dFile.ReadDir(100)
			if errDirs != nil && errDirs != io.EOF {
				logger.Warningf("[DM-Slave-Scan] [%s] Error reading slaves chunk: %v", devName, errDirs)
				break
			}
			if len(entries) == 0 {
				break
			}

			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return 0
				}

				slaveName := entry.Name() // e.g., sdX or nvmeXn2
				slaveDeviceDir := filepath.Join("/sys/block", devName, "slaves", slaveName, "device")
				
				addrIdentifier := "UNKNOWN"
				if realPath, errLink := os.Readlink(slaveDeviceDir); errLink == nil {
					addrIdentifier = filepath.Base(realPath)
				}

				var hardwareIdentity string
				isNvmeSlave := strings.HasPrefix(slaveName, "nvme")

				// FIXED: Correctly propagate the context lineage down to the sysfs lookups
				if isNvmeSlave {
					nqnPath := filepath.Join(slaveDeviceDir, "subsysnqn")
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

			if len(entries) < 100 || errDirs == io.EOF {
				break
			}
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
		realSubsysPath, err := os.Readlink(subsysSymlink)
		
		targetScanDir := deviceDir
		isNativeMultipathHead := (err == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys"))
		
		if isNativeMultipathHead {
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
		
		// FLATTENED & CHUNKED FOR PERFORMANCE (Rule 1/5): Extracted volatile directory checks 
		// into direct context-respecting chunk loops. Leaves internal worker channels deadlock-free.
		nvmeFile, errOpen := os.Open(targetScanDir)
		if errOpen != nil {
			logger.Warningf("[NVMe-Slave-Scan] [%s] Target NVMe device runtime directory missing or inaccessible: %v", devName, errOpen)
			return 0
		}
		defer nvmeFile.Close()
		
		count := 0
		logger.Infof("[NVMe-Slave-Scan] [%s] Inspecting active controller pathways in tree directory: %s...", devName, targetScanDir)
		
		for {
			if err := ctx.Err(); err != nil {
				return 0
			}

			entries, errDirs := nvmeFile.ReadDir(100)
			if errDirs != nil && errDirs != io.EOF {
				logger.Warningf("[NVMe-Slave-Scan] [%s] Error reading NVMe controller paths chunk: %v", devName, errDirs)
				break
			}
			if len(entries) == 0 {
				break
			}

			for _, e := range entries {
				if err := ctx.Err(); err != nil {
					return 0
				}

				name := e.Name()
				isNamespaceVolume := nvmeNamespaceRegex.MatchString(name)

				isController := strings.HasPrefix(name, "nvme") && !isNamespaceVolume
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

			if len(entries) < 100 || errDirs == io.EOF {
				break
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

// IsNativeNvmeNamespace strictly identifies an active NVMe storage volume disk node,
// mathematically isolating it from structural host bus controller slots.
func (r *GetDmsPathHelperGeneric) IsNativeNvmeNamespace(devName string) bool {
	cleanName := filepath.Base(devName)
	
	if !strings.HasPrefix(cleanName, "nvme") {
		return false
	}

	// FIX: REVERSE PROTOCOL GATER
	// We look for the LAST occurrence of "n". To be a valid storage volume disk partition,
	// the partition separator "n" must sit at an index greater than 0 (e.g., nvme0n1 is index 5).
	// If lastNIdx is 0, it means it's just the first letter of the word "nvme" itself (e.g., nvme0).
	lastNIdx := strings.LastIndex(cleanName, "n")
	if lastNIdx == -1 || lastNIdx == 0 {
		return false // It is a bare parent controller node (e.g. nvme0, nvme2), NOT a data volume disk
	}

	// Double-check alignment against your pre-validated global regex compilation bounds
	return nvmeNamespaceRegex.MatchString(cleanName)
}

// EvaluateSysfsTopology resolves volume WWID targets against device mapper and native NVMe systems.
func (of GetDmsPathHelperGeneric) EvaluateSysfsTopology(ctx context.Context, gater *executer.KeyedGater, rawScsiID string, checkPendingOnly bool) (hasDevice bool, isPending bool, devName string) {
	logger.Warning("EvaluateSysfsTopology")
	
	if err := ctx.Err(); err != nil {
		return false, false, ""
	}

	rawScsiTarget := normalizeWWID(rawScsiID)
	if rawScsiTarget == "" {
		return false, false, ""
	}
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	// 1. FAST & FAILSAFE: Single-pass scan of the RAM-backed /dev directory to find active blocks
	// instead of executing multiple expensive filepath.Glob operations on /sys/block.
	devEntries, errDir := os.ReadDir("/dev")
	if errDir != nil {
		return false, false, ""
	}

	// =========================================================================
	// PHASE 1: DEVICE-MAPPER (DM) EVALUATION
	// =========================================================================
	for _, entry := range devEntries {
		if err := ctx.Err(); err != nil {
			return false, false, ""
		}
		
		name := entry.Name()
		// Target only device-mapper paths by name signature matching
		if !strings.HasPrefix(name, "dm-") {
			continue
		}

		dmPath := filepath.Join("/sys/block", name)

		var contentBytesStr string
		var readErr error
		if contentBytesStr, readErr = secureReadSysfs(ctx, gater, name, filepath.Join(dmPath, "dm", "uuid")); readErr != nil {
			if contentBytesStr, readErr = secureReadSysfs(ctx, gater, name, filepath.Join(dmPath, "uuid")); readErr != nil {
				contentBytesStr, _ = secureReadSysfs(ctx, gater, name, filepath.Join(dmPath, "dm", "name"))
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
			roBytesStr, err := secureReadSysfs(ctx, gater, name, filepath.Join("/sys/block", name, "ro"))
			isReadOnly := err == nil && strings.TrimSpace(roBytesStr) != "0"
			suspendedBytesStr, err := secureReadSysfs(ctx, gater, name, filepath.Join("/sys/block", name, "dm", "suspended"))
			isSuspended := err == nil && strings.TrimSpace(suspendedBytesStr) == "1"

			if isSuspended || isReadOnly {
				return true, true, name
			}
			return true, false, name
		}
	}

	// =========================================================================
	// PHASE 2: NATIVE NVME NAMESPACE EVALUATION
	// =========================================================================
	logger.Warningf("Evaluate nvme matches %s %s", rawScsiTarget, rawNvmeTarget)

	for _, entry := range devEntries {
		if err := ctx.Err(); err != nil {
			return false, false, ""
		}
		
		name := entry.Name()
		// Filter for exactly matching native namespace layout specifications
		if !nvmeNamespaceRegex.MatchString(name) {
			continue
		}

		baseBlockName := name
		m := filepath.Join("/sys/block", name)
		targetSysDir := m

		logger.Warningf("Evaluating %s", name)
		
		// Strips virtual channel routing text (e.g., nvme2c0n1 -> nvme2n1)
		if strings.Contains(name, "c") {
			if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
				if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
					ctrlPart := name[:cIdx]     
					nsPart := name[lastNIdx:]    
					
					baseBlockName = ctrlPart + nsPart 
					targetSysDir = filepath.Join("/sys/block", baseBlockName)
				}
			}
		}

		logger.Warningf("Evaluating %s target is %s baseBlockName is %s", name, targetSysDir, baseBlockName)
		
		var availableIDs []string
		var discoveredID string

		// 2. FAST PATH: Try an ultra-fast binary ioctl on the active /dev node first to get the unique ID.
		deviceNode := filepath.Join("/dev", name)
		if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
			var nvmeInfo nvmeIdTarget
			_, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				df.Fd(),
				uintptr(NVME_IOCTL_ID_TARGET),
				uintptr(unsafe.Pointer(&nvmeInfo)),
			)
			df.Close()

			if errno == 0 {
				discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
				if discoveredID != "" && discoveredID != "00000000000000000000000000000000" {
					availableIDs = append(availableIDs, discoveredID)
				}
			}
		}

		// 3. MULTI-OS COMPATIBILITY FALLBACK (Rule 3 Parity)
		if len(availableIDs) == 0 {
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "device", "wwid")); err == nil && data != "" { availableIDs = append(availableIDs, data) }
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" { availableIDs = append(availableIDs, data) }
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && data != "" { availableIDs = append(availableIDs, data) }
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "device", "serial")); err == nil && data != "" { availableIDs = append(availableIDs, data) }
			
			subsysSymlink := filepath.Join(m, "device", "subsystem")
			realSubsysPath, errLink := os.Readlink(subsysSymlink)
			if errLink == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys") {
				subsysWwidPath := filepath.Join(realSubsysPath, "wwid")
				if data, err := secureReadSysfs(ctx, gater, baseBlockName, subsysWwidPath); err == nil && data != "" {
					availableIDs = append(availableIDs, data)
				}
			}
		}
		
		ctrlName := ExtractNvmeControllerBase(name)
		logger.Warningf("Evaluating %s target is %s controller is %s", name, targetSysDir, ctrlName)

		matchFound := false
		for _, rawID := range availableIDs {
			foundID := normalizeWWID(rawID)
			logger.Warningf("evaluate candidate %s against target %s", foundID, rawNvmeTarget)
			if len(foundID) == 32 && foundID == rawNvmeTarget {
				matchFound = true
				break 
			}
		}

		if matchFound {
			roBytesStr, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "ro"))
			isReadOnly := err == nil && strings.TrimSpace(roBytesStr) != "0"

			var isControllerTransitioning bool
			deviceDir := filepath.Join(targetSysDir, "device") 
			
			// Maintained raw context-bounded chunk loops to ensure zero inner framework deadlocks
			if dFile, errOpen := os.Open(filepath.Join("/sys/block", baseBlockName, "device")); errOpen == nil {
				for {
					if err := ctx.Err(); err != nil {
						break
					}

					entries, errEntries := dFile.ReadDir(100)
					if errEntries != nil && errEntries != io.EOF {
						break
					}

					for _, entry := range entries {
						if err := ctx.Err(); err != nil {
							break
						}

						entryName := entry.Name()
						logger.Warningf("check entry %s", entryName)
						if strings.HasPrefix(entryName, "nvme") && !strings.Contains(entryName, "-") {
							isNamespace := false
							if nIdx := strings.LastIndex(entryName, "n"); nIdx != -1 && nIdx > 0 {
								isNamespace = true
							}
							if !isNamespace {
								logger.Warning("Not namespace")
								statePath := filepath.Join(deviceDir, entryName, "state")
								
								if stateBytesStr, err := secureReadSysfs(ctx, gater, entryName, statePath); err == nil {
									state := strings.ToLower(strings.TrimSpace(stateBytesStr))
									logger.Warningf("state is %s", state)
									if state == "resetting" || state == "connecting" || state == "deleting" {
										isControllerTransitioning = true
										break
									}
								}
							}
						}
					}

					if isControllerTransitioning || len(entries) < 100 || errEntries == io.EOF {
						break
					}
				}
				dFile.Close()
			}

			if isControllerTransitioning || isReadOnly {
				return true, true, baseBlockName
			}
			return true, false, baseBlockName
		}
	}
	return false, false, ""
}

// EvaluateSpecificSysfsTopology checks a specific target device to see if its configuration aligns with expectations.
func (of GetDmsPathHelperGeneric) EvaluateSpecificSysfsTopology(
	ctx context.Context, 
	gater *executer.KeyedGater, 
	targetDeviceName string, 
	rawScsiID string, 
	checkPendingOnly bool,
) (hasDevice bool, isPending bool, err error) {
	
	if err := ctx.Err(); err != nil {
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

	// os.Stat reads metadata straight out of memory and will never hang.
	if _, errStat := os.Stat(dmPath); errStat != nil {
		return false, false, fmt.Errorf("target device mapper entry %s is missing from sysfs: %w", dmName, errStat)
	}

	// =========================================================================
	// TARGETED SPECIFIC DM LAYER EVALUATION
	// =========================================================================
	if strings.HasPrefix(dmName, "dm-") {
		var contentBytesStr string
		var readErr error

		// Read the mapping UUID via your safe wrapper configurations to bypass VFS locks
		if contentBytesStr, readErr = secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "dm", "uuid")); readErr != nil {
			if contentBytesStr, readErr = secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "uuid")); readErr != nil {
				contentBytesStr, _ = secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "dm", "name"))
			}
		}

		if contentBytesStr != "" {
			foundUUID := normalizeWWID(contentBytesStr)
			// Match evaluation against either standard SCSI hex or translated NVMe identities
			if foundUUID == rawScsiTarget || foundUUID == rawNvmeTarget {
				logger.Infof("[Topology-Match] Identity match validated strictly for target %s", dmName)

				roBytesStr, errRo := secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "ro"))
				isReadOnly := errRo == nil && strings.TrimSpace(roBytesStr) != "0"

				suspendedBytesStr, errSusp := secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "dm", "suspended"))
				isSuspended := errSusp == nil && strings.TrimSpace(suspendedBytesStr) == "1"

				if isSuspended || isReadOnly {
					return true, true, nil // Path is trapped in a transient state
				}
				return true, false, nil // Perfectly functional match confirmed
			}
		}
		
		return false, false, nil 
	}

	// =========================================================================
	// TARGETED SPECIFIC NATIVE NVME LAYER EVALUATION
	// =========================================================================
	if strings.HasPrefix(dmName, "nvme") {
		baseBlockName := dmName
		targetSysDir := dmPath
		
		// Strips virtual channel routing text (e.g., nvme2c0n1 -> nvme2n1) (Rule 5 Parity)
		if strings.Contains(dmName, "c") {
			if lastNIdx := strings.LastIndex(dmName, "n"); lastNIdx != -1 && lastNIdx > 0 {
				if cIdx := strings.Index(dmName, "c"); cIdx != -1 && cIdx < lastNIdx {
					ctrlPart := dmName[:cIdx]  
					nsPart := dmName[lastNIdx:] 
					
					baseBlockName = ctrlPart + nsPart 
					targetSysDir = filepath.Join("/sys/block", baseBlockName)
					logger.Debugf("[Spec-Topology] Normalized virtual block node routing path: %s -> %s", dmName, targetSysDir)
				}
			}
		}

		var availableIDs []string

		// 2. FAST PATH: Try an ultra-fast binary ioctl on the active /dev node first to get the unique ID.
		deviceNode := filepath.Join("/dev", dmName)
		if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
			var nvmeInfo nvmeIdTarget
			_, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				df.Fd(),
				uintptr(NVME_IOCTL_ID_TARGET),
				uintptr(unsafe.Pointer(&nvmeInfo)),
			)
			df.Close()

			if errno == 0 {
				discoveredID := normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
				if discoveredID != "" && discoveredID != "00000000000000000000000000000000" {
					availableIDs = append(availableIDs, discoveredID)
				}
			}
		}

		// 3. MULTI-OS COMPATIBILITY FALLBACK (Rule 3 Parity)
		if len(availableIDs) == 0 {
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "device", "wwid")); err == nil && data != "" {
				availableIDs = append(availableIDs, normalizeWWID(data))
			}
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" {
				availableIDs = append(availableIDs, normalizeWWID(data))
			}
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && data != "" {
				availableIDs = append(availableIDs, normalizeWWID(data))
			}
		}

		matchFound := false
		for _, rawID := range availableIDs {
			if rawID == rawNvmeTarget {
				matchFound = true
				break
			}
		}

		if matchFound {
			roBytesStr, errRo := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "ro"))
			isReadOnly := errRo == nil && strings.TrimSpace(roBytesStr) != "0"

			var isControllerTransitioning bool
			
			// Maintained raw context-bounded chunk loops to ensure zero inner framework deadlocks
			if dFile, errOpen := os.Open(filepath.Join("/sys/block", baseBlockName, "device")); errOpen == nil {
				deviceDir := filepath.Join("/sys/block", baseBlockName, "device")
				for {
					if err := ctx.Err(); err != nil {
						break
					}

					entries, errEntries := dFile.ReadDir(100)
					if errEntries != nil && errEntries != io.EOF {
						break
					}

					for _, entry := range entries {
						if err := ctx.Err(); err != nil {
							break
						}

						entryName := entry.Name()
						if strings.HasPrefix(entryName, "nvme") && !strings.Contains(entryName, "-") && !nvmeNamespaceRegex.MatchString(entryName) {
							statePath := filepath.Join(deviceDir, entryName, "state")
							
							if stateBytesStr, errState := secureReadSysfs(ctx, gater, baseBlockName, statePath); errState == nil {
								state := strings.ToLower(strings.TrimSpace(stateBytesStr))
								if state == "resetting" || state == "connecting" || state == "deleting" {
									isControllerTransitioning = true
									break
								}
							}
						}
					}

					if isControllerTransitioning || len(entries) < 100 || errEntries == io.EOF {
						break
					}
				}
				dFile.Close()
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
	data, err := secureReadSysfs(ctx, gater, name, fmt.Sprintf("/sys/block/%s/ro", name))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(data)
}

// safeSettle performs verification loops and small data read tests to ensure 
// that underlying paths have established safe architectural lock/ready layouts.
func (of GetDmsPathHelperGeneric) safeSettle(ctx context.Context, gater *executer.KeyedGater, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	name := filepath.Base(path)
	actualReadPath := path
	baseBlockName := name

	// =========================================================================
	// HARDENED IDENTIFIER RESOLUTION (Rule 5 Parity)
	// =========================================================================
	if nvmeControllerNodePattern.MatchString(name) && strings.Contains(name, "c") {
		if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 {
			if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
				ctrlPart := name[:cIdx]  // Extracts the specific active controller, e.g., "nvme2"
				nsPart := name[lastNIdx:] // Extracts the namespace layout suffix, e.g., "n1"
				
				baseBlockName = ctrlPart + nsPart // Resolves perfectly to "nvme2n1"
				actualReadPath = filepath.Join("/dev", baseBlockName)
				logger.Warningf("[Settle-Sanitize] Normalized target name from %s to core block descriptor %s", name, baseBlockName)
			}
		}
	}

	for i := 0; i < 10; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		if of.IsDeviceMapper(baseBlockName) {
			logger.Warningf("safeSettle DM %s itr %d", baseBlockName, i)
			
			suspendedPath := filepath.Join("/sys/block", baseBlockName, "dm", "suspended")
			suspended, err := secureReadSysfs(ctx, gater, baseBlockName, suspendedPath)
			
			if err == nil && strings.TrimSpace(suspended) == "0" {
				// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1): 
				// Replaced the internal nested ExecuteUninterruptible wrapper with direct, context-respecting 
				// disk I/O. The file descriptor operations cleanly inherit the top-level ctx deadline, 
				// protecting against kernel D-state hangs without causing token self-starvation.
				readErr := func() error {
					f, err := os.OpenFile(actualReadPath, os.O_RDONLY, 0)
					if err != nil {
						return err
					}
					defer f.Close()
					
					buf := make([]byte, 512)
					_, readErr := f.Read(buf)
					return readErr
				}()

				if readErr == nil {
					return nil
				}
			}
		} else {
			logger.Warningf("safeSettle native %s (via %s) itr %d", baseBlockName, actualReadPath, i)
			
			statePath := filepath.Join("/sys/block", baseBlockName, "device", "state")
			stateBytesStr, stateErr := secureReadSysfs(ctx, gater, baseBlockName, statePath)
			
			stateValid := false
			if stateErr == nil {
				state := strings.ToLower(strings.TrimSpace(stateBytesStr))
				if state == "live" || state == "running" {
					stateValid = true
				}
			}

			// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1)
			readErr := func() error {
				f, err := os.OpenFile(actualReadPath, os.O_RDONLY, 0)
				if err != nil {
					return err
				}
				defer f.Close()
				
				buf := make([]byte, 512)
				_, readErr := f.Read(buf)
				return readErr
			}()

			if readErr == nil || stateValid {
				logger.Infof("safeSettle native %s verification successful", baseBlockName)
				return nil
			}
		}
		
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(200+rand.IntN(300)) * time.Millisecond):
		}
	}
	return fmt.Errorf("device %s (read path: %s) failed to settle read tests after maximum tracking limits", name, actualReadPath)
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



// validateDMIntegrity now accepts a context and a KeyedGater to enforce D-state hang protection boundaries.
func (o GetDmsPathHelperGeneric) validateDMIntegrity(ctx context.Context, gater *executer.KeyedGater, dmPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

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
	
	// FLATTENED & CHUNKED FOR PERFORMANCE & DEADLOCK ELIMINATION (Rule 1/5):
	// Replaced the internal nested ExecuteUninterruptible readdir wrapper with a direct context-respecting 
	// 100-entry chunk pagination out of the VFS descriptor handle. This avoids token exhaustion deadlocks.
	dFile, errOpen := os.Open(slavesPath)
	if errOpen != nil {
		return "", fmt.Errorf("dm device %s has no active slave legs attached or unreadable path: %w", dmName, errOpen)
	}
	defer dFile.Close()

	var activePaths int
	var degradedPaths int
	var totalSlaves int

	for {
		if err := ctx.Err(); err != nil {
			return "", ctx.Err()
		}

		// Stream exactly 100 entries at a time sequentially to bound heap memory allocations
		slaves, errDirs := dFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			return "", fmt.Errorf("failed to read dm slaves tree: %w", errDirs)
		}
		if len(slaves) == 0 {
			break
		}

		totalSlaves += len(slaves)

		for _, s := range slaves {
			if err := ctx.Err(); err != nil {
				return "", ctx.Err()
			}

			slaveName := s.Name() // e.g., "sdb" or "nvme0n2"
			slaveDeviceBaseDir := filepath.Join("/sys/block", dmName, "slaves", slaveName, "device")
			statePath := filepath.Join(slaveDeviceBaseDir, "state")

			// =========================================================================
			// BRANCH A: CLASSIC SCSI SLAVE REFACTOR (FIBRE CHANNEL)
			// =========================================================================
			if strings.HasPrefix(slaveName, "sd") {
				// FIXED: Correctly propagate the context lineage down to the sysfs lookups
				stateBytesStr, err := secureReadSysfsFallback(ctx, gater, slaveName, statePath)
				if err == nil {
					stateStr := strings.ToLower(strings.TrimSpace(stateBytesStr))
					if stateStr == "running" {
						activePaths++
					} else {
						degradedPaths++
					}
				} else {
					degradedPaths++
				}

			// =========================================================================
			// BRANCH B: NATIVE NVME FABRIC SLAVES
			// =========================================================================
			} else if strings.HasPrefix(slaveName, "nvme") {
				stateBytesStr, err := secureReadSysfsFallback(ctx, gater, slaveName, statePath)
				if err == nil {
					stateStr := strings.ToLower(strings.TrimSpace(stateBytesStr))
					if stateStr == "live" || stateStr == "running" {
						activePaths++
						continue
					}
				}

				// FALLBACK: Sibling controller scanning using regex to protect fabric controllers
				var controllerPassed bool
				
				// FLATTENED & CHUNKED FOR PERFORMANCE (Rule 1/5): Extracted volatile directory checks 
				// into direct context-respecting chunk loops. Leaves internal worker channels deadlock-free.
				ctrlFile, errOpenCtrl := os.Open(slaveDeviceBaseDir)
				if errOpenCtrl == nil {
					for {
						if err := ctx.Err(); err != nil {
							break
						}

						entries, errEntries := ctrlFile.ReadDir(100)
						if errEntries != nil && errEntries != io.EOF {
							break
						}
						if len(entries) == 0 {
							break
						}

						for _, entry := range entries {
							if err := ctx.Err(); err != nil {
								break
							}

							entryName := entry.Name()
							isNamespaceDisk := nvmeNamespaceRegex.MatchString(entryName)
							
							if strings.HasPrefix(entryName, "nvme") && !isNamespaceDisk && !strings.Contains(entryName, "-") {
								ctrlStatePath := filepath.Join(slaveDeviceBaseDir, entryName, "state")
								
								if ctrlStateBytesStr, err := secureReadSysfsFallback(ctx, gater, entryName, ctrlStatePath); err == nil {
									ctrlState := strings.ToLower(strings.TrimSpace(ctrlStateBytesStr))
									if ctrlState == "live" || ctrlState == "running" {
										activePaths++
										controllerPassed = true
										break 
									}
								}
							}
						}

						if controllerPassed || len(entries) < 100 || errEntries == io.EOF {
							break
						}
					}
					ctrlFile.Close()
				}
				
				if !controllerPassed {
					degradedPaths++
				}
			}
		}

		if len(slaves) < 100 || errDirs == io.EOF {
			break
		}
	}

	logger.Infof("[Integrity-Check] [%s] Path analysis complete. Active lines: %d | Degraded/Preparing lines: %d", dmName, activePaths, degradedPaths)

	// Explicit boundary protection: if 100% of slave tracks are offline, block stage mounting
	if activePaths == 0 {
		return "", fmt.Errorf("dm device %s has zero functional operational paths (Total Slaves: %d, Degraded: %d)", dmName, totalSlaves, degradedPaths)
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


// ExtractNvmeControllerBase safely parses any NVMe string variation 
// (e.g., "nvme2c0n1", "nvme2n1", or raw "nvme2") to return the clean parent controller name.
func ExtractNvmeControllerBase(name string) string {
	cleanName := filepath.Base(name)
	
	// If it contains virtual channel routings (nvme2c0n1), strip the tail starting at the "c"
	if cIdx := strings.Index(cleanName, "c"); cIdx != -1 && cIdx > 0 {
		return cleanName[:cIdx] // Returns "nvme2"
	}
	
	// If it's a standard namespace block (nvme2n1), find the last "n" that isn't the first letter
	if lastNIdx := strings.LastIndex(cleanName, "n"); lastNIdx != -1 && lastNIdx > 0 {
		return cleanName[:lastNIdx] // Returns "nvme2"
	}
	
	// It's already a base controller node (e.g., "nvme2")
	return cleanName
}

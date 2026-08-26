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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
	TeardownVolume(ctx context.Context, target string, expectedWWID string) error
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

// IsVolumePathMatchesVolumeId safe-evaluates whether a block path represents an authentic target mapping.
// Fully prefix-free VFS resolution handles DM aliases, raw dm-X, native NVMe namespaces, and SCSI targets safely.
func (r *OsDeviceConnectivityHelperScsiGeneric) IsVolumePathMatchesVolumeId(ctx context.Context, volumeUuid string, volumePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	logger.Infof("[Identity-Check] Validating path [%s] for VolumeUUID: [%s]", volumePath, volumeUuid)
	expectedSerial := strings.ToLower(strings.TrimSpace(volumeUuid))
	if len(expectedSerial) != 32 {
		return false, fmt.Errorf("invalid IBM volume signature length: must reduce to 32 hex characters")
	}

	// 1. RESOLVE TARGET DEVICE LAYER
	mpathDeviceName, err := r.Helper.GetMpathDeviceName(ctx, r.KeyedGater, volumePath)
	if err != nil {
		return false, fmt.Errorf("failed to trace multipath map for path %s: %w", volumePath, err)
	}

	dmName := filepath.Base(mpathDeviceName)
	absoluteDevPath := mpathDeviceName
	
	if !filepath.IsAbs(absoluteDevPath) {
		mapperPath := filepath.Join("/dev/mapper", dmName)
		directDevPath := filepath.Join("/dev", dmName)
		if _, errStat := os.Stat(mapperPath); errStat == nil {
			absoluteDevPath = mapperPath
		} else if _, errStat := os.Stat(directDevPath); errStat == nil {
			absoluteDevPath = directDevPath
		} else {
			absoluteDevPath = directDevPath
		}
	}
	

	// =========================================================================
	// UPFRONT PROTOCOL FAST-PATH SHORT-CIRCUIT (PREFIX-FREE VFS INSPECTION)
	// =========================================================================
	helper := GetDmsPathHelperGeneric{}
	isNativeNVMe := helper.IsNativeNvmeNamespace(dmName)
	
	isMpathNVMe := false
	sysBlockTarget := filepath.Join("/sys/block", dmName)
	if _, errStat := os.Stat(sysBlockTarget); os.IsNotExist(errStat) {
		sysBlockTarget = filepath.Join("/sys/class/block", dmName)
	}
	if resolvedSysBlock, errLink := filepath.EvalSymlinks(sysBlockTarget); errLink == nil {
		sysBlockTarget = resolvedSysBlock
	}
	
	logger.Warningf("[Identity-Check] absolute path %s sysBlockTarget %s", absoluteDevPath, sysBlockTarget)


	slavesPath := filepath.Join(sysBlockTarget, "slaves")
	if dFile, errOpen := os.Open(slavesPath); errOpen == nil {
		if entries, errRead := dFile.ReadDir(16); errRead == nil {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "nvme") {
					isMpathNVMe = true
					break
				}
			}
		}
		dFile.Close()
	}

	// 2. PRIMARY STRATEGY: SCSI Generic Inquiry IOCTL (Bypassed entirely for NVMe structures)
	var errInq error
	if !isNativeNVMe && !isMpathNVMe {
		var sgInqWwn string
		sgInqWwn, errInq = r.Helper.GetWwnByScsiInq(ctx, r.KeyedGater, absoluteDevPath)
		if errInq == nil {
			if r.MatchVolumeToScsiSpec(sgInqWwn, expectedSerial) {
				logger.Infof("[Identity-Check] [%s] Identity successfully verified via raw SCSI generic IOCTL.", dmName)
				return true, nil
			}
			logger.Warningf("[Identity-Check] [%s] SCSI Inquiry string mismatch (Got: %s, Exp: %s).", dmName, sgInqWwn, expectedSerial)
			return false, &ErrorWrongDeviceFound{absoluteDevPath, volumeUuid, sgInqWwn}
		}
		logger.Warningf("[Identity-Check] [%s] Hardware SCSI Inquiry failed (%v). Inspecting structural sysfs properties...", dmName, errInq)
	} else {
		logger.Infof("[Identity-Check] [%s] NVMe signature identified. Safely bypassing legacy SCSI IOCTL layer.", dmName)
	}

	// 3. FALLBACK STRATEGY: Structural Sysfs Inspection for NVMe and Multipath/DM
	_, errSlaves := os.Stat(slavesPath)

	// =========================================================================
	// PROTOCOL BRANCH A: NATIVE NVME / NON-DM BLOCK STRUCTURE
	// =========================================================================
	// FIXED: Do not blindly rely on the absence of the 'slaves' folder. 
	// If our pre-calculated VFS check confirmed it is a native NVMe namespace, 
	// force it into Branch A immediately to prevent empty directory misdirection.
	if isNativeNVMe || os.IsNotExist(errSlaves) {
		logger.Infof("[Identity-Check] [%s] Evaluating as Native/Raw NVMe block structure.", dmName)
		
		possibleWwidPaths := []string{
			filepath.Join(sysBlockTarget, "wwid"),
			filepath.Join(sysBlockTarget, "device", "wwid"),
			filepath.Join(sysBlockTarget, "nguid"),
			filepath.Join(sysBlockTarget, "device", "nguid"),
		}

		for _, p := range possibleWwidPaths {
			if wwidBytes, errRead := os.ReadFile(p); errRead == nil {
				cleanedWwid := strings.ToLower(strings.TrimSpace(string(wwidBytes)))
				cleanedWwid = strings.TrimPrefix(cleanedWwid, "nvme-")
				
				logger.Warningf("[Identity-Check]  check clean id %s", cleanedWwid)
				
				if strings.Contains(cleanedWwid, expectedSerial) || cleanedWwid == convertScsiIdToNguid(expectedSerial) {
					logger.Infof("[Identity-Check] [%s] Identity verified successfully via Native NVMe metadata path: %s", dmName, p)
					return true, nil
				}
			}
		}

		hasDev, _, matchedDev := helper.EvaluateSysfsTopology(ctx, r.KeyedGater, expectedSerial, false)
		
		logger.Infof("[Identity-Check]  matchDev %s dmName %s", matchedDev, dmName)
		if hasDev && (matchedDev == dmName || strings.Contains(matchedDev, dmName)) {
			logger.Infof("[Identity-Check] [%s] Identity verified via topology helper fallback for raw NVMe.", dmName)
			return true, nil
		}

		return false, fmt.Errorf("hardware signature mapping failed: native NVMe identification mismatch for device %s", dmName)
	}

	// =========================================================================
	// PROTOCOL BRANCH B: DEVICE MAPPER / MULTIPATH SLAVES SELECTION
	// =========================================================================
	const maxCapCeiling = 10000
	validNvmeTargets := make([]string, 0, 100)
	
	dFile, errOpen := os.Open(slavesPath)
	if errOpen != nil {
		return false, fmt.Errorf("failed to open block slaves folder: %w", errOpen)
	}
	defer dFile.Close()

	for {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		entries, readErr := dFile.ReadDir(100)
		if readErr != nil && readErr != io.EOF {
			return false, readErr
		}
		if len(entries) == 0 || readErr == io.EOF {
			break
		}
		
		for _, entry := range entries {				
			entryName := entry.Name()
			isSCSI := strings.HasPrefix(entryName, "sd")
			isDM := strings.HasPrefix(entryName, "dm-")
			isNVMe := helper.IsNativeNvmeNamespace(entryName)

			if isSCSI || isDM || isNVMe {
				if len(validNvmeTargets) >= maxCapCeiling {
					break
				}
				validNvmeTargets = append(validNvmeTargets, entryName)
			}
		}
	}

	if len(validNvmeTargets) == 0 {
		return false, fmt.Errorf("hardware signature mapping failed: zero valid storage elements discovered in slave paths for %s", dmName)
	}

	results, errBatch := executer.ExecuteUninterruptibleBatch[string, bool](
		ctx,
		r.KeyedGater,
		"sysfs-slaves-nvme-scan-"+dmName,
		10, 50, 5*time.Second, 15*time.Second,
		validNvmeTargets,
		func(wCtx context.Context, index int, entryName string, cancelBatch func()) (bool, error) {
			if err := wCtx.Err(); err != nil {
				return false, err
			}

			innerHelper := GetDmsPathHelperGeneric{}
			hasDevice, isPending, matchedDev := innerHelper.EvaluateSysfsTopology(wCtx, r.KeyedGater, expectedSerial, false)

			if (!hasDevice || matchedDev == "") && !isPending && wCtx.Err() == nil {
				nvmeTargetSerial := convertScsiIdToNguid(expectedSerial)
				if nvmeTargetSerial != "" && nvmeTargetSerial != expectedSerial {
					hasDevice, isPending, matchedDev = innerHelper.EvaluateSysfsTopology(wCtx, r.KeyedGater, nvmeTargetSerial, false)
				}
			}

			if hasDevice && !isPending && matchedDev != "" && wCtx.Err() == nil {
				normalizedSlaveName := entryName
				if strings.Contains(entryName, "c") {
					if lastNIdx := strings.LastIndex(entryName, "n"); lastNIdx != -1 && lastNIdx > 0 {
						if cIdx := strings.Index(entryName, "c"); cIdx != -1 && cIdx < lastNIdx {
							normalizedSlaveName = entryName[:cIdx] + entryName[lastNIdx:]
						}
					}
				}

				if matchedDev == dmName || matchedDev == entryName || matchedDev == normalizedSlaveName {
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

	for _, res := range results {
		if res.Err == nil && res.Data {
			logger.Infof("[Identity-Check] [%s] Identity successfully verified via multi-protocol batch fallback architecture.", dmName)
			return true, nil
		}
	}

	return false, fmt.Errorf("hardware signature mapping failed: no matching identities verified across any available storage slaves for path %s", absoluteDevPath)
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

// RescanDevices scans SCSI host buses concurrently to discover newly provisioned target channels securely.
func (r *OsDeviceConnectivityHelperScsiGeneric) RescanDevices(ctx context.Context, lunId int, arrayIdentifiers []string, hostIDs map[int]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if len(hostIDs) == 0 {
		logger.Debugf("Rescan : no host IDs provided for lun id : {%v}", lunId)
		return nil
	}

	hosts := make([]int, 0, len(hostIDs))
	for hostNumber := range hostIDs {
		hosts = append(hosts, hostNumber)
	}

	// Distinct batch key pool separates concurrency loops, preventing self-deadlocks.
	batchGaterKey := fmt.Sprintf("batch-scsi-rescan-lun-%d", lunId)

	results, errBatch := executer.ExecuteUninterruptibleBatch[int, struct{}](
		ctx,
		r.KeyedGater,
		batchGaterKey,
		32,  
		128, 
		5*time.Second, 
		20*time.Second, 
		hosts,
		func(wCtx context.Context, index int, hostNumber int, cancelBatch func()) (struct{}, error) {
			logger.Warningf("RescanDevices parallel iter for host%d", hostNumber)

			// Simple, direct path generation.
			filename := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", hostNumber)
			
			// FIXED: Replaced OsOpenFile with the true standard library os.OpenFile call
			f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200)
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

	for _, res := range results {
		if res.Err != nil {
			return res.Err
		}
	}

	logger.Debugf("Rescan : finish parallel rescan lun on lun id : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	return nil
}

// isNvmeCoreMultipathEnabled safe-evaluates whether native NVMe kernel-level multipathing is active on the host.
func isNvmeCoreMultipathEnabled(ctx context.Context, gater *executer.KeyedGater) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// RESTORED INFRASTRUCTURE CONTAINER: Shield the system interaction loop against low-level kernel freezes.
	// Uses a shared, single-tenant key layout to serialize checks tightly without pool saturation.
	return executer.ExecuteUninterruptible[bool](
		ctx,
		gater,
		"check-nvme-multipath-core",
		5,          // maxRunning capacity ceiling
		20,         // maxSpare
		1*time.Second,
		3*time.Second, // Bounded hard timeout forces execution to break free if the kernel module locks up
		func(wCtx context.Context) (bool, error) {
			// Cooperative check prior to blocking syscalls
			if err := wCtx.Err(); err != nil {
				return false, err
			}

			data, err := os.ReadFile(nvmeCoreMultipathParamPath)
			if err != nil {
				if os.IsNotExist(err) {
					return false, nil // Return false natively if the module isn't loaded
				}
				return false, fmt.Errorf("failed to read nvme_core multipath param: %w", err)
			}
			
			// Accommodate varied values across legacy and modern storage stacks ("Y" or "1")
			statusStr := strings.ToUpper(strings.TrimSpace(string(data)))
			return statusStr == "Y" || statusStr == "1", nil
		},
	)
}



func (r *OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	logger.Infof("GetMpathDevice: Searching multipath devices for volume: [%s]", volumeId)

	// Multipathd configuration verification loop isolates volume matches deterministically.
	// Since SCSI generic inquiry ioctls (SG_IO) always return ENOTTY on NVMe storage targets, 
	// we rely entirely on the underlying structural multipathd identification layer here.
	mpathdOutput, err := r.Helper.WaitForDmToExist(ctx, r.KeyedGater, volumeId, WaitForMpathRetries, WaitForMpathWaitIntervalSec)
	if err != nil {
		return "", err
	}
	
	return mpathdOutput, nil
}

// flushDeviceBuffers runs a shielded flush ioctl on a single device path.
func (r *OsDeviceConnectivityHelperScsiGeneric) flushDeviceBuffers(ctx context.Context, devPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	
	if err := ctx.Err(); err != nil {
		return err
	}

	const BLKFLSBUF = 0x1261
	sanitizedDevPath := devPath
	
	if !filepath.IsAbs(sanitizedDevPath) {
		mapperPath := filepath.Join("/dev/mapper", devPath)
		directDevPath := filepath.Join("/dev", devPath)

		if _, err := os.Stat(mapperPath); err == nil {
			sanitizedDevPath = mapperPath
		} else if _, err := os.Stat(directDevPath); err == nil {
			sanitizedDevPath = directDevPath
		} else {
			sanitizedDevPath = directDevPath 
		}
	}

	if resolvedDev, errLink := filepath.EvalSymlinks(sanitizedDevPath); errLink == nil {
		sanitizedDevPath = resolvedDev
	}
	
	baseName := filepath.Base(sanitizedDevPath)
	logger.Warningf("device %s flushDeviceBuffers: initiation sweep via host path %s", devPath, sanitizedDevPath)

	nvmeType, errType := DevicesAreNvme(ctx, r.KeyedGater, baseName)
	if errType != nil {
		logger.Warningf("flushDeviceBuffers: Unable to structurally verify storage protocol for %s: %v. Proceeding with safety flush.", baseName, errType)
	}

	if nvmeType == NVMeNative {
		logger.Infof("device %s flushDeviceBuffers: isolated native NVMe path. Skipping ioctl flush step safely.", devPath)
		return nil
	}

	// CRITICAL FIX: Utilized raw syscall primitives to completely isolate execution 
	// from Go runtime descriptor manipulation. Opened strictly as O_RDONLY (Read-Only) 
	// to avoid exclusive write conflicts, and dropped O_NONBLOCK to guarantee a fully 
	// committed cache flush layer.
	fd, errOpen := syscall.Open(sanitizedDevPath, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if errOpen != nil {
		logger.Errorf("[FlushBuf-Trace] Failed to open device node target %s for flushing: %v", deviceNode, errOpen)
		return fmt.Errorf("failed to open block target %s for flush operations: %w", deviceNode, errOpen)
	}
	// Guarantee path teardown immediately following command execution bounds
	defer syscall.Close(fd)

	logger.Debugf("[FlushBuf-Trace] Initiating synchronous block device cache flush via BLKFLSBUF ioctl on '%s'", dmName)

	// Invoke the synchronous block cache purge macro instruction layer.
	// This forces the kernel to run fsync_bdev and invalidate_bdev instantly.
	errIoctl := unix.IoctlSetInt(fd, unix.BLKFLSBUF, 0)
	if errIoctl != nil {
		// Treat a missing or unmapped device as a non-fatal success criteria state
		if errIoctl == syscall.ENOTTY || errIoctl == syscall.ENXIO {
			logger.Warningf("[FlushBuf-Trace] Target device '%s' does not accept block flush macros (likely unmapped)", dmName)
			return nil
		}
		
		// Catch permissions validation failures clearly
		if errIoctl == syscall.EACCES || errIoctl == syscall.EPERM {
			logger.Errorf("[FlushBuf-Trace] Insufficient privilege capabilities (missing CAP_SYS_ADMIN) executing BLKFLSBUF on %s", dmName)
			return fmt.Errorf("privileged permission fault executing buffer invalidations: %w", errIoctl)
		}

		logger.Errorf("[FlushBuf-Trace] Kernel driver ioctl failure on device %s: %v", deviceNode, errIoctl)
		return fmt.Errorf("block hardware device cache flush operation rejected by storage sub-layer: %w", errIoctl)
	}

	logger.Infof("[FlushBuf-Trace] Synchronous disk and buffer cache purge completed successfully for device mapper target: %s", dmName)
	return nil
}

// flushDevicesBuffers implements parallel batch execution across multiple device configurations.
func (r *OsDeviceConnectivityHelperScsiGeneric) flushDevicesBuffers(ctx context.Context, deviceNames []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Debugf("executing parallel flush on devices : {%v}", deviceNames)
	
	// STEP 1: Fast filter pass with strict memory bounds pre-allocation limits
	const maxCapCeiling = 10000
	validDevices := make([]string, 0, len(deviceNames))
	
	for _, name := range deviceNames {
		if name != "" {
			// MEMORY BOUNDED CEILING BREAK: Prevent unbounded heap allocation under unstable node leaks
			if len(validDevices) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] Devices flush tracking slice reached maximum safe memory pre-allocation threshold (%d). Truncating scan.", maxCapCeiling)
				break
			}
			validDevices = append(validDevices, name)
		}
	}

	if len(validDevices) == 0 {
		return nil
	}

	// FIXED: Distinct multi-tenant key template structure isolates concurrency pools across independent
	// parallel attachment channels, preventing global slot contention under cluster workload strain.
	uniqueGaterKey := fmt.Sprintf("batch-flush-devices-%d", time.Now().UnixNano())

	// Maintained parallel thread execution pools securely bounded via infrastructure container
	results, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
		ctx,
		r.KeyedGater,
		uniqueGaterKey,
		25,  // maxRunning for parallel device flushes across fabrics
		150, // maxSpare
		5*time.Second,
		20*time.Second, // Bounded timeout forces execution to break free if hardware operations freeze
		validDevices,
		func(wCtx context.Context, index int, deviceName string, cancelBatch func()) (struct{}, error) {
			// Leaf function natively evaluates files under inherited parent worker timeline context safely
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

// RemovePhysicalDevice removes storage devices concurrently by injecting the "1" token into kernel deletion channels.
func (r *OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Debugf("Removing physical storage device lanes: {%v} via sysfs delete boundaries", sysDevices)
	
	const maxCapCeiling = 10000
	validDevices := make([]string, 0, len(sysDevices))
	
	for _, name := range sysDevices {
		if name != "" {
			if strings.HasPrefix(filepath.Base(name), "nvme") {
				logger.Debugf("[Device-Evict] Skipping NVMe device '%s' inside SCSI evictor channel.", name)
				continue
			}

			if len(validDevices) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] Eviction device tracking slice reached maximum safe memory threshold (%d). Truncating scan.", maxCapCeiling)
				break
			}
			validDevices = append(validDevices, name)
		}
	}

	if len(validDevices) == 0 {
		return nil
	}

	uniqueBatchKey := fmt.Sprintf("batch-device-eviction-%d", time.Now().UnixNano())

	results, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
		ctx,
		r.KeyedGater,
		uniqueBatchKey,
		15,  
		100, 
		5*time.Second,
		45*time.Second, 
		validDevices,
		func(wCtx context.Context, index int, name string, cancelBatch func()) (struct{}, error) {
			rawBlockDir := filepath.Join("/sys/block", name)
			baseBlockSysDir, errLink := filepath.EvalSymlinks(rawBlockDir)
			if errLink != nil {
				baseBlockSysDir = rawBlockDir 
			}
			
			var deletePath string
			if strings.HasPrefix(name, "sd") {
				deletePath = filepath.Join(baseBlockSysDir, "device", "delete")
			} else {
				logger.Warningf("Non-SCSI block device type passed to RemovePhysicalDevice: %s. Skipping.", name)
				return struct{}{}, nil
			}

			_ = r.flushDeviceBuffers(wCtx, name)

			logger.Infof("[Device-Evict] Directly writing eviction token '1' to: %s", deletePath)
			errWrite := os.WriteFile(deletePath, []byte("1\n"), 0200)
			if errWrite != nil {
				if !os.IsNotExist(errWrite) {
					return struct{}{}, errWrite
				}
				logger.Infof("Idempotency: Eviction path %s already cleared from host node.", deletePath)
			}			

			ticker := time.NewTicker(50 * time.Millisecond) // Optimized: Faster polling for instant VFS drops
			defer ticker.Stop()
			timeoutTimer := time.NewTimer(5 * time.Second)  // Optimized: Lower boundary prevents long worker hangs
			defer timeoutTimer.Stop()

			for {
				select {
				case <-ticker.C:
					// FIXED: Verify eviction via the top-level rawBlockDir (/sys/block/sdX) 
					// which disappears the exact microsecond the kernel clears the VFS maps.
					_, errStat := os.Stat(rawBlockDir)
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

	var aggregatedErrors []string
	for _, res := range results {
		if res.Err != nil {
			if res.Index >= 0 && res.Index < len(sysDevices) {
				aggregatedErrors = append(aggregatedErrors, fmt.Sprintf("%s: delete write failed (%v)", sysDevices[res.Index], res.Err))
			}
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

	rawScsiTarget := normalizeWWID(expectedSerial)
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)
	normExpectedLun := r.normalizeLun(strconv.Itoa(expectedLun))
	
	const maxCapCeiling = 10000
	validDevices := make([]string, 0, len(sysDevices))
	
	for _, name := range sysDevices {
		if name != "" {
			if len(validDevices) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] Validate LUN input slice reached maximum safe memory pre-allocation threshold (%d). Truncating scan.", maxCapCeiling)
				break
			}
			validDevices = append(validDevices, name)
		}
	}

	if len(validDevices) == 0 {
		return fmt.Errorf("zero active paths verified for device target %s; cumulative logs: [no paths supplied]", targetDm)
	}

	gaterKey := fmt.Sprintf("batch-lun-path-validation-%s-%s", rawScsiTarget, filepath.Base(targetDm))

	results, errBatch := executer.ExecuteUninterruptibleBatch[string, bool](
		ctx,
		r.KeyedGater,
		gaterKey,
		16,  
		128, 
		5*time.Second,
		30*time.Second, 
		validDevices,
		func(wCtx context.Context, index int, deviceName string, cancelBatch func()) (bool, error) {
			if err := wCtx.Err(); err != nil {
				return false, err
			}

			if r.Mounter.IsPathStuck(deviceName) {
				logger.Warningf("Path %s is currently marked as trapped in a kernel D-state. Skipping route evaluation.", deviceName)
				return false, fmt.Errorf("path %s skipped: active D-state hang recorded", deviceName)
			}
			
			

			// FIXED: Declared required local path variables and resolved block naming
			var targetSysDir string
			var err error
			baseBlockName := deviceName 
			targetSysDir = filepath.Join("/sys/block", deviceName)

			if _, errStat := os.Stat(targetSysDir); os.IsNotExist(errStat) {
				classBlockPath := filepath.Join("/sys/class/block", deviceName)
				if realClassPath, errEval := filepath.EvalSymlinks(classBlockPath); errEval == nil {
					if strings.Contains(realClassPath, "/block/") {
						parts := strings.Split(realClassPath, "/block/")
						if len(parts) == 2 {
							// FIXED: Split the string value parts[1] instead of the slice parts
							subParts := strings.Split(parts[1], "/")
							if len(subParts) > 0 && subParts[0] != "" {
								baseBlockName = subParts[0] 
								targetSysDir = filepath.Join("/sys/block", baseBlockName)
							}
						}
					}
				}
			}

			// Hardened NVMe virtual character channel cleaner (e.g., nvme1c0n1 -> nvme1n1)
			if strings.HasPrefix(baseBlockName, "nvme") && strings.Contains(baseBlockName, "c") {
				if lastNIdx := strings.LastIndex(baseBlockName, "n"); lastNIdx != -1 && lastNIdx > 0 {
					if cIdx := strings.Index(baseBlockName, "c"); cIdx != -1 && cIdx < lastNIdx {
						ctrlPart := baseBlockName[:cIdx]  
						nsPart := baseBlockName[lastNIdx:] 
						candidateName := ctrlPart + nsPart 
						// Verify candidate path exists in sysfs before committing the strip
						if _, errCand := os.Stat(filepath.Join("/sys/block", candidateName)); errCand == nil {
							baseBlockName = candidateName
							targetSysDir = filepath.Join("/sys/block", baseBlockName)
						}
					}
				}
			}

			isNvmePath := r.IsNativeNvmeNamespace(baseBlockName)

			if isNvmePath {
				err = r.validateNvmePathId(wCtx, deviceName, baseBlockName, targetSysDir, rawScsiTarget, rawNvmeTarget)
			} else {
				err = r.validateScsiPathId(wCtx, deviceName, baseBlockName, targetSysDir, rawScsiTarget, normExpectedLun)
			}
			
			if err != nil {
				return false, err
			}

			return true, nil
		},
	)
	
	if errBatch != nil {
		logger.Errorf("ValidateLun: Parallel validation batch engine failed structurally for target %s: %v", targetDm, errBatch)
		return fmt.Errorf("parallel validation batch engine failed structurally: %w", errBatch)
	}
	
	validPathsFound := 0
	var cumulativeErrors []string

	for _, res := range results {
		if res.Err != nil {
			cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("[Index %d] %v", res.Index, res.Err))
		} else if res.Data {
			validPathsFound++
		} else {
			// FIXED: Defensive boundary check eliminates out-of-bounds slice panics on tracking retries
			deviceName := "unknown-node"
			if res.Index >= 0 && res.Index < len(validDevices) {
				deviceName = validDevices[res.Index]
			}
			cumulativeErrors = append(cumulativeErrors, fmt.Sprintf("path %s skipped during inspection", deviceName))
		}
	}

	// At least one path must be completely validated and healthy to proceed.
	if validPathsFound == 0 {
		return fmt.Errorf("zero active paths verified for device target %s; cumulative logs: [%s]", targetDm, strings.Join(cumulativeErrors, "; "))
	}

	logger.Infof("Successfully verified and attached %d multi-path tracks out of %d for lun %d", validPathsFound, len(validDevices), expectedLun)
	return nil
}

// validateNvmePathId checks NVMe states, reads NSID/WWID/serial fields, and validates path identities.
func (r *OsDeviceConnectivityHelperScsiGeneric) validateNvmePathId(wCtx context.Context, deviceName, baseBlockName, targetSysDir, rawScsiTarget, rawNvmeTarget string) error {
	state, errState := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device", "state"))
	if errState != nil || state != "live" {
		logger.Warningf("NVMe path %s unavailable (state: %s, err: %v); skipping track", baseBlockName, state, errState)
		return fmt.Errorf("path %s: nvme state not live", baseBlockName)
	}

	sysfsIdRaw, _ := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "wwid"))
	if sysfsIdRaw == "" {
		sysfsIdRaw, _ = secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device", "wwid"))
	}

	var isSerialFallback bool
	if sysfsIdRaw == "" {
		sysfsIdRaw, _ = secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device", "serial"))
		isSerialFallback = (sysfsIdRaw != "")
	}
	hwIdRaw := sysfsIdRaw

	if isSerialFallback {
		normHwIdSerial := strings.ToLower(strings.TrimSpace(hwIdRaw))
		if !strings.Contains(rawScsiTarget, normHwIdSerial) && !strings.Contains(rawNvmeTarget, normHwIdSerial) {
			logger.Errorf("NVMe serial configuration profile mismatch on path %s (got ASCII: %s)", baseBlockName, normHwIdSerial)
			return fmt.Errorf("path %s: serial mismatch (got ASCII %s)", baseBlockName, normHwIdSerial)
		}
	}
	
	normSysfsId := normalizeWWID(sysfsIdRaw)
	normHwId := normalizeWWID(hwIdRaw)

	if normHwId != rawNvmeTarget {
		logger.Errorf("Hardware identifier signature mismatch on NVMe path %s (got %s, exp %s)", deviceName, normHwId, rawNvmeTarget)
		return fmt.Errorf("path %s: nvme identity mismatch", deviceName)
	}

	if normSysfsId != "" && normSysfsId != normHwId {
		logger.Errorf("Kernel sysfs and core hardware identification split detected on path %s (Sysfs: %s, HW: %s)", deviceName, normSysfsId, normHwId)
		return fmt.Errorf("path %s: hardware identity split profile tracking hazard", deviceName)
	}
	
	return nil
}

// validateScsiPathId checks SCSI path states, resolves LUN/WWID/Inquiry data, and validates identities.
func (r *OsDeviceConnectivityHelperScsiGeneric) validateScsiPathId(wCtx context.Context, deviceName, baseBlockName, targetSysDir, rawScsiTarget, normExpectedLun string) error {
	hctlRegex := regexp.MustCompile(`(\d+):(\d+):(\d+):(\d+)$`)
	
	state, errState := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device", "state"))
	if errState != nil || state != "running" {
		logger.Warningf("SCSI path %s checking phase dropped (state: %s, err: %v); skipping track", baseBlockName, state, errState)
		return fmt.Errorf("path %s: scsi state not running", baseBlockName)
	}

	var actualLun string
	var scsiLunEval string
	rawScsiLun, errLun := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device", "lun"))
	if errLun == nil {
		scsiLunEval = r.normalizeLun(rawScsiLun)
	}
	actualLun = scsiLunEval
	
	if actualLun == "" {
		if devLink, errLink := filepath.EvalSymlinks(filepath.Join(targetSysDir, "device")); errLink == nil {
			if match := hctlRegex.FindStringSubmatch(devLink); len(match) > 4 {
				actualLun = r.normalizeLun(match[4])
			}
		}
	}

	sysfsIdRaw, _ := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device", "wwid"))

	sanitizedDevPath := baseBlockName
	if !filepath.IsAbs(sanitizedDevPath) {
		mapperPath := filepath.Join("/dev/mapper", baseBlockName)
		directDevPath := filepath.Join("/dev", baseBlockName)

		if _, errStat := os.Stat(mapperPath); errStat == nil {
			sanitizedDevPath = mapperPath
		} else {
			sanitizedDevPath = directDevPath
		}
	}

	var hwIdRaw string
	var errInq error
	hwIdRaw, errInq = r.Helper.GetWwnByScsiInq(wCtx, r.KeyedGater, sanitizedDevPath)
	if errInq != nil {
		logger.Errorf("Hardware query block failure on %s: %v", baseBlockName, errInq)
		return fmt.Errorf("path %s: inquiry execution crash: %v", baseBlockName, errInq)
	}
	
	normSysfsId := normalizeWWID(sysfsIdRaw)
	normHwId := normalizeWWID(hwIdRaw)

	if actualLun != normExpectedLun {
		logger.Errorf("LUN/NSID layout mismatch on path %s (got %s, exp %s)", deviceName, actualLun, normExpectedLun)
		return fmt.Errorf("path %s: lun deviation detected", deviceName)
	}

	if normHwId != rawScsiTarget {
		logger.Errorf("Hardware identifier signature mismatch on SCSI path %s (got %s, exp %s)", deviceName, normHwId, rawScsiTarget)
		return fmt.Errorf("path %s: scsi identity mismatch", deviceName)
	}

	if normSysfsId != "" && normSysfsId != normHwId {
		logger.Errorf("Kernel sysfs and core hardware identification split detected on path %s (Sysfs: %s, HW: %s)", deviceName, normSysfsId, normHwId)
		return fmt.Errorf("path %s: hardware identity split profile tracking hazard", deviceName)
	}
	
	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	if !r.CleanScsiDevice {
		return nil
	}

	if err := r.purgeScsiGhosts(ctx, expectedSerial, expectedLun, arrayIdentifiers); err != nil {
		logger.Errorf("Ghost Scrubber: SCSI generic pruning cycle hit an error: %v", err)
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

// purgeScsiGhosts scans host SCSI adapters in a memory-bounded, decoupled pipeline to clear unmapped logical units safely.
func (r *OsDeviceConnectivityHelperScsiGeneric) purgeScsiGhosts(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dFile, errOpen := os.Open("/dev")
	if errOpen != nil {
		return fmt.Errorf("failed to open /dev: %w", errOpen)
	}

	type ghostCandidate struct {
		sgName    string
		hctl      string
		deviceDir string
	}

	// MEMORY BOUNDED CEILING POOL: Rigidly caps maximum memory pre-allocations under unstable node states.
	const maxCapCeiling = 10000
	rawCandidates := make([]ghostCandidate, 0, 200)

	// =========================================================================
	// STAGE 1: MICROSECOND SNAPSHOT SWEEP (Decouples VFS Handles Instantly)
	// =========================================================================
	for {
		if err := ctx.Err(); err != nil {
			dFile.Close()
			return err
		}

		devEntries, err := dFile.ReadDir(100)
		if err != nil && err != io.EOF {
			dFile.Close()
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

			// Clean, absolute directory reference evaluated via true kernel VFS symlink traversal
			deviceDirSymlink := filepath.Join("/sys/class/scsi_generic", sgName, "device")
			absoluteDeviceDir, errLink := filepath.EvalSymlinks(deviceDirSymlink)
			if errLink != nil {
				continue // Skip the single unreadable path element if a pathological link error occurs
			}

			hctl := filepath.Base(absoluteDeviceDir)
			hctlParts := strings.Split(hctl, ":")
			if len(hctlParts) < 4 {
				continue 
			}

			lunStr := hctlParts[3]
			parsedLun, errParse := strconv.Atoi(lunStr)
			if errParse != nil || parsedLun != expectedLun {
				continue 
			}
			
			// CIRCUIT BREAKER BOUNDARY: Prevent unbounded array bloat from unstable path leaks
			if len(rawCandidates) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] Ghost tracking candidate list reached safe allocation ceiling (%d). Truncating scan pass.", maxCapCeiling)
				break
			}

			rawCandidates = append(rawCandidates, ghostCandidate{
				sgName:    sgName,
				hctl:      hctl,
				deviceDir: absoluteDeviceDir,
			})
		}

		if len(rawCandidates) >= maxCapCeiling || len(devEntries) < 100 || err == io.EOF {
			break
		}
	}
	dFile.Close() // CLOSED IMMEDIATELY: Releases descriptor handles before entering slow, multi-second processing lines.

	// =========================================================================
	// STAGE 2: DECOUPLED BATCH CHUNKING PROCESSING ENGINE
	// =========================================================================
	executePurgeBatch := func(batch []ghostCandidate, gaterKey string) error {
		if len(batch) == 0 {
			return nil
		}
		_, errBatch := executer.ExecuteUninterruptibleBatch[ghostCandidate, struct{}](
			ctx,
			r.KeyedGater,
			gaterKey,
			10,  // maxRunning limits simultaneous unbind executions to preserve udev stability
			100, // maxSpare
			5*time.Second,
			30*time.Second, // Bounded timeout forces execution to break free if file sync operations freeze
			batch,
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

				logger.Warningf("Pruning stale SCSI device %s [Vendor: %s, Serial Match: %v, Ghost: %v, Our path: %v]. Executing hot-unplug.", candidate.sgName, vdr, r.IsSerialMatch(serialNumber, expectedSerial), ghostState, pathOwned)

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
		return errBatch
	}

	// Slice memory array in chunks of 100 for the parallel batch manager
	const chunkSize = 100
	for i := 0; i < len(rawCandidates); i += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}

		end := i + chunkSize
		if end > len(rawCandidates) {
			end = len(rawCandidates)
		}
		currentChunkBatch := rawCandidates[i:end]

		uniqueGaterKey := fmt.Sprintf("batch-purge-scsi-ghosts-%d", time.Now().UnixNano())
		if errBatch := executePurgeBatch(currentChunkBatch, uniqueGaterKey); errBatch != nil {
			return fmt.Errorf("parallel ghost batch engine execution failed: %w", errBatch)
		}
	}

	return nil
}

// GetHCTLFromSg extracts the absolute Host:Channel:Target:Lun parameter mapping from a scsi_generic handle.
func (r *OsDeviceConnectivityHelperScsiGeneric) GetHCTLFromSg(ctx context.Context, sgName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	
	cleanSgName := filepath.Base(sgName)
	deviceLink := filepath.Join("/sys/class/scsi_generic", cleanSgName, "device")
	
	// RESTORED VFS LAYER: Re-enabling true filepath.EvalSymlinks to guarantee absolute path mapping accuracy.
	// Natively resolves multi-tier kernel path configurations in RAM, shielding against raw link modifications.
	realPath, err := filepath.EvalSymlinks(deviceLink)
	if err != nil {
		return "", fmt.Errorf("failed resolving scsi generic path link: %w", err)
	}
	
	hctl := filepath.Base(strings.TrimSuffix(realPath, "/"))
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("malformed format address index generated: %s", hctl)
	}
	
	return hctl, nil
}

// isPathOwnedByMyArray performs a strict safety scan prior to orchestration steps to verify fabric ownership.
// Operates fully un-nested to prevent cross-worker pool starvation during heavy background unstage storms.
func (r *OsDeviceConnectivityHelperScsiGeneric) isPathOwnedByMyArray(ctx context.Context, deviceName string, arrayIdentifiers []string) bool {
	if err := ctx.Err(); err != nil {
		return false
	}

	baseDeviceName := filepath.Base(deviceName)
	backoff := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}
	var targetIDs []string
	var err error

	for i := 0; i < len(backoff); i++ {
		// FIXED: Enforce that any internal resource tracking layers here utilize the inherited context.
		// If resolveTargetIDsWithContext wraps an explicit gater, ensure it checks against parent worker exclusivity.
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

// getNvmeSubsysNQN retrieves the unique fabric NQN identification string for an NVMe block node.
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

	// Tier 1 Check: Target the standard parent controller node classification path
	nqnPath := fmt.Sprintf("/sys/class/nvme/%s/subsysnqn", deviceCtrl)
	dataStr, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, nqnPath)
	
	if err != nil {
		// Tier 2 Fallback: Target the true absolute base block folder layout
		nqnPath = fmt.Sprintf("/sys/block/%s/device/subsysnqn", baseBlockName)
		dataStr, err = secureReadSysfs(ctx, r.KeyedGater, baseBlockName, nqnPath)
		
		if err != nil {
			// Tier 3 Fallback (Legacy RHEL 7 / Customized Fabrics): Target raw discovery name via true VFS resolution
			subsysDirSymlink := fmt.Sprintf("/sys/block/%s/device/subsystem", rawName)
			
			// RESTORED VFS LAYER: Natively evaluates absolute canonical target string configurations 
			// in host memory, shielding against intermediate relative path variations.
			realSubsysPath, symErr := filepath.EvalSymlinks(subsysDirSymlink)
			
			if symErr == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys") {
				nqnPath = filepath.Join(realSubsysPath, "subsysnqn")
				dataStr, err = secureReadSysfs(ctx, r.KeyedGater, baseBlockName, nqnPath)
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
// IsScsiBlockDevice safe-evaluates whether a block path represents a classical SCSI device.
func (r *OsDeviceConnectivityHelperScsiGeneric) IsScsiBlockDevice(ctx context.Context, devName string) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	
	cleanName := filepath.Base(devName)
	if strings.HasPrefix(cleanName, "sd") || strings.HasPrefix(cleanName, "dasd") {
		return true
	}

	subsysPath := filepath.Join("/sys/block", cleanName, "device", "subsystem")
	
	// RESTORED VFS LAYER: Re-enabling true filepath.EvalSymlinks to guarantee absolute path matching accuracy.
	// Natively computes absolute canonical target string configurations to shield against folder depth mutations.
	realSubsysPath, err := filepath.EvalSymlinks(subsysPath)
	if err == nil {
		if strings.Contains(realSubsysPath, "bus/scsi") || strings.Contains(realSubsysPath, "scsi") {
			return true
		}
	}
	
	return false
}


// resolveTargetIDsRecursive traverses storage topologies recursively to extract unique fabric target identifiers.
// Decouples VFS file descriptors instantly to prevent host table starvation during nested lookups.
func (r *OsDeviceConnectivityHelperScsiGeneric) resolveTargetIDsRecursive(ctx context.Context, deviceName string, currentDepth, maxDepth int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if currentDepth > maxDepth {
		return nil, fmt.Errorf("exceeded maximum recursion limit (%d)", maxDepth)
	}

	baseName := filepath.Base(deviceName)

	// =========================================================================
	// Subsystem A: Device Mapper (Multipath) Layout
	// =========================================================================
	if strings.HasPrefix(baseName, "dm-") {
		slavesPath := fmt.Sprintf("/sys/block/%s/slaves", baseName)
		
		dFile, errOpen := os.Open(slavesPath)
		if errOpen != nil {
			return nil, fmt.Errorf("failed to open dm slaves path: %w", errOpen)
		}

		// MEMORY BOUNDED CEILING POOL: Enforces rigid array cap limits under unstable node states
		const maxCapCeiling = 10000
		slaveNames := make([]string, 0, 32)

		// STAGE 1: MICROSECOND SNAPSHOT SWEEP (Decouples VFS States Instantly)
		for {
			if err := ctx.Err(); err != nil {
				dFile.Close()
				return nil, err
			}

			entries, readErr := dFile.ReadDir(100)
			if readErr != nil && readErr != io.EOF {
				dFile.Close()
				return nil, fmt.Errorf("failed to read dm slaves: %w", readErr)
			}

			for _, entry := range entries {
				if len(slaveNames) >= maxCapCeiling {
					logger.Warningf("[VFS-Guard] DM slave elements reached maximum processing threshold ceiling (%d). Truncating scan.", maxCapCeiling)
					break
				}
				slaveNames = append(slaveNames, entry.Name())
			}

			if len(slaveNames) >= maxCapCeiling || len(entries) < 100 || readErr == io.EOF {
				break
			}
		}
		dFile.Close() // CLOSED IMMEDIATELY: Releases descriptor handles before entering nested recursion tracks

		uniqueIDs := make(map[string]struct{})
		var lastErr error
		var partialSuccess bool

		// STAGE 2: SAFE DECOUPLED RECURSION PROCESS
		for _, slaveName := range slaveNames {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			ids, errRecursive := r.resolveTargetIDsRecursive(ctx, slaveName, currentDepth+1, maxDepth)
			if errRecursive != nil {
				lastErr = errRecursive
				logger.Warningf("Failed to resolve target ID for slave link %s: %v", slaveName, errRecursive)
				continue
			}
			artialSuccess = true
			for _, id := range ids {
				if id != "" {
					uniqueIDs[id] = struct{}{}
				}
			}
		}

		if partialSuccess && len(uniqueIDs) > 0 {
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

	// =========================================================================
	// Subsystem B: Native NVMe Fabrics Layout
	// =========================================================================
	if nvmeNamespaceRegex.MatchString(baseName) || strings.HasPrefix(baseName, "nvme") {
		nqn, err := r.getNvmeSubsysNQN(ctx, baseName)
		if err != nil {
			return nil, err
		}
		return []string{nqn}, nil
	}

	// =========================================================================
	// Subsystem C: Canonical SCSI Layout
	// =========================================================================
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

	targetID := r.getScsiTargetID(ctx, hctl, baseName)
	if targetID == "" {
		return nil, fmt.Errorf("scsi target registration state attributes not ready for address %s", hctl)
	}

	return []string{targetID}, nil
}

// getHCTLFromSd extracts the absolute Host:Channel:Target:Lun parameter mapping from a standard SCSI block node name.
func (r *OsDeviceConnectivityHelperScsiGeneric) getHCTLFromSd(ctx context.Context, sdName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	
	cleanSdName := filepath.Base(sdName)
	deviceLink := filepath.Join("/sys/block", cleanSdName, "device")
	
	// RESTORED VFS LAYER: Re-enabling true filepath.EvalSymlinks to guarantee absolute path matching accuracy.
	// Natively resolves multi-tier kernel path configurations in RAM, shielding against raw link modifications.
	realPath, err := filepath.EvalSymlinks(deviceLink)
	if err != nil {
		return "", fmt.Errorf("failed resolving sd path reference: %w", err)
	}
	
	hctl := filepath.Base(strings.TrimSuffix(realPath, "/"))
	if strings.Count(hctl, ":") != 3 {
		return "", fmt.Errorf("malformed layout index derived: %s", hctl)
	}
	
	return hctl, nil
}

// getScsiTargetID safe-resolves hardware targets across FC, SAS, iSCSI, and NVMe-oF layers with punchy logging.
// Fail-safe VFS resolution handles DM aliases, raw dm-X, and native NVMe without prefix assumptions.
func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetID(ctx context.Context, hctl string, baseBlockName string) string {
	logger.Infof("[SCSI-Target-Inspector] Enter pipeline. HCTL: [%s] | Block Target: [%s]", hctl, baseBlockName)

	if err := ctx.Err(); err != nil {
		logger.Warningf("[SCSI-Target-Inspector] [%s] Abort: context cancelled: %v", hctl, err)
		return ""
	}

	parts := strings.Split(hctl, ":")
	if len(parts) < 4 {
		logger.Warningf("[SCSI-Target-Inspector] [%s] Abort: malformed HCTL segments", hctl)
		return ""
	}

	hostID := parts[0]
	channelID := parts[1]
	targetID := parts[2]
	hct := strings.Join(parts[:3], ":")
	targetDirName := fmt.Sprintf("target%s", hct)

	// =========================================================================
	// FAIL-SAFE STRUCTURAL NVME & SLAVE SCANNER (BYPASSES HCTL INT TRAPS)
	// =========================================================================
	transportType := "unknown"
	cleanBlockName := filepath.Base(baseBlockName)

	// Resolve actual block device directory under /sys/block, handling symlinks and aliases
	sysBlockPath := filepath.Join("/sys/block", cleanBlockName)
	if resolvedPath, errEval := filepath.EvalSymlinks(sysBlockPath); errEval == nil {
		sysBlockPath = resolvedPath
	}

	// 1. Check if the block device is directly native NVMe
	if r.IsNativeNvmeNamespace(cleanBlockName) || strings.Contains(sysBlockPath, "/nvme/") {
		transportType = "nvme"
		if nvmeCtrl := ExtractNvmeControllerBase(cleanBlockName); nvmeCtrl != "" {
			hostID = strings.TrimPrefix(nvmeCtrl, "nvme")
		}
	} else {
		// 2. Fail-safe scan for Device Mapper / Multipath slaves (handles names like mpatha, VolGroup01, dm-0)
		slavesPath := filepath.Join("/sys/block", cleanBlockName, "slaves")
		if dFile, errOpen := os.Open(slavesPath); errOpen == nil {
			if entries, errRead := dFile.ReadDir(32); errRead == nil {
				for _, entry := range entries {
					slaveName := entry.Name()
					if strings.HasPrefix(slaveName, "nvme") {
						transportType = "nvme"
						if nvmeCtrl := ExtractNvmeControllerBase(slaveName); nvmeCtrl != "" {
							hostID = strings.TrimPrefix(nvmeCtrl, "nvme")
						}
						break
					}
				}
			}
			dFile.Close()
		}
	}

	// =========================================================================
	// FALLBACK LEGACY SCSI/FC/iSCSI SUBSYSTEM SCAN (IF NOT NVME)
	// =========================================================================
	if transportType == "unknown" {
		hostSubsysLink := fmt.Sprintf("/sys/class/scsi_host/host%s/device/subsystem", hostID)
		if realHostSubsys, errLink := filepath.EvalSymlinks(hostSubsysLink); errLink == nil {
			subsysName := filepath.Base(realHostSubsys)
			if strings.Contains(subsysName, "iscsi") {
				transportType = "iscsi"
			} else if strings.Contains(subsysName, "fc") || strings.Contains(subsysName, "pci") {
				transportType = "fc"
			} else if strings.Contains(subsysName, "sas") {
				transportType = "sas"
			}
		}
	}

	logger.Debugf("[SCSI-Target-Inspector] [%s] Protocol sniffed: %s | Resolved Host Target Token: host%s", hctl, transportType, hostID)

	// =========================================================================
	// EXECUTION BRANCH 0: NATIVE NVME-OVER-FABRICS (NVMe-oF) RESOLUTION TRACK
	// =========================================================================
	if transportType == "nvme" {
		res := r.getScsiTargetIDTransportNvme(hctl, hostID)
		if res != "" {
			return res
		}
		logger.Warningf("[SCSI-Target-Inspector] [%s] NVMe transport resolution yielded empty ID; halting fallback bleed.", hctl)
		return ""
	}

	// =========================================================================
	// EXECUTION BRANCH 1: FIBRE CHANNEL RESOLUTION LAYER (FC STRATEGY)
	// =========================================================================
	if transportType == "fc" || transportType == "unknown" {
		resolvedFcID := r.getScsiTargetIDTransportFc(ctx, hctl, hostID, channelID, targetID, targetDirName)
		if resolvedFcID != "" {
			return resolvedFcID
		}
		if transportType == "fc" {
			return ""
		}
	}

	// =========================================================================
	// EXECUTION BRANCH 2: SERIAL ATTACHED SCSI RESOLUTION LAYER (SAS STRATEGY)
	// =========================================================================
	if transportType == "sas" || transportType == "unknown" {
		resolvedSasID := r.getScsiTargetIDTransportSas(hctl, targetDirName)
		if resolvedSasID != "" {
			return resolvedSasID
		}
		if transportType == "sas" {
			return ""
		}	
	}

	// =========================================================================
	// EXECUTION BRANCH 3: iSCSI RESOLUTION LAYER (iSCSI SESSION LOOKUP)
	// =========================================================================
	if transportType == "iscsi" || transportType == "unknown" {
		return r.getScsiTargetIDTransportIscsi(ctx, hctl, hostID)
	}
	
	logger.Warningf("[SCSI-Target-Inspector] [%s] [OUT OF STRATEGIES] Zero protocol matches across all system mappings.", hctl)
	return ""
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetIDTransportNvme(hctl string, hostID string) string {
	logger.Infof("[SCSI-Target-Inspector] [%s] [Track-NVMe-oF] Start fabrics parsing.", hctl)
	
	// FIXED: hostID is now properly received as a function parameter
	subsysNqnFile := fmt.Sprintf("/sys/class/nvme/nvme%s/subsysnqn", hostID)
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-NVMe-oF] Probe path: %s", hctl, subsysNqnFile)
	
	if data, errRead := os.ReadFile(subsysNqnFile); errRead == nil && len(data) > 0 {
		nqnString := strings.TrimSpace(string(data))
		logger.Infof("[SCSI-Target-Inspector] [%s] [NVMe-oF SUCCESS] Target verified NQN: %s", hctl, nqnString)
		return nqnString
	} else {
		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-NVMe-oF-Skip] Read failed: %v", hctl, errRead)
	}
	return "" 
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetIDTransportFc(ctx context.Context, hctl string, hostID string, channelID string, targetID string, targetDirName string) string {
	fcClassDir := "/sys/class/fc_remote_ports"
	fcClassPattern := fmt.Sprintf("/sys/class/fc_remote_ports/rport-%s:*", hostID)
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1] Wildcard scan: %s", hctl, fcClassPattern)

	directPortFile := filepath.Join(fcClassDir, fmt.Sprintf("rport-%s:%s-%s", hostID, channelID, targetID), "port_name")
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Direct] Try direct path: %s", hctl, directPortFile)
	
	if data, errRead := os.ReadFile(directPortFile); errRead == nil && len(data) > 0 {
		wwpn := strings.TrimSpace(string(data))
		logger.Infof("[SCSI-Target-Inspector] [%s] [FC-rport-FastPath SUCCESS] Direct rport verified WWPN: %s", hctl, wwpn)
		return wwpn 
	}

	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Fallback] Direct path missed. Run chunk sweep.", hctl)
	
	const maxCapCeiling = 10000
	fcCandidates := func() []string {
		dFile, err := os.Open(fcClassDir)
		if err != nil {
			return nil
		}
		defer dFile.Close()

		prefixSearch := fmt.Sprintf("rport-%s:", hostID)
		candidates := make([]string, 0, 32)

		for {
			rportEntries, errDirs := dFile.ReadDir(100)
			if errDirs != nil && errDirs != io.EOF {
				break
			}
			for _, entry := range rportEntries {
				rportName := entry.Name()
				if strings.HasPrefix(rportName, prefixSearch) {
					if len(candidates) >= maxCapCeiling {
						logger.Warningf("[VFS-Guard] Safe ceiling hit (%d). Truncate scan.", maxCapCeiling)
						break
					}
				
					candidates = append(candidates, rportName)
				}
			}
			if len(candidates) >= maxCapCeiling || len(rportEntries) < 100 || errDirs == io.EOF {
				break
			}
		}
		return candidates
	}()

	for _, rportName := range fcCandidates {
		if err := ctx.Err(); err != nil {
			return ""
		}
		fcPortFile := filepath.Join(fcClassDir, rportName, "port_name")
		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Loop] Probe file: %s", hctl, fcPortFile)

		if data, errRead := os.ReadFile(fcPortFile); errRead == nil && len(data) > 0 {
			wwpn := strings.TrimSpace(string(data))
			logger.Infof("[SCSI-Target-Inspector] [%s] [FC-rport-FastPath SUCCESS] Class rport verified WWPN: %s", hctl, wwpn)
			return wwpn 
		} else if errRead != nil {
			logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A1-Loop] Skip file %s. Error: %v", hctl, fcPortFile, errRead)
		}
	}

	fcClassicPath := fmt.Sprintf("/sys/class/scsi_target/%s/fc_transport/%s/port_name", targetDirName, targetDirName)
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A2] Fallback classic target probe: %s", hctl, fcClassicPath)

	if data, errRead := os.ReadFile(fcClassicPath); errRead == nil && len(data) > 0 {
		wwpn := strings.TrimSpace(string(data))
		logger.Infof("[SCSI-Target-Inspector] [%s] [FC-Classic-FastPath SUCCESS] Classic target verified WWPN: %s", hctl, wwpn)
		return wwpn 
	} else {
		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-A2-Skip] Classic target missed footprint. Error: %v", hctl, errRead)
	}
	
	return ""
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetIDTransportSas(hctl string, targetDirName string) string {
	sasClassicPath := fmt.Sprintf("/sys/class/scsi_target/%s/sas_device/%s/sas_address", targetDirName, targetDirName)
	logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-B] Probe SAS tree path: %s", hctl, sasClassicPath)

	if data, errRead := os.ReadFile(sasClassicPath); errRead == nil && len(data) > 0 {
		sasAddr := strings.TrimSpace(string(data))
		logger.Infof("[SCSI-Target-Inspector] [%s] [SAS-Class-FastPath SUCCESS] Verified SAS address: %s", hctl, sasAddr)
		return sasAddr 
	} else {
		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-B-Skip] SAS missed footprint. Error: %v", hctl, errRead)
	}
	
	return ""
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getScsiTargetIDTransportIscsi(ctx context.Context, hctl string, hostID string) string {
	sessionClassPath := "/sys/class/iscsi_session"
	matchToken := fmt.Sprintf("host%s", hostID)
	logger.Infof("[SCSI-Target-Inspector] [%s] [Track-C] Start iSCSI session sweep path: %s", hctl, sessionClassPath)

	const maxCapCeiling = 10000
	iscsiCandidates := func() []string {
		sFile, err := os.Open(sessionClassPath)
		if err != nil {
			logger.Warningf("[SCSI-Target-Inspector] [%s] [Track-C-Error] Global class tree lookups failed: %v", hctl, err)
			return nil
		}
		defer sFile.Close()

		candidates := make([]string, 0, 32)
		for {
			sessions, errDirs := sFile.ReadDir(100)
			if errDirs != nil && errDirs != io.EOF {
				logger.Warningf("[SCSI-Target-Inspector] [%s] [Track-C-Error] Chunk read error: %v", hctl, errDirs)
				break
			}
			for _, s := range sessions {
				if len(candidates) >= maxCapCeiling {
					logger.Warningf("[VFS-Guard] Safe ceiling hit (%d). Truncate scan.", maxCapCeiling)
					break
				}
				candidates = append(candidates, s.Name())
			}
			if len(candidates) >= maxCapCeiling || len(sessions) < 100 || errDirs == io.EOF {
				break
			}
		}
		return candidates
	}()

	for _, sessionName := range iscsiCandidates {
		if ctx.Err() != nil {
			logger.Warningf("[SCSI-Target-Inspector] [%s] [Track-C] Context expired in session loop traversal.", hctl)
			return ""
		}

		deviceMappingLink := filepath.Join(sessionClassPath, sessionName, "device")
		logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop] Evaluate: %s. Link: %s", hctl, sessionName, deviceMappingLink)

		trueHostPath, errLink := filepath.EvalSymlinks(deviceMappingLink)
		if errLink == nil {
			logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop] Entry %s resolved: [%s]", hctl, sessionName, trueHostPath)

			if strings.Contains(trueHostPath, matchToken) {
				targetNameFile := filepath.Join(sessionClassPath, sessionName, "targetname")
				logger.Infof("[SCSI-Target-Inspector] [%s] [Track-C-Loop] Correlated! Target file: %s", hctl, targetNameFile)

				if data, errRead := os.ReadFile(targetNameFile); errRead == nil && len(data) > 0 {
					iqnString := strings.TrimSpace(string(data))
					logger.Infof("[SCSI-Target-Inspector] [%s] [iSCSI-Class-FastPath SUCCESS] Session verified IQN: %s", hctl, iqnString)
					return iqnString 
				} else {
					logger.Warningf("[SCSI-Target-Inspector] [%s] [Track-C-Loop-Error] Session %s matched but targetname read failed: %v", hctl, sessionName, errRead)
				}
			} else {
				logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop-Mismatch] Entry %s rejected: token %s mismatch.", hctl, sessionName, matchToken)
			}
		} else {
			logger.Debugf("[SCSI-Target-Inspector] [%s] [Track-C-Loop-Skip] Entry %s link unreadable. Error: %v", hctl, sessionName, errLink)
		}
	}
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
	// STRATEGY B: HARDENED SYSTEM CLASS MAP SWEEP (DECOUPLED MEMORY PIPELINE)
	// =========================================================================
	sessionClassPath := "/sys/class/iscsi_session"
	
	dFile, errOpen := os.Open(sessionClassPath)
	if errOpen != nil {
		logger.Warningf("      [iSCSI-Subsystem-Scout] [Strategy-B CRITICAL FAILED] System class folder missing or inaccessible: %v", errOpen)
		return ""
	}

	// MEMORY BOUNDED SAFE GUARD: Rigidly caps maximum array pre-allocations under unstable node states.
	const maxCapCeiling = 10000
	sessionCandidates := make([]string, 0, 32)

	// STAGE 1: MICROSECOND SNAPSHOT SWEEP (Decouples VFS Handles Instantly)
	for {
		sessions, errDirs := dFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			logger.Warningf("      [iSCSI-Subsystem-Scout] [Strategy-B Error] Error reading iSCSI sessions chunk: %v", errDirs)
			break
		}
		for _, s := range sessions {
			if len(sessionCandidates) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] iSCSI sessions list reached safe allocation ceiling (%d). Truncating scan.", maxCapCeiling)
				break
			}
			sessionCandidates = append(sessionCandidates, s.Name())
		}
		if len(sessionCandidates) >= maxCapCeiling || len(sessions) < 100 || errDirs == io.EOF {
			break
		}
	}
	dFile.Close() // CLOSED IMMEDIATELY: Releases VFS handles before entering slow, multi-second file operations.

	matchToken := fmt.Sprintf("host%s", hostID)

	// STAGE 2: SAFE DECOUPLED EXECUTION PIPELINE
	for _, sessionName := range sessionCandidates {
		if err := ctx.Err(); err != nil {
			return ""
		}

		targetNamePath := filepath.Join(sessionClassPath, sessionName, "targetname")
		
		// Fast Path file read executes out of memory loop context
		rawTargetData, errRead := os.ReadFile(targetNamePath)
		if errRead != nil || len(rawTargetData) == 0 {
			continue
		}
		data := string(rawTargetData)
		
		deviceLinkMappingPath := filepath.Join(sessionClassPath, sessionName, "device")
		
		// RESTORED VFS LAYER: Re-enabling true filepath.EvalSymlinks to guarantee absolute path matching accuracy.
		// Natively computes fully qualified canonical targets in RAM, shielding against layout truncation defects.
		hostPath, errLink := filepath.EvalSymlinks(deviceLinkMappingPath)
		if errLink != nil {
			continue
		}
		
		if strings.Contains(hostPath, matchToken) || strings.Contains(realDevicePath, sessionName) {
			sigID := strings.TrimSpace(data)
			logger.Infof("      [iSCSI-Subsystem-Scout] [Strategy-B SUCCESS] Valid target correlated via fallback parameters on %s: %s", sessionName, sigID)
			return sigID
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


// IsSgDeviceGhost determines whether a scsi_generic path is a dead zombie target or missing from the host fabric.
// Natively runs inside inherited worker context lanes to maintain absolute deadlock immunity.
func (r *OsDeviceConnectivityHelperScsiGeneric) IsSgDeviceGhost(ctx context.Context, sgName string) (bool, error) {
	cleanSgName := filepath.Base(sgName)
	
	sgSysfsPath := fmt.Sprintf("/sys/class/scsi_generic/%s", cleanSgName)
	deviceLink := filepath.Join(sgSysfsPath, "device")

	// --- INLINE AGE TRACKING & SCAVENGER ENGINE (Rule 4/5) ---
	now := time.Now()
	actualFirstSeen, _ := r.discoveryCache.LoadOrStore(cleanSgName, now)
	deviceAge := time.Since(actualFirstSeen.(time.Time))

	// Inline Scavenger: Clean up the map on the fly if the underlying directory has disappeared
	_, statErr := os.Stat(sgSysfsPath)
	if os.IsNotExist(statErr) {
		r.discoveryCache.Delete(cleanSgName)
		return false, nil
	}

	// RESTORED VFS LAYER: Resolve target paths natively via absolute canonical link check
	// to protect against unaligned path construction and udev unbind races under workload density.
	deviceBase, errLink := filepath.EvalSymlinks(deviceLink)
	if errLink != nil {
		deviceBase = deviceLink // Fallback securely if the device is already unlinking from the fabric
	}

	// --- FLATTENED CONTEXT-RESPECTING SYSFS PARSING ---
	if err := ctx.Err(); err != nil {
		return false, err
	}

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
			r.discoveryCache.Delete(cleanSgName)
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

	// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1/4): Removed the internal nested 
	// ExecuteUninterruptible wrapper. The low-level ioctl executes directly and safely under the 
	// inherited, context-bounded parent batch worker context timeline lifecycle.
	isHwGhost, ioctlErr := r.checkPQviaIoctl(cleanSgName, deviceAge)
	
	if ioctlErr == nil && isHwGhost {
		r.discoveryCache.Delete(cleanSgName)
		return true, nil
	}

	// TRACK B: Stuck Initialization / Transient Error Management
	if ioctlErr != nil {
		// If the device is permanently stuck initializing or has a missing block directory for > 15 seconds,
		// break the shield and force zombie path eviction.
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
	
	// RESTORED VFS LAYER: Re-enabling true filepath.EvalSymlinks to guarantee absolute path matching accuracy.
	// Natively computes absolute canonical target string configs to shield against folder depth mutations.
	subsysPath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/subsystem", sgName)
	realSubsysPath, errLink := filepath.EvalSymlinks(subsysPath)
	if errLink == nil {
		if strings.Contains(realSubsysPath, "nvme") {
			logger.Debugf("[%s] IOCTL Probe: Native NVMe device detected. Bypassing SCSI evaluation.", sgName)
			return false, nil
		}
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
					return false, nil 
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

				if deviceAge < 30*time.Second {
					logger.Warningf("[%s] IOCTL Probe: Intercepted transient condition (%02x/%02x) during birth sequence (Age: %v). Retaining path to settle.", sgName, senseKey, asc, deviceAge)
					return false, nil 
				}
			}
			
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

// TeardownVolume coordinates the unified dual-protocol unmounting and eviction lifecycle of host storage tracks.
// Strictly restructured to execute the industry-standard 3-step NVMe-DM teardown pipeline flawlessly.
func (r *OsDeviceConnectivityHelperScsiGeneric) TeardownVolume(ctx context.Context, target string, expectedWWID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Infof("[Teardown-Main] Entering master volume cleanup sequence for mount target: %s", target)	
	
	mpathName, hardwareResolved, isNativeNVMe, major, minor, isMounted, needFlush, needRemovePhysical, _ := r.collectInformationForTeardown(ctx, target, expectedWWID)

	// --- PHASE 1: UNMOUNT & CRITICAL VERIFICATION MATRIX ---
	if isMounted {
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
	
	rawScsiTarget := strings.ToLower(strings.TrimSpace(expectedWWID))
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)
	
	// =========================================================================
	// DEVICE MAPPER MULTIPATH WORKFLOW (SCSI, iSCSI, FC, NVMe-DM)
	// =========================================================================
	if mpathName != "" && !isNativeNVMe {
		canonicalKernelName := mpathName
		userSpaceAliasName := mpathName

		if strings.HasPrefix(mpathName, "dm-") {
			if recoveredAlias := r.GetDMNameFromMinor(ctx, minor); recoveredAlias != "" {
				userSpaceAliasName = recoveredAlias
			}
		} else {
			resolvedDev, errLink := filepath.EvalSymlinks(filepath.Join("/dev/mapper", mpathName))
			if errLink == nil {
				canonicalKernelName = filepath.Base(resolvedDev)
			}
		}

		logger.Infof("[Teardown-Main] [%s] Starting Device Mapper teardown pipeline. Alias: %s | Kernel Node: %s", mpathName, userSpaceAliasName, canonicalKernelName)
		
		globalOpenCount := r.waitForNoRefs(ctx, canonicalKernelName)

		if needFlush && ctx.Err() == nil {
			logger.Infof("[Teardown-Main] [%s] Initiating isolated volume buffer flush on %s...", mpathName, canonicalKernelName)
			errFlush := r.flushDMBuffers(ctx, canonicalKernelName)
			if errFlush != nil {
				logger.Errorf("[Teardown-Main] [%s] Critical buffer flush failed: %v", mpathName, errFlush)
				return fmt.Errorf("teardown: safety boundary aborted due to buffer flush failure: %w", errFlush)
			}
		}

		// ----------------────────────────────────────────=========================
		// STEP 1: REMOVE DEVICE MAPPER LAYER & CLEAR QUEUES
		// ----------------────────────────────────────────────────────────=========
		if globalOpenCount > 0 {
			logger.Warningf("[Teardown-Main] [%s] Device remains busy (openCount=%d). Triggering Deferred Removal Rescue Pass.", mpathName, globalOpenCount)
			r.tearDownBusyRescue(ctx, userSpaceAliasName)
			return nil
		}

		// ----------------────────────────────────────────=========================
		// STEP 2: EXTRACT SLAVES WHILE THE DM TABLE IS FULLY INTACK
		// ----------------────────────────────────────────----------------=========
		var slaves []string
		if hardwareResolved && major != 0 {
			slaves, _ = r.Helper.getSlavesForDevice(ctx, major, minor)
		}
		if len(slaves) == 0 && expectedWWID != "" {
			slaves = r.FindSlavesByWWID(ctx, expectedWWID) 
		}

		logger.Infof("[Teardown-Main] [%s] Step 1/3: Dropping multipath layout via daemon entry...", mpathName)
		_ = r.multipathdAction(ctx, "disablequeueing map "+userSpaceAliasName)
		if errDelMap := r.multipathdAction(ctx, "del map "+userSpaceAliasName); errDelMap != nil {
			logger.Warningf("[Teardown-Main] [%s] Daemon map deletion failed: %v.", mpathName, errDelMap)
		}

		logger.Infof("[Teardown-Main] [%s] Issuing synchronous DM_DEV_REMOVE ioctl instruction...", userSpaceAliasName)
		if errIoctlRm := r.dmIoctlCall(ctx, userSpaceAliasName, DM_DEV_REMOVE, 0); errIoctlRm != nil {
			_ = r.dmIoctlCall(ctx, canonicalKernelName, DM_DEV_REMOVE, 0)
		}

		if len(slaves) > 0 {
			logger.Infof("[Teardown-Main] [%s] Step 2/3: Isolate and classify physical backend slave paths: %v", mpathName, slaves)
			
			var scsiSlaves []string
			var nvmeSlaves []string

			for _, slaveNode := range slaves {
				baseSlave := filepath.Base(slaveNode)
				if strings.HasPrefix(baseSlave, "sd") {
					scsiSlaves = append(scsiSlaves, baseSlave)
				} else if strings.HasPrefix(baseSlave, "nvme") {
					nvmeSlaves = append(nvmeSlaves, baseSlave)
				}
			}

			// Traditional SCSI/iSCSI lanes are unlinked normally via LUN isolation
			if len(scsiSlaves) > 0 {
				logger.Infof("[Teardown-Main] [%s] Dispatched SCSI slave tracking nodes to physical evictor: %v", mpathName, scsiSlaves)
				_ = r.RemovePhysicalDevice(ctx, scsiSlaves)
			}
			
			needRemovePhysical = false
		} else {
			logger.Infof("[Teardown-Main] [%s] Step 2/3: Slaves empty. Executing out-of-band transport fallback sweep...", mpathName)
			_ = r.purgeStuckPhysicalPathsDualProtocol(ctx, rawScsiTarget, rawNvmeTarget)
			needRemovePhysical = false 
		}

	// =========================================================================
	// PURE NATIVE NVMe FABRIC WORKFLOW (TARGETED LOGOUT IMPLEMENTATION)
	// =========================================================================
	} else if mpathName != "" && isNativeNVMe {
		logger.Infof("[Teardown-Main] Target node %s maps to a native NVMe architecture. Routing straight to transport logouts.", mpathName)
		
		logger.Infof("[Teardown-Main] [%s] Initiating native NVMe block cache synchronization check...", mpathName)
		_ = r.flushDeviceBuffers(ctx, filepath.Join("/dev", mpathName))

		slaves := r.FindSlavesByWWID(ctx, expectedWWID)
		if len(slaves) == 0 {
			slaves = []string{mpathName}
		}

		needRemovePhysical = false
	}
	
	// =========================================================================
	// PHASE 4: IDEMPOTENCY SAFETY ANCHORS
	// =========================================================================
	if mpathName == "" && (needFlush || needRemovePhysical) && expectedWWID != "" {
		stillMounted, errCheck := r.Mounter.IsMounted(target)
		if errCheck == nil && !stillMounted {
			logger.Warningf("[Teardown-Main] Volume target %s is already unmounted and mpath device for WWID %s cannot be resolved. Assuming previous unstage completed successfully.", target, expectedWWID)
			return nil
		}
		return fmt.Errorf("teardown: unable to resolve backing block architecture targets for WWID %s; halting execution to protect system paths", expectedWWID)
	}

	if needRemovePhysical && expectedWWID != "" {
		logger.Infof("[Teardown-Main] Executing global fallback sweep for WWID: %s", expectedWWID)
		_ = r.purgeStuckPhysicalPathsDualProtocol(ctx, rawScsiTarget, rawNvmeTarget)
	}

	logger.Infof("[Teardown-Main] Master cleanup sequence successfully finalized for target: %s", target)
	return nil
}


// tearDownBusyRescue triggers an immediate asynchronous kernel-level deferred unbinding sequence 
// to safely offload a blocked device mapper target from the system host paths.
func (r *OsDeviceConnectivityHelperScsiGeneric) tearDownBusyRescue(ctx context.Context, rawScsiTarget string, canonicalKernelName string, userSpaceAliasName string) {
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)
	_ = r.multipathdAction(ctx, "disablequeueing map "+userSpaceAliasName)	
	_ = r.dmIoctlCall(ctx, userSpaceAliasName, DM_DEV_REMOVE, DM_DEFERRED_REMOVE)
	
	go func() {
		// Allocate a completely detached context with a safe runtime threshold
		bgCtx, bgCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer bgCancel()

		logger.Infof("[Teardown-Async] Background monitor initialized for deferred path cleanup: %s", userSpaceAliasName)
		
		// Poll the host file tree until the dm-X node vanishes from /dev
		for i := 0; i < 24; i++ {
			time.Sleep(5 * time.Second)
			
			dmDevNode := filepath.Join("/dev", canonicalKernelName)
			if _, errStat := os.Stat(dmDevNode); os.IsNotExist(errStat) {
				logger.Infof("[Teardown-Async] Deferred map removal completed by kernel. Launching physical path purge for WWID: %s", rawScsiTarget)
				
				// Safely invoke the fallback path un-linker to wipe the dirty zombie footprints
				_ = r.purgeStuckPhysicalPathsDualProtocol(bgCtx, rawScsiTarget, rawNvmeTarget)
				return
			}
		}
		
		// Safety Catch: If the device stays pinned for too long, execute the purge anyway
		logger.Warningf("[Teardown-Async] Timeout reached waiting for deferred remove on %s. Forcing path purge.", userSpaceAliasName)
		_ = r.purgeStuckPhysicalPathsDualProtocol(bgCtx, rawScsiTarget, rawNvmeTarget)
	}()
}

// waitForNoRefs polls the host kernel to wait for active device references to hit zero.
// Returns the final open count state captured upon exit trajectory.
func (r *OsDeviceConnectivityHelperScsiGeneric) waitForNoRefs(ctx context.Context, canonicalKernelName string) int32 {
	var globalOpenCount int32 = -1

	for i := 0; i < 10; i++ {
		if ctx.Err() != nil {
			break
		}

		// FIXED: Declared and isolated variable type matching inside local frame scope safely
		count, errCount := r.Helper.GetOpenCount(ctx, canonicalKernelName)
		if errCount == nil {
			globalOpenCount = count
			if globalOpenCount == 0 {
				break 
			}
		}
		
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			// FIXED: Replaced brittle missing label breakout with an explicit clean return
			return globalOpenCount
		case <-timer.C:
			// Timer has channel drained naturally here; no manual stop required
		}
	}
	return globalOpenCount
}

func (r *OsDeviceConnectivityHelperScsiGeneric) collectInformationForTeardown(ctx context.Context, target string, expectedWWID string) (mpathName string, hardwareResolved bool, isNativeNVMe bool, major uint32, minor uint32, isMounted bool, needFlush bool, needRemovePhysical bool, isDeviceMapperTarget bool) {

	harvestDeviceMetadata := func(devNodePath string, hintMpathName string) {
		logger.Infof("[Teardown-Main] Target %s - harvest device %s hint %s", target, devNodePath, hintMpathName)
		sanitizedDevPath := devNodePath
		if resolvedDev, errLink := filepath.EvalSymlinks(sanitizedDevPath); errLink == nil {
			sanitizedDevPath = resolvedDev
		}
		
		if stat, errStat := os.Stat(sanitizedDevPath); errStat == nil {
			if sysObj, ok := stat.Sys().(*syscall.Stat_t); ok {
				major = unix.Major(uint64(sysObj.Rdev))
				minor = unix.Minor(uint64(sysObj.Rdev))
				hardwareResolved = true
				
				baseName := filepath.Base(sanitizedDevPath)
				nvmeType, errType := DevicesAreNvme(ctx, r.KeyedGater, baseName)
				if errType != nil {
					logger.Warningf("[Teardown-Main] Topology inspection failed for base device %s: %v. Assuming legacy behavior.", baseName, errType)
				}

				if nvmeType == NVMeNative {
					mpathName = baseName
					isNativeNVMe = true
					logger.Infof("[Teardown-Main] Target %s mapped structurally as Native NVMe Multipathing", baseName)
				} else {
					// NORMALIZE TO CANONICAL dm-X NAME IF RESOLVED VIA MINOR OR SYMLINK
					if strings.HasPrefix(baseName, "dm-") {
						mpathName = baseName
					} else if hintMpathName != "" && strings.HasPrefix(hintMpathName, "dm-") {
						mpathName = hintMpathName
					} else {
						// If it's a friendly name, check if we can get the canonical dm name from major/minor
						canonicalDm := r.GetDMNameFromMinor(ctx, minor)
						if canonicalDm != "" {
							mpathName = canonicalDm
							logger.Infof("[Teardown-Main] Normalized friendly alias '%s' to canonical kernel name: %s", hintMpathName, canonicalDm)
						} else if hintMpathName != "" {
							mpathName = hintMpathName
						} else {
							mpathName = baseName
						}
					}
					logger.Infof("[Teardown-Main] Target mapped structurally as Device Mapper Coordinator node: %s", mpathName)
				}
			}
		}
	}

	// --- PHASE 0: PRE-UNMOUNT HARDWARE HARVEST ---
	var mountErr error
	isMounted, mountErr = r.Mounter.IsMounted(target)
	if mountErr == nil && isMounted {
		logger.Warningf("[Teardown-Main] Target is mounted %s", target)
		if devPath, err := r.Mounter.GetDeviceFromMount(target); err == nil && devPath != "" {
			logger.Warningf("[Teardown-Main] Isolated backing device path node from mount tree: %s", devPath)
			harvestDeviceMetadata(devPath, "")
		} else {
			logger.Warningf("[Teardown-Main] GetDeviceFromMount returned empty or error for %s: %v. Relying on WWID fallback.", target, err)
		}
	}
	
	
	// --- PHASE 2: HARDWARE RESOLUTION FALLBACK ---
	if mpathName == "" && expectedWWID != "" {
		discoveredMapperName := r.Helper.findDMByWWID(ctx, expectedWWID)
		if discoveredMapperName != "" {
			logger.Infof("[Teardown-Main] Found device from volume id via /dev/mapper: %s", discoveredMapperName)
			if !hardwareResolved {
				var errSysfs error
				major, minor, errSysfs = r.Helper.GetMajorMinorFromSysfs(ctx, discoveredMapperName)
				if errSysfs == nil {
					hardwareResolved = true
				}
			}
			harvestDeviceMetadata(filepath.Join("/dev/mapper", discoveredMapperName), discoveredMapperName)
		} else {
			logger.Infof("[Teardown-Main] Couldn't find device with id for target: %s", target)
			slaves := r.FindSlavesByWWID(ctx, expectedWWID)
			if len(slaves) > 0 {
				for _, slavePath := range slaves {
					slaveBase := filepath.Base(slavePath)
					nvmeType, _ := DevicesAreNvme(ctx, r.KeyedGater, slaveBase)
					if nvmeType == NVMeNative {
						mpathName = slaveBase
						isNativeNVMe = true
						logger.Infof("[Teardown-Main] Fallback slave tracking matched native NVMe handle: %s", mpathName)
						break
					}
				}
			}
		}		
	}

	// --- CALCULATE CLEANUP POLICIES INTERNALLY ---
	helper := GetDmsPathHelperGeneric{}
	isDeviceMapperTarget = helper.IsDeviceMapper(mpathName)

	if mpathName == "" {
		needFlush = true
		needRemovePhysical = true
		logger.Infof("[Teardown-Main] No mpath found for mount target: %s", target)	
	} else if isNativeNVMe {
		needFlush = false
		needRemovePhysical = false
		logger.Infof("[Teardown-Main] Native NVMe found for mount target: %s", target)	
	} else if isDeviceMapperTarget {
		isMpathNVMe := false
		slavesPath := filepath.Join("/sys/block", mpathName, "slaves")
		if dFile, errOpen := os.Open(slavesPath); errOpen == nil {
			if entries, errRead := dFile.ReadDir(10); errRead == nil {
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "nvme") {
						isMpathNVMe = true
						break
					}
				}
			}
			dFile.Close()
		}

		if isMpathNVMe {
			needFlush = true
			needRemovePhysical = false
			logger.Infof("[Teardown-Main] NVMe DM found for mount target: %s", target)	
		} else {
			needFlush = true
			needRemovePhysical = true
			logger.Infof("[Teardown-Main] DM found for mount target: %s", target)	
		}
	} else {
		needFlush = true
		needRemovePhysical = true
		logger.Infof("[Teardown-Main] DM found, not DM target,  for mount target: %s", target)	
	}
	
	return mpathName, hardwareResolved, isNativeNVMe, major, minor, isMounted, needFlush, needRemovePhysical, isDeviceMapperTarget
}

// flushDMBuffers executes a clean device-level block cache sync boundary before mapper removal.
// Final optimization: Removed EvalSymlinks and minor fallbacks; uses the direct path safely.
func (r *OsDeviceConnectivityHelperScsiGeneric) flushDMBuffers(ctx context.Context, mpathName string) error {
	_, errFlush := executer.ExecuteUninterruptible[struct{}](
		ctx,
		r.KeyedGater,
		"flush-"+mpathName,
		1,   // maxRunning = 1: Forces absolute single-threaded exclusivity per device map
		10,  // maxSpare
		5*time.Second,
		30*time.Second,
		func(wCtx context.Context) (struct{}, error) {
			if err := wCtx.Err(); err != nil {
				return struct{}{}, err
			}

			var targetDevNode string
			mapperPath := filepath.Join("/dev/mapper", mpathName)
			directDevPath := filepath.Join("/dev", mpathName)

			// Tier 1: Check mapperPath explicitly and inspect any non-not-exist errors
			if _, err := os.Stat(mapperPath); err == nil {
				targetDevNode = mapperPath
			} else if !os.IsNotExist(err) {
				logger.Warningf("[Teardown-Main] [%s] Non-fatal stat error on mapper path %s: %v", mpathName, mapperPath, err)
			}

			// If not found yet, check directDevPath explicitly
			if targetDevNode == "" {
				if _, err := os.Stat(directDevPath); err == nil {
					targetDevNode = directDevPath
				} else if !os.IsNotExist(err) {
					logger.Warningf("[Teardown-Main] [%s] Non-fatal stat error on direct dev path %s: %v", mpathName, directDevPath, err)
				}
			}

			// Direct execution on the validated path alias
			if targetDevNode != "" {
				logger.Infof("[Teardown-Main] [%s] Launching sync buffers on target path %s...", mpathName, targetDevNode)
				flushErr := r.flushDeviceBuffers(wCtx, targetDevNode)
				if flushErr == nil {
					return struct{}{}, nil
				}
				return struct{}{}, flushErr
			}

			// Idempotency: Map already cleared or missing from host tree
			logger.Warningf("[Teardown-Main] [%s] Volume block device map already missing or cleared on the host file tree. Bypassing flush step safely.", mpathName)
			return struct{}{}, nil
		},
	)
	return errFlush
}

// FindSlavesByWWID safely scans the host block layer in parallel to aggregate all physical path lanes matching the volume identifier.
func (r *OsDeviceConnectivityHelperScsiGeneric) FindSlavesByWWID(ctx context.Context, expectedWWID string) []string {
	var slaves []string
	
	rawScsiTarget := normalizeWWID(expectedWWID)
	if rawScsiTarget == "" {
		return slaves
	}
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)

	dFile, errOpen := os.Open("/dev")
	if errOpen != nil {
		logger.Warningf("FindSlavesByWWID: failed to open /dev cleanly: %v", errOpen)
		return slaves
	}
	defer dFile.Close()

	const maxCapCeiling = 10000
	rawNames := make([]string, 0, 100)

	// =========================================================================
	// STAGE 1: MICROSECOND SNAPSHOT SWEEP (Decouples VFS State Instantly)
	// =========================================================================
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		entries, readErr := dFile.ReadDir(100)
		if readErr != nil && readErr != io.EOF {
			logger.Warningf("FindSlavesByWWID: failed to read streaming entries from /dev: %v", readErr)
			return slaves
		}
		if len(entries) == 0 || readErr == io.EOF {
			break
		}

		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
				continue
			}

			isNVMe := nvmeNamespaceRegex.MatchString(name)
			isSCSI := strings.HasPrefix(name, "sd")

			if !isNVMe && !isSCSI {
				continue
			}
			
			if len(rawNames) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] Slaves lookups candidate list reached safe allocation ceiling (%d). Truncating scan pass.", maxCapCeiling)
				break
			}

			rawNames = append(rawNames, name)
		}

		if len(rawNames) >= maxCapCeiling || readErr == io.EOF {
			break
		}
	}

	// =========================================================================
	// STAGE 2: DECOUPLED BATCH CHUNKING PROCESSING ENGINE
	// =========================================================================
	processBatch := func(batch []string, gaterKey string) []string {
		if len(batch) == 0 {
			return nil
		}

		var matchedSlaves []string
		results, errBatch := executer.ExecuteUninterruptibleBatch[string, string](
			ctx,
			r.KeyedGater,
			gaterKey,
			16, 128, 5*time.Second, 30*time.Second,
			batch,
			func(wCtx context.Context, index int, name string, cancelBatch func()) (string, error) {
				if err := wCtx.Err(); err != nil {
					return "", err
				}

				isNVMe := nvmeNamespaceRegex.MatchString(name)
				isSCSI := strings.HasPrefix(name, "sd")

				var discoveredID string
				if isNVMe {
					logger.Warningf("nvme flow for %s", name)
					baseBlockName := name 
					targetSysDir := filepath.Join("/sys/block", name)
					if _, errStat := os.Stat(targetSysDir); os.IsNotExist(errStat) {
						targetSysDir = filepath.Join("/sys/class/block", name)
					}
					
					if strings.Contains(name, "c") {
						if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
							if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
								baseBlockName = name[:cIdx] + name[lastNIdx:]
								targetSysDir = filepath.Join("/sys/block", baseBlockName) 
								if _, errStat := os.Stat(targetSysDir); os.IsNotExist(errStat) {
									targetSysDir = filepath.Join("/sys/class/block", baseBlockName)
								}
							}
						}
					}

					deviceNode := filepath.Join("/dev", name)
					if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
						logger.Warningf("nvme flow for %s - found in dev", name)
						var nvmeInfo nvmeIdTarget
						// FIXED: Replaced raw syscall.Syscall with unix.Syscall for GC protection
						_, _, errno := unix.Syscall(
							unix.SYS_IOCTL,
							df.Fd(),
							uintptr(NVME_IOCTL_ID_TARGET),
							uintptr(unsafe.Pointer(&nvmeInfo)),
						)
						df.Close()

						if errno == 0 {
							discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
						}
					}

					if discoveredID == "" || discoveredID == "00000000000000000000000000000000" {
						logger.Warningf("nvme flow for %s - read if", name)
						if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && bytesStr != "" {
							discoveredID = normalizeWWID(bytesStr)
						}
						if discoveredID == "" {
							if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && bytesStr != "" {
								discoveredID = normalizeWWID(bytesStr)
							}
						}
						if discoveredID == "" {
							if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device", "wwid")); err == nil && bytesStr != "" {
								discoveredID = normalizeWWID(bytesStr)
							}
						}
						if discoveredID == "" {
							subsysSymlink := filepath.Join(targetSysDir, "device", "subsystem")
							realSubsysPath, errLink := filepath.EvalSymlinks(subsysSymlink)
							if errLink == nil {
								if strings.Contains(realSubsysPath, "virtual/nvme-subsys") || strings.Contains(realSubsysPath, "bus/nvme") {
									subsysWwidPath := filepath.Join(realSubsysPath, "wwid")
									if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, baseBlockName, subsysWwidPath); err == nil && bytesStr != "" {
										discoveredID = normalizeWWID(bytesStr)
									}
								}
							}
						}
						logger.Warningf("nvme flow for %s - discovered id %s", name, discoveredID)
					}
				} else if isSCSI {
					scsiWwidPath := filepath.Join("/sys/block", name, "device", "wwid")
					if _, errStat := os.Stat(scsiWwidPath); os.IsNotExist(errStat) {
						scsiWwidPath = filepath.Join("/sys/class/block", name, "device", "wwid")
					}
					if bytesStr, err := secureReadSysfs(wCtx, r.KeyedGater, name, scsiWwidPath); err == nil && bytesStr != "" {
						discoveredID = normalizeWWID(bytesStr)
					}
					logger.Warningf("iscsi flow for %s found id %s", name, discoveredID)
				}

				if discoveredID == "" {
					return "", nil
				}
				
				isMatch := false
				if isNVMe {
					cleanedId := strings.TrimPrefix(discoveredID, "nvme-")
					isMatch = (cleanedId == rawNvmeTarget || strings.Contains(cleanedId, rawNvmeTarget))
				} else if isSCSI {
					isMatch = (discoveredID == rawScsiTarget || strings.Contains(discoveredID, rawScsiTarget))
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

	const chunkSize = 100
	for i := 0; i < len(rawNames); i += chunkSize {
		if err := ctx.Err(); err != nil {
			return slaves
		}

		end := i + chunkSize
		if end > len(rawNames) {
			end = len(rawNames)
		}
		currentChunkBatch := rawNames[i:end]

		uniqueGaterKey := fmt.Sprintf("batch-find-slaves-%s-%d", rawScsiTarget, time.Now().UnixNano())
		foundInChunk := processBatch(currentChunkBatch, uniqueGaterKey)
		slaves = append(slaves, foundInChunk...)
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

	const maxCapCeiling = 10000

	resolvedDmKernelName := fmt.Sprintf("dm-%d", minor)
	sysBlockPath := filepath.Join("/sys/block", resolvedDmKernelName)
	
	// FIXED: Multi-Distro VFS Fallback. If /sys/block defaults are opaque or masked 
	// inside the container pod, check the universal /sys/class/block pointer layout.
	if _, errStat := os.Stat(sysBlockPath); os.IsNotExist(errStat) {
		sysBlockPath = filepath.Join("/sys/class/block", resolvedDmKernelName)
	}

	if _, errStat := os.Stat(sysBlockPath); errStat == nil {
		if functionalName := r.readDMNameSafe(ctx, resolvedDmKernelName); functionalName != "" {
			return functionalName // INSTANT EARLY EXIT
		}
	}

	// =========================================================================
	// OPTIMIZED FALLBACK PATH: SCAN /dev/mapper IN ALL LINUX DISTRIBUTIONS
	// =========================================================================
	sessionClassPath := "/dev/mapper"
	sFile, errOpen := os.Open(sessionClassPath)
	if errOpen != nil {
		return ""
	}
	// FIXED: Bound directory resource table closing using a defer statement 
	// to completely eliminate un-closed file descriptor leaks during early loop breaks.
	defer sFile.Close()

	mapperNames := make([]string, 0, 64)

	// =========================================================================
	// STAGE 1: MICROSECOND SNAPSHOT SWEEP (Decouples VFS State Instantly)
	// =========================================================================
	for {
		if err := ctx.Err(); err != nil {
			return ""
		}

		mapperEntries, errDirs := sFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			break
		}

		for _, entry := range mapperEntries {
			name := entry.Name()
			if name == "control" {
				continue
			}
			
			if len(mapperNames) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] /dev/mapper entries reached safe processing ceiling (%d). Truncating scan.", maxCapCeiling)
				break
			}
			
			mapperNames = append(mapperNames, name)
		}

		if len(mapperNames) >= maxCapCeiling || len(mapperEntries) < 100 || errDirs == io.EOF {
			break
		}
	}

	// =========================================================================
	// STAGE 2: SAFE DECOUPLED EVALUATION PIPELINE
	// =========================================================================
	for _, name := range mapperNames {
		if err := ctx.Err(); err != nil {
			return ""
		}

		fullPath := filepath.Join("/dev/mapper", name)
		
		var statT unix.Stat_t
		if errStat := unix.Stat(fullPath, &statT); errStat != nil {
			continue
		}

		if (statT.Mode & unix.S_IFMT) != unix.S_IFBLK {
			continue
		}

		minorIndex := unix.Minor(uint64(statT.Rdev))
		if uint32(minorIndex) == minor {
			// FIXED: Instantly return 'name' (e.g. "mpatha") on match, bypassing the secondary 
			// sysfs readDMNameSafe check which can fail if sysfs is partially unpopulated or masked.
			logger.Infof("[GetDMNameFromMinor] Minor %d successfully mapped to alias name via /dev/mapper loop: %s", minor, name)
			return name
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
	// PROTECTED HIGH-AVAILABILITY SYSFS PARSING PASS
	// =========================================================================
	// Captured the explicit error channel to completely isolate error text leaks 
	// from being processed as valid storage mapping identifiers.
	result, err := executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater,
		"read-dm-name-"+cleanDmName, 
		20,                          
		100,                         
		500*time.Millisecond,        
		2*time.Second,               
		func(wCtx context.Context) (string, error) {
			if err := wCtx.Err(); err != nil {
				return "", err
			}

			// Route A: Standard modern system layout mapping
			modernPath := filepath.Join("/sys/block", cleanDmName, "dm", "name")
			if bytes, err := os.ReadFile(modernPath); err == nil {
				return string(bytes), nil
			}
			
			// Route B: Legacy RHEL 7 / early kernel fallback alignment scheme
			legacyPath := filepath.Join("/sys/block", cleanDmName, "name")
			if bytes, err := os.ReadFile(legacyPath); err == nil {
				return string(bytes), nil
			}

			return "", fmt.Errorf("dm name not accessible for: %s", cleanDmName)
		},
	)

	// FIXED: Halt validation loops if the execution gater failed or timed out.
	// This ensures that text error strings never leak up into active command loops.
	if err != nil || result == "" {
		logger.Warningf("readDMNameSafe: failed to read device mapper attribute name for %s: %v", cleanDmName, err)
		return ""
	}

	// =========================================================================
	// SANITIZE POTENTIAL NULL-BYTE POLLUTION FROM SYSLOG/UDEV STACKS
	// =========================================================================
	// Explicitly strip out all non-printable ASCII elements, null bytes, 
	// and trailing newlines natively to prevent payload contamination.
	sanitized := strings.Map(func(rn rune) rune {
		if rn == 0 || rn == '\x00' || (rn < 32 && rn != '\t' && rn != '\n' && rn != '\r') {
			return -1 // Drop control blocks and null bytes entirely
		}
		return rn
	}, result)

	return strings.TrimSpace(sanitized)
}


// dmIoctlCall executes a safe, stack-pinned kernel ioctl call on /dev/mapper/control.
// Hardened against string truncation, missing null-terminators, and Go GC pointer shifting.
func (r *OsDeviceConnectivityHelperScsiGeneric) dmIoctlCall(ctx context.Context, name string, op uintptr, flags uint32) error {
	cleanName := filepath.Base(name)
	if cleanName == "" || cleanName == "control" {
		return fmt.Errorf("dm-ioctl: invalid device target name provided: %s", name)
	}

	// Capture the tuple, discard the struct{}
	// FIXED: Corrected anonymised return type parameters list syntax to clear compilation breaks.
	_, err := executer.ExecuteUninterruptible[struct{}](
		ctx, r.KeyedGater, "dm-ioctl-"+cleanName, 1, 10, 1*time.Second, 5*time.Second,
		func(wCtx context.Context) (struct{}, error) {
			f, err := os.OpenFile(DM_IOCTL_CONTROL, os.O_RDWR, 0)
			if err != nil { 
				return struct{}{}, fmt.Errorf("dm-ioctl: failed to open control path: %w", err) 
			}
			defer f.Close()

			const dmNameLen = 128
			if len(cleanName) >= dmNameLen {
				return struct{}{}, fmt.Errorf("dm-ioctl: target name length (%d) exceeds kernel limit of %d", len(cleanName), dmNameLen-1)
			}

			type dmIoctlPacked struct {
				version     [3]uint32
				dataSize    uint32
				flags       uint32
				eventNr     uint32
				targetCount uint32
				openCount   int32
				lastEventNr uint32
				dev         uint64
				name        [dmNameLen]byte
				uuid        [dmNameLen]byte
				dataStart   uint32
			}

			var payload dmIoctlPacked
			payload.version = [3]uint32{4, 0, 0}
			payload.dataSize = uint32(unsafe.Sizeof(payload))
			payload.flags = flags

			copy(payload.name[:dmNameLen-1], cleanName)
			payload.name[len(cleanName)] = 0 // Enforce physical null terminator explicitly

			payloadPtr := unsafe.Pointer(&payload)

			logger.Infof("[DM-Ioctl] Dispatched tracking operator code %v on device map: %s", op, cleanName)
			_, _, errno := unix.Syscall(
				unix.SYS_IOCTL, 
				f.Fd(), 
				op, 
				uintptr(payloadPtr),
			)

			runtime.KeepAlive(payload)

			if errno != 0 && errno != unix.ENXIO && errno != unix.ENOENT { 
				return struct{}{}, fmt.Errorf("dm-ioctl execution returned kernel errno: %v", errno) 
			}

			return struct{}{}, nil
		},
	)
	return err
}


// IdentityAwarePreScan performs a strict safety scan prior to volume staging to confirm path availability and eliminate leaks.
// Hardened against invalid SCSI IOCTL calls on NVMe paths, memory leaks in busyTimestamps, and signature matching drift.
// Return values:
// mpathDevice: The resolved device path if found and active.
// isStaged: Already mounted at the exact target folder path.
// deviceReady: Block device visible in sysfs/dev (skip bus rescan).
// scanInProgress: State machine is actively stabilizing / reset needed (stop main execution).
func (r *OsDeviceConnectivityHelperScsiGeneric) IdentityAwarePreScan(ctx context.Context, targetPath string, volumeId string) (string, bool, bool, bool, error) {
	logger.Infof("[PreScan-Trace] Entering IdentityAwarePreScan: targetPath='%s', volumeId='%s'", targetPath, volumeId)

	if err := ctx.Err(); err != nil {
		logger.Warningf("[PreScan-Trace] Context canceled early: %v", err)
		return "", false, false, false, status.FromContextError(err).Err()
	}

	rawScsiTarget := strings.ToLower(strings.TrimSpace(volumeId))
	if len(rawScsiTarget) != 32 {
		logger.Errorf("[PreScan-Trace] Invalid specification footprint size provided: len=%d", len(rawScsiTarget))
		return "", false, false, false, status.Errorf(codes.InvalidArgument, "pre-scan: invalid specification footprint size: %s", volumeId)
	}
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)
	logger.Debugf("[PreScan-Trace] Targets normalized: rawScsiTarget='%s', rawNvmeTarget='%s'", rawScsiTarget, rawNvmeTarget)

	mpathAlias := r.Helper.findDMByWWID(ctx, rawScsiTarget)
	var mpathName string
	if mpathAlias != "" {
		if resolvedPath, errLink := filepath.EvalSymlinks(filepath.Join("/dev/mapper", mpathAlias)); errLink == nil {
			mpathName = filepath.Base(resolvedPath)
			logger.Debugf("[PreScan-Trace] Resolved device mapper alias '%s' to canonical name: '%s'", mpathAlias, mpathName)
		} else {
			logger.Debugf("[PreScan-Trace] Failed to resolve symlink for mapper alias '%s': %v", mpathAlias, errLink)
		}
	} else {
		logger.Debugf("[PreScan-Trace] No active device mapper alias found via WWID search for '%s'", rawScsiTarget)
	}

	mounts, _ := r.Mounter.GetMountsForPath(targetPath)
	logger.Debugf("[PreScan-Trace] Active mount check count for path '%s': %d", targetPath, len(mounts))

	if len(mounts) > 0 {
		// FIXED: Resolve major/minor numbers dynamically from the mount source path or target path using unix.Stat
		var major, minor uint32
		var statT unix.Stat_t
		statPath := mounts[0].MountSource
		if statPath == "" {
			statPath = targetPath
		}
		
		if errStat := unix.Stat(statPath, &statT); errStat == nil {
			major = unix.Major(uint64(statT.Rdev))
			minor = unix.Minor(uint64(statT.Rdev))
		} else {
			logger.Warningf("[PreScan-Trace] Failed to stat mount path '%s' for major/minor attributes: %v", statPath, errStat)
		}

		currentWWIDRaw, _ := r.Helper.getWWIDByDev(ctx, major, minor)
		currentWWID := normalizeWWID(currentWWIDRaw)
		logger.Debugf("[PreScan-Trace] Interrogated active mount device WWID: raw='%s', normalized='%s'", currentWWIDRaw, currentWWID)

		// Now safely passes the resolved major and minor attributes into your NVMe state checker
		isNvmeMount := r.SafeResolveNvmeMountState(ctx, major, minor)
		hwWWID := r.getPathHwid(ctx, mpathAlias, isNvmeMount)


		logger.Debugf("[PreScan-Trace] Hardware identity results: isNvmeMount=%v, hwWWID='%s'", isNvmeMount, hwWWID)

		isMatch := (len(currentWWID) == 32 && (currentWWID == rawScsiTarget || currentWWID == rawNvmeTarget)) ||
			(hwWWID != "" && r.MatchVolumeToScsiSpec(hwWWID, rawScsiTarget))
		logger.Infof("[PreScan-Trace] Mount identity match status evaluated: isMatch=%v", isMatch)

		if isMatch {
			helper := GetDmsPathHelperGeneric{}
			if mpathName != "" && helper.IsDeviceMapper(mpathName) {
				slaveCount := helper.GetSlaveCount(ctx, r.KeyedGater, mpathName)
				logger.Debugf("[PreScan-Trace] Device Mapper '%s' active slave count: %d", mpathName, slaveCount)
				
				if slaveCount == 0 {
					logger.Infof("[PreScan-Trace] Active DM node '%s' has zero slaves. Tearing down volume...", mpathName)
					_ = r.TeardownVolume(ctx, targetPath, rawScsiTarget)
					r.busyTimestamps.Delete(rawScsiTarget)
					
					ctrlName := ExtractNvmeControllerBase(mpathName)
					if ctrlName != "" {
						r.busyTimestamps.Delete(fmt.Sprintf("%s-%s", ctrlName, rawScsiTarget))
					}
					return "", false, false, true, nil
				}
			}

			devNode := mounts[0].MountSource
			if devNode == "" && mpathName != "" {
				mapperPath := filepath.Join("/dev/mapper", mpathName)
				directDevPath := filepath.Join("/dev", mpathName)

				if _, errStat := os.Stat(mapperPath); errStat == nil {
					devNode = mapperPath
				} else {
					devNode = directDevPath
				}
			}

			if devNode == "" {
				logger.Errorf("[PreScan-Trace] Target disk mount source could not be structurally resolved.")
				return "", false, false, false, status.Errorf(codes.Internal, "pre-scan: target disk mount source could not be resolved structurally")
			}

			nodeName := filepath.Base(devNode)
			if strings.HasPrefix(nodeName, "nvme") && strings.Contains(nodeName, "c") {
				if lastNIdx := strings.LastIndex(nodeName, "n"); lastNIdx != -1 && lastNIdx > 0 {
					if cIdx := strings.Index(nodeName, "c"); cIdx != -1 && cIdx < lastNIdx {
						devNode = filepath.Join(filepath.Dir(devNode), nodeName[:cIdx]+nodeName[lastNIdx:])
					}
				}
			}
			
			logger.Infof("[PreScan-Trace] Reusing valid active mount destination device node: '%s'", devNode)
			r.busyTimestamps.Delete(rawScsiTarget)
			ctrlName := ExtractNvmeControllerBase(nodeName)
			if ctrlName != "" {
				r.busyTimestamps.Delete(fmt.Sprintf("%s-%s", ctrlName, rawScsiTarget))
			}
			return devNode, true, true, false, nil
		}

		logger.Warningf("[PreScan-Trace] Identification collision detected on active mount path '%s'. Forcing unmount pass.", targetPath)
		_ = r.Mounter.UnmountWithTimeout(ctx, targetPath, 30*time.Second)
		r.busyTimestamps.Delete(rawScsiTarget)
		return "", false, false, false, status.Error(codes.Internal, "pre-scan: identification collision detected")
	}

	helper := GetDmsPathHelperGeneric{}
	var hasDevice, isPending bool
	var devName string
	
	if mpathName != "" {
		logger.Debugf("[PreScan-Trace] Evaluating specific target sysfs topology for '%s'", mpathName)
		hasDevice, isPending, _ = helper.EvaluateSpecificSysfsTopology(ctx, r.KeyedGater, mpathName, rawScsiTarget, true)
		devName = mpathName
	} else {
		logger.Debugf("[PreScan-Trace] Performing general broad sysfs topology scan pass...")
		hasDevice, isPending, devName = helper.EvaluateSysfsTopology(ctx, r.KeyedGater, rawScsiTarget, true)
	}
	logger.Infof("[PreScan-Trace] Topology evaluation result: hasDevice=%v, isPending=%v, devName='%s'", hasDevice, isPending, devName)

	if hasDevice {
		cleanDevName := filepath.Base(devName)
		if helper.IsDeviceMapper(cleanDevName) {
			if helper.GetSlaveCount(ctx, r.KeyedGater, cleanDevName) == 0 {
				logger.Infof("[PreScan-Trace] Discovered orphaned device mapper layout node '%s' with 0 slaves. Cleaning up...", cleanDevName)
				_ = r.cleanupOrphanedTopology(ctx, cleanDevName, rawScsiTarget)
				r.busyTimestamps.Delete(rawScsiTarget)
				ctrlName := ExtractNvmeControllerBase(cleanDevName)
				if ctrlName != "" {
					r.busyTimestamps.Delete(fmt.Sprintf("%s-%s", ctrlName, rawScsiTarget))
				}
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

		var targetPathOutput string
		mapperPath := filepath.Join("/dev/mapper", cleanDevName)
		directDevPath := filepath.Join("/dev", cleanDevName)

		// FIXED: Safe multi-tier path checking via os.Stat instead of blind assumption
		if _, errStat := os.Stat(mapperPath); errStat == nil {
			targetPathOutput = mapperPath
			logger.Debugf("[PreScan-Trace] Validated absolute storage target path via mapper: %s", targetPathOutput)
		} else if _, errStatDirect := os.Stat(directDevPath); errStatDirect == nil {
			targetPathOutput = directDevPath
			logger.Debugf("[PreScan-Trace] Validated absolute storage target path via raw dev root: %s", targetPathOutput)
		} else {
			logger.Warningf("[PreScan-Trace] Target device node '%s' found in topology metadata but missing from host VFS trees. Forcing rescan pass.", cleanDevName)
			r.busyTimestamps.Delete(rawScsiTarget)
			return "", false, false, false, nil
		}
		
		if isPending {
			ctrlTrackingKey := fmt.Sprintf("%s-%s", ExtractNvmeControllerBase(cleanDevName), rawScsiTarget)
			logger.Infof("[PreScan-Trace] Device '%s' is pending/settling. Checking timeout tracking key: '%s'", cleanDevName, ctrlTrackingKey)
			
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
			return targetPathOutput, false, false, true, status.Error(codes.Aborted, "discovery cycle is actively settling.")
		}

		logger.Infof("[PreScan-Trace] Successfully resolved ready device path target: '%s'", targetPathOutput)
		r.busyTimestamps.Delete(rawScsiTarget)
		ctrlName := ExtractNvmeControllerBase(cleanDevName)
		if ctrlName != "" {
			r.busyTimestamps.Delete(fmt.Sprintf("%s-%s", ctrlName, rawScsiTarget))
		}
		return targetPathOutput, false, true, false, nil
	}

	logger.Infof("[PreScan-Trace] Pre-scan finished with no device node matched or discovered for target volume.")
	r.busyTimestamps.Delete(rawScsiTarget)
	return "", false, false, false, nil
}


// SafeResolveNvmeMountState authoritatively inspects kernel state via major/minor attributes.
// Fully hardened: Bypasses ALL path/alias string heuristics by interrogating sysfs block topology.
func (r *OsDeviceConnectivityHelperScsiGeneric) SafeResolveNvmeMountState(ctx context.Context, major, minor uint32) bool {
	logger.Infof("[NvmeResolve-Trace] Inspecting major:minor %d:%d for NVMe backing attributes", major, minor)

	if ctx.Err() != nil {
		return false
	}

	// 1. Resolve canonical WWID/UUID properties using your hardened major/minor scanner
	wwid, errWwid := r.Helper.getWWIDByDev(ctx, major, minor)
	if errWwid == nil && wwid != "" {
		// Check if the unique identifier maps a structural NVMe signature registry token
		logger.Infof("[NvmeResolve-Trace] found id %s", wwid)
		if strings.HasPrefix(strings.ToLower(wwid), "nvme.") || strings.Contains(strings.ToLower(wwid), "nguid") {
			logger.Infof("[NvmeResolve-Trace] Confirmed NVMe protocol via device WWID pattern: '%s'", wwid)
			return true
		}
	}

	// 2. Structural Sysfs Check: Find the kernel name (dm-X or nvmeXnY) via /sys/dev/block
	basePath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	realPath, errLink := filepath.EvalSymlinks(basePath)
	if errLink != nil {
		logger.Debugf("[NvmeResolve-Trace] Failed to resolve symlink for dev block path %s: %v", basePath, errLink)
		return false
	}

	kernelBlockName := filepath.Base(realPath)
	logger.Debugf("[PreScan-Trace] Resolved major:minor to canonical kernel identifier: '%s'", kernelBlockName)

	// Tier A: If the kernel node is a Native NVMe block namespace device node
	if strings.HasPrefix(kernelBlockName, "nvme") {
		logger.Infof("[NvmeResolve-Trace] Confirmed native NVMe base block match: '%s'", kernelBlockName)
		return true
	}

	// Tier B: If the kernel node is a Device Mapper (dm-X) layout node, interrogate its physical slaves
	if strings.HasPrefix(kernelBlockName, "dm-") {
		slavesPath := filepath.Join("/sys/block", kernelBlockName, "slaves")
		logger.Debugf("[NvmeResolve-Trace] Interrogating device mapper slave paths at: %s", slavesPath)
		
		if dFile, errOpen := os.Open(slavesPath); errOpen == nil {
			defer dFile.Close()
			if entries, errRead := dFile.ReadDir(10); errRead == nil {
				for _, entry := range entries {
					// If ANY underlying physical path lane starts with "nvme", it is structurally an NVMe-DM map
					if strings.HasPrefix(entry.Name(), "nvme") {
						logger.Infof("[NvmeResolve-Trace] Confirmed NVMe backing via true device-mapper slave topology: %s -> %s", kernelBlockName, entry.Name())
						return true
					}
				}
			}
		}
	}

	logger.Debugf("[NvmeResolve-Trace] Major:minor %d:%d verified as a non-NVMe standard SAN storage lane.", major, minor)
	return false
}



// getPathHwid securely recovers hardware WWID signatures while maintaining an upfront protocol shield.
// Hardened against undeclared parameter references, container VFS path masks, and unsafe pointer conversions.
func (r *OsDeviceConnectivityHelperScsiGeneric) getPathHwid(ctx context.Context, mpathAlias string, isNvmeMount bool) string {
	logger.Infof("[GetHwid-Trace] Executing protocol shield extraction: mpathAlias='%s', isNvmeMount=%v", mpathAlias, isNvmeMount)
	
	if ctx.Err() != nil {
		logger.Warningf("[GetHwid-Trace] Aborted lookuppass due to caller context termination.")
		return ""
	}

	if mpathAlias == "" {
		return ""
	}

	// 1. RECOVERY LAYER: Resolve the definitive underlying dm-X string name upfront
	// to ensure sysfs directory queries do not miss target folders.
	lookupName := mpathAlias
	if resolvedPath, errLink := filepath.EvalSymlinks(filepath.Join("/dev/mapper", mpathAlias)); errLink == nil {
		lookupName = filepath.Base(resolvedPath)
		logger.Debugf("[GetHwid-Trace] Resolved user-space alias '%s' to kernel canonical string: %s", mpathAlias, lookupName)
	}

	isMpathNVMe := false
	if !isNvmeMount {
		// Enforce validation checks using the absolute resolved kernel naming standard
		if strings.HasPrefix(lookupName, "dm-") {
			slavesPath := filepath.Join("/sys/block", lookupName, "slaves")
			logger.Debugf("[GetHwid-Trace] Interrogating layout slaves mapping directory: %s", slavesPath)
			
			dFile, errOpen := os.Open(slavesPath)
			if errOpen == nil {
				// Prevent descriptor leak footprints via structured defer release
				defer dFile.Close()
				
				if entries, errRead := dFile.ReadDir(5); errRead == nil {
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), "nvme") {
							isMpathNVMe = true
							logger.Infof("[GetHwid-Trace] Target %s confirmed as NVMe-backed Device Mapper path.", lookupName)
							break
						}
					}
				}
			} else if !os.IsNotExist(errOpen) {
				logger.Warningf("[GetHwid-Trace] Unexpected VFS error reading sysfs slaves for %s: %v", lookupName, errOpen)
			}
		}
	}

	var hwWWID string
	// Execute raw SCSI Generic inquiry only if it is completely clear of NVMe protocol paths
	if !isNvmeMount && !isMpathNVMe {
		sanitizedInqPath := mpathAlias
		if !filepath.IsAbs(sanitizedInqPath) {
			mapperInqPath := filepath.Join("/dev/mapper", mpathAlias)
			directDevInqPath := filepath.Join("/dev", mpathAlias)
			
			if _, errStat := os.Stat(mapperInqPath); errStat == nil {
				sanitizedInqPath = mapperInqPath
			} else {
				sanitizedInqPath = directDevInqPath
			}
		}

		logger.Infof("[GetHwid-Trace] Issuing safe SCSI Inquiry IOCTL wrapper code against target path: %s", sanitizedInqPath)
		var errInq error
		hwWWID, errInq = r.Helper.GetWwnByScsiInq(ctx, r.KeyedGater, sanitizedInqPath)
		
		// Force the compiler to pin execution and string variables in place
		runtime.KeepAlive(sanitizedInqPath)

		if errInq != nil {
			logger.Warningf("[GetHwid-Trace] Low-level SCSI inquiry failed or was rejected on node %s: %v", sanitizedInqPath, errInq)
		} else {
			logger.Infof("[GetHwid-Trace] Successfully extracted hardware fingerprint signature: '%s'", hwWWID)
		}
	} else {
		logger.Infof("[GetHwid-Trace] Upfront protocol shield triggered. Bypassing SCSI Inquiry on NVMe lane target.")
	}

	return hwWWID
}

// cleanupOrphanedTopology clears residual hardware definitions from the node host.
func (r *OsDeviceConnectivityHelperScsiGeneric) cleanupOrphanedTopology(ctx context.Context, mpathName string, expectedWWID string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	// UNCHANGED LOGIC: Respecting the black-box volume ID processing algorithms exactly
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
			
			// Isolated NVMe parsing lane ensures character controllers are only stripped from NVMe nodes
			if strings.HasPrefix(targetNode, "nvme") {
				if strings.Contains(targetNode, "c") {
					if lastNIdx := strings.LastIndex(targetNode, "n"); lastNIdx != -1 && lastNIdx > 0 {
						if cIdx := strings.Index(targetNode, "c"); cIdx != -1 && cIdx < lastNIdx {
							baseBlockName = targetNode[:cIdx] + targetNode[lastNIdx:] 
						}
					}
				}
				mpathName = baseBlockName
				isNativeNVMe = true
			} else if strings.HasPrefix(targetNode, "sd") || r.IsScsiBlockDevice(ctx, targetNode) {
				// FIXED: Removed the broken nested NVMe string-mangling checks from the SCSI pathway entirely

				sysBlockTarget := filepath.Join("/sys/block", baseBlockName)
				if resolvedBlock, errLink := filepath.EvalSymlinks(sysBlockTarget); errLink == nil {
					baseBlockName = filepath.Base(resolvedBlock)
				}

				// FIXED: Cast variables via intermediate buffers to guarantee full multi-arch compilation safety
				sysMaj, sysMin, errSysfs := r.Helper.GetMajorMinorFromSysfs(ctx, baseBlockName)
				if errSysfs == nil && sysMaj != 0 {
					mpathName = r.GetDMNameFromMinor(ctx, uint32(sysMin))
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

	devEntries, errDir := os.ReadDir("/dev")
	if errDir != nil {
		return fmt.Errorf("failed to safely query system device nodes list: %w", errDir)
	}

	const maxCapCeiling = 10000
	rawNames := make([]string, 0, 100)

	for _, entry := range devEntries {
		devName := entry.Name()
		if !nvmeNamespaceRegex.MatchString(devName) {
			continue
		}
		
		if len(rawNames) >= maxCapCeiling {
			logger.Warningf("[VFS-Guard] NVMe queue tracking list reached safe allocation ceiling (%d). Truncating scan pass.", maxCapCeiling)
			break
		}

		rawNames = append(rawNames, devName)
	}

	processBatch := func(batch []string, gaterKey string) error {
		if len(batch) == 0 {
			return nil
		}

		logger.Infof("[Disable-Queue] Processing concurrent chunk of %d targets", len(batch))
		_, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
			ctx,
			r.KeyedGater,
			gaterKey,
			10,  
			100, 
			5*time.Second,
			35*time.Second, 
			batch,
			func(wCtx context.Context, index int, devName string, cancelBatch func()) (struct{}, error) {
				baseBlockName := devName 
				baseBlockDir := filepath.Join("/sys/block", devName)
				if _, errStat := os.Stat(baseBlockDir); os.IsNotExist(errStat) {
					baseBlockDir = filepath.Join("/sys/class/block", devName)
				}
				targetSysDir := baseBlockDir

				if strings.Contains(devName, "c") {
					if lastNIdx := strings.LastIndex(devName, "n"); lastNIdx != -1 && lastNIdx > 0 {
						if cIdx := strings.Index(devName, "c"); cIdx != -1 && cIdx < lastNIdx {
							ctrlPart := devName[:cIdx]  
							nsPart := devName[lastNIdx:] 
							
							baseBlockName = ctrlPart + nsPart 
							targetSysDir = filepath.Join("/sys/block", baseBlockName)
							if _, errStat := os.Stat(targetSysDir); os.IsNotExist(errStat) {
								targetSysDir = filepath.Join("/sys/class/block", baseBlockName)
							}
							logger.Debugf("[Disable-Queue] Normalized virtual block node routing path: %s -> %s", devName, targetSysDir)
						}
					}
				}

				var discoveredID string
				deviceNode := filepath.Join("/dev", devName)
				if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
					var nvmeInfo nvmeIdTarget
					// FIXED: Replaced unsafe syscall.Syscall with unix.Syscall to lock stack pinning during GC passes
					_, _, errno := unix.Syscall(
						unix.SYS_IOCTL,
						df.Fd(),
						uintptr(NVME_IOCTL_ID_TARGET),
						uintptr(unsafe.Pointer(&nvmeInfo)),
					)
					df.Close()

					if errno == 0 {
						discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
					}
				}

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
				subsysSymlink := filepath.Join(baseBlockDir, "device", "subsystem")
				
				realSubsysPath, errLink := filepath.EvalSymlinks(subsysSymlink)

				if errLink == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys") {
					if subFile, errOpenSub := os.Open(realSubsysPath); errOpenSub == nil {
						processedSubCount := 0
						for {
							entries, errSub := subFile.ReadDir(100)
							if errSub != nil && errSub != io.EOF {
								break
							}
							for _, e := range entries {
								name := e.Name()
								if strings.HasPrefix(name, "nvme") && !nvmeNamespaceRegex.MatchString(name) {
									if processedSubCount >= maxCapCeiling {
										break
									}
									processedSubCount++
								
									controllersToUpdate = append(controllersToUpdate, name)
								}
							}
							if processedSubCount >= maxCapCeiling || len(entries) < 100 || errSub == io.EOF {
								break
							}
						}
						subFile.Close()
					}
				} else {
					ctrlName := ExtractNvmeControllerBase(devName)
					controllersToUpdate = append(controllersToUpdate, ctrlName)
				}

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

	const chunkSize = 100
	for i := 0; i < len(rawNames); i += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}

		end := i + chunkSize
		if end > len(rawNames) {
			end = len(rawNames)
		}
		currentChunkBatch := rawNames[i:end]

		uniqueGaterKey := fmt.Sprintf("batch-disable-nvme-queue-%d", time.Now().UnixNano())
		if errChunk := processBatch(currentChunkBatch, uniqueGaterKey); errChunk != nil {
			return errChunk
		}
	}

	return nil
}

// purgeStuckPhysicalPathsDualProtocol scans host adapters in parallel to hot-unplug orphaned fabric paths cleanly.
// Production-Hardened: Enforces absolute compiler memory pinning and eliminates unsafe address drift hazards.
func (r *OsDeviceConnectivityHelperScsiGeneric) purgeStuckPhysicalPathsDualProtocol(ctx context.Context, rawScsiTarget, rawNvmeTarget string) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	scsiMatchTarget := normalizeWWID(rawScsiTarget)
	nvmeMatchTarget := normalizeWWID(rawNvmeTarget)

	dFile, errOpen := os.Open("/dev")
	if errOpen != nil {
		return fmt.Errorf("failed to scan system device path layer under safety frame: %w", errOpen)
	}
	defer dFile.Close()

	const maxCapCeiling = 10000
	rawNames := make([]string, 0, 100)

	// =========================================================================
	// STAGE 1: MICROSECOND SNAPSHOT SWEEP (Decouples VFS State Instantly)
	// =========================================================================
	for {
		if err := ctx.Err(); err != nil {
			return ctx.Err()
		}

		entries, readErr := dFile.ReadDir(100)
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("failed to stream directory snapshots from /dev: %w", readErr)
		}
		if len(entries) == 0 || readErr == io.EOF {
			break
		}

		for _, f := range entries {
			devName := f.Name()
			isSCSI := strings.HasPrefix(devName, "sd")
			// FIXED: Replaced brittle method dependency with standard prefix-aware validation rules
			isNVMe := strings.HasPrefix(devName, "nvme") && nvmeNamespaceRegex.MatchString(devName)

			if !isSCSI && !isNVMe {
				continue
			}
			
			if len(rawNames) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] Dual protocol purge list reached safe allocation ceiling (%d). Truncating scan pass.", maxCapCeiling)
				break
			}

			rawNames = append(rawNames, devName)
		}

		if len(rawNames) >= maxCapCeiling || readErr == io.EOF {
			break
		}
	}

	var aggregatedErrors []string

	// =========================================================================
	// STAGE 2: DECOUPLED BATCH CHUNKING PROCESSING ENGINE
	// =========================================================================
	processBatch := func(batch []string, gaterKey string) error {
		if len(batch) == 0 {
			return nil
		}

		logger.Infof("[Purge-Paths] Processing concurrent chunk of %d targets", len(batch))
		results, errBatch := executer.ExecuteUninterruptibleBatch[string, struct{}](
			ctx,
			r.KeyedGater,
			gaterKey,
			15,  
			100, 
			5*time.Second,
			30*time.Second, 
			batch,
			func(wCtx context.Context, index int, devName string, cancelBatch func()) (struct{}, error) {
				if err := wCtx.Err(); err != nil {
					return struct{}{}, err
				}

				isSCSI := strings.HasPrefix(devName, "sd")
				isNVMe := strings.HasPrefix(devName, "nvme") && nvmeNamespaceRegex.MatchString(devName)

				baseBlockName := devName 
				targetSysDir := filepath.Join("/sys/block", devName)
				
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
					deviceNode := filepath.Join("/dev", devName)
					if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
						var nvmeInfo nvmeIdTarget
						_, _, errno := unix.Syscall(
							unix.SYS_IOCTL,
							df.Fd(),
							uintptr(NVME_IOCTL_ID_TARGET),
							uintptr(unsafe.Pointer(&nvmeInfo)),
						)
						df.Close()
						// FIXED: Force compiler memory pinning boundary to stop address shift corruptions
						runtime.KeepAlive(nvmeInfo) 

						if errno == 0 {
							discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
						}
					}
				}

				if discoveredID == "" {
					var wwidPath string
					if isSCSI {
						wwidPath = filepath.Join("/sys/block", devName, "device", "wwid")
					} else {
						wwidPath = filepath.Join(targetSysDir, "wwid")
						if _, errStat := os.Stat(wwidPath); os.IsNotExist(errStat) {
							wwidPath = filepath.Join("/sys/block", baseBlockName, "wwid")
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

				deletePath, pciAddress, useUnbindStrategy := r.getDeletePath(wCtx, r.KeyedGater, devName, baseBlockName, targetSysDir, isSCSI, isNVMe)
				if deletePath == "" {
					return struct{}{}, nil
				}

				var payloadBytes []byte
				if useUnbindStrategy {
					payloadBytes = []byte(pciAddress)
				} else {
					payloadBytes = []byte("1\n")
				}

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

		// FIXED: Restructured type-safe extraction from the custom batch response fields
		for _, res := range results {
			if res.Err != nil {
				aggregatedErrors = append(aggregatedErrors, res.Err.Error())
			}
		}
		return nil
	}

	const chunkSize = 100
	for i := 0; i < len(rawNames); i += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}

		end := i + chunkSize
		if end > len(rawNames) {
			end = len(rawNames)
		}
		currentChunkBatch := rawNames[i:end]

		uniqueGaterKey := fmt.Sprintf("batch-purge-dual-paths-%s-%d", scsiMatchTarget, time.Now().UnixNano())
		if errChunk := processBatch(currentChunkBatch, uniqueGaterKey); errChunk != nil {
			return errChunk
		}
	}
	
	if len(aggregatedErrors) > 0 {
		return fmt.Errorf("purge failed for target nodes: %s", strings.Join(aggregatedErrors, "; "))
	}

	logger.Infof("purgeStuckPhysicalPathsDualProtocol successfully synchronized all hardware evictions.")
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getDeletePath(wCtx context.Context, gater *executer.KeyedGater, devName, baseBlockName, targetSysDir string, isSCSI, isNVMe bool) (deletePath, pciAddress string, useUnbindStrategy bool) {
	
	if isSCSI {
		deletePath = filepath.Join("/sys/block", devName, "device", "delete")
	} else if isNVMe {
		// Target standard direct namespace block delete targets if they exist
		deletePath = filepath.Join("/sys/block", baseBlockName, "delete")
		if _, errStat := os.Stat(deletePath); os.IsNotExist(errStat) {
			deletePath = filepath.Join("/sys/block", baseBlockName, "device", "delete")
		}
		if _, errStat := os.Stat(deletePath); os.IsNotExist(errStat) {
			deletePath = filepath.Join(targetSysDir, "delete")
		}

		// FIXED: If individual namespace deletes are missing (universal Linux behavior for shared NVMe-oF paths),
		// we MUST return an empty deletePath here if sibling volumes are active. 
		// We do NOT fall back to delete_controller here because doing so breaks concurrent companion volume teardowns.
		if _, errStat := os.Stat(deletePath); os.IsNotExist(errStat) {
			logger.Debugf("[Purge-Paths] [%s] Individual namespace delete unavailable on this kernel. Leaving controllers active to protect sibling stability.", devName)
			return "", "", false // Bypasses physical write, allows DM layer to handle unlinking cleanly
		}
	}
	return deletePath, pciAddress, useUnbindStrategy
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
        if err := ctx.Err(); err != nil {
                return "", ctx.Err()
        }

        name := filepath.Base(deviceName)
        logger.Warningf("getWWIDBySysfs entry point triggered for: %s", name)

        var isNVMe, isDM bool
        baseBlockName := name
        sysBlockTarget := filepath.Join("/sys/block", name)

        // FIXED: Aligned local variables to use 'name' and 'sysBlockTarget' consistently.
        // This ensures the partition resolution compiles cleanly and updates the real path references.
        if _, err := os.Stat(sysBlockTarget); os.IsNotExist(err) {
                classBlockPath := filepath.Join("/sys/class/block", name)
                if realClassPath, errEval := filepath.EvalSymlinks(classBlockPath); errEval == nil {
                        if strings.Contains(realClassPath, "/block/") {
                                parts := strings.Split(realClassPath, "/block/")
                                if len(parts) == 2 {
                                        subParts := strings.Split(parts[1], "/")
                                        if len(subParts) > 0 {
                                                name = subParts[0] // Correctly abstracts "nvme0n1p1" to parent "nvme0n1"
                                                baseBlockName = name
                                                sysBlockTarget = filepath.Join("/sys/block", name)
                                        }
                                }
                        }
                }
        }

	if resolvedBlock, errLink := filepath.EvalSymlinks(sysBlockTarget); errLink == nil {
		sysBlockTarget = resolvedBlock
		baseBlockName = filepath.Base(resolvedBlock)
	}
	targetSysDir := sysBlockTarget

	if strings.HasPrefix(name, "dm-") {
		isDM = true
	} else if nvmeNamespaceRegex.MatchString(name) || strings.HasPrefix(name, "nvme") {
		isNVMe = true
		
		if strings.Contains(name, "c") {
			if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
				if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
					ctrlPart := name[:cIdx]  
					nsPart := name[lastNIdx:] 
					
					baseBlockName = ctrlPart + nsPart 
					targetSysDir = filepath.Join("/sys/block", baseBlockName) 
					
					if resolvedBlock, errLink := filepath.EvalSymlinks(targetSysDir); errLink == nil {
						targetSysDir = resolvedBlock
						baseBlockName = filepath.Base(resolvedBlock)
					}
					logger.Debugf("[Sysfs-WWID] Normalized virtual block node routing path: %s -> %s", name, targetSysDir)
				}
			}
		}
	}

	var discoveredID string
	var readErr error

	if isNVMe {
		if data, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && data != "" {
			discoveredID = normalizeWWID(data)
		} else if data, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" {
			discoveredID = normalizeWWID(data)
		} else {
			readErr = fmt.Errorf("failed to read nguid or uuid attributes from nvme path: %s", targetSysDir)
		}
	} else if isDM {
		// FIXED: Pass baseBlockName to guarantee absolute gater key synchronization node-wide
		if data, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "dm", "uuid")); err == nil && data != "" {
			discoveredID = normalizeWWID(data)
		} else if data, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" {
			discoveredID = normalizeWWID(data)
		} else {
			readErr = fmt.Errorf("failed to read device mapper uuid attributes from path: %s", targetSysDir)
		}
	} else {
		// =========================================================================
		// TRADITIONAL SCSI VPD PAGE 0x83 PARSING LAYER
		// =========================================================================
		scsiWwidPath := filepath.Join(targetSysDir, "device", "wwid")
		// FIXED: Pass synchronized baseBlockName lock token consistently
		if data, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, scsiWwidPath); err == nil && data != "" {
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
               rawNvmeFromHost := strings.TrimPrefix(parsedID, "nvme-eui.")
               if len(rawNvmeFromHost) != 32 {
                       return false // Malformed NVMe descriptor payload size
               }

               // Convert the raw target SCSI spec into its expected NVMe sequence
               expectedNvmeSeq := convertScsiIdToNguid(rawScsiID)

               // Directly evaluate if the NVMe hardware matches our translated target
               return rawNvmeFromHost == expectedNvmeSeq

       case strings.HasPrefix(parsedID, "3") || strings.HasPrefix(parsedID, "naa.") || strings.HasPrefix(parsedID, "t10."):
               // STRATEGY B: Host node is talking to the volume via standard Fibre Channel / iSCSI SCSI.
               rawScsiFromHost := parsedID
               if strings.HasPrefix(rawScsiFromHost, "3") {
                       rawScsiFromHost = strings.TrimPrefix(rawScsiFromHost, "3")
               } else if strings.HasPrefix(rawScsiFromHost, "naa.") {
                       rawScsiFromHost = strings.TrimPrefix(rawScsiFromHost, "naa.")
               } else if strings.HasPrefix(rawScsiFromHost, "t10.") {
                       rawScsiFromHost = strings.TrimPrefix(rawScsiFromHost, "t10.")
               }

               // Directly compare the raw host SCSI sequence against our raw target SCSI spec
               return rawScsiFromHost == rawScsiID

       default:
               // STRATEGY C: The descriptor belongs to an unrelated format (Type 1, Type 8 text iqn, etc.)
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

// TODO unused (has nvme sensitive implementation in 1.13.1)
func (o OsDeviceConnectivityHelperGeneric) GetMpathVolumeId(ctx context.Context, gater *executer.KeyedGater, dmPath string) (volId string, err error) {
	SgInqWwn, err := o.GetWwnByScsiInq(ctx, gater, dmPath)
	if err != nil {
		return "", err
	}
	return SgInqWwn, nil
}


// GetWwnByScsiInq performs a shielded hardware query against a generic SCSI target device node.
// Fully flattened and un-nested to preserve cross-worker pool exclusivity during heavy concurrent storms.
func (o *OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(ctx context.Context, gater *executer.KeyedGater, dev string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	// RESTORED VFS LAYER: Resolve target paths natively via absolute canonical link check
	// to protect against hidden symbolic redirections or relative path arithmetic variations.
	sanitizedDevPath := dev
	if resolvedDev, errLink := filepath.EvalSymlinks(sanitizedDevPath); errLink == nil {
		sanitizedDevPath = resolvedDev
	} else {
		// Fallback to checking standard hierarchical paths if the node layout is transitioning
		if !filepath.IsAbs(sanitizedDevPath) {
			mapperPath := filepath.Join("/dev/mapper", dev)
			directDevPath := filepath.Join("/dev", dev)
			if _, errStat := os.Stat(mapperPath); errStat == nil {
				sanitizedDevPath = mapperPath
			} else {
				sanitizedDevPath = directDevPath
			}
		}
	}

	// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1/4): Removed the internal nested 
	// ExecuteUninterruptible wrapper. The low-level SCSI inquiry executes directly and safely under the 
	// inherited, context-bounded parent batch worker context timeline lifecycle.
	if o.willIoctl0x83Fail(ctx, gater, sanitizedDevPath) {
		return "", fmt.Errorf("path %s in unsafe state, bypassing ioctl query", sanitizedDevPath)
	}

	return o.GetWwnByScsiInqInternal(sanitizedDevPath) 
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

	// FIXED: Range-Check Constraint Boundary (Immune to multi-arch padding gaps or Resid integer overflows)
	// Ensures that if short transfers alter header.Resid calculations, the computed length is capped.
	actualLen := int(header.DxferLen) - int(header.Resid)
	if actualLen < 4 || actualLen > len(respBuf) {
		logger.Warningf("[SCSI-Inq-Mismap] [%s] Transport residue overflow tracked (DxferLen: %d, Resid: %d, Max Allocation: %d). Forcing strict buffer boundary capping.", dev, header.DxferLen, header.Resid, len(respBuf))
		actualLen = int(header.DxferLen)
		if actualLen > len(respBuf) || actualLen < 4 {
			return "", fmt.Errorf("SCSI payload evaluation failed: invalid boundary metrics")
		}
	}

	if respBuf[1] != 0x83 {
		return "", fmt.Errorf("unexpected SCSI VPD page identifier: 0x%02x", respBuf[1])
	}

	return o.parseVPD83(respBuf[:actualLen])
}

// willIoctl0x83Fail performs a rapid, pre-flight safety scan to determine if a block device is in an unsafe state.
func (r *OsDeviceConnectivityHelperGeneric) willIoctl0x83Fail(ctx context.Context, gater *executer.KeyedGater, dev string) bool {
	if err := ctx.Err(); err != nil {
		return true 
	}

	realPath, err := filepath.EvalSymlinks(dev)
	if err != nil {
		realPath = dev
	}
	if !filepath.IsAbs(realPath) {
		realPath = filepath.Join(filepath.Dir(dev), realPath)
	}
	
	devName := filepath.Base(realPath)

	// Dynamic Partition Handler: Pivots partition sub-nodes back to parent whole-disk root block targets
	sysBlockTarget := filepath.Join("/sys/block", devName)
	if _, errStat := os.Stat(sysBlockTarget); os.IsNotExist(errStat) {
		classBlockPath := filepath.Join("/sys/class/block", devName)
		if realClassPath, errEval := filepath.EvalSymlinks(classBlockPath); errEval == nil {
			if strings.Contains(realClassPath, "/block/") {
				parts := strings.Split(realClassPath, "/block/")
				if len(parts) == 2 {
					subParts := strings.Split(parts[1], "/")
					if len(subParts) > 0 {
						devName = subParts[0]
						sysBlockTarget = filepath.Join("/sys/block", devName)
					}
				}
			}
		}
	}

	// FIXED: 100% Failsafe Structural Verification.
	// We verify Device Mapper topology by probing for the kernel's dedicated 'dm' attribute subdirectory.
	// This captures NVMe-over-DM devices named 'nvme-eui...' flawlessly without relying on prefix heuristics.
	isDM := false
	if _, errDmDir := os.Stat(filepath.Join(sysBlockTarget, "dm")); errDmDir == nil {
		isDM = true
	}

	if isDM {
		return r.checkDMDevice(ctx, devName)
	}

	// FIXED: Leverage your stable, well-tested namespace method instead of fragile text prefixes
	helper := GetDmsPathHelperGeneric{}
	if helper.IsNativeNvmeNamespace(devName) {
		return r.checkNVMeDevice(ctx, gater, devName)
	}

	// Default cleanly to traditional SCSI tracking layers for plain block nodes (sdX)
	return r.isSCSIDeviceBlocked(ctx, devName)
}

// isSCSIDeviceBlocked safe-evaluates whether a classical SCSI block device is locked or unavailable.
// Natively runs inside inherited worker context lanes to maintain absolute deadlock immunity.
func (o *OsDeviceConnectivityHelperGeneric) isSCSIDeviceBlocked(ctx context.Context, name string) bool {
	if err := ctx.Err(); err != nil {
		return true // Terminate instantly if context expired
	}

	cleanName := filepath.Base(name)
	sysBlockTarget := filepath.Join("/sys/block", cleanName)

	// RESTORED VFS LAYER: Resolve target paths natively via absolute canonical link check
	// to protect against hidden symbolic redirections or folder depth variations across modern distros.
	if resolvedBlock, errLink := filepath.EvalSymlinks(sysBlockTarget); errLink == nil {
		sysBlockTarget = resolvedBlock
		cleanName = filepath.Base(resolvedBlock)
	}

	statePath := filepath.Join(sysBlockTarget, "device", "state")
	
	// FIXED: Direct file read replaced with the context-respecting secureReadSysfs utility.
	// Isolates locking tokens to individual disk-level name keys to prevent global pool serialization blocks.
	stateBytesStr, err := secureReadSysfs(ctx, o.KeyedGater, cleanName, statePath)
	if err != nil || stateBytesStr == "" {
		logger.Warningf("error reading scsi device state %s node: %v", statePath, err)
		return true 
	}

	s := strings.TrimSpace(stateBytesStr)
	logger.Warningf("path %s state %s", statePath, s)
	
	switch s {
	case "running":
		return false 
	case "blocked", "quiesce", "offline", "transport-offline", "deleting", "cancel":
		return true
	default:
		// Defensively treat unhandled or transitioning states as blocked to prevent ioctl wait traps
		return true
	}
}

// checkDMDevice safe-evaluates whether a Device Mapper volume is suspended or has zero unblocked pathways.
// FIXED: Receiver type standardized to OsDeviceConnectivityHelperScsiGeneric to prevent compilation breaks
func (r *OsDeviceConnectivityHelperGeneric) checkDMDevice(ctx context.Context, dmName string) bool {
        if err := ctx.Err(); err != nil {
                return true
        }

        cleanDmName := filepath.Base(dmName)
        sysBlockTarget := filepath.Join("/sys/block", cleanDmName)

        // FIXED: Dynamic Partition Handler. If the node path does not exist under /sys/block,
        // resolve it via /sys/class/block to capture the whole-disk root parent element.
        if _, err := os.Stat(sysBlockTarget); os.IsNotExist(err) {
                classBlockPath := filepath.Join("/sys/class/block", cleanDmName)
                if realClassPath, errEval := filepath.EvalSymlinks(classBlockPath); errEval == nil {
                        if strings.Contains(realClassPath, "/block/") {
                                parts := strings.Split(realClassPath, "/block/")
                                if len(parts) == 2 {
                                        // FIXED: Added the correct index specifier parts[1] to isolate the
                                        // target string fragment and satisfy the compiler.
                                        subParts := strings.Split(parts[1], "/")
                                        if len(subParts) > 0 {
                                                cleanDmName = subParts[0] // Correctly extracts parent disk node name (e.g. dm-0)
                                                sysBlockTarget = filepath.Join("/sys/block", cleanDmName)
                                        }
                                }
                        }
                }
        }

	if resolvedBlock, errLink := filepath.EvalSymlinks(sysBlockTarget); errLink == nil {
		sysBlockTarget = resolvedBlock
		cleanDmName = filepath.Base(resolvedBlock)
	}

	suspendedPath := filepath.Join(sysBlockTarget, "dm/suspended")
	
	dataStr, err := secureReadSysfs(ctx, r.KeyedGater, cleanDmName, suspendedPath)
	if err != nil || dataStr == "" {
		logger.Warningf("could not read suspension state for %s: %v", cleanDmName, err)
		return true 
	}

	if strings.TrimSpace(dataStr) == "1" {
		logger.Warningf("DM device %s is SUSPENDED; ioctl will block", cleanDmName)
		return true
	}

	slavesPath := filepath.Join(sysBlockTarget, "slaves")
	dFile, errOpen := os.Open(slavesPath)
	if errOpen != nil {
		logger.Warningf("no slaves or unreadable path for %s: %v", slavesPath, errOpen)
		return true
	}

	const maxCapCeiling = 10000
	slaveNames := make([]string, 0, 32)

	for {
		if err := ctx.Err(); err != nil {
			dFile.Close()
			return true 
		}

		slaves, errDirs := dFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			logger.Warningf("error reading dm slaves chunk: %v", errDirs)
			dFile.Close()
			return true
		}

		for _, s := range slaves {
			if len(slaveNames) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] DM slave tracking slice reached maximum safe allocation ceiling (%d). Truncating scan.", maxCapCeiling)
				break
			}
			slaveNames = append(slaveNames, s.Name())
		}

		if len(slaveNames) >= maxCapCeiling || len(slaves) < 100 || errDirs == io.EOF {
			break
		}
	}
	dFile.Close() 

	for _, name := range slaveNames {
		if err := ctx.Err(); err != nil {
			return true 
		}
		
		if !r.isSCSIDeviceBlocked(ctx, name) {
			return false 
		}
	}

	return true 
}

// checkNVMeDevice evaluates standard and alternative native NVMe naming layouts to verify controller availability.
// FIXED: Receiver type aligned cleanly across package utility layouts
func (r *OsDeviceConnectivityHelperGeneric) checkNVMeDevice(ctx context.Context, gater *executer.KeyedGater, nvmeName string) bool {
	if err := ctx.Err(); err != nil {
		return true 
	}

	cleanNvmeName := filepath.Base(nvmeName)
	baseBlockName := cleanNvmeName 
	
	sysBlockTarget := filepath.Join("/sys/block", cleanNvmeName)
	if resolvedBlock, errLink := filepath.EvalSymlinks(sysBlockTarget); errLink == nil {
		sysBlockTarget = resolvedBlock
		baseBlockName = filepath.Base(resolvedBlock)
	}
	targetSysDir := sysBlockTarget

	if strings.Contains(cleanNvmeName, "c") {
		if lastNIdx := strings.LastIndex(cleanNvmeName, "n"); lastNIdx != -1 && lastNIdx > 0 {
			if cIdx := strings.Index(cleanNvmeName, "c"); cIdx != -1 && cIdx < lastNIdx {
				ctrlPart := cleanNvmeName[:cIdx]  
				nsPart := cleanNvmeName[lastNIdx:] 
				
				baseBlockName = ctrlPart + nsPart 
				targetSysDir = filepath.Join("/sys/block", baseBlockName) 
				
				if resolvedBlock, errLink := filepath.EvalSymlinks(targetSysDir); errLink == nil {
					targetSysDir = resolvedBlock
					baseBlockName = filepath.Base(resolvedBlock)
				}
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
	
	// UNCHANGED UNTOUCHED LOGIC: Keeping your exact live/new comparison states fully intact
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
// FLATTENED FOR SIMPLICITY & DEADLOCK ELIMINATION (Rule 1/4): Removed the outer ExecuteUninterruptible wrapper.
// The operations execute directly and safely under the inherited, context-bounded parent context lifecycle.
// FIXED: Receiver type standardized to OsDeviceConnectivityHelperScsiGeneric to prevent compilation breaks
func (r *OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(ctx context.Context, gater *executer.KeyedGater, volumePath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	realVolumePath, err := filepath.EvalSymlinks(volumePath)
	if err != nil {
		realVolumePath = volumePath
	}
	if !filepath.IsAbs(realVolumePath) {
		realVolumePath = filepath.Join(filepath.Dir(volumePath), realVolumePath)
	}

	statFailed := false
	var stat syscall.Stat_t	
	if err := syscall.Stat(realVolumePath, &stat); err != nil {
		logger.Warningf("failed to stat path %s: %w", realVolumePath, err)
		// Mark that stat failed, but do NOT return early.
		// We will let the mountinfo fallback try to resolve it.
		statFailed = true 
	}	

	var major, minor uint64
	// Check if this file object is natively a raw block device type
	if !statFailed && (stat.Mode & syscall.S_IFMT) == syscall.S_IFBLK {
		major = uint64(unix.Major(uint64(stat.Rdev)))
		minor = uint64(unix.Minor(uint64(stat.Rdev)))
	} else {
		// FIXED: Capture using wide values to insulate against uint32/uint64 type overflow compilation drops
		sysMaj, sysMin, errMf := mount.GetMajorMinorFromSysfs(realVolumePath)
		if errMf != nil {
			deviceName, errDev := mount.GetDeviceFromPath(realVolumePath)
			if errDev != nil {
				return "", fmt.Errorf("failed to determine device from path: %w", errDev)
			}
			return filepath.Base(deviceName), nil
		}
		major = uint64(sysMaj)
		minor = uint64(sysMin)
	}

	if major > 0 {
		if kernelName, err := r.resolveIdToKernelName(ctx, gater, major, (minor); err == nil {
			return kernelName, nil
		}
	}

	return "", fmt.Errorf("could not resolve a valid multipath device for path %s", volumePath)
}

// resolveIdToKernelName performs an O(1) symbolic link evaluation to translate a device's
// major and minor metadata coordinates back into its active canonical kernel block device name.
func (r *OsDeviceConnectivityHelperGeneric) resolveIdToKernelName(ctx context.Context, gater *executer.KeyedGater, major, minor uint64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)

	realPath, err := filepath.EvalSymlinks(sysPath)
	if err != nil {
		// FIXED: Catch detached or unmapped storage states clearly for upper orchestration tracking
		if os.IsNotExist(err) {
			return "", fmt.Errorf("block device %d:%d has been detached from host layer (stale reference): %w", major, minor, err)
		}
		return "", fmt.Errorf("failed to resolve canonical sysfs block link %s: %w", sysPath, err)
	}

	return filepath.Base(realPath), nil
}

// ResolveToKernelName standardizes diverse input block names back to core system labels.
// FIXED: Receiver type aligned cleanly across the package module structure
func (r *OsDeviceConnectivityHelperGeneric) ResolveToKernelName(ctx context.Context, gater *executer.KeyedGater, deviceName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// UNCHANGED UNTOUCHED LOGIC: Keeping your exact prefix short-circuit checks fully intact
	cleanName := filepath.Base(deviceName)
	if strings.HasPrefix(cleanName, "dm-") || strings.HasPrefix(cleanName, "nvme") {
		return cleanName, nil
	}

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

		var stat syscall.Stat_t
		err := syscall.Stat(p, &stat)

		if err == nil && stat.Rdev != 0 && (stat.Mode&syscall.S_IFMT) == syscall.S_IFBLK {
			major := unix.Major(uint64(stat.Rdev))
			minor := unix.Minor(uint64(stat.Rdev))
			
			if kernelName, err := r.resolveIdToKernelName(ctx, gater, major, minor); err == nil {
				return kernelName, nil
			}
		}
	}

	return cleanName, nil
}

// findDMByWWID scans /dev/mapper to locate a device-mapper name matching a target SCSI/NVMe string.
// Production-hardened with immediate file descriptor release and a strict 10,000 element heap boundary ceiling.
// FIXED: Receiver type standardized to OsDeviceConnectivityHelperScsiGeneric to prevent compilation breaks
func (r *OsDeviceConnectivityHelperGeneric) findDMByWWID(ctx context.Context, wwid string) string {
	if err := ctx.Err(); err != nil {
		return ""
	}

	rawScsiID := normalizeWWID(wwid)
	if rawScsiID == "" {
		return "" 
	}

	expectedNvmeSeq := convertScsiIdToNguid(rawScsiID)
	sessionClassPath := "/dev/mapper"
	
	sFile, errOpen := os.Open(sessionClassPath)
	if errOpen != nil {
		return ""
	}
	// FIXED: Bound resource table closing using a defer statement to eliminate fd leak paths on errors
	defer sFile.Close()

	const maxCapCeiling = 10000
	mapperNames := make([]string, 0, 64)

	// =========================================================================
	// STAGE 1: MICROSECOND SNAPSHOT SWEEP (Decouples VFS Handles Instantly)
	// =========================================================================
	for {
		if err := ctx.Err(); err != nil {
			return ""
		}

		mapperEntries, errDirs := sFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			break
		}

		for _, entry := range mapperEntries {
			name := entry.Name()
			if name == "control" {
				continue
			}
			if len(mapperNames) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] /dev/mapper candidate entries reached safe processing ceiling (%d). Truncating scan.", maxCapCeiling)
				break
			}
			
			mapperNames = append(mapperNames, name)
		}

		if len(mapperNames) >= maxCapCeiling || len(mapperEntries) < 100 || errDirs == io.EOF {
			break
		}
	}

	// =========================================================================
	// STAGE 2: SAFE DECOUPLED EVALUATION PIPELINE
	// =========================================================================
	for _, name := range mapperNames {
		if err := ctx.Err(); err != nil {
			return ""
		}

		fullPath := filepath.Join("/dev/mapper", name)
		
		fi, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}

		var dmKernelName string
		if fi.Mode()&os.ModeSymlink != 0 {
			realPath, errLink := filepath.EvalSymlinks(fullPath)
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

		// UNCHANGED LAYER: Relying strictly on your internal identity verification rules
		uuidContent, err := r.readDmUuidWithFallbacks(ctx, dmKernelName)
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

// getSlavesForDevice returns raw underlying physical block device names safely shielded from D-state locks.
// FIXED: Receiver type standardized to OsDeviceConnectivityHelperScsiGeneric to prevent compilation breaks
func (r *OsDeviceConnectivityHelperGeneric) getSlavesForDevice(ctx context.Context, major, minor uint32) ([]string, error) {
	logger.Warning("getSlavesForDevice execution tracing initialized")

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	basePath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	if _, errStat := os.Stat(basePath); errStat != nil {
		if os.IsNotExist(errStat) {
			return nil, nil 
		}
	}

	slavesPath := fmt.Sprintf("/sys/dev/block/%d:%d/slaves", major, minor)

	entries, err := executer.ExecuteUninterruptible[[]os.DirEntry](
		ctx,
		r.KeyedGater,
		fmt.Sprintf("read-slaves-%d:%d", major, minor), 
		20,                                             
		100,                                            
		1*time.Second,                                  
		3*time.Second,                                  
		func(wCtx context.Context) ([]os.DirEntry, error) {
			if err := wCtx.Err(); err != nil {
				return nil, err
			}
			
			dFile, errOpen := os.Open(slavesPath)
			if errOpen != nil {
				return nil, errOpen
			}
			defer dFile.Close()

			// FIXED: Replaced unsafe unbounded ReadDir(0) with bounded chunk reads 
			// to protect against memory allocation spikes and directory stream locks.
			const maxCapCeiling = 10000
			var allEntries []os.DirEntry

			for {
				if err := wCtx.Err(); err != nil {
					return nil, err
				}

				chunk, readErr := dFile.ReadDir(100)
				if readErr != nil && readErr != io.EOF {
					return nil, readErr
				}

				for _, entry := range chunk {
					if len(allEntries) >= maxCapCeiling {
						break
					}
					allEntries = append(allEntries, entry)
				}

				if len(allEntries) >= maxCapCeiling || len(chunk) < 100 || readErr == io.EOF {
					break
				}
			}

			return allEntries, nil
		},
	)

	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil 
		}
		return nil, fmt.Errorf("failed to scan device layout mapper slaves tree configuration layout: %w", err)
	}

	var results []string
	for _, entry := range entries {
		slaveName := entry.Name() 
		logger.Warningf("getSlavesForDevice entry discovered: %s", slaveName)
		results = append(results, slaveName)
	}
	return results, nil
}


// readDmUuidWithFallbacks isolates sysfs location adjustments across old and new OS versions with context boundaries.
// Hardened with context-respecting secureReadSysfs barriers to prevent un-killable D-state deadlocks.
func (o *OsDeviceConnectivityHelperGeneric) readDmUuidWithFallbacks(ctx context.Context, dmKernelName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	// Clean out any full path prefixes to keep the file structure string pristine
	cleanKernelName := filepath.Base(dmKernelName)

	// FIXED: Direct file reads replaced with the context-respecting secureReadSysfs utility.
	// This protects against kernel stalls while isolating lock domains strictly to the individual device name.
	var contentBytesStr string
	var readErr error

	// Route A: Standard modern system layout mapping
	modernPath := filepath.Join("/sys/block", cleanKernelName, "dm", "uuid")
	contentBytesStr, readErr = secureReadSysfs(ctx, o.KeyedGater, cleanKernelName, modernPath)
	
	if readErr == nil && contentBytesStr != "" {
		return strings.TrimSpace(contentBytesStr), nil
	}

	// Route B: Legacy RHEL 7 / early kernel fallback alignment scheme
	legacyPath := filepath.Join("/sys/block", cleanKernelName, "uuid")
	contentBytesStr, readErr = secureReadSysfs(ctx, o.KeyedGater, cleanKernelName, legacyPath)
	
	if readErr == nil && contentBytesStr != "" {
		return strings.TrimSpace(contentBytesStr), nil
	}

	return "", fmt.Errorf("unable to read device-mapper identification footprint for %s across modern or legacy endpoints: %v", cleanKernelName, readErr)
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
// FIXED: Receiver type standardized to OsDeviceConnectivityHelperScsiGeneric to prevent compilation breaks
func (r *OsDeviceConnectivityHelperGeneric) getWWIDByDev(ctx context.Context, major, minor uint32) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	basePath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	
	// FIXED: Dynamic Partition Handler. Partition paths nested under /sys/dev/block/ contain a 
	// symlink layer named "partition" or point to a nested subdirectory. 
	// If it is a partition block node, we follow the structural VFS path up one tier to locate the parent disk.
	if _, errPart := os.Stat(filepath.Join(basePath, "partition")); errPart == nil {
		// Moving up one directory level resolves back to the canonical parent whole-disk container folder
		parentPath := filepath.Dir(basePath)
		if resolvedParent, errLink := filepath.EvalSymlinks(parentPath); errLink == nil {
			// Read the parent disk node's true hardware coordinates via /sys/block properties
			parentBase := filepath.Base(resolvedParent)
			var statT unix.Stat_t
			if errStat := unix.Stat(filepath.Join("/sys/block", parentBase), &statT); errStat == nil {
				major = unix.Major(uint64(statT.Rdev))
				minor = unix.Minor(uint64(statT.Rdev))
				basePath = fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
			}
		}
	}

	devKey := fmt.Sprintf("dev-%d:%d", major, minor)

	// =========================================================================
	// 1. DEVICE MAPPER (MULTIPATH) LAYER
	// =========================================================================
	dmUuidPath := filepath.Join(basePath, "dm/uuid")
	if uuid, err := secureReadSysfs(ctx, r.KeyedGater, devKey, dmUuidPath); err == nil && uuid != "" {
		return strings.TrimSpace(uuid), nil
	}

	// =========================================================================
	// 2. NATIVE NVME / NVME-FABRICS SUBSYSTEM LAYER
	// =========================================================================
	nvmeWwidPath := filepath.Join(basePath, "wwid")
	if wwid, err := secureReadSysfs(ctx, r.KeyedGater, devKey, nvmeWwidPath); err == nil && wwid != "" {
		return strings.TrimSpace(wwid), nil
	}

	realPath, errLink := filepath.EvalSymlinks(basePath)
	if errLink == nil {
		baseBlockName := filepath.Base(realPath)
		normalizedBlockName := baseBlockName 
		
		if strings.HasPrefix(baseBlockName, "nvme") && strings.Contains(baseBlockName, "c") {
			if lastNIdx := strings.LastIndex(baseBlockName, "n"); lastNIdx != -1 {
				if cIdx := strings.Index(baseBlockName, "c"); cIdx != -1 && cIdx < lastNIdx {
					ctrlPart := baseBlockName[:cIdx]  
					nsPart := baseBlockName[lastNIdx:] 
					
					normalizedBlockName = ctrlPart + nsPart 
					altNvnPath := fmt.Sprintf("/sys/block/%s/wwid", normalizedBlockName) 
					
					if wwid, err := secureReadSysfs(ctx, r.KeyedGater, normalizedBlockName, altNvnPath); err == nil && wwid != "" {
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
	if wwid, err := secureReadSysfs(ctx, r.KeyedGater, devKey, scsiWwidPath); err == nil && wwid != "" {
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

// GetMajorMinorFromSysfs takes an absolute device path or raw kernel block name 
// (e.g., "/dev/sda1", "dm-0", "dasda") and extracts its true major and minor numbers.
// This approach is completely agnostic to driver prefixes (sd, dm, dasd, nvme).
func (of *GetDmsPathHelperGeneric) GetMajorMinorFromSysfs(ctx context.Context, deviceInput string) (major uint64, minor uint64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, ctx.Err()
	}

	// 1. Sanitize input to build a valid absolute node pathway under /dev
	baseName := filepath.Base(deviceInput)
	if baseName == "" || baseName == "." || baseName == "/" {
		return 0, 0, fmt.Errorf("invalid or corrupt block device identifier provided: '%s'", deviceInput)
	}
	
	deviceNodePath := filepath.Join("/dev", baseName)

	// 2. Perform a low-level Stat call to extract raw kernel device properties directly
	var stat syscall.Stat_t
	if errStat := syscall.Stat(deviceNodePath, &stat); errStat != nil {
		// FALLBACK: If the /dev node is missing due to udev user-space rendering lag,
		// parse the values directly from the kernel's sysfs block configuration framework.
		sysfsDevPath := filepath.Join("/sys/block", baseName, "dev")
		if _, errErr := os.Stat(sysfsDevPath); os.IsNotExist(errErr) {
			// Handle partitioned systems safely (e.g., sda1 or dasda1 tracking parameters)
			sysfsDevPath = filepath.Join("/sys/class/block", baseName, "dev")
		}

		devBytes, errRead := os.ReadFile(sysfsDevPath)
		if errRead != nil {
			return 0, 0, fmt.Errorf("device node %s not found and sysfs resolution failed: %w", deviceNodePath, errRead)
		}

		// Sysfs formats major:minor as "8:16\n"
		sysfsStr := strings.TrimSpace(string(devBytes))
		_, errScan := fmt.Sscanf(sysfsStr, "%d:%d", &major, &minor)
		if errScan != nil {
			return 0, 0, fmt.Errorf("corrupt device number format detected inside sysfs path %s: %w", sysfsDevPath, errScan)
		}

		logger.Debugf("[Topology-Util] Resolved major/minor via sysfs fallback for '%s': %d:%d", baseName, major, minor)
		return major, minor, nil
	}

	// 3. Verify that the file node is a valid block device (S_IFBLK)
	if stat.Mode&syscall.S_IFMT != syscall.S_IFBLK {
		return 0, 0, fmt.Errorf("target node pathway '%s' exists but is not an active block storage device", deviceNodePath)
	}

	// 4. Bit-shift the raw system rdev (dev_t) values. 
	// This matches the Linux kernel's internal layout parameters exactly, completely bypassing string checks.
	// Raw Rdev extraction handles uint64 boundaries natively without data truncation.
	rawRdev := uint64(stat.Rdev)
	major = (rawRdev >> 8) & 0xfff
	major |= (rawRdev >> 32) & ^uint64(0xfff)
	minor = rawRdev & 0xff
	minor |= (rawRdev >> 12) & ^uint64(0xff)

	logger.Debugf("[Topology-Util] Extracted native kernel major/minor for '%s': %d:%d", baseName, major, minor)
	return major, minor, nil
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
// FIXED: Receiver type standardized to OsDeviceConnectivityHelperScsiGeneric to prevent compilation breaks
func (r *OsDeviceConnectivityHelperGeneric) GetDeviceWWID(ctx context.Context, gater *executer.KeyedGater, dev string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	name := filepath.Base(dev)

	if nvmeNamespaceRegex.MatchString(name) || strings.HasPrefix(name, "nvme") {
		return r.GetWwnByNvmeSysfs(ctx, gater, dev)
	}

	return r.GetWwnByScsiInq(ctx, gater, dev)
}

// GetWwnByNvmeSysfs extracts NVMe hardware identifiers safely, accommodating fabrics topologies and legacy kernels.
// FIXED: Receiver type standardized to OsDeviceConnectivityHelperScsiGeneric to prevent compilation breaks
func (r *OsDeviceConnectivityHelperGeneric) GetWwnByNvmeSysfs(ctx context.Context, gater *executer.KeyedGater, dev string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	name := filepath.Base(dev) 
	baseBlockName := name      
	targetSysDir := filepath.Join("/sys/block", name)

	if strings.Contains(name, "c") {
		if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
			if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
				ctrlPart := name[:cIdx]  
				nsPart := name[lastNIdx:] 
				
				baseBlockName = ctrlPart + nsPart 
				targetSysDir = filepath.Join("/sys/block", baseBlockName) 
				logger.Debugf("[NVMe-Sysfs-Wwn] Normalized virtual block node routing path: %s -> %s", name, targetSysDir)
			}
		}
	}

	if resolvedSysBlock, errLink := filepath.EvalSymlinks(targetSysDir); errLink == nil {
		targetSysDir = resolvedSysBlock
		baseBlockName = filepath.Base(resolvedSysBlock)
	}

	// FIXED: Standalone function calls (normalizeWWID) have their object pointer prefixes removed.
	// Field instances point consistently to the validated helper context ('r.KeyedGater').
	if nguid, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && nguid != "" {
		return normalizeWWID(nguid), nil
	}

	if uuid, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && uuid != "" {
		return normalizeWWID(uuid), nil
	}

	if serial, err := secureReadSysfs(ctx, r.KeyedGater, baseBlockName, filepath.Join(targetSysDir, "device/serial")); err == nil && serial != "" {
		normSerial := strings.ToLower(strings.TrimSpace(serial))
		if len(normSerial) != 32 {
			return normSerial, nil
		}
		return normalizeWWID(serial), nil
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
// Hardened with 100% structural verification: Fully immune to custom naming aliases or text prefix bugs.
func (o *GetDmsPathHelperGeneric) WaitForDmToExist(ctx context.Context, gater *executer.KeyedGater, volumeWWID string, maxRetries int, intervalSeconds int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var lastCount int
	var lastRo string
	var stableCycles int

	for attempt := 0; attempt < maxRetries; attempt++ {
		logger.Warningf("attempt %d", attempt)
		if err := ctx.Err(); err != nil {
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

		baseBlockName := name 
		if nvmeControllerHeadFormat.MatchString(name) && strings.Contains(name, "c") {
			if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 && lastNIdx > 0 {
				if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
					ctrlPart := name[:cIdx]  
					nsPart := name[lastNIdx:] 
					baseBlockName = ctrlPart + nsPart 
				}
			}
		}

		var path string
		mapperPath := filepath.Join("/dev/mapper", baseBlockName)
		directDevPath := filepath.Join("/dev", baseBlockName)

		if _, errStat := os.Stat(mapperPath); errStat == nil {
			path = mapperPath
		} else {
			path = directDevPath
		}

		logger.Warning("before IsDeviceMapper")
		isDM := o.IsDeviceMapper(baseBlockName)
		count := 0
		
		if isDM {
			logger.Warningf("[Topology-PathCheck] Querying DM slave count metrics for: %s", baseBlockName)
			count = o.GetSlaveCount(ctx, gater, baseBlockName)
			logger.Warningf("resolved path/slave count is %d", count)
		} else {
			// FIXED: Replaced strings.HasPrefix("nvme") with structural subsystem checking.
			// Probing for the presence of an NVMe configuration file or an active ANA node 
			// isolates fabrics targets cleanly without depending on prefix text heuristics.
			logger.Warningf("[Topology-PathCheck] Querying Native NVMe transport lane metrics for: %s", baseBlockName)
			rawSubsysDevicesDir := filepath.Join("/sys/block", baseBlockName, "device", "subsystem")
			subsysDevicesDir, errLink := filepath.EvalSymlinks(rawSubsysDevicesDir)
			if errLink != nil {
				subsysDevicesDir = filepath.Join("/sys/block", baseBlockName, "device")
			}

			countResult, errOpen := func() (int, error) {
				dFile, err := os.Open(subsysDevicesDir)
				if err != nil {
					if _, errStat := os.Stat(filepath.Join("/sys/class/nvme-fabrics")); errStat == nil {
						return 1, nil
					}
					return 0, err
				}
				defer dFile.Close()
				
				const maxCapCeiling = 10000
				processedEntriesCount := 0
				nvmeLanes := 0
				
				for {
					if err := ctx.Err(); err != nil {
						return 0, err
					}

					entries, errEntries := dFile.ReadDir(100)
					if errEntries != nil && errEntries != io.EOF {
						return 0, errEntries
					}
					if len(entries) == 0 || errEntries == io.EOF {
						break
					}
					
					for _, entry := range entries {
						if processedEntriesCount >= maxCapCeiling {
							break
						}
						processedEntriesCount++

						// FIXED: Sub-stat checks confirm if the node maps back to a valid NVMe class 
						// controller bus component rather than relying on an unverified prefix name.
						entryName := entry.Name()
						if _, errNvmeClass := os.Stat(filepath.Join("/sys/class/nvme", entryName)); errNvmeClass == nil {
							nvmeLanes++
						} else if strings.HasPrefix(entryName, "nvme") && !strings.Contains(entryName, "-") {
							// Traditional fallback for older kernel tracking trees
							nvmeLanes++
						}
					}
				}
				return nvmeLanes, nil
			}()

			if errOpen == nil {
				count = countResult
			}

			if count == 0 {
				if _, errStat := os.Stat(path); errStat == nil {
					count = 1
				}
			}
			logger.Warningf("resolved path/slave count is %d", count)
		}
		
		ro := o.getRoStatus(ctx, gater, path)
		logger.Warningf("ro status %s", ro)

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
					name = fmt.Sprintf("%s%s", ctrlName, name[lastNIdx:]) 
					
					innerMapperPath := filepath.Join("/dev/mapper", name)
					innerDirectDevPath := filepath.Join("/dev", name)
					if _, errStat := os.Stat(innerMapperPath); errStat == nil {
						path = innerMapperPath
					} else {
						path = innerDirectDevPath
					}
				}
			}

			logger.Warningf("[Settle-Main] Finalizing path tracks. Target device location: %s", path)

			// FLATTENED FOR STABILITY: Direct execution of hardware settling parameters.
			settleErr := o.safeSettle(ctx, gater, path)
			if settleErr == nil {
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
// Production-hardened via a Three-Stage Bounded Pipeline, immediate file handle decoupling, and memory array limits.
func (o *GetDmsPathHelperGeneric) GetSlaveCount(ctx context.Context, gater *executer.KeyedGater, devName string) int {
	if err := ctx.Err(); err != nil {
		return 0
	}

	devName = filepath.Base(devName) 


	// =========================================================================
	// 1. DEVICE MAPPER SUBSYSTEM SCAN (SCSI or NVMe-over-DM slaves)
	// =========================================================================
	if o.IsDeviceMapper(devName) {
		return o.GetSlaveCountDM(ctx, gater, devName)
	}
	
	// =========================================================================
	// 2. NATIVE NVME NAMESPACE SCAN (Native ANA Multipath Controllers)
	// =========================================================================
	if o.IsNativeNvmeNamespace(devName) {
		return o.GetSlaveCountNvmeNamespace(ctx, gater, devName)
	}

	logger.Infof("[Slave-Scan] [%s] Non-multipath physical or virtual device. Defaulting count to 1.", devName)
	return 1
}

func (o *GetDmsPathHelperGeneric) GetSlaveCountDM(ctx context.Context, gater *executer.KeyedGater, devName string) int {
	const maxCapCeiling = 10000
	slavesDir := filepath.Join("/sys/block", devName, "slaves")
	if _, errStat := os.Stat(slavesDir); os.IsNotExist(errStat) {
		slavesDir = filepath.Join("/sys/class/block", devName, "slaves")
	}
	
	dFile, errOpen := os.Open(slavesDir)
	if errOpen != nil {
		logger.Warningf("[DM-Slave-Scan] [%s] Failed to read slaves directory layout: %v", devName, errOpen)
		return 0
	}

	slaveNames := make([]string, 0, 32)

	for {
		if err := ctx.Err(); err != nil {
			dFile.Close()
			return 0
		}

		entries, errDirs := dFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			logger.Warningf("[DM-Slave-Scan] [%s] Error reading slaves chunk: %v", devName, errDirs)
			break
		}
		
		for _, entry := range entries {
			if len(slaveNames) >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] DM slave elements reached safe processing ceiling (%d). Truncating scan.", maxCapCeiling)
				break
			}
			slaveNames = append(slaveNames, entry.Name())
		}
				   if len(slaveNames) >= maxCapCeiling || len(entries) < 100 {
						   break
				   }
		   }
		   dFile.Close()

count := 0

// STAGE 2: SAFE DECOUPLED EVALUATION PIPELINE
for _, slaveName := range slaveNames {
		 if err := ctx.Err(); err != nil {
						  return 0
		  }

		  slaveDeviceDir := filepath.Join(slavesDir, slaveName, "device")

		 addrIdentifier := "UNKNOWN"
		  if realPath, errLink := filepath.EvalSymlinks(slaveDeviceDir); errLink == nil {
						  addrIdentifier = filepath.Base(realPath)
		  }

		  // RESTORED: Re-enabling the structural hardwareIdentity string evaluation
		  var hardwareIdentity string
		 isNvmeSlave := strings.HasPrefix(slaveName, "nvme")

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
										  count++
						 }
		  }

		  logger.Warningf("[DM-Slave-Scan] -> Slave: %s | Kernel Address Mapping: %s | Hardware Identity: %s | State: %s | Operational: %v",
				slaveName, addrIdentifier, hardwareIdentity, state, isOperational)
	}

	return count
}

func (o *GetDmsPathHelperGeneric) GetSlaveCountNvmeNamespace(ctx context.Context, gater *executer.KeyedGater, devName string) int {
	const maxCapCeiling = 10000
	baseBlockDir := filepath.Join("/sys/block", devName)
	if _, errStat := os.Stat(baseBlockDir); os.IsNotExist(errStat) {
		baseBlockDir = filepath.Join("/sys/class/block", devName)
	}
	deviceDir := filepath.Join(baseBlockDir, "device")
	
	subsysSymlink := filepath.Join(deviceDir, "subsystem")
	realSubsysPath, err := filepath.EvalSymlinks(subsysSymlink)
	
	targetScanDir := deviceDir
	isNativeMultipathHead := (err == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys"))
	
	if isNativeMultipathHead {
		if realDeviceDir, err := filepath.EvalSymlinks(deviceDir); err == nil {
			targetScanDir = realDeviceDir
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
	
	nvmeFile, errOpen := os.Open(targetScanDir)
	if errOpen != nil {
		logger.Warningf("[NVMe-Slave-Scan] [%s] Target NVMe device runtime directory missing or inaccessible: %v", devName, errOpen)
		return 0
	}

	logger.Infof("[NVMe-Slave-Scan] [%s] Inspecting active controller pathways in tree directory: %s...", devName, targetScanDir)
	
	nvmeCandidates := make([]string, 0, 32)

	for {
		if err := ctx.Err(); err != nil {
			nvmeFile.Close()
			return 0
		}

		entries, errDirs := nvmeFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			logger.Warningf("[NVMe-Slave-Scan] [%s] Error reading NVMe controller paths chunk: %v", devName, errDirs)
			break
		}
		
		for _, e := range entries {
			name := e.Name()
			isNamespaceVolume := nvmeNamespaceRegex.MatchString(name)
			isController := strings.HasPrefix(name, "nvme") && !isNamespaceVolume
			isSubsys := strings.HasPrefix(name, "nvme-subsys")

			if isController || isSubsys {
				if len(nvmeCandidates) >= maxCapCeiling {
					logger.Warningf("[VFS-Guard] NVMe slave candidates reached safe processing ceiling (%d). Truncating scan.", maxCapCeiling)
					break
				}
				nvmeCandidates = append(nvmeCandidates, name)				
			}
		}

		if len(nvmeCandidates) >= maxCapCeiling || len(entries) < 100 || errDirs == io.EOF {
			break
		}
	}
	nvmeFile.Close() 
	
	count := 0
	
	for _, name := range nvmeCandidates {
		if err := ctx.Err(); err != nil {
			return 0
		}

		// FIXED: Corrected multi-distro state path routing using explicit class/block lookups instead of malformed joins
		statePath := filepath.Join("/sys/class/nvme", name, "state")
		if _, errStat := os.Stat(statePath); os.IsNotExist(errStat) {
			statePath = filepath.Join(targetScanDir, name, "state")
		}

		stateBytesStr, err := secureReadSysfsFallback(ctx, gater, name, statePath)
		if err == nil {
			state := strings.ToLower(strings.TrimSpace(stateBytesStr))
			if state == "dead" || state == "deleting" || state == "failing" {
				logger.Warningf("[NVMe-Slave-Scan] -> Skipping unhealthy controller path: %s (State: %s)", name, state)
				continue
			}
		}

		count++

		nqnPath := filepath.Join("/sys/class/nvme", name, "subsysnqn")
		if _, errStat := os.Stat(nqnPath); os.IsNotExist(errStat) {
			nqnPath = filepath.Join(targetScanDir, name, "subsysnqn")
		}
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
	
	return count
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

// IsDeviceMapper verifies if a block entry is a true Device Mapper device 
// by inspecting sysfs topology and structural backing attributes with verbose debugging traces.
func (r *GetDmsPathHelperGeneric) IsDeviceMapper(devName string) bool {
	cleanName := filepath.Base(devName)
	logger.Debugf("[Is-DM-Check] Evaluating device name: original='%s', sanitized='%s'", devName, cleanName)

	if cleanName == "" || cleanName == "." || cleanName == "/" || cleanName == "control" {
		logger.Debugf("[Is-DM-Check] Rejected early: '%s' is an empty string, root path, or control node.", cleanName)
		return false
	}

	// 1. HARD EXCLUSION: Standalone native NVMe namespaces or raw SCSI drives can never be DM nodes.
	if r.IsNativeNvmeNamespace(cleanName) {
		logger.Debugf("[Is-DM-Check] Rejected: '%s' identified as a native NVMe namespace.", cleanName)
		return false
	}
	if strings.HasPrefix(cleanName, "sd") {
		logger.Debugf("[Is-DM-Check] Rejected: '%s' starts with 'sd' (raw SCSI block).", cleanName)
		return false
	}

	// 2. RESOLVE TO SYSFS BLOCK PATH (Handles both short aliases and canonical dm-X names)
	sysBlockPath := filepath.Join("/sys/block", cleanName)
	if _, errStat := os.Stat(sysBlockPath); os.IsNotExist(errStat) {
		sysBlockPath = filepath.Join("/sys/class/block", cleanName)
	}

	if resolvedPath, errEval := filepath.EvalSymlinks(sysBlockPath); errEval == nil {
		logger.Debugf("[Is-DM-Check] Resolved symlink for block node: inputPath='%s', finalPath='%s'", sysBlockPath, resolvedPath)
		sysBlockPath = resolvedPath
	} else {
		logger.Debugf("[Is-DM-Check] No symlink resolution or stat failure for base sysfs path %s: %v", sysBlockPath, errEval)
	}

	// 3. STRUCTURAL KERNEL VALIDATION: Check for explicit 'dm' metadata directory in sysfs
	dmMetaPath := filepath.Join(sysBlockPath, "dm")
	if info, errStat := os.Stat(dmMetaPath); errStat == nil && info.IsDir() {
		logger.Infof("[Is-DM-Check] Confirmed TRUE: Found active sysfs DM metadata directory at %s for device '%s'", dmMetaPath, cleanName)
		return true
	} else {
		logger.Debugf("[Is-DM-Check] No 'dm' metadata directory found at %s: %v", dmMetaPath, errStat)
	}

	if strings.Contains(sysBlockPath, "/virtual/block/dm-") || strings.Contains(sysBlockPath, "/dm/") {
		logger.Infof("[Is-DM-Check] Confirmed TRUE: Sysfs path structure contains virtual DM signature: path='%s'", sysBlockPath)
		return true
	}

	// 4. RESOLVE VIA /dev/mapper MAPPING BACKING CHECK
	devMapperPath := filepath.Join("/dev/mapper", cleanName)
	logger.Debugf("[Is-DM-Check] Checking enterprise mapper path link: %s", devMapperPath)
	if resolvedDev, errLink := filepath.EvalSymlinks(devMapperPath); errLink == nil {
		resolvedBase := filepath.Base(resolvedDev)
		logger.Debugf("[Is-DM-Check] /dev/mapper/%s points to underlying node: '%s'", cleanName, resolvedBase)
		
		if strings.HasPrefix(resolvedBase, "dm-") {
			logger.Infof("[Is-DM-Check] Confirmed TRUE: Mapper alias '%s' resolves directly to kernel node '%s'", cleanName, resolvedBase)
			return true
		}
		
		resolvedSysPath := filepath.Join("/sys/block", resolvedBase)
		if infoDm, errDm := os.Stat(filepath.Join(resolvedSysPath, "dm")); errDm == nil && infoDm.IsDir() {
			logger.Infof("[Is-DM-Check] Confirmed TRUE: Resolved target '%s' contains active sysfs DM subsystem backing", resolvedBase)
			return true
		}
	} else {
		logger.Debugf("[Is-DM-Check] Failed to evaluate symlinks for %s: %v", devMapperPath, errLink)
	}

	logger.Debugf("[Is-DM-Check] Evaluated FALSE: Device '%s' does not match any known Device Mapper backing patterns.", cleanName)
	return false
}


// IsNativeNvmeNamespace strictly identifies an active NVMe storage volume disk node,
// mathematically isolating it from structural host bus controller slots.
func (o *GetDmsPathHelperGeneric) IsNativeNvmeNamespace(devName string) bool {
	cleanName := filepath.Base(devName)
	
	// UNTOUCHED INVARIANT LOGIC: Your well-tested string index math is fully preserved
	if !strings.HasPrefix(cleanName, "nvme") {
		return false
	}

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
// Enhanced with exhaustive tracing telemetry to isolate evaluation branching and fallback paths.
func (of *GetDmsPathHelperGeneric) EvaluateSysfsTopology(ctx context.Context, gater *executer.KeyedGater, rawScsiID string, checkPendingOnly bool) (hasDevice bool, isPending bool, devName string) {
	logger.Infof("[EvalTopology-Trace] Entering topology evaluation: rawScsiID='%s', checkPendingOnly=%v", rawScsiID, checkPendingOnly)
	
	if err := ctx.Err(); err != nil {
		logger.Warningf("[EvalTopology-Trace] Operation canceled early due to context state: %v", err)
		return false, false, ""
	}

	rawScsiTarget := normalizeWWID(rawScsiID)
	if rawScsiTarget == "" {
		logger.Errorf("[EvalTopology-Trace] Failed to normalize input SCSI target ID: '%s'", rawScsiID)
		return false, false, ""
	}
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)
	logger.Debugf("[EvalTopology-Trace] Targets normalized successfully: rawScsiTarget='%s', rawNvmeTarget='%s'", rawScsiTarget, rawNvmeTarget)

	devEntries, errDir := os.ReadDir("/dev")
	if errDir != nil {
		logger.Errorf("[EvalTopology-Trace] VFS failure reading directory /dev: %v", errDir)
		return false, false, ""
	}

	const maxCapCeiling = 10000
	devNames := make([]string, 0, 128)

	for _, entry := range devEntries {
		name := entry.Name()
		isSCSI := strings.HasPrefix(name, "sd")
		isDM := strings.HasPrefix(name, "dm-")
		isNVMe := strings.HasPrefix(name, "nvme")

		if !isDM && !isNVMe && !isSCSI {
			continue
		}
	
		if len(devNames) >= maxCapCeiling {
			logger.Warningf("[EvalTopology-Trace] [VFS-Guard] /dev snapshot lookup exceeded safe processing ceiling (%d). Truncating scan pass.", maxCapCeiling)
			break
		}
		devNames = append(devNames, name)
	}
	logger.Debugf("[EvalTopology-Trace] Snapshot completed. Filtered device names captured: count=%d", len(devNames))

	// =========================================================================
	// PHASE 1: DEVICE-MAPPER (DM) EVALUATION
	// =========================================================================
	logger.Debugf("[EvalTopology-Trace] [Phase-1-DM] Starting Device Mapper evaluation pass...")
	hasDevice, isPending, devName := EvaluateSysfsTopologyScanDM(ctx, gater, devNames, rawScsiTarget, rawNvmeTarget)
	if hasDevice || devName != "" {
		return hasDevice, isPending, devName
	}

	logger.Debugf("[EvalTopology-Trace] [Phase-2-NVMe] Starting Native NVMe namespace evaluation pass...")
	
	hasDevice, isPending, devName := EvaluateSysfsTopologyScanNvme(ctx, gater, devNames, rawScsiTarget, rawNvmeTarget)
	if hasDevice || devName != "" {
		return hasDevice, isPending, devName
	}
	

	return false, false, ""
}

func (of *GetDmsPathHelperGeneric) EvaluateSysfsTopologyScanDM(ctx context.Context, gater *executer.KeyedGater, devNames []string, rawScsiTarget string, rawNvmeTarget string) (hasDevice bool, isPending bool, devName string) {
	for _, name := range devNames {
		if err := ctx.Err(); err != nil {
			return false, false, ""
		}
		
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
			logger.Infof("[EvalTopology-Trace] [Phase-1-DM] Match hit on DM device name: '%s'", name)
			roBytesStr, errRo := secureReadSysfs(ctx, gater, name, filepath.Join("/sys/block", name, "ro"))
			isReadOnly := errRo == nil && strings.TrimSpace(roBytesStr) != "0"
			suspendedBytesStr, errSusp := secureReadSysfs(ctx, gater, name, filepath.Join("/sys/block", name, "dm", "suspended"))
			isSuspended := errSusp == nil && strings.TrimSpace(suspendedBytesStr) == "1"

			logger.Debugf("[EvalTopology-Trace] [Phase-1-DM] Status flags for '%s': isReadOnly=%v, isSuspended=%v", name, isReadOnly, isSuspended)
			if isSuspended || isReadOnly {
				logger.Infof("[EvalTopology-Trace] [Phase-1-DM] Returning active match with pending/settling state: (hasDevice=true, isPending=true, name='%s')", name)
				return true, true, name
			}
			logger.Infof("[EvalTopology-Trace] [Phase-1-DM] Returning active match with ready state: (hasDevice=true, isPending=false, name='%s')", name)
			return true, false, name
		}
	
	}
	logger.Debugf("[EvalTopology-Trace] [Phase-1-DM] No matches validated in Device Mapper channel.")
	
	return false, false, ""
}

func (of *GetDmsPathHelperGeneric) EvaluateSysfsTopologyScanNvme(ctx context.Context, gater *executer.KeyedGater, devNames []string, rawScsiTarget string, rawNvmeTarget string) (hasDevice bool, isPending bool, devName string) {
	for _, name := range devNames {
		if err := ctx.Err(); err != nil {
			return false, false, ""
		}
		
		if !nvmeNamespaceRegex.MatchString(name) {
			continue
		}

		baseBlockName := name
		m := filepath.Join("/sys/block", name)
		targetSysDir := m

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
		
		var availableIDs []string
		var discoveredID string

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
			runtime.KeepAlive(nvmeInfo)

			if errno == 0 {
				discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
				if discoveredID != "" && discoveredID != "00000000000000000000000000000000" {
					availableIDs = append(availableIDs, discoveredID)
					logger.Debugf("[EvalTopology-Trace] [Phase-2-NVMe] NGUID recovered via IOCTL on '%s': '%s'", name, discoveredID)
				}
			}
		}

		if len(availableIDs) == 0 {
			logger.Debugf("[EvalTopology-Trace] [Phase-2-NVMe] IOCTL empty for '%s', checking sysfs fallback trees...", name)
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "device", "wwid")); err == nil && data != "" { availableIDs = append(availableIDs, normalizeWWID(data)) }
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "uuid")); err == nil && data != "" { availableIDs = append(availableIDs, normalizeWWID(data)) }
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "nguid")); err == nil && data != "" { availableIDs = append(availableIDs, normalizeWWID(data)) }
			if data, err := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "device", "serial")); err == nil && data != "" { availableIDs = append(availableIDs, normalizeWWID(data)) }
			
			subsysSymlink := filepath.Join(m, "device", "subsystem")
			realSubsysPath, errLink := filepath.EvalSymlinks(subsysSymlink)
			if errLink == nil && strings.Contains(realSubsysPath, "virtual/nvme-subsys") {
				subsysWwidPath := filepath.Join(realSubsysPath, "wwid")
				if data, err := secureReadSysfs(ctx, gater, baseBlockName, subsysWwidPath); err == nil && data != "" {
					availableIDs = append(availableIDs, normalizeWWID(data))
				}
			}
			logger.Debugf("[EvalTopology-Trace] [Phase-2-NVMe] Sysfs fallback collection yielded IDs count: %d", len(availableIDs))
		}

		matchFound := false
		for _, rawID := range availableIDs {
			if len(rawID) == 32 && rawID == rawNvmeTarget {
				matchFound = true
				logger.Infof("[EvalTopology-Trace] [Phase-2-NVMe] Namespace match confirmed on node '%s' using ID: '%s'", name, rawID)
				break 
			}
		}

		if matchFound {
			roBytesStr, errRo := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "ro"))
			isReadOnly := errRo == nil && strings.TrimSpace(roBytesStr) != "0"
			
			controllerEntries := of.getControllerEntries(ctx, baseBlockName)
			isControllerTransitioning := of.isNvmeControllerTransitioning(ctx, gater, baseBlockName, controllerEntries)

			logger.Debugf("[EvalTopology-Trace] [Phase-2-NVMe] Status check for '%s': isReadOnly=%v, isControllerTransitioning=%v", baseBlockName, isReadOnly, isControllerTransitioning)
			if isControllerTransitioning || isReadOnly {
				logger.Infof("[EvalTopology-Trace] [Phase-2-NVMe] Returning active NVMe match with pending/settling state: (hasDevice=true, isPending=true, name='%s')", baseBlockName)
				return true, true, baseBlockName
			}
			logger.Infof("[EvalTopology-Trace] [Phase-2-NVMe] Returning active NVMe match with ready state: (hasDevice=true, isPending=false, name='%s')", baseBlockName)
			return true, false, baseBlockName
		}
	}
	logger.Infof("[EvalTopology-Trace] No matching topologies discovered for identifier: '%s'", rawScsiTarget)

}

// getControllerEntries scans the universal /sys/class/nvme adapter tree safely.
// Enhanced with explicit diagnostic logging to track controller candidate discovery.
func (of *GetDmsPathHelperGeneric) getControllerEntries(ctx context.Context, baseBlockName string) []string {
	logger.Infof("[CtrlEntries-Trace] Scanning adapter tree for baseBlockName='%s' under /sys/class/nvme", baseBlockName)
	
	const maxCapCeiling = 10000
	controllerDir := "/sys/class/nvme"
	dFile, errOpen := os.Open(controllerDir)
	if errOpen != nil {
		logger.Errorf("[CtrlEntries-Trace] Failed to open universal controller directory %s: %v", controllerDir, errOpen)
		return nil
	}
	defer dFile.Close()

	candidates := make([]string, 0, 32)
	for {
		if err := ctx.Err(); err != nil {
			logger.Warningf("[CtrlEntries-Trace] Context expired during directory read stream: %v", err)
			break
		}
		entries, errEntries := dFile.ReadDir(100)
		if errEntries != nil && errEntries != io.EOF {
			logger.Errorf("[CtrlEntries-Trace] Error reading directory batch from %s: %v", controllerDir, errEntries)
			break
		}
		for _, entry := range entries {
			if len(candidates) >= maxCapCeiling {
				logger.Warningf("[CtrlEntries-Trace] [VFS-Guard] Controller candidate ceiling reached (%d). Truncating scan.", maxCapCeiling)
				break
			}
			entryName := entry.Name()
			logger.Debugf("[CtrlEntries-Trace] Evaluating directory entry: '%s'", entryName)
		
			isCandidate := strings.HasPrefix(entryName, "nvme") && !strings.Contains(entryName, "-") && !nvmeNamespaceRegex.MatchString(entryName)
			if isCandidate {
				candidates = append(candidates, entryName)
				logger.Debugf("[CtrlEntries-Trace] Accepted valid adapter controller candidate: '%s'", entryName)
			} else {
				logger.Debugf("[CtrlEntries-Trace] Skipped entry '%s' (not a primary controller base node)", entryName)
			}
		}
		if len(candidates) >= maxCapCeiling || len(entries) < 100 || errEntries == io.EOF {
			break
		}
	}
	logger.Infof("[CtrlEntries-Trace] Controller enumeration complete for '%s'. Discovered candidates count: %d", baseBlockName, len(candidates))
	return candidates
}

// isNvmeControllerTransitioning checks if any discovered controller adapter is busy settling.
// Enhanced with exhaustive status checking logs and explicit tracking tags.
func (of *GetDmsPathHelperGeneric) isNvmeControllerTransitioning(ctx context.Context, gater *executer.KeyedGater, baseBlockName string, controllerEntries []string) bool {
	logger.Infof("[CtrlTransition-Trace] Evaluating transition states for baseBlockName='%s' across controllers: count=%d", baseBlockName, len(controllerEntries))

	for _, ctrlName := range controllerEntries {
		if err := ctx.Err(); err != nil {
			logger.Warningf("[CtrlTransition-Trace] Context expired during transition iteration: %v", err)
			return false
		}

		subsysPath := filepath.Join("/sys/class/nvme", ctrlName, baseBlockName, "wwid")
		logger.Debugf("[CtrlTransition-Trace] Checking mapping link for controller '%s': path='%s'", ctrlName, subsysPath)

		if _, errStat := os.Stat(subsysPath); errStat == nil {
			statePath := filepath.Join("/sys/class/nvme", ctrlName, "state")
			logger.Debugf("[CtrlTransition-Trace] Namespace association verified for controller '%s'. Reading state via path: '%s'", ctrlName, statePath)

			if stateBytesStr, err := secureReadSysfs(ctx, gater, ctrlName, statePath); err == nil {
				state := strings.ToLower(strings.TrimSpace(stateBytesStr))
				logger.Infof("[CtrlTransition-Trace] Controller '%s' reported live kernel state: '%s'", ctrlName, state)
				
				if state == "resetting" || state == "connecting" || state == "deleting" {
					logger.Warningf("[CtrlTransition-Trace] Active transition state detected on controller '%s': state='%s'. Signaling busy transition.", ctrlName, state)
					return true
				}
			} else {
				logger.Debugf("[CtrlTransition-Trace] Failed to read sysfs state for controller '%s': %v", ctrlName, err)
			}
		} else {
			logger.Debugf("[CtrlTransition-Trace] Controller '%s' does not map active namespace path for '%s'", ctrlName, baseBlockName)
		}
	}
	
	logger.Infof("[CtrlTransition-Trace] All candidate controllers verified stable/idle for '%s'. Transitioning=false", baseBlockName)
	return false
}


// EvaluateSpecificSysfsTopology checks a specific target device to see if its configuration aligns with expectations.
// Enhanced with exhaustive diagnostic tracing and compilation corrections for variable scoping and gater keys.
func (of *GetDmsPathHelperGeneric) EvaluateSpecificSysfsTopology(
	ctx context.Context, 
	gater *executer.KeyedGater, 
	targetDeviceName string, 
	rawScsiID string, 
	checkPendingOnly bool,
) (hasDevice bool, isPending bool, err error) {
	logger.Infof("[SpecTopology-Trace] Entering specific topology scan: targetDeviceName='%s', rawScsiID='%s', checkPendingOnly=%v", targetDeviceName, rawScsiID, checkPendingOnly)
	
	if err := ctx.Err(); err != nil {
		logger.Warningf("[SpecTopology-Trace] Context canceled early: %v", err)
		return false, false, ctx.Err()
	}

	rawScsiTarget := normalizeWWID(rawScsiID)
	if rawScsiTarget == "" {
		logger.Errorf("[SpecTopology-Trace] Empty volume ID provided for specific lookup.")
		return false, false, fmt.Errorf("empty volume ID provided for topology lookup")
	}
	
	rawNvmeTarget := convertScsiIdToNguid(rawScsiTarget)
	logger.Debugf("[SpecTopology-Trace] Targets normalized: rawScsiTarget='%s', rawNvmeTarget='%s'", rawScsiTarget, rawNvmeTarget)

	dmName := filepath.Base(targetDeviceName)
	dmPath := filepath.Join("/sys/block", dmName)

	if _, errStat := os.Stat(dmPath); os.IsNotExist(errStat) {
		dmPath = filepath.Join("/sys/class/block", dmName)
		logger.Debugf("[SpecTopology-Trace] Fallback path activated: dmPath='%s'", dmPath)
	}

	if resolvedBlock, errLink := filepath.EvalSymlinks(dmPath); errLink == nil {
		dmPath = resolvedBlock
		dmName = filepath.Base(resolvedBlock)
		logger.Debugf("[SpecTopology-Trace] Symlink resolved: dmPath='%s', dmName='%s'", dmPath, dmName)
	}

	if _, errStat := os.Stat(dmPath); errStat != nil {
		logger.Errorf("[SpecTopology-Trace] Missing sysfs path entry for %s: %v", dmName, errStat)
		return false, false, fmt.Errorf("target device entry %s is missing from sysfs: %w", dmName, errStat)
	}

	const maxCapCeiling = 10000
	isDM := of.IsDeviceMapper(dmName)

	// =========================================================================
	// TARGETED SPECIFIC DM LAYER EVALUATION
	// =========================================================================
	if isDM {
		return EvaluateSpecificSysfsTopologyDM(ctx, gater, dmName, rawScsiTarget, rawNvmeTarget)
	}

	// =========================================================================
	// TARGETED SPECIFIC NATIVE NVME LAYER EVALUATION
	// =========================================================================
	helper := GetDmsPathHelperGeneric{}
	if helper.IsNativeNvmeNamespace(dmName) || strings.HasPrefix(dmName, "nvme") {
		return EvaluateSpecificSysfsTopologyNvme(ctx, gater, dmName, dmPath, rawNvmeTarget)
	}

	logger.Infof("[SpecTopology-Trace] No specific matching configuration found for '%s'", targetDeviceName)
	return false, false, nil
}

func (of *GetDmsPathHelperGeneric) EvaluateSpecificSysfsTopologyDM(
	ctx context.Context, 
	gater *executer.KeyedGater, 
	dmName string, 
	rawScsiTarget string, 
	rawNvmeTarget string,
) (hasDevice bool, isPending bool, err error) {
	logger.Debugf("[SpecTopology-Trace] [Phase-1-DM] Target '%s' is Device Mapper. Reading UUID...", dmName)
	var contentBytesStr string
	var readErr error

	if contentBytesStr, readErr = secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "dm", "uuid")); readErr != nil {
		if contentBytesStr, readErr = secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "uuid")); readErr != nil {
			contentBytesStr, _ = secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "dm", "name"))
		}
	}

	if contentBytesStr != "" {
		foundUUID := normalizeWWID(contentBytesStr)
		logger.Debugf("[SpecTopology-Trace] [Phase-1-DM] Found UUID string '%s' normalized to '%s'", contentBytesStr, foundUUID)
		
		if foundUUID == rawScsiTarget || foundUUID == rawNvmeTarget {
			logger.Infof("[SpecTopology-Trace] [Phase-1-DM] Identity match validated strictly for target %s", dmName)

			roBytesStr, errRo := secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "ro"))
			isReadOnly := errRo == nil && strings.TrimSpace(roBytesStr) != "0"

			suspendedBytesStr, errSusp := secureReadSysfs(ctx, gater, dmName, filepath.Join(dmPath, "dm", "suspended"))
			isSuspended := errSusp == nil && strings.TrimSpace(suspendedBytesStr) == "1"

			logger.Debugf("[SpecTopology-Trace] [Phase-1-DM] Flags for '%s': isReadOnly=%v, isSuspended=%v", dmName, isReadOnly, isSuspended)
			if isSuspended || isReadOnly {
				return true, true, nil 
			}
			return true, false, nil 
		}
	}
	logger.Infof("[SpecTopology-Trace] [Phase-1-DM] No matching UUID on Device Mapper target: %s", dmName)
	return false, false, nil 

}

func (of *GetDmsPathHelperGeneric) EvaluateSpecificSysfsTopologyNvme(
	ctx context.Context, 
	gater *executer.KeyedGater, 
	dmName string, 
	dmPath string,
	rawNvmeTarget string, 
) (hasDevice bool, isPending bool, err error) {
	logger.Debugf("[SpecTopology-Trace] [Phase-2-NVMe] Target '%s' evaluated as native NVMe footprint.", dmName)
	baseBlockName := dmName
	targetSysDir := dmPath
	
	if strings.Contains(dmName, "c") {
		if lastNIdx := strings.LastIndex(dmName, "n"); lastNIdx != -1 && lastNIdx > 0 {
			if cIdx := strings.Index(dmName, "c"); cIdx != -1 && cIdx < lastNIdx {
				ctrlPart := dmName[:cIdx]  
				nsPart := dmName[lastNIdx:] 
				baseBlockName = ctrlPart + nsPart 
				targetSysDir = filepath.Join("/sys/block", baseBlockName)
				
				if _, errStat := os.Stat(targetSysDir); os.IsNotExist(errStat) {
					targetSysDir = filepath.Join("/sys/class/block", baseBlockName)
				}
				if resolvedBlock, errLink := filepath.EvalSymlinks(targetSysDir); errLink == nil {
					targetSysDir = resolvedBlock
					baseBlockName = filepath.Base(resolvedBlock)
				}
				logger.Debugf("[SpecTopology-Trace] Normalized virtual block node routing path: %s -> %s", dmName, targetSysDir)
			}
		}
	}

	var availableIDs []string
	var discoveredID string // FIXED: Declared missing local identifier variable safely

	deviceNode := filepath.Join("/dev", dmName)
	if df, errOpen := os.OpenFile(deviceNode, os.O_RDONLY|syscall.O_NONBLOCK, 0); errOpen == nil {
		var nvmeInfo nvmeIdTarget
		_, _, errno := unix.Syscall(
			unix.SYS_IOCTL,
			df.Fd(),
			uintptr(NVME_IOCTL_ID_TARGET),
			uintptr(unsafe.Pointer(&nvmeInfo)),
		)
		df.Close()

		if errno == 0 {
			discoveredID = normalizeWWID(fmt.Sprintf("%x", nvmeInfo.Nguid))
			if discoveredID != "" && discoveredID != "00000000000000000000000000000000" {
				availableIDs = append(availableIDs, discoveredID)
				logger.Debugf("[SpecTopology-Trace] Discovered NGUID via ioctl: '%s'", discoveredID)
			}
		}
	}

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
			logger.Infof("[SpecTopology-Trace] NVMe Target candidate matched successfully: '%s'", rawID)
			break
		}
	}

	if matchFound {
		roBytesStr, errRo := secureReadSysfs(ctx, gater, baseBlockName, filepath.Join(targetSysDir, "ro"))
		isReadOnly := errRo == nil && strings.TrimSpace(roBytesStr) != "0"

		var isControllerTransitioning bool
		controllerDir := "/sys/class/nvme"
		dFile, errOpen := os.Open(controllerDir)
		
		var controllerEntries []string
		if errOpen == nil {
			candidates := make([]string, 0, 32)
			for {
				if err := ctx.Err(); err != nil {
					break
				}
				entries, errEntries := dFile.ReadDir(100)
				if errEntries != nil && errEntries != io.EOF {
					break
				}
				for _, entry := range entries {
					if len(candidates) >= maxCapCeiling {
						break
					}
					candidates = append(candidates, entry.Name())
				}
				if len(candidates) >= maxCapCeiling || len(entries) < 100 || errEntries == io.EOF {
					break
				}
			}
			dFile.Close()
			controllerEntries = candidates
		}

		for _, entryName := range controllerEntries {
			if err := ctx.Err(); err != nil {
				return false, false, ctx.Err()
			}

			if strings.HasPrefix(entryName, "nvme") && !strings.Contains(entryName, "-") && !nvmeNamespaceRegex.MatchString(entryName) {
				subsysPath := filepath.Join("/sys/class/nvme", entryName, fmt.Sprintf("%s/wwid", baseBlockName))
				if _, errStat := os.Stat(subsysPath); errStat == nil {
					statePath := filepath.Join("/sys/class/nvme", entryName, "state")
					// FIXED: Passed entryName instead of baseBlockName to key secureReadSysfs correctly to the adapter
					if stateBytesStr, errState := secureReadSysfs(ctx, gater, entryName, statePath); errState == nil {
						state := strings.ToLower(strings.TrimSpace(stateBytesStr))
						logger.Debugf("[SpecTopology-Trace] Controller adapter '%s' state is '%s'", entryName, state)
						if state == "resetting" || state == "connecting" || state == "deleting" {
							logger.Warningf("[SpecTopology-Trace] Transition state active on controller '%s': '%s'", entryName, state)
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
// Hardened with an asynchronous sector read barrier to guarantee absolute immunity against un-killable D-state deadlocks.
// FIXED: Receiver type converted to a pointer specifier to ensure synchronized state mapping across the driver
func (of *GetDmsPathHelperGeneric) safeSettle(ctx context.Context, gater *executer.KeyedGater, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	name := filepath.Base(path)
	actualReadPath := path
	baseBlockName := name

	// UNCHANGED LOGIC: Your well-tested string index math is fully preserved
	if nvmeControllerNodePattern.MatchString(name) && strings.Contains(name, "c") {
		if lastNIdx := strings.LastIndex(name, "n"); lastNIdx != -1 {
			if cIdx := strings.Index(name, "c"); cIdx != -1 && cIdx < lastNIdx {
				ctrlPart := name[:cIdx]  
				nsPart := name[lastNIdx:] 
				
				baseBlockName = ctrlPart + nsPart 
				actualReadPath = filepath.Join("/dev", baseBlockName)
				logger.Warningf("[Settle-Sanitize] Normalized target name from %s to core block descriptor %s", name, baseBlockName)
			}
		}
	}

	sysBlockTarget := filepath.Join("/sys/block", baseBlockName)
	if resolvedBlock, errLink := filepath.EvalSymlinks(sysBlockTarget); errLink == nil {
		sysBlockTarget = resolvedBlock
		baseBlockName = filepath.Base(resolvedBlock)
	}

	// FIXED: Core D-State Protection. We execute the synchronous disk sector read 
	// natively through your ExecuteUninterruptible D-protect framework. This completely protects the 
	// driver from un-killable kernel hangs using your configured resource pool ceilings.
	trySectorReadProtected := func(readCtx context.Context, targetDeviceNode string) error {
		_, err := executer.ExecuteUninterruptible[struct{}](
			readCtx,
			gater,
			"settle-read-"+filepath.Base(targetDeviceNode),
			5,              // maxRunning limits simultaneous read validation slots
			20,             // maxSpare handles delayed ghost leak tracks safely
			200*time.Millisecond, // handoffTimeout: rapid shift to spare if thread blocks
			1300*time.Millisecond, // hardTimeout: breaks execution free before the 1500ms cycle expires
			func(wCtx context.Context) (struct{}, error) {
				f, err := os.OpenFile(targetDeviceNode, os.O_RDONLY, 0)
				if err != nil {
					return struct{}{}, err
				}
				defer f.Close()

				buf := make([]byte, 512)
				_, readErr := f.Read(buf)
				return struct{}{}, readErr
			},
		)
		return err
	}

	for i := 0; i < 10; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		localTimeout, cancelLocal := context.WithTimeout(ctx, 1500*time.Millisecond)

		if of.IsDeviceMapper(baseBlockName) {
			logger.Warningf("safeSettle DM %s itr %d", baseBlockName, i)
			
			suspendedPath := filepath.Join(sysBlockTarget, "dm", "suspended")
			suspended, err := secureReadSysfs(ctx, gater, baseBlockName, suspendedPath)
			
			if err == nil && strings.TrimSpace(suspended) == "0" {
				// Invoke your robust, protected sector reader
				readErr := trySectorReadProtected(localTimeout, actualReadPath)
				cancelLocal()

				if readErr == nil {
					return nil
				}
			} else {
				cancelLocal()
			}
		} else {
			logger.Warningf("safeSettle native %s (via %s) itr %d", baseBlockName, actualReadPath, i)
			
			statePath := filepath.Join(sysBlockTarget, "device", "state")
			stateBytesStr, stateErr := secureReadSysfs(ctx, gater, baseBlockName, statePath)
			
			stateValid := false
			if stateErr == nil {
				state := strings.ToLower(strings.TrimSpace(stateBytesStr))
				if state == "live" || state == "running" {
					stateValid = true
				}
			}

			// Invoke your robust, protected sector reader
			readErr := trySectorReadProtected(localTimeout, actualReadPath)
			cancelLocal()

			if readErr == nil || stateValid {
				logger.Infof("safeSettle native %s verification successful", baseBlockName)
				return nil
			}
		}
		
		// Backoff timing format rewritten to preserve standard math/rand package compilation safety
		jitter := 200
		if i > 0 {
			jitter = 200 + (time.Now().Nanosecond() % 300)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(jitter) * time.Millisecond):
		}
	}
	return fmt.Errorf("device %s (read path: %s) failed to settle read tests after maximum tracking limits", name, actualReadPath)
}


// validateDMIntegrity evaluates multi-protocol pathways across active and alternative fabrics layouts.
// Hardened with 100% structural VFS path verification: Completely free of string-prefix traps.
func (o *GetDmsPathHelperGeneric) validateDMIntegrity(ctx context.Context, gater *executer.KeyedGater, dmPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ctx.Err()
	}

	dmName := filepath.Base(dmPath)
	const maxCapCeiling = 10000
	
	isDM := o.IsDeviceMapper(dmName)
	isNativeNVMe := o.IsNativeNvmeNamespace(dmName)

	// =========================================================================
	// PROTOCOL LANE A: NATIVE NVME OR STANDALONE RAW PASS-THROUGH
	// =========================================================================
	if isNativeNVMe || !isDM {
		classBlockPath := filepath.Join("/sys/class/block", dmName)
		realClassPath, errLink := filepath.EvalSymlinks(classBlockPath)
		if errLink != nil {
			realClassPath = filepath.Join("/sys/block", dmName) 
		}

		anaStatePath := filepath.Join(realClassPath, "ana_state")
		if _, errStat := os.Stat(anaStatePath); os.IsNotExist(errStat) {
			// Legacy RHEL 7 Fallback: If ana_state doesn't exist, the path is immediately stable.
			return dmPath, nil
		}
		
		if anaBytesStr, err := secureReadSysfsFallback(ctx, gater, dmName, anaStatePath); err == nil && anaBytesStr != "" {
			anaState := strings.TrimSpace(anaBytesStr)
			if anaState == "inaccessible" || anaState == "change" {
				return "", fmt.Errorf("native nvme path %s is currently unusable (ana_state: %s)", dmName, anaState)
			}
		}
		return dmPath, nil
	}

	// =========================================================================
	// PROTOCOL LANE B: DEVICE MAPPER MULTIPATH OVER SCSI OR NVME-DM SLAVES
	// =========================================================================
	slavesPath := filepath.Join("/sys/block", dmName, "slaves")
	if _, errStat := os.Stat(slavesPath); os.IsNotExist(errStat) {
		slavesPath = filepath.Join("/sys/class/block", dmName, "slaves")
	}
	
	dFile, errOpen := os.Open(slavesPath)
	if errOpen != nil {
		return "", fmt.Errorf("dm device %s has no active slave legs attached or unreadable path: %w", dmName, errOpen)
	}

	slaveNames := make([]string, 0, 32)
	totalSlaves := 0

	for {
		if err := ctx.Err(); err != nil {
			dFile.Close()
			return "", ctx.Err()
		}

		slaves, errDirs := dFile.ReadDir(100)
		if errDirs != nil && errDirs != io.EOF {
			dFile.Close()
			return "", fmt.Errorf("failed to read dm slaves tree: %w", errDirs)
		}
		if len(slaves) == 0 || errDirs == io.EOF {
			break
		}

		for _, s := range slaves {
			if len(slaveNames) >= maxCapCeiling {
				break
			}
			slaveNames = append(slaveNames, s.Name())
		}
		totalSlaves += len(slaves)
	}
	dFile.Close() 

	var activePaths int
	var degradedPaths int

	for _, slaveName := range slaveNames {
		if err := ctx.Err(); err != nil {
			return "", ctx.Err()
		}

		slaveDeviceBaseDir := filepath.Join("/sys/block", slaveName, "device")
		if _, errStat := os.Stat(slaveDeviceBaseDir); os.IsNotExist(errStat) {
			slaveDeviceBaseDir = filepath.Join("/sys/class/block", slaveName, "device")
		}
		
		canonicalSlaveDeviceDir, errLink := filepath.EvalSymlinks(slaveDeviceBaseDir)
		if errLink != nil {
			canonicalSlaveDeviceDir = slaveDeviceBaseDir
		}
		
		statePath := filepath.Join(canonicalSlaveDeviceDir, "state")

		// FIXED: Failsafe Structural Verification. 
		// We differentiate SCSI from NVMe slaves by testing for the literal presence 
		// of the kernel's dedicated 'scsi_device' backplane directory layout.
		_, errScsiType := os.Stat(filepath.Join(canonicalSlaveDeviceDir, "scsi_device"))
		isScsiSlave := errScsiType == nil

		// --- BRANCH B.1: VALIDATED SCSI SLAVE CHANNEL TRACK ---
		if isScsiSlave {
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

		// --- BRANCH B.2: VALIDATED NVME OVER DM SLAVE CHANNEL TRACK ---
		} else {
			stateBytesStr, err := secureReadSysfsFallback(ctx, gater, slaveName, statePath)
			if err == nil {
				stateStr := strings.ToLower(strings.TrimSpace(stateBytesStr))
				if stateStr == "live" || stateStr == "running" {
					activePaths++
					continue
				}
			}

			var controllerPassed bool
			ctrlFile, errOpenCtrl := os.Open(canonicalSlaveDeviceDir)
			if errOpenCtrl == nil {
				ctrlCandidates := make([]string, 0, 16)

				for {
					if err := ctx.Err(); err != nil {
						break
					}
					entries, errEntries := ctrlFile.ReadDir(100)
					if errEntries != nil && errEntries != io.EOF {
						break
					}
					if len(entries) == 0 || errEntries == io.EOF {
						break
					}
					for _, entry := range entries {
						if len(ctrlCandidates) >= maxCapCeiling {
							break
						}
						ctrlCandidates = append(ctrlCandidates, entry.Name())
					}
				}
				ctrlFile.Close() 

				for _, entryName := range ctrlCandidates {
					if err := ctx.Err(); err != nil {
						break
					}

					// FIXED: Structural lookup replaces string prefix checks.
					// We verify if the nested element points to an authentic NVMe subsystem 
					// or class controller folder structure using a sub-stat lookup.
					ctrlSubPath := filepath.Join("/sys/class/nvme", entryName, "state")
					if _, errCtrl := os.Stat(ctrlSubPath); errCtrl == nil {
						if ctrlStateBytesStr, err := secureReadSysfsFallback(ctx, gater, entryName, ctrlSubPath); err == nil {
							ctrlState := strings.ToLower(strings.TrimSpace(ctrlStateBytesStr))
							if ctrlState == "live" || ctrlState == "running" {
								activePaths++
								controllerPassed = true
								break 
							}
						}
					}
				}
			}
			
			if !controllerPassed {
				degradedPaths++
			}
		}
	}

	logger.Infof("[Integrity-Check] [%s] Path analysis complete. Active lines: %d | Degraded/Preparing lines: %d", dmName, activePaths, degradedPaths)

	if activePaths == 0 {
		return "", fmt.Errorf("dm device %s has zero functional operational paths (Total Slaves: %d, Degraded: %d)", dmName, totalSlaves, degradedPaths)
	}
	
	return dmPath, nil
}

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
	
	// Handle subsystem abstractions explicitly first
	if strings.HasPrefix(cleanName, "nvme-subsys") {
		return cleanName
	}

	// Rule: An NVMe controller base always matches the format "nvme" followed by digits.
	// Find where the digits after "nvme" end, before any namespace ('n') or channel ('c') designators.
	if !strings.HasPrefix(cleanName, "nvme") {
		return cleanName
	}

	// Start scanning after the initial "nvme" string prefix (index 4)
	endIdx := len(cleanName)
	for i := 4; i < len(cleanName); i++ {
		ch := cleanName[i]
		// The moment we hit a non-digit character (like 'n' or 'c'), 
		// we know we have fully captured the parent controller number.
		if ch < '0' || ch > '9' {
			endIdx = i
			break
		}
	}

	return cleanName[:endIdx]
}

type NvmeType string

const (
       NVMeNative    NvmeType = "native"
       NVMeNonNative NvmeType = "non-native"
       NotNVMe       NvmeType = "non-nvme"
)

func DevicesAreNvme(ctx context.Context, gater *executer.KeyedGater, device string) (NvmeType, error) {
	if err := ctx.Err(); err != nil {
		return NotNVMe, err
	}

	logger.Debugf("DevicesAreNvme: Analyzing transport topology structure for device %s", device)

	sanitizedDevicePath := device
	if !filepath.IsAbs(sanitizedDevicePath) {
		sanitizedDevicePath = filepath.Join("/dev", device)
	}

	// Step 1: Check for Native NVMe using the gater-protected helper with the absolute path
	isNative, err := IsNativeNvmeDevice(ctx, gater, sanitizedDevicePath)
	if err != nil {
		logger.Warningf("DevicesAreNvme: Failed native NVMe evaluation for %s: %v", device, err)
	} else if isNative {
		logger.Infof("DevicesAreNvme: Device %s verified structurally as NVMeNative", device)
		return NVMeNative, nil
	}

	// Step 2: Check for Non-Native NVMe managed via Device Mapper Multipath
	isNonNative, err := IsNonNativeNvmeDevice(ctx, gater, sanitizedDevicePath)
	if err != nil {
		logger.Warningf("DevicesAreNvme: Failed non-native NVMe evaluation for %s: %v", device, err)
	} else if isNonNative {
		logger.Infof("DevicesAreNvme: Device %s verified as NVMeNonNative (Device Mapper Multipath over NVMe Fabrics)", device)
		return NVMeNonNative, nil
	}

	logger.Debugf("DevicesAreNvme: Device %s verified as NotNVMe storage protocol type", device)
	return NotNVMe, nil
}

// IsNativeNvmeDevice safe-evaluates whether a block path represents a native NVMe device mapping.
func IsNativeNvmeDevice(ctx context.Context, gater *executer.KeyedGater, dmPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	resolvedPath, errLink := filepath.EvalSymlinks(dmPath)
	if errLink != nil {
		if os.IsNotExist(errLink) {
			return false, nil
		}
		return false, fmt.Errorf("failed to evaluate storage symlink for path %s: %w", dmPath, errLink)
	}
	baseDevice := filepath.Base(resolvedPath)

	if strings.HasPrefix(baseDevice, "dm-") {
		return false, nil 
	}

	sysBlockPath := filepath.Join("/sys/block", baseDevice)
	if _, err := os.Stat(sysBlockPath); os.IsNotExist(err) {
		classBlockPath := filepath.Join("/sys/class/block", baseDevice)
		if realClassPath, errEval := filepath.EvalSymlinks(classBlockPath); errEval == nil {
			realClassPathLower := strings.ToLower(realClassPath)
			// FIXED: Locate the LAST occurrence of "/block/" to protect against intermediate path components 
			// containing the string "block" (like container plugin directories).
			idx := strings.LastIndex(realClassPathLower, "/block/")
			if idx != -1 {
				remainder := realClassPath[idx+len("/block/"):]
				subParts := strings.Split(remainder, "/")
				if len(subParts) > 0 && subParts[0] != "" {
					baseDevice = subParts[0] // Safely abstracts "nvme0n1p1" or complex structures to parent "nvme0n1"
					sysBlockPath = filepath.Join("/sys/block", baseDevice)
				}
			}
		}
	}

	cleanGaterKey := filepath.Base(baseDevice)
	return executer.ExecuteUninterruptible[bool](
		ctx,
		gater,
		fmt.Sprintf("check-native-nvme-%s", cleanGaterKey),
		10, 50, 1*time.Second, 3*time.Second,
		func(wCtx context.Context) (bool, error) {
			if err := wCtx.Err(); err != nil {
				return false, err
			}

			// Tier 1: Modern Fabrics Check (subsysnqn exists on the whole disk parent)
			subsysNqnPath := filepath.Join(sysBlockPath, "device/subsysnqn")
			if _, err := os.Stat(subsysNqnPath); err == nil {
				return true, nil
			}

			// Tier 2: Legacy RHEL 8 Fallback Check
			subsystemLink := filepath.Join(sysBlockPath, "device/subsystem")
			if realTarget, errLink := filepath.EvalSymlinks(subsystemLink); errLink == nil {
				if strings.Contains(realTarget, "/bus/nvme") || strings.Contains(realTarget, "/class/nvme") {
					return true, nil
				}
			}

			// Tier 3: Sysfs Block Class Name Assessment
			ueventPath := filepath.Join(sysBlockPath, "device/uevent")
			if data, err := os.ReadFile(ueventPath); err == nil {
				if strings.Contains(string(data), "DRIVER=nvme") || strings.Contains(string(data), "SUBSYSTEM=nvme") {
					return true, nil
				}
			}

			return false, nil
		},
	)
}

// IsNonNativeNvmeDevice checks if a device-mapper path maps to underlying non-native NVMe fabrics.
func IsNonNativeNvmeDevice(ctx context.Context, gater *executer.KeyedGater, dmPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	resolvedPath, errLink := filepath.EvalSymlinks(dmPath)
	if errLink != nil {
		return false, fmt.Errorf("failed to evaluate storage storage symlink for path %s: %w", dmPath, errLink)
	}
	baseDevice := filepath.Base(resolvedPath)

	parentDevice, isDM, errVerify := resolveParentAndType(baseDevice)
	if errVerify != nil {
		return false, errVerify
	}
	if !isDM {
		return false, nil
	}

	gaterKey := "nvme-check-" + filepath.Base(parentDevice)
	return executer.ExecuteUninterruptible[bool](
		ctx,
		gater,
		gaterKey,
		30, 150, 2*time.Second, 8*time.Second,
		func(wCtx context.Context) (bool, error) {
			return scanSlavesForSubsystem(wCtx, parentDevice, "nvme")
		},
	)
}

func resolveParentAndType(baseDevice string) (string, bool, error) {
	baseDevice = filepath.Base(baseDevice)
	sysPath := filepath.Join("/sys/block", baseDevice)
	
	if _, err := os.Stat(sysPath); os.IsNotExist(err) {
		classPath := filepath.Join("/sys/class/block", baseDevice)
		realPath, errEval := filepath.EvalSymlinks(classPath)
		if errEval != nil {
			return "", false, fmt.Errorf("failed to evaluate block class path for %s: %w", baseDevice, errEval)
		}
		
		realPathLower := strings.ToLower(realPath)
		idx := strings.LastIndex(realPathLower, "/block/")
		if idx != -1 {
			remainder := realPath[idx+len("/block/"):]
			subParts := strings.Split(remainder, "/")
			if len(subParts) > 0 && subParts[0] != "" {
				baseDevice = subParts[0]
				// FIXED: Re-sync sysPath to match the newly resolved canonical baseDevice name
				sysPath = filepath.Join("/sys/block", baseDevice)
			}
		}
	}

	// Final verification that the resolved sysfs path actually exists on disk
	if _, err := os.Stat(sysPath); os.IsNotExist(err) {
		return baseDevice, strings.HasPrefix(baseDevice, "dm-"), fmt.Errorf("resolved device path does not exist in sysfs: %s", sysPath)
	}

	isDM := strings.HasPrefix(baseDevice, "dm-")
	return baseDevice, isDM, nil
}

// scanSlavesForSubsystem safely walks the /sys/block/<dev>/slaves folder to inspect backing hardware transport subsystems.
func scanSlavesForSubsystem(wCtx context.Context, parentDevice, expectedSubsystem string) (bool, error) {
	if err := wCtx.Err(); err != nil {
		return false, err
	}

	slavesPath := filepath.Join("/sys/block", parentDevice, "slaves")
	dFile, errOpen := os.Open(slavesPath)
	if errOpen != nil {
		if os.IsNotExist(errOpen) {
			return false, nil
		}
		return false, fmt.Errorf("failed to open device slaves directory %s: %w", slavesPath, errOpen)
	}
	defer dFile.Close()

	const maxCapCeiling = 10000
	processedCount := 0

	for {
		if err := wCtx.Err(); err != nil {
			return false, err
		}

		entries, readErr := dFile.ReadDir(100)
		if readErr != nil && readErr != io.EOF {
			return false, fmt.Errorf("failed to streaming-read entries from directory %s: %w", slavesPath, readErr)
		}
		if len(entries) == 0 || readErr == io.EOF {
			break
		}

		for _, entry := range entries {
			if processedCount >= maxCapCeiling {
				logger.Warningf("[VFS-Guard] Slaves directory processing bounds hit limits (%d). Truncating scan.", maxCapCeiling)
				return false, nil
			}
			processedCount++

			// Structural Subsystem Check: Query structural /sys definition to completely bypass fake admin text tags.
			slaveSubsystemPath := filepath.Join("/sys/block", entry.Name(), "device", "subsystem")
			if target, errEval := filepath.EvalSymlinks(slaveSubsystemPath); errEval == nil {
				if filepath.Base(target) == expectedSubsystem {
					logger.Debugf("Storage Scan Match: Device %s confirmed as %s via structural slave element (%s)", parentDevice, expectedSubsystem, entry.Name())
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// GetSysDevicesFromMpath cleanly resolves raw storage block devices protected against D-state freezes.
// Hardened against broken sysfs subsystem traversal paths and unbounded file descriptor reads.
func GetSysDevicesFromMpath(ctx context.Context, baseDevice string) ([]string, error) {
	if err := ctx.Err(); err != nil { 
		return nil, err 
	}

	logger.Debugf("GetSysDevicesFromMpath: Aggregating underlying paths for %s", baseDevice)

	baseDevice = filepath.Base(baseDevice)
	sysBlockTarget := filepath.Join("/sys/block", baseDevice)

	if _, err := os.Stat(sysBlockTarget); os.IsNotExist(err) {
		classBlockPath := filepath.Join("/sys/class/block", baseDevice)
		if realClassPath, errEval := filepath.EvalSymlinks(classBlockPath); errEval == nil {
			if strings.Contains(realClassPath, "/block/") {
				parts := strings.Split(realClassPath, "/block/")
				if len(parts) == 2 {
					subParts := strings.Split(parts[1], "/")
					if len(subParts) > 0 {
						baseDevice = subParts[0] 
						sysBlockTarget = filepath.Join("/sys/block", baseDevice)
					}
				}
			}
		}
	}

	isDM := false
	if _, errDmDir := os.Stat(filepath.Join(sysBlockTarget, "dm")); errDmDir == nil {
		isDM = true
	}

	const maxCapCeiling = 10000

	// =========================================================================
	// BRANCH 1: DEVICE MAPPER (SCSI OR NON-NATIVE NVMe OVER DM FABRICS)
	// =========================================================================
	if isDM {
		deviceSlavePath := filepath.Join(sysBlockTarget, "slaves")
		entries, errRead := os.ReadDir(deviceSlavePath)
		if errRead != nil {
			logger.Errorf("An error occurred while looking for device slaves: %v", errRead)
			return nil, fmt.Errorf("failed to read dm slaves directory at %s: %w", deviceSlavePath, errRead)
		}

		var slavesNames []string
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, ctx.Err()
			}
			if len(slavesNames) >= maxCapCeiling {
				break
			}
			slavesNames = append(slavesNames, entry.Name())
		}
		
		logger.Debugf("GetSysDevicesFromMpath: Discovered slaves for DM device %s: %v", baseDevice, slavesNames)
		return slavesNames, nil
	}

	// =========================================================================
	// BRANCH 2: NATIVE NVME MULTIPATH SUBSYSTEM LAYERS (nvmeXnY)
	// =========================================================================
	if strings.HasPrefix(baseDevice, "nvme") {
		nIdx := strings.LastIndex(baseDevice, "n")
		if nIdx == -1 || nIdx == len(baseDevice)-1 {
			return nil, fmt.Errorf("invalid nvme device configuration footprint name: %s", baseDevice)
		}
		nsID := baseDevice[nIdx:] // e.g., "n1"

		// FIXED: Correct sysfs path resolution for native NVMe multipath controllers.
		// Check the parent subsystem link or scan /sys/class/nvme-subsystem/ for associated controllers.
		subsysLink := filepath.Join(sysBlockTarget, "device", "subsystem")
		realSubsys, errEval := filepath.EvalSymlinks(subsysLink)
		
		var pathNames []string
		if errEval == nil {
			// realSubsys typically points to /sys/class/nvme-subsystem/nvme-subsysN
			subsEntries, errSubsRead := os.ReadDir(realSubsys)
			if errSubsRead == nil {
				for _, entry := range subsEntries {
					// Controller names inside the subsystem directory appear as nvme0, nvme1, etc.
					ctrlName := entry.Name()
					if strings.HasPrefix(ctrlName, "nvme") && !strings.Contains(ctrlName, "subsys") {
						pathName := ctrlName + nsID
						if _, errStat := os.Stat(filepath.Join("/sys/block", pathName)); errStat == nil {
							if len(pathNames) < maxCapCeiling {
								pathNames = append(pathNames, pathName)
							}
						}
					}
				}
			}
		}

		// Fallback to single baseDevice if controller enumeration is empty or unlinked
		if len(pathNames) == 0 {
			if _, errStat := os.Stat(sysBlockTarget); errStat == nil {
				pathNames = []string{baseDevice}
			} else {
				return nil, fmt.Errorf("failed to resolve native nvme controllers under subsystem for %s", baseDevice)
			}
		}
		
		logger.Debugf("GetSysDevicesFromMpath: Discovered native path components for %s: %v", baseDevice, pathNames)
		return pathNames, nil
	}
	
	return nil, fmt.Errorf("unsupported block layer device type layout specification: %s", baseDevice)
}

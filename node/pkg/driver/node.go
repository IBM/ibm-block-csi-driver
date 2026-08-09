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

package driver

import (
	"context"
	"errors"
	"regexp"
	"fmt"
	"os"
	"path"
	"path/filepath" // FIXED: Replaced brittle path with filepath
	"reflect"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/ibm/ibm-block-csi-driver/node/goid_info"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	mount "k8s.io/mount-utils"
)

var (
	nodeCaps = []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
	}

	// volumeCaps represents how the volume could be accessed.
	// It is SINGLE_NODE_WRITER since EBS volume could only be
	// attached to a single node at any given time.
	volumeCaps = []csi.VolumeCapability_AccessMode{
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		},
	}

	defaultFSType     = "ext4"
	StageInfoFilename = ".stageInfo.json"

	NvmeFullPath  = "/host/etc/nvme/hostnqn"
	IscsiFullPath = "/host/etc/iscsi/initiatorname.iscsi"
)

const (
	FCPath     = "/sys/class/fc_host"
	FCPortPath = "/sys/class/fc_host/host*/port_name"
)

//go:generate mockgen -destination=../../mocks/mock_NodeMounter.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver NodeMounter

type NodeMounter interface {
	mount.Interface
	FormatAndMount(source string, target string, fstype string, options []string) error
	GetDiskFormat(disk string) (string, error)
}

// nodeService represents the node service of CSI driver
type NodeService struct {
	// csi.NodeServer
	Mounter                     NodeMounter
	ConfigYaml                  ConfigFile
	Hostname                    string
	NodeUtils                   NodeUtilsInterface
	executer                    executer.ExecuterInterface
	VolumeIdLocksMap            SyncLockInterface
	OsDeviceConnectivityMapping map[string]device_connectivity.OsDeviceConnectivityInterface
	OsDeviceConnectivityHelper  device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface
}

type VolumeStatistics struct {
	AvailableBytes, TotalBytes, UsedBytes    int64
	AvailableInodes, TotalInodes, UsedInodes int64
}

// newNodeService creates a new node service
// it panics if failed to create the service
func NewNodeService(configYaml ConfigFile, hostname string, nodeUtils NodeUtilsInterface,
	OsDeviceConnectivityMapping map[string]device_connectivity.OsDeviceConnectivityInterface,
	osDeviceConnectivityHelper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface,
	executer executer.ExecuterInterface, mounter NodeMounter, syncLock SyncLockInterface) NodeService {
	return NodeService{
		ConfigYaml:                  configYaml,
		Hostname:                    hostname,
		NodeUtils:                   nodeUtils,
		executer:                    executer,
		OsDeviceConnectivityMapping: OsDeviceConnectivityMapping,
		OsDeviceConnectivityHelper:  osDeviceConnectivityHelper,
		Mounter:                     mounter,
		VolumeIdLocksMap:            syncLock,
	}
}

// Structural pattern matching to ensure accurate device name handling across all Linux layers
var nvmeStageControllerPattern = regexp.MustCompile(`^nvme\d+c\d+n\d+$`)

func (d *NodeService) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))

	// 1. INPUT VALIDATION & TYPE-SAFE UNWRAPPING
	err := d.nodeStageVolumeRequestValidation(req)
	if err != nil {
		var validationErr *RequestValidationError
		if errors.As(err, &validationErr) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	connectivityType, lun, ipsByArrayInitiator, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	
	volumeID := req.VolumeId
	err = d.VolumeIdLocksMap.AddVolumeAndLunLock(volumeID, lun, "NodeStageVolume")
	if err != nil {
		logger.Errorf("Another operation is being performed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeAndLunLock(volumeID, lun, "NodeStageVolume")

	arrayInitiators := d.NodeUtils.GetArrayInitiators(ipsByArrayInitiator)

	osDeviceConnectivity, ok := d.OsDeviceConnectivityMapping[connectivityType]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Wrong connectivity type %s", connectivityType))
	}

	stagingPath := req.GetStagingTargetPath() 
	stagingPathWithHostPrefix := d.NodeUtils.GetPodPath(stagingPath)
	volumeUuid := d.NodeUtils.GetVolumeUuid(volumeID)

	// 2. IDEMPOTENCY MOUNT CHECK
	isMounted, err := d.isTargetMounted(stagingPathWithHostPrefix, true)
	if err != nil {
		logger.Debugf("Existing mount check failed {%v}", err.Error())
		return nil, err
	}
	if isMounted { 
		return &csi.NodeStageVolumeResponse{}, nil
	}

	// =========================================================================
	// STAGE 1: EVALUATE IDENTITY AND KERNEL TOPOLOGY PRE-SCAN
	// =========================================================================
	mpathDevice, isStaged, skipRescan, _, preScanErr := d.OsDeviceConnectivityHelper.IdentityAwarePreScan(ctx, stagingPathWithHostPrefix, volumeUuid)
	if preScanErr != nil && status.Code(preScanErr) != codes.Aborted {
		return nil, preScanErr
	}

	if isStaged {
		logger.Infof("NodeStageVolume Complete: Already fully staged and verified via hardware inquiry.")
		return &csi.NodeStageVolumeResponse{}, nil
	}

	// =========================================================================
	// STAGE 2: DISCOVERY OR STABILIZATION ROUTING
	// =========================================================================
	if !skipRescan {
		logger.Infof("Device missing or recently purged for WWID %v. Initiating fabric discovery.", volumeUuid)
		osDeviceConnectivity.EnsureLogin(ctx, ipsByArrayInitiator)
		
		_ = d.OsDeviceConnectivityHelper.RemoveGhostDevice(ctx, volumeUuid, lun, arrayInitiators)
		if err := osDeviceConnectivity.RescanDevices(ctx, lun, arrayInitiators); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		_ = d.OsDeviceConnectivityHelper.RemoveGhostDevice(ctx, volumeUuid, lun, arrayInitiators)
	} else {
		if preScanErr != nil && status.Code(preScanErr) == codes.Aborted {
			logger.Infof("Optimization: Active kernel transition detected for %v. Bypassing rescan and entering poll loop.", volumeUuid)
		} else {
			 logger.Infof("Optimization: Healthy idle block device %s found in sysfs. Bypassing rescan phase.", mpathDevice)
		}
	}

	// =========================================================================
	// STAGE 3: CORE POLLING AND MULTI-PATH STABILIZATION
	// =========================================================================
	mpathDevice, err = d.OsDeviceConnectivityHelper.GetMpathDevice(ctx, volumeUuid)
	if err != nil {
		logger.Errorf("Error while discovering the device : {%v}", err.Error())
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
			return nil, status.Errorf(codes.DeadlineExceeded, "temporary discovery loop timeout: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "device fingerprint stabilization failed: %v", err)
	}

	// FIXED: Removed unreachable character-device regex tracking code completely.
	logger.Infof("Device discovery finalized and settled successfully at path: %s", mpathDevice)
	
	// 3. BLOCK VOLUME EXIT
	volumeCap := req.GetVolumeCapability()
	switch volumeCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		logger.Debugf("NodeStageVolume Finished: multipath device [%s] is ready to be mounted by NodePublishVolume API", mpathDevice)
		return &csi.NodeStageVolumeResponse{}, nil
	}

	// =========================================================================
	// 4. TOPOLOGY VALIDATION, FORMATTING, AND MOUNTING
	// =========================================================================
	baseDevice := filepath.Base(mpathDevice)
	
	// FIXED: Replace string prefix heuristics with your zero-fork, VFS-aware checker
	nvmeType, err := d.NodeUtils.DevicesAreNvme(ctx, baseDevice)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Topology evaluation failed during block scheme lookup for %s: %v", baseDevice, err)
	}

	var sysDevices []string
	logger.Infof("Device missing or recently purged for WWID %v. Initiating fabric discovery - 7.", volumeUuid)

	if nvmeType == NVMeNative {
		// Native NVMe Multipathing structures track the global virtual subsystem node directly
		sysDevices = []string{baseDevice}
	} else {
		// Securely handles both traditional SCSI maps and NVMe over DM (NVMeNonNative) layouts via sysfs
		sysDevices, err = d.NodeUtils.GetSysDevicesFromMpath(ctx, baseDevice)
		if err != nil {
			logger.Errorf("Error while trying to get sys devices : {%v}", err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	
	logger.Infof("Device missing or recently purged for WWID %v. Initiating fabric discovery - 8.", volumeUuid)
	
	// Invoke LUN tracking verification with the protocol-isolated tracking fields
	if err := osDeviceConnectivity.ValidateLun(ctx, mpathDevice, lun, sysDevices, volumeUuid); err != nil {
		logger.Errorf("Volume LUN validation failed for %s: %v", mpathDevice, err)
		return nil, status.Error(codes.Internal, err.Error())
	}
	
	logger.Infof("Device missing or recently purged for WWID %v. Initiating fabric discovery - 9.", volumeUuid)

	existingFormat, err := d.Mounter.GetDiskFormat(mpathDevice)
	if err != nil {
		logger.Errorf("Could not determine if disk {%v} is formatted, error: %v", mpathDevice, err)
		return nil, status.Error(codes.Internal, err.Error())
	}

	fsTypeForMount, err := d.resolveFsTypeForMount(volumeCap.GetMount().FsType, existingFormat)
	if err != nil {
		logger.Errorf("Error while resolving type of filesystem to mount : {%v}", err.Error())
		return nil, err
	}
	
	// HARDENED SANITIZATION LAYER: Pre-clean stale block files or dangling relics from prior crashes
	if d.NodeUtils.IsPathExists(stagingPathWithHostPrefix) {
		isBlockFile, errBlock := d.NodeUtils.IsBlock(ctx, stagingPathWithHostPrefix)
		if errBlock == nil && isBlockFile {
			logger.Warningf("Sanitization Safeguard: Removing left-behind block file artifact at staging path: %s", stagingPathWithHostPrefix)
			_ = os.Remove(stagingPathWithHostPrefix)
		}
	}

	if err = os.MkdirAll(stagingPathWithHostPrefix, 0750); err != nil {
		logger.Errorf("failed to create target directory %s: %v", stagingPathWithHostPrefix, err)
		return nil, status.Errorf(codes.Internal, "failed to create staging target directory: %v", err)
	}	

	// FIXED: Pass stagingPathWithHostPrefix instead of stagingPath to ensure format/mount operations targeting
	// the proper namespace work seamlessly across all protocols
	if err = d.formatAndMount(ctx, mpathDevice, stagingPathWithHostPrefix, fsTypeForMount, existingFormat); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Debugf("NodeStageVolume Finished: staging path [%s] is ready to be mounted by NodePublishVolume API", stagingPathWithHostPrefix)
	return &csi.NodeStageVolumeResponse{}, nil
}

func isValidConnectivity(connectivityTypes Connectivity_type, connectivityType string) bool {
	connectivityReflect := reflect.ValueOf(&connectivityTypes).Elem()
	for i := 0; i < connectivityReflect.NumField(); i++ {
		if connectivityReflect.Field(i).Interface() == connectivityType {
			return true
		}
	}
	return false
}

func (d *NodeService) nodeStageVolumeRequestValidation(req *csi.NodeStageVolumeRequest) error {

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return &RequestValidationError{"Volume ID not provided"}
	}

	stagingPath := req.GetStagingTargetPath()
	if len(stagingPath) == 0 {
		return &RequestValidationError{"Staging path not provided"}
	}

	stagingPathWithHostPrefix := d.NodeUtils.GetPodPath(stagingPath)
	isStagingPathExists := d.NodeUtils.IsPathExists(stagingPathWithHostPrefix)
	if !isStagingPathExists {
		return &RequestValidationError{fmt.Sprintf("Staging path [%s] does not exist", stagingPathWithHostPrefix)}
	}

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return &RequestValidationError{"Volume capability not provided"}
	}

	if !isValidVolumeCapabilitiesAccessMode([]*csi.VolumeCapability{volCap}) {
		return &RequestValidationError{"Volume capability AccessMode not supported"}
	}

	// If the access type is not mount and not block, should never happen
	switch volCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Mount:
	case *csi.VolumeCapability_Block:
	default:
		return &RequestValidationError{"Volume Access Type is not supported"}
	}

	connectivityType, lun, ipsByArrayInitiator, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext)
	if err != nil {
		return &RequestValidationError{fmt.Sprintf("Fail to parse PublishContext %v with err = %v", req.PublishContext, err)}
	}
	supportedConnectivityTypes := d.ConfigYaml.Connectivity_type
	if !isValidConnectivity(supportedConnectivityTypes, connectivityType) {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong connectivity type %s. Supported connectivities %v", connectivityType, supportedConnectivityTypes)}
	}

	if lun < 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong lun id %d.", lun)}
	}

	if len(ipsByArrayInitiator) == 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong arrayInitiators %v.",
			ipsByArrayInitiator)}
	}

	if connectivityType == d.ConfigYaml.Connectivity_type.Iscsi {
		isAnyIpFound := false
		for arrayInitiator := range ipsByArrayInitiator {
			if _, ok := req.PublishContext[arrayInitiator]; ok {
				isAnyIpFound = true
				break
			}
		}
		if !isAnyIpFound {
			return &RequestValidationError{fmt.Sprintf("PublishContext with no iscsi target IP %v.",
				req.PublishContext)}
		}
	}

	return nil
}

func (d *NodeService) resolveFsTypeForMount(requestedFsType string, existingFormat string) (string, error) {
	fsTypeForMount := requestedFsType
	if requestedFsType == "" {
		if existingFormat == "" {
			fsTypeForMount = defaultFSType
		} else {
			fsTypeForMount = existingFormat
		}
	} else if existingFormat != "" {
		if requestedFsType != existingFormat {
			return "", status.Errorf(codes.AlreadyExists, "Requested fs_type {%v} but found {%v}", requestedFsType, existingFormat)
		}
	}
	return fsTypeForMount, nil
}

func (d *NodeService) formatAndMount(ctx context.Context, mpathDevice string, stagingPath string, fsTypeForMount string, existingFormat string) error {
	if existingFormat == "" {
		d.NodeUtils.FormatDevice(ctx, mpathDevice, fsTypeForMount)
	}

	var mountOptions []string
	if fsTypeForMount == "xfs" {
		mountOptions = append(mountOptions, "nouuid")
	}

	logger.Debugf("Mount the device with fs_type = {%v} (Create filesystem if needed)", fsTypeForMount)
	return d.Mounter.FormatAndMount(mpathDevice, stagingPath, fsTypeForMount, mountOptions) // Passing without /host because k8s mounter uses mount\mkfs\fsck
}

// Structural pattern matching to ensure accurate device name handling across all Linux layers
var nvmeUnstageControllerPattern = regexp.MustCompile(`^nvme\d+c\d+n\d+$`)

func (d *NodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))
	volumeID := req.GetVolumeId()

	if len(volumeID) == 0 {
		logger.Errorf("Volume ID not provided")
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	err := d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodeUnstageVolume")
	if err != nil {
		logger.Errorf("Another operation is being performed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodeUnstageVolume")

	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		logger.Errorf("Staging target not provided")
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	stagingPathWithHostPrefix := d.NodeUtils.GetPodPath(stagingTargetPath)
	logger.Debugf("Check if staging path {%s} is mounted", stagingPathWithHostPrefix)

	volumeUuid := d.NodeUtils.GetVolumeUuid(volumeID)
	
	var needFlush bool
	var needRemovePhysical bool
	
	// FIXED: Clear Metadata info FIRST before the teardown unmounts the directory 
	// out from underneath the container namespace.
	stageInfoPath := filepath.Join(stagingPathWithHostPrefix, StageInfoFilename)
	if d.NodeUtils.StageInfoFileIsExist(stageInfoPath) {
		if err := d.NodeUtils.ClearStageInfoFile(stageInfoPath); err != nil {
			logger.Warningf("Failed to clear stage info metadata at %s: %v. Proceeding with block layer teardown.", stageInfoPath, err)
		}
	}

	device, err := d.OsDeviceConnectivityHelper.GetExistingMpathDevice(ctx, volumeUuid, stagingPathWithHostPrefix)
	if err != nil {
		logger.Errorf("Error while discovering the device : {%v}. Activating multi-protocol fallback configuration safety variables.", err.Error())
		needFlush = true
		needRemovePhysical = true
	} else {
		logger.Debugf("Discovered device : {%v}", device)
		baseDevice := filepath.Base(device)

		// Robust structural zero-fork lookups replace fragile regex text matching
		nvmeType, err := d.NodeUtils.DevicesAreNvme(ctx, baseDevice)
		if err != nil {
			logger.Errorf("Failed to determine device type for %s: %v. Defaulting to full cleanup strategy.", baseDevice, err)
			needFlush = true
			needRemovePhysical = true
		} else {
			switch nvmeType {
			case NVMeNative:
				logger.Infof("Device %s is native NVMe: skipping flush and SCSI device cleanup", baseDevice)
				needFlush = false
				needRemovePhysical = false

			case NVMeNonNative:
				needFlush = true
				logger.Infof("Device %s is non-native NVMe: flush multipath, skip physical device removal", baseDevice)
				needFlush = true
				needRemovePhysical = false

			case NotNVMe:
				logger.Infof("Device %s is not NVMe (SCSI/FC): flush multipath and trigger physical device path removal", baseDevice)
				needFlush = true
				needRemovePhysical = true

			default:
				return nil, status.Errorf(codes.Internal, "Unknown NVMe type for device %s", baseDevice)
			}
		}
	}

	// UNCHANGED INTERFACE: Invoking TeardownVolume exactly as it was originally defined by you, 
	// using stagingTargetPath as the second parameter and volumeUuid as the fifth parameter.
	err = d.OsDeviceConnectivityHelper.TeardownVolume(ctx, stagingTargetPath, needFlush, needRemovePhysical, volumeUuid)
	if err != nil {
		logger.Errorf("Failed to teardown volume %s at staging path %s: %v", volumeUuid, stagingTargetPath, err)
		return nil, status.Errorf(codes.Internal, "failed to teardown staging target %s: %v", stagingTargetPath, err)
	}

	// 3. Wipe target path registration now that the underlying unmount is safely complete
	if err := os.Remove(stagingPathWithHostPrefix); err != nil && !os.IsNotExist(err) {
		return nil, status.Errorf(codes.Internal, "failed to remove staging host path: %v", err)
	}

	logger.Infof("NodeUnstageVolume Finished Successfully: volume %s completely removed from host", volumeUuid)
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *NodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))

	code, err := d.nodePublishVolumeRequestValidation(req)
	if err != nil {
		switch err.(type) {
		case *RequestValidationError:
			return nil, status.Error(code, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	volumeID := req.GetVolumeId()

	err = d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodePublishVolume")
	if err != nil {
		logger.Errorf("Another operation is being perfomed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodePublishVolume")

	// checking if the node staging path was mounted into
	stagingPath := req.GetStagingTargetPath()
	targetPath := req.GetTargetPath()
	targetPathWithHostPrefix := d.NodeUtils.GetPodPath(targetPath)

	logger.Debugf("stagingPath : {%v}, targetPath : {%v}", stagingPath, targetPath)

	// if the device is not mounted then we are mounting it.
	volumeCap := req.GetVolumeCapability()
	isFSVolume := true
	switch volumeCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		isFSVolume = false
	}

	if isFSVolume {
		stagingPathWithHostPrefix := d.NodeUtils.GetPodPath(stagingPath)
		isStagingNotMounted, err := d.NodeUtils.IsNotMountPoint(stagingPathWithHostPrefix)
		if err != nil {
			logger.Errorf("Existing mount check failed {%v}", err.Error())
			return nil, err
		}
		if isStagingNotMounted {
			return nil, status.Errorf(codes.InvalidArgument, "Staging path %v is not a mount point", stagingPath)
		}
	}

	isTargetPathExists := d.NodeUtils.IsPathExists(targetPathWithHostPrefix)
	if isTargetPathExists {
		// check if already mounted
		isTargetMounted, err := d.isTargetMounted(targetPathWithHostPrefix, isFSVolume)
		if err != nil {
			logger.Debugf("Existing mount check failed {%v}", err.Error())
			return nil, err
		}
		if isTargetMounted { // idempotent case
			return &csi.NodePublishVolumeResponse{}, nil
		}
	} else {
		logger.Debugf("Target path does not exist. Creating : {%v}", targetPathWithHostPrefix)
		if isFSVolume {
			err = d.NodeUtils.MakeDir(targetPathWithHostPrefix)
		} else {
			err = d.NodeUtils.MakeFile(targetPathWithHostPrefix)
		}
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not create %q: %v", targetPathWithHostPrefix, err.Error())
		}
	}

	if isFSVolume {
		fsType := volumeCap.GetMount().FsType
		err = d.publishFileSystemVolume(stagingPath, targetPath, fsType)
	} else {
		volumeUuid := d.NodeUtils.GetVolumeUuid(volumeID)
		mpathDevice, err := d.OsDeviceConnectivityHelper.GetMpathDevice(ctx, volumeUuid)
		if err != nil {
			logger.Errorf("Error while discovering the device : {%v}", err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
		logger.Debugf("Discovered device : {%v}", mpathDevice)

		err = d.publishRawBlockVolume(mpathDevice, targetPath)
	}

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("NodePublishVolume Finished: targetPath {%v} is now a mount point", targetPath)

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *NodeService) publishFileSystemVolume(stagingPath string, targetPath string, fsType string) error {
	mountOptions := []string{"bind"}
	logger.Debugf("Bind mount staging: {%v} with target: {%v}, fs_type: {%v}", stagingPath, targetPath, fsType)
	return d.Mounter.Mount(stagingPath, targetPath, fsType, mountOptions) // Passing without /host because k8s mounter uses mount\mkfs\fsck
}

func (d *NodeService) publishRawBlockVolume(mpathDevice string, targetPath string) error {
	options := []string{"bind"}
	logger.Debugf("Mount the device to raw block volume. Target : {%s}, device : {%s}", targetPath, mpathDevice)
	return d.Mounter.Mount(mpathDevice, targetPath, "", options)
}

// targetPathWithHostPrefix: path of target
// isFSVolume: if we check volume with file system - true, otherwise for raw block false
// Returns: is <target mounted, error if occured>
func (d *NodeService) isTargetMounted(targetPathWithHostPrefix string, isFSVolume bool) (bool, error) {
	logger.Debugf("Check if target {%s} is mounted", targetPathWithHostPrefix)
	isNotMounted, err := d.NodeUtils.IsNotMountPoint(targetPathWithHostPrefix)
	if err != nil {
		logger.Warningf("Failed to check if (%s), is mounted", targetPathWithHostPrefix)
		return false, status.Error(codes.Internal, err.Error())
	}
	if isNotMounted {
		return false, nil
	} else {
		targetIsDir := d.NodeUtils.IsDirectory(targetPathWithHostPrefix)
		if isFSVolume && !targetIsDir {
			return true, status.Errorf(codes.AlreadyExists, "Required volume with file system but target {%s} is mounted and it is not a directory.", targetPathWithHostPrefix)
		} else if !isFSVolume && targetIsDir {
			return true, status.Errorf(codes.AlreadyExists, "Required raw block volume but target {%s} is mounted and it is a directory.", targetPathWithHostPrefix)
		}
		logger.Warningf("Idempotent case : targetPath already mounted (%s), so no need to mount again. Finish NodePublishVolume", targetPathWithHostPrefix)
		return true, nil
	}
}

func (d *NodeService) nodePublishVolumeRequestValidation(req *csi.NodePublishVolumeRequest) (codes.Code, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return codes.InvalidArgument, &RequestValidationError{"Volume ID not provided"}
	}

	target := req.GetTargetPath()
	if len(target) == 0 {
		return codes.InvalidArgument, &RequestValidationError{"Target path not provided"}
	}

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return codes.InvalidArgument, &RequestValidationError{"Volume capability not provided"}
	}

	source := req.GetStagingTargetPath()
	if len(source) == 0 {
		return codes.FailedPrecondition, &RequestValidationError{"Staging target not provided"}
	}

	if !isValidVolumeCapabilitiesAccessMode([]*csi.VolumeCapability{volCap}) {
		return codes.InvalidArgument, &RequestValidationError{"Volume capability AccessMode not supported"}
	}

	// If the access type is not mount and not block, should never happen
	switch volCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Mount:
	case *csi.VolumeCapability_Block:
	default:
		return codes.InvalidArgument, &RequestValidationError{"Volume Access Type is not supported"}
	}

	return codes.Internal, nil
}

func (d *NodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))
	volumeID := req.GetVolumeId()

	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	err := d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodeUnpublishVolume")
	if err != nil {
		logger.Errorf("Another operation is being perfomed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodeUnpublishVolume")

	target := req.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}
	targetPathWithHostPrefix := d.NodeUtils.GetPodPath(target)

	logger.Debugf("Check if target file exists %s", targetPathWithHostPrefix)
	if !d.NodeUtils.IsPathExists(targetPathWithHostPrefix) {
		logger.Warningf("Idempotent case: target file %s doesn't exist", targetPathWithHostPrefix)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// TODO replace with newer mount point check + unmountwittimeout

	// Unmount and delete mount point file/folder
	logger.Debugf("Check if target %s is mounted", targetPathWithHostPrefix)
	isNotMounted, err := d.NodeUtils.IsNotMountPoint(targetPathWithHostPrefix)
	if err != nil {
		logger.Errorf("Check is target mounted failed. Target : %q, err : %v", targetPathWithHostPrefix, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !isNotMounted {
		err = d.Mounter.Unmount(target)
		if err != nil {
			logger.Errorf("Unmount failed. Target : %q, err : %v", target, err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	logger.Debugf("Unmount finished. Target : {%s}", target)
	if err = d.NodeUtils.RemoveFileOrDirectory(targetPathWithHostPrefix); err != nil {
		logger.Errorf("Failed to remove mount path file/directory. Target %s: %v", targetPathWithHostPrefix, err)
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("Mount point deleted. Target : %s", targetPathWithHostPrefix)

	return &csi.NodeUnpublishVolumeResponse{}, nil

}

// Structural pattern matching to ensure accurate device name handling across all Linux layers
var nvmeStatsControllerPattern = regexp.MustCompile(`^nvme\d+c\d+n\d+$`)

func (d *NodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	defer logger.Exit(logger.Enter(req))
	volumeId := req.VolumeId
	goid_info.SetAdditionalIDInfo(volumeId)
	defer goid_info.DeleteAdditionalIDInfo()
	volumePath := req.VolumePath
	volumePathWithHostPrefix := d.NodeUtils.GetPodPath(volumePath)

	err := d.nodeGetVolumeStatsRequestValidation(volumeId, volumePath)
	if err != nil {
		return nil, err
	}

	isPathExists := d.NodeUtils.IsPathExists(volumePathWithHostPrefix)
	if !isPathExists {
		return nil, status.Errorf(codes.NotFound, "volume path %q does not exist", volumePath)
	}

	volumeStats, err := d.getVolumeStats(ctx, volumePathWithHostPrefix, volumeId)
	if err != nil {
		return nil, err
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: volumeStats.AvailableBytes,
				Total:     volumeStats.TotalBytes,
				Used:      volumeStats.UsedBytes,
			},
			{
				Unit:      csi.VolumeUsage_INODES,
				Available: volumeStats.AvailableInodes,
				Total:     volumeStats.TotalInodes,
				Used:      volumeStats.UsedInodes,
			},
		},
	}, nil
}

func (d *NodeService) nodeGetVolumeStatsRequestValidation(volumeId string, volumePath string) error {
	if volumeId == "" {
		return status.Error(codes.InvalidArgument, "NodeGetVolumeStats Volume ID must be provided")
	}
	if volumePath == "" {
		return status.Error(codes.InvalidArgument, "NodeGetVolumeStats Volume Path must be provided")
	}

	return nil
}

func (d *NodeService) getVolumeStats(ctx context.Context, path string, volumeId string) (VolumeStatistics, error) {
	if err := ctx.Err(); err != nil {
		return VolumeStatistics{}, err
	}

	// 1. Determine if the target Kubelet mount path is configured as a Raw Block volume
	isBlock, err := d.NodeUtils.IsBlock(ctx, path)
	if err != nil {
		return VolumeStatistics{}, status.Errorf(codes.Internal, "Failed to determine if %q is block device: %s", path, err)
	}

	// 2. Handle Block Volume Pipeline (Raw Block Volumes)
	if isBlock {
		volumeStats, err := d.NodeUtils.GetBlockVolumeStats(ctx, path)
		if err != nil {
			if _, ok := err.(*device_connectivity.MultipathDeviceNotFoundForVolumeError); ok {
				return VolumeStatistics{}, status.Errorf(codes.NotFound, "Multipath device of volume id %q does not exist", volumeId)
			}
			return VolumeStatistics{}, status.Errorf(codes.Internal, "Error while discovering the device: %s", err)
		}
		return volumeStats, nil
	}

	// 3. Handle Filesystem Volume Pipeline (Standard Mounts)
	volumeUuid := d.NodeUtils.GetVolumeUuid(volumeId)
	
	isVolumePathMatchesVolumeId, err := d.OsDeviceConnectivityHelper.IsVolumePathMatchesVolumeId(ctx, volumeUuid, path)
	if err != nil {
		return VolumeStatistics{}, status.Errorf(codes.Internal,
			"Failed to determine if volume id [%q] is accessible on volume path [%q], error: %s", volumeId, path, err)
	}
	if !isVolumePathMatchesVolumeId {
		return VolumeStatistics{}, status.Errorf(codes.NotFound, "Volume id [%q] is not accessible on volume path [%q]", volumeId, path)
	}

	volumeStats, err := d.NodeUtils.GetFileSystemVolumeStats(ctx, path)
	if err != nil {
		return VolumeStatistics{}, status.Errorf(codes.Internal, "Failed to get statistics: %s", err)
	}

	return volumeStats, nil
}

func (d *NodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))

	err := d.nodeExpandVolumeRequestValidation(req)
	if err != nil {
		return nil, err
	}

	volumeID := req.GetVolumeId()
	err = d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodeExpandVolume")
	if err != nil {
		logger.Errorf("Another operation is being performed on volume: {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodeExpandVolume")

	volumeUuid := d.NodeUtils.GetVolumeUuid(volumeID)
	volumePath := req.VolumePath
	volumePathWithHostPrefix := d.NodeUtils.GetPodPath(volumePath)

	device, err := d.OsDeviceConnectivityHelper.GetExistingMpathDevice(ctx, volumeUuid, volumePathWithHostPrefix)
	if err != nil {
		logger.Errorf("Error while discovering the device: {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("Discovered device: {%v}", device)

	baseDevice := path.Base(device)
	nvmeType, err := d.NodeUtils.DevicesAreNvme(ctx, baseDevice)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to determine device type for %s: %v", baseDevice, err)
	}

	switch nvmeType {
	case NVMeNative:
		// Native NVMe → The kernel updates the block sizing directly on namespace updates.
		// No multipath layer or manual controller rescan is required.
		logger.Infof("Device %s is native NVMe: skipping multipath expand/rescan", baseDevice)

	case NVMeNonNative:
		// FIXED: Non-native NVMe over DM MUST rescan its NVMe fabric controllers before resizing DM!
		logger.Infof("Device %s is non-native NVMe: initiating NVMe controller rescan prior to DM resize", baseDevice)
		sysDevices, err := d.NodeUtils.GetSysDevicesFromMpath(ctx, baseDevice)
		if err != nil {
			logger.Errorf("Error getting underlying paths for NVMe-DM device %s: %v", baseDevice, err)
			return nil, status.Error(codes.Internal, err.Error())
		}
		
		// This must iterate through the nvme session layers and trigger /sys/class/nvme/nvmeX/rescan
		err = d.NodeUtils.RescanNvmeControllers(ctx, sysDevices)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to rescan underlying NVMe paths: %v", err)
		}

		err = d.NodeUtils.ExpandMpathDevice(ctx, baseDevice)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

	case NotNVMe:
		// Standard SCSI (FC/iSCSI) -> Rescan physical paths via sysfs, then expand the multipath map
		sysDevices, err := d.NodeUtils.GetSysDevicesFromMpath(ctx, baseDevice)
		if err != nil {
			logger.Errorf("Error getting sys devices for %s: %v", baseDevice, err)
			return nil, status.Error(codes.Internal, err.Error())
		}

		err = d.NodeUtils.RescanPhysicalDevices(ctx, sysDevices)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		err = d.NodeUtils.ExpandMpathDevice(ctx, baseDevice)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

	default:
		return nil, status.Errorf(codes.Internal, "Unknown NVMe type for device %s", baseDevice)
	}

	// FIXED: Isolate raw block volumes to protect against file system parsing crashes and corruption
	isBlock, err := d.NodeUtils.IsBlock(ctx, volumePathWithHostPrefix)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed block check validation for path %s: %v", volumePathWithHostPrefix, err)
	}
	if isBlock {
		logger.Infof("Volume %s detected as Raw Block volume mode. Skipping filesystem expansion steps completely.", volumeID)
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	existingFormat, err := d.Mounter.GetDiskFormat(device)
	if err != nil {
		logger.Errorf("Could not determine if disk {%v} is formatted, error: %v", device, err)
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Warningf("Detected storage format: %s", existingFormat)

	// FIXED: Ensure staging paths are sanitized using the host path prefix utility wrapper
	rawMountPoint := req.GetStagingTargetPath()
	if rawMountPoint == "" {
		rawMountPoint = req.GetVolumePath()
	}
	mountPointToExpand := d.NodeUtils.GetPodPath(rawMountPoint)

	err = d.NodeUtils.ExpandFilesystem(ctx, device, mountPointToExpand, existingFormat)
	if err != nil {
		logger.Errorf("Could not resize {%v} file system of {%v}, error: %v", existingFormat, device, err)
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}

func (d *NodeService) nodeExpandVolumeRequestValidation(req *csi.NodeExpandVolumeRequest) error {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		err := &RequestValidationError{"Volume ID not provided"}
		return status.Error(codes.InvalidArgument, err.Error())
	}

	if !strings.Contains(volumeID, d.ConfigYaml.Parameters.Object_id_info.Delimiter) {
		errMsg := fmt.Sprintf("invalid Volume ID - no {%v} found", d.ConfigYaml.Parameters.Object_id_info.Delimiter)
		err := &RequestValidationError{errMsg}
		return status.Error(codes.NotFound, err.Error())
	}

	volumePath := req.GetVolumePath()
	if volumePath == "" {
		err := &RequestValidationError{"Volume path not provided"}
		return status.Error(codes.InvalidArgument, err.Error())
	}

	return nil
}

func (d *NodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	defer logger.Exit(logger.Enter(req))

	var caps []*csi.NodeServiceCapability
	for _, cap := range nodeCaps {
		c := &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: cap,
				},
			},
		}
		caps = append(caps, c)
	}
	return &csi.NodeGetCapabilitiesResponse{Capabilities: caps}, nil
}

func (d *NodeService) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	defer logger.Exit(logger.Enter(req))

	var nvmeNQN string
	var fcWWNs []string
	var iscsiIQN string
	var err error

	topologyLabels, err := d.NodeUtils.GetTopologyLabels(ctx, d.Hostname)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("discovered topology labels : %v", topologyLabels)

	nvmeExists := d.NodeUtils.IsPathExists(NvmeFullPath)
	if nvmeExists {
		nvmeNQN, err = d.NodeUtils.ReadNvmeNqn()
		if err != nil {
			logger.Warning(err)
		}
	}

	fcExists := d.NodeUtils.IsFCExists()
	if fcExists {
		fcWWNs, err = d.NodeUtils.ParseFCPorts()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	iscsiExists := d.NodeUtils.IsPathExists(IscsiFullPath)
	if iscsiExists {
		iscsiIQN, err = d.NodeUtils.ParseIscsiInitiators()
		if err != nil {
			logger.Warning(err)
		}
	}

	if nvmeNQN == "" && fcWWNs == nil && iscsiIQN == "" {
		err := fmt.Errorf("Cannot find valid nvme nqn, fc wwns or iscsi iqn")
		return nil, status.Error(codes.Internal, err.Error())
	}

	var nodeId = d.Hostname
	err = d.NodeUtils.UpdateNodeInitiatorsAnnotation(ctx, d.Hostname, nvmeNQN, fcWWNs, iscsiIQN)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Debugf("node id is : %s", nodeId)

	return &csi.NodeGetInfoResponse{
		NodeId:             nodeId,
		AccessibleTopology: &csi.Topology{Segments: topologyLabels},
	}, nil
}

func isValidVolumeCapabilitiesAccessMode(volCaps []*csi.VolumeCapability) bool {
	hasSupport := func(cap *csi.VolumeCapability) bool {
		for _, c := range volumeCaps {
			if c.GetMode() == cap.AccessMode.GetMode() {
				return true
			}
		}
		return false
	}

	foundAll := true
	for _, c := range volCaps {
		if !hasSupport(c) {
			foundAll = false
			break
		}
	}

	return foundAll
}


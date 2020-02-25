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
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/ibm/ibm-block-csi-driver/node/goid_info"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubernetes/pkg/util/mount"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	nodeCaps = []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
	}

	// volumeCaps represents how the volume could be accessed.
	// It is SINGLE_NODE_WRITER since EBS volume could only be
	// attached to a single node at any given time.
	volumeCaps = []csi.VolumeCapability_AccessMode{
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}

	defaultFSType              = "ext4"
	StageInfoFilename          = ".stageInfo.json"
	supportedConnectivityTypes = map[string]bool{
		"iscsi": true,
		"fc":    true,
		// TODO add nvme later on
	}

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
}

// nodeService represents the node service of CSI driver
type NodeService struct {
	Mounter                     NodeMounter
	ConfigYaml                  ConfigFile
	Hostname                    string
	NodeUtils                   NodeUtilsInterface
	executer                    executer.ExecuterInterface
	VolumeIdLocksMap            SyncLockInterface
	OsDeviceConnectivityMapping map[string]device_connectivity.OsDeviceConnectivityInterface
}

// newNodeService creates a new node service
// it panics if failed to create the service
func NewNodeService(configYaml ConfigFile, hostname string, nodeUtils NodeUtilsInterface, OsDeviceConnectivityMapping map[string]device_connectivity.OsDeviceConnectivityInterface, executer executer.ExecuterInterface, mounter NodeMounter, syncLock SyncLockInterface) NodeService {
	return NodeService{
		ConfigYaml:                  configYaml,
		Hostname:                    hostname,
		NodeUtils:                   nodeUtils,
		executer:                    executer,
		OsDeviceConnectivityMapping: OsDeviceConnectivityMapping,
		Mounter:                     mounter,
		VolumeIdLocksMap:            syncLock,
	}
}

func (d *NodeService) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	goid_info.SetAdditionalIDInfo(req.VolumeId)
	defer goid_info.DeleteAdditionalIDInfo()
	logger.Debugf(">>>> NodeStageVolume: called with args %+v", *req)
	defer logger.Debugf("<<<< NodeStageVolume")

	err := d.nodeStageVolumeRequestValidation(req)
	if err != nil {
		switch err.(type) {
		case *RequestValidationError:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	volId := req.VolumeId
	err = d.VolumeIdLocksMap.AddVolumeLock(volId, "NodeStageVolume")
	if err != nil {
		logger.Errorf("Another operation is being perfomed on volume : {%s}.", volId)
		return nil, status.Error(codes.Aborted, err.Error())
	}

	defer d.VolumeIdLocksMap.RemoveVolumeLock(volId, "NodeStageVolume")

	connectivityType, lun, arrayInitiators, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext, d.ConfigYaml)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	stagingPath := req.GetStagingTargetPath() // e.g in k8s /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-21967c74-b456-11e9-b93e-005056a45d5f/globalmount

	osDeviceConnectivity, ok := d.OsDeviceConnectivityMapping[connectivityType]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Wrong connectivity type %s", connectivityType))
	}

	err = osDeviceConnectivity.RescanDevices(lun, arrayInitiators)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	device, err := osDeviceConnectivity.GetMpathDevice(volId, lun, arrayInitiators)
	logger.Debugf("Discovered device : {%v}", device)
	if err != nil {
		logger.Errorf("Error while discovring the device : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// TODO move stageInfo into the node_until API
	// Generate the stageInfo detail
	stageInfoPath := path.Join(stagingPath, StageInfoFilename)
	stageInfo := make(map[string]string)
	baseDevice := path.Base(device)
	stageInfo["mpathDevice"] = baseDevice //this should return the mathhh for example
	sysDevices, err := d.NodeUtils.GetSysDevicesFromMpath(baseDevice)
	if err != nil {
		logger.Errorf("Error while trying to get sys devices : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	stageInfo["sysDevices"] = sysDevices // like sda,sdb,...
	stageInfo["connectivity"] = connectivityType

	// checking idempotent case if the stageInfoPath file is already exist
	if d.NodeUtils.StageInfoFileIsExist(stageInfoPath) {
		// means the file already exist
		logger.Warningf("Idempotent case: stage info file exist - indicates that node stage was already done on this path. Verify its content...")
		// lets read the file and comprae the stageInfo
		existingStageInfo, err := d.NodeUtils.ReadFromStagingInfoFile(stageInfoPath)
		if err != nil {
			logger.Warningf("Could not read and compare the info inside the staging info file. error : {%v}", err)
		} else {
			logger.Warningf("Idempotent case: check if stage info file is as expected. stage info is {%v} vs expected {%v}", existingStageInfo, stageInfo)

			if (stageInfo["mpathDevice"] != existingStageInfo["mpathDevice"]) ||
				(stageInfo["sysDevices"] != existingStageInfo["sysDevices"]) ||
				(stageInfo["connectivity"] != existingStageInfo["connectivity"]) {
				logger.Errorf("Stage info is not as expected. expected:  {%v}. got : {%v}", stageInfo, existingStageInfo)
				return nil, status.Error(codes.AlreadyExists, err.Error())
			}
			logger.Warningf("Idempotent case: stage info file is the same as expected. NodeStageVolume Finished: multipath device is ready [%s] to be mounted by NodePublishVolume API.", baseDevice)
			return &csi.NodeStageVolumeResponse{}, nil
		}
	}

	if err := d.NodeUtils.WriteStageInfoToFile(stageInfoPath, stageInfo); err != nil {
		logger.Errorf("Error while trying to save the stage metadata file: {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Debugf("NodeStageVolume Finished: multipath device is ready [%s] to be mounted by NodePublishVolume API.", baseDevice)
	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *NodeService) nodeStageVolumeRequestValidation(req *csi.NodeStageVolumeRequest) error {

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return &RequestValidationError{"Volume ID not provided"}
	}

	target := req.GetStagingTargetPath()
	if len(target) == 0 {
		return &RequestValidationError{"Staging target not provided"}
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

	connectivityType, lun, arrayInitiators, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext, d.ConfigYaml)
	if err != nil {
		return &RequestValidationError{fmt.Sprintf("Fail to parse PublishContext %v with err = %v", req.PublishContext, err)}
	}

	if _, ok := supportedConnectivityTypes[connectivityType]; !ok {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong connectivity type %s. Supported connectivities %v", connectivityType, supportedConnectivityTypes)}
	}

	if lun < 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong lun id %d.", lun)}
	}

	if len(arrayInitiators) == 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong arrayInitiators %s.", arrayInitiators)}
	}

	return nil
}

func (d *NodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	goid_info.SetAdditionalIDInfo(volumeID)
	defer goid_info.DeleteAdditionalIDInfo()
	logger.Debugf(">>>> NodeUnstageVolume: called with args %+v", *req)
	defer logger.Debugf("<<<< NodeUnstageVolume")

	if len(volumeID) == 0 {
		logger.Errorf("Volume ID not provided")
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	err := d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodeUnstageVolume")
	if err != nil {
		logger.Errorf("Another operation is being perfomed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodeUnstageVolume")

	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		logger.Errorf("Staging target not provided")
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	logger.Debugf("Reading stage info file")
	stageInfoPath := path.Join(stagingTargetPath, StageInfoFilename)
	infoMap, err := d.NodeUtils.ReadFromStagingInfoFile(stageInfoPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warningf("Idempotent case : stage info file does not exist. Finish NodeUnstageVolume OK.")
			return &csi.NodeUnstageVolumeResponse{}, nil
		} else {
			logger.Errorf("Error while trying to read from the staging info file : {%v}", err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	logger.Debugf("Reading stage info file detail : {%v}", infoMap)

	connectivityType := infoMap["connectivity"]
	mpathDevice := infoMap["mpathDevice"]
	sysDevices := strings.Split(infoMap["sysDevices"], ",")

	logger.Debugf("Got info from stageInfo file. connectivity : {%v}. device : {%v}, sysDevices : {%v}", connectivityType, mpathDevice, sysDevices)

	osDeviceConnectivity, ok := d.OsDeviceConnectivityMapping[connectivityType]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Wrong connectivity type %s", connectivityType))
	}

	err = osDeviceConnectivity.FlushMultipathDevice(mpathDevice)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Multipath -f command failed with error: %v", err)
	}
	err = osDeviceConnectivity.RemovePhysicalDevice(sysDevices)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Remove iscsi device failed with error: %v", err)
	}

	if err := d.NodeUtils.ClearStageInfoFile(stageInfoPath); err != nil {
		return nil, status.Errorf(codes.Internal, "Fail to clear the stage info file: error %v", err)
	}

	logger.Debugf("NodeUnStageVolume Finished: multipath device removed from host")

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *NodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	goid_info.SetAdditionalIDInfo(req.VolumeId)
	defer goid_info.DeleteAdditionalIDInfo()
	logger.Debugf(">>>> NodePublishVolume: called with args %+v", *req)
	defer logger.Debugf("<<<< NodePublishVolume")

	err := d.nodePublishVolumeRequestValidation(req)
	if err != nil {
		switch err.(type) {
		case *RequestValidationError:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	volumeID := req.VolumeId

	err = d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodePublishVolume")
	if err != nil {
		logger.Errorf("Another operation is being perfomed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodePublishVolume")

	// checking if the node staging path was mpounted into
	stagingPath := req.GetStagingTargetPath()
	targetPath := req.GetTargetPath()
	targetPathWithHostPrefix := d.NodeUtils.GetPodPath(targetPath)

	logger.Debugf("stagingPath : {%v}, targetPath : {%v}", stagingPath, targetPath)

	// Read staging info file in order to find the mpath device for mounting.
	stageInfoPath := path.Join(stagingPath, StageInfoFilename)
	infoMap, err := d.NodeUtils.ReadFromStagingInfoFile(stageInfoPath)
	if err != nil {
		// Note: after validation it looks like k8s create the directory in advance. So we don't try to remove it at the Unpublish
		logger.Errorf("Error while trying to read from the staging info file : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	mpathDevice := filepath.Join(device_connectivity.DevPath, infoMap["mpathDevice"])
	logger.Debugf("Got info from stageInfo file. device : {%v}", mpathDevice)

	// if the device is not mounted then we are mounting it.
	volumeCap := req.GetVolumeCapability()
	isFSVolume := true
	switch volumeCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		isFSVolume = false
	}
	isTargetPathExists := d.NodeUtils.IsPathExists(targetPathWithHostPrefix)
	if isTargetPathExists {
		// check if already mounted
		isMounted, err := d.isTargetMounted(targetPathWithHostPrefix, isFSVolume)
		if err != nil {
			logger.Debugf("Existing mount check failed {%v}", err.Error())
			return nil, err
		}
		if isMounted { // idempotent case
			return &csi.NodePublishVolumeResponse{}, nil
		}
	}
	if isFSVolume {
		fsType := volumeCap.GetMount().FsType
		err = d.mountFileSystemVolume(mpathDevice, targetPath, fsType, isTargetPathExists)
	} else {
		err = d.mountRawBlockVolume(mpathDevice, targetPath, isTargetPathExists)
	}

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("NodePublishVolume Finished: multipath device is now mounted to targetPath.")

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *NodeService) mountFileSystemVolume(mpathDevice string, targetPath string, fsType string, isTargetPathExists bool) error {
	if fsType == "" {
		fsType = defaultFSType
	}
	logger.Debugf("Volume will have FS type : {%v}", fsType)
	targetPathWithHostPrefix := d.NodeUtils.GetPodPath(targetPath)
	if !isTargetPathExists {
		logger.Debugf("Target path directory does not exist. Creating : {%v}", targetPathWithHostPrefix)
		err := d.Mounter.MakeDir(targetPathWithHostPrefix)
		if err != nil {
			return status.Errorf(codes.Internal, "Could not create directory %q: %v", targetPathWithHostPrefix, err)
		}
	}
	logger.Debugf("Mount the device with fs_type = {%v} (Create filesystem if needed)", fsType)
	return d.Mounter.FormatAndMount(mpathDevice, targetPath, fsType, nil) // Passing without /host because k8s mounter uses mount\mkfs\fsck
}

func (d *NodeService) mountRawBlockVolume(mpathDevice string, targetPath string, isTargetPathExists bool) error {
	logger.Debugf("Raw block volume will be created")
	targetPathWithHostPrefix := d.NodeUtils.GetPodPath(targetPath)
	// Create mount file and its parent directory if they don't exist
	targetPathParentDirWithHostPrefix := filepath.Dir(targetPathWithHostPrefix)
	if !d.NodeUtils.IsPathExists(targetPathParentDirWithHostPrefix) {
		logger.Debugf("Target path parent directory does not exist. creating : {%v}", targetPathParentDirWithHostPrefix)
		err := d.Mounter.MakeDir(targetPathParentDirWithHostPrefix)
		if err != nil {
			return status.Errorf(codes.Internal, "Could not create directory %q: %v", targetPathParentDirWithHostPrefix, err)
		}
	}
	if !isTargetPathExists {
		logger.Debugf("Target path file does not exist. creating : {%v}", targetPathWithHostPrefix)
		err := d.Mounter.MakeFile(targetPathWithHostPrefix)
		if err != nil {
			return status.Errorf(codes.Internal, "Could not create file %q: %v", targetPathWithHostPrefix, err)
		}
	}

	// Mount
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
		logger.Warningf("Failed to check if (%s), is mounted.", targetPathWithHostPrefix)
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
		logger.Warningf("Idempotent case : targetPath already mounted (%s), so no need to mount again. Finish NodePublishVolume.", targetPathWithHostPrefix)
		return true, nil
	}
}

func (d *NodeService) nodePublishVolumeRequestValidation(req *csi.NodePublishVolumeRequest) error {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return &RequestValidationError{"Volume ID not provided"}
	}

	source := req.GetStagingTargetPath()
	if len(source) == 0 {
		return &RequestValidationError{"Staging target not provided"}
	}

	target := req.GetTargetPath()
	if len(target) == 0 {
		return &RequestValidationError{"Target path not provided"}
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

	return nil
}

func (d *NodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	goid_info.SetAdditionalIDInfo(volumeID)
	defer goid_info.DeleteAdditionalIDInfo()
	logger.Debugf(">>>> NodeUnpublishVolume: called with args %+v", *req)
	defer logger.Debugf("<<<< NodeUnpublishVolume")

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

	// Unmount and delete mount point file/folder
	logger.Debugf("Check if target %s is mounted", targetPathWithHostPrefix)
	isNotMounted, err := d.NodeUtils.IsNotMountPoint(targetPathWithHostPrefix)
	if err != nil {
		logger.Errorf("Check is target mounted failed. Target : %q, err : %v", targetPathWithHostPrefix, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !isNotMounted {
		logger.Debugf("Unmounting %s", target)
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

func (d *NodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	goid_info.SetAdditionalIDInfo(req.VolumeId)
	defer goid_info.DeleteAdditionalIDInfo()
	return nil, status.Error(codes.Unimplemented, "NodeGetVolumeStats is not implemented yet")
}

func (d *NodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	goid_info.SetAdditionalIDInfo(req.VolumeId)
	defer goid_info.DeleteAdditionalIDInfo()
	return nil, status.Error(codes.Unimplemented, fmt.Sprintf("NodeExpandVolume is not yet implemented"))
}

func (d *NodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	logger.Debugf(">>>> NodeGetCapabilities: called with args %+v", *req)
	defer logger.Debugf("<<<< NodeGetCapabilities")

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
	logger.Debugf(">>>> NodeGetInfo: called with args %+v", *req)
	defer logger.Debugf("<<<< NodeGetInfo")

	var iscsiIQN string
	var fcWWNs []string
	var err error

	fcExists := d.NodeUtils.IsPathExists(FCPath)
	if fcExists {
		fcWWNs, err = d.NodeUtils.ParseFCPorts()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	iscsiExists := d.NodeUtils.IsPathExists(IscsiFullPath)
	if iscsiExists {
		iscsiIQN, _ = d.NodeUtils.ParseIscsiInitiators()
	}

	if fcWWNs == nil && iscsiIQN == "" {
		err := fmt.Errorf("Cannot find valid fc wwns or iscsi iqn")
		return nil, status.Error(codes.Internal, err.Error())
	}

	delimiter := ";"
	fcPorts := strings.Join(fcWWNs, ":")
	nodeId := d.Hostname + delimiter + iscsiIQN + delimiter + fcPorts
	logger.Debugf("node id is : %s", nodeId)

	return &csi.NodeGetInfoResponse{
		NodeId: nodeId,
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

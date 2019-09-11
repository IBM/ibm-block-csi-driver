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
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/ibm/ibm-block-csi-driver/node/goid_info"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"github.com/ibm/ibm-block-csi-driver/node/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubernetes/pkg/util/mount"
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
	stageInfoFilename          = ".stageInfo.json"
	supportedConnectivityTypes = map[string]bool{
		"iscsi": true,
		// TODO add fc \ nvme later on
	}

	IscsiFullPath = "/etc/iscsi/initiatorname.iscsi"
)

const (
	// In the Dockerfile of the node, specific commands (e.g: multipath, mount...) from the host mounted inside the container in /host directory.
	// Command lines inside the container will show /host prefix.
	PrefixChrootOfHostRoot = "/host"
	FCPath = "/sys/class/fc_host"
	FCPortPath = "/sys/class/fc_host/host*/port_name"
)

// nodeService represents the node service of CSI driver
type NodeService struct {
	mounter                     *mount.SafeFormatAndMount
	ConfigYaml                  ConfigFile
	Hostname                    string
	NodeUtils                   NodeUtilsInterface
	executer                    executer.ExecuterInterface
	VolumeIdLocksMap            SyncLockInterface
	OsDeviceConnectivityMapping map[string]device_connectivity.OsDeviceConnectivityInterface
}

// newNodeService creates a new node service
// it panics if failed to create the service
func NewNodeService(configYaml ConfigFile, hostname string, nodeUtils NodeUtilsInterface, OsDeviceConnectivityMapping map[string]device_connectivity.OsDeviceConnectivityInterface, executer executer.ExecuterInterface, mounter *mount.SafeFormatAndMount, syncLock SyncLockInterface) NodeService {
	return NodeService{
		ConfigYaml:                  configYaml,
		Hostname:                    hostname,
		NodeUtils:                   nodeUtils,
		executer:                    executer,
		OsDeviceConnectivityMapping: OsDeviceConnectivityMapping,
		mounter:                     mounter,
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

	connectivityType, lun, array_iqns, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext, d.ConfigYaml)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	stagingPath := req.GetStagingTargetPath() // e.g in k8s /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-21967c74-b456-11e9-b93e-005056a45d5f/globalmount

	osDeviceConnectivity, ok := d.OsDeviceConnectivityMapping[connectivityType]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Wrong connectivity type %s", connectivityType))
	}

	err = osDeviceConnectivity.RescanDevices(lun, array_iqns)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	device, err := osDeviceConnectivity.GetMpathDevice(volId, lun, array_iqns)
	logger.Debugf("Discovered device : {%v}", device)
	if err != nil {
		logger.Errorf("Error while discovring the device : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// TODO move stageInfo into the node_until API
	// Generate the stageInfo detail
	stageInfoPath := path.Join(stagingPath, stageInfoFilename)
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

	// If the access type is block, do nothing for stage
	switch volCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		return &RequestValidationError{"Volume Access Type Block is not supported yet"}
	}

	connectivityType, lun, array_iqns, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext, d.ConfigYaml)
	if err != nil {
		return &RequestValidationError{fmt.Sprintf("Fail to parse PublishContext %v with err = %v", req.PublishContext, err)}
	}

	if _, ok := supportedConnectivityTypes[connectivityType]; !ok {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong connectivity type %s. Supported connectivities %v", connectivityType, supportedConnectivityTypes)}
	}

	if lun < 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong lun id %d.", lun)}
	}

	if len(array_iqns) == 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong array_iqn %s.", array_iqns)}
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
	stageInfoPath := path.Join(stagingTargetPath, stageInfoFilename)
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
	targetPathWithHostPrefix := path.Join(PrefixChrootOfHostRoot, targetPath)

	logger.Debugf("stagingPath : {%v}, targetPath : {%v}", stagingPath, targetPath)

	// Read staging info file in order to find the mpath device for mounting.
	stageInfoPath := path.Join(stagingPath, stageInfoFilename)
	infoMap, err := d.NodeUtils.ReadFromStagingInfoFile(stageInfoPath)
	if err != nil {
		// Note: after validation it looks like k8s create the directory in advance. So we don't try to remove it at the Unpublish
		logger.Errorf("Error while trying to read from the staging info file : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	mpathDevice := filepath.Join(device_connectivity.DevPath, infoMap["mpathDevice"])
	logger.Debugf("Got info from stageInfo file. device : {%v}", mpathDevice)

	logger.Debugf("Check if targetPath {%s} exist in mount list", targetPath)
	mountList, err := d.mounter.List()
	for _, mount := range mountList {
		logger.Tracef("Check if mount path({%v}) [with device({%v})] is equel to targetPath {%s}", mount.Path, mount.Device, targetPath)
		if mount.Path == targetPathWithHostPrefix {
			if mount.Device == mpathDevice {
				logger.Warningf("Idempotent case : targetPath already mounted (%s), so no need to mount again. Finish NodePublishVolume.", targetPath)
				return &csi.NodePublishVolumeResponse{}, nil
			} else {
				return nil, status.Errorf(codes.AlreadyExists, "Mount point is already mounted to but with different multipath device (%s), while the expected device is %s ", mount.Device, mpathDevice)
			}
		}
	}

	// if the device is not mounted then we are mounting it.

	volumeCap := req.GetVolumeCapability()
	fsType := volumeCap.GetMount().FsType

	if fsType == "" {
		fsType = defaultFSType
	}

	if _, err := os.Stat(targetPathWithHostPrefix); os.IsNotExist(err) {
		logger.Debugf("Target path directory does not exist. creating : {%v}", targetPathWithHostPrefix)
		d.mounter.MakeDir(targetPathWithHostPrefix)
	}

	logger.Debugf("Mount the device with fs_type = {%v} (Create filesystem if needed)", fsType)

	logger.Debugf("FormatAndMount start [goid=%d]", util.GetGoID())
	err = d.mounter.FormatAndMount(mpathDevice, targetPath, fsType, nil) // Passing without /host because k8s mounter uses mount\mkfs\fsck
	// TODO: pass mount options
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("FormatAndMount end [goid=%d]", util.GetGoID())

	logger.Debugf("NodePublishVolume Finished: multipath device is now mounted to targetPath.")

	return &csi.NodePublishVolumeResponse{}, nil
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

	// If the access type is block, do nothing for stage
	switch volCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		return &RequestValidationError{"Volume Access Type Block is not supported yet"}
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

	logger.Debugf("NodeUnpublishVolume: unmounting %s", target)
	err = d.mounter.Unmount(target)
	if err != nil {
		if strings.Contains(err.Error(), "not mounted") {
			logger.Warningf("Idempotent case - target was already unmounted %s", target)
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "Could not unmount %q: %v", target, err)
	}

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

	fcExists := d.NodeUtils.Exists(FCPath)
	if fcExists {
		fcWWNs, err = d.NodeUtils.ParseFCPorts()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	iscsiExists := d.NodeUtils.Exists(IscsiFullPath)
	if iscsiExists {
		iscsiIQN, _ = d.NodeUtils.ParseIscsiInitiators()
	}

	if fcWWNs == nil && iscsiIQN == "" {
		err := fmt.Errorf("Cannot find valid fc wwns or iscsi iqn")
		return nil,status.Error(codes.Internal, err.Error())
	}

	delimiter := ";"
	fcPorts := strings.Join(fcWWNs, ":")
	nodeId := d.Hostname + delimiter + iscsiIQN + delimiter +fcPorts
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

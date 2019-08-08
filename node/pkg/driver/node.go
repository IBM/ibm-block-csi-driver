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

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	device_connectivity "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
	mount "k8s.io/kubernetes/pkg/util/mount"
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
		// TODO add fc later on
	}
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
	klog.V(5).Infof("NodeStageVolume: called with args %+v", *req)

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
	err = d.VolumeIdLocksMap.AddVolumeLock(volId)
	if err != nil {
		klog.Errorf("Another operation is being perfomed on volume : {%s}.", volId)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volId)

	connectivityType, lun, array_iqn, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext, d.ConfigYaml)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	stagingPath := req.GetStagingTargetPath()

	osDeviceConnectivity, ok := d.OsDeviceConnectivityMapping[connectivityType]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Wrong connectivity type %s", connectivityType))
	}

	err = osDeviceConnectivity.RescanDevices(lun, array_iqn)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	device, err := osDeviceConnectivity.GetMpathDevice(volId, lun, array_iqn)
	klog.V(4).Infof("Discovered device : {%v}", device)
	if err != nil {
		klog.Errorf("Error while discovring the device : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	dev, refs, err := mount.GetDeviceNameFromMount(d.mounter, stagingPath)
	klog.V(4).Infof("dev : {%v}. refs : {%v}", dev, refs)
	if err != nil {
		klog.Errorf("Error while trying to get device from mount : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	if refs != 0 {
		dmDevice, err := filepath.EvalSymlinks(dev)
		if err != nil {
			klog.Errorf("Error while reading symlink : {%v}. err : {%v}", dev, err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
		klog.V(4).Infof("comparing dev : {%v} with device : {%v}", dmDevice, device)
		if dmDevice == device {
			klog.V(4).Infof("Returning ok result") // TODO double check
			return &csi.NodeStageVolumeResponse{}, nil
		} else {
			return nil, status.Errorf(codes.AlreadyExists, "Mount point is already mounted to.")
		}
	}

	// if the device is not mounted then we are mounting it.

	volumeCap := req.GetVolumeCapability()
	fsType := volumeCap.GetMount().FsType

	if fsType == "" {
		fsType = defaultFSType
	}

	klog.V(4).Infof("Mount the device with fs_type = {%v}", fsType)

	// creating the stagingPath if it is missing
	if _, err := os.Stat(stagingPath); os.IsNotExist(err) {
		klog.V(4).Infof("Target path directory does not exist. creating : {%v}", stagingPath)
		d.mounter.MakeDir(stagingPath)
	}

	err = d.mounter.FormatAndMount(device, stagingPath, fsType, nil) // TODO: pass mount options
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(4).Infof("Succeeded to verify the ability to mount the mpath device to stagingPath [%s]. Now unmount it from staging path so only the NodePublishVolume will do the mount on the final target path.", stagingPath)
	err = d.mounter.Unmount(stagingPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not unmount target %q: %v during NodeStage mount check.", stagingPath, err)
	}

	stageInfoPath := path.Join(req.GetStagingTargetPath(), stageInfoFilename)
	stageInfo := make(map[string]string)
	baseDevice := path.Base(device)
	stageInfo["mpathDevice"] = baseDevice //this should return the mathhh for example
	sysDevices, err := d.NodeUtils.GetSysDevicesFromMpath(baseDevice)
	if err != nil {
		klog.Errorf("Error while trying to get sys devices : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	stageInfo["sysDevices"] = sysDevices // like sda,sdb,...
	stageInfo["connectivity"] = connectivityType

	if err := d.NodeUtils.WriteStageInfoToFile(stageInfoPath, stageInfo); err != nil{
		klog.Errorf("Error while trying to save the stage metadata file [%s]: {%v}", stageInfoPath, err.Error())
		return nil, status.Error(codes.Internal, err.Error())		
	}

	klog.V(4).Infof("NodeStageVolume Finished: multipath device is ready [%s] and with filesystem [%s] and ready to be mounted on NodePublishVolume API.", baseDevice, fsType)

	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *NodeService) getMountPointFromList(devicePath string, mountList []mount.MountPoint) *mount.MountPoint {
	//klog.V(4).Infof("current device : %v ", devicePath)
	for _, mount := range mountList {
		klog.V(4).Infof("devicePath : {%v}, device : {%v}", devicePath, mount.Device)
		if mount.Device == devicePath {
			klog.V(4).Infof("Found mounted device")
			return &mount
		}
	}
	return nil

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

	connectivityType, lun, array_iqn, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext, d.ConfigYaml)
	if err != nil {
		return &RequestValidationError{fmt.Sprintf("Fail to parse PublishContext %v with err = %v", req.PublishContext, err)}
	}

	if _, ok := supportedConnectivityTypes[connectivityType]; !ok {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong connectivity type %s. Supported connectivities %v", connectivityType, supportedConnectivityTypes)}
	}

	if lun < 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong lun id %d.", lun)}
	}

	if len(array_iqn) == 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong array_iqn %s.", array_iqn)}
	}

	return nil
}

func (d *NodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(5).Infof("NodeUnstageVolume: called with args %+v", *req)
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		klog.Errorf("Volume ID not provided")
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	err := d.VolumeIdLocksMap.AddVolumeLock(volumeID)
	if err != nil {
		klog.Errorf("Another operation is being perfomed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID)

	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		klog.Errorf("Staging target not provided")
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	dev, refs, err := mount.GetDeviceNameFromMount(d.mounter, "/host"+stagingTargetPath)
	if err != nil {
		klog.Errorf("Error while trying to get device from mount : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(4).Infof("dev : {%v}, refs: {%v}", dev, refs)

	if refs == 0 {
		klog.Warningf("Idempotent case : %s target not mounted", stagingTargetPath)
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	klog.V(4).Infof("Reading stage info file %s", stageInfoFilename)
	stageInfoPath := path.Join(req.GetStagingTargetPath(), stageInfoFilename)
	infoMap, err := d.NodeUtils.ReadFromStagingInfoFile(stageInfoPath)
	if err != nil {
		klog.Errorf("Error while trying to read from the staging info file : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	klog.V(4).Infof("Reading stage info file detail : {%v}", infoMap)

	connectivityType := infoMap["connectivity"]
	mpathDevice := infoMap["mpathDevice"]
	sysDevices := strings.Split(infoMap["sysDevices"], ",")

	klog.V(4).Infof("Got info from stageInfo file. connectivity : {%v}. device : {%v}, sysDevices : {%v}", connectivityType, mpathDevice, sysDevices)

	//TODO: there might be an indempotent issue here if we fail after the unmount there will be  faulty devices left
	// (since the next time we run unpstange we will reutrn that everytjing is OK since the path is unmounted)

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

	d.NodeUtils.ClearStageInfoFile(stageInfoPath)

	klog.V(4).Infof("NodeUnStageVolume Finished: multipath device removed from host")

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *NodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.V(5).Infof("NodePublishVolume: called with args %+v", *req)

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

	err = d.VolumeIdLocksMap.AddVolumeLock(volumeID)
	if err != nil {
		klog.Errorf("Another operation is being perfomed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID)

	// checking if the node staging path was mpounted into
	stagingPath := req.GetStagingTargetPath()
	targetPath := req.GetTargetPath()
	klog.V(4).Infof("stagingPath : {%v}, targetPath : {%v}", stagingPath, targetPath)

	mountList, err := d.mounter.List()
	for _, mount := range mountList {
		klog.V(5).Infof("mount device : {%v}, path : {%v} is equel to targetPath {%s}", mount.Device, mount.Path, targetPath)
		if strings.TrimPrefix(mount.Path, "/host") == targetPath {
			// Trim /host due to the mount of the / from the host into the /host mountpoint inside the csi node container.
			klog.Warningf("Idempotent case : targetPath already mounted (%s), so no need to mount again. Finish NodePublishVolume.", targetPath)
			return &csi.NodePublishVolumeResponse{}, nil
		}
	}

	// Read staging info file in order to find the mpath device for mounting.
	stageInfoPath := path.Join(stagingPath, stageInfoFilename)
	infoMap, err := d.NodeUtils.ReadFromStagingInfoFile(stageInfoPath)
	if err != nil {
		klog.Errorf("Error while trying to read from the staging info file : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	klog.V(4).Infof("Reading stage info file detail : {%v}", infoMap)

	mpathDevice := "/dev/" + infoMap["mpathDevice"]
	klog.V(4).Infof("Got info from stageInfo file. device : {%v}", mpathDevice)

	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		klog.V(4).Infof("Target path directory does not exist. creating : {%v}", targetPath)
		d.mounter.MakeDir(targetPath)
	}

	fsType := req.GetVolumeCapability().GetMount().FsType
	if fsType == "" {
		fsType = defaultFSType

	}

	klog.V(4).Infof("Mounting mpath-device=%s to targetPath=%s", mpathDevice, targetPath)
	err = d.mounter.Mount(mpathDevice, targetPath, fsType, nil)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(4).Infof("NodePublishVolume Finished: multipath device is now mounted to targetPath.")

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

	// TODO add verification of volume mode

	return nil
}

func (d *NodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(5).Infof("NodeUnpublishVolume: called with args %+v", *req)
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	err := d.VolumeIdLocksMap.AddVolumeLock(volumeID)
	if err != nil {
		klog.Errorf("Another operation is being perfomed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID)

	target := req.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	klog.V(5).Infof("NodeUnpublishVolume: unmounting %s", target)
	err = d.mounter.Unmount(target)
	if err != nil {
		if strings.Contains(err.Error(), "not mounted") {
			klog.V(4).Infof("Idempotent case - target was already unmounted %s", target)
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "Could not unmount %q: %v", target, err)
	}

	if _, err := os.Stat(target); os.IsNotExist(err) {
		klog.V(4).Infof("Deleting target path [%s] after successfully unmount it.", target)
		if err := os.RemoveAll(target); err != nil {
			klog.Warningf("Fail to remove the target path [%s] after successfully unmount it. Error %v", target, err)
		}
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil

}

func (d *NodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeGetVolumeStats is not implemented yet")
}

func (d *NodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, fmt.Sprintf("NodeExpandVolume is not yet implemented"))
}

func (d *NodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.V(5).Infof("NodeGetCapabilities: called with args %+v", *req)
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
	klog.V(5).Infof("NodeGetInfo: called with args %+v", *req)

	iscsiIQN, err := d.NodeUtils.ParseIscsiInitiators("/etc/iscsi/initiatorname.iscsi")
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	delimiter := ";"

	nodeId := d.Hostname + delimiter + iscsiIQN
	klog.V(4).Infof("node id is : %s", nodeId)

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

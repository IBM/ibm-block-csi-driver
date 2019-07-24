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
	"strings"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	iscsilib "github.com/kubernetes-csi/csi-lib-iscsi/iscsi"
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

	defaultFSType     = "ext4"
	stageInfoFilename = "stageInfo.json"
)

// nodeService represents the node service of CSI driver
type nodeService struct {
	mounter        *mount.SafeFormatAndMount // TODO fix k8s mount import
	configYaml     ConfigFile
	hostname       string
	nodeUtils      NodeUtilsInterface
	newRescanUtils NewRescanUtilsFunction
	executer       ExecutorInterface
}

// newNodeService creates a new node service
// it panics if failed to create the service
func NewNodeService(configYaml ConfigFile, hostname string, nodeUtils NodeUtilsInterface, newRescanUtils NewRescanUtilsFunction, executer ExecutorInterface, mounter *mount.SafeFormatAndMount) nodeService {
	return nodeService{
		configYaml:     configYaml,
		hostname:       hostname,
		nodeUtils:      nodeUtils,
		newRescanUtils: newRescanUtils,
		executer:       executer,

		mounter: mounter,

		//		mounter: &mount.SafeFormatAndMount{
		//			Interface: mount.New(""),
		//			Exec:      mount.NewOsExec(),
		//		},
	}
}

func (d *nodeService) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
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

	// get the volume device
	//get connectivity from publish context
	connectivityType, lun, array_iqn, err := d.nodeUtils.GetInfoFromPublishContext(req.PublishContext, d.configYaml)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	klog.V(4).Infof("connectivityType : %v", connectivityType)

	klog.V(4).Infof("lun : %v", lun)

	klog.V(4).Infof("array_iqn : %v", array_iqn)
	//
	connector := iscsilib.Connector{}
	klog.V(4).Infof("connector : %v", connector)

	rescanUtils, err := d.newRescanUtils(connectivityType, d.nodeUtils, d.executer)

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	err = rescanUtils.RescanSpecificLun(lun, array_iqn)

	device, err := rescanUtils.GetMpathDevice(lun, array_iqn)
	klog.V(4).Infof("Discovered device : {%v}", device)
	if err != nil {
		klog.V(4).Infof("error while discovring the device : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	isNotMountPoint, err := d.mounter.IsLikelyNotMountPoint(device)
	if err != nil {
		klog.V(4).Infof("error while trying to check mountpoint: {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(4).Infof("Return isMountPoint: {%v}", isNotMountPoint)

	mountList, err := d.mounter.List()
	klog.V(4).Infof("Return mountList: {%v}", mountList)

	if !isNotMountPoint {
		deviceMP := d.getMountPointFromList(device, mountList)
		if deviceMP != nil {
			isCorrectMountpoint := d.mounter.IsMountPointMatch(*deviceMP, req.GetStagingTargetPath())
			klog.V(4).Infof("Return isCorrectMountpoint: {%v}. for device : {%v}, staging target path : {%v}",
				isCorrectMountpoint, device, req.GetStagingTargetPath())
			if isCorrectMountpoint {
				klog.V(4).Infof("Returning ok result")
				return &csi.NodeStageVolumeResponse{}, nil
			} else {
				return nil, status.Errorf(codes.AlreadyExists, "Mount point is already mounted to.")
			}
		}
	}

	// if the device is not mounted then we are mounting it.

	volumeCap := req.GetVolumeCapability()
	fsType := volumeCap.GetMount().FsType

	if fsType == "" {
		fsType = defaultFSType
	}

	klog.V(4).Infof("fs_type : {%v}", fsType)

	err = d.mounter.FormatAndMount(device, req.GetStagingTargetPath(), fsType, nil)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// TODO : need to write the device info for unstage use, and iscsi connectivity

	stageInfoPath := path.Join(req.GetStagingTargetPath(), stageInfoFilename)
	stageInfo := make(map[string]string)
	baseDevice := path.Base(device)
	stageInfo["mpathDevice"] = baseDevice                                    //this should return the mathhh for example
	sysDevices, err := d.nodeUtils.GetSysDevicesFromMpath(baseDevice)
	if err != nil {
		klog.Errorf("error while trying to get sys devices : {%v}",err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	stageInfo["sysDevices"] = sysDevices // like sda,sdb,...
	stageInfo["connectivity"] = connectivityType

	d.nodeUtils.WriteStageInfoToFile(stageInfoPath, stageInfo)

	klog.V(4).Infof("mounter succeeded!: %v", d.mounter)

	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *nodeService) getMountPointFromList(devicePath string, mountList []mount.MountPoint) *mount.MountPoint {
	//klog.V(4).Infof("current device : %v ", devicePath)
	for _, mount := range mountList {
		klog.V(4).Infof("devicePath : {%v}, device : {%v}", devicePath, mount.Device)
		if mount.Device == devicePath {
			klog.V(4).Infof("found mounted device")
			return &mount
		}
	}
	return nil

}

func (d *nodeService) nodeStageVolumeRequestValidation(req *csi.NodeStageVolumeRequest) error {

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

	// TODO add check if its a mount volume and publish context

	return nil
}

func (d *nodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(5).Infof("NodeUnstageVolume: called with args %+v", *req)
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		klog.Errorf("Volume ID not provided")
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	target := req.GetStagingTargetPath()
	if len(target) == 0 {
		klog.Errorf("Staging target not provided")
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	dev, refs, err := mount.GetDeviceNameFromMount(d.mounter, target)
	if err != nil {
		klog.Errorf("error while trying to get device from mount : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(4).Infof("dev : {%v}, refs: {%v}", dev, refs)

	if refs == 0 {
		klog.V(5).Infof("Idempotent case : %s target not mounted", target)
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	// remove the mpath device //TODO: maybe this is before? the unmount??

	klog.V(4).Infof("Reading statge info file")
	stageInfoPath := path.Join(req.GetStagingTargetPath(), stageInfoFilename)
	infoMap, err := d.nodeUtils.ReadFromStagingInfoFile(stageInfoPath)
	if err != nil {
		klog.Errorf("error while trying to read from the staging info file : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	klog.V(4).Infof("found info map : {%v}", infoMap)

	connectivityType := infoMap["connectivity"]
	mpathDevice := infoMap["mpathDevice"]
	sysDevices := strings.Split(infoMap["sysDevices"], ",")

	klog.V(4).Infof("got info from stageInfo file . connectivity : {%v}. device : {%v}, sysDevices : {%v}", connectivityType, mpathDevice, sysDevices)

	klog.V(4).Infof("Calling unmount")
	err = d.mounter.Unmount(target)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not unmount target %q: %v", target, err)
	}

	rescanUtils, err := d.newRescanUtils(connectivityType, d.nodeUtils, d.executer)
	//rescanUtils.FlushMultipathDevice(mpathDevice)
	rescanUtils.RemoveIscsiDevice(sysDevices)
	
	d.nodeUtils.ClearStageInfoFile(stageInfoPath)

	klog.V(4).Infof("Sleeping for a second") //TODO: is this necessary?
	time.Sleep(time.Second)

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *nodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
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

	// checking if the node staging path was mpounted into
	stagingPath := req.GetStagingTargetPath()
	targetPath := req.GetTargetPath()
	klog.V(4).Infof("stagingPath : {%v}, targetPath : {%v}", stagingPath, targetPath)

	mountList, err := d.mounter.List()
	found := false
	for _, mount := range mountList {
		klog.V(4).Infof("mount device : {%v}, path : {%v}", mount.Device, mount.Path)
		if mount.Path == stagingPath {
			found = true
			break
		}
	}
	if !found {
		return nil, status.Error(codes.FailedPrecondition, "staging target path was not mounted into.")
	}

	//checking if there is a mount FROM staging target path
	for _, mount := range mountList {
		//klog.V(4).Infof("#mount device : {%v}, path : {%v}", mount.Device, mount.Path)
		if mount.Device == stagingPath {
			if mount.Path == targetPath {
				klog.V(4).Infof("Idempotent case : path was already mounted.")
				return &csi.NodePublishVolumeResponse{}, nil
			} else {
				klog.V(4).Infof("Failed pre condition, staging path already mounted somewhere else")
				return nil, status.Error(codes.FailedPrecondition, "staging target path is already mounted elsewhere")
			}
		}
	}

	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		klog.V(4).Infof("Target path directory does not exist. creating : {%v}", targetPath)
		d.mounter.MakeDir(targetPath)

	}

	fsType := req.GetVolumeCapability().GetMount().FsType
	if fsType == "" {
		fsType = defaultFSType

	}

	// bind mount!
	err = d.mounter.Mount(stagingPath, targetPath, fsType, []string{"bind"})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *nodeService) nodePublishVolumeRequestValidation(req *csi.NodePublishVolumeRequest) error {
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

func (d *nodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(5).Infof("NodeUnpublishVolume: called with args %+v", *req)
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	target := req.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	klog.V(5).Infof("NodeUnpublishVolume: unmounting %s", target)
	err := d.mounter.Unmount(target)
	if err != nil {
		if "not mounted" in err.Error(){
			klog.V(4).Infof("Idempotent case - target was already unmounted %s", target)
			return  &csi.NodeUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "Could not unmount %q: %v", target, err)
	}
	
	// TODO: do we need this??
//	klog.V(4).Infof("Delting target path  %s", target)
//	
//	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
//		klog.V(4).Infof("Target path directory does not exist. creating : {%v}", targetPath)
//		os.RemoveAll(targetPath)
//	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
	
}

func (d *nodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeGetVolumeStats is not implemented yet")
}

func (d *nodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, fmt.Sprintf("NodeExpandVolume is not yet implemented"))
}

func (d *nodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
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

func (d *nodeService) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	klog.V(5).Infof("NodeGetInfo: called with args %+v", *req)

	iscsiIQN, err := d.nodeUtils.ParseIscsiInitiators("/etc/iscsi/initiatorname.iscsi")
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	delimiter := ";"

	nodeId := d.hostname + delimiter + iscsiIQN
	klog.V(4).Infof("node id is : %s", nodeId)

	return &csi.NodeGetInfoResponse{
		NodeId: nodeId,
	}, nil

}

//func (d *nodeService) nodePublishVolumeForFileSystem(req *csi.NodePublishVolumeRequest) error {
//	/*
//		target := req.GetTargetPath()
//		source := req.GetStagingTargetPath()
//
//		klog.V(5).Infof("NodePublishVolume: creating dir %s", target)
//		if err := d.mounter.MakeDir(target); err != nil {
//			return status.Errorf(codes.Internal, "Could not create dir %q: %v", target, err)
//		}
//
//		klog.V(5).Infof("NodePublishVolume: mounting %s at %s", source, target)
//		if err := d.mounter.Mount(source, target, ""); err != nil { // TODO add support for mountOptions
//			return status.Errorf(codes.Internal, "Could not mount %q at %q: %v", source, target, err)
//		}
//	*/
//	return nil
//}

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

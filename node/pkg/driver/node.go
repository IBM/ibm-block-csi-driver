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

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
	//"k8s.io/kubernetes/pkg/util/mount" // TODO since there is error "loading module requirements" I comment it out for now.
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
)

// nodeService represents the node service of CSI driver
type nodeService struct {
	//mounter  *mount.SafeFormatAndMount  // TODO fix k8s mount import
	configYaml ConfigFile
}

// newNodeService creates a new node service
// it panics if failed to create the service
func newNodeService(configYaml ConfigFile) nodeService {
	return nodeService{
		configYaml: configYaml,
		//		mounter:  newSafeMounter(),
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

	return nil, status.Errorf(codes.Unimplemented, "NodeStageVolume - Not implemented yet") // TODO
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

	if !isValidVolumeCapabilities([]*csi.VolumeCapability{volCap}) {
		return &RequestValidationError{"Volume capability not supported"}
	}

	// TODO add check if its a mount volume and publish context

	return nil
}

func (d *nodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(5).Infof("NodeUnstageVolume: called with args %+v", *req)
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	target := req.GetStagingTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	/*
		TODO: fix issue with k8s mount in the import section and then uncomment this one.
		// Check if target directory is a mount point. GetDeviceNameFromMount
		// given a mnt point, finds the device from /proc/mounts
		// returns the device name, reference count, and error code
		dev, refCount, err := mount.GetDeviceNameFromMount(d.mounter, target)
		// TODO but its not enough for idempotancy, need to add additional find of the device even if the mountpoint not exist.
		if err != nil {
			msg := fmt.Sprintf("failed to check if volume is mounted: %v", err)
			return nil, status.Error(codes.Internal, msg)
		}


		// From the spec: If the volume corresponding to the volume_id
		// is not staged to the staging_target_path, the Plugin MUST
		// reply 0 OK.
		if refCount == 0 {
			klog.V(5).Infof("NodeUnstageVolume: %s target not mounted", target)
			return &csi.NodeUnstageVolumeResponse{}, nil
		}

		if refCount > 1 {
			klog.Warningf("NodeUnstageVolume: found %d references to device %s mounted at target path %s", refCount, dev, target)
		}

		klog.V(5).Infof("NodeUnstageVolume: unmounting %s", target)
		err = d.mounter.Unmount(target)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not unmount target %q: %v", target, err)
		}
		return &csi.NodeUnstageVolumeResponse{}, nil
	*/

	return nil, status.Errorf(codes.Unimplemented, "NodeUnstageVolume - Not implemented yet") // TODO
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

	if err := d.nodePublishVolumeForFileSystem(req); err != nil {
		return nil, err
	}

	return nil, status.Errorf(codes.Unimplemented, "nodePublishVolumeForFileSystem - Not implemented yet") // TODO
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

	if !isValidVolumeCapabilities([]*csi.VolumeCapability{volCap}) {
		return &RequestValidationError{"Volume capability not supported"}
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

	// TODO fix k8s mount import and then uncomment this section
	/*
		klog.V(5).Infof("NodeUnpublishVolume: unmounting %s", target)
		err := d.mounter.Unmount(target)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not unmount %q: %v", target, err)
		}

		return &csi.NodeUnpublishVolumeResponse{}, nil
	*/

	return nil, status.Errorf(codes.Unimplemented, "NodeUnpublishVolume - Not implemented yet")
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

	return nil, status.Error(codes.Unimplemented, "NodeGetInfo is not implemented yet") // TODO

	/* TODO

	return &csi.NodeGetInfoResponse{
		NodeId:             "TODO", // TODO need to implement this function.
	}, nil
	*/
}

func (d *nodeService) nodePublishVolumeForFileSystem(req *csi.NodePublishVolumeRequest) error {
	/*
		target := req.GetTargetPath()
		source := req.GetStagingTargetPath()

		klog.V(5).Infof("NodePublishVolume: creating dir %s", target)
		if err := d.mounter.MakeDir(target); err != nil {
			return status.Errorf(codes.Internal, "Could not create dir %q: %v", target, err)
		}

		klog.V(5).Infof("NodePublishVolume: mounting %s at %s", source, target)
		if err := d.mounter.Mount(source, target, ""); err != nil { // TODO add support for mountOptions
			return status.Errorf(codes.Internal, "Could not mount %q at %q: %v", source, target, err)
		}
	*/
	return nil
}

func isValidVolumeCapabilities(volCaps []*csi.VolumeCapability) bool {
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

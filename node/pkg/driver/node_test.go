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

package driver_test

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"

	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	PublishContextParamLun          string = "PUBLISH_CONTEXT_LUN" // TODO for some reason I coun't take it from config.yaml
	PublishContextParamConnectivity string = "PUBLISH_CONTEXT_CONNECTIVITY"
	PublishContextParamArrayIqn     string = "PUBLISH_CONTEXT_ARRAY_IQN"
)

var ConfigYaml = driver.ConfigFile{
	Controller: driver.Controller{Publish_context_separator: ","},
	Parameters: driver.Parameters{
		Object_id_info: driver.Object_id_info{Delimiter: ":", Ids_delimiter: ";"},
		Node_id_info:   driver.Node_id_info{Delimiter: ";", Fcs_delimiter: ":"},
	},
	Connectivity_type: driver.Connectivity_type{Nvme_over_fc: "nvmeofc", Fc: "fc", Iscsi: "iscsi"},
}

func newTestNodeService(nodeUtils driver.NodeUtilsInterface,
	osDevConHelper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface,
	nodeMounter driver.NodeMounter) driver.NodeService {
	return driver.NodeService{
		Hostname:                   "test-host",
		ConfigYaml:                 ConfigYaml,
		VolumeIdLocksMap:           driver.NewSyncLock(1000),
		NodeUtils:                  nodeUtils,
		Mounter:                    nodeMounter,
		OsDeviceConnectivityHelper: osDevConHelper,
	}
}

func newTestNodeServiceStaging(nodeUtils driver.NodeUtilsInterface,
	osDevCon device_connectivity.OsDeviceConnectivityInterface,
	osDevConHelper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface,
	nodeMounter driver.NodeMounter) driver.NodeService {
	osDeviceConnectivityMapping := map[string]device_connectivity.OsDeviceConnectivityInterface{
		ConfigYaml.Connectivity_type.Nvme_over_fc: osDevCon,
		ConfigYaml.Connectivity_type.Fc:           osDevCon,
		ConfigYaml.Connectivity_type.Iscsi:        osDevCon,
	}

	return driver.NodeService{
		Mounter:                     nodeMounter,
		Hostname:                    "test-host",
		ConfigYaml:                  ConfigYaml,
		VolumeIdLocksMap:            driver.NewSyncLock(1000),
		NodeUtils:                   nodeUtils,
		OsDeviceConnectivityMapping: osDeviceConnectivityMapping,
		OsDeviceConnectivityHelper:  osDevConHelper,
	}
}

func TestNodeStageVolume(t *testing.T) {
	dummyError := errors.New("Dummy error")
	conType := ConfigYaml.Connectivity_type.Iscsi
	volId := "vol-test"
	lun := 10
	dmSysFsName := "dm-2"
	mpathDevice := "/dev/" + dmSysFsName
	sysDevices := []string{"/dev/sda", "/dev/sdb"}
	fsType := "ext4"
	ipsByArrayInitiator := map[string][]string{"iqn.1994-05.com.redhat:686358c930fe": {"1.2.3.4", "[::1]"}}
	arrayInitiators := []string{"iqn.1994-05.com.redhat:686358c930fe"}
	stagingPath := "/test/path"
	stagingPathWithHostPrefix := GetPodPath(stagingPath)
	var mountOptions []string

	stdVolCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{FsType: fsType},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	publishContext := map[string]string{
		PublishContextParamLun:                "1",
		PublishContextParamConnectivity:       ConfigYaml.Connectivity_type.Iscsi,
		PublishContextParamArrayIqn:           "iqn.1994-05.com.redhat:686358c930fe",
		"iqn.1994-05.com.redhat:686358c930fe": "1.2.3.4,[::1]",
	}
	stagingRequest := &csi.NodeStageVolumeRequest{
		PublishContext:    publishContext,
		StagingTargetPath: stagingPath,
		VolumeCapability:  stdVolCap,
		VolumeId:          volId,
	}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				req := &csi.NodeStageVolumeRequest{
					PublishContext:    publishContext,
					StagingTargetPath: stagingPath,
					VolumeCapability:  stdVolCap,
				}
				node := newTestNodeService(nil, nil, nil)
				_, err := node.NodeStageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no StagingTargetPath",
			testFunc: func(t *testing.T) {
				req := &csi.NodeStageVolumeRequest{
					PublishContext:   publishContext,
					VolumeCapability: stdVolCap,
					VolumeId:         volId,
				}
				node := newTestNodeService(nil, nil, nil)
				_, err := node.NodeStageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no VolumeCapability",
			testFunc: func(t *testing.T) {
				req := &csi.NodeStageVolumeRequest{
					PublishContext:    publishContext,
					StagingTargetPath: stagingPath,
					VolumeId:          volId,
				}
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, nil)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)

				_, err := node.NodeStageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail invalid VolumeCapability",
			testFunc: func(t *testing.T) {
				req := &csi.NodeStageVolumeRequest{
					PublishContext:    publishContext,
					StagingTargetPath: stagingPath,
					VolumeCapability: &csi.VolumeCapability{
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
						},
					},
					VolumeId: volId,
				}
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, nil)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)

				_, err := node.NodeStageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail invalid arrayInitiators",
			testFunc: func(t *testing.T) {
				req := &csi.NodeStageVolumeRequest{
					PublishContext: map[string]string{
						PublishContextParamLun:          "1",
						PublishContextParamConnectivity: ConfigYaml.Connectivity_type.Iscsi,
						PublishContextParamArrayIqn:     "iqn.1994-05.com.redhat:686358c930fe",
					},
					StagingTargetPath: stagingPath,
					VolumeCapability: &csi.VolumeCapability{
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
					VolumeId: volId,
				}
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, nil)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)

				_, err := node.NodeStageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail parse PublishContext",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext).Return("", 0, nil, dummyError)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail rescan devices",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(dummyError)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail get mpath device",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId).Return("", dummyError)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail get disk format",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockOsDeviceCon.EXPECT().ValidateLun(lun, sysDevices).Return(nil)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return("", dummyError)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success new filesystem",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockOsDeviceCon.EXPECT().ValidateLun(lun, sysDevices).Return(nil)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return("", nil)
				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().FormatDevice(mpathDevice, fsType)
				mockMounter.EXPECT().FormatAndMount(mpathDevice, stagingPath, fsType, mountOptions)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success device already formatted",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockOsDeviceCon.EXPECT().ValidateLun(lun, sysDevices).Return(nil)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(true, nil)
				mockMounter.EXPECT().FormatAndMount(mpathDevice, stagingPath, fsType, mountOptions)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success idempotent",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockOsDeviceCon.EXPECT().ValidateLun(lun, sysDevices).Return(nil)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().IsDirectory(stagingPathWithHostPrefix).Return(true)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "fail existing fsType different from requested",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(stagingPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockOsDeviceCon.EXPECT().ValidateLun(lun, sysDevices).Return(nil)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return("different-fsType", nil)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.AlreadyExists)
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNodeUnstageVolume(t *testing.T) {
	volId := "vol-test"
	dummyError := errors.New("Dummy error")
	dmNotFoundError := &device_connectivity.MultipathDeviceNotFoundForVolumeError{VolumeId: volId}
	dmSysFsName := "dm-2"
	sysDevices := []string{"/dev/d1", "/dev/d2"}
	stagingPath := "/test/path"
	stageInfoPath := path.Join(stagingPath, driver.StageInfoFilename)
	stagingPathWithHostPrefix := GetPodPath(stagingPath)

	unstageRequest := &csi.NodeUnstageVolumeRequest{
		VolumeId:          volId,
		StagingTargetPath: stagingPath,
	}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)
				req := &csi.NodeUnstageVolumeRequest{
					StagingTargetPath: stagingPath,
				}
				_, err := node.NodeUnstageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no StagingTargetPath",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)
				req := &csi.NodeUnstageVolumeRequest{
					VolumeId: volId,
				}
				_, err := node.NodeUnstageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail discovering multipath device",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, nil)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return("", dummyError)

				_, err := node.NodeUnstageVolume(context.TODO(), unstageRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail flush multipath",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(dmSysFsName, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockOsDeviceConHelper.EXPECT().FlushMultipathDevice(dmSysFsName).Return(dummyError)

				_, err := node.NodeUnstageVolume(context.TODO(), unstageRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success idempotent",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return("", dmNotFoundError)

				_, err := node.NodeUnstageVolume(context.TODO(), unstageRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success normal",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(false, nil)
				mockMounter.EXPECT().Unmount(stagingPath).Return(nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(dmSysFsName, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockOsDeviceConHelper.EXPECT().FlushMultipathDevice(dmSysFsName).Return(nil)
				mockOsDeviceConHelper.EXPECT().RemovePhysicalDevice(sysDevices).Return(nil)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(true)
				mockNodeUtils.EXPECT().ClearStageInfoFile(stageInfoPath).Return(nil)

				_, err := node.NodeUnstageVolume(context.TODO(), unstageRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNodePublishVolume(t *testing.T) {
	volumeId := "vol-test"
	fsTypeXfs := "ext4"
	targetPath := "/test/path"
	stagingPath := "/test/staging"
	targetPathWithHostPrefix := GetPodPath(targetPath)
	stagingPathWithHostPrefix := GetPodPath(stagingPath)
	deviceName := "fakedev"
	mpathDevice := filepath.Join(device_connectivity.DevPath, deviceName)
	accessMode := &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	}
	fsVolCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{FsType: fsTypeXfs},
		},
		AccessMode: accessMode,
	}
	rawBlockVolumeCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{
			Block: &csi.VolumeCapability_BlockVolume{},
		},
		AccessMode: accessMode,
	}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					TargetPath:        targetPath,
					VolumeCapability:  fsVolCap,
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no StagingTargetPath",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:   map[string]string{},
					TargetPath:       targetPath,
					VolumeCapability: fsVolCap,
					VolumeId:         volumeId,
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.FailedPrecondition)
			},
		},
		{
			name: "fail no TargetPath",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          volumeId,
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no VolumeCapability",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					TargetPath:        targetPath,
					VolumeId:          volumeId,
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail invalid VolumeCapability",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					TargetPath:        targetPath,
					VolumeCapability: &csi.VolumeCapability{
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
						},
					},
					VolumeId: volumeId,
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail AlreadyExists",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().IsDirectory(targetPathWithHostPrefix).Return(false)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					TargetPath:        targetPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          volumeId,
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.AlreadyExists)
			},
		},
		{
			name: "success with filesystem volume",
			testFunc: func(t *testing.T) {
				mountOptions := []string{"bind"}
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(false)
				mockNodeUtils.EXPECT().MakeDir(targetPathWithHostPrefix).Return(nil)
				mockMounter.EXPECT().Mount(stagingPath, targetPath, fsTypeXfs, mountOptions)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					TargetPath:        targetPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          volumeId,
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success idempotent with filesystem volume",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(stagingPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().IsDirectory(targetPathWithHostPrefix).Return(true)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					TargetPath:        targetPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          volumeId,
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success with raw block volume",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(false)
				mockNodeUtils.EXPECT().MakeFile(gomock.Eq(targetPathWithHostPrefix)).Return(nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volumeId).Return(volumeId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volumeId).Return(mpathDevice, nil)
				mockMounter.EXPECT().Mount(mpathDevice, targetPath, "", []string{"bind"})

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					TargetPath:        targetPath,
					VolumeCapability:  rawBlockVolumeCap,
					VolumeId:          "vol-test",
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success with raw block volume with mount file exits",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volumeId).Return(volumeId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volumeId).Return(mpathDevice, nil)
				mockMounter.EXPECT().Mount(mpathDevice, targetPath, "", []string{"bind"})

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingPath,
					TargetPath:        targetPath,
					VolumeCapability:  rawBlockVolumeCap,
					VolumeId:          "vol-test",
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNodeUnpublishVolume(t *testing.T) {
	targetPath := "/test/path"
	targetPathWithHostPrefix := GetPodPath(targetPath)

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: targetPath,
				}
				_, err := node.NodeUnpublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no TargetPath",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil, nil)

				req := &csi.NodeUnpublishVolumeRequest{
					VolumeId: "vol-test",
				}
				_, err := node.NodeUnpublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "success normal",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, mockMounter)

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: targetPath,
					VolumeId:   "vol-test",
				}
				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPathWithHostPrefix).Return(false, nil)
				mockMounter.EXPECT().Unmount(targetPath).Return(nil)
				mockNodeUtils.EXPECT().RemoveFileOrDirectory(targetPathWithHostPrefix)
				_, err := node.NodeUnpublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success idempotent",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, nil, mockMounter)

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: targetPath,
					VolumeId:   "vol-test",
				}
				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(false)
				_, err := node.NodeUnpublishVolume(context.TODO(), req)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNodeGetVolumeStats(t *testing.T) {
	volumeId := "someStorageType:vol-test"
	volumeUuid := "vol-test"
	volumePath := "/test/path"
	stagingTargetPath := "/staging/test/path"
	volumePathWithHostPrefix := GetPodPath(volumePath)
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
	mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
	d := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, nil)
	req := &csi.NodeGetVolumeStatsRequest{
		VolumeId:          volumeId,
		VolumePath:        volumePath,
		StagingTargetPath: stagingTargetPath,
	}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "fail volumePath does not exists",
			testFunc: func(t *testing.T) {
				expErrCode := codes.NotFound
				mockNodeUtils.EXPECT().GetPodPath(volumePath).Return(volumePathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(volumePathWithHostPrefix).Return(false)

				_, err := d.NodeGetVolumeStats(context.TODO(), req)
				assertError(t, err, expErrCode)
			},
		},
		{
			name: "fail to get stats for block because of missing mpath device",
			testFunc: func(t *testing.T) {
				expErrCode := codes.NotFound
				mockNodeUtils.EXPECT().GetPodPath(volumePath).Return(volumePathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(volumePathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsBlock(volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetBlockVolumeStats(volumeId).Return(driver.VolumeStatistics{},
					&device_connectivity.MultipathDeviceNotFoundForVolumeError{VolumeId: ""})

				_, err := d.NodeGetVolumeStats(context.TODO(), req)
				assertError(t, err, expErrCode)
			},
		},
		{
			name: "fail to get stats for block because of general error",
			testFunc: func(t *testing.T) {
				expErrCode := codes.Internal
				mockNodeUtils.EXPECT().GetPodPath(volumePath).Return(volumePathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(volumePathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsBlock(volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetBlockVolumeStats(volumeId).Return(driver.VolumeStatistics{},
					errors.New("fail to get stats"))

				_, err := d.NodeGetVolumeStats(context.TODO(), req)
				assertError(t, err, expErrCode)
			},
		},
		{
			name: "fail to get stats",
			testFunc: func(t *testing.T) {
				expErrCode := codes.Internal
				expSubString := "Failed to get statistics"
				mockNodeUtils.EXPECT().GetPodPath(volumePath).Return(volumePathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(volumePathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsBlock(volumePathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volumeId).Return(volumeUuid)
				mockOsDeviceConHelper.EXPECT().IsVolumePathMatchesVolumeId(volumeUuid, volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetFileSystemVolumeStats(volumePathWithHostPrefix).Return(driver.VolumeStatistics{}, errors.New("fail to get stats"))

				_, err := d.NodeGetVolumeStats(context.TODO(), req)
				assertError(t, err, expErrCode)
				if err != nil && !strings.Contains(err.Error(), expSubString) {
					t.Fatalf("Expected substring: %s in error message from NodeGetVolumeStats, got error message: %s", expSubString, err.Error())
				}
			},
		},
		{
			name: "success get stats on file system volume",
			testFunc: func(t *testing.T) {
				volumeStats := driver.VolumeStatistics{
					AvailableBytes: 1,
					TotalBytes:     1,
					UsedBytes:      1,

					AvailableInodes: 1,
					TotalInodes:     1,
					UsedInodes:      1,
				}
				expResp := &csi.NodeGetVolumeStatsResponse{
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
				}
				mockNodeUtils.EXPECT().GetPodPath(volumePath).Return(volumePathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(volumePathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsBlock(volumePathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volumeId).Return(volumeUuid)
				mockOsDeviceConHelper.EXPECT().IsVolumePathMatchesVolumeId(volumeUuid, volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetFileSystemVolumeStats(volumePathWithHostPrefix).Return(volumeStats, nil)

				assertExpectedStats(t, expResp, req, d)
			},
		},
		{
			name: "success get stats on block device volume",
			testFunc: func(t *testing.T) {
				volumeStats := driver.VolumeStatistics{
					TotalBytes: 1,
				}
				expResp := &csi.NodeGetVolumeStatsResponse{
					Usage: []*csi.VolumeUsage{
						{
							Unit:  csi.VolumeUsage_BYTES,
							Total: volumeStats.TotalBytes,
						},
						{
							Unit: csi.VolumeUsage_INODES,
						},
					},
				}
				mockNodeUtils.EXPECT().GetPodPath(volumePath).Return(volumePathWithHostPrefix)
				mockNodeUtils.EXPECT().IsPathExists(volumePathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsBlock(volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetBlockVolumeStats(volumeId).Return(volumeStats, nil)

				assertExpectedStats(t, expResp, req, d)
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func assertExpectedStats(t *testing.T, expResp *csi.NodeGetVolumeStatsResponse, req *csi.NodeGetVolumeStatsRequest, node driver.NodeService) {
	resp, err := node.NodeGetVolumeStats(context.TODO(), req)
	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}
	if !reflect.DeepEqual(expResp, resp) {
		t.Fatalf("Expected response {%+v}, got {%+v}", expResp, resp)
	}
}

func newTestNodeServiceExpand(nodeUtils driver.NodeUtilsInterface, osDevConHelper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface, nodeMounter driver.NodeMounter) driver.NodeService {
	return driver.NodeService{
		Hostname:                    "test-host",
		ConfigYaml:                  driver.ConfigFile{},
		VolumeIdLocksMap:            driver.NewSyncLock(1000),
		NodeUtils:                   nodeUtils,
		OsDeviceConnectivityMapping: map[string]device_connectivity.OsDeviceConnectivityInterface{},
		OsDeviceConnectivityHelper:  osDevConHelper,
		Mounter:                     nodeMounter,
	}
}

func TestNodeExpandVolume(t *testing.T) {
	d := newTestNodeService(nil, nil, nil)
	volId := "someStorageType:vol-test"
	volumePath := "/test/path"
	stagingTargetPath := "/staging/test/path"
	expandRequest := &csi.NodeExpandVolumeRequest{
		VolumeId:          volId,
		VolumePath:        volumePath,
		StagingTargetPath: stagingTargetPath,
	}
	dmSysFsName := "dm-2"
	sysDevices := []string{"/dev/d1", "/dev/d2"}
	mpathDevice := "/dev/" + dmSysFsName
	fsType := "ext4"
	dummyError := errors.New("Dummy error")

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				node := d
				expandRequest := &csi.NodeExpandVolumeRequest{
					VolumePath:        volumePath,
					StagingTargetPath: stagingTargetPath,
				}

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no VolumePath",
			testFunc: func(t *testing.T) {
				node := d
				expandRequest := &csi.NodeExpandVolumeRequest{
					VolumeId:          volId,
					StagingTargetPath: stagingTargetPath,
				}

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "get multipath device fail",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return("", dummyError)

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "get sys devices fail",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(nil, dummyError)

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "rescan fail",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().DevicesAreNvme(sysDevices).Return(false, nil)
				mockNodeUtils.EXPECT().RescanPhysicalDevices(sysDevices).Return(dummyError)

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "expand multipath device fail",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().DevicesAreNvme(sysDevices).Return(false, nil)
				mockNodeUtils.EXPECT().RescanPhysicalDevices(sysDevices)
				mockNodeUtils.EXPECT().ExpandMpathDevice(dmSysFsName).Return(dummyError)

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "get disk format fail",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().DevicesAreNvme(sysDevices).Return(false, nil)
				mockNodeUtils.EXPECT().RescanPhysicalDevices(sysDevices)
				mockNodeUtils.EXPECT().ExpandMpathDevice(dmSysFsName)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return("", dummyError)

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "expand filesystem fail",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().DevicesAreNvme(sysDevices).Return(false, nil)
				mockNodeUtils.EXPECT().RescanPhysicalDevices(sysDevices)
				mockNodeUtils.EXPECT().ExpandMpathDevice(dmSysFsName)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				mockNodeUtils.EXPECT().ExpandFilesystem(mpathDevice, stagingTargetPath, fsType).Return(dummyError)

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success expand volume, nvme",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().DevicesAreNvme(sysDevices).Return(true, nil)
				mockNodeUtils.EXPECT().ExpandMpathDevice(dmSysFsName)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				mockNodeUtils.EXPECT().ExpandFilesystem(mpathDevice, stagingTargetPath, fsType)

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success expand volume",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				node := newTestNodeServiceExpand(mockNodeUtils, mockOsDeviceConHelper, mockMounter)

				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(volId).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(dmSysFsName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().DevicesAreNvme(sysDevices).Return(false, nil)
				mockNodeUtils.EXPECT().RescanPhysicalDevices(sysDevices)
				mockNodeUtils.EXPECT().ExpandMpathDevice(dmSysFsName)
				mockMounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				mockNodeUtils.EXPECT().ExpandFilesystem(mpathDevice, stagingTargetPath, fsType)

				_, err := node.NodeExpandVolume(context.TODO(), expandRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNodeGetCapabilities(t *testing.T) {
	req := &csi.NodeGetCapabilitiesRequest{}

	d := newTestNodeService(nil, nil, nil)

	caps := []*csi.NodeServiceCapability{
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
				},
			},
		},
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
				},
			},
		},
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
				},
			},
		},
	}
	expResp := &csi.NodeGetCapabilitiesResponse{Capabilities: caps}

	resp, err := d.NodeGetCapabilities(context.TODO(), req)
	if err != nil {
		srvErr, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Could not get error status code from error: %v", srvErr)
		}
		t.Fatalf("Expected nil error, got %d message %s", srvErr.Code(), srvErr.Message())
	}
	if !reflect.DeepEqual(expResp, resp) {
		t.Fatalf("Expected response {%+v}, got {%+v}", expResp, resp)
	}
}

func TestNodeGetInfo(t *testing.T) {
	topologySegments := map[string]string{"topology.block.csi.ibm.com/zone": "testZone"}

	testCases := []struct {
		name            string
		returnNqn       string
		returnNqnErr    error
		returnFcs       []string
		returnFcErr     error
		returnIqn       string
		returnIqnErr    error
		returnNodeIdErr error
		expErr          error
		expNodeId       string
		nvmeExists      bool
		fcExists        bool
		iscsiExists     bool
	}{
		{
			name:         "empty nqn with error from node_utils, valid fcs",
			returnNqnErr: fmt.Errorf("some error"),
			returnFcs:    []string{"10000000c9934d9f", "10000000c9934d9h"},
			expNodeId:    "test-host;;10000000c9934d9f:10000000c9934d9h",
			nvmeExists:   true,
			fcExists:     true,
			iscsiExists:  true,
		},
		{
			name:        "empty fc with error from node_utils",
			returnFcErr: fmt.Errorf("some error"),
			expErr:      status.Error(codes.Internal, fmt.Errorf("some error").Error()),
			nvmeExists:  false,
			fcExists:    true,
			iscsiExists: true,
		},
		{
			name:         "empty iqn with error from node_utils, valid fcs",
			returnIqnErr: fmt.Errorf("some error"),
			returnFcs:    []string{"10000000c9934d9f", "10000000c9934d9h"},
			expNodeId:    "test-host;;10000000c9934d9f:10000000c9934d9h",
			nvmeExists:   false,
			fcExists:     true,
			iscsiExists:  true,
		},
		{
			name:        "valid iqn and fcs",
			returnIqn:   "iqn.1994-07.com.redhat:e123456789",
			returnFcs:   []string{"10000000c9934d9f", "10000000c9934d9h"},
			expNodeId:   "test-host;;10000000c9934d9f:10000000c9934d9h;iqn.1994-07.com.redhat:e123456789",
			nvmeExists:  false,
			fcExists:    true,
			iscsiExists: true,
		},
		{
			name:        "valid nqn, fcs and iqn",
			returnNqn:   "nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1",
			returnFcs:   []string{"10000000c9934d9f", "10000000c9934d9h"},
			returnIqn:   "iqn.1994-07.com.redhat:e123456789",
			expNodeId:   "test-host;nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1;10000000c9934d9f:10000000c9934d9h;iqn.1994-07.com.redhat:e123456789",
			nvmeExists:  true,
			fcExists:    true,
			iscsiExists: true,
		},
		{
			name:        "nqn, fc and iqn path are inexistent",
			nvmeExists:  false,
			fcExists:    false,
			iscsiExists: false,
			expErr:      status.Error(codes.Internal, fmt.Errorf("Cannot find valid nvme nqn, fc wwns or iscsi iqn").Error()),
		},
		{
			name:        "nqn and iqn path is inexistsent",
			nvmeExists:  false,
			fcExists:    true,
			iscsiExists: false,
			returnFcs:   []string{"10000000c9934d9f"},
			expNodeId:   "test-host;;10000000c9934d9f",
		},
		{
			name:        "fc path is inexistent",
			nvmeExists:  true,
			fcExists:    false,
			iscsiExists: true,
			returnNqn:   "nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1",
			returnIqn:   "iqn.1994-07.com.redhat:e123456789",
			expNodeId:   "test-host;nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1;;iqn.1994-07.com.redhat:e123456789",
		}, {
			name:            "generate NodeID returns error",
			returnIqn:       "iqn.1994-07.com.redhat:e123456789",
			returnFcs:       []string{"10000000c9934d9f", "10000000c9934d9h"},
			returnNodeIdErr: fmt.Errorf("some error"),
			expErr:          status.Error(codes.Internal, fmt.Errorf("some error").Error()),
			nvmeExists:      false,
			fcExists:        true,
			iscsiExists:     true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &csi.NodeGetInfoRequest{}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeNodeutils := mocks.NewMockNodeUtilsInterface(mockCtrl)
			d := newTestNodeService(fakeNodeutils, nil, nil)
			fakeNodeutils.EXPECT().GetTopologyLabels(context.TODO(), d.Hostname).Return(topologySegments, nil)
			fakeNodeutils.EXPECT().IsPathExists(driver.NvmeFullPath).Return(tc.nvmeExists)
			fakeNodeutils.EXPECT().IsFCExists().Return(tc.fcExists)
			if tc.nvmeExists {
				fakeNodeutils.EXPECT().ReadNvmeNqn().Return(tc.returnNqn, tc.returnNqnErr)
			}
			if tc.fcExists {
				fakeNodeutils.EXPECT().ParseFCPorts().Return(tc.returnFcs, tc.returnFcErr)
			}
			if tc.returnFcErr == nil {
				fakeNodeutils.EXPECT().IsPathExists(driver.IscsiFullPath).Return(tc.iscsiExists)
				if tc.iscsiExists {
					fakeNodeutils.EXPECT().ParseIscsiInitiators().Return(tc.returnIqn, tc.returnIqnErr)
				}
			}

			if tc.returnNqn != "" || len(tc.returnFcs) > 0 || tc.returnIqn != "" {
				fakeNodeutils.EXPECT().GenerateNodeID("test-host", tc.returnNqn, tc.returnFcs, tc.returnIqn).Return(tc.expNodeId, tc.returnNodeIdErr)
			}

			expTopology := &csi.Topology{Segments: topologySegments}
			expResponse := &csi.NodeGetInfoResponse{NodeId: tc.expNodeId, AccessibleTopology: expTopology}

			res, err := d.NodeGetInfo(context.TODO(), req)
			if tc.expErr != nil {
				if err == nil {
					t.Fatalf("Expected error to be thrown : {%v}", tc.expErr)
				} else {
					if err.Error() != tc.expErr.Error() {
						t.Fatalf("Expected error : {%v} to be equal to expected error : {%v}", err, tc.expErr)
					}
				}
			} else {
				if !reflect.DeepEqual(res, expResponse) {
					t.Fatalf("Expected res : {%v}, and got {%v}", expResponse, res)
				}
			}
		})
	}
}

func assertError(t *testing.T, err error, expectedErrorCode codes.Code) {
	if err == nil {
		t.Fatalf("Expected error code %d, got success", expectedErrorCode)
	}
	grpcError, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Failed getting error code from error: %v", grpcError)
	}
	if grpcError.Code() != expectedErrorCode {
		t.Fatalf("Expected error code %d, got %d. Error: %s", expectedErrorCode, grpcError.Code(), grpcError.Message())
	}
}

// To some files/dirs pod cannot access using its real path. It has to use a different path which is <prefix>/<path>.
// E.g. in order to access /etc/test.txt pod has to use /host/etc/test.txt
func GetPodPath(filepath string) string {
	return path.Join(driver.PrefixChrootOfHostRoot, filepath)
}

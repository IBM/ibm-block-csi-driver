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
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	PublishContextParamLun          string = "PUBLISH_CONTEXT_LUN" // TODO for some reason I coun't take it from config.yaml
	PublishContextParamConnectivity string = "PUBLISH_CONTEXT_CONNECTIVITY"
	PublishContextParamArrayIqn     string = "PUBLISH_CONTEXT_ARRAY_IQN"
)

func newTestNodeService(nodeUtils driver.NodeUtilsInterface, nodeMounter driver.NodeMounter) driver.NodeService {
	return driver.NodeService{
		Hostname:         "test-host",
		ConfigYaml:       driver.ConfigFile{},
		VolumeIdLocksMap: driver.NewSyncLock(),
		NodeUtils:        nodeUtils,
		Mounter:          nodeMounter,
	}
}

func newTestNodeServiceStaging(nodeUtils driver.NodeUtilsInterface, osDevCon device_connectivity.OsDeviceConnectivityInterface) driver.NodeService {
	osDeviceConnectivityMapping := map[string]device_connectivity.OsDeviceConnectivityInterface{
		device_connectivity.ConnectionTypeISCSI: osDevCon,
		device_connectivity.ConnectionTypeFC:    osDevCon,
	}

	return driver.NodeService{
		Hostname:                    "test-host",
		ConfigYaml:                  driver.ConfigFile{},
		VolumeIdLocksMap:            driver.NewSyncLock(),
		NodeUtils:                   nodeUtils,
		OsDeviceConnectivityMapping: osDeviceConnectivityMapping,
	}
}

func TestNodeStageVolume(t *testing.T) {
	dummyError := errors.New("Dummy error")
	conType := device_connectivity.ConnectionTypeISCSI
	volId := "vol-test"
	lun := 10
	mpathDeviceName := "dm-2"
	sysDevices := "/dev/d1,/dev/d2"
	mpathDevice := "/dev/" + mpathDeviceName
	ipsByArrayInitiator := map[string][]string{"iqn.1994-05.com.redhat:686358c930fe": {"1.2.3.4", "[::1]"}}
	arrayInitiators := []string{"iqn.1994-05.com.redhat:686358c930fe"}
	stagingPath := "/test/path"
	stageInfoPath := path.Join(stagingPath, driver.StageInfoFilename)

	newStageInfo := make(map[string]string)
	newStageInfo["mpathDevice"] = mpathDeviceName
	newStageInfo["sysDevices"] = sysDevices
	newStageInfo["connectivity"] = conType

	currentStageInfo := make(map[string]string)
	currentStageInfo["mpathDevice"] = ""
	currentStageInfo["sysDevices"] = sysDevices
	currentStageInfo["connectivity"] = conType

	stdVolCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	publishContext := map[string]string{
		PublishContextParamLun:                "1",
		PublishContextParamConnectivity:       device_connectivity.ConnectionTypeISCSI,
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
				node := newTestNodeService(nil, nil)
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
				node := newTestNodeService(nil, nil)
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
				node := newTestNodeService(nil, nil)
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
				node := newTestNodeService(nil, nil)
				_, err := node.NodeStageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail invalid arrayInitiators ",
			testFunc: func(t *testing.T) {
				req := &csi.NodeStageVolumeRequest{
					PublishContext: map[string]string{
						PublishContextParamLun:          "1",
						PublishContextParamConnectivity: device_connectivity.ConnectionTypeISCSI,
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
				node := newTestNodeService(nil, nil)
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
				node := newTestNodeService(mockNodeUtils, mockMounter)

				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext, node.ConfigYaml).Return("", 0, nil, dummyError)

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
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext, node.ConfigYaml).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
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
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext, node.ConfigYaml).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId, lun, arrayInitiators).Return("", dummyError)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail get devices of multipath",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext, node.ConfigYaml).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId, lun, arrayInitiators).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(mpathDeviceName).Return("", dummyError)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success new staging file",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext, node.ConfigYaml).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId, lun, arrayInitiators).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(mpathDeviceName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(false)
				mockNodeUtils.EXPECT().WriteStageInfoToFile(stageInfoPath, newStageInfo).Return(nil)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success cannot read staging file",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext, node.ConfigYaml).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId, lun, arrayInitiators).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(mpathDeviceName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(true)
				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stageInfoPath).Return(nil, dummyError)
				mockNodeUtils.EXPECT().WriteStageInfoToFile(stageInfoPath, newStageInfo).Return(nil)

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
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext, node.ConfigYaml).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId, lun, arrayInitiators).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(mpathDeviceName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(true)
				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stageInfoPath).Return(newStageInfo, nil)

				_, err := node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "fail different staging file",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().GetInfoFromPublishContext(stagingRequest.PublishContext, node.ConfigYaml).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
				mockNodeUtils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
				mockOsDeviceCon.EXPECT().EnsureLogin(ipsByArrayInitiator)
				mockOsDeviceCon.EXPECT().RescanDevices(lun, arrayInitiators).Return(nil)
				mockOsDeviceCon.EXPECT().GetMpathDevice(volId, lun, arrayInitiators).Return(mpathDevice, nil)
				mockNodeUtils.EXPECT().GetSysDevicesFromMpath(mpathDeviceName).Return(sysDevices, nil)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(true)
				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stageInfoPath).Return(currentStageInfo, nil)

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
	dummyError := errors.New("Dummy error")
	_, fileNotExistErr := os.Stat("DUMMY_FILE")
	conType := device_connectivity.ConnectionTypeISCSI
	volId := "vol-test"
	mpathDeviceName := "dm-2"
	sysDevices := "/dev/d1,/dev/d2"
	sysDevicesList := strings.Split(sysDevices, ",")
	stagingPath := "/test/path"
	stageInfoPath := path.Join(stagingPath, driver.StageInfoFilename)

	stageInfo := make(map[string]string)
	stageInfo["mpathDevice"] = mpathDeviceName
	stageInfo["sysDevices"] = sysDevices
	stageInfo["connectivity"] = conType

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
				node := newTestNodeService(nil, nil)
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
				node := newTestNodeService(nil, nil)
				req := &csi.NodeUnstageVolumeRequest{
					VolumeId: volId,
				}
				_, err := node.NodeUnstageVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail cannot read staging file",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stageInfoPath).Return(nil, dummyError)

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
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stageInfoPath).Return(stageInfo, nil)
				mockOsDeviceCon.EXPECT().FlushMultipathDevice(mpathDeviceName).Return(dummyError)

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
				node := newTestNodeServiceStaging(mockNodeUtils, nil)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stageInfoPath).Return(nil, fileNotExistErr)

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
				mockOsDeviceCon := mocks.NewMockOsDeviceConnectivityInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, mockOsDeviceCon)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stageInfoPath).Return(stageInfo, nil)
				mockOsDeviceCon.EXPECT().FlushMultipathDevice(mpathDeviceName).Return(nil)
				mockOsDeviceCon.EXPECT().RemovePhysicalDevice(sysDevicesList).Return(nil)
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
	fsTypeXfs := "ext4"
	targetPath := "/test/path"
	targetPathWithHostPrefix := GetPodPath(targetPath)
	targetPathParentDirWithHostPrefix := filepath.Dir(targetPathWithHostPrefix)
	stagingTargetPath := path.Join("/test/staging", driver.StageInfoFilename)
	stagingTargetFile := path.Join(stagingTargetPath, ".stageInfo.json")
	deviceName := "fakedev"
	stagingInfo := map[string]string{"mpathDevice": deviceName}
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
				node := newTestNodeService(nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
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
				node := newTestNodeService(nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:   map[string]string{},
					TargetPath:       targetPath,
					VolumeCapability: fsVolCap,
					VolumeId:         "vol-test",
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no TargetPath",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          "vol-test",
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no VolumeCapability",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeId:          "vol-test",
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail invalid VolumeCapability",
			testFunc: func(t *testing.T) {
				node := newTestNodeService(nil, nil)
				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability: &csi.VolumeCapability{
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
						},
					},
					VolumeId: "vol-test",
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
				node := newTestNodeService(mockNodeUtils, mockMounter)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stagingTargetFile).Return(stagingInfo, nil)
				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().IsDirectory(targetPathWithHostPrefix).Return(false)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          "vol-test",
				}

				_, err := node.NodePublishVolume(context.TODO(), req)
				assertError(t, err, codes.AlreadyExists)
			},
		},
		{
			name: "success with filesystem volume",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, mockMounter)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stagingTargetFile).Return(stagingInfo, nil)
				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(false)
				mockMounter.EXPECT().MakeDir(targetPathWithHostPrefix).Return(nil)
				mockMounter.EXPECT().FormatAndMount(mpathDevice, targetPath, fsTypeXfs, nil)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          "vol-test",
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
				node := newTestNodeService(mockNodeUtils, mockMounter)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stagingTargetFile).Return(stagingInfo, nil)
				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().IsDirectory(targetPathWithHostPrefix).Return(true)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          "vol-test",
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
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, mockMounter)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stagingTargetFile).Return(stagingInfo, nil)
				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				gomock.InOrder(
					mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(false),
					mockNodeUtils.EXPECT().IsPathExists(targetPathParentDirWithHostPrefix).Return(false),
				)
				mockMounter.EXPECT().MakeDir(targetPathParentDirWithHostPrefix).Return(nil)
				mockMounter.EXPECT().MakeFile(gomock.Eq(targetPathWithHostPrefix)).Return(nil)
				mockMounter.EXPECT().Mount(mpathDevice, targetPath, "", []string{"bind"})

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
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
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				node := newTestNodeService(mockNodeUtils, mockMounter)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(stagingTargetFile).Return(stagingInfo, nil)
				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				gomock.InOrder(
					mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true),
					mockNodeUtils.EXPECT().IsPathExists(targetPathParentDirWithHostPrefix).Return(true),
				)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPathWithHostPrefix).Return(true, nil)
				mockMounter.EXPECT().Mount(mpathDevice, targetPath, "", []string{"bind"})

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
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
				node := newTestNodeService(nil, nil)

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
				node := newTestNodeService(nil, nil)

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
				node := newTestNodeService(mockNodeUtils, mockMounter)

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
				node := newTestNodeService(mockNodeUtils, mockMounter)

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

	req := &csi.NodeGetVolumeStatsRequest{}

	d := newTestNodeService(nil, nil)

	expErrCode := codes.Unimplemented

	_, err := d.NodeGetVolumeStats(context.TODO(), req)
	if err == nil {
		t.Fatalf("Expected error code %d, got nil", expErrCode)
	}
	srvErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Could not get error status code from error: %v", srvErr)
	}
	if srvErr.Code() != expErrCode {
		t.Fatalf("Expected error code %d, got %d message %s", expErrCode, srvErr.Code(), srvErr.Message())
	}
}

func TestNodeGetCapabilities(t *testing.T) {
	req := &csi.NodeGetCapabilitiesRequest{}

	d := newTestNodeService(nil, nil)

	caps := []*csi.NodeServiceCapability{
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
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
	testCases := []struct {
		name           string
		return_iqn     string
		return_iqn_err error
		return_fcs     []string
		return_fc_err  error
		expErr         error
		expNodeId      string
		iscsiExists    bool
		fcExists       bool
	}{
		{
			name:          "good iqn, empty fc with error from node_utils",
			return_fc_err: fmt.Errorf("some error"),
			expErr:        status.Error(codes.Internal, fmt.Errorf("some error").Error()),
			iscsiExists:   true,
			fcExists:      true,
		},
		{
			name:        "empty iqn with error, one fc port",
			return_fcs:  []string{"10000000c9934d9f"},
			expNodeId:   "test-host;;10000000c9934d9f",
			iscsiExists: true,
			fcExists:    true,
		},
		{
			name:        "empty iqn with error from node_utils, one more fc ports",
			return_iqn:  "",
			return_fcs:  []string{"10000000c9934d9f", "10000000c9934d9h"},
			expNodeId:   "test-host;;10000000c9934d9f:10000000c9934d9h",
			iscsiExists: true,
			fcExists:    true,
		},
		{
			name:        "good iqn and good fcs",
			return_iqn:  "iqn.1994-07.com.redhat:e123456789",
			return_fcs:  []string{"10000000c9934d9f", "10000000c9934d9h"},
			expNodeId:   "test-host;iqn.1994-07.com.redhat:e123456789;10000000c9934d9f:10000000c9934d9h",
			iscsiExists: true,
			fcExists:    true,
		},
		{
			name:        "iqn and fc path are inexistent",
			iscsiExists: false,
			fcExists:    false,
			expErr:      status.Error(codes.Internal, fmt.Errorf("Cannot find valid fc wwns or iscsi iqn").Error()),
		},
		{
			name:        "iqn path is inexistsent",
			iscsiExists: false,
			fcExists:    true,
			return_fcs:  []string{"10000000c9934d9f"},
			expNodeId:   "test-host;;10000000c9934d9f",
		},
		{
			name:        "fc path is inexistent",
			iscsiExists: true,
			fcExists:    false,
			return_iqn:  "iqn.1994-07.com.redhat:e123456789",
			expNodeId:   "test-host;iqn.1994-07.com.redhat:e123456789;",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &csi.NodeGetInfoRequest{}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_nodeutils := mocks.NewMockNodeUtilsInterface(mockCtrl)
			fake_nodeutils.EXPECT().IsPathExists(driver.FCPath).Return(tc.fcExists)
			if tc.fcExists {
				fake_nodeutils.EXPECT().ParseFCPorts().Return(tc.return_fcs, tc.return_fc_err)
			}
			if tc.return_fc_err == nil {
				fake_nodeutils.EXPECT().IsPathExists(driver.IscsiFullPath).Return(tc.iscsiExists)
				if tc.iscsiExists {
					fake_nodeutils.EXPECT().ParseIscsiInitiators().Return(tc.return_iqn, tc.return_iqn_err)
				}
			}

			d := newTestNodeService(fake_nodeutils, nil)

			expResponse := &csi.NodeGetInfoResponse{NodeId: tc.expNodeId}

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
				if res.NodeId != expResponse.NodeId {
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

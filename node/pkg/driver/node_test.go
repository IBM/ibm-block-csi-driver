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
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"

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
	},
	Connectivity_type: driver.Connectivity_type{Nvme_over_fc: "nvmeofc", Fc: "fc", Iscsi: "iscsi"},
}

func newTestNodeService(nodeUtils driver.NodeUtilsInterface,
	osDevConHelper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface,
	nodeMounter driver.NodeMounter) driver.NodeService {
	return driver.NodeService{
		Hostname:                   "test-host",
		ConfigYaml:                 ConfigYaml,
		VolumeIdLocksMap:           driver.NewSyncLock(1000, true),
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
		VolumeIdLocksMap:            driver.NewSyncLock(1000, true),
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

	type stageEnv struct {
		utils    *mocks.MockNodeUtilsInterface
		osDevCon *mocks.MockOsDeviceConnectivityInterface
		helper   *mocks.MockOsDeviceConnectivityHelperScsiGenericInterface
		mounter  *mocks.MockNodeMounter
		node     driver.NodeService
		hostPath string
	}
	blockStagingRequest := &csi.NodeStageVolumeRequest{
		PublishContext:    publishContext,
		StagingTargetPath: stagingPath,
		VolumeId:          volId,
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
			AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
		},
	}
	// newEnv builds mocks. The staging "host path" is a real temp dir because NodeStageVolume calls os.MkdirAll on it.
	newEnv := func(t *testing.T) *stageEnv {
		ctl := gomock.NewController(t)
		env := &stageEnv{
			utils:    mocks.NewMockNodeUtilsInterface(ctl),
			osDevCon: mocks.NewMockOsDeviceConnectivityInterface(ctl),
			helper:   mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(ctl),
			mounter:  mocks.NewMockNodeMounter(ctl),
			hostPath: t.TempDir(),
		}
		env.node = newTestNodeServiceStaging(env.utils, env.osDevCon, env.helper, env.mounter)
		env.utils.EXPECT().GetPodPath(stagingPath).Return(env.hostPath).AnyTimes()
		env.utils.EXPECT().IsPathExists(env.hostPath).Return(true).AnyTimes()
		env.utils.EXPECT().GetInfoFromPublishContext(publishContext).Return(conType, lun, ipsByArrayInitiator, nil).AnyTimes()
		env.utils.EXPECT().IsBlock(gomock.Any(), env.hostPath).Return(false, nil).AnyTimes()
		return env
	}
	// expectPreScan: GetArrayInitiators/GetVolumeUuid/IdentityAwarePreScan with the given results.
	expectPreScan := func(env *stageEnv, dev string, isStaged, skipRescan, scanInProgress bool, err error) {
		env.utils.EXPECT().GetArrayInitiators(ipsByArrayInitiator).Return(arrayInitiators)
		env.utils.EXPECT().GetVolumeUuid(volId).Return(volId)
		env.helper.EXPECT().IdentityAwarePreScan(gomock.Any(), env.hostPath, volId).Return(dev, isStaged, skipRescan, scanInProgress, err)
	}
	// expectRescan: the discovery bracket (login, ghost cleanup, rescan, ghost cleanup).
	expectRescan := func(env *stageEnv) {
		env.osDevCon.EXPECT().EnsureLogin(gomock.Any(), ipsByArrayInitiator)
		env.helper.EXPECT().RemoveGhostDevice(gomock.Any(), volId, lun, arrayInitiators).Return(nil).Times(2)
		env.osDevCon.EXPECT().RescanDevices(gomock.Any(), lun, arrayInitiators).Return(nil)
	}
	// expectDiscovery: pre-scan finds nothing, rescan succeeds, mpath device found.
	expectDiscovery := func(env *stageEnv) {
		expectPreScan(env, "", false, false, false, nil)
		expectRescan(env)
		env.helper.EXPECT().GetMpathDevice(gomock.Any(), volId).Return(mpathDevice, nil)
	}
	// expectValidated: topology lookup (faked sysfs) and LUN validation succeed.
	expectValidated := func(t *testing.T, env *stageEnv) {
		stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, nil)
		stubGetSysDevicesFromMpath(t, dmSysFsName, sysDevices, nil)
		env.osDevCon.EXPECT().ValidateLun(gomock.Any(), mpathDevice, lun, sysDevices, volId).Return(nil)
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
			name: "fail pre-scan",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectPreScan(env, "", false, false, false, status.Error(codes.Aborted, "scan in progress"))

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Aborted)
			},
		},
		{
			name: "success already staged",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectPreScan(env, mpathDevice, true, false, false, nil)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "fail rescan devices",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectPreScan(env, "", false, false, false, nil)
				env.osDevCon.EXPECT().EnsureLogin(gomock.Any(), ipsByArrayInitiator)
				env.helper.EXPECT().RemoveGhostDevice(gomock.Any(), volId, lun, arrayInitiators).Return(nil)
				env.osDevCon.EXPECT().RescanDevices(gomock.Any(), lun, arrayInitiators).Return(dummyError)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success block volume, rescan skipped because device is healthy",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectPreScan(env, mpathDevice, false, true, false, nil)
				env.helper.EXPECT().GetMpathDevice(gomock.Any(), volId).Return(mpathDevice, nil)

				_, err := env.node.NodeStageVolume(context.TODO(), blockStagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success block volume, rescan skipped because scan is in progress",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectPreScan(env, "", false, false, true, nil)
				env.helper.EXPECT().GetMpathDevice(gomock.Any(), volId).Return(mpathDevice, nil)

				_, err := env.node.NodeStageVolume(context.TODO(), blockStagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success block volume",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)

				_, err := env.node.NodeStageVolume(context.TODO(), blockStagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "fail get mpath device",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectPreScan(env, "", false, false, false, nil)
				expectRescan(env)
				env.helper.EXPECT().GetMpathDevice(gomock.Any(), volId).Return("", dummyError)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail get mpath device with deadline exceeded",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectPreScan(env, "", false, false, false, nil)
				expectRescan(env)
				env.helper.EXPECT().GetMpathDevice(gomock.Any(), volId).Return("", status.Error(codes.DeadlineExceeded, "timeout"))

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.DeadlineExceeded)
			},
		},
		{
			name: "fail devices are nvme lookup",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, dummyError)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail get sys devices",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, nil, dummyError)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail validate lun",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, sysDevices, nil)
				env.osDevCon.EXPECT().ValidateLun(gomock.Any(), mpathDevice, lun, sysDevices, volId).Return(dummyError)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "native nvme uses the device itself as sys device",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NVMeNative, nil)
				// getSysDevicesFromMpath must not be called, a call would hit the real sysfs and fail the test.
				stubGetSysDevicesFromMpath(t, "", nil, errors.New("unexpected call"))
				env.osDevCon.EXPECT().ValidateLun(gomock.Any(), mpathDevice, lun, []string{dmSysFsName}, volId).Return(nil)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().IsNotMountPoint(stagingPath).Return(true, nil)
				env.mounter.EXPECT().FormatAndMount(mpathDevice, stagingPath, fsType, mountOptions)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "fail get disk format",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				expectValidated(t, env)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return("", dummyError)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail existing fsType different from requested",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				expectValidated(t, env)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return("different-fsType", nil)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.AlreadyExists)
			},
		},
		{
			name: "fail checking whether staging path is mounted",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				expectValidated(t, env)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().IsNotMountPoint(stagingPath).Return(false, dummyError)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail format and mount",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				expectValidated(t, env)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().IsNotMountPoint(stagingPath).Return(true, nil)
				env.mounter.EXPECT().FormatAndMount(mpathDevice, stagingPath, fsType, mountOptions).Return(dummyError)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success new filesystem",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				expectValidated(t, env)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return("", nil)
				env.utils.EXPECT().IsNotMountPoint(stagingPath).Return(true, nil)
				env.utils.EXPECT().FormatDevice(gomock.Any(), mpathDevice, fsType).Return(nil)
				env.mounter.EXPECT().FormatAndMount(mpathDevice, stagingPath, fsType, mountOptions)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success device already formatted",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				expectValidated(t, env)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().IsNotMountPoint(stagingPath).Return(true, nil)
				env.mounter.EXPECT().FormatAndMount(mpathDevice, stagingPath, fsType, mountOptions)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success idempotent",
			testFunc: func(t *testing.T) {
				env := newEnv(t)
				expectDiscovery(env)
				expectValidated(t, env)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().IsNotMountPoint(stagingPath).Return(false, nil)
				env.utils.EXPECT().IsDirectory(env.hostPath).Return(true)

				_, err := env.node.NodeStageVolume(context.TODO(), stagingRequest)
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

func TestNodeUnstageVolume(t *testing.T) {
	volId := "vol-test"
	dummyError := errors.New("Dummy error")
	stagingPath := "/test/path"

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
			name: "fail teardown volume",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, nil)
				hostPath := t.TempDir()
				stageInfoPath := filepath.Join(hostPath, driver.StageInfoFilename)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(hostPath)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(false)
				mockOsDeviceConHelper.EXPECT().TeardownVolume(gomock.Any(), stagingPath, volId).Return(dummyError)

				_, err := node.NodeUnstageVolume(context.TODO(), unstageRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "fail removing staging path",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, nil)
				hostPath := t.TempDir()
				// A non-empty directory makes os.Remove fail with something other than "not exist".
				if err := os.WriteFile(filepath.Join(hostPath, "leftover"), []byte("x"), 0600); err != nil {
					t.Fatalf("failed preparing staging dir: %v", err)
				}
				stageInfoPath := filepath.Join(hostPath, driver.StageInfoFilename)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(hostPath)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(false)
				mockOsDeviceConHelper.EXPECT().TeardownVolume(gomock.Any(), stagingPath, volId).Return(nil)

				_, err := node.NodeUnstageVolume(context.TODO(), unstageRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success idempotent, staging path already gone",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, nil)
				hostPath := filepath.Join(t.TempDir(), "gone")
				stageInfoPath := filepath.Join(hostPath, driver.StageInfoFilename)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(hostPath)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(false)
				mockOsDeviceConHelper.EXPECT().TeardownVolume(gomock.Any(), stagingPath, volId).Return(nil)

				_, err := node.NodeUnstageVolume(context.TODO(), unstageRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success even if clearing stage info fails",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtl)
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, nil)
				hostPath := t.TempDir()
				stageInfoPath := filepath.Join(hostPath, driver.StageInfoFilename)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(hostPath)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(true)
				mockNodeUtils.EXPECT().ClearStageInfoFile(stageInfoPath).Return(dummyError)
				mockOsDeviceConHelper.EXPECT().TeardownVolume(gomock.Any(), stagingPath, volId).Return(nil)

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
				node := newTestNodeServiceStaging(mockNodeUtils, nil, mockOsDeviceConHelper, nil)
				hostPath := t.TempDir()
				stageInfoPath := filepath.Join(hostPath, driver.StageInfoFilename)

				mockNodeUtils.EXPECT().GetPodPath(stagingPath).Return(hostPath)
				mockNodeUtils.EXPECT().GetVolumeUuid(volId).Return(volId)
				mockNodeUtils.EXPECT().StageInfoFileIsExist(stageInfoPath).Return(true)
				mockNodeUtils.EXPECT().ClearStageInfoFile(stageInfoPath).Return(nil)
				mockOsDeviceConHelper.EXPECT().TeardownVolume(gomock.Any(), stagingPath, volId).Return(nil)

				_, err := node.NodeUnstageVolume(context.TODO(), unstageRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
				if _, statErr := os.Stat(hostPath); !os.IsNotExist(statErr) {
					t.Fatalf("Expected staging path %s to be removed, stat error: %v", hostPath, statErr)
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
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPath).Return(false, nil)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPath).Return(false, nil)
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
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPath).Return(false, nil)
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
				mockNodeUtils.EXPECT().IsNotMountPoint(stagingPath).Return(false, nil)
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPath).Return(false, nil)
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
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(gomock.Any(), volumeId).Return(mpathDevice, nil)
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

				// GetPodPath is called by NodePublishVolume and again by isTargetMounted.
				mockNodeUtils.EXPECT().GetPodPath(targetPath).Return(targetPathWithHostPrefix).AnyTimes()
				mockNodeUtils.EXPECT().IsPathExists(targetPathWithHostPrefix).Return(true)
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPath).Return(true, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volumeId).Return(volumeId)
				mockOsDeviceConHelper.EXPECT().GetMpathDevice(gomock.Any(), volumeId).Return(mpathDevice, nil)
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
				mockNodeUtils.EXPECT().IsNotMountPoint(targetPath).Return(false, nil)
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
				mockNodeUtils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetBlockVolumeStats(gomock.Any(), volumePathWithHostPrefix).Return(driver.VolumeStatistics{},
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
				mockNodeUtils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetBlockVolumeStats(gomock.Any(), volumePathWithHostPrefix).Return(driver.VolumeStatistics{},
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
				mockNodeUtils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volumeId).Return(volumeUuid)
				mockOsDeviceConHelper.EXPECT().IsVolumePathMatchesVolumeId(gomock.Any(), volumeUuid, volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetFileSystemVolumeStats(gomock.Any(), volumePathWithHostPrefix).Return(driver.VolumeStatistics{}, errors.New("fail to get stats"))

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
				mockNodeUtils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, nil)
				mockNodeUtils.EXPECT().GetVolumeUuid(volumeId).Return(volumeUuid)
				mockOsDeviceConHelper.EXPECT().IsVolumePathMatchesVolumeId(gomock.Any(), volumeUuid, volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetFileSystemVolumeStats(gomock.Any(), volumePathWithHostPrefix).Return(volumeStats, nil)

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
				mockNodeUtils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(true, nil)
				mockNodeUtils.EXPECT().GetBlockVolumeStats(gomock.Any(), volumePathWithHostPrefix).Return(volumeStats, nil)

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
		VolumeIdLocksMap:            driver.NewSyncLock(1000, true),
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
	volumeUuid := "vol-test"
	volumePathWithHostPrefix := GetPodPath(volumePath)

	type expandEnv struct {
		utils   *mocks.MockNodeUtilsInterface
		helper  *mocks.MockOsDeviceConnectivityHelperScsiGenericInterface
		mounter *mocks.MockNodeMounter
		node    driver.NodeService
	}
	newExpandEnv := func(t *testing.T) *expandEnv {
		ctl := gomock.NewController(t)
		env := &expandEnv{
			utils:   mocks.NewMockNodeUtilsInterface(ctl),
			helper:  mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(ctl),
			mounter: mocks.NewMockNodeMounter(ctl),
		}
		env.node = newTestNodeServiceExpand(env.utils, env.helper, env.mounter)
		return env
	}
	// expectDevice: volume uuid resolved and the existing mpath device found.
	expectDevice := func(env *expandEnv) {
		env.utils.EXPECT().GetVolumeUuid(volId).Return(volumeUuid)
		env.utils.EXPECT().GetPodPath(volumePath).Return(volumePathWithHostPrefix)
		env.helper.EXPECT().GetExistingMpathDevice(gomock.Any(), volumeUuid, volumePathWithHostPrefix).Return(mpathDevice, nil)
	}
	// expectScsiExpanded: non-NVMe flow up to and including a successful multipath expand.
	expectScsiExpanded := func(t *testing.T, env *expandEnv) {
		expectDevice(env)
		stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, nil)
		stubGetSysDevicesFromMpath(t, dmSysFsName, sysDevices, nil)
		env.utils.EXPECT().RescanPhysicalDevices(gomock.Any(), dmSysFsName, sysDevices).Return(nil)
		env.utils.EXPECT().ExpandMpathDevice(gomock.Any(), dmSysFsName, sysDevices).Return(nil)
	}

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
				env := newExpandEnv(t)
				env.utils.EXPECT().GetVolumeUuid(volId).Return(volumeUuid)
				env.utils.EXPECT().GetPodPath(volumePath).Return(volumePathWithHostPrefix)
				env.helper.EXPECT().GetExistingMpathDevice(gomock.Any(), volumeUuid, volumePathWithHostPrefix).Return("", dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "devices are nvme lookup fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "unknown nvme type fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NvmeType("bogus"), nil)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "get sys devices fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, nil, dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "rescan fail is only logged, expand continues",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, sysDevices, nil)
				env.utils.EXPECT().RescanPhysicalDevices(gomock.Any(), dmSysFsName, sysDevices).Return(dummyError)
				env.utils.EXPECT().ExpandMpathDevice(gomock.Any(), dmSysFsName, sysDevices).Return(nil)
				env.utils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, nil)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().ExpandFilesystem(gomock.Any(), mpathDevice, stagingTargetPath, fsType).Return(nil)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "expand multipath device fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NotNVMe, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, sysDevices, nil)
				env.utils.EXPECT().RescanPhysicalDevices(gomock.Any(), dmSysFsName, sysDevices).Return(nil)
				env.utils.EXPECT().ExpandMpathDevice(gomock.Any(), dmSysFsName, sysDevices).Return(dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "is block check fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectScsiExpanded(t, env)
				env.utils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success expand raw block volume, no filesystem expand",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectScsiExpanded(t, env)
				env.utils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(true, nil)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "get disk format fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectScsiExpanded(t, env)
				env.utils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, nil)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return("", dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "expand filesystem fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectScsiExpanded(t, env)
				env.utils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, nil)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().ExpandFilesystem(gomock.Any(), mpathDevice, stagingTargetPath, fsType).Return(dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "nvme non native, get sys devices fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NVMeNonNative, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, nil, dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "nvme non native, rescan fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NVMeNonNative, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, sysDevices, nil)
				env.utils.EXPECT().RescanPhysicalDevices(gomock.Any(), dmSysFsName, sysDevices).Return(dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "nvme non native, expand multipath device fail",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NVMeNonNative, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, sysDevices, nil)
				env.utils.EXPECT().RescanPhysicalDevices(gomock.Any(), dmSysFsName, sysDevices).Return(nil)
				env.utils.EXPECT().ExpandMpathDevice(gomock.Any(), dmSysFsName, sysDevices).Return(dummyError)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				assertError(t, err, codes.Internal)
			},
		},
		{
			name: "success expand volume, nvme non native",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NVMeNonNative, nil)
				stubGetSysDevicesFromMpath(t, dmSysFsName, sysDevices, nil)
				env.utils.EXPECT().RescanPhysicalDevices(gomock.Any(), dmSysFsName, sysDevices).Return(nil)
				env.utils.EXPECT().ExpandMpathDevice(gomock.Any(), dmSysFsName, sysDevices).Return(nil)
				env.utils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, nil)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().ExpandFilesystem(gomock.Any(), mpathDevice, stagingTargetPath, fsType).Return(nil)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success expand volume, nvme native skips rescan and multipath expand",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectDevice(env)
				stubDevicesAreNvme(t, dmSysFsName, device_connectivity.NVMeNative, nil)
				env.utils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, nil)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().ExpandFilesystem(gomock.Any(), mpathDevice, stagingTargetPath, fsType).Return(nil)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
				if err != nil {
					t.Fatalf("Expect no error but got: %v", err)
				}
			},
		},
		{
			name: "success expand volume",
			testFunc: func(t *testing.T) {
				env := newExpandEnv(t)
				expectScsiExpanded(t, env)
				env.utils.EXPECT().IsBlock(gomock.Any(), volumePathWithHostPrefix).Return(false, nil)
				env.mounter.EXPECT().GetDiskFormat(mpathDevice).Return(fsType, nil)
				env.utils.EXPECT().ExpandFilesystem(gomock.Any(), mpathDevice, stagingTargetPath, fsType).Return(nil)

				_, err := env.node.NodeExpandVolume(context.TODO(), expandRequest)
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
			expNodeId:    "test-host",
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
			expNodeId:    "test-host",
			nvmeExists:   false,
			fcExists:     true,
			iscsiExists:  true,
		},
		{
			name:        "valid iqn and fcs",
			returnIqn:   "iqn.1994-07.com.redhat:e123456789",
			returnFcs:   []string{"10000000c9934d9f", "10000000c9934d9h"},
			expNodeId:   "test-host",
			nvmeExists:  false,
			fcExists:    true,
			iscsiExists: true,
		},
		{
			name:        "valid nqn, fcs and iqn",
			returnNqn:   "nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1",
			returnFcs:   []string{"10000000c9934d9f", "10000000c9934d9h"},
			returnIqn:   "iqn.1994-07.com.redhat:e123456789",
			expNodeId:   "test-host",
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
			expNodeId:   "test-host",
		},
		{
			name:        "fc path is inexistent",
			nvmeExists:  true,
			fcExists:    false,
			iscsiExists: true,
			returnNqn:   "nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1",
			returnIqn:   "iqn.1994-07.com.redhat:e123456789",
			expNodeId:   "test-host",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &csi.NodeGetInfoRequest{}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeNodeutils := mocks.NewMockNodeUtilsInterface(mockCtrl)
			d := newTestNodeService(fakeNodeutils, nil, nil)
			fakeNodeutils.EXPECT().UpdateNodeInitiatorsAnnotation(context.TODO(), d.Hostname, tc.returnNqn, tc.returnFcs, tc.returnIqn).AnyTimes()
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

// stubDevicesAreNvme replaces the sysfs-reading devicesAreNvme for the duration of the test.
func stubDevicesAreNvme(t *testing.T, expectedDevice string, result device_connectivity.NvmeType, err error) {
	restore := driver.SetDevicesAreNvmeForTest(func(_ context.Context, _ *executer.KeyedGater, device string) (device_connectivity.NvmeType, error) {
		if device != expectedDevice {
			t.Errorf("devicesAreNvme called with %q, expected %q", device, expectedDevice)
		}
		return result, err
	})
	t.Cleanup(restore)
}

// stubGetSysDevicesFromMpath replaces the sysfs-reading getSysDevicesFromMpath for the duration of the test.
func stubGetSysDevicesFromMpath(t *testing.T, expectedDevice string, result []string, err error) {
	restore := driver.SetGetSysDevicesFromMpathForTest(func(_ context.Context, _ *executer.KeyedGater, baseDevice string) ([]string, error) {
		if baseDevice != expectedDevice {
			t.Errorf("getSysDevicesFromMpath called with %q, expected %q", baseDevice, expectedDevice)
		}
		return result, err
	})
	t.Cleanup(restore)
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

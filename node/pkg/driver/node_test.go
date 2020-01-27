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
	"fmt"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	PublishContextParamLun          string = "PUBLISH_CONTEXT_LUN" // TODO for some reason I coun't take it from config.yaml
	PublishContextParamConnectivity string = "PUBLISH_CONTEXT_CONNECTIVITY"
)

//func TestNodeStageVolume(t *testing.T) {
//	stdVolCap := &csi.VolumeCapability{
//		AccessType: &csi.VolumeCapability_Mount{
//			Mount: &csi.VolumeCapability_MountVolume{},
//		},
//		AccessMode: &csi.VolumeCapability_AccessMode{
//			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
//		},
//	}
//	testCases := []struct {
//		name       string
//		req        *csi.NodeStageVolumeRequest
//		expErrCode codes.Code
//	}{
//		{
//			name: "fail no VolumeId",
//			req: &csi.NodeStageVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/path",
//				VolumeCapability:  stdVolCap,
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail no StagingTargetPath",
//			req: &csi.NodeStageVolumeRequest{
//				PublishContext:   map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				VolumeCapability: stdVolCap,
//				VolumeId:         "vol-test",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail no VolumeCapability",
//			req: &csi.NodeStageVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/path",
//				VolumeId:          "vol-test",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail invalid VolumeCapability ",
//			req: &csi.NodeStageVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/path",
//				VolumeCapability: &csi.VolumeCapability{
//					AccessMode: &csi.VolumeCapability_AccessMode{
//						Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
//					},
//				},
//				VolumeId: "vol-test",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		/*{
//			name: "fail because not implemented yet - but pass all basic verifications",
//			req: &csi.NodeStageVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/path",
//				VolumeCapability:  stdVolCap,
//				VolumeId:          "vol-test",
//			},
//			expErrCode: codes.Unimplemented,
//		},*/
//	}
//
//	for _, tc := range testCases {
//
//		t.Run(tc.name, func(t *testing.T) {
//
//			d := newTestNodeService(nil, nil)
//
//			_, err := d.NodeStageVolume(context.TODO(), tc.req)
//			if err != nil {
//				srvErr, ok := status.FromError(err)
//				if !ok {
//					t.Fatalf("Could not get error status code from error: %v", srvErr)
//				}
//				if srvErr.Code() != tc.expErrCode {
//					t.Fatalf("Expected error code %d, got %d message %s", tc.expErrCode, srvErr.Code(), srvErr.Message())
//				}
//			} else if tc.expErrCode != codes.OK {
//				t.Fatalf("Expected error %v, got no error", tc.expErrCode)
//			}
//		})
//	}
//}

func newTestNodeService(nodeUtils driver.NodeUtilsInterface, nodeMounter driver.NodeMounter) driver.NodeService {
	return driver.NewNodeService(driver.ConfigFile{},"test-host",	nodeUtils,nil,
								nil, nodeMounter, driver.NewSyncLock())
}

//
//func TestNodeUnstageVolume(t *testing.T) {
//	testCases := []struct {
//		name       string
//		expErrCode codes.Code
//	}{
//		{
//			name: "fail no VolumeId",
//			req: &csi.NodeUnstageVolumeRequest{
//				StagingTargetPath: "/test/path",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail no StagingTargetPath",
//			req: &csi.NodeUnstageVolumeRequest{
//				VolumeId: "vol-test",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail because not implemented yet - but pass all basic verifications",
//			req: &csi.NodeUnstageVolumeRequest{
//				VolumeId:          "vol-test",
//				StagingTargetPath: "/test/path",
//			},
//			expErrCode: codes.Unimplemented,
//		},
//	}
//
//	for _, tc := range testCases {
//		t.Run(tc.name, func(t *testing.T) {
//			d := newTestNodeService(nil)
//
//			_, err := d.NodeUnstageVolume(context.TODO(), tc.req)
//			if err != nil {
//				srvErr, ok := status.FromError(err)
//				if !ok {
//					t.Fatalf("Could not get error status code from error: %v", srvErr)
//				}
//				if srvErr.Code() != tc.expErrCode {
//					t.Fatalf("Expected error code %d, got %d message %s", tc.expErrCode, srvErr.Code(), srvErr.Message())
//				}
//			} else if tc.expErrCode != codes.OK {
//				t.Fatalf("Expected error %v, got no error", tc.expErrCode)
//			}
//		})
//	}
//}

func TestNodePublishVolume(t *testing.T) {
	fsTypeXfs := "ext4"
	targetPath := "/test/path"
	targetPathWithHostPrefix := path.Join(driver.PrefixChrootOfHostRoot, targetPath)
	targetPathParentDirWithHostPrefix := filepath.Dir(targetPathWithHostPrefix)
	stagingTargetPath := "/test/staging/path/.stageInfo.json"
	stagingTargetFile := path.Join(stagingTargetPath, ".stageInfo.json")
	deviceName := "fakedev"
	stagingInfo := map[string]string{"mpathDevice": deviceName}
	mpathDevice := filepath.Join(device_connectivity.DevPath, deviceName)

	fsVolCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{FsType: fsTypeXfs},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	rawBlockVolumeCap := &csi.VolumeCapability{
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
		AccessType: &csi.VolumeCapability_Block{
			Block: &csi.VolumeCapability_BlockVolume{},
		},
	}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success with filesystem volume",
			testFunc: func(t *testing.T) {
				mockCtl := gomock.NewController(t)
				defer mockCtl.Finish()
				mockMounter := mocks.NewMockNodeMounter(mockCtl)
				mockNodeUtils := mocks.NewMockNodeUtilsInterface(mockCtl)
				driver := newTestNodeService(mockNodeUtils, mockMounter)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(gomock.Eq(stagingTargetFile)).Return(stagingInfo, nil)
				mockMounter.EXPECT().List().Return(nil, nil)
				mockMounter.EXPECT().MakeDir(gomock.Eq(targetPathWithHostPrefix)).Return(nil)
				mockMounter.EXPECT().FormatAndMount(mpathDevice, targetPath, fsTypeXfs, nil)

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  fsVolCap,
					VolumeId:          "vol-test",
				}

				_, err := driver.NodePublishVolume(context.TODO(), req)
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
				driver := newTestNodeService(mockNodeUtils, mockMounter)

				mockNodeUtils.EXPECT().ReadFromStagingInfoFile(gomock.Eq(stagingTargetFile)).Return(stagingInfo, nil)
				mockMounter.EXPECT().List().Return(nil, nil)
				mockMounter.EXPECT().MakeDir(gomock.Eq(targetPathParentDirWithHostPrefix)).Return(nil)
				mockMounter.EXPECT().MakeFile(gomock.Eq(targetPathWithHostPrefix)).Return(nil)
				mockMounter.EXPECT().Mount(mpathDevice, targetPath, "", []string{"bind"})

				req := &csi.NodePublishVolumeRequest{
					PublishContext:    map[string]string{},
					StagingTargetPath: stagingTargetPath,
					TargetPath:        targetPath,
					VolumeCapability:  rawBlockVolumeCap,
					VolumeId:          "vol-test",
				}

				_, err := driver.NodePublishVolume(context.TODO(), req)
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
	existingTargetPath := "/tmp/TestNodeUnpublishVolume.txt"
	nonExistingTargetPath := "/test/path"
	os.MkdirAll("/host/tmp", 0755)
	err := ioutil.WriteFile("/host/tmp/TestNodeUnpublishVolume.txt", []byte("Hello"), 0755)
	if err != nil {
		fmt.Printf("Unable to write file: %v", err)
	}
	defer os.RemoveAll(existingTargetPath)

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "fail no VolumeId",
			testFunc: func(t *testing.T) {
				driver := newTestNodeService(nil, nil)

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: existingTargetPath,
				}
				_, err := driver.NodeUnpublishVolume(context.TODO(), req)
				assertError(t, err, codes.InvalidArgument)
			},
		},
		{
			name: "fail no TargetPath",
			testFunc: func(t *testing.T) {
				driver := newTestNodeService(nil, nil)

				req := &csi.NodeUnpublishVolumeRequest{
					VolumeId: "vol-test",
				}
				_, err := driver.NodeUnpublishVolume(context.TODO(), req)
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
				driver := newTestNodeService(mockNodeUtils, mockMounter)

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: existingTargetPath,
					VolumeId:   "vol-test",
				}
				mockMounter.EXPECT().Unmount(gomock.Eq(existingTargetPath)).Return(nil)
				_, err := driver.NodeUnpublishVolume(context.TODO(), req)
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
				driver := newTestNodeService(mockNodeUtils, mockMounter)

				req := &csi.NodeUnpublishVolumeRequest{
					TargetPath: nonExistingTargetPath,
					VolumeId:   "vol-test",
				}
				//mockMounter.EXPECT().Unmount(gomock.Eq(nonExistingTargetPath)).Return(nil)
				_, err := driver.NodeUnpublishVolume(context.TODO(), req)
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

//func TestNodePublishVolume(t *testing.T) {
//	stdVolCap := &csi.VolumeCapability{
//		AccessType: &csi.VolumeCapability_Mount{
//			Mount: &csi.VolumeCapability_MountVolume{},
//		},
//		AccessMode: &csi.VolumeCapability_AccessMode{
//			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
//		},
//	}
//	rawBlockVolumeCapability := &csi.VolumeCapability{
//		AccessMode: &csi.VolumeCapability_AccessMode{
//			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
//		},
//		AccessType: &csi.VolumeCapability_Block{
//			Block: &csi.VolumeCapability_BlockVolume{},
//		},
//	}
//	testCases := []struct {
//		name       string
//		req        *csi.NodePublishVolumeRequest
//		expErrCode codes.Code
//	}{
//		{
//			name: "fail no VolumeId",
//			req: &csi.NodePublishVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/staging/path",
//				TargetPath:        "/test/target/path",
//				VolumeCapability:  stdVolCap,
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail no StagingTargetPath",
//			req: &csi.NodePublishVolumeRequest{
//				PublishContext:   map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				TargetPath:       "/test/target/path",
//				VolumeCapability: stdVolCap,
//				VolumeId:         "vol-test",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail no TargetPath",
//			req: &csi.NodePublishVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/staging/path",
//				VolumeCapability:  stdVolCap,
//				VolumeId:          "vol-test",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail no VolumeCapability",
//			req: &csi.NodePublishVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/staging/path",
//				TargetPath:        "/test/target/path",
//				VolumeId:          "vol-test",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail invalid VolumeCapability",
//			req: &csi.NodePublishVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/staging/path",
//				TargetPath:        "/test/target/path",
//				VolumeId:          "vol-test",
//				VolumeCapability: &csi.VolumeCapability{
//					AccessMode: &csi.VolumeCapability_AccessMode{
//						Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
//					},
//				},
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "pass volume with file system",
//			req: &csi.NodePublishVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/staging/path",
//				TargetPath:        "/test/target/path",
//				VolumeCapability:  stdVolCap,
//				VolumeId:          "vol-test",
//			},
//			//expErrCode: nil,
//		},
//		{
//			name: "pass raw block volume",
//			req: &csi.NodePublishVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/staging/path",
//				TargetPath:        "/test/target/path",
//				VolumeCapability:  rawBlockVolumeCapability,
//				VolumeId:          "vol-test",
//			},
//			//expErrCode: nil,
//		},
//	}
//
//	for _, tc := range testCases {
//		t.Run(tc.name, func(t *testing.T) {
//			d := newTestNodeService(nil)
//
//			_, err := d.NodePublishVolume(context.TODO(), tc.req)
//			if err != nil {
//				srvErr, ok := status.FromError(err)
//				if !ok {
//					t.Fatalf("Could not get error status code from error: %v", srvErr)
//				}
//				if srvErr.Code() != tc.expErrCode {
//					t.Fatalf("Expected error code %d, got %d message %s", tc.expErrCode, srvErr.Code(), srvErr.Message())
//				}
//			} else if tc.expErrCode != codes.OK {
//				t.Fatalf("Expected error %v and got no error", tc.expErrCode)
//			}
//
//		})
//	}
//}
//
//func TestNodeUnpublishVolume(t *testing.T) {
//	testCases := []struct {
//		name string
//		req  *csi.NodeUnpublishVolumeRequest
//		// expected test error code
//		expErrCode codes.Code
//	}{
//		{
//			name: "fail no VolumeId",
//			req: &csi.NodeUnpublishVolumeRequest{
//				TargetPath: "/test/path",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail no TargetPath",
//			req: &csi.NodeUnpublishVolumeRequest{
//				VolumeId: "vol-test",
//			},
//			expErrCode: codes.InvalidArgument,
//		},
//		{
//			name: "fail because not implemented yet - but pass all basic verifications",
//			req: &csi.NodeUnpublishVolumeRequest{
//				VolumeId:   "vol-test",
//				TargetPath: "/test/path",
//			},
//			expErrCode: codes.Unimplemented,
//		},
//	}
//
//	for _, tc := range testCases {
//		t.Run(tc.name, func(t *testing.T) {
//			d := newTestNodeService(nil)
//
//			_, err := d.NodeUnpublishVolume(context.TODO(), tc.req)
//			if err != nil {
//				srvErr, ok := status.FromError(err)
//				if !ok {
//					t.Fatalf("Could not get error status code from error: %v", srvErr)
//				}
//				if srvErr.Code() != tc.expErrCode {
//					t.Fatalf("Expected error code %d, got %d message %s", tc.expErrCode, srvErr.Code(), srvErr.Message())
//				}
//			} else if tc.expErrCode != codes.OK {
//				t.Fatalf("Expected error %v, got no error", tc.expErrCode)
//			}
//		})
//	}
//}

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
		iscsiExists      bool
		fcExists       bool
	}{
		{
			name: "good iqn, empty fc with error from node_utils",
			return_fc_err: fmt.Errorf("some error"),
			expErr: status.Error(codes.Internal, fmt.Errorf("some error").Error()),
			iscsiExists: true,
			fcExists: true,
		},
		{
			name: "empty iqn with error, one fc port",
			return_fcs: []string{"10000000c9934d9f"},
			expNodeId: "test-host;;10000000c9934d9f",
			iscsiExists: true,
			fcExists: true,
		},
		{
			name: "empty iqn with error from node_utils, one more fc ports",
			return_iqn: "",
			return_fcs: []string{"10000000c9934d9f","10000000c9934d9h"},
			expNodeId: "test-host;;10000000c9934d9f:10000000c9934d9h",
			iscsiExists: true,
			fcExists: true,
		},
		{
			name: "good iqn and good fcs",
			return_iqn: "iqn.1994-07.com.redhat:e123456789",
			return_fcs: []string{"10000000c9934d9f","10000000c9934d9h"},
			expNodeId: "test-host;iqn.1994-07.com.redhat:e123456789;10000000c9934d9f:10000000c9934d9h",
			iscsiExists: true,
			fcExists: true,
		},
		{
			name: "iqn and fc path are inexistent",
			iscsiExists: false,
			fcExists: false,
			expErr: status.Error(codes.Internal, fmt.Errorf("Cannot find valid fc wwns or iscsi iqn").Error()),
		},
		{
			name: "iqn path is inexistsent",
			iscsiExists: false,
			fcExists: true,
			return_fcs: []string{"10000000c9934d9f"},
			expNodeId: "test-host;;10000000c9934d9f",
		},
		{
			name: "fc path is inexistent",
			iscsiExists: true,
			fcExists: false,
			return_iqn: "iqn.1994-07.com.redhat:e123456789",
			expNodeId: "test-host;iqn.1994-07.com.redhat:e123456789;",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &csi.NodeGetInfoRequest{}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_nodeutils := mocks.NewMockNodeUtilsInterface(mockCtrl)
			fake_nodeutils.EXPECT().Exists(driver.FCPath).Return(tc.fcExists)
			if tc.fcExists {
				fake_nodeutils.EXPECT().ParseFCPorts().Return(tc.return_fcs, tc.return_fc_err)
			}
			if tc.return_fc_err == nil {
				fake_nodeutils.EXPECT().Exists(driver.IscsiFullPath).Return(tc.iscsiExists)
				if tc.iscsiExists {
					fake_nodeutils.EXPECT().ParseIscsiInitiators().Return(tc.return_iqn, tc.return_iqn_err)
				}
			}

			d:= newTestNodeService(fake_nodeutils, nil)

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

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
	"github.com/container-storage-interface/spec/lib/go/csi"
	gomock "github.com/golang/mock/gomock"
	mocks "github.com/ibm/ibm-block-csi-driver/node/mocks"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	driver "github.com/ibm/ibm-block-csi-driver/node/pkg/driver"
	"reflect"
	"testing"
	"fmt"
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
//			name: "fail invalid VolumeCapability Block instead of Mount",
//			req: &csi.NodeStageVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/path",
//				VolumeCapability: &csi.VolumeCapability{
//					AccessMode: &csi.VolumeCapability_AccessMode{
//						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
//					},
//					AccessType: &csi.VolumeCapability_Block{
//						Block: &csi.VolumeCapability_BlockVolume{},
//					},
//				},
//				VolumeId: "vol-test",
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
//		{
//			name: "fail because not implemented yet - but pass all basic verifications",
//			req: &csi.NodeStageVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/path",
//				VolumeCapability:  stdVolCap,
//				VolumeId:          "vol-test",
//			},
//			expErrCode: codes.Unimplemented,
//		},
//	}
//
//	for _, tc := range testCases {
//
//		t.Run(tc.name, func(t *testing.T) {
//
//			d := newTestNodeService(nil)
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

func newTestNodeService(nodeUtils driver.NodeUtilsInterface) driver.NodeService {
	return driver.NodeService{
		Hostname:   "test-host",
		ConfigYaml: driver.ConfigFile{},
		NodeUtils:  nodeUtils,
	}
}
//
//func TestNodeUnstageVolume(t *testing.T) {
//	testCases := []struct {
//		name       string
//		req        *csi.NodeUnstageVolumeRequest
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
//
//func TestNodePublishVolume(t *testing.T) {
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
//			name: "fail because not implemented yet - but pass all basic verifications",
//			req: &csi.NodePublishVolumeRequest{
//				PublishContext:    map[string]string{PublishContextParamLun: "1", PublishContextParamConnectivity: "iSCSI"},
//				StagingTargetPath: "/test/staging/path",
//				TargetPath:        "/test/target/path",
//				VolumeCapability:  stdVolCap,
//				VolumeId:          "vol-test",
//			},
//			expErrCode: codes.Unimplemented,
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

	d := newTestNodeService(nil)

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

	d := newTestNodeService(nil)

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
		returned_iqn   string
		returned_error error
		expErr         error
		expNodeId      string
	}{
		{
			name:         "good IQN",
			returned_iqn: "iqn.1994-07.com.redhat:e123456789",
			expNodeId:    "test-host;iqn.1994-07.com.redhat:e123456789",
		},
		{
			name:           "error from node_utils",
			returned_iqn:   "",
			returned_error: fmt.Errorf("some error "),
			expErr:         status.Error(codes.Internal, fmt.Errorf("some error ").Error()),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &csi.NodeGetInfoRequest{}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_nodeutils := mocks.NewMockNodeUtilsInterface(mockCtrl)
			fake_nodeutils.EXPECT().ParseIscsiInitiators("/etc/iscsi/initiatorname.iscsi").Return(tc.returned_iqn, tc.returned_error)

			d := newTestNodeService(fake_nodeutils)

			expReponse := &csi.NodeGetInfoResponse{NodeId: tc.expNodeId}

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
				if res.NodeId != expReponse.NodeId {
					t.Fatalf("Expected res : {%v}, and got {%v}", expReponse, res)
				}
			}

		})

	}
}


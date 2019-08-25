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

package device_connectivity_test

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	gomock "github.com/golang/mock/gomock"
	mocks "github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

type WaitForPathToExistReturn struct {
	devicePaths []string
	exists      bool
	err         error
}

func NewOsDeviceConnectivityIscsiForTest(
	executer executer.ExecuterInterface,
	helper device_connectivity.OsDeviceConnectivityHelperIscsiInterface,
    helperScsiGeneric	device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface,
) device_connectivity.OsDeviceConnectivityInterface {
	return &device_connectivity.OsDeviceConnectivityIscsi{
		Executer:          executer,		
		Helper:            helper,
		HelperScsiGeneric: helperScsiGeneric,		
	}
}

type GetMultipathDiskReturn struct {
	pathParam string
	path      string
	err       error
}

func TestGetMpathDevice(t *testing.T) {
	testCases := []struct {
		name string

		expErrType               reflect.Type
		expErr                   error
		expDMdevice              string
		waitForPathToExistReturn WaitForPathToExistReturn
		getMultipathDiskReturns  []GetMultipathDiskReturn
	}{
		{
			name: "Should fail when WaitForPathToExist not found any sd device",
			waitForPathToExistReturn: WaitForPathToExistReturn{
				devicePaths: nil,
				exists:      false,
				err:         nil,
			},

			expErrType:  reflect.TypeOf(&device_connectivity.MultipleDeviceNotFoundForLunError{}),
			expDMdevice: "",
		},
		{
			name: "Should fail when WaitForPathToExist fail for some reason",
			waitForPathToExistReturn: WaitForPathToExistReturn{
				devicePaths: nil,
				exists:      false,
				err:         fmt.Errorf("error"),
			},

			expErr:      fmt.Errorf("error"),
			expDMdevice: "",
		},

		{
			name: "Should fail when GetMultipathDisk fail for some reason",
			waitForPathToExistReturn: WaitForPathToExistReturn{
				devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-ID1-lun1"},
				exists:      true,
				err:         nil,
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-ID1-lun1",
					path:      "",
					err:       fmt.Errorf("error"),
				},
			},

			expErr:      fmt.Errorf("error"),
			expDMdevice: "",
		},

		{
			name: "Should fail when GetMultipathDisk fail for some reason",
			waitForPathToExistReturn: WaitForPathToExistReturn{
				devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-ID1-lun1"},
				exists:      true,
				err:         nil,
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-ID1-lun1",
					path:      "",
					err:       fmt.Errorf("error"),
				},
			},

			expErr:      fmt.Errorf("error"),
			expDMdevice: "",
		},

		{
			name: "Should fail when GetMultipathDisk provide 2 different dms that apply to the same lun (bas multipathing case)",
			waitForPathToExistReturn: WaitForPathToExistReturn{
				devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-ID1-lun1", "/dev/disk/by-path/ip1-iscsi-ID1-lun1___2"},
				exists:      true,
				err:         nil,
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-ID1-lun1",
					path:      "dm-1",
					err:       nil,
				},
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-ID1-lun1___2",
					path:      "dm-2", // The main point, look like multipath crazy and give to the same vol but different path a different md device, which is wrong case - so we check it.
					err:       nil,
				},
			},

			expErrType:  reflect.TypeOf(&device_connectivity.MultipleDmDevicesError{}),
			expDMdevice: "",
		},

		{
			name: "Should fail when GetMultipathDisk is ok but return no dm devices - empty string)",
			waitForPathToExistReturn: WaitForPathToExistReturn{
				devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-ID1-lun1"},
				exists:      true,
				err:         nil,
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-ID1-lun1",
					path:      "", // this is the thing for this test
					err:       nil,
				},
			},

			expErrType:  reflect.TypeOf(&device_connectivity.MultipleDeviceNotFoundForLunError{}),
			expDMdevice: "",
		},

		{
			name: "Should succeed to GetMpathDevice - good path",
			waitForPathToExistReturn: WaitForPathToExistReturn{
				devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-ID1-lun1", "/dev/disk/by-path/ip1-iscsi-ID1-lun1___2"},
				exists:      true,
				err:         nil,
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-ID1-lun1",
					path:      "dm-1",
					err:       nil,
				},
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-ID1-lun1___2",
					path:      "dm-1", // the same because there are 2 paths to the storage, so we should find 2 sd devices that point to the same dm device
					err:       nil,
				},
			},

			expErr:      nil,
			expDMdevice: "dm-1",
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_helper := mocks.NewMockOsDeviceConnectivityHelperIscsiInterface(mockCtrl)
			fake_helper_scsi_generic := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtrl)
			lunId := 0
			arrayIdentifier := "X"
			path := strings.Join([]string{"/dev/disk/by-path/ip*", "iscsi", arrayIdentifier, "lun", strconv.Itoa(lunId)}, "-")
			fake_helper.EXPECT().WaitForPathToExist(path, 5, 1).Return(
				tc.waitForPathToExistReturn.devicePaths,
				tc.waitForPathToExistReturn.exists,
				tc.waitForPathToExistReturn.err,
			)

			var mcalls []*gomock.Call
			for _, r := range tc.getMultipathDiskReturns {
				call := fake_helper.EXPECT().GetMultipathDisk(r.pathParam).Return(r.path, r.err)
				mcalls = append(mcalls, call)
			}
			gomock.InOrder(mcalls...)

			o := NewOsDeviceConnectivityIscsiForTest(fake_executer, fake_helper, fake_helper_scsi_generic)
			DMdevice, err := o.GetMpathDevice("volIdNotRelevant", lunId, arrayIdentifier)
			if tc.expErr != nil || tc.expErrType != nil {
				if err == nil {
					t.Fatalf("Expected to fail with error, got success.")
				}
				if tc.expErrType != nil {
					if reflect.TypeOf(err) != tc.expErrType {
						t.Fatalf("Expected error type %v, got different error %v", tc.expErrType, reflect.TypeOf(err))
					}
				} else {
					if err.Error() != tc.expErr.Error() {
						t.Fatalf("Expected error code %s, got %s", tc.expErr, err.Error())
					}
				}
			}

			if tc.expDMdevice != DMdevice {
				t.Fatalf("Expected found mpath device %v, got %v", tc.expDMdevice, DMdevice)
			}

		})
	}
}

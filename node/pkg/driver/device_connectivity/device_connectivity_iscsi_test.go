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
	gomock "github.com/golang/mock/gomock"
	mocks "github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type WaitForPathToExistReturn struct {
	devicePaths []string
	exists      bool
	err         error
}

func NewOsDeviceConnectivityIscsiForTest(executer executer.ExecuterInterface,
	helper device_connectivity.OsDeviceConnectivityHelperIscsiInterface) device_connectivity.OsDeviceConnectivityInterface {
	return &device_connectivity.OsDeviceConnectivityIscsi{
		Executer:        executer,
		MutexMultipathF: &sync.Mutex{},
		Helper:          helper,
	}
}

func TestGetMpathDevice(t *testing.T) {
	testCases := []struct {
		name string

		expErrType               reflect.Type
		expErr                   error
		expDMdevice              string
		waitForPathToExistReturn WaitForPathToExistReturn
	}{
		{
			name:        "Should fail when WaitForPathToExist not found any sd device",
			expErrType: reflect.TypeOf(&device_connectivity.MultipleDeviceNotFoundForLunError{}),
			expDMdevice: "",
			waitForPathToExistReturn: WaitForPathToExistReturn{
				devicePaths: nil,
				exists:      false,
				err:         nil,
			},
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_helper := mocks.NewMockOsDeviceConnectivityHelperIscsiInterface(mockCtrl)
			lunId := 0
			arrayIdentifier := "X"
			path := strings.Join([]string{"/dev/disk/by-path/ip*", "iscsi", arrayIdentifier, "lun", strconv.Itoa(lunId)}, "-")
			fake_helper.EXPECT().WaitForPathToExist(path, 5, 1).Return(
				tc.waitForPathToExistReturn.devicePaths,
				tc.waitForPathToExistReturn.exists,
				tc.waitForPathToExistReturn.err,
			)

			o := NewOsDeviceConnectivityIscsiForTest(fake_executer, fake_helper)
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

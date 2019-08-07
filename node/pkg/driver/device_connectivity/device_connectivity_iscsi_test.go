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
	//"reflect"
	"testing"
	"fmt"
	"os"
)

func TestHelperWaitForPathToExist(t *testing.T) {
	testCases := []struct {
		name string
		fpaths []string
		expErr error
		expFound bool
		globReturnErr error
	}{
		{
			name: "Should fail when Glob return error",
			fpaths: nil,
			globReturnErr: fmt.Errorf("error"),
			expErr: fmt.Errorf("error"),
			expFound: false,
		},
		{
			name: "Should fail when Glob succeed but with no paths",
			fpaths: nil,
			globReturnErr: nil,
			expErr: os.ErrNotExist,
			expFound: false,
		},
		{
			name: "Should fail when Glob return error",
			fpaths: []string{"/a/a", "/a/b"},
			globReturnErr: nil,
			expErr: nil,
			expFound: true,
		},

	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			devicePath := "/dev/disk/by-path/ip-ARRAYIP-iscsi-ARRAYIQN-lun-LUNID"
			fake_executer.EXPECT().FilepathGlob(devicePath).Return(tc.fpaths, tc.globReturnErr)
			helperIscsi := device_connectivity.NewOsDeviceConnectivityHelperIscsi(fake_executer)
			
			_, found, err := helperIscsi.WaitForPathToExist(devicePath, 1, 1)
			if err != nil {
				if err.Error() != tc.expErr.Error() {
					t.Fatalf("Expected error code %s, got %s", tc.expErr, err.Error())
				}
			}
			if found != tc.expFound{
					t.Fatalf("Expected found boolean code %t, got %t", tc.expFound, found)
			}
			

		})
	}
}


func TestHelperGetMultipathDisk(t *testing.T) {
	testCases := []struct {
		name    string
		osReadLinkReturnPath string
		osReadLinkReturnExc error
		
		globShouldRun bool
		globReturnfpaths []string
		globReturnErr error
	
		expErr  error
		expPath string
		
	}{
		{
			name: "Should fail to if OsReadlink return error",
			osReadLinkReturnPath: "",
			osReadLinkReturnExc: fmt.Errorf("error"),
			globShouldRun: false,

			expErr: fmt.Errorf("error"),
			expPath: "",
		},
		{
			name: "Succeed if OsReadlink return md-* device instead of sdX",
			osReadLinkReturnPath: "../../dm-4",
			osReadLinkReturnExc: nil,
			globShouldRun: false,
			
			expErr: nil,
			expPath: "dm-4",
			// TODO add here some way to check that FilepathGlobs was not called at all.
		},

		{
			name: "Fail if OsReadlink return sdX but FilepathGlob failed",
			osReadLinkReturnPath: "../../sdb",
			osReadLinkReturnExc: nil,
			
			globShouldRun: true,
			globReturnfpaths: nil,
			globReturnErr: fmt.Errorf("error"),
			
			expErr: fmt.Errorf("error"),
			expPath: "",
		},

	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			path := "/dev/disk/by-path/TARGET-iscsi-iqn:5"
			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_executer.EXPECT().OsReadlink(path).Return(tc.osReadLinkReturnPath, tc.osReadLinkReturnExc)
			
			if tc.globShouldRun{
				fake_executer.EXPECT().FilepathGlob("/sys/block/dm-*").Return(tc.globReturnfpaths, tc.globReturnErr)
			}
			helperIscsi := device_connectivity.NewOsDeviceConnectivityHelperIscsi(fake_executer)
			
			returnPath, err := helperIscsi.GetMultipathDisk(path)
			if err != nil {
				if err.Error() != tc.expErr.Error() {
					t.Fatalf("Expected error code %s, got %s", tc.expErr, err.Error())
				}
			}
			if returnPath != tc.expPath{
					t.Fatalf("Expected found multipath device %s, got %s", tc.expPath, returnPath)
			}

		})
	}
}

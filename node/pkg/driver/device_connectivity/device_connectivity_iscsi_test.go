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
	"fmt"
	"os"
	"testing"
)

func TestHelperWaitForPathToExist(t *testing.T) {
	testCases := []struct {
		name          string
		fpaths        []string
		expErr        error
		expFound      bool
		globReturnErr error
	}{
		{
			name:          "Should fail when Glob return error",
			fpaths:        nil,
			globReturnErr: fmt.Errorf("error"),
			expErr:        fmt.Errorf("error"),
			expFound:      false,
		},
		{
			name:          "Should fail when Glob succeed but with no paths",
			fpaths:        nil,
			globReturnErr: nil,
			expErr:        os.ErrNotExist,
			expFound:      false,
		},
		{
			name:          "Should fail when Glob return error",
			fpaths:        []string{"/a/a", "/a/b"},
			globReturnErr: nil,
			expErr:        nil,
			expFound:      true,
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
			if found != tc.expFound {
				t.Fatalf("Expected found boolean code %t, got %t", tc.expFound, found)
			}

		})
	}
}

type globReturn struct {
	globReturnfpaths []string
	globReturnErr    error
}

func TestHelperGetMultipathDisk(t *testing.T) {
	testCases := []struct {
		name                 string
		osReadLinkReturnPath string
		osReadLinkReturnExc  error
		globReturns          []globReturn

		expErr             error
		notCheckExpErrType bool // if true then test will check the specific ExpErrType
		expPath            string
	}{
		{
			name:                 "Should fail to if OsReadlink return error",
			osReadLinkReturnPath: "",
			osReadLinkReturnExc:  fmt.Errorf("error"),
			globReturns:          nil,

			expErr:  fmt.Errorf("error"),
			expPath: "",
		},
		{
			name:                 "Succeed if OsReadlink return md-* device instead of sdX",
			osReadLinkReturnPath: "../../dm-4",
			osReadLinkReturnExc:  nil,
			globReturns:          nil,

			expErr:  nil,
			expPath: "dm-4",
			// TODO add here some way to check that FilepathGlobs was not called at all.
		},

		{
			name:                 "Fail if OsReadlink return sdX but FilepathGlob failed",
			osReadLinkReturnPath: "../../sdb",
			osReadLinkReturnExc:  nil,
			globReturns: []globReturn{
				globReturn{
					globReturnfpaths: nil,
					globReturnErr:    fmt.Errorf("error"),
				},
			},

			expErr:  fmt.Errorf("error"),
			expPath: "",
		},
		{
			name:                 "Fail if OsReadlink return sdX but FilepathGlob not show any of the sdX",
			osReadLinkReturnPath: "../../sdb",
			osReadLinkReturnExc:  nil,

			globReturns: []globReturn{
				globReturn{
					globReturnfpaths: []string{"/sys/block/dm-4"},
					globReturnErr:    nil,
				},
				globReturn{
					globReturnfpaths: nil, // so no dm devices found at all /sys/block/dm-*/slaves
					globReturnErr:    nil,
				},
			},

			notCheckExpErrType: true,
			expPath:            "",
		},

		{
			name:                 "Should succeed to find the /dev/dm-4",
			osReadLinkReturnPath: "../../sdb",
			osReadLinkReturnExc:  nil,

			globReturns: []globReturn{
				globReturn{
					globReturnfpaths: []string{"/sys/block/dm-4"},
					globReturnErr:    nil,
				},
				globReturn{
					globReturnfpaths: []string{"/sys/block/dm-4/slaves/sdb"},
					globReturnErr:    nil,
				},
			},

			expErr:  nil,
			expPath: "/dev/dm-4",
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			path := "/dev/disk/by-path/TARGET-iscsi-iqn:5"
			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_executer.EXPECT().OsReadlink(path).Return(tc.osReadLinkReturnPath, tc.osReadLinkReturnExc)

			if len(tc.globReturns) == 1 {
				fake_executer.EXPECT().FilepathGlob("/sys/block/dm-*").Return(tc.globReturns[0].globReturnfpaths, tc.globReturns[0].globReturnErr)
			} else if len(tc.globReturns) == 2 {
				first := fake_executer.EXPECT().FilepathGlob("/sys/block/dm-*").Return(tc.globReturns[0].globReturnfpaths, tc.globReturns[0].globReturnErr)
				second := fake_executer.EXPECT().FilepathGlob("/sys/block/dm-4/slaves/*").Return(tc.globReturns[1].globReturnfpaths, tc.globReturns[1].globReturnErr)

				gomock.InOrder(first, second)
			}
			helperIscsi := device_connectivity.NewOsDeviceConnectivityHelperIscsi(fake_executer)

			returnPath, err := helperIscsi.GetMultipathDisk(path)
			if err != nil {
				if tc.notCheckExpErrType {
					_, ok := err.(*device_connectivity.MultipleDeviceNotFoundError)
					if !ok {
						t.Fatalf("Expected error type MultipleDeviceNotFoundError, got different error")
					}
				} else {
					if err.Error() != tc.expErr.Error() {
						t.Fatalf("Expected error code %s, got %s", tc.expErr, err.Error())
					}
				}
			}
			if returnPath != tc.expPath {
				t.Fatalf("Expected found multipath device %s, got %s", tc.expPath, returnPath)
			}

		})
	}
}

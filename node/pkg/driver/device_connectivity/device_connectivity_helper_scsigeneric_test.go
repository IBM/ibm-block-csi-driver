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
	"os"
	"path"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	gomock "github.com/golang/mock/gomock"
	mocks "github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

type WaitForPathToExistReturn struct {
	path        string
	devicePaths []string
	exists      bool
	err         error
}

func NewOsDeviceConnectivityHelperScsiGenericForTest(
	executer executer.ExecuterInterface,
	helper device_connectivity.OsDeviceConnectivityHelperInterface,
	mutexLock *sync.Mutex,
) device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface {
	return &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{
		Executer:        executer,
		Helper:          helper,
		MutexMultipathF: mutexLock,
	}
}

const byPathDir = "/dev/disk/by-path"

func getFcPath(fileNameSuffix string) string {
	var fileNamePrefix = "pci"
	if runtime.GOARCH == "s390x" {
		fileNamePrefix = "ccw"
	}
	var fileName = fileNamePrefix + fileNameSuffix
	return path.Join(byPathDir, fileName)
}

type GetMultipathDiskReturn struct {
	pathParam string
	path      string
	err       error
}

func TestGetMpathDevice(t *testing.T) {
	testCasesIscsi := []struct {
		name             string
		arrayIdentifiers []string

		expErrType                reflect.Type
		expErr                    error
		expDMdevice               string
		waitForPathToExistReturns []WaitForPathToExistReturn
		getMultipathDiskReturns   []GetMultipathDiskReturn
	}{
		{
			name:             "Should fail when WaitForPathToExist not found any sd device",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: nil,
					exists:      false,
					err:         nil,
				},
			},

			expErr:      fmt.Errorf("Couldn't find multipath device for volumeID [volIdNotRelevant] lunID [0] from array [[X]]. Please check the host connectivity to the storage."),
			expDMdevice: "",
		},

		{
			name:             "Should fail when WaitForPathToExist fail for some reason",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: nil,
					exists:      false,
					err:         fmt.Errorf("error"),
				},
			},

			expErr:      fmt.Errorf("error"),
			expDMdevice: "",
		},

		{
			name:             "Should fail when GetMultipathDisk fail for some reason",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-ID1-lun1"},
					exists:      true,
					err:         nil,
				},
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
			name:             "Should fail when GetMultipathDisk provide 2 different dms that apply to the same lun (bas multipathing case)",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-ID1-lun1", "/dev/disk/by-path/ip1-iscsi-ID1-lun1___2"},
					exists:      true,
					err:         nil,
				},
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
			name:             "Should succeed to GetMpathDevice - good path",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-ID1-lun1", "/dev/disk/by-path/ip1-iscsi-ID1-lun1___2"},
					exists:      true,
					err:         nil,
				},
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

		{
			name:             "Should succeed to GetMpathDevice with one more iqns",
			arrayIdentifiers: []string{"X", "Y"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-X-lun1"},
					exists:      true,
					err:         nil,
				},
				WaitForPathToExistReturn{
					devicePaths: []string{"/dev/disk/by-path/ip1-iscsi-Y-lun2"},
					exists:      true,
					err:         nil,
				},
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-X-lun1",
					path:      "dm-1",
					err:       nil,
				},
				GetMultipathDiskReturn{
					pathParam: "/dev/disk/by-path/ip1-iscsi-Y-lun2",
					path:      "dm-1",
					err:       nil,
				},
			},

			expErr:      nil,
			expDMdevice: "dm-1",
		},

		{
			name:             "Should fail when WaitForPathToExist return error with the first array iqn, and found no sd device with the second array iqn",
			arrayIdentifiers: []string{"X", "Y"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: nil,
					exists:      false,
					err:         fmt.Errorf("error"),
				},
				WaitForPathToExistReturn{
					devicePaths: nil,
					exists:      false,
					err:         nil,
				},
			},

			expErr:      fmt.Errorf("error,Couldn't find multipath device for volumeID [volIdNotRelevant] lunID [0] from array [[Y]]. Please check the host connectivity to the storage."),
			expDMdevice: "",
		},
	}

	for _, tc := range testCasesIscsi {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_helper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
			fake_mutex := &sync.Mutex{}
			lunId := 0

			var mcalls []*gomock.Call
			for index, r := range tc.waitForPathToExistReturns {
				path := strings.Join([]string{"/dev/disk/by-path/ip*", "iscsi", tc.arrayIdentifiers[index], "lun", strconv.Itoa(lunId)}, "-")
				call := fake_helper.EXPECT().WaitForPathToExist(path, 5, 1).Return(
					r.devicePaths,
					r.exists,
					r.err)
				mcalls = append(mcalls, call)
			}

			for _, r := range tc.getMultipathDiskReturns {
				call := fake_helper.EXPECT().GetMultipathDisk(r.pathParam).Return(r.path, r.err)
				mcalls = append(mcalls, call)
			}
			gomock.InOrder(mcalls...)

			o := NewOsDeviceConnectivityHelperScsiGenericForTest(fake_executer, fake_helper, fake_mutex)
			DMdevice, err := o.GetMpathDevice("volIdNotRelevant", lunId, tc.arrayIdentifiers, "iscsi")
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

	testCasesFc := []struct {
		name             string
		arrayIdentifiers []string

		expErrType                reflect.Type
		expErr                    error
		expDMdevice               string
		waitForPathToExistReturns []WaitForPathToExistReturn
		getMultipathDiskReturns   []GetMultipathDiskReturn
	}{
		{
			name:             "Should fail when WaitForPathToExist not found any sd device",
			arrayIdentifiers: []string{"x"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: nil,
					exists:      false,
					err:         nil,
				},
			},

			expErr:      fmt.Errorf("Couldn't find multipath device for volumeID [volIdNotRelevant] lunID [0] from array [[0xx]]. Please check the host connectivity to the storage."),
			expDMdevice: "",
		},

		{
			name:             "Should fail when WaitForPathToExist fail for some reason",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: nil,
					exists:      false,
					err:         fmt.Errorf("error"),
				},
			},

			expErr:      fmt.Errorf("error"),
			expDMdevice: "",
		},

		{
			name:             "Should fail when GetMultipathDisk fail for some reason",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: []string{getFcPath("-fc-ID1-lun-1")},
					exists:      true,
					err:         nil,
				},
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: getFcPath("-fc-ID1-lun-1"),
					path:      "",
					err:       fmt.Errorf("error"),
				},
			},

			expErr:      fmt.Errorf("error"),
			expDMdevice: "",
		},

		{
			name:             "Should fail when GetMultipathDisk provide 2 different dms that apply to the same lun (bas multipathing case)",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: []string{getFcPath("-fc-ID1-lun1"), getFcPath("-fc-ID1-lun1___2")},
					exists:      true,
					err:         nil,
				},
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: getFcPath("-fc-ID1-lun1"),
					path:      "dm-1",
					err:       nil,
				},
				GetMultipathDiskReturn{
					pathParam: getFcPath("-fc-ID1-lun1___2"),
					path:      "dm-2", // The main point, look like multipath crazy and give to the same vol but different path a different md device, which is wrong case - so we check it.
					err:       nil,
				},
			},

			expErrType:  reflect.TypeOf(&device_connectivity.MultipleDmDevicesError{}),
			expDMdevice: "",
		},

		{
			name:             "Should succeed to GetMpathDevice - good path",
			arrayIdentifiers: []string{"X"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: []string{getFcPath("-fc-ID1-lun1"), getFcPath("-fc-ID1-lun1___2")},
					exists:      true,
					err:         nil,
				},
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: getFcPath("-fc-ID1-lun1"),
					path:      "dm-1",
					err:       nil,
				},
				GetMultipathDiskReturn{
					pathParam: getFcPath("-fc-ID1-lun1___2"),
					path:      "dm-1", // the same because there are 2 paths to the storage, so we should find 2 sd devices that point to the same dm device
					err:       nil,
				},
			},

			expErr:      nil,
			expDMdevice: "dm-1",
		},

		{
			name:             "Should succeed to GetMpathDevice with one more iqns",
			arrayIdentifiers: []string{"X", "Y"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: []string{getFcPath("-fc-0xX-lun1")},
					exists:      true,
					err:         nil,
				},
				WaitForPathToExistReturn{
					devicePaths: []string{getFcPath("-fc-0xY-lun2")},
					exists:      true,
					err:         nil,
				},
			},
			getMultipathDiskReturns: []GetMultipathDiskReturn{
				GetMultipathDiskReturn{
					pathParam: getFcPath("-fc-0xX-lun1"),
					path:      "dm-1",
					err:       nil,
				},
				GetMultipathDiskReturn{
					pathParam: getFcPath("-fc-0xY-lun2"),
					path:      "dm-1",
					err:       nil,
				},
			},

			expErr:      nil,
			expDMdevice: "dm-1",
		},

		{
			name:             "Should fail when WaitForPathToExist return error with the first array wwn, and found no sd device with the second array wwn",
			arrayIdentifiers: []string{"x", "y"},
			waitForPathToExistReturns: []WaitForPathToExistReturn{
				WaitForPathToExistReturn{
					devicePaths: nil,
					exists:      false,
					err:         fmt.Errorf("error"),
				},
				WaitForPathToExistReturn{
					devicePaths: nil,
					exists:      false,
					err:         nil,
				},
			},

			expErr:      fmt.Errorf("error,Couldn't find multipath device for volumeID [volIdNotRelevant] lunID [0] from array [[0xy]]. Please check the host connectivity to the storage."),
			expDMdevice: "",
		},
	}
	for _, tc := range testCasesFc {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_helper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
			fake_mutex := &sync.Mutex{}
			lunId := 0

			var mcalls []*gomock.Call
			for index, r := range tc.waitForPathToExistReturns {
				array_inititor := "0x" + strings.ToLower(string(tc.arrayIdentifiers[index]))
				path := strings.Join([]string{getFcPath("*"), "fc", array_inititor, "lun", strconv.Itoa(lunId)}, "-")
				call := fake_helper.EXPECT().WaitForPathToExist(path, 5, 1).Return(
					r.devicePaths,
					r.exists,
					r.err)
				mcalls = append(mcalls, call)
			}

			for _, r := range tc.getMultipathDiskReturns {
				call := fake_helper.EXPECT().GetMultipathDisk(r.pathParam).Return(r.path, r.err)
				mcalls = append(mcalls, call)
			}
			gomock.InOrder(mcalls...)

			o := NewOsDeviceConnectivityHelperScsiGenericForTest(fake_executer, fake_helper, fake_mutex)
			DMdevice, err := o.GetMpathDevice("volIdNotRelevant", lunId, tc.arrayIdentifiers, "fc")
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
			devicePath := []string{getFcPath("-fc-ARRAYWWN-lun-LUNID"), "/dev/disk/by-path/ip-ARRAYIP-iscsi-ARRAYIQN-lun-LUNID"}
			for _, dp := range devicePath {
				fake_executer.EXPECT().FilepathGlob(dp).Return(tc.fpaths, tc.globReturnErr)
				helperGeneric := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fake_executer)
				_, found, err := helperGeneric.WaitForPathToExist(dp, 1, 1)
				if err != nil {
					if err.Error() != tc.expErr.Error() {
						t.Fatalf("Expected error code %s, got %s", tc.expErr, err.Error())
					}
				}
				if found != tc.expFound {
					t.Fatalf("Expected found boolean code %t, got %t", tc.expFound, found)
				}
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

		expErr     error
		expPath    string
		expErrType reflect.Type
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

			expErrType: reflect.TypeOf(&device_connectivity.MultipleDeviceNotFoundError{}),
			expPath:    "",
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
			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			path := []string{getFcPath("-fc-wwn:5"), "/dev/disk/by-path/ip-ARRAYIP-iscsi-ARRAYIQN-lun-LUNID"}

			for _, dp := range path {
				fake_executer.EXPECT().OsReadlink(dp).Return(tc.osReadLinkReturnPath, tc.osReadLinkReturnExc)

				if len(tc.globReturns) == 1 {
					fake_executer.EXPECT().FilepathGlob("/sys/block/dm-*").Return(tc.globReturns[0].globReturnfpaths, tc.globReturns[0].globReturnErr)
				} else if len(tc.globReturns) == 2 {
					first := fake_executer.EXPECT().FilepathGlob("/sys/block/dm-*").Return(tc.globReturns[0].globReturnfpaths, tc.globReturns[0].globReturnErr)
					second := fake_executer.EXPECT().FilepathGlob("/sys/block/dm-4/slaves/*").Return(tc.globReturns[1].globReturnfpaths, tc.globReturns[1].globReturnErr)

					gomock.InOrder(first, second)
				}
				helperGeneric := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fake_executer)

				returnPath, err := helperGeneric.GetMultipathDisk(dp)
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
				if returnPath != tc.expPath {
					t.Fatalf("Expected found multipath device %s, got %s", tc.expPath, returnPath)
				}
			}
		})
	}
}

type ioutilReadFileReturn struct {
	ReadFileParam string // The param that the IoutilReadDir recive on each call.
	data          []byte
	err           error
}

func TestGetHostsIdByArrayIdentifier(t *testing.T) {
	testCasesIscsi := []struct {
		name                  string
		ioutilReadFileReturns []ioutilReadFileReturn
		arrayIdentifier       string

		expErrType        reflect.Type
		expErr            error
		expHostList       []int
		globReturnMatches []string
		globReturnErr     error
	}{
		{
			name:              "Should fail when FilepathGlob return error",
			arrayIdentifier:   "iqn.1986-03.com.ibm:2145.v7k194.node2",
			globReturnMatches: nil,
			globReturnErr:     fmt.Errorf("error"),
			expErr:            fmt.Errorf("error"),
			expHostList:       nil,
		},
		{
			name:              "Should fail when FilepathGlob return without any hosts target files at all",
			arrayIdentifier:   "iqn.1986-03.com.ibm:2145.v7k194.node2",
			globReturnMatches: nil,
			globReturnErr:     nil,

			expErrType:  reflect.TypeOf(&device_connectivity.ConnectivityIdentifierStorageTargetNotFoundError{}),
			expHostList: nil,
		},
		{
			name: "Should fail when array IQN was not found in target files at all",
			ioutilReadFileReturns: []ioutilReadFileReturn{
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/iscsi_host/host1/device/session1/iscsi_session/session1/targetname",
					data:          []byte("fakeIQN_OTHER"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/iscsi_host/host2/device/session2/iscsi_session/session2/targetname",
					data:          []byte("fakeIQN_OTHER"),
					err:           nil,
				},
			},
			arrayIdentifier: "iqn.1986-03.com.ibm:2145.v7k194.node2",
			globReturnMatches: []string{
				"/sys/class/iscsi_host/host1/device/session1/iscsi_session/session1/targetname",
				"/sys/class/iscsi_host/host2/device/session2/iscsi_session/session2/targetname",
			},
			globReturnErr: nil,

			expErrType:  reflect.TypeOf(&device_connectivity.ConnectivityIdentifierStorageTargetNotFoundError{}),
			expHostList: nil,
		},
		{
			name: "Should fail when array IQN found but hostX where X is not int",
			ioutilReadFileReturns: []ioutilReadFileReturn{
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/iscsi_host/hostX/device/session1/iscsi_session/session1/targetname",
					data:          []byte("fakeIQN"),
					err:           nil,
				},
			},
			arrayIdentifier: "iqn.1986-03.com.ibm:2145.v7k194.node2",

			globReturnMatches: []string{
				"/sys/class/iscsi_host/hostX/device/session1/iscsi_session/session1/targetname",
			},
			globReturnErr: nil,

			expErrType:  reflect.TypeOf(&device_connectivity.ConnectivityIdentifierStorageTargetNotFoundError{}),
			expHostList: nil,
		},

		{
			name: "Should succeed to find host1 and host2 for the array IQN (while host3 is not from this IQN and also host666 fail ignore)",
			ioutilReadFileReturns: []ioutilReadFileReturn{
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/iscsi_host/host1/device/session1/iscsi_session/session1/targetname",
					data:          []byte("iqn.1986-03.com.ibm:2145.v7k194.node2"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/iscsi_host/host2/device/session1/iscsi_session/session1/targetname",
					data:          []byte("iqn.1986-03.com.ibm:2145.v7k194.node2"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/iscsi_host/host3/device/session1/iscsi_session/session1/targetname",
					data:          []byte("fakeIQN_OTHER"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/iscsi_host/host666/device/session1/iscsi_session/session1/targetname",
					data:          nil,
					err:           fmt.Errorf("error"),
				},
			},
			arrayIdentifier: "iqn.1986-03.com.ibm:2145.v7k194.node2",

			globReturnMatches: []string{
				"/sys/class/iscsi_host/host1/device/session1/iscsi_session/session1/targetname",
				"/sys/class/iscsi_host/host2/device/session1/iscsi_session/session1/targetname",
				"/sys/class/iscsi_host/host3/device/session1/iscsi_session/session1/targetname",
				"/sys/class/iscsi_host/host666/device/session1/iscsi_session/session1/targetname",
			},
			globReturnErr: nil,

			expErrType:  nil,
			expHostList: []int{1, 2},
		},
	}

	for _, tc := range testCasesIscsi {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)

			fake_executer.EXPECT().FilepathGlob(device_connectivity.IscsiHostRexExPath).Return(tc.globReturnMatches, tc.globReturnErr)

			var mcalls []*gomock.Call
			for _, r := range tc.ioutilReadFileReturns {
				call := fake_executer.EXPECT().IoutilReadFile(r.ReadFileParam).Return(r.data, r.err)
				mcalls = append(mcalls, call)
			}
			gomock.InOrder(mcalls...)

			helperGeneric := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fake_executer)

			returnHostList, err := helperGeneric.GetHostsIdByArrayIdentifier(tc.arrayIdentifier)
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

			if len(tc.expHostList) == 0 && len(returnHostList) == 0 {
				return
			} else if !reflect.DeepEqual(returnHostList, tc.expHostList) {
				t.Fatalf("Expected found hosts dirs %v, got %v", tc.expHostList, returnHostList)
			}

		})
	}

	testCasesFc := []struct {
		name                  string
		ioutilReadFileReturns []ioutilReadFileReturn
		arrayIdentifier       string

		expErrType        reflect.Type
		expErr            error
		expHostList       []int
		globReturnMatches []string
		globReturnErr     error
	}{
		{
			name:              "Should fail when FilepathGlob return error",
			arrayIdentifier:   "fakeWWN",
			globReturnMatches: nil,
			globReturnErr:     fmt.Errorf("error"),
			expErr:            fmt.Errorf("error"),
			expHostList:       nil,
		},
		{
			name:              "Should fail when FilepathGlob return without any hosts target files at all",
			arrayIdentifier:   "fakeWWN",
			globReturnMatches: nil,
			globReturnErr:     nil,

			expErrType:  reflect.TypeOf(&device_connectivity.ConnectivityIdentifierStorageTargetNotFoundError{}),
			expHostList: nil,
		},
		{
			name: "Should fail when all values are not match",
			ioutilReadFileReturns: []ioutilReadFileReturn{
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/fc_remote_ports/rport-3:0-0/port_name",
					data:          []byte("fakeWWN_other"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/fc_remote_ports/rport-4:0-0/port_name",
					data:          []byte("fakeWWN_other"),
					err:           nil,
				},
			},
			arrayIdentifier: "fakeWWN",
			globReturnMatches: []string{
				"/sys/class/fc_remote_ports/rport-3:0-0/port_name",
				"/sys/class/fc_remote_ports/rport-4:0-0/port_name",
			},
			globReturnErr: nil,

			expErrType:  reflect.TypeOf(&device_connectivity.ConnectivityIdentifierStorageTargetNotFoundError{}),
			expHostList: nil,
		},

		{
			name: "Should succeed to find host33 and host34(host 35 offline, hott36 return error)",
			ioutilReadFileReturns: []ioutilReadFileReturn{
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/fc_remote_ports/rport-33:0-0/port_name",
					data:          []byte("fakeWWN"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/fc_remote_ports/rport-34:0-0/port_name",
					data:          []byte("0xfakeWWN"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/fc_remote_ports/rport-35:0-0/port_name",
					data:          []byte("fakeWWN_other"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/fc_remote_ports/rport-36:0-0/port_name",
					data:          nil,
					err:           fmt.Errorf("error"),
				},
			},
			arrayIdentifier: "fakeWWN",

			globReturnMatches: []string{
				"/sys/class/fc_remote_ports/rport-33:0-0/port_name",
				"/sys/class/fc_remote_ports/rport-34:0-0/port_name",
				"/sys/class/fc_remote_ports/rport-35:0-0/port_name",
				"/sys/class/fc_remote_ports/rport-36:0-0/port_name",
			},
			globReturnErr: nil,

			expErrType:  nil,
			expHostList: []int{33, 34},
		},

		{
			name: "Should succeed to find host5 and host6",
			ioutilReadFileReturns: []ioutilReadFileReturn{
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/fc_remote_ports/rport-5:0-0/port_name",
					data:          []byte("0xfakeWWN"),
					err:           nil,
				},
				ioutilReadFileReturn{
					ReadFileParam: "/sys/class/fc_remote_ports/rport-6:0-0/port_name",
					data:          []byte("fakeWWN"),
					err:           nil,
				},
			},
			arrayIdentifier: "fakeWWN",

			globReturnMatches: []string{
				"/sys/class/fc_remote_ports/rport-5:0-0/port_name",
				"/sys/class/fc_remote_ports/rport-6:0-0/port_name",
			},
			globReturnErr: nil,

			expErrType:  nil,
			expHostList: []int{5, 6},
		},
	}

	for _, tc := range testCasesFc {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)

			fake_executer.EXPECT().FilepathGlob(device_connectivity.FC_HOST_SYSFS_PATH).Return(tc.globReturnMatches, tc.globReturnErr)

			var mcalls []*gomock.Call
			for _, r := range tc.ioutilReadFileReturns {
				call := fake_executer.EXPECT().IoutilReadFile(r.ReadFileParam).Return(r.data, r.err)
				mcalls = append(mcalls, call)
			}
			gomock.InOrder(mcalls...)

			helperGeneric := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fake_executer)

			returnHostList, err := helperGeneric.GetHostsIdByArrayIdentifier(tc.arrayIdentifier)
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

			if len(tc.expHostList) == 0 && len(returnHostList) == 0 {
				return
			} else if !reflect.DeepEqual(returnHostList, tc.expHostList) {
				t.Fatalf("Expected found hosts dirs %v, got %v", tc.expHostList, returnHostList)
			}

		})
	}
}

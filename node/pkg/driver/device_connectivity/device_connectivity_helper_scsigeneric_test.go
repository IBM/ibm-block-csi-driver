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
	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

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

func NewOsDeviceConnectivityHelperGenericForTest(
	executer executer.ExecuterInterface,
	helper device_connectivity.GetDmsPathHelperInterface,

) device_connectivity.OsDeviceConnectivityHelperInterface {
	return &device_connectivity.OsDeviceConnectivityHelperGeneric{
		Executer: executer,
		Helper:   helper,
	}
}

func areStringsEqualAsSet(str1, str2 string) bool {
	sl1 := strings.Split(str1, device_connectivity.GetMpahDevErrorsSep)
	sl2 := strings.Split(str2, device_connectivity.GetMpahDevErrorsSep)
	sort.Strings(sl1)
	sort.Strings(sl2)
	return reflect.DeepEqual(sl1, sl2)
}

type GetDmsPathReturn struct {
	dmPath string
	err    error
}

type GetWwnByScsiInqReturn struct {
	wwn string
	err error
}

type ReloadMultipathReturn struct {
	err error
}

func TestGetMpathDevice(t *testing.T) {
	testCases := []struct {
		name                  string
		expErrType            reflect.Type
		expErr                error
		expDMPath             string
		getDmsPathReturn      []GetDmsPathReturn
		getWwnByScsiInqReturn []GetWwnByScsiInqReturn
		reloadMultipathReturn []ReloadMultipathReturn
	}{
		{
			name: "Should fail when WaitForDmToExist did not find any dm device",
			getDmsPathReturn: []GetDmsPathReturn{
				GetDmsPathReturn{
					dmPath: "",
					err:    nil,
				},
				GetDmsPathReturn{
					dmPath: "",
					err:    nil,
				},
			},

			reloadMultipathReturn: []ReloadMultipathReturn{
				ReloadMultipathReturn{
					err: nil,
				},
			},

			expErrType: reflect.TypeOf(&device_connectivity.MultipathDeviceNotFoundForVolumeError{}),
			expDMPath:  "",
		},

		{
			name: "Should fail when WaitForDmToExist find more than 1 dm for volume",
			getDmsPathReturn: []GetDmsPathReturn{
				GetDmsPathReturn{
					dmPath: "",
					err:    nil,
				},
				GetDmsPathReturn{
					dmPath: "",
					err:    &device_connectivity.MultipleDmDevicesError{"", nil},
				},
			},

			reloadMultipathReturn: []ReloadMultipathReturn{
				ReloadMultipathReturn{
					err: nil,
				},
			},

			expErrType: reflect.TypeOf(&device_connectivity.MultipleDmDevicesError{}),
			expDMPath:  "",
		},

		{
			name: "Should fail on dm validation via GetWwnByScsiInq",
			getDmsPathReturn: []GetDmsPathReturn{
				GetDmsPathReturn{
					dmPath: "",
					err:    nil,
				},
				GetDmsPathReturn{
					dmPath: "/dev/dm-1",
					err:    nil,
				},
			},

			reloadMultipathReturn: []ReloadMultipathReturn{
				ReloadMultipathReturn{
					err: nil,
				},
			},

			getWwnByScsiInqReturn: []GetWwnByScsiInqReturn{
				GetWwnByScsiInqReturn{
					wwn: "otheruuid",
					err: nil,
				},
			},

			expErrType: reflect.TypeOf(&device_connectivity.ErrorWrongDeviceFound{}),
			expDMPath:  "",
		},

		{
			name: "Should succeed to GetMpathDevice on first call to GetDmsPath",
			getDmsPathReturn: []GetDmsPathReturn{
				GetDmsPathReturn{
					dmPath: "/dev/dm-1",
					err:    nil,
				},
			},
			getWwnByScsiInqReturn: []GetWwnByScsiInqReturn{
				GetWwnByScsiInqReturn{
					wwn: "600fakevolumeuuid000000000111",
					err: nil,
				},
			},

			expErr:    nil,
			expDMPath: "/dev/dm-1",
		},

		{
			name: "Should succeed to GetMpathDevice on second call to GetDmsPath",
			getDmsPathReturn: []GetDmsPathReturn{
				GetDmsPathReturn{
					dmPath: "",
					err:    nil,
				},
				GetDmsPathReturn{
					dmPath: "/dev/dm-1",
					err:    nil,
				},
			},

			reloadMultipathReturn: []ReloadMultipathReturn{
				ReloadMultipathReturn{
					err: nil,
				},
			},

			getWwnByScsiInqReturn: []GetWwnByScsiInqReturn{
				GetWwnByScsiInqReturn{
					wwn: "600fakevolumeuuid000000000111",
					err: nil,
				},
			},

			expErr:    nil,
			expDMPath: "/dev/dm-1",
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_helper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
			fake_mutex := &sync.Mutex{}

			for _, r := range tc.getDmsPathReturn {
				fake_helper.EXPECT().GetDmsPath("600fakevolumeuuid000000000111").Return(
					r.dmPath,
					r.err)
			}

			for _, r := range tc.reloadMultipathReturn {
				fake_helper.EXPECT().ReloadMultipath().Return(
					r.err)
			}

			for _, r := range tc.getWwnByScsiInqReturn {
				fake_helper.EXPECT().GetWwnByScsiInq("/dev/dm-1").Return(
					r.wwn,
					r.err)
			}

			o := NewOsDeviceConnectivityHelperScsiGenericForTest(fake_executer, fake_helper, fake_mutex)
			DMPath, err := o.GetMpathDevice("Test:600FAKEVOLUMEUUID000000000111")
			if tc.expErr != nil || tc.expErrType != nil {
				if err == nil {
					t.Fatalf("Expected to fail with error, got success.")
				}
				if tc.expErrType != nil {
					if reflect.TypeOf(err) != tc.expErrType {
						t.Fatalf("Expected error type %v, got different error %v", tc.expErrType, reflect.TypeOf(err))
					}
				} else {
					if !areStringsEqualAsSet(err.Error(), tc.expErr.Error()) {
						t.Fatalf("Expected error %s, got %s", tc.expErr, err.Error())
					}
				}
			}

			if tc.expDMPath != DMPath {
				t.Fatalf("Expected found device mapper  %v, got %v", tc.expDMPath, DMPath)
			}

		})
	}

}

type WaitForDmToExistReturn struct {
	out string
	err error
}

func TestGetDmsPath(t *testing.T) {
	testCases := []struct {
		name                   string
		expErrType             reflect.Type
		expErr                 error
		expDMPath              string
		waitForDmToExistReturn []WaitForDmToExistReturn
	}{
		{
			name: "Should fail when WaitForDmToExist did not find any dm device",
			waitForDmToExistReturn: []WaitForDmToExistReturn{
				WaitForDmToExistReturn{
					out: "",
					err: nil,
				},
			},

			expErrType: reflect.TypeOf(&device_connectivity.MultipathDeviceNotFoundForVolumeError{}),
			expDMPath:  "",
		},

		{
			name: "Should fail when WaitForDmToExist found more than 1 dm for volume",
			waitForDmToExistReturn: []WaitForDmToExistReturn{
				WaitForDmToExistReturn{
					out: "dm-1,600fakevolumeuuid000000000111\ndm-2,otheruuid\ndm-3,600fakevolumeuuid000000000111",
					err: nil,
				},
			},

			expErrType: reflect.TypeOf(&device_connectivity.MultipleDmDevicesError{}),
			expDMPath:  "",
		},

		{
			name: "Should succeed to GetDmPath with space in start of input",
			waitForDmToExistReturn: []WaitForDmToExistReturn{
				WaitForDmToExistReturn{
					out: " dm-1,600fakevolumeuuid000000000111",
					err: nil,
				},
			},

			expErr:    nil,
			expDMPath: "/dev/dm-1",
		},

		{
			name: "Should succeed to GetDmPath",
			waitForDmToExistReturn: []WaitForDmToExistReturn{
				WaitForDmToExistReturn{
					out: "dm-1,600fakevolumeuuid000000000111\ndm-2,otheruuid\ndm-3,otheruuid2",
					err: nil,
				},
			},

			expErr:    nil,
			expDMPath: "/dev/dm-1",
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_helper := mocks.NewMockGetDmsPathHelperInterface(mockCtrl)

			for _, r := range tc.waitForDmToExistReturn {
				fake_helper.EXPECT().WaitForDmToExist("600fakevolumeuuid000000000111", 5, 1).Return(r.out, r.err)
			}

			helperGeneric := NewOsDeviceConnectivityHelperGenericForTest(fake_executer, fake_helper)
			dmPath, err := helperGeneric.GetDmsPath("600FAKEVOLUMEUUID000000000111")
			if tc.expErr != nil || tc.expErrType != nil {
				if err == nil {
					t.Fatalf("Expected to fail with error, got success.")
				}
				if tc.expErrType != nil {
					if reflect.TypeOf(err) != tc.expErrType {
						t.Fatalf("Expected error type %v, got different error %v", tc.expErrType, reflect.TypeOf(err))
					}
				} else {
					if !areStringsEqualAsSet(err.Error(), tc.expErr.Error()) {
						t.Fatalf("Expected error %s, got %s", tc.expErr, err.Error())
					}
				}
			}

			if tc.expDMPath != dmPath {
				t.Fatalf("Expected found device mapper  %v, got %v", tc.expDMPath, dmPath)
			}

		})
	}

}

func TestHelperWaitForDmToExist(t *testing.T) {
	testCases := []struct {
		name         string
		devices      string
		expErr       error
		cmdReturnErr error
	}{
		{
			name:         "Should fail when cmd return error",
			devices:      "",
			cmdReturnErr: fmt.Errorf("error"),
			expErr:       fmt.Errorf("error"),
		},
		{
			name:         "Should return empty string when cmd succeed but with no dm.uuid pairs",
			devices:      "",
			cmdReturnErr: nil,
			expErr:       nil,
		},
		{
			name:         "Should succeed",
			devices:      "dm-1,volumeUuid\ndm-2,otherUuid",
			cmdReturnErr: nil,
			expErr:       nil,
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			volumeUuid := "volumeUuid"
			args := []string{"show", "maps", "raw", "format", "\"", "%d,%w", "\""}
			fake_executer.EXPECT().ExecuteWithTimeout(device_connectivity.TimeOutMultipathdCmd, "multipathd", args).Return([]byte(tc.devices), tc.cmdReturnErr)
			helperGeneric := device_connectivity.NewGetDmsPathHelperGeneric(fake_executer)
			devices, err := helperGeneric.WaitForDmToExist(volumeUuid, 1, 1)
			if err != nil {
				if err.Error() != tc.expErr.Error() {
					t.Fatalf("Expected error code %s, got %s", tc.expErr, err.Error())
				}
			}
			if tc.devices != devices {
				t.Fatalf("Expected found device mapper  %v, got %v", tc.devices, devices)
			}

		})
	}
}

func TestHelperGetWwnByScsiInq(t *testing.T) {
	testCases := []struct {
		name            string
		cmdReturn       []byte
		expErr          error
		wwn             string
		cmdReturnErr    error
		sgInqExecutable error
		expErrType      reflect.Type
	}{
		{
			name:            "Should fail when sg_Inq is not executable",
			cmdReturn:       []byte(""),
			wwn:             "",
			cmdReturnErr:    nil,
			expErr:          fmt.Errorf("error"),
			sgInqExecutable: fmt.Errorf("error"),
		},
		{
			name:            "Should fail when cmd return error",
			cmdReturn:       []byte(""),
			wwn:             "",
			cmdReturnErr:    fmt.Errorf("error"),
			expErr:          fmt.Errorf("error"),
			sgInqExecutable: nil,
		},
		{
			name:            "Should return error when wwn line not matching the expected pattern",
			cmdReturn:       []byte("Vendor Specific Identifier Extension: 0xcea5f6\n\t\t\t  [600fakevolumeuuid000000000111]"),
			wwn:             "",
			cmdReturnErr:    nil,
			expErrType:      reflect.TypeOf(&device_connectivity.ErrorNoRegexWwnMatchInScsiInq{}),
			sgInqExecutable: nil,
		},
		{
			name:            "Should fail when no device found",
			cmdReturn:       []byte(""),
			wwn:             "",
			cmdReturnErr:    nil,
			expErrType:      reflect.TypeOf(&device_connectivity.MultipathDeviceNotFoundForVolumeError{}),
			sgInqExecutable: nil,
		},
		{
			name:            "Should succeed",
			cmdReturn:       []byte("Vendor Specific Identifier Extension: 0xcea5f6\n\t\t\t  [0x600fakevolumeuuid000000000111]"),
			wwn:             "600FAKEVOLUMEUUID000000000111",
			cmdReturnErr:    nil,
			expErr:          nil,
			sgInqExecutable: nil,
		},
	}
	sgInqCmd := "sg_inq"
	device := "/dev/dm-1"
	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			args := []string{"-p", "0x83", device}
			fake_executer.EXPECT().IsExecutable(sgInqCmd).Return(tc.sgInqExecutable)

			if tc.sgInqExecutable == nil {
				fake_executer.EXPECT().ExecuteWithTimeout(3000, sgInqCmd, args).Return(tc.cmdReturn, tc.cmdReturnErr)
			}

			helperGeneric := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fake_executer)
			wwn, err := helperGeneric.GetWwnByScsiInq(device)
			if tc.expErr != nil || tc.expErrType != nil {
				if err == nil {
					t.Fatalf("Expected to fail with error, got success.")
				}
				if tc.expErrType != nil {
					if reflect.TypeOf(err) != tc.expErrType {
						t.Fatalf("Expected error type %v, got different error %v", tc.expErrType, reflect.TypeOf(err))
					}
				} else {
					if !areStringsEqualAsSet(err.Error(), tc.expErr.Error()) {
						t.Fatalf("Expected error %s, got %s", tc.expErr, err.Error())
					}
				}
			}
			if strings.ToLower(tc.wwn) != wwn {
				t.Fatalf("Expected wwn  %v, got %v", wwn, tc.wwn)
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

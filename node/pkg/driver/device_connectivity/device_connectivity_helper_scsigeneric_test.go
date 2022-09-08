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
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

var (
	volumeUuid  = "6oui000vendorsi0vendorsie0000000"
	volumeNguid = "vendorsie0000000oui0000vendorsi0"
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

type GetVolumeIdVariationsReturn struct {
	err error
}

func TestGetMpathDevice(t *testing.T) {
	testCases := []struct {
		name                        string
		expErrType                  reflect.Type
		expErr                      error
		expDMPath                   string
		getDmsPathReturn            []GetDmsPathReturn
		getWwnByScsiInqReturn       []GetWwnByScsiInqReturn
		reloadMultipathReturn       []ReloadMultipathReturn
		getVolumeIdVariationsReturn []GetVolumeIdVariationsReturn
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
			name: "Should fail when WaitForDmToExist found more than 1 dm for volume",
			getDmsPathReturn: []GetDmsPathReturn{
				GetDmsPathReturn{
					dmPath: "",
					err:    nil,
				},
				GetDmsPathReturn{
					dmPath: "",
					err:    &device_connectivity.MultipleDmFieldValuesError{"", nil},
				},
			},

			reloadMultipathReturn: []ReloadMultipathReturn{
				ReloadMultipathReturn{
					err: nil,
				},
			},

			expErrType: reflect.TypeOf(&device_connectivity.MultipleDmFieldValuesError{}),
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
					wwn: volumeUuid,
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
					wwn: volumeUuid,
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

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			fake_helper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
			fake_mutex := &sync.Mutex{}
			volumeIdVariations := []string{volumeUuid, volumeNguid}

			fake_helper.EXPECT().GetVolumeIdVariations(volumeUuid).Return(volumeIdVariations)

			for _, r := range tc.getDmsPathReturn {
				fake_helper.EXPECT().GetDmsPath(volumeIdVariations).Return(
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

			o := NewOsDeviceConnectivityHelperScsiGenericForTest(fakeExecuter, fake_helper, fake_mutex)
			DMPath, err := o.GetMpathDevice(volumeUuid)
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
						t.Fatalf("Expected error %s, got %s", tc.expErr, err.Error())
					}
				}
			}

			if tc.expDMPath != DMPath {
				logger.Error(err)
				t.Fatalf("Expected found device mapper  %v, got %v", tc.expDMPath, DMPath)
			}

		})
	}

}

type WaitForDmToExistReturn struct {
	out string
	err error
}

type ExtractDmFieldValuesReturn struct {
	mpathdOutput  string
	dmFieldValues map[string]bool
}

type GetFullDmPathReturn struct {
	dmFieldValues map[string]bool
	dmPath        string
	err           error
}

func TestGetDmsPath(t *testing.T) {
	testCases := []struct {
		name                       string
		expErrType                 reflect.Type
		expErr                     error
		expDMPath                  string
		waitForDmToExistReturn     []WaitForDmToExistReturn
		extractDmFieldValuesReturn []ExtractDmFieldValuesReturn
		getFullDmPathReturn        []GetFullDmPathReturn
	}{
		{
			name: "Should fail when WaitForDmToExist did not find any dm device",
			waitForDmToExistReturn: []WaitForDmToExistReturn{
				WaitForDmToExistReturn{
					out: "",
					err: &device_connectivity.MultipathDeviceNotFoundForVolumeError{""},
				},
			},

			expErrType: reflect.TypeOf(&device_connectivity.MultipathDeviceNotFoundForVolumeError{}),
			expDMPath:  "",
		},

		{
			name: "Should fail when WaitForDmToExist found more than 1 dm for volume",
			waitForDmToExistReturn: []WaitForDmToExistReturn{
				WaitForDmToExistReturn{
					out: fmt.Sprintf("dm-1,%s\ndm-2,%s\ndm-3,%s", volumeUuid, "otheruuid", volumeUuid),
					err: nil,
				},
			},

			extractDmFieldValuesReturn: []ExtractDmFieldValuesReturn{
				ExtractDmFieldValuesReturn{
					mpathdOutput: fmt.Sprintf("dm-1,%s\ndm-2,%s\ndm-3,%s", volumeUuid, "otheruuid", volumeUuid),
					dmFieldValues: map[string]bool{
						"dm-1": true,
						"dm-2": true,
						"dm-3": true,
					},
				},
			},

			getFullDmPathReturn: []GetFullDmPathReturn{
				GetFullDmPathReturn{
					dmFieldValues: map[string]bool{
						"dm-1": true,
						"dm-2": true,
						"dm-3": true,
					},
					dmPath: "",
					err:    &device_connectivity.MultipleDmFieldValuesError{"", map[string]bool{}},
				},
			},

			expErrType: reflect.TypeOf(&device_connectivity.MultipleDmFieldValuesError{}),
			expDMPath:  "",
		},

		{
			name: "Should succeed to GetDmPath with space in start of input",
			waitForDmToExistReturn: []WaitForDmToExistReturn{
				WaitForDmToExistReturn{
					out: fmt.Sprintf(" dm-1,%s", volumeUuid),
					err: nil,
				},
			},

			extractDmFieldValuesReturn: []ExtractDmFieldValuesReturn{
				ExtractDmFieldValuesReturn{
					mpathdOutput: fmt.Sprintf(" dm-1,%s", volumeUuid),
					dmFieldValues: map[string]bool{
						"dm-1": true,
					},
				},
			},

			getFullDmPathReturn: []GetFullDmPathReturn{
				GetFullDmPathReturn{
					dmFieldValues: map[string]bool{
						"dm-1": true,
					},
					dmPath: "/dev/dm-1",
					err:    nil,
				},
			},

			expErr:    nil,
			expDMPath: "/dev/dm-1",
		},

		{
			name: "Should succeed to GetDmPath",
			waitForDmToExistReturn: []WaitForDmToExistReturn{
				WaitForDmToExistReturn{
					out: fmt.Sprintf("dm-1,%s", volumeUuid),
					err: nil,
				},
			},

			extractDmFieldValuesReturn: []ExtractDmFieldValuesReturn{
				ExtractDmFieldValuesReturn{
					mpathdOutput: fmt.Sprintf("dm-1,%s", volumeUuid),
					dmFieldValues: map[string]bool{
						"dm-1": true,
					},
				},
			},

			getFullDmPathReturn: []GetFullDmPathReturn{
				GetFullDmPathReturn{
					dmFieldValues: map[string]bool{
						"dm-1": true,
					},
					dmPath: "/dev/dm-1",
					err:    nil,
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
			volumeIdVariations := []string{volumeUuid, volumeNguid}

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			fake_helper := mocks.NewMockGetDmsPathHelperInterface(mockCtrl)

			for _, r := range tc.waitForDmToExistReturn {
				fake_helper.EXPECT().WaitForDmToExist(volumeIdVariations, device_connectivity.WaitForMpathRetries,
					device_connectivity.WaitForMpathWaitIntervalSec, device_connectivity.MultipathdWildcardsVolumeIdAndMpath).Return(r.out, r.err)
			}

			for _, r := range tc.extractDmFieldValuesReturn {
				fake_helper.EXPECT().ExtractDmFieldValues(volumeIdVariations, r.mpathdOutput).Return(r.dmFieldValues)
			}

			for _, r := range tc.getFullDmPathReturn {
				fake_helper.EXPECT().GetFullDmPath(r.dmFieldValues, volumeUuid).Return(r.dmPath, r.err)
			}

			helperGeneric := NewOsDeviceConnectivityHelperGenericForTest(fakeExecuter, fake_helper)
			dmPath, err := helperGeneric.GetDmsPath(volumeIdVariations)
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
			name:         "Should return not found error cmd succeed but with no dm.uuid pairs",
			devices:      "",
			cmdReturnErr: nil,
			expErr:       &device_connectivity.MultipathDeviceNotFoundForVolumeError{VolumeId: volumeUuid},
		},
		{
			name:         "Should succeed",
			devices:      fmt.Sprintf("dm-1,%s\ndm-2,%s", volumeUuid, "otherUuid"),
			cmdReturnErr: nil,
			expErr:       nil,
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			volumeIdVariations := []string{volumeUuid, volumeNguid}

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			args := []string{"show", "maps", "raw", "format", "\"", "%w,%d", "\""}
			fakeExecuter.EXPECT().ExecuteWithTimeout(device_connectivity.TimeOutMultipathdCmd,
				"multipathd", args).Return([]byte(tc.devices), tc.cmdReturnErr)
			helperGeneric := device_connectivity.NewGetDmsPathHelperGeneric(fakeExecuter)
			devices, err := helperGeneric.WaitForDmToExist(volumeIdVariations, 1, 1,
				device_connectivity.MultipathdWildcardsVolumeIdAndMpath)
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
			cmdReturn:       []byte(fmt.Sprintf("Vendor Specific Identifier Extension: 0xcea5f6\n\t\t\t  [%s]", volumeUuid)),
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
			cmdReturn:       []byte(fmt.Sprintf("Vendor Specific Identifier Extension: 0xcea5f6\n\t\t\t  [0x%s]", volumeUuid)),
			wwn:             volumeUuid,
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

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			args := []string{"-p", "0x83", device}
			fakeExecuter.EXPECT().IsExecutable(sgInqCmd).Return(tc.sgInqExecutable)

			if tc.sgInqExecutable == nil {
				fakeExecuter.EXPECT().ExecuteWithTimeout(device_connectivity.TimeOutSgInqCmd, sgInqCmd, args).Return(tc.cmdReturn, tc.cmdReturnErr)
			}

			helperGeneric := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fakeExecuter)
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
					if err.Error() != tc.expErr.Error() {
						t.Fatalf("Expected error %s, got %s", tc.expErr, err.Error())
					}
				}
			}
			if strings.ToLower(tc.wwn) != strings.ToLower(wwn) {
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

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)

			fakeExecuter.EXPECT().FilepathGlob(device_connectivity.IscsiHostRexExPath).Return(tc.globReturnMatches, tc.globReturnErr)

			var mcalls []*gomock.Call
			for _, r := range tc.ioutilReadFileReturns {
				call := fakeExecuter.EXPECT().IoutilReadFile(r.ReadFileParam).Return(r.data, r.err)
				mcalls = append(mcalls, call)
			}
			gomock.InOrder(mcalls...)

			helperGeneric := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fakeExecuter)

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

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)

			fakeExecuter.EXPECT().FilepathGlob(device_connectivity.FcHostSysfsPath).Return(tc.globReturnMatches, tc.globReturnErr)

			var mcalls []*gomock.Call
			for _, r := range tc.ioutilReadFileReturns {
				call := fakeExecuter.EXPECT().IoutilReadFile(r.ReadFileParam).Return(r.data, r.err)
				mcalls = append(mcalls, call)
			}
			gomock.InOrder(mcalls...)

			helperGeneric := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fakeExecuter)

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

func TestIsVolumePathMatchesVolumeId(t *testing.T) {
	testCases := []struct {
		name                        string
		volumeUuid                  string
		volumePath                  string
		mpathdOutput                string
		mpathdOutputErr             error
		mpathDeviceName             string
		mpathDeviceNameErr          error
		volumeIdByVolumePath        string
		matchingVolumeIdErr         error
		isVolumePathMatchesVolumeId bool
	}{
		{
			name:                        "success",
			volumeUuid:                  "volumeUuid",
			volumePath:                  "/path",
			mpathdOutput:                "fakeMpathDevice 64905684095684",
			mpathDeviceName:             "fakeMpathDevice",
			volumeIdByVolumePath:        volumeUuid,
			isVolumePathMatchesVolumeId: true,
		},
		{
			name:            "fail when trying to get mpath output",
			volumeUuid:      "volumeUuid",
			volumePath:      "/path",
			mpathdOutputErr: errors.New("failed in getting mpath output"),
		},
		{
			name:                        "fail when trying to get mpath device name",
			volumeUuid:                  "volumeUuid",
			volumePath:                  "/path",
			mpathdOutput:                "fakeMpathDevice 64905684095684",
			mpathDeviceNameErr:          errors.New("failed in getting mpath device name"),
			isVolumePathMatchesVolumeId: false,
		},
		{
			name:                        "fail when trying to match volume id to mpath name",
			volumeUuid:                  "volumeUuid",
			volumePath:                  "/path",
			mpathdOutput:                "fakeMpathDevice 64905684095684",
			mpathDeviceName:             "fakeMpathDevice",
			matchingVolumeIdErr:         errors.New("failed in matching volume id to mpath name"),
			isVolumePathMatchesVolumeId: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			volumeIdVariations := []string{volumeUuid, volumeNguid}

			mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			o := NewOsDeviceConnectivityHelperScsiGenericForTest(fakeExecuter, mockOsDeviceConHelper, nil)

			mockOsDeviceConHelper.EXPECT().GetVolumeIdVariations(tc.volumeUuid).Return(volumeIdVariations)
			mockOsDeviceConHelper.EXPECT().GetMpathdOutputForVolume(volumeIdVariations,
				device_connectivity.MultipathdWildcardsMpathNameAndVolumeId).Return(tc.mpathdOutput, tc.mpathdOutputErr)
			if tc.mpathdOutput != "" {
				mockOsDeviceConHelper.EXPECT().GetMpathDeviceName(tc.volumePath).Return(tc.mpathDeviceName, tc.mpathDeviceNameErr)
			}
			if tc.mpathDeviceName != "" {
				mockOsDeviceConHelper.EXPECT().GetMpathVolumeId(
					tc.mpathdOutput, tc.mpathDeviceName).Return(tc.volumeIdByVolumePath, tc.matchingVolumeIdErr)
			}
			if tc.volumeIdByVolumePath != "" {
				mockOsDeviceConHelper.EXPECT().IsAnyVariationInMpathVolumeId(tc.volumeIdByVolumePath, volumeIdVariations).Return(
					tc.isVolumePathMatchesVolumeId)
			}
			isVolumePathMatchesVolumeId, err := o.IsVolumePathMatchesVolumeId(tc.volumeUuid, tc.volumePath)

			if isVolumePathMatchesVolumeId != tc.isVolumePathMatchesVolumeId {
				t.Fatalf("wrong volumestats: expected %v, got %v", tc.isVolumePathMatchesVolumeId, isVolumePathMatchesVolumeId)
			}
			if tc.mpathdOutputErr != nil {
				assertExpectedError(t, tc.mpathdOutputErr, err)
			} else if tc.mpathDeviceNameErr != nil {
				assertExpectedError(t, tc.mpathDeviceNameErr, err)
			} else {
				assertExpectedError(t, tc.matchingVolumeIdErr, err)
			}

		})
	}
}

func assertExpectedError(t *testing.T, expectedError error, responseErr error) {
	if expectedError != responseErr {
		t.Fatalf("wrong error: expected %v, got %v", expectedError, responseErr)
	}
}

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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	gomock "github.com/golang/mock/gomock"
	mocks "github.com/ibm/ibm-block-csi-driver/node/mocks"
	driver "github.com/ibm/ibm-block-csi-driver/node/pkg/driver"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

var (
	nodeUtils    = driver.NewNodeUtils(&executer.Executer{}, nil, ConfigYaml, device_connectivity.OsDeviceConnectivityHelperScsiGeneric{})
	hostName     = "test-hostname"
	longHostName = strings.Repeat(hostName, 25)
	nvmeNQN      = "nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1"
	fcWWNs       = []string{"10000000c9934d9f", "10000000c9934d9h", "10000000c9934d9a", "10000000c9934d9b",
		"10000000c9934d9z", "10000000c9934d9c", "10000000c9934d9d", "10000000c9934d9e", "10000000c9934d9g", "10000000c9934d9i"}
	iscsiIQN    = "iqn.1994-07.com.redhat:e123456789"
	volumeUuid  = "6oui000vendorsi0vendorsie0000000"
	volumeNguid = "vendorsie0000000oui0000vendorsi0"
)

func TestReadNvmeNqn(t *testing.T) {
	testCases := []struct {
		name         string
		file_content string
		expErr       error
		expNqn       string
	}{
		{
			name:   "non existing file",
			expErr: &os.PathError{Op: "open", Path: "/non/existent/path", Err: syscall.ENOENT},
		},
		{
			name:         "right_nqn",
			file_content: nvmeNQN,
			expNqn:       nvmeNQN,
		},
		{
			name:         "right nqn with commented lines",
			file_content: fmt.Sprintf("//commentedlines\n//morecommentedlines\n%s", nvmeNQN),
			expNqn:       nvmeNQN,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			filePath := ""

			if tc.file_content != "" {
				tmpFile, err := ioutil.TempFile(os.TempDir(), "nvme-")
				fmt.Println(tmpFile)
				if err != nil {
					t.Fatalf("Cannot create temporary file : %v", err)
				}

				defer func() {
					os.Remove(tmpFile.Name())
					driver.NvmeFullPath = "/host/etc/nvme/hostnqn"
				}()

				fmt.Println("Created File: " + tmpFile.Name())

				text := []byte(tc.file_content)
				if _, err = tmpFile.Write(text); err != nil {
					t.Fatalf("Failed to write to temporary file: %v", err)
				}

				if err := tmpFile.Close(); err != nil {
					t.Fatalf("%s", err.Error())
				}
				filePath = tmpFile.Name()
			} else {
				filePath = "/non/existent/path"
			}

			driver.NvmeFullPath = filePath
			nqn, err := nodeUtils.ReadNvmeNqn()

			if tc.expErr != nil {
				if err.Error() != tc.expErr.Error() {
					t.Fatalf("Expecting err: expected %v, got %v", tc.expErr, err)
				}

			} else {
				if err != nil {
					t.Fatalf("err is not nil. got: %v", err)
				}
				if nqn != tc.expNqn {
					t.Fatalf("scheme mismatches: expected %v, got %v", tc.expNqn, nqn)
				}

			}

		})
	}

}

func TestParseFCPortsName(t *testing.T) {
	testCases := []struct {
		name          string
		file_contents []string
		err           error
		expErr        error
		expFCPorts    []string
	}{
		{
			name:          "fc port file with wrong content",
			file_contents: []string{"wrong content"},
			expErr:        fmt.Errorf(driver.ErrorWhileTryingToReadPort, ConfigYaml.Connectivity_type.Fc, "wrong content"),
		},
		{
			name:   "fc unsupported",
			expErr: fmt.Errorf(driver.ErrorUnsupportedConnectivityType, ConfigYaml.Connectivity_type.Fc),
		},
		{
			name:          "one fc port",
			file_contents: []string{"0x10000000c9934d9f"},
			expFCPorts:    []string{"10000000c9934d9f"},
		},
		{
			name:          "one fc port file with wrong content, another is valid",
			file_contents: []string{"wrong content", "0x10000000c9934dab"},
			expFCPorts:    []string{"10000000c9934dab"},
		},
		{
			name:          "one fc port file with wrong content, another file path is inexistent",
			file_contents: []string{"wrong content", ""},
			expErr:        errors.New("[Error while trying to get fc port from string: wrong content., open /non/existent/path: no such file or directory]"),
		},
		{
			name:          "two FC ports",
			file_contents: []string{"0x10000000c9934d9f", "0x10000000c9934dab"},
			expFCPorts:    []string{"10000000c9934d9f", "10000000c9934dab"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filePath := ""
			var fpaths []string

			for _, file_content := range tc.file_contents {
				if file_content != "" {
					tmpFile, err := ioutil.TempFile(os.TempDir(), "fc-")
					fmt.Println(tmpFile)
					if err != nil {
						t.Fatalf("Cannot create temporary file : %v", err)
					}

					defer os.Remove(tmpFile.Name())

					text := []byte(file_content)
					if _, err = tmpFile.Write(text); err != nil {
						t.Fatalf("Failed to write to temporary file: %v", err)
					}

					if err := tmpFile.Close(); err != nil {
						t.Fatalf("%s", err.Error())
					}
					filePath = tmpFile.Name()
				} else {
					filePath = "/non/existent/path"
				}

				fpaths = append(fpaths, filePath)
			}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			devicePath := "/sys/class/fc_host/host*/port_name"
			fakeExecuter.EXPECT().FilepathGlob(devicePath).Return(fpaths, tc.err)
			nodeUtils := driver.NewNodeUtils(fakeExecuter, nil, ConfigYaml, nil)

			fcs, err := nodeUtils.ParseFCPorts()

			if tc.expErr != nil {
				if err.Error() != tc.expErr.Error() {
					t.Fatalf("Expecting err: expected %v, got %v", tc.expErr, err)
				}

			} else {
				if err != nil {
					t.Fatalf("err is not nil. got: %v", err)
				}
				if !reflect.DeepEqual(fcs, tc.expFCPorts) {
					t.Fatalf("scheme mismatches: expected %v, got %v", tc.expFCPorts, fcs)
				}

			}
		})
	}
}

func TestParseIscsiInitiators(t *testing.T) {
	testCases := []struct {
		name         string
		file_content string
		expErr       error
		expIqn       string
	}{
		{
			name:         "wrong iqn file",
			file_content: "wrong-content",
			expErr:       fmt.Errorf(driver.ErrorWhileTryingToReadPort, ConfigYaml.Connectivity_type.Iscsi, "wrong-content"),
		},
		{
			name:   "non existing file",
			expErr: &os.PathError{Op: "open", Path: "/non/existent/path", Err: syscall.ENOENT},
		},
		{
			name:         "right_iqn",
			file_content: fmt.Sprintf("InitiatorName=%s", iscsiIQN),
			expIqn:       iscsiIQN,
		},
		{
			name:         "right iqn with commented lines",
			file_content: fmt.Sprintf("//commentedlines\n//morecommentedlines\nInitiatorName=%s", iscsiIQN),
			expIqn:       iscsiIQN,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			filePath := ""

			if tc.file_content != "" {
				tmpFile, err := ioutil.TempFile(os.TempDir(), "iscis-initiators-")
				fmt.Println(tmpFile)
				if err != nil {
					t.Fatalf("Cannot create temporary file : %v", err)
				}

				defer func() {
					os.Remove(tmpFile.Name())
					driver.IscsiFullPath = "/host/etc/iscsi/initiatorname.iscsi"
				}()

				fmt.Println("Created File: " + tmpFile.Name())

				text := []byte(tc.file_content)
				if _, err = tmpFile.Write(text); err != nil {
					t.Fatalf("Failed to write to temporary file: %v", err)
				}

				if err := tmpFile.Close(); err != nil {
					t.Fatalf("%s", err.Error())
				}
				filePath = tmpFile.Name()
			} else {
				filePath = "/non/existent/path"
			}

			driver.IscsiFullPath = filePath
			isci, err := nodeUtils.ParseIscsiInitiators()

			if tc.expErr != nil {
				if err.Error() != tc.expErr.Error() {
					t.Fatalf("Expecting err: expected %v, got %v", tc.expErr, err)
				}

			} else {
				if err != nil {
					t.Fatalf("err is not nil. got: %v", err)
				}
				if isci != tc.expIqn {
					t.Fatalf("scheme mismatches: expected %v, got %v", tc.expIqn, isci)
				}

			}

		})
	}

}

func TestGetVolumeUuid(t *testing.T) {
	testCases := []struct {
		name     string
		volumeId string
	}{
		{name: "success",
			volumeId: "fakeArray:volumeUuid",
		},
		{name: "success with internal volumeId",
			volumeId: "fakeArray:internalVolumeId;volumeUuid",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			nodeUtils := driver.NewNodeUtils(fakeExecuter, nil, ConfigYaml, nil)

			volumeUuid := nodeUtils.GetVolumeUuid(tc.volumeId)

			expectedVolumeUuid := "volumeUuid"
			if volumeUuid != expectedVolumeUuid {
				t.Fatalf("wrong volumeUuid: expected %v, got %v", expectedVolumeUuid, volumeUuid)
			}

		})
	}

}

func TestGetBlockVolumeStats(t *testing.T) {
	sizeInBytes, _ := strconv.ParseInt("1073741824", 10, 64)
	testCases := []struct {
		name           string
		volumeUuid     string
		mpathDeviceErr error
		mpathDevice    string
		outInBytes     []byte
		outInBytesErr  error
		volumeStats    driver.VolumeStatistics
	}{
		{
			name:           "success",
			volumeUuid:     "volumeUuid",
			mpathDeviceErr: nil,
			mpathDevice:    "fakeMpathDevice",
			outInBytes:     []byte("1073741824"),
			outInBytesErr:  nil,
			volumeStats: driver.VolumeStatistics{
				TotalBytes: sizeInBytes,
			},
		},
		{
			name:           "failed to get mpath device",
			volumeUuid:     "volumeUuid",
			mpathDeviceErr: &device_connectivity.MultipathDeviceNotFoundForVolumeError{VolumeId: ""},
			mpathDevice:    "",
			outInBytes:     []byte{},
			outInBytesErr:  nil,
			volumeStats:    driver.VolumeStatistics{},
		},
		{
			name:           "failed to get size of mpath device",
			volumeUuid:     "volumeUuid",
			mpathDeviceErr: nil,
			mpathDevice:    "fakeMpathDevice",
			outInBytes:     []byte{},
			outInBytesErr:  errors.New("failed to run command"),
			volumeStats:    driver.VolumeStatistics{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			mockOsDeviceConHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtrl)
			nodeUtils := driver.NewNodeUtils(fakeExecuter, nil, ConfigYaml, mockOsDeviceConHelper)
			args := []string{"--getsize64", tc.mpathDevice}

			mockOsDeviceConHelper.EXPECT().GetMpathDevice(tc.volumeUuid).Return(tc.mpathDevice, tc.mpathDeviceErr)
			if tc.mpathDevice != "" {
				fakeExecuter.EXPECT().ExecuteWithTimeoutSilently(
					device_connectivity.TimeOutBlockDevCmd, driver.BlockDevCmd, args).Return(tc.outInBytes, tc.outInBytesErr)
			}
			volumestats, err := nodeUtils.GetBlockVolumeStats(tc.volumeUuid)

			if volumestats != tc.volumeStats {
				t.Fatalf("wrong volumestats: expected %v, got %v", tc.volumeStats, volumestats)
			}
			if tc.mpathDeviceErr != nil {
				assertExpectedError(t, tc.mpathDeviceErr, err)
			} else {
				assertExpectedError(t, tc.outInBytesErr, err)
			}

		})
	}
}

func assertExpectedError(t *testing.T, expectedError error, responseErr error) {
	if expectedError != responseErr {
		t.Fatalf("wrong error: expected %v, got %v", expectedError, responseErr)
	}
}

func TestGetInfoFromPublishContext(t *testing.T) {
	configYaml := driver.ConfigFile{
		Controller: driver.Controller{
			Publish_context_lun_parameter:          "PUBLISH_CONTEXT_LUN",
			Publish_context_connectivity_parameter: "PUBLISH_CONTEXT_CONNECTIVITY",
			Publish_context_separator:              ",",
			Publish_context_array_iqn:              "PUBLISH_CONTEXT_ARRAY_IQN",
			Publish_context_fc_initiators:          "PUBLISH_CONTEXT_ARRAY_FC_INITIATORS",
			Publish_context_nvme_initiators:        "PUBLISH_CONTEXT_ARRAY_NVME_INITIATORS",
		},
		Connectivity_type: driver.Connectivity_type{Nvme_over_fc: "nvmeofc", Nvme_over_tcp: "nvmeotcp", Fc: "fc", Iscsi: "iscsi"},
	}
	testCases := []struct {
		name                   string
		publishContext         map[string]string
		expConnectivityType    string
		expLun                 int
		expIpsByArrayInitiator map[string][]string
		expErr                 bool
	}{
		{
			name: "nvmeotcp target IPs without LUN",
			publishContext: map[string]string{
				"PUBLISH_CONTEXT_ARRAY_NVME_INITIATORS": "9.71.253.97,9.71.253.96",
				"PUBLISH_CONTEXT_CONNECTIVITY":          "nvmeotcp",
				"PUBLISH_CONTEXT_LUN":                   "",
			},
			expConnectivityType:    "nvmeotcp",
			expLun:                 0,
			expIpsByArrayInitiator: map[string][]string{"9.71.253.97": nil, "9.71.253.96": nil},
		},
		{
			name: "nvmeotcp target IPs with spaces and empty entries",
			publishContext: map[string]string{
				"PUBLISH_CONTEXT_ARRAY_NVME_INITIATORS": " 9.71.253.97 ,, 9.71.253.96",
				"PUBLISH_CONTEXT_CONNECTIVITY":          "nvmeotcp",
			},
			expConnectivityType:    "nvmeotcp",
			expLun:                 0,
			expIpsByArrayInitiator: map[string][]string{"9.71.253.97": nil, "9.71.253.96": nil},
		},
		{
			name: "nvmeofc target ports without LUN",
			publishContext: map[string]string{
				"PUBLISH_CONTEXT_ARRAY_NVME_INITIATORS": "nn-5005076810000006:pn-5005076810190006,nn-5005076810004654:pn-5005076810194654",
				"PUBLISH_CONTEXT_CONNECTIVITY":          "nvmeofc",
				"PUBLISH_CONTEXT_LUN":                   "",
			},
			expConnectivityType: "nvmeofc",
			expLun:              0,
			expIpsByArrayInitiator: map[string][]string{
				"nn-5005076810000006:pn-5005076810190006": nil,
				"nn-5005076810004654:pn-5005076810194654": nil,
			},
		},
		{
			name: "iscsi without LUN fails",
			publishContext: map[string]string{
				"PUBLISH_CONTEXT_ARRAY_IQN":    "iqn.1994-05.com.redhat:686358c930fe",
				"PUBLISH_CONTEXT_CONNECTIVITY": "iscsi",
				"PUBLISH_CONTEXT_LUN":          "",
			},
			expLun: -1,
			expErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nodeUtils := driver.NewNodeUtils(&executer.Executer{}, nil, configYaml, nil)
			connectivityType, lun, ipsByArrayInitiator, err := nodeUtils.GetInfoFromPublishContext(tc.publishContext)
			if (err != nil) != tc.expErr {
				t.Fatalf("wrong error: expected error %v, got %v", tc.expErr, err)
			}
			if connectivityType != tc.expConnectivityType {
				t.Fatalf("wrong connectivity type: expected %q, got %q", tc.expConnectivityType, connectivityType)
			}
			if lun != tc.expLun {
				t.Fatalf("wrong lun: expected %d, got %d", tc.expLun, lun)
			}
			if !reflect.DeepEqual(ipsByArrayInitiator, tc.expIpsByArrayInitiator) {
				t.Fatalf("wrong array initiators: expected %v, got %v", tc.expIpsByArrayInitiator, ipsByArrayInitiator)
			}
		})
	}
}

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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
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

type nvmeProbeExecuter struct {
	executer.Executer
	output []byte
	err    error
}

func (e *nvmeProbeExecuter) ExecuteWithTimeout(int, string, []string) ([]byte, error) {
	return e.output, e.err
}

func TestDevicesAreNvmeCommandErrors(t *testing.T) {
	if native, _ := (driver.NodeUtils{}).IsNativeNVMeMultipathEnabled(); native {
		t.Skip("nvme list is not used when native NVMe multipath is enabled")
	}

	testCases := []struct {
		name     string
		exitCode int
		output   string
		err      error
		wantErr  bool
	}{
		{name: "modules not loaded", exitCode: 1},
		{name: "GNU env missing nvme", exitCode: 127, output: "/usr/bin/env: 'nvme': No such file or directory\n"},
		{name: "uutils env missing nvme", exitCode: 127, output: "env: 'nvme': No such file or directory\nenv: use -[v]S to pass options in shebang lines\n"},
		{name: "missing nvme without output", exitCode: 127},
		{name: "missing nvme with localized output", exitCode: 127, output: "env: nvme: fichier introuvable\n"},
		{name: "permission denied", exitCode: 126, wantErr: true},
		{name: "other command failure", exitCode: 2, wantErr: true},
		{name: "unrelated missing file", exitCode: 2, output: "No such file or directory\n", wantErr: true},
		{name: "timeout", err: context.DeadlineExceeded, wantErr: true},
		{name: "non-exit error resembling status 1", err: errors.New("exit status 1"), wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			probeErr := tc.err
			if probeErr == nil {
				// Obtain a real exec.ExitError so the production GetExitCode is exercised.
				probeErr = exec.Command("sh", "-c", fmt.Sprintf("exit %d", tc.exitCode)).Run()
				if _, ok := probeErr.(*exec.ExitError); !ok {
					t.Fatalf("expected exec.ExitError, got %v", probeErr)
				}
			}
			nu := driver.NodeUtils{Executer: &nvmeProbeExecuter{output: []byte(tc.output), err: probeErr}}
			got, err := nu.DevicesAreNvme("dm-test")
			if got != driver.NotNVMe {
				t.Fatalf("expected NotNVMe, got %s", got)
			}
			if tc.wantErr {
				if err != probeErr {
					t.Fatalf("expected original error %v, got %v", probeErr, err)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

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

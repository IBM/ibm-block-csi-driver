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
	nodeUtils    = driver.NewNodeUtils(&executer.Executer{}, nil)
	hostName     = "test-hostname"
	longHostName = strings.Repeat("test-hostname", 15)
	nvmeNQN      = "nqn.2014-08.org.nvmexpress:uuid:b57708c7-5bb6-46a0-b2af-9d824bf539e1"
	fcWWNs       = []string{"10000000c9934d9f", "10000000c9934d9h", "10000000c9934d9a", "10000000c9934d9b", "10000000c9934d9z"}
	iscsiIQN     = "iqn.1994-07.com.redhat:e123456789"
)

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
			expErr:       fmt.Errorf(driver.ErrorWhileTryingToReadIQN, "wrong-content"),
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
					t.Fatalf(err.Error())
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
			expErr:        fmt.Errorf(driver.ErrorWhileTryingToReadFC, "wrong content"),
		},
		{
			name:   "fc unsupported",
			expErr: fmt.Errorf(driver.ErrorUnsupportedConnectivityType, device_connectivity.ConnectionTypeFC),
		},
		{
			name:          "one FC port",
			file_contents: []string{"0x10000000c9934d9f"},
			expFCPorts:    []string{"10000000c9934d9f"},
		},
		{
			name:          "one FC port file with wrong content, another is good",
			file_contents: []string{"wrong content", "0x10000000c9934dab"},
			expFCPorts:    []string{"10000000c9934dab"},
		},
		{
			name:          "one fc port file with wrong content, aonther file path is inexistent",
			file_contents: []string{"wrong content", ""},
			expErr:        errors.New("[Error while tring to get FC port from string: wrong content., open /non/existent/path: no such file or directory]"),
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
						t.Fatalf(err.Error())
					}
					filePath = tmpFile.Name()
				} else {
					filePath = "/non/existent/path"
				}

				fpaths = append(fpaths, filePath)
			}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			devicePath := "/sys/class/fc_host/host*/port_name"
			fake_executer.EXPECT().FilepathGlob(devicePath).Return(fpaths, tc.err)
			nodeUtils := driver.NewNodeUtils(fake_executer, nil)

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

func TestGenerateNodeID(t *testing.T) {
	testCases := []struct {
		name      string
		hostName  string
		nvmeNQN   string
		fcWWNs    []string
		iscsiIQN  string
		expErr    error
		expNodeId string
	}{
		{name: "success all in",
			hostName:  hostName,
			nvmeNQN:   nvmeNQN,
			fcWWNs:    fcWWNs[:2],
			iscsiIQN:  iscsiIQN,
			expNodeId: fmt.Sprintf("%s;%s;%s;%s", hostName, nvmeNQN, strings.Join(fcWWNs[:2], ":"), iscsiIQN),
		},
		{name: "success only iscsi port",
			hostName:  hostName,
			nvmeNQN:   "",
			fcWWNs:    []string{},
			iscsiIQN:  iscsiIQN,
			expNodeId: fmt.Sprintf("%s;;;%s", hostName, iscsiIQN),
		},
		{name: "success only fc ports",
			hostName:  hostName,
			nvmeNQN:   "",
			fcWWNs:    fcWWNs[:2],
			iscsiIQN:  "",
			expNodeId: fmt.Sprintf("%s;;%s", hostName, strings.Join(fcWWNs[:2], ":")),
		},
		{name: "success only nvme port",
			hostName:  hostName,
			nvmeNQN:   nvmeNQN,
			fcWWNs:    []string{},
			iscsiIQN:  "",
			expNodeId: fmt.Sprintf("%s;%s;", hostName, nvmeNQN),
		},
		{name: "success many fc ports and iscsi port",
			hostName:  hostName,
			nvmeNQN:   "",
			fcWWNs:    fcWWNs,
			iscsiIQN:  iscsiIQN,
			expNodeId: fmt.Sprintf("%s;;%s;%s", hostName, strings.Join(fcWWNs, ":"), iscsiIQN),
		},
		{name: "fail long hostName on nvme port",
			hostName: longHostName,
			nvmeNQN:  nvmeNQN,
			fcWWNs:   []string{},
			iscsiIQN: "",
			expErr:   errors.New(fmt.Sprintf("could not fit any ports in node id: %s;, length limit: 192", longHostName)),
		},
		{name: "fail long hostName on fc ports",
			hostName: longHostName,
			nvmeNQN:  "",
			fcWWNs:   fcWWNs[:2],
			iscsiIQN: "",
			expErr:   errors.New(fmt.Sprintf("could not fit any ports in node id: %s;;, length limit: 192", longHostName)),
		},
		{name: "fail long hostName on iscsi ports",
			hostName: longHostName,
			nvmeNQN:  "",
			fcWWNs:   []string{},
			iscsiIQN: iscsiIQN,
			expErr:   errors.New(fmt.Sprintf("could not fit any ports in node id: %s;;, length limit: 192", longHostName)),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			nodeUtils := driver.NewNodeUtils(fake_executer, nil)

			nodeId, err := nodeUtils.GenerateNodeID(tc.hostName, tc.nvmeNQN, tc.fcWWNs, tc.iscsiIQN)

			if tc.expErr != nil {
				if err == nil || err.Error() != tc.expErr.Error() {
					t.Fatalf("Expecting err: expected %v, got %v", tc.expErr, err)
				}

			} else {
				if err != nil {
					t.Fatalf("err is not nil. got: %v", err)
				}
				if nodeId != tc.expNodeId {
					t.Fatalf("wrong nodeId: expected %v, got %v", tc.expNodeId, nodeId)
				}

			}
		})
	}
}

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
	gomock "github.com/golang/mock/gomock"
	mocks "github.com/ibm/ibm-block-csi-driver/node/mocks"
	driver "github.com/ibm/ibm-block-csi-driver/node/pkg/driver"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"io/ioutil"
	"os"
	"reflect"
	"syscall"
	"testing"
)

var (
	nodeUtils = driver.NewNodeUtils(&executer.Executer{}, nil)
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
			file_content: "InitiatorName=iqn.1996-05.com.redhat:123123122",
			expIqn:       "iqn.1996-05.com.redhat:123123122",
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
		fcWWNs    []string
		iscsiIQN  string
		expErr    error
		expNodeId string
	}{
		{name: "success all in",
			hostName:  "test-host",
			fcWWNs:    []string{"10000000c9934d9f", "10000000c9934d9h"},
			iscsiIQN:  "iqn.1994-07.com.redhat:e123456789",
			expNodeId: "test-host;10000000c9934d9f:10000000c9934d9h;iqn.1994-07.com.redhat:e123456789",
		},
		{name: "success no fc ports",
			hostName:  "test-hostname.ibm.com",
			fcWWNs:    []string{},
			iscsiIQN:  "iqn.1994-07.com.redhat:e123456789",
			expNodeId: "test-hostname.ibm.com;;iqn.1994-07.com.redhat:e123456789",
		},
		{name: "success no iscsi port",
			hostName:  "test-hostname.ibm.com",
			fcWWNs:    []string{"10000000c9934d9f", "10000000c9934d9h"},
			iscsiIQN:  "",
			expNodeId: "test-hostname.ibm.com;10000000c9934d9f:10000000c9934d9h",
		},
		{name: "success many fc ports",
			hostName:  "test-hostname.ibm.com",
			fcWWNs:    []string{"10000000c9934d9f", "10000000c9934d9h", "10000000c9934d9a", "10000000c9934d9b", "10000000c9934d9z"},
			iscsiIQN:  "iqn.1994-07.com.redhat:e123456789",
			expNodeId: "test-hostname.ibm.com;10000000c9934d9f:10000000c9934d9h:10000000c9934d9a:10000000c9934d9b:10000000c9934d9z",
		},
		{name: "fail long hostName on fc ports",
			hostName: "test-hostname-that-is-too-long-and-take-almost-128-characters-so-no-port-get-place-additional-characters.nodeutilstest.ibm.com",
			fcWWNs:   []string{"10000000c9934d9f", "10000000c9934d9h"},
			iscsiIQN: "",
			expErr:   errors.New("could not fit any ports in node id: test-hostname-that-is-too-long-and-take-almost-128-characters-so-no-port-get-place-additional-characters.nodeutilstest.ibm.com;, length limit: 128"),
		},
		{name: "fail long hostName on iscsi ports",
			hostName: "test-hostname-that-is-too-long-and-take-almost-128-characters-so-no-port-get-place-additional-characters.nodeutilstest.ibm.com",
			fcWWNs:   []string{},
			iscsiIQN: "iqn.1994-07.com.redhat:e123456789",
			expErr:   errors.New("could not fit any ports in node id: test-hostname-that-is-too-long-and-take-almost-128-characters-so-no-port-get-place-additional-characters.nodeutilstest.ibm.com;, length limit: 128"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			nodeUtils := driver.NewNodeUtils(fake_executer, nil)

			nodeId, err := nodeUtils.GenerateNodeID(tc.hostName, tc.fcWWNs, tc.iscsiIQN)

			if tc.expErr != nil {
				if err.Error() != tc.expErr.Error() {
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

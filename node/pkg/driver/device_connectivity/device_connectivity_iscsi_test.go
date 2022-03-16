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
	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"testing"
)

var (
	iscsiCmdTimeoutMsec      = int(device_connectivity.IscsiCmdTimeout.Seconds() * 1000)
	iscsiCmd                 = "iscsiadm"
	iSCSIErrNoObjsFound      = device_connectivity.ISCSIErrNoObjsFound
	iscsiSessionArgs         = []string{"-m", "session"}
	iscsiDiscoverPortal1Args = []string{"-m", "discoverydb", "-t", "sendtargets", "-p", "portal1", "--discover"}
	iscsiDiscoverPortal2Args = []string{"-m", "discoverydb", "-t", "sendtargets", "-p", "portal2", "--discover"}
	iscsiLoginPortal1Args    = []string{"-m", "node", "-p", "portal1:3260", "-T", "target", "--login"}
	iscsiLoginPortal2Args    = []string{"-m", "node", "-p", "portal2:3260", "-T", "target", "--login"}
)

func NewOsDeviceConnectivityIscsiForTest(
	executer executer.ExecuterInterface,
	helper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface,
) device_connectivity.OsDeviceConnectivityInterface {
	return &device_connectivity.OsDeviceConnectivityIscsi{
		Executer:          executer,
		HelperScsiGeneric: helper,
	}
}

type ExecuteWithTimeoutArguments struct {
	mSeconds         int
	command          string
	commandArguments []string
}

type ExecuteWithTimeoutReturn struct {
	stdOut   []byte
	err      error
	exitCode int
}

type ExecuteWithTimeoutCall struct {
	input  ExecuteWithTimeoutArguments
	output ExecuteWithTimeoutReturn
}

func getExecuteWithTimeoutArguments(args []string) ExecuteWithTimeoutArguments {
	return ExecuteWithTimeoutArguments{
		mSeconds:         iscsiCmdTimeoutMsec,
		command:          iscsiCmd,
		commandArguments: args,
	}
}

func TestEnsureLogin(t *testing.T) {
	portalsByTarget := map[string][]string{"target": {"portal1", "portal2"}}

	executeIscsiSessionArguments := getExecuteWithTimeoutArguments(iscsiSessionArgs)
	executeIscsiDiscoverPortal1Arguments := getExecuteWithTimeoutArguments(iscsiDiscoverPortal1Args)
	executeIscsiDiscoverPortal2Arguments := getExecuteWithTimeoutArguments(iscsiDiscoverPortal2Args)
	executeIscsiLoginPortal1Arguments := getExecuteWithTimeoutArguments(iscsiLoginPortal1Args)
	executeIscsiLoginPortal2Arguments := getExecuteWithTimeoutArguments(iscsiLoginPortal2Args)

	singlePortalIscsiSessionOutput := "tcp: [1] portal1:3260,2 target (non-flash)"
	twoPortalsIscsiSessionOutput := fmt.Sprintf("%s\ntcp: [2] portal2:3260,2 target (non-flash)",
		singlePortalIscsiSessionOutput)

	testCases := []struct {
		name                    string
		portalsByTarget         map[string][]string
		executeWithTimeoutCalls []ExecuteWithTimeoutCall
	}{
		{
			name:            "Should not login when session command fails",
			portalsByTarget: portalsByTarget,
			executeWithTimeoutCalls: []ExecuteWithTimeoutCall{
				{
					input: executeIscsiSessionArguments,
					output: ExecuteWithTimeoutReturn{
						stdOut:   []byte("session command failure details"),
						err:      errors.New("session command failed"),
						exitCode: -1,
					},
				},
			},
		},
		{
			name:            "Should not login when session output is invalid",
			portalsByTarget: portalsByTarget,
			executeWithTimeoutCalls: []ExecuteWithTimeoutCall{
				{
					input: executeIscsiSessionArguments,
					output: ExecuteWithTimeoutReturn{
						stdOut: []byte("invalid session output"),
					},
				},
			},
		},
		{
			name:            "Should not login when all relevant portals are already in session output",
			portalsByTarget: portalsByTarget,
			executeWithTimeoutCalls: []ExecuteWithTimeoutCall{
				{
					input: executeIscsiSessionArguments,
					output: ExecuteWithTimeoutReturn{
						stdOut: []byte(twoPortalsIscsiSessionOutput),
					},
				},
			},
		},
		{
			name:            "Should not login when the only discover call fails",
			portalsByTarget: portalsByTarget,
			executeWithTimeoutCalls: []ExecuteWithTimeoutCall{
				{
					input: executeIscsiSessionArguments,
					output: ExecuteWithTimeoutReturn{
						stdOut: []byte(singlePortalIscsiSessionOutput),
					},
				},
				{
					input: executeIscsiDiscoverPortal2Arguments,
					output: ExecuteWithTimeoutReturn{
						stdOut: []byte("error"),
						err:    errors.New("discover failed"),
					},
				},
			},
		},
		{
			name:            "Should login all portals not in session when a target discover call succeeds",
			portalsByTarget: portalsByTarget,
			executeWithTimeoutCalls: []ExecuteWithTimeoutCall{
				{
					input: executeIscsiSessionArguments,
					output: ExecuteWithTimeoutReturn{
						stdOut:   []byte{},
						err:      errors.New("no sessions"),
						exitCode: iSCSIErrNoObjsFound,
					},
				},
				{
					input: executeIscsiDiscoverPortal1Arguments,
					output: ExecuteWithTimeoutReturn{
						stdOut: []byte("error"),
						err:    errors.New("discover failed"),
					},
				},
				{
					input: executeIscsiDiscoverPortal2Arguments,
				},
				{
					input: executeIscsiLoginPortal1Arguments,
				},
				{
					input: executeIscsiLoginPortal2Arguments,
				},
			},
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			fakeHelper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtrl)

			for _, c := range tc.executeWithTimeoutCalls {
				fakeExecuter.EXPECT().ExecuteWithTimeout(c.input.mSeconds, c.input.command, c.input.commandArguments).Return(
					c.output.stdOut, c.output.err)
				if c.output.err != nil && c.output.exitCode != 0 {
					fakeExecuter.EXPECT().GetExitCode(gomock.Any()).Return(c.output.exitCode, true)
				}
			}

			o := NewOsDeviceConnectivityIscsiForTest(fakeExecuter, fakeHelper)
			o.EnsureLogin(tc.portalsByTarget)
		})
	}
}

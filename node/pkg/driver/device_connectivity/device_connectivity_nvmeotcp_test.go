/**
 * Copyright 2026 IBM Corp.
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
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
)

const (
	nvmeTcpTestSubNqn = "nqn.1986-03.com.ibm:nvme:2145.000002042B60000C"

	// Live NVMe-oFC and NVMe-oTCP paths to the same subsystem.
	nvmeTcpTestListSubsysLive = `nvme-subsys0 - NQN=nqn.1986-03.com.ibm:nvme:2145.000002042B60000C
               hostnqn=nqn.2014-08.org.nvmexpress:uuid:9ffdf84c-1328-11e5-ac71-0894ef016e40
               iopolicy=numa
\
 +- nvme0 tcp traddr=9.71.253.96,trsvcid=4420,src_addr=9.71.253.105 live
 +- nvme1 tcp traddr=9.71.253.97,trsvcid=4420,src_addr=9.71.253.105 live
 +- nvme4 fc traddr=nn-5005076810000006:pn-5005076810190006,host_traddr=nn-200070b7e42b38b1:pn-100070b7e42b38b1 live
`

	// Only NVMe-oFC paths, none live.
	nvmeTcpTestListSubsysNoTcp = `nvme-subsys0 - NQN=nqn.1986-03.com.ibm:nvme:2145.000002042B60000C
               hostnqn=nqn.2014-08.org.nvmexpress:uuid:9ffdf84c-1328-11e5-ac71-0894ef016e40
               iopolicy=numa
\
 +- nvme0 fc traddr=nn-5005076810000006:pn-50050768101b0006,host_traddr=nn-200070b7e42b38b1:pn-100070b7e42b38b1 connecting
 +- nvme1 fc traddr=nn-5005076810000006:pn-50050768101b0006,host_traddr=nn-200070b7e42b38b2:pn-100070b7e42b38b2 connecting
`

	nvmeTcpTestDiscoverOutput = `
Discovery Log Number of Records 2, Generation counter 0
=====Discovery Log Entry 0======
trtype:  tcp
adrfam:  ipv4
subtype: nvme subsystem
treq:    not required, sq flow control disable supported
portid:  517
trsvcid: 4420
subnqn:  nqn.1986-03.com.ibm:nvme:2145.000002042B60000C
traddr:  9.71.253.97
eflags:  none
sectype: none
=====Discovery Log Entry 1======
trtype:  tcp
adrfam:  ipv4
subtype: nvme subsystem
treq:    not required, sq flow control disable supported
portid:  262
trsvcid: 4420
subnqn:  nqn.1986-03.com.ibm:nvme:2145.000002042B60000C
traddr:  9.71.253.96
eflags:  none
sectype: none
`
)

type nvmeTcpTestCall struct {
	args   []string
	stdOut string
	err    error
}

func TestNvmeOTcpEnsureLogin(t *testing.T) {
	listSubsysArgs := []string{"list-subsys"}
	discoverArgs := func(ip string) []string {
		return []string{"discover", "--transport=tcp", "--traddr=" + ip, "--trsvcid=4420"}
	}
	connectArgs := func(ip string) []string {
		return []string{"connect", "--transport=tcp", "--traddr=" + ip, "--trsvcid=4420", "--nqn=" + nvmeTcpTestSubNqn}
	}
	connectErr := errors.New("exit status 1")
	connectOut := "could not add new controller: invalid arguments/configuration\n"
	targets := map[string][]string{"9.71.253.97": nil, "9.71.253.96": nil}

	testCases := []struct {
		name                string
		ipsByArrayInitiator map[string][]string
		calls               []nvmeTcpTestCall
	}{
		{
			name:                "no target IPs in publish context, no nvme command",
			ipsByArrayInitiator: map[string][]string{},
			calls:               nil,
		},
		{
			name:                "all target IPs already live over tcp, skip discover and connect",
			ipsByArrayInitiator: targets,
			calls: []nvmeTcpTestCall{
				{args: listSubsysArgs, stdOut: nvmeTcpTestListSubsysLive},
			},
		},
		{
			name:                "no live tcp path, discover and connect every target IP in sorted order",
			ipsByArrayInitiator: targets,
			calls: []nvmeTcpTestCall{
				{args: listSubsysArgs, stdOut: nvmeTcpTestListSubsysNoTcp},
				{args: discoverArgs("9.71.253.96"), stdOut: nvmeTcpTestDiscoverOutput},
				{args: connectArgs("9.71.253.96"), stdOut: connectOut, err: connectErr},
				{args: discoverArgs("9.71.253.97"), stdOut: nvmeTcpTestDiscoverOutput},
				{args: connectArgs("9.71.253.97"), stdOut: connectOut, err: connectErr},
				{args: listSubsysArgs, stdOut: nvmeTcpTestListSubsysNoTcp},
			},
		},
		{
			name:                "discover fails, no connect",
			ipsByArrayInitiator: map[string][]string{"9.71.253.96": nil},
			calls: []nvmeTcpTestCall{
				{args: listSubsysArgs, stdOut: ""},
				{args: discoverArgs("9.71.253.96"), stdOut: "", err: errors.New("exit status 1")},
				{args: listSubsysArgs, stdOut: ""},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			expectedCalls := []*gomock.Call{}
			for _, c := range tc.calls {
				expectedCalls = append(expectedCalls,
					fakeExecuter.EXPECT().ExecuteWithTimeout(gomock.Any(), "nvme", c.args).Return([]byte(c.stdOut), c.err))
			}
			if len(expectedCalls) > 0 {
				gomock.InOrder(expectedCalls...)
			}

			device_connectivity.NewOsDeviceConnectivityNvmeOTcp(fakeExecuter, false).EnsureLogin(tc.ipsByArrayInitiator)
		})
	}
}

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
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
)

// listSubsysTwoArrayCtrlsPlusBoot: two FC controllers on the array subsystem
// (nvme0, nvme1) plus an unrelated local boot NVMe (nvme2). list-subsys emits
// traddrs with the "0x" hex prefix; the publish-context target ports do not.
const listSubsysTwoArrayCtrlsPlusBoot = `nvme-subsys0 - NQN=nqn.1986-03.com.ibm:nvme:2145.000002042061C916
\
 +- nvme0 fc traddr=nn-0x500507680b21c8f4:pn-0x500507680b22c8f4 host_traddr=nn-0x20000024ff8b1a2c:pn-0x21000024ff8b1a2c live
 +- nvme1 fc traddr=nn-0x500507680b21c8f5:pn-0x500507680b22c8f5 host_traddr=nn-0x20000024ff8b1a2d:pn-0x21000024ff8b1a2d live
nvme-subsys1 - NQN=nqn.2019-08.local:boot
\
 +- nvme2 pcie traddr=0000:04:00.0 live
`

func newNvmeOFcForTest(exec *mocks.MockExecuterInterface) device_connectivity.OsDeviceConnectivityInterface {
	return &device_connectivity.OsDeviceConnectivityNvmeOFc{
		Executer:          exec,
		HelperScsiGeneric: nil, // RescanDevices does not use the helper
	}
}

// arrayTargets are the two array target ports (publish-context format, no 0x)
// matching nvme0 and nvme1 above.
var arrayTargets = []string{
	"nn-500507680b21c8f4:pn-500507680b22c8f4",
	"nn-500507680b21c8f5:pn-500507680b22c8f5",
}

func expectListSubsys(exec *mocks.MockExecuterInterface, output string, err error) {
	exec.EXPECT().
		ExecuteWithTimeout(gomock.Any(), "nvme", []string{"list-subsys"}).
		Return([]byte(output), err)
}

func expectNsRescan(exec *mocks.MockExecuterInterface, controllerDev string, err error) {
	exec.EXPECT().
		ExecuteWithTimeout(gomock.Any(), "nvme", []string{"ns-rescan", controllerDev}).
		Return([]byte(""), err).
		Times(1)
}

func TestNvmeOFcRescanDevices(t *testing.T) {
	testCases := []struct {
		name       string
		arrayPorts []string
		// setup registers the exact Executer calls this case expects; gomock
		// fails the test if the code issues a call that was not registered.
		setup   func(exec *mocks.MockExecuterInterface)
		wantErr bool
	}{
		{
			name:       "rescans only the array's controllers, not the boot NVMe",
			arrayPorts: arrayTargets,
			setup: func(exec *mocks.MockExecuterInterface) {
				expectListSubsys(exec, listSubsysTwoArrayCtrlsPlusBoot, nil)
				expectNsRescan(exec, "/dev/nvme0", nil)
				expectNsRescan(exec, "/dev/nvme1", nil)
			},
			wantErr: false,
		},
		{
			name:       "matches a single target port, rescans its controller only",
			arrayPorts: []string{arrayTargets[0]},
			setup: func(exec *mocks.MockExecuterInterface) {
				expectListSubsys(exec, listSubsysTwoArrayCtrlsPlusBoot, nil)
				expectNsRescan(exec, "/dev/nvme0", nil)
			},
			wantErr: false,
		},
		{
			name:       "no target ports: skips rescan entirely, non-fatal",
			arrayPorts: []string{},
			setup:      func(exec *mocks.MockExecuterInterface) {}, // no calls expected
			wantErr:    false,
		},
		{
			name:       "no controller matches the array: skips rescan, non-fatal",
			arrayPorts: []string{"nn-aaaaaaaaaaaaaaaa:pn-bbbbbbbbbbbbbbbb"},
			setup: func(exec *mocks.MockExecuterInterface) {
				expectListSubsys(exec, listSubsysTwoArrayCtrlsPlusBoot, nil)
				// no ns-rescan expected
			},
			wantErr: false,
		},
		{
			name:       "list-subsys fails: non-fatal, no rescan, no error",
			arrayPorts: arrayTargets,
			setup: func(exec *mocks.MockExecuterInterface) {
				expectListSubsys(exec, "", fmt.Errorf("nvme list-subsys: command not found"))
			},
			wantErr: false,
		},
		{
			name:       "per-controller ns-rescan failure is non-fatal and still tries the rest",
			arrayPorts: arrayTargets,
			setup: func(exec *mocks.MockExecuterInterface) {
				expectListSubsys(exec, listSubsysTwoArrayCtrlsPlusBoot, nil)
				expectNsRescan(exec, "/dev/nvme0", fmt.Errorf("exit status 1"))
				expectNsRescan(exec, "/dev/nvme1", nil) // still attempted despite nvme0 failing
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			exec := mocks.NewMockExecuterInterface(mockCtrl)
			tc.setup(exec)

			r := newNvmeOFcForTest(exec)
			err := r.RescanDevices(0, tc.arrayPorts)

			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

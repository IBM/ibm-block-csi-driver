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
	nvmeFcTestHostPortPath1 = "/sys/class/fc_host/host11/port_name"
	nvmeFcTestHostPortPath2 = "/sys/class/fc_host/host12/port_name"
	nvmeFcTestHostPort1     = "nn-200070b7e42b38b1:pn-100070b7e42b38b1"
	nvmeFcTestHostPort2     = "nn-200070b7e42b38b2:pn-100070b7e42b38b2"
	nvmeFcTestTargetPort1   = "nn-5005076810000006:pn-5005076810190006"
	nvmeFcTestTargetPort2   = "nn-5005076810000006:pn-50050768101b0006"
	nvmeFcTestOtherArray    = "nn-500507680c00d7cf:pn-500507680c1ad7cf"

	// Three live NVMe-oFC paths to the array.
	nvmeFcTestListSubsysLive = `nvme-subsys0 - NQN=nqn.1986-03.com.ibm:nvme:2145.000002042B60000C
               hostnqn=nqn.2014-08.org.nvmexpress:uuid:9ffdf84c-1328-11e5-ac71-0894ef016e40
               iopolicy=numa
\
 +- nvme0 fc traddr=nn-5005076810000006:pn-5005076810190006,host_traddr=nn-200070b7e42b38b1:pn-100070b7e42b38b1 live
 +- nvme1 fc traddr=nn-5005076810000006:pn-5005076810190006,host_traddr=nn-200070b7e42b38b2:pn-100070b7e42b38b2 live
 +- nvme2 fc traddr=nn-5005076810000006:pn-50050768101b0006,host_traddr=nn-200070b7e42b38b1:pn-100070b7e42b38b1 live
`

	// No NVMe-oFC path, only a stale NVMe-oTCP controller.
	nvmeFcTestListSubsysNoFc = `nvme-subsys0 - NQN=nqn.1986-03.com.ibm:nvme:2145.000002042B60000C
               hostnqn=nqn.2014-08.org.nvmexpress:uuid:9ffdf84c-1328-11e5-ac71-0894ef016e40
               iopolicy=numa
\
 +- nvme1 tcp traddr=9.71.253.97,trsvcid=4420 connecting
`
)

func expectNvmeFcHostPorts(fakeExecuter *mocks.MockExecuterInterface) {
	fakeExecuter.EXPECT().FilepathGlob("/sys/class/fc_host/host*/port_name").Return(
		[]string{nvmeFcTestHostPortPath1, nvmeFcTestHostPortPath2}, nil)
	fakeExecuter.EXPECT().IoutilReadFile(nvmeFcTestHostPortPath1).Return([]byte("0x100070b7e42b38b1\n"), nil)
	fakeExecuter.EXPECT().IoutilReadFile("/sys/class/fc_host/host11/node_name").Return([]byte("0x200070b7e42b38b1\n"), nil)
	fakeExecuter.EXPECT().IoutilReadFile(nvmeFcTestHostPortPath2).Return([]byte("0x100070b7e42b38b2\n"), nil)
	fakeExecuter.EXPECT().IoutilReadFile("/sys/class/fc_host/host12/node_name").Return([]byte("0x200070b7e42b38b2\n"), nil)
}

func TestNvmeOFcEnsureLogin(t *testing.T) {
	listSubsysArgs := []string{"list-subsys"}

	t.Run("no target ports in publish context, nothing is read or run", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)

		device_connectivity.NewOsDeviceConnectivityNvmeOFc(fakeExecuter, false).EnsureLogin(map[string][]string{})
	})

	t.Run("no host FC ports, no nvme command", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
		fakeExecuter.EXPECT().FilepathGlob("/sys/class/fc_host/host*/port_name").Return([]string{}, nil)

		device_connectivity.NewOsDeviceConnectivityNvmeOFc(fakeExecuter, false).EnsureLogin(
			map[string][]string{nvmeFcTestTargetPort1: nil})
	})

	t.Run("already at target path count, skip discover and connect", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
		expectNvmeFcHostPorts(fakeExecuter)
		fakeExecuter.EXPECT().ExecuteWithTimeout(gomock.Any(), "nvme", listSubsysArgs).Return(
			[]byte(nvmeFcTestListSubsysLive), nil)

		device_connectivity.NewOsDeviceConnectivityNvmeOFc(fakeExecuter, false).EnsureLogin(
			map[string][]string{nvmeFcTestTargetPort1: nil, nvmeFcTestTargetPort2: nil})
	})

	t.Run("no live fc path, discover fails, no connect", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
		expectNvmeFcHostPorts(fakeExecuter)
		gomock.InOrder(
			fakeExecuter.EXPECT().ExecuteWithTimeout(gomock.Any(), "nvme", listSubsysArgs).Return(
				[]byte(nvmeFcTestListSubsysNoFc), nil),
			fakeExecuter.EXPECT().ExecuteWithTimeout(gomock.Any(), "nvme", []string{"discover", "--transport=fc",
				"--traddr=" + nvmeFcTestTargetPort1, "--host-traddr=" + nvmeFcTestHostPort1}).Return(
				[]byte("failed to add controller, error failed to write to nvme-fabrics device\n"), errors.New("exit status 1")),
			fakeExecuter.EXPECT().ExecuteWithTimeout(gomock.Any(), "nvme", []string{"discover", "--transport=fc",
				"--traddr=" + nvmeFcTestTargetPort1, "--host-traddr=" + nvmeFcTestHostPort2}).Return(
				[]byte("failed to add controller, error failed to write to nvme-fabrics device\n"), errors.New("exit status 1")),
			fakeExecuter.EXPECT().ExecuteWithTimeout(gomock.Any(), "nvme", listSubsysArgs).Return(
				[]byte(nvmeFcTestListSubsysNoFc), nil),
		)

		device_connectivity.NewOsDeviceConnectivityNvmeOFc(fakeExecuter, false).EnsureLogin(
			map[string][]string{nvmeFcTestTargetPort1: nil})
	})
}

func TestNvmeOFcRescanDevices(t *testing.T) {
	arrayTargetPorts := []string{nvmeFcTestTargetPort1, nvmeFcTestTargetPort2}

	t.Run("rescan only live fc controllers of the array target ports", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)

		fakeExecuter.EXPECT().FilepathGlob("/sys/class/nvme/nvme*").Return([]string{
			"/sys/class/nvme/nvme0", "/sys/class/nvme/nvme1", "/sys/class/nvme/nvme2", "/sys/class/nvme/nvme3"}, nil)
		expectRead := func(path, content string) {
			fakeExecuter.EXPECT().IoutilReadFile(path).Return([]byte(content), nil)
		}
		// live fc controller to the array: rescanned
		expectRead("/sys/class/nvme/nvme0/transport", "fc\n")
		expectRead("/sys/class/nvme/nvme0/state", "live\n")
		expectRead("/sys/class/nvme/nvme0/address", "traddr="+nvmeFcTestTargetPort1+",host_traddr="+nvmeFcTestHostPort1+"\n")
		// tcp controller: skipped after transport
		expectRead("/sys/class/nvme/nvme1/transport", "tcp\n")
		// fc controller that is not live: skipped after state
		expectRead("/sys/class/nvme/nvme2/transport", "fc\n")
		expectRead("/sys/class/nvme/nvme2/state", "connecting\n")
		// live fc controller to another array: skipped after address
		expectRead("/sys/class/nvme/nvme3/transport", "fc\n")
		expectRead("/sys/class/nvme/nvme3/state", "live\n")
		expectRead("/sys/class/nvme/nvme3/address", "traddr="+nvmeFcTestOtherArray+",host_traddr="+nvmeFcTestHostPort1+"\n")
		fakeExecuter.EXPECT().ExecuteWithTimeout(gomock.Any(), "nvme", []string{"ns-rescan", "/dev/nvme0"}).Return(nil, nil)

		err := device_connectivity.NewOsDeviceConnectivityNvmeOFc(fakeExecuter, false).RescanDevices(0, arrayTargetPorts)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("controller glob fails, no rescan and no error", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
		fakeExecuter.EXPECT().FilepathGlob("/sys/class/nvme/nvme*").Return(nil, errors.New("glob error"))

		err := device_connectivity.NewOsDeviceConnectivityNvmeOFc(fakeExecuter, false).RescanDevices(0, arrayTargetPorts)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}

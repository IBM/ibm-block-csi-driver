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

package device_connectivity

import (
	"fmt"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

type OsDeviceConnectivityNvmeOFc struct {
	Executer          executer.ExecuterInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityNvmeOFc(executer executer.ExecuterInterface, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityNvmeOFc{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, clean_scsi_device),
	}
}

func (r OsDeviceConnectivityNvmeOFc) EnsureLogin(_ map[string][]string) {
	fmt.Println("##### inside EnsureLogin")

	const maxRetries = 25
	const sleepSeconds = 5

	for i := 1; i <= maxRetries; i++ {
		out, err := r.Executer.ExecuteWithTimeout(
			int(IscsiCmdTimeout.Seconds()*1000),
			"nvme",
			[]string{
				"connect",
				"--transport=fc",
				"--traddr=nn-5005076810003F64:pn-50050768101A3F64",
				"--host-traddr=nn-2000f4e9d456d850:pn-2100f4e9d456d850",
				"--nqn=nqn.1986-03.com.ibm:nvme:2145.000002043D607F18",
			},
		)
		if err == nil {
			// Success, break out of loop
			fmt.Printf("Login successful on attempt %d\n", i)
			break
		} else {
			// Failed, log and retry after sleep
			logger.Errorf("Attempt %d: Failed to discover nvme: {%s}, error: {%s}", i, out, err)
			if i < maxRetries {
				time.Sleep(sleepSeconds * time.Second)
			} else {
				// Last attempt failed
				logger.Errorf("All %d attempts failed for NVMe discovery", maxRetries)
			}
		}

	}
}

func (r OsDeviceConnectivityNvmeOFc) RescanDevices(_ int, _ []string) error {
	fmt.Println("##### inside RescanDevices")
	return nil
}

func (r OsDeviceConnectivityNvmeOFc) GetMpathDevice(volumeId string) (string, error) {
	return r.HelperScsiGeneric.GetMpathDevice(volumeId)
}

func (r OsDeviceConnectivityNvmeOFc) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityNvmeOFc) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

func (r OsDeviceConnectivityNvmeOFc) RemoveGhostDevice(lun int) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(lun)
}

func (r OsDeviceConnectivityNvmeOFc) ValidateLun(_ int, _ []string) error {
	return nil
}

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
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

type OsDeviceConnectivityFc struct {
	Executer          executer.ExecuterInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityFc(executer executer.ExecuterInterface) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityFc{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer),
	}
}

func (r OsDeviceConnectivityFc) EnsureLogin(_ map[string][]string) {
	// FC doesn't require login
}

func (r OsDeviceConnectivityFc) RescanDevices(lunId int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RescanDevices(lunId, arrayIdentifiers)
}

func (r OsDeviceConnectivityFc) GetMpathDevice(volumeId string, lunId int, arrayIdentifiers []string) (string, error) {
	/*
			   Description:
				   1. Find all the files "/dev/disk/by-path/pci-*-fc-<WWN storage>-lun-<LUN-ID> -> ../../sd<X>
		 	          Note: <ARRAY-IP> Instead of setting here the IP we just search for * on that.
		 	       2. Get the sd<X> devices.
		 	       3. Search '/sys/block/dm-*\/slaves/*' and get the <DM device name>. For example dm-3 below:
		 	          /sys/block/dm-3/slaves/sdb -> ../../../../pci0000:00/0000:00:17.0/0000:13:00.0/host33/rport-33:0-3/target33:0:1/33:0:1:0/block/sdb

			   Return Value: "dm-X" of the volumeID by using the LunID and the array wwn.
	*/
	return r.HelperScsiGeneric.GetMpathDevice(volumeId, lunId, arrayIdentifiers, ConnectionTypeFC)
}

func (r OsDeviceConnectivityFc) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityFc) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

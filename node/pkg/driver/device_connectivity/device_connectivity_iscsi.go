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
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"os/exec"
)

type OsDeviceConnectivityIscsi struct {
	Executer          executer.ExecuterInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityIscsi(executer executer.ExecuterInterface) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityIscsi{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer),
	}
}

func (r OsDeviceConnectivityIscsi) iscsiDiscoverAndLogin(iqn, ip string) error {
	logger.Debugf("iscsiDiscoverAndLogin: iqn {%s}, ip {%s}", iqn, ip)
	_, err := exec.Command("iscsiadm", "-m", "discovery", "-t", "sendtargets", "-p", ip).CombinedOutput()
	if err != nil {
		logger.Error(err)
		return err
	}

	_, err = exec.Command("iscsiadm", "-m", "node", "-p", ip+":3260", "-T", iqn, "--login").CombinedOutput()
	if err != nil {
		logger.Error(err)
		return err
	}
	return nil
}

func (r OsDeviceConnectivityIscsi) EnsureLogin(ipsByArrayIdentifier map[string][]string) error {
	isAnyLoginSucceeded := false
	var err error
	for arrayIdentifier, ips := range ipsByArrayIdentifier {
		for _, ip := range ips {
			err = r.iscsiDiscoverAndLogin(arrayIdentifier, ip)
			if err == nil {
				isAnyLoginSucceeded = true
			}
		}
	}
	if !isAnyLoginSucceeded {
		return err
	}
	return nil
}

func (r OsDeviceConnectivityIscsi) RescanDevices(lunId int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RescanDevices(lunId, arrayIdentifiers)
}

func (r OsDeviceConnectivityIscsi) GetMpathDevice(volumeId string, lunId int, arrayIdentifiers []string) (string, error) {
	/*
			   Description:
				   1. Find all the files "/dev/disk/by-path/ip-<ARRAY-IP>-iscsi-<IQN storage>-lun-<LUN-ID> -> ../../sd<X>
		 	          Note: <ARRAY-IP> Instead of setting here the IP we just search for * on that.
		 	       2. Get the sd<X> devices.
		 	       3. Search '/sys/block/dm-*\/slaves/*' and get the <DM device name>. For example dm-3 below:
		 	          /sys/block/dm-3/slaves/sdb -> ../../../../platform/host41/session9/target41:0:0/41:0:0:13/block/sdb

			   Return Value: "dm-X" of the volumeID by using the LunID and the arrayIqn.
	*/
	return r.HelperScsiGeneric.GetMpathDevice(volumeId, lunId, arrayIdentifiers, "iscsi")
}

func (r OsDeviceConnectivityIscsi) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityIscsi) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

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
	"time"
)

const (
	iscsiCmdTimeout = 30 * time.Second
	iscsiPort       = 3260
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

func (r OsDeviceConnectivityIscsi) iscsiCmd(args ...string) (string, error) {
	out, err := r.Executer.ExecuteWithTimeout(int(iscsiCmdTimeout.Seconds()), "iscsiadm", args)
	return string(out), err
}

func (r OsDeviceConnectivityIscsi) iscsiDiscoverAndLogin(targetName, portal string) error {
	logger.Debugf("iscsiDiscoverAndLogin: target: {%s}, portal: {%s}", portal, portal)
	output, err := r.iscsiCmd("-m", "discoverydb", "-t", "sendtargets", "-p", portal, "--discover")
	if err != nil {
		logger.Errorf("Failed to discover iSCSI: {%s}, error: {%s}", output, err)
		return err
	}

	portalWithPort := portal + ":" + string(iscsiPort)
	output, err = r.iscsiCmd("-m", "node", "-p", portalWithPort, "-T", targetName, "--login")
	if err != nil {
		logger.Errorf("Failed to login iSCSI: {%s}, error: {%s}", output, err)
		return err
	}
	return nil
}

func (r OsDeviceConnectivityIscsi) EnsureLogin(portalsByTarget map[string][]string) error {
	isAnyLoginSucceeded := false
	var err error
	for targetName, portals := range portalsByTarget {
		for _, portal := range portals {
			err = r.iscsiDiscoverAndLogin(targetName, portal)
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

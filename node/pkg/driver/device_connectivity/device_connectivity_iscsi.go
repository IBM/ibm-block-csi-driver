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
	"errors"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	iscsiCmdTimeout     = 30 * time.Second
	iscsiPort           = 3260
	iSCSIErrNoObjsFound = 21
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
	out, err := r.Executer.ExecuteWithTimeout(int(iscsiCmdTimeout.Seconds()*1000), "iscsiadm", args)
	return string(out), err
}

func (r OsDeviceConnectivityIscsi) iscsiDiscover(portal string) error {
	output, err := r.iscsiCmd("-m", "discoverydb", "-t", "sendtargets", "-p", portal, "--discover")
	if err != nil {
		logger.Errorf("Failed to discover iSCSI: {%s}, error: {%s}", output, err)
		return err
	}
	return nil
}

func (r OsDeviceConnectivityIscsi) iscsiLogin(targetName, portal string) {
	portalWithPort := portal + ":" + strconv.Itoa(iscsiPort)
	output, err := r.iscsiCmd("-m", "node", "-p", portalWithPort, "-T", targetName, "--login")
	if err != nil {
		logger.Errorf("Failed to login iSCSI: {%s}, error: {%s}", output, err)
	}
}

func (r OsDeviceConnectivityIscsi) iscsiGetRawSessions() ([]string, error) {
	output, err := r.iscsiCmd("-m", "session")
	if err != nil {
		if exitError, isExitError := err.(*exec.ExitError); isExitError && exitError.ExitCode() == iSCSIErrNoObjsFound {
			logger.Debug("No active iSCSI sessions.")
			return []string{}, nil
		}
		logger.Error("Failed to check iSCSI sessions.")
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return lines, nil
}

func (r OsDeviceConnectivityIscsi) getAllSessions() (map[string]map[string]bool, error) {
	lines, err := r.iscsiGetRawSessions()
	if err != nil {
		return nil, err
	}
	parseErr := errors.New("failed to parse iSCSI sessions")
	portalsByTarget := make(map[string]map[string]bool)
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 4 {
			return nil, parseErr
		}
		portalInfo, targetName := parts[2], parts[3]
		ipPortSeparatorIndex := strings.LastIndex(portalInfo, ":")
		if ipPortSeparatorIndex < 0 {
			return nil, parseErr
		}
		ip := portalInfo[:ipPortSeparatorIndex]
		if set := portalsByTarget[targetName]; set == nil {
			portalsByTarget[targetName] = make(map[string]bool)
		}
		portalsByTarget[targetName][ip] = true
	}
	return portalsByTarget, nil
}

func (r OsDeviceConnectivityIscsi) filterLoggedIn(portalsByTarget map[string][]string) (map[string][]string, error) {
	loggedInPortalsByTarget, err := r.getAllSessions()
	if err != nil {
		return nil, err
	}
	filteredPortalsByTarget := make(map[string][]string)
	for targetName, portals := range portalsByTarget {
		for _, portal := range portals {
			if !loggedInPortalsByTarget[targetName][portal] {
				portals := filteredPortalsByTarget[targetName]
				filteredPortalsByTarget[targetName] = append(portals, portal)
			}
		}
	}
	return filteredPortalsByTarget, nil
}

func (r OsDeviceConnectivityIscsi) iscsiDiscoverAny(portals []string) bool {
	for _, portal := range portals {
		if err := r.iscsiDiscover(portal); err == nil {
			return true
		}
	}
	return false
}

func (r OsDeviceConnectivityIscsi) discoverAndLogin(portalsByTarget map[string][]string) {
	for targetName, portals := range portalsByTarget {
		if ok := r.iscsiDiscoverAny(portals); ok {
			for _, portal := range portals {
				r.iscsiLogin(targetName, portal)
			}
		}
	}
}

func (r OsDeviceConnectivityIscsi) EnsureLogin(allPortalsByTarget map[string][]string) {
	portalsByTarget, err := r.filterLoggedIn(allPortalsByTarget)
	if err == nil {
		r.discoverAndLogin(portalsByTarget)
	} else {
		logger.Errorf("Failed to filter logged in iSCSI portals: {%v}", err)
	}
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
	return r.HelperScsiGeneric.GetMpathDevice(volumeId, lunId, arrayIdentifiers, ConnectionTypeISCSI)
}

func (r OsDeviceConnectivityIscsi) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityIscsi) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

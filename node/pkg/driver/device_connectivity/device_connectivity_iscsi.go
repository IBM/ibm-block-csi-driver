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
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

const (
	IscsiCmdTimeout     = 30 * time.Second
	iscsiPort           = 3260
	ISCSIErrNoObjsFound = 21
)

type OsDeviceConnectivityIscsi struct {
	Executer          executer.ExecuterInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityIscsi(executer executer.ExecuterInterface, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityIscsi{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, clean_scsi_device),
	}
}

func (r OsDeviceConnectivityIscsi) iscsiCmd(args ...string) (string, error) {
	out, err := r.Executer.ExecuteWithTimeout(int(IscsiCmdTimeout.Seconds()*1000), "iscsiadm", args)
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
		if exitCode, isExitError := r.Executer.GetExitCode(err); isExitError && exitCode == ISCSIErrNoObjsFound {
			logger.Debug("No active iSCSI sessions")
			return []string{}, nil
		}
		logger.Error("Failed to check iSCSI sessions")
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

var (
	// Host Number line
	hostLineRE = regexp.MustCompile(`Host Number:\s*(\d+)`)

	// Source IQN line – this is the *initiator* IQN, not the target
	sourceIQNLineRE = regexp.MustCompile(`^Initiator:\s*(iqn\..+)$`)
)

// activeSession holds the data we need for one host
type activeSession struct {
	num       int    // host number (0,1,2,…)
	sourceIQN string // initiator IQN
}

// parseActiveSessions returns a slice of all active sessions (host number + source IQN)
func (r OsDeviceConnectivityIscsi) parseActiveSessions() ([]activeSession, error) {
	out, err := r.iscsiCmd("-m", "session", "-P", "3")

	if err != nil {
		//if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 21 {
		//	return nil, nil // no sessions
		//}
		return nil, fmt.Errorf("iscsiadm failed: %w", err)
	}

	var sessions []activeSession
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	var curNum = -1

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// ---- Host Number ----
		if m := hostLineRE.FindStringSubmatch(line); m != nil {
			num, _ := strconv.Atoi(m[1])
			curNum = num
			continue
		}

		// ---- Source IQN (Initiator) ----
		if curNum != -1 {
			if m := sourceIQNLineRE.FindStringSubmatch(line); m != nil {
				sessions = append(sessions, activeSession{
					num:       curNum,
					sourceIQN: m[1],
				})
				curNum = -1 // reset for next block
			}
		}
	}
	return sessions, scanner.Err()
}

// updateHostIDs adds new active hosts that share the *source* IQN with any known host
func (r OsDeviceConnectivityIscsi) updateHostIDs(hostIDs map[int]bool) {
	// 1. Get all active sessions
	active, err := r.parseActiveSessions()
	if err != nil {
		logger.Errorf("Failed to parse iSCSI sessions: {%s}", err)
		return
	}
	if len(active) == 0 {
		logger.Error("No active iSCSI sessions.")
		return
	}

	// 2. Build sourceIQN → list of known host numbers
	iqnToKnown := make(map[string][]int)
	for num := range hostIDs {
		// Find source IQN for this known host (must exist in active list)
		for _, s := range active {
			if s.num == num {
				iqnToKnown[s.sourceIQN] = append(iqnToKnown[s.sourceIQN], num)
				break
			}
		}
	}

	// 3. Walk through every active session
	for _, s := range active {
		if hostIDs[s.num] {
			continue // already known
		}

		// If this source IQN is used by any known host → add it
		if knownHosts, found := iqnToKnown[s.sourceIQN]; found && len(knownHosts) > 0 {
			hostIDs[s.num] = true
			iqnToKnown[s.sourceIQN] = append(iqnToKnown[s.sourceIQN], s.num)
			logger.Debugf("Added host%d (source IQN: %s) – matches known hosts: %v\n",
				s.num, s.sourceIQN, knownHosts)
		}
	}
}

func (r OsDeviceConnectivityIscsi) RescanDevices(lunId int, arrayIdentifiers []string) error {
	hostIDs, err := r.HelperScsiGeneric.RescanDevicesGetHostIds(lunId, arrayIdentifiers)
	if err != nil {
		return err
	}
	r.updateHostIDs(hostIDs)
	return r.HelperScsiGeneric.RescanDevices(lunId, arrayIdentifiers, hostIDs)
}

func (r OsDeviceConnectivityIscsi) GetMpathDevice(volumeId string) (string, error) {
	/*
	   Return Value: "dm-X" of the volumeID.
	*/
	return r.HelperScsiGeneric.GetMpathDevice(volumeId)
}

func (r OsDeviceConnectivityIscsi) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityIscsi) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

func (r OsDeviceConnectivityIscsi) RemoveGhostDevice(lun int) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(lun)
}

func (r OsDeviceConnectivityIscsi) ValidateLun(lun int, sysDevices []string) error {
	return r.HelperScsiGeneric.ValidateLun(lun, sysDevices)
}

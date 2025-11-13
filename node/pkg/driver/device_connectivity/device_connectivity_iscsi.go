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
	"os"
	"path/filepath"
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

// iscsiGetRawSessions now reads from /sys/class/iscsi_session
// It returns lines in the format: "tcp: [1] 192.168.1.100:3260,1 iqn.target.name"
// matching the output of `iscsiadm -m session`
func (r OsDeviceConnectivityIscsi) iscsiGetRawSessions() ([]string, error) {
	sysPath := "/sys/class/iscsi_session"
	if _, err := os.Stat(sysPath); os.IsNotExist(err) {
		return []string{}, nil
	}

	sessions, err := os.ReadDir(sysPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", sysPath, err)
	}

	var results []string
	for _, s := range sessions {
		sessionPath := filepath.Join(sysPath, s.Name())

		// Get Target Name
		targetName, err := os.ReadFile(filepath.Join(sessionPath, "targetname"))
		if err != nil {
			continue
		}

		// Get IP Address from the connection subdirectory (usually connection1:0 or similar)
		connDirs, err := os.ReadDir(sessionPath)
		if err != nil {
			continue
		}

		for _, c := range connDirs {
			if strings.HasPrefix(c.Name(), "connection") {
				addr, err := os.ReadFile(filepath.Join(sessionPath, c.Name(), "address"))
				if err != nil {
					continue
				}

				// Reconstruct the iscsiadm output format:
				// "transport: [sid] ip:port,tpgt targetname"
				// Note: getAllSessions only relies on parts[2] (ip:port) and parts[3] (targetname)
				line := fmt.Sprintf("tcp: [0] %s:3260,1 %s",
					strings.TrimSpace(string(addr)),
					strings.TrimSpace(string(targetName)))
				results = append(results, line)
				break // Usually one connection per session
			}
		}
	}

	return results, nil
}

func (r OsDeviceConnectivityIscsi) getAllSessions() (map[string]map[string]bool, error) {
	lines, err := r.iscsiGetRawSessions()
	if err != nil {
		return nil, err
	}

	portalsByTarget := make(map[string]map[string]bool)
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		portalInfo, targetName := parts[2], parts[3]

		// Extract IP from ip:port
		ipPortSeparatorIndex := strings.LastIndex(portalInfo, ":")
		if ipPortSeparatorIndex < 0 {
			continue
		}
		ip := portalInfo[:ipPortSeparatorIndex]

		if portalsByTarget[targetName] == nil {
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
		// Ensure targetName is compared consistently (lower case)
		normalizedTarget := strings.ToLower(targetName)

		for _, portal := range portals {
			// Normalize the portal string (lowercase for IPv6, trim whitespace)
			normalizedPortal := strings.ToLower(strings.TrimSpace(portal))

			// Check against the active sessions
			if !loggedInPortalsByTarget[normalizedTarget][normalizedPortal] {
				// This portal is NOT logged in. Add it to the work list.
				filteredPortalsByTarget[targetName] = append(filteredPortalsByTarget[targetName], portal)
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

func (r OsDeviceConnectivityIscsi) parseActiveSessions() ([]activeSession, error) {
    var sessions []activeSession
    sessionBaseDir := "/sys/class/iscsi_session"

    entries, err := os.ReadDir(sessionBaseDir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, fmt.Errorf("failed to read iSCSI sessions from sysfs: %w", err)
    }

    for _, entry := range entries {
        sessionPath := filepath.Join(sessionBaseDir, entry.Name())

        // 1. Resolve the Host Name via symlink (e.g., "host4")
        // This is much faster than ReadDir and provides the host ID immediately.
		// Ensure we handle potential relative links and missing devices
		linkTarget, err := os.Readlink(filepath.Join(sessionPath, "device"))
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Warnf("Could not read device link for %s: %v", entry.Name(), err)
			}
			continue
		}

		// filepath.Base is safe here to get the 'hostX' string
		hostName := filepath.Base(linkTarget)
		if !strings.HasPrefix(hostName, "host") {
			logger.Debugf("Skipping non-host device: %s", hostName)
			continue
		}

        // 2. Extract Host Number
        hostNum, err := strconv.Atoi(strings.TrimPrefix(hostName, "host"))
        if err != nil {
            continue
        }

        // 3. Get Initiator IQN (Passing hostName avoids redundant lookups)
        initiatorIQN, err := r.getInitiatorIQN(sessionPath, hostName)
        if err != nil {
            logger.Errorf("Failed to get initiator for %s: %s", entry.Name(), err)
            continue
        }

        sessions = append(sessions, activeSession{
            sourceIQN: initiatorIQN,
            num:       hostNum,
        })
    }

    return sessions, nil
}

func (r OsDeviceConnectivityIscsi) getInitiatorIQN(sessionPath, hostName string) (string, error) {
    // 1. Primary: Session-specific IQN
    if data, err := os.ReadFile(filepath.Join(sessionPath, "initiatorname")); err == nil {
        return strings.TrimSpace(string(data)), nil
    }

    // 2. Fallback: Host-specific IQN
    hostInitPath := fmt.Sprintf("/sys/class/iscsi_host/%s/initiatorname", hostName)
    if data, err := os.ReadFile(hostInitPath); err == nil {
        return strings.TrimSpace(string(data)), nil
    }

    // 3. Final Fallback: Global Config with Robust Parsing
    if data, err := os.ReadFile("/etc/iscsi/initiatorname.iscsi"); err == nil {
        for _, line := range strings.Split(string(data), "\n") {
            line = strings.TrimSpace(line)

            // Skip comments and empty lines
            if line == "" || strings.HasPrefix(line, "#") {
                continue
            }

            // Split on the first '=' to handle potential spaces: "InitiatorName = iqn..."
            parts := strings.SplitN(line, "=", 2)
            if len(parts) == 2 {
                key := strings.TrimSpace(parts[0])
                value := strings.TrimSpace(parts[1])

                // Case-insensitive key check
                if strings.EqualFold(key, "InitiatorName") {
                    return value, nil
                }
            }
        }
    }

    return "", fmt.Errorf("initiator IQN not found")
}

// updateHostIDs adds new active hosts that share the *source* IQN with any known host
func (r OsDeviceConnectivityIscsi) updateHostIDs(hostIDs map[int]bool) {
    active, err := r.parseActiveSessions()
	if err != nil {
		logger.Errorf("Failed to parse iSCSI sessions: {%s}", err)
		return
	}
	if len(active) == 0 {
		logger.Error("No active iSCSI sessions.")
		return
	}

    // Map host number to IQN for quick lookup
    numToIqn := make(map[int]string)
    // Track which IQNs are associated with "known" host IDs
    knownIqns := make(map[string]bool)

    for _, s := range active {
        numToIqn[s.num] = s.sourceIQN
        if hostIDs[s.num] {
            knownIqns[s.sourceIQN] = true
        }
    }

    // Add any active session that shares an IQN with a known host
    for _, s := range active {
        if !hostIDs[s.num] && knownIqns[s.sourceIQN] {
            hostIDs[s.num] = true
            logger.Debugf("Added host%d; shares IQN %s with known hosts", s.num, s.sourceIQN)
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

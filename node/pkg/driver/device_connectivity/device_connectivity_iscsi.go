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
	logger.Infof("Performing iSCSI discovery for portal: %s", portal)

	// Adding --op=update ensures that if the TPGT or other parameters 
	// changed on the array, the local Open-iSCSI DB is refreshed.
	output, err := r.iscsiCmd("-m", "discoverydb", "-t", "sendtargets", "-p", portal, "--discover", "--op=update")
	if err != nil {
		logger.Errorf("Failed to discover iSCSI: {%s}, error: {%s}", output, err)
		return err
	}
	return nil
}

func (r OsDeviceConnectivityIscsi) iscsiLogin(targetName, portal string) {
	// portal is already normalized to "host:port" via r.normalizePortal()
	output, err := r.iscsiCmd("-m", "node", "-p", portal, "-T", targetName, "--login")
	
	if err != nil {
		if exitCode, isExitError := r.Executer.GetExitCode(err); isExitError {
			// 15 = Already logged in. This is success for CSI.
			if exitCode == 15 {
				logger.Debugf("iSCSI session for %s (%s) already active", targetName, portal)
				return
			}
			
			// 24 = Login failed but session exists (often happens during transient SVC failovers)
			if exitCode == 24 {
				logger.Warnf("iSCSI session exists but login failed for %s. Multipath will handle recovery.", portal)
				return
			}
		}

		// Real error: log it so we can debug fabric/auth issues
		logger.Errorf("Failed to login iSCSI target %s via %s: %s (err: %v)", targetName, portal, output, err)
	}
}

// iscsiGetRawSessions now reads from /sys/class/iscsi_session
// It returns lines in the format: "tcp: [1] 192.168.1.100:3260,1 iqn.target.name"
// matching the output of `iscsiadm -m session`
func (r OsDeviceConnectivityIscsi) iscsiGetRawSessions() ([]string, error) {
	sysPath := "/sys/class/iscsi_session"
	sessions, err := os.ReadDir(sysPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", sysPath, err)
	}

	var results []string
	for _, s := range sessions {
		// Example: /sys/class/iscsi_session/session1
		if !strings.HasPrefix(s.Name(), "session") {
			continue
		}
		sessionID := strings.TrimPrefix(s.Name(), "session")
		sessionPath := filepath.Join(sysPath, s.Name())

		// 1. Verify State (Non-blocking)
		if strings.TrimSpace(string(stateBuf)) != "LOGGED_IN" {
			continue
		}
		
		if stateStr != "LOGGED_IN" {
			logger.Warnf("Session %s is in %s", sessionID, stateStr)
			// Ignore other transient/failed states (REOPENING, FREE, etc.)
			continue
		}

		// 2. Get Target IQN
		tnBuf, err := os.ReadFile(filepath.Join(sessionPath, "targetname"))
		if err != nil {
			continue
		}
		targetName := strings.TrimSpace(string(tnBuf))

		// 3. Find Connection Portal (Address + Port)
		// Usually /sys/class/iscsi_session/session1/device/connection1:0/iscsi_connection/connection1:0/
		// But your shorter path sessionPath/connectionX/ is often symlinked correctly.
		connDirs, _ := os.ReadDir(sessionPath)
		for _, c := range connDirs {
			if strings.HasPrefix(c.Name(), "connection") {
				connPath := filepath.Join(sessionPath, c.Name())
				addrBuf, errA := os.ReadFile(filepath.Join(connPath, "address"))
				portBuf, errP := os.ReadFile(filepath.Join(connPath, "port"))
				
				if errA == nil && errP == nil {
					portal := net.JoinHostPort(
						strings.TrimSpace(string(addrBuf)),
						strings.TrimSpace(string(portBuf)),
					)
					// Matches iscsiadm format exactly for downstream parsers
					results = append(results, fmt.Sprintf("tcp: [%s] %s %s", sessionID, portal, targetName))
					break // Found the primary portal for this session
				}
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
		// Native/iscsiadm format: "tcp: [id] 1.2.3.4:3260 iqn.2026-01.com.example:target"
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}

		// Check for "tcp" as validity in case we switch back to using iscsiadm
		if !strings.HasPrefix(parts[0], "tcp") {
			continue
		}

		// parts[2] = "ip:port", parts[3] = "iqn..."
		portalInfo := parts[2]
		targetName := parts[3]

		if _, exists := portalsByTarget[targetName]; !exists {
			portalsByTarget[targetName] = make(map[string]bool)
		}
		
		portalsByTarget[targetName][portalInfo] = true
	}
	return portalsByTarget, nil
}



func (r OsDeviceConnectivityIscsi) filterLoggedIn(portalsByTarget map[string][]string) (map[string][]string, error) {
	// 1. Get current state from sysfs
	loggedInPortalsByTarget, err := r.getAllSessions()
	if err != nil {
		return nil, err
	}

	filteredPortalsByTarget := make(map[string][]string)

	for targetName, portals := range portalsByTarget {
		// IQNs are technically case-insensitive in the iSCSI spec, 
		// but Linux sysfs and iscsiadm usually present them as lowercase.
		normalizedTarget := strings.ToLower(targetName)

		for _, portal := range portals {
			// 2. Normalize the portal (IP:Port)
			// This handles "1.2.3.4", "1.2.3.4:3260", and "[2001:db8::1]:3260"
			host, port, err := net.SplitHostPort(strings.TrimSpace(portal))
			if err != nil {
				host = strings.TrimSpace(portal)
				port = "3260"
			}
			normalizedPortal := net.JoinHostPort(strings.ToLower(host), port)

			// 3. Check if this specific path is missing
			activePortals, exists := loggedInPortalsByTarget[normalizedTarget]
			if !exists || !activePortals[normalizedPortal] {
				// Portal is NOT logged in. Add it to the list for action.
				filteredPortalsByTarget[targetName] = append(filteredPortalsByTarget[targetName], portal)
			}
		}
	}
	return filteredPortalsByTarget, nil
}



func (r OsDeviceConnectivityIscsi) discoverAndLogin(portalsByTarget map[string][]string) {
	// 1. Surgical Scan: Only load the DB entries for the targets we actually care about
	dbCache := r.loadRelevantTargets(portalsByTarget)

	discoveredPortals := make(map[string]bool)

	for targetName, requestedPortals := range portalsByTarget {
		for _, portal := range requestedPortals {
			normPortal := r.normalizePortal(portal)
			
			// If this specific portal isn't in the DB for this target, we must discover
			if !dbCache[targetName][normPortal] {
				if !discoveredPortals[normPortal] {
					logger.Infof("Target %s portal %s missing from DB, discovering...", targetName, normPortal)
					if err := r.iscsiDiscover(normPortal); err == nil {
						discoveredPortals[normPortal] = true
						
						// FIX: Immediately update the DB cache. 
						// This ensures that if the same target is encountered again 
						// (or if multiple targets are discovered via one portal), 
						// the cache reflects the current system state.
						if dbCache[targetName] == nil {
							dbCache[targetName] = make(map[string]bool)
						}
						dbCache[targetName][normPortal] = true
					}
				}
			}
		}
	}

	// 2. Perform Logins (using our earlier 'exit 15' safe login)
	for targetName, portals := range portalsByTarget {
		for _, portal := range portals {
			_ = r.iscsiLogin(targetName, portal)
		}
	}
}


// loadRelevantTargets only probes the specific subdirectories for the targets in the request
func (r OsDeviceConnectivityIscsi) loadRelevantTargets(requestedTargets map[string][]string) map[string]map[string]bool {
	db := make(map[string]map[string]bool)
	basePath := "/etc/iscsi/nodes"

	for targetName := range requestedTargets {
		targetPath := filepath.Join(basePath, targetName)
		
		db[targetName] = make(map[string]bool)

		// Attempt to read the specific target directory
		portals, err := os.ReadDir(targetPath)
		if err != nil {
			// Directory doesn't exist; target unknown to DB
			continue
		}

		for _, p := range portals {
			if !p.IsDir() { continue }
			
			// Open-iSCSI directory name format: "IP,Port,TPGT" 
			// IPv4 Example: "192.168.1.10,3260,1"
			// IPv6 Example: "2001:db8::1,3260,1"
			// Using strings.Split by comma is safe because colons in IPv6 won't conflict.
			parts := strings.Split(p.Name(), ",")
			if len(parts) >= 2 {
				// 1. Get raw IP and Port from the directory name
				rawIP := parts[0]
				rawPort := parts[1]

				// 2. net.JoinHostPort correctly wraps IPv6 (parts[0]) in brackets 
				// if it detects colons, resulting in "[2001:db8::1]:3260"
				hostPort := net.JoinHostPort(rawIP, rawPort)

				// 3. Normalize to ensure consistent casing and bracket formatting
				// to match the format used in your filterLoggedIn logic.
				norm := r.normalizePortal(hostPort)
				db[targetName][norm] = true
			}
		}
	}
	return db
}



func (r OsDeviceConnectivityIscsi) normalizePortal(portal string) string {
	// 1. Clean basics
	portal = strings.ToLower(strings.TrimSpace(portal))

	// 2. Split into Host and Port
	host, port, err := net.SplitHostPort(portal)
	if err != nil {
		// If SplitHostPort fails, it's likely a raw IP/hostname without a port
		// We assume the default iSCSI port 3260
		host = portal
		port = "3260"
	}

	// 3. Normalize Host (IP)
	// This handles IPv6 cases like converting "2001:DB8::1" to "2001:db8::1"
	// and stripping brackets from [2001:db8::1]
	ip := net.ParseIP(host)
	if ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}

	// 4. Standardized Re-join
	// This ensures IPv6 hosts are wrapped in brackets only if necessary
	// and that the port is always present.
	return net.JoinHostPort(host, port)
}


func (r OsDeviceConnectivityIscsi) EnsureLogin(allPortalsByTarget map[string][]string) {
	portalsByTarget, err := r.filterLoggedIn(allPortalsByTarget)
	if err == nil {
		if len(portalsByTarget) == 0 {
			logger.Debug("All iSCSI portals are already logged in.")
			return
		}
		r.discoverAndLogin(portalsByTarget)
	} else {
		logger.Errorf("Failed to filter logged in iSCSI portals: {%v}", err)
	}
}

func (r cleanSysfsData) {
	return strings.Trim(string(data), " \n\r\t\x00")
}

func (r OsDeviceConnectivityIscsi) parseActiveSessions() ([]activeSession, error) {
    sessionBaseDir := "/sys/class/iscsi_session"
    entries, err := os.ReadDir(sessionBaseDir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, fmt.Errorf("failed to read iSCSI sessions from sysfs: %w", err)
    }

    var sessions []activeSession
    for _, entry := range entries {
        sessionPath := filepath.Join(sessionBaseDir, entry.Name())
        
        // 1. STATE CHECK
        stateBuf, _ := os.ReadFile(filepath.Join(sessionPath, "state"))
        if cleanSysfsData(stateBuf) != "LOGGED_IN" {
            continue
        }

        // 2. HOST RESOLUTION
        // device link usually points to /sys/devices/platform/.../hostX/sessionY
        // Resolve the Host Name via symlink (e.g., "host4")
        // This is much faster than ReadDir and provides the host ID immediately.
		// Ensure we handle potential relative links and missing devices
        linkTarget, err := os.Readlink(filepath.Join(sessionPath, "device"))
        if err != nil {
			if !os.IsNotExist(err) {
				logger.Warnf("Could not read device link for %s: %v", entry.Name(), err)
			}
            continue
        }

        hostName := ""
        for _, part := range strings.Split(linkTarget, "/") {
            if strings.HasPrefix(part, "host") {
                hostName = part
                break
            }
        }
		if hostName == "" {
			logger.Debugf("Could not find host prefix in path: %s", linkTarget)
			continue
		}

        hostNum, err := strconv.Atoi(strings.TrimPrefix(hostName, "host"))
		if err != nil {
			logger.Debugf("Invalid host format: %s", hostName)
			continue
		}

        // 3. IQN EXTRACTION (With sanitization)
        initiatorIQN, err := r.getInitiatorIQN(sessionPath, hostName)
        if err != nil {
            logger.Debugf("Skipping session %s: %v", entry.Name(), err)
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


func (r OsDeviceConnectivityIscsi) updateHostIDs(hostIDs map[int]bool) {
	active, err := r.parseActiveSessions()
	if err != nil {
		logger.Errorf("Failed to parse iSCSI sessions: %v", err)
		return
	}
	if len(active) == 0 {
		logger.Info("No active iSCSI sessions.")
		return
	}

	// 1. Identify which Initiator IQNs belong to the hosts we already care about
	knownIqns := make(map[string]bool)
	for _, s := range active {
		if hostIDs[s.num] {
			knownIqns[strings.ToLower(s.sourceIQN)] = true
		}
	}

	// 2. Map all other hosts that use the same Initiator IQN
	// This captures secondary NICs/Paths for the same volume
	for _, s := range active {
		iqn := strings.ToLower(s.sourceIQN)
		if knownIqns[iqn] && !hostIDs[s.num] {
			hostIDs[s.num] = true
			logger.Infof("Multipath discovery: host%d associated with known initiator %s", s.num, iqn)
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

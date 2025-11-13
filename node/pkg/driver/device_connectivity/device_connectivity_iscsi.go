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

func (r OsDeviceConnectivityIscsi) iscsiDiscover(portal string) error {
	logger.Infof("Performing iSCSI discovery for portal: %s", portal)
	
	args := []string{"-m", "discoverydb", "-t", "sendtargets", "-p", portal, "--discover"}
	
	// Use a 10MB limit for discovery as discussed (it's an 'Inventory' operation)
	output, err := r.executer.ExecuteWithSafeBuffer(20000, 10*1024*1024, "iscsiadm", args...)
	if err != nil {
		logger.Errorf("Failed to discover iSCSI: %s, error: %v", string(output), err)
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

		state, _ := os.ReadFile(filepath.Join(sessionPath, "state"))
		if strings.TrimSpace(string(state)) != "LOGGED_IN" {
			continue
		}

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
		
		// We can discard the "session" prefix from s.Name to get the session id instead of 0 below (currently unsuable)

		for _, c := range connDirs {
			if c.IsDir() && strings.HasPrefix(c.Name(), "connection") {
				addr, err := os.ReadFile(filepath.Join(sessionPath, c.Name(), "address"))
				if err != nil {
					continue
				}

				port, err := os.ReadFile(filepath.Join(sessionPath, c.Name(), "port"))
				if err != nil { continue }
				
				// Use JoinHostPort to handle IPv6 brackets automatically and correctly
				portal := net.JoinHostPort(
					strings.TrimSpace(string(addr)),
					strings.TrimSpace(string(port)),
				)
				
				// Format: "ignore ignore <portal> <target>"
				// Format matching the parser's expectation - iscsiadm output format: "tcp: [id] ip:port targetname"
				line := fmt.Sprintf("tcp: [0] %s %s", portal, strings.TrimSpace(string(targetName)))
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

		if portalsByTarget[targetName] == nil {
			portalsByTarget[targetName] = make(map[string]bool)
		}
		portalsByTarget[targetName][portalInfo] = true
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
			//Normalize the input portal to handle IPv6 brackets/case
			// If input is "2001:DB8::1:3260", we want "2001:db8::1:3260"
			host, port, err := net.SplitHostPort(strings.TrimSpace(portal))
			if err != nil {
				// Fallback if portal is just an IP
				host = strings.TrimSpace(portal)
				port = "3260"
			}
			normalizedPortal := net.JoinHostPort(strings.ToLower(host), port)

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


func (r OsDeviceConnectivityIscsi) iscsiDiscoverAny(portals []string, targetName string) bool {
    dbPortals := r.getStoredPortalsForTarget(targetName)

    // Check if the node database is missing portals that the controller knows about
    needsUpdate := false
    for _, p := range portals {
        if !contains(dbPortals, p) {
            needsUpdate = true
            break
        }
    }

    if !needsUpdate {
        logger.Debugf("Target %s is fully discovered, skipping discovery", targetName)
        return true
    }

    // Perform an UPDATE discovery (safer than a full fresh discovery)
    for _, portal := range portals {
        if err := r.iscsiUpdateDiscovery(portal); err == nil {
            return true
        }
    }
    return false
}

func (r OsDeviceConnectivityIscsi) iscsiUpdateDiscovery(portal string) error {
    // --op update ensures we only add NEW portals found at this discovery address
    args := []string{"-m", "discoverydb", "-t", "st", "-p", portal, "--discover", "--op", "update"}
    _, err := r.executer.ExecuteWithSafeBuffer(20000, 10*1024*1024, "iscsiadm", args...)
    return err
}


func (r OsDeviceConnectivityIscsi) getStoredPortalsForTarget(targetName string) []string {
	// 1. Locate the target directory in the iSCSI DB
	targetPath := filepath.Join("/etc/iscsi/nodes", targetName)
	
	files, err := os.ReadDir(targetPath)
	if err != nil {
		// If directory doesn't exist, we have 0 stored portals
		return []string{}
	}

	var storedPortals []string
	for _, f := range files {
		if !f.IsDir() {
			continue
		}

		// 2. The directory name is usually "IP,PORT,TPGT" (e.g., "192.168.1.10,3260,1")
		// We need to extract the "IP:PORT" part for comparison.
		parts := strings.Split(f.Name(), ",")
		if len(parts) >= 2 {
			portal := net.JoinHostPort(parts[0], parts[1])
			storedPortals = append(storedPortals, portal)
		}
	}

	return storedPortals
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
		
		// Attempt to read the specific target directory
		portals, err := os.ReadDir(targetPath)
		if err != nil {
			// Directory doesn't exist; target unknown to DB
			db[targetName] = make(map[string]bool)
			continue
		}

		db[targetName] = make(map[string]bool)
		for _, p := range portals {
			if !p.IsDir() { continue }
			
			// Open-iSCSI format: "192.168.1.10,3260,1"
			parts := strings.Split(p.Name(), ",")
			if len(parts) >= 2 {
				norm := net.JoinHostPort(parts[0], parts[1])
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
	}

	// 4. Standardized Re-join
	// This ensures IPv6 hosts are wrapped in brackets only if necessary
	// and that the port is always present.
	return net.JoinHostPort(host, port)
}


ortalsByTarget map[string][]string) {
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
		
        // Ensure session is actually active
        state, _ := os.ReadFile(filepath.Join(sessionPath, "state"))
		
		
		// TODO cleanData := strings.Trim(string(data), " \n\r\t\x00")
		// **** ON EVERY READ FROM /sys (sysfs) and /proc
		
        if strings.TrimSpace(string(state)) != "LOGGED_IN" {
            continue
        }

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

        // HostName extraction
        hostName := filepath.Base(linkTarget)
        // Fallback: if the link target ends in /iscsi_session/sessionX,
        // we might need to go one level up.
        if !strings.HasPrefix(hostName, "host") {
            hostName = filepath.Base(filepath.Dir(linkTarget))
        }

        if !strings.HasPrefix(hostName, "host") {
            continue
        }
		
		// Robust HostName Extraction:
		// We iterate up the path to find the directory starting with 'host'
		// This is safer than filepath.Base for varying kernel versions.
		hostName := ""
		parts := strings.Split(linkTarget, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if strings.HasPrefix(parts[i], "host") {
				hostName = parts[i]
				break
			}
		}

		if hostName == "" {
			logger.Debugf("Could not find host prefix in path: %s", linkTarget)
			continue
		}

		// 3. EXTRACT HOST NUMBER
		hostNum, err := strconv.Atoi(strings.TrimPrefix(hostName, "host"))
		if err != nil {
			logger.Debugf("Invalid host format: %s", hostName)
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
		logger.Info("No active iSCSI sessions.")
		return
	}
	
	// 1. Identify which Initiator IQNs are already "known"
	knownIqns := make(map[string]bool)
	for _, s := range active {
		if hostIDs[s.num] {
			// Normalize for case-insensitive matching
			normalized := strings.ToLower(s.sourceIQN)
			knownIqns[normalized] = true
		}
	}

	// 2. Add all other host numbers sharing those IQNs
	for _, s := range active {
		normalized := strings.ToLower(s.sourceIQN)
		if !hostIDs[s.num] && knownIqns[normalized] {
			hostIDs[s.num] = true
			logger.Debugf("Associated host%d with IQN %s (Multipath identified)", s.num, s.sourceIQN)
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

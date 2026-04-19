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
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"
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

func NewOsDeviceConnectivityIscsi(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityIscsi{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, KeyedGater, Mounter, clean_scsi_device),
	}
}

// TOOD can HANG
// TODO consider gater
func (r OsDeviceConnectivityIscsi) iscsiCmd(args ...string) (string, error) {
	// TODO
	out, err := r.Executer.ExecuteWithTimeout(int(IscsiCmdTimeout.Seconds()*1000), "iscsiadm", args)
	return string(out), err
}

func (r OsDeviceConnectivityIscsi) iscsiDiscover(ctx context.Context, portal string) error {
	logger.Infof("Performing iSCSI discovery for portal: %s", portal)

	// REQUIREMENT 6: Gater prevents concurrent discovery to the same portal
	// We use the portal IP as the key to isolate failures.
	err := r.KeyedGater.Acquire(ctx, "discovery-"+portal, 1, 15*time.Second)
	if err != nil {
		return err
	}
	defer r.KeyedGater.Release("discovery-"+portal)

	// Adding --op=update ensures that if the TPGT or other parameters
	// changed on the array, the local Open-iSCSI DB is refreshed.	
	output, err := r.iscsiCmd("-m", "discoverydb", "-t", "sendtargets", "-p", portal, "--discover", "--op=update")
	if err != nil {
		// On RH7, discovery might fail if the DB is locked. 
		// The Gater + Context ensures we don't stay stuck.	
		logger.Errorf("Failed to discover iSCSI for %s: {%s}, error: {%s}", portal, output, err)
		return err
	}
	return nil
}



func (r OsDeviceConnectivityIscsi) iscsiLogin(ctx context.Context, targetName, portal string) {
	// REQUIREMENT 6: Gater prevents a "Thundering Herd" on a single portal
	// We use the portal IP as the key to isolate failures.
	err := r.KeyedGater.Acquire(ctx, "login-"+portal, 1, 30*time.Second)
	if err != nil {
		logger.Errorf("Gater: timed out waiting for login slot on %s", portal)
		return
	}
	defer r.KeyedGater.Release("login-"+portal)
	
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
				logger.Warningf("iSCSI session exists but login failed for %s. Multipath will handle recovery.", portal)
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
func (r OsDeviceConnectivityIscsi) iscsiGetRawSessions(ctx context.Context) ([]string, error) {
	const sysPath = "/sys/class/iscsi_session"
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

		// 1. Quick Exit for non-logged-in sessions
		stateBuf, err := r.readSysfs(ctx, filepath.Join(sessionPath, "state"))
		if err != nil {
			continue // Session likely vanished during ReadDir (common race condition)
		}
		stateStr := strings.TrimSpace(string(stateBuf))

		if stateStr != "LOGGED_IN" {
			logger.Warningf("Session %s is in %s", sessionID, stateStr)
			// Ignore other transient/failed states (REOPENING, FREE, etc.)
			continue
		}

		targetNameBuf, err := r.readSysfs(ctx, filepath.Join(sessionPath, "targetname"))
		if err != nil {
			continue
		}
		targetName := strings.TrimSpace(string(targetNameBuf))

		// 2. Direct Traversal (Avoids Glob overhead)
		// Path: /sys/class/iscsi_session/sessionX/device/connectionX:S/iscsi_connection/connectionX:S/
		devicePath := filepath.Join(sessionPath, "device")
		connDirs, err := r.readSysfs(ctx, devicePath)
		if err != nil {
			continue
		}

		for _, cd := range connDirs {
			if !strings.HasPrefix(cd.Name(), "connection") {
				continue
			}
			
			// 2. The standard path: /device/connectionX:S/iscsi_connection/connectionX:S/
			// We use cd.Name() for both levels to ensure they match dynamically.
			attrPath := filepath.Join(devicePath, cd.Name(), "iscsi_connection", cd.Name())

			addrBuf, errA := r.readSysfs(ctx, filepath.Join(attrPath, "address"))
			portBuf, errP := r.readSysfs(ctx, filepath.Join(attrPath, "port"))

			// 3. Safety Check: Does the attrPath actually exist? 
			// (Some hardware offload cards change this structure)
			if _, err := os.Stat(attrPath); os.IsNotExist(err) {
				// Fallback: Check if attributes are directly in the connection folder 
				// (Older kernels or specific transports)
				logger.Warning("subdir not found")
				attrPath = filepath.Join(devicePath, cd.Name())
			}			

			addrBuf, errA := os.ReadFile(filepath.Join(attrPath, "address"))
			portBuf, errP := os.ReadFile(filepath.Join(attrPath, "port"))

			if errA == nil && errP == nil {
				portal := net.JoinHostPort(
					strings.TrimSpace(string(addrBuf)),
					strings.TrimSpace(string(portBuf)),
				)
				// Format matches parser: "tcp: [id] ip:port iqn"
				// Format as: tcp: [1] 192.168.1.100:3260 iqn.2026.com.ibm:target
				// Matches iscsiadm format exactly for downstream parsers
				results = append(results, fmt.Sprintf("tcp: [%s] %s %s", sessionID, portal, targetName))
				break // One connection per session is the standard CSI expectation
			}
		}
	}
	return results, nil
}

func (r OsDeviceConnectivityIscsi) getAllSessions(ctx context.Context) (map[string]map[string]bool, error) {
	lines, err := r.iscsiGetRawSessions(ctx)
	if err != nil {
		return nil, err
	}

	portalsByTarget := make(map[string]map[string]bool)
	for _, line := range lines {
		// Native/iscsiadm format: "tcp: [id] 1.2.3.4:3260 iqn.2026-01.com.example:target"
		parts := strings.Fields(line)
		// Check for "tcp" as validity in case we switch back to using iscsiadm
		if len(parts) < 4 || !strings.HasPrefix(parts[0], "tcp") {
			continue
		}

		// Normalize both pieces of data from sysfs
		targetName := strings.ToLower(parts[3])
		normalizedPortal := r.normalizePortal(parts[2])

		if _, exists := portalsByTarget[targetName]; !exists {
			portalsByTarget[targetName] = make(map[string]bool)
		}
		portalsByTarget[targetName][normalizedPortal] = true
	}
	return portalsByTarget, nil
}

func (r OsDeviceConnectivityIscsi) filterLoggedIn(ctx context.Context, portalsByTarget map[string][]string) (map[string][]string, error) {
	loggedInPortalsByTarget, err := r.getAllSessions(ctx)
	if err != nil {
		return nil, err
	}

	filteredPortalsByTarget := make(map[string][]string)

	for targetName, portals := range portalsByTarget {
		// IQNs are technically case-insensitive in the iSCSI spec,
		// but Linux sysfs and iscsiadm usually present them as lowercase.
		normalizedTarget := strings.ToLower(targetName)

		for _, portal := range portals {
			// Normalize input portal to match the map keys
			normalizedPortal := r.normalizePortal(portal)

			activePortals, exists := loggedInPortalsByTarget[normalizedTarget]

			// If target doesn't exist or this specific portal isn't logged in
			if !exists || !activePortals[normalizedPortal] {
				filteredPortalsByTarget[targetName] = append(filteredPortalsByTarget[targetName], portal)
			}
		}
	}
	return filteredPortalsByTarget, nil
}

func (r OsDeviceConnectivityIscsi) discoverAndLogin(ctx context.Context, portalsByTarget map[string][]string) {
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
					if err := r.iscsiDiscover(ctx, normPortal); err == nil {
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

	// 2. Perform Logins (using 'exit 15' safe login)
	for targetName, portals := range portalsByTarget {
		for _, portal := range portals {
			r.iscsiLogin(ctx, targetName, portal)
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
			if !p.IsDir() {
				continue
			}

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
				// to match the format used in filterLoggedIn logic.
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

type activeSession struct {
	sourceIQN string
	hostNum   int
}

func (r OsDeviceConnectivityIscsi) extractHostFromDeviceLink(sessionPath string) (int, error) {
	deviceLink := filepath.Join(sessionPath, "device")

	// 1. Resolve the symlink to an absolute physical path
	// e.g., /sys/devices/platform/host4/session1
	realPath, err := filepath.EvalSymlinks(deviceLink)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve device link: %w", err)
	}

	// 2. Walk up the path to find the "hostX" component
	// This handles different sysfs nesting depths across kernel versions
	curr := realPath
	for {
		base := filepath.Base(curr)
		if strings.HasPrefix(base, "host") {
			hostNum, err := strconv.Atoi(strings.TrimPrefix(base, "host"))
			if err == nil {
				return hostNum, nil
			}
		}

		parent := filepath.Dir(curr)
		if parent == curr || parent == "/" || parent == "." {
			break
		}
		curr = parent
	}

	return 0, fmt.Errorf("could not find host identifier in path %s", realPath)
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

		// 1. STATE CHECK (using the helper from before)
		stateBuf, _ := os.ReadFile(filepath.Join(sessionPath, "state"))
		if cleanSysfsData(stateBuf) != "LOGGED_IN" {
			continue
		}

		// 2. ROBUST HOST RESOLUTION (Handles S_ISLNK variations)
		hostNum, err := r.extractHostFromDeviceLink(sessionPath)
		if err != nil {
			logger.Debugf("Skipping %s: %v", entry.Name(), err)
			continue
		}

		// 3. IQN EXTRACTION
		hostName := fmt.Sprintf("host%d", hostNum)
		initiatorIQN, err := r.getInitiatorIQN(sessionPath, hostName)
		if err != nil {
			logger.Debugf("Skipping session %s: %v", entry.Name(), err)
			continue
		}

		sessions = append(sessions, activeSession{
			sourceIQN: initiatorIQN,
			hostNum:   hostNum,
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
		if hostIDs[s.hostNum] {
			knownIqns[strings.ToLower(s.sourceIQN)] = true
		}
	}

	// 2. Map all other hosts that use the same Initiator IQN
	// This captures secondary NICs/Paths for the same volume
	for _, s := range active {
		iqn := strings.ToLower(s.sourceIQN)
		if knownIqns[iqn] && !hostIDs[s.hostNum] {
			hostIDs[s.hostNum] = true
			logger.Infof("Multipath discovery: host%d associated with known initiator %s", s.hostNum, iqn)
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

func (r OsDeviceConnectivityIscsi) RemoveGhostDevice(expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(expectedSerial, expectedLun, arrayIdentifiers)
}

func (r OsDeviceConnectivityIscsi) ValidateLun(targetDm string, lun int, sysDevices []string, expectedSerial string) error {
	return r.HelperScsiGeneric.ValidateLun(targetDm, lun, sysDevices, expectedSerial)
}

// Helper function to be used to extract canonical ID
func (r OsDeviceConnectivityIscsi) GetBlockDeviceForSession(sessionID string) (string, error) {
	// Path: /sys/class/iscsi_session/sessionID/device/targetX:Y:Z/X:Y:Z:L/block/
	sessionDevicePath := fmt.Sprintf("/sys/class/iscsi_session/session%s/device", sessionID)

	// 1. Find the target directory (e.g., target1:0:0)
	entries, err := os.ReadDir(sessionDevicePath)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "target") {
			targetPath := filepath.Join(sessionDevicePath, entry.Name())

			// 2. Find the LUN directory (e.g., 1:0:0:0)
			luns, err := os.ReadDir(targetPath)
			if err != nil {
				continue
			}

			for _, lun := range luns {
				// Look for the block subdirectory
				blockPath := filepath.Join(targetPath, lun.Name(), "block")
				disks, err := os.ReadDir(blockPath)
				if err == nil && len(disks) > 0 {
					// Returns "sdb"
					return "/dev/" + disks[0].Name(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("no block device found for session %s", sessionID)
}

func cleanSysfsData(data []byte) string {
	return strings.Trim(string(data), " \n\r\t\x00")
}

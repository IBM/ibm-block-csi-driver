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
	sysPath             = "/sys/class/iscsi_session"
)

type OsDeviceConnectivityIscsi struct {
	Executer          executer.ExecuterInterface
	KeyedGater      *executer.KeyedGater
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityIscsi(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityIscsi{
		Executer:          executer,
		KeyedGater: KeyedGater,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, KeyedGater, Mounter, clean_scsi_device),
	}
}

// TOOD can HANG
// TODO consider gater
func (r OsDeviceConnectivityIscsi) iscsiCmd(args ...string) (string, error) {
	logger.Error("Running command")
	out, err := r.Executer.ExecuteWithTimeout(int(IscsiCmdTimeout.Seconds()*1000), "iscsiadm", args)
	return string(out), err
}

func (r OsDeviceConnectivityIscsi) iscsiDiscover(ctx context.Context, portal string) error {
	// Fixed: Lock scope isolated via subnet to balance performance and avoid DB collisions
	lockScope := r.getDiscoveryScopeKey(portal)

	err := r.KeyedGater.Acquire(ctx, "discovery-scope-"+lockScope, 1, 30*time.Second)
	if err != nil {
		logger.Errorf("Timeout waiting for discovery scope lock %s: %v", lockScope, err)
		return err}
	defer r.KeyedGater.Release("discovery-scope-" + lockScope)
	cliPortal := r.EnsurePort(portal)
	output, err := r.iscsiCmd("-m", "discoverydb", "-t", "sendtargets", "-p", cliPortal, "--discover", "--op=update")
	if err != nil  {
		logger.Errorf("Failed to discover iSCSI for %s: %s (err: %v)", cliPortal, output, err)
		return err
	}
	return nil
}

func (r OsDeviceConnectivityIscsi) iscsiLogin(ctx context.Context, targetName, portal string) {
	// 1. Isolate the concurrency gate lock using strictly the portal's IP address.
	// This prevents format variations (like "10.0.0.1" vs "10.0.0.1:3260") from bypassing the lock.
	ipKey := r.ExtractIP(portal)
	err := r.KeyedGater.Acquire(ctx, "login-"+ipKey, 1, 30*time.Second)
	if err != nil {
		logger.Errorf("Gater: timed out waiting for login slot on portal IP %s", ipKey)
		return
	}
	defer r.KeyedGater.Release("login-" + ipKey)
	
	// 2. Ensure a port format exists on the portal parameter string.
	// The iscsiadm CLI strictly requires the host:port format to match its internal node records.
	cliPortal := r.EnsurePort(portal)
	
	logger.Infof("Executing iSCSI login for target %s via portal %s", targetName, cliPortal)
	output, err := r.iscsiCmd("-m", "node", "-p", cliPortal, "-T", targetName, "--login")

	// 3. Evaluate the command response and exit codes gracefully
	if err != nil {
		if exitCode, isExitError := r.Executer.GetExitCode(err); isExitError {
			// Exit Code 15: ISCSI_ERR_LOGIN_EXIST
			// The session is already logged in and active. This is a success state for a CSI driver.
			if exitCode == 15 {
				logger.Debugf("iSCSI session for %s (%s) already active", targetName, cliPortal)
				return
			}

			// Exit Code 24: ISCSI_ERR_SESSION_EXISTS
			// The connection session exists but logging in failed (common during transient storage failovers).
			// We treat this as success because Linux Multipath daemon (multipathd) handles path recovery automatically.
			if exitCode == 24 {
				logger.Warningf("iSCSI session exists but login failed for %s. Multipath will handle recovery.", cliPortal)
				return
			}
		}

		// Real connection, authorization, or fabric failure: log details for storage troubleshooting
		logger.Errorf("Failed to login iSCSI target %s via %s: %s (err: %v)", targetName, cliPortal, output, err)
	}
}

// iscsiGetRawSessions now reads from /sys/class/iscsi_session
// It returns lines in the format: "tcp: [1] 192.168.1.100:3260,1 iqn.target.name"
// matching the output of `iscsiadm -m session`
func (r OsDeviceConnectivityIscsi) iscsiGetRawSessions(ctx context.Context) ([]string, error) {
	const sysPath = "/sys/class/iscsi_session"
	sessions, err := os.ReadDir(sysPath)
	if err != nil {
		logger.Error("Cannot read sessions dir")
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", sysPath, err)
	}

	var results []string
	for _, s := range sessions {
		// Example: /sys/class/iscsi_session/session1
		logger.Errorf("Check session %s", s.Name())
		if !strings.HasPrefix(s.Name(), "session") {
			continue
		}

		sessionID := strings.TrimPrefix(s.Name(), "session")
		sessionPath := filepath.Join(sysPath, s.Name())

		// Context Check: Ensure we respect upstream cancellations or timeouts 
		// before initiating block reads on a target session path.
		if err := ctx.Err(); err != nil {
			logger.Warningf("Context canceled during session directory scan: %v", err)
			return nil, err
		}

		// 1. Quick Exit for non-logged-in sessions
		stateBuf, err := r.readSysfs(filepath.Join(sessionPath, "state"))
		if err != nil {
			logger.Warningf("Session %s likely vanished during processing, skipping", sessionID)
			continue // Gracefully skip common race condition (Session vanished during ReadDir)
		}
		stateStr := strings.TrimSpace(string(stateBuf))

		if stateStr != "LOGGED_IN" {
			logger.Warningf("Session %s is in %s state, skipping", sessionID, stateStr)
			// Ignore other transient/failed states (REOPENING, FREE, etc.)
			continue
		}

		targetNameBuf, err := r.readSysfs(filepath.Join(sessionPath, "targetname"))
		if err != nil {
			logger.Errorf("Cannot read target for session %s, skipping", sessionID)
			continue // Handle partial teardown gracefully
		}
		targetName := strings.TrimSpace(string(targetNameBuf))
		
		logger.Errorf("Target name %s", targetName)

		// 2. Direct Traversal (Avoids Glob overhead)
		devicePath := filepath.Join(sessionPath, "device")
		connDirs, err := os.ReadDir(devicePath)
		if err != nil {
			logger.Errorf("Cannot open devicePath for session %s, skipping", sessionID)
			continue
		}

		for _, cd := range connDirs {
			logger.Errorf("Scan conn %s", cd.Name())
			if !strings.HasPrefix(cd.Name(), "connection") {
				continue
			}
			
			// Path structure: /device/connectionX:S/iscsi_connection/connectionX:S/
			attrPath := filepath.Join(devicePath, cd.Name(), "iscsi_connection", cd.Name())

			// Fallback check if the subdirectory structure differs on older kernel versions
			if _, err := os.Stat(attrPath); os.IsNotExist(err) {
				logger.Warningf("Subdir not found for connection %s, falling back to base connection path", cd.Name())
				attrPath = filepath.Join(devicePath, cd.Name())
			}

			addrBuf, errA := os.ReadFile(filepath.Join(attrPath, "address"))
			portBuf, errP := os.ReadFile(filepath.Join(attrPath, "port"))
			
			if errA != nil {
				logger.Errorf("Failed to read connection address for %s: %v", cd.Name(), errA)
			}
			if errP != nil {
				logger.Errorf("Failed to read connection port for %s: %v", cd.Name(), errP)
			}

			// Construct the record string only if both components were successfully read
			if errA == nil && errP == nil {
				logger.Error("Compare portal")
				portal := net.JoinHostPort(
					strings.TrimSpace(string(addrBuf)),
					strings.TrimSpace(string(portBuf)),
				)
				logger.Errorf("Compare portal %s", portal)
				
				// Re-synthesizes the native/iscsiadm output format exactly: "tcp: [id] ip:port iqn"
				results = append(results, fmt.Sprintf("tcp: [%s] %s %s", sessionID, portal, targetName))
				break // One connection per session is the standard CSI expectation
			}
		}
	}
	return results, nil
}

// getAllSessions groups currently active sessions into maps isolated by IP key signatures
func (r OsDeviceConnectivityIscsi) getAllSessions(ctx context.Context) (map[string]map[string]bool, error) {
	lines, err := r.iscsiGetRawSessions(ctx)
	if err != nil {
		return nil, err
	}

	portalsByTarget := make(map[string]map[string]bool)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue // Completely empty lines are safe to ignore gracefully
		}

		parts := strings.Fields(line)
		
		// If it doesn't start with tcp, it's likely a benign log header or comment. 
		// We can safely skip it without assuming a session was lost.
		if len(parts) > 0 && !strings.HasPrefix(parts[0], "tcp") {
			logger.Warningf("Skipping non-session utility line: %s", line)
			continue
		}

		// CRITICAL LINE VALIDATION: 
		// If the line explicitly claimed to be a "tcp" session entry but is missing 
		// structural data fields, we MUST fail fast to prevent a dual-login collision.
		if len(parts) < 4 {
			logger.Errorf("CRITICAL: Active iSCSI session entry is corrupt or truncated: %s", line)
			return nil, fmt.Errorf("failed to parse active iSCSI session list safely: truncation detected")
		}

		targetName := strings.ToLower(parts[3])
		ipKey := r.ExtractIP(parts[2]) // Strips ports for correct filterLoggedIn mapping

		if _, exists := portalsByTarget[targetName]; !exists {
			portalsByTarget[targetName] = make(map[string]bool)
		}
		portalsByTarget[targetName][ipKey] = true
	}
	return portalsByTarget, nil
}

func (r OsDeviceConnectivityIscsi) filterLoggedIn(ctx context.Context, portalsByTarget map[string][]string) (map[string][]string, error) {
	// 1. Fetch current active sessions (keys are lowercase IQNs and portless IPs)
	loggedInPortalsByTarget, err := r.getAllSessions(ctx)
	if err != nil {
		logger.Error("Failed to get all sessions")
		return nil, err
	}

	filteredPortalsByTarget := make(map[string][]string)

	for targetName, portals := range portalsByTarget {
		logger.Debugf("Scanning target for active sessions: %s", targetName)
	
		// Normalize target name to lowercase to match standard Linux sysfs formatting
		normalizedTarget := strings.ToLower(targetName)
		activePortals, exists := loggedInPortalsByTarget[normalizedTarget]

		for _, portal := range portals {
			logger.Debugf("Checking login status for portal: %s", portal)
			
			// FIX: Extract ONLY the IP/host identity to accurately match the map keys.
			// This fixes the bug where appended ports caused false lookup misses.
			ipKey := r.ExtractIP(portal)

			// If the target has no active sessions, or this specific IP path isn't logged in yet
			if !exists || !activePortals[ipKey] {
				logger.Infof("Portal %s for target %s is not logged in; adding to execution queue.", portal, targetName)
				
				// CRITICAL: Append the ORIGINAL, unmutated 'portal' string (with its original port intact).
				// This guarantees downstream commands like iscsiLogin receive fully qualified parameters.
				filteredPortalsByTarget[targetName] = append(filteredPortalsByTarget[targetName], portal)
			} else {
				logger.Debugf("Portal %s for target %s is already logged in. Skipping redundant login.", portal, targetName)
			}
		}
	}
	return filteredPortalsByTarget, nil
}

func (r OsDeviceConnectivityIscsi) discoverAndLogin(ctx context.Context, portalsByTarget map[string][]string) {
	// 1. Surgical Scan: Load existing target folders from the local database on disk.
	// All keys inside dbCache are fully normalized to lowercase targets and portless IPs.
	dbCache := r.loadRelevantTargets(portalsByTarget)
	discoveredPortals := make(map[string]bool)

	for targetName, requestedPortals := range portalsByTarget {
		normalizedTarget := strings.ToLower(targetName)

		for _, portal := range requestedPortals {
			// Extract a standardized, portless IP key for accurate cache comparisons.
			ipKey := r.ExtractIP(portal)

			// Double-Checked Lock Optimization: If the local disk database already 
			// contains this target/IP record, skip discovery completely to maximize performance.
			if dbCache[normalizedTarget] != nil && dbCache[normalizedTarget][ipKey] {
				logger.Debugf("Target %s portal IP %s already verified in database cache. Skipping discovery.", targetName, ipKey)
				continue
			}

			// If the target record isn't in the database, execute discovery
			if !discoveredPortals[ipKey] {
				logger.Infof("Target %s portal IP %s missing from DB, triggering discovery sequence...", targetName, ipKey)
				
				// Pass the full unmutated portal string containing the port to ensure 
				// iscsiadm can reach the array properly.
				if err := r.iscsiDiscover(ctx, portal); err == nil {
					discoveredPortals[ipKey] = true

					// Gracefully update the dynamic runtime cache map so subsequent 
					// targets discovered via this interface avoid redundant scans.
					if dbCache[normalizedTarget] == nil {
						dbCache[normalizedTarget] = make(map[string]bool)
					}
					dbCache[normalizedTarget][ipKey] = true
				} else {
					logger.Errorf("Discovery failed for portal %s. Proceeding gracefully with remaining targets.", portal)
				}
			}
		}
	}

	// 2. Perform Logins (using 'exit 15' safe login checks)
	// Iterates through the original slice payloads to preserve original caller formats 
	// and ensure that standard host:port parameters pass directly to node map attachments.
	for targetName, portals := range portalsByTarget {
		for _, portal := range portals {
			logger.Infof("Routing attachment request to login subsystem for target %s via %s", targetName, portal)
			r.iscsiLogin(ctx, targetName, portal)
		}
	}
}


// loadRelevantTargets only probes the specific subdirectories for the targets in the request
// loadRelevantTargets only probes the specific subdirectories for the targets in the request
func (r OsDeviceConnectivityIscsi) loadRelevantTargets(requestedTargets map[string][]string) map[string]map[string]bool {
	db := make(map[string]map[string]bool)
	basePath := "/etc/iscsi/nodes"

	for targetName := range requestedTargets {
		// FIX: Use lowercase target keys to ensure case-insensitivity mapping
		normalizedTarget := strings.ToLower(targetName)
		targetPath := filepath.Join(basePath, targetName)
		
		logger.Errorf("Check target path %s", targetPath)

		db[normalizedTarget] = make(map[string]bool)

		// Attempt to read the specific target directory
		portals, err := os.ReadDir(targetPath)
		if err != nil {
			logger.Errorf("Check target path %s - fail", targetPath)
			// Graceful containment: directory doesn't exist; target unknown to DB
			continue
		}

		for _, p := range portals {
			logger.Errorf("Check portal %s", p.Name())
			if !p.IsDir() {
				logger.Error("Not dir")
				continue
			}

			// Open-iSCSI directory name format: "IP,Port,TPGT"
			// IPv4 Example: "192.168.1.10,3260,1"
			// IPv6 Example: "2001:db8::1,3260,1"
			// Using strings.Split by comma is safe because colons in IPv6 won't conflict.
			parts := strings.Split(p.Name(), ",")
			if len(parts) >= 2 {
				logger.Error("portal validity")
				
				// FIX: Run ExtractIP directly on the raw IP slice from the comma-split data.
				// This isolates the raw, portless, bracketless IP key instantly and consistently.
				ipKey := r.ExtractIP(parts[0])
				
				logger.Errorf("norm %s", ipKey)
				
				db[normalizedTarget][ipKey] = true
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

func (r OsDeviceConnectivityIscsi) EnsureLogin(ctx context.Context, allPortalsByTarget map[string][]string) {
	portalsByTarget, err := r.filterLoggedIn(ctx, allPortalsByTarget)
	if err == nil {
		if len(portalsByTarget) == 0 {
			logger.Debug("All iSCSI portals are already logged in.")
			return
		}
		logger.Error("discover")
		r.discoverAndLogin(ctx, portalsByTarget)
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
		logger.Error("Cannot read active sessions")
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read iSCSI sessions from sysfs: %w", err)
	}

	var sessions []activeSession
	for _, entry := range entries {
		sessionPath := filepath.Join(sessionBaseDir, entry.Name())
		
		logger.Errorf("Session path %s", sessionPath)

		// 1. STATE CHECK (using the helper from before)
		stateBuf, _ := os.ReadFile(filepath.Join(sessionPath, "state"))
		if cleanSysfsData(stateBuf) != "LOGGED_IN" {
			logger.Errorf("State %s", cleanSysfsData(stateBuf))
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
		
		logger.Errorf("Add init %s host %s", initiatorIQN, hostName)
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

func (r OsDeviceConnectivityIscsi) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {
	/*
	   Return Value: "dm-X" of the volumeID.
	*/
	return r.HelperScsiGeneric.GetMpathDevice(ctx, volumeId)
}

func (r OsDeviceConnectivityIscsi) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(ctx, sysDevices)
}

func (r OsDeviceConnectivityIscsi) RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(ctx, expectedSerial, expectedLun, arrayIdentifiers)
}

func (r OsDeviceConnectivityIscsi) ValidateLun(ctx context.Context, targetDm string, lun int, sysDevices []string, expectedSerial string) error {
	return r.HelperScsiGeneric.ValidateLun(ctx, targetDm, lun, sysDevices, expectedSerial)
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

func (r *OsDeviceConnectivityIscsi) readSysfs(path string) (string, error) {
        data, err := os.ReadFile(path)
        if err != nil {
                return "", err
         }
        return strings.Trim(string(data), " \n\r\t\x00"), nil
}



// -------------------------------------------------------------------------
// Helper Utilities for Format Extraction & Key Isolation
// -------------------------------------------------------------------------

// ExtractIP extracts a clean, bracketless IP/host for internal map matching and KeyedGater locks.
func (r OsDeviceConnectivityIscsi) ExtractIP(portal string) string {
	portal = strings.ToLower(strings.TrimSpace(portal))
	if host, _, err := net.SplitHostPort(portal); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(portal, "[]")
}

// EnsurePort ensures that the portal string passed to CLI execution contains a port suffix.
func (r OsDeviceConnectivityIscsi) EnsurePort(portal string) string {
	portal = strings.TrimSpace(portal)
	if _, _, err := net.SplitHostPort(portal); err == nil {
		return portal
	}
	return net.JoinHostPort(portal, "3260")
}

// getDiscoveryScopeKey groups locks by subnet prefix to avoid write collisions on the same storage array
func (r OsDeviceConnectivityIscsi) getDiscoveryScopeKey(portal string) string {
        ipStr := r.ExtractIP(portal)
        ip := net.ParseIP(ipStr)
        if ip == nil {
                return ipStr
        }
        if ipv4 := ip.To4(); ipv4 != nil {
                return fmt.Sprintf("%d.%d.%d", ipv4[0], ipv4[1], ipv4[2]) // /24 grouping
        }
        if ipv6 := ip.To16(); ipv6 != nil {
                // FIX: Combine adjacent bytes to construct the first four 16-bit blocks of a standard IPv6 /64 prefix
                block1 := uint16(ipv6[0])<<8 | uint16(ipv6[1])
                block2 := uint16(ipv6[2])<<8 | uint16(ipv6[3])
                block3 := uint16(ipv6[4])<<8 | uint16(ipv6[5])
                block4 := uint16(ipv6[6])<<8 | uint16(ipv6[7])

                return fmt.Sprintf("%x:%x:%x:%x", block1, block2, block3, block4) // /64 grouping
        }
        return ipStr
}

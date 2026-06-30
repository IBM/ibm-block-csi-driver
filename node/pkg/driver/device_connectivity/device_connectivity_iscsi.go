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
	KeyedGater        *executer.KeyedGater
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityIscsi(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityIscsi{
		Executer:          executer,
		KeyedGater:        KeyedGater,
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
	lockScope := r.getDiscoveryScopeKey(portal)

	err := r.KeyedGater.Acquire(ctx, "discovery-scope-"+lockScope, 1, 30*time.Second)
	if err != nil {
		return fmt.Errorf("timeout waiting for discovery lock scope %s: %w", lockScope, err)
	}
	defer r.KeyedGater.Release("discovery-scope-" + lockScope)

	cliPortal := r.EnsurePort(portal)
	output, err := r.iscsiCmd("-m", "discoverydb", "-t", "sendtargets", "-p", cliPortal, "--discover", "--op=update")
	if err != nil {
		return fmt.Errorf("iscsiadm discovery failed for portal %s (output: %s): %w", cliPortal, strings.TrimSpace(output), err)
	}
	return nil
}

func (r OsDeviceConnectivityIscsi) iscsiLogin(ctx context.Context, targetName, portal string) error {
	ipKey := r.ExtractIP(portal)
	err := r.KeyedGater.Acquire(ctx, "login-"+ipKey, 1, 30*time.Second)
	if err != nil {
		return fmt.Errorf("concurrency timeout waiting for login slot on portal IP %s: %w", ipKey, err)
	}
	defer r.KeyedGater.Release("login-" + ipKey)

	cliPortal := r.EnsurePort(portal)
	logger.Infof("Executing iSCSI login for target %s via portal %s", targetName, cliPortal)

	output, err := r.iscsiCmd("-m", "node", "-p", cliPortal, "-T", targetName, "--login")
	if err != nil {
		if exitCode, isExitError := r.Executer.GetExitCode(err); isExitError {
			// Exit Code 15: ISCSI_ERR_LOGIN_EXIST (Already logged in) -> Safe Success
			if exitCode == 15 {
				logger.Debugf("iSCSI session for %s (%s) already active.", targetName, cliPortal)
				return nil
			}

			// Exit Code 24: ISCSI_ERR_SESSION_EXISTS -> Active but needs recovery
			if exitCode == 24 {
				logger.Warningf("iSCSI session exists but path requires recovery for %s. Multipath will handle.", cliPortal)
				return nil
			}
		}
		return fmt.Errorf("iscsiadm login failed (target: %s, portal: %s, output: %s): %w", targetName, cliPortal, strings.TrimSpace(output), err)
	}

	logger.Infof("Successfully logged into target %s via portal %s", targetName, cliPortal)
	return nil
}

// iscsiGetRawSessions now reads from /sys/class/iscsi_session
// It returns lines in the format: "tcp: [1] 192.168.1.100:3260,1 iqn.target.name"
// matching the output of `iscsiadm -m session`
func (r OsDeviceConnectivityIscsi) iscsiGetRawSessions(ctx context.Context) ([]string, error) {
	// Querying connection endpoints directly avoids broken symlinks inside session device paths
	const connClassPath = "/sys/class/iscsi_connection"
	connections, err := os.ReadDir(connClassPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Debug("iSCSI connection class directory does not exist. No active sessions.")
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", connClassPath, err)
	}

	var results []string
	for _, c := range connections {
		// Target names like: connection1:0, connection2:0
		if !strings.HasPrefix(c.Name(), "connection") {
			continue
		}

		if err := ctx.Err(); err != nil {
			return nil, err
		}

		connPath := filepath.Join(connClassPath, c.Name())

		// Read address and port directly from the connection class attributes
		addrBuf, errA := os.ReadFile(filepath.Join(connPath, "address"))
		portBuf, errP := os.ReadFile(filepath.Join(connPath, "port"))
		if errA != nil || errP != nil {
			logger.Debugf("Skipping incomplete connection configuration %s", c.Name())
			continue
		}

		portal := net.JoinHostPort(
			strings.TrimSpace(string(addrBuf)),
			strings.TrimSpace(string(portBuf)),
		)

		// Climb up to find the parent session ID.
		// The connection path contains a symlink named "subsystem" or sits inside a parent session folder.
		// A reliable way is reading the actual device symlink destination path.
		evalPath, err := filepath.EvalSymlinks(connPath)
		if err != nil {
			continue
		}

		// Extract "sessionX" from a path like: /sys/devices/platform/host4/session4/connection4:0/iscsi_connection/connection4:0
		sessionID := "0"
		parts := strings.Split(evalPath, "/")
		for _, part := range parts {
			if strings.HasPrefix(part, "session") {
				sessionID = strings.TrimPrefix(part, "session")
				break
			}
		}

		// Read targetname from the sibling session path using the extracted sessionID
		sessionPath := fmt.Sprintf("/sys/class/iscsi_session/session%s", sessionID)

		stateBuf, errS := r.readSysfs(filepath.Join(sessionPath, "state"))
		targetBuf, errT := r.readSysfs(filepath.Join(sessionPath, "targetname"))
		if errS != nil || errT != nil {
			continue // Session tearing down or unavailable
		}

		if strings.TrimSpace(string(stateBuf)) != "LOGGED_IN" {
			continue // Skip transient or failing links
		}

		targetName := strings.TrimSpace(string(targetBuf))

		logger.Debugf("Discovered active sysfs session: [%s] target: %s portal: %s", sessionID, targetName, portal)
		results = append(results, fmt.Sprintf("tcp: [%s] %s %s", sessionID, portal, targetName))
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
			continue
		}

		parts := strings.Fields(line)
		// Fix: Check index 0 element of the slice
		if len(parts) > 0 && !strings.HasPrefix(parts[0], "tcp") {
			continue
		}

		// Verify the string format re-synthesized by iscsiGetRawSessions: "tcp: [id] ip:port iqn"
		if len(parts) < 4 {
			logger.Errorf("Kernel reported structurally corrupt or truncated metadata descriptor: %s", line)
			return nil, fmt.Errorf("failed to safely parse active raw session list: truncation detected")
		}

		// Fix: Extracted elements using explicit slice indexes
		targetName := strings.ToLower(parts[3])
		ipKey := r.ExtractIP(parts[2])

		if _, exists := portalsByTarget[targetName]; !exists {
			portalsByTarget[targetName] = make(map[string]bool)
		}
		portalsByTarget[targetName][ipKey] = true
	}
	return portalsByTarget, nil
}

// filterLoggedIn isolates targets and portals that do not have active sessions
func (r OsDeviceConnectivityIscsi) filterLoggedIn(ctx context.Context, portalsByTarget map[string][]string) (map[string][]string, error) {
	logger.Debug("Querying kernel sysfs to identify existing active iSCSI links")

	// Fetch active sessions from sysfs (Keys are lowercased IQNs and portless IPs)
	loggedInPortalsByTarget, err := r.getAllSessions(ctx)
	if err != nil {
		logger.Errorf("Failed to inventory existing sysfs sessions: %v", err)
		return nil, err
	}

	filteredPortalsByTarget := make(map[string][]string)

	for targetName, portals := range portalsByTarget {
		// Normalize target name to lowercase to match the formatting returned by getAllSessions
		normalizedTarget := strings.ToLower(targetName)
		activePortals, exists := loggedInPortalsByTarget[normalizedTarget]

		logger.Debugf("Evaluating connection health for target: %s (Found in sysfs active map: %t)", normalizedTarget, exists)

		for _, portal := range portals {
			// Extract portless, bracketless IP key signature
			ipKey := r.ExtractIP(portal)

			// If the target completely lacks a session, or this specific IP path isn't logged in
			if !exists || !activePortals[ipKey] {
				logger.Infof("Path disconnect detected: target %s via portal %s needs initialization", targetName, portal)
				filteredPortalsByTarget[targetName] = append(filteredPortalsByTarget[targetName], portal)
			} else {
				logger.Debugf("Path healthy: target %s via portal %s already shows active session", targetName, portal)
			}
		}
	}
	return filteredPortalsByTarget, nil
}

func (r OsDeviceConnectivityIscsi) discoverAndLogin(ctx context.Context, portalsByTarget map[string][]string) error {
	// 1. Surgical Scan: Load existing target folders from the local database on disk.
	dbCache := r.loadRelevantTargets(portalsByTarget)
	discoveredPortals := make(map[string]bool)

	// Track targets/portals that are confirmed ready for login
	validTargetsForLogin := make(map[string][]string)

	for targetName, requestedPortals := range portalsByTarget {
		normalizedTarget := strings.ToLower(targetName)

		for _, portal := range requestedPortals {
			// Ensure ipKey extraction matches the string shape parsed inside loadRelevantTargets
			ipKey := r.ExtractIP(portal)

			// Check if the local disk database already contains this target/IP record
			if dbCache[normalizedTarget] != nil && dbCache[normalizedTarget][ipKey] {
				logger.Debugf("Target %s portal IP %s already verified in database cache. Skipping discovery.", targetName, ipKey)
				validTargetsForLogin[targetName] = append(validTargetsForLogin[targetName], portal)
				continue
			}

			// If the target record isn't in the database, execute discovery
			if !discoveredPortals[ipKey] {
				logger.Infof("Target %s portal IP %s missing from DB, triggering discovery sequence...", targetName, ipKey)

				// Pass the full unmutated portal string (IP:Port) for iscsiadm execution
				if err := r.iscsiDiscover(ctx, portal); err == nil {
					discoveredPortals[ipKey] = true

					// Update local structural runtime cache
					if dbCache[normalizedTarget] == nil {
						dbCache[normalizedTarget] = make(map[string]bool)
					}
					dbCache[normalizedTarget][ipKey] = true

					validTargetsForLogin[targetName] = append(validTargetsForLogin[targetName], portal)
				} else {
					logger.Errorf("Discovery failed for portal %s. Target data will not be available for login.", portal)
				}
			} else if dbCache[normalizedTarget] != nil && dbCache[normalizedTarget][ipKey] {
				// Portal was discovered during this runtime cycle for a different target
				validTargetsForLogin[targetName] = append(validTargetsForLogin[targetName], portal)
			}
		}
	}

	if len(validTargetsForLogin) == 0 {
		return fmt.Errorf("no targets were successfully discovered or found in cache to perform login")
	}

	// 2. Perform Logins on validated database nodes
	var loginErrors []error
	for targetName, portals := range validTargetsForLogin {
		for _, portal := range portals {
			logger.Infof("Routing attachment request to login subsystem for target %s via %s", targetName, portal)

			// Assume r.iscsiLogin is updated to return an error when login fails
			if err := r.iscsiLogin(ctx, targetName, portal); err != nil {
				// Check for exit status 15 (already logged in) inside your iscsiLogin execution block
				logger.Errorf("Failed to log into target %s via portal %s: %v", targetName, portal, err)
				loginErrors = append(loginErrors, fmt.Errorf("target %s login failed via %s: %w", targetName, portal, err))
			}
		}
	}

	// If some logins failed, check if at least one path succeeded (Multipath resiliency fallback)
	if len(loginErrors) > 0 {
		if len(loginErrors) == len(portalsByTarget) { // All paths totally failed
			return fmt.Errorf("all iSCSI login attempts failed: %v", loginErrors)
		}
		logger.Warningf("Some iSCSI paths failed to log in, but proceeding with successful sessions: %v", loginErrors)
	}

	return nil
}

func (r OsDeviceConnectivityIscsi) loadRelevantTargets(requestedTargets map[string][]string) map[string]map[string]bool {
	db := make(map[string]map[string]bool)
	basePath := "/etc/iscsi/nodes"

	for targetName := range requestedTargets {
		// Open-iSCSI creates directories in strictly lowercase IQNs.
		// Normalizing here ensures targetPath matches the Linux filesystem exactly.
		normalizedTarget := strings.ToLower(targetName)
		targetPath := filepath.Join(basePath, normalizedTarget)

		// Changed to Debug level to prevent system log pollution
		logger.Debugf("Checking target directory path: %s", targetPath)

		db[normalizedTarget] = make(map[string]bool)

		// Read the target directory containing portal configurations
		portals, err := os.ReadDir(targetPath)
		if err != nil {
			logger.Debugf("Target path %s not found in local DB (target may not be discovered yet): %v", targetPath, err)
			continue
		}

		for _, p := range portals {
			if !p.IsDir() {
				continue
			}

			logger.Debugf("Processing discovered portal directory: %s", p.Name())

			// Open-iSCSI directory naming format: "IP_ADDRESS,PORT,TPGT"
			// Example IPv4: "192.168.1.10,3260,1"
			// Example IPv6: "[2001:db8::1],3260,1" or "2001:db8::1,3260,1"
			parts := strings.Split(p.Name(), ",")
			if len(parts) >= 2 {
				// Isolate raw IP address by removing brackets if present via ExtractIP
				ipKey := r.ExtractIP(parts[0])

				logger.Debugf("Successfully mapped normalized IP key: %s for target: %s", ipKey, normalizedTarget)

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
	logger.Infof("Starting iSCSI login verification for %d requested targets", len(allPortalsByTarget))

	// 1. Identify which targets and portals are missing active sessions in sysfs
	portalsToLogin, err := r.filterLoggedIn(ctx, allPortalsByTarget)
	if err != nil {
		logger.Errorf("Failed to filter logged in iSCSI portals: {%v}", err)
		return
	}

	// 2. Early exit optimization if all paths are already logged in and healthy
	if len(portalsToLogin) == 0 {
		logger.Debug("All iSCSI portals are already logged in.")
		return
	}

	logger.Infof("%d targets have paths requiring active discovery or login operations", len(portalsToLogin))

	// 3. Trigger discovery and execute the login pipeline
	if err := r.discoverAndLogin(ctx, portalsToLogin); err != nil {
		logger.Errorf("iSCSI storage attachment pipeline failed: %v", err)
		return
	}

	logger.Info("Successfully completed all required iSCSI target login procedures.")
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

func (r OsDeviceConnectivityIscsi) ExtractIP(portal string) string {
	portal = strings.ToLower(strings.TrimSpace(portal))

	// If the portal string contains a port via colon separator, split it cleanly
	if host, _, err := net.SplitHostPort(portal); err == nil {
		return strings.Trim(host, "[]")
	}

	// Fallback handling for raw strings or already split open-iscsi filesystem inputs
	return strings.Trim(portal, "[]")
}

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

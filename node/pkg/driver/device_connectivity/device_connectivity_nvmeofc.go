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
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

const (
	nvmeCmdTimeout                      = 10 * 1000
	nvmeTransportFC                     = "fc"
	nvmeDiscoveryNqn                    = "nqn.2014-08.org.nvmexpress.discovery"
	FCPortPath                          = "/sys/class/fc_host/host*/port_name"
	nvmeTargetPathCount                 = 3
	nvmeMinPathsForNonNativeDmMultipath = 2
	// nvmeByIdEuiPrefix is the stable udev by-id symlink prefix for an NVMe namespace
	// keyed by its NGUID. Storage Virtualize exposes only an NGUID descriptor (no
	// EUI-64), which udev labels with the "eui." prefix; the 32-hex value that follows
	// is exactly what convertScsiIdToNguid derives from the SCSI volume UID.
	nvmeByIdEuiPrefix    = "/dev/disk/by-id/nvme-eui."
	sysBlockNvmeWwidGlob = "/sys/block/nvme*/wwid"
	// sysClassNvmeSubsysNqnGlob enumerates every NVMe controller's subsystem NQN;
	// sysBlockDeviceSubsysNqnFmt reads a namespace head's subsystem NQN via its
	// device link. Used to find which controllers carry a given namespace for rescan.
	sysClassNvmeSubsysNqnGlob  = "/sys/class/nvme/nvme*/subsysnqn"
	sysBlockDeviceSubsysNqnFmt = "/sys/block/%s/device/subsysnqn"
)

type OsDeviceConnectivityNvmeOFc struct {
	Executer          executer.ExecuterInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityNvmeOFc(executer executer.ExecuterInterface, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityNvmeOFc{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, clean_scsi_device),
	}
}

// EnsureLogin performs NVMe-oFC discovery and connect for each (arrayTargetPort, hostPort) pair.
// Connects paths until nvmeTargetPathCount is reached. Logs error if 0 paths result,
// warning if below target. For non-native NVMe with find_multipaths=on, logs error if < 2 paths.
func (r OsDeviceConnectivityNvmeOFc) EnsureLogin(ipsByArrayInitiator map[string][]string) {
	if len(ipsByArrayInitiator) == 0 {
		logger.Warningf("NVMe-oFC EnsureLogin: no array target ports in publish context, skipping")
		return
	}

	hostPorts, err := r.getHostFCPorts()
	if err != nil || len(hostPorts) == 0 {
		logger.Errorf("NVMe-oFC EnsureLogin: failed to read host FC ports: %v", err)
		return
	}
	logger.Debugf("NVMe-oFC EnsureLogin: host FC ports: %v", hostPorts)

	livePaths := r.getLivePathPairs()
	logger.Debugf("NVMe-oFC EnsureLogin: live path pairs: %v", livePaths)

	currentPaths := countLivePathsForSubsystem(livePaths, ipsByArrayInitiator)
	logger.Infof("NVMe-oFC EnsureLogin: current live paths=%d target=%d", currentPaths, nvmeTargetPathCount)

	if currentPaths >= nvmeTargetPathCount {
		logger.Infof("NVMe-oFC EnsureLogin: already at target path count (%d), skipping connect", nvmeTargetPathCount)
		return
	}

	connectedPaths := currentPaths
	for arrayTargetPort := range ipsByArrayInitiator {
		if connectedPaths >= nvmeTargetPathCount {
			logger.Infof("NVMe-oFC EnsureLogin: reached target path count (%d), stopping", nvmeTargetPathCount)
			break
		}
		for _, hostPort := range hostPorts {
			if connectedPaths >= nvmeTargetPathCount {
				break
			}

			pathKey := normalizeTraddr(arrayTargetPort) + "|" + normalizeTraddr(hostPort)
			if livePaths[pathKey] {
				logger.Debugf("NVMe-oFC EnsureLogin: path already live target=%s host=%s, skipping",
					arrayTargetPort, hostPort)
				continue
			}

			subNqn, err := r.discoverSubNqn(arrayTargetPort, hostPort)
			if err != nil {
				logger.Debugf("NVMe-oFC EnsureLogin: discover error target=%s host=%s: %v",
					arrayTargetPort, hostPort, err)
				continue
			}
			if subNqn == "" {
				logger.Debugf("NVMe-oFC EnsureLogin: no subnqn found target=%s host=%s, skipping",
					arrayTargetPort, hostPort)
				continue
			}

			logger.Infof("NVMe-oFC EnsureLogin: connecting NQN=%s target=%s host=%s",
				subNqn, arrayTargetPort, hostPort)
			if r.nvmeConnect(arrayTargetPort, hostPort, subNqn) {
				connectedPaths++
			}
		}
	}

	// Re-read to get kernel-confirmed final count (nvmeConnect may have failed silently).
	finalLivePaths := r.getLivePathPairs()
	finalPathCount := countLivePathsForSubsystem(finalLivePaths, ipsByArrayInitiator)
	logger.Infof("NVMe-oFC EnsureLogin: final live paths=%d target=%d", finalPathCount, nvmeTargetPathCount)

	if finalPathCount == 0 {
		logger.Errorf("NVMe-oFC EnsureLogin: 0 live paths after all connect attempts — " +
			"check fabric connectivity and array zoning. NodeStageVolume will fail.")
		return
	}

	if finalPathCount < nvmeTargetPathCount {
		logger.Warningf("NVMe-oFC EnsureLogin: below target path count: final=%d target=%d, continuing with reduced redundancy",
			finalPathCount, nvmeTargetPathCount)
	}

	// Non-native NVMe + find_multipaths=on + < 2 paths: multipathd will not
	// create a dm device, causing GetMpathDevice to fail.
	nativeMpath, err := IsNvmeCoreMultipathEnabled(r.Executer)
	if err != nil {
		logger.Warningf("NVMe-oFC EnsureLogin: could not determine nvme_core multipath mode: %v", err)
		return
	}
	if !nativeMpath && finalPathCount < nvmeMinPathsForNonNativeDmMultipath {
		findMpathsOn, err := r.isFindMultipathsOn()
		if err != nil {
			logger.Warningf("NVMe-oFC EnsureLogin: could not read find_multipaths setting: %v", err)
			return
		}
		if findMpathsOn {
			logger.Errorf("NVMe-oFC EnsureLogin: non-native NVMe with find_multipaths=on requires >= %d paths "+
				"but only %d are live — multipathd will not create a dm device. "+
				"Set find_multipaths=no in /etc/multipath.conf or fix fabric connectivity.",
				nvmeMinPathsForNonNativeDmMultipath, finalPathCount)
		}
	}
}

// countLivePathsForSubsystem counts live paths whose traddr matches one of our array
// target ports. Both sides are normalized (strip "0x", lowercase) because the kernel's
// list-subsys output carries a "0x" prefix the publish-context targets lack.
func countLivePathsForSubsystem(livePaths map[string]bool, ipsByArrayInitiator map[string][]string) int {
	arrayTargets := make(map[string]bool, len(ipsByArrayInitiator))
	for target := range ipsByArrayInitiator {
		arrayTargets[normalizeTraddr(target)] = true
	}
	count := 0
	for pathKey := range livePaths {
		parts := strings.SplitN(pathKey, "|", 2)
		if len(parts) != 2 {
			continue
		}
		if arrayTargets[normalizeTraddr(parts[0])] {
			count++
		}
	}
	return count
}

// isFindMultipathsOn queries multipathd effective config for the find_multipaths setting.
func (r OsDeviceConnectivityNvmeOFc) isFindMultipathsOn() (bool, error) {
	out, err := r.Executer.ExecuteWithTimeout(TimeOutMultipathdCmd, multipathdCmd, []string{"show", "config"})
	if err != nil {
		return false, fmt.Errorf("multipathd show config failed: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "find_multipaths") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		val := strings.ToLower(fields[1])
		result := val == "yes" || val == "on"
		logger.Debugf("NVMe-oFC isFindMultipathsOn: find_multipaths=%s result=%v", val, result)
		return result, nil
	}
	// Not found — multipathd default is "no".
	return false, nil
}

// getLivePathPairs parses "nvme list-subsys" and returns a set of "traddr|host_traddr"
// strings for all currently live paths.
func (r OsDeviceConnectivityNvmeOFc) getLivePathPairs() map[string]bool {
	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", []string{"list-subsys"})
	if err != nil {
		logger.Warningf("NVMe-oFC getLivePathPairs: nvme list-subsys failed: %v", err)
		return map[string]bool{}
	}
	livePaths := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "+-") || !strings.Contains(trimmed, " live") {
			continue
		}
		traddr := extractNvmeField(trimmed, "traddr=")
		hostTraddr := extractNvmeField(trimmed, "host_traddr=")
		if traddr != "" && hostTraddr != "" {
			// The kernel emits addresses with a "0x" hex prefix; the publish-context
			// array targets and sysfs host ports do not. Normalize so the keys compare.
			livePaths[normalizeTraddr(traddr)+"|"+normalizeTraddr(hostTraddr)] = true
			logger.Debugf("NVMe-oFC getLivePathPairs: live path traddr=%s host_traddr=%s", traddr, hostTraddr)
		}
	}
	return livePaths
}

// extractNvmeField extracts a field value from an nvme list-subsys path line.
// e.g. extractNvmeField(line, "traddr=") returns "nn-5005...:pn-5005..."
func extractNvmeField(line, field string) string {
	idx := strings.Index(line, field)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(field):]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// discoverSubNqn runs "nvme discover" for one (arrayTargetPort, hostPort) pair
// and returns the storage subsystem NQN. Returns ("", nil) if no path exists.
func (r OsDeviceConnectivityNvmeOFc) discoverSubNqn(arrayTargetPort, hostPort string) (string, error) {
	args := []string{
		"discover",
		"--transport=" + nvmeTransportFC,
		"--traddr=" + arrayTargetPort,
		"--host-traddr=" + hostPort,
	}
	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
	if err != nil {
		logger.Debugf("NVMe-oFC discoverSubNqn: nvme discover failed target=%s host=%s: %v",
			arrayTargetPort, hostPort, err)
		return "", nil
	}
	subNqn := parseSubNqnFromDiscoverOutput(string(out))
	if subNqn != "" {
		logger.Debugf("NVMe-oFC discoverSubNqn: discovered subnqn=%s target=%s host=%s",
			subNqn, arrayTargetPort, hostPort)
	}
	return subNqn, nil
}

// parseSubNqnFromDiscoverOutput extracts the storage subsystem NQN from "nvme discover" output.
// Skips the discovery controller NQN (nqn.2014-08.org.nvmexpress.discovery).
func parseSubNqnFromDiscoverOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "subnqn:") {
			continue
		}
		// SplitN limit=2 preserves colons in the NQN itself.
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		nqn := strings.TrimSpace(parts[1])
		if nqn == "" || nqn == nvmeDiscoveryNqn {
			continue
		}
		return nqn
	}
	return ""
}

// nvmeConnect runs "nvme connect" for one (arrayTargetPort, hostPort, subNqn) combination.
func (r OsDeviceConnectivityNvmeOFc) nvmeConnect(arrayTargetPort, hostPort, subNqn string) bool {
	args := []string{
		"connect",
		"--transport=" + nvmeTransportFC,
		"--traddr=" + arrayTargetPort,
		"--host-traddr=" + hostPort,
		"--nqn=" + subNqn,
	}
	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
	if err != nil {
		// "already connected" is the steady state when host autoconnect brought the
		// path up (the norm in native mode) — the path IS live, so count it as success.
		if strings.Contains(string(out), "already connected") {
			logger.Debugf("NVMe-oFC nvmeConnect: path already connected NQN=%s target=%s host=%s",
				subNqn, arrayTargetPort, hostPort)
			return true
		}
		logger.Errorf("NVMe-oFC nvmeConnect: failed NQN=%s target=%s host=%s: %v output=%s",
			subNqn, arrayTargetPort, hostPort, err, string(out))
		return false
	}
	logger.Infof("NVMe-oFC nvmeConnect: connected NQN=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)
	return true
}

// getHostFCPorts reads node_name and port_name for every FC host adapter from sysfs
// and returns them as "nn-<node_name>:pn-<port_name>" strings for use as --host-traddr.
func (r OsDeviceConnectivityNvmeOFc) getHostFCPorts() ([]string, error) {
	portPaths, err := r.Executer.FilepathGlob(FCPortPath)
	if err != nil {
		return nil, fmt.Errorf("glob %s failed: %w", FCPortPath, err)
	}
	if len(portPaths) == 0 {
		return nil, fmt.Errorf("no FC host ports found under /sys/class/fc_host")
	}

	var hostPorts []string
	for _, portPath := range portPaths {
		hostDir := portPath[:strings.LastIndex(portPath, "/")]
		nodePath := hostDir + "/node_name"

		portBytes, err := r.Executer.IoutilReadFile(portPath)
		if err != nil {
			logger.Warningf("NVMe-oFC getHostFCPorts: cannot read %s: %v", portPath, err)
			continue
		}
		nodeBytes, err := r.Executer.IoutilReadFile(nodePath)
		if err != nil {
			logger.Warningf("NVMe-oFC getHostFCPorts: cannot read %s: %v", nodePath, err)
			continue
		}

		portName := strings.TrimPrefix(strings.TrimSpace(string(portBytes)), "0x")
		nodeName := strings.TrimPrefix(strings.TrimSpace(string(nodeBytes)), "0x")

		if portName == "" || nodeName == "" {
			logger.Warningf("NVMe-oFC getHostFCPorts: empty port/node name at %s, skipping", hostDir)
			continue
		}
		hostPorts = append(hostPorts, fmt.Sprintf("nn-%s:pn-%s", nodeName, portName))
	}

	if len(hostPorts) == 0 {
		return nil, fmt.Errorf("no valid host FC port pairs could be read from sysfs")
	}
	logger.Debugf("NVMe-oFC getHostFCPorts: found host ports: %v", hostPorts)
	return hostPorts, nil
}

// RescanDevices forces an NVMe namespace rescan on the array's already-connected
// controllers. nvme connect to an existing controller is a no-op and does not
// re-enumerate namespaces, so a LUN mapped after the controller was established
// only appears via a kernel AEN auto-rescan. When that AEN is missed, staging
// cannot discover the namespace. This mirrors the manual `nvme ns-rescan` recovery:
// it enumerates the controllers whose traddr matches the array target ports and
// rescans each, so a missed-AEN namespace becomes visible before GetMpathDevice.
//
// Best-effort and non-fatal by design: a rescan that finds nothing (or a transient
// per-controller failure) must not fail the stage — GetMpathDevice retries after,
// and the AEN may already have landed. arrayIdentifiers are the array target ports
// ("nn-WWNN:pn-WWPN"); lunId (NSID) is unused — all namespaces on the array
// controllers are rescanned, matching the proven manual recovery.
func (r OsDeviceConnectivityNvmeOFc) RescanDevices(_ int, arrayIdentifiers []string) error {
	if len(arrayIdentifiers) == 0 {
		logger.Warningf("NVMe-oFC RescanDevices: no array target ports provided, skipping namespace rescan")
		return nil
	}

	controllers := r.getArrayControllers(arrayIdentifiers)
	if len(controllers) == 0 {
		logger.Warningf("NVMe-oFC RescanDevices: no connected controllers matched array targets %v, "+
			"skipping namespace rescan", arrayIdentifiers)
		return nil
	}

	for _, controller := range controllers {
		devicePath := "/dev/" + controller
		logger.Infof("NVMe-oFC RescanDevices: rescanning namespaces on controller %s", devicePath)
		if out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", []string{"ns-rescan", devicePath}); err != nil {
			// Non-fatal: log and continue; GetMpathDevice retries discovery afterwards.
			logger.Warningf("NVMe-oFC RescanDevices: ns-rescan failed on %s: %v output=%s",
				devicePath, err, string(out))
			continue
		}
	}
	return nil
}

// getArrayControllers parses "nvme list-subsys" and returns the controller device
// names (e.g. "nvme0") whose traddr matches one of the array target ports. Only the
// array's controllers are rescanned so the local boot NVMe and unrelated subsystems
// are left untouched.
func (r OsDeviceConnectivityNvmeOFc) getArrayControllers(arrayIdentifiers []string) []string {
	arrayTraddrs := make(map[string]bool, len(arrayIdentifiers))
	for _, id := range arrayIdentifiers {
		arrayTraddrs[normalizeTraddr(id)] = true
	}

	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", []string{"list-subsys"})
	if err != nil {
		logger.Warningf("NVMe-oFC getArrayControllers: nvme list-subsys failed: %v", err)
		return nil
	}

	seen := map[string]bool{}
	var controllers []string
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "+-") {
			continue
		}
		fields := strings.Fields(trimmed)
		// Expected: "+- nvme0 fc traddr=... host_traddr=... live"
		if len(fields) < 2 {
			continue
		}
		controller := fields[1]
		traddr := normalizeTraddr(extractNvmeField(trimmed, "traddr="))
		if traddr == "" || !arrayTraddrs[traddr] {
			continue
		}
		if seen[controller] {
			continue
		}
		seen[controller] = true
		controllers = append(controllers, controller)
		logger.Debugf("NVMe-oFC getArrayControllers: matched controller=%s traddr=%s", controller, traddr)
	}
	return controllers
}

// normalizeTraddr lowercases and strips "0x" so target ports from the publish
// context ("nn-500...:pn-500...") match list-subsys traddrs regardless of whether
// the kernel emits the hex "0x" prefix.
func normalizeTraddr(traddr string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(traddr)), "0x", "")
}

// GetMpathDevice resolves the host block device for the volume. Under native NVMe
// multipath (nvme_core.multipath=Y) the kernel presents the volume as a single
// namespace-head /dev/nvmeXnY with no dm device, so the multipathd-based discovery
// finds nothing; resolve the head by its NGUID instead. Under dm-multipath (=N)
// delegate to the shared discovery unchanged. Scoped to NVMe/FC by dispatch, so SCSI
// and iSCSI volumes on a native-NVMe host are unaffected.
func (r OsDeviceConnectivityNvmeOFc) GetMpathDevice(volumeId string) (string, error) {
	native, err := IsNvmeCoreMultipathEnabled(r.Executer)
	if err != nil {
		logger.Warningf("NVMe-oFC GetMpathDevice: could not determine multipath mode: %v; using dm discovery", err)
	}
	if native {
		// Stage runs right after the namespace rescan, so wait for udev to settle.
		return DiscoverNativeNamespaceDevice(r.Executer, volumeId, WaitForMpathRetries)
	}
	return r.HelperScsiGeneric.GetMpathDevice(volumeId)
}

// DiscoverNativeNamespaceDevice finds the ANA namespace-head device for the volume by
// its NGUID. maxRetries bounds the wait while udev settles (stage passes the full retry
// budget right after an ns-rescan; post-stage callers, whose device already exists, pass
// 1). Returns the same MultipathDeviceNotFoundForVolumeError as the dm path when nothing
// is found, so callers (e.g. idempotent NodeUnstage) behave identically across modes.
// Exported so node-level callers without a connectivity dispatch can reuse it.
func DiscoverNativeNamespaceDevice(exec executer.ExecuterInterface, volumeId string, maxRetries int) (string, error) {
	nguid := convertScsiIdToNguid(strings.ToLower(volumeId))
	logger.Infof("DiscoverNativeNamespaceDevice: resolving native NVMe head for volume %s (nguid=%s)", volumeId, nguid)
	for i := 0; i < maxRetries; i++ {
		if device := resolveNativeNamespaceOnce(exec, nguid); device != "" {
			logger.Infof("DiscoverNativeNamespaceDevice: resolved volume %s to %s", volumeId, device)
			return device, nil
		}
		// Sleep only between attempts, not after the last — a single-shot lookup
		// (maxRetries=1, post-stage) returns immediately without a settle wait.
		if i < maxRetries-1 {
			time.Sleep(time.Second * time.Duration(WaitForMpathWaitIntervalSec))
		}
	}
	logger.Errorf("DiscoverNativeNamespaceDevice: no native NVMe namespace for volume %s (nguid=%s)", volumeId, nguid)
	return "", &MultipathDeviceNotFoundForVolumeError{volumeId}
}

// resolveNativeNamespaceOnce does one lookup: first the stable by-id symlink
// /dev/disk/by-id/nvme-eui.<nguid>, then a sysfs scan of /sys/block/nvme*/wwid (in case
// the udev symlink lags the rescan). Returns "" if the namespace is not yet present.
func resolveNativeNamespaceOnce(exec executer.ExecuterInterface, nguid string) string {
	byIdPath := nvmeByIdEuiPrefix + nguid
	if target, err := exec.OsReadlink(byIdPath); err == nil && target != "" {
		device := filepath.Join(DevPath, filepath.Base(target))
		logger.Debugf("resolveNativeNamespaceOnce: %s -> %s", byIdPath, device)
		return device
	}

	matches, err := exec.FilepathGlob(sysBlockNvmeWwidGlob)
	if err != nil {
		logger.Warningf("resolveNativeNamespaceOnce: glob %s failed: %v", sysBlockNvmeWwidGlob, err)
		return ""
	}
	for _, wwidPath := range matches {
		data, err := exec.IoutilReadFile(wwidPath)
		if err != nil {
			continue
		}
		if normalizeNguid(string(data)) == nguid {
			// wwidPath = /sys/block/<dev>/wwid → the device name is the parent dir.
			device := filepath.Join(DevPath, filepath.Base(filepath.Dir(wwidPath)))
			logger.Debugf("resolveNativeNamespaceOnce: sysfs %s matched -> %s", wwidPath, device)
			return device
		}
	}
	return ""
}

// normalizeNguid strips the "eui."/"nguid." prefix, dashes, and case so a sysfs wwid
// ("eui.5800…724") or a dashed sysfs nguid ("58000000-0000-…") compares equal to the
// undashed 32-hex NGUID that convertScsiIdToNguid derives from the SCSI volume UID.
func normalizeNguid(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "eui.")
	s = strings.TrimPrefix(s, "nguid.")
	return strings.ReplaceAll(s, "-", "")
}

// RescanNvmeNamespaceForResize triggers an NVMe controller rescan so the kernel re-reads
// a namespace's size after an array-side expand. Native multipath emits no dm device to
// resize, and the size only refreshes via a kernel AEN or an explicit rescan; without one
// the filesystem grow reads a stale size and under-expands. Rescans the controllers of the
// namespace head's subsystem (matched by subsysnqn, so unrelated subsystems are left
// alone). Best-effort: a missing subsysnqn or a per-controller failure is logged, not
// fatal — the subsequent grow still runs against whatever size the kernel reports.
func RescanNvmeNamespaceForResize(exec executer.ExecuterInterface, namespaceDevice string) error {
	subsysNqnPath := fmt.Sprintf(sysBlockDeviceSubsysNqnFmt, namespaceDevice)
	data, err := exec.IoutilReadFile(subsysNqnPath)
	if err != nil {
		logger.Warningf("RescanNvmeNamespaceForResize: cannot read %s: %v; relying on kernel AEN", subsysNqnPath, err)
		return nil
	}
	subsysNqn := strings.TrimSpace(string(data))

	controllers := findControllersBySubsysNqn(exec, subsysNqn)
	if len(controllers) == 0 {
		logger.Warningf("RescanNvmeNamespaceForResize: no controllers found for subsysnqn %s; relying on kernel AEN", subsysNqn)
		return nil
	}

	for _, controller := range controllers {
		devicePath := "/dev/" + controller
		logger.Infof("RescanNvmeNamespaceForResize: rescanning %s to refresh namespace %s size", devicePath, namespaceDevice)
		if out, err := exec.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", []string{"ns-rescan", devicePath}); err != nil {
			logger.Warningf("RescanNvmeNamespaceForResize: ns-rescan failed on %s: %v output=%s", devicePath, err, string(out))
		}
	}
	return nil
}

// findControllersBySubsysNqn returns the NVMe controller names (e.g. "nvme1") whose
// subsystem NQN equals targetNqn.
func findControllersBySubsysNqn(exec executer.ExecuterInterface, targetNqn string) []string {
	matches, err := exec.FilepathGlob(sysClassNvmeSubsysNqnGlob)
	if err != nil {
		logger.Warningf("findControllersBySubsysNqn: glob %s failed: %v", sysClassNvmeSubsysNqnGlob, err)
		return nil
	}
	var controllers []string
	for _, nqnPath := range matches {
		data, err := exec.IoutilReadFile(nqnPath)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == targetNqn {
			// nqnPath = /sys/class/nvme/<ctrl>/subsysnqn → controller is the parent dir.
			controllers = append(controllers, filepath.Base(filepath.Dir(nqnPath)))
		}
	}
	return controllers
}

func (r OsDeviceConnectivityNvmeOFc) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityNvmeOFc) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

func (r OsDeviceConnectivityNvmeOFc) RemoveGhostDevice(lun int) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(lun)
}

func (r OsDeviceConnectivityNvmeOFc) ValidateLun(_ int, _ []string) error {
	return nil
}

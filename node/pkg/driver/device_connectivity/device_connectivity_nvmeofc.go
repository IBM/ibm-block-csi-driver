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
    "strings"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

const (
    nvmeCmdTimeout   = 10 * 1000
    nvmeTransportFC  = "fc"
    nvmeDiscoveryNqn = "nqn.2014-08.org.nvmexpress.discovery"
	FCPortPath       = "/sys/class/fc_host/host*/port_name"
	nvmeTargetPathCount = 3
	nvmeMinPathsForNonNativeDmMultipath = 2
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

			pathKey := arrayTargetPort + "|" + hostPort
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
			r.nvmeConnect(arrayTargetPort, hostPort, subNqn)
			connectedPaths++
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
	nativeMpath, err := isNvmeCoreMultipathEnabled()
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

// countLivePathsForSubsystem counts live paths whose traddr matches one of our array target ports.
func countLivePathsForSubsystem(livePaths map[string]bool, ipsByArrayInitiator map[string][]string) int {
	count := 0
	for pathKey := range livePaths {
		parts := strings.SplitN(pathKey, "|", 2)
		if len(parts) != 2 {
			continue
		}
		if _, ok := ipsByArrayInitiator[parts[0]]; ok {
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
			livePaths[traddr+"|"+hostTraddr] = true
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
func (r OsDeviceConnectivityNvmeOFc) nvmeConnect(arrayTargetPort, hostPort, subNqn string) {
	args := []string{
		"connect",
		"--transport=" + nvmeTransportFC,
		"--traddr=" + arrayTargetPort,
		"--host-traddr=" + hostPort,
		"--nqn=" + subNqn,
	}
	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
	if err != nil {
		logger.Errorf("NVMe-oFC nvmeConnect: failed NQN=%s target=%s host=%s: %v output=%s",
			subNqn, arrayTargetPort, hostPort, err, string(out))
		return
	}
	logger.Infof("NVMe-oFC nvmeConnect: connected NQN=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)
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

func (r OsDeviceConnectivityNvmeOFc) RescanDevices(_ int, _ []string) error {
	return nil
}

func (r OsDeviceConnectivityNvmeOFc) GetMpathDevice(volumeId string) (string, error) {
	return r.HelperScsiGeneric.GetMpathDevice(volumeId)
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

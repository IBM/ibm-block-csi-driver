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
	"os"
	"path/filepath"
	"strings"
	"strconv"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"
)

const (
	nvmeCmdTimeout                      = 10 * 1000
	nvmeTransportFC                     = "fc"
	nvmeDiscoveryNqn                    = "nqn.2014-08.org.nvmexpress.discovery"
	recordSize         = 1024 // NVMe Spec discovery log page entry size
	FCPortPath                          = "/sys/class/fc_host/host*/port_name"
	nvmeTargetPathCount                 = 4 // Set according to your environment's requirements
	nvmeMinPathsForNonNativeDmMultipath = 2
)

type OsDeviceConnectivityNvmeOFc struct {
	Executer          executer.ExecuterInterface
	KeyedGater *executer.KeyedGater
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityNvmeOFc(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityNvmeOFc{
		Executer:          executer,
		KeyedGater: KeyedGater,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, KeyedGater, Mounter, clean_scsi_device),
	}
}


// EnsureLogin performs NVMe-oFC discovery and connect for each (arrayTargetPort, hostPort) pair.
// Connects paths until nvmeTargetPathCount is reached. Logs error if 0 paths result,
// warning if below target. For non-native NVMe with find_multipaths=on, logs error if < 2 paths.
func (r OsDeviceConnectivityNvmeOFc) EnsureLogin(ctx context.Context, ipsByArrayInitiator map[string][]string) {
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

                        subNqn, err := r.discoverSubNqn(ctx, arrayTargetPort, hostPort)
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
                        if r.nvmeConnect(ctx, arrayTargetPort, hostPort, subNqn) {
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
        nativeMpath, err := isNvmeCoreMultipathEnabled(ctx)
        if err != nil {
                logger.Warningf("NVMe-oFC EnsureLogin: could not determine nvme_core multipath mode: %v", err)
                return
        }
        if !nativeMpath && finalPathCount < nvmeMinPathsForNonNativeDmMultipath {
                findMpathsOn, err := r.isFindMultipathsOn(ctx)
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

func (r OsDeviceConnectivityNvmeOFc) updateHostIDs(hostIDs map[int]bool) {
        // 1. Get a map of HostNumber -> PhysicalIdentifier (PCI or WWPN)
        hostMap, err := r.mapHostsToPhysicalHardware()
        if err != nil {
                logger.Errorf("Failed to map FC hosts: %v", err)
                return
        }

        // 2. Identify the hardware IDs associated with the hosts we already have
        knownHardware := make(map[string]bool)
        for hostNum := range hostIDs {
                if hardwareID, exists := hostMap[hostNum]; exists {
                        knownHardware[hardwareID] = true
                }
        }

        // 3. Find siblings: any host sharing the same hardwareID but not yet in our set
        for hostNum, hardwareID := range hostMap {
                if knownHardware[hardwareID] && !hostIDs[hostNum] {
                        hostIDs[hostNum] = true
                        logger.Infof("Multipath discovery: host%d shares physical hardware %s", hostNum, hardwareID)
                }
        }
}

func (r OsDeviceConnectivityNvmeOFc) mapHostsToPhysicalHardware() (map[int]string, error) {
	hostMap := make(map[int]string)
	baseClassPath := "/sys/class/fc_host"

	// OPTIMIZED: Replace the heavy wildcard filepath.Glob sequence with an ultra-fast, 
	// direct directory list of the flat in-memory class folder.
	entries, err := os.ReadDir(baseClassPath)
	if err != nil {
		if os.IsNotExist(err) {
			return hostMap, nil // Return empty map smoothly if FC is unmounted/absent
		}
		return nil, err
	}

	for _, entry := range entries {
		name := entry.Name()
		// Match precisely against exactly named target host slots
		if !strings.HasPrefix(name, "host") {
			continue
		}

		hostNumStr := strings.TrimPrefix(name, "host")
		hostNum, _ := strconv.Atoi(hostNumStr)

		h := filepath.Join(baseClassPath, name)

		// Option A: Use PCI Address (Best for Multi-port/Multi-channel cards)
		// /sys/class/fc_host/hostX/device/ -> ../../../0000:04:00.0
		pciLink, err := os.Readlink(filepath.Join(h, "device"))
		if err == nil {
			// Extract the PCI slot (e.g., 0000:04:00.0)
			hostMap[hostNum] = filepath.Base(pciLink)
			continue
		}

		// Option B: Use Physical WWPN (Best for NPIV)
		// permanent_port_name is the physical burned-in WWN of the HBA
		wwpn, err := os.ReadFile(filepath.Join(h, "permanent_port_name"))
		if err == nil {
			hostMap[hostNum] = strings.TrimSpace(string(wwpn))
		}
	}
	return hostMap, nil
}

// isFindMultipathsOn queries multipathd effective config for the find_multipaths setting.
func (r OsDeviceConnectivityNvmeOFc) isFindMultipathsOn(ctx context.Context) (bool, error) {
        // Req 3 & 4: Use the existing socket-based Executer instead of forking 'multipathd'
        // 'show config' is too large; 'show daemon' or specific queries are lighter if supported,
        // but using the socket is already a massive win.
        out, err := r.Executer.MultipathdCmd(ctx, "global", "show config")
        if err != nil {
                return false, fmt.Errorf("multipathd socket query failed: %w", err)
        }

        // Req 8: Context is already respected by your MultipathdCmd infra
        for _, line := range strings.Split(out, "\n") {
                trimmed := strings.TrimSpace(line)
                if !strings.HasPrefix(trimmed, "find_multipaths") {
                        continue
                }

                fields := strings.Fields(trimmed)
                if len(fields) < 2 {
                        continue
                }

                // Handle "quoted" values often found in multipathd config
                val := strings.ToLower(strings.Trim(fields[1], "\""))

                // In RHEL 7 and newer, valid "on" values include yes, on, or smart
                result := val == "yes" || val == "on" || val == "smart"

                logger.Debugf("NVMe-oFC isFindMultipathsOn: find_multipaths=%s result=%v", val, result)
                return result, nil
        }

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
func (r *OsDeviceConnectivityNvmeOFc) discoverSubNqn(ctx context.Context, arrayTargetPort, hostPort string) (string, error) {
        args := []string{
                "discover",
                "--transport=" + nvmeTransportFC,
                "--traddr=" + arrayTargetPort,
                "--host-traddr=" + hostPort,
        }
		output := ""
		r.KeyedGater.ExecuteNvmeFabric(ctx, func() error {
			out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
			if err != nil {
					logger.Debugf("NVMe-oFC discoverSubNqn: nvme discover failed target=%s host=%s: %v",
							arrayTargetPort, hostPort, err)
					return nil
			}
			subNqn := parseSubNqnFromDiscoverOutput(string(out))
			if subNqn != "" {
					logger.Debugf("NVMe-oFC discoverSubNqn: discovered subnqn=%s target=%s host=%s",
							subNqn, arrayTargetPort, hostPort)
				output = subNqn
			}
			return nil
		})
		return output, nil
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
func (r *OsDeviceConnectivityNvmeOFc) nvmeConnect(ctx context.Context, arrayTargetPort, hostPort, subNqn string) bool {
        args := []string{
                "connect",
                "--transport=" + nvmeTransportFC,
                "--traddr=" + arrayTargetPort,
                "--host-traddr=" + hostPort,
                "--nqn=" + subNqn,
        }
		err := r.KeyedGater.ExecuteNvmeFabric(ctx, func() error {
			out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
			if err != nil {
					logger.Errorf("NVMe-oFC nvmeConnect: failed NQN=%s target=%s host=%s: %v output=%s",
							subNqn, arrayTargetPort, hostPort, err, string(out))
					return err
			}
			logger.Infof("NVMe-oFC nvmeConnect: connected NQN=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)
			return nil
		})
		return err == nil
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

func (r OsDeviceConnectivityNvmeOFc) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {
	return r.HelperScsiGeneric.GetMpathDevice(ctx, volumeId)
}

func (r OsDeviceConnectivityNvmeOFc) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(ctx, sysDevices)
}

func (r OsDeviceConnectivityNvmeOFc) RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(ctx, expectedSerial, expectedLun, arrayIdentifiers)
}

// TODO
func (r OsDeviceConnectivityNvmeOFc) ValidateLun(_ context.Context, _ string, _ int, _ []string, _ string) error {
	return nil
}

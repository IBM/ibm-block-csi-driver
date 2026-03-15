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
    nvmeCmdTimeout  = 30 * 1000
    nvmeTransportFC = "fc"
    nvmeDiscoveryNqn = "nqn.2014-08.org.nvmexpress.discovery"
    FCPortPath = "/sys/class/fc_host/host*/port_name"

    // nvmeTargetPathCount is the desired number of live NVMe-oFC paths per
    // subsystem. EnsureLogin connects paths until this count is reached.
    // Minimum for redundancy is 2 (one per array node, one per HBA).
    // Developers can raise this for more path redundancy.
    nvmeTargetPathCount = 3

    // nvmeMinPathsForNonNativeDmMultipath is the minimum number of live paths
    // required when multipathd find_multipaths is "on" and NVMe non-native
    // multipath is in use. With find_multipaths on, multipathd only creates a
    // dm device when >= 2 paths exist. With 1 path, no dm device is created
    // and GetMpathDevice will fail.
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

// EnsureLogin performs NVMe-oFC discovery and connect.
//
// ipsByArrayInitiator keys are array-side target ports: "nn-<WWNN>:pn-<WWPN>"
// These come from PublishContext key PUBLISH_CONTEXT_ARRAY_NVME_INITIATORS,
// parsed in GetInfoFromPublishContext — exactly how FC uses PUBLISH_CONTEXT_ARRAY_FC_INITIATORS
//
// For each array target port, we try every local host FC port as --host-traddr.
func (r OsDeviceConnectivityNvmeOFc) EnsureLogin(ipsByArrayInitiator map[string][]string) {
    logger.Infof("parth EnsureLogin: START ipsByArrayInitiator=%v", ipsByArrayInitiator)

    if len(ipsByArrayInitiator) == 0 {
        logger.Warningf("parth EnsureLogin: no array target ports, skipping")
        logger.Infof("parth EnsureLogin: END no targets")
        return
    }

    hostPorts, err := r.getHostFCPorts()
    if err != nil || len(hostPorts) == 0 {
        logger.Errorf("parth EnsureLogin: failed to read host FC ports: %v", err)
        logger.Infof("parth EnsureLogin: END with error")
        return
    }
    logger.Infof("parth EnsureLogin: host FC ports=%v", hostPorts)

    livePaths := r.getLivePathPairs()
    logger.Infof("parth EnsureLogin: live path pairs=%v", livePaths)

    currentPaths := countLivePathsForSubsystem(livePaths, ipsByArrayInitiator)
    logger.Infof("parth EnsureLogin: current live paths=%d target=%d",
        currentPaths, nvmeTargetPathCount)

    connectedPaths := currentPaths
    for arrayTargetPort := range ipsByArrayInitiator {
        if connectedPaths >= nvmeTargetPathCount {
            logger.Infof("parth EnsureLogin: reached target path count, stopping")
            break
        }

        for _, hostPort := range hostPorts {
            if connectedPaths >= nvmeTargetPathCount {
                break
            }

            pathKey := arrayTargetPort + "|" + hostPort
            if livePaths[pathKey] {
                logger.Infof("parth EnsureLogin: path already live target=%s host=%s, skipping",
                    arrayTargetPort, hostPort)
                continue
            }

            subNqn, err := r.discoverSubNqn(arrayTargetPort, hostPort)
            if err != nil {
                logger.Errorf("parth EnsureLogin: discoverSubNqn failed target=%s host=%s error=%v",
                    arrayTargetPort, hostPort, err)
                continue
            }

            if subNqn == "" {
                logger.Infof("parth EnsureLogin: no subnqn found target=%s host=%s, skipping",
                    arrayTargetPort, hostPort)
                continue
            }

            logger.Infof("parth EnsureLogin: connecting NQN=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)
            r.nvmeConnect(arrayTargetPort, hostPort, subNqn)
            connectedPaths++
        }
    }

    finalLivePaths := r.getLivePathPairs()
    finalPathCount := countLivePathsForSubsystem(finalLivePaths, ipsByArrayInitiator)
    logger.Infof("parth EnsureLogin: final live path count=%d target=%d", finalPathCount, nvmeTargetPathCount)

    if finalPathCount == 0 {
        logger.Errorf("parth EnsureLogin: FATAL: 0 live paths, NodeStageVolume will fail")
    } else if finalPathCount < nvmeTargetPathCount {
        logger.Warningf("parth EnsureLogin: reduced redundancy final=%d target=%d", finalPathCount, nvmeTargetPathCount)
    }

    logger.Infof("parth EnsureLogin: END")
}

// countLivePathsForSubsystem counts how many entries in livePaths have a
// traddr matching one of the array target ports for this subsystem.
// livePaths keys are "traddr|host_traddr" from getLivePathPairs.
// ipsByArrayInitiator keys are the array target ports from PublishContext.
func countLivePathsForSubsystem(livePaths map[string]bool, ipsByArrayInitiator map[string][]string) int {
    count := 0
    for pathKey := range livePaths {
        // pathKey = "nn-5005...:pn-5005...|nn-2000...:pn-2100..."
        parts := strings.SplitN(pathKey, "|", 2)
        if len(parts) != 2 {
            continue
        }
        traddr := parts[0]
        if _, ok := ipsByArrayInitiator[traddr]; ok {
            count++
        }
    }
    return count
}

// isFindMultipathsOn queries multipathd for its effective configuration and
// returns true if find_multipaths is set to "yes" or "on".
// This is used to detect the configuration that prevents dm device creation
// with a single NVMe path in non-native multipath mode.
func (r OsDeviceConnectivityNvmeOFc) isFindMultipathsOn() (bool, error) {
    logger.Infof("parth isFindMultipathsOn: START")

    out, err := r.Executer.ExecuteWithTimeout(TimeOutMultipathdCmd, multipathdCmd, []string{"show", "config"})
    if err != nil {
        logger.Errorf("parth isFindMultipathsOn: multipathd show config failed: %v", err)
        logger.Infof("parth isFindMultipathsOn: END with error")
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
        logger.Infof("parth isFindMultipathsOn: found find_multipaths=%s result=%v", val, result)
        logger.Infof("parth isFindMultipathsOn: END")
        return result, nil
    }

    logger.Infof("parth isFindMultipathsOn: not found in config, default=false")
    logger.Infof("parth isFindMultipathsOn: END")
    return false, nil
}

// getLivePathPairs parses nvme list-subsys and returns a set of
// "traddr|host_traddr" strings for all currently live paths.
//
// Path line format:
//   +- nvme0 fc traddr=nn-5005...:pn-5005...,host_traddr=nn-2000...:pn-2100... live
func (r OsDeviceConnectivityNvmeOFc) getLivePathPairs() map[string]bool {
    logger.Infof("parth getLivePathPairs: START")

    args := []string{"list-subsys"}
    out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
    if err != nil {
        logger.Errorf("parth getLivePathPairs: nvme list-subsys failed: %v", err)
        logger.Infof("parth getLivePathPairs: END with empty result")
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
            logger.Infof("parth getLivePathPairs: found live path traddr=%s hostTraddr=%s", traddr, hostTraddr)
        }
    }

    logger.Infof("parth getLivePathPairs: END livePaths=%v", livePaths)
    return livePaths
}

// extractNvmeField extracts a field value from an nvme list-subsys path line.
// e.g. extractNvmeField(line, "traddr=") on:
//   "+- nvme0 fc traddr=nn-5005...:pn-5005...,host_traddr=nn-2000...:pn-2100... live"
// returns "nn-5005...:pn-5005..."
func extractNvmeField(line, field string) string {
    idx := strings.Index(line, field)
    if idx < 0 {
        return ""
    }
    rest := line[idx+len(field):]
    // value ends at next comma or space
    end := strings.IndexAny(rest, ", ")
    if end < 0 {
        return rest
    }
    return rest[:end]
}

// discoverSubNqn runs nvme discover for one (arrayTargetPort, hostPort) pair
// and returns the storage subsystem NQN.
// Command: nvme discover --transport=fc --traddr=<arrayTargetPort> --host-traddr=<hostPort>
func (r OsDeviceConnectivityNvmeOFc) discoverSubNqn(arrayTargetPort, hostPort string) (string, error) {
    logger.Infof("parth discoverSubNqn: START target=%s host=%s", arrayTargetPort, hostPort)

    args := []string{
        "discover",
        "--transport=" + nvmeTransportFC,
        "--traddr=" + arrayTargetPort,
        "--host-traddr=" + hostPort,
    }

    out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
    if err != nil {
        logger.Infof("parth discoverSubNqn: nvme discover failed (no path?) target=%s host=%s error=%v",
            arrayTargetPort, hostPort, err)
        logger.Infof("parth discoverSubNqn: END no NQN found")
        return "", nil
    }

    subNqn := parseSubNqnFromDiscoverOutput(string(out))
    logger.Infof("parth discoverSubNqn: END subNqn=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)
    return subNqn, nil
}

// parseSubNqnFromDiscoverOutput extracts subnqn from nvme discover output.
// Looks for lines like:
//   subnqn:  nqn.1986-03.com.ibm:nvme:2145.000002043D607F18
// Skips the discovery controller NQN (nqn.2014-08.org.nvmexpress.discovery).
func parseSubNqnFromDiscoverOutput(output string) string {
    for _, line := range strings.Split(output, "\n") {
        trimmed := strings.TrimSpace(line)
        if !strings.HasPrefix(trimmed, "subnqn:") {
            continue
        }
        // "subnqn:  nqn.1986-03.com.ibm:nvme:2145.xxx"
        // SplitN with limit 2 so the rest of the NQN (which has colons) is preserved.
        parts := strings.SplitN(trimmed, ":", 2)
        if len(parts) != 2 {
            continue
        }
        nqn := strings.TrimSpace(parts[1])
        if nqn == "" || nqn == nvmeDiscoveryNqn {
            continue
        }
        logger.Debugf("NVMe-oFC: discovered subnqn: %s", nqn)
        return nqn
    }
    return ""
}

// nvmeConnect runs nvme connect for one (arrayTargetPort, hostPort, subNqn) combination.
//
// Command: nvme connect --transport=fc --traddr=<arrayTargetPort>
//           --host-traddr=<hostPort> --nqn=<subNqn>
func (r OsDeviceConnectivityNvmeOFc) nvmeConnect(arrayTargetPort, hostPort, subNqn string) {
    logger.Infof("parth nvmeConnect: START NQN=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)

    args := []string{
        "connect",
        "--transport=" + nvmeTransportFC,
        "--traddr=" + arrayTargetPort,
        "--host-traddr=" + hostPort,
        "--nqn=" + subNqn,
    }

    out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
    if err != nil {
        logger.Errorf("parth nvmeConnect: connect failed NQN=%s target=%s host=%s error=%v output=%s",
            subNqn, arrayTargetPort, hostPort, err, string(out))
        logger.Infof("parth nvmeConnect: END with error")
        return
    }

    logger.Infof("parth nvmeConnect: connected NQN=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)
    logger.Infof("parth nvmeConnect: END")
}

// getHostFCPorts reads /sys/class/fc_host/hostN/node_name and port_name for
// every FC host adapter and returns them as "nn-<node_name>:pn-<port_name>" strings.
//
// These are passed as --host-traddr to nvme discover and nvme connect.
func (r OsDeviceConnectivityNvmeOFc) getHostFCPorts() ([]string, error) {
    logger.Infof("parth getHostFCPorts: START")

    portPaths, err := r.Executer.FilepathGlob(FCPortPath)
    if err != nil {
        logger.Errorf("parth getHostFCPorts: glob %s failed: %v", FCPortPath, err)
        logger.Infof("parth getHostFCPorts: END with error")
        return nil, fmt.Errorf("glob %s failed: %w", FCPortPath, err)
    }

    if len(portPaths) == 0 {
        logger.Errorf("parth getHostFCPorts: no FC host ports found")
        logger.Infof("parth getHostFCPorts: END with error")
        return nil, fmt.Errorf("no FC host ports found under /sys/class/fc_host")
    }

    var hostPorts []string
    for _, portPath := range portPaths {
        hostDir := portPath[:strings.LastIndex(portPath, "/")]
        nodePath := hostDir + "/node_name"

        portBytes, err := r.Executer.IoutilReadFile(portPath)
        if err != nil {
            logger.Warningf("parth getHostFCPorts: cannot read port %s: %v", portPath, err)
            continue
        }
        nodeBytes, err := r.Executer.IoutilReadFile(nodePath)
        if err != nil {
            logger.Warningf("parth getHostFCPorts: cannot read node %s: %v", nodePath, err)
            continue
        }

        portName := strings.TrimPrefix(strings.TrimSpace(string(portBytes)), "0x")
        nodeName := strings.TrimPrefix(strings.TrimSpace(string(nodeBytes)), "0x")

        if portName == "" || nodeName == "" {
            logger.Warningf("parth getHostFCPorts: empty port/node name at %s, skipping", hostDir)
            continue
        }

        hostPort := fmt.Sprintf("nn-%s:pn-%s", nodeName, portName)
        hostPorts = append(hostPorts, hostPort)
        logger.Infof("parth getHostFCPorts: found hostPort=%s", hostPort)
    }

    if len(hostPorts) == 0 {
        logger.Errorf("parth getHostFCPorts: no valid host FC port pairs could be read")
        logger.Infof("parth getHostFCPorts: END with error")
        return nil, fmt.Errorf("no valid host FC port pairs could be read from sysfs")
    }

    logger.Infof("parth getHostFCPorts: END hostPorts=%v", hostPorts)
    return hostPorts, nil
}

func (r OsDeviceConnectivityNvmeOFc) RescanDevices(_ int, _ []string) error {
    logger.Infof("parth RescanDevices: START (no-op)")
    logger.Infof("parth RescanDevices: END")
    return nil
}

func (r OsDeviceConnectivityNvmeOFc) GetMpathDevice(volumeId string) (string, error) {
    logger.Infof("parth GetMpathDevice: START volumeId=%s", volumeId)

    mpathDevice, err := r.HelperScsiGeneric.GetMpathDevice(volumeId)
    if err != nil {
        logger.Errorf("parth GetMpathDevice: GetMpathDevice failed volumeId=%s error=%v", volumeId, err)
        logger.Infof("parth GetMpathDevice: END with error")
        return "", err
    }

    logger.Infof("parth GetMpathDevice: extracted mpathDevice=%s", mpathDevice)
    logger.Infof("parth GetMpathDevice: END")
    return mpathDevice, nil
}

func (r OsDeviceConnectivityNvmeOFc) FlushMultipathDevice(mpathDevice string) error {
    logger.Infof("parth FlushMultipathDevice: START mpathDevice=%s", mpathDevice)

    err := r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
    if err != nil {
        logger.Errorf("parth FlushMultipathDevice: FlushMultipathDevice failed mpathDevice=%s error=%v",
            mpathDevice, err)
        logger.Infof("parth FlushMultipathDevice: END with error")
        return err
    }

    logger.Infof("parth FlushMultipathDevice: END")
    return nil
}

func (r OsDeviceConnectivityNvmeOFc) RemovePhysicalDevice(sysDevices []string) error {
    logger.Infof("parth RemovePhysicalDevice: START sysDevices=%v", sysDevices)

    err := r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
    if err != nil {
        logger.Errorf("parth RemovePhysicalDevice: RemovePhysicalDevice failed sysDevices=%v error=%v",
            sysDevices, err)
        logger.Infof("parth RemovePhysicalDevice: END with error")
        return err
    }

    logger.Infof("parth RemovePhysicalDevice: END")
    return nil
}

func (r OsDeviceConnectivityNvmeOFc) RemoveGhostDevice(lun int) error {
    logger.Infof("parth RemoveGhostDevice: START lun=%d", lun)

    err := r.HelperScsiGeneric.RemoveGhostDevice(lun)
    if err != nil {
        logger.Errorf("parth RemoveGhostDevice: RemoveGhostDevice failed lun=%d error=%v", lun, err)
        logger.Infof("parth RemoveGhostDevice: END with error")
        return err
    }

    logger.Infof("parth RemoveGhostDevice: END")
    return nil
}

func (r OsDeviceConnectivityNvmeOFc) ValidateLun(_ int, _ []string) error {
    logger.Infof("parth ValidateLun: START (no-op)")
    logger.Infof("parth ValidateLun: END")
    return nil
}

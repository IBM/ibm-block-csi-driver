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
    "time"

    "github.com/ibm/ibm-block-csi-driver/node/logger"
    "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

const (
    nvmeCmdTimeout           = 30 * 1000 // milliseconds
    nvmeConnectRetries       = 3
    nvmeTransportFC          = "fc"
    nvmeDiscoveryNqn         = "nqn.2014-08.org.nvmexpress.discovery"
	FCPortPath = "/sys/class/fc_host/host*/port_name"
)

var nvmeConnectRetryInterval = 2 * time.Second

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
    if len(ipsByArrayInitiator) == 0 {
        logger.Warningf("NVMe-oFC EnsureLogin: no array target ports in publish context, skipping")
        return
    }

    // Read host-side FC port pairs from sysfs.
    // We need both node_name and port_name to form --host-traddr=nn-X:pn-Y.
    hostPorts, err := r.getHostFCPorts()
    if err != nil || len(hostPorts) == 0 {
        logger.Errorf("NVMe-oFC EnsureLogin: failed to read host FC ports: %v", err)
        return
    }
    logger.Debugf("NVMe-oFC EnsureLogin: host FC ports: %v", hostPorts)

    // Check which NQNs are already live — idempotency, same as iSCSI's filterLoggedIn.
    existingNqns, err := r.getConnectedSubsystems()
    if err != nil {
        logger.Warningf("NVMe-oFC EnsureLogin: could not check existing connections, proceeding anyway: %v", err)
        existingNqns = map[string]bool{}
    }
    logger.Debugf("NVMe-oFC EnsureLogin: already live NQNs: %v", existingNqns)

    for arrayTargetPort := range ipsByArrayInitiator {
        // arrayTargetPort = "nn-5005076810003F8C:pn-50050768101B3F8C"
        // This is exactly like iSCSI's targetName loop over portalsByTarget.
        for _, hostPort := range hostPorts {
            // hostPort = "nn-0x2000f4e9d456d851:pn-0x2100f4e9d456d851"
            // This is the host-side equivalent of iSCSI's portal IP.
            subNqn, err := r.discoverSubNqn(arrayTargetPort, hostPort)
            if err != nil {
                logger.Debugf("NVMe-oFC: discover error target=%s host=%s: %v",
                    arrayTargetPort, hostPort, err)
                continue
            }
            if subNqn == "" {
                // No path between this host port and this array port — normal for some pairs.
                logger.Debugf("NVMe-oFC: no subnqn found target=%s host=%s, skipping",
                    arrayTargetPort, hostPort)
                continue
            }

            if existingNqns[subNqn] {
                // Already connected and live — same as iSCSI's filterLoggedIn skipping.
                logger.Debugf("NVMe-oFC: already live NQN=%s, skipping connect", subNqn)
                continue
            }

            logger.Infof("NVMe-oFC: connecting NQN=%s target=%s host=%s",
                subNqn, arrayTargetPort, hostPort)
            r.nvmeConnect(arrayTargetPort, hostPort, subNqn)
        }
    }
}

// discoverSubNqn runs nvme discover for one (arrayTargetPort, hostPort) pair
// and returns the storage subsystem NQN.
// Command: nvme discover --transport=fc --traddr=<arrayTargetPort> --host-traddr=<hostPort>
func (r OsDeviceConnectivityNvmeOFc) discoverSubNqn(arrayTargetPort, hostPort string) (string, error) {
    args := []string{
        "discover",
        "--transport=" + nvmeTransportFC,
        "--traddr=" + arrayTargetPort,
        "--host-traddr=" + hostPort,
    }

    out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
    if err != nil {
        // Failure on a specific port pair is normal when there's no fabric path.
        // Not an error worth propagating — just means no path here.
        logger.Debugf("NVMe-oFC: nvme discover failed (no path?) target=%s host=%s: %v",
            arrayTargetPort, hostPort, err)
        return "", nil
    }

    return parseSubNqnFromDiscoverOutput(string(out)), nil
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
    args := []string{
        "connect",
        "--transport=" + nvmeTransportFC,
        "--traddr=" + arrayTargetPort,
        "--host-traddr=" + hostPort,
        "--nqn=" + subNqn,
    }

    for attempt := 1; attempt <= nvmeConnectRetries; attempt++ {
        out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
        if err == nil {
            logger.Infof("NVMe-oFC: connected NQN=%s target=%s (attempt %d)",
                subNqn, arrayTargetPort, attempt)
            return
        }
        logger.Warningf("NVMe-oFC: connect attempt %d/%d failed NQN=%s target=%s host=%s: %v output=%s",
            attempt, nvmeConnectRetries, subNqn, arrayTargetPort, hostPort, err, string(out))
        if attempt < nvmeConnectRetries {
            time.Sleep(nvmeConnectRetryInterval)
        }
    }
    logger.Errorf("NVMe-oFC: all %d connect attempts failed NQN=%s target=%s host=%s",
        nvmeConnectRetries, subNqn, arrayTargetPort, hostPort)
}

// getConnectedSubsystems runs nvme list-subsys and returns the set of NQNs
// that have at least one live path.
//
// Analogous to iSCSI's getAllSessions() which parses iscsiadm -m session output.
// Used for idempotency — skip NQNs already connected, just like iSCSI's filterLoggedIn.
//
// nvme list-subsys output format:
//   nvme-subsys1 - NQN=nqn.1986-03.com.ibm:nvme:2145.000002043F20D9BC
//                  hostnqn=nqn.2014-08.org.nvmexpress:uuid:...
//   \
//    +- nvme1 fc traddr=nn-...:pn-...,host_traddr=nn-...:pn-... live
func (r OsDeviceConnectivityNvmeOFc) getConnectedSubsystems() (map[string]bool, error) {
    args := []string{"list-subsys"}
    out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
    if err != nil {
        return nil, fmt.Errorf("nvme list-subsys failed: %w", err)
    }

    liveNqns := make(map[string]bool)
    currentNqn := ""

    for _, line := range strings.Split(string(out), "\n") {
        trimmed := strings.TrimSpace(line)

        // NQN line: "nvme-subsys1 - NQN=nqn.1986-03.com.ibm:nvme:2145.xxx"
        if idx := strings.Index(trimmed, "NQN="); idx >= 0 {
            rest := trimmed[idx+4:] // everything after "NQN="
            // The NQN is the first whitespace-delimited token
            fields := strings.Fields(rest)
            if len(fields) > 0 {
                currentNqn = fields[0]
            }
            continue
        }

        // Path line: "+- nvme1 fc traddr=...,host_traddr=... live"
        // "live" appears at end of line when path is active.
        if strings.HasPrefix(trimmed, "+-") && strings.Contains(trimmed, " live") {
            if currentNqn != "" && currentNqn != nvmeDiscoveryNqn {
                liveNqns[currentNqn] = true
            }
        }
    }

    return liveNqns, nil
}

// getHostFCPorts reads /sys/class/fc_host/hostN/node_name and port_name for
// every FC host adapter and returns them as "nn-<node_name>:pn-<port_name>" strings.
//
// These are passed as --host-traddr to nvme discover and nvme connect.
func (r OsDeviceConnectivityNvmeOFc) getHostFCPorts() ([]string, error) {
    portPaths, err := r.Executer.FilepathGlob(FCPortPath)
    if err != nil {
        return nil, fmt.Errorf("NVMe-oFC: glob %s failed: %w", FCPortPath, err)
    }
    if len(portPaths) == 0 {
        return nil, fmt.Errorf("NVMe-oFC: no FC host ports found under /sys/class/fc_host")
    }

    var hostPorts []string
    for _, portPath := range portPaths {
        // portPath = "/sys/class/fc_host/hostN/port_name"
        // nodePath = "/sys/class/fc_host/hostN/node_name"
        hostDir := portPath[:strings.LastIndex(portPath, "/")]
        nodePath := hostDir + "/node_name"

        portBytes, err := r.Executer.IoutilReadFile(portPath)
        if err != nil {
            logger.Warningf("NVMe-oFC: cannot read %s: %v", portPath, err)
            continue
        }
        nodeBytes, err := r.Executer.IoutilReadFile(nodePath)
        if err != nil {
            logger.Warningf("NVMe-oFC: cannot read %s: %v", nodePath, err)
            continue
        }

        // nvme CLI expects: nn-2000f4e9d456d851:pn-2100f4e9d456d851 (no 0x)
        portName := strings.TrimPrefix(strings.TrimSpace(string(portBytes)), "0x")
        nodeName := strings.TrimPrefix(strings.TrimSpace(string(nodeBytes)), "0x")

        if portName == "" || nodeName == "" {
            logger.Warningf("NVMe-oFC: empty port/node name at %s, skipping", hostDir)
            continue
        }

        // Result: "nn-2000f4e9d456d851:pn-2100f4e9d456d851"
        hostPorts = append(hostPorts, fmt.Sprintf("nn-%s:pn-%s", nodeName, portName))
    }

    if len(hostPorts) == 0 {
        return nil, fmt.Errorf("NVMe-oFC: no valid host FC port pairs could be read from sysfs")
    }
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

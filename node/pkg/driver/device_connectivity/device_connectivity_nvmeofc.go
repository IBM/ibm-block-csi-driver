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
    nvmeCmdTimeout           = 10 * 1000
    nvmeTransportFC          = "fc"
    nvmeDiscoveryNqn         = "nqn.2014-08.org.nvmexpress.discovery"
	FCPortPath = "/sys/class/fc_host/host*/port_name"
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

    for arrayTargetPort := range ipsByArrayInitiator {
        for _, hostPort := range hostPorts {
            pathKey := arrayTargetPort + "|" + hostPort
            if livePaths[pathKey] {
                logger.Debugf("NVMe-oFC: path already live target=%s host=%s, skipping",
                    arrayTargetPort, hostPort)
                continue
            }

            subNqn, err := r.discoverSubNqn(arrayTargetPort, hostPort)
            if err != nil {
                logger.Debugf("NVMe-oFC: discover error target=%s host=%s: %v",
                    arrayTargetPort, hostPort, err)
                continue
            }
            if subNqn == "" {
                logger.Debugf("NVMe-oFC: no subnqn found target=%s host=%s, skipping",
                    arrayTargetPort, hostPort)
                continue
            }

            logger.Infof("NVMe-oFC: connecting NQN=%s target=%s host=%s",
                subNqn, arrayTargetPort, hostPort)
            r.nvmeConnect(arrayTargetPort, hostPort, subNqn)
        }
    }
}

func (r OsDeviceConnectivityNvmeOFc) getLivePathPairs() map[string]bool {
    args := []string{"list-subsys"}
    out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
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
        }
    }
    return livePaths
}

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

func (r OsDeviceConnectivityNvmeOFc) discoverSubNqn(arrayTargetPort, hostPort string) (string, error) {
    args := []string{
        "discover",
        "--transport=" + nvmeTransportFC,
        "--traddr=" + arrayTargetPort,
        "--host-traddr=" + hostPort,
    }

    out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
    if err != nil {
        logger.Debugf("NVMe-oFC: nvme discover failed (no path?) target=%s host=%s: %v",
            arrayTargetPort, hostPort, err)
        return "", nil
    }

    return parseSubNqnFromDiscoverOutput(string(out)), nil
}

func parseSubNqnFromDiscoverOutput(output string) string {
    for _, line := range strings.Split(output, "\n") {
        trimmed := strings.TrimSpace(line)
        if !strings.HasPrefix(trimmed, "subnqn:") {
            continue
        }
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
        logger.Errorf("NVMe-oFC: connect failed NQN=%s target=%s host=%s: %v output=%s",
            subNqn, arrayTargetPort, hostPort, err, string(out))
        return
    }
    logger.Infof("NVMe-oFC: connected NQN=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)
}

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

        portName := strings.TrimPrefix(strings.TrimSpace(string(portBytes)), "0x")
        nodeName := strings.TrimPrefix(strings.TrimSpace(string(nodeBytes)), "0x")

        if portName == "" || nodeName == "" {
            logger.Warningf("NVMe-oFC: empty port/node name at %s, skipping", hostDir)
            continue
        }

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

/**
 * Copyright 2024 IBM Corp.
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
	"strings"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"
)

const (
	// TBD: SKG: Check whether other params which are
	// present in device_connectivity_nvmeofc.go are also needed here
	nvmeTransportTCP   = "tcp"
	defaultNvmeTcpPort = "4420"
)

type OsDeviceConnectivityNvmeOTcp struct {
	Executer          executer.ExecuterInterface
	KeyedGater        *executer.KeyedGater
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityNvmeOTcp(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityNvmeOTcp{
		Executer:          executer,
		KeyedGater:        KeyedGater,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, KeyedGater, Mounter, clean_scsi_device),
	}
}

// EnsureLogin performs NVMe-oF over TCP discovery and connect for each target IP endpoint.
func (r *OsDeviceConnectivityNvmeOTcp) EnsureLogin(ctx context.Context, ipsByArrayInitiator map[string][]string) {
	if len(ipsByArrayInitiator) == 0 {
		logger.Warningf("NVMe-oTCP EnsureLogin: no array target IPs in publish context, skipping")
		return
	}

	targetIPs := r.extractTargetIPs(ipsByArrayInitiator)
	if len(targetIPs) == 0 {
		logger.Warningf("NVMe-oTCP EnsureLogin: no valid target IPs extracted, skipping")
		return
	}

	liveTraddrs := r.getLiveTraddrs()
	logger.Debugf("NVMe-oTCP EnsureLogin: live target addresses: %v", liveTraddrs)

	currentPaths := 0
	for _, ip := range targetIPs {
		if liveTraddrs[ip] {
			currentPaths++
		}
	}
	logger.Infof("NVMe-oTCP EnsureLogin: current live paths=%d target=%d", currentPaths, nvmeTargetPathCount)

	if currentPaths >= nvmeTargetPathCount {
		logger.Infof("NVMe-oTCP EnsureLogin: already at target path count (%d), skipping connect", nvmeTargetPathCount)
		return
	}

	connectedPaths := currentPaths
	for _, targetIP := range targetIPs {
		if connectedPaths >= nvmeTargetPathCount {
			logger.Infof("NVMe-oTCP EnsureLogin: reached target path count (%d), stopping", nvmeTargetPathCount)
			break
		}

		if liveTraddrs[targetIP] {
			logger.Debugf("NVMe-oTCP EnsureLogin: path already live for target IP %s, skipping", targetIP)
			continue
		}

		subNqn, err := r.discoverSubNqn(ctx, targetIP, defaultNvmeTcpPort)
		if err != nil {
			logger.Debugf("NVMe-oTCP EnsureLogin: discover error for target IP %s: %v", targetIP, err)
			continue
		}
		if subNqn == "" {
			logger.Debugf("NVMe-oTCP EnsureLogin: no subnqn found for target IP %s, skipping", targetIP)
			continue
		}

		logger.Infof("NVMe-oTCP EnsureLogin: connecting NQN=%s targetIP=%s port=%s", subNqn, targetIP, defaultNvmeTcpPort)
		if r.nvmeConnect(ctx, targetIP, defaultNvmeTcpPort, subNqn) {
			connectedPaths++
		}
	}

	finalLiveTraddrs := r.getLiveTraddrs()
	finalPathCount := 0
	for _, ip := range targetIPs {
		if finalLiveTraddrs[ip] {
			finalPathCount++
		}
	}
	logger.Infof("NVMe-oTCP EnsureLogin: final live paths=%d target=%d", finalPathCount, nvmeTargetPathCount)

	if finalPathCount == 0 {
		logger.Errorf("NVMe-oTCP EnsureLogin: 0 live paths after all connect attempts — check network connectivity and array port configuration. NodeStageVolume will fail.")
		return
	}

	if finalPathCount < nvmeTargetPathCount {
		logger.Warningf("NVMe-oTCP EnsureLogin: below target path count: final=%d target=%d, continuing with reduced redundancy",
			finalPathCount, nvmeTargetPathCount)
	}
}

func (r *OsDeviceConnectivityNvmeOTcp) extractTargetIPs(ipsByArrayInitiator map[string][]string) []string {
	ipSet := make(map[string]bool)
	var ips []string
	for key, ipList := range ipsByArrayInitiator {
		if len(ipList) > 0 {
			for _, ip := range ipList {
				trimmed := strings.TrimSpace(ip)
				if trimmed != "" && !ipSet[trimmed] {
					ipSet[trimmed] = true
					ips = append(ips, trimmed)
				}
			}
		} else {
			trimmed := strings.TrimSpace(key)
			if trimmed != "" && !ipSet[trimmed] {
				ipSet[trimmed] = true
				ips = append(ips, trimmed)
			}
		}
	}
	return ips
}

func (r OsDeviceConnectivityNvmeOTcp) getLiveTraddrs() map[string]bool {
	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", []string{"list-subsys"})
	if err != nil {
		logger.Warningf("NVMe-oTCP getLiveTraddrs: nvme list-subsys failed: %v", err)
		return map[string]bool{}
	}
	liveTraddrs := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "+-") || !strings.Contains(trimmed, " live") {
			continue
		}
		traddr := extractNvmeField(trimmed, "traddr=")
		if traddr != "" {
			liveTraddrs[traddr] = true
			logger.Debugf("NVMe-oTCP getLiveTraddrs: live target address traddr=%s", traddr)
		}
	}
	return liveTraddrs
}

func (r *OsDeviceConnectivityNvmeOTcp) discoverSubNqn(ctx context.Context, targetIP, port string) (string, error) {
	args := []string{
		"discover",
		"--transport=" + nvmeTransportTCP,
		"--traddr=" + targetIP,
		"--trsvcid=" + port,
	}
	output := ""
	r.KeyedGater.ExecuteNvmeFabric(ctx, func() error {
		out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
		if err != nil {
			logger.Debugf("NVMe-oTCP discoverSubNqn: nvme discover failed targetIP=%s: %v", targetIP, err)
			return nil
		}
		subNqn := parseSubNqnFromDiscoverOutput(string(out))
		if subNqn != "" {
			logger.Debugf("NVMe-oTCP discoverSubNqn: discovered subnqn=%s targetIP=%s", subNqn, targetIP)
			output = subNqn
		}
		return nil
	})
	return output, nil
}

func (r *OsDeviceConnectivityNvmeOTcp) nvmeConnect(ctx context.Context, targetIP, port, subNqn string) bool {
	args := []string{
		"connect",
		"--transport=" + nvmeTransportTCP,
		"--traddr=" + targetIP,
		"--trsvcid=" + port,
		"--nqn=" + subNqn,
	}
	err := r.KeyedGater.ExecuteNvmeFabric(ctx, func() error {
		out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
		if err != nil {
			logger.Errorf("NVMe-oTCP nvmeConnect: failed NQN=%s targetIP=%s: %v output=%s",
				subNqn, targetIP, err, string(out))
			return err
		}
		logger.Infof("NVMe-oTCP nvmeConnect: connected NQN=%s targetIP=%s", subNqn, targetIP)
		return nil
	})
	return err == nil
}

func (r OsDeviceConnectivityNvmeOTcp) RescanDevices(_ context.Context, _ int, _ []string) error {
	return nil
}

func (r OsDeviceConnectivityNvmeOTcp) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {
	return r.HelperScsiGeneric.GetMpathDevice(ctx, volumeId)
}

func (r OsDeviceConnectivityNvmeOTcp) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(ctx, sysDevices)
}

func (r OsDeviceConnectivityNvmeOTcp) RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(ctx, expectedSerial, expectedLun, arrayIdentifiers)
}

func (r OsDeviceConnectivityNvmeOTcp) ValidateLun(_ context.Context, _ string, _ int, _ []string, _ string) error {
	return nil
}

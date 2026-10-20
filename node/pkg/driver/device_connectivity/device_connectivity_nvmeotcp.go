/**
 * Copyright 2026 IBM Corp.
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
	"path/filepath"
	"sort"
	"strings"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

const (
	nvmeTransportTCP     = "tcp"
	nvmeTcpPort          = "4420"
	nvmeTcpCtrlSysfsGlob = "/sys/class/nvme/nvme*"
	nvmeTcpCtrlStateLive = "live"
)

type OsDeviceConnectivityNvmeOTcp struct {
	Executer          executer.ExecuterInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityNvmeOTcp(executer executer.ExecuterInterface, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityNvmeOTcp{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, clean_scsi_device),
	}
}

// EnsureLogin performs NVMe-oTCP discovery and connect for each array target IP from the publish context.
// Each target IP is one path. Connects until min(number of target IPs, nvmeTargetPathCount) paths are live.
// Logs error if 0 paths result, warning if below that count.
// For non-native NVMe with find_multipaths=on, logs error if < 2 paths.
func (r OsDeviceConnectivityNvmeOTcp) EnsureLogin(ipsByArrayInitiator map[string][]string) {
	targetIps := getNvmeTcpTargetIps(ipsByArrayInitiator)
	if len(targetIps) == 0 {
		logger.Warningf("NVMe-oTCP EnsureLogin: no array target IPs in publish context, skipping")
		return
	}

	requiredPaths := nvmeTargetPathCount
	if len(targetIps) < requiredPaths {
		requiredPaths = len(targetIps)
	}
	logger.Debugf("NVMe-oTCP EnsureLogin: array target IPs: %v, required live paths: %d", targetIps, requiredPaths)

	liveTargetIps := r.getLiveTargetIps()
	logger.Debugf("NVMe-oTCP EnsureLogin: live target IPs: %v", liveTargetIps)

	currentPaths := countLiveTargetIps(liveTargetIps, targetIps)
	logger.Infof("NVMe-oTCP EnsureLogin: current live paths=%d required=%d", currentPaths, requiredPaths)

	if currentPaths >= requiredPaths {
		logger.Infof("NVMe-oTCP EnsureLogin: already at required path count (%d), skipping connect", requiredPaths)
		return
	}

	connectedPaths := currentPaths
	for _, targetIp := range targetIps {
		if connectedPaths >= requiredPaths {
			logger.Infof("NVMe-oTCP EnsureLogin: reached required path count (%d), stopping", requiredPaths)
			break
		}

		if liveTargetIps[targetIp] {
			logger.Debugf("NVMe-oTCP EnsureLogin: path already live target=%s, skipping", targetIp)
			continue
		}

		subNqn := r.discoverSubNqn(targetIp)
		if subNqn == "" {
			logger.Debugf("NVMe-oTCP EnsureLogin: no subnqn found target=%s, skipping", targetIp)
			continue
		}

		logger.Infof("NVMe-oTCP EnsureLogin: connecting NQN=%s target=%s port=%s", subNqn, targetIp, nvmeTcpPort)
		if r.nvmeConnect(targetIp, subNqn) {
			connectedPaths++
		}
	}

	// Re-read to get kernel-confirmed final count (nvmeConnect may have failed silently).
	finalLiveTargetIps := r.getLiveTargetIps()
	finalPathCount := countLiveTargetIps(finalLiveTargetIps, targetIps)
	logger.Infof("NVMe-oTCP EnsureLogin: final live paths=%d required=%d", finalPathCount, requiredPaths)

	if finalPathCount == 0 {
		logger.Errorf("NVMe-oTCP EnsureLogin: 0 live paths after all connect attempts, " +
			"check network connectivity to the array target IPs. NodeStageVolume will fail.")
		return
	}

	if finalPathCount < requiredPaths {
		logger.Warningf("NVMe-oTCP EnsureLogin: below required path count: final=%d required=%d, continuing with reduced redundancy",
			finalPathCount, requiredPaths)
	}

	// Non-native NVMe + find_multipaths=on + < 2 paths: multipathd will not
	// create a dm device, causing GetMpathDevice to fail.
	nativeMpath, err := isNvmeCoreMultipathEnabled()
	if err != nil {
		logger.Warningf("NVMe-oTCP EnsureLogin: could not determine nvme_core multipath mode: %v", err)
		return
	}
	logger.Debugf("NVMe-oTCP EnsureLogin: nvme_core native multipath enabled: %v", nativeMpath)
	if !nativeMpath && finalPathCount < nvmeMinPathsForNonNativeDmMultipath {
		findMpathsOn, err := OsDeviceConnectivityNvmeOFc{Executer: r.Executer}.isFindMultipathsOn()
		if err != nil {
			logger.Warningf("NVMe-oTCP EnsureLogin: could not read find_multipaths setting: %v", err)
			return
		}
		logger.Debugf("NVMe-oTCP EnsureLogin: find_multipaths on: %v", findMpathsOn)
		if findMpathsOn {
			logger.Errorf("NVMe-oTCP EnsureLogin: non-native NVMe with find_multipaths=on requires >= %d paths "+
				"but only %d are live, multipathd will not create a dm device. "+
				"Set find_multipaths=no in /etc/multipath.conf or fix network connectivity.",
				nvmeMinPathsForNonNativeDmMultipath, finalPathCount)
		}
	}
}

// getNvmeTcpTargetIps returns the array target IPs (keys of ipsByArrayInitiator), sorted for a stable order.
func getNvmeTcpTargetIps(ipsByArrayInitiator map[string][]string) []string {
	targetIps := make([]string, 0, len(ipsByArrayInitiator))
	for targetIp := range ipsByArrayInitiator {
		targetIp = strings.TrimSpace(targetIp)
		if targetIp != "" {
			targetIps = append(targetIps, targetIp)
		}
	}
	sort.Strings(targetIps)
	return targetIps
}

// countLiveTargetIps counts the array target IPs that have a live path.
func countLiveTargetIps(liveTargetIps map[string]bool, targetIps []string) int {
	count := 0
	for _, targetIp := range targetIps {
		if liveTargetIps[targetIp] {
			count++
		}
	}
	return count
}

// getLiveTargetIps parses "nvme list-subsys" and returns the set of traddr values of live tcp paths.
// e.g. path line: "+- nvme0 tcp traddr=9.71.253.97,trsvcid=4420,src_addr=9.71.253.106 live"
func (r OsDeviceConnectivityNvmeOTcp) getLiveTargetIps() map[string]bool {
	liveTargetIps := map[string]bool{}
	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", []string{"list-subsys"})
	if err != nil {
		logger.Warningf("NVMe-oTCP getLiveTargetIps: nvme list-subsys failed: %v", err)
		return liveTargetIps
	}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "+-") || !strings.Contains(trimmed, " live") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 3 || fields[2] != nvmeTransportTCP {
			continue
		}
		traddr := extractNvmeField(trimmed, "traddr=")
		if traddr != "" {
			liveTargetIps[traddr] = true
			logger.Debugf("NVMe-oTCP getLiveTargetIps: live path controller=%s traddr=%s", fields[1], traddr)
		}
	}
	return liveTargetIps
}

// discoverSubNqn runs "nvme discover" against one array target IP and returns the storage subsystem NQN.
// Returns "" if discovery fails or no subsystem NQN is found.
func (r OsDeviceConnectivityNvmeOTcp) discoverSubNqn(targetIp string) string {
	args := []string{
		"discover",
		"--transport=" + nvmeTransportTCP,
		"--traddr=" + targetIp,
		"--trsvcid=" + nvmeTcpPort,
	}
	logger.Debugf("NVMe-oTCP discoverSubNqn: running nvme %v", args)
	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
	if err != nil {
		logger.Debugf("NVMe-oTCP discoverSubNqn: nvme discover failed target=%s: %v output=%s",
			targetIp, err, string(out))
		return ""
	}
	subNqn := parseSubNqnFromDiscoverOutput(string(out))
	logger.Debugf("NVMe-oTCP discoverSubNqn: discovered subnqn=%s target=%s", subNqn, targetIp)
	return subNqn
}

// nvmeConnect runs "nvme connect" for one (array target IP, subsystem NQN) combination.
func (r OsDeviceConnectivityNvmeOTcp) nvmeConnect(targetIp, subNqn string) bool {
	args := []string{
		"connect",
		"--transport=" + nvmeTransportTCP,
		"--traddr=" + targetIp,
		"--trsvcid=" + nvmeTcpPort,
		"--nqn=" + subNqn,
	}
	logger.Debugf("NVMe-oTCP nvmeConnect: running nvme %v", args)
	out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", args)
	if err != nil {
		logger.Errorf("NVMe-oTCP nvmeConnect: failed NQN=%s target=%s: %v output=%s",
			subNqn, targetIp, err, string(out))
		return false
	}
	logger.Infof("NVMe-oTCP nvmeConnect: connected NQN=%s target=%s output=%s",
		subNqn, targetIp, strings.TrimSpace(string(out)))
	return true
}

// RescanDevices rescans the namespaces of every live NVMe-oTCP controller connected to one of the
// array target IPs. The storage does not remove unmapped namespaces from the host, so the scan
// both discovers newly mapped namespaces and removes the stale ones (kernel nvme_scan_work).
// Works for native NVMe multipath and for DM-multipath over NVMe, since both use the same controllers.
func (r OsDeviceConnectivityNvmeOTcp) RescanDevices(_ int, arrayInitiators []string) error {
	arrayTargetIps := make(map[string]bool, len(arrayInitiators))
	for _, arrayInitiator := range arrayInitiators {
		arrayTargetIps[strings.TrimSpace(arrayInitiator)] = true
	}
	logger.Debugf("NVMe-oTCP RescanDevices: array target IPs %v", arrayInitiators)

	ctrlPaths, err := r.Executer.FilepathGlob(nvmeTcpCtrlSysfsGlob)
	if err != nil {
		logger.Warningf("NVMe-oTCP RescanDevices: glob %s failed: %v", nvmeTcpCtrlSysfsGlob, err)
		return nil
	}
	logger.Debugf("NVMe-oTCP RescanDevices: found controllers %v", ctrlPaths)

	rescannedCtrls := []string{}
	for _, ctrlPath := range ctrlPaths {
		if !r.isLiveTcpCtrlToArray(ctrlPath, arrayTargetIps) {
			continue
		}
		ctrlDevice := filepath.Join(DevPath, filepath.Base(ctrlPath))
		logger.Debugf("NVMe-oTCP RescanDevices: running nvme ns-rescan %s", ctrlDevice)
		out, err := r.Executer.ExecuteWithTimeout(nvmeCmdTimeout, "nvme", []string{"ns-rescan", ctrlDevice})
		if err != nil {
			logger.Warningf("NVMe-oTCP RescanDevices: nvme ns-rescan %s failed: %v output=%s", ctrlDevice, err, string(out))
			continue
		}
		rescannedCtrls = append(rescannedCtrls, ctrlDevice)
	}
	logger.Infof("NVMe-oTCP RescanDevices: rescanned controllers %v of array target IPs %v", rescannedCtrls, arrayInitiators)
	return nil
}

// isLiveTcpCtrlToArray checks in sysfs that the controller uses TCP transport, is live,
// and its traddr is one of the array target IPs.
// e.g. address: "traddr=9.71.253.97,trsvcid=4420,src_addr=9.71.253.106"
func (r OsDeviceConnectivityNvmeOTcp) isLiveTcpCtrlToArray(ctrlPath string, arrayTargetIps map[string]bool) bool {
	transport, err := r.Executer.IoutilReadFile(filepath.Join(ctrlPath, "transport"))
	if err != nil || strings.TrimSpace(string(transport)) != nvmeTransportTCP {
		return false
	}
	state, err := r.Executer.IoutilReadFile(filepath.Join(ctrlPath, "state"))
	if err != nil || strings.TrimSpace(string(state)) != nvmeTcpCtrlStateLive {
		logger.Debugf("NVMe-oTCP isLiveTcpCtrlToArray: controller %s is not live, skipping", ctrlPath)
		return false
	}
	address, err := r.Executer.IoutilReadFile(filepath.Join(ctrlPath, "address"))
	if err != nil {
		logger.Debugf("NVMe-oTCP isLiveTcpCtrlToArray: cannot read address of controller %s: %v", ctrlPath, err)
		return false
	}
	traddr := extractNvmeField(strings.TrimSpace(string(address)), "traddr=")
	isArrayCtrl := arrayTargetIps[traddr]
	logger.Debugf("NVMe-oTCP isLiveTcpCtrlToArray: controller %s traddr=%s is array target: %v", ctrlPath, traddr, isArrayCtrl)
	return isArrayCtrl
}

func (r OsDeviceConnectivityNvmeOTcp) GetMpathDevice(volumeId string) (string, error) {
	return r.HelperScsiGeneric.GetMpathDevice(volumeId)
}

func (r OsDeviceConnectivityNvmeOTcp) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityNvmeOTcp) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

func (r OsDeviceConnectivityNvmeOTcp) RemoveGhostDevice(lun int) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(lun)
}

func (r OsDeviceConnectivityNvmeOTcp) ValidateLun(_ int, _ []string) error {
	return nil
}

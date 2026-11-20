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
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"

	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	zfcpAllowLunScanPath     = "/sys/module/zfcp/parameters/allow_lun_scan"
	zfcpAllowLunScanEnabled  = "Y"
	lszfcpCmd                = "lszfcp"
	lszfcpCmdTimeoutMsec     = 5 * 1000
	zfcpPortTypeKey          = "port_type:"
	zfcpNpivPortType         = "NPIV VPORT"
	zfcpLunDiscoveryRequired = "Automatic FCP LUN discovery requires both allow_lun_scan=Y in the OS and NPIV enabled on the FCP adapter (port_type: NPIV VPORT)."
)

type OsDeviceConnectivityFc struct {
	Executer          executer.ExecuterInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityFc(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityFc{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, KeyedGater, Mounter, clean_scsi_device),
	}
}

func (r OsDeviceConnectivityFc) EnsureLogin(_ context.Context, _ map[string][]string) {
	// FC doesn't require login
}

func (r OsDeviceConnectivityFc) updateFCHostIDs(hostIDs map[int]bool) {
	// Map host numbers to their physical PCI anchor
	// host0 -> 0000:04:00.0, host1 -> 0000:04:00.0
	pciMap := make(map[int]string)
	baseClassPath := "/sys/class/fc_host"

	// OPTIMIZED: Replace the dynamic wildcard Glob with a high-speed, direct
	// directory listing of the flat in-memory class directory.
	entries, err := os.ReadDir(baseClassPath)
	if err != nil {
		// Fallback smoothly if FC is unmounted, absent, or disabled on the node
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		// Match precisely against exactly named target host slots
		if !strings.HasPrefix(name, "host") {
			continue
		}

		hostNum, _ := strconv.Atoi(strings.TrimPrefix(name, "host"))

		// Req 4: Use os.Readlink to follow the 'device' symlink
		if link, err := os.Readlink(filepath.Join(baseClassPath, name, "device")); err == nil {
			pciMap[hostNum] = filepath.Base(link)
		}
	}

	// KEEP ORIGINAL: Capture the PCI addresses already in our "active" set
	targetHardware := make(map[string]bool)
	for id := range hostIDs {
		if pci, exists := pciMap[id]; exists {
			targetHardware[pci] = true
		}
	}

	// KEEP ORIGINAL: Add siblings: If we're scanning one "slice" of a PCI device, scan them all
	for id, pci := range pciMap {
		if targetHardware[pci] && !hostIDs[id] {
			hostIDs[id] = true
			logger.Infof("FC Sibling: associated host%d with shared HBA %s", id, pci)
		}
	}
}

func (r OsDeviceConnectivityFc) RescanDevices(ctx context.Context, lunId int, arrayIdentifiers []string) error {
	hostIDs, err := r.HelperScsiGeneric.RescanDevicesGetHostIds(lunId, arrayIdentifiers)
	if err != nil {
		return err
	}
	return r.HelperScsiGeneric.RescanDevices(ctx, lunId, arrayIdentifiers, hostIDs)
}

func (r OsDeviceConnectivityFc) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {
	/*
	   Return Value: "dm-X" of the volumeID.
	*/
	mpathDevice, err := r.HelperScsiGeneric.GetMpathDevice(ctx, volumeId)

	var notFoundErr *MultipathDeviceNotFoundForVolumeError
	if errors.As(err, &notFoundErr) {
		notFoundErr.Diagnostics = r.zfcpDiagnostics(ctx, volumeId)
	}
	return mpathDevice, err
}

// zfcpDiagnostics is a best-effort check run only after multipath discovery failed.
// It never fails and never alters discovery: any error is logged at Debug level and ignored.
// It returns an empty string on non-zFCP hosts (no zfcp module parameter) and when the settings look healthy.
func (r OsDeviceConnectivityFc) zfcpDiagnostics(ctx context.Context, volumeId string) string {
	if ctx.Err() != nil {
		return ""
	}

	var issues []string

	allowLunScan, err := r.Executer.IoutilReadFile(zfcpAllowLunScanPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		logger.Debugf("zFCP diagnostics: could not read %s: %v", zfcpAllowLunScanPath, err)
	} else if value := strings.TrimSpace(string(allowLunScan)); value != zfcpAllowLunScanEnabled {
		issues = append(issues, fmt.Sprintf("zfcp allow_lun_scan is \"%s\" (expected \"%s\"): automatic FCP LUN discovery is disabled on this node. Set zfcp.allow_lun_scan=1 as a kernel argument on the node.", value, zfcpAllowLunScanEnabled))
	}

	lszfcpOut, err := r.Executer.ExecuteWithTimeout(lszfcpCmdTimeoutMsec, lszfcpCmd, []string{"-Ha"})
	if err != nil {
		logger.Debugf("zFCP diagnostics: %s -Ha failed: %v", lszfcpCmd, err)
	} else if portTypes := parseZfcpPortTypes(string(lszfcpOut)); len(portTypes) > 0 && !containsString(portTypes, zfcpNpivPortType) {
		issues = append(issues, fmt.Sprintf("FCP adapter port_type is \"%s\" (expected \"%s\"): NPIV is not active on the FCP adapter. Enable NPIV for the FCP adapter in the HMC/LPAR configuration.", strings.Join(portTypes, "\", \""), zfcpNpivPortType))
	}

	if len(issues) == 0 {
		return ""
	}

	diagnostics := strings.Join(append(issues, zfcpLunDiscoveryRequired), " ")
	logger.Warningf("zFCP diagnostics for volumeID [%s]: %s", volumeId, diagnostics)
	return diagnostics
}

// parseZfcpPortTypes returns the distinct values of all "port_type:" entries in `lszfcp -Ha` output.
func parseZfcpPortTypes(output string) []string {
	var portTypes []string
	for _, line := range strings.Split(output, "\n") {
		idx := strings.Index(line, zfcpPortTypeKey)
		if idx == -1 {
			continue
		}
		portType := strings.TrimSpace(line[idx+len(zfcpPortTypeKey):])
		if portType != "" && !containsString(portTypes, portType) {
			portTypes = append(portTypes, portType)
		}
	}
	return portTypes
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func (r OsDeviceConnectivityFc) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(ctx, sysDevices)
}

func (r OsDeviceConnectivityFc) RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(ctx, expectedSerial, expectedLun, arrayIdentifiers)
}

func (r OsDeviceConnectivityFc) ValidateLun(ctx context.Context, targetDm string, lun int, sysDevices []string, expectedSerial string) error {
	return r.HelperScsiGeneric.ValidateLun(ctx, targetDm, lun, sysDevices, expectedSerial)
}

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
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"

	"context"
	"path/filepath"
	"strconv"
	"strings"
	"os"
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

func (r OsDeviceConnectivityFc) EnsureLogin(_ map[string][]string) {
	// FC doesn't require login
}

func (r OsDeviceConnectivityFc) updateFCHostIDs(hostIDs map[int]bool) {
    // Map host numbers to their physical PCI anchor
    // host0 -> 0000:04:00.0, host1 -> 0000:04:00.0
    pciMap := make(map[int]string)
    
    hosts, _ := filepath.Glob("/sys/class/fc_host/host*")
    for _, h := range hosts {
        hostNum, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(h), "host"))
        
        // Req 4: Use os.Readlink to follow the 'device' symlink
        if link, err := os.Readlink(filepath.Join(h, "device")); err == nil {
            pciMap[hostNum] = filepath.Base(link)
        }
    }

    // Capture the PCI addresses already in our "active" set
    targetHardware := make(map[string]bool)
    for id := range hostIDs {
        if pci, exists := pciMap[id]; exists {
            targetHardware[pci] = true
        }
    }

    // Add siblings: If we're scanning one "slice" of a PCI device, scan them all
    for id, pci := range pciMap {
        if targetHardware[pci] && !hostIDs[id] {
            hostIDs[id] = true
            logger.Infof("FC Sibling: associated host%d with shared HBA %s", id, pci)
        }
    }
}


func (r OsDeviceConnectivityFc) RescanDevices(lunId int, arrayIdentifiers []string) error {
	hostIDs, err := r.HelperScsiGeneric.RescanDevicesGetHostIds(lunId, arrayIdentifiers)
	if err != nil {
		return err
	}
	return r.HelperScsiGeneric.RescanDevices(lunId, arrayIdentifiers, hostIDs)
}

func (r OsDeviceConnectivityFc) GetMpathDevice(volumeId string) (string, error) {
	/*
	   Return Value: "dm-X" of the volumeID.
	*/
	return r.HelperScsiGeneric.GetMpathDevice(volumeId)
}

func (r OsDeviceConnectivityFc) FlushMultipathDevice(ctx context.Context, mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(ctx, mpathDevice)
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

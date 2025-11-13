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
)

type OsDeviceConnectivityFc struct {
	Executer          executer.ExecuterInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityFc(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityFc{
		Executer:          executer,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, KeyGater, Mounter, clean_scsi_device),
	}
}

func (r OsDeviceConnectivityFc) EnsureLogin(_ map[string][]string) {
	// FC doesn't require login
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

func (r OsDeviceConnectivityFc) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityFc) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

func (r OsDeviceConnectivityFc) RemoveGhostDevice(expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(expectedSerial, expectedLun, arrayIdentifiers)
}

func (r OsDeviceConnectivityFc) ValidateLun(targetDm string, lun int, sysDevices []string, expectedSerial string) error {
	return r.HelperScsiGeneric.ValidateLun(targetDm, lun, sysDevices, expectedSerial)
}

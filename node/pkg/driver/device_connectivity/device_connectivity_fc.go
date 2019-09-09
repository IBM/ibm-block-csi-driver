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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

type OsDeviceConnectivityFc struct {
	Executer          executer.ExecuterInterface
	Helper            OsDeviceConnectivityHelperFcInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityFc(executer executer.ExecuterInterface) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityFc{
		Executer:          executer,
		Helper:            NewOsDeviceConnectivityHelperFc(executer),
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer),
	}
}

func (r OsDeviceConnectivityFc) RescanDevices(lunId int, arrayIdentifiers []string) error {
	logger.Debugf("Rescan : Start rescan on specific lun, on lun : {%v}, with array wwn : {%v}", lunId, arrayIdentifiers)

	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		logger.Errorf(e.Error())
		return e
	}

	HostIDs, err := r.Helper.GetFcHostIDs()
	if err != nil {
		return err
	}

	return r.HelperScsiGeneric.RescanDevices(lunId, arrayIdentifiers, HostIDs)
}

func (r OsDeviceConnectivityFc) GetMpathDevice(volumeId string, lunId int, arrayIdentifiers []string) (string, error) {
	/*
			   Description:
				   1. Find all the files "/dev/disk/by-path/pci-*-fc-<WWN storage>-lun-<LUN-ID> -> ../../sd<X>
		 	          Note: <ARRAY-IP> Instead of setting here the IP we just search for * on that.
		 	       2. Get the sd<X> devices.
		 	       3. Search '/sys/block/dm-*\/slaves/*' and get the <DM device name>. For example dm-3 below:
		 	          /sys/block/dm-3/slaves/sdb -> ../../../../pci0000:00/0000:00:17.0/0000:13:00.0/host33/rport-33:0-3/target33:0:1/33:0:1:0/block/sdb

			   Return Value: "dm-X" of the volumeID by using the LunID and the array wwn.
	*/

	logger.Infof("FC: GetMpathDevice: Found multipath devices for volume : [%s] that relats to lunId=%d and arrayIdentifiers=%s", volumeId, lunId, arrayIdentifiers)

	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		return "", e
	}
	for index, wwn := range arrayIdentifiers {
		arrayIdentifiers[index] = "0x" + strings.ToLower(wwn)
	}

	return r.HelperScsiGeneric.GetMpathDevice(volumeId, lunId, arrayIdentifiers, "fc")
}

func (r OsDeviceConnectivityFc) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityFc) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

// ============== OsDeviceConnectivityHelperFcInterface ==========================

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperFcInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperFcInterface

type OsDeviceConnectivityHelperFcInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityFc.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test OsDeviceConnectivityFc logic.
	*/
	GetFcHostIDs() ([]int, error)
}

type OsDeviceConnectivityHelperFc struct {
	executer executer.ExecuterInterface
}

const (
	FC_HOST_SYSFS_PATH = "/sys/class/fc_host"
)

func NewOsDeviceConnectivityHelperFc(executer executer.ExecuterInterface) OsDeviceConnectivityHelperFcInterface {
	return &OsDeviceConnectivityHelperFc{executer: executer}
}

func (o OsDeviceConnectivityHelperFc) GetFcHostIDs() ([]int, error) {
	/*
		Description:
			This function find all the hosts IDs under directory /sys/class/fc_host/"
			So the function goes over all the above hosts and return back only the host numbers as a list.
	*/

	portStatePath := filepath.Join(FC_HOST_SYSFS_PATH, "/host*/port_state")
	var HostIDs []int
	matches, err := o.executer.FilepathGlob(portStatePath)
	if err != nil {
		logger.Errorf("Error while Glob portStatePath : {%v}. err : {%v}", portStatePath, err)
		return nil, err
	}

	logger.Debugf("targetname files matches were found : {%v}", matches)

	re := regexp.MustCompile("host([0-9]+)")
	for _, targetPath := range matches {
		logger.Debugf("Check if targetfile (%s) value is Online.", targetPath)
		targetName, err := o.executer.IoutilReadFile(targetPath)
		if err != nil {
			logger.Warningf("Could not read target name from file : {%v}, error : {%v}", targetPath, err)
			continue
		}

		if strings.EqualFold(strings.TrimSpace(string(targetName)), "online") {
			regexMatch := re.FindStringSubmatch(targetPath)
			logger.Tracef("Found regex matches : {%v}", regexMatch)
			hostNumber := -1

			if len(regexMatch) < 2 {
				logger.Warningf("Could not find host number for portStatePath : {%v}", targetPath)
				continue
			} else {
				hostNumber, err = strconv.Atoi(regexMatch[1])
				if err != nil {
					logger.Warningf("Host number in for target file was not valid : {%v}", regexMatch[1])
					continue
				}
			}

			HostIDs = append(HostIDs, hostNumber)
			logger.Debugf("portState path (%s) was found. Adding host ID {%v} to the id list.", targetPath, hostNumber)
		}
	}

	if len(HostIDs) == 0 {
		return []int{}, &ConnectivityFcHostTargetNotFoundError{DirectoryPath: FC_HOST_SYSFS_PATH}
	}
	return HostIDs, nil

}

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
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

type OsDeviceConnectivityIscsi struct {
	Executer          executer.ExecuterInterface
	Helper            OsDeviceConnectivityHelperIscsiInterface
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityIscsi(executer executer.ExecuterInterface) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityIscsi{
		Executer:          executer,
		Helper:            NewOsDeviceConnectivityHelperIscsi(executer),
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer),
	}
}

func (r OsDeviceConnectivityIscsi) RescanDevices(lunId int, arrayIdentifiers []string) error {
	logger.Debugf("Rescan : Start rescan on specific lun, on lun : {%v}, with array iqn : {%v}", lunId, arrayIdentifiers)
	var sessionHosts []int
	var errStrings []string

	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		logger.Errorf(e.Error())
		return e
	}

	for _, iqn := range arrayIdentifiers {
		hostsId, e := r.Helper.GetIscsiSessionHostsForArrayIQN(iqn)
		if e != nil {
			logger.Errorf(e.Error())
			errStrings = append(errStrings, e.Error())
		}
		sessionHosts = append(sessionHosts, hostsId...)
	}
	if len(sessionHosts) == 0 && len(errStrings) != 0 {
		err := errors.New(strings.Join(errStrings, ","))
		return err
	}

	return r.HelperScsiGeneric.RescanDevices(lunId, arrayIdentifiers, sessionHosts)
}

func (r OsDeviceConnectivityIscsi) GetMpathDevice(volumeId string, lunId int, arrayIdentifiers []string) (string, error) {
	/*
			   Description:
				   1. Find all the files "/dev/disk/by-path/ip-<ARRAY-IP>-iscsi-<IQN storage>-lun-<LUN-ID> -> ../../sd<X>
		 	          Note: <ARRAY-IP> Instead of setting here the IP we just search for * on that.
		 	       2. Get the sd<X> devices.
		 	       3. Search '/sys/block/dm-*\/slaves/*' and get the <DM device name>. For example dm-3 below:
		 	          /sys/block/dm-3/slaves/sdb -> ../../../../platform/host41/session9/target41:0:0/41:0:0:13/block/sdb

			   Return Value: "dm-X" of the volumeID by using the LunID and the arrayIqn.
	*/
	logger.Infof("iSCSI: GetMpathDevice: Found multipath devices for volume : [%s] that relats to lunId=%d and arrayIdentifiers=%s", volumeId, lunId, arrayIdentifiers)

	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		return "", e
	}

	return r.HelperScsiGeneric.GetMpathDevice(volumeId, lunId, arrayIdentifiers, "iscsi")
}

func (r OsDeviceConnectivityIscsi) FlushMultipathDevice(mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(mpathDevice)
}

func (r OsDeviceConnectivityIscsi) RemovePhysicalDevice(sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(sysDevices)
}

// ============== OsDeviceConnectivityHelperIscsiInterface ==========================

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperIscsiInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperIscsiInterface

type OsDeviceConnectivityHelperIscsiInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityIscsi.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test OsDeviceConnectivityIscsi logic.
	*/
	GetIscsiSessionHostsForArrayIQN(arrayIdentifier string) ([]int, error)
}

type OsDeviceConnectivityHelperIscsi struct {
	executer executer.ExecuterInterface
}

const (
	IscsiHostRexExPath = "/sys/class/iscsi_host/host*/device/session*/iscsi_session/session*/targetname"
)

func NewOsDeviceConnectivityHelperIscsi(executer executer.ExecuterInterface) OsDeviceConnectivityHelperIscsiInterface {
	return &OsDeviceConnectivityHelperIscsi{executer: executer}
}

func (o OsDeviceConnectivityHelperIscsi) GetIscsiSessionHostsForArrayIQN(arrayIdentifier string) ([]int, error) {
	/*
		Description:
			This function find all the hosts IDs under which has targetname that equal to the arrayIdentifier.
			/sys/class/iscsi_host/host<IDs>/device/session*\/iscsi_session/session*\/targetname"
			So the function goes over all the above hosts and return back only the host numbers as a list.
	*/

	targetNamePath := IscsiHostRexExPath
	var sessionHosts []int
	matches, err := o.executer.FilepathGlob(targetNamePath)
	if err != nil {
		logger.Errorf("Error while Glob targetNamePath : {%v}. err : {%v}", targetNamePath, err)
		return sessionHosts, err
	}

	logger.Debugf("targetname files matches were found : {%v}", matches)

	for _, targetPath := range matches {
		logger.Debugf("Check if targetname path (%s) is relevant for storage target (%s).", targetPath, arrayIdentifier)
		targetName, err := o.executer.IoutilReadFile(targetPath)
		if err != nil {
			logger.Warningf("Could not read target name from file : {%v}, error : {%v}", targetPath, err)
			continue
		}

		if strings.TrimSpace(string(targetName)) == arrayIdentifier {
			re := regexp.MustCompile("host([0-9]+)")
			regexMatch := re.FindStringSubmatch(targetPath)
			logger.Tracef("Found regex matches : {%v}", regexMatch)
			hostNumber := -1

			if len(regexMatch) < 2 {
				logger.Warningf("Could not find host number for targetNamePath : {%v}", targetPath)
				continue
			} else {
				hostNumber, err = strconv.Atoi(regexMatch[1])
				if err != nil {
					logger.Warningf("Host number in for target file was not valid : {%v}", regexMatch[1])
					continue
				}
			}

			sessionHosts = append(sessionHosts, hostNumber)
			logger.Debugf("targetname path (%s) found relevant for the storage target (%s). Adding host number {%v} to the session list.", targetPath, arrayIdentifier, hostNumber)

		}
	}

	if len(sessionHosts) == 0 {
		return []int{}, &ConnectivityIscsiStorageTargetNotFoundError{StorageTargetName: arrayIdentifier, DirectoryPath: targetNamePath}
	}
	return sessionHosts, nil

}

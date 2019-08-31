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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"errors"

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
	var devicePaths []string
	var errStrings []string
	lunIdStr := strconv.Itoa(lunId)

	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		return "", e
	}

	for _, arrayIdentifier := range arrayIdentifiers {
		wwn := "0x" + arrayIdentifier
		dp := strings.Join([]string{"/dev/disk/by-path/pci*", "fc", wwn, "lun", lunIdStr}, "-")
		logger.Infof("GetMpathDevice: Get the mpath devices related to wwn=%s and lunID=%s : {%v}", wwn, lunIdStr, dp)
		dps, exists, e := r.Helper.WaitForPathToExist(dp, 5, 1)
		if e != nil {
			logger.Errorf("GetMpathDevice: No device found error : %v ", e.Error())
			errStrings = append(errStrings, e.Error())
		} else if !exists {
			e := &MultipleDeviceNotFoundForLunError{volumeId, lunId, []string{wwn}}
			logger.Errorf(e.Error())
			errStrings = append(errStrings, e.Error())
		}
		devicePaths = append(devicePaths, dps...)
	}

	if len(devicePaths) == 0 && len(errStrings) != 0 {
		err := errors.New(strings.Join(errStrings, ","))
		return "", err
	}

	devicePathTosysfs := make(map[string]bool)
	// Looping over the physical devices of the volume - /dev/sdX and store all the dm devices inside map.
	for _, path := range devicePaths {
		if path != "" { // since it may return empty items
			mappedDevicePath, err := r.Helper.GetMultipathDisk(path)
			if err != nil {
				return "", err
			}

			if mappedDevicePath != "" {
				devicePathTosysfs[mappedDevicePath] = true // map it in order to save uniq dm devices
			}

		}
	}

	var mps string
	for key := range devicePathTosysfs {
		mps += ", " + key
	}
	logger.Infof("GetMpathDevice: Found multipath devices: [%s] that relats to lunId=%d and arrayIdentifiers=%s", mps, lunId, arrayIdentifiers)

	if len(devicePathTosysfs) > 1 {
		return "", &MultipleDmDevicesError{volumeId, lunId, arrayIdentifiers, devicePathTosysfs}
	}

	var md string
	for md = range devicePathTosysfs {
		break // because its a single value in the map(1 mpath device, if not it should fail above), so just take the first
	}
	return md, nil
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
	WaitForPathToExist(devicePath string, maxRetries int, intervalSeconds int) ([]string, bool, error)
	GetMultipathDisk(path string) (string, error)
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

	targetNamePath := FC_HOST_SYSFS_PATH + "/host*/port_state"
	var HostIDs []int
	matches, err := o.executer.FilepathGlob(targetNamePath)
	if err != nil {
		logger.Errorf("Error while Glob targetNamePath : {%v}. err : {%v}", targetNamePath, err)
		return HostIDs, err
	}

	logger.Debugf("targetname files matches were found : {%v}", matches)

	for _, targetPath := range matches {
		logger.Debugf("Check if targetfile (%s) value is Online.", targetPath)
		targetName, err := o.executer.IoutilReadFile(targetPath)
		if err != nil {
			logger.Warningf("Could not read target name from file : {%v}, error : {%v}", targetPath, err)
			continue
		}

		if strings.TrimSpace(string(targetName)) == "Online" {
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

			HostIDs = append(HostIDs, hostNumber)
			logger.Debugf("targetname path (%s) was found. Adding host number {%v} to the session list.", targetPath, hostNumber)

		}
	}

	if len(HostIDs) == 0 {
		return []int{}, &ConnectivityFcHostTargetNotFoundError{DirectoryPath: FC_HOST_SYSFS_PATH}
	}
	return HostIDs, nil

}

func (o OsDeviceConnectivityHelperFc) WaitForPathToExist(devicePath string, maxRetries int, intervalSeconds int) ([]string, bool, error) {
	/*
		Description:
			Try to find all the files /dev/disk/by-path/pci-*-fc-0xARRAYWWN-lun-LUNID. If not find then try again maxRetries.
	*/

	var err error
	for i := 0; i < maxRetries; i++ {
		err = nil
		fpaths, err := o.executer.FilepathGlob(devicePath)
		if err != nil {
			return nil, false, err
		}

		logger.Debugf("fpaths : {%v}", fpaths)

		if fpaths == nil {
			err = os.ErrNotExist
		} else {
			return fpaths, true, nil
		}

		time.Sleep(time.Second * time.Duration(intervalSeconds))
	}
	return nil, false, err
}

func (o OsDeviceConnectivityHelperFc) GetMultipathDisk(path string) (string, error) {
	/*
		Description:
			1. Get the name of the device(e.g: sdX) by check `path` as slink to the device.
			   e.g: Where path=/dev/disk/by-path/pci-*-fc-0xwwn-lun-<LUNID> which slink to "../../sdX"
			        /dev/disk/by-path/pci-*-fc-0xwwn-lun-<LUNID> -> ../../sdX
			2. After having sdX, the function loop over all the files in /sys/block/dm-*\/slaves/sd<X> and return its relevant <dm-*>.
			   The <dm-*> is actually the second directory of the path /sys/block/dm-*\/slaves/sd<X>.
			   e.g: function will return dm-1 for this path=/dev/disk/by-path/pci-0000:13:00.0-fc-0x500507680b25c0aa-lun-0,
					Because the /dev/disk/by-path/pci-0000:13:00.0-fc-0x500507680b25c0aa-lun-0 -> ../../sda
					And listing all the /sys/block/dm-*\/slaves/sda  will be with dm-1. So the fucntion will return dm-1.

		Return Value:
			dm-<X>
	*/

	// Follow link to destination directory
	logger.Debugf("Getting multipath device for given path %s", path)

	// Get the sdX which is the file that path link to.
	devicePath, err := o.executer.OsReadlink(path)
	if err != nil {
		logger.Errorf("Error reading link for multipath disk: %s. error: {%s}\n", path, err.Error())
		return "", err
	}

	// Get only the physical device from /dev/disk/by-path/pci-*-fc-0xwwn-lun-<LUNID> -> ../../sdb
	sdevice := filepath.Base(devicePath)

	// If destination directory is already identified as a multipath device,
	// just return its path
	if strings.HasPrefix(sdevice, "dm-") {
		logger.Debugf("Already found multipath device: %s", sdevice)
		return sdevice, nil
	}

	// Fallback to iterating through all the entries under /sys/block/dm-* and
	// check to see if any have an entry under /sys/block/dm-*/slaves matching
	// the device the symlink was pointing at
	dmPaths, err := o.executer.FilepathGlob("/sys/block/dm-*")
	// TODO improve looping by just filepath.Glob("/sys/block/dm-*/slaves/" + sdevice) and then no loops needed below, since it will just find the device directly.

	if err != nil {
		logger.Errorf("Glob error: %s", err)
		return "", err
	}
	for _, dmPath := range dmPaths {
		sdevices, err := o.executer.FilepathGlob(filepath.Join(dmPath, "slaves", "*"))
		if err != nil {
			logger.Warningf("Glob error: %s", err)
		}
		for _, spath := range sdevices {
			s := filepath.Base(spath)
			if sdevice == s {
				// We've found a matching entry, return the path for the
				// dm-* device it was found under
				// for Example, return /dev/dm-3
				//   ls -l  /sys/block/dm-*/slaves/*
				//    /sys/block/dm-3/slaves/sdb -> ../../../../pci0000:00/0000:00:17.0/0000:13:00.0/host33/rport-33:0-3/target33:0:1/33:0:1:0/block/sdb

				p := filepath.Join("/dev", filepath.Base(dmPath))
				logger.Debugf("Found matching multipath device: %s under dm-* device path %s", sdevice, dmPath)
				return p, nil
			}
		}
	}

	err = &MultipleDeviceNotFoundError{path, devicePath}
	logger.Errorf(err.Error())
	return "", err
}

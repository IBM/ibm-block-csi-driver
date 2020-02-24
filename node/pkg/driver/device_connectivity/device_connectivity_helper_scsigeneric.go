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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperScsiGenericInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperScsiGenericInterface

type OsDeviceConnectivityHelperScsiGenericInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityHelperScsiGenericInterface.
		Mainly for writing clean unit testing, so we can Mock this interface in order to unit test logic.
	*/
	RescanDevices(lunId int, arrayIdentifiers []string) error
	GetMpathDevice(volumeId string, lunId int, arrayIdentifiers []string, connectivityType string) (string, error)
	FlushMultipathDevice(mpathDevice string) error
	RemovePhysicalDevice(sysDevices []string) error
}

type OsDeviceConnectivityHelperScsiGeneric struct {
	Executer        executer.ExecuterInterface
	Helper          OsDeviceConnectivityHelperInterface
	MutexMultipathF *sync.Mutex
}

var (
	TimeOutMultipathFlashCmd = 4 * 1000
)

const (
	DevPath = "/dev"
)

func NewOsDeviceConnectivityHelperScsiGeneric(executer executer.ExecuterInterface) OsDeviceConnectivityHelperScsiGenericInterface {
	return &OsDeviceConnectivityHelperScsiGeneric{
		Executer:        executer,
		Helper:          NewOsDeviceConnectivityHelperGeneric(executer),
		MutexMultipathF: &sync.Mutex{},
	}
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevices(lunId int, arrayIdentifiers []string) error {
	logger.Debugf("Rescan : Start rescan on specific lun, on lun : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	var hostIDs []int
	var errStrings []string
	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		logger.Errorf(e.Error())
		return e
	}

	for _, arrayIdentifier := range arrayIdentifiers {
		hostsId, e := r.Helper.GetHostsIdByArrayIdentifier(arrayIdentifier)
		if e != nil {
			logger.Errorf(e.Error())
			errStrings = append(errStrings, e.Error())
		}
		hostIDs = append(hostIDs, hostsId...)
	}
	if len(hostIDs) == 0 && len(errStrings) != 0 {
		err := errors.New(strings.Join(errStrings, ","))
		return err
	}
	for _, hostNumber := range hostIDs {

		filename := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", hostNumber)
		f, err := r.Executer.OsOpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200)
		if err != nil {
			logger.Errorf("Rescan Error: could not open filename : {%v}. err : {%v}", filename, err)
			return err
		}

		defer f.Close()

		scanCmd := fmt.Sprintf("- - %d", lunId)
		logger.Debugf("Rescan host device : echo %s > %s", scanCmd, filename)
		if written, err := r.Executer.FileWriteString(f, scanCmd); err != nil {
			logger.Errorf("Rescan Error: could not write to rescan file :{%v}, error : {%v}", filename, err)
			return err
		} else if written == 0 {
			e := &ErrorNothingWasWrittenToScanFileError{filename}
			logger.Errorf(e.Error())
			return e
		}

	}

	logger.Debugf("Rescan : finish rescan lun on lun id : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	return nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(volumeId string, lunId int, arrayIdentifiers []string, connectivityType string) (string, error) {
	logger.Infof("GetMpathDevice: Searching multipath devices for volume : [%s] that relats to lunId=%d and arrayIdentifiers=%s", volumeId, lunId, arrayIdentifiers)

	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		return "", e
	}
	var devicePaths []string
	var errStrings []string
	var targetPath string
	lunIdStr := strconv.Itoa(lunId)

	if connectivityType == "fc" {
		targetPath = fmt.Sprintf("/dev/disk/by-path/%s*", fcSubsystem)
		// In host, the path like this: /dev/disk/by-path/pci-0000:13:00.0-fc-0x500507680b25c0aa-lun-0
		// So add prefix "0x" for the arrayIdentifiers
		for index, wwn := range arrayIdentifiers {
			arrayIdentifiers[index] = "0x" + strings.ToLower(wwn)
		}
	}
	if connectivityType == "iscsi" {
		targetPath = "/dev/disk/by-path/ip*"
	}

	for _, arrayIdentifier := range arrayIdentifiers {
		dp := strings.Join([]string{targetPath, connectivityType, arrayIdentifier, "lun", lunIdStr}, "-")
		logger.Infof("GetMpathDevice: Get the mpath devices related to connectivityType=%s initiator=%s and lunID=%s : {%v}", connectivityType, arrayIdentifier, lunIdStr, dp)
		dps, exists, e := r.Helper.WaitForPathToExist(dp, 5, 1)
		if e != nil {
			logger.Errorf("GetMpathDevice: No device found error : %v ", e.Error())
			errStrings = append(errStrings, e.Error())
		} else if !exists {
			e := &MultipleDeviceNotFoundForLunError{volumeId, lunId, []string{arrayIdentifier}}
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

func (r OsDeviceConnectivityHelperScsiGeneric) FlushMultipathDevice(mpathDevice string) error {
	// mpathdevice is dm-4 for example
	logger.Debugf("Flushing mpath device : {%v}", mpathDevice)

	fullDevice := filepath.Join(DevPath, mpathDevice)

	logger.Debugf("Try to acquire lock for running the command multipath -f {%v} (to avoid concurrent multipath commands)", mpathDevice)
	r.MutexMultipathF.Lock()
	logger.Debugf("Acquired lock for multipath -f command")
	_, err := r.Executer.ExecuteWithTimeout(TimeOutMultipathFlashCmd, "multipath", []string{"-f", fullDevice})
	r.MutexMultipathF.Unlock()

	if err != nil {
		if _, e := os.Stat(fullDevice); os.IsNotExist(e) {
			logger.Debugf("Mpath device {%v} was deleted", fullDevice)
		} else {
			logger.Errorf("multipath -f {%v} did not succeed to delete the device. err={%v}", fullDevice, err.Error())
			return err
		}
	}

	logger.Debugf("Finshed flushing mpath device : {%v}", mpathDevice)
	return nil

}

func (r OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(sysDevices []string) error {
	// sysDevices  = sdb, sda,...
	logger.Debugf("Removing scsi device : {%v}", sysDevices)
	// NOTE: this func could be also relevant for SCSI (not only for iSCSI)
	var (
		f   *os.File
		err error
	)

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}

		filename := fmt.Sprintf("/sys/block/%s/device/delete", deviceName)
		logger.Debugf("Delete scsi device by open the device delete file : {%v}", filename)

		if f, err = os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200); err != nil {
			if os.IsNotExist(err) {
				logger.Warningf("Idempotency: Block device {%v} was not found on the system, so skip deleting it", deviceName)
				continue
			} else {
				logger.Errorf("Error while opening file : {%v}. error: {%v}", filename, err.Error())
				return err
			}
		}

		defer f.Close()

		if _, err := f.WriteString("1"); err != nil {
			logger.Errorf("Error while writing to file : {%v}. error: {%v}", filename, err.Error())
			return err // TODO: maybe we need to just swallow the error and continnue??
		}
	}
	logger.Debugf("Finshed to remove SCSI devices : {%v}", sysDevices)
	return nil
}

// ============== OsDeviceConnectivityHelperInterface ==========================

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperInterface

type OsDeviceConnectivityHelperInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityScsiGeneric.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test OsDeviceConnectivityHelperGeneric logic.
	*/
	WaitForPathToExist(devicePath string, maxRetries int, intervalSeconds int) ([]string, bool, error)
	GetMultipathDisk(path string) (string, error)
	GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error)
}

type OsDeviceConnectivityHelperGeneric struct {
	executer executer.ExecuterInterface
}

func NewOsDeviceConnectivityHelperGeneric(executer executer.ExecuterInterface) OsDeviceConnectivityHelperInterface {
	return &OsDeviceConnectivityHelperGeneric{executer: executer}
}

func (o OsDeviceConnectivityHelperGeneric) WaitForPathToExist(devicePath string, maxRetries int, intervalSeconds int) ([]string, bool, error) {
	/*
				Description:
					Try to find all the files
					iSCSI -> /dev/disk/by-path/ip*-iscsi-<Array-WWN>-lun-<LUN-ID>
		            FC   -> /dev/disk/by-path/pci-*-fc-<Array-WWN>-lun-<LUN-ID>
					If not find then try again maxRetries.
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

func (o OsDeviceConnectivityHelperGeneric) GetMultipathDisk(path string) (string, error) {
	/*
		Description:
			1. Get the name of the device(e.g: sdX) by check `path` as slink to the device.
			   e.g: Where path=/dev/disk/by-path/pci-*-fc-0xwwn-lun-<LUNID> which slink to "../../sdX"
			        /dev/disk/by-path/pci-*-fc-0xwwn-lun-<LUNID> -> ../../sdX
			        or
			        /dev/disk/by-path/TARGET-iscsi-iqn:<LUNID> -> ../../sdX
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
	// or /dev/disk/by-path/TARGET-iscsi-iqn:<LUNID> -> ../../sdb
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

				p := filepath.Join(DevPath, filepath.Base(dmPath))
				logger.Debugf("Found matching multipath device: %s under dm-* device path %s", sdevice, dmPath)
				return p, nil
			}
		}
	}

	err = &MultipleDeviceNotFoundError{path, devicePath}
	logger.Errorf(err.Error())
	return "", err
}

const (
	FC_HOST_SYSFS_PATH = "/sys/class/fc_remote_ports/rport-*/port_name"
	IscsiHostRexExPath = "/sys/class/iscsi_host/host*/device/session*/iscsi_session/session*/targetname"
)

func (o OsDeviceConnectivityHelperGeneric) GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error) {
	/*
		Description:
			This function find all the hosts IDs under directory /sys/class/fc_host/ or /sys/class/iscsi_host"
			So the function goes over all the above hosts and return back only the host numbers as a list.
	*/
	//arrayIdentifier is wwn, value is 500507680b25c0aa
	var targetFilePath string
	var regexpValue string

	//IQN format is iqn.yyyy-mm.naming-authority:unique name
	//For example: iqn.1986-03.com.ibm:2145.v7k194.node2
	iscsiMatchRex := `^iqn\.(\d{4}-\d{2})\.([^:]+)(:)([^,:\s']+)`
	isIscsi, err := regexp.MatchString(iscsiMatchRex, arrayIdentifier)
	if isIscsi {
		targetFilePath = IscsiHostRexExPath
		regexpValue = "host([0-9]+)"
	} else {
		targetFilePath = FC_HOST_SYSFS_PATH
		regexpValue = "rport-([0-9]+)"
	}

	var HostIDs []int
	matches, err := o.executer.FilepathGlob(targetFilePath)
	if err != nil {
		logger.Errorf("Error while Glob targetFilePath : {%v}. err : {%v}", targetFilePath, err)
		return nil, err
	}

	logger.Debugf("targetname files matches were found : {%v}", matches)

	re := regexp.MustCompile(regexpValue)
	for _, targetPath := range matches {
		logger.Debugf("Check if targetname path (%s) is relevant for storage target (%s).", targetPath, arrayIdentifier)
		targetName, err := o.executer.IoutilReadFile(targetPath)
		if err != nil {
			logger.Warningf("Could not read target name from file : {%v}, error : {%v}", targetPath, err)
			continue
		}
		identifierFromHost := strings.TrimSpace(string(targetName))
		//For FC WWNs from the host, the value will like this: 0x500507680b26c0aa, but the arrayIdentifier doesn't has this prefix
		if strings.HasPrefix(identifierFromHost, "0x") {
			logger.Tracef("Remove the 0x prefix for: {%v}", identifierFromHost)
			identifierFromHost = strings.TrimLeft(identifierFromHost, "0x")
		}
		if strings.EqualFold(identifierFromHost, arrayIdentifier) {
			regexMatch := re.FindStringSubmatch(targetPath)
			logger.Tracef("Found regex matches : {%v}", regexMatch)
			hostNumber := -1

			if len(regexMatch) < 2 {
				logger.Warningf("Could not find host number for targetFilePath : {%v}", targetPath)
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
		return []int{}, &ConnectivityIdentifierStorageTargetNotFoundError{StorageTargetName: arrayIdentifier, DirectoryPath: targetFilePath}
	}

	return HostIDs, nil

}

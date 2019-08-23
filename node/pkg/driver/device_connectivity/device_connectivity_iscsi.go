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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"k8s.io/klog"
	"errors"
)

type OsDeviceConnectivityIscsi struct {
	Executer        executer.ExecuterInterface
	MutexMultipathF *sync.Mutex
	Helper          OsDeviceConnectivityHelperIscsiInterface
}

func NewOsDeviceConnectivityIscsi(executer executer.ExecuterInterface) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityIscsi{
		Executer:        executer,
		MutexMultipathF: &sync.Mutex{},
		Helper:          NewOsDeviceConnectivityHelperIscsi(executer),
	}
}

var (
	TimeOutMultipathFlashCmd = 4 * 1000
)

func (r OsDeviceConnectivityIscsi) RescanDevices(lunId int, arrayIdentifiers []string) error {
	klog.V(5).Infof("Rescan : Start rescan on specific lun, on lun : {%v}, with array iqn : {%v}", lunId, arrayIdentifiers)
	var sessionHosts []int
	var errStrings []string

	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		klog.Errorf(e.Error())
		return e
	}

	for _, iqn := range arrayIdentifiers{
		hostsId, e := r.Helper.GetIscsiSessionHostsForArrayIQN(iqn)
		if e != nil {
			klog.Errorf(e.Error())
			errStrings = append(errStrings, e.Error())
		}
		sessionHosts = append(sessionHosts, hostsId...)
	}
	if len(sessionHosts) == 0 && len(errStrings) != 0 {
		err := errors.New(strings.Join(errStrings, ","))
		return err
	}

	// TODO the below operations are SCSI and iSCSI, we should move it out to generic package.
	for _, hostNumber := range sessionHosts {

		filename := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", hostNumber)
		f, err := r.Executer.OsOpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200)
		if err != nil {
			klog.Errorf("Rescan Error: could not open filename : {%v}. err : {%v}", filename, err)
			return err
		}

		defer f.Close()

		scanCmd := fmt.Sprintf("0 0 %d", lunId)
		klog.V(5).Infof("Rescan host device : echo %s > %s", scanCmd, filename)
		if written, err := r.Executer.FileWriteString(f, scanCmd); err != nil {
			klog.Errorf("Rescan Error: could not write to rescan file :{%v}, error : {%v}", filename, err)
			return err
		} else if written == 0 {
			e := &ErrorNothingWasWrittenToScanFileError{filename}
			klog.Errorf(e.Error())
			return e
		}

	}

	klog.V(5).Infof("Rescan : finsihed rescan lun on lun id : {%v}, with array iqns : {%v}", lunId, arrayIdentifiers)
	return nil

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
	var devicePaths []string
	var errStrings []string
	lunIdStr := strconv.Itoa(lunId)

	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		return "", e
	}

	for _, iqn := range arrayIdentifiers {
		dp := strings.Join([]string{"/dev/disk/by-path/ip*", "iscsi", iqn, "lun", lunIdStr}, "-")
		klog.V(4).Infof("GetMpathDevice: Get the mpath devices related to arrayIdentifier=%s and lunID=%s : {%v}", iqn, lunIdStr, dp)
		dps, exists, e := r.Helper.WaitForPathToExist(dp, 5, 1)
		if e != nil {
			klog.Errorf("GetMpathDevice: No device found error : %v ", e.Error())
			errStrings = append(errStrings, e.Error())
		} else if !exists {
			e := &MultipleDeviceNotFoundForLunError{volumeId, lunId, []string{iqn}}
			klog.Errorf(e.Error())
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
	klog.V(4).Infof("GetMpathDevice: Found multipath devices: [%s] that relats to lunId=%d and arrayIdentifiers=%s", mps, lunId, arrayIdentifiers)

	if len(devicePathTosysfs) > 1 {
		return "", &MultipleDmDevicesError{volumeId, lunId, arrayIdentifiers, devicePathTosysfs}
	}

	var md string
	for md = range devicePathTosysfs {
		break // because its a single value in the map(1 mpath device, if not it should fail above), so just take the first
	}
	return md, nil
}

func (r OsDeviceConnectivityIscsi) FlushMultipathDevice(mpathDevice string) error {
	// mpathdevice is dm-4 for example
	// TODO since this function can be used also for FC SCSI (not only) iSCSI, we should move it out to generic.

	klog.V(5).Infof("Flushing mpath device : {%v}", mpathDevice)

	fullDevice := "/dev/" + mpathDevice

	klog.V(5).Infof("Try to acquire lock for running the command multipath -f {%v} (to avoid concurrent multipath commands)", mpathDevice)
	r.MutexMultipathF.Lock()
	klog.V(5).Infof("Acquired lock for multipath -f command")
	_, err := r.Executer.ExecuteWithTimeout(TimeOutMultipathFlashCmd, "multipath", []string{"-f", fullDevice})
	r.MutexMultipathF.Unlock()

	if err != nil {
		if _, errOpen := os.Open(fullDevice); errOpen != nil {
			if os.IsNotExist(errOpen) {
				klog.V(5).Infof("Mpath device {%v} was deleted", fullDevice)
			} else {
				klog.Errorf("Error while opening file : {%v}. error: {%v}. Means the multipath -f {%v} did not succeed to delete the device.", fullDevice, errOpen.Error(), fullDevice)
				return errOpen
			}
		} else {
			klog.Errorf("multipath -f {%v} did not succeed to delete the device. err={%v}", fullDevice, err.Error())
			return err
		}
	}

	klog.V(5).Infof("Finshed flushing mpath device : {%v}", mpathDevice)
	return nil

}

func (r OsDeviceConnectivityIscsi) RemovePhysicalDevice(sysDevices []string) error {
	// sysDevices  = sdb, sda,...
	klog.V(5).Infof("Removing iscsi device : {%v}", sysDevices)
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
		klog.V(5).Infof("Delete scsi device by open the device delete file : {%v}", filename)

		if f, err = os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200); err != nil {
			if os.IsNotExist(err) {
				klog.Warningf("Idempotency: Block device {%v} was not found on the system, so skip deleting it", deviceName)
			} else {
				klog.Errorf("Error while opening file : {%v}. error: {%v}", filename, err.Error())
				return err
			}
		}

		defer f.Close()

		if _, err := f.WriteString("1"); err != nil {
			klog.Errorf("Error while writing to file : {%v}. error: {%v}", filename, err.Error())
			return err // TODO: maybe we need to just swallow the error and continnue??
		}
	}
	klog.V(5).Infof("Finshed to remove iSCSI devices : {%v}", sysDevices)
	return nil
}

// ============== OsDeviceConnectivityHelperIscsiInterface ==========================

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperIscsiInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperIscsiInterface

type OsDeviceConnectivityHelperIscsiInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityIscsi.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test OsDeviceConnectivityIscsi logic.
	*/
	WaitForPathToExist(devicePath string, maxRetries int, intervalSeconds int) ([]string, bool, error)
	GetMultipathDisk(path string) (string, error)
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

func (o OsDeviceConnectivityHelperIscsi) WaitForPathToExist(devicePath string, maxRetries int, intervalSeconds int) ([]string, bool, error) {
	/*
		Description:
			Try to find all the files /dev/disk/by-path/ip-*-iscsi-ARRAYIQN-lun-LUNID. If not find then try again maxRetries.
	*/

	var err error
	for i := 0; i < maxRetries; i++ {
		err = nil
		fpaths, err := o.executer.FilepathGlob(devicePath)
		if err != nil {
			return nil, false, err
		}

		klog.V(4).Infof("fpaths : {%v}", fpaths)

		if fpaths == nil {
			err = os.ErrNotExist
		} else {
			return fpaths, true, nil
		}

		time.Sleep(time.Second * time.Duration(intervalSeconds))
	}
	return nil, false, err
}

func (o OsDeviceConnectivityHelperIscsi) GetMultipathDisk(path string) (string, error) {
	/*
		Description:
			1. Get the name of the device(e.g: sdX) by check `path` as slink to the device.
			   e.g: Where path=/dev/disk/by-path/TARGET-iscsi-iqn:<LUNID> which slink to "../../sdX"
			        /dev/disk/by-path/TARGET-iscsi-iqn:<LUNID> -> ../../sdX
			2. After having sdX, the function loop over all the files in /sys/block/dm-*\/slaves/sd<X> and return its relevant <dm-*>.
			   The <dm-*> is actually the second directory of the path /sys/block/dm-*\/slaves/sd<X>.
			   e.g: function will return dm-1 for this path=/dev/disk/by-path/TARGET-iscsi-iqn:5,
					Because the /dev/disk/by-path/TARGET-iscsi-iqn:5 -> ../../sda
					And listing all the /sys/block/dm-*\/slaves/sda  will be with dm-1. So the fucntion will return dm-1.

		Return Value:
			dm-<X>
	*/

	// Follow link to destination directory
	klog.V(5).Infof("Getting multipath device for given path %s", path)

	// Get the sdX which is the file that path link to.
	devicePath, err := o.executer.OsReadlink(path)
	if err != nil {
		klog.Errorf("Error reading link for multipath disk: %s. error: {%s}\n", path, err.Error())
		return "", err
	}

	// Get only the physical device from /dev/disk/by-path/TARGET-iscsi-iqn:<LUNID> -> ../../sdb
	sdevice := filepath.Base(devicePath)

	// If destination directory is already identified as a multipath device,
	// just return its path
	if strings.HasPrefix(sdevice, "dm-") {
		klog.V(4).Infof("Already found multipath device: %s", sdevice)
		return sdevice, nil
	}

	// Fallback to iterating through all the entries under /sys/block/dm-* and
	// check to see if any have an entry under /sys/block/dm-*/slaves matching
	// the device the symlink was pointing at
	dmPaths, err := o.executer.FilepathGlob("/sys/block/dm-*")
	// TODO improve looping by just filepath.Glob("/sys/block/dm-*/slaves/" + sdevice) and then no loops needed below, since it will just find the device directly.

	if err != nil {
		klog.Errorf("Glob error: %s", err)
		return "", err
	}
	for _, dmPath := range dmPaths {
		sdevices, err := o.executer.FilepathGlob(filepath.Join(dmPath, "slaves", "*"))
		if err != nil {
			klog.Warningf("Glob error: %s", err)
		}
		for _, spath := range sdevices {
			s := filepath.Base(spath)
			if sdevice == s {
				// We've found a matching entry, return the path for the
				// dm-* device it was found under
				// for Example, return /dev/dm-3
				//   ls -l  /sys/block/dm-*/slaves/*
				//    /sys/block/dm-3/slaves/sdb -> ../../../../platform/host41/session9/target41:0:0/41:0:0:13/block/sdb

				p := filepath.Join("/dev", filepath.Base(dmPath))
				klog.V(4).Infof("Found matching multipath device: %s under dm-* device path %s", sdevice, dmPath)
				return p, nil
			}
		}
	}

	err = &MultipleDeviceNotFoundError{path, devicePath}
	klog.Errorf(err.Error())
	return "", err
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
		klog.Errorf("Error while Glob targetNamePath : {%v}. err : {%v}", targetNamePath, err)
		return sessionHosts, err
	}

	klog.V(5).Infof("targetname files matches were found : {%v}", matches)

	for _, targetPath := range matches {
		klog.V(5).Infof("Check if targetname path (%s) is relevant for storage target (%s).", targetPath, arrayIdentifier)
		targetName, err := o.executer.IoutilReadFile(targetPath)
		if err != nil {
			klog.Warningf("Could not read target name from file : {%v}, error : {%v}", targetPath, err)
			continue
		}

		if strings.TrimSpace(string(targetName)) == arrayIdentifier {
			re := regexp.MustCompile("host([0-9]+)")
			regexMatch := re.FindStringSubmatch(targetPath)
			klog.V(6).Infof("Found regex matches : {%v}", regexMatch)
			hostNumber := -1

			if len(regexMatch) < 2 {
				klog.Warningf("Could not find host number for targetNamePath : {%v}", targetPath)
				continue
			} else {
				hostNumber, err = strconv.Atoi(regexMatch[1])
				if err != nil {
					klog.Warningf("Host number in for target file was not valid : {%v}", regexMatch[1])
					continue
				}
			}

			sessionHosts = append(sessionHosts, hostNumber)
			klog.V(5).Infof("targetname path (%s) found relevant for the storage target (%s). Adding host number {%v} to the session list.", targetPath, arrayIdentifier, hostNumber)

		}
	}

	if len(sessionHosts) == 0 {
		return []int{}, &ConnectivityIscsiStorageTargetNotFoundError{StorageTargetName: arrayIdentifier, DirectoryPath: targetNamePath}
	}
	return sessionHosts, nil

}

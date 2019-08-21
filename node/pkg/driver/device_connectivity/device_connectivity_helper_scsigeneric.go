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
	"sync"

	executer "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"k8s.io/klog"
)


//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperScsiGenericInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperScsiGenericInterface

type OsDeviceConnectivityHelperScsiGenericInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityHelperScsiGenericInterface.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test logic.
	*/
	RescanDevices(lunId int, arrayIdentifier string, sessionHosts []int) error
	FlushMultipathDevice(mpathDevice string) error
	RemovePhysicalDevice(sysDevices []string) error
}

type OsDeviceConnectivityHelperScsiGeneric struct {
	Executer executer.ExecuterInterface
	MutexMultipathF *sync.Mutex	
}

func NewOsDeviceConnectivityHelperScsiGeneric(executer executer.ExecuterInterface) OsDeviceConnectivityHelperScsiGenericInterface {
	return &OsDeviceConnectivityHelperScsiGeneric{
		Executer: executer,
		MutexMultipathF: &sync.Mutex{},
	}
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevices(lunId int, arrayIdentifier string, sessionHosts []int) error {
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

	klog.V(5).Infof("Rescan : finish rescan lun on lun id : {%v}, with array identifier : {%v}", lunId, arrayIdentifier)
	return nil
}



func (r OsDeviceConnectivityHelperScsiGeneric) FlushMultipathDevice(mpathDevice string) error {
	// mpathdevice is dm-4 for example
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



func (r OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(sysDevices []string) error {
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


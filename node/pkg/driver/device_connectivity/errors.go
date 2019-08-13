package device_connectivity

import (
	"fmt"
)

type MultipleDmDevicesError struct {
	VolumeId            string
	LunId               int
	ArrayIqn            string
	MultipathDevicesMap map[string]bool
}

func (e *MultipleDmDevicesError) Error() string {
	var mps string
	for key := range e.MultipathDevicesMap {
		mps += ", " + key
	}
	return fmt.Sprintf("Detected more then one multipath devices (%s) for single volume (%s) with lunID %d from array target iqn %s", mps, e.VolumeId, e.LunId, e.ArrayIqn)
}

type MultipleDeviceNotFoundForLunError struct {
	VolumeId string
	LunId    int
	ArrayIqn string
}

func (e *MultipleDeviceNotFoundForLunError) Error() string {
	return fmt.Sprintf("Couldn't find multipath device for volumeID [%s] lunID [%d] from array [%s]. Please check the host connectivity to the storage.", e.VolumeId, e.LunId, e.ArrayIqn)
}

type ConnectivityIscsiStorageTargetNotFoundError struct {
	StorageTargetName string
	DirectoryPath     string
}

func (e *ConnectivityIscsiStorageTargetNotFoundError) Error() string {
	return fmt.Sprintf("Connectivity Error: Storage target name [%s] was not found on the host, under directory %s. Please check the host connectivity to the storage.", e.StorageTargetName, e.DirectoryPath)
}

type MultipleDeviceNotFoundError struct {
	DiskByPathDevice     string
	LinkToPhysicalDevice string
}

func (e *MultipleDeviceNotFoundError) Error() string {
	return fmt.Sprintf("Couldn't find dm-* of the physical device path [%s -> %s]. Please check the host connectivity to the storage.", e.DiskByPathDevice, e.LinkToPhysicalDevice)
}

type ErrorNothingWasWrittenToScanFileError struct {
	path string
}

func (e *ErrorNothingWasWrittenToScanFileError) Error() string {
	return fmt.Sprintf("Rescan Error: Nothing was written to rescan file : {%s}", e.path)
}

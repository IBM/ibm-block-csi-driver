package device_connectivity

import (
	"fmt"
)

type MultipleDmDevicesError struct {
	VolumeId            string
	LunId               int
	ArrayIqns           []string
	MultipathDevicesMap map[string]bool
}

func (e *MultipleDmDevicesError) Error() string {
	var mps string
	for key := range e.MultipathDevicesMap {
		mps += ", " + key
	}
	return fmt.Sprintf("Detected more then one multipath devices (%s) for single volume (%s) with lunID %d from array target iqn %v", mps, e.VolumeId, e.LunId, e.ArrayIqns)
}

type MultipleDeviceNotFoundForLunError struct {
	VolumeId  string
	LunId     int
	ArrayIqns []string
}

func (e *MultipleDeviceNotFoundForLunError) Error() string {
	return fmt.Sprintf("Couldn't find multipath device for volumeID [%s] lunID [%d] from array [%s]. Please check the host connectivity to the storage.", e.VolumeId, e.LunId, e.ArrayIqns)
}

type ConnectivityIdentifierStorageTargetNotFoundError struct {
	StorageTargetName string
	DirectoryPath     string
}

func (e *ConnectivityIdentifierStorageTargetNotFoundError) Error() string {
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

type ErrorNotFoundArrayIdentifiers struct {
	lunId int
}

func (e *ErrorNotFoundArrayIdentifiers) Error() string {
	return fmt.Sprintf("Couldn't find arrayIdentifiers found for lunId: {%d}", e.lunId)
}

package device_connectivity

import (
	"fmt"
)

type MultipleDmFieldValuesError struct {
	Validator     string
	DmFieldValues map[string]bool
}

func (e *MultipleDmFieldValuesError) Error() string {
	return fmt.Sprintf("Detected more than one (%v) for single (%s)", e.DmFieldValues, e.Validator)
}

type MultipathDeviceNotFoundForVolumeError struct {
	VolumeId string
}

func (e *MultipathDeviceNotFoundForVolumeError) Error() string {
	return fmt.Sprintf("Couldn't find multipath device for volumeID [%s]. Please check the host connectivity to the storage.", e.VolumeId)
}

type MultipathDeviceNotFoundForVolumePathError struct {
	VolumePath string
}

func (e *MultipathDeviceNotFoundForVolumePathError) Error() string {
	return fmt.Sprintf("Couldn't find multipath device for VolumePath [%s]. Please verify the path is mounted", e.VolumePath)
}

type VolumeIdNotFoundForMultipathDeviceNameError struct {
	mpathDeviceName string
}

func (e *VolumeIdNotFoundForMultipathDeviceNameError) Error() string {
	return fmt.Sprintf("Couldn't find Volume Id for Multipath device name [%s]. Please check the host connectivity to the storage.", e.mpathDeviceName)
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
	return fmt.Sprintf("Multipath device(dm) is not found for this physical device [%s -> %s], this can happen when there is only one path to the storage system. Please verify that you have more than one path connected to the storage system.", e.DiskByPathDevice, e.LinkToPhysicalDevice)
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

type ErrorNoRegexWwnMatchInScsiInq struct {
	dev  string
	line string
}

func (e *ErrorNoRegexWwnMatchInScsiInq) Error() string {
	return fmt.Sprintf("Could not find wwn pattern in sg_inq of mpath devive: [%s] in line Vendor Specific "+
		"Identifier Extension: [%s]", e.dev, e.line)
}

type ErrorWrongDeviceFound struct {
	DevPath       string
	DmVolumeId    string
	SgInqVolumeId string
}

func (e *ErrorWrongDeviceFound) Error() string {
	return fmt.Sprintf("Multipath device [%s] was found as WWN [%s] via multipath -ll command, "+
		"BUT sg_inq identify this device as a different WWN: [%s]. Check your multipathd.", e.DevPath,
		e.DmVolumeId, e.SgInqVolumeId)
}

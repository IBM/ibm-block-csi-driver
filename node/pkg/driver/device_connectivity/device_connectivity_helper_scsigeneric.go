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
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperScsiGenericInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperScsiGenericInterface

type OsDeviceConnectivityHelperScsiGenericInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityHelperScsiGenericInterface.
		Mainly for writing clean unit testing, so we can Mock this interface in order to unit test logic.
	*/
	RescanDevices(lunId int, arrayIdentifiers []string) error
	GetMpathDevice(volumeId string) (string, error)
	FlushMultipathDevice(mpathDevice string) error
	RemovePhysicalDevice(sysDevices []string) error
	ValidateLun(lun int, sysDevices []string) error
	GetVolumeIdVariations(volumeUuid string) []string
	GetVolumeIdByVolumePath(volumePath string, volumeId string) (string, error)
}

type OsDeviceConnectivityHelperScsiGeneric struct {
	Executer        executer.ExecuterInterface
	Helper          OsDeviceConnectivityHelperInterface
	MutexMultipathF *sync.Mutex
}

type WaitForMpathResult struct {
	devicesPaths []string
	err          error
}

var (
	TimeOutMultipathCmd                     = 60 * 1000
	TimeOutMultipathdCmd                    = 10 * 1000
	TimeOutBlockDevCmd                      = 10 * 1000
	TimeOutSgInqCmd                         = 3 * 1000
	MultipathdWildcardsMpathAndVolumeId     = []string{"%w", "%d"}
	multipathdWildcardsMpathNameAndVolumeId = []string{"%n", "%w"}
)

const (
	DevPath                     = "/dev"
	devMapperPath               = "/dev/mapper"
	WaitForMpathRetries         = 5
	WaitForMpathWaitIntervalSec = 1
	FcHostSysfsPath             = "/sys/class/fc_remote_ports/rport-*/port_name"
	IscsiHostRexExPath          = "/sys/class/iscsi_host/host*/device/session*/iscsi_session/session*/targetname"
	sysDeviceSymLinkFormat      = "/sys/block/%s/device"
	sysDeviceDeletePathFormat   = sysDeviceSymLinkFormat + "/delete"
	blockDevCmd                 = "blockdev"
	flushBufsFlag               = "--flushbufs"
	mpathdSeparator             = ","
	multipathdCmd               = "multipathd"
	multipathCmd                = "multipath"
	WwnOuiEnd                   = 7
	WwnVendorIdentifierEnd      = 16
	procMountsFilePath          = "/proc/mounts"
)

func NewOsDeviceConnectivityHelperScsiGeneric(executer executer.ExecuterInterface) OsDeviceConnectivityHelperScsiGenericInterface {
	return &OsDeviceConnectivityHelperScsiGeneric{
		Executer:        executer,
		Helper:          NewOsDeviceConnectivityHelperGeneric(executer),
		MutexMultipathF: &sync.Mutex{},
	}
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetVolumeIdByVolumePath(volumePath string, volumeId string) (string, error) {
	logger.Infof("GetVolumeIdByVolumePath: Searching matching volume id for volume path: [%s] ", volumePath)
	volumeUids := r.GetVolumeIdVariations(volumeId)
	mpathdOutput, err := r.Helper.GetMpathOutputByVolumeId(volumeUids)
	if err != nil {
		return "", err
	}

	mpathDeviceName, err := r.Helper.GetMpathDeviceName(volumePath)
	if err != nil {
		return "", err
	}
	dmPath := filepath.Join(devMapperPath, filepath.Base(strings.TrimSpace(mpathDeviceName)))

	volumeId, _ = r.Helper.GetVolumeIdByMpathName(mpathDeviceName, mpathdOutput)
	if volumeId != "" {
		if r.Helper.IsMpathMatchVolumeIdWithoutErrors(dmPath, volumeUids) {
			return volumeId, nil
		}
	}

	if err := r.Helper.ReloadMultipath(); err != nil {
		return "", err
	}

	volumeId, err = r.Helper.GetVolumeIdByMpathName(mpathDeviceName, mpathdOutput)
	if err != nil {
		return "", err
	}

	if volumeId == "" {
		return "", &VolumeIdNotFoundForMultipathDeviceNameError{mpathDeviceName}
	}

	if _, err = r.Helper.IsMpathMatchVolumeId(dmPath, volumeUids); err != nil {
		return "", err
	}
	return volumeId, nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevices(lunId int, arrayIdentifiers []string) error {
	logger.Debugf("Rescan : Start rescan on specific lun, on lun : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	var hostIDs = make(map[int]bool)
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
		for _, hostId := range hostsId {
			hostIDs[hostId] = true
		}
	}
	if len(hostIDs) == 0 && len(errStrings) != 0 {
		err := errors.New(strings.Join(errStrings, ","))
		return err
	}
	for hostNumber := range hostIDs {

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

func (r OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(volumeId string) (string, error) {
	logger.Infof("GetMpathDevice: Searching multipath devices for volume : [%s] ", volumeId)

	volumeIds := r.GetVolumeIdVariations(volumeId)
	dmPath, _ := r.Helper.GetDmsPath(volumeIds)

	if dmPath != "" {
		if r.Helper.IsMpathMatchVolumeIdWithoutErrors(dmPath, volumeIds) {
			return dmPath, nil
		}
	}

	if err := r.Helper.ReloadMultipath(); err != nil {
		return "", err
	}

	dmPath, err := r.Helper.GetDmsPath(volumeIds)

	if err != nil {
		return "", err
	}

	if dmPath == "" {
		return "", &MultipathDeviceNotFoundForVolumeError{volumeId}
	}
	_, err = r.Helper.IsMpathMatchVolumeId(dmPath, volumeIds)
	if err != nil {
		return "", err
	}
	return dmPath, nil
}

func (OsDeviceConnectivityHelperScsiGeneric) GetVolumeIdVariations(volumeUuid string) []string {
	volumeUuidLower := strings.ToLower(volumeUuid)
	volumeNguid := convertScsiIdToNguid(volumeUuidLower)
	return []string{volumeUuidLower, volumeNguid}
}

func (r OsDeviceConnectivityHelperScsiGeneric) flushDeviceBuffers(deviceName string) error {
	devicePath := filepath.Join(DevPath, deviceName)
	_, err := r.Executer.ExecuteWithTimeoutSilently(TimeOutBlockDevCmd, blockDevCmd, []string{flushBufsFlag, devicePath})
	if err != nil {
		logger.Errorf("%v %v {%v} did not succeed to flush the device buffers. err={%v}", blockDevCmd, flushBufsFlag, devicePath,
			err.Error())
		return err
	}
	return nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) flushDevicesBuffers(deviceNames []string) error {
	logger.Debugf("executing commands : {%v %v} on devices : {%v} and timeout : {%v} mseconds", blockDevCmd, flushBufsFlag, deviceNames, TimeOutBlockDevCmd)
	for _, deviceName := range deviceNames {
		err := r.flushDeviceBuffers(deviceName)
		if err != nil {
			return err
		}
	}
	logger.Debugf("Finished executing commands: {%v %v}", blockDevCmd, flushBufsFlag)
	return nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) FlushMultipathDevice(mpathDevice string) error {
	err := r.flushDeviceBuffers(mpathDevice)
	if err != nil {
		return err
	}

	// mpathdevice is dm-4 for example
	logger.Debugf("Flushing mpath device : {%v}", mpathDevice)

	fullDevice := filepath.Join(DevPath, mpathDevice)

	logger.Debugf("Try to acquire lock for running the command multipath -f {%v} (to avoid concurrent multipath commands)", mpathDevice)
	r.MutexMultipathF.Lock()
	logger.Debugf("Acquired lock for multipath -f command")
	_, err = r.Executer.ExecuteWithTimeout(TimeOutMultipathCmd, multipathCmd, []string{"-f", fullDevice})
	r.MutexMultipathF.Unlock()

	if err != nil {
		if _, e := os.Stat(fullDevice); os.IsNotExist(e) {
			logger.Debugf("Mpath device {%v} was deleted", fullDevice)
		} else {
			logger.Errorf("multipath -f {%v} did not succeed to delete the device. err={%v}", fullDevice, err.Error())
			return err
		}
	}

	logger.Debugf("Finished flushing mpath device : {%v}", mpathDevice)
	return nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(sysDevices []string) error {
	flushErr := r.flushDevicesBuffers(sysDevices)
	if flushErr != nil {
		return flushErr
	}

	// sysDevices  = sdb, sda,...
	logger.Debugf(`Removing scsi device : {%v} by writing "1" to the delete file of each device: {%v}`, sysDevices, fmt.Sprintf(sysDeviceDeletePathFormat, "<deviceName>"))
	// NOTE: this func could be also relevant for SCSI (not only for iSCSI)
	var (
		f   *os.File
		err error
	)

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}

		filename := fmt.Sprintf(sysDeviceDeletePathFormat, deviceName)

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
	logger.Debugf("Finished removing SCSI devices : {%v}", sysDevices)
	return nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) ValidateLun(lun int, sysDevices []string) error {
	logger.Debugf("Validating lun {%v} on devices: {%v}", lun, sysDevices)
	for _, sysDevice := range sysDevices {
		sysDeviceParts := strings.Split(sysDevice, "/")
		device := sysDeviceParts[len(sysDeviceParts)-1]

		symLinkPath := fmt.Sprintf(sysDeviceSymLinkFormat, device)
		destinationPath, err := filepath.EvalSymlinks(symLinkPath)
		if err != nil {
			return err
		}

		if !strings.HasSuffix(destinationPath, strconv.Itoa(lun)) {
			return fmt.Errorf("lun not valid, storage lun: %v, linkedPath: %v to device: %v", lun, destinationPath, device)
		}
	}
	logger.Debugf("Finished lun validation")
	return nil

}

// ============== OsDeviceConnectivityHelperInterface ==========================

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperInterface

type OsDeviceConnectivityHelperInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityScsiGeneric.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test OsDeviceConnectivityHelperGeneric logic.
	*/
	GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error)
	GetDmsPath(identifiers []string) (string, error)
	GetWwnByScsiInq(dev string) (string, error)
	IsMpathMatchVolumeId(dmPath string, identifiers []string) (bool, error)
	IsMpathMatchVolumeIdWithoutErrors(dmPath string, identifiers []string) bool
	ReloadMultipath() error
	GetMpathOutputByVolumeId(identifiers []string) (string, error)
	GetVolumeIdByMpathName(mpathDeviceName string, mpathdOutput string) (string, error)
	GetMpathDeviceName(volumePath string) (string, error)
}

type OsDeviceConnectivityHelperGeneric struct {
	Executer executer.ExecuterInterface
	Helper   GetDmsPathHelperInterface
}

func NewOsDeviceConnectivityHelperGeneric(executer executer.ExecuterInterface) OsDeviceConnectivityHelperInterface {
	return &OsDeviceConnectivityHelperGeneric{
		Executer: executer,
		Helper:   NewGetDmsPathHelperGeneric(executer),
	}
}

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
		targetFilePath = FcHostSysfsPath
		regexpValue = "rport-([0-9]+)"
	}

	var HostIDs []int
	matches, err := o.Executer.FilepathGlob(targetFilePath)
	if err != nil {
		logger.Errorf("Error while Glob targetFilePath : {%v}. err : {%v}", targetFilePath, err)
		return nil, err
	}

	logger.Debugf("{%v} targetname files matches were found", len(matches))

	re := regexp.MustCompile(regexpValue)
	logger.Debugf("Check if any match is relevant for storage target (%s)", arrayIdentifier)
	for _, targetPath := range matches {
		targetName, err := o.Executer.IoutilReadFile(targetPath)
		if err != nil {
			logger.Warningf("Could not read target name from file : {%v}, error : {%v}", targetPath, err)
			continue
		}
		identifierFromHost := strings.TrimSpace(string(targetName))
		//For FC WWNs from the host, the value will like this: 0x500507680b26c0aa, but the arrayIdentifier doesn't have this prefix
		if strings.HasPrefix(identifierFromHost, "0x") {
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
			logger.Debugf("portState path (%s) was found. Adding host ID {%v} to the id list", targetPath, hostNumber)
		}
	}

	if len(HostIDs) == 0 {
		return []int{}, &ConnectivityIdentifierStorageTargetNotFoundError{StorageTargetName: arrayIdentifier, DirectoryPath: targetFilePath}
	}

	return HostIDs, nil

}

func (o OsDeviceConnectivityHelperGeneric) IsMpathMatchVolumeId(dmPath string, identifiers []string) (bool, error) {
	sgInqWwn, err := o.GetWwnByScsiInq(dmPath)
	if err != nil {
		return false, err
	}
	if isSameId, _ := o.isSameId(sgInqWwn, identifiers); isSameId == true {
		return true, nil
	}
	return false, &ErrorWrongDeviceFound{dmPath, identifiers[0], sgInqWwn}
}

func (o OsDeviceConnectivityHelperGeneric) IsMpathMatchVolumeIdWithoutErrors(dmPath string, identifiers []string) bool {
	sgInqWwn, _ := o.GetWwnByScsiInq(dmPath)
	isSameId, _ := o.isSameId(sgInqWwn, identifiers)
	return isSameId
}

func (o OsDeviceConnectivityHelperGeneric) isSameId(wwn string, identifiers []string) (bool, error) {
	for _, identifier := range identifiers {
		if wwn == identifier {
			return true, nil
		}
	}
	return false, nil
}

func (o OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(dev string) (string, error) {
	/* scsi inq example
	$> sg_inq -p 0x83 /dev/mapper/mpathhe
		VPD INQUIRY: Device Identification page
		  Designation descriptor number 1, descriptor length: 20
			designator_type: NAA,  code_set: Binary
			associated with the addressed logical unit
			  NAA 6, IEEE Company_id: 0x1738
			  Vendor Specific Identifier: 0xcfc9035eb
			  Vendor Specific Identifier Extension: 0xcea5f6
			  [0x6001738cfc9035eb0000000000ceaaaa]
		  Designation descriptor number 2, descriptor length: 52
			designator_type: T10 vendor identification,  code_set: ASCII
			associated with the addressed logical unit
			  vendor id: IBM
			  vendor specific: 2810XIV          60035EB0000000000CEAAAA
		  Designation descriptor number 3, descriptor length: 43
			designator_type: vendor specific [0x0],  code_set: ASCII
			associated with the addressed logical unit
			  vendor specific: vol=u_k8s_longevity_ibm-ubiquity-db
		  Designation descriptor number 4, descriptor length: 37
			designator_type: vendor specific [0x0],  code_set: ASCII
			associated with the addressed logical unit
			  vendor specific: host=k8s-acceptance-v18-node1
		  Designation descriptor number 5, descriptor length: 8
			designator_type: Target port group,  code_set: Binary
			associated with the target port
			  Target port group: 0x0
		  Designation descriptor number 6, descriptor length: 8
			designator_type: Relative target port,  code_set: Binary
			associated with the target port
			  Relative target port: 0xd22
	*/
	sgInqCmd := "sg_inq"

	if err := o.Executer.IsExecutable(sgInqCmd); err != nil {
		return "", err
	}

	args := []string{"-p", "0x83", dev}
	// add timeout in case the call never comes back.
	logger.Debugf("Calling [%s] with timeout", sgInqCmd)
	outputBytes, err := o.Executer.ExecuteWithTimeout(TimeOutSgInqCmd, sgInqCmd, args)
	if err != nil {
		return "", err
	}
	wwnRegex := "(?i)" + `\[0x(.*?)\]`
	wwnRegexCompiled, err := regexp.Compile(wwnRegex)

	if err != nil {
		return "", err
	}
	/*
	   sg_inq on device NAA6 returns "Vendor Specific Identifier Extension"
	   sg_inq on device EUI-64 returns "Vendor Specific Extension Identifier".
	*/
	pattern := "(?i)" + "Vendor Specific (Identifier Extension|Extension Identifier):"
	scanner := bufio.NewScanner(strings.NewReader(string(outputBytes[:])))
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	wwn := ""
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if found {
			matches := wwnRegexCompiled.FindStringSubmatch(line)
			if len(matches) != 2 {
				logger.Debugf("wrong line, too many matches in sg_inq output : %#v", matches)
				return "", &ErrorNoRegexWwnMatchInScsiInq{dev, line}
			}
			wwn = matches[1]
			logger.Debugf("Found the expected Wwn [%s] in sg_inq", wwn)
			return strings.ToLower(wwn), nil
		}
		if regex.MatchString(line) {
			found = true
			// its one line after "Vendor Specific Identifier Extension:" line which should contain the WWN
			continue
		}

	}
	return "", &MultipathDeviceNotFoundForVolumeError{wwn}
}

func (o OsDeviceConnectivityHelperGeneric) ReloadMultipath() error {
	logger.Infof("ReloadMultipath: reload start")
	if err := o.Executer.IsExecutable(multipathCmd); err != nil {
		return err
	}

	args := []string{}
	_, err := o.Executer.ExecuteWithTimeout(TimeOutMultipathCmd, multipathCmd, args)
	if err != nil {
		return err
	}

	args = []string{"-r"}
	_, err = o.Executer.ExecuteWithTimeout(TimeOutMultipathCmd, multipathCmd, args)
	if err != nil {
		return err
	}
	logger.Infof("ReloadMultipath: reload finished successfully")
	return nil
}

func (o OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(volumePath string) (string, error) {
	procMountsFileContent, err := ioutil.ReadFile(procMountsFilePath)
	if err != nil {
		return "", err
	}
	procMounts := string(procMountsFileContent)

	return o.Helper.GetMpathDeviceNameFromProcMounts(procMounts, volumePath)
}

func (o OsDeviceConnectivityHelperGeneric) GetMpathOutputByVolumeId(identifiers []string) (string, error) {
	mpathdOutput, err := o.Helper.GetMpathdOutput(identifiers, multipathdWildcardsMpathNameAndVolumeId)
	if err != nil {
		return "", err
	}
	return mpathdOutput, nil
}

func (o OsDeviceConnectivityHelperGeneric) GetVolumeIdByMpathName(mpathDeviceName string, mpathdOutput string) (string, error) {
	identifiers := []string{mpathDeviceName}
	volumeIds := o.Helper.ParseFieldValuesOfIdentifiers(identifiers, mpathdOutput)
	if len(volumeIds) > 1 {
		return "", &MultipleVolumeIdsError{volumeIds, mpathDeviceName}
	}

	var volumeId string
	for volumeId = range volumeIds {
		break // because its a single value in the map(1 mpath device, if not it should fail above), so just take the first
	}

	return volumeId, nil
}

func (o OsDeviceConnectivityHelperGeneric) GetDmsPath(identifiers []string) (string, error) {
	mpathdOutput, err := o.Helper.GetMpathdOutput(identifiers, MultipathdWildcardsMpathAndVolumeId)
	if err != nil {
		return "", err
	}
	dms := o.Helper.ParseFieldValuesOfIdentifiers(identifiers, mpathdOutput)
	return o.Helper.GetFullDmPath(dms, identifiers[0])
}

//go:generate mockgen -destination=../../../mocks/mock_GetDmsPathHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity GetDmsPathHelperInterface

type GetDmsPathHelperInterface interface {
	WaitForDmToExist(identifiers []string, maxRetries int, intervalSeconds int, multipathdCommandFormatArgs []string) (string, error)
	GetMpathdOutput(identifiers []string, multipathdCommandFormatArgs []string) (string, error)
	ParseFieldValuesOfIdentifiers(identifiers []string, mpathdOutput string) map[string]bool
	GetFullDmPath(dms map[string]bool, volumeId string) (string, error)
	IsThisMatchedDmObject(identifiers []string, dmObject string) bool
	GetMpathDeviceNameFromProcMounts(procMounts string, volumePath string) (string, error)
}

type GetDmsPathHelperGeneric struct {
	executer executer.ExecuterInterface
}

func NewGetDmsPathHelperGeneric(executer executer.ExecuterInterface) GetDmsPathHelperInterface {
	return &GetDmsPathHelperGeneric{executer: executer}
}

func convertScsiIdToNguid(scsiId string) string {
	logger.Infof("Converting scsi uuid : %s to nguid", scsiId)
	oui := scsiId[1:WwnOuiEnd]
	vendorIdentifier := scsiId[WwnOuiEnd:WwnVendorIdentifierEnd]
	vendorIdentifierExtension := scsiId[WwnVendorIdentifierEnd:]
	finalNguid := vendorIdentifierExtension + oui + "0" + vendorIdentifier
	logger.Infof("Nguid is : %s", finalNguid)
	return finalNguid
}

func (o GetDmsPathHelperGeneric) GetMpathdOutput(identifiers []string,
	multipathdCommandFormatArgs []string) (string, error) {
	mpathdOutput, err := o.WaitForDmToExist(identifiers, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec, multipathdCommandFormatArgs)

	if err != nil {
		return "", err
	}

	if mpathdOutput == "" {
		return "", &MultipathDeviceNotFoundForVolumeError{identifiers[0]}
	}
	return mpathdOutput, nil
}

func (o GetDmsPathHelperGeneric) WaitForDmToExist(identifiers []string, maxRetries int, intervalSeconds int,
	multipathdCommandFormatArgs []string) (string, error) {
	formatTemplate := strings.Join(multipathdCommandFormatArgs, mpathdSeparator)
	args := []string{"show", "maps", "raw", "format", "\"", formatTemplate, "\""}
	var err error
	logger.Debugf("Waiting for dm to exist")
	for i := 0; i < maxRetries; i++ {
		err = nil
		out, err := o.executer.ExecuteWithTimeout(TimeOutMultipathdCmd, multipathdCmd, args)
		if err != nil {
			return "", err
		}
		dms := string(out)
		for _, identifier := range identifiers {
			if strings.Contains(dms, identifier) {
				return dms, nil
			}
		}
		err = os.ErrNotExist

		time.Sleep(time.Second * time.Duration(intervalSeconds))
	}
	return "", err
}

func (o GetDmsPathHelperGeneric) ParseFieldValuesOfIdentifiers(identifiers []string, mpathdOutput string) map[string]bool {
	dmFieldValues := make(map[string]bool)

	scanner := bufio.NewScanner(strings.NewReader(mpathdOutput))
	for scanner.Scan() {
		identifier, dmObject := o.getLineParts(scanner)
		if o.IsThisMatchedDmObject(identifiers, identifier) {
			dmFieldValues[dmObject] = true
			logger.Infof("ParseFieldValuesOfIdentifiers: DM Object found: %s for DM Object %s", dmObject, identifier)
		}
	}

	return dmFieldValues
}

func (GetDmsPathHelperGeneric) getLineParts(scanner *bufio.Scanner) (string, string) {
	deviceLine := scanner.Text()
	lineParts := strings.Split(deviceLine, mpathdSeparator)
	return lineParts[0], lineParts[1]
}

func (o GetDmsPathHelperGeneric) IsThisMatchedDmObject(identifiers []string, dmObject string) bool {
	for _, identifier := range identifiers {
		if strings.Contains(dmObject, identifier) {
			return true
		}
	}
	return false
}

func (GetDmsPathHelperGeneric) GetFullDmPath(dms map[string]bool, volumeId string) (string, error) {
	if len(dms) > 1 {
		return "", &MultipleDmDevicesError{volumeId, dms}
	}

	var dm string
	for dm = range dms {
		break // because its a single value in the map(1 mpath device, if not it should fail above), so just take the first
	}

	dmPath := filepath.Join(DevPath, filepath.Base(strings.TrimSpace(dm)))
	return dmPath, nil
}

func (GetDmsPathHelperGeneric) GetMpathDeviceNameFromProcMounts(procMounts string, volumePath string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(procMounts))
	for scanner.Scan() {
		mountLine := scanner.Text()
		if strings.Contains(mountLine, volumePath) {
			return getMpathDeviceNameFromMountLine(mountLine), nil
		}
	}
	return "", &MultipathDeviceNotFoundForVolumePathError{volumePath}
}

func getMpathDeviceNameFromMountLine(mountLine string) string {
	lineParts := strings.Fields(mountLine)
	fullMpathDeviceName := lineParts[0]
	splitedFullMpathDeviceName := strings.Split(fullMpathDeviceName, "/")
	return splitedFullMpathDeviceName[len(splitedFullMpathDeviceName)-1]
}

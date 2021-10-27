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
	TimeOutMultipathCmd  = 60 * 1000
	TimeOutMultipathdCmd = 10 * 1000
)

const (
	DevPath                     = "/dev"
	ConnectionTypeISCSI         = "iscsi"
	ConnectionTypeFC            = "fc"
	ConnectionTypeNVMEoFC       = "nvmeof"
	WaitForMpathRetries         = 5
	WaitForMpathWaitIntervalSec = 1
	FC_HOST_SYSFS_PATH          = "/sys/class/fc_remote_ports/rport-*/port_name"
	IscsiHostRexExPath          = "/sys/class/iscsi_host/host*/device/session*/iscsi_session/session*/targetname"
	MpathdSeparator             = ","
	multipathdCmd               = "multipathd"
	multipathCmd                = "multipath"
	VolumeIdDelimiter           = ":"
	VolumeStorageIdsDelimiter   = ";"
	WwnOuiEnd                   = 7
	WwnVendorIdentifierEnd      = 16
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
func getVolumeUuid(volumeId string) string {
	volumeIdParts := strings.Split(volumeId, VolumeIdDelimiter)
	idsPart := volumeIdParts[len(volumeIdParts)-1]
	splittedIdsPart := strings.Split(idsPart, VolumeStorageIdsDelimiter)
	if len(splittedIdsPart) == 2 {
		return splittedIdsPart[1]
	} else {
		return splittedIdsPart[0]
	}
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(volumeId string) (string, error) {
	logger.Infof("GetMpathDevice: Searching multipath devices for volume : [%s] ", volumeId)

	volumeUuid := getVolumeUuid(volumeId)

	volumeUuidLower := strings.ToLower(volumeUuid)
	volumeNguid := ConvertScsiIdToNguid(volumeUuidLower)
	dmPath, _ := r.Helper.GetDmsPath(volumeUuidLower, volumeNguid)

	if dmPath != "" {
		SgInqWwn, _ := r.Helper.GetWwnByScsiInq(dmPath)
		SgInqWwnLower := strings.ToLower(SgInqWwn)
		if SgInqWwnLower == volumeUuidLower || SgInqWwnLower == volumeNguid {
			return dmPath, nil
		}
	}

	if err := r.Helper.ReloadMultipath(); err != nil {
		return "", err
	}

	dmPath, err := r.Helper.GetDmsPath(volumeUuidLower, volumeNguid)

	if err != nil {
		return "", err
	}

	if dmPath == "" {
		return "", &MultipathDeviceNotFoundForVolumeError{volumeId}
	}

	SgInqWwn, err := r.Helper.GetWwnByScsiInq(dmPath)
	if err != nil {
		return "", err
	}
	SgInqWwnLower := strings.ToLower(SgInqWwn)
	if SgInqWwnLower != volumeUuidLower && SgInqWwnLower != volumeNguid {
		// To make sure we found the right WWN, if not raise error instead of using wrong mpath
		return "", &ErrorWrongDeviceFound{dmPath, volumeUuidLower, SgInqWwn}
	}
	return dmPath, nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) FlushMultipathDevice(mpathDevice string) error {
	// mpathdevice is dm-4 for example
	logger.Debugf("Flushing mpath device : {%v}", mpathDevice)

	fullDevice := filepath.Join(DevPath, mpathDevice)

	logger.Debugf("Try to acquire lock for running the command multipath -f {%v} (to avoid concurrent multipath commands)", mpathDevice)
	r.MutexMultipathF.Lock()
	logger.Debugf("Acquired lock for multipath -f command")
	_, err := r.Executer.ExecuteWithTimeout(TimeOutMultipathCmd, "multipath", []string{"-f", fullDevice})
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
	GetHostsIdByArrayIdentifier(arrayIdentifier string) ([]int, error)
	GetDmsPath(volumeId string, volumeNguid string) (string, error)
	GetWwnByScsiInq(dev string) (string, error)
	ReloadMultipath() error
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
		targetFilePath = FC_HOST_SYSFS_PATH
		regexpValue = "rport-([0-9]+)"
	}

	var HostIDs []int
	matches, err := o.Executer.FilepathGlob(targetFilePath)
	if err != nil {
		logger.Errorf("Error while Glob targetFilePath : {%v}. err : {%v}", targetFilePath, err)
		return nil, err
	}

	logger.Debugf("targetname files matches were found : {%v}", matches)

	re := regexp.MustCompile(regexpValue)
	for _, targetPath := range matches {
		logger.Debugf("Check if targetname path (%s) is relevant for storage target (%s).", targetPath, arrayIdentifier)
		targetName, err := o.Executer.IoutilReadFile(targetPath)
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
	outputBytes, err := o.Executer.ExecuteWithTimeout(3000, sgInqCmd, args)
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
			logger.Debugf("Found the expected Wwn [%s] in sg_inq.", wwn)
			return wwn, nil
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
func (o OsDeviceConnectivityHelperGeneric) GetDmsPath(volumeId string, volumeNguid string) (string, error) {
	volumeUuidLower := strings.ToLower(volumeId)

	mpathdOutput, err := o.Helper.WaitForDmToExist(volumeUuidLower, volumeNguid, WaitForMpathRetries, WaitForMpathWaitIntervalSec)

	if err != nil {
		return "", err
	}

	if mpathdOutput == "" {
		return "", &MultipathDeviceNotFoundForVolumeError{volumeId}
	}

	dms := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(mpathdOutput))
	for scanner.Scan() {
		deviceLine := scanner.Text()
		lineParts := strings.Split(deviceLine, MpathdSeparator)
		dm, uuid := lineParts[0], lineParts[1]
		if strings.Contains(uuid, volumeUuidLower) || strings.Contains(uuid, volumeNguid) {
			dmPath := filepath.Join(DevPath, filepath.Base(strings.TrimSpace(dm)))
			dms[dmPath] = true
			logger.Infof("GetMpathDevice: DM found: %s for volume %s", dmPath, uuid)
		}
	}

	if len(dms) > 1 {
		return "", &MultipleDmDevicesError{volumeId, dms}
	}

	var dm string
	for dm = range dms {
		break // because its a single value in the map(1 mpath device, if not it should fail above), so just take the first
	}

	return dm, nil
}

//go:generate mockgen -destination=../../../mocks/mock_GetDmsPathHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity GetDmsPathHelperInterface

type GetDmsPathHelperInterface interface {
	WaitForDmToExist(volumeUuid string, volumeNguid string, maxRetries int, intervalSeconds int) (string, error)
}

type GetDmsPathHelperGeneric struct {
	executer executer.ExecuterInterface
}

func NewGetDmsPathHelperGeneric(executer executer.ExecuterInterface) GetDmsPathHelperInterface {
	return &GetDmsPathHelperGeneric{executer: executer}
}

func ConvertScsiIdToNguid(scsiId string) string {
	logger.Infof("Converting scsi uuid : %s to nguid", scsiId)
	oui := scsiId[1:WwnOuiEnd]
	vendorIdentifier := scsiId[WwnOuiEnd:WwnVendorIdentifierEnd]
	vendorIdentifierExtension := scsiId[WwnVendorIdentifierEnd:]
	finalNguid := vendorIdentifierExtension + oui + "0" + vendorIdentifier
	logger.Infof("Nguid is : %s", finalNguid)
	return finalNguid
}

func (o GetDmsPathHelperGeneric) WaitForDmToExist(volumeUuid string, volumeNguid string, maxRetries int, intervalSeconds int) (string, error) {
	formatTemplate := strings.Join([]string{"%d", "%w"}, MpathdSeparator)
	args := []string{"show", "maps", "raw", "format", "\"", formatTemplate, "\""}
	var err error
	for i := 0; i < maxRetries; i++ {
		err = nil
		out, err := o.executer.ExecuteWithTimeout(TimeOutMultipathdCmd, multipathdCmd, args)
		if err != nil {
			return "", err
		}
		dms := string(out)
		if !strings.Contains(dms, volumeUuid) && !strings.Contains(dms, volumeNguid) {
			err = os.ErrNotExist
		} else {
			return dms, nil
		}

		time.Sleep(time.Second * time.Duration(intervalSeconds))
	}
	return "", err
}

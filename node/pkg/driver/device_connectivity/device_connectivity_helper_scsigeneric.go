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
	RemoveGhostDevice() error
	ValidateLun(lun int, sysDevices []string) error
	IsVolumePathMatchesVolumeId(volumeId string, volumePath string) (bool, error)
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
	MultipathdWildcardsVolumeIdAndMpath     = []string{"%w", "%d"}
	MultipathdWildcardsMpathNameAndVolumeId = []string{"%n", "%w"}
	multipathdWildcardsMpathAndVolumeId     = []string{"%d", "%w"}
)

const (
	DevPath                     = "/dev"
	DevMapperPath               = "/dev/mapper"
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

func (r OsDeviceConnectivityHelperScsiGeneric) IsVolumePathMatchesVolumeId(volumeUuid string, volumePath string) (bool, error) {
	logger.Infof("IsVolumePathMatchesVolumeId: Searching matching volume id for volume path: [%s] ", volumePath)
	volumeIdVariations := r.Helper.GetVolumeIdVariations(volumeUuid)

	mpathDeviceName, err := r.Helper.GetMpathDeviceName(volumePath)
	if err != nil {
		return false, err
	}

	dmDirectory := DevPath
	multipathdCommandFormatArgs := multipathdWildcardsMpathAndVolumeId
	if r.Helper.IsDmName(mpathDeviceName) {
		dmDirectory = DevMapperPath
		multipathdCommandFormatArgs = MultipathdWildcardsMpathNameAndVolumeId
	}

	mpathdOutput, err := r.Helper.GetMpathdOutputForVolume(volumeIdVariations, multipathdCommandFormatArgs)
	if err != nil {
		return false, err
	}

	mpathVolumeId, err := r.Helper.GetMpathVolumeId(mpathdOutput, mpathDeviceName, dmDirectory)
	if err != nil {
		return false, err
	}

	logger.Infof("IsVolumePathMatchesVolumeId: found volume id [%s] for volume path [%s] ", mpathVolumeId, volumePath)
	return r.Helper.IsAnyVariationInMpathVolumeId(mpathVolumeId, volumeIdVariations), nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevices(lunId int, arrayIdentifiers []string) error {
	logger.Debugf("Rescan : Start rescan on specific lun, on lun : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	var hostIDs = make(map[int]bool)
	var errStrings []string
	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		logger.Errorf("%s", e.Error())
		return e
	}

	for _, arrayIdentifier := range arrayIdentifiers {
		hostsId, e := r.Helper.GetHostsIdByArrayIdentifier(arrayIdentifier)
		if e != nil {
			logger.Errorf("%s", e.Error())
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
			logger.Errorf("%s", e.Error())
			return e
		}

	}

	logger.Debugf("Rescan : finish rescan lun on lun id : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	return nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) GetMpathDevice(volumeId string) (string, error) {
	logger.Infof("GetMpathDevice: Searching multipath devices for volume : [%s] ", volumeId)

	volumeIdVariations := r.Helper.GetVolumeIdVariations(volumeId)
	dmPath, _ := r.Helper.GetDmsPath(volumeIdVariations)

	if dmPath != "" {
		SgInqWwn, _ := r.Helper.GetWwnByScsiInq(dmPath)
		if isSameId(SgInqWwn, volumeIdVariations) {
			return dmPath, nil
		}
		logger.Warningf("Expected {%v} but got {%v} from sg_inq", volumeId, SgInqWwn)
	}

	if err := r.Helper.ReloadMultipath(); err != nil {
		return "", err
	}

	dmPath, err := r.Helper.GetDmsPath(volumeIdVariations)

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
	if isSameId(SgInqWwn, volumeIdVariations) {
		return dmPath, nil
	}
	// To make sure we found the right WWN, if not raise error instead of using wrong mpath
	return "", &ErrorWrongDeviceFound{dmPath, volumeIdVariations[0], SgInqWwn}
}

func isSameId(wwn string, volumeIdVariations []string) bool {
	wwn = strings.ToLower(wwn)
	for _, volumeIdVariation := range volumeIdVariations {
		if wwn == volumeIdVariation {
			return true
		}
	}
	return false
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

func (o OsDeviceConnectivityHelperScsiGeneric) RemoveGhostDevice() error {
	// get all devices from sg_map
	sgMapCmd := "sg_map"
	args := []string{"-x"}

	out, err := o.Executer.ExecuteWithTimeout(TimeOutSgInqCmd, sgMapCmd, args)
	if err != nil {
		logger.Errorf("Error getting sg devices from sg_map. error: {%v}", err.Error())
		return err
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()

		// Skip lines that contain /dev/sd
		if strings.Contains(line, "/dev/sd") || strings.Contains(line, "/dev/sr") {
			continue
		}

		// Extract first field (e.g., /dev/sg3)
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		sgdev := fields[0]

		// Step 3: Run sg_inq and check for PQual=1 and IBM
		if isGhostIBMDevice(o, sgdev) {
			sgBase := filepath.Base(sgdev)
			deletePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/delete", sgBase)

			logger.Debugf("Deleting ghost device: %s\n", sgdev)

			// Step 4: Write "1" to delete path
			if err := os.WriteFile(deletePath, []byte("1"), 0644); err != nil {
				logger.Errorf("Error deleting device %s: %v\n", sgdev, err)
				return err // I actualy think it's better to continue and delete what we can
			}
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Errorf("Error reading sg_map output: %v\n", err)
	}
	return err
}

func isGhostIBMDevice(o OsDeviceConnectivityHelperScsiGeneric, sgdev string) bool {
	// Run sg_inq sgdev and check if both PQual=1 and IBM are present
	sgInqCmd := "sg_inq"

	if err := o.Executer.IsExecutable(sgInqCmd); err != nil {
		return false
	}

	args := []string{sgdev}
	// add timeout in case the call never comes back.
	logger.Debugf("Calling [%s] with timeout", sgInqCmd)
	outputBytes, err := o.Executer.ExecuteWithTimeout(TimeOutSgInqCmd, sgInqCmd, args)
	if err != nil {
		return false
	}
	outStr := string(outputBytes)
	/*-------------------------------------
	output, err := exec.Command("sg_inq", sgdev).Output() //shlomit: change this to ExecuteWithTimeout
	if err != nil {
		// Fail silently like Bash (2>/dev/null)
		return false
	}
	outStr := string(output)
	*/
	return strings.Contains(outStr, "PQual=1") && strings.Contains(outStr, "IBM")
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
	GetDmsPath(volumeIdVariations []string) (string, error)
	GetWwnByScsiInq(dev string) (string, error)
	ReloadMultipath() error
	GetMpathdOutputForVolume(volumeIdVariations []string, multipathdCommandFormatArgs []string) (string, error)
	GetVolumeIdVariations(volumeUuid string) []string
	GetMpathDeviceName(volumePath string) (string, error)
	IsAnyVariationInMpathVolumeId(mpathVolumeId string, volumeIdVariations []string) bool
	GetMpathVolumeId(mpathdOutput string, mpathDeviceName string, dmDirectory string) (string, error)
	IsDmName(mpathDeviceName string) bool
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

func (o OsDeviceConnectivityHelperGeneric) GetMpathVolumeId(mpathdOutput string, mpathDeviceName string,
	dmDirectory string) (string, error) {

	mpathVolumeId, err := o.Helper.ExtractVolumeId(mpathDeviceName, mpathdOutput)
	if err != nil {
		return "", err
	}
	dmPath := filepath.Join(dmDirectory, filepath.Base(strings.TrimSpace(mpathDeviceName)))

	SgInqWwn, err := o.GetWwnByScsiInq(dmPath)
	if err != nil {
		return "", err
	}
	if o.IsAnyVariationInMpathVolumeId(mpathVolumeId, []string{SgInqWwn}) {
		return mpathVolumeId, nil
	}
	return "", &ErrorWrongDeviceFound{dmPath, mpathVolumeId, SgInqWwn}
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
			logger.Debugf("Found wwn [%s] in sg_inq", wwn)
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

func (o OsDeviceConnectivityHelperGeneric) GetMpathdOutputForVolume(volumeIdVariations []string,
	multipathdCommandFormatArgs []string) (string, error) {
	mpathdOutput, err := o.Helper.WaitForDmToExist(volumeIdVariations, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec, multipathdCommandFormatArgs)
	if err != nil {
		return "", err
	}
	return mpathdOutput, nil
}

func (o OsDeviceConnectivityHelperGeneric) GetDmsPath(volumeIdVariations []string) (string, error) {
	mpathdOutput, err := o.Helper.WaitForDmToExist(volumeIdVariations, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec, MultipathdWildcardsVolumeIdAndMpath)
	if err != nil {
		return "", err
	}
	dms := o.Helper.ExtractDmFieldValues(volumeIdVariations, mpathdOutput)
	return o.Helper.GetFullDmPath(dms, volumeIdVariations[0])
}

func (OsDeviceConnectivityHelperGeneric) GetVolumeIdVariations(volumeUuid string) []string {
	volumeUuidLower := strings.ToLower(volumeUuid)
	volumeNguid := convertScsiIdToNguid(volumeUuidLower)
	return []string{volumeUuidLower, volumeNguid}
}

func (o OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(volumePath string) (string, error) {
	procMountsFileContent, err := ioutil.ReadFile(procMountsFilePath)
	if err != nil {
		return "", err
	}
	procMounts := string(procMountsFileContent)

	return o.Helper.GetMpathDeviceNameFromProcMounts(procMounts, volumePath)
}

func (o OsDeviceConnectivityHelperGeneric) IsAnyVariationInMpathVolumeId(mpathVolumeId string, volumeIdVariations []string) bool {
	for _, volumeIdVariation := range volumeIdVariations {
		if strings.Contains(mpathVolumeId, volumeIdVariation) {
			return true
		}
	}
	return false
}

func (o OsDeviceConnectivityHelperGeneric) IsDmName(mpathDeviceName string) bool {
	return strings.Contains(mpathDeviceName, "mpath")
}

//go:generate mockgen -destination=../../../mocks/mock_GetDmsPathHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity GetDmsPathHelperInterface

type GetDmsPathHelperInterface interface {
	WaitForDmToExist(volumeIdVariations []string, maxRetries int, intervalSeconds int, multipathdCommandFormatArgs []string) (string, error)
	ExtractDmFieldValues(dmFilterValues []string, mpathdOutput string) map[string]bool
	GetFullDmPath(dms map[string]bool, volumeId string) (string, error)
	IsIndicatorMatchesFilterValues(dmFilterValues []string, dmFieldValue string) bool
	GetMpathDeviceNameFromProcMounts(procMounts string, volumePath string) (string, error)
	ExtractVolumeId(mpathDeviceName string, mpathdOutput string) (string, error)
}

type GetDmsPathHelperGeneric struct {
	executer executer.ExecuterInterface
}

func NewGetDmsPathHelperGeneric(executer executer.ExecuterInterface) GetDmsPathHelperInterface {
	return &GetDmsPathHelperGeneric{executer: executer}
}

func (o GetDmsPathHelperGeneric) ExtractVolumeId(mpathDeviceName string, mpathdOutput string) (string, error) {
	mpathDeviceNames := []string{mpathDeviceName}
	volumeIds := o.ExtractDmFieldValues(mpathDeviceNames, mpathdOutput)
	volumeId, err := getUniqueDmFieldValue(volumeIds, mpathDeviceName)
	if err != nil {
		return "", err
	}

	if volumeId == "" {
		return "", &VolumeIdNotFoundForMultipathDeviceNameError{mpathDeviceName}
	}

	return volumeId, nil
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

func (o GetDmsPathHelperGeneric) WaitForDmToExist(volumeIdVariations []string, maxRetries int, intervalSeconds int,
	multipathdCommandFormatArgs []string) (string, error) {
	formatTemplate := strings.Join(multipathdCommandFormatArgs, mpathdSeparator)
	args := []string{"show", "maps", "raw", "format", "\"", formatTemplate, "\""}
	logger.Debugf("Waiting for dm to exist")
	for i := 0; i < maxRetries; i++ {
		out, err := o.executer.ExecuteWithTimeout(TimeOutMultipathdCmd, multipathdCmd, args)
		if err != nil {
			return "", err
		}
		dms := string(out)
		for _, volumeIdVariation := range volumeIdVariations {
			if strings.Contains(dms, volumeIdVariation) {
				return dms, nil
			}
		}

		time.Sleep(time.Second * time.Duration(intervalSeconds))
	}
	return "", &MultipathDeviceNotFoundForVolumeError{volumeIdVariations[0]}
}

func (o GetDmsPathHelperGeneric) ExtractDmFieldValues(dmFilterValues []string, mpathdOutput string) map[string]bool {
	dmFieldValues := make(map[string]bool)

	scanner := bufio.NewScanner(strings.NewReader(mpathdOutput))
	for scanner.Scan() {
		dmIndicator, dmFieldValue := o.getLineParts(scanner)
		if o.IsIndicatorMatchesFilterValues(dmFilterValues, dmIndicator) {
			dmFieldValues[dmFieldValue] = true
			logger.Infof("ExtractDmFieldValues: found: %s for: %s", dmFieldValue, dmIndicator)
		}
	}

	return dmFieldValues
}

func (GetDmsPathHelperGeneric) getLineParts(scanner *bufio.Scanner) (string, string) {
	deviceLine := scanner.Text()
	lineParts := strings.Split(deviceLine, mpathdSeparator)
	return lineParts[0], lineParts[1]
}

func (o GetDmsPathHelperGeneric) IsIndicatorMatchesFilterValues(dmFilterValues []string, indicatorValue string) bool {
	for _, filterValue := range dmFilterValues {
		if strings.Contains(indicatorValue, filterValue) {
			return true
		}
	}
	return false
}

func (GetDmsPathHelperGeneric) GetFullDmPath(dms map[string]bool, volumeId string) (string, error) {
	dm, err := getUniqueDmFieldValue(dms, volumeId)
	if err != nil {
		return "", err
	}

	dmPath := filepath.Join(DevPath, filepath.Base(strings.TrimSpace(dm)))
	return dmPath, nil
}

func getUniqueDmFieldValue(dmFieldValues map[string]bool, filter string) (string, error) {
	if len(dmFieldValues) > 1 {
		return "", &MultipleDmFieldValuesError{filter, dmFieldValues}
	}

	var dmFieldValue string
	for dmFieldValue = range dmFieldValues {
		break // because its a single value in the map(1 mpath device, if not it should fail above), so just take the first
	}
	return dmFieldValue, nil
}

func (GetDmsPathHelperGeneric) GetMpathDeviceNameFromProcMounts(procMounts string, volumePath string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(procMounts))
	for scanner.Scan() {
		procMountLine := scanner.Text()
		if strings.Contains(procMountLine, volumePath) {
			return extractMpathDeviceName(procMountLine), nil
		}
	}
	return "", &MultipathDeviceNotFoundForVolumePathError{volumePath}
}

func extractMpathDeviceName(procMountLine string) string {
	lineParts := strings.Fields(procMountLine)
	mpathDevicePath := lineParts[0]
	return filepath.Base(mpathDevicePath)
}

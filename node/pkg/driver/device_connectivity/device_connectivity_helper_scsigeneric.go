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
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"sync"
	"time"
	"unicode"
	"unsafe"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"golang.org/x/sys/unix"
)

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperScsiGenericInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperScsiGenericInterface

type OsDeviceConnectivityHelperScsiGenericInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityHelperScsiGenericInterface.
		Mainly for writing clean unit testing, so we can Mock this interface in order to unit test logic.
	*/
	RescanDevicesGetHostIds(lunId int, arrayIdentifiers []string) (map[int]bool, error)
	RescanDevices(lunId int, arrayIdentifiers []string, hostIDs map[int]bool) error
	GetMpathDevice(volumeId string) (string, error)
	FlushMultipathDevice(mpathDevice string) error
	RemovePhysicalDevice(sysDevices []string) error
	RemoveGhostDevice(lun int) error
	IsVolumePathMatchesVolumeId(volumeId string, volumePath string) (bool, error)
}

type OsDeviceConnectivityHelperScsiGeneric struct {
	Executer        executer.ExecuterInterface
	Helper          OsDeviceConnectivityHelperInterface
	MutexMultipathF *sync.Mutex
	CleanScsiDevice bool
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
	dmsetupCmd                  = "dmsetup"
	WwnOuiEnd                   = 7
	WwnVendorIdentifierEnd      = 16
	procMountsFilePath          = "/proc/mounts"
)

func NewOsDeviceConnectivityHelperScsiGeneric(executer executer.ExecuterInterface, clean_scsi_device bool) OsDeviceConnectivityHelperScsiGenericInterface {
	return &OsDeviceConnectivityHelperScsiGeneric{
		Executer:        executer,
		Helper:          NewOsDeviceConnectivityHelperGeneric(executer),
		MutexMultipathF: &sync.Mutex{},
		CleanScsiDevice: clean_scsi_device,
	}
}

func (r OsDeviceConnectivityHelperScsiGeneric) IsVolumePathMatchesVolumeId(volumeUuid string, volumePath string) (bool, error) {
    logger.Infof("IsVolumePathMatchesVolumeId: Searching matching volume id for volume path: [%s] ", volumePath)
    //volumeIdVariations := r.Helper.GetVolumeIdVariations(volumeUuid)

	mpathDeviceName, err := r.Helper.GetMpathDeviceName(volumePath)
	if err != nil {
		return false, err
	}

	SgInqWwn, err := r.Helper.GetWwnByScsiInq(mpathDeviceName)
	if err != nil {
		return false, err
	}

	// TODO variations
	if !isSameId(SgInqWwn, []string{volumeUuid}) {
		return false, &ErrorWrongDeviceFound{mpathDeviceName, volumeUuid, SgInqWwn}
	}

	return true, nil
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevicesGetHostIds(lunId int, arrayIdentifiers []string) (map[int]bool, error) {
	logger.Debugf("Rescan : Start rescan on specific lun, on lun : {%v}, with array identifiers : {%v}", lunId, arrayIdentifiers)
	if len(arrayIdentifiers) == 0 {
		e := &ErrorNotFoundArrayIdentifiers{lunId}
		logger.Errorf("%s", e.Error())
		return nil, e
	}

	return r.Helper.GetHostsIdByArrayIdentifiers(arrayIdentifiers)
}

func (r OsDeviceConnectivityHelperScsiGeneric) RescanDevices(lunId int, arrayIdentifiers []string, hostIDs map[int]bool) error {
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

	dmPath, _ := r.Helper.GetMpathDeviceName(volumeId)

	volumeIdVariations := []string{volumeId}

	if dmPath != "" {
		SgInqWwn, _ := r.Helper.GetWwnByScsiInq(dmPath)
		if isSameId(SgInqWwn, volumeIdVariations) {
			return dmPath, nil
		}
		logger.Warningf("Expected {%v} but got {%v} from sg_inq", volumeId, SgInqWwn)
		return "", &ErrorWrongDeviceFound{dmPath, volumeIdVariations[0], SgInqWwn}
	}
	return dmPath, nil
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
	f, err := os.OpenFile(deviceName, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	// BLKFLSBUF = 0x1261
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x1261, 0)
	if errno != 0 {
		return errno
	}
	return nil
	// TODO
	//	if err != nil && strings.Contains(err.Error(), "kernel hang") {
	//	c.limiter.StuckOps.Add(1)
}

// TODO
func (r *OsDeviceConnectivityHelperScsiGeneric) flushDeviceBuffersWithContext(ctx context.Context, devPath string) error {
	done := make(chan error, 1)

	go func() {
		// 1. Open the device with O_NONBLOCK
		// This prevents the open() itself from hanging if the driver is wedged [3]
		f, err := os.OpenFile(devPath, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			done <- fmt.Errorf("flush: failed to open %s: %w", devPath, err)
			return
		}
		defer f.Close()

		// 2. Execute the flush ioctl
		// This tells the kernel to commit and invalidate the buffer cache [4]
		_, _, errno := syscall.Syscall(
			syscall.SYS_IOCTL,
			f.Fd(),
			uintptr(BLKFLSBUF),
			0,
		)

		if errno != 0 && errno != syscall.ENOTTY {
			done <- fmt.Errorf("flush: ioctl BLKFLSBUF failed: %v", errno)
			return
		}
		done <- nil
	}()

	// 3. Monitor for completion or context timeout (Safety Gate)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// If the hardware is in D-state, the goroutine will remain
		// leaked, but our CSI logic can proceed [2]
		return fmt.Errorf("flush: timed out (D-state suspected) on %s: %w", devPath, ctx.Err())
	}
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


func (r *OsDeviceConnectivityHelperScsiGeneric) RemovePhysicalDevice(sysDevices []string) error {
	logger.Debugf(`Removing scsi device : {%v} by writing "1" to the delete file of each device: {%v}`, sysDevices, fmt.Sprintf(sysDeviceDeletePathFormat, "<deviceName>"))
	var wg sync.WaitGroup

	for _, deviceName := range sysDevices {
		if deviceName == "" { continue }

		wg.Add(1)
		go func(name string) {
			defer wg.Done()

			_ = r.Executer.ExecuteUninterruptible("path-delete", 10, 100, 5*time.Second, 30*time.Second, func() error {
				devPath := fmt.Sprintf("/dev/%s", name)
				_ = r.flushDeviceBuffers(devPath)

				var deletePath string
				if strings.HasPrefix(name, "nvme") {
					// On RHEL 7, namespaces are removed by deleting the controller
					// or specific namespace via the parent device's remove trigger.
					deletePath = fmt.Sprintf("/sys/block/%s/device/device/remove", name)
				} else {
					// STANDARD SCSI PATH
					deletePath = fmt.Sprintf("/sys/block/%s/device/delete", name)
				}

				// Safety check: if the path doesn't exist, we are already "clean"
				if _, err := os.Stat(deletePath); os.IsNotExist(err) {
					logger.Warningf("Idempotency: Block device {%v} was not found on the system, so skip deleting it", deviceName)
					return nil
				}

				if err := os.WriteFile(deletePath, []byte("1"), 0200); err != nil {
					logger.Errorf("Error while writing to file : {%v}. error: {%v}", deletePath, err.Error())
					return fmt.Errorf("failed to delete IBM stale device %s: %w", name, err)
				}
				return nil
			})
		}(deviceName)
	}

	wg.Wait()
	logger.Debugf("Finished removing SCSI devices : {%v}", sysDevices)
	return nil
}


// normalizeLun converts sysfs LUN strings (hex or decimal) to a standard decimal string.
// Example: "0x0001000000000000" -> "1"
// Example: "1" -> "1"
func (r *OsDeviceConnectivityHelperScsiGeneric) normalizeLun(lunStr string) string {
	lunStr = strings.TrimSpace(lunStr)
	if lunStr == "" {
		return ""
	}

	// If it starts with 0x, it's a hexadecimal representation (Common in 2025 Kernels)
	if strings.HasPrefix(lunStr, "0x") {
		// Parse the hex string to a 64-bit unsigned integer
		val, err := strconv.ParseUint(lunStr, 0, 64)
		if err != nil {
			return lunStr // Fallback to raw string if parsing fails
		}

		// SCSI LUNs in sysfs often use the "Peripheral Device Addressing" format.
		// For LUNs 0-255, the integer value is exactly what we expect.
		// For larger/hierarchical LUNs, we return the full integer string.
		return fmt.Sprintf("%d", val)
	}

	// If it's already a decimal string, return it as-is
	return lunStr
}

func (r OsDeviceConnectivityHelperScsiGeneric) ValidateLun(targetDm string, lun int, sysDevices []string) error {
	logger.Debugf("Validating lun {%v} on devices: {%v}", lun, sysDevices)
	validPathsFound := 0
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

		// readSysfs:		lunBytes, err := os.ReadFile(filepath.Join(slavePath, "lun"))
		// if err != nil {
		//	logger.Errorf("Cannot read LUN for %s: %v", sdName, err)
		//	continue // Skip path if metadata cannot be read
		//}
		//actualLun := strings.TrimSpace(string(lunBytes))


		var actualLun, sysfsId, hwId string
		if strings.HasPrefix(sysDevice, "nvme") {
			// NVMe Path Logic
			actualLun = readSysfs(fmt.Sprintf("/sys/block/%s/device/nsid", sysDevice))
			sysfsId = readSysfs(fmt.Sprintf("/sys/block/%s/nguid", sysDevice))
			hwId = sysfsId // NVMe ID is authoritative in sysfs
		} else {
			// SCSI Path Logic
			actualLun = normalizeLun(readSysfs(fmt.Sprintf("/sys/block/%s/device/lun", sysDevice)))
			sysfsId = readSysfs(fmt.Sprintf("/sys/block/%s/device/wwid", sysDevice))
			hwId, err = r.GetDeviceWWN("/dev/" + sysDevice) // Direct SG_IO IOCTL
		}

		isBad := false
		if err != nil || !r.IsSerialMatch(hwId, expectedSerial) || actualLun != normLun {
			isBad = true
		} else if sysfsId != "" && !r.IsSerialMatch(sysfsId, hwId) {
			isBad = true // Identity Split: Kernel believes one thing, Hardware says another
		}

//"Hardware inquiry failed"
			//reason = fmt.Sprintf("LUN Mismatch (got %s, exp %s)", actualLun, expectedLun)
			//reason = fmt.Sprintf("Hardware Serial mismatch (got %s, exp %s)", hwSerial, expectedSerial)
			//reason = fmt.Sprintf("Kernel/Hardware Identity Split (Sysfs: %s, HW: %s)", sysfsSerial, hwSerial)

		if isBad {
			logger.Warnf("unsafe path %s from %s (Serial/LUN mismatch)", name, targetDm)
			return fmt.Errorf("lun not valid, storage lun: %v, linkedPath: %v to device: %v", lun, destinationPath, device)
		}

		if strings.TrimSpace(string(stateBytes)) != "running" {
			// TODO Error message
			continue
		}
		validPathsFound++
	}
	if validPathsFound == 0 {
		logger.Debugf("Finished lun validation")
	} else {
		// TODO replace with error
		return "", fmt.Errorf("all paths for device %s failed safety verification", targetDm)
	}
}

func (r *OsDeviceConnectivityHelperScsiGeneric) RemoveGhostDevice(expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	if !r.CleanScsiDevice {
		return nil
	}
	sgEntries, err := os.ReadDir("/sys/class/scsi_generic")
	if err != nil {
		if os.IsNotExist(err) { return nil }
		return fmt.Errorf("failed to read scsi_generic: %w", err)
	}

	var (
		deleted int
		notLun  int
		notPQ   int
	)

	normLun := r.normalizeLun(fmt.Sprintf("%d", expectedLun))

	for _, entry := range sgEntries {
		sgName := entry.Name()
		deviceDir := filepath.Join("/sys/class/scsi_generic", sgName, "device")

		// Fix: Method must be called on 'r'
		hctl, err := r.GetHCTLFromSg(sgName)
		if err != nil {
			continue
		}

		// 2. Validate LUN Match
		lunBytes, _ := os.ReadFile(filepath.Join(deviceDir, "lun"))
		if r.normalizeLun(string(lunBytes)) != normLun {
			notLun++
			continue
		}

		// 3. TRANSPORT SCOPE CHECK
		// Fix: Removed 'filepath.EvalSymlinks' shadowing; calling method on 'r'
		isOurPath := r.isPathOwnedByMyArray(hctl, arrayIdentifiers)

		// 4. VENDOR CHECK (IBM)
		vendorBytes, _ := os.ReadFile(filepath.Join(deviceDir, "vendor"))
		vendor := strings.TrimSpace(string(vendorBytes))
		isIBM := strings.Contains(vendor, "IBM")

		// 5. Identity/Ghost Logic
		// Fix: getHardwareSerial returns (string, error). Added hwErr handling.
		hwSerial, hwErr := r.getHardwareSerial(deviceDir)
		
		// Fix: IsGhostDevice returns (bool, error). Added check.
		isGhost, _ := r.IsGhostDevice(sgName)

		// Fix: Removed shadowed 'shouldDelete' declaration.
		// Logic: Prune if it's an IBM Ghost OR if it's a path we own but the hardware ID is wrong.
		shouldDelete := (isGhost && isIBM) || (isOurPath && (!isIBM || (hwSerial != "" && !r.IsSerialMatch(hwSerial, expectedSerial))))

		if shouldDelete {
			reason := "serial mismatch"
			if isGhost {
				reason = "IBM PQ=1 Ghost (No block device)"
			} else if hwErr != nil {
				reason = fmt.Sprintf("IBM path inquiry failed: %v", hwErr)
			}

			logger.Warnf("Pruning stale IBM device %s. Reason: %s", sgName, reason)

			// 6. REMEDIATION: Using the Safety Gater to prevent D-state hangs
			// Fix: Writing "1" to sysfs delete must be 0200 (Write-only) for root.
			// TODO restore check
			_ = r.resourceManager.ExecuteUninterruptible("path-delete-"+sgName, 1, 10, 2*time.Second, 15*time.Second, func() error {
				deletePath := filepath.Join(deviceDir, "delete")
				return os.WriteFile(deletePath, []byte("1"), 0200) 
			})
			deleted++
		} else {
			notPQ++
		}
	}

	if deleted != 0 {
		logger.Debugf("Deleted %d devices. Found %d not-our-lun, %d our lun but not ghost", deleted, notLun, notPQ)
	}
	return nil
}



func(r *OsDeviceConnectivityHelperScsiGeneric) GetHCTLFromSg(sgName string) (string, error) {
	// sysPath: /sys/class/scsi_generic/sg5
	// This symlink is provided by the kernel specifically to link the generic
	// interface to the underlying SCSI device object.
	deviceLink := filepath.Join("/sys/class/scsi_generic", sgName, "device")

	realPath, err := filepath.EvalSymlinks(deviceLink)
	if err != nil {
		return "", fmt.Errorf("failed to resolve SCSI device link for %s: %w", sgName, err)
	}

	// The HCTL (Host:Channel:Target:LUN) is the base name of the leaf directory.
	// Example: /sys/devices/pci0000:00/.../target4:0:0/4:0:0:1 -> "4:0:0:1"
	hctl := filepath.Base(realPath)

	// 3. Validation
	// Standard SCSI HCTL always has exactly 3 colons (4 parts).
	if strings.Count(hctl, ":") != 3 {
		// Fallback: If it's a newer NVMe-oF device, the naming might differ.
		// For CSI/SCSI, we treat anything without 3 colons as an error.
		return "", fmt.Errorf("invalid HCTL format '%s' for device %s", hctl, sgName)
	}

	return hctl, nil
}



func (r *OsDeviceConnectivityHelperScsiGeneric) isPathOwnedByMyArray(hctl string, arrayIdentifiers []string) bool {
    // 1. Precise HCT Extraction
    // hctl is in format "H:C:T:L". We need "H:C:T" to find the target directory.
    parts := strings.Split(hctl, ":")
    if len(parts) < 4 {
        return false
    }
    hct := strings.Join(parts[:3], ":")
    deviceBase := fmt.Sprintf("/sys/class/scsi_device/%s/device", hctl)
    targetDir := fmt.Sprintf("target%s", hct)

    targetID := ""

    // 2. Fibre Channel (FC) Logic
    // Path: .../device/fc_transport/targetH:C:T/port_name
    fcPath := filepath.Join(deviceBase, "fc_transport", targetDir, "port_name")
    if data, err := os.ReadFile(fcPath); err == nil {
        // Normalization: Trim 0x and whitespace
        targetID = strings.TrimPrefix(strings.TrimSpace(string(data)), "0x")
    }

    // 3. SAS Logic
    // Path: .../device/sas_device/targetH:C:T/sas_address
    if targetID == "" {
        sasPath := filepath.Join(deviceBase, "sas_device", targetDir, "sas_address")
        if data, err := os.ReadFile(sasPath); err == nil {
            targetID = strings.TrimPrefix(strings.TrimSpace(string(data)), "0x")
        }
    }

    // 4. iSCSI Logic (Dynamic Traversal)
    // Path: .../device/sessionX/iscsi_session/sessionX/targetname
    if targetID == "" {
        targetID = r.getIscsiTargetName(deviceBase)
    }

    // 5. Case-Insensitive Normalized Comparison
    for _, id := range arrayIdentifiers {
        // Strip 0x from the expected IDs (CSI secrets) for a clean match
        normalizedExpected := strings.TrimPrefix(strings.ToLower(id), "0x")
        if strings.EqualFold(targetID, normalizedExpected) {
            return true
        }
    }
    return false
}

func (r *OsDeviceConnectivityHelperScsiGeneric) getIscsiTargetName(deviceBase string) string {
    // On RHEL 7, the session directory is a child of the SCSI device directory
    files, err := os.ReadDir(deviceBase)
    if err != nil {
        return ""
    }
    for _, f := range files {
        if strings.HasPrefix(f.Name(), "session") {
            // Traverse deeper to find the iscsi_session attributes
            // Path: .../sessionX/iscsi_session/sessionX/targetname
            targetNamePath := filepath.Join(deviceBase, f.Name(), "iscsi_session", f.Name(), "targetname")
            if data, err := os.ReadFile(targetNamePath); err == nil {
                return strings.TrimSpace(string(data))
            }
        }
    }
    return ""
}


// getHardwareSerial safely retrieves the serial, returning error if path is blocked
func (r *OsDeviceConnectivityHelperScsiGeneric) getHardwareSerial(deviceDir string) (string, error) {
	// Try the standard 'wwid' file first
	wwidBytes, err := os.ReadFile(filepath.Join(deviceDir, "wwid"))
	if err != nil || len(bytes.TrimSpace(wwidBytes)) == 0 {
		// Fallback: If 'wwid' is empty, path might be blocked or transitioning
		return "", fmt.Errorf("serial unavailable")
	}
	return strings.TrimSpace(string(wwidBytes)), nil
}


// sgIoHdr is the Linux SG_IO ioctl structure
// TODO duplicates SgIoHeader
type sgIoHdr struct {
	interface_id    int32
	dxfer_direction int32
	cmd_len         uint8
	mx_sb_len       uint8
	iovec_count     uint16
	dxfer_len       uint32
	dxferp          uintptr
	cmdp            uintptr
	sbp             uintptr
	timeout         uint32
	flags           uint32
	pack_id         int32
	usr_ptr         uintptr
	status          uint8
	masked_status   uint8
	msg_status      uint8
	sb_len_wr       uint8
	host_status     uint16
	driver_status   uint16
	resid           int32
	duration        uint32
	info            uint32
}



const (
	SG_IO             = 0x2285
	SG_DXFER_FROM_DEV = -3
)


func (r *OsDeviceConnectivityHelperGeneric) IsGhostDevice(sgName string) (bool, error) {
	// 1. Sysfs Fast-Check (Kernel-Level Knowledge)
	deviceBase := fmt.Sprintf("/sys/class/scsi_generic/%s/device", sgName)
	stateBytes, err := os.ReadFile(filepath.Join(deviceBase, "state"))
	if err == nil {
		state := strings.TrimSpace(string(stateBytes))
		// 'offline' or 'deleting' means the kernel has already severed ties
		if state == "offline" || state == "cancelled" || state == "deleting" {
			return true, nil
		}
		// 'blocked' or 'quiesce' means we CANNOT run ioctl without hanging
		if state == "blocked" || state == "quiesce" {
			return false, fmt.Errorf("device %s is blocked; cannot verify ghost status", sgName)
		}
	}

	// 2. Type 31 Check (PQ=1 mapped to Type 31)
	typeBytes, err := os.ReadFile(filepath.Join(deviceBase, "type"))
	if err == nil && strings.TrimSpace(string(typeBytes)) == "31" {
		return true, nil
	}

	// 3. Disk vs Block Check
	// If it's a disk type (0) but has no block child, it's a stale/ghost entry
	if isDiskType(deviceBase) {
		blockPath := filepath.Join(deviceBase, "block")
		if _, err := os.Stat(blockPath); os.IsNotExist(err) {
			return true, nil
		}
	}

	// 4. The Hardware Truth (SCSI Inquiry)
	return checkPQviaIoctl(sgName)
}



func (r *OsDeviceConnectivityHelperScsiGeneric) isDiskType(deviceBase string) bool {
	data, err := os.ReadFile(filepath.Join(deviceBase, "type"))
	return err == nil && strings.TrimSpace(string(data)) == "0"
}



func (r *OsDeviceConnectivityHelperScsiGeneric) checkPQviaIoctl(sgName string) (bool, error) {

	// 1. Avoid opening if sysfs already tells us the path is blocked
	if isHardwareBlocked(sgName) {
		return false, fmt.Errorf("device %s is in blocked/quiesce state, skipping ioctl", sgName)
	}

	devPath := filepath.Join("/dev", sgName)

	// 1. Try O_RDONLY first (The "Clean" way)
	fd, err := syscall.Open(devPath, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)

	// 2. If EACCES or EPERM, try O_RDWR (The "Privileged" way)
	// Some RHEL 7 drivers require Write bits to allow ANY SG_IO ioctl.
	if err != nil && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		fd, err = syscall.Open(devPath, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	}

	if err != nil {
		// ENXIO/ENODEV means the hardware is physically gone (Ghost)
		if errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) {
			return true, nil
		}
		return false, fmt.Errorf("failed to open %s: %w", devPath, err)
	}


	defer syscall.Close(fd)

	// 1. Check Subsystem (Avoid sending SCSI Inquiry to non-SCSI devices)
	subsystem, _ := os.Readlink(fmt.Sprintf("/sys/class/scsi_generic/%s/device/subsystem", sgName))
	if strings.Contains(subsystem, "nvme") {
		// NVMe 'ghosts' are handled differently (Namespace state)
		return false, nil
	}


	const allocationLen = 36
	inqResp := make([]byte, allocationLen)
	senseBuf := make([]byte, 32)
	cdb := [6]byte{0x12, 0, 0, 0, uint8(allocationLen), 0}

	header := sgIoHdr{
		interface_id:    'S',
		dxfer_direction: SG_DXFER_FROM_DEV,
		cmd_len:         uint8(len(cdb)),
		mx_sb_len:       uint8(len(senseBuf)),
		sbp:             uintptr(unsafe.Pointer(&senseBuf[0])),
		dxfer_len:       uint32(len(inqResp)),
		dxferp:          uintptr(unsafe.Pointer(&inqResp[0])),
		cmdp:            uintptr(unsafe.Pointer(&cdb[0])),
		timeout:         1000, // 1 second is standard
		flags:           0, // Ensure no direct I/O or other flags are set by accident
	}

	// Use a retry loop specifically for transient SCSI errors like Unit Attention (UA)
	maxRetries := 2
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Reset sense buffer before each attempt
		for i := range senseBuf {
			senseBuf[i] = 0
		}
		header.sb_len_wr = 0 // Reset written sense length

		// Syscall with retry on EAGAIN/EBUSY
		var errno syscall.Errno
		for i := 0; i < 3; i++ {
			_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), SG_IO, uintptr(unsafe.Pointer(&header)))
			if errno != syscall.EAGAIN && errno != syscall.EBUSY {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		if errno != 0 {
			// syscall.ENOTTY - Inappropriate ioctl for device
			if errno == syscall.ENXIO || errno == syscall.ENODEV || errno == syscall.ENOTTY {
				return true, nil // Confirmed gone at kernel level
			}
			return false, fmt.Errorf("ioctl failed: %v", errno)
		}

		// Validate Transport Health (Host Status) - if fabric is down, this isn't a ghost
		if header.host_status != 0 {
			return false, fmt.Errorf("transport failure (host: 0x%x): path is down, not a ghost", header.host_status)
		}

		// Evaluate SCSI Status
		switch header.status {
		case 0x00: // GOOD: Break retry loop and process the PQ bits
			goto PROCESS_PQ

		case 0x02: // CHECK CONDITION: Inspect Sense Data
			if header.sb_len_wr >= 14 {
				senseKey := senseBuf[2] & 0x0f

				if senseKey == 0x06 { // UNIT ATTENTION
					logger.Debugf("UA detected on %s, retrying inquiry...", sgName)
					continue // Retry the main 'attempt' loop
				}

				// Handle other "ghost" conditions if they appear on the first try:
				asc := senseBuf[12]
				ascq := senseBuf[13]
				if senseKey == 0x05 && asc == 0x25 && ascq == 0x00 { // LU Not Supported
					logger.Debugf("Confirmed Ghost via Sense Data: LU Not Supported")
					return true, nil
				}

				// Sense Key 0x02 (Not Ready) + ASC 0x3A (Medium Not Present)
				if senseKey == 0x02 && asc == 0x3A {
					return true, nil
				}

			}
			// If it's a different error, the path might just be transiently failing
			return false, fmt.Errorf("scsi check condition: sense key 0x%02x", senseBuf[2]&0x0f)

		default:
			return false, fmt.Errorf("unexpected scsi status: 0x%02x", header.status)
		}
	} // End of 'attempt' loop

	// If we exhausted retries and are still here (very rare)
	return false, fmt.Errorf("failed to get reliable status after retries")


PROCESS_PQ:
	// 5. Evaluate Peripheral Qualifier
	// PQ is bits 7-5 of byte 0.
	// 000b = Connected
	// 001b = Supported but not connected (GHOST)
	// 011b = Not supported
	pq := (inqResp[0] >> 5) & 0x07

	// Also check byte 0 bits 4-0 (Device Type)
	// Type 0x1f (31) is the standard "no device" type.
	devType := inqResp[0] & 0x1f
	// device 0x1f - Unknown or no device type.

	// 0x01 = PQ 1 (Logical unit is capable of being supported, but not connected)
	// 0x03 = PQ 3 (The device server is not capable of supporting a device on this
	if pq == 1 || pq == 3 || devType == 0x1f {
		logger.Debugf("SCSI Inquiry confirmed ghost: PQ=%d, Type=%d", pq, devType)
		return true, nil
	}
    return false, nil
}

//State	isGhostDevice Result	Reasoning
//offline/deleting	true	Kernel has marked the path as dead.
//blocked/quiesce	Error	Path is frozen; ioctl may hang despite O_NONBLOCK.
//running + PQ=1	true	Target confirms LUN is configured but disconnected.
//running + PQ=3	true	Target confirms LUN is unsupported.
//running + Type=31

func (r *OsDeviceConnectivityHelperScsiGeneric) isHardwareBlocked(sgName string) bool {
	statePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName)
	state, err := os.ReadFile(statePath)
	if err != nil {
		return true // Assume blocked if we can't read state
	}
	s := strings.TrimSpace(string(state))
	// 'blocked' means the transport layer has paused queues (e.g., FC cable pulled)
	// 'quiesce' means the driver is busy.
	// Both will cause an ioctl to hang despite O_NONBLOCK.
	// "quiesce" may be used during upgrade / failover - add log
	return s == "blocked"
}


const (
    // Correct OpCode for DM_DEV_REMOVE (Cmd 0x04)
    // _IOWR(0xfd, 0x04, 312 bytes)
    DM_DEV_REMOVE = 0xc138fd04
	DM_DEV_SUSPEND = 0xc138fd06
	DM_DEV_STATUS = 0xc138fd07


    DM_VERSION_MAJOR = 4
    DM_VERSION_MINOR = 0
    DM_VERSION_PATCH = 0


	DM_SUSPEND_FLAG    = 1 << 1  // Used to freeze I/O
	DM_NOFLUSH_FLAG    = 1 << 8  // Critical: do not hang on dead paths, Equivalent to --noflush: drops pending I/O on remove
	DM_DEFERRED_REMOVE = 1 << 17 // Standard for CSI Unstage
)



func (r *OsDeviceConnectivityHelperScsiGeneric) TeardownVolume(target string, expectedWWID string) error {

	// --- PHASE 1: UNMOUNT ---
	if r.Mounter.isMounted(target) {
		if err := r.Mounter.UnmountWithTimeout(target, 20); err != nil {
			return fmt.Errorf("unmount phase failed: %w", err)
		}
	}

	// --- PHASE 1: VFS LAYER (UNMOUNT STACK) ---
	mounts, _ := c.getMountsForPath(target)
	var major, minor int
	if len(mounts) > 0 {
		major, minor = mounts[0].Major, mounts[0].Minor
		for i := len(mounts) - 1; i >= 0; i-- {
			_ = c.escalatedUnmount(target)
			_ = c.pollLayerDeleted(target, mounts[i].MountID, 5*time.Second)
		}
	}

	// BARRIER: Mountinfo must be clean
	if !c.pollMountDeleted(target, 10*time.Second) {
		return fmt.Errorf("teardown: mountinfo not clean for %s", target)
	}

	// Resolve Hardware
	mpathName := c.findDMNameByWWID(expectedWWID)
	slaves := c.getSlavesForDevice(major, minor)

	// --- PHASE 2: BLOCK LAYER (IDENTITY & OPENCOUNT) ---
	if mpathName != "" {
		// Verify OpenCount to determine removal strategy
		openCount := c.getOpenCount(mpathName)

		needDeferRemove := false

		// TODO maybe check holders count?
		if openCount == 0 {
		    // --- PHASE 2: DEVICE-MAPPER LAYER ---
			// 1. Flush Virtual Cache (Uninterruptible, 5s Handoff)
		   _ = c.resourceManager.ExecuteUninterruptible("flush", 10, 50, 5*time.Second, 30*time.Second, func() error {
				   return c.flushDeviceBuffers(fmt.Sprintf("/dev/mapper/%s", mpathName))
		   })

			// SUCCESS PATH: No one is using the device. Immediate delete.
			err := c.resourceManager.ExecuteInterruptible("mpath-socket", 3, 20, 10*time.Second, func(ctx context.Context) error {
				return c.multipathdAction("del map " + mpathName)
			})
			if err != nil {
				needDeferRemove = true
				// Fallback if socket fails despite opencount being 0
			}
		} else if openCount > 0 {
			// STUBBORN PATH: Mount is gone, but a process has a raw FD open.
			// Do not block. Use Deferred Remove (Fire-and-Forget).

			// TODO should we try  DM_SKIP_BDGET_FLAG (same as dmsetup --force)
			// return exec.Command("dmsetup", "remove", "--force", "--retry", dmName).Run()

			needDeferRemove = true
		}
		if needDeferRemove {
			_ = c.multipathdAction("disablequeueing map " + mpathName)
			for _, s := range slaves {
				_ = c.multipathdAction("fail path " + s)
			}
			_ = c.deferredRemove(mpathName)
		}
	}

	// --- PHASE 3 & 4: EMERGENCY & PHYSICAL ---
	// If mpathName still exists in sysfs after the above, Phase 3 triggers
	if len(slaves) > 0 {
		c.RemovePhysicalDevice(slaves)
	}

	return os.Remove(target)
}

// TODO call FinalWwidPurge
func (r *OsDeviceConnectivityHelperScsiGeneric) IdentityAwarePreScan(targetPath string, expectedWWID string) error {
	mounts, _ := c.getMountsForPath(targetPath)
	mpathName := c.findDMNameByWWID(expectedWWID)

	// CASE 1: Path is mounted
	if len(mounts) > 0 {
		var currentWWID, actualHw string
		// Safety Guard: Resolving WWID involves sysfs/ioctl, which can hang
		_ = c.resourceManager.ExecuteUninterruptible("wwid-check", 5, 20, 2*time.Second, 10*time.Second, func() error {
			currentWWID, _ = r.getWWIDByDev(mounts[0].Major, mounts[0].Minor)
			actualHw, _ := r.GetDeviceWWN(devicePath)
			return nil
		})

		if strings.EqualFold(currentWWID, expectedWWID) && (actualHw == "" ||  strings.EqualFold(actualHw, expectedWWID)) {
			// Zombie Match: Same volume from a crashed attempt. Full cleanup.
			// TODO not null case?
			err := c.TeardownVolume(targetPath, expectedWWID)
			if err != nil {
				return fmt.Errorf("pre-scan: failed to clear zombie volume: %w", err)
			}
			return nil
		} else {
			// Collision: A DIFFERENT volume is here.
			// We MUST NOT delete the DM map (we don't own it), but we must rescue the path.
			for i := len(mounts) - 1; i >= 0; i-- {
				// MNT_DETACH is safe; it won't hang even if the rogue volume is in D-state
				_ = syscall.Unmount(targetPath, syscall.MNT_DETACH)
				// TODO duplicate check with IsMounted below
				if !c.pollLayerDeleted(targetPath, mounts[i].MountID, 5*time.Second) {
					return fmt.Errorf("pre-scan collision: failed to rescue path %s from rogue volume %s", targetPath, currentWWID)
				}
			}

			// After detaching, ensure the directory is clear for our new mount
			if mounted, _ := c.IsMounted(targetPath); mounted {
				return fmt.Errorf("pre-scan: collision at %s; failed to detach rogue volume", targetPath)
			}
			// TODO _ = os.RemoveAll(targetPath) // Use RemoveAll for safety in pre-scan
		}
	}

	// CASE 2: Unmounted Zombie Device
	if mpathName != "" {
		// Before checking OpenCount, ensure we aren't queuing I/O to a dead map
		_ = c.multipathdAction("disablequeueing map " + mpathName)

		openCount := c.getOpenCount(mpathName)
		if openCount == 0 {
			// Clean fresh start
			_ = c.multipathdAction("del map " + mpathName)
		} else {
			// Device is busy (e.g. by an old LVM scan or udev).
			// Schedule for deletion the moment it's released.
			_ = c.deferredRemove(mpathName)
		}
	}

	// CASE 3: Final Directory Cleanup
	// If it's not a mount point but the directory exists, clear it.
	if _, err := os.Stat(targetPath); err == nil {
		// TODO RemoveAll or Remove (should probably be Remove)
		_ = os.RemoveAll(targetPath) // Use RemoveAll for safety in pre-scan
	}

	return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) FinalWwidPurge(expectedWWID string) error {
	// Normalize WWID once for case-insensitive matching throughout the function
	targetWWID := strings.ToLower(strings.TrimSpace(expectedWWID))

	// 1. Clean up Multipath Layer (DM and Mapper)
	mpathName := r.findDMNameByWWID(targetWWID)
	if mpathName != "" {
		_ = r.resourceManager.ExecuteUninterruptible("mpath-final-del", 1, 5, 2*time.Second, 10*time.Second, func() error {
			// Try graceful socket delete first (zero-fork)
			err := r.multipathdAction("del map " + mpathName)
			if err != nil {
				// Fallback to kernel-level deferred removal if daemon is stuck
				return r.deferredRemove(mpathName)
			}
			return nil
		})
	}

	// Scan sysfs for hidden DM devices that multipathd might have lost track of
	dmUUIDs, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
	for _, path := range dmUUIDs {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(strings.ToLower(string(data)), targetWWID) {
			dmName := strings.Split(path, "/")[3] // Extract 'dm-X'
			_ = r.resourceManager.ExecuteUninterruptible("mpath-stale-del", 1, 5, 2*time.Second, 10*time.Second, func() error {
				return r.deferredRemove(dmName)
			})
		}
	}

	// 2. Identify and Sever all Physical SCSI Paths (sdX)
	devices, err := os.ReadDir("/sys/block")
	if err != nil {
		return fmt.Errorf("failed to scan /sys/block: %w", err)
	}

	for _, dev := range devices {
		devName := dev.Name()
		if !strings.HasPrefix(devName, "sd") && !strings.HasPrefix(devName, "nvme") {
			continue
		}

		// 3. Resolve Major:Minor for D-State Gating
		// This prevents the driver from even trying to stat a known-stuck device.
		canonicalID, err := r.getCanonicalID("/dev/" + devName)
		if err == nil && r.Executer.IsStuck(canonicalID) {
			logger.Warningf("FinalPurge: Skipping %s (%s) - hardware is wedged in D-state", devName, canonicalID)
			continue
		}

		// 4. Identity Check & Purge
		_ = r.resourceManager.ExecuteUninterruptible("scsi-purge-"+devName, 5, 50, 2*time.Second, 15*time.Second, func() error {
			currentWWID, err := r.getWWIDByDevName(devName)
			if err != nil || !strings.EqualFold(currentWWID, targetWWID) {
				return nil // Not our device or already gone
			}

			// MATCH FOUND: This is a stale path for our volume
			// Fail path in daemon first to prevent socket hangs during deletion
			_ = r.multipathdAction("fail path " + devName)

			// BEST EFFORT FLUSH: 2-second deadline to prevent D-state hang on dead fabric
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = r.flushDeviceBuffersWithContext(ctx, "/dev/"+devName)

			// 5. Final Severing
			if strings.HasPrefix(name, "nvme") {
				// On RHEL 7, namespaces are removed by deleting the controller 
				// or specific namespace via the parent device's remove trigger.
				deletePath = fmt.Sprintf("/sys/block/%s/device/device/remove", name)
			} else {
				// STANDARD SCSI PATH
				deletePath = fmt.Sprintf("/sys/block/%s/device/delete", name)
			}
			// Standard 0200 (Write-Only) for sysfs delete
			return os.WriteFile(deletePath, []byte("1"), 0200)
		})
	}

	return nil
}


func (r OsDeviceConnectivityHelperScsiGeneric) VerifyAndGetDmDevice(devName string) (string, error) {
	expectedSerial := strings.ToLower(volumeUuid)
	expectedLunStr := fmt.Sprintf("%d", lun)
	expectedMpathUuid := "mpath-" + expectedSerial

	// 1. GLOBAL CONFLICT AUDIT
	// Ensure the serial isn't already claimed by a DIFFERENT dm device.
	allDmDirs, _ := filepath.Glob("/sys/block/dm-*")
	for _, dmDir := range allDmDirs {
		if dm == currentDmName {
			continue
		}

		uuidContent, err := os.ReadFile(filepath.Join(dmDir, "dm/uuid"))
		if err != nil {
			continue
		}

		actualUuid := strings.TrimSpace(string(uuidContent))
		// Check for identity match (handling common mpath-3 / mpath- prefix variations)
		if r.IsSerialMatch(actualUuid, expectedSerial) {
			dmName := filepath.Base(dmDir)

			// Check if this is a "zombie" or an active device
			holders, _ := os.ReadDir(filepath.Join(dmDir, "holders"))
			if len(holders) > 0 {
				// If holders exist and it's NOT our current target, we have a fatal safety conflict
				return "", fmt.Errorf("FATAL: Serial %s is already in use by active device %s", volumeUuid, dmName)
			}

			// If it's a stale map for our serial, clean it up before proceeding
			logger.Warnf("Found stale multipath map %s for serial %s. Removing.", dmName, volumeUuid)
			//forceFlushDM(..)
			//_, _ = r.Executer.Execute("dmsetup", "remove", "-f", dmName)
		}
	}

	// TODO Discover based on uuid (this version) OR discover baese on dm name (and then cleanup is pre-scan step)

	// 2. DISCOVERY & INTERNAL TRIPLE-CHECK
	// Re-scan dm devices to find the one we just verified/cleaned
	targetDm := ""
	for _, dmDir := range allDmDirs {
		uuidContent, _ := os.ReadFile(filepath.Join(dmDir, "dm/uuid"))
		if r.IsSerialMatch(string(uuidContent), expectedSerial) {
			targetDm = filepath.Base(dmDir)
			break
		}
	}

	if targetDm == "" {
		return "", fmt.Errorf("no multipath device found in kernel for serial %s", volumeUuid)
	}
	return targetDm, nil
}

// DmIoctl corresponds to struct dm_ioctl in <linux/dm-ioctl.h>
type DmIoctl struct {
    Version      [3]uint32
    DataSize     uint32
    DataStart    uint32
    TargetCount  uint32
    OpenCount    int32
    Flags        uint32
    EventNr      uint32
    Padding      uint32
    Dev          uint64
    Name         [128]byte
    Uuid         [129]byte
    Data         [7]byte // Padding to align
}


func (r *OsDeviceConnectivityHelperScsiGeneric) GetOpenCount(dmName string) (int32, error) {
	f, err := os.OpenFile("/dev/mapper/control", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("failed to open dm control: %w", err)
	}
	defer f.Close()

	//io := dmIoctl{
	//	VersionMajor: 4,
	//	VersionMinor: 0,
	//	VersionPatch: 0,
	//	DataSize:     uint32(unsafe.Sizeof(dmIoctl{})),
	//	Flags:        0,
	//}

	io := dmIoctl{
		VersionMajor: 4,
		DataSize:     uint32(unsafe.Sizeof(dmIoctl{})),
		DataStart:    uint32(unsafe.Sizeof(dmIoctl{})), // Standard practice
	}
	copy(io.Name[:], dmName)

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		f.Fd(),
		uintptr(DM_DEV_STATUS),
		uintptr(unsafe.Pointer(&io)),
	)

	if errno != 0 {
		// Both ENOENT and ENXIO mean the IBM LUN is already detached or missing
		if errno == unix.ENOENT || errno == unix.ENXIO {
			return -1, nil
		}
		return -1, fmt.Errorf("ioctl DM_DEV_STATUS failed: %w", errno)
	}

	// io.OpenCount is now updated by the kernel in-place
	return int32(io.OpenCount), nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) multipathdAction(cmd string) error {
	response, err := r.Executer.MultipathdCmd(cmd)

	// 5. Parse Response Content
	// Multipathd returns "ok" or "map deleted" on success.
	// It returns "fail [reason]" on logical errors.
	if strings.HasPrefix(response, "fail") {
		// Identify specific failure types
		if strings.Contains(response, "map in use") {
			return fmt.Errorf("map in use") // Caller should use deferredRemove
		}
		if strings.Contains(response, "not found") {
			return nil // Already deleted, treat as success
		}
		return fmt.Errorf("multipathd command failed: %s", response)
	}

	return nil // Success
}


// pollLayerDeleted verifies that a specific MountID has vanished from the system
func (r *OsDeviceConnectivityHelperScsiGeneric) pollLayerDeleted(target string, mountID int, timeout time.Duration) bool {
	expiry := time.Now().Add(timeout)
	for time.Now().Before(expiry) {
		currentMounts, _ := c.getMountsForPath(target)
		found := false
		for _, m := range currentMounts {
			if m.MountID == mountID {
				found = true
				break
			}
		}
		if !found {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func (r *OsDeviceConnectivityHelperScsiGeneric) deferredRemove(name string) error {
	// TODO should we return success if errno is syscall.ENXIO
	flags := uint32(DM_DEFERRED_REMOVE | DM_NOFLUSH_FLAG)

	err := r.ExecuteDmIoctl(uintptr(DM_DEV_REMOVE), name, flags)
	if err != nil {
		// If we get EBUSY even with deferred, it's a kernel-level lock issue
		return fmt.Errorf("deferred remove failed for %s: %w", name, err)
	}
	return nil
}

func (r *OsDeviceConnectivityHelperScsiGeneric) ExecuteDmIoctl(command uintptr, dmName string, flags uint32) error {
    // 1. Structural Validation
    const expectedSize = 312
    if size := unsafe.Sizeof(DmIoctl{}); size != expectedSize {
        return fmt.Errorf("invalid DmIoctl size: expected %d, got %d", expectedSize, size)
    }

    // 2. Open Control Device
    control, err := os.OpenFile("/dev/mapper/control", os.O_RDWR|syscall.O_CLOEXEC, 0)
    if err != nil {
        return fmt.Errorf("failed to open /dev/mapper/control: %w", err)
    }
    defer control.Close()

    // 3. Prepare Data Structure
    data := DmIoctl{
        Version:   [3]uint32{DM_VERSION_MAJOR, DM_VERSION_MINOR, DM_VERSION_PATCH},
        DataSize:  uint32(unsafe.Sizeof(DmIoctl{})),
        DataStart: 0, // No extra payload follows this struct
        Flags:     flags,
    }
    copy(data.Name[:], dmName)

    // 4. Execute Syscall
    // WARNING: conversion to uintptr MUST happen inside the argument list to prevent GC moving data
    _, _, errno := syscall.Syscall(
        syscall.SYS_IOCTL,
        control.Fd(),
        command,
        uintptr(unsafe.Pointer(&data)),
    )

    // 5. Handle Results
    if errno != 0 {
        switch errno {
        case syscall.EBUSY:
            // Crucial for 2026 CSI drivers: identifies if a pod still holds a mount
			// TODO exepcted in deferred remove !!
            return fmt.Errorf("dm: %s is busy (OpenCount: %d)", dmName, data.OpenCount)
        case syscall.ENOENT:
            return nil // Already removed or doesn't exist
        case syscall.ENXIO:
            return fmt.Errorf("dm: device %s not found or invalid major/minor", dmName)
        case syscall.EINVAL:
            return fmt.Errorf("dm: invalid ioctl version or malformed DataSize")
        default:
            return fmt.Errorf("dm ioctl (0x%x) failed: %w", command, errno)
        }
    }

    return nil
}


func (r *OsDeviceConnectivityHelperScsiGeneric) MountVolume(source, target, wwid string, options []string) error {
	// 1. SELF-HEAL: Clear rogue/zombie mounts using the 2026 Pre-Scan
	if err := r.IdentityAwarePreScan(target, wwid); err != nil {
		return fmt.Errorf("mount-safety: pre-scan failed: %w", err)
	}

	// 2. RESOURCE PROTECTION: Use the "Mount" pool (Limit 10, Handoff 15s)
	// We use ExecuteUninterruptible because mount(2) can hang in D-state.
	return r.Executer.ExecuteUninterruptible(
		"mount-ops", 10, 50, 15*time.Second, 2*time.Minute,
		func() error {
			// Modern VFS API implementation (OpenTree -> MountSetattr -> MoveMount)
			return m.MountNative(source, target, options)
		},
	)
}


func (r *OsDeviceConnectivityHelperScsiGeneric) UnmountVolume(target, wwid string) error {
	// 1. Use the "Teardown" pool (Limit 20, Handoff 10s)
	// Phase 3 Emergency logic is triggered internally if del-map hangs.
	return r.Executer.ExecuteUninterruptible(
		"teardown-ops", 20, 100, 10*time.Second, 6*time.Minute,
		func() error {
			return r.TeardownVolume(target, wwid)
		},
	)
}




// ============== OsDeviceConnectivityHelperInterface ==========================

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityHelperInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityHelperInterface

type OsDeviceConnectivityHelperInterface interface {
	/*
		This is helper interface for OsDeviceConnectivityScsiGeneric.
		Mainly for writting clean unit testing, so we can Mock this interface in order to unit test OsDeviceConnectivityHelperGeneric logic.
	*/
	GetHostsIdByArrayIdentifiers(arrayIdentifier []string) ([]int, error)
	GetWwnByScsiInq(dev string) (string, error)
	GetMpathDeviceName(volumePath string) (string, error)
	GetMpathVolumeId(mpathdOutput string, mpathDeviceName string, dmDirectory string) (string, error)
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


//TODO
//cleanID := strings.TrimSpace(strings.Trim(string(data), "\x00"))
//idFromSys := strings.ToLower(strings.TrimPrefix(cleanID, "0x"))
func (o *OsDeviceConnectivityHelperGeneric) GetHostsIdByArrayIdentifiers(arrayIdentifier []string) (map[string][]int, error) {
	cleanLookup := make(map[string]string)

	// Track which protocols we actually need to search
	var hasIscsi, hasFC, hasNVMe bool

	for _, id := range arrayIdentifier {
		logger.Debugf("Check if any match is relevant for storage target (%s)", arrayIdentifier)
		clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "0x"))
		cleanLookup[clean] = id

		// Categorize by ID format
		if strings.HasPrefix(clean, "iqn.") {
			hasIscsi = true
		} else if strings.HasPrefix(clean, "nqn.") {
			hasNVMe = true
		} else if len(clean) == 16 {
			// Standard WWPN is 16 chars (64-bit)
			hasFC = true
		}
	}

	hostMap := make(map[string]map[int]struct{})

	// Only define search groups for protocols present in the input
	var searchGroups []struct {
		root   string
		suffix string
	}
	if hasIscsi {
		searchGroups = append(searchGroups, struct{ root, suffix string }{"/sys/class/iscsi_host", "targetname"})
	}
	if hasFC {
		searchGroups = append(searchGroups, struct{ root, suffix string }{"/sys/class/fc_remote_ports", "port_name"})
	}
	if hasNVMe {
		searchGroups = append(searchGroups, struct{ root, suffix string }{"/sys/class/nvme-fabrics", "subsysnqn"})
	}

	// 2. Loop only over active groups
	for _, group := range searchGroups {
		entries, err := os.ReadDir(group.root)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			idPath := filepath.Join(group.root, entry.Name(), group.suffix)
			data, err := os.ReadFile(idPath)
			if err != nil {
				logger.Warningf("Could not read target name from file : {%v}, error : {%v}", idPath, err)
				continue
			}

			idFromSys := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(string(data)), "0x"))

			if originalID, found := cleanLookup[idFromSys]; found {
				hostNum, err := o.extractHostNumber(entry.Name())
				if err == nil {
					logger.Debugf("portState path (%s) was found. Adding host ID {%v} to the id list", idPath, hostNum)
					if hostMap[originalID] == nil {
						hostMap[originalID] = make(map[int]struct{})
					}
					hostMap[originalID][hostNum] = struct{}{}
				} else {
					logger.Warningf("Host number in for target file was not valid : {%v}", idPath)
				}
			}
		}
	}

	// 3. Convert results...
	finalResults := make(map[string][]int)
	for id, hosts := range hostMap {
		for h := range hosts {
			finalResults[id] = append(finalResults[id], h)
		}
	}

	return finalResults, nil
}


// TODO perhaps strengthen prefix check
func (o *OsDeviceConnectivityHelperGeneric) extractHostNumber(entryName string) (int, error) {
    if strings.HasPrefix(entryName, "host") {
		return extractHostNumberInternal(strings.TrimPrefix(entryName, "host"))
    }
    // Handle both "rport-" and "remote_port-"
    if idx := strings.Index(entryName, "-"); idx != -1 {
        idPart := entryName[idx+1:]
        if colonIdx := strings.Index(idPart, ":"); colonIdx != -1 {
			return extractHostNumberInternal(idPart[:colonIdx])
        }
    }
    return 0, fmt.Errorf("unknown host format: %s", entryName)
}

func (o *OsDeviceConnectivityHelperGeneric) extractHostNumberInternal(entryName string) (int, error) {
	hostNumber, err := strconv.Atoi(entryName)
	if (err != nil) {
		return 0, fmt.Warningf("Host number in for target file was not valid : {%v}", entryName)

	}
	return hostNumber, nil
}



func (o *OsDeviceConnectivityHelperGeneric) RescanHosts(hostIDs []int) error {
	var errs []error
	for _, id := range hostIDs {
		scanPath := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", id)

		// Writing "- - -" triggers a full scan of all channels, targets, and LUNs
		err := os.WriteFile(scanPath, []byte("- - -"), 0644)
		if err != nil {
			logger.Errorf("Failed to rescan host %d: %v", id, err)
			errs = append(errs, err)
			continue
		}
		logger.Infof("Successfully triggered rescan for host %d", id)
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to rescan %d hosts", len(errs))
	}
	return nil
}

func (o OsDeviceConnectivityHelperGeneric) GetMpathVolumeId(dmPath string) {
	SgInqWwn, err := o.GetWwnByScsiInq(dmPath)
	if err != nil {
			return "", err
	}
	return SgInqWwn
}



// SgIoHeader matches the C struct sg_io_hdr_t for Linux ioctl
type SgIoHeader struct {
	InterfaceID    int32
	DxferDirection int32
	CmdLen         uint8
	MxSbpLen       uint8
	IovecCount     uint16
	DxferLen       uint32
	Dxferp         uintptr
	Cmdp           uintptr
	Sbp            uintptr
	Timeout        uint32
	Flags          uint32
	PackID         int32
	UsrPtr         uintptr
	Status         uint8
	MaskedStatus   uint8
	MsgStatus      uint8
	SbLenIv        uint8
	HostStatus     uint16
	DriverStatus   uint16
	Resid          int32
	Duration       uint32
	Info           uint32
}

// TODO  Apply open fallback from pq1
func (o OsDeviceConnectivityHelperGeneric) GetWwnByScsiInq(dev string) ([]string, error) {
	if o.willIoctl0x83Fail(filepath.Base(dev)) {
		return nil, fmt.Errorf("path %s in unsafe state", dev)
	}
	// 1. Try O_RDONLY first (The "Clean" way)
	fd, err := syscall.Open(dev, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)

	// 2. If EACCES or EPERM, try O_RDWR (The "Privileged" way)
	// Some RHEL 7 drivers require Write bits to allow ANY SG_IO ioctl.
	if err != nil && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		fd, err = syscall.Open(dev, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		// If the device is gone/dead, we want to know immediately
		return nil, err
	}
	defer f.Close()

	// SCSI INQUIRY CDB: 12h=Cmd, 01h=EVPD, 83h=DeviceIDPage, 00h=Res, FFh=Len, 00h=Ctrl
	cdb := [6]byte{0x12, 0x01, 0x83, 0x00, 0xFF, 0x00}
	respBuf := make([]byte, 256)
	senseBuf := make([]byte, 32)

	// TODO TimeOutSgInqCmd should be 2 seconds

	header := SgIoHeader{
		InterfaceID:    'S',
		DxferDirection: SG_DXFER_FROM_DEV,
		CmdLen:         uint8(len(cdb)),
		MxSbpLen:       uint8(len(senseBuf)),
		DxferLen:       uint32(len(respBuf)),
		Dxferp:         uintptr(unsafe.Pointer(&respBuf[0])),
		Cmdp:           uintptr(unsafe.Pointer(&cdb[0])),
		Sbp:            uintptr(unsafe.Pointer(&senseBuf[0])),
		Timeout:        uint32(TimeOutSgInqCmd.Milliseconds()),
	}

	maxRetries := 2
	for i := 0; i < maxRetries; i++ {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), SG_IO, uintptr(unsafe.Pointer(&header)))
		if errno != 0 {
			return nil, fmt.Errorf("ioctl failed: %v", errno)
		}

		// 1. Check for Host/HBA Errors (No point in retrying if the cable is pulled)
		if header.HostStatus != 0 {
			return nil, fmt.Errorf("SCSI host error: 0x%04x", header.HostStatus)
		}

		// 2. Check for Check Condition (0x02)
		if header.Status == 0x02 {
			// Sense data is usually in senseBuf. Sense Key is byte 2 (offset 2)
			// in fixed format sense data.
			senseKey := senseBuf[2] & 0x0f
			if senseKey == 0x06 { // 0x06 = UNIT ATTENTION
				logger.Infof("Unit Attention detected on %s, retrying...", dev)
				continue // Try again, the UA is now cleared
			}
			return nil, fmt.Errorf("SCSI Check Condition: SenseKey 0x%02x", senseKey)
		}

		// 3. Status is 0 (Good) - break and process result
		if header.Status == 0 {
			break
		}

		return nil, fmt.Errorf("Unexpected SCSI status: 0x%02x", header.Status)
	}

	actualLen := int(header.DxferLen) - int(header.Resid)

    if actualLen < 4 {
        return nil, fmt.Errorf("response too short")
    }
	// respBuf[1] is the Page Code. It should be 0x83.
	if respBuf[1] != 0x83 {
		return nil, fmt.Errorf("unexpected VPD page: 0x%02x", respBuf[1])
	}

	return parseVPD83(respBuf[:actualLen])
}

func (r *OsDeviceConnectivityHelperScsiGeneric) willIoctl0x83Fail(sgName string) bool {
    statePath := fmt.Sprintf("/sys/class/scsi_generic/%s/device/state", sgName)
    state, err := os.ReadFile(statePath)
    if err != nil {
        return true
    }

    s := strings.TrimSpace(string(state))
    switch s {
    case "running":
        return false // Best case for success
    case "blocked", "quiesce", "offline", "transport-offline", "deleting", "cancel":
        return true
    default:
        // Any unknown state should be treated as a failure for safety
        return true
    }
}
//blocked: Occurs during error recovery (e.g., a Fibre Channel rport is lost). The SCSI mid-layer queues all I/O, including SG_IO ioctls. Even with O_NONBLOCK, the ioctl call itself can block in the kernel until the timeout (dev_loss_tmo) expires.
//quiesce: Used when a device is being suspended or during certain driver-level resets. The device is temporarily not accepting commands.
//offline: The kernel has already determined the device is unusable after failed error recovery. Most ioctls will return an immediate -ENXIO (No such device or address) or -EIO (I/O error).
//transport-offline: Similar to offline but specifically indicates the transport layer (SAS/FC) has severed the link
//deleting/cancel - kernel is actively tearing down the device structures, and attempting an ioctl here is unreliable.


func (o *OsDeviceConnectivityHelperScsiGeneric) parseVPD83(data []byte) ([]string, error) {
	// 1. Initial boundary check
	if len(data) < 4 {
		return nil, fmt.Errorf("invalid VPD data: buffer too short")
	}

	// 2. Determine the true limit based on the header vs actual bytes read
	pageLen := int(binary.BigEndian.Uint16(data[2:4]))
	headerLimit := 4 + pageLen

	// The "True Limit" is the smaller of:
	// - The header's reported length + 4 bytes of header
	// - The actual number of bytes the kernel wrote to the buffer (actualLen)
	limit := len(data)
	if headerLimit < limit {
		limit = headerLimit
	}

	cursor := 4
	var candidates []string

	// 3. Iterate through designators with safety checks
	for cursor+4 <= limit {
		// Byte 1: [PIV (7) | Association (5:4) | Designator Type (3:0)]
		designatorType := int(data[cursor+1] & 0x0F)
		association := (data[cursor+1] >> 4) & 0x03

		// Byte 3 is the length of the specific designator data
		length := int(data[cursor+3])

		idStart := cursor + 4
		idEnd := idStart + length

		// Safety check: Does this designator exceed our buffer limit?
		if idEnd > limit {
			logger.Warningf("VPD 83 designator at offset %d truncated by buffer limit", cursor)
			break
		}

		// Only Association 0 (Logical Unit) is relevant for Volume WWIDs
		if association == 0 {
			idData := data[idStart:idEnd]

			switch designatorType {
			case 1, 2, 3: // T10, EUI-64, or NAA
				// Prepend type for udev-style compatibility (e.g., "3" + hex_data)
				candidates = append(candidates, fmt.Sprintf("%d%x", designatorType, idData))
			case 8: // SCSI Name String
				candidates = append(candidates, strings.ToLower(strings.TrimSpace(string(idData))))
			}
		}

		// Advance to next designator
		cursor += 4 + length
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no Association 0 identifiers found in VPD 83")
	}
	return candidates, nil
}


// Wrapper for 0x83 query
func (o *OsDeviceConnectivityHelperGeneric) GetSafeWwn(dev string) ([]string, error) {
    // 1. Isolate by WWID (if known) or Device Name to prevent "thundering herd"
    return o.resourceManager.ExecuteGlobalScsi(dev, func() error {
        // 2. Perform the Sysfs State Guard
        if o.willIoctl0x83Fail(filepath.Base(dev)) {
            return fmt.Errorf("device-safety: path %s is in unsafe state", dev)
        }
        return o.GetWwnByScsiInq(dev)
    })
}



func (o *OsDeviceConnectivityHelperGeneric) NormalizeDmVolumeIdentifier(filename string) string {
	// 1. Initial cleanup
	id := strings.ToLower(strings.TrimSpace(filename))

	// 2. Handle DM-specific UUID format
	// Filenames in /dev/mapper/ can be aliases (mpatha),
	// but the underlying UUIDs (found in /sys/block/dm-X/uuid)
	// often look like: "mpath-3600601..." or "dm-uuid-mpath-3600601..."
	// prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "pci-", "nvme-", "naa.", "eui.", "wwn-0x", "wwn-"}
	prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "wwn-0x", "wwn-"}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			id = strings.TrimPrefix(id, p)
			break
		}
	}

	// 3. IQN/NQN Check (Network targets don't follow hex rules)
	if strings.HasPrefix(id, "iqn.") || strings.HasPrefix(id, "nqn.") {
		return id
	}

	// 4. Hex-only filter for standard WWIDs
	var b strings.Builder
	for _, r := range id {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	cleanID := b.String()

	// 5. The "Type Digit" Check
	// Your parseVPD83 returns "[type][hex]" (e.g. "3600...").
	// Linux dm-uuid usually includes that '3' (for NAA) automatically.
	// If it's 32 chars and lacks the prefix, we assume it's a raw NAA-6 hex string.
	if len(cleanID) == 32 && !strings.HasPrefix(cleanID, "3") {
		return "3" + cleanID
	}

	// If it's 16 chars and starts with '5', it's an NAA-5 (standard for many arrays).
	// We keep it as-is because udev and multipathd treat '5...' as the primary key.

	return cleanID
}


func (o *OsDeviceConnectivityHelperGeneric) NormalizeOsVolumeIdentifier(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))

	// 1. Convert udev-style hints to SCSI Type Digits
	if strings.HasPrefix(id, "naa.") {
		return "3" + strings.TrimPrefix(id, "naa.")
	}
	if strings.HasPrefix(id, "eui.") {
		return "2" + strings.TrimPrefix(id, "eui.")
	}

	// 2. Handle standard DM/Multipath prefixes
	prefixes := []string{"dm-uuid-mpath-", "mpath-", "scsi-", "wwn-0x", "wwn-"}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			id = strings.TrimPrefix(id, p)
			break
		}
	}

	for _, r := range id {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	cleanID := b.String()

    return cleanID
}


func (o *OsDeviceConnectivityHelperGeneric) MatchVolumeWWID(targetWWID string, candidates []string) bool {
	// Normalize target: lowercase and strip '0x' or 'scsi-' prefixes
	target := strings.ToLower(strings.TrimSpace(targetWWID))
	target = strings.TrimPrefix(target, "scsi-")
	target = strings.TrimPrefix(target, "0x")

	// Prepare a version for numeric comparison (strip leading zeros)
	targetBody := strings.TrimLeft(target, "0")

	for _, candidate := range candidates {
		// 1. Direct match (covers Type 8 / SCSI Name Strings and exact hex matches)
		if candidate == target {
			return true
		}

		// 2. Handle prefixed hex strings from your parseVPD83 (Types 1, 2, 3)
		if len(candidate) > 1 {
			prefix := candidate[0]
			if prefix == '1' || prefix == '2' || prefix == '3' {
				// Strip the type digit, then strip leading zeros
				candidateBody := strings.TrimLeft(candidate[1:], "0")

				// If the 'clean' hex bodies match, it's a hit.
				// This matches target "123" with candidate "20000123"
				if candidateBody == targetBody && targetBody != "" {
					return true
				}
			}
		}
	}
	return false
}


func (o *OsDeviceConnectivityHelperGeneric) GetMpathdOutputForVolume(volumeIdVariations []string,
	multipathdCommandFormatArgs []string) (string, error) {
	mpathdOutput, err := o.Helper.WaitForDmToExist(volumeIdVariations, WaitForMpathRetries,
		WaitForMpathWaitIntervalSec)
	if err != nil {
		return "", err
	}
	return mpathdOutput, nil
}


// GetMpathDeviceName identifies the underlying DM device for a given path.
// It uses an O(1) stat check for block devices and falls back to /proc/self/mountinfo.
// It's quicker to parse than /proc/mounts
// It also translates aliases to DM names
func (o *OsDeviceConnectivityHelperGeneric) GetMpathDeviceName(volumePath string) (string, error) {
	var stat syscall.Stat_t
	// Tier 1: Get Device ID directly from the volume path inode
	if err := syscall.Stat(volumePath, &stat); err != nil {
		return "", fmt.Errorf("failed to stat path %s: %w", volumePath, err)
	}

	// Use Rdev for the underlying device ID
	major := unix.Major(uint64(stat.Dev))
	minor := unix.Minor(uint64(stat.Dev))

	// Tier 2: High-Speed Sysfs Resolution (Bulletproof DM lookup)
	if major > 0 {
		// This resolves aliases, symlinks, and raw paths to the kernel name (dm-N)
		if kernelName, err := o.resolveIdToKernelName(major, minor); err == nil {
			return kernelName, nil
		}
	}

	// Tier 3: Fallback - Parse mountinfo (for Bind Mounts, NFS, or complex overlays)
	deviceName, err := o.getDeviceFromMountInfo(volumePath)
	if err != nil {
		return "", err
	}

	// If mountinfo gave us an alias (e.g. "mpatha"), resolve it one last time to be sure
	return o.ResolveToKernelName(deviceName)
}

// resolveIdToKernelName performs the sysfs symlink resolution
func (o *OsDeviceConnectivityHelperGeneric) resolveIdToKernelName(major, minor uint32) (string, error) {
	sysPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	realPath, err := os.Readlink(sysPath)
	if err != nil {
		return "", err
	}
	// Returns canonical kernel name: "dm-5", "sda", etc.
	return filepath.Base(realPath), nil
}

func (o *OsDeviceConnectivityHelperGeneric) ResolveToKernelName(deviceName string) (string, error) {
	// If it's already a dm name, we're done
	if strings.HasPrefix(deviceName, "dm-") {
		return deviceName, nil
	}

	// Construct full path for stat
	devPath := deviceName
	if !strings.HasPrefix(devPath, "/dev/") {
		devPath = filepath.Join("/dev/mapper", deviceName)
		if _, err := os.Stat(devPath); err != nil {
			devPath = filepath.Join("/dev", deviceName)
		}
	}

	var stat syscall.Stat_t
	if err := syscall.Stat(devPath, &stat); err != nil {
		return deviceName, nil // Fallback to original if stat fails
	}

	return o.resolveIdToKernelName(unix.Major(uint64(stat.Rdev)), unix.Minor(uint64(stat.Rdev)))
}


// findDMByWWID helper to map a WWID to a /dev/mapper name (e.g., mpatha)
func (o *OsDeviceConnectivityHelperGeneric) findDMByWWID(wwid string) string {
	files, err := os.ReadDir("/dev/mapper")
	if err != nil {
		return ""
	}

	for _, file := range files {
		name := file.Name()
		if name == "control" {
			continue
		}

		// 1. Resolve /dev/mapper/name to its kernel dm-X name
		// Aliases like 'mpatha' are symlinks to ../dm-X
		fullPath := filepath.Join("/dev/mapper", name)
		realPath, err := filepath.EvalSymlinks(fullPath)
		if err != nil {
			continue
		}
		dmKernelName := filepath.Base(realPath) // e.g., "dm-5"

		// 2. Read the UUID from sysfs
		// The UUID in dm/uuid follows the format "mpath-<WWID>"
		uuidPath := fmt.Sprintf("/sys/block/%s/dm/uuid", dmKernelName)
		content, err := os.ReadFile(uuidPath)
		if err == nil {
			// Multipath UUIDs are prefixed with 'mpath-'
			// We use strings.Contains to handle different prefix conventions (mpath, part, etc.)
			if strings.Contains(strings.ToLower(string(content)), strings.ToLower(wwid)) {
				return name // Return the alias (e.g. mpatha) for multipathd commands
			}
		}
	}
	return ""
}

// GetMountWWID retrieves the unique hardware identifier (WWID or DM UUID)
// for the block device currently mounted at targetPath.
func (o *OsDeviceConnectivityHelperGeneric) GetMountWWID(targetPath string) (string, error) {
	var st unix.Stat_t
	// 1. Stat (not Lstat) to follow symlinks to the actual mount
	if err := unix.Stat(targetPath, &st); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("identity-check: path %s does not exist", targetPath)
		}
		return "", fmt.Errorf("identity-check: failed to stat %s: %w", targetPath, err)
	}

	major, minor := int(unix.Major(st.Dev)), int(unix.Minor(st.Dev))

	var wwid string
	// Gated execution: Read from sysfs can block if the device is in error recovery
	err := c.resourceManager.ExecuteUninterruptible(
		fmt.Sprintf("id-check-%d:%d", major, minor),
		5, 20, 1*time.Second, 5*time.Second,
		func() error {
			var innerErr error
			wwid, innerErr = c.getWWIDByDev(major, minor)
			return innerErr
		},
	)

	return c.normalizeWWID(wwid), err
}
// Corresponding:
func (o *OsDeviceConnectivityHelperGeneric) normalizeWWID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	// Remove common prefixes
	prefixes := []string{"mpath-", "uuid.", "naa.", "nvme."}
	for _, p := range prefixes {
		raw = strings.TrimPrefix(raw, p)
	}
	return raw
}


func (o *OsDeviceConnectivityHelperGeneric) getWWIDByDev(major, minor int) (string, error) {
	basePath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)

	// Order of operations: DM -> NVMe -> SCSI
	// 1. Device Mapper (Multipath) - Most common for IBM Block
	if uuid, err := os.ReadFile(filepath.Join(basePath, "dm/uuid")); err == nil {
		return strings.TrimSpace(string(uuid)), nil
	}

	// 2. NVMe (Standardized in modern kernels)
	if wwid, err := os.ReadFile(filepath.Join(basePath, "wwid")); err == nil {
		return strings.TrimSpace(string(wwid)), nil
	}

	// 3. SCSI (Legacy/Standard)
	if wwid, err := os.ReadFile(filepath.Join(basePath, "device/wwid")); err == nil {
		return strings.TrimSpace(string(wwid)), nil
	}

	return "", fmt.Errorf("identity-check: could not resolve WWID for device %d:%d at %s", major, minor, basePath)
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

 func convertScsiIdToNguid(scsiId string) string {
        logger.Infof("Converting scsi uuid : %s to nguid", scsiId)
        oui := scsiId[1:WwnOuiEnd]
        vendorIdentifier := scsiId[WwnOuiEnd:WwnVendorIdentifierEnd]
        vendorIdentifierExtension := scsiId[WwnVendorIdentifierEnd:]
        finalNguid := vendorIdentifierExtension + oui + "0" + vendorIdentifier
        logger.Infof("Nguid is : %s", finalNguid)
        return finalNguid
}


func (o GetDmsPathHelperGeneric) WaitForDmToExist(volumeWWID string, maxRetries int, intervalSeconds int) (string, error) {
	for i := 0; i < maxRetries; i++ {
		devicePath, err := o.FetchDeviceByWWID(volumeWWID)
		if err == nil {
			// Found a path, now validate it is healthy and not a ghost
			return o.validateDMIntegrity(devicePath)
		}

		logger.Debugf("Attempt %d/%d: %v", i+1, maxRetries, err)
		time.Sleep(time.Second * time.Duration(intervalSeconds))
	}
	return "", &MultipathDeviceNotFoundForVolumeError{volumeWWID}
}


// FetchDeviceByWWID finds a healthy block device for the given volume ID.
func (o GetDmsPathHelperGeneric) FetchDeviceByWWID(volumeWWID string) (string, error) {
    // Wrap the entire discovery in a global limit to prevent
    // multiple concurrent sysfs walks from saturating the kernel.
    var dev string
    err := o.resourceManager.ExecuteUninterruptible("global-discovery", 2, 0, 0, 45*time.Second, func() error {
        var innerErr error
        dev, innerErr = o.performDiscovery(volumeWWID)
        return innerErr
    })
    return dev, err
}

func (o GetDmsPathHelperGeneric) performDiscovery(volumeWWID string) (string, error) {
	normalizedWWID := strings.ToLower(strings.ReplaceAll(volumeWWID, "-", ""))

	// 1. STRATEGY A: DM-Multipath (SCSI or NVMe via DM)
	// Check udev shortcut first (O(1))
	dmPath := fmt.Sprintf("/dev/disk/by-id/dm-uuid-mpath-%s", normalizedWWID)
	if dev, err := verifyDevice(dmPath); err == nil {
		return dev, nil
	}

	// Fallback: Scan DM list in sysfs (O(N_dm))
	// Catches cases where udev is slow or stale
	if dev, err := scanDMSubsystem(normalizedWWID); err == nil {
		return dev, nil
	}

	// 2. STRATEGY B: Native NVMe (NVMe-oF / TCP / RDMA)
	// Check udev shortcut: /dev/disk/by-id/nvme-<uuid>
	nvmePath := fmt.Sprintf("/dev/disk/by-id/nvme-%s", normalizedWWID)
	if dev, err := verifyDevice(nvmePath); err == nil {
		return dev, nil
	}

	// Fallback: Scan NVMe subsystems (O(N_nvme))
	if dev, err := scanNVMeSubsystem(normalizedWWID); err == nil {
		return ev, nil
	}

	// Fallback: Scan all SCSI blocks for WWID match
	if dev, err := scanSCSISubsystem(normalizedWWID); err == nil {
		return dev, nil
	}

	return nil, fmt.Errorf("device with WWID %s not found after exhaustive scan", volumeWWID)
}



// verifyDevice ensures the link exists and returns the canonical path
func (o GetDmsPathHelperGeneric) verifyDevice(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	// Crucial for CSI: Resolve /dev/mapper/mpatha to /dev/dm-X
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return realPath, nil
}


// scanDMSubsystem finds the DM device using robust normalization.
func (o GetDmsPathHelperGeneric) scanDMSubsystem(targetID string) (string, error) {
	matches, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
	target := normalize(targetID)

	for _, m := range matches {
		if content, err := os.ReadFile(m); err == nil {
			// normalize() strips 'mpath-', handles case, and removes newlines
			if normalize(string(content)) == target {
				// Safely get 'dm-X' regardless of path depth
				dmName := filepath.Base(filepath.Dir(filepath.Dir(m)))
				return filepath.Join("/dev", dmName), nil
			}
		}
	}
	return "", fmt.Errorf("dm device not found")
}
// TODO corresponding}
func (o GetDmsPathHelperGeneric) normalize(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "mpath-")
	s = strings.TrimPrefix(s, "naa.")
	s = strings.TrimPrefix(s, "uuid.")
	s = strings.TrimPrefix(s, "nvme.")
	return strings.ReplaceAll(s, "-", "")
}



func (o GetDmsPathHelperGeneric) scanNVMeSubsystem(targetID string) (string, error) {
	// 1. Look for NVMe namespaces in sysfs
	// /sys/block/nvme0n1, /sys/block/nvme1n1, etc.
	matches, _ := filepath.Glob("/sys/block/nvme*n*")
	target := o.normalizeWWID(targetID)

	for _, m := range matches {
		// 2. Identify the Namespace ID (UUID or NGUID)
		var foundID string

		// Check NGUID first (Common for FlashSystem/SVC)
		if nguid, err := os.ReadFile(filepath.Join(m, "nguid")); err == nil {
			foundID = o.normalizeWWID(string(nguid))
		}

		// Check UUID if NGUID didn't match
		if foundID != target {
			if uuid, err := os.ReadFile(filepath.Join(m, "uuid")); err == nil {
				foundID = o.normalizeWWID(string(uuid))
			}
		}

		// 3. If we have a match, resolve the path
		if foundID == target {
			devName := filepath.Base(m)

			// 4. Handle Native Multipath (NVMe Head)
			// If this is a private path (e.g. nvme0n1) and there is a
			// multipath head (e.g. nvme-subsys0n1), we want the head.
			subsysPath := filepath.Join(m, "subsystem")
			if subsys, err := os.Readlink(subsysPath); err == nil {
				subsysName := filepath.Base(subsys) // e.g. "nvme-subsys0"

				// Search for the shared head node
				headPattern := fmt.Sprintf("/dev/%sn*", subsysName)
				heads, _ := filepath.Glob(headPattern)
				for _, h := range heads {
					// Verify this head node is actually a block device
					if _, err := os.Stat(h); err == nil {
						logger.Infof("Found NVMe multipath head for %s: %s", devName, h)
						return h, nil
					}
				}
			}

			// Fallback: return the direct device path (e.g. /dev/nvme0n1)
			return filepath.Join("/dev", devName), nil
		}
	}
	return "", fmt.Errorf("NVMe device with WWID %s not found", targetID)
}


// scanSCSISubsystem finds raw /dev/sdX devices.
func (o GetDmsPathHelperGeneric) scanSCSISubsystem(targetID string) (string, error) {
	matches, _ := filepath.Glob("/sys/block/sd*/device/wwid")
	target := normalize(targetID)

	for _, m := range matches {
		if content, err := os.ReadFile(m); err == nil {
			// SCSI wwid files often contain 'naa.' prefixes
			if strings.Contains(normalize(string(content)), target) {
				// /sys/block/sdX/device/wwid -> sdX is 3 levels up from wwid,
				// or 2 levels up from device.
				sdName := filepath.Base(filepath.Dir(filepath.Dir(m)))
				return filepath.Join("/dev", sdName), nil
			}
		}
	}
	return "", fmt.Errorf("scsi device not found")
}


// Helper to extract the core serial/WWID
func (o GetDmsPathHelperGeneric) normalizeWWID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	// Remove common sysfs prefixes to match dm-uuid formatting
	prefixes := []string{"t10.", "naa.", "eui."}
	for _, p := range prefixes {
		raw = strings.TrimPrefix(raw, p)
	}
	return raw
}


func (o GetDmsPathHelperGeneric) getWWIDByDevName(devName string) (string, error) {
	// Modern Linux kernels (4.x+) expose the WWID in sysfs for SCSI devices
	wwidPath := fmt.Sprintf("/sys/block/%s/device/wwid", devName)
	data, err := os.ReadFile(wwidPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}


func (o GetDmsPathHelperGeneric) validateDMIntegrity(dmPath string) (string, error) {
    dmName := filepath.Base(dmPath)
    slavesPath := fmt.Sprintf("/sys/block/%s/slaves", dmName)

    slaves, err := os.ReadDir(slavesPath)
    if err != nil || len(slaves) == 0 {
        return "", fmt.Errorf("dm device %s has no active slaves", dmName)
    }

    // Optional: Check if at least one slave is 'running'
    for _, s := range slaves {
        state, _ := os.ReadFile(fmt.Sprintf("/sys/block/%s/device/state", s.Name()))
        if strings.TrimSpace(string(state)) == "running" {
            return dmPath, nil
        }
    }

    return "", fmt.Errorf("dm device %s has slaves but none are in 'running' state", dmName)
}


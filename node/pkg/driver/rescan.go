package driver

import (
	"fmt"
	"k8s.io/klog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:generate mockgen -destination=../../mocks/mock_rescan_utils.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver RescanUtilsInterface

type RescanUtilsInterface interface {
	RescanSpecificLun(Lun int, array_iqn string) error
	GetMpathDevice(lunId int, array_iqn string) (string, error)
	FlushMultipathDevice(mpathDevice string) error
	RemoveIscsiDevice(sysDevices []string) error
}

type RescanUtilsIscsi struct {
	nodeUtils NodeUtilsInterface
	executor  ExecutorInterface
}

type NewRescanUtilsFunction func(connectivityType string, nodeUtils NodeUtilsInterface, executor ExecutorInterface) (RescanUtilsInterface, error)

func NewRescanUtils(connectivityType string, nodeUtils NodeUtilsInterface, executor ExecutorInterface) (RescanUtilsInterface, error) {
	klog.V(5).Infof("NewRescanUtils was called with connectivity type: %v", connectivityType)
	switch connectivityType {
	case "iscsi":
		return &RescanUtilsIscsi{nodeUtils: nodeUtils, executor: executor}, nil
	default:
		return nil, fmt.Errorf(ErrorUnsupportedConnectivityType, connectivityType)
	}
}

func (r RescanUtilsIscsi) RescanSpecificLun(lunId int, array_iqn string) error {
	klog.V(5).Infof("Starging Rescan specific lun, on lun : {%v}, with array iqn : {%v}", lunId, array_iqn)
	sessionHosts, err := r.nodeUtils.GetIscsiSessionHostsForArrayIQN(array_iqn)
	if err != nil {
		return err
	}

	for _, hostNumber := range sessionHosts {

		filename := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", hostNumber)
		f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200)
		if err != nil {
			klog.Errorf("could not open filename : {%v}. err : {%v}", filename, err)
			return err
		}

		defer f.Close()

		scanCmd := fmt.Sprintf("0 0 %d", lunId)
		klog.V(5).Infof("Rescan host device : echo %s > %s", scanCmd, filename)
		if written, err := f.WriteString(scanCmd); err != nil {
			klog.Errorf("could not write to file :{%v}, error : {%v}", filename, err)
			return err
		} else if written == 0 {
			klog.Errorf("nothing was written to file : {%v}", filename)
			return fmt.Errorf(ErrorNothingWasWrittenToScanFile, filename)
		}

	}

	klog.V(5).Infof("finsihed rescan lun on lun id : {%v}, with array iqn : {%v}", lunId, array_iqn)
	return nil

}

func (r RescanUtilsIscsi) GetMpathDevice(lunId int, array_iqn string) (string, error) {
	var devicePaths []string

	devicePath := strings.Join([]string{"/dev/disk/by-path/ip*", "iscsi", array_iqn, "lun", strconv.Itoa(lunId)}, "-")
	klog.V(4).Infof("device path is : {%v}", devicePath)

	devicePaths, exists, err := waitForPathToExist(devicePath, 5, 1)
	if !exists {
		klog.V(4).Infof("return error because file was not found")
		return "", fmt.Errorf("could not find path")
	}
	if err != nil {
		klog.V(4).Infof("founr error : %v ", err.Error())
		return "", err
	}

	devicePathTosysfs := make([]string, len(devicePaths))

	if err != nil {
		return "", err
	}
	if len(devicePaths) < 1 {
		return "", fmt.Errorf("failed to find device path: %s", devicePath)
	}

	// Looping over the physical devices of the volume - /dev/sdX (multiple since its with multipathing)
	for i, path := range devicePaths {
		if path != "" {
			if mappedDevicePath, err := getMultipathDisk(path); mappedDevicePath != "" {
				devicePathTosysfs[i] = mappedDevicePath
				if err != nil {
					return "", err
				}
			}
		}
	}
	klog.V(4).Infof("After connect we're returning devicePaths: %s", devicePathTosysfs)
	if len(devicePathTosysfs) > 0 {
		return devicePathTosysfs[0], err
		// TODO consider to validate that all the devicePathTosysfs are the same DM device, and if not maybe raise Error
	}
	return "", err

}

//return waitForPathToExistImpl(devicePath, maxRetries, intervalSeconds, deviceTransport, os.Stat, filepath.Glob)

func waitForPathToExist(devicePath string, maxRetries int, intervalSeconds int) ([]string, bool, error) {

	var err error
	for i := 0; i < maxRetries; i++ {
		err = nil
		fpaths, _ := filepath.Glob(devicePath)
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

func getMultipathDisk(path string) (string, error) {
	// Follow link to destination directory
	klog.V(5).Infof("Getting multipaht disk")
	devicePath, err := os.Readlink(path)
	if err != nil {
		klog.V(4).Infof("Failed reading link for multipath disk: %s. error: {%s}\n", path, err.Error())
		return "", err
	}
	// Get only the physical device from /dev/disk/by-path/TARGET-iscsi-iqn:<LUNID> -> ../../sdb
	sdevice := filepath.Base(devicePath)
	// If destination directory is already identified as a multipath device,
	// just return its path
	if strings.HasPrefix(sdevice, "dm-") {
		klog.V(4).Infof("Already found multipath device: %s", sdevice)
		return path, nil
	}
	// Fallback to iterating through all the entries under /sys/block/dm-* and
	// check to see if any have an entry under /sys/block/dm-*/slaves matching
	// the device the symlink was pointing at
	dmPaths, err := filepath.Glob("/sys/block/dm-*")
	// TODO improve looping by just filepath.Glob("/sys/block/dm-*/slaves/" + sdevice) and then no loops needed below, since it will just find the device directly.

	if err != nil {
		klog.V(4).Infof("Glob error: %s", err)
		return "", err
	}
	for _, dmPath := range dmPaths {
		sdevices, err := filepath.Glob(filepath.Join(dmPath, "slaves", "*"))
		if err != nil {
			klog.V(4).Infof("Glob error: %s", err)
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
	errorMsg := fmt.Sprintf("Couldn't find dm-* path for path: %s, found non dm-* path: %s", path, devicePath)
	klog.Errorf(errorMsg)
	return "", fmt.Errorf(errorMsg)
}

func (r RescanUtilsIscsi) FlushMultipathDevice(mpathDevice string) error {
	// mpathdevice is dm-4 for example

	klog.V(5).Infof("flushing mpath device : {%v}", mpathDevice)

	klog.V(5).Infof("dmsetup :")
	//dmsetup message mpathj 0 fail_if_no_path
	_, err := r.executor.ExecuteWithTimeout(10*1000, "dmsetup", []string{"message", "/dev/" + mpathDevice, "0", "fail_if_no_path"})
	if err != nil {
		klog.Errorf("error while running dmsetup command : {%v}", err.Error())
		return err
	}

	_, err = r.executor.ExecuteWithTimeout(10*1000, "multipath", []string{"-f", "/dev/" + mpathDevice})
	if err != nil {
		klog.Errorf("error while running multipath command : {%v}", err.Error())
		return err

	}

	klog.V(5).Infof("finshed flushing mpath device : {%v}", mpathDevice)

	return nil
}

func (r RescanUtilsIscsi) RemoveIscsiDevice(sysDevices []string) error {
	// sysDevices  = sdb, sda,...
	klog.V(5).Infof("Removing iscsi device : {%v}", sysDevices)

	var (
		f   *os.File
		err error
	)

	for _, deviceName := range sysDevices {
		if deviceName == "" {
			continue
		}
		klog.V(5).Infof("opening device for device name : {%v}", deviceName)
		filename := fmt.Sprintf("/sys/block/%s/device/delete", deviceName)
		if f, err = os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200); err != nil {
			klog.Errorf("errow while opening file : {%v}. error: {%v}", filename, err.Error())
			return err
		}

		defer f.Close()

		if _, err := f.WriteString("1"); err != nil {
			klog.Errorf("eror while writing to file : {%v}. error: {%v}", filename, err.Error())
			return err // TODO: maybe we need to just swallow the error and continnue??
		}
	}
	klog.V(5).Infof("finshed removing iscsi device : {%v}", sysDevices)
	return nil
}

/*



// flushDevice flushes any outstanding I/O to all paths to a device.
func flushDevice(deviceInfo *ScsiDeviceInfo) {

	log.Debug(">>>> osutils.flushDevice")
	defer log.Debug("<<<< osutils.flushDevice")

	for _, device := range deviceInfo.Devices {
		_, err := execCommandWithTimeout("blockdev", 5, "--flushbufs", "/dev/"+device)
		if err != nil {
			// nothing to do if it generates an error but log it
			log.WithFields(log.Fields{
				"device": device,
				"error":  err,
			}).Warning("Error encountered in blockdev --flushbufs command.")
		}
	}
}


*/

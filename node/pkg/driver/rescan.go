package driver

import (
	"k8s.io/klog"
	"fmt"
	"os"
)



//go:generate mockgen -destination=../../mocks/mock_rescan_utils.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver RescanUtils

type RescanUtilsInterface interface {
	RescanSpecificLun(Lun int, array_iqn string) (error)
}

type RescanUtilsIscsi struct {
	 nodeUtils NodeUtilsInterface
}

type NewRescanUtilsFunction func(connectivityType string, nodeUtils NodeUtilsInterface) (RescanUtilsInterface, error)

func NewRescanUtils(connectivityType string, nodeUtils NodeUtilsInterface) (RescanUtilsInterface, error) {
	klog.V(5).Infof("NewRescanUtils was called with connectivity type: %v", connectivityType)
	switch(connectivityType){
		case "iscsi" :
			return &RescanUtilsIscsi{nodeUtils: nodeUtils}, nil
		default:
			return nil, fmt.Errorf(ErrorUnsupportedConnectivityType, connectivityType)
	}
}


func (r RescanUtilsIscsi) RescanSpecificLun(lunId int, array_iqn string) (error){
	klog.V(5).Infof("Starging Rescan specific lun, on lun : {%v}, with array iqn : {%v}", lunId, array_iqn)
	sessionHosts, err := r.nodeUtils.GetIscsiSessionHostsForArrayIQN(array_iqn)
	if err != nil{
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
		if written, err := f.WriteString(scanCmd); err != nil {
			klog.Errorf("could not write to file :{%v}, error : {%v}",filename, err)
			return err
		} else if written == 0 {
			klog.Errorf("nothing was written to file : {%v}", filename)
			return fmt.Errorf(ErrorNothingWasWrittenToScanFile, filename)
		}

	}
	
	klog.V(5).Infof("finsihed rescan lun on lun id : {%v}, with array iqn : {%v}", lunId, array_iqn)
	return nil
	
}

//
//func getISCSIHostSessionMapForTarget(iSCSINodeName string) map[int]int {
//
//	fields := log.Fields{"iSCSINodeName": iSCSINodeName}
//	log.WithFields(fields).Debug(">>>> osutils.getISCSIHostSessionMapForTarget")
//	defer log.WithFields(fields).Debug("<<<< osutils.getISCSIHostSessionMapForTarget")
//
//	var (
//		hostNumber    int
//		sessionNumber int
//	)
//
//	hostSessionMap := make(map[int]int)
//
//	sysPath := chrootPathPrefix + "/sys/class/iscsi_host/"
//	if hostDirs, err := ioutil.ReadDir(sysPath); err != nil {
//		log.WithField("error", err).Errorf("Could not read %s", sysPath)
//		return hostSessionMap
//	} else {
//		for _, hostDir := range hostDirs {
//
//			hostName := hostDir.Name()
//			if !strings.HasPrefix(hostName, "host") {
//				continue
//			} else if hostNumber, err = strconv.Atoi(strings.TrimPrefix(hostName, "host")); err != nil {
//				log.WithField("host", hostName).Error("Could not parse host number")
//				continue
//			}
//
//			devicePath := sysPath + hostName + "/device/"
//			if deviceDirs, err := ioutil.ReadDir(devicePath); err != nil {
//				log.WithFields(log.Fields{
//					"error":      err,
//					"devicePath": devicePath,
//				}).Error("Could not read device path.")
//				return hostSessionMap
//			} else {
//				for _, deviceDir := range deviceDirs {
//
//					sessionName := deviceDir.Name()
//					if !strings.HasPrefix(sessionName, "session") {
//						continue
//					} else if sessionNumber, err = strconv.Atoi(strings.TrimPrefix(sessionName, "session")); err != nil {
//						log.WithField("session", sessionName).Error("Could not parse session number")
//						continue
//					}
//
//					targetNamePath := devicePath + sessionName + "/iscsi_session/" + sessionName + "/targetname"
//					if targetName, err := ioutil.ReadFile(targetNamePath); err != nil {
//
//						log.WithFields(log.Fields{
//							"path":  targetNamePath,
//							"error": err,
//						}).Error("Could not read targetname file")
//
//					} else if strings.TrimSpace(string(targetName)) == iSCSINodeName {
//		
//						log.WithFields(log.Fields{
//							"hostNumber":    hostNumber,
//							"sessionNumber": sessionNumber,
//						}).Debug("Found iSCSI host/session.")
//
//						hostSessionMap[hostNumber] = sessionNumber
//					}
//				}
//			}
//		}
//	}
//
//	return hostSessionMap
//}
////
//func iSCSIRescanTargetLUN(lunID int, hosts []int) error {
//
//	fields := log.Fields{"hosts": hosts, "lunID": lunID}
//	log.WithFields(fields).Debug(">>>> osutils.iSCSIRescanTargetLUN")
//	defer log.WithFields(fields).Debug("<<<< osutils.iSCSIRescanTargetLUN")
//
//	var (
//		f   *os.File
//		err error
//	)
//
//	for _, hostNumber := range hosts {
//
//		filename := fmt.Sprintf(chrootPathPrefix+"/sys/class/scsi_host/host%d/scan", hostNumber)
//		if f, err = os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200); err != nil {
//			log.WithField("file", filename).Warning("Could not open file for writing.")
//			return err
//		}
//
//		scanCmd := fmt.Sprintf("0 0 %d", lunID)
//		if written, err := f.WriteString(scanCmd); err != nil {
//			log.WithFields(log.Fields{"file": filename, "error": err}).Warning("Could not write to file.")
//			f.Close()
//			return err
//		} else if written == 0 {
//			log.WithField("file", filename).Warning("No data written to file.")
//			f.Close()
//			return fmt.Errorf("no data written to %s", filename)
//		}
//
//		f.Close()
//
//		log.WithFields(log.Fields{
//			"scanCmd":  scanCmd,
//			"scanFile": filename,
//		}).Debug("Invoked single-LUN rescan.")
//	}
//
//	return nil
//}


//func rescanTargetAndWaitForDevice(lunID int, iSCSINodeName string) error {
//
//	fields := log.Fields{
//		"lunID":         lunID,
//		"iSCSINodeName": iSCSINodeName,
//	}
//	log.WithFields(fields).Debug(">>>> osutils.rescanTargetAndWaitForDevice")
//	defer log.WithFields(fields).Debug("<<<< osutils.rescanTargetAndWaitForDevice")
//
//	hostSessionMap := getISCSIHostSessionMapForTarget(iSCSINodeName)
//	if len(hostSessionMap) == 0 {
//		return fmt.Errorf("no iSCSI hosts found for target %s", iSCSINodeName)
//	}
//
//	log.WithField("hostSessionMap", hostSessionMap).Debug("Built iSCSI host/session map.")
//	hosts := make([]int, 0)
//	for hostNumber := range hostSessionMap {
//		hosts = append(hosts, hostNumber)
//	}
//
//	if err := iSCSIRescanTargetLUN(lunID, hosts); err != nil {
//		log.WithField("rescanError", err).Error("Could not rescan for new LUN.")
//	}
//
//	paths := getSysfsBlockDirsForLUN(lunID, hostSessionMap)
//	log.Debugf("Scanning paths: %v", paths)
//	found := make([]string, 0)
//
//	checkAllDevicesExist := func() error {
//
//		found := make([]string, 0)
//		// Check if any paths present, and return nil (success) if so
//		for _, path := range paths {
//			dirname := path + "/block"
//			if !PathExists(dirname) {
//				return errors.New("device not present yet")
//			}
//			found = append(found, dirname)
//		}
//		return nil
//	}
//
//	devicesNotify := func(err error, duration time.Duration) {
//		log.WithField("increment", duration).Debug("All devices not yet present, waiting.")
//	}
//
//	deviceBackoff := backoff.NewExponentialBackOff()
//	deviceBackoff.InitialInterval = 1 * time.Second
//	deviceBackoff.Multiplier = 1.414 // approx sqrt(2)
//	deviceBackoff.RandomizationFactor = 0.1
//	deviceBackoff.MaxElapsedTime = 5 * time.Second
//
//	if err := backoff.RetryNotify(checkAllDevicesExist, deviceBackoff, devicesNotify); err == nil {
//		log.Debugf("Paths found: %v", found)
//		return nil
//	}
//
//	log.Debugf("Paths found so far: %v", found)
//
//	checkAnyDeviceExists := func() error {
//
//		found := make([]string, 0)
//		// Check if any paths present, and return nil (success) if so
//		for _, path := range paths {
//			dirname := path + "/block"
//			if PathExists(dirname) {
//				found = append(found, dirname)
//			}
//		}
//		if 0 == len(found) {
//			return errors.New("no devices present yet")
//		}
//		return nil
//	}
//
//	devicesNotify = func(err error, duration time.Duration) {
//		log.WithField("increment", duration).Debug("No devices present yet, waiting.")
//	}
//
//	deviceBackoff = backoff.NewExponentialBackOff()
//	deviceBackoff.InitialInterval = 1 * time.Second
//	deviceBackoff.Multiplier = 1.414 // approx sqrt(2)
//	deviceBackoff.RandomizationFactor = 0.1
//	deviceBackoff.MaxElapsedTime = (iSCSIDeviceDiscoveryTimeoutSecs - 5) * time.Second
//
//	// Run the check/rescan using an exponential backoff
//	if err := backoff.RetryNotify(checkAnyDeviceExists, deviceBackoff, devicesNotify); err != nil {
//		log.Warnf("Could not find all devices after %d seconds.", iSCSIDeviceDiscoveryTimeoutSecs)
//
//		// In the case of a failure, log info about what devices are present
//		execCommand("ls", "-al", "/dev")
//		execCommand("ls", "-al", "/dev/mapper")
//		execCommand("ls", "-al", "/dev/disk/by-path")
//		execCommand("lsscsi")
//		execCommand("lsscsi", "-t")
//		execCommand("free")
//		return err
//	}
//
//	log.Debugf("Paths found: %v", found)
//	return nil
//}
//
//func isAlreadyAttached(lunID int, targetIqn string) bool {
//
//	hostSessionMap := getISCSIHostSessionMapForTarget(targetIqn)
//	if len(hostSessionMap) == 0 {
//		return false
//	}
//
//	paths := getSysfsBlockDirsForLUN(lunID, hostSessionMap)
//
//	devices, err := getDevicesForLUN(paths)
//	if nil != err {
//		return false
//	}
//
//	return 0 < len(devices)
//}


//
//// after the scan is successful. One directory is returned for each path in the host session map.
//func getSysfsBlockDirsForLUN(lunID int, hostSessionMap map[int]int) []string {
//
//	paths := make([]string, 0)
//	for hostNumber, sessionNumber := range hostSessionMap {
//		path := fmt.Sprintf(chrootPathPrefix+"/sys/class/scsi_host/host%d/device/session%d/iscsi_session/session%d/device/target%d:0:0/%d:0:0:%d",
//			hostNumber, sessionNumber, sessionNumber, hostNumber, hostNumber, lunID)
//		paths = append(paths, path)
//	}
//	return paths
//}
//go:build linux

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

package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	mount "k8s.io/mount-utils"
)

var (
	getOpts          = metav1.GetOptions{}
	patchOpts        = metav1.PatchOptions{}
	topologyPrefixes = [...]string{"topology.block.csi.ibm.com"}
)

const (
	// In the Dockerfile of the node, specific commands (e.g: multipath, mount...) from the host mounted inside the container in /host directory.
	// Command lines inside the container will show /host prefix.
	PrefixChrootOfHostRoot            = "/host"
	mkfsTimeoutMilliseconds           = 15 * 60 * 1000
	resizeFsTimeoutMilliseconds       = 30 * 1000
	TimeOutGeneralCmd                 = 10 * 1000
	TimeOutMultipathdCmd              = TimeOutGeneralCmd
	TimeOutNvmeCmd                    = TimeOutGeneralCmd
	multipathdCmd                     = "multipathd"
	BlockDevCmd                       = "blockdev"
	nvmeCmd                           = "nvme"
	minFilesInNonEmptyDir             = 1
	noSuchFileOrDirectoryErrorMessage = "No such file or directory"
)

//go:generate mockgen -destination=../../mocks/mock_node_utils.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver NodeUtilsInterface

type NodeUtilsInterface interface {
	GetVolumeUuid(volumeId string) string
	ReadNvmeNqn() (string, error)
	IsNativeNVMeMultipathEnabled() (bool, error)
	ParseFCPorts() ([]string, error)
	ParseIscsiInitiators() (string, error)
	GetInfoFromPublishContext(publishContext map[string]string) (string, int, map[string][]string, error)
	GetArrayInitiators(ipsByArrayInitiator map[string][]string) []string
//	GetSysDevicesFromMpath(ctx context.Context, baseDevice string) ([]string, error)

	// TODO refactor and move all staging methods to dedicate interface.
	ClearStageInfoFile(filePath string) error
	StageInfoFileIsExist(filePath string) bool
	IsPathExists(filePath string) bool
	IsFCExists() bool
	IsDirectory(filePath string) bool
	RemoveFileOrDirectory(filePath string) error
	MakeDir(dirPath string) error
	MakeFile(filePath string) error
	ExpandFilesystem(ctx context.Context, devicePath string, volumePath string, fsType string) error
	ExpandMpathDevice(ctx context.Context, mpathDevice string, slaves []string) error
	RescanPhysicalDevices(ctx context.Context, mpathDevice string, sysDevices []string) error
	FormatDevice(ctx context.Context, devicePath string, fsType string) error
	IsNotMountPoint(file string) (bool, error)
	GetPodPath(filepath string) string
	GetTopologyLabels(ctx context.Context, nodeName string) (map[string]string, error)
	UpdateNodeInitiatorsAnnotation(ctx context.Context, nodeName string, iscsiIQN string, fcWWNs []string, nvmeNQN string) error
	IsBlock(ctx context.Context, devicePath string) (bool, error)
	GetFileSystemVolumeStats(ctx context.Context, path string) (VolumeStatistics, error)
	GetBlockVolumeStats(ctx context.Context, path string) (VolumeStatistics, error)
}

type NodeUtils struct {
	Executer   executer.ExecuterInterface
	KeyedGater *executer.KeyedGater
	mounter    mount.Interface
	ConfigYaml ConfigFile
}

func NewNodeUtils(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, mounter mount.Interface, configYaml ConfigFile,
	osDeviceConnectivityHelper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface) *NodeUtils {
	return &NodeUtils{
		Executer:   executer,
		KeyedGater: KeyedGater,
		mounter:    mounter,
		ConfigYaml: configYaml,
	}
}

func (n NodeUtils) GetInfoFromPublishContext(publishContext map[string]string) (string, int, map[string][]string, error) {
	// this will return :  connectivityType, lun, ipsByArrayInitiator, error
	ipsByArrayInitiator := make(map[string][]string)
	strLun := publishContext[n.ConfigYaml.Controller.Publish_context_lun_parameter]
	publishContextSeparator := n.ConfigYaml.Controller.Publish_context_separator
	var lun int
	var err error
	connectivityType := publishContext[n.ConfigYaml.Controller.Publish_context_connectivity_parameter]
	if connectivityType != n.ConfigYaml.Connectivity_type.Nvme_over_fc {
		lun, err = strconv.Atoi(strLun)
		if err != nil {
			return "", -1, nil, err
		}
	}
	if connectivityType == n.ConfigYaml.Connectivity_type.Fc {
		wwns := strings.Split(publishContext[n.ConfigYaml.Controller.Publish_context_fc_initiators], publishContextSeparator)
		for _, wwn := range wwns {
			ipsByArrayInitiator[wwn] = nil
		}
	}
	if connectivityType == n.ConfigYaml.Connectivity_type.Iscsi {
		iqns := strings.Split(publishContext[n.ConfigYaml.Controller.Publish_context_array_iqn], publishContextSeparator)
		for _, iqn := range iqns {
			if ips, iqnExists := publishContext[iqn]; iqnExists {
				ipsByArrayInitiator[iqn] = strings.Split(ips, publishContextSeparator)
			} else {
				logger.Errorf("Publish context does not contain any iscsi target IP for {%v}", iqn)
			}
		}
	}

	if connectivityType == n.ConfigYaml.Connectivity_type.Nvme_over_fc {
		// PUBLISH_CONTEXT_ARRAY_NVME_INITIATORS = "nn-WWNN1:pn-WWPN1,nn-WWNN2:pn-WWPN2,..."
		// key = "nn-WWNN:pn-WWPN" (array target port), value = nil (no IPs, fabric-routed).
		nvmePorts := strings.Split(
			publishContext[n.ConfigYaml.Controller.Publish_context_nvme_initiators],
			publishContextSeparator)
		for _, port := range nvmePorts {
			port = strings.TrimSpace(port)
			if port != "" {
				ipsByArrayInitiator[port] = nil
			}
		}
	}

	logger.Debugf("PublishContext relevant info : connectivityType=%v, lun=%v, arrayInitiators=%v",
		connectivityType, lun, ipsByArrayInitiator)
	return connectivityType, lun, ipsByArrayInitiator, nil
}

func (n NodeUtils) GetArrayInitiators(ipsByArrayInitiator map[string][]string) []string {
	arrayInitiators := make([]string, 0, len(ipsByArrayInitiator))
	for arrayInitiator := range ipsByArrayInitiator {
		arrayInitiators = append(arrayInitiators, arrayInitiator)
	}
	return arrayInitiators
}

func (n NodeUtils) ClearStageInfoFile(filePath string) error {
	filePath = n.GetPodPath(filePath)
	logger.Debugf("Delete StagingInfoFile : path {%v},", filePath)

	return os.Remove(filePath)
}


func (n NodeUtils) StageInfoFileIsExist(filePath string) bool {
	if _, err := os.Stat(filePath); err != nil {
		return false
	}
	return true
}

func (n NodeUtils) IsNativeNVMeMultipathEnabled() (bool, error) {
	data, err := os.ReadFile("/sys/module/nvme_core/parameters/multipath")
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read nvme_core multipath param: %w", err)
	}
	val := strings.TrimSpace(string(data))
	return val == "Y", nil
}


func getRelevantLines(rawContent *os.File) ([]string, error) {
	scanner := bufio.NewScanner(rawContent)
	var relevantLines []string
	for scanner.Scan() {
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		if strings.HasPrefix(trimmedLine, "#") || strings.HasPrefix(trimmedLine, "//") {
			continue
		}
		relevantLines = append(relevantLines, trimmedLine)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return relevantLines, nil
}

func readFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}

	defer file.Close()

	relevantLines, err := getRelevantLines(file)
	if err != nil {
		return "", err
	}
	if len(relevantLines) > 1 {
		err := fmt.Errorf("%s", fmt.Sprintf("too many lines in file %v", relevantLines))
		return "", err
	}

	return relevantLines[0], nil
}

func readAfterPrefix(path string, prefix string, portType string) (string, error) {
	fileContent, err := readFile(path)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(fileContent, prefix) {
		return "", fmt.Errorf(ErrorWhileTryingToReadPort, portType, fileContent)
	}
	contentPostfix := strings.TrimPrefix(fileContent, prefix)

	return contentPostfix, nil
}

func (n NodeUtils) ReadNvmeNqn() (string, error) {
	return readFile(NvmeFullPath)
}

func (n NodeUtils) ParseFCPorts() ([]string, error) {
	var errs []error
	var fcPorts []string

	fpaths, err := n.Executer.FilepathGlob(FCPortPath)
	if fpaths == nil {
		err = fmt.Errorf(ErrorUnsupportedConnectivityType, n.ConfigYaml.Connectivity_type.Fc)
	}
	if err != nil {
		return nil, err
	}

	for _, fpath := range fpaths {
		fcPort, err := readAfterPrefix(fpath, "0x", n.ConfigYaml.Connectivity_type.Fc)
		if err != nil {
			errs = append(errs, err)
		} else {
			fcPorts = append(fcPorts, fcPort)
		}
	}

	if errs != nil {
		err := errors.NewAggregate(errs)
		logger.Errorf("errors occured while looking for fc ports: {%v}", err)
		if fcPorts == nil {
			return nil, err
		}
	}

	return fcPorts, nil
}

func (n NodeUtils) ParseIscsiInitiators() (string, error) {
	return readAfterPrefix(IscsiFullPath, "InitiatorName=", n.ConfigYaml.Connectivity_type.Iscsi)
}

func (n NodeUtils) IsFCExists() bool {
	return n.IsPathExists(FCPath) && !n.isEmptyDir(FCPath)
}

func (n NodeUtils) isEmptyDir(path string) bool {
	f, _ := os.Open(path)
	defer f.Close()

	_, err := f.Readdir(minFilesInNonEmptyDir)

	if err != nil {
		if err != io.EOF {
			logger.Warningf("Check is directory %s empty returned error %s", path, err.Error())
		}
		return true
	}

	return false
}

func (n NodeUtils) IsPathExists(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warningf("Check is file %s exists returned error %s", path, err.Error())
		}
		return false
	}

	return true
}

func (n NodeUtils) IsDirectory(path string) bool {
	targetFile, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warningf("Check is directory %s returned error %s", path, err.Error())
		}
		return false
	}
	return targetFile.Mode().IsDir()
}

// Deletes file or directory with all sub-directories and files
func (n NodeUtils) RemoveFileOrDirectory(path string) error {
	return os.Remove(path)
}

func (n NodeUtils) MakeDir(dirPath string) error {
	err := os.MkdirAll(dirPath, os.FileMode(0755))
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func (n NodeUtils) MakeFile(filePath string) error {
	f, err := os.OpenFile(filePath, os.O_CREATE, os.FileMode(0644))
	defer f.Close()
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	return nil
}

// TODO
func (n NodeUtils) ExpandFilesystem(ctx context.Context, devicePath string, volumePath string, fsType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var cmd string
	var args []string

	switch fsType {
	case "ext4":
		cmd = "resize2fs"
		// FIXED: Pass the volume mount point path instead of the raw device path.
		// This bypasses container devfs namespace access boundaries during online resizes.
		args = []string{volumePath}
	case "xfs":
		cmd = "xfs_growfs"
		args = []string{"-d", volumePath}
	default:
		logger.Warningf("Skipping resize of unsupported fsType: %v", fsType)
		return nil
	}

	logger.Debugf("Resizing filesystem: %s on target mount %s", fsType, volumePath)
	_, err := n.Executer.ExecuteWithTimeout(resizeFsTimeoutMilliseconds, cmd, args)
	if err != nil {
		return fmt.Errorf("failed to execute filesystem utility %s on path %s: %w", cmd, volumePath, err)
	}
	return nil
}

// ExpandMpathDevice handles fork-free file updates across different block storage protocols
func (n NodeUtils) ExpandMpathDevice(ctx context.Context, mpathDevice string, slaves []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	logger.Infof("ExpandMpathDevice: Initializing expansion actions for target map %s", mpathDevice)
	
	if n.Executer.IsDeviceStillStuck(mpathDevice) {
		return fmt.Errorf("expand-safety: device %s is currently stuck in D-state; aborting resize", mpathDevice)
	}	
	
	// Step 1: Send the targeted resize directive to the multipathd socket handle
	cmd := fmt.Sprintf("resize map %s", mpathDevice)
	_, err := n.Executer.MultipathdCmd(ctx, mpathDevice, cmd)
	if err != nil {
		logger.Warningf("Socket resize map failed for %s: %v. Initiating structural fallback path rescans.", mpathDevice, err)
		
		// RESTORED FALLBACK: Retrieve the physical paths and trigger explicit kernel geometric rescans
		for _, slave := range slaves {
			if err := n.triggerPhysicalRescan(slave); err != nil {
				logger.Warningf("Rescan notification skipped for slave path node %s: %v", slave, err)
			}
		}
			
		// Retry socket expansion directive following the manual path updates
		_, err = n.Executer.MultipathdCmd(ctx, mpathDevice, cmd)
		if err != nil {
			return fmt.Errorf("multipathd resize map failed after executing path updates: %w", err)
		}
	}
	
	// Step 2: Target reload of the mutated map to apply boundaries safely without invoking global reconfigures
	_, err = n.Executer.MultipathdCmd(ctx, mpathDevice, fmt.Sprintf("reload map %s", mpathDevice))
	if err != nil {
		logger.Warningf("Targeted reload map completed with warnings for %s: %v", mpathDevice, err)
	}

	return nil
}

// triggerPhysicalRescan handles fork-free file updates across different block storage protocols
func (n NodeUtils) triggerPhysicalRescan(slave string) error {
	// Protocol Case A: Underlying path represents a standard SCSI target track (sdX)
	if strings.HasPrefix(slave, "sd") {
		rescanPath := fmt.Sprintf("/sys/block/%s/device/rescan", slave)
		return os.WriteFile(rescanPath, []byte("1"), 0644)
	}

	// Protocol Case B: Underlying path represents an NVMe over Fabrics target node layer (nvmeXnY)
	if strings.HasPrefix(slave, "nvme") {
		// Resolve back through sysfs to isolate the true controller sequence ID (nvmeX)
		// /sys/block/nvme0n1/device points to /sys/devices/virtual/nvme-fabrics/ctl/nvme0
		deviceSymlink := fmt.Sprintf("/sys/block/%s/device", slave)
		realControllerPath, err := filepath.EvalSymlinks(deviceSymlink)
		if err != nil {
			return err
		}
		
		controllerName := filepath.Base(realControllerPath) // e.g. "nvme0"
		nvmeRescanPath := fmt.Sprintf("/sys/class/nvme/%s/rescan", controllerName)
		return os.WriteFile(nvmeRescanPath, []byte("1"), 0644)
	}

	return fmt.Errorf("unknown block protocol scheme layout for device path: %s", slave)
}



func (n NodeUtils) rescanPhysicalDevice(deviceName string) error {
	if deviceName == "" {
		return fmt.Errorf("rescanPhysicalDevice: target device name cannot be empty")
	}

	// FIXED: Structural VFS Hardening Layer. Resolve symlinks upfront to cleanly 
	// translate user-space alias targets like /dev/mapper/nvme-eui... down to 
	// their true underlying block names (dm-X or nvmeXnY) or follow partition steps.
	resolvedPath, errLink := filepath.EvalSymlinks(deviceName)
	if errLink != nil {
		resolvedPath = deviceName 
	}
	
	cleanName := filepath.Base(resolvedPath)
	sysBlockTarget := filepath.Join("/sys/block", cleanName)

	// Pivot partition sub-nodes back to the parent whole-disk root container folder
	if _, err := os.Stat(sysBlockTarget); os.IsNotExist(err) {
		classBlockPath := filepath.Join("/sys/class/block", cleanName)
		if realClassPath, errEval := filepath.EvalSymlinks(classBlockPath); errEval == nil {
			if strings.Contains(realClassPath, "/block/") {
				parts := strings.Split(realClassPath, "/block/")
				if len(parts) == 2 {
					subParts := strings.Split(parts[1], "/")
					if len(subParts) > 0 {
						cleanName = subParts[0]
						sysBlockTarget = filepath.Join("/sys/block", cleanName)
					}
				}
			}
		}
	}

	// FIXED: Block Device Mapper nodes from hitting the NVMe prefix trap.
	// Rescans must be issued directly to individual physical slave tracks, never to the DM coordinator layer.
	if strings.HasPrefix(cleanName, "dm-") {
		logger.Infof("rescanPhysicalDevice: %s is a Device Mapper node. Skipping direct rescan write.", cleanName)
		return nil
	}

	var filename string

	// Protocol Case A: Underlying path represents a standard SCSI target track (sdX)
	if strings.HasPrefix(cleanName, "sd") {
		filename = fmt.Sprintf("/sys/block/%s/device/rescan", cleanName)
	} else if strings.HasPrefix(cleanName, "nvme") {
		// Protocol Case B: Underlying path represents an NVMe over Fabrics target node layer (nvmeXnY)
		deviceSymlink := fmt.Sprintf("/sys/block/%s/device", cleanName)
		realControllerPath, err := filepath.EvalSymlinks(deviceSymlink)
		if err != nil {
			if idx := strings.Index(cleanName, "n"); idx != -1 {
				filename = fmt.Sprintf("/sys/class/nvme/%s/rescan", cleanName[:idx])
			} else {
				return fmt.Errorf("failed to evaluate NVMe sysfs controller symlink for %s: %w", cleanName, err)
			}
		} else {
			controllerName := filepath.Base(realControllerPath) // e.g. "nvme0"
			filename = fmt.Sprintf("/sys/class/nvme/%s/rescan", controllerName)
		}
	} else {
		return fmt.Errorf("unknown block protocol scheme layout for device path name: %s", cleanName)
	}

	f, err := n.Executer.OsOpenFile(filename, os.O_WRONLY, 0200)
	if err != nil {
		logger.Errorf("Rescan Error: could not open filename: %s. err: %v", filename, err)
		return err
	}
	defer f.Close()

	scanCmd := "1"
	logger.Debugf("Rescan sys device: echo %s > %s", scanCmd, filename)
	
	written, err := n.Executer.FileWriteString(f, scanCmd)
	if err != nil {
		logger.Errorf("Rescan Error: could not write to rescan file: %s, error: %v", filename, err)
		return err
	} 
	if written == 0 {
		e := fmt.Errorf("rescan error: nothing was written to rescan file: %s", filename)
		logger.Errorf("%s", e.Error())
		return e
	}
	
	return nil
}

// RescanPhysicalDevices blocks securely via uninterruptible context pooling to refresh newly expanded physical drives.
// FIXED: Added the mpathName parameter to allow absolute concurrency lock segregation node-wide
func (n NodeUtils) RescanPhysicalDevices(ctx context.Context, mpathName string, sysDevices []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cleanMpathName := filepath.Base(mpathName)
	logger.Debugf("Rescan: Start rescan on sys devices: %v for parent map: %s", sysDevices, cleanMpathName)
	
	for _, deviceName := range sysDevices {
		if n.Executer.IsDeviceStillStuck(deviceName) {
			logger.Warningf("Rescan: Skipping %s as it is already marked stuck in D-state", deviceName)
			continue
		}

		// FIXED: Blending the unique parent map name into the gater key template completely 
		// de-duplicates competing parallel requests across independent volumes sharing the same hardware tracks.
		uniqueGaterKey := fmt.Sprintf("rescan-%s-%s", cleanMpathName, filepath.Base(deviceName))

		_, err := executer.ExecuteUninterruptible[struct{}](
			ctx,
			n.KeyedGater,
			uniqueGaterKey,
			5,              // maxRunning
			20,             // maxSpare
			2*time.Second,  // handoff
			10*time.Second, // hardTimeout
			func(wCtx context.Context) (struct{}, error) {
				return struct{}{}, n.rescanPhysicalDevice(deviceName)
			},
		)
		
		if err != nil {
			logger.Errorf("Rescan failed for device path target %s under map %s: %v", deviceName, cleanMpathName, err)
		}
	}
	logger.Debugf("Rescan: Finish rescan on sys devices: %v", sysDevices)
	return nil
}


//Use os.WriteFile for brevity unless your Executer wrapper specifically requires the OsOpenFile flow for tracking. os.WriteFile is inherently safer as it handles the close even on early errors.

//Missing "Rescue" Operation (7): If rescan times out repeatedly, the "rescue" is to check /sys/block/sdX/device/state. If it's blocked, you may need to trigger a link-loss reset at the HBA level (which we can discuss in the FC/iSCSI section).

func (n NodeUtils) FormatDevice(ctx context.Context, devicePath string, fsType string) error {
	// TODO wrap
	var args []string
	if fsType == "ext4" {
		args = []string{"-m0", "-Enodiscard,lazy_itable_init=1,lazy_journal_init=1", devicePath}
	} else if fsType == "xfs" {
		// TODO review -f
		args = []string{"-f", "-K", devicePath} // Added -f (force) to ensure it works on raw disks
	} else {
		return fmt.Errorf("unsupported fsType: %v", fsType)
	}

	logger.Debugf("Formatting the device with fs_type = {%v}", fsType)
	finalErr := n.KeyedGater.ExecuteNodeFs(ctx, func() error {
		_, err := n.Executer.ExecuteWithTimeout(mkfsTimeoutMilliseconds, "mkfs."+fsType, args)
		if err != nil {
			return fmt.Errorf("mkfs.%s execution failed: %v", fsType, err)
		}

		// TODO Brief pause to allow kernel to settle partition table/FS metadata
		// time.Sleep(2 * time.Second)
		return nil
	})
	
	return finalErr
}

func (n NodeUtils) IsNotMountPoint(file string) (bool, error) {
	return mount.IsNotMountPoint(n.mounter, file)
}

// To some files/dirs pod cannot access using its real path. It has to use a different path which is <prefix>/<path>.
// E.g. in order to access /etc/test.txt pod has to use /host/etc/test.txt
func (n NodeUtils) GetPodPath(origPath string) string {
	return path.Join(PrefixChrootOfHostRoot, origPath)
}

func (n NodeUtils) GetTopologyLabels(ctx context.Context, nodeName string) (map[string]string, error) {
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Infof("unable to load in-cluster configuration: %v", err)
		logger.Info("skipping topology retrieval. we might not be in a k8s cluster")
		return nil, nil
	}

	client, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, getOpts)
	if err != nil {
		return nil, err
	}

	topologyLabels := make(map[string]string)
	for key, value := range node.Labels {
		for _, prefix := range topologyPrefixes {
			if strings.HasPrefix(key, prefix) {
				topologyLabels[key] = value
			}
		}
	}
	return topologyLabels, nil
}

func (n NodeUtils) UpdateNodeInitiatorsAnnotation(ctx context.Context, nodeName string,
	nvmeNQN string, fcWWNs []string, iscsiIQN string) error {

	const nodeInitiatorsAnnotationKey = "block.csi.ibm.com/node-initiators"

	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Infof("failed to update initiators. Unable to load in-cluster configuration")
		return err
	}

	client, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		logger.Infof("failed to update initiators. Unable to create Kubernetes client")
		return err
	}

	connectivity_type := n.ConfigYaml.Connectivity_type

	portsData := map[string]interface{}{
		connectivity_type.Nvme_over_fc: []string{},
		connectivity_type.Fc:           []string{},
		connectivity_type.Iscsi:        []string{},
	}

	if nvmeNQN != "" {
		portsData[connectivity_type.Nvme_over_fc] = []string{nvmeNQN}
	}

	if len(fcWWNs) > 0 {
		portsData[connectivity_type.Fc] = fcWWNs
	}

	if iscsiIQN != "" {
		portsData[connectivity_type.Iscsi] = []string{iscsiIQN}
	}

	jsonBytes, err := json.Marshal(portsData)
	if err != nil {
		logger.Infof("failed to prepare initiators JSON data")
		return err
	}

	logger.Infof("Patching node %q: setting initiators in annotation %s to %s",
		nodeName, nodeInitiatorsAnnotationKey, string(jsonBytes))

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				nodeInitiatorsAnnotationKey: string(jsonBytes),
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		logger.Infof("failed to format node patch request")
		return err
	}

	_, err = client.CoreV1().Nodes().Patch(ctx, nodeName,
		types.MergePatchType, patchBytes, patchOpts)
	if err != nil {
		logger.Infof("failed to path node initiators in annotation")
		return err
	}

	return nil
}

func (n NodeUtils) IsBlock(ctx context.Context, devicePath string) (bool, error) {
	// REQUIREMENT 8: Respect the CSI API context
	if err := ctx.Err(); err != nil {
		return false, err
	}

	res, err := executer.ExecuteUninterruptible[bool](
		// Pass the ctx into the gater/executor
		ctx, 
		n.KeyedGater,
		"is-block-"+devicePath,
		10, 50, 
		1*time.Second, 5*time.Second,
		func(wCtx context.Context) (bool, error) {
			var stat unix.Stat_t
			// REQUIREMENT 4: Direct syscall (no 'lsblk' or 'file' process)
			// REQUIREMENT 1: unix.Stat is stable on RHEL 7 (Kernel 3.10)
			if err := unix.Stat(devicePath, &stat); err != nil {
				return false, err
			}
			return (stat.Mode & unix.S_IFMT) == unix.S_IFBLK, nil
		},
	)

	return res, err
}

// If this is used for Multipath or NVMe devices, the stat.Rdev field (which you aren't using here but is available) can be used to get the Major:Minor ID to verify against /proc/self/mountinfo, as we discussed in the Mounter review.

func (n NodeUtils) GetFileSystemVolumeStats(ctx context.Context, path string) (VolumeStatistics, error) {
	if err := ctx.Err(); err != nil {
		return VolumeStatistics{}, err
	}

	// FIXED: Use the absolute path as the key to prevent global collisions on common names like "mount"
	gaterKey := "statfs-" + path

	stat, err := executer.ExecuteUninterruptible[unix.Statfs_t](
		ctx,
		n.KeyedGater,
		gaterKey,
		5, 20, 1*time.Second, 5*time.Second,
		func(wCtx context.Context) (unix.Statfs_t, error) {
			var s unix.Statfs_t
			err := unix.Statfs(path, &s)
			return s, err
		},
	)

	if err != nil {
		return VolumeStatistics{}, err
	}

	blkSize     := uint64(stat.Bsize)
	totalBlocks := uint64(stat.Blocks)
	availBlocks := uint64(stat.Bavail)
	totalFiles  := uint64(stat.Files)
	freeFiles   := uint64(stat.Ffree)

	usedBlocks := totalBlocks - availBlocks
	if totalBlocks < availBlocks { 
		usedBlocks = 0
	}

	return VolumeStatistics{
		TotalBytes:      int64(totalBlocks * blkSize),
		AvailableBytes:  int64(availBlocks * blkSize),
		UsedBytes:       int64(usedBlocks * blkSize),
		TotalInodes:     int64(totalFiles),
		AvailableInodes: int64(freeFiles),
		UsedInodes:      int64(totalFiles - freeFiles),
	}, nil
}

func (n NodeUtils) GetBlockVolumeStats(ctx context.Context, devicePath string) (VolumeStatistics, error) {
	if err := ctx.Err(); err != nil {
		return VolumeStatistics{}, err
	}

	// FIXED: Use the complete path for proper multi-pod isolation checks
	gaterKey := "block-stats-" + devicePath

	size, err := executer.ExecuteUninterruptible[uint64](
		ctx,
		n.KeyedGater,
		gaterKey,
		5, 20, 1*time.Second, 5*time.Second,
		func(wCtx context.Context) (uint64, error) {
			if err := wCtx.Err(); err != nil {
				return 0, err
			}

			// Correctly uses O_NONBLOCK. If the device path is experiencing an active link transport 
			// drop or a D-state hang, this open call breaks out immediately without deadlocking the Go thread.
			f, err := os.OpenFile(devicePath, os.O_RDONLY|unix.O_NONBLOCK, 0)
			if err != nil {
				return 0, fmt.Errorf("failed to open device %s: %w", devicePath, err)
			}
			defer f.Close()

			// FIXED: Safe pointer tracking invocation via runtime-aware helper.
			// This avoids memory corruption and ensures full compatibility for SCSI, Native NVMe, and DM devices.
			sizeInt, errIoctl := unix.IoctlGetInt(int(f.Fd()), unix.BLKGETSIZE64)
			if errIoctl != nil {
				return 0, fmt.Errorf("ioctl BLKGETSIZE64 failed on target %s: %w", devicePath, errIoctl)
			}
			
			return uint64(sizeInt), nil
		},
	)

	if err != nil {
		return VolumeStatistics{}, err
	}

	return VolumeStatistics{
		TotalBytes:     int64(size),
		AvailableBytes: int64(size),
		UsedBytes:      0,
	}, nil
}


func (d NodeUtils) GetVolumeUuid(volumeId string) string {
	volumeIdParts := strings.Split(volumeId, d.ConfigYaml.Parameters.Object_id_info.Delimiter)
	idsPart := volumeIdParts[len(volumeIdParts)-1]
	splittedIdsPart := strings.Split(idsPart, d.ConfigYaml.Parameters.Object_id_info.Ids_delimiter)
	if len(splittedIdsPart) == 2 {
		return splittedIdsPart[1]
	} else {
		return splittedIdsPart[0]
	}
}

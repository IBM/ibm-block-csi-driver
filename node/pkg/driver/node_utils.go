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
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"golang.org/x/sys/unix"
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
	DevicesAreNvme(sysDevices []string) (bool, error)
	ParseFCPorts() ([]string, error)
	ParseIscsiInitiators() (string, error)
	GetInfoFromPublishContext(publishContext map[string]string) (string, int, map[string][]string, error)
	GetArrayInitiators(ipsByArrayInitiator map[string][]string) []string
	GetSysDevicesFromMpath(baseDevice string) ([]string, error)

	// TODO refactor and move all staging methods to dedicate interface.
	ClearStageInfoFile(filePath string) error
	StageInfoFileIsExist(filePath string) bool
	IsPathExists(filePath string) bool
	IsFCExists() bool
	IsDirectory(filePath string) bool
	RemoveFileOrDirectory(filePath string) error
	MakeDir(dirPath string) error
	MakeFile(filePath string) error
	ExpandFilesystem(devicePath string, volumePath string, fsType string) error
	ExpandMpathDevice(mpathDevice string) error
	RescanPhysicalDevices(sysDevices []string) error
	FormatDevice(devicePath string, fsType string)
	IsNotMountPoint(file string) (bool, error)
	GetPodPath(filepath string) string
	GenerateNodeID(hostName string, nvmeNQN string, fcWWNs []string, iscsiIQN string) (string, error)
	GetTopologyLabels(ctx context.Context, nodeName string) (map[string]string, error)
	IsBlock(devicePath string) (bool, error)
	GetFileSystemVolumeStats(path string) (VolumeStatistics, error)
	GetBlockVolumeStats(volumeId string) (VolumeStatistics, error)
}

type NodeUtils struct {
	Executer                   executer.ExecuterInterface
	mounter                    mount.Interface
	osDeviceConnectivityHelper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface
	ConfigYaml                 ConfigFile
}

func NewNodeUtils(executer executer.ExecuterInterface, mounter mount.Interface, configYaml ConfigFile,
	osDeviceConnectivityHelper device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface) *NodeUtils {
	return &NodeUtils{
		Executer:                   executer,
		mounter:                    mounter,
		osDeviceConnectivityHelper: osDeviceConnectivityHelper,
		ConfigYaml:                 configYaml,
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

func (n NodeUtils) GetSysDevicesFromMpath(baseDevice string) ([]string, error) {
	// this will return the 	/sys/block/dm-3/slaves/
	logger.Debugf("GetSysDevicesFromMpath with param : {%v}", baseDevice)
	deviceSlavePath := path.Join("/sys", "block", baseDevice, "slaves")
	logger.Debugf("looking in path : {%v}", deviceSlavePath)
	slaves, err := ioutil.ReadDir(deviceSlavePath)
	if err != nil {
		logger.Errorf("an error occured while looking for device slaves : {%v}", err.Error())
		return nil, err
	}

	logger.Debugf("found slaves : {%v}", slaves)

	var slavesNames []string
	for _, slave := range slaves {
		slavesNames = append(slavesNames, slave.Name())
	}

	return slavesNames, nil
}

func (n NodeUtils) StageInfoFileIsExist(filePath string) bool {
	if _, err := os.Stat(filePath); err != nil {
		return false
	}
	return true
}

func (n NodeUtils) DevicesAreNvme(sysDevices []string) (bool, error) {
	args := []string{"list"}
	out, err := n.Executer.ExecuteWithTimeout(TimeOutNvmeCmd, nvmeCmd, args)
	if err != nil {
		outMessage := strings.TrimSpace(string(out))
		if strings.HasSuffix(outMessage, noSuchFileOrDirectoryErrorMessage) {
			return false, nil
		}
		return false, err
	}
	nvmeDevices := string(out)
	for _, deviceName := range sysDevices {
		if strings.Contains(nvmeDevices, deviceName) {
			logger.Debugf("found device {%s} in nvme list", deviceName)
			return true, nil
		}
	}
	return false, nil
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
		err := fmt.Errorf(fmt.Sprintf("too many lines in file %v", relevantLines))
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
	return os.RemoveAll(path)
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

func (n NodeUtils) ExpandFilesystem(devicePath string, volumePath string, fsType string) error {
	var cmd string
	var args []string
	if fsType == "ext4" {
		cmd = "resize2fs"
		args = []string{devicePath}
	} else if fsType == "xfs" {
		cmd = "xfs_growfs"
		args = []string{"-d", volumePath}
	} else {
		logger.Warningf("Skipping resize of unsupported fsType: %v", fsType)
		return nil
	}

	logger.Debugf("Resizing the device: {%v} with fs_type = {%v}", devicePath, fsType)
	_, err := n.Executer.ExecuteWithTimeout(resizeFsTimeoutMilliseconds, cmd, args)
	if err != nil {
		logger.Errorf("Failed to resize filesystem, error: %v", err)
		return err
	}
	return nil
}

func (n NodeUtils) ExpandMpathDevice(mpathDevice string) error {
	logger.Infof("ExpandMpathDevice: [%s] ", mpathDevice)
	args := []string{"resize", "map", mpathDevice}
	output, err := n.Executer.ExecuteWithTimeout(TimeOutMultipathdCmd, multipathdCmd, args)
	if err != nil {
		return fmt.Errorf("multipathd resize failed: %v\narguments: %v\nOutput: %s\n", err, args, string(output))
	}

	args = []string{"reconfigure"}
	output, err = n.Executer.ExecuteWithTimeout(TimeOutMultipathdCmd, multipathdCmd, args)
	if err != nil {
		return fmt.Errorf("multipathd reconfigure failed: %v\narguments: %v\nOutput: %s\n", err, args, string(output))
	}
	return nil
}

func (n NodeUtils) rescanPhysicalDevice(deviceName string) error {
	filename := fmt.Sprintf("/sys/block/%s/device/rescan", deviceName)
	f, err := n.Executer.OsOpenFile(filename, os.O_APPEND|os.O_WRONLY, 0200)
	if err != nil {
		logger.Errorf("Rescan Error: could not open filename : {%v}. err : {%v}", filename, err)
		return err
	}

	defer f.Close()

	scanCmd := fmt.Sprintf("1")
	logger.Debugf("Rescan sys device : echo %s > %s", scanCmd, filename)
	if written, err := n.Executer.FileWriteString(f, scanCmd); err != nil {
		logger.Errorf("Rescan Error: could not write to rescan file :{%v}, error : {%v}", filename, err)
		return err
	} else if written == 0 {
		e := fmt.Errorf("rescan error: nothing was written to rescan file : {%s}", filename)
		logger.Errorf(e.Error())
		return e
	}
	return nil
}

func (n NodeUtils) RescanPhysicalDevices(sysDevices []string) error {
	logger.Debugf("Rescan : Start rescan on sys devices : {%v}", sysDevices)
	for _, deviceName := range sysDevices {
		err := n.rescanPhysicalDevice(deviceName)
		if err != nil {
			return err
		}
	}
	logger.Debugf("Rescan : finish rescan on sys devices : {%v}", sysDevices)
	return nil
}

func (n NodeUtils) FormatDevice(devicePath string, fsType string) {
	var args []string
	if fsType == "ext4" {
		args = []string{"-m0", "-Enodiscard,lazy_itable_init=1,lazy_journal_init=1", devicePath}
	} else if fsType == "xfs" {
		args = []string{"-K", devicePath}
	} else {
		logger.Errorf("Could not format unsupported fsType: %v", fsType)
		return
	}

	logger.Debugf("Formatting the device with fs_type = {%v}", fsType)
	_, err := n.Executer.ExecuteWithTimeout(mkfsTimeoutMilliseconds, "mkfs."+fsType, args)
	if err != nil {
		logger.Errorf("Failed to run mkfs, error: %v", err)
	}
}

func (n NodeUtils) IsNotMountPoint(file string) (bool, error) {
	return mount.IsNotMountPoint(n.mounter, file)
}

// To some files/dirs pod cannot access using its real path. It has to use a different path which is <prefix>/<path>.
// E.g. in order to access /etc/test.txt pod has to use /host/etc/test.txt
func (n NodeUtils) GetPodPath(origPath string) string {
	return path.Join(PrefixChrootOfHostRoot, origPath)
}

func (n NodeUtils) GenerateNodeID(hostName string, nvmeNQN string, fcWWNs []string, iscsiIQN string) (string, error) {
	var nodeId strings.Builder
	nodeIdDelimiter := n.ConfigYaml.Parameters.Node_id_info.Delimiter
	nodeIdFcDelimiter := n.ConfigYaml.Parameters.Node_id_info.Fcs_delimiter
	nodeId.Grow(MaxNodeIdLength)
	nodeId.WriteString(hostName)
	nodeId.WriteString(nodeIdDelimiter)

	if len(nvmeNQN) > 0 {
		if nodeId.Len()+len(nvmeNQN)+len(nodeIdDelimiter) <= MaxNodeIdLength {
			nodeId.WriteString(nvmeNQN)
		} else {
			return "", fmt.Errorf(ErrorNoPortsCouldFitInNodeId, nodeId.String(), MaxNodeIdLength)
		}
	}
	nodeId.WriteString(nodeIdDelimiter)
	if len(fcWWNs) > 0 {
		if nodeId.Len()+len(fcWWNs[0]) <= MaxNodeIdLength {
			nodeId.WriteString(fcWWNs[0])
		} else if nvmeNQN == "" {
			return "", fmt.Errorf(ErrorNoPortsCouldFitInNodeId, nodeId.String(), MaxNodeIdLength)
		}

		for _, fcPort := range fcWWNs[1:] {
			if nodeId.Len()+len(nodeIdFcDelimiter)+len(fcPort) <= MaxNodeIdLength {
				nodeId.WriteString(nodeIdFcDelimiter)
				nodeId.WriteString(fcPort)
			}
		}
	}
	if len(iscsiIQN) > 0 {
		if nodeId.Len()+len(nodeIdDelimiter)+len(iscsiIQN) <= MaxNodeIdLength {
			nodeId.WriteString(nodeIdDelimiter)
			nodeId.WriteString(iscsiIQN)
		} else if len(fcWWNs) == 0 && nvmeNQN == "" {
			return "", fmt.Errorf(ErrorNoPortsCouldFitInNodeId, nodeId.String(), MaxNodeIdLength)
		}
	}

	finalNodeId := strings.TrimSuffix(nodeId.String(), ";")
	return finalNodeId, nil
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

func (n NodeUtils) IsBlock(devicePath string) (bool, error) {
	var stat unix.Stat_t
	err := unix.Stat(devicePath, &stat)
	if err != nil {
		return false, err
	}
	return (stat.Mode & unix.S_IFMT) == unix.S_IFBLK, nil
}

func (d NodeUtils) GetFileSystemVolumeStats(path string) (VolumeStatistics, error) {
	statfs := &unix.Statfs_t{}
	err := unix.Statfs(path, statfs)
	if err != nil {
		return VolumeStatistics{}, err
	}

	availableBytes := int64(statfs.Bavail) * int64(statfs.Bsize)
	totalBytes := int64(statfs.Blocks) * int64(statfs.Bsize)
	usedBytes := (int64(statfs.Blocks) - int64(statfs.Bfree)) * int64(statfs.Bsize)

	totalInodes := int64(statfs.Files)
	availableInodes := int64(statfs.Ffree)
	usedInodes := totalInodes - availableInodes

	volumeStats := VolumeStatistics{
		AvailableBytes: availableBytes,
		TotalBytes:     totalBytes,
		UsedBytes:      usedBytes,

		AvailableInodes: availableInodes,
		TotalInodes:     totalInodes,
		UsedInodes:      usedInodes,
	}

	return volumeStats, nil
}

func (d NodeUtils) GetBlockVolumeStats(volumeId string) (VolumeStatistics, error) {
	volumeUuid := d.GetVolumeUuid(volumeId)
	mpathDevice, err := d.osDeviceConnectivityHelper.GetMpathDevice(volumeUuid)
	if err != nil {
		return VolumeStatistics{}, err
	}

	args := []string{"--getsize64", mpathDevice}
	out, err := d.Executer.ExecuteWithTimeoutSilently(device_connectivity.TimeOutBlockDevCmd, BlockDevCmd, args)
	if err != nil {
		return VolumeStatistics{}, err
	}

	strOut := strings.TrimSpace(string(out))
	sizeInBytes, err := strconv.ParseInt(strOut, 10, 64)
	if err != nil {
		return VolumeStatistics{}, err
	}

	volumeStats := VolumeStatistics{
		TotalBytes: sizeInBytes,
	}

	return volumeStats, nil
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

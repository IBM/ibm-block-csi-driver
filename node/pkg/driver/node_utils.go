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
	"encoding/json"
	"fmt"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/util/errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"k8s.io/utils/mount"
)

const (
	// In the Dockerfile of the node, specific commands (e.g: multipath, mount...) from the host mounted inside the container in /host directory.
	// Command lines inside the container will show /host prefix.
	PrefixChrootOfHostRoot  = "/host"
	PublishContextSeparator = ","
	mkfsTimeoutMilliseconds = 15 * 60 * 1000
)

//go:generate mockgen -destination=../../mocks/mock_node_utils.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver NodeUtilsInterface

type NodeUtilsInterface interface {
	ParseIscsiInitiators() (string, error)
	ParseFCPorts() ([]string, error)
	GetInfoFromPublishContext(publishContext map[string]string, configYaml ConfigFile) (string, int, map[string][]string, error)
	GetArrayInitiators(ipsByArrayInitiator map[string][]string) []string
	GetSysDevicesFromMpath(baseDevice string) (string, error)

	// TODO refactor and move all staging methods to dedicate interface.
	WriteStageInfoToFile(path string, info map[string]string) error
	ReadFromStagingInfoFile(filePath string) (map[string]string, error)
	ClearStageInfoFile(filePath string) error
	StageInfoFileIsExist(filePath string) bool
	IsPathExists(filePath string) bool
	IsDirectory(filePath string) bool
	RemoveFileOrDirectory(filePath string) error
	MakeDir(dirPath string) error
	MakeFile(filePath string) error
	FormatDevice(devicePath string, fsType string)
	IsNotMountPoint(file string) (bool, error)
	GetPodPath(filepath string) string
}

type NodeUtils struct {
	Executer executer.ExecuterInterface
	mounter  mount.Interface
}

func NewNodeUtils(executer executer.ExecuterInterface, mounter mount.Interface) *NodeUtils {
	return &NodeUtils{
		Executer: executer,
		mounter:  mounter,
	}
}

func (n NodeUtils) ParseIscsiInitiators() (string, error) {
	file, err := os.Open(IscsiFullPath)
	if err != nil {
		return "", err
	}

	defer file.Close()

	fileOut, err := ioutil.ReadAll(file)
	if err != nil {
		return "", err
	}

	fileSplit := strings.Split(string(fileOut), "InitiatorName=")
	if len(fileSplit) != 2 {
		return "", fmt.Errorf(ErrorWhileTryingToReadIQN, string(fileOut))
	}

	iscsiIqn := strings.TrimSpace(fileSplit[1])

	return iscsiIqn, nil
}

func (n NodeUtils) GetInfoFromPublishContext(publishContext map[string]string, configYaml ConfigFile) (string, int, map[string][]string, error) {
	// this will return :  connectivityType, lun, ipsByArrayInitiator, error
	ipsByArrayInitiator := make(map[string][]string)
	strLun := publishContext[configYaml.Controller.Publish_context_lun_parameter]

	lun, err := strconv.Atoi(strLun)
	if err != nil {
		return "", -1, nil, err
	}

	connectivityType := publishContext[configYaml.Controller.Publish_context_connectivity_parameter]
	if connectivityType == device_connectivity.ConnectionTypeISCSI {
		iqns := strings.Split(publishContext[configYaml.Controller.Publish_context_array_iqn], PublishContextSeparator)
		for _, iqn := range iqns {
			if ips, iqnExists := publishContext[iqn]; iqnExists {
				ipsByArrayInitiator[iqn] = strings.Split(ips, PublishContextSeparator)
			} else {
				logger.Errorf("Publish context does not contain any iscsi target IP for {%v}", iqn)
			}
		}
	}
	if connectivityType == device_connectivity.ConnectionTypeFC {
		wwns := strings.Split(publishContext[configYaml.Controller.Publish_context_fc_initiators], PublishContextSeparator)
		for _, wwn := range wwns {
			ipsByArrayInitiator[wwn] = nil
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

func (n NodeUtils) WriteStageInfoToFile(fPath string, info map[string]string) error {
	// writes to stageTargetPath/filename

	fPath = n.GetPodPath(fPath)
	stagePath := filepath.Dir(fPath)
	if _, err := os.Stat(stagePath); os.IsNotExist(err) {
		logger.Debugf("The filePath [%s] is not existed. Create it.", stagePath)
		if err = os.MkdirAll(stagePath, os.FileMode(0755)); err != nil {
			logger.Debugf("The filePath [%s] create fail. Error: [%v]", stagePath, err)
		}
	}
	logger.Debugf("WriteStageInfo file : path {%v}, info {%v}", fPath, info)
	stageInfo, err := json.Marshal(info)
	if err != nil {
		logger.Errorf("Error marshalling info file %s to json : {%v}", fPath, err.Error())
		return err
	}

	err = ioutil.WriteFile(fPath, stageInfo, 0600)

	if err != nil {
		logger.Errorf("Error while writing to file %s: {%v}", fPath, err.Error())
		return err
	}

	return nil
}

func (n NodeUtils) ReadFromStagingInfoFile(filePath string) (map[string]string, error) {
	// reads from stageTargetPath/filename
	filePath = n.GetPodPath(filePath)

	logger.Debugf("Read StagingInfoFile : path {%v},", filePath)
	stageInfo, err := ioutil.ReadFile(filePath)
	if err != nil {
		logger.Errorf("error reading file %s. err : {%v}", filePath, err.Error())
		return nil, err
	}

	infoMap := make(map[string]string)

	err = json.Unmarshal(stageInfo, &infoMap)
	if err != nil {
		logger.Errorf("Error unmarshalling file %s. err : {%v}", filePath, err.Error())
		return nil, err
	}

	return infoMap, nil
}

func (n NodeUtils) ClearStageInfoFile(filePath string) error {
	filePath = n.GetPodPath(filePath)
	logger.Debugf("Delete StagingInfoFile : path {%v},", filePath)

	return os.Remove(filePath)
}

func (n NodeUtils) GetSysDevicesFromMpath(device string) (string, error) {
	// this will return the 	/sys/block/dm-3/slaves/
	logger.Debugf("GetSysDevicesFromMpath with param : {%v}", device)
	deviceSlavePath := path.Join("/sys", "block", device, "slaves")
	logger.Debugf("looking in path : {%v}", deviceSlavePath)
	slaves, err := ioutil.ReadDir(deviceSlavePath)
	if err != nil {
		logger.Errorf("an error occured while looking for device slaves : {%v}", err.Error())
		return "", err
	}

	logger.Debugf("found slaves : {%v}", slaves)
	slavesString := ""
	for _, slave := range slaves {
		slavesString += "," + slave.Name()
	}
	return slavesString, nil

}

func (n NodeUtils) StageInfoFileIsExist(filePath string) bool {
	if _, err := os.Stat(filePath); err != nil {
		return false
	}
	return true
}

func (n NodeUtils) ParseFCPorts() ([]string, error) {
	var errs []error
	var fcPorts []string

	fpaths, err := n.Executer.FilepathGlob(FCPortPath)
	if fpaths == nil {
		err = fmt.Errorf(ErrorUnsupportedConnectivityType, device_connectivity.ConnectionTypeFC)
	}
	if err != nil {
		return nil, err
	}

	for _, fpath := range fpaths {
		file, err := os.Open(fpath)
		if err != nil {
			errs = append(errs, err)
			break
		}
		defer file.Close()

		fileOut, err := ioutil.ReadAll(file)
		if err != nil {
			errs = append(errs, err)
			break
		}

		fileSplit := strings.Split(string(fileOut), "0x")
		if len(fileSplit) != 2 {
			err := fmt.Errorf(ErrorWhileTryingToReadFC, string(fileOut))
			errs = append(errs, err)
		} else {
			fcPorts = append(fcPorts, strings.TrimSpace(fileSplit[1]))
		}
	}

	if errs != nil {
		err := errors.NewAggregate(errs)
		logger.Errorf("errors occured while looking for FC ports: {%v}", err)
		if fcPorts == nil {
			return nil, err
		}
	}

	return fcPorts, nil
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

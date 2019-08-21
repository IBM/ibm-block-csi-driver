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
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
)

//go:generate mockgen -destination=../../mocks/mock_node_utils.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver NodeUtilsInterface

type NodeUtilsInterface interface {
	ParseIscsiInitiators(path string) (string, error)
	GetInfoFromPublishContext(publishContext map[string]string, configYaml ConfigFile) (string, int, string, error)
	GetSysDevicesFromMpath(baseDevice string) (string, error)

	// TODO refactor and move all staging methods to dedicate interface.
	WriteStageInfoToFile(path string, info map[string]string) error
	ReadFromStagingInfoFile(filePath string) (map[string]string, error)
	ClearStageInfoFile(filePath string) error
	StageInfoFileIsExist(filePath string) bool
}

type NodeUtils struct {
}

func NewNodeUtils() *NodeUtils {
	return &NodeUtils{}

}

func (n NodeUtils) ParseIscsiInitiators(path string) (string, error) {

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}

	defer file.Close()

	file_out, err := ioutil.ReadAll(file)
	if err != nil {
		return "", err
	}

	fileSplit := strings.Split(string(file_out), "InitiatorName=")
	if len(fileSplit) != 2 {
		return "", fmt.Errorf(ErrorWhileTryingToReadIQN, string(file_out))
	}

	iscsiIqn := strings.TrimSpace(fileSplit[1])

	return iscsiIqn, nil
}

func (n NodeUtils) GetInfoFromPublishContext(publishContext map[string]string, configYaml ConfigFile) (string, int, string, error) {
	// this will return :  connectivityType, lun, array_iqn, error
	str_lun := publishContext[configYaml.Controller.Publish_context_lun_parameter]

	lun, err := strconv.Atoi(str_lun)
	if err != nil {
		return "", -1, "", err
	}

	connectivityType := publishContext[configYaml.Controller.Publish_context_connectivity_parameter]
	array_iqn := publishContext[configYaml.Controller.Publish_context_array_iqn]

	logger.Debugf( "PublishContext relevant info : connectivityType=%v, lun=%v, array_iqn=%v", connectivityType, lun, array_iqn)
	return connectivityType, lun, array_iqn, nil
}

func (n NodeUtils) WriteStageInfoToFile(filePath string, info map[string]string) error {
	// writes to stageTargetPath/filename

	filePath = PrefixChrootOfHostRoot + filePath
	logger.Debugf( "WriteStageInfo file : path {%v}, info {%v}", filePath, info)
	stageInfo, err := json.Marshal(info)
	if err != nil {
		logger.Errorf("Error marshalling info file %s to json : {%v}", filePath, err.Error())
		return err
	}

	err = ioutil.WriteFile(filePath, stageInfo, 0600)

	if err != nil {
		logger.Errorf("Error while writing to file %s: {%v}", filePath, err.Error())
		return err
	}

	return nil
}

func (n NodeUtils) ReadFromStagingInfoFile(filePath string) (map[string]string, error) {
	// reads from stageTargetPath/filename
	filePath = PrefixChrootOfHostRoot + filePath

	logger.Debugf( "Read StagingInfoFile : path {%v},", filePath)
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
	filePath = PrefixChrootOfHostRoot + filePath
	logger.Debugf( "Delete StagingInfoFile : path {%v},", filePath)

	return os.Remove(filePath)
}

func (n NodeUtils) GetSysDevicesFromMpath(device string) (string, error) {
	// this will return the 	/sys/block/dm-3/slaves/
	logger.Debugf( "GetSysDevicesFromMpath with param : {%v}", device)
	deviceSlavePath := path.Join("/sys", "block", device, "slaves")
	logger.Debugf( "looking in path : {%v}", deviceSlavePath)
	slaves, err := ioutil.ReadDir(deviceSlavePath)
	if err != nil {
		logger.Errorf("an error occured while looking for device slaves : {%v}", err.Error())
		return "", err
	}

	logger.Debugf( "found slaves : {%v}", slaves)
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

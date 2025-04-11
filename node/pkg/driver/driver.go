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
	"context"
	"io/ioutil"
	"net"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/util"

	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer_limited"
	mountwrapper "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"
	"google.golang.org/grpc"
	yaml "gopkg.in/yaml.v2"
	mount "k8s.io/mount-utils"
	"k8s.io/utils/exec"
)

type Driver struct {
	NodeService
	srv      *grpc.Server
	endpoint string
	config   ConfigFile
}

func NewDriver(endpoint string, configFilePath string, hostname string) (*Driver, error) {
	configFile, err := ReadConfigFile(configFilePath)
	if err != nil {
		return nil, err
	}
	logger.Infof("Driver: %v Version: %v", configFile.Identity.Name, configFile.Identity.Version)

	mounter := &mount.SafeFormatAndMount{
		Interface: mountwrapper.New(""),
		Exec:      exec.New(),
	}

	syncLock := NewSyncLock()
	executer := executer_limited.NewExecuter()
	osDeviceConnectivityMapping := map[string]device_connectivity.OsDeviceConnectivityInterface{
		configFile.Connectivity_type.Nvme_over_fc: device_connectivity.NewOsDeviceConnectivityNvmeOFc(executer),
		configFile.Connectivity_type.Fc:           device_connectivity.NewOsDeviceConnectivityFc(executer),
		configFile.Connectivity_type.Iscsi:        device_connectivity.NewOsDeviceConnectivityIscsi(executer),
	}
	osDeviceConnectivityHelper := device_connectivity.NewOsDeviceConnectivityHelperScsiGeneric(executer)
	return &Driver{
		endpoint:    endpoint,
		config:      configFile,
		NodeService: NewNodeService(configFile, hostname, *NewNodeUtils(executer, mounter, configFile, osDeviceConnectivityHelper), osDeviceConnectivityMapping, osDeviceConnectivityHelper, executer, mounter, syncLock),
	}, nil
}

func (d *Driver) Run() error {
	scheme, addr, err := util.ParseEndpoint(d.endpoint)
	if err != nil {
		return err
	}

	listener, err := net.Listen(scheme, addr)
	if err != nil {
		return err
	}

	logErr := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			logger.Errorf("GRPC error: %v", err)
		}
		return resp, err
	}
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logErr),
	}
	d.srv = grpc.NewServer(opts...)

	csi.RegisterIdentityServer(d.srv, d)
	csi.RegisterNodeServer(d.srv, d)

	logger.Infof("Listening for connections on address: %#v", listener.Addr())
	return d.srv.Serve(listener)
}

func (d *Driver) Stop() {
	logger.Infof("Stopping server")
	d.srv.Stop()
}

type Identity struct {
	Name    string
	Version string
	// TODO missing capabilities - currently the csi node is setting driver capability hardcoded. fix it low priority.
}

type Controller struct {
	Publish_context_lun_parameter          string
	Publish_context_connectivity_parameter string
	Publish_context_separator              string
	Publish_context_array_iqn              string
	Publish_context_fc_initiators          string
	//<array_iqn_1> : comma-separated list of iqn_1 iscsi target ips
	//<array_iqn_2> : comma-separated list of iqn_2 iscsi target ips
	//...
	//<array_iqn_k> : comma-separated list of iqn_k iscsi target ips
}

type Object_id_info struct {
	Delimiter     string
	Ids_delimiter string
}

type Node_id_info struct {
	Delimiter     string
	Fcs_delimiter string
}

type Parameters struct {
	Object_id_info Object_id_info
	Node_id_info   Node_id_info
}

type Connectivity_type struct {
	Nvme_over_fc string
	Fc           string
	Iscsi        string
}

type ConfigFile struct {
	Identity          Identity
	Controller        Controller
	Parameters        Parameters
	Connectivity_type Connectivity_type
}

const (
	DefualtConfigFile     string = "config.yaml"
	EnvNameDriverConfFile string = "DRIVER_CONFIG_YML"
)

func ReadConfigFile(configFilePath string) (ConfigFile, error) {
	var configFile ConfigFile

	configYamlPath := configFilePath
	if configYamlPath == "" {
		configYamlPath = DefualtConfigFile
		logger.Debugf("Not found config file environment variable %s. Set default value %s", EnvNameDriverConfFile, configYamlPath)
	} else {
		logger.Debugf("Config file environment variable %s=%s", EnvNameDriverConfFile, configYamlPath)
		logger.Info(logger.GetLevel())
	}

	yamlFile, err := ioutil.ReadFile(configYamlPath)
	if err != nil {
		logger.Errorf("failed to read file %q: %v", yamlFile, err)
		return ConfigFile{}, err
	}

	err = yaml.Unmarshal(yamlFile, &configFile)
	if err != nil {
		logger.Errorf("error unmarshaling yaml: %v", err)
		return ConfigFile{}, err
	}

	// Verify mandatory attributes in config file
	if configFile.Identity.Name == "" {
		err := &ConfigYmlEmptyAttribute{"Identity.Name"}
		logger.Errorf("%v", err)
		return ConfigFile{}, err
	}

	if configFile.Identity.Version == "" {
		err := &ConfigYmlEmptyAttribute{"Identity.Version"}
		logger.Errorf("%v", err)
		return ConfigFile{}, err
	}

	return configFile, nil
}

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
	csi "github.com/container-storage-interface/spec/lib/go/csi"
	util "github.com/ibm/ibm-block-csi-driver/node/util"
	"io/ioutil"
	"net"
	"os"

	"google.golang.org/grpc"
	"gopkg.in/yaml.v2"
	"k8s.io/klog"
)

type Driver struct {
	nodeService
	srv      *grpc.Server
	endpoint string
	config   ConfigFile
}

func NewDriver(endpoint string) (*Driver, error) {
	configFile, err := ReadConfigFile()
	if err != nil {
		return nil, err
	}
	klog.Infof("Driver: %v Version: %v", configFile.Identity.Name, configFile.Identity.Version)

	return &Driver{
		endpoint:    endpoint,
		config:      configFile,
		nodeService: newNodeService(),
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
			klog.Errorf("GRPC error: %v", err)
		}
		return resp, err
	}
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logErr),
	}
	d.srv = grpc.NewServer(opts...)

	csi.RegisterIdentityServer(d.srv, d)
	csi.RegisterNodeServer(d.srv, d)
	//csi.RegisterControllerServer(d.srv, d)

	klog.Infof("Listening for connections on address: %#v", listener.Addr())
	return d.srv.Serve(listener)
}

func (d *Driver) Stop() {
	klog.Infof("Stopping server")
	d.srv.Stop()
}

type ConfigFile struct {
	Identity struct {
		Name    string
		Version string
		// TODO missing capabilities
	}
	Controller struct {
		Publish_context_lun_parameter          string
		Publish_context_connectivity_parameter string
	}
}

const (
	DefualtConfigFile     string = "config.yaml"
	EnvNameDriverConfFile string = "DRIVER_CONFIG_YML"
)

func ReadConfigFile() (ConfigFile, error) {
	var configFile ConfigFile

	configYamlPath := os.Getenv(EnvNameDriverConfFile)
	if configYamlPath == "" {
		configYamlPath = DefualtConfigFile
		klog.V(4).Infof("Config file environment variable %s=%s", EnvNameDriverConfFile, configYamlPath)
	} else {
		klog.V(4).Infof("Not found config file environment variable %s. Set default value %s.", EnvNameDriverConfFile, configYamlPath)
	}

	yamlFile, err := ioutil.ReadFile(configYamlPath)
	if err != nil {
		klog.Errorf("failed to read file %q: %v", yamlFile, err)
		return ConfigFile{}, err
	}

	err = yaml.Unmarshal(yamlFile, &configFile)
	if err != nil {
		klog.Errorf("error unmarshaling yaml: %v", err)
		return ConfigFile{}, err
	}

	return configFile, nil
}

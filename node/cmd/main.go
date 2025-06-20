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

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver"
)

func logVersionInfo(configFile *string) {
	info, err := driver.GetVersionJSON(*configFile)
	if err != nil {
		logger.Errorln(err)
	}
	logger.Infof("%s", fmt.Sprintf("Node version info: %v", info))
}

func main() {
	logger.Debugf("Starting CSI node...") // Note - must set this in the first line in order to define the -loglevel in the flags
	var (
		endpoint            = flag.String("csi-endpoint", "unix://csi/csi.sock", "CSI Endpoint")
		exitAfterLogVersion = flag.Bool("version", false, "Log the version and exit.")
		configFile          = flag.String("config-file-path", "./config.yaml", "Shared config file.")
		hostname            = flag.String("hostname", "host-dns-name", "The name of the host the node is running on.")
		// 1000 - virtually unlimited
		max_invocations   = flag.Int("max-invocations", 1000, "Max number of external processes allowed to run concurrently")
		clean_scsi_device = flag.String("clean-scsi-device", "true", "Should clean ghost scsi device upon nodeStage")
	)

	flag.Parse()

	logVersionInfo(configFile)

	if *max_invocations < 1 {
		logger.Errorf("Max invocations is invalid: %d", *max_invocations)
		os.Exit(0)
	}

	if *exitAfterLogVersion {
		os.Exit(0)
	}

	drv, err := driver.NewDriver(*endpoint, *configFile, *hostname, *max_invocations, *clean_scsi_device == "true")
	if err != nil {
		logger.Panicln(err)
	}
	if err := drv.Run(); err != nil {
		logger.Panicln(err)
	}
}

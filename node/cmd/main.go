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
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

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

		isPreStop = flag.Bool("prestop", false, "Run as preStop hook client and wait for driver quiescence")
		quietPeriod = flag.Duration("shutdown-quiet-period", 10*time.Second, "Quiet duration before shutdown")
		giveUpTimeout = flag.Duration("shutdown-giveup-timeout", 45*time.Second, "Max wait duration in preStop")
		quiesceSock = flag.String("quiesce-sock", "/csi/quiesce.sock", "Unix socket path for preStop coordination")
	)

	// Set a hard limit on the total OS threads this process can ever create.
	debug.SetMaxThreads(2000)

	flag.Parse()

	logVersionInfo(configFile)

        if *isPreStop {
                // Client mode invoked by Kubernetes PreStop Exec
                if err := driver.RunPreStopClient(*quiesceSock, *giveUpTimeout+5*time.Second); err != nil {
                        logger.Warningf("PreStop client exited with error (proceeding with shutdown): %v", err)
                }
                os.Exit(0)
        }


	if *max_invocations < 1 {
		logger.Errorf("Max invocations is invalid: %d", *max_invocations)
		os.Exit(0)
	}

	if *exitAfterLogVersion {
		os.Exit(0)
	}


	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tracker := driver.NewTracker(driver.Config{
		QuietPeriod:      *quietPeriod,
		MaxGiveUpTimeout: *giveUpTimeout,
		SocketPath:       *quiesceSock,
	})

	go func() {
		if err := tracker.StartUnixServer(ctx); err != nil {
			logger.Errorf("Failed to run preStop unix server: %v", err)
		}
	}()

	// Pass tracker into NewDriver so the gRPC interceptor can use it
	drv, err := driver.NewDriver(*endpoint, *configFile, *hostname, *max_invocations, *clean_scsi_device == "true", tracker)
	if err != nil {
		logger.Panicln(err)
	}

	// Run the CSI gRPC server in the background so main() can wait on ctx.Done()
	errCh := make(chan error, 1)
	go func() {
		errCh <- drv.Run()
	}()

	// Wait for either a termination signal or a fatal server startup/runtime error
	select {
	case <-ctx.Done():
		logger.Infof("Shutdown signal received, stopping CSI driver...")
		drv.Stop()
	case err := <-errCh:
		if err != nil {
			logger.Panicln(err)
		}
	}

	// Clean up Unix domain sockets after the server stops
	_ = os.Remove("/csi/csi.sock")
	_ = os.Remove("/registration/ibm-block-csi-driver-reg.sock")
}

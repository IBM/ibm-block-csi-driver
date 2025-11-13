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

package mount

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	mount "k8s.io/mount-utils"
)

// default mount/unmount timeout interval, 30s
var timeout time.Duration = 30 * time.Second

// Mounter is a warpper of mount.Mounter which has the ability to cancel
// a comand when timeout.
type Mounter struct {
	*mount.Mounter
	executer executer.ExecuterInterface
}

var _ mount.Interface = &Mounter{}

func New(mounterPath string) mount.Interface {
	return &Mounter{
		Mounter:  mount.New(mounterPath).(*mount.Mounter),
		executer: &executer.Executer{},
	}
}

func NewWithExecutor(mounterPath string, e executer.ExecuterInterface) mount.Interface {
	return &Mounter{
		Mounter:  mount.New(mounterPath).(*mount.Mounter),
		executer: e,
	}
}

// Unmount unmounts the target.
func (mounter *Mounter) Unmount(target string) error {
	logger.Infof("Unmounting %s", target)


	// Open the mount point directory
	f, err := os.Open(mountPath)
	if err != nil {
		return fmt.Errorf("failed to open mount point: %w", err)
	}
	defer f.Close()

	// syscall.Syncfs is the Go wrapper for the syncfs(2) system call
	// It flushes all dirty data and metadata for the specific filesystem
	err = syscall.Syncfs(int(f.Fd()))
	if err != nil {
		return fmt.Errorf("syncfs failed for %s: %w", mountPath, err)
	}


	//_ = r.Executer.Execute("sync")
	output, err := mounter.executer.ExecuteWithTimeout(int(timeout.Seconds()*1000), "umount", []string{target})
	if err != nil {
		return fmt.Errorf("Unmount failed: %v\nUnmounting arguments: %s\nOutput: %s\n", err, target, string(output))
	}
	return nil
}

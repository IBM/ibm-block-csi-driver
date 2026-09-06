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

package mount

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"golang.org/x/sys/unix"
	mount "k8s.io/mount-utils"
)

// hostRoot is the path where the node's root filesystem is bind-mounted
// inside the driver container (set via hostPath volume in the DaemonSet).
const hostRoot = "/host"

// hostPath returns the in-container path for a host filesystem path.
func hostPath(p string) string {
	return path.Join(hostRoot, p)
}

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

// Mount overrides the mount-utils default to use the unix.Mount syscall
// directly instead of executing the mount(8) binary.  This avoids the
// getBindMountOptions/unix.Statfs call that mount-utils makes on the source
// path: that statfs runs in the container filesystem namespace where kubelet
// paths are only visible under /host, but our source is passed as a bare
// host path (without that prefix).
//
// The unix.Mount syscall runs in the kernel mount namespace (the driver
// container is privileged and shares the host mount namespace), so paths like
// /var/lib/kubelet/... are visible directly to the kernel.  The target is
// accessed via its /host-prefixed container path.
func (mounter *Mounter) Mount(source, target, fstype string, options []string) error {
	return mounter.MountSensitive(source, target, fstype, options, nil)
}

// MountSensitive is the same as Mount but accepts an additional
// sensitiveOptions slice (for passwords etc. that must not be logged).
//
// Path resolution strategy: on some OS variants (e.g. SUSE) the driver
// container's rootfs does not directly expose host paths such as /dev/dm-N or
// /var/lib/kubelet/...; they are only reachable via the /host bind-mount.  On
// others (e.g. Ubuntu/RHEL) those paths are visible directly.  We probe the
// source path first: if it is stat-able as-is we use bare paths; otherwise we
// prepend /host to both source and target.
func (mounter *Mounter) MountSensitive(source, target, fstype string, options, sensitiveOptions []string) error {
	logger.Infof("MountSensitive: mounting %s to %s (fstype=%s options=%v)", source, target, fstype, options)
	allOptions := append(options, sensitiveOptions...)
	flags, data := parseMountOptions(allOptions)

	src, tgt := resolveMountPaths(source, target)
	logger.Debugf("MountSensitive: resolved paths: source=%s target=%s", src, tgt)
	return unix.Mount(src, tgt, fstype, flags, data)
}

// resolveMountPaths determines the correct in-container paths for source and
// target.  It probes the source path: if unix.Stat succeeds on the bare path
// the OS exposes host paths directly (Ubuntu/RHEL), so both paths are used
// as-is.  If not, the /host prefix is applied to both (SUSE).
func resolveMountPaths(source, target string) (string, string) {
	var stat unix.Stat_t
	if err := unix.Stat(source, &stat); err == nil {
		// Source is directly visible — no prefix needed (Ubuntu/RHEL).
		return source, target
	}
	// Source not visible without prefix — use /host-prefixed paths (SUSE).
	return hostPath(source), hostPath(target)
}

// parseMountOptions translates a slice of mount option strings into the
// uintptr flags and comma-separated data string expected by unix.Mount.
func parseMountOptions(options []string) (uintptr, string) {
	var flags uintptr
	var data []string
	for _, opt := range options {
		switch opt {
		case "ro":
			flags |= unix.MS_RDONLY
		case "rw":
			flags &= ^uintptr(unix.MS_RDONLY)
		case "bind":
			flags |= unix.MS_BIND
		case "rbind":
			flags |= unix.MS_BIND | unix.MS_REC
		case "remount":
			flags |= unix.MS_REMOUNT
		case "shared":
			flags |= unix.MS_SHARED
		case "slave":
			flags |= unix.MS_SLAVE
		case "private":
			flags |= unix.MS_PRIVATE
		case "nosuid":
			flags |= unix.MS_NOSUID
		case "nodev":
			flags |= unix.MS_NODEV
		case "noexec":
			flags |= unix.MS_NOEXEC
		case "noatime":
			flags |= unix.MS_NOATIME
		case "relatime":
			flags |= unix.MS_RELATIME
		case "nouuid":
			// XFS-specific; not a kernel mount flag — pass as filesystem data
			data = append(data, opt)
		case "defaults", "_netdev", "nofail", "auto", "noauto":
			// userspace-only options — skip
		default:
			data = append(data, opt)
		}
	}
	return flags, strings.Join(data, ",")
}

// Unmount unmounts the target.
func (mounter *Mounter) Unmount(target string) error {
	logger.Infof("Unmounting %s", target)
	output, err := mounter.executer.ExecuteWithTimeout(int(timeout.Seconds()*1000), "umount", []string{target})
	if err != nil {
		return fmt.Errorf("Unmount failed: %v\nUnmounting arguments: %s\nOutput: %s\n", err, target, string(output))
	}
	return nil
}

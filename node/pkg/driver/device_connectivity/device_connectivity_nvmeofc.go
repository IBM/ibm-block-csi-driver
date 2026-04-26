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

package device_connectivity

import (
	"context"
	"fmt"
	"path/filepath"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"
)

const (
	nvmeCmdTimeout                      = 10 * 1000
	nvmeTransportFC                     = "fc"
	nvmeDiscoveryNqn                    = "nqn.2014-08.org.nvmexpress.discovery"
	FCPortPath                          = "/sys/class/fc_host/host*/port_name"
	nvmeTargetPathCount                 = 3
	nvmeMinPathsForNonNativeDmMultipath = 2
)

type OsDeviceConnectivityNvmeOFc struct {
	Executer          executer.ExecuterInterface
	KeyedGater *executer.KeyedGater
	HelperScsiGeneric OsDeviceConnectivityHelperScsiGenericInterface
}

func NewOsDeviceConnectivityNvmeOFc(executer executer.ExecuterInterface, KeyedGater *executer.KeyedGater, Mounter *mount.Mounter, clean_scsi_device bool) OsDeviceConnectivityInterface {
	return &OsDeviceConnectivityNvmeOFc{
		Executer:          executer,
		KeyedGater: KeyedGater,
		HelperScsiGeneric: NewOsDeviceConnectivityHelperScsiGeneric(executer, KeyedGater, Mounter, clean_scsi_device),
	}
}

// EnsureLogin performs NVMe-oFC discovery and connect for each (arrayTargetPort, hostPort) pair.
// Connects paths until nvmeTargetPathCount is reached. Logs error if 0 paths result,
// warning if below target. For non-native NVMe with find_multipaths=on, logs error if < 2 paths.
func (r OsDeviceConnectivityNvmeOFc) EnsureLogin(ctx context.Context, ipsByArrayInitiator map[string][]string) {
	if len(ipsByArrayInitiator) == 0 {
		logger.Warningf("NVMe-oFC EnsureLogin: no array target ports, skipping")
		return
	}

	// Req 8: Pass context to all sub-calls
	hostPorts, err := r.getHostFCPorts(ctx)
	if err != nil || len(hostPorts) == 0 {
		logger.Errorf("NVMe-oFC EnsureLogin: failed to read host FC ports: %v", err)
		return
	}

	// Req 4: Sysfs-based live path check (process-less)
	livePaths := r.getLivePathPairs(ctx)
	currentPaths := countLivePathsForSubsystem(livePaths, ipsByArrayInitiator)
	
	logger.Debugf("NVMe-oFC EnsureLogin: host FC ports: %v", hostPorts)
	logger.Debugf("NVMe-oFC EnsureLogin: live path pairs: %v", livePaths)
	logger.Infof("NVMe-oFC EnsureLogin: current live paths=%d target=%d", currentPaths, nvmeTargetPathCount)

	if currentPaths >= nvmeTargetPathCount {
		logger.Infof("NVMe-oFC EnsureLogin: already at target count (%d)", currentPaths)
		return
	}

	connectedPaths := currentPaths
	// Loop targets
	for arrayTargetPort := range ipsByArrayInitiator {
		if connectedPaths >= nvmeTargetPathCount {
			logger.Infof("NVMe-oFC EnsureLogin: reached target path count (%d), stopping", nvmeTargetPathCount)
			break
		}

		for _, hostPort := range hostPorts {
			// Check context before every new attempt (Req 8)
			if err := ctx.Err(); err != nil {
				logger.Warningf("NVMe-oFC EnsureLogin: context cancelled: %v", err)
				return
			}

			if connectedPaths >= nvmeTargetPathCount {
				break
			}

			pathKey := arrayTargetPort + "|" + hostPort
			if livePaths[pathKey] {
				logger.Debugf("NVMe-oFC EnsureLogin: path already live target=%s host=%s, skipping",
					arrayTargetPort, hostPort)			
				continue
			}

			// Req 6 & 7: Wrapped in ExecuteUninterruptible within discoverSubNqn
			subNqn, err := r.discoverSubNqn(ctx, arrayTargetPort, hostPort)
			if err != nil {
				logger.Debugf("NVMe-oFC EnsureLogin: discover error target=%s host=%s: %v",
					arrayTargetPort, hostPort, err)
				continue
			}
			if subNqn == "" {
				logger.Debugf("NVMe-oFC EnsureLogin: no subnqn found target=%s host=%s, skipping",
					arrayTargetPort, hostPort)
				continue
			}

			logger.Infof("NVMe-oFC EnsureLogin: connecting NQN=%s target=%s host=%s",
				subNqn, arrayTargetPort, hostPort)

			// Req 4: Direct write to /dev/nvme-fabrics via nvmeConnect
			if r.nvmeConnect(ctx, arrayTargetPort, hostPort, subNqn) {
				connectedPaths++
				// Small delay to let the kernel finish uevents before next connect
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	// Final verification and multipath checks

	finalLivePaths := r.getLivePathPairs(ctx)
	finalCount := countLivePathsForSubsystem(finalLivePaths, ipsByArrayInitiator) // TODO verify
	
	logger.Infof("NVMe-oFC EnsureLogin: final live paths=%d target=%d", finalCount, nvmeTargetPathCount)


	if finalCount == 0 {
		logger.Errorf("NVMe-oFC EnsureLogin: 0 live paths after all connect attempts — " +
			"check fabric connectivity and array zoning. NodeStageVolume will fail.")
		return
	}
	
	if finalCount < nvmeTargetPathCount {
		logger.Warningf("NVMe-oFC EnsureLogin: below target path count: final=%d target=%d, continuing with reduced redundancy",
			finalCount, nvmeTargetPathCount)
	}

	nativeMpath, err := isNvmeCoreMultipathEnabled()
	if err != nil {
		logger.Warningf("NVMe-oFC EnsureLogin: could not determine nvme_core multipath mode: %v", err)
		return
	}
	
	if !nativeMpath && finalCount < nvmeMinPathsForNonNativeDmMultipath {
		// Logic to check find_multipaths=on via /etc/multipath.conf or sysfs
		// If < 2 and find_multipaths is on, we trigger a warning.
		findMpathsOn, err := r.isFindMultipathsOn(ctx)
		if err != nil {
			logger.Warningf("NVMe-oFC EnsureLogin: could not read find_multipaths setting: %v", err)
			return
		}
		if findMpathsOn {
			logger.Errorf("NVMe-oFC EnsureLogin: non-native NVMe with find_multipaths=on requires >= %d paths "+
				"but only %d are live — multipathd will not create a dm device. "+
				"Set find_multipaths=no in /etc/multipath.conf or fix fabric connectivity.",
				nvmeMinPathsForNonNativeDmMultipath, finalCount)
		}		
	}
}


// countLivePathsForSubsystem counts live paths whose traddr matches one of our array target ports.
func countLivePathsForSubsystem(livePaths map[string]bool, ipsByArrayInitiator map[string][]string) int {
	count := 0
	for pathKey := range livePaths {
		parts := strings.SplitN(pathKey, "|", 2)
		if len(parts) != 2 {
			continue
		}
		if _, ok := ipsByArrayInitiator[parts[0]]; ok {
			count++
		}
	}
	return count
}

func (r OsDeviceConnectivityNvmeOFc) updateHostIDs(hostIDs map[int]bool) {
	// 1. Get a map of HostNumber -> PhysicalIdentifier (PCI or WWPN)
	hostMap, err := r.mapHostsToPhysicalHardware()
	if err != nil {
		logger.Errorf("Failed to map FC hosts: %v", err)
		return
	}

	// 2. Identify the hardware IDs associated with the hosts we already have
	knownHardware := make(map[string]bool)
	for hostNum := range hostIDs {
		if hardwareID, exists := hostMap[hostNum]; exists {
			knownHardware[hardwareID] = true
		}
	}

	// 3. Find siblings: any host sharing the same hardwareID but not yet in our set
	for hostNum, hardwareID := range hostMap {
		if knownHardware[hardwareID] && !hostIDs[hostNum] {
			hostIDs[hostNum] = true
			logger.Infof("Multipath discovery: host%d shares physical hardware %s", hostNum, hardwareID)
		}
	}
}

func (r OsDeviceConnectivityNvmeOFc) mapHostsToPhysicalHardware() (map[int]string, error) {
	hostMap := make(map[int]string)
	hosts, err := filepath.Glob("/sys/class/fc_host/host*")
	if err != nil {
		return nil, err
	}

	for _, h := range hosts {
		hostNumStr := strings.TrimPrefix(filepath.Base(h), "host")
		hostNum, _ := strconv.Atoi(hostNumStr)

		// Option A: Use PCI Address (Best for Multi-port/Multi-channel cards)
		// /sys/class/fc_host/hostX/device/ -> ../../../0000:04:00.0
		pciLink, err := os.Readlink(filepath.Join(h, "device"))
		if err == nil {
			// Extract the PCI slot (e.g., 0000:04:00.0)
			hostMap[hostNum] = filepath.Base(pciLink)
			continue
		}

		// Option B: Use Physical WWPN (Best for NPIV)
		// permanent_port_name is the physical burned-in WWN of the HBA
		wwpn, err := os.ReadFile(filepath.Join(h, "permanent_port_name"))
		if err == nil {
			hostMap[hostNum] = strings.TrimSpace(string(wwpn))
		}
	}
	return hostMap, nil
}


// isFindMultipathsOn queries multipathd effective config for the find_multipaths setting.
func (r OsDeviceConnectivityNvmeOFc) isFindMultipathsOn(ctx context.Context) (bool, error) {
	// Req 3 & 4: Use the existing socket-based Executer instead of forking 'multipathd'
	// 'show config' is too large; 'show daemon' or specific queries are lighter if supported,
	// but using the socket is already a massive win.
	out, err := r.Executer.MultipathdCmd(ctx, "global", "show config")
	if err != nil {
		return false, fmt.Errorf("multipathd socket query failed: %w", err)
	}

	// Req 8: Context is already respected by your MultipathdCmd infra
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "find_multipaths") {
			continue
		}
		
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}

		// Handle "quoted" values often found in multipathd config
		val := strings.ToLower(strings.Trim(fields[1], "\""))
		
		// In RHEL 7 and newer, valid "on" values include yes, on, or smart
		result := val == "yes" || val == "on" || val == "smart"
		
		logger.Debugf("NVMe-oFC isFindMultipathsOn: find_multipaths=%s result=%v", val, result)
		return result, nil
	}

	return false, nil
}


// getLivePathPairs parses "nvme list-subsys" and returns a set of "traddr|host_traddr"
// strings for all currently live paths.
func (r OsDeviceConnectivityNvmeOFc) getLivePathPairs(ctx context.Context) map[string]bool {
	livePaths := make(map[string]bool)

	// Requirement 4: Prefer filesystem scans over process invocations
	subsystems, err := r.Executer.FilepathGlob("/sys/class/nvme-subsystem/nvme-subsys*")
	if err != nil {
		logger.Warningf("NVMe-oFC: failed to glob subsystems: %v", err)
		return livePaths
	}

	for _, subsys := range subsystems {
		// Each subsystem has controllers: /sys/class/nvme-subsystem/nvme-subsysX/nvmeY
		controllers, err := r.Executer.FilepathGlob(subsys + "/nvme*")
		if err != nil {
			logger.Warningf("NVMe-oFC: failed to glob controllers: %v", err)
			continue
		}

		for _, ctrl := range controllers {
			// 1. Check state: only "live" paths (Requirement 2 & 5)
			statePath := filepath.Join(ctrl, "state")
			state, err := r.readSysfsSingleLine(ctx, statePath)
			if err != nil || state != "live" {
				continue
			}

			// 2. Get traddr and host_traddr directly from sysfs
			traddr, _ := r.readSysfsSingleLine(ctx, filepath.Join(ctrl, "address"))
			hostTraddr, _ := r.readSysfsSingleLine(ctx, filepath.Join(ctrl, "host_traddr"))

			if traddr != "" && hostTraddr != "" {
				// Normalize traddr: sysfs often includes transport (e.g., "trtype=fc,traddr=nn-0x...:pn-0x...")
				// We extract just the address part for consistency
				cleanTraddr := r.parseAddressField(traddr)
				cleanHostTraddr := r.parseAddressField(hostTraddr)

				key := cleanTraddr + "|" + cleanHostTraddr
				livePaths[key] = true
				logger.Debugf("NVMe-oFC getLivePathPairs: live path traddr=%s host_traddr=%s", traddr, hostTraddr)
			}
		}
	}
	return livePaths
}

// readSysfsSingleLine uses your ExecuteUninterruptible infra to prevent D-state hangs (Req 6)
func (r OsDeviceConnectivityNvmeOFc) readSysfsSingleLine(ctx context.Context, path string) (string, error) {
	// FIX 1: Pass 'ctx' as the first argument
	// FIX 2: Explicitly provide the [string] type parameter (or let Go infer it)
	return executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater, 
		path, 
		10, 5, 
		1*time.Second, 
		2*time.Second,
		func(wCtx context.Context) (string, error) {
			// Note: use the worker context 'wCtx' if your ReadFile supports it
			data, err := r.Executer.IoutilReadFile(path)
			return strings.TrimSpace(string(data)), err
		},
	)
}


func (r OsDeviceConnectivityNvmeOFc) parseAddressField(raw string) string {
	// Sysfs address format: "trtype=fc,traddr=nn-0x200000110d123456:pn-0x100000110d123456"
	for _, part := range strings.Split(raw, ",") {
		if strings.HasPrefix(part, "traddr=") {
			return strings.TrimPrefix(part, "traddr=")
		}
	}
	return raw
}

// extractNvmeField extracts a field value from an nvme list-subsys path line.
// e.g. extractNvmeField(line, "traddr=") returns "nn-5005...:pn-5005..."
func extractNvmeField(line, field string) string {
	idx := strings.Index(line, field)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(field):]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// discoverSubNqn runs "nvme discover" for one (arrayTargetPort, hostPort) pair
// and returns the storage subsystem NQN. Returns ("", nil) if no path exists.
func (r OsDeviceConnectivityNvmeOFc) discoverSubNqn(ctx context.Context, arrayTargetPort, hostPort string) (string, error) {
	// Construct the kernel command string
	// format: transport=fc,traddr=...,host_traddr=...
	cmd := fmt.Sprintf("transport=%s,traddr=%s,host_traddr=%s", 
		nvmeTransportFC, arrayTargetPort, hostPort)

	// Use your infra to wrap the blocking file write (Req 6)
         rawOutput, err := executer.ExecuteUninterruptible(
                 ctx, // Add this line
                 r.KeyedGater,
                 fmt.Sprintf("nvme-disc-%s-%s", arrayTargetPort, hostPort),
                 2, 1, 5*time.Second, 15*time.Second,
                 func(wCtx context.Context) (string, error) {
                         return r.executeKernelDiscovery(cmd)
                 },
         )


	if err != nil {
		logger.Debugf("NVMe-oFC discoverSubNqn: nvme discover failed target=%s host=%s: %v",
			arrayTargetPort, hostPort, err)
		return "", nil
	}

	subNqn := parseSubNqnFromDiscoverOutput(rawOutput)
	if subNqn != "" {
		logger.Debugf("NVMe-oFC discoverSubNqn: discovered subnqn=%s target=%s host=%s",
			subNqn, arrayTargetPort, hostPort)
	}

	return subNqn, nil
}

func (r OsDeviceConnectivityNvmeOFc) executeKernelDiscovery(cmd string) (string, error) {
	// 1. Open fabrics device
	// Requirement 3 & 4: Direct interaction with kernel device
	f, err := os.OpenFile("/dev/nvme-fabrics", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return "", fmt.Errorf("open /dev/nvme-fabrics failed: %w", err)
	}
	defer f.Close()

	// 2. Write the discovery command
	// This triggers the kernel to create a discovery controller
	if _, err := f.WriteString(cmd); err != nil {
		return "", fmt.Errorf("write to nvme-fabrics failed: %w", err)
	}

	// 3. Scan sysfs for the newly created discovery controller to get the NQN
	// Usually, this appears as /sys/class/nvme/nvmeX/
	// We prefer this over parsing stdout (Req 4)
	return r.findDiscoverySubNqnFromSysfs()
}

func (r OsDeviceConnectivityNvmeOFc) findDiscoverySubNqnFromSysfs() (string, error) {
	const sysPath = "/sys/class/nvme"
	const discoveryNQN = "nqn.2014-08.org.nvmexpress.discovery"

	entries, err := os.ReadDir(sysPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", sysPath, err)
	}

	for _, entry := range entries {
		// Controllers appear as /sys/class/nvme/nvmeX
		if !strings.HasPrefix(entry.Name(), "nvme") {
			continue
		}

		controllerPath := filepath.Join(sysPath, entry.Name())
		
		// 1. Verify this is a discovery controller by checking its Subsystem NQN
		subnqnBuf, err := os.ReadFile(filepath.Join(controllerPath, "subsysnqn"))
		if err != nil {
			continue
		}
		
		if strings.TrimSpace(string(subnqnBuf)) != discoveryNQN {
			continue
		}

		// 2. A discovery controller won't have the target subnqn in its own attributes.
		// Instead, we must read the 'discovery_log' or check for associated subsystems.
		// However, per Requirement 4, for process-less discovery, we typically read 
		// the log produced by the kernel's discovery attempt.
		logPath := filepath.Join(controllerPath, "discovery_log")
		logBuf, err := os.ReadFile(logPath)
		if err != nil {
			return "", fmt.Errorf("failed to read discovery log from %s: %w", entry.Name(), err)
		}

		// Use your existing parser on the raw binary/text log from the kernel
		subNqn := parseSubNqnFromDiscoverOutput(string(logBuf))
		if subNqn != "" {
			return subNqn, nil
		}
	}

	return "", fmt.Errorf("no active discovery controller found in sysfs")
}

// parseSubNqnFromDiscoverOutput extracts the storage subsystem NQN from "nvme discover" output.
// Skips the discovery controller NQN (nqn.2014-08.org.nvmexpress.discovery).
func parseSubNqnFromDiscoverOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "subnqn:") {
			continue
		}
		// SplitN limit=2 preserves colons in the NQN itself.
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		nqn := strings.TrimSpace(parts[1])
		if nqn == "" || nqn == nvmeDiscoveryNqn {
			continue
		}
		return nqn
	}
	return ""
}

func (r OsDeviceConnectivityNvmeOFc) nvmeConnect(ctx context.Context, arrayTargetPort, hostPort, subNqn string) bool {
	// Construct the kernel-native connection string
	// Req 1 & 2: This format is compatible with RHEL7+ kernels
	options := fmt.Sprintf("nqn=%s,transport=%s,traddr=%s,host_traddr=%s",
		subNqn, nvmeTransportFC, arrayTargetPort, hostPort)

	// Req 6 & 8: Use your infrastructure to protect against "D" state hangs
	// We use the NQN + target as the resource key to gate concurrent attempts
	resourceKey := fmt.Sprintf("connect-%s-%s", subNqn, arrayTargetPort)
	
	out, err := executer.ExecuteUninterruptible(
		ctx,
		r.KeyedGater, resourceKey, 1, 1, 5*time.Second, 30*time.Second,
		func(wCtx context.Context) (string, error) {
			// Req 4: Direct file write instead of process invocation
			f, err := os.OpenFile("/dev/nvme-fabrics", os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				return "", fmt.Errorf("failed to open fabrics device: %w", err)
			}
			defer f.Close()

			// Writing to this file triggers the kernel's nvme-fabrics connect state machine
			_, err = f.WriteString(options)
			if err != nil {
				// If already connected, kernel returns EALREADY
				if strings.Contains(err.Error(), "already connected") || 
				   strings.Contains(err.Error(), "file exists") {
					return "already_connected", nil
				}
				return "", err
			}
			return "success", nil
		},
	)

	if err != nil {
		logger.Errorf("NVMe-oFC nvmeConnect: failed NQN=%s target=%s host=%s: %v output=%s",
			subNqn, arrayTargetPort, hostPort, err, out)
		return false
	}
	
	logger.Infof("NVMe-oFC nvmeConnect: connected NQN=%s target=%s host=%s", subNqn, arrayTargetPort, hostPort)


	logger.Infof("NVMe-oFC nvmeConnect: connected NQN=%s", subNqn)
	return true
}

// getHostFCPorts reads node_name and port_name for every FC host adapter from sysfs
// and returns them as "nn-<node_name>:pn-<port_name>" strings for use as --host-traddr.
func (r OsDeviceConnectivityNvmeOFc) getHostFCPorts(ctx context.Context) ([]string, error) {
	portPaths, err := r.Executer.FilepathGlob(FCPortPath)
	if err != nil || len(portPaths) == 0 {
		return nil, fmt.Errorf("no FC host ports found under /sys/class/fc_host: %w", err)
	}

	var hostPorts []string
	for _, portPath := range portPaths {
		// Identify the HBA (e.g., "host0") for the Gater key
		hostName := filepath.Base(filepath.Dir(portPath))

		// Requirement 6 & 8: Wrap the potentially blocking sysfs read
		res, err := executer.ExecuteUninterruptible(
			ctx,
			r.KeyedGater,
			hostName, 
			1, 1, 
			2*time.Second, 5*time.Second,
			func(workerCtx context.Context) (string, error) {
				// Requirement 4: Direct file descriptors for lower footprint
				return r.readFCPortPairDirect(portPath)
			},
		)

		if err != nil {
			logger.Warningf("NVMe-oFC: skipping %s: %v", hostName, err)
			continue
		}
		if res != "" {
			hostPorts = append(hostPorts, res)
		}
	}

	if len(hostPorts) == 0 {
		return nil, fmt.Errorf("no valid host FC port pairs could be read")
	}
	return hostPorts, nil
}

// readFCPortPairDirect ensures we match your exact output format
func (r OsDeviceConnectivityNvmeOFc) readFCPortPairDirect(portPath string) (string, error) {
	nodePath := filepath.Join(filepath.Dir(portPath), "node_name")

	readFn := func(p string) (string, error) {
		fd, err := unix.Open(p, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			return "", err
		}
		defer unix.Close(fd)

		buf := make([]byte, 64)
		n, err := unix.Read(fd, buf)
		if err != nil {
			return "", err
		}
		// Clean "0x" and whitespace
		val := strings.TrimSpace(string(buf[:n]))
		return strings.TrimPrefix(val, "0x"), nil
	}

	portName, err := readFn(portPath)
	if err != nil {
		logger.Warningf("NVMe-oFC getHostFCPorts: cannot read %s: %v", portPath, err)
		return "", err
	}
	
	if err != nil || portName == "" {
		return "", err
	}

	nodeName, err := readFn(nodePath)
	
	if err != nil {
		logger.Warningf("NVMe-oFC getHostFCPorts: cannot read %s: %v", nodePath, err)
		return "", err
	}
	
	if portName == "" || nodeName == "" {
		logger.Warningf("NVMe-oFC getHostFCPorts: empty port/node name at %s, skipping", portPath) 
		return "", err
	}
	

	// Returns exact format: nn-<node_name>:pn-<port_name>
	return fmt.Sprintf("nn-%s:pn-%s", nodeName, portName), nil
}

// Low-level helper to meet Requirement 3 & 4
func (r OsDeviceConnectivityNvmeOFc) readSysfsFC(portPath string) (string, error) {
	// 1. Get Node Name path
	nodePath := filepath.Join(filepath.Dir(portPath), "node_name")

	readFn := func(p string) (string, error) {
		// Use unix.Open for a lower footprint than os.Open
		fd, err := unix.Open(p, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			return "", err
		}
		defer unix.Close(fd)

		buf := make([]byte, 64)
		n, err := unix.Read(fd, buf)
		if err != nil {
			return "", err
		}
		return strings.TrimPrefix(strings.TrimSpace(string(buf[:n])), "0x"), nil
	}

	pn, _ := readFn(portPath)
	nn, _ := readFn(nodePath)

	if pn == "" || nn == "" {
		return "", fmt.Errorf("invalid data")
	}
	return fmt.Sprintf("nn-%s:pn-%s", nn, pn), nil
}





func (r OsDeviceConnectivityNvmeOFc) RescanDevices(_ int, _ []string) error {
	return nil
}

func (r OsDeviceConnectivityNvmeOFc) GetMpathDevice(ctx context.Context, volumeId string) (string, error) {
	return r.HelperScsiGeneric.GetMpathDevice(ctx, volumeId)
}

func (r OsDeviceConnectivityNvmeOFc) FlushMultipathDevice(ctx context.Context, mpathDevice string) error {
	return r.HelperScsiGeneric.FlushMultipathDevice(ctx, mpathDevice)
}

func (r OsDeviceConnectivityNvmeOFc) RemovePhysicalDevice(ctx context.Context, sysDevices []string) error {
	return r.HelperScsiGeneric.RemovePhysicalDevice(ctx, sysDevices)
}

func (r OsDeviceConnectivityNvmeOFc) RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error {
	return r.HelperScsiGeneric.RemoveGhostDevice(ctx, expectedSerial, expectedLun, arrayIdentifiers)
}

// TODO
func (r OsDeviceConnectivityNvmeOFc) ValidateLun(_ context.Context, _ string, _ int, _ []string, _ string) error {
	return nil
}

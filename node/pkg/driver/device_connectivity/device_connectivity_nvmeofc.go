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
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
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
	recordSize         = 1024 // NVMe Spec discovery log page entry size
	FCPortPath                          = "/sys/class/fc_host/host*/port_name"
	nvmeTargetPathCount                 = 4 // Set according to your environment's requirements
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

	hostPorts, err := r.getHostFCPorts(ctx)
	if err != nil || len(hostPorts) == 0 {
		logger.Errorf("NVMe-oFC EnsureLogin: failed to read host FC ports: %v", err)
		return
	}

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
	for arrayTargetPort := range ipsByArrayInitiator {
		if connectedPaths >= nvmeTargetPathCount {
			logger.Infof("NVMe-oFC EnsureLogin: reached target path count (%d), stopping", nvmeTargetPathCount)
			break
		}
		
		for _, hostPort := range hostPorts {
			if err := ctx.Err(); err != nil {
				logger.Warningf("NVMe-oFC EnsureLogin: context cancelled: %v", err)
				return
			}

			cleanTarget := r.normalizePortString(arrayTargetPort)
			cleanHost   := r.normalizePortString(hostPort)
			pathKey     := cleanTarget + "|" + cleanHost

			if livePaths[pathKey] {
				logger.Debugf("NVMe-oFC EnsureLogin: path already live target=%s host=%s, skipping",
					arrayTargetPort, hostPort)			
				connectedPaths++
				continue
			}

			if connectedPaths >= nvmeTargetPathCount {
				break
			}

			// ADD THIS CRITICAL PACING FIX HERE:
			// Ensures that if a previous discovery iteration failed and 'continued',
			// the kernel fabric channel has enough time to clear its locks before the next write.
			time.Sleep(150 * time.Millisecond)

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

			if r.nvmeConnect(ctx, arrayTargetPort, hostPort, subNqn) {
				connectedPaths++
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	finalLivePaths := r.getLivePathPairs(ctx)
	finalCount := countLivePathsForSubsystem(finalLivePaths, ipsByArrayInitiator)
	
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

// Helper: Normalizes variants like "nn-0x123", "nn-123", or raw "123" strings uniformly to "nn-0x123:pn-0x..."
func (r OsDeviceConnectivityNvmeOFc) normalizePortString(val string) string {
	val = strings.ToLower(strings.TrimSpace(val))
	parts := strings.Split(val, ":")
	for i, part := range parts {
		part = strings.TrimPrefix(part, "nn-")
		part = strings.TrimPrefix(part, "pn-")
		part = strings.TrimPrefix(part, "0x")
		if i == 0 {
			parts[i] = "nn-0x" + part
		} else {
			parts[i] = "pn-0x" + part
		}
	}
	return strings.Join(parts, ":")
}


// countLivePathsForSubsystem counts live paths whose traddr matches one of our array target ports.
func countLivePathsForSubsystem(livePaths map[string]bool, ipsByArrayInitiator map[string][]string) int {
	count := 0
	for pathKey := range livePaths {
		parts := strings.SplitN(pathKey, "|", 2)
		if len(parts) != 2 {
			continue
		}
		// Match using normalized strings to safely handle variations in '0x' across system configurations
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

	subsystems, err := r.Executer.FilepathGlob("/sys/class/nvme-subsystem/nvme-subsys*")
	if err != nil {
		logger.Warningf("NVMe-oFC: failed to glob subsystems: %v", err)
		return livePaths
	}

	for _, subsys := range subsystems {
		controllers, err := r.Executer.FilepathGlob(subsys + "/nvme*")
		if err != nil {
			logger.Warningf("NVMe-oFC: failed to glob controllers: %v", err)
			continue
		}

		for _, ctrl := range controllers {
			statePath := filepath.Join(ctrl, "state")
			state, err := r.readSysfsSingleLine(ctx, statePath)
			if err != nil || state != "live" {
				continue
			}

			traddr, _ := r.readSysfsSingleLine(ctx, filepath.Join(ctrl, "address"))
			hostTraddr, _ := r.readSysfsSingleLine(ctx, filepath.Join(ctrl, "host_traddr"))

			if traddr != "" && hostTraddr != "" {
				cleanTraddr := r.parseAddressField(traddr)
				cleanHostTraddr := r.parseAddressField(hostTraddr)

				// BUG FIX 2: Standardize output token normalization across paths
				normTraddr := r.normalizePortString(cleanTraddr)
				normHost   := r.normalizePortString(cleanHostTraddr)

				key := normTraddr + "|" + normHost
				livePaths[key] = true
				logger.Debugf("NVMe-oFC getLivePathPairs: live path traddr=%s host_traddr=%s", normTraddr, normHost)
			}
		}
	}
	return livePaths
}


// readSysfsSingleLine uses your ExecuteUninterruptible infra to prevent D-state hangs (Req 6)
func (r OsDeviceConnectivityNvmeOFc) readSysfsSingleLine(ctx context.Context, path string) (string, error) {
	return executer.ExecuteUninterruptible[string](
		ctx,
		r.KeyedGater, 
		path, 
		2, 1, // Decreased loop/retry density to mitigate performance blocks under scaling paths
		500*time.Millisecond, 
		1*time.Second,
		func(wCtx context.Context) (string, error) {
			data, err := r.Executer.IoutilReadFile(path)
			return strings.TrimSpace(string(data)), err
		},
	)
}

func (r OsDeviceConnectivityNvmeOFc) parseAddressField(raw string) string {
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






// extractRawWWNs splits an address string and isolates the raw hex digits by stripping out structural labels.
func (r OsDeviceConnectivityNvmeOFc) extractRawWWNs(portStr string) (string, string, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(portStr)), ":")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid port structure string encountered: %s", portStr)
	}

	// BUG FIX: Clean both the outer label string and any nested hex prefixes (0x) in sequence
	nn := strings.TrimPrefix(strings.TrimPrefix(parts[0], "nn-"), "0x")
	pn := strings.TrimPrefix(strings.TrimPrefix(parts[1], "pn-"), "0x")
	
	if nn == "" || pn == "" {
		return "", "", fmt.Errorf("parsed empty WWN values from string: %s", portStr)
	}

	return nn, pn, nil
}



// discoverSubNqn runs "nvme discover" for one (arrayTargetPort, hostPort) pair
// and returns the storage subsystem NQN. Returns ("", nil) if no path exists.
// discoverSubNqn manages target subsystem discovery commands sequentially.
func (r OsDeviceConnectivityNvmeOFc) discoverSubNqn(ctx context.Context, arrayTargetPort, hostPort string) (string, error) {
	targetNN, targetPN, err := r.extractCleanHexWWNs(arrayTargetPort)
	if err != nil {
		return "", err
	}
	_, hostPN, err := r.extractCleanHexWWNs(hostPort)
	if err != nil {
		return "", err
	}

        cmd := fmt.Sprintf("nqn=%s,transport=fc,traddr=nn-%s:pn-%s,host-traddr=%s\n",
                nvmeDiscoveryNqn, targetNN, targetPN, hostPN)


	// DEBUG PRINT BLOCK: %q shows exact escape characters like \n and spaces
	logger.Infof("NVMe-oFC DEBUG RAW DISCOVERY STRING: %q", cmd)

	rawOutput, err := executer.ExecuteUninterruptible(
		ctx, 
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
		return "", err 
	}

	// BUG FIX: Pass the raw payload string converted to bytes to maintain slice compatibility 
	subNqn := parseSubNqnFromDiscoverOutput([]byte(rawOutput))
	if subNqn != "" {
		logger.Debugf("NVMe-oFC discoverSubNqn: discovered subnqn=%s target=%s host=%s",
			subNqn, arrayTargetPort, hostPort)
	}

	return subNqn, nil
}

// executeKernelDiscovery writes the discovery payload string directly into the kernel fabrics channel.
func (r OsDeviceConnectivityNvmeOFc) executeKernelDiscovery(cmd string) (string, error) {
	f, err := os.OpenFile("/dev/nvme-fabrics", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return "", fmt.Errorf("open /dev/nvme-fabrics failed: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(cmd); err != nil {
		return "", fmt.Errorf("write to nvme-fabrics failed: %w", err)
	}

	return r.findDiscoverySubNqnFromSysfs()
}

// findDiscoverySubNqnFromSysfs scans sysfs subsystems to read and parse the dynamic discovery log.
func (r OsDeviceConnectivityNvmeOFc) findDiscoverySubNqnFromSysfs() (string, error) {
	const sysPath = "/sys/class/nvme"

	entries, err := os.ReadDir(sysPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", sysPath, err)
	}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "nvme") {
			continue
		}

		controllerPath := filepath.Join(sysPath, entry.Name())
		
		subnqnBuf, err := os.ReadFile(filepath.Join(controllerPath, "subsysnqn"))
		if err != nil {
			continue
		}
		
		if strings.TrimSpace(string(subnqnBuf)) != nvmeDiscoveryNqn {
			continue
		}

		logPath := filepath.Join(controllerPath, "discovery_log")
		
		// FIX: Add a polling backoff mechanism to wait for the kernel's async sysfs population
		var logFile *os.File
		var openErr error
		for attempts := 0; attempts < 5; attempts++ {
			logFile, openErr = os.Open(logPath)
			if openErr == nil {
				break
			}
			// If it's a "not found" error, wait 15ms and try again
			if os.IsNotExist(openErr) {
				time.Sleep(15 * time.Millisecond)
				continue
			}
			return "", fmt.Errorf("failed to open discovery log from %s: %w", entry.Name(), openErr)
		}
		if openErr != nil {
			return "", fmt.Errorf("kernel failed to expose discovery log path within timeout: %w", openErr)
		}
		
		// 16-byte log page header + space for up to 64 records
		logBuf := make([]byte, 16+(64*recordSize))
		n, err := io.ReadFull(logFile, logBuf)
		logFile.Close() // Explicitly close file before deleting the temporary controller
		
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return "", fmt.Errorf("failed to pull complete stream data: %w", err)
		}

		// Clean up the transient discovery controller immediately to avoid host resource leakage
		deletePath := filepath.Join(controllerPath, "delete_controller")
		_ = os.WriteFile(deletePath, []byte("1\n"), 0200)

		subNqn := parseSubNqnFromDiscoverOutput(logBuf[:n])
		if subNqn != "" {
			return subNqn, nil
		}
	}

	return "", fmt.Errorf("no active discovery controller found in sysfs")
}

// NVMe Discovery Log Page Entry Structure
// Compliant with the NVMe Base Specification (1024 bytes per entry).
type nvmeDiscoveryLogEntry struct {
	Trtype      uint8     `header:"trtype"`      // Transport type (0x2 = RDMA, 0x3 = FC, 0x4 = TCP)
	Adrfam      uint8     `header:"adrfam"`      // Address family (0x1 = IPv4, 0x2 = IPv6, 0x3 = Fibre Channel)
	Subtype     uint8     `header:"subtype"`     // Subsystem type (0x2 = NVMe Storage Subsystem)
	Treq        uint8     `header:"treq"`        // Transport requirements
	Portid      uint16    `header:"portid"`      // Port ID
	Cntlid      uint16    `header:"cntlid"`      // Controller ID
	Asqsz       uint32    `header:"asqsz"`       // Admin Submission Queue Size
	Reserved    [20]byte  `header:"reserved"`    // Reserved bytes to pad out entry header block
	Trsvcid     [32]byte  `header:"trsvcid"`     // Transport Service ID (e.g. port number or service port)
	SubnqnBytes [256]byte `header:"subnqn"`      // The raw, null-padded Target Subsystem NQN we need
	TraddrBytes [256]byte `header:"traddr"`      // Transport address (e.g. target IP or WWN strings)
	Tsas        [256]byte `header:"tsas"`        // Transport Specific Address Subtype
}


// parseSubNqnFromDiscoverOutput extracts the storage subsystem NQN from "nvme discover" output.
// Skips the discovery controller NQN (nqn.2014-08.org.nvmexpress.discovery).
func parseSubNqnFromDiscoverOutput(rawBytes []byte) string {
	if len(rawBytes) < 16 { // Ensure header preamble minimum exists
		return ""
	}

	// The first 16 bytes contain the Discovery Log Page Header
	// Generation Counter (uint64) + Number of Records (uint64)
	if len(rawBytes) < 16 {
		return ""
	}
	
	numRecords := binary.LittleEndian.Uint64(rawBytes[8:16])
	if numRecords == 0 {
		return ""
	}

	// Offset past the 16-byte Discovery Log Page header to reach records
	offset := 16

	for i := uint64(0); i < numRecords; i++ {
		if offset+recordSize > len(rawBytes) {
			break
		}

		var entry nvmeDiscoveryLogEntry
		buffer := bytes.NewReader(rawBytes[offset : offset+recordSize])
		err := binary.Read(buffer, binary.LittleEndian, &entry)
		if err != nil {
			return ""
		}

		// Advance pointer to the next record block
		offset += recordSize

		// Filter out records that are not standard storage subsystems (subtype 0x2)
		if entry.Subtype != 0x02 {
			continue
		}

		// Extract string from null-padded byte array
		subNqn := string(bytes.Trim(entry.SubnqnBytes[:], "\x00"))
		subNqn = strings.TrimSpace(subNqn)

		// Ignore empty matches or connections pointing back to the discovery target itself
		if subNqn == "" || subNqn == nvmeDiscoveryNqn {
			continue
		}

		// Successfully extracted the unique operational volume NQN
		return subNqn
	}

	return ""
}


// nvmeConnect executes direct writes onto the /dev/nvme-fabrics channel
func (r OsDeviceConnectivityNvmeOFc) nvmeConnect(ctx context.Context, arrayTargetPort, hostPort, subNqn string) bool {
	targetNN, targetPN, err := r.extractCleanHexWWNs(arrayTargetPort)
	if err != nil {
		logger.Errorf("NVMe-oFC nvmeConnect: target error: %v", err)
		return false
	}
	_, hostPN, err := r.extractCleanHexWWNs(hostPort)
	if err != nil {
		logger.Errorf("NVMe-oFC nvmeConnect: host error: %v", err)
		return false
	}

        options := fmt.Sprintf("nqn=%s,transport=fc,traddr=nn-%s:pn-%s,host-traddr=%s\n",
                subNqn, targetNN, targetPN, hostPN)


	// DEBUG PRINT BLOCK: %q shows exact escape characters like \n and spaces
	logger.Infof("NVMe-oFC DEBUG RAW CONNECT STRING: %q", options)

	resourceKey := fmt.Sprintf("connect-%s-%s", subNqn, arrayTargetPort)
	
	out, err := executer.ExecuteUninterruptible(
		ctx,
		r.KeyedGater, resourceKey, 1, 1, 5*time.Second, 30*time.Second,
		func(wCtx context.Context) (string, error) {
			f, err := os.OpenFile("/dev/nvme-fabrics", os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				return "", fmt.Errorf("failed to open fabrics device: %w", err)
			}
			defer f.Close()

			_, err = f.WriteString(options)
			if err != nil {
				// BUG FIX 2: Implement robust POSIX error identification using errors.Is instead of text matching
				if errors.Is(err, syscall.EALREADY) || errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.EBUSY) {
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
	
	logger.Infof("NVMe-oFC nvmeConnect: connected NQN=%s target=%s host=%s result=%s", subNqn, arrayTargetPort, hostPort, out)
	return true
}

func (r OsDeviceConnectivityNvmeOFc) extractPreservedWWNs(portStr string) (string, string, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(portStr)), ":")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid port structure string encountered: %s", portStr)
	}

	// Only strip the network labels (nn- / pn-). Leave the rest of the string exactly as it arrived.
	nn := strings.TrimPrefix(parts[0], "nn-")
	pn := strings.TrimPrefix(parts[1], "pn-")
	
	if nn == "" || pn == "" {
		return "", "", fmt.Errorf("parsed empty WWN values from string: %s", portStr)
	}

	return nn, pn, nil
}

func (r OsDeviceConnectivityNvmeOFc) extractCleanHexWWNs(portStr string) (string, string, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(portStr)), ":")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid port structure string encountered: %s", portStr)
	}

	nn := strings.TrimPrefix(parts[0], "nn-")
	pn := strings.TrimPrefix(parts[1], "pn-")
	
	nn = strings.TrimPrefix(nn, "0x")
	pn = strings.TrimPrefix(pn, "0x")

	if nn == "" || pn == "" {
		return "", "", fmt.Errorf("parsed empty WWN values from string: %s", portStr)
	}

	return "0x" + nn, "0x" + pn, nil
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
		hostName := filepath.Base(filepath.Dir(portPath))

		res, err := executer.ExecuteUninterruptible(
			ctx,
			r.KeyedGater,
			hostName, 
			1, 1, 
			2*time.Second, 5*time.Second,
			func(workerCtx context.Context) (string, error) {
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

// readFCPortPairDirect cleanly reads specific hardware details bypassing high-overhead mechanisms
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
		return strings.TrimSpace(string(buf[:n])), nil
	}

	portName, err := readFn(portPath)
	if err != nil {
		logger.Warningf("NVMe-oFC getHostFCPorts: cannot read %s: %v", portPath, err)
		return "", err
	}

	nodeName, err := readFn(nodePath)
	if err != nil {
		logger.Warningf("NVMe-oFC getHostFCPorts: cannot read %s: %v", nodePath, err)
		return "", err
	}
	
	if portName == "" || nodeName == "" {
		logger.Warningf("NVMe-oFC getHostFCPorts: empty port/node name at %s, skipping", portPath) 
		return "", fmt.Errorf("empty hardware description fields encountered")
	}
	
	// BUG FIX 1 & 2: Maintain original format strings uniformly across all modules
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
		return strings.TrimSpace(string(buf[:n])), nil
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

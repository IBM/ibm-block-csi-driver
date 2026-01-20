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

package executer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
)

//go:generate mockgen -destination=../../../mocks/mock_executer.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer ExecuterInterface
type ExecuterInterface interface { // basic host dependent functions
	ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error)
	ExecuteWithTimeoutSilently(mSeconds int, command string, args []string) ([]byte, error)
	OsOpenFile(name string, flag int, perm os.FileMode) (*os.File, error)
	OsReadlink(name string) (string, error)
	FilepathGlob(pattern string) (matches []string, err error)
	IoutilReadDir(dirname string) ([]os.FileInfo, error)
	IoutilReadFile(filename string) ([]byte, error)
	FileWriteString(f *os.File, s string) (n int, err error)
	IsExecutable(path string) error
	GetExitCode(err error) (int, bool)
}

type Executer struct {
}


const DefaultMaxOutput = 1024 * 1024 

//Command Category
//	Typical Size	Recommended Limit	Why?
//Transaction (mount, login, remove)	< 1KB	64KB	Prevents "log spam" from eating your RAM.
//Status (multipath -ll, iscsiadm -m node)	10KB - 1MB	2MB	Enough for typical node density.
//nventory (discoverydb, scan)	1MB - 5MB	10MB	Prevents truncation on high-density nodes.


type limitWriter struct {
	io.Writer
	Limit int64
	curr  int64
}

func (w *limitWriter) Write(p []byte) (n int, err error) {
	if w.curr >= w.Limit {
		return len(p), nil // Silently drop overflow
	}
	toWrite := int64(len(p))
	if w.curr+toWrite > w.Limit {
		toWrite = w.Limit - w.curr
	}
	n, err = w.Writer.Write(p[:toWrite])
	w.curr += int64(n)
	return len(p), err // Return len(p) to avoid "short write" errors in cmd
}




type zombieInfo struct {
	pid       int
	command   string
	startTime uint64 // Use the raw Jiffies/ClockTicks from /proc
}

// Ensure you capture the start time immediately after cmd.Start()
func getPidStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	_, startTime, err := parseStatFile(data)
	return startTime, err
}

func (e *Executer) markAsStuck(device string, pid int, command string) {
	// Fetch the unique start time for this specific PID instance
	startTime, _ := getPidStartTime(pid)
	
	stuckMu.Lock()
	stuckProcesses[device] = zombieInfo{
		pid:       pid,
		command:   filepath.Base(command),
		startTime: startTime,
	}
	stuckMu.Unlock()
}


func (e *Executer) ExecuteWithTimeoutSilently(timeoutMs int, command string, args []string) ([]byte, error) {
	return ExecuteWithTracking("", timeoutMs, string, args)
}

func (e *Executer) ExecuteWithTracking(device string, timeoutMs int, command string, args []string) ([]byte, error) {
	// 1. Pre-check: Don't spawn a new process if one is already wedged
	if device != "" && isDeviceStillStuck(device) {
		return nil, fmt.Errorf("node-safety: previous %s process is still stuck in kernel D-state for device %s", command, device)
	}
	
	
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	// WaitDelay ensures the Go routine returns even if the process is in D-state after SIGKILL
	cmd.WaitDelay = 2 * time.Second

	// We use Start() + Wait() instead of CombinedOutput() to capture the PID correctly
	// Combined output buffer
	var output bytes.Buffer
	// Limit total output to prevent OOM from chatty commands
	limitWriter := &limitWriter{Writer: &output, Limit: DefaultMaxOutput}
	cmd.Stdout = limitWriter
	cmd.Stderr = limitWriter
	
    if err := cmd.Start(); err != nil {
        return nil, fmt.Errorf("failed to start command: %w", err)
    }	
	
    pid := cmd.Process.Pid
    err := cmd.Wait()
    captured := output.Bytes()

    if err != nil {
        // Check specifically for context timeout or WaitDelay expiration
        if errors.Is(err, exec.ErrWaitDelay) || ctx.Err() != nil {
            if device != "" {
				e.markAsStuck(device, pid, command)
			}
            return captured, fmt.Errorf("process %d hung on %s: %w", pid, device, err)
        }
        return captured, fmt.Errorf("exit error: %w", err)
    }

    // Success: Device is healthy, clear any existing block
	if device != "" {
	    clearTracking(device)
	}
    return captured, nil
}





func clearTracking(device string) {
	stuckMu.Lock()
	delete(stuckProcesses, device)
	stuckMu.Unlock()
}





func isDeviceStillStuck(device string) bool {
	stuckMu.Lock()
	info, exists := stuckProcesses[device]
	stuckMu.Unlock()

	if !exists {
		return false
	}

	// 1. Read raw data to handle parentheses safely
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", info.pid))
	if err != nil {
		clearTracking(device) // PID no longer exists
		return false
	}

	state, currentStart, err := parseStatFile(statData)
	if err != nil {
		return false
	}
	

	// 2. IDENTITY VERIFICATION: The "Gold Standard"
	// If the start time differs, the PID was reused by a different process.
	if currentStart != info.startTime {
		logger.Debugf("PID %d was reused (old start: %d, new start: %d)", info.pid, info.startTime, currentStart)
		clearTracking(device)
		return false
	}

    // 1. Attempt user-space identification via cmdline
    cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", info.pid))
    if err == nil && len(cmdline) > 0 {
        // Standard user-space process (iscsiadm, multipath, etc.)
        cleanCmd := bytes.ReplaceAll(cmdline, []byte{0}, []byte{' '})
        if !strings.Contains(string(cleanCmd), device) {
            clearTracking(device)
            return false
        }
    } else {
        // 2. FALLBACK: Handle Kernel Workers (jbd2, xfsaild, dm-*)
        // These have empty cmdlines; we must verify identity via 'comm'
        // 'comm' for jbd2 often looks like: (jbd2/dm-2-8)
        if strings.Contains(info.command, "jbd2") || 
           strings.Contains(info.command, "xfsaild") || 
           strings.Contains(info.command, "dm-") {
            
            // For kernel workers, the 'device' name is often part of the 'comm' field
            // e.g., 'jbd2/sda1' or 'xfsaild/dm-0'
            if !strings.Contains(info.command, filepath.Base(device)) {
                 // Command doesn't match our target device
                 clearTracking(device)
                 return false
            }
            logger.Warnf("Kernel worker %s is stuck for device %s", info.command, device)
        } else {
            // Unidentified process with empty cmdline (could be a zombie user task)
            clearTracking(device)
            return false
        }
    }
	
    // 3. Check State & WCHAN
    // D = Uninterruptible sleep (usually IO)
    // S = Interruptible sleep (but potentially stuck in storage RPC)
    isStuck := (state == 'D' || (state == 'S' && isStorageWait(info.pid)))

    if !isStuck {
        clearTracking(device)
        return false
    }

    return true // Process is still there and still in a blocking state	
}





func isStorageWait(pid int) bool {
	wchan, err := os.ReadFile(fmt.Sprintf("/proc/%d/wchan", pid))
	if err != nil { return false }
	w := string(wchan)
	
	// 'io_schedule' is the generic kernel indicator of waiting for disk I/O
	return strings.Contains(w, "nfs_wait") || 
	       strings.Contains(w, "rpc_wait") || 
	       strings.Contains(w, "scsi_wait") ||
	       strings.Contains(w, "blk_mq_wait") || 
	       strings.Contains(w, "nvme_wait") ||
	       strings.Contains(w, "io_schedule") || // Generic I/O wait
	       strings.Contains(w, "xfs_log_wait")
}



func parseStatFile(data []byte) (state byte, startTime uint64, err error) {
	// The comm field ends at the LAST ')'. 
	// Everything before that is PID and COMM.
	lastParen := bytes.LastIndex(data, []byte(")"))
	if lastParen == -1 || len(data) <= lastParen+2 {
		return 0, 0, fmt.Errorf("invalid stat format")
	}

	// Field 3 (State) is at index 0 after the ") "
	afterParen := string(data[lastParen+2:])
	fields := strings.Fields(afterParen)

	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("stat file too short")
	}

	state = fields[0][0]
	// Starttime is index 19 (Field 22 - PID, COMM, STATE = 19 fields later)
	startTime, err = strconv.ParseUint(fields[19], 10, 64)
	return state, startTime, err
}



func (e *Executer) ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error) {
	logger.Debugf("Executing command : {%v} with args : {%v}. and timeout : {%v} mseconds", command, args, mSeconds)

	out, err := e.ExecuteWithTimeoutSilently(mSeconds, command, args)

	outAsStr := string(out)
	noOutputMessage := ""
	if strings.TrimSpace(outAsStr) != "" {
		logger.Debugf("Output from command: %s", outAsStr)
	} else {
		noOutputMessage = " (no output)"
	}
	logger.Debugf("Finished executing command %s", noOutputMessage)
	return out, err
}

var (
	cachedSocket string
	socketMu     sync.RWMutex
)

const (
    // Use '@' for Go's internal abstract namespace handling
    AbstractSocketPath = "@/org/kernel/linux/storage/multipathd"
    StandardSocket     = "/run/multipathd.sock"
    LegacySocket       = "/var/run/multipathd.sock"
)

func resolveSocket() string {
    if env := os.Getenv("MULTIPATH_SOCKET_NAME"); env != "" {
        return env
    }

    // Try abstract first - no filesystem cleanup needed
    if conn, err := net.DialTimeout("unix", AbstractSocketPath, 50*time.Millisecond); err == nil {
        conn.Close()
        return AbstractSocketPath
    }

    // Check filesystem paths
    for _, path := range []string{StandardSocket, LegacySocket} {
        if conn, err := net.DialTimeout("unix", path, 50*time.Millisecond); err == nil {
            conn.Close()
            return path
        }
    }

    return StandardSocket
}


// GetSocket returns the cached socket or discovers it if empty.
func GetSocket() string {
	socketMu.RLock()
	s := cachedSocket
	socketMu.RUnlock()

	if s != "" {
		return s
	}

	socketMu.Lock()
	defer socketMu.Unlock()
	// Double-check to prevent race
	if cachedSocket == "" {
		cachedSocket = resolveSocket()
	}
	return cachedSocket
}

// invalidateSocket clears the cache if a connection fails.
func invalidateSocket() {
	socketMu.Lock()
	cachedSocket = ""
	socketMu.Unlock()
}

func MultipathdCmd(command string) (string, error) {
	socketPath := GetSocket()

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		invalidateSocket()
		socketPath = GetSocket()
		conn, err = net.DialTimeout("unix", socketPath, 2*time.Second)
		if err != nil {
			return "", fmt.Errorf("multipathd unreachable: %w", err)
		}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Write: Header (10 bytes) + Command + Newline
	// Multipathd expects the length to include the newline but not the header itself
	payload := command + "\n"
	header := fmt.Sprintf("%10d", len(payload)) // Standard multipathd uses space-padded 10 char
	if _, err := conn.Write([]byte(header + payload)); err != nil {
		return "", err
	}

	// Read Header
	lenBuf := make([]byte, 10)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return "", fmt.Errorf("resp header read fail: %w", err)
	}

	respLen, err := strconv.Atoi(strings.TrimSpace(string(lenBuf)))
	if err != nil || respLen <= 0 {
		return "", fmt.Errorf("invalid resp length: %v", string(lenBuf))
	}

	// Read Payload: Use io.ReadAll to avoid bufio.Scanner's 64KB limit
	reader := io.LimitReader(conn, int64(respLen))
	respBody, err := io.ReadAll(reader)

	if err != nil {
		return "", err
	}

	return string(respBody), nil
}



func (e *Executer) OsOpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}

func (e *Executer) OsReadlink(name string) (string, error) {
	return os.Readlink(name)
}

func (e *Executer) FilepathGlob(pattern string) (matches []string, err error) {
	return filepath.Glob(pattern)
}

func (e *Executer) IoutilReadDir(dirname string) ([]os.FileInfo, error) {
	return ioutil.ReadDir(dirname)
}

func (e *Executer) IoutilReadFile(filename string) ([]byte, error) {
	return ioutil.ReadFile(filename)
}

func (e *Executer) FileWriteString(f *os.File, s string) (n int, err error) {
	return f.WriteString(s)
}

func (e *Executer) IsExecutable(path string) error {
	_, err := exec.LookPath(path)
	return err
}

func (e *Executer) GetExitCode(err error) (int, bool) {
	if exitError, isExitError := err.(*exec.ExitError); isExitError {
		return exitError.ExitCode(), true
	}
	return 0, false
}

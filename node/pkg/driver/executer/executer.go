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
	"sync/atomic"
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

type SocketLimiter struct {
        sem          chan struct{}
        lastFail     time.Time
        mu           sync.RWMutex
        failureCount atomic.Int32
}

type Executer struct {
	stuckProcesses map[string]zombieInfo
	stuckMu        sync.Mutex

	cachedSocket string

	sl SocketLimiter
}

func NewExecuter() * Executer {
	return &Executer{
		stuckProcesses: make(map[string]zombieInfo),
	}
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




func (e *Executer) ExecuteWithTimeoutSilently(timeoutMs int, command string, args []string) ([]byte, error) {
	return e.ExecuteWithTracking("", timeoutMs, command, args)
}


type zombieInfo struct {
	pid       int
	command   string
	startTime uint64 // Use the raw Jiffies/ClockTicks from /proc
}

// Ensure you capture the start time immediately after cmd.Start()
func (e *Executer) getPidStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	_, startTime, err := e.parseStatFile(data)
	return startTime, err
}

func (e *Executer) markAsStuck(device string, pid int, command string) {
	// Fetch the unique start time for this specific PID instance
	startTime, _ := e.getPidStartTime(pid)

	e.stuckMu.Lock()
	e.stuckProcesses[device] = zombieInfo{
		pid:       pid,
		command:   filepath.Base(command),
		startTime: startTime,
	}
	e.stuckMu.Unlock()
}



func (e *Executer) ExecuteWithTracking(device string, timeoutMs int, command string, args []string) ([]byte, error) {
	// 1. Pre-check: Don't spawn a new process if one is already wedged
	if device != "" && e.isDeviceStillStuck(device) {
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
	    e.clearTracking(device)
	}
    return captured, nil
}


func (e *Executer) isDeviceStillStuck(device string) bool {
	e.stuckMu.Lock()
	info, exists := e.stuckProcesses[device]
	e.stuckMu.Unlock()

	if !exists {
		return false
	}

	// 1. SAFE READ: stat is memory-resident and will not hang.
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", info.pid))
	if err != nil {
		e.clearTracking(device) // Process gone
		return false
	}

	state, currentStart, err := e.parseStatFile(statData)
	if err != nil || currentStart != info.startTime {
		e.clearTracking(device) // PID recycled or corrupt file
		return false
	}

	// 2. SAFE IDENTITY VERIFICATION: Use the buffer from stat
	startParen := bytes.Index(statData, []byte("("))
	lastParen := bytes.LastIndex(statData, []byte(")"))
	if startParen != -1 && lastParen > startParen {
		currentComm := string(statData[startParen+1 : lastParen])

		// Handle 15-char kernel truncation [Source: TASK_COMM_LEN]
		if e.commMatches(currentComm, info.command) {
			if e.verifyStuckState(info.pid, state) {
				return true
			}
		}
	}

	// 3. FALLBACK FOR KERNEL WORKERS: (jbd2, xfsaild, dm-*)
	if e.isTargetKernelWorker(info.command, device) {
		if e.verifyStuckState(info.pid, state) {
			return true
		}
	}

	// 4. D-STATE GATEKEEPER: If we reach here and it's 'D', do NOT read cmdline.
	// Reading cmdline of a D-state process can hang your monitor indefinitely.
	if state == 'D' {
		return true
	}

	// 5. CLEANUP & VERIFY: Standard processes (iscsiadm, etc.)
	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", info.pid)); err == nil && len(cmdline) > 0 {
		cleanCmd := string(bytes.ReplaceAll(cmdline, []byte{0}, []byte{' '}))
		if strings.Contains(cleanCmd, device) {
			return e.verifyStuckState(info.pid, state)
		}
	}

	e.clearTracking(device)
	return false
}

func (e *Executer) isTargetKernelWorker(command, device string) bool {
	workers := []string{"jbd2", "xfsaild", "dm-", "kworker", "ext4-rsv-conve"}
	for _, w := range workers {
		if strings.Contains(command, w) {
			return strings.Contains(command, filepath.Base(device))
		}
	}
	return false
}

func (e *Executer) commMatches(current, target string) bool {
	if current == target {
		return true
	}
	// TASK_COMM_LEN is 16 including null terminator
	return len(current) == 15 && strings.HasPrefix(target, current)
}

func (e *Executer) parseStatFile(data []byte) (state byte, startTime uint64, err error) {
	lastParen := bytes.LastIndex(data, []byte(")"))
	if lastParen == -1 || len(data) <= lastParen+2 {
		return 0, 0, fmt.Errorf("invalid stat format")
	}

	afterParen := string(data[lastParen+2:])
	fields := strings.Fields(afterParen)
	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("stat file too short")
	}

	state = fields[0][0]
	// Field 22 is start_time, which is index 19 after the comm field
	startTime, err = strconv.ParseUint(fields[19], 10, 64)
	return state, startTime, err
}

func (e *Executer) verifyStuckState(pid int, state byte) bool {
	return state == 'D' || (state == 'S' && e.isStorageWait(pid))
}

func (e *Executer) isStorageWait(pid int) bool {
	wchan, err := os.ReadFile(fmt.Sprintf("/proc/%d/wchan", pid))
	if err != nil {
		return false
	}
	w := string(wchan)

	return strings.Contains(w, "nfs_wait") || strings.Contains(w, "rpc_wait") ||
		strings.Contains(w, "blk_mq_wait") || strings.Contains(w, "nvme_wait") ||
		strings.Contains(w, "scsi_wait") || strings.Contains(w, "xfs_log_wait") ||
		strings.Contains(w, "io_schedule") || strings.Contains(w, "fuse_request") ||
		strings.Contains(w, "dm_make_request") || strings.Contains(w, "xfs_log") ||
		strings.Contains(w, "multipath_wait")
}

func (e *Executer) clearTracking(device string) {
	e.stuckMu.Lock()
	delete(e.stuckProcesses, device)
	e.stuckMu.Unlock()
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




const (
    // Use '@' for Go's internal abstract namespace handling
    AbstractSocketPath = "\x00/org/kernel/linux/storage/multipathd"
    StandardSocket     = "/run/multipathd.sock"
    LegacySocket       = "/var/run/multipathd.sock"
)

func (e *Executer) resolveSocket() string {
	candidates := []string{
		"\x00/org/kernel/linux/storage/multipathd", // Use \x00 for Go abstract sockets
		"\x00multipathd",                            // Legacy abstract
		"/run/multipathd.sock",
		"/var/run/multipathd.sock",
	}

	// 2. User-defined override remains top priority
	if env := os.Getenv("MULTIPATH_SOCKET_NAME"); env != "" {
		candidates = append([]string{env}, candidates...)
	}

	for _, path := range candidates {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return path
		}
	}
	// 4. Default Fallback
	// If everything fails, return the standard path so error messages are helpful
	return StandardSocket
}



// GetSocket returns the cached socket or discovers it if empty.
func (e *Executer) GetSocket() string {
	e.socketMu.RLock()
	s := e.cachedSocket
	e.socketMu.RUnlock()

	if s != "" {
		return s
	}

	e.socketMu.Lock()
	defer e.socketMu.Unlock()
	// Double-check to prevent race
	if e.cachedSocket == "" {
		e.cachedSocket = e.resolveSocket()
	}
	return e.cachedSocket
}

// invalidateSocket clears the cache if a connection fails.
func (e *Executer) invalidateSocket() {
	e.socketMu.Lock()
	e.cachedSocket = ""
	e.socketMu.Unlock()
}


func (e *Executer) MultipathdCmd(command string) (string, error) {
	socketPath := e.GetSocket()

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		e.invalidateSocket()
		socketPath = e.GetSocket() // Try once more after invalidating
		conn, err = net.DialTimeout("unix", socketPath, 2*time.Second)
		if err != nil {
			return "", fmt.Errorf("multipathd unreachable: %w", err)
		}
	}
	defer conn.Close()

	// Set strict deadline for both Read and Write
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// 1. Send Command
	// Protocol: "LENGTH(10 bytes)PAYLOAD\n"
	payload := command + "\n"
	header := fmt.Sprintf("%10d", len(payload))
	if _, err := conn.Write([]byte(header + payload)); err != nil {
		return "", fmt.Errorf("failed to send command: %w", err)
	}

	// 2. Read Response Header (10 bytes)
	lenBuf := make([]byte, 10)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return "", fmt.Errorf("failed to read response header: %w", err)
	}

	// 3. Parse Length with extra safety
	trimmedLen := strings.TrimSpace(string(lenBuf))
	respLen, err := strconv.Atoi(trimmedLen)

	// TODO is respLen < 0 considered error or success
	if err != nil {
		return "", fmt.Errorf("protocol error: invalid response length %q", trimmedLen)
	}

	// Safety check: Don't allocate more than 1MB (your DefaultMaxOutput)
	if respLen > DefaultMaxOutput {
		return "", fmt.Errorf("protocol error: response size %d exceeds limit", respLen)
	}

	if respLen <= 0 {
		return "", nil
	}

	// 4. Read Body
	// Use LimitReader to prevent reading past the protocol-defined length
	respBody, err := io.ReadAll(io.LimitReader(conn, int64(respLen)))
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	response := strings.TrimSpace(string(respBody))

	// 5. Logical Error Handling
	if strings.HasPrefix(response, "fail") {
		return "", fmt.Errorf("multipathd error: %s", response)
	}

	if response == "timeout" {
		return "", fmt.Errorf("multipathd internal timeout")
	}

	return response, nil
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

func (e *Executer) TimeoutWrapper(timeout time.Duration, action func() error) error {
	// MUST be a buffered channel of size 1
	ch := make(chan error, 1)

	go func() {
		// This might hang forever in the kernel
		ch <- action()
	}()

	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		// We "kill" the operation by simply moving on.
		// The goroutine above is LEAKED. It stays in D-state
		// until the kernel wakes up, but our CSI driver continues.
		return fmt.Errorf("timeout: abandoning hanging goroutine")
	}
}


//Ensure your e.sl.sem has a small capacity (usually 1 to 3). Since multipathd processes commands serially, sending 50 concurrent requests will just fill the kernel's socket backlog and trigger the very timeouts you are trying to avoid.

func (e *Executer) MultipathdCmdLimiter(ctx context.Context, action func() error) error {
	// 1. Fail-Fast (Circuit Breaker)
	e.sl.mu.RLock()
	// If we've failed recently and frequently, don't even try.
	if e.sl.failureCount.Load() > 3 && time.Since(e.sl.lastFail) < 30*time.Second {
		e.sl.mu.RUnlock()
		return fmt.Errorf("multipathd-safety: circuit breaker open (last failure: %v)", e.sl.lastFail)
	}
	e.sl.mu.RUnlock()

	// 2. Concurrency Control (Semaphore)
	select {
	case e.sl.sem <- struct{}{}:
		defer func() { <-e.sl.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// 3. Execute
	err := action()

	// 4. Update Circuit State
	if err != nil {
		// Only trigger the breaker on transport errors or timeouts,
		// not logical "fail" responses from the daemon.
		if e.isTransportError(err) {
			e.sl.mu.Lock()
			e.sl.lastFail = time.Now()
			e.sl.failureCount.Add(1)
			e.sl.mu.Unlock()
		}
	} else {
		// Reset failure count on success
		e.sl.failureCount.Store(0)
	}

	return err
}

// Helper to identify if the error is a socket/timeout issue vs a logical multipath error
func (e *Executer) isTransportError(err error) bool {
    if err == nil {
        return false
    }
    // Check if it's a native Go net timeout
    if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
        return true
    }

    msg := strings.ToLower(err.Error())
    return strings.Contains(msg, "timeout") ||
           strings.Contains(msg, "unreachable") ||
           strings.Contains(msg, "refused") ||
           strings.Contains(msg, "no such file")
}





const (
	MultipathdSocket = "/var/run/multipathd.sock"
	CheckTimeout     = 2 * time.Second
)

// IsProcessRunning checks if a process with the given name exists in /proc
func (e *Executer) IsProcessRunning(name string) (bool, error) {
	// Read the /proc directory
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		// PIDs are represented as numeric directories
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}

		// Read /proc/[PID]/comm to get the executable name
		commPath := filepath.Join("/proc", entry.Name(), "comm")
		commContent, err := os.ReadFile(commPath)
		if err != nil {
			// Process might have terminated since we listed the directory
			continue
		}

		// Clean up trailing newline and compare
		if string(bytes.TrimSpace(commContent)) == name {
			return true, nil
		}
	}

	return false, nil
}

type MultipathHealth struct {
	IsRunning bool
	IsStuck   bool
	Error     error
}

func (e *Executer) CheckMultipathdHealth() MultipathHealth {
	// 1. Check if process exists (e.g., via pgrep or systemctl)
	// If the binary isn't in the process list, it's definitely not running.
	if !e.IsProcessRunning("multipathd") {
		return MultipathHealth{IsRunning: false, Error: fmt.Errorf("multipathd process not found")}
	}

	// TODO IsMultipathdAlive-like logic follows

	// 2. Multi-layered check: Socket Connection + Command Response
	// We use a context to strictly enforce a timeout for the entire check.
	ctx, cancel := context.WithTimeout(context.Background(), CheckTimeout)
	defer cancel()

	// Try to dial the socket. This confirms the listener exists.
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", MultipathdSocket)
	if err != nil {
		return MultipathHealth{IsRunning: true, IsStuck: true, Error: fmt.Errorf("socket connection failed: %w", err)}
	}
	defer conn.Close()

	// 3. Perform a "No-Op" Command via the CLI
	// Simply connecting to the socket isn't enough; we need to see if it responds.
	// 'multipathd show status' is lightweight and non-intrusive.
	cmd := exec.CommandContext(ctx, "multipathd", "show", "status")
	output, err := cmd.CombinedOutput()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return MultipathHealth{IsRunning: true, IsStuck: true, Error: fmt.Errorf("multipathd is unresponsive (deadlock suspected)")}
		}
		return MultipathHealth{IsRunning: true, IsStuck: false, Error: fmt.Errorf("command error: %w", err)}
	}

	// 4. Validate output
	if !strings.Contains(string(output), "status:") && !strings.Contains(string(output), "up") {
		return MultipathHealth{IsRunning: true, IsStuck: false, Error: fmt.Errorf("unexpected output: %s", string(output))}
	}

	return MultipathHealth{IsRunning: true, IsStuck: false}
}


// Integrated check for CSI NodePublish/Unpublish stages
func (e *Executer) VerifyStorageStack(device string) error {
	// 1. Check if the specific device task is stuck in the kernel
	if e.isDeviceStillStuck(device) {
		return fmt.Errorf("IO-Hangups: device %s has a stuck kernel worker", device)
	}

	// 2. Check if the management daemon is healthy
	if alive, err := e.IsMultipathdAlive(); !alive {
		// Log warning but evaluate if you want to hard-fail.
		// Note: Linux DM continues to route IO even if the daemon is dead.
		logger.Warningf("Multipathd health check failed: %v", err)

		// If it's a timeout (stuck), it's dangerous to proceed with mount/unmount
		if strings.Contains(err.Error(), "deadlock") {
			return err
		}
	}

	return nil
}


/ IsMultipathdAlive performs a liveness check by sending a no-op command.
// It distinguishes between "stopped" (connection refused) and "stuck" (timeout).
func (e *Executer) IsMultipathdAlive() (bool, error) {
	// We use 'show status' because it's a fast, read-only internal no-op.
	// If the event loop is deadlocked (D-state), this will trigger the
	// 5s deadline set in MultipathdCmd.
	resp, err := e.MultipathdCmd("show status")

	if err != nil {
		// If the error is a timeout, the daemon is likely stuck in D-state
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return false, fmt.Errorf("multipathd is unresponsive (deadlock suspected): %w", err)
		}

		// If the error is "connection refused", the daemon is simply not running
		if strings.Contains(err.Error(), "unreachable") || strings.Contains(err.Error(), "refused") {
			return false, fmt.Errorf("multipathd service is not running")
		}

		return false, err
	}

	// Verify we got a sane response (usually "up" or "multipathd vX.X.X")
	if resp == "" {
		return false, fmt.Errorf("multipathd returned empty response")
	}

	return true, nil
}


func (e *Executer) IsMultipathEnabled() (bool, error) {
	// 1. Attempt the command
	resp, err := e.MultipathdCmd("show status")

	if err != nil {
		// Scenario A: multipathd is legitimately down/not installed
		// We check for "unreachable", "refused", or "no such file"
		if strings.Contains(err.Error(), "unreachable") ||
		   strings.Contains(err.Error(), "refused") ||
		   strings.Contains(err.Error(), "no such file") {
			return false, nil // Legitimate state: Single path fallback
		}

		// Scenario B: multipathd is STUCK (Timeout)
		// Your MultipathdCmd deadline (5s) triggered.
		// This is dangerous because it indicates an IO hang.
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return true, fmt.Errorf("multipathd is deadlocked (D-state suspected): %w", err)
		}

		// Scenario C: Other errors (permissions, etc.)
		return false, err
	}

	// Scenario D: Up and responding
	return resp != "", nil
}


func (e *Executer) isMultipathdRunning() bool {
    // If we can dial any known socket, the daemon is active
    socket := e.resolveSocket()
    conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
    if err != nil {
        return false
    }
    conn.Close()
    return true
}

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
	exec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	k8sexec "k8s.io/utils/exec"
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
	IsDeviceStillStuck(device string) bool
	IsMultipathdAlive() (bool, error)
	MultipathdCmd(device string, command string) (string, error)
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
	socketMu       sync.RWMutex

	cachedSocket string

	sl SocketLimiter

	waitDelay time.Duration
}

func NewExecuter() *Executer {
	return &Executer{
		stuckProcesses: make(map[string]zombieInfo),
	}
}

//Command Category
//	Typical Size	Recommended Limit	Why?
//Transaction (mount, login, remove)	< 1KB	64KB	Prevents "log spam" from eating your RAM.
//Status (multipath -ll, iscsiadm -m node)	10KB - 1MB	2MB	Enough for typical node density.
//nventory (discoverydb, scan)	1MB - 5MB	10MB	Prevents truncation on high-density nodes.

var _ k8sexec.Interface = &Executer{}
var _ k8sexec.Cmd = &safeCmd{}

const DefaultMaxOutput int = 1024 * 1024 // 1MB limit for safety

// limitWriter prevents chatty commands from causing OOM
type limitWriter struct {
	io.Writer
	Limit int
	curr  int
	mu    sync.Mutex // Add this to prevent race conditions on shared buffers
}

func (w *limitWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	remaining := w.Limit - w.curr
	logger.Warningf("Remaining %d", remaining)
	if remaining <= 0 {
		return len(p), nil
	}
	
	writeLen := len(p)
	if writeLen > remaining {
		writeLen = remaining
	}
	logger.Warningf("Write len %d", writeLen)
	nActual, err := w.Writer.Write(p[:writeLen])
	logger.Warningf("Write len %d", nActual)
	w.curr += nActual
	logger.Warningf("pos %d", w.curr)
	if err != nil {
		logger.Warningf("have error")
}
    return nActual, err // Return nActual, NOT len(p)
}




// safeCmd wraps the standard Cmd to add device-aware safety logic
type safeCmd struct {
	*exec.Cmd
	name     string
	args     []string
	ctx      context.Context
	executor *Executer
}

func (e *Executer) CommandContext(ctx context.Context, name string, args ...string) k8sexec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	// (1) Inject WaitDelay: If process exits but pipes remain open,
	// or if context is cancelled, Wait() will wait this long before SIGKILL.
	cmd.WaitDelay = e.waitDelay

	return &safeCmd{
		Cmd:      cmd,
		name:     name,
		args:     args,
		ctx:      ctx,
		executor: e,
	}
}

func (e *Executer) Command(name string, args ...string) k8sexec.Cmd {
	logger.Warningf("command %s", name)
	return e.CommandContext(context.Background(), name, args...)
}

func (s *safeCmd) Start() error {
	logger.Warning("Start")

    device := s.extractDevice()
    if device != "" && s.executor.IsDeviceStillStuck(device) {
	logger.warning("stuck error")
        // SafeFormatAndMount expects a real execution error or success.
        // Returning a generic fmt.Errorf here causes the "exit status 2" log.
	return &stuckError{device: device, name: s.name}
    }

    // Wrap Stdout ONLY if it isn't already a limitWriter
    if s.Cmd.Stdout == nil {
        s.Cmd.Stdout = &limitWriter{Writer: &bytes.Buffer{}, Limit: DefaultMaxOutput}
    }

    // Wrap Stderr similarly
    if s.Cmd.Stderr == nil {
        s.Cmd.Stderr = &limitWriter{Writer: &bytes.Buffer{}, Limit: DefaultMaxOutput}
    }

    return s.Cmd.Start()
}


func (s *safeCmd) Wait() error {
	logger.Warning("wait")
	err := s.Cmd.Wait()
	device := s.extractDevice()
	logger.Warning("Wait done")

	var pid int
	if s.Cmd.Process != nil {
		pid = s.Cmd.Process.Pid
	}

	if err != nil {
		// 3. Use s.ctx instead of s.Cmd.Context()
		isTimeout := s.ctx != nil && s.ctx.Err() != nil
		isWaitDelay := errors.Is(err, exec.ErrWaitDelay)

		if isWaitDelay || isTimeout {
			if device != "" {
				s.executor.markAsStuck(device, pid, s.name)
			}
		}
		return err
	}

	// Success
	if device != "" {
		s.executor.clearTracking(device)
	}
	return nil
}

func (s *safeCmd) CombinedOutput() ([]byte, error) {
	logger.Warning("combined output")
    var b bytes.Buffer
    // Use a single limitWriter for BOTH to track the total output limit correctly
    lw := &limitWriter{Writer: &b, Limit: DefaultMaxOutput}
    
    s.Cmd.Stdout = lw
    s.Cmd.Stderr = lw

    // Bypass the wrapping logic in Start() by marking it as already wrapped
    // (This requires adding a check in Start() as shown in the previous response)
    if err := s.Start(); err != nil {
        return nil, err
    }
    err := s.Wait()
    return b.Bytes(), err
}



// Stop satisfies k8sexec.Cmd
func (s *safeCmd) Stop() {
	logger.Warning("Stop")
	if s.Cmd.Process == nil {
		return
	}
	// Attempt to kill the process
	_ = s.Cmd.Process.Kill()

	// Optional: You could trigger your stuck logic here if
	// the process doesn't exit after a SIGKILL, but usually
	// the WaitDelay in the main Wait() call handles this better.
}

// LookPath satisfies k8sexec.Interface
func (e *Executer) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (s *safeCmd) extractDevice() string {
	for _, arg := range s.args {
		logger.Warningf("Check arg %s", arg)
		// Standard path (/dev/sdb)
		if strings.HasPrefix(arg, "/dev/") {
			return arg
		}
		// Flag-wrapped path (--device=/dev/sdb)
		if strings.Contains(arg, "=/dev/") {
			parts := strings.SplitN(arg, "=", 2)
			return parts[1]
		}
		// Persistent Identifiers (UUID=... or LABEL=...)
		if strings.HasPrefix(arg, "UUID=") || strings.HasPrefix(arg, "LABEL=") {
			return arg
		}
	}
	return ""
}

// SetEnv satisfies the mount.Cmd interface
func (s *safeCmd) SetEnv(env []string) {
	s.Cmd.Env = env
}

// SetDir satisfies the mount.Cmd interface
func (s *safeCmd) SetDir(dir string) {
	s.Cmd.Dir = dir
}

func (s *safeCmd) SetStdout(out io.Writer) { s.Cmd.Stdout = out }
func (s *safeCmd) SetStderr(out io.Writer) { s.Cmd.Stderr = out }
func (s *safeCmd) SetStdin(in io.Reader)   { s.Cmd.Stdin = in }








// stuckError satisfies the k8s.io/utils/exec.ExitError interface
type stuckError struct {
	device string
	name   string
}

func (e *stuckError) Error() string {
	return fmt.Sprintf("node-safety: previous %s process is still stuck for device %s", e.name, e.device)
}

func (e *stuckError) String() string {
	return e.Error()
}

// ExitStatus is what k8sexec looks for to determine the return code.
// Status 1 is a general failure; Status 4 is a blkid/fsck "probing error".
// Using 1 is the safest way to signal "Command failed to run".
func (e *stuckError) ExitStatus() int {
	return 1 
}

func (e *stuckError) Exited() bool {
	return true
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
	if device != "" && e.IsDeviceStillStuck(device) {
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

		// TODO return captured, err // Keep the original error type (ExitError)
	}

	// Success: Device is healthy, clear any existing block
	if device != "" {
		e.clearTracking(device)
	}
	return captured, nil
}

func (e *Executer) IsDeviceStillStuck(device string) bool {
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
			return true
		}
	}

	e.clearTracking(device)
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
		"\x00multipathd", // Legacy abstract
		"/run/multipathd.sock",
		"/var/run/multipathd.sock",
	}

	for _, path := range candidates {
		logger.Warningf("Test candiate %s", path)
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			logger.Warning("Candidate found")
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
		logger.Warningf("Cached socket %s", s)
		return s
	}

	e.socketMu.Lock()
	defer e.socketMu.Unlock()
	// Double-check to prevent race
	if e.cachedSocket == "" {
		logger.Warning("no cached")
		e.cachedSocket = e.resolveSocket()
	}
	return e.cachedSocket
}

// invalidateSocket clears the cache if a connection fails.
func (e *Executer) invalidateSocket() {
	logger.Warning("invalidateSocket")
	e.socketMu.Lock()
	e.cachedSocket = ""
	e.socketMu.Unlock()
}

func (e *Executer) MultipathdCmd(device string, command string) (string, error) {
	logger.Warningf("MultpathCmd %s", command)
	logger.Warningf("My UID: %d", os.Getuid())
	// If the command targets a specific device, check the stuck map first.

	if device != "" {
		if e.IsDeviceStillStuck(device) {
			return "", fmt.Errorf("safety-gate: skipping multipathd query for stuck device %s", device)
		}
	}

	socketPath := e.GetSocket()
	// Use a very short dial timeout; if the daemon is in D-state, Dial can hang.
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err != nil {
		e.invalidateSocket()
		//                 conn, err = net.DialTimeout("unix", socketPath, 2*time.Second)
		//                 if err != nil {
		//                         return "", fmt.Errorf("multipathd unreachable: %w", err)
		//                 }

		return "", fmt.Errorf("multipathd unreachable: %w", err)
	}
	defer conn.Close()

	// Strict deadline: If multipathd doesn't answer in 5s, it's likely wedged.
	conn.SetDeadline(time.Now().Add(5 * time.Second))

payload := command + "\n"
lStr := strconv.Itoa(len(payload))
// Manual padding to ensure NO hidden characters/encodings
header := bytes.Repeat([]byte(" "), 10-len(lStr))
header = append(header, []byte(lStr)...)

// SINGLE WRITE
full := append(header, []byte(payload)...)
conn.Write(full)


    if _, err := conn.Write(full); err != nil {
	logger.Warning("failed to send")
        return "", err
    }

	// Protocol Read Header
	lenBuf := make([]byte, 10)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		logger.Warningf("fail to read %v", err)
		return "", fmt.Errorf("failed to read header: %w", err)
	}

	trimmedLen := strings.TrimSpace(string(lenBuf))
	respLen, err := strconv.Atoi(trimmedLen)
	if err != nil || respLen <= 0 {
		logger.Warning("invalid len")
		return "", fmt.Errorf("protocol error: invalid resp length %q", trimmedLen)
	}

	if respLen > DefaultMaxOutput {
		logger.Warning("exceeeds")
		return "", fmt.Errorf("response size %d exceeds limit", respLen)
	}

	// Body Read
	respBody, err := io.ReadAll(io.LimitReader(conn, int64(respLen)))
	if err != nil {
		logger.Warning("Cannot read")
		return "", fmt.Errorf("failed to read body: %w", err)
	}

	response := strings.TrimSpace(string(respBody))

	logger.Warningf("response %s", response)

	// Logical Errors
	if strings.HasPrefix(response, "fail") || response == "timeout" {
		// If multipathd specifically says 'timeout', the DAEMON is stuck on I/O
		if device != "" {
			e.markAsStuck(device, 0, "multipathd-internal")
		}
		return "", fmt.Errorf("multipathd internal error: %s", response)
	}

	return response, nil
}

// Wrapper for MultipathdCmd that checks up + keep alive
func (e *Executer) SafeMultipathdCmd(device string, command string) (string, error) {
	// 1. Level 1: Process Check (Ultra-lightweight)
	// If the daemon isn't running, don't even try the socket.
	if !e.IsMultipathdRunning() {
		return "", fmt.Errorf("circuit-breaker: multipathd process is not running")
	}

	// 2. Level 2: Keepalive Check (Responsive Event Loop)
	// Only run this if it's been more than X seconds since the last success
	// to prevent "thundering herd" overhead.
	if alive, err := e.IsMultipathdAlive(); !alive {
		return "", err
	}

	// 4. Level 4: Execution
	return e.MultipathdCmd(device, command)
}

// Wrapper for MultipathdCmd that adds throttling
// Ensure your e.sl.sem has a small capacity (usually 1 to 3). Since multipathd processes commands serially, sending 50 concurrent requests will just fill the kernel's socket backlog and trigger the very timeouts you are trying to avoid.
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

func (e *Executer) IsMultipathdRunning() bool {
	// Try standard RHEL path first, then common alternatives
	paths := []string{"/var/run/multipathd.pid", "/run/multipathd.pid", "/var/run/multipathd/multipathd.pid"}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}

		process, _ := os.FindProcess(pid)
		// Signal(0) verifies the PID is still alive and belongs to multipathd
		if err := process.Signal(syscall.Signal(0)); err == nil {
			return true
		}
	}
	return false
}

// IsMultipathdAlive performs a liveness check by sending a no-op command.
// It distinguishes between "stopped" (connection refused) and "stuck" (timeout).
func (e *Executer) IsMultipathdAlive() (bool, error) {
	// We use 'show status' because it's a fast, read-only internal no-op.
	// If the event loop is deadlocked (D-state), this will trigger the
	// 5s deadline set in MultipathdCmd.
	resp, err := e.MultipathdCmd("", "show status")

	if err != nil {
		// If the error is a timeout, the daemon is likely stuck in D-state
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return false, fmt.Errorf("multipathd is unresponsive (deadlock suspected): %w", err)
		}

		// TODO should we expect "timeout" string on error if not running
		// If the error is "connection refused", the daemon is simply not running
		if strings.Contains(err.Error(), "unreachable") ||
			strings.Contains(err.Error(), "refused") ||
			strings.Contains(err.Error(), "no such file") {
			return false, fmt.Errorf("multipathd service is not running")
		}

		return false, err
	}

	// Verify we got a sane response (usually "up" or "multipathd vX.X.X")
	if resp == "" {
		return false, fmt.Errorf("multipathd returned empty response")
	}

	// TODO is this too strict
	//if !strings.Contains(string(output), "status:") && !strings.Contains(string(output), "up")

	return true, nil
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

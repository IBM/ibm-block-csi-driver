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

package executer_limited

import (
	"context"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
)

//go:generate mockgen -destination=../../../mocks/mock_executer_limited.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer_limited ExecuterInterfaceLimited
type ExecuterInterfaceLimited interface { // basic host dependent functions
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

type ExecuterLimited struct {
	tokens chan struct{}
}

func NewExecuter(max_invocations int) ExecuterInterfaceLimited {
	return &ExecuterLimited{
		tokens: make(chan struct{}, max_invocations),
	}
}

func ReleaseSemaphore(e *ExecuterLimited) {
	<-e.tokens
}

func (e *ExecuterLimited) ExecuteWithTimeoutSilently(mSeconds int, command string, args []string) ([]byte, error) {
	defer ReleaseSemaphore(e)
	e.tokens <- struct{}{}
	// Create a new context and add a timeout to it
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(mSeconds)*time.Millisecond)
	defer cancel() // The cancel should be deferred so resources are cleaned up

	// Create the command with our context
	cmd := exec.CommandContext(ctx, command, args...)

	// This time we can simply use CombinedOutput() to get the result.
	out, err := cmd.CombinedOutput()

	// We want to check the context error to see if the timeout was executed.
	// The error returned by cmd.Output() will be OS specific based on what
	// happens when a process is killed.
	if ctx.Err() == context.DeadlineExceeded {
		logger.Debugf("Command %s timeout reached", command)
		return nil, ctx.Err()
	}

	// If there's no context error, we know the command completed (or errored).
	if err != nil {
		logger.Debugf("Non-zero exit code: %s", err)
	}
	return out, err
}

func (e *ExecuterLimited) ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error) {
	logger.Debugf("Executing command : {%v} with args : {%v}. and timeout : {%v} mseconds", command, args, mSeconds)

	out, err := e.ExecuteWithTimeoutSilently(mSeconds, command, args)

	outAsStr := string(out)
	noOutputMessage := ""
	if strings.TrimSpace(outAsStr) != "" {
		logger.Debugf("Output from command: %s", outAsStr)
	} else {
		noOutputMessage = " (no output)"
	}
	logger.Debugf("Finished executing command" + noOutputMessage)
	return out, err
}

func (e *ExecuterLimited) OsOpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}

func (e *ExecuterLimited) OsReadlink(name string) (string, error) {
	return os.Readlink(name)
}

func (e *ExecuterLimited) FilepathGlob(pattern string) (matches []string, err error) {
	return filepath.Glob(pattern)
}

func (e *ExecuterLimited) IoutilReadDir(dirname string) ([]os.FileInfo, error) {
	return ioutil.ReadDir(dirname)
}

func (e *ExecuterLimited) IoutilReadFile(filename string) ([]byte, error) {
	return ioutil.ReadFile(filename)
}

func (e *ExecuterLimited) FileWriteString(f *os.File, s string) (n int, err error) {
	return f.WriteString(s)
}

func (e *ExecuterLimited) IsExecutable(path string) error {
	_, err := exec.LookPath(path)
	return err
}

func (e *ExecuterLimited) GetExitCode(err error) (int, bool) {
	if exitError, isExitError := err.(*exec.ExitError); isExitError {
		logger.Debug("No active iSCSI sessions")
		return exitError.ExitCode(), true
	}
	return 0, false
}

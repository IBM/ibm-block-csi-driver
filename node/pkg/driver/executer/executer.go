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
	"context"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
)

//go:generate mockgen -destination=../../../mocks/mock_executer.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer ExecuterInterface
type ExecuterInterface interface { // basic host dependent functions
	ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error)
	OsOpenFile(name string, flag int, perm os.FileMode) (*os.File, error)
	OsReadlink(name string) (string, error)
	FilepathGlob(pattern string) (matches []string, err error)
	IoutilReadDir(dirname string) ([]os.FileInfo, error)
	IoutilReadFile(filename string) ([]byte, error)
	FileWriteString(f *os.File, s string) (n int, err error)
	IsExecutable(path string) error
}

type Executer struct {
}

func (e *Executer) ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error) {
	logger.Debugf("Executing command : {%v} with args : {%v}. and timeout : {%v} mseconds", command, args, mSeconds)

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
	logger.Debugf("Output from command: %s", string(out))
	if err != nil {
		logger.Debugf("Non-zero exit code: %s", err)
	}

	logger.Debugf("Finished executing command")
	return out, err
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

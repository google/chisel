// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package exec provides a client to issue commands and retrieve output.
package exec

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Client is an implementation of CmdExecutor interface.  It issues
// commands and returns stdout upon success.
type Client struct {
}

// RunCommandReadStdOutAllowNonZeroExitCode executes a command and returns stdout as a string
// slice. Stderr is not captured. If the return code of the command is present
// in legalExitCodes, the returned error is not propagated back to caller.
func (c *Client) RunCommandReadStdOutAllowNonZeroExitCode(cmd []string, directory string, legalExitCodes map[int]bool) ([]string, error) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	command := exec.Command(cmd[0], cmd[1:]...)
	command.Dir = directory
	command.Stdout = stdout
	command.Stderr = stderr
	runErr := command.Run()
	if runErr != nil {
		cmdStr := strings.Join(cmd, " ")
		runErr = fmt.Errorf("%q in directory %s. stderr: %s\n err: %w", cmdStr, directory, stderr.String(), runErr)
	}
	if stdout.Len() == 0 {
		return nil, runErr
	}
	split := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))

	out := make([]string, 0, len(split))
	for _, l := range split {
		out = append(out, string(bytes.TrimSpace(l)))
	}

	if runErr == nil {
		return out, nil
	}

	var exitError *exec.ExitError
	if !errors.As(runErr, &exitError) {
		return out, runErr
	}

	if _, found := legalExitCodes[exitError.ExitCode()]; found {
		return out, nil
	}

	return out, runErr
}

// RunCommandReadStdOut behaves like RunCommandReadStdOutAllowNonZeroExitCode,
// but zero is the only legal exit code.
func (c *Client) RunCommandReadStdOut(cmd []string, directory string) ([]string, error) {
	return c.RunCommandReadStdOutAllowNonZeroExitCode(cmd, directory, nil)
}

// RunCommandReadStdOutWithRetries behaves like RunCommandReadStdOut, but will
// retry commands on error up to `numRetries` times.
func (c *Client) RunCommandReadStdOutWithRetries(cmd []string, directory string, numRetries int) ([]string, error) {
	var out []string
	var err error
	for i := 0; i <= numRetries; i++ {
		out, err = c.RunCommandReadStdOut(cmd, directory)
		if err == nil {
			return out, err
		}
	}

	return out, err
}

// RunCommandReadStdOutAllowNonZeroExitCodeWithRetries behaves like
// RunCommandReadStdOutAllowNonZeroExitCode, but will retry commands on error
// up to `numRetries` times.
func (c *Client) RunCommandReadStdOutAllowNonZeroExitCodeWithRetries(cmd []string, directory string, legalExitCodes map[int]bool, numRetries int) ([]string, error) {
	var out []string
	var err error
	for i := 0; i <= numRetries; i++ {
		out, err = c.RunCommandReadStdOutAllowNonZeroExitCode(cmd, directory, legalExitCodes)
		if err == nil {
			return out, err
		}
	}

	return out, err
}

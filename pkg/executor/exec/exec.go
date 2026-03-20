/*
   Copyright @ 2021 bocloud <fushaosong@beyondcent.com>.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.

   Original file: https://gitee.com/bocloud-open-source/carina/blob/v0.9.1/utils/exec/exec.go
*/

package exec

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

// Executor is the main interface for all the exec commands
type Executor interface {
	ExecuteCommand(command string, arg ...string) error
	ExecuteCommandWithEnv(env []string, command string, arg ...string) error
	ExecuteCommandWithOutput(command string, arg ...string) (string, error)
	ExecuteCommandWithCombinedOutput(command string, arg ...string) (string, error)
	ExecuteCommandWithOutputFile(command, outfileArg string, arg ...string) (string, error)
	ExecuteCommandWithOutputFileTimeout(timeout time.Duration, command, outfileArg string, arg ...string) (string, error)
	ExecuteCommandWithTimeout(timeout time.Duration, command string, arg ...string) (string, error)
	ExecuteCommandResidentBinary(timeout time.Duration, command string, arg ...string) error
}

// CommandExecutor is the type of the Executor
type CommandExecutor struct {
}

// ExecuteCommand starts a process and wait for its completion
func (c *CommandExecutor) ExecuteCommand(command string, arg ...string) error {
	return c.ExecuteCommandWithEnv([]string{}, command, arg...)
}

// ExecuteCommandWithEnv starts a process with env variables and wait for its completion
func (*CommandExecutor) ExecuteCommandWithEnv(env []string, command string, arg ...string) error {
	result, err := startCommand(env, command, arg...)
	if err != nil {
		return err
	}

	logOutput(result.Stdout, result.Stderr)

	if err := result.Cmd.Wait(); err != nil {
		return err
	}

	return nil
}

// ExecuteCommandWithTimeout starts a process and wait for its completion with timeout.
func (*CommandExecutor) ExecuteCommandWithTimeout(timeout time.Duration, command string, arg ...string) (string, error) {
	logCommand(command, arg...)
	// #nosec G204 Rook controls the input to the exec arguments
	cmd := exec.Command(command, arg...)

	var b bytes.Buffer
	cmd.Stdout = &b
	cmd.Stderr = &b

	if err := cmd.Start(); err != nil {
		return "", err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	interruptSent := false
	for {
		// 使用 NewTimer 替代 time.After，避免在循环中创建多个定时器
		timer := time.NewTimer(timeout)
		select {
		case <-timer.C:
			timeoutParams := HandleTimeoutParams{
				Cmd:           cmd,
				Buffer:        &b,
				Command:       command,
				InterruptSent: &interruptSent,
			}
			result, err, shouldReturn := handleTimeout(timeoutParams)
			if shouldReturn {
				// 如果函数返回，需要确保定时器已停止
				if !timer.Stop() {
					// 如果定时器已经触发，需要从通道中读取值以避免goroutine泄漏
					<-timer.C
				}
				return result, err
			}
			// 如果不返回，定时器需要被停止以便下次循环重新使用
			if !timer.Stop() {
				// 如果定时器已经触发，需要从通道中读取值以避免goroutine泄漏
				<-timer.C
			}
		case err := <-done:
			// 在退出前停止定时器
			if !timer.Stop() {
				// 如果定时器已经触发，需要从通道中读取值以避免goroutine泄漏
				<-timer.C
			}
			completionParams := HandleCommandCompletionParams{
				Err:           err,
				Buffer:        &b,
				InterruptSent: interruptSent,
				Command:       command,
			}
			return handleCommandCompletion(completionParams)
		}
	}
}

// HandleTimeoutParams 包含处理超时所需的参数
type HandleTimeoutParams struct {
	Cmd           *exec.Cmd
	Buffer        *bytes.Buffer
	Command       string
	InterruptSent *bool
}

// handleTimeout 处理命令执行超时的情况
func handleTimeout(params HandleTimeoutParams) (string, error, bool) {
	if *params.InterruptSent {
		log.Infof("timeout waiting for process %s to return after interrupt signal was sent. Sending kill signal to the process", params.Command)
		var e error
		if err := params.Cmd.Process.Kill(); err != nil {
			log.Errorf("Failed to kill process %s: %v", params.Command, err)
			e = fmt.Errorf("timeout waiting for the command %s to return after interrupt signal was sent. Tried to kill the process but that failed: %v", params.Command, err)
		} else {
			e = fmt.Errorf("timeout waiting for the command %s to return", params.Command)
		}
		return strings.TrimSpace(params.Buffer.String()), e, true
	}

	log.Infof("timeout waiting for process %s to return. Sending interrupt signal to the process", params.Command)
	if err := params.Cmd.Process.Signal(os.Interrupt); err != nil {
		log.Errorf("Failed to send interrupt signal to process %s: %v", params.Command, err)
		// kill signal will be sent next loop
	}
	*params.InterruptSent = true
	return "", nil, false
}

// HandleCommandCompletionParams 包含处理命令完成所需的参数
type HandleCommandCompletionParams struct {
	Err           error
	Buffer        *bytes.Buffer
	InterruptSent bool
	Command       string
}

// handleCommandCompletion 处理命令完成的情况
func handleCommandCompletion(params HandleCommandCompletionParams) (string, error) {
	if params.Err != nil {
		return strings.TrimSpace(params.Buffer.String()), params.Err
	}
	if params.InterruptSent {
		return strings.TrimSpace(params.Buffer.String()), fmt.Errorf("timeout waiting for the command %s to return", params.Command)
	}
	return strings.TrimSpace(params.Buffer.String()), nil
}

// ExecuteCommandWithOutput executes a command with output
func (*CommandExecutor) ExecuteCommandWithOutput(command string, arg ...string) (string, error) {
	logCommand(command, arg...)
	// #nosec G204 Rook controls the input to the exec arguments
	cmd := exec.Command(command, arg...)
	return runCommandWithOutput(cmd, false)
}

// ExecuteCommandWithCombinedOutput executes a command with combined output
func (*CommandExecutor) ExecuteCommandWithCombinedOutput(command string, arg ...string) (string, error) {
	logCommand(command, arg...)
	// #nosec G204 Rook controls the input to the exec arguments
	cmd := exec.Command(command, arg...)
	return runCommandWithOutput(cmd, true)
}

// ExecuteCommandWithOutputFileTimeout Same as ExecuteCommandWithOutputFile but with a timeout limit.
// #nosec G307 Calling defer to close the file without checking the error return is not a risk for a simple file open and close
func (*CommandExecutor) ExecuteCommandWithOutputFileTimeout(timeout time.Duration,
	command, outfileArg string, arg ...string) (string, error) {

	outFile, err := ioutil.TempFile("", "")
	if err != nil {
		return "", fmt.Errorf("failed to open output file: %+v", err)
	}
	defer outFile.Close()
	defer os.Remove(outFile.Name())

	arg = append(arg, outfileArg, outFile.Name())
	logCommand(command, arg...)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// #nosec G204 Rook controls the input to the exec arguments
	cmd := exec.CommandContext(ctx, command, arg...)
	cmdOut, err := cmd.CombinedOutput()

	// if there was anything that went to stdout/stderr then log it, even before
	// we return an error
	if string(cmdOut) != "" {
		log.Debug(string(cmdOut))
	}

	if ctx.Err() == context.DeadlineExceeded {
		return string(cmdOut), ctx.Err()
	}

	if err != nil {
		return string(cmdOut), err
	}

	return readOutputFile(outFile)
}

// ExecuteCommandWithOutputFile executes a command with output on a file
// #nosec G307 Calling defer to close the file without checking the error return is not a risk for a simple file open and close
func (*CommandExecutor) ExecuteCommandWithOutputFile(command, outfileArg string, arg ...string) (string, error) {

	// create a temporary file to serve as the output file for the command to be run and ensure
	// it is cleaned up after this function is done
	outFile, err := ioutil.TempFile("", "")
	if err != nil {
		return "", fmt.Errorf("failed to open output file: %+v", err)
	}
	defer outFile.Close()
	defer os.Remove(outFile.Name())

	// append the output file argument to the list or args
	arg = append(arg, outfileArg, outFile.Name())

	logCommand(command, arg...)
	// #nosec G204 Rook controls the input to the exec arguments
	cmd := exec.Command(command, arg...)
	cmdOut, err := cmd.CombinedOutput()
	if err != nil {
		cmdOut = []byte(fmt.Sprintf("%s. %s", string(cmdOut), assertErrorType(err)))
	}
	// if there was anything that went to stdout/stderr then log it, even before we return an error
	if string(cmdOut) != "" {
		log.Debug(string(cmdOut))
	}
	if err != nil {
		return string(cmdOut), err
	}

	// read the entire output file and return that to the caller
	return readOutputFile(outFile)
}

func (*CommandExecutor) ExecuteCommandResidentBinary(timeout time.Duration, command string, arg ...string) error {
	cmd := exec.Command(command, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	go func() {
		if err := cmd.Run(); err != nil {
			log.Errorf("run Resident server failed: %s+v", err)
		}
	}()
	time.Sleep(timeout)
	return nil
}

// StartCommandResult 包含 startCommand 函数的执行结果
type StartCommandResult struct {
	Cmd    *exec.Cmd
	Stdout io.ReadCloser
	Stderr io.ReadCloser
}

func startCommand(env []string, command string, arg ...string) (StartCommandResult, error) {
	logCommand(command, arg...)

	// #nosec G204 Rook controls the input to the exec arguments
	cmd := exec.Command(command, arg...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Warnf("failed to open stdout pipe: %+v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Warnf("failed to open stderr pipe: %+v", err)
	}

	if len(env) > 0 {
		cmd.Env = env
	}

	startErr := cmd.Start()

	return StartCommandResult{
		Cmd:    cmd,
		Stdout: stdout,
		Stderr: stderr,
	}, startErr
}

// read from reader line by line and write it to the log
func logFromReader(reader io.ReadCloser) {
	in := bufio.NewScanner(reader)
	lastLine := ""
	for in.Scan() {
		lastLine = in.Text()
		log.Debug(lastLine)
	}
}

func logOutput(stdout, stderr io.ReadCloser) {
	if stdout == nil || stderr == nil {
		log.Warnf("failed to collect stdout and stderr")
		return
	}
	go logFromReader(stderr)
	logFromReader(stdout)
}

func runCommandWithOutput(cmd *exec.Cmd, combinedOutput bool) (string, error) {
	var output []byte
	var err error
	var out string

	if combinedOutput {
		output, err = cmd.CombinedOutput()
	} else {
		output, err = cmd.Output()
		if err != nil {
			output = []byte(fmt.Sprintf("%s. %s", string(output), assertErrorType(err)))
		}
	}

	out = strings.TrimSpace(string(output))

	if err != nil {
		return out, err
	}

	return out, nil
}

func logCommand(command string, arg ...string) {
	log.Infof("Running command: %s %s", command, strings.Join(arg, " "))
}

func assertErrorType(err error) string {
	switch errType := err.(type) {
	case *exec.ExitError:
		return string(errType.Stderr)
	case *exec.Error:
		return errType.Error()
	default:
		// For other error types, return the standard error string
		return err.Error()
	}
}

// readOutputFile reads the content from the output file and returns it as a string
func readOutputFile(outFile *os.File) (string, error) {
	fileOut, err := ioutil.ReadAll(outFile)
	if err := outFile.Close(); err != nil {
		return "", err
	}
	return string(fileOut), err
}

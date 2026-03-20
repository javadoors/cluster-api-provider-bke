/******************************************************************
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package selfupdate

import (
	"os"
	"path"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
)

type mockSelfUpdateExecutor struct {
	exec.Executor
	executeCommandCalled     bool
	executeCommandWithOutput string
	executeCommandError      error
}

func (m *mockSelfUpdateExecutor) ExecuteCommand(_ string, _ ...string) error {
	m.executeCommandCalled = true
	return m.executeCommandError
}

func (m *mockSelfUpdateExecutor) ExecuteCommandWithCombinedOutput(_ string, _ ...string) (string, error) {
	m.executeCommandCalled = true
	return m.executeCommandWithOutput, m.executeCommandError
}

func TestUpdatePluginName(t *testing.T) {
	pluginObj := &UpdatePlugin{}
	assert.Equal(t, Name, pluginObj.Name())
}

func TestUpdatePluginParam(t *testing.T) {
	pluginObj := &UpdatePlugin{}
	params := pluginObj.Param()
	assert.NotNil(t, params)
	assert.Contains(t, params, "agentUrl")
	assert.Equal(t, DefaultAgentUrl, params["agentUrl"].Default)
	assert.Equal(t, "Agent download url used to replace existing BKEAgent binary", params["agentUrl"].Description)
}

func TestNewUpdatePlugin(t *testing.T) {
	mockExec := &mockSelfUpdateExecutor{}
	pluginObj := New(mockExec)
	assert.NotNil(t, pluginObj)
	assert.Equal(t, Name, pluginObj.Name())
}

func TestNeedUpdateWhenBinNotExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})

	mockExec := &mockSelfUpdateExecutor{}
	pluginObj := &UpdatePlugin{exec: mockExec}

	result := pluginObj.NeedUpdate("/etc/bkeagent/bin/bkeagent")

	assert.True(t, result)
	assert.False(t, mockExec.executeCommandCalled)
}

func TestNeedUpdateWhenBinExistsCommandFails(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return true
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = ""
	mockExec.executeCommandError = errors.New("command failed")

	pluginObj := &UpdatePlugin{exec: mockExec}

	result := pluginObj.NeedUpdate("/etc/bkeagent/bin/bkeagent")

	assert.True(t, result)
	assert.True(t, mockExec.executeCommandCalled)
}

func TestNeedUpdateWhenBinExistsSuccessWithDifferentVersion(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return true
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "abc123"
	mockExec.executeCommandError = nil

	pluginObj := &UpdatePlugin{exec: mockExec}

	result := pluginObj.NeedUpdate("/etc/bkeagent/bin/bkeagent")

	assert.True(t, result)
	assert.True(t, mockExec.executeCommandCalled)
}

func TestNeedUpdateWhenBinExistsSuccessWithSameVersion(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return true
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "dev"
	mockExec.executeCommandError = nil

	pluginObj := &UpdatePlugin{exec: mockExec}

	result := pluginObj.NeedUpdate("/etc/bkeagent/bin/bkeagent")

	assert.False(t, result)
	assert.True(t, mockExec.executeCommandCalled)
}

func TestNeedUpdateWhenBinExistsOutputEmpty(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return true
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = ""
	mockExec.executeCommandError = nil

	pluginObj := &UpdatePlugin{exec: mockExec}

	result := pluginObj.NeedUpdate("/etc/bkeagent/bin/bkeagent")

	assert.True(t, result)
	assert.True(t, mockExec.executeCommandCalled)
}

func TestNeedUpdateWhenBinExistsOutputHasMultipleLines(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return true
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "abc123\nSome other output\nMore lines"
	mockExec.executeCommandError = nil

	pluginObj := &UpdatePlugin{exec: mockExec}

	result := pluginObj.NeedUpdate("/etc/bkeagent/bin/bkeagent")

	assert.True(t, result)
	assert.True(t, mockExec.executeCommandCalled)
}

func TestExecuteWhenNoUpdateNeeded(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return true
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "dev"

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteWhenScriptsDirNotExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	var mkdirCalled bool
	var writeFileCalled bool

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		if strings.Contains(s, "scripts") && !strings.Contains(s, "update.sh") {
			return false
		}
		if strings.Contains(s, "update.sh") {
			return false
		}
		return false
	})

	patches.ApplyFunc(os.MkdirAll, func(s string, _ os.FileMode) error {
		mkdirCalled = true
		return nil
	})

	patches.ApplyFunc(os.WriteFile, func(_ string, _ []byte, _ os.FileMode) error {
		writeFileCalled = true
		return nil
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "different-version"
	mockExec.executeCommandError = nil

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{})

	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.True(t, mkdirCalled)
	assert.True(t, writeFileCalled)
	assert.True(t, mockExec.executeCommandCalled)
}

func TestExecuteWhenScriptsDirExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	var mkdirCalled bool
	var writeFileCalled bool

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		if strings.Contains(s, "scripts") && !strings.Contains(s, "update.sh") {
			return true
		}
		if strings.Contains(s, "update.sh") {
			return false
		}
		return false
	})

	patches.ApplyFunc(os.MkdirAll, func(_ string, _ os.FileMode) error {
		mkdirCalled = true
		return nil
	})

	patches.ApplyFunc(os.WriteFile, func(_ string, _ []byte, _ os.FileMode) error {
		writeFileCalled = true
		return nil
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "different-version"
	mockExec.executeCommandError = nil

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{})

	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.False(t, mkdirCalled)
	assert.True(t, writeFileCalled)
}

func TestExecuteWhenScriptExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	var mkdirCalled bool
	var writeFileCalled bool

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		if s == "/etc/bkeagent/scripts" {
			return true
		}
		if s == "/etc/bkeagent/scripts/update.sh" {
			return true
		}
		return true
	})

	patches.ApplyFunc(os.MkdirAll, func(_ string, _ os.FileMode) error {
		mkdirCalled = true
		return nil
	})

	patches.ApplyFunc(os.WriteFile, func(_ string, _ []byte, _ os.FileMode) error {
		writeFileCalled = true
		return nil
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "abc123"
	mockExec.executeCommandError = nil

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{})

	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.False(t, mkdirCalled)
	assert.False(t, writeFileCalled)
}

func TestExecuteWithMkdirError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mkdirErr := errors.New("mkdir failed")

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		return false
	})

	patches.ApplyFunc(os.MkdirAll, func(_ string, _ os.FileMode) error {
		return mkdirErr
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "different-version"

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mkdir failed")
	assert.Nil(t, result)
}

func TestExecuteWithWriteFileError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	writeErr := errors.New("write file failed")

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		if strings.Contains(s, "scripts") && !strings.Contains(s, "update.sh") {
			return true
		}
		return false
	})

	patches.ApplyFunc(os.MkdirAll, func(_ string, _ os.FileMode) error {
		return nil
	})

	patches.ApplyFunc(os.WriteFile, func(_ string, _ []byte, _ os.FileMode) error {
		return writeErr
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "different-version"

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write file failed")
	assert.Nil(t, result)
}

func TestExecuteWithExecuteCommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	execErr := errors.New("execute command failed")

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		if s == "/etc/bkeagent/scripts" {
			return true
		}
		if s == "/etc/bkeagent/scripts/update.sh" {
			return true
		}
		return true
	})

	patches.ApplyFunc(os.MkdirAll, func(_ string, _ os.FileMode) error {
		return nil
	})

	patches.ApplyFunc(os.WriteFile, func(_ string, _ []byte, _ os.FileMode) error {
		return nil
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "abc123"
	mockExec.executeCommandError = execErr

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "execute command failed")
	assert.Nil(t, result)
}

func TestExecuteWithCustomAgentUrl(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	customUrl := "http://custom.example.com/bkeagent-latest-linux-{.arch}"

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		if s == "/etc/bkeagent/scripts" {
			return true
		}
		if s == "/etc/bkeagent/scripts/update.sh" {
			return true
		}
		return true
	})

	patches.ApplyFunc(os.MkdirAll, func(_ string, _ os.FileMode) error {
		return nil
	})

	patches.ApplyFunc(os.WriteFile, func(_ string, _ []byte, _ os.FileMode) error {
		return nil
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "abc123"
	mockExec.executeCommandError = nil

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{Name, "agentUrl=" + customUrl})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestUpdatePluginDefaultAgentUrl(t *testing.T) {
	pluginObj := &UpdatePlugin{}
	params := pluginObj.Param()

	assert.NotNil(t, params["agentUrl"])
	assert.Equal(t, DefaultAgentUrl, params["agentUrl"].Value)
	assert.Equal(t, DefaultAgentUrl, params["agentUrl"].Default)
	assert.False(t, params["agentUrl"].Required)
}

func TestUpdatePluginConstantName(t *testing.T) {
	assert.Equal(t, "SelfUpdate", Name)
	assert.Equal(t, "http://http.bocloud.k8s:40080/files/bkeagent-latest-linux-{.arch}", DefaultAgentUrl)
}

func TestUpdatePluginRestartScriptPath(t *testing.T) {
	scriptPath := path.Join("/etc/bkeagent/scripts", "update.sh")
	expectedPath := "/etc/bkeagent/scripts/update.sh"
	assert.Equal(t, expectedPath, scriptPath)
}

func TestUpdatePluginNeedUpdateExtractsFirstLine(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	testCases := []struct {
		name           string
		output         string
		expectedResult bool
	}{
		{"single line", "abc123", true},
		{"multiple lines", "abc123\nline2\nline3", true},
		{"empty output", "", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches.ApplyFunc(utils.Exists, func(_ string) bool {
				return true
			})

			mockExec := &mockSelfUpdateExecutor{}
			mockExec.executeCommandWithOutput = tc.output

			pluginObj := &UpdatePlugin{exec: mockExec}

			result := pluginObj.NeedUpdate("/etc/bkeagent/bin/bkeagent")

			assert.Equal(t, tc.expectedResult, result)
		})
		patches.Reset()
	}
}

func TestUpdatePluginExecuteParsesCommands(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		if s == "/etc/bkeagent/scripts" {
			return true
		}
		if s == "/etc/bkeagent/scripts/update.sh" {
			return true
		}
		return true
	})

	patches.ApplyFunc(os.MkdirAll, func(_ string, _ os.FileMode) error {
		return nil
	})

	patches.ApplyFunc(os.WriteFile, func(_ string, _ []byte, _ os.FileMode) error {
		return nil
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "abc123"

	pluginObj := &UpdatePlugin{exec: mockExec}

	result, err := pluginObj.Execute([]string{Name, "agentUrl=http://test.com"})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestUpdatePluginNeedUpdateBinaryPathConstruction(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	var binaryPath string

	patches.ApplyFunc(utils.Exists, func(s string) bool {
		binaryPath = s
		return true
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "dev"

	pluginObj := &UpdatePlugin{exec: mockExec}
	pluginObj.NeedUpdate("/etc/bkeagent/bin/bkeagent")

	assert.Equal(t, "/etc/bkeagent/bin/bkeagent", binaryPath)
}

func TestUpdatePluginUpdateScriptContent(t *testing.T) {
	assert.NotEmpty(t, updateScript)
	assert.True(t, strings.HasPrefix(updateScript, "#!/bin/bash"))
	assert.Contains(t, updateScript, "set -euo pipefail")
}

func TestUpdatePluginCommandsArrayParsing(t *testing.T) {
	testCases := []struct {
		name        string
		commands    []string
		expectError bool
	}{
		{"empty commands", []string{}, false},
		{"plugin name only", []string{Name}, false},
		{"with agent url", []string{Name, "agentUrl=http://test.com"}, false},
		{"multiple commands", []string{Name, "agentUrl=http://test.com", "extra=value"}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			patches.ApplyFunc(utils.Exists, func(_ string) bool {
				return true
			})

			mockExec := &mockSelfUpdateExecutor{}
			mockExec.executeCommandWithOutput = "dev"

			pluginObj := &UpdatePlugin{exec: mockExec}

			result, err := pluginObj.Execute(tc.commands)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Nil(t, result)
			}
		})
	}
}

func TestUpdatePluginScriptsDirectoryConstant(t *testing.T) {
	expected := "/etc/openFuyao/bkeagent/scripts"
	actual := utils.AgentScripts
	assert.Equal(t, expected, actual)
}

func TestUpdatePluginBinDirectoryConstant(t *testing.T) {
	expected := "/etc/openFuyao/bkeagent/bin"
	actual := utils.AgentBin
	assert.Equal(t, expected, actual)
}

func TestUpdatePluginUpdateScriptPermission(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()


	patches.ApplyFunc(utils.Exists, func(s string) bool {
		if s == "/etc/bkeagent/scripts" {
			return true
		}
		return false
	})

	patches.ApplyFunc(os.WriteFile, func(_ string, content []byte, perm os.FileMode) error {
		return nil
	})

	mockExec := &mockSelfUpdateExecutor{}
	mockExec.executeCommandWithOutput = "abc123"

	pluginObj := &UpdatePlugin{exec: mockExec}
	pluginObj.Execute([]string{})

}

/*
 *
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 *
 */

package env

import (
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
)

const (
	testNetworkManagerConfigWithDNS = `[main]
dns=none
plugins=ifupdown,keyfile,ofono
dns=dnsmasq

[ifupdown]
managed=false

[device]
wifi.scan-rand-mac-address=no
`
)

type mockExecutorForCentos struct {
	exec.Executor
	output    string
	outputErr error
}

func (m *mockExecutorForCentos) ExecuteCommandWithOutput(command string, arg ...string) (string, error) {
	return m.output, m.outputErr
}

func (m *mockExecutorForCentos) ExecuteCommandWithCombinedOutput(command string, arg ...string) (string, error) {
	return m.output, m.outputErr
}

func TestCheckNetworkManagerKeyExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	ep := &EnvPlugin{}
	ep.machine = NewMachine()
	err := ep.checkNetworkManager()
	assert.NoError(t, err)
}

func TestCheckNetworkManagerKeyNotExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, os.ErrNotExist
	})

	ep := &EnvPlugin{}
	ep.machine = NewMachine()
	err := ep.checkNetworkManager()
	assert.Error(t, err)
}

func TestCheckNetworkManagerFileNotExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, os.ErrNotExist
	})

	ep := &EnvPlugin{}
	ep.machine = NewMachine()
	err := ep.checkNetworkManager()
	assert.Error(t, err)
}

func TestCheckNetworkManagerMultipleDNSSettings(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, os.ErrNotExist
	})

	ep := &EnvPlugin{}
	ep.machine = NewMachine()
	err := ep.checkNetworkManager()
	assert.Error(t, err)
}

func TestCheckNetworkManagerCaseSensitive(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, os.ErrNotExist
	})

	ep := &EnvPlugin{}
	ep.machine = NewMachine()
	err := ep.checkNetworkManager()
	assert.Error(t, err)
}

func TestInitNetworkManagerKeyAlreadyExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCentos{
		output:    "",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err := ep.initNetworkManager()
	assert.NoError(t, err)
}

func TestInitNetworkManagerReplaceSuccess(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	tmpFile, err := os.CreateTemp("", "NetworkManager.conf.*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	content := `[main]
plugins=ifupdown,keyfile,ofono
dns=dnsmasq

[ifupdown]
managed=false

[device]
wifi.scan-rand-mac-address=no`
	_, err = tmpFile.WriteString(content)
	assert.NoError(t, err)
	tmpFile.Close()

	mockExec := &mockExecutorForCentos{
		output:    "",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	var replacedPath string
	var replacedContent string
	patches.ApplyFunc(catAndReplace, func(path string, src string, sub string, reg string) error {
		replacedPath = path
		replacedContent = sub
		return nil
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err = ep.initNetworkManager()
	assert.NoError(t, err)
	assert.Equal(t, InitNetWorkManagerPath, replacedPath)
	assert.Contains(t, replacedContent, "dns=none")
}

func TestInitNetworkManagerReplaceError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCentos{
		output:    "",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	patches.ApplyFunc(catAndReplace, func(path string, src string, sub string, reg string) error {
		return os.ErrPermission
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err := ep.initNetworkManager()
	assert.Error(t, err)
}

func TestInitNetworkManagerRestartError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCentos{
		output:    "Failed to restart NetworkManager",
		outputErr: os.ErrPermission,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	patches.ApplyFunc(catAndReplace, func(path string, src string, sub string, reg string) error {
		return nil
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err := ep.initNetworkManager()
	assert.Error(t, err)
}

func TestInitNetworkManagerWithDifferentConfig(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCentos{
		output:    "",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	var replacedPath string
	var replacedContent string
	patches.ApplyFunc(catAndReplace, func(path string, src string, sub string, reg string) error {
		replacedPath = path
		replacedContent = sub
		return nil
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err := ep.initNetworkManager()
	assert.NoError(t, err)
	assert.Equal(t, InitNetWorkManagerPath, replacedPath)
	assert.Contains(t, replacedContent, "dns=none")
}

func TestInitNetworkManagerEmptyConfig(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCentos{
		output:    "",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	var replacedPath string
	var replacedContent string
	patches.ApplyFunc(catAndReplace, func(path string, src string, sub string, reg string) error {
		replacedPath = path
		replacedContent = sub
		return nil
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err := ep.initNetworkManager()
	assert.NoError(t, err)
	assert.Equal(t, InitNetWorkManagerPath, replacedPath)
	assert.Contains(t, replacedContent, "dns=none")
}

func TestInitNetworkManagerWithSpecialCharacters(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCentos{
		output:    "",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	var replacedPath string
	var replacedContent string
	patches.ApplyFunc(catAndReplace, func(path string, src string, sub string, reg string) error {
		replacedPath = path
		replacedContent = sub
		return nil
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err := ep.initNetworkManager()
	assert.NoError(t, err)
	assert.Equal(t, InitNetWorkManagerPath, replacedPath)
	assert.Contains(t, replacedContent, "dns=none")
}

func TestInitNetworkManagerMultipleSections(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCentos{
		output:    "",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	var replacedPath string
	var replacedContent string
	patches.ApplyFunc(catAndReplace, func(path string, src string, sub string, reg string) error {
		replacedPath = path
		replacedContent = sub
		return nil
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err := ep.initNetworkManager()
	assert.NoError(t, err)
	assert.Equal(t, InitNetWorkManagerPath, replacedPath)
	assert.Contains(t, replacedContent, "dns=none")
}

func TestInitNetworkManagerWithWhitespace(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCentos{
		output:    "",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	var replacedPath string
	var replacedContent string
	patches.ApplyFunc(catAndReplace, func(path string, src string, sub string, reg string) error {
		replacedPath = path
		replacedContent = sub
		return nil
	})

	ep := &EnvPlugin{
		exec:   mockExec,
		backup: "false",
	}
	err := ep.initNetworkManager()
	assert.NoError(t, err)
	assert.Equal(t, InitNetWorkManagerPath, replacedPath)
	assert.Contains(t, replacedContent, "dns=none")
}

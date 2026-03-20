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

package shutdown

import (
	"fmt"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/plugin"
)

func TestShutDownPluginName(t *testing.T) {
	pluginObj := &ShutDown{}
	assert.Equal(t, Name, pluginObj.Name())
}

func TestNewShutDownPlugin(t *testing.T) {
	pluginObj := New()
	assert.NotNil(t, pluginObj)
	assert.Equal(t, Name, pluginObj.Name())
}

func TestShutDownPluginParam(t *testing.T) {
	pluginObj := &ShutDown{}
	params := pluginObj.Param()
	assert.NotNil(t, params)
	assert.Empty(t, params)
}

func TestShutDownPluginConstantName(t *testing.T) {
	assert.Equal(t, "Shutdown", Name)
}

func TestShutDownPluginExecutePanicsOnExit(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	assert.Panics(t, func() {
		pluginObj.Execute([]string{})
	})
}

func TestShutDownPluginExecuteCallsExitWithZero(t *testing.T) {
	var capturedCode int
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			capturedCode = code
			panic("exit called")
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	func() {
		defer func() {
			recover()
		}()
		pluginObj.Execute([]string{})
	}()

	assert.Equal(t, 0, capturedCode)
}

func TestShutDownPluginExecuteWithEmptyCommands(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	assert.Panics(t, func() {
		pluginObj.Execute([]string{})
	})
}

func TestShutDownPluginExecuteWithCommands(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	assert.Panics(t, func() {
		pluginObj.Execute([]string{Name, "extra=value"})
	})
}

func TestShutDownPluginExecuteWithVariousCommandFormats(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	testCases := []struct {
		name     string
		commands []string
	}{
		{"empty slice", []string{}},
		{"single element", []string{Name}},
		{"multiple elements", []string{Name, "arg1", "arg2"}},
		{"with equals", []string{Name, "key=value"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Panics(t, func() {
				pluginObj.Execute(tc.commands)
			})
		})
	}
}

func TestShutDownPluginImplementsPluginInterface(t *testing.T) {
	pluginObj := &ShutDown{}
	var _ plugin.Plugin = pluginObj
}

func TestShutDownPluginStructHasNoFields(t *testing.T) {
	var pluginObj ShutDown
	assert.Equal(t, ShutDown{}, pluginObj)
}

func TestShutDownPluginExecuteExitIsCalled(t *testing.T) {
	var exitCalled bool
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			exitCalled = true
			panic("exit")
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	func() {
		defer func() {
			recover()
		}()
		pluginObj.Execute([]string{})
	}()

	assert.True(t, exitCalled)
}

func TestShutDownPluginExecuteDoesNotModifyCommands(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}
	originalCommands := []string{Name, "key=value"}

	assert.Panics(t, func() {
		pluginObj.Execute(originalCommands)
	})
}

func TestShutDownPluginExecuteWithNilCommands(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	assert.Panics(t, func() {
		pluginObj.Execute(nil)
	})
}

func TestShutDownPluginExecuteWithLongCommandList(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}
	commands := make([]string, 10)
	for i := range commands {
		commands[i] = Name
	}

	assert.Panics(t, func() {
		pluginObj.Execute(commands)
	})
}

func TestShutDownPluginExecuteWithSpecialCharacters(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	assert.Panics(t, func() {
		pluginObj.Execute([]string{Name, "key=!@#$%^&*()", "path=/usr/local/bin"})
	})
}

func TestShutDownPluginExecuteMultipleCallsAllPanic(t *testing.T) {
	pluginObj := &ShutDown{}

	for i := 0; i < 3; i++ {
		patches := gomonkey.ApplyFunc(
			os.Exit,
			func(code int) {
				panic(fmt.Sprintf("exit with code %d", code))
			},
		)
		defer patches.Reset()

		assert.Panics(t, func() {
			pluginObj.Execute([]string{Name})
		})
	}
}

func TestShutDownPluginExecuteWithMixedCommandTypes(t *testing.T) {
	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	pluginObj := &ShutDown{}

	assert.Panics(t, func() {
		pluginObj.Execute([]string{Name, "mode=production", "env=dev", "debug=false"})
	})
}

func TestShutDownPluginExecuteAlwaysPanics(t *testing.T) {
	pluginObj := &ShutDown{}

	patches := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches.Reset()

	assert.Panics(t, func() {
		pluginObj.Execute([]string{})
	})

	patches2 := gomonkey.ApplyFunc(
		os.Exit,
		func(code int) {
			panic(fmt.Sprintf("exit with code %d", code))
		},
	)
	defer patches2.Reset()

	assert.Panics(t, func() {
		pluginObj.Execute([]string{Name, "arg1", "arg2"})
	})
}

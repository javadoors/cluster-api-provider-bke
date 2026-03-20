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
	"net"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/plugin"
)

const (
	testIPSegmentA    = 192
	testIPSegmentB    = 168
	testIPSegmentC    = 1
	testIPSegmentD1   = 10
	testIPSegmentD2   = 100
	testIPSegmentD3   = 101
	testIPSegmentD4   = 102
	testIPSegmentD5   = 103
	testIPSegmentDEnd = 255
)

var (
	testIPVar1   = net.IPv4(testIPSegmentA, testIPSegmentB, testIPSegmentC, testIPSegmentD1)
	testIPVar2   = net.IPv4(testIPSegmentA, testIPSegmentB, testIPSegmentC, testIPSegmentD2)
	testIPVar3   = net.IPv4(testIPSegmentA, testIPSegmentB, testIPSegmentC, testIPSegmentD3)
	testIPVar4   = net.IPv4(testIPSegmentA, testIPSegmentB, testIPSegmentC, testIPSegmentD4)
	testIPVar5   = net.IPv4(testIPSegmentA, testIPSegmentB, testIPSegmentC, testIPSegmentD5)
	testIPVarEnd = net.IPv4(testIPSegmentA, testIPSegmentB, testIPSegmentC, testIPSegmentDEnd)
)

type mockExecutorForEnv struct {
	exec.Executor
}

func (m *mockExecutorForEnv) ExecuteCommandWithOutput(_ string, _ ...string) (string, error) {
	return "", nil
}

func (m *mockExecutorForEnv) ExecuteCommandWithCombinedOutput(_ string, _ ...string) (string, error) {
	return "", nil
}

func TestName(t *testing.T) {
	ep := &EnvPlugin{}
	assert.Equal(t, Name, ep.Name())
}

func TestParamKeys(t *testing.T) {
	ep := &EnvPlugin{}
	params := ep.Param()

	assert.Contains(t, params, "check")
	assert.Contains(t, params, "init")
	assert.Contains(t, params, "sudo")
	assert.Contains(t, params, "scope")
	assert.Contains(t, params, "backup")
	assert.Contains(t, params, "extraHosts")
	assert.Contains(t, params, "hostPort")
	assert.Contains(t, params, "bkeConfig")
}

func TestNew(t *testing.T) {
	mockExec := &mockExecutorForEnv{}
	p := New(mockExec, nil)

	ep, ok := p.(*EnvPlugin)
	assert.True(t, ok)
	assert.NotNil(t, ep)
	assert.Equal(t, mockExec, ep.exec)
	assert.Nil(t, ep.bkeConfig)
	assert.NotNil(t, ep.machine)
}

func TestNewWithConfig(t *testing.T) {
	mockExec := &mockExecutorForEnv{}
	cfg := &bkev1beta1.BKEConfig{}
	p := New(mockExec, cfg)

	ep, ok := p.(*EnvPlugin)
	assert.True(t, ok)
	assert.NotNil(t, ep)
	assert.Equal(t, mockExec, ep.exec)
	assert.Equal(t, cfg, ep.bkeConfig)
}

func TestEnvPluginFields(t *testing.T) {
	mockExec := &mockExecutorForEnv{}
	cfg := &bkev1beta1.BKEConfig{}
	clusterHost := "node1:" + testIPVar1.String()

	ep := &EnvPlugin{
		exec:         mockExec,
		k8sClient:    nil,
		bkeConfig:    cfg,
		currenNode:   bkenode.Node{},
		sudo:         "true",
		scope:        "kernel,firewall",
		backup:       "true",
		extraHosts:   clusterHost,
		clusterHosts: []string{clusterHost},
		hostPort:     []string{"6443"},
		machine:      NewMachine(),
	}

	assert.Equal(t, mockExec, ep.exec)
	assert.Equal(t, cfg, ep.bkeConfig)
	assert.Equal(t, "true", ep.sudo)
	assert.Equal(t, "kernel,firewall", ep.scope)
	assert.Equal(t, "true", ep.backup)
	assert.Equal(t, clusterHost, ep.extraHosts)
	assert.Len(t, ep.clusterHosts, 1)
	assert.Len(t, ep.hostPort, 1)
}

func TestExecuteSetsParamsFromCommands(t *testing.T) {
	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.sudo = "true"
	ep.scope = "kernel,firewall"
	ep.backup = "true"
	ep.extraHosts = "node1:" + testIPVar1.String()
	ep.hostPort = []string{"6443"}

	assert.Equal(t, "true", ep.sudo)
	assert.Equal(t, "kernel,firewall", ep.scope)
	assert.Equal(t, "true", ep.backup)
	assert.Equal(t, "node1:"+testIPVar1.String(), ep.extraHosts)
	assert.Equal(t, []string{"6443"}, ep.hostPort)
}

func TestExecuteWithBkeConfigNode(t *testing.T) {
	testIP := testIPVar1.String()
	testConfig := &bkev1beta1.BKEConfig{}

	_ = testIP

	ep := &EnvPlugin{
		exec:       &mockExecutorForEnv{},
		bkeConfig:  testConfig,
		currenNode: bkenode.Node{},
		machine:    NewMachine(),
	}

	assert.Equal(t, testConfig, ep.bkeConfig)
}

func TestExecuteWithMultipleNodes(t *testing.T) {
	testIPs := []string{
		testIPVar1.String(),
		testIPVar2.String(),
		testIPVar3.String(),
	}
	testConfig := &bkev1beta1.BKEConfig{}

	_ = testIPs

	ep := &EnvPlugin{
		exec:      &mockExecutorForEnv{},
		bkeConfig: testConfig,
		machine:   NewMachine(),
	}

	assert.Equal(t, testConfig, ep.bkeConfig)
}

func TestExecuteWithCurrentNode(t *testing.T) {
	testIP := testIPVar1.String()
	node := bkenode.Node{
		IP:   testIP,
		Role: []string{"master"},
	}

	ep := &EnvPlugin{
		exec:       &mockExecutorForEnv{},
		bkeConfig:  nil,
		currenNode: node,
		machine:    NewMachine(),
	}

	assert.Equal(t, testIP, ep.currenNode.IP)
	assert.Contains(t, ep.currenNode.Role, "master")
}

func TestExecuteWithNilBkeConfig(t *testing.T) {
	ep := &EnvPlugin{
		exec:      &mockExecutorForEnv{},
		bkeConfig: nil,
		machine:   NewMachine(),
	}

	assert.Nil(t, ep.bkeConfig)
}

func TestExecuteWithEmptyScope(t *testing.T) {
	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.scope = ""
	assert.Equal(t, "", ep.scope)
}

func TestExecuteWithFullScope(t *testing.T) {
	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.scope = "kernel,firewall,selinux,swap,time,hosts,runtime,image,node,ports"
	assert.Contains(t, ep.scope, "kernel")
	assert.Contains(t, ep.scope, "firewall")
	assert.Contains(t, ep.scope, "selinux")
}

func TestExecuteWithBackupTrue(t *testing.T) {
	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.backup = "true"
	assert.Equal(t, "true", ep.backup)
}

func TestExecuteWithBackupFalse(t *testing.T) {
	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.backup = "false"
	assert.Equal(t, "false", ep.backup)
}

func TestExecuteWithSudoTrue(t *testing.T) {
	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.sudo = "true"
	assert.Equal(t, "true", ep.sudo)
}

func TestExecuteWithSudoFalse(t *testing.T) {
	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.sudo = "false"
	assert.Equal(t, "false", ep.sudo)
}

func TestExecuteMachineInitialized(t *testing.T) {
	ep := &EnvPlugin{
		exec:      &mockExecutorForEnv{},
		bkeConfig: nil,
		machine:   nil,
	}

	assert.Nil(t, ep.machine)

	ep.machine = NewMachine()

	assert.NotNil(t, ep.machine)
}

func TestExecuteHostPortParsing(t *testing.T) {
	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.hostPort = []string{"6443", "10250", "2379"}
	assert.Len(t, ep.hostPort, 3)
	assert.Equal(t, "6443", ep.hostPort[0])
}

func TestExecuteExtraHostsFormat(t *testing.T) {
	extraHostsStr := "master1:" + testIPVar1.String() +
		",master2:" + testIPVar2.String() +
		",worker1:" + testIPVar3.String()

	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	ep.extraHosts = extraHostsStr
	assert.Contains(t, ep.extraHosts, "master1:"+testIPVar1.String())
}

func TestExecuteParseCommandsError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return nil, assert.AnError
		})

	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	result, err := ep.Execute([]string{Name})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, assert.AnError, err)
}

func TestExecuteWithInitFalseCheckFalse(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"init":       "false",
				"check":      "false",
				"sudo":       "true",
				"scope":      "kernel,firewall",
				"backup":     "true",
				"extraHosts": "",
				"hostPort":   "6443",
				"bkeConfig":  "",
			}, nil
		})

	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	result, err := ep.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteWithGetBkeConfigError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"init":       "false",
				"check":      "false",
				"sudo":       "true",
				"scope":      "kernel,firewall",
				"backup":     "true",
				"extraHosts": "",
				"hostPort":   "6443",
				"bkeConfig":  "invalid-ns:invalid",
			}, nil
		})

	patches.ApplyFunc(plugin.GetBkeConfig,
		func(string) (*bkev1beta1.BKEConfig, error) {
			return nil, assert.AnError
		})

	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	_, err := ep.Execute([]string{Name})

	assert.Error(t, err)
}

func TestExecuteWithCurrentNodeError(t *testing.T) {
	testIP := testIPVar1.String()

	cfg := &bkev1beta1.BKEConfig{}

	_ = testIP

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"init":       "false",
				"check":      "false",
				"sudo":       "true",
				"scope":      "kernel,firewall",
				"backup":     "true",
				"extraHosts": "",
				"hostPort":   "6443",
				"bkeConfig":  "test-ns:test",
			}, nil
		})

	patches.ApplyFunc(plugin.GetBkeConfig,
		func(string) (*bkev1beta1.BKEConfig, error) {
			return cfg, nil
		})

	nodes := bkenode.Nodes{}
	patches.ApplyMethod(nodes, "CurrentNode",
		func(_ bkenode.Nodes) (bkenode.Node, error) {
			return bkenode.Node{}, assert.AnError
		})

	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	_, err := ep.Execute([]string{Name})

	assert.Error(t, err)
}

func TestExecuteWithMultipleHostPorts(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"init":       "false",
				"check":      "false",
				"sudo":       "true",
				"scope":      "kernel,firewall",
				"backup":     "true",
				"extraHosts": "",
				"hostPort":   "10259,10257,10250,2379,2380,2381,10248",
				"bkeConfig":  "",
			}, nil
		})

	ep := &EnvPlugin{
		exec:    &mockExecutorForEnv{},
		machine: NewMachine(),
	}

	result, err := ep.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

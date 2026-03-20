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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	bkesource "gopkg.openfuyao.cn/cluster-api-provider-bke/common/source"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/crontab"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	runtimeutils "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/runtime"
)

const (
	numZero                  = 0
	numOne                   = 1
	numTwo                   = 2
	numThree                 = 3
	numFour                  = 4
	numFive                  = 5
	numSix                   = 6
	numSeven                 = 7
	numEight                 = 8
	numNine                  = 9
	numTen                   = 10
	numSixteen               = 16
	numSeventeen             = 17
	numTwentyFour            = 24
	numSixtyFour             = 64
	numOneHundred            = 100
	numOneTwentySeven        = 127
	numOneSixtyFive          = 165
	numOneHundredTwentyEight = 128

	ipv4LoopbackSegmentA = numOneTwentySeven
	ipv4LoopbackSegmentB = numZero
	ipv4LoopbackSegmentC = numZero
	ipv4LoopbackSegmentD = numOne

	privateClassASegmentA = numTen
	privateClassASegmentB = numZero
	privateClassASegmentC = numZero
	privateClassASegmentD = numOne

	ipv4LocalhostStr  = "127.0.0.1"
	ipv4LocalhostPort = "10259"
	testTimeout       = 3 * time.Second
	shortWaitTimeout  = 100 * time.Millisecond
)

var (
	ipv4Localhost     = net.IPv4(ipv4LoopbackSegmentA, ipv4LoopbackSegmentB, ipv4LoopbackSegmentC, ipv4LoopbackSegmentD)
	ipv4PrivateClassA = net.IPv4(privateClassASegmentA, privateClassASegmentB, privateClassASegmentC, privateClassASegmentD)
)

type mockExecutorForCheck struct {
	exec.Executor
	output    string
	outputErr error
}

func (m *mockExecutorForCheck) ExecuteCommandWithOutput(command string, arg ...string) (string, error) {
	return m.output, m.outputErr
}

func (m *mockExecutorForCheck) ExecuteCommandWithCombinedOutput(command string, arg ...string) (string, error) {
	return m.output, m.outputErr
}

func TestGetPortOpenResultOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer ln.Close()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), testTimeout)
	assert.NoError(t, err)
	defer conn.Close()

	err = getPortOpenResult(nil, conn)
	assert.NoError(t, err)
}

func TestGetPortOpenResultClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	ln.Close()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), testTimeout)
	if err != nil {
		conn = nil
	}

	err = getPortOpenResult(err, conn)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not open")
}

func TestGetPortOpenResultWithError(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "192.0.2.1:1", shortWaitTimeout)
	if err != nil {
		conn = nil
	}
	resultErr := getPortOpenResult(err, conn)
	assert.Error(t, resultErr)
}

func TestCheckFirewallDisabled(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "dead",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	ep := &EnvPlugin{
		exec:    mockExec,
		scope:   "firewall",
		machine: NewMachine(),
	}
	err := ep.checkFirewall()
	assert.NoError(t, err)
}

func TestCheckFirewallNotDead(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "active",
		outputErr: nil,
	}

	ep := &EnvPlugin{
		exec:    mockExec,
		scope:   "firewall",
		machine: NewMachine(),
	}
	err := ep.checkFirewall()
	assert.NoError(t, err)
}

func TestCheckFirewallCommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "not loaded",
		outputErr: nil,
	}

	ep := &EnvPlugin{
		exec:    mockExec,
		scope:   "firewall",
		machine: NewMachine(),
	}
	err := ep.checkFirewall()
	assert.NoError(t, err)
}

func TestCheckFirewallNotFound(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "not be found",
		outputErr: nil,
	}

	ep := &EnvPlugin{
		exec:    mockExec,
		scope:   "firewall",
		machine: NewMachine(),
	}
	err := ep.checkFirewall()
	assert.NoError(t, err)
}

func TestCheckSelinuxUbuntu(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "Enforcing",
		outputErr: nil,
	}

	ep := &EnvPlugin{
		exec:    mockExec,
		scope:   "selinux",
		machine: &Machine{platform: "ubuntu"},
	}
	err := ep.checkSelinux()
	assert.NoError(t, err)
}

func TestCheckSelinuxDisabled(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "Permissive",
		outputErr: nil,
	}

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	ep := &EnvPlugin{
		exec:    mockExec,
		scope:   "selinux",
		machine: &Machine{platform: "centos"},
	}
	err := ep.checkSelinux()
	assert.NoError(t, err)
}

func TestCheckSwapDisabled(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	ep := &EnvPlugin{
		scope:   "swap",
		machine: NewMachine(),
	}
	err := ep.checkSwap()
	assert.NoError(t, err)
}

func TestCheckSwapEnabled(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	ep := &EnvPlugin{
		scope:   "swap",
		machine: NewMachine(),
	}
	err := ep.checkSwap()
	assert.NoError(t, err)
}

func TestCheckSwapError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, os.ErrPermission
	})

	ep := &EnvPlugin{
		scope:   "swap",
		machine: NewMachine(),
	}
	err := ep.checkSwap()
	assert.Error(t, err)
}

func TestCheckDNSExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(path string) bool {
		return true
	})

	ep := &EnvPlugin{
		scope:   "dns",
		machine: &Machine{hostOS: "linux"},
	}
	err := ep.checkDNS()
	assert.NoError(t, err)
}

func TestCheckDNSNotExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(path string) bool {
		return false
	})

	ep := &EnvPlugin{
		scope:   "dns",
		machine: &Machine{hostOS: "linux"},
	}
	err := ep.checkDNS()
	assert.Error(t, err)
}

func TestCheckRuntimeMatch(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(runtimeutils.DetectRuntime, func() string {
		return runtimeutils.ContainerRuntimeDocker
	})

	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			ContainerRuntime: bkev1beta1.ContainerRuntime{
				CRI: runtimeutils.ContainerRuntimeDocker,
			},
		},
	}

	ep := &EnvPlugin{
		scope:     "runtime",
		bkeConfig: cfg,
		machine:   NewMachine(),
	}
	err := ep.checkRuntime()
	assert.NoError(t, err)
}

func TestCheckRuntimeMismatch(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(runtimeutils.DetectRuntime, func() string {
		return runtimeutils.ContainerRuntimeDocker
	})

	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			ContainerRuntime: bkev1beta1.ContainerRuntime{
				CRI: runtimeutils.ContainerRuntimeContainerd,
			},
		},
	}

	ep := &EnvPlugin{
		scope:     "runtime",
		bkeConfig: cfg,
		machine:   NewMachine(),
	}
	err := ep.checkRuntime()
	assert.Error(t, err)
}

func TestCheckRuntimeNoRuntime(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(runtimeutils.DetectRuntime, func() string {
		return ""
	})

	ep := &EnvPlugin{
		scope:     "runtime",
		bkeConfig: &bkev1beta1.BKEConfig{},
		machine:   NewMachine(),
	}
	err := ep.checkRuntime()
	assert.Error(t, err)
}

func TestCheckRuntimeNoConfigNoRuntime(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(runtimeutils.DetectRuntime, func() string {
		return ""
	})

	ep := &EnvPlugin{
		scope:     "runtime",
		bkeConfig: nil,
		machine:   NewMachine(),
	}
	err := ep.checkRuntime()
	assert.Error(t, err)
}

func TestCheckHostPortAvailable(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer ln.Close()

	addr := ln.Addr().String()
	port := addr[strings.LastIndex(addr, ":")+1:]

	patches.ApplyFunc(epHostPortChecker, func(target string) error {
		return nil
	})

	ep := &EnvPlugin{
		scope:    "ports",
		hostPort: []string{port},
		machine:  NewMachine(),
	}
	err = ep.checkHostPort()
	assert.NoError(t, err)
}

func TestCheckHostPortUnavailable(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(epHostPortChecker, func(target string) error {
		return os.ErrPermission
	})

	ep := &EnvPlugin{
		scope:    "ports",
		hostPort: []string{ipv4LocalhostPort},
		machine:  NewMachine(),
	}
	err := ep.checkHostPort()
	assert.Error(t, err)
}

func TestCheckHostPortMultiple(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(epHostPortChecker, func(target string) error {
		return nil
	})

	ep := &EnvPlugin{
		scope:    "ports",
		hostPort: []string{"10250", "2379", "2380"},
		machine:  NewMachine(),
	}
	err := ep.checkHostPort()
	assert.Error(t, err)
}

func TestCheckNodeInfoMasterInsufficientCPU(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	machine := &Machine{
		cpuNum:  numOne,
		memSize: numFour,
	}

	cfg := &bkev1beta1.BKEConfig{}

	nodes := bkenode.Nodes{}
	patches.ApplyMethod(nodes, "CurrentNode", func(_ bkenode.Nodes) (bkenode.Node, error) {
		return bkenode.Node{
			IP:   "192.168.1.1",
			Role: []string{"master"},
		}, nil
	})

	patches.ApplyFunc(utils.ContainsString, func(slice []string, item string) bool {
		return item == "master"
	})

	ep := &EnvPlugin{
		scope:     "node",
		bkeConfig: cfg,
		machine:   machine,
		nodes:     nodes,
		currenNode: bkenode.Node{
			IP:   "192.168.1.1",
			Role: []string{"master"},
		},
	}
	err := ep.checkNodeInfo()
	assert.Error(t, err)
}

func TestCheckNodeInfoMasterInsufficientMemory(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	machine := &Machine{
		cpuNum:  numTwo,
		memSize: numOne,
	}

	cfg := &bkev1beta1.BKEConfig{}

	nodes := bkenode.Nodes{}
	patches.ApplyMethod(nodes, "CurrentNode", func(_ bkenode.Nodes) (bkenode.Node, error) {
		return bkenode.Node{
			IP:   "192.168.1.1",
			Role: []string{"master"},
		}, nil
	})

	patches.ApplyFunc(utils.ContainsString, func(slice []string, item string) bool {
		return item == "master"
	})

	ep := &EnvPlugin{
		scope:     "node",
		bkeConfig: cfg,
		machine:   machine,
		nodes:     nodes,
		currenNode: bkenode.Node{
			IP:   "192.168.1.1",
			Role: []string{"master"},
		},
	}
	err := ep.checkNodeInfo()
	assert.Error(t, err)
}

func TestCheckNodeInfoMasterSufficient(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	machine := &Machine{
		cpuNum:  numFour,
		memSize: numEight,
	}

	cfg := &bkev1beta1.BKEConfig{}

	nodes := bkenode.Nodes{}
	patches.ApplyMethod(nodes, "CurrentNode", func(_ bkenode.Nodes) (bkenode.Node, error) {
		return bkenode.Node{
			IP:   "192.168.1.1",
			Role: []string{"master"},
		}, nil
	})

	patches.ApplyFunc(utils.ContainsString, func(slice []string, item string) bool {
		return item == "master"
	})

	ep := &EnvPlugin{
		scope:     "node",
		bkeConfig: cfg,
		machine:   machine,
		nodes:     nodes,
		currenNode: bkenode.Node{
			IP:   "192.168.1.1",
			Role: []string{"master"},
		},
	}
	err := ep.checkNodeInfo()
	assert.NoError(t, err)
}

func TestCheckNodeInfoWorker(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	machine := &Machine{
		cpuNum:  numOne,
		memSize: numTwo,
	}

	cfg := &bkev1beta1.BKEConfig{}

	nodes := bkenode.Nodes{}
	patches.ApplyMethod(nodes, "CurrentNode", func(_ bkenode.Nodes) (bkenode.Node, error) {
		return bkenode.Node{
			IP:   "192.168.1.2",
			Role: []string{"worker"},
		}, nil
	})

	patches.ApplyFunc(utils.ContainsString, func(slice []string, item string) bool {
		return item == "worker"
	})

	ep := &EnvPlugin{
		scope:     "node",
		bkeConfig: cfg,
		machine:   machine,
		nodes:     nodes,
		currenNode: bkenode.Node{
			IP:   "192.168.1.2",
			Role: []string{"worker"},
		},
	}
	err := ep.checkNodeInfo()
	assert.NoError(t, err)
}

func TestCheckK8sEnvKernelCheck(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(path string) bool {
		return true
	})

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	ep := &EnvPlugin{
		scope:   "kernel",
		machine: NewMachine(),
	}
	err := ep.checkK8sEnv()
	assert.NoError(t, err)
}

func TestCheckK8sEnvMultipleScopes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(path string) bool {
		return true
	})

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	patches.ApplyFunc(crontab.FindSyncTimeJob, func() bool {
		return true
	})

	mockExec := &mockExecutorForCheck{
		output:    "not be found",
		outputErr: nil,
	}

	ep := &EnvPlugin{
		exec:    mockExec,
		scope:   "kernel,firewall,selinux,swap,time",
		machine: NewMachine(),
	}
	err := ep.checkK8sEnv()
	assert.NoError(t, err)
}

func TestCheckK8sEnvEmptyScope(t *testing.T) {
	ep := &EnvPlugin{
		scope:   "",
		machine: NewMachine(),
	}
	err := ep.checkK8sEnv()
	assert.NoError(t, err)
}

func TestCheckK8sEnvWithErrors(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists, func(path string) bool {
		return false
	})

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	patches.ApplyFunc(crontab.FindSyncTimeJob, func() bool {
		return false
	})

	mockExec := &mockExecutorForCheck{
		output:    "active",
		outputErr: nil,
	}

	ep := &EnvPlugin{
		scope:   "firewall,selinux,swap,time",
		exec:    mockExec,
		machine: &Machine{platform: "centos"},
	}
	err := ep.checkK8sEnv()
	assert.NoError(t, err)
}

func epHostPortChecker(target string) error {
	return nil
}

func TestCheckHostHostnameMatch(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	testHostname := "PC1SV6SV"

	patches.ApplyFunc(os.Hostname, func() (string, error) {
		return testHostname, nil
	})

	patches.ApplyFunc(utils.HostName, func() string {
		return testHostname
	})

	patches.ApplyFunc(NewHostsFile, func(path string) (*HostsFile, error) {
		return &HostsFile{}, nil
	})

	patches.ApplyFunc(NewMachine, func() *Machine {
		return &Machine{}
	})

	ep := &EnvPlugin{
		scope:        "hosts",
		machine:      &Machine{},
		extraHosts:   "",
		clusterHosts: nil,
	}
	err := ep.checkHost()
	assert.NoError(t, err)
}

func TestCheckHostHostnameMismatch(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(os.Hostname, func() (string, error) {
		return "actual-hostname", nil
	})
	patches.ApplyFunc(utils.HostName, func() string {
		return "expected-hostname"
	})

	ep := &EnvPlugin{
		scope:        "hosts",
		machine:      NewMachine(),
		extraHosts:   "",
		clusterHosts: nil,
	}
	err := ep.checkHost()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Hostname is not match")
}

func TestCheckHostHostnameError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(os.Hostname, func() (string, error) {
		return "", os.ErrPermission
	})

	patches.ApplyFunc(utils.HostName, func() string {
		return "PC1SV6SV"
	})

	patches.ApplyFunc(NewHostsFile, func(path string) (*HostsFile, error) {
		return nil, os.ErrNotExist
	})

	patches.ApplyFunc(NewMachine, func() *Machine {
		return &Machine{}
	})

	ep := &EnvPlugin{
		scope:        "hosts",
		machine:      &Machine{},
		extraHosts:   "",
		clusterHosts: nil,
	}
	err := ep.checkHost()
	assert.Error(t, err)
}

func TestCheckHostHostsFileError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	testHostname := "PC1SV6SV"

	patches.ApplyFunc(os.Hostname, func() (string, error) {
		return testHostname, nil
	})

	patches.ApplyFunc(utils.HostName, func() string {
		return testHostname
	})

	patches.ApplyFunc(NewHostsFile, func(path string) (*HostsFile, error) {
		return nil, os.ErrNotExist
	})

	ep := &EnvPlugin{
		scope:        "hosts",
		machine:      NewMachine(),
		extraHosts:   "",
		clusterHosts: nil,
	}
	err := ep.checkHost()
	assert.Error(t, err)
}

func TestCheckHttpRepoConfigNil(t *testing.T) {
	ep := &EnvPlugin{
		scope:     "httpRepo",
		bkeConfig: nil,
		machine:   NewMachine(),
	}
	err := ep.checkHttpRepo()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bke config not found")
}

func TestCheckHttpRepoYumRepoEmpty(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(bkesource.GetRPMDownloadPath, func(repo string) (string, error) {
		return "", nil
	})

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	ep := &EnvPlugin{
		scope: "httpRepo",
		bkeConfig: &bkev1beta1.BKEConfig{
			Cluster: bkev1beta1.Cluster{
				ContainerRuntime: bkev1beta1.ContainerRuntime{},
			},
		},
		machine: &Machine{platform: "ubuntu"},
	}
	err := ep.checkHttpRepo()
	assert.NoError(t, err)
}

func TestCheckHttpRepoUbuntuFound(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(bkesource.GetRPMDownloadPath, func(repo string) (string, error) {
		return "/test/path", nil
	})

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return true, nil
	})

	ep := &EnvPlugin{
		scope: "httpRepo",
		bkeConfig: &bkev1beta1.BKEConfig{
			Cluster: bkev1beta1.Cluster{
				ContainerRuntime: bkev1beta1.ContainerRuntime{},
			},
		},
		machine: &Machine{platform: "ubuntu"},
	}
	err := ep.checkHttpRepo()
	assert.NoError(t, err)
}

func TestCheckHttpRepoUbuntuNotFound(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(bkesource.GetRPMDownloadPath, func(repo string) (string, error) {
		return "/test/path", nil
	})

	patches.ApplyFunc(catAndSearch, func(path string, key string, reg string) (bool, error) {
		return false, nil
	})

	ep := &EnvPlugin{
		scope: "httpRepo",
		bkeConfig: &bkev1beta1.BKEConfig{
			Cluster: bkev1beta1.Cluster{
				ContainerRuntime: bkev1beta1.ContainerRuntime{},
			},
		},
		machine: &Machine{platform: "ubuntu"},
	}
	err := ep.checkHttpRepo()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bke repo not found")
}

func TestCheckHttpRepoUbuntuGetPathError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(bkesource.GetRPMDownloadPath, func(repo string) (string, error) {
		return "", errors.New("failed to get download path")
	})

	ep := &EnvPlugin{
		scope: "httpRepo",
		bkeConfig: &bkev1beta1.BKEConfig{
			Cluster: bkev1beta1.Cluster{
				ContainerRuntime: bkev1beta1.ContainerRuntime{},
			},
		},
		machine: &Machine{platform: "ubuntu"},
	}
	err := ep.checkHttpRepo()
	assert.Error(t, err)
}

func TestCheckHttpRepoCentosFound(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "repo id: bke\nrepo name: BKE Repository",
		outputErr: nil,
	}

	ep := &EnvPlugin{
		scope: "httpRepo",
		bkeConfig: &bkev1beta1.BKEConfig{
			Cluster: bkev1beta1.Cluster{
				ContainerRuntime: bkev1beta1.ContainerRuntime{},
			},
		},
		exec:    mockExec,
		machine: &Machine{platform: "centos"},
	}
	err := ep.checkHttpRepo()
	assert.NoError(t, err)
}

func TestCheckHttpRepoCentosNotFound(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "repo id: other\nrepo name: Other Repository",
		outputErr: nil,
	}

	ep := &EnvPlugin{
		scope: "httpRepo",
		bkeConfig: &bkev1beta1.BKEConfig{
			Cluster: bkev1beta1.Cluster{
				ContainerRuntime: bkev1beta1.ContainerRuntime{},
			},
		},
		exec:    mockExec,
		machine: &Machine{platform: "centos"},
	}
	err := ep.checkHttpRepo()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bke repo not found")
}

func TestCheckHttpRepoCentosCommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutorForCheck{
		output:    "",
		outputErr: errors.New("command failed"),
	}

	ep := &EnvPlugin{
		scope: "httpRepo",
		bkeConfig: &bkev1beta1.BKEConfig{
			Cluster: bkev1beta1.Cluster{
				ContainerRuntime: bkev1beta1.ContainerRuntime{},
			},
		},
		exec:    mockExec,
		machine: &Machine{platform: "centos"},
	}
	err := ep.checkHttpRepo()
	assert.Error(t, err)
}

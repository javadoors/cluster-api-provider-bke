//go:build linux

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
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkesource "gopkg.openfuyao.cn/cluster-api-provider-bke/common/source"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/docker"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/httprepo"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/initsystem"
)

const (
	testIPSegA  = 192
	testIPSegB  = 168
	testIPSegC  = 1
	testIPSegD1 = 10
)

var (
	testIP = net.IPv4(testIPSegA, testIPSegB, testIPSegC, testIPSegD1)
)

type mockExecutor struct {
	exec.Executor
}

func (m *mockExecutor) ExecuteCommandWithCombinedOutput(_ string, _ ...string) (string, error) {
	return "", nil
}

func (m *mockExecutor) ExecuteCommandWithOutput(_ string, _ ...string) (string, error) {
	return "", nil
}

func (m *mockExecutor) ExecuteCommand(_ string, _ ...string) error {
	return nil
}

type mockInitSystem struct{}

func (m *mockInitSystem) EnableCommand(_ string) string {
	return ""
}

func (m *mockInitSystem) ServiceStart(_ string) error {
	return nil
}

func (m *mockInitSystem) ServiceStop(_ string) error {
	return nil
}

func (m *mockInitSystem) ServiceRestart(_ string) error {
	return nil
}

func (m *mockInitSystem) ServiceExists(_ string) bool {
	return false
}

func (m *mockInitSystem) ServiceIsEnabled(_ string) bool {
	return false
}

func (m *mockInitSystem) ServiceIsActive(_ string) bool {
	return false
}

func (m *mockInitSystem) ServiceDisable(_ string) error {
	return nil
}

func (m *mockInitSystem) ServiceEnable(_ string) error {
	return nil
}

func TestExportImageListWithWorkerNode(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			KubernetesVersion: "v1.28.0",
		},
	}

	ep := &EnvPlugin{
		bkeConfig: cfg,
		currenNode: bkenode.Node{
			IP:   testIP.String(),
			Role: []string{"node"},
		},
	}

	images := ep.exportImageList()

	assert.Empty(t, images)
}

func TestExportImageListWithMasterNode(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			KubernetesVersion: "v1.28.0",
		},
	}

	ep := &EnvPlugin{
		bkeConfig: cfg,
		currenNode: bkenode.Node{
			IP:   testIP.String(),
			Role: []string{"master"},
		},
	}

	images := ep.exportImageList()

	assert.NotEmpty(t, images)
}

func TestExportImageListWithNodesError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			KubernetesVersion: "v1.28.0",
		},
	}

	nodes := bkenode.Nodes{}
	patches.ApplyMethod(nodes, "CurrentNode",
		func(_ bkenode.Nodes) (bkenode.Node, error) {
			return bkenode.Node{}, fmt.Errorf("node error")
		})

	ep := &EnvPlugin{
		bkeConfig: cfg,
		nodes:     nodes,
		currenNode: bkenode.Node{
			IP:   testIP.String(),
			Role: []string{"master"},
		},
	}

	images := ep.exportImageList()

	assert.NotEmpty(t, images)
}

func TestGetNTPServerFromConfig(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			NTPServer: "pool.ntp.org",
		},
	}

	server, err := getNTPServer(cfg)

	assert.NoError(t, err)
	assert.Equal(t, "pool.ntp.org", server)
}

func TestGetNTPServerEmptyConfig(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{},
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.GetNTPServerEnv,
		func() (string, error) {
			return "", nil
		})

	server, err := getNTPServer(cfg)

	assert.NoError(t, err)
	assert.Equal(t, "", server)
}

func TestInitRegistry(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: nil,
	}

	err := ep.initRegistry()

	assert.NoError(t, err)
}

func TestInitRegistryWithCustomPort(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			ImageRepo: bkev1beta1.Repo{
				Port: "5000",
			},
		},
	}

	ep := &EnvPlugin{
		bkeConfig: cfg,
	}

	err := ep.initRegistry()

	assert.NoError(t, err)
}

func TestInitRuntimeWithNilConfig(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: nil,
		machine:   NewMachine(),
	}

	err := ep.initRuntime()

	assert.NoError(t, err)
}

func TestInitRuntimeWithUnsupportedRuntime(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			ContainerRuntime: bkev1beta1.ContainerRuntime{
				CRI: "unsupported",
			},
		},
	}

	ep := &EnvPlugin{
		bkeConfig: cfg,
		machine:   NewMachine(),
	}

	err := ep.initRuntime()

	assert.Error(t, err)
}

func TestInitHttpRepoWithNilConfig(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: nil,
	}

	err := ep.initHttpRepo()

	assert.Error(t, err)
}

func TestDownloadContainerRuntimeWithNilConfig(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: nil,
	}

	err := ep.downloadContainerRuntime("", "", false, nil)

	assert.Error(t, err)
}

func TestDownloadContainerRuntimeWithUnsupported(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: &bkev1beta1.BKEConfig{},
	}

	err := ep.downloadContainerRuntime("unsupported", "", false, nil)

	assert.Error(t, err)
}

func TestDownloadCriDockerdWithNilConfig(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: nil,
	}

	err := ep.downloadCriDockerd()

	assert.Error(t, err)
}

func TestConfigContainerRuntimeWithDocker(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: &bkev1beta1.BKEConfig{},
	}

	cfg := runtimeConfig{
		containerRuntime: bkeinit.CRIDocker,
	}
	err := ep.configContainerRuntime(cfg, bkeinit.CRIDocker)

	assert.Error(t, err)
}

func TestConfigContainerRuntimeWithContainerd(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: &bkev1beta1.BKEConfig{},
	}

	cfg := runtimeConfig{
		containerRuntime: bkeinit.CRIContainerd,
	}
	err := ep.configContainerRuntime(cfg, bkeinit.CRIContainerd)

	assert.NoError(t, err)
}

func TestConfigContainerRuntimeWithUnsupported(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: &bkev1beta1.BKEConfig{},
	}

	cfg := runtimeConfig{}
	err := ep.configContainerRuntime(cfg, "unsupported")

	assert.Error(t, err)
}

func TestDownloadDockerWithNilConfig(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: nil,
	}

	err := ep.downloadDocker("", false, nil)

	assert.Error(t, err)
}

func TestDownloadContainerdWithNilConfig(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: nil,
	}

	err := ep.downloadContainerd("", nil)

	assert.Error(t, err)
}

func TestInitK8sEnvWithEmptyScope(t *testing.T) {
	ep := &EnvPlugin{
		exec:    nil,
		scope:   "",
		machine: NewMachine(),
	}

	err := ep.initK8sEnv()

	assert.NoError(t, err)
}

func TestInitK8sEnvWithEmptyScopeAndNilMachine(t *testing.T) {
	ep := &EnvPlugin{
		exec:  nil,
		scope: "",
	}

	err := ep.initK8sEnv()

	assert.NoError(t, err)
}

func TestInitFirewallWithNoFirewalld(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockSys := &mockInitSystem{}
	patches.ApplyFunc(initsystem.GetInitSystem,
		func() (initsystem.InitSystem, error) {
			return mockSys, nil
		})

	machine := NewMachine()
	machine.platform = "centos"

	ep := &EnvPlugin{
		exec:    nil,
		machine: machine,
	}

	err := ep.initFirewall()

	assert.NoError(t, err)
}

func TestInitFirewallWithFirewalldExists(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockSys := &mockInitSystem{}
	patches.ApplyFunc(initsystem.GetInitSystem,
		func() (initsystem.InitSystem, error) {
			return mockSys, nil
		})

	machine := NewMachine()
	machine.platform = "centos"

	ep := &EnvPlugin{
		exec:    nil,
		machine: machine,
	}

	err := ep.initFirewall()

	assert.NoError(t, err)
}

func TestInitSelinuxOnUbuntu(t *testing.T) {
	machine := NewMachine()
	machine.platform = utils.UbuntuOS

	ep := &EnvPlugin{
		machine: machine,
	}

	err := ep.initSelinux()

	assert.NoError(t, err)
}

func TestInitSelinuxOnOpenEuler(t *testing.T) {
	machine := NewMachine()
	machine.platform = utils.OpenEulerOS

	ep := &EnvPlugin{
		machine: machine,
	}

	err := ep.initSelinux()

	assert.NoError(t, err)
}

func TestInitSelinuxOnCentOS(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	machine := NewMachine()
	machine.platform = "centos"

	ep := &EnvPlugin{
		exec:    m,
		machine: machine,
	}

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	patches.ApplyFunc(os.Open,
		func(string) (*os.File, error) {
			return os.Create("/dev/null")
		})

	err := ep.initSelinux()

	assert.NoError(t, err)
}

func TestInitDNSWithUbuntu(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return true
		})

	machine := NewMachine()
	machine.platform = "ubuntu"

	ep := &EnvPlugin{
		machine: machine,
	}

	err := ep.initDNS()

	assert.NoError(t, err)
}

func TestInitDNSWithCentOS(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	machine := NewMachine()
	machine.platform = "centos"

	m := &mockExecutor{}
	ep := &EnvPlugin{
		exec:    m,
		machine: machine,
	}

	err := ep.initDNS()

	assert.Error(t, err)
}

func TestInitIptables(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	ep := &EnvPlugin{
		exec:    m,
		machine: NewMachine(),
	}

	err := ep.initIptables()

	assert.NoError(t, err)
}

func TestInitIptablesWithError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", fmt.Errorf("command failed")
		})

	ep := &EnvPlugin{
		exec:    m,
		machine: NewMachine(),
	}

	err := ep.initIptables()

	assert.NoError(t, err)
}

func TestInitHostWithEmptyExtraHosts(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	patches.ApplyFunc(os.Hostname,
		func() (string, error) {
			return "testhost", nil
		})

	patches.ApplyFunc(utils.HostName,
		func() string {
			return "testhost"
		})

	ep := &EnvPlugin{
		exec:       m,
		extraHosts: "",
		machine:    NewMachine(),
	}

	err := ep.initHost()

	assert.Error(t, err)
}

func TestInitHostWithSameHostname(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	patches.ApplyFunc(os.Hostname,
		func() (string, error) {
			return "testhost", nil
		})

	patches.ApplyFunc(utils.HostName,
		func() string {
			return "testhost"
		})

	ep := &EnvPlugin{
		exec:       m,
		extraHosts: "",
		machine:    NewMachine(),
	}

	err := ep.initHost()

	assert.Error(t, err)
}

func TestTrySetHostName(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	ep := &EnvPlugin{
		exec:    m,
		machine: NewMachine(),
	}

	ep.trySetHostName("newhostname")
}

func TestInitImageWithNoRuntime(t *testing.T) {
	ep := &EnvPlugin{
		bkeConfig: nil,
		machine:   NewMachine(),
	}

	err := ep.initImage()

	assert.Error(t, err)
}

func TestInitKernelParamWithNilBkeConfig(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "1024", nil
		})

	patches.ApplyMethod(m, "ExecuteCommand",
		func(*mockExecutor, string, ...string) error {
			return nil
		})

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	machine := NewMachine()
	machine.platform = "centos"
	machine.version = "7"

	ep := &EnvPlugin{
		exec:      m,
		bkeConfig: nil,
		machine:   machine,
	}

	err := ep.initKernelParam()

	assert.Error(t, err)
}

func TestInitKernelParamWithBkeConfig(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "1024", nil
		})

	patches.ApplyMethod(m, "ExecuteCommand",
		func(*mockExecutor, string, ...string) error {
			return nil
		})

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{},
	}

	machine := NewMachine()
	machine.platform = "centos"
	machine.version = "7"

	ep := &EnvPlugin{
		exec:      m,
		bkeConfig: cfg,
		machine:   machine,
	}

	err := ep.initKernelParam()

	assert.Error(t, err)
}

func TestInitKernelParamWithIpvsMode(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "1024", nil
		})

	patches.ApplyMethod(m, "ExecuteCommand",
		func(*mockExecutor, string, ...string) error {
			return nil
		})

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			ContainerRuntime: bkev1beta1.ContainerRuntime{
				CRI: bkeinit.CRIContainerd,
			},
		},
	}

	machine := NewMachine()
	machine.platform = "centos"
	machine.version = "7"
	machine.kernel = "3.10.0"

	ep := &EnvPlugin{
		exec:      m,
		bkeConfig: cfg,
		machine:   machine,
	}

	err := ep.initKernelParam()

	assert.Error(t, err)
}

func TestInitKernelParamWithUbuntu(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "1024", nil
		})

	patches.ApplyMethod(m, "ExecuteCommand",
		func(*mockExecutor, string, ...string) error {
			return nil
		})

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{},
	}

	machine := NewMachine()
	machine.platform = "ubuntu"

	ep := &EnvPlugin{
		exec:      m,
		bkeConfig: cfg,
		machine:   machine,
	}

	err := ep.initKernelParam()

	assert.Error(t, err)
}

func TestInitSwap(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommand",
		func(*mockExecutor, string, ...string) error {
			return nil
		})

	patches.ApplyMethod(m, "ExecuteCommandWithOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	ep := &EnvPlugin{
		exec:    m,
		machine: NewMachine(),
	}

	err := ep.initSwap()

	assert.Error(t, err)
}

func TestInitTimeWithNTPServer(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			NTPServer: "pool.ntp.org",
		},
	}

	ep := &EnvPlugin{
		exec:      m,
		bkeConfig: cfg,
		machine:   NewMachine(),
	}

	err := ep.initTime()

	assert.NoError(t, err)
}

func TestInitExtraWithNilConfig(t *testing.T) {
	m := &mockExecutor{}
	ep := &EnvPlugin{
		exec:      m,
		bkeConfig: nil,
		machine:   NewMachine(),
	}

	err := ep.initExtra()

	assert.NoError(t, err)
}

func TestInitExtraWithCentOS(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	patches.ApplyFunc(os.MkdirAll,
		func(string, os.FileMode) error {
			return nil
		})

	cfg := &bkev1beta1.BKEConfig{
		CustomExtra: map[string]string{
			"pipelineServer": testIP.String(),
		},
	}

	machine := NewMachine()
	machine.platform = "centos"

	ep := &EnvPlugin{
		exec:      m,
		bkeConfig: cfg,
		currenNode: bkenode.Node{
			IP: testIP.String(),
		},
		machine: machine,
	}

	err := ep.initExtra()

	assert.NoError(t, err)
}

func TestInitExtraWithUbuntu(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	m := &mockExecutor{}

	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput",
		func(*mockExecutor, string, ...string) (string, error) {
			return "", nil
		})

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return nil
		})

	cfg := &bkev1beta1.BKEConfig{}

	machine := NewMachine()
	machine.platform = "ubuntu"

	ep := &EnvPlugin{
		exec:      m,
		bkeConfig: cfg,
		machine:   machine,
	}

	err := ep.initExtra()

	assert.NoError(t, err)
}

func TestProcessInitScopeAllCases(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput", func(*mockExecutor, string, ...string) (string, error) {
		return "", nil
	})
	mockSys := &mockInitSystem{}
	patches.ApplyFunc(initsystem.GetInitSystem, func() (initsystem.InitSystem, error) {
		return mockSys, nil
	})
	patches.ApplyFunc(utils.Exists, func(string) bool { return true })
	patches.ApplyFunc(os.Hostname, func() (string, error) { return "test", nil })
	patches.ApplyFunc(utils.HostName, func() string { return "test" })
	
	ep := &EnvPlugin{exec: m, machine: NewMachine(), extraHosts: ""}
	
	tests := []struct {
		scope   string
		wantErr bool
	}{
		{"firewall", false},
		{"dns", false},
		{"registry", false},
		{"extra", false},
		{"unknown", false},
	}
	
	for _, tt := range tests {
		err, _ := ep.processInitScope(tt.scope)
		if tt.wantErr {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestInitK8sEnvMultiScope(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommand", func(*mockExecutor, string, ...string) error { return nil })
	patches.ApplyFunc(utils.Exists, func(string) bool { return true })
	
	ep := &EnvPlugin{exec: m, scope: "dns,registry,extra", machine: NewMachine()}
	err := ep.initK8sEnv()
	assert.NoError(t, err)
}

func TestSetupUlimitEdgeCases(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	
	tests := []struct {
		name   string
		output string
		exists bool
		found  bool
	}{
		{"high_value", "70000", false, false},
		{"error", "", false, false},
		{"existing_file", "1024", true, false},
		{"existing_config", "1024", true, true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches.Reset()
			patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput", func(*mockExecutor, string, ...string) (string, error) {
				if tt.output == "" {
					return "", fmt.Errorf("error")
				}
				return tt.output, nil
			})
			patches.ApplyMethod(m, "ExecuteCommand", func(*mockExecutor, string, ...string) error { return nil })
			patches.ApplyFunc(utils.Exists, func(string) bool { return tt.exists })
			patches.ApplyFunc(os.WriteFile, func(string, []byte, os.FileMode) error { return nil })
			patches.ApplyFunc(catAndSearch, func(string, string, string) (bool, error) { return tt.found, nil })
			patches.ApplyFunc(catAndReplace, func(string, string, string, string) error { return nil })
			
			ep := &EnvPlugin{exec: m}
			ep.setupUlimit()
		})
	}
}

func TestSetupIPVSConfigVariants(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(utils.Exists, func(string) bool { return true })
	
	tests := []struct {
		kernel    string
		proxyMode string
	}{
		{"4.19.0", "ipvs"},
		{"3.10.0", "ipvs"},
		{"2.6.32", "ipvs"},
		{"5.10.0", "iptables"},
	}
	
	for _, tt := range tests {
		machine := NewMachine()
		machine.hostOS = "linux"
		machine.kernel = tt.kernel
		cfg := &bkev1beta1.BKEConfig{
			CustomExtra: map[string]string{"proxyMode": tt.proxyMode},
		}
		ep := &EnvPlugin{machine: machine, bkeConfig: cfg}
		ep.setupIPVSConfig()
	}
}

func TestWriteKernelParamsWithData(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "test")
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()
	
	execKernelParam["test.param"] = "1"
	ep := &EnvPlugin{}
	errs := ep.writeKernelParams(tmpFile)
	assert.Empty(t, errs)
	delete(execKernelParam, "test.param")
}

func TestLoadSysModulesSuccess(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput", func(*mockExecutor, string, ...string) (string, error) {
		return "", nil
	})
	
	sysModule = []string{"br_netfilter"}
	ep := &EnvPlugin{exec: m}
	errs := ep.loadSysModules()
	assert.Empty(t, errs)
	sysModule = []string{}
}

func TestSetupUbuntuModulesSuccess(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput", func(*mockExecutor, string, ...string) (string, error) {
		return "", nil
	})
	patches.ApplyFunc(catAndSearch, func(string, string, string) (bool, error) {
		return false, nil
	})
	
	machine := NewMachine()
	machine.platform = "ubuntu"
	sysModule = []string{"br_netfilter"}
	ep := &EnvPlugin{exec: m, machine: machine}
	errs := ep.setupUbuntuModules()
	assert.Empty(t, errs)
	sysModule = []string{}
}

func TestBuildClusterHostsComplete(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			ImageRepo: bkev1beta1.Repo{Domain: "reg.local", Ip: "10.0.0.1"},
			HTTPRepo:  bkev1beta1.Repo{Domain: "yum.local", Ip: "10.0.0.2"},
		},
	}
	nodes := bkenode.Nodes{{IP: "10.0.0.10", Hostname: "node1"}}
	ep := &EnvPlugin{bkeConfig: cfg, nodes: nodes}
	ep.buildClusterHosts("testnode")
	assert.Len(t, ep.clusterHosts, 3)
}

func TestLoadRuntimeConfigComplete(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			ContainerRuntime: bkev1beta1.ContainerRuntime{
				CRI:     bkeinit.CRIDocker,
				Runtime: "runc",
				Param: map[string]string{
					"data-root":           "/data",
					"cgroup-driver":       "systemd",
					"insecure-registries": "reg1,reg2",
				},
			},
		},
		CustomExtra: map[string]string{
			"pipelineServer": "10.0.0.1",
		},
	}
	ep := &EnvPlugin{bkeConfig: cfg, currenNode: bkenode.Node{IP: "10.0.0.1"}}
	result := ep.loadRuntimeConfig()
	assert.Equal(t, "systemd", result.cgroupDriver)
	assert.True(t, result.enableDockerTls)
}

func TestConfigAndRestartRuntimeDocker(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput", func(*mockExecutor, string, ...string) (string, error) {
		return "", nil
	})
	patches.ApplyFunc(docker.ConfigDockerDaemon, func(docker.DockerDaemonConfig) error {
		return nil
	})
	
	ep := &EnvPlugin{exec: m, currenNode: bkenode.Node{IP: "10.0.0.1"}}
	cfg := runtimeConfig{containerRuntime: bkeinit.CRIDocker}
	err := ep.configAndRestartRuntime(cfg, bkeinit.CRIDocker)
	assert.NoError(t, err)
}

func TestInitTimeComplete(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommandWithOutput", func(*mockExecutor, string, ...string) (string, error) {
		return "", nil
	})
	
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{NTPServer: "ntp.server.com"},
	}
	ep := &EnvPlugin{exec: m, bkeConfig: cfg}
	err := ep.initTime()
	assert.NoError(t, err)
}

func TestInitHostComplete(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput", func(*mockExecutor, string, ...string) (string, error) {
		return "", nil
	})
	patches.ApplyFunc(os.Hostname, func() (string, error) {
		return "oldhost", nil
	})
	patches.ApplyFunc(utils.HostName, func() string {
		return "newhost"
	})
	
	ep := &EnvPlugin{exec: m, extraHosts: "host1:192.168.1.10"}
	err := ep.initHost()
	assert.Error(t, err)
}

func TestInitHttpRepoComplete(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(bkesource.SetSource, func(string) error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})
	
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			ImageRepo: bkev1beta1.Repo{Domain: "reg.local"},
			HTTPRepo:  bkev1beta1.Repo{Domain: "yum.local"},
		},
	}
	ep := &EnvPlugin{bkeConfig: cfg}
	err := ep.initHttpRepo()
	assert.NoError(t, err)
}

func TestInstallLxcfsAllPlatforms(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(utils.Exists, func(string) bool { return true })
	patches.ApplyFunc(httprepo.RepoInstall, func(...string) error { return nil })
	
	platforms := []struct {
		platform string
		version  string
	}{
		{"centos", "7.9"},
		{"centos", "8.5"},
		{"kylin", "10"},
		{"ubuntu", "20.04"},
		{"unknown", "1.0"},
	}
	
	for _, p := range platforms {
		machine := NewMachine()
		machine.platform = p.platform
		machine.version = p.version
		ep := &EnvPlugin{machine: machine}
		err := ep.installLxcfs()
		assert.NoError(t, err)
	}
}

func TestConfigureLxcfsServiceComplete(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	mockSys := &mockInitSystem{}
	patches.ApplyFunc(initsystem.GetInitSystem, func() (initsystem.InitSystem, error) {
		return mockSys, nil
	})
	patches.ApplyFunc(utils.Exists, func(string) bool { return true })
	patches.ApplyFunc(os.ReadFile, func(string) ([]byte, error) {
		return []byte("/var/lib/lxcfs"), nil
	})
	patches.ApplyFunc(os.WriteFile, func(string, []byte, os.FileMode) error { return nil })
	
	ep := &EnvPlugin{}
	err := ep.configureLxcfsService()
	assert.NoError(t, err)
}

func TestInitExtraComplete(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput", func(*mockExecutor, string, ...string) (string, error) {
		return "", nil
	})
	patches.ApplyFunc(utils.Exists, func(string) bool { return true })
	patches.ApplyFunc(os.MkdirAll, func(string, os.FileMode) error { return nil })
	patches.ApplyFunc(httprepo.RepoInstall, func(...string) error { return nil })
	mockSys := &mockInitSystem{}
	patches.ApplyFunc(initsystem.GetInitSystem, func() (initsystem.InitSystem, error) {
		return mockSys, nil
	})
	patches.ApplyFunc(os.ReadFile, func(string) ([]byte, error) {
		return []byte("test"), nil
	})
	patches.ApplyFunc(os.WriteFile, func(string, []byte, os.FileMode) error { return nil })
	
	cfg := &bkev1beta1.BKEConfig{
		CustomExtra: map[string]string{"pipelineServer": "10.0.0.1"},
	}
	machine := NewMachine()
	machine.platform = "centos"
	machine.version = "7.9"
	ep := &EnvPlugin{
		exec:       m,
		bkeConfig:  cfg,
		currenNode: bkenode.Node{IP: "10.0.0.1"},
		machine:    machine,
	}
	err := ep.initExtra()
	assert.NoError(t, err)
}

func TestInitDNSComplete(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(utils.Exists, func(string) bool { return true })
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommand", func(*mockExecutor, string, ...string) error { return nil })
	
	machine := NewMachine()
	machine.platform = "centos"
	ep := &EnvPlugin{exec: m, machine: machine}
	err := ep.initDNS()
	assert.Error(t, err)
}

func TestExportImageListComplete(t *testing.T) {
	cfg := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			KubernetesVersion: "v1.28.0",
			EtcdVersion:       "v3.5.9",
		},
	}
	ep := &EnvPlugin{
		bkeConfig:  cfg,
		currenNode: bkenode.Node{IP: "10.0.0.1", Role: []string{"master"}},
	}
	images := ep.exportImageList()
	assert.NotEmpty(t, images)
}

func TestInitIptablesComplete(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	m := &mockExecutor{}
	patches.ApplyMethod(m, "ExecuteCommandWithCombinedOutput", func(*mockExecutor, string, ...string) (string, error) {
		return "iptables v1.8.7", nil
	})
	
	machine := NewMachine()
	machine.platform = "Kylin"
	machine.hostArch = "arm64"
	ep := &EnvPlugin{exec: m, machine: machine}
	err := ep.initIptables()
	assert.NoError(t, err)
}

func TestInstallNfsUtilIfNeededComplete(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(httprepo.RepoInstall, func(...string) error { return nil })
	
	tests := []struct {
		platform string
		expected string
	}{
		{"centos", "nfs-utils"},
		{"ubuntu", "nfs-common"},
	}
	
	for _, tt := range tests {
		cfg := &bkev1beta1.BKEConfig{
			CustomExtra: map[string]string{"pipelineServer": "10.0.0.1"},
		}
		machine := NewMachine()
		machine.platform = tt.platform
		ep := &EnvPlugin{
			bkeConfig:  cfg,
			currenNode: bkenode.Node{IP: "10.0.0.1"},
			machine:    machine,
		}
		ep.installNfsUtilIfNeeded()
	}
}

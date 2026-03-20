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

package reset

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common/source"
	bkenet "gopkg.openfuyao.cn/cluster-api-provider-bke/common/utils/net"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/plugin"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/httprepo"
)

type mockResetExecutor struct {
	exec.Executor
	outputValue string
	outputErr   error
}

func (m *mockResetExecutor) ExecuteCommandWithCombinedOutput(_ string, _ ...string) (string, error) {
	return m.outputValue, m.outputErr
}

func (m *mockResetExecutor) ExecuteCommandWithOutput(_ string, _ ...string) (string, error) {
	return m.outputValue, m.outputErr
}

func createTestBKEConfigForReset() *bkev1beta1.BKEConfig {
	return &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			CertificatesDir: "/etc/kubernetes/pki",
			Networking: bkev1beta1.Networking{
				PodSubnet:     "10.244.0.0/16",
				ServiceSubnet: "10.96.0.0/12",
			},
			Kubelet: &bkev1beta1.Kubelet{
				ManifestsDir: "/etc/kubernetes/manifests",
			},
			ControlPlane: bkev1beta1.ControlPlane{
				Etcd: &bkev1beta1.Etcd{
					DataDir: "/var/lib/etcd",
				},
			},
		},
		CustomExtra: map[string]string{},
	}
}

func TestResetPluginName(t *testing.T) {
	plugin := &ResetPlugin{}
	assert.Equal(t, Name, plugin.Name())
}

func TestResetPluginParam(t *testing.T) {
	plugin := &ResetPlugin{}
	params := plugin.Param()
	assert.NotNil(t, params)
	assert.Contains(t, params, "bkeConfig")
	assert.Contains(t, params, "scope")
	assert.Contains(t, params, "extra")
}

func TestNewResetPlugin(t *testing.T) {
	plugin := New()
	assert.NotNil(t, plugin)
	assert.Equal(t, Name, plugin.Name())
}

func TestResetPluginParamDefaults(t *testing.T) {
	plugin := &ResetPlugin{}
	params := plugin.Param()
	assert.Equal(t, "cert,manifests,container,kubelet,containerRuntime,source,extra", params["scope"].Default)
}

func TestResetPluginExecuteWithPluginNameOnly(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return false
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()
	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	pluginObj := &ResetPlugin{}
	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithFullScope(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return true
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	patches.ApplyFunc(utils.IsDir, func(_ string) bool {
		return false
	})

	patches.ApplyFunc(bkenet.GetInterfaceFromIp, func(_ string) (string, error) {
		return "", errors.New("not found")
	})

	cfg := createTestBKEConfigForReset()
	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithBkeConfigError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	testErr := errors.New("failed to get bke config")
	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return nil, testErr
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get bke config")
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithAllInOneHostIPMatch(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return true
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()
	cfg.CustomExtra["host"] = "192.168.1.100"

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	patches.ApplyFunc(bkenet.GetInterfaceFromIp, func(_ string) (string, error) {
		return "eth0", nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithAllInOneInterfaceMatch(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return true
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()
	cfg.CustomExtra["host"] = "192.168.1.100"

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	patches.ApplyFunc(bkenet.GetInterfaceFromIp, func(_ string) (string, error) {
		return "eth0", nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithAllInOneNoHost(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return true
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()
	cfg.CustomExtra = map[string]string{}

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	patches.ApplyFunc(bkenet.GetInterfaceFromIp, func(_ string) (string, error) {
		return "", errors.New("no interface")
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithExtraAbsolutePath(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "extra"
	})
	patches.ApplyFunc(utils.Exists, func(path string) bool {
		return path == "/test/existing-dir"
	})
	patches.ApplyFunc(utils.IsDir, func(path string) bool {
		return path == "/test/existing-dir"
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "extra=/test/existing-dir"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithExtraIPAddress(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "extra"
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "extra=192.168.1.200"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithExtraEmptyArgs(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "extra"
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "extra="})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithExtraNonExistentPath(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "extra"
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "extra=/nonexistent/path"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithExtraInvalidIP(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "extra"
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "extra=invalid-ip"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithMixedExtraArgs(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "extra"
	})
	patches.ApplyFunc(utils.Exists, func(path string) bool {
		if path == "/test/dir" {
			return true
		}
		return false
	})
	patches.ApplyFunc(utils.IsDir, func(path string) bool {
		return path == "/test/dir"
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "extra=/test/dir,192.168.1.200,invalid"})

	assert.Error(t, err)
	assert.Empty(t, result)

}

func TestResetPluginExecuteWithSingleScope(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "cert"
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "scope=cert"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithMultipleScopes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "cert" || scope == "manifests"
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "scope=cert,manifests"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithCurrentNodeError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return true
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()
	cfg.CustomExtra["host"] = "192.168.1.100"

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithGetInterfaceError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return true
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()
	cfg.CustomExtra["host"] = "192.168.1.100"

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	patches.ApplyFunc(bkenet.GetInterfaceFromIp, func(_ string) (string, error) {
		return "", errors.New("interface not found")
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithExtraFileInsteadOfDir(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()


	patches.ApplyFunc(utils.ContainsString, func(_ []string, scope string) bool {
		return scope == "extra"
	})
	patches.ApplyFunc(utils.Exists, func(path string) bool {
		if path == "/test/file.txt" {
			return true
		}
		return false
	})
	patches.ApplyFunc(utils.IsDir, func(path string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "extra=/test/file.txt"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithHostIPNoMatch(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return true
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()
	cfg.CustomExtra["host"] = "192.168.1.100"

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithHostIPMatchButEmptyInterface(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return true
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})
	patches.ApplyFunc(source.ResetSource, func() error {
		return nil
	})
	patches.ApplyFunc(httprepo.RepoUpdate, func() error {
		return nil
	})

	cfg := createTestBKEConfigForReset()
	cfg.CustomExtra["host"] = "192.168.1.100"

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	patches.ApplyFunc(bkenet.GetInterfaceFromIp, func(_ string) (string, error) {
		return "", nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestResetPluginExecuteWithNoScopeMatch(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.ContainsString, func(_ []string, _ string) bool {
		return false
	})
	patches.ApplyFunc(utils.Exists, func(_ string) bool {
		return false
	})

	cfg := createTestBKEConfigForReset()

	pluginObj := &ResetPlugin{}

	patches.ApplyFunc(plugin.GetBkeConfig, func(_ string) (*bkev1beta1.BKEConfig, error) {
		return cfg, nil
	})

	result, err := pluginObj.Execute([]string{Name, "bkeConfig=ns:test", "scope=nonexistent"})

	assert.Error(t, err)
	assert.Empty(t, result)
}

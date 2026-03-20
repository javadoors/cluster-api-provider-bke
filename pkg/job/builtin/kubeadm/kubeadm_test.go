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

package kubeadm

import (
	"context"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkenetutil "gopkg.openfuyao.cn/cluster-api-provider-bke/common/utils/net"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/plugin"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/mfutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/pkiutil"
)

const (
	numOne        = 1
	numTwo        = 2
	numThree      = 3
	numFive       = 5
	numTen        = 10
	numSixty      = 60
	numOneHundred = 100

	testHostName    = "test-node"
	testHostIP      = "192.168.1.10"
	testClusterName = "test-cluster"
	testNamespace   = "test-namespace"
	testVersion     = "v1.26.0"
	testEtcdVersion = "3.6.4"
)

type mockExecutor struct {
	exec.Executor
}

func (m *mockExecutor) ExecuteCommand(_ string, _ ...string) error {
	return nil
}

func (m *mockExecutor) ExecuteCommandWithEnv(_ []string, _ string, _ ...string) error {
	return nil
}

func (m *mockExecutor) ExecuteCommandWithOutput(_ string, _ ...string) (string, error) {
	return "", nil
}

func (m *mockExecutor) ExecuteCommandWithCombinedOutput(_ string, _ ...string) (string, error) {
	return "", nil
}

func (m *mockExecutor) ExecuteCommandWithOutputFile(_ string, _ string, _ ...string) (string, error) {
	return "", nil
}

func (m *mockExecutor) ExecuteCommandWithOutputFileTimeout(_ time.Duration, _ string, _ string, _ ...string) (string, error) {
	return "", nil
}

func (m *mockExecutor) ExecuteCommandWithTimeout(_ time.Duration, _ string, _ ...string) (string, error) {
	return "", nil
}

func (m *mockExecutor) ExecuteCommandResidentBinary(_ time.Duration, _ string, _ ...string) error {
	return nil
}

type fakeClient struct {
	client.Client
	shouldUpdate bool
	createCount  int
}

func (m *fakeClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return nil
}

func (m *fakeClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return nil
}

func (m *fakeClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	m.createCount++
	if m.createCount > 1 {
		return errors.New("already exists")
	}
	return nil
}

func (m *fakeClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if m.shouldUpdate {
		return nil
	}
	return errors.New("update error")
}

func (m *fakeClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return nil
}

func (m *fakeClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return nil
}

func (m *fakeClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return nil
}

func (m *fakeClient) Status() client.StatusWriter {
	return &mockStatusWriter{}
}

func (m *fakeClient) Scheme() *runtime.Scheme {
	return nil
}

type mockStatusWriter struct{}

func (m *mockStatusWriter) Create(ctx context.Context, obj client.Object, sub client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (m *mockStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return nil
}

func (m *mockStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func TestName(t *testing.T) {
	kp := &KubeadmPlugin{}
	assert.Equal(t, Name, kp.Name())
}

func TestNew(t *testing.T) {
	mockExec := &mockExecutor{}
	p := New(mockExec, nil)

	kp, ok := p.(*KubeadmPlugin)
	assert.True(t, ok)
	assert.NotNil(t, kp)
	assert.Equal(t, mockExec, kp.exec)
	assert.Nil(t, kp.k8sClient)
	assert.NotNil(t, kp.boot)
	assert.Equal(t, "", kp.controlPlaneEndpoint)
}

func TestNewWithClient(t *testing.T) {
	mockExec := &mockExecutor{}
	mockClient := &fakeClient{}

	p := New(mockExec, mockClient)

	kp := p.(*KubeadmPlugin)
	assert.Equal(t, mockExec, kp.exec)
	assert.Equal(t, mockClient, kp.k8sClient)
}

func TestParam(t *testing.T) {
	kp := &KubeadmPlugin{}
	params := kp.Param()

	assert.Contains(t, params, "phase")
	assert.Contains(t, params, "bkeConfig")
	assert.Contains(t, params, "backUpEtcd")
	assert.Contains(t, params, "clusterType")
	assert.Contains(t, params, "etcdVersion")
}

func TestParamRequiredFields(t *testing.T) {
	kp := &KubeadmPlugin{}
	params := kp.Param()

	assert.True(t, params["phase"].Required)
	assert.False(t, params["bkeConfig"].Required)
	assert.False(t, params["backUpEtcd"].Required)
	assert.False(t, params["clusterType"].Required)
	assert.False(t, params["etcdVersion"].Required)
}

func TestParamDefaultValues(t *testing.T) {
	kp := &KubeadmPlugin{}
	params := kp.Param()

	assert.Equal(t, "initControlPlane", params["phase"].Default)
	assert.Equal(t, "false", params["backUpEtcd"].Default)
	assert.Equal(t, "bke", params["clusterType"].Default)
	assert.Equal(t, "", params["etcdVersion"].Default)
}

func TestExecuteWithParseCommandsError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec: mockExec,
	}

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return nil, errors.New("parse error")
		})

	_, err := kp.Execute([]string{Name})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")
}

func TestExecuteWithUnknownPhase(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec: mockExec,
	}

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"phase": "unknownPhase",
			}, nil
		})

	_, err := kp.Execute([]string{Name})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}

func TestExecuteInitControlPlane(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      &mfutil.BootScope{},
		isManager: false,
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteInitControlPlaneWithManager(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:           mockExec,
		boot:           &mfutil.BootScope{},
		isManager:      true,
		GableNameSpace: testNamespace,
		clusterName:    testClusterName,
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteJoinControlPlane(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      &mfutil.BootScope{},
		isManager: false,
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteJoinWorker(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      &mfutil.BootScope{},
		isManager: false,
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteUpgradeControlPlane(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      &mfutil.BootScope{},
		isManager: false,
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestKubeadmPluginFields(t *testing.T) {
	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		k8sClient:            nil,
		localK8sClient:       nil,
		exec:                 mockExec,
		boot:                 &mfutil.BootScope{},
		isManager:            true,
		clusterName:          testClusterName,
		controlPlaneEndpoint: "192.168.1.100:6443",
		GableNameSpace:       testNamespace,
	}

	assert.Equal(t, mockExec, kp.exec)
	assert.True(t, kp.isManager)
	assert.Equal(t, testClusterName, kp.clusterName)
	assert.Equal(t, "192.168.1.100:6443", kp.controlPlaneEndpoint)
	assert.Equal(t, testNamespace, kp.GableNameSpace)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "Kubeadm", Name)
	assert.Equal(t, 500*time.Millisecond, PollImmeInternal)
	assert.Equal(t, 3*time.Minute, PollImmeTimeout)
}

func TestExecuteUpgradeControlPlaneWithBocloud(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}

	kp := &KubeadmPlugin{
		exec: mockExec,
		boot: createTestBootScope(),
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetBKEConfigWithControlPlaneEndpoint(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      &mfutil.BootScope{},
		isManager: false,
	}

	testConfig := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			KubernetesVersion: testVersion,
		},
	}

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"phase":     utils.InitControlPlane,
				"bkeConfig": testNamespace + ":" + testClusterName,
			}, nil
		})

	patches.ApplyFunc(plugin.GetBKECluster,
		func(string) (*bkev1beta1.BKECluster, error) {
			return &bkev1beta1.BKECluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testNamespace,
				},
				Spec: bkev1beta1.BKEClusterSpec{
					ClusterConfig: testConfig,
					ControlPlaneEndpoint: bkev1beta1.APIEndpoint{
						Host: "192.168.1.100",
						Port: 6443,
					},
				},
			}, nil
		})

	patches.ApplyFunc(plugin.GetBkeConfigFromBkeCluster,
		func(*bkev1beta1.BKECluster) (*bkev1beta1.BKEConfig, error) {
			return testConfig, nil
		})

	patches.ApplyFunc(plugin.GetClusterData,
		func(string) (*plugin.ClusterData, error) {
			return &plugin.ClusterData{
				Nodes: bkenode.Nodes{{IP: testHostIP, Role: []string{"master"}}},
			}, nil
		})

	patches.ApplyFunc(bkenetutil.GetAllInterfaceIP, func() ([]string, error) {
		return []string{testHostIP}, nil
	})

	patches.ApplyFunc(utils.HostName, func() string {
		return testHostName
	})

	err := kp.getBKEConfig(testNamespace + ":" + testClusterName)

	assert.NoError(t, err)
	assert.Equal(t, "192.168.1.100", kp.controlPlaneEndpoint)
}

func TestGetBKEConfigWithoutManagerAddon(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      &mfutil.BootScope{},
		isManager: false,
	}

	testConfig := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			KubernetesVersion: testVersion,
		},
	}

	patches.ApplyFunc(plugin.GetBKECluster,
		func(string) (*bkev1beta1.BKECluster, error) {
			return &bkev1beta1.BKECluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testNamespace,
				},
				Spec: bkev1beta1.BKEClusterSpec{
					ClusterConfig: testConfig,
				},
			}, nil
		})

	patches.ApplyFunc(plugin.GetBkeConfigFromBkeCluster,
		func(*bkev1beta1.BKECluster) (*bkev1beta1.BKEConfig, error) {
			return testConfig, nil
		})

	patches.ApplyFunc(plugin.GetClusterData,
		func(string) (*plugin.ClusterData, error) {
			return &plugin.ClusterData{
				Nodes: bkenode.Nodes{{IP: testHostIP, Role: []string{"master"}}},
			}, nil
		})

	patches.ApplyFunc(bkenetutil.GetAllInterfaceIP, func() ([]string, error) {
		return []string{testHostIP}, nil
	})

	patches.ApplyFunc(utils.HostName, func() string {
		return testHostName
	})

	err := kp.getBKEConfig(testNamespace + ":" + testClusterName)

	assert.NoError(t, err)
	assert.False(t, kp.isManager)
}

func TestExecuteWithBkeConfigError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec: mockExec,
	}

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"phase":     utils.InitControlPlane,
				"bkeConfig": "invalid:config",
			}, nil
		})

	patches.ApplyFunc(plugin.GetBKECluster,
		func(string) (*bkev1beta1.BKECluster, error) {
			return nil, errors.New("failed to get BKECluster")
		})

	_, err := kp.Execute([]string{Name})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get BKECluster")
}

func TestExecuteWithGetBkeConfigFromBkeClusterError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec: mockExec,
	}

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"phase":     utils.InitControlPlane,
				"bkeConfig": testNamespace + ":" + testClusterName,
			}, nil
		})

	patches.ApplyFunc(plugin.GetBKECluster,
		func(string) (*bkev1beta1.BKECluster, error) {
			return &bkev1beta1.BKECluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testNamespace,
				},
			}, nil
		})

	patches.ApplyFunc(plugin.GetBkeConfigFromBkeCluster,
		func(*bkev1beta1.BKECluster) (*bkev1beta1.BKEConfig, error) {
			return nil, errors.New("failed to get BKEConfig")
		})

	_, err := kp.Execute([]string{Name})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get BKEConfig")
}

func TestExecuteUpgradeControlPlaneWithBackupEtcd(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}

	kp := &KubeadmPlugin{
		exec:           mockExec,
		boot:           createTestBootScope(),
		GableNameSpace: testNamespace,
		clusterName:    testClusterName,
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteUpgradeControlPlaneWithClientSetError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec: mockExec,
		boot: &mfutil.BootScope{},
	}

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"phase":       utils.UpgradeControlPlane,
				"backUpEtcd":  "true",
				"clusterType": "bke",
			}, nil
		})

	_, err := kp.Execute([]string{Name})

	assert.Error(t, err)
}

func TestExecuteUpgradeEtcdWithBackupEtcd(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}

	kp := &KubeadmPlugin{
		exec:           mockExec,
		boot:           createTestBootScope(),
		GableNameSpace: testNamespace,
		clusterName:    testClusterName,
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteUpgradeEtcdWithClientSetError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec: mockExec,
		boot: &mfutil.BootScope{},
	}

	patches.ApplyFunc(plugin.ParseCommands,
		func(plugin.Plugin, []string) (map[string]string, error) {
			return map[string]string{
				"phase":       utils.UpgradeEtcd,
				"backUpEtcd":  "false",
				"clusterType": "bke",
			}, nil
		})

	_, err := kp.Execute([]string{Name})

	assert.Error(t, err)
}

func TestGetBKEConfigWithEmptyControlPlaneEndpoint(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      &mfutil.BootScope{},
		isManager: false,
	}

	testConfig := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			KubernetesVersion: testVersion,
		},
	}

	patches.ApplyFunc(plugin.GetBKECluster,
		func(string) (*bkev1beta1.BKECluster, error) {
			return &bkev1beta1.BKECluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testNamespace,
				},
				Spec: bkev1beta1.BKEClusterSpec{
					ClusterConfig: testConfig,
					ControlPlaneEndpoint: bkev1beta1.APIEndpoint{
						Host: "",
						Port: 0,
					},
				},
			}, nil
		})

	patches.ApplyFunc(plugin.GetBkeConfigFromBkeCluster,
		func(*bkev1beta1.BKECluster) (*bkev1beta1.BKEConfig, error) {
			return testConfig, nil
		})

	patches.ApplyFunc(plugin.GetClusterData,
		func(string) (*plugin.ClusterData, error) {
			return &plugin.ClusterData{
				Nodes: bkenode.Nodes{{IP: testHostIP, Role: []string{"master"}}},
			}, nil
		})

	patches.ApplyFunc(bkenetutil.GetAllInterfaceIP, func() ([]string, error) {
		return []string{testHostIP}, nil
	})

	patches.ApplyFunc(utils.HostName, func() string {
		return testHostName
	})

	err := kp.getBKEConfig(testNamespace + ":" + testClusterName)

	assert.NoError(t, err)
	assert.Equal(t, "", kp.controlPlaneEndpoint)
}

func TestExecuteUpgradeWorker(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      createTestBootScope(),
		isManager: false,
	}

	patches.ApplyMethod(kp, "Execute", func(_ *KubeadmPlugin, _ []string) ([]string, error) {
		return nil, nil
	})

	result, err := kp.Execute([]string{Name})

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetBKEConfigCurrentNodeError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockExec := &mockExecutor{}
	kp := &KubeadmPlugin{
		exec:      mockExec,
		boot:      &mfutil.BootScope{},
		isManager: false,
	}

	testConfig := &bkev1beta1.BKEConfig{
		Cluster: bkev1beta1.Cluster{
			KubernetesVersion: testVersion,
		},
	}

	patches.ApplyFunc(plugin.GetBKECluster,
		func(string) (*bkev1beta1.BKECluster, error) {
			return &bkev1beta1.BKECluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testNamespace,
				},
				Spec: bkev1beta1.BKEClusterSpec{
					ClusterConfig: testConfig,
				},
			}, nil
		})

	patches.ApplyFunc(plugin.GetBkeConfigFromBkeCluster,
		func(*bkev1beta1.BKECluster) (*bkev1beta1.BKEConfig, error) {
			return testConfig, nil
		})

	patches.ApplyFunc(plugin.GetClusterData,
		func(string) (*plugin.ClusterData, error) {
			return nil, errors.New("current node error")
		})

	err := kp.getBKEConfig(testNamespace + ":" + testClusterName)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "current node error")
}

func createTestBootScope() *mfutil.BootScope {
	return &mfutil.BootScope{
		BkeConfig: &bkev1beta1.BKEConfig{
			Cluster: bkev1beta1.Cluster{
				KubernetesVersion: testVersion,
			},
		},
		ClusterName: testClusterName,
		HostName:    testHostName,
		HostIP:      testHostIP,
		CurrentNode: bkenode.Node{
			IP:   testHostIP,
			Role: []string{"master"},
		},
		Extra: map[string]interface{}{
			"Init":                 false,
			"gpuEnable":            "false",
			"KubernetesDir":        pkiutil.KubernetesDir,
			"mccs":                 []string{testNamespace, testClusterName},
			"upgradeWithOpenFuyao": false,
		},
	}
}

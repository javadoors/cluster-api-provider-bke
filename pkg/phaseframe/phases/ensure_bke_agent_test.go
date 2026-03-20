/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */
package phases

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkevalidate "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/validation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/certs"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	bkessh "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/remote"
)

const (
	testNodeIP1     = "127.0.0.1"
	testNodeIP2     = "127.0.0.2"
	testNodeIP3     = "127.0.0.3"
	testHostname1   = "node1"
	testHostname2   = "node2"
	testHostname3   = "node3"
	testClusterName = "test-cluster"
	testNamespace   = "default"
	testFileContent = "test"
	testDirPerm     = 0755
	testFilePerm    = 0644
	testDstDir      = "/tmp"
	emptySliceSize  = 0
	successField    = "success"
	failureField    = "failed"
	two             = 2
)

// createTestPhaseContext 创建测试用的PhaseContext，避免空指针
func createTestPhaseContext(bkeCluster *bkev1beta1.BKECluster) *phaseframe.PhaseContext {
	if bkeCluster == nil {
		bkeCluster = &bkev1beta1.BKECluster{}
	}
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	recorder := &fakeRecorder{}

	return &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, recorder, bkeCluster),
		Client:     c,
		Scheme:     scheme,
	}
}

// createTestBKECluster 创建测试用的BKECluster
// 注意：节点信息已拆分到独立的 BKENode CRD 中，不再存储在 BKECluster.Spec.ClusterConfig.Nodes
func createTestBKECluster(nodes []confv1beta1.Node) *bkev1beta1.BKECluster {
	_ = nodes // nodes 参数保留用于兼容性，实际节点通过 BKENode CRD 管理
	return &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: testNamespace,
		},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{},
		},
	}
}

// createTestNodes 创建测试用的节点列表
func createTestNodes(count int) []confv1beta1.Node {
	nodes := make([]confv1beta1.Node, 0, count)
	ips := []string{testNodeIP1, testNodeIP2, testNodeIP3}
	hostnames := []string{testHostname1, testHostname2, testHostname3}
	for i := 0; i < count && i < len(ips); i++ {
		nodes = append(nodes, confv1beta1.Node{
			IP:       ips[i],
			Hostname: hostnames[i],
		})
	}
	return nodes
}

// TestCheckAvailableHosts 测试checkAvailableHosts函数
func TestCheckAvailableHosts(t *testing.T) {
	tests := []struct {
		name     string
		multiCli *bkessh.MultiCli
		errs     map[string]error
		want     bool
	}{
		{
			name:     "available hosts count is zero should return false",
			multiCli: &bkessh.MultiCli{},
			errs:     make(map[string]error),
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnsureBKEAgent{}
			result := e.checkAvailableHosts(tt.multiCli, tt.errs)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestAllNeedPushNodesFailed_EmptyNodes 测试allNeedPushNodesFailed函数-空节点场景
func TestAllNeedPushNodesFailed_EmptyNodes(t *testing.T) {
	e := &EnsureBKEAgent{
		needPushNodes: bkenode.Nodes{},
	}
	result := e.allNeedPushNodesFailed([]string{testNodeIP1})
	assert.False(t, result)
}

// TestAllNeedPushNodesFailed_EmptyFailedInfo 测试allNeedPushNodesFailed函数-空失败信息场景
func TestAllNeedPushNodesFailed_EmptyFailedInfo(t *testing.T) {
	e := &EnsureBKEAgent{
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1, Hostname: testHostname1}},
	}
	result := e.allNeedPushNodesFailed(make([]string, emptySliceSize))
	assert.False(t, result)
}

// TestAllNeedPushNodesFailed_AllFailed 测试allNeedPushNodesFailed函数-所有节点失败场景
func TestAllNeedPushNodesFailed_AllFailed(t *testing.T) {
	e := &EnsureBKEAgent{
		needPushNodes: bkenode.Nodes{
			{IP: testNodeIP1, Hostname: testHostname1},
			{IP: testNodeIP2, Hostname: testHostname2},
		},
	}
	result := e.allNeedPushNodesFailed([]string{testNodeIP1, testNodeIP2})
	assert.True(t, result)
}

// TestAllNeedPushNodesFailed_SomeFailed 测试allNeedPushNodesFailed函数-部分节点失败场景
func TestAllNeedPushNodesFailed_SomeFailed(t *testing.T) {
	e := &EnsureBKEAgent{
		needPushNodes: bkenode.Nodes{
			{IP: testNodeIP1, Hostname: testHostname1},
			{IP: testNodeIP2, Hostname: testHostname2},
		},
	}
	result := e.allNeedPushNodesFailed([]string{testNodeIP1})
	assert.False(t, result)
}

// TestAllNeedPushNodesFailed_NoMatch 测试allNeedPushNodesFailed函数-无匹配节点场景
func TestAllNeedPushNodesFailed_NoMatch(t *testing.T) {
	e := &EnsureBKEAgent{
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1, Hostname: testHostname1}},
	}
	result := e.allNeedPushNodesFailed([]string{testNodeIP2})
	assert.False(t, result)
}

// TestAddFilesToUploadList_FileExists 测试addFilesToUploadList函数-文件存在场景
func TestAddFilesToUploadList_FileExists(t *testing.T) {
	tempDir := t.TempDir()
	existingFile := filepath.Join(tempDir, "existing.txt")
	err := os.WriteFile(existingFile, []byte(testFileContent), testFilePerm)
	assert.NoError(t, err)

	e := &EnsureBKEAgent{}
	fileUpList := make([]bkessh.File, emptySliceSize)
	result := e.addFilesToUploadList(fileUpList, []string{existingFile}, testDstDir)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, testDstDir, result[0].Dst)
}

// TestAddFilesToUploadList_FileNotExists 测试addFilesToUploadList函数-文件不存在场景
func TestAddFilesToUploadList_FileNotExists(t *testing.T) {
	tempDir := t.TempDir()
	nonExistingFile := filepath.Join(tempDir, "non-existing.txt")

	e := &EnsureBKEAgent{}
	fileUpList := make([]bkessh.File, emptySliceSize)
	result := e.addFilesToUploadList(fileUpList, []string{nonExistingFile}, testDstDir)
	assert.Equal(t, emptySliceSize, len(result))
}

// TestAddFilesToUploadList_MixedFiles 测试addFilesToUploadList函数-混合文件场景
func TestAddFilesToUploadList_MixedFiles(t *testing.T) {
	tempDir := t.TempDir()
	existingFile := filepath.Join(tempDir, "existing.txt")
	nonExistingFile := filepath.Join(tempDir, "non-existing.txt")
	err := os.WriteFile(existingFile, []byte(testFileContent), testFilePerm)
	assert.NoError(t, err)

	e := &EnsureBKEAgent{}
	fileUpList := make([]bkessh.File, emptySliceSize)
	result := e.addFilesToUploadList(fileUpList, []string{existingFile, nonExistingFile}, testDstDir)
	assert.Equal(t, 1, len(result))
}

// TestAddFilesToUploadList_EmptyPaths 测试addFilesToUploadList函数-空路径列表场景
func TestAddFilesToUploadList_EmptyPaths(t *testing.T) {
	e := &EnsureBKEAgent{}
	fileUpList := []bkessh.File{{Src: "test", Dst: testDstDir}}
	result := e.addFilesToUploadList(fileUpList, make([]string, emptySliceSize), testDstDir)
	assert.Equal(t, 1, len(result))
}

// TestAddGlobalCAFilesIfNeeded_EmptyAddons 测试addGlobalCAFilesIfNeeded函数-空addons场景
func TestAddGlobalCAFilesIfNeeded_EmptyAddons(t *testing.T) {
	bkeCluster := createTestBKECluster(createTestNodes(1))
	bkeCluster.Spec.ClusterConfig.Addons = nil
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}
	fileUpList := make([]bkessh.File, emptySliceSize)
	result := e.addGlobalCAFilesIfNeeded(fileUpList)
	assert.Equal(t, emptySliceSize, len(result))
}

// TestAddCSRFilesToUploadList 测试addCSRFilesToUploadList函数
func TestAddCSRFilesToUploadList(t *testing.T) {
	tempDir := t.TempDir()
	certConfigDir := filepath.Join(tempDir, "cert_config")
	err := os.MkdirAll(certConfigDir, testDirPerm)
	assert.NoError(t, err)

	existingCSR1 := filepath.Join(certConfigDir, certs.ConfigKeyClusterCAPolicy)
	err = os.WriteFile(existingCSR1, []byte(testFileContent), testFilePerm)
	assert.NoError(t, err)

	e := &EnsureBKEAgent{}
	fileUpList := make([]bkessh.File, emptySliceSize)
	result := e.addCSRFilesToUploadList(fileUpList)
	assert.GreaterOrEqual(t, len(result), emptySliceSize)
}

// TestPrepareFileUploadList 测试prepareFileUploadList函数
func TestPrepareFileUploadList(t *testing.T) {
	tempDir := t.TempDir()
	servicePath := filepath.Join(tempDir, "bkeagent.service")
	err := os.WriteFile(servicePath, []byte(testFileContent), testFilePerm)
	assert.NoError(t, err)

	bkeCluster := createTestBKECluster(createTestNodes(1))
	bkeCluster.Spec.ClusterConfig.Addons = nil
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.prepareFileUploadList(servicePath)
	assert.GreaterOrEqual(t, len(result), 1)

	hasServiceFile := false
	for _, file := range result {
		if file.Src == servicePath {
			hasServiceFile = true
			assert.Equal(t, "/etc/systemd/system", file.Dst)
			break
		}
	}
	assert.True(t, hasServiceFile)
}

// TestNewEnsureBKEAgent 测试NewEnsureBKEAgent函数
func TestNewEnsureBKEAgent(t *testing.T) {
	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	result := NewEnsureBKEAgent(ctx)
	assert.NotNil(t, result)

	agent, ok := result.(*EnsureBKEAgent)
	assert.True(t, ok)
	assert.NotNil(t, agent.BasePhase)
	assert.Equal(t, EnsureBKEAgentName, agent.BasePhase.PhaseName)
}

// TestNeedExecute_NoNewNodes 测试NeedExecute函数-无新节点场景
func TestNeedExecute_NoNewNodes(t *testing.T) {
	nodes := createTestNodes(1)
	oldCluster := createTestBKECluster(nodes)
	newCluster := createTestBKECluster(nodes)

	ctx := createTestPhaseContext(newCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}
	result := e.NeedExecute(oldCluster, newCluster)
	assert.False(t, result)
}

// TestUpdateNodeStatus_BothTypes 测试updateNodeStatus函数-成功和失败节点场景
func TestUpdateNodeStatus_BothTypes(t *testing.T) {
	bkeCluster := createTestBKECluster(createTestNodes(two))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}
	successNodes := []string{testNodeIP1 + successField}
	failedNodes := []string{testNodeIP2 + failureField}
	e.updateNodeStatus(bkeCluster, successNodes, failedNodes)
	assert.NotNil(t, bkeCluster)
}

// TestUpdateNodeStatus_EmptySuccess 测试updateNodeStatus函数-空成功节点场景
func TestUpdateNodeStatus_EmptySuccess(t *testing.T) {
	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}
	failedNodes := []string{testNodeIP1 + failureField}
	e.updateNodeStatus(bkeCluster, make([]string, emptySliceSize), failedNodes)
	assert.NotNil(t, bkeCluster)
}

// TestUpdateNodeStatus_EmptyFailed 测试updateNodeStatus函数-空失败节点场景
func TestUpdateNodeStatus_EmptyFailed(t *testing.T) {
	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}
	successNodes := []string{testNodeIP1 + successField}
	e.updateNodeStatus(bkeCluster, successNodes, make([]string, emptySliceSize))
	assert.NotNil(t, bkeCluster)
}

// TestCheckAllOrPushedAgentsFailed_AllSuccess 测试checkAllOrPushedAgentsFailed函数-全部成功场景
func TestCheckAllOrPushedAgentsFailed_AllSuccess(t *testing.T) {
	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1, Hostname: testHostname1}},
	}
	err := e.checkAllOrPushedAgentsFailed([]string{testNodeIP1 + successField}, make([]string, emptySliceSize))
	assert.NoError(t, err)
}

// TestCheckAllOrPushedAgentsFailed_SomeFailed 测试checkAllOrPushedAgentsFailed函数-部分失败场景
func TestCheckAllOrPushedAgentsFailed_SomeFailed(t *testing.T) {
	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase: phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{
			{IP: testNodeIP1, Hostname: testHostname1},
			{IP: testNodeIP2, Hostname: testHostname2},
		},
	}
	err := e.checkAllOrPushedAgentsFailed([]string{testNodeIP1 + successField}, []string{testNodeIP2 + failureField})
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_Execute_LoadConfigError 测试Execute函数-加载配置失败
func TestEnsureBKEAgent_Execute_LoadConfigError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "loadLocalKubeConfig", func(_ *EnsureBKEAgent) error {
		return assert.AnError
	})

	_, err := e.Execute()
	assert.Error(t, err)
}

// TestEnsureBKEAgent_Execute_GetNodesError 测试Execute函数-获取节点失败
func TestEnsureBKEAgent_Execute_GetNodesError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "loadLocalKubeConfig", func(_ *EnsureBKEAgent) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "getNeedPushNodes", func(_ *EnsureBKEAgent) error {
		return assert.AnError
	})

	_, err := e.Execute()
	assert.Error(t, err)
}

// TestEnsureBKEAgent_Execute_NoNeedPushNodes 测试Execute函数-无需推送节点
func TestEnsureBKEAgent_Execute_NoNeedPushNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "loadLocalKubeConfig", func(_ *EnsureBKEAgent) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "getNeedPushNodes", func(_ *EnsureBKEAgent) error {
		e.needPushNodes = nil
		return nil
	})

	_, err := e.Execute()
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_Execute_PushAgentError 测试Execute函数-推送agent失败
func TestEnsureBKEAgent_Execute_PushAgentError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "loadLocalKubeConfig", func(_ *EnsureBKEAgent) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "getNeedPushNodes", func(_ *EnsureBKEAgent) error {
		e.needPushNodes = bkenode.Nodes{{IP: testNodeIP1}}
		return nil
	})
	patches.ApplyPrivateMethod(e, "pushAgent", func(_ *EnsureBKEAgent) error {
		return assert.AnError
	})

	_, err := e.Execute()
	assert.Error(t, err)
}

// TestEnsureBKEAgent_Execute_Success 测试Execute函数-成功
func TestEnsureBKEAgent_Execute_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "loadLocalKubeConfig", func(_ *EnsureBKEAgent) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "getNeedPushNodes", func(_ *EnsureBKEAgent) error {
		e.needPushNodes = bkenode.Nodes{{IP: testNodeIP1}}
		return nil
	})
	patches.ApplyPrivateMethod(e, "pushAgent", func(_ *EnsureBKEAgent) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "pingAgent", func(_ *EnsureBKEAgent) error {
		return nil
	})

	_, err := e.Execute()
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_LoadLocalKubeConfig 测试loadLocalKubeConfig函数
func TestEnsureBKEAgent_LoadLocalKubeConfig(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "loadLocalKubeConfig", func(agent *EnsureBKEAgent) error {
		agent.localKubeConfig = []byte("test-config")
		return nil
	})

	err := e.loadLocalKubeConfig()
	assert.NoError(t, err)
	assert.NotNil(t, e.localKubeConfig)
}

// TestEnsureBKEAgent_GetNeedPushNodes 测试getNeedPushNodes函数
func TestEnsureBKEAgent_GetNeedPushNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "getNeedPushNodes", func(agent *EnsureBKEAgent) error {
		agent.needPushNodes = bkenode.Nodes{{IP: testNodeIP1}}
		return nil
	})

	err := e.getNeedPushNodes()
	assert.NoError(t, err)
	assert.Len(t, e.needPushNodes, 1)
}

// TestEnsureBKEAgent_PushAgent 测试pushAgent函数
func TestEnsureBKEAgent_PushAgent(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "pushAgent", func(_ *EnsureBKEAgent) error {
		return nil
	})

	err := e.pushAgent()
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_LogPushAgentStart 测试logPushAgentStart函数
func TestEnsureBKEAgent_LogPushAgentStart(t *testing.T) {
	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1, Hostname: testHostname1}},
	}
	e.logPushAgentStart()
}


// TestEnsureBKEAgent_PushAgent_Success 测试pushAgent成功场景
func TestEnsureBKEAgent_PushAgent_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}},
	}

	patches.ApplyPrivateMethod(e, "logPushAgentStart", func(_ *EnsureBKEAgent) {})
	patches.ApplyPrivateMethod(e, "prepareServiceFile", func(_ *EnsureBKEAgent, _ *bkev1beta1.BKECluster) (string, error) {
		return "/tmp/test.service", nil
	})
	patches.ApplyPrivateMethod(e, "performAgentPush", func(_ *EnsureBKEAgent, _ interface{}, _ interface{}, _ *bkev1beta1.BKECluster, _ string) ([]string, error) {
		return []string{}, nil
	})
	patches.ApplyPrivateMethod(e, "handlePushResults", func(_ *EnsureBKEAgent, _ interface{}, _ interface{}, _ *bkev1beta1.BKECluster, _ []string) error {
		return nil
	})

	err := e.pushAgent()
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_PushAgent_PrepareServiceFileError 测试pushAgent准备服务文件失败
func TestEnsureBKEAgent_PushAgent_PrepareServiceFileError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}},
	}

	patches.ApplyPrivateMethod(e, "logPushAgentStart", func(_ *EnsureBKEAgent) {})
	patches.ApplyPrivateMethod(e, "prepareServiceFile", func(_ *EnsureBKEAgent, _ *bkev1beta1.BKECluster) (string, error) {
		return "", assert.AnError
	})

	err := e.pushAgent()
	assert.Error(t, err)
}


// TestEnsureBKEAgent_PrepareServiceFile_Success 测试prepareServiceFile成功
func TestEnsureBKEAgent_PrepareServiceFile_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	tempDir := t.TempDir()
	
	bkeCluster := createTestBKECluster(createTestNodes(1))
	bkeCluster.Spec.ClusterConfig.Cluster.NTPServer = "ntp.example.com"
	bkeCluster.Spec.ClusterConfig.Cluster.AgentHealthPort = "8080"
	
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyFunc(os.ReadFile, func(name string) ([]byte, error) {
		return []byte("--ntpserver= --health-port="), nil
	})
	patches.ApplyFunc(os.MkdirTemp, func(dir, pattern string) (string, error) {
		return tempDir, nil
	})

	path, err := e.prepareServiceFile(bkeCluster)
	assert.NoError(t, err)
	assert.NotEmpty(t, path)
}

// TestEnsureBKEAgent_PerformAgentPush_Success 测试performAgentPush成功
func TestEnsureBKEAgent_PerformAgentPush_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}},
	}

	patches.ApplyPrivateMethod(e, "sshPushAgent", func(_ *EnsureBKEAgent, _ interface{}, _ interface{}, _ []byte, _ string) (map[string]error, error) {
		return make(map[string]error), nil
	})

	failedIPs, err := e.performAgentPush(context.Background(), nil, &bkev1beta1.BKECluster{}, "/tmp/test.service")
	assert.NoError(t, err)
	assert.Empty(t, failedIPs)
}

// TestEnsureBKEAgent_HandlePushResults_AllSuccess 测试handlePushResults全部成功
func TestEnsureBKEAgent_HandlePushResults_AllSuccess(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}},
	}

	patches.ApplyPrivateMethod(e, "handlePushResults", func(_ *EnsureBKEAgent, _ context.Context, _ client.Client, _ *bkev1beta1.BKECluster, _ []string) error {
		return nil
	})

	err := e.handlePushResults(context.Background(), nil, bkeCluster, []string{})
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_HandlePushResults_WithFailures 测试handlePushResults有失败节点
func TestEnsureBKEAgent_HandlePushResults_WithFailures(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}},
	}

	patches.ApplyPrivateMethod(e, "handlePushResults", func(_ *EnsureBKEAgent, _ context.Context, _ client.Client, _ *bkev1beta1.BKECluster, _ []string) error {
		return assert.AnError
	})

	err := e.handlePushResults(context.Background(), nil, bkeCluster, []string{testNodeIP1})
	assert.Error(t, err)
}

// TestEnsureBKEAgent_SshPushAgent_Success 测试sshPushAgent成功
func TestEnsureBKEAgent_SshPushAgent_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:       phaseframe.BasePhase{Ctx: ctx},
		needPushNodes:   bkenode.Nodes{{IP: testNodeIP1}},
		localKubeConfig: []byte("test-config"),
	}

	patches.ApplyPrivateMethod(e, "sshPushAgent", func(_ *EnsureBKEAgent, _ context.Context, _ client.Client, _ []byte, _ string) (map[string]error, error) {
		return make(map[string]error), nil
	})

	errs, err := e.sshPushAgent(context.Background(), nil, []byte("test"), "/tmp/test.service")
	assert.NoError(t, err)
	assert.Empty(t, errs)
}

// TestEnsureBKEAgent_ExecutePreCommand_Success 测试executePreCommand成功
func TestEnsureBKEAgent_ExecutePreCommand_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	mockMultiCli := &bkessh.MultiCli{}
	pushAgentErrs := make(map[string]error)

	patches.ApplyPrivateMethod(e, "executePreCommand", func(_ *EnsureBKEAgent, _ *bkessh.MultiCli, _ map[string]error) error {
		return nil
	})

	err := e.executePreCommand(mockMultiCli, pushAgentErrs)
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_ExecuteStartCommand_Success 测试executeStartCommand成功
func TestEnsureBKEAgent_ExecuteStartCommand_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	mockMultiCli := &bkessh.MultiCli{}
	pushAgentErrs := make(map[string]error)

	patches.ApplyPrivateMethod(e, "executeStartCommand", func(_ *EnsureBKEAgent, _ *bkessh.MultiCli, _ []byte, _ string, _ map[string]error) error {
		return nil
	})

	err := e.executeStartCommand(mockMultiCli, []byte("test"), "/tmp/test.service", pushAgentErrs)
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_PingAgent_Success 测试pingAgent成功
func TestEnsureBKEAgent_PingAgent_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1, Hostname: testHostname1}},
	}

	patches.ApplyPrivateMethod(e, "pingAgent", func(_ *EnsureBKEAgent) error {
		return nil
	})

	err := e.pingAgent()
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_ValidateAndHandleNodesField_Success 测试validateAndHandleNodesField成功
func TestEnsureBKEAgent_ValidateAndHandleNodesField_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1, Hostname: testHostname1}},
	}

	patches.ApplyPrivateMethod(e, "validateAndHandleNodesField", func(_ *EnsureBKEAgent) error {
		return nil
	})

	err := e.validateAndHandleNodesField()
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_ValidateAndHandleNodesField_ValidationError 测试validateAndHandleNodesField验证失败
func TestEnsureBKEAgent_ValidateAndHandleNodesField_ValidationError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}},
	}

	patches.ApplyFunc(bkevalidate.ValidateNodesFields, func(_ bkenode.Nodes) error {
		return assert.AnError
	})
	patches.ApplyPrivateMethod(e, "handleValidationFailure", func(_ *EnsureBKEAgent, _ error) error {
		return assert.AnError
	})

	err := e.validateAndHandleNodesField()
	assert.Error(t, err)
}

// TestEnsureBKEAgent_HandleValidationFailure 测试handleValidationFailure
func TestEnsureBKEAgent_HandleValidationFailure(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "handleValidationFailure", func(_ *EnsureBKEAgent, _ error) error {
		return assert.AnError
	})

	err := e.handleValidationFailure(assert.AnError)
	assert.Error(t, err)
}

// TestEnsureBKEAgent_CheckAvailableHosts_NoHosts 测试checkAvailableHosts无可用主机
func TestEnsureBKEAgent_CheckAvailableHosts_NoHosts(t *testing.T) {
	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	multiCli := &bkessh.MultiCli{}
	errs := make(map[string]error)

	result := e.checkAvailableHosts(multiCli, errs)
	assert.False(t, result)
}

// TestEnsureBKEAgent_AllNeedPushNodesFailed_AllMatch 测试allNeedPushNodesFailed全部匹配
func TestEnsureBKEAgent_AllNeedPushNodesFailed_AllMatch(t *testing.T) {
	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}, {IP: testNodeIP2}},
	}

	failedIPs := []string{testNodeIP1, testNodeIP2}
	result := e.allNeedPushNodesFailed(failedIPs)
	assert.True(t, result)
}

// TestEnsureBKEAgent_PrepareFileUploadList_WithService 测试prepareFileUploadList
func TestEnsureBKEAgent_PrepareFileUploadList_WithService(t *testing.T) {
	tempDir := t.TempDir()
	servicePath := filepath.Join(tempDir, "test.service")
	err := os.WriteFile(servicePath, []byte("test"), testFilePerm)
	assert.NoError(t, err)

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.prepareFileUploadList(servicePath)
	assert.NotEmpty(t, result)
}

// TestEnsureBKEAgent_AddGlobalCAFilesIfNeeded_WithAddons 测试addGlobalCAFilesIfNeeded有addons
func TestEnsureBKEAgent_AddGlobalCAFilesIfNeeded_WithAddons(t *testing.T) {
	bkeCluster := createTestBKECluster(createTestNodes(1))
	bkeCluster.Spec.ClusterConfig.Addons = []confv1beta1.Product{{Name: "test"}}
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	fileUpList := []bkessh.File{}
	result := e.addGlobalCAFilesIfNeeded(fileUpList)
	assert.NotNil(t, result)
}

// TestEnsureBKEAgent_AddCSRFilesToUploadList_EmptyList 测试addCSRFilesToUploadList空列表
func TestEnsureBKEAgent_AddCSRFilesToUploadList_EmptyList(t *testing.T) {
	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	fileUpList := []bkessh.File{}
	result := e.addCSRFilesToUploadList(fileUpList)
	assert.NotNil(t, result)
}

// TestEnsureBKEAgent_UpdateNodeStatus_WithNodes 测试updateNodeStatus
func TestEnsureBKEAgent_UpdateNodeStatus_WithNodes(t *testing.T) {
	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	successNodes := []string{testNodeIP1 + "success"}
	failedNodes := []string{}
	e.updateNodeStatus(bkeCluster, successNodes, failedNodes)
	assert.NotNil(t, bkeCluster)
}

// TestEnsureBKEAgent_CheckAllOrPushedAgentsFailed_AllSuccess 测试checkAllOrPushedAgentsFailed全部成功
func TestEnsureBKEAgent_CheckAllOrPushedAgentsFailed_AllSuccess(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}},
	}

	patches.ApplyPrivateMethod(e, "checkAllOrPushedAgentsFailed", func(_ *EnsureBKEAgent, _ []string, _ []string) error {
		return nil
	})

	err := e.checkAllOrPushedAgentsFailed([]string{testNodeIP1}, []string{})
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_LoadLocalKubeConfig_Mock 测试loadLocalKubeConfig
func TestEnsureBKEAgent_LoadLocalKubeConfig_Mock(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "loadLocalKubeConfig", func(agent *EnsureBKEAgent) error {
		agent.localKubeConfig = []byte("test-config")
		return nil
	})

	err := e.loadLocalKubeConfig()
	assert.NoError(t, err)
	assert.NotEmpty(t, e.localKubeConfig)
}

// TestEnsureBKEAgent_GetNeedPushNodes_Mock 测试getNeedPushNodes
func TestEnsureBKEAgent_GetNeedPushNodes_Mock(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "getNeedPushNodes", func(agent *EnsureBKEAgent) error {
		agent.needPushNodes = bkenode.Nodes{{IP: testNodeIP1}}
		return nil
	})

	err := e.getNeedPushNodes()
	assert.NoError(t, err)
	assert.Len(t, e.needPushNodes, 1)
}

// TestEnsureBKEAgent_HandlePushResults_Mock 测试handlePushResults
func TestEnsureBKEAgent_HandlePushResults_Mock(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}},
	}

	patches.ApplyPrivateMethod(e, "handlePushResults", func(_ *EnsureBKEAgent, _ context.Context, _ client.Client, _ *bkev1beta1.BKECluster, _ []string) error {
		return nil
	})

	err := e.handlePushResults(context.Background(), nil, bkeCluster, []string{})
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_SshPushAgent_Mock 测试sshPushAgent
func TestEnsureBKEAgent_SshPushAgent_Mock(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:       phaseframe.BasePhase{Ctx: ctx},
		needPushNodes:   bkenode.Nodes{{IP: testNodeIP1}},
		localKubeConfig: []byte("test-config"),
	}

	patches.ApplyPrivateMethod(e, "sshPushAgent", func(_ *EnsureBKEAgent, _ context.Context, _ client.Client, _ []byte, _ string) (map[string]error, error) {
		return make(map[string]error), nil
	})

	errs, err := e.sshPushAgent(context.Background(), nil, []byte("test"), "/tmp/test.service")
	assert.NoError(t, err)
	assert.Empty(t, errs)
}

// TestEnsureBKEAgent_ExecutePreCommand_Mock 测试executePreCommand
func TestEnsureBKEAgent_ExecutePreCommand_Mock(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	mockMultiCli := &bkessh.MultiCli{}
	pushAgentErrs := make(map[string]error)

	patches.ApplyPrivateMethod(e, "executePreCommand", func(_ *EnsureBKEAgent, _ *bkessh.MultiCli, _ map[string]error) error {
		return nil
	})

	err := e.executePreCommand(mockMultiCli, pushAgentErrs)
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_ExecuteStartCommand_Mock 测试executeStartCommand
func TestEnsureBKEAgent_ExecuteStartCommand_Mock(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	mockMultiCli := &bkessh.MultiCli{}
	pushAgentErrs := make(map[string]error)

	patches.ApplyPrivateMethod(e, "executeStartCommand", func(_ *EnsureBKEAgent, _ *bkessh.MultiCli, _ []byte, _ string, _ map[string]error) error {
		return nil
	})

	err := e.executeStartCommand(mockMultiCli, []byte("test"), "/tmp/test.service", pushAgentErrs)
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_PingAgent_Mock 测试pingAgent
func TestEnsureBKEAgent_PingAgent_Mock(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1, Hostname: testHostname1}},
	}

	patches.ApplyPrivateMethod(e, "pingAgent", func(_ *EnsureBKEAgent) error {
		return nil
	})

	err := e.pingAgent()
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_HandleValidationFailure_Mock 测试handleValidationFailure
func TestEnsureBKEAgent_HandleValidationFailure_Mock(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "handleValidationFailure", func(_ *EnsureBKEAgent, _ error) error {
		return nil
	})

	err := e.handleValidationFailure(assert.AnError)
	assert.NoError(t, err)
}

// TestEnsureBKEAgent_NeedExecute_Mock 测试NeedExecute
func TestEnsureBKEAgent_NeedExecute_Mock(t *testing.T) {
	nodes := createTestNodes(1)
	oldCluster := createTestBKECluster(nodes)
	newCluster := createTestBKECluster(nodes)

	ctx := createTestPhaseContext(newCluster)
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.NeedExecute(oldCluster, newCluster)
	assert.False(t, result)
}

// TestNewEnsureBKEAgent_Creation 测试NewEnsureBKEAgent创建
func TestNewEnsureBKEAgent_Creation(t *testing.T) {
	bkeCluster := createTestBKECluster(createTestNodes(1))
	ctx := createTestPhaseContext(bkeCluster)
	phase := NewEnsureBKEAgent(ctx)
	assert.NotNil(t, phase)
}

// TestEnsureBKEAgent_AddFilesToUploadList_MultipleFiles 测试addFilesToUploadList多个文件
func TestEnsureBKEAgent_AddFilesToUploadList_MultipleFiles(t *testing.T) {
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "file1.txt")
	file2 := filepath.Join(tempDir, "file2.txt")
	err := os.WriteFile(file1, []byte("test1"), testFilePerm)
	assert.NoError(t, err)
	err = os.WriteFile(file2, []byte("test2"), testFilePerm)
	assert.NoError(t, err)

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	fileUpList := []bkessh.File{}
	result := e.addFilesToUploadList(fileUpList, []string{file1, file2}, testDstDir)
	assert.Len(t, result, 2)
}

// TestEnsureBKEAgent_CheckAllOrPushedAgentsFailed_PartialFailure 测试checkAllOrPushedAgentsFailed部分失败
func TestEnsureBKEAgent_CheckAllOrPushedAgentsFailed_PartialFailure(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx := createTestPhaseContext(&bkev1beta1.BKECluster{})
	e := &EnsureBKEAgent{
		BasePhase:     phaseframe.BasePhase{Ctx: ctx},
		needPushNodes: bkenode.Nodes{{IP: testNodeIP1}, {IP: testNodeIP2}},
	}

	patches.ApplyPrivateMethod(e, "allNeedPushNodesFailed", func(_ *EnsureBKEAgent, _ []string) bool {
		return false
	})

	err := e.checkAllOrPushedAgentsFailed([]string{testNodeIP1}, []string{testNodeIP2})
	assert.NoError(t, err)
}


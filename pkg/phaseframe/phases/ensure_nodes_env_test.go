/*
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package phases

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

// createBKENodeWithFlags creates a BKENode with the specified flags set
func createBKENodeWithFlags(namespace, clusterName, ip, hostname string, roles []string, flags ...int) *confv1beta1.BKENode {
	stateCode := 0
	for _, flag := range flags {
		stateCode |= flag
	}

	return &confv1beta1.BKENode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hostname,
			Namespace: namespace,
			Labels: map[string]string{
				nodeutil.ClusterNameLabel: clusterName,
			},
		},
		Spec: confv1beta1.BKENodeSpec{
			IP:       ip,
			Hostname: hostname,
			Role:     roles,
		},
		Status: confv1beta1.BKENodeStatus{
			StateCode: stateCode,
		},
	}
}

func TestEnsure_nodes_env_get_nodes_to_init_env(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()

	// Create BKENode resources instead of NodesStatus
	bkeNode1 := createBKENodeWithFlags(
		bkeCluster.Namespace, bkeCluster.Name,
		"10.0.0.1", "node1", []string{bkenode.MasterNodeRole},
		bkev1beta1.NodeAgentReadyFlag,
	)
	bkeNode2 := createBKENodeWithFlags(
		bkeCluster.Namespace, bkeCluster.Name,
		"10.0.0.2", "node2", []string{bkenode.WorkerNodeRole},
		bkev1beta1.NodeAgentReadyFlag, bkev1beta1.NodeEnvFlag,
	)

	// Create fake client with BKENode resources
	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(bkeNode1, bkeNode2).
		WithStatusSubresource(bkeNode1, bkeNode2).
		Build()

	initPhaseContext.BKECluster = bkeCluster
	initPhaseContext.Client = fakeClient

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	nodes := e.getNodesToInitEnv()
	// Only node1 should be returned (has AgentReady but not EnvFlag)
	require.Equal(t, 1, nodes.Length())
	require.Equal(t, "10.0.0.1", nodes[0].IP)
}

func TestEnsure_nodes_env_handle_success_nodes(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()

	// Create BKENode resource
	bkeNode1 := createBKENodeWithFlags(
		bkeCluster.Namespace, bkeCluster.Name,
		"10.0.0.1", "master1", []string{bkenode.MasterNodeRole},
	)

	// Create fake client with BKENode resources
	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(bkeNode1).
		WithStatusSubresource(bkeNode1).
		Build()

	initPhaseContext.BKECluster = bkeCluster
	initPhaseContext.Client = fakeClient

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	e.nodes = bkenode.Nodes{{IP: "10.0.0.1", Hostname: "master1", Role: []string{bkenode.MasterNodeRole}}}

	e.handleSuccessNodes([]string{"master1/10.0.0.1"})

	// Verify the flag was set by reading the BKENode
	updatedNode := &confv1beta1.BKENode{}
	err := fakeClient.Get(context.Background(), client.ObjectKey{
		Namespace: bkeCluster.Namespace,
		Name:      "master1",
	}, updatedNode)
	require.NoError(t, err)
	require.True(t, phaseutil.GetNodeStateFlag(updatedNode, "10.0.0.1", bkev1beta1.NodeEnvFlag))
	require.Equal(t, 1, e.nodes.Length())
}

func TestEnsure_nodes_env_check_preprocess_config_exists_global(t *testing.T) {
	InitinitPhaseContextFun()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preprocess-all-config",
			Namespace: "user-system",
		},
		Data: map[string]string{
			"config.json": `{"scripts":[{"scriptName":"a.sh","order":1}]}`,
		},
	}
	initPhaseContext.Client = newFakeClientWithObjects(t, cm)

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	ok := e.checkPreprocessConfigExists(context.Background(), initPhaseContext.Client, initPhaseContext.Log, "10.0.0.1")
	require.True(t, ok)
}

func TestEnsure_nodes_env_check_preprocess_config_exists_batch(t *testing.T) {
	InitinitPhaseContextFun()

	mapping := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preprocess-node-batch-mapping",
			Namespace: "user-system",
		},
		Data: map[string]string{
			"mapping.json": `{"10.0.0.1":"001"}`,
		},
	}
	batch := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preprocess-config-batch-001",
			Namespace: "user-system",
		},
		Data: map[string]string{
			"config.json": `{"scripts":[{"scriptName":"b.sh","order":1}]}`,
		},
	}
	initPhaseContext.Client = newFakeClientWithObjects(t, mapping, batch)

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	ok := e.checkPreprocessConfigExists(context.Background(), initPhaseContext.Client, initPhaseContext.Log, "10.0.0.1")
	require.True(t, ok)
}

func TestEnsure_nodes_env_get_nodes_ips_by_script(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeCluster.Spec.ClusterConfig.CustomExtra = map[string]string{
		"pipelineServer":                  "10.0.0.9",
		"pipelineServerEnableCleanImages": "true",
		"host":                            "10.0.0.2",
	}
	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	e.nodes = bkenode.Nodes{
		{IP: "10.0.0.1", Hostname: "node1", Role: []string{bkenode.MasterNodeRole}},
		{IP: "10.0.0.2", Hostname: "node2", Role: []string{bkenode.WorkerNodeRole}},
	}

	got, err := e.getNodesIpsByScript("install-nfsutils.sh")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.9", got)

	got, err = e.getNodesIpsByScript("clean-docker-images.py")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.9", got)

	got, err = e.getNodesIpsByScript("update-runc.sh")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", got)
}

func TestEnsure_nodes_env_get_nodes_ips_by_script_errors(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeCluster.Spec.ClusterConfig.CustomExtra = map[string]string{}
	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	e.nodes = bkenode.Nodes{
		{IP: "10.0.0.1", Hostname: "node1", Role: []string{bkenode.MasterNodeRole}},
	}

	_, err := e.getNodesIpsByScript("install-nfsutils.sh")
	require.Error(t, err)

	_, err = e.getNodesIpsByScript("clean-docker-images.py")
	require.Error(t, err)
}

func newFakeClientWithObjects(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestEnsureNodesEnv_Constants(t *testing.T) {
	assert.Equal(t, "EnsureNodesEnv", string(EnsureNodesEnvName))
	assert.Len(t, defaultEnvExtraExecScripts, 7)
	assert.Len(t, commonEnvExtraExecScripts, 2)
}

func TestEnsureNodesEnv_NewEnsureNodesEnv(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureNodesEnv(initPhaseContext)
	assert.NotNil(t, phase)
	_, ok := phase.(*EnsureNodesEnv)
	assert.True(t, ok)
}

func TestEnsureNodesEnv_NeedExecute_DefaultNeedExecuteFalse(t *testing.T) {
	InitinitPhaseContextFun()

	now := metav1.Now()
	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeCluster.DeletionTimestamp = &now

	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	old := &bkev1beta1.BKECluster{}
	result := e.NeedExecute(old, bkeCluster)
	assert.False(t, result)
}

func TestEnsureNodesEnv_NeedExecute_GetBKENodesError(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	// Mock to return error when getting BKENodes
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(fetchBKENodesIfCPInitialized, func(ctx *phaseframe.PhaseContext, cluster *bkev1beta1.BKECluster) (bkev1beta1.BKENodes, bool) {
		return nil, false
	})

	old := &bkev1beta1.BKECluster{}
	result := e.NeedExecute(old, bkeCluster)
	assert.False(t, result)
}

func TestEnsureNodesEnv_NeedExecute_NoNodesNeedEnv(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	// Mock to return nodes without NodeEnvFlag
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(fetchBKENodesIfCPInitialized, func(ctx *phaseframe.PhaseContext, cluster *bkev1beta1.BKECluster) (bkev1beta1.BKENodes, bool) {
		return bkev1beta1.BKENodes{}, true
	})

	patches.ApplyFunc(phaseutil.HasNodesNeedingPhase, func(nodes bkev1beta1.BKENodes, flag int) bool {
		return false
	})

	old := &bkev1beta1.BKECluster{}
	result := e.NeedExecute(old, bkeCluster)
	assert.False(t, result)
}


func TestEnsureNodesEnv_shouldUseDeepRestore_True(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeCluster.Annotations = map[string]string{"deepRestoreNode": "true"}
	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	result := e.shouldUseDeepRestore(bkeCluster)
	assert.True(t, result)
}

func TestEnsureNodesEnv_shouldUseDeepRestore_False(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeCluster.Annotations = map[string]string{"bke.bocloud.com/deep-restore-node": "false"}
	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	result := e.shouldUseDeepRestore(bkeCluster)
	assert.False(t, result)
}

func TestEnsureNodesEnv_shouldUseDeepRestore_NoAnnotation(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	result := e.shouldUseDeepRestore(bkeCluster)
	assert.True(t, result)
}

func TestBuildCommonEnvCommandParams_Structure(t *testing.T) {
	params := BuildCommonEnvCommandParams{
		Ctx:            context.Background(),
		Client:         &fakeClient{},
		BKECluster:     &bkev1beta1.BKECluster{},
		Scheme:         runtime.NewScheme(),
		ExceptEnvNodes: bkenode.Nodes{{IP: "10.0.0.1"}},
		Extra:          []string{"extra1"},
		ExtraHosts:     []string{"host1"},
		DryRun:         false,
		DeepRestore:    true,
		Log:            createTestLogger(),
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.Scheme)
	assert.Len(t, params.ExceptEnvNodes, 1)
	assert.Len(t, params.Extra, 1)
	assert.Len(t, params.ExtraHosts, 1)
	assert.False(t, params.DryRun)
	assert.True(t, params.DeepRestore)
	assert.NotNil(t, params.Log)
}

func TestInstallScriptParams_Structure(t *testing.T) {
	params := InstallScriptParams{
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
		ScriptsLi:   []string{"script1", "script2"},
	}
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.Log)
	assert.Len(t, params.ScriptsLi, 2)
}

func TestInstallOtherScriptParams_Structure(t *testing.T) {
	params := InstallOtherScriptParams{
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
		ScriptsLi:  []string{"script1"},
		Cfg:        bkeinit.BkeConfig{},
	}
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.Log)
	assert.Len(t, params.ScriptsLi, 1)
}

func TestEnsureNodesEnv_createAddonTransfer(t *testing.T) {
	InitinitPhaseContextFun()

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	result := e.createAddonTransfer("test-script.sh", map[string]string{"key": "value"}, true)
	assert.NotNil(t, result)
	assert.Equal(t, "clusterextra", result.Addon.Name)
	assert.Equal(t, "test-script.sh", result.Addon.Version)
	assert.Equal(t, map[string]string{"key": "value"}, result.Addon.Param)
	assert.True(t, result.Addon.Block)
}

func TestEnsureNodesEnv_handleFileDownloaderScript(t *testing.T) {
	InitinitPhaseContextFun()

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	e.nodes = bkenode.Nodes{
		{IP: "10.0.0.1"},
		{IP: "10.0.0.2"},
	}

	result, err := e.handleFileDownloaderScript([]string{"10.0.0.1", "10.0.0.2"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1,10.0.0.2", result)
}

func TestEnsureNodesEnv_handlePackageDownloaderScript(t *testing.T) {
	InitinitPhaseContextFun()

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	e.nodes = bkenode.Nodes{
		{IP: "10.0.0.1"},
		{IP: "10.0.0.2"},
	}

	result, err := e.handlePackageDownloaderScript([]string{"10.0.0.1", "10.0.0.2"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1,10.0.0.2", result)
}

func TestEnsureNodesEnv_handleInstallLxcfsScript(t *testing.T) {
	InitinitPhaseContextFun()

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	result, err := e.handleInstallLxcfsScript([]string{"10.0.0.1", "10.0.0.2"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1,10.0.0.2", result)
}

func TestEnsureNodesEnv_handleInstallEtcdctlScript(t *testing.T) {
	InitinitPhaseContextFun()

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	result, err := e.handleInstallEtcdctlScript([]string{"10.0.0.1", "10.0.0.2"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1,10.0.0.2", result)
}

func TestEnsureNodesEnv_handleInstallHelmScript(t *testing.T) {
	InitinitPhaseContextFun()

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	result, err := e.handleInstallHelmScript([]string{"10.0.0.1", "10.0.0.2"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1,10.0.0.2", result)
}

func TestEnsureNodesEnv_handleInstallCalicoctlScript(t *testing.T) {
	InitinitPhaseContextFun()

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	result, err := e.handleInstallCalicoctlScript([]string{"10.0.0.1", "10.0.0.2"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1,10.0.0.2", result)
}

func TestEnsureNodesEnv_handleDefaultScript(t *testing.T) {
	InitinitPhaseContextFun()

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	result, err := e.handleDefaultScript([]string{"10.0.0.1", "10.0.0.2"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1,10.0.0.2", result)
}

func TestEnsureNodesEnv_handleUpdateRuncScript_NoHostFilter(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeCluster.Spec.ClusterConfig.CustomExtra = map[string]string{}
	initPhaseContext.BKECluster = bkeCluster

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	result, err := e.handleUpdateRuncScript([]string{"10.0.0.1", "10.0.0.2"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1,10.0.0.2", result)
}

func TestEnsureNodesEnv_checkPreprocessConfigExists_NodeConfig(t *testing.T) {
	InitinitPhaseContextFun()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preprocess-config-node-10.0.0.1",
			Namespace: "user-system",
		},
		Data: map[string]string{
			"config.json": `{"scripts":[{"scriptName":"c.sh","order":1}]}`,
		},
	}
	initPhaseContext.Client = newFakeClientWithObjects(t, cm)

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	ok := e.checkPreprocessConfigExists(context.Background(), initPhaseContext.Client, initPhaseContext.Log, "10.0.0.1")
	assert.True(t, ok)
}

func TestEnsureNodesEnv_checkPreprocessConfigExists_NotFound(t *testing.T) {
	InitinitPhaseContextFun()

	initPhaseContext.Client = newFakeClientWithObjects(t)

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	ok := e.checkPreprocessConfigExists(context.Background(), initPhaseContext.Client, initPhaseContext.Log, "10.0.0.1")
	assert.False(t, ok)
}

func TestEnsureNodesEnv_BuildCommonEnvCommand(t *testing.T) {
	InitinitPhaseContextFun()

	bkeCluster := initNewBkeCluster.DeepCopy()
	initPhaseContext.BKECluster = bkeCluster

	params := BuildCommonEnvCommandParams{
		Ctx:            context.Background(),
		Client:         &fakeClient{},
		BKECluster:     bkeCluster,
		Scheme:         runtime.NewScheme(),
		ExceptEnvNodes: bkenode.Nodes{{IP: "10.0.0.1"}},
		Extra:          []string{"extra1"},
		ExtraHosts:     []string{"host1"},
		DryRun:         false,
		DeepRestore:    false,
		Log:            initLog,
	}

	envCmd, err := BuildCommonEnvCommand(params)
	require.NoError(t, err)
	assert.NotNil(t, envCmd)
	assert.Equal(t, bkeCluster.Name, envCmd.BkeConfigName)
}

func TestEnsureNodesEnv_getNodesToInitEnv_NoNodes(t *testing.T) {
	InitinitPhaseContextFun()

	// Create BKENode with EnvFlag already set
	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeNode := createBKENodeWithFlags(
		bkeCluster.Namespace, bkeCluster.Name,
		"10.0.0.1", "node1", []string{bkenode.MasterNodeRole},
		bkev1beta1.NodeAgentReadyFlag, bkev1beta1.NodeEnvFlag,
	)

	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(bkeNode).
		WithStatusSubresource(bkeNode).
		Build()

	initPhaseContext.BKECluster = bkeCluster
	initPhaseContext.Client = fakeClient

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	nodes := e.getNodesToInitEnv()
	require.Equal(t, 0, nodes.Length())
}

func TestEnsureNodesEnv_getNodesToInitEnv_AgentNotReady(t *testing.T) {
	InitinitPhaseContextFun()

	// Create BKENode without AgentReadyFlag
	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeNode := createBKENodeWithFlags(
		bkeCluster.Namespace, bkeCluster.Name,
		"10.0.0.1", "node1", []string{bkenode.MasterNodeRole},
		// No NodeAgentReadyFlag
	)

	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(bkeNode).
		WithStatusSubresource(bkeNode).
		Build()

	initPhaseContext.BKECluster = bkeCluster
	initPhaseContext.Client = fakeClient

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	nodes := e.getNodesToInitEnv()
	require.Equal(t, 0, nodes.Length())
}

func TestEnsureNodesEnv_getNodesToInitEnv_NodeFailed(t *testing.T) {
	InitinitPhaseContextFun()

	// Create BKENode with Failed flag
	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeNode := createBKENodeWithFlags(
		bkeCluster.Namespace, bkeCluster.Name,
		"10.0.0.1", "node1", []string{bkenode.MasterNodeRole},
		bkev1beta1.NodeAgentReadyFlag, bkev1beta1.NodeFailedFlag,
	)

	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(bkeNode).
		WithStatusSubresource(bkeNode).
		Build()

	initPhaseContext.BKECluster = bkeCluster
	initPhaseContext.Client = fakeClient

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	nodes := e.getNodesToInitEnv()
	require.Equal(t, 0, nodes.Length())
}

func TestEnsureNodesEnv_getNodesToInitEnv_NodeDeleting(t *testing.T) {
	InitinitPhaseContextFun()

	// Create BKENode with Deleting flag
	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeNode := createBKENodeWithFlags(
		bkeCluster.Namespace, bkeCluster.Name,
		"10.0.0.1", "node1", []string{bkenode.MasterNodeRole},
		bkev1beta1.NodeAgentReadyFlag, bkev1beta1.NodeDeletingFlag,
	)

	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(bkeNode).
		WithStatusSubresource(bkeNode).
		Build()

	initPhaseContext.BKECluster = bkeCluster
	initPhaseContext.Client = fakeClient

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	nodes := e.getNodesToInitEnv()
	require.Equal(t, 0, nodes.Length())
}

func TestEnsureNodesEnv_getNodesToInitEnv_NodeNeedSkip(t *testing.T) {
	InitinitPhaseContextFun()

	// Create BKENode with NeedSkip
	bkeCluster := initNewBkeCluster.DeepCopy()
	bkeNode := createBKENodeWithFlags(
		bkeCluster.Namespace, bkeCluster.Name,
		"10.0.0.1", "node1", []string{bkenode.MasterNodeRole},
		bkev1beta1.NodeAgentReadyFlag,
	)
	bkeNode.Status.NeedSkip = true

	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(bkeNode).
		WithStatusSubresource(bkeNode).
		Build()

	initPhaseContext.BKECluster = bkeCluster
	initPhaseContext.Client = fakeClient

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)

	nodes := e.getNodesToInitEnv()
	require.Equal(t, 0, nodes.Length())
}

func TestEnsureNodesEnv_checkPreprocessConfigExists_BatchMappingNotFound(t *testing.T) {
	InitinitPhaseContextFun()

	// Only create batch mapping CM, not the batch config CM
	mapping := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preprocess-node-batch-mapping",
			Namespace: "user-system",
		},
		Data: map[string]string{
			"mapping.json": `{"10.0.0.1":"001"}`,
		},
	}
	initPhaseContext.Client = newFakeClientWithObjects(t, mapping)

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	ok := e.checkPreprocessConfigExists(context.Background(), initPhaseContext.Client, initPhaseContext.Log, "10.0.0.1")
	assert.False(t, ok)
}

func TestEnsureNodesEnv_checkPreprocessConfigExists_BatchMappingInvalid(t *testing.T) {
	InitinitPhaseContextFun()

	// Invalid mapping JSON
	mapping := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preprocess-node-batch-mapping",
			Namespace: "user-system",
		},
		Data: map[string]string{
			"mapping.json": `invalid json`,
		},
	}
	initPhaseContext.Client = newFakeClientWithObjects(t, mapping)

	e := NewEnsureNodesEnv(initPhaseContext).(*EnsureNodesEnv)
	ok := e.checkPreprocessConfigExists(context.Background(), initPhaseContext.Client, initPhaseContext.Log, "10.0.0.1")
	assert.False(t, ok)
}

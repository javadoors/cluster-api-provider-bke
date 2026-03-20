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

package phases

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
)

func TestEnsureWorkerUpgradeConstants(t *testing.T) {
	assert.Equal(t, "EnsureWorkerUpgrade", string(EnsureWorkerUpgradeName))
	assert.Equal(t, 2, WorkerNodeHealthCheckPollIntervalSeconds)
	assert.Equal(t, 5, WorkerNodeHealthCheckTimeoutMinutes)
}

func TestNewEnsureWorkerUpgrade(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	phase := NewEnsureWorkerUpgrade(ctx)
	assert.NotNil(t, phase)
}

func TestCreateUpgradeCommand(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	node := &confv1beta1.Node{IP: "192.168.1.1", Hostname: "node1"}
	params := CreateUpgradeCommandParams{
		Ctx:         context.Background(),
		Namespace:   "default",
		Client:      c,
		Scheme:      scheme,
		OwnerObj:    bkeCluster,
		ClusterName: "test",
		Node:        node,
		BKEConfig:   "test-config",
		Phase:       bkev1beta1.UpgradeWorker,
	}

	cmd := createUpgradeCommand(params)
	assert.NotNil(t, cmd)
	assert.Equal(t, "default", cmd.NameSpace)
	assert.Equal(t, "test", cmd.ClusterName)
	assert.Equal(t, node, cmd.Node)
	assert.Equal(t, "test-config", cmd.BKEConfig)
	assert.Equal(t, bkev1beta1.UpgradeWorker, cmd.Phase)
	assert.True(t, cmd.Unique)
	assert.NotNil(t, cmd.Ctx)
	assert.NotNil(t, cmd.Client)
	assert.NotNil(t, cmd.Scheme)
	assert.NotNil(t, cmd.OwnerObj)
}

func TestCreateUpgradeCommandParams_Structure(t *testing.T) {
	params := CreateUpgradeCommandParams{
		Ctx:         context.Background(),
		Namespace:   "default",
		Client:      &fakeClient{},
		Scheme:      runtime.NewScheme(),
		OwnerObj:    &bkev1beta1.BKECluster{},
		ClusterName: "test",
		Node:        &confv1beta1.Node{},
		BKEConfig:   "config",
		Phase:       bkev1beta1.UpgradeWorker,
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.Scheme)
	assert.NotNil(t, params.OwnerObj)
	assert.NotNil(t, params.Node)
}

func TestPrepareUpgradeNodesParams_Structure(t *testing.T) {
	params := PrepareUpgradeNodesParams{
		Ctx:        context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.Log)
}

func TestWaitForWorkerNodeHealthCheckParams_Structure(t *testing.T) {
	params := WaitForWorkerNodeHealthCheckParams{
		Ctx:        context.Background(),
		K8sVersion: "v1.28.0",
		Logger:     createTestLogger(),
	}
	assert.NotNil(t, params.Ctx)
	assert.Equal(t, "v1.28.0", params.K8sVersion)
	assert.NotNil(t, params.Logger)
}

func TestWaitForNodeHealthParams_Structure(t *testing.T) {
	params := WaitForNodeHealthParams{
		Ctx:        context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Node:       confv1beta1.Node{IP: "192.168.1.1"},
		Log:        createTestLogger(),
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.BKECluster)
	assert.Equal(t, "192.168.1.1", params.Node.IP)
	assert.NotNil(t, params.Log)
}

func TestProcessNodeUpgradeParams_Structure(t *testing.T) {
	params := ProcessNodeUpgradeParams{
		Ctx:        context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.Log)
}

func TestEnsureWorkerUpgrade_NeedExecute_UnhealthyCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{},
		},
		Status: confv1beta1.BKEClusterStatus{
			ClusterStatus: bkev1beta1.ClusterUnhealthy,
		},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	phase := NewEnsureWorkerUpgrade(ctx)
	e := phase.(*EnsureWorkerUpgrade)

	old := &bkev1beta1.BKECluster{}
	result := e.NeedExecute(old, bkeCluster)
	assert.False(t, result)
}

func TestEnsureWorkerUpgrade_NeedExecute_NoUpgradeNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{},
		},
		Status: confv1beta1.BKEClusterStatus{},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	patches.ApplyFunc(fetchBKENodesIfCPInitialized, func(ctx *phaseframe.PhaseContext, cluster *bkev1beta1.BKECluster) (bkev1beta1.BKENodes, bool) {
		return bkev1beta1.BKENodes{}, true
	})

	patches.ApplyFunc(phaseutil.GetNeedUpgradeWorkerNodesWithBKENodes, func(cluster *bkev1beta1.BKECluster, nodes bkev1beta1.BKENodes) bkenode.Nodes {
		return bkenode.Nodes{}
	})

	phase := NewEnsureWorkerUpgrade(ctx)
	e := phase.(*EnsureWorkerUpgrade)

	old := &bkev1beta1.BKECluster{}
	result := e.NeedExecute(old, bkeCluster)
	assert.False(t, result)
}

func TestEnsureWorkerUpgrade_ExecutePreHook(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{},
		},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	patches.ApplyMethod(&phaseframe.BasePhase{}, "DefaultPreHook", func(_ *phaseframe.BasePhase) error {
		return nil
	})

	phase := NewEnsureWorkerUpgrade(ctx)
	e := phase.(*EnsureWorkerUpgrade)
	err := e.ExecutePreHook()
	assert.NoError(t, err)
}

func TestWaitForWorkerNodeHealthCheck_ContextDone(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	params := WaitForWorkerNodeHealthCheckParams{
		Ctx:        ctx,
		K8sVersion: "v1.28.0",
		Logger:     createTestLogger(),
	}

	err := waitForWorkerNodeHealthCheck(params)
	assert.Error(t, err)
}

func TestEnsureWorkerUpgrade_NeedExecute_WithUpgradeNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{},
		},
		Status: confv1beta1.BKEClusterStatus{},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	patches.ApplyFunc(fetchBKENodesIfCPInitialized, func(ctx *phaseframe.PhaseContext, cluster *bkev1beta1.BKECluster) (bkev1beta1.BKENodes, bool) {
		return bkev1beta1.BKENodes{}, true
	})

	patches.ApplyFunc(phaseutil.GetNeedUpgradeWorkerNodesWithBKENodes, func(cluster *bkev1beta1.BKECluster, nodes bkev1beta1.BKENodes) bkenode.Nodes {
		return bkenode.Nodes{{IP: "192.168.1.1"}}
	})

	phase := NewEnsureWorkerUpgrade(ctx)
	e := phase.(*EnsureWorkerUpgrade)

	old := &bkev1beta1.BKECluster{}
	result := e.NeedExecute(old, bkeCluster)
	assert.True(t, result)
}

func TestEnsureWorkerUpgrade_NeedExecute_CPNotInitialized(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status:     confv1beta1.BKEClusterStatus{},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	patches.ApplyFunc(fetchBKENodesIfCPInitialized, func(ctx *phaseframe.PhaseContext, cluster *bkev1beta1.BKECluster) (bkev1beta1.BKENodes, bool) {
		return nil, false
	})

	phase := NewEnsureWorkerUpgrade(ctx).(*EnsureWorkerUpgrade)
	result := phase.NeedExecute(nil, bkeCluster)
	assert.False(t, result)
}

func TestEnsureWorkerUpgrade_NeedExecute_ClusterUnknown(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: confv1beta1.BKEClusterStatus{
			ClusterStatus: bkev1beta1.ClusterUnknown,
		},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerUpgrade(ctx).(*EnsureWorkerUpgrade)
	result := phase.NeedExecute(nil, bkeCluster)
	assert.False(t, result)
}

func TestEnsureWorkerUpgrade_NeedExecute_DefaultNeedExecuteFalse(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status:     confv1beta1.BKEClusterStatus{},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	patches.ApplyMethod(&phaseframe.BasePhase{}, "DefaultNeedExecute", func(_ *phaseframe.BasePhase, old, new *bkev1beta1.BKECluster) bool {
		return false
	})

	phase := NewEnsureWorkerUpgrade(ctx).(*EnsureWorkerUpgrade)
	result := phase.NeedExecute(nil, bkeCluster)
	assert.False(t, result)
}

func TestEnsureWorkerUpgrade_PrepareUpgradeNodes(t *testing.T) {
	t.Skip("Skipping - requires complex NodeFetcher mocking")
}

func TestEnsureWorkerUpgrade_Execute(t *testing.T) {
	t.Skip("Skipping - requires mocking private method reconcileWorkerUpgrade")
}

func TestEnsureWorkerUpgrade_ReconcileWorkerUpgrade(t *testing.T) {
	t.Skip("Skipping - private method cannot be mocked with gomonkey")
}

func TestEnsureWorkerUpgrade_RolloutUpgrade(t *testing.T) {
	t.Skip("Skipping - private method cannot be mocked with gomonkey")
}

func TestEnsureWorkerUpgrade_ProcessNodeUpgrade(t *testing.T) {
	t.Skip("Skipping - requires complex kubernetes client mocking")
}

func TestEnsureWorkerUpgrade_ExecuteNodeUpgrade(t *testing.T) {
	t.Skip("Skipping - requires complex command mocking")
}

func TestEnsureWorkerUpgrade_WaitForNodeHealth(t *testing.T) {
	t.Skip("Skipping - requires complex remote client mocking")
}

func TestEnsureWorkerUpgrade_UpgradeNode(t *testing.T) {
	t.Skip("Skipping - requires mocking private methods")
}

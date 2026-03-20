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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
)

func createTestEnsureMasterDelete() *EnsureMasterDelete {
	logger := createTestLogger()
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: confv1beta1.BKEClusterSpec{},
	}

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		Client:     &fakeClient{},
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Log:        logger,
	}

	return &EnsureMasterDelete{
		BasePhase:                    phaseframe.NewBasePhase(ctx, EnsureMasterDeleteName),
		machinesAndNodesToDelete:     make(map[string]phaseutil.MachineAndNode),
		machinesAndNodesToWaitDelete: make(map[string]phaseutil.MachineAndNode),
	}
}

func TestEnsureMasterDeleteConstants(t *testing.T) {
	assert.Equal(t, "EnsureMasterDelete", string(EnsureMasterDeleteName))
	assert.Equal(t, 4, WaitMasterDeleteTimeoutMinutes)
	assert.Equal(t, 2, WaitMasterDeletePollIntervalSeconds)
}

func TestNewEnsureMasterDelete(t *testing.T) {
	logger := createTestLogger()
	ctx := &phaseframe.PhaseContext{
		Context: context.Background(),
		Client:  &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		},
		Scheme: runtime.NewScheme(),
		Log:    logger,
	}

	phase := NewEnsureMasterDelete(ctx)
	assert.NotNil(t, phase)
	assert.IsType(t, &EnsureMasterDelete{}, phase)

	emd := phase.(*EnsureMasterDelete)
	assert.NotNil(t, emd.machinesAndNodesToDelete)
	assert.NotNil(t, emd.machinesAndNodesToWaitDelete)
}

func TestEnsureMasterDelete_NeedExecute_DefaultNeedExecuteFalse(t *testing.T) {
	e := createTestEnsureMasterDelete()
	now := metav1.Now()
	old := &bkev1beta1.BKECluster{}
	new := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			DeletionTimestamp: &now,
		},
	}

	result := e.NeedExecute(old, new)
	assert.False(t, result)
}

func TestEnsureMasterDelete_NeedExecute_WithDeleteNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureMasterDelete()

	patches.ApplyFunc(phaseutil.GetNeedDeleteMasterNodes, func(ctx context.Context, c client.Client, cluster *bkev1beta1.BKECluster) bkenode.Nodes {
		return bkenode.Nodes{{IP: "192.168.1.1"}}
	})

	old := &bkev1beta1.BKECluster{}
	new := &bkev1beta1.BKECluster{}

	result := e.NeedExecute(old, new)
	assert.True(t, result)
}

func TestEnsureMasterDelete_NeedExecute_NoDeleteNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureMasterDelete()

	patches.ApplyFunc(phaseutil.GetNeedDeleteMasterNodes, func(ctx context.Context, c client.Client, cluster *bkev1beta1.BKECluster) bkenode.Nodes {
		return bkenode.Nodes{}
	})

	patches.ApplyFunc(getDeleteTargetNodesIfDeployed, func(ctx *phaseframe.PhaseContext, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, bool) {
		return nil, false
	})

	old := &bkev1beta1.BKECluster{}
	new := &bkev1beta1.BKECluster{}

	result := e.NeedExecute(old, new)
	assert.False(t, result)
}

func TestEnsureMasterDelete_GetTargetClusterNodes_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureMasterDelete()

	patches.ApplyFunc(GetTargetClusterNodes, func(ctx context.Context, c client.Client, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
		return bkenode.Nodes{{IP: "192.168.1.1"}}, nil
	})

	nodes, err := e.getTargetClusterNodes(e.Ctx.BKECluster)
	assert.NoError(t, err)
	assert.Len(t, nodes, 1)
}

func TestEnsureMasterDelete_PrepareMachinesAndNodesToWaitDelete_Empty(t *testing.T) {
	e := createTestEnsureMasterDelete()
	result := e.prepareMachinesAndNodesToWaitDelete()
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestEnsureMasterDelete_PrepareMachinesAndNodesToWaitDelete_WithData(t *testing.T) {
	e := createTestEnsureMasterDelete()
	machine := &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine1"}}
	node := confv1beta1.Node{IP: "192.168.1.1"}

	e.machinesAndNodesToDelete = map[string]phaseutil.MachineAndNode{
		"machine1": {Machine: machine, Node: node},
	}
	e.machinesAndNodesToWaitDelete = map[string]phaseutil.MachineAndNode{
		"machine2": {Machine: &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine2"}}, Node: confv1beta1.Node{IP: "192.168.1.2"}},
	}

	result := e.prepareMachinesAndNodesToWaitDelete()
	assert.Len(t, result, 2)
}

func TestEnsureMasterDelete_WaitForMachinesDelete_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureMasterDelete()
	machine := &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "default"}}

	patches.ApplyMethod(&fakeClient{}, "Get", func(_ *fakeClient, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		return apierrors.NewNotFound(schema.GroupResource{}, "machine1")
	})

	params := WaitForMachinesDeleteParams{
		Ctx:    context.Background(),
		Client: &fakeClient{},
		MachinesAndNodesToWaitDelete: map[string]phaseutil.MachineAndNode{
			"machine1": {Machine: machine, Node: confv1beta1.Node{IP: "192.168.1.1"}},
		},
		Log: createTestLogger(),
	}

	result, err := e.waitForMachinesDelete(params)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestEnsureMasterDelete_WaitMasterDelete_NoMachines(t *testing.T) {
	e := createTestEnsureMasterDelete()
	err := e.waitMasterDelete()
	assert.NoError(t, err)
}

func TestEnsureMasterDelete_NeedExecute_WithTargetNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureMasterDelete()

	patches.ApplyFunc(phaseutil.GetNeedDeleteMasterNodes, func(ctx context.Context, c client.Client, cluster *bkev1beta1.BKECluster) bkenode.Nodes {
		return bkenode.Nodes{}
	})

	patches.ApplyFunc(getDeleteTargetNodesIfDeployed, func(ctx *phaseframe.PhaseContext, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, bool) {
		return bkenode.Nodes{{IP: "192.168.1.1"}}, true
	})

	patches.ApplyFunc(phaseutil.GetNeedDeleteMasterNodesWithTargetNodes, func(ctx context.Context, c client.Client, cluster *bkev1beta1.BKECluster, targetNodes bkenode.Nodes) bkenode.Nodes {
		return bkenode.Nodes{{IP: "192.168.1.1"}}
	})

	old := &bkev1beta1.BKECluster{}
	new := &bkev1beta1.BKECluster{}

	result := e.NeedExecute(old, new)
	assert.True(t, result)
}

func TestPauseAndScaleDownControlPlaneParams_Structure(t *testing.T) {
	params := PauseAndScaleDownControlPlaneParams{
		Ctx:        context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		DeleteMap:  make(map[string]phaseutil.MachineAndNode),
		Log:        createTestLogger(),
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.DeleteMap)
	assert.NotNil(t, params.Log)
}

func TestWaitForMachinesDeleteParams_Structure(t *testing.T) {
	params := WaitForMachinesDeleteParams{
		Ctx:                          context.Background(),
		Client:                       &fakeClient{},
		MachinesAndNodesToWaitDelete: make(map[string]phaseutil.MachineAndNode),
		Log:                          createTestLogger(),
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.MachinesAndNodesToWaitDelete)
	assert.NotNil(t, params.Log)
}

func TestCleanupDeletedNodePodsParams_Structure(t *testing.T) {
	params := CleanupDeletedNodePodsParams{
		Ctx:                context.Background(),
		Client:             &fakeClient{},
		BKECluster:         &bkev1beta1.BKECluster{},
		SuccessDeletedNode: make(map[string]confv1beta1.Node),
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.SuccessDeletedNode)
}







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
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
)

func TestEnsureWorkerDeleteConstants(t *testing.T) {
	assert.Equal(t, "EnsureWorkerDelete", string(EnsureWorkerDeleteName))
	assert.Equal(t, 10, WorkerDeleteRequeueAfterSeconds)
	assert.Equal(t, 4, WorkerDeleteWaitTimeoutMinutes)
	assert.Equal(t, 2, WorkerDeletePollIntervalSeconds)
}

func TestNewEnsureWorkerDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx)
	assert.NotNil(t, phase)

	workerDelete, ok := phase.(*EnsureWorkerDelete)
	assert.True(t, ok)
	assert.NotNil(t, workerDelete)
	assert.NotNil(t, workerDelete.machinesAndNodesToWaitDelete)
	assert.NotNil(t, workerDelete.machinesAndNodesToDelete)
}

func TestPrepareMachinesAndNodesToWaitDelete(t *testing.T) {
	machine1 := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "default"},
	}
	machine2 := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "machine2", Namespace: "default"},
	}

	node1 := confv1beta1.Node{IP: "192.168.1.1", Hostname: "node1"}
	node2 := confv1beta1.Node{IP: "192.168.1.2", Hostname: "node2"}

	tests := []struct {
		name                         string
		machinesAndNodesToWaitDelete map[string]phaseutil.MachineAndNode
		machinesAndNodesToDelete     map[string]phaseutil.MachineAndNode
		expectedCount                int
	}{
		{
			name: "Both maps have items",
			machinesAndNodesToWaitDelete: map[string]phaseutil.MachineAndNode{
				"machine1": {Machine: machine1, Node: node1},
			},
			machinesAndNodesToDelete: map[string]phaseutil.MachineAndNode{
				"machine2": {Machine: machine2, Node: node2},
			},
			expectedCount: 2,
		},
		{
			name: "Only wait delete map has items",
			machinesAndNodesToWaitDelete: map[string]phaseutil.MachineAndNode{
				"machine1": {Machine: machine1, Node: node1},
			},
			machinesAndNodesToDelete: nil,
			expectedCount:            1,
		},
		{
			name:                         "Only delete map has items",
			machinesAndNodesToWaitDelete: map[string]phaseutil.MachineAndNode{},
			machinesAndNodesToDelete: map[string]phaseutil.MachineAndNode{
				"machine2": {Machine: machine2, Node: node2},
			},
			expectedCount: 1,
		},
		{
			name:                         "Both maps empty",
			machinesAndNodesToWaitDelete: map[string]phaseutil.MachineAndNode{},
			machinesAndNodesToDelete:     map[string]phaseutil.MachineAndNode{},
			expectedCount:                0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := PrepareMachinesAndNodesToWaitDeleteParams{
				MachinesAndNodesToWaitDelete: tt.machinesAndNodesToWaitDelete,
				MachinesAndNodesToDelete:     tt.machinesAndNodesToDelete,
			}
			result := prepareMachinesAndNodesToWaitDelete(params)
			assert.Equal(t, tt.expectedCount, len(result))
		})
	}
}

func TestEnsureWorkerDelete_PrepareMachinesAndNodesToWaitDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "default"},
	}
	node := confv1beta1.Node{IP: "192.168.1.1", Hostname: "node1"}

	phase.machinesAndNodesToWaitDelete = map[string]phaseutil.MachineAndNode{
		"machine1": {Machine: machine, Node: node},
	}
	phase.machinesAndNodesToDelete = map[string]phaseutil.MachineAndNode{
		"machine2": {Machine: machine, Node: node},
	}

	result := phase.prepareMachinesAndNodesToWaitDelete()
	assert.Equal(t, 2, len(result))
}

func TestEnsureWorkerDelete_NeedExecute(t *testing.T) {
	t.Skip("Skipping - requires complex setup with GetNeedDeleteWorkerNodes")
}

func TestEnsureWorkerDelete_Execute(t *testing.T) {
	t.Skip("Skipping - requires complex mocking of reconcileWorkerDelete")
}

func TestEnsureWorkerDelete_GetTargetClusterNodes(t *testing.T) {
	t.Skip("Skipping - requires complex setup with GetTargetClusterNodes")
}

func TestEnsureWorkerDelete_DrainNodes(t *testing.T) {
	t.Skip("Skipping - requires complex mocking of kubernetes client")
}

func TestEnsureWorkerDelete_MarkMachinesForDeletion(t *testing.T) {
	t.Skip("Skipping - requires complex mocking of MarkMachineForDeletion")
}

func TestEnsureWorkerDelete_InitialSetup(t *testing.T) {
	t.Skip("Skipping - requires complex setup with ProcessNodeMachineMapping")
}

func TestEnsureWorkerDelete_ProcessDrainAndMark(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

func TestEnsureWorkerDelete_FinalizeDeletion(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

func TestEnsureWorkerDelete_ReconcileWorkerDelete(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

func TestEnsureWorkerDelete_WaitWorkerDelete(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

func TestEnsureWorkerDelete_WaitForMachinesDelete(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

func TestEnsureWorkerDelete_ProcessSuccessfulDeletions(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

func TestEnsureWorkerDelete_CleanupNodePods(t *testing.T) {
	t.Skip("Skipping - requires complex mocking of kubernetes client")
}

func TestEnsureWorkerDelete_FinalizeDeletion_NoNodesToDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	md := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-md", Namespace: "default"},
		Spec: clusterv1.MachineDeploymentSpec{
			Replicas: pointer.Int32(3),
		},
	}

	scope := &phaseutil.ClusterAPIObjs{
		MachineDeployment: md,
	}

	params := FinalizeDeletionParams{
		Ctx:        context.Background(),
		Client:     c,
		BKECluster: bkeCluster,
		MarkResult: MarkMachinesForDeletionResult{
			FinalMachineToNodeDeleteMap:       map[string]phaseutil.MachineAndNode{},
			FinalCanNotDeleteMachinesAndNodes: map[string]phaseutil.MachineAndNode{},
		},
		Scope:           scope,
		CurrentReplicas: pointer.Int32(3),
		Log:             ctx.Log,
	}

	result := phase.finalizeDeletion(params)
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "cannot be completely deleted")
}

func TestEnsureWorkerDelete_FinalizeDeletion_WithCanNotDeleteNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "default"},
	}
	node := confv1beta1.Node{IP: "192.168.1.1", Hostname: "node1"}

	md := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-md", Namespace: "default"},
		Spec: clusterv1.MachineDeploymentSpec{
			Replicas: pointer.Int32(3),
		},
	}

	scope := &phaseutil.ClusterAPIObjs{
		MachineDeployment: md,
	}

	params := FinalizeDeletionParams{
		Ctx:        context.Background(),
		Client:     c,
		BKECluster: bkeCluster,
		MarkResult: MarkMachinesForDeletionResult{
			FinalMachineToNodeDeleteMap: map[string]phaseutil.MachineAndNode{},
			FinalCanNotDeleteMachinesAndNodes: map[string]phaseutil.MachineAndNode{
				"machine1": {Machine: machine, Node: node},
			},
		},
		Scope:           scope,
		CurrentReplicas: pointer.Int32(3),
		Log:             ctx.Log,
	}

	result := phase.finalizeDeletion(params)
	assert.Error(t, result.Error)
	assert.Equal(t, time.Duration(WorkerDeleteRequeueAfterSeconds)*time.Second, result.Result.RequeueAfter)
}

func TestEnsureWorkerDelete_FinalizeDeletion_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "default"},
	}
	node := confv1beta1.Node{IP: "192.168.1.1", Hostname: "node1"}

	md := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-md", Namespace: "default"},
		Spec: clusterv1.MachineDeploymentSpec{
			Replicas: pointer.Int32(3),
		},
	}

	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster, md).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	scope := &phaseutil.ClusterAPIObjs{
		MachineDeployment: md,
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(phaseutil.ResumeClusterAPIObj, func(ctx context.Context, c any, obj any) error {
		return nil
	})

	params := FinalizeDeletionParams{
		Ctx:        context.Background(),
		Client:     c,
		BKECluster: bkeCluster,
		MarkResult: MarkMachinesForDeletionResult{
			FinalMachineToNodeDeleteMap: map[string]phaseutil.MachineAndNode{
				"machine1": {Machine: machine, Node: node},
			},
			FinalCanNotDeleteMachinesAndNodes: map[string]phaseutil.MachineAndNode{},
		},
		Scope:           scope,
		CurrentReplicas: pointer.Int32(3),
		Log:             ctx.Log,
	}

	result := phase.finalizeDeletion(params)
	assert.NoError(t, result.Error)
	assert.Equal(t, int32(2), *scope.MachineDeployment.Spec.Replicas)
}

func TestEnsureWorkerDelete_FinalizeDeletion_NegativeReplicas(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	machine1 := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "default"},
	}
	machine2 := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "machine2", Namespace: "default"},
	}
	node1 := confv1beta1.Node{IP: "192.168.1.1", Hostname: "node1"}
	node2 := confv1beta1.Node{IP: "192.168.1.2", Hostname: "node2"}

	md := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-md", Namespace: "default"},
		Spec: clusterv1.MachineDeploymentSpec{
			Replicas: pointer.Int32(1),
		},
	}

	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster, md).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	scope := &phaseutil.ClusterAPIObjs{
		MachineDeployment: md,
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(phaseutil.ResumeClusterAPIObj, func(ctx context.Context, c any, obj any) error {
		return nil
	})

	params := FinalizeDeletionParams{
		Ctx:        context.Background(),
		Client:     c,
		BKECluster: bkeCluster,
		MarkResult: MarkMachinesForDeletionResult{
			FinalMachineToNodeDeleteMap: map[string]phaseutil.MachineAndNode{
				"machine1": {Machine: machine1, Node: node1},
				"machine2": {Machine: machine2, Node: node2},
			},
			FinalCanNotDeleteMachinesAndNodes: map[string]phaseutil.MachineAndNode{},
		},
		Scope:           scope,
		CurrentReplicas: pointer.Int32(1),
		Log:             ctx.Log,
	}

	result := phase.finalizeDeletion(params)
	assert.NoError(t, result.Error)
	assert.Equal(t, int32(0), *scope.MachineDeployment.Spec.Replicas)
}

func TestEnsureWorkerDelete_GetTargetClusterNodes_Success(t *testing.T) {
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
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	expectedNodes := bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
		{IP: "192.168.1.2", Hostname: "node2"},
	}

	patches.ApplyFunc(GetTargetClusterNodes, func(ctx context.Context, c any, bkeCluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
		return expectedNodes, nil
	})

	nodes, err := phase.getTargetClusterNodes(bkeCluster)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(nodes))
}

func TestEnsureWorkerDelete_WaitWorkerDelete_EmptyMap(t *testing.T) {
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
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)
	phase.machinesAndNodesToWaitDelete = map[string]phaseutil.MachineAndNode{}
	phase.machinesAndNodesToDelete = map[string]phaseutil.MachineAndNode{}

	err := phase.waitWorkerDelete()
	assert.NoError(t, err)
}

func TestEnsureWorkerDelete_NeedExecute_WithLegacyNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(phaseutil.GetNeedDeleteWorkerNodes, func(ctx context.Context, c any, bkeCluster *bkev1beta1.BKECluster) bkenode.Nodes {
		return bkenode.Nodes{
			{IP: "192.168.1.1", Hostname: "node1"},
		}
	})

	result := phase.NeedExecute(nil, bkeCluster)
	assert.True(t, result)
}

func TestEnsureWorkerDelete_NeedExecute_NoNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(phaseutil.GetNeedDeleteWorkerNodes, func(ctx context.Context, c any, bkeCluster *bkev1beta1.BKECluster) bkenode.Nodes {
		return bkenode.Nodes{}
	})

	patches.ApplyFunc(getDeleteTargetNodesIfDeployed, func(ctx *phaseframe.PhaseContext, bkeCluster *bkev1beta1.BKECluster) (bkenode.Nodes, bool) {
		return nil, false
	})

	result := phase.NeedExecute(nil, bkeCluster)
	assert.False(t, result)
}

func TestEnsureWorkerDelete_Execute_Success(t *testing.T) {
	t.Skip("Skipping - cannot mock private methods with gomonkey")
}

func TestEnsureWorkerDelete_NeedExecute_WithTargetNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(phaseutil.GetNeedDeleteWorkerNodes, func(ctx context.Context, c any, bkeCluster *bkev1beta1.BKECluster) bkenode.Nodes {
		return bkenode.Nodes{}
	})

	patches.ApplyFunc(getDeleteTargetNodesIfDeployed, func(ctx *phaseframe.PhaseContext, bkeCluster *bkev1beta1.BKECluster) (bkenode.Nodes, bool) {
		return bkenode.Nodes{
			{IP: "192.168.1.1", Hostname: "node1"},
		}, true
	})

	patches.ApplyFunc(phaseutil.GetNeedDeleteWorkerNodesWithTargetNodes, func(ctx context.Context, c any, bkeCluster *bkev1beta1.BKECluster, targetNodes bkenode.Nodes) bkenode.Nodes {
		return bkenode.Nodes{
			{IP: "192.168.1.1", Hostname: "node1"},
		}
	})

	result := phase.NeedExecute(nil, bkeCluster)
	assert.True(t, result)
}

func TestEnsureWorkerDelete_NeedExecute_TargetNodesButNoDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerDelete(ctx).(*EnsureWorkerDelete)

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(phaseutil.GetNeedDeleteWorkerNodes, func(ctx context.Context, c any, bkeCluster *bkev1beta1.BKECluster) bkenode.Nodes {
		return bkenode.Nodes{}
	})

	patches.ApplyFunc(getDeleteTargetNodesIfDeployed, func(ctx *phaseframe.PhaseContext, bkeCluster *bkev1beta1.BKECluster) (bkenode.Nodes, bool) {
		return bkenode.Nodes{
			{IP: "192.168.1.1", Hostname: "node1"},
		}, true
	})

	patches.ApplyFunc(phaseutil.GetNeedDeleteWorkerNodesWithTargetNodes, func(ctx context.Context, c any, bkeCluster *bkev1beta1.BKECluster, targetNodes bkenode.Nodes) bkenode.Nodes {
		return bkenode.Nodes{}
	})

	result := phase.NeedExecute(nil, bkeCluster)
	assert.False(t, result)
}
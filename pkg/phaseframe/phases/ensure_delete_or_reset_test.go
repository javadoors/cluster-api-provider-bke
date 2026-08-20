/******************************************************************
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package phases

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/testutils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnsureDeleteOrResetConstants(t *testing.T) {
	assert.Equal(t, "EnsureDeleteOrReset", string(EnsureDeleteOrResetName))
}

func TestNewEnsureDeleteOrReset(t *testing.T) {
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
	phase := NewEnsureDeleteOrReset(ctx)
	assert.NotNil(t, phase)

	e, ok := phase.(*EnsureDeleteOrReset)
	assert.True(t, ok)
	assert.Equal(t, EnsureDeleteOrResetName, e.PhaseName)
}

func TestEnsureDeleteOrReset_NeedExecute_DeletionTimestamp(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test",
			Namespace:         "default",
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
		},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureDeleteOrResetName}}

	result := e.NeedExecute(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.True(t, result)
}

func TestEnsureDeleteOrReset_NeedExecute_Reset(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       confv1beta1.BKEClusterSpec{Reset: true},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureDeleteOrResetName}}

	result := e.NeedExecute(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.True(t, result)
}

func TestEnsureDeleteOrReset_NeedExecute_False(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureDeleteOrResetName}}

	result := e.NeedExecute(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestCreateShutdownAgentCommandSpec(t *testing.T) {
	spec := createShutdownAgentCommandSpec()
	assert.NotNil(t, spec)
	assert.Len(t, spec.Commands, 1)
	assert.Equal(t, "Shutdown agent", spec.Commands[0].ID)
}

func TestEnsureDeleteOrResetPostHook_Success(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	phase := &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	err := ensureDeleteOrResetPostHook(phase, nil)
	assert.NoError(t, err)
}

func TestEnsureDeleteOrResetPostHook_WithError(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	phase := &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	err := ensureDeleteOrResetPostHook(phase, assert.AnError)
	assert.NoError(t, err)
}

func TestEnsureDeleteOrReset_Execute_ReconcileSuccess(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "reconcileDelete", func(_ *EnsureDeleteOrReset, _ context.Context) error {
		return nil
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureDeleteOrReset_EnsureClusterStatusDeleting_AlreadyDeleting(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status:     confv1beta1.BKEClusterStatus{ClusterStatus: bkev1beta1.ClusterDeleting},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	err := e.ensureClusterStatusDeleting(c, bkeCluster, ctx.Log)
	assert.NoError(t, err)
}

func TestEnsureDeleteOrReset_HandleClusterDeletion_NoCluster(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Cluster:    nil,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	err := e.handleClusterDeletion(context.Background(), nil, ctx.Log)
	assert.NoError(t, err)
}

func TestEnsureDeleteOrReset_ShutdownAgentOnNodesWithParams_EmptyNodes(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	params := ShutdownAgentOnNodesParams{
		Ctx:        context.Background(),
		BKECluster: bkeCluster,
		Nodes:      nil,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	ShutdownAgentOnNodesWithParams(params)
}

func TestEnsureDeleteOrReset_CreateShutdownAgentCommandSpec(t *testing.T) {
	spec := createShutdownAgentCommandSpec()
	assert.NotNil(t, spec)
	assert.Len(t, spec.Commands, 1)
	assert.Equal(t, "Shutdown agent", spec.Commands[0].ID)
	assert.Equal(t, "Shutdown", spec.Commands[0].Command[0])
}

func TestEnsureDeleteOrReset_ShutdownAgentOnNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	patches.ApplyFunc(ShutdownAgentOnNodesWithParams, func(_ ShutdownAgentOnNodesParams) {})

	ShutdownAgentOnNodes(context.Background(), nil, bkeCluster, nil, nil, bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster))
}

func TestEnsureDeleteOrReset_ShutdownAgentOnSingleNode(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	node := confv1beta1.Node{IP: "192.168.1.1"}

	patches.ApplyFunc(ShutdownAgentOnSingleNodeWithParams, func(_ ShutdownAgentOnSingleNodeParams) error {
		return nil
	})

	err := ShutdownAgentOnSingleNode(context.Background(), nil, bkeCluster, nil, node, bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster))
	assert.NoError(t, err)
}

func TestEnsureDeleteOrReset_ShutdownAgentOnSingleNodeWithParams_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	node := confv1beta1.Node{IP: "192.168.1.1"}

	params := ShutdownAgentOnSingleNodeParams{
		Ctx:        context.Background(),
		BKECluster: bkeCluster,
		Node:       node,
	}

	patches.ApplyFunc(ShutdownAgentOnSingleNodeWithParams, func(_ ShutdownAgentOnSingleNodeParams) error {
		return nil
	})

	err := ShutdownAgentOnSingleNodeWithParams(params)
	assert.NoError(t, err)
}

func deleteScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, agentv1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func newDeletePhaseCov(t *testing.T, bkeCluster *bkev1beta1.BKECluster, objs ...client.Object) *EnsureDeleteOrReset {
	t.Helper()
	scheme := deleteScheme(t)
	builder := ctrlfake.NewClientBuilder().WithScheme(scheme)
	if bkeCluster != nil {
		builder = builder.WithObjects(bkeCluster)
	}
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	c := builder.Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	return &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx}}
}

// ---- handleBKEMachineDeletion (real body) ----

func TestEnsureDeleteOrResetHandleBKEMachineDeletion(t *testing.T) {
	t.Run("no bkeMachines returns nil", func(t *testing.T) {
		e := newDeletePhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}})
		err := e.handleBKEMachineDeletion(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.NoError(t, err)
	})

	t.Run("unbootstrapped machine with owner waits for delete", func(t *testing.T) {
		machine := &bkev1beta1.BKEMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{Kind: "KubeadmControlPlane"}}},
			Status:     bkev1beta1.BKEMachineStatus{Bootstrapped: false},
		}
		e := newDeletePhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}, machine)
		err := e.handleBKEMachineDeletion(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wait for bkeMachine delete")
	})

	t.Run("bootstrapped machine with owner waits for delete", func(t *testing.T) {
		machine := &bkev1beta1.BKEMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{Kind: "KubeadmControlPlane"}}},
			Status:     bkev1beta1.BKEMachineStatus{Bootstrapped: true},
		}
		e := newDeletePhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}, machine)
		err := e.handleBKEMachineDeletion(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wait for bkeMachine delete")
	})
}

// ---- deleteRelatedResources (real body) ----

func TestEnsureDeleteOrResetDeleteRelatedResources(t *testing.T) {
	t.Run("empty resources returns nil", func(t *testing.T) {
		e := newDeletePhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}})
		require.NoError(t, e.deleteRelatedResources(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log))
	})

	t.Run("with commands deletes them", func(t *testing.T) {
		cmd := &agentv1beta1.Command{
			ObjectMeta: metav1.ObjectMeta{Name: "cmd1", Namespace: "ns"},
		}
		e := newDeletePhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}, cmd)
		require.NoError(t, e.deleteRelatedResources(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log))
	})
}

// ---- cleanupClusterResources (real body) ----

func TestEnsureDeleteOrResetCleanupClusterResources(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error { return nil })

	t.Run("success removes finalizer", func(t *testing.T) {
		cluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "c1", Namespace: "ns",
				Finalizers: []string{bkev1beta1.ClusterFinalizer},
			},
		}
		e := newDeletePhaseCov(t, cluster)
		require.NoError(t, e.cleanupClusterResources(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log))
		assert.Empty(t, e.Ctx.BKECluster.Finalizers)
	})
}

// ---- handleNamespaceDeletion (real body) ----

func TestEnsureDeleteOrResetHandleNamespaceDeletion(t *testing.T) {
	t.Run("annotation false with no other cluster deletes namespace", func(t *testing.T) {
		cluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "c1", Namespace: "ns",
				Annotations: map[string]string{annotation.DeleteIgnoreNamespaceAnnotationKey: "false"},
			},
		}
		e := newDeletePhaseCov(t, cluster)
		require.NoError(t, e.handleNamespaceDeletion(context.Background(), e.Ctx.Client, cluster, e.Ctx.Log))
	})

	t.Run("annotation true ignores", func(t *testing.T) {
		cluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "c1", Namespace: "ns",
				Annotations: map[string]string{annotation.DeleteIgnoreNamespaceAnnotationKey: "true"},
			},
		}
		e := newDeletePhaseCov(t, cluster)
		require.NoError(t, e.handleNamespaceDeletion(context.Background(), e.Ctx.Client, cluster, e.Ctx.Log))
	})

	t.Run("no annotation ignores", func(t *testing.T) {
		cluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		e := newDeletePhaseCov(t, cluster)
		require.NoError(t, e.handleNamespaceDeletion(context.Background(), e.Ctx.Client, cluster, e.Ctx.Log))
	})
}

// ---- ShutDownAgent (real body via NodeFetcher + ShutdownAgentOnNodesWithParams seams) ----

func TestEnsureDeleteOrResetShutDownAgent(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	e := newDeletePhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}})
	patches.ApplyFunc((*nodeutil.NodeFetcher).FetchNodesForCluster, func(_ *nodeutil.NodeFetcher, _ context.Context, _, _ string) (*nodeutil.FetchResult, error) {
		return &nodeutil.FetchResult{Nodes: bkenode.Nodes{{IP: "10.0.0.1", Hostname: "node1"}}}, nil
	})
	patches.ApplyFunc((*nodeutil.NodeFetcher).GetNodeStateFlag, func(_ *nodeutil.NodeFetcher, _ context.Context, _, _, _ string, _ int) (bool, error) {
		return true, nil
	})
	called := false
	patches.ApplyFunc(ShutdownAgentOnNodesWithParams, func(_ ShutdownAgentOnNodesParams) { called = true })
	assert.NotPanics(t, func() { e.ShutDownAgent(context.Background()) })
	assert.True(t, called)
}

// ---- reconcileDelete (orchestration) ----

func TestEnsureDeleteOrResetReconcileDelete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		e := newDeletePhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}})
		patches.ApplyMethod(&phaseframe.PhaseContext{}, "RefreshCtxCluster", func(_ *phaseframe.PhaseContext, _ ...context.Context) error { return nil })
		patches.ApplyPrivateMethod(e, "ensureClusterStatusDeleting", func(_ *EnsureDeleteOrReset, _ client.Client, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKELogger) error {
			return nil
		})
		patches.ApplyPrivateMethod(e, "handleClusterDeletion", func(_ *EnsureDeleteOrReset, _ context.Context, _ client.Client, _ *bkev1beta1.BKELogger) error {
			return nil
		})
		patches.ApplyPrivateMethod(e, "handleBKEMachineDeletion", func(_ *EnsureDeleteOrReset, _ context.Context, _ client.Client, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKELogger) error {
			return nil
		})
		patches.ApplyPrivateMethod(e, "deleteRelatedResources", func(_ *EnsureDeleteOrReset, _ context.Context, _ client.Client, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKELogger) error {
			return nil
		})
		patches.ApplyPrivateMethod(e, "ShutDownAgent", func(_ *EnsureDeleteOrReset, _ context.Context) {})
		patches.ApplyPrivateMethod(e, "cleanupClusterResources", func(_ *EnsureDeleteOrReset, _ context.Context, _ client.Client, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKELogger) error {
			return nil
		})
		patches.ApplyPrivateMethod(e, "handleNamespaceDeletion", func(_ *EnsureDeleteOrReset, _ context.Context, _ client.Client, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKELogger) error {
			return nil
		})
		require.NoError(t, e.reconcileDelete(context.Background()))
	})

	t.Run("bke machine deletion error propagates", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		e := newDeletePhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}})
		patches.ApplyMethod(&phaseframe.PhaseContext{}, "RefreshCtxCluster", func(_ *phaseframe.PhaseContext, _ ...context.Context) error { return nil })
		patches.ApplyPrivateMethod(e, "ensureClusterStatusDeleting", func(_ *EnsureDeleteOrReset, _ client.Client, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKELogger) error {
			return nil
		})
		patches.ApplyPrivateMethod(e, "handleClusterDeletion", func(_ *EnsureDeleteOrReset, _ context.Context, _ client.Client, _ *bkev1beta1.BKELogger) error {
			return nil
		})
		patches.ApplyPrivateMethod(e, "handleBKEMachineDeletion", func(_ *EnsureDeleteOrReset, _ context.Context, _ client.Client, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKELogger) error {
			return assertErr("machine delete failed")
		})
		err := e.reconcileDelete(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "machine delete failed")
	})
}

// ---- helpers ----

func deleteOrResetGapsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, bkev1beta1.AddToScheme(scheme))
	require.NoError(t, agentv1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, clusterv1.AddToScheme(scheme))
	return scheme
}

func deleteOrResetGapsPhaseCov(t *testing.T, bkeCluster *bkev1beta1.BKECluster, objs ...client.Object) *EnsureDeleteOrReset {
	t.Helper()
	scheme := deleteOrResetGapsScheme(t)
	builder := ctrlfake.NewClientBuilder().WithScheme(scheme)
	if bkeCluster != nil {
		builder = builder.WithObjects(bkeCluster)
	}
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	c := builder.Build()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	return &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx}}
}

// deleteOrResetGapsPhaseWithClient builds a phase whose client is wrapped.
func deleteOrResetGapsPhaseWithClient(t *testing.T, c client.Client, bkeCluster *bkev1beta1.BKECluster) *EnsureDeleteOrReset {
	t.Helper()
	scheme := deleteOrResetGapsScheme(t)
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	return &EnsureDeleteOrReset{BasePhase: phaseframe.BasePhase{Ctx: ctx}}
}

// deleteOrResetGapsForbiddenOnBKENodeClient returns Forbidden on DeleteAllOf for BKENode.
type deleteOrResetGapsForbiddenOnBKENodeClient struct {
	client.Client
}

func (f *deleteOrResetGapsForbiddenOnBKENodeClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	if _, ok := obj.(*confv1beta1.BKENode); ok {
		return apierrors.NewForbidden(schema.GroupResource{Group: "bke.bocloud.com", Resource: "bkenodes"}, "", errors.New("forbidden"))
	}
	return f.Client.DeleteAllOf(ctx, obj, opts...)
}

// deleteOrResetGapsListErrClient returns a configurable error on all List calls when listErr is set.
type deleteOrResetGapsListErrClient struct {
	client.Client
	listErr error
}

func (f *deleteOrResetGapsListErrClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if f.listErr != nil {
		return f.listErr
	}
	return f.Client.List(ctx, list, opts...)
}

// deleteOrResetGapsDeleteErrClient returns an error on Delete for clusterv1.Cluster.
type deleteOrResetGapsDeleteErrClient struct {
	client.Client
}

func (f *deleteOrResetGapsDeleteErrClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if _, ok := obj.(*clusterv1.Cluster); ok {
		return apierrors.NewInternalError(errors.New("delete error"))
	}
	return f.Client.Delete(ctx, obj, opts...)
}

// ---- Execute ----

func TestEnsureDeleteOrResetExecuteGaps(t *testing.T) {
	t.Run("paused resumes cluster and removes commands", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		cmd := &agentv1beta1.Command{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "cmd1",
				Namespace:  "ns",
				Labels:     map[string]string{clusterv1.ClusterNameLabel: "c1"},
				Finalizers: []string{"command.bkeagent.bocloud.com/finalizers"},
			},
		}
		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Spec:       confv1beta1.BKEClusterSpec{Pause: true},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster, cmd)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error { return nil })
		patches.ApplyMethod(&phaseframe.PhaseContext{}, "RefreshCtxBKECluster", func(_ *phaseframe.PhaseContext, _ ...context.Context) error { return nil })

		result, err := e.Execute()
		require.NoError(t, err)
		assert.True(t, result.Requeue)
	})

	t.Run("paused list error still returns requeue", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Spec:       confv1beta1.BKEClusterSpec{Pause: true},
		}
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
		wrappedC := &deleteOrResetGapsListErrClient{Client: fakeC, listErr: errors.New("list boom")}
		e := deleteOrResetGapsPhaseWithClient(t, wrappedC, bkeCluster)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error { return nil })
		patches.ApplyMethod(&phaseframe.PhaseContext{}, "RefreshCtxBKECluster", func(_ *phaseframe.PhaseContext, _ ...context.Context) error { return nil })

		result, err := e.Execute()
		require.NoError(t, err)
		assert.True(t, result.Requeue)
	})

	t.Run("paused SyncStatusUntilComplete error", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Spec:       confv1beta1.BKEClusterSpec{Pause: true},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error {
			return errors.New("sync error")
		})

		result, err := e.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sync error")
		assert.False(t, result.Requeue)
	})

	t.Run("paused RefreshCtxBKECluster error", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Spec:       confv1beta1.BKEClusterSpec{Pause: true},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error { return nil })
		patches.ApplyMethod(&phaseframe.PhaseContext{}, "RefreshCtxBKECluster", func(_ *phaseframe.PhaseContext, _ ...context.Context) error { return errors.New("refresh error") })

		result, err := e.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refresh error")
		assert.False(t, result.Requeue)
	})

	t.Run("not paused reconcileDelete success", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		patches.ApplyPrivateMethod(e, "reconcileDelete", func(_ *EnsureDeleteOrReset, _ context.Context) error { return nil })

		result, err := e.Execute()
		require.NoError(t, err)
		assert.False(t, result.Requeue)
	})

	t.Run("not paused reconcileDelete timeout", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		patches.ApplyFunc(wait.PollImmediateUntil, func(_ time.Duration, _ wait.ConditionFunc, _ <-chan struct{}) error {
			return wait.ErrWaitTimeout
		})

		result, err := e.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Wait delete timeout")
		assert.False(t, result.Requeue)
	})
}

// ---- handleClusterDeletion ----

func TestEnsureDeleteOrResetHandleClusterDeletionGaps(t *testing.T) {
	t.Run("cluster not deleting delete succeeds", func(t *testing.T) {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Status:     clusterv1.ClusterStatus{Phase: string(clusterv1.ClusterPhaseProvisioned)},
		}
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster, cluster)
		e.Ctx.Cluster = cluster

		err := e.handleClusterDeletion(context.Background(), e.Ctx.Client, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wait for the deletion of cluster api obj")
	})

	t.Run("cluster not deleting delete fails", func(t *testing.T) {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Status:     clusterv1.ClusterStatus{Phase: string(clusterv1.ClusterPhaseProvisioned)},
		}
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster, cluster).Build()
		wrappedC := &deleteOrResetGapsDeleteErrClient{Client: fakeC}
		e := deleteOrResetGapsPhaseWithClient(t, wrappedC, bkeCluster)
		e.Ctx.Cluster = cluster

		err := e.handleClusterDeletion(context.Background(), e.Ctx.Client, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete cluster")
	})

	t.Run("cluster already deleting returns nil", func(t *testing.T) {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Status:     clusterv1.ClusterStatus{Phase: string(clusterv1.ClusterPhaseDeleting)},
		}
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)
		e.Ctx.Cluster = cluster

		err := e.handleClusterDeletion(context.Background(), e.Ctx.Client, e.Ctx.Log)
		require.NoError(t, err)
	})

	t.Run("cluster nil returns nil", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		err := e.handleClusterDeletion(context.Background(), e.Ctx.Client, e.Ctx.Log)
		require.NoError(t, err)
	})
}

// ---- handleBKEMachineDeletion ----

func TestEnsureDeleteOrResetHandleBKEMachineDeletionGaps(t *testing.T) {
	t.Run("bootstrapped machine no owner removes finalizer", func(t *testing.T) {
		machine := &bkev1beta1.BKEMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "m1",
				Namespace:  "ns",
				Finalizers: []string{bkev1beta1.BKEMachineFinalizer},
			},
			Status: bkev1beta1.BKEMachineStatus{Bootstrapped: true},
		}
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster, machine)

		err := e.handleBKEMachineDeletion(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wait for bkeMachine delete")
	})

	t.Run("list error returns error", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
		wrappedC := &deleteOrResetGapsListErrClient{Client: fakeC, listErr: errors.New("list boom")}
		e := deleteOrResetGapsPhaseWithClient(t, wrappedC, bkeCluster)

		err := e.handleBKEMachineDeletion(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list bkeMachine")
	})

	t.Run("list not found returns error", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
		notFoundErr := apierrors.NewNotFound(schema.GroupResource{Group: "bke.bocloud.com", Resource: "bkemachines"}, "")
		wrappedC := &deleteOrResetGapsListErrClient{Client: fakeC, listErr: notFoundErr}
		e := deleteOrResetGapsPhaseWithClient(t, wrappedC, bkeCluster)

		err := e.handleBKEMachineDeletion(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list bkeMachine")
	})

	t.Run("unbootstrapped machine no owner delete then remove finalizer fails", func(t *testing.T) {
		machine := &bkev1beta1.BKEMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "m1",
				Namespace:  "ns",
				Finalizers: []string{bkev1beta1.BKEMachineFinalizer},
			},
			Status: bkev1beta1.BKEMachineStatus{Bootstrapped: false},
		}
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster, machine)

		err := e.handleBKEMachineDeletion(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
	})
}

// ---- cleanupClusterResources ----

func TestEnsureDeleteOrResetCleanupClusterResourcesGaps(t *testing.T) {
	t.Run("forbidden fallback deletes individually", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "c1",
				Namespace:  "ns",
				Finalizers: []string{bkev1beta1.ClusterFinalizer},
			},
		}
		bkeNode := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node1",
				Namespace: "ns",
				Labels:    map[string]string{clusterv1.ClusterNameLabel: "c1"},
			},
		}
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster, bkeNode).Build()
		wrappedC := &deleteOrResetGapsForbiddenOnBKENodeClient{Client: fakeC}
		e := deleteOrResetGapsPhaseWithClient(t, wrappedC, bkeCluster)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error { return nil })

		err := e.cleanupClusterResources(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.NoError(t, err)
		assert.Empty(t, e.Ctx.BKECluster.Finalizers)
	})

	t.Run("forbidden fallback list error", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "c1",
				Namespace:  "ns",
				Finalizers: []string{bkev1beta1.ClusterFinalizer},
			},
		}
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
		// Wrap to make DeleteAllOf forbidden AND List fail for BKENodeList
		wrappedC := &deleteOrResetGapsForbiddenAndListErrClient{
			Client:         fakeC,
			bkeNodeListErr: errors.New("list boom"),
		}
		e := deleteOrResetGapsPhaseWithClient(t, wrappedC, bkeCluster)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error { return nil })

		err := e.cleanupClusterResources(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list boom")
	})

	t.Run("SyncStatusUntilComplete error", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "c1",
				Namespace:  "ns",
				Finalizers: []string{bkev1beta1.ClusterFinalizer},
			},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error {
			return errors.New("sync status error")
		})

		err := e.cleanupClusterResources(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update bkeCluster Status")
	})
}

// deleteOrResetGapsForbiddenAndListErrClient makes DeleteAllOf forbidden for BKENode
// and optionally returns an error on List for BKENodeList.
type deleteOrResetGapsForbiddenAndListErrClient struct {
	client.Client
	bkeNodeListErr error
}

func (f *deleteOrResetGapsForbiddenAndListErrClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	if _, ok := obj.(*confv1beta1.BKENode); ok {
		return apierrors.NewForbidden(schema.GroupResource{Group: "bke.bocloud.com", Resource: "bkenodes"}, "", errors.New("forbidden"))
	}
	return f.Client.DeleteAllOf(ctx, obj, opts...)
}

func (f *deleteOrResetGapsForbiddenAndListErrClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*confv1beta1.BKENodeList); ok && f.bkeNodeListErr != nil {
		return f.bkeNodeListErr
	}
	return f.Client.List(ctx, list, opts...)
}

// ---- ensureClusterStatusDeleting ----

func TestEnsureDeleteOrResetEnsureClusterStatusDeletingGaps(t *testing.T) {
	t.Run("not deleting marks deleting success", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error { return nil })

		err := e.ensureClusterStatusDeleting(e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.NoError(t, err)
		assert.Equal(t, bkev1beta1.ClusterDeleting, e.Ctx.BKECluster.Status.ClusterStatus)
	})

	t.Run("not deleting SyncStatus error", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(client.Client, *bkev1beta1.BKECluster, ...mergecluster.PatchFunc) error {
			return errors.New("sync error")
		})

		err := e.ensureClusterStatusDeleting(e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update bkeCluster Status")
	})

	t.Run("already deleting returns nil", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Status:     confv1beta1.BKEClusterStatus{ClusterStatus: bkev1beta1.ClusterDeleting},
		}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)

		err := e.ensureClusterStatusDeleting(e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.NoError(t, err)
	})
}

// ---- ShutdownAgentOnSingleNodeWithParams ----

func TestShutdownAgentOnSingleNodeWithParamsGaps(t *testing.T) {
	t.Run("new error returns err", func(t *testing.T) {
		params := ShutdownAgentOnSingleNodeParams{
			Ctx:        context.Background(),
			Client:     nil,
			BKECluster: &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}},
			Scheme:     deleteOrResetGapsScheme(t),
			Node:       confv1beta1.Node{IP: "10.0.0.1", Hostname: "node1"},
			Log:        testutils.NewLog(),
		}

		err := ShutdownAgentOnSingleNodeWithParams(params)
		require.Error(t, err)
	})

	t.Run("success creates and waits", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		scheme := deleteOrResetGapsScheme(t)
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

		params := ShutdownAgentOnSingleNodeParams{
			Ctx:        context.Background(),
			Client:     fakeC,
			BKECluster: bkeCluster,
			Scheme:     scheme,
			Node:       confv1beta1.Node{IP: "10.0.0.1", Hostname: "node1"},
			Log:        testutils.NewLog(),
		}

		patches.ApplyMethod(&command.Custom{}, "Wait", func(_ *command.Custom) (error, []string, []string) {
			return nil, nil, nil
		})

		err := ShutdownAgentOnSingleNodeWithParams(params)
		require.NoError(t, err)
	})
}

// ---- ShutdownAgentOnNodesWithParams ----

func TestShutdownAgentOnNodesWithParamsGaps(t *testing.T) {
	t.Run("empty nodes returns immediately", func(t *testing.T) {
		params := ShutdownAgentOnNodesParams{
			Ctx:        context.Background(),
			Client:     nil,
			BKECluster: &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}},
			Scheme:     deleteOrResetGapsScheme(t),
			Nodes:      bkenode.Nodes{},
			Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}),
		}
		assert.NotPanics(t, func() { ShutdownAgentOnNodesWithParams(params) })
	})

	t.Run("non empty nodes creates and waits", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		scheme := deleteOrResetGapsScheme(t)
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

		params := ShutdownAgentOnNodesParams{
			Ctx:        context.Background(),
			Client:     fakeC,
			BKECluster: bkeCluster,
			Scheme:     scheme,
			Nodes:      bkenode.Nodes{{IP: "10.0.0.1", Hostname: "node1"}},
			Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
		}

		patches.ApplyMethod(&command.Custom{}, "Wait", func(_ *command.Custom) (error, []string, []string) {
			return nil, nil, nil
		})

		assert.NotPanics(t, func() { ShutdownAgentOnNodesWithParams(params) })
	})
}

// ---- NeedExecute ----

func TestEnsureDeleteOrResetNeedExecuteGaps(t *testing.T) {
	t.Run("deletion timestamp set returns true", func(t *testing.T) {
		e := deleteOrResetGapsPhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}})
		now := metav1.Now()
		newCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns", DeletionTimestamp: &now},
		}
		assert.True(t, e.NeedExecute(nil, newCluster))
	})

	t.Run("spec reset returns true", func(t *testing.T) {
		e := deleteOrResetGapsPhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}})
		newCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
			Spec:       confv1beta1.BKEClusterSpec{Reset: true},
		}
		assert.True(t, e.NeedExecute(nil, newCluster))
	})

	t.Run("neither returns false", func(t *testing.T) {
		e := deleteOrResetGapsPhaseCov(t, &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}})
		newCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
		}
		assert.False(t, e.NeedExecute(nil, newCluster))
	})
}

// ---- NewEnsureDeleteOrReset ----

func TestNewEnsureDeleteOrResetGaps(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
	e := deleteOrResetGapsPhaseCov(t, bkeCluster)
	p := NewEnsureDeleteOrReset(e.Ctx)
	assert.NotNil(t, p)
}

// ---- ensureDeleteOrResetPostHook ----

func TestEnsureDeleteOrResetPostHookGaps(t *testing.T) {
	t.Run("nil err unregisters metrics", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)
		err := ensureDeleteOrResetPostHook(e, nil)
		require.NoError(t, err)
	})

	t.Run("non nil err skips unregister", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		e := deleteOrResetGapsPhaseCov(t, bkeCluster)
		err := ensureDeleteOrResetPostHook(e, errors.New("some error"))
		require.NoError(t, err)
	})
}

// ---- ShutdownAgentOnNodes (thin wrapper) ----

func TestShutdownAgentOnNodesGaps(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	called := false
	patches.ApplyFunc(ShutdownAgentOnNodesWithParams, func(_ ShutdownAgentOnNodesParams) {
		called = true
	})

	bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
	ShutdownAgentOnNodes(context.Background(), nil, bkeCluster, deleteOrResetGapsScheme(t),
		bkenode.Nodes{{IP: "10.0.0.1", Hostname: "node1"}},
		bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster))
	assert.True(t, called)
}

// ---- ShutdownAgentOnSingleNode (thin wrapper) ----

func TestShutdownAgentOnSingleNodeGaps(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(ShutdownAgentOnSingleNodeWithParams, func(_ ShutdownAgentOnSingleNodeParams) error {
		return nil
	})

	bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
	err := ShutdownAgentOnSingleNode(context.Background(), nil, bkeCluster,
		deleteOrResetGapsScheme(t), confv1beta1.Node{IP: "10.0.0.1"},
		bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster))
	require.NoError(t, err)
}

// ---- handleNamespaceDeletion (additional branches) ----

func TestEnsureDeleteOrResetHandleNamespaceDeletionGaps(t *testing.T) {
	t.Run("annotation false empty cluster list deletes namespace", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "c1",
				Namespace:   "ns",
				Annotations: map[string]string{annotation.DeleteIgnoreNamespaceAnnotationKey: "false"},
			},
		}
		// BKECluster not in fake client so List returns empty
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).Build()
		e := deleteOrResetGapsPhaseWithClient(t, fakeC, bkeCluster)

		err := e.handleNamespaceDeletion(context.Background(), e.Ctx.Client, bkeCluster, e.Ctx.Log)
		require.NoError(t, err)
	})

	t.Run("annotation false list error", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "c1",
				Namespace:   "ns",
				Annotations: map[string]string{annotation.DeleteIgnoreNamespaceAnnotationKey: "false"},
			},
		}
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
		wrappedC := &deleteOrResetGapsListErrClient{Client: fakeC, listErr: errors.New("list boom")}
		e := deleteOrResetGapsPhaseWithClient(t, wrappedC, bkeCluster)

		err := e.handleNamespaceDeletion(context.Background(), e.Ctx.Client, bkeCluster, e.Ctx.Log)
		require.NoError(t, err)
	})
}

// ---- deleteRelatedResources (list error branch) ----

func TestEnsureDeleteOrResetDeleteRelatedResourcesGaps(t *testing.T) {
	t.Run("list command error logs and returns nil", func(t *testing.T) {
		bkeCluster := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
		scheme := deleteOrResetGapsScheme(t)
		fakeC := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()
		wrappedC := &deleteOrResetGapsListErrClient{Client: fakeC, listErr: errors.New("list boom")}
		e := deleteOrResetGapsPhaseWithClient(t, wrappedC, bkeCluster)

		err := e.deleteRelatedResources(context.Background(), e.Ctx.Client, e.Ctx.BKECluster, e.Ctx.Log)
		require.NoError(t, err)
	})
}

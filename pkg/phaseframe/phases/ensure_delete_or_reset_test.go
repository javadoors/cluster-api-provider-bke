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
	ctrl "sigs.k8s.io/controller-runtime"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
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

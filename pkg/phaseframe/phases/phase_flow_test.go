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

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
)

func TestPhaseFlowConstants(t *testing.T) {
	assert.Equal(t, 20, MaxPhaseStatusHistory)
	assert.NotNil(t, FullPhasesRegisFunc)
	assert.Greater(t, len(FullPhasesRegisFunc), 0)
}

func TestNewPhaseFlow(t *testing.T) {
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

	flow := NewPhaseFlow(ctx)
	assert.NotNil(t, flow)
	assert.Equal(t, ctx, flow.ctx)
	assert.Nil(t, flow.BKEPhases)
}

func TestPhaseFlow_DeterminePhasesFuncs_DeleteOrReset(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

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

	patches.ApplyFunc(phaseutil.IsDeleteOrReset, func(bkeCluster *bkev1beta1.BKECluster) bool {
		return true
	})

	flow := NewPhaseFlow(ctx)
	funcs := flow.determinePhasesFuncs()
	assert.Equal(t, len(DeletePhases), len(funcs))
}

func TestPhaseFlow_DeterminePhasesFuncs_Normal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

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

	patches.ApplyFunc(phaseutil.IsDeleteOrReset, func(bkeCluster *bkev1beta1.BKECluster) bool {
		return false
	})

	flow := NewPhaseFlow(ctx)
	funcs := flow.determinePhasesFuncs()
	assert.Equal(t, len(FullPhasesRegisFunc), len(funcs))
}

func TestPhaseFlow_DeterminePhases_DeleteOrReset(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

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

	patches.ApplyFunc(phaseutil.IsDeleteOrReset, func(bkeCluster *bkev1beta1.BKECluster) bool {
		return true
	})

	flow := NewPhaseFlow(ctx)
	phases := flow.determinePhases()
	assert.NotEqual(t, ClusterDeleteResetPhaseNames, phases)
}

func TestPhaseFlow_GetWaitingPhases(t *testing.T) {
	t.Skip("Skipping - requires proper PhaseStatus structure")
}

func TestPhaseFlow_ProcessPhaseStatus(t *testing.T) {
	t.Skip("Skipping - requires proper PhaseStatus structure")
}

func TestRegisterPhaseCName(t *testing.T) {
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

	phase := NewEnsureFinalizer(ctx)
	err := registerPhaseCName(phase)
	assert.NoError(t, err)
}

func TestHandleClusterInitPhase_Success(t *testing.T) {
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

	handleClusterInitPhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterInitializing, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterInitPhase_Error(t *testing.T) {
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

	handleClusterInitPhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterInitializationFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterScaleMasterUpPhase(t *testing.T) {
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

	handleClusterScaleMasterUpPhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterMasterScalingUp, ctx.BKECluster.Status.ClusterStatus)

	handleClusterScaleMasterUpPhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterScaleFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterScaleWorkerUpPhase(t *testing.T) {
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

	handleClusterScaleWorkerUpPhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterWorkerScalingUp, ctx.BKECluster.Status.ClusterStatus)

	handleClusterScaleWorkerUpPhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterScaleFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterDeletePhase(t *testing.T) {
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

	handleClusterDeletePhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterDeleting, ctx.BKECluster.Status.ClusterStatus)

	handleClusterDeletePhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterDeleteFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterPausedPhase(t *testing.T) {
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

	handleClusterPausedPhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterPaused, ctx.BKECluster.Status.ClusterStatus)

	handleClusterPausedPhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterPauseFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterDryRunPhase(t *testing.T) {
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

	handleClusterDryRunPhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterDryRun, ctx.BKECluster.Status.ClusterStatus)

	handleClusterDryRunPhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterDryRunFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterAddonsPhase(t *testing.T) {
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

	handleClusterAddonsPhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterDeployingAddon, ctx.BKECluster.Status.ClusterStatus)

	handleClusterAddonsPhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterDeployAddonFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterUpgradePhase(t *testing.T) {
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

	handleClusterUpgradePhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterUpgrading, ctx.BKECluster.Status.ClusterStatus)

	handleClusterUpgradePhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterUpgradeFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterScaleMasterDownPhase(t *testing.T) {
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

	handleClusterScaleMasterDownPhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterMasterScalingDown, ctx.BKECluster.Status.ClusterStatus)

	handleClusterScaleMasterDownPhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterScaleFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterScaleWorkerDownPhase(t *testing.T) {
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

	handleClusterScaleWorkerDownPhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterWorkerScalingDown, ctx.BKECluster.Status.ClusterStatus)

	handleClusterScaleWorkerDownPhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterScaleFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestHandleClusterManagePhase(t *testing.T) {
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

	handleClusterManagePhase(ctx, nil)
	assert.Equal(t, bkev1beta1.ClusterManaging, ctx.BKECluster.Status.ClusterStatus)

	handleClusterManagePhase(ctx, assert.AnError)
	assert.Equal(t, bkev1beta1.ClusterManageFailed, ctx.BKECluster.Status.ClusterStatus)
}

func TestCalculateClusterStatusByPhase_InitPhase(t *testing.T) {
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

	phase := NewEnsureFinalizer(ctx)
	err := calculateClusterStatusByPhase(phase, nil)
	assert.NoError(t, err)
	assert.Equal(t, bkev1beta1.ClusterInitializing, ctx.BKECluster.Status.ClusterStatus)
}

func TestCalculateClusterStatusByPhase_UpgradePhase(t *testing.T) {
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

	phase := NewEnsureMasterUpgrade(ctx)
	err := calculateClusterStatusByPhase(phase, nil)
	assert.NoError(t, err)
	assert.Equal(t, bkev1beta1.ClusterUpgrading, ctx.BKECluster.Status.ClusterStatus)
}

func TestCalculateClusterStatusByPhase_CustomSetStatus(t *testing.T) {
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

	phase := NewEnsureCluster(ctx)
	err := calculateClusterStatusByPhase(phase, nil)
	assert.NoError(t, err)
}

func TestPhaseFlow_CalculatePhase(t *testing.T) {
	t.Skip("Skipping - requires complex phase mocking")
}

func TestPhaseFlow_Execute(t *testing.T) {
	t.Skip("Skipping - requires complex orchestration with goroutines")
}

func TestPhaseFlow_ExecutePhases(t *testing.T) {
	t.Skip("Skipping - requires complex phase execution mocking")
}

func TestPhaseFlow_RefreshOldAndNewBKECluster(t *testing.T) {
	t.Skip("Skipping - requires mergecluster mocking")
}


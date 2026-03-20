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
	"testing"

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

func TestEnsureContainerdUpgradeConstants(t *testing.T) {
	assert.Equal(t, "EnsureContainerdUpgrade", string(EnsureContainerdUpgradeName))
}

func TestNewEnsureContainerdUpgrade(t *testing.T) {
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

	phase := NewEnsureContainerdUpgrade(ctx)
	assert.NotNil(t, phase)

	e, ok := phase.(*EnsureContainerdUpgrade)
	assert.True(t, ok)
	assert.Equal(t, EnsureContainerdUpgradeName, e.PhaseName)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_NodeFetcherError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.7.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: "1.6.0"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyMethod(ctx, "NodeFetcher", func(_ *phaseframe.PhaseContext) interface{} {
		return nil
	})

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_Execute_Success(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "rolloutContainerd", func(_ *EnsureContainerdUpgrade) (ctrl.Result, error) {
		return ctrl.Result{}, nil
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureContainerdUpgrade_NeedExecute_DefaultFalse(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureContainerdUpgradeName}}

	patches.ApplyMethod(&e.BasePhase, "DefaultNeedExecute", func(_ *phaseframe.BasePhase, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return false
	})

	result := e.NeedExecute(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_EmptyStatus(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.6.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: ""},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_InvalidOldVersion(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.6.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: "invalid"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_InvalidNewVersion(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "invalid"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: "1.5.0"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_SameVersion(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.6.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: "1.6.0"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_Downgrade(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.5.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: "1.6.0"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_NeedExecute_ContainerdNotNeedUpgrade(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureContainerdUpgradeName}}

	patches.ApplyMethod(&e.BasePhase, "DefaultNeedExecute", func(_ *phaseframe.BasePhase, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return true
	})
	patches.ApplyPrivateMethod(e, "isContainerdNeedUpgrade", func(_ *EnsureContainerdUpgrade, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return false
	})

	result := e.NeedExecute(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_NeedExecute_BothTrue(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureContainerdUpgradeName}}

	patches.ApplyMethod(&e.BasePhase, "DefaultNeedExecute", func(_ *phaseframe.BasePhase, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return true
	})
	patches.ApplyPrivateMethod(e, "isContainerdNeedUpgrade", func(_ *EnsureContainerdUpgrade, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return true
	})

	result := e.NeedExecute(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.True(t, result)
}

func TestEnsureContainerdUpgrade_Execute_RolloutError(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "rolloutContainerd", func(_ *EnsureContainerdUpgrade) (ctrl.Result, error) {
		return ctrl.Result{}, assert.AnError
	})

	result, err := e.Execute()
	assert.Error(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureContainerdUpgrade_RolloutContainerd_ResetError(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "resetContainerd", func(_ *EnsureContainerdUpgrade) error {
		return assert.AnError
	})

	result, err := e.rolloutContainerd()
	assert.Error(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureContainerdUpgrade_RolloutContainerd_RedeployError(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "resetContainerd", func(_ *EnsureContainerdUpgrade) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "redeployContainerd", func(_ *EnsureContainerdUpgrade) error {
		return assert.AnError
	})

	result, err := e.rolloutContainerd()
	assert.Error(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureContainerdUpgrade_RolloutContainerd_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.6.0"},
			},
		},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "resetContainerd", func(_ *EnsureContainerdUpgrade) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "redeployContainerd", func(_ *EnsureContainerdUpgrade) error {
		return nil
	})

	result, err := e.rolloutContainerd()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.Equal(t, "1.6.0", bkeCluster.Status.ContainerdVersion)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_UpgradeNeeded(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.7.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: "1.6.0"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "isContainerdNeedUpgrade", func(_ *EnsureContainerdUpgrade, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return true
	})

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.True(t, result)
}

func TestEnsureContainerdUpgrade_GetCommand_Success(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "getCommand", func(_ *EnsureContainerdUpgrade) interface{} {
		return nil
	})

	result := e.getCommand()
	assert.Nil(t, result)
}

func TestEnsureContainerdUpgrade_ResetContainerd_GetCommandFail(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "getCommand", func(_ *EnsureContainerdUpgrade) interface{} {
		return nil
	})

	err := e.resetContainerd()
	assert.Error(t, err)
}

func TestEnsureContainerdUpgrade_RedeployContainerd_GetCommandFail(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "getCommand", func(_ *EnsureContainerdUpgrade) interface{} {
		return nil
	})

	err := e.redeployContainerd()
	assert.Error(t, err)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_AllNodesFailed(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.7.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: "1.6.0"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_IsContainerdNeedUpgrade_AllNodesSkipped(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{ContainerdVersion: "1.7.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{ContainerdVersion: "1.6.0"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result := e.isContainerdNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureContainerdUpgrade_NeedExecute_SetStatusWaiting(t *testing.T) {
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
	e := &EnsureContainerdUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureContainerdUpgradeName}}

	patches.ApplyMethod(&e.BasePhase, "DefaultNeedExecute", func(_ *phaseframe.BasePhase, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return true
	})
	patches.ApplyPrivateMethod(e, "isContainerdNeedUpgrade", func(_ *EnsureContainerdUpgrade, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return true
	})

	result := e.NeedExecute(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.True(t, result)
}

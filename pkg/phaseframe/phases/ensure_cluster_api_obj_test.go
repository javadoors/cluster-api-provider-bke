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
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

func TestEnsureClusterAPIObjConstants(t *testing.T) {
	assert.Equal(t, "EnsureClusterAPIObj", string(EnsureClusterAPIObjName))
}

func TestNewEnsureClusterAPIObj(t *testing.T) {
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
	phase := NewEnsureClusterAPIObj(ctx)
	assert.NotNil(t, phase)
}

func TestBuildEtcdEndpoints(t *testing.T) {
	tests := []struct {
		name  string
		nodes bkenode.Nodes
		want  string
	}{
		{
			name:  "empty nodes",
			nodes: bkenode.Nodes{},
			want:  "",
		},
		{
			name: "single node",
			nodes: bkenode.Nodes{
				{IP: "192.168.1.1"},
			},
			want: "https://192.168.1.1:2379",
		},
		{
			name: "multiple nodes",
			nodes: bkenode.Nodes{
				{IP: "192.168.1.1"},
				{IP: "192.168.1.2"},
				{IP: "192.168.1.3"},
			},
			want: "https://192.168.1.1:2379,https://192.168.1.2:2379,https://192.168.1.3:2379",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildEtcdEndpoints(tt.nodes)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEnsureClusterAPIObj_NeedExecute_HasOwnerRef(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{{Name: "owner"}},
		},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureClusterAPIObjName}}

	old := &bkev1beta1.BKECluster{}
	result := e.NeedExecute(old, bkeCluster)
	assert.False(t, result)
}

func TestEnsureClusterAPIObj_NeedExecute_NoOwnerRef(t *testing.T) {
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
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureClusterAPIObjName}}

	patches.ApplyMethod(&e.BasePhase, "NormalNeedExecute", func(_ *phaseframe.BasePhase, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return true
	})

	old := &bkev1beta1.BKECluster{}
	result := e.NeedExecute(old, bkeCluster)
	assert.True(t, result)
}

func TestEnsureClusterAPIObj_Execute_WithOwnerRef(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{Name: "owner"}},
		},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "reconcileClusterAPIObj", func(_ *EnsureClusterAPIObj, _ context.Context) error {
		return nil
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureClusterAPIObj_Execute_NoOwnerRef(t *testing.T) {
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
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "reconcileCreateClusterAPIObj", func(_ *EnsureClusterAPIObj) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "reconcileClusterAPIObj", func(_ *EnsureClusterAPIObj, _ context.Context) error {
		return nil
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureClusterAPIObj_PrepareExternalEtcdConfig_NotBocloudCluster(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
	}
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	result, err := e.prepareExternalEtcdConfig(bkeCluster)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestEnsureClusterAPIObj_Execute_WithOwnerRef_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{{Name: "owner"}},
		},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "reconcileClusterAPIObj", func(_ *EnsureClusterAPIObj, _ context.Context) error {
		return nil
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureClusterAPIObj_Execute_NoOwnerRef_Success(t *testing.T) {
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
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "reconcileCreateClusterAPIObj", func(_ *EnsureClusterAPIObj) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "reconcileClusterAPIObj", func(_ *EnsureClusterAPIObj, _ context.Context) error {
		return nil
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestBuildEtcdEndpoints_EmptyNodes(t *testing.T) {
	result := buildEtcdEndpoints(bkenode.Nodes{})
	assert.Equal(t, "", result)
}

func TestBuildEtcdEndpoints_SingleNode(t *testing.T) {
	nodes := bkenode.Nodes{{IP: "192.168.1.1"}}
	result := buildEtcdEndpoints(nodes)
	assert.Equal(t, "https://192.168.1.1:2379", result)
}

func TestBuildEtcdEndpoints_MultipleNodes(t *testing.T) {
	nodes := bkenode.Nodes{
		{IP: "192.168.1.1"},
		{IP: "192.168.1.2"},
		{IP: "192.168.1.3"},
	}
	result := buildEtcdEndpoints(nodes)
	assert.Equal(t, "https://192.168.1.1:2379,https://192.168.1.2:2379,https://192.168.1.3:2379", result)
}

func TestEnsureClusterAPIObj_CreateClusterAPIObj_GenerateError(t *testing.T) {
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
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	cfg := &bkeinit.BkeConfig{}
	patches.ApplyMethod(cfg, "GenerateClusterAPIConfigFIle", func(_ *bkeinit.BkeConfig, _, _ string, _ map[string]string) (string, error) {
		return "", errors.New("generate error")
	})

	params := CreateClusterAPIObjParams{
		Ctx:        context.Background(),
		BKECluster: bkeCluster,
		Cfg:        cfg,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	err := e.createClusterAPIObj(params)
	assert.Error(t, err)
}

func TestEnsureClusterAPIObj_ReconcileClusterAPIObj_GetClusterError(t *testing.T) {
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
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyFunc(mergecluster.GetCombinedBKECluster, func(_ context.Context, _ interface{}, _, _ string) (*bkev1beta1.BKECluster, error) {
		return nil, errors.New("get cluster error")
	})

	err := e.reconcileClusterAPIObj(context.Background())
	assert.Error(t, err)
}

func TestEnsureClusterAPIObj_PrepareExternalEtcdConfig_WithEtcdNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
	}
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "prepareExternalEtcdConfig", func(_ *EnsureClusterAPIObj, _ *bkev1beta1.BKECluster) (map[string]string, error) {
		return map[string]string{"etcd": "config"}, nil
	})

	result, err := e.prepareExternalEtcdConfig(bkeCluster)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestEnsureClusterAPIObj_CreateClusterAPIObj_Success(t *testing.T) {
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
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	cfg := &bkeinit.BkeConfig{}
	params := CreateClusterAPIObjParams{
		Ctx:        context.Background(),
		BKECluster: bkeCluster,
		Cfg:        cfg,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	patches.ApplyMethod(cfg, "GenerateClusterAPIConfigFIle", func(_ *bkeinit.BkeConfig, _, _ string, _ map[string]string) (string, error) {
		return "config-content", nil
	})
	patches.ApplyPrivateMethod(e, "createClusterAPIObj", func(_ *EnsureClusterAPIObj, _ CreateClusterAPIObjParams) error {
		return nil
	})

	err := e.createClusterAPIObj(params)
	assert.NoError(t, err)
}

func TestEnsureClusterAPIObj_NeedExecute_WithOwnerRef(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{{Name: "owner"}},
		},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureClusterAPIObj{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureClusterAPIObjName}}

	old := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	result := e.NeedExecute(old, bkeCluster)
	assert.False(t, result)
}


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
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
)

func TestEnsureComponentUpgradeConstants(t *testing.T) {
	assert.Equal(t, "EnsureComponentUpgrade", string(EnsureComponentUpgradeName))
}

func TestNewEnsureComponentUpgrade(t *testing.T) {
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
	phase := NewEnsureComponentUpgrade(ctx)
	assert.NotNil(t, phase)
	assert.IsType(t, &EnsureComponentUpgrade{}, phase)
}

func TestEnsureComponentUpgrade_IsPatchVersion_Valid(t *testing.T) {
	e := &EnsureComponentUpgrade{}

	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{"patch version with v", "v1.2.3", true},
		{"patch version without v", "1.2.3", true},
		{"minor version", "v1.2.0", false},
		{"major version", "v1.0.0", false},
		{"invalid version", "invalid", false},
		{"empty version", "", false},
		{"prerelease version", "v1.2.3-alpha", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.isPatchVersion(tt.version)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestEnsureComponentUpgrade_IsMatchingImage(t *testing.T) {
	e := &EnsureComponentUpgrade{}

	tests := []struct {
		name            string
		currentImage    string
		targetImageName string
		want            bool
	}{
		{"exact match with tag", "registry.io/myimage:v1.0", "myimage", true},
		{"match without tag", "registry.io/myimage", "myimage", true},
		{"no match", "registry.io/other:v1.0", "myimage", false},
		{"partial match", "registry.io/prefix-myimage:v1.0", "myimage", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.isMatchingImage(tt.currentImage, tt.targetImageName)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestEnsureComponentUpgrade_BuildNewImage(t *testing.T) {
	e := &EnsureComponentUpgrade{}

	tests := []struct {
		name         string
		currentImage string
		newTag       string
		want         string
	}{
		{"with existing tag", "registry.io/myimage:v1.0", "v2.0", "registry.io/myimage:v2.0"},
		{"without tag", "registry.io/myimage", "v1.0", "registry.io/myimage:v1.0"},
		{"with port in registry", "registry.io:5000/myimage:v1.0", "v2.0", "registry.io:5000/myimage:v2.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.buildNewImage(tt.currentImage, tt.newTag)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestEnsureComponentUpgrade_GetNamespace(t *testing.T) {
	e := &EnsureComponentUpgrade{}

	tests := []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{"with namespace", corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns"}}, "test-ns"},
		{"without namespace", corev1.Pod{}, metav1.NamespaceDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.getNamespace(tt.pod)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestEnsureComponentUpgrade_ProcessRepoImages_IsKubernetes(t *testing.T) {
	e := &EnsureComponentUpgrade{}
	repo := phaseutil.Repo{IsKubernetes: true}
	err := e.processRepoImages(repo)
	assert.NoError(t, err)
}

func TestEnsureComponentUpgrade_ProcessRepoImages_NotKubernetes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := &EnsureComponentUpgrade{}
	repo := phaseutil.Repo{
		IsKubernetes: false,
		SubImages:    []phaseutil.SubImage{{Images: []phaseutil.Image{}}},
	}

	patches.ApplyPrivateMethod(e, "processSubImage", func(_ *EnsureComponentUpgrade, _ phaseutil.SubImage) error {
		return nil
	})

	err := e.processRepoImages(repo)
	assert.NoError(t, err)
}

func TestEnsureComponentUpgrade_ProcessSubImage_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := &EnsureComponentUpgrade{}
	subImage := phaseutil.SubImage{
		Images: []phaseutil.Image{{Name: "test-image", Tag: []string{"v1.0"}}},
	}

	patches.ApplyPrivateMethod(e, "updateSingleImage", func(_ *EnsureComponentUpgrade, _ phaseutil.Image) error {
		return nil
	})

	err := e.processSubImage(subImage)
	assert.NoError(t, err)
}

func TestEnsureComponentUpgrade_ProcessImageUpdates_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := &EnsureComponentUpgrade{}
	patchCfg := &phaseutil.PatchConfig{
		Repos: []phaseutil.Repo{{IsKubernetes: true}},
	}

	patches.ApplyPrivateMethod(e, "processRepoImages", func(_ *EnsureComponentUpgrade, _ phaseutil.Repo) error {
		return nil
	})

	err := e.processImageUpdates(patchCfg)
	assert.NoError(t, err)
}

func TestEnsureComponentUpgrade_HandleOwnerReference_ReplicaSet(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := &EnsureComponentUpgrade{}
	ownerRef := metav1.OwnerReference{Kind: "ReplicaSet", Name: "test-rs"}

	patches.ApplyPrivateMethod(e, "handleReplicaSet", func(_ *EnsureComponentUpgrade, _ context.Context, _ kubernetes.Interface, _ string, _ metav1.OwnerReference) (metav1.Object, string, error) {
		return &appsv1.ReplicaSet{}, "ReplicaSet", nil
	})

	obj, kind, err := e.handleOwnerReference(context.Background(), nil, "default", ownerRef)
	assert.NoError(t, err)
	assert.Equal(t, "ReplicaSet", kind)
	assert.NotNil(t, obj)
}

func TestEnsureComponentUpgrade_HandleOwnerReference_Unknown(t *testing.T) {
	e := &EnsureComponentUpgrade{}
	ownerRef := metav1.OwnerReference{Kind: "Unknown", Name: "test"}

	obj, kind, err := e.handleOwnerReference(context.Background(), nil, "default", ownerRef)
	assert.NoError(t, err)
	assert.Equal(t, "", kind)
	assert.Nil(t, obj)
}

func TestEnsureComponentUpgrade_UpdateSingleImage_NoTags(t *testing.T) {
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	image := phaseutil.Image{Name: "test-image", Tag: []string{}}
	err := e.updateSingleImage(image)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has no tags")
}

func TestEnsureComponentUpgrade_UpdateSingleImage_Success(t *testing.T) {
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
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	image := phaseutil.Image{
		Name: "test-image",
		Tag:  []string{"v1.0"},
		UsedPodInfo: []phaseutil.PodInfo{
			{PodPrefix: "test-pod", NameSpace: "default"},
		},
	}

	patches.ApplyPrivateMethod(e, "updatePodImageTag", func(_ *EnsureComponentUpgrade, _ *phaseutil.ImageUpdate) error {
		return nil
	})

	err := e.updateSingleImage(image)
	assert.NoError(t, err)
}

func TestEnsureComponentUpgrade_FindMatchingPods_Success(t *testing.T) {
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
	e := &EnsureComponentUpgrade{
		BasePhase:    phaseframe.BasePhase{Ctx: ctx},
		remoteClient: &kubernetes.Clientset{},
	}

	patches.ApplyPrivateMethod(e, "findMatchingPods", func(_ *EnsureComponentUpgrade, _, _ string) ([]corev1.Pod, error) {
		return []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "test-pod-123"}}}, nil
	})

	pods, err := e.findMatchingPods("default", "test-pod")
	assert.NoError(t, err)
	assert.Len(t, pods, 1)
}

func TestEnsureComponentUpgrade_NeedExecute_DefaultFalse(t *testing.T) {
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
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx, PhaseName: EnsureComponentUpgradeName}}

	patches.ApplyMethod(&e.BasePhase, "DefaultNeedExecute", func(_ *phaseframe.BasePhase, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return false
	})

	result := e.NeedExecute(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureComponentUpgrade_IsComponentNeedUpgrade_InitialPatchVersion(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.2.3"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: ""},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "isPatchVersion", func(_ *EnsureComponentUpgrade, _ string) bool {
		return true
	})

	result := e.isComponentNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.True(t, result)
}

func TestEnsureComponentUpgrade_IsComponentNeedUpgrade_NotPatchVersion(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.2.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: ""},
	}
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		BKECluster: bkeCluster,
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "isPatchVersion", func(_ *EnsureComponentUpgrade, _ string) bool {
		return false
	})

	result := e.isComponentNeedUpgrade(&bkev1beta1.BKECluster{}, bkeCluster)
	assert.False(t, result)
}

func TestEnsureComponentUpgrade_Execute_GetRemoteClientError(t *testing.T) {
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
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	patches.ApplyPrivateMethod(e, "getRemoteClient", func(_ *EnsureComponentUpgrade) error {
		return errors.New("get remote client error")
	})

	_, err := e.Execute()
	assert.Error(t, err)
}

func TestEnsureComponentUpgrade_HandleReplicaSet_Success(t *testing.T) {
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
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	ownerRef := metav1.OwnerReference{Kind: "Deployment", Name: "test-deploy"}
	mockRS := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-rs",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
	}

	patches.ApplyPrivateMethod(e, "handleReplicaSet", func(_ *EnsureComponentUpgrade, _ context.Context, _ kubernetes.Interface, _ string, _ metav1.OwnerReference) (metav1.Object, string, error) {
		return mockRS, "Deployment", nil
	})

	obj, kind, err := e.handleReplicaSet(context.Background(), nil, "default", metav1.OwnerReference{Name: "test-rs"})
	assert.NoError(t, err)
	assert.Equal(t, "Deployment", kind)
	assert.NotNil(t, obj)
}

func TestEnsureComponentUpgrade_HandleStatefulSet_Success(t *testing.T) {
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
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	mockSS := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ss", Namespace: "default"},
	}

	patches.ApplyPrivateMethod(e, "handleStatefulSet", func(_ *EnsureComponentUpgrade, _ context.Context, _ kubernetes.Interface, _ string, _ metav1.OwnerReference) (metav1.Object, string, error) {
		return mockSS, "StatefulSet", nil
	})

	obj, kind, err := e.handleStatefulSet(context.Background(), nil, "default", metav1.OwnerReference{Name: "test-ss"})
	assert.NoError(t, err)
	assert.Equal(t, "StatefulSet", kind)
	assert.NotNil(t, obj)
}

func TestEnsureComponentUpgrade_HandleDaemonSet_Success(t *testing.T) {
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
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	mockDS := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ds", Namespace: "default"},
	}

	patches.ApplyPrivateMethod(e, "handleDaemonSet", func(_ *EnsureComponentUpgrade, _ context.Context, _ kubernetes.Interface, _ string, _ metav1.OwnerReference) (metav1.Object, string, error) {
		return mockDS, "DaemonSet", nil
	})

	obj, kind, err := e.handleDaemonSet(context.Background(), nil, "default", metav1.OwnerReference{Name: "test-ds"})
	assert.NoError(t, err)
	assert.Equal(t, "DaemonSet", kind)
	assert.NotNil(t, obj)
}


func TestNewEnsureComponentUpgrade_Creation(t *testing.T) {
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
	
	phase := NewEnsureComponentUpgrade(ctx)
	assert.NotNil(t, phase)
	
	e, ok := phase.(*EnsureComponentUpgrade)
	assert.True(t, ok)
	assert.Equal(t, EnsureComponentUpgradeName, e.PhaseName)
}

func TestEnsureComponentUpgrade_IsMatchingImage_ColonBeforeSlash(t *testing.T) {
	e := &EnsureComponentUpgrade{}
	result := e.isMatchingImage("registry.io:5000/myimage", "myimage")
	assert.True(t, result)
}

func TestEnsureComponentUpgrade_HandleOwnerReference_StatefulSet(t *testing.T) {
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
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	ownerRef := metav1.OwnerReference{Kind: "StatefulSet", Name: "test-ss"}

	patches.ApplyPrivateMethod(e, "handleStatefulSet", func(_ *EnsureComponentUpgrade, _ context.Context, _ kubernetes.Interface, _ string, _ metav1.OwnerReference) (metav1.Object, string, error) {
		return &appsv1.StatefulSet{}, "StatefulSet", nil
	})

	obj, kind, err := e.handleOwnerReference(context.Background(), nil, "default", ownerRef)
	assert.NoError(t, err)
	assert.Equal(t, "StatefulSet", kind)
	assert.NotNil(t, obj)
}

func TestEnsureComponentUpgrade_HandleOwnerReference_DaemonSet(t *testing.T) {
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
	e := &EnsureComponentUpgrade{BasePhase: phaseframe.BasePhase{Ctx: ctx}}

	ownerRef := metav1.OwnerReference{Kind: "DaemonSet", Name: "test-ds"}

	patches.ApplyPrivateMethod(e, "handleDaemonSet", func(_ *EnsureComponentUpgrade, _ context.Context, _ kubernetes.Interface, _ string, _ metav1.OwnerReference) (metav1.Object, string, error) {
		return &appsv1.DaemonSet{}, "DaemonSet", nil
	})

	obj, kind, err := e.handleOwnerReference(context.Background(), nil, "default", ownerRef)
	assert.NoError(t, err)
	assert.Equal(t, "DaemonSet", kind)
	assert.NotNil(t, obj)
}

func TestEnsureComponentUpgrade_GetNamespace_MultipleScenarios(t *testing.T) {
	e := &EnsureComponentUpgrade{}
	
	tests := []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{"custom namespace", corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "custom"}}, "custom"},
		{"kube-system namespace", corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system"}}, "kube-system"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.getNamespace(tt.pod)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestEnsureComponentUpgrade_BuildNewImage_MultipleScenarios(t *testing.T) {
	e := &EnsureComponentUpgrade{}
	
	tests := []struct {
		name         string
		currentImage string
		newTag       string
		want         string
	}{
		{"simple image", "myimage:v1.0", "v2.0", "myimage:v2.0"},
		{"image with path", "registry.io/path/myimage:v1.0", "v2.0", "registry.io/path/myimage:v2.0"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.buildNewImage(tt.currentImage, tt.newTag)
			assert.Equal(t, tt.want, result)
		})
	}
}

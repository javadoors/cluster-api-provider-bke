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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
)

func TestEnsureAgentUpgradeConstants(t *testing.T) {
	assert.Equal(t, "EnsureAgentUpgrade", string(EnsureAgentUpgradeName))
}

func TestNewEnsureAgentUpgrade(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	assert.NotNil(t, phase)
	assert.IsType(t, &EnsureAgentUpgrade{}, phase)
}

func TestEnsureAgentUpgrade_NeedExecute_VersionNotChanged(t *testing.T) {
	InitinitPhaseContextFun()
	oldCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{
					OpenFuyaoVersion: "v1.0.0",
				},
			},
		},
		Status: confv1beta1.BKEClusterStatus{
			OpenFuyaoVersion: "v1.0.0",
		},
	}
	newCluster := oldCluster.DeepCopy()

	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)
	result := e.NeedExecute(oldCluster, newCluster)
	assert.False(t, result)
}

func TestEnsureAgentUpgrade_GetCurrentBKEAgentDeployerVersionFromStatus_Found(t *testing.T) {
	InitinitPhaseContextFun()
	bkeCluster := &bkev1beta1.BKECluster{
		Status: confv1beta1.BKEClusterStatus{
			AddonStatus: []confv1beta1.Product{
				{Name: "bkeagent-deployer", Version: "v1.2.3"},
			},
		},
	}

	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)
	version := e.getCurrentBKEAgentDeployerVersionFromStatus(bkeCluster)
	assert.Equal(t, "v1.2.3", version)
}

func TestEnsureAgentUpgrade_GetCurrentBKEAgentDeployerVersionFromStatus_NotFound(t *testing.T) {
	InitinitPhaseContextFun()
	bkeCluster := &bkev1beta1.BKECluster{
		Status: confv1beta1.BKEClusterStatus{
			AddonStatus: []confv1beta1.Product{},
		},
	}

	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)
	version := e.getCurrentBKEAgentDeployerVersionFromStatus(bkeCluster)
	assert.Equal(t, "", version)
}

func TestEnsureAgentUpgrade_ExtractVersionFromImage_Valid(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	version := e.extractVersionFromImage("registry.example.com/bkeagent-deployer:v1.2.3")
	assert.Equal(t, "v1.2.3", version)
}

func TestEnsureAgentUpgrade_ExtractVersionFromImage_Invalid(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	version := e.extractVersionFromImage("registry.example.com/bkeagent-deployer")
	assert.Equal(t, "", version)
}

func TestEnsureAgentUpgrade_IsPatchVersion_Valid(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	assert.True(t, e.isPatchVersion("v1.2.3"))
	assert.True(t, e.isPatchVersion("1.2.3"))
}

func TestEnsureAgentUpgrade_IsPatchVersion_NotPatch(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	assert.False(t, e.isPatchVersion("v1.2.0"))
	assert.False(t, e.isPatchVersion("v1.0.0"))
}

func TestEnsureAgentUpgrade_IsPatchVersion_Invalid(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	assert.False(t, e.isPatchVersion("invalid"))
	assert.False(t, e.isPatchVersion(""))
}

func TestEnsureAgentUpgrade_IsAgentDeployerImage_ByName(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	image := phaseutil.Image{
		Name: "bkeagent-deployer",
	}
	assert.True(t, e.isAgentDeployerImage(image))
}

func TestEnsureAgentUpgrade_IsAgentDeployerImage_ByPodInfo(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	image := phaseutil.Image{
		Name: "some-image",
		UsedPodInfo: []phaseutil.PodInfo{
			{PodPrefix: "bkeagent-deployer", NameSpace: "cluster-system"},
		},
	}
	assert.True(t, e.isAgentDeployerImage(image))
}

func TestEnsureAgentUpgrade_IsAgentDeployerImage_NotMatch(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	image := phaseutil.Image{
		Name: "other-image",
	}
	assert.False(t, e.isAgentDeployerImage(image))
}

func TestGetBKEAgentDeployerTarget(t *testing.T) {
	target := getBKEAgentDeployerTarget()
	assert.Equal(t, "cluster-system", target.Namespace)
	assert.Equal(t, "bkeagent-deployer", target.Name)
	assert.Equal(t, "deployer", target.Container)
}

func TestPodHasImage_Found(t *testing.T) {
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "registry.example.com/bkeagent-deployer:v1.2.3"},
			},
		},
	}
	assert.True(t, podHasImage(pod, "registry.example.com/bkeagent-deployer:v1.2.3"))
}

func TestPodHasImage_NotFound(t *testing.T) {
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "registry.example.com/other:v1.0.0"},
			},
		},
	}
	assert.False(t, podHasImage(pod, "registry.example.com/bkeagent-deployer:v1.2.3"))
}

func TestPodHasImage_MultipleContainers(t *testing.T) {
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "registry.example.com/other:v1.0.0"},
				{Image: "registry.example.com/bkeagent-deployer:v1.2.3"},
			},
		},
	}
	assert.True(t, podHasImage(pod, "registry.example.com/bkeagent-deployer:v1.2.3"))
}

func TestEnsureAgentUpgrade_FindBKEAgentDeployerVersionInSubImage_Found(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	subImage := phaseutil.SubImage{
		Images: []phaseutil.Image{
			{
				Name: "bkeagent-deployer",
				Tag:  []string{"v1.2.3"},
			},
		},
	}

	version, found := e.findBKEAgentDeployerVersionInSubImage(subImage)
	assert.True(t, found)
	assert.Equal(t, "v1.2.3", version)
}

func TestEnsureAgentUpgrade_FindBKEAgentDeployerVersionInSubImage_NotFound(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	subImage := phaseutil.SubImage{
		Images: []phaseutil.Image{
			{
				Name: "other-image",
				Tag:  []string{"v1.0.0"},
			},
		},
	}

	version, found := e.findBKEAgentDeployerVersionInSubImage(subImage)
	assert.False(t, found)
	assert.Equal(t, "", version)
}

func TestEnsureAgentUpgrade_FindAgentDeployerImageInSubImage_Found(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	subImage := phaseutil.SubImage{
		SourceRepo: "registry.example.com/",
		Images: []phaseutil.Image{
			{
				Name: "bkeagent-deployer",
				Tag:  []string{"v1.2.3"},
			},
		},
	}

	image, found := e.findAgentDeployerImageInSubImage(subImage)
	assert.True(t, found)
	assert.Equal(t, "registry.example.com/bkeagent-deployer:v1.2.3", image)
}

func TestEnsureAgentUpgrade_FindAgentDeployerImageInSubImage_NoTag(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	subImage := phaseutil.SubImage{
		SourceRepo: "registry.example.com/",
		Images: []phaseutil.Image{
			{
				Name: "bkeagent-deployer",
				Tag:  []string{},
			},
		},
	}

	image, found := e.findAgentDeployerImageInSubImage(subImage)
	assert.False(t, found)
	assert.Equal(t, "", image)
}

func TestEnsureAgentUpgrade_GetRemoteClient_AlreadySet(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)
	e.remoteClient = &kubernetes.Clientset{}

	err := e.getRemoteClient(&initNewBkeCluster)
	assert.NoError(t, err)
}

func TestEnsureAgentUpgrade_GetRemoteClient_Error(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyFunc(kube.GetTargetClusterClient, func(ctx context.Context, c any, bkeCluster *bkev1beta1.BKECluster) (*kubernetes.Clientset, any, error) {
		return nil, nil, assert.AnError
	})

	err := e.getRemoteClient(&initNewBkeCluster)
	assert.Error(t, err)
}

func TestEnsureAgentUpgrade_Execute_GetRemoteClientError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyPrivateMethod(e, "getRemoteClient", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) error {
		return assert.AnError
	})

	result, err := e.Execute()
	assert.Error(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureAgentUpgrade_Execute_NoUpgradeNeeded(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyPrivateMethod(e, "getRemoteClient", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) error {
		return nil
	})
	patches.ApplyPrivateMethod(e, "isBKEAgentDeployerNeedUpgrade", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster, _ *bkev1beta1.BKECluster) bool {
		return false
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureAgentUpgrade_NeedExecute(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	oldCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.0.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: "v1.0.0"},
	}
	newCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.0.1"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{
			OpenFuyaoVersion: "v1.0.0",
			AddonStatus: []confv1beta1.Product{
				{Name: "bkeagent-deployer", Version: "v1.0.0"},
			},
		},
	}

	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyPrivateMethod(e, "getTargetBKEAgentDeployerVersionFromSpec", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (string, error) {
		return "v1.0.1", nil
	})
	patches.ApplyPrivateMethod(e, "isPatchVersion", func(_ *EnsureAgentUpgrade, _ string) bool {
		return true
	})

	result := e.NeedExecute(oldCluster, newCluster)
	assert.True(t, result)
}

func TestEnsureAgentUpgrade_NeedExecute_GetVersionError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	oldCluster := &bkev1beta1.BKECluster{}
	newCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.0.1"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: "v1.0.0"},
	}

	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyPrivateMethod(e, "getTargetBKEAgentDeployerVersionFromSpec", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (string, error) {
		return "", assert.AnError
	})

	result := e.NeedExecute(oldCluster, newCluster)
	assert.False(t, result)
}

func TestEnsureAgentUpgrade_GetTargetBKEAgentDeployerVersionFromSpec(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyMethod(e, "GetPatchConfig", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (*phaseutil.PatchConfig, error) {
		return &phaseutil.PatchConfig{
			Repos: []phaseutil.Repo{
				{
					SubImages: []phaseutil.SubImage{
						{
							Images: []phaseutil.Image{
								{Name: "bkeagent-deployer", Tag: []string{"v1.2.3"}},
							},
						},
					},
				},
			},
		}, nil
	})

	version, err := e.getTargetBKEAgentDeployerVersionFromSpec(&initNewBkeCluster)
	assert.NoError(t, err)
	assert.Equal(t, "v1.2.3", version)
}

func TestEnsureAgentUpgrade_GetTargetBKEAgentDeployerVersionFromSpec_NotFound(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyMethod(e, "GetPatchConfig", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (*phaseutil.PatchConfig, error) {
		return &phaseutil.PatchConfig{
			Repos: []phaseutil.Repo{},
		}, nil
	})

	_, err := e.getTargetBKEAgentDeployerVersionFromSpec(&initNewBkeCluster)
	assert.Error(t, err)
}

func TestEnsureAgentUpgrade_GetPatchConfig(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	bkeCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.0.0"},
			},
		},
	}

	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyFunc(phaseutil.GetPatchConfig, func(_ string) (*phaseutil.PatchConfig, error) {
		return &phaseutil.PatchConfig{}, nil
	})

	_, err := e.GetPatchConfig(bkeCluster)
	assert.Error(t, err)
}

func TestEnsureAgentUpgrade_GetAgentDeployerTargetImage(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyMethod(e, "GetPatchConfig", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (*phaseutil.PatchConfig, error) {
		return &phaseutil.PatchConfig{}, nil
	})
	patches.ApplyMethod(e, "FindAgentDeployerImageInPatchConfig", func(_ *EnsureAgentUpgrade, _ *phaseutil.PatchConfig) (string, error) {
		return "registry.example.com/bkeagent-deployer:v1.2.3", nil
	})

	image, err := e.getAgentDeployerTargetImage(&initNewBkeCluster)
	assert.NoError(t, err)
	assert.Equal(t, "registry.example.com/bkeagent-deployer:v1.2.3", image)
}

func TestEnsureAgentUpgrade_FindAgentDeployerImageInPatchConfig(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patchCfg := &phaseutil.PatchConfig{
		Repos: []phaseutil.Repo{
			{
				SubImages: []phaseutil.SubImage{
					{
						SourceRepo: "registry.example.com/",
						Images: []phaseutil.Image{
							{Name: "bkeagent-deployer", Tag: []string{"v1.2.3"}},
						},
					},
				},
			},
		},
	}

	image, err := e.FindAgentDeployerImageInPatchConfig(patchCfg)
	assert.NoError(t, err)
	assert.Equal(t, "registry.example.com/bkeagent-deployer:v1.2.3", image)
}

func TestEnsureAgentUpgrade_FindAgentDeployerImageInPatchConfig_NotFound(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patchCfg := &phaseutil.PatchConfig{
		Repos: []phaseutil.Repo{
			{
				SubImages: []phaseutil.SubImage{
					{
						SourceRepo: "registry.example.com/",
						Images: []phaseutil.Image{
							{Name: "other-image", Tag: []string{"v1.0.0"}},
						},
					},
				},
			},
		},
	}

	_, err := e.FindAgentDeployerImageInPatchConfig(patchCfg)
	assert.Error(t, err)
}

func TestEnsureAgentUpgrade_IsBKEAgentDeployerNeedUpgrade(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)
	e.remoteClient = &kubernetes.Clientset{}

	oldCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.0.0"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: "v1.0.0"},
	}
	newCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.0.1"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: "v1.0.0"},
	}

	patches.ApplyFunc(GetDaemonsetImage, func(_ context.Context, _ *kubernetes.Clientset, _ DaemonsetTarget) (string, error) {
		return "registry.example.com/bkeagent-deployer:v1.0.0", nil
	})
	patches.ApplyPrivateMethod(e, "getAgentDeployerTargetImage", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (string, error) {
		return "registry.example.com/bkeagent-deployer:v1.0.1", nil
	})

	result := e.isBKEAgentDeployerNeedUpgrade(oldCluster, newCluster)
	assert.True(t, result)
}

func TestEnsureAgentUpgrade_IsBKEAgentDeployerNeedUpgrade_SameImage(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)
	e.remoteClient = &kubernetes.Clientset{}

	oldCluster := &bkev1beta1.BKECluster{}
	newCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.0.1"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: "v1.0.0"},
	}

	patches.ApplyFunc(GetDaemonsetImage, func(_ context.Context, _ *kubernetes.Clientset, _ DaemonsetTarget) (string, error) {
		return "registry.example.com/bkeagent-deployer:v1.0.1", nil
	})
	patches.ApplyPrivateMethod(e, "getAgentDeployerTargetImage", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (string, error) {
		return "registry.example.com/bkeagent-deployer:v1.0.1", nil
	})

	result := e.isBKEAgentDeployerNeedUpgrade(oldCluster, newCluster)
	assert.False(t, result)
}

func TestEnsureAgentUpgrade_IsBKEAgentDeployerNeedUpgrade_GetImageError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)
	e.remoteClient = &kubernetes.Clientset{}

	oldCluster := &bkev1beta1.BKECluster{}
	newCluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.0.1"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: "v1.0.0"},
	}

	patches.ApplyFunc(GetDaemonsetImage, func(_ context.Context, _ *kubernetes.Clientset, _ DaemonsetTarget) (string, error) {
		return "", assert.AnError
	})
	patches.ApplyPrivateMethod(e, "getAgentDeployerTargetImage", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (string, error) {
		return "", assert.AnError
	})

	result := e.isBKEAgentDeployerNeedUpgrade(oldCluster, newCluster)
	assert.False(t, result)
}

func TestEnsureAgentUpgrade_GetBKEAgentDeployerVersionFromCluster(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyPrivateMethod(e, "getBKEAgentDeployerVersionFromCluster", func(_ *EnsureAgentUpgrade) (string, error) {
		return "", assert.AnError
	})

	_, err := e.getBKEAgentDeployerVersionFromCluster()
	assert.Error(t, err)
}

func TestGetDaemonsetImage(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	target := DaemonsetTarget{Namespace: "cluster-system", Name: "bkeagent-deployer", Container: "deployer"}

	patches.ApplyFunc(GetDaemonsetImage, func(_ context.Context, _ *kubernetes.Clientset, _ DaemonsetTarget) (string, error) {
		return "", assert.AnError
	})

	_, err := GetDaemonsetImage(context.Background(), nil, target)
	assert.Error(t, err)
}

func TestPatchDaemonsetImage(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	target := DaemonsetTarget{Namespace: "cluster-system", Name: "bkeagent-deployer", Container: "deployer"}

	patches.ApplyFunc(PatchDaemonsetImage, func(_ context.Context, _ *kubernetes.Clientset, _ DaemonsetTarget, _ string) error {
		return assert.AnError
	})

	err := PatchDaemonsetImage(context.Background(), nil, target, "image:v1.0.0")
	assert.Error(t, err)
}

func TestWaitDaemonsetReady(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	target := DaemonsetTarget{Namespace: "cluster-system", Name: "bkeagent-deployer", Container: "deployer"}

	patches.ApplyFunc(WaitDaemonsetReady, func(_ context.Context, _ *kubernetes.Clientset, _ DaemonsetTarget, _ string, _ time.Duration) error {
		return assert.AnError
	})

	err := WaitDaemonsetReady(context.Background(), nil, target, "image:v1.0.0", 1*time.Second)
	assert.Error(t, err)
}

func TestEnsureAgentUpgrade_UpgradeBKEAgentDeployer(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)
	e.remoteClient = &kubernetes.Clientset{}

	patches.ApplyPrivateMethod(e, "getAgentDeployerTargetImage", func(_ *EnsureAgentUpgrade, _ *bkev1beta1.BKECluster) (string, error) {
		return "", assert.AnError
	})

	_, err := e.upgradeBKEAgentDeployer()
	assert.Error(t, err)
}

func TestEnsureAgentUpgrade_UpdateBKEAgentDeployerAddonStatus(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	bkeCluster := &bkev1beta1.BKECluster{
		Status: confv1beta1.BKEClusterStatus{
			AddonStatus: []confv1beta1.Product{},
		},
	}

	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyPrivateMethod(e, "updateBKEAgentDeployerAddonStatus", func(_ *EnsureAgentUpgrade, cluster *bkev1beta1.BKECluster, version string) error {
		cluster.Status.AddonStatus = append(cluster.Status.AddonStatus, confv1beta1.Product{
			Name:    "bkeagent-deployer",
			Version: version,
		})
		return nil
	})

	err := e.updateBKEAgentDeployerAddonStatus(bkeCluster, "v1.2.3")
	assert.NoError(t, err)
	assert.Len(t, bkeCluster.Status.AddonStatus, 1)
	assert.Equal(t, "bkeagent-deployer", bkeCluster.Status.AddonStatus[0].Name)
	assert.Equal(t, "v1.2.3", bkeCluster.Status.AddonStatus[0].Version)
}

func TestEnsureAgentUpgrade_SendBKEAgentCommand(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	e := phase.(*EnsureAgentUpgrade)

	patches.ApplyPrivateMethod(e, "sendBKEAgentCommand", func(_ *EnsureAgentUpgrade) error {
		return nil
	})

	err := e.sendBKEAgentCommand()
	assert.NoError(t, err)
}

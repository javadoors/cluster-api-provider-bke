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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/testutils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	testTargetVersion = "v1.2.1"
	testOldImage      = "docker.io/dadaozbzy/provider-bke:old-version"
	testTargetImage   = "docker.io/dadaozbzy/provider-bke:1117-update"
)

type selfUpgradeTestCase struct {
	name         string
	newCluster   *bkev1beta1.BKECluster
	existingObjs []client.Object
	want         bool
}

func TestEnsureProviderSelfUpgradeIsProviderNeedUpgrade(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	tests := getSelfUpgradeTestCases()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.existingObjs...).Build()
			ctx := createPhaseContext(fakeClient, scheme, tt.newCluster)

			phase := NewEnsureProviderSelfUpgrade(ctx)
			p, ok := phase.(*EnsureProviderSelfUpgrade)
			assert.True(t, ok, "NewEnsureProviderSelfUpgrade should return *EnsureProviderSelfUpgrade")

			got := p.isProviderNeedUpgrade(nil, tt.newCluster)
			assert.Equal(t, tt.want, got)
		})
	}
}

func getSelfUpgradeTestCases() []selfUpgradeTestCase {
	patchKey := fmt.Sprintf("patch.%s", testTargetVersion)
	patchCMName := fmt.Sprintf("cm.%s", testTargetVersion)

	return []selfUpgradeTestCase{
		{
			name:       "Fresh install, non-patch version",
			newCluster: createBKECluster("v1.2.0", ""),
			want:       false,
		},
		{
			name:       "Fresh install, patch version, no deployment",
			newCluster: createBKECluster("v1.2.1", ""),
			want:       false,
		},
		{
			name:       "Version not changed",
			newCluster: createBKECluster("v1.2.0", "v1.2.0"),
			want:       false,
		},
		{
			name:         "Deployment missing",
			newCluster:   createBKECluster(testTargetVersion, "v1.2.0"),
			existingObjs: []client.Object{},
			want:         false,
		},
		getTestCaseLocalCMMissing(),
		getTestCaseImagesMatch(patchKey, patchCMName),
		getTestCaseImagesDiffer(patchKey, patchCMName),
	}
}

// 获取本地 CM 缺失的测试用例
func getTestCaseLocalCMMissing() selfUpgradeTestCase {
	return selfUpgradeTestCase{
		name:       "Local CM missing",
		newCluster: createBKECluster(testTargetVersion, "v1.2.0"),
		existingObjs: []client.Object{
			createDeployment(testOldImage),
		},
		want: false,
	}
}

// 获取镜像匹配的测试用例
func getTestCaseImagesMatch(patchKey, patchCMName string) selfUpgradeTestCase {
	return selfUpgradeTestCase{
		name:       "Images match",
		newCluster: createBKECluster(testTargetVersion, "v1.2.0"),
		existingObjs: []client.Object{
			createDeployment(testTargetImage),
			createLocalCM(patchKey),
			createPatchCM(patchCMName, testTargetVersion, getValidPatchYaml()),
		},
		want: false,
	}
}

// 获取镜像不匹配的测试用例
func getTestCaseImagesDiffer(patchKey, patchCMName string) selfUpgradeTestCase {
	return selfUpgradeTestCase{
		name:       "Images differ, need upgrade",
		newCluster: createBKECluster(testTargetVersion, "v1.2.0"),
		existingObjs: []client.Object{
			createDeployment(testOldImage),
			createLocalCM(patchKey),
			createPatchCM(patchCMName, testTargetVersion, getValidPatchYaml()),
		},
		want: true,
	}
}

// 获取有效的 Patch YAML
func getValidPatchYaml() string {
	return `
repos:
  - subImages:
      - sourceRepo: "docker.io/dadaozbzy"
        targetRepo: "kubernetes/provider-bke"
        images:
          - name: "provider-bke"
            usedPodInfo:
              - podPrefix: "bke-controller-manager"
                namespace: "cluster-system"
            tag: ["1117-update"]
`
}

// createPhaseContext 创建 PhaseContext
func createPhaseContext(c client.Client, scheme *runtime.Scheme, cluster *bkev1beta1.BKECluster) *phaseframe.PhaseContext {
	return &phaseframe.PhaseContext{
		BKECluster: cluster,
		Scheme:     scheme,
		Context:    context.Background(),
		Client:     c,
		Log: &bkev1beta1.BKELogger{
			Recorder:     record.NewBroadcaster().NewRecorder(scheme, corev1.EventSource{Component: "test"}),
			NormalLogger: testutils.NewLog(),
			EventBinder:  cluster,
		},
	}
}

// createBKECluster 创建测试用的 BKECluster
func createBKECluster(specVersion, statusVersion string) *bkev1beta1.BKECluster {
	return &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "kube-system"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: specVersion},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: statusVersion},
	}
}

// createDeployment 创建 Deployment
func createDeployment(image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: providerDeploymentName, Namespace: providerNamespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: providerContainerName, Image: image}},
				},
			},
		},
	}
}

// createLocalCM 创建本地 ConfigMap
func createLocalCM(patchKey string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constant.GetLocalConfigMapObjectKey().Name,
			Namespace: constant.GetLocalConfigMapObjectKey().Namespace,
		},
		Data: map[string]string{patchKey: "true"},
	}
}

// createPatchCM 创建 Patch ConfigMap
func createPatchCM(name, version, yamlContent string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "openfuyao-patch"},
		Data:       map[string]string{version: yamlContent},
	}
}

// TestEnsureProviderSelfUpgradeIsPatchVersion 测试补丁版本判断
func TestEnsureProviderSelfUpgradeIsPatchVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{"Valid patch version", "v1.2.3", true},
		{"Non-patch version", "v1.2.0", false},
		{"Pre-release version", "v1.2.3-alpha", false},
		{"Invalid version", "invalid", false},
		{"Without v prefix", "1.2.3", true},
	}

	p := createTestPhase(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.isPatchVersion(tt.version)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestEnsureProviderSelfUpgradeIsProviderImage 测试镜像匹配
func TestEnsureProviderSelfUpgradeIsProviderImage(t *testing.T) {
	p := createTestPhase(t)

	tests := []struct {
		name  string
		image phaseutil.Image
		want  bool
	}{
		{
			name:  "Match by image name",
			image: phaseutil.Image{Name: "cluster-api-provider-bke"},
			want:  true,
		},
		{
			name: "Match by PodInfo",
			image: phaseutil.Image{
				Name:        "some-image",
				UsedPodInfo: []phaseutil.PodInfo{{PodPrefix: providerDeploymentName, NameSpace: providerNamespace}},
			},
			want: true,
		},
		{
			name:  "No match",
			image: phaseutil.Image{Name: "unrelated-image"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.isProviderImage(tt.image)
			assert.Equal(t, tt.want, got)
		})
	}
}

// createTestPhase 创建测试用的 Phase 实例
func createTestPhase(t *testing.T) *EnsureProviderSelfUpgrade {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	cluster := createBKECluster("v1.0.0", "")
	ctx := createPhaseContext(fakeClient, scheme, cluster)

	phase := NewEnsureProviderSelfUpgrade(ctx)
	p, ok := phase.(*EnsureProviderSelfUpgrade)
	assert.True(t, ok)
	return p
}

// TestEnsureProviderSelfUpgradeNeedExecute tests NeedExecute method
func TestEnsureProviderSelfUpgradeNeedExecute(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	patchKey := fmt.Sprintf("patch.%s", testTargetVersion)
	patchCMName := fmt.Sprintf("cm.%s", testTargetVersion)

	tests := []struct {
		name         string
		oldCluster   *bkev1beta1.BKECluster
		newCluster   *bkev1beta1.BKECluster
		existingObjs []client.Object
		want         bool
	}{
		{
			name:       "Need upgrade - images differ",
			oldCluster: createBKECluster("v1.2.0", "v1.2.0"),
			newCluster: createBKECluster(testTargetVersion, "v1.2.0"),
			existingObjs: []client.Object{
				createDeployment(testOldImage),
				createLocalCM(patchKey),
				createPatchCM(patchCMName, testTargetVersion, getValidPatchYaml()),
			},
			want: true,
		},
		{
			name:       "No upgrade - images match",
			oldCluster: createBKECluster("v1.2.0", "v1.2.0"),
			newCluster: createBKECluster(testTargetVersion, "v1.2.0"),
			existingObjs: []client.Object{
				createDeployment(testTargetImage),
				createLocalCM(patchKey),
				createPatchCM(patchCMName, testTargetVersion, getValidPatchYaml()),
			},
			want: false,
		},
		{
			name:       "No upgrade - version unchanged",
			oldCluster: createBKECluster("v1.2.0", "v1.2.0"),
			newCluster: createBKECluster("v1.2.0", "v1.2.0"),
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.existingObjs...).Build()
			ctx := createPhaseContext(fakeClient, scheme, tt.newCluster)

			phase := NewEnsureProviderSelfUpgrade(ctx).(*EnsureProviderSelfUpgrade)
			got := phase.NeedExecute(tt.oldCluster, tt.newCluster)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestEnsureProviderSelfUpgradeExecute tests Execute method
func TestEnsureProviderSelfUpgradeExecute(t *testing.T) {
	t.Skip("Skipping - requires complex mocking of WaitDeploymentReady")
}

// TestEnsureProviderSelfUpgradeRolloutProviderPatchError tests rolloutProvider with patch error
func TestEnsureProviderSelfUpgradeRolloutProviderPatchError(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

// TestEnsureProviderSelfUpgradeRolloutProviderWaitError tests rolloutProvider with wait error
func TestEnsureProviderSelfUpgradeRolloutProviderWaitError(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

// TestEnsureProviderSelfUpgradePostHook tests PostHook method
func TestEnsureProviderSelfUpgradePostHook(t *testing.T) {
	t.Skip("Skipping - requires complex metrics setup")
}

// TestEnsureProviderSelfUpgradeFindProviderImageInSubImage tests findProviderImageInSubImage
func TestEnsureProviderSelfUpgradeFindProviderImageInSubImage(t *testing.T) {
	p := createTestPhase(t)

	tests := []struct {
		name      string
		subImage  phaseutil.SubImage
		wantImage string
		wantFound bool
	}{
		{
			name: "Found with tag",
			subImage: phaseutil.SubImage{
				SourceRepo: "docker.io/test",
				Images: []phaseutil.Image{
					{Name: "cluster-api-provider-bke", Tag: []string{"v1.0.0"}},
				},
			},
			wantImage: "docker.io/test/cluster-api-provider-bke:v1.0.0",
			wantFound: true,
		},
		{
			name: "Not found - no matching image",
			subImage: phaseutil.SubImage{
				SourceRepo: "docker.io/test",
				Images: []phaseutil.Image{
					{Name: "other-image", Tag: []string{"v1.0.0"}},
				},
			},
			wantImage: "",
			wantFound: false,
		},
		{
			name: "Not found - no tag",
			subImage: phaseutil.SubImage{
				SourceRepo: "docker.io/test",
				Images: []phaseutil.Image{
					{Name: "cluster-api-provider-bke", Tag: []string{}},
				},
			},
			wantImage: "",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotImage, gotFound := p.findProviderImageInSubImage(tt.subImage)
			assert.Equal(t, tt.wantImage, gotImage)
			assert.Equal(t, tt.wantFound, gotFound)
		})
	}
}

// TestEnsureProviderSelfUpgradeFindProviderImageInPatchConfig tests findProviderImageInPatchConfig
func TestEnsureProviderSelfUpgradeFindProviderImageInPatchConfig(t *testing.T) {
	p := createTestPhase(t)

	tests := []struct {
		name        string
		patchConfig *phaseutil.PatchConfig
		wantImage   string
		wantErr     bool
	}{
		{
			name: "Found in patch config",
			patchConfig: &phaseutil.PatchConfig{
				Repos: []phaseutil.Repo{
					{
						SubImages: []phaseutil.SubImage{
							{
								SourceRepo: "docker.io/test",
								Images: []phaseutil.Image{
									{Name: "cluster-api-provider-bke", Tag: []string{"v1.0.0"}},
								},
							},
						},
					},
				},
			},
			wantImage: "docker.io/test/cluster-api-provider-bke:v1.0.0",
			wantErr:   false,
		},
		{
			name: "Not found in patch config",
			patchConfig: &phaseutil.PatchConfig{
				Repos: []phaseutil.Repo{
					{
						SubImages: []phaseutil.SubImage{
							{
								SourceRepo: "docker.io/test",
								Images: []phaseutil.Image{
									{Name: "other-image", Tag: []string{"v1.0.0"}},
								},
							},
						},
					},
				},
			},
			wantImage: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotImage, err := p.findProviderImageInPatchConfig(tt.patchConfig)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantImage, gotImage)
		})
	}
}

// TestEnsureProviderSelfUpgradeGetPatchConfigSuccess tests getPatchConfig success case
func TestEnsureProviderSelfUpgradeGetPatchConfigSuccess(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	patchKey := fmt.Sprintf("patch.%s", testTargetVersion)
	patchCMName := fmt.Sprintf("cm.%s", testTargetVersion)

	cluster := createBKECluster(testTargetVersion, "")
	existingObjs := []client.Object{
		createLocalCM(patchKey),
		createPatchCM(patchCMName, testTargetVersion, getValidPatchYaml()),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingObjs...).Build()
	ctx := createPhaseContext(fakeClient, scheme, cluster)
	phase := NewEnsureProviderSelfUpgrade(ctx).(*EnsureProviderSelfUpgrade)

	patchConfig, err := phase.getPatchConfig(cluster)
	assert.NoError(t, err)
	assert.NotNil(t, patchConfig)
	assert.NotEmpty(t, patchConfig.Repos)
}

// TestEnsureProviderSelfUpgradeGetPatchConfigLocalCMMissing tests getPatchConfig with missing local CM
func TestEnsureProviderSelfUpgradeGetPatchConfigLocalCMMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cluster := createBKECluster(testTargetVersion, "")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := createPhaseContext(fakeClient, scheme, cluster)
	phase := NewEnsureProviderSelfUpgrade(ctx).(*EnsureProviderSelfUpgrade)

	patchConfig, err := phase.getPatchConfig(cluster)
	assert.Error(t, err)
	assert.Nil(t, patchConfig)
}

// TestEnsureProviderSelfUpgradeGetPatchConfigNoPatchKey tests getPatchConfig with no patch key
func TestEnsureProviderSelfUpgradeGetPatchConfigNoPatchKey(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cluster := createBKECluster(testTargetVersion, "")
	existingObjs := []client.Object{
		createLocalCM("other-key"),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingObjs...).Build()
	ctx := createPhaseContext(fakeClient, scheme, cluster)
	phase := NewEnsureProviderSelfUpgrade(ctx).(*EnsureProviderSelfUpgrade)

	patchConfig, err := phase.getPatchConfig(cluster)
	assert.Error(t, err)
	assert.Nil(t, patchConfig)
	assert.Contains(t, err.Error(), "non-patch version")
}

// TestEnsureProviderSelfUpgradeGetPatchConfigPatchCMMissing tests getPatchConfig with missing patch CM
func TestEnsureProviderSelfUpgradeGetPatchConfigPatchCMMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	patchKey := fmt.Sprintf("patch.%s", testTargetVersion)
	cluster := createBKECluster(testTargetVersion, "")
	existingObjs := []client.Object{
		createLocalCM(patchKey),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingObjs...).Build()
	ctx := createPhaseContext(fakeClient, scheme, cluster)
	phase := NewEnsureProviderSelfUpgrade(ctx).(*EnsureProviderSelfUpgrade)

	patchConfig, err := phase.getPatchConfig(cluster)
	assert.Error(t, err)
	assert.Nil(t, patchConfig)
}

// TestEnsureProviderSelfUpgradeGetProviderTargetImageSuccess tests getProviderTargetImage success
func TestEnsureProviderSelfUpgradeGetProviderTargetImageSuccess(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	patchKey := fmt.Sprintf("patch.%s", testTargetVersion)
	patchCMName := fmt.Sprintf("cm.%s", testTargetVersion)

	cluster := createBKECluster(testTargetVersion, "")
	existingObjs := []client.Object{
		createLocalCM(patchKey),
		createPatchCM(patchCMName, testTargetVersion, getValidPatchYaml()),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingObjs...).Build()
	ctx := createPhaseContext(fakeClient, scheme, cluster)
	phase := NewEnsureProviderSelfUpgrade(ctx).(*EnsureProviderSelfUpgrade)

	image, err := phase.getProviderTargetImage(cluster)
	assert.NoError(t, err)
	assert.Equal(t, testTargetImage, image)
}

// TestEnsureProviderSelfUpgradeGetProviderTargetImageError tests getProviderTargetImage error
func TestEnsureProviderSelfUpgradeGetProviderTargetImageError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cluster := createBKECluster(testTargetVersion, "")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := createPhaseContext(fakeClient, scheme, cluster)
	phase := NewEnsureProviderSelfUpgrade(ctx).(*EnsureProviderSelfUpgrade)

	image, err := phase.getProviderTargetImage(cluster)
	assert.Error(t, err)
	assert.Empty(t, image)
}

// TestEnsureProviderSelfUpgradeRolloutProviderContextCanceled tests context canceled scenario
func TestEnsureProviderSelfUpgradeRolloutProviderContextCanceled(t *testing.T) {
	t.Skip("Skipping - requires complex mocking")
}

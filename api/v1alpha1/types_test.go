/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestClusterVersionDeepCopy(t *testing.T) {
	now := metav1.Now()
	cv := &ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "default"},
		Spec: ClusterVersionSpec{
			DesiredVersion: "v26.03",
		},
		Status: ClusterVersionStatus{
			CurrentVersion: "v26.02",
			Phase:          ClusterVersionPhaseUpgrading,
			UpgradeHistory: []ClusterUpgradeRecord{{
				From:      "v26.02",
				To:        "v26.03",
				StartedAt: &now,
				Status:    ClusterUpgradeRecordStatusSucceeded,
			}},
			Conditions: []ClusterVersionCondition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: now,
			}},
		},
	}

	copied := cv.DeepCopy()
	copied.Status.UpgradeHistory[0].From = "changed"
	copied.Status.Conditions[0].Type = "Changed"

	if cv.Status.UpgradeHistory[0].From != "v26.02" {
		t.Fatalf("upgrade history was not deep copied")
	}
	if cv.Status.Conditions[0].Type != "Ready" {
		t.Fatalf("conditions were not deep copied")
	}
	assertRuntimeObject(t, cv.DeepCopyObject())
}

func TestReleaseImageDeepCopy(t *testing.T) {
	now := metav1.Now()
	ri := &ReleaseImage{
		ObjectMeta: metav1.ObjectMeta{Name: "release-v26.03", Namespace: "default"},
		Spec: ReleaseImageSpec{
			Version: "v26.03",
			Install: &ReleaseImageInstallSpec{
				Components: []ReleaseImageInstallComponent{{Name: "kube-apiserver", Version: "v1.32.0"}},
			},
			Upgrade: &ReleaseImageUpgradeSpec{
				Components: []ReleaseImageUpgradeComponent{{
					Name:    "kubelet",
					Version: "v1.32.0",
					Inline:  &ReleaseImageUpgradeInline{Handler: "restart", Version: "v1"},
				}},
			},
		},
		Status: ReleaseImageStatus{
			Phase:          ReleaseImagePhaseValid,
			ComponentCount: 1,
			Components:     []ComponentStatus{{Name: "kubelet", Version: "v1.32.0", Type: ComponentTypeInline}},
			ValidatedAt:    &now,
		},
	}

	copied := ri.DeepCopy()
	copied.Spec.Install.Components[0].Name = "changed"
	copied.Spec.Upgrade.Components[0].Inline.Handler = "changed"
	copied.Status.Components[0].Name = "changed"

	if ri.Spec.Install.Components[0].Name != "kube-apiserver" {
		t.Fatalf("install components were not deep copied")
	}
	if ri.Spec.Upgrade.Components[0].Inline.Handler != "restart" {
		t.Fatalf("upgrade inline handler was not deep copied")
	}
	if ri.Status.Components[0].Name != "kubelet" {
		t.Fatalf("status components were not deep copied")
	}
	assertRuntimeObject(t, ri.DeepCopyObject())
}

func TestUpgradePathDeepCopy(t *testing.T) {
	now := metav1.Now()
	up := &UpgradePath{
		ObjectMeta: metav1.ObjectMeta{Name: "upgrade-path"},
		Spec: UpgradePathSpec{
			Paths: []UpgradePathRule{{
				From:      "v26.02",
				To:        "v26.03",
				PostCheck: []CheckStep{{Name: "post-health", Required: true}},
			}},
			Versions: []VersionEntry{{Version: "v26.03"}},
		},
		Status: UpgradePathStatus{
			Phase:         UpgradePathPhaseActive,
			LastCheckedAt: &now,
			Conditions:    []metav1.Condition{{Type: "Validated", Status: metav1.ConditionTrue}},
		},
	}

	copied := up.DeepCopy()
	copied.Spec.Paths[0].PostCheck[0].Name = "changed"
	copied.Spec.Versions[0].Version = "changed"
	copied.Status.Conditions[0].Type = "Changed"

	if up.Spec.Paths[0].PostCheck[0].Name != "post-health" {
		t.Fatalf("path checks were not deep copied")
	}
	if up.Spec.Versions[0].Version != "v26.03" {
		t.Fatalf("versions were not deep copied")
	}
	if up.Status.Conditions[0].Type != "Validated" {
		t.Fatalf("conditions were not deep copied")
	}
	assertRuntimeObject(t, up.DeepCopyObject())
}

func TestComponentVersionDeepCopy(t *testing.T) {
	cv := &ComponentVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "kubelet"},
		Spec: ComponentVersionSpec{
			Name:          "kubelet",
			Type:          ComponentTypeInline,
			Version:       "v1.32.0",
			Inline:        &InlineSpec{Handler: "restart", Version: "v1"},
			SubComponents: []SubComponent{{Name: "kube-proxy", Version: "v1.32.0"}},
			Compatibility: CompatibilitySpec{Constraints: []Constraint{{Component: "containerd", Rule: ">=1.7"}}},
			Dependencies:  []Dependency{{Name: "containerd", Phase: "Ready"}},
			Resources:     []ResourceSpec{{Kind: "ConfigMap", APIVersion: "v1", Name: "kubelet", Labels: map[string]string{"app": "kubelet"}}},
		},
		Status: ComponentVersionStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}},
		},
	}

	copied := cv.DeepCopy()
	copied.Spec.Inline.Handler = "changed"
	copied.Spec.SubComponents[0].Name = "changed"
	copied.Spec.Compatibility.Constraints[0].Rule = "changed"
	copied.Spec.Dependencies[0].Name = "changed"
	copied.Spec.Resources[0].Labels["app"] = "changed"
	copied.Status.Conditions[0].Type = "Changed"

	if cv.Spec.Inline.Handler != "restart" {
		t.Fatalf("inline spec was not deep copied")
	}
	if cv.Spec.SubComponents[0].Name != "kube-proxy" {
		t.Fatalf("sub components were not deep copied")
	}
	if cv.Spec.Compatibility.Constraints[0].Rule != ">=1.7" {
		t.Fatalf("compatibility constraints were not deep copied")
	}
	if cv.Spec.Dependencies[0].Name != "containerd" {
		t.Fatalf("dependencies were not deep copied")
	}
	if cv.Spec.Resources[0].Labels["app"] != "kubelet" {
		t.Fatalf("resource labels were not deep copied")
	}
	if cv.Status.Conditions[0].Type != "Ready" {
		t.Fatalf("conditions were not deep copied")
	}
	assertRuntimeObject(t, cv.DeepCopyObject())
}

func TestListDeepCopyObjects(t *testing.T) {
	assertRuntimeObject(t, (&ClusterVersionList{Items: []ClusterVersion{{ObjectMeta: metav1.ObjectMeta{Name: "cluster-a"}}}}).DeepCopyObject())
	assertRuntimeObject(t, (&ReleaseImageList{Items: []ReleaseImage{{ObjectMeta: metav1.ObjectMeta{Name: "release-v26.03"}}}}).DeepCopyObject())
	assertRuntimeObject(t, (&UpgradePathList{Items: []UpgradePath{{ObjectMeta: metav1.ObjectMeta{Name: "upgrade-path"}}}}).DeepCopyObject())
	assertRuntimeObject(t, (&ComponentVersionList{Items: []ComponentVersion{{ObjectMeta: metav1.ObjectMeta{Name: "kubelet"}}}}).DeepCopyObject())
}

func assertRuntimeObject(t *testing.T, obj runtime.Object) {
	t.Helper()
	if obj == nil {
		t.Fatalf("expected runtime object")
	}
}

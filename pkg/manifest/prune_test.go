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

package manifest

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

func TestIsPruneable(t *testing.T) {
	tests := []struct {
		gvk  schema.GroupVersionKind
		want bool
	}{
		{gvk: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}, want: true},
		{gvk: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, want: true},
		{gvk: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}, want: false},
		{gvk: schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"}, want: false},
	}
	for _, tt := range tests {
		if got := IsPruneable(tt.gvk); got != tt.want {
			t.Fatalf("IsPruneable(%s)=%v, want %v", tt.gvk.String(), got, tt.want)
		}
	}
}

func TestPruneableGVKsExcludesCoreClusterObjects(t *testing.T) {
	for _, gvk := range PruneableGVKs() {
		if gvk.Kind == "Namespace" || gvk.Kind == "CustomResourceDefinition" {
			t.Fatalf("PruneableGVKs must not include %s", gvk.String())
		}
	}
}

func TestSelectStaleObjects(t *testing.T) {
	want := uObj("v1", "ConfigMap", "keep", "default")
	staleCandidate := uObj("v1", "ConfigMap", "old", "default")
	wantSet := buildWantSet([]unstructured.Unstructured{want})

	stale := selectStaleObjects([]unstructured.Unstructured{want, staleCandidate}, wantSet)
	if len(stale) != 1 || stale[0].GetName() != "old" {
		t.Fatalf("unexpected stale %#v", stale)
	}
}

func TestClusterApplier_PruneResources_NotConfigured(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{})
	err := a.PruneResources(context.Background(), map[string]string{"app": "x"}, "default", nil)
	if err == nil || err.Error() != "cluster manifest applier is not configured" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClusterApplier_PruneResources_EmptySelector(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{
		Client:     fake.NewClientBuilder().Build(),
		BKECluster: &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"}},
	})
	err := a.PruneResources(context.Background(), nil, "default", nil)
	if err == nil || err.Error() != "prune requires a non-empty label selector" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListPruneCandidates_SkipsNonPruneableKinds(t *testing.T) {
	scheme := runtime.NewScheme()
	keepCM := labeledObj("v1", "ConfigMap", "keep-cm", "default", map[string]string{"app": "demo"})
	oldCM := labeledObj("v1", "ConfigMap", "old-cm", "default", map[string]string{"app": "demo"})
	nsObj := labeledObj("v1", "Namespace", "ns-demo", "", map[string]string{"app": "demo"})

	dc := fakedynamic.NewSimpleDynamicClient(scheme, &keepCM, &oldCM, &nsObj)
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}, meta.RESTScopeRoot)

	sel, err := labels.Set{"app": "demo"}.AsValidatedSelector()
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := listPruneCandidates(context.Background(), mapper, dc, sel, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	names := map[string]bool{}
	for _, obj := range candidates {
		names[obj.GetName()] = true
		if obj.GetKind() == "Namespace" {
			t.Fatal("Namespace must not appear in prune candidates")
		}
	}
	if !names["keep-cm"] || !names["old-cm"] {
		t.Fatalf("expected configmaps in candidates, got %#v", names)
	}
}

func TestSelectStaleAfterList(t *testing.T) {
	keepCM := labeledObj("v1", "ConfigMap", "keep-cm", "default", map[string]string{"app": "demo"})
	oldCM := labeledObj("v1", "ConfigMap", "old-cm", "default", map[string]string{"app": "demo"})
	wantSet := buildWantSet([]unstructured.Unstructured{keepCM})
	stale := selectStaleObjects([]unstructured.Unstructured{keepCM, oldCM}, wantSet)
	if len(stale) != 1 || stale[0].GetName() != "old-cm" {
		t.Fatalf("unexpected stale %#v", stale)
	}
}

func TestPruneStaleResources_DeletesStaleKeepsWantSet(t *testing.T) {
	lbls := map[string]string{"app": "demo"}
	keepCM := labeledObj("v1", "ConfigMap", "keep-cm", "default", lbls)
	oldCM := labeledObj("v1", "ConfigMap", "old-cm", "default", lbls)

	wantObjects, err := collectRenderedObjects(&ComponentPackage{
		Name: "prune",
		Manifests: [][]byte{[]byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: keep-cm
  namespace: default
`)},
	}, nil)
	if err != nil {
		t.Fatalf("collect want objects: %v", err)
	}
	wantSet := buildWantSet(wantObjects)

	scheme := runtime.NewScheme()
	dc := fakedynamic.NewSimpleDynamicClient(scheme, &keepCM, &oldCM)
	mapper := configMapRESTMapper()
	sel, err := labels.Set(lbls).AsValidatedSelector()
	if err != nil {
		t.Fatal(err)
	}

	a := NewClusterApplier(ClusterApplierConfig{})
	if err := a.pruneStaleResources(pruneStaleInput{
		ctx:       context.Background(),
		mapper:    mapper,
		dc:        dc,
		selector:  sel,
		namespace: "default",
		wantSet:   wantSet,
	}); err != nil {
		t.Fatalf("prune: %v", err)
	}

	cmGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	if _, err := dc.Resource(cmGVR).Namespace("default").Get(context.Background(), "keep-cm", metav1.GetOptions{}); err != nil {
		t.Fatalf("want-set object must remain: %v", err)
	}
	_, err = dc.Resource(cmGVR).Namespace("default").Get(context.Background(), "old-cm", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("stale object must be deleted, get err=%v", err)
	}
}

func TestPruneStaleResources_EmptyStaleNoOp(t *testing.T) {
	lbls := map[string]string{"app": "demo"}
	keepCM := labeledObj("v1", "ConfigMap", "keep-cm", "default", lbls)

	wantObjects, err := collectRenderedObjects(&ComponentPackage{
		Name: "prune",
		Manifests: [][]byte{[]byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: keep-cm
  namespace: default
`)},
	}, nil)
	if err != nil {
		t.Fatalf("collect want objects: %v", err)
	}
	wantSet := buildWantSet(wantObjects)

	scheme := runtime.NewScheme()
	dc := fakedynamic.NewSimpleDynamicClient(scheme, &keepCM)
	mapper := configMapRESTMapper()
	sel, err := labels.Set(lbls).AsValidatedSelector()
	if err != nil {
		t.Fatal(err)
	}

	a := NewClusterApplier(ClusterApplierConfig{})
	if err := a.pruneStaleResources(pruneStaleInput{
		ctx:       context.Background(),
		mapper:    mapper,
		dc:        dc,
		selector:  sel,
		namespace: "default",
		wantSet:   wantSet,
	}); err != nil {
		t.Fatalf("prune empty stale: %v", err)
	}

	cmGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	if _, err := dc.Resource(cmGVR).Namespace("default").Get(context.Background(), "keep-cm", metav1.GetOptions{}); err != nil {
		t.Fatalf("object must remain when stale is empty: %v", err)
	}
}

func configMapRESTMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)
	return mapper
}

func labeledObj(apiVersion, kind, name, namespace string, lbls map[string]string) unstructured.Unstructured {
	obj := uObj(apiVersion, kind, name, namespace)
	obj.SetLabels(lbls)
	return obj
}

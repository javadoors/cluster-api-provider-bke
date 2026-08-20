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
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

func TestClusterApplier_DeleteComponent_NilPackage(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{})
	if err := a.DeleteComponent(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil package")
	}
}

func TestClusterApplier_DeleteComponent_EmptyManifests(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{})
	if err := a.DeleteComponent(context.Background(), &ComponentPackage{Name: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestClusterApplier_DeleteComponent_NotConfigured(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{
		BKECluster: &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"}},
	})
	err := a.DeleteComponent(context.Background(), &ComponentPackage{
		Name:      "coredns",
		Manifests: [][]byte{[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n")},
	})
	if err == nil || err.Error() != "cluster manifest applier is not configured" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollectRenderedObjects(t *testing.T) {
	pkg := &ComponentPackage{
		Name: "demo",
		Manifests: [][]byte{
			[]byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-a
  namespace: default
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dep-a
  namespace: default
`),
		},
	}
	objects, err := collectRenderedObjects(pkg, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}
}

func TestDeleteObjectsInUninstallOrder(t *testing.T) {
	objects := []unstructured.Unstructured{
		uObj("apiextensions.k8s.io/v1", "CustomResourceDefinition", "crds.example.com", ""),
		uObj("apps/v1", "Deployment", "dep", "default"),
		uObj("v1", "ConfigMap", "cm", "default"),
	}
	var order []string
	err := deleteObjectsInUninstallOrder(context.Background(), objects, func(_ context.Context, obj unstructured.Unstructured) error {
		order = append(order, obj.GetKind()+"/"+obj.GetName())
		return nil
	})
	if err != nil {
		t.Fatalf("delete order: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 deletes, got %v", order)
	}
	// Helm uninstall order deletes workloads before CRDs.
	crdIdx, depIdx := -1, -1
	for i, item := range order {
		if item == "CustomResourceDefinition/crds.example.com" {
			crdIdx = i
		}
		if item == "Deployment/dep" {
			depIdx = i
		}
	}
	if depIdx < 0 || crdIdx < 0 || depIdx > crdIdx {
		t.Fatalf("expected Deployment before CRD, got %v", order)
	}
}

func TestDeleteObjectsInUninstallOrder_PropagatesError(t *testing.T) {
	objects := []unstructured.Unstructured{
		uObj("v1", "ConfigMap", "cm", "default"),
	}
	err := deleteObjectsInUninstallOrder(context.Background(), objects, func(_ context.Context, _ unstructured.Unstructured) error {
		return fmt.Errorf("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped boom error, got %v", err)
	}
}

func TestDeleteOneObject_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	dc := fakedynamic.NewSimpleDynamicClient(scheme)
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "", Version: "v1"}})
	mapper.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)

	a := NewClusterApplier(ClusterApplierConfig{})
	obj := uObj("v1", "ConfigMap", "missing", "default")
	if err := a.deleteOneObject(context.Background(), mapper, dc, obj); err != nil {
		t.Fatalf("NotFound should be success, got %v", err)
	}
}

func TestDeleteOneObject_NoMatchSkip(t *testing.T) {
	scheme := runtime.NewScheme()
	dc := fakedynamic.NewSimpleDynamicClient(scheme)
	mapper := meta.NewDefaultRESTMapper(nil)

	a := NewClusterApplier(ClusterApplierConfig{})
	obj := uObj("monitoring.coreos.com/v1", "ServiceMonitor", "sm", "default")
	if err := a.deleteOneObject(context.Background(), mapper, dc, obj); err != nil {
		t.Fatalf("NoMatch should be skipped, got %v", err)
	}
}

func TestDeleteOneObject_DeletesExisting(t *testing.T) {
	scheme := runtime.NewScheme()
	existing := uObj("v1", "ConfigMap", "cm", "default")
	dc := fakedynamic.NewSimpleDynamicClient(scheme, &existing)
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "", Version: "v1"}})
	mapper.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)

	a := NewClusterApplier(ClusterApplierConfig{})
	if err := a.deleteOneObject(context.Background(), mapper, dc, existing); err != nil {
		t.Fatalf("delete existing: %v", err)
	}
	_, err := dc.Resource(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}).
		Namespace("default").Get(context.Background(), "cm", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected object deleted, get err=%v", err)
	}
}

func uObj(apiVersion, kind, name, namespace string) unstructured.Unstructured {
	obj := unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]interface{}{
			"name": name,
		},
	}}
	if namespace != "" {
		obj.SetNamespace(namespace)
	}
	return obj
}

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

	cvv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
	releasemanifest "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/release/manifest"
)

func TestBundleStore_GetComponentManifests(t *testing.T) {
	bundle := &releasemanifest.Bundle{
		Files: map[string][]byte{
			"components/provider/v1.0.0/01-deploy.yaml": []byte("apiVersion: apps/v1\nkind: Deployment"),
		},
		Components: map[string]cvv1alpha1.ComponentVersion{
			releasemanifest.ComponentKey("provider", "v1.0.0"): {
				Spec: cvv1alpha1.ComponentVersionSpec{
					Name:    "provider",
					Version: "v1.0.0",
					Resources: []cvv1alpha1.ResourceSpec{{
						Manifest: "apiVersion: v1\nkind: ConfigMap",
					}},
				},
			},
		},
	}
	store := NewBundleStore(bundle)
	pkg, err := store.GetComponentManifests(context.Background(), "provider", "v1.0.0", TemplateContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Manifests) != 2 {
		t.Fatalf("expected 2 manifests, got %d", len(pkg.Manifests))
	}
}

func TestBundleStore_MissingComponent(t *testing.T) {
	store := NewBundleStore(&releasemanifest.Bundle{Components: map[string]cvv1alpha1.ComponentVersion{}})
	_, err := store.GetComponentManifests(context.Background(), "missing", "v1", TemplateContext{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBundleStore_NotInitialized(t *testing.T) {
	for _, store := range []*BundleStore{nil, NewBundleStore(nil)} {
		_, err := store.GetComponentManifests(context.Background(), "provider", "v1", TemplateContext{})
		if err == nil {
			t.Fatal("expected uninitialized store error")
		}
	}
}

func TestBundleStore_GetComponentVersion(t *testing.T) {
	bundle := &releasemanifest.Bundle{
		Files: map[string][]byte{
			"components/coredns/v1.0.0/01-cm.yaml": []byte("apiVersion: v1\nkind: ConfigMap"),
		},
		Components: map[string]cvv1alpha1.ComponentVersion{
			releasemanifest.ComponentKey("coredns", "v1.0.0"): {
				Spec: cvv1alpha1.ComponentVersionSpec{
					Name:    "coredns",
					Version: "v1.0.0",
					Type:    cvv1alpha1.ComponentTypeYAML,
				},
			},
		},
	}
	store := NewBundleStore(bundle)
	cv, err := store.GetComponentVersion(context.Background(), "coredns", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if cv == nil || cv.Spec.Type != cvv1alpha1.ComponentTypeYAML {
		t.Fatalf("unexpected cv: %+v", cv)
	}
}

func TestBundleStore_GetComponentVersion_Missing(t *testing.T) {
	store := NewBundleStore(&releasemanifest.Bundle{Components: map[string]cvv1alpha1.ComponentVersion{}})
	_, err := store.GetComponentVersion(context.Background(), "missing", "v1")
	if err == nil {
		t.Fatal("expected missing component error")
	}
}

func TestBundleStore_GetComponentVersion_NotInitialized(t *testing.T) {
	for _, store := range []*BundleStore{nil, NewBundleStore(nil)} {
		_, err := store.GetComponentVersion(context.Background(), "coredns", "v1")
		if err == nil {
			t.Fatal("expected uninitialized store error")
		}
	}
}


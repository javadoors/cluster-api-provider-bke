/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
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

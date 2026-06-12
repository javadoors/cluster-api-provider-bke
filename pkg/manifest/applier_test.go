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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

func TestClusterApplier_ApplyComponent_NilPackage(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{})
	if err := a.ApplyComponent(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil package")
	}
}

func TestClusterApplier_ApplyComponent_EmptyManifests(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{})
	if err := a.ApplyComponent(context.Background(), &ComponentPackage{Name: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestClusterApplier_imageRepo(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{
		BKECluster: &bkev1beta1.BKECluster{
			Spec: confv1beta1.BKEClusterSpec{
				ClusterConfig: &confv1beta1.BKEConfig{
					Cluster: confv1beta1.Cluster{
						ImageRepo: confv1beta1.Repo{
							Domain: "registry.example.com",
							Port:   "443",
							Prefix: "openfuyao",
						},
					},
				},
			},
		},
	})
	if got := a.imageRepo(); got == "" {
		t.Fatalf("expected non-empty image repo, got %q", got)
	}
}

func TestFallbackRenderParams(t *testing.T) {
	params := fallbackRenderParams(&bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "bke-system"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{
					KubernetesVersion: "v1.29.0",
					OpenFuyaoVersion:  "v2.6.0",
				},
			},
		},
	})
	if params["namespace"] != "bke-system" {
		t.Fatalf("namespace: %v", params["namespace"])
	}
	if params["kubernetesVersion"] != "v1.29.0" {
		t.Fatalf("kubernetesVersion: %v", params["kubernetesVersion"])
	}
}

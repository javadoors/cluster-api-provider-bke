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

package dagexec

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	cvv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/manifest"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/topology"
)

type skipManifestStore struct{}

func (skipManifestStore) GetComponentManifests(context.Context, string, string, manifest.TemplateContext) (*manifest.ComponentPackage, error) {
	return &manifest.ComponentPackage{
		Name:      "coredns",
		Version:   "v1.0.0",
		Manifests: [][]byte{[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n")},
	}, nil
}

type skipManifestApplier struct{}

func (skipManifestApplier) ApplyComponent(context.Context, *manifest.ComponentPackage) error {
	return manifest.NewSkipNotInstalledError("coredns")
}

func TestExecuteDAG_SkipNotInstalled_DoesNotMarkCompleted(t *testing.T) {
	bc := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"},
		Status: confv1beta1.BKEClusterStatus{
			DeclarativeUpgrade: &confv1beta1.DeclarativeUpgradeStatus{
				TargetVersion: "v2",
			},
		},
	}
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	phaseCtx := phaseframe.NewReconcilePhaseCtx(context.Background())
	phaseCtx.SetBKECluster(bc)
	phaseCtx.SetClient(fake.NewClientBuilder().WithScheme(scheme).WithObjects(bc).WithStatusSubresource(bc).Build())

	dag, err := topology.BuildUpgradeDAG(
		[]cvv1alpha1.ReleaseImageUpgradeComponent{{Name: "coredns", Version: "v1.0.0"}},
		topology.DefaultDependencyResolver(),
	)
	if err != nil {
		t.Fatal(err)
	}

	sched := NewScheduler(Config{
		ManifestStore:   skipManifestStore{},
		ManifestApplier: skipManifestApplier{},
	})
	if err := sched.ExecuteDAG(context.Background(), phaseCtx, bc, bc, dag); err != nil {
		t.Fatal(err)
	}
	if len(bc.Status.DeclarativeUpgrade.Completed) != 0 {
		t.Fatalf("expected no completed records, got %v", bc.Status.DeclarativeUpgrade.Completed)
	}
}

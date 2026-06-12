/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 ******************************************************************/

package clusterversion

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	cvv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
)

func TestReleaseImageRefForVersion(t *testing.T) {
	if got := ReleaseImageRefForVersion(" v26.03 "); got != "release-v26.03" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenFuyaoVersionForBKECluster(t *testing.T) {
	bc := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "spec-ver"},
			},
		},
		Status: confv1beta1.BKEClusterStatus{OpenFuyaoVersion: "status-ver"},
	}
	if got := OpenFuyaoVersionForBKECluster(bc); got != "status-ver" {
		t.Fatalf("got %q", got)
	}
}

func TestEnsureClusterVersionForBKECluster_CreatesOnInstall(t *testing.T) {
	bc := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns", UID: "uid-1"},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v2.6.0"},
			},
		},
	}
	scheme := runtime.NewScheme()
	_ = cvv1alpha1.AddToScheme(scheme)
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bc).Build()

	created, err := EnsureClusterVersionForBKECluster(context.Background(), c, scheme, bc)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}

	cv := &cvv1alpha1.ClusterVersion{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "c1"}, cv); err != nil {
		t.Fatal(err)
	}
	if cv.Spec.DesiredVersion != "v2.6.0" {
		t.Fatalf("desired %q", cv.Spec.DesiredVersion)
	}
	if cv.Status.Phase != "" {
		t.Fatalf("status phase should be empty on create, got %q", cv.Status.Phase)
	}
	if len(cv.OwnerReferences) != 1 || cv.OwnerReferences[0].Kind != "BKECluster" {
		t.Fatalf("owner refs %+v", cv.OwnerReferences)
	}

	created, err = EnsureClusterVersionForBKECluster(context.Background(), c, scheme, bc)
	if err != nil || created {
		t.Fatalf("second ensure created=%v err=%v", created, err)
	}
}

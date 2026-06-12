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

package clusterversion

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	cvv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
)

func TestGetClusterVersionForBKECluster_OwnerReference(t *testing.T) {
	bc := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns", UID: "bc-uid"},
	}
	cv := &cvv1alpha1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cv-other",
			Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: bkev1beta1.GroupVersion.String(),
				Kind:       "BKECluster",
				Name:       "c1",
				UID:        "bc-uid",
			}},
		},
		Spec: cvv1alpha1.ClusterVersionSpec{DesiredVersion: "v1"},
	}
	scheme := runtime.NewScheme()
	_ = cvv1alpha1.AddToScheme(scheme)
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cv, bc).Build()

	got, err := GetClusterVersionForBKECluster(context.Background(), c, bc)
	if err != nil || got.Name != "cv-other" {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestGetClusterVersionForBKECluster_SameName(t *testing.T) {
	bc := &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"}}
	cv := &cvv1alpha1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "ns"},
	}
	scheme := runtime.NewScheme()
	_ = cvv1alpha1.AddToScheme(scheme)
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cv, bc).Build()

	got, err := GetClusterVersionForBKECluster(context.Background(), c, bc)
	if err != nil || got.Name != "c1" {
		t.Fatalf("got %v err %v", got, err)
	}
}

/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 ******************************************************************/

package clusterversion

import (
	"testing"

	cvv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/release/imageref"
)

func TestReleaseImageCRFromParsedUsesMetadataName(t *testing.T) {
	parsed := &cvv1alpha1.ReleaseImage{
		Spec: cvv1alpha1.ReleaseImageSpec{Version: "v26.05"},
	}
	parsed.Name = "release-v26.05"
	ri := releaseImageCRFromParsed(parsed, "ns", "cluster-a")
	if ri.Name != "release-v26.05" {
		t.Fatalf("name %q", ri.Name)
	}
	if ri.Annotations[imageref.AnnotationBKEClusterName] != "cluster-a" {
		t.Fatalf("annotation %v", ri.Annotations)
	}
}

func TestReleaseImageCRFromParsedFallbackName(t *testing.T) {
	parsed := &cvv1alpha1.ReleaseImage{
		Spec: cvv1alpha1.ReleaseImageSpec{Version: "v26.05"},
	}
	ri := releaseImageCRFromParsed(parsed, "ns", "cluster-a")
	if ri.Name != "release-v26.05" {
		t.Fatalf("name %q", ri.Name)
	}
}

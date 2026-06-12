/*
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 */

package manifest

import (
	"testing"

	apiv1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
)

func TestCollectComponentManifests_FromBundleFiles(t *testing.T) {
	bundle := &Bundle{
		Files: map[string][]byte{
			"release.yaml": []byte("kind: ReleaseImage"),
			"components/provider/v1.0.0/component.yaml": []byte("kind: ComponentVersion"),
			"components/provider/v1.0.0/02-rbac.yaml":     []byte("kind: ClusterRole"),
			"components/provider/v1.0.0/01-cm.yaml":       []byte("kind: ConfigMap"),
			"components/other/v1.0.0/01-cm.yaml":                []byte("kind: Secret"),
		},
		Components: map[string]apiv1.ComponentVersion{
			ComponentKey("provider", "v1.0.0"): {
				Spec: apiv1.ComponentVersionSpec{
					Name:    "provider",
					Version: "v1.0.0",
					Resources: []apiv1.ResourceSpec{{
						Manifest: "kind: Service",
					}},
				},
			},
		},
	}

	manifests := CollectComponentManifests(bundle, "provider", "v1.0.0")
	if len(manifests) != 3 {
		t.Fatalf("got %d manifests, want 3", len(manifests))
	}
	if string(manifests[0]) != "kind: ConfigMap" {
		t.Fatalf("first manifest order: %q", manifests[0])
	}
	if string(manifests[1]) != "kind: ClusterRole" {
		t.Fatalf("second manifest: %q", manifests[1])
	}
	if string(manifests[2]) != "kind: Service" {
		t.Fatalf("inline manifest: %q", manifests[2])
	}
}

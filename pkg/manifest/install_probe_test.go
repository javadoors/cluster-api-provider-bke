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

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/upgrade"
)

func TestIsComponentInstalled_KubeProxy(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()

	installed, err := IsComponentInstalled(ctx, client, upgrade.ComponentKubeProxy, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("expected not installed")
	}

	_, err = client.AppsV1().DaemonSets(metav1.NamespaceSystem).Create(ctx, &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-proxy", Namespace: metav1.NamespaceSystem},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	installed, err = IsComponentInstalled(ctx, client, upgrade.ComponentKubeProxy, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("expected installed")
	}
}

func TestIsComponentInstalled_FromManifestYAML(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	manifests := [][]byte{[]byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-addon
  namespace: kube-system
`)}

	installed, err := IsComponentInstalled(ctx, client, "custom-addon", manifests, nil)
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("expected not installed")
	}

	_, err = client.AppsV1().Deployments("kube-system").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-addon", Namespace: "kube-system"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	installed, err = IsComponentInstalled(ctx, client, "custom-addon", manifests, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("expected installed from manifest anchor")
	}
}

func TestWorkloadRefsFromManifests_EmptyAllowsApply(t *testing.T) {
	refs := workloadRefsFromManifests([][]byte{[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n")})
	if len(refs) != 0 {
		t.Fatalf("expected no workload refs, got %v", refs)
	}
}

func TestIsComponentInstalled_TemplatedProviderManifest(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	manifests := [][]byte{[]byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: bke-controller-manager
  namespace: cluster-system
spec:
  template:
    spec:
      containers:
      - name: manager
        image: {{ if .repo }}{{ .repo }}{{ else }}cr.openfuyao.cn/openfuyao/{{ end }}cluster-api-provider-bke:{{.providerVersion}}
`)}
	params := map[string]interface{}{
		"repo":             "registry.example.com/kubernetes/",
		"providerVersion":  "v26.07",
	}

	installed, err := IsComponentInstalled(ctx, client, upgrade.ComponentProvider, manifests, params)
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("expected not installed before apply")
	}

	_, err = client.AppsV1().Deployments("cluster-system").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "bke-controller-manager", Namespace: "cluster-system"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	installed, err = IsComponentInstalled(ctx, client, upgrade.ComponentProvider, manifests, params)
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("expected installed after deployment exists")
	}
}

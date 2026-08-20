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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

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

func TestIsComponentInstalled_ErrorBranches(t *testing.T) {
	if _, err := IsComponentInstalled(context.Background(), nil, "component", nil, nil); err == nil {
		t.Fatal("expected nil client error")
	}

	client := fake.NewSimpleClientset()
	client.Fake.PrependReactor("get", "deployments", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errBoom{})
	})
	_, err := IsComponentInstalled(
		context.Background(),
		client,
		"component",
		[][]byte{[]byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: bad\n")},
		nil,
	)
	if err == nil {
		t.Fatal("expected workload get error")
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

func TestWorkloadRefsForComponent(t *testing.T) {
	if refs := workloadRefsForComponent(upgrade.ComponentCoreDNS, nil); len(refs) != 1 || refs[0].name != "coredns" {
		t.Fatalf("unexpected coredns refs: %#v", refs)
	}
	if refs := knownWorkloadRefs("unknown"); refs != nil {
		t.Fatalf("expected nil known refs")
	}
}

func TestWorkloadRefsFromYAML_MultipleDocsAndDefaults(t *testing.T) {
	doc := []byte(`apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: db
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: ignored
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: apps
---
apiVersion: apps/v1
kind: Deployment
metadata:
  namespace: apps
`)
	refs := workloadRefsFromYAML(doc)
	if len(refs) != 2 {
		t.Fatalf("expected 2 workload refs, got %#v", refs)
	}
	if refs[0] != (workloadRef{kind: "StatefulSet", namespace: metav1.NamespaceDefault, name: "db"}) {
		t.Fatalf("unexpected statefulset ref: %#v", refs[0])
	}
	if refs[1] != (workloadRef{kind: "Deployment", namespace: "apps", name: "web"}) {
		t.Fatalf("unexpected deployment ref: %#v", refs[1])
	}
}

func TestWorkloadExists(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
	})
	exists, err := workloadExists(context.Background(), client, workloadRef{kind: "StatefulSet", namespace: "default", name: "db"})
	if err != nil || !exists {
		t.Fatalf("statefulset exists=%v err=%v", exists, err)
	}
	exists, err = workloadExists(context.Background(), client, workloadRef{kind: "StatefulSet", namespace: "default", name: "missing"})
	if err != nil || exists {
		t.Fatalf("missing statefulset exists=%v err=%v", exists, err)
	}
	exists, err = workloadExists(context.Background(), client, workloadRef{kind: "Unknown", namespace: "default", name: "x"})
	if err == nil || exists {
		t.Fatalf("expected unsupported kind error, exists=%v err=%v", exists, err)
	}
}

func TestWorkloadRefsFromManifests_Deduplicates(t *testing.T) {
	manifest := []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: apps\n")
	refs := workloadRefsFromManifests([][]byte{manifest, manifest})
	if len(refs) != 1 {
		t.Fatalf("expected deduplicated ref, got %#v", refs)
	}
}

var _ = schema.GroupVersionResource{}

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
		"repo":            "registry.example.com/kubernetes/",
		"providerVersion": "v26.07",
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

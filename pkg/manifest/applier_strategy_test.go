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
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeaddon "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/addon"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/log"
)

var errApplyYamlBoom = errors.New("boom")

// recordingRemoteKube records ApplyYaml tasks; other RemoteKubeClient methods are unused.
type recordingRemoteKube struct {
	tasks []*kube.Task
	err   error
}

func (r *recordingRemoteKube) ApplyYaml(task *kube.Task) error {
	cp := *task
	r.tasks = append(r.tasks, &cp)
	return r.err
}

func (r *recordingRemoteKube) InstallAddon(
	*bkev1beta1.BKECluster,
	*bkeaddon.AddonTransfer,
	*kube.AddonRecorder,
	client.Client,
	bkenode.Nodes,
) error {
	return nil
}
func (r *recordingRemoteKube) NewK8sToken() (string, error) { return "", nil }
func (r *recordingRemoteKube) KubeClient() (*kubernetes.Clientset, dynamic.Interface) {
	return nil, nil
}
func (r *recordingRemoteKube) Collect() (*kube.CollectResult, []error, []error) {
	return nil, nil, nil
}
func (r *recordingRemoteKube) CheckClusterHealth(*bkev1beta1.BKECluster, string, bkev1beta1.BKENodes) error {
	return nil
}
func (r *recordingRemoteKube) NodeHealthCheck(*corev1.Node, string, *log.Logger) error {
	return nil
}
func (r *recordingRemoteKube) CheckComponentHealth(*corev1.Node) error { return nil }
func (r *recordingRemoteKube) ListNodes(*metav1.ListOptions) (*corev1.NodeList, error) {
	return &corev1.NodeList{}, nil
}
func (r *recordingRemoteKube) GetPod(string, string) (*corev1.Pod, error) { return nil, nil }
func (r *recordingRemoteKube) SetLogger(*log.Logger)                     {}
func (r *recordingRemoteKube) SetBKELogger(*bkev1beta1.BKELogger)         {}

func TestApplyPackageManifests_Strategies(t *testing.T) {
	doc := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n")
	pkg := &ComponentPackage{Name: "demo", Manifests: [][]byte{doc}}
	a := NewClusterApplier(ClusterApplierConfig{
		BKECluster: &bkev1beta1.BKECluster{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"}},
	})

	tests := []struct {
		name     string
		strategy string
		wantOps  []bkeaddon.AddonOperate
		wantStr  []string
	}{
		{
			name:     "server-side apply",
			strategy: ApplyStrategyServerSideApply,
			wantOps:  []bkeaddon.AddonOperate{bkeaddon.UpgradeAddon},
			wantStr:  []string{""},
		},
		{
			name:     "empty defaults to SSA",
			strategy: "",
			wantOps:  []bkeaddon.AddonOperate{bkeaddon.UpgradeAddon},
			wantStr:  []string{""},
		},
		{
			name:     "replace delete then create",
			strategy: ApplyStrategyReplace,
			wantOps:  []bkeaddon.AddonOperate{bkeaddon.RemoveAddon, bkeaddon.CreateAddon},
			wantStr:  []string{"", ""},
		},
		{
			name:     "create only",
			strategy: ApplyStrategyCreateOnly,
			wantOps:  []bkeaddon.AddonOperate{bkeaddon.UpgradeAddon},
			wantStr:  []string{ApplyStrategyCreateOnly},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recordingRemoteKube{}
			pkg.ApplyStrategy = tt.strategy
			if err := a.applyPackageManifests(rec, pkg, nil); err != nil {
				t.Fatalf("applyPackageManifests: %v", err)
			}
			if len(rec.tasks) != len(tt.wantOps) {
				t.Fatalf("tasks=%d want %d", len(rec.tasks), len(tt.wantOps))
			}
			for i, task := range rec.tasks {
				if task.Operate != tt.wantOps[i] {
					t.Fatalf("task[%d] operate=%v want %v", i, task.Operate, tt.wantOps[i])
				}
				if task.ApplyStrategy != tt.wantStr[i] {
					t.Fatalf("task[%d] strategy=%q want %q", i, task.ApplyStrategy, tt.wantStr[i])
				}
				if string(task.ManifestContent) != string(doc) {
					t.Fatalf("task[%d] unexpected manifest content", i)
				}
			}
		})
	}
}

func TestApplyPackageManifests_UnsupportedStrategy(t *testing.T) {
	a := NewClusterApplier(ClusterApplierConfig{})
	err := a.applyPackageManifests(&recordingRemoteKube{}, &ComponentPackage{
		Name:          "demo",
		ApplyStrategy: "NoSuchStrategy",
		Manifests:     [][]byte{[]byte("x")},
	}, nil)
	if err == nil {
		t.Fatal("expected unsupported strategy error")
	}
}

func TestApplyPackageManifests_PropagatesApplyYamlError(t *testing.T) {
	rec := &recordingRemoteKube{err: errApplyYamlBoom}
	a := NewClusterApplier(ClusterApplierConfig{})
	err := a.applyPackageManifests(rec, &ComponentPackage{
		Name:      "demo",
		Manifests: [][]byte{[]byte("apiVersion: v1\nkind: ConfigMap\n")},
	}, nil)
	if err == nil {
		t.Fatal("expected ApplyYaml error")
	}
}

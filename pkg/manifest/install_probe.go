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
	"bytes"
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/upgrade"
)

// workloadRef identifies a namespaced controller workload in the target cluster.
type workloadRef struct {
	kind      string
	namespace string
	name      string
}

// IsComponentInstalled reports whether the upgrade target cluster already has the
// component's primary workload. Manifest upgrades are skipped when false so that
// release YAML is not applied onto clusters that never installed the addon.
// params must match ApplyYaml template parameters so workload anchors are resolved
// from rendered manifests, not raw release bundle templates.
func IsComponentInstalled(
	ctx context.Context,
	clientset kubernetes.Interface,
	componentName string,
	manifests [][]byte,
	params map[string]interface{},
) (bool, error) {
	if clientset == nil {
		return false, fmt.Errorf("kubernetes clientset is required")
	}
	probeManifests, err := renderManifestsForProbe(componentName, manifests, params)
	if err != nil {
		return false, err
	}
	refs := workloadRefsForComponent(componentName, probeManifests)
	if len(refs) == 0 {
		// No detectable workload anchor; keep legacy apply behaviour.
		return true, nil
	}
	for _, ref := range refs {
		installed, err := workloadExists(ctx, clientset, ref)
		if err != nil {
			return false, err
		}
		if installed {
			return true, nil
		}
	}
	return false, nil
}

func workloadRefsForComponent(componentName string, manifests [][]byte) []workloadRef {
	if refs := knownWorkloadRefs(componentName); len(refs) > 0 {
		return refs
	}
	return workloadRefsFromManifests(manifests)
}

func knownWorkloadRefs(componentName string) []workloadRef {
	switch componentName {
	case upgrade.ComponentKubeProxy:
		return []workloadRef{{kind: "DaemonSet", namespace: metav1.NamespaceSystem, name: "kube-proxy"}}
	case upgrade.ComponentCoreDNS:
		return []workloadRef{{kind: "Deployment", namespace: metav1.NamespaceSystem, name: "coredns"}}
	case upgrade.ComponentProvider:
		return []workloadRef{{kind: "Deployment", namespace: "cluster-system", name: "bke-controller-manager"}}
	default:
		return nil
	}
}

func renderManifestsForProbe(componentName string, manifests [][]byte, params map[string]interface{}) ([][]byte, error) {
	if len(manifests) == 0 {
		return nil, nil
	}
	out := make([][]byte, 0, len(manifests))
	for i, doc := range manifests {
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		rendered, err := kube.RenderManifest(fmt.Sprintf("%s-probe-%d", componentName, i), doc, params)
		if err != nil {
			return nil, fmt.Errorf("render manifest %d for probe: %w", i, err)
		}
		out = append(out, rendered)
	}
	return out, nil
}

func workloadRefsFromManifests(manifests [][]byte) []workloadRef {
	var refs []workloadRef
	seen := make(map[string]struct{})
	for _, doc := range manifests {
		for _, ref := range workloadRefsFromYAML(doc) {
			key := ref.kind + "/" + ref.namespace + "/" + ref.name
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

func workloadRefsFromYAML(doc []byte) []workloadRef {
	if len(bytes.TrimSpace(doc)) == 0 {
		return nil
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(doc), 4096)
	var refs []workloadRef
	for {
		raw := map[string]interface{}{}
		if err := decoder.Decode(&raw); err != nil {
			break
		}
		if len(raw) == 0 {
			continue
		}
		u := &unstructured.Unstructured{Object: raw}
		kind := u.GetKind()
		switch kind {
		case "DaemonSet", "Deployment", "StatefulSet":
			ns := u.GetNamespace()
			if ns == "" {
				ns = metav1.NamespaceDefault
			}
			name := u.GetName()
			if name == "" {
				continue
			}
			refs = append(refs, workloadRef{kind: kind, namespace: ns, name: name})
		default:
			continue
		}
	}
	return refs
}

func workloadExists(ctx context.Context, clientset kubernetes.Interface, ref workloadRef) (bool, error) {
	var err error
	switch ref.kind {
	case "DaemonSet":
		_, err = clientset.AppsV1().DaemonSets(ref.namespace).Get(ctx, ref.name, metav1.GetOptions{})
	case "Deployment":
		_, err = clientset.AppsV1().Deployments(ref.namespace).Get(ctx, ref.name, metav1.GetOptions{})
	case "StatefulSet":
		_, err = clientset.AppsV1().StatefulSets(ref.namespace).Get(ctx, ref.name, metav1.GetOptions{})
	default:
		return false, fmt.Errorf("unsupported workload kind %q", ref.kind)
	}
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// SkipReasonNotInstalled is the log message reason when a manifest upgrade is skipped.
const SkipReasonNotInstalled = "component not installed in target cluster"

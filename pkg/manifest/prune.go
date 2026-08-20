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
	"fmt"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// PruneableGVKs returns the allowlist of resources that PruneResources may delete.
// CRD, Namespace, and other core cluster objects are intentionally excluded.
func PruneableGVKs() []schema.GroupVersionKind {
	return []schema.GroupVersionKind{
		{Group: "", Version: "v1", Kind: "ConfigMap"},
		{Group: "", Version: "v1", Kind: "Secret"},
		{Group: "", Version: "v1", Kind: "Service"},
		{Group: "", Version: "v1", Kind: "ServiceAccount"},
		{Group: "apps", Version: "v1", Kind: "Deployment"},
		{Group: "apps", Version: "v1", Kind: "DaemonSet"},
		{Group: "apps", Version: "v1", Kind: "StatefulSet"},
		{Group: "batch", Version: "v1", Kind: "Job"},
		{Group: "batch", Version: "v1", Kind: "CronJob"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"},
		{Group: "policy", Version: "v1", Kind: "PodDisruptionBudget"},
		{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
		{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"},
	}
}

// IsPruneable reports whether gvk is in PruneableGVKs (match by group + kind).
func IsPruneable(gvk schema.GroupVersionKind) bool {
	for _, allowed := range PruneableGVKs() {
		if allowed.Group == gvk.Group && allowed.Kind == gvk.Kind {
			return true
		}
	}
	return false
}

type pruneStaleInput struct {
	ctx       context.Context
	mapper    meta.RESTMapper
	dc        dynamic.Interface
	selector  labels.Selector
	namespace string
	wantSet   map[string]struct{}
}

// PruneResources deletes pruneable objects that match selector but are not in currentManifests.
func (a *ClusterApplier) PruneResources(
	ctx context.Context,
	selector map[string]string,
	namespace string,
	currentManifests [][]byte,
) error {
	if a == nil || a.client == nil || a.bkeCluster == nil {
		return errors.New("cluster manifest applier is not configured")
	}
	if len(selector) == 0 {
		return errors.New("prune requires a non-empty label selector")
	}

	kubeClient, params, err := a.prepareManifestOperation(ctx, &ComponentPackage{Name: "prune"})
	if err != nil {
		return errors.Wrap(err, "prepare manifest applier for prune")
	}

	wantObjects, err := collectRenderedObjects(&ComponentPackage{
		Name:      "prune",
		Manifests: currentManifests,
	}, params)
	if err != nil {
		return errors.Wrap(err, "collect want-set objects for prune")
	}
	wantSet := buildWantSet(wantObjects)

	clientset, dynamicClient := kubeClient.KubeClient()
	if clientset == nil || dynamicClient == nil {
		return errors.New("remote clients are nil")
	}
	mapper, err := newDiscoveryRESTMapper(clientset)
	if err != nil {
		return errors.Wrap(err, "build RESTMapper for prune")
	}

	labelSelector, err := labels.Set(selector).AsValidatedSelector()
	if err != nil {
		return errors.Wrap(err, "invalid prune label selector")
	}

	pruneCtx := a.ctx
	if pruneCtx == nil {
		pruneCtx = ctx
	}

	return a.pruneStaleResources(pruneStaleInput{
		ctx:       pruneCtx,
		mapper:    mapper,
		dc:        dynamicClient,
		selector:  labelSelector,
		namespace: namespace,
		wantSet:   wantSet,
	})
}

// pruneStaleResources lists prune candidates, diffs against wantSet, and deletes stale objects.
func (a *ClusterApplier) pruneStaleResources(in pruneStaleInput) error {
	candidates, err := listPruneCandidates(in.ctx, in.mapper, in.dc, in.selector, in.namespace)
	if err != nil {
		return errors.Wrap(err, "list prune candidates")
	}

	stale := selectStaleObjects(candidates, in.wantSet)
	if len(stale) == 0 {
		return nil
	}

	return deleteObjectsInUninstallOrder(in.ctx, stale, func(ctx context.Context, obj unstructured.Unstructured) error {
		return a.deleteOneObject(ctx, in.mapper, in.dc, obj)
	})
}

func selectStaleObjects(candidates []unstructured.Unstructured, wantSet map[string]struct{}) []unstructured.Unstructured {
	var stale []unstructured.Unstructured
	for _, obj := range candidates {
		if _, keep := wantSet[objectKey(obj)]; keep {
			continue
		}
		stale = append(stale, obj)
	}
	return stale
}

func buildWantSet(objects []unstructured.Unstructured) map[string]struct{} {
	set := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		set[objectKey(obj)] = struct{}{}
	}
	return set
}

func objectKey(obj unstructured.Unstructured) string {
	gvk := obj.GroupVersionKind()
	return fmt.Sprintf("%s/%s/%s/%s", gvk.Group, gvk.Kind, obj.GetNamespace(), obj.GetName())
}

func listPruneCandidates(
	ctx context.Context,
	mapper meta.RESTMapper,
	dc dynamic.Interface,
	selector labels.Selector,
	namespace string,
) ([]unstructured.Unstructured, error) {
	listOpts := metav1.ListOptions{LabelSelector: selector.String()}
	var out []unstructured.Unstructured

	for _, gvk := range PruneableGVKs() {
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			if meta.IsNoMatchError(err) {
				continue
			}
			return nil, errors.Wrapf(err, "RESTMapping for pruneable %s", gvk.String())
		}

		list, err := listPruneableResources(ctx, dc, mapping, namespace, listOpts)
		if err != nil {
			if meta.IsNoMatchError(err) {
				continue
			}
			return nil, errors.Wrapf(err, "list %s for prune", gvk.String())
		}
		if list == nil || len(list.Items) == 0 {
			continue
		}
		out = append(out, list.Items...)
	}
	return out, nil
}

func listPruneableResources(
	ctx context.Context,
	dc dynamic.Interface,
	mapping *meta.RESTMapping,
	namespace string,
	listOpts metav1.ListOptions,
) (*unstructured.UnstructuredList, error) {
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ns := namespace
		if ns == "" {
			ns = metav1.NamespaceAll
		}
		return dc.Resource(mapping.Resource).Namespace(ns).List(ctx, listOpts)
	}
	return dc.Resource(mapping.Resource).List(ctx, listOpts)
}

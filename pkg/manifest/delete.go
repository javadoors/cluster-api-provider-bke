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
	"io"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/addonutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

// DeleteComponent removes package resources in uninstall order.
// NotFound is treated as success. Unresolvable GVRs are warned and skipped.
func (a *ClusterApplier) DeleteComponent(ctx context.Context, pkg *ComponentPackage) error {
	session, err := a.openManifestPackage(ctx, pkg)
	if err != nil || session == nil {
		return err
	}

	objects, err := collectRenderedObjects(pkg, session.params)
	if err != nil {
		return errors.Wrapf(err, "component %s: collect objects for delete", pkg.Name)
	}
	if len(objects) == 0 {
		return nil
	}

	clientset, dynamicClient := session.kubeClient.KubeClient()
	if clientset == nil || dynamicClient == nil {
		return fmt.Errorf("component %s: remote clients are nil", pkg.Name)
	}
	mapper, err := newDiscoveryRESTMapper(clientset)
	if err != nil {
		return errors.Wrapf(err, "component %s: build RESTMapper", pkg.Name)
	}

	deleteCtx := a.ctx
	if deleteCtx == nil {
		deleteCtx = ctx
	}
	return deleteObjectsInUninstallOrder(deleteCtx, objects, func(ctx context.Context, obj unstructured.Unstructured) error {
		return a.deleteOneObject(ctx, mapper, dynamicClient, obj)
	})
}

func collectRenderedObjects(pkg *ComponentPackage, params map[string]interface{}) ([]unstructured.Unstructured, error) {
	var objects []unstructured.Unstructured
	for i, doc := range pkg.Manifests {
		chunk, err := objectsFromManifestDoc(pkg.Name, i, doc, params)
		if err != nil {
			return nil, err
		}
		objects = append(objects, chunk...)
	}
	return objects, nil
}

func objectsFromManifestDoc(
	pkgName string,
	idx int,
	doc []byte,
	params map[string]interface{},
) ([]unstructured.Unstructured, error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return nil, nil
	}
	rendered, err := kube.RenderManifest(fmt.Sprintf("%s-delete-%d", pkgName, idx), doc, params)
	if err != nil {
		return nil, errors.Wrapf(err, "render manifest %d", idx)
	}
	decoded, err := decodeYAMLObjects(rendered)
	if err != nil {
		return nil, errors.Wrapf(err, "decode manifest %d", idx)
	}

	var objects []unstructured.Unstructured
	for _, obj := range decoded {
		if addonutil.IsListKind(obj.GetKind()) {
			items, err := addonutil.UnwrapList(obj)
			if err != nil {
				return nil, errors.Wrapf(err, "unwrap list in manifest %d", idx)
			}
			objects = append(objects, items...)
			continue
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

func decodeYAMLObjects(doc []byte) ([]unstructured.Unstructured, error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return nil, nil
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(doc), 4096)
	var out []unstructured.Unstructured
	for {
		raw := map[string]interface{}{}
		err := decoder.Decode(&raw)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Wrap(err, "decode yaml object")
		}
		if len(raw) == 0 {
			continue
		}
		out = append(out, unstructured.Unstructured{Object: raw})
	}
	return out, nil
}

func deleteObjectsInUninstallOrder(
	ctx context.Context,
	objects []unstructured.Unstructured,
	deleteFn func(context.Context, unstructured.Unstructured) error,
) error {
	if len(objects) == 0 {
		return nil
	}
	ordered := addonutil.SortUninstallUnstructuredByKind(append([]unstructured.Unstructured(nil), objects...))
	for i := range ordered {
		obj := ordered[i]
		if err := deleteFn(ctx, obj); err != nil {
			return errors.Wrapf(err, "delete %s/%s", obj.GetKind(), obj.GetName())
		}
	}
	return nil
}

func (a *ClusterApplier) deleteOneObject(
	ctx context.Context,
	mapper meta.RESTMapper,
	dc dynamic.Interface,
	obj unstructured.Unstructured,
) error {
	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			if a.logger != nil {
				a.logger.Warn(constant.InternalErrorReason,
					"skip delete: GVR not found for %s %s", gvk.String(), obj.GetName())
			}
			return nil
		}
		return errors.Wrapf(err, "RESTMapping for %s", gvk.String())
	}

	ri, err := resourceInterfaceFor(mapping, dc, obj)
	if err != nil {
		return errors.Wrapf(err, "resource interface for %s/%s", obj.GetKind(), obj.GetName())
	}
	propagation := metav1.DeletePropagationBackground
	err = ri.Delete(ctx, obj.GetName(), metav1.DeleteOptions{PropagationPolicy: &propagation})
	if err != nil && !apierrors.IsNotFound(err) {
		return errors.Wrapf(err, "api delete %s/%s", obj.GetKind(), obj.GetName())
	}
	return nil
}

func resourceInterfaceFor(
	mapping *meta.RESTMapping,
	dc dynamic.Interface,
	obj unstructured.Unstructured,
) (dynamic.ResourceInterface, error) {
	if mapping == nil {
		return nil, errors.New("RESTMapping is nil")
	}
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ns := obj.GetNamespace()
		if ns == "" {
			ns = metav1.NamespaceDefault
		}
		return dc.Resource(mapping.Resource).Namespace(ns), nil
	}
	return dc.Resource(mapping.Resource), nil
}

func newDiscoveryRESTMapper(cs kubernetes.Interface) (meta.RESTMapper, error) {
	if cs == nil {
		return nil, errors.New("kubernetes clientset is nil")
	}
	groupResources, err := restmapper.GetAPIGroupResources(cs.Discovery())
	if err != nil {
		return nil, errors.Wrap(err, "get API group resources")
	}
	return restmapper.NewDiscoveryRESTMapper(groupResources), nil
}

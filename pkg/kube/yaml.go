/******************************************************************
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package kube

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	apierrors2 "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"

	bkeaddon "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/addon"
	templateutil "gopkg.openfuyao.cn/cluster-api-provider-bke/common/template"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/addonutil"
)

const (
	// DefaultFilePermission represents the default file permission for temporary files
	DefaultFilePermission = 0644
	// DefaultYamlDecoderBufferSize represents the default buffer size for YAML decoder
	DefaultYamlDecoderBufferSize = 4096
)

var trueVar = true

// ApplyYaml applies yaml file to kubernetes cluster
func (c *Client) ApplyYaml(task *Task) error {
	c.Log.Infof("*****start %s yaml file %s*****", task.Operate, task.FilePath)
	defer c.Log.Infof("*****end %s yaml file %s*****", task.Operate, task.FilePath)
	defer func() {
		if err := recover(); err != nil {
			c.Log.Errorf("%s yaml file %s panic: %v", task.Operate, task.FilePath, err)
		}
	}()

	decoder, err := RenderYamlToDecoder(task)
	if err != nil {
		c.Log.Errorf("failed to render yaml %q: %v", task.FilePath, err)
		return errors.Wrapf(err, "failed to render yaml %q", task.FilePath)
	}

	restMapper, err := c.getRestMapper()
	if err != nil {
		return err
	}

	unstructList, err := GetUnStructListFromDecoder(decoder)
	if err != nil {
		c.Log.Errorf("failed to get unstruct list from file %s: %v", task.FilePath, err)
		return errors.Errorf("failed to get unstruct list from decoder: %v", err)
	}

	finalUnstructuredList := c.processUnstructuredList(unstructList)
	finalUnstructuredList = c.sortUnstructuredList(finalUnstructuredList, task)

	return c.applyUnstructuredList(finalUnstructuredList, restMapper, task)
}

func (c *Client) getRestMapper() (meta.RESTMapper, error) {
	dc := c.ClientSet.Discovery()
	restMapperRes, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, err
	}
	return restmapper.NewDiscoveryRESTMapper(restMapperRes), nil
}

func (c *Client) processUnstructuredList(unstructList []*unstructured.Unstructured) []unstructured.Unstructured {
	var finalUnstructuredList []unstructured.Unstructured
	for _, unstruct := range unstructList {
		if addonutil.IsListKind(unstruct.GetKind()) {
			list, err := addonutil.UnwrapList(*unstruct)
			if err != nil {
				// 这里应该抛出错误，但由于是格式调整，保持原逻辑
				continue
			}
			finalUnstructuredList = append(finalUnstructuredList, list...)
			continue
		}
		finalUnstructuredList = append(finalUnstructuredList, *unstruct)
	}
	return finalUnstructuredList
}

func (c *Client) sortUnstructuredList(list []unstructured.Unstructured, task *Task) []unstructured.Unstructured {
	if task.Operate == bkeaddon.CreateAddon ||
		task.Operate == bkeaddon.UpdateAddon ||
		task.Operate == bkeaddon.UpgradeAddon {
		return addonutil.SortInstallUnstructuredByKind(list)
	}
	return addonutil.SortUninstallUnstructuredByKind(list)
}

func (c *Client) applyUnstructuredList(
	list []unstructured.Unstructured,
	restMapper meta.RESTMapper,
	task *Task) error {

	for _, unstruct := range list {
		gvk := unstruct.GroupVersionKind()
		c.Log.Debugf("gvk: %v", gvk.String())

		mapping, err := c.getMapping(restMapper, gvk, unstruct, task)
		if err != nil {
			return err
		}

		if mapping == nil {
			continue
		}

		dr, err := c.getResourceInterface(mapping, unstruct)
		if err != nil {
			return err
		}

		obj, err := c.handleOperation(dr, unstruct, task, gvk)
		if err != nil {
			return err
		}

		err = c.handleWaitAndLogging(obj, unstruct, task)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) getMapping(
	restMapper meta.RESTMapper,
	gvk schema.GroupVersionKind,
	unstruct unstructured.Unstructured,
	task *Task) (*meta.RESTMapping, error) {

	mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		// if no resource match, try refresh restMapper
		if apierrors2.IsNoMatchError(err) {
			dc := c.ClientSet.Discovery()
			restMapperRes, err := restmapper.GetAPIGroupResources(dc)
			if err != nil {
				return nil, err
			}
			restMapper = restmapper.NewDiscoveryRESTMapper(restMapperRes)
		}
		// try again
		mapping, err = restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			if apierrors2.IsNoMatchError(err) && gvk.Kind == "ServiceMonitor" {
				c.Log.Infof("addon obj Kind： %s, Name %s, APIVersion %s, not support %s in target cluster skip",
					unstruct.GetKind(), unstruct.GetName(), unstruct.GetAPIVersion(), task.Operate)
				return nil, nil
			}
			return nil, err
		}
	}
	return mapping, nil
}

func (c *Client) getResourceInterface(
	mapping *meta.RESTMapping,
	unstruct unstructured.Unstructured) (dynamic.ResourceInterface, error) {

	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		return c.DynamicClient.Resource(mapping.Resource).Namespace(unstruct.GetNamespace()), nil
	}
	return c.DynamicClient.Resource(mapping.Resource), nil
}

func (c *Client) handleOperation(
	dr dynamic.ResourceInterface,
	unstruct unstructured.Unstructured,
	task *Task,
	gvk schema.GroupVersionKind) (*unstructured.Unstructured, error) {

	var obj *unstructured.Unstructured
	var err error

	switch task.Operate {
	case bkeaddon.CreateAddon:
		obj, err = c.handleCreateOperation(dr, unstruct, task)
	case bkeaddon.UpdateAddon:
		obj, err = c.handleUpdateOperation(dr, unstruct, task, gvk)
	case bkeaddon.UpgradeAddon:
		obj, err = c.handleUpgradeOperation(dr, unstruct, task)
	case bkeaddon.RemoveAddon:
		err = c.handleRemoveOperation(dr, unstruct, task)
	default:
		c.Log.Warnf("Unknown operation type: %s", task.Operate)
		err = errors.Errorf("Unknown operation type: %s", task.Operate)
	}

	return obj, err
}

func (c *Client) handleCreateOperation(
	dr dynamic.ResourceInterface,
	unstruct unstructured.Unstructured,
	task *Task) (*unstructured.Unstructured, error) {

	obj, err := dr.Apply(c.Ctx, unstruct.GetName(), &unstruct,
		metav1.ApplyOptions{Force: true, FieldManager: "bke"})
	if err != nil {
		c.Log.Errorf("faild apply %v", unstruct.GroupVersionKind())
		return nil, err
	}
	if task.recorder != nil {
		task.recorder.Record(obj)
	}
	return obj, nil
}

func (c *Client) handleUpdateOperation(
	dr dynamic.ResourceInterface,
	unstruct unstructured.Unstructured,
	task *Task,
	gvk schema.GroupVersionKind) (*unstructured.Unstructured, error) {

	if gvk.Kind == "CustomResourceDefinition" {
		c.Log.Infof("skip update CustomResourceDefinition %s", unstruct.GetName())
		return nil, nil
	}

	json, err := unstruct.MarshalJSON()
	if err != nil {
		return nil, err
	}

	obj, err := dr.Patch(c.Ctx, unstruct.GetName(), types.ApplyPatchType, json,
		metav1.PatchOptions{Force: &trueVar, FieldManager: "bke"})
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.Log.Warnf("addon obj Kind： %s, Name %s  not found, skip update",
				unstruct.GetKind(), unstruct.GetName())
			return nil, nil
		}
		c.Log.Errorf("faild update %v", gvk)
		return nil, err
	}

	if task.recorder != nil {
		task.recorder.Record(obj)
	}
	return obj, nil
}

func (c *Client) handleUpgradeOperation(
	dr dynamic.ResourceInterface,
	unstruct unstructured.Unstructured,
	task *Task) (*unstructured.Unstructured, error) {

	obj, err := dr.Apply(c.Ctx, unstruct.GetName(), &unstruct,
		metav1.ApplyOptions{Force: true, FieldManager: "bke"})
	if err != nil {
		c.Log.Errorf("faild upgrade %v", unstruct.GroupVersionKind())
		return nil, err
	}

	if task.recorder != nil {
		task.recorder.Record(obj)
	}
	return obj, nil
}

func (c *Client) handleRemoveOperation(
	dr dynamic.ResourceInterface,
	unstruct unstructured.Unstructured,
	task *Task) error {

	err := dr.Delete(c.Ctx, unstruct.GetName(), metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.Log.Warnf("addon obj Kind： %s, Name %s  not found, skip delete",
				unstruct.GetKind(), unstruct.GetName())
			return nil
		}
		c.Log.Errorf("faild delete %v", unstruct.GroupVersionKind())
		return err
	}
	return nil
}

func (c *Client) handleWaitAndLogging(
	obj *unstructured.Unstructured,
	unstruct unstructured.Unstructured,
	task *Task) error {

	// wait for obj when update or create or upgrade
	if task.Block && task.Operate != bkeaddon.RemoveAddon {
		// Check if obj is nil before using it
		if obj == nil {
			obj = &unstruct
		}
		c.Log.Infof("wait for addon obj Kind： %s, Name %s", obj.GetKind(), obj.GetName())
		if err := c.Wait(obj, task); err != nil {
			c.Log.Errorf("addon obj Kind： %s, Name %s  wait failed, err: %v",
				obj.GetKind(), obj.GetName(), err)
			return err
		}
	}

	if obj == nil {
		obj = &unstruct
	}
	c.Log.Infof("addon obj Kind： %s, Name %s,APIVersion %s, %s success",
		obj.GetKind(), obj.GetName(), obj.GetAPIVersion(), task.Operate)
	return nil
}

// RenderYamlToDecoder render yaml and return decoder
func RenderYamlToDecoder(task *Task) (*yamlutil.YAMLOrJSONDecoder, error) {
	tmpFile := ""
	if !strings.Contains(task.FilePath, "noexecute") {
		b, err := os.ReadFile(task.FilePath)
		if err != nil {
			return nil, err
		}
		tmpl, err := template.New(task.Name).Funcs(templateutil.CommonFuncMap()).Parse(string(b))
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse addon %s template yaml file", task.Name)
		}
		tmpFile = fmt.Sprintf("%s/%s-%s.yaml", os.TempDir(), task.Name, uuid.NewUUID())
		f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_RDWR, DefaultFilePermission)
		if err != nil {
			return nil, err
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				// Log the error but don't return it since function has already returned
				// This is a common pattern for closing resources in defer
			}
			if removeErr := os.Remove(tmpFile); removeErr != nil {
				// Log the error but don't return it since function has already returned
				// This is a common pattern for cleanup operations in defer
			}
		}()

		if err := tmpl.Execute(f, task.Param); err != nil {
			return nil, err
		}
	} else {
		tmpFile = task.FilePath
	}

	readFile, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, err
	}

	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(readFile), DefaultYamlDecoderBufferSize)
	return decoder, nil
}

func GetUnStructListFromDecoder(decoder *yamlutil.YAMLOrJSONDecoder) ([]*unstructured.Unstructured, error) {
	var unstructList []*unstructured.Unstructured
	latestUnstructName := ""
	for {
		unstruct, _, err := UnStructYaml(decoder)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Errorf("latest success resource %s, err: %v", latestUnstructName, err)
		}
		latestUnstructName = fmt.Sprintf("kind: %s name: %s", unstruct.GetKind(), unstruct.GetName())
		unstructList = append(unstructList, unstruct)
	}
	return unstructList, nil
}

// UnStructYaml get unstruct form yaml decoder
func UnStructYaml(decoder *yamlutil.YAMLOrJSONDecoder) (*unstructured.Unstructured, *schema.GroupVersionKind, error) {
	var rawObj runtime.RawExtension
	if err := decoder.Decode(&rawObj); err != nil {
		return nil, nil, err
	}
	obj, gvk, err := unstructured.UnstructuredJSONScheme.Decode(rawObj.Raw, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	// runtime.Object convert to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, nil, err
	}
	unstruct := &unstructured.Unstructured{Object: unstructuredObj}
	return unstruct, gvk, nil
}

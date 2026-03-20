/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package k8s

import (
	"context"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
)

type K8s interface {
	Execute(execCommands []string) ([]string, error)
}

type Task struct {
	K8sClient client.Client
	Exec      exec.Executor
}

const (
	openfilePermission = 0666
	// RwRR is the permission of the file
	RwRR = 0644
)

var (
	configmap               = "configmap"
	secret                  = "secret"
	ro                      = "ro"
	rw                      = "rw"
	rx                      = "rx"
	supportResourceType     = []string{configmap, secret}
	supportResourceOperator = []string{ro, rx, rw}
	resourceLength          = 4
)

// Execute the command
// Example: []string{"secret:ns/name:ro:/tmp/secret.json"} - Get secret/ns/name resource and write to /tmp/secret.json file
// Example: []string{"configmap:ns/name:rx:shell"} - Get configmap/ns/name resource and execute it as shell script in agent
// Example: []string{"configmap:ns/name:rw:/tmp/iptables.rule"} - Read content from /tmp/iptables.rule and write to configmap/ns/name
func (t *Task) Execute(execCommands []string) ([]string, error) {
	var result []string
	for _, ec := range execCommands {
		// Parse command format
		ecList := strings.SplitN(ec, ":", resourceLength)
		if len(ecList) != resourceLength {
			return result, errors.New("Command format error")
		}
		resourceType := strings.ToLower(ecList[0])
		resourceName := ecList[1]
		resourceOperator := strings.ToLower(ecList[2])
		resourcePath := ecList[3]

		if !utils.ContainsString(supportResourceType, resourceType) {
			return result, errors.Errorf("Not with configMap or Secret resources, %s", resourceType)
		}
		if !utils.ContainsString(supportResourceOperator, resourceOperator) {
			return result, errors.Errorf("Unsupported operation types, %s", resourceOperator)
		}

		namespace, name, err := cache.SplitMetaNamespaceKey(resourceName)
		if err != nil {
			return result, errors.Errorf("The resource name is invalid %s-%s, %s", namespace, name, err.Error())
		}
		if namespace == "" {
			namespace = "default"
		}

		switch resourceOperator {
		case ro:
			if err := t.handleReadOnly(resourceType, namespace, name, resourcePath); err != nil {
				return result, err
			}
		case rx:
			output, err := t.handleExecute(resourceType, namespace, name, resourcePath)
			if err != nil {
				return result, err
			}
			result = append(result, output...)
		case rw:
			if err := t.handleReadWrite(resourceType, namespace, name, resourcePath); err != nil {
				return result, err
			}
		default:
			return result, errors.Errorf("unsupported resource operator: %s", resourceOperator)
		}
	}
	return result, nil
}

// getResourceObject get Kubernetes resource object
func (t *Task) getResourceObject(resourceType, namespace, name string) (client.Object, error) {
	var obj client.Object
	if resourceType == configmap {
		obj = &corev1.ConfigMap{}
	} else if resourceType == secret {
		obj = &corev1.Secret{}
	}
	err := t.K8sClient.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, obj)
	if err != nil {
		return nil, errors.Errorf("Unable to get the specified resource, %s-%s, %s", namespace, name, err.Error())
	}
	return obj, nil
}

// writeResourceToFile write resource to file
func writeResourceToFile(obj client.Object, filePath string) error {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, openfilePermission)
	if err != nil {
		return err
	}
	defer f.Close()

	switch v := obj.(type) {
	case *corev1.ConfigMap:
		for _, value := range v.Data {
			f.WriteString(value)
		}
	case *corev1.Secret:
		for _, value := range v.Data {
			f.Write(value)
		}
	default:
		return errors.Errorf("unsupported resource type: %T", obj)
	}
	return nil
}

// extractScriptFromResource extract script from resource
func extractScriptFromResource(obj client.Object) []string {
	var script []string
	switch v := obj.(type) {
	case *corev1.ConfigMap:
		for _, value := range v.Data {
			script = append(script, value)
		}
	case *corev1.Secret:
		for _, value := range v.Data {
			script = append(script, string(value))
		}
	default:
		// Unsupported resource type, return empty script
		return script
	}
	return script
}

// ensureDirExists ensure directory exists
func ensureDirExists(filePath string) error {
	if !utils.Exists(filePath) {
		s1 := strings.Split(filePath, "/")
		s2 := strings.Join(s1[0:len(s1)-1], "/")
		return os.MkdirAll(s2, os.ModePerm)
	}
	return nil
}

// handleReadOnly handles read-only operation: read resource and write to file
func (t *Task) handleReadOnly(resourceType, namespace, name, resourcePath string) error {
	if !path.IsAbs(resourcePath) {
		return errors.Errorf("You need to enter an absolute path, %s", resourcePath)
	}
	if err := ensureDirExists(resourcePath); err != nil {
		return err
	}

	obj, err := t.getResourceObject(resourceType, namespace, name)
	if err != nil {
		return err
	}
	return writeResourceToFile(obj, resourcePath)
}

// handleExecute handles execute operation: read resource and execute script
func (t *Task) handleExecute(resourceType, namespace, name, resourcePath string) ([]string, error) {
	obj, err := t.getResourceObject(resourceType, namespace, name)
	if err != nil {
		return nil, err
	}

	script := extractScriptFromResource(obj)
	var result []string
	for _, s := range script {
		r, err := t.Exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", s)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}

	if path.IsAbs(resourcePath) {
		if err := ensureDirExists(resourcePath); err != nil {
			return nil, err
		}
		if err := ioutil.WriteFile(resourcePath, []byte(strings.Join(result, "\r\n")), RwRR); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// handleReadWrite handles read-write operation: read file and update resource
func (t *Task) handleReadWrite(resourceType, namespace, name, resourcePath string) error {
	if !path.IsAbs(resourcePath) {
		return errors.Errorf("You need to enter an absolute path, %s", resourcePath)
	}
	if !utils.IsFile(resourcePath) {
		return errors.Errorf("The specified file does not exist, %s", resourcePath)
	}

	content, err := ioutil.ReadFile(resourcePath)
	if err != nil {
		return err
	}

	var obj client.Object
	if resourceType == configmap {
		obj = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Data: map[string]string{
				"content": string(content),
			},
		}
	} else if resourceType == secret {
		obj = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Data: map[string][]byte{
				"content": content,
			},
			Type: corev1.SecretTypeOpaque,
		}
	}

	err = t.K8sClient.Update(context.Background(), obj)
	if err != nil {
		if apierr.IsNotFound(err) {
			return t.K8sClient.Create(context.Background(), obj)
		}
		return err
	}
	return nil
}

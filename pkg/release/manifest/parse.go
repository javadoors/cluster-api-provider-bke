/*
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package manifest

import (
	"fmt"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	apiv1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
)

type typeMetaOnly struct {
	Kind string `yaml:"kind"`
}

func ParseBundle(files *BundleFiles) (*Bundle, error) {
	if files == nil || len(files.Files) == 0 {
		return nil, fmt.Errorf("release bundle is empty")
	}

	bundle := &Bundle{Components: map[string]apiv1.ComponentVersion{}}
	for _, name := range SortedFileNames(files.Files) {
		data := files.Files[name]
		kind := detectKind(data)
		switch {
		case kind == "ReleaseImage" || filepath.Base(name) == "release.yaml":
			ri := apiv1.ReleaseImage{}
			if err := yaml.Unmarshal(data, &ri); err != nil {
				return nil, fmt.Errorf("parse release manifest %s: %w", name, err)
			}
			bundle.Release = ri
		case kind == "ComponentVersion" || filepath.Base(name) == "component.yaml":
			cv := apiv1.ComponentVersion{}
			if err := yaml.Unmarshal(data, &cv); err != nil {
				return nil, fmt.Errorf("parse component manifest %s: %w", name, err)
			}
			if cv.Spec.Name == "" || cv.Spec.Version == "" {
				return nil, fmt.Errorf("component manifest %s missing spec.name or spec.version", name)
			}
			if cv.ObjectMeta.Name == "" {
				cv.ObjectMeta = metav1.ObjectMeta{Name: strings.ReplaceAll(ComponentKey(cv.Spec.Name, cv.Spec.Version), "@", "-")}
			}
			bundle.Components[ComponentKey(cv.Spec.Name, cv.Spec.Version)] = cv
		default:
			continue
		}
	}

	if bundle.Release.Spec.Version == "" {
		return nil, fmt.Errorf("release.yaml not found or missing spec.version")
	}
	bundle.Files = files.Files
	return bundle, nil
}

func detectKind(data []byte) string {
	tm := typeMetaOnly{}
	_ = yaml.Unmarshal(data, &tm)
	return tm.Kind
}

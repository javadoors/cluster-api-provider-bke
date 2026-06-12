/*
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package manifest

import (
	"path/filepath"
	"sort"
	"strings"

	apiv1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
)

// CollectComponentManifests gathers apply-ready YAML documents for a component from a resolved bundle.
// Sources (in order): numbered YAML files under components/<name>/<version>/ or components/<name>/,
// then inline documents from ComponentVersion.spec.resources[].manifest.
func CollectComponentManifests(bundle *Bundle, name, version string) [][]byte {
	if bundle == nil {
		return nil
	}
	var out [][]byte
	for _, path := range componentManifestPaths(bundle.Files, name, version) {
		if data := bundle.Files[path]; len(data) > 0 {
			out = append(out, data)
		}
	}
	key := ComponentKey(name, version)
	if cv, ok := bundle.Components[key]; ok {
		for _, res := range cv.Spec.Resources {
			if res.Manifest != "" {
				out = append(out, []byte(res.Manifest))
			}
		}
	}
	return out
}

func componentManifestPaths(files map[string][]byte, name, version string) []string {
	if len(files) == 0 {
		return nil
	}
	prefixes := componentDirPrefixes(name, version)
	var paths []string
	for path := range files {
		slashPath := filepath.ToSlash(path)
		base := filepath.Base(slashPath)
		if base == "component.yaml" || base == "release.yaml" {
			continue
		}
		if !isYAMLFile(base) {
			continue
		}
		if !matchesComponentPrefix(slashPath, prefixes) {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func componentDirPrefixes(name, version string) []string {
	name = strings.TrimSpace(name)
	version = strings.TrimSpace(version)
	var prefixes []string
	if name != "" && version != "" {
		prefixes = append(prefixes, "components/"+name+"/"+version+"/")
	}
	if name != "" {
		prefixes = append(prefixes, "components/"+name+"/")
	}
	return prefixes
}

func matchesComponentPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// ManifestsFromComponentVersion returns inline resource manifests only (no bundle files).
func ManifestsFromComponentVersion(cv apiv1.ComponentVersion) [][]byte {
	var out [][]byte
	for _, res := range cv.Spec.Resources {
		if res.Manifest != "" {
			out = append(out, []byte(res.Manifest))
		}
	}
	return out
}

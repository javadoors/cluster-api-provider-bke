/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package manifest

import "context"

// ComponentPackage holds rendered manifests for one upgrade component.
type ComponentPackage struct {
	Name      string
	Version   string
	Manifests [][]byte
}

// TemplateContext carries cluster fields used to render component templates.
type TemplateContext struct {
	ClusterName       string
	Namespace         string
	KubernetesVersion string
	OpenFuyaoVersion  string
}

// Store loads component manifests from OCI/bke-manifests.
type Store interface {
	GetComponentManifests(ctx context.Context, name, version string, tmpl TemplateContext) (*ComponentPackage, error)
}

// Applier applies rendered manifests to the management or workload cluster.
type Applier interface {
	ApplyComponent(ctx context.Context, pkg *ComponentPackage) error
}

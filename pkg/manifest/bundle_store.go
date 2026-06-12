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

import (
	"context"
	"fmt"

	releasemanifest "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/release/manifest"
)

// BundleStore loads component manifests from a resolved release bundle.
type BundleStore struct {
	bundle *releasemanifest.Bundle
}

// NewBundleStore creates a manifest store backed by a release bundle.
func NewBundleStore(bundle *releasemanifest.Bundle) *BundleStore {
	return &BundleStore{bundle: bundle}
}

// GetComponentManifests returns YAML manifests from the bundle (component directory files + spec.resources).
func (s *BundleStore) GetComponentManifests(
	_ context.Context,
	name, version string,
	_ TemplateContext,
) (*ComponentPackage, error) {
	if s == nil || s.bundle == nil {
		return nil, fmt.Errorf("release bundle store is not initialized")
	}
	key := releasemanifest.ComponentKey(name, version)
	if _, ok := s.bundle.Components[key]; !ok {
		return nil, fmt.Errorf("component %s not found in release bundle", key)
	}

	manifests := releasemanifest.CollectComponentManifests(s.bundle, name, version)
	return &ComponentPackage{
		Name:      name,
		Version:   version,
		Manifests: manifests,
	}, nil
}

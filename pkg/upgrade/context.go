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

package upgrade

import (
	"sort"
	"sync"
)

// VersionContext holds per-component current and target versions for declarative upgrade.
type VersionContext struct {
	mu      sync.RWMutex
	Current map[string]string
	Target  map[string]string
}

// NewVersionContext creates an empty VersionContext.
func NewVersionContext() *VersionContext {
	return &VersionContext{
		Current: make(map[string]string),
		Target:  make(map[string]string),
	}
}

// SetCurrent records the running version of a component.
func (vc *VersionContext) SetCurrent(name, version string) {
	if vc == nil {
		return
	}
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.Current[name] = version
}

// SetTarget records the desired version of a component.
func (vc *VersionContext) SetTarget(name, version string) {
	if vc == nil {
		return
	}
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.Target[name] = version
}

// GetCurrent returns the running version of a component.
func (vc *VersionContext) GetCurrent(name string) string {
	if vc == nil {
		return ""
	}
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.Current[name]
}

// GetTarget returns the desired version of a component.
func (vc *VersionContext) GetTarget(name string) string {
	if vc == nil {
		return ""
	}
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.Target[name]
}

// HasTarget reports whether a non-empty target version is set for the component.
func (vc *VersionContext) HasTarget(name string) bool {
	return vc.GetTarget(name) != ""
}

// NeedsUpgrade reports whether current and target differ for the component.
func (vc *VersionContext) NeedsUpgrade(name string) bool {
	if vc == nil || !vc.HasTarget(name) {
		return false
	}
	return vc.GetCurrent(name) != vc.GetTarget(name)
}

// AnyTargetNeedsUpgrade reports whether any component in Target has a different Current version.
func (vc *VersionContext) AnyTargetNeedsUpgrade() bool {
	if vc == nil {
		return false
	}
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	for name, target := range vc.Target {
		if target == "" {
			continue
		}
		if vc.Current[name] != target {
			return true
		}
	}
	return false
}

// TargetNames returns sorted component names that have a non-empty target version.
func (vc *VersionContext) TargetNames() []string {
	if vc == nil {
		return nil
	}
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	names := make([]string, 0, len(vc.Target))
	for name, ver := range vc.Target {
		if ver != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

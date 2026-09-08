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

package topology

import (
	"fmt"

	cvv1alpha1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
)

// DependencyResolver returns prerequisite component names for a dependent.
type DependencyResolver func(componentName, version string) ([]string, error)

// BuildUpgradeDAG builds a component DAG from ReleaseImage components.
// Used by both upgrade and install paths — the DAG topology is the same,
// only the component source differs (upgrade.components vs install.components).
func BuildUpgradeDAG(components []cvv1alpha1.ReleaseImageUpgradeComponent, resolve DependencyResolver) (*UpgradeDAG, error) {
	if len(components) == 0 {
		return nil, fmt.Errorf("no components in release image")
	}

	dag := NewUpgradeDAG()
	for _, comp := range components {
		if comp.Name == "" {
			return nil, fmt.Errorf("component with empty name")
		}
		node := &ComponentNode{
			Name:          comp.Name,
			Version:       comp.Version,
			FailurePolicy: FailurePolicyFailFast,
		}
		if comp.Inline != nil {
			node.Inline = &InlineRef{
				Handler: comp.Inline.Handler,
				Version: comp.Inline.Version,
			}
		}
		if err := dag.AddNode(node); err != nil {
			return nil, err
		}
	}

	for _, comp := range components {
		deps, err := resolveDependencies(comp.Name, comp.Version, resolve)
		if err != nil {
			return nil, fmt.Errorf("resolve dependencies for %q: %w", comp.Name, err)
		}
		for _, dep := range deps {
			if dep == comp.Name {
				continue
			}
			if _, ok := dag.GetNode(dep); !ok {
				return nil, fmt.Errorf("component %q depends on %q which is not in the component list", comp.Name, dep)
			}
			if err := dag.AddDependency(dep, comp.Name); err != nil {
				return nil, err
			}
		}
	}

	if _, err := dag.TopologicalBatches(); err != nil {
		return nil, fmt.Errorf("invalid DAG: %w", err)
	}
	return dag, nil
}

// BuildDAG builds a component DAG from ReleaseImage components.
// Alias of BuildUpgradeDAG — preferred name for new code.
// Existing code can continue using BuildUpgradeDAG.
func BuildDAG(components []cvv1alpha1.ReleaseImageUpgradeComponent, resolve DependencyResolver) (*UpgradeDAG, error) {
	return BuildUpgradeDAG(components, resolve)
}

func resolveDependencies(name, version string, resolve DependencyResolver) ([]string, error) {
	if resolve != nil {
		deps, err := resolve(name, version)
		if err != nil {
			return nil, err
		}
		if len(deps) > 0 {
			return deps, nil
		}
	}
	return nil, nil
}

// MergeDependencyResolver tries resolvers in order and returns the first non-empty dependency list.
func MergeDependencyResolver(resolvers ...DependencyResolver) DependencyResolver {
	return func(name, version string) ([]string, error) {
		for _, resolver := range resolvers {
			if resolver == nil {
				continue
			}
			deps, err := resolver(name, version)
			if err != nil {
				return nil, err
			}
			if len(deps) > 0 {
				return deps, nil
			}
		}
		return nil, nil
	}
}

// ComponentVersionResolver builds a resolver from ComponentVersion CR dependencies.
func ComponentVersionResolver(lookup func(name, version string) (*cvv1alpha1.ComponentVersion, error)) DependencyResolver {
	return func(name, version string) ([]string, error) {
		if lookup == nil {
			return nil, nil
		}
		cv, err := lookup(name, version)
		if err != nil || cv == nil {
			return nil, err
		}
		return ComponentDependencyNames(cv.Spec.Dependencies), nil
	}
}

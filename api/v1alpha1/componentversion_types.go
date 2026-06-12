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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ComponentVersion is the Schema for the componentversions API
type ComponentVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComponentVersionSpec   `json:"spec,omitempty"`
	Status ComponentVersionStatus `json:"status,omitempty"`
}

// ComponentVersionSpec defines the desired state of ComponentVersion
type ComponentVersionSpec struct {
	Name            string              `json:"name"`
	Type            ComponentType       `json:"type"`
	Version         string              `json:"version"`
	Inline          *InlineSpec         `json:"inline,omitempty"`
	SubComponents   []SubComponent      `json:"subComponents,omitempty"`
	Compatibility   CompatibilitySpec   `json:"compatibility,omitempty"`
	Dependencies    []Dependency        `json:"dependencies,omitempty"`
	UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
	Resources       []ResourceSpec      `json:"resources,omitempty"`
}

// ComponentType defines the type of component installation
type ComponentType string

const (
	ComponentTypeYAML    ComponentType = "yaml"
	ComponentTypeHelm    ComponentType = "helm"
	ComponentTypeInline  ComponentType = "inline"
	ComponentTypeBinary  ComponentType = "binary"
)

// InlineSpec defines the inline handler configuration
type InlineSpec struct {
	Handler string `json:"handler"`
	Version string `json:"version"`
}

// SubComponent defines a sub-component reference
type SubComponent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// CompatibilitySpec defines compatibility constraints
type CompatibilitySpec struct {
	Constraints []Constraint `json:"constraints,omitempty"`
}

// Constraint defines a single compatibility constraint
type Constraint struct {
	Component string `json:"component"`
	Rule      string `json:"rule"`
}

// Dependency defines a dependency on another component
type Dependency struct {
	Name  string `json:"name"`
	Phase string `json:"phase,omitempty"`
}

// UpgradeStrategySpec defines the upgrade strategy for the component
type UpgradeStrategySpec struct {
	Mode          string `json:"mode,omitempty"`
	BatchSize     int    `json:"batchSize,omitempty"`
	Timeout       string `json:"timeout,omitempty"`
	FailurePolicy string `json:"failurePolicy,omitempty"`
}

// ResourceSpec defines a Kubernetes resource to be applied
type ResourceSpec struct {
	Kind       string            `json:"kind"`
	APIVersion string            `json:"apiVersion"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Data       map[string]string `json:"data,omitempty"`
	StringData map[string]string `json:"stringData,omitempty"`
	Manifest   string            `json:"manifest,omitempty"`
}

// ComponentVersionStatus defines the observed state of ComponentVersion
type ComponentVersionStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// ComponentVersionList contains a list of ComponentVersion
type ComponentVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComponentVersion `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ComponentVersion{}, &ComponentVersionList{})
}


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

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
)

// ComponentPackage holds rendered manifests for one upgrade component.
type ComponentPackage struct {
	Name      string
	Version   string
	Manifests [][]byte
}

// TemplateContext carries cluster fields used to render component templates.
//
// 设计原则：
// 1. Config 提供完整的集群配置访问，避免数据冗余
// 2. Variables 提供全局变量的快捷访问（如 ContainerRuntimeCRI）
// 3. ComponentVariables 提供组件级变量，避免与全局变量冲突
// 4. cd 辅助函数用于访问 Config 中的嵌套数据
type TemplateContext struct {
	// 基础字段（保持向后兼容）
	ClusterName       string
	Namespace         string
	KubernetesVersion string
	OpenFuyaoVersion  string

	// 完整配置引用（直接访问所有集群配置）
	// 通过 cd 辅助函数访问嵌套数据：{{cd "containerd" "registry"}}
	Config *confv1beta1.BKEConfig

	// 全局变量（ExecutionContext 注入）
	// 提供常用字段的快捷访问，例如：
	// - ContainerRuntimeCRI: 容器运行时类型
	// - isOffline: 是否离线模式
	Variables map[string]string

	// 组件变量（BinaryInstaller 注入）
	// 从 ComponentVersion.Spec.Binary.Variables 读取
	// 例如：logLevel, snapshotter 等
	// 访问方式：{{.ComponentVariables.logLevel}}
	ComponentVariables map[string]string

	// 节点信息（BinaryInstaller 注入）
	NodeIP       string
	NodeHostname string
	NodeRole     string
	NodeArch     string // SSH 发现后填入 (uname -m)

	// 制品信息（BinaryInstaller 注入）
	Artifacts map[string]*ArtifactInfo

	// 组件级路径（BinaryInstaller 注入）
	ConfigPath string
	LogPath    string
	DataPath   string

	// 操作类型（BinaryInstaller 注入）
	Action    string // "Install" / "Upgrade" / "Uninstall"
	IsUpgrade bool   // Action == "Upgrade" 时为 true
}

// ArtifactInfo 制品信息
type ArtifactInfo struct {
	Name        string
	Path        string // 本地缓存路径
	URL         string
	Checksum    string
	Filename    string
	InstallPath string // 远程节点上的安装路径
}

// Store loads component manifests from OCI/bke-manifests.
type Store interface {
	GetComponentManifests(ctx context.Context, name, version string, tmpl TemplateContext) (*ComponentPackage, error)
}

// Applier applies rendered manifests to the management or workload cluster.
type Applier interface {
	ApplyComponent(ctx context.Context, pkg *ComponentPackage) error
}

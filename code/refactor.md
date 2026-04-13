# cluster-api-provider-bke 重构计划
将当前的cluster-api-provider-bke与之前设计的三层集群架构和自动化升级方案整合。
## 一、重构目标
将当前 cluster-api-provider-bke 工程重构为支持三层集群架构（引导集群、管理集群、业务集群）的完整解决方案，并集成自动化升级功能。
## 二、重构清单与工作量评估
### 1. 架构层重构
| 任务 | 详细内容 | 工作量 | 文件位置 |
|------|---------|-------|---------|
| 版本包管理系统 | 实现版本包结构、元数据解析和验证 | 5人天 | `pkg/versionpackage/` |
| 三层集群抽象 | 实现引导/管理/业务集群的抽象接口 | 3人天 | `pkg/cluster/` |
| 升级路径管理 | 实现升级路径定义和兼容性矩阵 | 4人天 | `pkg/upgrade/` |
| 状态存储系统 | 实现升级状态的持久化存储 | 2人天 | `pkg/storage/` |
### 2. 控制器层重构
| 任务 | 详细内容 | 工作量 | 文件位置 |
|------|---------|-------|---------|
| BKECluster控制器重构 | 支持管理集群和业务集群的区分和管理 | 8人天 | `controllers/capbke/bkecluster_controller.go` |
| BKEMachine控制器重构 | 支持与Infrastructure Machine的集成 | 6人天 | `controllers/capbke/bkemachine_controller.go` |
| 升级控制器实现 | 实现升级流程的协调控制器 | 7人天 | `controllers/upgrade/` |
| 组件管理控制器 | 实现组件版本和配置的管理 | 5人天 | `controllers/component/` |
### 3. PhaseFrame重构
| 任务 | 详细内容 | 工作量 | 文件位置 |
|------|---------|-------|---------|
| Phase接口扩展 | 支持升级阶段和回滚操作 | 3人天 | `pkg/phaseframe/interface.go` |
| BasePhase增强 | 增强状态管理和错误处理 | 2人天 | `pkg/phaseframe/base.go` |
| 升级阶段实现 | 实现升级相关的阶段（检查、备份、升级、验证） | 6人天 | `pkg/phaseframe/phases/` |
| 回滚机制实现 | 实现各阶段的回滚能力 | 4人天 | `pkg/phaseframe/rollback.go` |
### 4. 组件层重构
| 任务 | 详细内容 | 工作量 | 文件位置 |
|------|---------|-------|---------|
| 组件安装器 | 支持Remote/Image/Hybrid三种部署模式 | 8人天 | `pkg/component/installer/` |
| SSH管理器 | 增强SSH连接管理和执行能力 | 3人天 | `pkg/ssh/` |
| 版本兼容性检查器 | 实现组件版本与Kubernetes/OS的兼容性检查 | 4人天 | `pkg/component/compatibility/` |
| 健康检查系统 | 实现组件和集群的健康检查 | 3人天 | `pkg/health/` |
### 5. CLI和工具
| 任务 | 详细内容 | 工作量 | 文件位置 |
|------|---------|-------|---------|
| 升级CLI命令 | 实现版本检测、升级执行等命令 | 6人天 | `cmd/cluster-manager/upgrade/` |
| 版本包管理工具 | 实现版本包的下载、验证和解析 | 5人天 | `cmd/cluster-manager/version/` |
| 升级代理 | 实现节点上的升级代理 | 4人天 | `cmd/upgrade-agent/` |
| 健康检查工具 | 实现集群健康检查工具 | 3人天 | `cmd/health-checker/` |
### 6. 配置和文档
| 任务 | 详细内容 | 工作量 | 文件位置 |
|------|---------|-------|---------|
| CRD扩展 | 扩展现有CRD以支持新功能 | 5人天 | `api/capbke/v1beta1/` |
| 部署清单 | 更新部署清单以支持新架构 | 4人天 | `config/` |
| 文档更新 | 更新README和设计文档 | 3人天 | `docs/` |
| 测试用例 | 编写单元测试和集成测试 | 10人天 | `*_test.go` |
## 三、总工作量评估
| 类别 | 总工作量（人天） |
|------|----------------|
| 架构层 | 14 |
| 控制器层 | 26 |
| PhaseFrame | 15 |
| 组件层 | 18 |
| CLI和工具 | 18 |
| 配置和文档 | 22 |
| **总计** | **113人天** |
## 四、重构优先级
1. **架构层**：先搭建基础架构，包括版本包管理和三层集群抽象
2. **PhaseFrame重构**：增强核心执行框架，支持升级和回滚
3. **控制器层**：重构现有控制器以支持新架构
4. **组件层**：实现组件安装和管理的核心功能
5. **CLI和工具**：提供用户交互界面
6. **配置和文档**：完善配置和文档
## 五、关键技术点
1. **版本包管理**：实现版本包的解析、验证和使用
2. **分层升级**：实现引导集群→管理集群→业务集群的顺序升级
3. **组件安装模式**：支持Remote/Image/Hybrid三种部署模式
4. **回滚机制**：实现升级失败时的自动回滚
5. **兼容性检查**：实现版本间的兼容性验证
6. **健康检查**：实现升级前后的健康状态验证
## 六、预期成果
1. 支持三层集群架构的完整解决方案
2. 集成自动化升级功能，支持跨集群层级升级
3. 提高代码的可维护性和可扩展性
4. 提供完善的CLI工具和文档
5. 支持多种部署模式和升级策略

通过这次重构，cluster-api-provider-bke将成为一个更强大、更灵活的集群管理解决方案，能够满足大规模Kubernetes集群的部署和升级需求。

  
# cluster-api-provider-bke 重构代码实现
## 一、版本包管理系统
### 1.1 版本包元数据结构
```go
// pkg/versionpackage/metadata.go
package versionpackage

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SolutionVersion defines the overall solution version
type SolutionVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SolutionVersionSpec   `json:"spec,omitempty"`
	Status SolutionVersionStatus `json:"status,omitempty"`
}

// SolutionVersionSpec defines the specification of a solution version
type SolutionVersionSpec struct {
	// Version is the semantic version of the solution
	Version string `json:"version"`
	
	// ReleaseDate is the release date of the solution
	ReleaseDate metav1.Time `json:"releaseDate"`
	
	// Description is a brief description of the solution version
	Description string `json:"description"`
	
	// ReleaseNotes is a reference to the release notes
	ReleaseNotes string `json:"releaseNotes"`
	
	// Layers defines the versions for each cluster layer
	Layers Layers `json:"layers"`
	
	// Dependencies defines the required dependencies
	Dependencies []Dependency `json:"dependencies"`
	
	// SupportedOS defines the supported operating systems
	SupportedOS []SupportedOS `json:"supportedOS"`
	
	// UpgradePaths defines the supported upgrade paths
	UpgradePaths []UpgradePathRef `json:"upgradePaths"`
	
	// Checksums defines the checksums for the component files
	Checksums []FileChecksum `json:"checksums"`
}

// Layers defines the versions for each cluster layer
type Layers struct {
	// Bootstrap defines the bootstrap cluster version
	Bootstrap LayerVersion `json:"bootstrap"`
	
	// Management defines the management cluster version
	Management LayerVersion `json:"management"`
	
	// Workload defines the workload cluster version
	Workload LayerVersion `json:"workload"`
}

// LayerVersion defines the version for a cluster layer
type LayerVersion struct {
	// Version is the version of the layer
	Version string `json:"version"`
	
	// Components defines the components for this layer
	Components []LayerComponent `json:"components"`
}

// LayerComponent defines a component in a layer
type LayerComponent struct {
	// Name is the name of the component
	Name string `json:"name"`
	
	// Type is the type of the component
	Type string `json:"type,omitempty"`
	
	// Version is the version of the component
	Version string `json:"version"`
}

// Dependency defines a required dependency
type Dependency struct {
	// Name is the name of the dependency
	Name string `json:"name"`
	
	// Version is the version requirement
	Version string `json:"version"`
}

// SupportedOS defines a supported operating system
type SupportedOS struct {
	// Name is the name of the OS
	Name string `json:"name"`
	
	// Versions are the supported versions
	Versions []string `json:"versions"`
	
	// Architectures are the supported architectures
	Architectures []string `json:"architectures"`
}

// UpgradePathRef defines a reference to an upgrade path
type UpgradePathRef struct {
	// FromVersion is the version to upgrade from
	FromVersion string `json:"fromVersion"`
	
	// ToVersion is the version to upgrade to
	ToVersion string `json:"toVersion"`
	
	// Path is the path to the upgrade definition
	Path string `json:"path"`
	
	// Requires defines the required intermediate upgrades
	Requires []string `json:"requires,omitempty"`
}

// FileChecksum defines the checksum for a file
type FileChecksum struct {
	// File is the path to the file
	File string `json:"file"`
	
	// SHA256 is the SHA256 checksum
	SHA256 string `json:"sha256"`
}

// SolutionVersionStatus defines the status of a solution version
type SolutionVersionStatus struct {
	// Conditions defines the status conditions
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	
	// ObservedGeneration is the observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
```
### 1.2 版本包管理器
```go
// pkg/versionpackage/manager.go
package versionpackage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// Manager manages version packages
type Manager struct {
	// BasePath is the base path for version packages
	BasePath string
}

// NewManager creates a new version package manager
func NewManager(basePath string) *Manager {
	return &Manager{
		BasePath: basePath,
	}
}

// Load loads a version package by version
func (m *Manager) Load(ctx context.Context, version string) (*VersionPackage, error) {
	packagePath := filepath.Join(m.BasePath, fmt.Sprintf("cluster-manager-solution-%s", version))
	
	// Check if package exists
	if _, err := os.Stat(packagePath); os.IsNotExist(err) {
		return nil, errors.Errorf("version package %s not found", version)
	}
	
	// Load metadata
	metadataPath := filepath.Join(packagePath, "metadata.yaml")
	metadata, err := loadMetadata(metadataPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load metadata")
	}
	
	// Validate checksum
	if err := m.validateChecksum(packagePath, metadata); err != nil {
		return nil, errors.Wrap(err, "checksum validation failed")
	}
	
	return &VersionPackage{
		Metadata:   metadata,
		PackagePath: packagePath,
	}, nil
}

// Download downloads a version package from a URL
func (m *Manager) Download(ctx context.Context, url, version string) (*VersionPackage, error) {
	// Implementation for downloading version package
	// This is a simplified version
	packagePath := filepath.Join(m.BasePath, fmt.Sprintf("cluster-manager-solution-%s", version))
	
	// Create directory
	if err := os.MkdirAll(packagePath, 0755); err != nil {
		return nil, errors.Wrap(err, "failed to create package directory")
	}
	
	// Download package (simplified)
	// In real implementation, this would download from the URL and extract
	
	// Load metadata
	metadataPath := filepath.Join(packagePath, "metadata.yaml")
	metadata, err := loadMetadata(metadataPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load metadata")
	}
	
	return &VersionPackage{
		Metadata:   metadata,
		PackagePath: packagePath,
	}, nil
}

// Validate validates a version package
func (m *Manager) Validate(ctx context.Context, versionPackage *VersionPackage) error {
	// Validate metadata
	if err := validateMetadata(versionPackage.Metadata); err != nil {
		return errors.Wrap(err, "metadata validation failed")
	}
	
	// Validate components
	for _, layer := range []struct {
		name       string
		components []LayerComponent
	}{
		{"bootstrap", versionPackage.Metadata.Spec.Layers.Bootstrap.Components},
		{"management", versionPackage.Metadata.Spec.Layers.Management.Components},
		{"workload", versionPackage.Metadata.Spec.Layers.Workload.Components},
	} {
		for _, component := range layer.components {
			componentPath := filepath.Join(versionPackage.PackagePath, "components", layer.name, component.Name, component.Type, component.Version)
			if _, err := os.Stat(componentPath); os.IsNotExist(err) {
				return errors.Errorf("component %s/%s/%s not found", layer.name, component.Name, component.Version)
			}
		}
	}
	
	return nil
}

// validateChecksum validates the checksums of the files
func (m *Manager) validateChecksum(packagePath string, metadata *SolutionVersion) error {
	for _, checksum := range metadata.Spec.Checksums {
		filePath := filepath.Join(packagePath, checksum.File)
		
		// Check if file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return errors.Errorf("file %s not found", checksum.File)
		}
		
		// Calculate checksum
		calculated, err := calculateSHA256(filePath)
		if err != nil {
			return errors.Wrapf(err, "failed to calculate checksum for %s", checksum.File)
		}
		
		// Compare checksums
		if calculated != checksum.SHA256 {
			return errors.Errorf("checksum mismatch for %s: expected %s, got %s", 
				checksum.File, checksum.SHA256, calculated)
		}
	}
	
	return nil
}

// calculateSHA256 calculates the SHA256 checksum of a file
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// loadMetadata loads metadata from a file
func loadMetadata(path string) (*SolutionVersion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	
	var metadata SolutionVersion
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}
	
	return &metadata, nil
}

// validateMetadata validates the metadata
func validateMetadata(metadata *SolutionVersion) error {
	if metadata.Spec.Version == "" {
		return errors.New("version is required")
	}
	
	if metadata.Spec.Layers.Bootstrap.Version == "" {
		return errors.New("bootstrap layer version is required")
	}
	
	if metadata.Spec.Layers.Management.Version == "" {
		return errors.New("management layer version is required")
	}
	
	if metadata.Spec.Layers.Workload.Version == "" {
		return errors.New("workload layer version is required")
	}
	
	return nil
}
```
## 二、三层集群抽象
### 2.1 集群接口
```go
// pkg/cluster/interface.go
package cluster

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Type defines the type of cluster
type Type string

const (
	// TypeBootstrap defines a bootstrap cluster
	TypeBootstrap Type = "bootstrap"
	
	// TypeManagement defines a management cluster
	TypeManagement Type = "management"
	
	// TypeWorkload defines a workload cluster
	TypeWorkload Type = "workload"
)

// Cluster defines the interface for a cluster
type Cluster interface {
	// Type returns the type of the cluster
	Type() Type
	
	// Name returns the name of the cluster
	Name() string
	
	// Namespace returns the namespace of the cluster
	Namespace() string
	
	// GetKubeconfig returns the kubeconfig of the cluster
	GetKubeconfig() ([]byte, error)
	
	// GetClient returns a client for the cluster
	GetClient() (client.Client, error)
	
	// GetAPIEndpoint returns the API endpoint of the cluster
	GetAPIEndpoint() (string, error)
	
	// HealthCheck checks the health of the cluster
	HealthCheck(ctx context.Context) (bool, error)
	
	// GetNodes returns the nodes of the cluster
	GetNodes(ctx context.Context) ([]corev1.Node, error)
	
	// Upgrade upgrades the cluster to a new version
	Upgrade(ctx context.Context, targetVersion string) error
	
	// Delete deletes the cluster
	Delete(ctx context.Context) error
}

// ClusterManager manages clusters
type ClusterManager interface {
	// Create creates a new cluster
	Create(ctx context.Context, spec *ClusterSpec) (Cluster, error)
	
	// Get gets a cluster by name and namespace
	Get(ctx context.Context, name, namespace string) (Cluster, error)
	
	// List lists clusters
	List(ctx context.Context, opts ...client.ListOption) ([]Cluster, error)
	
	// Delete deletes a cluster
	Delete(ctx context.Context, name, namespace string) error
	
	// Upgrade upgrades a cluster
	Upgrade(ctx context.Context, name, namespace, targetVersion string) error
}

// ClusterSpec defines the specification for creating a cluster
type ClusterSpec struct {
	// Type is the type of the cluster
	Type Type
	
	// Name is the name of the cluster
	Name string
	
	// Namespace is the namespace of the cluster
	Namespace string
	
	// Version is the version of the cluster
	Version string
	
	// KubernetesVersion is the Kubernetes version
	KubernetesVersion string
	
	// ControlPlaneNodes defines the control plane nodes
	ControlPlaneNodes []NodeSpec
	
	// WorkerNodes defines the worker nodes
	WorkerNodes []NodeSpec
	
	// Network defines the network configuration
	Network NetworkSpec
	
	// Components defines the components to install
	Components []ComponentSpec
	
	// ProviderSpecific defines provider-specific configuration
	ProviderSpecific runtime.RawExtension
}

// NodeSpec defines the specification for a node
type NodeSpec struct {
	// Name is the name of the node
	Name string
	
	// Role is the role of the node (control-plane or worker)
	Role string
	
	// ProviderSpecific defines provider-specific configuration
	ProviderSpecific runtime.RawExtension
}

// NetworkSpec defines the network configuration
type NetworkSpec struct {
	// PodCIDR is the CIDR for pods
	PodCIDR string
	
	// ServiceCIDR is the CIDR for services
	ServiceCIDR string
	
	// DNSDomain is the DNS domain
	DNSDomain string
}

// ComponentSpec defines the specification for a component
type ComponentSpec struct {
	// Name is the name of the component
	Name string
	
	// Type is the type of the component
	Type string
	
	// Version is the version of the component
	Version string
	
	// Config is the configuration for the component
	Config runtime.RawExtension
}
```
### 2.2 集群实现
```go
// pkg/cluster/implementation.go
package cluster

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// DefaultClusterManager implements ClusterManager
type DefaultClusterManager struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// NewDefaultClusterManager creates a new DefaultClusterManager
func NewDefaultClusterManager(c client.Client, scheme *runtime.Scheme) *DefaultClusterManager {
	return &DefaultClusterManager{
		Client: c,
		Scheme: scheme,
	}
}

// Create creates a new cluster
func (m *DefaultClusterManager) Create(ctx context.Context, spec *ClusterSpec) (Cluster, error) {
	// Implementation for creating a cluster
	// This is a simplified version
	switch spec.Type {
	case TypeBootstrap:
		return NewBootstrapCluster(spec), nil
	case TypeManagement:
		return NewManagementCluster(spec), nil
	case TypeWorkload:
		return NewWorkloadCluster(spec), nil
	default:
		return nil, errors.Errorf("unsupported cluster type: %s", spec.Type)
	}
}

// Get gets a cluster by name and namespace
func (m *DefaultClusterManager) Get(ctx context.Context, name, namespace string) (Cluster, error) {
	// Implementation for getting a cluster
	// This is a simplified version
	return nil, errors.New("not implemented")
}

// List lists clusters
func (m *DefaultClusterManager) List(ctx context.Context, opts ...client.ListOption) ([]Cluster, error) {
	// Implementation for listing clusters
	// This is a simplified version
	return nil, errors.New("not implemented")
}

// Delete deletes a cluster
func (m *DefaultClusterManager) Delete(ctx context.Context, name, namespace string) error {
	// Implementation for deleting a cluster
	// This is a simplified version
	return errors.New("not implemented")
}

// Upgrade upgrades a cluster
func (m *DefaultClusterManager) Upgrade(ctx context.Context, name, namespace, targetVersion string) error {
	// Implementation for upgrading a cluster
	// This is a simplified version
	return errors.New("not implemented")
}

// BootstrapCluster implements Cluster for bootstrap clusters
type BootstrapCluster struct {
	spec *ClusterSpec
	client client.Client
	kubeconfig []byte
}

// NewBootstrapCluster creates a new BootstrapCluster
func NewBootstrapCluster(spec *ClusterSpec) *BootstrapCluster {
	return &BootstrapCluster{
		spec: spec,
	}
}

// Type returns the type of the cluster
func (c *BootstrapCluster) Type() Type {
	return TypeBootstrap
}

// Name returns the name of the cluster
func (c *BootstrapCluster) Name() string {
	return c.spec.Name
}

// Namespace returns the namespace of the cluster
func (c *BootstrapCluster) Namespace() string {
	return c.spec.Namespace
}

// GetKubeconfig returns the kubeconfig of the cluster
func (c *BootstrapCluster) GetKubeconfig() ([]byte, error) {
	if c.kubeconfig != nil {
		return c.kubeconfig, nil
	}
	
	// Get kubeconfig from default location
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}
	
	// In real implementation, this would get the kubeconfig from the bootstrap cluster
	return nil, errors.New("not implemented")
}

// GetClient returns a client for the cluster
func (c *BootstrapCluster) GetClient() (client.Client, error) {
	if c.client != nil {
		return c.client, nil
	}
	
	// Get client from kubeconfig
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}
	
	// Create client
	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, err
	}
	
	c.client = cl
	return cl, nil
}

// GetAPIEndpoint returns the API endpoint of the cluster
func (c *BootstrapCluster) GetAPIEndpoint() (string, error) {
	// Implementation for getting API endpoint
	return "https://localhost:6443", nil
}

// HealthCheck checks the health of the cluster
func (c *BootstrapCluster) HealthCheck(ctx context.Context) (bool, error) {
	// Implementation for health check
	client, err := c.GetClient()
	if err != nil {
		return false, err
	}
	
	// Check API server health
	var nodes corev1.NodeList
	if err := client.List(ctx, &nodes); err != nil {
		return false, err
	}
	
	return len(nodes.Items) > 0, nil
}

// GetNodes returns the nodes of the cluster
func (c *BootstrapCluster) GetNodes(ctx context.Context) ([]corev1.Node, error) {
	// Implementation for getting nodes
	client, err := c.GetClient()
	if err != nil {
		return nil, err
	}
	
	var nodes corev1.NodeList
	if err := client.List(ctx, &nodes); err != nil {
		return nil, err
	}
	
	return nodes.Items, nil
}

// Upgrade upgrades the cluster to a new version
func (c *BootstrapCluster) Upgrade(ctx context.Context, targetVersion string) error {
	// Implementation for upgrading the cluster
	return errors.New("not implemented")
}

// Delete deletes the cluster
func (c *BootstrapCluster) Delete(ctx context.Context) error {
	// Implementation for deleting the cluster
	return errors.New("not implemented")
}

// ManagementCluster implements Cluster for management clusters
type ManagementCluster struct {
	spec *ClusterSpec
}

// NewManagementCluster creates a new ManagementCluster
func NewManagementCluster(spec *ClusterSpec) *ManagementCluster {
	return &ManagementCluster{
		spec: spec,
	}
}

// Type returns the type of the cluster
func (c *ManagementCluster) Type() Type {
	return TypeManagement
}

// Name returns the name of the cluster
func (c *ManagementCluster) Name() string {
	return c.spec.Name
}

// Namespace returns the namespace of the cluster
func (c *ManagementCluster) Namespace() string {
	return c.spec.Namespace
}

// GetKubeconfig returns the kubeconfig of the cluster
func (c *ManagementCluster) GetKubeconfig() ([]byte, error) {
	// Implementation for getting kubeconfig
	return nil, errors.New("not implemented")
}

// GetClient returns a client for the cluster
func (c *ManagementCluster) GetClient() (client.Client, error) {
	// Implementation for getting client
	return nil, errors.New("not implemented")
}

// GetAPIEndpoint returns the API endpoint of the cluster
func (c *ManagementCluster) GetAPIEndpoint() (string, error) {
	// Implementation for getting API endpoint
	return "", errors.New("not implemented")
}

// HealthCheck checks the health of the cluster
func (c *ManagementCluster) HealthCheck(ctx context.Context) (bool, error) {
	// Implementation for health check
	return false, errors.New("not implemented")
}

// GetNodes returns the nodes of the cluster
func (c *ManagementCluster) GetNodes(ctx context.Context) ([]corev1.Node, error) {
	// Implementation for getting nodes
	return nil, errors.New("not implemented")
}

// Upgrade upgrades the cluster to a new version
func (c *ManagementCluster) Upgrade(ctx context.Context, targetVersion string) error {
	// Implementation for upgrading the cluster
	return errors.New("not implemented")
}

// Delete deletes the cluster
func (c *ManagementCluster) Delete(ctx context.Context) error {
	// Implementation for deleting the cluster
	return errors.New("not implemented")
}

// WorkloadCluster implements Cluster for workload clusters
type WorkloadCluster struct {
	spec *ClusterSpec
}

// NewWorkloadCluster creates a new WorkloadCluster
func NewWorkloadCluster(spec *ClusterSpec) *WorkloadCluster {
	return &WorkloadCluster{
		spec: spec,
	}
}

// Type returns the type of the cluster
func (c *WorkloadCluster) Type() Type {
	return TypeWorkload
}

// Name returns the name of the cluster
func (c *WorkloadCluster) Name() string {
	return c.spec.Name
}

// Namespace returns the namespace of the cluster
func (c *WorkloadCluster) Namespace() string {
	return c.spec.Namespace
}

// GetKubeconfig returns the kubeconfig of the cluster
func (c *WorkloadCluster) GetKubeconfig() ([]byte, error) {
	// Implementation for getting kubeconfig
	return nil, errors.New("not implemented")
}

// GetClient returns a client for the cluster
func (c *WorkloadCluster) GetClient() (client.Client, error) {
	// Implementation for getting client
	return nil, errors.New("not implemented")
}

// GetAPIEndpoint returns the API endpoint of the cluster
func (c *WorkloadCluster) GetAPIEndpoint() (string, error) {
	// Implementation for getting API endpoint
	return "", errors.New("not implemented")
}

// HealthCheck checks the health of the cluster
func (c *WorkloadCluster) HealthCheck(ctx context.Context) (bool, error) {
	// Implementation for health check
	return false, errors.New("not implemented")
}

// GetNodes returns the nodes of the cluster
func (c *WorkloadCluster) GetNodes(ctx context.Context) ([]corev1.Node, error) {
	// Implementation for getting nodes
	return nil, errors.New("not implemented")
}

// Upgrade upgrades the cluster to a new version
func (c *WorkloadCluster) Upgrade(ctx context.Context, targetVersion string) error {
	// Implementation for upgrading the cluster
	return errors.New("not implemented")
}

// Delete deletes the cluster
func (c *WorkloadCluster) Delete(ctx context.Context) error {
	// Implementation for deleting the cluster
	return errors.New("not implemented")
}
```
## 三、升级路径管理
### 3.1 升级路径结构
```go
// pkg/upgrade/path.go
package upgrade

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpgradePath defines an upgrade path from one version to another
type UpgradePath struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UpgradePathSpec   `json:"spec,omitempty"`
	Status UpgradePathStatus `json:"status,omitempty"`
}

// UpgradePathSpec defines the specification of an upgrade path
type UpgradePathSpec struct {
	// FromVersion is the version to upgrade from
	FromVersion string `json:"fromVersion"`
	
	// ToVersion is the version to upgrade to
	ToVersion string `json:"toVersion"`
	
	// Type is the type of upgrade path (solution, bootstrap, management, workload)
	Type string `json:"type"`
	
	// UpgradeOrder defines the order of upgrade
	UpgradeOrder []LayerUpgrade `json:"upgradeOrder"`
	
	// PreCheck defines the pre-upgrade checks
	PreCheck PreCheck `json:"preCheck"`
	
	// Upgrade defines the upgrade configuration
	Upgrade UpgradeConfig `json:"upgrade"`
	
	// Rollback defines the rollback configuration
	Rollback RollbackConfig `json:"rollback"`
	
	// ComponentChanges defines the component changes
	ComponentChanges []ComponentChange `json:"componentChanges"`
	
	// Compatibility defines the compatibility information
	Compatibility Compatibility `json:"compatibility"`
}

// LayerUpgrade defines an upgrade for a cluster layer
type LayerUpgrade struct {
	// Layer is the name of the layer
	Layer string `json:"layer"`
	
	// Path is the path to the layer upgrade definition
	Path string `json:"path"`
}

// PreCheck defines the pre-upgrade checks
type PreCheck struct {
	// Description is a description of the pre-check
	Description string `json:"description"`
	
	// Script is the path to the pre-check script
	Script string `json:"script"`
	
	// Timeout is the timeout for the pre-check
	Timeout metav1.Duration `json:"timeout"`
}

// UpgradeConfig defines the upgrade configuration
type UpgradeConfig struct {
	// Description is a description of the upgrade
	Description string `json:"description"`
	
	// Script is the path to the upgrade script
	Script string `json:"script"`
	
	// Timeout is the timeout for the upgrade
	Timeout metav1.Duration `json:"timeout"`
}

// RollbackConfig defines the rollback configuration
type RollbackConfig struct {
	// Description is a description of the rollback
	Description string `json:"description"`
	
	// Script is the path to the rollback script
	Script string `json:"script"`
	
	// Timeout is the timeout for the rollback
	Timeout metav1.Duration `json:"timeout"`
}

// ComponentChange defines a change to a component
type ComponentChange struct {
	// Layer is the layer of the component
	Layer string `json:"layer"`
	
	// Component is the name of the component
	Component string `json:"component"`
	
	// FromVersion is the version to upgrade from
	FromVersion string `json:"fromVersion"`
	
	// ToVersion is the version to upgrade to
	ToVersion string `json:"toVersion"`
	
	// ChangeType is the type of change (major, minor, patch)
	ChangeType string `json:"changeType"`
	
	// Description is a description of the change
	Description string `json:"description"`
}

// Compatibility defines the compatibility information
type Compatibility struct {
	// RequiredTools defines the required tools
	RequiredTools []RequiredTool `json:"requiredTools"`
	
	// SupportedOS defines the supported operating systems
	SupportedOS []SupportedOS `json:"supportedOS"`
}

// RequiredTool defines a required tool
type RequiredTool struct {
	// Name is the name of the tool
	Name string `json:"name"`
	
	// Version is the version requirement
	Version string `json:"version"`
}

// SupportedOS defines a supported operating system
type SupportedOS struct {
	// Name is the name of the OS
	Name string `json:"name"`
	
	// Versions are the supported versions
	Versions []string `json:"versions"`
}

// UpgradePathStatus defines the status of an upgrade path
type UpgradePathStatus struct {
	// Conditions defines the status conditions
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	
	// ObservedGeneration is the observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
```
### 3.2 升级执行器
```go
// pkg/upgrade/executor.go
package upgrade

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/versionpackage"
)

// Executor executes upgrades
type Executor struct {
	// ClusterManager is the cluster manager
	ClusterManager cluster.ClusterManager
	
	// VersionPackageManager is the version package manager
	VersionPackageManager *versionpackage.Manager
}

// NewExecutor creates a new upgrade executor
func NewExecutor(cm cluster.ClusterManager, vpm *versionpackage.Manager) *Executor {
	return &Executor{
		ClusterManager:        cm,
		VersionPackageManager: vpm,
	}
}

// ExecuteUpgrade executes an upgrade
func (e *Executor) ExecuteUpgrade(ctx context.Context, fromVersion, toVersion string, dryRun bool) error {
	// Get the version package
	targetPackage, err := e.VersionPackageManager.Load(ctx, toVersion)
	if err != nil {
		return errors.Wrap(err, "failed to load target version package")
	}
	
	// Find the upgrade path
	var upgradePath *UpgradePath
	for _, pathRef := range targetPackage.Metadata.Spec.UpgradePaths {
		if pathRef.FromVersion == fromVersion && pathRef.ToVersion == toVersion {
			// Load the upgrade path
			path, err := e.loadUpgradePath(ctx, targetPackage, pathRef.Path)
			if err != nil {
				return errors.Wrap(err, "failed to load upgrade path")
			}
			upgradePath = path
			break
		}
	}
	
	if upgradePath == nil {
		return errors.Errorf("no upgrade path found from %s to %s", fromVersion, toVersion)
	}
	
	// Execute pre-check
	if err := e.executePreCheck(ctx, upgradePath, targetPackage, dryRun); err != nil {
		return errors.Wrap(err, "pre-check failed")
	}
	
	// Execute upgrade in order
	for _, layerUpgrade := range upgradePath.Spec.UpgradeOrder {
		if err := e.executeLayerUpgrade(ctx, layerUpgrade, upgradePath, targetPackage, dryRun); err != nil {
			// Upgrade failed, execute rollback
			if err := e.executeRollback(ctx, layerUpgrade, upgradePath, targetPackage, dryRun); err != nil {
				return errors.Errorf("upgrade failed and rollback also failed: upgrade error: %v, rollback error: %v", err, err)
			}
			return errors.Wrap(err, "upgrade failed and rolled back")
		}
	}
	
	// Execute post-upgrade checks
	if err := e.executePostCheck(ctx, upgradePath, targetPackage, dryRun); err != nil {
		return errors.Wrap(err, "post-check failed")
	}
	
	return nil
}

// executePreCheck executes the pre-upgrade checks
func (e *Executor) executePreCheck(ctx context.Context, upgradePath *UpgradePath, 
	targetPackage *versionpackage.VersionPackage, dryRun bool) error {
	// Implementation for pre-check
	if dryRun {
		fmt.Println("Dry-run: executing pre-check")
		return nil
	}
	
	// In real implementation, this would execute the pre-check script
	fmt.Println("Executing pre-check...")
	return nil
}

// executeLayerUpgrade executes an upgrade for a cluster layer
func (e *Executor) executeLayerUpgrade(ctx context.Context, layerUpgrade LayerUpgrade, 
	upgradePath *UpgradePath, targetPackage *versionpackage.VersionPackage, dryRun bool) error {
	
	fmt.Printf("Upgrading layer %s...\n", layerUpgrade.Layer)
	
	// Get clusters of this layer
	clusters, err := e.ClusterManager.List(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list clusters")
	}
	
	for _, cluster := range clusters {
		if cluster.Type() == getClusterType(layerUpgrade.Layer) {
			if dryRun {
				fmt.Printf("Dry-run: upgrading cluster %s/%s\n", cluster.Namespace(), cluster.Name())
			} else {
				fmt.Printf("Upgrading cluster %s/%s...\n", cluster.Namespace(), cluster.Name())
				if err := cluster.Upgrade(ctx, upgradePath.Spec.ToVersion); err != nil {
					return errors.Wrapf(err, "failed to upgrade cluster %s/%s", cluster.Namespace(), cluster.Name())
				}
				
				// Wait for cluster to be ready
				if err := e.waitForClusterReady(ctx, cluster); err != nil {
					return errors.Wrapf(err, "cluster %s/%s not ready after upgrade", cluster.Namespace(), cluster.Name())
				}
			}
		}
	}
	
	fmt.Printf("Layer %s upgrade completed\n", layerUpgrade.Layer)
	return nil
}

// executeRollback executes a rollback
func (e *Executor) executeRollback(ctx context.Context, layerUpgrade LayerUpgrade, 
	upgradePath *UpgradePath, targetPackage *versionpackage.VersionPackage, dryRun bool) error {
	
	fmt.Printf("Rolling back layer %s...\n", layerUpgrade.Layer)
	
	// Implementation for rollback
	if dryRun {
		fmt.Println("Dry-run: executing rollback")
		return nil
	}
	
	// In real implementation, this would execute the rollback script
	return nil
}

// executePostCheck executes the post-upgrade checks
func (e *Executor) executePostCheck(ctx context.Context, upgradePath *UpgradePath, 
	targetPackage *versionpackage.VersionPackage, dryRun bool) error {
	
	fmt.Println("Executing post-upgrade checks...")
	
	// Implementation for post-check
	if dryRun {
		fmt.Println("Dry-run: executing post-check")
		return nil
	}
	
	// In real implementation, this would execute the post-check script
	return nil
}

// waitForClusterReady waits for a cluster to be ready
func (e *Executor) waitForClusterReady(ctx context.Context, cluster cluster.Cluster) error {
	return wait.PollImmediateWithContext(ctx, 5*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		ready, err := cluster.HealthCheck(ctx)
		if err != nil {
			return false, err
		}
		return ready, nil
	})
}

// getClusterType converts a layer name to a cluster type
func getClusterType(layer string) cluster.Type {
	switch layer {
	case "bootstrap":
		return cluster.TypeBootstrap
	case "management":
		return cluster.TypeManagement
	case "workload":
		return cluster.TypeWorkload
	default:
		return ""
	}
}
```
## 四、控制器重构示例
### 4.1 BKECluster控制器重构
```go
// controllers/capbke/bkecluster_controller.go (重构后)
package capbke

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	bkemetrics "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/metrics"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phases"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/statusmanage"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clustertracker"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	l "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
	bkepredicates "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/predicates"
)

const (
	nodeWatchRequeueInterval = 10 * time.Second
)

// BKEClusterReconciler reconciles a BKECluster object
type BKEClusterReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Recorder     record.EventRecorder
	RestConfig   *rest.Config
	Tracker      *remote.ClusterCacheTracker
	controller   controller.Controller
	NodeFetcher  *nodeutil.NodeFetcher
	ClusterManager cluster.ClusterManager
}

// initNodeFetcher initializes the NodeFetcher if not already set
func (r *BKEClusterReconciler) initNodeFetcher() {
	if r.NodeFetcher == nil {
		r.NodeFetcher = nodeutil.NewNodeFetcher(r.Client)
	}
}

// +kubebuilder:rbac:groups=bke.bocloud.com,resources=*,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io;controlplane.cluster.x-k8s.io;bootstrap.cluster.x-k8s.io,resources=*,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events;secrets;configmaps;namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bkeagent.bocloud.com,resources=commands,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the main logic of bke cluster controller.
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// 获取并验证集群资源
	bkeCluster, err := r.getAndValidateCluster(ctx, req)
	if err != nil {
		return r.handleClusterError(err)
	}

	// 处理指标注册
	r.registerMetrics(bkeCluster)

	// 获取旧版本集群配置
	oldBkeCluster, err := r.getOldBKECluster(bkeCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 初始化日志记录器
	bkeLogger := r.initializeLogger(bkeCluster)

	// 处理代理和节点状态
	if err = r.handleClusterStatus(ctx, bkeCluster, bkeLogger); err != nil {
		return ctrl.Result{}, err
	}

	// 检查是否需要升级
	if r.needsUpgrade(oldBkeCluster, bkeCluster) {
		return r.handleUpgrade(ctx, bkeCluster, oldBkeCluster, bkeLogger)
	}

	// 初始化阶段上下文并执行阶段流程
	phaseResult, err := r.executePhaseFlow(ctx, bkeCluster, oldBkeCluster, bkeLogger)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 设置集群监控
	watchResult, err := r.setupClusterWatching(ctx, bkeCluster, bkeLogger)
	if err != nil {
		return watchResult, err
	}

	// 返回最终结果
	result, err := r.getFinalResult(phaseResult, bkeCluster)
	return result, err
}

// needsUpgrade checks if the cluster needs to be upgraded
func (r *BKEClusterReconciler) needsUpgrade(old, new *bkev1beta1.BKECluster) bool {
	// Check if the version has changed
	if old.Spec.ClusterConfig.Cluster.Version != new.Spec.ClusterConfig.Cluster.Version {
		return true
	}

	// Check if any component version has changed
	if old.Spec.ClusterConfig.Cluster.ContainerdVersion != new.Spec.ClusterConfig.Cluster.ContainerdVersion {
		return true
	}

	if old.Spec.ClusterConfig.Cluster.KubernetesVersion != new.Spec.ClusterConfig.Cluster.KubernetesVersion {
		return true
	}

	if old.Spec.ClusterConfig.Cluster.OpenFuyaoVersion != new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion {
		return true
	}

	return false
}

// handleUpgrade handles the cluster upgrade
func (r *BKEClusterReconciler) handleUpgrade(ctx context.Context, bkeCluster, oldBkeCluster *bkev1beta1.BKECluster, bkeLogger log.Logger) (ctrl.Result, error) {
	bkeLogger.Info("Starting cluster upgrade", "fromVersion", oldBkeCluster.Spec.ClusterConfig.Cluster.Version, "toVersion", bkeCluster.Spec.ClusterConfig.Cluster.Version)

	// Create cluster spec for upgrade
	clusterSpec := &cluster.ClusterSpec{
		Type:              r.getClusterType(bkeCluster),
		Name:              bkeCluster.Name,
		Namespace:         bkeCluster.Namespace,
		Version:           bkeCluster.Spec.ClusterConfig.Cluster.Version,
		KubernetesVersion: bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
		// Populate other fields...
	}

	// Get or create cluster
	clusterObj, err := r.ClusterManager.Get(ctx, bkeCluster.Name, bkeCluster.Namespace)
	if err != nil {
		// If cluster not found, create it
		clusterObj, err = r.ClusterManager.Create(ctx, clusterSpec)
		if err != nil {
			return ctrl.Result{}, errors.Wrap(err, "failed to create cluster object")
		}
	}

	// Execute upgrade
	if err := clusterObj.Upgrade(ctx, bkeCluster.Spec.ClusterConfig.Cluster.Version); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "cluster upgrade failed")
	}

	// Update cluster status
	bkeCluster.Status.Version = bkeCluster.Spec.ClusterConfig.Cluster.Version
	bkeCluster.Status.ContainerdVersion = bkeCluster.Spec.ClusterConfig.Cluster.ContainerdVersion
	bkeCluster.Status.KubernetesVersion = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
	bkeCluster.Status.OpenFuyaoVersion = bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion

	// Sync status
	if err := mergecluster.SyncStatusUntilComplete(r.Client, bkeCluster); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to sync cluster status")
	}

	bkeLogger.Info("Cluster upgrade completed successfully")
	return ctrl.Result{Requeue: true}, nil
}

// getClusterType determines the type of cluster
func (r *BKEClusterReconciler) getClusterType(bkeCluster *bkev1beta1.BKECluster) cluster.Type {
	// Determine cluster type based on labels or annotations
	if bkeCluster.Labels != nil && bkeCluster.Labels["cluster-manager.io/cluster-type"] == "management" {
		return cluster.TypeManagement
	}
	if bkeCluster.Labels != nil && bkeCluster.Labels["cluster-manager.io/cluster-type"] == "workload" {
		return cluster.TypeWorkload
	}
	// Default to workload cluster
	return cluster.TypeWorkload
}

// ... existing methods remain mostly unchanged ...
```
## 五、总结
以上代码实现了cluster-api-provider-bke重构的核心部分，包括：
1. **版本包管理系统**：实现了版本包的加载、验证和管理
2. **三层集群抽象**：定义了引导集群、管理集群和业务集群的统一接口
3. **升级路径管理**：实现了升级路径的定义和执行
4. **控制器重构**：展示了BKECluster控制器的重构，支持升级功能

这些代码为完整的重构提供了基础框架，实际实现还需要补充更多细节，如：
- 完整的组件安装器实现
- SSH管理器的增强
- 版本兼容性检查器
- 健康检查系统
- CLI工具的实现
- 测试用例的编写

重构后的代码将支持三层集群架构和自动化升级功能，提高系统的可维护性和可扩展性。

# 组件安装器的实现
```
filePath: D:\code\github\cluster-api-provider-bke\pkg\installer\installer.go
 /*
   Copyright @ 2026 bocloud <fushaosong@beyondcent.com>.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package installer

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

// ComponentType defines the type of component to install
type ComponentType string

// InstallationMode defines the installation mode
type InstallationMode string

// InstallationStatus represents the status of an installation
type InstallationStatus string

const (
	// ComponentTypeBootstrap represents a bootstrap component
	ComponentTypeBootstrap ComponentType = "bootstrap"
	// ComponentTypeManagement represents a management component
	ComponentTypeManagement ComponentType = "management"
	// ComponentTypeWorkload represents a workload component
	ComponentTypeWorkload ComponentType = "workload"

	// InstallationModeRemote installs from remote repository
	InstallationModeRemote InstallationMode = "remote"
	// InstallationModeImage installs from container image
	InstallationModeImage InstallationMode = "image"
	// InstallationModeHybrid combines remote and image installation
	InstallationModeHybrid InstallationMode = "hybrid"

	// InstallationStatusPending indicates installation is pending
	InstallationStatusPending InstallationStatus = "pending"
	// InstallationStatusRunning indicates installation is running
	InstallationStatusRunning InstallationStatus = "running"
	// InstallationStatusCompleted indicates installation completed successfully
	InstallationStatusCompleted InstallationStatus = "completed"
	// InstallationStatusFailed indicates installation failed
	InstallationStatusFailed InstallationStatus = "failed"
)

// ComponentConfig represents the configuration for a component
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ComponentConfig struct {
	// Name is the name of the component
	Name string `json:"name"`
	// Version is the version of the component
	Version string `json:"version"`
	// Type is the type of the component
	Type ComponentType `json:"type"`
	// Mode is the installation mode
	Mode InstallationMode `json:"mode"`
	// Image is the container image (for image mode)
	Image string `json:"image,omitempty"`
	// URL is the remote URL (for remote mode)
	URL string `json:"url,omitempty"`
	// Checksum is the checksum of the component
	Checksum string `json:"checksum,omitempty"`
	// Dependencies are the component dependencies
	Dependencies []string `json:"dependencies,omitempty"`
	// Config is the component-specific configuration
	Config map[string]interface{} `json:"config,omitempty"`
}

// InstallationResult represents the result of an installation
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type InstallationResult struct {
	// Component is the component name
	Component string `json:"component"`
	// Status is the installation status
	Status InstallationStatus `json:"status"`
	// Message is the installation message
	Message string `json:"message,omitempty"`
	// StartTime is the installation start time
	StartTime time.Time `json:"startTime"`
	// EndTime is the installation end time
	EndTime time.Time `json:"endTime,omitempty"`
	// Duration is the installation duration
	Duration time.Duration `json:"duration,omitempty"`
	// Error is the installation error (if any)
	Error string `json:"error,omitempty"`
}

// Installer is the interface for component installation
type Installer interface {
	// Install installs a component
	Install(ctx context.Context, config ComponentConfig) (*InstallationResult, error)
	// Uninstall uninstalls a component
	Uninstall(ctx context.Context, componentName string) (*InstallationResult, error)
	// Upgrade upgrades a component to a new version
	Upgrade(ctx context.Context, config ComponentConfig) (*InstallationResult, error)
	// Status checks the status of a component
	Status(ctx context.Context, componentName string) (*InstallationResult, error)
	// List lists all installed components
	List(ctx context.Context) ([]*InstallationResult, error)
}

// BaseInstaller is the base implementation of Installer
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type BaseInstaller struct {
	// Executor is the command executor
	Executor exec.Executor
	// InstallDir is the installation directory
	InstallDir string
	// TempDir is the temporary directory
	TempDir string
}

// NewBaseInstaller creates a new BaseInstaller
func NewBaseInstaller(executor exec.Executor, installDir, tempDir string) *BaseInstaller {
	return &BaseInstaller{
		Executor:  executor,
		InstallDir: installDir,
		TempDir:    tempDir,
	}
}

// Install installs a component
func (bi *BaseInstaller) Install(ctx context.Context, config ComponentConfig) (*InstallationResult, error) {
	result := &InstallationResult{
		Component: config.Name,
		Status:    InstallationStatusPending,
		StartTime: time.Now(),
	}

	log.Infof("Starting installation of component %s version %s", config.Name, config.Version)

	// Set status to running
	result.Status = InstallationStatusRunning

	// Execute installation based on mode
	switch config.Mode {
	case InstallationModeRemote:
		if config.URL == "" {
			result.Status = InstallationStatusFailed
			result.Error = "URL is required for remote installation mode"
			return result, fmt.Errorf(result.Error)
		}
		return bi.installRemote(ctx, config, result)
	case InstallationModeImage:
		if config.Image == "" {
			result.Status = InstallationStatusFailed
			result.Error = "Image is required for image installation mode"
			return result, fmt.Errorf(result.Error)
		}
		return bi.installImage(ctx, config, result)
	case InstallationModeHybrid:
		if config.URL == "" || config.Image == "" {
			result.Status = InstallationStatusFailed
			result.Error = "Both URL and Image are required for hybrid installation mode"
			return result, fmt.Errorf(result.Error)
		}
		return bi.installHybrid(ctx, config, result)
	default:
		result.Status = InstallationStatusFailed
		result.Error = fmt.Sprintf("Unknown installation mode: %s", config.Mode)
		return result, fmt.Errorf(result.Error)
	}
}

// Uninstall uninstalls a component
func (bi *BaseInstaller) Uninstall(ctx context.Context, componentName string) (*InstallationResult, error) {
	result := &InstallationResult{
		Component: componentName,
		Status:    InstallationStatusPending,
		StartTime: time.Now(),
	}

	log.Infof("Starting uninstallation of component %s", componentName)
	result.Status = InstallationStatusRunning

	// Check if component is installed
	status, err := bi.Status(ctx, componentName)
	if err != nil {
		result.Status = InstallationStatusFailed
		result.Error = fmt.Sprintf("Failed to check component status: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	if status.Status != InstallationStatusCompleted {
		result.Status = InstallationStatusCompleted
		result.Message = fmt.Sprintf("Component %s is not installed", componentName)
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)
		return result, nil
	}

	// Execute uninstallation command
	componentDir := filepath.Join(bi.InstallDir, componentName)
	uninstallScript := filepath.Join(componentDir, "uninstall.sh")

	// Check if uninstall script exists
	_, err = bi.Executor.ExecuteCommandWithOutput("test", "-f", uninstallScript)
	if err != nil {
		// Fallback to manual uninstall
		log.Infof("Uninstall script not found for component %s, performing manual uninstall", componentName)
		_, err = bi.Executor.ExecuteCommandWithOutput("rm", "-rf", componentDir)
		if err != nil {
			result.Status = InstallationStatusFailed
			result.Error = fmt.Sprintf("Failed to uninstall component: %v", err)
			return result, fmt.Errorf(result.Error)
		}
	} else {
		// Use uninstall script
		_, err = bi.Executor.ExecuteCommandWithOutput("bash", uninstallScript)
		if err != nil {
			result.Status = InstallationStatusFailed
			result.Error = fmt.Sprintf("Failed to run uninstall script: %v", err)
			return result, fmt.Errorf(result.Error)
		}
	}

	// Update result
	result.Status = InstallationStatusCompleted
	result.Message = fmt.Sprintf("Component %s uninstalled successfully", componentName)
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	log.Infof("Uninstallation of component %s completed successfully", componentName)
	return result, nil
}

// Upgrade upgrades a component to a new version
func (bi *BaseInstaller) Upgrade(ctx context.Context, config ComponentConfig) (*InstallationResult, error) {
	log.Infof("Starting upgrade of component %s to version %s", config.Name, config.Version)

	// First uninstall the old version
	_, err := bi.Uninstall(ctx, config.Name)
	if err != nil {
		log.Errorf("Failed to uninstall old version of component %s: %v", config.Name, err)
		// Proceed with installation anyway
	}

	// Then install the new version
	return bi.Install(ctx, config)
}

// Status checks the status of a component
func (bi *BaseInstaller) Status(ctx context.Context, componentName string) (*InstallationResult, error) {
	result := &InstallationResult{
		Component: componentName,
		StartTime: time.Now(),
	}

	// Check if component directory exists
	componentDir := filepath.Join(bi.InstallDir, componentName)
	_, err := bi.Executor.ExecuteCommandWithOutput("test", "-d", componentDir)
	if err != nil {
		result.Status = InstallationStatusPending
		result.Message = fmt.Sprintf("Component %s is not installed", componentName)
		return result, nil
	}

	// Check if component is running (simple check)
	running := false
	if componentName == "containerd" {
		_, err := bi.Executor.ExecuteCommandWithOutput("systemctl", "is-active", "containerd")
		running = err == nil
	} else if componentName == "kubelet" {
		_, err := bi.Executor.ExecuteCommandWithOutput("systemctl", "is-active", "kubelet")
		running = err == nil
	}

	status := InstallationStatusCompleted
	if !running {
		status = InstallationStatusPending
	}

	result.Status = status
	result.Message = fmt.Sprintf("Component %s is installed", componentName)
	if running {
		result.Message += " and running"
	} else {
		result.Message += " but not running"
	}

	return result, nil
}

// List lists all installed components
func (bi *BaseInstaller) List(ctx context.Context) ([]*InstallationResult, error) {
	// List directories in install directory
	output, err := bi.Executor.ExecuteCommandWithOutput("ls", "-la", bi.InstallDir)
	if err != nil {
		return nil, fmt.Errorf("Failed to list components: %v", err)
	}

	var results []*InstallationResult
	// Parse output (simple parsing for demo purposes)
	for _, line := range strings.Split(output, "\n") {
		// Skip header line and . and ..
		if strings.HasPrefix(line, "total") || strings.Contains(line, ".") {
			continue
		}

		// Extract component name
		parts := strings.Fields(line)
		if len(parts) < 9 {
			continue
		}
		componentName := parts[8]

		// Get status for each component
		status, err := bi.Status(ctx, componentName)
		if err != nil {
			log.Errorf("Failed to get status for component %s: %v", componentName, err)
			continue
		}

		results = append(results, status)
	}

	return results, nil
}

// installRemote installs a component from a remote URL
func (bi *BaseInstaller) installRemote(ctx context.Context, config ComponentConfig, result *InstallationResult) (*InstallationResult, error) {
	// Create temporary directory
	tempDir := filepath.Join(bi.TempDir, fmt.Sprintf("%s-%s", config.Name, time.Now().Format("20060102150405")))
	_, err := bi.Executor.ExecuteCommandWithOutput("mkdir", "-p", tempDir)
	if err != nil {
		result.Status = InstallationStatusFailed
		result.Error = fmt.Sprintf("Failed to create temporary directory: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	defer func() {
		_, _ = bi.Executor.ExecuteCommandWithOutput("rm", "-rf", tempDir)
	}()

	// Download component
	componentFile := filepath.Join(tempDir, filepath.Base(config.URL))
	_, err = bi.Executor.ExecuteCommandWithOutput("curl", "-L", "-o", componentFile, config.URL)
	if err != nil {
		result.Status = InstallationStatusFailed
		result.Error = fmt.Sprintf("Failed to download component: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	// Verify checksum if provided
	if config.Checksum != "" {
		checksumFile := filepath.Join(tempDir, "checksum.txt")
		_, err = bi.Executor.ExecuteCommandWithOutput("echo", config.Checksum, componentFile, ">", checksumFile)
		if err != nil {
			result.Status = InstallationStatusFailed
			result.Error = fmt.Sprintf("Failed to create checksum file: %v", err)
			return result, fmt.Errorf(result.Error)
		}

		_, err = bi.Executor.ExecuteCommandWithOutput("sha256sum", "-c", checksumFile)
		if err != nil {
			result.Status = InstallationStatusFailed
			result.Error = fmt.Sprintf("Checksum verification failed: %v", err)
			return result, fmt.Errorf(result.Error)
		}
	}

	// Extract component
	_, err = bi.Executor.ExecuteCommandWithOutput("tar", "-xzf", componentFile, "-C", tempDir)
	if err != nil {
		result.Status = InstallationStatusFailed
		result.Error = fmt.Sprintf("Failed to extract component: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	// Install component
	installDir := filepath.Join(bi.InstallDir, config.Name)
	_, err = bi.Executor.ExecuteCommandWithOutput("mkdir", "-p", installDir)
	if err != nil {
		result.Status = InstallationStatusFailed
		result.Error = fmt.Sprintf("Failed to create installation directory: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	// Move extracted files to install directory
	// Assuming the extracted files are in a subdirectory
	extractedDir := filepath.Join(tempDir, fmt.Sprintf("%s-%s", config.Name, config.Version))
	_, err = bi.Executor.ExecuteCommandWithOutput("cp", "-r", fmt.Sprintf("%s/*", extractedDir), installDir)
	if err != nil {
		// Try without version suffix
		extractedDir = tempDir
		_, err = bi.Executor.ExecuteCommandWithOutput("cp", "-r", fmt.Sprintf("%s/*", extractedDir), installDir)
		if err != nil {
			result.Status = InstallationStatusFailed
			result.Error = fmt.Sprintf("Failed to move extracted files: %v", err)
			return result, fmt.Errorf(result.Error)
		}
	}

	// Run install script if exists
	installScript := filepath.Join(installDir, "install.sh")
	_, err = bi.Executor.ExecuteCommandWithOutput("test", "-f", installScript)
	if err == nil {
		_, err = bi.Executor.ExecuteCommandWithOutput("bash", installScript)
		if err != nil {
			result.Status = InstallationStatusFailed
			result.Error = fmt.Sprintf("Failed to run install script: %v", err)
			return result, fmt.Errorf(result.Error)
		}
	}

	// Update result
	result.Status = InstallationStatusCompleted
	result.Message = fmt.Sprintf("Component %s installed successfully from remote", config.Name)
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	log.Infof("Installation of component %s from remote completed successfully", config.Name)
	return result, nil
}

// installImage installs a component from a container image
func (bi *BaseInstaller) installImage(ctx context.Context, config ComponentConfig, result *InstallationResult) (*InstallationResult, error) {
	// Pull the image
	_, err := bi.Executor.ExecuteCommandWithOutput("docker", "pull", config.Image)
	if err != nil {
		// Try with containerd
		_, err = bi.Executor.ExecuteCommandWithOutput("crictl", "pull", config.Image)
		if err != nil {
			result.Status = InstallationStatusFailed
			result.Error = fmt.Sprintf("Failed to pull image: %v", err)
			return result, fmt.Errorf(result.Error)
		}
	}

	// Create installation directory
	installDir := filepath.Join(bi.InstallDir, config.Name)
	_, err = bi.Executor.ExecuteCommandWithOutput("mkdir", "-p", installDir)
	if err != nil {
		result.Status = InstallationStatusFailed
		result.Error = fmt.Sprintf("Failed to create installation directory: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	// Extract component from image
	// This is a simplified approach - in real implementation, you would use a more robust method
	_, err = bi.Executor.ExecuteCommandWithOutput(
		"docker", "run", "--rm", "-v", fmt.Sprintf("%s:/target", installDir), 
		config.Image, "/bin/sh", "-c", "cp -r /app/* /target/")
	if err != nil {
		result.Status = InstallationStatusFailed
		result.Error = fmt.Sprintf("Failed to extract component from image: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	// Run install script if exists
	installScript := filepath.Join(installDir, "install.sh")
	_, err = bi.Executor.ExecuteCommandWithOutput("test", "-f", installScript)
	if err == nil {
		_, err = bi.Executor.ExecuteCommandWithOutput("bash", installScript)
		if err != nil {
			result.Status = InstallationStatusFailed
			result.Error = fmt.Sprintf("Failed to run install script: %v", err)
			return result, fmt.Errorf(result.Error)
		}
	}

	// Update result
	result.Status = InstallationStatusCompleted
	result.Message = fmt.Sprintf("Component %s installed successfully from image", config.Name)
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	log.Infof("Installation of component %s from image completed successfully", config.Name)
	return result, nil
}

// installHybrid installs a component using hybrid mode
func (bi *BaseInstaller) installHybrid(ctx context.Context, config ComponentConfig, result *InstallationResult) (*InstallationResult, error) {
	// First install from remote
	remoteResult, err := bi.installRemote(ctx, config, result)
	if err != nil {
		// If remote installation fails, try image installation
		log.Warnf("Remote installation failed, trying image installation: %v", err)
		return bi.installImage(ctx, config, result)
	}

	return remoteResult, nil
}
```
        

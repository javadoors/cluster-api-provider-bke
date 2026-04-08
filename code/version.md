       
# 基于cluster-api的商业化版本管理方案设计
## 一、版本体系架构设计
### 1.1 版本层级关系
```
商业化版本
    │
    ├─> 组件版本
    │     ├─> Cluster API Core (v1.13.0)
    │     ├─> Kubeadm ControlPlane (v1.13.0)
    │     ├─> Kubeadm Bootstrap (v1.13.0)
    │     └─> Infrastructure Provider (v1.0.0)
    │
    └─> Cluster API Contract Version
          └─> CRD API Versions (v1beta1, v1beta2)
```
### 1.2 版本映射元数据设计
创建版本映射配置文件：

filePath: d:\code\github\cluster-api\metadata.yaml
          
基于cluster-api的metadata.yaml结构，我为你设计完整的商业化版本管理方案：
## 二、版本映射元数据定义
### 2.1 商业化版本元数据结构
```yaml
# commercial-version.yaml
apiVersion: commercial.company.com/v1
kind: CommercialVersion
metadata:
  name: commercial-version-v2.0
spec:
  # 商业化版本号
  version: v2.0.0
  
  # 版本发布时间
  releaseDate: "2026-01-15"
  
  # 版本状态
  status: stable
  
  # Cluster API Contract版本
  contractVersion: v1beta2
  
  # 组件版本列表
  components:
    - name: cluster-api-core
      version: v1.13.0
      repository: kubernetes-sigs/cluster-api
      contract: v1beta2
      crdVersions:
        - v1beta1
        - v1beta2
      storageVersion: v1beta2
      
    - name: kubeadm-controlplane
      version: v1.13.0
      repository: kubernetes-sigs/cluster-api
      contract: v1beta2
      crdVersions:
        - v1beta1
        - v1beta2
      storageVersion: v1beta2
      
    - name: kubeadm-bootstrap
      version: v1.13.0
      repository: kubernetes-sigs/cluster-api
      contract: v1beta2
      crdVersions:
        - v1beta1
        - v1beta2
      storageVersion: v1beta2
      
    - name: infrastructure-provider
      version: v1.5.0
      repository: company/infrastructure-provider
      contract: v1beta2
      crdVersions:
        - v1beta2
      storageVersion: v1beta2
      
    - name: custom-addon
      version: v2.0.0
      repository: company/custom-addon
      contract: v1beta2
      crdVersions:
        - v1beta2
      storageVersion: v1beta2
  
  # 升级路径
  upgradePaths:
    from:
      - v1.0.0
      - v1.1.0
      - v1.2.0
    
  # Kubernetes版本支持
  kubernetesVersions:
    management:
      min: v1.28.0
      max: v1.32.0
    workload:
      min: v1.26.0
      max: v1.32.0
```
### 2.2 版本历史记录
```yaml
# commercial-version-history.yaml
apiVersion: commercial.company.com/v1
kind: CommercialVersionHistory
metadata:
  name: version-history
spec:
  versions:
    - version: v1.0.0
      releaseDate: "2025-06-01"
      status: eol
      contractVersion: v1beta1
      components:
        cluster-api-core: v1.10.0
        kubeadm-controlplane: v1.10.0
        kubeadm-bootstrap: v1.10.0
        infrastructure-provider: v1.0.0
      eolDate: "2026-06-01"
      
    - version: v1.1.0
      releaseDate: "2025-09-01"
      status: maintenance
      contractVersion: v1beta1
      components:
        cluster-api-core: v1.10.3
        kubeadm-controlplane: v1.10.3
        kubeadm-bootstrap: v1.10.3
        infrastructure-provider: v1.2.0
      maintenanceDate: "2026-03-01"
      eolDate: "2026-09-01"
      
    - version: v1.2.0
      releaseDate: "2025-12-01"
      status: stable
      contractVersion: v1beta2
      components:
        cluster-api-core: v1.11.0
        kubeadm-controlplane: v1.11.0
        kubeadm-bootstrap: v1.11.0
        infrastructure-provider: v1.4.0
      note: "首次引入v1beta2 contract"
      
    - version: v2.0.0
      releaseDate: "2026-01-15"
      status: stable
      contractVersion: v1beta2
      components:
        cluster-api-core: v1.13.0
        kubeadm-controlplane: v1.13.0
        kubeadm-bootstrap: v1.13.0
        infrastructure-provider: v1.5.0
        custom-addon: v2.0.0
```
## 三、版本映射Go实现
### 3.1 版本映射API定义
```go
// api/v1/commercial_version_types.go
package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ComponentVersion 定义组件版本信息
type ComponentVersion struct {
	// 组件名称
	Name string `json:"name"`
	
	// 组件版本
	Version string `json:"version"`
	
	// 组件仓库
	Repository string `json:"repository"`
	
	// Contract版本
	Contract string `json:"contract"`
	
	// 支持的CRD版本列表
	CRDVersions []string `json:"crdVersions"`
	
	// 存储版本
	StorageVersion string `json:"storageVersion"`
}

// UpgradePath 定义升级路径
type UpgradePath struct {
	// 可从哪些版本升级
	From []string `json:"from"`
}

// KubernetesVersionSupport 定义Kubernetes版本支持范围
type KubernetesVersionSupport struct {
	// 最小版本
	Min string `json:"min"`
	
	// 最大版本
	Max string `json:"max"`
}

// CommercialVersionSpec 定义商业化版本规格
type CommercialVersionSpec struct {
	// 商业化版本号
	Version string `json:"version"`
	
	// 发布日期
	ReleaseDate string `json:"releaseDate"`
	
	// 版本状态
	Status string `json:"status"`
	
	// Cluster API Contract版本
	ContractVersion string `json:"contractVersion"`
	
	// 组件版本列表
	Components []ComponentVersion `json:"components"`
	
	// 升级路径
	UpgradePaths UpgradePath `json:"upgradePaths"`
	
	// Kubernetes版本支持
	KubernetesVersions struct {
		Management KubernetesVersionSupport `json:"management"`
		Workload   KubernetesVersionSupport `json:"workload"`
	} `json:"kubernetesVersions"`
}

// CommercialVersionStatus 定义商业化版本状态
type CommercialVersionStatus struct {
	// 当前安装的版本
	InstalledVersion string `json:"installedVersion,omitempty"`
	
	// 当前Contract版本
	CurrentContract string `json:"currentContract,omitempty"`
	
	// 各组件当前版本
	ComponentVersions map[string]string `json:"componentVersions,omitempty"`
	
	// 升级可用性
	UpgradeAvailable bool `json:"upgradeAvailable,omitempty"`
	
	// 可升级到的目标版本
	AvailableUpgrades []string `json:"availableUpgrades,omitempty"`
	
	// 最后更新时间
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// CommercialVersion 商业化版本资源
type CommercialVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	
	Spec   CommercialVersionSpec   `json:"spec,omitempty"`
	Status CommercialVersionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CommercialVersionList 商业化版本列表
type CommercialVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CommercialVersion `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CommercialVersion{}, &CommercialVersionList{})
}
```
### 3.2 版本管理控制器
```go
// controllers/commercialversion_controller.go
package controllers

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commercialv1 "company.com/commercial-version-manager/api/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// CommercialVersionReconciler 商业化版本控制器
type CommercialVersionReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=commercial.company.com,resources=commercialversions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=commercial.company.com,resources=commercialversions/status,verbs=get;update;patch

func (r *CommercialVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("commercialversion", req.NamespacedName)
	
	// 获取CommercialVersion资源
	commercialVersion := &commercialv1.CommercialVersion{}
	if err := r.Get(ctx, req.NamespacedName, commercialVersion); err != nil {
		if errors.IsNotFound(err) {
			log.Info("CommercialVersion resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get CommercialVersion")
		return ctrl.Result{}, err
	}
	
	// 检测当前安装的版本
	installedVersion, err := r.detectInstalledVersion(ctx)
	if err != nil {
		log.Error(err, "Failed to detect installed version")
		return ctrl.Result{}, err
	}
	
	// 检测当前Contract版本
	currentContract, err := r.detectCurrentContract(ctx)
	if err != nil {
		log.Error(err, "Failed to detect current contract")
		return ctrl.Result{}, err
	}
	
	// 检测各组件版本
	componentVersions, err := r.detectComponentVersions(ctx)
	if err != nil {
		log.Error(err, "Failed to detect component versions")
		return ctrl.Result{}, err
	}
	
	// 检查升级可用性
	availableUpgrades, err := r.checkAvailableUpgrades(ctx, installedVersion)
	if err != nil {
		log.Error(err, "Failed to check available upgrades")
		return ctrl.Result{}, err
	}
	
	// 更新状态
	commercialVersion.Status.InstalledVersion = installedVersion
	commercialVersion.Status.CurrentContract = currentContract
	commercialVersion.Status.ComponentVersions = componentVersions
	commercialVersion.Status.UpgradeAvailable = len(availableUpgrades) > 0
	commercialVersion.Status.AvailableUpgrades = availableUpgrades
	
	if err := r.Status().Update(ctx, commercialVersion); err != nil {
		log.Error(err, "Failed to update CommercialVersion status")
		return ctrl.Result{}, err
	}
	
	return ctrl.Result{}, nil
}

// detectInstalledVersion 检测当前安装的商业化版本
func (r *CommercialVersionReconciler) detectInstalledVersion(ctx context.Context) (string, error) {
	// 通过ConfigMap或Deployment获取当前版本
	configMap := &clusterv1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      "commercial-version-info",
		Namespace: "commercial-system",
	}, configMap); err != nil {
		if errors.IsNotFound(err) {
			return "unknown", nil
		}
		return "", err
	}
	
	version, ok := configMap.Data["version"]
	if !ok {
		return "unknown", nil
	}
	
	return version, nil
}

// detectCurrentContract 检测当前Contract版本
func (r *CommercialVersionReconciler) detectCurrentContract(ctx context.Context) (string, error) {
	// 通过检查Cluster CRD的label来确定contract版本
	clusterCRD := &clusterv1.CustomResourceDefinition{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: "clusters.cluster.x-k8s.io",
	}, clusterCRD); err != nil {
		return "", err
	}
	
	// 检查label中的contract版本
	labels := clusterCRD.GetLabels()
	for key := range labels {
		if key == "cluster.x-k8s.io/v1beta2" {
			return "v1beta2", nil
		}
		if key == "cluster.x-k8s.io/v1beta1" {
			return "v1beta1", nil
		}
	}
	
	return "", fmt.Errorf("contract version not found in CRD labels")
}

// detectComponentVersions 检测各组件版本
func (r *CommercialVersionReconciler) detectComponentVersions(ctx context.Context) (map[string]string, error) {
	versions := make(map[string]string)
	
	// 检测Cluster API Core版本
	clusterAPIVersion, err := r.getDeploymentVersion(ctx, "capi-controller-manager", "capi-system")
	if err != nil {
		return nil, err
	}
	versions["cluster-api-core"] = clusterAPIVersion
	
	// 检测Kubeadm ControlPlane版本
	kcpVersion, err := r.getDeploymentVersion(ctx, "capi-kubeadm-control-plane-controller-manager", "capi-kubeadm-control-plane-system")
	if err != nil {
		return nil, err
	}
	versions["kubeadm-controlplane"] = kcpVersion
	
	// 检测Kubeadm Bootstrap版本
	kubeadmVersion, err := r.getDeploymentVersion(ctx, "capi-kubeadm-bootstrap-controller-manager", "capi-kubeadm-bootstrap-system")
	if err != nil {
		return nil, err
	}
	versions["kubeadm-bootstrap"] = kubeadmVersion
	
	// 检测Infrastructure Provider版本
	infraVersion, err := r.getDeploymentVersion(ctx, "infrastructure-controller-manager", "infrastructure-system")
	if err != nil {
		return nil, err
	}
	versions["infrastructure-provider"] = infraVersion
	
	return versions, nil
}

// getDeploymentVersion 从Deployment获取版本
func (r *CommercialVersionReconciler) getDeploymentVersion(ctx context.Context, name, namespace string) (string, error) {
	deployment := &clusterv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, deployment); err != nil {
		return "", err
	}
	
	// 从Deployment的annotation或label获取版本
	if version, ok := deployment.GetLabels()["app.kubernetes.io/version"]; ok {
		return version, nil
	}
	
	return "unknown", nil
}

// checkAvailableUpgrades 检查可用的升级版本
func (r *CommercialVersionReconciler) checkAvailableUpgrades(ctx context.Context, currentVersion string) ([]string, error) {
	// 获取所有CommercialVersion资源
	versionList := &commercialv1.CommercialVersionList{}
	if err := r.List(ctx, versionList); err != nil {
		return nil, err
	}
	
	var availableUpgrades []string
	
	for _, version := range versionList.Items {
		// 检查是否在升级路径中
		for _, fromVersion := range version.Spec.UpgradePaths.From {
			if fromVersion == currentVersion {
				availableUpgrades = append(availableUpgrades, version.Spec.Version)
				break
			}
		}
	}
	
	// 排序版本
	sort.Strings(availableUpgrades)
	
	return availableUpgrades, nil
}

func (r *CommercialVersionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&commercialv1.CommercialVersion{}).
		Complete(r)
}
```
### 3.3 版本兼容性检查器
```go
// pkg/version/compatibility_checker.go
package version

import (
	"context"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commercialv1 "company.com/commercial-version-manager/api/v1"
)

// CompatibilityChecker 版本兼容性检查器
type CompatibilityChecker struct {
	client client.Client
}

// NewCompatibilityChecker 创建兼容性检查器
func NewCompatibilityChecker(client client.Client) *CompatibilityChecker {
	return &CompatibilityChecker{
		client: client,
	}
}

// CheckUpgradeCompatibility 检查升级兼容性
func (c *CompatibilityChecker) CheckUpgradeCompatibility(
	ctx context.Context,
	fromVersion, toVersion string,
) (*CompatibilityResult, error) {
	result := &CompatibilityResult{
		Compatible:    true,
		Warnings:      []string{},
		Errors:        []string{},
		ContractCheck: &ContractCheckResult{},
	}
	
	// 获取源版本和目标版本信息
	fromVersionInfo, err := c.getVersionInfo(ctx, fromVersion)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get from version info: %s", fromVersion)
	}
	
	toVersionInfo, err := c.getVersionInfo(ctx, toVersion)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get to version info: %s", toVersion)
	}
	
	// 1. 检查Contract版本兼容性
	if err := c.checkContractCompatibility(fromVersionInfo, toVersionInfo, result); err != nil {
		return nil, err
	}
	
	// 2. 检查组件版本兼容性
	if err := c.checkComponentCompatibility(fromVersionInfo, toVersionInfo, result); err != nil {
		return nil, err
	}
	
	// 3. 检查CRD版本兼容性
	if err := c.checkCRDCompatibility(fromVersionInfo, toVersionInfo, result); err != nil {
		return nil, err
	}
	
	// 4. 检查Kubernetes版本兼容性
	if err := c.checkKubernetesCompatibility(fromVersionInfo, toVersionInfo, result); err != nil {
		return nil, err
	}
	
	// 5. 检查升级路径
	if err := c.checkUpgradePath(fromVersion, toVersionInfo, result); err != nil {
		return nil, err
	}
	
	// 如果有错误，标记为不兼容
	if len(result.Errors) > 0 {
		result.Compatible = false
	}
	
	return result, nil
}

// getVersionInfo 获取版本信息
func (c *CompatibilityChecker) getVersionInfo(ctx context.Context, version string) (*commercialv1.CommercialVersion, error) {
	versionList := &commercialv1.CommercialVersionList{}
	if err := c.client.List(ctx, versionList); err != nil {
		return nil, err
	}
	
	for _, v := range versionList.Items {
		if v.Spec.Version == version {
			return &v, nil
		}
	}
	
	return nil, fmt.Errorf("version %s not found", version)
}

// checkContractCompatibility 检查Contract版本兼容性
func (c *CompatibilityChecker) checkContractCompatibility(
	from, to *commercialv1.CommercialVersion,
	result *CompatibilityResult,
) error {
	fromContract := from.Spec.ContractVersion
	toContract := to.Spec.ContractVersion
	
	// Contract版本相同，完全兼容
	if fromContract == toContract {
		result.ContractCheck.SameContract = true
		result.ContractCheck.FromContract = fromContract
		result.ContractCheck.ToContract = toContract
		return nil
	}
	
	// Contract版本不同，检查兼容性
	compatibleContracts := c.getCompatibleContracts(toContract)
	if !compatibleContracts.Has(fromContract) {
		result.Errors = append(result.Errors, 
			fmt.Sprintf("contract version incompatible: from %s to %s", fromContract, toContract))
		result.ContractCheck.SameContract = false
		result.ContractCheck.FromContract = fromContract
		result.ContractCheck.ToContract = toContract
		return nil
	}
	
	// 兼容但有警告
	result.Warnings = append(result.Warnings,
		fmt.Sprintf("contract version will change from %s to %s, ensure all providers are upgraded", 
			fromContract, toContract))
	result.ContractCheck.SameContract = false
	result.ContractCheck.FromContract = fromContract
	result.ContractCheck.ToContract = toContract
	
	return nil
}

// getCompatibleContracts 获取兼容的Contract版本
func (c *CompatibilityChecker) getCompatibleContracts(contract string) sets.Set[string] {
	compatible := sets.New(contract)
	
	// v1beta2临时兼容v1beta1
	if contract == "v1beta2" {
		compatible.Insert("v1beta1")
	}
	
	return compatible
}

// checkComponentCompatibility 检查组件版本兼容性
func (c *CompatibilityChecker) checkComponentCompatibility(
	from, to *commercialv1.CommercialVersion,
	result *CompatibilityResult,
) error {
	fromComponents := make(map[string]commercialv1.ComponentVersion)
	for _, comp := range from.Spec.Components {
		fromComponents[comp.Name] = comp
	}
	
	for _, toComp := range to.Spec.Components {
		fromComp, exists := fromComponents[toComp.Name]
		
		if !exists {
			// 新增组件
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("new component added: %s (%s)", toComp.Name, toComp.Version))
			continue
		}
		
		// 检查版本升级方向
		fromSemver, err := semver.NewVersion(fromComp.Version)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("failed to parse version for component %s: %s", fromComp.Name, fromComp.Version))
			continue
		}
		
		toSemver, err := semver.NewVersion(toComp.Version)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("failed to parse version for component %s: %s", toComp.Name, toComp.Version))
			continue
		}
		
		// 版本降级不允许
		if toSemver.LessThan(fromSemver) {
			result.Errors = append(result.Errors,
				fmt.Sprintf("component downgrade not allowed: %s from %s to %s",
					toComp.Name, fromComp.Version, toComp.Version))
		}
		
		// 大版本跳跃警告
		if toSemver.Major() > fromSemver.Major()+1 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("major version jump for component %s: %s -> %s",
					toComp.Name, fromComp.Version, toComp.Version))
		}
	}
	
	return nil
}

// checkCRDCompatibility 检查CRD版本兼容性
func (c *CompatibilityChecker) checkCRDCompatibility(
	from, to *commercialv1.CommercialVersion,
	result *CompatibilityResult,
) error {
	// 检查CRD存储版本变化
	for _, toComp := range to.Spec.Components {
		for _, fromComp := range from.Spec.Components {
			if toComp.Name != fromComp.Name {
				continue
			}
			
			// 存储版本变化
			if toComp.StorageVersion != fromComp.StorageVersion {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("CRD storage version change for %s: %s -> %s, migration required",
						toComp.Name, fromComp.StorageVersion, toComp.StorageVersion))
				
				// 检查是否支持旧版本CRD
				supportedVersions := sets.New(toComp.CRDVersions...)
				for _, oldVersion := range fromComp.CRDVersions {
					if !supportedVersions.Has(oldVersion) {
						result.Errors = append(result.Errors,
							fmt.Sprintf("CRD version %s not supported in new version for component %s",
								oldVersion, toComp.Name))
					}
				}
			}
		}
	}
	
	return nil
}

// checkKubernetesCompatibility 检查Kubernetes版本兼容性
func (c *CompatibilityChecker) checkKubernetesCompatibility(
	from, to *commercialv1.CommercialVersion,
	result *CompatibilityResult,
) error {
	// 检查管理集群Kubernetes版本范围
	fromK8s := from.Spec.KubernetesVersions.Management
	toK8s := to.Spec.KubernetesVersions.Management
	
	if toK8s.Min > fromK8s.Min {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("minimum Kubernetes version increased from %s to %s", fromK8s.Min, toK8s.Min))
	}
	
	if toK8s.Max < fromK8s.Max {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("maximum Kubernetes version decreased from %s to %s", fromK8s.Max, toK8s.Max))
	}
	
	return nil
}

// checkUpgradePath 检查升级路径
func (c *CompatibilityChecker) checkUpgradePath(
	fromVersion string,
	to *commercialv1.CommercialVersion,
	result *CompatibilityResult,
) error {
	allowed := false
	for _, allowedFrom := range to.Spec.UpgradePaths.From {
		if allowedFrom == fromVersion {
			allowed = true
			break
		}
	}
	
	if !allowed {
		result.Errors = append(result.Errors,
			fmt.Sprintf("upgrade from %s to %s is not in the allowed upgrade path", 
				fromVersion, to.Spec.Version))
	}
	
	return nil
}

// CompatibilityResult 兼容性检查结果
type CompatibilityResult struct {
	Compatible    bool
	Warnings      []string
	Errors        []string
	ContractCheck *ContractCheckResult
}

// ContractCheckResult Contract检查结果
type ContractCheckResult struct {
	SameContract bool
	FromContract string
	ToContract   string
}

// IsContractUpgrade 是否是Contract升级
func (r *CompatibilityResult) IsContractUpgrade() bool {
	return !r.ContractCheck.SameContract
}

// GetUpgradeSummary 获取升级摘要
func (r *CompatibilityResult) GetUpgradeSummary() string {
	var summary strings.Builder
	
	summary.WriteString(fmt.Sprintf("Compatible: %v\n", r.Compatible))
	
	if len(r.Warnings) > 0 {
		summary.WriteString("\nWarnings:\n")
		for _, w := range r.Warnings {
			summary.WriteString(fmt.Sprintf("  - %s\n", w))
		}
	}
	
	if len(r.Errors) > 0 {
		summary.WriteString("\nErrors:\n")
		for _, e := range r.Errors {
			summary.WriteString(fmt.Sprintf("  - %s\n", e))
		}
	}
	
	if r.IsContractUpgrade() {
		summary.WriteString(fmt.Sprintf("\nContract Upgrade: %s -> %s\n",
			r.ContractCheck.FromContract, r.ContractCheck.ToContract))
	}
	
	return summary.String()
}
```
### 3.4 升级执行器
```go
// pkg/upgrade/executor.go
package upgrade

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commercialv1 "company.com/commercial-version-manager/api/v1"
	"company.com/commercial-version-manager/pkg/version"
)

// Executor 升级执行器
type Executor struct {
	client              client.Client
	log                 logr.Logger
	compatibilityChecker *version.CompatibilityChecker
}

// NewExecutor 创建升级执行器
func NewExecutor(client client.Client, log logr.Logger) *Executor {
	return &Executor{
		client:               client,
		log:                  log,
		compatibilityChecker: version.NewCompatibilityChecker(client),
	}
}

// UpgradePlan 升级计划
type UpgradePlan struct {
	FromVersion string
	ToVersion   string
	Steps       []UpgradeStep
}

// UpgradeStep 升级步骤
type UpgradeStep struct {
	Name        string
	Description string
	Action      string
	Required    bool
}

// Execute 执行升级
func (e *Executor) Execute(ctx context.Context, fromVersion, toVersion string, dryRun bool) error {
	log := e.log.WithValues("from", fromVersion, "to", toVersion)
	
	// 1. 检查兼容性
	compatResult, err := e.compatibilityChecker.CheckUpgradeCompatibility(ctx, fromVersion, toVersion)
	if err != nil {
		return errors.Wrap(err, "failed to check compatibility")
	}
	
	if !compatResult.Compatible {
		return fmt.Errorf("upgrade not compatible:\n%s", compatResult.GetUpgradeSummary())
	}
	
	log.Info("Compatibility check passed", "warnings", len(compatResult.Warnings))
	
	// 2. 生成升级计划
	plan, err := e.generateUpgradePlan(ctx, fromVersion, toVersion, compatResult)
	if err != nil {
		return errors.Wrap(err, "failed to generate upgrade plan")
	}
	
	log.Info("Upgrade plan generated", "steps", len(plan.Steps))
	
	// 3. 如果是dry-run，只返回计划
	if dryRun {
		e.printUpgradePlan(plan)
		return nil
	}
	
	// 4. 执行升级步骤
	return e.executeUpgradeSteps(ctx, plan, log)
}

// generateUpgradePlan 生成升级计划
func (e *Executor) generateUpgradePlan(
	ctx context.Context,
	fromVersion, toVersion string,
	compatResult *version.CompatibilityResult,
) (*UpgradePlan, error) {
	plan := &UpgradePlan{
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		Steps:       []UpgradeStep{},
	}
	
	// 步骤1: Pre-upgrade检查
	plan.Steps = append(plan.Steps, UpgradeStep{
		Name:        "pre-upgrade-check",
		Description: "Pre-upgrade validation and health check",
		Action:      "check",
		Required:    true,
	})
	
	// 步骤2: Contract升级（如果需要）
	if compatResult.IsContractUpgrade() {
		plan.Steps = append(plan.Steps, UpgradeStep{
			Name:        "contract-upgrade",
			Description: fmt.Sprintf("Upgrade contract from %s to %s",
				compatResult.ContractCheck.FromContract, compatResult.ContractCheck.ToContract),
			Action:   "contract-migration",
			Required: true,
		})
		
		// CRD迁移
		plan.Steps = append(plan.Steps, UpgradeStep{
			Name:        "crd-migration",
			Description: "Migrate CRDs to new storage version",
			Action:      "crd-migration",
			Required:    true,
		})
	}
	
	// 步骤3: 升级Core Provider
	plan.Steps = append(plan.Steps, UpgradeStep{
		Name:        "upgrade-core-provider",
		Description: "Upgrade Cluster API Core Provider",
		Action:      "upgrade-component",
		Required:    true,
	})
	
	// 步骤4: 升级Bootstrap Provider
	plan.Steps = append(plan.Steps, UpgradeStep{
		Name:        "upgrade-bootstrap-provider",
		Description: "Upgrade Kubeadm Bootstrap Provider",
		Action:      "upgrade-component",
		Required:    true,
	})
	
	// 步骤5: 升级ControlPlane Provider
	plan.Steps = append(plan.Steps, UpgradeStep{
		Name:        "upgrade-controlplane-provider",
		Description: "Upgrade Kubeadm ControlPlane Provider",
		Action:      "upgrade-component",
		Required:    true,
	})
	
	// 步骤6: 升级Infrastructure Provider
	plan.Steps = append(plan.Steps, UpgradeStep{
		Name:        "upgrade-infrastructure-provider",
		Description: "Upgrade Infrastructure Provider",
		Action:      "upgrade-component",
		Required:    true,
	})
	
	// 步骤7: 升级自定义组件
	plan.Steps = append(plan.Steps, UpgradeStep{
		Name:        "upgrade-custom-components",
		Description: "Upgrade custom components",
		Action:      "upgrade-component",
		Required:    false,
	})
	
	// 步骤8: Post-upgrade验证
	plan.Steps = append(plan.Steps, UpgradeStep{
		Name:        "post-upgrade-verification",
		Description: "Post-upgrade verification and health check",
		Action:      "verify",
		Required:    true,
	})
	
	// 步骤9: 更新版本信息
	plan.Steps = append(plan.Steps, UpgradeStep{
		Name:        "update-version-info",
		Description: "Update commercial version info",
		Action:      "update-config",
		Required:    true,
	})
	
	return plan, nil
}

// executeUpgradeSteps 执行升级步骤
func (e *Executor) executeUpgradeSteps(ctx context.Context, plan *UpgradePlan, log logr.Logger) error {
	for i, step := range plan.Steps {
		log.Info("Executing upgrade step",
			"step", i+1,
			"total", len(plan.Steps),
			"name", step.Name,
			"description", step.Description)
		
		// 使用重试机制执行每个步骤
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			return e.executeStep(ctx, step, log)
		})
		
		if err != nil {
			if step.Required {
				return errors.Wrapf(err, "failed to execute required step %s", step.Name)
			}
			log.Error(err, "Failed to execute optional step, continuing", "step", step.Name)
		}
		
		// 等待步骤完成
		if err := e.waitForStepCompletion(ctx, step, log); err != nil {
			return errors.Wrapf(err, "step %s did not complete successfully", step.Name)
		}
	}
	
	return nil
}

// executeStep 执行单个步骤
func (e *Executor) executeStep(ctx context.Context, step UpgradeStep, log logr.Logger) error {
	switch step.Action {
	case "check":
		return e.executePreUpgradeCheck(ctx, log)
		
	case "contract-migration":
		return e.executeContractMigration(ctx, log)
		
	case "crd-migration":
		return e.executeCRDMigration(ctx, log)
		
	case "upgrade-component":
		return e.executeComponentUpgrade(ctx, step.Name, log)
		
	case "verify":
		return e.executePostUpgradeVerification(ctx, log)
		
	case "update-config":
		return e.updateVersionInfo(ctx, log)
		
	default:
		return fmt.Errorf("unknown action: %s", step.Action)
	}
}

// executePreUpgradeCheck 执行升级前检查
func (e *Executor) executePreUpgradeCheck(ctx context.Context, log logr.Logger) error {
	log.Info("Running pre-upgrade checks")
	
	// 检查所有Cluster是否健康
	// 检查所有Machine是否正常
	// 检查管理集群资源是否充足
	
	// TODO: 实现具体的检查逻辑
	
	return nil
}

// executeContractMigration 执行Contract迁移
func (e *Executor) executeContractMigration(ctx context.Context, log logr.Logger) error {
	log.Info("Executing contract migration")
	
	// 1. 更新所有CRD的label
	// 2. 触发conversion webhook转换现有资源
	// 3. 验证转换完成
	
	// TODO: 实现具体的迁移逻辑
	
	return nil
}

// executeCRDMigration 执行CRD迁移
func (e *Executor) executeCRDMigration(ctx context.Context, log logr.Logger) error {
	log.Info("Executing CRD migration")
	
	// 1. 应用新的CRD定义
	// 2. 等待CRD ready
	// 3. 验证存储版本
	
	// TODO: 实现具体的CRD迁移逻辑
	
	return nil
}

// executeComponentUpgrade 执行组件升级
func (e *Executor) executeComponentUpgrade(ctx context.Context, componentName string, log logr.Logger) error {
	log.Info("Upgrading component", "component", componentName)
	
	// 1. 获取目标版本的组件信息
	// 2. 应用新的Deployment/StatefulSet
	// 3. 等待Pod ready
	// 4. 验证组件健康
	
	// TODO: 实现具体的组件升级逻辑
	
	return nil
}

// executePostUpgradeVerification 执行升级后验证
func (e *Executor) executePostUpgradeVerification(ctx context.Context, log logr.Logger) error {
	log.Info("Running post-upgrade verification")
	
	// 1. 验证所有CRD版本正确
	// 2. 验证所有组件版本正确
	// 3. 验证所有Cluster健康
	// 4. 验证所有Machine正常
	
	// TODO: 实现具体的验证逻辑
	
	return nil
}

// updateVersionInfo 更新版本信息
func (e *Executor) updateVersionInfo(ctx context.Context, log logr.Logger) error {
	log.Info("Updating version info")
	
	// 更新ConfigMap中的版本信息
	// 更新CommercialVersion资源状态
	
	// TODO: 实现具体的更新逻辑
	
	return nil
}

// waitForStepCompletion 等待步骤完成
func (e *Executor) waitForStepCompletion(ctx context.Context, step UpgradeStep, log logr.Logger) error {
	// 根据步骤类型等待不同的时间
	waitDuration := 10 * time.Second
	
	switch step.Action {
	case "contract-migration", "crd-migration":
		waitDuration = 30 * time.Second
	case "upgrade-component":
		waitDuration = 60 * time.Second
	}
	
	log.Info("Waiting for step to complete", "step", step.Name, "duration", waitDuration)
	
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, waitDuration, true, func(ctx context.Context) (bool, error) {
		// TODO: 实现具体的完成检查逻辑
		return true, nil
	})
}

// printUpgradePlan 打印升级计划
func (e *Executor) printUpgradePlan(plan *UpgradePlan) {
	fmt.Printf("Upgrade Plan: %s -> %s\n", plan.FromVersion, plan.ToVersion)
	fmt.Printf("Total Steps: %d\n\n", len(plan.Steps))
	
	for i, step := range plan.Steps {
		fmt.Printf("Step %d: %s\n", i+1, step.Name)
		fmt.Printf("  Description: %s\n", step.Description)
		fmt.Printf("  Action: %s\n", step.Action)
		fmt.Printf("  Required: %v\n\n", step.Required)
	}
}
```
## 四、升级流程示例
### 4.1 从v1.0升级到v2.0的完整流程
```bash
# 1. 查看当前版本
kubectl get commercialversion -n commercial-system

# 2. 检查可用升级
kubectl describe commercialversion commercial-version -n commercial-system

# 3. 执行dry-run检查
kubectl apply -f upgrade-request.yaml --dry-run=client

# upgrade-request.yaml
apiVersion: commercial.company.com/v1
kind: UpgradeRequest
metadata:
  name: upgrade-to-v2.0
  namespace: commercial-system
spec:
  fromVersion: v1.0.0
  toVersion: v2.0.0
  dryRun: true

# 4. 执行实际升级
kubectl apply -f upgrade-request.yaml
# upgrade-request.yaml
apiVersion: commercial.company.com/v1
kind: UpgradeRequest
metadata:
  name: upgrade-to-v2.0
  namespace: commercial-system
spec:
  fromVersion: v1.0.0
  toVersion: v2.0.0
  dryRun: false

# 5. 监控升级进度
kubectl get upgraderequest upgrade-to-v2.0 -n commercial-system -w

# 6. 验证升级结果
kubectl get commercialversion -n commercial-system
kubectl get clusters -A
kubectl get machines -A
```
### 4.2 版本映射关系表
| 商业化版本 | Contract | Cluster API | Kubeadm CP | Kubeadm Bootstrap | Infra Provider | CRD Versions |
|-----------|----------|-------------|------------|-------------------|----------------|--------------|
| v1.0.0 | v1beta1 | v1.10.0 | v1.10.0 | v1.10.0 | v1.0.0 | v1beta1 |
| v1.1.0 | v1beta1 | v1.10.3 | v1.10.3 | v1.10.3 | v1.2.0 | v1beta1 |
| v1.2.0 | v1beta2 | v1.11.0 | v1.11.0 | v1.11.0 | v1.4.0 | v1beta1, v1beta2 |
| v2.0.0 | v1beta2 | v1.13.0 | v1.13.0 | v1.13.0 | v1.5.0 | v1beta1, v1beta2 |
## 五、关键设计要点
### 5.1 版本映射原则
1. **Contract版本优先**: 商业化版本的Contract版本决定整体兼容性
2. **组件版本独立**: 各组件可以独立升级，但必须符合Contract约束
3. **CRD版本兼容**: 新版本必须支持旧版本的CRD（通过conversion webhook）
4. **升级路径明确**: 明确定义允许的升级路径，防止跳跃式升级
### 5.2 升级策略
1. **滚动升级**: 逐个组件升级，确保服务连续性
2. **版本锁定**: 升级过程中锁定资源，防止并发修改
3. **回滚机制**: 升级失败时支持回滚到前一版本
4. **健康检查**: 每个步骤后进行健康检查，确保系统稳定
### 5.3 注意事项
1. **Contract升级**: v1beta1→v1beta2是一次重大变更，需要充分测试
2. **数据迁移**: CRD存储版本变更需要数据迁移
3. **Provider兼容**: 确保所有provider都支持新的Contract版本
4. **Kubernetes版本**: 注意管理集群和工作集群的Kubernetes版本要求

这个方案提供了完整的版本管理和升级框架，可以根据实际需求进行调整和扩展。
        
# 基于对cluster-api代码库的深入分析,我为你整理了CRD版本体系的完整分析
## Cluster-API CRD版本体系分析
### 一、CRD版本概览
#### 1. 核心CRD及其版本
cluster-api中的主要CRD都支持两个API版本:

| CRD类型 | 支持的版本 | 存储版本 | 状态 |
|---------|-----------|---------|------|
| Cluster | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| Machine | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| MachineSet | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| MachineDeployment | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| MachinePool | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| ClusterClass | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| KubeadmConfig | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| KubeadmControlPlane | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| IPAddress | v1beta1, v1beta2 | v1beta2 | v1beta1已废弃 |
| ExtensionConfig | v1beta2 | v1beta2 | 仅v1beta2 |

**关键特征**:
- **v1beta1**: 标记为`deprecated: true`, `storage: false`,但仍被served
- **v1beta2**: 当前存储版本,标记为`storage: true`
- 通过Conversion Webhook实现版本间的无损转换
### 二、Contract Version(合约版本)机制
#### 1. Contract Version定义
Contract Version是cluster-api定义的**全局版本概念**,用于规范provider与core之间的交互规则。

**位置**: [metadata.yaml](file:///d:/code/github/cluster-api/metadata.yaml)

```yaml
releaseSeries:
  - major: 1
    minor: 13
    contract: v1beta2
  - major: 1
    minor: 12
    contract: v1beta2
  - major: 1
    minor: 11
    contract: v1beta2
  - major: 1
    minor: 10
    contract: v1beta1
  # ... v1.0-v1.10都是v1beta1 contract
```
#### 2. Contract Version与API Version的关系
**核心约定**([version.go:29](file:///d:/code/github/cluster-api/internal/contract/version.go#L29)):
```go
var (
    Version = clusterv1.GroupVersion.Version
)
```
**重要原则**:
- 每个Cluster API release支持**一个**contract version
- Contract version**约定等于**该release的最新API version
- Contract version定义在release级别,跨patch版本不变
### 三、CRD Version与Contract Version的关系
#### 1. CRD Labels标记机制
CRD通过labels标记其支持的contract version和对应的API version:

**示例**([bootstrap kustomization.yaml](file:///d:/code/github/cluster-api/bootstrap/kubeadm/config/crd/kustomization.yaml#L1)):
```yaml
labels:
- pairs:
    cluster.x-k8s.io/v1beta1: v1beta1
    cluster.x-k8s.io/v1beta2: v1beta2
```
**Label格式**: `cluster.x-k8s.io/<contract-version>: <api-version>`

**作用**:
- Topology reconciler通过label确定CRD支持的contract
- 在ClusterClass场景下确定provider实现的contract版本
#### 2. 版本兼容性机制
**定义**([version.go:32-40](file:///d:/code/github/cluster-api/internal/contract/version.go#L32)):
```go
func GetCompatibleVersions(contract string) sets.Set[string] {
    compatibleContracts := sets.New(contract)
    if contract == "v1beta2" {
        compatibleContracts.Insert("v1beta1")
    }
    return compatibleContracts
}
```
**兼容规则**:
- v1beta2 contract**临时兼容**v1beta1 contract
- 允许v1beta2 core provider与v1beta1 infrastructure provider共存
- 兼容性将在v1beta1 EOL时(v1.16, 2027年4月)移除
### 四、版本配套关系分析
#### 1. 版本层级关系
```
Release Version (如 v1.13.0)
    └─> Contract Version (v1beta2)
           ├─> 定义provider交互规则
           └─> 映射到API Version
                  ├─> v1beta2 (当前/stable)
                  └─> v1beta1 (deprecated/兼容)
```
#### 2. 版本配套矩阵
| Cluster API Release | Contract Version | 支持的API Versions | 存储版本 | 兼容的Contracts |
|---------------------|------------------|-------------------|---------|----------------|
| v1.13.x | v1beta2 | v1beta1, v1beta2 | v1beta2 | v1beta1, v1beta2 |
| v1.12.x | v1beta2 | v1beta1, v1beta2 | v1beta2 | v1beta1, v1beta2 |
| v1.11.x | v1beta2 | v1beta1, v1beta2 | v1beta2 | v1beta1, v1beta2 |
| v1.10.x | v1beta1 | v1beta1 | v1beta1 | v1beta1 |
| v1.0-v1.9 | v1beta1 | v1beta1 | v1beta1 | v1beta1 |
#### 3. Hub-Spoke转换模式
cluster-api采用标准的Kubernetes多版本转换模式:

**Hub版本**(存储版本): v1beta2
- 定义在[v1beta2/conversion.go](file:///d:/code/github/cluster-api/api/core/v1beta2/conversion.go#L23)
```go
func (*Cluster) Hub() {}
func (*Machine) Hub() {}
```
**Spoke版本**: v1beta1
- 实现ConvertTo/ConvertFrom方法
- 通过[v1beta1/conversion.go](file:///d:/code/github/cluster-api/api/core/v1beta1/conversion.go#L35)实现转换逻辑
### 五、全局Version的设计建议
基于以上分析,针对你计划定义的**全局整体version**,建议如下:
#### 1. 全局Version应等同于Contract Version
**原因**:
- Contract version已经是cluster-api的全局版本概念
- 它定义了整个provider生态的版本契约
- 与release版本有明确映射关系
#### 2. 版本配套规则
```
全局Version (Contract Version)
    ├─> Core Provider版本
    │     └─> 必须实现该contract的所有CRD字段和行为
    │
    ├─> Infrastructure Provider版本
    │     ├─> 理想: 实现相同contract
    │     └─> 兼容: 实现兼容的旧contract (临时)
    │
    ├─> Bootstrap Provider版本
    │     └─> 同上
    │
    └─> ControlPlane Provider版本
          └─> 同上
```
#### 3. 升级策略
**场景1: 同Contract升级**(如v1.11→v1.13)
```
全局Version: v1beta2 (不变)
操作: 直接升级,无需考虑版本配套
```
**场景2: 跨Contract升级**(如v1.10→v1.11)
```
全局Version: v1beta1 → v1beta2
前提条件:
  1. 所有CRD已升级到支持v1beta2
  2. 所有provider已升级到实现v1beta2 contract
  3. v1beta1对象已通过conversion webhook转换
```
#### 4. 版本检查机制
建议实现类似cluster-api的版本检查:
```go
// 获取当前全局version
globalVersion := contract.Version // v1beta2

// 获取兼容的contracts
compatibleVersions := contract.GetCompatibleVersions(globalVersion)

// 检查provider CRD是否支持
crdContract, err := contract.GetContractVersion(ctx, client, gk)
if !compatibleVersions.Has(crdContract) {
    return errors.New("provider contract不兼容")
}
```
### 六、关键文件参考
1. **Contract定义**: [metadata.yaml](file:///d:/code/github/cluster-api/metadata.yaml)
2. **Version管理**: [internal/contract/version.go](file:///d:/code/github/cluster-api/internal/contract/version.go)
3. **CRD Labels**: [bootstrap/kubeadm/config/crd/kustomization.yaml](file:///d:/code/github/cluster-api/bootstrap/kubeadm/config/crd/kustomization.yaml)
4. **版本转换**: [api/core/v1beta1/conversion.go](file:///d:/code/github/cluster-api/api/core/v1beta1/conversion.go)
5. **版本文档**: [docs/book/src/reference/versions.md](file:///d:/code/github/cluster-api/docs/book/src/reference/versions.md)

### 七、总结
**全局Version = Contract Version**是最佳实践,因为:
1. **单一职责**: Contract version已经承担了全局版本协调的角色
2. **生态一致**: 所有provider都遵循contract version规范
3. **升级清晰**: 通过metadata.yaml明确映射release→contract
4. **兼容可控**: 通过GetCompatibleVersions明确兼容范围
5. **演进路径**: 有明确的v1beta1→v1beta2迁移计划

建议直接采用contract version作为你的全局version,并复用cluster-api现有的版本管理机制。
        

# 设计一个声明式的升级管理系统
## 阶段四：升级管理 — ClusterVersion CRD + CVO 控制器详细设计
### 一、当前升级架构问题分析
#### 1.1 升级逻辑分散
**当前升级 Phase 分布：**
```
PostDeployPhases:
├── EnsureProviderSelfUpgrade    # Provider 自升级
├── EnsureAgentUpgrade           # BKE Agent 升级
├── EnsureContainerdUpgrade      # Containerd 升级
├── EnsureEtcdUpgrade            # Etcd 升级
├── EnsureWorkerUpgrade          # Worker 节点升级
├── EnsureMasterUpgrade          # Master 节点升级
└── EnsureComponentUpgrade       # 核心组件升级
```
**问题：**
1. **缺少统一编排**：各 Phase 独立执行，无法统一管理升级流程
2. **版本状态分散**：版本信息分散在 BKECluster.Status 的多个字段中
3. **缺少回滚能力**：升级失败后无法自动回滚
4. **版本兼容性检查不完善**：缺少系统性的版本兼容性矩阵
5. **升级历史缺失**：无法追溯历史升级记录
#### 1.2 版本管理混乱
**当前版本字段分布：**
```go
type BKEClusterStatus struct {
    OpenFuyaoVersion    string  // openFuyao 版本
    KubernetesVersion   string  // K8s 版本
    EtcdVersion         string  // Etcd 版本
    ContainerdVersion   string  // Containerd 版本
    // ... 缺少其他组件版本
}
```
**问题：**
1. 版本信息不完整（缺少 Agent、Addon 等版本）
2. 缺少版本来源追踪
3. 缺少版本升级历史
4. 缺少版本验证机制
### 二、目标架构设计
#### 2.1 ClusterVersion Operator (CVO) 架构
```
┌────────────────────────────────────────────────────────────────┐
│                    Cluster Version Operator                    │
│                                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                  ClusterVersion CRD                     │   │
│  │  spec.desiredVersion: v1.29.0                           │   │
│  │  status.currentVersion: v1.28.0                         │   │
│  │  status.history: [...]                                  │   │
│  └─────────────────────────────────────────────────────────┘   │
│                              │                                 │
│                              ▼                                 │
│  ┌────────────────────────────────────────────────────────┐    │
│  │              ClusterVersion Controller                 │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │    │
│  │  │ Version      │  │ Upgrade      │  │ Rollback     │  │    │
│  │  │ Validator    │  │ Orchestrator │  │ Manager      │  │    │
│  │  └──────────────┘  └──────────────┘  └──────────────┘  │    │
│  └────────────────────────────────────────────────────────┘    │
│                              │                                 │
│                              ▼                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                  Upgrade Executors                      │   │
│  │  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐        │   │
│  │  │K8s      │ │Etcd     │ │Container│ │Addon    │        │   │
│  │  │Upgrader │ │Upgrader │ │Upgrader │ │Upgrader │        │   │
│  │  └─────────┘ └─────────┘ └─────────┘ └─────────┘        │   │
│  └─────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────┘
```
#### 2.2 核心组件
1. **ClusterVersion CRD**：声明式版本管理
2. **ClusterVersion Controller**：升级编排控制器
3. **Upgrade Executor**：具体升级执行器
4. **Version Validator**：版本兼容性验证器
5. **Rollback Manager**：回滚管理器
### 三、详细 CRD 设计
#### 3.1 ClusterVersion CRD
```go
// api/cvo/v1beta1/clusterversion_types.go

package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
    ClusterVersionFinalizer = "clusterversion.cvo.cluster.x-k8s.io"
)

type ClusterVersionPhase string

const (
    ClusterVersionPhaseAvailable    ClusterVersionPhase = "Available"
    ClusterVersionPhaseProgressing  ClusterVersionPhase = "Progressing"
    ClusterVersionPhaseDegraded     ClusterVersionPhase = "Degraded"
    ClusterVersionPhaseRollingBack  ClusterVersionPhase = "RollingBack"
)

type ClusterVersionSpec struct {
    // 期望版本
    DesiredVersion string `json:"desiredVersion"`
    
    // 期望的组件版本
    DesiredComponentVersions ComponentVersions `json:"desiredComponentVersions,omitempty"`
    
    // 升级通道
    Channel string `json:"channel,omitempty"`
    
    // 升级策略
    UpgradeStrategy UpgradeStrategy `json:"upgradeStrategy,omitempty"`
    
    // 是否允许自动升级
    AutomaticUpdate bool `json:"automaticUpdate,omitempty"`
    
    // 是否允许降级
    AllowDowngrade bool `json:"allowDowngrade,omitempty"`
    
    // 暂停升级
    Pause bool `json:"pause,omitempty"`
}

type ComponentVersions struct {
    // Kubernetes 版本
    Kubernetes string `json:"kubernetes,omitempty"`
    
    // Etcd 版本
    Etcd string `json:"etcd,omitempty"`
    
    // Containerd 版本
    Containerd string `json:"containerd,omitempty"`
    
    // openFuyao 版本
    OpenFuyao string `json:"openFuyao,omitempty"`
    
    // BKE Agent 版本
    BKEAgent string `json:"bkeAgent,omitempty"`
    
    // 其他组件版本
    Extra map[string]string `json:"extra,omitempty"`
}

type UpgradeStrategy struct {
    // 升级类型：RollingUpdate, Recreate
    Type UpgradeType `json:"type,omitempty"`
    
    // 滚动升级配置
    RollingUpdate *RollingUpdateConfig `json:"rollingUpdate,omitempty"`
    
    // 最大并行升级节点数
    MaxParallelNodes int `json:"maxParallelNodes,omitempty"`
    
    // 升级前健康检查
    PreUpgradeHealthCheck *HealthCheckConfig `json:"preUpgradeHealthCheck,omitempty"`
    
    // 升级后健康检查
    PostUpgradeHealthCheck *HealthCheckConfig `json:"postUpgradeHealthCheck,omitempty"`
    
    // 备份配置
    Backup *BackupConfig `json:"backup,omitempty"`
}

type UpgradeType string

const (
    RollingUpdateUpgrade UpgradeType = "RollingUpdate"
    RecreateUpgrade      UpgradeType = "Recreate"
)

type RollingUpdateConfig struct {
    // 最大不可用节点数
    MaxUnavailable *int `json:"maxUnavailable,omitempty"`
    
    // 最大激增节点数
    MaxSurge *int `json:"maxSurge,omitempty"`
    
    // 节点间等待时间
    IntervalSeconds int `json:"intervalSeconds,omitempty"`
}

type HealthCheckConfig struct {
    // 是否启用
    Enabled bool `json:"enabled,omitempty"`
    
    // 超时时间（秒）
    TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
    
    // 检查项
    Checks []HealthCheck `json:"checks,omitempty"`
}

type HealthCheck struct {
    // 检查类型：Pod, Node, Component
    Type HealthCheckType `json:"type"`
    
    // 检查名称
    Name string `json:"name"`
    
    // 期望状态
    ExpectedStatus string `json:"expectedStatus,omitempty"`
    
    // 超时时间
    TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

type HealthCheckType string

const (
    HealthCheckTypePod      HealthCheckType = "Pod"
    HealthCheckTypeNode     HealthCheckType = "Node"
    HealthCheckTypeComponent HealthCheckType = "Component"
)

type BackupConfig struct {
    // 是否启用备份
    Enabled bool `json:"enabled,omitempty"`
    
    // 备份位置
    Location string `json:"location,omitempty"`
    
    // 备份保留时间（天）
    RetentionDays int `json:"retentionDays,omitempty"`
}

type ClusterVersionStatus struct {
    // 当前版本
    CurrentVersion string `json:"currentVersion,omitempty"`
    
    // 当前组件版本
    CurrentComponentVersions ComponentVersions `json:"currentComponentVersions,omitempty"`
    
    // 阶段
    Phase ClusterVersionPhase `json:"phase,omitempty"`
    
    // 条件
    Conditions []ClusterVersionCondition `json:"conditions,omitempty"`
    
    // 升级历史
    History []UpgradeHistory `json:"history,omitempty"`
    
    // 正在进行的升级
    CurrentUpgrade *CurrentUpgrade `json:"currentUpgrade,omitempty"`
    
    // 可用升级
    AvailableUpdates []AvailableUpdate `json:"availableUpdates,omitempty"`
    
    // 最后更新时间
    LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`
}

type ClusterVersionCondition struct {
    Type               ClusterVersionConditionType `json:"type"`
    Status             ConditionStatus             `json:"status"`
    LastTransitionTime *metav1.Time                `json:"lastTransitionTime,omitempty"`
    Reason             string                      `json:"reason,omitempty"`
    Message            string                      `json:"message,omitempty"`
}

type ClusterVersionConditionType string

const (
    ClusterVersionConditionProgressing    ClusterVersionConditionType = "Progressing"
    ClusterVersionConditionAvailable      ClusterVersionConditionType = "Available"
    ClusterVersionConditionDegraded       ClusterVersionConditionType = "Degraded"
    ClusterVersionConditionUpgradeable    ClusterVersionConditionType = "Upgradeable"
    ClusterVersionConditionRollback       ClusterVersionConditionType = "Rollback"
    ClusterVersionConditionValidationDone ClusterVersionConditionType = "ValidationDone"
)

type UpgradeHistory struct {
    // 版本
    Version string `json:"version"`
    
    // 组件版本
    ComponentVersions ComponentVersions `json:"componentVersions,omitempty"`
    
    // 状态：Completed, Partial, Failed, RolledBack
    State UpgradeState `json:"state"`
    
    // 开始时间
    StartTime *metav1.Time `json:"startTime,omitempty"`
    
    // 完成时间
    CompletionTime *metav1.Time `json:"completionTime,omitempty"`
    
    // 失败原因
    FailureReason string `json:"failureReason,omitempty"`
    
    // 失败消息
    FailureMessage string `json:"failureMessage,omitempty"`
    
    // 升级日志引用
    LogRef *LogReference `json:"logRef,omitempty"`
}

type UpgradeState string

const (
    UpgradeStateCompleted  UpgradeState = "Completed"
    UpgradeStatePartial    UpgradeState = "Partial"
    UpgradeStateFailed     UpgradeState = "Failed"
    UpgradeStateRolledBack UpgradeState = "RolledBack"
)

type CurrentUpgrade struct {
    // 目标版本
    TargetVersion string `json:"targetVersion,omitempty"`
    
    // 目标组件版本
    TargetComponentVersions ComponentVersions `json:"targetComponentVersions,omitempty"`
    
    // 开始时间
    StartTime *metav1.Time `json:"startTime,omitempty"`
    
    // 当前步骤
    CurrentStep UpgradeStep `json:"currentStep,omitempty"`
    
    // 已完成步骤
    CompletedSteps []UpgradeStep `json:"completedSteps,omitempty"`
    
    // 进度百分比
    Progress int `json:"progress,omitempty"`
    
    // 失败信息
    Failure *UpgradeFailure `json:"failure,omitempty"`
}

type UpgradeStep struct {
    // 步骤名称
    Name string `json:"name"`
    
    // 步骤状态
    State StepState `json:"state"`
    
    // 开始时间
    StartTime *metav1.Time `json:"startTime,omitempty"`
    
    // 完成时间
    CompletionTime *metav1.Time `json:"completionTime,omitempty"`
    
    // 消息
    Message string `json:"message,omitempty"`
}

type StepState string

const (
    StepStatePending    StepState = "Pending"
    StepStateRunning    StepState = "Running"
    StepStateCompleted  StepState = "Completed"
    StepStateFailed     StepState = "Failed"
    StepStateSkipped    StepState = "Skipped"
)

type UpgradeFailure struct {
    // 失败步骤
    Step string `json:"step,omitempty"`
    
    // 失败原因
    Reason string `json:"reason,omitempty"`
    
    // 失败消息
    Message string `json:"message,omitempty"`
    
    // 失败时间
    Time *metav1.Time `json:"time,omitempty"`
    
    // 是否可重试
    Retryable bool `json:"retryable,omitempty"`
}

type AvailableUpdate struct {
    // 版本
    Version string `json:"version"`
    
    // 组件版本
    ComponentVersions ComponentVersions `json:"componentVersions,omitempty"`
    
    // 是否为推荐版本
    Recommended bool `json:"recommended,omitempty"`
    
    // 升级风险：Low, Medium, High
    Risk UpdateRisk `json:"risk,omitempty"`
    
    // 变更说明
    ReleaseNotes string `json:"releaseNotes,omitempty"`
}

type UpdateRisk string

const (
    UpdateRiskLow    UpdateRisk = "Low"
    UpdateRiskMedium UpdateRisk = "Medium"
    UpdateRiskHigh   UpdateRisk = "High"
)

type LogReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="VERSION",type="string",JSONPath=".status.currentVersion"
// +kubebuilder:printcolumn:name="DESIRED",type="string",JSONPath=".spec.desiredVersion"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="PROGRESS",type="string",JSONPath=".status.currentUpgrade.progress"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
type ClusterVersion struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   ClusterVersionSpec   `json:"spec,omitempty"`
    Status ClusterVersionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterVersionList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []ClusterVersion `json:"items"`
}
```
#### 3.2 UpgradeConfig CRD
```go
// api/cvo/v1beta1/upgradeconfig_types.go

package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type UpgradeConfigSpec struct {
    // 升级包配置
    UpgradePackage UpgradePackageConfig `json:"upgradePackage"`
    
    // 版本兼容性矩阵
    CompatibilityMatrix []CompatibilityRule `json:"compatibilityMatrix,omitempty"`
    
    // 升级路径
    UpgradePaths []UpgradePath `json:"upgradePaths,omitempty"`
    
    // 镜像仓库配置
    ImageRegistry ImageRegistryConfig `json:"imageRegistry,omitempty"`
    
    // 软件源配置
    PackageRepo PackageRepoConfig `json:"packageRepo,omitempty"`
}

type UpgradePackageConfig struct {
    // 升级包仓库地址
    Repository string `json:"repository,omitempty"`
    
    // 升级包名称前缀
    NamePrefix string `json:"namePrefix,omitempty"`
    
    // 认证信息
    AuthSecretRef *AuthSecretRef `json:"authSecretRef,omitempty"`
    
    // TLS 配置
    TlsSecretRef *TlsSecretRef `json:"tlsSecretRef,omitempty"`
}

type CompatibilityRule struct {
    // 组件名称
    Component string `json:"component"`
    
    // 最小版本
    MinVersion string `json:"minVersion,omitempty"`
    
    // 最大版本
    MaxVersion string `json:"maxVersion,omitempty"`
    
    // 兼容的 Kubernetes 版本范围
    KubernetesVersions []string `json:"kubernetesVersions,omitempty"`
    
    // 兼容的操作系统
    OperatingSystems []string `json:"operatingSystems,omitempty"`
    
    // 兼容的架构
    Architectures []string `json:"architectures,omitempty"`
}

type UpgradePath struct {
    // 起始版本
    FromVersion string `json:"fromVersion"`
    
    // 目标版本
    ToVersion string `json:"toVersion"`
    
    // 是否允许直接升级
    Direct bool `json:"direct,omitempty"`
    
    // 中间版本（如果需要）
    IntermediateVersions []string `json:"intermediateVersions,omitempty"`
    
    // 升级顺序
    Order []string `json:"order,omitempty"`
}

type ImageRegistryConfig struct {
    // 镜像仓库地址
    Registry string `json:"registry,omitempty"`
    
    // 镜像仓库前缀
    Prefix string `json:"prefix,omitempty"`
    
    // 认证信息
    AuthSecretRef *AuthSecretRef `json:"authSecretRef,omitempty"`
    
    // 是否跳过 TLS 验证
    InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`
}

type PackageRepoConfig struct {
    // HTTP 软件源
    HTTPRepo string `json:"httpRepo,omitempty"`
    
    // 认证信息
    AuthSecretRef *AuthSecretRef `json:"authSecretRef,omitempty"`
}

type UpgradeConfigStatus struct {
    // 最后同步时间
    LastSynced *metav1.Time `json:"lastSynced,omitempty"`
    
    // 可用版本
    AvailableVersions []string `json:"availableVersions,omitempty"`
    
    // 条件
    Conditions []UpgradeConfigCondition `json:"conditions,omitempty"`
}

type UpgradeConfigCondition struct {
    Type               string       `json:"type"`
    Status             ConditionStatus `json:"status"`
    LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
    Reason             string       `json:"reason,omitempty"`
    Message            string       `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type UpgradeConfig struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   UpgradeConfigSpec   `json:"spec,omitempty"`
    Status UpgradeConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type UpgradeConfigList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []UpgradeConfig `json:"items"`
}
```
### 四、控制器设计
#### 4.1 ClusterVersion Controller
```go
// controllers/cvo/clusterversion_controller.go

package cvo

import (
    "context"
    
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cvo/orchestrator"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cvo/validator"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cvo/rollback"
)

type ClusterVersionReconciler struct {
    client.Client
    
    // 版本验证器
    Validator validator.VersionValidator
    
    // 升级编排器
    Orchestrator orchestrator.UpgradeOrchestrator
    
    // 回滚管理器
    RollbackManager rollback.RollbackManager
}

func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    clusterVersion := &cvov1beta1.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, clusterVersion); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 状态机模式处理
    switch clusterVersion.Status.Phase {
    case "", cvov1beta1.ClusterVersionPhaseAvailable:
        return r.handleAvailable(ctx, clusterVersion)
    case cvov1beta1.ClusterVersionPhaseProgressing:
        return r.handleProgressing(ctx, clusterVersion)
    case cvov1beta1.ClusterVersionPhaseDegraded:
        return r.handleDegraded(ctx, clusterVersion)
    case cvov1beta1.ClusterVersionPhaseRollingBack:
        return r.handleRollingBack(ctx, clusterVersion)
    }
    
    return ctrl.Result{}, nil
}

func (r *ClusterVersionReconciler) handleAvailable(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
    // 检查是否需要升级
    if cv.Spec.DesiredVersion == "" || cv.Spec.DesiredVersion == cv.Status.CurrentVersion {
        // 检查可用更新
        return r.checkAvailableUpdates(ctx, cv)
    }
    
    // 验证版本兼容性
    if err := r.Validator.Validate(ctx, cv); err != nil {
        r.markDegraded(cv, "ValidationFailed", err.Error())
        return ctrl.Result{}, r.Status().Update(ctx, cv)
    }
    
    // 开始升级
    return r.startUpgrade(ctx, cv)
}

func (r *ClusterVersionReconciler) handleProgressing(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
    // 执行升级编排
    result, err := r.Orchestrator.Orchestrate(ctx, cv)
    if err != nil {
        // 升级失败，触发回滚
        if cv.Spec.UpgradeStrategy.Backup != nil && cv.Spec.UpgradeStrategy.Backup.Enabled {
            return r.startRollback(ctx, cv, err)
        }
        
        r.markDegraded(cv, "UpgradeFailed", err.Error())
        return ctrl.Result{}, r.Status().Update(ctx, cv)
    }
    
    return result, nil
}

func (r *ClusterVersionReconciler) handleDegraded(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
    // 检查是否可以重试
    if cv.Status.CurrentUpgrade != nil && cv.Status.CurrentUpgrade.Failure != nil {
        if cv.Status.CurrentUpgrade.Failure.Retryable {
            // 重试升级
            return r.startUpgrade(ctx, cv)
        }
    }
    
    // 等待用户干预
    return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ClusterVersionReconciler) handleRollingBack(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
    // 执行回滚
    result, err := r.RollbackManager.Rollback(ctx, cv)
    if err != nil {
        r.markDegraded(cv, "RollbackFailed", err.Error())
        return ctrl.Result{}, r.Status().Update(ctx, cv)
    }
    
    return result, nil
}

func (r *ClusterVersionReconciler) startUpgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
    // 初始化升级状态
    cv.Status.Phase = cvov1beta1.ClusterVersionPhaseProgressing
    cv.Status.CurrentUpgrade = &cvov1beta1.CurrentUpgrade{
        TargetVersion: cv.Spec.DesiredVersion,
        TargetComponentVersions: cv.Spec.DesiredComponentVersions,
        StartTime: &metav1.Time{Time: time.Now()},
        CurrentStep: cvov1beta1.UpgradeStep{
            Name: "Initialize",
            State: cvov1beta1.StepStateRunning,
        },
    }
    
    // 创建升级计划
    plan, err := r.Orchestrator.CreatePlan(ctx, cv)
    if err != nil {
        r.markDegraded(cv, "PlanCreationFailed", err.Error())
        return ctrl.Result{}, r.Status().Update(ctx, cv)
    }
    
    // 执行升级前备份
    if cv.Spec.UpgradeStrategy.Backup != nil && cv.Spec.UpgradeStrategy.Backup.Enabled {
        if err := r.Orchestrator.Backup(ctx, cv); err != nil {
            r.markDegraded(cv, "BackupFailed", err.Error())
            return ctrl.Result{}, r.Status().Update(ctx, cv)
        }
    }
    
    return ctrl.Result{Requeue: true}, r.Status().Update(ctx, cv)
}

func (r *ClusterVersionReconciler) startRollback(ctx context.Context, cv *cvov1beta1.ClusterVersion, upgradeErr error) (ctrl.Result, error) {
    cv.Status.Phase = cvov1beta1.ClusterVersionPhaseRollingBack
    cv.Status.CurrentUpgrade.Failure = &cvov1beta1.UpgradeFailure{
        Reason:  "UpgradeFailed",
        Message: upgradeErr.Error(),
        Time:    &metav1.Time{Time: time.Now()},
    }
    
    return ctrl.Result{Requeue: true}, r.Status().Update(ctx, cv)
}

func (r *ClusterVersionReconciler) markDegraded(cv *cvov1beta1.ClusterVersion, reason, message string) {
    cv.Status.Phase = cvov1beta1.ClusterVersionPhaseDegraded
    condition := cvov1beta1.ClusterVersionCondition{
        Type:               cvov1beta1.ClusterVersionConditionDegraded,
        Status:             "True",
        LastTransitionTime: &metav1.Time{Time: time.Now()},
        Reason:             reason,
        Message:            message,
    }
    cv.Status.Conditions = append(cv.Status.Conditions, condition)
}
```
#### 4.2 Upgrade Orchestrator
```go
// pkg/cvo/orchestrator/orchestrator.go

package orchestrator

import (
    "context"
    
    cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cvo/executor"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/cvo/plan"
)

type UpgradeOrchestrator interface {
    CreatePlan(ctx context.Context, cv *cvov1beta1.ClusterVersion) (*plan.UpgradePlan, error)
    Orchestrate(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error)
    Backup(ctx context.Context, cv *cvov1beta1.ClusterVersion) error
}

type BKEUpgradeOrchestrator struct {
    // 升级执行器
    Executors map[string]executor.UpgradeExecutor
    
    // 升级计划生成器
    PlanGenerator plan.PlanGenerator
}

func (o *BKEUpgradeOrchestrator) CreatePlan(ctx context.Context, cv *cvov1beta1.ClusterVersion) (*plan.UpgradePlan, error) {
    // 生成升级计划
    upgradePlan, err := o.PlanGenerator.Generate(ctx, cv)
    if err != nil {
        return nil, err
    }
    
    return upgradePlan, nil
}

func (o *BKEUpgradeOrchestrator) Orchestrate(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
    // 获取当前升级步骤
    currentStep := cv.Status.CurrentUpgrade.CurrentStep
    
    // 执行当前步骤
    executor, ok := o.Executors[currentStep.Name]
    if !ok {
        return ctrl.Result{}, fmt.Errorf("no executor for step: %s", currentStep.Name)
    }
    
    result, err := executor.Execute(ctx, cv)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 更新步骤状态
    if result.Complete {
        cv.Status.CurrentUpgrade.CompletedSteps = append(
            cv.Status.CurrentUpgrade.CompletedSteps,
            currentStep,
        )
        
        // 移动到下一步
        if nextStep := o.getNextStep(cv, currentStep); nextStep != nil {
            cv.Status.CurrentUpgrade.CurrentStep = *nextStep
        } else {
            // 所有步骤完成
            return o.completeUpgrade(ctx, cv)
        }
    }
    
    return result.Result, nil
}

func (o *BKEUpgradeOrchestrator) getNextStep(cv *cvov1beta1.ClusterVersion, currentStep cvov1beta1.UpgradeStep) *cvov1beta1.UpgradeStep {
    // 定义升级步骤顺序
    steps := []string{
        "PreCheck",
        "Backup",
        "ProviderSelfUpgrade",
        "AgentUpgrade",
        "ContainerdUpgrade",
        "EtcdUpgrade",
        "MasterUpgrade",
        "WorkerUpgrade",
        "ComponentUpgrade",
        "PostCheck",
        "Cleanup",
    }
    
    // 找到当前步骤的索引
    currentIndex := -1
    for i, name := range steps {
        if name == currentStep.Name {
            currentIndex = i
            break
        }
    }
    
    // 返回下一步
    if currentIndex >= 0 && currentIndex < len(steps)-1 {
        return &cvov1beta1.UpgradeStep{
            Name:  steps[currentIndex+1],
            State: cvov1beta1.StepStatePending,
        }
    }
    
    return nil
}

func (o *BKEUpgradeOrchestrator) completeUpgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
    // 更新版本状态
    cv.Status.CurrentVersion = cv.Spec.DesiredVersion
    cv.Status.CurrentComponentVersions = cv.Spec.DesiredComponentVersions
    cv.Status.Phase = cvov1beta1.ClusterVersionPhaseAvailable
    
    // 添加到历史记录
    history := cvov1beta1.UpgradeHistory{
        Version:            cv.Spec.DesiredVersion,
        ComponentVersions:  cv.Spec.DesiredComponentVersions,
        State:              cvov1beta1.UpgradeStateCompleted,
        StartTime:          cv.Status.CurrentUpgrade.StartTime,
        CompletionTime:     &metav1.Time{Time: time.Now()},
    }
    cv.Status.History = append(cv.Status.History, history)
    
    // 清空当前升级
    cv.Status.CurrentUpgrade = nil
    
    return ctrl.Result{}, nil
}
```
#### 4.3 Upgrade Executor（迁移现有 Phase）
```go
// pkg/cvo/executor/executor.go

package executor

import (
    "context"
    
    ctrl "sigs.k8s.io/controller-runtime"
    cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
)

type ExecutorResult struct {
    Complete bool
    Result   ctrl.Result
    Error    error
}

type UpgradeExecutor interface {
    Name() string
    Execute(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ExecutorResult, error)
    Rollback(ctx context.Context, cv *cvov1beta1.ClusterVersion) error
}

// MasterUpgradeExecutor 迁移自 ensure_master_upgrade.go
type MasterUpgradeExecutor struct {
    // 迁移自 EnsureMasterUpgrade 的逻辑
}

func (e *MasterUpgradeExecutor) Execute(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ExecutorResult, error) {
    // 迁移自 ensure_master_upgrade.go 的 Execute 逻辑
    // 1. 获取需要升级的 Master 节点
    // 2. 执行滚动升级
    // 3. 等待升级完成
    // 4. 更新状态
    
    return ExecutorResult{Complete: true, Result: ctrl.Result{}}, nil
}

// WorkerUpgradeExecutor 迁移自 ensure_worker_upgrade.go
type WorkerUpgradeExecutor struct {
    // 迁移自 EnsureWorkerUpgrade 的逻辑
}

func (e *WorkerUpgradeExecutor) Execute(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ExecutorResult, error) {
    // 迁移自 ensure_worker_upgrade.go 的 Execute 逻辑
    
    return ExecutorResult{Complete: true, Result: ctrl.Result{}}, nil
}

// EtcdUpgradeExecutor 迁移自 ensure_etcd_upgrade.go
type EtcdUpgradeExecutor struct {
    // 迁移自 EnsureEtcdUpgrade 的逻辑
}

func (e *EtcdUpgradeExecutor) Execute(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ExecutorResult, error) {
    // 迁移自 ensure_etcd_upgrade.go 的 Execute 逻辑
    
    return ExecutorResult{Complete: true, Result: ctrl.Result{}}, nil
}
```
### 五、版本验证器设计
```go
// pkg/cvo/validator/validator.go

package validator

import (
    "context"
    
    cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
)

type VersionValidator interface {
    Validate(ctx context.Context, cv *cvov1beta1.ClusterVersion) error
}

type BKEVersionValidator struct {
    // 兼容性矩阵
    CompatibilityMatrix []cvov1beta1.CompatibilityRule
    
    // 升级路径
    UpgradePaths []cvov1beta1.UpgradePath
}

func (v *BKEVersionValidator) Validate(ctx context.Context, cv *cvov1beta1.ClusterVersion) error {
    // 1. 验证版本格式
    if err := v.validateVersionFormat(cv); err != nil {
        return err
    }
    
    // 2. 验证版本兼容性
    if err := v.validateCompatibility(cv); err != nil {
        return err
    }
    
    // 3. 验证升级路径
    if err := v.validateUpgradePath(cv); err != nil {
        return err
    }
    
    // 4. 验证前置条件
    if err := v.validatePreconditions(cv); err != nil {
        return err
    }
    
    return nil
}

func (v *BKEVersionValidator) validateCompatibility(cv *cvov1beta1.ClusterVersion) error {
    // 检查 Kubernetes 版本兼容性
    k8sVersion := cv.Spec.DesiredComponentVersions.Kubernetes
    for _, rule := range v.CompatibilityMatrix {
        if rule.Component == "kubernetes" {
            if !v.isVersionInRange(k8sVersion, rule.MinVersion, rule.MaxVersion) {
                return fmt.Errorf("kubernetes version %s is not in compatible range [%s, %s]",
                    k8sVersion, rule.MinVersion, rule.MaxVersion)
            }
        }
    }
    
    // 检查 Etcd 版本兼容性
    etcdVersion := cv.Spec.DesiredComponentVersions.Etcd
    if err := v.validateEtcdCompatibility(k8sVersion, etcdVersion); err != nil {
        return err
    }
    
    // 检查 Containerd 版本兼容性
    containerdVersion := cv.Spec.DesiredComponentVersions.Containerd
    if err := v.validateContainerdCompatibility(k8sVersion, containerdVersion); err != nil {
        return err
    }
    
    return nil
}

func (v *BKEVersionValidator) validateUpgradePath(cv *cvov1beta1.ClusterVersion) error {
    fromVersion := cv.Status.CurrentVersion
    toVersion := cv.Spec.DesiredVersion
    
    // 检查是否允许直接升级
    for _, path := range v.UpgradePaths {
        if path.FromVersion == fromVersion && path.ToVersion == toVersion {
            if path.Direct {
                return nil
            }
            
            // 需要中间版本
            if len(path.IntermediateVersions) > 0 {
                return fmt.Errorf("upgrade from %s to %s requires intermediate versions: %v",
                    fromVersion, toVersion, path.IntermediateVersions)
            }
        }
    }
    
    // 检查是否允许降级
    if !cv.Spec.AllowDowngrade && v.isDowngrade(fromVersion, toVersion) {
        return fmt.Errorf("downgrade from %s to %s is not allowed", fromVersion, toVersion)
    }
    
    return nil
}
```
### 六、回滚管理器设计
```go
// pkg/cvo/rollback/rollback.go

package rollback

import (
    "context"
    
    cvov1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/cvo/v1beta1"
)

type RollbackManager interface {
    Rollback(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error)
}

type BKERollbackManager struct {
    // 备份存储
    BackupStorage BackupStorage
    
    // 回滚执行器
    RollbackExecutors []RollbackExecutor
}

func (m *BKERollbackManager) Rollback(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error) {
    // 1. 恢复备份
    backup, err := m.BackupStorage.GetLatestBackup(ctx, cv)
    if err != nil {
        return ctrl.Result{}, fmt.Errorf("failed to get backup: %w", err)
    }
    
    // 2. 按逆序执行回滚
    completedSteps := cv.Status.CurrentUpgrade.CompletedSteps
    for i := len(completedSteps) - 1; i >= 0; i-- {
        step := completedSteps[i]
        
        // 查找对应的回滚执行器
        executor := m.getRollbackExecutor(step.Name)
        if executor == nil {
            continue
        }
        
        // 执行回滚
        if err := executor.Rollback(ctx, cv, backup); err != nil {
            return ctrl.Result{}, fmt.Errorf("failed to rollback step %s: %w", step.Name, err)
        }
    }
    
    // 3. 恢复到之前版本
    cv.Status.CurrentVersion = cv.Status.History[len(cv.Status.History)-1].Version
    cv.Status.Phase = cvov1beta1.ClusterVersionPhaseAvailable
    
    // 4. 添加回滚记录到历史
    history := cvov1beta1.UpgradeHistory{
        Version:        cv.Status.CurrentVersion,
        State:          cvov1beta1.UpgradeStateRolledBack,
        CompletionTime: &metav1.Time{Time: time.Now()},
        FailureReason:  cv.Status.CurrentUpgrade.Failure.Reason,
        FailureMessage: cv.Status.CurrentUpgrade.Failure.Message,
    }
    cv.Status.History = append(cv.Status.History, history)
    
    return ctrl.Result{}, nil
}
```
### 七、迁移策略
#### 7.1 迁移步骤
**阶段 4.1：创建 CVO 基础设施**
1. 创建 `ClusterVersion` 和 `UpgradeConfig` CRD
2. 实现 `ClusterVersion Controller`
3. 实现 `VersionValidator`
4. 添加 Feature Gate

**阶段 4.2：迁移升级逻辑**
1. 将现有升级 Phase 转换为 `UpgradeExecutor`
2. 实现 `UpgradeOrchestrator`
3. 实现 `RollbackManager`
4. 实现备份和恢复机制

**阶段 4.3：集成和测试**
1. 集成到现有 BKECluster 控制器
2. 实现数据迁移
3. 编写集成测试
4. 性能测试和优化
#### 7.2 兼容性保障
**Feature Gate：**
```go
const (
    // ClusterVersionOperator 启用 CVO
    ClusterVersionOperator featuregate.Feature = "ClusterVersionOperator"
)
```
**转换 Webhook：**
```go
// 自动创建 ClusterVersion CR
func (r *BKECluster) Default() {
    if features.DefaultFeatureGate.Enabled(features.ClusterVersionOperator) {
        r.createClusterVersion()
    }
}

func (r *BKECluster) createClusterVersion() {
    // 从 BKECluster.Status 中提取版本信息
    // 创建 ClusterVersion CR
}
```
### 八、测试策略
#### 8.1 单元测试
```go
func TestClusterVersionController_Upgrade(t *testing.T) {
    tests := []struct {
        name           string
        clusterVersion *cvov1beta1.ClusterVersion
        wantPhase      cvov1beta1.ClusterVersionPhase
        wantVersion    string
    }{
        {
            name: "successful upgrade",
            clusterVersion: &cvov1beta1.ClusterVersion{
                Spec: cvov1beta1.ClusterVersionSpec{
                    DesiredVersion: "v1.29.0",
                },
                Status: cvov1beta1.ClusterVersionStatus{
                    CurrentVersion: "v1.28.0",
                    Phase:          cvov1beta1.ClusterVersionPhaseAvailable,
                },
            },
            wantPhase:   cvov1beta1.ClusterVersionPhaseProgressing,
            wantVersion: "v1.29.0",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 测试逻辑
        })
    }
}
```
#### 8.2 集成测试
```go
func TestClusterVersionIntegration(t *testing.T) {
    // 1. 创建 BKECluster
    // 2. 启用 CVO Feature Gate
    // 3. 验证自动创建 ClusterVersion
    // 4. 触发升级
    // 5. 验证升级流程
    // 6. 验证回滚能力
}
```
### 九、实施计划
| 阶段 | 任务 | 周期 | 依赖 |
|------|------|------|------|
| 4.1 | 创建 ClusterVersion 和 UpgradeConfig CRD | 1 周 | 阶段三完成 |
| 4.2 | 实现 ClusterVersion Controller | 2 周 | 4.1 |
| 4.3 | 实现 VersionValidator | 1 周 | 4.1 |
| 4.4 | 迁移升级 Phase 到 Executor | 2 周 | 4.2 |
| 4.5 | 实现 UpgradeOrchestrator | 2 周 | 4.4 |
| 4.6 | 实现 RollbackManager | 1 周 | 4.5 |
| 4.7 | 实现备份恢复机制 | 1 周 | 4.5 |
| 4.8 | 集成测试和性能优化 | 2 周 | 4.6, 4.7 |

**总周期：12 周**
### 十、风险评估与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 升级失败导致集群不可用 | 高 | 实现自动回滚，充分测试 |
| 版本兼容性问题 | 中 | 完善兼容性矩阵，版本验证 |
| 备份恢复失败 | 高 | 多重备份机制，备份验证 |
| 性能问题 | 中 | 性能基准测试，优化关键路径 |
| 数据迁移失败 | 高 | 保留旧逻辑，实现回滚 |
### 十一、总结
阶段四通过引入 ClusterVersion CRD 和 CVO 控制器，实现了声明式的升级管理：

**收益：**
1. **声明式升级**：通过 CRD 声明期望版本，自动执行升级
2. **统一管理**：所有升级逻辑统一由 CVO 管理
3. **自动回滚**：升级失败时自动回滚到之前版本
4. **版本追踪**：完整的升级历史记录
5. **兼容性验证**：升级前自动验证版本兼容性

**关键设计：**
1. `ClusterVersion` CRD 定义期望版本和升级策略
2. `UpgradeConfig` CRD 定义升级配置和兼容性矩阵
3. `ClusterVersion Controller` 实现升级编排
4. `UpgradeExecutor` 迁移现有升级 Phase
5. `RollbackManager` 实现自动回滚

这个设计为后续支持更复杂的升级场景（如跨版本升级、金丝雀升级）奠定了基础，同时提供了完善的回滚机制保障升级安全。
        

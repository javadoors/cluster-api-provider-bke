# 整理 Phase 4 的完整设计方案
## 阶段四：CVO 升级机制 + ClusterVersion CRD — 独立实现方案设计
### 一、当前升级架构分析
#### 1.1 现有升级 Phase 分布
当前升级逻辑分布在 `PostDeployPhases` 中，按固定顺序执行：
```
PostDeployPhases:
├── EnsureProviderSelfUpgrade    # Provider 自升级（Patch Deployment 镜像）
├── EnsureAgentUpgrade           # BKEAgent DaemonSet 升级
├── EnsureContainerdUpgrade      # Containerd 重置+重部署
├── EnsureEtcdUpgrade            # Etcd 滚动升级
├── EnsureWorkerUpgrade          # Worker 节点滚动升级
├── EnsureMasterUpgrade          # Master 节点滚动升级
├── EnsureWorkerDelete           # Worker 删除
├── EnsureMasterDelete           # Master 删除
├── EnsureComponentUpgrade       # openFuyao 核心组件升级（Patch ConfigMap 中的镜像）
└── EnsureCluster                # 集群健康检查
```
#### 1.2 核心问题
| 问题 | 现状 | 影响 |
|------|------|------|
| **缺少统一编排** | 各 Phase 独立执行，通过 `NeedExecute` 判断是否需要执行 | 无法统一管理升级流程，升级顺序硬编码 |
| **版本状态分散** | `BKEClusterStatus` 中有 `OpenFuyaoVersion`、`KubernetesVersion`、`EtcdVersion`、`ContainerdVersion` | 版本信息不完整，缺少 Agent、Addon 等版本 |
| **缺少回滚能力** | 升级失败后无法自动回滚 | 只能手动修复 |
| **升级触发单一** | 仅通过 `OpenFuyaoVersion` 变化触发 | 无法独立升级单个组件 |
| **升级历史缺失** | 无升级记录 | 无法追溯升级历史 |
| **PatchConfig 依赖** | 组件升级依赖 `openfuyao-patch` ConfigMap 中的 YAML 配置 | 配置分散，难以管理 |
### 二、Phase 4 与其他阶段的依赖分析
| 依赖项 | 是否必须 | 说明 |
|--------|---------|------|
| 阶段一（统一错误处理 + CRD 扩展） | **部分依赖** | 需要 `api/bkecommon/v1beta1/` 中的升级相关共享类型，但可自行定义 |
| 阶段二（状态机引擎） | **不依赖** | Phase 4 的 ClusterVersion Controller 是独立控制器，不依赖状态机 |
| 阶段三（Provider 分离） | **不依赖** | 升级控制器独立于 Bootstrap/ControlPlane Provider |
| 阶段五（OSProvider） | **不依赖** | OS 抽象与升级编排无关 |
| 阶段六（版本包管理 + Asset） | **不依赖** | 版本包管理是更高层的抽象，Phase 4 可独立工作 |
| 阶段七（清理 + 测试） | **不依赖** | 清理是后续工作 |

**结论：Phase 4 可以独立实现，只需最小化依赖现有代码结构。**
### 三、独立实现策略
核心思路：**在现有 Phase Flow 框架之上引入 ClusterVersion CRD 和 CVO Controller，通过适配层与现有升级 Phase 协同工作，而非替换。**
```
┌─────────────────────────────────────────────────────────────────┐
│                    现有架构（保持不变）                         │
│  BKEClusterReconciler → PhaseFlow → PostDeployPhases            │
│                                    ├── EnsureProviderSelfUpgrade│
│                                    ├── EnsureAgentUpgrade       │
│                                    ├── EnsureContainerdUpgrade  │
│                                    ├── EnsureEtcdUpgrade        │
│                                    ├── EnsureWorkerUpgrade      │
│                                    ├── EnsureMasterUpgrade      │
│                                    └── EnsureComponentUpgrade   │
└─────────────────────────────────────────────────────────────────┘
                              ↕ 适配层
┌────────────────────────────────────────────────────────────────┐
│                    新增架构（Phase 4）                         │
│  ClusterVersionReconciler → UpgradeCoordinator                 │
│                           ├── VersionValidator                 │
│                           ├── UpgradeDetector                  │
│                           └── RollbackManager                  │
└────────────────────────────────────────────────────────────────┘
```
### 四、详细 CRD 设计
#### 4.1 ClusterVersion CRD
```go
// api/cvo/v1beta1/clusterversion_types.go
package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
    ClusterVersionFinalizer = "clusterversion.cvo.cluster.x-k8s.io"
    ClusterVersionKind      = "ClusterVersion"
)

// ClusterVersionPhase 定义 ClusterVersion 的当前阶段
type ClusterVersionPhase string

const (
    ClusterVersionPhaseAvailable   ClusterVersionPhase = "Available"
    ClusterVersionPhaseProgressing ClusterVersionPhase = "Progressing"
    ClusterVersionPhaseDegraded    ClusterVersionPhase = "Degraded"
    ClusterVersionPhaseRollingBack ClusterVersionPhase = "RollingBack"
)

// UpgradeStepType 定义升级步骤类型
type UpgradeStepType string

const (
    UpgradeStepPreCheck          UpgradeStepType = "PreCheck"
    UpgradeStepProviderSelf      UpgradeStepType = "ProviderSelfUpgrade"
    UpgradeStepAgent             UpgradeStepType = "AgentUpgrade"
    UpgradeStepContainerd        UpgradeStepType = "ContainerdUpgrade"
    UpgradeStepEtcd              UpgradeStepType = "EtcdUpgrade"
    UpgradeStepControlPlane      UpgradeStepType = "ControlPlaneUpgrade"
    UpgradeStepWorker            UpgradeStepType = "WorkerUpgrade"
    UpgradeStepComponent         UpgradeStepType = "ComponentUpgrade"
    UpgradeStepPostCheck         UpgradeStepType = "PostCheck"
)

// StepState 定义步骤状态
type StepState string

const (
    StepStatePending   StepState = "Pending"
    StepStateRunning   StepState = "Running"
    StepStateCompleted StepState = "Completed"
    StepStateFailed    StepState = "Failed"
    StepStateSkipped   StepState = "Skipped"
)

// UpgradeState 定义升级历史状态
type UpgradeState string

const (
    UpgradeStateCompleted  UpgradeState = "Completed"
    UpgradeStatePartial    UpgradeState = "Partial"
    UpgradeStateFailed     UpgradeState = "Failed"
    UpgradeStateRolledBack UpgradeState = "RolledBack"
)

// ClusterVersionSpec 定义 ClusterVersion 的期望状态
type ClusterVersionSpec struct {
    // DesiredVersion 期望的 openFuyao 整体版本
    DesiredVersion string `json:"desiredVersion"`

    // DesiredComponentVersions 期望的各组件版本
    DesiredComponentVersions ComponentVersions `json:"desiredComponentVersions,omitempty"`

    // ClusterRef 关联的 BKECluster 引用
    ClusterRef *ClusterReference `json:"clusterRef,omitempty"`

    // UpgradeStrategy 升级策略
    UpgradeStrategy UpgradeStrategy `json:"upgradeStrategy,omitempty"`

    // Pause 暂停升级
    Pause bool `json:"pause,omitempty"`

    // AllowDowngrade 是否允许降级
    AllowDowngrade bool `json:"allowDowngrade,omitempty"`
}

// ClusterReference 定义集群引用
type ClusterReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
}

// ComponentVersions 定义组件版本集合
type ComponentVersions struct {
    Kubernetes string `json:"kubernetes,omitempty"`
    Etcd       string `json:"etcd,omitempty"`
    Containerd string `json:"containerd,omitempty"`
    OpenFuyao  string `json:"openFuyao,omitempty"`
    BKEAgent   string `json:"bkeAgent,omitempty"`
    Extra      map[string]string `json:"extra,omitempty"`
}

// UpgradeStrategy 定义升级策略
type UpgradeStrategy struct {
    Type              UpgradeStrategyType `json:"type,omitempty"`
    MaxParallelNodes  int                 `json:"maxParallelNodes,omitempty"`
    BatchInterval     metav1.Duration     `json:"batchInterval,omitempty"`
    RollbackOnFailure bool                `json:"rollbackOnFailure,omitempty"`

    PreUpgradeHealthCheck  *HealthCheckConfig `json:"preUpgradeHealthCheck,omitempty"`
    PostUpgradeHealthCheck *HealthCheckConfig `json:"postUpgradeHealthCheck,omitempty"`
    Backup                 *BackupConfig      `json:"backup,omitempty"`
}

// UpgradeStrategyType 升级策略类型
type UpgradeStrategyType string

const (
    RollingUpdateUpgrade UpgradeStrategyType = "RollingUpdate"
    RecreateUpgrade      UpgradeStrategyType = "Recreate"
)

// HealthCheckConfig 健康检查配置
type HealthCheckConfig struct {
    Enabled       bool          `json:"enabled,omitempty"`
    TimeoutSeconds int          `json:"timeoutSeconds,omitempty"`
    Checks        []HealthCheck `json:"checks,omitempty"`
}

// HealthCheck 健康检查项
type HealthCheck struct {
    Type           HealthCheckType `json:"type"`
    Name           string          `json:"name"`
    ExpectedStatus string          `json:"expectedStatus,omitempty"`
    TimeoutSeconds int             `json:"timeoutSeconds,omitempty"`
}

// HealthCheckType 健康检查类型
type HealthCheckType string

const (
    HealthCheckTypePod       HealthCheckType = "Pod"
    HealthCheckTypeNode      HealthCheckType = "Node"
    HealthCheckTypeComponent HealthCheckType = "Component"
)

// BackupConfig 备份配置
type BackupConfig struct {
    Enabled      bool   `json:"enabled,omitempty"`
    Location     string `json:"location,omitempty"`
    RetentionDays int   `json:"retentionDays,omitempty"`
}

// ClusterVersionStatus 定义 ClusterVersion 的当前状态
type ClusterVersionStatus struct {
    // CurrentVersion 当前 openFuyao 整体版本
    CurrentVersion string `json:"currentVersion,omitempty"`

    // CurrentComponentVersions 当前各组件版本
    CurrentComponentVersions ComponentVersions `json:"currentComponentVersions,omitempty"`

    // Phase 当前阶段
    Phase ClusterVersionPhase `json:"phase,omitempty"`

    // Conditions 条件集合
    Conditions []metav1.Condition `json:"conditions,omitempty"`

    // History 升级历史
    History []UpgradeHistory `json:"history,omitempty"`

    // CurrentUpgrade 正在进行的升级
    CurrentUpgrade *CurrentUpgrade `json:"currentUpgrade,omitempty"`

    // ObservedGeneration 观察到的代际
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// UpgradeHistory 升级历史记录
type UpgradeHistory struct {
    Version            string           `json:"version"`
    ComponentVersions  ComponentVersions `json:"componentVersions,omitempty"`
    State              UpgradeState     `json:"state"`
    StartTime          *metav1.Time     `json:"startTime,omitempty"`
    CompletionTime     *metav1.Time     `json:"completionTime,omitempty"`
    FailureReason      string           `json:"failureReason,omitempty"`
    FailureMessage     string           `json:"failureMessage,omitempty"`
}

// CurrentUpgrade 当前升级状态
type CurrentUpgrade struct {
    TargetVersion            string           `json:"targetVersion,omitempty"`
    TargetComponentVersions  ComponentVersions `json:"targetComponentVersions,omitempty"`
    StartTime                *metav1.Time     `json:"startTime,omitempty"`
    Steps                    []UpgradeStep    `json:"steps,omitempty"`
    CurrentStepIndex         int              `json:"currentStepIndex,omitempty"`
    Progress                 int              `json:"progress,omitempty"`
    Failure                  *UpgradeFailure  `json:"failure,omitempty"`
}

// UpgradeStep 升级步骤
type UpgradeStep struct {
    Name           UpgradeStepType `json:"name"`
    State          StepState       `json:"state"`
    StartTime      *metav1.Time    `json:"startTime,omitempty"`
    CompletionTime *metav1.Time    `json:"completionTime,omitempty"`
    Message        string          `json:"message,omitempty"`
}

// UpgradeFailure 升级失败信息
type UpgradeFailure struct {
    Step      string       `json:"step,omitempty"`
    Reason    string       `json:"reason,omitempty"`
    Message   string       `json:"message,omitempty"`
    Time      *metav1.Time `json:"time,omitempty"`
    Retryable bool         `json:"retryable,omitempty"`
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
#### 4.2 关键设计决策说明
**1. ClusterVersion 与 BKECluster 的关系**
```
BKECluster (1) ──── (1) ClusterVersion
```
- 每个 BKECluster 最多对应一个 ClusterVersion
- ClusterVersion 通过 `spec.clusterRef` 关联 BKECluster
- ClusterVersion 的命名约定：`clusterversion-<bkecluster-name>`

**2. 升级步骤映射**

将现有 Phase 映射为 ClusterVersion 的升级步骤：

| ClusterVersion Step | 对应现有 Phase | 说明 |
|---------------------|---------------|------|
| PreCheck | 无（新增） | 升级前健康检查和版本兼容性验证 |
| ProviderSelfUpgrade | EnsureProviderSelfUpgrade | Provider 自升级 |
| AgentUpgrade | EnsureAgentUpgrade | Agent DaemonSet 升级 |
| ContainerdUpgrade | EnsureContainerdUpgrade | Containerd 升级 |
| EtcdUpgrade | EnsureEtcdUpgrade | Etcd 滚动升级 |
| ControlPlaneUpgrade | EnsureMasterUpgrade | Master 节点升级 |
| WorkerUpgrade | EnsureWorkerUpgrade | Worker 节点升级 |
| ComponentUpgrade | EnsureComponentUpgrade | 核心组件升级 |
| PostCheck | 无（新增） | 升级后健康检查 |

**3. 升级触发机制**

当前触发：`BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion` 变化 → PhaseFlow 执行 PostDeployPhases

新触发方式：
- 用户创建/更新 ClusterVersion CR，设置 `spec.desiredVersion`
- ClusterVersionReconciler 检测到 `desiredVersion != currentVersion`
- 通过适配层修改 BKECluster 的 `OpenFuyaoVersion` 字段，触发现有 PhaseFlow 执行
- ClusterVersionReconciler 监控 BKECluster.Status 变化，更新 ClusterVersion.Status
### 五、控制器设计
#### 5.1 ClusterVersionReconciler
```go
// controllers/cvo/clusterversion_controller.go
package cvo

type ClusterVersionReconciler struct {
    client.Client
    Scheme *runtime.Scheme

    Validator      VersionValidator
    Orchestrator   UpgradeOrchestrator
    RollbackManager RollbackManager
}

func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    clusterVersion := &cvov1beta1.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, clusterVersion); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 处理 Finalizer
    if !clusterVersion.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, clusterVersion)
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
    default:
        return r.handleAvailable(ctx, clusterVersion)
    }
}
```
#### 5.2 状态机转换
```
┌───────────┐    desiredVersion != currentVersion     ┌─────────────┐
│ Available │ ──────────────────────────────────────→ │ Progressing │
└───────────┘                                         └──────┬──────┘
      ↑                                                      │
      │ 升级成功                                             │ 升级失败
      │                                                      ↓
      │                                               ┌──────────┐
      │                                               │ Degraded │
      │                                               └────┬─────┘
      │                                                     │
      │                              rollbackOnFailure=true │
      │                                                     ↓
      │                                                ┌────────────┐
      └─────────────────────────────────────────────── │ RollingBack│
           回滚完成                                    └────────────┘
```
#### 5.3 UpgradeOrchestrator — 升级编排器
```go
// pkg/cvo/orchestrator/orchestrator.go
package orchestrator

type UpgradeOrchestrator interface {
    // StartUpgrade 启动升级流程
    StartUpgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) error

    // ContinueUpgrade 继续升级流程（监控进度）
    ContinueUpgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) (bool, error)

    // Rollback 执行回滚
    Rollback(ctx context.Context, cv *cvov1beta1.ClusterVersion) error
}

type DefaultUpgradeOrchestrator struct {
    Client client.Client
}

// 默认升级步骤顺序
var defaultUpgradeSteps = []cvov1beta1.UpgradeStepType{
    cvov1beta1.UpgradeStepPreCheck,
    cvov1beta1.UpgradeStepProviderSelf,
    cvov1beta1.UpgradeStepAgent,
    cvov1beta1.UpgradeStepContainerd,
    cvov1beta1.UpgradeStepEtcd,
    cvov1beta1.UpgradeStepControlPlane,
    cvov1beta1.UpgradeStepWorker,
    cvov1beta1.UpgradeStepComponent,
    cvov1beta1.UpgradeStepPostCheck,
}
```
#### 5.4 适配层 — 与现有 PhaseFlow 协同
这是 Phase 4 独立实现的关键：**不替换现有 Phase，而是通过适配层桥接。**
```go
// pkg/cvo/adapter/bkecluster_adapter.go
package adapter

type BKEClusterAdapter struct {
    Client client.Client
}

// TriggerUpgrade 触发 BKECluster 升级
// 通过修改 BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion 来触发现有升级流程
func (a *BKEClusterAdapter) TriggerUpgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) error {
    bkeCluster, err := a.getBKECluster(ctx, cv)
    if err != nil {
        return err
    }

    // 更新 BKECluster 的版本字段，触发 PhaseFlow 执行
    patchFunc := func(bc *bkev1beta1.BKECluster) {
        bc.Spec.ClusterConfig.Cluster.OpenFuyaoVersion = cv.Spec.DesiredVersion
        if cv.Spec.DesiredComponentVersions.Kubernetes != "" {
            bc.Spec.ClusterConfig.Cluster.KubernetesVersion = cv.Spec.DesiredComponentVersions.Kubernetes
        }
        if cv.Spec.DesiredComponentVersions.Etcd != "" {
            bc.Spec.ClusterConfig.Cluster.EtcdVersion = cv.Spec.DesiredComponentVersions.EtcdVersion
        }
        if cv.Spec.DesiredComponentVersions.Containerd != "" {
            bc.Spec.ClusterConfig.Cluster.ContainerdVersion = cv.Spec.DesiredComponentVersions.Containerd
        }
    }

    return mergecluster.SyncStatusUntilComplete(a.Client, bkeCluster, patchFunc)
}

// GetUpgradeProgress 从 BKECluster.Status 获取升级进度
func (a *BKEClusterAdapter) GetUpgradeProgress(ctx context.Context, cv *cvov1beta1.ClusterVersion) (*UpgradeProgress, error) {
    bkeCluster, err := a.getBKECluster(ctx, cv)
    if err != nil {
        return nil, err
    }

    progress := &UpgradeProgress{
        CurrentVersions: cvov1beta1.ComponentVersions{
            OpenFuyao:  bkeCluster.Status.OpenFuyaoVersion,
            Kubernetes: bkeCluster.Status.KubernetesVersion,
            Etcd:       bkeCluster.Status.EtcdVersion,
            Containerd: bkeCluster.Status.ContainerdVersion,
        },
    }

    // 从 PhaseStatus 中提取各步骤状态
    for _, phaseStatus := range bkeCluster.Status.PhaseStatus {
        stepType := mapPhaseToStep(phaseStatus.Name)
        if stepType != "" {
            progress.StepStates = append(progress.StepStates, StepProgress{
                Step:  stepType,
                State: mapPhaseStatusToStepState(phaseStatus.Status),
            })
        }
    }

    return progress, nil
}
```
### 六、实现步骤
| 步骤 | 任务 | 产出文件 | 依赖 |
|------|------|---------|------|
| 4.1 | 定义 ClusterVersion CRD | `api/cvo/v1beta1/clusterversion_types.go` | 无 |
| 4.2 | 定义 GroupVersion 信息 | `api/cvo/v1beta1/groupversion_info.go` | 无 |
| 4.3 | 生成 deepcopy + CRD YAML | `make generate; make manifests` | 4.1-4.2 |
| 4.4 | 实现 VersionValidator | `pkg/cvo/validator/validator.go` | 4.1 |
| 4.5 | 实现 BKEClusterAdapter | `pkg/cvo/adapter/bkecluster_adapter.go` | 4.1 |
| 4.6 | 实现 UpgradeOrchestrator | `pkg/cvo/orchestrator/orchestrator.go` | 4.4, 4.5 |
| 4.7 | 实现 RollbackManager | `pkg/cvo/rollback/rollback_manager.go` | 4.1 |
| 4.8 | 实现 ClusterVersionReconciler | `controllers/cvo/clusterversion_controller.go` | 4.6, 4.7 |
| 4.9 | 注册 Controller 到 main.go | `cmd/capbke/main.go` | 4.8 |
| 4.10 | 编写单元测试 | `controllers/cvo/clusterversion_controller_test.go` 等 | 4.8 |
### 七、目录结构
```
cluster-api-provider-bke/
├── api/
│   └── cvo/v1beta1/                          # 新增
│       ├── clusterversion_types.go            # ClusterVersion CRD
│       ├── groupversion_info.go               # GroupVersion 定义
│       └── zz_generated.deepcopy.go           # 自动生成
├── controllers/
│   └── cvo/                                  # 新增
│       ├── clusterversion_controller.go       # ClusterVersion Reconciler
│       └── clusterversion_controller_test.go  # 测试
├── pkg/
│   └── cvo/                                  # 新增
│       ├── adapter/
│       │   └── bkecluster_adapter.go         # BKECluster 适配层
│       ├── orchestrator/
│       │   ├── orchestrator.go               # 升级编排器接口
│       │   └── default_orchestrator.go       # 默认实现
│       ├── validator/
│       │   └── validator.go                  # 版本验证器
│       └── rollback/
│           └── rollback_manager.go           # 回滚管理器
└── config/
    ├── crd/bases/                            # 新增 CRD YAML
    └── rbac/                                 # 新增 RBAC
```
### 八、关键交互流程
#### 8.1 升级触发流程
```
用户创建 ClusterVersion CR (desiredVersion=v26.03)
    │
    ▼
ClusterVersionReconciler.Reconcile()
    │
    ├── VersionValidator.Validate()  ─── 版本兼容性检查
    │
    ├── 初始化 CurrentUpgrade.Steps (9个步骤)
    │
    ├── BKEClusterAdapter.TriggerUpgrade()
    │       │
    │       └── 修改 BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion = "v26.03"
    │
    └── 更新 ClusterVersion.Status.Phase = "Progressing"
```
#### 8.2 升级进度监控流程
```
BKEClusterReconciler.Reconcile() (现有逻辑不变)
    │
    ├── PhaseFlow 执行 PostDeployPhases
    │       │
    │       └── 各 Phase 执行升级，更新 BKECluster.Status
    │
    └── BKECluster.Status 变更触发 ClusterVersion Watch
            │
            ▼
ClusterVersionReconciler.Reconcile()
    │
    ├── BKEClusterAdapter.GetUpgradeProgress()
    │       │
    │       └── 读取 BKECluster.Status 中的版本和 PhaseStatus
    │
    ├── 更新 ClusterVersion.Status.CurrentUpgrade.Steps 状态
    │
    └── 判断升级是否完成
            │
            ├── 完成 → Phase = "Available", 记录 History
            └── 失败 → Phase = "Degraded", 触发回滚（如果配置）
```
#### 8.3 回滚流程
```
ClusterVersion.Status.Phase = "Degraded"
    │
    ├── RollbackOnFailure = true
    │       │
    │       └── ClusterVersionReconciler.handleDegraded()
    │               │
    │               ├── Phase → "RollingBack"
    │               │
    │               ├── BKEClusterAdapter.TriggerUpgrade(previousVersion)
    │               │       │
    │               └── 修改 BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion = previousVersion
    │               │
    │               └── 监控回滚进度
    │
    └── RollbackOnFailure = false
            │
            └── 保持 "Degraded" 状态，等待人工介入
```
### 九、与现有代码的兼容性
1. **不修改现有 Phase 实现**：所有 `ensure_*_upgrade.go` 文件保持不变
2. **不修改 BKEClusterReconciler 主逻辑**：现有 PhaseFlow 执行流程不变
3. **通过 Watch 机制监控**：ClusterVersionReconciler Watch BKECluster 的 Status 变化
4. **版本信息双向同步**：
   - ClusterVersion → BKECluster：通过 `BKEClusterAdapter.TriggerUpgrade()` 修改 BKECluster Spec
   - BKECluster → ClusterVersion：通过 Watch BKECluster Status 变化更新 ClusterVersion Status
### 十、验收标准
1. 创建 ClusterVersion CR 后，自动触发 BKECluster 升级流程
2. ClusterVersion.Status 准确反映升级进度和各步骤状态
3. 升级失败时，若 `rollbackOnFailure=true`，自动回滚到前一版本
4. 升级历史记录完整，可通过 `kubectl get clusterversion -o yaml` 查看
5. 现有升级流程行为不受影响（零回归）
6. 支持 `spec.pause=true` 暂停升级

# Phase 重构为 ComponentVersion 方案设计
## Phase 重构为 ComponentVersion 方案设计分析
### 一、现有架构问题深度剖析
#### 1.1 当前 Phase 升级体系的核心缺陷
当前升级体系由 7 个 PostDeployPhase 组成，它们在 [list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go) 中按固定顺序注册：
```
EnsureProviderSelfUpgrade → EnsureAgentUpgrade → EnsureContainerdUpgrade 
→ EnsureEtcdUpgrade → EnsureWorkerUpgrade → EnsureMasterUpgrade → EnsureComponentUpgrade
```
**核心问题：**

| 问题 | 具体表现 | 源码位置 |
|------|----------|----------|
| **版本判断维度单一** | 各 Phase 独立判断 `NeedExecute`，只检查自身关注的版本字段变化 | [ensure_master_upgrade.go:NeedExecute](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go) 只检查 K8s 版本 |
| **版本状态分散** | `BKEClusterStatus` 中版本字段分散在 `OpenFuyaoVersion`、`KubernetesVersion`、`EtcdVersion`、`ContainerdVersion` | [bkecluster_status.go:93-102](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_status.go#L93-L102) |
| **缺少统一编排** | PhaseFlow 顺序执行，无法根据版本变更动态调整执行顺序 | [phase_flow.go:executePhases](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go) 线性遍历 |
| **升级状态不完整** | 缺少 Agent、Addon 等组件版本追踪，升级进度不可见 | `BKEClusterStatus` 无 `BKEAgentVersion` 字段 |
| **无回滚能力** | 升级失败后无自动回滚机制，只能人工干预 | 所有 Phase 的 `Execute()` 均无回滚逻辑 |
#### 1.2 版本信息流分析
当前版本信息的流向：
```
BKEClusterSpec.ClusterConfig.Cluster
├── OpenFuyaoVersion    ──→ EnsureProviderSelfUpgrade / EnsureAgentUpgrade / EnsureComponentUpgrade
├── KubernetesVersion   ──→ EnsureMasterUpgrade / EnsureWorkerUpgrade
├── EtcdVersion         ──→ EnsureEtcdUpgrade
└── ContainerdVersion   ──→ EnsureContainerdUpgrade

BKEClusterStatus
├── OpenFuyaoVersion    ←── EnsureProviderSelfUpgrade 完成后写入
├── KubernetesVersion   ←── EnsureMasterUpgrade 完成后写入
├── EtcdVersion         ←── EnsureEtcdUpgrade 完成后写入
└── ContainerdVersion   ←── EnsureContainerdUpgrade 完成后写入
```
**关键发现：** 版本写入时机不统一——`KubernetesVersion` 在 `EnsureMasterUpgrade` 完成后才更新（见 [ensure_master_upgrade.go:rolloutUpgrade](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go) 中 `bkeCluster.Status.KubernetesVersion = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion`），而其他组件版本在各自 Phase 中更新，导致中间状态不一致。
### 二、ComponentVersion 核心设计
#### 2.1 设计理念
借鉴 OpenShift CVO 的 `ComponentVersion` 概念，将每个可升级组件抽象为独立的版本管理单元。核心思想：
1. **声明式驱动**：用户声明 `desiredVersion`，系统自动计算需要升级的组件
2. **组件级追踪**：每个组件有独立的版本状态和升级进度
3. **DAG 编排**：基于组件依赖关系动态编排升级顺序
4. **统一状态**：所有组件版本汇聚到 `ClusterVersion` CRD 的 `status` 中
#### 2.2 ComponentVersion 接口设计
```go
// pkg/cvo/component/component.go

type ComponentName string

const (
    ComponentKubernetes      ComponentName = "kubernetes"
    ComponentEtcd            ComponentName = "etcd"
    ComponentContainerd      ComponentName = "containerd"
    ComponentOpenFuyao       ComponentName = "openFuyao"
    ComponentBKEAgent        ComponentName = "bkeAgent"
    ComponentBKEProvider     ComponentName = "bkeProvider"
    ComponentAddon           ComponentName = "addon"
)

type ComponentVersion struct {
    Name       ComponentName `json:"name"`
    Current    string        `json:"current,omitempty"`
    Desired    string        `json:"desired,omitempty"`
    Phase      ComponentPhase `json:"phase,omitempty"`
    UpdatedAt  *metav1.Time  `json:"updatedAt,omitempty"`
    Message    string        `json:"message,omitempty"`
}

type ComponentPhase string

const (
    ComponentPhaseAvailable   ComponentPhase = "Available"
    ComponentPhaseProgressing ComponentPhase = "Progressing"
    ComponentPhaseDegraded    ComponentPhase = "Degraded"
    ComponentPhaseSucceeded   ComponentPhase = "Succeeded"
    ComponentPhaseSkipped     ComponentPhase = "Skipped"
)

type Upgrader interface {
    Name() ComponentName
    Dependencies() []ComponentName
    NeedUpgrade(ctx context.Context, current, desired *cvov1beta1.ClusterVersion) (bool, error)
    Upgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error)
    Rollback(ctx context.Context, cv *cvov1beta1.ClusterVersion) error
    CurrentVersion(ctx context.Context, cv *cvov1beta1.ClusterVersion) (string, error)
}
```
#### 2.3 组件依赖 DAG
基于当前 Phase 执行顺序和组件间实际依赖关系，构建如下 DAG：
```
                    ┌──────────────────┐
                    │  BKEProvider     │
                    │  (self-upgrade)  │
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │   BKEAgent       │
                    │   (daemonset)    │
                    └────────┬─────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───────┐      │     ┌────────▼───────┐
     │   Containerd   │      │     │   OpenFuyao    │
     │   (node-level) │      │     │  (components)  │
     └────────┬───────┘      │     └────────┬───────┘
              │              │              │
     ┌────────▼───────┐      │              │
     │     Etcd       │      │              │
     │  (data plane)  │      │              │
     └────────┬───────┘      │              │
              │              │              │
              └──────┬───────┘──────────────┘
                     │
           ┌─────────▼──────────┐
           │    Kubernetes      │
           │ (control-plane +   │
           │  worker nodes)     │
           └─────────┬──────────┘
                     │
           ┌─────────▼──────────┐
           │      Addon         │
           │  (post-upgrade)    │
           └────────────────────┘
```
**依赖关系表：**

| 组件 | 依赖 | 理由 |
|------|------|------|
| BKEProvider | 无 | 自升级，必须先完成 |
| BKEAgent | BKEProvider | Agent 镜像由 Provider 管理 |
| Containerd | BKEAgent | 需要 Agent 执行节点级操作 |
| Etcd | BKEAgent | 需要 Agent 执行 etcd 升级命令 |
| OpenFuyao | BKEAgent | 组件镜像由 Agent 管理 |
| Kubernetes | Containerd, Etcd | 需要容器运行时和 etcd 就绪 |
| Addon | Kubernetes | 依赖 K8s API 可用 |
#### 2.4 DAG 调度器设计
```go
// pkg/cvo/scheduler/dag_scheduler.go

type DAGScheduler struct {
    upgraders   map[ComponentName]Upgrader
    dependencies map[ComponentName][]ComponentName
}

type ScheduleResult struct {
    Ready    []ComponentName
    Blocked  []ComponentName
    Running  []ComponentName
    Done     []ComponentName
}

func (s *DAGScheduler) Schedule(
    cv *cvov1beta1.ClusterVersion,
    completed []ComponentName,
) *ScheduleResult {
    result := &ScheduleResult{}
    
    for name, upgrader := range s.upgraders {
        componentStatus := getComponentStatus(cv, name)
        
        switch componentStatus.Phase {
        case ComponentPhaseSucceeded:
            result.Done = append(result.Done, name)
        case ComponentPhaseProgressing:
            result.Running = append(result.Running, name)
        default:
            if s.dependenciesMet(name, completed) {
                result.Ready = append(result.Ready, name)
            } else {
                result.Blocked = append(result.Blocked, name)
            }
        }
    }
    
    return result
}

func (s *DAGScheduler) dependenciesMet(
    name ComponentName,
    completed []ComponentName,
) bool {
    deps := s.dependencies[name]
    for _, dep := range deps {
        if !contains(completed, dep) {
            return false
        }
    }
    return true
}
```
### 三、Phase 到 ComponentVersion 的映射方案
#### 3.1 映射关系
| 现有 Phase | 目标 Upgrader | 版本来源 | 版本写入 |
|------------|--------------|----------|----------|
| `EnsureProviderSelfUpgrade` | `BKEProviderUpgrader` | `Spec.ClusterConfig.Cluster.OpenFuyaoVersion` | `Status.ComponentVersions.OpenFuyao` |
| `EnsureAgentUpgrade` | `BKEAgentUpgrader` | 从 OpenFuyaoVersion 推导 | `Status.ComponentVersions.BKEAgent` |
| `EnsureContainerdUpgrade` | `ContainerdUpgrader` | `Spec.ClusterConfig.Cluster.ContainerdVersion` | `Status.ComponentVersions.Containerd` |
| `EnsureEtcdUpgrade` | `EtcdUpgrader` | `Spec.ClusterConfig.Cluster.EtcdVersion` | `Status.ComponentVersions.Etcd` |
| `EnsureWorkerUpgrade` | 合并到 `KubernetesUpgrader` | `Spec.ClusterConfig.Cluster.KubernetesVersion` | `Status.ComponentVersions.Kubernetes` |
| `EnsureMasterUpgrade` | 合并到 `KubernetesUpgrader` | `Spec.ClusterConfig.Cluster.KubernetesVersion` | `Status.ComponentVersions.Kubernetes` |
| `EnsureComponentUpgrade` | `OpenFuyaoComponentUpgrader` | `Spec.ClusterConfig.Cluster.OpenFuyaoVersion` | `Status.ComponentVersions.OpenFuyao` |

**关键设计决策：** `EnsureWorkerUpgrade` 和 `EnsureMasterUpgrade` 合并为 `KubernetesUpgrader`，因为它们本质上是同一组件（Kubernetes）在不同节点角色上的升级操作，应统一编排。
#### 3.2 KubernetesUpgrader 内部编排
```go
// pkg/cvo/upgrader/kubernetes_upgrader.go

type KubernetesUpgrader struct {
    BaseUpgrader
}

type K8sUpgradeStep string

const (
    K8sStepPreCheck     K8sUpgradeStep = "PreCheck"
    K8sStepEtcdBackup   K8sUpgradeStep = "EtcdBackup"
    K8sStepMasterRollout K8sUpgradeStep = "MasterRollout"
    K8sStepWorkerRollout K8sUpgradeStep = "WorkerRollout"
    K8sStepPostCheck    K8sUpgradeStep = "PostCheck"
)

func (u *KubernetesUpgrader) Upgrade(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    currentStep := u.getCurrentStep(cv)
    
    switch currentStep {
    case K8sStepPreCheck:
        return u.preCheck(ctx, cv)
    case K8sStepEtcdBackup:
        return u.etcdBackup(ctx, cv)
    case K8sStepMasterRollout:
        return u.masterRollout(ctx, cv)
    case K8sStepWorkerRollout:
        return u.workerRollout(ctx, cv)
    case K8sStepPostCheck:
        return u.postCheck(ctx, cv)
    default:
        return u.startUpgrade(ctx, cv)
    }
}
```
### 四、PhaseFlow 重构方案
#### 4.1 双轨运行架构
为了平滑过渡，设计兼容层使现有 Phase 和新 ComponentVersion 系统可以并行运行：
```go
// pkg/phaseframe/phases/phase_flow.go 重构

type PhaseFlow struct {
    BKEPhases []phaseframe.Phase
    ctx       *phaseframe.PhaseContext
    oldBKECluster *bkev1beta1.BKECluster
    newBKECluster *bkev1beta1.BKECluster
    
    // 新增：CVO 编排器（Feature Gate 控制）
    cvoOrchestrator *cvo.Orchestrator
    useCVO          bool
}

func (p *PhaseFlow) Execute() (ctrl.Result, error) {
    defer p.handlePanic()
    
    if p.useCVO {
        return p.executeWithCVO()
    }
    
    // 原有逻辑
    phases := p.determinePhases()
    go p.ctx.WatchBKEClusterStatus()
    return p.executePhases(phases)
}

func (p *PhaseFlow) executeWithCVO() (ctrl.Result, error) {
    return p.cvoOrchestrator.Orchestrate(p.ctx)
}
```
#### 4.2 CVO Orchestrator 集成
```go
// pkg/cvo/orchestrator/orchestrator.go

type Orchestrator struct {
    client    client.Client
    scheduler *scheduler.DAGScheduler
    upgraders map[ComponentName]Upgrader
    
    // 与现有 PhaseFlow 的桥接
    phaseContext *phaseframe.PhaseContext
}

func (o *Orchestrator) Orchestrate(
    phaseCtx *phaseframe.PhaseContext,
) (ctrl.Result, error) {
    ctx := phaseCtx.Ctx
    bkeCluster := phaseCtx.BKECluster
    
    // 1. 获取或创建 ClusterVersion CR
    cv, err := o.getOrCreateClusterVersion(ctx, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 2. 同步 BKECluster Spec → ClusterVersion Spec
    if err := o.syncDesiredVersions(bkeCluster, cv); err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. 调度可执行组件
    completed := o.getCompletedComponents(cv)
    scheduleResult := o.scheduler.Schedule(cv, completed)
    
    // 4. 执行就绪组件的升级
    for _, componentName := range scheduleResult.Ready {
        upgrader := o.upgraders[componentName]
        result, err := upgrader.Upgrade(ctx, cv)
        if err != nil {
            o.markComponentFailed(cv, componentName, err)
            return result, err
        }
        if !result.Requeue && result.RequeueAfter == 0 {
            o.markComponentSucceeded(cv, componentName)
        }
    }
    
    // 5. 同步 ClusterVersion Status → BKECluster Status
    if err := o.syncVersionStatus(cv, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }
    
    // 6. 更新 CR 状态
    if err := o.client.Status().Update(ctx, cv); err != nil {
        return ctrl.Result{}, err
    }
    
    // 7. 判断是否全部完成
    if len(scheduleResult.Blocked) == 0 && len(scheduleResult.Ready) == 0 
       && len(scheduleResult.Running) == 0 {
        return ctrl.Result{}, nil
    }
    
    return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
```
#### 4.3 版本同步逻辑
```go
// syncDesiredVersions 将 BKECluster.Spec 中的版本信息同步到 ClusterVersion.Spec

func (o *Orchestrator) syncDesiredVersions(
    bkeCluster *bkev1beta1.BKECluster,
    cv *cvov1beta1.ClusterVersion,
) error {
    cluster := bkeCluster.Spec.ClusterConfig.Cluster
    
    cv.Spec.DesiredComponentVersions = cvov1beta1.ComponentVersions{
        Kubernetes: cluster.KubernetesVersion,
        Etcd:       cluster.EtcdVersion,
        Containerd: cluster.ContainerdVersion,
        OpenFuyao:  cluster.OpenFuyaoVersion,
        BKEAgent:   deriveBKEAgentVersion(cluster.OpenFuyaoVersion),
        BKEProvider: deriveProviderVersion(cluster.OpenFuyaoVersion),
    }
    
    return nil
}

// syncVersionStatus 将 ClusterVersion.Status 中的版本信息同步回 BKECluster.Status

func (o *Orchestrator) syncVersionStatus(
    cv *cvov1beta1.ClusterVersion,
    bkeCluster *bkev1beta1.BKECluster,
) error {
    bkeCluster.Status.OpenFuyaoVersion = cv.Status.CurrentComponentVersions.OpenFuyao
    bkeCluster.Status.KubernetesVersion = cv.Status.CurrentComponentVersions.Kubernetes
    bkeCluster.Status.EtcdVersion = cv.Status.CurrentComponentVersions.Etcd
    bkeCluster.Status.ContainerdVersion = cv.Status.CurrentComponentVersions.Containerd
    return nil
}
```
### 五、各 Phase 重构详细方案
#### 5.1 EnsureProviderSelfUpgrade → BKEProviderUpgrader
**当前逻辑**（[ensure_provider_self_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_provider_self_upgrade.go)）：
- `NeedExecute`: 检查 `isProviderNeedUpgrade`，比较当前运行的 Provider Deployment 镜像 tag 与 spec 中的版本
- `Execute`: 等待 Provider Deployment 滚动更新完成

**重构方案：**
```go
// pkg/cvo/upgrader/provider_upgrader.go

type BKEProviderUpgrader struct {
    BaseUpgrader
}

func (u *BKEProviderUpgrader) Name() ComponentName {
    return ComponentBKEProvider
}

func (u *BKEProviderUpgrader) Dependencies() []ComponentName {
    return nil  // 无依赖，最先执行
}

func (u *BKEProviderUpgrader) NeedUpgrade(
    ctx context.Context,
    current, desired *cvov1beta1.ClusterVersion,
) (bool, error) {
    currentVersion := current.Status.CurrentComponentVersions.BKEProvider
    desiredVersion := desired.Spec.DesiredComponentVersions.BKEProvider
    return currentVersion != desiredVersion && desiredVersion != "", nil
}

func (u *BKEProviderUpgrader) Upgrade(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    // 复用现有 phaseutil.DeploymentTarget 逻辑
    target := phaseutil.DeploymentTarget{
        Namespace: providerNamespace,
        Name:      providerDeploymentName,
        Container: providerContainerName,
    }
    // 等待 Deployment 就绪
    return u.waitForDeploymentReady(ctx, cv, target)
}

func (u *BKEProviderUpgrader) CurrentVersion(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (string, error) {
    // 从运行中的 Deployment 获取当前版本
    return u.getDeploymentImageTag(ctx, providerNamespace, providerDeploymentName, providerContainerName)
}
```
#### 5.2 EnsureAgentUpgrade → BKEAgentUpgrader
**当前逻辑**（[ensure_agent_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_agent_upgrade.go)）：
- `NeedExecute`: 比较 `Status.OpenFuyaoVersion` 与 `Spec.OpenFuyaoVersion`
- `Execute`: 更新 DaemonSet 镜像，等待就绪

**重构方案：**
```go
// pkg/cvo/upgrader/agent_upgrader.go

type BKEAgentUpgrader struct {
    BaseUpgrader
}

func (u *BKEAgentUpgrader) Dependencies() []ComponentName {
    return []ComponentName{ComponentBKEProvider}
}

func (u *BKEAgentUpgrader) NeedUpgrade(
    ctx context.Context,
    current, desired *cvov1beta1.ClusterVersion,
) (bool, error) {
    currentVersion := current.Status.CurrentComponentVersions.BKEAgent
    desiredVersion := desired.Spec.DesiredComponentVersions.BKEAgent
    return currentVersion != desiredVersion && desiredVersion != "", nil
}

func (u *BKEAgentUpgrader) Upgrade(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    // 复用现有 EnsureAgentUpgrade 的核心逻辑
    // 1. 获取远程集群 client
    // 2. 更新 DaemonSet 镜像
    // 3. 等待 DaemonSet 就绪
    return u.rolloutAgentDaemonSet(ctx, cv)
}
```
#### 5.3 EnsureContainerdUpgrade → ContainerdUpgrader
**当前逻辑**（[ensure_containerd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go)）：
- `Execute`: 通过 `command.ENV` 命令在所有节点上升级 containerd

**重构方案：**
```go
// pkg/cvo/upgrader/containerd_upgrader.go

type ContainerdUpgrader struct {
    BaseUpgrader
}

func (u *ContainerdUpgrader) Dependencies() []ComponentName {
    return []ComponentName{ComponentBKEAgent}
}

func (u *ContainerdUpgrader) Upgrade(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    // 复用现有 EnsureContainerdUpgrade 的 getCommand + rolloutContainerd 逻辑
    // 但通过 NodeConfig 支持节点级配置差异
    return u.rolloutContainerd(ctx, cv)
}
```
#### 5.4 EnsureEtcdUpgrade → EtcdUpgrader
**当前逻辑**（[ensure_etcd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_etcd_upgrade.go)）：
- 逐个 etcd 节点执行升级命令
- 支持备份和健康检查

**重构方案：**
```go
// pkg/cvo/upgrader/etcd_upgrader.go

type EtcdUpgrader struct {
    BaseUpgrader
}

func (u *EtcdUpgrader) Dependencies() []ComponentName {
    return []ComponentName{ComponentBKEAgent}
}

func (u *EtcdUpgrader) Upgrade(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    // 复用现有 NodeUpgradeParams 和逐节点升级逻辑
    // 增加 NodeConfig 支持（如不同节点的 etcd 配置差异）
    return u.rolloutEtcd(ctx, cv)
}
```
#### 5.5 EnsureMasterUpgrade + EnsureWorkerUpgrade → KubernetesUpgrader
**当前逻辑**：
- `EnsureMasterUpgrade`：逐个 master 节点执行升级命令，完成后更新 `Status.KubernetesVersion`
- `EnsureWorkerUpgrade`：逐个 worker 节点 drain + 升级 + uncordon

**重构方案（合并为统一编排器）：**
```go
// pkg/cvo/upgrader/kubernetes_upgrader.go

type KubernetesUpgrader struct {
    BaseUpgrader
}

func (u *KubernetesUpgrader) Dependencies() []ComponentName {
    return []ComponentName{ComponentContainerd, ComponentEtcd}
}

func (u *KubernetesUpgrader) Upgrade(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    step := u.getCurrentStep(cv)
    
    switch step {
    case "", K8sStepPreCheck:
        if err := u.preCheck(ctx, cv); err != nil {
            return ctrl.Result{}, err
        }
        u.setCurrentStep(cv, K8sStepEtcdBackup)
        return ctrl.Result{Requeue: true}, nil
        
    case K8sStepEtcdBackup:
        if err := u.backupEtcd(ctx, cv); err != nil {
            return ctrl.Result{}, err
        }
        u.setCurrentStep(cv, K8sStepMasterRollout)
        return ctrl.Result{Requeue: true}, nil
        
    case K8sStepMasterRollout:
        result, err := u.rolloutMasters(ctx, cv)
        if err != nil {
            return result, err
        }
        if result.Requeue || result.RequeueAfter > 0 {
            return result, nil
        }
        u.setCurrentStep(cv, K8sStepWorkerRollout)
        return ctrl.Result{Requeue: true}, nil
        
    case K8sStepWorkerRollout:
        result, err := u.rolloutWorkers(ctx, cv)
        if err != nil {
            return result, err
        }
        if result.Requeue || result.RequeueAfter > 0 {
            return result, nil
        }
        u.setCurrentStep(cv, K8sStepPostCheck)
        return ctrl.Result{Requeue: true}, nil
        
    case K8sStepPostCheck:
        if err := u.postCheck(ctx, cv); err != nil {
            return ctrl.Result{}, err
        }
        return ctrl.Result{}, nil
    }
    
    return ctrl.Result{}, nil
}

func (u *KubernetesUpgrader) rolloutMasters(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    // 复用 EnsureMasterUpgrade.executeNodeUpgrade 核心逻辑
    // 但从 ClusterVersion 获取目标版本而非 BKECluster.Spec
}

func (u *KubernetesUpgrader) rolloutWorkers(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    // 复用 EnsureWorkerUpgrade 的 drain + upgrade + uncordon 逻辑
}
```
#### 5.6 EnsureComponentUpgrade → OpenFuyaoComponentUpgrader
**当前逻辑**（[ensure_component_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_component_upgrade.go)）：
- 连接远程集群，更新 openFuyao 组件的 Deployment 镜像

**重构方案：**
```go
// pkg/cvo/upgrader/openfuyao_upgrader.go

type OpenFuyaoComponentUpgrader struct {
    BaseUpgrader
}

func (u *OpenFuyaoComponentUpgrader) Dependencies() []ComponentName {
    return []ComponentName{ComponentBKEAgent}
}
```
### 六、NodeConfig 集成设计
#### 6.1 NodeConfig 概念
借鉴 OpenShift 的 MachineConfig，为节点级升级提供声明式配置：
```go
// api/cvo/v1beta1/nodeconfig_types.go

type NodeConfig struct {
    // 节点角色
    Role NodeRole `json:"role"`
    
    // 目标版本
    Version string `json:"version,omitempty"`
    
    // OS 配置
    OSConfig *OSConfig `json:"osConfig,omitempty"`
    
    // 容器运行时配置
    ContainerRuntime *ContainerRuntimeConfig `json:"containerRuntime,omitempty"`
    
    // Kubelet 配置
    Kubelet *KubeletConfig `json:"kubelet,omitempty"`
}

type NodeRole string

const (
    NodeRoleMaster NodeRole = "master"
    NodeRoleWorker NodeRole = "worker"
    NodeRoleEtcd   NodeRole = "etcd"
)

type OSConfig struct {
    // OS 包更新列表
    Packages []PackageUpdate `json:"packages,omitempty"`
}

type PackageUpdate struct {
    Name    string `json:"name"`
    Version string `json:"version,omitempty"`
}

type ContainerRuntimeConfig struct {
    Type         string `json:"type"`
    Version      string `json:"version"`
    ConfigData   string `json:"configData,omitempty"`
}

type KubeletConfig struct {
    Version       string            `json:"version"`
    ExtraArgs     map[string]string `json:"extraArgs,omitempty"`
    FeatureGates  map[string]bool   `json:"featureGates,omitempty"`
}
```
#### 6.2 NodeConfig 与 Upgrader 的集成
```go
// pkg/cvo/upgrader/node_config_adapter.go

type NodeConfigAdapter struct {
    client client.Client
}

func (a *NodeConfigAdapter) ResolveNodeConfigs(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
    bkeCluster *bkev1beta1.BKECluster,
) ([]NodeConfig, error) {
    var configs []NodeConfig
    
    cluster := bkeCluster.Spec.ClusterConfig.Cluster
    
    // Master 节点配置
    configs = append(configs, NodeConfig{
        Role:    NodeRoleMaster,
        Version: cluster.KubernetesVersion,
        ContainerRuntime: &ContainerRuntimeConfig{
            Type:    "containerd",
            Version: cluster.ContainerdVersion,
        },
        Kubelet: &KubeletConfig{
            Version: cluster.KubernetesVersion,
        },
    })
    
    // Worker 节点配置
    configs = append(configs, NodeConfig{
        Role:    NodeRoleWorker,
        Version: cluster.KubernetesVersion,
        ContainerRuntime: &ContainerRuntimeConfig{
            Type:    "containerd",
            Version: cluster.ContainerdVersion,
        },
        Kubelet: &KubeletConfig{
            Version: cluster.KubernetesVersion,
        },
    })
    
    // Etcd 节点配置（如果独立部署）
    if cluster.EtcdVersion != "" {
        configs = append(configs, NodeConfig{
            Role:    NodeRoleEtcd,
            Version: cluster.EtcdVersion,
        })
    }
    
    return configs, nil
}
```
### 七、ClusterVersion CRD 与 BKECluster 的关系
#### 7.1 从属关系
```
BKECluster (Owner)
    └── ClusterVersion (Owned, 1:1)
```
ClusterVersion 由 BKECluster Controller 自动创建和管理，用户不直接操作 ClusterVersion。
#### 7.2 版本信息流转
```
用户修改 BKECluster.Spec.ClusterConfig.Cluster
         │
         ▼
BKECluster Controller 检测到版本变化
         │
         ▼
同步到 ClusterVersion.Spec.DesiredComponentVersions
         │
         ▼
CVO Controller 检测到 ClusterVersion 变化
         │
         ▼
DAG Scheduler 计算可执行组件
         │
         ▼
Upgrader 执行升级，更新 ClusterVersion.Status
         │
         ▼
同步回 BKECluster.Status 的版本字段
```
#### 7.3 ClusterVersion Status 扩展
在 [stage4.md](file:///d:/code/github/cluster-api-provider-bke/code/kep/plan/stage4.md) 已设计的 `ClusterVersionStatus` 基础上，增加组件级状态追踪：
```go
type ClusterVersionStatus struct {
    CurrentVersion string `json:"currentVersion,omitempty"`
    
    CurrentComponentVersions ComponentVersions `json:"currentComponentVersions,omitempty"`
    
    // 新增：组件级详细状态
    ComponentStatuses map[string]ComponentVersionStatus `json:"componentStatuses,omitempty"`
    
    Phase          ClusterVersionPhase   `json:"phase,omitempty"`
    Conditions     []ClusterVersionCondition `json:"conditions,omitempty"`
    History        []UpgradeHistory      `json:"history,omitempty"`
    CurrentUpgrade *CurrentUpgrade       `json:"currentUpgrade,omitempty"`
}

type ComponentVersionStatus struct {
    Name       ComponentName   `json:"name"`
    Current    string          `json:"current,omitempty"`
    Desired    string          `json:"desired,omitempty"`
    Phase      ComponentPhase  `json:"phase,omitempty"`
    Message    string          `json:"message,omitempty"`
    UpdatedAt  *metav1.Time    `json:"updatedAt,omitempty"`
}
```
### 八、实施路径与迁移策略
#### 8.1 三阶段迁移
| 阶段 | 目标 | 改动范围 | Feature Gate |
|------|------|----------|-------------|
| **阶段 A** | 引入 ClusterVersion CRD + CVO Controller，与现有 PhaseFlow 并行 | 新增 `api/cvo/`、`pkg/cvo/`，不修改现有代码 | `CVOUpgrade=false`（默认关闭） |
| **阶段 B** | 实现 Upgrader 适配层，复用现有 Phase 核心逻辑 | 新增 `pkg/cvo/upgrader/`，各 Upgrader 内部调用现有 phaseutil 函数 | `CVOUpgrade=true`（可开启） |
| **阶段 C** | 移除旧 PhaseFlow 升级逻辑，完全由 CVO 接管 | 修改 `phase_flow.go`，移除 PostDeployPhases 中的升级 Phase | `CVOUpgrade=true`（默认开启） |
#### 8.2 阶段 A 详细步骤
1. 创建 `api/cvo/v1beta1/` 目录，定义 ClusterVersion CRD
2. 创建 `pkg/cvo/` 目录结构：
   ```
   pkg/cvo/
   ├── orchestrator/     # 升级编排器
   ├── scheduler/        # DAG 调度器
   ├── upgrader/         # 各组件升级器
   ├── validator/        # 版本验证器
   └── rollback/         # 回滚管理器
   ```
3. 在 BKECluster Controller 中添加 ClusterVersion 创建逻辑
4. 添加 Feature Gate `CVOUpgrade`
#### 8.3 阶段 B 详细步骤
1. 实现各 Upgrader，内部复用现有 Phase 的核心函数
2. 实现 DAG Scheduler
3. 实现 Orchestrator 与 PhaseFlow 的桥接
4. 实现版本同步逻辑（BKECluster ↔ ClusterVersion）
5. 添加 NodeConfig 支持
#### 8.4 阶段 C 详细步骤
1. 从 `PostDeployPhases` 中移除升级相关 Phase
2. 修改 `PhaseFlow.Execute()` 直接调用 CVO Orchestrator
3. 清理 `BKEClusterStatus` 中的版本字段（迁移到 ClusterVersion）
4. 移除旧升级 Phase 文件
### 九、风险与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 升级过程中状态不一致 | 升级中断后难以恢复 | ClusterVersion CR 持久化升级进度，支持断点续升 |
| DAG 依赖关系变更 | 组件升级顺序错误 | 依赖关系通过 UpgradeConfig CRD 声明式配置，支持运行时更新 |
| 并行升级导致集群不可用 | 业务中断 | KubernetesUpgrader 内部仍串行执行（先 Master 后 Worker） |
| 现有 Phase 逻辑遗漏 | 新 Upgrader 行为不一致 | 阶段 B 充分复用现有函数，通过集成测试验证行为一致性 |
| 回滚失败 | 集群降级 | 回滚前强制 etcd 备份，回滚操作幂等设计 |
### 十、总结
本方案的核心思路是：
1. **引入 ClusterVersion CRD** 作为升级的声明式入口，统一管理所有组件的版本状态
2. **将现有 7 个升级 Phase 映射为 7 个 Upgrader**，每个 Upgrader 负责一个组件的升级逻辑
3. **通过 DAG Scheduler 编排升级顺序**，替代当前硬编码的线性执行
4. **合并 Master/Worker 升级为 KubernetesUpgrader**，内部通过步骤状态机管理执行流程
5. **引入 NodeConfig** 为节点级升级提供声明式配置能力
6. **三阶段渐进式迁移**，通过 Feature Gate 控制切换，确保平滑过渡


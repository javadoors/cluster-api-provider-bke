# KEPU-6: 基于 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 的声明式集群版本管理
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-6 |
| **标题** | 声明式集群版本管理：基于四层 CRD 的安装、升级与扩缩容 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-23 |
| **依赖** | KEPU-1（整体架构重构） |
| **替代** | KEPU-2（YAML 驱动方案）、KEPU-5（CVO 方案）、KEPU-4（PhaseFrame 重构） |
## 1. 摘要
本提案设计基于四个核心 CRD（ClusterVersion、ReleaseImage、ComponentVersion、NodeConfig）的声明式集群版本管理系统，借鉴 OpenShift ClusterVersion Operator 的 ReleaseImage/Payload 模式与 KEPU-4 的 ComponentVersion/NodeConfig 声明式架构，实现 openFuyao 集群版本的安装、升级与扩缩容能力。

**核心设计原则**：
- **ClusterVersion** = 整个集群版本的概念（1:1 对应 BKECluster）
- **ReleaseImage** = 发布版本清单（1:1 对应 ClusterVersion，定义该版本包含哪些组件及版本）
- **ComponentVersion** = 组件清单（含多个版本，定义组件的安装/升级/回滚动作）
- **NodeConfig** = 节点组件清单及配置（定义节点上应安装哪些组件及配置）
- **组件安装、升级与扩缩容由 ComponentVersion 控制器根据配置生成脚本、manifest、chart 等完成，不再调用现有 Phase 代码**
- **PhaseFrame 中的各 Phase 重构为 ComponentVersion 声明式架构（通过 YAML 配置声明，而非 Go 代码实现）**

**与先前提案的关键差异**：

| 维度 | KEPU-2（纯 YAML） | KEPU-5（CVO+Controller） | 本提案（KEPU-6） |
|------|-------------------|--------------------------|-----------------|
| 执行方式 | 仅 YAML Script/Manifest/Chart | Script/Manifest/Helm/Controller 四种 | Script/Manifest/Helm/Controller 四种 |
| 组件安装逻辑 | 全部 YAML 声明 | Controller 类型仍需 Go 代码 | Controller 类型声明式+可插拔 Executor |
| ReleaseImage | CRD 引用 | CRD 引用 | CRD 引用（不可变） |
| 升级卸载旧组件 | 未明确 | 未明确 | 通过 ClusterVersion 找旧 ReleaseImage → 旧 ComponentVersion → uninstallAction |
| Phase 重构 | 全量 YAML 替换 | 部分保留 Go Executor | 渐进式：先声明式定义，再逐步迁移执行逻辑 |
## 2. 动机
### 2.1 现有架构问题
| 问题 | 现状 | 影响 |
|------|------|------|
| **版本概念缺失** | BKECluster 仅记录 KubernetesVersion/EtcdVersion/ContainerdVersion/OpenFuyaoVersion | 无整体版本概念，无法回答"集群当前是什么版本" |
| **发布清单缺失** | 组件版本散落在 BKECluster Spec 各字段 | 无法追溯某个版本包含哪些组件及版本 |
| **命令式编排** | 各 Phase 通过 `NeedExecute` + 固定顺序执行 | 无法并行、无法跳过、无法回滚 |
| **组件独立演进受限** | Phase 之间硬编码依赖，升级路径固定 | 无法独立升级单个组件，无法 A/B 测试 |
| **安装逻辑与编排耦合** | Phase 代码既包含编排逻辑又包含安装逻辑 | 无法复用安装逻辑，无法独立测试 |
| **扩缩容与升级耦合** | EnsureWorkerDelete/EnsureMasterDelete 与升级 Phase 混在一起 | 缩容逻辑无法独立演进 |
| **升级卸载旧组件缺失** | 升级时直接覆盖安装，无旧版本卸载流程 | 旧版本残留文件/配置可能导致冲突 |
### 2.2 OpenShift CVO 的启发
OpenShift 的 Cluster Version Operator（CVO）采用以下架构：
```
ClusterVersion (集群版本)
    └── desiredUpdate.release (Release Image 引用)
            └── Release Payload (容器镜像)
                    └── release-manifests/ (组件 manifest 清单)
```
**借鉴点**：
1. ClusterVersion 作为集群版本的全局入口
2. ReleaseImage 作为版本清单的载体（不可变）
3. 组件 manifest 声明式定义，CVO 自动编排
4. 升级路径显式声明，支持兼容性检查

**差异点**：
1. OpenShift 使用容器镜像作为 Release Payload，我们使用 ReleaseImage CRD
2. OpenShift 的 manifest 是原生 Kubernetes 资源，我们需要支持 Script/Manifest/Helm/Controller 四种执行方式
3. OpenShift 的组件是 Operator，我们的组件包括节点级和集群级两种
4. 我们需要支持升级时先卸载旧组件再安装新组件的流程
## 3. 目标
### 3.1 主要目标
1. 定义 ClusterVersion、ReleaseImage、ComponentVersion、NodeConfig 四个 CRD 及其关联关系
2. 实现 ClusterVersion 控制器：管理集群版本生命周期（安装→升级→回滚）
3. 实现 ReleaseImage 控制器：验证发布清单，生成 ComponentVersion 列表
4. 实现 ComponentVersion 控制器：根据组件配置生成脚本/manifest/chart 并执行
5. 实现 NodeConfig 控制器：管理节点组件的安装/升级/卸载
6. 将现有 PhaseFrame 各 Phase 重构为 ComponentVersion 声明式架构
7. 实现升级时先卸载旧组件再安装新组件的完整流程
### 3.2 非目标
1. 不实现 OpenShift 式的 Release Image 容器镜像载体（使用 CRD 替代）
2. 不实现多集群版本管理（仅单集群）
3. 不实现 OS 级别的版本管理（由 OSProvider 独立负责）
4. 不实现版本包的构建与发布流程（由 CI/CD 独立负责）
5. 不修改现有 BKECluster CRD 的 Spec 定义
## 4. 范围
### 4.1 在范围内
| 范围 | 说明 |
|------|------|
| CRD 定义与注册 | 四个核心 CRD 的 API 定义 |
| 控制器实现 | 四个控制器的 Reconcile 逻辑 |
| ActionEngine | 通用执行引擎，解释执行 YAML 中的 Action 定义 |
| Phase→ComponentVersion 迁移 | 20+ 个 Phase 到 ComponentVersion 的映射与迁移 |
| DAG 调度 | 组件依赖图与调度算法 |
| 版本升级流程 | PreCheck→UninstallOld→Upgrade→PostCheck→Rollback |
| 扩缩容流程 | NodeConfig 增删触发组件安装/卸载 |
| Feature Gate 渐进切换 | 新旧架构双轨运行 |
### 4.2 不在范围内
| 范围 | 原因 |
|------|------|
| 版本包构建 | CI/CD 流程独立 |
| 多集群管理 | 超出单集群版本管理范围 |
| OS 版本管理 | 由 OSProvider 独立负责 |
| UI/CLI 交互 | 仅定义 API，不涉及前端 |
## 5. 约束
| 约束 | 说明 |
|------|------|
| **向后兼容** | 必须支持从现有 PhaseFrame 平滑迁移，不能破坏现有集群 |
| **Feature Gate** | 新架构通过 Feature Gate 开关控制，默认关闭 |
| **单集群单 ClusterVersion** | 每个集群仅允许一个 ClusterVersion 实例 |
| **1:1 ReleaseImage** | 每个 ClusterVersion 仅关联一个 ReleaseImage |
| **ReleaseImage 不可变** | 创建后不可修改，确保版本清单一致性 |
| **组件不可降级** | 默认不允许组件版本降级，除非显式设置 allowDowngrade |
| **离线环境** | 必须支持离线环境，所有资源通过 CRD 定义 |
| **性能** | 控制器 Reconcile 周期不超过 30s，升级单节点不超过 10min |
| **ActionEngine 唯一执行路径** | 不绕过引擎直接操作 |
## 6. 场景
### 6.1 场景一：全新集群安装
```
用户创建 BKECluster
    → BKEClusterReconciler 创建 ClusterVersion（引用 ReleaseImage）
        → ClusterVersion 控制器解析 ReleaseImage，创建 ComponentVersion 列表
            → DAGScheduler 计算安装顺序
                → ComponentVersion 控制器按序执行 installAction
                    → NodeConfig 控制器在对应节点上执行组件安装
```
### 6.2 场景二：集群版本升级（含旧组件卸载）
```
用户更新 ClusterVersion.Spec.DesiredVersion
    → ClusterVersion 控制器检测版本变更
        → 查找新 ReleaseImage → 解析新 ComponentVersion 列表
            → 对比新旧 ReleaseImage 的 ComponentVersion 列表
                → 标记需要升级的 ComponentVersion
                    → DAGScheduler 计算升级顺序
                        → ComponentVersion 控制器按序执行：
                            1. 通过 ClusterVersion 找到旧 ReleaseImage
                            2. 通过旧 ReleaseImage 找到旧 ComponentVersion
                            3. 执行旧 ComponentVersion 的 uninstallAction
                            4. 执行新 ComponentVersion 的 upgradeAction
                            5. 执行健康检查
                        → 升级完成后更新 ClusterVersion.Status
```
### 6.3 场景三：单组件独立升级
```
用户更新 ComponentVersion.Spec.Versions 中的目标版本
    → ComponentVersion 控制器检测版本变更
        → 执行 PreCheck
            → 执行 UpgradeAction
                → 执行 PostCheck
                    → 更新 ComponentVersion.Status
                        → 更新 ClusterVersion.Status 中的组件版本
```
### 6.4 场景四：节点扩容
```
用户在 BKECluster.Spec 中添加新节点
    → BKEClusterReconciler 创建新 NodeConfig
        → NodeConfig 控制器检测到新节点
            → 根据 NodeConfig.Spec.Components 引用 ComponentVersion
                → ComponentVersion 控制器在新节点上执行 installAction
```
### 6.5 场景五：节点缩容
```
用户从 BKECluster.Spec 中删除节点
    → BKEClusterReconciler 标记 NodeConfig Phase=Deleting
        → NodeConfig 控制器执行节点组件卸载（nodeDelete uninstallAction）
            → 删除 NodeConfig 资源
```
### 6.6 场景六：升级回滚
```
ComponentVersion 升级失败
    → ComponentVersion 控制器标记 Phase=UpgradeFailed
        → ClusterVersion 控制器检测到失败
            → 根据 UpgradeStrategy.AutoRollback 决定是否自动回滚
                → ComponentVersion 控制器执行 RollbackAction
                    → 回滚到上一个已知良好版本
```
### 6.7 场景七：纳管现有集群
```
用户创建 BKECluster（spec.manageMode=Import）
    → ClusterVersion 控制器创建 clusterManage ComponentVersion
        → ComponentVersion 控制器执行 installAction
            → 收集集群信息 → 推送 Agent → 伪引导 → 兼容性补丁
```
## 7. 提案
### 7.1 资源关联关系
```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  (集群实例，1:1 对应 ClusterVersion)                            │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 1:1
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  (集群版本，记录当前版本/目标版本/升级历史)                      │
│                                                                  │
│  spec.releaseRef ──────────┐                                     │
│  spec.desiredVersion       │                                     │
│  status.currentVersion     │                                     │
│  status.currentReleaseRef ─┼─────┐                               │
└────────────────────────────┼─────┼───────────────────────────────┘
                             │ 1:1 │
                             ▼     │
┌────────────────────────────────────────────────────────────────┐
│                       ReleaseImage                             │
│  (发布版本清单，不可变，定义该版本包含哪些组件及版本)          │
│                                                                │
│  spec.components:                                              │
│    - name: etcd        ────────┐                               │
│      version: v3.5.12          │                               │
│    - name: containerd  ────┐   │                               │
│      version: v1.7.2       │   │                               │
│    - name: kubernetes  ─┐  │   │                               │
│      version: v1.29.0   │  │   │                               │
└─────────────────────────┼──┼───┼───────────────────────────────┘
                          │  │   │
                          │  │   │  N:1 (多个 ReleaseImage 可引用同一 ComponentVersion)
                          ▼  ▼   ▼
┌──────────────────────────────────────────────────────────────────┐
│                    ComponentVersion                              │
│  (组件清单，含多个版本，定义安装/升级/回滚/卸载动作)             │
│                                                                  │
│  spec.componentName: etcd                                        │
│  spec.versions:                                                  │
│    - version: v3.5.11                                            │
│      installAction: {...}                                        │
│      uninstallAction: {...}                                      │
│    - version: v3.5.12                                            │
│      installAction: {...}                                        │
│      upgradeFrom:                                                │
│        - version: v3.5.11                                        │
│          upgradeAction: {...}                                    │
│      uninstallAction: {...}                                      │
│  spec.scope: Node                                                │
└──────────────────────────┬───────────────────────────────────────┘
                           │ 1:N (ComponentVersion 被 NodeConfig 引用)
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                       NodeConfig                                 │
│  (节点组件清单及配置，定义节点上应安装哪些组件)                  │
│                                                                  │
│  spec.nodeName: node-1                                           │
│  spec.roles: [master, etcd]                                      │
│  spec.components:                                                │
│    - componentName: etcd                                         │
│      version: v3.5.12                                            │
│      config:                                                     │
│        dataDir: /var/lib/etcd                                    │
│    - componentName: containerd                                   │
│      version: v1.7.2                                             │
│      config:                                                     │
│        dataDir: /var/lib/containerd                              │
└──────────────────────────────────────────────────────────────────┘
```
**关联关系总结**：
- **BKECluster → ClusterVersion**：1:1，一个集群对应一个版本资源
- **ClusterVersion → ReleaseImage**：1:1（当前版本）+ 1:1（目标版本），通过 `status.currentReleaseRef` 和 `spec.releaseRef` 引用
- **ReleaseImage → ComponentVersion**：1:N，一个发布清单包含多个组件引用
- **NodeConfig → ComponentVersion**：N:N，多个节点引用多个组件
### 7.2 ClusterVersion CRD
借鉴 OpenShift `config.openshift.io/v1.ClusterVersion`，使用 ReleaseImage CRD 引用替代 Release Image 容器镜像。
```go
// api/cvo/v1beta1/clusterversion_types.go

type ClusterVersionSpec struct {
    DesiredVersion string           `json:"desiredVersion"`
    ReleaseRef     ReleaseReference `json:"releaseRef"`
    ClusterRef     *ClusterReference `json:"clusterRef,omitempty"`
    UpgradeStrategy UpgradeStrategy `json:"upgradeStrategy,omitempty"`
    Pause          bool             `json:"pause,omitempty"`
    AllowDowngrade bool             `json:"allowDowngrade,omitempty"`
}

type ReleaseReference struct {
    Name    string `json:"name"`
    Version string `json:"version,omitempty"`
}

type ClusterReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
}

type UpgradeStrategy struct {
    Type            UpgradeStrategyType  `json:"type,omitempty"`
    MaxUnavailable  *intstr.IntOrString  `json:"maxUnavailable,omitempty"`
    PreCheck        *PreCheckSpec        `json:"preCheck,omitempty"`
    PostCheck       *PostCheckSpec       `json:"postCheck,omitempty"`
    AutoRollback    bool                 `json:"autoRollback,omitempty"`
    RollbackTimeout *metav1.Duration     `json:"rollbackTimeout,omitempty"`
}

type UpgradeStrategyType string

const (
    RollingUpdateStrategy UpgradeStrategyType = "RollingUpdate"
    InPlaceStrategy       UpgradeStrategyType = "InPlace"
    RecreateStrategy      UpgradeStrategyType = "Recreate"
)

type ClusterVersionStatus struct {
    CurrentVersion    string              `json:"currentVersion,omitempty"`
    CurrentReleaseRef *ReleaseReference   `json:"currentReleaseRef,omitempty"`
    CurrentComponents ComponentVersionRefs `json:"currentComponents,omitempty"`
    Phase             ClusterVersionPhase `json:"phase,omitempty"`
    UpgradeSteps      []UpgradeStep       `json:"upgradeSteps,omitempty"`
    CurrentStepIndex  int                 `json:"currentStepIndex,omitempty"`
    History           []UpgradeHistory    `json:"history,omitempty"`
    Conditions        []metav1.Condition  `json:"conditions,omitempty"`
}

type ClusterVersionPhase string

const (
    ClusterVersionInstalling    ClusterVersionPhase = "Installing"
    ClusterVersionInstalled     ClusterVersionPhase = "Installed"
    ClusterVersionUpgrading     ClusterVersionPhase = "Upgrading"
    ClusterVersionUpgradeFailed ClusterVersionPhase = "UpgradeFailed"
    ClusterVersionRollingBack   ClusterVersionPhase = "RollingBack"
    ClusterVersionRolledBack    ClusterVersionPhase = "RolledBack"
    ClusterVersionHealthy       ClusterVersionPhase = "Healthy"
    ClusterVersionDegraded      ClusterVersionPhase = "Degraded"
)

type UpgradeStep struct {
    ComponentName ComponentName    `json:"componentName"`
    Version       string           `json:"version"`
    Phase         UpgradeStepPhase `json:"phase,omitempty"`
    Message       string           `json:"message,omitempty"`
    StartedAt     *metav1.Time     `json:"startedAt,omitempty"`
    CompletedAt   *metav1.Time     `json:"completedAt,omitempty"`
}

type UpgradeStepPhase string

const (
    UpgradeStepPending    UpgradeStepPhase = "Pending"
    UpgradeStepPreCheck   UpgradeStepPhase = "PreCheck"
    UpgradeStepUpgrading  UpgradeStepPhase = "Upgrading"
    UpgradeStepPostCheck  UpgradeStepPhase = "PostCheck"
    UpgradeStepCompleted  UpgradeStepPhase = "Completed"
    UpgradeStepFailed     UpgradeStepPhase = "Failed"
    UpgradeStepSkipped    UpgradeStepPhase = "Skipped"
)

type UpgradeHistory struct {
    FromVersion   string      `json:"fromVersion"`
    ToVersion     string      `json:"toVersion"`
    StartedAt     metav1.Time `json:"startedAt"`
    CompletedAt   *metav1.Time `json:"completedAt,omitempty"`
    Result        string      `json:"result,omitempty"`
    FailureReason string      `json:"failureReason,omitempty"`
}

type ComponentVersionRefs map[ComponentName]string
```
### 7.3 ReleaseImage CRD
借鉴 OpenShift Release Payload 概念，使用 CRD 替代容器镜像载体。**ReleaseImage 创建后不可变**。
```go
// api/cvo/v1beta1/releaseimage_types.go

type ReleaseImageSpec struct {
    Version       string              `json:"version"`
    DisplayName   string              `json:"displayName,omitempty"`
    Description   string              `json:"description,omitempty"`
    ReleaseTime   *metav1.Time        `json:"releaseTime,omitempty"`
    Components    []ReleaseComponent  `json:"components"`
    Compatibility *ReleaseCompatibility `json:"compatibility,omitempty"`
    UpgradePaths  []UpgradePath       `json:"upgradePaths,omitempty"`
}

type ReleaseComponent struct {
    ComponentName       ComponentName             `json:"componentName"`
    Version             string                    `json:"version"`
    ComponentVersionRef *ComponentVersionReference `json:"componentVersionRef,omitempty"`
    Mandatory           bool                      `json:"mandatory,omitempty"`
}

type ComponentVersionReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace,omitempty"`
}

type ReleaseCompatibility struct {
    MinKubernetesVersion string         `json:"minKubernetesVersion,omitempty"`
    MaxKubernetesVersion string         `json:"maxKubernetesVersion,omitempty"`
    MinOpenFuyaoVersion  string         `json:"minOpenFuyaoVersion,omitempty"`
    OSRequirements       []OSRequirement `json:"osRequirements,omitempty"`
}

type UpgradePath struct {
    FromVersion string `json:"fromVersion"`
    ToVersion   string `json:"toVersion"`
    Blocked     bool   `json:"blocked,omitempty"`
    Reason      string `json:"reason,omitempty"`
}

type ReleaseImageStatus struct {
    Phase               ReleaseImagePhase   `json:"phase,omitempty"`
    ValidatedComponents []ValidatedComponent `json:"validatedComponents,omitempty"`
    ValidationErrors    []string            `json:"validationErrors,omitempty"`
    Conditions          []metav1.Condition  `json:"conditions,omitempty"`
}

type ReleaseImagePhase string

const (
    ReleaseImageValid     ReleaseImagePhase = "Valid"
    ReleaseImageInvalid   ReleaseImagePhase = "Invalid"
    ReleaseImageProcessing ReleaseImagePhase = "Processing"
)
```
### 7.4 ComponentVersion CRD
继承 KEPU-4 设计，重构 Action 定义，支持 Script/Manifest/Helm/Controller 四种执行方式。**核心变化**：每个 ComponentVersion 包含多版本条目，每个版本条目定义独立的安装/升级/卸载动作。
```go
// api/cvo/v1beta1/componentversion_types.go

type ComponentVersionSpec struct {
    ComponentName ComponentName          `json:"componentName"`
    Scope         ComponentScope         `json:"scope,omitempty"`
    Versions      []ComponentVersionEntry `json:"versions"`
    Dependencies  []ComponentDependency  `json:"dependencies,omitempty"`
    HealthCheck   *HealthCheckSpec       `json:"healthCheck,omitempty"`
}

type ComponentScope string

const (
    ScopeCluster ComponentScope = "Cluster"
    ScopeNode    ComponentScope = "Node"
)

type ComponentVersionEntry struct {
    Version         string              `json:"version"`
    InstallAction   *ActionSpec         `json:"installAction,omitempty"`
    UpgradeFrom     []UpgradeActionEntry `json:"upgradeFrom,omitempty"`
    RollbackAction  *ActionSpec         `json:"rollbackAction,omitempty"`
    UninstallAction *ActionSpec         `json:"uninstallAction,omitempty"`
    PreCheck        *ActionSpec         `json:"preCheck,omitempty"`
    PostCheck       *ActionSpec         `json:"postCheck,omitempty"`
    Compatibility   *VersionCompatibility `json:"compatibility,omitempty"`
}

type UpgradeActionEntry struct {
    FromVersion string     `json:"fromVersion"`
    Action      *ActionSpec `json:"action"`
}

type ActionSpec struct {
    Type        ActionType     `json:"type"`
    Script      *ScriptAction  `json:"script,omitempty"`
    Manifest    *ManifestAction `json:"manifest,omitempty"`
    Helm        *HelmAction    `json:"helm,omitempty"`
    Controller  *ControllerAction `json:"controller,omitempty"`
    Timeout     *metav1.Duration `json:"timeout,omitempty"`
    RetryPolicy *RetryPolicy   `json:"retryPolicy,omitempty"`
}

type ActionType string

const (
    ActionScript     ActionType = "Script"
    ActionManifest   ActionType = "Manifest"
    ActionHelm       ActionType = "Helm"
    ActionController ActionType = "Controller"
)

type ScriptAction struct {
    Source      ScriptSource           `json:"source"`
    Inline      string                 `json:"inline,omitempty"`
    ConfigMapRef *ConfigMapKeySelector  `json:"configMapRef,omitempty"`
    SecretRef   *SecretKeySelector     `json:"secretRef,omitempty"`
    Args        []string               `json:"args,omitempty"`
    Env         []EnvVar               `json:"env,omitempty"`
}

type ManifestAction struct {
    Source      ManifestSource          `json:"source"`
    Inline      string                  `json:"inline,omitempty"`
    ConfigMapRef *ConfigMapKeySelector  `json:"configMapRef,omitempty"`
    URL         string                  `json:"url,omitempty"`
}

type HelmAction struct {
    ChartRef    *HelmChartRef `json:"chartRef,omitempty"`
    Values      *HelmValues   `json:"values,omitempty"`
    ReleaseName string        `json:"releaseName,omitempty"`
    Namespace   string        `json:"namespace,omitempty"`
}

type ControllerAction struct {
    Executor string            `json:"executor"`
    Params   map[string]string `json:"params,omitempty"`
}

type RetryPolicy struct {
    MaxRetries    int              `json:"maxRetries,omitempty"`
    RetryInterval *metav1.Duration `json:"retryInterval,omitempty"`
    RetryOnFailed bool             `json:"retryOnFailed,omitempty"`
}

type ComponentDependency struct {
    ComponentName    ComponentName   `json:"componentName"`
    VersionConstraint string         `json:"versionConstraint,omitempty"`
    Phase            DependencyPhase `json:"phase,omitempty"`
}

type DependencyPhase string

const (
    DependencyInstall DependencyPhase = "Install"
    DependencyUpgrade DependencyPhase = "Upgrade"
    DependencyAll     DependencyPhase = "All"
)

type ComponentVersionStatus struct {
    Phase            ComponentPhase              `json:"phase,omitempty"`
    InstalledVersion string                      `json:"installedVersion,omitempty"`
    DesiredVersion   string                      `json:"desiredVersion,omitempty"`
    NodeStatuses     map[string]NodeComponentStatus `json:"nodeStatuses,omitempty"`
    LastOperation    *LastOperation              `json:"lastOperation,omitempty"`
    Message          string                      `json:"message,omitempty"`
    Conditions       []metav1.Condition          `json:"conditions,omitempty"`
}

type ComponentPhase string

const (
    ComponentPending       ComponentPhase = "Pending"
    ComponentInstalling    ComponentPhase = "Installing"
    ComponentInstalled     ComponentPhase = "Installed"
    ComponentUpgrading     ComponentPhase = "Upgrading"
    ComponentUpgradeFailed ComponentPhase = "UpgradeFailed"
    ComponentRollingBack   ComponentPhase = "RollingBack"
    ComponentRolledBack    ComponentPhase = "RolledBack"
    ComponentUninstalling  ComponentPhase = "Uninstalling"
    ComponentUninstalled   ComponentPhase = "Uninstalled"
    ComponentHealthy       ComponentPhase = "Healthy"
    ComponentDegraded      ComponentPhase = "Degraded"
)
```
### 7.5 NodeConfig CRD
```go
// api/cvo/v1beta1/nodeconfig_types.go

type NodeConfigSpec struct {
    NodeName   string          `json:"nodeName"`
    NodeIP     string          `json:"nodeIP,omitempty"`
    ClusterRef *ClusterReference `json:"clusterRef,omitempty"`
    Roles      []NodeRole      `json:"roles"`
    Connection NodeConnection  `json:"connection,omitempty"`
    OS         NodeOSInfo      `json:"os,omitempty"`
    Components []NodeComponent `json:"components,omitempty"`
}

type NodeRole string

const (
    NodeRoleMaster NodeRole = "master"
    NodeRoleWorker NodeRole = "worker"
    NodeRoleEtcd   NodeRole = "etcd"
)

type NodeComponent struct {
    ComponentName       ComponentName             `json:"componentName"`
    Version             string                    `json:"version"`
    Config              *runtime.RawExtension     `json:"config,omitempty"`
    ComponentVersionRef *ComponentVersionReference `json:"componentVersionRef,omitempty"`
}

type NodeConfigStatus struct {
    Phase           NodeConfigPhase                `json:"phase,omitempty"`
    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`
    OSInfo          *NodeOSDetailInfo              `json:"osInfo,omitempty"`
    LastOperation   *LastOperation                 `json:"lastOperation,omitempty"`
    Conditions      []metav1.Condition             `json:"conditions,omitempty"`
}

type NodeConfigPhase string

const (
    NodeConfigPending      NodeConfigPhase = "Pending"
    NodeConfigInstalling   NodeConfigPhase = "Installing"
    NodeConfigInstalled    NodeConfigPhase = "Installed"
    NodeConfigUpgrading    NodeConfigPhase = "Upgrading"
    NodeConfigUninstalling NodeConfigPhase = "Uninstalling"
    NodeConfigDeleting     NodeConfigPhase = "Deleting"
    NodeConfigDeleted      NodeConfigPhase = "Deleted"
    NodeConfigReady        NodeConfigPhase = "Ready"
    NodeConfigNotReady     NodeConfigPhase = "NotReady"
)
```
### 7.6 ComponentName 枚举
```go
type ComponentName string

const (
    ComponentBKEAgent      ComponentName = "bkeAgent"
    ComponentNodesEnv      ComponentName = "nodesEnv"
    ComponentContainerd    ComponentName = "containerd"
    ComponentEtcd          ComponentName = "etcd"
    ComponentKubernetes    ComponentName = "kubernetes"
    ComponentClusterAPI    ComponentName = "clusterAPI"
    ComponentCerts         ComponentName = "certs"
    ComponentLoadBalancer  ComponentName = "loadBalancer"
    ComponentAddon         ComponentName = "addon"
    ComponentOpenFuyao     ComponentName = "openFuyao"
    ComponentBKEProvider   ComponentName = "bkeProvider"
    ComponentNodesPostProc ComponentName = "nodesPostProcess"
    ComponentAgentSwitch   ComponentName = "agentSwitch"
    ComponentClusterManage ComponentName = "clusterManage"
    ComponentNodeDelete    ComponentName = "nodeDelete"
    ComponentClusterHealth ComponentName = "clusterHealth"
)
```
### 7.7 控制器设计
#### 7.7.1 控制器总览与关系
```
┌─────────────────────────────────────────────────────────────────────┐
│                     BKEClusterReconciler                            │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │ 控制类逻辑（保留在 Controller 中）                            │  │
│  │  ├── EnsureFinalizer                                         │  │
│  │  ├── EnsurePaused                                            │  │
│  │  ├── EnsureDeleteOrReset                                     │  │
│  │  └── EnsureDryRun                                            │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                              │                                      │
│                              ▼                                      │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │              ClusterVersion Controller                        │  │
│  │  作用：集群版本生命周期管理                                    │  │
│  │  ┌─────────────────────────────────────────────────────────┐  │  │
│  │  │ 1. 检测 desiredVersion 变更                              │  │  │
│  │  │ 2. 解析 ReleaseImage → ComponentVersion 列表             │  │  │
│  │  │ 3. DAG 调度 → 按序更新 ComponentVersion                  │  │  │
│  │  │ 4. 监控升级进度                                          │  │  │
│  │  │ 5. 升级失败时触发回滚                                    │  │  │
│  │  └─────────────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                    │                    │                             │
│                    ▼                    ▼                             │
│  ┌─────────────────────────┐  ┌──────────────────────────────────┐ │
│  │ ReleaseImage Controller │  │  ComponentVersion Controller     │ │
│  │ 作用：发布清单验证       │  │  作用：组件生命周期驱动          │ │
│  │ ┌─────────────────────┐ │  │ ┌──────────────────────────────┐ │ │
│  │ │1.验证组件引用存在   │ │  │ │1.检测版本变更               │ │ │
│  │ │2.验证版本兼容性     │ │  │ │2.查找旧版本→执行uninstall   │ │ │
│  │ │3.验证升级路径合法性 │ │  │ │3.执行install/upgradeAction  │ │ │
│  │ │4.更新验证状态       │ │  │ │4.执行健康检查               │ │ │
│  │ └─────────────────────┘ │  │ │5.升级失败→执行rollbackAction│ │ │
│  └─────────────────────────┘  │ └──────────────────────────────┘ │ │
│                               └──────────────────────────────────┘ │
│                                             │                       │
│                                             ▼                       │
│                               ┌──────────────────────────────────┐ │
│                               │    NodeConfig Controller         │ │
│                               │ 作用：节点组件管理               │ │
│                               │ ┌──────────────────────────────┐ │ │
│                               │ │1.监听节点增删                 │ │ │
│                               │ │2.触发组件安装/卸载            │ │ │
│                               │ │3.更新节点组件状态             │ │ │
│                               │ └──────────────────────────────┘ │ │
│                               └──────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
```
#### 7.7.2 ClusterVersion 控制器
**作用**：集群版本生命周期管理，是版本编排的入口控制器。

**设计思路**：
1. ClusterVersion 是 BKECluster 与组件系统之间的桥梁
2. 通过 ReleaseImage 解耦版本定义与集群实例
3. 通过 DAG 调度实现组件有序升级
4. 通过 status.upgradeSteps 跟踪升级进度

**Reconcile 逻辑**：
```
Reconcile(ctx, req):
    1. 获取 ClusterVersion 实例
    2. 如果 spec.pause == true，返回
    3. 获取关联的 ReleaseImage
    4. 对比 spec.desiredVersion 与 status.currentVersion
    5. 如果版本相同且 phase=Healthy，执行健康检查后返回
    6. 如果版本不同（升级场景）：
       a. 验证升级路径（从 ReleaseImage.spec.upgradePaths）
       b. 计算需要变更的 ComponentVersion 列表（对比新旧 ReleaseImage）
       c. 按 DAG 顺序创建/更新 ComponentVersion
       d. 生成 status.upgradeSteps
       e. 监控各 ComponentVersion 状态
    7. 如果所有 ComponentVersion 完成：
       a. 更新 status.currentVersion = spec.desiredVersion
       b. 更新 status.currentReleaseRef = spec.releaseRef
       c. 更新 status.phase = Healthy
       d. 记录 upgradeHistory
    8. 如果有 ComponentVersion 失败：
       a. 根据 upgradeStrategy.autoRollback 决定是否回滚
       b. 更新 status.phase = UpgradeFailed/RollingBack
```
**代码实现**：
```go
// controllers/cvo/clusterversion_controller.go

type ClusterVersionReconciler struct {
    client.Client
    Scheme        *runtime.Scheme
    dagScheduler  *DAGScheduler
    actionEngine  *ActionEngine
}

func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvo.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if cv.Spec.Pause {
        return ctrl.Result{}, nil
    }

    release := &cvo.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      cv.Spec.ReleaseRef.Name,
        Namespace: cv.Namespace,
    }, release); err != nil {
        return ctrl.Result{}, fmt.Errorf("get release image: %w", err)
    }

    if cv.Status.CurrentVersion == "" {
        return r.handleInstall(ctx, cv, release)
    }

    if cv.Spec.DesiredVersion != cv.Status.CurrentVersion {
        return r.handleUpgrade(ctx, cv, release)
    }

    return r.handleHealthCheck(ctx, cv)
}

func (r *ClusterVersionReconciler) handleUpgrade(
    ctx context.Context,
    cv *cvo.ClusterVersion,
    newRelease *cvo.ReleaseImage,
) (ctrl.Result, error) {
    if cv.Status.Phase != cvo.ClusterVersionUpgrading {
        if err := r.validateUpgradePath(ctx, cv, newRelease); err != nil {
            cv.Status.Phase = cvo.ClusterVersionUpgradeFailed
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{}, err
        }

        oldRelease := &cvo.ReleaseImage{}
        if cv.Status.CurrentReleaseRef != nil {
            _ = r.Get(ctx, types.NamespacedName{
                Name:      cv.Status.CurrentReleaseRef.Name,
                Namespace: cv.Namespace,
            }, oldRelease)
        }

        upgradeSteps := r.computeUpgradeSteps(oldRelease, newRelease)
        cv.Status.UpgradeSteps = upgradeSteps
        cv.Status.CurrentStepIndex = 0
        cv.Status.Phase = cvo.ClusterVersionUpgrading
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{Requeue: true}, nil
    }

    return r.processUpgradeSteps(ctx, cv, newRelease)
}

func (r *ClusterVersionReconciler) processUpgradeSteps(
    ctx context.Context,
    cv *cvo.ClusterVersion,
    newRelease *cvo.ReleaseImage,
) (ctrl.Result, error) {
    for i := cv.Status.CurrentStepIndex; i < len(cv.Status.UpgradeSteps); i++ {
        step := &cv.Status.UpgradeSteps[i]

        comp := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      string(step.ComponentName),
            Namespace: cv.Namespace,
        }, comp); err != nil {
            return ctrl.Result{}, fmt.Errorf("get component %s: %w", step.ComponentName, err)
        }

        if !r.dependenciesReady(ctx, cv, step.ComponentName) {
            step.Phase = cvo.UpgradeStepPending
            continue
        }

        switch comp.Status.Phase {
        case cvo.ComponentHealthy, cvo.ComponentInstalled:
            if comp.Status.InstalledVersion == step.Version {
                step.Phase = cvo.UpgradeStepCompleted
                cv.Status.CurrentStepIndex = i + 1
                _ = r.Status().Update(ctx, cv)
                continue
            }
            step.Phase = cvo.UpgradeStepUpgrading
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

        case cvo.ComponentUpgradeFailed:
            step.Phase = cvo.UpgradeStepFailed
            if cv.Spec.UpgradeStrategy.AutoRollback {
                cv.Status.Phase = cvo.ClusterVersionRollingBack
            } else {
                cv.Status.Phase = cvo.ClusterVersionUpgradeFailed
            }
            _ = r.Status().Update(ctx, cv)
            return ctrl.Result{}, nil

        default:
            return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
        }
    }

    cv.Status.CurrentVersion = cv.Spec.DesiredVersion
    cv.Status.CurrentReleaseRef = &cvo.ReleaseReference{
        Name:    cv.Spec.ReleaseRef.Name,
        Version: cv.Spec.ReleaseRef.Version,
    }
    cv.Status.Phase = cvo.ClusterVersionHealthy
    cv.Status.History = append(cv.Status.History, cvo.UpgradeHistory{...})
    _ = r.Status().Update(ctx, cv)
    return ctrl.Result{}, nil
}
```
#### 7.7.3 ReleaseImage 控制器
**作用**：发布清单验证，确保 ReleaseImage 引用的所有 ComponentVersion 存在且版本兼容。

**设计思路**：
1. ReleaseImage 是不可变的版本清单，控制器仅做验证
2. 验证所有引用的 ComponentVersion 是否存在
3. 验证组件版本兼容性
4. 验证升级路径合法性

**Reconcile 逻辑**：
```
Reconcile(ctx, req):
    1. 获取 ReleaseImage 实例
    2. 遍历 spec.components：
       a. 查找 ComponentVersion CR
       b. 验证指定版本存在于 ComponentVersion.spec.versions 中
       c. 记录验证结果到 status.validatedComponents
    3. 验证 spec.compatibility 中的版本约束
    4. 验证 spec.upgradePaths 中的升级路径
    5. 更新 status.phase = Valid/Invalid
    6. 更新 status.validationErrors
```
#### 7.7.4 ComponentVersion 控制器
**作用**：组件生命周期驱动，是整个系统的核心执行控制器。

**设计思路**：
1. ComponentVersion 是声明式组件定义，控制器负责将声明转化为实际操作
2. 支持四种执行方式：Script/Manifest/Helm/Controller
3. **升级时先卸载旧组件**：通过 ClusterVersion 找到旧 ReleaseImage → 旧 ComponentVersion → uninstallAction
4. 升级失败时执行 rollbackAction
5. 删除 CR 时通过 Finalizer 执行 uninstallAction

**Reconcile 逻辑**：
```
Reconcile(ctx, req):
    1. 获取 ComponentVersion 实例
    2. 确定 desiredVersion（从 ReleaseImage 引用或 NodeConfig 引用）
    3. 根据 status.phase 决定执行路径：

    case Pending:
        a. 检查依赖组件是否就绪
        b. 查找旧版本 ComponentVersion（通过 ClusterVersion.currentReleaseRef）
        c. 如果旧版本存在且有 uninstallAction → 执行旧版本卸载
        d. 执行 PreCheck Action
        e. 执行 InstallAction
        f. 执行 PostCheck Action
        g. 更新 status.phase = Healthy

    case Healthy:
        a. 如果 desiredVersion != installedVersion → 转入 Upgrading
        b. 否则执行周期性健康检查

    case Upgrading:
        a. 查找匹配的 UpgradeAction（从 upgradeFrom 列表）
        b. 查找旧版本 ComponentVersion → 执行 uninstallAction
        c. 执行 PreCheck Action
        d. 执行 UpgradeAction
        e. 执行 PostCheck Action
        f. 更新 status.installedVersion = desiredVersion
        g. 更新 status.phase = Healthy

    case UpgradeFailed:
        a. 如果有 rollbackAction → 执行回滚
        b. 更新 status.phase = RolledBack/Failed

    case Uninstalling (Finalizer):
        a. 执行当前版本的 uninstallAction
        b. 移除 Finalizer
```
**升级时卸载旧组件的核心代码**：
```go
// controllers/cvo/componentversion_controller.go

func (r *ComponentVersionReconciler) findOldComponentVersion(
    ctx context.Context,
    cv *cvo.ComponentVersion,
) (*cvo.ComponentVersion, error) {
    clusterVersions := &cvo.ClusterVersionList{}
    if err := r.List(ctx, clusterVersions,
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": cv.Labels["cluster.x-k8s.io/cluster-name"]},
    ); err != nil {
        return nil, err
    }

    for _, clusterVer := range clusterVersions.Items {
        if clusterVer.Status.CurrentReleaseRef == nil {
            continue
        }

        oldRelease := &cvo.ReleaseImage{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      clusterVer.Status.CurrentReleaseRef.Name,
            Namespace: cv.Namespace,
        }, oldRelease); err != nil {
            continue
        }

        for _, comp := range oldRelease.Spec.Components {
            if comp.ComponentName == cv.Spec.ComponentName {
                if comp.ComponentVersionRef != nil {
                    oldCV := &cvo.ComponentVersion{}
                    if err := r.Get(ctx, types.NamespacedName{
                        Name:      comp.ComponentVersionRef.Name,
                        Namespace: comp.ComponentVersionRef.Namespace,
                    }, oldCV); err == nil {
                        return oldCV, nil
                    }
                }
            }
        }
    }
    return nil, nil
}

func (r *ComponentVersionReconciler) uninstallOldVersion(
    ctx context.Context,
    cv *cvo.ComponentVersion,
    nodeConfigs []*cvo.NodeConfig,
) error {
    oldCV, err := r.findOldComponentVersion(ctx, cv)
    if err != nil || oldCV == nil {
        return nil
    }

    oldEntry := r.findVersionEntry(oldCV, cv.Status.InstalledVersion)
    if oldEntry == nil || oldEntry.UninstallAction == nil {
        return nil
    }

    return r.actionEngine.ExecuteAction(ctx, oldEntry.UninstallAction, oldCV, nodeConfigs)
}
```
#### 7.7.5 NodeConfig 控制器
**作用**：节点组件管理，负责节点级组件的安装/升级/卸载。

**设计思路**：
1. NodeConfig 是节点与组件系统之间的桥梁
2. 通过 NodeConfig 的 components 列表确定节点应安装哪些组件
3. NodeConfig 删除时触发缩容流程
4. 节点角色变更时自动更新组件列表

**Reconcile 逻辑**：
```
Reconcile(ctx, req):
    1. 获取 NodeConfig 实例
    2. 如果 phase=Deleting：
       a. 查找 nodeDelete ComponentVersion
       b. 执行 uninstallAction（drain → delete machine → clean）
       c. 删除 NodeConfig
    3. 遍历 spec.components：
       a. 查找关联的 ComponentVersion
       b. 更新 ComponentVersion 的 nodeStatuses
       c. 如果组件版本不匹配，触发安装/升级
    4. 更新 status.componentStatus
    5. 更新 status.phase
```
### 7.8 组件依赖 DAG
```go
var InstallDependencyGraph = map[ComponentName][]ComponentName{
    ComponentBKEAgent:      {},
    ComponentNodesEnv:      {ComponentBKEAgent},
    ComponentClusterAPI:    {ComponentBKEAgent},
    ComponentCerts:         {ComponentClusterAPI},
    ComponentLoadBalancer:  {ComponentCerts},
    ComponentContainerd:    {ComponentNodesEnv},
    ComponentEtcd:          {ComponentCerts, ComponentNodesEnv},
    ComponentKubernetes:    {ComponentContainerd, ComponentEtcd, ComponentLoadBalancer},
    ComponentAddon:         {ComponentKubernetes},
    ComponentNodesPostProc: {ComponentAddon},
    ComponentAgentSwitch:   {ComponentNodesPostProc},
    ComponentBKEProvider:   {ComponentNodesPostProc},
    ComponentOpenFuyao:     {ComponentKubernetes},
    ComponentClusterManage: {},
    ComponentNodeDelete:    {},
    ComponentClusterHealth: {ComponentKubernetes, ComponentAddon, ComponentOpenFuyao},
}

var UpgradeDependencyGraph = map[ComponentName][]ComponentName{
    ComponentBKEProvider:   {},
    ComponentBKEAgent:      {ComponentBKEProvider},
    ComponentContainerd:    {ComponentBKEAgent},
    ComponentEtcd:          {ComponentBKEAgent},
    ComponentKubernetes:    {ComponentContainerd, ComponentEtcd},
    ComponentOpenFuyao:     {ComponentKubernetes},
    ComponentAddon:         {ComponentKubernetes},
    ComponentNodesPostProc: {ComponentAddon},
    ComponentAgentSwitch:   {ComponentNodesPostProc},
}
```
### 7.9 Phase→ComponentVersion 迁移映射表
| Phase | ComponentName | Scope | ActionType | installAction | upgradeAction |
|-------|---------------|-------|------------|---------------|---------------|
| EnsureBKEAgent | bkeAgent | Node | Controller | Executor:bkeAgent | Executor:bkeAgent |
| EnsureNodesEnv | nodesEnv | Node | Script | Inline: env install script | Inline: env update script |
| EnsureClusterAPIObj | clusterAPI | Cluster | Controller | Executor:clusterAPI | Executor:clusterAPI |
| EnsureCerts | certs | Cluster | Controller | Executor:certs | Executor:certs |
| EnsureLoadBalance | loadBalancer | Node | Script | Inline: haproxy install | Inline: haproxy update |
| EnsureMasterInit | kubernetes | Node | Controller | Executor:kubernetes-init | - |
| EnsureMasterJoin | kubernetes | Node | Controller | Executor:kubernetes-join | - |
| EnsureWorkerJoin | kubernetes | Node | Controller | Executor:kubernetes-join | - |
| EnsureMasterUpgrade | kubernetes | Node | Controller | - | Executor:kubernetes-upgrade |
| EnsureWorkerUpgrade | kubernetes | Node | Controller | - | Executor:kubernetes-upgrade |
| EnsureContainerdUpgrade | containerd | Node | Script | Inline: containerd install | Inline: reset+redeploy |
| EnsureEtcdUpgrade | etcd | Node | Controller | Executor:etcd | Executor:etcd |
| EnsureAddonDeploy | addon | Cluster | Helm | ChartRef: addon chart | ChartRef: addon chart |
| EnsureAgentSwitch | agentSwitch | Cluster | Controller | Executor:agentSwitch | - |
| EnsureProviderSelfUpgrade | bkeProvider | Cluster | Controller | Executor:bkeProvider | Executor:bkeProvider |
| EnsureComponentUpgrade | openFuyao | Cluster | Controller | Executor:openFuyao | Executor:openFuyao |
| EnsureNodesPostProcess | nodesPostProcess | Node | Script | Inline: post-process script | Inline: post-process script |
| EnsureClusterManage | clusterManage | Cluster | Controller | Executor:clusterManage | - |
| EnsureWorkerDelete | nodeDelete | Node | Script | - | - (uninstallAction) |
| EnsureMasterDelete | nodeDelete | Node | Script | - | - (uninstallAction) |
| EnsureCluster | clusterHealth | Cluster | Controller | - | - (healthCheck only) |

**不映射为 ComponentVersion 的 Phase（5 个）**：

| Phase | 归属 | 原因 |
|-------|------|------|
| EnsureFinalizer | ClusterVersion Controller | 框架级 Finalizer 管理 |
| EnsurePaused | ClusterVersion Controller | 框架级暂停控制 |
| EnsureDeleteOrReset | ClusterVersion Controller | 触发各组件 uninstallAction |
| EnsureDryRun | ClusterVersion Controller | 框架级预检模式 |
### 7.10 ActionEngine 设计
ActionEngine 是通用执行引擎，解释执行 ComponentVersion YAML 中的 Action 定义。
```go
// pkg/actionengine/engine.go

type ActionEngine struct {
    client      client.Client
    templateCtx *TemplateContext
    executors   map[ActionType]ActionExecutor
}

type ActionExecutor interface {
    Execute(ctx context.Context, action *ActionSpec, nodeConfig *NodeConfig, templateCtx *TemplateContext) error
}

func (e *ActionEngine) ExecuteAction(
    ctx context.Context,
    action *ActionSpec,
    cv *cvo.ComponentVersion,
    nodeConfigs []*cvo.NodeConfig,
) error {
    if action.PreCheck != nil {
        if err := e.executeStep(ctx, action.PreCheck, nil, e.templateCtx); err != nil {
            return fmt.Errorf("preCheck failed: %w", err)
        }
    }

    for _, step := range action.Steps {
        if step.Condition != "" && !e.evaluateCondition(step.Condition, e.templateCtx) {
            continue
        }

        switch action.Strategy.ExecutionMode {
        case ExecutionParallel:
            e.executeParallel(ctx, step, nodeConfigs)
        case ExecutionSerial:
            e.executeSerial(ctx, step, nodeConfigs)
        case ExecutionRolling:
            e.executeRolling(ctx, step, nodeConfigs, action.Strategy)
        }
    }

    if action.PostCheck != nil {
        if err := e.executeStepWithRetry(ctx, action.PostCheck, nil, e.templateCtx); err != nil {
            return fmt.Errorf("postCheck failed: %w", err)
        }
    }
    return nil
}
```

**Rolling 执行器（支持 etcd 逐节点升级）**：
```go
func (e *ActionEngine) executeRolling(
    ctx context.Context,
    action *ActionSpec,
    nodeConfigs []*cvo.NodeConfig,
    strategy ActionStrategy,
) error {
    batchSize := strategy.BatchSize
    if batchSize <= 0 {
        batchSize = 1
    }

    for i := 0; i < len(nodeConfigs); i += batchSize {
        end := i + batchSize
        if end > len(nodeConfigs) {
            end = len(nodeConfigs)
        }
        batch := nodeConfigs[i:end]

        for _, nc := range batch {
            templateCtx := e.templateCtx.ForNode(nc)

            for _, step := range action.Steps {
                if step.Condition != "" && !e.evaluateCondition(step.Condition, templateCtx) {
                    continue
                }
                if err := e.executeStep(ctx, &step, nc, templateCtx); err != nil {
                    if strategy.FailurePolicy == FailFast {
                        return fmt.Errorf("node %s step %s failed: %w", nc.Name, step.Name, err)
                    }
                    continue
                }
            }

            if action.PostCheck != nil && strategy.WaitForCompletion {
                if err := e.executeStepWithRetry(ctx, action.PostCheck, nc, templateCtx); err != nil {
                    if strategy.FailurePolicy == FailFast {
                        return fmt.Errorf("node %s postCheck failed: %w", nc.Name, err)
                    }
                    continue
                }
            }
        }

        if strategy.BatchInterval != nil && i+batchSize < len(nodeConfigs) {
            time.Sleep(strategy.BatchInterval.Duration)
        }
    }
    return nil
}
```
### 7.11 升级时卸载旧组件的完整流程
```
用户修改 ClusterVersion.spec.desiredVersion = "v2.6.0"
    │
    ├── ClusterVersion Controller
    │   ├── 检测 desiredVersion != currentVersion
    │   ├── 查找新 ReleaseImage → 解析新 ComponentVersion 列表
    │   ├── 查找旧 ReleaseImage（status.currentReleaseRef）→ 解析旧 ComponentVersion 列表
    │   ├── 对比新旧 ReleaseImage，生成 UpgradeSteps
    │   └── 按升级 DAG 逐步更新 ComponentVersion CR
    │
    ├── ComponentVersion Controller（逐组件触发）
    │   ├── 检测 desiredVersion != installedVersion
    │   ├── 查找旧版本：
    │   │   └── ClusterVersion.status.currentReleaseRef
    │   │       → 旧 ReleaseImage
    │   │         → spec.components[name=当前组件]
    │   │           → 旧 ComponentVersion CR
    │   │             → spec.versions[version=旧版本].uninstallAction
    │   ├── 执行旧版本 uninstallAction
    │   ├── 执行新版本 upgradeAction（或 installAction）
    │   └── 健康检查
    │
    └── 全部组件完成 → ClusterVersion 更新 currentVersion + currentReleaseRef
```
### 7.12 模板变量系统
ActionSpec 中的 Script、Manifest、Helm.Values 支持模板变量，运行时由 ActionEngine 渲染：
```go
type TemplateContext struct {
    ComponentName string
    Version       string
    NodeIP        string
    NodeHostname  string
    NodeRoles     []string
    ClusterName      string
    ClusterNamespace string
    CurrentVersion   string
    EtcdVersion        string
    KubernetesVersion  string
    ContainerdVersion  string
    OpenFuyaoVersion   string
    ImageRepo          string
    HTTPRepo           string
    CertificatesDir    string
    ControlPlaneEndpoint string
    EtcdInitialCluster   string
    EtcdDataDir          string
    AgentPort            int
    IsFirstMaster        bool
    PreviousVersion      string
}
```
### 7.13 ReleaseImage 示例
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.0.0
  namespace: bke-system
spec:
  version: v2.0.0
  displayName: "openFuyao v2.0.0"
  description: "openFuyao 2.0 GA Release"
  releaseTime: "2026-04-01T00:00:00Z"
  components:
    - componentName: bkeAgent
      version: v1.2.0
      mandatory: true
    - componentName: nodesEnv
      version: v1.0.0
      mandatory: true
    - componentName: containerd
      version: v1.7.2
      mandatory: true
    - componentName: etcd
      version: v3.5.12
      mandatory: true
    - componentName: kubernetes
      version: v1.29.0
      mandatory: true
    - componentName: clusterAPI
      version: v1.0.0
      mandatory: true
    - componentName: certs
      version: v1.0.0
      mandatory: true
    - componentName: loadBalancer
      version: v1.0.0
      mandatory: true
    - componentName: addon
      version: v1.0.0
      mandatory: true
    - componentName: openFuyao
      version: v2.0.0
      mandatory: true
    - componentName: bkeProvider
      version: v1.1.0
      mandatory: true
    - componentName: nodesPostProcess
      version: v1.0.0
      mandatory: false
    - componentName: agentSwitch
      version: v1.0.0
      mandatory: true
  compatibility:
    minKubernetesVersion: v1.28.0
    maxKubernetesVersion: v1.30.0
    osRequirements:
      - osType: kylin
        minVersion: V10
      - osType: centos
        minVersion: "7.9"
  upgradePaths:
    - fromVersion: v1.9.0
      toVersion: v2.0.0
    - fromVersion: v1.8.0
      toVersion: v2.0.0
```
### 7.14 ClusterVersion 示例
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ClusterVersion
metadata:
  name: bke-cluster-version
  namespace: bke-system
spec:
  desiredVersion: v2.0.0
  releaseRef:
    name: openfuyao-v2.0.0
    version: v2.0.0
  clusterRef:
    name: bke-cluster
    namespace: bke-system
  upgradeStrategy:
    type: RollingUpdate
    maxUnavailable: 1
    preCheck:
      enabled: true
      timeout: 5m
    postCheck:
      enabled: true
      timeout: 10m
    autoRollback: true
    rollbackTimeout: 30m
status:
  currentVersion: v1.9.0
  currentReleaseRef:
    name: openfuyao-v1.9.0
    version: v1.9.0
  currentComponents:
    bkeAgent: v1.1.0
    containerd: v1.7.0
    etcd: v3.5.11
    kubernetes: v1.28.0
    openFuyao: v1.9.0
  phase: Upgrading
  upgradeSteps:
    - componentName: bkeProvider
      version: v1.1.0
      phase: Completed
    - componentName: bkeAgent
      version: v1.2.0
      phase: Completed
    - componentName: containerd
      version: v1.7.2
      phase: Upgrading
    - componentName: etcd
      version: v3.5.12
      phase: Pending
    - componentName: kubernetes
      version: v1.29.0
      phase: Pending
    - componentName: openFuyao
      version: v2.0.0
      phase: Pending
  currentStepIndex: 2
  history:
    - fromVersion: v1.8.0
      toVersion: v1.9.0
      startedAt: "2026-03-01T10:00:00Z"
      completedAt: "2026-03-01T10:30:00Z"
      result: Succeeded
```
## 8. 迁移策略
### 8.1 Feature Gate 渐进切换
```go
const FeatureGateDeclarativeVersionOrchestration = "DeclarativeVersionOrchestration"
```

| 阶段 | Feature Gate | 行为 |
|------|-------------|------|
| Phase A | `DeclarativeVersionOrchestration=false`（默认） | CRD 注册，控制器不运行，PhaseFlow 正常 |
| Phase B | `DeclarativeVersionOrchestration=true`（手动） | 新控制器运行，dry-run 模式，不实际执行 |
| Phase C | `DeclarativeVersionOrchestration=true`（手动） | 新控制器接管安装逻辑，PhaseFrame 仍运行但不执行 |
| Phase D | 不可逆 | 完全切换到新架构，移除 PhaseFrame 代码 |
### 8.2 迁移步骤
| 步骤 | 工作量 | 说明 |
|------|--------|------|
| 1. 定义 CRD | 8人天 | 四个 CRD 的 API 定义与注册 |
| 2. 实现 ActionEngine | 18人天 | Script/Manifest/Helm/Controller 四种执行器 + 模板渲染 |
| 3. 实现 ReleaseImage 控制器 | 5人天 | 验证逻辑 |
| 4. 实现 ComponentVersion 控制器 | 15人天 | 核心逻辑，含旧版本卸载流程 |
| 5. 实现 NodeConfig 控制器 | 12人天 | 节点组件管理 |
| 6. 实现 ClusterVersion 控制器 | 15人天 | 版本编排与 DAG 调度 |
| 7. Phase→ComponentVersion 迁移 | 30人天 | 16 个组件的 YAML 声明 + Executor 实现 |
| 8. BKEClusterReconciler 适配 | 8人天 | 控制类逻辑保留，编排逻辑切换 |
| 9. Feature Gate 集成 | 5人天 | 双轨运行与切换 |
| 10. E2E 测试 | 15人天 | 安装/升级/扩缩容/回滚全链路测试 |
| **总计** | **131人天（约 6.55人月）** | |
## 9. 风险与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 迁移过程中破坏现有集群 | 高 | Feature Gate 渐进切换，双轨运行 |
| DAG 调度死锁 | 高 | 循环依赖检测，超时自动解除，手动跳过 |
| 升级时旧版本 ComponentVersion 找不到 | 中 | ReleaseImage 保留历史版本；降级为直接执行 uninstallAction |
| Script 执行安全风险 | 中 | 限制脚本来源，沙箱执行 |
| ComponentVersion 状态不一致 | 中 | 定期全量 Reconcile，状态校验 |
| 升级过程中集群不可用 | 高 | 滚动升级策略，自动回滚 |
| ReleaseImage 与 ComponentVersion 版本不匹配 | 中 | ReleaseImage 控制器验证 |
| YAML 脚本调试困难 | 中 | dry-run 模式 + 模板渲染预览 + 日志输出 |
| 模板变量缺失/错误 | 中 | 模板校验 + 缺失变量报错 + 默认值机制 |
## 10. 测试计划
| 测试类型 | 范围 | 工具 |
|----------|------|------|
| 单元测试 | 各控制器核心逻辑、ActionEngine、DAGScheduler | Ginkgo/Gomega |
| 集成测试 | CRD 创建/更新/删除、控制器交互 | envtest |
| E2E 测试 | 安装/升级/扩缩容/回滚全链路 | Kind + 自定义框架 |
| 性能测试 | 控制器 Reconcile 延迟 | pprof + benchmark |
| 混沌测试 | 升级过程中节点故障 | chaos-mesh |
| 兼容性测试 | 从现有 PhaseFrame 迁移 | 双轨对比测试 |
## 11. 验收标准
1. **YAML 声明验收**：16 个组件全部通过 YAML 声明安装/升级/卸载
2. **安装验收**：从零创建集群，ActionEngine 解释 YAML 完成安装
3. **升级验收**：修改 ClusterVersion 版本，触发旧版本卸载 + 新版本安装
4. **旧组件卸载验收**：升级时通过 ClusterVersion 找到旧 ReleaseImage → 旧 ComponentVersion → 执行 uninstallAction
5. **单组件升级验收**：修改 ComponentVersion 版本，仅升级该组件
6. **扩缩容验收**：添加/移除节点，NodeConfig 自动创建/删除
7. **回滚验收**：升级失败后自动执行 rollbackAction
8. **模板验收**：模板变量正确渲染，条件表达式正确评估
9. **兼容性验收**：Feature Gate 关闭时旧 PhaseFlow 正常运行
10. **ReleaseImage 不可变验收**：创建后修改被拒绝
## 12. 目录结构
```
cluster-api-provider-bke/
├── api/
│   └── cvo/v1beta1/
│       ├── clusterversion_types.go
│       ├── releaseimage_types.go
│       ├── componentversion_types.go
│       ├── nodeconfig_types.go
│       ├── component_types.go
│       └── zz_generated.deepcopy.go
├── controllers/
│   └── cvo/
│       ├── clusterversion_controller.go
│       ├── releaseimage_controller.go
│       ├── componentversion_controller.go
│       └── nodeconfig_controller.go
├── pkg/
│   ├── actionengine/
│   │   ├── engine.go
│   │   ├── template.go
│   │   ├── condition.go
│   │   └── executor/
│   │       ├── script_executor.go
│   │       ├── manifest_executor.go
│   │       ├── helm_executor.go
│   │       └── controller_executor.go
│   ├── cvo/
│   │   ├── dag_scheduler.go
│   │   ├── validator.go
│   │   └── rollback.go
│   └── phaseframe/                      # 保留，逐步废弃
├── config/
│   └── components/                      # 组件 YAML 声明
│       ├── bkeagent-*.yaml
│       ├── nodesenv-*.yaml
│       ├── containerd-*.yaml
│       ├── etcd-*.yaml
│       ├── kubernetes-*.yaml
│       ├── clusterapi-*.yaml
│       ├── certs-*.yaml
│       ├── loadbalancer-*.yaml
│       ├── addon-*.yaml
│       ├── openfuyao-*.yaml
│       ├── bkeprovider-*.yaml
│       ├── nodespostprocess-*.yaml
│       ├── agentswitch-*.yaml
│       ├── clustermanage-*.yaml
│       ├── nodedelete-*.yaml
│       └── clusterhealth-*.yaml
```
## 13. 未来工作
1. **版本包管理**：基于 ReleaseImage 构建版本包，支持 CI/CD 自动发布
2. **多集群版本管理**：支持多集群统一版本管理
3. **版本灰度发布**：支持按节点池灰度升级
4. **版本合规检查**：基于 ReleaseImage 的合规性检查
5. **版本回溯审计**：完整的版本变更审计日志

**核心设计要点总结**：
1. **四层资源关系**：BKECluster(1) → ClusterVersion(1) → ReleaseImage(1) → ComponentVersion(N) → NodeConfig(N)
2. **ReleaseImage 不可变**：确保版本清单一致性，升级通过引用新 ReleaseImage 实现
3. **升级时卸载旧组件**：通过 ClusterVersion.currentReleaseRef → 旧 ReleaseImage → 旧 ComponentVersion → uninstallAction
4. **四种执行方式**：Script/Manifest/Helm/Controller，覆盖所有 Phase 的安装逻辑
5. **Rolling 策略增强**：支持 `waitForCompletion` + `failurePolicy`，实现 etcd 逐节点升级
6. **16 个 ComponentVersion**：完整覆盖所有 26 个 Phase（含 clusterManage、nodeDelete、clusterHealth）
7. **架构清晰、组件可独立演进**：每个 ComponentVersion 独立定义版本和动作，不耦合
        

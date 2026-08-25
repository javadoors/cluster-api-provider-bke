# 声明式升级框架 KEP 文档讨论材料

| 字段 | 值 |
|------|-----|
| **关联事项** | A4: 声明式升级框架 KEP 文档准备 |
| **目标** | 8 月底与工行讨论清晰声明式升级框架方案 |
| **状态** | `讨论稿` |
| **创建日期** | 2026-08-24 |

---

## 目录

1. [声明式升级框架总览](#1-声明式升级框架总览)
2. [文档清单](#2-文档清单)
3. [A4-1: Binary 组件设计要点](#3-a4-1-binary-组件设计要点)
4. [A4-2: 三层状态机设计要点](#4-a4-2-三层状态机设计要点)
5. [A4-3: 可观测性设计要点](#5-a4-3-可观测性设计要点)
6. [A4-4: 备份/回滚设计要点](#6-a4-4-备份回滚设计要点)
7. [A4-5: 升级前预检设计要点](#7-a4-5-升级前预检设计要点)

---

## 1. 声明式升级框架总览

### 1.1 整体架构

openFuyao 声明式升级框架借鉴 OpenShift CVO 理念，通过 `ClusterVersion`、`ReleaseImage`、`UpgradePath`、`ComponentVersion` 四个核心 CRD（`config.openfuyao.com/v1alpha1`），结合 CAPI 基础设施层的 `BKECluster`/`BKEMachine`/`BKENode`（`bke.bocloud.com/v1beta1`），实现集群版本的声明式管理与 DAG 驱动升级。

**当前代码库采用双执行路径**：
- **Legacy 路径**：PhaseFlow 按硬编码 Phase 列表顺序执行（现有生产路径）
- **DAG 路径**：Scheduler 按拓扑排序分批执行（声明式升级路径，由 `cvo.openfuyao.cn/upgrade-ready` annotation 控制）

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                     声明式升级框架整体架构 (基于代码库)                            │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  API 层                                                                  │
    │  ─────                                                                   │
    │  config.openfuyao.com/v1alpha1          bke.bocloud.com/v1beta1          │
    │  ┌──────────────────┐                 ┌──────────────────┐              │
    │  │ ClusterVersion   │                 │ BKECluster       │              │
    │  │ ReleaseImage     │                 │ BKEMachine       │              │
    │  │ ComponentVersion │                 │ BKENode          │              │
    │  │ UpgradePath      │                 │ BKEClusterTemplate│             │
    │  └──────────────────┘                 └──────────────────┘              │
    └─────────────────────────────────────────────────────────────────────────┘
                                          │
                                          ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │  控制器层 (Controllers)                                                  │
    │  ─────────────────────────────────────────────────────────────────────  │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  BKEClusterReconciler (controllers/capbke/)                      │   │
    │  │  ──────────────────────────────────────────────────────────────  │   │
    │  │  依赖: ReleaseStore, ManifestApplier, Tracker, NodeFetcher       │   │
    │  │                                                                    │   │
    │  │  Reconcile 流程:                                                   │   │
    │  │  1. getAndValidateCluster (mergecluster.GetCombinedBKECluster)     │   │
    │  │  2. ensureClusterVersionOnInstall (创建 ClusterVersion CR)         │   │
    │  │  3. handleClusterStatus (计算 agent/节点状态)                      │   │
    │  │  4. executePhaseFlow:                                              │   │
    │  │     ├─ [annotation=upgrade-ready] → executeUpgradeDAG (DAG 路径)   │   │
    │  │     └─ [else] → PhaseFlow.CalculatePhase → Execute (Legacy 路径)  │   │
    │  │  5. completeClusterVersionInstall (更新 ClusterVersion.Status)     │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  BKEMachineReconciler (controllers/capbke/)                      │   │
    │  │  ──────────────────────────────────────────────────────────────  │   │
    │  │  依赖: NodeFetcher                                                │   │
    │  │                                                                    │   │
    │  │  Reconcile 流程:                                                   │   │
    │  │  1. 获取 BKEMachine + Owner Machine + Cluster                      │   │
    │  │  2. reconcileDelete / reconcile                                    │   │
    │  │  3. reconcileCommand (agent 命令) → reconcileBootstrap             │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘
                                          │
                                          ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │  执行层 (Execution Layer)                                                │
    │  ─────────────────────────────────────────────────────────────────────  │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  DAG 执行引擎 (pkg/dagexec/)                                      │   │
    │  │                                                                    │   │
    │  │  Scheduler                                                        │   │
    │  │  ├─ dag.TopologicalBatches() → 按依赖分批                         │   │
    │  │  ├─ executeBatchParallel → errgroup + semaphore (MaxParallel=8)   │   │
    │  │  ├─ shouldSkipComponent → 跳过已完成组件                          │   │
    │  │  └─ persistBatchResults → 更新 DeclarativeUpgradeStatus           │   │
    │  │                                                                    │   │
    │  │  ExecutorRegistry (type → executor 映射)                          │   │
    │  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐            │   │
    │  │  │   Inline     │  │    YAML      │  │    Helm      │            │   │
    │  │  │  Executor    │  │   Executor   │  │   Executor   │            │   │
    │  │  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘            │   │
    │  │         │                 │                 │                      │   │
    │  │         ▼                 ▼                 ▼                      │   │
    │  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐            │   │
    │  │  │ InlineRunner │  │YamlInstaller │  │HelmInstaller │            │   │
    │  │  │ (PhaseRunner │  │(Apply/Prune/ │  │ (pluggable)  │            │   │
    │  │  │  adapter)    │  │ HealthCheck) │  │              │            │   │
    │  │  └──────────────┘  └──────────────┘  └──────────────┘            │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  Legacy 执行引擎 (pkg/phaseframe/)                                │   │
    │  │                                                                    │   │
    │  │  PhaseFlow                                                        │   │
    │  │  ├─ CalculatePhase: 遍历 Phase 列表, NeedExecute() 过滤           │   │
    │  │  └─ Execute: PreHook → Execute → PostHook                         │   │
    │  │                                                                    │   │
    │  │  DeployPhases: Agent → NodesEnv → APIObj → Certs → LB →           │   │
    │  │                MasterInit → MasterJoin → WorkerJoin → Addon →      │   │
    │  │                PostProcess → AgentSwitch                           │   │
    │  │                                                                    │   │
    │  │  PostDeployPhases: Provider → Agent → Containerd → Etcd →          │   │
    │  │                    Worker → Master → Component → Cluster            │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘
                                          │
                                          ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │  状态层 (Status Layer)                                                   │
    │  ─────────────────────────────────────────────────────────────────────  │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  BKECluster.Status                                                │   │
    │  │  ├─ Phase: InitControlPlane → JoinWorker → Ready → UpgradeXxx     │   │
    │  │  ├─ ClusterStatus: Ready / Initializing / Upgrading / Failed      │   │
    │  │  ├─ ClusterHealthState: Deploying / Healthy / Unhealthy / Failed  │   │
    │  │  ├─ DeclarativeUpgrade: TargetVersion, Completed[], LastError     │   │
    │  │  └─ ClusterComponentStatuses: map[name]ComponentLifecycleStatus   │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  StatusManager (pkg/statusmanage/)                                │   │
    │  │  ├─ 重试计数器: 连续相同失败次数                                  │   │
    │  │  ├─ <= 10 次: 屏蔽失败, 恢复上一正常状态, 继续 Reconcile          │   │
    │  │  └─ > 10 次: 设置终端 *Failed 状态, 停止 Reconcile                │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘
```

### 1.2 核心数据结构

#### 1.2.1 CRD 关系图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          CRD 关系与数据流 (基于代码库)                            │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  bke.bocloud.com/v1beta1                                                │
    │  ─────────────────────────────────────────────────────────────────────  │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  BKECluster                                                       │   │
    │  │  spec.controlPlaneEndpoint: {host, port}                          │   │
    │  │  spec.clusterConfig: {cluster, networking, containerRuntime...}   │   │
    │  │  spec.pause / spec.dryRun / spec.reset                            │   │
    │  │  status.phase: InitControlPlane / JoinWorker / Ready / ...        │   │
    │  │  status.clusterStatus: Ready / Upgrading / Failed                 │   │
    │  │  status.declarativeUpgrade: {targetVersion, completed[], ...}     │   │
    │  │  status.clusterComponentStatuses: {certs: Installed, ...}         │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                          │ 1:1 (OwnerReference)                          │
    │                          ▼                                               │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  BKEMachine                                                       │   │
    │  │  spec.providerID / spec.pause / spec.dryRun                       │   │
    │  │  status.ready / status.bootstrapped / status.addresses            │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘
                                          │
                                          │ ensureClusterVersionOnInstall
                                          ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │  config.openfuyao.com/v1alpha1                                          │
    │  ─────────────────────────────────────────────────────────────────────  │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  ClusterVersion                                                   │   │
    │  │  spec.desiredVersion: v2.7.0                                      │   │
    │  │  status.phase: Pending → Installing → Installed → Ready           │   │
    │  │            → PreChecking → Upgrading → Upgraded / Failed          │   │
    │  │  status.currentVersion: v2.6.0                                    │   │
    │  │  status.upgradeHistory: [{version, phase, startTime, ...}]        │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                          │ 1:1 引用                                      │
    │                          ▼                                               │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  ReleaseImage                                                     │   │
    │  │  spec.version: v2.7.0                                             │   │
    │  │  spec.digest / spec.verifySignature                               │   │
    │  │  spec.install.components: [{name, version}, ...]                  │   │
    │  │  spec.upgrade.components: [{name, version, inline?}, ...]         │   │
    │  │  status.phase: Valid / Invalid / ManifestMissing                  │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                          │ 按 (name, version) 定位                       │
    │                          ▼                                               │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  ComponentVersion                                                 │   │
    │  │  spec.name / spec.type / spec.version                             │   │
    │  │  spec.type: yaml | helm | inline | binary                         │   │
    │  │  spec.yaml: {namespace, applyStrategy, healthCheck, ...}          │   │
    │  │  spec.inline: {handler, version}                                  │   │
    │  │  spec.subComponents / spec.dependencies / spec.upgradeStrategy    │   │
    │  │  spec.resources: [{kind, apiVersion, data/stringData/manifest}]   │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  UpgradePath (Cluster-scoped)                                     │   │
    │  │  spec.versions: [{version, installable, deprecated, ...}]         │   │
    │  │  spec.paths: [{from, to, blocked, preCheck[], postCheck[]}]       │   │
    │  │  status.phase: Active / Blocked / Invalid                         │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘
```

#### 1.2.2 核心 Go 类型定义 (基于代码库)

```go
// ============================================================
// config.openfuyao.com/v1alpha1 - 声明式升级 CRD
// ============================================================

// ClusterVersion (api/v1alpha1/clusterversion_types.go)
type ClusterVersion struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   ClusterVersionSpec   `json:"spec,omitempty"`
    Status ClusterVersionStatus `json:"status,omitempty"`
}
type ClusterVersionSpec struct {
    DesiredVersion string `json:"desiredVersion"`
}
type ClusterVersionStatus struct {
    Phase           ClusterVersionPhase `json:"phase,omitempty"`
    CurrentVersion  string              `json:"currentVersion,omitempty"`
    UpgradeHistory  []UpgradeHistory    `json:"upgradeHistory,omitempty"`
    Conditions      []metav1.Condition  `json:"conditions,omitempty"`
}
// Phase: Pending → Installing → Installed → Ready → PreChecking → Upgrading → Upgraded / Failed

// ReleaseImage (api/v1alpha1/releaseimage_types.go)
type ReleaseImage struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   ReleaseImageSpec   `json:"spec,omitempty"`
    Status ReleaseImageStatus `json:"status,omitempty"`
}
type ReleaseImageSpec struct {
    Version        string                  `json:"version"`
    Digest         string                  `json:"digest,omitempty"`
    Install        ReleaseImageComponents  `json:"install,omitempty"`
    Upgrade        ReleaseImageComponents  `json:"upgrade,omitempty"`
}
type ReleaseImageComponents struct {
    Components []ReleaseImageComponent `json:"components,omitempty"`
}
type ReleaseImageComponent struct {
    Name    string       `json:"name"`
    Version string       `json:"version"`
    Inline  *InlineSpec  `json:"inline,omitempty"`  // 可选: 指定 inline handler
}

// ComponentVersion (api/v1alpha1/componentversion_types.go)
type ComponentVersion struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   ComponentVersionSpec   `json:"spec,omitempty"`
    Status ComponentVersionStatus `json:"status,omitempty"`
}
type ComponentVersionSpec struct {
    Name            string              `json:"name"`
    Type            ComponentType       `json:"type"`
    Version         string              `json:"version"`
    YAML            *YAMLSpec           `json:"yaml,omitempty"`
    Inline          *InlineSpec         `json:"inline,omitempty"`
    SubComponents   []SubComponent      `json:"subComponents,omitempty"`
    Compatibility   CompatibilitySpec   `json:"compatibility,omitempty"`
    Dependencies    []Dependency        `json:"dependencies,omitempty"`
    UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
    Resources       []ResourceSpec      `json:"resources,omitempty"`
}
type ComponentType string
const (
    ComponentTypeYAML   ComponentType = "yaml"
    ComponentTypeHelm   ComponentType = "helm"
    ComponentTypeInline ComponentType = "inline"
    ComponentTypeBinary ComponentType = "binary"
)

// UpgradePath (api/v1alpha1/upgradepath_types.go) - Cluster-scoped
type UpgradePath struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   UpgradePathSpec   `json:"spec,omitempty"`
    Status UpgradePathStatus `json:"status,omitempty"`
}
type UpgradePathSpec struct {
    Versions []VersionEntry  `json:"versions,omitempty"`
    Paths    []UpgradePathRule `json:"paths"`
}
type UpgradePathRule struct {
    From      string      `json:"from"`
    To        string      `json:"to"`
    Blocked   bool        `json:"blocked,omitempty"`
    PreCheck  []CheckStep `json:"preCheck,omitempty"`
    PostCheck []CheckStep `json:"postCheck,omitempty"`
}

// ============================================================
// bke.bocloud.com/v1beta1 - CAPI 基础设施层
// ============================================================

// BKECluster (api/capbke/v1beta1/bkecluster_types.go)
type BKECluster struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   confv1beta1.BKEClusterSpec   `json:"spec,omitempty"`
    Status confv1beta1.BKEClusterStatus `json:"status,omitempty"`
}

// BKEClusterStatus (api/bkecommon/v1beta1/bkecluster_status.go)
type BKEClusterStatus struct {
    Ready                    bool                              `json:"ready"`
    Phase                    BKEClusterPhase                   `json:"phase,omitempty"`
    ClusterStatus            ClusterStatus                     `json:"clusterStatus,omitempty"`
    ClusterHealthState       ClusterHealthState                `json:"clusterHealthState,omitempty"`
    DeclarativeUpgrade       *DeclarativeUpgradeStatus         `json:"declarativeUpgrade,omitempty"`
    ClusterComponentStatuses map[string]ComponentLifecycleStatus `json:"clusterComponentStatuses,omitempty"`
    PhaseStatus              PhaseStatus                       `json:"phaseStatus,omitempty"`
    Conditions               ClusterConditions                 `json:"conditions,omitempty"`
}

// DeclarativeUpgradeStatus - DAG 升级状态追踪
type DeclarativeUpgradeStatus struct {
    TargetVersion string           `json:"targetVersion,omitempty"`
    Completed     []string         `json:"completed,omitempty"`
    LastError     string           `json:"lastError,omitempty"`
    LastFailure   *UpgradeFailure  `json:"lastFailure,omitempty"`
}

// ComponentLifecycleStatus - 组件生命周期状态
type ComponentLifecycleStatus struct {
    Phase   ComponentLifecyclePhase `json:"phase"`
    Version string                  `json:"version,omitempty"`
    Message string                  `json:"message,omitempty"`
}
// Phase: Pending → Installing → Installed → Upgrading → Failed

// BKEMachine (api/capbke/v1beta1/bkemachine_types.go)
type BKEMachine struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   BKEMachineSpec   `json:"spec,omitempty"`
    Status BKEMachineStatus `json:"status,omitempty"`
}

// ============================================================
// DAG 执行引擎 (pkg/dagexec/)
// ============================================================

// ComponentExecutor - 组件执行器接口
type ComponentExecutor interface {
    ExecuteComponent(ctx context.Context, node *topology.ComponentNode, execCtx *ExecutionContext) error
    GetComponentType() ComponentType  // "inline" | "yaml" | "helm"
}

// ExecutorRegistry - type → executor 映射
type ExecutorRegistry struct {
    executors map[ComponentType]ComponentExecutor
}

// Scheduler - DAG 调度器
type Scheduler struct {
    InlineRunner        InlineRunner
    ManifestStore       manifest.Store
    ManifestApplier     manifest.Applier
    MaxParallelPerBatch int               // default 8
    Registry            *ExecutorRegistry
    CVStore             ComponentVersionStore
}

// ExecutionContext - 执行上下文
type ExecutionContext struct {
    OldCluster             *bkev1beta1.BKECluster
    Cluster                *bkev1beta1.BKECluster
    ComponentStatusUpdater ComponentStatusUpdater
    VersionContext         *upgrade.VersionContext
    TemplateContext        manifest.TemplateContext
    TargetClient           kubernetes.Interface
    Client                 client.Client
}

// ============================================================
// Phase 框架 (pkg/phaseframe/)
// ============================================================

// Phase - Phase 接口
type Phase interface {
    Name() confv1beta1.BKEClusterPhase
    Execute() (ctrl.Result, error)
    NeedExecute(old, new *bkev1beta1.BKECluster) bool
    ExecutePreHook() error
    ExecutePostHook(err error) error
    Report(msg string, onlyRecord bool) error
}

// PhaseFlow - Phase 编排引擎
type PhaseFlow struct {
    BKEPhases []Phase
    ctx       *PhaseContext
}
// CalculatePhase: 遍历 Phase 列表, NeedExecute() 过滤
// Execute: PreHook → Execute → PostHook
```

### 1.3 设计思路

| 维度 | 设计要点 |
|------|---------|
| **双执行路径** | Legacy PhaseFlow (硬编码 Phase 列表) + DAG Scheduler (拓扑排序)，通过 annotation 切换 |
| **声明式版本管理** | 用户声明 `ClusterVersion.Spec.DesiredVersion`，系统自动编排升级 |
| **版本清单不可变** | `ReleaseImage.Spec` 创建后不可修改，保证升级一致性 |
| **组件化架构** | 每个组件独立定义版本、依赖、兼容性约束，支持独立演进 |
| **DAG 驱动执行** | `pkg/topology` 构建 DAG，`pkg/dagexec.Scheduler` 按拓扑批次并行执行 |
| **可插拔执行器** | `ExecutorRegistry` 按 ComponentType 分发到 Inline/YAML/Helm Executor |
| **升级路径管控** | `UpgradePath` 定义版本间路径，支持 blocked/deprecated 策略 |
| **兼容性校验** | 基于 semver 约束的 CSP 回溯算法校验组件间版本兼容性 |
| **重试状态机** | `StatusManager` 提供失败计数 + 状态屏蔽，<=10 次自动重试，>10 次终端失败 |
| **离线环境支持** | `ManifestStore` 优先本地 `/etc/bke/manifests`，降级 OCI 拉取 |

### 1.4 执行流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    声明式升级完整执行流程 (基于代码库)                              │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌──────────────────────────────┐
    │  1. 用户声明期望版本          │
    │  kubectl annotate bkecluster │
    │  cvo.openfuyao.cn/           │
    │  upgrade-ready=true          │
    │  + 修改 ClusterVersion       │
    │  spec.desiredVersion         │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  2. BKEClusterReconciler.Reconcile                                   │
    │  ──────────────────────────────────────────────────────────────────  │
    │  getAndValidateCluster (mergecluster.GetCombinedBKECluster)          │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  3. ensureClusterVersionOnInstall                                    │
    │  ──────────────────────────────────────────────────────────────────  │
    │  创建/更新 ClusterVersion CR (OwnerReference → BKECluster)           │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  4. ClusterVersion Controller                                        │
    │  ──────────────────────────────────────────────────────────────────  │
    │  加载 ReleaseImage (ReleaseStore → OCI/本地缓存)                     │
    │  加载 UpgradePath → 校验版本路径合法性                               │
    │  加载 ComponentVersion → 校验兼容性                                  │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  5. executeUpgradeDAG (BKEClusterReconciler)                         │
    │  ──────────────────────────────────────────────────────────────────  │
    │  BuildUpgradeDAG (pkg/topology/build.go)                             │
    │  ├─ 解析 ReleaseImage.Upgrade.Components                             │
    │  ├─ 展开 composite 组件为 subComponents                              │
    │  ├─ 构建依赖图 (DependencyResolver)                                  │
    │  └─ 拓扑排序 → TopologicalBatches                                    │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  6. Scheduler.ExecuteDAG                                             │
    │  ──────────────────────────────────────────────────────────────────  │
    │  for each batch in dag.TopologicalBatches():                         │
    │    executeBatchParallel (errgroup + semaphore, MaxParallel=8):       │
    │      for each component in batch:                                    │
    │        ├─ shouldSkipComponent → 跳过已完成                           │
    │        ├─ markLifecyclePending → 标记 ComponentStatus=Pending        │
    │        ├─ CVStore.GetComponentVersion → 获取组件类型                 │
    │        ├─ Registry.Resolve(type) → 分发到执行器                      │
    │        │   ├─ "inline" → InlineComponentExecutor                     │
    │        │   │   └─ InlineRunner.Execute (PhaseRunner adapter)         │
    │        │   ├─ "yaml"   → YamlComponentExecutor                       │
    │        │   │   └─ YamlInstaller.Apply (SSA Apply + Prune)            │
    │        │   └─ "helm"   → HelmComponentExecutor (pluggable)           │
    │        │       └─ Helm SDK install/upgrade                           │
    │        └─ markLifecycleInstalled/Failed → 更新 ComponentStatus       │
    │    persistBatchResults → 更新 DeclarativeUpgradeStatus               │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  7. 升级完成                                                         │
    │  ──────────────────────────────────────────────────────────────────  │
    │  completeClusterVersionInstall                                       │
    │  ├─ ClusterVersion.Status.Phase → Upgraded                           │
    │  ├─ ClusterVersion.Status.CurrentVersion → 目标版本                  │
    │  ├─ BKECluster.Status.DeclarativeUpgrade.Completed → 全部组件        │
    │  └─ BKECluster.Status.ClusterComponentStatuses → 全部 Installed      │
    └──────────────────────────────────────────────────────────────────────┘
```

### 1.5 关键设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| **双执行路径** | PhaseFlow + DAG Scheduler | 渐进式迁移，Legacy 路径保证生产稳定，DAG 路径支持声明式升级 |
| **执行器注册** | ExecutorRegistry (type → executor) | 可插拔架构，新增组件类型只需注册新 Executor |
| **Inline 桥接** | InlinePhaseRunnerAdapter | 复用现有 Phase 代码，无需重写即可在 DAG 中执行 |
| **状态追踪** | DeclarativeUpgradeStatus + ClusterComponentStatuses | 组件级生命周期追踪，支持断点续传 |
| **重试机制** | StatusManager (失败计数 + 状态屏蔽) | 避免瞬时故障导致升级中断，超过阈值才终端失败 |
| **制品分发** | ManifestStore (本地优先 + OCI 降级) | 支持离线环境，多级缓存提升性能 |
| **Feature Gate** | DeclarativeUpgradeEnabled + HelmComponentEnabled | 灰度发布，控制新能力启用范围 |

### 1.6 目标架构

当前架构已实现 DAG 驱动的声明式升级，但节点级组件（bkeagent、kubelet、etcd 等）仍通过 Inline Phase 硬编码在 BKECluster Controller 中执行，缺乏独立的节点生命周期管理。目标架构引入**三层状态机**和 **CAPI BKEMachine 深度集成**，实现集群层、节点层、组件层的分层管理。

#### 1.6.1 目标架构图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                     目标架构：三层状态机 + CAPI BKEMachine 集成                    │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  CAPI 集成架构                                                           │
    │  ─────────────────────────────────────────────────────────────────────  │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  Cluster API Core Controllers                                     │   │
    │  │  ┌──────────────────┐  ┌──────────────────┐  ┌────────────────┐  │   │
    │  │  │ Cluster          │  │ Machine          │  │MachineDeployment│  │   │
    │  │  │ Controller       │  │ Controller       │  │ Controller     │  │   │
    │  │  └────────┬─────────┘  └────────┬─────────┘  └────────┬───────┘  │   │
    │  └───────────┼──────────────────────┼──────────────────────┼────────┘   │
    │              │ Watch                │ Watch                │ Watch      │
    │              ▼                      ▼                      ▼            │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  BKE Infrastructure Provider                                      │   │
    │  │                                                                    │   │
    │  │  ┌────────────────────────────────┐  ┌────────────────────────┐  │   │
    │  │  │ BKECluster Controller          │  │ BKEMachine Controller  │  │   │
    │  │  │ ───────────────────────────    │  │ ─────────────────────  │  │   │
    │  │  │                                  │  │                          │  │   │
    │  │  │ 职责:                            │  │ 职责:                    │  │   │
    │  │  │ • L1 集群层状态机                │  │ • L2 节点层状态机        │  │   │
    │  │  │ • DAG 构建与执行                 │  │ • L3 组件层状态机        │  │   │
    │  │  │ • 集群级组件直接执行             │  │ • 节点级组件执行         │  │   │
    │  │  │ • node-group 驱动节点状态机      │  │ • 组件状态追踪           │  │   │
    │  │  │                                  │  │                          │  │   │
    │  │  │ 输入: BKECluster                 │  │ 输入: BKEMachine         │  │   │
    │  │  │ 输出: BKECluster.Status          │  │ 输出: BKEMachine.Status  │  │   │
    │  │  │       + BKEMachine 创建/更新     │  │       + 组件执行结果     │  │   │
    │  │  │       + 等待 BKEMachine 完成     │  │                          │  │   │
    │  │  └────────────────────────────────┘  └────────────────────────┘  │   │
    │  │                    │                                │              │   │
    │  │                    │ 创建/更新 BKEMachine           │              │   │
    │  │                    │ + 等待 Status 就绪             │              │   │
    │  │                    └────────────────────────────────┘              │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  ReleaseImage 数据流向                                                   │
    │  ─────────────────────────────────────────────────────────────────────  │
    │                                                                          │
    │  ReleaseImage                                                            │
    │    │                                                                     │
    │    ├─ clusterComponents ──────────────────────────────────────────────┐  │
    │    │   (certs, coredns, kube-proxy, bocoperator, cluster-api)         │  │
    │    │                                                                   │  │
    │    │   直接传递给 BKECluster Controller                               │  │
    │    │                                                                   │  │
    │    │   BKECluster Controller DAG 执行:                                │  │
    │    │   ┌────────┐   ┌─────────┐   ┌────────────┐   ┌────────────┐    │  │
    │    │   │ certs  │──>│ coredns │   │ kube-proxy │   │ bocoperator│    │  │
    │    │   │        │──>│         │   │            │   │            │    │  │
    │    │   └────────┘   └─────────┘   └────────────┘   └────────────┘    │  │
    │    │   (DAG 直接执行 L3 组件状态机)                                   │  │
    │    │                                                                   │  │
    │    ├─ nodeComponents ───────────────────────────────────────────────┐ │  │
    │    │   (bkeagent, containerd, kubelet, kubectl, etcd, apiserver...) │ │  │
    │    │                                                                 │ │  │
    │    │   按 roles 过滤，写入 BKEMachine.Spec                          │ │  │
    │    │                                                                 │ │  │
    │    │   ┌─────────────────────────────────────────────────────────┐  │ │  │
    │    │   │ BKEMachine (master) Spec.NodeComponents:                │  │ │  │
    │    │   │   bkeagent → containerd → kubelet → kubectl             │  │ │  │
    │    │   │                            └→ etcd → apiserver          │  │ │  │
    │    │   │                                      └→ cm, scheduler   │  │ │  │
    │    │   └─────────────────────────────────────────────────────────┘  │ │  │
    │    │                                                                 │ │  │
    │    │   ┌─────────────────────────────────────────────────────────┐  │ │  │
    │    │   │ BKEMachine (worker) Spec.NodeComponents:                │  │ │  │
    │    │   │   bkeagent → containerd → kubelet → kubectl             │  │ │  │
    │    │   │   (无 etcd/apiserver/controller-manager/scheduler)      │  │ │  │
    │    │   └─────────────────────────────────────────────────────────┘  │ │  │
    │    │                                                                 │ │  │
    │    │   BKEMachine Controller:                                       │ │  │
    │    │   按依赖顺序驱动 L2 节点层 + L3 组件层状态机                    │ │  │
    │    │                                                                 │ │  │
    │    └─────────────────────────────────────────────────────────────────┘ │  │
    │    └──────────────────────────────────────────────────────────────────┘  │
    └─────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  三层状态机执行位置                                                      │
    │  ─────────────────────────────────────────────────────────────────────  │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  BKECluster Controller                                            │   │
    │  │  ─────────────────────                                            │   │
    │  │  执行: L1 集群层状态机                                             │   │
    │  │  状态: Pending → Installing → Running → Upgrading → Scaling       │   │
    │  │        → RollingBack → Failed                                     │   │
    │  │                                                                    │   │
    │  │  DAG 执行:                                                         │   │
    │  │  ┌──────────┐   ┌──────────────┐   ┌──────────┐   ┌──────────┐  │   │
    │  │  │  certs   │──>│  node-group  │──>│  coredns │──>│kube-proxy│  │   │
    │  │  │(L3直接执行)│  │(创建BKEMachine)│  │(L3直接执行)│  │(L3直接执行)│  │   │
    │  │  └──────────┘   └──────────────┘   └──────────┘   └──────────┘  │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                                                                          │
    │  ┌──────────────────────────────────────────────────────────────────┐   │
    │  │  BKEMachine Controller (每个节点一个 Reconcile)                   │   │
    │  │  ─────────────────────────────────────────                        │   │
    │  │  执行: L2 节点层状态机 + L3 组件层状态机                           │   │
    │  │                                                                    │   │
    │  │  L2 节点层状态:                                                    │   │
    │  │  Pending → Provisioning → Ready → Upgrading → Deleting → Failed   │   │
    │  │                                                                    │   │
    │  │  L3 组件层状态 (每个组件独立状态机):                                │   │
    │  │  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐      │   │
    │  │  │ bkeagent │──>│containerd│──>│ kubelet  │──>│ kubectl  │      │   │
    │  │  │ Pending  │   │ Pending  │   │ Pending  │   │ Pending  │      │   │
    │  │  │    ↓     │   │    ↓     │   │    ↓     │   │    ↓     │      │   │
    │  │  │Installing│   │Installing│   │Installing│   │Installing│      │   │
    │  │  │    ↓     │   │    ↓     │   │    ↓     │   │    ↓     │      │   │
    │  │  │Installed │   │Installed │   │Installed │   │Installed │      │   │
    │  │  └──────────┘   └──────────┘   └──────────┘   └──────────┘      │   │
    │  └──────────────────────────────────────────────────────────────────┘   │
    │                                                                          │
    └─────────────────────────────────────────────────────────────────────────┘
```

#### 1.6.2 当前架构 vs 目标架构

| 维度 | 当前架构 | 目标架构 |
|------|---------|---------|
| **节点级组件执行** | Inline Phase 硬编码在 BKECluster Controller | BKEMachine Controller 独立驱动 |
| **节点生命周期管理** | 无独立状态，通过 BKENode.State 追踪 | BKEMachine.Status.LifecyclePhase (L2) |
| **组件状态追踪** | BKECluster.Status.ClusterComponentStatuses | BKEMachine.Status.ComponentStatuses (L3) |
| **状态层级** | 单层（集群级） | 三层（集群/节点/组件） |
| **CAPI 集成深度** | 浅层（BKEMachine 仅用于节点引导） | 深度（BKEMachine 作为节点状态协调资源） |
| **节点并行执行** | Phase 内串行 | 所有节点并行执行各自的组件状态机 |
| **依赖表达** | 集群级和节点级依赖分离 | 统一 DAG 表达（通过 composite 类型） |

#### 1.6.3 目标架构设计思路

| 维度 | 设计要点 |
|------|---------|
| **三层状态机** | L1 集群层由 BKECluster Controller 驱动，L2/L3 由 BKEMachine Controller 驱动，职责分离 |
| **CAPI 兼容** | 完全遵循 Cluster API 的 Machine 模式，BKEMachine 作为节点状态协调资源 |
| **节点并行** | 所有节点并行执行各自的组件状态机，提升升级效率 |
| **状态聚合** | 组件状态 → 节点状态 → 集群状态，逐层聚合，清晰可观测 |
| **集群层驱动节点层** | node-group 节点在 DAG 中创建/更新 BKEMachine，轮询等待节点状态机完成 |
| **Composite 组件类型** | 新增 composite 类型，将节点级组件嵌入主流程 DAG，统一依赖表达 |
| **可观测性** | 三层状态独立追踪，支持 kubectl 查询、Event 事件、Prometheus 指标 |
| **备份/回滚** | 支持组件级、节点级、集群级三层回滚，复用安装器接口 |
| **升级前预检** | CheckPolicy CRD 独立管理检查策略，支持按环境定制 |

#### 1.6.4 目标架构执行流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    目标架构执行流程 (三层状态机)                                   │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌──────────────────────────────┐
    │  用户声明期望版本            │
    │  ClusterVersion.Spec.        │
    │  DesiredVersion: v2.7.0      │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  BKEClusterReconciler (L1 集群层状态机)                              │
    │  ──────────────────────────────────────────────────────────────────  │
    │  1. 解析 ReleaseImage                                                │
    │     ├─ clusterComponents: [certs, coredns, kube-proxy]               │
    │     └─ nodeComponents: [bkeagent, containerd, kubelet, etcd, ...]    │
    │  2. 构建 DAG                                                         │
    │     └─ [certs] → [node-group] → [coredns] → [kube-proxy]            │
    │  3. 执行 DAG                                                         │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  DAG Batch 1: [certs]                                                │
    │  ──────────────────────────────────────────────────────────────────  │
    │  BinaryExecutor: 下载制品 → 渲染配置 → SSH 执行 → 健康检查           │
    │  L3: certs Installing → Installed                                    │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  DAG Batch 2: [node-group]                                           │
    │  ──────────────────────────────────────────────────────────────────  │
    │  NodeGroupNode.Execute():                                            │
    │  1. 按角色过滤 nodeComponents                                        │
    │     ├─ Master: [bkeagent, containerd, kubelet, etcd, apiserver, ...] │
    │     └─ Worker: [bkeagent, containerd, kubelet, kubectl]              │
    │  2. 创建/更新 BKEMachine (写入 Spec.NodeComponents)                  │
    │  3. waitForNodesReady() → 轮询等待 BKEMachine.Status.Ready           │
    │     │                                                                │
    │     │  BKEMachineReconciler (每个节点独立 Reconcile)                 │
    │     │  ├─ 读取 Spec.NodeComponents                                   │
    │     │  ├─ L2: Pending → Provisioning                                 │
    │     │  ├─ 按依赖顺序执行 L3 组件层状态机                             │
    │     │  │   ├─ bkeagent: Pending → Installing → Installed             │
    │     │  │   ├─ containerd: Pending → Installing → Installed           │
    │     │  │   ├─ kubelet: Pending → Installing → Installed              │
    │     │  │   └─ etcd/apiserver/... (Master only)                       │
    │     │  └─ L2: Provisioning → Ready                                   │
    │     │                                                                │
    │  4. aggregateNodeStatuses() → 聚合节点状态到集群状态                 │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  DAG Batch 3: [coredns]                                              │
    │  ──────────────────────────────────────────────────────────────────  │
    │  HelmExecutor: 拉取 Chart → 渲染 Values → helm install → 健康检查    │
    │  L3: coredns Installing → Installed                                  │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  DAG Batch 4: [kube-proxy]                                           │
    │  ──────────────────────────────────────────────────────────────────  │
    │  HelmExecutor: 拉取 Chart → 渲染 Values → helm install → 健康检查    │
    │  L3: kube-proxy Installing → Installed                               │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  DAG 执行完成                                                        │
    │  ──────────────────────────────────────────────────────────────────  │
    │  BKECluster.Status.LifecyclePhase → Running                          │
    │  BKECluster.Status.CurrentVersion → v2.7.0                           │
    └──────────────────────────────────────────────────────────────────────┘
```

#### 1.6.5 迁移路径

从当前架构到目标架构的迁移分为三个阶段：

| 阶段 | 目标 | 关键任务 |
|------|------|---------|
| **Phase 1** | 基础能力 | 1. BKEMachine Spec/Status 扩展<br>2. BKEMachineReconciler 增加 L2/L3 状态机<br>3. node-group 节点实现 |
| **Phase 2** | 状态迁移 | 1. 节点级组件从 Inline Phase 迁移到 BKEMachine Controller<br>2. 状态聚合机制实现<br>3. 可观测性集成 |
| **Phase 3** | 完整能力 | 1. Composite 组件类型实现<br>2. 备份/回滚三层支持<br>3. CheckPolicy 预检集成 |

---

## 2. 文档清单

| 议题 |
|------|
| binary 组件 |
| 三层状态机 |
| 可观测性 |
| 备份/回滚 |
| 升级前预检 |

---

## 3. A4-1: Binary 组件设计要点

**设计目标**：实现二进制组件的声明式安装/升级，支持制品下载、配置渲染、健康检查。

### 3.1 整体架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          Binary 组件执行架构                                      │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌──────────────────┐
    │  ComponentVersion │
    │  (type: binary)   │
    │  ├── artifacts    │
    │  ├── configTempl. │
    │  ├── installScript│
    │  └── healthCheck  │
    └────────┬─────────┘
             │
             ▼
    ┌──────────────────────────────────────────────────────────────────┐
    │                    BinaryComponentExecutor                        │
    │  ┌────────────────┐  ┌────────────────┐  ┌─────────────────┐    │
    │  │   Artifact     │  │   Template     │  │    Config       │    │
    │  │   Downloader   │  │   Renderer     │  │   Renderer      │    │
    │  │                │  │                │  │                 │    │
    │  │ 下载二进制制品  │  │ 渲染安装脚本   │  │ 渲染配置文件    │    │
    │  │ 校验 checksum  │  │ 50+模板变量    │  │ 3种渲染模式     │    │
    │  └───────┬────────┘  └───────┬────────┘  └────────┬────────┘    │
    │          │                   │                     │             │
    │          └───────────────────┼─────────────────────┘             │
    │                              ▼                                   │
    │                     ┌────────────────┐                           │
    │                     │  SSH Executor  │                           │
    │                     │                │                           │
    │                     │ 上传制品+配置  │                           │
    │                     │ 执行安装脚本   │                           │
    │                     └────────┬───────┘                           │
    │                              │                                   │
    │                              ▼                                   │
    │                     ┌────────────────┐                           │
    │                     │ HealthChecker  │                           │
    │                     │                │                           │
    │                     │ SSH执行检查脚本 │                           │
    │                     │ 验证服务可用   │                           │
    │                     └────────────────┘                           │
    └──────────────────────────────────────────────────────────────────┘
```

### 3.2 核心设计思路

| 维度 | 设计要点 |
|------|---------|
| **制品管理** | 支持 HTTP/本地/OCI 多种制品来源，强制 checksum 校验 |
| **模板变量** | 8 类 50+ 变量：节点信息、集群配置、组件版本、网络配置等 |
| **配置渲染** | 3 种模式：Content（Go template）、Secret（引用 K8s Secret）、Kubeconfig（动态生成）|
| **条件渲染** | 支持 Go template 条件块，按 OS/架构/离线模式动态生成配置 |
| **健康检查** | 安装后通过 SSH 执行脚本验证服务可用性 |

### 3.3 待改造的二进制组件清单

基于当前代码库中的 Phase 实现，以下组件需要从 Inline Phase 改造为 `binary` 类型 ComponentVersion：

#### 3.3.1 节点级二进制组件（需在每个节点上执行）

| 组件名称 | 当前 Phase | 制品 | 适用角色 | 改造优先级 | 说明 |
|---------|-----------|------|---------|-----------|------|
| **bkeagent** | `EnsureBKEAgent` / `EnsureAgentUpgrade` | bkeagent 二进制 + bkeagent.conf + kubeconfig | master, worker | P0 | 节点 Agent，负责节点管理和命令执行 |
| **containerd** | `EnsureContainerdUpgrade` | containerd.tar.gz + config.toml + service | master, worker | P0 | 容器运行时，需支持 docker → containerd 迁移，包含 sandbox_image (pause) 配置 |
| **kubelet** | `EnsureMasterUpgrade` / `EnsureWorkerUpgrade` | kubelet + kubectl + kubelet.conf + service | master, worker | P0 | K8s 节点组件，需按角色分别处理 |
| **kubectl** | `EnsureMasterUpgrade` / `EnsureWorkerUpgrade` | kubectl 二进制 | master, worker | P0 | K8s 命令行工具 |
| **runc** | `EnsureNodesEnv` (update-runc.sh) | runc 二进制 | master, worker | P0 | 容器运行时底层 |
| **helm** | `EnsureNodesEnv` (install-helm.sh) | helm 二进制 | master | P2 | Helm 命令行工具 |
| **etcdctl** | `EnsureNodesEnv` (install-etcdctl.sh) | etcdctl 二进制 | master | P2 | etcd 命令行工具 |
| **calicoctl** | `EnsureNodesEnv` (install-calicoctl.sh) | calicoctl 二进制 | master, worker | P2 | Calico 命令行工具 |
| **lxcfs** | `EnsureNodesEnv` (install-lxcfs.sh) | lxcfs 二进制 + service | master, worker | P2 | 容器文件系统隔离 |
| **nfs-utils** | `EnsureNodesEnv` (install-nfsutils.sh) | nfs-utils 包 | master, worker | P2 | NFS 存储支持 |

#### 3.3.2 Static Pod 类型组件（需改造为 `staticpod` 类型 ComponentVersion）

以下组件通过 Static Pod 方式部署，需要改造为 `staticpod` 类型（详见 `staticpod-type-design.md`）：

| 组件名称 | 当前 Phase | 镜像 | 适用角色 | 改造优先级 | 说明 |
|---------|-----------|------|---------|-----------|------|
| **etcd** | `EnsureEtcdUpgrade` | etcd 镜像 | master | P0 | 分布式 KV 存储 |
| **kube-apiserver** | `EnsureMasterUpgrade` | kube-apiserver 镜像 | master | P0 | K8s API Server |
| **kube-controller-manager** | `EnsureMasterUpgrade` | kube-controller-manager 镜像 | master | P0 | K8s 控制器管理器 |
| **kube-scheduler** | `EnsureMasterUpgrade` | kube-scheduler 镜像 | master | P0 | K8s 调度器 |
| **haproxy** | `EnsureLoadBalance` | haproxy 镜像 | master | P1 | 负载均衡器 |
| **keepalived** | `EnsureLoadBalance` | keepalived 镜像 | master | P1 | VIP 管理 |

#### 3.3.3 改造优先级说明

| 优先级 | 说明 | 组件 |
|-------|------|------|
| **P0** | 核心组件，必须首先改造 | bkeagent, containerd, kubelet, kubectl, runc, etcd, kube-apiserver, kube-controller-manager, kube-scheduler |
| **P1** | 重要组件，第二批改造 | haproxy, keepalived |
| **P2** | 辅助组件，最后改造 | helm, etcdctl, calicoctl, lxcfs, nfs-utils |

#### 3.3.4 改造工作量评估

| 组件类型 | 组件数量 | 改造复杂度 | 预估工作量 |
|---------|---------|-----------|-----------|
| **Binary 类型** | 10 个 | 中等（需编写 installScript + configTemplates） | 2-3 周 |
| **Static Pod 类型** | 6 个 | 较高（需编写 manifestTemplate + 健康检查） | 2-3 周 |
| **总计** | 16 个 | - | 4-6 周 |

### 3.4 支持的组件类型

| 组件 | 制品 | 配置文件 | 说明 |
|------|------|---------|------|
| containerd | containerd.tar.gz | config.toml, service | 容器运行时 |
| bkeagent | bkeagent | bkeagent.conf, kubeconfig | 节点 Agent |
| kubelet/kubectl | kubelet, kubectl | kubelet.conf, service | K8s 核心组件 |

### 3.5 讨论要点

1. 工行现有二进制安装方案如何迁移到 binary 类型 ComponentVersion？
2. 模板变量系统是否满足工行场景需求？
3. 离线环境制品缓存策略如何设计？

---

## 4. A4-2: 三层状态机设计要点

**设计目标**：实现集群/节点/组件三层状态管理，精确控制升级流程。

### 4.1 三层状态机架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          三层状态机执行架构                                       │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  L1: 集群层状态机 (BKECluster Controller 驱动)                          │
    │  ─────────────────────────────────────────────────────────────────────  │
    │  状态: Pending → Installing → Running → Upgrading → RollingBack → Failed│
    │                                                                          │
    │  职责:                                                                   │
    │  • 驱动 DAG 执行                                                         │
    │  • 直接执行集群级组件 (certs, coredns, kube-proxy)                       │
    │  • 通过 node-group 节点驱动节点层状态机                                  │
    │  • 聚合节点状态到集群状态                                                │
    └─────────────────────────────────────────────────────────────────────────┘
                                        │
                                        │ 创建/更新 BKEMachine
                                        │ 等待 BKEMachine.Status.Ready
                                        ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │  L2: 节点层状态机 (BKEMachine Controller 驱动)                          │
    │  ─────────────────────────────────────────────────────────────────────  │
    │  状态: Pending → Provisioning → Ready → Upgrading → Deleting → Failed   │
    │                                                                          │
    │  职责:                                                                   │
    │  • 管理节点生命周期                                                      │
    │  • 按依赖顺序驱动组件层状态机                                            │
    │  • 聚合组件状态到节点状态                                                │
    └─────────────────────────────────────────────────────────────────────────┘
                                        │
                                        │ 按依赖顺序执行
                                        ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │  L3: 组件层状态机 (BKEMachine Controller 驱动)                          │
    │  ─────────────────────────────────────────────────────────────────────  │
    │  状态: Pending → Installing → Installed → Upgrading → Deleting → Failed │
    │                                                                          │
    │  组件执行顺序 (Master 节点示例):                                         │
    │  bkeagent → containerd → kubelet → kubectl                             │
    │                                 └→ etcd → apiserver                    │
    │                                           └→ controller-manager        │
    │                                           └→ scheduler                 │
    └─────────────────────────────────────────────────────────────────────────┘
```

### 4.2 DAG 执行流程

```
BKECluster Controller (L1 集群层状态机)
  │
  ├─ Build DAG (从 ReleaseImage 构建)
  │   ├─ ClusterComponentNode: certs, coredns, kube-proxy
  │   └─ NodeGroupNode: node-group
  │
  └─ Execute DAG (按拓扑排序分批执行)
      │
      ├─ Batch 1: [certs]              ← 集群层组件直接执行 L3
      │   └─ ClusterComponentNode.Execute()
      │
      ├─ Batch 2: [node-group]         ← 触发节点层状态机
      │   └─ NodeGroupNode.Execute()
      │       ├─ 1. 按角色过滤 → 写入 BKEMachine.Spec.NodeComponents
      │       ├─ 2. waitForNodesReady()  ← 轮询等待
      │       │       └─ BKEMachine Controller 独立驱动 L2/L3
      │       └─ 3. aggregateNodeStatuses()
      │
      ├─ Batch 3: [coredns]            ← 集群层组件直接执行 L3
      └─ Batch 4: [kube-proxy]
```

### 4.3 核心设计思路

| 维度 | 设计要点 |
|------|---------|
| **职责分离** | L1 集群层由 BKECluster Controller 驱动，L2/L3 由 BKEMachine Controller 驱动 |
| **CAPI 兼容** | 完全遵循 Cluster API 的 Machine 模式，BKEMachine 作为协调资源 |
| **节点并行** | 所有节点并行执行各自的组件状态机 |
| **依赖管理** | 组件间依赖通过 DAG 拓扑排序保证执行顺序 |
| **状态聚合** | 组件状态 → 节点状态 → 集群状态，逐层聚合 |

### 4.4 讨论要点

1. 三层状态机的状态转换条件如何定义？
2. 节点层状态机与 CAPI Machine 的集成深度？
3. 状态聚合的实时性如何保证？

---

## 5. A4-3: 可观测性设计要点

**设计目标**：实现升级进度实时展示、状态查询、事件日志、指标监控四大能力。

### 5.1 可观测性三层架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          可观测性三层架构                                         │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  Layer 1: 状态可观测                                                    │
    │  ─────────────────────                                                  │
    │  • BKECluster.Status.LifecyclePhase         (集群层状态)                │
    │  • BKEMachine.Status.LifecyclePhase         (节点层状态)                │
    │  • BKEMachine.Status.ComponentStatuses      (组件层状态)                │
    │  • BKEMachine.Status.OperationProgress      (节点操作进度)              │
    │  • Conditions                               (状态条件)                  │
    │                                                                          │
    │  查询方式:                                                               │
    │  kubectl get bkecluster my-cluster -o jsonpath='{.status}'              │
    │  kubectl get bkemachine -l cluster.x-k8s.io/cluster-name=my-cluster    │
    └─────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  Layer 2: 事件可观测                                                    │
    │  ─────────────────────                                                  │
    │  • StateTransition events                   (状态转换事件)              │
    │  • OperationStarted/Completed/Failed events (操作事件)                  │
    │  • ComponentInstalled/Upgraded/Failed       (组件事件)                  │
    │  • BKEMachineCreated/Updated                (BKEMachine 事件)          │
    │                                                                          │
    │  查询方式:                                                               │
    │  kubectl get events --field-selector involvedObject.name=node-1         │
    └─────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  Layer 3: 指标可观测                                                    │
    │  ─────────────────────                                                  │
    │  • bke_cluster_phase_gauge                  (集群状态)                  │
    │  • bke_node_phase_gauge                     (节点状态)                  │
    │  • bke_component_phase_gauge                (组件状态)                  │
    │  • bke_node_ready_count                     (就绪节点数)                │
    │  • bke_component_install_total              (组件安装计数)              │
    │                                                                          │
    │  查询方式:                                                               │
    │  Prometheus + Grafana 仪表盘                                            │
    └─────────────────────────────────────────────────────────────────────────┘
```

### 5.2 状态查询 API 示例

```bash
# 查询集群状态
kubectl get bkecluster my-cluster -o jsonpath='{.status.lifecyclePhase}'
# 输出: Running

# 查询节点状态
kubectl get bkemachine -l cluster.x-k8s.io/cluster-name=my-cluster -o wide
# 输出:
# NAME       ROLE     PHASE   READY   COMPONENTS
# node-1     master   Ready   True    8/8
# node-2     worker   Ready   True    4/4

# 查询节点组件状态
kubectl get bkemachine node-1 -o jsonpath='{.status.componentStatuses}'
# 输出:
# [
#   {"name":"bkeagent","version":"v2.7.0","phase":"Installed"},
#   {"name":"containerd","version":"v1.7.18","phase":"Installed"},
#   {"name":"kubelet","version":"v1.29.0","phase":"Installed"},
#   ...
# ]

# 查询节点操作进度
kubectl get bkemachine node-1 -o jsonpath='{.status.operationProgress}'
# 输出:
# {
#   "operationType":"Upgrade",
#   "currentStage":"Upgrading kubelet",
#   "totalComponents":8,
#   "completedComponents":5
# }
```

### 5.3 核心设计思路

| 维度 | 设计要点 |
|------|---------|
| **状态分层** | 集群/节点/组件三层状态独立追踪，逐层聚合 |
| **事件驱动** | 状态转换自动产生 Event，支持审计和告警 |
| **指标暴露** | Prometheus 格式指标，支持 Grafana 可视化 |
| **进度追踪** | OperationProgress 实时显示当前阶段和完成比例 |

### 5.4 讨论要点

1. 可观测性数据如何与工行现有监控系统集成？
2. 升级进度的实时推送机制（WebSocket/SSE）？
3. 告警规则如何配置？

---

## 6. A4-4: 备份/回滚设计要点

**设计目标**：实现集群级回滚能力，确保运行态与发布版本的一致性。

**设计原则**：
- **仅支持集群级回滚**：不支持组件级和节点级回滚
- **版本一致性**：运行态必须与发布版本（ReleaseImage）保持一致
- **整体回滚**：回滚时整个集群回退到上一稳定版本，所有组件统一回退

### 6.1 集群级回滚架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           集群级回滚架构                                           │
└─────────────────────────────────────────────────────────────────────────────────┘

  ┌────────────────────────────────────────────────────────────────────────────┐
  │  集群级回滚                                                                 │
  │  ─────────────────────────────────────────────────────────────────────────  │
  │                                                                              │
  │  影响范围: 全集群                                                            │
  │  ┌────────────────────────────────────────────────────────────────────┐    │
  │  │  v2.6.0 → v2.5.0                                                   │    │
  │  │  所有组件统一回退到上一版本                                          │    │
  │  │                                                                    │    │
  │  │  触发方式:                                                          │    │
  │  │  • 用户修改 ClusterVersion.Spec.DesiredVersion 回退                │    │
  │  │  • 升级失败后自动触发（FailurePolicy=Rollback）                     │    │
  │  │                                                                    │    │
  │  │  回滚范围:                                                          │    │
  │  │  • 所有集群级组件（certs, coredns, kube-proxy 等）                 │    │
  │  │  • 所有节点级组件（bkeagent, containerd, kubelet, etcd 等）        │    │
  │  │  • 所有节点（master + worker）                                     │    │
  │  └────────────────────────────────────────────────────────────────────┘    │
  │                                                                              │
  │  严重程度: 高                                                                │
  │  恢复时间: 十分钟级                                                          │
  │  影响面:   全集群                                                            │
  └────────────────────────────────────────────────────────────────────────────┘
```

**为什么不支持组件级/节点级回滚**：

| 维度 | 说明 |
|------|------|
| **版本一致性** | 运行态必须与 ReleaseImage 定义保持一致，避免版本混乱 |
| **依赖关系** | 组件间存在复杂依赖，单独回滚某组件可能导致依赖不匹配 |
| **状态管理** | 集群状态以版本为单位管理，不支持细粒度的组件/节点状态 |
| **运维复杂度** | 组件级/节点级回滚会大幅增加运维复杂度和故障排查难度 |

### 6.2 回滚决策流程

```
升级后发现问题
  │
  ├─ 升级过程中失败
  │   └─ FailurePolicy=Rollback 时自动触发集群级回滚
  │       └─ 回滚所有已升级的组件到上一版本
  │
  └─ 升级完成后发现问题
      └─ 用户修改 ClusterVersion.Spec.DesiredVersion 回退
          └─ 触发集群级回滚：所有组件统一回退到目标版本
```

### 6.3 集群级回滚执行流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          集群级回滚执行流程                                       │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌──────────────────────────────┐
    │  1. 触发回滚                 │
    │  • 用户修改 DesiredVersion   │
    │  • 或升级失败自动触发        │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  2. 验证回滚路径             │
    │  UpgradePath: v2.6.0 → v2.5.0│
    │  路径合法性校验               │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  3. 执行 PreCheck            │
    │  • etcd 备份                 │
    │  • 集群健康检查              │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  4. 构建回滚 DAG             │
    │  按升级 DAG 逆序构建         │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────────────────────┐
    │  5. 执行回滚 DAG                                                     │
    │  ──────────────────────────────────────────────────────────────────  │
    │  按拓扑逆序逐批次回滚:                                               │
    │                                                                      │
    │  Batch N: [kube-proxy]                                               │
    │    └─ HelmExecutor: helm rollback → 恢复到上一 Revision              │
    │                                                                      │
    │  Batch N-1: [coredns]                                                │
    │    └─ HelmExecutor: helm rollback → 恢复到上一 Revision              │
    │                                                                      │
    │  Batch N-2: [node-group]                                             │
    │    └─ NodeGroupNode.Rollback():                                      │
    │        ├─ 更新 BKEMachine.Spec.NodeComponents (旧版本)               │
    │        ├─ waitForNodesReady() → 等待所有节点回滚完成                 │
    │        └─ aggregateNodeStatuses() → 聚合节点状态                     │
    │                                                                      │
    │  Batch 1: [certs]                                                    │
    │    └─ 证书通常不需要回滚，跳过或保持当前状态                         │
    └──────────────┬───────────────────────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  6. 执行 PostCheck           │
    │  • 集群健康检查              │
    │  • 版本验证                  │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  7. 更新状态                 │
    │  • ClusterVersion.Status     │
    │    .CurrentVersion → v2.5.0  │
    │  • BKECluster.Status         │
    │    .Phase → Running          │
    └──────────────────────────────┘
```

### 6.4 各类型组件回滚策略

| 组件类型 | 回滚方式 | 说明 |
|---------|---------|------|
| **Binary** | Uninstall(新) + Install(旧) | 停止服务 → 删除二进制 → 安装旧版本 → 启动服务 |
| **Helm** | helm rollback | 恢复到上一 Revision，Helm SDK 原生支持 |
| **YAML** | Apply(旧版本清单) | 加载旧版本清单，SSA Apply 覆盖，Prune 多余资源 |
| **StaticPod** | 替换 Manifest YAML | 渲染旧版本 Manifest → 替换文件 → Kubelet 自动重建 Pod |
| **Inline** | 旧版本 Phase.Execute() | 调用旧版本的 Phase 执行器 |

### 6.5 核心设计思路

| 维度 | 设计要点 |
|------|---------|
| **版本一致性** | 运行态必须与 ReleaseImage 保持一致，不支持部分回滚 |
| **整体回滚** | 回滚时整个集群统一回退，确保组件间依赖关系正确 |
| **复用安装接口** | 回滚不新增安装器接口，复用现有 Install/Uninstall/Rollback 接口 |
| **制品缓存优先** | Binary 组件回滚时优先从本地缓存获取旧版本制品 |
| **独立超时控制** | 回滚操作有独立的超时时间，与升级超时解耦 |
| **数据保全** | 回滚不删除用户数据（etcd 数据、PV 等）|

### 6.6 讨论要点

1. 回滚前的 etcd 备份策略如何设计？
2. Binary 组件的旧版本制品缓存策略？
3. 回滚失败后的兜底方案？

---

## 7. A4-5: 升级前预检设计要点

**设计目标**：实现升级前自动化检查，降低升级失败率。

### 7.1 检查框架架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          检查框架整体架构                                         │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌─────────────────────────────────────────────────────────────────────────┐
    │  CheckPolicy CRD (独立资源)                                              │
    │  ┌─────────────────────────────────────────────────────────────────┐   │
    │  │  CheckPolicy "default" (全局默认)                                │   │
    │  │  ┌──────────────────────────────────────────────────────────┐  │   │
    │  │  │  spec:                                                    │  │   │
    │  │  │    preCheck:                                              │  │   │
    │  │  │      - name: "cluster-health"        required: true       │  │   │
    │  │  │      - name: "backup-verification"   required: true       │  │   │
    │  │  │      - name: "resource-check"        required: false      │  │   │
    │  │  │    postCheck:                                             │  │   │
    │  │  │      - name: "component-version"     required: true       │  │   │
    │  │  │      - name: "cluster-health"        required: true       │  │   │
    │  │  └──────────────────────────────────────────────────────────┘  │   │
    │  └─────────────────────────────────────────────────────────────────┘   │
    │                                                                          │
    │  ┌─────────────────────────────────────────────────────────────────┐   │
    │  │  CheckPolicy "production" (生产环境，priority=10)                │   │
    │  │  ┌──────────────────────────────────────────────────────────┐  │   │
    │  │  │  spec:                                                    │  │   │
    │  │  │    selector:                                              │  │   │
    │  │  │      matchLabels: { environment: production }             │  │   │
    │  │  │    priority: 10                                           │  │   │
    │  │  │    preCheck:                                              │  │   │
    │  │  │      - name: "resource-check"        required: true       │  │   │
    │  │  │      ...                                                  │  │   │
    │  │  └──────────────────────────────────────────────────────────┘  │   │
    │  └─────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘
                                          │
                                          │ 按标签匹配 + 优先级选择
                                          ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │  CheckPolicyResolver (策略解析器)                                        │
    │  ┌─────────────────────────────────────────────────────────────────┐   │
    │  │  1. 列出所有 CheckPolicy                                        │   │
    │  │  2. 按 Selector 筛选匹配当前集群的策略                           │   │
    │  │  3. 按 Priority 排序，选择最高优先级的策略                       │   │
    │  │  4. 若无匹配，使用内置默认策略                                   │   │
    │  └─────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘
                                          │
                                          │ 检查项列表
                                          ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │  CheckRunner (执行引擎)                                                  │
    │  ┌─────────────────────────────────────────────────────────────────┐   │
    │  │  1. 构建 DAG 依赖图                                              │   │
    │  │  2. 拓扑排序，检测循环依赖                                       │   │
    │  │  3. 评估 Condition 条件表达式                                   │   │
    │  │  4. 事件驱动执行：依赖满足即调度                                 │   │
    │  │  5. 支持超时控制和重试                                           │   │
    │  │  6. 生成 CheckReport                                            │   │
    │  └─────────────────────────────────────────────────────────────────┘   │
    └─────────────────────────────────────────────────────────────────────────┘
```

### 7.2 预检项清单

| 检查项 | 检查内容 | 必须通过 | 说明 |
|--------|---------|---------|------|
| **cluster-health** | 所有节点 Ready，所有 Pod Running | 是 | 集群基础健康检查 |
| **backup-verification** | etcd 备份存在且可恢复 | 是 | 备份验证 |
| **resource-check** | CPU/内存/磁盘充足 | 否 | 资源检查 |
| **api-deprecation** | 检查废弃 API 使用 | 是 | API 版本兼容性 |
| **crd-compatibility** | CRD 字段变更检查 | 是 | CRD 兼容性 |
| **dependency-check** | 组件依赖版本检查 | 是 | 依赖关系验证 |

### 7.3 后检项清单

| 检查项 | 检查内容 | 必须通过 | 说明 |
|--------|---------|---------|------|
| **component-version** | 所有组件版本正确 | 是 | 版本验证 |
| **cluster-health** | 所有节点 Ready，所有 Pod Running | 是 | 集群健康检查 |
| **node-ready** | 所有节点 Ready | 是 | 节点状态验证 |
| **application-health** | 关键应用正常运行 | 否 | 应用健康检查 |

### 7.4 核心设计思路

| 维度 | 设计要点 |
|------|---------|
| **解耦设计** | CheckPolicy 独立于 UpgradePath，职责分离 |
| **灵活配置** | 支持按集群标签/环境定制检查策略 |
| **插件化** | 检查项通过注册机制接入，支持动态扩展 |
| **DAG 执行** | 检查项按依赖关系执行，支持并行 |
| **结果报告** | 生成 CheckReport，持久化到 ClusterVersion.Status |

### 7.5 讨论要点

1. 工行有哪些特定的预检需求？
2. 检查项的超时时间和重试策略如何配置？
3. 检查结果如何与升级流程联动（阻断/告警）？

# KEPU-1-Phase4: PhaseFrame 声明式重构 + CVO 升级机制提案
## 元数据
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-1-Phase4 |
| **标题** | PhaseFrame 声明式重构 + CVO 升级机制独立实现 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **父提案** | KEPU-1 |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-20 |
| **最后更新** | 2026-04-20 |
## 1. 摘要
本提案涵盖 KEPU-1 阶段四的完整重构任务，包含三个递进层次：
1. **PhaseFrame 升级 Phase 重构为 ComponentVersion 声明式架构** — 将 PostDeployPhases 中的 7 个升级 Phase 映射为 ComponentVersion + Upgrader 声明式模型
2. **PhaseFrame 安装 Phase 重构为 ComponentVersion 声明式架构** — 将 DeployPhases 中的 11 个安装 Phase 映射为 ComponentVersion + NodeConfig 声明式模型
3. **CVO 升级机制 + ClusterVersion CRD** — 在声明式架构基础上实现声明式升级编排

三个层次存在严格的前置依赖关系：声明式安装架构（层次2）是声明式升级架构（层次1）的基础，两者共同构成 CVO 升级机制（层次3）的运行平台。
## 2. 动机
### 2.1 当前架构核心缺陷
当前升级和安装逻辑分布在 [list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go) 的 `DeployPhases` 和 `PostDeployPhases` 中：
```
DeployPhases (11个安装Phase):
├── EnsureBKEAgent          # SSH推送Agent二进制
├── EnsureNodesEnv          # 节点环境初始化脚本
├── EnsureClusterAPIObj     # 创建Cluster API对象
├── EnsureCerts             # 证书生成
├── EnsureLoadBalance       # HAProxy配置
├── EnsureMasterInit        # kubeadm init
├── EnsureMasterJoin        # kubeadm join --control-plane
├── EnsureWorkerJoin        # kubeadm join
├── EnsureAddonDeploy       # Helm/YAML部署Addon
├── EnsureNodesPostProcess  # 节点后处理
└── EnsureAgentSwitch       # Agent切换目标集群

PostDeployPhases (7个升级Phase):
├── EnsureProviderSelfUpgrade  # Provider Deployment滚动更新
├── EnsureAgentUpgrade         # Agent DaemonSet升级
├── EnsureContainerdUpgrade    # Containerd节点级升级
├── EnsureEtcdUpgrade          # Etcd滚动升级
├── EnsureWorkerUpgrade        # Worker节点滚动升级
├── EnsureMasterUpgrade        # Master节点滚动升级
└── EnsureComponentUpgrade     # openFuyao组件升级
```
**核心问题**：

| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 1 | 安装与升级逻辑耦合 | 安装Phase和升级Phase共享BKECluster状态，无独立版本追踪 | 无法独立管理组件生命周期 |
| 2 | 缺少声明式模型 | Phase通过命令式脚本执行，状态仅存在BKECluster.Status | 无法声明式定义组件期望状态 |
| 3 | 版本状态分散 | `BKEClusterStatus`中仅4个版本字段 | 缺少Agent、Addon、Provider等版本 |
| 4 | 缺少回滚能力 | 升级失败后无法自动回滚 | 只能手动修复 |
| 5 | 升级触发单一 | 仅通过`OpenFuyaoVersion`变化触发 | 无法独立升级单个组件 |
| 6 | 组件依赖隐式 | Phase执行顺序硬编码在list.go | 无法并行、无法动态调整 |
### 2.2 重构必要性
要实现 CVO 声明式升级机制，必须先将 PhaseFrame 的命令式 Phase 重构为 ComponentVersion 声明式架构，原因：
1. **ClusterVersion 需要组件级版本追踪**：当前 BKECluster.Status 仅有4个版本字段，CVO 需要追踪所有组件的版本状态
2. **DAG 调度需要组件依赖声明**：当前 Phase 依赖隐式定义在执行顺序中，DAG 调度器需要显式依赖声明
3. **Upgrader 需要组件级执行接口**：当前 Phase 的 `Execute()` 接口过于通用，Upgrader 需要组件特定的安装/升级/回滚接口
4. **NodeConfig 需要节点级配置分离**：当前节点配置嵌入在 BKECluster.Spec 中，NodeConfig 需要独立管理
## 3. 与其他阶段的依赖分析
### 3.1 逐阶段依赖评估
| 阶段 | 依赖程度 | 说明 |
|------|---------|------|
| **阶段一**（统一错误处理 + CRD 扩展） | **不依赖** | 本提案可自行定义 `UpgradeError` 类型；`api/bkecommon/v1beta1/` 现有类型已满足需求 |
| **阶段二**（状态机引擎） | **不依赖** | ClusterVersion Controller 拥有独立状态机（Available→Progressing→Degraded→RollingBack），不嵌入阶段二的状态机框架 |
| **阶段三**（Provider 分离） | **不依赖** | ComponentVersion/NodeConfig 是全新CRD，不依赖 Bootstrap/ControlPlane Provider 分离 |
| **阶段五**（OSProvider） | **不依赖** | OS抽象是节点操作层，与组件编排层正交；ComponentVersion 的 `compatibility.os` 字段可先硬编码 |
| **阶段六**（版本包管理 + Asset） | **弱依赖** | 版本兼容性校验可先实现简化版（`CompatibilityRule`），后续迁移到 VersionPackage CRD |
| **阶段七**（清理 + 测试） | **不依赖** | 清理是后续工作 |
### 3.2 最小化依赖集
本提案仅依赖**当前代码库中已存在**的组件：

| 依赖项 | 来源 | 用途 |
|--------|------|------|
| `BKECluster` CRD | `api/capbke/v1beta1/` | ClusterVersion 通过 `clusterRef` 关联 |
| `BKEClusterStatus` | `api/bkecommon/v1beta1/` | 读取当前版本信息 |
| PhaseFlow 执行引擎 | `pkg/phaseframe/` | 过渡期复用现有 Phase 核心函数 |
| `controller-runtime` | 已有依赖 | Reconciler 框架 |
| `kubebuilder` | 已有依赖 | CRD 生成 |
### 3.3 依赖关系图
```
┌─────────────────────────────────────────────────────────────────┐
│                    阶段依赖关系图                                │
│                                                                 │
│  阶段一 ──→ 阶段二 ──→ 阶段三    （Provider 分离链路）          │
│                                                                 │
│  阶段五                         （OS 抽象，独立）               │
│                                                                 │
│  阶段六                         （版本包管理，弱依赖）           │
│                                                                 │
│  ★ 本提案 ★ = {层次1 + 层次2 + 层次3}  （独立实现）            │
│                                                                 │
│  阶段七                         （清理，依赖所有阶段完成）       │
│                                                                 │
│  结论：本提案无前置依赖，可独立实现                             │
└─────────────────────────────────────────────────────────────────┘
```
## 4. 提案
### 4.1 总体架构

```
┌──────────────────────────────────────────────────────────────────────┐
│                         重构后架构                                   │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │              BKEClusterReconciler（精简后）                    │  │
│  │  ├── 控制类逻辑（Finalizer/Paused/DryRun/Delete/Manage）       │  │
│  │  └── ClusterOrchestrator.Reconcile()                           │  │
│  └────────────────────────────┬───────────────────────────────────┘  │
│                               │                                      │
│  ┌────────────────────────────▼───────────────────────────────────┐  │
│  │              ClusterOrchestrator（声明式编排）                 │  │
│  │  ├── syncDesiredState()→生成/更新 ComponentVersion + NodeConfig│  │
│  │  ├── DAGScheduler.Schedule() → 计算就绪/阻塞/运行中组件        │  │
│  │  └── executeComponent() → 调用 ComponentExecutor               │  │
│  └───────────────────────────┬────────────────────────────────────┘  │
│                              │                                       │
│          ┌───────────────────┼───────────────────┐                   │
│          │                   │                   │                   │
│  ┌───────▼────────┐  ┌───────▼────────┐  ┌───────▼────────┐          │
│  │ComponentVersion│  │ComponentVersion│  │ ClusterVersion │          │
│  │  (安装/升级)   │  │  (安装/升级)   │  │  (全局版本)    │          │
│  └───────┬────────┘  └────────┬───────┘  └─────────┬──────┘          │
│          │                    │                    │                 │
│  ┌───────▼─────────┐  ┌───────▼─────────┐  ┌───────▼───────────┐     │
│  │ComponentExecutor│  │ComponentExecutor│  │UpgradeOrchestrator│     │
│  │  (安装执行器)   │  │  (升级执行器)   │  │  (升级编排器)     │     │
│  └─────────────────┘  └─────────────────┘  └───────────────────┘     │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │              NodeConfig[]（节点级配置）                        │  │
│  │  ├── node-192.168.1.10 (master)                                │  │
│  │  ├── node-192.168.1.11 (master)                                │  │
│  │  └── node-192.168.1.20 (worker)                                │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```
### 4.2 层次一：升级 Phase → ComponentVersion 声明式架构
#### 4.2.1 升级 Phase 映射关系
| 现有升级 Phase | 目标 ComponentVersion | Scope | 依赖 |
|---------------|----------------------|-------|------|
| `EnsureProviderSelfUpgrade` | `bkeProvider` | Cluster | 无 |
| `EnsureAgentUpgrade` | `bkeAgent` | Node | bkeProvider |
| `EnsureContainerdUpgrade` | `containerd` | Node | bkeAgent |
| `EnsureEtcdUpgrade` | `etcd` | Node | bkeAgent |
| `EnsureMasterUpgrade` | 合并到 `kubernetes` | Node | containerd, etcd |
| `EnsureWorkerUpgrade` | 合并到 `kubernetes` | Node | containerd, etcd |
| `EnsureComponentUpgrade` | `openFuyao` | Cluster | bkeAgent |

**关键设计决策**：`EnsureMasterUpgrade` 和 `EnsureWorkerUpgrade` 合并为单个 `kubernetes` ComponentVersion，因为它们操作同一组二进制（kubeadm/kubelet/kubectl），仅节点角色不同。
#### 4.2.2 升级组件依赖 DAG
```
                    ┌─────────────────┐
                    │  BKEProvider    │ ← 最先升级
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │   BKEAgent      │
                    │   (daemonset)   │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───────┐      │     ┌────────▼───────┐
     │   Containerd   │      │     │   OpenFuyao    │
     │   (node-level) │      │     │  (components)  │
     └────────┬───────┘      │     └────────────────┘
              │              │
     ┌────────▼───────┐      │
     │     Etcd       │      │
     │  (data plane)  │      │
     └────────┬───────┘      │
              │              │
              └──────┬───────┘
                     │
           ┌─────────▼──────────┐
           │    Kubernetes      │
           │ (control-plane +   │
           │  worker nodes)     │
           └────────────────────┘
```
#### 4.2.3 Upgrader 接口设计
```go
// pkg/cvo/upgrader/interface.go
package upgrader

type Upgrader interface {
    Name() ComponentName
    Scope() ComponentScope
    Dependencies() []ComponentName
    NeedUpgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) (bool, error)
    Upgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error)
    Rollback(ctx context.Context, cv *cvov1beta1.ClusterVersion) error
    HealthCheck(ctx context.Context, cv *cvov1beta1.ClusterVersion) (bool, error)
    CurrentVersion(ctx context.Context, cv *cvov1beta1.ClusterVersion) (string, error)
}
```
#### 4.2.4 KubernetesUpgrader 内部步骤状态机
Kubernetes 组件是最复杂的升级对象，需要内部步骤状态机：
```
PreCheck → EtcdBackup → MasterRollout(逐节点) → WorkerRollout(逐节点) → PostCheck
```

```go
type K8sUpgradeStep string

const (
    K8sStepPreCheck      K8sUpgradeStep = "PreCheck"
    K8sStepEtcdBackup    K8sUpgradeStep = "EtcdBackup"
    K8sStepMasterRollout K8sUpgradeStep = "MasterRollout"
    K8sStepWorkerRollout K8sUpgradeStep = "WorkerRollout"
    K8sStepPostCheck     K8sUpgradeStep = "PostCheck"
)
```
#### 4.2.5 升级 Phase 重构实现策略
各 Upgrader **复用现有 Phase 的核心函数**，通过适配层桥接：

| Upgrader | 复用的现有函数 | 适配方式 |
|----------|--------------|---------|
| `BKEProviderUpgrader` | `phaseutil.DeploymentTarget` + `waitForDeploymentReady` | 直接调用 |
| `BKEAgentUpgrader` | `EnsureAgentUpgrade.rolloutAgentDaemonSet` | 提取为公共函数 |
| `ContainerdUpgrader` | `EnsureContainerdUpgrade.getCommand` + `rolloutContainerd` | 提取为公共函数 |
| `EtcdUpgrader` | `EnsureEtcdUpgrade` 的逐节点升级逻辑 | 提取为公共函数 |
| `KubernetesUpgrader` | `EnsureMasterUpgrade.executeNodeUpgrade` + `EnsureWorkerUpgrade.drain+upgrade+uncordon` | 提取为公共函数 |
| `OpenFuyaoComponentUpgrader` | `EnsureComponentUpgrade.rolloutOpenfuyaoComponent` | 提取为公共函数 |
### 4.3 层次二：安装 Phase → ComponentVersion 声明式架构
#### 4.3.1 安装 Phase 映射关系
| 现有安装 Phase | 目标 ComponentVersion | Scope | 依赖 |
|---------------|----------------------|-------|------|
| `EnsureBKEAgent` | `bkeAgent` | Node | 无 |
| `EnsureNodesEnv` | `nodesEnv` | Node | bkeAgent |
| `EnsureClusterAPIObj` | `clusterAPI` | Cluster | bkeAgent, nodesEnv |
| `EnsureCerts` | `certs` | Cluster | clusterAPI |
| `EnsureLoadBalance` | `loadBalancer` | Node | certs |
| `EnsureMasterInit` | 合并到 `kubernetes` | Node | loadBalancer |
| `EnsureMasterJoin` | 合并到 `kubernetes` | Node | kubernetes(MasterInit) |
| `EnsureWorkerJoin` | 合并到 `kubernetes` | Node | kubernetes(MasterJoin) |
| `EnsureAddonDeploy` | `addon` | Cluster | kubernetes |
| `EnsureNodesPostProcess` | `nodesPostProcess` | Node | addon |
| `EnsureAgentSwitch` | `agentSwitch` | Cluster | nodesPostProcess |
#### 4.3.2 安装组件依赖 DAG

```
                    ┌─────────────────┐
                    │  BKEAgent       │ ← 最先安装
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───────┐      │     ┌────────▼───────┐
     │   NodesEnv     │      │     │  ClusterAPI    │
     └────────┬───────┘      │     └────────┬───────┘
              │              │              │
              │              │     ┌────────▼───────┐
              │              │     │    Certs       │
              │              │     └────────┬───────┘
              │              │              │
              │     ┌────────▼──────────────▼───────┐
              │     │       LoadBalancer            │
              │     └────────┬──────────────────────┘
              │              │
              │     ┌────────▼──────────────────────┐
              │     │    Kubernetes (Init/Join)     │
              │     └────────┬──────────────────────┘
              │              │
              │     ┌────────▼───────┐
              │     │     Addon      │
              │     └────────┬───────┘
              │              │
              │     ┌────────▼──────────┐
              │     │  NodesPostProcess │
              │     └────────┬──────────┘
              │              │
              └──────┬───────┘
                     │
           ┌─────────▼──────────┐
           │   AgentSwitch      │
           └────────────────────┘
```
#### 4.3.3 ComponentExecutor 接口设计
```go
// pkg/orchestrator/executor/interface.go
package executor

type ComponentExecutor interface {
    Name() ComponentName
    Scope() ComponentScope
    Install(ctx context.Context, cv *v1alpha1.ComponentVersion, ncs []*v1alpha1.NodeConfig) (ctrl.Result, error)
    Upgrade(ctx context.Context, cv *v1alpha1.ComponentVersion, ncs []*v1alpha1.NodeConfig) (ctrl.Result, error)
    Rollback(ctx context.Context, cv *v1alpha1.ComponentVersion, ncs []*v1alpha1.NodeConfig) error
    HealthCheck(ctx context.Context, cv *v1alpha1.ComponentVersion, ncs []*v1alpha1.NodeConfig) (bool, error)
}
```
#### 4.3.4 Kubernetes ComponentVersion 的安装步骤状态机
Kubernetes 组件合并了 MasterInit、MasterJoin、WorkerJoin，内部步骤：
```
MasterInit → MasterJoin(逐节点) → WorkerJoin(逐节点)
```

```go
type K8sInstallStep string

const (
    K8sStepMasterInit K8sInstallStep = "MasterInit"
    K8sStepMasterJoin K8sInstallStep = "MasterJoin"
    K8sStepWorkerJoin K8sInstallStep = "WorkerJoin"
)
```
#### 4.3.5 控制类 Phase 处理
控制类 Phase（EnsureFinalizer、EnsurePaused、EnsureClusterManage、EnsureDeleteOrReset、EnsureDryRun）**不映射为 ComponentVersion**，保留在 BKECluster Controller 中：
```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // === 控制类逻辑（保留在 Controller 中） ===
    // 1. EnsureFinalizer
    // 2. EnsurePaused
    // 3. EnsureDryRun
    // 4. EnsureDeleteOrReset
    // 5. EnsureClusterManage

    // === 声明式组件编排（由 ClusterOrchestrator 接管） ===
    return r.orchestrator.Reconcile(ctx, bkeCluster)
}
```
#### 4.3.6 缩容 Phase 处理
EnsureWorkerDelete 和 EnsureMasterDelete **不映射为 ComponentVersion**，通过 NodeConfig 的 `status.phase = Deleting` 触发缩容流程。
### 4.4 层次三：CVO 升级机制 + ClusterVersion CRD
#### 4.4.1 ClusterVersion CRD
```go
// api/cvo/v1beta1/clusterversion_types.go
package v1beta1

type ClusterVersionPhase string

const (
    ClusterVersionPhaseAvailable   ClusterVersionPhase = "Available"
    ClusterVersionPhaseProgressing ClusterVersionPhase = "Progressing"
    ClusterVersionPhaseDegraded    ClusterVersionPhase = "Degraded"
    ClusterVersionPhaseRollingBack ClusterVersionPhase = "RollingBack"
)

type ClusterVersionSpec struct {
    DesiredVersion           string            `json:"desiredVersion"`
    DesiredComponentVersions ComponentVersions `json:"desiredComponentVersions,omitempty"`
    ClusterRef               *ClusterReference `json:"clusterRef,omitempty"`
    UpgradeStrategy          UpgradeStrategy   `json:"upgradeStrategy,omitempty"`
    Pause                    bool              `json:"pause,omitempty"`
    AllowDowngrade           bool              `json:"allowDowngrade,omitempty"`
}

type ComponentVersions struct {
    Kubernetes string            `json:"kubernetes,omitempty"`
    Etcd       string            `json:"etcd,omitempty"`
    Containerd string            `json:"containerd,omitempty"`
    OpenFuyao  string            `json:"openFuyao,omitempty"`
    BKEAgent   string            `json:"bkeAgent,omitempty"`
    BKEProvider string           `json:"bkeProvider,omitempty"`
    Extra      map[string]string `json:"extra,omitempty"`
}

type UpgradeStrategy struct {
    Type                   UpgradeStrategyType `json:"type,omitempty"`
    MaxParallelNodes       int                 `json:"maxParallelNodes,omitempty"`
    BatchInterval          metav1.Duration     `json:"batchInterval,omitempty"`
    RollbackOnFailure      bool                `json:"rollbackOnFailure,omitempty"`
    PreUpgradeHealthCheck  *HealthCheckConfig  `json:"preUpgradeHealthCheck,omitempty"`
    PostUpgradeHealthCheck *HealthCheckConfig  `json:"postUpgradeHealthCheck,omitempty"`
}

type ClusterVersionStatus struct {
    CurrentVersion           string                       `json:"currentVersion,omitempty"`
    CurrentComponentVersions ComponentVersions            `json:"currentComponentVersions,omitempty"`
    Phase                    ClusterVersionPhase          `json:"phase,omitempty"`
    Conditions               []metav1.Condition           `json:"conditions,omitempty"`
    History                  []UpgradeHistory             `json:"history,omitempty"`
    CurrentUpgrade           *CurrentUpgrade              `json:"currentUpgrade,omitempty"`
    ComponentStatuses        map[string]ComponentVersionStatus `json:"componentStatuses,omitempty"`
    ObservedGeneration       int64                        `json:"observedGeneration,omitempty"`
}

type ComponentVersionStatus struct {
    Name      ComponentName  `json:"name"`
    Current   string         `json:"current,omitempty"`
    Desired   string         `json:"desired,omitempty"`
    Phase     ComponentPhase `json:"phase,omitempty"`
    Message   string         `json:"message,omitempty"`
    UpdatedAt *metav1.Time   `json:"updatedAt,omitempty"`
}

type CurrentUpgrade struct {
    TargetVersion           string        `json:"targetVersion,omitempty"`
    TargetComponentVersions ComponentVersions `json:"targetComponentVersions,omitempty"`
    StartTime               *metav1.Time  `json:"startTime,omitempty"`
    Steps                   []UpgradeStep `json:"steps,omitempty"`
    CurrentStepIndex        int           `json:"currentStepIndex,omitempty"`
    Progress                int           `json:"progress,omitempty"`
    Failure                 *UpgradeFailure `json:"failure,omitempty"`
}

type UpgradeStep struct {
    Name           UpgradeStepType `json:"name"`
    State          StepState       `json:"state"`
    StartTime      *metav1.Time    `json:"startTime,omitempty"`
    CompletionTime *metav1.Time    `json:"completionTime,omitempty"`
    Message        string          `json:"message,omitempty"`
}

type UpgradeStepType string

const (
    UpgradeStepPreCheck     UpgradeStepType = "PreCheck"
    UpgradeStepProviderSelf UpgradeStepType = "ProviderSelfUpgrade"
    UpgradeStepAgent        UpgradeStepType = "AgentUpgrade"
    UpgradeStepContainerd   UpgradeStepType = "ContainerdUpgrade"
    UpgradeStepEtcd         UpgradeStepType = "EtcdUpgrade"
    UpgradeStepControlPlane UpgradeStepType = "ControlPlaneUpgrade"
    UpgradeStepWorker       UpgradeStepType = "WorkerUpgrade"
    UpgradeStepComponent    UpgradeStepType = "ComponentUpgrade"
    UpgradeStepPostCheck    UpgradeStepType = "PostCheck"
)
```
#### 4.4.2 ClusterVersionReconciler 状态机
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
#### 4.4.3 ClusterOrchestrator 调谐逻辑
```go
// pkg/orchestrator/cluster_orchestrator.go

func (o *ClusterOrchestrator) Reconcile(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // 1. 根据 BKECluster Spec 生成/更新 ComponentVersion 和 NodeConfig 列表
    if err := o.syncDesiredState(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }

    // 2. 获取所有 ComponentVersion
    components, err := o.listComponentVersions(ctx, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 3. DAG 调度：计算就绪、阻塞、运行中的组件
    completed := o.getCompletedComponents(components)
    scheduleResult := o.scheduler.Schedule(components, completed)

    // 4. 执行就绪组件的安装/升级
    for _, comp := range scheduleResult.Ready {
        executor := o.executors[comp.Spec.ComponentName]
        result, err := o.executeComponent(ctx, executor, comp)
        if err != nil {
            return result, err
        }
    }

    // 5. 同步状态回 BKECluster
    if err := o.syncStatusToBKECluster(ctx, bkeCluster, components); err != nil {
        return ctrl.Result{}, err
    }

    // 6. 判断是否全部完成
    if len(scheduleResult.Ready) == 0 && len(scheduleResult.Blocked) == 0 
       && len(scheduleResult.Running) == 0 {
        return ctrl.Result{}, nil
    }

    return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
```
#### 4.4.4 版本信息流转
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
Upgrader 执行升级，更新 ClusterVersion.Status.ComponentStatuses
         │
         ▼
同步回 BKECluster.Status 的版本字段
```
#### 4.4.5 资源关系总览
```
BKECluster (Owner)
    │
    ├── ClusterVersion (1:1) ─── 全局版本状态
    │
    ├── ComponentVersion[] (1:N) ─── 各组件版本定义
    │   ├── bkeAgent-v1.0.0
    │   ├── nodesEnv-v1.0.0
    │   ├── clusterAPI-v1.0.0
    │   ├── certs-v1.0.0
    │   ├── loadBalancer-v1.0.0
    │   ├── kubernetes-v1.29.0
    │   ├── containerd-v1.7.2
    │   ├── etcd-v3.5.12
    │   ├── addon-v1.0.0
    │   ├── openFuyao-v1.1.0
    │   ├── bkeProvider-v1.1.0
    │   ├── nodesPostProcess-v1.0.0
    │   └── agentSwitch-v1.0.0
    │
    └── NodeConfig[] (1:N) ─── 节点级配置
        ├── node-192.168.1.10 (master)
        ├── node-192.168.1.11 (master)
        └── node-192.168.1.20 (worker)
```
## 5. 迁移策略
### 5.1 四阶段渐进式迁移
| 阶段 | 目标 | 改动范围 | Feature Gate |
|------|------|----------|-------------|
| **Phase A** | 定义 CRD + Controller 骨架，不修改现有 PhaseFlow | 新增 `api/cvo/`、`api/nodecomponent/`、`pkg/cvo/`、`pkg/orchestrator/` | `DeclarativeOrchestration=false` |
| **Phase B** | 实现 ClusterOrchestrator + DAGScheduler + 各 ComponentExecutor/Upgrader，内部复用现有 Phase 核心函数 | 新增执行器实现，提取现有 Phase 核心函数为公共函数 | `DeclarativeOrchestration=true`（可选开启） |
| **Phase C** | 移除 PhaseFlow 中的 DeployPhases 和 PostDeployPhases，完全由声明式编排接管 | 修改 `phase_flow.go`，移除旧 Phase | `DeclarativeOrchestration=true`（默认开启） |
| **Phase D** | 清理旧 Phase 代码，统一版本状态到 ClusterVersion | 移除旧 Phase 文件 | 不可逆 |
### 5.2 双轨运行架构
过渡期（Phase B）支持新旧路径并行：
```go
func (p *PhaseFlow) Execute() (ctrl.Result, error) {
    defer p.handlePanic()
    
    if p.useDeclarativeOrchestration {
        return p.orchestrator.Reconcile(p.ctx)
    }
    
    // 原有逻辑
    phases := p.determinePhases()
    go p.ctx.WatchBKEClusterStatus()
    return p.executePhases(phases)
}
```
## 6. 目录结构
```
cluster-api-provider-bke/
├── api/
│   ├── cvo/v1beta1/                          # 新增：CVO API
│   │   ├── clusterversion_types.go
│   │   ├── groupversion_info.go
│   │   └── zz_generated.deepcopy.go
│   └── nodecomponent/v1alpha1/               # 新增：组件 API
│       ├── componentversion_types.go
│       ├── nodeconfig_types.go
│       ├── groupversion_info.go
│       └── zz_generated.deepcopy.go
├── controllers/
│   └── cvo/                                  # 新增：CVO Controller
│       └── clusterversion_controller.go
├── pkg/
│   ├── cvo/                                  # 新增：CVO 核心
│   │   ├── orchestrator/
│   │   │   └── orchestrator.go
│   │   ├── scheduler/
│   │   │   └── dag_scheduler.go
│   │   ├── upgrader/
│   │   │   ├── interface.go
│   │   │   ├── provider_upgrader.go
│   │   │   ├── agent_upgrader.go
│   │   │   ├── containerd_upgrader.go
│   │   │   ├── etcd_upgrader.go
│   │   │   ├── kubernetes_upgrader.go
│   │   │   └── openfuyao_upgrader.go
│   │   ├── validator/
│   │   │   └── version_validator.go
│   │   └── rollback/
│   │       └── rollback_manager.go
│   └── orchestrator/                         # 新增：安装编排
│       ├── cluster_orchestrator.go
│       ├── executor/
│       │   ├── interface.go
│       │   ├── bke_agent_executor.go
│       │   ├── nodes_env_executor.go
│       │   ├── cluster_api_executor.go
│       │   ├── certs_executor.go
│       │   ├── loadbalancer_executor.go
│       │   ├── kubernetes_executor.go
│       │   ├── addon_executor.go
│       │   ├── nodes_postprocess_executor.go
│       │   └── agent_switch_executor.go
│       └── scheduler/
│           └── dag_scheduler.go
```
## 7. 实施计划
| 步骤 | 内容 | 前置条件 | 周期 |
|------|------|---------|------|
| **S1** | ComponentVersion + NodeConfig CRD 定义 + DeepCopy | 无 | 3天 |
| **S2** | ClusterVersion CRD 定义 + DeepCopy | 无 | 2天 |
| **S3** | DAGScheduler 实现 | S1 | 3天 |
| **S4** | 安装 ComponentExecutor 实现（11个） | S1, S3 | 8天 |
| **S5** | ClusterOrchestrator 安装编排实现 | S3, S4 | 5天 |
| **S6** | 升级 Upgrader 实现（7个） | S1, S3 | 8天 |
| **S7** | ClusterVersionReconciler + 状态机 | S2, S6 | 5天 |
| **S8** | VersionValidator（简化版） | S2 | 3天 |
| **S9** | RollbackManager | S7 | 3天 |
| **S10** | 双轨运行 + Feature Gate | S5, S7 | 3天 |
| **S11** | 集成测试 + E2E 测试 | S10 | 5天 |
| **总计** | | | **48天** |
## 8. 风险评估
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 安装Phase重构范围大（11个Phase） | 高 | 分批实现：先实现节点级（4个），再实现集群级（7个） |
| 升级过程中状态不一致 | 高 | ClusterVersion CR 持久化升级进度，支持断点续升 |
| 现有Phase核心函数提取困难 | 中 | 优先复用，必要时通过适配层包装 |
| DAG依赖关系变更导致升级顺序错误 | 高 | 依赖关系通过CRD声明式配置，支持运行时更新 |
| 并行升级导致集群不可用 | 中 | KubernetesUpgrader内部仍串行执行（先Master后Worker） |
| 回滚失败 | 高 | 回滚前强制etcd备份，回滚操作幂等设计 |
| 双轨运行期间状态冲突 | 中 | Feature Gate严格互斥，同一时刻仅一条路径生效 |
## 9. 验收标准
1. **层次一验收**：7个升级Phase全部重构为Upgrader，升级行为与原Phase一致
2. **层次二验收**：11个安装Phase全部重构为ComponentExecutor，安装行为与原Phase一致
3. **层次三验收**：
   - ClusterVersion CRD 创建后可自动检测版本差异并触发升级
   - 升级流程支持 PreCheck → 升级步骤 → PostCheck 完整链路
   - 升级失败且 `rollbackOnFailure=true` 时自动触发回滚
   - 升级历史可追溯（`status.history` 记录完整）
4. **整体验收**：
   - 所有现有功能通过集成测试验证，无回归
   - DAG调度正确处理组件依赖关系
   - Feature Gate 可平滑切换新旧路径
   - ComponentVersion + NodeConfig 声明式模型覆盖所有安装和升级场景
        

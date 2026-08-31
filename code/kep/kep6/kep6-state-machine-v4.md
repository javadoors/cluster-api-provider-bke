# KEP-6 状态机设计 v4：基于 DAG 的三层状态机引擎

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-6 |
| **标题** | 基于 DAG 的三层状态机引擎设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-24 |
| **依赖** | KEP-5 声明式升级框架、Cluster API v1beta1 |

---

## 1. 设计目标与约束

### 1.1 设计目标

1. **统一执行入口**：BKECluster Reconcile 触发集群层状态机引擎执行
2. **DAG 驱动执行**：集群层状态机从 ReleaseImage 构建执行 DAG
3. **节点级并行**：节点级组件在 DAG 中聚合为节点组节点，多节点并行执行
4. **三层状态清晰**：集群层、节点层、组件层状态定义清晰，职责分明
5. **可观测性**：状态转换、执行进度、健康状态全链路可观测
6. **CAPI 集成**：架构设计与 Cluster API 天然兼容

### 1.2 设计约束

| 约束 | 说明 |
|------|------|
| **节点级组件相邻** | 在 DAG 中，节点级组件聚合为一个"节点组"节点，保持 DAG 拓扑简洁 |
| **节点级并行** | BinaryComponentExecutor 内部支持 Rolling/Parallel/Batch 节点级并发 |
| **组件级状态机** | 每个节点的每个组件通过组件级状态机引擎驱动安装/升级/卸载 |
| **幂等性** | 所有状态转换和操作必须幂等，支持 Reconcile 重入 |
| **CAPI 兼容** | 状态机引擎与 CAPI Controller 的 Reconcile 模式兼容 |

---

## 2. 整体架构

### 2.1 架构总览

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        BKEClusterReconciler.Reconcile()                          │
│                                                                                 │
│  1. Get BKECluster                                                              │
│  2. PatchHelper (defer Patch)                                                   │
│  3. engine.Execute(ctx, cluster)  ──────────────────────────────────────┐       │
│  4. SyncLegacyFields(cluster)                                           │       │
│  5. RecordMetrics(cluster)                                              │       │
│  6. return decideRequeue(cluster)                                       │       │
└─────────────────────────────────────────────────────────────────────────┼───────┘
                                                                        │
                                                                        ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                     L1: ClusterStateMachine (集群层状态机)                        │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  1. EvaluateClusterPhase(cluster)                                       │   │
│  │     └─ 根据 OperationProgress 判断集群生命周期阶段                        │   │
│  │        Pending / Installing / Running / Upgrading / Scaling / Failed    │   │
│  │                                                                          │   │
│  │  2. BuildDAG(releaseImage)                                               │   │
│  │     └─ 从 ReleaseImage 构建执行 DAG                                      │   │
│  │                                                                          │   │
│  │  3. ExecuteDAG(ctx, dag)                                                 │   │
│  │     └─ 按拓扑顺序执行 DAG 节点                                           │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 DAG 结构设计

**核心设计**：DAG 中所有组件均为统一的 `ComponentNode`，不再区分集群级/节点级节点类型。K8s 核心组件和节点二进制组件通过 `composite` 类型（KEP-15）封装，DAG 构建时自动展开为子组件节点。

**设计思路 — 为什么移除 ClusterComponentNode 和 NodeGroupNode**：

1. **yaml/helm 本身就是集群类型**：yaml/helm 组件通过 K8s API 或 Helm SDK 部署到目标集群，天然是集群级操作，无需额外的 `ClusterComponentNode` 包装。
2. **二进制组件本身就是 Node 类型**：binary 组件通过 SSH 在节点上执行，`BinaryComponentExecutor` 内部已支持 Rolling/Parallel/Batch 节点级并发策略，无需 `NodeGroupNode` 包装。
3. **composite 封装更自然**：K8s 核心组件（etcd/apiserver/cm/scheduler/kubelet/kubectl/kube-proxy）和节点二进制组件（bkeagent/containerd）通过 `composite` 类型组合管理，DAG 构建时展开为独立子组件节点，各自按依赖关系参与拓扑排序。

```
ReleaseImage 组件列表:
  - kubernetes-core (type=composite, 含 K8s 核心组件)
  - node-binaries (type=composite, 含节点二进制组件)
  - coredns (type=helm)
  - kube-proxy (type=yaml)

DAG 构建时展开 composite:

  ┌────────┐   ┌──────────┐   ┌──────────────────┐   ┌──────────┐   ┌────────────┐
  │ certs  │──>│ bkeagent │──>│    containerd    │──>│ kubelet  │──>│ kube-proxy │
  │(staticpod)  │(binary)  │   │    (binary)      │   │(binary)  │   │ (yaml)     │
  └────────┘   └──────────┘   └──────────────────┘   └──────────┘   └────────────┘
                                  │                        │
                                  ▼                        ▼
                              ┌──────────┐           ┌──────────────────┐
                              │ kube-    │           │ kube-controller- │
                              │ apiserver│           │ manager          │
                              │(staticpod)          │(staticpod)       │
                              └──────────┘           └──────────────────┘
                                  │                        │
                                  ▼                        ▼
                              ┌──────────┐           ┌──────────────────┐
                              │ etcd     │           │ kube-scheduler   │
                              │(staticpod)          │(staticpod)       │
                              └──────────┘           └──────────────────┘

  ┌──────────────────┐
  │     coredns      │  (helm, 集群级部署)
  │  依赖 kubelet    │
  └──────────────────┘

组件执行顺序由 ComponentVersion.spec.dependencies 定义:
  - etcd → kube-apiserver → kube-controller-manager / kube-scheduler
  - bkeagent → containerd → kubelet → kubectl
  - kubelet → kube-proxy / coredns (集群级组件依赖节点级组件就绪)

节点级并发由 BinaryComponentExecutor.upgradeStrategy.mode 控制:
  - Rolling: 逐节点执行 (containerd 等高风险组件)
  - Batch: 分批执行 (bkeagent 等中风险组件)
  - Parallel: 全节点并行 (低风险配置更新)
```

### 2.3 DAG 节点类型

DAG 中只有一种节点类型 `ComponentNode`，每个组件（无论 binary/yaml/helm/staticpod）都是独立的 DAG 节点。`composite` 类型在 DAG 构建时展开为子组件节点，自身不产生 DAG 节点。

| 组件类型 | 执行方式 | 节点级并发 | 说明 |
|---------|---------|-----------|------|
| **binary** | SSH 在节点上执行 | Rolling/Parallel/Batch | 由 BinaryComponentExecutor 控制逐节点/分批/全并行 |
| **staticpod** | Static Pod 拉起/替换 | Rolling (etcd quorum) | 由 StaticPodComponentExecutor 控制 |
| **yaml** | K8s API Apply 到集群 | 无 (集群级一次执行) | 由 YamlComponentExecutor 执行 |
| **helm** | Helm SDK install/upgrade | 无 (集群级一次执行) | 由 HelmComponentExecutor 执行 |
| **inline** | Phase handler 执行 | 无 | 复用现有 Phase 逻辑 |
| **composite** | 不执行 (展开为子组件) | — | DAG 构建时展开，自身不产生节点 |
| **selector** | 不执行 (按 condition 选择) | — | DAG 构建时评估 condition，选择子组件 |

以下数据结构直接复用代码库中已有的定义，无需新增：

```go
// pkg/topology/component.go (已有，直接复用)

// FailurePolicy 控制调度器对组件失败的反应
type FailurePolicy string

const (
    FailurePolicyFailFast FailurePolicy = "FailFast"
    FailurePolicyContinue FailurePolicy = "Continue"
    FailurePolicyRollback FailurePolicy = "Rollback"
)

// InlineRef 指向 ComponentFactory 注册的 handler
type InlineRef struct {
    Handler string
    Version string
}

// ComponentNode 是升级 DAG 中的一个顶点 (统一节点类型，不再区分 Cluster/NodeGroup)
// 所有组件类型（binary/yaml/helm/staticpod/inline）均为 ComponentNode
// composite/selector 类型在 DAG 构建时展开，不产生 ComponentNode
type ComponentNode struct {
    Name          string
    Version       string
    Inline        *InlineRef         // 仅 inline 类型使用
    FailurePolicy FailurePolicy      // 失败策略: FailFast / Continue / Rollback
    Dependencies  []string           // 依赖的组件名称列表
}

// UpgradeDAG 是升级依赖图，包含组件元数据
type UpgradeDAG struct {
    graph *Graph                         // 底层有向无环图
    nodes map[string]*ComponentNode      // 组件名 → 节点
}
```

```go
// pkg/topology/graph.go (已有，直接复用)

// Graph 是有向无环图，用于升级调度
// 边 From -> To 表示 From 必须在 To 之前完成
type Graph struct {
    nodes    map[string]struct{}
    outEdges map[string]map[string]struct{}
    inDegree map[string]int
}
```

```go
// pkg/topology/build.go (已有，直接复用)

// DependencyResolver 返回指定组件的前置依赖名称列表
type DependencyResolver func(componentName, version string) ([]string, error)

// BuildUpgradeDAG 从 ReleaseImage 升级组件列表构建升级 DAG
func BuildUpgradeDAG(
    components []cvv1alpha1.ReleaseImageUpgradeComponent,
    resolve DependencyResolver,
) (*UpgradeDAG, error)
```

```go
// pkg/dagexec/executor.go (已有，直接复用)

// ComponentExecutor 运行一种组件类型的升级操作
// Scheduler 通过 ExecutorRegistry 按 ComponentType 分发到对应 Executor
type ComponentExecutor interface {
    ExecuteComponent(ctx context.Context, node *topology.ComponentNode, execCtx *ExecutionContext) error
    GetComponentType() ComponentType
}
```

```go
// pkg/dagexec/registry.go (已有，直接复用)

// ExecutorRegistry 将 ComponentType 映射到 ComponentExecutor
type ExecutorRegistry struct {
    mu        sync.RWMutex
    executors map[ComponentType]ComponentExecutor
}
```

**组件类型与 Executor 的映射**：

```go
// pkg/dagexec/scheduler.go — resolveComponentType 已有逻辑
// ComponentNode 本身不含 Type 字段，Scheduler 通过 ComponentVersionStore
// 加载 ComponentVersion 后读取 cv.Spec.Type 确定组件类型，
// 再从 ExecutorRegistry 获取对应的 ComponentExecutor 执行。

// api/v1alpha1/componentversion_types.go — CRD 层组件类型 (4 值)
type ComponentType string
const (
    ComponentTypeYAML   ComponentType = "yaml"
    ComponentTypeHelm   ComponentType = "helm"
    ComponentTypeInline ComponentType = "inline"
    ComponentTypeBinary ComponentType = "binary"
)

// 后续新增 (KEP-15/KEP-17):
// ComponentTypeComposite ComponentType = "composite"  // DAG 构建时展开
// ComponentTypeSelector  ComponentType = "selector"   // DAG 构建时按 condition 选择
```

**Scheduler 执行流程**（复用已有逻辑，无需新增 DAGNode 接口）：

```go
// pkg/dagexec/scheduler.go (已有)

// Scheduler.ExecuteDAG 按拓扑批次执行 DAG
// 同一批次内的 ComponentNode 并行执行，批次间串行
func (s *Scheduler) ExecuteDAG(ctx context.Context, execCtx *ExecutionContext, dag *topology.UpgradeDAG) error {
    batches, err := dag.TopologicalBatches()
    // ... 逐批次并行执行 ComponentNode ...
    for _, batch := range batches {
        for _, name := range batch {
            node, _ := dag.GetNode(name)
            // resolveComponentType 通过 CVStore 加载 ComponentVersion
            // 读取 cv.Spec.Type，从 Registry 获取 Executor
            executor, _ := s.Registry.Get(s.resolveComponentType(ctx, node))
            executor.ExecuteComponent(ctx, node, execCtx)
        }
    }
}
```

**与旧设计的区别**：

| 维度 | 旧设计 (v4 原版) | 新设计 (复用代码库) |
|------|-----------------|-------------------|
| **DAG 节点类型** | `ClusterComponentNode` + `NodeGroupNode` (两种) | `topology.ComponentNode` (统一一种) |
| **DAGNode 接口** | 新增 `DAGNode` interface + `DAGNodeType` enum | 不需要，Scheduler 直接操作 `*ComponentNode` |
| **节点执行** | 各节点类型各自实现 `Execute()` | `ComponentExecutor.ExecuteComponent()` 统一接口 |
| **类型分发** | 节点类型固定 | `ExecutorRegistry` 按 `ComponentType` 动态分发 |
| **节点级并发** | `NodeGroupNode` 包装层 | `BinaryComponentExecutor` 内部 `upgradeStrategy.mode` |


### 2.4 三层状态机引擎架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          三层状态机引擎架构                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  L1: ClusterStateMachine (集群层状态机)                                  │   │
│  │  ─────────────────────────────────────                                   │   │
│  │  入口: BKEClusterReconciler.Reconcile()                                 │   │
│  │  职责: 管理集群生命周期，构建并执行 DAG                                    │   │
│  │  状态: Pending → Installing → Running → Upgrading → Scaling → Failed    │   │
│  │                                                                          │   │
│  │  DAG 执行:                                                               │   │
│  │  ┌──────────┐   ┌──────────────┐   ┌──────────┐   ┌──────────┐        │   │
│  │  │  certs   │──>│  components  │──>│  coredns │──>│kube-proxy│        │   │
│  │  │(ClusterSM)│  │ (CompSM)   │   │(ClusterSM)│  │(ClusterSM)│        │   │
│  │  └──────────┘   └──────────────┘   └──────────┘   └──────────┘        │   │
│  │                       │                                                 │   │
│  │                       │ 并行执行 N 个节点                                 │   │
│  │                       ▼                                                 │   │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │  │  L2: NodeStateMachine (节点层状态机) × N 个节点                   │   │   │
│  │  │  ─────────────────────────────────────                           │   │   │
│  │  │  职责: 管理单个节点的生命周期                                      │   │   │
│  │  │  状态: Pending → Provisioned → Ready → Upgrading → Deleting      │   │   │
│  │  │                                                                    │   │   │
│  │  │  节点内组件执行 (按依赖顺序):                                       │   │   │
│  │  │  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐     │   │   │
│  │  │  │ bkeagent │──>│containerd│──>│ kubelet  │──>│ kubectl  │     │   │   │
│  │  │  │(CompSM)  │   │(CompSM)  │   │(CompSM)  │   │(CompSM)  │     │   │   │
│  │  │  └──────────┘   └──────────┘   └──────────┘   └──────────┘     │   │   │
│  │  │       │              │              │              │              │   │   │
│  │  │       ▼              ▼              ▼              ▼              │   │   │
│  │  │  ┌─────────────────────────────────────────────────────────┐   │   │   │
│  │  │  │  L3: ComponentStateMachine (组件层状态机)                 │   │   │   │
│  │  │  │  ─────────────────────────────────                       │   │   │   │
│  │  │  │  职责: 管理单个组件的生命周期                              │   │   │   │
│  │  │  │  状态: Pending → Installing → Installed → Upgrading      │   │   │   │
│  │  │  │  执行: Install / Upgrade / Uninstall 操作                │   │   │   │
│  │  │  └─────────────────────────────────────────────────────────┘   │   │   │
│  │  └─────────────────────────────────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. 三层状态定义

### 3.1 L1 集群层状态

```go
// ClusterLifecyclePhase 集群生命周期阶段
type ClusterLifecyclePhase string

const (
    ClusterPhasePending     ClusterLifecyclePhase = "Pending"      // 等待操作
    ClusterPhaseInstalling  ClusterLifecyclePhase = "Installing"   // 安装中
    ClusterPhaseRunning     ClusterLifecyclePhase = "Running"      // 运行中
    ClusterPhaseUpgrading   ClusterLifecyclePhase = "Upgrading"    // 升级中
    ClusterPhaseScaling     ClusterLifecyclePhase = "Scaling"      // 扩缩容中
    ClusterPhaseRollingBack ClusterLifecyclePhase = "RollingBack"  // 回滚中
    ClusterPhaseFailed      ClusterLifecyclePhase = "Failed"       // 失败
)
```

**状态转换图**：

```
                    ┌──────────────────────────────────────┐
                    │                                      │
                    ▼                                      │
 [*] ──> Pending ──> Installing ──> Running ──> Upgrading ─┘
                    │                │           │
                    │                │           ├──> RollingBack ──> Running
                    │                │           │
                    │                ├──> Scaling ──> Running
                    │                │
                    ▼                ▼
                  Failed <───────────┘
                    │
                    └──> (人工介入) ──> 重新进入对应状态
```

**状态语义**：

| 状态 | 含义 | 进入条件 | 退出条件 |
|------|------|---------|---------|
| Pending | 等待操作触发 | 初始状态 / 操作完成后 | desiredVersion 变更 |
| Installing | 集群安装中 | desiredVersion 设置且无 currentVersion | DAG 执行完成 |
| Running | 集群正常运行 | 安装完成 / 升级完成 / 扩缩容完成 | 新操作触发 |
| Upgrading | 集群升级中 | desiredVersion 变更且已有 currentVersion | DAG 执行完成 |
| Scaling | 扩缩容中 | 节点数量变更 | DAG 执行完成 |
| RollingBack | 回滚中 | 升级失败触发回滚 | 回滚完成 |
| Failed | 操作失败 | 操作失败且超过重试次数 | 人工介入恢复 |

### 3.2 L2 节点层状态

```go
// NodeLifecyclePhase 节点生命周期阶段
type NodeLifecyclePhase string

const (
    NodePhasePending      NodeLifecyclePhase = "Pending"       // 等待操作
    NodePhaseProvisioning NodeLifecyclePhase = "Provisioning"  // 环境准备中
    NodePhaseReady        NodeLifecyclePhase = "Ready"         // 节点就绪
    NodePhaseUpgrading    NodeLifecyclePhase = "Upgrading"     // 升级中
    NodePhaseDeleting     NodeLifecyclePhase = "Deleting"      // 删除中
    NodePhaseFailed       NodeLifecyclePhase = "Failed"        // 失败
)
```

**状态转换图**：

```
 [*] ──> Pending ──> Provisioning ──> Ready ──> Upgrading ──> Ready
                      │                  │          │
                      │                  │          └──> Deleting ──> [*]
                      │                  │
                      ▼                  ▼
                    Failed <─────────────┘
                      │
                      └──> (人工介入) ──> 重新进入对应状态
```

**状态语义**：

| 状态 | 含义 | 进入条件 | 退出条件 |
|------|------|---------|---------|
| Pending | 等待 Agent 推送 | 节点创建 | Agent 推送完成 |
| Provisioning | 环境准备中 | Agent 就绪 | 所有节点级组件 Installed |
| Ready | 节点就绪 | 所有组件安装完成 | 升级触发 / 删除触发 |
| Upgrading | 升级中 | 版本变更 | 所有组件升级完成 |
| Deleting | 删除中 | 节点删除触发 | 所有组件卸载完成 |
| Failed | 操作失败 | 操作失败且超过重试 | 人工介入 |

### 3.3 L3 组件层状态

```go
// ComponentLifecyclePhase 组件生命周期阶段
type ComponentLifecyclePhase string

const (
    CompPhasePending      ComponentLifecyclePhase = "Pending"       // 等待执行
    CompPhaseInstalling   ComponentLifecyclePhase = "Installing"    // 安装中
    CompPhaseInstalled    ComponentLifecyclePhase = "Installed"     // 已安装
    CompPhaseUpgrading    ComponentLifecyclePhase = "Upgrading"     // 升级中
    CompPhaseDeleting     ComponentLifecyclePhase = "Deleting"      // 卸载中
    CompPhaseFailed       ComponentLifecyclePhase = "Failed"        // 失败
)
```

**状态转换图**：

```
 [*] ──> Pending ──> Installing ──> Installed ──> Upgrading ──> Installed
                      │                │              │
                      │                │              └──> Deleting ──> [*]
                      │                │
                      ▼                ▼
                    Failed <───────────┘
```

**状态语义**：

| 状态 | 含义 | 进入条件 | 退出条件 |
|------|------|---------|---------|
| Pending | 等待执行 | 组件创建 / 前置依赖未完成 | 前置依赖完成 |
| Installing | 安装中 | 开始执行安装操作 | 安装完成 |
| Installed | 已安装 | 安装完成 | 升级触发 / 卸载触发 |
| Upgrading | 升级中 | 版本变更 | 升级完成 |
| Deleting | 卸载中 | 卸载触发 | 卸载完成 |
| Failed | 失败 | 操作失败且超过重试 | 人工介入 |

### 3.4 三层状态关系

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        三层状态关系                                       │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  L1 (集群层)                                                             │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ ClusterPhase = Installing                                       │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────┐     │   │
│  │  │ L2 (节点层) × N                                         │     │   │
│  │  │ ┌─────────────────────────────────────────────────┐     │     │   │
│  │  │ │ Node-1: NodePhase = Provisioning                │     │     │   │
│  │  │ │   ┌───────────────────────────────────────┐     │     │     │   │
│  │  │ │   │ L3 (组件层)                            │     │     │     │   │
│  │  │ │   │ bkeagent:   Installing                │     │     │     │   │
│  │  │ │   │ containerd: Pending                   │     │     │     │   │
│  │  │ │   │ kubelet:    Pending                   │     │     │     │   │
│  │  │ │   └───────────────────────────────────────┘     │     │     │   │
│  │  │ └─────────────────────────────────────────────────┘     │     │   │
│  │  │ ┌─────────────────────────────────────────────────┐     │     │   │
│  │  │ │ Node-2: NodePhase = Ready                       │     │     │   │
│  │  │ │   ┌───────────────────────────────────────┐     │     │     │   │
│  │  │ │   │ L3 (组件层)                            │     │     │     │   │
│  │  │ │   │ bkeagent:   Installed                 │     │     │     │   │
│  │  │ │   │ containerd: Installed                 │     │     │     │   │
│  │  │ │   │ kubelet:    Installed                 │     │     │     │   │
│  │  │ │   └───────────────────────────────────────┘     │     │     │   │
│  │  │ └─────────────────────────────────────────────────┘     │     │   │
│  │  └─────────────────────────────────────────────────────────┘     │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  状态聚合规则:                                                           │
│  - ClusterPhase 由 DAG 执行状态决定                                      │
│  - NodePhase 由该节点所有组件状态聚合决定                                  │
│  - ComponentPhase 由组件操作执行结果决定                                   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 4. 核心数据结构

### 4.1 状态机引擎

**设计思路 — 三层状态机为何共享引擎实例而非每节点独立创建**：

1. **L1 集群层为唯一入口**：`ClusterStateMachine` 由 `BKEClusterReconciler` 持有，每次 Reconcile 调用 `engine.Execute()`。集群层负责构建 DAG 并按拓扑批次驱动执行，是整个状态机的调度核心。
2. **L2/L3 共享实例、独立状态**：`NodeStateMachine` 和 `ComponentStateMachine` 作为共享实例注入 `ClusterStateMachine`，但每个节点/组件的状态数据存储在各自的 CR Status 中（`BKENode.Status` / `ComponentLifecycleStatus`），引擎实例无状态，仅提供执行逻辑。这避免了 N 个节点创建 N 个引擎实例的内存开销。
3. **DAG 执行器委托给 Scheduler**：`dagExecutor` 字段在设计上指向 `dagexec.Scheduler`（§4.2 详述），集群层不直接操作 ComponentNode，而是委托给 Scheduler 的 `ExecuteDAG` 方法，复用代码库已有的拓扑排序和并行执行能力。

```go
// ClusterStateMachine 集群层状态机引擎 (L1)
type ClusterStateMachine struct {
    client    client.Client
    recorder  record.EventRecorder
    
    // DAG 执行器（复用 dagexec.Scheduler，见 §4.2）
    dagExecutor *DAGExecutor
    
    // 节点层状态机 (共享实例，每个节点独立执行)
    nodeSM *NodeStateMachine
    
    // 组件层状态机 (共享实例)
    componentSM *ComponentStateMachine
    
    // 可观测性
    metrics   *StateMachineMetrics
}

// NodeStateMachine 节点层状态机引擎 (L2)
type NodeStateMachine struct {
    // 组件层状态机
    componentSM *ComponentStateMachine
}

// ComponentStateMachine 组件层状态机引擎 (L3)
type ComponentStateMachine struct {
    // 组件执行器注册表（复用 dagexec.ExecutorRegistry）
    executors map[ComponentType]ComponentExecutor
}
```

### 4.2 DAG 执行器

**设计思路 — 复用代码库已有结构而非重新发明**：

1. **`topology.UpgradeDAG` 已满足需求**：代码库中 `pkg/topology/component.go` 已定义 `UpgradeDAG`（含 `Graph` 底层图 + `nodes` 元数据映射）和 `ComponentNode`（含 Name/Version/Inline/FailurePolicy/Dependencies），无需新增 `DAGExecutor`/`DAGNode`/`DAGNodeType` 等类型。
2. **`dagexec.Scheduler` 已实现拓扑执行**：`Scheduler.ExecuteDAG` 已实现"批次间串行、批次内并行"的调度策略，通过 `ExecutorRegistry` 按 `ComponentType` 分发到对应 `ComponentExecutor`，无需在状态机层重新实现。
3. **composite 展开是 DAG 构建的前置步骤**：`expandCompositeComponents` 在 `BuildUpgradeDAG` 之前执行，将 composite 类型的 ReleaseImage 组件展开为子组件列表，展开后的子组件作为普通 `ReleaseImageUpgradeComponent` 传入 `BuildUpgradeDAG`，DAG 构建逻辑无需感知 composite 的存在。
4. **类型分发延迟到执行期**：`ComponentNode` 不含 Type 字段，Scheduler 在执行时通过 `ComponentVersionStore` 加载 `ComponentVersion` 读取 `cv.Spec.Type`，再从 `ExecutorRegistry` 获取 Executor。这种延迟分发设计使得 DAG 结构与组件类型解耦——DAG 构建期不需要加载 ComponentVersion，仅需要组件名和版本号。

> **复用说明**：以下结构直接复用代码库中已有的 `topology.UpgradeDAG`、`topology.ComponentNode`、`dagexec.Scheduler`、`dagexec.ExecutorRegistry`，无需新增 `DAGExecutor`/`DAGNode`/`DAGNodeType` 等类型。

```go
// pkg/topology/build.go (已有)
// DependencyResolver 返回指定组件的前置依赖名称列表
type DependencyResolver func(componentName, version string) ([]string, error)

// BuildUpgradeDAG 从 ReleaseImage 升级组件列表构建升级 DAG
// composite/selector 类型在构建时展开为子组件节点，自身不产生 DAG 节点
// 所有组件（binary/yaml/helm/staticpod/inline）均为统一的 ComponentNode
func BuildUpgradeDAG(
    components []cvv1alpha1.ReleaseImageUpgradeComponent,
    resolve DependencyResolver,
) (*UpgradeDAG, error)

// DAG 构建增强（KEP-15 composite 展开）:
// 1. 展开 composite 组件为子组件
//    expandCompositeComponents(releaseImage.Spec.Components)
//    composite 自身不产生 DAG 节点，展开为子组件
// 2. 展开后的子组件作为 ReleaseImageUpgradeComponent 传入 BuildUpgradeDAG
// 3. BuildUpgradeDAG 内部为每个组件创建 topology.ComponentNode 并构建依赖边
```

```go
// pkg/dagexec/scheduler.go (已有)

// Scheduler 执行升级组件，按拓扑 DAG 调度
type Scheduler struct {
    InlineRunner        InlineRunner
    ManifestStore       manifest.Store
    ManifestApplier     manifest.Applier
    MaxParallelPerBatch int
    Registry            *ExecutorRegistry       // 按 ComponentType 分发到 ComponentExecutor
    CVStore             ComponentVersionStore   // 加载 ComponentVersion 确定组件类型
}

// ExecuteDAG 按拓扑批次执行 DAG (已有)
// 同一批次内的 ComponentNode 并行执行，批次间串行
// 不再需要 DAGNode 接口，Scheduler 直接操作 *topology.ComponentNode
func (s *Scheduler) ExecuteDAG(
    ctx context.Context,
    execCtx *ExecutionContext,
    dag *topology.UpgradeDAG,
) error {
    batches, err := dag.TopologicalBatches()
    // 逐批次并行执行
    for _, batch := range batches {
        for _, name := range batch {
            node, _ := dag.GetNode(name)
            // resolveComponentType 通过 CVStore 加载 ComponentVersion
            // 读取 cv.Spec.Type，从 Registry 获取对应 Executor
            typ := s.resolveComponentType(ctx, node)
            executor, ok := s.Registry.Get(typ)
            if !ok {
                // 回退到 Inline/Manifest 路径
            }
            executor.ExecuteComponent(ctx, node, execCtx)
        }
    }
}
```

### 4.3 DAG 节点执行

**设计思路 — ComponentNode 无 Execute 方法，执行委托给 ComponentExecutor**：

1. **数据与行为分离**：`topology.ComponentNode` 是纯数据结构（只有 Name/Version/Inline/FailurePolicy/Dependencies 字段），不携带执行逻辑。执行行为由 `dagexec.ComponentExecutor` 接口承担，各 Executor（BinaryComponentExecutor/YamlComponentExecutor/HelmComponentExecutor 等）实现该接口。这与代码库现有设计一致——`Scheduler` 直接操作 `*topology.ComponentNode`，不需要 `DAGNode` 接口。
2. **类型分发由 Scheduler 承担**：`Scheduler.resolveComponentType` 通过 `ComponentVersionStore` 加载 `ComponentVersion`，读取 `cv.Spec.Type` 转换为 `dagexec.ComponentType`，再从 `ExecutorRegistry` 获取对应 Executor。这意味着 DAG 节点本身是类型无关的——同一套 DAG 构建和拓扑排序逻辑适用于所有组件类型。
3. **节点级并发内化于 Executor**：binary 组件的逐节点/分批/全并行执行策略由 `BinaryComponentExecutor` 内部的 `upgradeStrategy.mode`（Rolling/Parallel/Batch）控制，不在 DAG 调度层处理。DAG 调度器只关心组件间的依赖顺序（批次间串行、批次内并行），节点内的并发策略是 Executor 的实现细节。
4. **未注册类型的回退路径**：当 `ExecutorRegistry` 中找不到对应 `ComponentType` 时，Scheduler 回退到 Inline/Manifest 路径（复用现有 `executeComponentLegacy` 逻辑），保证向后兼容。

> **复用说明**：`ComponentNode` 不含 `Execute()` 方法，执行逻辑由 `ComponentExecutor` 接口承担。Scheduler 通过 `ComponentVersionStore` 加载 `ComponentVersion` 确定组件类型，再从 `ExecutorRegistry` 获取对应 Executor。

```go
// pkg/dagexec/executor.go (已有)

// ComponentExecutor 运行一种组件类型的升级操作
type ComponentExecutor interface {
    ExecuteComponent(ctx context.Context, node *topology.ComponentNode, execCtx *ExecutionContext) error
    GetComponentType() ComponentType
}

// 各 Executor 实现 (已有或按 KEP-16/KEP-9 新增):
// - BinaryComponentExecutor:    SSH 逐节点执行，内部支持 Rolling/Batch/Parallel
// - YamlComponentExecutor:      K8s API Apply 到集群
// - HelmComponentExecutor:      Helm SDK install/upgrade
// - StaticPodComponentExecutor: Static Pod 拉起/替换 (KEP-9)
// - InlineComponentExecutor:    Phase handler 执行

// pkg/dagexec/scheduler.go (已有)
// resolveComponentType 通过 CVStore 加载 ComponentVersion，读取 cv.Spec.Type
// 转换为 dagexec.ComponentType 后从 Registry 获取 Executor
func (s *Scheduler) resolveComponentType(
    ctx context.Context,
    node *topology.ComponentNode,
) ComponentType {
    cv, _ := s.CVStore.GetComponentVersion(ctx, node.Name, node.Version)
    return ComponentType(cv.Spec.Type)  // api ComponentType → dagexec ComponentType
}
```


### 4.4 集群层状态机执行

**设计思路 — 集群层是状态机引擎的调度核心**：

1. **L1 评估状态、委托 DAG 执行**：`ClusterStateMachine.Execute` 的核心职责是评估集群状态转换（`evaluateClusterPhase`）并构建/执行 DAG。集群层不直接操作 Executor，而是委托给 `dagexec.Scheduler.ExecuteDAG`（§4.2），由 Scheduler 按拓扑批次分发到各 `ComponentExecutor`。这种分层委托保证集群层只关注"做什么操作"（Install/Upgrade/Rollback），不关心"怎么做"（SSH/Helm/K8s API）。
2. **集群状态由 DAG 执行结果驱动**：与节点层 `evaluateNodePhase` 自底向上聚合组件状态不同，集群层状态主要由 DAG 执行的总体结果驱动——DAG 全部成功则集群进入 Running/稳态，DAG 失败则集群进入 Failed。集群层不需要逐组件聚合，因为 Scheduler 已在 DAG 执行过程中处理了组件级状态。
3. **DAG 构建与执行分离**：每次 Reconcile 时，`Execute` 先从 ReleaseImage 构建 DAG（`buildDAG`），再执行 DAG（`scheduler.ExecuteDAG`）。DAG 构建是幂等的——相同的 ReleaseImage 产生相同的 DAG 结构。DAG 执行也是幂等的——已完成的组件通过 `VersionContext.NeedsUpgrade` 跳过，未完成的继续执行。
4. **Reconcile 重入安全**：`Execute` 是幂等的。当 Reconcile 因组件执行中断而重入时，`evaluateClusterPhase` 根据当前集群状态（Installing/Upgrading 等）决定继续执行 DAG，而非重新开始。DAG 内部的组件级幂等（`evaluateComponentPhase` 返回 Installed 跳过）保证已完成的组件不会重复执行。

```go
// ClusterStateMachine.Execute 执行集群层状态机
// 由 BKEClusterReconciler.Reconcile() 调用，是整个状态机的唯一入口
func (sm *ClusterStateMachine) Execute(ctx context.Context, cluster *bkev1beta1.BKECluster) error {
    // 1. 评估集群状态转换
    oldPhase := cluster.Status.LifecyclePhase
    newPhase := sm.evaluateClusterPhase(ctx, cluster)

    if oldPhase != newPhase {
        cluster.Status.LifecyclePhase = newPhase
        sm.recordClusterTransition(cluster, oldPhase, newPhase)
    }

    // 2. 根据集群状态执行操作
    switch newPhase {
    case ClusterPhaseInstalling:
        // 首次安装：从 ReleaseImage.spec.install 构建 DAG
        dag, err := sm.buildInstallDAG(ctx, cluster)
        if err != nil {
            return fmt.Errorf("build install DAG failed: %w", err)
        }
        execCtx := sm.buildExecutionContext(ctx, cluster)
        if err := sm.scheduler.ExecuteDAG(ctx, execCtx, dag); err != nil {
            return fmt.Errorf("execute install DAG failed: %w", err)
        }
        cluster.Status.CurrentVersion = cluster.Spec.DesiredVersion

    case ClusterPhaseUpgrading:
        // 版本升级：从 ReleaseImage.spec.upgrade 构建 DAG
        dag, err := sm.buildUpgradeDAG(ctx, cluster)
        if err != nil {
            return fmt.Errorf("build upgrade DAG failed: %w", err)
        }
        execCtx := sm.buildExecutionContext(ctx, cluster)
        if err := sm.scheduler.ExecuteDAG(ctx, execCtx, dag); err != nil {
            return fmt.Errorf("execute upgrade DAG failed: %w", err)
        }
        cluster.Status.CurrentVersion = cluster.Spec.DesiredVersion

    case ClusterPhaseScaling:
        // 扩缩容：仅对新节点构建节点级组件 DAG
        dag, err := sm.buildScalingDAG(ctx, cluster)
        if err != nil {
            return fmt.Errorf("build scaling DAG failed: %w", err)
        }
        execCtx := sm.buildExecutionContext(ctx, cluster)
        if err := sm.scheduler.ExecuteDAG(ctx, execCtx, dag); err != nil {
            return fmt.Errorf("execute scaling DAG failed: %w", err)
        }

    case ClusterPhaseRollingBack:
        // 回滚：从上一个 ReleaseImage.spec.upgrade 构建降级 DAG
        dag, err := sm.buildRollbackDAG(ctx, cluster)
        if err != nil {
            return fmt.Errorf("build rollback DAG failed: %w", err)
        }
        execCtx := sm.buildExecutionContext(ctx, cluster)
        if err := sm.scheduler.ExecuteDAG(ctx, execCtx, dag); err != nil {
            return fmt.Errorf("execute rollback DAG failed: %w", err)
        }
        cluster.Status.CurrentVersion = cluster.Spec.DesiredVersion
    }

    // Pending / Running / Failed → 无操作，直接返回
    return nil
}
```

#### 4.4.1 evaluateClusterPhase 设计思路

**设计思路 — 集群级状态由操作触发与 DAG 执行结果共同驱动**：

与节点层 `evaluateNodePhase`（自底向上聚合组件状态）不同，集群层状态决策由两个维度共同驱动：

1. **外部触发条件**：`desiredVersion` 变更、节点数量变更、回滚标记等外部输入决定集群应进入哪个操作态（Installing/Upgrading/Scaling/RollingBack）。
2. **DAG 执行结果**：DAG 全部成功 → 进入稳态（Running）；DAG 失败 → 进入 Failed；DAG 部分完成 → 保持当前操作态继续执行。

**决策优先级**：

1. **Failed 不自愈**：当前为 Failed 时返回 Failed，等待人工介入清除状态后重新评估。与组件层一致，避免故障集群在未修复前被反复重试。
2. **回滚触发优先**：`RollbackRequested == true` 时直接返回 RollingBack，不受其他条件影响。回滚是紧急操作，优先级最高。
3. **操作中间态保持**：当前为 Installing/Upgrading/Scaling/RollingBack 时，保持原状态继续执行 DAG。这保证跨 Reconcile 的操作连续性。
4. **版本比较驱动**：`desiredVersion != currentVersion` 时，根据是否有 `currentVersion` 判断 Installing（首次安装）或 Upgrading（版本升级）。
5. **稳态**：`desiredVersion == currentVersion` 且无其他触发条件 → Running。

**状态决策规则矩阵**：

| 当前 Phase | 触发条件 | 返回 Phase | Execute 行为 |
|-----------|---------|-----------|-------------|
| Failed | — | `Failed` | 无操作（等待人工介入） |
| * | RollbackRequested=true | `RollingBack` | 执行回滚 DAG |
| Installing | DAG 未完成 | `Installing` | 继续执行安装 DAG |
| Upgrading | DAG 未完成 | `Upgrading` | 继续执行升级 DAG |
| Scaling | 节点数量变更未完成 | `Scaling` | 执行扩缩容 DAG |
| Pending | desiredVersion 设置, 无 currentVersion | `Installing` | 构建并执行安装 DAG |
| Running | desiredVersion != currentVersion | `Upgrading` | 构建并执行升级 DAG |
| Running | desiredVersion == currentVersion, 节点变更 | `Scaling` | 执行扩缩容 DAG |
| Running | desiredVersion == currentVersion, 无变更 | `Running` | 无操作（稳态） |
| * | DAG 全部成功 | `Running` | 更新 currentVersion |
| * | DAG 失败 | `Failed` | 等待人工介入 |

#### 4.4.2 evaluateClusterPhase 实现

```go
// evaluateClusterPhase 评估集群生命周期阶段
// 由外部触发条件和 DAG 执行结果共同驱动
//
// 决策优先级:
//   1. Failed → 保持 Failed (不自愈，等待人工介入)
//   2. RollbackRequested → RollingBack (回滚优先级最高)
//   3. 操作中间态保持 (Installing/Upgrading/Scaling/RollingBack 继续)
//   4. 版本比较驱动 (desiredVersion != currentVersion → Installing/Upgrading)
//   5. 节点变更 → Scaling
//   6. 稳态 → Running
func (sm *ClusterStateMachine) evaluateClusterPhase(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) ClusterLifecyclePhase {
    currentPhase := cluster.Status.LifecyclePhase

    // 1. Failed 不自愈 — 等待人工介入清除状态
    if currentPhase == ClusterPhaseFailed {
        return ClusterPhaseFailed
    }

    // 2. 回滚触发优先
    if cluster.Spec.RollbackRequested {
        return ClusterPhaseRollingBack
    }

    // 3. 操作中间态保持 — 跨 Reconcile 继续执行
    switch currentPhase {
    case ClusterPhaseInstalling:
        // 检查 DAG 是否已全部完成
        if sm.isDAGCompleted(ctx, cluster) {
            return ClusterPhaseRunning // 安装完成 → 稳态
        }
        return ClusterPhaseInstalling // 继续安装

    case ClusterPhaseUpgrading:
        if sm.isDAGCompleted(ctx, cluster) {
            return ClusterPhaseRunning // 升级完成 → 稳态
        }
        return ClusterPhaseUpgrading // 继续升级

    case ClusterPhaseScaling:
        if sm.isScalingCompleted(ctx, cluster) {
            return ClusterPhaseRunning // 扩缩容完成 → 稳态
        }
        return ClusterPhaseScaling // 继续扩缩容

    case ClusterPhaseRollingBack:
        if sm.isDAGCompleted(ctx, cluster) {
            return ClusterPhaseRunning // 回滚完成 → 稳态
        }
        return ClusterPhaseRollingBack // 继续回滚
    }

    // 4. 版本比较驱动 — 从 Pending 或 Running 进入操作态
    desiredVersion := cluster.Spec.DesiredVersion
    currentVersion := cluster.Status.CurrentVersion

    if desiredVersion != "" && desiredVersion != currentVersion {
        if currentVersion == "" {
            // 无当前版本 → 首次安装
            return ClusterPhaseInstalling
        }
        // 有当前版本且不一致 → 升级
        return ClusterPhaseUpgrading
    }

    // 5. 节点数量变更 → 扩缩容
    if sm.hasNodeCountChange(ctx, cluster) {
        return ClusterPhaseScaling
    }

    // 6. 稳态
    return ClusterPhaseRunning
}

// isDAGCompleted 检查 DAG 是否全部执行完成
// 通过 VersionContext 检查所有目标组件是否已达到目标版本
func (sm *ClusterStateMachine) isDAGCompleted(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) bool {
    if sm.versionContext == nil {
        return false
    }
    // 检查是否还有组件需要升级
    return !sm.versionContext.AnyTargetNeedsUpgrade()
}

// hasNodeCountChange 检查节点数量是否发生变更
func (sm *ClusterStateMachine) hasNodeCountChange(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) bool {
    // 对比 cluster.Spec.NodeRefs 与实际 BKENode 数量
    nodes, err := sm.nodeProvider.GetNodes(ctx, cluster)
    if err != nil || len(nodes) == 0 {
        return false
    }
    // 检查是否有新增节点（待 Provisioning）或删除节点（待 Deleting）
    for _, node := range nodes {
        if node.Status.LifecyclePhase == NodePhasePending {
            return true // 有新节点待安装
        }
        if node.Spec.Deleted {
            return true // 有节点待删除
        }
    }
    return false
}

// isScalingCompleted 检查扩缩容是否完成
func (sm *ClusterStateMachine) isScalingCompleted(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) bool {
    nodes, err := sm.nodeProvider.GetNodes(ctx, cluster)
    if err != nil {
        return false
    }
    for _, node := range nodes {
        // 有新节点仍处于 Pending/Provisioning → 扩容未完成
        if node.Status.LifecyclePhase == NodePhasePending ||
           node.Status.LifecyclePhase == NodePhaseProvisioning {
            return false
        }
        // 有节点仍处于 Deleting → 缩容未完成
        if node.Status.LifecyclePhase == NodePhaseDeleting {
            return false
        }
    }
    return true
}
```

**evaluateClusterPhase 与 Execute 的协作**：

```go
// evaluateClusterPhase 返回值 → Execute switch 分支映射:
//
// Installing   → buildInstallDAG  + scheduler.ExecuteDAG   构建安装 DAG 并执行
// Upgrading    → buildUpgradeDAG  + scheduler.ExecuteDAG   构建升级 DAG 并执行
// Scaling      → buildScalingDAG  + scheduler.ExecuteDAG    构建扩缩容 DAG 并执行
// RollingBack  → buildRollbackDAG + scheduler.ExecuteDAG   构建回滚 DAG 并执行
// Running      → 无匹配 case，直接返回 nil                   稳态，无操作
// Pending      → 无匹配 case，直接返回 nil                   等待 desiredVersion 设置
// Failed       → 无匹配 case，直接返回 nil                   等待人工介入
//
// 设计说明: Running/Pending/Failed 是非操作态，Execute 中无对应 case，
// 直接返回 nil。这保证了:
// - 幂等性: 稳态集群 Reconcile 时跳过 DAG 构建
// - 故障隔离: Failed 集群不自动重试
// - 等待触发: Pending 集群等待用户设置 desiredVersion
```

**边界情况处理**：

| 场景 | 当前 Phase | 触发条件 | 返回 Phase | Execute 行为 |
|------|-----------|---------|-----------|-------------|
| 首次安装 | Pending | desiredVersion 设置, 无 currentVersion | `Installing` | 构建安装 DAG 执行 |
| 安装中断重入 | Installing | DAG 未完成 | `Installing` | 继续执行安装 DAG |
| 安装完成 | Installing | DAG 全部完成 | `Running` | 更新 currentVersion |
| 版本升级触发 | Running | desiredVersion != currentVersion | `Upgrading` | 构建升级 DAG 执行 |
| 升级中断重入 | Upgrading | DAG 未完成 | `Upgrading` | 继续执行升级 DAG |
| 升级完成 | Upgrading | DAG 全部完成 | `Running` | 更新 currentVersion |
| 扩容触发 | Running | 新节点 Pending | `Scaling` | 执行扩缩容 DAG |
| 回滚触发 | Upgrading | RollbackRequested=true | `RollingBack` | 构建回滚 DAG 执行 |
| DAG 执行失败 | Upgrading | DAG 失败 | `Failed` | 等待人工介入 |
| 故障不自愈 | Failed | — | `Failed` | 无操作 |
| 稳态 | Running | 版本一致, 无节点变更 | `Running` | 无操作（幂等跳过） |

#### 4.4.3 DAG 构建设计思路

**设计思路 — 安装与升级 DAG 为何拆分**：

ReleaseImage 中 `spec.install.components` 和 `spec.upgrade.components` 是两个独立的组件列表，分别对应首次安装和版本升级场景。拆分 `buildInstallDAG` 与 `buildUpgradeDAG` 有以下理由：

1. **组件列表不同**：安装 DAG 包含全部组件（从零开始的完整安装），升级 DAG 可能只包含变更的组件（增量升级）。合并构建会导致安装场景执行了仅升级需要的逻辑，或升级场景执行了仅安装需要的前置步骤。
2. **composite 展开差异**：安装场景的 composite 展开 `subComponents` 全部子组件；升级场景的 composite 展开时还可能携带 `deferredSubComponents`（延迟升级声明），需要被 `executeControlPlaneHop` 读取。两个场景对 composite 的处理方式不同。
3. **依赖解析差异**：安装 DAG 的 `DependencyResolver` 读取 `cv.Spec.Dependencies`（Install 阶段依赖）；升级 DAG 的 `DependencyResolver` 可能过滤 `phase != "Upgrade"` 的依赖（部分依赖仅在安装时生效，升级时不需要）。
4. **VersionContext 初始化不同**：安装场景 `VersionContext.Current` 为空（无已安装版本），所有组件视为 Install；升级场景 `VersionContext.Current` 有值，Scheduler 通过 `NeedsUpgrade` 判断哪些组件需要升级、哪些跳过。DAG 结构相同但执行语义不同。

**四类 DAG 的职责对比**：

| DAG 类型 | 组件来源 | composite 处理 | VersionContext | 典型场景 |
|---------|---------|---------------|----------------|---------|
| **buildInstallDAG** | `releaseImage.Spec.Install.Components` | 展开 subComponents，无 deferred | Current 为空，全部 Install | 首次创建集群 |
| **buildUpgradeDAG** | `releaseImage.Spec.Upgrade.Components` | 展开 subComponents + 解析 deferred | Current 有值，按 NeedsUpgrade 过滤 | 修改 desiredVersion |
| **buildScalingDAG** | 当前 ReleaseImage 的节点级组件 | 仅 binary 类型组件 | Current 有值，新节点 Install | 新增/删除节点 |
| **buildRollbackDAG** | 上一个 ReleaseImage 的 Upgrade.Components | 同 buildUpgradeDAG | 回退 Current 版本 | 升级失败回滚 |

#### 4.4.4 DAG 构建实现

```go
// ──────────────────────────────────────────────────────────────
// buildInstallDAG 构建安装 DAG
// 从 ReleaseImage.spec.install.components 构建，适用于首次安装
// ──────────────────────────────────────────────────────────────
func (sm *ClusterStateMachine) buildInstallDAG(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*topology.UpgradeDAG, error) {
    releaseImage, err := sm.releaseImageResolver.Resolve(ctx, cluster)
    if err != nil {
        return nil, fmt.Errorf("resolve release image: %w", err)
    }

    // 1. 从 install.components 获取组件列表
    installComponents := releaseImage.Spec.Install.Components
    if installComponents == nil {
        // install 为空时回退到 upgrade.components（兼容无 install 声明的 ReleaseImage）
        installComponents = releaseImage.Spec.Upgrade.Components
    }

    // 2. 展开 composite 组件 (KEP-15)
    expandedComponents := expandCompositeComponents(installComponents)

    // 3. 构建 DAG (复用 topology.BuildUpgradeDAG)
    resolve := sm.makeDependencyResolver(ctx)
    return topology.BuildUpgradeDAG(expandedComponents, resolve)
}

// ──────────────────────────────────────────────────────────────
// buildUpgradeDAG 构建升级 DAG
// 从 ReleaseImage.spec.upgrade.components 构建，适用于版本升级
// ──────────────────────────────────────────────────────────────
func (sm *ClusterStateMachine) buildUpgradeDAG(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*topology.UpgradeDAG, error) {
    releaseImage, err := sm.releaseImageResolver.Resolve(ctx, cluster)
    if err != nil {
        return nil, fmt.Errorf("resolve release image: %w", err)
    }

    // 1. 从 upgrade.components 获取组件列表
    upgradeComponents := releaseImage.Spec.Upgrade.Components

    // 2. 展开 composite 组件 (KEP-15)
    //    同时解析 deferredSubComponents，传入 executeControlPlaneHop
    expandedComponents := expandCompositeComponents(upgradeComponents)

    // 3. 解析延迟升级的子组件列表
    //    由编排器在 executeControlPlaneHop 中跳过这些组件的 Target 版本更新
    deferredComponents := resolveDeferredComponents(upgradeComponents)

    // 4. 构建 DAG
    resolve := sm.makeDependencyResolver(ctx)
    dag, err := topology.BuildUpgradeDAG(expandedComponents, resolve)
    if err != nil {
        return nil, err
    }

    // 5. 将 deferredComponents 存入 ExecutionContext 供编排器使用
    //    DAG 结构本身不变，deferred 仅影响 VersionContext 的 Target 设置
    _ = deferredComponents // 通过 execCtx 传递，见 buildExecutionContext

    return dag, nil
}

// ──────────────────────────────────────────────────────────────
// buildScalingDAG 构建扩缩容 DAG
// 仅包含节点级组件（binary 类型），对新节点执行安装、对删除节点执行卸载
// ──────────────────────────────────────────────────────────────
func (sm *ClusterStateMachine) buildScalingDAG(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*topology.UpgradeDAG, error) {
    releaseImage, err := sm.releaseImageResolver.Resolve(ctx, cluster)
    if err != nil {
        return nil, fmt.Errorf("resolve release image: %w", err)
    }

    // 1. 获取当前版本的组件列表（使用 install 还是 upgrade 取决于集群状态）
    components := releaseImage.Spec.Install.Components
    if cluster.Status.CurrentVersion != "" {
        // 已安装集群扩容：使用 upgrade.components（版本已就绪，新节点安装到当前版本）
        components = releaseImage.Spec.Upgrade.Components
    }

    // 2. 展开 composite 组件
    expandedComponents := expandCompositeComponents(components)

    // 3. 仅保留节点级组件（binary 类型），集群级组件不需要在扩缩容时重新执行
    var nodeComponents []cvv1alpha1.ReleaseImageUpgradeComponent
    for _, comp := range expandedComponents {
        cv, err := sm.cvStore.GetComponentVersion(ctx, comp.Name, comp.Version)
        if err != nil {
            return nil, fmt.Errorf("lookup component %s: %w", comp.Name, err)
        }
        if cv.Spec.Type == ComponentTypeBinary {
            nodeComponents = append(nodeComponents, comp)
        }
    }

    // 4. 构建 DAG（仅含节点级组件）
    resolve := sm.makeDependencyResolver(ctx)
    return topology.BuildUpgradeDAG(nodeComponents, resolve)
}

// ──────────────────────────────────────────────────────────────
// buildRollbackDAG 构建回滚 DAG
// 从上一个 ReleaseImage 的 upgrade.components 构建，回退到旧版本
// ──────────────────────────────────────────────────────────────
func (sm *ClusterStateMachine) buildRollbackDAG(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*topology.UpgradeDAG, error) {
    // 1. 解析回滚目标版本（上一个成功安装的版本）
    rollbackVersion := cluster.Status.PreviousVersion
    if rollbackVersion == "" {
        return nil, fmt.Errorf("no previous version to rollback to")
    }

    // 2. 获取回滚目标版本的 ReleaseImage
    rollbackReleaseImage, err := sm.releaseImageResolver.ResolveByVersion(ctx, rollbackVersion)
    if err != nil {
        return nil, fmt.Errorf("resolve rollback release image %s: %w", rollbackVersion, err)
    }

    // 3. 从 upgrade.components 获取组件列表
    rollbackComponents := rollbackReleaseImage.Spec.Upgrade.Components

    // 4. 展开 composite 组件
    expandedComponents := expandCompositeComponents(rollbackComponents)

    // 5. 构建 DAG
    //    回滚 DAG 的依赖解析与升级相同——组件依赖关系不变，仅版本回退
    resolve := sm.makeDependencyResolver(ctx)

    // 6. 将 VersionContext 的 Current/Target 交换
    //    回滚场景下：原 Current（升级前版本）→ Target，原 Target（升级后版本）→ Current
    //    这样 NeedsUpgrade 判断的是"从升级后版本回退到升级前版本"
    sm.versionContext.PrepareRollback()

    return topology.BuildUpgradeDAG(expandedComponents, resolve)
}

// ──────────────────────────────────────────────────────────────
// 公共辅助函数
// ──────────────────────────────────────────────────────────────

// makeDependencyResolver 创建依赖解析器
// 从 ComponentVersion.spec.dependencies 读取依赖关系
func (sm *ClusterStateMachine) makeDependencyResolver(
    ctx context.Context,
) topology.DependencyResolver {
    return func(name, version string) ([]string, error) {
        cv, err := sm.cvStore.GetComponentVersion(ctx, name, version)
        if err != nil {
            return nil, fmt.Errorf("lookup component %s-%s: %w", name, version, err)
        }
        return topology.ComponentDependencyNames(cv.Spec.Dependencies), nil
    }
}

// buildExecutionContext 构建执行上下文
func (sm *ClusterStateMachine) buildExecutionContext(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) *ExecutionContext {
    return NewExecutionContext(
        cluster,
        sm.nodeProvider,
        sm.cvStore,
        sm.versionContext,
        sm.client,
    )
}
```

**DAG 构建对比总结**：

| 维度 | buildInstallDAG | buildUpgradeDAG | buildScalingDAG | buildRollbackDAG |
|------|----------------|----------------|----------------|-----------------|
| **组件来源** | `install.components` | `upgrade.components` | 当前版本组件 | 旧版本 `upgrade.components` |
| **composite 展开** | 全部子组件 | 全部 + deferred | 仅 binary 子组件 | 全部子组件 |
| **VersionContext** | Current 为空 | Current 有值 | Current 有值 | Current/Target 交换 |
| **组件过滤** | 无 | 无 | 仅 binary 类型 | 无 |
| **依赖解析** | Install 阶段依赖 | Upgrade 阶段依赖 | Install 阶段依赖 | Upgrade 阶段依赖 |
| **DAG 结构** | 完整（全部组件） | 完整或增量 | 子集（节点级） | 完整（旧版本） |
| **幂等机制** | VersionContext 无 Current | `NeedsUpgrade` 过滤 | 新节点无状态→Install | `PrepareRollback` 后 `NeedsUpgrade` |

### 4.5 节点层状态机执行

**设计思路 — 节点层状态机的职责边界与组件来源**：

1. **L2 评估状态、L3 执行操作**：`NodeStateMachine.Execute` 的核心职责是评估节点状态转换（`evaluateNodePhase`）并决定执行何种操作（Install/Upgrade/Uninstall），实际的组件安装/升级委托给 `ComponentStateMachine.Execute`。节点层不直接操作 Executor，通过组件层间接调用，保持三层职责分离。
2. **节点组件从 DAG 获取而非硬编码**：`components` 参数由 BKEMachine Controller 从 ReleaseImage 解析后传入（§6.3 详述），不再从 `NodeGroupNode` 获取。节点组件列表按依赖关系拓扑排序后逐个执行，排序算法复用代码库已有的 `topologicalSort`。
3. **节点状态聚合由组件状态决定**：`evaluateNodePhase` 根据该节点所有组件的 `ComponentLifecycleStatus` 聚合判断——全部 Installed 则节点 Ready，任一 Installing 则节点 Provisioning，任一 Failed 则节点 Failed。这种自底向上的状态聚合保证节点状态真实反映组件执行进度。
4. **与 DAG 调度的协作关系**：在统一 ComponentNode 设计下，节点级 binary 组件是 DAG 中的独立节点，由 Scheduler 按拓扑批次调度。`NodeStateMachine` 主要服务于 BKEMachine Controller 的独立 Reconcile（如节点扩容场景），与 Scheduler 的 DAG 执行是两条并行路径——Scheduler 负责集群级升级的 DAG 调度，NodeStateMachine 负责单节点生命周期管理。

```go
// NodeStateMachine.Execute 执行节点层状态机
func (sm *NodeStateMachine) Execute(ctx context.Context, node *BKENode, components []*ComponentVersion) error {
    // 1. 评估节点状态转换
    oldPhase := node.Status.LifecyclePhase
    newPhase := sm.evaluateNodePhase(node, components)
    
    if oldPhase != newPhase {
        node.Status.LifecyclePhase = newPhase
        // 记录状态转换事件
        sm.recordNodeTransition(node, oldPhase, newPhase)
    }
    
    // 2. 根据节点状态执行操作
    switch newPhase {
    case NodePhaseProvisioning:
        // 执行节点级组件安装（按依赖顺序）
        return sm.executeNodeComponents(ctx, node, components, ActionInstall)
    
    case NodePhaseUpgrading:
        // 执行节点级组件升级
        return sm.executeNodeComponents(ctx, node, components, ActionUpgrade)
    
    case NodePhaseDeleting:
        // 执行节点级组件卸载
        return sm.executeNodeComponents(ctx, node, components, ActionUninstall)
    }
    
    return nil
}

// executeNodeComponents 按依赖顺序执行节点级组件
func (sm *NodeStateMachine) executeNodeComponents(
    ctx context.Context,
    node *BKENode,
    components []*ComponentVersion,
    action ComponentAction,
) error {
    // 按依赖关系排序
    sorted := topologicalSort(components)
    
    for _, comp := range sorted {
        compStatus := node.GetComponentStatus(comp.Name)
        
        // 执行组件级状态机
        if err := sm.componentSM.Execute(ctx, compStatus, comp); err != nil {
            return fmt.Errorf("component %s failed: %w", comp.Name, err)
        }
    }
    
    return nil
}
```

#### 4.5.1 evaluateNodePhase 设计思路

**设计思路 — 自底向上的状态聚合**：

`evaluateNodePhase` 是节点层状态机的核心决策函数，负责根据组件层（L3）状态和外部触发条件推断节点层（L2）状态。其设计遵循以下原则：

1. **组件状态优先，外部触发其次**：函数首先扫描该节点所有组件的 `ComponentLifecyclePhase`，根据组件状态聚合判断节点状态。只有当组件状态不足以决定时（如全部 Installed 但有升级触发），才检查外部触发条件（如 `desiredVersion != currentVersion`）。
2. **优先级从严到宽**：判断顺序为 Failed → Installing/Upgrading/Deleting → Pending → Ready。任一组件 Failed 则节点 Failed（最严），任一组件正在执行则节点处于对应操作中，全部 Installed 且无升级触发则 Ready（最宽）。这种顺序保证节点状态始终反映最严重的组件状态。
3. **幂等性保证**：当所有组件已 Installed 且目标版本等于当前版本时，`evaluateNodePhase` 返回 `Ready`，`Execute` 的 switch 语句不匹配任何 case（Ready 不是操作态），直接返回 nil——不会重复执行安装/升级。
4. **扩容场景的 Provisioning 判定**：新节点加入时，所有组件状态为空（无 ComponentLifecycleStatus 记录），`evaluateNodePhase` 将其判定为 `Provisioning`（环境准备中），触发组件安装流程。安装完成后组件状态变为 Installed，节点状态聚合为 Ready。

**状态聚合规则矩阵**：

| 组件状态组合 | 节点状态 | 判定逻辑 |
|------------|---------|---------|
| 任一组件 Failed | `Failed` | 最高优先级，立即终止 |
| 任一组件 Installing 且无 Failed | `Provisioning` | 首次安装进行中 |
| 任一组件 Upgrading 且无 Failed | `Upgrading` | 升级进行中 |
| 任一组件 Deleting 且无 Failed | `Deleting` | 卸载进行中 |
| 全部 Installed + desiredVersion != currentVersion | `Upgrading` | 版本变更触发升级 |
| 全部 Installed + desiredVersion == currentVersion | `Ready` | 稳态，无操作 |
| 全部 Pending（新节点） | `Provisioning` | 新节点首次安装 |
| 节点删除标记为 true | `Deleting` | 外部删除触发 |

#### 4.5.2 evaluateNodePhase 实现

```go
// evaluateNodePhase 评估节点生命周期阶段
// 自底向上聚合组件状态，结合外部触发条件推断节点状态
//
// 判断优先级 (从严到宽):
//   1. Deleting (外部删除触发)
//   2. Failed (任一组件 Failed)
//   3. Provisioning (任一组件 Installing)
//   4. Upgrading (任一组件 Upgrading)
//   5. Deleting (任一组件 Deleting — 组件级卸载)
//   6. Upgrading (全部 Installed + 版本变更)
//   7. Provisioning (全部 Pending — 新节点)
//   8. Ready (全部 Installed + 版本一致)
func (sm *NodeStateMachine) evaluateNodePhase(
    node *BKENode,
    components []*ComponentVersion,
) NodeLifecyclePhase {
    // 1. 外部删除触发优先
    if node.Spec.Deleted {
        return NodePhaseDeleting
    }

    // 2. 收集组件状态
    componentStatuses := node.Status.ComponentStatuses
    if len(componentStatuses) == 0 && len(components) > 0 {
        // 新节点：无组件状态记录，首次安装
        return NodePhaseProvisioning
    }

    hasFailed := false
    hasInstalling := false
    hasUpgrading := false
    hasDeleting := false
    allInstalled := true

    for _, comp := range components {
        status, exists := componentStatuses[comp.Name]
        if !exists {
            // 组件无状态记录 → 视为 Pending，未完成安装
            allInstalled = false
            hasInstalling = true // 待安装等同于 Installing 语义
            continue
        }

        switch status.Phase {
        case CompPhaseFailed:
            hasFailed = true
        case CompPhaseInstalling:
            hasInstalling = true
            allInstalled = false
        case CompPhaseUpgrading:
            hasUpgrading = true
            allInstalled = false
        case CompPhaseDeleting:
            hasDeleting = true
            allInstalled = false
        case CompPhaseInstalled:
            // 已安装，继续检查其他组件
        case CompPhasePending:
            allInstalled = false
            hasInstalling = true
        }
    }

    // 3. 按优先级返回节点状态

    // 3a. 任一组件 Failed → 节点 Failed
    if hasFailed {
        return NodePhaseFailed
    }

    // 3b. 任一组件 Installing → 节点 Provisioning (首次安装进行中)
    if hasInstalling {
        return NodePhaseProvisioning
    }

    // 3c. 任一组件 Upgrading → 节点 Upgrading (升级进行中)
    if hasUpgrading {
        return NodePhaseUpgrading
    }

    // 3d. 任一组件 Deleting → 节点 Deleting (组件级卸载进行中)
    if hasDeleting {
        return NodePhaseDeleting
    }

    // 3e. 全部 Installed → 检查是否需要升级
    if allInstalled {
        // 检查版本变更触发升级
        if sm.hasVersionChange(node, components) {
            return NodePhaseUpgrading
        }
        // 稳态：所有组件已安装且版本一致
        return NodePhaseReady
    }

    // 3f. 兜底：状态不明确时保持 Provisioning
    return NodePhaseProvisioning
}

// hasVersionChange 检查是否有组件的目标版本与当前安装版本不一致
func (sm *NodeStateMachine) hasVersionChange(
    node *BKENode,
    components []*ComponentVersion,
) bool {
    componentStatuses := node.Status.ComponentStatuses
    for _, comp := range components {
        status, exists := componentStatuses[comp.Name]
        if !exists {
            return true // 组件未安装，需要安装
        }
        // 已安装版本与目标版本不一致 → 需要升级
        if status.Version != comp.Spec.Version {
            return true
        }
    }
    return false
}
```

**evaluateNodePhase 与 Execute 的协作**：

```go
// evaluateNodePhase 返回值 → Execute switch 分支映射:
//
// Provisioning  → executeNodeComponents(ActionInstall)   首次安装
// Upgrading     → executeNodeComponents(ActionUpgrade)   版本升级
// Deleting      → executeNodeComponents(ActionUninstall) 卸载
// Ready         → 无匹配 case，直接返回 nil               稳态，无操作
// Failed        → 无匹配 case，直接返回 nil               等待人工介入
// Pending       → 无匹配 case，直接返回 nil               等待 Agent 推送
//
// 设计说明: Ready/Failed/Pending 不是操作态，Execute 中无对应 case，
// evaluateNodePhase 返回这些状态时 Execute 直接返回 nil，不执行任何操作。
// 这保证了幂等性——稳态节点 Reconcile 不会触发误操作。
```

**边界情况处理**：

| 场景 | 组件状态 | evaluateNodePhase 返回值 | Execute 行为 |
|------|---------|------------------------|-------------|
| 新节点首次安装 | 无状态记录 | `Provisioning` | 安装所有组件 |
| 安装中部分失败 | bkeagent=Installed, containerd=Failed | `Failed` | 不执行（等待人工介入） |
| 安装中部分进行中 | bkeagent=Installed, containerd=Installing | `Provisioning` | 继续安装未完成组件 |
| 全部安装完成 | 全部 Installed, 版本一致 | `Ready` | 无操作（幂等跳过） |
| 版本变更触发升级 | 全部 Installed, containerd 版本不一致 | `Upgrading` | 升级版本不一致的组件 |
| 升级中部分失败 | bkeagent=Upgrading, containerd=Failed | `Failed` | 不执行（等待人工介入） |
| 节点删除 | Deleted=true | `Deleting` | 卸载所有组件 |
| 组件部分卸载 | bkeagent=Deleting, containerd=Installed | `Deleting` | 继续卸载未完成组件 |

### 4.6 组件层状态机执行

**设计思路 — 组件层是三层状态机的执行终端**：

1. **状态评估 + Executor 分发两步走**：`ComponentStateMachine.Execute` 首先评估组件状态转换（`evaluateComponentPhase` 根据当前版本与目标版本判断 Install/Upgrade/Skip），然后根据 `cv.Spec.Type` 从 `executors` map 中获取对应 Executor 执行具体操作。状态评估与执行分离使得幂等判断（目标版本已达成则跳过）在状态层完成，Executor 只负责执行，无需重复判断。
2. **Executor 注册表与 dagexec.ExecutorRegistry 对齐**：`executors map[ComponentType]ComponentExecutor` 在设计上与 `dagexec.ExecutorRegistry` 等价，实际实现中可直接复用 `ExecutorRegistry` 而非维护独立的 map。`ComponentExecutor` 接口也与 `dagexec.ComponentExecutor` 对齐——`ExecuteComponent(ctx, node, execCtx)` 统一签名。
3. **组件状态存储在 BKENode.Status 中**：`ComponentLifecycleStatus` 存储在 `BKENode.Status.ComponentStatuses` 中（而非 BKECluster.Status），因为组件安装是 per-node 的——同一个组件在不同节点上可能有不同状态（如 containerd 在 node-1 上 Installed 但在 node-2 上 Installing）。这与 §3.4 的三层状态关系一致。
4. **幂等性保证**：`evaluateComponentPhase` 在目标版本等于当前版本时返回 `Installed`（跳过执行），保证 Reconcile 重入时不会重复安装。Executor 内部也通过 per-node per-component 状态（`NodeComponentStatuses`）实现幂等——已安装到目标版本的组件跳过执行。

```go
// ComponentStateMachine.Execute 执行组件级状态机
func (sm *ComponentStateMachine) Execute(
    ctx context.Context,
    status *ComponentLifecycleStatus,
    cv *ComponentVersion,
    action ComponentAction,
) error {
    // 1. 评估组件状态转换
    oldPhase := status.Phase
    newPhase := sm.evaluateComponentPhase(status, cv, action)
    
    if oldPhase != newPhase {
        status.Phase = newPhase
        sm.recordComponentTransition(status, oldPhase, newPhase)
    }
    
    // 2. 根据状态执行操作
    switch newPhase {
    case CompPhaseInstalling:
        executor := sm.executors[cv.Spec.Type]
        return executor.Install(ctx, status, cv)
    
    case CompPhaseUpgrading:
        executor := sm.executors[cv.Spec.Type]
        return executor.Upgrade(ctx, status, cv)
    
    case CompPhaseDeleting:
        executor := sm.executors[cv.Spec.Type]
        return executor.Uninstall(ctx, status, cv)
    }

    // Installed / Pending / Failed → 无操作，直接返回
    return nil
}
```

#### 4.6.1 evaluateComponentPhase 设计思路

**设计思路 — 单组件级别的状态决策**：

`evaluateComponentPhase` 是组件层状态机的核心决策函数，负责根据组件当前状态、目标版本和操作动作（Install/Upgrade/Uninstall）推断组件下一步状态。与 `evaluateNodePhase` 的自底向上聚合不同，`evaluateComponentPhase` 只关注单个组件的版本比较和操作类型，决策逻辑更简单、更确定。

1. **版本比较驱动状态转换**：函数的核心逻辑是比较 `status.Version`（已安装版本）与 `cv.Spec.Version`（目标版本）。版本一致 → Installed（稳态）；版本不一致 → Upgrading（需升级）；无版本记录 → Installing（需安装）。版本比较是幂等性的基础——Reconcile 重入时，已安装到目标版本的组件直接返回 Installed，跳过执行。
2. **操作动作覆盖版本判断**：当 `action == ActionUninstall` 时，无论版本是否一致，直接返回 Deleting。卸载是外部触发的强制操作（如节点删除），不受版本状态影响。这种优先级设计保证卸载操作不会被版本判断逻辑阻塞。
3. **Failed 状态的保持与恢复**：当组件当前为 Failed 时，`evaluateComponentPhase` 不自动恢复——仍返回 Failed，Execute 不执行任何操作，等待人工介入。人工介入后清除 Failed 状态（重置为 Pending），下次 Reconcile 才会重新评估。这种"Failed 不自愈"设计避免故障组件在未修复前被反复重试。
4. **Installing/Upgrading 的中间态保持**：如果组件当前正在 Installing 或 Upgrading（上次 Reconcile 未完成），函数返回原状态继续执行。这保证了跨 Reconcile 的操作连续性——上次未完成的安装/升级在下次 Reconcile 时继续，而非重新开始。
5. **与 evaluateNodePhase 的协作**：`evaluateNodePhase` 聚合所有组件状态决定节点级操作（Install/Upgrade/Uninstall），然后将 `action` 传递给 `executeNodeComponents` → `ComponentStateMachine.Execute` → `evaluateComponentPhase`。节点层决定"做什么操作"，组件层决定"这个组件在这个操作下应该处于什么状态"。

**状态决策规则矩阵**：

| 当前 Phase | action | status.Version vs cv.Spec.Version | 返回 Phase | Execute 行为 |
|-----------|--------|----------------------------------|-----------|-------------|
| Pending | Install | 无版本记录 | `Installing` | 执行 Install |
| Pending | Install | 有版本记录但不一致 | `Installing` | 执行 Install |
| Pending | Upgrade | — | `Upgrading` | 执行 Upgrade |
| Pending | Uninstall | — | `Deleting` | 执行 Uninstall |
| Installing | Install | — | `Installing` | 继续执行 Install（中间态保持） |
| Installed | Install | 版本一致 | `Installed` | 无操作（幂等跳过） |
| Installed | Install | 版本不一致 | `Installing` | 执行 Install（版本回退场景） |
| Installed | Upgrade | 版本一致 | `Installed` | 无操作（幂等跳过） |
| Installed | Upgrade | 版本不一致 | `Upgrading` | 执行 Upgrade |
| Installed | Uninstall | — | `Deleting` | 执行 Uninstall |
| Upgrading | Upgrade | — | `Upgrading` | 继续执行 Upgrade（中间态保持） |
| Upgrading | Install | — | `Installing` | 操作变更，切换为 Install |
| Deleting | Uninstall | — | `Deleting` | 继续执行 Uninstall（中间态保持） |
| Failed | * | — | `Failed` | 无操作（等待人工介入） |

#### 4.6.2 evaluateComponentPhase 实现

```go
// evaluateComponentPhase 评估组件生命周期阶段
// 根据当前状态、操作动作和版本比较推断组件下一步状态
//
// 决策优先级:
//   1. Failed → 保持 Failed (不自愈，等待人工介入)
//   2. action == Uninstall → Deleting (卸载是强制操作)
//   3. 中间态保持 (Installing/Upgrading/Deleting 继续执行)
//   4. 版本比较驱动 (版本一致→Installed, 不一致→Upgrading/Installing)
//   5. Pending → 根据 action 决定 Installing/Upgrading/Deleting
func (sm *ComponentStateMachine) evaluateComponentPhase(
    status *ComponentLifecycleStatus,
    cv *ComponentVersion,
    action ComponentAction,
) ComponentLifecyclePhase {
    currentPhase := status.Phase

    // 1. Failed 状态不自愈 — 等待人工介入清除
    if currentPhase == CompPhaseFailed {
        return CompPhaseFailed
    }

    // 2. 卸载操作优先 — 无论当前状态如何
    if action == ActionUninstall {
        // 已卸载完成则保持 Deleting（等待状态清理）
        // 否则进入 Deleting
        if currentPhase == CompPhaseDeleting {
            return CompPhaseDeleting // 中间态保持
        }
        return CompPhaseDeleting
    }

    // 3. 中间态保持 — 上次未完成的操作继续执行
    switch currentPhase {
    case CompPhaseInstalling:
        // 安装中：根据 action 决定是否切换操作
        if action == ActionUpgrade {
            return CompPhaseUpgrading // 操作变更：安装→升级
        }
        return CompPhaseInstalling // 继续安装

    case CompPhaseUpgrading:
        // 升级中：根据 action 决定是否切换操作
        if action == ActionInstall {
            return CompPhaseInstalling // 操作变更：升级→安装
        }
        return CompPhaseUpgrading // 继续升级

    case CompPhaseDeleting:
        // 卸载中：继续卸载（卸载不受 action 影响，已由步骤 2 处理）
        return CompPhaseDeleting
    }

    // 4. 稳态判断 — 版本比较驱动
    // 到达此处的当前状态: Pending 或 Installed
    targetVersion := cv.Spec.Version
    installedVersion := status.Version

    if installedVersion == "" {
        // 无版本记录 → 首次安装
        switch action {
        case ActionInstall:
            return CompPhaseInstalling
        case ActionUpgrade:
            // 升级场景下无版本记录也视为安装
            return CompPhaseInstalling
        }
    }

    if installedVersion == targetVersion {
        // 已安装版本与目标版本一致 → 稳态
        // 幂等保证: Reconcile 重入时跳过已完成的安装/升级
        return CompPhaseInstalled
    }

    // 版本不一致 → 需要安装或升级
    switch action {
    case ActionInstall:
        // 版本不一致的安装（如版本回退场景）
        return CompPhaseInstalling
    case ActionUpgrade:
        return CompPhaseUpgrading
    }

    // 5. 兜底: Pending + 未知 action
    return CompPhasePending
}
```

**evaluateComponentPhase 与 Execute 的协作**：

```go
// evaluateComponentPhase 返回值 → Execute switch 分支映射:
//
// Installing  → executor.Install(ctx, status, cv)    执行安装
// Upgrading   → executor.Upgrade(ctx, status, cv)    执行升级
// Deleting    → executor.Uninstall(ctx, status, cv)  执行卸载
// Installed   → 无匹配 case，直接返回 nil             稳态，幂等跳过
// Pending     → 无匹配 case，直接返回 nil             等待前置依赖
// Failed      → 无匹配 case，直接返回 nil             等待人工介入
//
// 设计说明: Installed/Pending/Failed 是非操作态，Execute 中无对应 case，
// 直接返回 nil。这保证了:
// - 幂等性: 已安装到目标版本的组件 Reconcile 时跳过
// - 依赖等待: Pending 组件等待前置依赖完成后才被触发
// - 故障隔离: Failed 组件不自动重试，避免故障扩散
```

**边界情况处理**：

| 场景 | 当前 Phase | action | 版本关系 | 返回 Phase | Execute 行为 | 说明 |
|------|-----------|--------|---------|-----------|-------------|------|
| 首次安装 | Pending | Install | 无版本记录 | `Installing` | 执行 Install | 新组件首次安装 |
| 首次安装（升级路径） | Pending | Upgrade | 无版本记录 | `Installing` | 执行 Install | 升级场景下无版本也走安装 |
| 幂等跳过 | Installed | Install | 版本一致 | `Installed` | 无操作 | Reconcile 重入跳过 |
| 幂等跳过 | Installed | Upgrade | 版本一致 | `Installed` | 无操作 | 已升级到目标版本 |
| 版本变更升级 | Installed | Upgrade | 版本不一致 | `Upgrading` | 执行 Upgrade | 正常升级流程 |
| 版本回退安装 | Installed | Install | 版本不一致 | `Installing` | 执行 Install | 回退到旧版本 |
| 中间态保持 | Installing | Install | — | `Installing` | 继续 Install | 跨 Reconcile 连续性 |
| 操作切换 | Installing | Upgrade | — | `Upgrading` | 执行 Upgrade | 安装中切换为升级 |
| 故障不自愈 | Failed | Install | — | `Failed` | 无操作 | 等待人工清除 Failed |
| 强制卸载 | Installed | Uninstall | — | `Deleting` | 执行 Uninstall | 节点删除触发 |
| 卸载中保持 | Deleting | Uninstall | — | `Deleting` | 继续 Uninstall | 跨 Reconcile 连续性 |

---

## 5. 可观测性设计

### 5.1 可观测性架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          可观测性三层架构                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Layer 1: 状态可观测                                                    │   │
│  │  ─────────────────────                                                  │   │
│  │  • ClusterVersion.Status.LifecyclePhase     (集群层状态)                │   │
│  │  • BKENode.Status.LifecyclePhase            (节点层状态)                │   │
│  │  • ComponentLifecycleStatus.Phase           (组件层状态)                │   │
│  │  • OperationProgress                        (操作进度)                  │   │
│  │  • Conditions                               (状态条件)                  │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Layer 2: 事件可观测                                                    │   │
│  │  ─────────────────────                                                  │   │
│  │  • StateTransition events                   (状态转换事件)              │   │
│  │  • OperationStarted/Completed/Failed events (操作事件)                  │   │
│  │  • ComponentInstalled/Upgraded/Failed       (组件事件)                  │   │
│  │  • DAGNodeStarted/Completed/Failed          (DAG 节点事件)             │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Layer 3: 指标可观测                                                    │   │
│  │  ─────────────────────                                                  │   │
│  │  • bke_state_transition_total               (状态转换计数)              │   │
│  │  • bke_state_transition_duration_seconds    (状态转换耗时)              │   │
│  │  • bke_operation_total                      (操作计数)                  │   │
│  │  • bke_operation_duration_seconds           (操作耗时)                  │   │
│  │  • bke_dag_execution_duration_seconds       (DAG 执行耗时)             │   │
│  │  • bke_component_install_total              (组件安装计数)              │   │
│  │  • bke_node_ready_count                     (就绪节点数)                │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 状态查询 API

```bash
# 查询集群状态
kubectl get bkecluster my-cluster -o jsonpath='{.status}'

# 查询节点状态
kubectl get bkenode -l cluster=my-cluster -o wide

# 查询组件状态
kubectl get bkecluster my-cluster -o jsonpath='{.status.componentStatuses}'

# 查询操作进度
kubectl get bkecluster my-cluster -o jsonpath='{.status.operationProgress}'

# 查询状态转换历史（通过 Events）
kubectl get events --field-selector involvedObject.name=my-cluster,reason=StateTransition

# 查询操作历史
kubectl get events --field-selector involvedObject.name=my-cluster,reason=OperationCompleted
```

### 5.3 Prometheus 指标

```go
var (
    // 状态转换指标
    stateTransitionTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "bke_state_transition_total",
            Help: "Total number of state transitions",
        },
        []string{"layer", "cluster", "old_phase", "new_phase"},
    )
    
    stateTransitionDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "bke_state_transition_duration_seconds",
            Help:    "Duration of state transitions",
            Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
        },
        []string{"layer", "cluster"},
    )
    
    // DAG 执行指标
    dagExecutionDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "bke_dag_execution_duration_seconds",
            Help:    "Duration of DAG execution",
            Buckets: prometheus.ExponentialBuckets(1, 2, 10),
        },
        []string{"cluster", "operation"},
    )
    
    dagNodeDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "bke_dag_node_execution_duration_seconds",
            Help:    "Duration of DAG node execution",
            Buckets: prometheus.ExponentialBuckets(1, 2, 10),
        },
        []string{"cluster", "node_type", "node_name"},
    )
    
    // 组件执行指标
    componentOperationTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "bke_component_operation_total",
            Help: "Total number of component operations",
        },
        []string{"cluster", "node", "component", "operation", "status"},
    )
    
    // 节点状态指标
    nodePhaseGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_node_phase",
            Help: "Current node phase (0=Pending, 1=Provisioning, 2=Ready, 3=Upgrading, 4=Deleting, 5=Failed)",
        },
        []string{"cluster", "node"},
    )
    
    nodeReadyCount = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_node_ready_count",
            Help: "Number of ready nodes",
        },
        []string{"cluster"},
    )
)
```

### 5.4 告警规则

```yaml
groups:
  - name: bke-state-machine-alerts
    rules:
      # 集群操作失败告警
      - alert: BKEClusterOperationFailed
        expr: bke_state_transition_total{layer="cluster", new_phase="Failed"} > 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "BKE cluster operation failed"
          description: "Cluster {{ $labels.cluster }} operation failed"
      
      # 节点长时间未就绪告警
      - alert: BKENodeNotReady
        expr: bke_node_phase{phase!="Ready"} > 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "BKE node not ready"
          description: "Node {{ $labels.node }} in cluster {{ $labels.cluster }} not ready for 10m"
      
      # DAG 执行超时告警
      - alert: BKEDAGExecutionTimeout
        expr: bke_dag_execution_duration_seconds{quantile="0.99"} > 1800
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "BKE DAG execution timeout"
          description: "DAG execution in cluster {{ $labels.cluster }} took too long"
      
      # 组件操作失败告警
      - alert: BKEComponentOperationFailed
        expr: bke_component_operation_total{status="failed"} > 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "BKE component operation failed"
          description: "Component {{ $labels.component }} on node {{ $labels.node }} failed"
```

---

## 6. Cluster API 集成设计

### 6.1 与 CAPI 的集成架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        Cluster API 集成架构                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Cluster API Controllers                                                │   │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐      │   │
│  │  │ Cluster          │  │ Machine          │  │ MachineDeployment│      │   │
│  │  │ Controller       │  │ Controller       │  │ Controller       │      │   │
│  │  └────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘      │   │
│  └───────────┼──────────────────────┼──────────────────────┼───────────────┘   │
│              │                      │                      │                   │
│              │ Watch                │ Watch                │ Watch             │
│              ▼                      ▼                      ▼                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  BKE Infrastructure Provider                                            │   │
│  │  ┌──────────────────┐  ┌──────────────────┐                            │   │
│  │  │ BKECluster       │  │ BKEMachine       │                            │   │
│  │  │ Controller       │  │ Controller       │                            │   │
│  │  │                  │  │                  │                            │   │
│  │  │ 调用:            │  │ 调用:            │                            │   │
│  │  │ engine.Execute() │  │ engine.Execute() │                            │   │
│  │  └────────┬─────────┘  └────────┬─────────┘                            │   │
│  └───────────┼──────────────────────┼─────────────────────────────────────┘   │
│              │                      │                                          │
│              ▼                      ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  StateMachineEngine                                                     │   │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │  │ ClusterStateMachine (L1)                                        │   │   │
│  │  │ ┌─────────────────────────────────────────────────────────┐     │   │   │
│  │  │ │ NodeStateMachine (L2) × N                               │     │   │   │
│  │  │ │ ┌─────────────────────────────────────────────────┐     │     │   │   │
│  │  │ │ │ ComponentStateMachine (L3)                      │     │     │   │   │
│  │  │ │ └─────────────────────────────────────────────────┘     │     │   │   │
│  │  │ └─────────────────────────────────────────────────────────┘     │   │   │
│  │  └─────────────────────────────────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 6.2 BKECluster Controller 集成

```go
// BKEClusterReconciler 集成状态机引擎
type BKEClusterReconciler struct {
    client.Client
    Scheme   *runtime.Scheme
    Recorder record.EventRecorder
    
    // 状态机引擎
    engine *statemachine.ClusterStateMachine
}

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)
    
    // 1. 获取 BKECluster
    cluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. 创建 PatchHelper
    patchHelper, err := patch.NewHelper(cluster, r.Client)
    if err != nil {
        return ctrl.Result{}, err
    }
    defer func() {
        if err := patchHelper.Patch(ctx, cluster); err != nil {
            log.Error(err, "failed to patch cluster")
        }
    }()
    
    // 3. 执行状态机引擎
    if err := r.engine.Execute(ctx, cluster); err != nil {
        log.Error(err, "state machine execution failed")
        // 记录事件
        r.Recorder.Eventf(cluster, v1.EventTypeWarning, "StateMachineFailed", 
            "State machine execution failed: %v", err)
    }
    
    // 4. 同步旧字段（兼容性）
    statemachine.SyncLegacyFields(cluster)
    
    // 5. 记录指标
    r.recordMetrics(cluster)
    
    // 6. 决定 Requeue
    return r.decideRequeue(cluster), nil
}
```

### 6.3 BKEMachine Controller 集成

```go
// BKEMachineReconciler 集成节点层状态机
type BKEMachineReconciler struct {
    client.Client
    Scheme   *runtime.Scheme
    Recorder record.EventRecorder
    
    // 状态机引擎
    engine *statemachine.ClusterStateMachine
    
    // ReleaseImage 解析器
    releaseImageResolver *ReleaseImageResolver
}

func (r *BKEMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)
    
    // 1. 获取 BKEMachine
    machine := &bkev1beta1.BKEMachine{}
    if err := r.Get(ctx, req.NamespacedName, machine); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. 获取关联的 BKECluster
    cluster, err := getClusterForMachine(ctx, r.Client, machine)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. 创建 PatchHelper
    patchHelper, err := patch.NewHelper(machine, r.Client)
    if err != nil {
        return ctrl.Result{}, err
    }
    defer func() {
        if err := patchHelper.Patch(ctx, machine); err != nil {
            log.Error(err, "failed to patch machine")
        }
    }()
    
    // 4. 获取节点级组件列表（从 ReleaseImage 中解析）
    nodeComponents, err := r.getNodeComponents(ctx, cluster)
    if err != nil {
        log.Error(err, "failed to get node components")
        return ctrl.Result{}, err
    }
    
    // 5. 执行节点层状态机
    node := machine.ToBKENode()
    if err := r.engine.NodeSM().Execute(ctx, node, nodeComponents); err != nil {
        log.Error(err, "node state machine execution failed")
        r.Recorder.Eventf(machine, v1.EventTypeWarning, "NodeStateMachineFailed",
            "Node state machine failed: %v", err)
    }
    
    // 6. 同步状态回 BKEMachine
    machine.UpdateFromBKENode(node)
    
    // 7. 决定 Requeue
    return r.decideRequeue(machine), nil
}

// getNodeComponents 从 ReleaseImage 中获取节点级组件列表
func (r *BKEMachineReconciler) getNodeComponents(ctx context.Context, cluster *bkev1beta1.BKECluster) ([]*ComponentVersion, error) {
    // 1. 获取当前操作的 ReleaseImage
    releaseImage, err := r.releaseImageResolver.Resolve(ctx, cluster)
    if err != nil {
        return nil, fmt.Errorf("failed to resolve release image: %w", err)
    }
    
    // 2. 展开 composite 并获取节点级组件（binary 类型）
    var nodeComponents []*ComponentVersion
    for _, comp := range releaseImage.Spec.Components {
        cv, err := r.lookupComponentVersion(ctx, comp.Name, comp.Version)
        if err != nil {
            return nil, fmt.Errorf("failed to lookup component %s: %w", comp.Name, err)
        }
        
        if cv.Spec.Type == ComponentTypeBinary {
            nodeComponents = append(nodeComponents, cv)
        }
    }
    
    // 3. 按依赖关系排序
    sorted, err := topologicalSort(nodeComponents)
    if err != nil {
        return nil, fmt.Errorf("failed to sort components: %w", err)
    }
    
    return sorted, nil
}

// lookupComponentVersion 查找 ComponentVersion
func (r *BKEMachineReconciler) lookupComponentVersion(ctx context.Context, name, version string) (*ComponentVersion, error) {
    cv := &cvoapi.ComponentVersion{}
    key := client.ObjectKey{
        Namespace: "bke-system",  // ComponentVersion 存储在固定命名空间
        Name:      fmt.Sprintf("%s-%s", name, version),
    }
    
    if err := r.Get(ctx, key, cv); err != nil {
        return nil, err
    }
    
    return cv, nil
}

// topologicalSort 按依赖关系对组件进行拓扑排序
func topologicalSort(components []*ComponentVersion) ([]*ComponentVersion, error) {
    // 构建依赖图
    graph := make(map[string][]string)
    inDegree := make(map[string]int)
    
    for _, comp := range components {
        if _, ok := inDegree[comp.Name]; !ok {
            inDegree[comp.Name] = 0
        }
        
        for _, dep := range comp.Spec.Dependencies {
            // 只统计节点级组件的依赖
            if isNodeComponent(components, dep.Name) {
                graph[dep.Name] = append(graph[dep.Name], comp.Name)
                inDegree[comp.Name]++
            }
        }
    }
    
    // Kahn 算法拓扑排序
    var queue []string
    for name, degree := range inDegree {
        if degree == 0 {
            queue = append(queue, name)
        }
    }
    
    var sorted []*ComponentVersion
    for len(queue) > 0 {
        name := queue[0]
        queue = queue[1:]
        
        // 找到对应的组件
        for _, comp := range components {
            if comp.Name == name {
                sorted = append(sorted, comp)
                break
            }
        }
        
        // 减少依赖该组件的组件的入度
        for _, dependent := range graph[name] {
            inDegree[dependent]--
            if inDegree[dependent] == 0 {
                queue = append(queue, dependent)
            }
        }
    }
    
    if len(sorted) != len(components) {
        return nil, fmt.Errorf("circular dependency detected")
    }
    
    return sorted, nil
}

// isNodeComponent 检查组件是否为节点级组件
func isNodeComponent(components []*ComponentVersion, name string) bool {
    for _, comp := range components {
        if comp.Name == name {
            return comp.Spec.Scope == ComponentScopeNode
        }
    }
    return false
}
```

### 6.4 CAPI 条件集成

```go
// 设置 CAPI 标准条件
func setCAPIConditions(cluster *bkev1beta1.BKECluster) {
    switch cluster.Status.LifecyclePhase {
    case ClusterPhaseRunning:
        conditions.MarkTrue(cluster, clusterv1.ReadyCondition)
        
    case ClusterPhaseInstalling, ClusterPhaseUpgrading, ClusterPhaseScaling:
        conditions.MarkFalse(cluster, clusterv1.ReadyCondition,
            "OperationInProgress", clusterv1.ConditionSeverityInfo,
            "Operation %s in progress", cluster.Status.LifecyclePhase)
        
    case ClusterPhaseFailed:
        conditions.MarkFalse(cluster, clusterv1.ReadyCondition,
            "OperationFailed", clusterv1.ConditionSeverityError,
            "Operation failed, manual intervention required")
    }
}
```

---

## 7. 完整执行流程

### 7.1 安装流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          集群安装完整流程                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Reconcile #1                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Pending → Installing                           │   │
│  │    Condition: desiredVersion != "" && currentVersion == ""               │   │
│  │                                                                          │   │
│  │ 2. BuildDAG(releaseImage)                                                │   │
│  │    DAG: [certs] → [bkeagent→containerd→kubelet] → [coredns] → [kube-proxy]               │   │
│  │                                                                          │   │
│  │ 3. ExecuteDAG(ctx, dag)                                                  │   │
│  │    ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │    │ Batch 1: [certs]                                                │   │   │
│  │    │   └─ ComponentNode.Execute() → L3: Installing           │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 2: [bkeagent, containerd, kubelet]                                           │   │   │
│  │    │   └─ BinaryComponentExecutor.Execute() (逐节点)                                    │   │   │
│  │    │       ┌─────────────────────────────────────────────────────┐   │   │   │
│  │    │       │ 并行执行所有节点:                                     │   │   │   │
│  │    │       │                                                       │   │   │   │
│  │    │       │ Node-1: L2: Provisioning                              │   │   │   │
│  │    │       │   └─ L3: bkeagent Installing                         │   │   │   │
│  │    │       │   └─ L3: containerd Pending (等待 bkeagent)           │   │   │   │
│  │    │       │                                                       │   │   │   │
│  │    │       │ Node-2: L2: Provisioning                              │   │   │   │
│  │    │       │   └─ L3: bkeagent Installing                         │   │   │   │
│  │    │       │   └─ L3: containerd Pending                          │   │   │   │
│  │    │       │                                                       │   │   │   │
│  │    │       │ ...                                                   │   │   │   │
│  │    │       └─────────────────────────────────────────────────────┘   │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 3: [coredns]                                              │   │   │
│  │    │   └─ ComponentNode.Execute() → L3: Installing           │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 4: [kube-proxy]                                           │   │   │
│  │    │   └─ ComponentNode.Execute() → L3: Installing           │   │   │
│  │    └─────────────────────────────────────────────────────────────────┘   │   │
│  │                                                                          │   │
│  │ 4. DAG 执行完成，等待组件安装完成                                          │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #2 (bkeagent 安装完成后)                                             │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Installing (无变化)                             │   │
│  │                                                                          │   │
│  │ 2. BuildDAG + ExecuteDAG                                                 │   │
│  │    └─ Batch 2: [bkeagent, containerd, kubelet]                                              │   │
│  │       └─ BinaryComponentExecutor.Execute() (逐节点)                                         │   │
│  │           └─ 并行执行所有节点:                                            │   │
│  │              Node-1: L2: Provisioning                                    │   │
│  │                └─ L3: bkeagent Installed ✓                               │   │
│  │                └─ L3: containerd Installing                              │   │
│  │              Node-2: L2: Provisioning                                    │   │
│  │                └─ L3: bkeagent Installed ✓                               │   │
│  │                └─ L3: containerd Installing                              │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #N (所有组件安装完成后)                                               │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Installing → Running                           │   │
│  │    Condition: 所有组件 Installed                                         │   │
│  │                                                                          │   │
│  │ 2. 记录升级完成事件                                                       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.2 升级流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          集群升级完整流程                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  用户: kubectl patch bkecluster my-cluster --type merge                         │
│        -p '{"spec":{"desiredVersion":"v2.7.0"}}'                                │
│                                                                                 │
│  Reconcile #1                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Running → Upgrading                            │   │
│  │    Condition: desiredVersion != currentVersion                           │   │
│  │                                                                          │   │
│  │ 2. BuildDAG(newReleaseImage)                                             │   │
│  │    DAG: [certs] → [bkeagent→containerd→kubelet] → [coredns] → [kube-proxy]               │   │
│  │                                                                          │   │
│  │ 3. ExecuteDAG(ctx, dag)                                                  │   │
│  │    └─ Batch 2: [bkeagent, containerd, kubelet]                                              │   │
│  │       └─ BinaryComponentExecutor.Execute() (逐节点)                                         │   │
│  │           └─ 并行执行所有节点:                                            │   │
│  │              Node-1: L2: Ready → Upgrading                               │   │
│  │                └─ L3: containerd Installed → Upgrading                   │   │
│  │                └─ L3: kubelet Installed → Upgrading                      │   │
│  │              Node-2: L2: Ready → Upgrading                               │   │
│  │                └─ L3: containerd Installed → Upgrading                   │   │
│  │                └─ L3: kubelet Installed → Upgrading                      │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #N (所有组件升级完成后)                                               │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Upgrading → Running                            │   │
│  │    Condition: 所有组件升级完成                                            │   │
│  │                                                                          │   │
│  │ 2. 更新 currentVersion = "v2.7.0"                                        │   │
│  │ 3. 记录升级完成事件                                                       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 8. 总结

### 8.1 设计优势

| 优势 | 说明 |
|------|------|
| **统一入口** | BKECluster Reconcile 统一触发，架构清晰 |
| **DAG 驱动** | 从 ReleaseImage 构建 DAG，执行顺序由依赖关系决定 |
| **节点级并行** | 节点组节点并行执行所有节点，提升大规模集群效率 |
| **三层清晰** | 集群层、节点层、组件层职责分明，状态转换清晰 |
| **可观测** | 状态、事件、指标三层可观测，全链路可追踪 |
| **CAPI 兼容** | 与 Cluster API Controller 模式天然兼容 |

### 8.2 与 v3 的主要区别

| 维度 | v3 设计 | v4 设计 |
|------|--------|--------|
| **执行入口** | L1 直接遍历节点 | BKECluster Reconcile 触发 |
| **DAG 构建** | 无 DAG | 从 ReleaseImage 构建 DAG |
| **节点级组件** | 分散在各处 | 通过 composite 封装，展开为独立节点 |
| **节点执行** | 串行遍历 | 并行执行 |
| **状态驱动** | L1 驱动 L2 驱动 L3 | 每层独立状态机，通过 DAG 协调 |

---

**文档版本**: v4.0  
**维护者**: openFuyao Team

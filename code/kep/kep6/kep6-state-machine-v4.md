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

```go
// ClusterStateMachine 集群层状态机引擎 (L1)
type ClusterStateMachine struct {
    client    client.Client
    recorder  record.EventRecorder
    
    // DAG 执行器
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
    // 组件执行器注册表
    executors map[ComponentType]ComponentExecutor
}
```

### 4.2 DAG 执行器

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


### 4.4 节点层状态机执行

```go
// NodeStateMachine.Execute 执行节点层状态机
func (sm *NodeStateMachine) Execute(ctx context.Context, node *BKENode, components []*ComponentVersion) error {
    // 1. 评估节点状态转换
    oldPhase := node.Status.LifecyclePhase
    newPhase := sm.evaluateNodePhase(node)
    
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

### 4.5 组件层状态机执行

```go
// ComponentStateMachine.Execute 执行组件级状态机
func (sm *ComponentStateMachine) Execute(
    ctx context.Context,
    status *ComponentLifecycleStatus,
    cv *ComponentVersion,
) error {
    // 1. 评估组件状态转换
    oldPhase := status.Phase
    newPhase := sm.evaluateComponentPhase(status, cv)
    
    if oldPhase != newPhase {
        status.Phase = newPhase
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
    
    return nil
}
```

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

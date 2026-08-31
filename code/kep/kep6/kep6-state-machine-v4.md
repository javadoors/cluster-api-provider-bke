# KEP-6 状态机设计 v4：基于 DAG 的三层状态机引擎

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-6 |
| **标题** | 基于 DAG 的三层状态机引擎设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **依赖** | KEP-5 声明式升级框架、Cluster API v1beta1 |

---

## 1. 设计目标与约束

### 1.1 设计目标

1. **统一执行入口**：BKECluster Reconcile 触发集群层状态机引擎执行
2. **DAG 驱动执行**：集群层状态机从 ReleaseImage 构建执行 DAG
3. **节点级并行**：binary 组件由 BinaryComponentExecutor 内部支持 Rolling/Parallel/Batch 节点级并发，多节点并行执行
4. **三层状态清晰**：集群层、节点层、组件层状态定义清晰，职责分明
5. **可观测性**：状态转换、执行进度、健康状态全链路可观测
6. **CAPI 集成**：架构设计与 Cluster API 天然兼容

### 1.2 设计约束

| 约束 | 说明 |
|------|------|
| **统一 DAG 节点** | DAG 中所有组件均为统一的 `ComponentNode`，不区分集群级/节点级节点类型 |
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

**核心设计**：DAG 中所有组件均为统一的 `ComponentNode`，不区分集群级/节点级节点类型。K8s 核心组件和节点二进制组件通过 `composite` 类型（KEP-15）封装，DAG 构建时自动展开为子组件节点。

**设计思路 — 统一 ComponentNode，通过 composite 封装组件分组**：

1. **yaml/helm 本身就是集群级操作**：yaml/helm 组件通过 K8s API 或 Helm SDK 部署到目标集群，天然是集群级操作。
2. **binary 组件通过 Executor 内部策略实现节点级并发**：binary 组件通过 SSH 在节点上执行，`BinaryComponentExecutor` 内部支持 Rolling/Parallel/Batch 节点级并发策略。
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

// ComponentNode 是升级 DAG 中的一个顶点 (统一节点类型)
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

**与 v3 设计的区别**：

| 维度 | v3 设计 | v4 设计 (复用代码库) |
|------|---------|-------------------|
| **DAG 节点类型** | 无 DAG，Phase 框架直接调度 | `topology.ComponentNode` (统一一种) |
| **节点接口** | 无统一接口 | `ComponentExecutor.ExecuteComponent()` 统一接口 |
| **类型分发** | 硬编码分发 | `ExecutorRegistry` 按 `ComponentType` 动态分发 |
| **节点级并发** | 串行遍历节点 | `BinaryComponentExecutor` 内部 `upgradeStrategy.mode` |


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
│  │  DAG 执行 (所有组件为统一 ComponentNode, composite 构建时展开):          │   │
│  │  ┌──────────┐   ┌──────────┐   ┌──────────────────┐   ┌──────────┐    │   │
│  │  │  etcd    │──>│ apiserver│──>│   kubelet        │──>│ coredns  │    │   │
│  │  │(staticpod)  │(staticpod)  │   (binary)         │   │ (helm)   │    │   │
│  │  └──────────┘   └──────────┘   └──────────────────┘   └──────────┘    │   │
│  │                                     │                                     │   │
│  │  ┌──────────┐   ┌──────────┐   ┌──────────────────┐                     │   │
│  │  │ bkeagent │──>│containerd│──>│ kube-proxy       │                     │   │
│  │  │(binary)  │   │(binary)  │   │ (yaml)            │                     │   │
│  │  └──────────┘   └──────────┘   └──────────────────┘                     │   │
│  │                                     │                                     │   │
│  │                       ┌─────────────┼─────────────┐                     │   │
│  │                       ▼             ▼             ▼                     │   │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │  │  L3: ComponentStateMachine (组件层状态机)                        │   │   │
│  │  │  ─────────────────────────────────                                │   │   │
│  │  │  职责: 管理单个组件的生命周期                                      │   │   │
│  │  │  状态: Pending → Installing → Installed → Upgrading              │   │   │
│  │  │  执行: Install / Upgrade / Uninstall 操作                        │   │   │
│  │  │                                                                    │   │   │
│  │  │  binary 组件由 BinaryComponentExecutor SSH 逐节点执行              │   │   │
│  │  │  yaml/helm 组件由对应 Executor 集群级一次执行                      │   │   │
│  │  │  staticpod 组件由 StaticPodComponentExecutor 拉起/替换            │   │   │
│  │  │  inline 组件由 InlineComponentExecutor Phase handler 执行           │   │   │
│  │  └─────────────────────────────────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  L2: NodeStateMachine (节点层状态机, 可选)                               │   │
│  │  ─────────────────────────────────────                                   │   │
│  │  职责: 单节点生命周期管理 (扩缩容 Watch 触发路径 + 故障恢复)               │   │
│  │  状态: Pending → Provisioning → Ready → Upgrading → Deleting → Failed  │   │
│  │  仅在可选路径 (Watch 触发) 或故障恢复时激活                                │   │
│  │  DAG 内联路径中 L2 不参与 (由 syncNodeStatus 回写节点状态)                │   │
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
    scheduler *dagexec.Scheduler

    // VersionContext 管理 (统一 DAG 操作语义的载体, 见 §4.4.4)
    versionContext *VersionContext

    // ReleaseImage 解析器
    releaseImageResolver *ReleaseImageResolver

    // ComponentVersion 存储
    cvStore ComponentVersionStore

    // 节点 Provider
    nodeProvider NodeProvider

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

1. **L1 评估状态、委托 DAG 执行**：`ClusterStateMachine.Execute` 的核心职责是评估集群状态转换（`evaluateClusterPhase`）并构建/执行统一 DAG。集群层不直接操作 Executor，而是委托给 `dagexec.Scheduler.ExecuteDAG`（§4.2），由 Scheduler 按拓扑批次分发到各 `ComponentExecutor`。这种分层委托保证集群层只关注"做什么操作"（Install/Upgrade/Rollback），不关心"怎么做"（SSH/Helm/K8s API）。
2. **统一 DAG，操作语义由 VersionContext 承载**：install/upgrade/rollback 共用同一个 `buildDAG`——DAG 拓扑结构（组件依赖图）与操作类型解耦。操作语义（Install vs Upgrade vs Rollback）由 `prepareVersionContext` 设置到 VersionContext 中，Scheduler 执行时通过 `NeedsUpgrade` 逐组件判断 Install/Upgrade/Skip。这与 §4.3 的"类型分发延迟到执行期"哲学一致——DAG 构建期不感知操作类型，操作分发延迟到执行期。
3. **集群状态由 DAG 执行结果驱动**：与节点层 `evaluateNodePhase` 自底向上聚合组件状态不同，集群层状态主要由 DAG 执行的总体结果驱动——DAG 全部成功则集群进入 Running/稳态，DAG 失败则集群进入 Failed。集群层不需要逐组件聚合，因为 Scheduler 已在 DAG 执行过程中处理了组件级状态。
4. **DAG 构建与执行分离**：每次 Reconcile 时，`Execute` 先准备 VersionContext（`prepareVersionContext`）、构建统一 DAG（`buildDAG`），再执行 DAG（`scheduler.ExecuteDAG`）。DAG 构建是幂等的——相同的 ReleaseImage 产生相同的 DAG 结构，VersionContext 决定哪些组件需要执行。DAG 执行也是幂等的——已完成的组件通过 `VersionContext.NeedsUpgrade` 跳过，未完成的继续执行。
5. **Reconcile 重入安全**：`Execute` 是幂等的。当 Reconcile 因组件执行中断而重入时，`evaluateClusterPhase` 根据当前集群状态（Installing/Upgrading 等）决定继续执行 DAG，而非重新开始。DAG 内部的组件级幂等（`evaluateComponentPhase` 返回 Installed 跳过）保证已完成的组件不会重复执行。

```go
// ClusterStateMachine.Execute 执行集群层状态机
// 由 BKEClusterReconciler.Reconcile() 调用，是整个状态机的唯一入口
// 统一 DAG: install/upgrade/rollback 共用 buildDAG, 操作语义由 VersionContext 承载
func (sm *ClusterStateMachine) Execute(ctx context.Context, cluster *bkev1beta1.BKECluster) error {
    // 1. 评估集群状态转换
    oldPhase := cluster.Status.LifecyclePhase
    newPhase := sm.evaluateClusterPhase(ctx, cluster)

    if oldPhase != newPhase {
        cluster.Status.LifecyclePhase = newPhase
        sm.recordClusterTransition(cluster, oldPhase, newPhase)
    }

    // 2. 准备 VersionContext (统一设置操作语义, 见 §4.4.4)
    //    VersionContext 承载操作类型 (Install/Upgrade/Rollback), DAG 结构不感知操作类型
    if err := sm.prepareVersionContext(ctx, cluster, newPhase); err != nil {
        return fmt.Errorf("prepare version context failed: %w", err)
    }

    // 3. 根据 L1 Phase 执行统一 DAG
    switch newPhase {
    case ClusterPhaseInstalling, ClusterPhaseUpgrading, ClusterPhaseRollingBack:
        // 统一 DAG: 同一 buildDAG 构建, VersionContext 决定 per-component Install/Upgrade/Skip
        dag, err := sm.buildDAG(ctx, cluster)
        if err != nil {
            return fmt.Errorf("build DAG failed: %w", err)
        }
        execCtx := sm.buildExecutionContext(ctx, cluster)
        if err := sm.scheduler.ExecuteDAG(ctx, execCtx, dag); err != nil {
            // 即使 DAG 执行失败也尝试同步节点状态（部分组件可能已成功）
            _ = sm.syncNodeStatus(ctx, cluster)
            return fmt.Errorf("execute DAG failed: %w", err)
        }
        // DAG 执行成功后同步节点状态
        if err := sm.syncNodeStatus(ctx, cluster); err != nil {
            sm.recorder.Eventf(cluster, v1.EventTypeWarning,
                "NodeStatusSyncFailed", "failed to sync node status: %v", err)
        }
        cluster.Status.CurrentVersion = cluster.Spec.DesiredVersion

    case ClusterPhaseScaling:
        // 扩缩容: 统一 DAG 的结构变体 (binary 过滤 + drain 节点, 见 §4.4.4)
        dag, err := sm.buildScalingDAG(ctx, cluster)
        if err != nil {
            return fmt.Errorf("build scaling DAG failed: %w", err)
        }
        execCtx := sm.buildExecutionContext(ctx, cluster)
        if err := sm.scheduler.ExecuteDAG(ctx, execCtx, dag); err != nil {
            _ = sm.syncNodeStatus(ctx, cluster)
            return fmt.Errorf("execute scaling DAG failed: %w", err)
        }
        if err := sm.syncNodeStatus(ctx, cluster); err != nil {
            sm.recorder.Eventf(cluster, v1.EventTypeWarning,
                "NodeStatusSyncFailed", "failed to sync node status: %v", err)
        }
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
// Installing   → prepareVersionContext + buildDAG + ExecuteDAG + syncNodeStatus
// Upgrading    → prepareVersionContext + buildDAG + ExecuteDAG + syncNodeStatus
// RollingBack  → prepareVersionContext + buildDAG + ExecuteDAG + syncNodeStatus
//    ← 统一 DAG: 三个操作态共用 buildDAG, VersionContext 承载操作语义差异
//
// Scaling      → prepareVersionContext + buildScalingDAG + ExecuteDAG + syncNodeStatus
//    ← 结构变体: binary 过滤 + drain inline 节点
//
// Running      → 无匹配 case，直接返回 nil                   稳态，无操作
// Pending      → 无匹配 case，直接返回 nil                   等待 desiredVersion 设置
// Failed       → 无匹配 case，直接返回 nil                   等待人工介入
//
// 设计说明: Running/Pending/Failed 是非操作态，Execute 中无对应 case，
// 直接返回 nil。这保证了:
// - 幂等性: 稳态集群 Reconcile 时跳过 DAG 构建
// - 故障隔离: Failed 集群不自动重试
// - 等待触发: Pending 集群等待用户设置 desiredVersion
// - 统一 DAG: install/upgrade/rollback 共用同一 buildDAG, 消除 80% 重复代码
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

**设计思路 — 统一 DAG 构建，操作语义由 VersionContext 承载**：

ReleaseImage 中 `spec.install.components` 和 `spec.upgrade.components` 虽是两个独立列表，但二者构建的 DAG **拓扑结构一致**（组件依赖关系不变），区别仅在于 VersionContext 的 Current/Target 版本。采用统一 DAG 构建有以下理由：

1. **DAG 拓扑与操作类型解耦**：install/upgrade/rollback 的组件依赖关系相同（etcd→apiserver→kubelet 等顺序不变），DAG 拓扑结构一致。区别仅在于 VersionContext——Install 时 Current 为空（全部安装），Upgrade 时 Current 有值（`NeedsUpgrade` 过滤），Rollback 时 Current/Target 交换。构建 4 个结构相同的 DAG 是冗余的，统一 `buildDAG` 消除重复。
2. **复用 §4.3 延迟分发哲学**：§4.3 确立了 ComponentNode 类型无关、类型分发延迟到执行期的原则。同理，操作类型（Install/Upgrade）也应延迟到执行期由 VersionContext 决定，而非在 DAG 构建期固化。`buildDAG` 只负责构建拓扑结构，不感知操作类型；`prepareVersionContext` 根据 L1 Phase 设置 VersionContext，Scheduler 执行时通过 `NeedsUpgrade` 逐组件判断 Install/Upgrade/Skip。
3. **VersionContext 承载操作语义**：`prepareVersionContext` 根据 L1 Phase 设置 Current/Target：Installing→Current 为空，Upgrading→Current=currentVersion，RollingBack→PrepareRollback() 交换 Current/Target。VersionContext 是操作语义的唯一载体，DAG 构建与操作语义彻底解耦。
4. **扩缩容为结构变体而非操作变体**：扩缩容 DAG 在统一 DAG 基础上**过滤出 binary 组件**并**追加 drain inline 节点**，这是 DAG 结构的变体（组件集合不同），而非操作语义的差异（仍然是 Install/Uninstall）。保留独立的 `buildScalingDAG` 处理结构差异，但 VersionContext 仍由 `prepareVersionContext` 统一设置。

**统一 DAG 与旧设计 (拆分 4 类) 的对比**：

| 维度 | 旧设计 (拆分 4 类) | 新设计 (统一 DAG) |
|------|-------------------|-------------------|
| **DAG 构建方法** | buildInstallDAG / buildUpgradeDAG / buildScalingDAG / buildRollbackDAG (4 个) | buildDAG (1 个) + buildScalingDAG (1 个结构变体) |
| **操作语义载体** | DAG 构建方法选择 (buildInstallDAG=Install, buildUpgradeDAG=Upgrade) | VersionContext (Current/Target) |
| **DAG 拓扑** | 4 个结构相同 (除 Scaling 过滤) | 1 个统一拓扑 |
| **代码复用** | 4 个方法 80% 逻辑重复 | 统一 buildDAG 消除重复 |
| **回滚处理** | 独立 buildRollbackDAG + PrepareRollback | 统一 buildDAG + prepareVersionContext 中 PrepareRollback |
| **扩缩容处理** | 独立 buildScalingDAG | buildScalingDAG (结构变体, 复用 buildDAG 逻辑) |
| **操作分发时机** | DAG 构建期 (选哪个 builder) | 执行期 (VersionContext.NeedsUpgrade) |

#### 4.4.4 DAG 构建实现

```go
// ──────────────────────────────────────────────────────────────
// prepareVersionContext 根据 L1 Phase 设置 VersionContext 操作语义
// 统一 DAG 的操作语义由此方法承载, buildDAG 不感知操作类型
// ──────────────────────────────────────────────────────────────
func (sm *ClusterStateMachine) prepareVersionContext(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    phase ClusterLifecyclePhase,
) error {
    switch phase {
    case ClusterPhaseInstalling:
        // 首次安装: Current 为空, 全部组件视为 Install
        // NeedsUpgrade 判断: Current 为空 → 所有组件需要安装
        sm.versionContext.SetCurrent("")
        sm.versionContext.SetTarget(cluster.Spec.DesiredVersion)

    case ClusterPhaseUpgrading:
        // 版本升级: Current=currentVersion, Target=desiredVersion
        // NeedsUpgrade 判断: Current != Target 的组件需要升级
        sm.versionContext.SetCurrent(cluster.Status.CurrentVersion)
        sm.versionContext.SetTarget(cluster.Spec.DesiredVersion)

    case ClusterPhaseRollingBack:
        // 回滚: 交换 Current/Target
        // Current = 升级后版本 (DesiredVersion), Target = 升级前版本 (PreviousVersion)
        // PrepareRollback 后 NeedsUpgrade 判断 "从升级后版本回退到升级前版本"
        sm.versionContext.SetCurrent(cluster.Spec.DesiredVersion)
        sm.versionContext.SetTarget(cluster.Status.PreviousVersion)
        sm.versionContext.PrepareRollback()

    case ClusterPhaseScaling:
        // 扩缩容: Current=currentVersion, Target=currentVersion (版本不变)
        // 新节点 Current 为空 (per-node 判断) → Install 语义
        // 删除节点 → Uninstall 语义 (由 buildScalingDAG 的 drain 节点触发)
        sm.versionContext.SetCurrent(cluster.Status.CurrentVersion)
        sm.versionContext.SetTarget(cluster.Status.CurrentVersion)
    }
    return nil
}

// ──────────────────────────────────────────────────────────────
// buildDAG 统一构建 DAG (install/upgrade/rollback 共用)
// DAG 拓扑与操作类型解耦, 操作语义由 VersionContext 承载 (见 prepareVersionContext)
// ──────────────────────────────────────────────────────────────
func (sm *ClusterStateMachine) buildDAG(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*topology.UpgradeDAG, error) {
    // 1. 解析 ReleaseImage (rollback 时解析 PreviousVersion, 其他场景解析 DesiredVersion)
    releaseImage, err := sm.resolveReleaseImage(ctx, cluster)
    if err != nil {
        return nil, fmt.Errorf("resolve release image: %w", err)
    }

    // 2. 获取组件列表 (统一使用 upgrade.components, 无 upgrade 声明时回退 install)
    //    install/upgrade/rollback 共用同一组件列表, DAG 拓扑一致
    components := releaseImage.Spec.Upgrade.Components
    if components == nil {
        // 兼容无 upgrade 声明的 ReleaseImage (首次安装场景)
        components = releaseImage.Spec.Install.Components
    }

    // 3. 展开 composite 组件 (KEP-15)
    //    composite 自身不产生 DAG 节点, 展开为子组件
    //    deferredSubComponents 通过 ExecutionContext 传递给编排器
    expandedComponents := expandCompositeComponents(components)

    // 4. 构建 DAG (复用 topology.BuildUpgradeDAG)
    //    DAG 结构不感知操作类型, 操作语义由 VersionContext 在执行期决定
    resolve := sm.makeDependencyResolver(ctx)
    return topology.BuildUpgradeDAG(expandedComponents, resolve)
}

// ──────────────────────────────────────────────────────────────
// buildScalingDAG 构建扩缩容 DAG (统一 DAG 的结构变体)
// 在 buildDAG 基础上: 过滤 binary 组件 + 追加 drain inline 节点
// ──────────────────────────────────────────────────────────────
func (sm *ClusterStateMachine) buildScalingDAG(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*topology.UpgradeDAG, error) {
    // 1. 解析 ReleaseImage (扩缩容使用当前版本, 非 DesiredVersion)
    releaseImage, err := sm.releaseImageResolver.Resolve(ctx, cluster)
    if err != nil {
        return nil, fmt.Errorf("resolve release image: %w", err)
    }

    // 2. 获取组件列表 (已安装集群用 upgrade, 首次安装用 install)
    components := releaseImage.Spec.Install.Components
    if cluster.Status.CurrentVersion != "" {
        components = releaseImage.Spec.Upgrade.Components
    }

    // 3. 展开 composite
    expandedComponents := expandCompositeComponents(components)

    // 4. 仅保留 binary 类型组件 (扩缩容仅涉及节点级组件)
    //    集群级组件 (yaml/helm/staticpod) 不在扩缩容时重新执行
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

    // 5. 构建 DAG (仅含 binary 组件)
    resolve := sm.makeDependencyResolver(ctx)
    dag, err := topology.BuildUpgradeDAG(nodeComponents, resolve)
    if err != nil {
        return nil, err
    }

    // 6. 缩容场景: 追加 drain inline 节点
    //    drain 必须在组件卸载之前完成 (通过依赖边保证)
    nodes, _ := sm.nodeProvider.GetNodes(ctx, cluster)
    for _, node := range nodes {
        if node.Spec.Deleted {
            drainNode := &topology.ComponentNode{
                Name: fmt.Sprintf("drain-%s", node.Name),
                Inline: &topology.InlineRef{
                    Handler: "DrainNode",
                    Version: "v1",
                },
                FailurePolicy: topology.FailurePolicyFailFast,
            }
            dag.AddNode(drainNode)
            // drain → 组件卸载 (依赖边保证 drain 先完成)
            for _, comp := range nodeComponents {
                dag.AddDependency(drainNode.Name, comp.Name)
            }
        }
    }

    return dag, nil
}

// ──────────────────────────────────────────────────────────────
// resolveReleaseImage 解析 ReleaseImage
// rollback 时解析 PreviousVersion 的 ReleaseImage, 其他场景解析 DesiredVersion
// ──────────────────────────────────────────────────────────────
func (sm *ClusterStateMachine) resolveReleaseImage(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*cvv1alpha1.ReleaseImage, error) {
    if cluster.Status.LifecyclePhase == ClusterPhaseRollingBack {
        // 回滚: 解析 PreviousVersion 的 ReleaseImage
        return sm.releaseImageResolver.ResolveByVersion(ctx, cluster.Status.PreviousVersion)
    }
    // 其他: 解析 DesiredVersion 的 ReleaseImage
    return sm.releaseImageResolver.Resolve(ctx, cluster)
}

// ──────────────────────────────────────────────────────────────
// 公共辅助函数
// ──────────────────────────────────────────────────────────────

// makeDependencyResolver 创建依赖解析器 (与原实现一致)
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

// buildExecutionContext 构建执行上下文 (与原实现一致)
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

**统一 DAG 构建对比总结**：

| 维度 | buildDAG (统一) | buildScalingDAG (结构变体) |
|------|----------------|--------------------------|
| **适用操作** | Install / Upgrade / Rollback | 扩容 / 缩容 |
| **组件来源** | `upgrade.components` (回退 `install`) | 当前版本 binary 组件 |
| **composite 展开** | 全部子组件 | 仅 binary 子组件 |
| **组件过滤** | 无 | 仅 binary 类型 |
| **drain 节点** | 无 | 缩容时追加 drain inline 节点 |
| **DAG 拓扑** | 完整 (全部组件) | 子集 (节点级) |
| **操作语义** | VersionContext 承载 (prepareVersionContext) | VersionContext 承载 (prepareVersionContext) |
| **回滚处理** | resolveReleaseImage 解析 PreviousVersion | 不适用 |
| **幂等机制** | `NeedsUpgrade` 过滤 (VersionContext) | 新节点 Current 为空 → Install |

**prepareVersionContext 操作语义映射**：

| L1 Phase | Current | Target | NeedsUpgrade 语义 | 效果 |
|----------|---------|--------|------------------|------|
| Installing | `""` (空) | desiredVersion | Current 为空 → 所有组件 Install | 全量安装 |
| Upgrading | currentVersion | desiredVersion | Current != Target → 变更组件 Upgrade | 增量升级 |
| RollingBack | desiredVersion | previousVersion | PrepareRollback 交换后 → 降级 | 版本回退 |
| Scaling | currentVersion | currentVersion | 版本一致 → 已有节点跳过, 新节点 Install | 扩容安装/缩容卸载 |

### 4.5 节点层状态机执行

**设计思路 — 节点层状态机的职责边界与组件来源**：

1. **L2 评估状态、L3 执行操作**：`NodeStateMachine.Execute` 的核心职责是评估节点状态转换（`evaluateNodePhase`）并决定执行何种操作（Install/Upgrade/Uninstall），实际的组件安装/升级委托给 `ComponentStateMachine.Execute`。节点层不直接操作 Executor，通过组件层间接调用，保持三层职责分离。
2. **节点组件从 ReleaseImage 解析而非硬编码**：`components` 参数由 BKEMachine Controller 从 ReleaseImage 解析后传入（§6.4 详述），展开 composite 后过滤出 binary 类型组件。节点组件列表按依赖关系拓扑排序后逐个执行，排序算法复用代码库已有的 `topologicalSort`。
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

**设计思路 — 双 Controller 分工协作，各驱动一层状态机**：

1. **BKECluster Controller 驱动 L1，BKEMachine Controller 驱动 L2/L3**：集群级操作（安装/升级/回滚的 DAG 调度）由 BKECluster Reconciler 触发 `ClusterStateMachine.Execute`；节点级操作（单节点组件安装/升级/卸载）由 BKEMachine Reconciler 触发 `NodeStateMachine.Execute`。两个 Controller 各自独立 Reconcile，通过 Watch BKECluster/BKEMachine CR 变化协调，无需直接调用。
2. **StateMachineEngine 共享实例**：两个 Controller 共享同一个 `StateMachineEngine` 实例（包含 `ClusterStateMachine`/`NodeStateMachine`/`ComponentStateMachine`），但调用不同的入口方法。引擎实例无状态（状态存储在 CR Status 中），共享实例不产生并发安全问题。
3. **Watch 而非直接调用**：BKEMachine Controller 不通过 RPC 调用 BKECluster Controller，而是通过 Watch BKEMachine CR 的状态变化间接感知集群操作。BKECluster Controller 执行 DAG 时更新 BKEMachine 的 `NodeComponents` 字段，BKEMachine Controller Watch 到变化后触发节点层状态机执行。这种松耦合设计是 CAPI 的标准模式。
4. **CAPI 标准兼容**：BKECluster/BKEMachine 作为 CAPI Infrastructure Provider 的自定义资源，遵循 CAPI 的 Reconcile + Conditions + Patch 模式。状态机引擎的输出通过 CAPI 标准字段（`ReadyCondition`/`InfrastructureReadyCondition`）暴露给上层 CAPI Controller。

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

### 6.2 集群层触发节点层状态机的协调机制

**设计思路 — DAG 内联为默认路径，BKEMachine Watch 触发为可选路径**：

集群层状态机（L1）默认通过 `Scheduler.ExecuteDAG` 统一调度所有操作场景（安装/升级/扩缩容），由 `syncNodeStatus` 回写节点状态。同时保留 BKEMachine Watch 触发路径作为可选方案——适用于需要节点独立 Reconcile 的场景（如节点分布在多可用区、网络分区容忍、大规模集群卸载 L1 调度压力）。

**两条触发路径**：

| 路径 | 触发场景 | 触发方式 | L2 是否参与 | 适用条件 |
|------|---------|---------|------------|---------|
| **DAG 内联执行（默认）** | 集群安装、版本升级、节点扩缩容 | Scheduler 通过 `BinaryComponentExecutor` 在 DAG 执行中直接 SSH 操作节点 | 不参与 | 默认路径 |
| **BKEMachine Watch 触发（可选）** | 节点扩容、节点缩容 | L1 更新 BKEMachine.Status.NodePhase → BKEMachine Controller Watch → L2 Execute | 参与 | 需要节点独立 Reconcile 的场景 |

**为什么扩缩容默认走 DAG 内联**：

1. **架构统一性**：安装/升级/扩缩容走同一执行路径（`Scheduler.ExecuteDAG` + `syncNodeStatus`），减少分支逻辑和维护成本。`BinaryComponentExecutor` 已支持节点级并发策略（Rolling/Parallel/Batch），扩容场景使用 Parallel（新节点全并行安装）或 Batch 即可。
2. **节点状态一致性**：DAG 内联路径的 `syncNodeStatus` 统一聚合和回写节点状态。如果扩缩容走 Watch 触发，两种路径的状态回写逻辑不同（DAG 内联由 `syncNodeStatus` 聚合，Watch 触发由 `evaluateNodePhase` 实时聚合），增加维护负担和潜在不一致风险。
3. **扩容的依赖顺序同样需要保证**：新节点安装时，组件间有依赖顺序（bkeagent → containerd → kubelet），DAG 拓扑排序保证顺序。虽然 Watch 触发下 `NodeStateMachine.executeNodeComponents` 也有 `topologicalSort`，但那是节点内排序，无法保证跨节点顺序（如 etcd 节点需先于 master 节点就绪）。DAG 内联可以统一处理跨节点和节点内的依赖。
4. **缩容的安全性**：节点缩容时需要先 drain（驱逐 Pod）再卸载组件。drain 操作可封装为 DAG 节点（inline 类型），与组件卸载节点按依赖顺序执行。Watch 触发下各节点独立 Reconcile，无法协调 drain 与组件卸载的顺序。

**Watch 触发路径的适用场景**：

1. **大规模集群**：数千节点集群中，L1 的 DAG 执行串行化所有节点操作可能成为瓶颈。Watch 触发下各 BKEMachine 并行 Reconcile，卸载 L1 调度压力。
2. **多可用区/多地域**：节点分布在不同可用区时，各区域的网络延迟不一致，Watch 触发允许各区域节点独立 Reconcile，不受 L1 调度的全局批次等待。
3. **网络分区容忍**：L1 Controller 与部分节点网络分区时，Watch 触发下已连通的节点仍可独立完成组件安装（L2 不依赖 L1 的 DAG 执行）。
4. **灰度迁移**：从旧架构（Phase 框架的 BKEMachine Controller 驱动）迁移到新架构时，可先启用 Watch 触发路径（行为接近旧架构），再切换到 DAG 内联。

#### 6.2.1 路径 1：DAG 内联执行（集群安装/升级）

**设计思路 — 安装/升级时 L1 Scheduler 直接操作 L3，不经过 L2，但需回写节点状态**：

集群安装和升级时，L1 通过 `Scheduler.ExecuteDAG` 统一调度所有组件。binary 组件作为 DAG 中的独立 `ComponentNode`，由 `BinaryComponentExecutor` 直接在节点上 SSH 执行。此路径不经过 L2 `NodeStateMachine`——L1 的 Scheduler 直接操作 L3 Executor。

但这引入一个问题：**L2 节点状态（`BKEMachine.Status.NodePhase`、`NodeComponentStatuses`）由谁更新？** 路径 2 中 L2 的 `evaluateNodePhase` 负责聚合组件状态并更新节点状态，路径 1 跳过了 L2，如果不回写节点状态，BKEMachine CR 将缺少节点级进度数据，导致 `kubectl get bkenode` 无法展示组件安装/升级进度，CAPI 上层 Controller 也无法通过 `BKEMachine.Status` 判断节点是否就绪。

**解决方案 — BinaryComponentExecutor 在执行组件操作时同步回写节点状态，syncNodeStatus 在 DAG 执行后聚合并直接写入 BKEMachine CR**：

`BinaryComponentExecutor` 内部已有 `NodeStatusUpdater` 和 `ComponentStatusUpdater` 接口（§4.2 Scheduler 注入），在执行 binary 组件的每个节点操作前后更新 per-node per-component 状态。路径 1 复用此机制回写节点状态，无需 L2 参与：

1. **组件级状态回写**：`BinaryComponentExecutor` 在每个节点上执行组件前调用 `NodeStatusUpdater.MarkPending`，执行成功后调用 `MarkSuccess`，失败时调用 `MarkFailed`。这些状态写入 `BKECluster.Status.NodeComponentStatuses[componentName][nodeIP]`。
2. **节点级状态聚合**：DAG 执行完成后（或每个 binary 组件批次完成后），由 `ClusterStateMachine` 调用 `syncNodeStatus` 将 `NodeComponentStatuses` 聚合为 `BKEMachine.Status.NodePhase`，写入对应的 BKEMachine CR。聚合逻辑复用 `evaluateNodePhase` 的规则——全部 Installed → Ready，任一 Installing → Provisioning，任一 Failed → Failed。
3. **CAPI Conditions 由 syncNodeStatus 直接写入**：`syncNodeStatus` 在 Patch BKEMachine CR 时同步设置 CAPI 标准 Conditions（`ReadyCondition`）。路径 1 中 BKEMachine Controller **不参与**——不 Watch NodePhase、不执行 L2、不更新 Conditions。CAPI Conditions 的更新由 `syncNodeStatus` 直接完成，与 BKECluster Controller 的 `setCAPIConditions` 对称（集群级 Conditions 由 BKECluster Controller 写，节点级 Conditions 由 `syncNodeStatus` 写）。

**为什么 BKEMachine Controller 在路径 1 中不需要 Watch NodePhase**：

1. **Watch 是路径 2 的触发机制，不是路径 1 的**：路径 2（扩缩容）中 L1 通过更新 `NodePhase` 触发 BKEMachine Controller Reconcile 执行 L2。路径 1（安装/升级）中 L1 已经通过 DAG 直接完成了组件执行和状态回写，不需要再触发 BKEMachine Controller 做任何事。
2. **CAPI Conditions 不需要 BKEMachine Controller 介入**：`syncNodeStatus` 在 Patch BKEMachine CR 时直接写入 `ReadyCondition`，CAPI Cluster Controller 和 MachineDeployment Controller Watch 到 Conditions 变化即可判断节点就绪状态。BKEMachine Controller 的 Reconcile 在路径 1 中是空操作——`evaluateNodePhase` 读取已更新的 `NodeComponentStatuses` 后返回 Ready，Execute 无匹配 case 直接返回 nil。
3. **避免循环触发**：如果 BKEMachine Controller Watch 到 `NodePhase` 变化后执行 L2，L2 又会写 `NodePhase`（通过 `evaluateNodePhase` 聚合），可能触发新的 Reconcile。虽然幂等保证不会重复执行，但会产生不必要的 Reconcile 开销。路径 1 中 `syncNodeStatus` 直接写入最终状态，BKEMachine Controller Reconcile 时发现状态已达成，直接跳过。

**为什么安装/升级不走 Watch 触发而走 DAG 内联**：

1. **依赖顺序保证**：安装/升级需要严格的跨组件依赖顺序（如 etcd → apiserver → kubelet），DAG 拓扑排序保证依赖在前。Watch 触发是各节点独立 Reconcile，无法保证跨节点、跨组件的依赖顺序。
2. **节点级并发策略**：binary 组件升级需要 Rolling/Batch 策略（逐节点/分批），`BinaryComponentExecutor` 内部控制并发。Watch 触发下各 BKEMachine 独立 Reconcile，无法协调多节点间的并发节奏。
3. **集群级组件与节点级组件的协调**：安装/升级时集群级组件（etcd/apiserver）和节点级组件（containerd/kubelet）需按统一 DAG 顺序执行。Watch 触发仅处理节点级组件，无法包含集群级组件。

**执行流程（含节点状态回写）**：

```
BKECluster Reconcile
  └─ ClusterStateMachine.Execute
       └─ evaluateClusterPhase → Installing/Upgrading
       └─ prepareVersionContext + buildDAG (统一 DAG)
       └─ scheduler.ExecuteDAG
            └─ Batch N: [bkeagent, containerd, kubelet]
                 └─ BinaryComponentExecutor.ExecuteComponent
                      ├─ NodeStatusUpdater.MarkPending(nodeIP, "bkeagent")   ← 回写组件状态
                      ├─ SSH 逐节点执行 (Rolling/Batch)
                      │    └─ 直接调用 ComponentExecutor (L3)
                      │         ← 不经过 NodeStateMachine (L2)
                      ├─ NodeStatusUpdater.MarkSuccess(nodeIP, "bkeagent")    ← 回写组件状态
                      └─ ComponentStatusUpdater.MarkSuccess("bkeagent")       ← 回写组件级状态
       └─ syncNodeStatus(ctx, cluster)                                        ← DAG 执行后聚合节点状态
            └─ 遍历所有 BKENode:
                 ├─ evaluateNodePhase(node, components)                       ← 复用 L2 聚合逻辑
                 ├─ BKEMachine.Status.NodePhase = 聚合结果                    ← 回写节点状态
                 ├─ setCAPIConditions(machine, nodePhase)                     ← 直接写入 CAPI Conditions
                 └─ Patch BKEMachine CR
                      ← BKEMachine Controller 不参与路径 1
```

**节点状态回写实现**：

```go
// syncNodeStatus 在 DAG 执行完成后将组件状态聚合为节点状态并回写 BKEMachine CR
// 此方法由 ClusterStateMachine.Execute 在 DAG 执行后调用
// 解决路径 1 中 L2 不参与执行导致节点状态缺失的问题
// 同时直接写入 CAPI Conditions，无需 BKEMachine Controller Watch
func (sm *ClusterStateMachine) syncNodeStatus(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) error {
    // 1. 获取所有节点
    nodes, err := sm.nodeProvider.GetNodes(ctx, cluster)
    if err != nil {
        return fmt.Errorf("get nodes for sync: %w", err)
    }

    // 2. 获取节点级组件列表（与 DAG 构建时一致）
    releaseImage, err := sm.releaseImageResolver.Resolve(ctx, cluster)
    if err != nil {
        return fmt.Errorf("resolve release image for sync: %w", err)
    }
    expandedComponents := expandCompositeComponents(releaseImage.Spec.Upgrade.Components)
    var nodeComponents []*ComponentVersion
    for _, comp := range expandedComponents {
        cv, err := sm.cvStore.GetComponentVersion(ctx, comp.Name, comp.Version)
        if err != nil {
            continue
        }
        if cv.Spec.Type == ComponentTypeBinary {
            nodeComponents = append(nodeComponents, cv)
        }
    }

    // 3. 逐节点聚合组件状态 → 节点状态
    nodeSM := NewNodeStateMachine(nil) // 无需 componentSM，仅用 evaluateNodePhase
    for _, node := range nodes {
        // evaluateNodePhase 从 NodeComponentStatuses 读取组件状态并聚合
        newPhase := nodeSM.evaluateNodePhase(node, nodeComponents)

        // 4. 更新 BKEMachine CR（含 NodePhase + ComponentStatuses + CAPI Conditions）
        machine := &bkev1beta1.BKEMachine{}
        if err := sm.client.Get(ctx, types.NamespacedName{
            Name: node.MachineName, Namespace: cluster.Namespace,
        }, machine); err != nil {
            continue // BKEMachine 可能已被删除
        }

        if machine.Status.NodePhase != newPhase {
            patchHelper, _ := patch.NewHelper(machine, sm.client)
            machine.Status.NodePhase = newPhase
            // 同步组件级状态到 BKEMachine（供 kubectl 查询）
            machine.Status.ComponentStatuses = node.Status.ComponentStatuses
            // 直接写入 CAPI 标准 Conditions（无需 BKEMachine Controller 介入）
            setMachineCAPIConditions(machine, newPhase)
            if err := patchHelper.Patch(ctx, machine); err != nil {
                sm.recorder.Eventf(machine, v1.EventTypeWarning,
                    "NodeStatusSyncFailed", "failed to sync node phase: %v", err)
            }
        }
    }

    return nil
}

// setMachineCAPIConditions 将节点状态映射为 CAPI 标准 Conditions
// 与 §6.5 的 setCAPIConditions 对称——集群级 Conditions 由 BKECluster Controller 写，
// 节点级 Conditions 由 syncNodeStatus 直接写
func setMachineCAPIConditions(machine *bkev1beta1.BKEMachine, phase NodeLifecyclePhase) {
    switch phase {
    case NodePhaseReady:
        conditions.MarkTrue(machine, clusterv1.ReadyCondition)

    case NodePhaseProvisioning, NodePhaseUpgrading, NodePhaseDeleting:
        conditions.MarkFalse(machine, clusterv1.ReadyCondition,
            "OperationInProgress", clusterv1.ConditionSeverityInfo,
            "Node %s in progress", phase)

    case NodePhaseFailed:
        conditions.MarkFalse(machine, clusterv1.ReadyCondition,
            "NodeFailed", clusterv1.ConditionSeverityError,
            "Node operation failed, manual intervention required")
    }
}
```

**ClusterStateMachine.Execute 中的调用点**：

```go
// ClusterStateMachine.Execute 中统一 DAG 分支 (Installing/Upgrading/RollingBack 共用)
// syncNodeStatus 已内嵌在 Execute 中, 见 §4.4
case ClusterPhaseInstalling, ClusterPhaseUpgrading, ClusterPhaseRollingBack:
    dag, err := sm.buildDAG(ctx, cluster)
    if err != nil {
        return fmt.Errorf("build DAG failed: %w", err)
    }
    execCtx := sm.buildExecutionContext(ctx, cluster)
    if err := sm.scheduler.ExecuteDAG(ctx, execCtx, dag); err != nil {
        // 即使 DAG 执行失败也尝试同步节点状态（部分组件可能已成功）
        _ = sm.syncNodeStatus(ctx, cluster)
        return fmt.Errorf("execute DAG failed: %w", err)
    }
    // DAG 执行成功后同步节点状态
    if err := sm.syncNodeStatus(ctx, cluster); err != nil {
        sm.recorder.Eventf(cluster, v1.EventTypeWarning,
            "NodeStatusSyncFailed", "failed to sync node status: %v", err)
    }
    cluster.Status.CurrentVersion = cluster.Spec.DesiredVersion
```

**节点状态回写的数据流**：

```
DAG 执行过程中:
  BinaryComponentExecutor
    ├─ NodeStatusUpdater.MarkPending  → BKECluster.Status.NodeComponentStatuses[comp][nodeIP]
    ├─ SSH 执行组件操作 (L3)
    └─ NodeStatusUpdater.MarkSuccess  → BKECluster.Status.NodeComponentStatuses[comp][nodeIP]

DAG 执行完成后:
  syncNodeStatus (BKECluster Controller 内)
    ├─ 读取 BKECluster.Status.NodeComponentStatuses
    ├─ evaluateNodePhase 聚合 → NodePhase (Ready/Provisioning/Failed)
    ├─ BKEMachine.Status.NodePhase = 聚合结果
    ├─ BKEMachine.Status.ComponentStatuses = per-component 状态
    ├─ setMachineCAPIConditions(machine, nodePhase) → ReadyCondition
    └─ Patch BKEMachine CR
         ← BKEMachine Controller 不参与路径 1

CAPI 上层 Controller:
    └─ Watch BKEMachine.Status.Conditions.ReadyCondition 变化
         └─ MachineDeployment Controller 据此判断节点就绪状态
```

**与路径 2 的状态更新对比**：

| 维度 | 路径 1 (DAG 内联) | 路径 2 (Watch 触发) |
|------|-------------------|-------------------|
| **组件状态写入者** | BinaryComponentExecutor 内部的 NodeStatusUpdater | NodeStateMachine.executeNodeComponents → ComponentStateMachine |
| **节点状态聚合者** | syncNodeStatus（DAG 执行后，BKECluster Controller 内） | evaluateNodePhase（BKEMachine Controller Reconcile 内） |
| **聚合时机** | DAG 全部完成后一次性聚合 | 每次 BKEMachine Reconcile 实时聚合 |
| **BKEMachine CR 更新者** | syncNodeStatus 直接 Patch | NodeStateMachine.Execute 内部更新后 BKEMachine Reconcile Patch |
| **CAPI Conditions 更新者** | syncNodeStatus 内的 setMachineCAPIConditions 直接写入 | BKEMachine Reconcile 内的 setMachineCAPIConditions 写入 |
| **BKEMachine Controller 是否参与** | 不参与（空操作 Reconcile，状态已达成直接跳过） | 参与（执行 L2 NodeStateMachine.Execute） |
| **中间状态可见性** | DAG 执行期间 NodeComponentStatuses 有值，NodePhase 待 DAG 完成后才聚合 | 每次组件执行后实时聚合，NodePhase 实时更新 |

#### 6.2.2 路径 2：DAG 内联执行（节点扩缩容）

**设计思路 — 扩缩容也走 DAG 内联，与安装/升级统一执行路径**：

节点扩缩容时，L1 通过 `buildScalingDAG` 构建仅含节点级 binary 组件的 DAG，由 `Scheduler.ExecuteDAG` 直接执行。扩容场景使用 Parallel 策略（新节点全并行安装），缩容场景使用 Rolling 策略（逐节点 drain + 卸载）。与安装/升级路径相同，`syncNodeStatus` 在 DAG 执行后聚合节点状态并回写 BKEMachine CR。

**扩容与缩容的 DAG 差异**：

| 维度 | 扩容 DAG | 缩容 DAG |
|------|---------|---------|
| **组件来源** | 当前版本 ReleaseImage 的 binary 组件 | 同左 |
| **组件操作** | Install（新节点首次安装） | Uninstall（卸载已有组件） |
| **并发策略** | Parallel（新节点全并行） | Rolling（逐节点串行，确保安全驱逐） |
| **前置节点** | 无（新节点无前置依赖） | drain 节点（inline 类型，驱逐 Pod 后再卸载） |
| **VersionContext** | 新节点 Current 为空 → Install | 已有节点 Current 有值 → Uninstall |

**扩容执行流程**：

```
BKECluster Reconcile
  └─ ClusterStateMachine.Execute
       └─ evaluateClusterPhase → Scaling
       └─ buildScalingDAG(ctx, cluster)                         ← 仅 binary 组件
       └─ scheduler.ExecuteDAG
            └─ Batch 1: [bkeagent, containerd, kubelet]          ← 新节点全并行
                 └─ BinaryComponentExecutor.ExecuteComponent
                      ├─ NodeStatusUpdater.MarkPending(newNodeIP, "bkeagent")
                      ├─ SSH 执行 Install (Parallel 全节点同时)
                      ├─ NodeStatusUpdater.MarkSuccess(newNodeIP, "bkeagent")
                      └─ 新节点组件全部安装完成
       └─ syncNodeStatus(ctx, cluster)                           ← 回写新节点状态
            └─ 新节点: NodeComponentStatuses 全部 Installed
            └─ evaluateNodePhase → Ready
            └─ BKEMachine.Status.NodePhase = Ready
            └─ setMachineCAPIConditions → ReadyCondition = True
```

**缩容执行流程**：

```
BKECluster Reconcile
  └─ ClusterStateMachine.Execute
       └─ evaluateClusterPhase → Scaling
       └─ buildScalingDAG(ctx, cluster)                         ← 仅 binary 组件
       └─ scheduler.ExecuteDAG
            └─ Batch 1: [drain-node-1]                          ← inline 类型: drain 节点
                 └─ InlineComponentExecutor.ExecuteComponent
                      └─ kubectl drain node-1 (驱逐 Pod)
            └─ Batch 2: [bkeagent, containerd, kubelet]         ← Rolling 逐节点卸载
                 └─ BinaryComponentExecutor.ExecuteComponent
                      ├─ SSH 执行 Uninstall (Rolling 逐节点)
                      ├─ NodeStatusUpdater.MarkRemoved(nodeIP, "bkeagent")
                      └─ 节点-1 组件全部卸载完成
            └─ Batch 3: [drain-node-2, ...]                     ← 下一节点
                 └─ ... (重复 drain + 卸载)
       └─ syncNodeStatus(ctx, cluster)                           ← 回写删除节点状态
            └─ 删除节点: NodeComponentStatuses 已清除
            └─ evaluateNodePhase → (无组件状态) → 从 DAG 中移除
```

**扩缩容 DAG 构建实现**：

```go
// buildScalingDAG 构建扩缩容 DAG（仅节点级 binary 组件）
// 扩容: 新节点 Install (Parallel 全并行)
// 缩容: 删除节点 drain (inline) → Uninstall (Rolling 逐节点)
func (sm *ClusterStateMachine) buildScalingDAG(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*topology.UpgradeDAG, error) {
    releaseImage, err := sm.releaseImageResolver.Resolve(ctx, cluster)
    if err != nil {
        return nil, fmt.Errorf("resolve release image: %w", err)
    }

    // 1. 获取组件列表（已安装集群用 upgrade，首次安装用 install）
    components := releaseImage.Spec.Install.Components
    if cluster.Status.CurrentVersion != "" {
        components = releaseImage.Spec.Upgrade.Components
    }

    // 2. 展开 composite
    expandedComponents := expandCompositeComponents(components)

    // 3. 过滤出 binary 类型组件（扩缩容仅涉及节点级组件）
    var nodeComponents []cvv1alpha1.ReleaseImageUpgradeComponent
    for _, comp := range expandedComponents {
        cv, err := sm.cvStore.GetComponentVersion(ctx, comp.Name, comp.Version)
        if err != nil {
            return nil, fmt.Errorf("lookup component %s: %w", comp.Name, err)
        }
        if cv.Spec.Type == ComponentTypeBinary {
            // 扩容: upgradeStrategy.mode = Parallel (新节点全并行)
            // 缩容: upgradeStrategy.mode = Rolling (逐节点串行)
            nodeComponents = append(nodeComponents, comp)
        }
    }

    // 4. 构建 DAG
    resolve := sm.makeDependencyResolver(ctx)
    dag, err := topology.BuildUpgradeDAG(nodeComponents, resolve)
    if err != nil {
        return nil, err
    }

    // 5. 缩容场景: 添加 drain 前置节点
    nodes, _ := sm.nodeProvider.GetNodes(ctx, cluster)
    for _, node := range nodes {
        if node.Spec.Deleted {
            // 为每个删除节点添加 drain inline 节点
            drainNode := &topology.ComponentNode{
                Name: fmt.Sprintf("drain-%s", node.Name),
                Inline: &topology.InlineRef{
                    Handler: "DrainNode",
                    Version: "v1",
                },
                FailurePolicy: topology.FailurePolicyFailFast,
            }
            dag.AddNode(drainNode)
            // drain 必须在组件卸载之前完成
            for _, comp := range nodeComponents {
                dag.AddDependency(drainNode.Name, comp.Name)
            }
        }
    }

    return dag, nil
}
```

**Execute 中 Scaling 分支的实现**：

```go
case ClusterPhaseScaling:
    // 与安装/升级相同: 构建 DAG → 执行 DAG → 同步节点状态
    dag, err := sm.buildScalingDAG(ctx, cluster)
    if err != nil {
        return fmt.Errorf("build scaling DAG failed: %w", err)
    }
    execCtx := sm.buildExecutionContext(ctx, cluster)
    if err := sm.scheduler.ExecuteDAG(ctx, execCtx, dag); err != nil {
        _ = sm.syncNodeStatus(ctx, cluster)
        return fmt.Errorf("execute scaling DAG failed: %w", err)
    }
    // DAG 执行后同步节点状态（与路径 1 完全一致）
    if err := sm.syncNodeStatus(ctx, cluster); err != nil {
        sm.recorder.Eventf(cluster, v1.EventTypeWarning,
            "NodeStatusSyncFailed", "failed to sync node status: %v", err)
    }
```

#### 6.2.3 路径 3：BKEMachine Watch 触发（节点扩缩容，可选）

**设计思路 — 扩缩容的可选路径，L1 通过 CR Status 变化触发 L2 独立执行**：

作为 DAG 内联路径的可选替代方案，节点扩缩容可走 BKEMachine Watch 触发路径。L1 更新 BKEMachine CR 的状态字段（`NodePhase`、`NodeComponents`），BKEMachine Controller Watch 到变化后在自身 Reconcile 中调用 `NodeStateMachine.Execute` 驱动 L2/L3 执行。各节点独立 Reconcile，并行安装/卸载。

**适用场景**：

1. **大规模集群**：数千节点集群中，L1 的 DAG 执行串行化所有节点操作可能成为瓶颈。Watch 触发下各 BKEMachine 并行 Reconcile，卸载 L1 调度压力。
2. **多可用区/多地域**：节点分布在不同可用区时，各区域的网络延迟不一致，Watch 触发允许各区域节点独立 Reconcile，不受 L1 调度的全局批次等待。
3. **网络分区容忍**：L1 Controller 与部分节点网络分区时，Watch 触发下已连通的节点仍可独立完成组件安装（L2 不依赖 L1 的 DAG 执行）。
4. **灰度迁移**：从旧架构（Phase 框架的 BKEMachine Controller 驱动）迁移到新架构时，可先启用 Watch 触发路径（行为接近旧架构），再切换到 DAG 内联。

**与 DAG 内联路径的权衡**：

| 维度 | DAG 内联（默认） | Watch 触发（可选） |
|------|-------------------|-------------------|
| **跨组件依赖顺序** | DAG 拓扑排序保证 | 仅节点内 topologicalSort，无法保证跨节点 |
| **节点级并发** | BinaryComponentExecutor 控制 | 各 BKEMachine 独立 Reconcile，无全局协调 |
| **集群级+节点级协调** | 统一 DAG 包含全部组件 | 仅节点级组件，集群级组件需另外处理 |
| **缩容 drain 安全性** | drain 作为 DAG inline 节点，按依赖执行 | 各节点独立 drain，无法保证顺序 |
| **大规模集群性能** | L1 串行化可能成为瓶颈 | 各节点并行 Reconcile，性能更好 |
| **网络分区容忍** | L1 与节点分区时 DAG 执行中断 | 已连通节点可独立完成 |
| **状态回写** | syncNodeStatus 统一聚合 | evaluateNodePhase 实时聚合 |
| **中间状态可见性** | DAG 完成后才聚合 NodePhase | 每次组件执行后实时更新 |

**执行流程**：

```
BKECluster Reconcile
  └─ ClusterStateMachine.Execute
       └─ evaluateClusterPhase → Scaling
       └─ 更新 BKEMachine CR:
            - NodePhase = Provisioning (新节点) 或 Deleting (删除节点)
            - NodeComponents = [bkeagent, containerd, kubelet] (展开 composite)
       └─ Patch BKEMachine CR

         ↓ BKEMachine Controller Watch 到 Status 变化 ↓

BKEMachine Reconcile
  └─ 获取 BKEMachine + 关联 BKECluster
  └─ getNodeComponents (从 ReleaseImage 解析)
  └─ NodeStateMachine.Execute (L2)
       └─ evaluateNodePhase → Provisioning / Deleting
       └─ executeNodeComponents
            └─ ComponentStateMachine.Execute (L3)
                 └─ ComponentExecutor.Install / Uninstall
```

**触发机制实现**：

```go
// ClusterStateMachine.Execute 中 Scaling 分支的 Watch 触发逻辑（可选路径）
// 通过 Feature Gate 控制是否启用 Watch 触发路径
case ClusterPhaseScaling:
    if sm.scalingWatchTriggerEnabled(cluster) {
        // 可选路径: Watch 触发
        nodes, err := sm.nodeProvider.GetNodes(ctx, cluster)
        if err != nil {
            return fmt.Errorf("get nodes failed: %w", err)
        }
        for _, node := range nodes {
            machine := &bkev1beta1.BKEMachine{}
            if err := sm.client.Get(ctx, types.NamespacedName{
                Name: node.MachineName, Namespace: cluster.Namespace,
            }, machine); err != nil {
                continue
            }
            patchHelper, _ := patch.NewHelper(machine, sm.client)
            if node.Status.LifecyclePhase == NodePhasePending {
                machine.Status.NodePhase = NodePhaseProvisioning
                machine.Status.NodeComponents = toNodeComponentRefs(dag)
            }
            if node.Spec.Deleted {
                machine.Status.NodePhase = NodePhaseDeleting
            }
            patchHelper.Patch(ctx, machine)
        }
    } else {
        // 默认路径: DAG 内联执行（见 §6.2.2）
        dag, err := sm.buildScalingDAG(ctx, cluster)
        // ... scheduler.ExecuteDAG + syncNodeStatus ...
    }
```

**Feature Gate 控制**：

```go
// scalingWatchTriggerEnabled 判断是否启用 Watch 触发路径
// 通过集群注解或全局 Feature Gate 控制
func (sm *ClusterStateMachine) scalingWatchTriggerEnabled(
    cluster *bkev1beta1.BKECluster,
) bool {
    // 注解优先: 集群级开关
    if annotations.Has(cluster, "cvo.openfuyao.cn/scaling-watch-trigger") {
        return annotations.Get(cluster, "cvo.openfuyao.cn/scaling-watch-trigger") == "true"
    }
    // 全局 Feature Gate
    return config.ScalingWatchTriggerEnabled
}
```

#### 6.2.4 场景对比

**DAG 内联路径各场景的差异**：

| 维度 | 安装 (Installing) | 升级 (Upgrading) | 扩容 (Scaling) | 缩容 (Scaling) |
|------|-------------------|-----------------|---------------|---------------|
| **DAG 构建** | `buildDAG` (统一) | `buildDAG` (统一) | `buildScalingDAG` (变体) | `buildScalingDAG` (变体) |
| **组件来源** | `upgrade.components` (回退 `install`) | `upgrade.components` | 当前版本 binary 组件 | 当前版本 binary 组件 |
| **组件操作** | Install | Upgrade | Install（新节点） | Uninstall（删除节点） |
| **并发策略** | Rolling/Parallel | Rolling/Batch | Parallel（全并行） | Rolling（逐节点 drain+卸载） |
| **前置节点** | 无 | 无 | 无 | drain inline 节点 |
| **VersionContext** | Current 为空 | Current 有值 | 新节点 Current 为空 | 删除节点 Current 有值 |
| **状态回写** | syncNodeStatus | syncNodeStatus | syncNodeStatus | syncNodeStatus |
| **CAPI Conditions** | setMachineCAPIConditions | setMachineCAPIConditions | setMachineCAPIConditions | 清除后移除 |
| **BKEMachine Controller** | 不参与 | 不参与 | 不参与 | 不参与 |

**DAG 内联与 Watch 触发路径的对比**：

| 维度 | DAG 内联（默认，路径 1/2） | Watch 触发（可选，路径 3） |
|------|---------------------------|--------------------------|
| **触发场景** | 安装/升级/扩缩容 | 仅扩缩容 |
| **L1 操作** | `scheduler.ExecuteDAG` 直接执行 | 更新 BKEMachine CR Status |
| **L2 是否参与** | 不参与 | 参与（L2 评估状态后驱动 L3） |
| **组件来源** | DAG 中的 ComponentNode | BKEMachine.Status.NodeComponents |
| **并发控制** | Scheduler 批次内并行 | 每个 BKEMachine 独立 Reconcile |
| **幂等保证** | VersionContext.NeedsUpgrade | evaluateNodePhase + evaluateComponentPhase |
| **状态回写** | syncNodeStatus 统一聚合 | evaluateNodePhase 实时聚合 |
| **大规模集群** | L1 串行化可能瓶颈 | 各节点并行，性能更好 |
| **网络分区容忍** | L1 与节点分区时中断 | 已连通节点可独立完成 |
| **Feature Gate** | 默认启用 | `ScalingWatchTriggerEnabled` 控制 |

**DAG 内联路径的优势**：

| 优势 | 说明 |
|------|------|
| **单一执行路径** | 所有场景走 `Scheduler.ExecuteDAG` + `syncNodeStatus`，无需维护 Watch 触发分支 |
| **状态回写统一** | 所有场景的节点状态由 `syncNodeStatus` 统一聚合和回写，避免两种路径的状态不一致 |
| **依赖顺序保证** | 扩容也能保证跨节点、跨组件的依赖顺序（如 etcd 节点先于 master 节点就绪） |
| **缩容安全性** | drain 操作作为 DAG inline 节点，与组件卸载按依赖顺序执行，保证 Pod 驱逐完成后才卸载 |
| **并发控制统一** | `BinaryComponentExecutor` 的 upgradeStrategy.mode 统一控制所有场景的节点级并发 |

#### 6.2.5 NodeStateMachine 的保留场景

`NodeStateMachine` 在 DAG 内联路径中不参与主执行，但在以下场景中保留：

| 场景 | 触发方式 | NodeStateMachine 职责 | 说明 |
|------|---------|----------------------|------|
| **Watch 触发扩缩容** | BKEMachine Controller Watch NodePhase | L2 驱动 L3 执行组件安装/卸载 | 可选路径（§6.2.3），通过 Feature Gate 启用 |
| **单节点故障恢复** | BKEMachine Controller Reconcile | `evaluateNodePhase` 判断节点状态 | 节点重启后组件状态检查，判断是否需要重新安装 |
| **手动重试失败节点** | 人工清除 Failed 状态 | `evaluateNodePhase` → Provisioning | 人工介入后重新触发组件安装 |
| **状态查询** | BKEMachine Controller Reconcile | `evaluateNodePhase` 聚合只读 | 不执行操作，仅聚合组件状态供查询（DAG 内联已写入 NodeComponentStatuses） |

> **设计说明**：DAG 内联为默认路径时，`NodeStateMachine` 降级为"单节点状态查询与故障恢复"辅助角色。启用 Watch 触发路径（Feature Gate `ScalingWatchTriggerEnabled`）后，`NodeStateMachine` 恢复为扩缩容的执行入口。两种路径通过 Feature Gate 切换，不会同时执行。

### 6.3 BKECluster Controller 集成

**设计思路 — Reconcile 作为状态机的唯一触发入口**：

1. **PatchHelper 保证 Status 一致性**：Reconcile 使用 `patch.NewHelper` 创建 PatchHelper，在函数退出时 defer Patch。状态机引擎执行过程中对 `cluster.Status` 的修改（如 `LifecyclePhase` 转换、`CurrentVersion` 更新）在 defer Patch 时一次性写入，避免多次 API 调用和中间状态可见性。如果 Reconcile 因 panic 退出，PatchHelper 不会执行，Status 保持上一次成功 Patch 的状态，保证一致性。
2. **引擎执行错误不中断 Reconcile**：`engine.Execute` 返回错误时，Reconcile 不直接 `return err`（那样会导致 Requeue 间隔内 Status 未被 Patch），而是记录 Event 并继续执行后续步骤（同步旧字段、记录指标）。这样即使状态机执行失败，集群的当前状态（如 Installing/Failed）也会被 Patch 到 CR 中，用户和 CAPI 上层 Controller 能观测到。
3. **SyncLegacyFields 向后兼容**：状态机引擎输出的 `LifecyclePhase` 等新字段需要同步到现有代码使用的旧字段（如 `Status.Phase`/`Status.Ready` 等），保证未迁移到状态机的代码路径不受影响。这是 Feature Gate 灰度迁移的关键——新旧字段共存，逐步移除旧字段。
4. **decideRequeue 控制重试节奏**：Requeue 间隔由集群状态决定——Installing/Upgrading 状态快速 Requeue（如 10s），Running/Failed 状态慢速 Requeue（如 5min）或不 Requeue。这保证了操作进行中的集群能快速推进，稳态集群不浪费 Reconcile 资源。

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

### 6.4 BKEMachine Controller 集成

**设计思路 — 节点层状态机独立 Reconcile，服务于扩缩容与单节点生命周期**：

1. **BKEMachine Reconcile 与 DAG 调度的关系**：默认路径（DAG 内联）下，所有场景由 BKECluster Controller 通过 `Scheduler.ExecuteDAG` + `syncNodeStatus` 统一执行和状态回写，BKEMachine Controller 不参与主执行路径——其 Reconcile 中 `evaluateNodePhase` 读取已写入的 `NodeComponentStatuses`，聚合后若状态已达成则 Execute 无匹配 case 直接返回 nil。启用可选路径（Watch 触发，Feature Gate `ScalingWatchTriggerEnabled`）后，BKEMachine Controller 恢复扩缩容执行职责——Watch 到 `NodePhase` 变化后执行 `NodeStateMachine.Execute` 驱动 L2/L3。BKEMachine Controller 的保留职责为：① Watch 触发扩缩容（可选）；② 单节点故障恢复——节点重启后检查组件状态；③ 手动重试——人工清除 Failed 状态后重新触发；④ 状态查询聚合——为 `kubectl get bkenode` 提供实时状态。
2. **nodeComponents 从 ReleaseImage 解析**：`getNodeComponents` 从当前 ReleaseImage 展开 composite 后过滤出 binary 类型组件。这与 §4.4.3 的设计一致——通过组件类型（binary）确定节点级组件。组件列表按依赖关系拓扑排序后传入 `NodeStateMachine.Execute`。
3. **BKEMachine 与 BKENode 的双向转换**：`machine.ToBKENode()` 将 CAPI BKEMachine CR 转换为状态机引擎使用的 `BKENode` 内部结构，`machine.UpdateFromBKENode(node)` 将状态机执行后的 `BKENode` 状态同步回 BKEMachine CR。这种转换隔离了 CAPI CR 结构与状态机内部数据模型——状态机引擎不依赖 CAPI 类型，仅操作 `BKENode`，便于独立测试。
4. **topologicalSort 复用已有实现**：节点内组件排序复用 `pkg/topology` 包的拓扑排序逻辑，不重新实现。排序仅考虑节点级组件间的依赖（如 bkeagent → containerd → kubelet），忽略对集群级组件的依赖（如 kubelet → kube-apiserver），因为集群级组件由 DAG 调度器保证在节点级组件之前完成。

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

### 6.5 CAPI 条件集成

**设计思路 — 将三层状态映射为 CAPI 标准 Conditions**：

1. **ReadyCondition 为唯一对外暴露条件**：CAPI Cluster Controller 和 Machine Controller 通过检查 `ReadyCondition` 判断集群/节点是否就绪。`setCAPIConditions` 将 `LifecyclePhase` 映射为 `ReadyCondition` 的 True/False + Reason，使 CAPI 上层无需感知 BKE 内部状态枚举。
2. **操作中状态映射为 Info 级别**：Installing/Upgrading/Scaling 状态映射为 `ReadyCondition=False, Reason=OperationInProgress, Severity=Info`。CAPI 模式中 Info 级别表示"正在处理，非异常"，上层 Controller 不会将其视为故障，只是等待 ReadyCondition 变为 True。
3. **Failed 状态映射为 Error 级别**：Failed 状态映射为 `ReadyCondition=False, Reason=OperationFailed, Severity=Error`。CAPI 上层 Controller 看到 Error 级别会触发告警或停止后续操作（如 MachineDeployment 不再扩容到故障集群）。
4. **RollingBack 状态的特殊处理**：RollingBack 不在 `setCAPIConditions` 的 switch 中——它映射为 `ReadyCondition=False, Reason=RollingBack, Severity=Warning`（需新增 case）。Warning 级别表示"需要注意但非致命"，与 CAPI 对回滚操作的预期一致。

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

**设计思路 — 首次安装是全量 DAG 执行，所有组件从零开始**：

1. **全量组件 DAG**：安装 DAG 由统一 `buildDAG` 从 `ReleaseImage.spec.upgrade.components` 构建（无 upgrade 声明时回退 `install.components`），包含全部组件（集群级 + 节点级）。composite 在构建时展开为子组件，全部作为独立 ComponentNode 参与拓扑排序。
2. **VersionContext Current 为空**：`prepareVersionContext` 在 Installing 阶段设置 Current 为空，`NeedsUpgrade` 对全部组件返回 true，所有组件执行 Install 操作。这与升级场景不同——升级时仅版本变更的组件执行 Upgrade。
3. **依赖顺序保证**：安装需要严格的跨组件依赖顺序（etcd → apiserver → kubelet → coredns），DAG 拓扑排序保证依赖在前。binary 组件的节点级并发由 `upgradeStrategy.mode` 控制（安装场景通常使用 Parallel 加速）。
4. **syncNodeStatus 回写**：DAG 执行完成后 `syncNodeStatus` 聚合所有节点的组件状态为 `NodePhase`，直接写入 BKEMachine CR 和 CAPI Conditions。BKEMachine Controller 不参与安装路径（默认 DAG 内联）。


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
│  │ 2. prepareVersionContext (Current 为空 → 全部 Install)                    │   │
│  │    buildDAG(releaseImage)  ← 统一 DAG 构建                                │   │
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

**设计思路 — 升级是增量 DAG 执行，仅版本变更的组件执行 Upgrade**：

1. **增量组件过滤**：升级 DAG 由统一 `buildDAG` 从 `ReleaseImage.spec.upgrade.components` 构建，`VersionContext.NeedsUpgrade` 过滤掉版本未变更的组件——已完成升级的组件跳过，仅执行版本变更的组件。这保证 Reconcile 重入时不会重复升级。
2. **deferredSubComponents 延迟升级**：composite 中声明的延迟组件（如 kubelet）由 `expandCompositeComponents` 解析后通过 ExecutionContext 传入 `executeControlPlaneHop`，跳过这些组件的 Target 版本更新，在后续偏差极限内补充升级。这避免了 kubelet 与 apiserver 版本差距过大导致集群不可用。
3. **节点级并发策略**：binary 组件升级使用 Rolling/Batch 策略（逐节点/分批），确保升级过程中集群始终有节点在线提供服务。这与安装场景的 Parallel 策略不同——升级需要保证服务连续性。
4. **syncNodeStatus 回写**：与安装流程完全一致，`syncNodeStatus` 在 DAG 执行后聚合节点状态。DAG 执行失败时也尝试同步已成功部分的状态（`_ = sm.syncNodeStatus(ctx, cluster)`），保证部分升级的进度可见。


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
│  │ 2. prepareVersionContext (Current=currentVersion → NeedsUpgrade 过滤)     │   │
│  │    buildDAG(newReleaseImage)  ← 统一 DAG 构建 (与安装同一方法)              │   │
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

### 7.3 扩容流程

**触发条件**：用户新增 BKENode（节点状态为 Pending），`desiredVersion == currentVersion` 且无其他操作触发。


**设计思路 — 扩容是仅新节点的 binary 组件安装，全并行提升效率**：

1. **仅 binary 组件**：`buildScalingDAG` 过滤出 `ComponentTypeBinary` 组件，集群级组件（yaml/helm/staticpod）不在扩容时重复执行——它们已在安装/升级时部署到集群，新节点通过 K8s 调度自动拉起 Pod。
2. **Parallel 全并行**：扩容场景使用 `Parallel` 策略，新节点全并行安装组件。这与安装/升级的 Rolling/Batch 策略不同——新节点的组件安装不影响已有节点的运行状态，无需逐节点串行。
3. **VersionContext 新节点 Current 为空**：新节点的组件状态为空（无 `ComponentLifecycleStatus` 记录），`evaluateNodePhase` 判定为 `Provisioning`，组件执行 Install 操作。已有节点的组件状态不受扩容影响。
4. **syncNodeStatus 统一回写**：DAG 执行后 `syncNodeStatus` 聚合新节点状态为 Ready，写入 BKEMachine CR 和 CAPI Conditions。CAPI MachineDeployment Controller Watch 到 `ReadyCondition=True` 后判定新节点就绪。

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          集群扩容完整流程                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  用户: 新增 2 个 BKENode (node-3, node-4)                                        │
│        节点 Agent 推送后 Status.LifecyclePhase = Pending                          │
│                                                                                 │
│  Reconcile #1                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Running → Scaling                              │   │
│  │    Condition: hasNodeCountChange = true (node-3/node-4 为 Pending)       │   │
│  │    跳过: desiredVersion == currentVersion (版本未变)                      │   │
│  │                                                                          │   │
│  │ 2. BuildScalingDAG(ctx, cluster)                                         │   │
│  │    └─ 仅 binary 组件 (cluster.Status.CurrentVersion != "" → 用 upgrade)  │   │
│  │    └─ VersionContext: 新节点 Current 为空 → Install 语义                  │   │
│  │    DAG: [bkeagent] → [containerd] → [kubelet]                            │   │
│  │                                                                          │   │
│  │ 3. ExecuteDAG(ctx, dag)                                                  │   │
│  │    ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │    │ Batch 1: [bkeagent]   upgradeStrategy.mode = Parallel            │   │   │
│  │    │   └─ BinaryComponentExecutor.ExecuteComponent()                   │   │   │
│  │    │       ├─ NodeStatusUpdater.MarkPending(node-3, "bkeagent")        │   │   │
│  │    │       ├─ NodeStatusUpdater.MarkPending(node-4, "bkeagent")        │   │   │
│  │    │       ├─ SSH 并行执行 Install (node-3 + node-4 同时)               │   │   │
│  │    │       ├─ NodeStatusUpdater.MarkSuccess(node-3, "bkeagent") ✓      │   │   │
│  │    │       └─ NodeStatusUpdater.MarkSuccess(node-4, "bkeagent") ✓      │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 2: [containerd]  Parallel                                    │   │   │
│  │    │   └─ SSH 并行执行 Install (node-3 + node-4 同时)                   │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 3: [kubelet]     Parallel                                    │   │   │
│  │    │   └─ SSH 并行执行 Install (node-3 + node-4 同时)                   │   │   │
│  │    └─────────────────────────────────────────────────────────────────┘   │   │
│  │                                                                          │   │
│  │ 4. syncNodeStatus(ctx, cluster)                                          │   │
│  │    └─ node-3: 全部 Installed → evaluateNodePhase → Ready                │   │
│  │    └─ node-4: 全部 Installed → evaluateNodePhase → Ready                │   │
│  │    └─ Patch BKEMachine CR (NodePhase=Ready, ComponentStatuses)           │   │
│  │    └─ setMachineCAPIConditions(machine, Ready) → ReadyCondition=True     │   │
│  │    ← BKEMachine Controller 不参与 (路径 1 DAG 内联)                       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #2                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Scaling → Running                              │   │
│  │    Condition: isScalingCompleted = true (无 Pending/Provisioning/Deleting)│   │
│  │                                                                          │   │
│  │ 2. 无操作 (Running 为稳态，无匹配 Execute case)                           │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  CAPI 上层:                                                                      │
│    └─ MachineDeployment Controller Watch BKEMachine.ReadyCondition=True         │
│       └─ 判定 node-3/node-4 就绪，继续后续编排                                    │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**设计要点**：
- 扩容走 DAG 内联路径（默认），与安装/升级统一执行路径（§6.2.2）
- `buildScalingDAG` 仅包含 binary 类型组件，集群级组件不重复执行
- `upgradeStrategy.mode = Parallel`，新节点全并行安装，提升扩容效率
- `syncNodeStatus` 统一聚合节点状态并直接写入 CAPI Conditions，BKEMachine Controller 不参与

### 7.4 缩容流程

**触发条件**：用户对 BKENode 设置 `Spec.Deleted = true`，`desiredVersion == currentVersion`。


**设计思路 — 缩容是 drain + 逐节点卸载，安全性优先于效率**：

1. **drain 前置保证**：缩容时先执行 `kubectl drain`（驱逐 Pod），作为 inline 类型 DAG 节点。通过 DAG 依赖边保证 drain 在组件卸载之前完成——Pod 驱逐后节点上无运行的工作负载，卸载组件不会影响业务。
2. **Rolling 逐节点串行**：缩容使用 Rolling 策略逐节点卸载（drain node-1 → 卸载 node-1 组件 → drain node-2 → 卸载 node-2 组件），而非 Parallel 全并行。这保证一次只删除一个节点，集群有足够的副本维持服务可用性。
3. **VersionContext Uninstall 语义**：已有节点的 `Current` 有值，`evaluateComponentPhase` 根据 `action == ActionUninstall` 返回 `Deleting`，Executor 执行 UninstallScript 卸载组件。卸载完成后 `NodeStatusUpdater.MarkRemoved` 清除组件状态。
4. **状态清除**：`syncNodeStatus` 对已删除节点清除 `NodeComponentStatuses`，节点从 DAG 中移除。BKEMachine CR 的 CAPI Conditions 被清除，CAPI MachineDeployment Controller 据此回收 Machine 资源。

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          集群缩容完整流程                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  用户: 标记 node-3, node-4 为删除 (Spec.Deleted = true)                          │
│                                                                                 │
│  Reconcile #1                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Running → Scaling                              │   │
│  │    Condition: hasNodeCountChange = true (node-3/node-4.Spec.Deleted)     │   │
│  │                                                                          │   │
│  │ 2. BuildScalingDAG(ctx, cluster)                                         │   │
│  │    └─ 仅 binary 组件 + drain inline 节点                                  │   │
│  │    └─ VersionContext: 已有节点 Current 有值 → Uninstall 语义              │   │
│  │    DAG:                                                                 │   │
│  │      [drain-node-3] → [bkeagent→containerd→kubelet] (node-3 卸载)        │   │
│  │      [drain-node-4] → [bkeagent→containerd→kubelet] (node-4 卸载)        │   │
│  │                                                                          │   │
│  │ 3. ExecuteDAG(ctx, dag)                                                  │   │
│  │    ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │    │ Batch 1: [drain-node-3, drain-node-4]  inline 类型                 │   │   │
│  │    │   └─ InlineComponentExecutor.ExecuteComponent()                   │   │   │
│  │    │       └─ kubectl drain node-3 (驱逐 Pod)                           │   │   │
│  │    │       └─ kubectl drain node-4 (驱逐 Pod)                           │   │   │
│  │    │       ← drain 完成后才允许组件卸载 (依赖边保证)                       │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 2: [bkeagent, containerd, kubelet]  Rolling 逐节点           │   │   │
│  │    │   └─ BinaryComponentExecutor.ExecuteComponent()                    │   │   │
│  │    │       ├─ SSH 执行 Uninstall (Rolling: node-3 → node-4 串行)        │   │   │
│  │    │       ├─ NodeStatusUpdater.MarkRemoved(node-3, comp)               │   │   │
│  │    │       └─ NodeStatusUpdater.MarkRemoved(node-4, comp)               │   │   │
│  │    └─────────────────────────────────────────────────────────────────┘   │   │
│  │                                                                          │   │
│  │ 4. syncNodeStatus(ctx, cluster)                                          │   │
│  │    └─ node-3: NodeComponentStatuses 已清除 → 从 DAG 移除                  │   │
│  │    └─ node-4: NodeComponentStatuses 已清除 → 从 DAG 移除                  │   │
│  │    └─ 清除 BKEMachine CAPI Conditions (节点已删除)                        │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #2                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. EvaluateClusterPhase: Scaling → Running                              │   │
│  │    Condition: isScalingCompleted = true (无 Deleting 节点)                │   │
│  │ 2. 无操作 (稳态)                                                          │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**设计要点**：
- 缩容使用 Rolling 策略逐节点串行卸载，确保安全驱逐 Pod 后才卸载组件（§6.2.2）
- drain 作为 inline 类型 DAG 节点，通过依赖边保证 drain 在组件卸载之前完成
- drain 与组件卸载的顺序由 DAG 拓扑排序保证，避免各节点独立 Reconcile 导致的顺序问题

### 7.5 回滚流程

**触发条件**：升级失败后用户设置 `Spec.RollbackRequested = true`，回滚到 `Status.PreviousVersion`。


**设计思路 — 回滚是版本回退的 DAG 执行，Current/Target 交换实现降级**：

1. **RollbackRequested 优先级最高**：`evaluateClusterPhase` 中 `RollbackRequested=true` 的优先级高于 `Failed` 不自愈检查，允许从故障态直接触发回滚。这是唯一能从 `Failed` 状态退出的自动路径——其他场景需人工清除 Failed。
2. **统一 DAG + resolveReleaseImage**：`buildDAG` 通过 `resolveReleaseImage` 在 RollingBack 阶段解析 `Status.PreviousVersion` 的 ReleaseImage，使用其 `upgrade.components` 构建组件列表。回滚 DAG 的结构与升级 DAG 相同（统一 `buildDAG`），仅版本回退。
3. **VersionContext.PrepareRollback()**：交换 `Current` 和 `Target`——原 Current（升级前版本 v2.6.0）变为 Target，原 Target（升级后版本 v2.7.0）变为 Current。这样 `NeedsUpgrade` 判断的是"从 v2.7.0 回退到 v2.6.0"，已完成回滚的组件跳过。
4. **Rolling 逐节点降级**：回滚使用 Rolling 策略逐节点降级，确保降级过程中集群始终有节点在线。binary 组件执行 UninstallScript + InstallScript 回退到旧版本，helm 组件执行 `helm rollback`。

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          集群回滚完整流程                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  前置: 集群升级 v2.6.0 → v2.7.0 失败, Status.LifecyclePhase = Failed             │
│        Status.PreviousVersion = "v2.6.0"                                         │
│        Status.CurrentVersion = "v2.6.0" (未更新成功)                              │
│                                                                                 │
│  用户: kubectl patch bkecluster my-cluster --type merge                          │
│        -p '{"spec":{"rollbackRequested":true}}'                                  │
│                                                                                 │
│  Reconcile #1                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Failed → RollingBack                           │   │
│  │    └─ RollbackRequested=true (优先级最高, 跳过 Failed 不自愈检查)         │   │
│  │    ← 注意: RollbackRequested 优先于 Failed, 允许从故障态触发回滚           │   │
│  │                                                                          │   │
│  │ 2. prepareVersionContext (RollingBack → PrepareRollback 交换 Current/Target)│   │
│  │       ← 交换: Current=v2.7.0 (升级后), Target=v2.6.0 (回滚目标)             │   │
│  │       ← NeedsUpgrade 判断 "从 v2.7.0 回退到 v2.6.0"                       │   │
│  │    buildDAG(ctx, cluster)  ← 统一 DAG 构建 (resolveReleaseImage 解析 v2.6.0) │   │
│  │    └─ resolveReleaseImage: RollingBack → ResolveByVersion(PreviousVersion)│   │
│  │    └─ 从 v2.6.0 ReleaseImage.spec.upgrade.components 构建组件列表          │   │
│  │    └─ expandCompositeComponents 展开为子组件                              │   │
│  │    DAG: [bkeagent] → [containerd] → [kubelet] → [coredns]                │   │
│  │                                                                          │   │
│  │ 3. ExecuteDAG(ctx, dag)                                                  │   │
│  │    ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │    │ Batch 1: [bkeagent]  Rolling                                       │   │   │
│  │    │   └─ BinaryComponentExecutor: SSH 逐节点降级 bkeagent              │   │   │
│  │    │       v2.7.0 → v2.6.0                                              │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 2: [containerd] Rolling                                      │   │   │
│  │    │   └─ SSH 逐节点降级 containerd v2.7.0 → v2.6.0                     │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 3: [kubelet]    Rolling                                      │   │   │
│  │    │   └─ SSH 逐节点降级 kubelet v2.7.0 → v2.6.0                        │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 4: [coredns]    helm                                         │   │   │
│  │    │   └─ Helm SDK: helm rollback coredns v2.6.0                        │   │   │
│  │    └─────────────────────────────────────────────────────────────────┘   │   │
│  │                                                                          │   │
│  │ 4. syncNodeStatus(ctx, cluster)                                          │   │
│  │    └─ 所有节点组件回退到 v2.6.0 → evaluateNodePhase → Ready              │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #2 (回滚 DAG 全部完成后)                                               │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. EvaluateClusterPhase: RollingBack → Running                          │   │
│  │    Condition: isDAGCompleted = true (所有组件已达目标版本 v2.6.0)         │   │
│  │                                                                          │   │
│  │ 2. 更新 currentVersion = "v2.6.0" (Status.DesiredVersion 已回退)         │   │
│  │ 3. 清除 RollbackRequested = false                                       │   │
│  │ 4. 记录回滚完成事件                                                       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**设计要点**：
- `RollbackRequested` 优先级最高，允许从 `Failed` 状态触发回滚（§4.4.1 决策优先级 2）
- 回滚使用统一 `buildDAG` 构建，`resolveReleaseImage` 解析 `PreviousVersion` 的 ReleaseImage（§4.4.4）
- `prepareVersionContext` 中 `PrepareRollback()` 交换 Current/Target，使 `NeedsUpgrade` 判断 "从升级后版本回退到升级前版本"
- 回滚使用 Rolling 策略逐节点降级，确保降级过程中的服务可用性

### 7.6 升级失败与中断重入流程

**触发条件**：升级过程中部分组件执行失败，演示跨 Reconcile 的幂等性和中间态保持。


**设计思路 — 跨 Reconcile 的幂等性和中间态保持是核心保证**：

1. **中间态保持**：`Upgrading` 状态跨 Reconcile 保持，`evaluateClusterPhase` 返回 `Upgrading`（决策优先级 3：操作中间态保持），下次 Reconcile 继续执行 DAG 而非重新开始。这保证长时间的升级操作不被中断重置。
2. **组件级幂等**：已完成升级的组件通过 `VersionContext.NeedsUpgrade=false` 跳过（`evaluateComponentPhase` 返回 `Installed`），未完成的组件继续执行。即使 Reconcile 因超时中断，下次重入时仅执行未完成部分。
3. **Failed 不自愈**：node-2 的 containerd 进入 `Failed` 后，`evaluateComponentPhase` 返回 `Failed`（优先级 1：Failed 不自愈），Execute 无匹配 case 跳过执行。故障组件不会在未修复前被反复重试，避免故障扩散。
4. **syncNodeStatus 容错**：DAG 执行失败时也调用 `syncNodeStatus`（`_ = sm.syncNodeStatus(ctx, cluster)`），同步已成功部分的状态。这保证部分升级的进度可见——用户和 CAPI 上层能观测到哪些节点已完成、哪些失败。

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                  升级失败与中断重入完整流程 (幂等性演示)                              │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  前置: 集群 v2.6.0 → v2.7.0 升级中, Status.LifecyclePhase = Upgrading            │
│                                                                                 │
│  Reconcile #1 (升级开始)                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. EvaluateClusterPhase: Running → Upgrading                            │   │
│  │ 2. prepareVersionContext + buildDAG → ExecuteDAG                          │   │
│  │    └─ Batch 2: [containerd]                                              │   │
│  │       ├─ node-1: NeedsUpgrade=false (已 v2.7.0) → 跳过 ✓                  │   │
│  │       ├─ node-2: containerd Pending → evaluateComponentPhase:            │   │
│  │       │     installedVersion="" (人工清除) → Installing                   │   │
│  │       │   └─ Execute: executor.Install(containerd v2.7.0) ✓               │   │
│  │       │   └─ status.Version = "v2.7.0", Phase = Installed                │   │
│  │       └─ node-3: NeedsUpgrade=false (已 v2.7.0) → 跳过 ✓                  │   │
│  │    └─ Batch 1: [bkeagent] Rolling                                        │   │
│  │       ├─ node-1: bkeagent v2.6.0 → v2.7.0 ✓                             │   │
│  │       ├─ node-2: bkeagent v2.6.0 → v2.7.0 ✓                             │   │
│  │       └─ node-3: bkeagent v2.6.0 → v2.7.0 ✓                             │   │
│  │    └─ Batch 2: [containerd] Rolling                                      │   │
│  │       ├─ node-1: containerd v2.6.0 → v2.7.0 ✓                           │   │
│  │       ├─ node-2: containerd v2.6.0 → v2.7.0 ✗ (SSH 超时失败)             │   │
│  │       │   └─ NodeStatusUpdater.MarkFailed(node-2, "containerd")          │   │
│  │       └─ node-3: 未执行 (Rolling 串行, node-2 失败中断)                    │   │
│  │    ← ExecuteDAG 返回 error                                               │   │
│  │ 3. _ = sm.syncNodeStatus(ctx, cluster)                                   │   │
│  │    └─ node-1: bkeagent=v2.7.0, containerd=v2.7.0 → Ready                │   │
│  │    └─ node-2: bkeagent=v2.7.0, containerd=Failed → Failed               │   │
│  │    └─ node-3: bkeagent=v2.7.0, containerd=v2.6.0 → Provisioning         │   │
│  │ 4. 返回 error (Reconciler 记录 Event, defer Patch 保存当前状态)            │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #2 (Requeue 后重入)                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. EvaluateClusterPhase: Upgrading (中间态保持)                          │   │
│  │    └─ currentPhase == Upgrading, isDAGCompleted = false                  │   │
│  │    ← 决策优先级 3: 操作中间态保持, 跨 Reconcile 继续执行                    │   │
│  │                                                                          │   │
│  │ 2. prepareVersionContext + buildDAG → ExecuteDAG                          │   │
│  │    └─ Batch 1: [bkeagent]                                                │   │
│  │       ├─ node-1: NeedsUpgrade=false (已 v2.7.0) → 跳过 ✓                  │   │
│  │       ├─ node-2: NeedsUpgrade=false (已 v2.7.0) → 跳过 ✓                  │   │
│  │       └─ node-3: NeedsUpgrade=false (已 v2.7.0) → 跳过 ✓                  │   │
│  │       ← 幂等: 已完成组件通过 VersionContext.NeedsUpgrade 跳过              │   │
│  │    └─ Batch 2: [containerd]                                              │   │
│  │       ├─ node-1: NeedsUpgrade=false (已 v2.7.0) → 跳过 ✓                  │   │
│  │       ├─ node-2: containerd Failed → 自愈?                                │   │
│  │       │   ← ComponentStateMachine.evaluateComponentPhase:                │   │
│  │       │     currentPhase=Failed → 返回 Failed (不自愈)                     │   │
│  │       │   ← Execute 无匹配 case → 跳过执行                                 │   │
│  │       └─ node-3: NeedsUpgrade=true (仍 v2.6.0) → 执行 Upgrade            │   │
│  │           └─ containerd v2.6.0 → v2.7.0 ✓                                │   │
│  │    ← ExecuteDAG 返回 error (node-2 containerd 仍 Failed)                  │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #N (超过 maxRetries)                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. EvaluateClusterPhase: Upgrading → Failed                             │   │
│  │    Condition: 重试次数超限, DAG 持续失败                                   │   │
│  │ 2. 无操作 (Failed 不自愈, 等待人工介入)                                    │   │
│  │ 3. 记录 Failed 事件, 触发告警 (BKEClusterOperationFailed)                 │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**设计要点**：
- **中间态保持**：`Upgrading` 状态跨 Reconcile 保持，不重新开始（§4.4.1 决策优先级 3）
- **组件级幂等**：已完成组件通过 `VersionContext.NeedsUpgrade=false` 跳过（§4.2）
- **Failed 不自愈**：node-2 的 containerd 进入 Failed 后不自动重试，等待人工介入（§4.6.1）
- **syncNodeStatus 容错**：即使 DAG 执行失败也尝试同步已成功部分的状态（§6.2.1）

### 7.7 Failed 故障恢复流程

**触发条件**：集群处于 `Failed` 状态，人工修复底层问题（如网络、磁盘）后清除 Failed 状态重新触发操作。


**设计思路 — 人工介入清除 Failed 后重新评估，幂等恢复保证安全**：

1. **Failed 不自愈的设计权衡**：集群和组件层均不自动恢复 `Failed` 状态，避免在未修复底层问题（如网络、磁盘故障）前反复重试导致故障扩散。人工介入修复底层问题后清除 Failed，重新评估版本关系。
2. **双重清除**：需同时清除集群级 `Failed`（重置为 `Upgrading`）和组件级 `Failed`（重置为 `Pending`）。仅清除集群级而不清除组件级，组件仍返回 `Failed` 跳过执行。仅清除组件级而不清除集群级，集群仍在 `Failed` 不执行 DAG。
3. **清除后幂等恢复**：人工清除后 `evaluateComponentPhase` 重新判断版本关系——已完成升级的组件（版本一致）返回 `Installed` 跳过，未完成的组件（版本不一致）返回 `Upgrading` 继续执行。这保证恢复不会重新升级已完成组件。
4. **evaluateComponentPhase 版本比较驱动**：人工清除 Failed 后 `status.Phase = Pending`，`evaluateComponentPhase` 通过比较 `status.Version`（人工清除时也清除了版本记录）与 `cv.Spec.Version` 判定为 `Installing`（无版本记录）或 `Upgrading`（版本不一致），触发重新执行。

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                  Failed 故障恢复完整流程 (人工介入)                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  前置: Status.LifecyclePhase = Failed                                            │
│        node-2 containerd 仍为 Failed (底层 SSH 不可达, 已修复网络)                 │
│        desiredVersion = "v2.7.0", currentVersion = "v2.6.0" (升级未完成)         │
│                                                                                 │
│  用户操作 (人工介入):                                                              │
│    1. 修复 node-2 网络问题 (SSH 恢复可达)                                         │
│    2. 清除 node-2 containerd 的 Failed 状态:                                      │
│       kubectl patch bkenode node-2 --type merge                                  │
│         -p '{"status":{"componentStatuses":{"containerd":{"phase":"Pending"}}}}' │
│    3. 清除集群 Failed 状态:                                                       │
│       kubectl patch bkecluster my-cluster --type merge                           │
│         -p '{"status":{"lifecyclePhase":"Upgrading"}}'                           │
│                                                                                 │
│  Reconcile #1 (人工介入后)                                                        │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. EvaluateClusterPhase: Upgrading (用户已手动重置为 Upgrading)           │   │
│  │    └─ currentPhase=Upgrading, isDAGCompleted=false → 保持 Upgrading      │   │
│  │                                                                          │   │
│  │ 2. prepareVersionContext + buildDAG → ExecuteDAG                          │   │
│  │    └─ Batch 2: [containerd]                                              │   │
│  │       ├─ node-1: NeedsUpgrade=false (已 v2.7.0) → 跳过 ✓                  │   │
│  │       ├─ node-2: containerd Pending → evaluateComponentPhase:            │   │
│  │       │     installedVersion="" (人工清除) → Installing                   │   │
│  │       │   └─ Execute: executor.Install(containerd v2.7.0) ✓               │   │
│  │       │   └─ status.Version = "v2.7.0", Phase = Installed                │   │
│  │       └─ node-3: NeedsUpgrade=false (已 v2.7.0) → 跳过 ✓                  │   │
│  │    ← ExecuteDAG 成功 (所有组件达 v2.7.0)                                  │   │
│  │                                                                          │   │
│  │ 3. syncNodeStatus(ctx, cluster)                                          │   │
│  │    └─ node-2: 全部 Installed → Ready                                     │   │
│  │ 4. isDAGCompleted = true → 更新 currentVersion = "v2.7.0"                │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #2                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. EvaluateClusterPhase: Upgrading → Running                            │   │
│  │    Condition: isDAGCompleted = true                                      │   │
│  │ 2. 记录升级完成事件                                                       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**设计要点**：
- **Failed 不自愈**：集群和组件层均不自动恢复 Failed 状态，需人工清除后重新评估（§4.4.1/§4.6.1）
- **人工清除粒度**：需同时清除集群级 Failed（重置为 Upgrading）和组件级 Failed（重置为 Pending）
- **清除后幂等恢复**：人工清除 Failed 后，`evaluateComponentPhase` 重新判断版本关系，已完成组件跳过，未完成组件重新执行

### 7.8 Watch 触发扩容流程（可选路径）

**触发条件**：启用 Feature Gate `ScalingWatchTriggerEnabled`，扩容走 BKEMachine Watch 触发路径（§6.2.3）。


**设计思路 — 可选路径通过 Feature Gate 控制，L2 驱动 L3 各节点独立并行**：

1. **Feature Gate 切换**：`ScalingWatchTriggerEnabled` 控制扩缩容走 DAG 内联（默认）还是 Watch 触发（可选）。两种路径不会同时执行——DAG 内联路径中 L1 直接操作 L3，Watch 触发路径中 L2 驱动 L3。Feature Gate 通过集群注解或全局 flag 控制。
2. **L2 完整参与**：与 DAG 内联路径（L2 不参与）不同，Watch 触发路径中 `NodeStateMachine.Execute` 驱动 L2 评估节点状态 → L3 执行组件操作。三层状态机完整参与，与旧架构（Phase 框架）的行为接近，便于灰度迁移。
3. **各节点独立并行**：每个 BKEMachine 独立 Reconcile，无全局批次协调。适用于大规模集群（数千节点时 L1 DAG 串行化成为瓶颈）和多可用区场景（各区域网络延迟不一致时独立 Reconcile 不受全局等待）。
4. **状态实时聚合**：与 DAG 内联路径的 `syncNodeStatus`（DAG 完成后一次性聚合）不同，Watch 触发路径的 `evaluateNodePhase` 在每次组件执行后实时聚合节点状态。中间状态可见性更好，但聚合逻辑分散在各 BKEMachine Controller 中。

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                  Watch 触发扩容完整流程 (可选路径, Feature Gate 控制)                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  前置: Feature Gate ScalingWatchTriggerEnabled = true                            │
│        新增 node-3, node-4 (Status.LifecyclePhase = Pending)                      │
│                                                                                 │
│  Reconcile #1 (BKECluster Controller)                                            │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Running → Scaling                              │   │
│  │ 2. scalingWatchTriggerEnabled(cluster) = true → 走 Watch 触发路径        │   │
│  │ 3. 不构建/执行 DAG, 仅更新 BKEMachine CR Status:                         │   │
│  │    └─ node-3: NodePhase = Provisioning, NodeComponents = [bkeagent,      │   │
│  │               containerd, kubelet] (展开 composite 后的 binary 组件)      │   │
│  │    └─ node-4: NodePhase = Provisioning, NodeComponents = [...]           │   │
│  │    └─ Patch BKEMachine CR                                                │   │
│  │    ← L1 不执行 DAG, 仅触发 L2                                            │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│          ↓ BKEMachine Controller Watch 到 Status 变化 ↓                          │
│                                                                                 │
│  Reconcile #1 (BKEMachine Controller, node-3)  ← 各节点独立 Reconcile, 并行      │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. 获取 BKEMachine + 关联 BKECluster                                    │   │
│  │ 2. getNodeComponents (从 ReleaseImage 解析 binary 组件)                  │   │
│  │ 3. NodeStateMachine.Execute(node-3, components)  (L2)                    │   │
│  │    └─ evaluateNodePhase: Provisioning (无组件状态 → 新节点首次安装)       │   │
│  │    └─ executeNodeComponents(ActionInstall)                               │   │
│  │       └─ topologicalSort: [bkeagent, containerd, kubelet]                │   │
│  │       └─ ComponentStateMachine.Execute(comp, ActionInstall)  (L3)        │   │
│  │          ├─ bkeagent:   Pending → Installing → Installed ✓               │   │
│  │          ├─ containerd: Pending → Installing → Installed ✓               │   │
│  │          └─ kubelet:    Pending → Installing → Installed ✓               │   │
│  │ 4. evaluateNodePhase: 全部 Installed → Ready                             │   │
│  │ 5. UpdateFromBKENode + Patch BKEMachine (NodePhase=Ready)                │   │
│  │ 6. setMachineCAPIConditions(machine, Ready) → ReadyCondition=True        │   │
│  │    ← 路径 3 中 CAPI Conditions 由 BKEMachine Controller 写入              │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #1 (BKEMachine Controller, node-4)  ← 与 node-3 并行                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ ... (与 node-3 相同流程, 并行执行) ...                                    │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Reconcile #2 (BKECluster Controller)                                            │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 1. EvaluateClusterPhase: Scaling → Running                              │   │
│  │    Condition: isScalingCompleted = true (所有 BKEMachine Ready)           │   │
│  │ 2. 无操作 (稳态)                                                          │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**设计要点**：
- **Feature Gate 控制**：通过 `ScalingWatchTriggerEnabled` 切换 DAG 内联与 Watch 触发路径（§6.2.3）
- **L2 驱动 L3**：Watch 触发路径中 `NodeStateMachine.Execute` 驱动 `ComponentStateMachine.Execute`，三层完整参与
- **各节点独立并行**：每个 BKEMachine 独立 Reconcile，无全局批次协调，适用于大规模集群
- **状态实时聚合**：`evaluateNodePhase` 在每次组件执行后实时聚合节点状态（对比 DAG 内联路径的 DAG 完成后一次性聚合）
- **CAPI Conditions 写入者差异**：路径 3 由 BKEMachine Controller 写入，路径 1/2 由 `syncNodeStatus` 写入（§6.2.4 对比表）

### 7.9 场景对比总结
**设计思路 — 统一 DAG 内联为默认路径，所有场景共享核心设计原则**：

8 个场景共享 4 条核心设计原则，差异仅在 DAG 构建方式、组件操作类型和并发策略：



| 场景 | 触发条件 | L1 Phase | DAG 类型 | 执行路径 | L2 参与 | 关键策略 |
|------|---------|----------|---------|---------|---------|---------|
| **安装** (§7.1) | desiredVersion 设置, 无 currentVersion | Installing | `buildDAG` (统一) | DAG 内联 | 否 | 全组件, VersionContext 无 Current |
| **升级** (§7.2) | desiredVersion != currentVersion | Upgrading | `buildDAG` (统一) | DAG 内联 | 否 | NeedsUpgrade 过滤, Rolling/Batch |
| **扩容** (§7.3) | 新增 BKENode (Pending) | Scaling | `buildScalingDAG` (变体) | DAG 内联 | 否 | 仅 binary, Parallel 全并行 |
| **缩容** (§7.4) | BKENode.Spec.Deleted=true | Scaling | `buildScalingDAG` (变体) | DAG 内联 | 否 | drain inline + Rolling 逐节点 |
| **回滚** (§7.5) | RollbackRequested=true | RollingBack | `buildDAG` (统一) | DAG 内联 | 否 | PreviousVersion, Current/Target 交换 |
| **失败重入** (§7.6) | 升级中部分组件失败 | Upgrading→Failed | `buildDAG` (统一) | DAG 内联 | 否 | 中间态保持, 组件级幂等跳过 |
| **故障恢复** (§7.7) | 人工清除 Failed 后 | Upgrading | `buildDAG` (统一) | DAG 内联 | 否 | 人工重置状态, 重新评估版本 |
| **Watch 扩容** (§7.8) | 新增 BKENode + Feature Gate | Scaling | 无 (L1 仅更新 CR) | Watch 触发 | 是 | 各节点独立 Reconcile, 实时聚合 |

**核心设计原则贯穿所有场景**：
1. **幂等性**：所有场景支持 Reconcile 重入，已完成组件通过 `VersionContext.NeedsUpgrade` 跳过
2. **中间态保持**：操作态（Installing/Upgrading/Scaling/RollingBack）跨 Reconcile 保持，不重新开始
3. **Failed 不自愈**：故障状态不自动恢复，等待人工介入清除后重新评估
4. **状态回写统一**：DAG 内联路径由 `syncNodeStatus` 统一聚合，Watch 触发路径由 `evaluateNodePhase` 实时聚合

---

## 8. 总结

### 8.1 设计优势

| 优势 | 说明 |
|------|------|
| **统一入口** | BKECluster Reconcile 统一触发，架构清晰 |
| **DAG 驱动** | 从 ReleaseImage 构建 DAG，执行顺序由依赖关系决定 |
| **节点级并行** | BinaryComponentExecutor 支持 Rolling/Parallel/Batch，多节点并行执行，提升大规模集群效率 |
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

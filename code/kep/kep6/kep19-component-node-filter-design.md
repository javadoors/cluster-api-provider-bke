# KEP-19: ComponentVersion 节点过滤设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-19 |
| **标题** | ComponentVersion 节点过滤：基于角色/标签/幂等的节点级选择 |
| **状态** | `provisional` |
| **类型** | Feature |
| **依赖** | KEP-5 声明式升级框架、KEP-16 二进制组件设计、KEP-18 条件过滤 |
| **来源** | 从 `kep16-binary-component-design.md` §8.5 抽离，泛化到所有组件类型 |

---

## 目录

1. [设计动机](#1-设计动机)
2. [数据结构设计](#2-数据结构设计)
3. [节点角色与标签模型](#3-节点角色与标签模型)
4. [NodeFilter 接口设计](#4-nodefilter-接口设计)
5. [BKENodeFilter 默认实现](#5-bkenodefilter-默认实现)
6. [per-node 幂等设计](#6-per-node-幂等设计)
7. [状态数据模型](#7-状态数据模型)
8. [各组件类型集成方式](#8-各组件类型集成方式)
9. [ComponentVersion YAML 示例](#9-componentversion-yaml-示例)
10. [与当前代码的等价性](#10-与当前代码的等价性)
11. [兼容性设计](#11-兼容性设计)
12. [与 KEP-18 Condition 的关系](#12-与-kep-18-condition-的关系)
13. [工作量评估](#13-工作量评估)
14. [风险与缓解措施](#14-风险与缓解措施)

---

## 1. 设计动机

### 1.1 问题分析

当前 DAG 执行的节点过滤逻辑存在三个缺口：

| 缺口 | 当前状态 | 应有行为 |
|------|---------|---------|
| **节点选择** | `NodeProvider.GetNodes()` 返回全部节点，无角色/标签过滤 | 应支持按角色/标签选择目标节点 |
| **per-node 幂等** | `VersionContext.NeedsUpgrade(name)` 是组件级判断 | 应支持 per-node 判断（node1 已安装，node2 未安装） |
| **状态回写** | 无 per-node per-component 状态回写 | 应在安装成功/失败后更新 per-node 状态 |

当前代码中各组件的节点过滤逻辑各不相同，无统一机制：

| 组件 | 当前过滤函数 | 过滤条件 |
|------|------------|---------|
| bkeagent (安装) | `GetNeedPushAgentNodesWithBKENodes` | `!NodeAgentPushedFlag` + Appointment 排除 |
| bkeagent (升级) | **无过滤** | 全部节点都执行 |
| containerd | `GetNeedUpgradeNodesWithBKENodes` | `OpenFuyaoVersion` 版本比较 |
| kubernetes | `GetNeedUpgradeK8sNodes` + 角色过滤 | `KubernetesVersion` + master/worker |
| etcd | `filterUpgradeableNodes` + `.Etcd()` | `EtcdVersion` + etcd 角色 |

### 1.2 设计目标

通过 `ComponentVersion.Spec.NodeFilter` 声明式定义节点过滤策略，统一替代当前分散在各 Phase 中的硬编码过滤逻辑。

---

## 2. 数据结构设计

### 2.1 ComponentVersionSpec 新增 NodeFilter 字段

```go
// api/v1alpha1/componentversion_types.go

type ComponentVersionSpec struct {
    // ... 现有字段 ...
    Condition  string           `json:"condition,omitempty"`    // KEP-18: 集群级条件过滤
    NodeFilter *NodeFilterSpec  `json:"nodeFilter,omitempty"`   // ★ 新增: 节点级过滤
}

// NodeFilterSpec 定义组件在哪些节点上执行
//
// 设计思路 — 为什么放在 ComponentVersionSpec 顶层而非 UpgradeStrategySpec 内:
// 安装和升级都需要节点过滤，不应绑定到"升级策略"语义中
//
// 设计思路 — 为什么泛化到所有组件类型 (不仅 Binary):
// - Binary: SSH 在节点上执行，NodeFilter 选择目标节点
// - Inline: handler 内部 filterNodes() 可消费 NodeFilter
// - YAML/Helm: 集群级部署，NodeFilter 无效 (通过 values/nodeSelector 自行处理)
type NodeFilterSpec struct {
    // 目标节点角色列表
    // 空或不填 = 所有角色
    // 示例: ["master"], ["node"], ["etcd"], ["master", "node"]
    // +optional
    Roles []string `json:"roles,omitempty"`

    // 节点标签选择器 (等值匹配)
    // 空或不填 = 不按标签过滤
    // 示例: {"gpu": "true", "node-pool": "compute"}
    // +optional
    MatchLabels map[string]string `json:"matchLabels,omitempty"`

    // 是否跳过已完成的节点 (per-node 幂等)
    // true (默认): 检查 NodeComponentStatuses[nodeIP].Version == target → 跳过
    // false: 对所有节点执行，不检查 per-node 状态
    // 例外: bkeagent 升级设为 false (当前代码 EnsureAgentUpgrade 无过滤)
    // +optional
    SkipCompleted *bool `json:"skipCompleted,omitempty"`

    // 是否排除预约添加的节点
    // 默认 true (与当前 filterNodes 的 WithExcludeAppointmentNodes 一致)
    // +optional
    ExcludeAppointment *bool `json:"excludeAppointment,omitempty"`
}
```

### 2.2 为什么放在 ComponentVersionSpec 顶层

| 候选位置 | 优点 | 缺点 |
|---------|------|------|
| `ComponentVersionSpec` 顶层 ★ | 安装和升级共用；语义清晰 | 无 |
| `UpgradeStrategySpec` 内 | 与升级策略聚合 | 仅升级场景使用，安装场景需要单独定义；UpgradeStrategy 语义是"怎么做"不是"在哪做" |

---

## 3. 节点角色与标签模型

### 3.1 BKENodeSpec 身份字段

```go
// api/bkecommon/v1beta1/bkenode_types.go (现有)

type BKENodeSpec struct {
    Role     []string `json:"role,omitempty"`      // 节点角色列表
    IP       string   `json:"ip"`                   // 节点 IP
    Hostname string   `json:"hostname,omitempty"`   // 主机名
    Labels   []Label  `json:"labels,omitempty"`      // 节点标签 (Key-Value 列表)
}
```

### 3.2 角色常量

```go
// common/cluster/node/node.go (现有)

const (
    WorkerNodeRole       = "node"
    MasterNodeRole       = "master"
    EtcdNodeRole         = "etcd"
    MasterWorkerNodeRole = "master/node"            // 复合角色: 单个字符串元素
)
```

### 3.3 Label 类型

```go
// api/bkecommon/v1beta1/bkecluster_spec.go (现有)

type Label struct {
    Key   string `json:"key"`
    Value string `json:"value"`
}
```

> **注意**: `BKENode.Spec.Labels` 是 `[]Label`（切片），不是 `map[string]string`。NodeFilter 的 `MatchLabels` 是 `map[string]string`，匹配时需转换。

---

## 4. NodeFilter 接口设计

```go
// pkg/dagexec/node_filter.go

// NodeFilter 节点过滤接口
//
// 设计思路 — 为什么不内置到 Executor:
// 1. 不同组件的过滤逻辑不同 (bkeagent 按位标记，containerd 按版本比较)
// 2. 过滤逻辑可能随组件类型演化，Executor 不应绑定特定实现
// 3. 测试时可注入 Mock Filter，独立测试 Executor 调度逻辑
//
// 设计思路 — 为什么不内置到 NodeProvider:
// NodeProvider 职责是"获取节点"，NodeFilter 职责是"过滤节点"
// 两者关注点不同: Provider 关心数据来源，Filter 关心业务逻辑
type NodeFilter interface {
    // Filter 返回需要执行操作的节点列表
    Filter(ctx context.Context, nodes []Node, cv *cvv1alpha1.ComponentVersion, execCtx *ExecutionContext) ([]Node, error)
}
```

### 4.1 Node 类型

```go
// pkg/dagexec/node_filter.go

// Node 是 NodeFilter 和 Executor 使用的节点数据结构
type Node struct {
    Name   string            // 节点名称
    IP     string            // 节点 IP
    Hostname string          // 主机名
    Role   string            // 主角色 (master/node/etcd)
    Labels map[string]string // 节点标签 (从 BKENode.Spec.Labels 转换)
}
```

---

## 5. BKENodeFilter 默认实现

```go
// pkg/dagexec/bke_node_filter.go

type BKENodeFilter struct {
    client client.Client
}

func (f *BKENodeFilter) Filter(
    ctx context.Context,
    nodes []Node,
    cv *cvv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) ([]Node, error) {
    nf := cv.Spec.NodeFilter
    skipCompleted := true
    if nf != nil && nf.SkipCompleted != nil {
        skipCompleted = *nf.SkipCompleted
    }
    excludeAppointment := true
    if nf != nil && nf.ExcludeAppointment != nil {
        excludeAppointment = *nf.ExcludeAppointment
    }

    var targetNodes []Node
    for _, node := range nodes {
        // 1. 硬排除: Failed/Deleting/Skipped (不可配置，安全约束)
        if f.isExcluded(ctx, node, execCtx) {
            continue
        }

        // 2. 角色过滤
        if nf != nil && len(nf.Roles) > 0 {
            if !slices.Contains(nf.Roles, node.Role) {
                continue
            }
        }

        // 3. 标签过滤 (等值匹配)
        if nf != nil && len(nf.MatchLabels) > 0 {
            if !matchLabels(node.Labels, nf.MatchLabels) {
                continue
            }
        }

        // 4. 预约节点排除
        if excludeAppointment && f.isAppointmentNode(node, execCtx) {
            continue
        }

        // 5. per-node 幂等
        if skipCompleted && f.isAlreadyAtTarget(ctx, node, cv, execCtx) {
            continue
        }

        targetNodes = append(targetNodes, node)
    }

    return targetNodes, nil
}

func matchLabels(nodeLabels, selector map[string]string) bool {
    for k, v := range selector {
        if nodeLabels[k] != v {
            return false
        }
    }
    return true
}
```

### 5.1 isExcluded — 硬排除

```go
// isExcluded 检查节点是否应被硬排除 (不可配置)
func (f *BKENodeFilter) isExcluded(ctx context.Context, node Node, execCtx *ExecutionContext) bool {
    bkeNode := &confv1beta1.BKENode{}
    err := f.client.Get(ctx, types.NamespacedName{
        Namespace: execCtx.Cluster.Namespace,
        Name:      node.Name,
    }, bkeNode)
    if err != nil {
        return true
    }

    // 硬排除条件: 节点失败 / 节点删除中 / 节点需跳过
    if bkeNode.Status.StateCode&confv1beta1.NodeFailedFlag != 0 {
        return true
    }
    if bkeNode.Status.StateCode&confv1beta1.NodeDeletingFlag != 0 {
        return true
    }
    if bkeNode.Status.NeedSkip {
        return true
    }
    return false
}
```

### 5.2 五层过滤总结

| 层 | 检查 | 数据来源 | 可配置 |
|----|------|---------|--------|
| 1. 硬排除 | Failed/Deleting/NeedSkip | `BKENode.Status.StateCode` / `BKENode.Status.NeedSkip` | 否 |
| 2. 角色 | `node.Role` in `nf.Roles` | `Node.Role` ← `BKENode.Spec.Role` | 是 (`Roles`) |
| 3. 标签 | `node.Labels` 匹配 `nf.MatchLabels` | `Node.Labels` ← `BKENode.Spec.Labels` | 是 (`MatchLabels`) |
| 4. 预约排除 | 预约节点 | `BKECluster.Status` | 是 (`ExcludeAppointment`) |
| 5. 幂等 | 已安装到目标版本 | `NodeComponentStatuses` 或 `StateCode` | 是 (`SkipCompleted`) |

---

## 6. per-node 幂等设计

### 6.1 isAlreadyAtTarget — 双源读取

```go
// isAlreadyAtTarget 检查节点是否已安装到目标版本 (双源读取)
//
// 1. 优先读 NodeComponentStatuses (新模型)
// 2. 回退读 BKENode.StateCode (旧模型，向后兼容)
func (f *BKENodeFilter) isAlreadyAtTarget(
    ctx context.Context,
    node Node,
    cv *cvv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) bool {
    componentName := cv.Spec.Name
    targetVersion := cv.Spec.Version

    // 优先: 从 NodeComponentStatuses 读取 (新模型)
    if execCtx.Cluster.Status.NodeComponentStatuses != nil {
        if compStatuses, ok := execCtx.Cluster.Status.NodeComponentStatuses[componentName]; ok {
            if status, ok := compStatuses[node.IP]; ok {
                if status.Phase == "Installed" && status.Version == targetVersion {
                    return true  // 已安装到目标版本
                }
                if status.Phase == "Installing" {
                    return true  // 正在安装中 (避免并发)
                }
                return false  // 版本不匹配或失败 → 需要执行
            }
        }
    }

    // 回退: 从 BKENode.StateCode 读取 (旧模型)
    bkeNode := &confv1beta1.BKENode{}
    err := f.client.Get(ctx, types.NamespacedName{
        Namespace: execCtx.Cluster.Namespace,
        Name:      node.Name,
    }, bkeNode)
    if err != nil {
        return false
    }

    switch componentName {
    case "bkeagent":
        // bkeagent: NodeAgentPushedFlag 表示已推送
        if execCtx.VersionContext != nil && !execCtx.VersionContext.HasCurrent("bkeagent") {
            return bkeNode.Status.StateCode&confv1beta1.NodeAgentPushedFlag != 0
        }
        return false
    default:
        return false  // 其他组件: 旧模型无 per-node 状态，不过滤
    }
}
```

### 6.2 幂等场景

```txt
场景 1: 全新安装
  NodeComponentStatuses["containerd"] = nil (无记录)
  → NodeFilter: 不跳过
  → 执行安装 → MarkSuccess(version="v1.7.18", phase="Installed")

场景 2: 全部节点已安装 (组件级跳过)
  VersionContext.NeedsUpgrade("containerd") = false
  → Executor: 整个组件跳过，不进入 NodeFilter

场景 3: 部分节点已安装 (per-node 跳过)
  VersionContext.NeedsUpgrade("containerd") = true (集群级需要升级)
  NodeComponentStatuses["containerd"]["10.0.0.1"] = {Version:"v1.7.18", Phase:"Installed"}
  NodeComponentStatuses["containerd"]["10.0.0.2"] = {Version:"v1.7.15", Phase:"Installed"}
  NodeComponentStatuses["containerd"]["10.0.0.3"] = nil (未安装)
  → NodeFilter: 跳过 10.0.0.1 (已 v1.7.18) 和 10.0.0.2 (版本不同但已安装→升级)
  → 仅对 10.0.0.3 执行安装

场景 4: 失败重试
  NodeComponentStatuses["containerd"]["10.0.0.2"] = {Version:"v1.7.15", Phase:"Failed"}
  → NodeFilter: Phase=Failed → 不跳过 (需要重试)
```

---

## 7. 状态数据模型

### 7.1 NodeComponentStatuses

```go
// api/bkecommon/v1beta1/bkecluster_status.go 扩展

type BKEClusterStatus struct {
    // ... 现有字段 ...

    // 每节点每组件安装状态 (新增)
    // key 外层: 组件名 (如 "containerd")
    // key 内层: 节点 IP
    // 用于 Binary 组件的 per-node 幂等判断
    // YAML/Inline 组件不写入此字段 (它们是集群级部署)
    // +optional
    NodeComponentStatuses map[string]map[string]NodeComponentStatus `json:"nodeComponentStatuses,omitempty"`
}

// NodeComponentStatus 单个节点上单个组件的安装状态
type NodeComponentStatus struct {
    Version         string        `json:"version"`              // 已安装版本
    Phase           ComponentPhase `json:"phase"`               // 安装阶段
    LastUpdateTime  *metav1.Time  `json:"lastUpdateTime,omitempty"` // 最后更新时间
    Message         string        `json:"message,omitempty"`    // 错误信息 (Phase=Failed 时)
}
```

### 7.2 ComponentPhase 状态枚举

```go
type ComponentPhase string

const (
    ComponentPhasePending        ComponentPhase = "Pending"        // 等待安装
    ComponentPhaseInstalling     ComponentPhase = "Installing"     // 首次安装中
    ComponentPhaseUpgrading      ComponentPhase = "Upgrading"       // 升级中
    ComponentPhaseInstalled      ComponentPhase = "Installed"      // 安装/升级成功
    ComponentPhaseFailed         ComponentPhase = "Failed"          // 安装/升级失败
    ComponentPhaseRollingBack    ComponentPhase = "RollingBack"    // 正在回滚
    ComponentPhaseRolledBack     ComponentPhase = "RolledBack"      // 回滚成功
    ComponentPhasePartialSuccess ComponentPhase = "PartialSuccess" // 部分节点成功 (仅 Binary)
    ComponentPhaseTimeout        ComponentPhase = "Timeout"         // 超时
)
```

### 7.3 状态模型全景

```
BKECluster.Status
├── KubernetesVersion          ← 集群级 (现有，向后兼容)
├── ContainerdVersion          ← 集群级 (现有，向后兼容)
├── EtcdVersion                ← 集群级 (现有，向后兼容)
├── OpenFuyaoVersion           ← 集群级 (现有，向后兼容)
├── ClusterComponentStatuses   ← 组件级 (现有，所有类型共用)
│   └── [name] → ComponentLifecycleStatus { Phase, CurrentVersion, ... }
└── NodeComponentStatuses       ← per-node per-component (新增，仅 Binary)
    └── [componentName] → [nodeIP] → NodeComponentStatus { Version, Phase, ... }

BKENode.Status
├── State                      ← 节点整体状态 (现有)
├── StateCode                  ← 位标记 (现有，向后兼容)
│   ├── NodeAgentPushedFlag    ← bit 0
│   ├── NodeAgentReadyFlag     ← bit 1
│   ├── NodeEnvFlag            ← bit 2
│   └── ...
└── NeedSkip                   ← 跳过标记 (现有)
```

### 7.4 状态存储位置决策

| 维度 | BKECluster.Status.NodeComponentStatuses ★ | BKENode.Status.ComponentStatuses |
|------|--------------------------------------------|----------------------------------|
| 更新操作 | 1 次 Patch (整个 BKECluster) | N 次 Update (每个 BKENode) |
| 并发冲突 | 低 (单对象) | 高 (N 个对象同时更新) |
| 性能 | 优 | 差 (N 次 API 调用) |

---

## 8. 各组件类型集成方式

### 8.1 分层架构

```
┌───────────────────────────────────────────────────────────┐
│  DAG Scheduler                                            │
│  ├─ 拓扑排序 → 执行批次                                    │
│  ├─ shouldSkipComponent (已完成)                          │
│  ├─ componentNeedsUpgrade (版本)                          │
│  └─ shouldExecuteByCondition (KEP-18 条件)               │
├───────────────────────────────────────────────────────────┤
│  执行器层                                                  │
│                                                           │
│  BinaryComponentExecutor (KEP-16)                         │
│  ├─ 组件级幂等 (VersionContext)                            │
│  ├─ 获取节点 (NodeProvider.GetNodes)                      │
│  ├─ 节点级过滤 (NodeFilter.Filter) ★                     │
│  ├─ 按策略执行 (Rolling/Parallel/Batch)                   │
│  ├─ 每节点状态更新 (NodeStatusUpdater)                    │
│  └─ 委托 BinaryInstaller.Install()                        │
│                                                           │
│  InlineComponentExecutor                                  │
│  ├─ 组件级幂等 (VersionContext)                            │
│  ├─ 委托 InlineRunner.Execute()                           │
│  │  └─ Phase 内部 filterNodes() 可消费 NodeFilter ★      │
│  └─ (Phase 自行处理节点过滤)                              │
│                                                           │
│  YamlComponentExecutor                                    │
│  ├─ 组件级幂等 (VersionContext)                            │
│  ├─ NodeFilter 无效 (集群级部署)                           │
│  └─ 委托 YamlInstaller.Apply()                            │
├───────────────────────────────────────────────────────────┤
│  安装层                                                    │
│                                                           │
│  BinaryInstaller (SSH → 节点)                             │
│  YamlInstaller (kubectl apply → 集群)                    │
│  ※ 不感知节点状态、不做过滤、不更新状态                     │
├───────────────────────────────────────────────────────────┤
│  状态层                                                    │
│                                                           │
│  BKECluster.Status.NodeComponentStatuses (per-node)       │
│  BKECluster.Status.ClusterComponentStatuses (组件级)     │
│  BKENode.Status.StateCode (位标记，向后兼容)               │
└───────────────────────────────────────────────────────────┘
```

### 8.2 各组件类型的 NodeFilter 行为

| 组件类型 | NodeFilter 是否生效 | 说明 |
|---------|-------------------|------|
| **Binary** | ✅ 生效 | Executor 调用 `NodeFilter.Filter()` 精确选择目标节点 |
| **Inline** | ✅ 生效 (间接) | Phase 内部 `filterNodes()` 可消费 `NodeFilter`；也可通过 `WithComponentNodeFilter()` option 传入 |
| **YAML** | ❌ 无效 | 集群级部署 (`kubectl apply`)，节点调度由 K8s Scheduler 处理；通过 `values/nodeSelector` 自行处理 |
| **Helm** | ❌ 无效 | 同 YAML，集群级部署 |

### 8.3 幂等机制对比

| 组件类型 | 幂等粒度 | 判断机制 | 判断位置 |
|---------|---------|---------|---------|
| **Binary** | per-node per-component | `NodeComponentStatuses[name][ip].Version == target && Phase == "Installed"` | NodeFilter |
| **YAML** | 组件级 (集群) | `VersionContext.NeedsUpgrade(name)` | Executor |
| **Inline** | 自定义 | `Phase.NeedExecute()` 自行判断 (StateCode) | InlineRunner |

---

## 9. ComponentVersion YAML 示例

```yaml
# bkeagent 安装 (首次推送) — 按幂等过滤
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v2.7.0
spec:
  name: bkeagent
  type: binary
  version: "v2.7.0"
  nodeFilter:
    skipCompleted: true
    excludeAppointment: true

---
# bkeagent 升级 — 不过滤 (所有节点都执行)
spec:
  name: bkeagent
  type: binary
  version: "v2.7.0"
  nodeFilter:
    skipCompleted: false

---
# containerd — 仅安装到 compute 节点池
spec:
  name: containerd
  type: binary
  version: "v1.7.18"
  nodeFilter:
    matchLabels:
      node-pool: compute

---
# kubernetes-master — 仅 Master 节点
spec:
  name: kubernetes-master
  type: inline
  version: "v2.7.0"
  inline:
    handler: EnsureMasterInit
  nodeFilter:
    roles: ["master"]

---
# etcd — 仅 etcd 节点
spec:
  name: etcd
  type: binary
  version: "v3.5.21"
  nodeFilter:
    roles: ["etcd"]

---
# GPU 节点专用组件 — 按标签过滤
spec:
  name: nvidia-driver
  type: binary
  version: "v535.104.05"
  nodeFilter:
    matchLabels:
      gpu: "true"
      accelerator: nvidia

---
# coredns — 集群级部署, NodeFilter 无效
spec:
  name: coredns
  type: yaml
  version: "v1.11.1"
  # nodeFilter 不设置 (集群级部署, 由 K8s Scheduler 调度)
  yaml:
    applyStrategy: ServerSideApply
```

---

## 10. 与当前代码的等价性

| 组件 | 当前过滤函数 | NodeFilterSpec 配置 | 等价性 |
|------|------------|-------------------|--------|
| EnsureBKEAgent | `!NodeAgentPushedFlag` + Appointment 排除 | `skipCompleted: true, excludeAppointment: true` | ✅ |
| EnsureAgentUpgrade | 无过滤 | `skipCompleted: false` | ✅ |
| EnsureMasterUpgrade | `.Master()` | `roles: ["master"]` | ✅ |
| EnsureWorkerUpgrade | `.Worker()` | `roles: ["node"]` | ✅ |
| EnsureEtcdUpgrade | `.Etcd()` | `roles: ["etcd"]` | ✅ |
| EnsureContainerdUpgrade | `GetNeedUpgradeNodes` (版本比较) | `skipCompleted: true` (per-node 版本检查) | ✅ |
| GPU 组件 (新增) | 无对应 Phase | `matchLabels: {"gpu": "true"}` | ✅ 新能力 |

---

## 11. 兼容性设计

### 11.1 Feature Gate OFF (旧路径)

```txt
BKEClusterReconciler → Phase 框架 → EnsureBKEAgent / EnsureContainerdUpgrade / ...
                                       │
                                       ├─ 读取: BKENode.Status.StateCode (位标记)
                                       ├─ 写入: BKENode.Status.StateCode (位标记)
                                       └─ 写入: BKECluster.Status.*Version (集群级版本)

NodeComponentStatuses: 不写入、不读取
NodeFilter: 不消费 (Phase 自行用 filterNodes + StateCode)
```

**完全不变**，现有行为不受影响。

### 11.2 Feature Gate ON (新路径)

```txt
BKEClusterReconciler → DAG Scheduler → BinaryComponentExecutor
                                          │
                                          ├─ 读取: NodeComponentStatuses (per-node 幂等)
                                          ├─ 读取: BKENode.Status.StateCode (硬排除)
                                          ├─ NodeFilter.Filter (角色/标签/幂等)
                                          ├─ 写入: NodeComponentStatuses (per-node 状态)
                                          └─ 写入: ClusterComponentStatuses (组件级状态)
```

### 11.3 Feature Gate 首次开启 — 双源读取

```txt
NodeFilter.isAlreadyAtTarget():
  1. 优先读 NodeComponentStatuses (新模型)
     → 有记录: 按新模型判断
     → 无记录: 进入步骤 2

  2. 回退读 BKENode.Status.StateCode (旧模型)
     → bkeagent: NodeAgentPushedFlag → 视为已安装
     → 其他组件: 不过滤 (由组件级 VersionContext 处理)

  3. 懒初始化: 首次读取旧模型时，写入 NodeComponentStatuses
     → 后续读取走步骤 1，不再回退
```

### 11.4 兼容性矩阵

| 场景 | Feature Gate | 状态来源 | 行为 |
|------|-------------|---------|------|
| 全新集群安装 | OFF | StateCode | 旧路径，不变 |
| 全新集群安装 | ON | NodeComponentStatuses | 新路径 |
| 已有集群 + FG OFF→ON | ON (首次) | StateCode → NodeComponentStatuses (懒初始化) | 不重复安装 |
| 已有集群 + FG ON→OFF→ON | ON (再次) | StateCode (OFF 期间更新) → NodeComponentStatuses (重新初始化) | 不重复安装 |
| 混合模式 | 部分 ON | 各组件独立判断 | containerd 走新路径，bkeagent 走旧路径 |

---

## 12. 与 KEP-18 Condition 的关系

| 维度 | KEP-18 Condition | KEP-19 NodeFilter |
|------|-----------------|-------------------|
| **过滤粒度** | 组件级 (集群) | 节点级 (每节点) |
| **数据结构** | `ComponentVersion.Spec.Condition` (Go Template 字符串) | `ComponentVersion.Spec.NodeFilter` (声明式字段) |
| **求值时机** | DAG 执行时 (Scheduler 跳过链 C 层) | Executor 内部 (组件执行后、节点操作前) |
| **效果** | 组件整体跳过或执行 | 组件执行，但只在匹配的节点上操作 |
| **表达式** | Go Template (`{{ eq .Operation "scale" }}`) | 声明式 (`roles`, `matchLabels`, `skipCompleted`) |
| **适用类型** | 所有类型 | Binary + Inline (YAML/Helm 无效) |
| **执行顺序** | 先于 NodeFilter | Condition 通过后才到 NodeFilter |

**执行顺序**：

```
DAG Scheduler 跳过链:
  (A) shouldSkipComponent      → 已完成跳过
  (B) componentNeedsUpgrade    → 版本匹配跳过
  (C) shouldExecuteByCondition → 条件表达式跳过 (KEP-18)
  (D) executeComponent         → 执行器分发
        │
        ▼
  Executor 内部:
    (E) NodeFilter.Filter       → 节点级过滤 (KEP-19) ★
    (F) Rolling/Parallel/Batch  → 逐节点执行
```

---

## 13. 工作量评估

| 类别 | 模块 | 估算（人天） |
|------|------|------------|
| 开发 | `NodeFilterSpec` 类型定义 + `ComponentVersionSpec` 字段 | 0.5 |
| 开发 | `NodeFilter` 接口 + `BKENodeFilter` 实现 | 2 |
| 开发 | `NodeComponentStatuses` 状态模型 + `NodeComponentStatus` 类型 | 1 |
| 开发 | `NodeStatusUpdater` 接口 + `BKENodeStatusUpdater` 实现 | 1.5 |
| 开发 | `ComponentPhase` 枚举 + 状态转换逻辑 | 0.5 |
| 开发 | `BinaryComponentExecutor` 集成 NodeFilter | 1 |
| 开发 | `InlineComponentExecutor` / `filterNodes` 集成 NodeFilter | 1 |
| 测试 | NodeFilter 单元测试 + Executor 集成测试 + 兼容性测试 | 4 |
| **合计** | | **~11.5 人天** |

---

## 14. 风险与缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| Feature Gate 首次开启导致重复安装 | 节点重复安装组件 | 中 | NodeFilter 双源读取 + 懒初始化 (§11.3) |
| NodeComponentStatuses 并发冲突 | 状态更新失败 | 低 | 单对象 Patch (BKECluster.Status)，retry.RetryOnConflict |
| 角色匹配不准确 (复合角色 `master/node`) | 节点误过滤 | 中 | 匹配逻辑兼容复合角色，与现有 `Nodes.Master()` 行为一致 |
| Label 类型不匹配 (`[]Label` vs `map[string]string`) | 标签过滤失败 | 低 | `matchLabels` 函数处理类型转换 |
| YAML/Helm 组件误设 NodeFilter | 配置无效但不报错 | 低 | Admission Webhook 校验: YAML/Helm 类型设置 NodeFilter 时拒绝或警告 |

---

## 附录

### A. 参考文档

1. [KEP-5 声明式升级框架](kep5/kep5.md)
2. [KEP-16 二进制组件设计](kep16-binary-component-design.md) — 原始 NodeFilter 设计来源
3. [KEP-18 ComponentVersion 执行时条件过滤](kep18-component-condition-filter-design.md) — 集群级条件过滤，与本 KEP 互补
4. [KEP-10 安装流程声明式设计](kep10-install-components-declarative-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **NodeFilter** | `ComponentVersion.Spec.NodeFilter`，声明式节点过滤策略（角色/标签/幂等/预约排除） |
| **NodeFilterSpec** | 节点过滤配置类型，包含 Roles/MatchLabels/SkipCompleted/ExcludeAppointment |
| **BKENodeFilter** | NodeFilter 接口的默认实现，五层过滤（硬排除→角色→标签→预约→幂等） |
| **NodeComponentStatuses** | `BKECluster.Status` 中的 per-node per-component 状态 map，用于幂等判断 |
| **NodeStatusUpdater** | 节点状态更新接口，在执行前后更新 NodeComponentStatuses |
| **双源读取** | isAlreadyAtTarget 优先读 NodeComponentStatuses，回退读 StateCode，用于 Feature Gate 迁移 |
| **懒初始化** | 首次读旧模型 (StateCode) 时，同步写入新模型 (NodeComponentStatuses)，后续不再回退 |

---

**文档版本**: v1.0
**维护者**: openFuyao Team

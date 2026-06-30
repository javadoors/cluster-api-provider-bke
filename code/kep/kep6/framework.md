# 从编排层、执行器层与每节点、每组件记录状态，分层解耦，设计完整的安装、升级状态记录与冥等性、兼容性设计
Now I have a complete picture. Let me present the comprehensive design.

---

## 完整设计：分层状态模型、幂等性与兼容性

### 1. 问题分析

#### 1.1 当前 KEP-6 设计中的状态缺口

当前 `BinaryComponentExecutor.ExecuteComponent()` (9.4.3.1) 存在三个状态相关缺口：

| 缺口 | 当前设计 | 应有行为 |
|------|---------|---------|
| **节点过滤** | `NodeProvider.GetNodes()` 返回全部节点，无过滤 | 应排除 Failed/Deleting/Skipped/已完成节点 |
| **per-node 幂等** | `VersionContext.HasCurrent(name)` 是组件级判断 | 应支持 per-node 判断（node1 已升级，node2 未升级） |
| **状态回写** | 无状态回写逻辑 | 应在安装成功/失败后更新 per-node per-component 状态 |

#### 1.2 当前代码 vs KEP-6 的状态模型对比

| 维度 | 当前代码 (Phase 模型) | KEP-6 设计 (DAG 模型) |
|------|----------------------|---------------------|
| 状态存储 | `BKENode.Status.StateCode` (位标记) | `BKECluster.Status.ComponentStatuses` (组件级，无 per-node) |
| 幂等判断 | `StateCode & NodeAgentPushedFlag` | `VersionContext.NeedsUpgrade(name)` (组件级) |
| per-node 粒度 | ✅ 有 (每个 BKENode 独立 StateCode) | ❌ 缺失 |
| per-node 版本 | ❌ 无 (只有集群级版本号) | ❌ 缺失 |
| 状态更新 | 三层机制 (内存→API→集群同步) | 未设计 |

#### 1.3 各组件节点过滤逻辑差异

| 组件 | 当前过滤函数 | 过滤条件 | 版本来源 |
|------|------------|---------|---------|
| bkeagent (安装) | `GetNeedPushAgentNodesWithBKENodes` | `!NodeAgentPushedFlag` | 无版本比较 |
| bkeagent (升级) | **无过滤** | 全部节点 | `AddonStatus[].Version` |
| containerd | `GetNeedUpgradeNodesWithBKENodes` | `OpenFuyaoVersion` 比较 | `Status.OpenFuyaoVersion` |
| kubernetes | `GetNeedUpgradeK8sNodes` + 角色过滤 | `KubernetesVersion` 比较 | `Status.KubernetesVersion` |
| etcd | `filterUpgradeableNodes` + `.Etcd()` | `EtcdVersion` 比较 | `Status.EtcdVersion` |

**结论**：过滤逻辑因组件而异，不能硬编码到 BinaryInstaller 或 BinaryComponentExecutor 中。

---

### 2. 分层架构设计

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          编排层 (Orchestration Layer)                             │
│                                                                                  │
│  DAG Scheduler                                                                   │
│  ├─ 拓扑排序 → 执行批次                                                          │
│  ├─ 批次间串行、批次内并行                                                       │
│  └─ FailurePolicy (FailFast/Continue/Rollback)                                  │
│                                                                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                          执行器层 (Executor Layer)                                │
│                                                                                  │
│  BinaryComponentExecutor                                                         │
│  ├─ 加载 ComponentVersion                                                       │
│  ├─ 组件级幂等判断 (VersionContext.NeedsUpgrade)                                │
│  ├─ 获取节点列表 (NodeProvider.GetNodes)                                        │
│  ├─ 节点级过滤 (NodeFilter.Filter)              ← 新增接口                      │
│  ├─ 按策略执行 (Rolling/Parallel/Batch)                                          │
│  ├─ 每节点状态更新 (NodeStatusUpdater)          ← 新增接口                      │
│  └─ 委托 BinaryInstaller.Install()                                              │
│                                                                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                          安装层 (Installer Layer)                                 │
│                                                                                  │
│  BinaryInstaller                                                                 │
│  ├─ SSH 发现架构                                                                 │
│  ├─ 下载制品 + 校验                                                              │
│  ├─ 渲染脚本/配置                                                                │
│  ├─ SSH 上传 + 执行                                                              │
│  └─ 健康检查                                                                     │
│  ※ 不感知节点状态、不做过滤、不更新状态                                         │
│                                                                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                          状态层 (State Layer)                                     │
│                                                                                  │
│  BKECluster.Status.NodeComponentStatuses (per-node per-component)  ← 新增        │
│  BKENode.Status.StateCode (per-node 位标记，向后兼容)                            │
│  BKECluster.Status.*Version (集群级版本，向后兼容)                               │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

### 3. 状态数据模型

#### 3.1 新增 `NodeComponentStatuses` 字段

```go
// api/bkecommon/v1beta1/bkecluster_status.go 扩展

type BKEClusterStatus struct {
    // ... 现有字段保持不变 ...

    // 每节点每组件安装状态 (KEP-6 新增)
    // key 外层: 组件名 (如 "containerd", "bkeagent")
    // key 内层: 节点 IP
    // 用于 Binary 组件的 per-node 幂等判断和状态追踪
    // Helm/YAML/Inline 组件不写入此字段 (它们是集群级部署)
    NodeComponentStatuses map[string]map[string]NodeComponentStatus `json:"nodeComponentStatuses,omitempty"`
}

// NodeComponentStatus 单个节点上单个组件的安装状态
type NodeComponentStatus struct {
    // 已安装版本 (如 "v1.7.18")
    Version string `json:"version"`

    // 安装阶段: Installing / Installed / Failed
    Phase string `json:"phase"`

    // 最后更新时间
    LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`

    // 错误信息 (Phase=Failed 时)
    Message string `json:"message,omitempty"`
}
```

**设计决策 — 为什么放在 BKECluster.Status 而非 BKENode.Status**：

| 维度 | BKECluster.Status.NodeComponentStatuses | BKENode.Status.ComponentStatuses |
|------|----------------------------------------|----------------------------------|
| 更新操作 | 1 次 Patch (整个 BKECluster) | N 次 Update (每个 BKENode) |
| 并发冲突 | 低 (单对象) | 高 (N 个对象同时更新) |
| 查询模式 | "containerd 在哪些节点已安装？" → 直接查 | "node1 上装了哪些组件？" → 直接查 |
| 与现有模型一致性 | 与 `ComponentStatuses` (组件级) 对称 | 需新增 BKENode 字段 |
| 性能 | ✅ 优 | ❌ 差 (N 次 API 调用) |

选择 BKECluster.Status 方案。查询 "node1 上 containerd 的状态" 通过 `NodeComponentStatuses["containerd"]["10.0.0.1"]` 两步 map 查找，性能可接受。

#### 3.2 状态模型全景

```
BKECluster.Status
├── KubernetesVersion          ← 集群级 (现有，向后兼容)
├── ContainerdVersion          ← 集群级 (现有，向后兼容)
├── EtcdVersion                ← 集群级 (现有，向后兼容)
├── OpenFuyaoVersion           ← 集群级 (现有，向后兼容)
├── ComponentStatuses          ← 组件级 (18.4 节设计)
│   └── [name] → ComponentStatus { Version, Phase, Type, ... }
└── NodeComponentStatuses      ← per-node per-component (新增)
    └── [componentName] → [nodeIP] → NodeComponentStatus { Version, Phase, ... }

BKENode.Status
├── State                      ← 节点整体状态 (现有)
├── StateCode                  ← 位标记 (现有，向后兼容)
│   ├── NodeAgentPushedFlag    ← bit 0
│   ├── NodeAgentReadyFlag     ← bit 1
│   ├── NodeEnvFlag            ← bit 2
│   └── ...
├── Message                    ← 状态消息 (现有)
└── NeedSkip                   ← 跳过标记 (现有)
```

**职责分工**：

| 状态存储 | 谁写入 | 谁读取 | 用途 |
|---------|--------|--------|------|
| `BKECluster.Status.*Version` | 兼容层 (Feature Gate OFF) | VersionContext 构建 | 集群级版本判断 |
| `BKECluster.Status.ComponentStatuses` | ComponentExecutor (所有类型) | DAG 调度器、UI | 组件级状态展示 |
| `BKECluster.Status.NodeComponentStatuses` | BinaryComponentExecutor | NodeFilter、BinaryComponentExecutor | per-node 幂等判断 |
| `BKENode.Status.StateCode` | 兼容层 (Feature Gate OFF) | NodeFilter (兼容模式) | 向后兼容 |

---

### 4. NodeFilter 接口

#### 4.1 接口定义

```go
// pkg/dagexec/node_filter.go

// NodeFilter 节点过滤接口
//
// 设计思路 — 为什么不内置到 BinaryComponentExecutor:
// 1. 不同组件的过滤逻辑不同 (bkeagent 按位标记，containerd 按版本比较)
// 2. 过滤逻辑可能随组件类型演化，Executor 不应绑定特定实现
// 3. 测试时可注入 Mock Filter，独立测试 Executor 调度逻辑
//
// 设计思路 — 为什么不内置到 NodeProvider:
// NodeProvider 职责是"获取节点"，NodeFilter 职责是"过滤节点"
// 两者关注点不同: Provider 关心数据来源 (BKECluster/BKENode)，
// Filter 关心业务逻辑 (幂等判断、状态排除)
type NodeFilter interface {
    // Filter 返回需要执行操作的节点列表
    // 输入: 全部节点、组件版本、执行上下文
    // 输出: 需要操作的节点列表 (已排除无需操作的节点)
    Filter(ctx context.Context, nodes []Node, cv *configv1alpha1.ComponentVersion, execCtx *ExecutionContext) ([]Node, error)
}
```

#### 4.2 默认实现: BKENodeFilter

```go
// pkg/dagexec/bke_node_filter.go

// BKENodeFilter 默认节点过滤器
// 复用现有 BKENode 状态模型，同时支持新的 NodeComponentStatuses
type BKENodeFilter struct {
    client client.Client  // 读取 BKENode 和 BKECluster
}

// Filter 实现 NodeFilter 接口
func (f *BKENodeFilter) Filter(
    ctx context.Context,
    nodes []Node,
    cv *configv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) ([]Node, error) {
    var targetNodes []Node

    for _, node := range nodes {
        // 1. 硬排除: Failed/Deleting/Skipped (等价于当前 filterNodes 的硬排除)
        if f.isExcluded(ctx, node, execCtx) {
            continue
        }

        // 2. per-node 幂等判断
        if f.isAlreadyAtTarget(ctx, node, cv, execCtx) {
            continue
        }

        targetNodes = append(targetNodes, node)
    }

    return targetNodes, nil
}

// isExcluded 排除不需要操作的节点
func (f *BKENodeFilter) isExcluded(ctx context.Context, node Node, execCtx *ExecutionContext) bool {
    bkeNode := &configv1beta1.BKENode{}
    err := f.client.Get(ctx, types.NamespacedName{
        Namespace: execCtx.Cluster.Namespace,
        Name:      node.Name,
    }, bkeNode)
    if err != nil {
        return true // 获取失败则排除 (安全侧)
    }

    // 排除 Failed/Deleting/Skipped — 等价于当前 filterNodes 的硬排除
    if bkeNode.Status.StateCode&configv1beta1.NodeFailedFlag != 0 {
        return true
    }
    if bkeNode.Status.StateCode&configv1beta1.NodeDeletingFlag != 0 {
        return true
    }
    if bkeNode.Status.NeedSkip {
        return true
    }

    return false
}

// isAlreadyAtTarget per-node 幂等判断
// 优先使用 NodeComponentStatuses (新模型)，回退到 StateCode (旧模型)
func (f *BKENodeFilter) isAlreadyAtTarget(
    ctx context.Context,
    node Node,
    cv *configv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) bool {
    componentName := cv.Spec.Name
    targetVersion := cv.Spec.Version

    // 优先: 从 NodeComponentStatuses 读取 (新模型)
    if execCtx.Cluster.Status.NodeComponentStatuses != nil {
        if compStatuses, ok := execCtx.Cluster.Status.NodeComponentStatuses[componentName]; ok {
            if status, ok := compStatuses[node.IP]; ok {
                // 已在目标版本且安装成功 → 跳过
                if status.Phase == "Installed" && status.Version == targetVersion {
                    return true
                }
                // 正在安装中 → 跳过 (避免并发冲突)
                if status.Phase == "Installing" {
                    return true
                }
                // Phase == Failed → 不跳过 (需要重试)
                return false
            }
        }
    }

    // 回退: 从 BKENode.StateCode 读取 (旧模型，向后兼容)
    // 仅用于 Feature Gate 首次开启时的过渡期
    bkeNode := &configv1beta1.BKENode{}
    err := f.client.Get(ctx, types.NamespacedName{
        Namespace: execCtx.Cluster.Namespace,
        Name:      node.Name,
    }, bkeNode)
    if err != nil {
        return false // 获取失败则不跳过 (安全侧: 宁可重复执行)
    }

    switch componentName {
    case "bkeagent":
        // bkeagent 安装: 检查 NodeAgentPushedFlag
        // 注意: bkeagent 升级不过滤 (当前代码 EnsureAgentUpgrade 无过滤)
        if execCtx.VersionContext != nil && !execCtx.VersionContext.HasCurrent("bkeagent") {
            // 首次安装场景: 已推送则跳过
            return bkeNode.Status.StateCode&configv1beta1.NodeAgentPushedFlag != 0
        }
        // 升级场景: 不过滤 (与当前 EnsureAgentUpgrade 行为一致)
        return false
    case "containerd":
        // containerd: 通过集群级版本判断 (当前代码 GetNeedUpgradeNodes 按 OpenFuyaoVersion 比较)
        // 由组件级 VersionContext.NeedsUpgrade 处理，per-node 不额外过滤
        return false
    default:
        return false
    }
}
```

#### 4.3 NodeFilter 与组件类型的对应关系

| 组件类型 | NodeFilter 行为 | 过滤依据 |
|---------|----------------|---------|
| **Binary (bkeagent 安装)** | 排除 + `!NodeAgentPushedFlag` + `NodeComponentStatuses` | per-node |
| **Binary (bkeagent 升级)** | 仅硬排除 (Failed/Deleting/Skipped) | 无 per-node 幂等 |
| **Binary (containerd)** | 仅硬排除 | 组件级 `VersionContext.NeedsUpgrade` |
| **Helm** | **不使用 NodeFilter** | 集群级部署，无节点概念 |
| **YAML** | **不使用 NodeFilter** | 集群级部署，无节点概念 |
| **Inline** | **不使用 NodeFilter** | 无节点概念 |

---

### 5. NodeStatusUpdater 接口

#### 5.1 接口定义

```go
// pkg/dagexec/node_status_updater.go

// NodeStatusUpdater 节点状态更新接口
//
// 设计思路 — 为什么不直接在 Executor 中更新:
// 1. 状态更新涉及 BKECluster.Status 和 BKENode.Status 两个对象
// 2. 需要处理并发冲突 (retry.RetryOnConflict)
// 3. 需要处理新旧状态模型的兼容 (NodeComponentStatuses vs StateCode)
// 4. 测试时可注入 Mock，独立测试 Executor 逻辑
type NodeStatusUpdater interface {
    // MarkPending 标记节点开始安装
    MarkPending(ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, componentName string) error

    // MarkSuccess 标记节点安装成功
    MarkSuccess(ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, componentName string, version string) error

    // MarkFailed 标记节点安装失败
    MarkFailed(ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, componentName string, err error) error
}
```

#### 5.2 默认实现: BKENodeStatusUpdater

```go
// pkg/dagexec/bke_node_status_updater.go

// BKENodeStatusUpdater 默认状态更新器
// 同时更新 NodeComponentStatuses (新模型) 和 StateCode (旧模型，向后兼容)
type BKENodeStatusUpdater struct {
    client client.Client
}

// MarkPending 标记节点开始安装
func (u *BKENodeStatusUpdater) MarkPending(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
) error {
    return u.updateNodeComponentStatus(ctx, cluster, nodeIP, componentName, NodeComponentStatus{
        Phase:          "Installing",
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

// MarkSuccess 标记节点安装成功
func (u *BKENodeStatusUpdater) MarkSuccess(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
    version string,
) error {
    return u.updateNodeComponentStatus(ctx, cluster, nodeIP, componentName, NodeComponentStatus{
        Version:        version,
        Phase:          "Installed",
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

// MarkFailed 标记节点安装失败
func (u *BKENodeStatusUpdater) MarkFailed(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
    installErr error,
) error {
    // 保留原版本号 (不覆盖)
    existingVersion := ""
    if cluster.Status.NodeComponentStatuses != nil {
        if compStatuses, ok := cluster.Status.NodeComponentStatuses[componentName]; ok {
            if status, ok := compStatuses[nodeIP]; ok {
                existingVersion = status.Version
            }
        }
    }

    return u.updateNodeComponentStatus(ctx, cluster, nodeIP, componentName, NodeComponentStatus{
        Version:        existingVersion,
        Phase:          "Failed",
        Message:        installErr.Error(),
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

// updateNodeComponentStatus 更新 NodeComponentStatuses (带冲突重试)
func (u *BKENodeStatusUpdater) updateNodeComponentStatus(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
    status NodeComponentStatus,
) error {
    return retry.RetryOnConflict(retry.DefaultRetry, func() error {
        // 获取最新的 BKECluster
        latest := &bkev1beta1.BKECluster{}
        if err := u.client.Get(ctx, types.NamespacedName{
            Namespace: cluster.Namespace,
            Name:      cluster.Name,
        }, latest); err != nil {
            return err
        }

        // 初始化 map
        if latest.Status.NodeComponentStatuses == nil {
            latest.Status.NodeComponentStatuses = make(map[string]map[string]NodeComponentStatus)
        }
        if latest.Status.NodeComponentStatuses[componentName] == nil {
            latest.Status.NodeComponentStatuses[componentName] = make(map[string]NodeComponentStatus)
        }

        // 更新状态
        latest.Status.NodeComponentStatuses[componentName][nodeIP] = status

        // Patch 更新 (仅更新 status 子资源)
        return u.client.Status().Update(ctx, latest)
    })
}
```

---

### 6. 完整执行流程

#### 6.1 BinaryComponentExecutor 完整流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    BinaryComponentExecutor 完整流程                               │
└─────────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────┐
  │  1. 加载 ComponentVersion            │
  │  cv = cvStore.GetComponentVersion()  │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  2. 组件级幂等判断                   │
  │  VersionContext.NeedsUpgrade(name)   │
  │  → false: 整个组件跳过              │
  └──────────┬───────────────────────────┘
             │ true (需要执行)
             ▼
  ┌──────────────────────────────────────┐
  │  3. 获取全部节点                     │
  │  allNodes = NodeProvider.GetNodes()  │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  4. 节点级过滤                       │
  │  targetNodes = NodeFilter.Filter()   │
  │  ├─ 排除 Failed/Deleting/Skipped    │
  │  ├─ 排除 per-node 已完成            │
  │  └─ 排除 per-node 安装中            │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
    有节点     无节点
        │         │
        │         ▼
        │  ┌────────────────────────┐
        │  │  跳过 (全部已完成)     │
        │  │  return nil            │
        │  └────────────────────────┘
        │
        ▼
  ┌──────────────────────────────────────┐
  │  5. 按策略执行                       │
  │  switch strategy.Mode                │
  └──────────┬───────────────────────────┘
             │
        ┌────┼────┐
        │    │    │
     Rolling Parallel Batch
        │    │    │
        └────┼────┘
             │
             ▼ (对每个目标节点)
  ┌──────────────────────────────────────┐
  │  6. 标记安装中                       │
  │  NodeStatusUpdater.MarkPending()     │
  │  → NodeComponentStatuses[name][ip]   │
  │    = { Phase: "Installing" }         │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  7. 执行安装                         │
  │  BinaryInstaller.Install(opts)       │
  │  ├─ SSH 发现架构                     │
  │  ├─ 下载制品 + 校验                  │
  │  ├─ 渲染脚本/配置                    │
  │  ├─ SSH 上传 + 执行                  │
  │  └─ 健康检查                         │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
     成功       失败
        │         │
        ▼         ▼
  ┌──────────┐  ┌────────────────────────┐
  │ 8a. 成功 │  │ 8b. 失败               │
  │ MarkSucc │  │ MarkFailed             │
  │ Version  │  │ Message = err.Error()  │
  │ Installed│  │                        │
  └────┬─────┘  └──────────┬─────────────┘
       │                   │
       │              ┌────┴────┐
       │              │         │
       │           FailFast  Continue
       │              │         │
       │              ▼         ▼
       │         return err  继续下一节点
       │
       ▼
  ┌──────────────────────────────────────┐
  │  9. 全部节点完成                     │
  │  return nil                          │
  └──────────────────────────────────────┘
```

#### 6.2 HelmComponentExecutor 完整流程

```
  ┌──────────────────────────────────────┐
  │  1. 加载 ComponentVersion            │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  2. 组件级幂等判断                   │
  │  VersionContext.NeedsUpgrade(name)   │
  │  → false: 跳过                      │
  └──────────┬───────────────────────────┘
             │ true
             ▼
  ┌──────────────────────────────────────┐
  │  3. 确定操作类型                     │
  │  Strategy.Mode 优先                  │
  │  为空时: HasCurrent → Upgrade        │
  │          否则    → Install            │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  4. 更新组件状态                     │
  │  ComponentStatuses[name]             │
  │  = { Phase: "Installing", Type: helm }│
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  5. HelmInstaller.Install()          │
  │  ├─ 获取 Chart                       │
  │  ├─ 渲染 Values                      │
  │  ├─ helm install/upgrade             │
  │  └─ 健康检查                         │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
     成功       失败
        │         │
        ▼         ▼
  ┌──────────┐  ┌────────────────────────┐
  │ MarkSucc │  │ FailurePolicy=Rollback │
  │ Version  │  │ → helm rollback        │
  │ Installed│  │ MarkFailed             │
  └──────────┘  └────────────────────────┘
```

**Helm 不需要 NodeFilter 和 NodeStatusUpdater**：Helm 部署到集群而非单节点，无 per-node 状态。组件级状态通过 `ComponentStatuses[name]` 记录。

#### 6.3 YamlComponentExecutor 完整流程

```
  ┌──────────────────────────────────────┐
  │  1. 加载 ComponentVersion            │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  2. 组件级幂等判断                   │
  │  VersionContext.NeedsUpgrade(name)   │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  3. YamlInstaller.Apply()            │
  │  ├─ 下载清单 (ManifestDownloader)    │
  │  ├─ 解析多文档 YAML                  │
  │  ├─ ApplyComponent (SSA/Replace)     │
  │  └─ 健康检查                         │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
     成功       失败
        │         │
        ▼         ▼
  ┌──────────┐  ┌──────────┐
  │ MarkSucc │  │MarkFailed│
  └──────────┘  └──────────┘
```

#### 6.4 InlineComponentExecutor 完整流程

```
  ┌──────────────────────────────────────┐
  │  1. 获取 handler/version             │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  2. InlineRunner.Execute()           │
  │  (无幂等判断 — 由 Phase 自身决定)   │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
     成功       失败
        │         │
        ▼         ▼
  ┌──────────┐  ┌──────────┐
  │ return   │  │return err│
  │ nil      │  │          │
  └──────────┘  └──────────┘
```

**Inline 不需要状态记录**：Inline Phase 的幂等性由 `NeedExecute()` 自行判断，不依赖外部状态。

---

### 7. 状态转换表

| 当前 Phase | 事件 | 新 Phase | Version | 触发者 |
|-----------|------|---------|---------|--------|
| (不存在) | 开始安装 | `Installing` | "" | Executor (MarkPending) |
| `Installing` | 安装成功 | `Installed` | targetVersion | Executor (MarkSuccess) |
| `Installing` | 安装失败 | `Failed` | "" (保留原版本) | Executor (MarkFailed) |
| `Installed` | 目标版本变更 | `Installing` | "" (保留旧版本) | Executor (MarkPending) |
| `Failed` | 重试 | `Installing` | 保留旧版本 | Executor (MarkPending) |
| `Failed` | 组件移除 | (删除条目) | — | Executor |
| `Installed` | 版本相同 | (跳过) | — | NodeFilter |

---

### 8. 幂等性设计

#### 8.1 各组件类型的幂等机制

| 组件类型 | 幂等粒度 | 判断机制 | 判断位置 |
|---------|---------|---------|---------|
| **Binary** | per-node per-component | `NodeComponentStatuses[name][ip].Version == target && Phase == "Installed"` | NodeFilter |
| **Helm** | 组件级 (集群) | `VersionContext.NeedsUpgrade(name)` → `Current[name] != Target[name]` | Executor |
| **YAML** | 组件级 (集群) | `VersionContext.NeedsUpgrade(name)` | Executor |
| **Inline** | 自定义 | `Phase.NeedExecute()` 自行判断 | InlineRunner |

#### 8.2 Binary 幂等的三种场景

```
场景 1: 全新安装
  NodeComponentStatuses["containerd"] = nil (无记录)
  → NodeFilter: 不跳过
  → 执行安装
  → MarkSuccess: version = "v1.7.18", phase = "Installed"

场景 2: 全部节点已安装 (组件级跳过)
  VersionContext.NeedsUpgrade("containerd") = false
  (Current["containerd"] == Target["containerd"])
  → Executor: 整个组件跳过，不进入 NodeFilter

场景 3: 部分节点已安装 (per-node 跳过)
  VersionContext.NeedsUpgrade("containerd") = true (集群级需要升级)
  NodeComponentStatuses["containerd"]["10.0.0.1"] = { Version: "v1.7.18", Phase: "Installed" }
  NodeComponentStatuses["containerd"]["10.0.0.2"] = { Version: "v1.7.15", Phase: "Installed" }
  NodeComponentStatuses["containerd"]["10.0.0.3"] = nil (未安装/中断)
  → NodeFilter: 跳过 10.0.0.1 和 10.0.0.2，仅对 10.0.0.3 执行
```

**场景 3 的关键**：`VersionContext` 是组件级的，无法区分 per-node 状态。当 Rolling 升级中途中断时，部分节点已升级、部分未升级。此时 `VersionContext.NeedsUpgrade` 返回 true（因为集群级 Current != Target），但 NodeFilter 可以精确跳过已升级的节点。

#### 8.3 失败重试的幂等性

```
场景: 节点 10.0.0.2 安装失败
  NodeComponentStatuses["containerd"]["10.0.0.2"] = { Version: "v1.7.15", Phase: "Failed" }

下次 Reconcile:
  VersionContext.NeedsUpgrade("containerd") = true
  NodeFilter:
    10.0.0.1: Phase=Installed, Version=v1.7.18 == target → 跳过
    10.0.0.2: Phase=Failed → 不跳过 (需要重试)
    10.0.0.3: 无记录 → 不跳过
  → 仅对 10.0.0.2 和 10.0.0.3 执行
```

---

### 9. 兼容性设计

#### 9.1 Feature Gate OFF (旧路径)

```
BKEClusterReconciler → Phase 框架 → EnsureBKEAgent / EnsureContainerdUpgrade / ...
                                       │
                                       ├─ 读取: BKENode.Status.StateCode (位标记)
                                       ├─ 写入: BKENode.Status.StateCode (位标记)
                                       └─ 写入: BKECluster.Status.*Version (集群级版本)

NodeComponentStatuses: 不写入、不读取
ComponentStatuses: 不写入、不读取
```

**完全不变**，现有行为不受影响。

#### 9.2 Feature Gate ON (新路径)

```
BKEClusterReconciler → DAG Scheduler → BinaryComponentExecutor / HelmComponentExecutor / ...
                                          │
                                          ├─ 读取: NodeComponentStatuses (per-node 幂等)
                                          ├─ 读取: BKENode.Status.StateCode (硬排除)
                                          ├─ 写入: NodeComponentStatuses (per-node 状态)
                                          └─ 写入: ComponentStatuses (组件级状态)
```

#### 9.3 迁移策略：Feature Gate 首次开启

**问题**：Feature Gate 首次开启时，`NodeComponentStatuses` 为空，但节点上已安装了组件（通过旧路径的 StateCode 位标记记录）。如果不处理，NodeFilter 会对所有节点重新安装。

**方案**：NodeFilter 的双源读取（已在 4.2 节实现）

```
NodeFilter.isAlreadyAtTarget():
  1. 优先读 NodeComponentStatuses (新模型)
     → 有记录: 按新模型判断
     → 无记录: 进入步骤 2

  2. 回退读 BKENode.Status.StateCode (旧模型)
     → bkeagent: NodeAgentPushedFlag → 视为已安装
     → containerd: 不过滤 (由组件级 VersionContext 处理)
     → 其他: 不过滤

  3. 懒初始化: 首次读取旧模型时，写入 NodeComponentStatuses
     → 后续读取走步骤 1，不再回退
```

**懒初始化流程**：

```
NodeFilter.isAlreadyAtTarget() 回退到 StateCode:
  │
  ├─ bkeagent: NodeAgentPushedFlag 已设置
  │   → 从 BKECluster.Status.AddonStatus 读取当前版本
  │   → 写入 NodeComponentStatuses["bkeagent"][nodeIP]
  │     = { Version: addonVersion, Phase: "Installed" }
  │   → 返回 true (跳过)
  │
  └─ 其他组件: 不初始化 (由组件级 VersionContext 处理)
```

#### 9.4 回滚策略：Feature Gate 从 ON 切回 OFF

```
Feature Gate ON → OFF:
  NodeComponentStatuses 保留在 BKECluster.Status 中 (不删除)
  旧路径不读取 NodeComponentStatuses (不受影响)
  旧路径继续读写 BKENode.Status.StateCode (行为不变)

Feature Gate OFF → ON (再次开启):
  NodeComponentStatuses 可能不是最新的 (OFF 期间旧路径更新了 StateCode 但未更新 NodeComponentStatuses)
  → NodeFilter 检测到 NodeComponentStatuses 与 StateCode 不一致时，以 StateCode 为准并重新初始化
```

#### 9.5 兼容性保证矩阵

| 场景 | Feature Gate | 状态来源 | 行为 |
|------|-------------|---------|------|
| 全新集群安装 | OFF | StateCode | 旧路径，不变 |
| 全新集群安装 | ON | NodeComponentStatuses | 新路径 |
| 已有集群 + FG OFF→ON | ON (首次) | StateCode → NodeComponentStatuses (懒初始化) | 不重复安装 |
| 已有集群 + FG ON→OFF→ON | ON (再次) | StateCode (OFF 期间更新) → NodeComponentStatuses (重新初始化) | 不重复安装 |
| 混合模式 (containerd ON, bkeagent OFF) | 部分 ON | 各组件独立判断 | containerd 走新路径，bkeagent 走旧路径 |

---

### 10. 文档更新计划

#### 10.1 新增章节

在第 9 章 (DAG 集成详细设计) 中新增 `### 9.5 状态模型与幂等性设计`，包含上述 10 个小节的内容。

#### 10.2 更新章节

| 章节 | 更新内容 |
|------|---------|
| **9.4.1 ExecutionContext** | 新增 `StatusUpdater NodeStatusUpdater` 字段 |
| **9.4.2 NodeProvider** | 补充说明: 职责是"获取节点"，不包含过滤逻辑 |
| **9.4.3 ComponentExecutor** | 分层表增加 NodeFilter 和 NodeStatusUpdater 列 |
| **9.4.3.1 BinaryComponentExecutor** | 重构 ExecuteComponent 代码，集成 NodeFilter + NodeStatusUpdater |
| **9.1.4 Scheduler 初始化** | Config 增加 NodeFilter 和 NodeStatusUpdater 注入 |
| **3.1 ComponentVersion 类型定义** | BKEClusterStatus 扩展 NodeComponentStatuses 字段说明 |
| **目录** | 增加 9.5 条目 |

#### 10.3 不修改的章节

| 章节 | 原因 |
|------|------|
| 4.x BinaryInstaller | 安装层不变，不感知状态 |
| 5.x HelmInstaller | Helm 不需要 NodeFilter/NodeStatusUpdater |
| 6.x YamlInstaller | YAML 不需要 NodeFilter/NodeStatusUpdater |
| 8.x 模板变量系统 | 不涉及状态 |
| 12.x 迁移策略 | 9.5 节已包含兼容性设计，12 章补充 Feature Gate 细节 |

---

### 11. 设计决策总结

| 决策 | 选择 | 理由 |
|------|------|------|
| per-node 状态存储位置 | `BKECluster.Status.NodeComponentStatuses` | 1 次 Patch 更新，避免 N 次 BKENode 更新的并发冲突 |
| NodeFilter 归属 | 独立接口，由 Executor 持有 | 过滤逻辑因组件而异，不应内置到 Installer 或 NodeProvider |
| NodeStatusUpdater 归属 | 独立接口，由 Executor 持有 | 状态更新是编排层职责，不应下沉到安装层 |
| 新旧状态模型兼容 | NodeFilter 双源读取 + 懒初始化 | 避免 Feature Gate 首次开启时的重复安装 |
| Helm/YAML 状态记录 | 仅组件级 (`ComponentStatuses`) | 集群级部署，无 per-node 概念 |
| Inline 状态记录 | 不记录 | 由 Phase 自身 `NeedExecute()` 判断幂等性 |

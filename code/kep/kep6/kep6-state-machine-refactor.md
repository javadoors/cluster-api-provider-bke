# KEP-6 状态机重构方案

**文档版本**: v1.0  
**状态**: Draft  
**创建日期**: 2026-07-13  
**依赖**: [kep6-state-machine-v1.md](./kep6-state-machine-v1.md), [kep6-state-machine-v3.md](./kep6-state-machine-v3.md)

---

## 目录

1. [概述](#1-概述)
   - 1.1 重构目标
   - 1.2 核心原则
   - 1.3 设计决策
2. [问题分析](#2-问题分析)
   - 2.1 v1 的 6 个核心问题
   - 2.2 影响评估
3. [解决方案](#3-解决方案)
   - 3.1 v3 状态模型
   - 3.2 状态映射关系
   - 3.3 聚合逻辑统一
   - 3.4 组件层状态
   - 3.5 分层重试机制
4. [迁移方案](#4-迁移方案)
   - 4.1 平滑迁移策略
   - 4.2 Feature Gate 设计
   - 4.3 双写机制
5. [兼容性设计](#5-兼容性设计)
   - 5.1 老字段保留策略
   - 5.2 派生视图实现
   - 5.3 渐进式废弃时间线
6. [实施计划](#6-实施计划)
   - 6.1 分阶段实施步骤
   - 6.2 时间线
   - 6.3 测试计划
7. [风险管理](#7-风险管理)
   - 7.1 回滚方案
   - 7.2 监控和告警
8. [迁移指南](#8-迁移指南)
   - 8.1 给外部消费者的迁移指南
   - 8.2 常见问题

---

## 1. 概述

### 1.1 重构目标

本次重构旨在解决 v1 状态机设计的 6 个核心问题，通过引入 v3 状态模型实现：

- **简化状态模型**：将状态值从 73+ 减少到 22 个（减少 70%）
- **统一聚合入口**：将分散的聚合逻辑集中到 StateAggregator
- **补充组件层状态**：为 containerd、coredns 等组件增加独立状态追踪
- **完善重试机制**：增加三层重试机制（Command 级 + 集群级 + 人工介入）
- **向后兼容**：通过 Feature Gate 和双写机制实现零停机迁移

### 1.2 核心原则

**单一数据源（Single Source of Truth）**

- `LifecyclePhase` 作为集群和节点的唯一状态源
- 旧字段（Phase/ClusterStatus/ClusterHealthState/State）作为**派生视图**，从 LifecyclePhase 计算得出
- 新字段写入后，自动派生旧字段，保证一致性

### 1.3 设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 状态模型 | v3 三层状态机 | 已完整解决 v1 的所有问题 |
| 迁移方式 | 平滑迁移（Feature Gate + 双写） | 零停机，可随时回滚 |
| 老字段策略 | 保留作为派生视图，渐进式废弃 | 保护外部依赖，降低迁移风险 |
| 实施节奏 | 分 6 个阶段，18 个月完成 | 充分测试，逐步推广 |

---

## 2. 问题分析

### 2.1 v1 的 6 个核心问题

#### 问题 1：状态维度爆炸

**现状**：
- 集群层：6 个状态字段（Phase, ClusterStatus, ClusterHealthState, PhaseStatus, Conditions, DeclarativeUpgrade）
- 节点层：4 个状态字段（State, StateCode, Message, NeedSkip）
- 总计：11 个状态字段，73+ 个状态值

**影响**：
- 无法在单张图中完整展现状态机
- 理解和维护成本高
- 容易出现状态不一致

#### 问题 2：语义重叠严重

**现状**：
- `Phase` 和 `ClusterStatus` 高度重叠：
  - `Phase=InitControlPlane` 对应 `ClusterStatus=Initializing`
  - `Phase=UpgradeControlPlane` 对应 `ClusterStatus=Upgrading`
- `ClusterHealthState` 是 `ClusterStatus` 的子集 + 失败升级版：
  - `Deploying` vs `Initializing`
  - `Upgrading` vs `Upgrading`
  - `DeployFailed` vs `InitializationFailed`

**影响**：
- 三套状态由不同组件在不同时机更新
- 存在不一致风险
- 增加认知负担

#### 问题 3：聚合逻辑分散

**现状**：
- PhaseFlow post-hook 设置 ClusterStatus（`phase_flow.go:301-455`）
- StatusManager 处理失败计数和状态升级（`statusmanager.go:121-200`）
- setClusterHealthStatus 设置健康状态（`bkecluster_controller.go:757-775`）

**影响**：
- 没有统一的聚合入口
- 聚合规则分散在代码中
- 难以测试和维护

#### 问题 4：双轨不一致

**现状**：
- `State` 和 `StateCode` 两套并行
- `StateCode` 是事实来源，`State` 是对外接口
- 可能出现不一致（如 `State=Ready` 但 `StateCode` 缺少位标记）

**影响**：
- 需要额外的修复机制（v1 文档 10.4.3）
- 增加理解和维护成本
- `bootstrapReadyStateCode = 527` 是魔法数字

#### 问题 5：缺少组件层状态

**现状**：
- containerd、coredns 等组件没有独立状态追踪
- 只能通过 Command 间接推断组件状态

**影响**：
- 无法精确追踪组件生命周期
- 升级时无法知道哪些组件已完成
- 故障排查困难

#### 问题 6：重试机制不完整

**现状**：
- 只有 Command 级自动重试（BackoffLimit）
- 集群级只有人工注解重试
- 缺少中间的自动重试 + 人工介入机制

**影响**：
- 临时错误需要人工介入，增加运维负担
- 无法自动恢复，降低系统可用性

### 2.2 影响评估

| 问题 | 严重性 | 影响范围 | 修复难度 |
|------|--------|---------|---------|
| 状态维度爆炸 | 高 | 所有开发者 | 中 |
| 语义重叠 | 高 | 所有消费者 | 高 |
| 聚合分散 | 中 | 维护者 | 中 |
| 双轨不一致 | 中 | 运维人员 | 低 |
| 缺少组件层状态 | 中 | 升级场景 | 高 |
| 重试机制不完整 | 中 | 故障恢复 | 中 |

---

## 3. 解决方案

### 3.1 v3 状态模型

#### 3.1.1 集群层状态

**LifecyclePhase（6 个值）**：

```go
type LifecyclePhase string

const (
    ClusterLifecycleCreating    LifecyclePhase = "Creating"
    ClusterLifecycleRunning     LifecyclePhase = "Running"
    ClusterLifecycleUpgrading   LifecyclePhase = "Upgrading"
    ClusterLifecycleScaling     LifecyclePhase = "Scaling"
    ClusterLifecycleRollingBack LifecyclePhase = "RollingBack"
    ClusterLifecycleFailed      LifecyclePhase = "Failed"
)
```

**状态转换图**：

```
         ┌──────────┐
         │ Creating │
         └────┬─────┘
              │ 所有节点 Ready + 所有组件 Installed
              ▼
         ┌──────────┐
    ┌───▶│ Running  │◀────┐
    │    └────┬─────┘     │
    │         │           │
    │    ┌────┴────┐      │
    │    │         │      │
    │    ▼         ▼      │
    │ ┌────────┐ ┌─────┐  │
    │ │Upgrade │ │Scale│  │
    │ └───┬────┘ └──┬──┘  │
    │     │         │     │
    │     ▼         ▼     │
    │  ┌──────────────┐   │
    │  │   RollingBack│   │
    │  └──────┬───────┘   │
    │         │           │
    └─────────┴───────────┘
              │
              ▼
         ┌──────────┐
         │  Failed  │
         └──────────┘
```

#### 3.1.2 节点层状态

**LifecyclePhase（8 个值）**：

```go
const (
    NodeLifecyclePending      LifecyclePhase = "Pending"
    NodeLifecycleProvisioned  LifecyclePhase = "Provisioned"
    NodeLifecycleReady        LifecyclePhase = "Ready"
    NodeLifecycleUpgrading    LifecyclePhase = "Upgrading"
    NodeLifecycleRollingBack  LifecyclePhase = "RollingBack"
    NodeLifecycleDeleting     LifecyclePhase = "Deleting"
    NodeLifecycleRemoved      LifecyclePhase = "Removed"
    NodeLifecycleFailed       LifecyclePhase = "Failed"
)
```

#### 3.1.3 组件层状态

**ComponentLifecycleStatus（8 个值）**：

```go
type ComponentLifecycleStatus struct {
    Name               string         `json:"name"`
    NodeIP             string         `json:"nodeIP,omitempty"` // 节点级组件必填
    ComponentType      ComponentType  `json:"componentType"`    // node/cluster
    Phase              LifecyclePhase `json:"phase"`
    CurrentVersion     string         `json:"currentVersion,omitempty"`
    TargetVersion      string         `json:"targetVersion,omitempty"`
    LastTransitionTime *metav1.Time   `json:"lastTransitionTime,omitempty"`
    Message            string         `json:"message,omitempty"`
}

const (
    ComponentLifecyclePending      LifecyclePhase = "Pending"
    ComponentLifecycleInstalling   LifecyclePhase = "Installing"
    ComponentLifecycleInstalled    LifecyclePhase = "Installed"
    ComponentLifecycleUpgrading    LifecyclePhase = "Upgrading"
    ComponentLifecycleRollingBack  LifecyclePhase = "RollingBack"
    ComponentLifecycleUninstalling LifecyclePhase = "Uninstalling"
    ComponentLifecycleRemoved      LifecyclePhase = "Removed"
    ComponentLifecycleFailed       LifecyclePhase = "Failed"
)
```

**存储位置**：
- 节点级组件：`BKECluster.Status.NodeComponentStatuses[componentName][nodeIP]`
- 集群级组件：`BKECluster.Status.ClusterComponentStatuses[componentName]`

### 3.2 状态映射关系

#### 3.2.1 集群层映射

| v3 LifecyclePhase | v1 Phase | v1 ClusterStatus | v1 ClusterHealthState |
|-------------------|----------|------------------|----------------------|
| `Creating` | `InitControlPlane` / `JoinControlPlane` / `JoinWorker` | `Initializing` | `Deploying` |
| `Running` | `ClusterReady` | `Ready` | `Healthy` |
| `Upgrading` | `UpgradeControlPlane` / `UpgradeWorker` / `UpgradeEtcd` | `Upgrading` | `Upgrading` |
| `Scaling` | `Scale` | `ScalingMasterNodesUp/Down` / `ScalingWorkerNodesUp/Down` | `Deploying` |
| `RollingBack` | `UpgradeControlPlane` | `Upgrading` | `UpgradeFailed` |
| `Failed` | `FailedBootstrapNode` | `UpgradeFailed` / `ScaleFailed` / `InitializationFailed` | `Unhealthy` / `DeployFailed` / `UpgradeFailed` |

**派生逻辑**：

```go
func SyncClusterPhaseToLegacyFields(cluster *BKECluster, phase LifecyclePhase) {
    switch phase {
    case ClusterLifecycleCreating:
        cluster.Status.Phase = InitControlPlane
        cluster.Status.ClusterStatus = ClusterInitializing
        cluster.Status.ClusterHealthState = Deploying
    case ClusterLifecycleRunning:
        cluster.Status.Phase = ClusterReadyOld
        cluster.Status.ClusterStatus = ClusterReady
        cluster.Status.ClusterHealthState = Healthy
        cluster.Status.Ready = true
    case ClusterLifecycleUpgrading:
        cluster.Status.Phase = UpgradeControlPlane
        cluster.Status.ClusterStatus = ClusterUpgrading
        cluster.Status.ClusterHealthState = Upgrading
    case ClusterLifecycleScaling:
        cluster.Status.Phase = Scale
        if hasScalingUp(cluster) {
            cluster.Status.ClusterStatus = ClusterWorkerScalingUp
        } else {
            cluster.Status.ClusterStatus = ClusterWorkerScalingDown
        }
        cluster.Status.ClusterHealthState = Deploying
    case ClusterLifecycleRollingBack:
        cluster.Status.Phase = UpgradeControlPlane
        cluster.Status.ClusterStatus = ClusterUpgrading
        cluster.Status.ClusterHealthState = UpgradeFailed
    case ClusterLifecycleFailed:
        cluster.Status.Phase = FailedBootstrapNode
        cluster.Status.ClusterStatus = ClusterUpgradeFailed
        cluster.Status.ClusterHealthState = Unhealthy
    }
}
```

#### 3.2.2 节点层映射

| v3 LifecyclePhase | v1 State | v1 StateCode |
|-------------------|----------|--------------|
| `Pending` | `Pending` | `0` |
| `Provisioned` | `Provisioned` | `NodeAgentReadyFlag + NodeEnvFlag` |
| `Ready` | `Ready` | `527`（bootstrapReadyStateCode） |
| `Upgrading` | `Upgrading` | 保持不变 |
| `RollingBack` | `Upgrading`（旧版本无 RollingBack） | 保持不变 |
| `Deleting` | `Deleting` | `NodeDeletingFlag` |
| `Removed` | `NotReady` | 清除所有标记 |
| `Failed` | `Failed` | `NodeFailedFlag` |

**派生逻辑**：

```go
func SyncNodeStateToLegacyFields(node *BKENode, phase LifecyclePhase) {
    switch phase {
    case NodeLifecyclePending:
        node.Status.State = NodePending
    case NodeLifecycleProvisioned:
        node.Status.State = NodeProvisioned
    case NodeLifecycleReady:
        node.Status.State = NodeReady
    case NodeLifecycleUpgrading:
        node.Status.State = NodeUpgrading
    case NodeLifecycleRollingBack:
        node.Status.State = NodeUpgrading // 旧版本无 RollingBack
    case NodeLifecycleDeleting:
        node.Status.State = NodeDeleting
    case NodeLifecycleRemoved:
        node.Status.State = NodeNotReady
    case NodeLifecycleFailed:
        node.Status.State = NodeFailed
    }
}
```

### 3.3 聚合逻辑统一

**现状**：聚合逻辑分散在 3 个地方
- PhaseFlow post-hook
- StatusManager
- setClusterHealthStatus

**重构后**：统一 `StateAggregator` 组件

```go
// pkg/statemachine/aggregator.go
type StateAggregator struct{}

func (a *StateAggregator) AggregateClusterLifecycle(
    nodePhases []LifecyclePhase,
    clusterComponentStatuses map[string]ComponentLifecycleStatus,
) LifecyclePhase {
    // 优先级规则（按重要性排序）：
    // 1. Failed: 任意节点或集群级组件 Failed
    // 2. RollingBack: 任意节点或集群级组件 RollingBack
    // 3. Upgrading: 任意节点或集群级组件 Upgrading
    // 4. Scaling: 任意节点 Deleting 或 Pending
    // 5. Creating: 任意节点 Pending/Provisioned
    // 6. Running: 所有节点 Ready + 所有集群级组件 Installed

    if anySliceMatch(nodePhases, NodeLifecycleFailed) ||
        anyMapMatch(clusterComponentStatuses, ComponentLifecycleFailed) {
        return ClusterLifecycleFailed
    }
    if anySliceMatch(nodePhases, NodeLifecycleRollingBack) ||
        anyMapMatch(clusterComponentStatuses, ComponentLifecycleRollingBack) {
        return ClusterLifecycleRollingBack
    }
    if anySliceMatch(nodePhases, NodeLifecycleUpgrading) ||
        anyMapMatch(clusterComponentStatuses, ComponentLifecycleUpgrading) {
        return ClusterLifecycleUpgrading
    }
    if anySliceMatch(nodePhases, NodeLifecycleDeleting) {
        return ClusterLifecycleScaling
    }
    if anySliceMatch(nodePhases, NodeLifecyclePending, NodeLifecycleProvisioned) {
        return ClusterLifecycleCreating
    }
    if allSliceMatch(nodePhases, NodeLifecycleReady) &&
        allMapMatch(clusterComponentStatuses, ComponentLifecycleInstalled) {
        return ClusterLifecycleRunning
    }
    return ClusterLifecycleCreating
}
```

**聚合优先级矩阵**：

```
                    Failed  RollingBack  Upgrading  Deleting  Pending/Provisioned  Ready
Failed              ✓
RollingBack         -         ✓
Upgrading           -         -            ✓
Scaling             -         -            -           ✓
Creating            -         -            -           -              ✓
Running             -         -            -           -              -              ✓

规则：从上到下扫描，第一个匹配的规则决定集群状态
```

### 3.4 组件层状态

**新增** `ComponentLifecycleStatus` 类型（见 3.1.3）。

**组件状态聚合到节点状态**：

```go
func AggregateNodeLifecycle(
    nodeIP string,
    nodeComponentStatuses map[string]map[string]ComponentLifecycleStatus,
) LifecyclePhase {
    components := collectNodeComponents(nodeIP, nodeComponentStatuses)
    if len(components) == 0 {
        return NodeLifecyclePending
    }

    // 优先级：Failed > RollingBack > Upgrading > Removing > Installing > Installed > Pending
    if anyMatch(components, ComponentLifecycleFailed) {
        return NodeLifecycleFailed
    }
    if anyMatch(components, ComponentLifecycleRollingBack) {
        return NodeLifecycleRollingBack
    }
    if anyMatch(components, ComponentLifecycleUpgrading) {
        return NodeLifecycleUpgrading
    }
    if anyMatch(components, ComponentLifecycleUninstalling) {
        return NodeLifecycleDeleting
    }
    if allMatch(components, ComponentLifecycleRemoved) {
        return NodeLifecycleRemoved
    }
    if allMatch(components, ComponentLifecycleInstalled) {
        return NodeLifecycleReady
    }
    if anyMatch(components, ComponentLifecycleInstalling) {
        return NodeLifecyclePending
    }
    return NodeLifecycleProvisioned
}
```

### 3.5 分层重试机制

**现状**：
- Command 级：自动重试（BackoffLimit）
- 集群级：人工注解重试

**重构后**：三层重试

```
Layer 1: Command 级自动重试（现有逻辑，不变）
  └─ BackoffLimit = 3，指数退避
  └─ 实现在 CommandReconciler

Layer 2: 集群级自动重试（新增）
  └─ MaxAutoRetries = 3，指数退避
  └─ 记录 OperationProgress.LastFailure
  └─ 实现在 BKEClusterReconciler

Layer 3: 人工介入（增强）
  └─ 注解：cvo.openfuyao.cn/retry-operation
  └─ 支持两种策略：从失败点继续 / 从头开始
  └─ 实现在 BKEClusterReconciler
```

**OperationProgress 类型**：

```go
type OperationProgress struct {
    OperationType           OperationType          `json:"operationType"`
    TargetVersion           string                 `json:"targetVersion,omitempty"`
    StartedAt               *metav1.Time           `json:"startedAt,omitempty"`
    FinishedAt              *metav1.Time           `json:"finishedAt,omitempty"`
    NeedsManualIntervention bool                   `json:"needsManualIntervention,omitempty"`
    Completed               []ComponentRecord      `json:"completed,omitempty"`
    LastFailure             *OperationFailureRecord `json:"lastFailure,omitempty"`
}

type OperationFailureRecord struct {
    Name     string      `json:"name"`
    Version  string      `json:"version,omitempty"`
    NodeIP   string      `json:"nodeIP,omitempty"` // 节点级组件必填
    FailedAt metav1.Time `json:"failedAt"`
    Error    string      `json:"error,omitempty"`
    Attempt  int32       `json:"attempt,omitempty"`
}
```

**自动重试逻辑**：

```go
func (r *Reconciler) executeDAGWithRetry(ctx context.Context, cluster *BKECluster) (ctrl.Result, error) {
    result, err := r.executeDAG(ctx, cluster)
    
    if err != nil {
        cluster.Status.OperationProgress.MarkFailure(
            componentName, version, nodeIP, err.Error(), metav1.Now())
        
        if cluster.Status.OperationProgress.LastFailure.Attempt >= maxAutoRetries {
            cluster.Status.OperationProgress.NeedsManualIntervention = true
            r.Status().Update(ctx, cluster)
            return ctrl.Result{}, nil // 停止调谐，等待人工介入
        }
        
        r.Status().Update(ctx, cluster)
        
        attempt := cluster.Status.OperationProgress.LastFailure.Attempt
        backoff := calculateBackoff(attempt)
        return ctrl.Result{RequeueAfter: backoff}, nil
    }
    
    cluster.Status.OperationProgress.FinishedAt = &metav1.Time{Time: time.Now()}
    cluster.Status.LifecyclePhase = ClusterLifecycleRunning
    cluster.Status.OperationProgress.LastFailure = nil
    
    return ctrl.Result{}, r.Status().Update(ctx, cluster)
}
```

---

## 4. 迁移方案

### 4.1 平滑迁移策略

**核心原则**：零停机，可随时回滚

**迁移路径**：

```
Phase 1: 基础设施（Feature Gate 关闭）
  └─ 新增类型和字段，不影响现有逻辑
  
Phase 2: 双写模式（Feature Gate 开启）
  └─ 同时写入新旧字段，读取新字段优先
  
Phase 3: 全量上线（Feature Gate 默认开启）
  └─ 旧字段标记为 deprecated
  
Phase 4: 清理（未来版本）
  └─ 移除旧字段写入逻辑
```

### 4.2 Feature Gate 设计

**定义**：

```go
// utils/capbke/featuregate/gates.go
const (
    StateMachineV3 = "StateMachineV3"
)

var defaultFeatureGates = map[string]bool{
    StateMachineV3: false, // 默认关闭
}
```

**行为矩阵**：

| Feature Gate | PhaseFlow | DAG 调度 | 生命周期评估 | 旧字段写入 | 新字段写入 |
|-------------|-----------|---------|-------------|-----------|-----------|
| `StateMachineV3=false` | ✅ 执行 | ✅ 执行 | ❌ 跳过 | ✅ 写入 | ❌ 不写入 |
| `StateMachineV3=true` | ✅ 执行 | ✅ 执行 | ✅ 执行 | ✅ 双写 | ✅ 写入 |

**启用方式**：

```bash
# 方式 1：环境变量
export FEATURE_GATES=StateMachineV3=true

# 方式 2：命令行参数
./bke-controller-manager --feature-gates=StateMachineV3=true
```

### 4.3 双写机制

**写入逻辑**：

```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 现有逻辑（不变）
    // ...
    
    // 2. 新增：状态机评估（Feature Gate 控制）
    if featuregate.Enabled(featuregate.StateMachineV3) {
        // 自底向上评估：组件 → 节点 → 集群
        r.evaluateLifecycle(ctx, cluster, nodes)
        
        // 双写：新字段 → 旧字段
        statemachine.SyncClusterPhaseToLegacyFields(cluster, cluster.Status.LifecyclePhase)
        for i := range nodes {
            statemachine.SyncNodeStateToLegacyFields(&nodes[i], nodes[i].Status.LifecyclePhase)
        }
    }
    
    // 3. 持久化（同时写入新旧字段）
    // ...
}
```

**读取逻辑**：

```go
// 新字段优先，降级到旧字段
func getClusterLifecyclePhase(cluster *BKECluster) LifecyclePhase {
    if cluster.Status.LifecyclePhase != "" {
        return cluster.Status.LifecyclePhase
    }
    // 降级：从旧字段推导
    return LegacyPhaseToLifecycle(cluster.Status.Phase)
}
```

---

## 5. 兼容性设计

### 5.1 老字段保留策略

**策略**：保留作为派生视图，渐进式废弃

**保留清单**：

#### 集群层

| 字段 | 保留策略 | 废弃时间线 |
|------|---------|-----------|
| `Phase` | 派生视图，标记 deprecated | 12 个月后只读，18 个月后移除 |
| `ClusterStatus` | 派生视图，标记 deprecated | 12 个月后只读，18 个月后移除 |
| `ClusterHealthState` | 派生视图，标记 deprecated | 12 个月后只读，18 个月后移除 |
| `PhaseStatus` | 保留，简化为历史记录 | 长期保留 |
| `Conditions` | 保留，符合 K8s 标准 | 长期保留 |
| `DeclarativeUpgrade` | 迁移到 `OperationProgress` | 12 个月后移除 |

#### 节点层

| 字段 | 保留策略 | 废弃时间线 |
|------|---------|-----------|
| `State` | 派生视图，标记 deprecated | 12 个月后只读，18 个月后移除 |
| `StateCode` | 保留，降级为实现细节 | 长期保留，标记为 internal |
| `Message` | 保留，从 LifecyclePhase 派生 | 长期保留 |
| `NeedSkip` | 保留，业务逻辑字段 | 长期保留 |

### 5.2 派生视图实现

**标记 deprecated**：

```go
type BKEClusterStatus struct {
    // ===== 新字段（单一数据源）=====
    LifecyclePhase LifecyclePhase `json:"lifecyclePhase,omitempty"`
    
    // ===== 旧字段（派生视图，标记 deprecated）=====
    // +optional
    // Deprecated: Use LifecyclePhase instead. This field is derived from LifecyclePhase.
    // This field will be removed in v4.0.
    // Migration guide: https://docs.openfuyao.cn/migration/lifecycle-phase
    Phase BKEClusterPhase `json:"phase,omitempty"`
    
    // +optional
    // Deprecated: Use LifecyclePhase instead. This field is derived from LifecyclePhase.
    // This field will be removed in v4.0.
    ClusterStatus ClusterStatus `json:"clusterStatus,omitempty"`
    
    // +optional
    // Deprecated: Use LifecyclePhase instead. This field is derived from LifecyclePhase.
    // This field will be removed in v4.0.
    ClusterHealthState ClusterHealthState `json:"clusterHealthState,omitempty"`
}
```

### 5.3 渐进式废弃时间线

```
Phase 1: 双写共存（当前 → 6 个月）
  ├─ 保留所有旧字段
  ├─ 从新字段派生旧字段
  └─ 监控外部依赖

Phase 2: 标记废弃（6 个月 → 12 个月）
  ├─ 旧字段标记为 deprecated
  ├─ 发布迁移指南
  └─ 通过审计日志追踪使用情况

Phase 3: 只读兼容（12 个月 → 18 个月）
  ├─ 停止写入旧字段
  ├─ 保留读取兼容
  └─ 通过 Webhook 提供默认值

Phase 4: 完全移除（18 个月+）
  ├─ 移除旧字段
  └─ 确认无外部依赖
```

---

## 6. 实施计划

### 6.1 分阶段实施步骤

#### Phase 1：基础设施（已完成）

**已完成内容**：
- ✅ 新增 `LifecyclePhase` 类型和常量
- ✅ 新增 `ComponentLifecycleStatus` 类型
- ✅ 新增 `OperationProgress` 类型
- ✅ 新增 `OperationFailureRecord` 类型
- ✅ 扩展 `BKEClusterStatus` 和 `BKENodeStatus`
- ✅ 实现 `pkg/statemachine/` 包
- ✅ 实现 `ClusterLifecycleMachine`
- ✅ 实现 `NodeLifecycleMachine`
- ✅ 实现 `ComponentLifecycleMachine`
- ✅ 实现 `Aggregator`（自底向上聚合）
- ✅ 实现兼容性映射（`SyncClusterPhaseToLegacyFields`、`SyncNodeStateToLegacyFields`）

**测试**：
- ✅ 单元测试覆盖状态转换规则

#### Phase 2：集成到 Reconciler（待实施，2 周）

**任务**：
- [ ] 在 `BKEClusterReconciler` 中集成 `evaluateLifecycle`
- [ ] 实现双写逻辑
- [ ] 实现降级读取
- [ ] 集成到 DAG 调度器

**代码修改**：

```go
// controllers/capbke/bkecluster_controller.go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 现有逻辑（不变）
    // ...
    
    // 2. 新增：状态机评估（Feature Gate 控制）
    if featuregate.Enabled(featuregate.StateMachineV3) {
        r.evaluateLifecycle(ctx, cluster, nodes)
    }
    
    // 3. 持久化
    // ...
}

func (r *BKEClusterReconciler) evaluateLifecycle(
    ctx context.Context,
    cluster *BKECluster,
    nodes []BKENode,
) error {
    smCtx := &statemachine.TransitionContext{
        Cluster:                  cluster,
        Nodes:                    nodes,
        NodeComponentStatuses:    cluster.Status.NodeComponentStatuses,
        ClusterComponentStatuses: cluster.Status.ClusterComponentStatuses,
    }

    // 1. 评估组件层状态（自底向上）
    for componentName, nodeComps := range cluster.Status.NodeComponentStatuses {
        for nodeIP := range nodeComps {
            compMachine := statemachine.NewComponentLifecycleMachine(componentName, nodeIP)
            if target, changed := compMachine.Evaluate(smCtx); changed {
                compMachine.Transition(smCtx, target)
            }
        }
    }
    for componentName := range cluster.Status.ClusterComponentStatuses {
        compMachine := statemachine.NewComponentLifecycleMachine(componentName, "")
        if target, changed := compMachine.Evaluate(smCtx); changed {
            compMachine.Transition(smCtx, target)
        }
    }

    // 2. 评估节点层状态
    for i := range nodes {
        nodeMachine := statemachine.NewNodeLifecycleMachine(&nodes[i])
        if target, changed := nodeMachine.Evaluate(smCtx); changed {
            nodeMachine.Transition(smCtx, target)
        }
    }

    // 3. 评估集群层状态
    clusterMachine := statemachine.NewClusterLifecycleMachine()
    clusterMachine.SetCurrentPhase(LifecyclePhase(cluster.Status.LifecyclePhase))
    if target, changed := clusterMachine.Evaluate(smCtx); changed {
        clusterMachine.Transition(smCtx, target)
    }

    // 4. 双写：新字段 → 旧字段
    statemachine.SyncClusterPhaseToLegacyFields(cluster, cluster.Status.LifecyclePhase)
    for i := range nodes {
        statemachine.SyncNodeStateToLegacyFields(&nodes[i], nodes[i].Status.LifecyclePhase)
    }

    return nil
}
```

**测试**：
- [ ] 集成测试验证双写一致性
- [ ] 端到端测试验证完整流程

#### Phase 3：组件层状态（待实施，2 周）

**任务**：
- [ ] 在 DAG 调度器中更新 `ComponentLifecycleStatus`
- [ ] 实现组件级状态聚合到节点/集群

**代码修改**：

```go
// pkg/dagexec/scheduler.go
func (s *Scheduler) executeComponent(ctx context.Context, node *ComponentNode) error {
    // 1. 更新组件状态为 Installing/Upgrading
    s.updateComponentLifecycle(cluster, node.Name, node.NodeIP, ComponentLifecycleInstalling, version)
    
    // 2. 执行组件
    err := s.doExecute(ctx, node)
    
    // 3. 更新组件状态
    if err != nil {
        s.updateComponentLifecycle(cluster, node.Name, node.NodeIP, ComponentLifecycleFailed, version)
    } else {
        s.updateComponentLifecycle(cluster, node.Name, node.NodeIP, ComponentLifecycleInstalled, version)
    }
    
    return err
}

func (s *Scheduler) updateComponentLifecycle(
    cluster *BKECluster,
    componentName string,
    nodeIP string,
    phase LifecyclePhase,
    version string,
) {
    status := ComponentLifecycleStatus{
        Name:               componentName,
        NodeIP:             nodeIP,
        Phase:              phase,
        CurrentVersion:     version,
        LastTransitionTime: &metav1.Time{Time: time.Now()},
    }

    if nodeIP != "" {
        if cluster.Status.NodeComponentStatuses == nil {
            cluster.Status.NodeComponentStatuses = make(map[string]map[string]ComponentLifecycleStatus)
        }
        if cluster.Status.NodeComponentStatuses[componentName] == nil {
            cluster.Status.NodeComponentStatuses[componentName] = make(map[string]ComponentLifecycleStatus)
        }
        cluster.Status.NodeComponentStatuses[componentName][nodeIP] = status
    } else {
        if cluster.Status.ClusterComponentStatuses == nil {
            cluster.Status.ClusterComponentStatuses = make(map[string]ComponentLifecycleStatus)
        }
        cluster.Status.ClusterComponentStatuses[componentName] = status
    }
}
```

**测试**：
- [ ] 端到端测试验证组件状态追踪

#### Phase 4：分层重试（待实施，1 周）

**任务**：
- [ ] 实现集群级自动重试（Layer 2）
- [ ] 增强人工介入（Layer 3）

**代码修改**：

```go
// controllers/capbke/bkecluster_controller.go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 检查人工介入注解（最高优先级）
    if retryOp, hasRetry := cluster.Annotations[annotation.RetryOperationAnnotation]; hasRetry {
        delete(cluster.Annotations, annotation.RetryOperationAnnotation)
        r.Update(ctx, cluster)
        return r.handleManualIntervention(ctx, cluster, retryOp)
    }

    // 2. 检查是否需要人工介入
    if r.needsManualIntervention(cluster) {
        if !cluster.Status.OperationProgress.NeedsManualIntervention {
            cluster.Status.OperationProgress.NeedsManualIntervention = true
            r.Status().Update(ctx, cluster)
        }
        return ctrl.Result{}, nil // 停止调谐，等待人工介入
    }

    // 3. 正常执行或自动重试
    return r.executeDAGWithRetry(ctx, cluster)
}
```

**测试**：
- [ ] 故障注入测试验证重试机制

#### Phase 5：灰度发布（待实施，2 周）

**任务**：
- [ ] 内部环境开启 Feature Gate
- [ ] 收集反馈，修复问题
- [ ] 逐步扩大灰度范围

**操作**：

```bash
# 内部环境开启
kubectl set env deployment/bke-controller-manager FEATURE_GATES=StateMachineV3=true
kubectl rollout restart deployment/bke-controller-manager

# 监控指标
kubectl get --raw /metrics | grep state_machine_v3
```

#### Phase 6：全量上线（待实施，1 周）

**任务**：
- [ ] Feature Gate 默认开启
- [ ] 文档更新
- [ ] 旧字段标记为 deprecated

**操作**：

```go
// utils/capbke/featuregate/gates.go
var defaultFeatureGates = map[string]bool{
    StateMachineV3: true, // 默认开启
}
```

### 6.2 时间线

| 阶段 | 时间 | 内容 | 风险 |
|------|------|------|------|
| **Phase 1** | 已完成 | 基础设施 | 无（Feature Gate 关闭） |
| **Phase 2** | 2 周 | 集成到 Reconciler | 低（双写，旧字段仍有效） |
| **Phase 3** | 2 周 | 组件层状态 | 中（需要充分测试） |
| **Phase 4** | 1 周 | 分层重试 | 中（需要故障注入测试） |
| **Phase 5** | 2 周 | 灰度发布 | 低（可随时回滚） |
| **Phase 6** | 1 周 | 全量上线 | 低 |
| **总计** | **8 周** | | |

### 6.3 测试计划

#### 单元测试

| 测试目标 | 测试文件 | 覆盖范围 |
|---------|---------|---------|
| 集群层状态机 | `pkg/statemachine/cluster_machine_test.go` | 所有状态转换路径 |
| 节点层状态机 | `pkg/statemachine/node_machine_test.go` | 所有状态转换路径 |
| 组件层状态机 | `pkg/statemachine/component_machine_test.go` | 所有状态转换路径 |
| 状态聚合器 | `pkg/statemachine/aggregator_test.go` | 聚合优先级矩阵 |
| 兼容性映射 | `pkg/statemachine/compatibility_test.go` | 旧 ↔ 新映射正确性 |

#### 集成测试

| 测试场景 | 验证内容 |
|---------|---------|
| 安装流程 | Creating → Running 全路径，旧字段同步正确 |
| 升级流程 | Running → Upgrading → Running，DAG 组件状态同步 |
| 升级失败回滚 | Upgrading → RollingBack → Running |
| 扩容流程 | Running → Scaling → Running |
| 缩容流程 | Running → Scaling → Running |
| Feature Gate 关闭 | 旧字段正常写入，新字段为空 |
| Feature Gate 开启 | 新旧字段同时写入，值一致 |
| Controller 重启恢复 | 从 Status 字段恢复状态机状态 |
| 人工介入-从失败点继续 | Failed → 注解触发 → 重置失败组件 → 从失败点继续 → Running |
| 人工介入-从头开始 | Failed → 注解触发(full) → 重置所有进度 → 重新执行 → Running |

#### 端到端测试

| 测试场景 | 验证内容 |
|---------|---------|
| 完整安装流程 | 从创建 BKECluster 到 Running |
| 完整升级流程 | 从 Running 到 Upgrading 到 Running |
| 故障恢复 | 升级失败 → 自动重试 → 人工介入 → 恢复 |
| 并发操作 | 同时扩容和升级 |

---

## 7. 风险管理

### 7.1 回滚方案

#### 场景 1：Phase 2-3（Feature Gate 开启，发现问题）

```bash
# 关闭 Feature Gate
kubectl set env deployment/bke-controller-manager FEATURE_GATES=StateMachineV3=false
kubectl rollout restart deployment/bke-controller-manager
```

#### 场景 2：Phase 4-5（灰度发布，部分节点开启）

```bash
# 通过注解控制哪些集群使用新状态机
kubectl annotate bkecluster my-cluster bke.bocloud.com/feature-gates="StateMachineV3=false"
```

#### 场景 3：Phase 6（全量上线后，发现严重问题）

```bash
# 回滚到旧版本
kubectl rollout undo deployment/bke-controller-manager
```

### 7.2 监控和告警

#### 监控指标

```go
// pkg/metrics/state_machine.go
var (
    StateMachineTransitions = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "bke_state_machine_transitions_total",
            Help: "Total number of state machine transitions",
        },
        []string{"from_phase", "to_phase", "resource_type"},
    )
    
    StateMachineEvaluationDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "bke_state_machine_evaluation_duration_seconds",
            Help:    "Duration of state machine evaluation",
            Buckets: prometheus.ExponentialBuckets(0.001, 2, 10),
        },
        []string{"resource_type"},
    )
)
```

#### 告警规则

```yaml
# prometheus/rules/state_machine.yaml
groups:
  - name: state_machine
    rules:
      - alert: HighStateMachineTransitionRate
        expr: rate(bke_state_machine_transitions_total[5m]) > 10
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High state machine transition rate"
          description: "State machine transition rate is {{ $value }} per second"
      
      - alert: StateMachineEvaluationSlow
        expr: histogram_quantile(0.99, rate(bke_state_machine_evaluation_duration_seconds_bucket[5m])) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "State machine evaluation is slow"
          description: "99th percentile evaluation time is {{ $value }} seconds"
```

---

## 8. 迁移指南

### 8.1 给外部消费者的迁移指南

#### 变更说明

从 v3.0 开始，BKE 引入 `LifecyclePhase` 作为集群和节点的唯一状态源。旧字段（`Phase`、`ClusterStatus`、`ClusterHealthState`、`State`）将在 v4.0 移除。

#### 迁移步骤

**Step 1：读取新字段**

```go
// 旧代码
phase := cluster.Status.Phase
clusterStatus := cluster.Status.ClusterStatus

// 新代码
lifecyclePhase := cluster.Status.LifecyclePhase
```

**Step 2：映射关系**

| 旧字段 | 新字段 |
|--------|--------|
| `Phase=InitControlPlane` | `LifecyclePhase=Creating` |
| `Phase=ClusterReady` | `LifecyclePhase=Running` |
| `ClusterStatus=Upgrading` | `LifecyclePhase=Upgrading` |
| `ClusterHealthState=Healthy` | `LifecyclePhase=Running` |
| `State=Ready` | `LifecyclePhase=Ready` |

**Step 3：时间线**

- **v3.0**：新字段可用，旧字段保留（双写）
- **v3.6**：旧字段标记 deprecated
- **v4.0**：移除旧字段

#### 验证方法

```bash
# 查看新旧字段是否一致
kubectl get bkecluster my-cluster -o yaml | grep -E "(phase|lifecyclePhase)"

# 预期输出
#   phase: ClusterReady
#   lifecyclePhase: Running
```

### 8.2 常见问题

**Q: 我必须在 v3.0 就迁移吗？**  
A: 不需要。旧字段在 v4.0 之前都会保留，但建议尽早迁移。

**Q: 如果我同时读取新旧字段，会冲突吗？**  
A: 不会。新旧字段保持一致，读取哪个都可以。

**Q: 迁移后如何验证？**  
A: 可以通过 `kubectl get bkecluster -o yaml` 查看新旧字段是否一致。

**Q: 如果我的系统依赖旧字段，怎么办？**  
A: 旧字段会保留至少 18 个月，给您足够的迁移时间。建议制定迁移计划，逐步切换到新字段。

**Q: 新字段和旧字段的语义完全一致吗？**  
A: 基本一致，但有一些细微差别：
- `LifecyclePhase` 更简洁，6 个值 vs 39 个值
- `LifecyclePhase` 是单一数据源，旧字段是派生视图
- 建议优先使用 `LifecyclePhase`

---

## 附录

### A. 关键文件清单

| 文件路径 | 操作 | 说明 |
|---------|------|------|
| `api/bkecommon/v1beta1/lifecycle_types.go` | 新增 | LifecyclePhase 类型和常量 |
| `api/bkecommon/v1beta1/operation_progress.go` | 新增 | OperationProgress 类型 |
| `api/bkecommon/v1beta1/component_lifecycle.go` | 新增 | ComponentLifecycleStatus 类型 |
| `api/bkecommon/v1beta1/bkecluster_status.go` | 修改 | 新增 LifecyclePhase、OperationProgress 等字段 |
| `api/bkecommon/v1beta1/bkenode_types.go` | 修改 | 新增 LifecyclePhase 字段 |
| `pkg/statemachine/engine.go` | 新增 | 状态机引擎核心 |
| `pkg/statemachine/cluster_machine.go` | 新增 | 集群层状态机 |
| `pkg/statemachine/node_machine.go` | 新增 | 节点层状态机 |
| `pkg/statemachine/component_machine.go` | 新增 | 组件层状态机 |
| `pkg/statemachine/aggregator.go` | 新增 | 状态聚合器 |
| `pkg/statemachine/transition.go` | 新增 | 状态转换规则 |
| `pkg/statemachine/compatibility.go` | 新增 | 兼容性映射 |
| `pkg/statemachine/types.go` | 新增 | 内部类型 |
| `controllers/capbke/bkecluster_controller.go` | 修改 | 集成生命周期评估 |
| `controllers/capbke/manual_intervention.go` | 新增 | 人工介入处理逻辑 |
| `pkg/dagexec/scheduler.go` | 修改 | 集成组件生命周期更新 |
| `utils/capbke/featuregate/gates.go` | 修改 | 新增 StateMachineV3 Feature Gate |
| `utils/capbke/annotation/annotation.go` | 修改 | 新增 RetryOperationAnnotation 常量 |

### B. 术语表

| 术语 | 定义 |
|------|------|
| **LifecyclePhase** | 资源的生命周期阶段，单一数据源 |
| **派生视图** | 从 LifecyclePhase 计算得出的旧字段 |
| **StateAggregator** | 统一的状态聚合器 |
| **Feature Gate** | 功能开关，控制新特性的启用 |
| **双写** | 同时写入新旧字段，保证兼容性 |
| **单一数据源** | 只有一个字段是真实数据源，其他字段都是派生的 |

---

**文档版本**: v1.0  
**最后更新**: 2026-07-13  
**维护者**: BKE Team
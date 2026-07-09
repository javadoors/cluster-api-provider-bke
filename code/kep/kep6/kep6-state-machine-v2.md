# KEP-6 完整分层状态机设计

## 目录

1. [资源与状态字段概览](#1-资源与状态字段概览)
2. [分层状态机架构](#2-分层状态机架构)
3. [集群层状态机](#3-集群层状态机)
4. [节点层状态机](#4-节点层状态机)
5. [组件层状态机](#5-组件层状态机)
6. [状态转换完整流程图](#6-状态转换完整流程图)
7. [重试与幂等性设计](#7-重试与幂等性设计)
8. [场景用例](#8-场景用例)

---

## 1. 资源与状态字段概览

### 1.1 涉及的资源

| 资源 | 层级 | 说明 |
|------|------|------|
| **BKECluster** | 集群层 | 集群主资源，管理整个集群生命周期 |
| **BKENode** | 节点层 | 节点资源，管理单个节点状态 |
| **Node** | 节点层 | Kubernetes 原生 Node 资源 |
| **ComponentVersion** | 组件层 | 组件版本定义 |
| **Command** | 组件层 | Agent 命令资源 |
| **Reconciler** | 控制层 | 调谐器，驱动状态转换 |

### 1.2 BKECluster 状态字段

```go
type BKEClusterStatus struct {
    // 集群就绪状态
    Ready bool `json:"ready"`
    
    // 版本信息
    OpenFuyaoVersion    string `json:"openFuyaoVersion,omitempty"`
    KubernetesVersion   string `json:"kubernetesVersion,omitempty"`
    EtcdVersion         string `json:"etcdVersion,omitempty"`
    ContainerdVersion   string `json:"containerdVersion,omitempty"`
    
    // Agent 状态
    AgentStatus BKEAgentStatus `json:"agentStatus"`
    
    // 集群阶段
    Phase BKEClusterPhase `json:"phase,omitempty"`
    
    // 集群操作状态
    ClusterStatus ClusterStatus `json:"clusterStatus,omitempty"`
    
    // 集群健康状态
    ClusterHealthState ClusterHealthState `json:"clusterHealthState,omitempty"`
    
    // Addon 状态
    AddonStatus []Product `json:"addonStatus,omitempty"`
    
    // Phase 执行状态
    PhaseStatus PhaseStatus `json:"phaseStatus,omitempty"`
    
    // 条件列表
    Conditions ClusterConditions `json:"conditions,omitempty"`
    
    // 声明式升级状态
    DeclarativeUpgrade *DeclarativeUpgradeStatus `json:"declarativeUpgrade,omitempty"`
}
```

**关键字段说明**：

| 字段 | 类型 | 说明 | 示例值 |
|------|------|------|--------|
| `Phase` | `BKEClusterPhase` | 集群生命周期阶段 | `Provisioning`, `Provisioned`, `Running`, `Failed` |
| `ClusterStatus` | `ClusterStatus` | 集群操作状态 | `Creating`, `Updating`, `Deleting` |
| `ClusterHealthState` | `ClusterHealthState` | 集群健康状态 | `Healthy`, `Unhealthy`, `Degraded` |
| `Conditions` | `ClusterConditions` | 条件列表 | `Ready`, `Health`, `AddonReady` |
| `DeclarativeUpgrade` | `DeclarativeUpgradeStatus` | 升级进度 | 包含 `TargetVersion`, `Completed`, `LastError` |

### 1.3 BKENode 状态字段

```go
type BKENodeStatus struct {
    // 节点状态
    State NodeState `json:"state,omitempty"`
    
    // 状态位标记
    StateCode int `json:"stateCode,omitempty"`
    
    // 状态消息
    Message string `json:"message,omitempty"`
    
    // 是否跳过
    NeedSkip bool `json:"needSkip,omitempty"`
}
```

**State 枚举值**：

| 状态 | 说明 |
|------|------|
| `NotReady` | 节点未就绪 |
| `Ready` | 节点就绪 |
| `Pending` | 节点等待中 |
| `Failed` | 节点失败 |
| `Deleting` | 节点删除中 |
| `Upgrading` | 节点升级中 |
| `Provisioned` | 节点已配置 |

**StateCode 位标记**：

| 位 | 标记名 | 说明 |
|----|--------|------|
| 0 | `NodeAgentPushedFlag` | Agent 已推送 |
| 1 | `NodeAgentReadyFlag` | Agent 就绪 |
| 2 | `NodeEnvFlag` | 环境已初始化 |
| 3 | `NodeBootFlag` | 节点已启动 |
| 4 | `NodeHAFlag` | 高可用已配置 |
| 5 | `MasterInitFlag` | Master 已初始化 |
| 6 | `NodeDeletingFlag` | 节点删除中 |
| 7 | `NodeFailedFlag` | 节点失败 |
| 8 | `NodeStateNeedRecord` | 状态需要记录 |
| 9 | `NodePostProcessFlag` | 后处理已完成 |

### 1.4 Node 状态字段（Kubernetes 原生）

```go
type NodeStatus struct {
    // 节点条件
    Conditions []NodeCondition `json:"conditions,omitempty"`
    
    // 节点地址
    Addresses []NodeAddress `json:"addresses,omitempty"`
    
    // 节点信息
    NodeInfo NodeSystemInfo `json:"nodeInfo,omitempty"`
}
```

**关键 Condition**：

| Type | 说明 |
|------|------|
| `Ready` | 节点是否就绪 |
| `MemoryPressure` | 内存压力 |
| `DiskPressure` | 磁盘压力 |
| `PIDPressure` | PID 压力 |
| `NetworkUnavailable` | 网络不可用 |

### 1.5 ComponentVersion 状态字段

```go
type ComponentVersionStatus struct {
    // 组件阶段
    Phase ComponentPhase `json:"phase,omitempty"`
    
    // 组件版本
    Version string `json:"version,omitempty"`
    
    // 最后更新时间
    LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
    
    // 错误信息
    Message string `json:"message,omitempty"`
}
```

### 1.6 DeclarativeUpgradeStatus 状态字段

```go
type DeclarativeUpgradeStatus struct {
    // 目标版本
    TargetVersion string `json:"targetVersion,omitempty"`
    
    // 开始时间
    StartedAt *metav1.Time `json:"startedAt,omitempty"`
    
    // 完成时间
    FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
    
    // 最后错误
    LastError string `json:"lastError,omitempty"`
    
    // 最后失败记录
    LastFailure *DeclarativeUpgradeFailureRecord `json:"lastFailure,omitempty"`
    
    // 已完成组件列表
    Completed []DeclarativeUpgradeComponentRecord `json:"completed,omitempty"`
}
```

---

## 2. 分层状态机架构

### 2.1 三层状态机模型

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           集群层 (Cluster Layer)                             │
│  BKECluster.Status.Phase / ClusterStatus / ClusterHealthState               │
│  DeclarativeUpgradeStatus                                                   │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ 管理
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           节点层 (Node Layer)                                │
│  BKENode.Status.State / StateCode                                           │
│  Node.Status.Conditions                                                     │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ 承载
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          组件层 (Component Layer)                            │
│  ComponentVersion.Status.Phase / Version                                    │
│  NodeComponentStatuses / ComponentStatuses                                  │
│  Command.Status                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 状态依赖关系

```
集群状态 (BKECluster.Phase)
  ├─ Running
  │   ├─ 节点状态 (BKENode.State)
  │   │   ├─ Ready
  │   │   │   └─ 组件状态 (ComponentPhase)
  │   │   │       ├─ Installed
  │   │   │       └─ Upgrading
  │   │   └─ NotReady
  │   │       └─ 组件状态暂停
  │   └─ 所有节点 Ready → 集群 Ready
  └─ Failed
      └─ 触发回滚或人工介入
```

### 2.3 状态转换驱动

```
Reconciler (调谐器)
  │
  ├─ Watch BKECluster 变更
  │   └─ 触发集群层状态转换
  │
  ├─ Watch BKENode 变更
  │   └─ 触发节点层状态转换
  │
  ├─ Watch ComponentVersion 变更
  │   └─ 触发组件层状态转换
  │
  └─ 执行 DAG
      └─ 按依赖顺序执行组件安装/升级
```

---

## 3. 集群层状态机

### 3.1 BKECluster.Phase 状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      BKECluster.Phase 状态机                                 │
└─────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │   (初始)     │
                    └──────┬───────┘
                           │
                           │ 创建 BKECluster
                           ▼
                    ┌──────────────┐
                    │ Provisioning │ ← 集群配置中
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │Provisioned│ │  Failed  │ │  Failed  │
        │ (已配置) │ │ (配置失败)│ │ (超时)   │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │            │            │
             │            │            │ 重试
             │            │            ▼
             │            │     ┌──────────────┐
             │            │     │ Provisioning │
             │            │     └──────────────┘
             │            │
             │            │ 人工介入
             │            ▼
             │     ┌──────────────┐
             │     │ Provisioning │
             │     └──────────────┘
             │
             │ 所有节点就绪
             ▼
        ┌──────────┐
        │ Running  │ ← 集群运行中
        └────┬─────┘
             │
             │ 版本变更
             ▼
        ┌──────────┐
        │ Upgrading│ ← 集群升级中
        └────┬─────┘
             │
             │ 升级完成
             ▼
        ┌──────────┐
        │ Running  │
        └──────────┘
```

### 3.2 BKECluster.ClusterStatus 状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    BKECluster.ClusterStatus 状态机                           │
└─────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │   (初始)     │
                    └──────┬───────┘
                           │
                           ▼
                    ┌──────────────┐
                    │   Creating   │ ← 集群创建中
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ Created  │ │  Failed  │ │  Failed  │
        │ (已创建) │ │ (创建失败)│ │ (超时)   │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │            │            │
             │            │            │ 重试
             │            │            ▼
             │            │     ┌──────────────┐
             │            │     │   Creating   │
             │            │     └──────────────┘
             │            │
             │            │ 人工介入
             │            ▼
             │     ┌──────────────┐
             │     │   Creating   │
             │     └──────────────┘
             │
             │ 所有组件就绪
             ▼
        ┌──────────┐
        │  Ready   │ ← 集群就绪
        └────┬─────┘
             │
             │ 版本变更
             ▼
        ┌──────────┐
        │ Updating │ ← 集群更新中
        └────┬─────┘
             │
             │ 更新完成
             ▼
        ┌──────────┐
        │  Ready   │
        └──────────┘
```

### 3.3 BKECluster.ClusterHealthState 状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                  BKECluster.ClusterHealthState 状态机                        │
└─────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │   (初始)     │
                    └──────┬───────┘
                           │
                           ▼
                    ┌──────────────┐
                    │   Unknown    │ ← 健康状态未知
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ Healthy  │ │ Degraded │ │Unhealthy │
        │ (健康)   │ │ (降级)   │ │ (不健康) │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │            │            │
             │            │            │ 恢复
             │            │            ▼
             │            │     ┌──────────┐
             │            │     │ Degraded │
             │            │     └──────────┘
             │            │
             │            │ 恢复
             │            ▼
             │     ┌──────────┐
             │     │ Healthy  │
             │     └──────────┘
             │
             │ 故障
             ▼
        ┌──────────┐
        │Unhealthy │
        └──────────┘
```

### 3.4 DeclarativeUpgradeStatus 状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                   DeclarativeUpgradeStatus 状态机                            │
└─────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │   (初始)     │
                    └──────┬───────┘
                           │
                           │ 版本变更
                           ▼
                    ┌──────────────┐
                    │  Upgrading   │ ← 升级中
                    │ StartedAt=now│
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │Completed │ │  Failed  │ │  Failed  │
        │FinishedAt│ │LastError │ │LastFailure│
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │            │            │
             │            │            │ 重试
             │            │            ▼
             │            │     ┌──────────────┐
             │            │     │  Upgrading   │
             │            │     └──────────────┘
             │            │
             │            │ 人工介入
             │            ▼
             │     ┌──────────────┐
             │     │  Upgrading   │
             │     └──────────────┘
             │
             │ 版本再次变更
             ▼
        ┌──────────────┐
        │  Upgrading   │ ← 重置状态
        │ TargetVersion│
        │ =newVersion  │
        └──────────────┘
```

---

## 4. 节点层状态机

### 4.1 BKENode.State 状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       BKENode.State 状态机                                   │
└─────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │   (初始)     │
                    └──────┬───────┘
                           │
                           │ 节点加入
                           ▼
                    ┌──────────────┐
                    │   Pending    │ ← 节点等待中
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │Provisioned│ │  Failed  │ │  Failed  │
        │ (已配置) │ │ (配置失败)│ │ (超时)   │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │            │            │
             │            │            │ 重试
             │            │            ▼
             │            │     ┌──────────────┐
             │            │     │   Pending    │
             │            │     └──────────────┘
             │            │
             │            │ 人工介入
             │            ▼
             │     ┌──────────────┐
             │     │   Pending    │
             │     └──────────────┘
             │
             │ Agent 就绪
             ▼
        ┌──────────┐
        │  Ready   │ ← 节点就绪
        └────┬─────┘
             │
             │ 版本变更
             ▼
        ┌──────────┐
        │ Upgrading│ ← 节点升级中
        └────┬─────┘
             │
             │ 升级完成
             ▼
        ┌──────────┐
        │  Ready   │
        └────┬─────┘
             │
             │ 节点删除
             ▼
        ┌──────────┐
        │ Deleting │ ← 节点删除中
        └────┬─────┘
             │
             │ 删除完成
             ▼
        ┌──────────┐
        │ NotReady │ ← 节点未就绪
        └──────────┘
```

### 4.2 BKENode.StateCode 位标记状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    BKENode.StateCode 位标记状态机                            │
└─────────────────────────────────────────────────────────────────────────────┘

初始状态: StateCode = 0

节点加入:
  StateCode |= NodeAgentPushedFlag (bit 0)  ← Agent 已推送

Agent 就绪:
  StateCode |= NodeAgentReadyFlag (bit 1)   ← Agent 就绪

环境初始化:
  StateCode |= NodeEnvFlag (bit 2)          ← 环境已初始化

节点启动:
  StateCode |= NodeBootFlag (bit 3)         ← 节点已启动

Master 初始化 (仅 Master):
  StateCode |= MasterInitFlag (bit 5)       ← Master 已初始化

后处理完成:
  StateCode |= NodePostProcessFlag (bit 9)  ← 后处理已完成

节点删除:
  StateCode |= NodeDeletingFlag (bit 6)     ← 节点删除中

节点失败:
  StateCode |= NodeFailedFlag (bit 7)       ← 节点失败

状态记录:
  StateCode |= NodeStateNeedRecord (bit 8)  ← 状态需要记录
```

### 4.3 Node.Status.Conditions 状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Node.Status.Conditions 状态机                             │
└─────────────────────────────────────────────────────────────────────────────┘

Ready Condition:
  ┌──────────────┐
  │   Unknown    │ ← 初始状态
  └──────┬───────┘
         │
         │ 节点就绪
         ▼
  ┌──────────────┐
  │    True      │ ← 节点就绪
  └──────┬───────┘
         │
         │ 节点故障
         ▼
  ┌──────────────┐
  │    False     │ ← 节点未就绪
  └──────────────┘

MemoryPressure / DiskPressure / PIDPressure:
  ┌──────────────┐
  │    False     │ ← 正常状态
  └──────┬───────┘
         │
         │ 资源压力
         ▼
  ┌──────────────┐
  │    True      │ ← 资源压力
  └──────────────┘

NetworkUnavailable:
  ┌──────────────┐
  │    False     │ ← 网络可用
  └──────┬───────┘
         │
         │ 网络故障
         ▼
  ┌──────────────┐
  │    True      │ ← 网络不可用
  └──────────────┘
```

---

## 5. 组件层状态机

### 5.1 ComponentPhase 状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       ComponentPhase 状态机                                  │
└─────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │   Pending    │ ← 初始状态
                    └──────┬───────┘
                           │
                           │ 开始安装
                           ▼
                    ┌──────────────┐
                    │  Installing  │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┬────────────┐
              │            │            │            │
              ▼            ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
        │Installed │ │  Failed  │ │ Timeout  │ │ Partial  │
        │ (成功)   │ │ (失败)   │ │ (超时)   │ │ Success  │
        └──────────┘ └────┬─────┘ └────┬─────┘ │(仅Binary)│
                          │            │        └──────────┘
                          │            │
                          └─────┬──────┘
                                │
                                │ FailurePolicy=Rollback
                                ▼
                         ┌──────────────┐
                         │ RollingBack  │
                         └──────┬───────┘
                                │
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
             ┌──────────────┐        ┌──────────┐
             │  RolledBack  │        │  Failed  │
             │ (回滚成功)   │        │(回滚失败)│
             └──────────────┘        └──────────┘
```

### 5.2 NodeComponentStatuses 状态机（节点级）

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                  NodeComponentStatuses 状态机（节点级）                       │
└─────────────────────────────────────────────────────────────────────────────┘

节点状态: NodeComponentStatuses[componentName][nodeIP]

                    ┌──────────────┐
                    │   无记录     │ ← 新节点
                    └──────┬───────┘
                           │
                           │ MarkPending
                           ▼
                    ┌──────────────┐
                    │  Installing  │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┬────────────┐
              │            │            │            │
              ▼            ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
        │Installed │ │  Failed  │ │ Timeout  │ │ Partial  │
        │ v2.6.0   │ │          │ │          │ │ Success  │
        └──────────┘ └────┬─────┘ └────┬─────┘ └──────────┘
                          │            │
                          │            │ FailurePolicy=Rollback
                          │            ▼
                          │     ┌──────────────┐
                          │     │ RollingBack  │
                          │     └──────┬───────┘
                          │            │
                          │     ┌──────┴──────┐
                          │     │             │
                          │     ▼             ▼
                          │ ┌──────────┐ ┌──────────┐
                          │ │RolledBack│ │  Failed  │
                          │ └──────────┘ └──────────┘
                          │
                          │ 重试
                          ▼
                   ┌──────────────┐
                   │  Installing  │ ← 重新安装
                   └──────────────┘
```

### 5.3 ComponentStatuses 状态机（组件级）

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                  ComponentStatuses 状态机（组件级）                           │
└─────────────────────────────────────────────────────────────────────────────┘

组件状态: ComponentStatuses[componentName]

                    ┌──────────────┐
                    │   Pending    │
                    └──────┬───────┘
                           │
                           │ MarkPending
                           ▼
                    ┌──────────────┐
                    │  Installing  │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┬────────────┐
              │            │            │            │
              ▼            ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
        │Installed │ │  Failed  │ │ Timeout  │ │ Partial  │
        │ v1.11.1  │ │          │ │          │ │ Success  │
        └──────────┘ └────┬─────┘ └────┬─────┘ └──────────┘
                          │            │
                          │            │ FailurePolicy=Rollback
                          │            ▼
                          │     ┌──────────────┐
                          │     │ RollingBack  │
                          │     └──────┬───────┘
                          │            │
                          │     ┌──────┴──────┐
                          │     │             │
                          │     ▼             ▼
                          │ ┌──────────┐ ┌──────────┐
                          │ │RolledBack│ │  Failed  │
                          │ └──────────┘ └──────────┘
                          │
                          │ 重试
                          ▼
                   ┌──────────────┐
                   │  Installing  │
                   └──────────────┘
```

### 5.4 Command.Status 状态机

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       Command.Status 状态机                                  │
└─────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │   Pending    │ ← 命令创建
                    └──────┬───────┘
                           │
                           │ Agent 接收
                           ▼
                    ┌──────────────┐
                    │   Running    │ ← 命令执行中
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │Completed │ │  Failed  │ │  Failed  │
        │ (成功)   │ │ (失败)   │ │ (超时)   │
        └──────────┘ └────┬─────┘ └────┬─────┘
                          │            │
                          │            │ 重试
                          │            ▼
                          │     ┌──────────────┐
                          │     │   Running    │
                          │     └──────────────┘
                          │
                          │ 重试
                          ▼
                   ┌──────────────┐
                   │   Running    │
                   └──────────────┘
```

---

## 6. 状态转换完整流程图

### 6.1 安装流程状态转换

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         安装流程状态转换                                      │
└─────────────────────────────────────────────────────────────────────────────┘

集群层:
  BKECluster.Phase: (初始) → Provisioning → Provisioned → Running
  BKECluster.ClusterStatus: (初始) → Creating → Created → Ready
  BKECluster.ClusterHealthState: Unknown → Healthy

节点层:
  BKENode.State: (初始) → Pending → Provisioned → Ready
  BKENode.StateCode: 0 → NodeAgentPushedFlag → NodeAgentReadyFlag → NodeEnvFlag → NodeBootFlag
  Node.Status.Conditions.Ready: Unknown → True

组件层:
  ComponentPhase: Pending → Installing → Installed
  NodeComponentStatuses: (无记录) → Installing → Installed
  ComponentStatuses: Pending → Installing → Installed
  Command.Status: Pending → Running → Completed

状态转换时序:
  T0: 创建 BKECluster
      BKECluster.Phase = Provisioning
      BKECluster.ClusterStatus = Creating
  
  T1: 节点加入
      BKENode.State = Pending
      BKENode.StateCode = 0
  
  T2: Agent 推送
      BKENode.StateCode |= NodeAgentPushedFlag
      Command.Status = Pending → Running
  
  T3: Agent 就绪
      BKENode.State = Provisioned
      BKENode.StateCode |= NodeAgentReadyFlag
      Command.Status = Running → Completed
  
  T4: 环境初始化
      BKENode.StateCode |= NodeEnvFlag
      BKENode.StateCode |= NodeBootFlag
  
  T5: 组件安装
      ComponentPhase = Installing
      NodeComponentStatuses[nodeIP] = Installing
  
  T6: 组件安装完成
      ComponentPhase = Installed
      NodeComponentStatuses[nodeIP] = Installed
  
  T7: 所有节点就绪
      Node.Status.Conditions.Ready = True
      BKECluster.Phase = Running
      BKECluster.ClusterStatus = Ready
      BKECluster.ClusterHealthState = Healthy
```

### 6.2 升级流程状态转换

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         升级流程状态转换                                      │
└─────────────────────────────────────────────────────────────────────────────┘

集群层:
  BKECluster.Phase: Running → Upgrading → Running
  DeclarativeUpgradeStatus: (初始) → Upgrading → Completed
  BKECluster.ClusterStatus: Ready → Updating → Ready

节点层:
  BKENode.State: Ready → Upgrading → Ready
  BKENode.StateCode: (保持不变)

组件层:
  ComponentPhase: Installed → Upgrading → Installed
  NodeComponentStatuses: Installed → Upgrading → Installed
  ComponentStatuses: Installed → Upgrading → Installed

状态转换时序:
  T0: 版本变更
      BKECluster.Spec.DesiredVersion = v2.6.0
      DeclarativeUpgradeStatus.TargetVersion = v2.6.0
      DeclarativeUpgradeStatus.StartedAt = now
      BKECluster.Phase = Upgrading
      BKECluster.ClusterStatus = Updating
  
  T1: 组件升级开始
      ComponentPhase = Upgrading
      NodeComponentStatuses[nodeIP] = Upgrading
  
  T2: 节点升级
      BKENode.State = Upgrading
  
  T3: 组件升级完成
      ComponentPhase = Installed
      NodeComponentStatuses[nodeIP] = Installed
      DeclarativeUpgradeStatus.Completed = append(...)
  
  T4: 节点升级完成
      BKENode.State = Ready
  
  T5: 所有组件升级完成
      DeclarativeUpgradeStatus.FinishedAt = now
      BKECluster.Phase = Running
      BKECluster.ClusterStatus = Ready
      BKECluster.Status.KubernetesVersion = v1.29.0
```

### 6.3 回滚流程状态转换

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         回滚流程状态转换                                      │
└─────────────────────────────────────────────────────────────────────────────┘

集群层:
  BKECluster.Phase: Upgrading → RollingBack → Running
  DeclarativeUpgradeStatus: Upgrading → Failed → (重置)

节点层:
  BKENode.State: Upgrading → Ready (回滚后)

组件层:
  ComponentPhase: Failed → RollingBack → RolledBack
  NodeComponentStatuses: Failed → RollingBack → RolledBack
  ComponentStatuses: Failed → RollingBack → RolledBack

状态转换时序:
  T0: 升级失败
      ComponentPhase = Failed
      NodeComponentStatuses[nodeIP] = Failed
      DeclarativeUpgradeStatus.LastError = "upgrade failed"
      DeclarativeUpgradeStatus.LastFailure = {...}
  
  T1: 触发回滚 (FailurePolicy=Rollback)
      ComponentPhase = RollingBack
      NodeComponentStatuses[nodeIP] = RollingBack
  
  T2: 执行回滚
      Binary: 执行 UninstallScript
      Helm: 执行 helm rollback
      YAML: 删除资源后重新 Apply
  
  T3: 回滚完成
      ComponentPhase = RolledBack
      NodeComponentStatuses[nodeIP] = RolledBack
      BKENode.State = Ready
  
  T4: 重置升级状态
      DeclarativeUpgradeStatus.ResetForTarget(oldVersion)
      BKECluster.Phase = Running
```

### 6.4 扩容流程状态转换

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         扩容流程状态转换                                      │
└─────────────────────────────────────────────────────────────────────────────┘

集群层:
  BKECluster.Phase: Running → (保持不变)
  BKECluster.ClusterStatus: Ready → (保持不变)

节点层:
  BKENode.State: (初始) → Pending → Provisioned → Ready
  BKENode.StateCode: 0 → NodeAgentPushedFlag → NodeAgentReadyFlag → NodeEnvFlag → NodeBootFlag

组件层:
  ComponentPhase: Pending → Installing → Installed
  NodeComponentStatuses: (无记录) → Installing → Installed

状态转换时序:
  T0: 新节点加入
      BKENode.State = Pending
      BKENode.StateCode = 0
  
  T1: Agent 推送
      BKENode.StateCode |= NodeAgentPushedFlag
      Command.Status = Pending → Running
  
  T2: Agent 就绪
      BKENode.State = Provisioned
      BKENode.StateCode |= NodeAgentReadyFlag
      Command.Status = Running → Completed
  
  T3: 环境初始化
      BKENode.StateCode |= NodeEnvFlag
      BKENode.StateCode |= NodeBootFlag
  
  T4: 组件安装
      ComponentPhase = Installing
      NodeComponentStatuses[newNodeIP] = Installing
  
  T5: 组件安装完成
      ComponentPhase = Installed
      NodeComponentStatuses[newNodeIP] = Installed
  
  T6: 节点就绪
      Node.Status.Conditions.Ready = True
      BKENode.State = Ready
```

### 6.5 缩容流程状态转换

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         缩容流程状态转换                                      │
└─────────────────────────────────────────────────────────────────────────────┘

集群层:
  BKECluster.Phase: Running → (保持不变)
  BKECluster.ClusterStatus: Ready → (保持不变)

节点层:
  BKENode.State: Ready → Deleting → NotReady
  BKENode.StateCode: |= NodeDeletingFlag

组件层:
  ComponentPhase: Installed → Uninstalling → Removed
  NodeComponentStatuses: Installed → Uninstalling → Removed
  ComponentStatuses: Installed → Uninstalling → Removed

状态转换时序:
  T0: 节点标记删除
      BKENode.DeletionTimestamp = now
      BKENode.State = Deleting
      BKENode.StateCode |= NodeDeletingFlag
  
  T1: 组件卸载开始
      ComponentPhase = Uninstalling
      NodeComponentStatuses[nodeIP] = Uninstalling
  
  T2: 执行卸载
      Binary: 执行 UninstallScript
      Helm: 执行 helm uninstall
      YAML: 删除资源
  
  T3: 组件卸载完成
      ComponentPhase = Removed
      NodeComponentStatuses[nodeIP] = Removed
  
  T4: 节点删除完成
      BKENode.State = NotReady
      Node.Status.Conditions.Ready = False
```

---

## 7. 重试与幂等性设计

### 7.1 重试机制

#### 7.1.1 自动重试

```go
// 重试配置
type RetryConfig struct {
    MaxRetries     int           // 最大重试次数
    BackoffDelay   time.Duration // 重试间隔
    BackoffFactor  float64       // 退避因子
    MaxBackoff     time.Duration // 最大退避时间
}

// 重试逻辑
func (r *Reconciler) reconcileWithRetry(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 获取当前状态
    cluster := &confv1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
        return ctrl.Result{}, err
    }
    
    // 检查是否需要重试
    if cluster.Status.DeclarativeUpgrade != nil && 
       cluster.Status.DeclarativeUpgrade.LastError != "" {
        // 已失败，检查重试次数
        if cluster.Status.DeclarativeUpgrade.LastFailure != nil &&
           cluster.Status.DeclarativeUpgrade.LastFailure.Attempt >= maxRetries {
            // 达到最大重试次数，等待人工介入
            return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
        }
        
        // 执行重试
        result, err := r.executeDAG(ctx, cluster)
        if err != nil {
            // 更新失败记录
            cluster.Status.DeclarativeUpgrade.MarkFailure(
                componentName, version, err.Error(), metav1.Now())
            r.Status().Update(ctx, cluster)
            
            // 指数退避
            backoff := calculateBackoff(attempt)
            return ctrl.Result{RequeueAfter: backoff}, nil
        }
        
        return result, nil
    }
    
    // 首次执行
    return r.executeDAG(ctx, cluster)
}
```

#### 7.1.2 重试触发条件

| 场景 | 触发条件 | 重试策略 |
|------|---------|---------|
| 组件安装失败 | `ComponentPhase = Failed` | 指数退避，最多 3 次 |
| 节点升级失败 | `BKENode.State = Failed` | 固定间隔 5 分钟，最多 5 次 |
| 集群升级失败 | `DeclarativeUpgrade.LastError != ""` | 指数退避，最多 3 次 |
| 超时失败 | `ComponentPhase = Timeout` | 固定间隔 10 分钟，最多 2 次 |

### 7.2 幂等性设计

#### 7.2.1 幂等性保证

```go
// 幂等性检查
func (r *Reconciler) isIdempotent(ctx context.Context, cluster *confv1beta1.BKECluster) bool {
    // 检查组件是否已完成
    if cluster.Status.DeclarativeUpgrade != nil {
        for _, component := range cluster.Status.DeclarativeUpgrade.Completed {
            if component.Name == componentName && component.Version == version {
                // 已完成，跳过
                return true
            }
        }
    }
    
    // 检查节点组件状态
    if cluster.Status.NodeComponentStatuses != nil {
        if compStatuses, ok := cluster.Status.NodeComponentStatuses[componentName]; ok {
            if status, ok := compStatuses[nodeIP]; ok {
                if status.Phase == "Installed" && status.Version == version {
                    // 已安装到目标版本，跳过
                    return true
                }
            }
        }
    }
    
    return false
}
```

#### 7.2.2 幂等性场景

| 场景 | 幂等性保证 | 实现方式 |
|------|-----------|---------|
| 重复 Reconcile | ✅ | 检查 `DeclarativeUpgrade.Completed` |
| Controller 重启 | ✅ | 从 `DeclarativeUpgrade` 恢复状态 |
| 网络中断后重试 | ✅ | 检查 `NodeComponentStatuses` |
| 人工介入后重试 | ✅ | 清除 `LastError`，重新执行 |

### 7.3 人工介入后重试

#### 7.3.1 人工介入方式

```yaml
# 方式 1: 清除错误状态，触发重试
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: my-cluster
status:
  declarativeUpgrade:
    targetVersion: v2.6.0
    lastError: ""  # 清除错误
    lastFailure: null  # 清除失败记录
```

```yaml
# 方式 2: 通过注解触发重试
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: my-cluster
  annotations:
    bke.bocloud.com/retry-upgrade: "true"
```

#### 7.3.2 人工介入后重试流程

```
T0: 人工介入
    清除 DeclarativeUpgrade.LastError
    清除 DeclarativeUpgrade.LastFailure
    
T1: Reconciler 检测到状态变更
    检查 DeclarativeUpgrade.TargetVersion
    检查 DeclarativeUpgrade.Completed
    
T2: 重新执行 DAG
    跳过已完成的组件
    从失败的组件继续执行
    
T3: 升级完成
    DeclarativeUpgrade.FinishedAt = now
    BKECluster.Phase = Running
```

---

## 8. 场景用例

### 8.1 用例 1: 全新集群安装

**场景描述**：用户创建 BKECluster，系统自动完成集群安装。

**前置条件**：
- BKECluster CRD 已创建
- 节点已加入集群

**执行流程**：

```
1. 用户创建 BKECluster
   kubectl apply -f bkecluster.yaml
   
2. Reconciler 检测到 BKECluster 创建
   BKECluster.Phase = Provisioning
   BKECluster.ClusterStatus = Creating
   
3. 节点加入
   BKENode.State = Pending
   BKENode.StateCode = 0
   
4. Agent 推送
   BKENode.StateCode |= NodeAgentPushedFlag
   Command.Status = Pending → Running → Completed
   
5. Agent 就绪
   BKENode.State = Provisioned
   BKENode.StateCode |= NodeAgentReadyFlag
   
6. 环境初始化
   BKENode.StateCode |= NodeEnvFlag
   BKENode.StateCode |= NodeBootFlag
   
7. 组件安装
   ComponentPhase = Installing
   NodeComponentStatuses[nodeIP] = Installing
   
8. 组件安装完成
   ComponentPhase = Installed
   NodeComponentStatuses[nodeIP] = Installed
   
9. 所有节点就绪
   Node.Status.Conditions.Ready = True
   BKECluster.Phase = Running
   BKECluster.ClusterStatus = Ready
   BKECluster.ClusterHealthState = Healthy
```

**预期结果**：
- BKECluster.Phase = Running
- BKECluster.ClusterStatus = Ready
- BKECluster.ClusterHealthState = Healthy
- 所有 BKENode.State = Ready
- 所有 ComponentPhase = Installed

### 8.2 用例 2: 集群升级（成功）

**场景描述**：用户修改 BKECluster 版本，系统自动完成集群升级。

**前置条件**：
- BKECluster 已安装并运行
- 目标版本已发布

**执行流程**：

```
1. 用户修改 BKECluster 版本
   kubectl patch bkecluster my-cluster --type merge -p '{"spec":{"clusterConfig":{"cluster":{"kubernetesVersion":"v1.29.0"}}}}'
   
2. Reconciler 检测到版本变更
   DeclarativeUpgradeStatus.TargetVersion = v2.6.0
   DeclarativeUpgradeStatus.StartedAt = now
   BKECluster.Phase = Upgrading
   BKECluster.ClusterStatus = Updating
   
3. 组件升级开始
   ComponentPhase = Upgrading
   NodeComponentStatuses[nodeIP] = Upgrading
   
4. 节点升级
   BKENode.State = Upgrading
   
5. 组件升级完成
   ComponentPhase = Installed
   NodeComponentStatuses[nodeIP] = Installed
   DeclarativeUpgradeStatus.Completed = append(...)
   
6. 节点升级完成
   BKENode.State = Ready
   
7. 所有组件升级完成
   DeclarativeUpgradeStatus.FinishedAt = now
   BKECluster.Phase = Running
   BKECluster.ClusterStatus = Ready
   BKECluster.Status.KubernetesVersion = v1.29.0
```

**预期结果**：
- BKECluster.Phase = Running
- BKECluster.ClusterStatus = Ready
- DeclarativeUpgradeStatus.FinishedAt != nil
- BKECluster.Status.KubernetesVersion = v1.29.0

### 8.3 用例 3: 集群升级（失败后重试）

**场景描述**：集群升级失败，系统自动重试，最终成功。

**前置条件**：
- BKECluster 已安装并运行
- 目标版本已发布
- 升级过程中出现临时故障

**执行流程**：

```
1. 用户修改 BKECluster 版本
   DeclarativeUpgradeStatus.TargetVersion = v2.6.0
   BKECluster.Phase = Upgrading
   
2. 组件升级开始
   ComponentPhase = Upgrading
   
3. 组件升级失败
   ComponentPhase = Failed
   DeclarativeUpgradeStatus.LastError = "network timeout"
   DeclarativeUpgradeStatus.LastFailure = {Name: "containerd", Attempt: 1}
   
4. 自动重试 (第 1 次)
   等待 5 秒 (指数退避)
   ComponentPhase = Upgrading
   
5. 组件升级再次失败
   ComponentPhase = Failed
   DeclarativeUpgradeStatus.LastError = "network timeout"
   DeclarativeUpgradeStatus.LastFailure.Attempt = 2
   
6. 自动重试 (第 2 次)
   等待 10 秒 (指数退避)
   ComponentPhase = Upgrading
   
7. 组件升级成功
   ComponentPhase = Installed
   DeclarativeUpgradeStatus.Completed = append(...)
   DeclarativeUpgradeStatus.ClearFailure()
   
8. 所有组件升级完成
   DeclarativeUpgradeStatus.FinishedAt = now
   BKECluster.Phase = Running
```

**预期结果**：
- BKECluster.Phase = Running
- DeclarativeUpgradeStatus.FinishedAt != nil
- DeclarativeUpgradeStatus.LastError = ""
- DeclarativeUpgradeStatus.LastFailure = nil

### 8.4 用例 4: 集群升级（失败后人工介入）

**场景描述**：集群升级失败，达到最大重试次数，人工介入后重试成功。

**前置条件**：
- BKECluster 已安装并运行
- 目标版本已发布
- 升级过程中出现持续故障

**执行流程**：

```
1. 用户修改 BKECluster 版本
   DeclarativeUpgradeStatus.TargetVersion = v2.6.0
   BKECluster.Phase = Upgrading
   
2. 组件升级失败 (多次重试)
   ComponentPhase = Failed
   DeclarativeUpgradeStatus.LastFailure.Attempt = 3 (达到最大重试次数)
   
3. 系统停止自动重试
   Reconciler 检测到 Attempt >= maxRetries
   等待人工介入
   
4. 人工介入
   kubectl patch bkecluster my-cluster --type merge -p '{"status":{"declarativeUpgrade":{"lastError":"","lastFailure":null}}}'
   
5. Reconciler 检测到状态变更
   清除 LastError 和 LastFailure
   重新执行 DAG
   
6. 组件升级成功
   ComponentPhase = Installed
   DeclarativeUpgradeStatus.Completed = append(...)
   
7. 所有组件升级完成
   DeclarativeUpgradeStatus.FinishedAt = now
   BKECluster.Phase = Running
```

**预期结果**：
- BKECluster.Phase = Running
- DeclarativeUpgradeStatus.FinishedAt != nil

### 8.5 用例 5: 节点扩容

**场景描述**：用户添加新节点，系统自动完成节点配置和组件安装。

**前置条件**：
- BKECluster 已安装并运行
- 新节点已加入集群

**执行流程**：

```
1. 新节点加入集群
   BKENode 资源创建
   BKENode.State = Pending
   BKENode.StateCode = 0
   
2. Agent 推送
   BKENode.StateCode |= NodeAgentPushedFlag
   Command.Status = Pending → Running → Completed
   
3. Agent 就绪
   BKENode.State = Provisioned
   BKENode.StateCode |= NodeAgentReadyFlag
   
4. 环境初始化
   BKENode.StateCode |= NodeEnvFlag
   BKENode.StateCode |= NodeBootFlag
   
5. 组件安装
   ComponentPhase = Installing
   NodeComponentStatuses[newNodeIP] = Installing
   
6. 组件安装完成
   ComponentPhase = Installed
   NodeComponentStatuses[newNodeIP] = Installed
   
7. 节点就绪
   Node.Status.Conditions.Ready = True
   BKENode.State = Ready
```

**预期结果**：
- BKENode.State = Ready
- NodeComponentStatuses[newNodeIP].Phase = Installed

### 8.6 用例 6: 节点缩容

**场景描述**：用户删除节点，系统自动完成组件卸载和节点清理。

**前置条件**：
- BKECluster 已安装并运行
- 节点已就绪

**执行流程**：

```
1. 用户删除节点
   kubectl delete bkenode node1
   
2. BKENode 标记删除
   BKENode.DeletionTimestamp = now
   BKENode.State = Deleting
   BKENode.StateCode |= NodeDeletingFlag
   
3. 组件卸载开始
   ComponentPhase = Uninstalling
   NodeComponentStatuses[nodeIP] = Uninstalling
   
4. 执行卸载
   Binary: 执行 UninstallScript
   Helm: 执行 helm uninstall
   YAML: 删除资源
   
5. 组件卸载完成
   ComponentPhase = Removed
   NodeComponentStatuses[nodeIP] = Removed
   
6. 节点删除完成
   BKENode.State = NotReady
   Node.Status.Conditions.Ready = False
   BKENode 资源删除
```

**预期结果**：
- BKENode 资源已删除
- NodeComponentStatuses[nodeIP] 已清除

### 8.7 用例 7: 集群回滚

**场景描述**：集群升级失败，系统自动回滚到上一版本。

**前置条件**：
- BKECluster 升级失败
- FailurePolicy = Rollback

**执行流程**：

```
1. 升级失败
   ComponentPhase = Failed
   DeclarativeUpgradeStatus.LastError = "upgrade failed"
   
2. 触发回滚 (FailurePolicy=Rollback)
   ComponentPhase = RollingBack
   NodeComponentStatuses[nodeIP] = RollingBack
   
3. 执行回滚
   Binary: 执行 UninstallScript
   Helm: 执行 helm rollback
   YAML: 删除资源后重新 Apply
   
4. 回滚完成
   ComponentPhase = RolledBack
   NodeComponentStatuses[nodeIP] = RolledBack
   BKENode.State = Ready
   
5. 重置升级状态
   DeclarativeUpgradeStatus.ResetForTarget(oldVersion)
   BKECluster.Phase = Running
```

**预期结果**：
- BKECluster.Phase = Running
- ComponentPhase = RolledBack
- BKECluster.Status.KubernetesVersion = 旧版本

### 8.8 用例 8: 扩容+升级并发

**场景描述**：新节点加入时，恰好触发版本升级，系统保证幂等性。

**前置条件**：
- BKECluster 已安装并运行
- 新节点已加入集群
- 目标版本已发布

**执行流程**：

```
1. 新节点加入
   BKENode.State = Pending
   
2. 版本变更
   DeclarativeUpgradeStatus.TargetVersion = v2.6.0
   BKECluster.Phase = Upgrading
   
3. Agent 推送到目标版本
   BKENode.StateCode |= NodeAgentPushedFlag
   ComponentPhase = Installing
   NodeComponentStatuses[newNodeIP] = Installing
   NodeComponentStatuses[newNodeIP].Version = v2.6.0
   
4. Agent 就绪
   BKENode.State = Provisioned
   BKENode.StateCode |= NodeAgentReadyFlag
   
5. 组件安装完成
   ComponentPhase = Installed
   NodeComponentStatuses[newNodeIP] = Installed
   
6. 升级 Phase 检查
   isAlreadyAtTarget:
     Version == targetVersion (v2.6.0 == v2.6.0)
     → true → 跳过升级
   
7. 节点就绪
   BKENode.State = Ready
```

**预期结果**：
- BKENode.State = Ready
- NodeComponentStatuses[newNodeIP].Version = v2.6.0
- 升级 Phase 幂等跳过

---

## 附录：状态转换矩阵

### A.1 集群层状态转换矩阵

| 当前状态 | 事件 | 新状态 | 触发者 |
|---------|------|--------|--------|
| (初始) | 创建 BKECluster | `Provisioning` | Reconciler |
| `Provisioning` | 配置完成 | `Provisioned` | Reconciler |
| `Provisioning` | 配置失败 | `Failed` | Reconciler |
| `Provisioned` | 所有节点就绪 | `Running` | Reconciler |
| `Running` | 版本变更 | `Upgrading` | Reconciler |
| `Upgrading` | 升级完成 | `Running` | Reconciler |
| `Upgrading` | 升级失败 | `Failed` | Reconciler |
| `Failed` | 重试 | `Provisioning` / `Upgrading` | 人工介入 |

### A.2 节点层状态转换矩阵

| 当前状态 | 事件 | 新状态 | 触发者 |
|---------|------|--------|--------|
| (初始) | 节点加入 | `Pending` | Reconciler |
| `Pending` | Agent 就绪 | `Provisioned` | Reconciler |
| `Pending` | 配置失败 | `Failed` | Reconciler |
| `Provisioned` | 节点就绪 | `Ready` | Reconciler |
| `Ready` | 版本变更 | `Upgrading` | Reconciler |
| `Upgrading` | 升级完成 | `Ready` | Reconciler |
| `Ready` | 节点删除 | `Deleting` | Reconciler |
| `Deleting` | 删除完成 | `NotReady` | Reconciler |

### A.3 组件层状态转换矩阵

| 当前状态 | 事件 | 新状态 | 触发者 |
|---------|------|--------|--------|
| `Pending` | 开始安装 | `Installing` | Executor |
| `Installing` | 安装成功 | `Installed` | Executor |
| `Installing` | 安装失败 | `Failed` | Executor |
| `Installing` | 超时 | `Timeout` | Executor |
| `Failed` | 重试 | `Installing` | Reconciler |
| `Failed` | 开始回滚 | `RollingBack` | Executor |
| `Timeout` | 重试 | `Installing` | Reconciler |
| `Timeout` | 开始回滚 | `RollingBack` | Executor |
| `RollingBack` | 回滚成功 | `RolledBack` | Executor |
| `RollingBack` | 回滚失败 | `Failed` | Executor |
| `Installed` | 版本变更 | `Upgrading` | Executor |
| `Upgrading` | 升级成功 | `Installed` | Executor |
| `Upgrading` | 升级失败 | `Failed` | Executor |
| `Installed` | 开始卸载 | `Uninstalling` | Executor |
| `Uninstalling` | 卸载成功 | `Removed` | Executor |
| `Uninstalling` | 卸载失败 | `Failed` | Executor |

---

**文档版本**: v2.0  
**维护者**: openFuyao Team

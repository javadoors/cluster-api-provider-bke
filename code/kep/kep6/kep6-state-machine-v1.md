# KEP-6 状态机设计文档 v1

**文档版本**: v1.0  
**状态**: Draft  
**创建日期**: 2026-07-09  
**依赖**: 现有代码实现（api/, controllers/, pkg/）

---

## 目录

1. [概述](#1-概述)
2. [涉及的资源清单](#2-涉及的资源清单)
3. [状态字段总览](#3-状态字段总览)
   - 3.1 集群层状态
   - 3.2 节点层状态
   - 3.3 命令层状态
4. [状态分层模型](#4-状态分层模型)
   - 4.1 集群层（BKECluster）
   - 4.2 节点层（BKENode / BKEMachine）
   - 4.3 命令层（Command）
5. [状态转换图](#5-状态转换图)
   - 5.1 集群状态机
   - 5.2 节点状态机
   - 5.3 节点 StateCode 生命周期
   - 5.4 命令状态机
   - 5.5 声明式升级状态机
6. [重试与幂等机制](#6-重试与幂等机制)
   - 6.1 Command 级别重试
   - 6.2 BKECluster 级别重试（人工介入）
   - 6.3 声明式升级幂等
7. [场景用例](#7-场景用例)
   - 7.1 全新安装
   - 7.2 扩容（Scale-Out）
   - 7.3 缩容（Scale-In）
   - 7.4 升级
   - 7.5 安装/升级失败 → 重试
   - 7.6 删除
8. [单图完整性评估](#8-单图完整性评估)
9. [状态更新机制](#9-状态更新机制)
   - 9.1 状态更新责任矩阵
   - 9.2 集群状态更新流程
   - 9.3 节点状态更新流程
   - 9.4 命令状态更新流程
   - 9.5 持久化机制
   - 9.6 状态更新触发条件
10. [状态间关系](#10-状态间关系)
   - 10.1 状态依赖关系图
   - 10.2 状态聚合规则
   - 10.3 状态因果关系
   - 10.4 状态冲突处理

---

## 1. 概述

本文档基于现有代码实现，梳理 BKE 集群生命周期中的完整状态机，覆盖**安装、升级、扩容、缩容、回滚**五个核心场景。

**设计目标**：
- 明确各资源的状态字段和转换规则
- 提供可视化的状态转换图
- 说明重试与幂等机制
- 给出实际场景用例

**核心发现**：
- 状态维度多（集群3个状态字段 + 节点2个状态字段 + 命令1个状态字段）
- **无法在单张图中完整展现**，采用分层图 + 资源关联图
- 重试机制分三层：Command 自动重试、BKECluster 人工重试、声明式升级幂等

---

## 2. 涉及的资源清单

| 资源名称 | API Group | 版本 | 作用 | 状态字段 |
|---------|-----------|------|------|---------|
| **BKECluster** | bke.bocloud.com | v1beta1 | 集群主资源，管理整个集群生命周期 | Phase, ClusterStatus, ClusterHealthState, Conditions, PhaseStatus, DeclarativeUpgrade |
| **BKENode** | bke.bocloud.com | v1beta1 | 节点资源，记录节点状态和配置 | State, StateCode, Message, NeedSkip |
| **BKEMachine** | infrastructure.cluster.x-k8s.io | v1beta1 | Cluster API 机器资源，管理节点引导 | Ready, Bootstrapped, Addresses, Conditions, Node |
| **Command** | bkeagent.bocloud.com | v1beta1 | 命令资源，在节点上执行具体操作 | Phase, Status, Conditions, LastStartTime, CompletionTime, Succeeded, Failed |
| **ClusterVersion** | config.openfuyao.cn | v1alpha1 | 集群版本资源，声明期望版本 | DesiredVersion, CurrentVersion, Conditions |
| **ReleaseImage** | config.openfuyao.cn | v1alpha1 | 发布镜像资源，定义组件版本 | Components, Version |
| **ComponentVersion** | config.openfuyao.cn | v1alpha1 | 组件版本资源，定义组件配置 | Type, Version, Binary/Helm/YAML/Inline Spec |

---

## 3. 状态字段总览

### 3.1 集群层状态

#### 3.1.1 ClusterStatus（18 个值）

**定义位置**: `api/capbke/v1beta1/bkecluster_consts.go:151-183`

```go
const (
    // 基础状态
    ClusterReady     ClusterStatus = "Ready"
    ClusterUnhealthy ClusterStatus = "Unhealthy"
    ClusterUnknown   ClusterStatus = "Unknown"
    ClusterChecking  ClusterStatus = "Checking"

    // 暂停状态
    ClusterPaused      ClusterStatus = "Paused"
    ClusterPauseFailed ClusterStatus = "PauseFailed"

    // DryRun 状态
    ClusterDryRun       ClusterStatus = "DryRun"
    ClusterDryRunFailed ClusterStatus = "DryRunFailed"

    // 初始化状态
    ClusterInitializing         ClusterStatus = "Initializing"
    ClusterInitializationFailed ClusterStatus = "InitializationFailed"

    // 升级状态
    ClusterUpgrading     ClusterStatus = "Upgrading"
    ClusterUpgradeFailed ClusterStatus = "UpgradeFailed"

    // 扩容状态
    ClusterMasterScalingUp   ClusterStatus = "ScalingMasterNodesUp"
    ClusterMasterScalingDown ClusterStatus = "ScalingMasterNodesDown"
    ClusterWorkerScalingUp   ClusterStatus = "ScalingWorkerNodesUp"
    ClusterWorkerScalingDown ClusterStatus = "ScalingWorkerNodesDown"
    ClusterScaleFailed       ClusterStatus = "ScaleFailed"

    // Addon 部署状态
    ClusterDeployingAddon    ClusterStatus = "DeployingAddon"
    ClusterDeployAddonFailed ClusterStatus = "DeployAddonFailed"

    // 管理状态
    ClusterManaging     ClusterStatus = "Managing"
    ClusterManageFailed ClusterStatus = "ManageFailed"

    // 删除状态
    ClusterDeleting     ClusterStatus = "Deleting"
    ClusterDeleteFailed ClusterStatus = "DeleteFailed"
)
```

#### 3.1.2 ClusterHealthState（9 个值）

**定义位置**: `api/capbke/v1beta1/bkecluster_consts.go:221-231`

```go
const (
    Deploying     ClusterHealthState = "Deploying"
    DeployFailed  ClusterHealthState = "DeployFailed"
    Upgrading     ClusterHealthState = "Upgrading"
    UpgradeFailed ClusterHealthState = "UpgradeFailed"
    Managing      ClusterHealthState = "Managing"
    ManageFailed  ClusterHealthState = "ManageFailed"
    Unhealthy     ClusterHealthState = "Unhealthy"
    Healthy       ClusterHealthState = "Healthy"
    Deleting      ClusterHealthState = "Deleting"
)
```

#### 3.1.3 BKEClusterPhaseStatus（6 个值）

**定义位置**: `api/capbke/v1beta1/bkecluster_consts.go:185-192`

```go
const (
    PhaseSucceeded BKEClusterPhaseStatus = "Succeeded"
    PhaseFailed    BKEClusterPhaseStatus = "Failed"
    PhaseUnknown   BKEClusterPhaseStatus = "Unknown"
    PhaseWaiting   BKEClusterPhaseStatus = "Waiting"
    PhaseRunning   BKEClusterPhaseStatus = "Running"
    PhaseSkipped   BKEClusterPhaseStatus = "Skipped"
)
```

#### 3.1.4 ClusterConditionType（19 个值）

**定义位置**: `api/capbke/v1beta1/bkecluster_consts.go:108-134`

```go
const (
    ControlPlaneEndPointSetCondition ClusterConditionType = "ControlPlaneEndPointSet"
    TargetClusterReadyCondition      ClusterConditionType = "TargetClusterReady"
    TargetClusterBootCondition       ClusterConditionType = "TargetClusterBoot"
    ClusterAddonCondition            ClusterConditionType = "Addon"
    NodesInfoCondition               ClusterConditionType = "NodesInfo"
    BKEAgentCondition                ClusterConditionType = "BKEAgent"
    LoadBalancerCondition            ClusterConditionType = "LoadBalancer"
    NodesEnvCondition                ClusterConditionType = "NodesEnv"
    ClusterAPIObjCondition           ClusterConditionType = "ClusterAPIObj"
    SwitchBKEAgentCondition          ClusterConditionType = "SwitchBKEAgent"
    ControlPlaneInitializedCondition ClusterConditionType = "ControlPlaneInitialized"
    BKEConfigCondition               ClusterConditionType = "BKEConfig"
    ClusterHealthyStateCondition     ClusterConditionType = "ClusterHealthyState"
    NodesPostProcessCondition        ClusterConditionType = "NodesPostProcess"
    BootstrapSucceededCondition      ClusterConditionType = "BootstrapSucceeded"

    // Bocloud 集群专用
    BocloudClusterDataBackupCondition             ClusterConditionType = "BocloudClusterDataBackup"
    BocloudClusterMasterCertDistributionCondition ClusterConditionType = "BocloudClusterMasterCertDistribution"
    BocloudClusterWorkerCertDistributionCondition ClusterConditionType = "BocloudClusterWorkerCertDistribution"
    BocloudClusterEnvInitCondition                ClusterConditionType = "BocloudClusterEnvInit"
    TypeOfManagementClusterGuessCondition         ClusterConditionType = "TypeOfManagementClusterGuess"
    InternalSpecChangeCondition                   ClusterConditionType = "InternalSpecChange"
)
```

### 3.2 节点层状态

#### 3.2.1 NodeState（BKENode，7 个值）

**定义位置**: `api/bkecommon/v1beta1/bkenode_types.go:34-43`

```go
const (
    NodeNotReady    NodeState = "NotReady"
    NodeReady       NodeState = "Ready"
    NodePending     NodeState = "Pending"
    NodeFailed      NodeState = "Failed"
    NodeDeleting    NodeState = "Deleting"
    NodeUpgrading   NodeState = "Upgrading"
    NodeProvisioned NodeState = "Provisioned"
)
```

#### 3.2.2 NodeState（BKEMachine/Bootstrap，16 个值）

**定义位置**: `api/capbke/v1beta1/bkecluster_consts.go:196-219`

```go
const (
    NodeUnknown         NodeState = "Unknown"
    NodeInitializing    NodeState = "Initializing"
    NodeInitFailed      NodeState = "InitFailed"
    NodeBootStrapping   NodeState = "BootStrapping"
    NodeBootStrapFailed NodeState = "BootStrapFailed"
    NodeDeleting        NodeState = "Deleting"
    NodeDeleteFailed    NodeState = "DeleteFailed"
    NodeUpgrading       NodeState = "Upgrading"
    NodeUpgradeFailed   NodeState = "UpgradeFailed"
    NodeReady           NodeState = "Ready"
    NodeNotReady        NodeState = "NotReady"
    NodeManaging        NodeState = "Managing"
    NodeManageFailed    NodeState = "ManageFailed"
    EtcdUpgrading       NodeState = "Upgrading"
    EtcdUpgradeFailed   NodeState = "UpgradeFailed"
)
```

#### 3.2.3 StateCode（位标记，10 位）

**定义位置**: `api/capbke/v1beta1/bkecluster_consts.go:233-246`

```go
const (
    NodeAgentPushedFlag   = 1 << iota  // bit 0 = 1    (EnsureBKEAgent 阶段)
    NodeAgentReadyFlag                 // bit 1 = 2    (bkeagent 健康检查通过)
    NodeEnvFlag                        // bit 2 = 4    (EnsureNodesEnv 阶段)
    NodeBootFlag                       // bit 3 = 8    (bootstrap 命令成功)
    NodeHAFlag                         // bit 4 = 16   (高可用标记)
    MasterInitFlag                     // bit 5 = 32   (master init 完成)
    NodeDeletingFlag                   // bit 6 = 64   (节点删除中)
    NodeFailedFlag                     // bit 7 = 128  (节点失败)
    NodeStateNeedRecord                // bit 8 = 256  (状态需要记录)
    NodePostProcessFlag                // bit 9 = 512  (后处理完成)
)
```

**关键常量**:
```go
bootstrapReadyStateCode = 527  // = 1+2+4+8+32+64+256+128 = bits 0,1,2,3,5,6,7,8
```

### 3.3 命令层状态

#### 3.3.1 CommandPhase（7 个值）

**定义位置**: `api/bkeagent/v1beta1/command_types.go:34-42`

```go
const (
    CommandPending  CommandPhase = "Pending"
    CommandRunning  CommandPhase = "Running"
    CommandComplete CommandPhase = "Completed"
    CommandSuspend  CommandPhase = "Suspend"
    CommandSkip     CommandPhase = "Skip"
    CommandFailed   CommandPhase = "Failed"
    CommandUnKnown  CommandPhase = "unKnown"
)
```

#### 3.3.2 CommandType（3 个值）

**定义位置**: `api/bkeagent/v1beta1/command_types.go:23-29`

```go
const (
    CommandBuiltIn    CommandType = "BuiltIn"
    CommandShell      CommandType = "Shell"
    CommandKubernetes CommandType = "Kubernetes"
)
```

---

## 4. 状态分层模型

### 4.1 集群层（BKECluster）

```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                                │
├─────────────────────────────────────────────────────────────────┤
│  Status:                                                         │
│    ├── Phase: BKEClusterPhase (当前阶段名称)                     │
│    ├── ClusterStatus: ClusterStatus (操作状态)                   │
│    ├── ClusterHealthState: ClusterHealthState (健康状态)         │
│    ├── PhaseStatus: []PhaseState (各阶段状态)                    │
│    │     └── PhaseState:                                         │
│    │           ├── Name: BKEClusterPhase                         │
│    │           ├── Status: BKEClusterPhaseStatus                 │
│    │           ├── StartTime: *metav1.Time                       │
│    │           ├── EndTime: *metav1.Time                         │
│    │           └── Message: string                               │
│    ├── Conditions: []ClusterCondition                            │
│    │     └── ClusterCondition:                                   │
│    │           ├── Type: ClusterConditionType                    │
│    │           ├── Status: ConditionStatus (True/False/Unknown)  │
│    │           ├── LastTransitionTime: *metav1.Time              │
│    │           ├── Reason: string                                │
│    │           └── Message: string                               │
│    └── DeclarativeUpgrade: *DeclarativeUpgradeStatus             │
│          ├── TargetVersion: string                               │
│          ├── StartedAt: *metav1.Time                             │
│          ├── FinishedAt: *metav1.Time                            │
│          ├── LastError: string                                   │
│          ├── LastFailure: *DeclarativeUpgradeFailureRecord       │
│          └── Completed: []DeclarativeUpgradeComponentRecord      │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 节点层（BKENode / BKEMachine）

```
┌─────────────────────────────────────────────────────────────────┐
│                         BKENode                                  │
├─────────────────────────────────────────────────────────────────┤
│  Status:                                                         │
│    ├── State: NodeState (节点状态)                               │
│    ├── StateCode: int (位标记，10 位)                            │
│    ├── Message: string (状态消息)                                │
│    └── NeedSkip: bool (是否跳过)                                 │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                        BKEMachine                                │
├─────────────────────────────────────────────────────────────────┤
│  Status:                                                         │
│    ├── Ready: bool (是否就绪)                                    │
│    ├── Bootstrapped: bool (是否已引导)                           │
│    ├── Addresses: []MachineAddress (节点地址)                    │
│    ├── Conditions: clusterv1.Conditions (条件)                   │
│    └── Node: *confv1beta1.Node (节点配置)                        │
└─────────────────────────────────────────────────────────────────┘
```

### 4.3 命令层（Command）

```
┌─────────────────────────────────────────────────────────────────┐
│                          Command                                 │
├─────────────────────────────────────────────────────────────────┤
│  Spec:                                                           │
│    ├── NodeName: string (目标节点)                               │
│    ├── Suspend: bool (是否暂停)                                  │
│    ├── Commands: []ExecCommand (命令列表)                        │
│    ├── BackoffLimit: int (重试次数)                              │
│    ├── ActiveDeadlineSecond: int (超时时间)                      │
│    ├── TTLSecondsAfterFinished: int (完成后清理时间)             │
│    └── NodeSelector: *metav1.LabelSelector (节点选择器)          │
│                                                                  │
│  Status: map[string]*CommandStatus (按节点分组的状态)            │
│    └── CommandStatus:                                            │
│          ├── Phase: CommandPhase (命令阶段)                      │
│          ├── Status: metav1.ConditionStatus (执行状态)           │
│          ├── Conditions: []*Condition (各命令条件)               │
│          │     └── Condition:                                    │
│          │           ├── ID: string (命令 ID)                    │
│          │           ├── Status: metav1.ConditionStatus          │
│          │           ├── Phase: CommandPhase                     │
│          │           ├── LastStartTime: *metav1.Time             │
│          │           ├── StdOut: []string                        │
│          │           ├── StdErr: []string                        │
│          │           └── Count: int (执行次数)                   │
│          ├── LastStartTime: *metav1.Time                         │
│          ├── CompletionTime: *metav1.Time                        │
│          ├── Succeeded: int (成功数)                             │
│          └── Failed: int (失败数)                                │
└─────────────────────────────────────────────────────────────────┘
```

---

## 5. 状态转换图

### 5.1 集群状态机

```
┌─────────────────────────────────────────────────────────────────────┐
│                      BKECluster 状态转换图                           │
└─────────────────────────────────────────────────────────────────────┘

                         ┌──────────────┐
                         │   Unknown    │
                         └──────┬───────┘
                                │
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
          ┌─────────────────┐     ┌─────────────────┐
          │  Initializing   │     │    Checking     │
          └────────┬────────┘     └────────┬────────┘
                   │                       │
         ┌─────────┴─────────┐             │
         │                   │             │
         ▼                   ▼             ▼
┌─────────────────┐ ┌─────────────────┐ ┌──────────────┐
│  DeployingAddon │ │     Ready       │ │  Unhealthy   │
└────────┬────────┘ └────────┬────────┘ └──────────────┘
         │                   │
         │         ┌─────────┴─────────┬───────────────┬──────────────┐
         │         │                   │               │              │
         │         ▼                   ▼               ▼              ▼
         │  ┌─────────────┐   ┌──────────────┐ ┌─────────────┐ ┌──────────┐
         │  │  Upgrading  │   │   Managing   │ │  Deleting   │ │  Paused  │
         │  └──────┬──────┘   └──────┬───────┘ └──────┬──────┘ └────┬─────┘
         │         │                 │                │             │
         │         │                 │                │             │
         │         ▼                 ▼                ▼             ▼
         │  ┌─────────────┐   ┌──────────────┐ ┌─────────────┐ ┌──────────┐
         │  │  ScalingUp  │   │    Ready     │ │  Deleted    │ │  Resume  │
         │  │ ScalingDown │   │              │ │             │ │  Ready   │
         │  └──────┬──────┘   └──────────────┘ └─────────────┘ └──────────┘
         │         │
         │         └──────────┬──────────┐
         │                    │          │
         │                    ▼          ▼
         │             ┌──────────┐ ┌──────────┐
         │             │  Ready   │ │ Failed   │
         │             └──────────┘ └──────────┘
         │
         └──────────┬──────────┐
                    │          │
                    ▼          ▼
             ┌──────────┐ ┌──────────┐
             │  Ready   │ │ Failed   │
             └──────────┘ └──────────┘

失败状态转换：
  Initializing → InitializationFailed
  DeployingAddon → DeployAddonFailed
  Upgrading → UpgradeFailed
  ScalingUp/Down → ScaleFailed
  Managing → ManageFailed
  Deleting → DeleteFailed
  Paused → PauseFailed
  DryRun → DryRunFailed
```

### 5.2 节点状态机

```
┌─────────────────────────────────────────────────────────────────────┐
│                        BKENode 状态转换图                            │
└─────────────────────────────────────────────────────────────────────┘

                         ┌──────────────┐
                         │   Pending    │
                         └──────┬───────┘
                                │
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
          ┌─────────────────┐     ┌─────────────────┐
          │  BootStrapping  │     │   Initializing  │
          └────────┬────────┘     └────────┬────────┘
                   │                       │
         ┌─────────┴─────────┐             │
         │                   │             │
         ▼                   ▼             ▼
┌─────────────────┐ ┌─────────────────┐ ┌──────────────┐
│     Ready       │ │   NotReady      │ │ InitFailed   │
└────────┬────────┘ └────────┬────────┘ └──────────────┘
         │                   │
         │         ┌─────────┴─────────┬───────────────┐
         │         │                   │               │
         │         ▼                   ▼               ▼
         │  ┌─────────────┐   ┌──────────────┐ ┌─────────────┐
         │  │  Upgrading  │   │    Failed    │ │  Deleting   │
         │  └──────┬──────┘   └──────┬───────┘ └──────┬──────┘
         │         │                 │                │
         │         │                 │                │
         │         ▼                 ▼                ▼
         │  ┌─────────────┐   ┌──────────────┐ ┌─────────────┐
         │  │    Ready    │   │    Retry     │ │  Deleted    │
         │  └─────────────┘   └──────────────┘ └─────────────┘
         │
         └──────────┬──────────┐
                    │          │
                    ▼          ▼
             ┌──────────┐ ┌──────────┐
             │ NotReady │ │ Managing │
             └──────────┘ └──────────┘

BKEMachine 引导状态：
  Unknown → Initializing → BootStrapping → Ready / BootStrapFailed
                                            ↓
                                      Ready (StateCode=527)
```

### 5.3 节点 StateCode 生命周期

```
┌─────────────────────────────────────────────────────────────────────┐
│                    BKENode StateCode 生命周期                        │
└─────────────────────────────────────────────────────────────────────┘

StateCode = 0 (初始状态)
    │
    │ EnsureBKEAgent 阶段
    │ bkeagent 推送成功
    ▼
StateCode |= NodeAgentPushedFlag (bit 0, value=1)
    │
    │ bkeagent 健康检查通过
    ▼
StateCode |= NodeAgentReadyFlag (bit 1, value=2)
    │
    │ EnsureNodesEnv 阶段
    │ 环境初始化完成
    ▼
StateCode |= NodeEnvFlag (bit 2, value=4)
    │
    │ Bootstrap 命令成功
    ▼
StateCode |= NodeBootFlag (bit 3, value=8)
    │
    │ Master 节点 InitControlPlane
    ▼
StateCode |= MasterInitFlag (bit 5, value=32)
    │
    │ EnsureNodesPostProcess 阶段
    ▼
StateCode |= NodePostProcessFlag (bit 9, value=512)
    │
    ▼
StateCode = 527 (bootstrapReadyStateCode)
    = 1 + 2 + 4 + 8 + 32 + 512
    = bits 0,1,2,3,5,9

异常状态：
  任意阶段失败 → StateCode |= NodeFailedFlag (bit 7, value=128)
  节点删除中 → StateCode |= NodeDeletingFlag (bit 6, value=64)

重试机制：
  人工介入 → 清除 NodeFailedFlag → 重新执行失败阶段
```

### 5.4 命令状态机

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Command 状态转换图                            │
└─────────────────────────────────────────────────────────────────────┘

                         ┌──────────────┐
                         │   Pending    │
                         └──────┬───────┘
                                │
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
          ┌─────────────────┐     ┌─────────────────┐
          │    Running      │     │     Suspend     │
          └────────┬────────┘     └────────┬────────┘
                   │                       │
         ┌─────────┴─────────┐             │
         │                   │             │
         ▼                   ▼             ▼
┌─────────────────┐ ┌─────────────────┐ ┌──────────────┐
│   Completed     │ │     Failed      │ │    Resume    │
└─────────────────┘ └────────┬────────┘ └──────┬───────┘
                             │                 │
                             │                 │
                             ▼                 ▼
                      ┌──────────────┐  ┌──────────────┐
                      │  BackoffRetry│  │   Running    │
                      │ (Count++)    │  └──────────────┘
                      └──────┬───────┘
                             │
                    ┌────────┴────────┐
                    │                 │
                    ▼                 ▼
             ┌──────────┐     ┌──────────┐
             │Completed │     │  Failed  │
             │          │     │(超过限制)│
             └──────────┘     └──────────┘

重试逻辑：
  for backoffLimit >= 0 && condition.Count <= backoffLimit:
    condition.Count++
    executeByType()
    if error:
      condition.Status = ConditionFalse
      condition.Phase = CommandFailed
      continue
    else:
      condition.Status = ConditionTrue
      condition.Phase = CommandComplete
      break

  if failed && BackoffIgnore:
    Phase = CommandSkip
```

### 5.5 声明式升级状态机

```
┌─────────────────────────────────────────────────────────────────────┐
│                 DeclarativeUpgrade 状态转换图                        │
└─────────────────────────────────────────────────────────────────────┘

DeclarativeUpgradeStatus:
  TargetVersion: string          // 目标版本
  StartedAt: *metav1.Time        // 开始时间
  FinishedAt: *metav1.Time       // 完成时间
  LastError: string              // 最后错误
  LastFailure: *FailureRecord    // 最后失败记录
  Completed: []ComponentRecord   // 已完成组件列表

状态转换：

1. 初始化
   EnsureInitialized(targetVersion, now)
     ├─ if TargetVersion != targetVersion:
     │     ResetForTarget(targetVersion, now)
     │     └─ TargetVersion = targetVersion
     │     └─ StartedAt = now
     │     └─ FinishedAt = nil
     │     └─ LastError = ""
     │     └─ LastFailure = nil
     │     └─ Completed = nil
     └─ return true (表示重置)

2. 组件执行
   for each component in DAG:
     ├─ if IsCompleted(name, version):
     │     skip (幂等)
     └─ executeComponent()
           ├─ if success:
           │     MarkCompleted(name, version, now)
           │     └─ append to Completed
           │     └─ clear LastError
           │     └─ clear LastFailure
           └─ if failed:
                 MarkFailure(name, version, errMsg, now)
                 └─ LastFailure.Name = name
                 └─ LastFailure.Version = version
                 └─ LastFailure.FailedAt = now
                 └─ LastFailure.Error = errMsg
                 └─ LastFailure.Attempt++ (连续失败)
                 └─ LastError = errMsg

3. 完成
   completeDeclarativeUpgrade()
     ├─ FinishedAt = now
     ├─ LastError = ""
     ├─ ClearFailure()
     └─ remove upgrade-ready annotation

4. 目标版本变更
   if TargetVersion changes:
     ResetForTarget(newTarget, now)
     └─ 清除所有进度
     └─ 重新开始升级
```

---

## 6. 重试与幂等机制

### 6.1 Command 级别重试

**定义位置**: `controllers/bkeagent/command_controller.go:464-493`

```go
func (r *CommandReconciler) executeWithRetry(
    execCommand agentv1beta1.ExecCommand,
    condition *agentv1beta1.Condition,
    stopTime time.Time,
    backoffLimit int,
) commandExecutionResult {
    for backoffLimit >= 0 && condition.Count <= backoffLimit {
        // 检查超时
        if stopTime.Before(time.Now()) {
            return commandExecutionResult{timedOut: true}
        }
        
        // 延迟重试
        if execCommand.BackoffDelay != 0 && condition.Count > 0 {
            time.Sleep(time.Duration(execCommand.BackoffDelay) * time.Second)
        }
        
        // 执行命令
        condition.LastStartTime = &metav1.Time{Time: time.Now()}
        condition.Count++
        result, err := r.executeByType(execCommand.Type, execCommand.Command)
        
        if err != nil {
            condition.Status = metav1.ConditionFalse
            condition.Phase = agentv1beta1.CommandFailed
            condition.StdErr = append(condition.StdErr, err.Error())
            continue  // 继续重试
        }
        
        // 成功
        condition.Status = metav1.ConditionTrue
        condition.Phase = agentv1beta1.CommandComplete
        condition.StdOut = append(condition.StdOut, result...)
        break  // 跳出循环
    }
    
    return commandExecutionResult{timedOut: false}
}
```

**默认配置**:
```go
const (
    DefaultBackoffLimit            = 3      // 默认重试 3 次
    DefaultActiveDeadlineSecond    = 1000   // 默认超时 1000 秒
    DefaultTTLSecondsAfterFinished = 600    // 完成后 600 秒清理
)
```

**Rate Limiter**:
```go
const (
    defaultFastDelay       = 10 * time.Second   // 快速重试间隔
    defaultSlowDelay       = 60 * time.Second   // 慢速重试间隔
    defaultMaxFastAttempts = 5                  // 最大快速重试次数
)
```

### 6.2 BKECluster 级别重试（人工介入）

**定义位置**: `controllers/capbke/bkecluster_controller.go:660-744`

**触发方式**:
```bash
# 重试所有失败节点
kubectl annotate bkecluster my-cluster bke.bocloud.com/retry=""

# 重试特定节点
kubectl annotate bkecluster my-cluster bke.bocloud.com/retry="10.0.0.1,10.0.0.2"
```

**处理逻辑**:
```go
func (r *BKEClusterReconciler) handleRetryLogic(bkeCluster *bkev1beta1.BKECluster) {
    // 检查 retry 注解
    if retryNodeIPs, ok := annotation.HasAnnotation(bkeCluster, annotation.RetryAnnotationKey); ok {
        if retryNodeIPs == "" {
            // 重试所有节点
            r.processAllNodesRetry(bkeCluster)
        } else {
            // 重试特定节点
            r.processSpecificNodesRetry(bkeCluster, retryNodeIPs)
        }
        
        // 移除 retry 注解
        annotation.RemoveAnnotation(bkeCluster, annotation.RetryAnnotationKey)
    }
}

func (r *BKEClusterReconciler) processAllNodesRetry(bkeCluster *bkev1beta1.BKECluster) {
    // 清除所有节点的 NodeFailedFlag
    for _, node := range bkeCluster.Status.Nodes {
        node.StateCode &= ^bkev1beta1.NodeFailedFlag
    }
    
    // 重置 status manager 缓存
    r.StatusManager.ResetCache()
}

func (r *BKEClusterReconciler) processSpecificNodesRetry(bkeCluster *bkev1beta1.BKECluster, nodeIPs string) {
    // 清除特定节点的 NodeFailedFlag
    ips := strings.Split(nodeIPs, ",")
    for _, node := range bkeCluster.Status.Nodes {
        if utils.ContainsString(ips, node.IP) {
            node.StateCode &= ^bkev1beta1.NodeFailedFlag
        }
    }
    
    // 重置 status manager 缓存
    r.StatusManager.ResetCache()
}
```

**幂等性保证**:
- StateCode 位标记清除是幂等操作
- StatusManager 缓存重置确保重新计算
- Phase 状态重新评估，从 Waiting 阶段开始

### 6.3 声明式升级幂等

**定义位置**: `pkg/dagexec/scheduler.go:276-324`

**跳过已完成组件**:
```go
func (s *Scheduler) shouldSkipComponent(
    ctx context.Context,
    execCtx *ExecutionContext,
    node *topology.ComponentNode,
) bool {
    bc := execCtx.Cluster
    
    // 检查 DeclarativeUpgrade 状态
    if bc.Status.DeclarativeUpgrade != nil {
        st := bc.Status.DeclarativeUpgrade
        
        // 检查组件是否已完成
        if st.IsCompleted(node.Name, s.nodeVersionKey(node)) {
            execCtx.Log.Info("skipping completed component",
                "component", node.Name,
                "version", s.nodeVersionKey(node),
            )
            return true
        }
    }
    
    return false
}
```

**标记组件完成**:
```go
func (s *Scheduler) markComponentCompleted(
    ctx context.Context,
    execCtx *ExecutionContext,
    node *topology.ComponentNode,
) error {
    bc := execCtx.Cluster
    
    if bc.Status.DeclarativeUpgrade != nil {
        st := bc.Status.DeclarativeUpgrade
        
        // 标记完成
        st.MarkCompleted(node.Name, s.nodeVersionKey(node), metav1.Now())
        
        // 清除错误
        st.LastError = ""
        st.ClearFailure()
        
        // 持久化
        return s.client.Status().Update(ctx, bc)
    }
    
    return nil
}
```

**标记组件失败**:
```go
func (s *Scheduler) markComponentFailed(
    ctx context.Context,
    execCtx *ExecutionContext,
    node *topology.ComponentNode,
    err error,
) error {
    bc := execCtx.Cluster
    
    if bc.Status.DeclarativeUpgrade != nil {
        st := bc.Status.DeclarativeUpgrade
        
        // 标记失败
        st.MarkFailure(node.Name, s.nodeVersionKey(node), err.Error(), metav1.Now())
        
        // 持久化
        return s.client.Status().Update(ctx, bc)
    }
    
    return nil
}
```

**幂等性保证**:
- `IsCompleted()` 检查组件+版本是否已完成
- 相同组件+版本不会重复执行
- 目标版本变更时自动重置进度

---

## 7. 场景用例

### 7.1 全新安装

**场景描述**: 创建新的 BKE 集群，从 0 到 Ready 状态

**Phase 执行顺序**:
```
CommonPhases:
  1. EnsureFinalizer
  2. EnsurePaused
  3. EnsureClusterManage
  4. EnsureDeleteOrReset
  5. EnsureDryRun

DeployPhases:
  6. EnsureBKEAgent          → 推送 bkeagent 到所有节点
  7. EnsureNodesEnv          → 初始化节点环境（containerd, 系统配置）
  8. EnsureClusterAPIObj     → 创建 Cluster API 对象
  9. EnsureCerts             → 生成证书
  10. EnsureLoadBalance      → 配置负载均衡
  11. EnsureMasterInit       → 初始化第一个 master 节点
  12. EnsureMasterJoin       → 其他 master 节点加入
  13. EnsureWorkerJoin       → worker 节点加入
  14. EnsureAddonDeploy      → 部署 addon（coredns, kube-proxy）
  15. EnsureNodesPostProcess → 节点后处理
  16. EnsureAgentSwitch      → 切换 bkeagent 监听目标

PostDeployPhases:
  17. EnsureProviderSelfUpgrade
  18. EnsureAgentUpgrade
  19. EnsureContainerdUpgrade
  20. EnsureEtcdUpgrade
  21. EnsureWorkerUpgrade
  22. EnsureMasterUpgrade
  23. EnsureWorkerDelete
  24. EnsureMasterDelete
  25. EnsureComponentUpgrade
  26. EnsureClusterAPIManagerManifest
  27. EnsureCluster
```

**状态转换**:
```
BKECluster:
  Unknown → Initializing → DeployingAddon → Ready

BKENode (每个节点):
  StateCode: 0 → 1 → 3 → 7 → 15 → 47 → 527
  State: Pending → BootStrapping → NotReady → Ready

BKEMachine (每个节点):
  Ready: false → true
  Bootstrapped: false → true

Command (每个节点):
  Phase: Pending → Running → Completed
```

**关键检查点**:
- EnsureBKEAgent: 检查 `NodeAgentPushedFlag`
- EnsureNodesEnv: 检查 `NodeEnvFlag`
- EnsureMasterInit: 检查 `MasterInitFlag`
- 最终: `StateCode == 527`

### 7.2 扩容（Scale-Out）

**场景描述**: 向现有集群添加新节点

**触发方式**:
```bash
# 方式 1: 添加 BKENode 资源
kubectl apply -f new-node.yaml

# 方式 2: 使用预约注解
kubectl annotate bkecluster my-cluster \
  bke.bocloud.com/appointment-add-nodes="10.0.0.10,10.0.0.11"
```

**Phase 执行顺序**:
```
ClusterScaleWorkerUpPhaseNames:
  1. EnsureWorkerJoin  → 新 worker 节点加入

ClusterScaleMasterUpPhaseNames:
  1. EnsureMasterJoin  → 新 master 节点加入
```

**节点过滤逻辑**:
```go
// 获取需要加入的节点
func GetNeedJoinNodesWithBKENodes(bkeCluster, bkeNodes) bkenode.Nodes {
    return filterNodes(bkeCluster,
        func(ip string, bn *confv1beta1.BKENode) bool {
            // 未引导且未初始化
            return !GetNodeStateFlag(bn, ip, bkev1beta1.NodeBootFlag) &&
                !GetNodeStateFlag(bn, ip, bkev1beta1.MasterInitFlag)
        },
        WithExcludeAppointmentNodes(),  // 排除预约节点
    )
}

// 获取预约添加的节点
func GetAppointmentAddNodes(bkeCluster *bkev1beta1.BKECluster) bkenode.Nodes {
    v, found := annotation.HasAnnotation(bkeCluster, annotation.AppointmentAddNodesAnnotationKey)
    if !found {
        return nil
    }
    nodesIP := strings.Split(v, ",")
    // 过滤出预约节点
    // ...
}
```

**状态转换**:
```
BKECluster:
  Ready → ScalingWorkerNodesUp → Ready

BKENode (新节点):
  StateCode: 0 → 527 (完整生命周期)
  State: Pending → BootStrapping → NotReady → Ready

Command (新节点):
  Phase: Pending → Running → Completed
```

**关键检查点**:
- 新节点 `StateCode == 0`
- 预约节点通过注解识别
- 扩容完成后 `StateCode == 527`

### 7.3 缩容（Scale-In）

**场景描述**: 从集群中移除节点

**触发方式**:
```bash
# 删除 BKENode 资源
kubectl delete bkenode node-to-remove
```

**Phase 执行顺序**:
```
ClusterScaleWorkerDownPhaseNames:
  1. EnsureWorkerDelete  → 删除 worker 节点

ClusterScaleMasterDownPhaseNames:
  1. EnsureMasterDelete  → 删除 master 节点
```

**节点删除流程**:
```go
// 标记节点删除
case bkenode.RemoveNode:
    if err := r.NodeFetcher.UpdateBKENodeState(ctx, bkeCluster.Namespace, bkeCluster.Name,
        t.Node.IP, confv1beta1.NodeDeleting, "Node marked for deletion"); err != nil {
        // ...
    }
    
    // 设置 StateCode
    bkeNode.Status.StateCode |= bkev1beta1.NodeDeletingFlag
```

**状态转换**:
```
BKECluster:
  Ready → ScalingWorkerNodesDown → Ready

BKENode (被删除节点):
  StateCode |= NodeDeletingFlag (bit 6)
  State: Ready → Deleting → (removed)

BKEMachine:
  Ready: true → false
  Bootstrapped: true → false
```

**关键检查点**:
- `NodeDeletingFlag` 设置
- 节点从 Cluster API 中移除
- BKENode 资源删除

### 7.4 升级

**场景描述**: 升级集群版本（如 v2.5.0 → v2.6.0）

**触发方式**:
```bash
# 方式 1: 修改 ClusterVersion
kubectl patch clusterversion my-cluster --type merge \
  -p '{"spec":{"desiredVersion":"v2.6.0"}}'

# 方式 2: 设置 upgrade-ready 注解
kubectl annotate bkecluster my-cluster \
  cvo.openfuyao.cn/upgrade-ready="v2.6.0"
```

**声明式升级流程**:
```
1. ClusterVersion 控制器检测到 desiredVersion 变更
   └─ 设置 upgrade-ready 注解

2. BKECluster 控制器检测到 upgrade-ready 注解
   └─ shouldUseDeclarativeUpgrade() = true
   └─ executeUpgradeDAG()

3. 初始化 DeclarativeUpgrade 状态
   └─ EnsureInitialized(targetVersion, now)
   └─ TargetVersion = "v2.6.0"
   └─ StartedAt = now

4. 构建升级 DAG
   └─ 从 ReleaseImage 获取组件列表
   └─ 构建 DAG 依赖关系

5. 执行 DAG
   └─ for each batch in TopologicalBatches():
        └─ for each component in batch:
             ├─ if IsCompleted(name, version): skip
             └─ executeComponent()
                  ├─ if success: MarkCompleted()
                  └─ if failed: MarkFailure()

6. 完成升级
   └─ FinishedAt = now
   └─ LastError = ""
   └─ ClearFailure()
   └─ remove upgrade-ready annotation
```

**状态转换**:
```
BKECluster:
  Ready → Upgrading → Ready
  ClusterStatus: Ready → Upgrading → Ready
  DeclarativeUpgrade:
    TargetVersion: "v2.5.0" → "v2.6.0"
    StartedAt: nil → now
    FinishedAt: nil → now
    Completed: [] → [component1, component2, ...]

BKENode (每个节点):
  State: Ready → Upgrading → Ready

Command (每个节点):
  Phase: Pending → Running → Completed
```

**幂等性保证**:
- `IsCompleted()` 检查组件+版本
- 相同组件不会重复执行
- 目标版本变更时自动重置

### 7.5 安装/升级失败 → 重试

**场景描述**: 安装或升级过程中某个节点失败，人工介入后重试

**失败检测**:
```go
// 节点失败时设置标志
bkeNode.Status.StateCode |= bkev1beta1.NodeFailedFlag
bkeNode.Status.State = confv1beta1.NodeFailed
bkeNode.Status.Message = "Bootstrap failed: xxx"
```

**人工介入**:
```bash
# 1. 分析问题
kubectl get bkecluster my-cluster -o yaml
kubectl get bkenode -o yaml
kubectl logs -n bke-system deployment/bke-controller-manager

# 2. 解决问题（如修复配置、增加资源）

# 3. 触发重试
kubectl annotate bkecluster my-cluster bke.bocloud.com/retry=""
# 或重试特定节点
kubectl annotate bkecluster my-cluster bke.bocloud.com/retry="10.0.0.1"
```

**重试流程**:
```
1. BKECluster 控制器检测到 retry 注解
   └─ handleRetryLogic()

2. 清除失败标志
   └─ processAllNodesRetry() 或 processSpecificNodesRetry()
   └─ StateCode &= ^NodeFailedFlag

3. 重置缓存
   └─ StatusManager.ResetCache()

4. 移除注解
   └─ annotation.RemoveAnnotation(bkeCluster, RetryAnnotationKey)

5. 重新执行失败阶段
   └─ executePhaseFlow()
   └─ 从 Waiting 阶段开始
```

**状态转换**:
```
BKECluster:
  InitializationFailed → Initializing → Ready
  UpgradeFailed → Upgrading → Ready

BKENode (失败节点):
  StateCode: 527|128 → 527 (清除 NodeFailedFlag)
  State: Failed → BootStrapping → Ready

Command (失败节点):
  Phase: Failed → Running → Completed
```

**幂等性保证**:
- StateCode 位标记清除是幂等操作
- StatusManager 缓存重置确保重新计算
- Phase 状态重新评估

### 7.6 删除

**场景描述**: 删除整个集群

**触发方式**:
```bash
kubectl delete bkecluster my-cluster
```

**Phase 执行顺序**:
```
ClusterDeletePhaseNames:
  1. EnsureDeleteOrReset  → 删除集群资源
```

**删除流程**:
```
1. 检测到 DeletionTimestamp
   └─ shouldDelete() = true

2. 执行删除阶段
   └─ EnsureDeleteOrReset.Execute()
   └─ 删除 Cluster API 对象
   └─ 删除节点资源
   └─ 清理证书

3. 移除 Finalizer
   └─ removeFinalizer()
   └─ 允许资源删除
```

**状态转换**:
```
BKECluster:
  Ready → Deleting → (deleted)
  ClusterHealthState: Healthy → Deleting

BKENode (所有节点):
  StateCode |= NodeDeletingFlag
  State: Ready → Deleting → (removed)

BKEMachine (所有节点):
  Ready: true → false
  Bootstrapped: true → false
```

---

## 8. 单图完整性评估

### 8.1 维度分析

| 维度 | 状态字段数量 | 状态值数量 | 复杂度 |
|------|-------------|-----------|--------|
| 集群层 | 3 (Phase, ClusterStatus, ClusterHealthState) | 18 + 9 + 6 = 33 | 高 |
| 节点层 | 2 (State, StateCode) | 7 + 16 + 10位 = 33 | 高 |
| 命令层 | 1 (Phase) | 7 | 中 |
| 升级层 | 5 (TargetVersion, StartedAt, FinishedAt, LastError, Completed) | N/A | 高 |

**总计**: 11 个状态字段，73+ 个状态值

### 8.2 单图可行性

**结论**: **无法在单张图中完整展现**

**原因**:
1. **维度太多**: 11 个状态字段，无法在 2D 图中清晰展示
2. **状态值太多**: 73+ 个状态值，图表会过于复杂
3. **层次复杂**: 集群、节点、命令、升级四层状态相互关联
4. **转换规则复杂**: 每个状态字段有独立的转换规则

### 8.3 推荐方案

**采用分层图 + 资源关联图**:

```
┌─────────────────────────────────────────────────────────────────┐
│                    资源关联图（顶层）                             │
└─────────────────────────────────────────────────────────────────┘

BKECluster ──1:N──> BKENode ──1:1──> BKEMachine
    │                    │
    │                    └──1:N──> Command
    │
    └──1:1──> ClusterVersion ──1:1──> ReleaseImage ──1:N──> ComponentVersion

┌─────────────────────────────────────────────────────────────────┐
│                    集群状态机（第 5.1 节）                        │
└─────────────────────────────────────────────────────────────────┘

[ClusterStatus 状态转换图]

┌─────────────────────────────────────────────────────────────────┐
│                    节点状态机（第 5.2 节）                        │
└─────────────────────────────────────────────────────────────────┘

[BKENode State 状态转换图]

┌─────────────────────────────────────────────────────────────────┐
│                节点 StateCode 生命周期（第 5.3 节）               │
└─────────────────────────────────────────────────────────────────┘

[StateCode 位标记累积图]

┌─────────────────────────────────────────────────────────────────┐
│                    命令状态机（第 5.4 节）                        │
└─────────────────────────────────────────────────────────────────┘

[CommandPhase 状态转换图]

┌─────────────────────────────────────────────────────────────────┐
│                声明式升级状态机（第 5.5 节）                      │
└─────────────────────────────────────────────────────────────────┘

[DeclarativeUpgrade 状态转换图]
```

### 8.4 替代方案

如果需要单图展示，可以采用**简化版状态机**:

```
┌─────────────────────────────────────────────────────────────────┐
│                    BKE 集群简化状态机                             │
└─────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │   Unknown    │
                    └──────┬───────┘
                           │
                           ▼
                    ┌──────────────┐
                    │ Initializing │
                    └──────┬───────┘
                           │
                    ┌──────┴───────┐
                    │              │
                    ▼              ▼
             ┌──────────┐   ┌──────────┐
             │  Ready   │   │  Failed  │
             └────┬─────┘   └────┬─────┘
                  │              │
         ┌────────┴────────┐     │
         │                 │     │
         ▼                 ▼     │
  ┌─────────────┐   ┌─────────┐  │
  │  Upgrading  │   │ Deleting│  │
  └──────┬──────┘   └────┬────┘  │
         │               │       │
         └───────┬───────┘       │
                 │               │
                 ▼               │
          ┌──────────┐           │
          │  Ready   │◄──────────┘
          └──────────┘   (retry)
```

**简化说明**:
- 合并 ClusterStatus 和 ClusterHealthState
- 隐藏 PhaseStatus 和 Conditions
- 只展示主要状态转换
- 隐藏节点和命令层细节

---

## 9. 状态更新机制

> **本章目标**：说明各状态字段由谁更新、何时更新、如何持久化，以及状态更新的触发条件和原子性保证。

### 9.1 状态更新责任矩阵

下表明确每个状态字段的**更新责任方**、**更新时机**和**更新方式**：

| 资源 | 状态字段 | 更新责任方 | 更新时机 | 更新方式 | 代码位置 |
|------|---------|-----------|---------|---------|---------|
| **BKECluster** | `Phase` | PhaseFlow | 每个 Phase 开始/结束 | `PhaseStatus.SetStatus()` → `SyncStatusUntilComplete()` | `pkg/phaseframe/phaseflow.go` |
| **BKECluster** | `ClusterStatus` | Phase 钩子 + StatusManager | Phase 执行后（pre/post hook） | `calculatingClusterPostStatusByPhase()` → `SyncStatusUntilComplete()` | `pkg/phaseframe/phases/phase_flow.go` |
| **BKECluster** | `ClusterHealthState` | BKEClusterReconciler + StatusManager | Reconcile 开始时 + 失败升级时 | `setClusterHealthStatus()` → `recordBKEClusterStatus()` | `controllers/capbke/bkecluster_controller.go` + `pkg/statusmanage/statusmanager.go` |
| **BKECluster** | `PhaseStatus` | PhaseFlow | 每个 Phase 开始/结束 | `DefaultPreHook()`/`DefaultPostHook()` → `Report()` | `pkg/phaseframe/phaseflow.go` |
| **BKECluster** | `Conditions` | 各 Phase / StatusManager | 条件满足时 | `condition.ConditionMark()` / `condition.ConditionRemove()` | `utils/capbke/condition/` |
| **BKECluster** | `DeclarativeUpgrade` | Scheduler (DAG) | 组件执行成功/失败后 | `markComponentCompleted()` / `markComponentFailed()` → `client.Status().Update()` | `pkg/dagexec/scheduler.go` |
| **BKECluster** | `Ready` / `*Version` | EnsureCluster Phase | 健康检查通过后 | 直接赋值 → `SyncStatusUntilComplete()` | `pkg/phaseframe/phases/ensure_cluster.go` |
| **BKENode** | `State` | NodeFetcher + Phase | 节点操作后（删除/引导完成） | `UpdateBKENodeState()` → `client.Status().Update()` | `utils/capbke/nodeutil/fetcher.go` |
| **BKENode** | `StateCode` | Phase（内存操作）+ NodeFetcher（持久化） | Phase 内位标记操作 → Phase 结束后批量持久化 | 内存：`StateCode \|= flag`；持久化：`UpdateModifiedBKENodes()` | `pkg/mergecluster/bkecluster.go` |
| **BKENode** | `Message` | NodeFetcher + Phase | 状态变化时 | 与 State 一起更新 | `utils/capbke/nodeutil/fetcher.go` |
| **BKEMachine** | `Ready` / `Bootstrapped` | BKEMachineController | 引导完成后 | Cluster API 自动更新 | `controllers/capbke/bkemachine_controller_phases.go` |
| **Command** | `Phase` / `Status` | CommandReconciler | 命令执行时 | `executeWithRetry()` → `syncStatusUntilComplete()` | `controllers/bkeagent/command_controller.go` |
| **Command** | `Conditions[]` | CommandReconciler | 每条子命令执行后 | 直接赋值 → `syncStatusUntilComplete()` | `controllers/bkeagent/command_controller.go` |

### 9.2 集群状态更新流程

#### 9.2.1 ClusterStatus 更新流程

ClusterStatus 的更新分两条路径：**Phase 执行路径**（正常安装/升级）和 **DAG 执行路径**（声明式升级）。

**路径 1：Phase 执行路径**

```
BKEClusterReconciler.Reconcile()
  │
  ├─ executePhaseFlow()
  │     │
  │     ├─ for each phase:
  │     │     │
  │     │     ├─ ExecutePreHook()
  │     │     │     ├─ SetStatus(PhaseRunning)
  │     │     │     ├─ Report() → SyncStatusUntilComplete()    ← 持久化 Phase
  │     │     │     └─ calculatingClusterPreStatusByPhase()
  │     │     │           └─ ClusterStatus = 过渡状态            ← 如 ClusterChecking
  │     │     │
  │     │     ├─ Execute()                                      ← 实际 Phase 逻辑
  │     │     │
  │     │     └─ ExecutePostHook()
  │     │           ├─ SetStatus(PhaseSucceeded / PhaseFailed)
  │     │           ├─ Report() → SyncStatusUntilComplete()     ← 持久化 PhaseStatus
  │     │           └─ calculatingClusterPostStatusByPhase()
  │     │                 ├─ 根据 Phase 分组路由到对应 handler
  │     │                 ├─ err != nil → ClusterStatus = *Failed
  │     │                 └─ err == nil → ClusterStatus = 活跃状态
  │     │
  │     └─ getFinalResult()
  │           └─ StatusManager.GetCtrlResult()
  │                 ├─ 失败次数 ≤ 限制 → 保持当前状态，requeue 重试
  │                 └─ 失败次数 > 限制 → ClusterHealthState 升级为 *Failed
  │
  └─ SyncStatusUntilComplete()                                  ← 最终持久化
```

**ClusterStatus 路由规则**（`calculateClusterStatusByPhase`）：

| Phase 分组 | 成功时 ClusterStatus | 失败时 ClusterStatus |
|-----------|---------------------|---------------------|
| `ClusterInitPhaseNames` | `ClusterInitializing` | `ClusterInitializationFailed` |
| `ClusterScaleMasterUpPhaseNames` | `ClusterMasterScalingUp` | `ClusterScaleFailed` |
| `ClusterScaleWorkerUpPhaseNames` | `ClusterWorkerScalingUp` | `ClusterScaleFailed` |
| `ClusterScaleMasterDownPhaseNames` | `ClusterMasterScalingDown` | `ClusterScaleFailed` |
| `ClusterScaleWorkerDownPhaseNames` | `ClusterWorkerScalingDown` | `ClusterScaleFailed` |
| `ClusterUpgradePhaseNames` | `ClusterUpgrading` | `ClusterUpgradeFailed` |
| `ClusterAddonsPhaseNames` | `ClusterDeployingAddon` | `ClusterDeployAddonFailed` |
| `ClusterManagePhaseNames` | `ClusterManaging` | `ClusterManageFailed` |
| `ClusterDeletePhaseNames` | `ClusterDeleting` | `ClusterDeleteFailed` |

**路径 2：DAG 执行路径**

```
BKEClusterReconciler.executeUpgradeDAG()
  │
  ├─ patchClusterStatus(ClusterUpgrading)     ← 显式 patch
  │
  ├─ Scheduler.ExecuteDAG()
  │     └─ 执行组件...
  │
  ├─ 成功 → patchClusterStatus(ClusterReady)
  │
  └─ 失败 → patchClusterStatus(ClusterUpgradeFailed)
```

#### 9.2.2 ClusterHealthState 更新流程

ClusterHealthState 的更新分三个阶段：

```
阶段 1：初始设置（Reconcile 开始时）
  │
  ├─ handleClusterStatus()
  │     └─ initNodeStatus()
  │           └─ setClusterHealthStatus(flags)
  │                 ├─ DeployFlag → ClusterHealthState = Deploying
  │                 ├─ UpgradeFlag → ClusterHealthState = Upgrading
  │                 ├─ ManageFlag → ClusterHealthState = Managing
  │                 └─ DeleteFlag → ClusterHealthState = Deleting
  │
  ▼
阶段 2：Phase 执行中（健康检查 Phase）
  │
  ├─ EnsureCluster.performHealthCheck()
  │     ├─ 检查通过 → ClusterHealthState = Healthy
  │     └─ 检查失败 → ClusterHealthState = Unhealthy
  │
  ▼
阶段 3：失败升级（StatusManager）
  │
  └─ recordBKEClusterStatus()
        ├─ 失败次数 ≤ ReconcileAllowedFailedCount (默认 10)
        │     └─ 保持上一个正常状态（掩盖失败，继续重试）
        │
        └─ 失败次数 > ReconcileAllowedFailedCount
              ├─ Deploying → ClusterHealthState = DeployFailed
              ├─ Upgrading → ClusterHealthState = UpgradeFailed
              └─ Managing → ClusterHealthState = ManageFailed
```

### 9.3 节点状态更新流程

#### 9.3.1 State 更新

BKENode.State 由 `NodeFetcher.UpdateBKENodeState()` 统一更新：

```
更新触发场景：
  │
  ├─ 节点标记删除
  │     └─ handleNodeChanges() → UpdateBKENodeState(NodeDeleting, "Node marked for deletion")
  │
  ├─ 引导完成
  │     └─ BKEClusterReconciler → State = NodeReady, StateCode = 527
  │
  └─ Phase 内操作
        └─ 各 Phase 直接操作 BKENodes 内存对象 → 最终由 UpdateModifiedBKENodes() 持久化
```

#### 9.3.2 StateCode 更新

StateCode 采用**内存操作 + 批量持久化**模式：

```
Phase 执行中（内存操作）：
  │
  ├─ EnsureBKEAgent:
  │     StateCode &= ^NodeAgentPushedFlag     ← 清除旧标记
  │     StateCode |= NodeAgentPushedFlag      ← 设置新标记
  │     StateCode |= NodeAgentReadyFlag       ← 健康检查通过
  │
  ├─ EnsureNodesEnv:
  │     StateCode |= NodeEnvFlag
  │
  ├─ EnsureClusterManage (bootstrap):
  │     StateCode |= NodeBootFlag
  │
  ├─ EnsureMasterInit:
  │     StateCode |= MasterInitFlag
  │
  ├─ EnsureNodesPostProcess:
  │     StateCode |= NodePostProcessFlag
  │
  └─ 任意阶段失败:
        StateCode |= NodeFailedFlag
        StateCode |= NodeStateNeedRecord      ← 标记需要持久化

Phase 结束后（批量持久化）：
  │
  └─ mergecluster.UpdateModifiedBKENodes()
        ├─ 遍历所有 BKENode
        ├─ 检查 NodeStateNeedRecord 标记
        ├─ StateCode &= ^NodeStateNeedRecord  ← 清除记录标记
        └─ client.Status().Update(node)       ← 持久化到 API Server
```

**关键设计**：`NodeStateNeedRecord` 标记（bit 8）是持久化的触发器。Phase 在内存中修改 StateCode 后，设置此标记；Phase 结束后，`UpdateModifiedBKENodes()` 只持久化带此标记的节点，避免不必要的 API 调用。

### 9.4 命令状态更新流程

Command 状态更新由 `CommandReconciler` 驱动，采用**逐条子命令更新 + 最终聚合**模式：

```
CommandReconciler.Reconcile()
  │
  ├─ 1. ensureStatusInitialized()
  │     └─ Phase = CommandRunning, Status = ConditionUnknown
  │     └─ syncStatusUntilComplete()                    ← 持久化
  │
  ├─ 2. handleSuspend() (如果 Spec.Suspend=true)
  │     └─ Phase = CommandSuspend
  │     └─ syncStatusUntilComplete()                    ← 持久化
  │
  ├─ 3. executeWithRetry() (逐条子命令)
  │     │
  │     └─ for each execCommand:
  │           │
  │           ├─ condition.LastStartTime = now
  │           ├─ condition.Count++
  │           │
  │           ├─ executeByType(type, command)
  │           │     ├─ BuiltIn → 内置命令执行
  │           │     ├─ Shell → SSH 远程执行
  │           │     └─ Kubernetes → K8s API 操作
  │           │
  │           ├─ 成功:
  │           │     condition.Status = ConditionTrue
  │           │     condition.Phase = CommandComplete
  │           │     break (跳出重试循环)
  │           │
  │           └─ 失败:
  │                 condition.Status = ConditionFalse
  │                 condition.Phase = CommandFailed
  │                 continue (继续重试，直到 BackoffLimit)
  │
  ├─ 4. BackoffIgnore 处理
  │     └─ 如果 condition.Status == False && BackoffIgnore == true
  │           condition.Phase = CommandSkip              ← 跳过失败
  │
  └─ 5. finalizeTaskStatus() (聚合)
        │
        ├─ ConditionCount(conditions, commandCount)
        │     ├─ 任意 Failed → Phase = CommandFailed
        │     ├─ 未完成 → Phase = CommandRunning
        │     └─ 全部完成 → Phase = CommandComplete
        │
        ├─ Succeeded = 成功数
        ├─ Failed = 失败数
        ├─ CompletionTime = now
        │
        └─ syncStatusUntilComplete()                    ← 持久化
```

### 9.5 持久化机制

#### 9.5.1 SyncStatusUntilComplete（BKECluster 持久化）

所有 BKECluster 状态持久化最终通过 `SyncStatusUntilComplete()` 完成：

```go
// pkg/mergecluster/bkecluster.go
func SyncStatusUntilComplete(c client.Client, bkeCluster *v1beta1.BKECluster, patchs ...PatchFunc) (err error) {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()
    for {
        err = UpdateCombinedBKECluster(ctx, c, bkeCluster, []string{}, patchs...)
        if err != nil {
            if apierrors.IsConflict(err) { continue }   // 冲突重试
            if apierrors.IsNotFound(err) { break }       // 已删除则放弃
            continue
        }
        break
    }
}
```

**内部流程**：

```
SyncStatusUntilComplete()
  │
  ├─ UpdateCombinedBKECluster()
  │     │
  │     ├─ 1. 从 API Server 获取最新 BKECluster（避免冲突）
  │     │
  │     ├─ 2. 应用 patchs（状态修改函数）
  │     │
  │     ├─ 3. StatusManager.SetStatus()
  │     │     ├─ recordBKEClusterStatus()    ← 失败计数 + 状态升级
  │     │     └─ recordBKENodesStatus()      ← 节点级失败计数
  │     │
  │     ├─ 4. UpdateModifiedBKENodes()       ← 持久化修改过的 BKENode
  │     │
  │     └─ 5. PatchHelper.Patch()            ← 持久化 BKECluster
  │
  └─ 冲突/失败 → 重试（2 分钟超时）
```

#### 9.5.2 syncStatusUntilComplete（Command 持久化）

Command 使用独立的持久化机制，采用**读取-修改-Patch**循环：

```go
// controllers/bkeagent/command_controller.go
func (r *CommandReconciler) syncStatusUntilComplete(cmd *agentv1beta1.Command) (err error) {
    for {
        obj := &agentv1beta1.Command{}
        err = r.APIReader.Get(r.Ctx, client.ObjectKey{...}, obj)  // 从 API Server 读取最新
        objCopy := obj.DeepCopy()
        objCopy.Status[r.commandStatusKey()] = cmd.Status[r.commandStatusKey()]
        err = r.Client.Status().Patch(r.Ctx, objCopy, client.MergeFrom(obj))  // merge-patch
        if err != nil { continue }  // 冲突重试
        break
    }
}
```

#### 9.5.3 持久化方式对比

| 资源 | 持久化方式 | 冲突处理 | 超时 | 代码位置 |
|------|-----------|---------|------|---------|
| BKECluster | `PatchHelper.Patch()` (merge-patch) | 自动重试 | 2 分钟 | `pkg/mergecluster/bkecluster.go` |
| BKENode | `client.Status().Update()` | `retry.RetryOnConflict()` | 默认重试策略 | `utils/capbke/nodeutil/fetcher.go` |
| Command | `client.Status().Patch()` (merge-patch) | 循环重试 | 5 分钟 | `controllers/bkeagent/command_controller.go` |

### 9.6 状态更新触发条件

#### 9.6.1 触发源

| 触发源 | 触发条件 | 影响的状态 |
|--------|---------|-----------|
| **BKECluster Spec 变更** | 用户修改 `spec.nodes` / `spec.clusterConfig` | Phase, ClusterStatus, ClusterHealthState |
| **BKECluster 注解变更** | `bke.bocloud.com/retry` / `cvo.openfuyao.cn/upgrade-ready` | DeclarativeUpgrade, ClusterStatus |
| **BKENode 创建/删除** | 节点加入/离开集群 | BKENode.State, StateCode, ClusterStatus |
| **Command 创建** | Phase 创建命令在节点上执行 | Command.Phase, Command.Status |
| **Command 完成** | bkeagent 执行命令完毕 | BKENode.StateCode, ClusterStatus |
| **Reconcile 循环** | 控制器定期调谐（默认 10 分钟） | 所有状态字段（重新评估） |
| **Watch 事件** | 关联资源变更触发（BKENode/Command/BKEMachine） | 聚合状态 |

#### 9.6.2 节流与去重

| 机制 | 说明 | 代码位置 |
|------|------|---------|
| **Rate Limiter** | 快速重试 10s × 5 次 → 慢速重试 60s | `controllers/capbke/bkecluster_controller.go` |
| **StatusRecord 注解** | 避免重复记录相同状态 | `pkg/statusmanage/statusmanager.go` |
| **NodeStateNeedRecord** | 只持久化修改过的节点 | `pkg/mergecluster/bkecluster.go` |
| **冲突重试** | API Server 冲突时自动重试 | `SyncStatusUntilComplete()` |

#### 9.6.3 原子性保证

| 场景 | 保证方式 |
|------|---------|
| BKECluster + BKENode 同时更新 | `UpdateCombinedBKECluster()` 在同一事务中先更新 BKENode 再 Patch BKECluster |
| 状态 + 注解同时更新 | 在同一个 Patch 请求中完成 |
| 多节点并发更新 | 每个节点独立 `Status().Update()`，带 `retry.RetryOnConflict()` |
| Phase 执行中途崩溃 | 下次 Reconcile 重新评估，Phase 幂等执行 |

---

## 10. 状态间关系

> **本章目标**：说明各状态字段之间的依赖关系、聚合规则、因果传播路径和冲突处理策略。

### 10.1 状态依赖关系图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         状态依赖关系总览                                      │
└─────────────────────────────────────────────────────────────────────────────┘

集群层内部依赖：
  ┌─────────────────────────────────────────────────────┐
  │                                                     │
  │  ClusterStatus ←── Phase (当前执行阶段)              │
  │       │                                             │
  │       ├──← ClusterHealthState (健康视角)             │
  │       │       │                                     │
  │       │       └──← 节点聚合状态 + 组件健康状态         │
  │       │                                             │
  │       └──← Conditions (细粒度条件)                   │
  │                                                     │
  │  PhaseStatus ←── Phase (各阶段执行结果)               │
  │                                                     │
  │  DeclarativeUpgrade ←── Phase (DAG 组件执行结果)      │
  │                                                     │
  └─────────────────────────────────────────────────────┘

节点层内部依赖：
  ┌─────────────────────────────────────────────────────┐
  │                                                     │
  │  BKENode.State ←── StateCode (位标记聚合)            │
  │       │                                             │
  │       └──← BKEMachine.Ready (引导完成标记)            │
  │                                                     │
  │  StateCode ←── 各 Phase 位标记                       │
  │       ├── NodeAgentPushedFlag (bit 0)               │
  │       ├── NodeAgentReadyFlag  (bit 1)               │
  │       ├── NodeEnvFlag         (bit 2)               │
  │       ├── NodeBootFlag        (bit 3)               │
  │       ├── MasterInitFlag      (bit 5)               │
  │       ├── NodePostProcessFlag (bit 9)               │
  │       ├── NodeFailedFlag      (bit 7)  ← 异常       │
  │       └── NodeDeletingFlag    (bit 6)  ← 删除       │
  │                                                     │
  └─────────────────────────────────────────────────────┘

命令层内部依赖：
  ┌─────────────────────────────────────────────────────┐
  │                                                     │
  │  Command.Phase ←── 聚合(所有 Condition.Phase)        │
  │       │                                             │
  │       └──← 各子命令执行结果                           │
  │                                                     │
  │  Command.Status ←── 聚合(所有 Condition.Status)      │
  │                                                     │
  └─────────────────────────────────────────────────────┘

跨层依赖：
  ┌─────────────────────────────────────────────────────┐
  │                                                     │
  │  ClusterStatus ←── 聚合(所有 BKENode.State)          │
  │                                                     │
  │  ClusterHealthState ←── 聚合(所有 BKENode.State      │
  │                         + 集群级组件状态)              │
  │                                                     │
  │  BKENode.State ←── 聚合(所有关联 Command.Phase)      │
  │                                                     │
  └─────────────────────────────────────────────────────┘
```

### 10.2 状态聚合规则

#### 10.2.1 节点状态聚合 → 集群状态

```
规则 1：所有节点 Ready → 集群 Ready
  if all(node.State == NodeReady for node in nodes):
      ClusterStatus = ClusterReady

规则 2：任意节点 Upgrading → 集群 Upgrading
  if any(node.State == NodeUpgrading for node in nodes):
      ClusterStatus = ClusterUpgrading

规则 3：任意节点 Failed → 集群 Failed
  if any(node.State == NodeFailed for node in nodes):
      ClusterStatus = ClusterScaleFailed  // 或 ClusterInitializationFailed 等

规则 4：任意节点 Deleting → 集群 Scaling
  if any(node.State == NodeDeleting for node in nodes):
      ClusterStatus = ClusterWorkerScalingDown  // 或 ClusterMasterScalingDown

规则 5：任意节点 Pending/Provisioned → 集群 Creating
  if any(node.State in [NodePending, NodeProvisioned] for node in nodes):
      ClusterStatus = ClusterInitializing
```

#### 10.2.2 StateCode 位标记聚合 → 节点状态

```
规则 1：StateCode == 527 (bootstrapReadyStateCode) → NodeReady
  // 527 = bits 0,1,2,3,5,9 = AgentPushed + AgentReady + Env + Boot + MasterInit + PostProcess
  if node.StateCode & 527 == 527:
      node.State = NodeReady

规则 2：NodeFailedFlag (bit 7) 置位 → NodeFailed
  if node.StateCode & NodeFailedFlag != 0:
      node.State = NodeFailed

规则 3：NodeDeletingFlag (bit 6) 置位 → NodeDeleting
  if node.StateCode & NodeDeletingFlag != 0:
      node.State = NodeDeleting

规则 4：StateCode == 0 → NodePending
  if node.StateCode == 0:
      node.State = NodePending

规则 5：部分位标记置位 → NodeProvisioned / NodeNotReady
  if node.StateCode > 0 && node.StateCode != 527:
      node.State = NodeProvisioned  // 或 NodeNotReady，取决于具体位标记
```

#### 10.2.3 命令状态聚合 → Command.Phase

```
规则 1：任意子命令 Failed → Command.Phase = CommandFailed
  if any(condition.Phase == CommandFailed for condition in conditions):
      command.Phase = CommandFailed

规则 2：所有子命令 Complete → Command.Phase = CommandComplete
  if all(condition.Phase == CommandComplete for condition in conditions):
      command.Phase = CommandComplete

规则 3：部分子命令完成中 → Command.Phase = CommandRunning
  if any(condition.Phase in [CommandRunning, CommandPending] for condition in conditions):
      command.Phase = CommandRunning

规则 4：BackoffIgnore + Failed → Command.Phase = CommandSkip
  if condition.Phase == CommandFailed && execCommand.BackoffIgnore:
      condition.Phase = CommandSkip
```

#### 10.2.4 ClusterHealthState 聚合规则

```
规则 1：DeployFlag (首次部署) → ClusterHealthState = Deploying
规则 2：UpgradeFlag (升级中) → ClusterHealthState = Upgrading
规则 3：ManageFlag (管理中) → ClusterHealthState = Managing
规则 4：DeleteFlag (删除中) → ClusterHealthState = Deleting
规则 5：健康检查通过 → ClusterHealthState = Healthy
规则 6：健康检查失败 → ClusterHealthState = Unhealthy

失败升级规则（StatusManager）：
规则 7：Deploying + 失败次数 > 10 → ClusterHealthState = DeployFailed
规则 8：Upgrading + 失败次数 > 10 → ClusterHealthState = UpgradeFailed
规则 9：Managing + 失败次数 > 10 → ClusterHealthState = ManageFailed
```

### 10.3 状态因果关系

#### 10.3.1 因果传播路径

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         状态因果传播路径                                      │
└─────────────────────────────────────────────────────────────────────────────┘

路径 1：命令执行 → 节点状态 → 集群状态

  Command.Phase: Pending → Running → Completed
       │
       ▼
  BKENode.StateCode |= NodeBootFlag (bit 3)
       │
       ▼
  BKENode.State: Provisioned → Ready (当 StateCode == 527)
       │
       ▼
  ClusterStatus: Initializing → Ready (当所有节点 Ready)

路径 2：Phase 执行 → 集群状态

  Phase.Execute() 成功/失败
       │
       ▼
  ClusterStatus: 活跃状态 / *Failed (由 post-hook 设置)
       │
       ▼
  StatusManager.recordBKEClusterStatus()
       │
       ├─ 失败次数 ≤ 10 → 保持当前状态，requeue
       │
       └─ 失败次数 > 10 → ClusterHealthState 升级为 *Failed

路径 3：节点变化 → 集群状态

  BKENode 创建/删除
       │
       ▼
  BKEClusterReconciler.handleNodeChanges()
       │
       ├─ 新节点 → StateCode = 0, State = Pending
       │              → ClusterStatus = ClusterInitializing
       │
       └─ 节点删除 → StateCode |= NodeDeletingFlag
                       → ClusterStatus = ClusterWorkerScalingDown

路径 4：版本变更 → 声明式升级 → 组件状态

  ClusterVersion.Spec.DesiredVersion 变更
       │
       ▼
  BKECluster 注解: cvo.openfuyao.cn/upgrade-ready = "v2.6.0"
       │
       ▼
  DeclarativeUpgrade.TargetVersion = "v2.6.0"
       │
       ▼
  Scheduler.ExecuteDAG()
       │
       ├─ 组件成功 → DeclarativeUpgrade.Completed++
       │              → ClusterStatus = ClusterUpgrading
       │
       └─ 组件失败 → DeclarativeUpgrade.LastFailure++
                       → ClusterStatus = ClusterUpgradeFailed
```

#### 10.3.2 因果传播延迟

| 传播路径 | 延迟 | 原因 |
|---------|------|------|
| Command → BKENode | 秒级 | Command 完成后立即更新 StateCode |
| BKENode → BKECluster | 秒级 | Reconcile 循环中聚合 |
| Phase → ClusterStatus | 秒级 | Phase post-hook 立即设置 |
| ClusterHealthState 升级 | 分钟级 | 需要累积 10 次失败（默认） |
| DeclarativeUpgrade 进度 | 秒级 | 每个组件执行后立即更新 |

### 10.4 状态冲突处理

#### 10.4.1 多状态同时变化

**场景**：Phase 执行同时修改 ClusterStatus 和 BKENode.StateCode

**处理策略**：

```
UpdateCombinedBKECluster() 保证原子性：
  │
  ├─ 1. 先持久化 BKENode（UpdateModifiedBKENodes）
  │
  ├─ 2. 再持久化 BKECluster（PatchHelper.Patch）
  │
  └─ 3. 如果 BKECluster Patch 失败（冲突）
        └─ 重新读取 → 重新应用 → 重试
```

#### 10.4.2 状态回滚级联

**场景**：升级失败后回滚

```
回滚触发：
  DeclarativeUpgrade.LastFailure.Attempt > 阈值
       │
       ▼
  回滚传播：
  │
  ├─ 1. DeclarativeUpgrade.LastError = "升级失败"
  │
  ├─ 2. ClusterStatus = ClusterUpgradeFailed
  │
  ├─ 3. ClusterHealthState = UpgradeFailed (失败升级后)
  │
  ├─ 4. BKENode.StateCode |= NodeFailedFlag (失败节点)
  │
  └─ 5. BKENode.State = NodeFailed (失败节点)

回滚恢复（人工介入）：
  │
  ├─ 1. 用户添加注解: bke.bocloud.com/retry=""
  │
  ├─ 2. BKEClusterReconciler.handleRetryLogic()
  │     └─ BKENode.StateCode &= ^NodeFailedFlag (清除失败标记)
  │
  ├─ 3. StatusManager.ResetCache() (重置失败计数)
  │
  └─ 4. 重新执行失败阶段
        └─ Phase 幂等执行，跳过已完成组件
```

#### 10.4.3 状态不一致修复

**场景**：BKENode.State 与 StateCode 不一致（如 State=Ready 但 StateCode 缺少位标记）

**修复机制**：

```
Reconcile 循环中自动修复：
  │
  ├─ 1. StatusManager.SetStatus() 重新评估节点状态
  │     └─ 根据 StateCode 重新计算 State
  │
  ├─ 2. 如果 State 与 StateCode 不一致
  │     └─ 以 StateCode 为准（StateCode 是事实来源）
  │
  └─ 3. 更新 BKENode.State 并持久化

人工修复：
  │
  ├─ 1. 查看 StateCode: kubectl get bkenode <name> -o jsonpath='{.status.stateCode}'
  │
  ├─ 2. 手动修正: kubectl patch bkenode <name> --type merge --subresource status -p '{"status":{"stateCode":527}}'
  │
  └─ 3. 触发 Reconcile: kubectl annotate bkecluster <name> bke.bocloud.com/retry=""
```

#### 10.4.4 状态冲突优先级

当多个状态源产生冲突时，按以下优先级处理：

| 优先级 | 状态源 | 说明 |
|--------|--------|------|
| **最高** | StateCode 位标记 | 事实来源，不可覆盖 |
| **高** | Phase 执行结果 | Phase post-hook 设置的状态 |
| **中** | StatusManager 聚合 | 基于失败计数的状态升级 |
| **低** | 用户手动设置 | 可能被 Reconcile 覆盖 |

---

## 附录

### A. 关键常量定义

```go
// bootstrapReadyStateCode
bootstrapReadyStateCode = 527  // = 1+2+4+8+32+512

// 默认重试配置
DefaultBackoffLimit            = 3
DefaultActiveDeadlineSecond    = 1000
DefaultTTLSecondsAfterFinished = 600

// Rate Limiter
defaultFastDelay       = 10 * time.Second
defaultSlowDelay       = 60 * time.Second
defaultMaxFastAttempts = 5

// 注解键
RetryAnnotationKey              = "bke.bocloud.com/retry"
AppointmentAddNodesAnnotationKey = "bke.bocloud.com/appointment-add-nodes"
UpgradeReadyAnnotationKey       = "cvo.openfuyao.cn/upgrade-ready"
DeclarativeUpgradeAnnotationKey = "cvo.openfuyao.cn/declarative-upgrade"
```

### B. 关键文件位置

| 文件 | 作用 |
|------|------|
| `api/capbke/v1beta1/bkecluster_consts.go` | 集群状态常量定义 |
| `api/bkecommon/v1beta1/bkenode_types.go` | 节点类型定义 |
| `api/bkeagent/v1beta1/command_types.go` | 命令类型定义 |
| `controllers/capbke/bkecluster_controller.go` | BKECluster 控制器 |
| `controllers/capbke/bkemachine_controller_phases.go` | BKEMachine 引导逻辑 |
| `controllers/bkeagent/command_controller.go` | Command 控制器 |
| `pkg/dagexec/scheduler.go` | DAG 调度器 |
| `pkg/phaseframe/phases/list.go` | Phase 注册 |
| `pkg/phaseframe/phaseutil/util.go` | 节点过滤工具 |

### C. 术语表

| 术语 | 定义 |
|------|------|
| **StateCode** | 节点位标记，10 位整数，记录节点生命周期各阶段完成状态 |
| **Phase** | 集群当前执行阶段名称（如 EnsureBKEAgent, EnsureNodesEnv） |
| **ClusterStatus** | 集群操作状态（如 Initializing, Ready, Upgrading） |
| **ClusterHealthState** | 集群健康状态（如 Deploying, Healthy, Unhealthy） |
| **DeclarativeUpgrade** | 声明式升级状态，记录升级进度和已完成组件 |
| **BackoffLimit** | 命令重试次数限制 |
| **ActiveDeadlineSecond** | 命令执行超时时间 |
| **bootstrapReadyStateCode** | 节点引导完成的 StateCode 值（527） |

---

**文档版本**: v1.0  
**最后更新**: 2026-07-09  
**维护者**: BKE Team
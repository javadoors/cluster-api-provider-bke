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
# 整理 `bkemachine_controller_phases.go` 文件的功能与规格清单：

## 文件概述

**文件路径**：[bkemachine_controller_phases.go](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go)

**核心职责**：BKEMachine Controller 的 Phase 处理逻辑，负责 BKEMachine 的生命周期管理，包括 Bootstrap、Command 处理、节点状态管理等。

**代码规模**：
- **总行数**：约 1541 行
- **函数数量**：47 个
- **参数结构体**：15 个

## 功能模块清单

### 1. **Bootstrap 引导模块**

#### 1.1 `reconcileBootstrap()` - Bootstrap 协调入口
**功能**：处理 BKEMachine 的 Bootstrap 流程
**触发条件**：`BKEMachine.Status.Bootstrapped == false`
**处理流程**：
```
1. 检查是否已 Bootstrap
2. 创建 PatchHelper
3. 检查 BKEMachine Label（判断是否在 Bootstrap 过程中）
4. 调用 handleFirstTimeReconciliation()
```
**代码位置**：[Line 180-207](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L180-L207)

#### 1.2 `handleFirstTimeReconciliation()` - 首次协调处理
**功能**：处理首次协调的 BKEMachine
**处理流程**：
```
1. 等待 Control Plane 初始化（Worker 节点）
2. 同步 Kubeadm 配置（Master 节点）
3. 获取角色节点
4. 确定 Bootstrap Phase（InitControlPlane/JoinControlPlane/JoinWorker）
5. 过滤可用节点
6. 标记节点状态
7. 记录 Bootstrap 事件
8. 分发到 FakeBootstrap 或 RealBootstrap
```
**代码位置**：[Line 209-286](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L209-L286)

#### 1.3 `handleFakeBootstrap()` - 伪引导处理
**功能**：处理非完全控制集群的 Bootstrap（如 Bocloud 部分控制集群）
**适用场景**：`!clusterutil.FullyControlled(BKECluster)`
**处理流程**：
```
1. 生成 ProviderID
2. 修补远程节点 ProviderID
3. 处理 Master 证书（Master 节点）
4. 标记 BKEMachine Bootstrap Ready
5. 协调 BKEMachine 状态
6. 设置 Annotation 和 Label
```
**代码位置**：[Line 336-390](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L336-L390)

#### 1.4 `handleRealBootstrap()` - 真实引导处理
**功能**：处理完全控制集群的 Bootstrap
**适用场景**：`clusterutil.FullyControlled(BKECluster)`
**处理流程**：
```
1. 创建 Bootstrap Command
   - MasterInit Command（InitControlPlane）
   - MasterJoin Command（JoinControlPlane）
   - WorkerJoin Command（JoinWorker）
2. 设置 BKEMachine Label
3. 保存节点信息到 Status.Node
4. 等待 BKEAgent 执行 Command
```
**代码位置**：[Line 428-477](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L428-L477)

### 2. **Command 处理模块**

#### 2.1 `reconcileCommand()` - Command 协调入口
**功能**：处理 BKEMachine 关联的所有 Command
**处理流程**：
```
1. 创建 PatchHelper
2. 获取 BKEMachine 关联的 Commands
3. 选择合适的节点
4. 遍历处理每个 Command
```
**代码位置**：[Line 480-541](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L480-L541)

#### 2.2 `processCommand()` - Command 处理分发
**功能**：根据 Command 类型分发到不同的处理函数
**Command 类型**：
- `BootstrapCommand`：Bootstrap 命令
- `ResetCommand`：节点重置命令

**代码位置**：[Line 553-619](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L553-L619)

#### 2.3 `processBootstrapCommand()` - Bootstrap Command 处理
**功能**：处理 Bootstrap Command 的执行结果
**处理流程**：
```
1. 清理节点 Bootstrap 记录
2. 检查是否已 Bootstrap
3. 检查集群是否正在删除
4. 根据 Command 状态分发：
   - Failed → processBootstrapFailure()
   - Success → processBootstrapSuccess()
```
**代码位置**：[Line 621-677](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L621-L677)

#### 2.4 `processBootstrapFailure()` - Bootstrap 失败处理
**功能**：处理 Bootstrap Command 执行失败
**处理流程**：
```
1. 记录失败指标
2. 设置节点状态为 NodeBootStrapFailed
3. 记录失败日志
4. 标记 Condition 为 False
5. 如果 Control Plane 未初始化：
   - 暂停集群部署
   - 输出用户提示
6. 如果 Control Plane 已初始化：
   - 移除 BKEMachine Label（允许重新 Bootstrap）
   - 输出用户提示
```
**代码位置**：[Line 679-743](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L679-L743)

#### 2.5 `processBootstrapSuccess()` - Bootstrap 成功处理
**功能**：处理 Bootstrap Command 执行成功
**处理流程**：
```
1. 连接目标集群节点（等待 4 分钟）
2. 检查节点是否可用
3. 标记 BKEMachine Bootstrap Ready
4. 记录成功指标
5. 标记 Command 已协调
6. 协调 BKEMachine 状态
```
**代码位置**：[Line 793-827](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L793-L827)

#### 2.6 `processResetCommand()` - Reset Command 处理
**功能**：处理节点重置命令
**处理流程**：
```
1. 检查 Reset Command 状态
2. 如果成功：
   - 删除 BKENode CRD
   - 同步 BKECluster Status
   - 移除 BKEMachine Finalizer
```
**代码位置**：[Line 829-859](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L829-L859)

### 3. **节点状态管理模块**

#### 3.1 `reconcileBKEMachine()` - BKEMachine 状态协调
**功能**：协调 BKEMachine 的整体状态
**处理流程**：
```
1. 检查 TargetClusterBootCondition 是否已为 True
2. 获取集群信息
3. 检查 Bootstrap 状态
4. 处理不同集群状态
```
**代码位置**：[Line 861-893](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L861-L893)

#### 3.2 `checkBootstrapStatus()` - Bootstrap 状态检查
**功能**：检查所有 BKEMachine 的 Bootstrap 状态
**返回值**：
- `clusterReady`：所有节点已 Bootstrap
- `bootstrapNodeFailed`：存在 Bootstrap 失败的节点

**代码位置**：[Line 912-939](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L912-L939)

#### 3.3 `handleClusterState()` - 集群状态处理
**功能**：根据集群状态分发到不同的处理函数
**状态分类**：
- `AllNodesBootstrapped`：所有节点已 Bootstrap
- `BootstrapFailure`：存在 Bootstrap 失败
- `ClusterReady`：集群就绪
- `ClusterBooting`：集群启动中

**代码位置**：[Line 941-968](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L941-L968)

#### 3.4 `handleClusterReady()` - 集群就绪处理
**功能**：处理集群就绪状态
**处理流程**：
```
1. 标记 TargetClusterBootCondition 为 True
2. 记录集群 Bootstrap 完成指标
3. 同步 BKECluster Status
```
**代码位置**：[Line 993-1011](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L993-L1011)

### 4. **节点选择与分配模块**

#### 4.1 `filterAvailableNode()` - 可用节点过滤
**功能**：从角色节点中选择可用的节点
**选择策略**：
```
1. InitControlPlane：直接选择第一个节点
2. 其他 Phase：
   - 遍历所有角色节点
   - 检查节点是否已被分配（通过 BKEMachine Label）
   - 检查节点是否在 Bootstrap 记录中
   - 返回第一个可用节点
```
**并发控制**：使用 `r.mux` 互斥锁防止并发分配

**代码位置**：[Line 1027-1086](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L1027-L1086)

#### 4.2 `getRoleNodes()` - 角色节点获取
**功能**：获取指定角色的节点
**角色类型**：
- `MasterNodeRole`：Master 节点
- `WorkerNodeRole`：Worker 节点

**代码位置**：[Line 318-334](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L318-L334)

### 5. **目标集群节点检查模块**

#### 5.1 `checkTargetClusterNode()` - 目标集群节点检查
**功能**：检查目标集群节点是否已创建并配置正确
**检查内容**：
```
1. 创建目标集群客户端
2. 获取节点列表
3. 根据 ProviderID 查找节点
4. 设置节点角色标签
5. 设置节点 Taint（Master 节点）
6. 创建模拟 ConfigMaps
```
**代码位置**：[Line 1088-1113](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L1088-L1113)

#### 5.2 `connectToTargetClusterNode()` - 连接目标集群节点
**功能**：尝试连接目标集群节点（带超时）
**超时时间**：4 分钟（`DefaultNodeConnectTimeout`）
**重试间隔**：5 秒（`DefaultRequeueAfterDuration`）

**代码位置**：[Line 745-770](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L745-L770)

### 6. **辅助功能模块**

#### 6.1 `markBKEMachineBootstrapReady()` - 标记 Bootstrap Ready
**功能**：标记 BKEMachine 已 Bootstrap 成功
**设置内容**：
```
1. 设置 MachineAddress（IP、Hostname）
2. 设置 ProviderID
3. 设置 Status.Ready = true
4. 设置 Status.Bootstrapped = true
5. 标记 BootstrapSucceededCondition 为 True
```
**代码位置**：[Line 1258-1287](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L1258-L1287)

#### 6.2 `syncKubeadmConfig()` - 同步 Kubeadm 配置
**功能**：同步 KubeadmConfig 到 KubeadmControlPlane 的配置
**适用节点**：Master 节点

**代码位置**：[Line 288-307](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L288-L307)

#### 6.3 `handleMasterMachineCertificates()` - 处理 Master 证书
**功能**：设置 Master 节点的证书过期时间
**证书有效期**：100 年（`CertificateExpiryYears`）

**代码位置**：[Line 392-426](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L392-L426)

#### 6.4 `getBootstrapPhase()` - 获取 Bootstrap Phase
**功能**：确定 BKEMachine 的 Bootstrap Phase
**Phase 类型**：
- `InitControlPlane`：第一个 Master 节点
- `JoinControlPlane`：后续 Master 节点
- `JoinWorker`：Worker 节点

**代码位置**：[Line 1390-1405](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L1390-L1405)

## 参数结构体清单

| 结构体名称 | 用途 | 包含字段 |
|-----------|------|---------|
| `CommonContextParams` | 通用上下文参数 | Ctx, Log |
| `CommonResourceParams` | 通用资源参数 | Machine, Cluster, BKEMachine, BKECluster |
| `CommonNodeParams` | 通用节点参数 | Node, Role |
| `CommonCommandParams` | 通用命令参数 | PatchHelper, Cmd, Complete, SuccessNodes, FailedNodes |
| `BootstrapReconcileParams` | Bootstrap 协调参数 | CommonResourceParams |
| `FakeBootstrapParams` | 伪引导参数 | CommonNodeParams |
| `RealBootstrapParams` | 真实引导参数 | CommonNodeParams, Phase |
| `ProcessCommandParams` | Command 处理参数 | Nodes, HostIp, Cmd |
| `ProcessBootstrapCommandParams` | Bootstrap Command 处理参数 | CurrentNode, Cmd, Complete |
| `ProcessBootstrapCommonParams` | Bootstrap 通用参数 | PatchHelper, CurrentNode, Cmd |
| `ProcessBootstrapFailureParams` | Bootstrap 失败参数 | FailedNodes, Role |
| `ProcessBootstrapSuccessParams` | Bootstrap 成功参数 | - |
| `ProcessResetCommandParams` | Reset Command 参数 | CurrentNode |
| `HandleClusterStateParams` | 集群状态处理参数 | NodeState, BKEMachines, Nodes, ClusterReady |
| `TargetClusterNodeParams` | 目标集群节点参数 | BKECluster, Cluster, Machine, CurrentNode |

## 常量定义

```go
const (
    DefaultRequeueAfterDuration = 5 * time.Second   // 默认重新排队时间
    DefaultNodeConnectTimeout   = 4 * time.Minute   // 节点连接超时时间
    CertificateExpiryYears      = 100               // 证书有效期（年）
)
```

## 核心流程图

### Bootstrap 流程

```
reconcileBootstrap()
    ↓
handleFirstTimeReconciliation()
    ├─ 等待 Control Plane 初始化
    ├─ 同步 Kubeadm 配置
    ├─ 获取角色节点
    ├─ 确定 Bootstrap Phase
    ├─ 过滤可用节点
    └─ 分发处理：
        ├─ handleFakeBootstrap()  (非完全控制集群)
        └─ handleRealBootstrap()  (完全控制集群)
            └─ 创建 Bootstrap Command
```

### Command 处理流程

```
reconcileCommand()
    ↓
processCommand()
    ├─ processBootstrapCommand()
    │   ├─ processBootstrapFailure()  (失败)
    │   └─ processBootstrapSuccess()  (成功)
    │       └─ checkTargetClusterNode()
    └─ processResetCommand()
```

### 状态协调流程

```
reconcileBKEMachine()
    ↓
checkBootstrapStatus()
    ↓
handleClusterState()
    ├─ handleAllNodesBootstrapped()
    ├─ handleBootstrapFailure()
    ├─ handleClusterReady()
    └─ handleClusterBooting()
```

## 关键设计模式

### 1. **参数对象模式**
- 使用参数结构体封装函数参数
- 提高代码可读性和可维护性
- 便于扩展新参数

### 2. **策略模式**
- 根据 Command 类型分发到不同的处理函数
- 根据集群状态分发到不同的状态处理函数

### 3. **模板方法模式**
- `handleFirstTimeReconciliation()` 定义了 Bootstrap 的骨架流程
- 子步骤由具体函数实现

### 4. **并发控制**
- 使用 `sync.Mutex` 保护 `nodesBootRecord`
- 防止节点被并发分配

## 总结

`bkemachine_controller_phases.go` 是 BKEMachine Controller 的核心处理逻辑文件，主要功能包括：
1. **Bootstrap 引导**：处理 BKEMachine 的初始化引导流程
2. **Command 处理**：处理 Bootstrap 和 Reset Command 的执行结果
3. **节点状态管理**：管理和协调节点状态
4. **节点选择与分配**：选择可用节点并分配给 BKEMachine
5. **目标集群检查**：检查目标集群节点是否就绪

该文件采用了良好的设计模式和代码组织方式，通过参数结构体和函数分解实现了高内聚低耦合的代码结构。

# BKEMachineReconciler 与其他模块的完整交互流程：

## BKEMachineReconciler 架构概览

### 核心结构

**代码位置**：[bkemachine_controller.go:58-66](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L58-L66)

```go
type BKEMachineReconciler struct {
    client.Client                    // Kubernetes Client
    Scheme      *runtime.Scheme      // Runtime Scheme
    Recorder    record.EventRecorder // Event Recorder
    NodeFetcher *nodeutil.NodeFetcher // Node Fetcher

    nodesBootRecord map[string]struct{} // 节点 Bootstrap 记录
    mux             sync.Mutex          // 互斥锁
}
```

## Watch 机制（触发源）

**代码位置**：[bkemachine_controller.go:537-570](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L537-L570)

```go
func (r *BKEMachineReconciler) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&bkev1beta1.BKEMachine{}).  // 主资源
        Watches(&agentv1beta1.Command{}, ...).  // Command 变化
        Watches(&clusterv1.Machine{}, ...).     // CAPI Machine 变化
        Watches(&clusterv1.Cluster{}, ...).     // CAPI Cluster 变化
        Watches(&bkev1beta1.BKECluster{}, ...). // BKECluster 变化
        Complete(r)
}
```

### 触发源详解

| 触发源 | Watch 类型 | 触发条件 | 触发逻辑 |
|-------|-----------|---------|---------|
| **BKEMachine** | `For` | BKEMachine Create/Update/Delete | 直接触发 Reconcile |
| **Command** | `Watches` | Command Status 变为 Complete | 通过 OwnerReference 找到 BKEMachine |
| **CAPI Machine** | `Watches` | Machine Create/Update | 通过 InfrastructureRef 找到 BKEMachine |
| **CAPI Cluster** | `Watches` | Cluster UnPause | 找到所有关联的 BKEMachine |
| **BKECluster** | `Watches` | BKECluster AgentReady + UnPause | 找到所有关联的 BKEMachine |

## 模块交互流程图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        BKEMachineReconciler 交互流程                     │
└─────────────────────────────────────────────────────────────────────────┘

                                 触发源
                                   │
        ┌──────────────────────────┼──────────────────────────┐
        │                          │                          │
        ▼                          ▼                          ▼
┌───────────────┐         ┌───────────────┐         ┌───────────────┐
│  BKEMachine   │         │    Command    │         │ CAPI Machine  │
│  (主资源)      │         │  (状态更新)    │         │  (关联资源)   │
└───────┬───────┘         └───────┬───────┘         └───────┬───────┘
        │                         │                         │
        └─────────────────────────┼─────────────────────────┘
                                  │
                                  ▼
                    ┌─────────────────────────┐
                    │   Reconcile() 入口      │
                    └───────────┬─────────────┘
                                │
                                ▼
                    ┌─────────────────────────┐
                    │  fetchRequiredObjects() │
                    │  获取必要资源对象         │
                    └───────────┬─────────────┘
                                │
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
        ┌──────────────────┐    ┌──────────────────┐
        │  BKEMachine      │    │  CAPI Machine    │
        │  (目标资源)       │    │  (OwnerRef)      │
        └──────────────────┘    └──────────────────┘
                    │
                    ▼
        ┌──────────────────┐
        │  CAPI Cluster    │
        │  (关联集群)       │
        └──────────────────┘
                    │
                    ▼
        ┌─────────────────────────┐
        │  mergecluster.          │
        │  GetCombinedBKECluster()│
        │  获取合并的 BKECluster   │
        └───────────┬─────────────┘
                    │
                    ▼
        ┌─────────────────────────┐
        │  handleMainReconcile()  │
        └───────────┬─────────────┘
                    │
        ┌───────────┴───────────┐
        │                       │
        ▼                       ▼
┌──────────────┐      ┌──────────────────┐
│  reconcile() │      │ reconcileDelete()│
│  (正常流程)   │      │  (删除流程)       │
└──────┬───────┘      └──────────────────┘
       │
       ├─────────────────────────────────────┐
       │                                     │
       ▼                                     ▼
┌──────────────────┐              ┌──────────────────┐
│ reconcileCommand │              │ reconcileBootstrap│
│  (Command 处理)   │              │  (Bootstrap 处理) │
└────────┬─────────┘              └────────┬─────────┘
         │                                  │
         │                                  │
         ▼                                  ▼
┌─────────────────────────────────────────────────────┐
│                  交互的其他模块                      │
└─────────────────────────────────────────────────────┘
```

## 详细交互流程

### 1. **与 CAPI (Cluster API) 模块交互**

#### 1.1 CAPI Machine 交互

```
BKEMachineReconciler
    │
    ├─→ util.GetOwnerMachine()          // 获取 Owner Machine
    │   └─ 返回：clusterv1.Machine
    │
    ├─→ util.GetClusterFromMetadata()   // 获取关联 Cluster
    │   └─ 返回：clusterv1.Cluster
    │
    ├─→ util.IsControlPlaneMachine()    // 判断是否为 Master
    │   └─ 返回：bool
    │
    └─→ conditions.IsTrue(Cluster, ControlPlaneInitializedCondition)
        └─ 检查 Control Plane 是否已初始化
```
**交互场景**：
- **获取 Machine 信息**：通过 OwnerRef 获取 CAPI Machine
- **判断节点角色**：Master 或 Worker
- **等待 Control Plane**：Worker 节点等待 Master 初始化完成

#### 1.2 CAPI Cluster 交互
```
BKEMachineReconciler
    │
    ├─→ Cluster.Status.InfrastructureReady  // 检查基础设施是否就绪
    │
    ├─→ conditions.IsTrue(Cluster, ControlPlaneInitializedCondition)
    │   └─ 检查 Control Plane 是否已初始化
    │
    └─→ annotations.IsPaused(Cluster, BKEMachine)
        └─ 检查是否暂停
```
**交互场景**：
- **基础设施检查**：等待 BKECluster Controller 创建基础设施
- **Control Plane 检查**：判断是否可以开始 Bootstrap
- **暂停检查**：支持集群暂停功能

### 2. **与 BKECluster 模块交互**

```
BKEMachineReconciler
    │
    ├─→ mergecluster.GetCombinedBKECluster()
    │   └─ 获取合并的 BKECluster（BKECluster + BKENode）
    │
    ├─→ mergecluster.SyncStatusUntilComplete()
    │   └─ 同步 BKECluster Status 到 API Server
    │
    ├─→ clusterutil.FullyControlled(BKECluster)
    │   └─ 判断是否完全控制
    │
    └─→ BKECluster.Status.ClusterStatus
        └─ 检查集群状态
```
**交互场景**：
- **获取集群配置**：获取 BKECluster 的完整配置
- **状态同步**：更新 BKECluster 的 Status
- **控制模式判断**：决定使用 FakeBootstrap 还是 RealBootstrap

### 3. **与 Command 模块交互**

```
BKEMachineReconciler
    │
    ├─→ command.Bootstrap.New()          // 创建 Bootstrap Command
    │   └─ 创建：agentv1beta1.Command
    │
    ├─→ command.Reset.New()              // 创建 Reset Command
    │   └─ 创建：agentv1beta1.Command
    │
    ├─→ command.CheckCommandStatus()     // 检查 Command 状态
    │   └─ 返回：complete, successNodes, failedNodes
    │
    └─→ getBKEMachineAssociateCommands() // 获取关联的 Commands
        └─ 返回：[]agentv1beta1.Command
```
**交互场景**：
- **创建 Bootstrap Command**：触发节点 Bootstrap
- **创建 Reset Command**：触发节点重置
- **监控 Command 状态**：检查 Command 执行结果

### 4. **与 NodeFetcher 模块交互**

```
BKEMachineReconciler
    │
    ├─→ NodeFetcher.GetReadyBootstrapNodes()
    │   └─ 获取就绪的 Bootstrap 节点
    │
    ├─→ NodeFetcher.GetNodesForBKECluster()
    │   └─ 获取集群的所有节点
    │
    ├─→ NodeFetcher.SetNodeStateWithMessageForCluster()
    │   └─ 设置节点状态
    │
    ├─→ NodeFetcher.MarkNodeStateFlagForCluster()
    │   └─ 标记节点状态标志
    │
    ├─→ NodeFetcher.GetNodeStateFlagForCluster()
    │   └─ 获取节点状态标志
    │
    └─→ NodeFetcher.DeleteBKENodeForCluster()
        └─ 删除 BKENode CRD
```
**交互场景**：
- **节点选择**：从 BKENode CRD 中选择可用节点
- **状态管理**：设置和获取节点状态
- **节点删除**：删除 BKEMachine 时删除对应的 BKENode

### 5. **与 PhaseFrame 模块交互**

```
BKEMachineReconciler
    │
    ├─→ phaseutil.GetClusterAPIKubeadmControlPlane()
    │   └─ 获取 KubeadmControlPlane
    │
    ├─→ phaseutil.GetBKEClusterAssociateBKEMachines()
    │   └─ 获取集群的所有 BKEMachine
    │
    ├─→ phaseutil.GenerateProviderID()
    │   └─ 生成 ProviderID
    │
    └─→ phaseutil.CalculateBKEMachineBootNum()
        └─ 计算 Bootstrap 成功/失败节点数
```
**交互场景**：
- **获取集群资源**：获取 KubeadmControlPlane、BKEMachine 等
- **ProviderID 生成**：生成节点的 ProviderID
- **状态统计**：统计 Bootstrap 状态

### 6. **与 Kubernetes API Server 交互**

```
BKEMachineReconciler
    │
    ├─→ Client.Get()         // 获取资源
    ├─→ Client.List()        // 列出资源
    ├─→ Client.Create()      // 创建资源
    ├─→ Client.Update()      // 更新资源
    ├─→ Client.Delete()      // 删除资源
    ├─→ Client.Patch()       // 补丁资源
    │
    └─→ patch.Helper         // 使用 CAPI Patch Helper
        └─ 优化 Status 更新
```
**交互场景**：
- **CRUD 操作**：对所有 CRD 进行 CRUD 操作
- **Status 更新**：使用 Patch Helper 优化 Status 更新

### 7. **与 Event Recorder 交互**

```
BKEMachineReconciler
    │
    ├─→ Recorder.AnnotatedEventf()
    │   └─ 发送带 Annotation 的 Event
    │
    └─→ logInfoAndEvent() / logWarningAndEvent() / logErrorAndEvent()
        └─ 同时记录日志和发送 Event
```
**交互场景**：
- **事件记录**：记录重要操作和状态变化
- **日志输出**：输出调试和监控日志

## 完整交互流程示例

### 场景 1：BKEMachine Bootstrap 流程

```
1. 触发源：BKEMachine Create
   │
   ▼
2. Reconcile() 入口
   │
   ├─→ fetchRequiredObjects()
   │   ├─ 获取 BKEMachine
   │   ├─ 获取 CAPI Machine (OwnerRef)
   │   └─ 获取 CAPI Cluster
   │
   ├─→ mergecluster.GetCombinedBKECluster()
   │   └─ 获取 BKECluster + BKENode
   │
   ├─→ handleMainReconcile()
   │   │
   │   ├─→ reconcileBootstrap()
   │   │   │
   │   │   ├─→ NodeFetcher.GetReadyBootstrapNodes()
   │   │   │   └─ 获取可用节点
   │   │   │
   │   │   ├─→ filterAvailableNode()
   │   │   │   └─ 选择一个可用节点
   │   │   │
   │   │   ├─→ clusterutil.FullyControlled()
   │   │   │   └─ 判断控制模式
   │   │   │
   │   │   └─→ handleRealBootstrap()
   │   │       │
   │   │       ├─→ command.Bootstrap.New()
   │   │       │   └─ 创建 Bootstrap Command
   │   │       │
   │   │       ├─→ labelhelper.SetBKEMachineLabel()
   │   │       │   └─ 设置 BKEMachine Label
   │   │       │
   │   │       └─→ mergecluster.SyncStatusUntilComplete()
   │   │           └─ 同步 BKECluster Status
   │   │
   │   └─→ 返回 ctrl.Result{}
   │
   └─→ 返回结果
```

### 场景 2：Command 完成处理流程

```
1. 触发源：Command Status 变为 Complete
   │
   ▼
2. Reconcile() 入口
   │
   ├─→ fetchRequiredObjects()
   │   └─ 获取 BKEMachine (通过 OwnerRef)
   │
   ├─→ handleMainReconcile()
   │   │
   │   └─→ reconcileCommand()
   │       │
   │       ├─→ getBKEMachineAssociateCommands()
   │       │   └─ 获取关联的 Commands
   │       │
   │       ├─→ NodeFetcher.GetNodesForBKECluster()
   │       │   └─ 获取节点列表
   │       │
   │       └─→ processBootstrapCommand()
   │           │
   │           ├─→ command.CheckCommandStatus()
   │           │   └─ 检查 Command 状态
   │           │
   │           ├─→ processBootstrapSuccess()  (成功)
   │           │   │
   │           │   ├─→ connectToTargetClusterNode()
   │           │   │   └─ 连接目标集群节点
   │           │   │
   │           │   ├─→ checkTargetClusterNode()
   │           │   │   └─ 检查节点状态
   │           │   │
   │           │   ├─→ markBKEMachineBootstrapReady()
   │           │   │   └─ 标记 Bootstrap Ready
   │           │   │
   │           │   └─→ reconcileBKEMachine()
   │           │       └─ 协调 BKEMachine 状态
   │           │
   │           └─→ processBootstrapFailure()  (失败)
   │               │
   │               ├─→ NodeFetcher.SetNodeStateWithMessageForCluster()
   │               │   └─ 设置节点失败状态
   │               │
   │               └─→ conditions.MarkFalse()
   │                   └─ 标记 Condition 为 False
   │
   └─→ 返回结果
```

### 场景 3：BKEMachine 删除流程

```
1. 触发源：BKEMachine Delete
   │
   ▼
2. Reconcile() 入口
   │
   ├─→ handleMainReconcile()
   │   │
   │   └─→ reconcileDelete()
   │       │
   │       ├─→ NodeFetcher.GetNodesForBKECluster()
   │       │   └─ 获取节点列表
   │       │
   │       ├─→ NodeFetcher.SetNodeStateWithMessageForCluster()
   │       │   └─ 设置节点为 Deleting
   │       │
   │       ├─→ shouldSkipDeletion()
   │       │   └─ 判断是否跳过删除
   │       │
   │       └─→ executeResetCommand()
   │           │
   │           ├─→ command.Reset.New()
   │           │   └─ 创建 Reset Command
   │           │
   │           └─→ 等待 Reset Command 完成
   │
   └─→ 返回结果
```

## 数据流图

```
┌─────────────────────────────────────────────────────────────────┐
│                        数据流向                                  │
└─────────────────────────────────────────────────────────────────┘

CAPI Cluster ────────┐
                     │
                     ▼
CAPI Machine ────────┼───→ BKEMachineReconciler ────→ BKEMachine
                     │           │
                     │           │
BKECluster ──────────┤           │
                     │           │
                     │           ▼
BKENode ─────────────┤      Command (Create/Update)
                     │           │
                     │           │
                     │           ▼
                     └─────→ BKEAgent (执行)
                                 │
                                 │
                                 ▼
                           Command Status
                                 │
                                 │
                                 ▼
                         BKEMachineReconciler
                                 │
                                 │
                                 ▼
                      BKEMachine.Status.Bootstrapped = true
```

## 总结

### BKEMachineReconciler 的核心职责

1. **协调 BKEMachine 生命周期**：创建、更新、删除
2. **处理 Bootstrap 流程**：创建 Bootstrap Command，等待完成
3. **处理 Command 状态**：监控 Command 执行结果
4. **管理节点状态**：设置和更新节点状态
5. **与 CAPI 集成**：与 CAPI Machine/Cluster 交互

### 主要交互模块

| 模块 | 交互方式 | 主要功能 |
|------|---------|---------|
| **CAPI** | 读取 | 获取 Machine/Cluster 信息 |
| **BKECluster** | 读写 | 获取集群配置，更新状态 |
| **Command** | 创建/监控 | 创建和监控 Command 执行 |
| **NodeFetcher** | 读写 | 管理节点状态和 BKENode CRD |
| **PhaseFrame** | 读取 | 获取集群资源和工具函数 |
| **Kubernetes API** | 读写 | 所有 CRD 的 CRUD 操作 |
| **Event Recorder** | 写入 | 记录事件和日志 |

### 设计特点

1. **声明式设计**：通过创建 Command CRD 声明期望状态
2. **异步处理**：Command 由 BKEAgent 异步执行
3. **状态驱动**：根据资源状态决定下一步操作
4. **事件驱动**：通过 Watch 机制响应资源变化
5. **幂等性**：所有操作都是幂等的，可安全重试

这种设计实现了管理集群（BKEMachineReconciler）与工作节点（BKEAgent）的解耦，通过 Command CRD 作为中间层进行通信，是典型的 Kubernetes 声明式 API 设计模式。

# 整理对 `agentv1beta1.Command` 资源调谐的所有处理代码

## Command 资源调谐处理代码清单

### 一、BKEAgent 端（Command 执行）

#### 1. **CommandReconciler** - Command 执行控制器

**文件位置**：[command_controller.go](file:///d:/code/github/\cluster-api-provider-bke\controllers\bkeagent\command_controller.go)

##### 核心结构

```go
type CommandReconciler struct {
    client.Client
    APIReader client.Reader
    Scheme    *runtime.Scheme
    Ctx       context.Context
    Job       job.Job      // 任务管理器
    NodeName  string       // 节点名称
    NodeIP    string       // 节点 IP
}
```

##### 主要函数

| 函数名 | 行号 | 功能 |
|-------|------|------|
| `Reconcile()` | 98-143 | Command 协调主入口 |
| `fetchCommand()` | 145-158 | 获取 Command 对象 |
| `ensureStatusInitialized()` | 161-183 | 初始化 Command Status |
| `handleFinalizer()` | 229-237 | 处理 Finalizer |
| `ensureFinalizer()` | 203-215 | 确保 Finalizer 存在 |
| `handleDeletion()` | 218-236 | 处理 Command 删除 |
| `handleSuspend()` | 240-270 | 处理暂停逻辑 |
| `shouldSkipOldTask()` | 275-287 | 检查是否跳过旧任务 |
| `createAndStartTask()` | 292-317 | 创建并启动任务 |
| `startTask()` | - | 启动任务执行（goroutine） |
| `cleanupTask()` | 200-206 | 清理任务 |
| `ttlSecondAfterFinished()` | - | TTL 清理机制 |

##### Reconcile 主流程

```go
func (r *CommandReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 获取 Command 对象
    command, res := r.fetchCommand(ctx, req)
    
    // 2. Commands 为空检查
    if len(command.Spec.Commands) == 0 {
        return ctrl.Result{}, nil
    }
    
    // 3. 初始化 Status
    if res := r.ensureStatusInitialized(command); res.done {
        return res.result, res.err
    }
    
    // 4. 处理 Finalizer
    if res := r.handleFinalizer(ctx, command, gid); res.done {
        return res.result, res.err
    }
    
    // 5. 跳过已完成的命令
    if currentStatus.Phase == agentv1beta1.CommandComplete {
        return ctrl.Result{}, nil
    }
    
    // 6. 处理暂停逻辑
    if res := r.handleSuspend(command, currentStatus, gid); res.done {
        return res.result, res.err
    }
    
    // 7. 跳过旧版本任务
    if r.shouldSkipOldTask(command, gid) {
        return ctrl.Result{}, nil
    }
    
    // 8. 创建并启动任务
    return r.createAndStartTask(ctx, command, currentStatus, gid).unwrap()
}
```

### 二、管理集群端（Command 创建与监控）

#### 2. **BKEMachineReconciler** - Command 创建与监控

**文件位置**：[bkemachine_controller_phases.go](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go)

##### Command 相关函数

| 函数名 | 行号 | 功能 |
|-------|------|------|
| `reconcileCommand()` | 480-541 | Command 协调入口 |
| `processCommand()` | 553-619 | Command 处理分发 |
| `processBootstrapCommand()` | 621-677 | Bootstrap Command 处理 |
| `processBootstrapFailure()` | 679-743 | Bootstrap 失败处理 |
| `processBootstrapSuccess()` | 793-827 | Bootstrap 成功处理 |
| `processResetCommand()` | 829-859 | Reset Command 处理 |
| `handleRealBootstrap()` | 428-477 | 创建 Bootstrap Command |
| `executeResetCommand()` | - | 创建 Reset Command |

##### reconcileCommand 流程

```go
func (r *BKEMachineReconciler) reconcileCommand(params BootstrapReconcileParams) (ctrl.Result, error) {
    // 1. 创建 PatchHelper
    patchHelper, err := patch.NewHelper(params.BKEMachine, r.Client)
    
    // 2. 获取 BKEMachine 关联的 Commands
    commands, err := getBKEMachineAssociateCommands(params.Ctx, r.Client, params.BKECluster, params.BKEMachine)
    
    // 3. 选择合适的节点
    nodes, err := r.selectAppropriateNodes(params.Ctx, params.BKECluster)
    
    // 4. 遍历处理每个 Command
    for _, cmd := range commands {
        res, errs = r.processCommand(commandParams)
    }
    
    return res, kerrors.NewAggregate(errs)
}
```

##### processCommand 分发逻辑

```go
func (r *BKEMachineReconciler) processCommand(params ProcessCommandParams) (ctrl.Result, []error) {
    // 检查 Command 状态
    complete, successNodes, failedNodes := command.CheckCommandStatus(&params.Cmd)
    
    // 根据 Command 类型分发
    if strings.HasPrefix(params.Cmd.Name, command.BootstrapCommandNamePrefix) {
        return r.processBootstrapCommand(bootstrapCommandParams)
    }
    
    if strings.HasPrefix(params.Cmd.Name, command.ResetNodeCommandNamePrefix) {
        return r.processResetCommand(resetCommandParams)
    }
    
    return params.Res, params.Errs
}
```

### 三、Command 创建工具

#### 3. **BaseCommand** - Command 创建基类

**文件位置**：[command.go](file:///d:/code/github/\cluster-api-provider-bke\pkg\command\command.go)

##### 核心结构

```go
type BaseCommand struct {
    Ctx             context.Context
    Client          client.Client
    NameSpace       string
    Scheme          *runtime.Scheme
    OwnerObj        metav1.Object
    ClusterName     string
    RemoveAfterWait bool
    Unique          bool
    ForceRemove     bool
    WaitTimeout     time.Duration
    WaitInterval    time.Duration
    commandName     string
    Command         *agentv1beta1.Command
}
```

##### 主要函数

| 函数名 | 功能 |
|-------|------|
| `validate()` | 验证 BaseCommand 字段 |
| `ValidateBkeCommand()` | 验证 BKE Command |
| `GenerateBkeConfigStr()` | 生成 BKE 配置字符串 |

##### Command 类型常量

```go
const (
    BootstrapCommandNamePrefix = "bootstrap-"        // Bootstrap Command 前缀
    HACommandName              = "k8s-ha-deploy"     // HA 部署命令
    K8sEnvCommandName          = "k8s-env-init"      // K8s 环境初始化
    K8sContainerdResetCommandName = "k8s-containerd-reset"  // Containerd 重置
    K8sContainerdRedeployCommandName = "k8s-containerd-redeploy"  // Containerd 重部署
    K8sEnvDryRunCommandName    = "k8s-env-dry-run"   // K8s 环境干运行
)
```

### 四、Command 工具函数

#### 4. **Command 状态检查与日志**

**文件位置**：[command.go](file:///d:/code/github/\cluster-api-provider-bke\pkg\phaseframe\phaseutil\command.go)

| 函数名 | 功能 |
|-------|------|
| `LogCommandFailed()` | 记录 Command 失败日志 |
| `LogCommandInfo()` | 记录 Command 信息日志 |
| `processNodeConditions()` | 处理节点 Conditions |

### 五、Command Predicate（Watch 过滤器）

#### 5. **CommandUpdateCompleted** - Command 完成过滤器

**文件位置**：[command.go](file:///d:/code/github/\cluster-api-provider-bke\utils\capbke\predicates\command.go)

```go
func CommandUpdateCompleted() predicate.Predicate {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            oldCommand, ok := e.ObjectOld.(*agentv1beta1.Command)
            if !ok {
                return false
            }
            
            newCommand, newOk := e.ObjectNew.(*agentv1beta1.Command)
            if !newOk || newCommand == nil {
                return false
            }
            
            // 触发条件：
            // 1. 新 Command 有更多节点状态
            // 2. 新 Command 状态为 Complete
            switch {
            case len(newCommand.Status) > len(oldCommand.Status):
                return true
            case len(newCommand.Status) == len(oldCommand.Status):
                for node, newStatus := range newCommand.Status {
                    if oldStatus, ok := oldCommand.Status[node]; !ok || 
                       oldStatus.Phase != newStatus.Phase && 
                       newStatus.Phase == agentv1beta1.CommandComplete {
                        return true
                    }
                }
            }
            return false
        },
    }
}
```
**触发条件**：
1. 新 Command 有更多节点状态（新节点加入）
2. 新 Command 状态变为 Complete（执行完成）

### 六、Command 相关参数结构体

**文件位置**：[bkemachine_controller_phases.go](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go)

```go
// CommonCommandParams - 通用 Command 参数
type CommonCommandParams struct {
    CommonResourceParams
    PatchHelper  *patch.Helper
    Cmd          *agentv1beta1.Command
    Complete     bool
    SuccessNodes []string
    FailedNodes  []string
    Res          ctrl.Result
    Errs         []error
}

// ProcessCommandParams - Command 处理参数
type ProcessCommandParams struct {
    CommonResourceParams
    PatchHelper *patch.Helper
    Nodes       bkenode.Nodes
    HostIp      string
    Cmd         agentv1beta1.Command
    Res         ctrl.Result
    Errs        []error
}

// ProcessBootstrapCommandParams - Bootstrap Command 处理参数
type ProcessBootstrapCommandParams struct {
    CommonResourceParams
    PatchHelper  *patch.Helper
    CurrentNode  confv1beta1.Node
    Cmd          *agentv1beta1.Command
    Complete     bool
    SuccessNodes []string
    FailedNodes  []string
    Res          ctrl.Result
    Errs         []error
}

// ProcessResetCommandParams - Reset Command 处理参数
type ProcessResetCommandParams struct {
    CommonCommandParams
    CurrentNode confv1beta1.Node
}
```

## Command 调谐完整流程图

```
┌─────────────────────────────────────────────────────────────────┐
│                  Command 资源调谐完整流程                        │
└─────────────────────────────────────────────────────────────────┘

管理集群                                节点侧
─────────                              ───────

1. Command 创建
   │
   ├─→ handleRealBootstrap()
   │   └─→ command.Bootstrap.New()
   │       └─ 创建 Command CR
   │           ├─ Spec.Commands (执行步骤)
   │           ├─ Spec.NodeName (目标节点)
   │           └─ OwnerRef → BKEMachine
   │
   └─→ 等待 BKEAgent 执行

                                        2. Command 执行
                                           │
                                           ├─→ CommandReconciler.Reconcile()
                                           │   │
                                           │   ├─→ fetchCommand()
                                           │   ├─→ ensureStatusInitialized()
                                           │   ├─→ handleFinalizer()
                                           │   ├─→ handleSuspend()
                                           │   └─→ createAndStartTask()
                                           │       │
                                           │       └─→ go startTask()
                                           │           │
                                           │           ├─ 执行 Spec.Commands
                                           │           │  ├─ K8sEnvInit
                                           │           │  └─ Kubeadm
                                           │           │
                                           │           └─ 更新 Status
                                           │               └─ Phase = Complete

3. Command 状态监控
   │
   ├─→ Watch Command (Predicate: CommandUpdateCompleted)
   │   └─ 触发条件：Status 变为 Complete
   │
   └─→ reconcileCommand()
       │
       ├─→ getBKEMachineAssociateCommands()
       │   └─ 获取关联的 Commands
       │
       └─→ processCommand()
           │
           ├─→ command.CheckCommandStatus()
           │   └─ 检查执行结果
           │
           └─→ 分发处理：
               ├─ processBootstrapCommand()
               │   ├─ processBootstrapSuccess()
               │   │   └─ 标记 Bootstrapped=true
               │   └─ processBootstrapFailure()
               │       └─ 标记失败状态
               │
               └─ processResetCommand()
                   └─ 移除 Finalizer
```

## Command 生命周期状态机

```
┌─────────────┐
│  Created    │  管理集群创建 Command
└──────┬──────┘
       │
       ▼
┌─────────────┐
│  Pending    │  等待 BKEAgent 拾取
└──────┬──────┘
       │
       ▼
┌─────────────┐
│  Running    │  BKEAgent 执行中
└──────┬──────┘
       │
       ├─────────────┐
       │             │
       ▼             ▼
┌─────────────┐ ┌─────────────┐
│  Complete   │ │  Suspend    │
│  (成功/失败) │ │  (暂停)      │
└─────────────┘ └─────────────┘
       │
       ▼
┌─────────────┐
│  TTL Clean  │  TTL 后自动清理
└─────────────┘
```

## 总结

### Command 调谐涉及的主要代码

| 模块 | 文件 | 主要职责 |
|------|------|---------|
| **CommandReconciler** | `controllers/bkeagent/command_controller.go` | 执行 Command（BKEAgent 端） |
| **BKEMachineReconciler** | `controllers/capbke/bemachine_controller_phases.go` | 创建和监控 Command（管理集群端） |
| **BaseCommand** | `pkg/command/command.go` | Command 创建工具 |
| **Command Utils** | `pkg/phaseframe/phaseutil/command.go` | Command 工具函数 |
| **Command Predicate** | `utils/capbke/predicates/command.go` | Watch 过滤器 |

### 核心交互流程

1. **管理集群创建 Command**：`BKEMachineReconciler` 创建 Command CR
2. **BKEAgent 执行 Command**：`CommandReconciler` 执行 Command 并更新 Status
3. **管理集群监控结果**：`BKEMachineReconciler` 通过 Watch 监控 Command 状态变化
4. **处理执行结果**：根据成功/失败更新 BKEMachine 状态

这种设计实现了**声明式命令执行模型**，管理集群与工作节点通过 Command CRD 解耦，是典型的 Kubernetes 控制器模式。

# 整理 **BKEMachine 创建的触发流程**

## BKEMachine 创建触发流程

### 一、创建流程概览

```
用户操作                    Cluster API                    BKE Provider
─────────                  ──────────                     ────────────
创建 Machine ──────→ Machine Controller ──────→ 创建 BKEMachine
     │                        │                              │
     │                        │                              │
     └─ Machine.Spec.         └─ 读取 InfrastructureRef      └─ BKEMachine 被创建
        InfrastructureRef        创建对应的 Infra Machine        触发 Reconcile
```

### 二、详细触发步骤

#### 步骤 1：用户创建 Machine 资源

用户通过以下方式创建 Machine：
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Machine
metadata:
  name: my-machine
  namespace: default
  labels:
    cluster.x-k8s.io/cluster-name: my-cluster
spec:
  clusterName: my-cluster
  infrastructureRef:
    apiVersion: bke.bocloud.com/v1beta1
    kind: BKEMachine
    name: my-bke-machine    # 指向要创建的 BKEMachine
    namespace: default
```
**关键点**：
- `spec.infrastructureRef` 指定了要创建的基础设施 Machine 类型为 `BKEMachine`
- 这是 Cluster API 的标准模式

#### 步骤 2：Cluster API Machine Controller 创建 BKEMachine

**Cluster API 的 Machine Controller** 会：
1. **监听 Machine 资源的变化**
2. **读取 `Machine.Spec.InfrastructureRef`**
3. **根据 InfrastructureRef 创建对应的 BKEMachine**

**代码位置**：这是 Cluster API 框架的标准行为，不在本项目中

#### 步骤 3：BKEMachineTemplate 定义模板（可选）

通常通过 `BKEMachineTemplate` 来定义 BKEMachine 的模板：

**文件位置**：[bkemachinetemplate_types.go](file:///\cluster-api-provider-bke\api\capbke\v1beta1\bkemachinetemplate_types.go)
```go
type BKEMachineTemplate struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec BKEMachineTemplateSpec `json:"spec,omitempty"`
}

type BKEMachineTemplateSpec struct {
    Template BKEMachineTemplateResource `json:"template"`
}

type BKEMachineTemplateResource struct {
    ObjectMeta clusterv1.ObjectMeta `json:"metadata,omitempty"`
    Spec BKEMachineSpec `json:"spec"`
}
```
**使用场景**：
- **KubeadmControlPlane** 使用 `BKEMachineTemplate` 创建控制平面节点
- **MachineDeployment** 使用 `BKEMachineTemplate` 创建工作节点

#### 步骤 4：BKEMachineReconciler 开始协调

**文件位置**：[bkemachine_controller.go](file:///\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L547-L567)

BKEMachine 创建后，`BKEMachineReconciler` 会通过以下 Watch 机制被触发：
```go
func (r *BKEMachineReconciler) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&bkev1beta1.BKEMachine{}).  // ← 监听 BKEMachine 创建
        WithOptions(options).
        Watches(
            &agentv1beta1.Command{},
            handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), 
                &bkev1beta1.BKEMachine{}, handler.OnlyControllerOwner()),
            builder.WithPredicates(predicates.CommandUpdateCompleted()),
        ).
        Watches(
            &clusterv1.Machine{},
            handler.EnqueueRequestsFromMapFunc(
                util.MachineToInfrastructureMapFunc(
                    bkev1beta1.GroupVersion.WithKind("BKEMachine"))),  // ← Machine 变化触发
        ).
        Watches(
            &clusterv1.Cluster{},
            handler.EnqueueRequestsFromMapFunc(clusterToBKEMachines),
            builder.WithPredicates(predicates.ClusterUnPause()),
        ).
        Watches(
            &bkev1beta1.BKECluster{},
            handler.EnqueueRequestsFromMapFunc(r.BKEClusterToBKEMachines),
            builder.WithPredicates(predicates.BKEAgentReady(), predicates.BKEClusterUnPause()),
        ).
        Complete(r)
}
```

### 三、触发 BKEMachine Reconcile 的场景

根据代码分析，以下场景会触发 BKEMachine 的 Reconcile：

| 触发源 | 触发条件 | 代码位置 |
|--------|---------|---------|
| **BKEMachine 创建/更新** | BKEMachine 资源被创建或更新 | `For(&bkev1beta1.BKEMachine{})` |
| **Machine 变化** | Machine 的 `InfrastructureRef` 指向 BKEMachine | `MachineToInfrastructureMapFunc` |
| **Cluster 变化** | Cluster UnPause | `ClusterUnPause()` Predicate |
| **BKECluster 变化** | BKECluster Agent Ready 且 UnPause | `BKEClusterToBKEMachines` |
| **Command 完成** | Command 状态变为 Complete | `CommandUpdateCompleted()` |

### 四、Reconcile 流程中的关键检查

**文件位置**：[bkemachine_controller.go](file:///\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L165-L185)
```go
func (r *BKEMachineReconciler) fetchRequiredObjects(ctx context.Context, 
    req ctrl.Request, log *zap.SugaredLogger) (*RequiredObjects, error) {
    
    // 1. 获取 BKEMachine
    bkeMachine := &bkev1beta1.BKEMachine{}
    if err := r.Get(ctx, req.NamespacedName, bkeMachine); err != nil {
        return nil, err
    }
    
    // 2. 获取关联的 Machine（Cluster API）
    machine, err := util.GetOwnerMachine(ctx, r.Client, bkeMachine.ObjectMeta)
    if machine == nil {
        log.Info("Waiting for Machine Controller to set OwnerRef on BKEMachine")
        return nil, nil  // ← 等待 Machine Controller 设置 OwnerRef
    }
    
    // 3. 获取关联的 Cluster
    cluster, err := util.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
    if cluster == nil {
        log.Info("BKEMachine owner Machine is missing cluster label")
        return nil, err
    }
    
    // 4. 获取关联的 BKECluster
    bkeCluster, err := mergecluster.GetCombinedBKECluster(ctx, r.Client, 
        bkeMachine.Namespace, cluster.Spec.InfrastructureRef.Name)
    
    return &RequiredObjects{...}, nil
}
```
**关键点**：
- 如果 `machine == nil`，说明 Machine Controller 还没有设置 OwnerRef，Reconcile 会等待
- 这是 Cluster API 的标准模式：**Infrastructure Provider 等待 Cluster API 设置关联关系**

### 五、完整创建时序图

```
┌─────────┐          ┌──────────────┐          ┌──────────────┐          ┌──────────────┐
│  User   │          │ Machine Ctrl │          │ BKEMachine   │          │ BKECluster   │
│         │          │  (ClusterAPI)│          │  Reconciler  │          │  Reconciler  │
└────┬────┘          └──────┬───────┘          └──────┬───────┘          └──────┬───────┘
     │                      │                         │                         │
     │ 1. Create Machine    │                         │                         │
     │─────────────────────→│                         │                         │
     │                      │                         │                         │
     │                      │ 2. Create BKEMachine    │                         │
     │                      │─────────────────────────→                         │
     │                      │                         │                         │
     │                      │                         │ 3. Reconcile Triggered  │
     │                      │                         │←────────────────────────│
     │                      │                         │                         │
     │                      │ 4. Set OwnerRef         │                         │
     │                      │─────────────────────────→                         │
     │                      │                         │                         │
     │                      │                         │ 5. Wait for Machine     │
     │                      │                         │    Controller           │
     │                      │                         │                         │
     │                      │                         │ 6. Start Bootstrap      │
     │                      │                         │─────────────────────────→
     │                      │                         │                         │
```

### 六、实际使用示例

#### 场景 1：通过 KubeadmControlPlane 创建控制平面节点

```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1beta1
kind: KubeadmControlPlane
metadata:
  name: my-cluster-control-plane
spec:
  machineTemplate:
    infrastructureRef:
      apiVersion: bke.bocloud.com/v1beta1
      kind: BKEMachineTemplate
      name: my-cluster-control-plane
  # ... 其他配置
---
apiVersion: bke.bocloud.com/v1beta1
kind: BKEMachineTemplate
metadata:
  name: my-cluster-control-plane
spec:
  template:
    spec:
      # BKEMachine 配置
```
**流程**：
1. KubeadmControlPlane Controller 创建 Machine
2. Machine Controller 根据 `infrastructureRef` 创建 BKEMachine
3. BKEMachineReconciler 开始协调

#### 场景 2：通过 MachineDeployment 创建工作节点

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachineDeployment
metadata:
  name: my-cluster-md-0
spec:
  template:
    spec:
      infrastructureRef:
        apiVersion: bke.bocloud.com/v1beta1
        kind: BKEMachineTemplate
        name: my-cluster-md-0
  # ... 其他配置
```

### 七、总结

| 问题 | 答案 |
|------|------|
| **谁创建 BKEMachine？** | Cluster API 的 Machine Controller |
| **触发条件是什么？** | 用户创建 Machine，且 `spec.infrastructureRef.kind = BKEMachine` |
| **如何定义 BKEMachine 规格？** | 通过 `BKEMachineTemplate` 定义模板 |
| **创建后谁处理？** | `BKEMachineReconciler` 开始协调 |
| **等待什么？** | 等待 Machine Controller 设置 OwnerRef |

**核心机制**：
- BKEMachine 的创建遵循 **Cluster API 的 Infrastructure Provider 模式**
- Machine Controller 负责创建 Infrastructure Machine（BKEMachine）
- BKEMachineReconciler 负责协调 BKEMachine 的状态

# 整理 **控制平面触发 BKEMachine 创建的完整流程**

## 控制平面触发 BKEMachine 创建流程

### 一、核心架构

```
┌─────────────────────────────────────────────────────────────────────┐
│                    Cluster API 架构层次                              │
└─────────────────────────────────────────────────────────────────────┘

Cluster (集群)
    │
    ├─→ ControlPlaneRef ─→ KubeadmControlPlane (控制平面)
    │                           │
    │                           └─→ machineTemplate.infrastructureRef
    │                                    │
    │                                    └─→ BKEMachineTemplate (机器模板)
    │
    └─→ InfrastructureRef ─→ BKECluster (基础设施集群)
```

### 二、详细创建流程

#### 步骤 1：用户创建 Cluster 资源

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: my-cluster
  namespace: default
spec:
  controlPlaneRef:                    # ← 指向控制平面
    apiVersion: controlplane.cluster.x-k8s.io/v1beta1
    kind: KubeadmControlPlane
    name: my-cluster-control-plane
  infrastructureRef:                  # ← 指向基础设施集群
    apiVersion: bke.bocloud.com/v1beta1
    kind: BKECluster
    name: my-cluster
```
**关键点**：
- `spec.controlPlaneRef` 指向 `KubeadmControlPlane`
- `spec.infrastructureRef` 指向 `BKECluster`

#### 步骤 2：KubeadmControlPlane 定义机器模板

```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1beta1
kind: KubeadmControlPlane
metadata:
  name: my-cluster-control-plane
  namespace: default
spec:
  machineTemplate:
    infrastructureRef:                # ← 指向 BKEMachine 模板
      apiVersion: bke.bocloud.com/v1beta1
      kind: BKEMachineTemplate
      name: my-cluster-control-plane
  replicas: 3                         # ← 控制平面节点数量
  version: v1.28.0
  # ... kubeadm 配置
```
**关键点**：
- `machineTemplate.infrastructureRef` 指向 `BKEMachineTemplate`
- `replicas` 定义了控制平面节点数量

#### 步骤 3：BKEMachineTemplate 定义机器规格
**文件位置**：[bkemachinetemplate_types.go](file:///\cluster-api-provider-bke\api\capbke\v1beta1\bkemachinetemplate_types.go)
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKEMachineTemplate
metadata:
  name: my-cluster-control-plane
  namespace: default
spec:
  template:
    spec:
      # BKEMachine 的具体规格
      # 例如：节点选择、资源配置等
```

**代码定义**：
```go
type BKEMachineTemplate struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec BKEMachineTemplateSpec `json:"spec,omitempty"`
}

type BKEMachineTemplateSpec struct {
    Template BKEMachineTemplateResource `json:"template"`
}

type BKEMachineTemplateResource struct {
    ObjectMeta clusterv1.ObjectMeta `json:"metadata,omitempty"`
    Spec BKEMachineSpec `json:"spec"`
}
```

#### 步骤 4：KubeadmControlPlane Controller 创建 Machine

**Cluster API 的 KubeadmControlPlane Controller** 会：
1. **监听 KubeadmControlPlane 资源**
2. **根据 `replicas` 和 `machineTemplate` 创建 Machine**
```yaml
# KubeadmControlPlane Controller 创建的 Machine 示例
apiVersion: cluster.x-k8s.io/v1beta1
kind: Machine
metadata:
  name: my-cluster-control-plane-xxxxx
  namespace: default
  labels:
    cluster.x-k8s.io/cluster-name: my-cluster
    cluster.x-k8s.io/control-plane: ""    # ← 控制平面标签
  ownerReferences:
    - apiVersion: controlplane.cluster.x-k8s.io/v1beta1
      kind: KubeadmControlPlane
      name: my-cluster-control-plane
spec:
  clusterName: my-cluster
  version: v1.28.0
  infrastructureRef:                     # ← 指向要创建的 BKEMachine
    apiVersion: bke.bocloud.com/v1beta1
    kind: BKEMachine
    name: my-cluster-control-plane-xxxxx
    namespace: default
  bootstrap:
    configRef:
      apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
      kind: KubeadmConfig
      name: my-cluster-control-plane-xxxxx
```
**关键点**：
- Machine 自动添加 `cluster.x-k8s.io/control-plane` 标签
- `infrastructureRef` 指向要创建的 BKEMachine

#### 步骤 5：Machine Controller 创建 BKEMachine

**Cluster API 的 Machine Controller** 会：
1. **监听 Machine 资源**
2. **读取 `Machine.Spec.InfrastructureRef`**
3. **根据 BKEMachineTemplate 创建 BKEMachine**
```yaml
# Machine Controller 创建的 BKEMachine 示例
apiVersion: bke.bocloud.com/v1beta1
kind: BKEMachine
metadata:
  name: my-cluster-control-plane-xxxxx
  namespace: default
  labels:
    cluster.x-k8s.io/cluster-name: my-cluster
    cluster.x-k8s.io/control-plane: ""    # ← 控制平面标签
  ownerReferences:
    - apiVersion: cluster.x-k8s.io/v1beta1
      kind: Machine
      name: my-cluster-control-plane-xxxxx
spec:
  # 从 BKEMachineTemplate 复制的规格
```

#### 步骤 6：BKEMachineReconciler 开始协调

**文件位置**：[bkemachine_controller.go](file:///\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L547-L567)
```go
func (r *BKEMachineReconciler) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&bkev1beta1.BKEMachine{}).  // ← 监听 BKEMachine 创建
        WithOptions(options).
        // ... 其他 Watch
        Complete(r)
}
```

### 三、代码层面的关键函数

#### 1. 获取 KubeadmControlPlane

**文件位置**：[clusterapi.go](file:///\cluster-api-provider-bke\pkg\phaseframe\phaseutil\clusterapi.go#L58-L73)
```go
func GetClusterAPIKubeadmControlPlane(ctx context.Context, c client.Client, 
    cluster *clusterv1beta1.Cluster) (*controlv1beta1.KubeadmControlPlane, error) {
    if cluster == nil {
        return nil, errors.New("cluster is nil")
    }

    cp := &controlv1beta1.KubeadmControlPlane{}
    key := client.ObjectKey{
        Namespace: cluster.Spec.ControlPlaneRef.Namespace,  // ← 从 Cluster 获取
        Name:      cluster.Spec.ControlPlaneRef.Name,
    }
    if err := c.Get(ctx, key, cp); err != nil {
        return nil, err
    }
    return cp, nil
}
```

#### 2. 判断是否为控制平面 BKEMachine

**文件位置**：[bkemachine.go](file:///\cluster-api-provider-bke\pkg\phaseframe\phaseutil\bkemachine.go#L59-L62)
```go
func IsControlPlaneBKEMachine(machine *bkev1beta1.BKEMachine) bool {
    _, ok := machine.ObjectMeta.Labels[clusterv1.MachineControlPlaneLabel]
    return ok  // ← 检查是否有控制平面标签
}
```

#### 3. 获取控制平面的所有 BKEMachine

**文件位置**：[bkemachine.go](file:///\cluster-api-provider-bke\pkg\phaseframe\phaseutil\bkemachine.go#L64-L73)
```go
func GetControlPlaneBKEMachines(ctx context.Context, c client.Client, 
    cluster *bkev1beta1.BKECluster) ([]*bkev1beta1.BKEMachine, error) {
    machines, err := GetBKEClusterAssociateBKEMachines(ctx, c, cluster)
    if err != nil {
        return nil, err
    }

    var controlPlaneMachines []*bkev1beta1.BKEMachine
    for _, machine := range machines {
        if IsControlPlaneBKEMachine(machine) {
            controlPlaneMachines = append(controlPlaneMachines, machine)
        }
    }
    return controlPlaneMachines, nil
}
```

### 四、完整时序图

```
┌─────────┐    ┌──────────────┐    ┌──────────────────┐    ┌──────────┐    ┌──────────┐
│  User   │    │   Cluster    │    │ KubeadmControl   │    │  Machine │    │ BKEMachine│
│         │    │   Controller │    │ Plane Controller │    │   Ctrl   │    │ Reconciler│
└────┬────┘    └──────┬───────┘    └────────┬─────────┘    └────┬─────┘    └─────┬────┘
     │                │                     │                   │                │
     │ 1. Create      │                     │                   │                │
     │    Cluster     │                     │                   │                │
     │───────────────→                     │                   │                │
     │                │                     │                   │                │
     │                │ 2. Reconcile        │                   │                │
     │                │←────────────────────│                   │                │
     │                │                     │                   │                │
     │                │                     │ 3. Create Machine │                │
     │                │                     │  (根据 replicas)  │                │
     │                │                     │──────────────────→                │
     │                │                     │                   │                │
     │                │                     │                   │ 4. Create      │
     │                │                     │                   │    BKEMachine  │
     │                │                     │                   │────────────────→
     │                │                     │                   │                │
     │                │                     │                   │                │ 5. Reconcile
     │                │                     │                   │                │←─────
     │                │                     │                   │                │
     │                │                     │                   │                │ 6. Bootstrap
     │                │                     │                   │                │    (Init)
     │                │                     │                   │                │─────→
```

### 五、控制平面节点识别

#### 通过 Label 识别

```go
// Machine 和 BKEMachine 都会有这个标签
labels:
  cluster.x-k8s.io/control-plane: ""
```

#### 代码中的判断逻辑

```go
// 判断是否为控制平面节点
if _, ok := machine.Labels[clusterv1.MachineControlPlaneLabel]; ok {
    // 这是控制平面节点
}
```

### 六、控制平面节点的特殊处理

#### 1. 第一个控制平面节点

```go
// 获取第一个创建的控制平面节点
func GetControlPlaneInitBKEMachine(ctx context.Context, c client.Client, 
    cluster *bkev1beta1.BKECluster) (*bkev1beta1.BKEMachine, error) {
    machines, err := GetControlPlaneBKEMachines(ctx, c, cluster)
    if err != nil {
        return nil, err
    }

    // 返回创建时间最早的节点
    var initMachine *bkev1beta1.BKEMachine
    for _, machine := range machines {
        if initMachine == nil || 
           machine.CreationTimestamp.Before(&initMachine.CreationTimestamp) {
            initMachine = machine
        }
    }
    return initMachine, nil
}
```

#### 2. 控制平面初始化阶段

控制平面节点会执行特殊的初始化阶段：

| 阶段 | 说明 |
|------|------|
| **InitControlPlane** | 第一个控制平面节点初始化 |
| **JoinControlPlane** | 其他控制平面节点加入 |

**代码位置**：[kubeadm.go](file:///\cluster-api-provider-bke\pkg\job\builtin\kubeadm\kubeadm.go#L78-L80)
```go
"phase": {
    Key:         "phase",
    Value:       "initControlPlane,joinControlPlane,joinWorker,upgradeControlPlane,upgradeWorker,upgradeEtcd",
    Required:    true,
    Default:     "initControlPlane",
    Description: "phase",
}
```

### 七、总结

| 问题 | 答案 |
|------|------|
| **谁触发 BKEMachine 创建？** | KubeadmControlPlane Controller → Machine Controller |
| **触发链路是什么？** | Cluster → KubeadmControlPlane → Machine → BKEMachine |
| **如何定义控制平面节点？** | 通过 `KubeadmControlPlane.machineTemplate.infrastructureRef` 指向 `BKEMachineTemplate` |
| **如何识别控制平面节点？** | 通过 `cluster.x-k8s.io/control-plane` Label |
| **第一个节点有什么特殊？** | 执行 `initControlPlane` 阶段，其他节点执行 `joinControlPlane` |
| **创建后谁处理？** | `BKEMachineReconciler` 开始协调 |

**核心机制**：
- 遵循 **Cluster API 的 Control Plane Provider 模式**
- KubeadmControlPlane 负责管理控制平面节点的生命周期
- 通过 BKEMachineTemplate 定义控制平面节点的规格
- 自动添加控制平面 Label 用于识别

# 

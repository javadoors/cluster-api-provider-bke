# EnsureMasterInit 业务流程梳理
## 一、Phase 概览
[ensure_master_init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go) 负责集群**首个 Master 节点的初始化**，是整个集群引导的关键 Phase。它不直接执行初始化命令，而是**等待并监控**由 BKEMachine Controller 创建的 Bootstrap Command 的执行结果。
## 二、整体业务流程
```
┌──────────────────────────────────────────────────────────────────────────┐
│                     EnsureMasterInit.Execute()                           │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │ 1. setupConditionAndRefresh()                                     │  │
│  │    标记 ControlPlaneInitializedCondition=False                     │  │
│  │    同步状态到 API Server                                           │  │
│  └────────────────────────────────────────────────────────────────────┘  │
│                              ↓                                           │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │ 2. wait.PollImmediateUntil(1s, masterInitPollFunc, timeout)       │  │
│  │                                                                    │  │
│  │    每次轮询执行 4 个 Step（串行）：                                  │  │
│  │    ┌──────────────────────────────────────────────────────────┐    │  │
│  │    │ Step 1: checkClusterInitializedStep                     │    │  │
│  │    │   ClusterAPI Cluster 已初始化？ → 直接返回成功            │    │  │
│  │    └──────────────────────────────────────────────────────────┘    │  │
│  │                              ↓                                     │  │
│  │    ┌──────────────────────────────────────────────────────────┐    │  │
│  │    │ Step 2: waitForCommandCompleteStep                       │    │  │
│  │    │   查找 MasterInit Command → 检查执行状态                  │    │  │
│  │    │   ├── 成功 → commandCompleteFlag=true                    │    │  │
│  │    │   ├── 失败 → ProcessCommandFailure → 重试/报错           │    │  │
│  │    │   └── 未完成 → 继续等待                                  │    │  │
│  │    └──────────────────────────────────────────────────────────┘    │  │
│  │                              ↓                                     │  │
│  │    ┌──────────────────────────────────────────────────────────┐    │  │
│  │    │ Step 3: waitForMachineBootstrapStep                      │    │  │
│  │    │   BKEMachine.Status.Bootstrapped == true？               │    │  │
│  │    └──────────────────────────────────────────────────────────┘    │  │
│  │                              ↓                                     │  │
│  │    ┌──────────────────────────────────────────────────────────┐    │  │
│  │    │ Step 4: checkClusterFinalStep                            │    │  │
│  │    │   ClusterAPI Cluster ControlPlaneInitialized==True？      │    │  │
│  │    └──────────────────────────────────────────────────────────┘    │  │
│  └────────────────────────────────────────────────────────────────────┘  │
│                              ↓                                           │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │ 3. defer: 若未初始化成功，标记 Condition=False                     │  │
│  │    防止环境初始化 Command 清除已 init 完成的部分                    │  │
│  └────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────┘
```
## 三、NeedExecute —— 判断是否需要执行
```go
func (e *EnsureMasterInit) NeedExecute(old, new) bool {
    // 1. DefaultNeedExecute 返回 false → 不执行
    
    // 2. 刷新 ClusterAPI Cluster 对象
    //    Cluster.ControlPlaneInitializedCondition == True → 不执行
    //    （集群已初始化，无需重复执行）
    
    // 3. 否则 → 需要执行，设置 PhaseWaiting
}
```
**关键判断**：以 CAPI Cluster 的 `ControlPlaneInitializedCondition` 为准，一旦为 True 就不再执行。
## 四、Execute 详细流程
### 4.1 前置准备
```go
// Step 1: 标记条件
setupConditionAndRefresh()
  ├── ConditionMark(ControlPlaneInitializedCondition=False, "Master still not init")
  ├── SyncStatusUntilComplete()  → 同步到 API Server
  └── RefreshCtxBKECluster()    → 刷新本地缓存

// Step 2: 获取超时时间
timeOut = GetBootTimeOut(bkeCluster)  // 从 Annotation 读取，默认值
ctx, cancel = context.WithTimeout(ctx, timeOut)
```
### 4.2 轮询核心逻辑 —— masterInitPollFunc
每次轮询（间隔 1s）依次执行 4 个 Step：
#### Step 1: checkClusterInitializedStep —— 快速路径检查
```
刷新 Cluster 和 BKECluster 对象
  ├── Cluster.ControlPlaneInitializedCondition == True
  │   → 标记 BKECluster Condition=True
  │   → 返回 (done=true, success=true)  ← 轮询结束，初始化成功
  │
  └── 否则 → 继续下一步
```
**作用**：如果集群已经初始化（可能被其他流程完成），直接跳过后续步骤。
#### Step 2: waitForCommandCompleteStep —— 等待初始化命令完成
这是最核心的步骤，涉及 Command 的查找、状态检查和失败处理。
```
waitForCommandCompleteStep()
│
├── getInitCommandStep()  — 查找 MasterInit Command
│   │
│   │  GetMasterInitCommand() 
│   │  → 列出所有 Command，查找带 MasterInitCommandLabel 的
│   │
│   ├── Command 不存在 → 返回 nil，继续等待
│   │   （BKEMachine Controller 尚未创建 Command）
│   │
│   └── Command 存在 → 提取 InitNodeIp
│       ├── 从 Command.Spec.NodeName 获取
│       └── 或从 NodeSelector.MatchLabels 的 key 获取
│
├── CheckCommandStatus(initCommand)  — 检查命令执行状态
│   ├── complete=false → 继续等待
│   ├── complete=true, failedNodes≠∅ → 失败处理
│   └── complete=true, successNodes≠∅ → commandCompleteFlag=true
│
└── processCommandComplete()  — 处理命令完成结果
    ├── 有失败节点 → processCommandFailure()
    └── 有成功节点 → 标记 commandCompleteFlag=true
```
**Command 创建来源**：不是 EnsureMasterInit 创建的，而是由 BKEMachine Controller 创建：
```
BKEMachine Controller
  ├── 判断 Phase = InitControlPlane
  ├── 选择第一个 Master 节点
  ├── 创建 Bootstrap Command:
  │   ├── Label: MasterInitCommandLabel
  │   ├── Commands:
  │   │   ├── "K8sEnvInit" (check container runtime)
  │   │   └── "Kubeadm" (phase=InitControlPlane)
  │   └── NodeSelector: 指向第一个 Master 节点
  └── BKEAgent 执行 Command
```
#### Step 3: waitForMachineBootstrapStep —— 等待 BKEMachine 标记已引导
```
waitForMachineBootstrapStep()
│
├── machineBootFlag == true → 跳过（已标记）
│
└── machineBootFlag == false
    ├── GetControlPlaneInitBKEMachine()
    │   → 获取最早创建的 BKEMachine（即 init 节点对应的 Machine）
    │
    ├── BKEMachine.Status.Bootstrapped == false → 继续等待
    └── BKEMachine.Status.Bootstrapped == true
        → machineBootFlag = true
```
**Bootstrapped 标记**：由 BKEMachine Controller 在检测到 Command 执行成功后设置。
#### Step 4: checkClusterFinalStep —— 最终确认集群初始化
```
checkClusterFinalStep()
│
├── Cluster.ControlPlaneInitializedCondition == True → 等待 CAPI 设置
│
└── 否则 → 继续等待
```
**CAPI Cluster 初始化标记**：由 CAPI 的 KubeadmControlPlane Controller 在检测到首个控制平面节点就绪后自动设置。
### 4.3 失败处理 —— ProcessCommandFailure
[common.go:141](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/common.go#L141)：
```
ProcessCommandFailure()
│
├── Sleep(2s)  — 等待状态稳定
│
├── RefreshCtxBKECluster()  — 刷新集群状态
│
├── 检查节点是否有 NodeFailedFlag
│   ├── 有 NodeFailedFlag
│   │   ├── 设置节点状态 NodeBootStrapFailed
│   │   ├── 删除失败的 Command
│   │   └── 返回 (Done=true, Success=false)  ← 终止轮询，报错
│   │
│   └── 无 NodeFailedFlag（可重试的失败）
│       ├── 移除 BKEMachine 的 Master Label
│       │   → BKEMachine Controller 下次调和时会重新创建 Command
│       └── 返回 (Done=false, Success=false)  ← 继续轮询，等待重试
```
**重试机制**：通过移除 BKEMachine 的 Master Label，触发 BKEMachine Controller 重新创建 Bootstrap Command，实现自动重试。
### 4.4 Defer 保护
```go
defer func() {
    // 刷新 Cluster 对象
    RefreshCtxCluster()
    
    // 如果集群未初始化成功，确保 Condition=False
    if Cluster != nil && !ControlPlaneInitializedCondition {
        ConditionMark(ControlPlaneInitializedCondition=False, "Master still not init")
        SyncStatusUntilComplete()
    }
}()
```
**作用**：防止在初始化过程中被其他 Phase（如 EnsureNodesEnv）误清除已完成的初始化状态。
## 五、完整时序图
```
  BKEMachine Controller          EnsureMasterInit Phase         BKEAgent (on Init Node)
  ─────────────────────          ──────────────────────         ──────────────────────
         │                              │                              │
         │  检测到首个 Master 节点      │                              │
         │  Phase=InitControlPlane      │                              │
         │                              │                              │
  创建 Bootstrap Command ──────────────→│                              │
  (MasterInitCommandLabel)              │                              │
  标记 MasterInitFlag                   │                              │
         │                              │                              │
         │                     Execute() 开始                          │
         │                     标记 Condition=False                    │
         │                              │                              │
         │                     ┌─── Poll Loop ─────────────────────┐   │
         │                     │                                   │   │
         │                     │ Step1: Cluster 已初始化？ No      │   │
         │                     │                                   │   │
         │                     │ Step2: 查找 MasterInit Command    │   │
         │                     │   → 找到！提取 InitNodeIp         │   │
         │                     │   → CheckCommandStatus            │   │
         │                     │     → complete=false              │   │
         │                     │     → 继续等待...                 │   │
         │                     │                                   │   │
         │                     │          ┌── Agent 执行 Command ──┤   │
         │                     │          │ K8sEnvInit (check)     │   │
         │                     │          │ Kubeadm Init:          │   │
         │                     │          │  ├ install kubectl     │   │
         │                     │          │  ├ init certs          │   │
         │                     │          │  ├ generate manifests  │   │
         │                     │          │  ├ install kubelet     │   │
         │                     │          │  └ upload config       │   │
         │                     │          └────────────────────────┘   │
         │                     │                                   │   │
         │                     │ Step2: CheckCommandStatus         │   │
         │                     │   → complete=true, success!       │   │
         │                     │   → commandCompleteFlag=true      │   │
         │                     │                                   │   │
         │  设置 Bootstrapped  │ Step3: BKEMachine.Bootstrapped?   │   │
         │  ←──────────────────│   → true! machineBootFlag=true    │   │
         │                     │                                   │   │
         │                     │ Step4: Cluster.Initialized?       │   │
         │                     │   → CAPI Controller 设置 True     │   │
         │                     │                                   │   │
         │                     └───────────────────────────────────┘   │
         │                              │                              │
         │                     标记 Condition=True                     │
         │                     SyncStatusUntilComplete()               │
         │                     返回 ctrl.Result{}                      │
```
## 六、关键设计要点
| 要点 | 说明 |
|------|------|
| **职责分离** | EnsureMasterInit 不创建 Command，只等待和监控。Command 由 BKEMachine Controller 创建 |
| **幂等安全** | 通过 CAPI Cluster 的 `ControlPlaneInitializedCondition` 保证只执行一次 |
| **自动重试** | 失败时移除 BKEMachine Label，触发 BKEMachine Controller 重新创建 Command |
| **超时控制** | 通过 `GetBootTimeOut` 获取超时时间，支持自定义 |
| **Defer 保护** | 退出时确保未初始化成功的集群 Condition=False，防止其他 Phase 误操作 |
| **轮询间隔** | 1 秒轮询，每 10 次输出一次日志（避免日志爆炸） |
| **单节点初始化** | 只初始化第一个 Master 节点（最早创建的 BKEMachine），其他 Master 由 EnsureMasterJoin 处理 |
## 七、与上下游 Phase 的关系
```
DeployPhases 执行顺序:
  ┌──────────────────┐
  │ EnsureBKEAgent   │ ← 部署 Agent 到所有节点
  ├──────────────────┤
  │ EnsureNodesEnv   │ ← 初始化节点环境（OS/容器运行时）
  ├──────────────────┤
  │ EnsureClusterAPI │ ← 创建 CAPI 资源（Cluster/Machine）
  ├──────────────────┤
  │ EnsureCerts      │ ← 生成证书
  ├──────────────────┤
  │ EnsureLoadBalance│ ← 配置 HAProxy+Keepalived
  ├──────────────────┤
  │ EnsureMasterInit │ ← ★ 等待首个 Master 初始化完成
  ├──────────────────┤
  │ EnsureMasterJoin │ ← 其他 Master 节点加入集群
  ├──────────────────┤
  │ EnsureWorkerJoin │ ← Worker 节点加入集群
  └──────────────────┘
```
EnsureMasterInit 的前置条件：
- EnsureNodesEnv 已完成（节点环境就绪，NodeEnvFlag=true）
- EnsureCerts 已完成（证书已生成）
- EnsureLoadBalance 已完成（VIP 已配置，API Server 可通过 VIP 访问）
- BKEMachine Controller 已创建 Bootstrap Command
        

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
        
# Command 是如何由 BKEMachine Controller 创建的？
根据 `ensure_master_init.md` 的内容，`Command` 的创建逻辑如下：
## 1. 创建者与时机
`Command` **不是**由 `EnsureMasterInit` Phase 创建的，而是由 **`BKEMachine Controller`** 负责创建。
*   **触发条件**：当 `BKEMachine Controller` 检测到**首个 Master 节点**，且该节点处于 `InitControlPlane` 阶段时。
## 2. 创建内容详情
`BKEMachine Controller` 会创建一个 **Bootstrap Command**，具体包含：
*   **标识 (Label)**：打上 `MasterInitCommandLabel` 标签，以便 `EnsureMasterInit` Phase 能够通过该标签查找到该 Command。
*   **执行指令 (Commands)**：
    1.  `"K8sEnvInit"`：用于检查容器运行时等环境状态。
    2.  `"Kubeadm"`：指定 `phase=InitControlPlane`，用于执行控制平面的初始化操作。
*   **目标节点 (NodeSelector)**：通过 `NodeSelector` 将命令指向**第一个 Master 节点**（即最早创建的 BKEMachine）。
## 3. 与 Phase 的协作关系
*   **BKEMachine Controller**：负责“生产”Command。
*   **BKEAgent**：负责在节点上“执行”Command。
*   **EnsureMasterInit Phase**：负责“监控”Command。它通过轮询查找带有 `MasterInitCommandLabel` 的 Command，检查其执行状态（是否完成、是否成功），并等待 `BKEMachine.Status.Bootstrapped` 变为 `true`。
## 4. 失败重试机制
如果 Command 执行失败，`EnsureMasterInit` 会移除 `BKEMachine` 的 Master Label。这一操作会触发 `BKEMachine Controller` 重新调和（Reconcile），进而**重新创建**一个新的 Bootstrap Command 进行重试。

# MasterInit Command 是由 BKEMachine Controller 创建的
具体触发流程如下：

## 触发执行流程

### 1. **入口：BKEMachine Controller 的 Reconcile 循环**

在 [bkemachine_controller_phases.go](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go) 中：
```
reconcileBootstrap() 
  ↓
handleFirstTimeReconciliation()  // 处理首次协调的机器
  ↓
handleRealBootstrap()            // 处理真实引导流程
  ↓
bootstrapCommand.New()           // 创建 Bootstrap Command
```

### 2. **关键触发条件**
在 [handleFirstTimeReconciliation](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L207-L314) 函数中：
- **Phase 判断**：通过 `getBootstrapPhase()` 获取当前 BKEMachine 的阶段
- **节点选择**：通过 `filterAvailableNode()` 选择可用的节点
- **标记节点**：如果 Phase 是 `InitControlPlane`，会标记 `MasterInitFlag`

```go
if phase == bkev1beta1.InitControlPlane {
    if err := r.NodeFetcher.MarkNodeStateFlagForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.MasterInitFlag); err != nil {
        params.Log.Warnf("Failed to mark node state flag: %v", err)
    }
}
```

### 3. **Command 创建逻辑**
在 [handleRealBootstrap](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L418-L468) 函数中：
```go
bootstrapCommand := command.Bootstrap{
    BaseCommand: command.BaseCommand{
        Ctx:             params.Ctx,
        NameSpace:       params.BKEMachine.Namespace,
        Client:          r.Client,
        Scheme:          r.Scheme,
        OwnerObj:        params.BKEMachine,
        ClusterName:     params.BKECluster.Name,
        Unique:          true,
        RemoveAfterWait: false,
    },
    Node:      params.Node,
    BKEConfig: params.BKECluster.Name,
    Phase:     params.Phase,  // 这里传入的是 InitControlPlane
}

if err := bootstrapCommand.New(); err != nil {
    // 错误处理
}
```

### 4. **Label 设置逻辑**
在 [bootstrap.go](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\pkg\command\bootstrap.go#L55-L64) 中：
```go
customLabel := ""
switch b.Phase {
case bkev1beta1.InitControlPlane:
    customLabel = MasterInitCommandLabel  // "bke.bocloud.com/master-init-command"
case bkev1beta1.JoinControlPlane:
    customLabel = MasterJoinCommandLabel
case bkev1beta1.JoinWorker:
    customLabel = WorkerJoinCommandLabel
}
```

### 5. **Command 内容**
创建的 Command 包含两个执行步骤：
```go
commandSpec.Commands = []agentv1beta1.ExecCommand{
    {
        ID: "check container runtime",
        Command: []string{
            "K8sEnvInit",
            "init=false",
            "check=true",
            "scope=runtime",
            bkeConfig,
        },
    },
    {
        ID: "bootstrap",
        Command: []string{
            "Kubeadm",
            phase,  // "phase=InitControlPlane"
            bkeConfig,
        },
    },
}
```

## 总结
**MasterInit Command 的触发条件：**
1. **BKEMachine 首次协调**（Status.Bootstrapped == false）
2. **Phase 为 InitControlPlane**（表示这是第一个 Master 节点）
3. **集群完全控制**（FullyControlled == true）
4. **找到可用的节点**

**职责分离：**
- **BKEMachine Controller**：负责创建 MasterInit Command
- **EnsureMasterInit Phase**：负责监控和等待 Command 执行完成
- **BKEAgent**：负责在目标节点上执行 Command

# MasterInit Command 
包含两个主要执行步骤**，具体功能如下：

## MasterInit Command 执行流程

### 步骤 1: K8sEnvInit（环境检查）

**命令参数**：
```go
{
    ID: "check container runtime",
    Command: []string{
        "K8sEnvInit",
        "init=false",      // 不执行初始化
        "check=true",      // 只执行检查
        "scope=runtime",   // 检查容器运行时
        "bkeConfig=ns:name",
    },
}
```
**功能说明**：
- **检查容器运行时状态**（Containerd 或 Docker）
- 验证容器运行时是否正常运行
- 确保节点环境满足 Kubernetes 运行要求

**代码位置**：[env.go](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\pkg\job\builtin\kubeadm\env\env.go#L202-L255)

### 步骤 2: Kubeadm InitControlPlane（控制平面初始化）
**命令参数**：
```go
{
    ID: "bootstrap",
    Command: []string{
        "Kubeadm",
        "phase=InitControlPlane",
        "bkeConfig=ns:name",
    },
}
```
**功能说明**：在 [kubeadm.go](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\pkg\job\builtin\kubeadm\kubeadm.go#L167-L196) 中实现，包含以下子步骤：

#### 2.1 安装 kubectl 命令行工具
```go
k.installKubectlCommand()
```
- 下载 kubectl 二进制文件到 `/usr/bin/kubectl`
- 设置执行权限 `chmod 755`

#### 2.2 初始化控制平面证书
```go
k.initControlPlaneCertCommand()
```
- 从管理集群 Secret 加载 CA 证书
- 生成以下证书：
  - **API Server 证书**
  - **Etcd 证书**（CA、Server、Peer、Healthcheck）
  - **Kubelet 证书**
  - **Front Proxy 证书**
  - **Service Account 密钥**
- 生成 kubeconfig 文件：
  - `/etc/kubernetes/admin.conf`
  - `/etc/kubernetes/controller-manager.conf`
  - `/etc/kubernetes/scheduler.conf`
  - `/etc/kubernetes/kubelet.conf`

#### 2.3 生成静态 Pod YAML
```go
k.initControlPlaneManifestCommand()
```
- 渲染并生成以下静态 Pod Manifest：
  - **kube-apiserver.yaml**
  - **kube-controller-manager.yaml**
  - **kube-scheduler.yaml**
  - **etcd.yaml**
- 创建 etcd 用户和目录
- 配置 Pod 挂载卷和启动参数

#### 2.4 安装并启动 kubelet
```go
k.installKubeletCommand()
```
- 下载 kubelet 二进制文件
- 生成 kubelet systemd service 文件
- 生成 kubelet 配置文件 `/var/lib/kubelet/config.yaml`
- 启动 kubelet 服务：
  ```bash
  systemctl enable kubelet
  systemctl restart kubelet
  ```

#### 2.5 上传 kubelet 配置到管理集群
```go
k.uploadTargetClusterKubeletConfig()
```
- 创建 ConfigMap 存储集群配置
- 上传 kubeadm 配置到管理集群
- 供其他节点加入集群时使用

#### 2.6 上传全局 CA（仅管理集群）
```go
k.uploadUserCustomConfigAndGlobalCA()
```
- 如果是管理集群，上传全局 CA 证书
- 上传用户自定义配置

## 执行流程图
```
MasterInit Command 执行流程
│
├─ Step 1: K8sEnvInit (check container runtime)
│   └─ 检查容器运行时状态
│
└─ Step 2: Kubeadm InitControlPlane
    │
    ├─ 2.1 installKubectlCommand
    │   └─ 下载并安装 kubectl
    │
    ├─ 2.2 initControlPlaneCertCommand
    │   ├─ 加载 CA 证书
    │   ├─ 生成 TLS 证书
    │   └─ 生成 kubeconfig
    │
    ├─ 2.3 initControlPlaneManifestCommand
    │   ├─ 生成 kube-apiserver.yaml
    │   ├─ 生成 kube-controller-manager.yaml
    │   ├─ 生成 kube-scheduler.yaml
    │   ├─ 生成 etcd.yaml
    │   └─ 创建 etcd 用户/目录
    │
    ├─ 2.4 installKubeletCommand
    │   ├─ 下载 kubelet
    │   ├─ 生成 service 文件
    │   ├─ 生成配置文件
    │   └─ 启动 kubelet
    │
    ├─ 2.5 uploadTargetClusterKubeletConfig
    │   └─ 上传配置到管理集群
    │
    └─ 2.6 uploadUserCustomConfigAndGlobalCA (可选)
        └─ 上传全局 CA
```
## 关键特性
1. **不依赖 kubeadm 二进制**：自己实现了证书管理、配置生成等逻辑
2. **与 Cluster API 集成**：证书和配置存储在 Kubernetes CRD 中
3. **使用 bke-manifests 模板**：静态 Pod YAML 从模板渲染
4. **静态 Pod 方式**：控制平面组件以静态 Pod 形式运行，由 kubelet 管理

## 执行结果
执行成功后，第一个 Master 节点将：
- 拥有完整的控制平面组件（API Server、Controller Manager、Scheduler、Etcd）
- Kubelet 正常运行并管理静态 Pod
- 集群配置已上传到管理集群
- 可以通过 kubectl 访问集群

#  BKEMachine.Status.Bootstrapped == true 是由哪个功能设置的？

BKEMachine.Status.Bootstrapped = true 是由 BKEMachine Controller 在检测到 Bootstrap Command 执行成功后设置的

## 设置流程

### 完整调用链

```
BKEMachineReconciler.Reconcile()
  ↓
reconcileCommand()                                    // 监控 Command 状态
  ↓
processCommand()                                      // 处理单个 Command
  ↓
processBootstrapCommand()                             // 处理 Bootstrap Command
  ↓
processBootstrapSuccess()                             // 处理 Bootstrap 成功
  ↓
markBKEMachineBootstrapReady()                        // 标记 BKEMachine 为已引导
  ↓
bkeMachine.Status.Bootstrapped = true                 // 设置 Bootstrapped 标志
```

### 关键代码位置

#### 1. 入口：reconcileCommand
在 [bkemachine_controller_phases.go:480](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L480) 中：
```go
func (r *BKEMachineReconciler) reconcileCommand(params BootstrapReconcileParams) (ctrl.Result, error) {
    // 获取 BKEMachine 关联的所有 Command
    commands, err := getBKEMachineAssociateCommands(params.Ctx, r.Client, params.BKECluster, params.BKEMachine)
    
    // 处理每个 Command
    for _, cmd := range commands {
        res, errs = r.processCommand(commandParams)
    }
}
```

#### 2. 处理 Bootstrap Command
在 [bkemachine_controller_phases.go:620](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L620) 中：

```go
func (r *BKEMachineReconciler) processBootstrapCommand(params ProcessBootstrapCommandParams) (ctrl.Result, []error) {
    // 如果已经 Bootstrapped，直接返回
    if params.BKEMachine.Status.Bootstrapped {
        return params.Res, params.Errs
    }
    
    // 检查 Command 执行状态
    complete, successNodes, failedNodes := command.CheckCommandStatus(&params.Cmd)
    
    // 如果有失败的节点
    if params.Complete && len(params.FailedNodes) > 0 {
        return r.processBootstrapFailure(failureParams)
    }
    
    // 如果成功（关键判断条件）
    if params.Complete && len(params.FailedNodes) == 0 && len(params.SuccessNodes) == 1 {
        return r.processBootstrapSuccess(successParams)
    }
}
```

#### 3. 处理成功情况
在 [bkemachine_controller_phases.go:792](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L792) 中：
```go
func (r *BKEMachineReconciler) processBootstrapSuccess(params ProcessBootstrapSuccessParams) (ctrl.Result, []error) {
    // 尝试连接到目标集群节点
    err := r.connectToTargetClusterNode(params)
    if err != nil {
        return r.handleBootstrapSuccessFailure(params, err)
    }
    
    // 生成 ProviderID
    providerID := phaseutil.GenerateProviderID(params.BKECluster, params.CurrentNode)
    
    // 标记 BKEMachine 为已引导（这里设置 Bootstrapped = true）
    if err := r.markBKEMachineBootstrapReady(params.Ctx, params.BKECluster, params.BKEMachine, 
        params.CurrentNode, providerID, params.Log); err != nil {
        params.Errs = append(params.Errs, err)
    }
    
    // 记录指标
    metricrecord.NodeBootstrapSuccessCountRecord(params.BKECluster)
    metricrecord.NodeBootstrapDurationRecord(params.BKECluster, params.CurrentNode, 
        params.Cmd.CreationTimestamp.Time, "success")
}
```

#### 4. 设置 Bootstrapped 标志
在 [bkemachine_controller_phases.go:1258](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L1258) 中：
```go
func (r *BKEMachineReconciler) markBKEMachineBootstrapReady(ctx context.Context, bkeCluster *bkev1beta1.BKECluster,
    bkeMachine *bkev1beta1.BKEMachine, assocNode confv1beta1.Node, providerID string,
    log *zap.SugaredLogger) error {
    
    // 设置 MachineAddress
    setMachineAddress(bkeMachine, assocNode)
    
    // 设置 ProviderID
    setProviderID(bkeMachine, providerID)
    
    // 设置 Ready 和 Bootstrapped 标志
    bkeMachine.Status.Ready = true
    bkeMachine.Status.Bootstrapped = true  // ← 这里设置为 true
    
    // 标记 BootstrapSucceededCondition 为 True
    conditions.MarkTrue(bkeMachine, bkev1beta1.BootstrapSucceededCondition)
    
    // 记录日志和事件
    r.logInfoAndEvent(log, bkeCluster, constant.TargetClusterBootingReason,
        "node %q, role %v bootstrap succeeded", phaseutil.NodeInfo(assocNode), assocNode.Role)
    
    // 标记节点状态标志
    if err := r.NodeFetcher.MarkNodeStateFlagForCluster(ctx, bkeCluster, assocNode.IP, bkev1beta1.NodeBootFlag); err != nil {
        log.Warnf("Failed to mark node state flag: %v", err)
    }
    
    // 设置节点状态为 NotReady（等待后续就绪）
    if err := r.NodeFetcher.SetNodeStateWithMessageForCluster(ctx, bkeCluster, assocNode.IP, 
        bkev1beta1.NodeNotReady, "Bootstrap Succeeded"); err != nil {
        log.Warnf("Failed to set node state: %v", err)
    }
    
    // 同步状态到 API Server
    if err := mergecluster.SyncStatusUntilComplete(r.Client, bkeCluster); err != nil {
        log.Errorf("failed to update bkeCluster Status, err: %s", err.Error())
        return errors.Errorf("failed to update bkeCluster Status, err: %s", err.Error())
    }
    
    return nil
}
```
## 触发条件
**BKEMachine.Status.Bootstrapped = true** 的触发条件：
1. **Command 执行完成**：`params.Complete == true`
2. **没有失败的节点**：`len(params.FailedNodes) == 0`
3. **有成功的节点**：`len(params.SuccessNodes) == 1`
4. **目标集群节点可连接**：`connectToTargetClusterNode()` 成功

## 执行流程图
```
Bootstrap Command 执行流程
│
├─ BKEAgent 执行 Command
│   ├─ K8sEnvInit (环境检查)
│   └─ Kubeadm InitControlPlane (控制平面初始化)
│
├─ Command 执行完成
│   └─ Command.Status.Complete = true
│       └─ Command.Status.SuccessNodes = [nodeIP]
│
└─ BKEMachine Controller 监控到 Command 状态变化
    │
    ├─ reconcileCommand()
    │   └─ 获取 Command 状态
    │
    ├─ processBootstrapCommand()
    │   ├─ 检查 Complete == true
    │   ├─ 检查 FailedNodes == 0
    │   └─ 检查 SuccessNodes == 1
    │
    ├─ processBootstrapSuccess()
    │   ├─ 连接目标集群节点
    │   └─ markBKEMachineBootstrapReady()
    │       │
    │       ├─ 设置 MachineAddress
    │       ├─ 设置 ProviderID
    │       ├─ bkeMachine.Status.Ready = true
    │       ├─ bkeMachine.Status.Bootstrapped = true ← 关键步骤
    │       ├─ conditions.MarkTrue(BootstrapSucceededCondition)
    │       ├─ 标记 NodeBootFlag
    │       ├─ 设置节点状态为 NodeNotReady
    │       └─ 同步状态到 API Server
    │
    └─ 记录成功指标
```

## 总结
**BKEMachine.Status.Bootstrapped = true** 是由 **BKEMachine Controller** 在以下情况下设置的：
1. **监控到 Bootstrap Command 执行成功**（Command.Status.Complete=true 且 SuccessNodes 不为空）
2. **成功连接到目标集群节点**（验证节点确实在运行）
3. **在 `markBKEMachineBootstrapReady()` 函数中**完成以下操作：
   - 设置 `bkeMachine.Status.Bootstrapped = true`
   - 设置 `bkeMachine.Status.Ready = true`
   - 标记 `BootstrapSucceededCondition` 为 True
   - 记录节点状态和指标

这个标志表示节点已经成功完成 Bootstrap 流程，可以继续后续的集群加入操作。

# 触发BKEMachine Controller执行的场景有哪些?​

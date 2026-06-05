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

在 [bkemachine_controller_phases.go](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go) 中：
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
在 [handleFirstTimeReconciliation](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L207-L314) 函数中：
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
在 [handleRealBootstrap](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L418-L468) 函数中：
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
在 [bootstrap.go](file:///d:/code/github/\cluster-api-provider-bke\pkg\command\bootstrap.go#L55-L64) 中：
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

**代码位置**：[env.go](file:///d:/code/github/\cluster-api-provider-bke\pkg\job\builtin\kubeadm\env\env.go#L202-L255)

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
**功能说明**：在 [kubeadm.go](file:///d:/code/github/\cluster-api-provider-bke\pkg\job\builtin\kubeadm\kubeadm.go#L167-L196) 中实现，包含以下子步骤：

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
在 [bkemachine_controller_phases.go:480](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L480) 中：
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
在 [bkemachine_controller_phases.go:620](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L620) 中：

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
在 [bkemachine_controller_phases.go:792](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L792) 中：
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
在 [bkemachine_controller_phases.go:1258](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L1258) 中：
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

## 触发场景详解

### 1. BKEMachine 自身变化（主要触发源）

**代码位置**：[bkemachine_controller.go:548](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L548)

```go
For(&bkev1beta1.BKEMachine{})
```
**触发场景**：
- **创建 BKEMachine**：新的 BKEMachine CR 被创建
- **更新 BKEMachine**：BKEMachine 的 Spec 或 Status 发生变化
- **删除 BKEMachine**：BKEMachine 被删除（触发清理逻辑）

**典型场景**：
- CAPI Machine Controller 创建关联的 BKEMachine
- BKEMachine Status 更新（如 Bootstrapped 状态变化）
- 节点删除时触发 BKEMachine 删除

### 2. Command 状态更新（Bootstrap 完成触发）

**代码位置**：[bkemachine_controller.go:550-554](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L550-L554)
```go
Watches(
    &agentv1beta1.Command{},
    handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &bkev1beta1.BKEMachine{}, handler.OnlyControllerOwner()),
    builder.WithPredicates(predicates.CommandUpdateCompleted()),
)
```

**Predicate 逻辑**：[command.go:22-48](file:///d:/code/github/\cluster-api-provider-bke\utils\capbke\predicates\command.go#L22-L48)
```go
func CommandUpdateCompleted() predicate.Funcs {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            // 检查 Command 是否所有节点都执行完成
            switch {
            case len(newCommand.Status) < len(oldCommand.Status):
                return false
            case newCommand.Spec.NodeName != "" && len(newCommand.Status) != 1:
                return false
            default:
                return true  // Command 执行完成，触发 Reconcile
            }
        },
        CreateFunc:  func(event.CreateEvent) bool { return false },
        DeleteFunc:  func(event.DeleteEvent) bool { return false },
    }
}
```
**触发场景**：
- **Bootstrap Command 执行完成**：所有节点的 Command 都执行完毕（Status 中包含所有节点的执行结果）
- **Reset Command 执行完成**：节点重置命令执行完毕

**典型场景**：
- MasterInit Command 执行完成 → 触发 BKEMachine Reconcile → 设置 Bootstrapped=true
- MasterJoin Command 执行完成 → 触发 BKEMachine Reconcile
- WorkerJoin Command 执行完成 → 触发 BKEMachine Reconcile

### 3. CAPI Machine 变化（上游触发）

**代码位置**：[bkemachine_controller.go:555-558](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L555-L558)
```go
Watches(
    &clusterv1.Machine{},
    handler.EnqueueRequestsFromMapFunc(util.MachineToInfrastructureMapFunc(bkev1beta1.GroupVersion.WithKind("BKEMachine"))),
)
```
**触发场景**：
- **Machine 创建**：CAPI Machine 被创建，触发关联的 BKEMachine Reconcile
- **Machine 更新**：Machine Spec 变化（如版本升级）
- **Machine 删除**：Machine 被删除，触发 BKEMachine 清理

**典型场景**：
- KubeadmControlPlane Controller 创建新的 Machine → 触发 BKEMachine 创建
- Machine Deployment 扩容 → 创建新 Machine → 触发 BKEMachine 创建
- 集群升级时 Machine 版本变更 → 触发 BKEMachine 更新

### 4. CAPI Cluster 变化（集群状态触发）

**代码位置**：[bkemachine_controller.go:559-563](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L559-L563)
```go
Watches(
    &clusterv1.Cluster{},
    handler.EnqueueRequestsFromMapFunc(clusterToBKEMachines),
    builder.WithPredicates(predicates.ClusterUnPause()),
)
```

**Predicate 逻辑**：[cluster.go:23-47](file:///d:/code/github/\cluster-api-provider-bke\utils\capbke\predicates\cluster.go#L23-L47)
```go
func ClusterUnPause() predicate.Funcs {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj := e.ObjectNew.(*clusterv1.Cluster)
            return !newObj.Spec.Paused  // 集群未暂停时触发
        },
        CreateFunc: func(e event.CreateEvent) bool {
            obj := e.Object.(*clusterv1.Cluster)
            return !obj.Spec.Paused
        },
    }
}
```
**触发场景**：
- **Cluster 创建**：CAPI Cluster 被创建且未暂停
- **Cluster 更新**：Cluster 状态变化且未暂停
- **Cluster 暂停状态变更**：从暂停变为非暂停

**典型场景**：
- 集群创建时 Cluster 创建 → 触发所有关联的 BKEMachine Reconcile
- 集群恢复（从暂停状态恢复）→ 触发 BKEMachine Reconcile

### 5. BKECluster 变化（基础设施状态触发）

**代码位置**：[bkemachine_controller.go:564-568](file:///d:/code/github/\cluster-api-provider-bke\controllers\capbke\bkemachine_controller.go#L564-L568)
```go
Watches(
    &bkev1beta1.BKECluster{},
    handler.EnqueueRequestsFromMapFunc(r.BKEClusterToBKEMachines),
    builder.WithPredicates(predicates.BKEAgentReady(), predicates.BKEClusterUnPause()),
)
```

**Predicate 逻辑**：[bkecluster.go:27-46](file:///d:/code/github/\cluster-api-provider-bke\utils\capbke\predicates\bkecluster.go#L27-L46)
```go
func BKEAgentReady() predicate.Funcs {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj := e.ObjectNew.(*bkev1beta1.BKECluster)
            // Agent 就绪或需要重新引导
            agent := condition.HasConditionStatus(BKEAgentCondition, newObj, ConditionTrue) && 
                     condition.HasConditionStatus(NodesEnvCondition, newObj, ConditionTrue)
            requeue := condition.HasConditionStatus(TargetClusterBootCondition, newObj, ConditionFalse)
            return agent || requeue
        },
    }
}

func BKEClusterUnPause() predicate.Funcs {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj := e.ObjectNew.(*bkev1beta1.BKECluster)
            return !newObj.Spec.Pause
        },
        CreateFunc: func(e event.CreateEvent) bool {
            obj := e.Object.(*bkev1beta1.BKECluster)
            return !obj.Spec.Pause
        },
        DeleteFunc: func(event.DeleteEvent) bool { return true },
    }
}
```
**触发场景**：
- **BKECluster 创建**：BKECluster 被创建且未暂停
- **BKEAgent 就绪**：BKEAgent 部署完成且节点环境初始化完成
- **Bootstrap 失败重试**：TargetClusterBootCondition=False
- **BKECluster 删除**：触发所有 BKEMachine 清理

**典型场景**：
- EnsureBKEAgent Phase 完成 → BKEAgentCondition=True → 触发 BKEMachine Reconcile
- EnsureNodesEnv Phase 完成 → NodesEnvCondition=True → 触发 BKEMachine Reconcile
- Bootstrap 失败 → TargetClusterBootCondition=False → 触发重试

## 触发流程图
```
触发 BKEMachine Controller 执行的场景
│
├─ 1. BKEMachine 自身变化
│   ├─ 创建 BKEMachine
│   ├─ 更新 BKEMachine (Spec/Status)
│   └─ 删除 BKEMachine
│
├─ 2. Command 状态更新
│   ├─ Bootstrap Command 执行完成
│   │   ├─ MasterInit Command 完成
│   │   ├─ MasterJoin Command 完成
│   │   └─ WorkerJoin Command 完成
│   └─ Reset Command 执行完成
│
├─ 3. CAPI Machine 变化
│   ├─ Machine 创建
│   ├─ Machine 更新 (版本升级)
│   └─ Machine 删除
│
├─ 4. CAPI Cluster 变化
│   ├─ Cluster 创建 (未暂停)
│   ├─ Cluster 更新 (未暂停)
│   └─ Cluster 恢复 (从暂停恢复)
│
└─ 5. BKECluster 变化
    ├─ BKECluster 创建 (未暂停)
    ├─ BKEAgent 就绪
    │   ├─ BKEAgentCondition=True
    │   └─ NodesEnvCondition=True
    ├─ Bootstrap 失败重试
    │   └─ TargetClusterBootCondition=False
    └─ BKECluster 删除
```

## 典型执行场景示例

### 场景 1：集群初始化
```
1. CAPI Cluster 创建 → 触发 BKEMachine Reconcile
2. CAPI Machine 创建 → 触发 BKEMachine Reconcile
3. BKECluster 创建 → 触发 BKEMachine Reconcile
4. BKEAgent 部署完成 → BKEAgentCondition=True → 触发 BKEMachine Reconcile
5. 节点环境初始化完成 → NodesEnvCondition=True → 触发 BKEMachine Reconcile
6. BKEMachine 创建 Bootstrap Command
7. Command 执行完成 → 触发 BKEMachine Reconcile
8. 设置 Bootstrapped=true
```

### 场景 2：节点扩容
```
1. Machine Deployment 扩容 → 创建新 Machine
2. Machine 创建 → 触发 BKEMachine Reconcile
3. BKEMachine 创建 → 触发 Bootstrap 流程
```

### 场景 3：节点删除
```
1. Machine 删除 → 触发 BKEMachine Reconcile
2. BKEMachine 删除 → 触发 Reset Command
3. Reset Command 完成 → 触发 BKEMachine Reconcile
4. 清理节点资源
```

## 总结
**BKEMachine Controller 的触发场景**主要分为 5 类：
1. **BKEMachine 自身变化**：直接的生命周期事件
2. **Command 状态更新**：Bootstrap/Reset 完成的关键触发
3. **CAPI Machine 变化**：上游 CAPI 的触发
4. **CAPI Cluster 变化**：集群级别的触发
5. **BKECluster 变化**：基础设施状态的触发

其中，**Command 状态更新**是最关键的触发场景，它标志着节点 Bootstrap 流程的完成，是设置 `Bootstrapped=true` 的直接触发源。

# Bootstrap Command是在哪里触发执行的？

分为两个阶段：**管理集群创建 Command** 和 **节点侧执行 Command**。

## 完整执行触发流程

### 阶段 1：管理集群创建 Command

#### 1.1 触发入口
**代码位置**：[bkemachine_controller_phases.go:418-476](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go#L418-L476)
```go
func (r *BKEMachineReconciler) handleRealBootstrap(params RealBootstrapParams) (ctrl.Result, error) {
    // 创建 Bootstrap Command
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
        Node:      params.Node,      // 目标节点信息
        BKEConfig: params.BKECluster.Name,
        Phase:     params.Phase,     // InitControlPlane/JoinControlPlane/JoinWorker
    }

    // 创建 Command CR
    if err := bootstrapCommand.New(); err != nil {
        // 错误处理
    }
    
    // 设置 BKEMachine Label，标记节点已使用
    labelhelper.SetBKEMachineLabel(params.BKEMachine, params.Role, params.Node.IP)
    
    return ctrl.Result{}, nil
}
```

#### 1.2 Command 创建逻辑
**代码位置**：[bootstrap.go:45-101](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\pkg\command\bootstrap.go#L45-L101)
```go
func (b *Bootstrap) New() error {
    commandName := fmt.Sprintf("%s%s-%d", BootstrapCommandNamePrefix, b.Node.IP, time.Now().Unix())
    
    // 根据 Phase 设置不同的 Label
    customLabel := ""
    switch b.Phase {
    case bkev1beta1.InitControlPlane:
        customLabel = MasterInitCommandLabel
    case bkev1beta1.JoinControlPlane:
        customLabel = MasterJoinCommandLabel
    case bkev1beta1.JoinWorker:
        customLabel = WorkerJoinCommandLabel
    }
    
    // 设置 Command Spec
    commandSpec := GenerateDefaultCommandSpec()
    commandSpec.Commands = []agentv1beta1.ExecCommand{
        {
            ID: "check container runtime",
            Command: []string{"K8sEnvInit", "init=false", "check=true", "scope=runtime", bkeConfig},
            Type: agentv1beta1.CommandBuiltIn,
        },
        {
            ID: "bootstrap",
            Command: []string{"Kubeadm", phase, bkeConfig},
            Type: agentv1beta1.CommandBuiltIn,
        },
    }
    
    // 设置 NodeSelector，指定执行节点
    commandSpec.NodeSelector = getNodeSelector(nodes)
    
    // 创建 Command CR
    return b.newCommand(commandName, BKEMachineLabel, commandSpec, customLabel)
}
```
**Command 创建结果**：
- Command CR 被创建到管理集群 API Server
- Command.Spec.NodeSelector 指向目标节点
- Command 包含两个执行步骤：K8sEnvInit 和 Kubeadm

### 阶段 2：节点侧执行 Command

#### 2.1 BKEAgent 启动
**代码位置**：[main.go:133-144](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\cmd\bkeagent\main.go#L133-L144)
```go
func setupController(mgr ctrl.Manager, j job.Job, ctx context.Context) error {
    hostName := utils.HostName()
    log.Infof("BKEAgent node hostName: %s", hostName)
    
    // 注册 CommandReconciler
    return (&bkeagentctrl.CommandReconciler{
        Client:    mgr.GetClient(),
        APIReader: mgr.GetAPIReader(),
        Scheme:    mgr.GetScheme(),
        Job:       j,           // Job 执行引擎
        NodeName:  hostName,    // 当前节点名称
        Ctx:       ctx,
    }).SetupWithManager(mgr)
}
```

#### 2.2 Watch Command CRD
**代码位置**：[command_controller.go:342-352](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\bkeagent\command_controller.go#L342-L352)
```go
func (r *CommandReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&agentv1beta1.Command{}, r.commandPredicateFn()).  // Watch Command CR
        Complete(r)
}
```

#### 2.3 过滤匹配的 Command
**代码位置**：[command_controller.go:354-387](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\bkeagent\command_controller.go#L354-L387)
```go
func (r *CommandReconciler) shouldReconcileCommand(o *agentv1beta1.Command, eventType string) bool {
    // 检查是否是当前节点应该执行的 Command
    if o.Spec.NodeName == r.NodeName {
        return true  // NodeName 匹配
    }
    return r.nodeMatchNodeSelector(o.Spec.NodeSelector)  // NodeSelector 匹配
}

func (r *CommandReconciler) commandPredicateFn() builder.Predicates {
    return builder.WithPredicates(
        predicate.Funcs{
            CreateFunc: func(e event.CreateEvent) bool {
                cmd := e.Object.(*agentv1beta1.Command)
                return r.shouldReconcileCommand(cmd, "CreateFunc")
            },
            UpdateFunc: func(e event.UpdateEvent) bool {
                cmd := e.ObjectNew.(*agentv1beta1.Command)
                return r.shouldReconcileCommand(cmd, "UpdateFunc")
            },
        },
    )
}
```
**过滤逻辑**：
- **NodeName 匹配**：Command.Spec.NodeName == 当前节点名称
- **NodeSelector 匹配**：当前节点标签匹配 Command.Spec.NodeSelector

#### 2.4 触发 Reconcile
**代码位置**：[command_controller.go:98-143](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\bkeagent\command_controller.go#L98-L143)
```go
func (r *CommandReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 获取 Command 对象
    command, res := r.fetchCommand(ctx, req)
    
    // 2. 初始化 Status
    if res := r.ensureStatusInitialized(command); res.done {
        return res.result, res.err
    }
    
    // 3. 处理 Finalizer
    if res := r.handleFinalizer(ctx, command, gid); res.done {
        return res.result, res.err
    }
    
    // 4. 跳过已完成的命令
    if currentStatus.Phase == agentv1beta1.CommandComplete {
        return ctrl.Result{}, nil
    }
    
    // 5. 创建并启动任务
    return r.createAndStartTask(ctx, command, currentStatus, gid).unwrap()
}
```

#### 2.5 启动异步任务执行
**代码位置**：[command_controller.go:320-341](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\bkeagent\command_controller.go#L320-L341)
```go
func (r *CommandReconciler) createAndStartTask(ctx context.Context, command *agentv1beta1.Command,
    currentStatus *agentv1beta1.CommandStatus, gid string) reconcileResult {
    
    // 创建 Task 对象
    r.Job.Task[gid] = &job.Task{
        StopChan:                make(chan struct{}),
        Phase:                   agentv1beta1.CommandRunning,
        ResourceVersion:         command.ResourceVersion,
        Generation:              command.GetGeneration(),
    }
    
    // 启动异步任务
    go r.startTask(ctx, r.Job.Task[gid].StopChan, command)
    
    return finishReconcile(ctrl.Result{}, nil)
}
```

#### 2.6 执行 Command
**代码位置**：[command_controller.go:543-583](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\bkeagent\command_controller.go#L543-L583)
```go
func (r *CommandReconciler) startTask(ctx context.Context, stopChan chan struct{}, command *agentv1beta1.Command) {
    gid := fmt.Sprintf("%s/%s", command.Namespace, command.Name)
    currentStatus := command.Status[r.commandStatusKey()]
    
    // 顺序执行所有命令
    for _, execCommand := range command.Spec.Commands {
        // 检查停止信号
        select {
        case <-stopChan:
            return
        default:
        }
        
        // 检查是否已完成
        if isCommandCompleted(currentStatus.Conditions, execCommand.ID) {
            continue
        }
        
        // 处理单个命令
        result := r.processExecCommand(command, execCommand, currentStatus, stopTime)
        if result.shouldBreak {
            break
        }
    }
    
    // 更新最终状态
    r.finalizeTaskStatus(command, currentStatus, gid)
}
```

#### 2.7 路由到具体执行器
**代码位置**：[command_controller.go:465-479](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\controllers\bkeagent\command_controller.go#L465-L479)
```go
func (r *CommandReconciler) executeByType(cmdType agentv1beta1.CommandType, command []string) ([]string, error) {
    switch cmdType {
    case agentv1beta1.CommandBuiltIn:
        return r.Job.BuiltIn.Execute(command)  // 内置插件执行
    case agentv1beta1.CommandKubernetes:
        return r.Job.K8s.Execute(command)      // K8s API 操作
    case agentv1beta1.CommandShell:
        return r.Job.Shell.Execute(command)    // Shell 命令执行
    }
}
```
**对于 Bootstrap Command**：
- 第一个命令：`K8sEnvInit` → BuiltIn 插件执行
- 第二个命令：`Kubeadm` → BuiltIn 插件执行

## 执行流程图

```
Bootstrap Command 执行触发流程
│
├─ 阶段 1: 管理集群创建 Command
│   │
│   ├─ BKEMachine Controller Reconcile
│   │   ├─ reconcileBootstrap()
│   │   ├─ handleFirstTimeReconciliation()
│   │   └─ handleRealBootstrap()
│   │
│   ├─ 创建 Bootstrap Command
│   │   ├─ 设置 Command.Spec.Commands
│   │   │   ├─ ["K8sEnvInit", "init=false", "check=true", "scope=runtime"]
│   │   │   └─ ["Kubeadm", "phase=InitControlPlane"]
│   │   ├─ 设置 Command.Spec.NodeSelector (指向目标节点)
│   │   └─ 设置 Label (MasterInitCommandLabel)
│   │
│   └─ Command CR 创建到 API Server
│
├─ 阶段 2: 节点侧执行 Command
│   │
│   ├─ BKEAgent 启动
│   │   ├─ 获取节点 HostName
│   │   └─ 注册 CommandReconciler
│   │
│   ├─ Watch Command CRD
│   │   └─ 监听 Command 的 Create/Update 事件
│   │
│   ├─ 过滤匹配的 Command
│   │   ├─ 检查 NodeName 匹配
│   │   └─ 检查 NodeSelector 匹配
│   │
│   ├─ 触发 Reconcile
│   │   ├─ 获取 Command 对象
│   │   ├─ 初始化 Status
│   │   └─ 创建 Task
│   │
│   ├─ 启动异步任务
│   │   └─ go startTask()
│   │
│   ├─ 顺序执行命令
│   │   ├─ 执行 K8sEnvInit
│   │   │   └─ BuiltIn.Execute(["K8sEnvInit", ...])
│   │   │       └─ EnvPlugin.Execute()
│   │   │           └─ 检查容器运行时
│   │   │
│   │   └─ 执行 Kubeadm
│   │       └─ BuiltIn.Execute(["Kubeadm", "phase=InitControlPlane", ...])
│   │           └─ KubeadmPlugin.Execute()
│   │               ├─ installKubectlCommand()
│   │               ├─ initControlPlaneCertCommand()
│   │               ├─ initControlPlaneManifestCommand()
│   │               ├─ installKubeletCommand()
│   │               └─ uploadTargetClusterKubeletConfig()
│   │
│   └─ 更新 Command Status
│       ├─ 设置 Phase=Complete
│       ├─ 记录执行结果
│       └─ 同步到 API Server
│
└─ BKEMachine Controller 监控到 Command 完成
    └─ 设置 Bootstrapped=true
```

## 关键触发点总结

### 1. **管理集群侧触发**
- **BKEMachine Controller** 在 `handleRealBootstrap()` 中创建 Bootstrap Command
- **触发条件**：
  - BKEMachine 首次协调（Status.Bootstrapped=false）
  - 集群完全控制（FullyControlled=true）
  - 找到可用节点

### 2. **节点侧触发**
- **BKEAgent** 通过 Watch 机制监听 Command CRD
- **过滤条件**：
  - NodeName 匹配当前节点
  - NodeSelector 匹配当前节点标签
- **执行方式**：
  - 异步任务执行
  - 顺序执行多个命令
  - 支持 BuiltIn/Shell/K8s 三种执行器

### 3. **执行完成反馈**
- BKEAgent 更新 Command.Status 为 Complete
- BKEMachine Controller 监控到 Command 完成
- 设置 BKEMachine.Status.Bootstrapped=true

## 总结

**Bootstrap Command 的执行触发**是一个跨集群的协作过程：
1. **管理集群**：BKEMachine Controller 创建 Command CR，声明要执行的命令
2. **目标节点**：BKEAgent Watch 到匹配的 Command，在节点上执行命令
3. **状态反馈**：BKEAgent 更新 Command Status，BKEMachine Controller 监控完成状态

这种**声明式命令执行模型**实现了管理集群与工作节点的解耦，支持灵活的命令调度和执行。


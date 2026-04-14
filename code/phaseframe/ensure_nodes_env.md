
# 详细分析`command.ENV` 如何下发执行
## 分析：节点尚未被 K8s 纳管时，`command.ENV` 如何下发执行
### 核心问题
`EnsureNodesEnv` 阶段通过 `command.ENV` 创建 `Command` CRD 对象来下发环境初始化命令，但此时目标节点还没有加入任何 K8s 集群，BKEAgent 是如何感知并执行这些命令的？
### 答案：BKEAgent 连接的是**管理集群**，而非目标集群
关键在于理解 BKEAgent 的连接目标。整个机制的核心架构如下：
### 1. BKEAgent 推送时已注入管理集群的 KubeConfig
在 **EnsureBKEAgent** 阶段（[ensure_bke_agent.go:508-523](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L508-L523)），通过 SSH 推送 Agent 时，会写入一个**指向管理集群**的 kubeconfig：
```go
// ensure_bke_agent.go:523
fmt.Sprintf("echo -e %q > /etc/openFuyao/bkeagent/config", localKubeConfig)
```
这个 `localKubeConfig` 来自 [ensure_bke_agent.go:119-167](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L119-L167)：
```go
func (e *EnsureBKEAgent) loadLocalKubeConfig() error {
    // 获取的是管理集群的 kubeconfig
    localKubeConfig, err = phaseutil.GetLocalKubeConfig(ctx, c)
    // ...
}
```
**所以 BKEAgent 启动后，连接的是管理集群的 API Server，而不是目标集群。**
### 2. BKEAgent 启动后注册 controller-runtime Manager
在 [cmd/bkeagent/main.go](file:///D:/code/github/cluster-api-provider-bke/cmd/bkeagent/main.go) 中，BKEAgent 使用管理集群的 kubeconfig 创建 controller-runtime Manager：
```go
func newManager() (ctrl.Manager, error) {
    return ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
        // GetConfigOrDie() 读取的是 /etc/openFuyao/bkeagent/config 中的管理集群配置
        Scheme:             scheme,
        MetricsBindAddress: "0",
        LeaderElection:     false,
    })
}
```
然后注册 `CommandReconciler`，Watch 管理集群上的 `Command` CRD：
```go
func setupController(mgr ctrl.Manager, j job.Job, ctx context.Context) error {
    return (&bkeagentctrl.CommandReconciler{
        Client:   mgr.GetClient(),
        // ...
        NodeName: hostName,  // 本节点的主机名
    }).SetupWithManager(mgr)
}
```
### 3. Command CRD 创建在管理集群上
`command.ENV.New()` 方法（[env.go:89-109](file:///D:/code/github/cluster-api-provider-bke/pkg/command/env.go#L89-L109)）最终调用 `BaseCommand.newCommand()`（[command.go:195-210](file:///D:/code/github/cluster-api-provider-bke/pkg/command/command.go#L195-L210)），在**管理集群**上创建 `Command` CRD 对象：
```go
func (b *BaseCommand) createCommand(command *agentv1beta1.Command) error {
    // b.Client 是管理集群的 client
    if err := b.Client.Create(b.Ctx, command); err != nil {
        // ...
    }
    return nil
}
```
`Command` 对象的 `NodeSelector` 包含目标节点的 IP 作为 label：
```go
func getNodeSelector(nodes bkenode.Nodes) *metav1.LabelSelector {
    nodeSelector := &metav1.LabelSelector{}
    for _, node := range nodes {
        metav1.AddLabelToSelector(nodeSelector, node.IP, node.IP)
    }
    return nodeSelector
}
```
例如，一个 `k8s-env-init` Command 的 NodeSelector 可能是：
```yaml
nodeSelector:
  matchLabels:
    192.168.1.10: 192.168.1.10
    192.168.1.11: 192.168.1.11
```
### 4. BKEAgent 通过 NodeSelector 匹配自己是否应该执行
`CommandReconciler` 的 Predicate 过滤逻辑（[command_controller.go:625-669](file:///D:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go#L625-L669)）：
```go
func (r *CommandReconciler) shouldReconcileCommand(o *agentv1beta1.Command, eventType string) bool {
    // 检查 Spec.NodeName 是否匹配
    if o.Spec.NodeName == r.NodeName {
        return true
    }
    // 检查 NodeSelector 是否匹配本节点
    return r.nodeMatchNodeSelector(o.Spec.NodeSelector)
}
```
`nodeMatchNodeSelector` 方法（[command_controller.go:711-751](file:///D:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go#L711-L751)）的关键逻辑：
```go
func (r *CommandReconciler) nodeMatchNodeSelector(s *metav1.LabelSelector) bool {
    // 1. 先用主机名匹配
    nodeName, found := selector.RequiresExactMatch(r.NodeName)
    if nodeName == r.NodeName {
        return true
    }
    // 2. 再用本机所有网卡IP匹配
    ips, err := bkenet.GetAllInterfaceIP()
    for _, p := range ips {
        tmpIP, _, _ := net.ParseCIDR(p)
        if ip, found := selector.RequiresExactMatch(tmpIP.String()); found {
            if ip == tmpIP.String() {
                r.NodeIP = ip  // 记录匹配到的IP
                return true
            }
        }
    }
    return false
}
```
**BKEAgent 会获取本机所有网卡 IP，与 Command 的 NodeSelector 中的 IP label 进行匹配。如果本机某个网卡 IP 出现在 NodeSelector 中，就认为这个 Command 是给自己的，触发 Reconcile 执行。**
### 5. 完整数据流图
```
┌─────────────────────────────────────────────────────────────────────┐
│                        管理集群 (Management Cluster)                  │
│                                                                     │
│  ┌──────────────────┐     ┌──────────────────────────────────┐     │
│  │ BKECluster       │     │ Command CRD (k8s-env-init-xxx)   │     │
│  │ Controller       │────>│                                  │     │
│  │                  │     │ spec:                            │     │
│  │ EnsureNodesEnv   │     │   nodeSelector:                  │     │
│  │   phase          │     │     matchLabels:                 │     │
│  │                  │     │       192.168.1.10: 192.168.1.10 │     │
│  │ command.ENV.New()│     │       192.168.1.11: 192.168.1.11 │     │
│  │                  │     │   commands:                      │     │
│  └──────────────────┘     │     - K8sEnvInit (BuiltIn)      │     │
│                           │     - Reset (BuiltIn)            │     │
│                           │     - K8sEnvInit (BuiltIn)      │     │
│                           └──────────┬───────────────────────┘     │
│                                      │ Watch (Informer)             │
│                                      ▼                              │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │              BKEAgent (运行在目标节点上)                        │  │
│  │                                                              │  │
│  │  1. 使用管理集群 kubeconfig 连接管理集群 API Server             │  │
│  │  2. controller-runtime Manager Watch Command CRD             │  │
│  │  3. Predicate: nodeMatchNodeSelector()                       │  │
│  │     → 获取本机所有网卡IP                                       │  │
│  │     → 与 Command.NodeSelector 的 label 匹配                   │  │
│  │     → 匹配成功 → 触发 Reconcile                               │  │
│  │  4. CommandReconciler.Reconcile()                            │  │
│  │     → 解析 ExecCommand                                       │  │
│  │     → 根据 Type 路由到 BuiltIn/Shell/Kubernetes 执行器         │  │
│  │     → 执行 K8sEnvInit、Reset 等内置命令                        │  │
│  │  5. 更新 Command.Status 上报执行结果                           │  │
│  └──────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘

目标节点 (192.168.1.10)          目标节点 (192.168.1.11)
┌─────────────────────┐         ┌─────────────────────┐
│ BKEAgent 进程        │         │ BKEAgent 进程        │
│ ↓ 连接管理集群       │         │ ↓ 连接管理集群       │
│ Watch Command CRD   │         │ Watch Command CRD   │
│ ↓ NodeSelector匹配  │         │ ↓ NodeSelector匹配  │
│ IP=192.168.1.10 ✓   │         │ IP=192.168.1.11 ✓   │
│ ↓ 执行K8sEnvInit    │         │ ↓ 执行K8sEnvInit    │
│ ↓ 上报Status        │         │ ↓ 上报Status        │
└─────────────────────┘         └─────────────────────┘
```
### 6. 总结：为什么节点未被 K8s 纳管也能下发命令
| 关键点 | 说明 |
|--------|------|
| **BKEAgent 连接管理集群** | EnsureBKEAgent 阶段通过 SSH 推送时，注入的是**管理集群的 kubeconfig**，BKEAgent 启动后 Watch 的是管理集群的 API Server |
| **Command CRD 在管理集群创建** | `command.ENV.New()` 通过管理集群的 `client.Client.Create()` 在管理集群上创建 Command 资源 |
| **NodeSelector 基于 IP 匹配** | Command 的 NodeSelector 使用节点 IP 作为 label key/value，BKEAgent 通过获取本机网卡 IP 与之匹配 |
| **无需目标集群存在** | 整个命令下发和执行机制完全依赖管理集群，目标节点上只需要有 BKEAgent 进程在运行即可 |
| **执行结果回写管理集群** | BKEAgent 执行完命令后，将结果更新到 Command.Status（也在管理集群上），BKECluster Controller 通过 `envCmd.Wait()` 轮询管理集群上的 Command 状态来获知执行结果 |

**本质上，BKEAgent 是一个"边车"模式（sidecar-like）的 Agent**：它运行在目标节点上，但通过管理集群的 kubeconfig 与管理集群通信，形成一个**中心化控制 + 分布式执行**的架构。节点不需要被任何 K8s 集群纳管，只需要能访问管理集群的 API Server 即可接收和执行命令。

# EnsureNodesEnv 业务流程梳理
## 一、总体流程概览
```
Execute()
  │
  └── CheckOrInitNodesEnv()
        │
        ├── 1. getNodesToInitEnv()              — 获取需要初始化环境的节点
        │
        ├── 2. setupClusterConditionAndSync()   — 设置集群条件状态
        │
        ├── 3. buildEnvCommand()                — 构建环境初始化 Command
        │     ├── getExtraAndExtraHosts()       — 计算 extra/extraHosts 参数
        │     ├── shouldUseDeepRestore()        — 判断是否深度重置
        │     └── command.ENV.New()             — 创建 Command CRD
        │
        ├── 4. executeEnvCommand()              — 等待 Command 执行完成
        │
        ├── 5. handleSuccessNodes()             — 处理成功节点
        │
        ├── 6. handleFailedNodes()              — 处理失败节点
        │
        └── 7. finalDecisionAndCleanup()        — 最终决策与清理
              │
              ├── initClusterExtra()            — 安装自定义脚本
              │     ├── installCommonScripts()  — 安装基础脚本
              │     └── installOtherCustomScripts() — 安装其他自定义脚本
              │
              └── executeNodePreprocessScripts() — 执行前置处理脚本
                    ├── checkPreprocessConfigExists() — 检查配置是否存在
                    └── createPreprocessCommand()     — 创建前置处理 Command
```
## 二、阶段入口判断：NeedExecute
```go
func (e *EnsureNodesEnv) NeedExecute(old, new *bkev1beta1.BKECluster) bool
```
**判断逻辑**：
1. 调用 `BasePhase.DefaultNeedExecute()` 检查基础条件
2. 通过 `NodeFetcher` 获取集群关联的所有 `BKENode` CRD
3. 调用 `phaseutil.HasNodesNeedingPhase(bkeNodes, NodeEnvFlag)` 检查是否存在**尚未标记 `NodeEnvFlag`** 的节点
4. 如果存在，设置状态为 `PhaseWaiting` 并返回 `true`
## 三、步骤 1：getNodesToInitEnv — 获取需要初始化环境的节点
**代码位置**：[ensure_nodes_env.go:92-120](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L92-L120)

**过滤逻辑**（逐条检查每个 BKENode）：

| 过滤条件 | 说明 | 动作 |
|---------|------|------|
| `NodeFailedFlag ≠ 0` | 节点已失败 | 跳过 |
| `NodeDeletingFlag ≠ 0` | 节点正在删除 | 跳过 |
| `NeedSkip = true` | 节点被标记跳过 | 跳过 |
| `NodeEnvFlag ≠ 0` | 环境已初始化 | 跳过 |
| `NodeAgentReadyFlag = 0` | Agent 未就绪 | 跳过 |
| 以上均不满足 | 需要初始化环境 | 加入列表 |

对通过过滤的节点，设置状态为 `NodeInitializing` + "Initializing node env"。

**关键前置条件**：节点必须已标记 `NodeAgentReadyFlag`（即 EnsureBKEAgent 阶段已完成）。
## 四、步骤 2：setupClusterConditionAndSync — 设置集群条件状态
**代码位置**：[ensure_nodes_env.go:122-129](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L122-L129)
```
1. 设置 BKECluster 条件：NodesEnvCondition = False, NodesEnvNotReadyReason
2. 同步状态到管理集群（SyncStatusUntilComplete）
```
## 五、步骤 3：buildEnvCommand — 构建环境初始化 Command
**代码位置**：[ensure_nodes_env.go:168-198](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L168-L198)
### 5.1 getExtraAndExtraHosts — 计算额外参数
**代码位置**：[ensure_nodes_env.go:206-237](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L206-L237)
```
extra（额外 IP 列表）：
  ├── ControlPlaneEndpoint 是外部 VIP（非节点 IP）？
  │     └── 是 → 添加 VIP IP 到 extra
  └── IngressVIP 存在且 ≠ ControlPlaneEndpoint.Host？
        └── 是 → 添加 IngressVIP 到 extra

extraHosts（额外 hosts 映射）：
  └── ControlPlaneEndpoint 有效？
        ├── HA 集群（VIP）→ master.bocloud.com → VIP
        └── 单 Master    → master.bocloud.com → Master[0].IP
```
**用途**：这些参数传递给 BKEAgent 的 `K8sEnvInit` 内置命令，用于：
- `extra`：配置证书的 SAN（Subject Alternative Name），确保 VIP 和 Ingress IP 包含在 API Server 证书中
- `extraHosts`：写入节点 `/etc/hosts`，将 `master.bocloud.com` 映射到 VIP 或 Master IP
### 5.2 shouldUseDeepRestore — 判断是否深度重置
**代码位置**：[ensure_nodes_env.go:200-203](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L200-L203)
```
检查 BKECluster Annotation: annotation.DeepRestoreNodeAnnotationKey
  ├── 注解值 = "true"  → deepRestore = true
  ├── 注解值 = "false" → deepRestore = false
  └── 注解不存在       → deepRestore = true（默认深度重置）
```
**影响**：决定 `Reset` 命令的 scope 范围：
- `deepRestore = true`：`scope=cert,manifests,container,kubelet,containerRuntime,extra`
- `deepRestore = false`：`scope=cert,manifests,container,kubelet,extra`（不重置 containerRuntime）
### 5.3 command.ENV 创建的 Command 内容
**代码位置**：[env.go:89-109](file:///D:/code/github/cluster-api-provider-bke/pkg/command/env.go#L89-L109)

创建的 Command CRD 包含三条顺序执行的内置命令：
```
Command: k8s-env-init-{timestamp}
NodeSelector: {nodeIP1: nodeIP1, nodeIP2: nodeIP2, ...}
Unique: true（同集群仅保留一个）
RemoveAfterWait: true（执行完自动删除）
WaitTimeout: GetBootTimeOut(bkeCluster)

Commands（顺序执行）：
  ┌──────────────────────────────────────────────────────────────┐
  │ 1. K8sEnvInit (ID: "node hardware resources check")         │
  │    参数: init=true, check=true, scope=node, bkeConfig=ns:name│
  │    功能: 检查节点硬件资源是否满足 K8s 运行要求                  │
  │    重试: 不忽略失败                                            │
  ├──────────────────────────────────────────────────────────────┤
  │ 2. Reset (ID: "reset")                                       │
  │    参数: bkeConfig=ns:name, scope=cert,manifests,container,  │
  │          kubelet[,containerRuntime],extra                     │
  │    功能: 重置节点环境（清理旧配置）                              │
  │    重试: 忽略失败（BackoffIgnore=true）                        │
  ├──────────────────────────────────────────────────────────────┤
  │ 3. K8sEnvInit (ID: "init and check node env")                │
  │    参数: init=true, check=true,                               │
  │          scope=time,hosts,dns,kernel,firewall,selinux,swap,  │
  │                httpRepo,runtime,iptables,registry,extra       │
  │          bkeConfig=ns:name, extraHosts=master.bocloud.com:IP │
  │    功能: 初始化并检查节点环境                                   │
  │    重试: 延迟5秒重试，不忽略失败                                │
  └──────────────────────────────────────────────────────────────┘
```
**额外：预拉取镜像命令**（`PrePullImage = true` 时，仅首次部署）：
```
Command: k8s-image-pre-pull-{timestamp}
NodeSelector: 排除首个 Master 节点
Commands:
  └── K8sEnvInit (ID: "pre pull images")
      参数: init=true, check=true, scope=image, bkeConfig=ns:name
      重试: 延迟15秒，忽略失败
```
## 六、步骤 4：executeEnvCommand — 等待 Command 执行
**代码位置**：[ensure_nodes_env.go:239-242](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L239-L242)
```
1. 调用 envCmd.Wait() 轮询管理集群上的 Command 状态
2. 等待所有目标节点执行完成或超时
3. 返回 (error, successNodes, failedNodes)
```
## 七、步骤 5：handleSuccessNodes — 处理成功节点
**代码位置**：[ensure_nodes_env.go:244-262](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L244-L262)
```
对每个成功节点：
  1. 从 Command WaitResult 中提取节点 IP
  2. 标记 NodeEnvFlag（表示环境初始化完成）
  3. 设置状态消息："Nodes env is ready"
  4. 从 allNodes 中找到该节点，加入 e.nodes 缓存（供后续脚本安装使用）
```
## 八、步骤 6：handleFailedNodes — 处理失败节点
**代码位置**：[ensure_nodes_env.go:264-281](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L264-L281)
```
对每个失败节点：
  1. 设置状态：NodeInitFailed + "Failed to check k8s env"
  2. 调用 SetSkipNodeErrorForWorker()：
     - Worker 节点 → 标记 NeedSkip=true（跳过后续阶段）
     - Master 节点 → 不跳过（后续阶段会重试）
  3. 记录 Command 执行错误日志
  4. 标记节点错误状态到 BKENode
```
## 九、步骤 7：finalDecisionAndCleanup — 最终决策与清理
**代码位置**：[ensure_nodes_env.go:283-316](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L283-L316)
```
1. 同步状态到管理集群
2. 全部节点失败？→ 返回错误
3. 部分成功 → 继续执行：
   ├── initClusterExtra()           — 安装自定义脚本
   └── executeNodePreprocessScripts() — 执行前置处理脚本
4. Deploying 状态下有失败节点？
   └── 检查不可跳过的失败节点数 > 0？→ 返回错误重试
5. 全部通过 → 设置 NodesEnvCondition = True
```
## 十、initClusterExtra — 安装自定义脚本
**代码位置**：[ensure_nodes_env.go:318-352](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L318-L352)
### 10.1 installCommonScripts — 安装基础脚本
**代码位置**：[ensure_nodes_env.go:374-403](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L374-L403)

**基础脚本列表**（必须全部存在，任一缺失则中止）：

| 脚本 | 安装节点 | 参数 |
|------|---------|------|
| `file-downloader.sh` | 所有节点 | nodesIps=全部IP |
| `package-downloader.sh` | 所有节点 | nodesIps=全部IP |

**执行方式**：通过 `LocalClient.InstallAddon()` 以 `clusterextra` addon 的形式部署到目标集群。

**关键特性**：基础脚本是**串行阻塞**的，任一脚本缺失或安装失败，整个基础脚本安装中止（`return` 而非 `continue`）。
### 10.2 installOtherCustomScripts — 安装其他自定义脚本
**代码位置**：[ensure_nodes_env.go:405-451](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L405-L451)

**默认自定义脚本列表**：

| 脚本 | 安装节点 | 特殊逻辑 | 参数 |
|------|---------|---------|------|
| `install-lxcfs.sh` | 所有节点 | — | nodesIps |
| `install-nfsutils.sh` | — | 需要 `pipelineServer` 配置 | pipelineServer IP |
| `install-etcdctl.sh` | Etcd 节点 | — | etcdNodesIps |
| `install-helm.sh` | Master 节点 | — | masterNodesIps |
| `install-calicoctl.sh` | Master 节点 | — | masterNodesIps |
| `update-runc.sh` | 所有节点（排除 host 节点） | 仅 Docker 场景；block=true | nodesIps, httpRepo |
| `clean-docker-images.py` | — | 需要 `pipelineServer` + `pipelineServerEnableCleanImages=true` | pipelineServer IP |

**自定义脚本来源**：
- 默认使用 `defaultEnvExtraExecScripts` 列表
- 如果 `BKECluster.Spec.ClusterConfig.CustomExtra["envExtraExecScripts"]` 有配置，则使用用户自定义的脚本列表

**容错策略**：与基础脚本不同，自定义脚本是**非阻塞**的，单个脚本缺失或失败仅记录警告，继续执行下一个（`continue`）。

**特殊处理**：
- `update-runc.sh`：当 CRI 为 containerd 时跳过（仅 Docker 需要）；如果 `CustomExtra["host"]` 有值，排除该 IP 节点
- `clean-docker-images.py`：需要同时配置 `pipelineServer` 和 `pipelineServerEnableCleanImages=true`
- `install-nfsutils.sh`：需要配置 `pipelineServer`
## 十一、executeNodePreprocessScripts — 执行前置处理脚本
**代码位置**：[ensure_nodes_env.go:453-505](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L453-L505)
### 11.1 checkPreprocessConfigExists — 检查前置处理配置
**代码位置**：[ensure_nodes_env.go:538-600](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L538-L600)

按优先级检查三种 ConfigMap（命名空间均为 `user-system`）：
```
优先级 1：全局配置
  └── ConfigMap: preprocess-all-config
        存在 → 所有节点都需要执行前置处理

优先级 2：批次配置
  └── ConfigMap: preprocess-node-batch-mapping
        └── Data["mapping.json"] → {nodeIP: batchId}
              └── ConfigMap: preprocess-config-batch-{batchId}
                    存在 → 该节点需要执行前置处理

优先级 3：节点配置
  └── ConfigMap: preprocess-config-node-{nodeIP}
        存在 → 该节点需要执行前置处理
```
### 11.2 createPreprocessCommand — 创建前置处理 Command
**代码位置**：[ensure_nodes_env.go:507-536](file:///D:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L507-L536)
```
Command: preprocess-all-nodes-{timestamp}
NodeSelector: 所有有配置的节点 IP
WaitTimeout: 30 分钟
RemoveAfterWait: true

Commands:
  └── BuiltIn: "Preprocess"
      ID: execute-preprocess-scripts
      BackoffIgnore: false
```
**执行逻辑**：BKEAgent 收到 `Preprocess` 内置命令后，自动获取当前节点 IP，查找对应的 ConfigMap 配置，执行前置处理脚本。
## 十二、节点状态变迁图
```
初始状态（EnsureBKEAgent 阶段已完成）
  │
  ▼ NodeAgentReadyFlag ✓, NodeEnvFlag ✗
  │
  ▼ NeedExecute 检测到未标记 NodeEnvFlag 的节点
  │
  ▼
NodeInitializing + "Initializing node env"    ← getNodesToInitEnv()
  │
  ▼
NodesEnvCondition = False                     ← setupClusterConditionAndSync()
  │
  ▼
┌────────── Command 执行 ─────────┐
│                                 │
│ 成功                            │ 失败
▼                                 ▼
NodeEnvFlag ✓                      NodeInitFailed
"Nodes env is ready"               Worker → NeedSkip ✓
                                   Master → 不跳过（可重试）
  │
  ▼
  ┌────────── 自定义脚本安装 ─────────┐
  │                                   │
  │ 基础脚本（阻塞）                  │ 自定义脚本（非阻塞）
  │ file-downloader.sh                │ install-lxcfs.sh
  │ package-downloader.sh             │ install-nfsutils.sh
  │                                   │ install-etcdctl.sh
  │                                   │ install-helm.sh
  │                                   │ install-calicoctl.sh
  │                                   │ update-runc.sh (仅Docker)
  │                                   │ clean-docker-images.py
  │
  ▼
  ┌────────── 前置处理脚本 ──────────┐
  │ 检查 ConfigMap 配置              │
  │ 全局 > 批次 > 节点               │
  │ 有配置 → 创建 Preprocess Command │
  │ 无配置 → 跳过                    │
  │
  ▼
NodesEnvCondition = True
[进入下一阶段 EnsureClusterAPIObj]
```
## 十三、容错与重试机制总结
| 场景 | 处理策略 |
|------|---------|
| 节点 Agent 未就绪 | 跳过该节点（`NodeAgentReadyFlag = 0`） |
| 节点已失败/删除/跳过 | 跳过该节点 |
| 全部节点 ENV 初始化失败 | 返回错误，触发 Reconcile 重试 |
| Worker 节点 ENV 失败 | 标记 `NeedSkip`，继续后续流程 |
| Master 节点 ENV 失败 | 不标记跳过，后续阶段可重试 |
| Deploying 状态下有不可跳过的失败节点 | 返回错误重试 |
| 基础脚本缺失/失败 | **中止**整个脚本安装（`return`） |
| 自定义脚本缺失/失败 | **跳过**该脚本继续（`continue`） |
| 前置处理无 ConfigMap 配置 | 跳过，不创建 Command |
| 前置处理执行失败 | 返回错误（包含成功/失败节点信息） |
| DeepRestore 注解不存在 | 默认启用深度重置（包含 containerRuntime） |
| `update-runc.sh` + containerd | 跳过（仅 Docker 场景需要） |
| Command 超时 | `GetBootTimeOut` 控制超时时间 |
| ENV Command Unique=true | 同集群仅保留一个 env init 命令，避免重复执行 |

# SyncStatusUntilComplete 业务流程梳理
## 一、函数签名与定位
```go
func SyncStatusUntilComplete(c client.Client, bkeCluster *v1beta1.BKECluster, patchs ...PatchFunc) error
```
**代码位置**：[bkecluster.go:43-66](file:///D:/code/github/cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L43-L66)

**核心职责**：将 BKECluster 的内存状态（各阶段修改的 Spec/Status/Conditions 等）持久化到管理集群的 API Server，确保更新成功完成。这是整个部署流程中**最关键的状态同步函数**，几乎所有阶段在修改集群状态后都会调用它。
## 二、总体流程概览
```
SyncStatusUntilComplete()
  │
  ├── 创建 2 分钟超时 context
  │
  └── 循环重试（直到成功或超时）
        │
        ├── UpdateCombinedBKECluster()        — 核心更新逻辑
        │     │
        │     ├── 1. prepareClusterData()     — 准备当前集群数据 + 应用 Patch
        │     │     └── GetCombinedBKECluster() — 从 API Server 获取最新 BKECluster + ConfigMap
        │     │
        │     ├── 2. handleExternalUpdates()  — 合并外部更新
        │     │     └── GetCurrentBkeClusterPatches() → JSON Patch 合并
        │     │
        │     ├── 3. initializePatchHelper()  — 初始化 CAPI PatchHelper
        │     │
        │     ├── 4. handleInternalUpdateCondition() — 处理内部更新条件标记
        │     │
        │     ├── 5. processNodeData()        — 处理节点数据分发
        │     │     ├── getBkeClusterAssociateNodesCM() — 获取关联的 ConfigMap
        │     │     └── 节点分发到 finalClusterNodes / finalCMNodes
        │     │
        │     └── 6. updateClusterAndConfigMap() — 最终写入
        │           ├── newTmpBkeCluster()    — 构建最终 BKECluster 对象
        │           ├── fixPhaseStatus()      — 修复 PhaseStatus 大小
        │           ├── 设置 LastUpdateConfiguration 注解
        │           ├── getBKENodesForCluster() — 获取 BKENode CRD
        │           ├── BKEClusterStatusManager.SetStatus() — 计算集群健康状态
        │           ├── updateModifiedBKENodes() — 更新被修改的 BKENode
        │           ├── PatchHelper.Patch()   — 更新 BKECluster CRD
        │           └── Client.Update(CM)     — 更新 ConfigMap
        │
        ├── 成功 → break（随机 sleep 0-2 秒后退出）
        │
        └── 失败处理：
              ├── NotFound → 跳过（集群已删除）
              ├── Conflict → 重试（并发冲突）
              ├── Forbidden/BadRequest/Invalid → 直接返回错误
              └── 其他错误 → 重试
```
## 三、外层循环：SyncStatusUntilComplete
**代码位置**：[bkecluster.go:43-66](file:///D:/code/github/cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L43-L66)
```
1. 创建 2 分钟超时 context（SyncStatusTimeout = 2 * time.Minute）
2. 进入 for 循环：
   ├── 检查 context 是否超时 → 是则返回 "The update failed to complete after 2 minutes."
   ├── 调用 UpdateCombinedBKECluster()
   ├── 返回值处理：
   │     ├── nil（成功）       → sleep 0~2 秒随机时间后 break
   │     ├── NotFound          → 记录日志，break（集群已删除，无需更新）
   │     ├── Conflict          → 记录日志，continue（重试）
   │     ├── Forbidden/BadRequest/Invalid → 直接返回错误（不可恢复）
   │     └── 其他错误          → 记录日志，continue（重试）
   └── 循环直到成功或超时
```
**关键设计**：
- **Conflict 重试**：K8s 乐观锁冲突时自动重试，确保并发安全
- **随机 sleep**：成功后 sleep 0~2 秒，错开并发更新峰值
- **2 分钟超时**：防止无限重试
## 四、核心逻辑：UpdateCombinedBKECluster
**代码位置**：[bkecluster.go:329-368](file:///D:/code/github/cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L329-L368)
### 步骤 1：prepareClusterData — 准备当前集群数据
```
1. 调用 GetCombinedBKECluster() 从 API Server 获取最新的 BKECluster + ConfigMap
   ├── c.Get() 获取 BKECluster CRD
   ├── GetCombinedBKEClusterCM() 获取关联的 ConfigMap
   │     ├── ConfigMap 存在 → 返回
   │     └── ConfigMap 不存在 → 自动创建默认 ConfigMap（nodes:[], status:[]）
   └── CombinedBKECluster() 合并 BKECluster + ConfigMap

2. 修复 PhaseStatus：fixPhaseStatus()
   ├── 去重（deduplicatePhaseStatus）
   └── 清理过多的 EnsureCluster 失败记录（最多保留3条）

3. 应用传入的 PatchFunc：
   for _, p := range patchs {
       p(currentCombinedBkeCluster)  // 应用到最新数据
       p(combinedCluster)            // 应用到内存数据
   }
```
### 步骤 2：handleExternalUpdates — 合并外部更新
**代码位置**：[bkecluster.go:253-276](file:///D:/code/github/cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L253-L276)
```
目的：检测是否有外部（用户或其他 Controller）修改了 BKECluster 的 Spec/Labels/Annotations/Finalizers，
     如果有，将这些修改合并到当前 combinedCluster 中。

1. 调用 GetCurrentBkeClusterPatches() 计算差异：
   ├── 清除 LastUpdateConfiguration 注解（避免循环比较）
   ├── 只比较 Spec、Labels、Annotations、Finalizers
   ├── 忽略 Status（Status 由 Controller 管理）
   └── 使用 JSON Patch（patchutil.Diff）计算 old vs new 的差异

2. 如果 patches 不为空：
   ├── 将 combinedCluster 序列化为 JSON
   ├── 应用 JSON Patch
   └── 反序列化回 combinedCluster
```
**为什么需要这一步**：在 Controller Reconcile 过程中，用户可能修改了 BKECluster 的 Spec（如添加节点、修改配置），这些修改需要被保留，不能被 Controller 的内存状态覆盖。
### 步骤 3：initializePatchHelper — 初始化 PatchHelper
```
1. 从 API Server 获取最新的 BKECluster（currentBkeCluster）
2. 使用 CAPI 的 patch.NewHelper() 创建 PatchHelper
   └── PatchHelper 会记录 currentBkeCluster 的原始状态，用于后续计算差异
```
### 步骤 4：handleInternalUpdateCondition — 处理内部更新条件
**代码位置**：[bkecluster.go:296-327](file:///D:/code/github/cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L296-L327)
```
如果 config.EnableInternalUpdate = true：
  ├── 有 PatchFunc（Spec 变更）：
  │     └── 标记 InternalSpecChangeCondition → 防止内部更新触发 Reconcile 入队
  │         然后 Patch currentBkeCluster
  └── 无 PatchFunc（仅 Status 变更）：
        └── 如果已有 InternalSpecChangeCondition → 移除它并 Patch
```
### 步骤 5：processNodeData — 处理节点数据分发
**代码位置**：[bkecluster.go:278-327](file:///D:/code/github/cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L278-L327)
```
目的：将节点数据分发到 BKECluster CRD 和 ConfigMap 两个存储位置

1. 获取关联的 ConfigMap 和其中的节点数据（nodesCM）
   ├── GetCombinedBKEClusterCM() → ConfigMap
   └── 从 ConfigMap.Data["nodes"] 反序列化 → nodesCM.spec

2. 从 combinedCluster 提取节点数据（nodesCombined）

3. 节点分发逻辑：
   for _, node := range nodesCombined.spec {
       ├── node.IP 在 deleteNodes 中 → 跳过（删除节点）
       ├── node.IP 存在于 nodesCM.spec 中 → finalCMNodes（写入 ConfigMap）
       └── node.IP 不在 nodesCM.spec 中 → finalClusterNodes（写入 BKECluster CRD）
   }
```
**节点分发策略**：

| 条件 | 目标存储 | 说明 |
|------|---------|------|
| 节点在 deleteNodes 中 | 丢弃 | 正在删除的节点 |
| 节点在 ConfigMap 中已存在 | ConfigMap | 已有的节点继续由 ConfigMap 管理 |
| 节点是新增的 | BKECluster CRD | 新增节点由 BKECluster Spec 管理 |
### 步骤 6：updateClusterAndConfigMap — 最终写入
**代码位置**：[bkecluster.go:369-438](file:///D:/code/github/cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L369-L438)
```
1. 构建 newBKECluster：
   ├── newTmpBkeCluster(combinedCluster, currentBkeCluster)
   │     ├── 深拷贝 combinedCluster（获取最新的 Spec/Status）
   │     ├── 保留 currentBkeCluster 的 ObjectMeta（UID/ResourceVersion 等）
   │     └── 使用 combinedCluster 的 Labels/Annotations/OwnerReferences/Finalizers
   │
   ├── fixPhaseStatus() — 去重和清理 PhaseStatus
   │
   ├── 设置 LastUpdateConfiguration 注解：
   │     └── 将 cleanBkeCluster() 序列化为 JSON 存入注解
   │         （仅保留 Name/Namespace/Spec，用于下次比较外部更新）
   │
   ├── 获取 BKENode CRD 列表：
   │     └── getBKENodesForCluster() → 按 cluster label 过滤
   │
   ├── BKEClusterStatusManager.SetStatus() — 计算集群健康状态
   │     ├── recordBKEClusterStatus() — 记录集群状态到内存缓存
   │     └── recordBKENodesStatus()   — 记录节点状态到内存缓存
   │
   ├── updateModifiedBKENodes() — 更新被修改的 BKENode CRD
   │     ├── GetModifiedNodes() — 获取标记了 NodeStateNeedRecord 的节点
   │     ├── 清除 NodeStateNeedRecord 标记
   │     └── Status().Update() — 更新到 API Server
   │
   ├── 回写关键状态到 combinedCluster（供调用方使用）：
   │     ├── ClusterHealthState
   │     ├── ClusterStatus
   │     └── Conditions
   │
   ├── PatchHelper.Patch(newBKECluster) — 更新 BKECluster CRD
   │
   └── Client.Update(ConfigMap) — 更新 ConfigMap
        ├── 将 finalCMNodes 序列化为 JSON 写入 Data["nodes"]
        └── 设置 LastUpdateConfiguration 注解
```
## 五、数据流图
```
┌─────────────────────────────────────────────────────────────────────┐
│                     API Server (管理集群)                           │
│                                                                     │
│  ┌──────────────────┐     ┌──────────────────┐  ┌───────────────┐   │
│  │ BKECluster CRD   │     │ ConfigMap        │  │ BKENode CRDs  │   │
│  │ (Spec + Status)  │     │ (nodes 数据)     │  │ (节点状态)    │   │
│  └────────┬─────────┘     └────────┬─────────┘  └───────┬───────┘   │
│           │                        │                     │          │
└───────────┼────────────────────────┼─────────────────────┼──────────┘
            │ Get                    │ Get                 │ List
            ▼                        ▼                     ▼
┌─────────────────────────────────────────────────────────────────────┐
│               SyncStatusUntilComplete (Controller 内存)             │
│                                                                     │
│  combinedCluster (内存中的最新状态)                                 │
│  ├── Spec (各阶段修改)                                              │
│  ├── Status (Conditions, PhaseStatus, ClusterHealthState...)        │
│  └── Annotations (LastUpdateConfiguration...)                       │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │ UpdateCombinedBKECluster                                    │    │
│  │                                                             │    │
│  │  1. GetCombinedBKECluster() ← API Server 最新数据           │    │
│  │  2. handleExternalUpdates() ← 合并外部修改                  │    │
│  │  3. processNodeData()       ← 节点分发                      │    │
│  │  4. SetStatus()             ← 计算健康状态                  │    │
│  │  5. Patch(BKECluster)       → 写入 BKECluster CRD           │    │
│  │  6. Update(ConfigMap)       → 写入 ConfigMap                │    │
│  │  7. StatusUpdate(BKENodes)  → 写入 BKENode CRDs             │    │
│  └─────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘
```
## 六、LastUpdateConfiguration 注解机制
这是 `SyncStatusUntilComplete` 中一个重要的防冲突机制：
```
写入时：
  annotation.SetAnnotation(newBKECuster, LastUpdateConfigurationAnnotationKey, 
      JSON(cleanBkeCluster(bkeCluster)))
  // cleanBkeCluster 仅保留 Name/Namespace/Spec，去除 Status

读取时（下次 SyncStatusUntilComplete）：
  1. 从 BKECluster 注解中反序列化出 lastBkeCluster
  2. 从 ConfigMap 注解中反序列化出 lastCM
  3. 合并为 lastAnnotation = CombinedBKECluster(lastBkeCluster, lastCM)
  4. 将 lastAnnotation 序列化后重新设置到 BKECluster 注解

用途：
  在 handleExternalUpdates() 中，通过比较 lastUpdate 和 currentUpdate 的差异，
  检测外部修改并合并到当前更新中，避免覆盖用户的修改。
```

## 七、PatchFunc 机制
```go
type PatchFunc func(currentCombinedBkeCluster *v1beta1.BKECluster)
```
`SyncStatusUntilComplete` 支持传入可选的 `PatchFunc`，用于在状态同步时同时修改 BKECluster：

| PatchFunc 场景 | 说明 |
|----------------|------|
| 有 PatchFunc | 表示本次更新包含 Spec 变更，标记 `InternalSpecChangeCondition` 防止触发 Reconcile |
| 无 PatchFunc | 表示仅 Status 变更，清除 `InternalSpecChangeCondition` |
## 八、错误处理与重试策略总结
| 错误类型 | 处理策略 | 原因 |
|---------|---------|------|
| `Conflict` | 重试（continue） | 乐观锁冲突，其他 Controller/用户同时修改，重试可解决 |
| `NotFound` | 跳过（break） | BKECluster 已被删除，无需更新 |
| `Forbidden` | 直接返回 | 权限不足，不可恢复 |
| `BadRequest` | 直接返回 | 请求格式错误，不可恢复 |
| `Invalid` | 直接返回 | 数据校验失败，不可恢复 |
| 其他错误 | 重试（continue） | 网络抖动等临时性问题 |
| 超时（2分钟） | 返回错误 | 防止无限重试 |
## 九、调用场景
`SyncStatusUntilComplete` 在整个部署流程中被广泛调用，典型场景：

| 调用位置 | 用途 |
|---------|------|
| `EnsureBKEAgent.handlePushResults()` | 推送 Agent 成功后同步节点状态 |
| `EnsureBKEAgent.pingAgent()` | Ping 成功后同步节点信息 |
| `EnsureNodesEnv.setupClusterConditionAndSync()` | 设置 NodesEnvCondition |
| `EnsureNodesEnv.finalDecisionAndCleanup()` | 环境初始化完成后同步最终状态 |
| `EnsureNodesPostProcess` | 后置脚本完成后同步状态 |
| `EnsureAgentSwitch.reconcileAgentSwitch()` | Agent 切换后同步注解 |
| 各阶段设置 Condition 后 | 确保条件变更被持久化 |


# env.go 业务流程梳理
## 一、文件定位与职责
**代码位置**：[env.go](file:///D:/code/github/cluster-api-provider-bke/pkg/command/env.go)

**核心职责**：封装 K8s 节点环境初始化相关的 Command 创建逻辑。`ENV` 结构体负责在管理集群上创建 `Command` CRD，由目标节点上的 BKEAgent 拉取执行，完成节点环境准备。
## 二、ENV 结构体
```go
type ENV struct {
    BaseCommand                     // 继承基础命令能力（Client/Timeout/Wait 等）

    Nodes         bkenode.Nodes     // 目标节点列表
    BkeConfigName string            // BKE 配置名称（对应 BKECluster.Name）
    Extra         []string          // 额外 IP（用于证书 SAN）
    ExtraHosts    []string          // 额外 hosts 映射
    DryRun        bool              // DryRun 模式（仅检查不执行）
    PrePullImage  bool              // 是否预拉取镜像
    DeepRestore   bool              // 是否深度重置（包含 containerRuntime）
}
```
## 三、三种 Command 创建方法
`ENV` 提供三个方法，分别对应三种不同的环境操作场景：

| 方法 | 命令名常量 | 用途 | 调用场景 |
|------|-----------|------|---------|
| `New()` | `k8s-env-init` | 完整环境初始化 | EnsureNodesEnv 阶段 |
| `NewConatinerdReset()` | `k8s-containerd-reset` | Containerd 配置重置 | Containerd 升级场景 |
| `NewConatinerdRedeploy()` | `k8s-containerd-redeploy` | Containerd 重新部署 | Containerd 重部署场景 |
## 四、New() — 完整环境初始化
**代码位置**：[env.go:89-109](file:///D:/code/github/cluster-api-provider-bke/pkg/command/env.go#L89-L109)
### 4.1 总体流程
```
New()
  │
  ├── 1. Validate()                    — 参数校验
  │
  ├── 2. getCommandName()              — 确定命令名称
  │
  ├── 3. GenerateBkeConfigStr()        — 生成 bkeConfig 参数
  │
  ├── 4. 格式化 extra / extraHosts     — 生成额外参数
  │
  ├── 5. getScope()                    — 确定 Reset 的 scope
  │
  ├── 6. buildCommandSpec()            — 构建命令规格（3条顺序命令）
  │
  ├── 7. DryRun 处理                   — 仅保留第一条命令
  │
  ├── 8. PrePullImage 处理             — 额外创建预拉取镜像命令
  │
  ├── 9. 设置 NodeSelector             — 按节点 IP 选择目标节点
  │
  └── 10. newCommand()                 — 在管理集群创建 Command CRD
```
### 4.2 步骤详解
#### 步骤 1：Validate
```go
func (e *ENV) Validate() error {
    return ValidateBkeCommand(e.Nodes, e.BkeConfigName, &e.BaseCommand)
}
```
校验内容：
- `Client` 不为 nil
- `Nodes` 至少有 1 个节点
- `BkeConfigName` 不为空
- `Scheme` 不为 nil
- `NameSpace` 不为空
#### 步骤 2：getCommandName
```
DryRun = true  → "k8s-env-dry-run"
DryRun = false → "k8s-env-init"
```
#### 步骤 3：GenerateBkeConfigStr
```go
func GenerateBkeConfigStr(namespace, bkeConfigName string) string {
    return fmt.Sprintf("bkeConfig=%s:%s", namespace, bkeConfigName)
}
// 输出示例: "bkeConfig=default:my-cluster"
```
BKEAgent 通过此参数找到对应的 `BkeConfig` ConfigMap，获取集群配置信息（镜像仓库地址、Yum 源等）。
#### 步骤 4：格式化 extra / extraHosts
```go
extra     = "extra=192.168.1.100,192.168.1.200"        // VIP/Ingress IP
extraHosts = "extraHosts=master.bocloud.com:192.168.1.100" // hosts 映射
```
#### 步骤 5：getScope
```
DeepRestore = true  → "scope=cert,manifests,container,kubelet,containerRuntime,extra"
DeepRestore = false → "scope=cert,manifests,container,kubelet,extra"
```
差异：`containerRuntime`（是否重置容器运行时配置）
#### 步骤 6：buildCommandSpec — 核心命令构建
创建 3 条**顺序执行**的内置命令：
```
┌─────────────────────────────────────────────────────────────────────┐
│ Command 1: "node hardware resources check"                         │
│                                                                     │
│   K8sEnvInit init=true check=true scope=node bkeConfig=ns:name     │
│                                                                     │
│   功能: 检查节点硬件资源是否满足 K8s 运行要求                          │
│   重试: BackoffIgnore=false（失败不跳过，阻塞后续命令）                 │
│   类型: BuiltIn（BKEAgent 内置实现）                                  │
├─────────────────────────────────────────────────────────────────────┤
│ Command 2: "reset"                                                  │
│                                                                     │
│   Reset bkeConfig=ns:name scope=cert,manifests,container,           │
│         kubelet[,containerRuntime],extra extra=VIP1,VIP2            │
│                                                                     │
│   功能: 重置节点环境（清理旧配置/证书/容器/manifests 等）               │
│   重试: BackoffIgnore=true（失败可跳过，继续执行后续命令）              │
│   类型: BuiltIn                                                      │
├─────────────────────────────────────────────────────────────────────┤
│ Command 3: "init and check node env"                                │
│                                                                     │
│   K8sEnvInit init=true check=true                                   │
│     scope=time,hosts,dns,kernel,firewall,selinux,swap,              │
│           httpRepo,runtime,iptables,registry,extra                   │
│     bkeConfig=ns:name extraHosts=master.bocloud.com:VIP             │
│                                                                     │
│   功能: 初始化并检查节点环境                                          │
│   重试: BackoffDelay=5, BackoffIgnore=false（延迟5秒重试，失败不跳过） │
│   类型: BuiltIn                                                      │
└─────────────────────────────────────────────────────────────────────┘
```
**scope 各项含义**：

| scope 值 | Command | 说明 |
|----------|---------|------|
| `node` | K8sEnvInit #1 | 硬件资源检查（CPU/内存/磁盘） |
| `cert` | Reset | 清理旧证书文件 |
| `manifests` | Reset | 清理 K8s static manifest 文件 |
| `container` | Reset | 停止并清理运行中的容器 |
| `kubelet` | Reset | 清理 kubelet 配置和数据 |
| `containerRuntime` | Reset | 清理容器运行时（containerd/docker）配置 |
| `extra` | Reset | 清理额外配置 |
| `time` | K8sEnvInit #3 | NTP 时间同步配置 |
| `hosts` | K8sEnvInit #3 | /etc/hosts 配置 |
| `dns` | K8sEnvInit #3 | DNS 配置 |
| `kernel` | K8sEnvInit #3 | 内核参数调优 |
| `firewall` | K8sEnvInit #3 | 防火墙配置 |
| `selinux` | K8sEnvInit #3 | SELinux 配置 |
| `swap` | K8sEnvInit #3 | 关闭 swap |
| `httpRepo` | K8sEnvInit #3 | HTTP 仓库配置 |
| `runtime` | K8sEnvInit #3 | 容器运行时安装配置 |
| `iptables` | K8sEnvInit #3 | iptables 规则配置 |
| `registry` | K8sEnvInit #3 | 镜像仓库认证配置 |
| `extra` | K8sEnvInit #3 | 额外配置（VIP hosts 等） |
| `image` | PrePull | 预拉取 K8s 所需镜像 |
#### 步骤 7：DryRun 处理
```go
if e.DryRun {
    commandSpec.Commands = commandSpec.Commands[:1]
}
```
DryRun 模式仅保留第一条命令（硬件资源检查），不执行 Reset 和环境初始化。
#### 步骤 8：PrePullImage 处理
```go
if e.PrePullImage {
    e.createPrePullImageCommand(bkeConfigStr)
}
```
创建一个**独立的**预拉取镜像命令：
```
Command: k8s-image-pre-pull-{timestamp}
NodeSelector: 排除首个 Master 节点（Master 初始化时会自动拉取）
Commands:
  └── K8sEnvInit init=true check=true scope=image bkeConfig=ns:name
      BackoffDelay=15, BackoffIgnore=true（延迟15秒重试，失败可跳过）
```
**为什么排除首个 Master**：首个 Master 在 `kubeadm init` 时会自动拉取所需镜像，无需预拉取。
**容错**：预拉取镜像失败不影响集群部署（`BackoffIgnore=true`），且 `newCommand` 的错误被忽略（`_ = e.newCommand(...)`）。
#### 步骤 9：NodeSelector
```go
commandSpec.NodeSelector = getNodeSelector(e.Nodes)
```
生成的 NodeSelector 格式：
```yaml
nodeSelector:
  matchLabels:
    192.168.1.10: "192.168.1.10"
    192.168.1.11: "192.168.1.11"
```
BKEAgent 通过匹配本机网卡 IP 来判断是否应该执行该命令。
#### 步骤 10：newCommand
调用 `BaseCommand.newCommand()` 在管理集群创建 `Command` CRD：
- 设置 OwnerReference（BKECluster 为 Owner）
- 设置 Label（`bke.bocloud.com/cluster-command`）
- Unique=true 时删除同名前缀的已有命令
## 五、NewConatinerdReset() — Containerd 配置重置
**代码位置**：[env.go:46-70](file:///D:/code/github/cluster-api-provider-bke/pkg/command/env.go#L46-L70)
```
Command: k8s-containerd-reset-{timestamp}
Commands:
  └── Reset bkeConfig=ns:name scope=containerd-cfg extra=VIP1,VIP2
      BackoffIgnore=true（失败可跳过）

功能: 仅重置 containerd 配置文件
场景: Containerd 配置变更后的重置（如升级/修改镜像仓库配置）
```
**与 New() 中 Reset 的区别**：
- `New()` 的 Reset scope 范围更广（cert,manifests,container,kubelet,...）
- `NewConatinerdReset()` 仅重置 `containerd-cfg`，影响范围小
## 六、NewConatinerdRedeploy() — Containerd 重新部署
**代码位置**：[env.go:72-96](file:///D:/code/github/cluster-api-provider-bke/pkg/command/env.go#L72-L96)
```
Command: k8s-containerd-redeploy-{timestamp}
Commands:
  └── K8sEnvInit init=true check=true scope=runtime bkeConfig=ns:name
      BackoffIgnore=false, BackoffDelay=5

功能: 重新部署容器运行时（containerd/docker）
场景: Containerd 版本升级后重新部署
```
**与 New() 中 K8sEnvInit #3 的区别**：
- `New()` 的 scope 包含 time,hosts,dns,kernel,... 等全面初始化
- `NewConatinerdRedeploy()` 仅 scope=runtime，只重新部署容器运行时
## 七、Wait() — 等待命令执行完成
**代码位置**：[env.go:234-244](file:///D:/code/github/cluster-api-provider-bke/pkg/command/env.go#L234-L244)
```go
func (e *ENV) Wait() (error, []string, []string) {
    err, complete, nodes := e.waitCommandComplete()
    // means all command not executed
    if !complete && len(nodes.FailedNodes) == 0 {
        for _, node := range e.Nodes {
            if utils.ContainsString(nodes.SuccessNodes, node.Hostname) {
                continue
            }
            nodes.FailedNodes = append(nodes.FailedNodes, node.Hostname)
        }
    }
    return err, nodes.SuccessNodes, nodes.FailedNodes
}
```
**逻辑**：
1. 调用 `BaseCommand.waitCommandComplete()` 轮询管理集群上的 Command 状态
2. 特殊处理：如果命令未完成（`!complete`）且没有失败节点（超时场景），将所有未成功的节点标记为失败
3. 返回 `(error, successNodes, failedNodes)`

**超时场景处理**：当 `WaitTimeout` 到期但命令尚未在所有节点执行完成时，`complete=false` 且 `FailedNodes` 为空。此时将所有不在 `SuccessNodes` 中的节点视为失败。
## 八、三种方法对比
| 维度 | New() | NewConatinerdReset() | NewConatinerdRedeploy() |
|------|-------|---------------------|------------------------|
| **命令数** | 3 条（+ 可选预拉取） | 1 条 | 1 条 |
| **命令序列** | 硬件检查 → Reset → 环境初始化 | Reset(containerd-cfg) | K8sEnvInit(runtime) |
| **scope** | 全面（node → cert,manifests,... → time,hosts,...） | 仅 containerd-cfg | 仅 runtime |
| **DryRun 支持** | ✓（仅保留硬件检查） | ✗ | ✗ |
| **PrePullImage** | ✓（可选） | ✗ | ✗ |
| **DeepRestore** | ✓（影响 Reset scope） | ✗（固定 containerd-cfg） | ✗（固定 runtime） |
| **Extra/ExtraHosts** | ✓ | ✓（仅 Extra） | ✗ |
| **场景** | 首次部署/完整重置 | Containerd 配置变更 | Containerd 版本升级 |
| **TTLSecondsAfterFinished** | 0（不自动清理） | 0 | 0 |
## 九、Command CRD 最终结构示例
以 `New()` 创建的 Command 为例，最终在管理集群上创建的 CRD 如下：
```yaml
apiVersion: bkeagent.bocloud.com/v1beta1
kind: Command
metadata:
  name: k8s-env-init-1710000000
  namespace: default
  labels:
    bke.bocloud.com/cluster-command: ""
    cluster.x-k8s.io/cluster-name: my-cluster
  ownerReferences:
    - apiVersion: bke.bocloud.com/v1beta1
      kind: BKECluster
      name: my-cluster
      controller: true
spec:
  nodeName: ""
  suspend: false
  backoffLimit: 3
  activeDeadlineSecond: 1000
  ttlSecondsAfterFinished: 0
  nodeSelector:
    matchLabels:
      192.168.1.10: "192.168.1.10"
      192.168.1.11: "192.168.1.11"
  commands:
    - id: "node hardware resources check"
      command: ["K8sEnvInit", "init=true", "check=true", "scope=node", "bkeConfig=default:my-cluster"]
      type: BuiltIn
      backoffIgnore: false
    - id: "reset"
      command: ["Reset", "bkeConfig=default:my-cluster", "scope=cert,manifests,container,kubelet,containerRuntime,extra", "extra=192.168.1.100"]
      type: BuiltIn
      backoffIgnore: true
    - id: "init and check node env"
      command: ["K8sEnvInit", "init=true", "check=true", "scope=time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,runtime,iptables,registry,extra", "bkeConfig=default:my-cluster", "extraHosts=master.bocloud.com:192.168.1.100"]
      type: BuiltIn
      backoffDelay: 5
      backoffIgnore: false
status: {}   # 由各节点 BKEAgent 执行后填充
```
## 十、执行时序图
```
Controller (管理集群)                    API Server                    BKEAgent (目标节点)
      │                                     │                                │
      │  ENV.New()                          │                                │
      │  ─── 创建 Command CRD ──────────►   │                                │
      │                                     │                                │
      │  ENV.Wait()                         │                                │
      │  ─── 轮询 Command.Status ◄───────   │                                │
      │                                     │   Watch Command CRD            │
      │                                     │   ◄────────────────────────────│
      │                                     │                                │
      │                                     │   NodeSelector 匹配本机 IP ✓   │
      │                                     │                                │
      │                                     │   执行 Command #1:             │
      │                                     │   K8sEnvInit scope=node        │
      │                                     │   → 检查硬件资源               │
      │                                     │                                │
      │                                     │   执行 Command #2:             │
      │                                     │   Reset scope=cert,...         │
      │                                     │   → 清理旧环境                 │
      │                                     │                                │
      │                                     │   执行 Command #3:             │
      │                                     │   K8sEnvInit scope=time,...    │
      │                                     │   → 初始化环境                 │
      │                                     │                                │
      │                                     │   更新 Command.Status ──────►  │
      │  ◄────── Status 已更新 ──────────   │                                │
      │                                     │                                │
      │  返回 (error, successNodes, failedNodes)                             │
```

# 详细分析 `agentv1beta1.CommandSpec` 在执行时是如何进行命令拼接的
## `agentv1beta1.CommandSpec` 命令拼接与执行机制分析
### 一、整体架构
`CommandSpec` 的命令执行是一个**三层分发架构**：
```
CommandReconciler (控制器层)
    ↓ 遍历 spec.commands[]
Job.Execute (任务路由层)
    ↓ 按 Type 分发
Plugin/Shell/K8s (具体执行层)
    ↓ 实际执行
exec.CommandExecutor (系统命令执行层)
```
### 二、CommandSpec 的数据结构
[command_types.go](file:///d:/code/github/cluster-api-provider-bke/api/bkeagent/v1beta1/command_types.go) 中定义了核心结构：
```go
type CommandSpec struct {
    NodeName             string         // 指定单个节点
    Suspend              bool           // 暂停执行
    Commands             []ExecCommand  // 按顺序执行的指令数组
    BackoffLimit         int            // 最大重试次数
    ActiveDeadlineSecond int            // 超时时间
    TTLSecondsAfterFinished int         // 完成后清理时间
    NodeSelector         *LabelSelector // 节点选择器
}

type ExecCommand struct {
    ID            string       // 唯一标识
    Command       []string     // 命令参数数组
    Type          CommandType  // 命令类型: BuiltIn / Shell / Kubernetes
    BackoffIgnore bool         // 失败后是否跳过
    BackoffDelay  int          // 重试间隔
}
```
**关键点**：`ExecCommand.Command` 是一个 `[]string` 数组，其拼接方式取决于 `Type` 字段。
### 三、三种命令类型的拼接方式
#### 1. `CommandBuiltIn` — 内置插件路由
**拼接规则**：`Command[0]` 作为插件名称，`Command[1:]` 作为 `key=value` 形式的参数。

**执行链路**：
```
CommandReconciler.executeByType()
    → Job.BuiltIn.Execute(command)
        → builtin.Task.Execute(execCommands)
            → pluginRegistry[execCommands[0]].Execute(execCommands)
                → plugin.ParseCommands(plugin, commands)  // 解析 Command[1:] 为 key=value
```
[builtin.go:118-128](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/builtin.go#L118-L128) 中的核心路由逻辑：
```go
func (t *Task) Execute(execCommands []string) ([]string, error) {
    if len(execCommands) == 0 {
        return []string{}, errors.Errorf("Instructions cannot be null")
    }
    // execCommands[0] 作为插件名查找
    if v, ok := pluginRegistry[strings.ToLower(execCommands[0])]; ok {
        res, err := v.Execute(execCommands)  // 传递整个数组给插件
        return res, err
    }
    return nil, errors.Errorf("Instruction not found")
}
```
**参数解析**（[interface.go:67-86](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/plugin/interface.go#L67-L86)）：
```go
func ParseCommands(plugin Plugin, commands []string) (map[string]string, error) {
    externalParam := map[string]string{}
    for _, c := range commands[1:] {       // 跳过 commands[0]（插件名）
        arg := strings.SplitN(c, "=", 2)   // 按 "=" 分割为 key=value
        if len(arg) != 2 { continue }
        externalParam[arg[0]] = arg[1]
    }
    // 校验必填参数、填充默认值
    pluginParam := map[string]string{}
    for key, v := range plugin.Param() {
        if v, ok := externalParam[key]; ok {
            pluginParam[key] = v; continue
        }
        if v.Required {
            return pluginParam, errors.Errorf("Missing required parameters %s", key)
        }
        pluginParam[key] = v.Default
    }
    return pluginParam, nil
}
```
**以 `K8sEnvInit` 为例**，在 [env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/env.go) 中构建的命令：
```go
Command: []string{
    "K8sEnvInit",                                                    // [0] 插件名
    "init=true",                                                     // [1] 参数
    "check=true",                                                    // [2] 参数
    "scope=time,hosts,dns,kernel,firewall,selinux,swap,...",         // [3] 参数
    "bkeConfig=namespace:bkeConfigName",                             // [4] 参数
    "extraHosts=hostname1:ip1,hostname2:ip2",                        // [5] 参数
}
```
执行时：
1. `execCommands[0]` = `"K8sEnvInit"` → 在 `pluginRegistry` 中查找 → 找到 `EnvPlugin`
2. `plugin.ParseCommands()` 将 `execCommands[1:]` 解析为 `map[string]string`：
   - `init` → `true`
   - `check` → `true`
   - `scope` → `time,hosts,dns,...`
   - `bkeConfig` → `namespace:bkeConfigName`
   - `extraHosts` → `hostname1:ip1,...`
3. `EnvPlugin.Execute()` 根据参数执行初始化和检查

**以 `Reset` 为例**：
```go
Command: []string{
    "Reset",                          // [0] 插件名
    "bkeConfig=namespace:configName", // [1] 参数
    "scope=cert,manifests,...",       // [2] 参数
    "extra=file1,dir1,ip1",           // [3] 参数
}
```
#### 2. `CommandShell` — Shell 命令拼接
**拼接规则**：`Command` 数组中的所有元素用空格连接，通过 `/bin/sh -c` 执行。

[shell.go:32-38](file:///d:/code/github/cluster-api-provider-bke/pkg/job/shell/shell.go#L32-L38)：
```go
func (t *Task) Execute(execCommands []string) ([]string, error) {
    // 将所有元素用空格拼接，交给 /bin/sh -c 执行
    s, err := t.Exec.ExecuteCommandWithOutput("/bin/sh", "-c", strings.Join(execCommands, " "))
    ...
}
```
**示例**：
```go
Command: []string{"iptables", "--table", "nat", "--list", ">", "/tmp/iptables.rule"}
```
实际执行：`/bin/sh -c "iptables --table nat --list > /tmp/iptables.rule"`
#### 3. `CommandKubernetes` — Kubernetes 资源操作
**拼接规则**：每个 `Command` 元素使用 `:` 分隔为 4 段固定格式。

[k8s.go:72-76](file:///d:/code/github/cluster-api-provider-bke/pkg/job/k8s/k8s.go#L72-L76)：
```go
// 格式: resourceType:ns/name:operator:path
ecList := strings.SplitN(ec, ":", 4)
// ecList[0] = resourceType (configmap | secret)
// ecList[1] = ns/name (资源标识)
// ecList[2] = operator (ro | rx | rw)
// ecList[3] = path (文件路径或执行标识)
```

**三种操作模式**：
- `ro`（只读）：从 K8s 读取资源写入文件 → `secret:ns/name:ro:/tmp/secret.json`
- `rx`（执行）：从 K8s 读取资源作为脚本执行 → `configmap:ns/name:rx:shell`
- `rw`（读写）：从文件读取内容写入 K8s 资源 → `configmap:ns/name:rw:/tmp/iptables.rule`
### 四、顺序执行与失败处理
[command_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go) 中的 `startTask` 方法实现了顺序执行逻辑：
```
for _, execCommand := range command.Spec.Commands {
    1. 检查停止信号
    2. 检查超时
    3. 检查是否已完成（跳过已完成的）
    4. 执行命令（支持重试）
    5. 如果失败且 BackoffIgnore=false → 停止后续执行
    6. 如果失败且 BackoffIgnore=true → 标记为 Skip，继续执行
}
```
### 五、总结流程图
```
┌──────────────────────────────────────────────────────────────┐
│                    CommandSpec.Commands[]                    │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │ ExecCommand{ID:"check", Type:BuiltIn,                   │ │
│  │   Command:["K8sEnvInit","init=true","check=true",...]}  │ │
│  ├─────────────────────────────────────────────────────────┤ │
│  │ ExecCommand{ID:"reset", Type:BuiltIn,                   │ │
│  │   Command:["Reset","bkeConfig=ns:name","scope=..."]}    │ │
│  ├─────────────────────────────────────────────────────────┤ │
│  │ ExecCommand{ID:"shell-cmd", Type:Shell,                 │ │
│  │   Command:["iptables","--table","nat","--list"]}        │ │
│  ├─────────────────────────────────────────────────────────┤ │
│  │ ExecCommand{ID:"k8s-res", Type:Kubernetes,              │ │
│  │   Command:["secret:ns/name:ro:/tmp/secret.json"]}       │ │
│  └─────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
                          │
                    按 Type 分发
           ┌──────────────┼──────────────┐
           ▼              ▼              ▼
     ┌──────────┐   ┌──────────┐   ┌──────────┐
     │ BuiltIn  │   │  Shell   │   │    K8s   │
     │          │   │          │   │          │
     │[0]=插件名│    │Join(" ") │   │Split(":")│
     │[1:]=参数 │   │→/bin/sh  │   │=4段格式  │
     │key=value │   │  -c 执行 │   │ro/rx/rw  │
     └──────────┘   └──────────┘   └──────────┘
           │              │              │
           ▼              ▼              ▼
     pluginRegistry   exec.Command   K8s Client
     插件路由执行     系统命令执行    资源操作执行
```
**核心结论**：`CommandSpec` 本身不做命令拼接，它只是定义了 `[]ExecCommand` 的有序列表。真正的"拼接"发生在 Agent 端的执行层，根据 `Type` 字段选择不同的解析策略：
- **BuiltIn**：`Command[0]` 路由到插件，`Command[1:]` 解析为 `key=value` 参数
- **Shell**：`Command` 数组用空格 Join 后交给 `/bin/sh -c` 执行
- **Kubernetes**：每个 `Command` 元素按 `:` 分割为 4 段资源操作描述

# 完整的 `pluginRegistry` 设计和 `K8sEnvInit` 插件的执行流程
## pluginRegistry 设计与 K8sEnvInit 插件执行流程
### 一、pluginRegistry 设计模式
`pluginRegistry` 采用**插件注册表模式（Plugin Registry Pattern）**，是 [builtin.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/builtin.go) 中的全局变量：
```go
var pluginRegistry = map[string]plugin.Plugin{}
```
#### 1.1 Plugin 接口定义
[interface.go:45-50](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/plugin/interface.go#L45-L50) 定义了统一的插件接口：
```go
type Plugin interface {
    Name() string                                    // 插件名称，作为路由 key
    Param() map[string]PluginParam                   // 声明支持的参数及约束
    Execute(commands []string) ([]string, error)     // 执行入口
}

type PluginParam struct {
    Key         string // 参数名
    Value       string // 可选值描述
    Required    bool   // 是否必填
    Default     string // 默认值
    Description string // 描述
}
```
#### 1.2 注册过程
在 `builtin.New()` 中，所有插件在初始化时被注册到 `pluginRegistry`：
```go
func New(exec exec.Executor, k8sClient client.Client) BuiltIn {
    t := Task{}
    c := containerd.New(exec)
    pluginRegistry[strings.ToLower(c.Name())] = c      // "containerd"
    e := env.New(exec, nil)
    pluginRegistry[strings.ToLower(e.Name())] = e       // "k8senvinit"
    cert := certs.New(k8sClient, exec, nil)
    pluginRegistry[strings.ToLower(cert.Name())] = cert // "certs"
    k := kubelet.New(nil, exec)
    pluginRegistry[strings.ToLower(k.Name())] = k       // "kubelet"
    h := ha.New(exec)
    pluginRegistry[strings.ToLower(h.Name())] = h       // "ha"
    r := reset.New()
    pluginRegistry[strings.ToLower(r.Name())] = r       // "reset"
    // ... 共注册 18 个插件
    return &t
}
```
**已注册的完整插件列表**：

| 插件名 | 实现类 | 功能 |
|--------|--------|------|
| `k8senvinit` | EnvPlugin | K8s 环境初始化与检查 |
| `reset` | ResetPlugin | 节点重置/清理 |
| `containerd` | ContainerdPlugin | Containerd 安装配置 |
| `docker` | DockerPlugin | Docker 安装配置 |
| `cri-docker` | CriDockerPlugin | cri-dockerd 安装 |
| `certs` | CertsPlugin | 证书管理 |
| `kubelet` | KubeletPlugin | Kubelet 配置 |
| `kubeadm` | KubeadmPlugin | Kubeadm 操作 |
| `ha` | HAPlugin | 高可用负载均衡部署 |
| `switchcluster` | SwitchClusterPlugin | 集群切换 |
| `downloader` | DownloaderPlugin | 文件下载 |
| `ping` | PingPlugin | 连通性检测 |
| `backup` | BackupPlugin | 备份 |
| `collect` | CollectPlugin | 信息采集 |
| `manifests` | ManifestsPlugin | 清单管理 |
| `shutdown` | ShutdownPlugin | 节点关机 |
| `selfupdate` | SelfUpdatePlugin | Agent 自更新 |
| `preprocess` | PreProcessPlugin | 前置处理脚本 |
| `postprocess` | PostProcessPlugin | 后置处理脚本 |
#### 1.3 路由机制
```go
func (t *Task) Execute(execCommands []string) ([]string, error) {
    // execCommands[0] 作为插件名，大小写不敏感
    if v, ok := pluginRegistry[strings.ToLower(execCommands[0])]; ok {
        res, err := v.Execute(execCommands)  // 整个数组传递给插件
        return res, err
    }
    return nil, errors.Errorf("Instruction not found")
}
```
#### 1.4 参数解析机制
`ParseCommands` 将 `commands[1:]` 解析为 `key=value` 参数映射，并与插件声明的 `Param()` 做校验和默认值填充：
```go
func ParseCommands(plugin Plugin, commands []string) (map[string]string, error) {
    // 1. 解析 commands[1:] 中所有 key=value
    externalParam := map[string]string{}
    for _, c := range commands[1:] {
        arg := strings.SplitN(c, "=", 2)
        if len(arg) != 2 { continue }
        externalParam[arg[0]] = arg[1]
    }
    // 2. 校验必填参数、填充默认值
    pluginParam := map[string]string{}
    for key, v := range plugin.Param() {
        if v, ok := externalParam[key]; ok {
            pluginParam[key] = v; continue
        }
        if v.Required {
            return pluginParam, errors.Errorf("Missing required parameters %s", key)
        }
        pluginParam[key] = v.Default
    }
    return pluginParam, nil
}
```
### 二、K8sEnvInit 插件执行流程
#### 2.1 插件结构
[env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/env.go) 中定义了 `EnvPlugin`：
```go
type EnvPlugin struct {
    exec        exec.Executor       // 命令执行器
    k8sClient   client.Client       // K8s 客户端
    bkeConfig   *bkev1beta1.BKEConfig  // 集群配置
    bkeConfigNS string              // 配置命名空间
    currenNode  bkenode.Node        // 当前节点信息
    nodes       bkenode.Nodes       // 集群所有节点
    sudo        string              // 是否使用 sudo
    scope       string              // 操作范围
    backup      string              // 是否备份
    extraHosts  string              // 额外 hosts
    clusterHosts []string           // 集群 hosts
    hostPort    []string            // 检查端口
    machine     *Machine            // 机器信息
}
```
#### 2.2 参数声明
```go
func (ep *EnvPlugin) Param() map[string]plugin.PluginParam {
    return map[string]plugin.PluginParam{
        "check":      {Default: "true",  Description: "是否检查环境"},
        "init":       {Default: "true",  Description: "是否初始化环境"},
        "sudo":       {Default: "true",  Description: "是否使用sudo"},
        "scope":      {Default: "kernel,firewall,selinux,swap,time,hosts,ports,image,node,httpRepo,iptables,registry",
                       Description: "操作范围"},
        "backup":     {Default: "true",  Description: "修改前是否备份"},
        "extraHosts": {Default: "",      Description: "额外hosts配置"},
        "hostPort":   {Default: "10259,10257,10250,2379,2380,2381,10248", Description: "检查端口"},
        "bkeConfig":  {Default: "",      Description: "BKE配置 ns:name"},
    }
}
```
#### 2.3 Execute 入口
```go
func (ep *EnvPlugin) Execute(commands []string) ([]string, error) {
    // 1. 解析参数
    envParamMap, err := plugin.ParseCommands(ep, commands)
    
    // 2. 加载 BKEConfig（如果提供了 bkeConfig 参数）
    if envParamMap["bkeConfig"] != "" {
        ep.bkeConfig = plugin.GetBkeConfig(envParamMap["bkeConfig"])
        clusterData := plugin.GetClusterData(envParamMap["bkeConfig"])
        ep.nodes = clusterData.Nodes
        ep.currenNode = ep.nodes.CurrentNode()
    }
    
    // 3. 执行初始化（如果 init=true）
    if envParamMap["init"] == "true" {
        ep.initK8sEnv()
    }
    
    // 4. 执行检查（如果 check=true 或 init=true）
    if envParamMap["check"] == "true" || envParamMap["init"] == "true" {
        ep.checkK8sEnv()
    }
}
```
#### 2.4 initK8sEnv — 初始化流程
[init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) 中，`initK8sEnv` 按 scope 逗号分割依次执行：
```
initK8sEnv()
  ├── 遍历 scope（逗号分割）
  │   ├── "kernel"    → initKernelParam()
  │   ├── "swap"      → initSwap()
  │   ├── "firewall"  → initFirewall()
  │   ├── "selinux"   → initSelinux()
  │   ├── "time"      → initTime()
  │   ├── "hosts"     → initHost()
  │   ├── "image"     → initImage()
  │   ├── "runtime"   → initRuntime()
  │   ├── "dns"       → initDNS()
  │   ├── "httpRepo"  → initHttpRepo()
  │   ├── "iptables"  → initIptables()
  │   ├── "registry"  → initRegistry()
  │   └── "extra"     → umask 0022
  │
  └── 如果 kernel 或 swap 发生了变更 → sysctl -p 重新加载
```
**各 scope 初始化详细逻辑**：

| Scope | 方法 | 具体操作 |
|-------|------|----------|
| `kernel` | `initKernelParam()` | 写内核参数到 `/etc/sysctl.d/k8s.conf`；加载内核模块（ip_vs 等）；配置 IPVS；设置 ulimit；针对 CentOS7+containerd 设置 `fs.may_detach_mounts` |
| `swap` | `initSwap()` | 注释 `/etc/fstab` 中 swap 行；`swapoff -a`；写入 `vm.swappiness=0` |
| `firewall` | `initFirewall()` | 停止并禁用 firewalld/ufw |
| `selinux` | `initSelinux()` | `setenforce 0`；修改 `/etc/selinux/config` 为 `SELINUX=disabled` |
| `time` | `initTime()` | 设置时区为 Asia/Shanghai |
| `hosts` | `initHost()` | 设置 hostname；解析集群节点和额外 hosts 写入 `/etc/hosts` |
| `runtime` | `initRuntime()` | 检测当前容器运行时；按需下载安装 containerd/docker/cri-dockerd |
| `dns` | `initDNS()` | 确保 `/etc/resolv.conf` 存在；CentOS 关闭 NetworkManager 自动覆盖 |
| `httpRepo` | `initHttpRepo()` | 配置 YUM/APT 软件源 |
| `iptables` | `initIptables()` | 设置 INPUT/OUTPUT/FORWARD 策略为 ACCEPT |
| `registry` | `initRegistry()` | 记录镜像仓库端口 |
| `image` | `initImage()` | 拉取所需容器镜像 |
#### 2.5 checkK8sEnv — 检查流程
[check.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/check.go) 中，`checkK8sEnv` 同样按 scope 依次检查：
```
checkK8sEnv()
  ├── 遍历 scope（逗号分割）
  │   ├── "kernel"    → checkKernelParam()   检查内核参数、文件描述符、系统模块
  │   ├── "firewall"  → checkFirewall()      检查防火墙已关闭
  │   ├── "selinux"   → checkSelinux()       检查 SELinux 已关闭
  │   ├── "swap"      → checkSwap()          检查 Swap 已关闭
  │   ├── "time"      → checkTime()          检查时间同步任务
  │   ├── "hosts"     → checkHost()          检查 hosts 文件正确性
  │   ├── "ports"     → checkHostPort()      检查端口可用性
  │   ├── "node"      → checkNodeInfo()      检查 CPU/内存资源是否满足
  │   ├── "runtime"   → checkRuntime()       检查容器运行时一致性
  │   ├── "dns"       → checkDNS()           检查 DNS 配置
  │   └── "httpRepo"  → [skip]              跳过检查
```
#### 2.6 完整调用链路（以 env.go 中构建的命令为例）
在 [env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/env.go) 中构建的 `K8sEnvInit` 命令：
```go
// 第1条指令：硬件资源检查
{
    ID: "node hardware resources check",
    Command: []string{
        "K8sEnvInit", "init=true", "check=true",
        "scope=node",                          // 只检查节点资源
        "bkeConfig=ns:configName",
    },
    Type: CommandBuiltIn,
}

// 第3条指令：完整环境初始化
{
    ID: "init and check node env",
    Command: []string{
        "K8sEnvInit", "init=true", "check=true",
        "scope=time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,runtime,iptables,registry,extra",
        "bkeConfig=ns:configName",
        "extraHosts=hostname1:ip1,hostname2:ip2",
    },
    Type: CommandBuiltIn,
}
```
**执行链路**：
```
CommandReconciler.startTask()
  └── processExecCommand()
        └── executeByType(CommandBuiltIn, command)
              └── Job.BuiltIn.Execute(["K8sEnvInit","init=true","check=true","scope=...","bkeConfig=..."])
                    └── pluginRegistry["k8senvinit"].Execute(commands)
                          └── EnvPlugin.Execute(commands)
                                ├── ParseCommands() → 解析参数
                                │     init=true, check=true, scope=time,hosts,...
                                │     bkeConfig=ns:configName, extraHosts=...
                                │
                                ├── 加载 BKEConfig
                                │     GetBkeConfig("ns:configName")
                                │     GetClusterData("ns:configName")
                                │     → 获取集群配置和节点列表
                                │     → 确定当前节点信息
                                │
                                ├── init=true → initK8sEnv()
                                │     ├── scope="time"   → initTime()     设置时区
                                │     ├── scope="hosts"   → initHost()     写hosts文件
                                │     ├── scope="dns"     → initDNS()      配置DNS
                                │     ├── scope="kernel"  → initKernelParam() 内核参数+模块
                                │     ├── scope="firewall"→ initFirewall() 关闭防火墙
                                │     ├── scope="selinux" → initSelinux()  关闭SELinux
                                │     ├── scope="swap"    → initSwap()     关闭Swap
                                │     ├── scope="httpRepo"→ initHttpRepo() 配置软件源
                                │     ├── scope="runtime" → initRuntime()  安装容器运行时
                                │     ├── scope="iptables"→ initIptables() 设置iptables
                                │     ├── scope="registry"→ initRegistry() 镜像仓库
                                │     └── scope="extra"   → umask 0022
                                │
                                └── check=true → checkK8sEnv()
                                      ├── scope="time"    → checkTime()
                                      ├── scope="hosts"   → checkHost()
                                      ├── scope="dns"     → checkDNS()
                                      ├── scope="kernel"  → checkKernelParam()
                                      ├── scope="firewall"→ checkFirewall()
                                      ├── scope="selinux" → checkSelinux()
                                      ├── scope="swap"    → checkSwap()
                                      ├── scope="httpRepo"→ [skip]
                                      ├── scope="runtime" → checkRuntime()
                                      ├── scope="iptables"→ (无检查)
                                      ├── scope="registry"→ (无检查)
                                      └── scope="extra"   → (无检查)
```
### 三、设计特点总结
1. **插件注册表解耦**：`pluginRegistry` 将命令路由与具体实现解耦，新增插件只需实现 `Plugin` 接口并在 `New()` 中注册即可
2. **参数自描述**：每个插件通过 `Param()` 声明参数约束，`ParseCommands` 统一做校验和默认值填充，实现了参数验证的标准化
3. **scope 驱动执行**：`K8sEnvInit` 通过 `scope` 参数控制初始化/检查的范围，支持灵活组合（如首次部署全量 scope，升级只执行部分 scope）
4. **init + check 双阶段**：先初始化再检查，确保初始化后的状态符合预期。`init=true` 时自动触发 `check`
5. **BKEConfig 动态加载**：通过 `bkeConfig=ns:name` 参数，插件从管理集群动态获取集群配置，实现配置与执行的分离

# 从 Command CRD 创建到 pluginRegistry 调用的完整链路
## Command 执行时调用 pluginRegistry 的完整链路
### 一、整体调用链路图
```
┌────────────────────────────────────────────────────────────────────┐
│  管理集群 (Management Cluster)                                     │
│                                                                    │
│  BKECluster Controller                                             │
│    └── ensure_nodes_env.go                                         │
│          └── command.ENV.New()                                     │
│                └── 创建 Command CRD (kubectl apply 到管理集群)     │
│                      spec.commands = [{ID, Command[], Type}]       │
│                      spec.nodeSelector = {nodeIP: nodeIP}          │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                    Watch Command CRD 变更
                               │
┌──────────────────────────────▼─────────────────────────────────────┐
│  工作节点 (Worker Node) — BKEAgent 进程内                          │
│                                                                    │
│  ① CommandReconciler.Reconcile()                                   │
│       ├── shouldReconcileCommand() → NodeName/NodeSelector 匹配    │
│       ├── ensureStatusInitialized() → 初始化 Status                │
│       ├── handleFinalizer() → 添加 finalizer                       │
│       └── createAndStartTask() → 创建 Task 并启动 goroutine        │
│                                                                    │
│  ② startTask() (goroutine)                                         │
│       └── 遍历 spec.commands[]                                     │
│             └── processExecCommand()                               │
│                   └── executeWithRetry()                           │
│                         └── executeByType(Type, Command)           │
│                                                                    │
│  ③ executeByType() — 按 Type 路由                                  │
│       ├── CommandBuiltIn  → Job.BuiltIn.Execute(Command)           │
│       ├── CommandShell    → Job.Shell.Execute(Command)             │
│       └── CommandKubernetes → Job.K8s.Execute(Command)             │
│                                                                    │
│  ④ builtin.Task.Execute() — 插件注册表路由                         │
│       └── pluginRegistry[Command[0]].Execute(Command)              │
│                                                                    │
│  ⑤ Plugin.Execute() — 具体插件执行                                 │
│       └── ParseCommands() → 解析参数 → 执行业务逻辑                │
└────────────────────────────────────────────────────────────────────┘
```
### 二、各阶段详细分析
#### 阶段①：CommandReconciler — 事件过滤与任务创建
BKEAgent 进程启动时，`CommandReconciler` 通过 `SetupWithManager` 注册对 `Command` CRD 的 Watch，并配置了 **Predicate 过滤器**：

[command_controller.go:362-377](file:///d:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go#L362-L377)：
```go
func (r *CommandReconciler) shouldReconcileCommand(o *agentv1beta1.Command, eventType string) bool {
    // 检查是否已有更新版本在执行
    if v, ok := r.Job.Task[gid]; ok {
        if o.Generation <= v.Generation { return false }
    }
    // 方式1：精确匹配 NodeName
    if o.Spec.NodeName == r.NodeName { return true }
    // 方式2：匹配 NodeSelector 中的 IP
    return r.nodeMatchNodeSelector(o.Spec.NodeSelector)
}
```
**NodeSelector 匹配机制**（[command_controller.go:711-751](file:///d:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go#L711-L751)）：
```go
func (r *CommandReconciler) nodeMatchNodeSelector(s *metav1.LabelSelector) bool {
    selector, _ := metav1.LabelSelectorAsSelector(s)
    // 1. 先用 Agent 的 NodeName 匹配
    if nodeName, found := selector.RequiresExactMatch(r.NodeName); found {
        if nodeName == r.NodeName { return true }
    }
    // 2. 再用 Agent 节点的所有网卡 IP 匹配
    ips, _ := bkenet.GetAllInterfaceIP()
    for _, p := range ips {
        tmpIP, _, _ := net.ParseCIDR(p)
        if ip, found := selector.RequiresExactMatch(tmpIP.String()); found {
            r.NodeIP = ip   // 记录匹配到的 IP
            return true
        }
    }
    return false
}
```
> **关键点**：在 [command.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/command.go) 中，`getNodeSelector` 函数将节点 IP 作为 Label 的 key 和 value：
> ```go
> func getNodeSelector(nodes bkenode.Nodes) *metav1.LabelSelector {
>     for _, node := range nodes {
>         metav1.AddLabelToSelector(nodeSelector, node.IP, node.IP)
>     }
>     return nodeSelector
> }
> ```
> 所以 NodeSelector 的格式是 `{matchLabels: {"10.0.0.1": "10.0.0.1"}}`，Agent 通过遍历自身网卡 IP 来匹配。

通过 Predicate 后，`Reconcile` 方法执行：
```go
func (r *CommandReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    command, _ := r.fetchCommand(ctx, req)        // 获取 Command 对象
    r.ensureStatusInitialized(command)             // 初始化 Status
    r.handleFinalizer(ctx, command, gid)           // 处理 Finalizer
    r.createAndStartTask(ctx, command, ...)        // 创建并启动任务
}
```
#### 阶段②：startTask — 顺序执行 ExecCommand 列表
[command_controller.go:540-575](file:///d:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go#L540-L575)：
```go
func (r *CommandReconciler) startTask(ctx context.Context, stopChan chan struct{}, command *agentv1beta1.Command) {
    currentStatus := command.Status[r.commandStatusKey()]
    stopTime := calculateStopTime(currentStatus.LastStartTime.Time, command.Spec.ActiveDeadlineSecond)

    for _, execCommand := range command.Spec.Commands {
        // 1. 检查停止信号
        select { case <-stopChan: terminated = true; default: }
        if terminated { return }

        // 2. 检查超时
        if stopTime.Before(time.Now()) { break }

        // 3. 跳过已完成的命令
        if isCommandCompleted(currentStatus.Conditions, execCommand.ID) { continue }

        // 4. 执行命令
        result := r.processExecCommand(command, execCommand, currentStatus, stopTime)
        if result.shouldBreak { break }  // 执行失败且不可跳过 → 停止
    }

    r.finalizeTaskStatus(command, currentStatus, gid)  // 统计最终状态
}
```
#### 阶段③：executeByType — 按类型路由
[command_controller.go:449-460](file:///d:/code/github/cluster-api-provider-bke/controllers/bkeagent/command_controller.go#L449-L460)：

```go
func (r *CommandReconciler) executeByType(cmdType agentv1beta1.CommandType, command []string) ([]string, error) {
    switch cmdType {
    case agentv1beta1.CommandBuiltIn:
        return r.Job.BuiltIn.Execute(command)      // → pluginRegistry
    case agentv1beta1.CommandKubernetes:
        return r.Job.K8s.Execute(command)          // → K8s 资源操作
    case agentv1beta1.CommandShell:
        return r.Job.Shell.Execute(command)        // → Shell 执行
    default:
        return nil, nil
    }
}
```
其中 `r.Job` 是在 Agent 启动时通过 `job.NewJob(client)` 初始化的：
```go
func NewJob(client client.Client) (Job, error) {
    j.BuiltIn = builtin.New(commandExec, client)  // 注册所有插件到 pluginRegistry
    j.K8s     = &k8s.Task{K8sClient: client, Exec: commandExec}
    j.Shell   = &shell.Task{Exec: commandExec}
    j.Task    = map[string]*Task{}
    return j, nil
}
```
#### 阶段④：builtin.Task.Execute — 插件注册表路由
[builtin.go:118-128](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/builtin.go#L118-L128)：
```go
func (t *Task) Execute(execCommands []string) ([]string, error) {
    if len(execCommands) == 0 {
        return []string{}, errors.Errorf("Instructions cannot be null")
    }
    // execCommands[0] = 插件名（大小写不敏感）
    if v, ok := pluginRegistry[strings.ToLower(execCommands[0])]; ok {
        res, err := v.Execute(execCommands)  // 将整个数组传递给插件
        return res, err
    }
    return nil, errors.Errorf("Instruction not found")
}
```
**核心逻辑**：
- `execCommands[0]`（如 `"K8sEnvInit"`）转为小写后作为 key 查找 `pluginRegistry`
- 找到后调用 `Plugin.Execute(execCommands)`，**整个数组**（包括插件名本身）都传递给插件
- 插件内部通过 `ParseCommands` 跳过 `commands[0]`，解析 `commands[1:]` 的 `key=value` 参数
#### 阶段⑤：Plugin.Execute — 具体插件执行
以 `K8sEnvInit` 为例，[env.go:218-255](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/env.go#L218-L255)：
```go
func (ep *EnvPlugin) Execute(commands []string) ([]string, error) {
    // 解析参数：commands[0]="K8sEnvInit" 被跳过
    // commands[1:] = ["init=true", "check=true", "scope=...", "bkeConfig=..."]
    envParamMap, err := plugin.ParseCommands(ep, commands)

    // 加载集群配置
    if envParamMap["bkeConfig"] != "" {
        ep.bkeConfig = plugin.GetBkeConfig(envParamMap["bkeConfig"])
        ep.nodes = plugin.GetClusterData(envParamMap["bkeConfig"]).Nodes
        ep.currenNode = ep.nodes.CurrentNode()
    }

    // 执行初始化
    if envParamMap["init"] == "true" { ep.initK8sEnv() }

    // 执行检查
    if envParamMap["check"] == "true" { ep.checkK8sEnv() }
}
```
### 三、状态回写机制
每条 `ExecCommand` 执行后，Agent 通过 `syncStatusUntilComplete` 将执行状态回写到 Command CRD：
```go
func (r *CommandReconciler) syncStatusUntilComplete(cmd *agentv1beta1.Command) error {
    // 从 API Server 获取最新版本（避免 Conflict）
    obj := &agentv1beta1.Command{}
    r.APIReader.Get(r.Ctx, client.ObjectKey{...}, obj)

    // 只 Patch 当前节点的 Status
    objCopy.Status[r.commandStatusKey()] = cmd.Status[r.commandStatusKey()]
    // commandStatusKey() = "NodeName/NodeIP"，如 "node1/10.0.0.1"

    r.Client.Status().Patch(r.Ctx, objCopy, client.MergeFrom(obj))
}
```
**Status 结构**是 `map[string]*CommandStatus`，key 为节点标识（`NodeName/NodeIP`），这样多个节点的 Agent 可以各自回写自己的状态，互不冲突：
```go
Status: {
    "node1/10.0.0.1": { Phase: Complete, Conditions: [...] },
    "node2/10.0.0.2": { Phase: Running, Conditions: [...] },
}
```
### 四、完整时序图
```
管理集群                          Agent(node1)                    Agent(node2)
  │                                  │                               │
  │  创建 Command CRD                │                               │
  │  spec.nodeSelector:              │                               │
  │    10.0.0.1: 10.0.0.1            │                               │
  │    10.0.0.2: 10.0.0.2            │                               │
  │  spec.commands:                  │                               │
  │    [{ID:"check",                 │                               │
  │      Command:["K8sEnvInit",...], │                               │
  │      Type:BuiltIn}]              │                               │
  │─────────────────────────────────►│                               │
  │                                  │  Watch 事件触发               │
  │                                  │  shouldReconcileCommand()     │
  │                                  │  NodeSelector 匹配 10.0.0.1 ✓ │
  │                                  │                               │
  │─────────────────────────────────────────────────────────────────►│
  │                                  │                    Watch 事件触发
  │                                  │                    NodeSelector 匹配 10.0.0.2 ✓
  │                                  │                               │
  │                                  │  startTask() goroutine        │
  │                                  │  ┌──────────────────┐         │
  │                                  │  │ 遍历 commands[]  │         │
  │                                  │  │                  │         │
  │                                  │  │ executeByType()  │         │
  │                                  │  │   BuiltIn        │         │
  │                                  │  │     ↓            │         │
  │                                  │  │ pluginRegistry   │         │
  │                                  │  │ ["k8senvinit"]   │         │
  │                                  │  │     ↓            │         │
  │                                  │  │ EnvPlugin        │         │
  │                                  │  │ .Execute()       │         │
  │                                  │  └──────────────────┘         │
  │                                  │                               │
  │  ◄─── Patch Status ───────────── │                               │
  │       Status["node1/10.0.0.1"]   │                               │
  │       = {Phase:Complete}         │                               │
  │                                  │                               │
  │   ◄────────────────────────────────────────── Patch Status ──────│
  │       Status["node2/10.0.0.2"]                                   │
  │       = {Phase:Complete}                                         │
  │                                                                  │
  │  管理集群 CheckCommandStatus()                                   │
  │  检查所有节点 Status → 全部 Complete → 命令执行完成              │
```
### 五、关键设计总结
| 设计点 | 实现方式 |
|--------|----------|
| **命令下发** | 管理集群创建 Command CRD，Agent 通过 Watch 机制感知 |
| **节点匹配** | NodeSelector 使用节点 IP 作为 Label，Agent 遍历网卡 IP 匹配 |
| **类型路由** | `executeByType` 按 `CommandType` 分发到 BuiltIn/Shell/K8s |
| **插件发现** | `pluginRegistry` 以插件名（小写）为 key，`Command[0]` 查找 |
| **参数传递** | `Command[1:]` 以 `key=value` 格式传递，`ParseCommands` 统一解析 |
| **顺序执行** | `startTask` 顺序遍历 `commands[]`，前一条失败则后续不执行（除非 `BackoffIgnore`） |
| **状态隔离** | Status 以 `NodeName/NodeIP` 为 key，多节点各自回写互不冲突 |
| **冲突处理** | `syncStatusUntilComplete` 先 Get 最新版本再 Patch，遇到 Conflict 重试 |


# 全面掌握 `env.go` 及其关联文件的设计
## `env.go` 的规格与设计思路
### 一、文件定位
[env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/env.go) 是 `K8sEnvInit` 插件的**规格定义与入口文件**，它定义了插件的身份、参数契约、数据结构和执行入口，而将具体的初始化逻辑和检查逻辑分别委托给 [init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/init.go) 和 [check.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/env/check.go)。

整个 `env` 包的文件职责划分：

| 文件 | 职责 |
|------|------|
| `env.go` | 插件规格定义（名称、参数、常量、结构体）+ 执行入口 |
| `init.go` | `initK8sEnv()` — 各 scope 的初始化实现 |
| `check.go` | `checkK8sEnv()` — 各 scope 的检查实现 |
| `machine.go` | `Machine` 结构体 — 主机信息采集 |
| `hostfile.go` | `HostsFile` 封装 — hosts 文件读写 |
| `centos.go` | CentOS 专用逻辑 — NetworkManager 配置 |
| `utils.go` | 通用工具函数 — 文件搜索/替换/备份/MD5 |
### 二、插件规格
#### 2.1 身份标识
```go
const Name = "K8sEnvInit"
```
在 `pluginRegistry` 中以 `"k8senvinit"`（小写）注册，是 `Command.Command[0]` 的路由 key。
#### 2.2 参数契约
```go
func (ep *EnvPlugin) Param() map[string]plugin.PluginParam
```

| 参数 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `init` | 否 | `"true"` | 是否执行初始化 |
| `check` | 否 | `"true"` | 是否执行检查 |
| `sudo` | 否 | `"true"` | 是否使用 sudo 执行命令 |
| `scope` | 否 | `"kernel,firewall,selinux,swap,time,hosts,runtime,image,node,ports"` | 操作范围 |
| `backup` | 否 | `"true"` | 修改文件前是否备份 |
| `extraHosts` | 否 | `""` | 额外 hosts 配置，格式 `hostname1:ip1,hostname2:ip2` |
| `hostPort` | 否 | `"10259,10257,10250,2379,2380,2381,10248"` | 需检查的端口 |
| `bkeConfig` | 否 | `""` | BKE 配置引用，格式 `ns:name` |

**设计要点**：
- 所有参数都是可选的，有合理默认值，最简调用只需 `["K8sEnvInit"]`
- `bkeConfig` 虽然非必填，但在实际部署场景中总是提供的（用于获取集群配置和节点信息）
- `scope` 是核心控制参数，决定了初始化/检查的范围
#### 2.3 scope 完整枚举
| scope | init 方法 | check 方法 | 说明 |
|-------|-----------|------------|------|
| `kernel` | `initKernelParam()` | `checkKernelParam()` | 内核参数+模块 |
| `swap` | `initSwap()` | `checkSwap()` | 关闭 Swap |
| `firewall` | `initFirewall()` | `checkFirewall()` | 关闭防火墙 |
| `selinux` | `initSelinux()` | `checkSelinux()` | 关闭 SELinux |
| `time` | `initTime()` | `checkTime()` | 时间同步 |
| `hosts` | `initHost()` | `checkHost()` | hosts 文件 |
| `runtime` | `initRuntime()` | `checkRuntime()` | 容器运行时 |
| `image` | `initImage()` | — | 拉取镜像（仅 init） |
| `node` | — | `checkNodeInfo()` | 节点资源检查（仅 check） |
| `ports` | — | `checkHostPort()` | 端口检查（仅 check） |
| `dns` | `initDNS()` | `checkDNS()` | DNS 配置 |
| `httpRepo` | `initHttpRepo()` | [skip] | 软件源配置 |
| `iptables` | `initIptables()` | — | iptables 策略（仅 init） |
| `registry` | `initRegistry()` | — | 镜像仓库（仅 init） |
| `extra` | `umask 0022` | — | 额外设置（已废弃，仅 umask） |
### 三、数据结构设计
#### 3.1 EnvPlugin 结构体
```go
type EnvPlugin struct {
    // 依赖注入
    exec      exec.Executor       // 命令执行器（系统命令）
    k8sClient client.Client       // K8s 客户端（未在 env.go 中使用）

    // 集群上下文（按需加载）
    bkeConfig   *bkev1beta1.BKEConfig  // 集群配置
    bkeConfigNS string                  // 配置命名空间标识
    currenNode  bkenode.Node            // 当前节点信息
    nodes       bkenode.Nodes           // 集群所有节点

    // 执行参数（从 Command 解析）
    sudo   string    // 是否 sudo
    scope  string    // 操作范围
    backup string    // 是否备份

    // Hosts 相关
    extraHosts   string    // 额外 hosts
    clusterHosts []string  // 集群内 hosts（从 bkeConfig 动态构建）
    hostPort     []string  // 检查端口列表

    // 主机信息
    machine *Machine    // 主机元数据
}
```
**设计思路**：
1. **依赖注入**：`exec` 和 `k8sClient` 通过 `New()` 构造函数注入，便于测试时替换为 Mock
2. **按需加载**：`bkeConfig`、`currenNode`、`nodes` 不是在 `New()` 时注入，而是在 `Execute()` 时根据 `bkeConfig` 参数动态加载。这有两个好处：
   - 插件可以在无集群配置的情况下工作（如仅做 `scope=node` 的硬件检查）
   - 确保每次执行都获取最新的集群数据
3. **参数字段化**：`sudo`、`scope`、`backup` 等从 Command 解析后存为结构体字段，供 `init.go` 和 `check.go` 中的方法直接访问，避免参数在方法间传递
#### 3.2 内核参数的三层结构
```go
// 第一层：IP 模式相关参数
var kernelParam = map[string]map[string]string{
    "ipv4": { "net.ipv4.conf.all.rp_filter": "0", ... },
    "ipv6": { "net.bridge.bridge-nf-call-ip6tables": "1", ... },
}

// 第二层：通用默认参数
var defaultKernelParam = map[string]string{
    "net.ipv4.ip_forward": "1",
    "vm.max_map_count":    "262144",
    ...
}

// 第三层：实际执行参数（合并后）
var execKernelParam = map[string]string{}
```
`init()` 函数在包加载时将三层参数合并到 `execKernelParam`：
```go
func init() {
    // 合并 ipv4 参数
    for k, v := range kernelParam[DefaultIpMode] { execKernelParam[k] = v }
    // 合并通用参数
    for k, v := range defaultKernelParam { execKernelParam[k] = v }
    // 动态添加网卡 rp_filter
    face, _ := netutil.GetV4Interface()
    execKernelParam[fmt.Sprintf("net.ipv4.conf.%s.rp_filter", face)] = "0"
}
```
**设计思路**：
- **分层合并**：IP 模式参数 → 通用参数 → 动态参数，逐层覆盖
- **全局可变**：`execKernelParam` 是全局变量，`initKernelParam()` 中还会根据运行时条件（如 CentOS7+containerd、IPVS 模式）动态添加参数
- **默认 IPv4**：`DefaultIpMode = "ipv4"`，当前只支持 IPv4，IPv6 参数已定义但未启用
#### 3.3 文件路径常量
```go
// Init 路径 = 写入路径
InitKernelConfPath  = "/etc/sysctl.d/k8s.conf"
InitSwapConfPath    = "/etc/sysctl.d/k8s-swap.conf"
InitSelinuxConfPath = "/etc/selinux/config"
InitHostConfPath    = "/etc/hosts"
InitDNSConfPath     = "/etc/resolv.conf"
...

// Check 路径 = 读取路径（部分与 Init 不同）
CheckSwapConfPath = "/proc/meminfo"       // Swap 检查读 /proc/meminfo
CheckHostConfPath = InitHostConfPath      // Host 检查读 /etc/hosts
CheckDNSConfPath  = InitDNSConfPath       // DNS 检查读 /etc/resolv.conf
```
**设计思路**：Init 和 Check 路径分开定义，因为检查的来源不一定与写入目标一致（如 Swap 写 fstab 但检查读 /proc/meminfo）。
### 四、Execute 入口设计
```go
func (ep *EnvPlugin) Execute(commands []string) ([]string, error) {
    // 1. 解析参数
    envParamMap, err := plugin.ParseCommands(ep, commands)

    // 2. 填充执行参数到结构体
    ep.sudo = envParamMap["sudo"]
    ep.scope = envParamMap["scope"]
    ep.backup = envParamMap["backup"]
    ep.extraHosts = envParamMap["extraHosts"]
    ep.hostPort = strings.Split(envParamMap["hostPort"], ",")
    ep.machine = NewMachine()

    // 3. 按需加载集群上下文
    if envParamMap["bkeConfig"] != "" {
        ep.bkeConfig = plugin.GetBkeConfig(envParamMap["bkeConfig"])
        clusterData := plugin.GetClusterData(envParamMap["bkeConfig"])
        ep.nodes = clusterData.Nodes
        ep.currenNode = ep.nodes.CurrentNode()
    }

    // 4. 先 init 后 check
    if envParamMap["init"] == "true" {
        ep.initK8sEnv()
    }
    if envParamMap["check"] == "true" || envParamMap["init"] == "true" {
        ep.checkK8sEnv()
    }
}
```
**关键设计决策**：
1. **init 隐含 check**：当 `init=true` 时，即使 `check=false`，也会执行 `checkK8sEnv()`。这确保初始化后的状态一定经过验证，是一种防御性设计。
2. **参数填充而非传递**：解析后的参数直接赋值到 `EnvPlugin` 字段，后续方法通过 `ep.scope`、`ep.backup` 等访问，避免在 `initK8sEnv → processInitScope → initXxx` 调用链中逐层传参。
3. **Machine 每次重建**：`ep.machine = NewMachine()` 在每次 `Execute` 时重新创建，确保获取最新的主机信息（CPU、内存等可能动态变化）。
### 五、scope 驱动的执行模型
`initK8sEnv` 和 `checkK8sEnv` 都采用 **scope 驱动** 的执行模型：
```
initK8sEnv()
  └── 遍历 strings.Split(ep.scope, ",")
        └── processInitScope(scope)
              ├── "kernel"   → initKernelParam()    → 返回 (err, kernelChanged=true)
              ├── "swap"     → initSwap()            → 返回 (err, kernelChanged=true)
              ├── "firewall" → initFirewall()        → 返回 (err, kernelChanged=false)
              ├── ...        → initXxx()             → 返回 (err, kernelChanged=false)
              └── default    → Warn + skip

  └── if kernelChanged → sysctl -p 重新加载
```
**设计要点**：
1. **kernelChanged 标志**：`kernel` 和 `swap` 两个 scope 会修改内核参数文件，需要 `sysctl -p` 重新加载。`processInitScope` 返回 `(error, bool)` 第二个值标识是否触发了内核变更。
2. **容错策略不一致**：
   - `kernel` scope 失败时仅 Warn 不返回错误（`log.Warnf("(ignore)init kernel parameters failed")`）
   - 其他 scope 失败时返回错误，中断执行
   - 这是因为内核参数初始化在某些环境下可能不成功但不影响后续操作
3. **processSimpleInitScope 模板方法**：对于不需要特殊处理的 scope（大多数），统一通过 `processSimpleInitScope` 执行，减少重复代码：
```go
func (ep *EnvPlugin) processSimpleInitScope(logMsg string, initFunc func() error) error {
    log.Infof(logMsg)
    return initFunc()
}
```
### 六、调用方视角 — 命令构建映射
从 [command/env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/env.go) 的调用方视角，`K8sEnvInit` 有三种典型调用场景：
#### 场景1：硬件资源检查（首次部署前）
```go
Command: ["K8sEnvInit", "init=true", "check=true", "scope=node", "bkeConfig=ns:name"]
// 仅检查节点 CPU/内存是否满足要求
```
#### 场景2：完整环境初始化（首次部署）
```go
Command: ["K8sEnvInit", "init=true", "check=true",
    "scope=time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,runtime,iptables,registry,extra",
    "bkeConfig=ns:name", "extraHosts=hostname1:ip1,hostname2:ip2"]
// 全量初始化 + 全量检查
```
#### 场景3：Containerd 重置/重部署
```go
// NewConatinerdReset
Command: ["K8sEnvInit", "init=true", "check=false",
    "scope=runtime", "bkeConfig=ns:name"]
// 仅重新初始化容器运行时

// NewConatinerdRedeploy
Command: ["K8sEnvInit", "init=true", "check=true",
    "scope=runtime", "bkeConfig=ns:name"]
// 重新初始化容器运行时 + 检查
```
### 七、设计思路总结
```
┌──────────────────────────────────────────────────────────────────────┐
│                     env.go 设计思路                                  │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  1. 规格与实现分离                                                   │
│     env.go 定义规格（名称、参数、结构体、入口）                      │
│     init.go / check.go 实现具体逻辑                                  │
│     职责清晰，避免单文件膨胀                                         │
│                                                                      │
│  2. scope 驱动执行                                                   │
│     用逗号分隔的 scope 字符串控制操作范围                            │
│     支持灵活组合：全量初始化、部分初始化、仅检查                     │
│     新增 scope 只需在 processInitScope/processCheckScope 加 case     │
│                                                                      │
│  3. init 隐含 check                                                  │
│     init=true 时自动触发 check，确保初始化结果经过验证               │
│     防御性设计，避免"初始化了但状态不对"的情况                       │
│                                                                      │
│  4. 参数字段化                                                       │
│     解析后的参数存为 EnvPlugin 字段，方法间通过 ep 访问              │
│     避免在深层调用链中逐层传参                                       │
│                                                                    │
│  5. 按需加载集群上下文                                               │
│     bkeConfig 参数驱动，无配置时也能工作（如 scope=node）            │
│     每次执行获取最新数据，避免缓存过期                               │
│                                                                      │
│  6. 内核参数三层合并                                                 │
│     IP模式参数 → 通用参数 → 运行时动态参数                           │
│     全局可变 execKernelParam，支持运行时追加                         │
│                                                                      │
│  7. kernelChanged 标志                                               │
│     跟踪内核参数变更，统一在 initK8sEnv 末尾 sysctl -p               │
│     避免每个 scope 重复执行 sysctl -p                                │
│                                                                      │
│  8. 容错分级                                                         │
│     kernel scope 失败仅 Warn（环境差异大）                           │
│     其他 scope 失败返回错误（必须成功）                              │
│     体现了不同操作的关键性差异                                       │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

# 整理出完整的 pluginRegistry 各插件的命令及节点类型执行流程
## pluginRegistry 各插件执行的命令及节点类型执行流程
### 一、插件注册表总览
在 [builtin.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/builtin.go) 中，`pluginRegistry` 是一个 `map[string]plugin.Plugin`，以插件名称（小写）为 key，插件实例为 value。命令路由机制为：`execCommands[0]` 作为插件名称，从 `pluginRegistry` 中查找对应插件并调用其 `Execute(execCommands)` 方法。
### 二、各插件详细信息
| # | 注册名(Name) | 插件类型 | 源文件路径 |
|---|---|---|---|
| 1 | `containerd` | ContainerdPlugin | containerruntime/containerd/containerd.go |
| 2 | `k8senvinit` | EnvPlugin | kubeadm/env/env.go |
| 3 | `switchcluster` | SwitchClusterPlugin | switchcluster/switch.go |
| 4 | `cert` | CertPlugin | kubeadm/certs/certs.go |
| 5 | `runkubelet` | kubeletPlugin | kubeadm/kubelet/run.go |
| 6 | `kubeadm` | KubeadmPlugin | kubeadm/kubeadm.go |
| 7 | `ha` | HA | ha/ha.go |
| 8 | `downloader` | DownloaderPlugin | downloader/downloader.go |
| 9 | `reset` | ResetPlugin | reset/reset.go |
| 10 | `ping` | Ping | ping/main.go |
| 11 | `backup` | BackupPlugin | backup/backup.go |
| 12 | `installdocker` | DockerPlugin | containerruntime/docker/docker.go |
| 13 | `collect` | CollectPlugin | collect/collect.go |
| 14 | `manifests` | ManifestPlugin | kubeadm/manifests/manifests.go |
| 15 | `shutdown` | ShutDown | shutdown/shutdown.go |
| 16 | `selfupdate` | UpdatePlugin | selfupdate/selfupdate.go |
| 17 | `installcridocker` | CRIDockerPlugin | containerruntime/cridocker/cri_docker.go |
| 18 | `preprocess` | PreprocessPlugin | preprocess/preprocess.go |
| 19 | `postprocess` | PostprocessPlugin | postprocess/postprocess.go |
### 三、各插件执行的命令及目标节点类型
#### 1. **K8sEnvInit** (注册名: `k8senvinit`)
**核心功能**：K8s 环境初始化与检查

**参数**：
- `check`: 是否检查环境
- `init`: 是否初始化环境
- `scope`: 操作范围 (`kernel,firewall,selinux,swap,time,hosts,ports,image,node,httpRepo,iptables,runtime,registry,extra,cert,manifests,container,kubelet,containerRuntime`)
- `bkeConfig`: 集群配置引用 (`ns:name`)
- `extraHosts`: 额外 hosts 映射

**在 Phase 中的调用场景**：

| 调用场景 | 命令组装位置 | 目标节点类型 | scope 内容 |
|---|---|---|---|
| **EnsureNodesEnv** (环境初始化) | [env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/env.go) `ENV.New()` | **所有节点** (Master + Worker) | ① `scope=node` (硬件检查) → ② `Reset` (重置) → ③ `scope=time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,runtime,iptables,registry,extra` |
| **Bootstrap** (节点引导) | [bootstrap.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/bootstrap.go) `Bootstrap.New()` | **Master 或 Worker** (取决于 Phase) | `scope=runtime` (仅检查容器运行时) |
| **Hosts 更新** | [hosts.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/hosts.go) `Hosts.New()` | **所有节点** | `scope=hosts` |
| **Containerd 重部署** | [env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/env.go) `ENV.NewConatinerdRedeploy()` | **指定节点** | `scope=runtime` |

**命令执行流程** (EnsureNodesEnv 场景)：
```
Step 1: K8sEnvInit init=true check=true scope=node     → 硬件资源检查
Step 2: Reset bkeConfig=ns:name scope=cert,manifests,...  → 重置旧环境
Step 3: K8sEnvInit init=true check=true scope=time,hosts,... → 初始化K8s环境
```
#### 2. **Kubeadm** (注册名: `kubeadm`)
**核心功能**：Kubeadm 引导操作（init/join/upgrade）

**参数**：
- `phase`: 操作阶段 (`InitControlPlane` / `JoinControlPlane` / `JoinWorker`)
- `bkeConfig`: 集群配置引用
- `clusterType`: 集群类型
- `backUpEtcd`: 是否备份 etcd

**在 Phase 中的调用场景**：

| 调用场景 | 命令组装位置 | 目标节点类型 | phase 值 |
|---|---|---|---|
| **EnsureMasterInit** | [bootstrap.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/bootstrap.go) | **首个 Master 节点** | `InitControlPlane` |
| **EnsureMasterJoin** | [bootstrap.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/bootstrap.go) | **新增 Master 节点** | `JoinControlPlane` |
| **EnsureWorkerJoin** | [bootstrap.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/bootstrap.go) | **Worker 节点** | `JoinWorker` |
| **EnsureMasterUpgrade** | [upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/upgrade.go) | **Master 节点** | `UpgradeControlPlane` |
| **EnsureWorkerUpgrade** | [upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/upgrade.go) | **Worker 节点** | `UpgradeWorker` |

**Bootstrap 命令执行流程**：
```
Step 1: K8sEnvInit init=false check=true scope=runtime → 检查容器运行时
Step 2: Kubeadm phase=<phase> bkeConfig=ns:name       → 执行 kubeadm init/join
```
#### 3. **HA** (注册名: `ha`)
**核心功能**：高可用负载均衡配置（HAProxy + Keepalived）

**参数**：
- `haNodes`: HA 节点列表 (`hostname1:ip1,hostname2:ip2`)
- `controlPlaneEndpointPort`: 控制面端口
- `controlPlaneEndpointVIP`: 控制面 VIP
- `ingressVIP`: Ingress VIP
- `virtualRouterId`: VRRP 虚拟路由 ID
- `thirdImageRepo` / `fuyaoImageRepo`: 镜像仓库地址
- `manifestsDir`: 静态 Pod 清单目录
- `wait`: 是否等待 VIP 就绪

**在 Phase 中的调用场景**：

| 调用场景 | 命令组装位置 | 目标节点类型 | HA 类型 |
|---|---|---|---|
| **EnsureLoadBalance** | [loadbalance.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/loadbalance.go) | **Master 节点** | Master HA (HAProxy + Keepalived) |
| **EnsureLoadBalance** | [loadbalance.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/loadbalance.go) | **Ingress 节点** | Ingress HA |

**命令执行流程**：
```
Master HA: HA haNodes=... controlPlaneEndpointPort=6443 controlPlaneEndpointVIP=x.x.x.x virtualRouterId=...
Ingress HA: HA haNodes=... ingressVIP=x.x.x.x virtualRouterId=...
```
#### 4. **Reset** (注册名: `reset`)
**核心功能**：节点重置/清理

**参数**：
- `bkeConfig`: 集群配置引用
- `scope`: 重置范围 (`cert,manifests,kubelet,container,containerRuntime,source,extra,global-cert,containerd-cfg`)
- `extra`: 额外清理项

**在 Phase 中的调用场景**：

| 调用场景 | 命令组装位置 | 目标节点类型 | scope 内容 |
|---|---|---|---|
| **EnsureNodesEnv** (环境初始化时先重置) | [env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/env.go) | **所有节点** | `cert,manifests,container,kubelet,extra` (普通) / `cert,manifests,container,kubelet,containerRuntime,extra` (深度) |
| **EnsureDeleteOrReset** (节点删除) | [cleannode.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/cleannode.go) | **指定节点** (Master/Worker) | `cert,manifests,kubelet,container,containerRuntime,source,extra,global-cert` |
| **Containerd 重置** | [env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/env.go) `NewConatinerdReset()` | **指定节点** | `containerd-cfg` |
#### 5. **Cert** (注册名: `cert`)
**核心功能**：节点级证书分发

**参数**：
- `bkeConfig`: 集群配置引用
- `nodeRole`: 节点角色

**目标节点类型**：**Master 节点** 和 **Worker 节点**（根据节点角色分发不同证书）
#### 6. **RunKubelet** (注册名: `runkubelet`)
**核心功能**：Kubelet 服务配置与启动

**参数**：
- `url`: 下载 URL
- `perm`: 文件权限
- `rename`: 重命名

**目标节点类型**：**所有节点** (Master + Worker)
#### 7. **Manifests** (注册名: `manifests`)
**核心功能**：静态 Pod 清单管理

**参数**：
- `bkeConfig`: 集群配置引用

**目标节点类型**：**Master 节点**（etcd/kube-apiserver/kube-controller/scheduler 静态 Pod）
#### 8. **SwitchCluster** (注册名: `switchcluster`)
**核心功能**：切换 Agent 监听的集群

**参数**：
- `kubeconfig`: 目标集群 kubeconfig 路径
- `clusterName`: 集群名称

**在 Phase 中的调用场景**：

| 调用场景 | 命令组装位置 | 目标节点类型 |
|---|---|---|
| **EnsureAgentSwitch** | [switchcluster.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/switchcluster.go) | **所有节点** |
#### 9. **Ping** (注册名: `ping`)
**核心功能**：Agent 心跳检测

**在 Phase 中的调用场景**：

| 调用场景 | 命令组装位置 | 目标节点类型 |
|---|---|---|
| **EnsureBKEAgent** | [ping.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/ping.go) | **所有新推送 Agent 的节点** |
#### 10. **Collect** (注册名: `collect`)
**核心功能**：证书收集

**参数**：
- `clusterName`: 集群名称
- `namespace`: 命名空间
- `k8sCertDir`: K8s 证书目录
- `etcdCertDir`: Etcd 证书目录

**在 Phase 中的调用场景**：

| 调用场景 | 命令组装位置 | 目标节点类型 |
|---|---|---|
| **EnsureCerts** (证书收集) | [collect.go](file:///d:/code/github/cluster-api-provider-bke/pkg/command/collect.go) | **Master 节点** (首个控制面节点) |
#### 11. **Backup** (注册名: `backup`)
**核心功能**：文件/目录备份

**参数**：
- `backupDirs`: 需要备份的目录列表

**目标节点类型**：**所有节点**（在环境初始化前备份关键文件）
#### 12. **Containerd** (注册名: `containerd`)
**核心功能**：Containerd 容器运行时安装与配置

**目标节点类型**：**所有节点** (Master + Worker)
#### 13. **InstallDocker** (注册名: `installdocker`)
**核心功能**：Docker 容器运行时安装

**目标节点类型**：**所有节点** (Master + Worker)
#### 14. **InstallCRIDocker** (注册名: `installcridocker`)
**核心功能**：CRI-Dockerd 安装

**目标节点类型**：**所有节点** (Master + Worker)
#### 15. **Downloader** (注册名: `downloader`)
**核心功能**：文件下载器

**目标节点类型**：**所有节点**
#### 16. **Shutdown** (注册名: `shutdown`)
**核心功能**：Agent 关闭

**在 Phase 中的调用场景**：

| 调用场景 | 目标节点类型 |
|---|---|
| **EnsureDeleteOrReset** | **所有节点** (集群删除时) |
#### 17. **SelfUpdate** (注册名: `selfupdate`)
**核心功能**：Agent 自更新

**参数**：
- `agentUrl`: Agent 下载 URL

**在 Phase 中的调用场景**：

| 调用场景 | 目标节点类型 |
|---|---|
| **EnsureProviderSelfUpgrade** | **所有节点** |
#### 18. **Preprocess** (注册名: `preprocess`)
**核心功能**：前置处理脚本执行

**目标节点类型**：**所有节点**（在环境初始化前执行自定义预处理脚本）
#### 19. **Postprocess** (注册名: `postprocess`)
**核心功能**：后置处理脚本执行

**在 Phase 中的调用场景**：

| 调用场景 | 命令组装位置 | 目标节点类型 |
|---|---|---|
| **EnsureNodesPostProcess** | [ensure_nodes_postprocess.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_postprocess.go) | **所有节点** (已完成引导的节点) |
### 四、Phase 与插件命令的完整执行流程图
```
集群创建流程 (Phase 执行顺序):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
1. EnsureCerts          → [控制面] 生成/查找证书
                          → [Master节点] Collect: 收集证书信息

2. EnsureClusterAPIObj  → [管理集群] 创建 CAPI 资源 (Cluster, KCP, MD 等)

3. EnsureBKEAgent       → [所有节点] SSH 推送 Agent
                          → [所有节点] Ping: 检测 Agent 心跳

4. EnsureNodesEnv       → [所有节点] K8sEnvInit(scope=node): 硬件检查
                          → [所有节点] Reset: 重置旧环境
                          → [所有节点] K8sEnvInit(scope=full): 环境初始化

5. EnsureLoadBalance    → [Master节点] HA: 部署 HAProxy+Keepalived

6. EnsureMasterInit     → [首个Master] K8sEnvInit(scope=runtime): 运行时检查
                          → [首个Master] Kubeadm(phase=InitControlPlane): 初始化控制面

7. EnsureMasterJoin     → [新增Master] Kubeadm(phase=JoinControlPlane): 加入控制面

8. EnsureWorkerJoin     → [Worker节点] K8sEnvInit(scope=runtime): 运行时检查
                          → [Worker节点] Kubeadm(phase=JoinWorker): 加入工作节点

9. EnsureNodesPostProcess → [所有节点] Postprocess: 执行后置脚本

10. EnsureAddonDeploy   → [管理集群] 部署 Addon

集群删除流程:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
EnsureDeleteOrReset     → [指定节点] Reset: 清理节点
                        → [所有节点] Shutdown: 关闭 Agent
```
### 五、命令分发与节点选择机制
命令通过 Kubernetes CRD (`agentv1beta1.Command`) 分发到目标节点。节点选择机制在 [command.go:537](file:///d:/code/github/cluster-api-provider-bke/pkg/command/command.go#L537) 中实现：
```go
func getNodeSelector(nodes bkenode.Nodes) *metav1.LabelSelector {
    nodeSelector := &metav1.LabelSelector{}
    for _, node := range nodes {
        metav1.AddLabelToSelector(nodeSelector, node.IP, node.IP)
    }
    return nodeSelector
}
```
每个节点的 IP 作为 Label Selector 的键值对，Agent 通过匹配自身 IP 的 Label 来决定是否执行该命令。命令还有两种标签区分作用域：
- `bke.bocloud.com/cluster-command`: 集群级命令
- `bke.bocloud.com/machine-command`: 机器级命令（含更细粒度的 `master-init`、`master-join`、`worker-join`）

# pluginRegistry 各插件安装业务流及执行的安装命令
## pluginRegistry 各插件安装业务流及执行的安装命令
### 1. K8sEnvInit 插件（环境初始化）
**注册名**: `k8senvinit` | **适用节点**: 所有节点（Master + Worker）

**命令格式**:
```
["K8sEnvInit", "init=true", "check=true", "sudo=true", "scope=kernel,firewall,selinux,swap,time,hosts,ports,image,node,httpRepo,iptables", "backup=true", "extraHosts=hostname1:ip1", "hostPort=10259,10257,10250", "bkeConfig=ns:name"]
```

**业务流（按 scope 顺序执行）**:

| Scope | 执行的命令/操作 | 说明 |
|-------|----------------|------|
| `kernel` | `modprobe ip_vs/ip_vs_wrr/ip_vs_rr/ip_vs_sh/fuse/rbd/br_netfilter`<br>写入 `/etc/sysctl.d/k8s.conf`<br>写入 `/etc/sysctl.d/k8s-swap.conf`<br>`sysctl -p /etc/sysctl.d/k8s.conf`<br>`sysctl -p /etc/sysctl.d/k8s-swap.conf`<br>Ubuntu: `echo "module" >> /etc/modules`<br>CentOS/Kylin: 写入 `/etc/sysconfig/modules/ip_vs.modules`<br>设置 ulimit: `echo '* hard nofile 65536' >> /etc/security/limits.conf`<br>CentOS7+Containerd: `fs.may_detach_mounts=1`<br>IPVS模式: `modprobe nf_conntrack` | 内核参数初始化 |
| `swap` | `sudo sed -ri 's/.*swap.*/#&/' /etc/fstab`<br>`sudo swapoff -a`<br>写入 `vm.swappiness=0` 到 `/etc/sysctl.d/k8s-swap.conf` | 关闭交换分区 |
| `firewall` | `systemctl stop firewalld`<br>`systemctl disable firewalld`<br>`systemctl stop ufw`<br>`systemctl disable ufw` | 关闭防火墙 |
| `selinux` | `sudo setenforce 0`<br>替换 `/etc/selinux/config` 中 `SELINUX=disabled` | 关闭 SELinux |
| `time` | `sudo ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime` | 时间同步 |
| `hosts` | `sudo hostnamectl set-hostname <name>`<br>写入 `/etc/hosts`（集群节点映射+镜像仓库域名映射） | 配置 hosts 文件 |
| `runtime` | Docker: 调用 `InstallDocker` 插件<br>Containerd: 调用 `InstallContainerd` 插件<br>CRIDocker: 调用 `InstallCRIDocker` 插件 | 安装容器运行时 |
| `image` | Docker: `docker pull <image>`<br>Containerd: `ctr -n k8s.io image pull <image>` | 拉取所需镜像 |
| `dns` | 创建 `/etc/resolv.conf`<br>CentOS: 配置 NetworkManager 不覆盖 resolv.conf | DNS 配置 |
| `httpRepo` | `yum update` / `apt update`（设置 HTTP 软件源） | 配置软件源 |
| `iptables` | `iptables -P INPUT ACCEPT`<br>`iptables -P OUTPUT ACCEPT`<br>`iptables -P FORWARD ACCEPT`<br>Kylin ARM: `iptables -I INPUT 1 -p vrrp -j ACCEPT` | 配置 iptables |
| `registry` | 读取镜像仓库端口配置 | 镜像仓库配置 |
| `node` | 检查节点资源（CPU/内存/磁盘） | 节点资源检查 |
| `ports` | 检查端口 10259/10257/10250/2379/2380/2381/10248 是否可用 | 端口检查 |

**Check 阶段**（init 后自动执行或单独执行）: 逐项验证上述配置是否生效。
### 2. InstallContainerd 插件（Containerd 安装）
**注册名**: `installcontainerd` | **适用节点**: 所有节点（当 CRI=containerd 时）

**命令格式**:
```
["InstallContainerd", "url=http://repo:40080/containerd-1.6.x-linux-amd64.tar.gz", "repo=cr.openfuyao.cn:443", "sandbox=cr.openfuyao.cn:443/kubernetes/pause:3.9", "runtime=runc", "dataRoot=/var/lib/containerd", "directory=/", "insecureRegistries=xxx", "containerdConfig=ns:name"]
```

**业务流**:

| 步骤 | 执行的命令/操作 |
|------|----------------|
| 1. 下载 | HTTP 下载 containerd tar.gz 到 `/tmp/containerd-<id>.tar.gz` |
| 2. 解压 | `tar -xzf <file> -C /` |
| 3. 配置（Legacy） | 写入 `/etc/containerd/config.toml`（sandbox/dataRoot/runtime等）<br>创建 `/etc/containerd/certs.d/<repo>/hosts.toml` |
| 3. 配置（CR模式） | 从 ContainerdConfig CR 读取配置：<br>- Script: 执行自定义脚本<br>- Service: 生成 containerd.service override<br>- Main: 渲染 config.toml<br>- Registry: 生成 hosts.toml |
| 4. 启动 | `systemctl enable containerd`<br>`systemctl restart containerd` |
| 5. 等待就绪 | 等待 containerd socket 可用 |
### 3. InstallDocker 插件（Docker 安装）
**注册名**: `installdocker` | **适用节点**: 所有节点（当 CRI=docker 时）

**命令格式**:
```
["InstallDocker", "runtime=runc", "dataRoot=/var/lib/docker", "cgroupDriver=systemd", "insecureRegistries=xxx", "enableDockerTls=false", "tlsHost=192.168.1.1", "runtimeUrl="]
```

**业务流**:

| 步骤 | 执行的命令/操作 |
|------|----------------|
| 1. 停止旧服务 | `systemctl stop docker`<br>`systemctl stop docker.socket` |
| 2. 下载 richrunc（可选） | 调用 Downloader 插件下载到 `/usr/local/beyondvm/runc` |
| 3. 安装 Docker | `yum install docker-ce` 或 `apt install docker-ce`<br>（若 docker-ce 不可用则安装 docker） |
| 4. 配置 daemon | 写入 `/etc/docker/daemon.json`（cgroupDriver/runtime/dataRoot/insecureRegistries/TLS） |
| 5. 启动 | `systemctl enable docker`<br>`systemctl restart docker` |
| 6. 等待就绪 | 等待 Docker daemon 可用 |
### 4. InstallCRIDocker 插件（cri-dockerd 安装）
**注册名**: `installcridocker` | **适用节点**: 所有节点（当 CRI=docker 且 K8s ≥ 1.24 时）

**命令格式**:
```
["InstallCRIDocker", "criDockerdUrl=http://repo:40080/cri-dockerd-0.3.9-amd64", "sandbox=cr.openfuyao.cn:443/kubernetes/pause:3.8"]
```

**业务流**:

| 步骤 | 执行的命令/操作 |
|------|----------------|
| 1. 停止旧服务 | `systemctl stop cri-dockerd`<br>`systemctl stop cri-dockerd.socket` |
| 2. 下载 | 调用 Downloader 插件下载 cri-dockerd 到 `/usr/bin/cri-dockerd` |
| 3. 配置 | 写入 `/etc/systemd/system/cri-dockerd.service`<br>写入 `/etc/systemd/system/cri-dockerd.socket` |
| 4. 安装依赖 | `yum install socat` / `apt install socat` |
| 5. 启动 | `systemctl daemon-reload`<br>`systemctl enable cri-dockerd`<br>`systemctl restart cri-dockerd` |
### 5. Cert 插件（证书管理）
**注册名**: `cert` | **适用节点**: 所有节点

**命令格式**:
```
["Cert", "generate=true", "loadCACert=true", "caCertNames=ca,sa,etcd,proxy", "altDNSNames=kubernetes.default", "altIPs=127.0.0.1,10.96.0.1", "certificatesDir=/etc/kubernetes/pki", "namespace=ns", "clusterName=name", "uploadCerts=true", "generateKubeConfig=true", "localKubeConfigScope=kubeconfig,controller-manager,scheduler,kubelet,kube-proxy", "loadTargetClusterCert=false", "loadAdminKubeconfig=false", "isManagerCluster=false", "tlsScope="]
```

**业务流**:

| 步骤 | 操作 |
|------|------|
| 1. 加载 CA 证书 | 从管理集群 Secret 加载 CA/SA/Etcd/Proxy CA 证书到本地 `/etc/kubernetes/pki/` |
| 2. 加载 Admin Kubeconfig | 从管理集群 Secret 加载 admin kubeconfig |
| 3. 处理证书链和全局 CA | 加载 trust-chain.crt / global-ca.crt 到 `/etc/openFuyao/certs/` |
| 4. 加载目标集群证书 | 从管理集群 Secret 加载所有已生成的证书 |
| 5. 生成证书 | 使用 CA 签发所有 K8s 所需证书（apiserver/apiserver-kubelet-client/etcd-server/...） |
| 6. 生成 KubeConfig | 生成 kubeconfig/controller-manager.conf/scheduler.conf/kubelet.conf/kube-proxy.conf |
| 7. 生成 TLS 证书 | 根据 tlsScope 生成自定义 TLS 证书 |
| 8. 上传证书 | 将生成的证书上传到管理集群 Secret |
### 6. Kubeadm 插件（集群引导）
**注册名**: `kubeadm` | **适用节点**: Master + Worker

**命令格式**:
```
["Kubeadm", "phase=initControlPlane|joinControlPlane|joinWorker|upgradeControlPlane|upgradeWorker|upgradeEtcd", "bkeConfig=ns:name", "backUpEtcd=false", "clusterType=bke", "etcdVersion=3.6.4"]
```
**业务流（按 phase）**:
#### initControlPlane（首个 Master 初始化）
| 步骤 | 操作 |
|------|------|
| 1 | 安装 kubectl 二进制文件 |
| 2 | 调用 Cert 插件生成/加载证书 |
| 3 | 调用 Manifests 插件生成静态 Pod YAML（apiserver/controller-manager/scheduler/etcd） |
| 4 | 调用 RunKubelet 插件安装并启动 kubelet |
| 5 | 上传 kubelet 配置到管理集群 ConfigMap |
| 6 | 上传用户自定义配置和全局 CA（如果是管理集群） |
#### joinControlPlane（Master 加入）
| 步骤 | 操作 |
|------|------|
| 1 | 安装 kubectl |
| 2 | 从管理集群加载证书 |
| 3 | 启动 kubelet |
| 4 | 生成静态 Pod YAML |
| 5 | 上传全局 CA（如果是管理集群） |
#### joinWorker（Worker 加入）
| 步骤 | 操作 |
|------|------|
| 1 | 从管理集群加载证书 |
| 2 | 启动 kubelet |
| 3 | 安装 kubectl |

#### upgradeControlPlane（Master 升级）
| 步骤 | 操作 |
|------|------|
| 1 | 备份 etcd（可选） |
| 2 | 备份集群配置 |
| 3 | 预拉取镜像 |
| 4 | 逐个升级组件：生成新静态 Pod YAML → 等待 Pod 就绪 |
| 5 | 升级 kubelet |
| 6 | 升级 kubectl |
#### upgradeWorker（Worker 升级）
| 步骤 | 操作 |
|------|------|
| 1 | 升级 kubelet |
| 2 | 升级 kubectl |

#### upgradeEtcd（Etcd 升级）
| 步骤 | 操作 |
|------|------|
| 1 | 备份 etcd（可选） |
| 2 | 生成新的 etcd 静态 Pod YAML |
| 3 | 等待 etcd Pod 就绪 |
### 7. RunKubelet 插件（Kubelet 安装与启动）
**注册名**: `runkubelet` | **适用节点**: 所有节点

**命令格式**:
```
["RunKubelet", "url=http://repo:40080/kubelet", "containerName=kubelet", "phase=initControlPlane", "certificatesDir=/etc/kubernetes/pki", "manifestDir=/etc/kubernetes/manifests", "imageRepo=cr.openfuyao.cn:443", "kubernetesVersion=v1.28.0", "clusterDNSDomain=cluster.local", "clusterDNSIP=10.96.0.10", "providerID=xxx", "dataRootDir=/var/lib/kubelet", "cgroupDriver=systemd", "extraArgs=key=value;key=value", "extraVolumes=hostpath:mountpath;hostpath:mountpath", "generateKubeletConfig=true", "useDeliveredConfig=false", "kubeletConfigName=xxx", "kubeletConfigNamespace=bke-system"]
```
**业务流**:

| 步骤 | 执行的命令/操作 |
|------|----------------|
| 1. 下载二进制 | 下载 kubelet 二进制文件到 `/usr/bin/kubelet` |
| 2. 生成配置（默认） | 渲染 `kubelet.conf` → `/etc/kubernetes/kubelet.conf`<br>渲染 `kubelet.service` → `/etc/systemd/system/kubelet.service` |
| 2. 生成配置（CR模式） | 从管理集群 KubeletConfig CR 读取配置 |
| 3. 拉取镜像 | 拉取 kubelet 容器镜像（pause 镜像等） |
| 4. 启动 | `systemctl daemon-reload`<br>`systemctl enable kubelet`<br>`systemctl restart kubelet` |
| 5. 等待就绪 | 检查 kubelet 服务 active 状态 |
### 8. Manifests 插件（静态 Pod 清单生成）
**注册名**: `manifests` | **适用节点**: Master 节点

**命令格式**:
```
["Manifests", "bkeConfig=ns:name", "scope=kube-apiserver,kube-controller-manager,kube-scheduler,etcd", "manifestDir=/etc/kubernetes/manifests", "etcdDataDir=/var/lib/etcd", "gpuEnable=false"]
```

**业务流**:

| 步骤 | 执行的命令/操作 |
|------|----------------|
| 1. 创建 etcd 环境 | `mkdir -p -m 700 /var/lib/etcd`<br>`useradd -r -c "etcd user" -s /sbin/nologin etcd`<br>`chown -R etcd:etcd /var/lib/etcd` |
| 2. 渲染清单 | 根据 scope 渲染静态 Pod YAML 到 `/etc/kubernetes/manifests/` |
| 3. 重启 kubelet | `systemctl restart kubelet`（使静态 Pod 生效） |
### 9. HA 插件（高可用配置）
**注册名**: `ha` | **适用节点**: Master 节点（配置 HAProxy + Keepalived）

**命令格式**:
```
["HA", "haNodes=master1:192.168.1.1,master2:192.168.1.2", "controlPlaneEndpointVIP=192.168.1.100", "controlPlaneEndpointPort=6443", "ingressVIP=192.168.1.200", "haproxyImageName=haproxy", "haproxyImageTag=2.1.4", "keepAlivedImageName=keepalived/keepalived", "keepAlivedImageTag=1.3.5", "thirdImageRepo=xxx", "fuyaoImageRepo=xxx", "manifestsDir=/etc/kubernetes/manifests", "virtualRouterId=51", "wait=false"]
```
**业务流**:

| 步骤 | 执行的命令/操作 |
|------|----------------|
| 1. 加载 IPVS | `modprobe ip_vs` |
| 2. 查找网卡 | 根据节点 IP 找到 VIP 所在网卡接口 |
| 3. 渲染清单 | Master HA: 生成 HAProxy + Keepalived 静态 Pod YAML<br>Ingress HA: 仅生成 Keepalived 静态 Pod YAML |
| 4. 等待 VIP 就绪（可选） | 轮询等待 VIP 可达 |
### 10. Reset 插件（节点重置）
**注册名**: `reset` | **适用节点**: 所有节点

**命令格式**:
```
["Reset", "bkeConfig=ns:name", "scope=cert,manifests,containerd-cfg,container,kubelet,containerRuntime,source,extra", "extra=/path/to/clean,192.168.1.100"]
```
**业务流（按 CleanPhase 顺序）**:

| Phase | 执行的命令/操作 |
|-------|----------------|
| `kubelet` | `systemctl stop kubelet`<br>`systemctl disable kubelet`<br>删除 kubelet 服务文件和数据目录 |
| `containerd-cfg` | 删除 `/etc/containerd/config.toml`<br>删除 `/etc/containerd/certs.d/` |
| `container` | 停止所有容器：`crictl stopp $(crictl pods -q)`<br>删除所有容器和镜像 |
| `containerRuntime` | `systemctl stop containerd` 或 `systemctl stop docker`<br>删除运行时数据目录 |
| `cert` | 删除 `/etc/kubernetes/pki/` 目录 |
| `manifests` | 删除 `/etc/kubernetes/manifests/` 目录 |
| `source` | 清理 HTTP 软件源配置 |
| `extra` | 删除额外指定的文件/目录/IP 地址<br>`ip addr del <ip> dev <interface>` |
| `globalCert` | 删除 `/etc/openFuyao/certs/` 目录 |
### 11. Downloader 插件（文件下载）
**注册名**: `downloader` | **适用节点**: 所有节点

**命令格式**:
```
["Downloader", "url=http://repo:40080/file", "chmod=755", "rename=kubelet", "saveto=/usr/bin"]
```

**业务流**: 下载 URL 文件 → 保存到指定目录 → 重命名 → 设置权限
### 12. SwitchCluster 插件（集群切换）
**注册名**: `switchcluster` | **适用节点**: Agent 节点

**命令格式**:
```
["SwitchCluster", "kubeconfig=ns/secret", "nodeName=node1", "clusterName=cluster1"]
```
**业务流**: 从管理集群 Secret 读取 kubeconfig → 写入本地配置文件 → 切换 Agent 连接的目标集群
### 13. Ping 插件（心跳检测）
**注册名**: `ping` | **适用节点**: 所有节点

**命令格式**: `["Ping"]`

**业务流**: 返回 `["pong", "<hostname>"]`
### 14. Backup 插件（备份）
**注册名**: `backup` | **适用节点**: Master 节点

**命令格式**:
```
["Backup", "backupDirs=/etc/kubernetes,/var/lib/etcd", "backupFiles=/etc/hosts", "saveTo=/opt/bke/backup"]
```
**业务流**: 备份指定目录和文件到目标路径
### 15. Collect 插件（证书收集）
**注册名**: `collect` | **适用节点**: Master 节点

**命令格式**:
```
["Collect", "clusterName=cluster1", "namespace=default", "k8sCertDir=/etc/kubernetes/pki", "etcdCertDir=/etc/kubernetes/pki/etcd"]
```
**业务流**: 收集本地 K8s/etcd 证书 → 上传到管理集群 Secret
### 16. Preprocess 插件（前置处理脚本）
**注册名**: `preprocess` | **适用节点**: 所有节点

**命令格式**: `["Preprocess"]` 或 `["Preprocess", "nodeIP=192.168.1.10"]`

**业务流**: 获取节点 IP → 读取配置（全局/批次/节点，互斥）→ 按优先级排序执行脚本
### 17. Postprocess 插件（后置处理脚本）
**注册名**: `postprocess` | **适用节点**: 所有节点

**命令格式**: `["Postprocess"]` 或 `["Postprocess", "nodeIP=192.168.1.10"]`

**业务流**: 与 Preprocess 相同机制，在集群操作完成后执行
### 18. SelfUpdate 插件（Agent 自更新）
**注册名**: `selfupdate` | **适用节点**: 所有节点

**命令格式**:
```
["SelfUpdate", "agentUrl=http://repo:40080/bkeagent-latest-linux-amd64"]
```

**业务流**: 检查版本号 → 下载新版本 → 执行 `nohup update.sh /path/to/bkeagent &` 后台替换并重启
### 19. Shutdown 插件（Agent 关闭）
**注册名**: `shutdown` | **适用节点**: 所有节点

**命令格式**: `["Shutdown"]`

**业务流**: 调用 `os.Exit(0)` 退出 Agent 进程
### 整体安装流程（按集群部署阶段）
```
┌─────────────────────────────────────────────────────────────────────┐
│                    BKECluster 部署完整流程                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Phase 1: ensure_certs (控制面)                                     │
│    └─ Cert 插件 → 生成/加载所有证书和 KubeConfig                     │
│                                                                     │
│  Phase 2: ensure_nodes_env (所有节点)                                │
│    └─ K8sEnvInit 插件                                               │
│        ├─ kernel: 内核参数 + 模块加载                                │
│        ├─ swap: 关闭交换分区                                         │
│        ├─ firewall: 关闭防火墙                                       │
│        ├─ selinux: 关闭 SELinux                                      │
│        ├─ time: 时间同步                                             │
│        ├─ hosts: 配置 /etc/hosts                                     │
│        ├─ runtime: → InstallContainerd / InstallDocker / InstallCRIDocker │
│        ├─ image: 拉取镜像                                            │
│        ├─ httpRepo: 配置软件源                                       │
│        ├─ iptables: 配置 iptables                                    │
│        └─ check: 验证所有配置                                        │
│                                                                     │
│  Phase 3: ensure_load_balance (Master 节点)                         │
│    └─ HA 插件 → HAProxy + Keepalived 静态 Pod                       │
│                                                                     │
│  Phase 4: ensure_master_init (首个 Master)                          │
│    └─ Kubeadm(phase=initControlPlane)                               │
│        ├─ Cert → 加载/生成证书                                       │
│        ├─ Manifests → 生成静态 Pod YAML                              │
│        ├─ RunKubelet → 安装并启动 kubelet                            │
│        └─ 上传配置到管理集群                                          │
│                                                                     │
│  Phase 5: ensure_master_join (其他 Master)                          │
│    └─ Kubeadm(phase=joinControlPlane)                               │
│        ├─ Cert → 加载证书                                            │
│        ├─ RunKubelet → 启动 kubelet                                  │
│        └─ Manifests → 生成静态 Pod YAML                              │
│                                                                     │
│  Phase 6: ensure_worker_join (Worker 节点)                          │
│    └─ Kubeadm(phase=joinWorker)                                     │
│        ├─ Cert → 加载证书                                            │
│        ├─ RunKubelet → 启动 kubelet                                  │
│        └─ 安装 kubectl                                               │
│                                                                     │
│  Phase 7: ensure_bke_agent (所有节点)                                │
│    └─ SelfUpdate → 更新 Agent 版本                                   │
│                                                                     │
│  Phase 8: ensure_delete_or_reset (删除/重置节点)                     │
│    └─ Reset 插件 → 逐 Phase 清理节点                                 │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

# Kubelet 和 Kubectl 的下载来源
## Kubelet 和 Kubectl 的下载来源
### 1. Kubelet 下载来源
**入口**: [command.go:installKubeletCommand](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/command.go) → [run.go:downloadAndInstallKubeletBinary](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubelet/run.go)

**URL 构建链路**:
```
BkeConfig.YumRepo()
  → "http://{HTTPRepo.Domain}:{HTTPRepo.Port}"     // 例如: http://http.bocloud.k8s:40080

GetCustomDownloadPath(yumRepo)
  → yumRepo + "/files"                               // 例如: http://http.bocloud.k8s:40080/files

最终 URL:
  → "{baseUrl}/kubelet-{k8sVersion}-{arch}"          // 例如: http://http.bocloud.k8s:40080/files/kubelet-v1.28.0-amd64
```

**关键代码** ([command.go:133-139](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/command.go)):
```go
kubeletUrl := bkesource.GetCustomDownloadPath(cfg.YumRepo())
kubelet := fmt.Sprintf("kubelet-%s-%s", k8sVersion, hostArch)
kubeletUrl = fmt.Sprintf("%s/%s", kubeletUrl, kubelet)
```

**下载执行** ([run.go:571-590](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubelet/run.go)):

```go
func (kp *kubeletPlugin) downloadAndInstallKubeletBinary(commandMap map[string]string) error {
    url := commandMap["url"]       // http://http.bocloud.k8s:40080/files/kubelet-v1.28.0-amd64
    rename := commandMap["rename"] // "kubelet"
    saveto := commandMap["saveto"] // "/usr/bin"
    chmod := commandMap["chmod"]   // "755"

    // 先停止旧的 kubelet 服务
    kp.exec.ExecuteCommandWithCombinedOutput("sh", "-c", "systemctl stop kubelet")
    // 删除旧的二进制
    kp.exec.ExecuteCommandWithCombinedOutput("sh", "-c", "rm -rf /usr/bin/kubelet")
    // 通过 HTTP 下载新二进制
    return download.ExecDownload(url, saveto, rename, chmod)
}
```
**download.ExecDownload** 使用 `http.Get(url)` 直接通过 HTTP GET 下载文件，保存到 `/usr/bin/kubelet`，权限设为 `755`。
### 2. Kubectl 下载来源
**入口**: [command.go:installKubectlCommand](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/command.go)

**URL 构建链路**（与 kubelet 完全相同的模式）:
```
BkeConfig.YumRepo()
  → "http://{HTTPRepo.Domain}:{HTTPRepo.Port}"

GetCustomDownloadPath(yumRepo)
  → yumRepo + "/files"

最终 URL:
  → "{baseUrl}/kubectl-{k8sVersion}-{arch}"
```

**关键代码** ([command.go:198-204](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/command.go)):
```go
kubectlUrl := bkesource.GetCustomDownloadPath(cfg.YumRepo())
kubectl := fmt.Sprintf("kubectl-%s-%s", k8sVersion, hostArch)
kubectlUrl = fmt.Sprintf("%s/%s", kubectlUrl, kubectl)
```

**下载执行**（直接调用 Downloader 插件）:
```go
command := []string{
    "Downloader",
    fmt.Sprintf("url=%s", kubectlUrl),  // http://http.bocloud.k8s:40080/files/kubectl-v1.28.0-amd64
    "rename=kubectl",
    "saveto=/usr/bin",
    "chmod=755",
}
dp := downloadPlugin.New()
dp.Execute(command)
```
### 3. 关于 kubeadm 二进制
**重要发现**: 在整个 Kubeadm 插件中，**并没有下载 kubeadm 二进制文件**。Kubeadm 插件并不使用 `kubeadm` CLI 工具，而是**直接在代码中实现了 kubeadm 的功能**：
- 证书生成：调用 Cert 插件（基于 Go crypto 库）
- 静态 Pod YAML 生成：调用 Manifests 插件（基于 Go template 渲染）
- Kubelet 启动：调用 RunKubelet 插件
- KubeConfig 生成：直接用 Go 代码生成

这与传统 `kubeadm init/join` 的方式不同——BKE **不依赖 kubeadm CLI**，而是用自己的插件体系重新实现了 kubeadm 的核心功能。
### 4. 下载来源汇总
| 二进制 | URL 模板 | 示例 | 下载方式 |
|--------|----------|------|----------|
| **kubelet** | `http://{HTTPRepo.Domain}:{HTTPRepo.Port}/files/kubelet-{version}-{arch}` | `http://http.bocloud.k8s:40080/files/kubelet-v1.28.0-amd64` | `download.ExecDownload` (HTTP GET) |
| **kubectl** | `http://{HTTPRepo.Domain}:{HTTPRepo.Port}/files/kubectl-{version}-{arch}` | `http://http.bocloud.k8s:40080/files/kubectl-v1.28.0-amd64` | Downloader 插件 (HTTP GET) |
| **kubeadm** | ❌ 不下载 | — | 功能由插件代码直接实现，无需 CLI |

**核心依赖**: 所有下载都依赖 `BkeConfig.Cluster.HTTPRepo` 配置（默认 `http.bocloud.k8s:40080`），这是一个**内部 HTTP 文件服务器**，存放了预编译的二进制文件。

# 基于 kubeadm SDK 重构的详细设计方案
## 基于 kubeadm SDK 重构的详细设计方案
### 一、重构总体架构
```
┌─────────────────────────────────────────────────────────────────────────┐
│                        BKE Kubeadm Plugin (重构后)                       │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                   Phase Orchestrator (编排层)                    │    │
│  │  • initControlPlane / joinControlPlane / joinWorker             │    │
│  │  • upgradeControlPlane / upgradeWorker / upgradeEtcd            │    │
│  │  • 保留现有流程编排逻辑，调用下层 Phase 实现                       │    │
│  └────────────────────────┬────────────────────────────────────────┘    │
│                           │                                             │
│  ┌────────────────────────┼────────────────────────────────────────┐    │
│  │                   Phase Implementation (实现层)                   │    │
│  │                        │                                         │    │
│  │  ┌─────────────────────┼──────────────────────────┐             │    │
│  │  │  kubeadm SDK Phase  │   BKE 自研 Phase          │             │    │
│  │  │  (标准 K8s 能力)    │   (差异化能力)             │             │    │
│  │  │                     │                            │             │    │
│  │  │  • CertsPhase       │   • EtcdStaticPodPhase     │             │    │
│  │  │  • KubeConfigPhase  │   • HAProxyPhase           │             │    │
│  │  │  • ControlPlanePhase│   • KeepalivedPhase        │             │    │
│  │  │  • KubeletPhase     │   • EnvInitPhase           │             │    │
│  │  │                     │   • PreProcessPhase        │             │    │
│  │  │                     │   • PostProcessPhase       │             │    │
│  │  │                     │   • AgentUpdatePhase       │             │    │
│  │  │                     │   • ResetPhase             │             │    │
│  │  └─────────────────────┴──────────────────────────┘             │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                   Config Adapter (配置适配层)                     │    │
│  │  BkeConfig ──► kubeadmapi.InitConfiguration                     │    │
│  │  BkeConfig ──► kubeadmapi.ClusterConfiguration                  │    │
│  │  BkeConfig ──► kubeadmapi.JoinConfiguration                     │    │
│  │  BootScope ──► kubeadmapi.NodeRegistrationOptions               │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                   Feature Gate (特性开关层)                       │    │
│  │  • UseKubeadmCerts=true/false      (证书生成)                    │    │
│  │  • UseKubeadmKubeConfig=true/false (KubeConfig 生成)            │    │
│  │  • UseKubeadmControlPlane=true/false (Static Pod 渲染)          │    │
│  │  • UseKubeadmKubelet=true/false    (Kubelet 配置生成)           │    │
│  └─────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────┘
```
### 二、核心模块详细设计
#### 2.1 Config Adapter — 配置适配层
**目标**：将 BKE 自有的 `BKEConfig` / `BootScope` 转换为 kubeadm SDK 所需的标准类型。

**新文件**：`pkg/job/builtin/kubeadm/adapter/config.go`
```go
package adapter

import (
    kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
    kubeadmscheme "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/scheme"
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
    bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/mfutil"
)

type ConfigAdapter struct {
    BkeConfig *bkev1beta1.BKEConfig
    BootScope *mfutil.BootScope
}

func NewConfigAdapter(cfg *bkev1beta1.BKEConfig, scope *mfutil.BootScope) *ConfigAdapter {
    return &ConfigAdapter{BkeConfig: cfg, BootScope: scope}
}

// ToInitConfiguration 将 BkeConfig 转换为 kubeadm InitConfiguration
// 用于 initControlPlane 阶段
func (a *ConfigAdapter) ToInitConfiguration() (*kubeadmapi.InitConfiguration, error) {
    cfg := bkeinit.BkeConfig(*a.BkeConfig)
    initCfg := &kubeadmapi.InitConfiguration{
        TypeMeta: metav1.TypeMeta{
            APIVersion: "kubeadm.k8s.io/v1beta3",
            Kind:       "InitConfiguration",
        },
        NodeRegistration: kubeadmapi.NodeRegistrationOptions{
            Name:             a.BootScope.HostName,
            CRISocket:        a.detectCRISocket(),
            KubeletExtraArgs: a.parseKubeletExtraArgs(),
        },
        LocalAPIEndpoint: kubeadmapi.APIEndpoint{
            AdvertiseAddress: a.BootScope.HostIP,
            BindPort:         int32(cfg.Cluster.APIServer.Port),
        },
    }

    return initCfg, nil
}

// ToClusterConfiguration 将 BkeConfig 转换为 kubeadm ClusterConfiguration
// 用于证书生成、Static Pod 渲染等阶段
func (a *ConfigAdapter) ToClusterConfiguration() (*kubeadmapi.ClusterConfiguration, error) {
    cfg := bkeinit.BkeConfig(*a.BkeConfig)
    clusterCfg := &kubeadmapi.ClusterConfiguration{
        TypeMeta: metav1.TypeMeta{
            APIVersion: "kubeadm.k8s.io/v1beta3",
            Kind:       "ClusterConfiguration",
        },
        Networking: kubeadmapi.Networking{
            ServiceSubnet: cfg.Cluster.Networking.ServiceSubnet,
            PodSubnet:     cfg.Cluster.Networking.PodSubnet,
            DNSDomain:     cfg.Cluster.Networking.DNSDomain,
        },
        KubernetesVersion: cfg.Cluster.KubernetesVersion,
        ControlPlaneEndpoint: cfg.Cluster.ControlPlaneEndpoint,
        APIServer: kubeadmapi.APIServer{
            ControlPlaneComponent: kubeadmapi.ControlPlaneComponent{
                ExtraArgs: a.buildAPIServerExtraArgs(),
                ExtraVolumes: a.buildAPIServerExtraVolumes(),
            },
            CertSANs: a.buildAPIServerSANs(),
        },
        ControllerManager: kubeadmapi.ControlPlaneComponent{
            ExtraArgs:    a.buildControllerExtraArgs(),
            ExtraVolumes: a.buildControllerExtraVolumes(),
        },
        Scheduler: kubeadmapi.ControlPlaneComponent{
            ExtraArgs:    a.buildSchedulerExtraArgs(),
            ExtraVolumes: a.buildSchedulerExtraVolumes(),
        },
        Etcd: kubeadmapi.Etcd{
            Local: &kubeadmapi.LocalEtcd{
                DataDir:       cfg.Cluster.Etcd.DataDir,
                ExtraArgs:     a.buildEtcdExtraArgs(),
                ServerCertSANs: a.buildEtcdSANs(),
                PeerCertSANs:   a.buildEtcdSANs(),
            },
        },
        CertificatesDir: cfg.Cluster.CertificatesDir,
        ImageRepository: cfg.ImageFuyaoRepo(),
    }

    return clusterCfg, nil
}

// ToJoinConfiguration 将 BkeConfig 转换为 kubeadm JoinConfiguration
// 用于 joinControlPlane / joinWorker 阶段
func (a *ConfigAdapter) ToJoinConfiguration(controlPlane bool) (*kubeadmapi.JoinConfiguration, error) {
    cfg := bkeinit.BkeConfig(*a.BkeConfig)
    joinCfg := &kubeadmapi.JoinConfiguration{
        TypeMeta: metav1.TypeMeta{
            APIVersion: "kubeadm.k8s.io/v1beta3",
            Kind:       "JoinConfiguration",
        },
        NodeRegistration: kubeadmapi.NodeRegistrationOptions{
            Name:             a.BootScope.HostName,
            CRISocket:        a.detectCRISocket(),
            KubeletExtraArgs: a.parseKubeletExtraArgs(),
        },
        Discovery: kubeadmapi.Discovery{
            BootstrapToken: &kubeadmapi.BootstrapTokenDiscovery{
                APIServerEndpoint: a.getControlPlaneEndpoint(),
            },
        },
    }

    if controlPlane {
        joinCfg.ControlPlane = &kubeadmapi.JoinControlPlane{
            LocalAPIEndpoint: kubeadmapi.APIEndpoint{
                AdvertiseAddress: a.BootScope.HostIP,
                BindPort:         int32(cfg.Cluster.APIServer.Port),
            },
        }
    }

    return joinCfg, nil
}
```
**关键设计决策**：

| 转换项 | BKE 自有 | kubeadm 标准 | 适配策略 |
|--------|---------|-------------|---------|
| 证书目录 | `cfg.Cluster.CertificatesDir` | `ClusterConfiguration.CertificatesDir` | 直接映射 |
| 镜像仓库 | `cfg.ImageFuyaoRepo()` | `ClusterConfiguration.ImageRepository` | 直接映射 |
| etcd 配置 | 外部 Static Pod | `ClusterConfiguration.Etcd.Local` | BKE 使用外部 etcd，但仍映射为 Local（因为 Static Pod 部署方式与 kubeadm stacked etcd 的证书结构一致） |
| APIServer SAN | 自研 `BKECertAPIServer().Config.AltNames` | `ClusterConfiguration.APIServer.CertSANs` | 合并自研 SAN + kubeadm 标准 SAN |
| Kubelet ExtraArgs | 字符串 `"key=value;key=value"` | `map[string]string` | 解析转换 |
#### 2.2 CertsPhase — 证书生成重构
**目标**：使用 kubeadm SDK 的 `phases/certs` 替代自研 `pkiutil`，同时保留 BKE 特有的证书（如 `tls-server`、`tls-client`、Global CA）。

**新文件**：`pkg/job/builtin/kubeadm/phase/certs.go`
```go
package phase

import (
    "k8s.io/kubernetes/cmd/kubeadm/app/phases/certs"
    kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
    kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/kubeadm/adapter"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/pkiutil"
)

type CertsPhase struct {
    adapter    *adapter.ConfigAdapter
    pkiDir     string
    featureGates map[string]bool
}

func NewCertsPhase(adapter *adapter.ConfigAdapter, pkiDir string) *CertsPhase {
    return &CertsPhase{
        adapter: adapter,
        pkiDir:  pkiDir,
        featureGates: map[string]bool{
            "UseKubeadmCerts": true,
        },
    }
}

// Execute 执行证书生成
// 如果 UseKubeadmCerts=true，使用 kubeadm SDK 生成标准证书
// 如果 UseKubeadmCerts=false，回退到自研 pkiutil
func (p *CertsPhase) Execute() error {
    if p.featureGates["UseKubeadmCerts"] {
        return p.executeWithKubeadmSDK()
    }
    return p.executeWithBKE()
}

func (p *CertsPhase) executeWithKubeadmSDK() error {
    // Step 1: 转换配置
    initCfg, err := p.adapter.ToInitConfiguration()
    if err != nil {
        return err
    }
    clusterCfg, err := p.adapter.ToClusterConfiguration()
    if err != nil {
        return err
    }

    // Step 2: 使用 kubeadm SDK 生成标准证书
    // certs.CreatePKIAssets 会生成以下证书：
    //   - ca.crt/ca.key (Root CA)
    //   - apiserver.crt/apiserver.key (API Server serving cert)
    //   - apiserver-kubelet-client.crt/key (API Server -> Kubelet client cert)
    //   - front-proxy-ca.crt/key (Front Proxy CA)
    //   - front-proxy-client.crt/key (Front Proxy Client cert)
    //   - etcd/ca.crt/key (Etcd CA)
    //   - etcd/server.crt/key (Etcd Server cert)
    //   - etcd/peer.crt/key (Etcd Peer cert)
    //   - etcd/healthcheck-client.crt/key (Etcd Healthcheck cert)
    //   - apiserver-etcd-client.crt/key (API Server -> Etcd client cert)
    //   - sa.key/sa.pub (Service Account key pair)
    cfg := &kubeadmapi.InitConfiguration{
        ClusterConfiguration: *clusterCfg,
    }
    cfg.CertificatesDir = p.pkiDir

    if err := certs.CreatePKIAssets(cfg, ""); err != nil {
        return err
    }

    // Step 3: 生成 BKE 特有的证书（kubeadm 不生成的）
    // - tls-server.crt/key (controller-manager/scheduler 的 TLS 证书)
    // - tls-client.crt/key (TLS 客户端证书)
    // - ca-chain.crt (CA 证书链)
    if err := p.generateBKEExtraCerts(); err != nil {
        return err
    }

    return nil
}

func (p *CertsPhase) executeWithBKE() error {
    // 回退到原有 pkiutil 逻辑
    certList := pkiutil.GetDefaultCertList()
    certList.SetPkiPath(p.pkiDir)

    for _, cert := range certList {
        if cert.CAName == "" {
            if err := pkiutil.GenerateCACert(cert); err != nil {
                return err
            }
        } else {
            caCert := certList.ByName(cert.CAName)
            if caCert == nil {
                return fmt.Errorf("CA cert %q not found", cert.CAName)
            }
            if err := pkiutil.GenerateCertWithCA(cert, caCert); err != nil {
                return err
            }
        }
    }
    return nil
}

// generateBKEExtraCerts 生成 BKE 特有的证书
func (p *CertsPhase) generateBKEExtraCerts() error {
    // tls-server 证书：controller-manager 和 scheduler 的 TLS 证书
    // 这是 BKE 特有的，kubeadm 不生成
    tlsServerCert := pkiutil.BKETlsServerConfig()
    tlsServerCert.PkiPath = p.pkiDir
    caCert := pkiutil.BKECertRootCA()
    caCert.PkiPath = p.pkiDir
    if err := pkiutil.GenerateCertWithCA(tlsServerCert, caCert); err != nil {
        return err
    }

    // tls-client 证书
    tlsClientCert := pkiutil.BKETlsClientConfig()
    tlsClientCert.PkiPath = p.pkiDir
    if err := pkiutil.GenerateCertWithCA(tlsClientCert, caCert); err != nil {
        return err
    }

    // CA 证书链
    if err := p.generateCertChain(); err != nil {
        return err
    }

    return nil
}
```
**kubeadm SDK 证书生成 vs 自研对比**：

| 证书 | kubeadm SDK | 自研 pkiutil | 差异 |
|------|------------|-------------|------|
| ca.crt/key | ✅ 自动生成 | ✅ 手动实现 | SAN 一致性由 SDK 保证 |
| apiserver.crt/key | ✅ 自动生成，含标准 SAN | ✅ 手动实现，SAN 硬编码 | SDK 自动添加 `kubernetes.default.svc.{domain}` |
| apiserver-kubelet-client | ✅ | ✅ | 一致 |
| front-proxy-ca/client | ✅ | ✅ | 一致 |
| etcd 全套 | ✅ | ✅ | SDK 自动根据节点 IP 生成 SAN |
| sa.key/sa.pub | ✅ | ✅ | 一致 |
| tls-server | ❌ kubeadm 不生成 | ✅ | BKE 特有，需自研补充 |
| tls-client | ❌ | ✅ | BKE 特有，需自研补充 |
| ca-chain.crt | ❌ | ✅ | BKE 特有，需自研补充 |
#### 2.3 KubeConfigPhase — KubeConfig 生成重构
**目标**：使用 kubeadm SDK 的 `kubeconfig` Phase 替代自研的 kubeconfig 生成逻辑。

**新文件**：`pkg/job/builtin/kubeadm/phase/kubeconfig.go`
```go
package phase

import (
    kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
    kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util/kubeconfig"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/kubeadm/adapter"
)

type KubeConfigPhase struct {
    adapter      *adapter.ConfigAdapter
    pkiDir       string
    kubernetesDir string
}

func NewKubeConfigPhase(adapter *adapter.ConfigAdapter, pkiDir, kubernetesDir string) *KubeConfigPhase {
    return &KubeConfigPhase{
        adapter:       adapter,
        pkiDir:        pkiDir,
        kubernetesDir: kubernetesDir,
    }
}

// Execute 生成所有标准 kubeconfig 文件
// kubeadm SDK 会生成以下 kubeconfig：
//   - admin.conf
//   - kubelet.conf
//   - controller-manager.conf
//   - scheduler.conf
func (p *KubeConfigPhase) Execute() error {
    initCfg, err := p.adapter.ToInitConfiguration()
    if err != nil {
        return err
    }
    clusterCfg, err := p.adapter.ToClusterConfiguration()
    if err != nil {
        return err
    }

    cfg := &kubeadmapi.InitConfiguration{
        ClusterConfiguration: *clusterCfg,
    }
    cfg.CertificatesDir = p.pkiDir

    // 使用 kubeadm SDK 生成 admin.conf
    // 内部会读取 CA 证书和密钥，生成正确的 kubeconfig
    if err := kubeadmutil.CreateKubeconfigFile(
        kubeadmconstants.AdminKubeConfigFileName,
        p.kubernetesDir,
        cfg,
    ); err != nil {
        return err
    }

    // 生成 controller-manager.conf
    if err := kubeadmutil.CreateKubeconfigFile(
        kubeadmconstants.ControllerManagerKubeConfigFileName,
        p.kubernetesDir,
        cfg,
    ); err != nil {
        return err
    }

    // 生成 scheduler.conf
    if err := kubeadmutil.CreateKubeconfigFile(
        kubeadmconstants.SchedulerKubeConfigFileName,
        p.kubernetesDir,
        cfg,
    ); err != nil {
        return err
    }

    // 生成 kubelet.conf
    if err := kubeadmutil.CreateKubeconfigFile(
        kubeadmconstants.KubeletKubeConfigFileName,
        p.kubernetesDir,
        cfg,
    ); err != nil {
        return err
    }

    return nil
}
```
**对比当前实现**：

当前 [certs.go:handleGenerateKubeConfig](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/certs/certs.go) 中的 kubeconfig 生成逻辑：
- 自行构建 `clientcmdapi.Config` 对象
- 手动设置 Cluster、Context、AuthInfo
- 需要手动处理 Worker 节点和 Master 节点的 server 地址差异

重构后：
- 直接调用 `kubeadmutil.CreateKubeconfigFile`，自动处理所有细节
- Worker 节点的 server 地址差异在 `ConfigAdapter.ToClusterConfiguration` 中处理
#### 2.4 ControlPlanePhase — Static Pod 渲染重构
**目标**：使用 kubeadm SDK 的 `controlplane` Phase 替代自研的 Go 模板渲染。

**新文件**：`pkg/job/builtin/kubeadm/phase/controlplane.go`
```go
package phase

import (
    "k8s.io/kubernetes/cmd/kubeadm/app/phases/controlplane"
    kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
    kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/kubeadm/adapter"
)

type ControlPlanePhase struct {
    adapter     *adapter.ConfigAdapter
    manifestsDir string
}

func NewControlPlanePhase(adapter *adapter.ConfigAdapter, manifestsDir string) *ControlPlanePhase {
    return &ControlPlanePhase{
        adapter:      adapter,
        manifestsDir: manifestsDir,
    }
}

// Execute 渲染控制平面 Static Pod YAML
// kubeadm SDK 会生成以下 Static Pod：
//   - kube-apiserver.yaml
//   - kube-controller-manager.yaml
//   - kube-scheduler.yaml
func (p *ControlPlanePhase) Execute() error {
    initCfg, err := p.adapter.ToInitConfiguration()
    if err != nil {
        return err
    }
    clusterCfg, err := p.adapter.ToClusterConfiguration()
    if err != nil {
        return err
    }

    cfg := &kubeadmapi.InitConfiguration{
        ClusterConfiguration: *clusterCfg,
    }
    cfg.CertificatesDir = p.adapter.BkeConfig.Cluster.CertificatesDir

    // 使用 kubeadm SDK 渲染 API Server Static Pod
    if err := controlplane.CreateAPIServerStaticPodManifest(
        p.manifestsDir,
        cfg,
        false, // 不等待
    ); err != nil {
        return err
    }

    // 渲染 Controller Manager Static Pod
    if err := controlplane.CreateControllerManagerStaticPodManifest(
        p.manifestsDir,
        cfg,
        false,
    ); err != nil {
        return err
    }

    // 渲染 Scheduler Static Pod
    if err := controlplane.CreateSchedulerStaticPodManifest(
        p.manifestsDir,
        cfg,
        false,
    ); err != nil {
        return err
    }

    return nil
}
```
**关键差异处理**：

当前自研模板与 kubeadm SDK 的差异点及适配策略：

| 差异点 | 当前自研模板 | kubeadm SDK | 适配策略 |
|--------|-----------|------------|---------|
| APIServer 审计日志 | 硬编码 `--audit-log-path=/var/log/openFuyao/apiserver/` | 不包含审计参数 | 在 `ExtraArgs` 中添加审计参数 |
| APIServer profiling | `--profiling=false` | 默认不设置 | 在 `ExtraArgs` 中添加 |
| APIServer TLS Cipher | 硬编码 `--tls-cipher-suites=...` | 使用默认值 | 在 `ExtraArgs` 中添加 |
| APIServer Webhook | 条件添加 `--authentication-token-webhook-config-file` | 不包含 | 在 `ExtraArgs` 中条件添加 |
| Controller Manager bind-address | `--bind-address={{ .HostIP }}` | 默认 `0.0.0.0` | 在 `ExtraArgs` 中覆盖 |
| Controller Manager TLS | 使用 `tls-server.crt/key` | 使用组件专用证书 | **需要保留自研 TLS 证书逻辑** |
| Scheduler GPU 策略 | 条件添加 `--policy-config-file` | 不包含 | 在 `ExtraArgs` 中条件添加 |
| 日志目录 | `/var/log/openFuyao/` | `/var/log/` | 通过 ExtraVolumes 挂载 |

**BKE 自定义 Static Pod 后处理**：
```go
// PostProcessStaticPod 对 kubeadm 生成的 Static Pod 进行 BKE 自定义修改
func (p *ControlPlanePhase) PostProcessStaticPod(component string) error {
    manifestPath := filepath.Join(p.manifestsDir, component+".yaml")

    // 读取 kubeadm 生成的 YAML
    pod, err := readStaticPodManifest(manifestPath)
    if err != nil {
        return err
    }

    // BKE 自定义修改
    switch component {
    case kubeadmconstants.KubeAPIServer:
        // 1. 添加审计策略文件挂载
        addAuditVolumeAndMount(pod)
        // 2. 添加 Webhook 配置挂载（条件）
        if p.adapter.BootScope.HasOpenFuyaoAddon() {
            addWebhookVolumeAndMount(pod)
        }
        // 3. 替换日志目录
        replaceLogVolume(pod, "/var/log/openFuyao/apiserver")

    case kubeadmconstants.KubeControllerManager:
        // 1. 替换 TLS 证书为 BKE 的 tls-server.crt/key
        replaceTLSCertVolumes(pod, p.adapter.BkeConfig.Cluster.CertificatesDir)

    case kubeadmconstants.KubeScheduler:
        // 1. 替换 TLS 证书
        replaceTLSCertVolumes(pod, p.adapter.BkeConfig.Cluster.CertificatesDir)
        // 2. 添加 GPU 策略配置挂载（条件）
        if p.adapter.BootScope.Extra["gpuEnable"] == "true" {
            addGPUPolicyVolumeAndMount(pod)
        }
    }

    // 写回文件
    return writeStaticPodManifest(manifestPath, pod)
}
```
#### 2.5 EtcdStaticPodPhase — Etcd Static Pod 渲染（保留自研）
**目标**：BKE 使用外部 etcd Static Pod，与 kubeadm 的 stacked etcd 模型不同，必须保留自研逻辑。

**新文件**：`pkg/job/builtin/kubeadm/phase/etcd.go`
```go
package phase

import (
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/mfutil"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/kubeadm/adapter"
)

type EtcdStaticPodPhase struct {
    adapter      *adapter.ConfigAdapter
    manifestsDir string
}

func NewEtcdStaticPodPhase(adapter *adapter.ConfigAdapter, manifestsDir string) *EtcdStaticPodPhase {
    return &EtcdStaticPodPhase{
        adapter:      adapter,
        manifestsDir: manifestsDir,
    }
}

// Execute 渲染 etcd Static Pod YAML
// BKE 使用外部 etcd Static Pod，不能使用 kubeadm 的 etcd Phase
func (p *EtcdStaticPodPhase) Execute() error {
    // 保留现有 mfutil.RenderEtcd 逻辑
    // 但使用 adapter 提供的配置，而非直接读取 BootScope
    component := &mfutil.BKEComponent{
        Name: mfutil.Etcd,
        Path: p.manifestsDir,
    }
    return mfutil.RenderEtcd(component, p.adapter.BootScope)
}
```
**保留原因**：
- kubeadm 的 etcd Phase 假设 etcd 运行在 Stacked 模式（与 API Server 同一 Pod 网络）
- BKE 的 etcd 使用独立 Static Pod，数据目录为 `/var/lib/openFuyao/etcd`（而非 `/var/lib/etcd`）
- BKE 需要自定义 etcd 集群成员管理（`handleEtcdMembership`）
- BKE 需要自定义 etcd 监听端口和指标端口
#### 2.6 KubeletPhase — Kubelet 配置生成重构
**目标**：使用 kubeadm SDK 的 kubelet Phase 替代自研的 `kubelet.conf.tmpl` 和 `kubelet.service.tmpl`。

**新文件**：`pkg/job/builtin/kubeadm/phase/kubelet.go`
```go
package phase

import (
    "k8s.io/kubernetes/cmd/kubeadm/app/phases/kubelet"
    kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/kubeadm/adapter"
)

type KubeletPhase struct {
    adapter      *adapter.ConfigAdapter
    pkiDir       string
    manifestsDir string
}

func NewKubeletPhase(adapter *adapter.ConfigAdapter, pkiDir, manifestsDir string) *KubeletPhase {
    return &KubeletPhase{
        adapter:      adapter,
        pkiDir:       pkiDir,
        manifestsDir: manifestsDir,
    }
}

// Execute 生成 Kubelet 配置并写入文件
func (p *KubeletPhase) Execute() error {
    initCfg, err := p.adapter.ToInitConfiguration()
    if err != nil {
        return err
    }
    clusterCfg, err := p.adapter.ToClusterConfiguration()
    if err != nil {
        return err
    }

    cfg := &kubeadmapi.InitConfiguration{
        ClusterConfiguration: *clusterCfg,
    }
    cfg.CertificatesDir = p.pkiDir
    cfg.NodeRegistration.Name = p.adapter.BootScope.HostName

    // 使用 kubeadm SDK 生成 Kubelet 配置
    // 内部会：
    //   1. 生成 /var/lib/kubelet/config.yaml (KubeletConfiguration)
    //   2. 自动设置 clusterDNS、clusterDomain、cgroupDriver 等
    //   3. 自动处理 rotateCertificates: true
    if err := kubelet.WriteKubeletConfigFiles(
        p.manifestsDir,
        cfg,
    ); err != nil {
        return err
    }

    // 生成 kubelet.service（kubeadm SDK 不生成 systemd service 文件）
    if err := p.writeKubeletService(); err != nil {
        return err
    }

    return nil
}

// writeKubeletService 生成 kubelet systemd service 文件
// kubeadm SDK 不提供此功能，需要自研
func (p *KubeletPhase) writeKubeletService() error {
    // 保留现有的 kubelet.service.tmpl 渲染逻辑
    // 但参数来源从 string map 改为结构体
    cfg := bkeinit.BkeConfig(*p.adapter.BkeConfig)
    param := map[string]string{
        "kubeletConfig":         utils.GetKubeletConfPath(),
        "hostIP":                p.adapter.BootScope.HostIP,
        "hostName":              p.adapter.BootScope.HostName,
        "podInfraContainerImage": fmt.Sprintf("%s/kubernetes/pause:%s",
            strings.TrimRight(cfg.ImageFuyaoRepo(), "/"), bkeinit.DefaultPauseImageTag),
        "runtimeEndpoint": p.detectRuntimeEndpoint(),
    }
    // ... 保留现有模板渲染逻辑
}
```
**关键差异**：

| 项目 | 当前自研 | kubeadm SDK | 适配策略 |
|------|---------|------------|---------|
| KubeletConfiguration | Go 模板 `kubelet.conf.tmpl` | `kubelet.WriteKubeletConfigFiles` | 使用 SDK，自动处理版本差异 |
| kubelet.service | Go 模板 `kubelet.service.tmpl` | 不提供 | 保留自研 |
| providerID | 追加写入 conf.yaml | 通过 NodeRegistration 设置 | 在 ConfigAdapter 中处理 |
| KubeletConfig CR | 自研 `readConfigFromKubeletConfigCR` | 不支持 | 保留自研，作为后处理步骤 |
| VariableSubstitutor | `${EXPR\|command\|END}` | 不支持 | **移除**，改用预定义变量映射 |
#### 2.7 Feature Gate — 特性开关层
**新文件**：`pkg/job/builtin/kubeadm/featuregate/featuregate.go`
```go
package featuregate

type FeatureGate string

const (
    UseKubeadmCerts       FeatureGate = "UseKubeadmCerts"
    UseKubeadmKubeConfig  FeatureGate = "UseKubeadmKubeConfig"
    UseKubeadmControlPlane FeatureGate = "UseKubeadmControlPlane"
    UseKubeadmKubelet     FeatureGate = "UseKubeadmKubelet"
)

var defaultGates = map[FeatureGate]bool{
    UseKubeadmCerts:        false,  // Phase 1: 默认关闭
    UseKubeadmKubeConfig:   false,  // Phase 1: 默认关闭
    UseKubeadmControlPlane: false,  // Phase 2: 默认关闭
    UseKubeadmKubelet:      false,  // Phase 3: 默认关闭
}

type FeatureGates struct {
    gates map[FeatureGate]bool
}

func NewFeatureGates() *FeatureGates {
    fg := &FeatureGates{gates: make(map[FeatureGate]bool)}
    for k, v := range defaultGates {
        fg.gates[k] = v
    }
    return fg
}

func (fg *FeatureGates) Enabled(gate FeatureGate) bool {
    if v, ok := fg.gates[gate]; ok {
        return v
    }
    return false
}

func (fg *FeatureGates) Set(gate FeatureGate, enabled bool) {
    fg.gates[gate] = enabled
}

// FromConfigMap 从管理集群的 ConfigMap 加载 Feature Gate 配置
func FromConfigMap(namespace, name string) (*FeatureGates, error) {
    // 从管理集群读取 Feature Gate 配置
    // 允许运行时动态切换，无需重新编译
}
```
### 三、KubeadmPlugin 重构后的流程
#### 3.1 initControlPlane 重构后流程
```
initControlPlane()
  │
  ├─ 1. installKubectlCommand()          [保留不变]
  │
  ├─ 2. CertsPhase.Execute()             [重构]
  │     ├─ if UseKubeadmCerts:
  │     │   ├─ adapter.ToInitConfiguration()
  │     │   ├─ adapter.ToClusterConfiguration()
  │     │   ├─ certs.CreatePKIAssets()    ← kubeadm SDK
  │     │   └─ generateBKEExtraCerts()   ← 自研补充
  │     └─ else:
  │         └─ pkiutil.GenerateCACert/GenerateCertWithCA  ← 原有逻辑
  │
  ├─ 3. KubeConfigPhase.Execute()        [重构]
  │     ├─ if UseKubeadmKubeConfig:
  │     │   └─ kubeadmutil.CreateKubeconfigFile()  ← kubeadm SDK
  │     └─ else:
  │         └─ 原有 handleGenerateKubeConfig 逻辑
  │
  ├─ 4. ControlPlanePhase.Execute()      [重构]
  │     ├─ if UseKubeadmControlPlane:
  │     │   ├─ controlplane.CreateAPIServerStaticPodManifest()  ← kubeadm SDK
  │     │   ├─ controlplane.CreateControllerManagerStaticPodManifest()
  │     │   ├─ controlplane.CreateSchedulerStaticPodManifest()
  │     │   └─ PostProcessStaticPod()    ← BKE 自定义后处理
  │     └─ else:
  │         └─ mfutil.RenderAPIServer/Controller/Scheduler  ← 原有逻辑
  │
  ├─ 5. EtcdStaticPodPhase.Execute()     [保留自研]
  │     └─ mfutil.RenderEtcd()
  │
  ├─ 6. installKubeletCommand()          [重构]
  │     ├─ if UseKubeadmKubelet:
  │     │   ├─ kubelet.WriteKubeletConfigFiles()  ← kubeadm SDK
  │     │   └─ writeKubeletService()     ← 自研
  │     └─ else:
  │         └─ 原有 kubeletPlugin.Execute()
  │
  ├─ 7. uploadTargetClusterKubeletConfig()  [保留不变]
  │
  └─ 8. uploadUserCustomConfigAndGlobalCA() [保留不变]
```
#### 3.2 joinControlPlane 重构后流程
```
joinControlPlane()
  │
  ├─ 1. installKubectlCommand()          [保留不变]
  │
  ├─ 2. CertPlugin (证书下载)             [保留不变]
  │     └─ 从管理集群 Secret 下载证书到本地
  │
  ├─ 3. KubeConfigPhase.Execute()        [重构]
  │     └─ 生成 controller-manager.conf / scheduler.conf / kubelet.conf
  │
  ├─ 4. installKubeletCommand()          [重构]
  │
  ├─ 5. ControlPlanePhase.Execute()      [重构]
  │     └─ 渲染 Static Pod + 后处理
  │
  ├─ 6. EtcdStaticPodPhase.Execute()     [保留自研]
  │     └─ 渲染 etcd.yaml + 添加到已有集群
  │
  └─ 7. uploadUserCustomConfigAndGlobalCA() [保留不变]
```
#### 3.3 upgradeControlPlane 重构后流程
```
upgradeControlPlane()
  │
  ├─ 1. prepareUpgrade()                 [保留不变]
  │     ├─ backupEtcd()
  │     ├─ backupClusterEtc()
  │     ├─ upgradePrePullImageCommand()
  │     └─ getBeforeUpgradeComponentPodHash()
  │
  ├─ 2. for each component:              [重构]
  │     ├─ ControlPlanePhase.Execute(component)  ← kubeadm SDK
  │     ├─ PostProcessStaticPod(component)       ← BKE 后处理
  │     └─ waitComponentReady(component)
  │
  ├─ 3. installKubeletCommand()          [重构]
  │
  └─ 4. installKubectlCommand()          [保留不变]
```
### 四、依赖管理重构
#### 4.1 go.mod 变更
```diff
 require (
-    k8s.io/kubernetes v1.26.4
+    k8s.io/kubernetes v1.28.0
 )

 replace (
-    k8s.io/kubernetes => k8s.io/kubernetes v1.27.0-alpha.2
+    k8s.io/kubernetes => k8s.io/kubernetes v1.28.0
 )
```
**理由**：
- 当前使用 `v1.27.0-alpha.2` 是 alpha 版本，极不稳定
- 升级到 `v1.28.0` 可以使用 `kubeadm.k8s.io/v1beta3` API（v1beta3 从 1.22 开始支持，到 1.28 仍是最新）
- kubeadm 的 Phase API 在 `v1.28` 中相对稳定
#### 4.2 新增依赖
```go
import (
    // 证书生成 Phase
    "k8s.io/kubernetes/cmd/kubeadm/app/phases/certs"

    // KubeConfig 生成
    kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util/kubeconfig"

    // Static Pod 渲染 Phase
    "k8s.io/kubernetes/cmd/kubeadm/app/phases/controlplane"

    // Kubelet 配置生成 Phase
    "k8s.io/kubernetes/cmd/kubeadm/app/phases/kubelet"

    // kubeadm API 类型（已在使用）
    kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"

    // kubeadm 常量
    kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"

    // etcd 工具（已在使用）
    "k8s.io/kubernetes/cmd/kubeadm/app/util/etcd"
)
```
### 五、文件变更清单
| 操作 | 文件路径 | 说明 |
|------|---------|------|
| **新增** | `pkg/job/builtin/kubeadm/adapter/config.go` | Config Adapter，BkeConfig → kubeadm 类型转换 |
| **新增** | `pkg/job/builtin/kubeadm/adapter/config_test.go` | Config Adapter 单元测试 |
| **新增** | `pkg/job/builtin/kubeadm/phase/certs.go` | 证书生成 Phase，封装 kubeadm SDK |
| **新增** | `pkg/job/builtin/kubeadm/phase/kubeconfig.go` | KubeConfig 生成 Phase |
| **新增** | `pkg/job/builtin/kubeadm/phase/controlplane.go` | Static Pod 渲染 Phase |
| **新增** | `pkg/job/builtin/kubeadm/phase/etcd.go` | Etcd Static Pod Phase（保留自研） |
| **新增** | `pkg/job/builtin/kubeadm/phase/kubelet.go` | Kubelet 配置生成 Phase |
| **新增** | `pkg/job/builtin/kubeadm/phase/postprocess.go` | Static Pod 后处理（BKE 自定义修改） |
| **新增** | `pkg/job/builtin/kubeadm/featuregate/featuregate.go` | Feature Gate 管理 |
| **修改** | `pkg/job/builtin/kubeadm/kubeadm.go` | 重构各 Phase 调用，集成 Feature Gate |
| **修改** | `pkg/job/builtin/kubeadm/command.go` | 调用新的 Phase 实现 |
| **修改** | `go.mod` | 升级 k8s.io/kubernetes 版本 |
| **保留** | `utils/bkeagent/pkiutil/` | 保留作为 fallback 和 BKE 特有证书生成 |
| **保留** | `utils/bkeagent/mfutil/tmpl/k8s/` | 保留作为 fallback 模板 |
| **保留** | `pkg/job/builtin/kubeadm/kubelet/tmpl/` | 保留 kubelet.service.tmpl（SDK 不生成） |
| **保留** | `pkg/job/builtin/kubeadm/certs/certs.go` | 保留证书下载/上传逻辑（非生成逻辑） |
| **保留** | `pkg/job/builtin/kubeadm/manifests/manifests.go` | 保留作为 Manifests Phase 的入口 |
### 六、渐进式实施计划
#### Phase 1（2-3 周）— 低风险：KubeConfig 生成
- 实现 `ConfigAdapter`
- 实现 `KubeConfigPhase`
- Feature Gate: `UseKubeadmKubeConfig`
- **回退路径**：关闭 Feature Gate 即可回退到原有逻辑
- **验证**：对比生成的 admin.conf / controller-manager.conf / scheduler.conf / kubelet.conf 内容
#### Phase 2（3-4 周）— 中风险：证书生成
- 实现 `CertsPhase`
- Feature Gate: `UseKubeadmCerts`
- **回退路径**：关闭 Feature Gate 即可回退到原有 pkiutil
- **验证**：
  - 对比 kubeadm SDK 生成的证书 SAN 与自研 pkiutil 生成的 SAN
  - 验证 BKE 特有证书（tls-server/tls-client/ca-chain）仍能正确生成
  - 验证证书轮换功能
#### Phase 3（4-6 周）— 高风险：Static Pod 渲染
- 实现 `ControlPlanePhase` + `PostProcessStaticPod`
- Feature Gate: `UseKubeadmControlPlane`
- **回退路径**：关闭 Feature Gate 即可回退到原有模板渲染
- **验证**：
  - 对比 kubeadm SDK 生成的 Static Pod YAML 与自研模板的 YAML
  - 验证 BKE 自定义修改（审计日志、Webhook、TLS 证书替换）正确应用
  - 端到端测试：init → join → upgrade
#### Phase 4（2-3 周）— 中风险：Kubelet 配置
- 实现 `KubeletPhase`
- Feature Gate: `UseKubeadmKubelet`
- **回退路径**：关闭 Feature Gate 即可回退到原有 kubelet 插件
- **验证**：
  - 对比 KubeletConfiguration 内容
  - 验证 kubelet.service 正确生成
  - 验证 KubeletConfig CR 功能仍正常
#### Phase 5（持续）— 长期：依赖解耦
- 逐步将 `k8s.io/kubernetes` 的引用限制在 `kubeadm/app/apis/kubeadm` 和 `kubeadm/app/phases`
- 评估是否可以将 `k8s.io/kubernetes` 替换为独立的 kubeadm 库（如果社区未来发布）
- 将 etcd 工具从 `k8s.io/kubernetes/cmd/kubeadm/app/util/etcd` 迁移到自研实现
### 七、风险与缓解措施
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| kubeadm Phase API 不稳定 | 升级 K8s 版本时可能 Breaking Change | Feature Gate 快速回退；锁定 k8s.io/kubernetes 版本 |
| kubeadm SDK 生成的 Static Pod 不满足 BKE 需求 | 缺少审计日志/Webhook 等自定义配置 | PostProcessStaticPod 后处理机制 |
| kubeadm SDK 不支持外部 etcd | etcd Phase 无法使用 | 保留自研 EtcdStaticPodPhase |
| ConfigAdapter 转换不完整 | 丢失 BKE 特有配置 | 完善单元测试，对比转换前后的完整配置 |
| 并存两套实现增加维护成本 | 代码量增加 | Phase 5 最终移除自研 fallback |
### 八、总结
本重构方案的核心思路是**选择性引入 kubeadm SDK 的 Phase API**，而非完全替换：
1. **引入 kubeadm SDK 的部分**：证书生成、KubeConfig 生成、Static Pod 渲染、Kubelet 配置生成——这些是 Kubernetes 标准化能力，kubeadm SDK 能自动适配版本变化
2. **保留自研的部分**：etcd Static Pod 管理、HAProxy/Keepalived、环境初始化、预处理/后处理脚本、Agent 自更新、Reset 清理——这些是 BKE 差异化能力，kubeadm 不提供
3. **新增适配层**：Config Adapter 将 BKE 自有配置转换为 kubeadm 标准类型，PostProcessStaticPod 对 kubeadm 输出进行 BKE 自定义修改
4. **Feature Gate 保障**：每个 Phase 都有独立的 Feature Gate，可以逐个启用/关闭，确保生产环境安全


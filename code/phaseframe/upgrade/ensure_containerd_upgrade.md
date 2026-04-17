# EnsureContainerdUpgrade 业务流程梳理
## 一、Phase 定位
`EnsureContainerdUpgrade` 负责升级集群所有节点上的 **containerd 运行时**。containerd 是节点级别的组件，升级方式是通过 Command 机制下发 Agent 内置命令到每个节点执行，而非修改 Kubernetes 资源（如 DaemonSet）。
## 二、核心常量
| 常量 | 值 | 含义 |
|------|-----|------|
| `EnsureContainerdUpgradeName` | `"EnsureContainerdUpgrade"` | Phase 名称 |
| `MasterHADomain` | `"master.bocloud.com"` | Master HA 域名，用于 ExtraHosts 注入 |
## 三、完整业务流程
```
PhaseFlow 调度 EnsureContainerdUpgrade
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Phase 1: NeedExecute(old, new) — 判断是否需要执行           │
│  └──────────────────────────────────────────────────────────────┘
│
├── 1.1 通用检查 (DefaultNeedExecute)
│   ├── BKECluster 正在删除？ → 跳过
│   ├── BKECluster 已暂停？ → 跳过
│   ├── BKECluster DryRun？ → 跳过
│   ├── BKECluster 健康状态 Failed？ → 跳过
│   └── 非 BKECluster 类型且非完全控制？ → 跳过
│
├── 1.2 containerd 版本升级检查 (isContainerdNeedUpgrade)
│   │
│   ├── 1.2.1 检查当前版本是否已记录
│   │   └── Status.ContainerdVersion == ""?
│   │       └── 是 → 返回 false（首次安装，由 EnsureNodesEnv 负责，不由升级负责）
│   │
│   ├── 1.2.2 解析版本号（Major.Minor.Patch）
│   │   ├── oldVersion = version.ParseMajorMinorPatch(Status.ContainerdVersion)
│   │   │   例: "1.6.28" → [1, 6, 28]
│   │   └── newVersion = version.ParseMajorMinorPatch(Spec.ContainerdVersion)
│   │       例: "1.7.2" → [1, 7, 2]
│   │
│   ├── 1.2.3 比较版本
│   │   ├── newVersion < oldVersion → 降级，返回 false
│   │   ├── newVersion == oldVersion → 版本相同，返回 false
│   │   └── newVersion > oldVersion → 升级，继续检查
│   │
│   └── 1.2.4 检查是否存在可升级节点
│       ├── 获取 BKENodes 列表
│       ├── 遍历所有节点:
│       │   ├── 跳过 NeedSkip 的节点
│       │   ├── 跳过 Failed 状态的节点
│       │   └── 只要有一个非 Skip 非 Failed 的节点 → 返回 true ✅
│       └── 全部节点都 Skip 或 Failed → 返回 false
│
└── 1.3 设置状态
    └── SetStatus(PhaseWaiting) → 返回 true ✅
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Phase 2: Execute() → rolloutContainerd() — 执行升级        │
│  └──────────────────────────────────────────────────────────────┘
│
├── 2.1 重置 containerd 配置 (resetContainerd)
│   │
│   ├── 2.1.1 构建 ENV Command (getCommand)
│   │   │
│   │   ├── 获取 BKENodes 列表
│   │   │   └── NodeFetcher.GetBKENodesWrapperForCluster()
│   │   │
│   │   ├── 过滤需要升级的节点
│   │   │   └── GetNeedUpgradeNodesWithBKENodes(bkeCluster, bkeNodes)
│   │   │       ├── 比较版本: Status.OpenFuyaoVersion vs Spec.OpenFuyaoVersion
│   │   │       ├── 版本需要升级 → 继续
│   │   │       ├── 获取节点状态列表
│   │   │       └── 过滤掉 Failed 状态的节点
│   │   │       → 结果: exceptEnvNodes (需要升级的节点列表)
│   │   │
│   │   ├── 获取节点 IP 列表 (用于 LB 判断)
│   │   │   └── Ctx.GetNodes()
│   │   │
│   │   ├── 构建 Extra 参数 (额外的 SAN 地址)
│   │   │   ├── 如果 LB Endpoint 可用且不在节点 IP 中:
│   │   │   │   ├── extra = [LB.Host]
│   │   │   │   └── extraHosts = ["master.bocloud.com:<LB.Host>"]
│   │   │   └── 如果 Ingress VIP 存在且 != LB Host:
│   │   │       └── extra = [..., ingressVIP]
│   │   │
│   │   └── 构建 ENV Command 对象
│   │       ├── Nodes: exceptEnvNodes (需要升级的节点)
│   │       ├── BkeConfigName: bkeCluster.Name
│   │       ├── Extra: [LB IP, Ingress VIP]
│   │       ├── ExtraHosts: ["master.bocloud.com:<LB IP>"]
│   │       ├── DryRun: bkeCluster.Spec.DryRun
│   │       └── Unique: true, RemoveAfterWait: true
│   │
│   ├── 2.1.2 创建 Containerd Reset 命令 (NewConatinerdReset)
│   │   │
│   │   └── 生成 Command CR:
│   │       apiVersion: bkeagent.openfuyao.cn/v1beta1
│   │       kind: Command
│   │       metadata:
│   │         name: k8s-containerd-reset-<timestamp>
│   │       spec:
│   │         nodeSelector:
│   │           matchLabels:
│   │             kubernetes.io/hostname: <node1, node2, ...>
│   │         commands:
│   │         - id: "reset"
│   │           command:
│   │           - "Reset"                              ← Agent 内置命令
│   │           - "bkeConfig=<ns>:<name>"              ← BKE 配置引用
│   │           - "scope=containerd-cfg"               ← 仅重置 containerd 配置
│   │           - "extra=<LB IP>,<Ingress VIP>"        ← 额外 SAN 地址
│   │           type: BuiltIn
│   │           backoffIgnore: true
│   │         ttlSecondsAfterFinished: 0               ← 不自动清理
│   │
│   └── 2.1.3 等待 Reset 命令完成
│       ├── envCommand.Wait()
│       ├── 收集成功/失败节点
│       └── 有失败节点 → 返回错误
│
├── 2.2 重新部署 containerd (redeployContainerd)
│   │
│   ├── 2.2.1 重新构建 ENV Command (getCommand)
│   │   └── 同 2.1.1，获取相同的节点列表和参数
│   │
│   ├── 2.2.2 创建 Containerd Redeploy 命令 (NewConatinerdRedeploy)
│   │   │
│   │   └── 生成 Command CR:
│   │       apiVersion: bkeagent.openfuyao.cn/v1beta1
│   │       kind: Command
│   │       metadata:
│   │         name: k8s-containerd-redeploy-<timestamp>
│   │       spec:
│   │         nodeSelector:
│   │           matchLabels:
│   │             kubernetes.io/hostname: <node1, node2, ...>
│   │         commands:
│   │         - id: "init and check node env"
│   │           command:
│   │           - "K8sEnvInit"                         ← Agent 内置命令
│   │           - "init=true"                          ← 执行初始化
│   │           - "check=true"                         ← 执行检查
│   │           - "scope=runtime"                      ← 仅处理运行时(containerd)
│   │           - "bkeConfig=<ns>:<name>"              ← BKE 配置引用
│   │           type: BuiltIn
│   │           backoffIgnore: false
│   │           backoffDelay: 5
│   │         ttlSecondsAfterFinished: 0
│   │
│   └── 2.2.3 等待 Redeploy 命令完成
│       ├── envCommand.Wait()
│       ├── 收集成功/失败节点
│       └── 有失败节点 → 返回错误
│
└── 2.3 更新 containerd 版本状态
    └── bkeCluster.Status.ContainerdVersion = bkeCluster.Spec.ClusterConfig.Cluster.ContainerdVersion
        例: Status.ContainerdVersion = "1.7.2"
```
## 四、两阶段升级模型详解
containerd 升级采用 **Reset → Redeploy** 两阶段模型：
```
阶段1: Reset (重置 containerd 配置)
│
│   命令: Reset, scope=containerd-cfg
│
│   Agent 在每个节点上执行:
│   ├── 停止 containerd 服务
│   ├── 清理 containerd 配置文件
│   │   (保留数据目录，仅重置配置)
│   └── 准备好接收新配置
│
│   目的: 清除旧版本的 containerd 配置，
│         确保新配置能干净地写入
│
▼
阶段2: Redeploy (重新部署 containerd)
│
│   命令: K8sEnvInit, scope=runtime, init=true, check=true
│
│   Agent 在每个节点上执行:
│   ├── 读取 BKE 配置中的 containerd 版本
│   ├── 安装新版本的 containerd 二进制
│   ├── 生成新的 containerd 配置文件
│   │   (注入 Extra SAN 地址、ExtraHosts 等)
│   ├── 启动 containerd 服务
│   ├── 检查 containerd 运行状态
│   └── 验证容器运行时可用
│
│   目的: 安装新版本 containerd 并确保运行正常
│
▼
更新 Status.ContainerdVersion 记录当前版本
```
**为什么需要两阶段？**
- **Reset** 先清理旧配置，避免新旧配置冲突（如配置格式变化、废弃参数等）
- **Redeploy** 再安装新版本并生成新配置，确保干净的升级路径
- 两阶段分离使得如果 Redeploy 失败，可以安全地重试而不需要先 Reset
## 五、Extra / ExtraHosts 参数的作用
```
场景: 集群使用外部负载均衡器访问 API Server

BKECluster.Spec.ControlPlaneEndpoint.Host = "10.0.0.100"  (LB VIP)
BKECluster.Spec.ControlPlaneEndpoint.Port = 6443

构建 Extra 参数:
├── AvailableLoadBalancerEndPoint 检查:
│   LB IP 不在节点 IP 列表中 → LB 是外部地址
│   ├── extra = ["10.0.0.100"]
│   └── extraHosts = ["master.bocloud.com:10.0.0.100"]
│
└── Ingress VIP (如果存在):
    ├── ingressVip = "10.0.0.200"
    └── extra = ["10.0.0.100", "10.0.0.200"]

这些参数传递给 Agent 后:
├── extra → 写入 containerd 配置的 TLS SAN
│   (containerd 与 API Server 通信时需要验证证书)
│
└── extraHosts → 写入 /etc/hosts
    (确保节点能通过 master.bocloud.com 域名访问 LB)
```
## 六、节点过滤逻辑
```
NeedExecute 中的节点过滤:
│
├── 获取所有 BKENodes
├── 遍历每个节点:
│   ├── NeedSkip == true → 跳过 (用户标记跳过的节点)
│   ├── FailedFlag == true → 跳过 (已失败的节点)
│   └── 否则 → 存在可升级节点，返回 true
│
Execute 中的节点过滤:
│
├── GetNeedUpgradeNodesWithBKENodes
│   ├── 比较 OpenFuyaoVersion: Status vs Spec
│   │   ├── 版本需要升级 → 继续
│   │   └── 版本不需要升级 → 返回空列表
│   │
│   └── filterNonFailedNodes
│       └── 过滤掉 FailedFlag 的节点
│
└── 结果: exceptEnvNodes (需要升级且健康的节点列表)
```
**两层过滤的区别**：
- NeedExecute：判断是否存在**任意**可升级节点（快速决策）
- Execute：获取**所有**需要升级的节点列表（精确执行）
## 七、Command 执行机制
```
管理集群                                    目标集群节点
┌──────────────────┐                    ┌──────────────────┐
│ Provider         │                    │ bkeagent Pod     │
│                  │                    │                  │
│ 1. 创建 Command ─┼───────────────────►│ Watch 到 Command │
│    CR 对象       │   API Server       │                  │
│                  │                    │ 2. 匹配当前节点  │
│                  │                    │ 3. 执行内置命令: │
│                  │                    │    Reset /       │
│                  │                    │    K8sEnvInit    │
│                  │                    │                  │
│ 4. 轮询等待 ◄────┼────────────────────┤ 5. 更新 Command  │
│    Command 完成  │   API Server       │    Status        │
│                  │                    │                  │
│ 6. 收集结果      │                    │                  │
│   successNodes   │                    │                  │
│   failedNodes    │                    │                  │
└──────────────────┘                    └──────────────────┘
```
## 八、流程总结图

```
                    NeedExecute
                        │
            ┌───────────┼──────────────┐
            ▼           ▼              ▼
       通用检查    Status版本为空?   版本比较
       (失败→跳过) (是→跳过)     (降级/相同→跳过)
            │           │              │
            └───────────┼──────────────┘
                        │ 版本升级 + 存在可升级节点
                        ▼
                      Execute
                        │
                        ▼
              ┌─── getCommand ────┐
              │                   │
              ▼                   ▼
        获取升级节点列表     构建 Extra/ExtraHosts
        (过滤Failed节点)    (LB VIP + Ingress VIP)
              │                   │
              └─────────┬─────────┘
                        │
                        ▼
              ┌──────────────────────┐
              │  Step 1: Reset       │
              │  Command: Reset      │
              │  scope=containerd-cfg│
              │  (清理旧配置)        │
              └──────────┬───────────┘
                         │ 成功
                         ▼
              ┌─────────────────────┐
              │  Step 2: Redeploy   │
              │  Command: K8sEnvInit│
              │  scope=runtime      │
              │  (安装新版本+检查)  │
              └──────────┬──────────┘
                         │ 成功
                         ▼
              ┌─────────────────────┐
              │  Step 3: Update     │
              │  Status             │
              │  ContainerdVersion  │
              └──────────┬──────────┘
                         ▼
                       完成
```
## 九、与其他升级 Phase 的对比
| 维度 | EnsureProviderSelfUpgrade | EnsureAgentUpgrade | EnsureContainerdUpgrade |
|------|--------------------------|-------------------|------------------------|
| **操作对象** | 管理集群 Deployment | 目标集群 DaemonSet | 目标集群节点 (通过 Agent) |
| **组件** | bke-controller-manager | bkeagent-deployer | containerd 运行时 |
| **升级方式** | Patch Deployment 镜像 | Patch DS 镜像 + Command | 纯 Command (Reset + Redeploy) |
| **阶段数** | 1 (Patch 镜像) | 2 (Patch DS + Agent SelfUpdate) | 2 (Reset + Redeploy) |
| **客户端** | 本地 client.Client | 远程 kubernetes.Clientset | 本地 client.Client (创建 Command CR) |
| **版本判断** | PatchConfig 镜像比较 | PatchConfig + AddonStatus | Spec.ContainerdVersion vs Status.ContainerdVersion |
| **节点范围** | N/A (单实例) | N/A (DaemonSet 全节点) | 过滤后的可升级节点 |
| **状态更新** | 无 | AddonStatus | Status.ContainerdVersion |
| **K8s 资源变更** | Deployment 镜像 | DaemonSet 镜像 | 无 (仅 Agent 命令) |

**核心区别**：containerd 升级不涉及任何 Kubernetes 资源的修改，完全通过 Agent 内置命令在节点上直接操作 containerd 二进制和配置文件。
        

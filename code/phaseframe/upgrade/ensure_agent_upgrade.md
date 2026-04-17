# EnsureAgentUpgrade 业务流程梳理
## 一、Phase 定位
`EnsureAgetUpgrade` 负责升级目标集群中的 **bkeagent-deployer** 组件。bkeagent-deployer 以 DaemonSet 形式运行在目标集群的每个节点上，是 BKE 在节点上的代理。升级分两步：先更新 DaemonSet 镜像，再通过 Command 机制通知所有节点上的 Agent 执行自更新。
## 二、核心常量与目标资源
| 常量 | 值 | 含义 |
|------|-----|------|
| `bkeagentDeployerName` | `bkeagent-deployer` | DaemonSet 名称 |
| `bkeagentDeployerNamespace` | `cluster-system` | DaemonSet 所在命名空间 |
| `bkeagentDeployerContainerName` | `deployer` | 目标容器名称 |
| `DaemonsetReadyTimeout` | `5m` | 等待 DaemonSet 就绪超时 |

**操作对象**：目标集群中的 `DaemonSet/cluster-system/bkeagent-deployer`（注意：操作的是**远程目标集群**，不是管理集群）
## 三、完整业务流程
```
PhaseFlow 调度 EnsureAgentUpgrade
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
├── 1.2 版本变更检查（纯本地判断，不访问远程集群）
│   │
│   ├── Spec.OpenFuyaoVersion == Status.OpenFuyaoVersion？
│   │   └── 是 → 版本未变化，设置 PhaseSucceeded，返回 false
│   │
│   ├── 从 PatchConfig 解析目标 bkeagent-deployer 版本
│   │   ├── 读取 ConfigMap: cluster-system/bke-config
│   │   │   └── 检查 key "patch.<version>" 是否存在
│   │   ├── 读取 Patch ConfigMap: openfuyao-patch/cm.<version>
│   │   ├── 解析 YAML 为 PatchConfig
│   │   └── 遍历 PatchConfig.Repos → SubImages → Images
│   │       └── 查找 bkeagent-deployer 镜像，提取 Tag 作为目标版本
│   │           例: targetVersion = "v1.0.1"
│   │
│   ├── 从 Status.AddonStatus 获取当前已部署版本
│   │   └── 遍历 AddonStatus，查找 Name=="bkeagent-deployer" 的条目
│   │       例: currentVersion = "v1.0.0"
│   │       例: 未找到 → currentVersion = ""
│   │
│   └── 比较版本
│       ├── currentVersion == "" → 设置 PhaseSucceeded，返回 false
│       │   (未部署过，由安装 Phase 负责，不由升级 Phase 负责)
│       ├── currentVersion == targetVersion → 设置 PhaseSucceeded，返回 false
│       └── currentVersion != targetVersion → 设置 PhaseWaiting，返回 true ✅
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Phase 2: Execute() — 执行升级                               │
│  └──────────────────────────────────────────────────────────────┘
│
├── 2.1 获取远程集群 Client
│   ├── 如果 remoteClient 已缓存 → 直接使用
│   └── 否则调用 kube.GetTargetClusterClient()
│       └── 通过 BKECluster 的 kubeconfig 创建 Clientset
│           → 连接到目标集群 API Server
│
├── 2.2 二次确认是否需要升级 (isBKEAgentDeployerNeedUpgrade)
│   │   (NeedExecute 只做本地判断，这里访问远程集群做精确判断)
│   │
│   ├── 检查版本是否变化（同 1.2）
│   ├── 获取远程 DaemonSet 当前镜像
│   │   └── GET DaemonSet cluster-system/bkeagent-deployer
│   │       └── 读取 container "deployer" 的 Image
│   │           例: currentImage = "registry.example.com/bkeagent-deployer:v1.0.0"
│   │
│   ├── 解析目标镜像 (getAgentDeployerTargetImage)
│   │   └── 同 PatchConfig 查找逻辑，拼接完整镜像
│   │       例: targetImage = "registry.example.com/bkeagent-deployer:v1.0.1"
│   │
│   └── 比较镜像
│       ├── currentImage == targetImage → 返回 false (无需升级)
│       └── currentImage != targetImage → 返回 true ✅
│
├── 2.3 升级 DaemonSet 镜像 (upgradeBKEAgentDeployer)
│   │
│   ├── 2.3.1 解析目标镜像
│   │   └── targetImage = "registry.example.com/bkeagent-deployer:v1.0.1"
│   │
│   ├── 2.3.2 Patch DaemonSet 镜像 (PatchDaemonsetImage)
│   │   ├── GET DaemonSet cluster-system/bkeagent-deployer
│   │   ├── DeepCopy 避免修改缓存
│   │   ├── 更新 container "deployer" 的 Image = targetImage
│   │   ├── 添加 Annotation 触发滚动更新:
│   │   │   annotations["bke.openfuyao.cn/restartedAt"] = <当前时间>
│   │   └── UPDATE DaemonSet
│   │       │
│   │       └── Kubernetes DaemonSet 滚动更新:
│   │           ├── 逐个节点更新 Pod
│   │           ├── 新 Pod 使用新镜像
│   │           └── 旧 Pod 被终止
│   │
│   ├── 2.3.3 等待 DaemonSet 就绪 (WaitDaemonsetReady)
│   │   ├── 轮询间隔: 2s
│   │   ├── 超时: 5min
│   │   ├── 每次轮询检查:
│   │   │   ├── DesiredNumberScheduled > 0?
│   │   │   ├── UpdatedNumberScheduled == DesiredNumberScheduled?
│   │   │   ├── NumberUnavailable == 0?
│   │   │   └── 所有 Pod 都使用 targetImage 且 Ready?
│   │   │
│   │   └── 特殊处理: Context Canceled
│   │       ├── 用 context.Background() 重新检查镜像
│   │       ├── 镜像已更新 → 视为成功
│   │       └── 镜像未更新 → 返回错误
│   │
│   ├── 2.3.4 下发 Agent 自更新命令 (sendBKEAgentCommand)  ← 关键步骤
│   │   │
│   │   ├── 获取集群节点列表
│   │   │   └── NodeFetcher.GetBKENodesWrapperForCluster()
│   │   │       └── 从 API Server 获取所有 BKENode
│   │   │
│   │   ├── 过滤已推送 Agent 的节点
│   │   │   └── GetAgentPushedNodesWithBKENodes()
│   │   │       └── 只保留 NodeAgentPushedFlag = true 的节点
│   │   │           (即 Agent 已安装且可接收命令的节点)
│   │   │
│   │   ├── 获取超时时间
│   │   │   └── GetBootTimeOut(bkeCluster)
│   │   │
│   │   ├── 构造 Command 资源
│   │   │   ├── CommandSpec:
│   │   │   │   ├── Commands:
│   │   │   │   │   └── {ID: "rolloutBKEAgent",
│   │   │   │   │        Command: ["SelfUpdate"],
│   │   │   │   │        Type: CommandBuiltIn}
│   │   │   │   ├── NodeSelector: 选择所有目标节点
│   │   │   │   └── BackoffLimit / ActiveDeadline / TTL
│   │   │   │
│   │   │   └── Custom Command 配置:
│   │   │       ├── CommandName: "bkeagent-deployer-upgrade"
│   │   │       ├── Unique: true (集群内唯一)
│   │   │       └── RemoveAfterWait: true (完成后删除)
│   │   │
│   │   ├── 创建 Command (customCommand.New())
│   │   │   └── 在管理集群创建 Command CR
│   │   │       └── Agent Watch 到 Command 后在节点上执行 SelfUpdate
│   │   │
│   │   └── 等待 Command 完成 (customCommand.Wait())
│   │       ├── 轮询 Command 状态
│   │       ├── 收集成功/失败节点
│   │       ├── 有失败节点 → 返回错误
│   │       └── 全部成功 → 继续
│   │
│   └── 2.3.5 更新 AddonStatus (updateBKEAgentDeployerAddonStatus)
│       ├── 从 targetImage 提取版本号
│       │   例: "registry.example.com/bkeagent-deployer:v1.0.1" → "v1.0.1"
│       │
│       ├── 在 AddonStatus 中查找 bkeagent-deployer 条目
│       │   ├── 已存在 → 更新 Version
│       │   └── 不存在 → 追加新条目 {Name: "bkeagent-deployer", Version: "v1.0.1"}
│       │
│       └── SyncStatusUntilComplete() 更新 BKECluster.Status
│
└── 2.4 返回结果
    └── ctrl.Result{} (不 Requeue，升级完成)
```
## 四、关键数据流：PatchConfig → 目标镜像
与 EnsureProviderSelfUpgrade 相同的查找链，但查找的目标不同：
```
BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion = "v1.0.1"
                          │
                          ▼
ConfigMap: cluster-system/bke-config
  data["patch.v1.0.1"] = "cm.v1.0.1"
                          │
                          ▼
ConfigMap: openfuyao-patch/cm.v1.0.1
  data["v1.0.1"] = <YAML>
                          │
                          ▼
PatchConfig 解析:
  repos:
    - subImages:
        - sourceRepo: registry.example.com
          images:
            - name: /bkeagent-deployer          ← 匹配目标
              tag: ["v1.0.1"]
              usedPodInfo:
              - podPrefix: bkeagent-deployer
                namespace: cluster-system
                          │
                          ▼
拼接完整镜像:
  "registry.example.com/bkeagent-deployer:v1.0.1"
```
**镜像匹配规则** (`isAgentDeployerImage`)：
- 规则1：`image.Name` 包含 `"bkeagent-deployer"`
- 规则2：`image.UsedPodInfo` 中存在 `PodPrefix=="bkeagent-deployer" && NameSpace=="cluster-system"`
## 五、两阶段升级模型
这是本 Phase 最核心的设计——升级分两个阶段：
```
阶段1: DaemonSet 镜像更新 (管理面)
│
│   管理集群 Provider ──PATCH──► 目标集群 DaemonSet
│   (修改镜像 + 注解触发滚动更新)
│
│   DaemonSet Controller (Kubernetes 内置)
│   ├── 逐个节点滚动更新 Pod
│   ├── 新 Pod 拉取新镜像
│   └── 旧 Pod 被替换
│
│   结果: DaemonSet 中所有 Pod 的镜像已更新
│         但 Pod 内的 Agent 进程可能还在运行旧二进制
│         (因为镜像更新只影响新建的 Pod，已运行的进程不会自动更新)
│
▼
阶段2: Agent SelfUpdate 命令 (数据面)
│
│   管理集群 Provider ──CREATE──► Command CR
│   (命令: SelfUpdate)
│
│   各节点 Agent Watch 到 Command
│   ├── 执行 SelfUpdate 内置命令
│   │   ├── 停止当前 Agent 进程
│   │   ├── 从新镜像中提取二进制
│   │   ├── 替换本地 Agent 二进制文件
│   │   └── 重启 Agent 进程
│   │
│   └── 上报 Command 执行结果
│
│   结果: 所有节点上的 Agent 进程已更新为新版本
│
▼
更新 AddonStatus 记录当前版本
```
**为什么需要两阶段？**
- **阶段1** 只更新了 DaemonSet 的 Pod 模板镜像，Kubernetes 滚动更新会替换 Pod，但每个节点上实际运行的 Agent 二进制文件可能需要额外的自更新逻辑（如热更新、配置重载等）
- **阶段2** 通过 Command 机制显式通知每个 Agent 执行 `SelfUpdate`，确保 Agent 进程本身完成自更新
## 六、Command 机制详解
```
┌──────────────────────────────────────────────────────────────────┐
│                      管理集群                                    │
│                                                                  │
│  Provider (EnsureAgentUpgrade)                                   │
│       │                                                          │
│       │ 创建 Command CR                                          │
│       ▼                                                          │
│  ┌───────────────────────────────────────────┐                   │
│  │ apiVersion: bkeagent.openfuyao.cn/v1beta1 │                   │
│  │ kind: Command                             │                   │
│  │ metadata:                                 │                   │
│  │   name: bkeagent-deployer-upgrade-xxxx    │                   │
│  │   namespace: <cluster-namespace>          │                   │
│  │   labels:                                 │                   │
│  │     bke.openfuyao.cn/cluster: <name>      │                   │
│  │ spec:                                     │                   │
│  │   nodeSelector:                           │                   │
│  │     matchLabels:                          │                   │
│  │       kubernetes.io/hostname: node1       │                   │
│  │   commands:                               │                   │
│  │   - id: rolloutBKEAgent                   │                   │
│  │     command: ["SelfUpdate"]               │                   │
│  │     type: BuiltIn                         │                   │
│  │   backoffLimit: 3                         │                   │
│  │   activeDeadlineSecond: 600               │                   │
│  └───────────────────────────────────────────┘                   │
│       │                                                          │
│       │ 等待 Command 完成                                        │
│       │                                                          │
└───────┼──────────────────────────────────────────────────────────┘
        │
        │ Agent 通过 Watch/List 获取 Command
        │
┌───────┼──────────────────────────────────────────────────────────┐
│       ▼              目标集群节点                                │
│                                                                  │
│  bkeagent Pod                                                    │
│  ┌──────────────────────────────────────────┐                    │
│  │ Agent 进程                               │                    │
│  │   ├── Watch Command CR                   │                    │
│  │   ├── 发现匹配当前节点的 Command         │                    │
│  │   ├── 执行 SelfUpdate 内置命令           │                    │
│  │   │   ├── 停止自身                       │                    │
│  │   │   ├── 替换二进制文件                 │                    │
│  │   │   └── 重启                           │                    │
│  │   └── 更新 Command.Status                │                    │
│  └──────────────────────────────────────────┘                    │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```
## 七、NeedExecute 与 Execute 的双重判断设计
```
NeedExecute (轻量级，不访问远程集群)
│
├── 通用检查
├── 版本变化检查 (Spec vs Status)
├── PatchConfig 中目标版本解析
└── AddonStatus 中当前版本比较
    │
    │ 目的: 快速过滤不需要升级的场景
    │       避免不必要的远程集群访问
    │
    ▼ 返回 true

Execute → isBKEAgentDeployerNeedUpgrade (精确判断，访问远程集群)
│
├── 获取远程集群 Client
├── 读取远程 DaemonSet 当前镜像
├── 解析目标镜像
└── 镜像比较
    │
    │ 目的: 精确判断是否真的需要升级
    │       处理 Status 与实际不一致的情况
    │
    ▼ 确认需要升级 → 执行
```
## 八、流程总结图
```
                    NeedExecute
                        │
            ┌───────────┼──────────────┐
            ▼           ▼              ▼
       通用检查     版本变化?     AddonStatus版本
       (失败→跳过)  (否→跳过)   (已是目标→跳过)
            │           │              │
            └───────────┼──────────────┘
                        │ 是
                        ▼
                      Execute
                        │
                ┌───────┼───────┐
                ▼               ▼
          获取远程Client    二次确认升级
                          (远程DaemonSet镜像)
                                │
                    ┌───────────┼───────────┐
                    ▼                       ▼
              不需要升级               需要升级
              (返回空结果)                  │
                                         ▼
                              ┌─────────────────────┐
                              │  Step 1: Patch DS   │
                              │  更新 DaemonSet 镜像 │
                              └──────────┬──────────┘
                                         ▼
                              ┌─────────────────────┐
                              │  Step 2: Wait Ready │
                              │  等待 DS 滚动完成    │
                              └──────────┬──────────┘
                                         ▼
                              ┌─────────────────────┐
                              │  Step 3: Command    │
                              │  下发 SelfUpdate    │
                              │  通知 Agent 自更新   │
                              └──────────┬──────────┘
                                         ▼
                              ┌─────────────────────┐
                              │  Step 4: Update     │
                              │  AddonStatus        │
                              │  记录当前版本        │
                              └──────────┬──────────┘
                                         ▼
                                      完成
```
## 九、与 EnsureProviderSelfUpgrade 的对比
| 维度 | EnsureProviderSelfUpgrade | EnsureAgentUpgrade |
|------|--------------------------|-------------------|
| **操作对象** | 管理集群 Deployment | 目标集群 DaemonSet |
| **组件** | bke-controller-manager | bkeagent-deployer |
| **客户端** | 本地 client.Client | 远程 kubernetes.Clientset |
| **升级方式** | Patch Deployment 镜像 | Patch DaemonSet 镜像 + Command 自更新 |
| **两阶段** | 否（仅 Patch 镜像） | 是（Patch 镜像 + Agent SelfUpdate） |
| **Context Canceled** | 自身会被终止，需特殊处理 | 不涉及自身终止 |
| **版本记录** | 无（Provider 不记录自身版本到 Status） | 更新 AddonStatus |
| **PostHook** | Sleep 2s 优雅等待 | 无特殊处理 |
        

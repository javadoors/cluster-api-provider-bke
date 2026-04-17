# EnsureEtcdUpgrade 业务流程梳理
## 一、Phase 定位
`EnsureEtcdUpgrade` 负责集群 **etcd 集群的滚动升级**。etcd 是 Kubernetes 控制平面的核心有状态组件，升级策略必须是**逐节点滚动**，确保集群始终有法定人数（quorum）可用。
## 二、核心常量
| 常量 | 值 | 含义 |
|------|-----|------|
| `EnsureEtcdUpgradeName` | `"EnsureEtcdUpgrade"` | Phase 名称 |
| `PollImmeInternal` | `500ms` | 轮询 etcd Pod 状态间隔 |
| `PollImmeTimeout` | `3min` | 等待 etcd Pod Ready 超时 |
| `EtcdHealthCheckInterval` | `2s` | etcd 健康检查轮询间隔 |
| `EtcdHealthCheckTimeout` | `5min` | etcd 健康检查超时 |
| `UpgradeNodeCommandNamePrefix` | `"upgrade-node-"` | 升级命令前缀 |
## 三、完整业务流程
```
PhaseFlow 调度 EnsureEtcdUpgrade
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
├── 1.2 etcd 版本变更检查
│   ├── Spec.EtcdVersion == Status.EtcdVersion? → 版本未变，跳过
│   ├── Spec.EtcdVersion 为空? → 跳过
│   ├── Status.EtcdVersion 为空? → 跳过 (首次安装，由安装 Phase 负责)
│   └── Spec.EtcdVersion != Status.EtcdVersion → 需要升级 ✅
│
└── 1.3 设置状态
    └── SetStatus(PhaseWaiting) → 返回 true
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Phase 2: Execute() → reconcileEtcdUpgrade()                │
│  └──────────────────────────────────────────────────────────────┘
│
├── 2.1 二次版本确认
│   ├── Spec.EtcdVersion != Status.EtcdVersion 且均非空?
│   │   └── 是 → 继续升级
│   └── 否 → 记录日志 "etcd version same, not need to upgrade"，返回
│
├── 2.2 过滤可升级节点 (filterUpgradeableNodes)
│   │
│   ├── 2.2.1 获取 BKENodes 列表
│   │   └── NodeFetcher.GetBKENodesWrapperForCluster()
│   │
│   ├── 2.2.2 过滤需要升级的 etcd 节点
│   │   └── GetNeedUpgradeEtcdsWithBKENodes()
│   │       ├── 比较版本: Status.EtcdVersion vs Spec.EtcdVersion
│   │       ├── 版本需要升级 → 继续
│   │       ├── 获取节点状态列表
│   │       └── 过滤掉 Failed 状态的节点
│   │
│   ├── 2.2.3 检查 Agent 就绪状态
│   │   └── 遍历每个节点:
│   │       ├── GetNodeStateFlag(NodeAgentReadyFlag) == true?
│   │       │   └── 是 → 加入可升级列表
│   │       └── 否 → 跳过该节点 ("agent is not ready")
│   │
│   └── 2.2.4 结果检查
│       └── 可升级节点数为 0 → 返回错误
│           "all the master node BKEAgent is not ready"
│
├── 2.3 确定备份节点 (determineBackupNode)
│   │
│   ├── 获取集群节点列表
│   │   └── NodeFetcher.GetNodesForBKECluster()
│   │
│   ├── 过滤 etcd 节点
│   │   └── specNodes.Etcd()
│   │
│   ├── etcd 节点数为 0?
│   │   └── 不需要备份，返回 (false, {})
│   │
│   └── 选择第一个 etcd 节点作为备份节点
│       └── 返回 (true, etcdNodes[0])
│           例: needBackup=true, backupNode={IP: "10.0.0.1", Hostname: "master1"}
│
├── 2.4 逐节点滚动升级 (upgradeNodes)
│   │
│   │   ┌──────────────────────────────────────────────────┐
│   │   │  对每个节点执行 upgradeSingleNode                  │
│   │   │  (串行执行，一个完成后再执行下一个)                 │
│   │   └──────────────────────────────────────────────────┘
│   │
│   ├── 节点1: master1 (10.0.0.1)
│   ├── 节点2: master2 (10.0.0.2)
│   └── 节点3: master3 (10.0.0.3)
│
└── 2.5 完成升级 (finalizeUpgrade)
    ├── 更新 Status.EtcdVersion = Spec.EtcdVersion
    └── SyncStatusUntilComplete() 持久化状态
```
## 四、单节点升级详细流程 (upgradeSingleNode)
这是整个 Phase 的核心，每个 etcd 节点按以下步骤串行升级：
```
upgradeSingleNode(node)
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Step 1: 跳过检查 (shouldSkipNode)                           │
│  └──────────────────────────────────────────────────────────────┘
│
├── 1.1 获取节点当前 etcd 版本 (getEtcdImageVersion)
│   ├── 获取远程集群 Client
│   │   └── kube.GetTargetClusterClient()
│   ├── 构造 etcd Pod 名称
│   │   └── StaticPodName("etcd", node.Hostname)
│   │       例: "etcd-master1"
│   ├── 轮询等待 etcd Pod Ready (间隔 500ms, 超时 3min)
│   │   ├── GET Pod kube-system/etcd-master1
│   │   ├── Pod.Status.Phase == Running?
│   │   └── Pod Condition PodReady == True?
│   ├── 获取 Pod 中 etcd 容器的镜像
│   │   └── 遍历 pod.Spec.Containers，找到 name=="etcd"
│   │       例: container.Image = "registry.example.com/etcd:3.5.9-0"
│   └── 提取版本号
│       └── extractVersionFromImage("registry.example.com/etcd:3.5.9-0")
│           → "3.5.9-0"
│
├── 1.2 版本比较
│   ├── Spec.EtcdVersion 包含当前版本? (strings.Contains)
│   │   例: Spec="3.5.11-0", 当前="3.5.9-0" → 不包含，需要升级
│   │   例: Spec="3.5.11-0", 当前="3.5.11-0" → 包含，跳过
│   └── 跳过 → 返回 nil (不执行后续步骤)
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Step 2: 标记节点升级中 (markNodeUpgrading)                  │
│  └──────────────────────────────────────────────────────────────┘
│
├── 2.1 设置节点状态
│   └── SetNodeStateWithMessage(node.IP, EtcdUpgrading, "Upgrading")
│       例: 节点 10.0.0.1 状态 → EtcdUpgrading, 消息 "Upgrading"
│
└── 2.2 同步状态到 API Server
    └── SyncStatusUntilComplete()
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Step 3: 执行 etcd 升级 (upgradeEtcd)                        │
│  └──────────────────────────────────────────────────────────────┘
│
├── 3.1 创建升级命令 (createUpgradeCommand)
│   │
│   ├── 构建 Upgrade Command 对象:
│   │   ├── Node: 当前节点
│   │   ├── BKEConfig: bkeCluster.Name
│   │   ├── Phase: "UpgradeEtcd"
│   │   └── BackUpEtcd: (当前节点 == 备份节点)? true : false
│   │
│   └── 生成 Command CR:
│       apiVersion: bkeagent.openfuyao.cn/v1beta1
│       kind: Command
│       metadata:
│         name: upgrade-node-10.0.0.1-1700000000
│       spec:
│         nodeSelector:
│           matchLabels:
│             kubernetes.io/hostname: master1
│         commands:
│         - id: "upgrade"
│           command:
│           - "Kubeadm"                     ← Agent 内置命令
│           - "phase=UpgradeEtcd"            ← 升级阶段标识
│           - "bkeConfig=default:prod-cluster" ← BKE 配置引用
│           - "clusterType=bke"              ← 集群类型
│           - "backUpEtcd=true"              ← 是否备份(仅第一个节点)
│           type: BuiltIn
│           backoffDelay: 3
│           backoffIgnore: false
│
├── 3.2 提交升级命令 (executeUpgradeCommand)
│   └── upgrade.New()
│       └── 在管理集群创建 Command CR
│           └── Agent Watch 到后在节点上执行:
│               ├── (如果 backUpEtcd=true) 备份 etcd 数据
│               ├── 停止当前 etcd 进程
│               ├── 更新 etcd 静态 Pod 清单
│               │   (修改镜像版本为新版本)
│               ├── 启动新版本 etcd
│               └── 等待 etcd 加入集群
│
├── 3.3 等待升级命令完成 (waitForUpgradeComplete)
│   └── upgrade.Wait()
│       ├── 轮询 Command 状态
│       ├── 收集成功/失败节点
│       ├── 有失败节点?
│       │   ├── LogCommandFailed() 记录失败详情
│       │   ├── MarkNodeStatusByCommandErrs() 标记节点失败状态
│       │   └── 返回错误
│       └── 全部成功 → 继续
│
├── 3.4 等待 etcd 健康检查通过 (waitForEtcdHealthCheck)
│   │
│   ├── 轮询间隔: 2s, 超时: 5min
│   │
│   ├── 每次轮询:
│   │   ├── 获取远程集群 Client
│   │   ├── 获取 etcd Pod: kube-system/etcd-<hostname>
│   │   ├── 等待 Pod Ready (间隔 500ms, 超时 3min)
│   │   ├── 读取 etcd 容器镜像版本
│   │   │   例: "registry.example.com/etcd:3.5.11-0" → "3.5.11-0"
│   │   └── 版本包含目标版本? (strings.Contains)
│   │       ├── 是 → 健康检查通过 ✅
│   │       └── 否 → 继续轮询
│   │
│   └── 超时 → 返回错误
│
└── 3.5 升级成功日志
    └── "upgrade etcd <node> success"
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Step 4: 标记节点升级成功 (markNodeUpgradeSuccess)           │
│  └──────────────────────────────────────────────────────────────┘
│
├── 4.1 设置节点状态
│   └── SetNodeStateWithMessage(node.IP, EtcdUpgrading, "Upgrading success")
│
└── 4.2 同步状态到 API Server
    └── SyncStatusUntilComplete()
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Step 5: 如果升级失败 → handleUpgradeFailure                 │
│  └──────────────────────────────────────────────────────────────┘
│
├── 5.1 记录错误日志
│   └── "upgrade node <node> failed: <error>"
│
├── 5.2 设置节点失败状态
│   └── SetNodeStateWithMessage(node.IP, EtcdUpgradeFailed, error.Error())
│
├── 5.3 同步状态到 API Server
│   └── SyncStatusUntilComplete()
│
└── 5.4 返回错误
    └── 中断后续节点的升级
```
## 五、滚动升级时序图（3 节点示例）
```
时间轴 ──────────────────────────────────────────────────────────────────►

节点1 (master1, 10.0.0.1)          节点2 (master2, 10.0.0.2)          节点3 (master3, 10.0.0.3)
    │                                   │                                   │
    │ ① 检查版本: 3.5.9-0               │                                   │
    │    ≠ 目标 3.5.11-0                │                                   │
    │                                   │                                   │
    │ ② 标记: EtcdUpgrading             │                                   │
    │                                   │                                   │
    │ ③ 创建 Command                    │                                   │
    │    backUpEtcd=true ← 第一个节点   │                                   │
    │    (备份 etcd 数据)               │                                   │
    │                                   │                                   │
    │ ④ Agent 执行:                     │                                   │
    │    ├── 备份 etcd 数据             │                                   │
    │    ├── 停止 etcd                  │                                   │
    │    ├── 更新静态 Pod 清单          │                                   │
    │    │   etcd:3.5.9-0 → 3.5.11-0    │                                   │
    │    └── 启动新 etcd                │                                   │
    │                                   │                                   │
    │ ⑤ 等待 Command 完成               │                                   │
    │                                   │                                   │
    │ ⑥ 健康检查:                       │                                   │
    │    etcd Pod Ready?                │                                   │
    │    镜像版本 == 3.5.11-0? ✅       │                                   │
    │                                   │                                   │
    │ ⑦ 标记: Upgrading success         │                                   │
    │                                   │                                   │
    │ ██████████████████████            │                                   │
    │ 节点1 升级完成 (v3.5.11-0)        │                                   │
    │                                   │                                   │
    │                                   │ ⑧ 检查版本: 3.5.9-0              │
    │                                   │    ≠ 目标 3.5.11-0               │
    │                                   │                                   │
    │                                   │ ⑨ 标记: EtcdUpgrading            │
    │                                   │                                   │
    │                                   │ ⑩ 创建 Command                   │
    │                                   │    backUpEtcd=false               │
    │                                   │                                   │
    │                                   │ ⑪ Agent 执行:                    │
    │                                   │    ├── 停止 etcd                  │
    │                                   │    ├── 更新静态 Pod 清单          │
    │                                   │    └── 启动新 etcd                │
    │                                   │                                   │
    │                                   │ ⑫ 等待 + 健康检查 ✅             │
    │                                   │                                   │
    │                                   │ ⑬ 标记: Upgrading success        │
    │                                   │                                   │
    │                                   │ ██████████████████████            │
    │                                   │ 节点2 升级完成 (v3.5.11-0)       │
    │                                   │                                   │
    │                                   │                                   │ ⑭ 同上...
    │                                   │                                   │
    │                                   │                                   │ ██████████████████████
    │                                   │                                   │ 节点3 升级完成 (v3.5.11-0)
    │                                   │                                   │
    │                                   │                                   │
    ▼                                   ▼                                   ▼
 ⑮ finalizeUpgrade: Status.EtcdVersion = "3.5.11-0"
```
## 六、etcd 备份策略
```
determineBackupNode:
│
├── 获取所有 etcd 节点
│   └── specNodes.Etcd() → [master1, master2, master3]
│
├── 选择第一个 etcd 节点作为备份节点
│   └── backupNode = master1
│
└── 在 createUpgradeCommand 中:
    ├── 如果 当前节点.IP == backupNode.IP
    │   └── upgrade.BackUpEtcd = true
    │       → Command 中 backUpEtcd=true
    │       → Agent 在升级前先备份 etcd 数据
    │
    └── 其他节点
        └── upgrade.BackUpEtcd = false
            → Command 中 backUpEtcd=false
            → Agent 直接升级，不备份

备份时机: 仅在升级第一个 etcd 节点时备份
目的: 如果升级失败，可以用备份数据恢复 etcd 集群
```
## 七、etcd 版本验证机制 (getEtcdImageVersion)
这是 etcd 升级中**最关键的验证步骤**——通过检查实际运行的 Pod 镜像来确认版本，而非仅依赖 Status 记录：
```
getEtcdImageVersion(node)
│
├── 1. 获取远程集群 ClientSet
│   └── kube.GetTargetClusterClient()
│       → 连接到目标集群 API Server
│
├── 2. 构造 etcd Pod 名称
│   └── StaticPodName("etcd", node.Hostname)
│       → "etcd-master1"
│
├── 3. 轮询等待 etcd Pod Ready
│   ├── 间隔: 500ms
│   ├── 超时: 3min
│   ├── 每次轮询:
│   │   ├── GET Pod kube-system/etcd-master1
│   │   ├── 404 → Pod 不存在，继续等待
│   │   ├── Phase != Running → 继续等待
│   │   └── Condition PodReady == True → Pod 就绪
│   └── 超时 → 返回错误
│
├── 4. 获取 Pod 详情
│   └── GET Pod kube-system/etcd-master1
│
└── 5. 提取 etcd 容器镜像版本
    ├── 遍历 pod.Spec.Containers
    ├── 找到 name == "etcd" 的容器
    ├── 读取 container.Image
    │   例: "registry.example.com/etcd:3.5.11-0"
    └── extractVersionFromImage()
        └── 按 ":" 分割，取最后一段 → "3.5.11-0"
```
**使用场景**：
- `shouldSkipNode`：判断节点是否已是目标版本，避免重复升级
- `waitForEtcdHealthCheck`：升级后验证 etcd Pod 已使用新版本镜像且 Ready
## 八、节点状态流转
```
正常状态
    │
    │ markNodeUpgrading
    ▼
EtcdUpgrading ("Upgrading")
    │
    ├── 升级成功
    │   │ markNodeUpgradeSuccess
    │   ▼
    │ EtcdUpgrading ("Upgrading success")
    │
    └── 升级失败
        │ handleUpgradeFailure
        ▼
      EtcdUpgradeFailed ("<error message>")
```
## 九、流程总结图
```
                    NeedExecute
                        │
            ┌───────────┼──────────────┐
            ▼           ▼              ▼
       通用检查    版本未变/为空?   版本不同
       (失败→跳过) (是→跳过)     (否→执行)
                        │
                        ▼
                    Execute
                        │
            ┌───────────┼───────────────┐
            ▼           ▼               ▼
      过滤可升级节点  确定备份节点    逐节点滚动升级
      (Agent Ready    (第一个etcd    (串行执行)
       + 非Failed)     节点)            │
            │           │               │
            └───────────┼───────────────┘
                        │
                        ▼
              ┌─── 对每个节点 ────┐
              │                   │
              ▼                   ▼
        版本已是目标? ──是──→ 跳过
              │否
              ▼
        标记 EtcdUpgrading
              │
              ▼
        创建 Upgrade Command
        (Kubeadm, phase=UpgradeEtcd)
        (backUpEtcd=仅第一个节点)
              │
              ▼
        等待 Command 完成
              │
              ├── 失败 → 标记 EtcdUpgradeFailed → 中断
              │
              ▼ 成功
        等待 etcd 健康检查
        (Pod Ready + 镜像版本验证)
              │
              ├── 超时 → 返回错误
              │
              ▼ 通过
        标记 Upgrading success
              │
              ▼
          下一个节点...
              │
              ▼ 全部完成
        finalizeUpgrade
        Status.EtcdVersion = Spec.EtcdVersion
```
## 十、与其他升级 Phase 的对比
| 维度 | EnsureProviderSelfUpgrade | EnsureAgentUpgrade | EnsureContainerdUpgrade | EnsureEtcdUpgrade |
|------|--------------------------|-------------------|------------------------|-------------------|
| **操作对象** | 管理集群 Deployment | 目标集群 DaemonSet | 节点 containerd | 节点 etcd 静态 Pod |
| **升级策略** | 滚动更新 (K8s 原生) | DS 滚动 + Agent SelfUpdate | 全节点并行 (Command) | **逐节点串行滚动** |
| **升级命令** | Patch Deployment 镜像 | Patch DS + Command(SelfUpdate) | Command(Reset + K8sEnvInit) | Command(Kubeadm, UpgradeEtcd) |
| **备份** | 无 | 无 | 无 | **第一个节点备份 etcd 数据** |
| **健康检查** | WaitDeploymentReady | WaitDaemonsetReady | Command 等待 | **Pod Ready + 镜像版本验证** |
| **版本验证** | Deployment 镜像比较 | DS 镜像比较 | Spec vs Status | **远程集群 Pod 实际镜像** |
| **节点状态** | 无 | AddonStatus | Status.ContainerdVersion | **NodeState: EtcdUpgrading/Failed** |
| **失败处理** | 返回错误 | 返回错误 | 返回错误 | **标记节点失败 + 中断后续** |
| **串行/并行** | N/A | N/A | 并行 | **严格串行** |

**核心区别**：etcd 升级是所有 Phase 中最保守的——严格串行、备份、双重健康检查（Command 完成 + Pod 版本验证），因为 etcd 是有状态集群，任何节点不可用都可能影响 quorum。
        

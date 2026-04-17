# `ensure_worker_upgrade.go` 业务流程
## `ensure_worker_upgrade.go` 业务流程梳理
### 一、Phase 定位
`EnsureWorkerUpgrade` 是**Worker 节点滚动升级协调器**，负责在集群 K8s 版本变更时，将所有 Worker 节点逐个升级到目标版本。它位于升级流水线的中后段（在 etcd/master 升级之后），采用**串行滚动升级**策略——一次只升级一个 Worker 节点，升级完一个再升级下一个。
### 二、核心常量
| 常量 | 值 | 说明 |
|------|------|------|
| `EnsureWorkerUpgradeName` | `"EnsureWorkerUpgrade"` | Phase 名称标识 |
| `WorkerNodeHealthCheckPollIntervalSeconds` | `2` | 健康检查轮询间隔 2 秒 |
| `WorkerNodeHealthCheckTimeoutMinutes` | `5` | 健康检查超时 5 分钟 |
### 三、完整业务流程
```
┌─────────────────────────────────────────────────────────────┐
│                    NeedExecute() 判断                        │
│  1. DefaultNeedExecute → 检查是否有 spec 变更               │
│  2. 集群状态检查 → Unhealthy/Unknown 时跳过                  │
│  3. ControlPlane 是否已初始化                                │
│  4. 是否有需要升级的 Worker 节点                             │
│     (Status.KubernetesVersion vs Spec.KubernetesVersion)     │
└──────────────────────┬──────────────────────────────────────┘
                       │ 需要执行
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                    Execute() 入口                            │
│  1. 设置 deployAction=k8s_upgrade 注解（仅首次）             │
│  2. 调用 reconcileWorkerUpgrade()                           │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────────┐
│              reconcileWorkerUpgrade()                        │
│  比较 Spec.KubernetesVersion 与 Status.KubernetesVersion     │
│  → 不同：调用 rolloutUpgrade()                              │
│  → 相同：跳过，打印 "k8s version same, not need upgrade"     │
└──────────────────────┬──────────────────────────────────────┘
                       │ 版本不同
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                 rolloutUpgrade() 主流程                      │
│                                                              │
│  Step 1: prepareUpgradeNodes()  准备升级节点列表             │
│    ├── NodeFetcher.GetBKENodesWrapperForCluster()            │
│    ├── GetNeedUpgradeWorkerNodesWithBKENodes() 版本过滤      │
│    └── 逐节点检查 Agent Ready 状态                           │
│        → Agent 不 Ready 的节点跳过                           │
│        → 全部不 Ready 则返回错误                             │
│                                                              │
│  Step 2: 创建 Drainer 和 ClientSet                          │
│    ├── GetTargetClusterClient() 获取远端集群客户端           │
│    └── NewDrainer() 创建节点驱逐器                           │
│                                                              │
│  Step 3: processNodeUpgrade()  逐节点升级                    │
│    └── 详见下方                                              │
│                                                              │
│  Step 4: 汇总结果                                           │
│    ├── 全部成功 → 返回 nil                                   │
│    └── 有失败 → 返回错误，阻止进入下一 Phase                 │
└─────────────────────────────────────────────────────────────┘
```
### 四、逐节点升级详细流程 (`processNodeUpgrade`)
```
对 needUpgradeNodes 中每个节点：
│
├── 1. GetRemoteNodeByBKENode()  获取远端集群 Node 资源
│
├── 2. 版本预检
│   └── 如果 NodeInfo.KubeletVersion == 目标版本 → 跳过该节点
│
├── 3. 标记节点状态为 Upgrading
│   ├── NodeFetcher.SetNodeStateWithMessage(NodeUpgrading)
│   └── SyncStatusUntilComplete() 持久化状态
│
├── 4. upgradeNode()  执行升级
│   ├── executeNodeUpgrade()  下发升级命令
│   │   ├── 创建 Upgrade Command（Phase=UpgradeWorker, BackUpEtcd=false）
│   │   ├── upgrade.New()  创建命令资源
│   │   ├── upgrade.Wait()  等待命令完成
│   │   └── 失败时：LogCommandFailed + MarkNodeStatusByCommandErrs
│   │
│   └── waitForNodeHealth()  等待健康检查
│       ├── NewRemoteClientByBKECluster()  获取远端客户端
│       └── waitForWorkerNodeHealthCheck()  轮询检查
│           ├── 每 2 秒轮询，超时 5 分钟
│           ├── 获取远端 Node 资源
│           └── NodeHealthCheck() 检查：
│               ├── Step1: 节点 Ready 状态
│               ├── Step2: Kubelet 版本 == 目标版本
│               └── Step3: Worker 节点不检查组件（仅 Master 才检查）
│
├── 5a. 升级成功 → 标记 NodeNotReady("Upgrading success")
│
└── 5b. 升级失败 → 标记 NodeUpgradeFailed(err.Error())
    └── continue（继续升级下一个节点，不中断整个流程）
```
### 五、关键设计要点
#### 1. 滚动升级策略——串行逐节点
与 etcd/master 升级类似，Worker 升级采用**串行逐节点**方式。但有一个重要区别：**Worker 升级不驱逐 Pod**（虽然创建了 Drainer，但在当前代码中并未调用 `Drain` 方法），仅通过命令下发方式升级 kubelet。
#### 2. 容错策略——Best-Effort 继续
`processNodeUpgrade` 中，单个节点升级失败后**不会中断整个流程**，而是：
- 记录失败节点到 `failedUpgradeNodes` 列表
- 标记该节点状态为 `NodeUpgradeFailed`
- `continue` 继续处理下一个节点

最终汇总时，如果有任何节点失败，整个 Phase 返回错误，**阻止进入下一阶段**，等待下次 Requeue 重试。
#### 3. 升级命令——不备份 etcd
Worker 节点升级时 `BackUpEtcd = false`，因为 etcd 数据只在 Master 节点上，Worker 节点无需备份。
#### 4. Agent 就绪检查
在 `prepareUpgradeNodes` 阶段，会通过 `NodeFetcher.GetNodeStateFlag(NodeAgentReadyFlag)` 检查每个节点的 Agent 是否就绪。**Agent 不 Ready 的节点会被跳过**，避免向不可达的节点下发命令。
#### 5. 健康检查——Worker 节点简化检查
`NodeHealthCheck` 对 Worker 节点只检查两项：
- **节点 Ready 状态**：`node.conditions` 中 `Ready == True`
- **Kubelet 版本**：`node.status.nodeInfo.kubeletVersion == 目标版本`

不像 Master 节点还需要额外检查 kube-apiserver、kube-controller-manager 等组件健康状态。
#### 6. deployAction 注解
Execute 入口处会检查并设置 `deployAction=k8s_upgrade` 注解，这是与外部系统（BOC）的集成点，用于通知外部系统当前处于升级状态。
### 六、与 Master 升级的对比
| 维度 | Worker 升级 | Master 升级 |
|------|------------|------------|
| 升级策略 | 串行逐节点 | 串行逐节点 |
| etcd 备份 | `BackUpEtcd=false` | `BackUpEtcd=true`（首个节点备份） |
| 健康检查 | 仅 Ready + 版本 | Ready + 版本 + 组件健康 |
| Pod 驱逐 | 创建 Drainer 但未使用 | 创建 Drainer 并执行 Drain |
| 失败处理 | 记录失败节点，继续下一个 | 直接返回错误中断 |
| Phase 标识 | `UpgradeWorker` | `UpgradeMaster` |
### 七、状态流转图
```
节点状态流转：
  NodeNotReady/NodeReady
       │
       ▼ (开始升级)
  NodeUpgrading("Upgrading")
       │
       ├── 成功 ▶ NodeNotReady("Upgrading success")
       │              │
       │              ▼ (健康检查通过后由后续 Phase 更新)
       │           NodeReady
       │
       └── 失败 ▶ NodeUpgradeFailed(err.Error())
                      │
                      ▼ (下次 Requeue 重试)
                   NodeUpgrading("Upgrading") → ...
```
### 八、潜在改进点
1. **Drainer 未使用**：创建了 `kubedrain.Helper` 但未在升级前驱逐 Pod，可能导致工作负载中断。如果需要优雅升级，应在升级前调用 `Drain` 方法。
2. **并行升级**：当前串行逐节点升级效率较低，对于大量 Worker 节点的集群，可以考虑引入并行度控制（如 `maxUnavailable` 参数）。
3. **失败节点重试**：当前失败节点在下次 Requeue 时会重新进入整个流程，缺少针对失败节点的独立重试机制。
4. **超时配置硬编码**：健康检查的轮询间隔和超时时间是常量，无法根据集群规模动态调整。
        

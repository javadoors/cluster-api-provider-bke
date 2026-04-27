# 对比 `ensure_master_upgrade.md` 文档与实际代码的差异，并给出正确的规格。

## 差异对比

### 文档 vs 代码

| 方面 | `ensure_master_upgrade.md` 文档 | 实际 `kubeadm.go:278-337` 代码 |
|------|--------------------------------|-------------------------------|
| **定位** | 协调层流程（EnsureMasterUpgrade Phase） | 执行层代码（Kubeadm 插件的 upgradeControlPlane） |
| **etcd 是否升级** | 文档中暗示包含 etcd（有 etcd 注解保障等） | ❌ **不包含 etcd**（etcd 有单独的 `upgradeEtcd` 函数） |
| **控制平面组件** | 未明确列出 | ✅ kube-apiserver、kube-controller-manager、kube-scheduler |

## 正确规格：`upgradeControlPlane` 函数完整流程

### 函数签名
```go
func (k *KubeadmPlugin) upgradeControlPlane(backUpEtcd bool, clusterType string) error
```

### 完整执行流程

```
┌─────────────────────────────────────────────────────────────────┐
│  upgradeControlPlane (控制平面节点升级)                          │
└─────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 0: clusterType == "bocloud" 检查（注释，但无实际代码）     │
└─────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 1: prepareUpgrade()  升级前准备                           │
├─────────────────────────────────────────────────────────────────┤
│  1.1 backupEtcd() → 如果 backUpEtcd = true 则备份 etcd          │
│  1.2 backupClusterEtc() → 备份集群配置                           │
│  1.3 upgradePrePullImageCommand() → 预拉取镜像                   │
│  1.4 getBeforeUpgradeComponentPodHash() → 获取组件 Pod Hash      │
└─────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 2: 设置 boot 参数                                          │
│      k.boot.Extra["upgradeWithOpenFuyao"] = ...                  │
└─────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 3: 逐个升级控制平面组件（不含 etcd）                       │
├─────────────────────────────────────────────────────────────────┤
│  循环遍历 mfutil.GetControlPlaneComponents():                    │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 组件 1: kube-apiserver                                      │  │
│  │   a. needUpgradeComponent() → 检查是否需要升级               │  │
│  │   b. upgradeControlPlaneManifestCommand() → 生成新 manifest │  │
│  │   c. waitComponentReady() → 等待组件就绪                     │  │
│  └───────────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 组件 2: kube-controller-manager                             │  │
│  │   (同上流程)                                               │  │
│  └───────────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 组件 3: kube-scheduler                                      │  │
│  │   (同上流程)                                               │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 4: 升级 kubelet                                           │
│      installKubeletCommand()                                    │
└─────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 5: 升级 kubectl                                           │
│      installKubectlCommand()                                    │
└─────────────────────────────────────────────────────────────────┘
```

## 关键点说明

### 1. **etcd 是独立的**
- `upgradeControlPlane` **不升级 etcd**
- etcd 升级有单独的函数：`upgradeEtcd`（第 353-387 行）

### 2. **控制平面组件列表**
从 `mfutil.GetControlPlaneComponents()`（第 117-123 行）：
```go
return []string{
    "kube-apiserver",
    "kube-controller-manager", 
    "kube-scheduler",
}
```

### 3. **升级方式**
- **静态 Pod 升级**：通过生成新的 `manifest` YAML 文件，kubelet 自动重启 Pod
- **kubelet/kubectl 升级**：二进制升级 + systemd 重启

## 完整的升级架构关系

```
┌─────────────────────────────────────────────────────────────┐
│  协调层 (EnsureMasterUpgrade Phase)                          │
│  - ensure_master_upgrade.md 描述的流程                       │
└─────────────────────────────────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────────┐
│  命令层 (Upgrade Command)                                   │
│  - Phase=UpgradeControlPlane 或 UpgradeEtcd                 │
└─────────────────────────────────────────────────────────────┘
                          ↓
        ┌─────────────────┴─────────────────┐
        ↓                                   ↓
┌───────────────────────┐      ┌───────────────────────┐
│  upgradeControlPlane  │      │    upgradeEtcd        │
│  (kubeadm.go:278-337) │      │  (kubeadm.go:353-387) │
├───────────────────────┤      ├───────────────────────┤
│  kube-apiserver       │      │  etcd                 │
│  kube-controller-mgr  │      │  (仅 etcd)            │
│  kube-scheduler       │      │                       │
│  kubelet              │      │                       │
│  kubectl              │      │                       │
└───────────────────────┘      └───────────────────────┘
```

## 总结

**文档与代码的不一致**：`ensure_master_upgrade.md` 描述的是协调层的整体流程，而 `upgradeControlPlane` 只是其中的执行层函数，**不包含 etcd 升级**（etcd 有独立的 `upgradeEtcd` 函数）。

正确的架构是：
- **协调层**：`EnsureMasterUpgrade` Phase（文档描述）
- **执行层**：两个独立函数
  - `upgradeControlPlane` → 升级控制平面组件 + kubelet/kubectl
  - `upgradeEtcd` → 单独升级 etcd
        
# `ensure_master_upgrade.go` 业务流程
## `ensure_master_upgrade.go` 业务流程梳理
### 一、Phase 定位
`EnsureMasterUpgrade` 是 **Master 节点滚动升级协调器**，负责在集群 K8s 版本变更时，将所有 Master 节点逐个升级到目标版本。它是升级流水线中**最核心的 Phase**——因为 Master 升级完成后，还需要更新集群状态版本号、升级 kube-proxy Addon、确保 etcd 注解等收尾工作。
### 二、核心常量
| 常量 | 值 | 说明 |
|------|------|------|
| `EnsureMasterUpgradeName` | `"EnsureMasterUpgrade"` | Phase 名称标识 |
| `MasterUpgradePollIntervalSeconds` | `2` | 健康检查轮询间隔 2 秒 |
| `MasterUpgradeTimeoutMinutes` | `5` | 健康检查超时 5 分钟 |
### 三、完整业务流程
```
┌──────────────────────────────────────────────────────────────┐
│                     NeedExecute() 判断                        │
│  1. DefaultNeedExecute → 检查是否有 spec 变更                │
│  2. ControlPlane 是否已初始化（fetchBKENodesIfCPInitialized） │
│  3. 是否有需要升级的 Master 节点                              │
│     (Status.KubernetesVersion vs Spec.KubernetesVersion)      │
│     → 过滤出 Master 角色节点                                 │
└──────────────────────┬───────────────────────────────────────┘
                       │ 需要执行
                       ▼
┌──────────────────────────────────────────────────────────────┐
│                     Execute() 入口                            │
│  1. 设置 deployAction=k8s_upgrade 注解（仅首次）              │
│  2. 调用 reconcileMasterUpgrade()                            │
└──────────────────────┬───────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────┐
│               reconcileMasterUpgrade()                        │
│  比较 Spec.KubernetesVersion 与 Status.KubernetesVersion      │
│  → 不同：调用 rolloutUpgrade()                               │
│  → 相同：跳过，打印 "k8s version same, not need upgrade"      │
└──────────────────────┬───────────────────────────────────────┘
                       │ 版本不同
                       ▼
┌──────────────────────────────────────────────────────────────┐
│                  rolloutUpgrade() 主流程                      │
│                                                               │
│  Step 1: getNeedUpgradeNodes()  获取升级节点列表              │
│    ├── NodeFetcher.GetBKENodesWrapperForCluster()             │
│    ├── GetNeedUpgradeMasterNodesWithBKENodes() 版本过滤       │
│    └── 逐节点检查 Agent Ready 状态                            │
│        → Agent 不 Ready 的节点跳过                            │
│        → 全部不 Ready 则返回错误                              │
│                                                               │
│  Step 2: 检查 etcd 配置，决定是否备份                         │
│    ├── NodeFetcher.GetNodesForBKECluster() 获取 Spec 节点     │
│    ├── 提取 etcd 节点列表                                     │
│    ├── etcd 节点存在 → needBackupEtcd=true                    │
│    │   backEtcdNode = etcdNodes[0]（取第一个 etcd 节点）       │
│    └── etcd 节点不存在 → needBackupEtcd=false                 │
│                                                               │
│  Step 3: ensureEtcdAdvertiseClientUrlsAnnotation()            │
│    确保每个 etcd StaticPod 有 advertise-client-urls 注解       │
│                                                               │
│  Step 4: upgradeMasterNodesWithParams()  逐节点升级           │
│    └── 详见下方                                               │
│                                                               │
│  Step 5: 更新集群状态版本号                                   │
│    bkeCluster.Status.KubernetesVersion = Spec.KubernetesVersion│
│                                                               │
│  Step 6: updateAddonVersions()  更新 Addon 版本               │
│    └── 详见下方                                               │
└──────────────────────────────────────────────────────────────┘
```
### 四、逐节点升级详细流程 (`upgradeMasterNodesWithParams`)
```
对 needUpgradeNodes 中每个节点（串行）：
│
├── 1. GetRemoteNodeByBKENode()  获取远端集群 Node 资源
│
├── 2. 版本预检
│   └── 如果 NodeInfo.KubeletVersion == 目标版本 → 跳过该节点
│
├── 3. 标记节点状态为 Upgrading
│   ├── NodeFetcher.SetNodeStateWithMessageForCluster(NodeUpgrading)
│   └── SyncStatusUntilComplete() 持久化状态
│
├── 4. upgradeNode()  执行升级
│   │
│   ├── 4a. executeNodeUpgradeWithParams()  下发升级命令
│   │   ├── 创建 Upgrade Command（Phase=UpgradeControlPlane）
│   │   ├── 判断是否需要备份 etcd
│   │   │   └── NeedBackupEtcd && 当前节点IP == backEtcdNode.IP
│   │   │       → upgrade.BackUpEtcd = true（仅备份节点触发）
│   │   ├── upgrade.New()  创建命令资源
│   │   ├── upgrade.Wait()  等待命令完成
│   │   └── 失败时：LogCommandFailed + MarkNodeStatusByCommandErrs
│   │
│   └── 4b. waitForNodeHealthCheckWithParams()  等待健康检查
│       ├── NewRemoteClientByBKECluster()  获取远端客户端
│       └── waitForWorkerNodeHealthCheck()  轮询检查
│           ├── 每 2 秒轮询，超时 5 分钟
│           ├── 获取远端 Node 资源
│           └── NodeHealthCheck() 检查：
│               ├── Step1: 节点 Ready 状态
│               ├── Step2: Kubelet 版本 == 目标版本
│               └── Step3: Master 节点额外检查组件健康
│                   （CheckComponentHealth: kube-apiserver等）
│
├── 5a. 升级成功 → 标记 NodeNotReady("Upgrading success")
│        └── SyncStatusUntilComplete()
│
└── 5b. 升级失败 → 标记 NodeUpgradeFailed(err.Error())
     ├── SyncStatusUntilComplete()
     └── ⚠️ 立即返回错误，中断整个升级流程
         （与 Worker 升级的 Best-Effort 策略不同！）
```
### 五、etcd 注解保障 (`ensureEtcdAdvertiseClientUrlsAnnotation`)
```
对每个 etcd 节点：
│
├── 1. 构造 etcd StaticPod 名称: "etcd-{hostname}"
│
├── 2. 获取 etcd Pod 对象（kube-system 命名空间）
│
├── 3. 检查注解 "kubeadm.kubernetes.io/etcd.advertise-client-urls"
│   ├── 注解已存在且非空 → 跳过
│   └── 注解不存在 → 设置注解值为 "https://{nodeIP}:2379"
│
└── 4. 更新 Pod 对象
```
**设计意图**：确保 etcd Pod 的 `advertise-client-urls` 信息以注解形式持久化，防止升级过程中 etcd 客户端连接信息丢失。这是 kubeadm 升级流程中的关键步骤。
### 六、Addon 版本更新 (`updateAddonVersions`)
```
┌─────────────────────────────────────────────────────────────┐
│              updateAddonVersions()                           │
│                                                              │
│  1. 遍历 bkeCluster.Spec.ClusterConfig.Addons               │
│     ├── 检查 kubeproxy: addon.Version != 目标K8s版本?       │
│     │   → kubeproxyNeedUpgrade = true                       │
│     └── 检查 kubectl: addon.Version != "v1.25"?            │
│         → kubectlNeedUpgrade = true                         │
│                                                              │
│  2. 如果 kubeproxyNeedUpgrade:                              │
│     ├── upgradeKubeProxy(目标版本)                           │
│     │   ├── 获取远端集群 kube-proxy DaemonSet               │
│     │   ├── ModifyImageRepository() 替换镜像仓库地址         │
│     │   ├── ModifyImageTag() 替换镜像 Tag 为目标版本         │
│     │   └── Update DaemonSet 到远端集群                      │
│     └── PatchFunc: 更新 Spec.Addons 和 Status.AddonStatus   │
│         中 kubeproxy 的 Version                              │
│                                                              │
│  3. 如果 kubectlNeedUpgrade:                                │
│     └── PatchFunc: 更新 Spec.Addons 中 kubectl 版本为 v1.25 │
│         （如果不存在则追加）                                  │
│                                                              │
│  4. SyncStatusUntilComplete(patchFuncs...) 统一持久化        │
└─────────────────────────────────────────────────────────────┘
```
### 七、关键设计要点
#### 1. etcd 备份策略——仅首个 etcd 节点触发
```go
if params.NeedBackupEtcd && params.Node.IP == params.BackEtcdNode.IP {
    upgrade.BackUpEtcd = true
}
```

只有当**当前升级节点的 IP 等于第一个 etcd 节点的 IP** 时，才在升级命令中启用 etcd 备份。这意味着：
- etcd 备份只在升级第一个 etcd 节点时执行一次
- 后续节点升级时 `BackUpEtcd = false`，避免重复备份

#### 2. 失败策略——立即中断（Fail-Fast）

与 Worker 升级的 Best-Effort 策略**截然不同**，Master 升级采用 **Fail-Fast** 策略：
- 单个 Master 节点升级失败 → **立即返回错误，中断整个升级流程**
- 不继续尝试升级后续节点
- 原因：Master 节点承载控制平面，一个 Master 异常可能导致整个集群不可用，必须立即停止
#### 3. Master 升级后更新集群版本号
```go
bkeCluster.Status.KubernetesVersion = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
```
Master 升级完成后，将 `Status.KubernetesVersion` 更新为目标版本。这是在**所有 Master 节点都升级成功后**才执行的，确保版本号的一致性。
#### 4. kube-proxy 镜像更新
Master 升级完成后，直接通过 Kubernetes API 更新远端集群的 `kube-proxy` DaemonSet 镜像：
- 保持镜像仓库不变（使用 `ModifyImageRepository` 替换为 BKE 配置的仓库）
- 更新镜像 Tag 为目标 K8s 版本
- 同时更新 BKECluster 的 Spec 和 Status 中的 Addon 版本记录
#### 5. kubectl 版本硬编码
```go
if addon.Name == "kubectl" && addon.Version != "v1.25" {
```
kubectl 的目标版本 `v1.25` 是**硬编码**的，这是一个潜在问题——应该与 K8s 版本联动或从配置中获取。
#### 6. 健康检查——Master 节点额外检查
Master 节点的健康检查比 Worker 多一步 `CheckComponentHealth`，会检查：
- kube-apiserver、kube-controller-manager、kube-scheduler 等 Static Pod 的健康状态
- 这是因为 Master 节点升级不仅涉及 kubelet，还涉及控制平面组件
### 八、与 Worker 升级的对比
| 维度 | Master 升级 | Worker 升级 |
|------|------------|------------|
| 升级策略 | 串行逐节点 | 串行逐节点 |
| 失败策略 | **Fail-Fast**（立即中断） | **Best-Effort**（继续下一个） |
| etcd 备份 | 首个 etcd 节点备份 | 不备份 |
| 健康检查 | Ready + 版本 + **组件健康** | 仅 Ready + 版本 |
| 版本号更新 | ✅ 升级后更新 Status.KubernetesVersion | ❌ 不更新 |
| Addon 更新 | ✅ kube-proxy + kubectl | ❌ 不涉及 |
| etcd 注解保障 | ✅ 确保 advertise-client-urls | ❌ 不涉及 |
| Phase 标识 | `UpgradeControlPlane` | `UpgradeWorker` |
| Drainer | 未创建 | 创建但未使用 |
### 九、状态流转图
```
节点状态流转：
  NodeReady
       │
       ▼ (开始升级)
  NodeUpgrading("Upgrading")
       │
       ├── 成功 ▶ NodeNotReady("Upgrading success")
       │              │
       │              ▼ (健康检查通过)
       │           NodeReady
       │
       └── 失败 ▶ NodeUpgradeFailed(err.Error())
                      │
                      ▼ (⚠️ 立即中断，不继续升级后续节点)
                   返回错误，等待下次 Requeue

集群状态流转：
  Status.KubernetesVersion = 旧版本
       │
       ▼ (所有 Master 升级成功后)
  Status.KubernetesVersion = Spec.KubernetesVersion
       │
       ▼ (Addon 更新完成)
  AddonStatus[kubeproxy].Version = 新版本
```
### 十、潜在改进点
1. **kubectl 版本硬编码**：`v1.25` 应该从配置或版本映射表中获取，而非硬编码。
2. **kube-proxy 镜像更新时机**：当前在所有 Master 升级后才更新 kube-proxy，但 kube-proxy 运行在所有节点上（包括未升级的 Worker），可能导致版本不兼容窗口期。
3. **etcd 备份节点选择**：始终选择 `etcdNodes[0]` 作为备份节点，如果该节点恰好不是第一个升级的 Master 节点，则备份不会执行。应确保备份节点在升级节点列表中。
4. **缺少 PreCheck 阶段**：Master 升级前没有检查集群 API Server 可用性、etcd 健康状态等前置条件，直接进入升级流程。
5. **重试机制缺失**：单个 Master 升级失败后，下次 Requeue 会重新从第一个节点开始，已升级成功的节点会因版本预检而跳过，但整体流程缺乏断点续传的显式支持。
        

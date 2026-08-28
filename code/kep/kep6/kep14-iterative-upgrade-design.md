# KEP-14: K8s 核心组件迭代式升级方案设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-14 |
| **标题** | K8s 核心组件迭代式升级：版本偏差约束下的控制面与 kubelet 分离升级 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-26 |
| **依赖** | KEP-5 声明式升级框架、KEP-9 Static Pod 类型设计、KEP-13 二进制组件改造 |

---

## 目录

1. [概述](#1-概述)
2. [kubernetes-master 现有实现分析](#2-kubernetes-master-现有实现分析)
3. [版本偏差约束](#3-版本偏差约束)
4. [设计方案](#4-设计方案)
5. [偏差门控实现](#5-偏差门控实现)
6. [ClusterVersionReconciler 集成](#6-clusterversionreconciler-集成)
7. [升级顺序总结](#7-升级顺序总结)
8. [优势与适用场景](#8-优势与适用场景)
9. [ReleaseImage 中 K8s 核心组件重构方案](#9-releaseimage-中-k8s-核心组件重构方案)
10. [工作量评估](#10-工作量评估)
11. [风险与缓解](#11-风险与缓解)

---

## 1. 概述

### 1.1 设计背景

K8s 核心组件（kube-apiserver、kube-controller-manager、kube-scheduler、kubelet、kube-proxy）在跨多个小版本升级时（如 v1.34→v1.36），需要满足严格的版本偏差约束。当前设计中 `kubernetes-master` 作为 inline/composite 节点，kubeadm 在一个节点上一次性升级所有控制面组件 + kubelet，带来以下问题：

| 问题 | 说明 | 影响 |
|------|------|------|
| 跨 hop 偏差风险 | Hop 1 升级 apiserver 到 1.35，kubelet 还在 1.34；Hop 2 apiserver 到 1.36 时 kubelet 仍在 1.34，偏差为 2（K8s v1.25+ 允许最多 3）；Hop 3 apiserver 到 1.37 时偏差达到 3（极限） | 可能导致 kubelet 版本落后过多 |
| 组件间无法独立编排 | apiserver 和 kubelet 被绑定在同一个 DAG 节点中 | 无法分别控制升级节奏 |
| kubelet 升级阻塞控制面 | 大规模集群 kubelet 逐节点 drain 耗时 | 控制面升级被 kubelet 阻塞 |

### 1.2 设计目标

- 将控制面升级（apiserver/cm/scheduler/kube-proxy）和 kubelet 升级拆分为独立的 DAG 节点
- 通过版本偏差约束动态控制执行顺序
- 支持 kubelet 延迟升级策略（控制面先升级，kubelet 延迟最多 3 个 hop 后批量补充升级）
- 偏差门控确保每个阶段都满足 K8s 版本偏差约束（kubelet/kube-proxy 最多滞后 3，cm/scheduler 最多滞后 1）

## 2. kubernetes-master 现有实现分析

### 2.1 组件注册与映射

在升级组件目录（`pkg/upgrade/catalog.go`）中，`kubernetes-master` 注册为 inline 类型：

```go
var DeclarativeUpgradeCatalog = []UpgradeComponentSpec{
    // ...
    {Name: "kubernetes-master", Mode: UpgradeExecutionInline, InlineHandler: "EnsureMasterUpgrade"},
    // ...
}
```

ReleaseImage 的 `spec.upgrade.components` 中通过 `inline.handler: EnsureMasterUpgrade` 引用：

```yaml
upgrade:
  components:
    - name: kubernetes-master
      version: v1.36.0
      inline:
        handler: EnsureMasterUpgrade
        version: v1.0.0
```

### 2.2 Phase 结构定义

```go
// pkg/phaseframe/phases/ensure_master_upgrade.go

type EnsureMasterUpgrade struct {
    phaseframe.BasePhase
    mockTargetClient kubernetes.Interface  // 测试注入，生产环境为 nil
}

const (
    EnsureMasterUpgradeName confv1beta1.BKEClusterPhase = "EnsureMasterUpgrade"
    MasterUpgradePollIntervalSeconds = 2  // 健康检查轮询间隔
    MasterUpgradeTimeoutMinutes      = 5  // 健康检查超时
)
```

### 2.3 版本解析

`EnsureMasterUpgrade` 通过 `VersionContext` 解析当前版本和目标版本，解析优先级为：

```go
// 目标版本解析优先级
func (e *EnsureMasterUpgrade) desiredKubernetesVersion() string {
    vc := e.GetVersionContext()
    if vc != nil {
        // 1. VersionContext 中 kubernetes-master 的 Target
        if target := vc.GetTarget(upgrade.ComponentKubernetesMaster); target != "" {
            return target
        }
        // 2. VersionContext 中 kubernetes-worker 的 Target
        if target := vc.GetTarget(upgrade.ComponentKubernetesWorker); target != "" {
            return target
        }
        // 3. VersionContext 中 kubernetes 的 Target
        if target := vc.GetTarget("kubernetes"); target != "" {
            return target
        }
    }
    // 4. 回退到 BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion（Legacy）
    return e.deprecatedSpecKubernetesVersion()
}

// 当前版本解析优先级（与目标版本对称）
func (e *EnsureMasterUpgrade) currentKubernetesVersion() string {
    vc := e.GetVersionContext()
    if vc != nil {
        // 1. VersionContext 中 kubernetes-master 的 Current
        // 2. VersionContext 中 kubernetes-worker 的 Current
        // 3. VersionContext 中 kubernetes 的 Current
    }
    // 4. 回退到 BKECluster.Status.KubernetesVersion
    return e.Ctx.BKECluster.Status.KubernetesVersion
}
```

**关键设计**：`kubernetes-master` 和 `kubernetes-worker` 共享同一个 K8s 版本号（`KubernetesVersion`），在 VersionContext 中使用相同的版本值。

### 2.4 NeedExecute 判断逻辑

```go
func (e *EnsureMasterUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. 通用检查（非删除、非暂停、非 DryRun、非终端 Failed）
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    
    // 2. 集群健康检查（Unhealthy/Unknown 时跳过）
    if new.Status.ClusterStatus == bkev1beta1.ClusterUnhealthy ||
       new.Status.ClusterStatus == bkev1beta1.ClusterUnknown {
        return false
    }
    
    // 3. 版本比较（VersionContext 优先，回退到 Legacy 版本比较）
    //    current != target 且都不为空时才需要升级
    if !e.NeedExecuteWithVersionContext(
        upgrade.ComponentKubernetesMaster, old, new,
        e.isKubernetesMasterNeedUpgrade,  // Legacy 回调
    ) {
        return false
    }
    
    // 4. 获取 BKENodes，筛选需要升级的 Master 节点
    bkeNodes, ok := fetchBKENodesIfCPInitialized(e.Ctx, new)
    if !ok {
        return false
    }
    nodes := phaseutil.GetNeedUpgradeMasterNodesWithBKENodes(new, bkeNodes)
    if nodes.Length() == 0 {
        return false  // 没有需要升级的 Master 节点
    }
    
    e.SetStatus(bkev1beta1.PhaseWaiting)
    return true
}
```

### 2.5 Execute 执行流程

`Execute()` 是整个 Master 升级的入口，完整执行流程如下：

```go
func (e *EnsureMasterUpgrade) Execute() (ctrl.Result, error) {
    // 1. 设置 DeployAction annotation（标记集群正在进行 K8s 升级）
    annotation.SetAnnotation(bkeCluster, 
        annotation.DeployActionAnnotationKey, 
        annotation.DeployActionK8sUpgrade)
    
    // 2. 进入主升级逻辑
    return e.reconcileMasterUpgrade()
}
```

**`reconcileMasterUpgrade()` 详细流程**：

```
reconcileMasterUpgrade()
│
├─ 1. 解析版本
│   ├─ targetVersion = desiredKubernetesVersion()  // 目标版本
│   └─ currentVersion = currentKubernetesVersion()  // 当前版本
│
├─ 2. 版本不同才升级
│   if targetVersion != currentVersion:
│       ├─ syncLegacyTargetKubernetesVersion(target)  // 同步到 BKECluster.Spec
│       └─ rolloutUpgrade()  // 执行滚动升级
│   else:
│       └─ log "k8s version same, not need to upgrade"
│
└─ 完成
```

**`rolloutUpgrade()` 详细流程**：

```
rolloutUpgrade()
│
├─ 1. 获取需要升级的 Master 节点
│   ├─ 获取 BKENodes（所有节点）
│   ├─ GetNeedUpgradeMasterNodesWithBKENodes() 筛选 Master 节点
│   │   └─ 比较节点 kubelet 版本 vs 集群目标版本
│   └─ 过滤 BKEAgent 未就绪的节点（跳过）
│       └─ 检查 NodeAgentReadyFlag
│
├─ 2. 确定 etcd 备份节点
│   ├─ 获取 specNodes 中的 etcd 节点
│   ├─ needBackupEtcd = true（如果有 etcd 节点）
│   └─ backEtcdNode = etcdNodes[0]（第一个 etcd 节点作为备份目标）
│
├─ 3. 设置 etcd advertise client URLs annotation
│   └─ ensureEtcdAdvertiseClientUrlsAnnotation(etcdNodes)
│       └─ 为每个 etcd Static Pod 添加 EtcdAdvertiseClientUrlsAnnotationKey
│          （确保后续 etcd 操作能找到 advertise URLs）
│
├─ 4. 逐节点滚动升级（阻塞式，失败则停止）
│   └─ upgradeMasterNodesWithParams():
│       │
│       for each master node:
│       │
│       ├─ 4a. 获取远端 Node 资源
│       │   └─ GetRemoteNodeByBKENode() → remoteNode
│       │
│       ├─ 4b. 跳过已是目标版本的节点
│       │   if remoteNode.Status.NodeInfo.KubeletVersion == targetVersion:
│       │       continue
│       │
│       ├─ 4c. 标记节点为 Upgrading
│       │   └─ SetNodeStateWithMessageForCluster(NodeUpgrading, "Upgrading")
│       │
│       ├─ 4d. 执行节点升级
│       │   └─ upgradeNode():
│       │       │
│       │       ├─ 创建 Upgrade Command CR:
│       │       │   ├─ Phase: UpgradeControlPlane
│       │       │   ├─ NodeSelector: 单节点 IP
│       │       │   ├─ BackUpEtcd: true（仅备份节点）
│       │       │   └─ 命令: Kubeadm UpgradeControlPlane
│       │       │
│       │       ├─ upgrade.New()  → 创建 Command CR
│       │       ├─ upgrade.Wait() → 等待命令完成（2s 轮询，5min 超时）
│       │       │
│       │       │   BKEAgent 接收命令后执行:
│       │       │   ├─ kubeadm upgrade apply <targetVersion>
│       │       │   │   ├─ 下载 kubelet 二进制
│       │       │   │   ├─ 更新 kubelet 配置
│       │       │   │   ├─ 替换 kube-apiserver Static Pod manifest
│       │       │   │   ├─ 替换 kube-controller-manager Static Pod manifest
│       │       │   │   ├─ 替换 kube-scheduler Static Pod manifest
│       │       │   │   ├─ 重启 kubelet
│       │       │   │   └─ 等待 Kubelet 拉起新版本 Static Pod
│       │       │   └─ （如果有 etcd 节点）备份 etcd 数据
│       │       │
│       │       └─ 等待节点健康检查
│       │           └─ waitForNodeHealthCheck():
│       │               ├─ 轮询 Node 状态（2s 间隔，5min 超时）
│       │               ├─ 验证 Node Ready
│       │               └─ 验证 KubeletVersion 匹配目标版本
│       │
│       ├─ 4e. 失败处理（Master 升级是阻塞式的）
│       │   if err:
│       │       └─ 标记 NodeUpgradeFailed
│       │       └─ 返回错误（停止整个升级流程）
│       │
│       └─ 4f. 成功标记
│           └─ SetNodeStateWithMessageForCluster(NodeNotReady, "Upgrading success")
│
├─ 5. 更新集群版本
│   └─ bkeCluster.Status.KubernetesVersion = desiredKubernetesVersion()
│
└─ 6. 更新 addon 版本
    └─ updateAddonVersions():
        ├─ 检查 kube-proxy addon 版本是否需要同步
        ├─ 检查 kubectl addon 版本是否需要同步
        └─ 通过 mergecluster.SyncStatusUntilComplete 更新
```

### 2.6 kubeadm 命令机制

`EnsureMasterUpgrade` 通过 `command.Upgrade` CR 向 BKEAgent 发送升级命令：

```go
// executeNodeUpgradeWithParams() 核心逻辑

masterParams := CreateUpgradeCommandParams{
    Ctx:         params.Ctx,
    Namespace:   params.BKECluster.Namespace,
    Client:      params.Client,
    Scheme:      params.Scheme,
    OwnerObj:    params.BKECluster,
    ClusterName: params.BKECluster.Name,
    Node:        &params.Node,
    BKEConfig:   params.BKECluster.Name,
    Phase:       bkev1beta1.UpgradeControlPlane,  // ← 关键：指定 kubeadm 升级阶段
}

upgrade := createUpgradeCommand(masterParams)

// 如果是 etcd 备份节点，设置备份标记
if params.NeedBackupEtcd && params.Node.IP == params.BackEtcdNode.IP {
    upgrade.BackUpEtcd = true
}

// 创建 Command CR
upgrade.New()

// 等待命令完成
upgrade.Wait()
```

BKEAgent 接收 `Command` CR 后，解析 `Phase=UpgradeControlPlane`，执行内置的 `Kubeadm UpgradeControlPlane` 命令：

```
BKEAgent 执行的命令等价于:
  kubeadm upgrade apply v1.36.0

kubeadm 内部完成的操作:
  1. 预检（检查节点健康、版本兼容性）
  2. 下载新版 kubelet 二进制
  3. 更新 kubelet 配置文件
  4. 替换 etcd Static Pod manifest（如果版本变更）
  5. 替换 kube-apiserver Static Pod manifest
  6. 替换 kube-controller-manager Static Pod manifest
  7. 替换 kube-scheduler Static Pod manifest
  8. 重启 kubelet（加载新配置）
  9. Kubelet 检测 manifest 变化，逐个重建 Static Pod
  10. 等待所有控制面 Pod 就绪
```

### 2.7 kubernetes-master 的黑盒问题

从 2.5 和 2.6 的分析可以看出，`kubernetes-master` 存在以下黑盒问题：

| 问题 | 说明 | 影响 |
|------|------|------|
| **kubeadm 一次性处理所有组件** | apiserver + cm + scheduler + kubelet + etcd 在一个 kubeadm 命令中全部升级 | 无法对单个组件独立控制升级节奏 |
| **kubelet 被绑定在控制面升级中** | kubeadm upgrade apply 内部会升级 kubelet 二进制 | 无法实现 kubelet 延迟升级策略 |
| **无法控制组件升级顺序** | kubeadm 内部决定 apiserver/cm/scheduler 的升级顺序 | 无法按偏差约束精确控制 |
| **无法追踪单组件状态** | 只有 `BKECluster.Status.KubernetesVersion` 一个版本号 | apiserver/cm/scheduler/kubelet 各自版本不可见 |
| **kube-proxy 升级耦合** | `updateAddonVersions()` 中同步 kube-proxy 版本 | kube-proxy 应与 apiserver 同步，但耦合在 Master 升级中 |
| **etcd 备份耦合** | `BackUpEtcd=true` 在 kubeadm 命令内部处理 | 备份逻辑不可见，无法独立控制 |

### 2.8 现有 kubeadm 升级与 K8s 偏差规则的符合性分析

基于代码库 `pkg/job/builtin/kubeadm/kubeadm.go` 分析 BKEAgent 中 `Kubeadm UpgradeControlPlane` 命令的实际执行逻辑及其与 K8s 官方偏差规则的符合性。

#### 2.8.1 BKE 实际升级架构

BKE 并非使用标准 `kubeadm upgrade apply` 命令，而是 BKEAgent 内置了自定义的 `KubeadmPlugin`，分为独立的 Phase：

| Phase | 执行命令 | 代码函数 | 升级内容 | 顺序 |
|-------|---------|---------|---------|------|
| `EnsureEtcdUpgrade` | `Kubeadm UpgradeEtcd` | `upgradeEtcd()` | etcd Static Pod manifest 替换 | **先执行**（独立 Phase） |
| `EnsureMasterUpgrade` | `Kubeadm UpgradeControlPlane` | `upgradeControlPlane()` | apiserver → cm → scheduler → kubelet → kubectl | **后执行** |
| `EnsureWorkerUpgrade` | `Kubeadm UpgradeWorker` | `upgradeWorker()` | kubelet → kubectl | **最后执行** |

**关键发现**：etcd **不在** `upgradeControlPlane` 命令中，而是由独立的 `EnsureEtcdUpgrade` Phase 先执行。`upgradeControlPlane` 仅升级 apiserver/cm/scheduler/kubelet/kubectl。

#### 2.8.2 `upgradeControlPlane()` 实际执行流程

```go
// pkg/job/builtin/kubeadm/kubeadm.go:284

func (k *KubeadmPlugin) upgradeControlPlane(backUpEtcd bool, clusterType string) error {
    // step 1: prepareUpgrade（备份 etcd + 备份集群配置 + 预拉镜像 + 获取 Pod Hash）
    beforeHash, err := k.prepareUpgrade(backUpEtcd, clusterType)

    // step 2: 逐个升级控制面组件（顺序由 GetControlPlaneComponents() 决定）
    for _, component := range mfutil.GetControlPlaneComponents() {
        // GetControlPlaneComponents() 返回: [kube-apiserver, kube-controller-manager, kube-scheduler]
        // 注意: etcd 不在此列表中！
        
        k.upgradeControlPlaneManifestCommand(component)  // 生成新 Static Pod manifest
        k.waitComponentReady(component, podHash)          // 等待新 Pod Running + Ready
    }

    // step 3: 升级 kubelet 二进制
    k.installKubeletCommand(false)

    // step 4: 安装 kubectl 二进制
    k.installKubectlCommand()
}
```

**实际组件升级顺序**：

```
EnsureEtcdUpgrade Phase (独立执行，在 EnsureMasterUpgrade 之前):
  └─ upgradeEtcd():
     ├─ prepareUpgrade: 备份 etcd + 预拉镜像
     ├─ 替换 etcd Static Pod manifest
     │   └─ Kubelet 检测文件变化 → 终止旧 Pod → 创建新 Pod
     └─ 等待 etcd Pod Running + Ready

EnsureMasterUpgrade Phase:
  └─ upgradeControlPlane():
     ├─ prepareUpgrade: 备份 etcd（如果 backUpEtcd=true）+ 备份集群配置 + 预拉镜像
     │
     ├─ kube-apiserver: 替换 manifest
     │   ├─ 写入新 manifest YAML（新镜像版本 tag）到 /etc/kubernetes/manifests/
     │   ├─ Kubelet 通过 inotify 检测到文件变化
     │   ├─ Kubelet 计算 Pod Hash（新 manifest hash ≠ 旧 Pod hash）
     │   ├─ Kubelet 终止旧 apiserver Pod（graceful shutdown）
     │   ├─ Kubelet 创建新 apiserver Pod（新镜像版本）
     │   └─ waitComponentReady: 等待新 Pod Running + Ready
     │      ↑ apiserver 在此步骤完成升级，此时 kubelet 尚未重启
     │
     ├─ kube-controller-manager: 替换 manifest → 同上流程
     ├─ kube-scheduler: 替换 manifest → 同上流程
     │
     ├─ kubelet: 下载二进制 + 安装
     │   ├─ installKubeletCommand: 下载 kubelet 二进制 + 替换
     │   └─ systemctl restart kubelet（重启 kubelet 进程）
     │      ↑ kubelet 重启时：
     │        - apiserver Pod 已在运行（新版本，Hash 匹配）→ 不重启
     │        - cm Pod 已在运行（新版本，Hash 匹配）→ 不重启
     │        - scheduler Pod 已在运行（新版本，Hash 匹配）→ 不重启
     │        （kubelet 重启仅检查 manifest Hash 是否匹配，已匹配的 Pod 跳过）
     │
     └─ kubectl: 下载二进制 + 安装
```

#### 2.8.2a Static Pod manifest 替换的生效机制

**问题：替换 manifest 后立即生效吗？kubelet 重启时 apiserver 会再重启一次吗？**

**回答：替换 manifest 后立即生效，kubelet 重启时不会重复重启 apiserver。**

Static Pod 由 Kubelet 直接管理，Kubelet 通过 inotify 监控 `/etc/kubernetes/manifests/` 目录，检测到文件变化后自动重建 Pod：

```
manifest 替换后的完整生效过程:

1. upgradeControlPlaneManifestCommand(kube-apiserver)
   └─ 写入新 manifest YAML（新镜像 tag）到 /etc/kubernetes/manifests/kube-apiserver.yaml

2. Kubelet 检测到文件变化（inotify 监控 manifests 目录）
   ├─ 计算新 Pod Hash（manifest 内容的 hash）
   ├─ 发现 Hash 变化（旧 Pod hash ≠ 新 manifest hash）
   ├─ 终止旧 Pod（graceful shutdown，等待 terminationGracePeriodSeconds）
   └─ 创建新 Pod（使用新镜像版本）

3. waitComponentReady(kube-apiserver, oldPodHash)
   └─ 轮询等待：
      ├─ Pod Hash 已变化（确认 Kubelet 重建了 Pod）
      ├─ 新 Pod 状态 = Running
      └─ 新 Pod Ready 条件 = True
```

**kubelet 重启时的 Static Pod 行为**：

```
kubelet 重启（systemctl restart kubelet）时:
  1. 扫描 /etc/kubernetes/manifests/ 目录
  2. 对每个 manifest 文件:
     ├─ 如果对应 Pod 已在运行且 Hash 匹配 → 跳过（不重启）
     └─ 如果 Pod 不存在或 Hash 不匹配 → 创建 Pod
  3. 如果 manifest 文件已删除 → 终止对应 Pod
```

**结论**：

| 问题 | 答案 | 说明 |
|------|------|------|
| 替换 manifest 后立即生效吗？ | **是** | Kubelet 通过 inotify 监控 manifests 目录，检测到变化后自动终止旧 Pod → 创建新 Pod |
| kubelet 重启时 apiserver 会再重启一次吗？ | **不会** | kubelet 重启时检查 manifest Hash，已匹配的 Pod 跳过，不重复创建 |
| apiserver 和 kubelet 谁先升级？ | **apiserver 先** | apiserver 在 manifest 替换步骤升级（Kubelet 自动重建 Pod），kubelet 在后续步骤升级（二进制替换 + systemctl restart） |

#### 2.8.3 单节点升级期间的偏差状态

基于 2.8.2 的实际执行顺序，分析各时刻的偏差状态（以 v1.34 → v1.35 升级为例）：

**阶段一：`EnsureEtcdUpgrade` 执行期间**

| 时刻 | etcd | apiserver | cm/scheduler | kubelet | kubectl | kube-proxy | 偏差状态分析 |
|------|------|-----------|-------------|---------|---------|------------|-------------|
| T0（升级前） | v3.5.18 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ 全部一致 |
| T1（etcd manifest 替换后） | **v3.5.19** | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ etcd 版本独立，不影响 K8s 组件偏差 |

**阶段二：`EnsureMasterUpgrade` / `upgradeControlPlane()` 执行期间**

| 时刻 | etcd | apiserver | cm/scheduler | kubelet | kubectl | kube-proxy | 偏差状态分析 |
|------|------|-----------|-------------|---------|---------|------------|-------------|
| T2（prepareUpgrade 完成后） | v3.5.19 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ 无偏差（仅备份和预拉镜像） |
| T3（apiserver manifest 替换 + Ready 后） | v3.5.19 | **v1.35** | v1.34 | v1.34 | v1.34 | v1.34 | ✅ cm(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 1）；kubelet(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3）；kubectl(1.34) vs apiserver(1.35) = 1 偏差（允许双向 ±1）；kube-proxy(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3） |
| T4（cm manifest 替换 + Ready 后） | v3.5.19 | v1.35 | **v1.35** | v1.34 | v1.34 | v1.34 | ✅ kubelet(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3）；kubectl(1.34) vs apiserver(1.35) = 1 偏差（允许 ±1）；kube-proxy(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3） |
| T5（scheduler manifest 替换 + Ready 后） | v3.5.19 | v1.35 | v1.35 | v1.34 | v1.34 | v1.34 | ✅ 同 T4（scheduler 已与 cm 一致） |
| T6（kubelet 二进制替换后） | v3.5.19 | v1.35 | v1.35 | **v1.35** | v1.34 | v1.34 | ✅ kubectl(1.34) vs apiserver(1.35) = 1 偏差（允许 ±1）；kube-proxy(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3） |
| T7（kubectl 二进制安装后） | v3.5.19 | v1.35 | v1.35 | v1.35 | **v1.35** | v1.34 | ✅ kube-proxy(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3） |

**阶段三：`updateAddonVersions()` 执行期间**

| 时刻 | etcd | apiserver | cm/scheduler | kubelet | kubectl | kube-proxy | 偏差状态分析 |
|------|------|-----------|-------------|---------|---------|------------|-------------|
| T8（kube-proxy 版本同步后） | v3.5.19 | v1.35 | v1.35 | v1.35 | v1.35 | **v1.35** | ✅ 全部一致，无偏差 |

**分析结论**：

| 偏差规则 | 单节点升级期间是否满足 | 说明 |
|---------|---------------------|------|
| kubelet ≤ apiserver（允许滞后 3） | ✅ 满足 | kubelet 在 apiserver 之后升级（T3→T6），最大偏差 1（远小于允许的 3） |
| kube-proxy ≤ apiserver（允许滞后 3） | ✅ 满足 | kube-proxy 在 apiserver 之后由 `updateAddonVersions()` 同步升级（T3→T8），最大偏差 1（远小于允许的 3） |
| cm/scheduler ≤ apiserver（允许滞后 1） | ✅ 满足 | kubeadm 在 apiserver 后逐个升级 cm/scheduler（T3→T4→T5），最大偏差 1（正好等于允许的极限 1） |
| kubectl vs apiserver（允许双向 ±1） | ✅ 满足 | kubectl 在 kubelet 之后升级（T6→T7），最大偏差 1（正好等于允许的极限 1） |
| etcd 与 apiserver 配套 | ✅ 满足 | etcd 在独立的 `EnsureEtcdUpgrade` Phase 中先升级，`EnsureMasterUpgrade` 执行时 etcd 已就绪 |

#### 2.8.4 多节点 Master 集群的偏差状态

对于多 Master 节点集群，`EnsureEtcdUpgrade` 和 `EnsureMasterUpgrade` 分别逐节点滚动升级（阻塞式），各节点版本如下：

| 阶段 | node-1 apiserver | node-2 apiserver | node-1 kubelet | node-2 kubelet | node-1 kubectl | node-2 kubectl | node-1 kube-proxy | node-2 kube-proxy | 偏差状态分析 |
|------|-----------------|-----------------|----------------|----------------|---------------|---------------|------------------|------------------|-------------|
| 升级前 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ 一致 |
| node-1 EnsureEtcdUpgrade 完成 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ etcd 升级不影响 K8s 组件版本 |
| node-1 EnsureMasterUpgrade 完成 | **v1.35** | v1.34 | **v1.35** | v1.34 | **v1.35** | v1.34 | **v1.35** | v1.34 | ✅ HA 中 apiserver 实例间偏差 1（允许 1）；node-2 的 kubelet/kubectl/kube-proxy(1.34) vs apiserver(1.34) = 0 偏差（自身无偏差） |
| node-2 EnsureEtcdUpgrade 完成 | v1.35 | v1.34 | v1.35 | v1.34 | v1.35 | v1.34 | v1.35 | v1.34 | ✅ 同上 |
| node-2 EnsureMasterUpgrade 完成 | v1.35 | **v1.35** | v1.35 | **v1.35** | v1.35 | **v1.35** | v1.35 | **v1.35** | ✅ 全部一致 |

**分析结论**：

多节点滚动升级期间，**单个节点内的偏差**由 `upgradeControlPlane()` 内部逐组件升级管理（见 2.8.3），**跨节点偏差**通过逐节点升级保证安全。

| 偏差规则 | 多节点升级期间是否满足 | 说明 |
|---------|---------------------|------|
| **HA apiserver 实例间偏差** | ✅ 满足 | node-1 完成(1.35) vs node-2 未完成(1.34) = 1 偏差（允许 1） |
| **kubelet vs apiserver** | ✅ 满足 | node-2 的 kubelet(1.34) vs 最低 apiserver(1.34) = 0 偏差；node-2 的 kubelet(1.34) vs 最高 apiserver(1.35) = 1 偏差（允许 3） |
| **kubectl vs apiserver** | ✅ 满足 | node-2 的 kubectl(1.34) vs 最低 apiserver(1.34) = 0 偏差；vs 最高 apiserver(1.35) = 1 偏差（允许 ±1） |
| **kube-proxy vs apiserver** | ✅ 满足 | node-2 的 kube-proxy(1.34) vs 最低 apiserver(1.34) = 0 偏差；vs 最高 apiserver(1.35) = 1 偏差（允许 3） |
| **cm/scheduler vs apiserver** | ✅ 满足 | `upgradeControlPlane()` 在每个节点内同步升级 cm/scheduler，不跨节点偏差 |

> **注意**：在 node-1 完成而 node-2 未完成时，集群存在两个版本的 apiserver（HA 场景下可接受，官方允许 apiserver 实例间最多偏差 1 个小版本）。此时 node-2 上的 kubelet(1.34)/kubectl(1.34)/kube-proxy(1.34) 与 node-1 的 apiserver(1.35) 之间偏差为 1，均在允许范围内。

#### 2.8.5 多 hop 升级的偏差问题

**关键问题：`upgradeControlPlane()` 强制升级 kubelet，无法支持多 hop 延迟升级策略。**

`upgradeControlPlane()` 的 step 3 固定执行 `installKubeletCommand(false)`，kubelet 二进制在每个 hop 中被强制替换，无法跳过：

```go
// step 5 upgrade kubelet  ← 固定步骤，无法跳过
log.Infof("upgrade kubelet for cluster %s", k.clusterName)
if err := k.installKubeletCommand(false); err != nil {
    return err
}
```

这意味着：

```
当前 BKE 行为（无法延迟 kubelet）:

Hop 1: EnsureEtcdUpgrade + EnsureMasterUpgrade
  └─ upgradeControlPlane():
     ├─ apiserver: v1.34 → v1.35  ✓
     ├─ cm/scheduler: v1.34 → v1.35  ✓
     ├─ kubelet: v1.34 → v1.35  ← 强制升级，无法延迟
     └─ kubectl: v1.34 → v1.35  ✓

  偏差状态: kubelet(1.35) vs apiserver(1.35) = 0 偏差

Hop 2: EnsureEtcdUpgrade + EnsureMasterUpgrade
  └─ upgradeControlPlane():
     ├─ apiserver: v1.35 → v1.36  ✓
     ├─ cm/scheduler: v1.35 → v1.36  ✓
     ├─ kubelet: v1.35 → v1.36  ← 强制升级，无法延迟
     └─ kubectl: v1.35 → v1.36  ✓

  偏差状态: kubelet(1.36) vs apiserver(1.36) = 0 偏差
```

| 对比维度 | BKE 当前行为 | K8s 偏差规则允许的 | 差距 |
|---------|-------------|------------------|------|
| kubelet 升级时机 | 与 apiserver 同步升级（`upgradeControlPlane` 内固定步骤） | 可滞后 apiserver 最多 3 个小版本（v1.25+） | 无法利用偏差窗口延迟 kubelet |
| kubelet drain 次数 | 每个 hop 都 drain 所有节点（kubelet 在 control plane 升级中） | 理论上可 3 个 hop drain 一次（利用偏差窗口） | 大集群 drain 次数最多增加 3 倍 |
| 控制面升级速度 | 被 kubelet 二进制替换阻塞（在 cm/scheduler 之后执行） | 控制面可先升级，kubelet 后续批量 | 大集群控制面升级被延迟 |

#### 2.8.6 结论

| 维度 | BKE 现有实现 | K8s 偏差规则要求 | 符合性 |
|------|-------------|-----------------|--------|
| **单节点升级期间偏差** | cm/scheduler 短暂落后 apiserver（T3→T4 窗口） | cm/scheduler 最多落后 apiserver 1 个小版本 | ✅ 满足（最大偏差 1，正好等于允许的极限） |
| **kube-proxy 同步** | apiserver 升级后 kube-proxy 在 `updateAddonVersions()` 中同步 | kube-proxy 最多落后 apiserver 3 个小版本 | ✅ 满足（最大偏差 1，远小于允许的 3） |
| **kubelet 同步** | kubelet 在 cm/scheduler 之后、在 `upgradeControlPlane` 内升级 | kubelet 最多落后 apiserver 3 个小版本 | ✅ 满足（最大偏差 1，远小于允许的 3） |
| **etcd 升级** | etcd 由独立 `EnsureEtcdUpgrade` Phase 先升级 | 每 K8s 版本有推荐 etcd 版本 | ✅ 满足（etcd 先于 apiserver 升级） |
| **多 hop kubelet 延迟** | 不支持，`upgradeControlPlane` 内 kubelet 固定升级 | 允许最多滞后 3 个小版本（v1.25+） | ❌ **无法利用 3 版本偏差窗口优化大集群升级** |
| **跨节点偏差** | 逐节点滚动升级，集群短暂存在两个版本 apiserver | HA 中 apiserver 实例间最多 1 个小版本偏差 | ✅ 满足 |

**总结**：BKE 的单节点升级**完全满足**偏差规则（所有组件偏差均在允许范围内），但 `upgradeControlPlane()` 内 kubelet 是固定步骤无法跳过，**无法利用 K8s 允许的 3 版本偏差窗口**来优化多 hop 升级的 kubelet drain 效率。这正是 KEP-14 要解决的核心问题：**拆解 `upgradeControlPlane` 黑盒，将 kubelet 升级从控制面升级中分离，实现延迟升级**。

| 追踪字段 | 位置 | 说明 |
|---------|------|------|
| `BKECluster.Status.KubernetesVersion` | 集群级 | K8s 版本（apiserver/cm/scheduler/kubelet 共用） |
| `BKECluster.Status.EtcdVersion` | 集群级 | etcd 版本 |
| `Node.Status.NodeInfo.KubeletVersion` | 节点级 | kubelet 版本（从目标集群 Node 资源读取） |
| `BKECluster.Status.AddonStatus[].Version` | 集群级 | addon 版本（kube-proxy/kubectl） |

**问题**：apiserver/cm/scheduler 没有独立版本追踪，全部归为 `KubernetesVersion`。拆解后需要为每个组件添加独立的版本追踪。

## 3. 版本偏差约束

### 3.1 K8s 官方偏差规则

> 以下内容基于 K8s 官方 Version Skew Policy（https://kubernetes.io/releases/version-skew-policy/），截至 K8s v1.37。

#### 3.1.1 kube-apiserver（HA 集群）

在 HA 集群中，最新和最旧的 `kube-apiserver` 实例之间最多相差 **1 个小版本**。

| 示例 | 说明 |
|------|------|
| 最新 kube-apiserver = v1.37 | 其他实例支持 v1.37 和 v1.36 |

#### 3.1.2 kubelet

- `kubelet` **不能比** `kube-apiserver` 新
- `kubelet` 最多比 `kube-apiserver` 旧 **3 个小版本**（v1.25 以下最多旧 2 个小版本）

| 示例 | 说明 |
|------|------|
| kube-apiserver = v1.37 | kubelet 支持 v1.37、v1.36、v1.35、v1.34 |

> **注意**：如果 HA 集群中 kube-apiserver 实例之间存在版本偏差，则 kubelet 的允许范围会收窄。例如 apiserver 为 v1.37 和 v1.36 时，kubelet 支持 v1.36、v1.35、v1.34（不支持 v1.37，因为会比 v1.36 的 apiserver 新）。

#### 3.1.3 kube-proxy

- `kube-proxy` **不能比** `kube-apiserver` 新
- `kube-proxy` 最多比 `kube-apiserver` 旧 **3 个小版本**（v1.25 以下最多旧 2 个小版本）
- `kube-proxy` 可以比同节点的 `kubelet` 最多旧或新 **3 个小版本**（v1.25 以下最多 2 个小版本）

| 示例 | 说明 |
|------|------|
| kube-apiserver = v1.37 | kube-proxy 支持 v1.37、v1.36、v1.35、v1.34 |

#### 3.1.4 kube-controller-manager / kube-scheduler / cloud-controller-manager

- **不能比** `kube-apiserver` 新
- 应与 `kube-apiserver` **小版本一致**，但允许落后 **1 个小版本**（支持滚动升级）

| 示例 | 说明 |
|------|------|
| kube-apiserver = v1.37 | cm/scheduler 支持v1.37 和 v1.36 |

#### 3.1.5 kubectl

- `kubectl` 支持与 `kube-apiserver` 相差 **1 个小版本**（旧或新均可）

| 示例 | 说明 |
|------|------|
| kube-apiserver = v1.37 | kubectl 支持 v1.38、v1.37、v1.36 |

#### 3.1.6 官方偏差规则汇总表

| 组件 | 偏差方向 | 最大偏差 | 约束说明 |
|------|---------|---------|---------|
| **kube-apiserver（HA）** | 最新 vs 最旧 | 1 个小版本 | HA 集群中 apiserver 实例间最多相差 1 个小版本 |
| **kubelet vs apiserver** | kubelet ≤ apiserver | 3 个小版本（v1.25 以下为 2） | kubelet 不能比 apiserver 新 |
| **kube-proxy vs apiserver** | kube-proxy ≤ apiserver | 3 个小版本（v1.25 以下为 2） | kube-proxy 不能比 apiserver 新 |
| **kube-proxy vs kubelet** | 双向 | 3 个小版本（v1.25 以下为 2） | kube-proxy 可以比 kubelet 旧或新 |
| **cm/scheduler vs apiserver** | cm/scheduler ≤ apiserver | 1 个小版本 | 应与 apiserver 一致，允许落后 1 个小版本 |
| **kubectl vs apiserver** | 双向 | 1 个小版本 | kubectl 可以比 apiserver 旧或新 1 个小版本 |

#### 3.1.7 官方支持的组件升级顺序

K8s 官方推荐的组件升级顺序（从 v1.36 升级到 v1.37 示例）：

| 顺序 | 组件 | 前置条件 | 说明 |
|------|------|---------|------|
| 1 | **kube-apiserver** | HA 中所有 apiserver 在 v1.36 或 v1.37；cm/scheduler 在 v1.36；kubelet 在 v1.35+ | apiserver 不能跳小版本升级 |
| 2 | **cm/scheduler** | apiserver 已升级到 v1.37 | 三个组件无固定顺序，可同时升级 |
| 3 | **kubelet**（可选） | apiserver 已升级到 v1.37 | 可留在 v1.36/v1.35/v1.34；升级前需 drain 节点 |
| 4 | **kube-proxy**（可选） | apiserver 已升级到 v1.37 | 可留在 v1.36/v1.35/v1.34 |

> **关键设计依据**：kubelet 和 kube-proxy 是**可选升级**的，可以滞后 apiserver 最多 3 个小版本。这是 KEP-14 实现 kubelet 延迟升级策略的官方依据。

#### 3.1.8 containerd 不在 K8s Version Skew Policy 中的原因

K8s 官方 Version Skew Policy **不包含 containerd**，因为 containerd 不是 K8s 核心组件：

| 维度 | K8s 核心组件（apiserver/kubelet 等） | containerd |
|------|--------------------------------------|------------|
| **归属** | K8s 项目组件 | 独立项目（CNCF，非 K8s 核心） |
| **版本管理** | K8s 统一版本号（x.y.z） | 独立版本号（如 v1.7.24） |
| **偏差规则** | K8s 官方制定 | 无官方偏差规则 |
| **兼容性机制** | 版本偏差约束 | CRI API 兼容性 + kubeadm 推荐版本 |

containerd 的版本兼容性通过以下机制管理（非 Version Skew Policy）：

| 约束 | 说明 |
|------|------|
| **CRI API 兼容** | kubelet 通过 CRI（Container Runtime Interface）与 containerd 通信，CRI API 版本兼容即可，不要求 containerd 版本与 K8s 版本一致 |
| **kubeadm 推荐版本** | 每个 K8s 版本的 kubeadm 配置中有推荐的 containerd 版本，kubeadm upgrade 时会检查 |
| **无版本偏差规则** | containerd 可以跨多个版本运行（如 containerd v1.7.x 可配合 K8s v1.28~v1.37），无官方偏差限制 |

**对 KEP-14 的影响**：

`K8sSkewConstraints` 中**不包含 containerd**，因为：

1. containerd 没有与 apiserver 的偏差约束
2. containerd 的版本兼容性由 CRI API 保证，不需要偏差门控
3. containerd 升级可以独立于 K8s 版本升级（在 ReleaseImage 中定义为独立组件）

但为确保 containerd 与 kubelet 的 CRI 兼容性，增加一个**软约束**（非 K8s Version Skew Policy，而是兼容性检查）：

```go
// pkg/upgrade/container_runtime_compatibility.go

// ContainerRuntimeCompatibilityRule 容器运行时兼容性规则
// 非 K8s Version Skew Policy，而是 kubeadm 推荐版本检查
type ContainerRuntimeCompatibilityRule struct {
    // K8s 版本范围（semver range）
    K8sVersionRange string
    // 推荐的最低 containerd 版本
    MinContainerdVersion string
    // 推荐的最高 containerd 版本（可选）
    MaxContainerdVersion string
}

// ContainerRuntimeCompatibilityRules 容器运行时兼容性规则表
// 基于 kubeadm 推荐配置，非 K8s 官方偏差规则
var ContainerRuntimeCompatibilityRules = []ContainerRuntimeCompatibilityRule{
    {K8sVersionRange: ">=v1.34", MinContainerdVersion: "v1.7.20"},
    {K8sVersionRange: ">=v1.35", MinContainerdVersion: "v1.7.22"},
    {K8sVersionRange: ">=v1.36", MinContainerdVersion: "v1.7.24"},
    {K8sVersionRange: ">=v1.37", MinContainerdVersion: "v1.7.26"},
}

// CheckContainerRuntimeCompatibility 检查 containerd 版本与 K8s 版本的兼容性
// 返回: 兼容=true, 不兼容=false + 建议版本
func CheckContainerRuntimeCompatibility(
    k8sVersion string,
    containerdVersion string,
) (bool, string) {
    for _, rule := range ContainerRuntimeCompatibilityRules {
        if matchVersionRange(k8sVersion, rule.K8sVersionRange) {
            if compareVersions(containerdVersion, rule.MinContainerdVersion) < 0 {
                return false, rule.MinContainerdVersion
            }
            return true, ""
        }
    }
    return true, ""  // 无匹配规则，默认兼容
}
```

**与偏差门控的区别**：

| 维度 | Version Skew 偏差门控 | 容器运行时兼容性检查 |
|------|----------------------|-------------------|
| **适用组件** | kubelet/kube-proxy/cm/scheduler/kubectl vs apiserver | containerd vs kubelet |
| **规则来源** | K8s 官方 Version Skew Policy | kubeadm 推荐配置（非官方偏差规则） |
| **检查时机** | 每 hop 间（偏差门控） | 升级前预检（PreCheck） |
| **违反后果** | 阻止继续升级 | 告警，不阻止升级（CRI API 兼容即可运行） |
| **在 KEP-14 中的角色** | 核心机制（偏差门控） | 辅助检查（PreCheck 中的兼容性验证） |

### 3.2 偏差约束声明

```go
// pkg/upgrade/skew_constraints.go

// SkewConstraint 版本偏差约束定义
type SkewConstraint struct {
    // 被约束的组件名称
    Component string
    // 参照组件名称
    ReferenceComponent string
    // 最大版本偏差（被约束组件最多比参照组件旧几个小版本）
    // kubelet/kube-proxy: 3（v1.25+）
    // cm/scheduler/kubectl: 1
    MaxSkewBehind int
}

// K8sSkewConstraints K8s 标准版本偏差约束
// 基于 K8s 官方 Version Skew Policy (v1.25+)
var K8sSkewConstraints = []SkewConstraint{
    {
        Component:         "kubelet",
        ReferenceComponent: "kube-apiserver",
        MaxSkewBehind:     3,  // kubelet 最多比 apiserver 旧 3 个小版本（v1.25+）
    },
    {
        Component:         "kube-proxy",
        ReferenceComponent: "kube-apiserver",
        MaxSkewBehind:     3,  // kube-proxy 最多比 apiserver 旧 3 个小版本（v1.25+）
    },
    {
        Component:         "kube-controller-manager",
        ReferenceComponent: "kube-apiserver",
        MaxSkewBehind:     1,  // cm 最多比 apiserver 旧 1 个小版本
    },
    {
        Component:         "kube-scheduler",
        ReferenceComponent: "kube-apiserver",
        MaxSkewBehind:     1,  // scheduler 最多比 apiserver 旧 1 个小版本
    },
    {
        Component:         "kubectl",
        ReferenceComponent: "kube-apiserver",
        MaxSkewBehind:     1,  // kubectl 最多比 apiserver 旧 1 个小版本
    },
}
```

### 3.3 偏差计算

```go
// computeMinorVersionSkew 计算两个版本的小版本偏差
// 返回值: 正数表示 reference 比 component 新 N 个小版本
//         负数表示 component 比 reference 新（违反约束）
//         0 表示版本一致
func computeMinorVersionSkew(reference, component string) int {
    refMinor := parseMinorVersion(reference)   // "1.36" → 36
    compMinor := parseMinorVersion(component)   // "1.34" → 34
    return refMinor - compMinor
}

// parseMinorVersion 从版本号中提取小版本号
// "v1.36.0" → 36, "v1.34.2" → 34
func parseMinorVersion(version string) int {
    // 去除 v 前缀
    v := strings.TrimPrefix(version, "v")
    parts := strings.Split(v, ".")
    if len(parts) < 2 {
        return 0
    }
    minor, _ := strconv.Atoi(parts[1])
    return minor
}
```

## 4. 设计方案

### 4.1 核心思路

**将 K8s 核心组件（etcd、apiserver、cm、scheduler、kubelet、kubectl、kube-proxy）拆分为独立的 DAG 节点，通过版本偏差约束动态控制执行顺序，实现 kubelet 延迟升级。**

```
传统方式（BKE upgradeControlPlane 黑盒）:
  Hop 1: EnsureEtcdUpgrade → etcd 升级
          EnsureMasterUpgrade → apiserver + cm + scheduler + kubelet + kubectl 全部升级
  Hop 2: EnsureEtcdUpgrade → etcd 升级
          EnsureMasterUpgrade → apiserver + cm + scheduler + kubelet + kubectl 全部升级

本方案（分离升级 + 偏差门控）:
  Hop 1: K8s 核心组件升级（不含 kubelet）
    ├─ etcd: v3.5.18 → v3.5.19（StaticPod，先升级数据存储）
    ├─ kube-apiserver: v1.34 → v1.35（StaticPod，控制面入口）
    ├─ kube-controller-manager: v1.34 → v1.35（StaticPod，跟随 apiserver）
    ├─ kube-scheduler: v1.34 → v1.35（StaticPod，跟随 apiserver）
    ├─ kube-proxy: v1.34 → v1.35（YAML Apply，匹配 apiserver）
    ├─ kubectl: v1.34 → v1.35（Binary，命令行工具）
    └─ kubelet: 保持 v1.34（延迟升级）
  偏差门 1: kubelet(v1.34) vs apiserver(v1.35) → 1 偏差 → ✅ 通过（允许 3）

  Hop 2: K8s 核心组件升级（不含 kubelet）
    ├─ etcd: v3.5.19 → v3.5.20（StaticPod）
    ├─ kube-apiserver: v1.35 → v1.36（StaticPod）
    ├─ kube-controller-manager: v1.35 → v1.36（StaticPod）
    ├─ kube-scheduler: v1.35 → v1.36（StaticPod）
    ├─ kube-proxy: v1.35 → v1.36（YAML Apply）
    ├─ kubectl: v1.35 → v1.36（Binary）
    └─ kubelet: 保持 v1.34（2 偏差，安全）
  偏差门 2: kubelet(v1.34) vs apiserver(v1.36) → 2 偏差 → ✅ 安全（允许 3）

  Hop 3: K8s 核心组件升级（不含 kubelet）
    ├─ etcd: v3.5.20 → v3.5.21（StaticPod）
    ├─ kube-apiserver: v1.36 → v1.37（StaticPod）
    ├─ kube-controller-manager: v1.36 → v1.37（StaticPod）
    ├─ kube-scheduler: v1.36 → v1.37（StaticPod）
    ├─ kube-proxy: v1.36 → v1.37（YAML Apply）
    ├─ kubectl: v1.36 → v1.37（Binary）
    └─ kubelet: 保持 v1.34（3 偏差，达到极限）
  偏差门 3: kubelet(v1.34) vs apiserver(v1.37) → 3 偏差 → ⚠️ 达到极限，必须升级 kubelet

  Kubelet 补充升级: v1.34→v1.35→v1.36→v1.37（逐节点 drain → replace → uncordon）
  最终偏差验证: 0 偏差 → ✅ K8s 核心组件全部升级完成

  ─── K8s 核心组件全部升级完成后，执行其它组件升级 ───

  其它组件升级 → containerd / bkeagent / runc / lxcfs / ...
    ├─ containerd: 检查 CRI 兼容性 → ENV 命令升级
    ├─ bkeagent: SSH 推送新版本
    ├─ runc: BinaryInstaller 替换二进制
    └─ ...
  最终验证: 所有组件版本与目标 ReleaseImage 一致 → ✅ 升级完成
```

**K8s 核心组件清单及类型**：

| 组件 | 类型 | 升级方式 | 偏差规则 | 是否延迟升级 | 原因 |
|------|------|---------|---------|------------|------|
| **etcd** | staticpod | StaticPodInstaller manifest 替换 | 与 K8s 版本配套 | ❌ 每 hop 同步 | 数据存储，需先于 apiserver 升级 |
| **kube-apiserver** | staticpod | StaticPodInstaller manifest 替换 | 参照组件 | ❌ 每 hop 同步 | 控制面入口，偏差参照基准 |
| **kube-controller-manager** | staticpod | StaticPodInstaller manifest 替换 | ≤ apiserver 1 | ❌ 每 hop 同步 | 偏差窗口仅 1，必须紧跟 apiserver |
| **kube-scheduler** | staticpod | StaticPodInstaller manifest 替换 | ≤ apiserver 1 | ❌ 每 hop 同步 | 偏差窗口仅 1，必须紧跟 apiserver |
| **kube-proxy** | yaml | YamlInstaller SSA Apply | ≤ apiserver 3 | ❌ 每 hop 同步 | DaemonSet 滚动更新，无需逐节点 drain，不阻塞 |
| **kubectl** | binary | BinaryInstaller 替换二进制 | vs apiserver ±1 | ❌ 每 hop 同步 | 偏差窗口仅 1；仅替换二进制，无需 drain |
| **kubelet** | binary | BinaryInstaller drain → 替换 → uncordon | ≤ apiserver 3 | ✅ **延迟升级** | drain 耗时长，利用 3 版本偏差窗口集中升级 |

**为什么不延迟 kubectl？**

kubectl 的偏差规则允许与 apiserver 相差 ±1 个小版本（比 kubelet 的 3 个小版本更严格），延迟 kubectl 会很快触及偏差极限。且 kubectl 是命令行工具，升级无需 drain 节点（仅替换二进制），不阻塞控制面升级，因此 kubectl 随控制面同步升级。

**为什么不延迟 kube-proxy？**

kube-proxy 的偏差规则允许滞后 apiserver 3 个小版本（与 kubelet 相同），但 kube-proxy 通过 DaemonSet 滚动更新升级，无需逐节点 drain，不阻塞控制面升级流程。因此 kube-proxy 随控制面同步升级。

### 4.2 多阶段升级编排：K8s 组件优先 + 其它组件后续

**核心设计**：升级编排分为两大阶段，K8s 核心组件（受偏差约束）优先完成所有 hop 升级，然后再升级其它组件（不受偏差约束，可独立升级）。

```
完整升级编排流程 (openFuyao v2.6.0 → v2.7.0, K8s v1.34 → v1.36):

┌─────────────────────────────────────────────────────────────────────────┐
│  阶段一: K8s 核心组件多 Hop 升级（受偏差约束管控）                        │
│                                                                         │
│  Hop 1: 控制面升级 (K8s v1.34 → v1.35)                                  │
│    ├─ etcd → v3.5.19                                                    │
│    ├─ kube-apiserver → v1.35                                            │
│    ├─ cm/scheduler → v1.35                                              │
│    ├─ kube-proxy → v1.35                                                │
│    ├─ kubectl → v1.35                                                  │
│    └─ kubelet: 保持 v1.34（延迟升级）                                    │
│  偏差门 1: kubelet(1.34) vs apiserver(1.35) → 1 偏差 → ✅ 安全          │
│                                                                         │
│  Hop 2: 控制面升级 (K8s v1.35 → v1.36)                                  │
│    ├─ etcd → v3.5.20                                                    │
│    ├─ kube-apiserver → v1.36                                            │
│    ├─ cm/scheduler → v1.36                                              │
│    ├─ kube-proxy → v1.36                                               │
│    ├─ kubectl → v1.36                                                  │
│    └─ kubelet: 保持 v1.34（偏差 2，安全）                                │
│  偏差门 2: kubelet(1.34) vs apiserver(1.36) → 2 偏差 → ✅ 安全          │
│                                                                         │
│  Kubelet 补充升级: v1.34→v1.35→v1.36（逐节点 drain → replace → uncordon）│
│  最终偏差验证: 0 偏差 → ✅ K8s 核心组件升级完成                          │
│                                                                         │
│  ─────────────────────────────────────────────────────────────────────  │
│                                                                         │
│  阶段二: 其它组件升级（不受偏差约束，独立升级）                           │
│                                                                         │
│  ├─ containerd: v1.7.20 → v1.7.24                                      │
│  │   └─ CRI 兼容性检查（CheckContainerRuntimeCompatibility）→ ENV 命令   │
│  │                                                                       │
│  ├─ bkeagent: v2.6.0 → v2.7.0                                          │
│  │   └─ SSH 推送新版本二进制                                             │
│  │                                                                       │
│  ├─ runc: v1.1.10 → v1.1.12                                            │
│  │   └─ BinaryInstaller 替换二进制                                       │
│  │                                                                       │
│  ├─ lxcfs: v5.0.2 → v6.0.2                                             │
│  │   └─ BinaryInstaller 替换二进制 + systemd service                     │
│  │                                                                       │
│  └─ ...（其它辅助组件）                                                  │
│                                                                         │
│  最终验证: 所有组件版本与目标 ReleaseImage 一致 → ✅ 升级完成             │
└─────────────────────────────────────────────────────────────────────────┘
```

**为什么分两阶段？**

| 维度 | 阶段一（K8s 核心组件） | 阶段二（其它组件） |
|------|----------------------|-------------------|
| **偏差约束** | 受 K8s Version Skew Policy 约束 | 不受偏差约束 |
| **编排方式** | 多 hop + 偏差门控 + kubelet 延迟 | 独立 DAG，无偏差门控 |
| **升级顺序** | 严格按 K8s 官方升级顺序（apiserver → cm/scheduler → kube-proxy/kubectl → kubelet） | 按 ComponentVersion.spec.dependencies 排序 |
| **失败影响** | 可能导致集群不可用 | 不影响 K8s 控制面可用性 |
| **幂等性** | kubelet 补充升级跳过已完成节点 | 各组件独立幂等 |
| **执行方式** | ClusterVersionReconciler 逐 hop 编排 | DAG Scheduler 统一调度 |

**containerd 在阶段二而非阶段一的原因**：

1. containerd 不受 K8s Version Skew Policy 约束（见 3.1.8），无需参与偏差门控
2. containerd 升级通过 CRI API 兼容性保证，不需要与 apiserver 同步
3. containerd 可以在 K8s 组件升级完成后再独立升级（ENV 命令重置 + 重新部署）
4. 但需在阶段二执行前做 CRI 兼容性预检（`CheckContainerRuntimeCompatibility`）

**阶段间的衔接**：

```go
// controllers/clusterversion/clusterversion_controller.go

func (r *ClusterVersionReconciler) orchestrateFullUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopPath []string, // ["v2.6.5", "v2.7.0"] (openFuyao 版本)
) error {
    // ─── 阶段一: K8s 核心组件多 Hop 升级 ───
    log.Info("phase 1: K8s core components multi-hop upgrade")
    if err := r.orchestrateMultiHopUpgrade(ctx, bkeCluster, hopPath); err != nil {
        return fmt.Errorf("phase 1 (K8s core) failed: %w", err)
    }
    
    // 最终偏差验证
    vc := r.getCurrentVersionContext(bkeCluster)
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
    if !ok {
        return fmt.Errorf("phase 1 skew check failed: %v", violations)
    }
    log.Info("phase 1 completed: K8s core components upgraded, skew constraints satisfied")
    
    // ─── 阶段二: 其它组件升级 ───
    log.Info("phase 2: other components upgrade (containerd, bkeagent, runc, ...)")
    if err := r.executeOtherComponentsUpgrade(ctx, bkeCluster, hopPath[len(hopPath)-1]); err != nil {
        return fmt.Errorf("phase 2 (other components) failed: %w", err)
    }
    
    log.Info("full upgrade completed: all components upgraded to target version")
    return nil
}

// executeOtherComponentsUpgrade 执行非 K8s 核心组件的升级
// 这些组件不受 K8s Version Skew Policy 约束，按 ComponentVersion 依赖排序
func (r *ClusterVersionReconciler) executeOtherComponentsUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) error {
    // 1. 解析目标 ReleaseImage
    bundle, err := r.resolveReleaseBundle(ctx, targetVersion)
    if err != nil {
        return err
    }
    
    // 2. 构建 VersionContext
    vc := upgrade.BuildVersionContextForUpgrade(bundle, currentBundle, bkeCluster)
    
    // 3. 排除 K8s 核心组件（已在阶段一升级完成）
    //    K8s 核心组件: etcd, kube-apiserver, kube-controller-manager, 
    //                   kube-scheduler, kubelet, kubectl, kube-proxy
    k8sCoreComponents := map[string]bool{
        "etcd": true, "kube-apiserver": true, "kube-controller-manager": true,
        "kube-scheduler": true, "kubelet": true, "kubectl": true, "kube-proxy": true,
    }
    
    // 4. 过滤出需要升级的非核心组件
    for name := range vc.Target {
        if k8sCoreComponents[name] {
            // K8s 核心组件已在阶段一升级，设置 Current = Target 跳过
            vc.SetCurrent(name, vc.GetTarget(name))
        }
    }
    
    // 5. containerd CRI 兼容性预检
    containerdTarget := vc.GetTarget("containerd")
    k8sVersion := vc.GetCurrent("kube-apiserver")
    if containerdTarget != "" && k8sVersion != "" {
        compatible, suggestedVer := upgrade.CheckContainerRuntimeCompatibility(
            k8sVersion, vc.GetCurrent("containerd"))
        if !compatible {
            log.Info("containerd version below recommended, will upgrade",
                "current", vc.GetCurrent("containerd"),
                "recommended", suggestedVer,
                "target", containerdTarget)
        }
    }
    
    // 6. 构建 DAG（仅包含非核心组件）
    dag, err := upgrade.BuildDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    if err != nil {
        return err
    }
    
    // 7. 执行 DAG
    sched := r.buildScheduler(bundle, vc)
    execCtx := r.buildExecutionContext(ctx, bkeCluster, vc)
    
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return fmt.Errorf("execute other components DAG: %w", err)
    }
    
    return nil
}
```

**阶段二 DAG 结构**：

```
阶段二 DAG（非 K8s 核心组件，按依赖排序）:

Batch 1: [bkeagent]                     ← 无依赖，最先执行
    └─ SSH 推送新版本二进制

Batch 2: [containerd, runc]            ← 依赖 bkeagent，并行
    ├─ containerd: ENV 命令（reset + redeploy）
    └─ runc: BinaryInstaller 替换二进制

Batch 3: [lxcfs, nfs-utils, ...]       ← 依赖 bkeagent，并行
    ├─ lxcfs: BinaryInstaller + systemd service
    └─ nfs-utils: 包管理器安装（yum/apt）

Batch 4: [helm, etcdctl, calicoctl]     ← 辅助工具，最后
    ├─ helm: BinaryInstaller
    ├─ etcdctl: BinaryInstaller
    └─ calicoctl: BinaryInstaller
```

> **注意**：阶段二的组件不需要偏差门控，因为它们不受 K8s Version Skew Policy 约束。各组件按 ComponentVersion.spec.dependencies 中的依赖关系排序，由 DAG Scheduler 统一调度。

#### 4.2.1 kubelet 延迟升级的收益

将 kubelet 升级从 `upgradeControlPlane()` 中分离，实现延迟升级，核心收益如下：

**1. 减少 drain 次数（最大收益）**

| 场景 | 当前（每 hop 强制升级 kubelet） | 延迟升级（最后批量补充） |
|------|-------------------------------|------------------------|
| 3-hop 升级（v1.34→v1.37） | 3 轮 × N 节点 drain | 1 轮 × N 节点 drain（集中在最后） |
| 100 节点集群 | 300 次 drain | 100 次 drain |
| 每次 drain 耗时 ~5min | ~25 小时 | ~8 小时 |

**2. 控制面快速推进**

| 维度 | 当前 | 延迟升级 |
|------|------|---------|
| apiserver 升级方式 | manifest 替换（秒级，无需 drain） | 同左 |
| kubelet 升级方式 | 二进制替换 + systemctl restart（需 drain） | 延迟到所有 hop 完成后 |
| 3-hop 控制面升级耗时 | 被 kubelet drain 阻塞：每 hop 等 N 节点 drain | 仅等 manifest 替换：3 hop 快速完成 |
| 100 节点 3-hop | ~25 小时（3 轮 drain） | ~30 分钟（3 轮 manifest 替换）+ 8 小时（最后 1 轮 drain） |

**3. 业务影响最小化**

| 维度 | 当前 | 延迟升级 |
|------|------|---------|
| drain 窗口数 | 3 个分散窗口（每 hop 一个） | 1 个集中窗口（最后一次性） |
| 业务中断次数 | 3 次 | 1 次 |
| 可选择维护窗口 | 每个 hop 都需找维护窗口 | 只需在最后找 1 个维护窗口 |
| Pod 重启次数 | 3 × N 节点 × M Pod | 1 × N 节点 × M Pod |

**4. 独立失败处理**

| 场景 | 当前 | 延迟升级 |
|------|------|---------|
| kubelet drain 失败 | 阻塞整个 hop（控制面也卡住） | 仅影响 kubelet 补充升级，控制面已完成 |
| 控制面升级失败 | kubelet 已升级（可能不一致） | kubelet 未升级（仍可回滚到旧版本） |
| 部分节点 kubelet 升级失败 | Worker 节点 Continue，Master 阻塞 | 同左，但控制面已就绪，不影响集群可用性 |

**5. 充分利用 K8s 偏差窗口**

| 维度 | 当前 | 延迟升级 |
|------|------|---------|
| K8s 允许偏差 | kubelet 最多滞后 apiserver 3 个小版本 | 未利用 |
| 实际偏差 | 每 hop 强制 0 偏差 | 利用满 3 版本窗口 |
| 资源利用率 | 浪费了 K8s 官方提供的灵活性 | 最大化利用 |

#### 4.2.2 阶段一实现：orchestrateMultiHopUpgrade

> 依赖函数：[`executeControlPlaneHop`](#42-executecontrolplanehop-实现重构版)（4.2.3）、[`upgradeKubeletCatchup`](#424-upgradekubeletcatchup-实现)（4.2.4）、[`executeKubeletUpgrade`](#425-executeKubeletUpgrade-实现)（4.2.5）

```go
// controllers/clusterversion/clusterversion_controller.go

// orchestrateMultiHopUpgrade 执行 K8s 核心组件的多 hop 升级
// 每个 hop 升级控制面组件（etcd/apiserver/cm/scheduler/kube-proxy/kubectl），
// kubelet 延迟到偏差达到极限时补充升级
//
// 调用链:
//   orchestrateMultiHopUpgrade
//     ├─ executeControlPlaneHop (4.2.3)   ← 每个 hop 执行控制面升级（不含 kubelet）
//     ├─ NeedsKubeletCatchup                ← 偏差门控：检查是否需要 kubelet 补充升级
//     └─ upgradeKubeletCatchup (4.2.4)     ← kubelet 补充升级（逐版本）
//          └─ executeKubeletUpgrade (4.2.5) ← 逐节点 drain → 替换 → uncordon
func (r *ClusterVersionReconciler) orchestrateMultiHopUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopPath []string, // ["v2.6.5", "v2.7.0"] (openFuyao 版本路径)
) error {
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    
    for i, hopTarget := range hopPath {
        log.Info("starting hop", "hop", i+1, "target", hopTarget)
        
        // 1. 执行当前 hop（控制面升级，kubelet 延迟）
        //    executeControlPlaneHop 内部设置 kubelet Target=Current 跳过升级
        if err := r.executeControlPlaneHop(ctx, bkeCluster, hopTarget); err != nil {
            return fmt.Errorf("hop %d (%s) control plane upgrade failed: %w", i+1, hopTarget, err)
        }
        
        // 2. 更新 VersionContext（控制面组件已升级，kubelet 未升级）
        vc := r.getCurrentVersionContext(bkeCluster)
        
        // 3. 偏差门控
        if i < len(hopPath)-1 {
            // 还有下一个 hop，进行前瞻性检查
            nextHopVersions := r.resolveHopTargetVersions(hopPath[i+1])
            
            // 检查执行下一个 hop 后是否仍满足偏差约束
            needsCatchup, skew := skewChecker.NeedsKubeletCatchup(vc, nextHopVersions)
            
            if needsCatchup {
                // 偏差即将达到极限（3），必须先升级 kubelet
                log.Info("skew limit reached, must upgrade kubelet before next hop",
                    "currentSkew", skew, "maxSkew", 3)
                
                // 触发 kubelet 补充升级（从当前版本逐版本升级到 hopPath[i] 的版本）
                if err := r.upgradeKubeletCatchup(ctx, bkeCluster, vc, hopPath[:i+1]); err != nil {
                    return fmt.Errorf("kubelet catchup upgrade failed: %w", err)
                }
            } else {
                // 偏差在安全范围内，可以继续下一个 hop
                ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
                if !ok {
                    return fmt.Errorf("skew violations after hop %d: %v", i+1, violations)
                }
                log.Info("skew gate passed, continuing to next hop",
                    "hop", i+1, "currentSkew", skew)
            }
        } else {
            // 最后一个 hop 完成，执行 kubelet 最终升级
            log.Info("final hop completed, upgrading kubelet to target version")
            if err := r.upgradeKubeletCatchup(ctx, bkeCluster, vc, hopPath); err != nil {
                return fmt.Errorf("final kubelet upgrade failed: %w", err)
            }
        }
        
        // 4. 偏差验证
        ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
        if !ok {
            return fmt.Errorf("skew violations after hop %d: %v", i+1, violations)
        }
        
        log.Info("hop completed, skew constraints satisfied",
            "hop", i+1, "version", hopTarget)
    }
    
    return nil
}
```

#### 4.2.3 executeControlPlaneHop 实现（重构版）

> 重构目标：支持通用场景，如 K8s v1.30→v1.36（6-hop），kubelet 需多次中途补充升级

**问题分析**：K8s v1.30→v1.36 跨 6 个小版本，kubelet 最多滞后 apiserver 3 个小版本：

```
v1.30→v1.36 升级路径（6 hops）:

Hop 1: apiserver v1.31, kubelet v1.30 → skew 1 ✅
Hop 2: apiserver v1.32, kubelet v1.30 → skew 2 ✅
Hop 3: apiserver v1.33, kubelet v1.30 → skew 3 ⚠️ 极限，必须升级 kubelet
  → kubelet 补充: v1.30→v1.31→v1.32→v1.33（升级到当前 apiserver 版本）
Hop 4: apiserver v1.34, kubelet v1.33 → skew 1 ✅
Hop 5: apiserver v1.35, kubelet v1.33 → skew 2 ✅
Hop 6: apiserver v1.36, kubelet v1.33 → skew 3 ⚠️ 极限，必须升级 kubelet
  → kubelet 最终补充: v1.33→v1.34→v1.35→v1.36
```

原 `executeControlPlaneHop` 硬编码 kubelet 跳过，无法处理多次中途补充升级。重构为**通用版本**：

```go
// executeControlPlaneHop 执行单个 hop 的 K8s 核心组件升级
// 支持延迟升级指定的组件（通过 deferredComponents 参数配置）
//
// hopTarget: openFuyao 版本（如 "v2.6.5"），非 K8s 版本
//            从对应 ReleaseImage 解析 K8s 组件版本（kube-apiserver/kubelet 等）
//
// 重构要点（vs 原版）:
// 1. hopTarget 是 openFuyao 版本，K8s 版本从 ReleaseImage 解析
// 2. deferredComponents 从硬编码 kubelet 改为参数传入，支持多组件延迟
// 3. 返回 HopResult，包含各组件升级后的版本，供 orchestrateK8sCoreMultiHop 做偏差判断
// 4. 不再在此函数内更新 BKECluster.Status（由 orchestrateK8sCoreMultiHop 统一管理）
//
// 调用方: orchestrateK8sCoreMultiHop (9.10.6.3)
func (r *ClusterVersionReconciler) executeControlPlaneHop(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopTarget string, // openFuyao 版本，如 "v2.6.5"
    deferredComponents []string, // 延迟升级的组件名列表，如 ["kubelet"]
) (*HopResult, error) {
    // 1. 解析目标版本 ReleaseImage（openFuyao 版本 → ReleaseImage bundle）
    //    ReleaseImage 中包含 K8s 组件版本: kube-apiserver=v1.35, etcd=v3.5.19, ...
    bundle, err := r.resolveReleaseBundle(ctx, hopTarget)
    if err != nil {
        return nil, err
    }
    
    // 2. 构建 VersionContext
    vc := upgrade.BuildVersionContextForUpgrade(bundle, currentBundle, bkeCluster)
    
    // 3. 延迟升级：将 deferredComponents 的 Target 设为 Current（跳过升级）
    //    VersionContext 判定 current == target → Skip
    deferredSet := make(map[string]bool)
    for _, name := range deferredComponents {
        current := vc.GetCurrent(name)
        if current != "" {
            vc.SetTarget(name, current) // 保持不变，跳过
            deferredSet[name] = true
        }
    }
    
    // 4. 构建 DAG
    //    依赖关系来自 ComponentVersion.spec.dependencies
    //    延迟组件在 DAG 中仍存在，但 VersionContext.NeedsUpgrade 返回 false → Skip
    dag, err := upgrade.BuildDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    if err != nil {
        return nil, err
    }
    
    // 5. 构建 Scheduler 并执行 DAG
    sched := r.buildScheduler(bundle, vc)
    execCtx := r.buildExecutionContext(ctx, bkeCluster, vc)
    
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return nil, fmt.Errorf("execute control plane DAG: %w", err)
    }
    
    // 6. 收集 hop 结果（各组件升级后的版本）
    result := &HopResult{
        HopTarget:       hopTarget,
        UpgradedVersions: make(map[string]string),  // 已升级组件的版本
        DeferredVersions: make(map[string]string),  // 延迟组件的当前版本
    }
    
    for name := range vc.Target {
        if deferredSet[name] {
            result.DeferredVersions[name] = vc.GetCurrent(name)
        } else {
            result.UpgradedVersions[name] = vc.GetTarget(name)
        }
    }
    
    return result, nil
}

// HopResult 单个 hop 的执行结果
type HopResult struct {
    // hop 目标版本（openFuyao 版本）
    HopTarget string
    
    // 已升级组件的版本映射
    // 如: {"kube-apiserver": "v1.35", "etcd": "v3.5.19", ...}
    UpgradedVersions map[string]string
    
    // 延迟升级组件的当前版本映射
    // 如: {"kubelet": "v1.34"}
    DeferredVersions map[string]string
}

// GetUpgradedVersion 获取已升级组件的版本
func (h *HopResult) GetUpgradedVersion(name string) string {
    return h.UpgradedVersions[name]
}

// GetDeferredVersion 获取延迟组件的当前版本
func (h *HopResult) GetDeferredVersion(name string) string {
    return h.DeferredVersions[name]
}
```

#### 4.2.3a orchestrateMultiHopUpgrade 重构（支持多轮 kubelet 补充）

> 重构目标：支持 v1.30→v1.36 等大跨度升级，kubelet 需多次中途补充

```go
// orchestrateMultiHopUpgrade 执行 K8s 核心组件的多 hop 升级
// 支持大跨度升级（如 v1.30→v1.36），kubelet 可多次中途补充升级
//
// 调用链:
//   orchestrateMultiHopUpgrade
//     ├─ executeControlPlaneHop (4.2.3)      ← 每个 hop 执行控制面升级（延迟 kubelet）
//     ├─ checkSkewAfterHop                     ← 偏差检查：当前偏差是否达到极限
//     ├─ computeKubeletCatchupTarget           ← 计算 kubelet 需要补充升级到的目标版本
//     └─ upgradeKubeletCatchup (4.2.4)        ← kubelet 补充升级（逐版本）
//          └─ executeKubeletUpgrade (4.2.5)    ← 逐节点 drain → 替换 → uncordon
//
// v1.30→v1.36 示例（6 hops，2 轮 kubelet 补充）:
//
//   Hop 1: apiserver 1.31, kubelet 1.30 → skew 1 → ✅ 继续
//   Hop 2: apiserver 1.32, kubelet 1.30 → skew 2 → ✅ 继续
//   Hop 3: apiserver 1.33, kubelet 1.30 → skew 3 → ⚠️ 极限
//     → kubelet catchup: 1.30→1.31→1.32→1.33（升级到当前 apiserver 版本）
//   Hop 4: apiserver 1.34, kubelet 1.33 → skew 1 → ✅ 继续
//   Hop 5: apiserver 1.35, kubelet 1.33 → skew 2 → ✅ 继续
//   Hop 6: apiserver 1.36, kubelet 1.33 → skew 3 → ⚠️ 极限
//     → kubelet final catchup: 1.33→1.34→1.35→1.36
func (r *ClusterVersionReconciler) orchestrateMultiHopUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopPath []string, // ["v2.6.0", "v2.6.5", ..., "v2.9.0"] (openFuyao 版本路径)
) error {
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    maxKubeletSkew := 3 // K8s v1.25+ 允许 kubelet 最多滞后 apiserver 3 个小版本
    
    // kubelet 当前版本（跨 hop 持续追踪）
    kubeletCurrentVersion := r.getKubeletVersion(bkeCluster)
    
    for i, hopTarget := range hopPath {
        log.Info("starting hop", "hop", i+1, "target", hopTarget,
            "kubeletCurrent", kubeletCurrentVersion)
        
        // 1. 执行当前 hop（控制面升级，kubelet 延迟）
        hopResult, err := r.executeControlPlaneHop(
            ctx, bkeCluster, hopTarget,
            []string{"kubelet"}, // 延迟升级 kubelet
        )
        if err != nil {
            return fmt.Errorf("hop %d (%s) control plane upgrade failed: %w", i+1, hopTarget, err)
        }
        
        // 2. 更新 BKECluster.Status（控制面组件版本）
        apiserverVersion := hopResult.GetUpgradedVersion("kube-apiserver")
        etcdVersion := hopResult.GetUpgradedVersion("etcd")
        bkeCluster.Status.KubernetesVersion = apiserverVersion
        bkeCluster.Status.EtcdVersion = etcdVersion
        
        // 3. 计算当前偏差
        currentSkew := computeMinorVersionSkew(apiserverVersion, kubeletCurrentVersion)
        log.Info("hop completed, checking skew",
            "hop", i+1, "apiserver", apiserverVersion,
            "kubelet", kubeletCurrentVersion, "skew", currentSkew,
            "maxSkew", maxKubeletSkew)
        
        // 4. 偏差门控：判断是否需要 kubelet 补充升级
        needsCatchup := false
        catchupTargetVersion := ""
        
        if i < len(hopPath)-1 {
            // 还有下一个 hop，前瞻性检查
            nextHopVersions := r.resolveHopTargetVersions(hopPath[i+1])
            nextApiserverVersion := nextHopVersions["kube-apiserver"]
            
            if nextApiserverVersion != "" {
                // 模拟下一个 hop 后的偏差
                nextSkew := computeMinorVersionSkew(nextApiserverVersion, kubeletCurrentVersion)
                
                if nextSkew >= maxKubeletSkew {
                    // 下一个 hop 后偏差将达到极限，必须先升级 kubelet
                    // 目标版本：当前 apiserver 版本（不是下一个 hop 的版本）
                    // 这样升级后偏差为 0，为下一个 hop 留出完整的 3 版本窗口
                    needsCatchup = true
                    catchupTargetVersion = apiserverVersion
                    log.Info("skew will exceed limit after next hop, must upgrade kubelet now",
                        "currentSkew", currentSkew,
                        "projectedNextSkew", nextSkew,
                        "maxSkew", maxKubeletSkew,
                        "catchupTarget", catchupTargetVersion)
                }
            }
        } else {
            // 最后一个 hop，必须将 kubelet 升级到最终目标版本
            needsCatchup = true
            catchupTargetVersion = apiserverVersion
            log.Info("final hop completed, upgrading kubelet to final target version",
                "catchupTarget", catchupTargetVersion)
        }
        
        // 5. 执行 kubelet 补充升级（如需要）
        if needsCatchup {
            if err := r.upgradeKubeletCatchupToVersion(
                ctx, bkeCluster, kubeletCurrentVersion, catchupTargetVersion,
            ); err != nil {
                return fmt.Errorf("kubelet catchup to %s failed: %w",
                    catchupTargetVersion, err)
            }
            
            // 更新 kubelet 当前版本
            kubeletCurrentVersion = catchupTargetVersion
            bkeCluster.Status.KubeletVersion = catchupTargetVersion
            
            // 偏差验证
            newSkew := computeMinorVersionSkew(apiserverVersion, kubeletCurrentVersion)
            log.Info("kubelet catchup completed",
                "kubelet", kubeletCurrentVersion,
                "apiserver", apiserverVersion,
                "newSkew", newSkew)
            
            if newSkew > maxKubeletSkew {
                return fmt.Errorf("skew %d still exceeds max %d after kubelet catchup",
                    newSkew, maxKubeletSkew)
            }
        }
        
        log.Info("hop fully completed",
            "hop", i+1, "version", hopTarget,
            "apiserver", apiserverVersion,
            "kubelet", kubeletCurrentVersion)
    }
    
    return nil
}

// upgradeKubeletCatchupToVersion kubelet 补充升级到指定目标版本
// 从 currentVersion 逐版本升级到 targetVersion
// 如 v1.30→v1.33: 经过 v1.31, v1.32, v1.33
func (r *ClusterVersionReconciler) upgradeKubeletCatchupToVersion(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    currentVersion string,
    targetVersion string,
) error {
    log.Info("starting kubelet catchup",
        "from", currentVersion, "to", targetVersion)
    
    // 计算中间版本路径
    intermediateVersions := computeIntermediateVersions(currentVersion, targetVersion)
    // 如: v1.30→v1.33 → ["v1.31.0", "v1.32.0", "v1.33.0"]
    
    for _, version := range intermediateVersions {
        log.Info("upgrading kubelet to intermediate version",
            "target", version)
        
        // 逐节点 drain → 替换二进制 → 健康检查 → uncordon
        if err := r.executeKubeletUpgrade(ctx, bkeCluster, version); err != nil {
            return fmt.Errorf("kubelet upgrade to %s failed: %w", version, err)
        }
        
        log.Info("kubelet upgraded to intermediate version", "version", version)
    }
    
    return nil
}
```

**重构要点总结**：

| 维度 | 原版 | 重构版 |
|------|------|--------|
| **延迟组件** | 硬编码 kubelet | `deferredComponents` 参数传入 |
| **返回值** | `error` | `*HopResult`（含各组件版本） |
| **状态更新** | 函数内更新 BKECluster.Status | 由 `orchestrateMultiHopUpgrade` 统一管理 |
| **kubelet 补充目标** | 升级到最终目标版本 | 升级到**当前 apiserver 版本**（为下一个 hop 留出完整 3 版本窗口） |
| **多轮补充** | 不支持（仅最终一轮） | 支持（偏差达到极限即触发，可多次中途补充） |
| **偏差计算** | 依赖 VersionContext | 直接使用 `computeMinorVersionSkew` 简化 |
| **适用场景** | 2-3 hop（v1.34→v1.36） | 任意 hop 数（v1.30→v1.36 等 6-hop） |

**v1.30→v1.36 完整升级时序**：

```
Hop 1: apiserver 1.31, kubelet 1.30, skew=1 → ✅ 继续
Hop 2: apiserver 1.32, kubelet 1.30, skew=2 → ✅ 继续
Hop 3: apiserver 1.33, kubelet 1.30, skew=3 → ⚠️ 下一个 hop skew 将达到 4
  → kubelet catchup: 1.30→1.31→1.32→1.33（目标=当前 apiserver 1.33）
  → 补充后: kubelet 1.33, apiserver 1.33, skew=0 → ✅
Hop 4: apiserver 1.34, kubelet 1.33, skew=1 → ✅ 继续
Hop 5: apiserver 1.35, kubelet 1.33, skew=2 → ✅ 继续
Hop 6: apiserver 1.36, kubelet 1.33, skew=3 → 最后一个 hop
  → kubelet final catchup: 1.33→1.34→1.35→1.36（目标=最终 apiserver 1.36）
  → 补充后: kubelet 1.36, apiserver 1.36, skew=0 → ✅ 升级完成
```

#### 4.2.4 upgradeKubeletCatchup 实现

```go
// upgradeKubeletCatchup kubelet 补充升级（从当前版本逐版本升级到目标版本）
// 利用 K8s 允许 kubelet 滞后 apiserver 3 个小版本的偏差窗口
func (r *ClusterVersionReconciler) upgradeKubeletCatchup(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    vc *upgrade.VersionContext,
    completedHops []string,
) error {
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    
    currentKubelet := vc.GetCurrent("kubelet")
    targetKubelet := vc.GetTarget("kubelet")
    
    log.Info("starting kubelet catchup upgrade",
        "from", currentKubelet, "to", targetKubelet)
    
    // 1. 计算需要经过的中间版本
    // 例如: v1.34.0 → v1.36.0 → ["v1.35.0", "v1.36.0"]
    intermediateVersions := computeIntermediateVersions(currentKubelet, targetKubelet)
    
    for _, version := range intermediateVersions {
        log.Info("upgrading kubelet to intermediate version",
            "from", currentKubelet, "to", version)
        
        // 2. 通过 BinaryInstaller 执行 kubelet 升级
        //    逐节点: drain → 下载二进制 → 替换 → 重启 → 健康检查 → uncordon
        if err := r.executeKubeletUpgrade(ctx, bkeCluster, version); err != nil {
            return fmt.Errorf("kubelet upgrade to %s failed: %w", version, err)
        }
        
        // 3. 更新 VersionContext
        currentKubelet = version
        vc.SetCurrent("kubelet", version)
        
        // 4. 每次中间版本升级后验证偏差
        ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
        if !ok {
            return fmt.Errorf("skew violation during kubelet catchup at %s: %v",
                version, violations)
        }
        
        log.Info("kubelet upgraded to intermediate version, skew check passed",
            "version", version)
    }
    
    // 5. 更新 kubelet 版本状态
    bkeCluster.Status.KubeletVersion = targetKubelet
    
    log.Info("kubelet catchup upgrade completed", "final", targetKubelet)
    return nil
}
```

#### 4.2.5 executeKubeletUpgrade 实现

```go
// executeKubeletUpgrade 执行 kubelet 到指定版本的逐节点升级
// Master 节点: 阻塞式（失败则停止）
// Worker 节点: 非阻塞式（失败继续下一个节点，记录失败节点）
func (r *ClusterVersionReconciler) executeKubeletUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) error {
    // 1. 加载 kubelet ComponentVersion（binary 类型）
    cv, err := r.CVStore.GetComponentVersion(ctx, "kubelet", targetVersion)
    if err != nil {
        return fmt.Errorf("get kubelet component version %s: %w", targetVersion, err)
    }
    
    // 2. 构建 BinaryInstaller 选项
    opts := binaryinstaller.InstallOptions{
        Component:   cv,
        Action:      binaryinstaller.BinaryActionUpgrade,
        TemplateCtx: r.buildTemplateContext(bkeCluster),
    }
    
    // 3. 获取所有节点
    bkeNodes, err := r.NodeFetcher().GetBKENodesWrapperForCluster(ctx, bkeCluster)
    if err != nil {
        return err
    }
    allNodes := bkeNodes.ToNodes()
    
    // 4. 创建 drainer
    drainer := phaseutil.NewDrainer(true, true, true, 20*time.Second)
    
    var failedNodes []string
    
    // 5. 逐节点升级
    for _, node := range allNodes {
        // 5a. drain 节点（驱逐 Pod）
        if err := drainer.Drain(ctx, node.Hostname); err != nil {
            log.Error(err, "drain failed", "node", node.IP)
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("drain master node %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5b. 设置目标节点 IP
        opts.TemplateCtx.NodeIP = node.IP
        
        // 5c. 执行 BinaryInstaller 安装（升级）
        //    内部流程: 下载 kubelet 二进制 → 校验 checksum →
        //              渲染 kubelet.conf + kubelet.service → SSH 上传+执行 →
        //              systemctl stop → 替换二进制 → systemctl start
        if err := r.BinaryInstaller.Install(ctx, opts); err != nil {
            log.Error(err, "kubelet upgrade failed", "node", node.IP)
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("kubelet upgrade on master %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5d. 等待节点健康检查
        //    轮询 Node 状态（2s 间隔，5min 超时）
        //    验证 Node Ready + KubeletVersion 匹配目标版本
        if err := waitForNodeHealthCheck(ctx, r.Client, bkeCluster, node, targetVersion); err != nil {
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("health check on master %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5e. uncordon 节点（恢复调度）
        _ = uncordonNode(ctx, r.Client, bkeCluster, node.Hostname)
        
        log.Info("kubelet upgraded", "node", node.IP, "version", targetVersion)
    }
    
    if len(failedNodes) > 0 {
        log.Info("kubelet upgrade completed with failures", "failedNodes", failedNodes)
        // Worker 节点失败不阻塞，Reconcile 重试时跳过已升级节点
    }
    
    return nil
}

// computeIntermediateVersions 计算从 currentVersion 到 targetVersion 的中间版本列表
// 例如: ("v1.34.0", "v1.36.0") → ["v1.35.0", "v1.36.0"]
func computeIntermediateVersions(currentVersion, targetVersion string) []string {
    currentMinor := parseMinorVersion(currentVersion)  // 34
    targetMinor := parseMinorVersion(targetVersion)    // 36
    
    var versions []string
    for minor := currentMinor + 1; minor <= targetMinor; minor++ {
        versions = append(versions, fmt.Sprintf("v1.%d.0", minor))
    }
    
    return versions
}
```

### 4.3 单 hop 内的升级顺序

每个 hop 内严格执行以下顺序（通过 DAG dependencies 保证）：

```
单 hop 升级 DAG (K8s v1.34 → v1.35):

Batch 1: [etcd]                        ← 先升级 etcd（数据存储）
    └─ StaticPodInstaller

Batch 2: [kube-apiserver]             ← 升级 apiserver（控制面入口）
    └─ StaticPodInstaller

Batch 3: [kube-controller-manager,    ← 跟随 apiserver（并行）
          kube-scheduler]
    ├─ StaticPodInstaller
    └─ StaticPodInstaller

Batch 4: [kube-proxy]                 ← 可滞后 apiserver 最多 3 个小版本（v1.25+）
    └─ YamlInstaller Apply

Batch 5: [kubectl]                    ← 命令行工具
    └─ BinaryInstaller

─── kubelet 不在此 hop 中升级（延迟到偏差门后） ───
```

### 4.4 多 hop 间的偏差门控

在 hop 之间增加**偏差验证门**，决定是否可以继续下一个 hop：

```
Hop 1: K8s v1.34 → v1.35
  ├─ apiserver: v1.34 → v1.35
  ├─ cm/scheduler: v1.34 → v1.35  ← 最多落后 apiserver 1 个小版本，本次同步升级
  ├─ kube-proxy: v1.34 → v1.35   ← 最多落后 apiserver 3 个小版本，本次同步升级
  ├─ kubectl: v1.34 → v1.35      ← 最多落后 apiserver 1 个小版本，本次同步升级
  └─ kubelet: 保持 v1.34（1 版本偏差，安全，允许 3）

  偏差门 1: kubelet(v1.34) vs apiserver(v1.35) → 1 偏差 → ✅ 通过

Hop 2: K8s v1.35 → v1.36
  ├─ apiserver: v1.35 → v1.36
  ├─ cm/scheduler: v1.35 → v1.36
  ├─ kube-proxy: v1.35 → v1.36
  ├─ kubectl: v1.35 → v1.36
  └─ kubelet: 保持 v1.34（2 版本偏差，安全，v1.25+ 允许最多 3）

  偏差门 2: kubelet(v1.34) vs apiserver(v1.36) → 2 偏差 → ✅ 安全
  决策: 可继续下一个 hop（如果偏差 < 3）

Hop 3（如有）: K8s v1.36 → v1.37
  ├─ apiserver: v1.36 → v1.37
  ├─ cm/scheduler: v1.36 → v1.37
  ├─ kube-proxy: v1.36 → v1.37
  ├─ kubectl: v1.36 → v1.37
  └─ kubelet: 保持 v1.34（3 版本偏差，达到极限）

  偏差门 3: kubelet(v1.34) vs apiserver(v1.37) → 3 偏差 → ⚠️ 达到极限
  强制动作: 触发 kubelet 补充升级 v1.34 → v1.35 → v1.36 → v1.37
```

### 4.5 完整多 hop 升级流程

```
┌─────────────────────────────────────────────────────────────────────────┐
│  ClusterVersionReconciler 编排                                          │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Hop 1: 控制面升级 (K8s v1.34 → v1.35)                          │   │
│  │                                                                 │   │
│  │  Batch 1: [etcd] → v3.5.19 (StaticPod)                         │   │
│  │  Batch 2: [kube-apiserver] → v1.35.0 (StaticPod)               │   │
│  │  Batch 3: [kube-controller-manager, kube-scheduler] → v1.35.0   │   │
│  │  Batch 4: [kube-proxy] → v1.35.0 (YAML)                        │   │
│  │  Batch 5: [kubectl] → v1.35.0 (Binary)                         │   │
│  │  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─   │   │
│  │  kubelet: 保持 v1.34.0（延迟升级）                               │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  偏差门 1: Skew Gate                                            │   │
│  │                                                                 │   │
│  │  检查: kubelet(v1.34) vs apiserver(v1.35) → 1 版本偏差 → ✅ 安全  │   │
│  │  检查: kube-proxy(v1.35) ≤ apiserver(v1.35) → ✅ 允许滞后 3        │   │
│  │  检查: cm(v1.35) ≤ apiserver(v1.35) → ✅ 允许滞后 1                │   │
│  │  决策: 允许继续 Hop 2                                            │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Hop 2: 控制面升级 (K8s v1.35 → v1.36)                          │   │
│  │                                                                 │   │
│  │  Batch 1: [etcd] → v3.5.20 (StaticPod)                         │   │
│  │  Batch 2: [kube-apiserver] → v1.36.0 (StaticPod)                │   │
│  │  Batch 3: [kube-controller-manager, kube-scheduler] → v1.36.0   │   │
│  │  Batch 4: [kube-proxy] → v1.36.0 (YAML)                        │   │
│  │  Batch 5: [kubectl] → v1.36.0 (Binary)                         │   │
│  │  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─   │   │
│  │  kubelet: 保持 v1.34.0（仍延迟升级）                             │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  偏差门 2: Skew Gate                                            │   │
│  │                                                                 │   │
│  │  检查: kubelet(v1.34) vs apiserver(v1.36) → 2 版本偏差 → ✅ 安全（允许 3）│   │
│  │  决策: 偏差未达极限，允许继续下一个 hop                              │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Kubelet 补充升级 (v1.34 → v1.35 → v1.36)                      │   │
│  │  （在最后一个 hop 完成后或偏差达到 3 时触发）                       │   │
│  │                                                                 │   │
│  │  Sub-hop A: kubelet v1.34 → v1.35                              │   │
│  │    └─ BinaryInstaller: 逐节点 drain → replace → uncordon       │   │
│  │    偏差检查: kubelet(v1.35) vs apiserver(v1.36) → 1 偏差 → ✅   │   │
│  │                                                                 │   │
│  │  Sub-hop B: kubelet v1.35 → v1.36                              │   │
│  │    └─ BinaryInstaller: 逐节点 drain → replace → uncordon       │   │
│  │    偏差检查: kubelet(v1.36) vs apiserver(v1.36) → 0 偏差 → ✅   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              ↓                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  最终偏差验证                                                    │   │
│  │  检查: kubelet(v1.36) vs apiserver(v1.36) → 0 版本偏差 → ✅     │   │
│  │  检查: kube-proxy(v1.36) ≤ apiserver(v1.36) → ✅               │   │
│  │  检查: cm/scheduler(v1.36) ≤ apiserver(v1.36) → ✅             │   │
│  │  检查: etcd(v3.5.20) compatible with K8s v1.36 → ✅             │   │
│  │  升级完成                                                        │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
```

### 4.6 kubelet 延迟升级策略

对于大规模集群，kubelet 逐节点 drain 升级耗时较长。采用**延迟升级策略**：

| 组件 | 偏差规则 | 是否延迟 | 原因 |
|------|---------|---------|------|
| **kubelet** | 最多滞后 apiserver 3 个小版本 | ✅ **延迟** | drain 耗时长，利用 3 版本偏差窗口减少 drain 次数 |
| **kubectl** | 最多滞后 apiserver 1 个小版本 | ❌ **不延迟** | 偏差窗口仅 1，延迟 2 个 hop 即超限；且升级仅替换二进制，无需 drain，不阻塞控制面 |
| **kube-proxy** | 最多滞后 apiserver 3 个小版本 | ❌ **不延迟** | 通过 DaemonSet 滚动更新，无需逐节点 drain，不阻塞控制面 |
| **cm/scheduler** | 最多滞后 apiserver 1 个小版本 | ❌ **不延迟** | 偏差窗口仅 1，必须随 apiserver 同步升级 |

| 策略 | 说明 | 适用场景 |
|------|------|---------|
| **同步升级** | 每个 hop 内 kubelet 与 apiserver 同步升级 | 小集群（<10 节点），偏差始终为 0 |
| **延迟升级** | 控制面先升级，kubelet 延迟到偏差达到极限（3）时批量升级 | 大集群（>10 节点），减少 drain 次数 |
| **最终升级** | 所有 hop 完成后，kubelet 逐版本补充升级到目标版本 | 与延迟升级配合 |

> **注意**：kubectl 和 kube-proxy 在每个 hop 中随控制面同步升级。kubectl 升级仅替换二进制（无需 drain），kube-proxy 通过 DaemonSet 滚动更新（无需逐节点 drain），两者都不阻塞控制面升级流程。

**延迟升级的版本路径**：

```
3-hop 升级 (v1.34 → v1.37，充分利用 K8s v1.25+ 的 3 版本偏差窗口):

同步升级:                    延迟升级:
  Hop 1:                       Hop 1:
    apiserver v1.35              apiserver v1.35
    kubectl v1.35                kubectl v1.35
    kube-proxy v1.35            kube-proxy v1.35
    kubelet v1.35                kubelet 保持 v1.34（偏差 1，安全）
  Hop 2:                       Hop 2:
    apiserver v1.36              apiserver v1.36
    kubectl v1.36                kubectl v1.36
    kube-proxy v1.36            kube-proxy v1.36
    kubelet v1.36                kubelet 保持 v1.34（偏差 2，安全）
  Hop 3:                       Hop 3:
    apiserver v1.37              apiserver v1.37
    kubectl v1.37                kubectl v1.37
    kube-proxy v1.37            kube-proxy v1.37
    kubelet v1.37                kubelet 保持 v1.34（偏差 3，极限）
                               补充:
                                 kubelet v1.34→v1.35
                                 kubelet v1.35→v1.36
                                 kubelet v1.36→v1.37

同步升级 drain 次数: 3 轮 × N 节点
延迟升级 drain 次数: 3 轮 × N 节点（但集中在最后一次性完成，减少中间窗口）
```

## 5. 偏差门控实现

### 5.1 VersionSkewChecker

```go
// pkg/upgrade/skew_checker.go

// VersionSkewChecker 版本偏差检查器
type VersionSkewChecker struct {
    client client.Client
}

// SkewViolation 偏差违反详情
type SkewViolation struct {
    Component     string // 被约束的组件
    Reference     string // 参照组件
    ComponentVer  string // 被约束组件当前版本
    ReferenceVer  string // 参照组件当前版本
    Skew          int    // 偏差值（正=component比reference旧，负=component比reference新）
    Reason        string // 违反原因
}

// CheckSkew 检查版本偏差是否满足约束
// 返回: 通过=true, 违反=false + 违反详情列表
func (c *VersionSkewChecker) CheckSkew(
    vc *VersionContext,
    constraints []SkewConstraint,
) (bool, []SkewViolation) {
    var violations []SkewViolation
    
    for _, constraint := range constraints {
        componentVersion := vc.GetCurrent(constraint.Component)
        referenceVersion := vc.GetCurrent(constraint.ReferenceComponent)
        
        if componentVersion == "" || referenceVersion == "" {
            continue // 版本未知，跳过
        }
        
        skew := computeMinorVersionSkew(referenceVersion, componentVersion)
        
        if skew < 0 {
            // 组件比参照新，违反约束（所有组件都不能比 apiserver 新）
            violations = append(violations, SkewViolation{
                Component:     constraint.Component,
                Reference:     constraint.ReferenceComponent,
                ComponentVer:  componentVersion,
                ReferenceVer:  referenceVersion,
                Skew:          skew,
                Reason:        "component is newer than reference (not allowed)",
            })
        } else if skew > constraint.MaxSkewBehind {
            // 偏差超过限制（kubelet/kube-proxy 最多 3，cm/scheduler/kubectl 最多 1）
            violations = append(violations, SkewViolation{
                Component:     constraint.Component,
                Reference:     constraint.ReferenceComponent,
                ComponentVer:  componentVersion,
                ReferenceVer:  referenceVersion,
                Skew:          skew,
                Reason:        fmt.Sprintf("skew %d exceeds max %d", skew, constraint.MaxSkewBehind),
            })
        }
    }
    
    return len(violations) == 0, violations
}
```

### 5.2 前瞻性偏差检查

```go
// CheckSkewBeforeHop 检查执行下一个 hop 后是否仍满足偏差约束
// 模拟下一个 hop 完成后的版本状态，检查偏差是否超限
func (c *VersionSkewChecker) CheckSkewBeforeHop(
    vc *VersionContext,
    nextHopTargetVersions map[string]string,
) (bool, []SkewViolation) {
    // 模拟下一个 hop 完成后的版本状态
    simulatedVC := vc.Clone()
    for name, version := range nextHopTargetVersions {
        simulatedVC.SetCurrent(name, version)
    }
    
    // 检查模拟状态下的偏差约束
    return c.CheckSkew(simulatedVC, K8sSkewConstraints)
}

// NeedsKubeletCatchup 判断是否需要 kubelet 补充升级
func (c *VersionSkewChecker) NeedsKubeletCatchup(
    vc *VersionContext,
    nextHopTargetVersions map[string]string,
) (bool, int) {
    // 模拟下一个 hop 后的偏差
    simulatedVC := vc.Clone()
    for name, version := range nextHopTargetVersions {
        simulatedVC.SetCurrent(name, version)
    }
    
    kubeletVersion := simulatedVC.GetCurrent("kubelet")
    apiserverVersion := simulatedVC.GetCurrent("kube-apiserver")
    
    if kubeletVersion == "" || apiserverVersion == "" {
        return false, 0
    }
    
    skew := computeMinorVersionSkew(apiserverVersion, kubeletVersion)
    
    // 偏差即将达到极限（K8s v1.25+ 允许最多 3 个小版本偏差）
    // 当偏差达到 3 时必须补充升级（不能继续下一个 hop）
    if skew >= 3 {
        return true, skew
    }
    
    return false, skew
}
```

## 6. ClusterVersionReconciler 集成

### 6.1 多 hop 编排

```go
// controllers/clusterversion/clusterversion_controller.go

func (r *ClusterVersionReconciler) orchestrateMultiHopUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopPath []string, // ["v2.6.0", "v2.6.5", "v2.7.0"]
) error {
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    
    for i, hopTarget := range hopPath {
        log.Info("starting hop", "hop", i+1, "target", hopTarget)
        
        // 1. 执行当前 hop（控制面升级，kubelet 延迟）
        if err := r.executeControlPlaneHop(ctx, bkeCluster, hopTarget); err != nil {
            return fmt.Errorf("hop %d (%s) control plane upgrade failed: %w", i+1, hopTarget, err)
        }
        
        // 2. 更新 VersionContext（控制面组件已升级，kubelet 未升级）
        vc := r.getCurrentVersionContext(bkeCluster)
        
        // 3. 偏差门控
        if i < len(hopPath)-1 {
            // 还有下一个 hop
            nextHopVersions := r.resolveHopTargetVersions(hopPath[i+1])
            
            // 前瞻性检查：下一个 hop 后偏差是否超限
            needsCatchup, skew := skewChecker.NeedsKubeletCatchup(vc, nextHopVersions)
            
            if needsCatchup {
                log.Info("skew limit reached, must upgrade kubelet before next hop",
                    "currentSkew", skew, "maxSkew", 3)
                
                // 触发 kubelet 补充升级
                if err := r.upgradeKubeletCatchup(ctx, bkeCluster, vc, hopPath[:i+1]); err != nil {
                    return fmt.Errorf("kubelet catchup upgrade failed: %w", err)
                }
            } else {
                // 偏差在安全范围内，可以继续
                ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
                if !ok {
                    return fmt.Errorf("skew violations after hop %d: %v", i+1, violations)
                }
                log.Info("skew gate passed, continuing to next hop", "hop", i+1)
            }
        } else {
            // 最后一个 hop 完成，执行 kubelet 最终升级
            log.Info("final hop completed, upgrading kubelet to target version")
            if err := r.upgradeKubeletCatchup(ctx, bkeCluster, vc, hopPath); err != nil {
                return fmt.Errorf("final kubelet upgrade failed: %w", err)
            }
        }
        
        // 4. 最终偏差验证
        ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
        if !ok {
            return fmt.Errorf("skew violations after hop %d: %v", i+1, violations)
        }
        
        log.Info("hop completed, skew constraints satisfied",
            "hop", i+1, "version", hopTarget)
    }
    
    return nil
}
```

### 6.2 控制面 hop 执行

```go
// executeControlPlaneHop 执行单个 hop 的控制面升级（不含 kubelet）
func (r *ClusterVersionReconciler) executeControlPlaneHop(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopTarget string,
) error {
    // 1. 解析目标版本 ReleaseImage
    bundle, err := r.resolveReleaseBundle(ctx, hopTarget)
    if err != nil {
        return err
    }
    
    // 2. 构建 VersionContext（仅控制面组件的 Target）
    vc := upgrade.BuildVersionContextForUpgrade(bundle, currentBundle, bkeCluster)
    
    // 3. 从升级列表中排除 kubelet（延迟升级）
    //    通过设置 kubelet 的 Target = Current 实现（VersionContext 判定 current == target → Skip）
    kubeletCurrent := vc.GetCurrent("kubelet")
    if kubeletCurrent != "" {
        vc.SetTarget("kubelet", kubeletCurrent) // 保持不变，跳过
    }
    
    // 4. 构建 DAG（控制面组件 + kube-proxy + kubectl，不含 kubelet）
    dag, err := upgrade.BuildDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    if err != nil {
        return err
    }
    
    // 5. 执行 DAG
    sched := r.buildScheduler(bundle, vc)
    execCtx := r.buildExecutionContext(ctx, bkeCluster, vc)
    
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return fmt.Errorf("execute control plane DAG: %w", err)
    }
    
    // 6. 更新控制面组件版本状态
    bkeCluster.Status.KubernetesVersion = vc.GetTarget("kube-apiserver")
    bkeCluster.Status.EtcdVersion = vc.GetTarget("etcd")
    // 注意: 不更新 kubelet 版本（延迟升级）
    
    return nil
}
```

### 6.3 kubelet 补充升级

```go
// upgradeKubeletCatchup kubelet 补充升级（从当前版本逐版本升级到目标版本）
func (r *ClusterVersionReconciler) upgradeKubeletCatchup(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    vc *upgrade.VersionContext,
    completedHops []string,
) error {
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    
    currentKubelet := vc.GetCurrent("kubelet")
    targetKubelet := vc.GetTarget("kubelet")
    
    log.Info("starting kubelet catchup upgrade",
        "from", currentKubelet, "to", targetKubelet)
    
    // 1. 计算需要经过的中间版本
    intermediateVersions := computeIntermediateVersions(currentKubelet, targetKubelet)
    // 例如: v1.34.0 → v1.36.0 → ["v1.35.0", "v1.36.0"]
    
    for _, version := range intermediateVersions {
        log.Info("upgrading kubelet to intermediate version",
            "from", currentKubelet, "to", version)
        
        // 2. 通过 BinaryInstaller 执行 kubelet 升级
        //    逐节点 drain → replace binary → restart → health check → uncordon
        if err := r.executeKubeletUpgrade(ctx, bkeCluster, version); err != nil {
            return fmt.Errorf("kubelet upgrade to %s failed: %w", version, err)
        }
        
        // 3. 更新 VersionContext
        currentKubelet = version
        vc.SetCurrent("kubelet", version)
        
        // 4. 每次中间版本升级后验证偏差
        ok, violations := skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
        if !ok {
            return fmt.Errorf("skew violation during kubelet catchup at %s: %v",
                version, violations)
        }
        
        log.Info("kubelet upgraded to intermediate version, skew check passed",
            "version", version)
    }
    
    // 5. 更新 kubelet 版本状态
    bkeCluster.Status.KubeletVersion = targetKubelet
    
    log.Info("kubelet catchup upgrade completed", "final", targetKubelet)
    return nil
}

// computeIntermediateVersions 计算从 currentVersion 到 targetVersion 的中间版本列表
// 例如: ("v1.34.0", "v1.36.0") → ["v1.35.0", "v1.36.0"]
func computeIntermediateVersions(currentVersion, targetVersion string) []string {
    currentMinor := parseMinorVersion(currentVersion)
    targetMinor := parseMinorVersion(targetVersion)
    
    var versions []string
    for minor := currentMinor + 1; minor <= targetMinor; minor++ {
        versions = append(versions, fmt.Sprintf("v1.%d.0", minor))
    }
    
    return versions
}

// executeKubeletUpgrade 执行 kubelet 到指定版本的升级
func (r *ClusterVersionReconciler) executeKubeletUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) error {
    // 1. 加载 kubelet ComponentVersion
    cv, err := r.CVStore.GetComponentVersion(ctx, "kubelet", targetVersion)
    if err != nil {
        return fmt.Errorf("get kubelet component version %s: %w", targetVersion, err)
    }
    
    // 2. 构建 BinaryInstaller 选项
    opts := binaryinstaller.InstallOptions{
        Component:  cv,
        Action:     binaryinstaller.BinaryActionUpgrade,
        TemplateCtx: r.buildTemplateContext(bkeCluster),
    }
    
    // 3. 获取所有节点
    bkeNodes, err := r.NodeFetcher().GetBKENodesWrapperForCluster(ctx, bkeCluster)
    if err != nil {
        return err
    }
    
    allNodes := bkeNodes.ToNodes()
    
    // 4. 创建 drainer
    drainer := phaseutil.NewDrainer(true, true, true, 20*time.Second)
    
    var failedNodes []string
    
    // 5. 逐节点升级（Master 阻塞式，Worker 非阻塞式）
    for _, node := range allNodes {
        // 5a. drain 节点
        if err := drainer.Drain(ctx, node.Hostname); err != nil {
            log.Error(err, "drain failed", "node", node.IP)
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("drain master node %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5b. 设置目标节点 IP
        opts.TemplateCtx.NodeIP = node.IP
        
        // 5c. 执行 BinaryInstaller 安装（升级）
        if err := r.BinaryInstaller.Install(ctx, opts); err != nil {
            log.Error(err, "kubelet upgrade failed", "node", node.IP)
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("kubelet upgrade on master %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5d. 等待节点健康检查
        if err := waitForNodeHealthCheck(ctx, r.Client, bkeCluster, node, targetVersion); err != nil {
            failedNodes = append(failedNodes, node.IP)
            if isMasterNode(node) {
                return fmt.Errorf("health check on master %s failed: %w", node.IP, err)
            }
            continue
        }
        
        // 5e. uncordon 节点
        _ = uncordonNode(ctx, r.Client, bkeCluster, node.Hostname)
        
        log.Info("kubelet upgraded", "node", node.IP, "version", targetVersion)
    }
    
    if len(failedNodes) > 0 {
        log.Info("kubelet upgrade completed with failures", "failedNodes", failedNodes)
        // Worker 节点失败不阻塞，Reconcile 重试时跳过已升级节点
    }
    
    return nil
}
```

## 7. 升级顺序总结

| 阶段 | 组件 | 升级方向 | 偏差状态 | 说明 |
|------|------|---------|---------|------|
| Hop 1 - 控制面 | etcd | v3.5.18→v3.5.19 | - | 先升级数据存储 |
| Hop 1 - 控制面 | apiserver | v1.34→v1.35 | - | 控制面入口 |
| Hop 1 - 控制面 | cm/scheduler | v1.34→v1.35 | cm/scheduler ≤ apiserver（允许滞后 1） | 紧随 apiserver |
| Hop 1 - 控制面 | kube-proxy | v1.34→v1.35 | kube-proxy ≤ apiserver（允许滞后 3） | 随 apiserver 同步升级 |
| Hop 1 - 控制面 | kubectl | v1.34→v1.35 | kubectl ≤ apiserver（允许滞后 1） | 随 apiserver 同步升级（仅替换二进制） |
| Hop 1 - 控制面 | kubelet | **保持 v1.34** | kubelet vs apiserver = 1 偏差 | **延迟升级**（允许滞后 3） |
| **偏差门 1** | - | - | 1 偏差，✅ 安全 | 可继续 Hop 2 |
| Hop 2 - 控制面 | apiserver | v1.35→v1.36 | - | 第二跳 |
| Hop 2 - 控制面 | cm/scheduler | v1.35→v1.36 | cm/scheduler ≤ apiserver | 紧随 apiserver |
| Hop 2 - 控制面 | kube-proxy | v1.35→v1.36 | kube-proxy ≤ apiserver | 随 apiserver 同步升级 |
| Hop 2 - 控制面 | kubectl | v1.35→v1.36 | kubectl ≤ apiserver | 随 apiserver 同步升级 |
| Hop 2 - 控制面 | kubelet | **仍保持 v1.34** | kubelet vs apiserver = 2 偏差 | **安全**（允许滞后 3） |
| **偏差门 2** | - | - | 2 偏差，✅ 安全 | 可继续 Hop 3（如有） |
| Hop 3（如有） | apiserver | v1.36→v1.37 | - | 第三跳 |
| Hop 3（如有） | cm/scheduler | v1.36→v1.37 | cm/scheduler ≤ apiserver | 紧随 apiserver |
| Hop 3（如有） | kube-proxy | v1.36→v1.37 | kube-proxy ≤ apiserver | 随 apiserver 同步升级 |
| Hop 3（如有） | kubectl | v1.36→v1.37 | kubectl ≤ apiserver | 随 apiserver 同步升级 |
| Hop 3（如有） | kubelet | **仍保持 v1.34** | kubelet vs apiserver = 3 偏差 | **达到极限** |
| **偏差门 3** | - | - | 3 偏差，⚠️ 极限 | **必须升级 kubelet** |
| Kubelet 补充 | kubelet | v1.34→v1.35 | 2→1 偏差 | 逐节点 drain 升级 |
| Kubelet 补充 | kubelet | v1.35→v1.36 | 1→0 偏差 | 逐节点 drain 升级 |
| Kubelet 补充 | kubelet | v1.36→v1.37 | 0 偏差 | 逐节点 drain 升级 |
| **最终验证** | - | - | 0 偏差，✅ 通过 | 升级完成 |

## 8. 优势与适用场景

### 8.1 优势

| 优势 | 说明 |
|------|------|
| **控制面快速升级** | apiserver/cm/scheduler 不等 kubelet，快速完成多 hop |
| **kubelet 延迟升级** | 大规模集群中 kubelet 逐节点 drain 耗时，延迟到偏差达到极限（3）时批量升级 |
| **偏差安全** | 偏差门控确保每个阶段都满足 K8s 版本偏差约束 |
| **可观测** | 每个组件独立追踪版本，偏差状态清晰可见 |
| **灵活** | 小集群可选择每 hop 都升级 kubelet（0 偏差），大集群选择延迟升级 |
| **幂等** | kubelet 补充升级跳过已升级节点，支持 Reconcile 重入 |

### 8.2 适用场景

| 场景 | 推荐策略 | 说明 |
|------|---------|------|
| **小集群（<10 节点）** | 同步升级 | 每个 hop 内 kubelet 与 apiserver 同步升级，偏差始终为 0 |
| **中等集群（10-50 节点）** | 延迟 1-2 hop | kubelet 延迟 1-2 个 hop，偏差达到 2-3 时补充升级 |
| **大集群（>50 节点）** | 延迟到极限 | kubelet 延迟到偏差达到 3（极限），然后批量补充升级 |
| **跨 3+ hop 升级** | 必须延迟 | kubelet 可利用 3 版本偏差窗口延迟到最后批量升级 |

### 8.3 策略配置

```go
// KubeletUpgradeStrategy kubelet 升级策略
type KubeletUpgradeStrategy string

const (
    // KubeletStrategySync 每个 hop 内同步升级 kubelet
    KubeletStrategySync KubeletUpgradeStrategy = "Sync"
    
    // KubeletStrategyDeferred 延迟到偏差极限时升级
    KubeletStrategyDeferred KubeletUpgradeStrategy = "Deferred"
)

// UpgradeOrchestrationConfig 升级编排配置
type UpgradeOrchestrationConfig struct {
    // kubelet 升级策略
    KubeletStrategy KubeletUpgradeStrategy
    
    // 最大允许偏差（默认 3，与 K8s v1.25+ 官方一致）
    MaxKubeletSkew int
    
    // kubelet 补充升级的批次大小（每批 drain 多少节点）
    KubeletBatchSize int
}
```

## 9. ReleaseImage 中 K8s 核心组件重构方案

### 9.1 当前结构的问题

当前 ReleaseImage 中 K8s 核心组件以 `kubernetes-master`（inline 黑盒）和 `kubernetes-worker`（inline 黑盒）的形式存在：

```yaml
# 当前结构
upgrade:
  components:
    - name: kubernetes-master          # 黑盒：apiserver + cm + scheduler + kubelet + kubectl
      version: v1.36.0
      inline:
        handler: EnsureMasterUpgrade
    - name: kubernetes-worker          # 黑盒：kubelet + kubectl
      version: v1.36.0
      inline:
        handler: EnsureWorkerUpgrade
    - name: etcd                       # 独立组件
      version: v3.5.20
      inline:
        handler: EnsureEtcdUpgrade
    - name: kube-proxy                 # 独立组件
      version: v1.36.0
```

| 问题 | 说明 | 影响 |
|------|------|------|
| **kubernetes-master 是黑盒** | 一个 inline handler 统一处理 apiserver + cm + scheduler + kubelet + kubectl | 无法对单个组件独立控制版本、升级顺序、偏差约束 |
| **kubelet 版本被绑定** | kubelet 版本 = kubernetes-master 版本，无法独立管理 | 无法实现 kubelet 延迟升级策略 |
| **kubectl 版本被绑定** | kubectl 版本 = kubernetes-master 版本 | 无法独立追踪 kubectl 版本 |
| **cm/scheduler 不可见** | cm/scheduler 被 kubernetes-master 包裹，ReleaseImage 中无独立条目 | 无法独立升级 cm/scheduler |
| **偏差约束外部化** | 偏差规则定义在 `K8sSkewConstraints` 外部代码中 | 偏差约束未内化到 ComponentVersion 声明 |

### 9.2 重构目标

1. **拆解 kubernetes-master 黑盒**：将 apiserver/cm/scheduler/kubelet/kubectl 拆为 ReleaseImage 中的独立组件
2. **声明偏差约束**：在 ComponentVersion.spec 中新增 `versionSkew` 字段，内化偏差约束
3. **内聚依赖关系**：etcd → apiserver → cm/scheduler 的依赖关系声明在 ComponentVersion.spec.dependencies
4. **支持延迟升级**：kubelet 作为独立组件，可被 `executeControlPlaneHop` 通过 `deferredComponents` 跳过

### 9.3 重构后的 ReleaseImage 结构

```yaml
# 重构后：K8s 核心组件全部独立声明
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.7.0
spec:
  version: "v2.7.0"

  install:
    components:
      # ── K8s 核心组件（全部独立） ──
      - name: etcd
        version: v3.5.20
      - name: kube-apiserver
        version: v1.36.0
      - name: kube-controller-manager
        version: v1.36.0
      - name: kube-scheduler
        version: v1.36.0
      - name: kubelet
        version: v1.36.0
      - name: kubectl
        version: v1.36.0
      - name: kube-proxy
        version: v1.36.0
      # ── 其它组件 ──
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.24
      - name: coredns
        version: v1.11.3

  upgrade:
    components:
      # ── K8s 核心组件（全部独立，按依赖排序） ──
      - name: etcd
        version: v3.5.20
      - name: kube-apiserver
        version: v1.36.0
      - name: kube-controller-manager
        version: v1.36.0
      - name: kube-scheduler
        version: v1.36.0
      - name: kube-proxy
        version: v1.36.0
      - name: kubectl
        version: v1.36.0
      - name: kubelet                       # 独立组件，可被 executeControlPlaneHop 跳过
        version: v1.36.0
      # ── 其它组件 ──
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.24
      - name: coredns
        version: v1.11.3
```

### 9.4 ComponentVersion 中声明偏差约束

重构后偏差约束从外部 `K8sSkewConstraints` 规则表**内化**到每个组件的 `ComponentVersion.spec.versionSkew`：

```go
// api/v1alpha1/componentversion_types.go 扩展

// ComponentVersionSpec 新增 versionSkew 字段
type ComponentVersionSpec struct {
    // ... 现有字段 ...
    
    // 🆕新增：版本偏差约束（内化 K8s Version Skew Policy）
    VersionSkew *VersionSkewSpec `json:"versionSkew,omitempty"`
}

// VersionSkewSpec 定义组件与参照组件之间的版本偏差约束
type VersionSkewSpec struct {
    // 参照组件名称（通常是 kube-apiserver）
    ReferenceComponent string `json:"referenceComponent"`
    
    // 最大版本偏差（被约束组件最多比参照组件旧几个小版本）
    // kubelet/kube-proxy: 3（v1.25+）
    // cm/scheduler/kubectl: 1
    MaxSkewBehind int `json:"maxSkewBehind"`
    
    // 偏差方向: behind（仅允许滞后）/ bidirectional（允许双向，如 kubectl ±1）
    Direction string `json:"direction,omitempty"`
}
```

**各组件 ComponentVersion YAML 示例**：

```yaml
# kubelet（偏差约束：最多滞后 apiserver 3 个小版本）
spec:
  name: kubelet
  type: binary
  version: v1.36.0
  versionSkew:
    referenceComponent: kube-apiserver
    maxSkewBehind: 3
    direction: behind
  dependencies:
    - name: containerd
      phase: Install
    - name: kube-apiserver
      phase: Install

# kube-controller-manager（偏差约束：最多滞后 apiserver 1 个小版本）
spec:
  name: kube-controller-manager
  type: staticpod
  version: v1.36.0
  versionSkew:
    referenceComponent: kube-apiserver
    maxSkewBehind: 1
    direction: behind
  dependencies:
    - name: kube-apiserver
      phase: Install

# kube-scheduler（偏差约束：最多滞后 apiserver 1 个小版本）
spec:
  name: kube-scheduler
  type: staticpod
  version: v1.36.0
  versionSkew:
    referenceComponent: kube-apiserver
    maxSkewBehind: 1
    direction: behind
  dependencies:
    - name: kube-apiserver
      phase: Install

# kube-proxy（偏差约束：最多滞后 apiserver 3 个小版本）
spec:
  name: kube-proxy
  type: yaml
  version: v1.36.0
  versionSkew:
    referenceComponent: kube-apiserver
    maxSkewBehind: 3
    direction: behind
  dependencies:
    - name: kube-apiserver
      phase: Install

# kubectl（偏差约束：允许双向 ±1）
spec:
  name: kubectl
  type: binary
  version: v1.36.0
  versionSkew:
    referenceComponent: kube-apiserver
    maxSkewBehind: 1
    direction: bidirectional
  dependencies:
    - name: kubelet
      phase: Install

# etcd（无 versionSkew，但声明兼容性约束）
spec:
  name: etcd
  type: staticpod
  version: v3.5.20
  # etcd 不在 K8s Version Skew Policy 中，无需 versionSkew
  # 但通过 compatibility 声明与 K8s 版本的配套关系
  compatibility:
    - component: kube-apiserver
      rule: ">=v1.34"
  dependencies:
    - name: kubelet
      phase: Install
```

### 9.5 重构后的偏差检查

偏差约束内化后，`VersionSkewChecker` 从外部规则表读取改为从 ComponentVersion 读取：

```go
// pkg/upgrade/skew_checker.go 重构

// CheckSkewFromComponentVersions 从 ComponentVersion 的 versionSkew 字段读取约束
// 替代原有的 K8sSkewConstraints 外部规则表
func (c *VersionSkewChecker) CheckSkewFromComponentVersions(
    ctx context.Context,
    vc *VersionContext,
    cvStore ComponentVersionStore,
) (bool, []SkewViolation) {
    var violations []SkewViolation
    
    // 遍历 VersionContext 中的所有组件
    for name := range vc.Current {
        // 从 ComponentVersion 获取 versionSkew 声明
        cv, err := cvStore.GetComponentVersion(ctx, name, vc.GetCurrent(name))
        if err != nil || cv.Spec.VersionSkew == nil {
            continue // 无偏差约束声明，跳过
        }
        
        skewSpec := cv.Spec.VersionSkew
        componentVersion := vc.GetCurrent(name)
        referenceVersion := vc.GetCurrent(skewSpec.ReferenceComponent)
        
        if componentVersion == "" || referenceVersion == "" {
            continue
        }
        
        skew := computeMinorVersionSkew(referenceVersion, componentVersion)
        
        if skew < 0 {
            // 组件比参照新，违反约束
            violations = append(violations, SkewViolation{
                Component:    name,
                Reference:    skewSpec.ReferenceComponent,
                ComponentVer: componentVersion,
                ReferenceVer: referenceVersion,
                Skew:         skew,
                Reason:       "component is newer than reference (not allowed)",
            })
        } else if skew > skewSpec.MaxSkewBehind {
            // 偏差超过限制
            violations = append(violations, SkewViolation{
                Component:    name,
                Reference:    skewSpec.ReferenceComponent,
                ComponentVer: componentVersion,
                ReferenceVer: referenceVersion,
                Skew:         skew,
                Reason:       fmt.Sprintf("skew %d exceeds max %d", skew, skewSpec.MaxSkewBehind),
            })
        }
    }
    
    return len(violations) == 0, violations
}
```

### 9.6 重构后的 DAG 依赖关系

```
重构后的 DAG（K8s 核心组件独立声明，依赖关系内化到 ComponentVersion）:

安装 DAG:
  Batch 1: [etcd]                    ← type: staticpod
      └─ StaticPodInstaller           ← 依赖 kubelet（Static Pod 需 Kubelet 拉起）

  Batch 2: [kube-apiserver]          ← type: staticpod
      └─ StaticPodInstaller           ← 依赖 etcd（数据存储）

  Batch 3: [kube-controller-manager,  ← type: staticpod, 并行
            kube-scheduler]            ← 依赖 apiserver
      ├─ StaticPodInstaller
      └─ StaticPodInstaller

  Batch 4: [kube-proxy]              ← type: yaml
      └─ YamlInstaller Apply          ← 依赖 apiserver

  Batch 5: [kubelet, kubectl]        ← type: binary, 并行
      ├─ BinaryInstaller (kubelet)    ← 依赖 containerd + apiserver
      └─ BinaryInstaller (kubectl)    ← 依赖 kubelet

升级 DAG（延迟 kubelet）:
  Batch 1: [etcd]                    ← 先升级数据存储
  Batch 2: [kube-apiserver]          ← 控制面入口
  Batch 3: [cm, scheduler]           ← 跟随 apiserver
  Batch 4: [kube-proxy]              ← 匹配 apiserver
  Batch 5: [kubectl]                 ← 命令行工具
  ─── kubelet 延迟到偏差门控后 ───
  Batch 6 (延迟): [kubelet]          ← 偏差达到极限时补充升级
```

### 9.7 重构后的 VersionContext

```go
// 重构后 VersionContext 可独立追踪每个 K8s 核心组件版本
// Current 来自当前 ReleaseImage bundle
// Target 来自目标 ReleaseImage bundle

// 示例：v1.34 → v1.36 升级
vc := &VersionContext{
    Current: map[string]string{
        "etcd":                    "v3.5.18",
        "kube-apiserver":          "v1.34.0",
        "kube-controller-manager": "v1.34.0",
        "kube-scheduler":          "v1.34.0",
        "kube-proxy":              "v1.34.0",
        "kubectl":                 "v1.34.0",
        "kubelet":                 "v1.34.0",    // 独立追踪
    },
    Target: map[string]string{
        "etcd":                    "v3.5.20",
        "kube-apiserver":          "v1.36.0",
        "kube-controller-manager": "v1.36.0",
        "kube-scheduler":          "v1.36.0",
        "kube-proxy":              "v1.36.0",
        "kubectl":                 "v1.36.0",
        "kubelet":                 "v1.36.0",    // 独立追踪
    },
}

// 偏差检查直接基于 VersionContext + ComponentVersion.versionSkew
// 不再需要外部 K8sSkewConstraints 规则表
skew := computeMinorVersionSkew(
    vc.GetCurrent("kube-apiserver"),  // v1.36.0
    vc.GetCurrent("kubelet"),         // v1.34.0（延迟升级）
)
// skew = 2，允许 3 → ✅ 安全
```

### 9.8 重构收益

| 维度 | 重构前（kubernetes-master 黑盒） | 重构后（独立组件） |
|------|-------------------------------|------------------|
| **版本追踪** | 仅 `KubernetesVersion` 一个版本号 | 7 个独立版本号（apiserver/cm/scheduler/kubelet/kubectl/kube-proxy/etcd） |
| **偏差约束** | 外部 `K8sSkewConstraints` 规则表 | 内化到 `ComponentVersion.spec.versionSkew` |
| **依赖关系** | kubeadm 内部硬编码顺序 | `ComponentVersion.spec.dependencies` 声明式 |
| **kubelet 延迟** | 不支持（kubeadm 强制升级） | 支持（独立组件，可被 `executeControlPlaneHop` 跳过） |
| **升级粒度** | 粗粒度（整个 kubernetes-master） | 细粒度（每个组件独立升级/回滚） |
| **可观测性** | 仅集群级版本状态 | 组件级版本状态（`ClusterComponentStatuses`） |
| **偏差检查** | 需要外部 `VersionSkewChecker` + 规则表 | 直接从 `ComponentVersion.versionSkew` + `VersionContext` 计算 |

### 9.9 迁移策略

| 阶段 | 目标 | 说明 | Feature Gate |
|------|------|------|-------------|
| **阶段 1** | etcd 独立 | etcd 已独立（现状），保持不变 | - |
| **阶段 2** | kubelet + kubectl 独立 | 从 kubernetes-master 拆出为 binary 类型（KEP-13） | `KubeletBinaryMigration` |
| **阶段 3** | apiserver/cm/scheduler 独立 | 从 kubernetes-master 拆出为 staticpod 类型（KEP-9） | `StaticPodComponentEnabled` |
| **阶段 4** | 删除 kubernetes-master | 所有子组件独立后，kubernetes-master 不再需要 | 移除 Feature Gate |
| **阶段 5** | 偏差约束内化 | versionSkew 从外部规则表迁移到 ComponentVersion.spec | `VersionSkewInComponentVersion` |

```
迁移路径:

阶段 1: 现状
  ReleaseImage: kubernetes-master(inline) + etcd(inline) + kube-proxy(yaml)
  偏差检查: 外部 K8sSkewConstraints

阶段 2: kubelet + kubectl 独立
  ReleaseImage: kubernetes-master(inline) + etcd(inline) + kube-proxy(yaml)
               + kubelet(binary) + kubectl(binary) 🆕
  偏差检查: 外部 K8sSkewConstraints
  Feature Gate: KubeletBinaryMigration ON → kubelet 由 BinaryInstaller 处理
                 kubernetes-master 的 installKubeletCommand 跳过

阶段 3: apiserver/cm/scheduler 独立
  ReleaseImage: kubernetes-master(inline, 仅剩 kubeadm 引导逻辑)
               + etcd(staticpod) + kube-apiserver(staticpod) 🆕
               + kube-controller-manager(staticpod) 🆕 + kube-scheduler(staticpod) 🆕
               + kubelet(binary) + kubectl(binary) + kube-proxy(yaml)
  偏差检查: 外部 K8sSkewConstraints
  Feature Gate: StaticPodComponentEnabled ON → apiserver/cm/scheduler 由 StaticPodInstaller 处理

阶段 4: 删除 kubernetes-master
  ReleaseImage: etcd(staticpod) + kube-apiserver(staticpod)
               + kube-controller-manager(staticpod) + kube-scheduler(staticpod)
               + kubelet(binary) + kubectl(binary) + kube-proxy(yaml)
  偏差检查: 外部 K8sSkewConstraints
  Feature Gate: 移除（kubernetes-master 不再存在）

阶段 5: 偏差约束内化
  ReleaseImage: 同阶段 4
  偏差检查: ComponentVersion.spec.versionSkew（内化）🆕
  Feature Gate: VersionSkewInComponentVersion ON → 从 ComponentVersion 读取偏差约束
                 外部 K8sSkewConstraints 标记为 deprecated
```

### 9.10 控制器中多 Hop 升级的设计

#### 9.10.1 设计思路

**核心约束**：`hopPath` 是 openFuyao 版本列表（如 `["v2.6.5", "v2.7.0"]`），**不是** K8s 版本列表。K8s 组件版本（kube-apiserver/kubelet 等）从每个 openFuyao 版本对应的 ReleaseImage 中按组件名解析。

**版本映射关系**：

```
用户层:    ClusterVersion.Spec.DesiredVersion = "v2.7.0"  (openFuyao 版本)

编排层:    hopPath = ["v2.6.5", "v2.7.0"]  (openFuyao 版本路径)
                    │                    │
                    ▼                    ▼
版本解析:  ReleaseImage v2.6.5    ReleaseImage v2.7.0
           ├─ kube-apiserver: v1.35.0    ├─ kube-apiserver: v1.36.0
           ├─ kubelet: v1.35.0           ├─ kubelet: v1.36.0
           ├─ etcd: v3.5.19              ├─ etcd: v3.5.20
           └─ ...                        └─ ...

偏差门控:  kubelet(v1.34) vs apiserver(v1.35) = 1  ← 从 ReleaseImage 解析后的 K8s 版本计算
```

**设计原则**：

1. **hopPath 驱动**：编排器遍历 openFuyao 版本路径，每个 hop 对应一个 openFuyao 版本
2. **K8s 版本延迟解析**：K8s 组件版本不在编排层暴露，由 `executeControlPlaneHop` 从 ReleaseImage 中按组件名解析
3. **偏差门控基于 K8s 版本**：偏差计算使用从 ReleaseImage 解析的 K8s 组件版本，而非 openFuyao 版本
4. **kubelet 延迟升级**：kubelet 在 `executeControlPlaneHop` 中被跳过（`deferredComponents=["kubelet"]`），偏差达到极限时补充升级
5. **kubelet 补充目标使用 K8s 版本**：`upgradeKubeletCatchupToVersion` 的参数是 K8s 版本（如 `v1.36.0`），从 ReleaseImage 解析的 apiserver 版本
6. **断点续传**：通过 annotation 记录 hop 进度（openFuyao 版本 + hop 阶段），Reconcile 重入时从断点恢复

**控制器协作模型**：

```
┌─────────────────────────────────────────────────────────────────────────┐
│  ClusterVersionReconciler（编排器）                                     │
│  职责: hopPath 遍历 + 偏差门控 + kubelet 补充升级 + 状态管理          │
│                                                                         │
│  for each openFuyao hop in hopPath:                                    │
│    1. 设置 upgrade-ready annotation = openFuyao 版本                     │
│    2. 等待 BKEClusterReconciler 完成（清除 annotation）                 │
│    3. 从 HopResult 提取 apiserver 版本（K8s 版本，非 openFuyao 版本）   │
│    4. 偏差门控: kubelet(K8s 版本) vs apiserver(K8s 版本)               │
│    5. 偏差 >= 3 → kubelet 补充升级到 apiserver 的 K8s 版本              │
│                                                                         │
│  ─── K8s 核心组件全部完成后 ───                                        │
│  阶段二: 其它组件升级（openFuyao 版本的最终 ReleaseImage）              │
└─────────────────────────────────────────────────────────────────────────┘
                              │ upgrade-ready annotation (openFuyao 版本)
                              ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  BKEClusterReconciler（执行器）                                         │
│  职责: 解析 ReleaseImage → 构建 DAG → 执行组件升级                       │
│                                                                         │
│  1. 读取 upgrade-ready annotation → openFuyao 版本                      │
│  2. 解析 ReleaseImage → K8s 组件版本（apiserver/kubelet/etcd 等）       │
│  3. 构建 VersionContext（kubelet Target=Current 跳过）                  │
│  4. 构建 DAG: etcd→apiserver→cm/scheduler→kube-proxy→kubectl            │
│  5. Scheduler.ExecuteDAG() → 返回 HopResult（含 K8s 组件版本）          │
│  6. 清除 upgrade-ready annotation                                      │
└─────────────────────────────────────────────────────────────────────────┘
                              │ Command CR
                              ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  BKEAgent（节点级执行）                                                 │
│  StaticPodInstaller / BinaryInstaller / YamlInstaller                   │
└─────────────────────────────────────────────────────────────────────────┘
```

#### 9.10.2 编排配置

```go
// UpgradeOrchestrationConfig 升级编排配置
type UpgradeOrchestrationConfig struct {
    // kubelet 升级策略
    KubeletStrategy KubeletUpgradeStrategy
    
    // 延迟升级的组件列表（默认 ["kubelet"]）
    DeferredComponents []string `json:"deferredComponents,omitempty"`
    
    // 最大允许偏差（默认 3，与 K8s v1.25+ 官方一致）
    // 注意: 这是 K8s 版本的偏差，不是 openFuyao 版本的偏差
    MaxKubeletSkew int `json:"maxKubeletSkew,omitempty"`
    
    // 是否从 ComponentVersion.versionSkew 读取偏差约束
    // false: 使用 MaxKubeletSkew（外部配置）
    // true: 从 ComponentVersion.spec.versionSkew 读取（内化约束）
    UseVersionSkewFromComponentVersion bool `json:"useVersionSkewFromComponentVersion,omitempty"`
    
    // kubelet 补充升级的批次大小
    KubeletBatchSize int `json:"kubeletBatchSize,omitempty"`
}
```

#### 9.10.3 ClusterVersionReconciler Reconcile 入口

```go
// controllers/clusterversion/clusterversion_controller.go

func (r *ClusterVersionReconciler) Reconcile(
    ctx context.Context,
    req ctrl.Request,
) (ctrl.Result, error) {
    log := log.FromContext(ctx)
    
    // 1. 获取 ClusterVersion
    cv := &cvv1alpha1.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. 检测 openFuyao 版本变化
    if cv.Status.CurrentVersion == cv.Spec.DesiredVersion {
        return ctrl.Result{}, nil
    }
    
    // 3. 校验 UpgradePath，解析 openFuyao 版本路径
    // hopPath 是 openFuyao 版本列表，如 ["v2.6.5", "v2.7.0"]
    // K8s 组件版本不在此时解析，由每个 hop 执行时从 ReleaseImage 解析
    hopPath, err := r.resolveUpgradePath(ctx, cv.Status.CurrentVersion, cv.Spec.DesiredVersion)
    if err != nil {
        log.Error(err, "failed to resolve upgrade path")
        r.Recorder.Eventf(cv, corev1.EventTypeWarning, "UpgradePathInvalid", "%v", err)
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }
    log.Info("upgrade path resolved", "hops", hopPath)
    
    // 4. 获取关联的 BKECluster
    bkeCluster, err := r.getBKEClusterForClusterVersion(ctx, cv)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 5. 解析升级编排配置
    config := r.resolveOrchestrationConfig(bkeCluster)
    
    // 6. 更新 ClusterVersion.Status.Phase
    cv.Status.Phase = cvv1alpha1.ClusterVersionPhaseUpgrading
    if err := r.Status().Update(ctx, cv); err != nil {
        return ctrl.Result{}, err
    }
    
    // 7. 执行完整升级编排
    if err := r.orchestrateFullUpgrade(ctx, bkeCluster, hopPath, config); err != nil {
        log.Error(err, "full upgrade failed")
        cv.Status.Phase = cvv1alpha1.ClusterVersionPhaseFailed
        cv.Status.LastError = err.Error()
        _ = r.Status().Update(ctx, cv)
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }
    
    // 8. 升级完成
    cv.Status.Phase = cvv1alpha1.ClusterVersionPhaseReady
    cv.Status.CurrentVersion = cv.Spec.DesiredVersion
    if err := r.Status().Update(ctx, cv); err != nil {
        return ctrl.Result{}, err
    }
    
    r.Recorder.Eventf(cv, corev1.EventTypeNormal, "UpgradeCompleted",
        "Cluster upgraded to %s", cv.Spec.DesiredVersion)
    
    return ctrl.Result{}, nil
}

// resolveUpgradePath 解析 openFuyao 版本升级路径
// currentVersion/desiredVersion 是 openFuyao 版本，如 "v2.6.0"/"v2.7.0"
// 返回 openFuyao 版本列表，如 ["v2.6.5", "v2.7.0"]
// K8s 组件版本不在此函数解析，由每个 hop 从对应 ReleaseImage 解析
func (r *ClusterVersionReconciler) resolveUpgradePath(
    ctx context.Context,
    currentVersion string,  // openFuyao 版本
    desiredVersion string,  // openFuyao 版本
) ([]string, error) {
    up := &cvv1alpha1.UpgradePath{}
    if err := r.Get(ctx, client.ObjectKey{Name: "openfuyao-upgrade-paths"}, up); err != nil {
        return nil, fmt.Errorf("get upgrade path: %w", err)
    }
    
    path, err := findPath(up.Spec.Paths, currentVersion, desiredVersion)
    if err != nil {
        return nil, fmt.Errorf("no valid upgrade path from %s to %s: %w",
            currentVersion, desiredVersion, err)
    }
    
    // 检查每个 openFuyao 版本的 ReleaseImage 是否存在且有效
    for _, hop := range path {
        ri, err := r.resolveReleaseImage(ctx, hop)
        if err != nil {
            return nil, fmt.Errorf("release image %s not found: %w", hop, err)
        }
        if ri.Status.Phase != cvv1alpha1.ReleaseImagePhaseValid {
            return nil, fmt.Errorf("release image %s is not valid (phase=%s)",
                hop, ri.Status.Phase)
        }
    }
    
    return path, nil
}

// resolveOrchestrationConfig 从 BKECluster annotation 或默认值解析编排配置
func (r *ClusterVersionReconciler) resolveOrchestrationConfig(
    bkeCluster *bkev1beta1.BKECluster,
) *UpgradeOrchestrationConfig {
    config := &UpgradeOrchestrationConfig{
        KubeletStrategy:                    KubeletStrategyDeferred,
        DeferredComponents:                 []string{"kubelet"},
        MaxKubeletSkew:                     3,
        UseVersionSkewFromComponentVersion: false,
        KubeletBatchSize:                   1,
    }
    
    if v, ok := bkeCluster.Annotations["bke.bocloud.com/kubelet-upgrade-strategy"]; ok {
        config.KubeletStrategy = KubeletUpgradeStrategy(v)
    }
    if v, ok := bkeCluster.Annotations["bke.bocloud.com/max-kubelet-skew"]; ok {
        if n, err := strconv.Atoi(v); err == nil {
            config.MaxKubeletSkew = n
        }
    }
    if _, ok := bkeCluster.Annotations["bke.bocloud.com/use-version-skew-from-cv"]; ok {
        config.UseVersionSkewFromComponentVersion = true
    }
    
    return config
}
```

#### 9.10.4 完整升级编排

```go
// orchestrateFullUpgrade 完整升级编排
// hopPath: openFuyao 版本列表，如 ["v2.6.5", "v2.7.0"]
// 阶段一: K8s 核心组件多 hop 升级（偏差门控 + kubelet 延迟）
//         每个 hop 从对应 openFuyao 版本的 ReleaseImage 解析 K8s 组件版本
// 阶段二: 其它组件升级（独立 DAG，无偏差门控）
func (r *ClusterVersionReconciler) orchestrateFullUpgrade(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopPath []string, // openFuyao 版本列表
    config *UpgradeOrchestrationConfig,
) error {
    // ─── 阶段一: K8s 核心组件多 Hop 升级 ───
    log.Info("phase 1: K8s core components multi-hop upgrade")
    if err := r.orchestrateK8sCoreMultiHop(ctx, bkeCluster, hopPath, config); err != nil {
        return fmt.Errorf("phase 1 (K8s core) failed: %w", err)
    }
    
    // 最终偏差验证
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    vc := r.getCurrentVersionContext(bkeCluster)
    
    var ok bool
    var violations []upgrade.SkewViolation
    if config.UseVersionSkewFromComponentVersion {
        ok, violations = skewChecker.CheckSkewFromComponentVersions(ctx, vc, r.CVStore)
    } else {
        ok, violations = skewChecker.CheckSkew(vc, upgrade.K8sSkewConstraints)
    }
    if !ok {
        return fmt.Errorf("phase 1 skew check failed: %v", violations)
    }
    log.Info("phase 1 completed: K8s core components upgraded, skew satisfied")
    
    // ─── 阶段二: 其它组件升级 ───
    log.Info("phase 2: other components upgrade")
    if err := r.executeOtherComponentsUpgrade(ctx, bkeCluster, hopPath[len(hopPath)-1]); err != nil {
        return fmt.Errorf("phase 2 (other components) failed: %w", err)
    }
    
    log.Info("full upgrade completed: all components upgraded")
    return nil
}
```

#### 9.10.5 K8s 核心组件多 Hop 编排（含断点续传）

```go
// orchestrateK8sCoreMultiHop K8s 核心组件多 hop 升级
//
// hopPath: openFuyao 版本列表，如 ["v2.6.5", "v2.7.0"]
// 每个 hop 内部从对应 openFuyao 版本的 ReleaseImage 解析 K8s 组件版本
// K8s 版本不直接出现在 hopPath 中，而是从 ReleaseImage 中按组件名解析
//
// 偏差门控基于 K8s 版本（从 ReleaseImage 解析），不是 openFuyao 版本
// kubelet 补充升级目标使用 K8s 版本（apiserver 的 K8s 版本）
func (r *ClusterVersionReconciler) orchestrateK8sCoreMultiHop(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopPath []string, // openFuyao 版本列表
    config *UpgradeOrchestrationConfig,
) error {
    skewChecker := &upgrade.VersionSkewChecker{Client: r.Client}
    deferredComponents := config.DeferredComponents
    
    // 断点续传：从上次中断的 hop 恢复
    currentHopIndex := r.getCurrentHopIndex(bkeCluster)
    kubeletCurrentVersion := r.getComponentVersion(bkeCluster, "kubelet") // K8s 版本
    
    for i := currentHopIndex; i < len(hopPath); i++ {
        hopTarget := hopPath[i] // openFuyao 版本，如 "v2.6.5"
        log.Info("starting K8s core hop",
            "hop", i+1, "openFuyaoVersion", hopTarget,
            "kubeletCurrent(K8s)", kubeletCurrentVersion,
            "resumed", i > currentHopIndex)
        
        // 记录当前 hop 进度
        r.setCurrentHopIndex(bkeCluster, i)
        
        // 1. 检查当前 hop 是否已部分完成（重入场景）
        hopPhase := r.getHopPhase(bkeCluster, hopTarget)
        if hopPhase == HopPhaseControlPlaneCompleted {
            apiserverVersion := r.getComponentVersion(bkeCluster, "kube-apiserver")
            if r.needsKubeletCatchup(ctx, config, kubeletCurrentVersion, apiserverVersion, i, hopPath) {
                if err := r.upgradeKubeletCatchupToVersion(
                    ctx, bkeCluster, kubeletCurrentVersion, apiserverVersion,
                ); err != nil {
                    return fmt.Errorf("kubelet catchup (resumed) failed: %w", err)
                }
                kubeletCurrentVersion = apiserverVersion
                r.setComponentVersion(bkeCluster, "kubelet", kubeletCurrentVersion)
            }
            r.setHopPhase(bkeCluster, hopTarget, HopPhaseCompleted)
            continue
        }
        
        // 2. 执行当前 hop（设置 upgrade-ready annotation → BKEClusterReconciler 执行 DAG）
        hopResult, err := r.executeControlPlaneHop(
            ctx, bkeCluster, hopTarget, deferredComponents,
        )
        if err != nil {
            r.setHopPhase(bkeCluster, hopTarget, HopPhaseFailed)
            return fmt.Errorf("hop %d (%s) failed: %w", i+1, hopTarget, err)
        }
        
        r.updateComponentStatuses(bkeCluster, hopResult)
        r.setHopPhase(bkeCluster, hopTarget, HopPhaseControlPlaneCompleted)
        
        // 3. 从 HopResult 提取 K8s 版本进行偏差门控
        // apiserverVersion 是 K8s 版本（如 "v1.35.0"），从 ReleaseImage 解析
        apiserverVersion := hopResult.GetUpgradedVersion("kube-apiserver")
        kubeletCurrentVersion = r.getComponentVersion(bkeCluster, "kubelet")
        
        // 4. 偏差门控：基于 K8s 版本计算偏差
        currentSkew := computeMinorVersionSkew(apiserverVersion, kubeletCurrentVersion)
        log.Info("hop completed, checking skew",
            "hop", i+1, "openFuyaoVersion", hopTarget,
            "apiserver(K8s)", apiserverVersion,
            "kubelet(K8s)", kubeletCurrentVersion,
            "skew", currentSkew)
        
        // 5. 前瞻性检查 + kubelet 补充升级判断
        needsCatchup, catchupTargetVersion := r.evaluateSkewGate(
            ctx, config, kubeletCurrentVersion, apiserverVersion, i, hopPath,
        )
        
        if needsCatchup {
            // catchupTargetVersion 是 K8s 版本（如 "v1.36.0"），从 ReleaseImage 解析
            if err := r.upgradeKubeletCatchupToVersion(
                ctx, bkeCluster, kubeletCurrentVersion, catchupTargetVersion,
            ); err != nil {
                return fmt.Errorf("kubelet catchup to %s failed: %w",
                    catchupTargetVersion, err)
            }
            kubeletCurrentVersion = catchupTargetVersion
            r.setComponentVersion(bkeCluster, "kubelet", kubeletCurrentVersion)
        }
        
        // 6. 持久化状态（支持重入恢复）
        if err := r.Status().Update(ctx, bkeCluster); err != nil {
            log.Error(err, "failed to persist hop status")
        }
        
        r.setHopPhase(bkeCluster, hopTarget, HopPhaseCompleted)
        
        log.Info("hop fully completed",
            "hop", i+1, "openFuyaoVersion", hopTarget,
            "apiserver(K8s)", apiserverVersion,
            "kubelet(K8s)", kubeletCurrentVersion)
    }
    
    r.clearHopProgress(bkeCluster)
    return nil
}

// evaluateSkewGate 评估偏差门控
// kubeletCurrentVersion/apiserverVersion 是 K8s 版本（从 ReleaseImage 解析）
// hopPath 是 openFuyao 版本列表
// 返回: needsCatchup=true + catchupTargetVersion(K8s 版本)
func (r *ClusterVersionReconciler) evaluateSkewGate(
    ctx context.Context,
    config *UpgradeOrchestrationConfig,
    kubeletCurrentVersion string, // K8s 版本
    apiserverVersion string,       // K8s 版本
    hopIndex int,
    hopPath []string, // openFuyao 版本列表
) (bool, string) {
    if hopIndex < len(hopPath)-1 {
        // 前瞻性检查：解析下一个 hop 的 ReleaseImage，获取 K8s apiserver 版本
        nextHopBundle, err := r.resolveReleaseBundle(ctx, hopPath[hopIndex+1]) // openFuyao 版本
        if err != nil {
            return false, ""
        }
        // 从 ReleaseImage 中按组件名解析 K8s 版本
        nextApiserverVersion := nextHopBundle.GetComponentVersion("kube-apiserver") // K8s 版本
        if nextApiserverVersion == "" {
            return false, ""
        }
        
        // 偏差基于 K8s 版本计算
        nextSkew := computeMinorVersionSkew(nextApiserverVersion, kubeletCurrentVersion)
        maxSkew := config.MaxKubeletSkew
        
        if config.UseVersionSkewFromComponentVersion {
            kubeletCV, err := r.CVStore.GetComponentVersion(ctx, "kubelet", kubeletCurrentVersion)
            if err == nil && kubeletCV.Spec.VersionSkew != nil {
                maxSkew = kubeletCV.Spec.VersionSkew.MaxSkewBehind
            }
        }
        
        if nextSkew >= maxSkew {
            // kubelet 补充目标 = 当前 apiserver 的 K8s 版本
            return true, apiserverVersion // K8s 版本
        }
    } else {
        // 最后一个 hop，kubelet 升级到最终 apiserver 的 K8s 版本
        return true, apiserverVersion // K8s 版本
    }
    
    return false, ""
}
```

#### 9.10.6 executeControlPlaneHop 实现

```go
// executeControlPlaneHop 执行单个 hop 的 K8s 核心组件升级
//
// hopTarget: openFuyao 版本（如 "v2.6.5"）
//            从对应 ReleaseImage 解析 K8s 组件版本（kube-apiserver/kubelet 等）
//            K8s 版本不直接作为参数传入
//
// 返回 HopResult，包含各组件升级后的 K8s 版本
func (r *ClusterVersionReconciler) executeControlPlaneHop(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopTarget string, // openFuyao 版本
    deferredComponents []string,
) (*HopResult, error) {
    // 1. 解析 ReleaseImage（openFuyao 版本 → ReleaseImage bundle）
    //    ReleaseImage 中包含 K8s 组件版本:
    //    kube-apiserver=v1.35, etcd=v3.5.19, kubelet=v1.35, ...
    bundle, err := r.resolveReleaseBundle(ctx, hopTarget)
    if err != nil {
        return nil, err
    }
    
    // 2. 构建 VersionContext
    //    Target 来自 ReleaseImage（K8s 组件版本）
    //    Current 来自当前 ReleaseImage 或 BKECluster.Status
    vc := upgrade.BuildVersionContextForUpgrade(bundle, currentBundle, bkeCluster)
    
    // 3. 延迟升级：将 deferredComponents 的 Target 设为 Current（跳过）
    deferredSet := make(map[string]bool)
    for _, name := range deferredComponents {
        current := vc.GetCurrent(name)
        if current != "" {
            vc.SetTarget(name, current)
            deferredSet[name] = true
        }
    }
    
    // 4. 构建 DAG（etcd→apiserver→cm/scheduler→kube-proxy→kubectl，kubelet 被跳过）
    dag, err := upgrade.BuildDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    if err != nil {
        return nil, err
    }
    
    // 5. 构建 Scheduler 并执行 DAG
    sched := r.buildScheduler(bundle, vc)
    execCtx := r.buildExecutionContext(ctx, bkeCluster, vc)
    
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return nil, fmt.Errorf("execute control plane DAG: %w", err)
    }
    
    // 6. 收集 hop 结果（各组件 K8s 版本）
    result := &HopResult{
        HopTarget:        hopTarget, // openFuyao 版本
        UpgradedVersions: make(map[string]string),  // K8s 版本
        DeferredVersions: make(map[string]string),  // K8s 版本
    }
    
    for name := range vc.Target {
        if deferredSet[name] {
            result.DeferredVersions[name] = vc.GetCurrent(name)
        } else {
            result.UpgradedVersions[name] = vc.GetTarget(name)
        }
    }
    
    return result, nil
}

// HopResult 单个 hop 的执行结果
type HopResult struct {
    // openFuyao 版本（如 "v2.6.5"）
    HopTarget string
    
    // 已升级组件的 K8s 版本映射
    // 如: {"kube-apiserver": "v1.35.0", "etcd": "v3.5.19"}
    UpgradedVersions map[string]string
    
    // 延迟组件的当前 K8s 版本映射
    // 如: {"kubelet": "v1.34.0"}
    DeferredVersions map[string]string
}

func (h *HopResult) GetUpgradedVersion(name string) string {
    return h.UpgradedVersions[name]
}

func (h *HopResult) GetDeferredVersion(name string) string {
    return h.DeferredVersions[name]
}
```

#### 9.10.7 BKEClusterReconciler 适配

```go
// controllers/capbke/bkecluster_controller.go

func (r *BKEClusterReconciler) Reconcile(
    ctx context.Context,
    req ctrl.Request,
) (ctrl.Result, error) {
    // ... 现有逻辑 ...
    
    // 检测 upgrade-ready annotation（值是 openFuyao 版本）
    hopTarget, ok := annotation.HasAnnotation(bkeCluster, annotation.UpgradeReadyAnnotationKey)
    if ok {
        result, err := r.executeUpgradeDAGForHop(ctx, bkeCluster, hopTarget)
        if err != nil {
            bkeCluster.Status.DeclarativeUpgrade.LastError = err.Error()
            _ = r.Status().Patch(ctx, bkeCluster, client.Merge)
            return ctrl.Result{}, err
        }
        
        r.writeHopResult(ctx, bkeCluster, result)
        annotation.RemoveAnnotation(bkeCluster, annotation.UpgradeReadyAnnotationKey)
        _ = r.Update(ctx, bkeCluster)
        return ctrl.Result{}, nil
    }
    
    // ... 其它逻辑 ...
}

// executeUpgradeDAGForHop 执行单个 hop 的升级 DAG
// hopTarget: openFuyao 版本，从 ReleaseImage 解析 K8s 组件版本
func (r *BKEClusterReconciler) executeUpgradeDAGForHop(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopTarget string, // openFuyao 版本
) (*HopResult, error) {
    return r.executeControlPlaneHop(ctx, bkeCluster, hopTarget, []string{"kubelet"})
}
```

#### 9.10.8 状态追踪

| 旧字段 | 新字段（重构后） | 兼容策略 |
|--------|----------------|---------|
| `Status.KubernetesVersion` | `ClusterComponentStatuses["kube-apiserver"].Version` | 过渡期同时更新 |
| `Status.EtcdVersion` | `ClusterComponentStatuses["etcd"].Version` | 过渡期同时更新 |
| `Status.KubeletVersion` | `ClusterComponentStatuses["kubelet"].Version` | 过渡期同时更新 |
| 无 | `ClusterComponentStatuses["kube-controller-manager"].Version` | 新增独立追踪 |
| 无 | `ClusterComponentStatuses["kube-scheduler"].Version` | 新增独立追踪 |
| 无 | `ClusterComponentStatuses["kubectl"].Version` | 新增独立追踪 |
| 无 | `ClusterComponentStatuses["kube-proxy"].Version` | 新增独立追踪 |

```bash
# 查询各组件版本（K8s 版本，独立追踪）
kubectl get bkecluster my-cluster -o jsonpath='{.status.clusterComponentStatuses}'

# 查询 kubelet 延迟升级状态
kubectl get bkecluster my-cluster -o jsonpath='{.status.clusterComponentStatuses.kubelet}'
# 输出: {"phase": "Pending", "version": "v1.34.0"}  ← K8s 版本，延迟升级中
```

#### 9.10.9 完整多 Hop 升级时序

```
完整多 Hop 升级时序 (openFuyao v2.6.0 → v2.7.0, K8s v1.34 → v1.36, 2 hops):

// hopPath = ["v2.6.5", "v2.7.0"]（openFuyao 版本）
// K8s 版本从每个 hop 的 ReleaseImage 中按组件名解析:
//   ReleaseImage v2.6.5 → kube-apiserver=v1.35.0, etcd=v3.5.19, kubelet=v1.35.0, ...
//   ReleaseImage v2.7.0 → kube-apiserver=v1.36.0, etcd=v3.5.20, kubelet=v1.36.0, ...

用户: kubectl patch clusterversion --desired-version v2.7.0

T0: ClusterVersionReconciler.Reconcile()
    ├─ resolveUpgradePath("v2.6.0", "v2.7.0") → ["v2.6.5", "v2.7.0"]  ← openFuyao 版本路径
    ├─ resolveOrchestrationConfig() → DeferredComponents=["kubelet"], MaxSkew=3
    └─ orchestrateFullUpgrade(hopPath=["v2.6.5", "v2.7.0"]):
       │
       ├─ 阶段一: orchestrateK8sCoreMultiHop():
       │  │
       │  ├─ Hop 1 (openFuyao v2.6.5):
       │  │  ├─ setCurrentHopIndex(0)
       │  │  ├─ set upgrade-ready annotation = "v2.6.5"      ← openFuyao 版本
       │  │  │
       │  │  │   BKEClusterReconciler.Reconcile():
       │  │  │   ├─ 读取 upgrade-ready = "v2.6.5"            ← openFuyao 版本
       │  │  │   ├─ executeUpgradeDAGForHop("v2.6.5"):
       │  │  │   │   ├─ 解析 ReleaseImage v2.6.5
       │  │  │   │   │   → K8s 组件版本: apiserver=v1.35, etcd=v3.5.19, kubelet=v1.35, ...
       │  │  │   │   ├─ 构建 VersionContext（kubelet Target=Current=v1.34 跳过）
       │  │  │   │   ├─ 构建 DAG: etcd→apiserver→cm/scheduler→kube-proxy→kubectl
       │  │  │   │   └─ Scheduler.ExecuteDAG()
       │  │  │   │        ├─ etcd: v3.5.18→v3.5.19 StaticPodInstaller
       │  │  │   │        ├─ kube-apiserver: v1.34→v1.35 StaticPodInstaller
       │  │  │   │        ├─ kube-controller-manager: v1.34→v1.35 StaticPodInstaller
       │  │  │   │        ├─ kube-scheduler: v1.34→v1.35 StaticPodInstaller
       │  │  │   │        ├─ kube-proxy: v1.34→v1.35 YamlInstaller
       │  │  │   │        └─ kubectl: v1.34→v1.35 BinaryInstaller
       │  │  │   ├─ writeHopResult(): 更新 ClusterComponentStatuses
       │  │  │   │   → apiserver=v1.35, etcd=v3.5.19, ...（K8s 版本）
       │  │  │   └─ 清除 upgrade-ready annotation
       │  │  │
       │  │  ├─ apiserverVersion = "v1.35.0"               ← K8s 版本（从 ReleaseImage 解析）
       │  │  ├─ kubeletCurrentVersion = "v1.34.0"          ← K8s 版本（延迟，未升级）
       │  │  ├─ currentSkew = computeMinorVersionSkew(v1.35, v1.34) = 1  ← 安全
       │  │  ├─ evaluateSkewGate() → needsCatchup=false
       │  │  ├─ setHopPhase("v2.6.5", Completed)           ← openFuyao 版本
       │  └─ Status().Update()
       │  │
       │  ├─ Hop 2 (openFuyao v2.7.0):
       │  │  ├─ setCurrentHopIndex(1)
       │  │  ├─ set upgrade-ready annotation = "v2.7.0"      ← openFuyao 版本
       │  │  │   BKEClusterReconciler.Reconcile():
       │  │  │   └─ executeUpgradeDAGForHop("v2.7.0")
       │  │  │       └─ 解析 ReleaseImage v2.7.0 → apiserver=v1.36, etcd=v3.5.20, ...
       │  │  │
       │  │  ├─ apiserverVersion = "v1.36.0"               ← K8s 版本
       │  │  ├─ kubeletCurrentVersion = "v1.34.0"          ← K8s 版本（仍延迟）
       │  │  ├─ currentSkew = computeMinorVersionSkew(v1.36, v1.34) = 2  ← 安全
       │  │  ├─ 最后一个 hop → needsCatchup=true
       │  │  │   catchupTargetVersion = "v1.36.0"          ← K8s 版本
       │  │  │
       │  │  ├─ upgradeKubeletCatchupToVersion("v1.34.0", "v1.36.0"):  ← K8s 版本
       │  │  │   ├─ computeIntermediateVersions → ["v1.35.0", "v1.36.0"]
       │  │  │   ├─ executeKubeletUpgrade("v1.35.0"): 逐节点 drain→replace→uncordon
       │  │  │   └─ executeKubeletUpgrade("v1.36.0"): 逐节点 drain→replace→uncordon
       │  │  │
       │  │  └─ setHopPhase("v2.7.0", Completed)           ← openFuyao 版本
       │  │
       │  └─ clearHopProgress()
       │
       ├─ 偏差最终验证: CheckSkew() → 0 偏差 ✅
       │
       └─ 阶段二: executeOtherComponentsUpgrade("v2.7.0"):    ← openFuyao 版本
          └─ set upgrade-ready annotation → BKEClusterReconciler 执行其它组件 DAG

T1: ClusterVersionReconciler 更新:
    ├─ ClusterVersion.Status.CurrentVersion = "v2.7.0"      ← openFuyao 版本
    ├─ ClusterVersion.Status.Phase = Ready
    └─ Event: "UpgradeCompleted"
```

#### 9.10.10 错误恢复场景

| 场景 | 恢复机制 | 说明 |
|------|---------|------|
| **控制面升级失败** | Reconcile 重入，从断点 hop 恢复 | hopIndex annotation 记录 openFuyao 版本进度，已完成的 hop 跳过 |
| **kubelet drain 失败** | Worker 节点 Continue，Master 阻塞 | 失败节点记录在 failedNodes，Reconcile 重试时跳过已升级节点 |
| **kubelet 补充升级中断** | 从中断的中间 K8s 版本继续 | computeIntermediateVersions 基于当前 kubelet 的 K8s 版本重新计算 |
| **ClusterVersionReconciler 重启** | 从 annotation 恢复 hopIndex 和 hopPhase | BKECluster.Status 已持久化各组件 K8s 版本 |
| **BKEClusterReconciler 重启** | 从 upgrade-ready annotation 重新执行当前 hop | DAG 内部幂等（VersionContext.NeedsUpgrade 跳过已完成组件） |
| **节点 BKEAgent 不可用** | 跳过该节点，继续其它节点 | BKEAgent 未就绪的节点在过滤时被排除 |

#### 9.10.11 Feature Gate 矩阵

| Feature Gate | 控制器行为 | ReleaseImage 结构 | 偏差检查 |
|-------------|-----------|------------------|---------|
| 全部 OFF | `orchestrateMultiHopUpgrade`（原版，kubernetes-master inline） | kubernetes-master + etcd + kube-proxy | 外部 K8sSkewConstraints |
| `KubeletBinaryMigration` ON | kubelet 从 kubernetes-master 拆出，`executeControlPlaneHop` 跳过 kubelet | + kubelet(binary) + kubectl(binary) | 外部 K8sSkewConstraints |
| + `StaticPodComponentEnabled` ON | apiserver/cm/scheduler 从 kubernetes-master 拆出 | + apiserver/cm/scheduler(staticpod) | 外部 K8sSkewConstraints |
| + 删除 kubernetes-master | kubernetes-master 不再存在 | 全部独立 | 外部 K8sSkewConstraints |
| + `VersionSkewInComponentVersion` ON | 偏差检查从 ComponentVersion 读取 | 同上 | ComponentVersion.versionSkew（内化） |

## 10. 工作量评估

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **VersionSkewChecker** | 偏差约束定义 + 偏差计算 + 检查逻辑 | 3 |
| **前瞻性偏差检查** | CheckSkewBeforeHop + NeedsKubeletCatchup | 2 |
| **ClusterVersionReconciler 集成** | 多 hop 编排 + 偏差门控 + kubelet 补充升级触发 | 4 |
| **executeControlPlaneHop** | 控制面独立升级（排除 kubelet） | 3 |
| **upgradeKubeletCatchup** | kubelet 逐版本补充升级 + drain/uncordon | 4 |
| **computeIntermediateVersions** | 中间版本计算 + 版本路径规划 | 1 |
| **偏差门控日志 + 事件** | 偏差状态事件 + Prometheus 指标 | 2 |
| **策略配置** | UpgradeOrchestrationConfig + Feature Gate | 1 |
| **ReleaseImage 重构** | K8s 核心组件拆分 + versionSkew 内化 | 5 |
| **控制器多 Hop 重构** | orchestrateK8sCoreMultiHop + 状态追踪重构 | 4 |
| **集成测试** | 3-hop 升级 + 延迟 kubelet + 偏差门控 + 补充升级 E2E | 6 |
| **小计** | - | **35 人天** |

## 11. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **偏差计算错误** | kubelet 无法注册到 apiserver | 低 | 单元测试覆盖版本解析 + 偏差计算 |
| **kubelet 补充升级超时** | 节点长时间 NotReady | 中 | 滚动升级 + 批次大小控制 + 超时回滚 |
| **控制面升级期间 kubelet 版本过低** | 部分 API 不兼容 | 中 | 偏差门控确保不超过 3 版本偏差（K8s v1.25+ 允许） |
| **中间版本不存在** | kubelet 补充升级找不到制品 | 低 | 中间版本必须是已发布版本 |
| **节点 drain 失败** | Pod 无法驱逐 | 中 | 强制 drain + 超时 + Continue 策略 |
| **偏差门误判** | 允许继续或阻塞升级 | 低 | 前瞻性检查 + 实际状态双重验证 |
| **ReleaseImage 重构兼容性** | 新旧 ReleaseImage 格式混用 | 中 | Feature Gate 控制 + 渐进迁移 |
| **versionSkew 内化遗漏** | 部分组件缺少偏差约束声明 | 低 | 迁移验证清单 + 单元测试 |
| **状态追踪兼容性** | 旧字段与新 ClusterComponentStatuses 不一致 | 中 | 过渡期同时更新两个字段 |

---

## 附录

### A. 参考文档

1. [Kubernetes Version Skew Policy](https://kubernetes.io/releases/version-skew-policy/)
2. [KEP-5 声明式升级框架](kep5/kep5.md)
3. [KEP-9 Static Pod 类型设计](kep9-staticpod-upgrade-framework.md)
4. [KEP-13 二进制组件改造](kep13-binary-component-migration-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **版本偏差** | 两个组件之间的小版本号差值，如 apiserver v1.37 vs kubelet v1.34 = 3 偏差 |
| **偏差门控** | 在 hop 之间检查版本偏差是否满足约束，决定是否继续或触发补充升级 |
| **延迟升级** | kubelet 不随控制面同步升级，延迟到偏差达到极限（3）时批量升级 |
| **补充升级** | kubelet 从当前版本逐版本升级到目标版本的过程 |
| **中间版本** | 从当前版本到目标版本之间的 K8s 小版本，如 v1.34→v1.36 的中间版本是 v1.35 |
| **SkewConstraint** | 版本偏差约束定义，声明组件间的最大允许偏差 |

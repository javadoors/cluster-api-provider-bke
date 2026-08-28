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
9. [工作量评估](#9-工作量评估)
10. [风险与缓解](#10-风险与缓解)

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

| 时刻 | etcd | apiserver | cm/scheduler | kubelet | kube-proxy | 偏差状态分析 |
|------|------|-----------|-------------|---------|------------|-------------|
| T0（升级前） | v3.5.18 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ 全部一致 |
| T1（etcd manifest 替换后） | **v3.5.19** | v1.34 | v1.34 | v1.34 | v1.34 | ✅ etcd 版本独立，不影响 K8s 组件偏差 |

**阶段二：`EnsureMasterUpgrade` / `upgradeControlPlane()` 执行期间**

| 时刻 | etcd | apiserver | cm/scheduler | kubelet | kube-proxy | 偏差状态分析 |
|------|------|-----------|-------------|---------|------------|-------------|
| T2（prepareUpgrade 完成后） | v3.5.19 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ 无偏差（仅备份和预拉镜像） |
| T3（apiserver manifest 替换 + Ready 后） | v3.5.19 | **v1.35** | v1.34 | v1.34 | v1.34 | ✅ cm(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 1）；kubelet(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3）；kube-proxy(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3） |
| T4（cm manifest 替换 + Ready 后） | v3.5.19 | v1.35 | **v1.35** | v1.34 | v1.34 | ✅ kubelet(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3）；kube-proxy(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3） |
| T5（scheduler manifest 替换 + Ready 后） | v3.5.19 | v1.35 | v1.35 | v1.34 | v1.34 | ✅ 同 T4（scheduler 已与 cm 一致） |
| T6（kubelet 二进制替换后） | v3.5.19 | v1.35 | v1.35 | **v1.35** | v1.34 | ✅ kube-proxy(1.34) vs apiserver(1.35) = 1 偏差（允许滞后 3） |
| T7（kubectl 二进制安装后） | v3.5.19 | v1.35 | v1.35 | v1.35 | v1.34 | ✅ 同 T6（kubectl 不影响偏差） |

**阶段三：`updateAddonVersions()` 执行期间**

| 时刻 | etcd | apiserver | cm/scheduler | kubelet | kube-proxy | 偏差状态分析 |
|------|------|-----------|-------------|---------|------------|-------------|
| T8（kube-proxy 版本同步后） | v3.5.19 | v1.35 | v1.35 | v1.35 | **v1.35** | ✅ 全部一致，无偏差 |

**分析结论**：

| 偏差规则 | 单节点升级期间是否满足 | 说明 |
|---------|---------------------|------|
| kubelet ≤ apiserver（允许滞后 3） | ✅ 满足 | kubelet 在 apiserver 之后升级（T3→T6），最大偏差 1（远小于允许的 3） |
| kube-proxy ≤ apiserver（允许滞后 3） | ✅ 满足 | kube-proxy 在 apiserver 之后由 `updateAddonVersions()` 同步升级，最大偏差 1（远小于允许的 3） |
| cm/scheduler ≤ apiserver（允许滞后 1） | ✅ 满足 | kubeadm 在 apiserver 后逐个升级 cm/scheduler（T3→T4→T5），最大偏差 1（正好等于允许的极限 1） |
| etcd 与 apiserver 配套 | ✅ 满足 | etcd 在独立的 `EnsureEtcdUpgrade` Phase 中先升级，`EnsureMasterUpgrade` 执行时 etcd 已就绪 |

#### 2.8.4 多节点 Master 集群的偏差状态

对于多 Master 节点集群，`EnsureEtcdUpgrade` 和 `EnsureMasterUpgrade` 分别逐节点滚动升级（阻塞式），各节点版本如下：

| 阶段 | node-1 apiserver | node-2 apiserver | node-1 kubelet | node-2 kubelet | 偏差状态分析 |
|------|-----------------|-----------------|----------------|----------------|-------------|
| 升级前 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ 一致 |
| node-1 EnsureEtcdUpgrade 完成 | v1.34 | v1.34 | v1.34 | v1.34 | ✅ etcd 升级不影响 K8s 组件版本 |
| node-1 EnsureMasterUpgrade 完成 | **v1.35** | v1.34 | **v1.35** | v1.34 | ✅ HA 中 apiserver 实例间偏差 1（允许 1）；node-2 的 kubelet(1.34) vs apiserver(1.34) = 0 偏差（自身无偏差） |
| node-2 EnsureEtcdUpgrade 完成 | v1.35 | v1.34 | v1.35 | v1.34 | ✅ 同上 |
| node-2 EnsureMasterUpgrade 完成 | v1.35 | **v1.35** | v1.35 | **v1.35** | ✅ 全部一致 |

**分析结论**：多节点滚动升级期间，**单个节点内的偏差**由 `upgradeControlPlane()` 内部逐组件升级管理（见 2.8.3），**跨节点偏差**通过逐节点升级保证安全。在 node-1 完成而 node-2 未完成时，集群存在两个版本的 apiserver（HA 场景下可接受，官方允许 apiserver 实例间最多偏差 1 个小版本）。

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

**将控制面升级和 kubelet/kubectl 升级拆分为独立的 DAG 节点，通过版本偏差约束动态控制执行顺序。**

```
传统方式（BKE upgradeControlPlane 黑盒）:
  Hop 1: EnsureMasterUpgrade → apiserver + cm + scheduler + kubelet + kubectl 全部升级
  Hop 2: EnsureMasterUpgrade → apiserver + cm + scheduler + kubelet + kubectl 全部升级

本方案（分离升级 + 偏差门控）:
  Hop 1: 控制面升级 → apiserver + cm + scheduler + kube-proxy + kubectl（不含 kubelet）
  偏差门 1: kubelet(v1.34) vs apiserver(v1.35) → 1 偏差 → ✅ 通过（允许 3）
  Hop 2: 控制面升级 → apiserver + cm + scheduler + kube-proxy + kubectl（不含 kubelet）
  偏差门 2: kubelet(v1.34) vs apiserver(v1.36) → 2 偏差 → ✅ 安全（允许 3）
  Hop 3: 控制面升级 → apiserver + cm + scheduler + kube-proxy + kubectl（不含 kubelet）
  偏差门 3: kubelet(v1.34) vs apiserver(v1.37) → 3 偏差 → ⚠️ 达到极限，必须升级 kubelet
  Kubelet 补充升级: v1.34→v1.35→v1.36→v1.37（逐节点 drain → replace → uncordon）
  最终验证: 0 偏差 → ✅ 升级完成
```

**为什么不延迟 kubectl？**

kubectl 的偏差规则允许与 apiserver 相差 ±1 个小版本（比 kubelet 的 3 个小版本更严格），延迟 kubectl 会很快触及偏差极限。且 kubectl 是命令行工具，升级无需 drain 节点（仅替换二进制），不阻塞控制面升级，因此 kubectl 随控制面同步升级。

### 4.2 单 hop 内的升级顺序

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

### 4.3 多 hop 间的偏差门控

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

### 4.4 完整多 hop 升级流程

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

### 4.5 kubelet 延迟升级策略

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

## 9. 工作量评估

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
| **集成测试** | 3-hop 升级 + 延迟 kubelet + 偏差门控 + 补充升级 E2E | 6 |
| **小计** | - | **26 人天** |

## 10. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **偏差计算错误** | kubelet 无法注册到 apiserver | 低 | 单元测试覆盖版本解析 + 偏差计算 |
| **kubelet 补充升级超时** | 节点长时间 NotReady | 中 | 滚动升级 + 批次大小控制 + 超时回滚 |
| **控制面升级期间 kubelet 版本过低** | 部分 API 不兼容 | 中 | 偏差门控确保不超过 3 版本偏差（K8s v1.25+ 允许） |
| **中间版本不存在** | kubelet 补充升级找不到制品 | 低 | 中间版本必须是已发布版本 |
| **节点 drain 失败** | Pod 无法驱逐 | 中 | 强制 drain + 超时 + Continue 策略 |
| **偏差门误判** | 允许继续或阻塞升级 | 低 | 前瞻性检查 + 实际状态双重验证 |

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

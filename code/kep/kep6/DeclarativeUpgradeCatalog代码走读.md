# DeclarativeUpgradeCatalog 代码走读

## 文件位置

`pkg/upgrade/catalog.go`

## 核心作用

`DeclarativeUpgradeCatalog` 是声明式升级 DAG 的**组件目录表**，定义了 ReleaseImage 升级时所有组件的执行规格。

其核心作用是：**将旧版 PhaseFlow 硬编码的升级 Phase 映射为声明式 DAG 可消费的组件清单**。

## 数据结构

### UpgradeComponentSpec

每条 `UpgradeComponentSpec` 定义一个升级组件的执行规格：

| 字段 | 含义 | 示例 |
|------|------|------|
| `Name` | DAG 节点名 / VersionContext key | `etcd`, `bkeagent`, `coredns` |
| `Version` | 组件 manifest 版本 | `v1.0.0` |
| `Mode` | 执行方式：`inline`(Go Phase) / `manifest`(YAML 清单) | inline 走 ComponentFactory，manifest 走 YamlInstaller |
| `ManifestPath` | manifest 模式的 YAML 路径 | `provider/v1.0.0/component.yaml` |
| `LegacyPhase` | 对应的旧 PhaseFlow Phase 名 | `EnsureEtcdUpgrade` |
| `InlineHandler` | ComponentFactory 注册的 handler key | `EnsureEtcdUpgrade` |

### UpgradeExecutionMode

```go
type UpgradeExecutionMode string

const (
    UpgradeExecutionManifest UpgradeExecutionMode = "manifest"
    UpgradeExecutionInline   UpgradeExecutionMode = "inline"
)
```

- `inline`：通过 ComponentFactory 注册的 Go Phase Handler 执行（复用现有 Phase 代码）
- `manifest`：通过 YamlInstaller 加载并 Apply YAML 清单文件执行

## 组件清单

当前 `DeclarativeUpgradeCatalog` 包含 9 个组件，分为两种执行模式：

### inline 模式（6 个，Go 代码执行）

| 组件名 | Inline Handler | 旧 Phase 名 | 说明 |
|--------|---------------|------------|------|
| `pre-upgrade-resources` | `EnsurePreUpgradeResources` | `EnsurePreUpgradeResources` | 升级前资源预创建（CRD/ConfigMap/Secret） |
| `bkeagent` | `EnsureAgentUpgrade` | `EnsureAgentUpgrade` | BKE Agent 二进制升级 |
| `etcd` | `EnsureEtcdUpgrade` | `EnsureEtcdUpgrade` | etcd 集群升级 |
| `kubernetes-master` | `EnsureMasterUpgrade` | `EnsureMasterUpgrade` | Master 控制面组件升级 |
| `kubernetes-worker` | `EnsureWorkerUpgrade` | `EnsureWorkerUpgrade` | Worker 节点 kubelet 升级 |
| `containerd` | `EnsureContainerdUpgrade` | `EnsureContainerdUpgrade` | 容器运行时升级 |

### manifest 模式（3 个，YAML 清单执行）

| 组件名 | ManifestPath | 旧 Phase 名 | 说明 |
|--------|-------------|------------|------|
| `provider` | `provider/v1.0.0/component.yaml` | `EnsureProviderSelfUpgrade` | Provider 自身升级 |
| `kube-proxy` | `kube-proxy/v1.0.0/component.yaml` | - | kube-proxy DaemonSet 升级 |
| `coredns` | `coredns/v1.0.0/component.yaml` | `EnsureComponentUpgrade` | CoreDNS Deployment 升级 |

## DAG 执行顺序

DAG 构建器遍历 `DeclarativeUpgradeCatalog`，为每个组件创建 `ComponentNode`，按依赖关系拓扑排序后逐个执行：

```
pre-upgrade-resources (inline)
    ↓
provider (manifest) + bkeagent (inline)     ← 并行
    ↓
containerd (inline)
    ↓
etcd (inline)
    ↓
kubernetes-master (inline)
    ↓
kubernetes-worker (inline)
    ↓
kube-proxy (manifest) + coredns (manifest)  ← 并行
```

## 执行路径

```
DAG Scheduler 遍历 DeclarativeUpgradeCatalog
    │
    ├─ Mode=inline
    │   └─ ComponentFactory.Resolve(handler, version)
    │       └─ Phase.NeedExecute() → Phase.Execute()
    │           (复用现有 PhaseFlow Phase 代码)
    │
    └─ Mode=manifest
        └─ ManifestStore.GetComponentManifests(name, version)
            └─ YamlInstaller.Apply()
                (加载 YAML 清单 → SSA Apply)
```

## 辅助函数

### ManifestComponentManifestPath

```go
func ManifestComponentManifestPath(componentName, version string) string {
    return componentName + "/" + version + "/component.yaml"
}
```

拼接 manifest 模式组件的 YAML 文件相对路径，如 `provider/v1.0.0/component.yaml`。

### InlineUpgradeHandlers

```go
func InlineUpgradeHandlers() []string
```

从 `DeclarativeUpgradeCatalog` 中提取所有 `Mode=inline` 的 handler 名称列表，供 ComponentFactory 批量注册时使用。

## 常量定义

```go
const ComponentManifestVersion = "v1.0.0"
```

所有组件的 manifest 版本统一为 `v1.0.0`（manifest 内容本身可随版本更新，此版本号是 manifest 结构格式的版本）。

```go
const (
    InlineHandlerPreUpgradeResources = "EnsurePreUpgradeResources"
    InlineHandlerEtcdUpgrade         = "EnsureEtcdUpgrade"
    InlineHandlerMasterUpgrade       = "EnsureMasterUpgrade"
    InlineHandlerWorkerUpgrade       = "EnsureWorkerUpgrade"
    InlineHandlerContainerdUpgrade   = "EnsureContainerdUpgrade"
    InlineHandlerAgentUpgrade        = "EnsureAgentUpgrade"
)
```

Inline handler 名称与 `phaseframe.Phase.Name()` 和 `ComponentVersion.spec.inline.handler` 保持一致，确保三方对齐。

## kubernetes-master 组件规格与实现

### Catalog 定义

```go
{
    Name:          ComponentKubernetesMaster,    // "kubernetes-master"
    Version:       ComponentManifestVersion,     // "v1.0.0"
    Mode:          UpgradeExecutionInline,       // inline 模式
    LegacyPhase:   InlineHandlerMasterUpgrade,   // "EnsureMasterUpgrade"
    InlineHandler: InlineHandlerMasterUpgrade,   // "EnsureMasterUpgrade"
}
```

### 注册路径

```
catalog.go: DeclarativeUpgradeCatalog[kubernetes-master]
    → componentfactory/registry.go:29: registerInlineHandler(InlineHandlerMasterUpgrade)
        → phases.NewEnsureMasterUpgrade
            → ComponentFactory.Resolve("EnsureMasterUpgrade", "v1.0.0", phaseCtx)
                → Phase.NeedExecute() → Phase.Execute()
```

### Phase 实现

**文件**：`pkg/phaseframe/phases/ensure_master_upgrade.go`（720 行）

#### 结构体定义

```go
type EnsureMasterUpgrade struct {
    phaseframe.BasePhase
    // mockTargetClient 允许测试注入 fake clientset，生产环境为 nil
    mockTargetClient kubernetes.Interface
}
```

#### VersionContext 版本决策链

Master 升级的版本判断通过 `VersionContext` 实现，优先级链如下：

```go
// currentKubernetesVersion() — 当前运行版本 (ensure_master_upgrade.go:100-117)
vc.GetCurrent("kubernetes-master")   // 优先：VersionContext 中 master 的当前版本
vc.GetCurrent("kubernetes-worker")   // 回退 1：worker 的当前版本
vc.GetCurrent("kubernetes")          // 回退 2：通用 kubernetes key
BKECluster.Status.KubernetesVersion  // 最终回退：集群状态字段

// desiredKubernetesVersion() — 目标升级版本 (ensure_master_upgrade.go:119-133)
vc.GetTarget("kubernetes-master")    // 优先：VersionContext 中 master 的目标版本
vc.GetTarget("kubernetes-worker")    // 回退 1
vc.GetTarget("kubernetes")           // 回退 2
BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion  // 最终回退（已废弃）
```

**设计原因**：master 和 worker 共享同一个 K8s 版本，但 ReleaseImage 可能将版本拆分到不同组件 key。优先级链确保无论 ReleaseImage 如何定义，都能正确解析版本。

#### NeedExecute() 逻辑

```go
func (e *EnsureMasterUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. 基础守卫：跳过删除/暂停/DryRun/Failed 状态
    if !e.BasePhase.DefaultNeedExecute(old, new) { return false }

    // 2. 集群不健康则跳过
    if new.Status.ClusterStatus == ClusterUnhealthy || ClusterUnknown { return false }

    // 3. VersionContext 版本比对：current != target → 需要升级
    if !e.NeedExecuteWithVersionContext("kubernetes-master", old, new,
        e.isKubernetesMasterNeedUpgrade) { return false }

    // 4. 检查 CAPI ControlPlaneInitialized 条件为 True
    bkeNodes, ok := fetchBKENodesIfCPInitialized(e.Ctx, new)
    if !ok { return false }

    // 5. 获取需要升级的 master 节点（semver 比较 status vs spec 版本）
    nodes := phaseutil.GetNeedUpgradeMasterNodesWithBKENodes(new, bkeNodes)
    if nodes.Length() == 0 { return false }

    e.SetStatus(PhaseWaiting)
    return true
}
```

#### Execute() 执行流程

```
Execute()
  │
  ├─ 1. 设置 DeployActionK8sUpgrade 注解（Boc 平台所需）
  │     └─ mergecluster.SyncStatusUntilComplete()
  │
  └─ 2. reconcileMasterUpgrade()
       │
       ├─ 解析 targetVersion / currentVersion
       │
       ├─ syncLegacyTargetKubernetesVersion(target)
       │   └─ 将 VersionContext 的 target 写回 BKECluster.Spec（兼容旧 kubeadm 路径）
       │
       └─ rolloutUpgrade()
            │
            ├─ getNeedUpgradeNodes()
            │   ├─ 获取 BKENodes
            │   ├─ 过滤需要升级的 master 节点
            │   └─ 跳过 BKEAgent 未就绪的节点 (NodeAgentReadyFlag)
            │
            ├─ etcd 备份准备
            │   ├─ 选取 etcdNodes[0] 作为备份节点
            │   └─ ensureEtcdAdvertiseClientUrlsAnnotation()
            │       └─ 为每个 etcd static pod 补充 advertise-client-urls 注解
            │
            ├─ upgradeMasterNodesWithParams() — 逐节点滚动升级 (阻塞式)
            │   │
            │   └─ for each master node:
            │       ├─ 获取远端 K8s Node 对象
            │       ├─ 若 kubeletVersion 已等于目标版本 → 跳过
            │       ├─ 标记 BKENode 状态 = NodeUpgrading
            │       │
            │       ├─ upgradeNode()
            │       │   ├─ executeNodeUpgradeWithParams()
            │       │   │   ├─ 构建 command.Upgrade{Phase: UpgradeControlPlane}
            │       │   │   │   └─ BKEAgent 执行: kubeadm upgrade apply
            │       │   │   │       (更新 kube-apiserver/cm/scheduler static pod manifest)
            │       │   │   ├─ 若为 etcd 备份节点: BackUpEtcd = true
            │       │   │   ├─ upgrade.New() → 创建 Command CR
            │       │   │   └─ upgrade.Wait() → 轮询 Command 状态 (5min, 2s)
            │       │   │
            │       │   └─ waitForNodeHealthCheckWithParams()
            │       │       └─ 轮询远端 Node kubeletVersion + NodeHealthCheck
            │       │           (2s 间隔, 5min 超时)
            │       │
            │       ├─ 失败 → 标记 NodeUpgradeFailed + 立即返回错误 (FailStop)
            │       └─ 成功 → 标记 NodeNotReady "Upgrading success"
            │
            └─ 升级后处理
                ├─ 更新 bkeCluster.Status.KubernetesVersion = desiredKubernetesVersion()
                └─ updateAddonVersions() — 同步 kubectl addon 到 v1.25
```

#### BKEAgent 命令机制

Master 升级**不直接 SSH**，而是通过 BKEAgent Command CR 派发任务：

```go
// command/upgrade.go:52-87
func (u *Upgrade) New() error {
    commandName := fmt.Sprintf("upgrade-node-%s-%d", u.Node.IP, time.Now().Unix())
    execCommand := []string{
        "Kubeadm",
        fmt.Sprintf("phase=%s", u.Phase),              // UpgradeControlPlane
        fmt.Sprintf("bkeConfig=%s:%s", u.NameSpace, u.BKEConfig),
        fmt.Sprintf("clusterType=%s", u.ClusterFrom),   // bke
        fmt.Sprintf("backUpEtcd=%t", u.BackUpEtcd),     // true/false
    }
    // 创建 agentv1beta1.Command CR，NodeSelector 指向目标节点 IP
    // BKEAgent 在目标节点上监听到 Command CR → 执行 kubeadm upgrade apply
    return u.newCommand(commandName, BKEClusterLabel, commandSpec)
}
```

**执行链路**：

```
Controller 创建 Command CR
    → BKEAgent (目标节点) 监听到 Command
        → 执行 kubeadm upgrade apply
            → 更新 /etc/kubernetes/manifests/kube-apiserver.yaml
            → 更新 /etc/kubernetes/manifests/kube-controller-manager.yaml
            → 更新 /etc/kubernetes/manifests/kube-scheduler.yaml
            → Kubelet 检测文件变化 → 重建 Static Pod
        → 更新 kubelet 配置和二进制
        → 报告 Command 执行结果
    → Controller 轮询 Command 状态 (upgrade.Wait())
```

#### 失败策略

**Master 升级为 FailStop（阻塞式）**：任一节点升级失败立即终止整个 Phase，返回错误。不继续升级后续节点。

```go
if err := e.upgradeNode(...); err != nil {
    // master node block until upgrade success
    nodeFetcher.SetNodeStateWithMessageForCluster(ctx, bkeCluster, node.IP,
        bkev1beta1.NodeUpgradeFailed, err.Error())
    return fmt.Errorf("upgrade node %q failed: %w", phaseutil.NodeInfo(node), err)
}
```

**原因**：master 是控制面核心，部分升级失败会导致集群不可用，必须立即停止并等待人工介入。

---

## kubernetes-worker 组件规格与实现

### Catalog 定义

```go
{
    Name:          ComponentKubernetesWorker,    // "kubernetes-worker"
    Version:       ComponentManifestVersion,     // "v1.0.0"
    Mode:          UpgradeExecutionInline,       // inline 模式
    LegacyPhase:   InlineHandlerWorkerUpgrade,   // "EnsureWorkerUpgrade"
    InlineHandler: InlineHandlerWorkerUpgrade,   // "EnsureWorkerUpgrade"
}
```

### 注册路径

```
catalog.go: DeclarativeUpgradeCatalog[kubernetes-worker]
    → componentfactory/registry.go:31: registerInlineHandler(InlineHandlerWorkerUpgrade)
        → phases.NewEnsureWorkerUpgrade
            → ComponentFactory.Resolve("EnsureWorkerUpgrade", "v1.0.0", phaseCtx)
                → Phase.NeedExecute() → Phase.Execute()
```

### Phase 实现

**文件**：`pkg/phaseframe/phases/ensure_worker_upgrade.go`（498 行）

该文件还定义了**共享的** `CreateUpgradeCommandParams` 结构体和 `createUpgradeCommand` 工厂函数，master 和 worker 两个 Phase 共用。

#### 结构体定义

```go
type EnsureWorkerUpgrade struct {
    phaseframe.BasePhase
}
```

#### VersionContext 版本决策链

Worker 升级的版本判断与 master 对称，优先级链**主 key 为 `kubernetes-worker`**：

```go
// currentKubernetesVersion() — 当前运行版本 (ensure_worker_upgrade.go:117-134)
vc.GetCurrent("kubernetes-worker")   // 优先
vc.GetCurrent("kubernetes-master")   // 回退 1
vc.GetCurrent("kubernetes")          // 回退 2
BKECluster.Status.KubernetesVersion  // 最终回退

// desiredKubernetesVersion() — 目标升级版本 (ensure_worker_upgrade.go:136-150)
vc.GetTarget("kubernetes-worker")    // 优先
vc.GetTarget("kubernetes-master")    // 回退 1
vc.GetTarget("kubernetes")           // 回退 2
BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion  // 最终回退（已废弃）
```

#### NeedExecute() 逻辑

```go
func (e *EnsureWorkerUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. 基础守卫
    if !e.BasePhase.DefaultNeedExecute(old, new) { return false }

    // 2. 集群不健康则跳过
    if new.Status.ClusterStatus == ClusterUnhealthy || ClusterUnknown { return false }

    // 3. VersionContext 版本比对（主 key = kubernetes-worker）
    if !e.NeedExecuteWithVersionContext("kubernetes-worker", old, new,
        e.isKubernetesWorkerNeedUpgrade) { return false }

    // 4. 检查 CAPI ControlPlaneInitialized
    bkeNodes, ok := fetchBKENodesIfCPInitialized(e.Ctx, new)
    if !ok { return false }

    // 5. 获取需要升级的 worker 节点
    nodes := phaseutil.GetNeedUpgradeWorkerNodesWithBKENodes(new, bkeNodes)
    if nodes.Length() == 0 { return false }

    e.SetStatus(PhaseWaiting)
    return true
}
```

#### Execute() 执行流程

```
Execute()
  │
  ├─ 1. 设置 DeployActionK8sUpgrade 注解
  │
  └─ 2. reconcileWorkerUpgrade()
       │
       ├─ 解析 targetVersion / currentVersion
       ├─ syncLegacyTargetKubernetesVersion(target)
       │
       └─ rolloutUpgrade()
            │
            ├─ prepareUpgradeNodes()
            │   ├─ 获取 BKENodes
            │   ├─ 过滤需要升级的 worker 节点
            │   └─ 跳过 BKEAgent 未就绪的节点
            │
            ├─ 创建 Drainer (kubedrain.Helper)
            │   └─ 注意：Drainer 已创建但当前未实际调用
            │
            ├─ processNodeUpgrade() — 逐节点滚动升级
            │   │
            │   └─ for each worker node:
            │       ├─ 获取远端 K8s Node 对象
            │       ├─ 若 kubeletVersion 已等于目标版本 → 跳过
            │       ├─ 标记 BKENode 状态 = NodeUpgrading
            │       │
            │       ├─ upgradeNode()
            │       │   ├─ executeNodeUpgrade()
            │       │   │   ├─ 构建 command.Upgrade{Phase: UpgradeWorker}
            │       │   │   │   └─ BKEAgent 执行: kubeadm upgrade node
            │       │   │   │       (更新 kubelet 二进制 + kubelet.conf)
            │       │   │   ├─ BackUpEtcd = false (worker 不备份 etcd)
            │       │   │   ├─ upgrade.New() → 创建 Command CR
            │       │   │   └─ upgrade.Wait() → 轮询 Command 状态 (5min, 2s)
            │       │   │
            │       │   └─ waitForNodeHealth()
            │       │       └─ waitForWorkerNodeHealthCheck()
            │       │           └─ 轮询 Node kubeletVersion + NodeHealthCheck
            │       │               (2s 间隔, 5min 超时)
            │       │
            │       ├─ 失败 → 收集到 failedUpgradeNodes + 继续下一个节点 (Continue)
            │       └─ 成功 → 标记 NodeNotReady "Upgrading success"
            │
            └─ 汇总结果
                ├─ failedUpgradeNodes 为空 → "upgrade all worker success"
                └─ 不为空 → 返回错误，列出失败节点（允许后续重试）
```

#### BKEAgent 命令机制

Worker 升级同样通过 BKEAgent Command CR 派发，但 Phase 为 `UpgradeWorker`：

```go
// ensure_worker_upgrade.go:405-436
func (e *EnsureWorkerUpgrade) executeNodeUpgrade(params ExecuteNodeUpgradeParams) error {
    createParams := CreateUpgradeCommandParams{
        // ...
        Phase: bkev1beta1.UpgradeWorker,  // 区别于 master 的 UpgradeControlPlane
    }
    upgrade := createUpgradeCommand(createParams)
    upgrade.BackUpEtcd = false  // worker 不备份 etcd
    // ...
}
```

**BKEAgent 执行内容**：

```
BKEAgent 收到 Command (Phase=UpgradeWorker)
    → 执行 kubeadm upgrade node
        → 下载新版本 kubelet 二进制
        → 更新 /etc/kubernetes/kubelet.conf
        → 更新 /etc/kubernetes/pki 证书（如需要）
        → 重启 kubelet 服务
    → 报告 Command 执行结果
```

#### 失败策略

**Worker 升级为 Continue（继续式）**：单个节点升级失败不终止整个 Phase，收集失败节点后继续升级下一个节点，最后统一报告。

```go
if err := e.upgradeNode(node, remoteNode, params.Drainer); err != nil {
    failedUpgradeNodes = append(failedUpgradeNodes, phaseutil.NodeInfo(node))
    params.Log.Warn("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
    nodeFetcher.SetNodeStateWithMessage(..., bkev1beta1.NodeUpgradeFailed, err.Error())
    continue  // 继续下一个节点
}
// ...
if len(failedUpgradeNodes) == 0 {
    return "upgrade all worker success"
} else {
    return errors.Errorf("some nodes upgrade failed, will retry later: %v", failedUpgradeNodes)
}
```

**原因**：worker 节点相对独立，单节点失败不影响其他节点和集群可用性，可以继续升级剩余节点后统一重试失败节点。

---

## Master 与 Worker 对比

| 维度 | EnsureMasterUpgrade | EnsureWorkerUpgrade |
|------|---------------------|---------------------|
| **文件** | `ensure_master_upgrade.go` (720 行) | `ensure_worker_upgrade.go` (498 行) |
| **结构体** | `EnsureMasterUpgrade{BasePhase, mockTargetClient}` | `EnsureWorkerUpgrade{BasePhase}` |
| **VersionContext 主 key** | `kubernetes-master` | `kubernetes-worker` |
| **BKEAgent 命令 Phase** | `UpgradeControlPlane` (`kubeadm upgrade apply`) | `UpgradeWorker` (`kubeadm upgrade node`) |
| **etcd 备份** | ✅ `BackUpEtcd=true`（选取一个 etcd 节点） | ❌ `BackUpEtcd=false` |
| **etcd 注解处理** | ✅ `ensureEtcdAdvertiseClientUrlsAnnotation` | ❌ 无 |
| **Drain/Cordon** | ❌ 不执行 | Drainer 已创建但**未实际调用** |
| **SSH** | ❌ 不直接 SSH（通过 BKEAgent） | ❌ 不直接 SSH（通过 BKEAgent） |
| **失败策略** | **FailStop** — 首个失败立即终止 | **Continue** — 收集失败节点，继续升级 |
| **升级后处理** | 更新 `Status.KubernetesVersion` + 同步 addon | 无（版本状态由 master 更新） |
| **健康检查** | `waitForNodeHealthCheckWithParams` (2s/5min) | `waitForNodeHealth` → `waitForWorkerNodeHealthCheck` (2s/5min) |
| **kubectl addon** | 更新 kubectl addon 到 v1.25 | 无 |

### 升级执行顺序

DAG 依赖确保 master 先于 worker 升级：

```
etcd (inline)
    ↓
kubernetes-master (inline)     ← EnsureMasterUpgrade
    │   ├─ kubeadm upgrade apply (逐节点, FailStop)
    │   ├─ etcd 备份
    │   └─ 更新 Status.KubernetesVersion
    ↓
kubernetes-worker (inline)     ← EnsureWorkerUpgrade
    │   ├─ kubeadm upgrade node (逐节点, Continue)
    │   └─ 失败节点可后续重试
    ↓
kube-proxy (manifest) + coredns (manifest)
```

### 关键设计决策

1. **为什么 master 是 FailStop 而 worker 是 Continue？**
   - Master 是控制面核心，部分升级失败会导致 API Server 不可用或 etcd quorum 丢失，必须立即停止
   - Worker 相对独立，单节点失败不影响集群控制面和其他 worker，可以继续升级后统一重试

2. **为什么通过 BKEAgent 而非直接 SSH？**
   - BKEAgent 提供了 Command CR 的声明式任务派发机制，天然支持状态追踪和重试
   - `upgrade.Wait()` 轮询 Command CR 状态，无需维护 SSH 连接
   - BKEAgent 内部调用 `kubeadm`，复用 kubeadm 的成熟升级逻辑

3. **为什么 Drainer 创建但未使用？**
   - `kubedrain.Helper` 在 `rolloutUpgrade` 中构造并传入 `processNodeUpgrade`，但 `upgradeNode` 方法接收了 `drainer` 参数却未调用
   - 当前 worker 升级依赖 `kubeadm upgrade node` 内部的节点准备逻辑
   - Drainer 是为未来 drain → upgrade → uncordon 流程预留的接口

4. **为什么 VersionContext 有多级回退？**
   - ReleaseImage 可能将 K8s 版本拆分为 `kubernetes-master` 和 `kubernetes-worker` 两个组件
   - 也可能合并为单个 `kubernetes` 组件
   - 多级回退确保无论 ReleaseImage 如何定义，都能正确解析版本

---

## 设计意义

1. **解耦**：将升级组件定义从 BKECluster Controller 硬编码中提取为独立目录表，DAG 构建器只需遍历此表
2. **迁移桥梁**：`LegacyPhase` 字段记录旧 PhaseFlow Phase 名，支持声明式升级与旧版 PhaseFlow 并行运行和渐进式迁移
3. **统一入口**：inline 和 manifest 两种执行模式统一在同一数据结构中，DAG Scheduler 按类型分发
4. **可扩展**：新增组件只需在 catalog 中添加一条 `UpgradeComponentSpec`，无需修改 DAG 构建逻辑

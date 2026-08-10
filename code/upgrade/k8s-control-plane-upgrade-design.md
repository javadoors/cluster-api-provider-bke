# BKE 升级 K8s 控制面组件完整设计方案

> 基于 cluster-api-provider-bke 代码库分析生成

## 一、架构总览

```
┌──────────────────────────────────────────────────────────────────────┐
│                        升级系统架构                                    │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐ │
│  │ ClusterVersion  │───>│  UpgradePath     │───>│ ReleaseImage    │ │
│  │ Controller      │    │  Service         │    │ (OCI Bundle)    │ │
│  └────────┬────────┘    └──────────────────┘    └────────┬────────┘ │
│           │                                              │          │
│           │ markUpgradeReady                             │          │
│           v                                              v          │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                    BKECluster Controller                       │ │
│  │  ┌─────────────────┐    ┌──────────────────┐                  │ │
│  │  │ shouldUse       │───>│ executeUpgrade   │                  │ │
│  │  │ Declarative     │    │ DAG()            │                  │ │
│  │  │ Upgrade()       │    └────────┬─────────┘                  │ │
│  │  └─────────────────┘             │                            │ │
│  └──────────────────────────────────┼────────────────────────────┘ │
│                                     │                              │
│           ┌─────────────────────────┼─────────────────────────┐    │
│           │                         │                         │    │
│           v                         v                         v    │
│  ┌────────────────┐    ┌────────────────┐    ┌────────────────┐    │
│  │ DAG Scheduler  │    │ Component      │    │ Phase          │    │
│  │ (拓扑排序+并行) │    │ Factory        │    │ Runner         │    │
│  └────────┬───────┘    └────────────────┘    └────────┬───────┘    │
│           │                                           │            │
│           └───────────────────────────────────────────┘            │
│                                     │                              │
│           ┌─────────────────────────┼─────────────────────────┐    │
│           │                         │                         │    │
│           v                         v                         v    │
│  ┌────────────────┐    ┌────────────────┐    ┌────────────────┐    │
│  │ EnsureEtcd     │    │ EnsureMaster   │    │ EnsureWorker   │    │
│  │ Upgrade        │    │ Upgrade        │    │ Upgrade        │    │
│  └────────────────┘    └────────────────┘    └────────────────┘    │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

## 二、核心 CRD 定义

### 2.1 ClusterVersion CRD

**文件**: `api/v1alpha1/clusterversion_types.go`

```
ClusterVersion
├── Spec.DesiredVersion (string) — 目标 openFuyao 版本
└── Status
    ├── CurrentVersion (string) — 当前运行版本
    ├── Phase (ClusterVersionPhase) — 生命周期阶段
    ├── UpgradeHistory ([]ClusterUpgradeRecord) — 升级历史记录
    └── Conditions ([]ClusterVersionCondition) — 细粒度状态条件
```

**Phase 状态枚举**:
- `Pending` / `Installing` / `Installed` / `Ready` — 安装流程
- `PreChecking` / `Upgrading` / `Upgraded` — 升级流程
- `Blocked` / `PreCheckFailed` / `Failed` — 异常状态

### 2.2 UpgradePath CRD

**文件**: `api/v1alpha1/upgradepath_types.go`

```
UpgradePath
├── Spec
│   ├── Paths []UpgradePathRule — 有向升级边
│   │   ├── From / To (string) — 版本对
│   │   ├── Blocked / Deprecated (bool)
│   │   ├── PreCheck / PostCheck ([]CheckStep) — 升级前后校验步骤
│   │   └── Notes (string)
│   └── Versions []VersionEntry — 版本元数据
└── Status
    ├── Phase (Active/Blocked/Invalid)
    ├── PathCount / LastDigest / LastCheckedAt
    └── Conditions
```

### 2.3 ReleaseImage CRD

**文件**: `api/v1alpha1/releaseimage_types.go`

```
ReleaseImage
├── Spec
│   ├── Version / Digest / VerifySignature / SignatureKey
│   ├── Install.Components []ReleaseImageInstallComponent
│   └── Upgrade.Components []ReleaseImageUpgradeComponent
│       ├── Name / Version
│       └── Inline (*ReleaseImageUpgradeInline)
│           ├── Handler (string) — 处理器名称
│           └── Version (string)
└── Status
    ├── Phase (Valid/Invalid/ManifestMissing/CompatibilityFailed)
    ├── ComponentCount / Components / ValidatedAt
    └── Digest / Source / CacheFallback / Message
```

### 2.4 ComponentVersion CRD

**文件**: `api/v1alpha1/componentversion_types.go`

```
ComponentVersion
├── Spec
│   ├── Name / Type (yaml/helm/inline/binary) / Version
│   ├── Inline (*InlineSpec) — 内联处理器配置
│   ├── SubComponents / Compatibility / Dependencies
│   ├── UpgradeStrategy (Mode/BatchSize/Timeout/FailurePolicy)
│   └── Resources []ResourceSpec — 升级前置资源 (ConfigMap/Secret/Manifest)
└── Status (Phase/Conditions)
```

## 三、K8s 控制面组件升级流程

### 3.1 升级组件清单

| 组件 | Phase 名称 | 执行模式 | 文件位置 |
|------|-----------|---------|---------|
| **etcd** | EnsureEtcdUpgrade | Inline | `pkg/phaseframe/phases/ensure_etcd_upgrade.go` |
| **API Server** | EnsureMasterUpgrade | Inline | `pkg/phaseframe/phases/ensure_master_upgrade.go` |
| **Controller Manager** | EnsureMasterUpgrade | Inline | 同上（与 API Server 一起升级） |
| **Scheduler** | EnsureMasterUpgrade | Inline | 同上（与 API Server 一起升级） |
| **containerd** | EnsureContainerdUpgrade | Inline | `pkg/phaseframe/phases/ensure_containerd_upgrade.go` |
| **BKE Agent** | EnsureAgentUpgrade | Inline | `pkg/phaseframe/phases/ensure_agent_upgrade.go` |
| **Provider** | EnsureProviderSelfUpgrade | Manifest/Inline | `pkg/phaseframe/phases/ensure_provider_self_upgrade.go` |
| **kube-proxy** | EnsureComponentUpgrade | Manifest | `pkg/phaseframe/phases/ensure_component_upgrade.go` |
| **CoreDNS** | EnsureComponentUpgrade | Manifest | 同上 |
| **Pre-upgrade Resources** | EnsurePreUpgradeResources | Inline | `pkg/phaseframe/phases/ensure_pre_upgrade_resources.go` |

### 3.2 升级顺序（DAG 拓扑）

```
pre-upgrade-resources
        │
        ├──> bkeagent ──> containerd ──> etcd ──> kubernetes-master ──> kubernetes-worker
        │                                                              │
        │                                                              ├──> kube-proxy
        │                                                              └──> coredns
        │
        └──> provider (可并行)
```

**依赖规则**:
- `pre-upgrade-resources` 必须最先执行（创建升级所需 CRD/ConfigMap/Secret）
- `bkeagent` 优先（协调升级过程）
- `containerd` 在 kubelet 之前（容器运行时基础）
- `etcd` 在 control plane 之前（数据存储层）
- `kubernetes-master` 在 worker 之前（控制面）
- `kube-proxy` / `coredns` 在 worker 之后

### 3.3 etcd 升级详细流程

**文件**: `pkg/phaseframe/phases/ensure_etcd_upgrade.go:134`

```
EnsureEtcdUpgrade
├── 1. filterUpgradeableNodes()
│    └── 筛选 Agent Ready 的 etcd 节点
│
├── 2. determineBackupNode()
│    └── 选择第一个 etcd 节点做备份
│
├── 3. 逐节点滚动升级 upgradeNodes()
│    │
│    └── 对每个节点 upgradeSingleNode():
│        ├── markNodeUpgrading() — 设置节点状态为 EtcdUpgrading
│        ├── upgradeEtcd()
│        │   ├── createUpgradeCommand() — 创建 Agent Command CR (含 etcdVersion)
│        │   ├── executeUpgradeCommand() — 通过 Agent 执行升级
│        │   ├── waitForUpgradeComplete() — 等待命令完成
│        │   └── waitForEtcdHealthCheck() — 轮询 etcd Pod 镜像版本
│        │       ├── 间隔: 2s
│        │       ├── 超时: 5min
│        │       └── 验证: Pod Running + Ready + 镜像版本匹配
│        │
│        ├── markNodeUpgradeSuccess() — 成功
│        └── handleUpgradeFailure() — 失败，中止后续节点
│
└── 4. finalizeUpgrade()
     └── 更新 BKECluster.Status.EtcdVersion
```

**静态 Pod 管理**:
- etcd 以静态 Pod 方式运行
- 通过 `kube.StaticPodName(mfutil.Etcd, node.Hostname)` 获取 Pod 名称
- 读取 Pod 容器镜像版本验证升级结果

### 3.4 Master 组件升级详细流程

**文件**: `pkg/phaseframe/phases/ensure_master_upgrade.go:51`

```
EnsureMasterUpgrade
├── 1. getNeedUpgradeNodes()
│    └── 获取需要升级的 Master 节点
│
├── 2. ensureEtcdAdvertiseClientUrlsAnnotation()
│    └── 确保 etcd Pod 有正确的 advertise 注解
│
├── 3. 逐节点滚动升级 upgradeMasterNodesWithParams()
│    │
│    └── 对每个节点:
│        ├── 检查远程 kubelet 版本是否已匹配
│        ├── 标记节点为 NodeUpgrading
│        ├── upgradeNode()
│        │   ├── executeNodeUpgradeWithParams()
│        │   │   └── 创建 UpgradeControlPlane 命令
│        │   └── waitForNodeHealthCheckWithParams()
│        │       ├── 间隔: 2s
│        │       ├── 超时: 5min
│        │       └── 验证: Node Ready + kubelet 版本
│        │
│        └── 标记节点为 NodeNotReady (升级成功)
│
├── 4. updateAddonVersions()
│    └── 更新 kube-proxy/kubectl addon 版本
│
└── 5. 更新 BKECluster.Status.KubernetesVersion
```

**升级的静态 Pod 组件**:
- API Server (`kube-apiserver`)
- Controller Manager (`kube-controller-manager`)
- Scheduler (`kube-scheduler`)

### 3.5 Worker 节点升级详细流程

**文件**: `pkg/phaseframe/phases/ensure_worker_upgrade.go:50`

```
EnsureWorkerUpgrade
├── 1. getNeedUpgradeNodes()
│    └── 获取需要升级的 Worker 节点
│
├── 2. 逐节点滚动升级
│    │
│    └── 对每个节点:
│        ├── drainNode() — 驱逐 Pod
│        ├── 标记节点为 NodeUpgrading
│        ├── upgradeNode()
│        │   ├── 创建 UpgradeWorker 命令
│        │   └── waitForNodeHealthCheck()
│        │
│        ├── 成功 → 标记 NodeNotReady
│        └── 失败 → 记录到 failedUpgradeNodes, 继续其他节点
│
└── 3. 返回失败节点列表（如有）
```

## 四、升级触发与执行流程

### 4.1 升级触发机制

```
用户修改 BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
  │
  v
BKEClusterReconciler.Reconcile()
  │
  ├── ensureClusterVersionOnInstall() — 确保 ClusterVersion CR 存在
  │
  v
ClusterVersionReconciler.Reconcile()
  │
  ├── isInstallPhase() → false (已有 currentVersion)
  │
  v
reconcileUpgrade()
  │
  ├── resolveUpgradePath()
  │   └── pathService.FindPath(current, desired) — BFS 最短路径
  │
  ├── ensurer.Ensure(hopTarget) — 确保 ReleaseImage CR 存在且 Valid
  │
  v
markUpgradeReady(bc, cv, path, hopTarget)
  │
  └── 设置 BKECluster 注解:
      ├── cvo.openfuyao.cn/upgrade-ready = hopTarget
      ├── cvo.openfuyao.cn/cluster-version = cv.Name
      └── cvo.openfuyao.cn/upgrade-path = "v1->v2,v2->v3"
```

### 4.2 声明式 DAG 执行流程

```
BKEClusterReconciler.Reconcile() 再次触发
  │
  v
executePhaseFlow()
  │
  ├── shouldUseDeclarativeUpgrade(bkeCluster)
  │   └── featuregate.UpgradeReady(bkeCluster) — 检查注解
  │
  v
executeUpgradeDAG()
  │
  ├── (1) declarativeUpgradeTargetVersion() — 读取 upgrade-ready 注解
  │
  ├── (2) resolveUpgradeBundle() — 解析 OCI Release Bundle
  │   ├── 查找 ReleaseImage CR
  │   ├── 验证 ReleaseImage.Status.Phase == Valid
  │   └── releaseStore().ResolveRelease() — 拉取 OCI artifact
  │
  ├── (3) resolveCurrentReleaseBundle() — 当前版本 Bundle
  │
  ├── (4) BuildAndSetVersionContextFromBundle()
  │   └── BuildVersionContextForUpgrade()
  │       ├── FillTargetFromBundle(targetBundle)
  │       └── FillCurrentFromBundle(currentBundle)
  │
  ├── (5) SyncUpgradeTargetsToClusterSpec() — 写入目标版本
  │
  ├── (6) patchClusterOpenFuyaoVersionSpecBeforeDAG()
  │
  ├── (7) patchClusterStatus(ClusterUpgrading)
  │
  ├── (8) ensureDeclarativeUpgradeProgress() — 初始化状态
  │
  ├── (9) BuildDAGFromBundle(bundle, BundleDependencyResolver)
  │   ├── UpgradeComponentsFromBundle() — 提取组件列表
  │   ├── topology.BuildUpgradeDAG() — 构建 DAG
  │   └── 验证 DAG 无环
  │
  ├── (10) componentfactory.NewFactoryFromBundle(bundle)
  │
  ├── (11) dagexec.NewScheduler(Config{...})
  │
  v
sched.ExecuteDAG(ctx, phaseCtx, old, new, dag)
  │
  ├── dag.TopologicalBatches() — 拓扑排序
  │
  └── 对每个 batch 执行 executeBatchParallel()
      │
      ├── 过滤: shouldSkipComponent() — 跳过已完成组件
      │
      ├── errgroup + 信号量 (max 8 并发) 并行执行
      │
      └── 对每个 component 执行 executeComponent()
          ├── node.Inline != nil → executeInline()
          │   └── InlineRunner.Execute()
          │       ├── phase.NeedExecute(old, new)
          │       ├── phase.ExecutePreHook()
          │       ├── phase.Execute()
          │       └── phase.ExecutePostHook(err)
          │
          └── node.Inline == nil → executeManifest()
              ├── ManifestStore.GetComponentManifests()
              └── ManifestApplier.ApplyComponent()
```

### 4.3 多跳升级

```
ClusterVersionReconciler.reconcileUpgrade()
  │
  v
pathService.FindPath(current, desired) → [v1→v2, v2→v3, v3→v4]
  │
  v
hopTarget = path[0].To = v2  (只执行第一跳)
  │
  v
标记 upgrade-ready = v2
  │
  v
BKECluster 执行 v1→v2 升级
  │
  v
completeDeclarativeUpgrade()
  ├── cv.Status.CurrentVersion = v2
  ├── cv.Status.Phase = Upgrading (因为 v2 != v4)
  └── 删除 upgrade-ready 注解
        │
        v
触发 ClusterVersionReconciler 再次 Reconcile
  │
  v
下一跳: hopTarget = v3
  │
  ... 直到 currentVersion == desiredVersion
        │
        v
cv.Status.Phase = Ready
```

## 五、状态管理

### 5.1 ClusterVersion 状态机

```
Pending ──> Installing ──> Installed ──> Ready
                                │            │
                                │            ├─> (用户修改 desiredVersion)
                                │            │
                                │            v
                                │      PreChecking ──> Upgrading ──> Upgraded ──> Ready
                                │           │              │
                                │           v              v
                                │    PreCheckFailed     Failed
                                │
                                └─> Blocked
```

### 5.2 BKECluster 状态机（升级相关）

```
ClusterStatus:
  Ready ──> Upgrading ──> Ready
              │
              v
        UpgradeFailed

ClusterHealthState:
  Healthy ──> Upgrading ──> Healthy
                │
                v
          UpgradeFailed

BKEClusterPhase:
  UpgradeControlPlane / UpgradeWorker / UpgradeEtcd
```

### 5.3 DeclarativeUpgradeStatus

```
DeclarativeUpgradeStatus
├── TargetVersion: 当前升级目标版本
├── StartedAt: 升级开始时间
├── FinishedAt: 升级完成时间
├── LastError: 最后错误信息
├── LastFailure: 最后失败组件记录 (含 Attempt 计数)
└── Completed: 已完成组件列表
    └── []DeclarativeUpgradeComponentRecord{Name, Version, CompletedAt}
```

**状态转换**:
- `EnsureInitialized(targetVersion)` — 目标变化时 Reset
- `MarkCompleted(name, version)` — 记录组件完成
- `MarkFailure(name, version, errMsg)` — 记录失败
- `IsCompleted(name, version)` — 跳过已完成组件（断点续传）

## 六、健康检查机制

| 组件 | 方法 | 间隔 | 超时 | 验证内容 |
|------|------|------|------|---------|
| **etcd** | `waitForEtcdHealthCheck()` | 2s | 5min | Pod Running + Ready + 镜像版本匹配 |
| **Master** | `waitForNodeHealthCheckWithParams()` | 2s | 5min | Node Ready + kubelet 版本 |
| **Worker** | `waitForWorkerNodeHealthCheck()` | 2s | 5min | Node Ready + kubelet 版本 |
| **Agent** | `PingBKEAgentOnNodes()` | — | — | Agent 存活 |
| **Provider** | `WaitDeploymentReady()` | — | 5min | Deployment 新 Pod Ready |

## 七、升级失败处理

### 7.1 组件级失败策略

| FailurePolicy | 行为 | 说明 |
|---------------|------|------|
| **FailFast** | 组件失败立即中止当前 batch | 默认策略 |
| **Continue** | 继续执行其他组件 | 预留 |
| **Rollback** | 预留，当前未实现 | 预留 |

### 7.2 节点级失败处理

| 组件 | 失败行为 |
|------|---------|
| **etcd** | 设置节点状态为 `EtcdUpgradeFailed`，中止后续节点 |
| **Master** | 节点失败 → `NodeUpgradeFailed`，**阻塞**直到成功 |
| **Worker** | 节点失败 → 记录到 `failedUpgradeNodes`，**继续**升级其他节点 |

### 7.3 集群级失败处理

```
DAG 执行失败
  │
  v
patchClusterStatus(ClusterUpgradeFailed)
  │
  v
DeclarativeUpgradeStatus.MarkFailure() — 记录最后失败组件和错误信息
  │
  v
ClusterVersion Phase 转为 Failed
  │
  v
断点续传: IsCompleted() 跳过已完成组件，从失败点重试
```

## 八、PodDisruptionBudget 感知设计

### 8.1 PDB 在升级中的作用

PodDisruptionBudget (PDB) 是 Kubernetes 保护应用可用性的关键机制，用于限制同时不可用的 Pod 数量。在升级过程中，PDB 可以：

- **防止服务中断**：确保关键应用的副本数始终满足最低可用性要求
- **控制升级节奏**：避免同时驱逐过多 Pod 导致服务不可用
- **保护有状态应用**：确保 StatefulSet 等关键应用的稳定性

### 8.2 升级前 PDB 检查

在升级开始前，应该检查集群中的 PDB 配置，确保升级过程不会违反 PDB 约束。

**检查流程**：

```
PreUpgradePDBCheck
├── 1. 获取所有 PDB 资源
│   └── kubectl get pdb --all-namespaces
│
├── 2. 验证 PDB 配置合理性
│   ├── minAvailable 或 maxUnavailable 是否设置
│   ├── 选择器是否匹配到实际 Pod
│   └── 当前可用 Pod 数量是否满足 PDB 要求
│
├── 3. 模拟升级影响
│   ├── 对于每个待升级节点
│   ├── 计算该节点上的 Pod 被驱逐后
│   └── 是否会导致违反 PDB 约束
│
└── 4. 生成 PDB 兼容性报告
    ├── 列出可能受影响的 PDB
    ├── 标记高风险应用
    └── 提供调整建议
```

**代码实现**：

```go
// pkg/upgrade/pdb/checker.go

type PDBChecker struct {
    client kubernetes.Interface
}

func (c *PDBChecker) CheckBeforeUpgrade(ctx context.Context, nodes []string) (*PDBCheckReport, error) {
    // 1. 获取所有 PDB
    pdbs, err := c.client.PolicyV1().PodDisruptionBudgets("").List(ctx, metav1.ListOptions{})
    if err != nil {
        return nil, err
    }

    report := &PDBCheckReport{
        AffectedPDBs: []PDBImpact{},
        RiskLevel:    "low",
    }

    // 2. 对每个 PDB 检查升级影响
    for _, pdb := range pdbs.Items {
        impact := c.analyzePDBImpact(ctx, pdb, nodes)
        if impact.WillViolate {
            report.AffectedPDBs = append(report.AffectedPDBs, impact)
            report.RiskLevel = "high"
        }
    }

    return report, nil
}

func (c *PDBChecker) analyzePDBImpact(ctx context.Context, pdb policyv1.PodDisruptionBudget, nodes []string) PDBImpact {
    // 获取匹配的 Pod
    selector, _ := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
    pods, _ := c.client.CoreV1().Pods(pdb.Namespace).List(ctx, metav1.ListOptions{
        LabelSelector: selector.String(),
    })

    // 计算当前可用 Pod 数量
    currentAvailable := 0
    for _, pod := range pods.Items {
        if pod.Status.Phase == v1.PodRunning && pod.DeletionTimestamp == nil {
            currentAvailable++
        }
    }

    // 计算待升级节点上的 Pod 数量
    willBeDisrupted := 0
    for _, pod := range pods.Items {
        if contains(nodes, pod.Spec.NodeName) {
            willBeDisrupted++
        }
    }

    // 检查是否会违反 PDB
    minAvailable := 0
    if pdb.Spec.MinAvailable != nil {
        minAvailable = pdb.Spec.MinAvailable.IntValue()
    } else if pdb.Spec.MaxUnavailable != nil {
        maxUnavailable := pdb.Spec.MaxUnavailable.IntValue()
        minAvailable = len(pods.Items) - maxUnavailable
    }

    willViolate := (currentAvailable - willBeDisrupted) < minAvailable

    return PDBImpact{
        PDBName:          pdb.Name,
        Namespace:        pdb.Namespace,
        CurrentAvailable: currentAvailable,
        WillBeDisrupted:  willBeDisrupted,
        MinRequired:      minAvailable,
        WillViolate:      willViolate,
    }
}
```

### 8.3 Worker 节点升级时的 PDB 感知 drain

Worker 节点升级前需要驱逐节点上的 Pod，这个过程必须尊重 PDB 约束。

**PDB 感知 drain 流程**：

```
PDBAwareDrain
├── 1. 获取节点上的所有 Pod
│   └── 排除 DaemonSet、Mirror Pod 等系统 Pod
│
├── 2. 对每个 Pod 检查 PDB 约束
│   ├── 获取 Pod 匹配的 PDB
│   ├── 检查当前 DisruptionsAllowed
│   └── 如果 DisruptionsAllowed = 0，等待或跳过
│
├── 3. 批量驱逐 Pod
│   ├── 按 PDB 分组
│   ├── 每批驱逐数量不超过 DisruptionsAllowed
│   └── 等待 Pod 优雅终止
│
├── 4. 监控 PDB 状态
│   ├── 实时检查 PDB 的 DisruptionsAllowed
│   ├── 如果达到限制，暂停驱逐
│   └── 等待 PDB 恢复后继续
│
└── 5. 超时处理
    ├── 如果某个 Pod 长时间无法驱逐
    ├── 检查是否违反 PDB
    └── 提供强制驱逐选项（需用户确认）
```

**代码实现**：

```go
// pkg/phaseframe/phases/ensure_worker_upgrade.go

func (e *EnsureWorkerUpgrade) pdbAwareDrain(ctx context.Context, node *confv1beta1.Node) error {
    clientSet, _, _ := kube.GetTargetClusterClient(ctx, e.Ctx.Client, e.Ctx.BKECluster)
    
    // 1. 获取节点上的 Pod
    pods, err := clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{
        FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Hostname}).String(),
    })
    if err != nil {
        return err
    }

    // 2. 过滤可驱逐的 Pod
    evictablePods := filterEvictablePods(pods.Items)

    // 3. 按 PDB 分组
    pdbGroups := groupPodsByPDB(ctx, clientSet, evictablePods)

    // 4. 逐组驱逐
    for pdbName, pods := range pdbGroups {
        for _, pod := range pods {
            // 检查 PDB 是否允许驱逐
            allowed, err := c.checkPDBAllowsEviction(ctx, clientSet, pod)
            if err != nil {
                return err
            }

            if !allowed {
                // 等待 PDB 恢复
                log.Info("PDB constraint, waiting for disruption budget", 
                    "pod", pod.Name, "pdb", pdbName)
                if err := c.waitForPDBRecovery(ctx, clientSet, pod, 5*time.Minute); err != nil {
                    return fmt.Errorf("PDB timeout: %w", err)
                }
            }

            // 驱逐 Pod
            if err := evictPod(ctx, clientSet, pod); err != nil {
                return err
            }
        }
    }

    return nil
}

func (c *PDBChecker) checkPDBAllowsEviction(ctx context.Context, client kubernetes.Interface, pod v1.Pod) (bool, error) {
    pdbs, err := client.PolicyV1().PodDisruptionBudgets(pod.Namespace).List(ctx, metav1.ListOptions{})
    if err != nil {
        return false, err
    }

    for _, pdb := range pdbs.Items {
        selector, _ := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
        if selector.Matches(labels.Set(pod.Labels)) {
            // 检查 DisruptionsAllowed
            if pdb.Status.DisruptionsAllowed <= 0 {
                return false, nil
            }
        }
    }

    return true, nil
}
```

### 8.4 Master 节点升级时的 PDB 考虑

Master 节点上通常运行关键的控制面组件（API Server、Controller Manager、Scheduler），这些组件通常不以 Deployment 方式运行，因此不受 PDB 保护。但是，Master 节点上可能还运行其他业务 Pod，这些 Pod 可能受 PDB 保护。

**Master 节点升级策略**：

1. **控制面组件升级**：
   - API Server、Controller Manager、Scheduler 是静态 Pod，不受 PDB 保护
   - 升级时直接替换静态 Pod manifest，无需考虑 PDB
   - 但是需要确保多 Master 场景下，至少有一个 API Server 可用

2. **业务 Pod 处理**：
   - 如果 Master 节点上运行有业务 Pod，需要先驱逐
   - 驱逐时必须尊重 PDB 约束
   - 使用与 Worker 节点相同的 PDB 感知 drain 逻辑

**多 Master 场景的 API Server 可用性保护**：

```go
// pkg/phaseframe/phases/ensure_master_upgrade.go

func (e *EnsureMasterUpgrade) ensureAPIServerAvailability(ctx context.Context) error {
    clientSet, _, _ := kube.GetTargetClusterClient(ctx, e.Ctx.Client, e.Ctx.BKECluster)
    
    // 获取所有 Master 节点
    masterNodes := e.getMasterNodes()
    
    // 计算当前可用的 API Server 数量
    availableAPIServers := 0
    for _, node := range masterNodes {
        if e.isAPIServerHealthy(ctx, clientSet, node) {
            availableAPIServers++
        }
    }
    
    // 如果只剩一个 API Server，拒绝升级
    if availableAPIServers <= 1 {
        return fmt.Errorf("cannot upgrade master: only %d API Server available", availableAPIServers)
    }
    
    return nil
}
```

### 8.5 PDB 配置建议

为了确保升级过程的顺利进行，建议用户为关键应用配置合理的 PDB：

**推荐 PDB 配置**：

```yaml
# 对于 Deployment（3 副本）
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: my-app-pdb
spec:
  minAvailable: 2  # 至少保持 2 个 Pod 可用
  selector:
    matchLabels:
      app: my-app

# 对于 StatefulSet（如数据库）
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: database-pdb
spec:
  maxUnavailable: 1  # 最多允许 1 个 Pod 不可用
  selector:
    matchLabels:
      app: database
```

**PDB 配置原则**：

| 应用类型 | 推荐配置 | 说明 |
|---------|---------|------|
| **无状态应用（3+ 副本）** | `minAvailable: N-1` | 允许 1 个 Pod 不可用 |
| **有状态应用（主从架构）** | `maxUnavailable: 1` | 只允许 1 个节点升级 |
| **关键业务应用** | `minAvailable: N` | 不允许任何中断 |
| **测试环境应用** | 不配置 PDB | 允许自由升级 |

### 8.6 升级过程中的 PDB 监控

升级过程中应该持续监控 PDB 状态，及时发现并处理问题。

**监控指标**：

| 指标 | 说明 | 告警阈值 |
|------|------|---------|
| `pdb_disruptions_allowed` | 当前允许的中断数量 | = 0 且持续 5 分钟 |
| `pdb_expected_healthy` | 期望的健康 Pod 数量 | < 实际健康数量 |
| `pdb_current_healthy` | 当前健康的 Pod 数量 | < 期望数量 |
| `pdb_disruptions_total` | 累计中断次数 | 超过阈值 |

**监控代码**：

```go
// pkg/upgrade/pdb/monitor.go

type PDBMonitor struct {
    client kubernetes.Interface
    ticker *time.Ticker
}

func (m *PDBMonitor) Start(ctx context.Context) {
    m.ticker = time.NewTicker(30 * time.Second)
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case <-m.ticker.C:
                m.checkPDBStatus(ctx)
            }
        }
    }()
}

func (m *PDBMonitor) checkPDBStatus(ctx context.Context) {
    pdbs, _ := m.client.PolicyV1().PodDisruptionBudgets("").List(ctx, metav1.ListOptions{})
    
    for _, pdb := range pdbs.Items {
        // 检查 DisruptionsAllowed
        if pdb.Status.DisruptionsAllowed == 0 {
            log.Warn("PDB has no disruptions allowed",
                "pdb", pdb.Name,
                "namespace", pdb.Namespace,
                "currentHealthy", pdb.Status.CurrentHealthy,
                "desiredHealthy", pdb.Status.DesiredHealthy)
        }
        
        // 检查是否违反 PDB
        if pdb.Status.CurrentHealthy < pdb.Status.DesiredHealthy {
            log.Error("PDB violated",
                "pdb", pdb.Name,
                "namespace", pdb.Namespace,
                "currentHealthy", pdb.Status.CurrentHealthy,
                "desiredHealthy", pdb.Status.DesiredHealthy)
        }
    }
}
```

### 8.7 PDB 感知升级的完整流程

```
升级开始
  │
  v
PreCheck 阶段
  ├── PDB 兼容性检查
  │   ├── 获取所有 PDB
  │   ├── 模拟升级影响
  │   └── 生成兼容性报告
  │
  └── 如果存在高风险 PDB
      ├── 警告用户
      └── 建议调整 PDB 配置
  │
  v
Master 节点升级
  ├── 检查 API Server 可用性（多 Master 场景）
  ├── 驱逐 Master 上的业务 Pod（PDB 感知）
  ├── 升级控制面组件
  └── 验证控制面健康
  │
  v
Worker 节点升级
  ├── 对每个 Worker 节点
  │   ├── PDB 感知 drain
  │   │   ├── 按 PDB 分组 Pod
  │   │   ├── 检查 DisruptionsAllowed
  │   │   ├── 批量驱逐（不超过限制）
  │   │   └── 等待 PDB 恢复
  │   │
  │   ├── 升级 kubelet
  │   └── 验证节点健康
  │
  └── 所有 Worker 升级完成
  │
  v
PostCheck 阶段
  ├── 验证所有 PDB 满足约束
  ├── 检查所有应用健康
  └── 生成升级报告
  │
  v
升级完成
```

### 8.8 代码改造清单

| 文件 | 改造内容 | 工作量 |
|------|---------|--------|
| `pkg/upgrade/pdb/checker.go`（新增） | PDB 检查器实现 | 0.3 人月 |
| `pkg/upgrade/pdb/monitor.go`（新增） | PDB 监控器实现 | 0.2 人月 |
| `pkg/phaseframe/phases/ensure_worker_upgrade.go` | 集成 PDB 感知 drain | 0.3 人月 |
| `pkg/phaseframe/phases/ensure_master_upgrade.go` | 集成 PDB 感知 drain + API Server 可用性检查 | 0.2 人月 |
| 测试与文档 | 单元测试、集成测试、用户文档 | 0.2 人月 |
| **总计** | | **1.2 人月** |

## 九、证书管理

**文件**: `pkg/certs/generator.go`

| 证书类型 | 说明 |
|---------|------|
| RootCA | 根 CA 证书 |
| EtcdCA | etcd CA 证书 |
| FrontProxyCA | Front Proxy CA 证书 |
| APIServer | API Server 证书 |
| EtcdServer | etcd Server 证书 |
| EtcdPeer | etcd Peer 证书 |
| ControllerManager | Controller Manager 证书 |
| Scheduler | Scheduler 证书 |
| Kubelet | Kubelet 证书 |
| ServiceAccount | Service Account 密钥 |
| Kubeconfig | Kubeconfig 文件 |

**升级场景**: 证书在部署阶段生成，升级不重新生成证书。`VerifyExpirationTime()` 检查 30 天过期告警。

## 十、当前实现的优势

1. **声明式 DAG 架构**: 通过 `ReleaseImage` + `ComponentVersion` 定义升级组件和依赖，实现完全声明式的升级编排
2. **拓扑排序 + 并行执行**: `TopologicalBatches()` 将无依赖组件放入同一批次并行执行（最大 8 并发）
3. **断点续传**: `DeclarativeUpgradeStatus.Completed` 持久化已完成组件，控制器重启后从断点继续
4. **多跳升级自动化**: `ClusterVersionReconciler` 自动计算最短升级路径，逐跳执行
5. **双模式执行**: 组件支持 `inline`（Go 代码处理器）和 `manifest`（YAML 清单）两种执行模式
6. **版本上下文驱动**: `VersionContext` 统一管理所有组件的 current/target 版本
7. **完善的校验链**: UpgradePath 环检测 + ReleaseImage 签名验证 + 组件兼容性检查
8. **状态持久化**: `DeclarativeUpgradeStatus` 提供完整的升级可观测性

## 十一、当前实现的不足与改进建议

| 问题 | 影响 | 改进建议 |
|------|------|---------|
| **Rollback 策略未实现** | 升级失败后只能人工介入 | 实现 `FailurePolicy.Rollback` |
| **Provider 自升级风险** | 控制器重启可能导致状态丢失 | 增加状态持久化和恢复机制 |
| **etcd 备份策略简单** | 仅选择第一个节点备份 | 增加备份验证和恢复机制 |
| **Master 升级阻塞式** | 节点失败阻塞整个流程 | 增加自动回退或跳过机制 |
| **健康检查超时固定** | 大型集群可能不够 | 支持可配置超时 |
| **PreCheck/PostCheck 未执行** | 定义了检查步骤但未实际执行 | 实现检查步骤执行逻辑 |
| **证书升级不支持** | 证书过期需要单独处理 | 增加证书轮转能力 |
| **多集群升级无编排** | 每次只处理一个集群 | 增加跨集群升级编排能力 |

---

**文档版本**：v1.0  
**创建日期**：2026-08-10  
**基于代码版本**：cluster-api-provider-bke main 分支

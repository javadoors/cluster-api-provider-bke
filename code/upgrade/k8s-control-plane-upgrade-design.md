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

## 八、证书管理

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

## 九、当前实现的优势

1. **声明式 DAG 架构**: 通过 `ReleaseImage` + `ComponentVersion` 定义升级组件和依赖，实现完全声明式的升级编排
2. **拓扑排序 + 并行执行**: `TopologicalBatches()` 将无依赖组件放入同一批次并行执行（最大 8 并发）
3. **断点续传**: `DeclarativeUpgradeStatus.Completed` 持久化已完成组件，控制器重启后从断点继续
4. **多跳升级自动化**: `ClusterVersionReconciler` 自动计算最短升级路径，逐跳执行
5. **双模式执行**: 组件支持 `inline`（Go 代码处理器）和 `manifest`（YAML 清单）两种执行模式
6. **版本上下文驱动**: `VersionContext` 统一管理所有组件的 current/target 版本
7. **完善的校验链**: UpgradePath 环检测 + ReleaseImage 签名验证 + 组件兼容性检查
8. **状态持久化**: `DeclarativeUpgradeStatus` 提供完整的升级可观测性

## 十、当前实现的不足与改进建议

| 问题 | 影响 | 改进建议 |
|------|------|---------|
| **Rollback 策略未实现** | 升级失败后只能人工介入 | 实现 `FailurePolicy.Rollback` |
| **Provider 自升级风险** | 控制器重启可能导致状态丢失 | 增加状态持久化和恢复机制 |
| **etcd 备份策略简单** | 仅选择第一个节点备份 | 增加备份验证和恢复机制 |
| **Master 升级阻塞式** | 节点失败阻塞整个流程 | 增加自动回退或跳过机制 |
| **Worker 升级 drain 无 PDB 保护** | 可能导致服务中断 | 集成 PodDisruptionBudget 感知 |
| **健康检查超时固定** | 大型集群可能不够 | 支持可配置超时 |
| **PreCheck/PostCheck 未执行** | 定义了检查步骤但未实际执行 | 实现检查步骤执行逻辑 |
| **证书升级不支持** | 证书过期需要单独处理 | 增加证书轮转能力 |
| **多集群升级无编排** | 每次只处理一个集群 | 增加跨集群升级编排能力 |

---

**文档版本**：v1.0  
**创建日期**：2026-08-10  
**基于代码版本**：cluster-api-provider-bke main 分支

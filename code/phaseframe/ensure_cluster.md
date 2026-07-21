# `ensure_cluster.go` 功能规格与业务流程

## 一、定位

- **文件**：[pkg/phaseframe/phases/ensure_cluster.go](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go)
- **Phase 名**：`EnsureCluster`（[L41](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L41)）
- **类型**：`phaseframe.Phase` 接口实现（继承 `BasePhase`）
- **位置**：在 DAG 状态机中处于**集群安装/升级完成后的稳定态**，承担"集群就绪检查 + 定时健康巡检"职责

## 二、功能规格

### 2.1 核心职责

| 职责 | 描述 | 关键方法 |
|------|------|---------|
| **远程客户端初始化** | 通过 `BKECluster` 建立到目标集群的连接 | [getRemoteClient](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L154) |
| **节点标签管理** | 为节点设置告警标签、裸金属标签、用户自定义标签 | setAlertLabel / setBareMetalLabel / setNodeLabel |
| **K8s Token 维护** | 创建/补全访问目标集群的 ServiceAccount Token | [ensureK8sToken](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L215) |
| **集群健康检查** | 调用远程集群 `CheckClusterHealth` 做节点+组件级健康巡检 | [performHealthCheck](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L330) |
| **状态归一** | 健康检查通过后归一为 `ClusterReady`，并同步版本字段 | [performHealthCheck](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L330) |
| **ClusterVersion 同步** | 首次安装成功后同步 `ClusterVersion` CR 的安装状态 | [performHealthCheck:386](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L386) |
| **Tracker 状态联动** | 校验节点 tracker 是否允许，移除"健康检查失败"注解 | [handleClusterReadyPostCheck](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L295) |
| **指标上报** | 上报 `ClusterHealthyCount` 指标 | [handleClusterReadyPostCheck](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L295) |
| **定时 Reconcile** | 正常态每 5min 重新调谐，异常态 10s 快速重试 | [Execute](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L85) |

### 2.2 触发条件（NeedExecute）

[L164-L168](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L164-L168)：基于 `BasePhase.DefaultNeedExecute`，并将 Phase 状态置为 `PhaseWaiting`。

### 2.3 短路条件（不执行健康检查的场景）

| 条件 | 返回 | 说明 |
|------|------|------|
| `e.Ctx.Cluster == nil` | 立即 error | CAPI Cluster 未注入 |
| `ControlPlaneInitializedCondition != True` | 立即 error | 控制面未初始化 |
| `isClusterInSpecialState` | 返回聚合错误 | 集群处于 Scaling/Initializing/Paused/Upgrading 时跳过 |
| 有节点需要 PostProcess | `RequeueAfter=10s` | 节点后置处理未完成时不进入健康检查，避免误判 |
| `ClusterHealthState==Deploying && 未完成首次部署` | error | 首次部署未完成前不检查 |

**特殊状态列表**（[L117-L125](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L117-L125)）：
- ClusterMasterScalingUp / ScalingDown
- ClusterWorkerScalingUp / ScalingDown
- ClusterInitializing / ClusterPaused / ClusterUpgrading

## 三、业务流程

```
EnsureCluster.Execute()
    │
    ├─ 1. 前置校验
    │   ├─ Cluster != nil
    │   └─ ControlPlaneInitializedCondition == True
    │
    ├─ 2. getRemoteClient()                  # 建立到目标集群的 Client
    │
    ├─ 3. setAlertLabel()                     # 给第一个 worker 节点打告警标签（忽略错误）
    │      └─ 仅 BKECluster 才执行
    │
    ├─ 4. setBareMetalLabel()                 # ContainerRuntime==richrunc 时打裸金属标签
    │
    ├─ 5. setNodeLabel()                      # 应用 Spec.ClusterConfig.Cluster.Labels + 节点自定义 Labels
    │      ├─ buildNodeLabelsMap：merge 节点 Labels 与全局 Labels
    │      └─ applyNecessaryLabels → waitLabelReady (1min 超时, 10s 轮询)
    │
    ├─ 6. ensureK8sToken()                    # 维护访问远端集群的 ServiceAccount Token
    │      ├─ Secret 不存在 → NewK8sToken + 创建 Secret
    │      ├─ Secret 存在但无 OwnerRef → SetControllerReference
    │      └─ Secret.Data.token 为空 → 重新生成
    │
    ├─ 7. 短路检查
    │   ├─ isClusterInSpecialState → return
    │   └─ GetNeedPostProcessNodesWithBKENodes > 0
    │      → ConditionMark NodesPostProcessCondition=False
    │      → RequeueAfter=10s
    │
    ├─ 8. ensureClusterReady()
    │   │
    │   ├─ 8.1 首次部署未完成检查
    │   │      ClusterHealthState==Deploying && !ClusterEndDeployedWithContext
    │   │      → return error "cluster is deploying"
    │   │
    │   ├─ 8.2 runHealthChecks()              # 循环 3 次执行健康检查
    │   │      └─ performHealthCheck()
    │   │          │
    │   │          ├─ GetBKENodesForBKECluster → NewBKENodes
    │   │          │
    │   │          ├─ remoteClient.CheckClusterHealth()
    │   │          │   ├─ 成功 → UpdateModifiedBKENodes (回写节点状态)
    │   │          │   │        updateClusterVersionStatus (补全版本字段)
    │   │          │   │        ShouldSyncClusterVersionInstallStatus
    │   │          │   │           → SyncClusterVersionInstallStatus (首次安装同步)
    │   │          │   │        ClusterStatus = ClusterReady
    │   │          │   │        ClusterHealthState = Healthy
    │   │          │   │        Report() (上报 Phase 状态)
    │   │          │   │
    │   │          │   └─ 失败 → ClusterStatus = ClusterUnhealthy
    │   │          │            ClusterHealthState = Unhealthy
    │   │          │            UpdateModifiedBKENodes (仍回写失败节点状态)
    │   │          │            return err
    │   │          │
    │   │          └─ 循环 3 次成功 → log.Finish("cluster is ready")
    │   │
    │   └─ 8.3 handleClusterReadyPostCheck()
    │       ├─ metricrecord.ClusterHealthyCountRecord
    │       ├─ ClusterAllowTrackerWithBKENodes 校验
    │       │   ├─ 允许 → TargetClusterReadyCondition=True
    │       │   └─ 不允许 → TargetClusterReadyCondition=False
    │       └─ ClusterStatus==ClusterReady 且存在 Tracker 失败注解
    │           → RemoveAnnotation (ClusterTrackerHealthyCheckFailedAnnotationKey)
    │
    └─ 9. 返回
        ├─ 正常态 → RequeueAfter = 5min (periodicCheckInterval)
        └─ 异常态 → RequeueAfter = 10s (quickRequeueInterval)
```

## 四、关键设计要点

### 4.1 三次循环健康检查（[L324-L331](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L324-L331)）

```go
for i := 0; i < 3; i++ {
    if err := e.performHealthCheck(...); err != nil {
        return err
    }
}
```
**设计意图**：连续 3 次健康检查通过才认定集群稳定，避免瞬时抖动误判。但当前实现中**只要任一次失败立即返回**，并未真正"3 次都通过才放行"——这是已识别的一个**潜在缺陷**（3 次循环没有累加状态判断，等价于单次检查 + 2 次冗余调用）。

### 4.2 节点状态回写

无论健康检查成功或失败，都会调用 [mergecluster.UpdateModifiedBKENodes](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L345) 把 `BKENodes` 的最新状态（`State`/`StateCode`/`Message`/`NeedSkip`）同步到 BKEMachine 对象，确保用户能在 CR 状态中看到具体节点的健康情况。

### 4.3 ClusterVersion 状态联动（[L386-L394](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L386-L394)）

首次安装成功后，调用 `clusterversion.ShouldSyncClusterVersionInstallStatus` 判断是否需要同步 `ClusterVersion` CR 的安装状态。这是 KEP-5 声明式升级方案中"集群层状态回写 ClusterVersion"的落地。

### 4.4 标签管理三层结构

| 层级 | 来源 | 应用范围 |
|------|------|---------|
| 全局标签 | `Spec.ClusterConfig.Cluster.Labels` | 所有节点 |
| 节点自定义标签 | `Spec.Nodes[i].Labels` | 指定 hostname 节点 |
| 系统标签 | `AlertLabelKey` / `BareMetalLabelKey` | 第一个 worker / 所有节点（richrunc 运行时） |

合并策略（[mergeLabels](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L478)）：**节点自定义标签优先于全局标签**。

### 4.5 重试节奏

| 场景 | RequeueAfter | 原因 |
|------|------------|------|
| 节点后置处理未完成 | 10s | 快速收敛 |
| 健康检查失败 | 10s | 等待组件恢复 |
| 正常巡检 | 5min | 周期性巡检，平衡负载与时效 |

### 4.6 错误聚合

采用 `kerrors.NewAggregate(errs)` 模式，多次错误聚合返回，便于调用方一次性看到所有问题。但部分位置（如 [L96](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L96)）存在"错误已加入 errs 又单独 return"的冗余，是已识别的小问题。

## 五、与其他 Phase 的协作

```
EnsureNodesEnv ──┐
EnsureBKEAgent ──┤
EnsureMasterInit ┤
EnsureWorkerJoin ┤  → EnsureCluster (本 Phase)
EnsureAddonDeploy┘        │
                          ▼
                   定时 Reconcile (5min)
                          │
                          ▼
            CheckClusterHealth (节点 + 组件级检查)
                          │
              ┌───────────┴────────────┐
              ▼                        ▼
        ClusterReady               ClusterUnhealthy
        Healthy                    (10s 后重试)
```
**上下游关系**：
- **上游**：所有 `Ensure*`Phase 完成后进入本 Phase
- **下游**：无显式下游 Phase，但通过 `ClusterStatus` 触发上层 Controller 的进一步决策（如升级、扩缩容）
- **横向**：与 `EnsureEtcdUpgrade`/`EnsureControlPlaneUpgrade` 等 KEP-5 DAG 节点互斥（通过 `isClusterInSpecialState` 短路）

## 六、已知问题与改进方向

| 问题 | 位置 | 说明 |
|------|------|------|
| 三次循环等价于一次 | [L325-L328](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L325-L328) | 失败立即返回，未真正实现"3 次都通过"语义 |
| `errs` 聚合冗余 | [L96-L99](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L96-L99) | 错误已加入 errs 又单独 return 聚合结果 |
| 特殊状态错误日志误用 | [L102](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L102) | `isClusterInSpecialState` 返回 true 时打印 `err.Error()`，但此时 err 可能为 nil |
| `ensureRemoteBKEConfigCM` 已废弃 | [L262](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L262) | 函数保留但未被调用，注释标注 Deprecated |
| `ensureAgentStatus` 未被调用 | [L410](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L410) | 方法保留但 `Execute()` 中未调用，疑似遗留代码 |
| 性能瓶颈 | 64 节点集群 | `ListNodes` + `CheckClusterHealth` 大规模 Pod 列表耗时严重（详见 [性能报告](file:///cluster-api-provider-bke/code/performance/report/64节点集群性能瓶颈分析与优化方案.md)） |

## 七、与 KEP-5/KEP-6 的关系

- **KEP-5（声明式升级）**：本 Phase 是升级 DAG 完成后的**最终归一点**，通过 `ClusterStatus=ClusterReady` 标识升级成功；同时通过 `SyncClusterVersionInstallStatus` 回写 `ClusterVersion` CR
- **KEP-6（状态机重构）**：本 Phase 的 `ClusterStatus`/`ClusterHealthState` 二元状态字段是 KEP-6 重点治理对象，未来将统一为 `PhaseStatus` + Conditions 模式
- **Tracker 机制**：[handleClusterReadyPostCheck](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L295) 中的 `ClusterAllowTrackerWithBKENodes` 是 BKECluster Controller 与节点 Tracker 的解耦点


# `CheckClusterHealth` 函数作用与业务流程

## 一、定位

- **文件**：[pkg/kube/health.go:31](file:///cluster-api-provider-bke/pkg/kube/health.go#L31)
- **接口契约**：[pkg/kube/kube.go:64-65](file:///cluster-api-provider-bke/pkg/kube/kube.go#L64-L65)
- **调用方**：[ensure_cluster.go:371](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L371) `EnsureCluster.Execute()`
- **触发场景**：集群安装完成、升级完成、定期 Reconcile 时，由 `EnsureCluster` Phase 调用远程目标集群做最终健康检查

## 二、核心作用

**一句话**：对目标 Kubernetes 集群做"节点级 + 组件级"双重健康检查，返回聚合错误；同时把节点状态写回 `BKENodes`。

| 维度 | 检查内容 |
|------|---------|
| **节点级** | Node Ready 状态、Kubelet 版本一致性、Master 节点静态 Pod（apiserver/controller-manager/scheduler/etcd） |
| **组件级** | kube-system 核心组件 + 用户已声明的扩展 Addon（按 Pod 前缀匹配，检查 Pod Running + Ready + 无致命 Waiting 状态） |

## 三、业务流程

```
EnsureCluster.Execute()
    │
    ▼
CheckClusterHealth(cluster, currentVersion, bkeNodes)
    │
    ├─ 1. ListNodes()                              # 拉取目标集群所有 Node
    │
    ├─ 2. 解析 currentVersion                       # 空则 fallback 到 Spec.ClusterConfig.Cluster.KubernetesVersion
    │
    ├─ 3. 遍历 Node 列表 (节点级检查)
    │   │
    │   ├─ GetNodeStateNeedSkip(nodeIP) == true     # 跳过节点（缩容/删除中）
    │   │      → continue
    │   │
    │   ├─ NodeReady(node) == true
    │   │      → bkeNodes.SetNodeStateWithMessage(NodeReady, "")
    │   │
    │   └─ NodeHealthCheck(node, currentVersion)
    │       │
    │       ├─ checkNodeReady()                    # NodeReady==True
    │       ├─ checkNodeVersion()                   # kubeletVersion == currentVersion
    │       └─ Master 节点额外检查 CheckComponentHealth()
    │           └─ 遍历 etcd/kube-apiserver/kube-controller-manager/kube-scheduler
    │              验证 StaticPod (kube-system/<component>-<nodeName>) Phase=Running
    │
    │   失败 → bkeNodes.SetNodeStateWithMessage(NodeNotReady, err)
    │          errs = append(errs, ...)
    │
    ├─ 4. CheckAllComponentsHealth(cluster, log)    # 组件级检查
    │   │
    │   ├─ 遍历 neededComponentChecks (kube-system 必查)
    │   │   └─ calico-kube-controllers / calico-node / coredns / etcd- / kube-apiserver- /
    │   │      kube-controller-manager- / kube-proxy- / kube-scheduler-
    │   │      每个 prefix → verifyComponentPods()
    │   │          ├─ 无 Pod → "no pods with prefix 'X' in namespace"
    │   │          ├─ coredns 特殊：1 个 Ready 即通过
    │   │          └─ 其他：所有 Pod 必须 Running + Ready + 无致命 Waiting
    │   │
    │   └─ 遍历 cluster.Spec.ClusterConfig.Addons
    │       ├─ neededAddons (kubeproxy/calico/coredns) → 已在 neededComponentChecks 中检查，跳过
    │       ├─ 不在 extraAddonComponents 中 → 用户自定义 Addon，自动跳过
    │       └─ 在 extraAddonComponents 中 → processAddonComponentCheck()
    │           └─ cluster-api / openfuyao-system-controller 两类扩展
    │
    └─ 5. 聚合返回
        ├─ errs 非空 → kerrors.NewAggregate(errs)
        └─ 全部通过   → log.Infof("cluster %s/%s health check pass")
```

## 四、返回处理（调用方）

[ensure_cluster.go:371-378](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster.go#L371-L378)：

```go
if err := e.remoteClient.CheckClusterHealth(bkeCluster, bkeCluster.Status.KubernetesVersion, bkeNodes); err != nil {
    bkeCluster.Status.ClusterStatus      = ClusterUnhealthy
    bkeCluster.Status.ClusterHealthState = Unhealthy
    log.Warn(ClusterUnhealthyReason, err.Error())
    // 仍要把 BKENodes 状态写回，便于定位具体节点
    mergecluster.UpdateModifiedBKENodes(ctx, c, bkeNodes)
    return err
}
```

**关键设计**：
1. **节点状态回写**：检查过程中无论成功失败都更新 `bkeNodes`，再由 `UpdateModifiedBKENodes` 同步回 BKEMachine status
2. **错误聚合**：所有节点/组件错误聚合后返回，调用方一次性看到所有不健康项
3. **短路径终止**：组件级检查失败立即 return（不继续 addon 检查），但节点级错误已收集

## 五、设计要点与已知问题

| 要点 | 说明 |
|------|------|
| **needskip 跳过机制** | 缩容中的节点（[GetNodeStateNeedSkip](file:///cluster-api-provider-bke/pkg/kube/health.go#L41)）跳过检查，避免误报 |
| **版本一致性校验** | Kubelet 版本必须等于 `currentVersion`（升级时为 Status.KubernetesVersion，确认目标节点已升级完成） |
| **CoreDNS 宽松策略** | [verifyComponentPods](file:///cluster-api-provider-bke/pkg/kube/health.go) 对 `coredns` 前缀做了特例：1 个 Ready 即通过 |
| **Addon 白名单** | 仅 `cluster-api`、`openfuyao-system-controller` 两类声明在 [extraAddonComponents](file:///cluster-api-provider-bke/pkg/kube/health.go#L81-L123) 的 Addon 才校验，用户自定义 Addon 自动跳过 |
| **致命 Waiting Reason** | `CrashLoopBackOff`、`ImagePullBackOff`、`ErrImagePull`、`CreateContainerConfigError`、`CreateContainerError` 被判定为永久失败，其他 Waiting Reason（如 `ContainerCreating`）不报错 |
| **已知问题** | 64 节点集群下 Pod 列表规模大，性能瓶颈明显（见 [64节点集群性能瓶颈分析与优化方案.md](file:///cluster-api-provider-bke/code/performance/report/64节点集群性能瓶颈分析与优化方案.md#L1426)）；日志中可见大量 `ingress-nginx-controller-* unhealthy: status: Pending` 噪声 |

## 六、与 KEP-5 状态机的关系

`CheckClusterHealth` 是 [EnsureCluster Phase](file:///cluster-api-provider-bke/code/exception/state/ensure_cluster.md) 的 `PerformCheck → CheckHealth` 实现，是 KEP-5 DAG 执行完毕后的**最终收敛点**：
- DAG 升级成功 → EnsureCluster → CheckClusterHealth 通过 → `ClusterStatus=Healthy`
- DAG 升级失败 → EnsureCluster → CheckClusterHealth 失败 → `ClusterStatus=Unhealthy`，错误回写 `BKENodes`

但在 KEP-5 26.06 版本回顾中已发现：`EnsureEtcdUpgrade` 未纳入 `ClusterUpgradePhaseNames`，导致 Etcd 升级期间状态归一可能落到 `ClusterUnknown`，与本函数的"Healthy/Unhealthy"二元结果不闭环。

# `NeedSkip` 含义说明

## 一、定义

**`NeedSkip` 是 `BKENode.Status` 上的一个布尔字段，表示"该节点在后续操作中需要被跳过"。**

- **字段位置**：[api/bkecommon/v1beta1/bkenode_types.go:98-100](file:///cluster-api-provider-bke/api/bkecommon/v1beta1/bkenode_types.go#L98-L100)
  ```go
  // NeedSkip indicates whether this node should be skipped during operations
  // +optional
  NeedSkip bool `json:"needSkip,omitempty"`
  ```
- **CRD 字段**：`bkenode.status.needSkip`
- **读取方法**：`BKENodes.GetNodeStateNeedSkip(ip)`（[ensure_worker_join.go:90](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go#L90)）
- **设置方法**：`NodeFetcher.SetNodeNeedSkip(...)`（[fetcher.go:336](file://cluster-api-provider-bke/utils/capbke/nodeutil/fetcher.go#L336)）

## 二、业务语义

`NeedSkip=true` 表示该节点被**永久排除出当前批次操作**，后续 Phase 在过滤节点时会自动跳过它。这是一个**尽力模式（Best-Effort）**的设计：

| 维度 | 说明 |
|------|------|
| **设计目标** | 部分 Worker 节点失败时不阻塞整体流程，让成功的节点继续推进，失败节点隔离处理 |
| **触发场景** | Worker 节点在 `EnsureNodesEnv`、`EnsureBKEAgent`、`EnsureWorkerJoin` 等 Phase 中失败 |
| **影响范围** | 后续所有 Phase 的节点过滤逻辑（`NeedSkip=true` 即排除）+ 健康检查跳过 |

## 三、何时被设置为 true

根据 [安装规格.md](file:///cluster-api-provider-bke/code/specification/安装规格.md) 与 [扩缩容规格.md](file:///cluster-api-provider-bke/code/specification/扩缩容规格.md)：

| 代码位置 | 触发条件 |
|----------|---------|
| [ensure_worker_join.go:364](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go#L364) | Worker 节点 kubeadm join 失败 |
| [ensure_worker_join.go:516,528](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go#L516) | Worker 节点初始化失败 |
| [ensure_nodes_env.go:299](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L299) | Worker 节点环境准备失败 |
| [ensure_bke_agent.go:264,613](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L264) | Worker 节点 Agent 推送失败 |
| [fetcher.go:528](file:///cluster-api-provider-bke/utils/capbke/nodeutil/fetcher.go#L528) | 通用失败兜底 |

**关键区别**：**Master 节点失败不设置 NeedSkip**，而是直接返回 error 中断整个流程（参考 [安装规格.md:319](file:///cluster-api-provider-bke/code/specification/安装规格.md)）。

## 四、读取位置（过滤逻辑）

`NeedSkip=true` 的节点在以下位置被排除：

| 代码位置 | 用途 |
|----------|------|
| [health.go:41](file:///cluster-api-provider-bke/pkg/kube/health.go#L41) | 健康检查跳过（你之前问的 `CheckClusterHealth`） |
| [util.go:123,136,204,1154](file:///cluster-api-provider-bke/pkg/phaseframe/phaseutil/util.go#L123) | 节点过滤通用工具 |
| [command.go:205](file:///cluster-api-provider-bke/pkg/phaseframe/phaseutil/command.go#L205) | 命令执行跳过 |
| [clusterapi.go:273](file:///cluster-api-provider-bke/pkg/phaseframe/phaseutil/clusterapi.go#L273) | CAPI 对象处理跳过 |
| [ensure_nodes_env.go:116](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L116) | 节点环境准备跳过 |
| [ensure_containerd_upgrade.go:206](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go#L206) | Containerd 升级跳过 |

## 五、业务流程示意

```
Worker 扩容（多个 Worker 节点并行处理）
        │
        ├─ Worker A: kubeadm join 成功 → NeedSkip=false
        ├─ Worker B: kubeadm join 失败 → NeedSkip=true  ← 隔离
        └─ Worker C: kubeadm join 成功 → NeedSkip=false
                │
                ▼
      后续 Phase（如 EnsureAddonDeploy、EnsureCluster）
                │
                ├─ 过滤节点：NeedSkip=true 的 B 被排除
                ├─ 仅对 A、C 执行操作
                └─ 健康检查：B 被跳过，不误报 ClusterUnhealthy
```

## 六、注意事项（已知风险）

参考 [扩缩容规格.md:983-985](file:///cluster-api-provider-bke/code/specification/扩缩容规格.md)：

1. **永久阻塞风险**：若所有 Worker 节点都被标记 `NeedSkip=true`，扩容流程会永久阻塞，需**手动清除 NeedSkip 标记**才能重试：
   ```bash
   kubectl patch bkenode <name> --type=merge -p '{"status":{"needSkip":false}}'
   ```
2. **无自动恢复**：26.06 版本未实现"失败节点重试恢复"机制，`NeedSkip` 一旦设置不会自动清除
3. **状态语义重叠**：与 `State=NodeInitFailed` 表达相近语义，KEP-6 状态机重构中已识别此问题（参考 [kep6-state-machine-refactor.md:667](file:///cluster-api-provider-bke/code/kep/kep6/kep6-state-machine-refactor.md#L667)："NeedSkip 保留，业务逻辑字段"）

## 七、与 `CheckClusterHealth` 的关系

回到你之前问的 [CheckClusterHealth](file:///cluster-api-provider-bke/pkg/kube/health.go#L31)：

```go
if bkeNodes.GetNodeStateNeedSkip(nodeIP) {
    log.Debugf("node %q (IP: %s) health check skipped due to needskip=true", node.Name, nodeIP)
    continue
}
```
**作用**：避免对"已被隔离的失败节点"做健康检查，防止这些节点误报 `NodeNotReady` 导致整个集群状态被判定为 `ClusterUnhealthy`。这是"尽力模式"的最后一道防线——**失败节点不影响整体集群健康判定**。

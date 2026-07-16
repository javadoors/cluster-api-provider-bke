# handleClusterXXXPhase与ClusterStatus的对应关系

## `handleClusterXXXPhase` 与 `ClusterStatus` 的对应关系

### 完整对应表

| Handler | Phase 集合 | 成功（err=nil）→ 进行中状态 | 失败（err≠nil）→ 失败状态 |
|---------|-----------|---------------------------|-------------------------|
| `handleClusterInitPhase` | `ClusterInitPhaseNames`（8 个） | `ClusterInitializing` | `ClusterInitializationFailed` |
| `handleClusterScaleMasterUpPhase` | `ClusterScaleMasterUpPhaseNames` | `ClusterMasterScalingUp` | `ClusterScaleFailed` |
| `handleClusterScaleWorkerUpPhase` | `ClusterScaleWorkerUpPhaseNames` | `ClusterWorkerScalingUp` | `ClusterScaleFailed` |
| `handleClusterScaleMasterDownPhase` | `ClusterScaleMasterDownPhaseNames` | `ClusterMasterScalingDown` | `ClusterScaleFailed` |
| `handleClusterScaleWorkerDownPhase` | `ClusterScaleWorkerDownPhaseNames` | `ClusterWorkerScalingDown` | `ClusterScaleFailed` |
| `handleClusterDeletePhase` | `ClusterDeletePhaseNames` | `ClusterDeleting` | `ClusterDeleteFailed` |
| `handleClusterPausedPhase` | `ClusterPausedPhaseNames` | `ClusterPaused` | `ClusterPauseFailed` |
| `handleClusterDryRunPhase` | `ClusterDryRunPhaseNames` | `ClusterDryRun` | `ClusterDryRunFailed` |
| `handleClusterAddonsPhase` | `ClusterAddonsPhaseNames` | `ClusterDeployingAddon` | `ClusterDeployAddonFailed` |
| `handleClusterUpgradePhase` | `ClusterUpgradePhaseNames` + `DeclarativeClusterUpgradePhaseNames` | `ClusterUpgrading` | `ClusterUpgradeFailed` |
| `handleClusterManagePhase` | `ClusterManagePhaseNames` | `ClusterManaging` | `ClusterManageFailed` |
| **无**（短路） | `CustomSetStatusPhaseNames`（`EnsureCluster`） | `ClusterChecking`（前置 Hook 专门设置） | — |
| **无**（default） | 未匹配的 phase | `ClusterUnknown` | `ClusterUnknown` |

### Phase 集合具体 Phase 明细

> **来源**：`pkg/phaseframe/phases/list.go:87-158`

#### ClusterInitPhaseNames（8 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureFinalizerName` | `"EnsureFinalizer"` | 部署任务创建 |
| 2 | `EnsureCertsName` | `"EnsureCerts"` | 集群证书创建 |
| 3 | `EnsureClusterAPIObjName` | `"EnsureClusterAPIObj"` | ClusterAPI对接 |
| 4 | `EnsureMasterInitName` | `"EnsureMasterInit"` | Master初始化 |
| 5 | `EnsureBKEAgentName` | `"EnsureBKEAgent"` | 推送Agent |
| 6 | `EnsureNodesEnvName` | `"EnsureNodesEnv"` | 节点环境准备 |
| 7 | `EnsureLoadBalanceName` | `"EnsureLoadBalance"` | 集群入口配置 |
| 8 | `EnsureAgentSwitchName` | `"EnsureAgentSwitch"` | Agent监听切换 |

#### ClusterScaleMasterUpPhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureMasterJoinName` | `"EnsureMasterJoin"` | Master加入 |

#### ClusterScaleWorkerUpPhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureWorkerJoinName` | `"EnsureWorkerJoin"` | Worker加入 |

#### ClusterScaleMasterDownPhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureMasterDeleteName` | `"EnsureMasterDelete"` | Master删除 |

#### ClusterScaleWorkerDownPhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureWorkerDeleteName` | `"EnsureWorkerDelete"` | Worker删除 |

#### ClusterDeletePhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureDeleteOrResetName` | `"EnsureDeleteOrReset"` | 集群删除 |

#### ClusterPausedPhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsurePausedName` | `"EnsurePaused"` | 集群管理暂停 |

#### ClusterDryRunPhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureDryRunName` | `"EnsureDryRun"` | DryRun部署 |

#### ClusterAddonsPhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureAddonDeployName` | `"EnsureAddonDeploy"` | 集群组件部署 |

#### ClusterUpgradePhaseNames（5 个，旧 PhaseFlow 升级路径）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureAgentUpgradeName` | `"EnsureAgentUpgrade"` | Agent升级 |
| 2 | `EnsureContainerdUpgradeName` | `"EnsureContainerdUpgrade"` | Containerd升级 |
| 3 | `EnsureMasterUpgradeName` | `"EnsureMasterUpgrade"` | Master升级 |
| 4 | `EnsureWorkerUpgradeName` | `"EnsureWorkerUpgrade"` | Worker升级 |
| 5 | `EnsureComponentUpgradeName` | `"EnsureComponentUpgrade"` | openFuyao核心组件升级 |

#### DeclarativeClusterUpgradePhaseNames（6 个，声明式 DAG 升级路径）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsurePreUpgradeResourcesName` | `"EnsurePreUpgradeResources"` | 升级前资源预创建 |
| 2 | `EnsureAgentUpgradeName` | `"EnsureAgentUpgrade"` | Agent升级 |
| 3 | `EnsureEtcdUpgradeName` | `"EnsureEtcdUpgrade"` | Etcd升级 |
| 4 | `EnsureContainerdUpgradeName` | `"EnsureContainerdUpgrade"` | Containerd升级 |
| 5 | `EnsureMasterUpgradeName` | `"EnsureMasterUpgrade"` | Master升级 |
| 6 | `EnsureWorkerUpgradeName` | `"EnsureWorkerUpgrade"` | Worker升级 |

> **注意**：`handleClusterUpgradePhase` 同时匹配 `ClusterUpgradePhaseNames`（旧路径 5 个）和 `DeclarativeClusterUpgradePhaseNames`（DAG 路径 6 个）。两者有 4 个重叠 Phase（Agent/Containerd/Master/Worker Upgrade），差异为：旧路径含 `EnsureComponentUpgrade`，DAG 路径含 `EnsurePreUpgradeResources` 和 `EnsureEtcdUpgrade`。

#### ClusterManagePhaseNames（1 个）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureClusterManageName` | `"EnsureClusterManage"` | 纳管现有集群 |

#### CustomSetStatusPhaseNames（1 个，短路处理）

| # | Phase 常量 | Phase 值 | 中文名 |
|---|-----------|---------|--------|
| 1 | `EnsureClusterName` | `"EnsureCluster"` | 集群健康检查 |

> **注意**：`EnsureCluster` 不走任何 `handleCluster*Phase` 函数，而是在前置 Hook 中短路设置 `ClusterChecking`，由 `ensure_cluster.go` 的健康检查逻辑决定最终设置 `Healthy` 或 `Unhealthy`。

#### 未纳入任何 Phase 集合的 Phase

以下 Phase 存在于 `DeployPhases`/`PostDeployPhases` 注册列表中，但**不在任何 `ClusterXxxPhaseNames` 集合中**，因此它们在 `calculateClusterStatusByPhase` 中走 default 分支，设置 `ClusterUnknown`：

| Phase 常量 | Phase 值 | 中文名 | 所属注册列表 |
|-----------|---------|--------|-------------|
| `EnsureNodesPostProcessName` | `"EnsureNodesPostProcess"` | 后置脚本处理 | `DeployPhases` |
| `EnsureClusterAPIManagerManifestName` | `"EnsureClusterAPIManagerManifest"` | Cluster-API Manager部署 | `PostDeployPhases` |
| `EnsureProviderSelfUpgradeName` | `"EnsureProviderSelfUpgrade"` | provider自升级 | `PostDeployPhases` |

> **注意**：`EnsureProviderSelfUpgrade` 虽然在 `PostDeployPhases` 中注册，但不在 `ClusterUpgradePhaseNames` 或 `DeclarativeClusterUpgradePhaseNames` 中。在升级场景中它由 DAG 调度器直接执行，不经过 `calculateClusterStatusByPhase` 的状态映射。

### 共性规律

所有 handler 的逻辑结构完全一致：

```go
func handleClusterXxxPhase(ctx *phaseframe.PhaseContext, err error) {
    if err != nil {
        ctx.BKECluster.Status.ClusterStatus = <XXXFailed>  // 失败状态
    } else {
        ctx.BKECluster.Status.ClusterStatus = <XXXPing>    // 进行中状态
    }
}
```

差异仅在**状态常量**和**适用的 phase 集合**。

### 失败状态命名规律

| 场景 | 失败状态 | 命名规律 |
|------|---------|---------|
| 初始化 | `ClusterInitializationFailed` | `Cluster` + 场景 + `Failed` |
| 扩容/缩容（4 种） | `ClusterScaleFailed` | **共用同一个失败状态** |
| 删除 | `ClusterDeleteFailed` | `Cluster` + 场景 + `Failed` |
| 暂停 | `ClusterPauseFailed` | `Cluster` + 场景 + `Failed` |
| DryRun | `ClusterDryRunFailed` | `Cluster` + 场景 + `Failed` |
| Addons | `ClusterDeployAddonFailed` | `Cluster` + 场景 + `Failed` |
| 升级 | `ClusterUpgradeFailed` | `Cluster` + 场景 + `Failed` |
| 纳管 | `ClusterManageFailed` | `Cluster` + 场景 + `Failed` |

**特殊点**：4 种扩缩容场景（Master/Worker 扩容/缩容）的**进行中状态不同**（`MasterScalingUp`/`WorkerScalingUp`/`MasterScalingDown`/`WorkerScalingDown`），但**失败状态共用** `ClusterScaleFailed`。

### 与 StatusManager 的关联

失败状态是否触发 StatusManager 的失败计数，取决于**状态名是否以 `Failed` 结尾**：

| 失败状态 | 以 `Failed` 结尾 | StatusManager 行为 |
|---------|----------------|-------------------|
| `ClusterInitializationFailed` | 是 | 计数++，可能伪装 |
| `ClusterScaleFailed` | 是 | 计数++，可能伪装 |
| `ClusterDeleteFailed` | 是 | 计数++，可能伪装 |
| `ClusterUpgradeFailed` | 是 | 计数++，可能伪装 |
| `ClusterManageFailed` | 是 | 计数++，可能伪装 |
| `ClusterPauseFailed` | 是 | 计数++，可能伪装 |
| `ClusterDryRunFailed` | 是 | 计数++，可能伪装 |
| `ClusterDeployAddonFailed` | 是 | 计数++，可能伪装 |
| `ClusterUnhealthy`（EnsureCluster） | **否** | **不计数**，重置计数 |

**关键**：除 `EnsureCluster` 的 `ClusterUnhealthy` 外，所有 handler 设置的失败状态都以 `Failed` 结尾，都会触发 StatusManager 的失败计数与状态伪装机制。

### 前置 Hook 与后置 Hook 的状态对照

由于前置 Hook 传入 `err=nil`，只走"成功分支"设置"进行中"状态；后置 Hook 传入真实 err，决定最终状态：

| Handler | 前置 Hook 设置（err=nil） | 后置 Hook 设置（err≠nil） | 后置 Hook 设置（err=nil） |
|---------|--------------------------|--------------------------|--------------------------|
| `handleClusterInitPhase` | `ClusterInitializing` | `ClusterInitializationFailed` | `ClusterInitializing` |
| `handleClusterUpgradePhase` | `ClusterUpgrading` | `ClusterUpgradeFailed` | `ClusterUpgrading` |
| `handleClusterDeletePhase` | `ClusterDeleting` | `ClusterDeleteFailed` | `ClusterDeleting` |
| ...其他同理 | 进行中状态 | 失败状态 | 进行中状态 |

**注意**：后置 Hook 在 phase 成功时设置的"进行中"状态与前置 Hook 相同（如 `ClusterUpgrading`），表示"该 phase 成功完成，但整个升级流程尚未结束"。

### 状态转换流程示例（以升级为例）

```
phase 执行前:
  → 前置 Hook: calculateClusterStatusByPhase(phase, nil)
  → handleClusterUpgradePhase(ctx, nil)
  → ClusterStatus = ClusterUpgrading（升级中）

phase 执行:
  → EnsureMasterUpgrade.Execute()

phase 执行后:
  → 后置 Hook: calculateClusterStatusByPhase(phase, err)
  → if err != nil:
      handleClusterUpgradePhase(ctx, err)
      ClusterStatus = ClusterUpgradeFailed（升级失败）
      → 触发 StatusManager 失败计数
  → else:
      handleClusterUpgradePhase(ctx, nil)
      ClusterStatus = ClusterUpgrading（继续升级中）
      → 触发 StatusManager 记录正常状态
```

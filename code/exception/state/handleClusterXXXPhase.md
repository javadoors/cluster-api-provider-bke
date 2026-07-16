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

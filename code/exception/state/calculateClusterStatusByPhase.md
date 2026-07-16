# calculateClusterStatusByPhase

## `calculateClusterStatusByPhase` 作用（精简版）

**位置**：[phase_flow.go:322-356](file:///cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L322-L356)

### 核心定位

**phase 名称 → 集群状态的分发器**，被前置 Hook 和后置 Hook 共用：

```go
func calculateClusterStatusByPhase(phase phaseframe.Phase, err error) error {
    switch {
    case phaseName.In(CustomSetStatusPhaseNames): return nil  // 短路
    case phaseName.In(ClusterInitPhaseNames):       handleClusterInitPhase(ctx, err)
    case phaseName.In(ClusterUpgradePhaseNames) || phaseName.In(DeclarativeClusterUpgradePhaseNames):
                                                    handleClusterUpgradePhase(ctx, err)
    // ... 共 11 个分支
    default: ctx.BKECluster.Status.ClusterStatus = bkev1beta1.ClusterUnknown
    }
}
```

### 三个关键设计

| 设计 | 说明 |
|------|------|
| **短路排除** | `EnsureCluster` 属 `CustomSetStatusPhaseNames`，直接 `return nil`，由前置 Hook 专门设为 `ClusterChecking` |
| **双集合合并** | 升级分支合并 `ClusterUpgradePhaseNames`（传统）+ `DeclarativeClusterUpgradePhaseNames`（DAG），共享 `handleClusterUpgradePhase` |
| **default 兜底** | 未匹配的 phase 设为 `ClusterUnknown`，后置 Hook 因 `ClusterStatus == Unknown` 不设置注解，StatusManager 不记录 |

### 前置 vs 后置 Hook 的调用差异

两者调用同一个分发器，但传入的 `err` 不同：

| Hook | err | 效果 |
|------|-----|------|
| 前置（phase 执行前） | **恒为 `nil`** | 只走"成功分支"，设置"进行中"状态（如 `Upgrading`） |
| 后置（phase 执行后） | 真实执行结果 | 按结果设置"成功/失败"最终状态（如 `Upgrading`/`UpgradeFailed`） |

**关键**：前置 Hook 传入 `err=nil`，永远不会走失败分支，仅标记"开始"；后置 Hook 传入真实 err，确定"最终状态"并设置注解触发 StatusManager 记录。

### 分发分支一览

| 分支 | Handler | 进行中 → 失败状态 |
|------|---------|------------------|
| 初始化（8 phase） | `handleClusterInitPhase` | `ClusterInitializing` → `ClusterInitializationFailed` |
| Master 扩容 | `handleClusterScaleMasterUpPhase` | `ClusterMasterScalingUp` → `ClusterScaleFailed` |
| Worker 扩容 | `handleClusterScaleWorkerUpPhase` | `ClusterWorkerScalingUp` → `ClusterScaleFailed` |
| 删除 | `handleClusterDeletePhase` | `ClusterDeleting` → `ClusterDeleteFailed` |
| 升级（双集合） | `handleClusterUpgradePhase` | `ClusterUpgrading` → `ClusterUpgradeFailed` |
| 纳管 | `handleClusterManagePhase` | `ClusterManaging` → `ClusterManageFailed` |
| 自定义（EnsureCluster） | 无 | 由前置 Hook 设为 `ClusterChecking` |
| default | 无 | `ClusterUnknown` |

### 本质

**集中映射 + 前后复用**：所有 phase 的状态设置逻辑集中在一处，通过 `err` 参数区分"进行中"（前置）和"最终状态"（后置），避免分散到各 phase 内部。

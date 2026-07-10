# StatusManager 的作用

## StatusManager 的作用

**位置**：[pkg/statusmanage/statusmanager.go](file:///cluster-api-provider-bke/pkg/statusmanage/statusmanager.go)

StatusManager 是 BKECluster 控制器的**状态管理单例**，核心职责是**控制失败状态的重试节奏与上限**，实现"软失败"机制。

### 一、核心定位

```
// 用来控制 BKECluster 的失败状态，使用单例模式运行，在BKECluster更新的末端调用
type StatusManager struct {
    BKEClusterStatusMap map[string]*StatusRecord       // 集群级状态记录
    BKENodesStatusMap   map[string]map[string]*StatusRecord  // 节点级状态记录
}
```

全局单例 `BKEClusterStatusManager` 在控制器启动时创建，**跨 reconcile 持久化失败计数**（内存态，非 CRD 状态）。

### 二、四大核心作用

#### 1. 失败计数（L2 重试上限控制）

**记录规则**（[statusmanager.go:172-186](file:///cluster-api-provider-bke/pkg/statusmanage/statusmanager.go#L172-L186)）：

| 事件 | 计数动作 |
|------|----------|
| 状态以 `Failed` 结尾且与上次一致 | `StatusCount++` |
| 状态以 `Failed` 结尾但与上次不同 | 重置 + 记录新失败状态 + `StatusCount=1` |
| 正常状态（非 Failed） | 重置计数 |

**阈值**：`ReconcileAllowedFailedCount`（默认 10，可通过 `ALLOWED_FAILED_COUNT` 环境变量覆盖）

#### 2. 状态伪装（软失败设计）

**关键逻辑**（[statusmanager.go:195-198](file:///cluster-api-provider-bke/pkg/statusmanage/statusmanager.go#L195-L198)）：

```go
if sr.AllowFailed() {  // StatusCount < 10
    bkeCluster.Status.ClusterStatus = confv1beta1.ClusterStatus(sr.LatestNormalState)
    sr.NeedRequeue = true
}
```

**效果**：失败 10 次内，将 `ClusterStatus` 改回升级前的正常状态（如 `Ready`），对外表现"升级未发生"，但后台持续重试。

代码注释明确说明（[statusmanager.go:187-194](file:///cluster-api-provider-bke/pkg/statusmanage/statusmanager.go#L187-L194)）：
> 在一定失败次数内对其状态进行修正，表现出的效果为，实际执行失败但显示正常，控制器重试一定次数后停止重试，并暂停对该 bkeCluster 的调谐，直至 spec 被修改

#### 3. 超限终止与最终状态设置

超过阈值后（[statusmanager.go:200-215](file:///cluster-api-provider-bke/pkg/statusmanage/statusmanager.go#L200-L215)）：

```go
} else {
    switch sr.CurrentClusterState {
    case bkev1beta1.Upgrading:
        bkeCluster.Status.ClusterHealthState = bkev1beta1.UpgradeFailed
    case bkev1beta1.Deploying:
        bkeCluster.Status.ClusterHealthState = bkev1beta1.DeployFailed
    case bkev1beta1.Managing:
        bkeCluster.Status.ClusterHealthState = bkev1beta1.ManageFailed
    }
    sr.Reset()
    sr.NeedRequeue = false  // 停止自动重试
}
```

**效果**：
- `ClusterHealthState` 设为最终失败状态（如 `UpgradeFailed`）
- `NeedRequeue = false`，停止自动重试
- 调谐暂停，直至 spec 被修改或添加 retry 注解

#### 4. 控制 Requeue 行为

`GetCtrlResult` 方法（[statusmanager.go:64-77](file:///cluster-api-provider-bke/pkg/statusmanage/statusmanager.go#L64-L77)）返回 `ctrl.Result{Requeue: sr.NeedRequeue}`，控制是否继续重试：

| 场景 | NeedRequeue | 效果 |
|------|-------------|------|
| 失败 < 10 次 | true | 继续重试 |
| 失败 ≥ 10 次 | false | 停止重试 |
| 正常状态 | false | 不重试 |
| ClusterPaused | false | 不重试 |

### 三、双层管理（集群级 + 节点级）

StatusManager 同时管理两个层级：

| 层级 | 数据结构 | 触发条件 | 超限处理 |
|------|----------|----------|----------|
| **集群级** | `BKEClusterStatusMap` | `StatusRecordAnnotationKey` 注解存在 | `ClusterHealthState=UpgradeFailed`，停止重试 |
| **节点级** | `BKENodesStatusMap` | `NodeStateNeedRecord` flag 设置 | `NodeFailedFlag` 标记，后续 phase 跳过该节点 |

节点级超限处理（[statusmanager.go:328-336](file:///cluster-api-provider-bke/pkg/statusmanage/statusmanager.go#L328-L336)）：

```go
// 标记失败，这将会让后续所有调谐跳过该节点
bkeNodes.SetNodeState(nodeIP, confv1beta1.NodeState(state))
bkeNodes.MarkNodeStateFlag(nodeIP, bkev1beta1.NodeFailedFlag)
```

### 四、与三层重试机制的关系

StatusManager 是三层重试机制的 **L2 层**：

| 层次 | 机制 | StatusManager 的角色 |
|------|------|---------------------|
| L1 | workqueue 限速器 | 无关（L1 由 controller-runtime 控制） |
| **L2** | **失败计数 + 状态伪装** | **核心实现者** |
| L3 | 手动重试注解 | 提供 `RemoveClusterStatusManagerCache` / `RemoveSingleNodeStatusCache` 清理缓存 |

L3 手动重试时，正是通过调用 StatusManager 的缓存清理方法来重置失败计数，从而恢复重试能力。

### 五、StatusRecord 数据结构

[staterecords.go](file:///cluster-api-provider-bke/pkg/statusmanage/staterecords.go)：

```go
type StatusRecord struct {
    CurrentClusterState confv1beta1.ClusterHealthState  // 当前集群健康状态（用于判断最终失败类型）
    LatestFailedState   string                          // 最近一次失败状态
    LatestNormalState   string                          // 最近一次正常状态（用于伪装）
    StatusCount         int                             // 连续失败次数
    NeedRequeue         bool                            // 是否需要重新入队
}
```

### 六、总结

StatusManager 的本质是**失败重试控制器**，通过内存态的失败计数实现：
1. **限次重试**：10 次内自动重试
2. **状态伪装**：重试期间对外显示正常状态，避免误告警
3. **超限终止**：超过阈值停止重试，暴露真实失败状态
4. **节点隔离**：节点级失败不影响其他节点
5. **手动恢复**：通过清理缓存支持 L3 手动重试

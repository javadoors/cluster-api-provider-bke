# `ensure_worker_delete.go` 业务流程
## `ensure_worker_delete.go` 业务流程梳理
### 一、Phase 定位
`EnsureWorkerDelete` 是 **Worker 节点缩容删除协调器**，负责在集群缩容时，安全地驱逐并删除指定的 Worker 节点。它与 Cluster API 的 MachineDeployment 深度集成，通过操纵 Machine 副本数和删除注解来实现节点生命周期管理。
### 二、核心常量
| 常量 | 值 | 说明 |
|------|------|------|
| `EnsureWorkerDeleteName` | `"EnsureWorkerDelete"` | Phase 名称标识 |
| `WorkerDeleteRequeueAfterSeconds` | `10` | 删除失败后 Requeue 间隔 10 秒 |
| `WorkerDeleteWaitTimeoutMinutes` | `4` | 等待 Machine 删除超时 4 分钟 |
| `WorkerDeletePollIntervalSeconds` | `2` | 轮询 Machine 状态间隔 2 秒 |
### 三、内部状态
```go
type EnsureWorkerDelete struct {
    phaseframe.BasePhase
    machinesAndNodesToDelete     map[string]phaseutil.MachineAndNode  // 本次需要删除的
    machinesAndNodesToWaitDelete map[string]phaseutil.MachineAndNode  // 之前已标记删除、等待完成的
}
```
Phase 在多次 Reconcile 之间维护两个映射表，实现**跨 Reconcile 的删除状态追踪**。
### 四、完整业务流程
```
┌──────────────────────────────────────────────────────────────────┐
│                      NeedExecute() 判断                          │
│                                                                  │
│  路径1: Legacy 模式（预约注解）                                  │
│    ├── GetAppointmentDeletedNodes() 检查预约删除注解             │
│    └── GetNeedDeleteWorkerNodes() → 有节点需删除?                │
│                                                                  │
│  路径2: BKENode 删除模式                                         │
│    ├── getDeleteTargetNodesIfDeployed() 检查集群是否已部署       │
│    └── GetNeedDeleteWorkerNodesWithTargetNodes()                 │
│        → 对比远端集群节点与 BKENode 资源，找出多余节点           │
│                                                                  │
│  任一路径有节点 → PhaseWaiting，需要执行                         │
└──────────────────────┬───────────────────────────────────────────┘
                       │ 需要执行
                       ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Execute() 入口                             │
│  1. reconcileWorkerDelete()  → 执行删除操作                     │
│  2. waitWorkerDelete()       → 等待删除完成                     │
└──────────────────────┬──────────────────────────────────────────┘
                       │
          ┌────────────┴────────────┐
          ▼                         ▼
┌──────────────────────┐  ┌─────────────────────────────┐
│ reconcileWorkerDelete│  │     waitWorkerDelete        │
│   (发起删除)         │  │   (等待删除完成)            │
└──────────────────────┘  └─────────────────────────────┘
```
### 五、Phase 1: `reconcileWorkerDelete()` — 发起删除
```
┌───────────────────────────────────────────────────────────────────┐
│                  reconcileWorkerDelete()                          │
│                                                                   │
│  Step 0: getTargetClusterNodes()                                  │
│    获取远端 K8s 集群的节点列表（用于 BKENode 删除模式）           │
│                                                                   │
│  Step 1: initialSetup()  初始设置                                 │
│    ├── 获取需要删除的 Worker 节点（Legacy 或 BKENode 模式）       │
│    ├── ProcessNodeMachineMapping()  节点-Machine 映射处理         │
│    │   ├── 节点无关联 Machine → 清理残留状态，跳过                │
│    │   ├── Machine 在 Deleting 阶段 → 加入 WaitDeleteMap          │
│    │   ├── Machine 在 Deleted 阶段 → 跳过                         │
│    │   └── Machine 正常运行 → 加入 DeleteMap                      │
│    ├── 获取 MachineDeployment                                     │
│    └── PauseClusterAPIObj()  暂停 MachineDeployment               │
│        （防止缩容期间 CAPI 干扰）                                 │
│                                                                   │
│  Step 2: processDrainAndMark()  驱逐 + 标记删除                   │
│    ├── drainNodes()  逐节点驱逐 Pod                               │
│    │   └── 详见下方                                               │
│    └── markMachinesForDeletion()  标记 Machine 待删除             │
│        └── 详见下方                                               │
│                                                                   │
│  Step 3: finalizeDeletion()  完成删除                             │
│    ├── 计算缩容后副本数: exceptReplicas = current - deleteCount   │
│    ├── ResumeClusterAPIObj()  恢复 MachineDeployment              │
│    │   （同时更新 Replicas 副本数）                               │
│    └── 有无法删除的节点 → RequeueAfter 10s                        │
│                                                                   │
│  ⚠️ defer 回滚: 如果整个流程出错                                  │
│    → 恢复 MachineDeployment 原始副本数                            │
│    → ResumeClusterAPIObj() 恢复运行                               │
└───────────────────────────────────────────────────────────────────┘
```
### 六、节点驱逐详细流程 (`drainNodes`)
```
对 machineToNodeDeleteMap 中每个 Machine-Node 对：
│
├── 1. GetRemoteNodeByBKENode()  获取远端集群 Node 资源
│   ├── Node 不存在(NotFound) → 跳过（仍需删除 Machine）
│   └── 其他错误 → 标记 NodeDeleteFailed，移入 canNotDelete，跳过
│
├── 2. GetPodsForDeletion()  获取节点上需要驱逐的 Pod 列表
│   └── 失败 → 标记 NodeDeleteFailed，移入 canNotDelete，跳过
│
├── 3. 日志记录待驱逐 Pod 列表
│
└── 4. RunNodeDrain()  执行节点驱逐
    ├── 成功 → 保留在 machineToNodeDeleteMap
    └── 失败 → 标记 NodeDeleteFailed，移入 canNotDelete
```
**关键设计**：驱逐失败的节点**不阻塞**其他节点的删除，而是从删除列表中移除，让能删除的节点先删除。
### 七、Machine 标记删除 (`markMachinesForDeletion`)
```
对 drainNodes 成功的 machineToNodeDeleteMap 中每个 Machine：
│
└── MarkMachineForDeletion()
    ├── 添加注解: cluster.x-k8s.io/delete-machine = ""
    ├── c.Update(machine)  更新 Machine 对象
    └── 失败 → 移入 canNotDelete，从 deleteMap 中移除
```
**原理**：Cluster API 的 Machine 控制器会监听带有 `delete-machine` 注解的 Machine，自动执行删除流程。
### 八、Phase 2: `waitWorkerDelete()` — 等待删除完成
```
┌──────────────────────────────────────────────────────────────────┐
│                    waitWorkerDelete()                             │
│                                                                   │
│  Step 1: prepareMachinesAndNodesToWaitDelete()                   │
│    合并 machinesAndNodesToWaitDelete + machinesAndNodesToDelete   │
│    （将之前等待中的 + 本次新标记的合并）                           │
│                                                                   │
│  Step 2: waitForMachinesDelete()  轮询等待 Machine 删除          │
│    ├── 超时: 4 分钟                                               │
│    ├── 轮询间隔: 2 秒                                             │
│    └── 对每个 Machine:                                            │
│        ├── Get(machine) → NotFound → 已删除，记录成功            │
│        ├── 检查 DrainingSucceededCondition                        │
│        │   └── False + DrainingFailed → 打印警告                  │
│        ├── 检查 VolumeDetachSucceededCondition                    │
│        │   └── False → 打印信息（卷分离中）                       │
│        ├── 检查 MachineNodeHealthyCondition                       │
│        │   └── False + DeletionFailed → 打印警告                  │
│        └── 全部删除完成 → 返回 successDeletedNode                │
│                                                                   │
│  Step 3: processSuccessfulDeletions()  清理已删除节点            │
│    └── 对每个成功删除的节点:                                      │
│        └── cleanupNodePods()                                      │
│            ├── DeleteBKENodeForCluster()  从 BKE 中删除节点记录   │
│            ├── RemoveSingleNodeStatusCache()  清除状态缓存        │
│            └── 强制删除节点上残留 Pod (gracePeriodSeconds=0)      │
│                                                                   │
│  Step 4: SyncStatusUntilComplete()  持久化状态                    │
└──────────────────────────────────────────────────────────────────┘
```
### 九、两种节点删除模式
#### 模式1: Legacy 预约注解模式
```
用户通过注解指定要删除的节点 IP:
  annotation: "bke.bocloud.com/appointment-deleted-nodes" = "ip1,ip2"

流程:
  GetAppointmentDeletedNodes() → 解析注解获取 IP 列表
  GetNeedDeleteNodes().Worker() → 获取当前可删除的 Worker 节点
  ComputeFinalDeleteNodes() → 取交集，确定最终删除列表
```
#### 模式2: BKENode 资源删除模式
```
用户删除 BKENode CR 资源，系统自动检测并删除对应节点:
  远端集群节点列表 vs BKENode 资源列表
  → 找出在远端集群中存在但 BKENode 中不存在的节点
  → 这些节点需要被删除

流程:
  getTargetClusterNodes() → 获取远端集群实际节点
  GetNeedDeleteNodesFromTargetNodes() → 对比差异
  → 返回需要删除的 Worker 节点
```
### 十、关键设计要点
#### 1. MachineDeployment 暂停/恢复机制
```
PauseClusterAPIObj()  → 添加 "cluster.x-k8s.io/paused" 注解
                        CAPI 控制器不再处理该 MachineDeployment

... 修改 Replicas、标记 Machine 删除 ...

ResumeClusterAPIObj() → 移除 "paused" 注解
                        CAPI 控制器恢复处理，按新 Replicas 调和
```
**目的**：防止在标记 Machine 删除和缩容 Replicas 之间，CAPI 控制器产生竞争条件。
#### 2. Defer 回滚机制
```go
defer func() {
    if err != nil {
        scope.MachineDeployment.Spec.Replicas = currentReplicas
        phaseutil.ResumeClusterAPIObj(ctx, c, scope.MachineDeployment)
    }
}()
```
如果整个 `reconcileWorkerDelete` 流程出错，自动回滚 MachineDeployment 副本数并恢复运行，确保集群状态一致性。
#### 3. 驱逐失败的容错策略
驱逐失败的节点从删除列表中移除，不阻塞其他节点的正常删除。但如果有**任何**节点无法删除，Phase 会返回错误并 Requeue，等待下次重试。
#### 4. 跨 Reconcile 状态追踪
通过 `machinesAndNodesToWaitDelete` 和 `machinesAndNodesToDelete` 两个成员变量，Phase 在多次 Reconcile 之间追踪删除状态：
- `WaitDeleteMap`：之前已标记删除但尚未完成的 Machine
- `DeleteMap`：本次新标记删除的 Machine

在 `waitWorkerDelete` 阶段，两者合并后统一等待。
#### 5. 残留 Pod 强制清理
节点删除后，可能仍有 DaemonSet Pod 或 Terminating 状态的 Pod 残留。`cleanupNodePods` 使用 `gracePeriodSeconds=0` 强制删除这些 Pod，确保集群资源干净。
#### 6. Machine 条件监控
在等待 Machine 删除期间，持续监控三个关键条件：
- **DrainingSucceededCondition**：Pod 驱逐状态
- **VolumeDetachSucceededCondition**：卷分离状态
- **MachineNodeHealthyCondition**：节点健康状态

这些信息帮助运维人员了解删除进度和阻塞原因。
### 十一、状态流转图
```
节点状态流转：
  NodeReady
       │
       ▼ (开始驱逐)
  NodeDraining
       │
       ├── 驱逐成功 ▶ 标记 Machine delete-machine 注解
       │                  │
       │                  ▼ (CAPI 处理删除)
       │              Machine Phase: Deleting → Deleted
       │                  │
       │                  ▼ (Machine NotFound)
       │              cleanupNodePods() → 清理残留
       │                  │
       │                  ▼
       │              节点完全移除
       │
       └── 驱逐失败 ▶ NodeDeleteFailed
                          │
                          ▼ (下次 Requeue 重试)
                      重新进入删除流程

MachineDeployment 状态流转：
  Running (Replicas=N)
       │
       ▼ (PauseClusterAPIObj)
  Paused (Replicas=N)
       │
       ▼ (标记删除 + 缩容)
  Paused (Replicas=N-K)
       │
       ▼ (ResumeClusterAPIObj)
  Running (Replicas=N-K)
       │
       ▼ (出错回滚)
  Running (Replicas=N)  ← 恢复原状
```
### 十二、与 Master 删除的对比（参考 `ensure_master_delete.go`）
| 维度 | Worker 删除 | Master 删除 |
|------|------------|------------|
| 删除触发 | 预约注解 / BKENode 删除 | 预约注解 / BKENode 删除 |
| CAPI 对象 | MachineDeployment | KubeadmControlPlane |
| 驱逐方式 | `kubedrain.RunNodeDrain` | 类似 |
| 副本数管理 | 修改 MachineDeployment.Replicas | 修改 KCP.Replicas |
| 暂停机制 | Pause MachineDeployment | Pause KCP |
| 回滚机制 | ✅ Defer 自动回滚 | 类似 |
| 残留清理 | ✅ 强制删除 Pod + 清除状态缓存 | 类似 |
### 十三、潜在改进点
1. **并行驱逐**：当前逐节点串行驱逐，对于大量缩容场景效率较低，可以考虑并行驱逐多个节点。
2. **部分失败处理**：驱逐失败的节点仅 Requeue 等待，缺少人工干预接口（如强制删除选项）。
3. **超时配置硬编码**：4 分钟等待超时对于有大量 PV 的节点可能不够，应支持动态配置。
4. **状态持久化**：`machinesAndNodesToDelete` 和 `machinesAndNodesToWaitDelete` 存储在内存中，Controller 重启后会丢失，需要重新计算。
5. **BKENode 删除模式的竞态**：在获取远端集群节点列表和执行删除之间，远端集群状态可能已变化，缺少乐观锁或版本校验机制。
        

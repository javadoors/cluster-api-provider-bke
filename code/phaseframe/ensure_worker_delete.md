# `ensure_worker_delete.go` 功能清单与流程
          
## `ensure_worker_delete.go` 功能清单与流程详解

### 一、核心作用

#### 1. Worker 节点缩容

```
┌─────────────────────────────────────────────────────────────────────┐
│  EnsureWorkerDelete Phase                                           │
│  Worker 节点缩容（删除 Worker 节点）                                   │
└─────────────────────────────────────────────────────────────────────┘

核心功能：
• 确定需要删除的 Worker 节点
• 驱逐节点上的 Pod（Drain）
• 暂停 MachineDeployment Controller
• 标记 Machine 待删除
• 缩容 MachineDeployment Replicas
• 等待 Machine 删除完成
• 清理节点相关资源
```

### 二、Phase 结构

```go
type EnsureWorkerDelete struct {
    phaseframe.BasePhase
    machinesAndNodesToDelete     map[string]phaseutil.MachineAndNode  // 待删除的 Machine
    machinesAndNodesToWaitDelete map[string]phaseutil.MachineAndNode  // 等待删除的 Machine
}
```

### 三、执行条件判断

```go
func (e *EnsureWorkerDelete) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. 基础判断
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }

    // 2. 尝试传统模式（通过预约注解）
    nodes := phaseutil.GetNeedDeleteWorkerNodes(e.Ctx, e.Ctx.Client, new)
    if nodes.Length() > 0 {
        e.SetStatus(bkev1beta1.PhaseWaiting)
        return true
    }

    // 3. 尝试 BKENode 删除模式
    targetNodes, ok := getDeleteTargetNodesIfDeployed(e.Ctx, new)
    if !ok {
        return false
    }

    nodes = phaseutil.GetNeedDeleteWorkerNodesWithTargetNodes(e.Ctx, e.Ctx.Client, new, targetNodes)
    if nodes.Length() == 0 {
        return false
    }

    e.SetStatus(bkev1beta1.PhaseWaiting)
    return true
}
```

**两种删除模式**：

| 模式 | 说明 | 判断方式 |
|------|------|---------|
| **传统模式** | 通过预约注解标记删除 | `GetNeedDeleteWorkerNodes()` |
| **BKENode 删除模式** | 通过 BKENode 删除标记 | `GetNeedDeleteWorkerNodesWithTargetNodes()` |

### 四、执行流程

#### Execute 主流程

```go
func (e *EnsureWorkerDelete) Execute() (ctrl.Result, error) {
    // 1. 执行 Worker 删除协调
    res, err := e.reconcileWorkerDelete()
    if err != nil {
        return res, err
    }

    // 2. 等待 Worker 删除完成
    return res, e.waitWorkerDelete()
}
```

### 五、详细流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│  Execute()                                                          │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  1. reconcileWorkerDelete()                                         │
│     • 初始设置（获取节点、暂停 MD）                                      │
│     • 驱逐节点 Pod（Drain）                                           │
│     • 标记 Machine 待删除                                             │
│     • 缩容 MachineDeployment Replicas                                │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  2. waitWorkerDelete()                                              │
│     • 等待 Machine 删除完成                                          │
│     • 清理节点相关资源                                                │
│     • 更新 BKECluster 状态                                           │
└─────────────────────────────────────────────────────────────────────┘
```

### 六、核心功能详解

#### 1. reconcileWorkerDelete() - Worker 删除协调

```go
func (e *EnsureWorkerDelete) reconcileWorkerDelete() (ctrl.Result, error) {
    // 1. 获取目标集群节点
    targetNodes, targetErr := e.getTargetClusterNodes(bkeCluster)

    // 2. 初始设置和准备工作
    initialResult := e.initialSetup(initialSetupParams)
    if initialResult.Error != nil {
        return ctrl.Result{}, initialResult.Error
    }

    // 3. 设置回滚逻辑
    defer func() {
        if err != nil {
            scope.MachineDeployment.Spec.Replicas = currentReplicas
            phaseutil.ResumeClusterAPIObj(ctx, c, scope.MachineDeployment)
        }
    }()

    // 4. 处理驱逐和标记
    drainMarkResult := e.processDrainAndMark(drainMarkParams)

    // 5. 完成删除操作
    finalizeResult := e.finalizeDeletion(finalizeParams)

    return finalizeResult.Result, finalizeResult.Error
}
```

#### 2. initialSetup() - 初始设置

```go
func (e *EnsureWorkerDelete) initialSetup(params) InitialSetupResult {
    // 1. 确定需要删除的节点（优先传统模式）
    nodes := phaseutil.GetNeedDeleteWorkerNodes(params.Ctx, params.Client, params.BKECluster)
    if nodes.Length() == 0 && params.TargetNodes != nil {
        nodes = phaseutil.GetNeedDeleteWorkerNodesWithTargetNodes(params.Ctx, params.Client, 
            params.BKECluster, params.TargetNodes)
    }

    // 2. 处理节点与 Machine 映射关系
    nodeMappingResult, err := ProcessNodeMachineMapping(nodeMappingParams)

    // 3. 获取 MachineDeployment
    scope, err := phaseutil.GetClusterAPIAssociateObjs(params.Ctx, params.Client, params.Cluster)

    // 4. 暂停 MachineDeployment Controller
    if err = phaseutil.PauseClusterAPIObj(params.Ctx, params.Client, scope.MachineDeployment); err != nil {
        return InitialSetupResult{Error: err}
    }

    // 5. 保存当前 Replicas
    specCopy := scope.MachineDeployment.Spec.DeepCopy()
    currentReplicas := specCopy.Replicas

    return InitialSetupResult{
        NodeMappingResult: nodeMappingResult,
        Scope:             scope,
        CurrentReplicas:   currentReplicas,
        Error:             nil,
    }
}
```

#### 3. drainNodes() - 驱逐节点 Pod

```go
func (e *EnsureWorkerDelete) drainNodes(params) DrainNodesResult {
    // 1. 创建 Drainer
    drainer := phaseutil.NewDrainer(params.Ctx, clientSet, dynamicClient, true, params.Log)

    // 2. 遍历需要删除的节点
    for machineName, machineAndNode := range machineToNodeDeleteMap {
        // 2.1 获取远程节点
        remoteNode, err := phaseutil.GetRemoteNodeByBKENode(params.Ctx, client, machineAndNode.Node)
        if err != nil {
            if apierrors.IsNotFound(err) {
                // 找不到也需要删除
                continue
            }
            // 标记为无法删除
            canNotDeleteMachinesAndNodes[machineName] = machineAndNode
            delete(machineToNodeDeleteMap, machineName)
        }

        // 2.2 获取需要驱逐的 Pod
        podsList, errs := drainer.GetPodsForDeletion(remoteNode.Name)
        if errs != nil {
            canNotDeleteMachinesAndNodes[machineName] = machineAndNode
            delete(machineToNodeDeleteMap, machineName)
        }

        // 2.3 执行驱逐
        if err := kubedrain.RunNodeDrain(drainer, remoteNode.Name); err != nil {
            canNotDeleteMachinesAndNodes[machineName] = machineAndNode
            delete(machineToNodeDeleteMap, machineName)
        }
    }

    return DrainNodesResult{
        UpdatedMachineToNodeDeleteMap: machineToNodeDeleteMap,
        CanNotDeleteMachinesAndNodes:  canNotDeleteMachinesAndNodes,
    }
}
```
**驱逐流程**：
```
获取需要驱逐的 Pod
        ↓
执行 kubectl drain
        ↓
驱逐成功 → 继续删除
        ↓
驱逐失败 → 标记为无法删除
```

#### 4. markMachinesForDeletion() - 标记 Machine 待删除

```go
func (e *EnsureWorkerDelete) markMachinesForDeletion(params) MarkMachinesForDeletionResult {
    // 遍历需要删除的 Machine
    for machineName, machineAndNode := range finalMachineToNodeDeleteMap {
        machine := machineAndNode.Machine

        // 标记 Machine 待删除
        if err := phaseutil.MarkMachineForDeletion(params.Ctx, params.Client, machine); err != nil {
            // 标记失败，加入无法删除列表
            finalCanNotDeleteMachinesAndNodes[machineName] = machineAndNode
            delete(finalMachineToNodeDeleteMap, machineName)
        }
    }

    return MarkMachinesForDeletionResult{
        FinalMachineToNodeDeleteMap:       finalMachineToNodeDeleteMap,
        FinalCanNotDeleteMachinesAndNodes: finalCanNotDeleteMachinesAndNodes,
    }
}
```

#### 5. finalizeDeletion() - 完成删除操作

```go
func (e *EnsureWorkerDelete) finalizeDeletion(params) FinalizeDeletionResult {
    // 1. 检查是否有无法删除的节点
    req := ctrl.Result{}
    if len(params.MarkResult.FinalCanNotDeleteMachinesAndNodes) > 0 {
        req = ctrl.Result{RequeueAfter: time.Duration(WorkerDeleteRequeueAfterSeconds) * time.Second}
    }

    // 2. 如果没有需要删除的节点，直接返回
    if len(params.MarkResult.FinalMachineToNodeDeleteMap) == 0 {
        return FinalizeDeletionResult{
            Result: req,
            Error:  errors.Errorf("some nodes cannot be completely deleted"),
        }
    }

    // 3. 缩容 MachineDeployment Replicas
    exceptReplicas := *params.CurrentReplicas - int32(len(params.MarkResult.FinalMachineToNodeDeleteMap))
    if exceptReplicas < 0 {
        exceptReplicas = 0  // 不能为负数
    }
    params.Scope.MachineDeployment.Spec.Replicas = &exceptReplicas

    // 4. 恢复 MachineDeployment Controller
    err := phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, params.Scope.MachineDeployment)

    return FinalizeDeletionResult{
        Result: req,
        Error:  err,
    }
}
```

#### 6. waitWorkerDelete() - 等待 Worker 删除完成

```go
func (e *EnsureWorkerDelete) waitWorkerDelete() error {
    // 1. 准备待删除列表
    machinesAndNodesToWaitDelete := e.prepareMachinesAndNodesToWaitDelete()

    // 2. 等待 Machine 删除
    waitResult := e.waitForMachinesDelete(waitParams)
    if waitResult.Error != nil {
        return waitResult.Error
    }

    // 3. 处理成功删除的节点
    return e.processSuccessfulDeletions(processParams)
}
```

#### 7. waitForMachinesDelete() - 等待 Machine 删除

```go
func (e *EnsureWorkerDelete) waitForMachinesDelete(params) WaitMachinesDeleteResult {
    successDeletedNode := map[string]confv1beta1.Node{}

    // 设置超时
    ctxTimeout, cancel := context.WithTimeout(params.Ctx, 
        time.Duration(WorkerDeleteWaitTimeoutMinutes)*time.Minute)
    defer cancel()

    // 轮询检查 Machine 是否已删除
    err := wait.PollImmediateUntil(
        time.Duration(WorkerDeletePollIntervalSeconds)*time.Second, 
        func() (done bool, err error) {
            for machineName, machineWithNode := range params.MachinesAndNodesToWaitDelete {
                if _, ok := successDeletedNode[machineName]; ok {
                    continue  // 已删除，跳过
                }

                machine := machineWithNode.Machine
                if err = params.Client.Get(params.Ctx, util.ObjectKey(machine), machine); err != nil {
                    if apierrors.IsNotFound(err) {
                        // Machine 已删除
                        successDeletedNode[machineName] = machineWithNode.Node
                        continue
                    }
                    return false, err
                }

                // 检查 Machine 的 Condition（驱逐、卷分离、节点健康）
                drainCondition := conditions.Get(machine, clusterv1.DrainingSucceededCondition)
                volumeDetachCondition := conditions.Get(machine, clusterv1.VolumeDetachSucceededCondition)
                nodeHealthyCondition := conditions.Get(machine, clusterv1.MachineNodeHealthyCondition)
            }

            // 检查是否全部删除
            if len(successDeletedNode) != len(params.MachinesAndNodesToWaitDelete) {
                return false, nil
            }
            return true, nil
        }, 
        ctxTimeout.Done())

    return WaitMachinesDeleteResult{
        SuccessDeletedNode: successDeletedNode,
        Error:              err,
    }
}
```
**超时配置**：

| 配置项 | 值 | 说明 |
|--------|-----|------|
| `WorkerDeleteWaitTimeoutMinutes` | 4 分钟 | 等待删除超时时间 |
| `WorkerDeletePollIntervalSeconds` | 2 秒 | 轮询间隔 |
| `WorkerDeleteRequeueAfterSeconds` | 10 秒 | 重排队时间 |

#### 8. processSuccessfulDeletions() - 处理成功删除的节点

```go
func (e *EnsureWorkerDelete) processSuccessfulDeletions(params) error {
    if len(params.SuccessDeletedNode) != 0 {
        // 获取远程客户端
        remoteClient, err := kube.NewRemoteClientByBKECluster(params.Ctx, params.Client, e.Ctx.BKECluster)
        clientSet, _ := remoteClient.KubeClient()

        // 遍历已删除的节点
        for _, node := range params.SuccessDeletedNode {
            // 清理节点上的 Pod
            if err := e.cleanupNodePods(params.Ctx, clientSet, params.BKECluster, node, params.Log); err != nil {
                continue
            }
        }

        return mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster)
    }
    return nil
}
```

#### 9. cleanupNodePods() - 清理节点上的 Pod

```go
func (e *EnsureWorkerDelete) cleanupNodePods(ctx, clientSet, bkeCluster, node, log) error {
    // 1. 从 BKECluster 中删除节点
    e.Ctx.NodeFetcher().DeleteBKENodeForCluster(e.Ctx, bkeCluster, node.IP)

    // 2. 从状态管理器中删除
    statusmanage.BKEClusterStatusManager.RemoveSingleNodeStatusCache(bkeCluster, node.IP)

    // 3. 列出节点上的所有 Pod
    pods, err := clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{
        FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
    })

    // 4. 强制删除 Pod
    for _, pod := range pods.Items {
        err = clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, 
            metav1.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)})
    }

    return nil
}
```
**清理内容**：

| 清理项 | 说明 |
|--------|------|
| `DeleteBKENodeForCluster` | 从 BKECluster 中删除 BKENode |
| `RemoveSingleNodeStatusCache` | 从状态管理器中删除节点状态 |
| `Delete Pod` | 强制删除节点上的 Pod（GracePeriodSeconds=0） |

### 七、Drain 机制详解

#### 1. 为什么需要 Drain？

```
┌─────────────────────────────────────────────────────────────────────┐
│  不 Drain 的问题                                                    │
└─────────────────────────────────────────────────────────────────────┘

直接删除节点：
• Pod 被强制终止
• 数据可能丢失
• 服务中断

┌─────────────────────────────────────────────────────────────────────┐
│  Drain 后的正确流程                                                 │
└─────────────────────────────────────────────────────────────────────┘

Drain 节点：
• 驱逐所有 Pod 到其他节点
• 等待 Pod 迁移完成
• 安全删除节点
```

#### 2. Drain 实现

```go
// 创建 Drainer
drainer := phaseutil.NewDrainer(ctx, clientSet, dynamicClient, true, log)

// 获取需要驱逐的 Pod
podsList, errs := drainer.GetPodsForDeletion(remoteNode.Name)

// 执行驱逐
if err := kubedrain.RunNodeDrain(drainer, remoteNode.Name); err != nil {
    // 驱逐失败
}
```

### 八、Pause/Resume 机制

#### 1. 为什么需要 Pause？

```
┌─────────────────────────────────────────────────────────────────────┐
│  不 Pause 的问题                                                    │
└─────────────────────────────────────────────────────────────────────┘

T1: Phase 标记 Machine-3 待删除
        ↓
T2: MD Controller 立即调谐
        ↓
T3: MD Controller 发现 Replicas=3，但只有 2 个 Machine
        ↓
T4: MD Controller 创建新 Machine-4（错误！）
        ↓
结果：既要删除 Machine-3，又要创建 Machine-4

┌─────────────────────────────────────────────────────────────────────┐
│  Pause 后的正确流程                                                 │
└─────────────────────────────────────────────────────────────────────┘

T1: Phase Pause MD Controller
        ↓
T2: Phase 标记 Machine-3 待删除
        ↓
T3: Phase 设置 Replicas=2
        ↓
T4: Phase Resume MD Controller
        ↓
T5: MD Controller 调谐
        ↓
T6: MD Controller 发现 Replicas=2，删除 Machine-3
        ↓
结果：正确删除 Machine-3
```

### 九、错误处理与回滚

#### 1. defer 回滚机制

```go
defer func() {
    if err != nil {
        log.Debug("Rollback: scale up MachineDeployment replicas to %d.", *currentReplicas)
        scope.MachineDeployment.Spec.Replicas = currentReplicas
        if rollbackErr := phaseutil.ResumeClusterAPIObj(ctx, c, scope.MachineDeployment); rollbackErr != nil {
            log.Error("Rollback MachineDeployment replicas failed. err: %v", rollbackErr)
        }
    }
}()
```
**作用**：
- ✅ 如果删除过程中出现错误，恢复 Replicas 到删除前的值
- ✅ Resume MachineDeployment，使其继续正常工作

### 十、约束与限制

#### 1. 最小副本数限制

```go
exceptReplicas := *params.CurrentReplicas - int32(len(params.MarkResult.FinalMachineToNodeDeleteMap))
if exceptReplicas < 0 {
    exceptReplicas = 0  // 不能为负数
}
```
**约束**：
- ⚠️ **Replicas 不能为负数**：最小为 0

#### 2. 无法删除的节点处理

```go
if len(params.MarkResult.FinalCanNotDeleteMachinesAndNodes) > 0 {
    req = ctrl.Result{RequeueAfter: time.Duration(WorkerDeleteRequeueAfterSeconds) * time.Second}
}
```
**处理方式**：
- ⚠️ **Requeue**：10 秒后重试
- ⚠️ **不影响其他节点**：可以删除的节点继续删除

### 十一、总结

#### 1. 功能清单

| 功能 | 说明 |
|------|------|
| **确定删除节点** | 通过注解或 BKENode 删除标记确定 |
| **处理节点映射** | 建立节点与 Machine 的映射关系 |
| **暂停 MD** | 防止 MD Controller 干扰 |
| **驱逐节点 Pod** | 安全驱逐 Pod 到其他节点 |
| **标记待删除** | 为 Machine 设置删除注解 |
| **缩容 Replicas** | 减少 MachineDeployment 副本数 |
| **等待删除** | 轮询检查 Machine 是否删除 |
| **清理资源** | 删除 BKENode、清理 Pod |

#### 2. 关键特性
- ⚠️ **两种删除模式**：传统模式（注解）和 BKENode 删除模式
- ⚠️ **Drain 机制**：安全驱逐 Pod，避免数据丢失
- ⚠️ **Pause/Resume 机制**：防止 MD Controller 干扰
- ⚠️ **错误回滚**：失败时恢复 Replicas
- ⚠️ **无法删除处理**：Requeue 重试，不影响其他节点
- ⚠️ **资源清理**：删除 BKENode、强制删除 Pod

#### 3. 执行流程

```
NeedExecute 判断
    ↓
reconcileWorkerDelete
    ├── 初始设置（暂停 MD）
    ├── 驱逐节点 Pod
    ├── 标记 Machine 待删除
    ├── 缩容 Replicas
    └── Resume MD
    ↓
waitWorkerDelete
    ├── 等待 Machine 删除
    └── 清理节点资源
```

#### 4. 与 Master 删除的区别

| 区别点 | Master 删除 | Worker 删除 |
|--------|------------|------------|
| **Drain** | 无 | 有（安全驱逐 Pod） |
| **最小副本数** | 1 | 0 |
| **CAPI 对象** | KubeadmControlPlane | MachineDeployment |
| **Condition 检查** | 无 | 检查 DrainingSucceeded、VolumeDetach、NodeHealthy |


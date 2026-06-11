# `ensure_master_delete.go` 功能清单与流程
          
## `ensure_master_delete.go` 功能清单与流程详解

### 一、核心作用

#### 1. Master 节点缩容

```
┌─────────────────────────────────────────────────────────────────────┐
│  EnsureMasterDelete Phase                                          │
│  Master 节点缩容（删除 Master 节点）                                 │
└─────────────────────────────────────────────────────────────────────┘

核心功能：
• 确定需要删除的 Master 节点
• 暂停 KCP Controller
• 标记 Machine 待删除
• 缩容 KCP Replicas
• 等待 Machine 删除完成
• 清理节点相关资源
```

### 二、Phase 结构

```go
type EnsureMasterDelete struct {
    phaseframe.BasePhase
    machinesAndNodesToDelete     map[string]phaseutil.MachineAndNode  // 待删除的 Machine
    machinesAndNodesToWaitDelete map[string]phaseutil.MachineAndNode  // 等待删除的 Machine
}
```

### 三、执行条件判断

```go
func (e *EnsureMasterDelete) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. 基础判断
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }

    // 2. 尝试传统模式（通过预约注解）
    nodes := phaseutil.GetNeedDeleteMasterNodes(e.Ctx, e.Ctx.Client, new)
    if nodes.Length() > 0 {
        e.SetStatus(bkev1beta1.PhaseWaiting)
        return true
    }

    // 3. 尝试 BKENode 删除模式
    targetNodes, ok := getDeleteTargetNodesIfDeployed(e.Ctx, new)
    if !ok {
        return false
    }

    nodes = phaseutil.GetNeedDeleteMasterNodesWithTargetNodes(e.Ctx, e.Ctx.Client, new, targetNodes)
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
| **传统模式** | 通过预约注解标记删除 | `GetNeedDeleteMasterNodes()` |
| **BKENode 删除模式** | 通过 BKENode 删除标记 | `GetNeedDeleteMasterNodesWithTargetNodes()` |

### 四、执行流程

#### Execute 主流程

```go
func (e *EnsureMasterDelete) Execute() (ctrl.Result, error) {
    // 1. 执行 Master 删除协调
    if err := e.reconcileMasterDelete(); err != nil {
        return ctrl.Result{}, err
    }

    // 2. 等待 Master 删除完成
    return ctrl.Result{}, e.waitMasterDelete()
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
│  1. reconcileMasterDelete()                                         │
│     • 确定需要删除的节点                                             │
│     • 处理节点与 Machine 映射                                       │
│     • 暂停 KCP Controller                                          │
│     • 标记 Machine 待删除                                           │
│     • 缩容 KCP Replicas                                            │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  2. waitMasterDelete()                                              │
│     • 等待 Machine 删除完成                                         │
│     • 清理节点相关资源                                               │
│     • 更新 BKECluster 状态                                          │
└─────────────────────────────────────────────────────────────────────┘
```

### 六、核心功能详解

#### 1. reconcileMasterDelete() - Master 删除协调

```go
func (e *EnsureMasterDelete) reconcileMasterDelete() error {
    // 1. 获取目标集群节点
    targetNodes, targetErr := e.getTargetClusterNodes(bkeCluster)

    // 2. 确定需要删除的节点（优先传统模式）
    nodes := phaseutil.GetNeedDeleteMasterNodes(ctx, c, bkeCluster)
    if nodes.Length() == 0 && targetNodes != nil {
        nodes = phaseutil.GetNeedDeleteMasterNodesWithTargetNodes(ctx, c, bkeCluster, targetNodes)
    }

    // 3. 处理节点与 Machine 映射关系
    params := ProcessNodeMachineMappingParams{
        Ctx:               ctx,
        Client:            c,
        BKECluster:        bkeCluster,
        Nodes:             nodes,
        Log:               log,
        NodeDeletedReason: constant.MasterDeletedReason,
        NodeJoinedReason:  constant.MasterJoinedReason,
    }
    result, err := ProcessNodeMachineMapping(params)

    // 4. 暂停并缩容控制平面
    pauseParams := PauseAndScaleDownControlPlaneParams{
        Ctx:        ctx,
        Client:     c,
        BKECluster: bkeCluster,
        DeleteMap:  result.DeleteMap,
        Log:        log,
    }
    return e.pauseAndScaleDownControlPlane(pauseParams)
}
```

#### 2. pauseAndScaleDownControlPlane() - 暂停并缩容控制平面

```go
func (e *EnsureMasterDelete) pauseAndScaleDownControlPlane(params) error {
    // 1. 获取 KCP
    scope, err := phaseutil.GetClusterAPIAssociateObjs(ctx, c, e.Ctx.Cluster)

    // 2. 暂停 KCP Controller
    if err = phaseutil.PauseClusterAPIObj(ctx, c, scope.KubeadmControlPlane); err != nil {
        return err
    }

    // 3. 设置 defer 回滚（失败时恢复 Replicas）
    defer func() {
        if err != nil {
            scope.KubeadmControlPlane.Spec.Replicas = currentReplicas
            phaseutil.ResumeClusterAPIObj(ctx, c, scope.KubeadmControlPlane)
        }
    }()

    // 4. 标记 Machine 待删除
    for _, machineAndNode := range deleteMap {
        machine := machineAndNode.Machine
        if err = phaseutil.MarkMachineForDeletion(ctx, c, machine); err != nil {
            delete(deleteMap, machine.Name)
        }
    }

    // 5. 缩容 KCP Replicas
    exceptReplicas := *currentReplicas - int32(len(deleteMap))
    if exceptReplicas < 1 {
        exceptReplicas = 1  // 至少保留 1 个 Master
    }
    scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas

    // 6. 恢复 KCP Controller
    if err = phaseutil.ResumeClusterAPIObj(ctx, c, scope.KubeadmControlPlane); err != nil {
        return err
    }

    return nil
}
```

**流程**：
```
获取 KCP
    ↓
暂停 KCP Controller
    ↓
标记 Machine 待删除
    ↓
缩容 KCP Replicas
    ↓
恢复 KCP Controller
    ↓
KCP Controller 删除 Machine
```

#### 3. waitMasterDelete() - 等待 Master 删除完成

```go
func (e *EnsureMasterDelete) waitMasterDelete() error {
    // 1. 准备待删除列表
    machinesAndNodesToWaitDelete := e.prepareMachinesAndNodesToWaitDelete()

    // 2. 等待 Machine 删除完成
    params := WaitForMachinesDeleteParams{
        Ctx:                          ctx,
        Client:                       c,
        MachinesAndNodesToWaitDelete: machinesAndNodesToWaitDelete,
        Log:                          log,
    }
    successDeletedNode, err := e.waitForMachinesDelete(params)

    // 3. 清理已删除节点的资源
    if len(successDeletedNode) != 0 {
        cleanupParams := CleanupDeletedNodePodsParams{
            Ctx:                ctx,
            Client:             c,
            BKECluster:         bkeCluster,
            SuccessDeletedNode: successDeletedNode,
        }
        return e.cleanupDeletedNodePods(cleanupParams)
    }

    return nil
}
```

#### 4. waitForMachinesDelete() - 等待 Machine 删除完成

```go
func (e *EnsureMasterDelete) waitForMachinesDelete(params) (map[string]confv1beta1.Node, error) {
    successDeletedNode := map[string]confv1beta1.Node{}

    // 设置超时
    ctxTimeout, cancel := context.WithTimeout(ctx, 
        time.Duration(WaitMasterDeleteTimeoutMinutes)*time.Minute)
    defer cancel()

    // 轮询检查 Machine 是否已删除
    err := wait.PollImmediateUntil(
        time.Duration(WaitMasterDeletePollIntervalSeconds)*time.Second, 
        func() (done bool, err error) {
            for machineName, machineWithNode := range machinesAndNodesToWaitDelete {
                if _, ok := successDeletedNode[machineName]; ok {
                    continue  // 已删除，跳过
                }

                machine := machineWithNode.Machine
                if err = c.Get(ctx, util.ObjectKey(machine), machine); err != nil {
                    if apierrors.IsNotFound(err) {
                        // Machine 已删除
                        log.Info("Machine %s has been deleted", utils.ClientObjNS(machine))
                        successDeletedNode[machineName] = machineWithNode.Node
                        continue
                    }
                    return false, err
                }
            }

            // 检查是否全部删除
            if len(successDeletedNode) != len(machinesAndNodesToWaitDelete) {
                return false, nil
            }
            return true, nil
        }, 
        ctxTimeout.Done())

    return successDeletedNode, err
}
```

**超时配置**：

| 配置项 | 值 | 说明 |
|--------|-----|------|
| `WaitMasterDeleteTimeoutMinutes` | 4 分钟 | 等待删除超时时间 |
| `WaitMasterDeletePollIntervalSeconds` | 2 秒 | 轮询间隔 |

#### 5. cleanupDeletedNodePods() - 清理已删除节点的资源

```go
func (e *EnsureMasterDelete) cleanupDeletedNodePods(params) error {
    // 1. 获取远程客户端
    remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, e.Ctx.BKECluster)
    clientSet, _ := remoteClient.KubeClient()

    // 2. 遍历已删除的节点
    for _, node := range successDeletedNode {
        // 2.1 从 BKECluster 中删除节点
        e.Ctx.NodeFetcher().DeleteBKENodeForCluster(params.Ctx, bkeCluster, node.IP)

        // 2.2 从状态管理器中删除
        statusmanage.BKEClusterStatusManager.RemoveSingleNodeStatusCache(bkeCluster, node.IP)

        // 2.3 列出节点上的所有 Pod
        pods, err := clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{
            FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
        })

        // 2.4 强制删除 Pod
        for _, pod := range pods.Items {
            err = clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, 
                metav1.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)})
        }
    }

    // 3. 更新 BKECluster 状态
    return mergecluster.SyncStatusUntilComplete(c, bkeCluster)
}
```

**清理内容**：

| 清理项 | 说明 |
|--------|------|
| `DeleteBKENodeForCluster` | 从 BKECluster 中删除 BKENode |
| `RemoveSingleNodeStatusCache` | 从状态管理器中删除节点状态 |
| `Delete Pod` | 强制删除节点上的 Pod（GracePeriodSeconds=0） |

### 七、删除模式详解

#### 1. 传统模式（预约注解）

```go
nodes := phaseutil.GetNeedDeleteMasterNodes(e.Ctx, e.Ctx.Client, bkeCluster)
```
**判断依据**：
```yaml
BKENode:
  metadata:
    annotations:
      bke.bocloud.com/delete-appointment: "true"  # ← 预约删除标记
```

#### 2. BKENode 删除模式

```go
targetNodes, ok := getDeleteTargetNodesIfDeployed(e.Ctx, new)
nodes = phaseutil.GetNeedDeleteMasterNodesWithTargetNodes(e.Ctx, e.Ctx.Client, new, targetNodes)
```

**判断依据**：
```
比较 BKECluster.Spec.Nodes.Master 与目标集群实际节点

示例：
BKECluster.Spec.Nodes.Master = [node1, node2]  # 期望 2 个 Master
目标集群实际 Master = [node1, node2, node3]    # 实际 3 个 Master

结果：node3 需要删除
```

### 八、Pause/Resume 机制

#### 1. 为什么需要 Pause？

```
┌─────────────────────────────────────────────────────────────────────┐
│  不 Pause 的问题                                                     │
└─────────────────────────────────────────────────────────────────────┘

T1: Phase 标记 Machine-3 待删除
        ↓
T2: KCP Controller 立即调谐
        ↓
T3: KCP Controller 发现 Replicas=3，但只有 2 个 Machine
        ↓
T4: KCP Controller 创建新 Machine-4（错误！）
        ↓
结果：既要删除 Machine-3，又要创建 Machine-4

┌─────────────────────────────────────────────────────────────────────┐
│  Pause 后的正确流程                                                   │
└─────────────────────────────────────────────────────────────────────┘

T1: Phase Pause KCP Controller
        ↓
T2: Phase 标记 Machine-3 待删除
        ↓
T3: Phase 设置 Replicas=2
        ↓
T4: Phase Resume KCP Controller
        ↓
T5: KCP Controller 调谐
        ↓
T6: KCP Controller 发现 Replicas=2，删除 Machine-3
        ↓
结果：正确删除 Machine-3
```

#### 2. Pause/Resume 实现

```go
// Pause
func PauseClusterAPIObj(ctx context.Context, c client.Client, obj client.Object) error {
    annotations := obj.GetAnnotations()
    if annotations == nil {
        annotations = map[string]string{}
    }
    annotations[clusterv1beta1.PausedAnnotation] = ""  // 设置 Pause 注解
    obj.SetAnnotations(annotations)
    return c.Update(ctx, obj)
}

// Resume
func ResumeClusterAPIObj(ctx context.Context, c client.Client, obj client.Object) error {
    annotations := obj.GetAnnotations()
    delete(annotations, clusterv1beta1.PausedAnnotation)  // 删除 Pause 注解
    obj.SetAnnotations(annotations)
    return c.Update(ctx, obj)
}
```

### 九、错误处理与回滚

#### 1. defer 回滚机制

```go
specCopy := scope.KubeadmControlPlane.Spec.DeepCopy()
currentReplicas := specCopy.Replicas

// 设置回滚
defer func() {
    if err != nil {
        log.Info("Scale up KubeadmControlPlane replicas to %d.", currentReplicas)
        scope.KubeadmControlPlane.Spec.Replicas = currentReplicas
        if err = phaseutil.ResumeClusterAPIObj(ctx, c, scope.KubeadmControlPlane); err != nil {
            log.Error("Rollback KubeadmControlPlane replicas failed. err: %v", err)
        }
    }
}()
```
**作用**：
- ✅ 如果删除过程中出现错误，恢复 Replicas 到删除前的值
- ✅ Resume KCP Controller，使其继续正常工作

### 十、约束与限制

#### 1. 最小副本数限制

```go
exceptReplicas := *currentReplicas - int32(len(deleteMap))
if exceptReplicas < 1 {
    exceptReplicas = 1  // 至少保留 1 个 Master
}
```
**约束**：
- ⚠️ **至少保留 1 个 Master**：即使删除多个节点，也至少保留 1 个

#### 2. 删除顺序

```
删除顺序：
1. 暂停 KCP Controller
2. 标记 Machine 待删除
3. 缩容 Replicas
4. 恢复 KCP Controller
5. KCP Controller 删除 Machine
6. 等待删除完成
7. 清理资源
```

### 十一、总结

#### 1. 功能清单

| 功能 | 说明 |
|------|------|
| **确定删除节点** | 通过注解或 BKENode 删除标记确定 |
| **处理节点映射** | 建立节点与 Machine 的映射关系 |
| **暂停 KCP** | 防止 KCP Controller 干扰 |
| **标记待删除** | 为 Machine 设置删除注解 |
| **缩容 Replicas** | 减少 KCP 副本数 |
| **等待删除** | 轮询检查 Machine 是否删除 |
| **清理资源** | 删除 BKENode、清理 Pod |

#### 2. 关键特性

- ⚠️ **两种删除模式**：传统模式（注解）和 BKENode 删除模式
- ⚠️ **Pause/Resume 机制**：防止 KCP Controller 干扰
- ⚠️ **错误回滚**：失败时恢复 Replicas
- ⚠️ **最小副本限制**：至少保留 1 个 Master
- ⚠️ **资源清理**：删除 BKENode、强制删除 Pod

#### 3. 执行流程

```
NeedExecute 判断
    ↓
reconcileMasterDelete
    ├── 确定删除节点
    ├── 处理节点映射
    ├── Pause KCP
    ├── 标记待删除
    ├── 缩容 Replicas
    └── Resume KCP
    ↓
waitMasterDelete
    ├── 等待 Machine 删除
    └── 清理节点资源
```


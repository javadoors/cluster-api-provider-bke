# 📋 BKECluster 删除规格与流程分析

## 一、删除触发机制

### 1.1 触发条件
删除由以下**任一**条件触发（定义于 `pkg/phaseframe/phaseutil/bkecluster.go`）：
```go
func IsDeleteOrReset(bkeCluster *bkev1beta1.BKECluster) bool {
    return !bkeCluster.DeletionTimestamp.IsZero() || bkeCluster.Spec.Reset
}
```
- **DeletionTimestamp 非空**: 用户执行 `kubectl delete bkecluster <name>` 或调用 `DELETE /rest/cluster/v1/clusters/{name}`
- **Spec.Reset = true**: 软重置/重建操作也复用同一删除流程

### 1.2 Finalizer 机制
- **Finalizer 常量**: `bkecluster.infrastructure.cluster.x-k8s.io`
- **注入时机**: `EnsureFinalizer` Phase 在集群创建时注入
- **拦截删除**: K8s 检测到 Finalizer 存在时，仅设置 `DeletionTimestamp`，不物理删除资源
- **释放时机**: `cleanupClusterResources()` 完成所有清理后移除 Finalizer，K8s 完成最终清理

---

## 二、删除 Phase 定义

**DeletePhases 注册** (`pkg/phaseframe/phases/list.go`):
```go
DeletePhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsurePaused,       // 检查暂停状态，若暂停则恢复
    NewEnsureDeleteOrReset // 核心删除逻辑
}

ClusterDeleteResetPhaseNames = []confv1beta1.BKEClusterPhase{
    EnsurePausedName,
    EnsureDeleteOrResetName,
}
```

> **注意**: `EnsureWorkerDelete` 和 `EnsureMasterDelete` 属于 `PostDeployPhases`，用于**节点缩容**，不用于集群整体删除。

## 三、完整删除流程

### 3.1 控制器入口与 Phase 计算
```
BKEClusterReconciler.Reconcile()
  │
  ├─ 检测 IsDeleteOrReset() == true
  │   └─ 使用 DeletePhases (仅 EnsurePaused + EnsureDeleteOrReset)
  │
  └─ PhaseFlow.Execute()
      │
      ├─ EnsurePaused: 若集群处于暂停状态，自动恢复并清理 Agent Commands
      │
      └─ EnsureDeleteOrReset: 执行核心删除逻辑
```

### 3.2 `EnsureDeleteOrReset.Execute()` 核心逻辑
```go
// 1. 设置 5 分钟超时上下文
ctx, cancel := context.WithTimeout(baseCtx, 5*time.Minute)

// 2. 每 10 秒轮询执行 reconcileDelete()，直到成功或超时
err := wait.PollImmediateUntil(10*time.Second, func() (bool, error) {
    if err := e.reconcileDelete(ctx); err != nil {
        return false, nil // 错误则重试
    }
    return true, nil
}, ctx.Done())
```

### 3.3 `reconcileDelete()` 资源清理顺序

| 序号 | 步骤 | 方法 | 清理内容 | 失败处理 |
|:---:|:---|:---|:---|:---|
| **1** | 标记删除状态 | `ensureClusterStatusDeleting()` | 设置 `ClusterStatus = ClusterDeleting` | 阻断 |
| **2** | 删除 CAPI Cluster | `handleClusterDeletion()` | 删除 `clusterv1.Cluster` 对象，触发级联删除 (Machines, MachineDeployments, KCP) | 阻断，等待级联完成 |
| **3** | 清理 BKEMachine | `handleBKEMachineDeletion()` | 删除未引导的 BKEMachine；对无 Owner 的资源强制移除 Finalizer | 阻断，等待删除完成 |
| **4** | 清理 Secrets | `deleteRelatedResources()` | 删除所有 `type=bke.bocloud.com/secret` 的 Secret | **忽略错误** |
| **5** | 清理 Commands | `deleteRelatedResources()` | 移除 Finalizer 后删除所有 `agentv1beta1.Command` | **忽略错误** |
| **6** | 关闭节点 Agent | `ShutDownAgent()` | 向已推送 Agent 的节点发送 `shutdown-bkeagent` 命令 (超时 30s) | 异步执行，不阻断 |
| **7** | 清理集群资源 | `cleanupClusterResources()` | 删除 BKENode、移除 BKECluster Finalizer、删除关联 Events | BKENode 删除错误**忽略** |
| **8** | 清理命名空间 | `handleNamespaceDeletion()` | 若无其他 BKECluster 且未设置忽略注解，删除 Namespace | **忽略错误** |
| **9** | 清理状态缓存 | `handleNamespaceDeletion()` | 移除 StatusManager 缓存 | - |
| **10** | 清理指标 | `ensureDeleteOrResetPostHook()` | 注销 Prometheus 指标 | - |

## 四、关键资源清理详解

### 4.1 CAPI Cluster 级联删除
```go
func (e *EnsureDeleteOrReset) handleClusterDeletion(ctx context.Context, c client.Client, log *bkev1beta1.BKELogger) error {
    if e.Ctx.Cluster != nil && e.Ctx.Cluster.Status.Phase != string(clusterv1.ClusterPhaseDeleting) {
        // 删除 CAPI Cluster 对象，这将触发所有关联资源的级联删除
        if err := c.Delete(ctx, e.Ctx.Cluster); err != nil {
            return err
        }
        // 返回错误以触发重试，等待级联删除完成
        return errors.New("wait for the deletion of cluster api obj")
    }
    return nil
}
```

### 4.2 BKEMachine 强制删除
```go
func (e *EnsureDeleteOrReset) handleBKEMachineDeletion(...) error {
    for _, bkeMachine := range bkeMachines.Items {
        // 未引导的机器，CAPI 不会自动删除，需手动删除
        if !bkeMachine.Status.Bootstrapped {
            c.Delete(ctx, &bkeMachine)
        }
        // 无 Owner 的机器，Controller 不会处理，强制移除 Finalizer
        if len(bkeMachine.OwnerReferences) == 0 {
            controllerutil.RemoveFinalizer(&bkeMachine, bkev1beta1.BKEMachineFinalizer)
            c.Update(ctx, &bkeMachine)
        }
    }
    return errors.New("wait for bkeMachine delete") // 等待删除完成
}
```

### 4.3 Agent 关闭命令
```go
func (e *EnsureDeleteOrReset) ShutDownAgent(ctx context.Context) {
    // 1. 获取所有已推送 Agent 的节点
    needShutDownNodes := bkenode.Nodes{}
    for _, node := range allNodes {
        nodeState, _ := nodeFetcher.GetNodeStateFlagForCluster(ctx, bkeCluster, node.IP, NodeAgentPushedFlag)
        if nodeState {
            needShutDownNodes = append(needShutDownNodes, node)
        }
    }
    
    // 2. 发送 shutdown-bkeagent 命令
    shutdownAgentCommand := command.Custom{
        BaseCommand: command.BaseCommand{
            ForceRemove:     true,
            WaitTimeout:     30 * time.Second,
            // ...
        },
        CommandName: "shutdown-bkeagent",
        CommandSpec: &agentv1beta1.CommandSpec{
            Commands: []agentv1beta1.ExecCommand{{
                ID:      "Shutdown agent",
                Command: []string{"Shutdown"},
                Type:    agentv1beta1.CommandBuiltIn,
            }},
        },
    }
    shutdownAgentCommand.New()
    shutdownAgentCommand.Wait()
}
```

### 4.4 命名空间清理策略
```go
func (e *EnsureDeleteOrReset) handleNamespaceDeletion(...) error {
    // 检查是否设置了忽略删除注解
    if v, ok := annotation.HasAnnotation(bkeCluster, DeleteIgnoreNamespaceAnnotationKey); ok && v == "false" {
        // 检查是否还有其他 BKECluster 存在
        bkeClusters := &bkev1beta1.BKEClusterList{}
        c.List(ctx, bkeClusters, client.InNamespace(bkeCluster.Namespace))
        
        // 仅当无其他集群时才删除 Namespace
        if len(bkeClusters.Items) == 0 {
            c.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: bkeCluster.Namespace}})
        }
    }
    // 清理状态缓存
    statusmanage.BKEClusterStatusManager.RemoveBKEClusterStatusCache(bkeCluster)
    return nil
}
```

## 五、异常处理与容错机制

| 场景 | 处理策略 |
|:---|:---|
| **暂停中删除** | 自动恢复 (`Spec.Pause = false`)，清理 Commands 后重新入队 |
| **轮询重试** | 每 10 秒重试一次，最长 5 分钟超时 |
| **Secret 删除失败** | 记录 Warning 日志，**不阻断**删除流程 |
| **Command 删除失败** | 记录 Warning 日志，**不阻断**删除流程 |
| **BKENode 删除失败** | 记录 Warning 日志，**不阻断**删除流程 |
| **Event 删除失败** | 记录 Warning 日志，**不阻断**删除流程 |
| **Namespace 删除失败** | 记录 Warning 日志，**不阻断**删除流程 |
| **超时** | 返回 `Wait delete timeout` 错误，标记 `ClusterDeleteFailed` |
| **Panic 恢复** | `PhaseFlow.handlePanic()` 捕获 Panic，打印堆栈，防止控制器崩溃 |

## 六、删除流程图

```text
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                          BKECluster 删除流程                                         │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  用户调用 DELETE /clusters/{name} 或 kubectl delete bkecluster                      │
│       │                                                                             │
│       ▼                                                                             │
│  ┌─────────────────────────┐                                                        │
│  │ K8s 设置 DeletionTimestamp │                                                        │
│  │ 保留 Finalizer，对象进入 Terminating 状态    │                                                        │
│  └────────────┬────────────┘                                                        │
│               │                                                                     │
│               ▼                                                                     │
│  ┌─────────────────────────┐                                                        │
│  │ BKEClusterReconciler 检测到删除意图        │                                                        │
│  │ determinePhasesFuncs() → DeletePhases      │                                                        │
│  └────────────┬────────────┘                                                        │
│               │                                                                     │
│               ▼                                                                     │
│  ┌─────────────────────────┐                                                        │
│  │ EnsurePaused            │ ← 若暂停则恢复，清理 Commands，Requeue                 │
│  └────────────┬────────────┘                                                        │
│               │                                                                     │
│               ▼                                                                     │
│  ┌─────────────────────────┐                                                        │
│  │ EnsureDeleteOrReset     │ ← 5 分钟超时，10 秒轮询                                │
│  │                         │                                                        │
│  │  1. 标记 ClusterDeleting│                                                        │
│  │  2. 删除 CAPI Cluster   │ → 触发级联删除 (Machines, KCP, MD)                     │
│  │  3. 清理 BKEMachine     │ → 强制删除无 Owner 资源                                │
│  │  4. 清理 Secrets        │ → 忽略错误                                             │
│  │  5. 清理 Commands       │ → 移除 Finalizer 后删除                                │
│  │  6. ShutDownAgent       │ → 发送 shutdown 命令 (30s 超时)                        │
│  │  7. 清理 BKENode/Events │ → 忽略错误                                             │
│  │  8. 移除 Finalizer      │ → K8s 物理删除 BKECluster                              │
│  │  9. 清理 Namespace      │ → 仅当无其他集群时删除                                 │
│  │ 10. 清理缓存/指标       │                                                        │
│  └─────────────────────────┘                                                        │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

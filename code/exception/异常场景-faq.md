# EnsureNodesEnv.Execute() 返回错误的影响
          
## EnsureNodesEnv.Execute() 返回错误的影响分析

### 一、错误传递链路

```
┌─────────────────────────────────────────────────────────────────────┐
│  错误传递链路                                                         │
└─────────────────────────────────────────────────────────────────────┘

EnsureNodesEnv.Execute() 返回错误
        │
        ├── (ctrl.Result, error)
        │
        ▼
phase_flow.go:executePhases()
        │
        ├── phaseResult, phaseErr := phase.Execute()
        ├── errs = append(errs, phaseErr)  // 收集错误
        ├── err = phase.ExecutePostHook(err)  // 设置 Phase 状态为 Failed
        ├── err = kerrors.NewAggregate(errs)  // 聚合错误
        │
        ▼
phase_flow.go:Execute()
        │
        ├── return res, err  // 返回聚合错误
        │
        ▼
bkecluster_controller.go:executePhaseFlow()
        │
        ├── res, err := flow.Execute()
        ├── if err != nil { log.Warnf(...) }  // 记录警告
        ├── return res, err  // 返回错误
        │
        ▼
bkecluster_controller.go:Reconcile()
        │
        ├── phaseResult, err := r.executePhaseFlow(...)
        ├── if err != nil { return ctrl.Result{}, err }  // 返回错误
        │
        ▼
Controller Runtime
        │
        ├── 记录错误到 Event
        ├── Requeue 请求
        └── 等待 ExponentialBackoff 后重新调谐
```

### 二、关键代码分析

#### 1. Phase 执行与错误收集

```go
// phase_flow.go:executePhases()
func (p *PhaseFlow) executePhases(phases confv1beta1.BKEClusterPhases) (ctrl.Result, error) {
    var errs []error
    var err error
    var res ctrl.Result

    for _, phase := range p.BKEPhases {
        if phase.Name().In(phases) {
            // 执行前置 hook，设置 phase 状态为 Running
            if err = phase.ExecutePreHook(); err != nil {
                return res, err
            }

            // 执行 Phase
            phaseResult, phaseErr := phase.Execute()
            if phaseErr != nil {
                err = phaseErr
                errs = append(errs, phaseErr)  // ⭐ 收集错误
            }
            res = util.LowestNonZeroResult(res, phaseResult)

            // 执行后置 hook，设置 phase 状态为 Success 或 Failed
            err = phase.ExecutePostHook(err)
            if err != nil {
                errs = append(errs, err)
            }

            // 刷新 oldBKECluster 和 newBKECluster
            if err = p.refreshOldAndNewBKECluster(); err != nil {
                errs = append(errs, err)
            }

            // ⭐ 如果有错误，立即返回聚合错误
            if len(errs) > 0 {
                err = kerrors.NewAggregate(errs)
                return res, err
            }
        }
    }

    return res, nil
}
```

#### 2. Phase 状态设置

```go
// phaseframe/phase.go (推测)
func (p *BasePhase) ExecutePostHook(err error) error {
    if err != nil {
        p.SetStatus(bkev1beta1.PhaseFailed)  // ⭐ 设置为 Failed
        p.Report(err.Error(), false)
    } else {
        p.SetStatus(bkev1beta1.PhaseSucceeded)  // 设置为 Succeeded
        p.Report("", false)
    }
    return nil
}
```

#### 3. Controller Reconcile 处理

```go
// bkecluster_controller.go:Reconcile()
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // ...
    
    // 执行阶段流程
    phaseResult, err := r.executePhaseFlow(ctx, bkeCluster, oldBkeCluster, bkeLogger)
    if err != nil {
        return ctrl.Result{}, err  // ⭐ 返回错误，触发 Requeue
    }
    
    // ...
    return result, nil
}

func (r *BKEClusterReconciler) executePhaseFlow(...) (ctrl.Result, error) {
    // ...
    res, err := flow.Execute()
    if err != nil {
        log.Warnf("Reconcile bkeCluster %q failed: %v", utils.ClientObjNS(bkeCluster), err)
    }
    return res, err  // ⭐ 返回错误
}
```

### 三、影响分析（8 个层面）

#### 1. Phase 状态影响 ⭐⭐⭐

| 影响项 | 说明 |
|--------|------|
| **Phase 状态** | EnsureNodesEnv 状态设置为 `PhaseFailed` |
| **PhaseStatus 记录** | BKECluster.Status.PhaseStatus 中记录失败状态 |
| **失败信息** | 记录错误信息到 PhaseStatus.Message |
| **时间戳** | 记录失败时间到 PhaseStatus.EndTime |

```yaml
# BKECluster.Status.PhaseStatus 示例
phaseStatus:
  - name: EnsureNodesEnv
    status: Failed
    message: "failed to check k8s env: kernel param net.ipv4.ip_forward=0"
    startTime: "2025-01-15T10:00:00Z"
    endTime: "2025-01-15T10:00:05Z"
```

#### 2. 集群状态影响 ⭐⭐⭐

| 影响项 | 说明 |
|--------|------|
| **集群状态** | BKECluster.Status.ClusterStatus 设置为失败状态 |
| **Condition** | NodesEnvCondition 设置为 False |
| **Reason** | 设置为 NodesEnvNotReadyReason |

```go
// calculatingClusterPostStatusByPhase
func calculatingClusterPostStatusByPhase(phase phaseframe.Phase, err error) error {
    if err != nil {
        // 设置集群失败状态
        ctx.BKECluster.Status.ClusterStatus = bkev1beta1.ClusterFailed
        condition.ConditionMark(ctx.BKECluster, bkev1beta1.NodesEnvCondition, 
            confv1beta1.ConditionFalse, constant.NodesEnvNotReadyReason, err.Error())
    }
    return nil
}
```

#### 3. 节点状态影响 ⭐⭐⭐

| 影响项 | 说明 |
|--------|------|
| **节点状态** | 失败节点的状态设置为 `NodeInitFailed` |
| **状态码** | 设置 `NodeFailedFlag` |
| **错误信息** | 记录失败原因到节点状态 |

```go
// 在 EnsureNodesEnv 中设置节点失败状态
nodeFetcher.SetNodeStateWithMessageForCluster(
    e.Ctx, bkeCluster, node.IP, 
    bkev1beta1.NodeInitFailed, 
    "Failed to check k8s env"
)
```

#### 4. Controller Runtime 影响 ⭐⭐⭐

| 影响项 | 说明 |
|--------|------|
| **Requeue** | Controller Runtime 自动 Requeue 请求 |
| **ExponentialBackoff** | 使用指数退避策略重试 |
| **重试间隔** | 5s → 10s → 20s → 40s → ... |
| **最大重试** | 无限制，直到成功或手动干预 |

```
重试时间线：
T0: 第一次执行，失败
T0+5s: 第一次重试
T0+15s: 第二次重试 (5s + 10s)
T0+35s: 第三次重试 (15s + 20s)
T0+75s: 第四次重试 (35s + 40s)
...
```

#### 5. Event 记录影响 ⭐⭐

| 影响项 | 说明 |
|--------|------|
| **Warning Event** | 记录 Warning 事件到 BKECluster |
| **Event Message** | 记录错误信息 |
| **Event Reason** | NodesEnvNotReadyReason |

```yaml
# Event 示例
apiVersion: v1
kind: Event
metadata:
  name: bkecluster-xxx.123456
  namespace: default
involvedObject:
  apiVersion: capbke.bocloud.com/v1beta1
  kind: BKECluster
  name: bkecluster-xxx
reason: NodesEnvNotReady
message: "Failed to check k8s env: kernel param net.ipv4.ip_forward=0"
type: Warning
```

#### 6. 后续 Phase 执行影响 ⭐⭐⭐

| 影响项 | 说明 |
|--------|------|
| **Phase 流程中断** | 当前 Phase 失败后，后续 Phase 不执行 |
| **Phase 状态** | 后续 Phase 状态设置为 `PhaseUnknown` |
| **清理逻辑** | cleanupUnexecutedPhases 清理未执行的 Phase |

```go
// phase_flow.go:executePhases()
defer p.cleanupUnexecutedPhases(&phases)

// cleanupUnexecutedPhases 清理未执行的 Phase
func (p *PhaseFlow) cleanupUnexecutedPhases(phases *confv1beta1.BKEClusterPhases) {
    if len(*phases) > 0 {
        for i, phase := range p.ctx.BKECluster.Status.PhaseStatus {
            if phase.Name.In(*phases) {
                p.ctx.BKECluster.Status.PhaseStatus[i].Status = bkev1beta1.PhaseUnknown
            }
        }
    }
}
```

#### 7. 用户可见影响 ⭐⭐⭐

| 影响项 | 说明 |
|--------|------|
| **kubectl get bkecluster** | 状态显示为 Failed |
| **kubectl describe bkecluster** | 显示错误信息和 Events |
| **监控告警** | 触发集群失败告警 |
| **UI 展示** | 管理界面显示失败状态 |

```bash
$ kubectl get bkecluster
NAME          STATUS    REASON
bkecluster-1  Failed    NodesEnvNotReady

$ kubectl describe bkecluster bkecluster-1
Status:
  ClusterStatus:  Failed
  PhaseStatus:
    - Name:   EnsureNodesEnv
      Status:  Failed
      Message:  failed to check k8s env: kernel param net.ipv4.ip_forward=0
Events:
  Type     Reason              Message
  ----     ------              -------
  Warning  NodesEnvNotReady    Failed to check k8s env: kernel param net.ipv4.ip_forward=0
```

#### 8. 资源清理影响 ⭐

| 影响项 | 说明 |
|--------|------|
| **已创建资源** | 不会自动清理已创建的资源 |
| **Command CR** | 已创建的 Command CR 保留 |
| **ConfigMap** | 已创建的 ConfigMap 保留 |
| **需要手动清理** | 或等待下次 Reconcile 重试 |

### 四、错误处理流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│  EnsureNodesEnv.Execute() 返回错误                                   │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  1. 收集错误到 errs 数组                                              │
│     errs = append(errs, phaseErr)                                   │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  2. 设置 Phase 状态为 Failed                                         │
│     phase.SetStatus(bkev1beta1.PhaseFailed)                         │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  3. 上报 Phase 状态到 BKECluster.Status.PhaseStatus                   │
│     phase.Report(err.Error(), false)                                │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  4. 设置集群状态为 Failed                                             │
│     BKECluster.Status.ClusterStatus = ClusterFailed                 │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  5. 设置 Condition 为 False                                          │
│     condition.ConditionMark(bkeCluster, NodesEnvCondition, False)   │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  6. 清理未执行的 Phase                                                │
│     cleanupUnexecutedPhases() → 设置为 PhaseUnknown                  │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  7. 返回聚合错误                                                      │
│     return res, kerrors.NewAggregate(errs)                          │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  8. Controller Runtime 记录 Warning Event                            │
│     Event: Warning, NodesEnvNotReady, error message                 │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  9. Controller Runtime Requeue 请求                                  │
│     ExponentialBackoff: 5s → 10s → 20s → 40s → ...                  │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  10. 等待重试或手动干预                                                │
│      - 自动重试：等待 ExponentialBackoff 后重新调谐                     │
│      - 手动干预：修复问题后，下次 Reconcile 成功                         │
└─────────────────────────────────────────────────────────────────────┘
```

### 五、总结

#### 1. 核心影响（8 个层面）

| 层面 | 影响 | 重要性 |
|------|------|--------|
| **Phase 状态** | 设置为 Failed，记录错误信息 | ⭐⭐⭐ |
| **集群状态** | 设置为 Failed，Condition 为 False | ⭐⭐⭐ |
| **节点状态** | 失败节点设置为 NodeInitFailed | ⭐⭐⭐ |
| **Controller Runtime** | Requeue + ExponentialBackoff | ⭐⭐⭐ |
| **Event 记录** | 记录 Warning Event | ⭐⭐ |
| **后续 Phase** | 中断执行，设置为 Unknown | ⭐⭐⭐ |
| **用户可见** | kubectl 显示失败状态 | ⭐⭐⭐ |
| **资源清理** | 不自动清理，需手动或重试 | ⭐ |

#### 2. 关键行为

- ✅ **错误不会丢失**：错误会被收集、聚合、记录
- ✅ **状态会更新**：Phase、集群、节点状态都会更新为失败
- ✅ **会自动重试**：Controller Runtime 使用 ExponentialBackoff 自动重试
- ✅ **会中断流程**：当前 Phase 失败后，后续 Phase 不执行
- ✅ **不会清理资源**：已创建的资源不会自动清理

#### 3. 最佳实践

| 场景 | 建议 |
|------|------|
| **临时性错误**（网络抖动） | 等待自动重试，通常会成功 |
| **持久性错误**（配置错误） | 手动修复问题，下次 Reconcile 成功 |
| **需要清理** | 手动删除 BKECluster 或使用 Reset |
| **需要快速重试** | 手动触发 Reconcile（kubectl annotate） |

# Reconcile 返回值行为详解
          
## Reconcile 返回值行为详解

### 一、Reconcile 返回值与重试行为

#### 1. 返回值组合与行为

```go
// ctrl.Result 结构
type Result struct {
    Requeue      bool          // 是否重新入队
    RequeueAfter time.Duration // 延迟重新入队时间
}

// 四种返回方式
return ctrl.Result{}, nil                    // ① 不重试，结束
return ctrl.Result{Requeue: true}, nil       // ② 立即重试
return ctrl.Result{RequeueAfter: 10s}, nil   // ③ 延迟 10s 重试
return ctrl.Result{}, err                    // ④ 错误重试
```

#### 2. 返回错误的重试行为

```go
// 返回错误
return ctrl.Result{}, err
```

| 行为 | 说明 |
|------|------|
| **重试** | ✅ 会重试 |
| **重试策略** | ExponentialBackoff（指数退避） |
| **重试间隔** | 5s → 10s → 20s → 40s → 80s → ... |
| **最大间隔** | 通常 1000s |
| **重试次数** | 无限制，直到成功或手动干预 |

#### 3. Controller Runtime 重试机制

```
┌─────────────────────────────────────────────────────────────────────┐
│  ExponentialBackoff 重试策略                                         │
└─────────────────────────────────────────────────────────────────────┘

初始间隔: 5ms
最大间隔: 1000s
退避系数: 2.0

重试时间线：
T0:      第一次执行，返回错误
T0+5ms:  第一次重试
T0+15ms: 第二次重试 (5ms + 10ms)
T0+35ms: 第三次重试 (15ms + 20ms)
T0+75ms: 第四次重试 (35ms + 40ms)
T0+155ms: 第五次重试 (75ms + 80ms)
...
T0+1000s: 达到最大间隔，后续每次间隔 1000s
```

### 二、不可恢复错误的处理方式

#### 1. 不可恢复错误类型

| 错误类型 | 示例 | 是否可恢复 |
|---------|------|-----------|
| **配置错误** | KubernetesVersion 格式错误 | ❌ 不可恢复 |
| **资源不足** | CPU/Memory 不满足要求 | ❌ 不可恢复（需手动扩容） |
| **版本不兼容** | 跨版本升级（1.24 → 1.26） | ❌ 不可恢复 |
| **证书过期** | CA 证书已过期 | ❌ 不可恢复（需手动续期） |
| **网络错误** | API Server 暂时不可用 | ✅ 可恢复 |
| **临时错误** | etcd 选举中 | ✅ 可恢复 |

#### 2. 不可恢复错误处理策略

##### 策略 1：设置失败状态 + 返回 nil（推荐）

```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bkeCluster, err := r.getAndValidateCluster(ctx, req)
    if err != nil {
        return r.handleClusterError(err)
    }
    
    // 检测到不可恢复错误
    if isIrrecoverableError(bkeCluster) {
        // ① 设置失败状态
        bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterFailed
        condition.ConditionMark(bkeCluster, bkev1beta1.ClusterReadyCondition, 
            confv1beta1.ConditionFalse, "IrrecoverableError", "configuration error")
        
        // ② 更新状态
        if err := r.Client.Status().Update(ctx, bkeCluster); err != nil {
            return ctrl.Result{}, err  // 更新失败，返回错误重试
        }
        
        // ③ 记录 Event
        r.Recorder.Eventf(bkeCluster, corev1.EventTypeWarning, 
            "IrrecoverableError", "Cluster configuration is invalid, please fix manually")
        
        // ④ 返回 nil，不重试
        return ctrl.Result{}, nil
    }
    
    // 正常处理
    return r.normalReconcile(ctx, bkeCluster)
}
```
**优点**：
- ✅ 不会无限重试，节省资源
- ✅ 用户可见失败状态
- ✅ 记录失败原因，便于排障
- ✅ 用户修复后，下次 Reconcile 会成功

##### 策略 2：设置失败状态 + 延迟重试

```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 检测到可能恢复的错误（如资源不足）
    if isRecoverableError(bkeCluster) {
        // 设置失败状态
        bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterFailed
        r.Client.Status().Update(ctx, bkeCluster)
        
        // 延迟 5 分钟重试（给用户时间扩容）
        return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
    }
    
    return r.normalReconcile(ctx, bkeCluster)
}
```
**适用场景**：
- 资源不足（用户可能扩容）
- 依赖服务不可用（可能恢复）
- 配额不足（用户可能调整配额）

##### 策略 3：使用 TerminalError（不推荐）

```go
import "sigs.k8s.io/cluster-api/util"

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    if isIrrecoverableError(bkeCluster) {
        // 返回 TerminalError，Controller Runtime 不会重试
        return ctrl.Result{}, util.TerminalError(errors.New("irrecoverable error"))
    }
    
    return r.normalReconcile(ctx, bkeCluster)
}
```
**缺点**：
- ❌ 用户不可见失败状态
- ❌ 不符合 CAPI 最佳实践
- ❌ 难以排障

### 三、返回 nil 的行为

#### 1. 返回 `ctrl.Result{}, nil`

```go
return ctrl.Result{}, nil
```

| 行为 | 说明 |
|------|------|
| **重试** | ❌ 不会重试 |
| **状态** | 调谐完成 |
| **触发条件** | 下次资源变更时才会重新调谐 |

**适用场景**：
- ✅ 调谐成功
- ✅ 不可恢复错误（已设置失败状态）
- ✅ 无需进一步处理

#### 2. 返回 `ctrl.Result{Requeue: true}, nil`

```go
return ctrl.Result{Requeue: true}, nil
```

| 行为 | 说明 |
|------|------|
| **重试** | ✅ 立即重试 |
| **间隔** | 无延迟，立即重新入队 |

**适用场景**：
- 需要立即重新调谐
- 状态变更后需要立即处理

#### 3. 返回 `ctrl.Result{RequeueAfter: 10s}, nil`

```go
return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
```

| 行为 | 说明 |
|------|------|
| **重试** | ✅ 延迟重试 |
| **间隔** | 10 秒后重新入队 |

**适用场景**：
- 等待外部条件满足
- 定期检查状态
- 避免频繁调谐

### 四、错误处理最佳实践

#### 1. 错误分类处理

```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bkeCluster, err := r.getAndValidateCluster(ctx, req)
    if err != nil {
        return r.handleClusterError(err)
    }
    
    // 根据错误类型选择不同处理策略
    result, err := r.reconcileWithStrategy(ctx, bkeCluster)
    if err != nil {
        return r.handleErrorByType(ctx, bkeCluster, err)
    }
    
    return result, nil
}

func (r *BKEClusterReconciler) handleErrorByType(
    ctx context.Context, 
    bkeCluster *bkev1beta1.BKECluster, 
    err error) (ctrl.Result, error) {
    
    // ① 不可恢复错误：设置失败状态 + 返回 nil
    if isIrrecoverable(err) {
        return r.handleIrrecoverableError(ctx, bkeCluster, err)
    }
    
    // ② 可恢复但需等待：设置失败状态 + 延迟重试
    if isRecoverableWithWait(err) {
        return r.handleRecoverableErrorWithWait(ctx, bkeCluster, err)
    }
    
    // ③ 临时错误：返回错误，自动重试
    return ctrl.Result{}, err
}
```

#### 2. 不可恢复错误处理

```go
func (r *BKEClusterReconciler) handleIrrecoverableError(
    ctx context.Context, 
    bkeCluster *bkev1beta1.BKECluster, 
    err error) (ctrl.Result, error) {
    
    // ① 设置失败状态
    bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterFailed
    
    // ② 设置 Condition
    condition.ConditionMark(bkeCluster, 
        bkev1beta1.ClusterReadyCondition, 
        confv1beta1.ConditionFalse, 
        "IrrecoverableError", 
        err.Error())
    
    // ③ 更新状态
    if updateErr := r.Client.Status().Update(ctx, bkeCluster); updateErr != nil {
        // 更新失败，返回错误重试
        return ctrl.Result{}, updateErr
    }
    
    // ④ 记录 Warning Event
    r.Recorder.Eventf(bkeCluster, 
        corev1.EventTypeWarning, 
        "IrrecoverableError", 
        "Cluster reconciliation failed: %v. Please fix manually.", err)
    
    // ⑤ 返回 nil，不重试
    return ctrl.Result{}, nil
}
```

#### 3. 可恢复错误处理

```go
func (r *BKEClusterReconciler) handleRecoverableErrorWithWait(
    ctx context.Context, 
    bkeCluster *bkev1beta1.BKECluster, 
    err error) (ctrl.Result, error) {
    
    // ① 设置失败状态
    bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterFailed
    r.Client.Status().Update(ctx, bkeCluster)
    
    // ② 记录 Warning Event
    r.Recorder.Eventf(bkeCluster, 
        corev1.EventTypeWarning, 
        "RecoverableError", 
        "Cluster reconciliation failed: %v. Will retry in 5 minutes.", err)
    
    // ③ 延迟 5 分钟重试
    return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}
```

### 五、错误类型判断

#### 1. 不可恢复错误判断

```go
func isIrrecoverable(err error) bool {
    // 配置错误
    if errors.Is(err, ErrInvalidConfiguration) {
        return true
    }
    
    // 版本不兼容
    if errors.Is(err, ErrIncompatibleVersion) {
        return true
    }
    
    // 证书过期
    if errors.Is(err, ErrCertificateExpired) {
        return true
    }
    
    // 资源配额不足（需要手动调整）
    if errors.Is(err, ErrQuotaExceeded) {
        return true
    }
    
    return false
}
```

#### 2. 可恢复错误判断

```go
func isRecoverableWithWait(err error) bool {
    // 资源不足（可能扩容）
    if errors.Is(err, ErrInsufficientResources) {
        return true
    }
    
    // 依赖服务不可用（可能恢复）
    if errors.Is(err, ErrDependencyUnavailable) {
        return true
    }
    
    // 节点 NotReady（可能恢复）
    if errors.Is(err, ErrNodeNotReady) {
        return true
    }
    
    return false
}
```

### 六、实际应用示例

#### 1. EnsureNodesEnv 错误处理

```go
func (e *EnsureNodesEnv) Execute() (ctrl.Result, error) {
    // 执行环境检查
    if err := e.checkNodesEnv(); err != nil {
        // 判断错误类型
        if isIrrecoverableEnvError(err) {
            // 不可恢复错误：设置失败状态 + 返回 nil
            return e.handleIrrecoverableEnvError(err)
        }
        
        // 临时错误：返回错误，自动重试
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}

func isIrrecoverableEnvError(err error) bool {
    // 内核参数错误（需要手动修复）
    if strings.Contains(err.Error(), "kernel param") {
        return true
    }
    
    // SELinux 错误（需要手动修复）
    if strings.Contains(err.Error(), "selinux") {
        return true
    }
    
    // 资源不足（需要手动扩容）
    if strings.Contains(err.Error(), "not enough") {
        return true
    }
    
    return false
}

func (e *EnsureNodesEnv) handleIrrecoverableEnvError(err error) (ctrl.Result, error) {
    _, c, bkeCluster, _, log := e.Ctx.Untie()
    
    // 设置节点失败状态
    nodeFetcher := e.Ctx.NodeFetcher()
    for _, node := range e.nodes {
        nodeFetcher.SetNodeStateWithMessageForCluster(
            e.Ctx, bkeCluster, node.IP, 
            bkev1beta1.NodeInitFailed, 
            fmt.Sprintf("Irrecoverable error: %v", err))
    }
    
    // 设置集群失败状态
    bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterFailed
    condition.ConditionMark(bkeCluster, bkev1beta1.NodesEnvCondition, 
        confv1beta1.ConditionFalse, "IrrecoverableError", err.Error())
    
    // 更新状态
    if updateErr := c.Status().Update(e.Ctx.Ctx, bkeCluster); updateErr != nil {
        return ctrl.Result{}, updateErr
    }
    
    // 记录 Event
    e.Ctx.Log.Error("IrrecoverableError", "Node env check failed: %v", err)
    
    // 返回 nil，不重试
    return ctrl.Result{}, nil
}
```

### 七、总结

#### 1. 返回值行为对比

| 返回方式 | 重试 | 间隔 | 适用场景 |
|---------|------|------|---------|
| `return ctrl.Result{}, nil` | ❌ | - | 成功、不可恢复错误 |
| `return ctrl.Result{}, err` | ✅ | ExponentialBackoff | 临时错误 |
| `return ctrl.Result{Requeue: true}, nil` | ✅ | 立即 | 需要立即重新调谐 |
| `return ctrl.Result{RequeueAfter: 10s}, nil` | ✅ | 10s | 等待条件满足 |

#### 2. 不可恢复错误处理流程

```
┌─────────────────────────────────────────────────────────────────────┐
│  检测到不可恢复错误                                                    │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  1. 设置失败状态                                                      │
│     bkeCluster.Status.ClusterStatus = ClusterFailed                 │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  2. 设置 Condition                                                  │
│     condition.ConditionMark(bkeCluster, ..., False, ...)            │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  3. 更新状态                                                         │
│     r.Client.Status().Update(ctx, bkeCluster)                       │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  4. 记录 Warning Event                                               │
│     r.Recorder.Eventf(bkeCluster, Warning, "IrrecoverableError")    │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  5. 返回 nil，不重试                                                  │
│     return ctrl.Result{}, nil                                       │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  6. 用户修复问题                                                      │
│     - 修改配置                                                       │
│     - 扩容资源                                                       │
│     - 续期证书                                                       │
└─────────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│  7. 下次 Reconcile 成功                                              │
│     - 资源变更触发 Reconcile                                          │
│     - 问题已修复，调谐成功                                             │
└─────────────────────────────────────────────────────────────────────┘
```

#### 3. 最佳实践

| 场景 | 返回方式 | 原因 |
|------|---------|------|
| **调谐成功** | `return ctrl.Result{}, nil` | 无需进一步处理 |
| **临时错误** | `return ctrl.Result{}, err` | 自动重试 |
| **等待条件** | `return ctrl.Result{RequeueAfter: 10s}, nil` | 延迟重试 |
| **不可恢复错误** | 设置失败状态 + `return ctrl.Result{}, nil` | 避免无限重试 |
| **需要立即重试** | `return ctrl.Result{Requeue: true}, nil` | 立即重新调谐 |

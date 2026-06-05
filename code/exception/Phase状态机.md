# Phase 状态机完整流程图

## Phase 状态机流程图

```
┌─────────────────────────────────────────────────────────────────┐
│                      Phase 状态机流程                              │
└─────────────────────────────────────────────────────────────────┘

                    ┌──────────────┐
                    │  Phase 初始   │
                    │   (无状态)    │
                    └──────┬───────┘
                           │
                           ▼
                  ┌────────────────┐
                  │ NeedExecute()? │
                  └────────┬───────┘
                           │
              ┌────────────┴────────────┐
              │                         │
              ▼                         ▼
        ┌──────────┐             ┌──────────────┐
        │  false   │             │    true      │
        └────┬─────┘             └──────┬───────┘
             │                          │
             ▼                          ▼
    ┌─────────────────┐         ┌──────────────┐
    │  PhaseSkipped   │         │ PhaseWaiting │ ← 等待执行
    │   (跳过执行)     │         │  (初始状态)   │
    └─────────────────┘         └──────┬───────┘
                                        │
                                        ▼
                               ┌─────────────────┐
                               │ ExecutePreHook  │
                               └────────┬────────┘
                                        │
                                        ▼
                               ┌─────────────────┐
                               │  PhaseRunning   │ ← 执行中
                               │  (设置开始时间)  │
                               └────────┬────────┘
                                        │
                                        ▼
                               ┌─────────────────┐
                               │    Execute()    │
                               └────────┬────────┘
                                        │
                              ┌─────────┴─────────┐
                              │                   │
                              ▼                   ▼
                        ┌──────────┐        ┌──────────┐
                        │  error   │        │  success │
                        └────┬─────┘        └────┬─────┘
                             │                   │
                             ▼                   ▼
                    ┌─────────────────┐  ┌─────────────────┐
                    │  ExecutePostHook│  │  ExecutePostHook│
                    └────────┬────────┘  └────────┬────────┘
                             │                   │
                             ▼                   ▼
                    ┌─────────────────┐  ┌─────────────────┐
                    │  PhaseFailed    │  │ PhaseSucceeded  │
                    │  (设置结束时间)  │  │  (设置结束时间) │
                    └─────────────────┘  └─────────────────┘
                             │                   │
                             └─────────┬─────────┘
                                       │
                                       ▼
                              ┌─────────────────┐
                              │  Report() 上报  │
                              └─────────────────┘
```
## 状态定义
**代码位置**：[types.go](file:///d:/code/github/\cluster-api-provider-bke\api\bke\v1beta1\types.go)
```go
const (
    PhaseSucceeded BKEClusterPhaseStatus = "Succeeded"  // 执行成功
    PhaseFailed    BKEClusterPhaseStatus = "Failed"     // 执行失败
    PhaseUnknown   BKEClusterPhaseStatus = "Unknown"    // 未知状态
    PhaseWaiting   BKEClusterPhaseStatus = "Waiting"    // 等待执行
    PhaseRunning   BKEClusterPhaseStatus = "Running"    // 执行中
    PhaseSkipped   BKEClusterPhaseStatus = "Skipped"    // 跳过执行
)
```
## 状态转换详解

### 1. **初始状态 → PhaseWaiting**
**触发位置**：`determinePhases()` 计算需要执行的 Phase

**代码位置**：[phase_flow.go:139-142](file:///d:/code/github/\cluster-api-provider-bke\pkg\phaseframe\phases\phase_flow.go#L139-L142)
```go
if phase.GetStatus() == bkev1beta1.PhaseWaiting {
    p.ctx.Log.Debug("phase %s    ->     %s", phase.Name(), bkev1beta1.PhaseWaiting)
    waitPhaseCount++
}
```
**状态含义**：Phase 已被识别为需要执行，等待调度

### 2. **PhaseWaiting → PhaseRunning**
**触发位置**：`ExecutePreHook()` 执行前置钩子

**代码位置**：[base.go:67-87](file:///d:/code/github/\cluster-api-provider-bke\pkg\phaseframe\base.go#L67-L87)
```go
func (b *BasePhase) DefaultPreHook() error {
    // refresh bkecluster
    if err := b.Ctx.RefreshCtxBKECluster(); err != nil {
        return err
    }
    
    // set status and start time
    b.SetStatus(bkev1beta1.PhaseRunning)  // ← 设置为 Running
    b.SetStartTime(metav1.Now())           // ← 设置开始时间
    
    // run custom pre hook
    if b.CustomPreHookFuncs != nil && len(b.CustomPreHookFuncs) > 0 {
        for _, f := range b.CustomPreHookFuncs {
            if err := f(b); err != nil {
                return err
            }
        }
    }
    
    // report phase status
    return b.Report("", false)  // ← 上报状态
}
```
**状态含义**：Phase 正在执行，开始计时

### 3. **PhaseRunning → PhaseSucceeded / PhaseFailed**
**触发位置**：`ExecutePostHook()` 执行后置钩子

**代码位置**：[base.go:91-110](file:///d:/code/github/\cluster-api-provider-bke\pkg\phaseframe\base.go#L91-L110)
```go
func (b *BasePhase) DefaultPostHook(err error) error {
    if b.Name() != "EnsureDeleteOrReset" {
        defer metricrecord.PhaseDurationRecord(b.Ctx.BKECluster, b.CName(), b.StartTime.Time, err)
    }
    
    var msg string
    if err != nil {
        msg = err.Error()
    }
    
    if b.GetStatus() == bkev1beta1.PhaseSkipped {
        return b.Report(msg, false)
    }
    
    if err != nil {
        b.SetStatus(bkev1beta1.PhaseFailed)      // ← 执行失败
        b.Ctx.Log.Debug("phase %q run failed: %v", b.Name(), err)
    } else {
        b.SetStatus(bkev1beta1.PhaseSucceeded)   // ← 执行成功
        b.Ctx.Log.Debug("phase %q run succeeded", b.Name())
    }
    
    // ... custom post hooks
    
    return b.Report(msg, false)  // ← 上报最终状态
}
```
**状态含义**：
- `PhaseSucceeded`：Phase 执行成功，设置结束时间
- `PhaseFailed`：Phase 执行失败，记录错误信息

### 4. **初始状态 → PhaseSkipped**
**触发位置**：`executePhases()` 判断 Phase 不需要执行

**代码位置**：[phase_flow.go:232-246](file:///d:/code/github/\cluster-api-provider-bke\pkg\phaseframe\phases\phase_flow.go#L232-L246)
```go
} else {
    phase.SetStatus(bkev1beta1.PhaseSkipped)  // ← 设置为 Skipped
}

} else {
    phase.SetStatus(bkev1beta1.PhaseSkipped)  // ← 设置为 Skipped
}

if phase.GetStatus() == bkev1beta1.PhaseSkipped {
    p.ctx.Log.Debug("********************************")
    p.ctx.Log.Debug("phase %s    ->     %s", phase.Name(), bkev1beta1.PhaseSkipped)
    p.ctx.Log.Debug("********************************")
    if err := phase.Report("", false); err != nil {
        return ctrl.Result{}, err
    }
    continue
}
```
**状态含义**：Phase 不满足执行条件，跳过执行

### 5. **任意状态 → PhaseUnknown**
**触发位置**：`cleanupUnexecutedPhases()` 清理未执行的 Phase

**代码位置**：[phase_flow.go:181-192](file:///d:/code/github/\cluster-api-provider-bke\pkg\phaseframe\phases\phase_flow.go#L181-L192)
```go
func (p *PhaseFlow) cleanupUnexecutedPhases(phases *confv1beta1.BKEClusterPhases) {
    if len(*phases) > 0 {
        for i, phase := range p.ctx.BKECluster.Status.PhaseStatus {
            if phase.Name.In(*phases) {
                p.ctx.BKECluster.Status.PhaseStatus[i].Status = bkev1beta1.PhaseUnknown  // ← 设置为 Unknown
            }
        }
        if err := mergecluster.SyncStatusUntilComplete(p.ctx.Client, p.ctx.BKECluster); err != nil {
            return
        }
    }
}
```
**状态含义**：Phase 原本应该执行，但被清理（如集群删除、Phase 列表变更）

## 状态上报机制

### Report() 函数
**代码位置**：[base.go:209-237](file:///d:/code/github/\cluster-api-provider-bke\pkg\phaseframe\base.go#L209-L237)
```go
func (b *BasePhase) Report(msg string, onlyRecord bool) error {
    _, c, bkeCluster, _, log := b.Ctx.Untie()
    
    // 没有状态不上报，说明不执行，也不需要在状态中展示
    if b.Status == "" {
        return nil
    }
    status := bkeCluster.Status.PhaseStatus
    
    defer func() {
        bkeCluster.Status.PhaseStatus = status
        if onlyRecord {
            return  // 只记录，不上报
        }
        if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
            log.NormalLogger.Errorf("Failed to update BKECluster status: %v", err)
        }
    }()
    
    // 根据不同状态处理报告
    switch b.Status {
    case bkev1beta1.PhaseSkipped:
        status = b.handleSkippedStatus(status, b.PhaseName)
    case bkev1beta1.PhaseWaiting:
        status = b.handleWaitingStatus(status, b.PhaseName)
    case bkev1beta1.PhaseRunning:
        status = b.handleRunningStatus(status, b.PhaseName, bkeCluster)
    default: // PhaseFailed or PhaseSucceeded
        status = b.handleCompletedStatus(status, b.PhaseName, msg)
    }
    return nil
}
```
**上报策略**：
- `PhaseSkipped`：原地更新或追加状态记录
- `PhaseWaiting`：原地更新或追加状态记录
- `PhaseRunning`：原地更新，设置开始时间
- `PhaseSucceeded/PhaseFailed`：原地更新，设置结束时间

## 状态转换矩阵

| 当前状态 | 触发条件 | 下一状态 | 触发函数 |
|---------|---------|---------|---------|
| 无状态 | `NeedExecute()=true` | `PhaseWaiting` | `determinePhases()` |
| 无状态 | `NeedExecute()=false` | `PhaseSkipped` | `executePhases()` |
| `PhaseWaiting` | 开始执行 | `PhaseRunning` | `ExecutePreHook()` |
| `PhaseRunning` | 执行成功 | `PhaseSucceeded` | `ExecutePostHook()` |
| `PhaseRunning` | 执行失败 | `PhaseFailed` | `ExecutePostHook()` |
| 任意状态 | 清理未执行 | `PhaseUnknown` | `cleanupUnexecutedPhases()` |

## 状态持久化策略

**代码位置**：[phase_flow.go:107-128](file:///d:/code/github/\cluster-api-provider-bke\pkg\phaseframe\phases\phase_flow.go#L107-L128)
```go
func (p *PhaseFlow) processPhaseStatus() {
    // 上报前先找到最后一个成功的
    var lastSuccessPhaseIndex int
    
    for i, phaseStatus := range p.ctx.BKECluster.Status.PhaseStatus {
        if phaseStatus.Status == bkev1beta1.PhaseSucceeded {
            lastSuccessPhaseIndex = i
        }
    }
    
    // 移除成功后除了失败的phase
    last := lastSuccessPhaseIndex + 1
    for last < len(p.ctx.BKECluster.Status.PhaseStatus) && 
        p.ctx.BKECluster.Status.PhaseStatus[last].Status != bkev1beta1.PhaseFailed {
        last++
    }
    
    p.ctx.BKECluster.Status.PhaseStatus = p.ctx.BKECluster.Status.PhaseStatus[:last]
    
    // 最多保留MaxPhaseStatusHistory个phase，避免重复执行时，phaseStatus过多
    if len(p.ctx.BKECluster.Status.PhaseStatus) > MaxPhaseStatusHistory {
        p.ctx.BKECluster.Status.PhaseStatus = p.ctx.BKECluster.Status.PhaseStatus[len(p.ctx.BKECluster.Status.PhaseStatus)-MaxPhaseStatusHistory:]
    }
}
```
**持久化规则**：
1. **保留最后一个成功的 Phase**：作为执行进度的基准点
2. **移除成功后的非失败 Phase**：避免状态冗余
3. **限制最大历史记录**：最多保留 `MaxPhaseStatusHistory` 个 Phase 状态

## 完整执行流程示例

```
Phase 生命周期示例：

1. 初始状态
   PhaseStatus: []

2. determinePhases() 识别需要执行的 Phase
   PhaseStatus: [
     {Name: "InitControlPlane", Status: "Waiting"}
   ]

3. ExecutePreHook() 开始执行
   PhaseStatus: [
     {Name: "InitControlPlane", Status: "Running", StartTime: "2025-01-01T00:00:00Z"}
   ]

4. Execute() 执行成功
   PhaseStatus: [
     {Name: "InitControlPlane", Status: "Succeeded", StartTime: "...", EndTime: "2025-01-01T00:01:00Z"}
   ]

5. 下一个 Phase
   PhaseStatus: [
     {Name: "InitControlPlane", Status: "Succeeded", ...},
     {Name: "JoinControlPlane", Status: "Waiting"}
   ]

6. 如果某个 Phase 失败
   PhaseStatus: [
     {Name: "InitControlPlane", Status: "Succeeded", ...},
     {Name: "JoinControlPlane", Status: "Failed", StartTime: "...", EndTime: "...", Message: "error..."}
   ]
```

## 总结
**Phase 状态机**是一个典型的**有限状态自动机**，具有以下特点：
1. **状态清晰**：6 种状态覆盖了 Phase 的所有生命周期
2. **转换明确**：每个状态转换都有明确的触发条件和处理函数
3. **持久化机制**：通过 `Report()` 函数将状态持久化到 BKECluster.Status.PhaseStatus
4. **历史管理**：智能清理历史状态，避免状态冗余
5. **错误处理**：失败状态会保留错误信息，便于排查问题

这个状态机设计确保了 Phase 执行过程的**可观测性**和**可追溯性**，是 Cluster API Provider BKE 的核心控制机制之一。
        

# 详细说明暂停状态的判断逻辑

## 暂停状态判断逻辑

### 一、暂停状态的判断层次

BKE 项目在多个层次实现了暂停状态判断：

```
┌─────────────────────────────────────────────────────────────────┐
│  暂停状态判断层次                                                  │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. Controller 层（Predicate 过滤）                               │
│     └── 决定是否触发 Reconcile                                    │
│                                                                 │
│  2. Reconcile 层（早期检查）                                      │
│     └── 决定是否执行协调逻辑                                       │
│                                                                 │
│  3. Phase 层（状态同步）                                          │
│     └── 同步暂停状态到子资源                                       │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 二、Controller 层：Predicate 过滤

#### 1. BKECluster Predicate

```go
// utils/capbke/predicates/bkecluster.go

func BKEClusterUnPause() predicate.Funcs {
    return predicate.Funcs{
        // Update 事件：只有非暂停状态才触发
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj, ok := e.ObjectNew.(*bkev1beta1.BKECluster)
            if !ok {
                return false
            }
            return !newObj.Spec.Pause  // Pause=false → 触发
        },
        
        // Create 事件：只有非暂停状态才触发
        CreateFunc: func(e event.CreateEvent) bool {
            obj, ok := e.Object.(*bkev1beta1.BKECluster)
            if !ok {
                return false
            }
            return !obj.Spec.Pause  // Pause=false → 触发
        },
        
        // Delete 事件：始终触发
        DeleteFunc:  func(event.DeleteEvent) bool { return true },
        
        // Generic 事件：不触发
        GenericFunc: func(event.GenericEvent) bool { return false },
    }
}
```
**作用**：
- ✅ 暂停状态的 BKECluster 不会触发 Reconcile
- ✅ 减少不必要的协调开销
- ✅ 删除操作始终触发（确保清理）

#### 2. Cluster API Cluster Predicate

```go
// utils/capbke/predicates/cluster.go

func ClusterUnPause() predicate.Funcs {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj, ok := e.ObjectNew.(*clusterv1.Cluster)
            if !ok {
                return false
            }
            // Only need to trigger a reconcile if the Cluster.Spec.Paused is false
            return !newObj.Spec.Paused
        },
        
        CreateFunc: func(e event.CreateEvent) bool {
            obj, ok := e.Object.(*clusterv1.Cluster)
            if !ok {
                return false
            }
            return !obj.Spec.Paused
        },
        
        DeleteFunc:  func(event.DeleteEvent) bool { return true },
        GenericFunc: func(event.GenericEvent) bool { return false },
    }
}
```

#### 3. Spec 变更时的暂停检测

```go
// utils/capbke/predicates/bkecluster.go

func BKEClusterSpecChange() predicate.Funcs {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj, ok := e.ObjectNew.(*bkev1beta1.BKECluster)
            oldObj, ok := e.ObjectOld.(*bkev1beta1.BKECluster)
            
            if newObj.Generation != oldObj.Generation {
                // 检测暂停状态变更
                if oldObj.Spec.Pause != newObj.Spec.Pause {
                    log.Infof("集群暂停状态变更 %v -> %v", 
                        oldObj.Spec.Pause, newObj.Spec.Pause)
                    return true  // 暂停状态变更时触发
                }
                
                // ... 其他逻辑
            }
        },
    }
}
```

### 三、Reconcile 层：早期检查

#### 1. BKEMachine Controller

```go
// controllers/capbke/bkemachine_controller.go

func (r *BKEMachineReconciler) handlePauseAndFinalizer(
    objects *RequiredObjects, 
    log *zap.SugaredLogger,
) (ctrl.Result, bool) {
    // 使用 Cluster API 标准的 IsPaused 检查
    if annotations.IsPaused(objects.Cluster, objects.BKEMachine) {
        log.Info("Reconciliation is paused for this object")
        return ctrl.Result{}, true  // 返回 stopped=true
    }

    return ctrl.Result{}, false
}
```

**Cluster API 标准的 IsPaused 实现**：
```go
// sigs.k8s.io/cluster-api/util/annotations/annotations.go

// IsPaused returns true if the Cluster or the Machine is paused.
func IsPaused(cluster *clusterv1.Cluster, machine *clusterv1.Machine) bool {
    if cluster.Spec.Paused {
        return true
    }
    
    if HasPausedAnnotation(machine) {
        return true
    }
    
    return false
}

// HasPausedAnnotation returns true if the object has the paused annotation.
func HasPausedAnnotation(obj client.Object) bool {
    annotations := obj.GetAnnotations()
    if annotations == nil {
        return false
    }
    
    _, ok := annotations[clusterv1.PausedAnnotation]
    return ok
}

// Cluster API 标准的暂停 annotation
const PausedAnnotation = "cluster.x-k8s.io/paused"
```
**判断逻辑**：
1. **Cluster 级别暂停**：`Cluster.Spec.Paused == true`
2. **Machine 级别暂停**：Machine 有 `cluster.x-k8s.io/paused` annotation

#### 2. BKECluster Phase 判断

```go
// pkg/phaseframe/phaseutil/bkecluster.go

// IsPaused 判断bkecluster是否暂停
func IsPaused(bkeCluster *bkev1beta1.BKECluster) bool {
    // 检查 annotation
    v, ok := annotation.HasAnnotation(bkeCluster, annotation.BKEClusterPauseAnnotationKey)
    flag := ok && v == "true"

    // Spec.Pause 和 annotation 必须一致
    return bkeCluster.Spec.Pause == flag
}
```
**判断逻辑**：
- `Spec.Pause == true` 且 annotation `bke.bocloud.com/pause == "true"`
- 两者必须同时满足

**为什么需要双重检查**：
```go
// Spec.Pause: 用户声明的期望状态
// Annotation: 实际生效的状态

// 场景 1: Spec.Pause=true, Annotation 不存在
// → 正在执行暂停操作，还未完成

// 场景 2: Spec.Pause=true, Annotation="true"
// → 暂停已完成，处于暂停状态

// 场景 3: Spec.Pause=false, Annotation="true"
// → 正在执行恢复操作，还未完成

// 场景 4: Spec.Pause=false, Annotation 不存在
// → 正常运行状态
```

### 四、Phase 层：状态同步

#### EnsurePaused Phase

```go
// pkg/phaseframe/phases/ensure_paused.go

func (e *EnsurePaused) reconcilePause() error {
    // 1. 同步 BKECluster 暂停状态
    if err := e.syncBKEClusterPauseStatus(params); err != nil {
        return err
    }

    // 2. 暂停或恢复集群中的命令
    if err := e.pauseOrResumeCommands(params); err != nil {
        return err
    }

    // 3. 暂停或恢复集群 API 对象
    return e.pauseOrResumeClusterAPIObjs(params)
}
```

#### 1. 同步 BKECluster 暂停状态

```go
func (e *EnsurePaused) syncBKEClusterPauseStatus(params PauseOperationParams) error {
    var patchF func(currentCombinedBKECluster *bkev1beta1.BKECluster)
    
    if params.BKECluster.Spec.Pause {
        // 暂停：设置 annotation
        patchF = func(currentCombinedBKECluster *bkev1beta1.BKECluster) {
            annotation.SetAnnotation(currentCombinedBKECluster, 
                annotation.BKEClusterPauseAnnotationKey, "true")
        }
    } else {
        // 恢复：移除 annotation
        patchF = func(currentCombinedBKECluster *bkev1beta1.BKECluster) {
            annotation.RemoveAnnotation(currentCombinedBKECluster, 
                annotation.BKEClusterPauseAnnotationKey)
        }
    }
    
    // 同步状态直到完成
    return mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster, patchF)
}
```

#### 2. 暂停或恢复 Commands

```go
func (e *EnsurePaused) pauseOrResumeCommands(params PauseOperationParams) error {
    // 获取集群所有 Command
    commandLi := &agentv1beta1.CommandList{}
    filters := phaseutil.GetListFiltersByBKECluster(params.BKECluster)
    if err := params.Client.List(params.Ctx, commandLi, filters...); err != nil {
        return errors.Errorf("failed to list command: %v", err)
    }

    // 暂停或恢复所有 Command
    for _, cmd := range commandLi.Items {
        if cmd.Spec.Suspend != params.BKECluster.Spec.Pause {
            cmd.Spec.Suspend = params.BKECluster.Spec.Pause
            if err := params.Client.Update(params.Ctx, &cmd); err != nil {
                params.Log.Warn("Failed to Suspend command %q, err: %v", cmd.Name, err)
                continue
            }
        }
    }
    return nil
}
```

#### 3. 暂停或恢复 Cluster API 对象

```go
func (e *EnsurePaused) pauseOrResumeClusterAPIObjs(params PauseOperationParams) error {
    kcp, _ := phaseutil.GetClusterAPIKubeadmControlPlane(params.Ctx, params.Client, e.Ctx.Cluster)
    md, _ := phaseutil.GetClusterAPIMachineDeployment(params.Ctx, params.Client, e.Ctx.Cluster)

    if params.BKECluster.Spec.Pause {
        // 暂停：设置 CAPI 对象的 paused annotation
        params.Log.Info("Cluster deploy %q is paused", params.BKECluster.Name)
        
        if kcp != nil {
            if err := phaseutil.PauseClusterAPIObj(params.Ctx, params.Client, kcp); err != nil {
                return err
            }
        }
        if md != nil {
            if err := phaseutil.PauseClusterAPIObj(params.Ctx, params.Client, md); err != nil {
                return err
            }
        }
    } else {
        // 恢复：移除 CAPI 对象的 paused annotation
        params.Log.Info("Cluster deploy %q is resumed", params.BKECluster.Name)
        
        // 特殊情况：升级/扩缩容阶段不恢复
        if params.BKECluster.Status.Phase == bkev1beta1.Scale || 
           params.BKECluster.Status.Phase == bkev1beta1.UpgradeControlPlane || 
           params.BKECluster.Status.Phase == bkev1beta1.UpgradeWorker {
            return nil  // 不恢复，继续由自研 Phase 控制
        }
        
        if kcp != nil {
            if err := phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, kcp); err != nil {
                return err
            }
        }
        if md != nil {
            if err := phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, md); err != nil {
                return err
            }
        }
    }
    return nil
}
```

### 五、暂停状态的完整流程

```
┌─────────────────────────────────────────────────────────────────────┐
│  用户设置 BKECluster.Spec.Pause = true                               │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  1. Predicate 过滤                                                   │
│     └── BKEClusterUnPause 检测到 Pause=true                          │
│     └── 不触发 Reconcile（但 Spec 变更会触发）                          │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  2. BKECluster Controller Reconcile                                 │
│     └── determinePhasesFuncs() → 包含 EnsurePaused                   │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  3. EnsurePaused.Execute()                                          │
│     ├── syncBKEClusterPauseStatus()                                 │
│     │   └── 设置 annotation: bke.bocloud.com/pause="true"            │
│     ├── pauseOrResumeCommands()                                     │
│     │   └── 设置所有 Command.Spec.Suspend = true                     │
│     └── pauseOrResumeClusterAPIObjs()                               │
│         ├── 设置 KCP annotation: cluster.x-k8s.io/paused=""          │
│         └── 设置 MD annotation: cluster.x-k8s.io/paused=""           │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  4. 状态更新                                                         │
│     └── BKECluster.Status.ClusterStatus = ClusterPaused             │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  5. 后续影响                                                         │
│     ├── BKECluster Reconcile 被 Predicate 阻止                       │
│     ├── BKEMachine Reconcile 被 IsPaused 阻止                        │
│     ├── Command Controller 检测到 Suspend=true，停止执行              │
│     └── KCP/MD Controller 检测到 paused annotation，停止协调          │
└─────────────────────────────────────────────────────────────────────┘
```

### 六、恢复流程

```
┌─────────────────────────────────────────────────────────────────────┐
│  用户设置 BKECluster.Spec.Pause = false                              │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  1. Predicate 触发 Reconcile                                         │
│     └── BKEClusterUnPause 检测到 Pause=false → 触发                   │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  2. EnsurePaused.Execute()                                          │
│     ├── syncBKEClusterPauseStatus()                                 │
│     │   └── 移除 annotation: bke.bocloud.com/pause                 │
│     ├── pauseOrResumeCommands()                                     │
│     │   └── 设置所有 Command.Spec.Suspend = false                   │
│     └── pauseOrResumeClusterAPIObjs()                               │
│         ├── 移除 KCP annotation: cluster.x-k8s.io/paused           │
│         └── 移除 MD annotation: cluster.x-k8s.io/paused            │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  3. 状态更新                                                         │
│     └── BKECluster.Status.ClusterStatus = ClusterRunning            │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  4. 恢复协调                                                         │
│     ├── BKECluster Reconcile 正常执行                                │
│     ├── BKEMachine Reconcile 正常执行                                │
│     ├── Command Controller 恢复执行                                  │
│     └── KCP/MD Controller 恢复协调                                   │
└─────────────────────────────────────────────────────────────────────┘
```

### 七、特殊情况处理

#### 1. 部署 Addon 时不允许暂停

```go
// webhooks/capbke/bkecluster.go

if newBKECluster.Status.ClusterStatus == bkev1beta1.ClusterDeployingAddon && 
   newBKECluster.Spec.Pause {
    return admission.Errored(http.StatusBadRequest, 
        fmt.Errorf("The cluster is deploying addon and cannot set pause"))
}
```

#### 2. 升级/扩缩容阶段不恢复 CAPI 对象

```go
// pkg/phaseframe/phases/ensure_paused.go

if params.BKECluster.Status.Phase == bkev1beta1.Scale || 
   params.BKECluster.Status.Phase == bkev1beta1.UpgradeControlPlane || 
   params.BKECluster.Status.Phase == bkev1beta1.UpgradeWorker {
    return nil  // 不恢复 CAPI 对象，继续由自研 Phase 控制
}
```

#### 3. Cluster Tracker 不跟踪暂停集群

```go
// utils/capbke/clustertracker/tracker.go

func isClusterInInvalidState(bkeCluster *bkev1beta1.BKECluster) bool {
    return bkeCluster.Spec.Pause ||  // 暂停
           !bkeCluster.DeletionTimestamp.IsZero() ||  // 删除中
           bkeCluster.Spec.Reset ||  // 重置
           condition.HasConditionStatus(bkev1beta1.SwitchBKEAgentCondition, 
               bkeCluster, confv1beta1.ConditionTrue)
}
```

### 八、暂停状态的影响范围

| 组件 | 暂停影响 | 判断方式 |
|------|---------|---------|
| **BKECluster Controller** | 不触发 Reconcile | Predicate: `BKEClusterUnPause` |
| **BKEMachine Controller** | 提前退出 Reconcile | `annotations.IsPaused()` |
| **Command Controller** | 停止执行 Command | `Command.Spec.Suspend = true` |
| **KCP Controller** | 停止协调 Machine | `cluster.x-k8s.io/paused` annotation |
| **MD Controller** | 停止协调 Machine | `cluster.x-k8s.io/paused` annotation |
| **Cluster Tracker** | 不跟踪集群健康 | `isClusterInInvalidState()` |

### 九、测试用例

```go
// controllers/capbke/bkemachine_controller_test.go

t.Run("not paused", func(t *testing.T) {
    objects := &RequiredObjects{
        Cluster:    &clusterv1.Cluster{},
        BKEMachine: &bkev1beta1.BKEMachine{},
    }
    result, stopped := r.handlePauseAndFinalizer(objects, nil)
    assert.False(t, stopped)  // 不停止
})

t.Run("paused via spec", func(t *testing.T) {
    objects := &RequiredObjects{
        Cluster: &clusterv1.Cluster{
            Spec: clusterv1.ClusterSpec{Paused: true},
        },
        BKEMachine: &bkev1beta1.BKEMachine{},
    }
    result, stopped := r.handlePauseAndFinalizer(objects, log)
    assert.True(t, stopped)  // 停止协调
})
```

### 十、总结

**暂停状态判断的多层机制**：

1. **Predicate 层**：
   - 在事件过滤阶段阻止暂停对象的 Reconcile
   - 减少不必要的协调开销

2. **Reconcile 层**：
   - 在协调开始时检查暂停状态
   - 使用 Cluster API 标准的 `IsPaused()` 函数
   - 支持集群级别和 Machine 级别的暂停

3. **Phase 层**：
   - 同步暂停状态到所有子资源
   - 包括 Commands、KCP、MD 等
   - 使用 annotation 标记实际生效状态

**关键设计**：
- ✅ 多层判断确保暂停生效
- ✅ 与 Cluster API 标准兼容
- ✅ 使用 annotation 确保状态一致性
- ✅ 支持暂停/恢复的完整生命周期
- ✅ 特殊场景的保护机制（如 Addon 部署时不允许暂停）

这种多层判断机制确保了暂停状态的可靠性和一致性，同时与 Cluster API 生态保持兼容。
        

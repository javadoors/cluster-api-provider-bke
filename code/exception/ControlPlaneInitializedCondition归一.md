#将两个层面的 ControlPlaneInitializedCondition 重构为一个状态的方案

## 重构方案：统一 ControlPlaneInitializedCondition

### 一、当前问题分析

#### 1. **两个 Condition 的使用情况**

| Condition | 定义位置 | 设置者 | 使用场景 |
|-----------|---------|--------|----------|
| `clusterv1.ControlPlaneInitializedCondition` | CAPI 标准 | KubeadmControlPlane Controller | 30+ 处检查，核心判断依据 |
| `bkev1beta1.ControlPlaneInitializedCondition` | BKE 自定义 | EnsureMasterInit Phase | 仅 6 处设置，无独立检查 |

#### 2. **冗余性问题**

BKECluster 的 `ControlPlaneInitializedCondition` **只是简单同步** CAPI Cluster 的状态：
```go
// ensure_master_init.go:229-231
if conditions.IsTrue(params.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
    params.Ctx.Log.Info(constant.MasterInitReason, "ClusterAPI Cluster obj already initialized")
    // 只是同步，没有独立逻辑
    condition.ConditionMark(params.Ctx.BKECluster, bkev1beta1.ControlPlaneInitializedCondition, 
        confv1beta1.ConditionTrue, "", "")
    return true, nil
}
```
**问题**：
- ❌ 维护两个状态增加复杂度
- ❌ 需要手动同步，容易出错
- ❌ BKECluster Condition 没有独立使用价值
- ❌ 增加了代码理解和维护成本

### 二、重构方案

#### 方案 A：完全移除 BKECluster Condition（推荐）

**核心思路**：所有地方统一使用 CAPI Cluster 的 `ControlPlaneInitializedCondition`

##### 1. **删除 BKECluster Condition 定义**

```go
// api/capbke/v1beta1/bkecluster_types.go
// 删除以下定义
const ControlPlaneInitializedCondition = "ControlPlaneInitialized"
```

##### 2. **修改 ensure_master_init.go**

```go
// 删除 setupConditionAndRefresh 中的设置
func (e *EnsureMasterInit) setupConditionAndRefresh(params SetupConditionAndRefreshParams) error {
    // 删除这行
    // condition.ConditionMark(params.Ctx.BKECluster, bkev1beta1.ControlPlaneInitializedCondition, ...)
    
    // 只需要刷新状态
    if err := params.Ctx.RefreshCtxBKECluster(); err != nil {
        return err
    }
    return nil
}

// 修改 checkClusterInitializedStep
func (e *EnsureMasterInit) checkClusterInitializedStep(...) (bool, bool, error) {
    // 刷新 Cluster 对象
    if err := params.Ctx.RefreshCtxCluster(); err != nil {
        return false, false, err
    }
    
    // 直接使用 CAPI Cluster 的 Condition
    if conditions.IsTrue(params.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
        params.Ctx.Log.Info(constant.MasterInitReason, "Cluster already initialized")
        // 删除 BKECluster 的同步设置
        return true, true, nil
    }
    
    return false, false, nil
}

// 删除 defer 中的 BKECluster Condition 设置
defer func() {
    if derr := e.Ctx.RefreshCtxCluster(); derr != nil {
        e.Ctx.Log.Error(constant.MasterNotInitReason, "Get Cluster obj failed, err: %v", derr)
        err = derr
    }
    // 删除 BKECluster Condition 的设置逻辑
}()
```

##### 3. **提供辅助函数**

```go
// pkg/phaseframe/phaseutil/clusterapi.go

// IsControlPlaneInitialized 检查控制平面是否已初始化
func IsControlPlaneInitialized(cluster *clusterv1.Cluster) bool {
    if cluster == nil {
        return false
    }
    return conditions.IsTrue(cluster, clusterv1.ControlPlaneInitializedCondition)
}

// WaitForControlPlaneInitialized 等待控制平面初始化（可选）
func WaitForControlPlaneInitialized(ctx context.Context, client client.Client, cluster *clusterv1.Cluster, timeout time.Duration) error {
    return wait.PollImmediateUntil(2*time.Second, func() (bool, error) {
        if err := client.Get(ctx, client.ObjectKeyFromObject(cluster), cluster); err != nil {
            return false, err
        }
        return IsControlPlaneInitialized(cluster), nil
    }, ctx.Done())
}
```

##### 4. **统一使用辅助函数**

```go
// 所有检查点统一使用
if !phaseutil.IsControlPlaneInitialized(cluster) {
    // 等待控制平面初始化
    return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}
```

#### 方案 B：保留 BKECluster Condition 但改为派生状态（备选）

如果需要在 BKECluster 状态中体现控制平面初始化状态，可以使用**派生属性**：
```go
// api/capbke/v1beta1/bkecluster_types.go

type BKEClusterStatus struct {
    // ... 其他字段
    
    // 控制平面状态（派生自 CAPI Cluster）
    ControlPlaneInitialized bool `json:"controlPlaneInitialized"`
}

// 在 Reconcile 中自动同步
func (r *BKEClusterReconciler) syncControlPlaneStatus(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, cluster *clusterv1.Cluster) {
    bkeCluster.Status.ControlPlaneInitialized = conditions.IsTrue(cluster, clusterv1.ControlPlaneInitializedCondition)
}
```
**优点**：
- ✅ BKECluster 状态中可见控制平面状态
- ✅ 不需要手动同步
- ✅ 减少维护负担

**缺点**：
- ⚠️ 仍然有冗余
- ⚠️ 需要确保同步时机正确

### 三、重构步骤

#### 阶段 1：准备工作

1. **添加辅助函数**
   ```go
   // pkg/phaseframe/phaseutil/clusterapi.go
   func IsControlPlaneInitialized(cluster *clusterv1.Cluster) bool
   ```

2. **逐步替换检查点**
   - 将所有 `conditions.IsTrue(cluster, clusterv1.ControlPlaneInitializedCondition)` 
   - 替换为 `phaseutil.IsControlPlaneInitialized(cluster)`

#### 阶段 2：移除 BKECluster Condition

1. **删除设置代码**
   - 移除 `ensure_master_init.go` 中所有 `bkev1beta1.ControlPlaneInitializedCondition` 的设置

2. **删除定义**
   - 移除 `bkev1beta1.ControlPlaneInitializedCondition` 常量定义

3. **更新测试**
   - 移除相关测试代码
   - 更新测试断言

#### 阶段 3：验证与清理

1. **运行完整测试套件**
2. **验证集群创建、扩缩容、升级流程**
3. **更新文档**

### 四、重构后的代码示例

#### 1. **ensure_master_init.go 简化后**

```go
func (e *EnsureMasterInit) checkClusterInitializedStep(params MasterInitPollParams, pollCount int) (bool, bool, error) {
    _, _, _, _, log := params.Ctx.Untie()

    // 刷新 Cluster 对象
    if err := params.Ctx.RefreshCtxCluster(); err != nil {
        log.Error(constant.InternalErrorReason, "Refresh Cluster obj failed, err: %v", err)
        return false, false, err
    }

    // 直接检查 CAPI Cluster 状态
    if phaseutil.IsControlPlaneInitialized(params.Ctx.Cluster) {
        log.Info(constant.MasterInitReason, "Cluster already initialized")
        return true, true, nil
    }

    return false, false, nil
}
```

#### 2. **bkemachine_controller_phases.go 简化后**

```go
func (r *BKEMachineReconciler) handleFirstTimeReconciliation(params BootstrapReconcileParams) (ctrl.Result, error) {
    // Worker 节点等待控制平面初始化
    if !util.IsControlPlaneMachine(params.Machine) && 
       !phaseutil.IsControlPlaneInitialized(params.Cluster) {
        params.Log.Info("Waiting for the control plane to be initialized")
        conditions.MarkFalse(params.BKEMachine, bkev1beta1.BootstrapSucceededCondition,
            clusterv1.WaitingForControlPlaneAvailableReason, clusterv1.ConditionSeverityInfo, "")
        return ctrl.Result{}, nil
    }
    
    // ... 其他逻辑
}
```

### 五、影响范围评估

#### 需要修改的文件：

| 文件 | 修改内容 | 影响程度 |
|------|---------|----------|
| `ensure_master_init.go` | 移除 BKECluster Condition 设置 | 高 |
| `bkemachine_controller_phases.go` | 使用辅助函数 | 中 |
| `phaseutil/clusterapi.go` | 添加辅助函数 | 低 |
| `bkecluster_consts.go` | 删除常量定义 | 低 |
| `*_test.go` | 更新测试代码 | 中 |

#### 不受影响的部分：

- ✅ CAPI Cluster 的 Condition 设置（由 KubeadmControlPlane Controller 管理）
- ✅ 其他 Phase 的逻辑
- ✅ BKEMachine 的 Bootstrap 流程

### 六、总结

**重构收益**：
1. **简化状态管理**：从两个 Condition 减少到一个
2. **消除同步负担**：不再需要手动同步状态
3. **提高代码清晰度**：统一使用 CAPI 标准 Condition
4. **减少维护成本**：减少约 20 行代码
5. **降低出错风险**：避免状态不一致问题

**推荐方案**：**方案 A - 完全移除 BKECluster Condition**

这是最简洁、最符合 Cluster API 标准的方案。BKECluster 可以通过 `PhaseStatus` 或其他方式体现控制平面状态，而不需要维护冗余的 Condition。
        

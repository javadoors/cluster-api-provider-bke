# BKECluster
## BasePhase
### 通用执行条件
- 对于删除的BKECluster，不执行（DeletionTimestamp）
- 对于暂停的BKECluster，不执行（Spec.Pause || annotations.HasPaused()）
- 对于DryRun的BKECluster，不执行（Spec.DryRun）
- 不健康的BKECluster不执行(Status.ClusterHealthState)
- 对于不是BKECluster，并且没有完全控制的，不执行(??)
### Phase
```go
	PhaseSucceeded confv1beta1.BKEClusterPhaseStatus = "Succeeded"
	PhaseFailed    confv1beta1.BKEClusterPhaseStatus = "Failed"
	PhaseUnknown   confv1beta1.BKEClusterPhaseStatus = "Unknown"
	PhaseWaiting   confv1beta1.BKEClusterPhaseStatus = "Waiting"
	PhaseRunning   confv1beta1.BKEClusterPhaseStatus = "Running"
	PhaseSkipped   confv1beta1.BKEClusterPhaseStatus = "Skipped"
```
- 执行前设置：bkev1beta1.PhaseWaiting
- 执行前设置：bkev1beta1.PhaseWaiting


# `!clusterutil.IsBKECluster(new) && !clusterutil.FullyControlled(new) `
      
根据代码分析，这个条件判断的作用是**过滤掉不应该执行 Phase 的集群类型**。

## 条件解析

### 完整条件
```go
if !clusterutil.IsBKECluster(new) && !clusterutil.FullyControlled(new) {
    return false
}
```
**逻辑含义**：如果集群**不是 BKE 类型**，并且**不是完全控制**的集群，则**不执行该 Phase**

## 函数详解

### 1. IsBKECluster() - 判断是否是 BKE 集群
**代码位置**：[helper.go:43-46](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\utils\capbke\clusterutil\helper.go#L43-L46)
```go
func IsBKECluster(bkeCluster client.Object) bool {
    v, ok := annotation.HasAnnotation(bkeCluster, common.BKEClusterFromAnnotationKey)
    return !ok || v == common.BKEClusterFromAnnotationValueBKE || v == ""
}
```
**判断逻辑**：
- Annotation `bke.bocloud.com/cluster-from` **不存在** → 返回 `true`（默认是 BKE 集群）
- Annotation 值为 `"bke"` 或 `""` → 返回 `true`（BKE 集群）
- Annotation 值为 `"bocloud"` 或 `"other"` → 返回 `false`（非 BKE 集群）

**集群类型标识**：
```go
BKEClusterFromAnnotationKey          = "bke.bocloud.com/cluster-from"
BKEClusterFromAnnotationValueBKE     = "bke"      // BKE 自建集群
BKEClusterFromAnnotationValueBocloud = "bocloud"  // Bocloud 托管集群
BKEClusterFromAnnotationValueOther   = "other"    // 其他类型集群
```

### 2. FullyControlled() - 判断是否完全控制
**代码位置**：[helper.go:27-39](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\utils\capbke\clusterutil\helper.go#L27-L39)
```go
func FullyControlled(bkeCluster client.Object) bool {
    if IsBKECluster(bkeCluster) {
        return true  // BKE 集群默认完全控制
    }
    if IsOtherCluster(bkeCluster) {
        return false  // Other 集群不控制
    }
    if IsBocloudCluster(bkeCluster) {
        v, ok := annotation.HasAnnotation(bkeCluster, annotation.KONKFullManagementClusterAnnotationKey)
        return ok && v == "true"  // Bocloud 集群需要检查是否标记为完全控制
    }
    return false
}
```
**判断逻辑**：
- **BKE 集群** → 返回 `true`（默认完全控制）
- **Other 集群** → 返回 `false`（不控制）
- **Bocloud 集群** → 检查 Annotation `bke.bocloud.com/full-management-cluster` 是否为 `"true"`

## 条件判断矩阵

| 集群类型 | `IsBKECluster` | `FullyControlled` | `!IsBKECluster && !FullyControlled` | 是否执行 Phase |
|---------|---------------|------------------|-------------------------------------|--------------|
| **BKE 集群** | `true` | `true` | `false && false` = `false` | ✅ **执行** |
| **Bocloud 集群（完全控制）** | `false` | `true` | `true && false` = `false` | ✅ **执行** |
| **Bocloud 集群（部分控制）** | `false` | `false` | `true && true` = `true` | ❌ **不执行** |
| **Other 集群** | `false` | `false` | `true && true` = `true` | ❌ **不执行** |

## 条件作用详解

### 代码位置
**文件**：[base.go:143-155](file:///c:\Users\z00820145\code\github\cluster-api-provider-bke\pkg\phaseframe\base.go#L143-L155)
```go
// DefaultNeedExecute is the default implementation of NeedExecute, use on demand
// it's only used for BKECluster type of 'bke'
func (b *BasePhase) DefaultNeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
    // 检查通用条件
    if !b.checkCommonNeedExecute(new) {
        return false
    }

    // 对于不是BKECluster，并且没有完全控制的，不执行
    if !clusterutil.IsBKECluster(new) && !clusterutil.FullyControlled(new) {
        return false
    }

    return true
}
```

### 作用说明
这个条件用于 **Phase 执行前的过滤**，确保只有符合条件的集群才会执行该 Phase：

#### 1. **允许执行的集群类型**
- ✅ **BKE 自建集群**：完全由 BKE 管理的集群
- ✅ **Bocloud 完全控制集群**：标记为 `full-management-cluster=true` 的 Bocloud 集群

#### 2. **不允许执行的集群类型**
- ❌ **Bocloud 部分控制集群**：只管理部分资源的 Bocloud 集群
- ❌ **Other 集群**：其他类型的集群（如导入的集群）

## 实际应用场景

### 场景 1：BKE 自建集群
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: my-cluster
  annotations:
    bke.bocloud.com/cluster-from: "bke"  # 或者不设置这个 annotation
```
**结果**：`IsBKECluster=true` → 条件为 `false` → **执行 Phase**

### 场景 2：Bocloud 完全控制集群
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: my-cluster
  annotations:
    bke.bocloud.com/cluster-from: "bocloud"
    bke.bocloud.com/full-management-cluster: "true"
```
**结果**：`IsBKECluster=false`, `FullyControlled=true` → 条件为 `false` → **执行 Phase**

### 场景 3：Bocloud 部分控制集群
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: my-cluster
  annotations:
    bke.bocloud.com/cluster-from: "bocloud"
    bke.bocloud.com/full-management-cluster: "false"  # 或者不设置
```
**结果**：`IsBKECluster=false`, `FullyControlled=false` → 条件为 `true` → **不执行 Phase**

### 场景 4：Other 集群
```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: my-cluster
  annotations:
    bke.bocloud.com/cluster-from: "other"
```
**结果**：`IsBKECluster=false`, `FullyControlled=false` → 条件为 `true` → **不执行 Phase**

## 设计意图
这个条件的设计意图是：
1. **支持多种集群类型**：
   - BKE 自建集群：完全控制
   - Bocloud 托管集群：可选择完全控制或部分控制
   - Other 集群：不控制
2. **灵活的控制粒度**：
   - 对于 Bocloud 集群，可以通过 Annotation 选择是否完全控制
   - 部分控制的集群可能只需要管理部分资源（如监控、日志）
3. **避免误操作**：
   - 对于不应该控制的集群，跳过 Phase 执行
   - 防止对导入集群或其他类型集群造成影响

## 总结
**条件 `!clusterutil.IsBKECluster(new) && !clusterutil.FullyControlled(new)` 的作用**：
- **过滤条件**：排除不应该执行 Phase 的集群
- **执行范围**：只对 BKE 集群和完全控制的 Bocloud 集群执行
- **跳过范围**：跳过部分控制的 Bocloud 集群和 Other 集群

这是 Phase 框架中的**重要安全机制**，确保 Phase 只在合适的集群类型上执行，避免对不该管理的集群造成影响。
        

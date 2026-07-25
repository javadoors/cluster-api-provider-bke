# 统一集群升级方案设计

## 1. 问题背景

### 1.1 当前现状

目前 ReleaseImage 中的组件都是业务集群的，缺少管理集群的组件定义：

- **业务集群**：包含 etcd、kubernetes-master、kubernetes-worker、containerd、bkeagent 等组件
- **管理集群**：缺少 provider (bke-controller-manager)、install-service、capi-controller-manager 等组件

### 1.2 核心诉求

设计一个方案，同时支持管理集群和业务集群的升级，实现：

1. **统一升级框架**：复用现有的 ReleaseImage 和 DAG 升级框架
2. **自动化升级**：自动检测集群类型，执行对应的升级流程
3. **差异化升级**：管理集群和业务集群升级不同的组件集合
4. **向后兼容**：保持现有业务集群升级流程不变

### 1.3 约束条件

1. 业务集群会被管理集群纳管，即在管理集群中有业务集群相关的 BKECluster 等资源
2. 管理集群也被管理集群纳管，即在管理集群中有管理集群相关的 BKECluster 等资源
3. 判断集群是否为管理集群，可通过是否存在 bke-controller-manager Deployment

---

## 2. 核心思路

**同一个 ReleaseImage CRD，不同的实例**：

- 业务集群使用 `bke-workload-v2.0.0` 实例
- 管理集群使用 `bke-management-v2.0.0` 实例
- 通过标签（label）区分集群类型
- 升级时根据集群类型自动选择对应的 ReleaseImage 实例
- 完全复用现有的升级框架，无需修改 API

### 2.1 设计原则

1. **同构架构**：管理集群与业务集群具有相同的架构，使用同一套升级框架
2. **实例分离**：通过不同的 ReleaseImage 实例定义不同集群类型的组件
3. **标签选择**：使用 Kubernetes 原生标签机制区分集群类型
4. **框架复用**：完全复用现有的 DAG 升级框架，无需修改 API

### 2.2 架构概览

```
ReleaseImage CRD (统一)
    │
    ├── bke-workload-v2.0.0 (业务集群实例)
    │   ├── labels: cluster-type=workload, release-version=2.0.0
    │   └── components: etcd, kubernetes-master, kubernetes-worker, ...
    │
    └── bke-management-v2.0.0 (管理集群实例)
        ├── labels: cluster-type=management, release-version=2.0.0
        └── components: provider, install-service, capi-controller-manager, ...

升级流程:
    │
    ├── 1. 检测集群类型
    │   └── 检查 bke-controller-manager Deployment
    │
    ├── 2. 选择 ReleaseImage 实例
    │   └── 根据集群类型和版本选择对应的实例
    │
    ├── 3. 构建 DAG
    │   └── 使用选中实例的组件列表
    │
    └── 4. 执行升级
        └── 按 DAG 依赖顺序执行
```

### 2.3 方案对比

| 维度 | 方案 A（ClusterType 字段） | 方案 B（不同实例） |
|------|--------------------------|-------------------|
| **API 复杂度** | 需要新增字段 | 无需修改 API |
| **ReleaseImage 复杂度** | 单个实例包含所有组件 | 每个实例只包含对应组件 |
| **职责清晰度** | 混合在一起 | 清晰分离 |
| **版本管理** | 复杂 | 灵活独立 |
| **升级框架复用** | 需要修改过滤逻辑 | 完全复用 |
| **扩展性** | 需要修改 API | 只需创建新实例 |

**选择方案 B**，理由：
- ReleaseImage 保持简单，每个实例职责清晰
- 无需修改 API，向后兼容性更好
- 管理集群和业务集群可以独立版本管理
- 完全复用现有升级框架，改动最小

---

## 3. ReleaseImage 实例设计

### 3.1 业务集群 ReleaseImage

```yaml
apiVersion: v1alpha1
kind: ReleaseImage
metadata:
  name: bke-workload-v2.0.0
  namespace: bke-system
  labels:
    cluster-type: workload
    release-version: "2.0.0"
spec:
  version: "2.0.0"
  digest: "sha256:abc123..."
  
  upgrade:
    components:
      - name: pre-upgrade-resources
        version: "2.0.0"
        inline:
          handler: EnsurePreUpgradeResources
      
      - name: bkeagent
        version: "2.0.0"
        inline:
          handler: EnsureAgentUpgrade
      
      - name: etcd
        version: "3.5.12"
        inline:
          handler: EnsureEtcdUpgrade
      
      - name: kubernetes-master
        version: "1.29.0"
        inline:
          handler: EnsureMasterUpgrade
      
      - name: kubernetes-worker
        version: "1.29.0"
        inline:
          handler: EnsureWorkerUpgrade
      
      - name: containerd
        version: "1.7.13"
        inline:
          handler: EnsureContainerdUpgrade
```

### 3.2 管理集群 ReleaseImage

```yaml
apiVersion: v1alpha1
kind: ReleaseImage
metadata:
  name: bke-management-v2.0.0
  namespace: bke-system
  labels:
    cluster-type: management
    release-version: "2.0.0"
spec:
  version: "2.0.0"
  digest: "sha256:def456..."
  
  upgrade:
    components:
      - name: pre-upgrade-resources
        version: "2.0.0"
        inline:
          handler: EnsurePreUpgradeResources
      
      - name: provider
        version: "2.0.0"
        manifest: "provider/v2.0.0/component.yaml"
      
      - name: install-service
        version: "2.0.0"
        inline:
          handler: EnsureInstallServiceUpgrade
      
      - name: capi-controller-manager
        version: "2.0.0"
        manifest: "capi-controller-manager/v2.0.0/component.yaml"
```

### 3.3 标签规范

| 标签 | 值 | 说明 |
|------|-----|------|
| `cluster-type` | `workload` / `management` | 集群类型 |
| `release-version` | `2.0.0` | 版本号 |

### 3.4 命名规范

- 业务集群：`bke-workload-<version>`
- 管理集群：`bke-management-<version>`

---

## 4. 升级流程改造

### 4.1 扩展 PhaseContext

在 `pkg/phaseframe/context.go` 中添加集群类型字段：

```go
// ClusterType 定义集群类型
type ClusterType string

const (
    ClusterTypeManagement ClusterType = "management"
    ClusterTypeWorkload   ClusterType = "workload"
)

// PhaseContext 添加集群类型字段
type PhaseContext struct {
    // ... 现有字段
    
    clusterType ClusterType
}

// SetClusterType 设置集群类型
func (pc *PhaseContext) SetClusterType(clusterType ClusterType) {
    pc.clusterType = clusterType
}

// GetClusterType 获取集群类型
func (pc *PhaseContext) GetClusterType() ClusterType {
    return pc.clusterType
}

// IsManagementCluster 判断是否为管理集群
func (pc *PhaseContext) IsManagementCluster() bool {
    return pc.clusterType == ClusterTypeManagement
}

// IsWorkloadCluster 判断是否为业务集群
func (pc *PhaseContext) IsWorkloadCluster() bool {
    return pc.clusterType == ClusterTypeWorkload
}
```

### 4.2 修改升级流程

在 `controllers/capbke/bkecluster_upgrade_dag.go` 中修改 `executeUpgradeDAG`：

```go
func (r *BKEClusterReconciler) executeUpgradeDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldBkeCluster, bkeCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger,
) (ctrl.Result, error) {
    // 1. 检测集群类型
    clusterType, err := r.detectClusterType(ctx, phaseCtx)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 2. 将集群类型存入 PhaseContext
    phaseCtx.SetClusterType(clusterType)
    
    bkeLogger.Info("detected cluster type: %s", clusterType)
    
    // 3. 根据集群类型选择对应的 ReleaseImage
    releaseImage, err := r.resolveReleaseImageByClusterType(
        ctx,
        bkeCluster,
        clusterType,
    )
    if err != nil {
        return ctrl.Result{}, err
    }
    
    bkeLogger.Info("selected ReleaseImage: %s", releaseImage.Name)
    
    // 4. 构建 DAG（复用现有逻辑）
    dag, err := topology.BuildUpgradeDAG(
        releaseImage.Spec.Upgrade.Components,
        resolveDependency,
    )
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 5. 执行 DAG
    scheduler := dagexec.NewScheduler(dagexec.Config{
        MaxParallel: 8,
        Logger:      bkeLogger,
    })
    
    return scheduler.ExecuteDAG(ctx, phaseCtx, oldBkeCluster, bkeCluster, dag)
}

// detectClusterType 检测集群类型
func (r *BKEClusterReconciler) detectClusterType(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
) (ClusterType, error) {
    // 检查 bke-controller-manager Deployment 是否存在
    target := phaseutil.DeploymentTarget{
        Namespace: "cluster-system",
        Name:      "bke-controller-manager",
        Container: "manager",
    }
    
    _, err := phaseutil.GetDeploymentImage(ctx, phaseCtx.Client, target)
    if err != nil {
        if apierrors.IsNotFound(err) {
            return ClusterTypeWorkload, nil  // 业务集群
        }
        return "", err
    }
    
    return ClusterTypeManagement, nil  // 管理集群
}

// resolveReleaseImageByClusterType 根据集群类型选择 ReleaseImage
func (r *BKEClusterReconciler) resolveReleaseImageByClusterType(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    clusterType ClusterType,
) (*cvv1alpha1.ReleaseImage, error) {
    // 使用标签选择器查找对应的 ReleaseImage
    labelSelector := labels.SelectorFromSet(labels.Set{
        "cluster-type":    string(clusterType),
        "release-version": bkeCluster.Spec.OpenFuyaoVersion,
    })
    
    releaseImageList := &cvv1alpha1.ReleaseImageList{}
    if err := r.List(ctx, releaseImageList, &client.ListOptions{
        LabelSelector: labelSelector,
        Namespace:     bkeCluster.Namespace,
    }); err != nil {
        return nil, err
    }
    
    if len(releaseImageList.Items) == 0 {
        return nil, fmt.Errorf(
            "no ReleaseImage found for cluster type %s version %s",
            clusterType,
            bkeCluster.Spec.OpenFuyaoVersion,
        )
    }
    
    if len(releaseImageList.Items) > 1 {
        return nil, fmt.Errorf(
            "multiple ReleaseImages found for cluster type %s version %s",
            clusterType,
            bkeCluster.Spec.OpenFuyaoVersion,
        )
    }
    
    return &releaseImageList.Items[0], nil
}
```

---

## 5. 升级流程对比

### 5.1 管理集群升级流程

```
检测集群类型: management
    │
    ├── 选择 ReleaseImage: bke-management-v2.0.0
    │
    └── 执行 DAG:
        Batch 1: [pre-upgrade-resources]
        Batch 2: [provider]
        Batch 3: [install-service, capi-controller-manager]
```

### 5.2 业务集群升级流程

```
检测集群类型: workload
    │
    ├── 选择 ReleaseImage: bke-workload-v2.0.0
    │
    └── 执行 DAG:
        Batch 1: [pre-upgrade-resources]
        Batch 2: [bkeagent]
        Batch 3: [etcd]
        Batch 4: [kubernetes-master]
        Batch 5: [kubernetes-worker]
        Batch 6: [containerd]
```

### 5.3 对比表

| 集群类型 | 检测方式 | ReleaseImage 实例 | 升级组件 |
|---------|---------|------------------|---------|
| **管理集群** | bke-controller-manager 存在 | bke-management-v2.0.0 | provider → install-service → capi-controller-manager |
| **业务集群** | bke-controller-manager 不存在 | bke-workload-v2.0.0 | bkeagent → etcd → kubernetes-master → kubernetes-worker → containerd |

---

## 6. 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 集群类型检测时机 | 升级流程开始时 | 避免重复检测，统一存储 |
| ReleaseImage 选择方式 | 标签选择器 | 利用 Kubernetes 原生机制 |
| 集群类型存储位置 | PhaseContext | 便于各 Phase 访问 |
| 向后兼容性 | 无需修改 API | 使用标签区分实例 |
| 版本管理 | 独立版本 | 管理集群和业务集群可以独立版本 |

---

## 7. 实施步骤

### 7.1 阶段一：PhaseContext 扩展（0.5 天）

1. 在 `pkg/phaseframe/context.go` 中添加 `ClusterType` 类型和常量
2. 在 `PhaseContext` 中添加 `clusterType` 字段
3. 添加 `SetClusterType`、`GetClusterType`、`IsManagementCluster`、`IsWorkloadCluster` 方法

### 7.2 阶段二：升级流程改造（1.5 天）

1. 实现 `detectClusterType` 函数
2. 实现 `resolveReleaseImageByClusterType` 函数
3. 修改 `executeUpgradeDAG` 流程
4. 添加日志记录

### 7.3 阶段三：创建 ReleaseImage 实例（0.5 天）

1. 创建 `bke-workload-v2.0.0` 实例
2. 创建 `bke-management-v2.0.0` 实例
3. 添加标签

### 7.4 阶段四：测试验证（2 天）

1. 单元测试：集群类型检测
2. 单元测试：ReleaseImage 选择
3. 集成测试：管理集群升级
4. 集成测试：业务集群升级
5. 端到端测试：完整升级流程

### 7.5 阶段五：文档更新（0.5 天）

1. 更新 ReleaseImage 使用文档
2. 更新升级流程文档
3. 添加使用示例

**总计：5 天**

---

## 8. 优势

### 8.1 同构架构

- 管理集群与业务集群使用相同的 ReleaseImage CRD
- 复用现有的升级框架
- 降低维护成本

### 8.2 职责清晰

- 每个 ReleaseImage 实例只包含对应集群类型的组件
- 避免单个实例过于臃肿
- 便于理解和维护

### 8.3 灵活独立

- 管理集群和业务集群可以独立版本管理
- 可以独立发布和升级
- 降低耦合度

### 8.4 向后兼容

- 无需修改 ReleaseImage API
- 保持现有业务集群升级流程不变
- 平滑迁移到新版本

### 8.5 易于扩展

- 新增集群类型只需创建新的 ReleaseImage 实例
- 无需修改代码
- 扩展性好

---

## 9. 风险与缓解

### 9.1 风险识别

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| 集群类型检测失败 | 升级中断 | 低 | 添加重试机制，检测失败时降级为业务集群 |
| ReleaseImage 实例缺失 | 升级失败 | 中 | 添加详细的错误提示，指导用户创建实例 |
| 标签选择器匹配多个实例 | 升级失败 | 低 | 添加校验逻辑，确保唯一匹配 |
| 管理集群组件升级失败 | 管理面不可用 | 中 | 添加回滚机制，支持手动恢复 |

### 9.2 测试策略

1. **单元测试**：覆盖集群类型检测、ReleaseImage 选择逻辑
2. **集成测试**：验证管理集群和业务集群的升级流程
3. **端到端测试**：完整升级流程验证
4. **回归测试**：确保现有业务集群升级流程不受影响

---

## 10. 总结

本方案通过**同一个 ReleaseImage CRD，不同的实例**的设计，实现了管理集群和业务集群的统一升级框架。核心特点包括：

1. **同构架构**：管理集群与业务集群使用相同的 CRD 和升级框架
2. **实例分离**：通过不同的实例定义不同集群类型的组件
3. **标签选择**：使用 Kubernetes 原生标签机制区分集群类型
4. **框架复用**：完全复用现有的 DAG 升级框架，无需修改 API

该方案具有良好的扩展性和向后兼容性，能够满足未来多集群类型升级的需求。

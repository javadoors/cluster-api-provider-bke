# 统一集群升级方案设计

## 1. 问题背景

### 1.1 当前现状

目前 ReleaseImage 中的组件都是业务集群的，缺少管理集群的组件定义：

- **业务集群**：包含 etcd、kubernetes-master、kubernetes-worker、containerd、bkeagent 等组件
- **管理集群**：缺少 provider (bke-controller-manager)、install-service、capi-controller-manager 等组件

### 1.2 核心诉求

设计一个方案，在 ReleaseImage 中同时定义管理集群和业务集群的组件，实现：

1. **统一版本管理**：一个 ReleaseImage 定义所有组件的版本
2. **自动化升级**：自动检测集群类型，执行对应的升级流程
3. **差异化升级**：管理集群和业务集群升级不同的组件集合
4. **向后兼容**：保持现有业务集群升级流程不变

### 1.3 约束条件

1. 业务集群会被管理集群纳管，即在管理集群中有业务集群相关的 BKECluster 等资源
2. 管理集群也被管理集群纳管，即在管理集群中有管理集群相关的 BKECluster 等资源
3. 判断集群是否为管理集群，可通过是否存在 bke-controller-manager Deployment

---

## 2. 核心思路

在 ReleaseImage 中同时定义管理集群和业务集群的组件，通过**集群类型检测**和**组件过滤**实现差异化升级。

### 2.1 设计原则

1. **统一入口**：一个 ReleaseImage 定义所有组件
2. **自动检测**：升级流程开始时自动检测集群类型
3. **智能过滤**：根据集群类型过滤出需要的组件
4. **独立执行**：管理集群和业务集群独立升级，互不影响

### 2.2 架构概览

```
ReleaseImage (统一版本定义)
    │
    ├── 管理集群组件
    │   ├── provider (bke-controller-manager)
    │   ├── install-service
    │   └── capi-controller-manager
    │
    └── 业务集群组件
        ├── etcd
        ├── kubernetes-master
        ├── kubernetes-worker
        ├── containerd
        └── bkeagent

升级流程:
    │
    ├── 1. 检测集群类型
    │   └── 检查 bke-controller-manager Deployment
    │
    ├── 2. 过滤组件
    │   └── 根据集群类型过滤 ReleaseImage 中的组件
    │
    ├── 3. 构建 DAG
    │   └── 只包含当前集群类型需要的组件
    │
    └── 4. 执行升级
        └── 按 DAG 依赖顺序执行
```

---

## 3. API 扩展

### 3.1 扩展 ReleaseImageUpgradeComponent

在 `api/v1alpha1/releaseimage_types.go` 中添加集群类型字段：

```go
// ReleaseImageUpgradeComponent 添加集群类型字段
type ReleaseImageUpgradeComponent struct {
    Name        string                     `json:"name,omitempty"`
    Version     string                     `json:"version,omitempty"`
    Inline      *ReleaseImageUpgradeInline `json:"inline,omitempty"`
    ClusterType ClusterType                `json:"clusterType,omitempty"` // 新增
}

// ClusterType 定义组件适用的集群类型
type ClusterType string

const (
    ClusterTypeManagement ClusterType = "management" // 管理集群专用
    ClusterTypeWorkload   ClusterType = "workload"   // 业务集群专用
    ClusterTypeAll        ClusterType = "all"        // 所有集群
)
```

### 3.2 扩展组件目录

在 `pkg/upgrade/catalog.go` 中添加管理集群组件：

```go
// 新增组件常量
const (
    ComponentInstallService      = "install-service"
    ComponentCAPIController      = "capi-controller-manager"
)

// 新增内联处理器常量
const (
    InlineHandlerInstallServiceUpgrade = "EnsureInstallServiceUpgrade"
)

// DeclarativeUpgradeCatalog 添加管理集群组件
var DeclarativeUpgradeCatalog = []UpgradeComponentSpec{
    // 管理集群组件
    {
        Name:          ComponentProvider,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionManifest,
        ClusterType:   ClusterTypeManagement,
        ManifestPath:  ManifestComponentManifestPath(ComponentProvider, ComponentManifestVersion),
        LegacyPhase:   "EnsureProviderSelfUpgrade",
    },
    {
        Name:          ComponentInstallService,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        ClusterType:   ClusterTypeManagement,
        InlineHandler: InlineHandlerInstallServiceUpgrade,
    },
    {
        Name:          ComponentCAPIController,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionManifest,
        ClusterType:   ClusterTypeManagement,
        ManifestPath:  ManifestComponentManifestPath(ComponentCAPIController, ComponentManifestVersion),
    },
    
    // 所有集群组件
    {
        Name:          ComponentPreUpgradeResources,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        ClusterType:   ClusterTypeAll,
        LegacyPhase:   InlineHandlerPreUpgradeResources,
        InlineHandler: InlineHandlerPreUpgradeResources,
    },
    {
        Name:          ComponentBKEAgent,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        ClusterType:   ClusterTypeAll,
        LegacyPhase:   InlineHandlerAgentUpgrade,
        InlineHandler: InlineHandlerAgentUpgrade,
    },
    
    // 业务集群组件
    {
        Name:          ComponentEtcd,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        ClusterType:   ClusterTypeWorkload,
        LegacyPhase:   InlineHandlerEtcdUpgrade,
        InlineHandler: InlineHandlerEtcdUpgrade,
    },
    {
        Name:          ComponentKubernetesMaster,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        ClusterType:   ClusterTypeWorkload,
        LegacyPhase:   InlineHandlerMasterUpgrade,
        InlineHandler: InlineHandlerMasterUpgrade,
    },
    {
        Name:          ComponentKubernetesWorker,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        ClusterType:   ClusterTypeWorkload,
        LegacyPhase:   InlineHandlerWorkerUpgrade,
        InlineHandler: InlineHandlerWorkerUpgrade,
    },
    {
        Name:          ComponentContainerd,
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        ClusterType:   ClusterTypeWorkload,
        LegacyPhase:   InlineHandlerContainerdUpgrade,
        InlineHandler: InlineHandlerContainerdUpgrade,
    },
}
```

### 3.3 扩展 UpgradeComponentSpec

在 `pkg/upgrade/catalog.go` 中添加 ClusterType 字段：

```go
// UpgradeComponentSpec maps legacy phases to declarative upgrade entries.
type UpgradeComponentSpec struct {
    // Name is the ReleaseImage component name (VersionContext key and DAG node name).
    Name    string
    Version string
    Mode    UpgradeExecutionMode
    // ClusterType 定义组件适用的集群类型
    ClusterType ClusterType
    // ManifestPath is set for manifest-mode components (e.g. provider/v1.0.0/component.yaml).
    ManifestPath string
    // LegacyPhase is the pre-declarative BKECluster phase name, if any.
    LegacyPhase string
    // InlineHandler is the ComponentFactory handler key for inline mode.
    InlineHandler string
}
```

---

## 4. 升级流程改造

### 4.1 扩展 PhaseContext

在 `pkg/phaseframe/context.go` 中添加集群类型字段：

```go
// PhaseContext 添加集群类型字段
type PhaseContext struct {
    // ... 现有字段
    
    clusterType ClusterType  // 新增
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
    
    // 3. 解析 ReleaseImage
    hopTarget := r.getUpgradeHopTarget(bkeCluster)
    bundle, ri, err := r.resolveUpgradeBundle(ctx, bkeCluster, hopTarget)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 4. 过滤组件
    filteredComponents := filterComponentsByClusterType(
        bundle.Release.Spec.Upgrade.Components,
        clusterType,
    )
    
    bkeLogger.Info("filtered %d components for %s cluster", 
        len(filteredComponents), clusterType)
    
    // 5. 构建 DAG
    dag, err := topology.BuildUpgradeDAG(filteredComponents, resolveDependency)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 6. 执行 DAG
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

// filterComponentsByClusterType 过滤组件
func filterComponentsByClusterType(
    components []ReleaseImageUpgradeComponent,
    clusterType ClusterType,
) []ReleaseImageUpgradeComponent {
    var filtered []ReleaseImageUpgradeComponent
    
    for _, comp := range components {
        // ClusterTypeAll 的组件始终包含
        if comp.ClusterType == ClusterTypeAll {
            filtered = append(filtered, comp)
            continue
        }
        
        // 根据集群类型过滤
        if comp.ClusterType == clusterType {
            filtered = append(filtered, comp)
        }
    }
    
    return filtered
}
```

---

## 5. ReleaseImage 示例

### 5.1 完整示例

```yaml
apiVersion: v1alpha1
kind: ReleaseImage
metadata:
  name: bke-v2.0.0
  namespace: bke-system
spec:
  version: "2.0.0"
  digest: "sha256:abc123..."
  
  upgrade:
    components:
      # 管理集群组件
      - name: provider
        version: "2.0.0"
        clusterType: management
      
      - name: install-service
        version: "2.0.0"
        clusterType: management
        inline:
          handler: EnsureInstallServiceUpgrade
      
      - name: capi-controller-manager
        version: "2.0.0"
        clusterType: management
      
      # 所有集群组件
      - name: pre-upgrade-resources
        version: "2.0.0"
        clusterType: all
        inline:
          handler: EnsurePreUpgradeResources
      
      - name: bkeagent
        version: "2.0.0"
        clusterType: all
        inline:
          handler: EnsureAgentUpgrade
      
      # 业务集群组件
      - name: etcd
        version: "3.5.12"
        clusterType: workload
        inline:
          handler: EnsureEtcdUpgrade
      
      - name: kubernetes-master
        version: "1.29.0"
        clusterType: workload
        inline:
          handler: EnsureMasterUpgrade
      
      - name: kubernetes-worker
        version: "1.29.0"
        clusterType: workload
        inline:
          handler: EnsureWorkerUpgrade
      
      - name: containerd
        version: "1.7.13"
        clusterType: workload
        inline:
          handler: EnsureContainerdUpgrade
```

### 5.2 向后兼容示例

未指定 `clusterType` 时，默认为 `all`：

```yaml
apiVersion: v1alpha1
kind: ReleaseImage
metadata:
  name: bke-v1.0.0
spec:
  version: "1.0.0"
  upgrade:
    components:
      # 未指定 clusterType，默认为 all
      - name: pre-upgrade-resources
        version: "1.0.0"
        inline:
          handler: EnsurePreUpgradeResources
      
      - name: etcd
        version: "3.5.9"
        inline:
          handler: EnsureEtcdUpgrade
```

---

## 6. 升级流程对比

### 6.1 管理集群升级流程

```
检测集群类型: management
    │
    ├── 过滤组件:
    │   ├── provider
    │   ├── install-service
    │   ├── capi-controller-manager
    │   ├── pre-upgrade-resources
    │   └── bkeagent
    │
    └── 执行 DAG:
        Batch 1: [pre-upgrade-resources]
        Batch 2: [provider]
        Batch 3: [install-service, capi-controller-manager]
        Batch 4: [bkeagent]
```

### 6.2 业务集群升级流程

```
检测集群类型: workload
    │
    ├── 过滤组件:
    │   ├── pre-upgrade-resources
    │   ├── bkeagent
    │   ├── etcd
    │   ├── kubernetes-master
    │   ├── kubernetes-worker
    │   └── containerd
    │
    └── 执行 DAG:
        Batch 1: [pre-upgrade-resources]
        Batch 2: [bkeagent]
        Batch 3: [etcd]
        Batch 4: [kubernetes-master]
        Batch 5: [kubernetes-worker]
        Batch 6: [containerd]
```

### 6.3 对比表

| 集群类型 | 检测方式 | 升级组件 | 执行顺序 |
|---------|---------|---------|---------|
| **管理集群** | bke-controller-manager 存在 | provider → install-service → capi-controller-manager → pre-upgrade-resources → bkeagent | 管理面优先 |
| **业务集群** | bke-controller-manager 不存在 | pre-upgrade-resources → bkeagent → etcd → kubernetes-master → kubernetes-worker → containerd | 业务面优先 |

---

## 7. 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 集群类型检测时机 | 升级流程开始时 | 避免重复检测，统一存储 |
| 组件过滤方式 | 构建 DAG 前过滤 | 简化 DAG 构建逻辑 |
| 集群类型存储位置 | PhaseContext | 便于各 Phase 访问 |
| 向后兼容性 | ClusterType 可选 | 未指定时默认为 all |
| 管理集群组件定义 | 在同一个 ReleaseImage 中 | 统一版本管理 |

---

## 8. 实施步骤

### 8.1 阶段一：API 扩展（1 天）

1. 在 `api/v1alpha1/releaseimage_types.go` 中添加 `ClusterType` 类型和常量
2. 在 `ReleaseImageUpgradeComponent` 中添加 `ClusterType` 字段
3. 在 `pkg/upgrade/catalog.go` 中扩展 `UpgradeComponentSpec`
4. 添加管理集群组件常量

### 8.2 阶段二：组件目录扩展（1 天）

1. 在 `DeclarativeUpgradeCatalog` 中添加管理集群组件
2. 为所有组件添加 `ClusterType` 字段
3. 添加 `install-service` 组件的内联处理器注册

### 8.3 阶段三：PhaseContext 扩展（0.5 天）

1. 在 `PhaseContext` 中添加 `clusterType` 字段
2. 添加 `SetClusterType`、`GetClusterType`、`IsManagementCluster`、`IsWorkloadCluster` 方法

### 8.4 阶段四：升级流程改造（2 天）

1. 实现 `detectClusterType` 函数
2. 实现 `filterComponentsByClusterType` 函数
3. 修改 `executeUpgradeDAG` 流程
4. 添加日志记录

### 8.5 阶段五：测试验证（2 天）

1. 单元测试：集群类型检测
2. 单元测试：组件过滤
3. 集成测试：管理集群升级
4. 集成测试：业务集群升级
5. 端到端测试：完整升级流程

### 8.6 阶段六：文档更新（0.5 天）

1. 更新 ReleaseImage API 文档
2. 更新升级流程文档
3. 添加使用示例

**总计：7 天**

---

## 9. 优势

### 9.1 统一管理

- 一个 ReleaseImage 定义所有组件的版本
- 避免管理集群和业务集群版本不一致
- 简化版本管理流程

### 9.2 自动化

- 自动检测集群类型
- 无需手动配置升级组件
- 减少人为错误

### 9.3 灵活性

- 支持 `all` 类型，适用于所有集群的组件
- 支持按集群类型差异化升级
- 便于扩展新的集群类型

### 9.4 向后兼容

- 未指定 `ClusterType` 时默认为 `all`
- 保持现有业务集群升级流程不变
- 平滑迁移到新版本

### 9.5 清晰分离

- 管理集群和业务集群的升级逻辑清晰分离
- 便于问题定位和调试
- 降低维护成本

---

## 10. 风险与缓解

### 10.1 风险识别

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| 集群类型检测失败 | 升级中断 | 低 | 添加重试机制，检测失败时降级为业务集群 |
| 组件过滤逻辑错误 | 升级错误组件 | 中 | 添加详细日志，单元测试覆盖 |
| 向后兼容性问题 | 现有流程中断 | 低 | ClusterType 可选，默认值为 all |
| 管理集群组件升级失败 | 管理面不可用 | 中 | 添加回滚机制，支持手动恢复 |

### 10.2 测试策略

1. **单元测试**：覆盖集群类型检测、组件过滤逻辑
2. **集成测试**：验证管理集群和业务集群的升级流程
3. **端到端测试**：完整升级流程验证
4. **回归测试**：确保现有业务集群升级流程不受影响

---

## 11. 总结

本方案通过在 ReleaseImage 中同时定义管理集群和业务集群的组件，实现了统一的版本管理和差异化的升级流程。核心特点包括：

1. **统一入口**：一个 ReleaseImage 定义所有组件
2. **自动检测**：升级流程开始时自动检测集群类型
3. **智能过滤**：根据集群类型过滤出需要的组件
4. **独立执行**：管理集群和业务集群独立升级

该方案具有良好的扩展性和向后兼容性，能够满足未来多集群类型升级的需求。

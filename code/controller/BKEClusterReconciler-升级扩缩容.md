# 详细说明升级/扩缩容的功能清单及代码入口

## 升级/扩缩容功能清单及代码入口

### 一、功能清单总览

```
┌─────────────────────────────────────────────────────────────────┐
│  PostDeployPhases（部署后阶段）                                   │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  升级功能（7个 Phase）：                                           │
│    1. EnsureProviderSelfUpgrade  - Provider 自升级               │
│    2. EnsureAgentUpgrade         - BKEAgent 升级                │
│    3. EnsureContainerdUpgrade    - Containerd 升级              │
│    4. EnsureEtcdUpgrade          - Etcd 滚动升级                │
│    5. EnsureMasterUpgrade        - Master 节点升级              │
│    6. EnsureWorkerUpgrade        - Worker 节点升级              │
│    7. EnsureComponentUpgrade     - openFuyao 核心组件升级        │
│                                                                │
│  扩缩容功能（4个 Phase）：                                        │
│    8. EnsureMasterJoin           - Master 扩容                  │
│    9. EnsureWorkerJoin           - Worker 扩容                  │
│   10. EnsureMasterDelete         - Master 缩容                  │
│   11. EnsureWorkerDelete         - Worker 缩容                  │
│                                                                │
│  健康检查（1个 Phase）：                                          │
│   12. EnsureCluster              - 集群健康检查                   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 二、代码入口

#### 1. Phase 注册入口

**文件**：[pkg/phaseframe/phases/list.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/list.go)
```go
// PostDeployPhases post deploy phases
PostDeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureProviderSelfUpgrade,   // Provider 自升级
    NewEnsureAgentUpgrade,          // Agent 升级
    NewEnsureContainerdUpgrade,     // Containerd 升级
    NewEnsureEtcdUpgrade,           // Etcd 升级
    NewEnsureWorkerUpgrade,         // Worker 升级
    NewEnsureMasterUpgrade,         // Master 升级
    NewEnsureWorkerDelete,          // Worker 删除（缩容）
    NewEnsureMasterDelete,          // Master 删除（缩容）
    NewEnsureComponentUpgrade,      // 组件升级
    NewEnsureCluster,               // 集群健康检查
}
```

#### 2. Phase 执行入口

**文件**：[pkg/phaseframe/phases/phase_flow.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go)
```go
// CalculatePhase 计算需要执行的 Phase
func (p *PhaseFlow) CalculatePhase(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) error {
    phasesFuncs := p.determinePhasesFuncs()
    p.calculateAndAddPhases(old, new, phasesFuncs)
    return p.ReportPhaseStatus()
}

// determinePhasesFuncs 根据集群状态决定执行哪些 Phase
func (p *PhaseFlow) determinePhasesFuncs() []func(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    if phaseutil.IsDeleteOrReset(p.ctx.BKECluster) {
        return DeletePhases  // 删除阶段
    }
    return FullPhasesRegisFunc  // 全量 Phase（包含升级/扩缩容）
}

// calculateAndAddPhases 计算并添加需要执行的 Phase
func (p *PhaseFlow) calculateAndAddPhases(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster, 
    phasesFuncs []func(ctx *phaseframe.PhaseContext) phaseframe.Phase) {    
    for _, f := range phasesFuncs {
        phase := f(p.ctx)
        if phase.NeedExecute(old, new) {  // 关键：通过 NeedExecute 判断是否需要执行
            p.BKEPhases = append(p.BKEPhases, phase)
        }
    }
}
```

### 三、升级功能详细说明

#### 1. EnsureProviderSelfUpgrade - Provider 自升级

**文件**：[ensure_provider_self_upgrade.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_provider_self_upgrade.go)

**功能**：升级 BKE Provider 自身（管理集群中的 Deployment）

**触发条件**：
```go
func (e *EnsureProviderSelfUpgrade) NeedExecute(old, new) bool {
    // Provider 版本变化
    return old.Spec.ProviderVersion != new.Spec.ProviderVersion
}
```
**执行逻辑**：
- Patch Provider Deployment 的镜像版本
- 等待 Deployment rollout 完成

#### 2. EnsureAgentUpgrade - Agent 升级

**文件**：[ensure_agent_upgrade.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_agent_upgrade.go)

**功能**：升级业务集群中的 BKEAgent DaemonSet

**触发条件**：
```go
func (e *EnsureAgentUpgrade) NeedExecute(old, new) bool {
    // Agent 版本变化
    return old.Spec.AgentVersion != new.Spec.AgentVersion
}
```
**执行逻辑**：
- Patch BKEAgent DaemonSet 的镜像版本
- 等待所有 Pod 滚动升级完成

#### 3. EnsureContainerdUpgrade - Containerd 升级

**文件**：[ensure_containerd_upgrade.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go)

**功能**：升级所有节点的 Containerd 运行时

**触发条件**：
```go
func (e *EnsureContainerdUpgrade) NeedExecute(old, new) bool {
    // Containerd 版本变化
    return old.Spec.ContainerdVersion != new.Spec.ContainerdVersion
}
```
**执行逻辑**：
- 创建 `k8s-containerd-reset` Command（重置 Containerd）
- 创建 `k8s-containerd-redeploy` Command（重新部署 Containerd）
- 等待所有节点执行完成

#### 4. EnsureEtcdUpgrade - Etcd 升级

**文件**：[ensure_etcd_upgrade.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_etcd_upgrade.go)

**功能**：滚动升级 Etcd 集群

**触发条件**：
```go
func (e *EnsureEtcdUpgrade) NeedExecute(old, new) bool {
    // Etcd 版本变化
    return old.Spec.EtcdVersion != new.Spec.EtcdVersion
}
```
**执行逻辑**：
- 逐个升级 Etcd 成员（滚动升级）
- 每个成员升级前进行健康检查
- 升级后验证 Etcd 集群健康

#### 5. EnsureMasterUpgrade - Master 升级

**文件**：[ensure_master_upgrade.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go)

**功能**：滚动升级 Master 节点

**触发条件**：
```go
func (e *EnsureMasterUpgrade) NeedExecute(old, new) bool {
    // Kubernetes 版本变化
    if new.Spec.KubernetesVersion != new.Status.KubernetesVersion {
        // 获取需要升级的 Master 节点
        nodes := phaseutil.GetNeedUpgradeMasterNodesWithBKENodes(new, bkeNodes)
        return nodes.Length() > 0
    }
    return false
}
```
**执行逻辑**：
```go
func (e *EnsureMasterUpgrade) reconcileMasterUpgrade() (ctrl.Result, error) {
    if bkeCluster.Spec.KubernetesVersion != bkeCluster.Status.KubernetesVersion {
        return e.rolloutUpgrade()  // 滚动升级
    }
    return ctrl.Result{}, nil
}

func (e *EnsureMasterUpgrade) rolloutUpgrade() (ctrl.Result, error) {
    // 1. 获取需要升级的 Master 节点
    nodes := phaseutil.GetNeedUpgradeMasterNodesWithBKENodes(...)
    
    // 2. 逐个升级（滚动升级）
    for _, node := range nodes {
        // a. Cordon 节点（标记不可调度）
        // b. Drain 节点（驱逐 Pod）
        // c. 创建升级 Command
        // d. 等待升级完成
        // e. Uncordon 节点（恢复调度）
    }
    
    // 3. 更新 Status.KubernetesVersion
}
```

#### 6. EnsureWorkerUpgrade - Worker 升级

**文件**：[ensure_worker_upgrade.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go)

**功能**：滚动升级 Worker 节点

**触发条件**：
```go
func (e *EnsureWorkerUpgrade) NeedExecute(old, new) bool {
    // Kubernetes 版本变化
    if new.Spec.KubernetesVersion != new.Status.KubernetesVersion {
        // 获取需要升级的 Worker 节点
        nodes := phaseutil.GetNeedUpgradeWorkerNodesWithBKENodes(new, bkeNodes)
        return nodes.Length() > 0
    }
    return false
}
```
**执行逻辑**：
```go
func (e *EnsureWorkerUpgrade) Execute() (ctrl.Result, error) {
    // 1. 获取需要升级的 Worker 节点
    nodes := phaseutil.GetNeedUpgradeWorkerNodesWithBKENodes(...)
    
    // 2. 逐个升级（滚动升级）
    for _, node := range nodes {
        // a. Cordon 节点
        // b. Drain 节点
        // c. 创建升级 Command
        upgradeCmd := createUpgradeCommand(CreateUpgradeCommandParams{
            Node:  node,
            Phase: "UpgradeWorker",
        })
        // d. 等待升级完成
        // e. Uncordon 节点
    }
    
    // 3. 更新 Status.KubernetesVersion
}
```

#### 7. EnsureComponentUpgrade - 组件升级

**文件**：[ensure_component_upgrade.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_component_upgrade.go)

**功能**：升级 openFuyao 核心组件（如 CNI、CSI 等）

**触发条件**：
```go
func (e *EnsureComponentUpgrade) NeedExecute(old, new) bool {
    // 组件版本变化
    return old.Spec.ComponentVersions != new.Spec.ComponentVersions
}
```
**执行逻辑**：
- Patch 组件 ConfigMap/Deployment 的镜像版本
- 等待组件 rollout 完成

### 四、扩缩容功能详细说明

#### 1. EnsureMasterJoin - Master 扩容

**文件**：[ensure_master_join.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go)

**功能**：添加新的 Master 节点

**触发条件**：
```go
func (e *EnsureMasterJoin) NeedExecute(old, new) bool {
    // Master 节点数量增加
    oldMasterCount := len(old.Spec.Nodes.Master)
    newMasterCount := len(new.Spec.Nodes.Master)
    return newMasterCount > oldMasterCount
}
```
**执行逻辑**：
- 获取新增的 Master 节点
- 为每个节点创建 Bootstrap Command
- 等待节点加入集群

#### 2. EnsureWorkerJoin - Worker 扩容

**文件**：[ensure_worker_join.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go)

**功能**：添加新的 Worker 节点

**触发条件**：
```go
func (e *EnsureWorkerJoin) NeedExecute(old, new) bool {
    // Worker 节点数量增加
    oldWorkerCount := len(old.Spec.Nodes.Worker)
    newWorkerCount := len(new.Spec.Nodes.Worker)
    return newWorkerCount > oldWorkerCount
}
```
**执行逻辑**：
- 获取新增的 Worker 节点
- 为每个节点创建 Bootstrap Command
- 等待节点加入集群

#### 3. EnsureMasterDelete - Master 缩容

**文件**：[ensure_master_delete.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_delete.go)

**功能**：删除 Master 节点

**触发条件**：
```go
func (e *EnsureMasterDelete) NeedExecute(old, new) bool {
    // Master 节点数量减少
    oldMasterCount := len(old.Spec.Nodes.Master)
    newMasterCount := len(new.Spec.Nodes.Master)
    return newMasterCount < oldMasterCount
}
```
**执行逻辑**：
- 获取需要删除的 Master 节点
- Drain 节点（驱逐 Pod）
- 删除节点
- 清理相关资源

#### 4. EnsureWorkerDelete - Worker 缩容

**文件**：[ensure_worker_delete.go](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_delete.go)

**功能**：删除 Worker 节点

**触发条件**：
```go
func (e *EnsureWorkerDelete) NeedExecute(old, new) bool {
    // Worker 节点数量减少
    nodes := phaseutil.GetNeedDeleteWorkerNodes(e.Ctx, e.Ctx.Client, new)
    return nodes.Length() > 0
}
```
**执行逻辑**：
```go
func (e *EnsureWorkerDelete) Execute() (ctrl.Result, error) {
    // 1. 获取需要删除的 Worker 节点
    nodes := phaseutil.GetNeedDeleteWorkerNodes(...)
    
    // 2. 逐个删除
    for _, node := range nodes {
        // a. Cordon 节点
        // b. Drain 节点
        // c. 删除 Machine
        // d. 删除 BKEMachine
        // e. 清理节点资源
    }
    
    // 3. 等待删除完成
    return e.waitWorkerDelete()
}
```

### 五、执行流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│  BKECluster Controller Reconcile                                    │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  PhaseFlow.CalculatePhase(old, new)                                 │
│     └── 计算需要执行的 Phase                                          │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  determinePhasesFuncs()                                             │
│     ├── 删除/重置？→ DeletePhases                                     │
│     └── 否则 → FullPhasesRegisFunc                                   │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  calculateAndAddPhases(old, new, phasesFuncs)                       │
│     └── 遍历所有 Phase，调用 NeedExecute() 判断                        │
└─────────────────────────────────────────────────────────────────────┘
                                │
                ┌───────────────┴───────────────┐
                │                               │
                ▼                               ▼
┌──────────────────────────┐      ┌──────────────────────────┐
│  升级场景                 │      │  扩缩容场景                │
│                          │      │                          │
│  NeedExecute() 检测：     │      │  NeedExecute() 检测：     │
│  - K8s 版本变化           │      │  - 节点数量变化            │
│  - Etcd 版本变化          │      │                          │
│  - Containerd 版本变化    │      │                          │
│  - Agent 版本变化         │      │                          │
│  - 组件版本变化            │      │                          │
└──────────────────────────┘      └──────────────────────────┘
                │                               │
                ▼                               ▼
┌──────────────────────────┐      ┌──────────────────────────┐
│  升级 Phase 执行顺序：     │      │  扩缩容 Phase 执行：        │
│                          │      │                          │
│  1. ProviderSelfUpgrade  │      │  Master 扩容：            │
│  2. AgentUpgrade         │      │    EnsureMasterJoin      │
│  3. ContainerdUpgrade    │      │                          │
│  4. EtcdUpgrade          │      │  Worker 扩容：            │
│  5. MasterUpgrade        │      │    EnsureWorkerJoin      │
│  6. WorkerUpgrade        │      │                          │
│  7. ComponentUpgrade     │      │  Master 缩容：            │
│                          │      │    EnsureMasterDelete    │
│                          │      │                          │
│                          │      │  Worker 缩容：            │
│                          │      │    EnsureWorkerDelete    │
└──────────────────────────┘      └──────────────────────────┘
                │                               │
                └───────────────┬───────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  PhaseFlow.Execute()                                                │
│     └── 按顺序执行所有待执行的 Phase                                    │
└─────────────────────────────────────────────────────────────────────┘
```

### 六、关键判断函数

#### 1. NeedExecute - 判断是否需要执行

每个 Phase 都实现了 `NeedExecute(old, new)` 方法：
```go
// 基础实现
func (b *BasePhase) DefaultNeedExecute(old, new) bool {
    // 1. 检查是否已成功执行
    if b.GetStatus() == bkev1beta1.PhaseSucceeded {
        return false
    }
    
    // 2. 检查是否正在执行
    if b.GetStatus() == bkev1beta1.PhaseRunning {
        return true
    }
    
    return true
}

// 具体实现示例（Master 升级）
func (e *EnsureMasterUpgrade) NeedExecute(old, new) bool {
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    
    // 检查 K8s 版本是否变化
    if new.Spec.KubernetesVersion == new.Status.KubernetesVersion {
        return false
    }
    
    // 检查是否有需要升级的节点
    nodes := phaseutil.GetNeedUpgradeMasterNodesWithBKENodes(new, bkeNodes)
    return nodes.Length() > 0
}
```

#### 2. GetNeedUpgradeMasterNodes - 获取需要升级的 Master 节点

```go
func GetNeedUpgradeMasterNodesWithBKENodes(bkeCluster *bkev1beta1.BKECluster, bkeNodes []*bkev1beta1.BKENode) bkenode.Nodes {
    var nodes bkenode.Nodes
    
    for _, bkeNode := range bkeNodes {
        // 检查节点是否为 Master
        if !bkeNode.Spec.IsMaster {
            continue
        }
        
        // 检查节点版本是否需要升级
        if bkeNode.Status.KubernetesVersion != bkeCluster.Spec.KubernetesVersion {
            nodes = append(nodes, bkeNode.ToNode())
        }
    }
    
    return nodes
}
```

#### 3. GetNeedDeleteWorkerNodes - 获取需要删除的 Worker 节点

```go
func GetNeedDeleteWorkerNodes(ctx, client, bkeCluster) bkenode.Nodes {
    // 1. 检查是否有预约删除的节点
    if v, ok := annotation.HasAnnotation(bkeCluster, annotation.AppointmentDeletedNodesAnnotationKey); ok {
        // 解析预约删除的节点列表
        nodes := parseAppointmentNodes(v)
        return nodes
    }
    
    // 2. 比较 Spec 和 Status 中的节点数量
    specWorkerCount := len(bkeCluster.Spec.Nodes.Worker)
    statusWorkerCount := len(bkeCluster.Status.Nodes.Worker)
    
    if statusWorkerCount > specWorkerCount {
        // 需要删除多余的节点
        return getNodesToDelete(...)
    }
    
    return nil
}
```

### 七、Webhook 验证

**文件**：[webhooks/capbke/bkecluster.go](file:////cluster-api-provider-bke/webhooks/capbke/bkecluster.go)

#### 1. Kubernetes 版本升级验证

```go
func (webhook *BKECluster) validateKubernetesVersionUpgrade(newBKECluster, oldBKECluster) error {
    // 1. 比较版本是否为升级
    newVersion := semver.MustParse(newBKECluster.Spec.KubernetesVersion)
    oldVersion := semver.MustParse(oldBKECluster.Status.KubernetesVersion)
    
    if !newVersion.GT(oldVersion) {
        return fmt.Errorf("kubernetes version downgrade is not allowed")
    }
    
    // 2. 检查是否为 'other' 类型集群
    if newBKECluster.Spec.ClusterType == "other" {
        return fmt.Errorf("'other' type cluster does not support kubernetes version upgrade")
    }
    
    return nil
}
```

#### 2. 集群可升级性验证

```go
func (webhook *BKECluster) validateCommonUpgradeability(ctx, newBKECluster, oldBKECluster) error {
    // 1. 集群必须健康才能升级
    if !conditions.IsTrue(newBKECluster, bkev1beta1.ReadyCondition) {
        return fmt.Errorf("BKECluster is not in a ready state and cannot be upgraded")
    }
    
    // 2. 所有节点的 Agent 必须存活
    for _, node := range newBKECluster.Status.Nodes {
        if !isAgentAlive(node) {
            return fmt.Errorf("node %s BKEAgent is not alive, cannot be upgraded", node.IP)
        }
    }
    
    return nil
}
```

### 八、总结

**升级功能清单**：

| Phase | 功能 | 触发条件 | 文件 |
|-------|------|---------|------|
| EnsureProviderSelfUpgrade | Provider 自升级 | ProviderVersion 变化 | ensure_provider_self_upgrade.go |
| EnsureAgentUpgrade | Agent 升级 | AgentVersion 变化 | ensure_agent_upgrade.go |
| EnsureContainerdUpgrade | Containerd 升级 | ContainerdVersion 变化 | ensure_containerd_upgrade.go |
| EnsureEtcdUpgrade | Etcd 升级 | EtcdVersion 变化 | ensure_etcd_upgrade.go |
| EnsureMasterUpgrade | Master 升级 | KubernetesVersion 变化 | ensure_master_upgrade.go |
| EnsureWorkerUpgrade | Worker 升级 | KubernetesVersion 变化 | ensure_worker_upgrade.go |
| EnsureComponentUpgrade | 组件升级 | ComponentVersions 变化 | ensure_component_upgrade.go |

**扩缩容功能清单**：

| Phase | 功能 | 触发条件 | 文件 |
|-------|------|---------|------|
| EnsureMasterJoin | Master 扩容 | Master 节点数量增加 | ensure_master_join.go |
| EnsureWorkerJoin | Worker 扩容 | Worker 节点数量增加 | ensure_worker_join.go |
| EnsureMasterDelete | Master 缩容 | Master 节点数量减少 | ensure_master_delete.go |
| EnsureWorkerDelete | Worker 缩容 | Worker 节点数量减少 | ensure_worker_delete.go |

**代码入口**：
- **Phase 注册**：`pkg/phaseframe/phases/list.go`
- **Phase 执行**：`pkg/phaseframe/phases/phase_flow.go`
- **具体实现**：`pkg/phaseframe/phases/ensure_*_upgrade.go` 和 `ensure_*_delete.go`
- **Webhook 验证**：`webhooks/capbke/bkecluster.go`

**关键机制**：
- ✅ 通过 `NeedExecute(old, new)` 动态判断是否需要执行
- ✅ 升级采用滚动升级策略（逐个节点升级）
- ✅ 扩缩容通过节点数量变化触发
- ✅ Webhook 验证升级的合法性和集群的可升级性
- ✅ 所有操作都通过 Command CRD 异步执行

# 详细说明 BKEClusterReconciler 的触发执行条件和场景区分机制

## BKEClusterReconciler 触发执行与场景区分

### 一、触发执行条件

#### 1. SetupWithManager 定义

**文件**：[bkecluster_controller.go:248-279](file:////cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L248-L279)
```go
func (r *BKEClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
    c, err := ctrl.NewControllerManagedBy(mgr).
        // 1. Watch BKECluster 自身
        For(&bkev1beta1.BKECluster{},
            builder.WithPredicates(predicate.Or(
                bkepredicates.BKEClusterAnnotationsChange(),  // Annotation 变化
                bkepredicates.BKEClusterSpecChange(),         // Spec 变化
            )),
        ).
        // 2. Watch Cluster API Cluster
        Watches(
            &clusterv1.Cluster{},
            handler.EnqueueRequestsFromMapFunc(clusterToBKEClusterMapFunc(...)),
            builder.WithPredicates(bkepredicates.ClusterUnPause()),
        ).
        // 3. Watch BKENode
        Watches(
            &confv1beta1.BKENode{},
            handler.EnqueueRequestsFromMapFunc(r.bkeNodeToBKEClusterMapFunc()),
            builder.WithPredicates(bkepredicates.BKENodeChange()),
        ).
        Build(r)
}
```

#### 2. 触发源详细说明

| 触发源 | Predicate | 触发条件 | 说明 |
|--------|-----------|---------|------|
| **BKECluster 创建** | `BKEClusterSpecChange` | 新建 BKECluster | 安装场景 |
| **BKECluster Spec 变化** | `BKEClusterSpecChange` | Generation 变化 | 升级/扩缩容场景 |
| **BKECluster Annotation 变化** | `BKEClusterAnnotationsChange` | 特定 annotation 变化 | 重试、节点预约等 |
| **Cluster API Cluster 变化** | `ClusterUnPause` | Cluster.Spec.Paused=false | CAPI 状态同步 |
| **BKENode 创建/更新/删除** | `BKENodeChange` | BKENode 状态变化 | 节点状态同步 |
| **业务集群 Node 状态变化** | `NodeNotReadyPredicate` | Node NotReady | 健康检查触发 |

### 二、Predicate 过滤器详解

#### 1. BKEClusterSpecChange - Spec 变化触发

```go
func BKEClusterSpecChange() predicate.Funcs {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj := e.ObjectNew.(*bkev1beta1.BKECluster)
            oldObj := e.ObjectOld.(*bkev1beta1.BKECluster)
            
            // 1. Generation 变化（Spec 变化）
            if newObj.Generation != oldObj.Generation {
                // 2. 删除中 → 触发
                if !newObj.DeletionTimestamp.IsZero() {
                    return true
                }
                
                // 3. 暂停状态变更 → 触发
                if oldObj.Spec.Pause != newObj.Spec.Pause {
                    return true
                }
                
                // 4. 内部修改 → 跳过
                if condition.HasCondition(InternalSpecChangeCondition, newObj) {
                    return false
                }
                
                // 5. Deploying 状态 → 跳过
                if newObj.Status.ClusterHealthState == Deploying {
                    return false
                }
                
                return true
            }
            return false
        },
        CreateFunc: func(e event.CreateEvent) bool {
            return true  // 创建时始终触发
        },
    }
}
```
**触发场景**：
- ✅ 版本升级（KubernetesVersion、EtcdVersion 等变化）
- ✅ 节点扩缩容（Nodes.Master/Worker 数量变化）
- ✅ 配置变更（ClusterConfig 变化）
- ✅ 暂停状态变更
- ❌ 内部修改（Controller 自动修改的 Spec）
- ❌ Deploying 状态下的更新

#### 2. BKEClusterAnnotationsChange - Annotation 变化触发

```go
func BKEClusterAnnotationsChange() predicate.Funcs {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj := e.ObjectNew.(*bkev1beta1.BKECluster)
            oldObj := e.ObjectOld.(*bkev1beta1.BKECluster)
            
            // 只监听特定的 annotation 变化
            allowChangeAnnotations := []string{
                annotation.AppointmentDeletedNodesAnnotationKey,    // 预约删除节点
                annotation.AppointmentAddNodesAnnotationKey,        // 预约添加节点
                annotation.RetryAnnotationKey,                      // 重试
                annotation.ClusterTrackerHealthyCheckFailedAnnotationKey,  // 健康检查失败
            }
            
            for _, key := range allowChangeAnnotations {
                newV, newFound := annotation.HasAnnotation(newObj, key)
                oldV, oldFound := annotation.HasAnnotation(oldObj, key)
                if (newV != oldV) || (newFound && !oldFound) {
                    return true
                }
            }
            return false
        },
    }
}
```
**触发场景**：
- ✅ 预约删除节点（缩容）
- ✅ 预约添加节点（扩容）
- ✅ 重试操作
- ✅ 健康检查失败标记

#### 3. BKENodeChange - BKENode 状态变化触发

```go
func BKENodeChange() predicate.Funcs {
    return predicate.Funcs{
        CreateFunc: func(e event.CreateEvent) bool {
            return true  // BKENode 创建时触发
        },
        UpdateFunc: func(e event.UpdateEvent) bool {
            newObj := e.ObjectNew.(*confv1beta1.BKENode)
            oldObj := e.ObjectOld.(*confv1beta1.BKENode)
            
            // 状态变化时触发
            return newObj.Status != oldObj.Status
        },
        DeleteFunc: func(e event.DeleteEvent) bool {
            return true  // BKENode 删除时触发
        },
    }
}
```
**触发场景**：
- ✅ 新节点加入（BKENode 创建）
- ✅ 节点状态变化（Ready、Failed 等）
- ✅ 节点删除（BKENode 删除）

### 三、场景区分机制

#### 1. Phase 执行流程

```
┌─────────────────────────────────────────────────────────────────────┐
│  BKECluster Controller Reconcile                                    │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  1. executePhaseFlow()                                              │
│     └── PhaseFlow.CalculatePhase(old, new)                          │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  2. determinePhasesFuncs()                                          │
│     ├── 删除/重置？→ DeletePhases                                     │
│     └── 否则 → FullPhasesRegisFunc                                   │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  3. calculateAndAddPhases(old, new, phasesFuncs)                    │
│     └── 遍历所有 Phase，调用 NeedExecute() 判断                        │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  4. PhaseFlow.Execute()                                             │
│     ├── determinePhases() → 获取待执行的 Phase 列表                    │
│     └── executePhases() → 执行 Phase 并更新 ClusterStatus             │
└─────────────────────────────────────────────────────────────────────┘
```

#### 2. NeedExecute - 场景判断核心

每个 Phase 通过 `NeedExecute(old, new)` 判断是否需要执行：

**安装场景判断**：
```go
// EnsureMasterInit - Master 初始化
func (e *EnsureMasterInit) NeedExecute(old, new) bool {
    // 控制平面未初始化
    return !conditions.IsTrue(new, ControlPlaneInitializedCondition)
}

// EnsureWorkerJoin - Worker 加入
func (e *EnsureWorkerJoin) NeedExecute(old, new) bool {
    // 有新的 Worker 节点需要加入
    nodes := GetNeedJoinWorkerNodes(new)
    return nodes.Length() > 0
}
```

**升级场景判断**：
```go
// EnsureMasterUpgrade - Master 升级
func (e *EnsureMasterUpgrade) NeedExecute(old, new) bool {
    // Kubernetes 版本变化
    if new.Spec.KubernetesVersion != new.Status.KubernetesVersion {
        // 有需要升级的 Master 节点
        nodes := GetNeedUpgradeMasterNodes(new)
        return nodes.Length() > 0
    }
    return false
}

// EnsureEtcdUpgrade - Etcd 升级
func (e *EnsureEtcdUpgrade) NeedExecute(old, new) bool {
    // Etcd 版本变化
    return new.Spec.EtcdVersion != new.Status.EtcdVersion
}
```

**扩缩容场景判断**：
```go
// EnsureMasterJoin - Master 扩容
func (e *EnsureMasterJoin) NeedExecute(old, new) bool {
    // Master 节点数量增加
    oldCount := len(old.Spec.Nodes.Master)
    newCount := len(new.Spec.Nodes.Master)
    return newCount > oldCount
}

// EnsureWorkerDelete - Worker 缩容
func (e *EnsureWorkerDelete) NeedExecute(old, new) bool {
    // 有需要删除的 Worker 节点
    nodes := GetNeedDeleteWorkerNodes(new)
    return nodes.Length() > 0
}
```

#### 3. ClusterStatus 状态转换

通过 `calculateClusterStatusByPhase()` 设置不同的 ClusterStatus：
```go
func calculateClusterStatusByPhase(phase phaseframe.Phase, err error) error {
    phaseName := phase.Name()
    
    switch {
    // 安装场景
    case phaseName.In(ClusterInitPhaseNames):
        handleClusterInitPhase(ctx, err)
        // → ClusterInitializing / ClusterInitializationFailed
    
    // 升级场景
    case phaseName.In(ClusterUpgradePhaseNames):
        handleClusterUpgradePhase(ctx, err)
        // → ClusterUpgrading / ClusterUpgradeFailed
    
    // Master 扩容场景
    case phaseName.In(ClusterScaleMasterUpPhaseNames):
        handleClusterScaleMasterUpPhase(ctx, err)
        // → ClusterMasterScalingUp / ClusterScaleFailed
    
    // Worker 扩容场景
    case phaseName.In(ClusterScaleWorkerUpPhaseNames):
        handleClusterScaleWorkerUpPhase(ctx, err)
        // → ClusterWorkerScalingUp / ClusterScaleFailed
    
    // Master 缩容场景
    case phaseName.In(ClusterScaleMasterDownPhaseNames):
        handleClusterScaleMasterDownPhase(ctx, err)
        // → ClusterMasterScalingDown / ClusterScaleFailed
    
    // Worker 缩容场景
    case phaseName.In(ClusterScaleWorkerDownPhaseNames):
        handleClusterScaleWorkerDownPhase(ctx, err)
        // → ClusterWorkerScalingDown / ClusterScaleFailed
    
    // 删除场景
    case phaseName.In(ClusterDeletePhaseNames):
        handleClusterDeletePhase(ctx, err)
        // → ClusterDeleting / ClusterDeleteFailed
    
    // 暂停场景
    case phaseName.In(ClusterPausedPhaseNames):
        handleClusterPausedPhase(ctx, err)
        // → ClusterPaused / ClusterPauseFailed
    }
}
```

### 四、场景区分流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│  BKECluster Reconcile 触发                                           │
└─────────────────────────────────────────────────────────────────────┘
                                │
                ┌───────────────┴───────────────┐
                │                               │
                ▼                               ▼
┌──────────────────────────┐      ┌──────────────────────────┐
│  BKECluster 创建          │      │  BKECluster Spec 变化    │
│  (Generation 初始化)      │      │  (Generation 增加)        │
└──────────────────────────┘      └──────────────────────────┘
                │                               │
                └───────────────┬───────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  PhaseFlow.CalculatePhase(old, new)                                 │
│     └── 遍历所有 Phase，调用 NeedExecute()                            │
└─────────────────────────────────────────────────────────────────────┘
                                │
                ┌───────────────┼───────────────┬───────────────┐
                │               │               │               │
                ▼               ▼               ▼               ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ 安装场景      │ │ 升级场景       │ │ 扩容场景      │ │ 缩容场景      │
│              │ │              │ │              │ │              │
│ NeedExecute: │ │ NeedExecute: │ │ NeedExecute: │ │ NeedExecute: │
│ • 无旧集群    │ │ • 版本变化    │ │ • 节点增加     │ │ • 节点减少    │
│ • Status 空  │ │ • Status≠Spec│ │              │ │              │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
        │               │               │               │
        ▼               ▼               ▼               ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ ClusterInit  │ │ ClusterUpgrad│ │ ClusterScale │ │ ClusterScale │
│ PhaseNames   │ │ PhaseNames   │ │ UpPhaseNames │ │ DownPhaseNames│
│              │ │              │ │              │ │              │
│ • EnsureCerts│ │ • EnsureEtcd │ │ • EnsureMaste│ │ • EnsureMaster│
│ • EnsureAPI  │ │   Upgrade    │ │   rJoin      │ │   Delete      │
│ • EnsureMast │ │ • EnsureMaste│ │ • EnsureWorke│ │ • EnsureWorker│
│   erInit     │ │   rUpgrade   │ │   rJoin      │ │   Delete      │
│ • EnsureWork │ │ • EnsureWorke│ │              │ │              │
│   erJoin     │ │   rUpgrade   │ │              │ │              │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
        │               │               │               │
        ▼               ▼               ▼               ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ClusterStatus │ │ClusterStatus │ │ClusterStatus │ │ClusterStatus │
│              │ │              │ │              │ │              │
│ Initializing │ │ Upgrading    │ │ Master/Worker│ │ Master/Worker│
│              │ │              │ │ ScalingUp    │ │ ScalingDown  │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
```

### 五、场景判断示例

#### 1. 安装场景

```
触发条件：
  - BKECluster 首次创建
  - Status.ClusterStatus == ""
  - Status.KubernetesVersion == ""

NeedExecute 判断：
  - EnsureFinalizer: ✅ (始终执行)
  - EnsureBKEAgent: ✅ (Agent 未部署)
  - EnsureClusterAPIObj: ✅ (CAPI 对象不存在)
  - EnsureCerts: ✅ (证书不存在)
  - EnsureMasterInit: ✅ (ControlPlaneInitialized=False)
  - EnsureMasterUpgrade: ❌ (版本相同)
  - EnsureWorkerDelete: ❌ (无节点删除)

ClusterStatus: ClusterInitializing → ClusterRunning
```

#### 2. 升级场景

```
触发条件：
  - BKECluster.Spec.KubernetesVersion 变化
  - BKECluster.Spec.EtcdVersion 变化
  - Generation 增加

NeedExecute 判断：
  - EnsureFinalizer: ❌ (已执行)
  - EnsureBKEAgent: ❌ (已部署)
  - EnsureEtcdUpgrade: ✅ (EtcdVersion != Status.EtcdVersion)
  - EnsureMasterUpgrade: ✅ (KubernetesVersion != Status.KubernetesVersion)
  - EnsureWorkerUpgrade: ✅ (KubernetesVersion != Status.KubernetesVersion)
  - EnsureMasterJoin: ❌ (节点数量未变)

ClusterStatus: ClusterRunning → ClusterUpgrading → ClusterRunning
```

#### 3. 扩容场景

```
触发条件：
  - BKECluster.Spec.Nodes.Master 数量增加
  - 或 BKECluster.Spec.Nodes.Worker 数量增加
  - Generation 增加

NeedExecute 判断：
  - EnsureFinalizer: ❌ (已执行)
  - EnsureMasterJoin: ✅ (Master 数量增加)
  - EnsureWorkerJoin: ✅ (Worker 数量增加)
  - EnsureMasterUpgrade: ❌ (版本未变)
  - EnsureWorkerDelete: ❌ (无节点删除)

ClusterStatus: ClusterRunning → ClusterMasterScalingUp → ClusterRunning
```

#### 4. 缩容场景

```
触发条件：
  - BKECluster.Spec.Nodes.Worker 数量减少
  - 或设置 appointment-deleted-nodes annotation
  - Generation 增加

NeedExecute 判断：
  - EnsureFinalizer: ❌ (已执行)
  - EnsureWorkerDelete: ✅ (Worker 数量减少)
  - EnsureMasterDelete: ✅ (Master 数量减少)
  - EnsureMasterUpgrade: ❌ (版本未变)

ClusterStatus: ClusterRunning → ClusterWorkerScalingDown → ClusterRunning
```

### 六、总结

**触发执行条件**：

| 触发源 | Predicate | 场景 |
|--------|-----------|------|
| BKECluster 创建 | `BKEClusterSpecChange` | 安装 |
| BKECluster Spec 变化 | `BKEClusterSpecChange` | 升级/扩缩容 |
| BKECluster Annotation 变化 | `BKEClusterAnnotationsChange` | 重试/预约操作 |
| Cluster API Cluster 变化 | `ClusterUnPause` | CAPI 状态同步 |
| BKENode 状态变化 | `BKENodeChange` | 节点状态同步 |
| 业务集群 Node 状态变化 | `NodeNotReadyPredicate` | 健康检查 |

**场景区分机制**：

1. **Phase NeedExecute()**：判断是否需要执行该 Phase
2. **ClusterStatus 状态转换**：通过 Phase 名称分组设置不同状态
3. **Phase 名称分组**：
   - `ClusterInitPhaseNames`：安装场景
   - `ClusterUpgradePhaseNames`：升级场景
   - `ClusterScaleMasterUpPhaseNames`：Master 扩容
   - `ClusterScaleWorkerUpPhaseNames`：Worker 扩容
   - `ClusterScaleMasterDownPhaseNames`：Master 缩容
   - `ClusterScaleWorkerDownPhaseNames`：Worker 缩容

**关键设计**：
- ✅ 通过 Predicate 过滤不必要的触发
- ✅ 通过 NeedExecute 动态判断场景
- ✅ 通过 ClusterStatus 状态机管理生命周期
- ✅ 通过 Phase 名称分组实现场景区分
- ✅ 支持多种触发源（Spec、Annotation、外部资源）

# 重构方案：根据场景组装不同的 Phase，而不是统一使用 NeedExecute

## 重构方案：根据场景组装 Phase

### 一、当前实现的问题

```go
// 当前实现：所有 Phase 都注册，通过 NeedExecute 过滤
func (p *PhaseFlow) calculateAndAddPhases(old, new, phasesFuncs) {
    for _, f := range phasesFuncs {
        phase := f(p.ctx)
        if phase.NeedExecute(old, new) {  // ❌ 每个 Phase 都要判断
            p.BKEPhases = append(p.BKEPhases, phase)
        }
    }
}

// 问题：
// 1. 所有 Phase 都会被创建（性能浪费）
// 2. 场景判断逻辑分散在各个 Phase 中
// 3. NeedExecute 逻辑复杂，难以维护
// 4. 无法直观看出不同场景的执行路径
```

### 二、重构方案

#### 方案 1：场景判断函数 + Phase 组装

**文件**：`pkg/phaseframe/phases/phase_flow.go`
```go
// Scene 场景类型
type Scene string

const (
    SceneInit           Scene = "Init"           // 安装
    SceneUpgrade        Scene = "Upgrade"        // 升级
    SceneScaleUp        Scene = "ScaleUp"        // 扩容
    SceneScaleDown      Scene = "ScaleDown"      // 缩容
    SceneDelete         Scene = "Delete"         // 删除
    ScenePause          Scene = "Pause"          // 暂停
    SceneDryRun         Scene = "DryRun"         // DryRun
    SceneManage         Scene = "Manage"         // 纳管
    SceneHealthCheck    Scene = "HealthCheck"    // 健康检查
)

// determineScene 根据集群状态判断场景
func (p *PhaseFlow) determineScene(old, new *bkev1beta1.BKECluster) Scene {
    // 1. 删除场景
    if phaseutil.IsDeleteOrReset(new) {
        return SceneDelete
    }
    
    // 2. 暂停场景
    if old.Spec.Pause != new.Spec.Pause && new.Spec.Pause {
        return ScenePause
    }
    
    // 3. DryRun 场景
    if v, ok := annotation.HasAnnotation(new, annotation.BKEClusterDryRunAnnotationKey); ok && v == "true" {
        return SceneDryRun
    }
    
    // 4. 纳管场景
    if new.Spec.ClusterType == "managed" && old.Status.ClusterStatus == "" {
        return SceneManage
    }
    
    // 5. 安装场景
    if new.Status.ClusterStatus == "" || new.Status.ClusterStatus == bkev1beta1.ClusterUnknown {
        return SceneInit
    }
    
    // 6. 升级场景
    if p.isUpgradeScene(old, new) {
        return SceneUpgrade
    }
    
    // 7. 扩容场景
    if p.isScaleUpScene(old, new) {
        return SceneScaleUp
    }
    
    // 8. 缩容场景
    if p.isScaleDownScene(old, new) {
        return SceneScaleDown
    }
    
    // 9. 默认：健康检查
    return SceneHealthCheck
}

// isUpgradeScene 判断是否为升级场景
func (p *PhaseFlow) isUpgradeScene(old, new *bkev1beta1.BKECluster) bool {
    // 版本变化
    if new.Spec.KubernetesVersion != old.Status.KubernetesVersion ||
       new.Spec.EtcdVersion != old.Status.EtcdVersion ||
       new.Spec.ContainerdVersion != old.Status.ContainerdVersion ||
       new.Spec.AgentVersion != old.Status.AgentVersion ||
       new.Spec.ProviderVersion != old.Status.ProviderVersion {
        return true
    }
    
    // 组件版本变化
    if !reflect.DeepEqual(new.Spec.ComponentVersions, old.Status.ComponentVersions) {
        return true
    }
    
    return false
}

// isScaleUpScene 判断是否为扩容场景
func (p *PhaseFlow) isScaleUpScene(old, new *bkev1beta1.BKECluster) bool {
    // Master 扩容
    if len(new.Spec.Nodes.Master) > len(old.Status.Nodes.Master) {
        return true
    }
    
    // Worker 扩容
    if len(new.Spec.Nodes.Worker) > len(old.Status.Nodes.Worker) {
        return true
    }
    
    // 预约添加节点
    if v, ok := annotation.HasAnnotation(new, annotation.AppointmentAddNodesAnnotationKey); ok && v != "" {
        return true
    }
    
    return false
}

// isScaleDownScene 判断是否为缩容场景
func (p *PhaseFlow) isScaleDownScene(old, new *bkev1beta1.BKECluster) bool {
    // Master 缩容
    if len(new.Spec.Nodes.Master) < len(old.Status.Nodes.Master) {
        return true
    }
    
    // Worker 缩容
    if len(new.Spec.Nodes.Worker) < len(old.Status.Nodes.Worker) {
        return true
    }
    
    // 预约删除节点
    if v, ok := annotation.HasAnnotation(new, annotation.AppointmentDeletedNodesAnnotationKey); ok && v != "" {
        return true
    }
    
    return false
}

// assemblePhasesByScene 根据场景组装 Phase 列表
func (p *PhaseFlow) assemblePhasesByScene(scene Scene) []func(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    switch scene {
    case SceneInit:
        return p.assembleInitPhases()
    case SceneUpgrade:
        return p.assembleUpgradePhases()
    case SceneScaleUp:
        return p.assembleScaleUpPhases()
    case SceneScaleDown:
        return p.assembleScaleDownPhases()
    case SceneDelete:
        return DeletePhases
    case ScenePause:
        return []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{NewEnsurePaused}
    case SceneDryRun:
        return []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{NewEnsureDryRun}
    case SceneManage:
        return []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{NewEnsureClusterManage}
    case SceneHealthCheck:
        return []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{NewEnsureCluster}
    default:
        return []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{NewEnsureCluster}
    }
}

// assembleInitPhases 组装安装场景的 Phase
func (p *PhaseFlow) assembleInitPhases() []func(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    phases := make([]func(ctx *phaseframe.PhaseContext) phaseframe.Phase, 0)
    
    // 1. Common Phases
    phases = append(phases, NewEnsureFinalizer)
    
    // 2. Deploy Phases
    phases = append(phases,
        NewEnsureBKEAgent,
        NewEnsureNodesEnv,
        NewEnsureClusterAPIObj,
        NewEnsureCerts,
        NewEnsureLoadBalance,
        NewEnsureMasterInit,
        NewEnsureMasterJoin,
        NewEnsureWorkerJoin,
        NewEnsureAddonDeploy,
        NewEnsureNodesPostProcess,
        NewEnsureAgentSwitch,
    )
    
    // 3. 健康检查
    phases = append(phases, NewEnsureCluster)
    
    return phases
}

// assembleUpgradePhases 组装升级场景的 Phase
func (p *PhaseFlow) assembleUpgradePhases() []func(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    phases := make([]func(ctx *phaseframe.PhaseContext) phaseframe.Phase, 0)
    old := p.oldBKECluster
    new := p.newBKECluster
    
    // 1. Provider 自升级
    if new.Spec.ProviderVersion != old.Status.ProviderVersion {
        phases = append(phases, NewEnsureProviderSelfUpgrade)
    }
    
    // 2. Agent 升级
    if new.Spec.AgentVersion != old.Status.AgentVersion {
        phases = append(phases, NewEnsureAgentUpgrade)
    }
    
    // 3. Containerd 升级
    if new.Spec.ContainerdVersion != old.Status.ContainerdVersion {
        phases = append(phases, NewEnsureContainerdUpgrade)
    }
    
    // 4. Etcd 升级
    if new.Spec.EtcdVersion != old.Status.EtcdVersion {
        phases = append(phases, NewEnsureEtcdUpgrade)
    }
    
    // 5. K8s 升级
    if new.Spec.KubernetesVersion != old.Status.KubernetesVersion {
        phases = append(phases,
            NewEnsureMasterUpgrade,
            NewEnsureWorkerUpgrade,
        )
    }
    
    // 6. 组件升级
    if !reflect.DeepEqual(new.Spec.ComponentVersions, old.Status.ComponentVersions) {
        phases = append(phases, NewEnsureComponentUpgrade)
    }
    
    // 7. 健康检查
    phases = append(phases, NewEnsureCluster)
    
    return phases
}

// assembleScaleUpPhases 组装扩容场景的 Phase
func (p *PhaseFlow) assembleScaleUpPhases() []func(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    phases := make([]func(ctx *phaseframe.PhaseContext) phaseframe.Phase, 0)
    old := p.oldBKECluster
    new := p.newBKECluster
    
    // Master 扩容
    if len(new.Spec.Nodes.Master) > len(old.Status.Nodes.Master) {
        phases = append(phases, NewEnsureMasterJoin)
    }
    
    // Worker 扩容
    if len(new.Spec.Nodes.Worker) > len(old.Status.Nodes.Worker) {
        phases = append(phases, NewEnsureWorkerJoin)
    }
    
    // 健康检查
    phases = append(phases, NewEnsureCluster)
    
    return phases
}

// assembleScaleDownPhases 组装缩容场景的 Phase
func (p *PhaseFlow) assembleScaleDownPhases() []func(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    phases := make([]func(ctx *phaseframe.PhaseContext) phaseframe.Phase, 0)
    old := p.oldBKECluster
    new := p.newBKECluster
    
    // Master 缩容
    if len(new.Spec.Nodes.Master) < len(old.Status.Nodes.Master) {
        phases = append(phases, NewEnsureMasterDelete)
    }
    
    // Worker 缩容
    if len(new.Spec.Nodes.Worker) < len(old.Status.Nodes.Worker) {
        phases = append(phases, NewEnsureWorkerDelete)
    }
    
    // 健康检查
    phases = append(phases, NewEnsureCluster)
    
    return phases
}

// CalculatePhase 重构后的实现
func (p *PhaseFlow) CalculatePhase(old, new *bkev1beta1.BKECluster) error {
    // 1. 判断场景
    scene := p.determineScene(old, new)
    p.ctx.Log.Info("Determined scene: %s", scene)
    
    // 2. 根据场景组装 Phase
    phasesFuncs := p.assemblePhasesByScene(scene)
    
    // 3. 创建 Phase 实例（无需 NeedExecute 判断）
    for _, f := range phasesFuncs {
        phase := f(p.ctx)
        p.BKEPhases = append(p.BKEPhases, phase)
    }
    
    // 4. 上报 Phase 状态
    return p.ReportPhaseStatus()
}
```

#### 方案 2：场景注册表模式

**文件**：`pkg/phaseframe/phases/scene_registry.go`
```go
package phases

import (
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

// SceneHandler 场景处理器
type SceneHandler struct {
    // Determine 判断是否为该场景
    Determine func(old, new *bkev1beta1.BKECluster) bool
    // Assemble 组装该场景的 Phase
    Assemble func(ctx *phaseframe.PhaseContext, old, new *bkev1beta1.BKECluster) []phaseframe.Phase
}

// SceneRegistry 场景注册表
var SceneRegistry = []SceneHandler{
    {Determine: isDeleteScene, Assemble: assembleDeletePhases},
    {Determine: isPauseScene, Assemble: assemblePausePhases},
    {Determine: isDryRunScene, Assemble: assembleDryRunPhases},
    {Determine: isManageScene, Assemble: assembleManagePhases},
    {Determine: isInitScene, Assemble: assembleInitPhases},
    {Determine: isUpgradeScene, Assemble: assembleUpgradePhases},
    {Determine: isScaleUpScene, Assemble: assembleScaleUpPhases},
    {Determine: isScaleDownScene, Assemble: assembleScaleDownPhases},
    {Determine: isHealthCheckScene, Assemble: assembleHealthCheckPhases},
}

// CalculatePhase 使用场景注册表
func (p *PhaseFlow) CalculatePhase(old, new *bkev1beta1.BKECluster) error {
    // 遍历场景注册表，找到第一个匹配的场景
    for _, handler := range SceneRegistry {
        if handler.Determine(old, new) {
            phases := handler.Assemble(p.ctx, old, new)
            p.BKEPhases = append(p.BKEPhases, phases...)
            break
        }
    }
    
    return p.ReportPhaseStatus()
}
```

### 三、重构后的优势

#### 1. 性能提升

```go
// 重构前：创建所有 Phase，再过滤
for _, f := range FullPhasesRegisFunc {  // 26 个 Phase
    phase := f(p.ctx)  // 创建 26 个 Phase 实例
    if phase.NeedExecute(old, new) {  // 判断 26 次
        p.BKEPhases = append(p.BKEPhases, phase)
    }
}

// 重构后：只创建需要的 Phase
scene := p.determineScene(old, new)  // 判断 1 次
phasesFuncs := p.assemblePhasesByScene(scene)  // 只组装需要的 Phase
for _, f := range phasesFuncs {  // 例如升级场景只有 7 个 Phase
    phase := f(p.ctx)  // 只创建 7 个 Phase 实例
    p.BKEPhases = append(p.BKEPhases, phase)
}
```

#### 2. 代码可读性提升

```go
// 重构前：场景判断分散在各个 Phase 中
func (e *EnsureMasterUpgrade) NeedExecute(old, new) bool {
    // 版本判断
    if new.Spec.KubernetesVersion != new.Status.KubernetesVersion {
        nodes := GetNeedUpgradeMasterNodes(new)
        return nodes.Length() > 0
    }
    return false
}

// 重构后：场景判断集中在一处
func (p *PhaseFlow) isUpgradeScene(old, new) bool {
    return new.Spec.KubernetesVersion != old.Status.KubernetesVersion ||
           new.Spec.EtcdVersion != old.Status.EtcdVersion ||
           new.Spec.ContainerdVersion != old.Status.ContainerdVersion
}

func (p *PhaseFlow) assembleUpgradePhases() []PhaseFunc {
    if new.Spec.KubernetesVersion != old.Status.KubernetesVersion {
        phases = append(phases, NewEnsureMasterUpgrade, NewEnsureWorkerUpgrade)
    }
    // ...
}
```

#### 3. 执行路径清晰

```go
// 重构前：无法直观看出执行路径
FullPhasesRegisFunc = CommonPhases + DeployPhases + PostDeployPhases  // 26 个 Phase

// 重构后：每个场景的执行路径一目了然
SceneInit:      [EnsureFinalizer, EnsureBKEAgent, ..., EnsureCluster]  // 13 个 Phase
SceneUpgrade:   [EnsureEtcdUpgrade, EnsureMasterUpgrade, ..., EnsureCluster]  // 7 个 Phase
SceneScaleUp:   [EnsureMasterJoin, EnsureWorkerJoin, EnsureCluster]  // 3 个 Phase
SceneScaleDown: [EnsureMasterDelete, EnsureWorkerDelete, EnsureCluster]  // 3 个 Phase
```

### 四、Phase NeedExecute 简化

重构后，Phase 的 NeedExecute 可以大幅简化：
```go
// 重构前：复杂的场景判断
func (e *EnsureMasterUpgrade) NeedExecute(old, new) bool {
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    
    if new.Spec.KubernetesVersion != new.Status.KubernetesVersion {
        nodes := GetNeedUpgradeMasterNodes(new)
        return nodes.Length() > 0
    }
    return false
}

// 重构后：只需检查是否已完成
func (e *EnsureMasterUpgrade) NeedExecute(old, new) bool {
    // 只检查是否已成功执行
    return e.GetStatus() != bkev1beta1.PhaseSucceeded
}

// 或者完全移除 NeedExecute，在 assemble 时已经确保需要执行
```

### 五、ClusterStatus 设置简化

```go
// 重构前：通过 Phase 名称分组判断
func calculateClusterStatusByPhase(phase, err) {
    switch {
    case phase.Name().In(ClusterInitPhaseNames):
        handleClusterInitPhase(ctx, err)
    case phase.Name().In(ClusterUpgradePhaseNames):
        handleClusterUpgradePhase(ctx, err)
    // ...
    }
}

// 重构后：直接根据场景设置
func (p *PhaseFlow) setClusterStatusByScene(scene Scene, err error) {
    statusMap := map[Scene]bkev1beta1.ClusterStatus{
        SceneInit:        bkev1beta1.ClusterInitializing,
        SceneUpgrade:     bkev1beta1.ClusterUpgrading,
        SceneScaleUp:     bkev1beta1.ClusterMasterScalingUp,
        SceneScaleDown:   bkev1beta1.ClusterWorkerScalingDown,
        SceneDelete:      bkev1beta1.ClusterDeleting,
        ScenePause:       bkev1beta1.ClusterPaused,
        SceneHealthCheck: bkev1beta1.ClusterChecking,
    }
    
    if err != nil {
        p.ctx.BKECluster.Status.ClusterStatus = getFailedStatus(scene)
    } else {
        p.ctx.BKECluster.Status.ClusterStatus = statusMap[scene]
    }
}
```

### 六、完整重构示例

**文件**：`pkg/phaseframe/phases/phase_flow_refactored.go`
```go
package phases

import (
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

// PhaseFlow 重构后的 PhaseFlow
type PhaseFlow struct {
    BKEPhases      []phaseframe.Phase
    ctx            *phaseframe.PhaseContext
    oldBKECluster  *bkev1beta1.BKECluster
    newBKECluster  *bkev1beta1.BKECluster
    scene          Scene  // 当前场景
}

// CalculatePhase 根据场景组装 Phase
func (p *PhaseFlow) CalculatePhase(old, new *bkev1beta1.BKECluster) error {
    p.oldBKECluster = old
    p.newBKECluster = new
    
    // 1. 判断场景
    p.scene = p.determineScene(old, new)
    p.ctx.Log.Info("Cluster scene: %s", p.scene)
    
    // 2. 设置 ClusterStatus
    p.setClusterStatusByScene(p.scene, nil)
    
    // 3. 组装 Phase
    phasesFuncs := p.assemblePhasesByScene(p.scene)
    
    // 4. 创建 Phase 实例
    for _, f := range phasesFuncs {
        phase := f(p.ctx)
        p.BKEPhases = append(p.BKEPhases, phase)
    }
    
    // 5. 上报 Phase 状态
    return p.ReportPhaseStatus()
}

// Execute 执行 Phase
func (p *PhaseFlow) Execute() (ctrl.Result, error) {
    defer p.handlePanic()
    
    var errs []error
    var res ctrl.Result
    
    for _, phase := range p.BKEPhases {
        p.ctx.Log.Info("Executing phase: %s", phase.Name())
        
        // 执行前置 hook
        if err := phase.ExecutePreHook(); err != nil {
            return res, err
        }
        
        // 执行 Phase
        phaseResult, phaseErr := phase.Execute()
        if phaseErr != nil {
            errs = append(errs, phaseErr)
        }
        res = util.LowestNonZeroResult(res, phaseResult)
        
        // 执行后置 hook
        if err := phase.ExecutePostHook(phaseErr); err != nil {
            errs = append(errs, err)
        }
        
        // 如果失败，设置失败状态
        if phaseErr != nil {
            p.setClusterStatusByScene(p.scene, phaseErr)
            break
        }
    }
    
    if len(errs) > 0 {
        return res, kerrors.NewAggregate(errs)
    }
    return res, nil
}
```

### 七、总结

**重构收益**：

| 方面 | 重构前 | 重构后 |
|------|--------|--------|
| **Phase 创建数量** | 26 个（全部） | 按场景创建（3-13 个） |
| **场景判断次数** | 26 次（每个 Phase） | 1 次（集中判断） |
| **代码可读性** | 分散在各个 Phase | 集中在一处 |
| **执行路径** | 不直观 | 一目了然 |
| **维护成本** | 高（修改需改多个 Phase） | 低（修改只需改一处） |

**重构步骤**：

1. ✅ 定义场景类型
2. ✅ 实现场景判断函数
3. ✅ 实现各场景的 Phase 组装函数
4. ✅ 重构 CalculatePhase 方法
5. ✅ 简化 Phase 的 NeedExecute
6. ✅ 简化 ClusterStatus 设置逻辑
7. ✅ 添加单元测试验证各场景

这种重构方式使代码更加清晰、高效、易维护，符合单一职责原则和开闭原则。

# 详细说明扩缩容场景如何由 KCP 移交到 Phase 的机制

## 扩缩容场景：KCP → Phase 移交机制

### 一、整体流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│  用户操作：修改 BKECluster.Spec.Nodes                                 │
│  • 扩容：增加 Master/Worker 节点                                      │
│  • 缩容：减少 Master/Worker 节点                                      │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  BKEClusterReconciler 触发                                           │
│  └── BKEClusterSpecChange Predicate 检测到 Generation 变化            │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  PhaseFlow.CalculatePhase() 判断场景                                 │
│  ├── 扩容：len(new.Spec.Nodes) > len(old.Status.Nodes)               │
│  └── 缩容：len(new.Spec.Nodes) < len(old.Status.Nodes)               │
└─────────────────────────────────────────────────────────────────────┘
                                │
                ┌───────────────┴───────────────┐
                │                               │
                ▼                               ▼
┌──────────────────────────┐      ┌────────────────────────────┐
│  扩容场景                 │      │  缩容场景                    │
│                          │      │                            │
│  Master: EnsureMasterJoin│      │  Master: EnsureMasterDelete│
│  Worker: EnsureWorkerJoin│      │  Worker: EnsureWorkerDelete│
└──────────────────────────┘      └────────────────────────────┘
                │                               │
                └───────────────┬───────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Phase 操作 CAPI 对象                                                │
│  ├── 暂停 CAPI 控制器                                                 │
│  ├── 修改 Replicas（扩容 +N / 缩容 -N）                               │
│  ├── 标记删除 Machine（缩容时）                                       │
│  └── 恢复 CAPI 控制器                                                 │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  CAPI 控制器接管                                                      │
│  ├── KCP Controller：根据 Replicas 创建/删除 Machine                   │
│  └── MD Controller：根据 Replicas 创建/删除 Machine                    │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  BKEMachine Controller 处理 Machine 生命周期                          │
│  ├── 创建：引导节点加入集群                                             │
│  └── 删除：清理节点资源                                                │
└─────────────────────────────────────────────────────────────────────┘
```

### 二、扩容流程详解

#### 1. Master 扩容：EnsureMasterJoin

**文件**：[ensure_master_join.go:200-253](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go#L200-L253)
```go
func (e *EnsureMasterJoin) scaleAndJoinMasterNodes(params MasterJoinScaleParams) error {
    // 1. 获取 KubeadmControlPlane
    scope, err := phaseutil.GetClusterAPIAssociateObjs(params.Ctx, params.Client, e.Ctx.Cluster)
    
    // 2. 保存当前 Replicas（用于回滚）
    specCopy := scope.KubeadmControlPlane.Spec.DeepCopy()
    currentReplicas := specCopy.Replicas
    
    // 3. 计算期望 Replicas
    exceptReplicas := *currentReplicas + int32(params.NodesCount)
    // 不能超过 BKECluster 的 Master 数量
    if exceptReplicas > int32(masterNodes.Length()) {
        exceptReplicas = int32(masterNodes.Length())
    }
    
    // 4. 更新 KCP Replicas
    scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas
    
    // 5. 恢复 KCP（触发 CAPI 控制器创建 Machine）
    if err = phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, scope.KubeadmControlPlane); err != nil {
        return err
    }
    
    // 6. 等待节点加入
    if err = e.waitMasterJoin(params.NodesCount); err != nil {
        return err
    }
    
    return nil
}
```
**关键步骤**：

| 步骤 | 操作 | 说明 |
|------|------|------|
| 1 | 获取 KCP | 从 CAPI 获取 KubeadmControlPlane 对象 |
| 2 | 保存 Replicas | 用于失败时回滚 |
| 3 | 计算 Replicas | `currentReplicas + nodesCount` |
| 4 | 更新 KCP | `KCP.Spec.Replicas = exceptReplicas` |
| 5 | 恢复 KCP | 移除 paused annotation，触发 CAPI 控制器 |
| 6 | 等待加入 | 等待 BKEMachine Controller 完成引导 |

#### 2. Worker 扩容：EnsureWorkerJoin

**文件**：[ensure_worker_join.go:190-230](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go#L190-L230)
```go
func (e *EnsureWorkerJoin) scaleMachineDeployment(params ScaleMachineDeploymentParams) error {
    // 1. 获取 MachineDeployment
    scope := params.Scope
    
    // 2. 保存当前 Replicas
    specCopy := scope.MachineDeployment.Spec.DeepCopy()
    currentReplicas := specCopy.Replicas
    
    // 3. 计算期望 Replicas
    exceptReplicas := *currentReplicas + int32(params.NodesCount)
    // 不能超过 BKECluster 的 Worker 数量
    if exceptReplicas > int32(workerNodes.Length()) {
        exceptReplicas = int32(workerNodes.Length())
    }
    
    // 4. 更新 MD Replicas
    scope.MachineDeployment.Spec.Replicas = &exceptReplicas
    
    // 5. 恢复 MD（触发 CAPI 控制器创建 Machine）
    if scaleErr = phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, scope.MachineDeployment); scaleErr != nil {
        return scaleErr
    }
    
    // 6. 等待节点加入
    if scaleErr = e.waitWorkerJoin(); scaleErr != nil {
        return scaleErr
    }
    
    return nil
}
```

### 三、缩容流程详解

#### 1. Master 缩容：EnsureMasterDelete

**文件**：[ensure_master_delete.go:165-235](file:////cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_delete.go#L165-L235)
```go
func (e *EnsureMasterDelete) pauseAndScaleDownControlPlane(params PauseAndScaleDownControlPlaneParams) error {
    // 1. 获取 KubeadmControlPlane
    scope, err := phaseutil.GetClusterAPIAssociateObjs(ctx, c, e.Ctx.Cluster)
    
    // 2. 暂停 KCP（防止 CAPI 控制器干扰）
    if err = phaseutil.PauseClusterAPIObj(ctx, c, scope.KubeadmControlPlane); err != nil {
        return err
    }
    
    // 3. 保存当前 Replicas（用于回滚）
    specCopy := scope.KubeadmControlPlane.Spec.DeepCopy()
    currentReplicas := specCopy.Replicas
    
    // 4. 标记需要删除的 Machine
    for _, machineAndNode := range deleteMap {
        machine := machineAndNode.Machine
        if err = phaseutil.MarkMachineForDeletion(ctx, c, machine); err != nil {
            // 标记失败，跳过该节点
            delete(deleteMap, machine.Name)
        }
    }
    
    // 5. 计算 Replicas
    exceptReplicas := *currentReplicas - int32(len(deleteMap))
    // 副本数不能小于 1
    if exceptReplicas < 1 {
        exceptReplicas = 1
    }
    
    // 6. 更新 KCP Replicas
    scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas
    
    // 7. 恢复 KCP（触发 CAPI 控制器删除 Machine）
    if err = phaseutil.ResumeClusterAPIObj(ctx, c, scope.KubeadmControlPlane); err != nil {
        return err
    }
    
    return nil
}
```

**关键步骤**：

| 步骤 | 操作 | 说明 |
|------|------|------|
| 1 | 获取 KCP | 从 CAPI 获取 KubeadmControlPlane 对象 |
| 2 | **暂停 KCP** | 添加 paused annotation，停止 CAPI 控制器 |
| 3 | 保存 Replicas | 用于失败时回滚 |
| 4 | **标记删除** | 为特定 Machine 添加删除标记 |
| 5 | 计算 Replicas | `currentReplicas - len(deleteMap)` |
| 6 | 更新 KCP | `KCP.Spec.Replicas = exceptReplicas` |
| 7 | 恢复 KCP | 移除 paused annotation，触发 CAPI 控制器 |

### 四、Pause/Resume 机制

#### 1. PauseClusterAPIObj

**文件**：[clusterapi.go:129-147](file:////cluster-api-provider-bke/pkg/phaseframe/phaseutil/clusterapi.go#L129-L147)
```go
func PauseClusterAPIObj(ctx context.Context, c client.Client, obj client.Object, extraAnnotations ...string) error {
    // 添加 paused annotation
    annotations := obj.GetAnnotations()
    if annotations == nil {
        annotations = map[string]string{}
    }
    annotations[clusterv1beta1.PausedAnnotation] = ""  // "cluster.x-k8s.io/paused": ""
    
    obj.SetAnnotations(annotations)
    return c.Update(ctx, obj)
}
```
**效果**：
- KCP Controller 检测到 paused annotation 后停止调谐
- MD Controller 检测到 paused annotation 后停止调谐
- Phase 可以安全修改 CAPI 对象而不被干扰

#### 2. ResumeClusterAPIObj

**文件**：[clusterapi.go:149-170](file:////cluster-api-provider-bke/pkg/phaseframe/phaseutil/clusterapi.go#L149-L170)
```go
func ResumeClusterAPIObj(ctx context.Context, c client.Client, obj client.Object, extraAnnotations ...string) error {
    // 移除 paused annotation
    annotations := obj.GetAnnotations()
    delete(annotations, clusterv1beta1.PausedAnnotation)
    
    obj.SetAnnotations(annotations)
    return c.Update(ctx, obj)
}
```
**效果**：
- KCP Controller 恢复调谐，根据 Replicas 创建/删除 Machine
- MD Controller 恢复调谐，根据 Replicas 创建/删除 Machine

### 五、CAPI 控制器接管

#### 1. KCP Controller 行为

```
KCP Controller 检测到 Replicas 变化
    │
    ├── Replicas 增加
    │   └── 创建新的 Machine 对象
    │       ├── Machine.Spec.InfrastructureRef → BKEMachine
    │       └── Machine.Spec.Bootstrap.ConfigRef → KubeadmConfig
    │
    └── Replicas 减少
        └── 删除 Machine 对象（优先删除标记为删除的）
            └── 触发 BKEMachine Controller 清理节点
```

#### 2. MD Controller 行为

```
MD Controller 检测到 Replicas 变化
    │
    ├── Replicas 增加
    │   └── 创建新的 Machine 对象
    │       ├── Machine.Spec.InfrastructureRef → BKEMachine
    │       └── Machine.Spec.Bootstrap.ConfigRef → KubeadmConfig
    │
    └── Replicas 减少
        └── 删除 Machine 对象
            └── 触发 BKEMachine Controller 清理节点
```

### 六、BKEMachine Controller 处理

```
BKEMachine Controller 监听 Machine
    │
    ├── Machine 创建
    │   ├── 获取关联的 BKENode
    │   ├── 执行节点引导
    │   │   ├── 安装 BKEAgent
    │   │   ├── 准备节点环境
    │   │   ├── 执行 kubeadm join
    │   │   └── 后处理配置
    │   └── 更新 BKEMachine.Status
    │
    └── Machine 删除
        ├── Drain 节点
        ├── 清理节点资源
        └── 更新 BKEMachine.Status
```

### 七、完整扩容示例

```
用户操作：BKECluster.Spec.Nodes.Master 增加 2 个节点
    │
    ├── BKEClusterReconciler 触发
    │   └── PhaseFlow 判断场景：ScaleUp
    │
    ├── EnsureMasterJoin.Execute()
    │   ├── 获取需要加入的节点：[node1, node2]
    │   ├── 获取 KCP：KCP.Spec.Replicas = 3
    │   ├── 计算 Replicas：3 + 2 = 5
    │   ├── 更新 KCP：KCP.Spec.Replicas = 5
    │   └── 恢复 KCP：移除 paused annotation
    │
    ├── KCP Controller 接管
    │   ├── 检测到 Replicas = 5
    │   ├── 创建 Machine-4
    │   │   └── Machine.Spec.InfrastructureRef → BKEMachine-4
    │   └── 创建 Machine-5
    │       └── Machine.Spec.InfrastructureRef → BKEMachine-5
    │
    ├── BKEMachine Controller 处理
    │   ├── BKEMachine-4 引导
    │   │   ├── 安装 BKEAgent
    │   │   ├── 准备环境
    │   │   ├── kubeadm join
    │   │   └── 后处理
    │   └── BKEMachine-5 引导
    │       ├── 安装 BKEAgent
    │       ├── 准备环境
    │       ├── kubeadm join
    │       └── 后处理
    │
    └── EnsureMasterJoin.waitMasterJoin()
        └── 等待 2 个节点加入成功
```

### 八、完整缩容示例

```
用户操作：BKECluster.Spec.Nodes.Master 减少 2 个节点
    │
    ├── BKEClusterReconciler 触发
    │   └── PhaseFlow 判断场景：ScaleDown
    │
    ├── EnsureMasterDelete.Execute()
    │   ├── 获取需要删除的节点：[node3, node4]
    │   ├── 获取 KCP：KCP.Spec.Replicas = 5
    │   ├── 暂停 KCP：添加 paused annotation
    │   ├── 标记删除：
    │   │   ├── Machine-3：添加删除标记
    │   │   └── Machine-4：添加删除标记
    │   ├── 计算 Replicas：5 - 2 = 3
    │   ├── 更新 KCP：KCP.Spec.Replicas = 3
    │   └── 恢复 KCP：移除 paused annotation
    │
    ├── KCP Controller 接管
    │   ├── 检测到 Replicas = 3
    │   ├── 检测到删除标记
    │   ├── 删除 Machine-3
    │   └── 删除 Machine-4
    │
    └── BKEMachine Controller 处理
        ├── BKEMachine-3 清理
        │   ├── Drain 节点
        │   └── 清理资源
        └── BKEMachine-4 清理
            ├── Drain 节点
            └── 清理资源
```

### 九、关键设计点

#### 1. 为什么需要 Pause/Resume？

```go
// ❌ 不暂停直接修改 Replicas
KCP.Spec.Replicas = 5
c.Update(ctx, KCP)
// 问题：KCP Controller 可能立即调谐，与 Phase 产生竞争

// ✅ 正确做法：暂停 → 修改 → 恢复
PauseClusterAPIObj(ctx, c, KCP)    // 暂停 KCP Controller
KCP.Spec.Replicas = 5              // 安全修改
ResumeClusterAPIObj(ctx, c, KCP)   // 恢复 KCP Controller
```

#### 2. 为什么缩容需要标记删除？

```go
// ❌ 不标记删除，KCP Controller 可能删除错误的节点
KCP.Spec.Replicas = 3  // 从 5 减少到 3
// KCP Controller 可能删除 node1, node2（而不是 node3, node4）

// ✅ 正确做法：标记要删除的节点
MarkMachineForDeletion(ctx, c, Machine-3)
MarkMachineForDeletion(ctx, c, Machine-4)
KCP.Spec.Replicas = 3
// KCP Controller 会优先删除标记的节点
```

#### 3. 为什么需要回滚机制？

```go
// 保存当前 Replicas
currentReplicas := KCP.Spec.Replicas

// 失败时回滚
defer func() {
    if err != nil {
        KCP.Spec.Replicas = currentReplicas
        ResumeClusterAPIObj(ctx, c, KCP)
    }
}()

// 如果后续步骤失败，自动恢复到原始状态
```

### 十、总结

**移交流程**：

| 阶段 | 执行者 | 操作 |
|------|--------|------|
| **1. 触发** | BKEClusterReconciler | 检测 Spec.Nodes 变化 |
| **2. 判断场景** | PhaseFlow | 判断扩容/缩容 |
| **3. Phase 执行** | EnsureMasterJoin/Delete | 操作 CAPI 对象 |
| **4. 暂停 CAPI** | Phase | PauseClusterAPIObj |
| **5. 修改 Replicas** | Phase | KCP.Spec.Replicas ± N |
| **6. 恢复 CAPI** | Phase | ResumeClusterAPIObj |
| **7. CAPI 接管** | KCP/MD Controller | 创建/删除 Machine |
| **8. 节点处理** | BKEMachine Controller | 引导/清理节点 |

**关键机制**：
- ✅ **Pause/Resume**：控制 CAPI 控制器的调谐时机
- ✅ **Replicas**：触发 CAPI 控制器创建/删除 Machine
- ✅ **MarkMachineForDeletion**：精确控制删除哪些节点
- ✅ **回滚机制**：失败时恢复原始状态

**设计问题**：
- ❌ 绕过 CAPI 的声明式能力，自研命令式逻辑
- ❌ Pause/Resume 增加复杂度，容易出错
- ❌ 标记删除机制与 CAPI 标准做法不一致

# 详细说明 KCP Controller 与 Phase 之间的竞争点

## KCP Controller 与 Phase 的竞争点

### 一、竞争场景总览

```
┌─────────────────────────────────────────────────────────────────────┐
│  Phase 执行流程（不暂停）                                              │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  T1: Phase 读取 KCP                                                  │
│      KCP.Spec.Replicas = 3                                          │
└─────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  T2: Phase 更新 KCP                                                  │
│      KCP.Spec.Replicas = 5                                          │
│      c.Update(ctx, KCP)                                             │
└─────────────────────────────────────────────────────────────────────┘
                                │
                ┌───────────────┴───────────────┐
                │                               │
                ▼                               ▼
┌──────────────────────────┐      ┌──────────────────────────┐
│  T3: Phase 继续执行       │      │  T3': KCP Controller     │
│      标记删除 Machine-3   │      │      检测到 Replicas=5    │
│      标记删除 Machine-4   │      │      创建 Machine-4       │
└──────────────────────────┘      │      创建 Machine-5       │
                │                 └──────────────────────────┘
                │                               │
                └───────────────┬───────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  竞争结果：                                                          │
│  • Phase 想删除 Machine-3, Machine-4                                │
│  • KCP Controller 创建了 Machine-4, Machine-5                       │
│  • 最终状态混乱：Machine-4 既被标记删除又被创建                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 二、具体竞争点分析

#### 竞争点 1：Replicas 更新竞争

**场景**：Phase 想扩容，KCP Controller 同时也在处理
```go
// Phase 执行（不暂停）
func (e *EnsureMasterJoin) scaleAndJoinMasterNodes() error {
    // T1: Phase 读取 KCP
    scope, _ := phaseutil.GetClusterAPIAssociateObjs(ctx, c, cluster)
    // KCP.Spec.Replicas = 3
    
    // T2: Phase 计算新 Replicas
    exceptReplicas := *currentReplicas + 2  // 3 + 2 = 5
    
    // T3: Phase 更新 KCP
    scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas
    c.Update(ctx, scope.KubeadmControlPlane)  // KCP.Spec.Replicas = 5
    
    // 问题：此时 KCP Controller 可能已经开始处理
}
```

**KCP Controller 并发执行**：
```go
// KCP Controller（在另一个 goroutine 中）
func (r *KubeadmControlPlaneReconciler) Reconcile(ctx, req) {
    // T3': 检测到 KCP.Spec.Replicas = 5
    kcp := r.getKubeadmControlPlane(ctx, req)
    
    // T4': 计算需要创建的 Machine 数量
    currentMachines := r.getCurrentMachines(ctx, kcp)  // 3 个 Machine
    desiredMachines := *kcp.Spec.Replicas              // 5
    
    // T5': 创建新 Machine
    for i := 0; i < desiredMachines - len(currentMachines); i++ {
        r.createMachine(ctx, kcp)  // 创建 Machine-4, Machine-5
    }
}
```

**竞争结果**：
```
Phase 想要：
  - 扩容到 5 个节点
  - 创建 Machine-4, Machine-5

KCP Controller 同时：
  - 检测到 Replicas = 5
  - 创建 Machine-4, Machine-5

结果：
  ✅ 正常：创建了 2 个 Machine
  ❌ 问题：Phase 还没标记节点，KCP Controller 可能选择了错误的节点
```

#### 竞争点 2：标记删除竞争（最严重）

**场景**：Phase 想删除特定节点，KCP Controller 可能删除错误的节点
```go
// Phase 执行（不暂停）
func (e *EnsureMasterDelete) pauseAndScaleDownControlPlane() error {
    // T1: 获取 KCP
    scope, _ := phaseutil.GetClusterAPIAssociateObjs(ctx, c, cluster)
    // KCP.Spec.Replicas = 5
    
    // T2: 标记要删除的 Machine
    // 想删除 node3, node4
    phaseutil.MarkMachineForDeletion(ctx, c, Machine-3)  // 添加删除标记
    phaseutil.MarkMachineForDeletion(ctx, c, Machine-4)  // 添加删除标记
    
    // T3: 更新 Replicas
    exceptReplicas := 5 - 2  // 3
    scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas
    c.Update(ctx, scope.KubeadmControlPlane)
    
    // 问题：KCP Controller 可能在 T2 和 T3 之间就开始删除了
}
```

**KCP Controller 并发执行**：
```go
// KCP Controller（在另一个 goroutine 中）
func (r *KubeadmControlPlaneReconciler) Reconcile(ctx, req) {
    // T2': 检测到 KCP.Spec.Replicas = 3（Phase 还没更新）
    //      或者检测到删除标记（Phase 已经标记）
    
    // 情况 1：Phase 还没标记删除
    if !hasDeleteAnnotation {
        // KCP Controller 选择删除哪些节点？
        // 可能删除 node1, node2（而不是 node3, node4）
        r.deleteMachine(ctx, Machine-1)
        r.deleteMachine(ctx, Machine-2)
    }
    
    // 情况 2：Phase 已经标记删除
    if hasDeleteAnnotation {
        // KCP Controller 优先删除标记的节点
        r.deleteMachine(ctx, Machine-3)
        r.deleteMachine(ctx, Machine-4)
    }
}
```

**竞争结果**：
```
┌─────────────────────────────────────────────────────────────────────┐
│  情况 1：Phase 标记删除时，KCP Controller 已经开始删除                   │
└─────────────────────────────────────────────────────────────────────┘
Phase 想要：
  - 删除 node3, node4
  - 标记 Machine-3, Machine-4 删除

KCP Controller 同时：
  - 检测到 Replicas 需要从 5 减到 3
  - 选择删除 Machine-1, Machine-2（错误的节点）

结果：
  ❌ 删除了错误的节点（node1, node2）
  ❌ Phase 标记的节点（node3, node4）仍然存在
  ❌ 集群状态混乱

┌─────────────────────────────────────────────────────────────────────┐
│  情况 2：Phase 标记删除完成，KCP Controller 才开始删除                   │
└─────────────────────────────────────────────────────────────────────┘
Phase 想要：
  - 删除 node3, node4
  - 标记 Machine-3, Machine-4 删除

KCP Controller：
  - 检测到删除标记
  - 删除 Machine-3, Machine-4

结果：
  ✅ 删除了正确的节点
```

#### 竞争点 3：状态更新竞争

**场景**：Phase 和 KCP Controller 都在更新 KCP 的状态
```go
// Phase 执行
func (e *EnsureMasterJoin) scaleAndJoinMasterNodes() error {
    // T1: Phase 更新 KCP
    scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas
    c.Update(ctx, scope.KubeadmControlPlane)
    
    // T2: Phase 等待节点加入
    e.waitMasterJoin(nodesCount)
    
    // T3: Phase 更新 BKECluster 状态
    bkeCluster.Status.ClusterStatus = ClusterMasterScalingUp
    c.Status().Update(ctx, bkeCluster)
}
```

**KCP Controller 并发执行**：
```go
// KCP Controller
func (r *KubeadmControlPlaneReconciler) Reconcile(ctx, req) {
    // T2': 创建 Machine
    r.createMachines(ctx, kcp)
    
    // T3': 更新 KCP 状态
    kcp.Status.ReadyReplicas = 5
    kcp.Status.UpdatedReplicas = 5
    c.Status().Update(ctx, kcp)
}
```

**竞争结果**：
```
Phase 更新：
  - BKECluster.Status.ClusterStatus = ClusterMasterScalingUp

KCP Controller 更新：
  - KCP.Status.ReadyReplicas = 5
  - KCP.Status.UpdatedReplicas = 5

问题：
  ❌ 两个控制器都在更新状态，可能产生冲突
  ❌ 如果使用乐观锁，可能产生 Update 冲突
```

### 三、为什么需要 Pause/Resume

#### 1. Pause 的作用

```go
// PauseClusterAPIObj 添加 paused annotation
func PauseClusterAPIObj(ctx context.Context, c client.Client, obj client.Object) error {
    annotations := obj.GetAnnotations()
    annotations[clusterv1beta1.PausedAnnotation] = ""  // "cluster.x-k8s.io/paused": ""
    obj.SetAnnotations(annotations)
    return c.Update(ctx, obj)
}
```

**KCP Controller 检测到 paused annotation 后的行为**：
```go
// CAPI KubeadmControlPlane Controller
func (r *KubeadmControlPlaneReconciler) Reconcile(ctx, req) {
    kcp := r.getKubeadmControlPlane(ctx, req)
    
    // 检查 paused annotation
    if annotations.HasPausedAnnotation(kcp) {
        // 暂停调谐，直接返回
        log.Info("KubeadmControlPlane is paused, skipping reconciliation")
        return ctrl.Result{}, nil
    }
    
    // 正常调谐逻辑
    // ...
}
```

#### 2. 正确的执行流程（使用 Pause/Resume）

```go
func (e *EnsureMasterDelete) pauseAndScaleDownControlPlane() error {
    // Step 1: 暂停 KCP Controller
    phaseutil.PauseClusterAPIObj(ctx, c, scope.KubeadmControlPlane)
    // 此时 KCP Controller 停止调谐
    
    // Step 2: 安全地标记删除
    phaseutil.MarkMachineForDeletion(ctx, c, Machine-3)
    phaseutil.MarkMachineForDeletion(ctx, c, Machine-4)
    // KCP Controller 不会干扰
    
    // Step 3: 安全地更新 Replicas
    scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas
    c.Update(ctx, scope.KubeadmControlPlane)
    // KCP Controller 不会干扰
    
    // Step 4: 恢复 KCP Controller
    phaseutil.ResumeClusterAPIObj(ctx, c, scope.KubeadmControlPlane)
    // 此时 KCP Controller 恢复调谐
    
    // Step 5: KCP Controller 检测到删除标记和 Replicas 变化
    // 优先删除标记的 Machine-3, Machine-4
}
```

**时序图**：
```
Phase                    KCP Controller
  │                            │
  ├── Pause KCP ───────────────┤
  │                            │ (停止调谐)
  │                            │
  ├── 标记删除 Machine-3        │
  ├── 标记删除 Machine-4        │
  │                            │
  ├── 更新 Replicas = 3         │
  │                            │
  ├── Resume KCP ──────────────┤
  │                            │ (恢复调谐)
  │                            │
  │                            ├── 检测到删除标记
  │                            ├── 删除 Machine-3
  │                            └── 删除 Machine-4
  │
  └── 等待删除完成
```

### 四、竞争的根源

#### 1. CAPI 的声明式设计

```go
// ✅ CAPI 标准做法：声明式
// 用户只需声明最终状态，CAPI Controller 自动调谐到该状态

// 扩容：修改 Replicas
KCP.Spec.Replicas = 5
c.Update(ctx, KCP)
// KCP Controller 自动创建 Machine

// 缩容：修改 Replicas
KCP.Spec.Replicas = 3
c.Update(ctx, KCP)
// KCP Controller 自动删除 Machine（按一定策略选择删除哪些）
```

#### 2. BKE 的命令式设计

```go
// ❌ BKE 做法：命令式
// Phase 想精确控制删除哪些节点

// 1. 标记要删除的节点
MarkMachineForDeletion(Machine-3)
MarkMachineForDeletion(Machine-4)

// 2. 修改 Replicas
KCP.Spec.Replicas = 3

// 问题：如果不暂停，KCP Controller 可能在步骤 1 和 2 之间就开始删除了
```

### 五、竞争的具体表现

#### 1. 缩容时删除错误的节点

```
期望：删除 node3, node4
实际：删除了 node1, node2

原因：
  - Phase 标记删除 node3, node4 时
  - KCP Controller 已经检测到 Replicas 需要减少
  - KCP Controller 按自己的策略选择了 node1, node2
```

#### 2. 扩容时创建重复的 Machine

```
期望：创建 Machine-4, Machine-5
实际：创建了 Machine-4, Machine-5, Machine-6

原因：
  - Phase 更新 Replicas = 5
  - KCP Controller 立即创建了 Machine-4, Machine-5
  - Phase 再次检查时发现 Replicas 不够，又更新为 6
  - KCP Controller 又创建了 Machine-6
```

#### 3. 状态不一致

```
期望：BKECluster.Status 显示 3 个 Master
实际：KCP 有 5 个 Machine，BKECluster.Status 显示 3 个

原因：
  - Phase 更新了 BKECluster.Status
  - KCP Controller 创建了额外的 Machine
  - 两者状态不同步
```

### 六、总结

**竞争点**：

| 竞争点 | 描述 | 后果 |
|--------|------|------|
| **Replicas 更新** | Phase 和 KCP Controller 同时修改 Replicas | 创建/删除错误的节点 |
| **标记删除** | Phase 标记删除时，KCP Controller 已经开始删除 | 删除错误的节点 |
| **状态更新** | Phase 和 KCP Controller 都在更新状态 | 状态不一致、Update 冲突 |

**Pause/Resume 的作用**：
- ✅ **暂停 KCP Controller**：Phase 可以安全地标记删除、更新 Replicas
- ✅ **恢复 KCP Controller**：KCP Controller 按照标记执行删除
- ✅ **避免竞争**：确保删除正确的节点

**设计问题**：
- ❌ BKE 使用命令式设计，需要精确控制删除哪些节点
- ❌ CAPI 使用声明式设计，Controller 自动选择删除哪些节点
- ❌ 两者设计理念冲突，必须通过 Pause/Resume 协调

**CAPI 标准做法**：
```go
// ✅ 标准 CAPI：永远不需要暂停
// 缩容时，CAPI Controller 自动选择删除哪些节点
// 如果需要精确控制，使用 Machine 的 deletionPriority

KCP.Spec.Replicas = 3
c.Update(ctx, KCP)
// CAPI Controller 自动处理，无需暂停
```

# 扩容不需要 Pause，只有缩容需要！

## 关键发现：扩容不需要 Pause，只有缩容需要！

### 一、代码证据

```bash
# 扩容场景：没有 Pause
ensure_master_join.go   - ❌ 没有调用 PauseClusterAPIObj
ensure_worker_join.go   - ❌ 没有调用 PauseClusterAPIObj

# 缩容场景：需要 Pause
ensure_master_delete.go - ✅ 调用了 PauseClusterAPIObj (line 181)
ensure_worker_delete.go - ✅ 调用了 PauseClusterAPIObj (line 287)
```

### 二、为什么扩容不需要 Pause？

**扩容操作**：
```go
// EnsureMasterJoin.scaleAndJoinMasterNodes()
func scaleAndJoinMasterNodes() error {
    // 1. 获取 KCP
    scope, _ := GetClusterAPIAssociateObjs(ctx, c, cluster)
    
    // 2. 计算 Replicas
    exceptReplicas := *currentReplicas + nodesCount
    
    // 3. 直接更新 Replicas（没有 Pause）
    scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas
    c.Update(ctx, scope.KubeadmControlPlane)
    
    // 4. 等待节点加入
    waitMasterJoin(nodesCount)
}
```

**为什么不需要 Pause？**
- ✅ 扩容只修改 `Replicas`（一个原子操作）
- ✅ KCP Controller 检测到 `Replicas` 增加，创建新 Machine
- ✅ 没有竞争：Phase 不关心 KCP Controller 创建哪些 Machine
- ✅ 新 Machine 会自动关联到 BKENode（通过 BKEMachine Controller）

### 三、为什么缩容需要 Pause？

**缩容操作**：
```go
// EnsureMasterDelete.pauseAndScaleDownControlPlane()
func pauseAndScaleDownControlPlane() error {
    // 1. 暂停 KCP Controller
    PauseClusterAPIObj(ctx, c, KCP)  // ✅ 需要 Pause
    
    // 2. 标记删除特定 Machine
    MarkMachineForDeletion(Machine-3)
    MarkMachineForDeletion(Machine-4)
    
    // 3. 更新 Replicas
    KCP.Spec.Replicas = 3
    c.Update(ctx, KCP)
    
    // 4. 恢复 KCP Controller
    ResumeClusterAPIObj(ctx, c, KCP)
}
```
**为什么需要 Pause？**
- ❌ 缩容需要两步操作：
  1. 标记删除特定 Machine（非原子）
  2. 更新 Replicas（原子）
- ❌ 如果不暂停，KCP Controller 可能在步骤 1 和 2 之间介入
- ❌ KCP Controller 可能删除错误的 Machine（没有标记的）

### 四、总结

| 操作 | 是否需要 Pause | 原因 |
|------|---------------|------|
| **扩容** | ❌ 不需要 | 只修改 Replicas（原子操作），无竞争 |
| **缩容** | ✅ 需要 | 需要标记删除 + 修改 Replicas（非原子操作），有竞争 |

**您的观察完全正确**：扩容只修改 Replicas，确实没有冲突，所以不需要 Pause！


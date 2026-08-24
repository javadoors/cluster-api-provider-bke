# KEP-6 状态机设计 v5：基于 CAPI BKEMachine 的三层状态机引擎

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-6 |
| **标题** | 基于 CAPI BKEMachine 的三层状态机引擎设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-24 |
| **依赖** | KEP-5 声明式升级框架、Cluster API v1beta1 |

---

## 1. 设计目标与约束

### 1.1 设计目标

1. **统一执行入口**：BKECluster Reconcile 触发集群层状态机引擎执行
2. **DAG 驱动执行**：集群层状态机从 ReleaseImage 构建执行 DAG
3. **节点级并行**：节点级组件在 DAG 中聚合为节点组节点，多节点并行执行
4. **三层状态清晰**：集群层、节点层、组件层状态定义清晰，职责分明
5. **可观测性**：状态转换、执行进度、健康状态全链路可观测
6. **CAPI 深度集成**：节点级状态机迁移到 BKEMachine Controller 执行

### 1.2 设计约束

| 约束 | 说明 |
|------|------|
| **节点级组件相邻** | 在 DAG 中，节点级组件聚合为一个"节点组"节点，保持 DAG 拓扑简洁 |
| **节点级并行** | 节点组节点执行时，所有节点并行执行各自的组件状态机 |
| **BKEMachine 驱动** | 节点级和组件层状态机由 BKEMachine Controller 驱动执行 |
| **幂等性** | 所有状态转换和操作必须幂等，支持 Reconcile 重入 |
| **CAPI 兼容** | 完全遵循 Cluster API 的 Controller 模式 |

### 1.3 与 v4 的核心区别

| 维度 | v4 设计 | v5 设计 |
|------|--------|--------|
| **节点级状态机执行位置** | BKECluster Controller | BKEMachine Controller |
| **节点组节点职责** | 直接执行节点级状态机 | 创建/更新 BKEMachine 资源 |
| **节点状态追踪** | BKECluster.Status.NodeStatuses | BKEMachine.Status |
| **组件状态追踪** | BKECluster.Status.ComponentStatuses | BKEMachine.Status.ComponentStatuses |
| **CAPI 集成深度** | 浅层集成 | 深度集成，完全遵循 CAPI 模式 |

---

## 2. 整体架构

### 2.1 架构总览

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              CAPI 集成架构                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Cluster API Core Controllers                                           │   │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐      │   │
│  │  │ Cluster          │  │ Machine          │  │ MachineDeployment│      │   │
│  │  │ Controller       │  │ Controller       │  │ Controller       │      │   │
│  │  └────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘      │   │
│  └───────────┼──────────────────────┼──────────────────────┼───────────────┘   │
│              │                      │                      │                   │
│              │ Watch                │ Watch                │ Watch             │
│              ▼                      ▼                      ▼                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  BKE Infrastructure Provider                                            │   │
│  │                                                                          │   │
│  │  ┌──────────────────────────────────┐  ┌──────────────────────────────┐  │   │
│  │  │ BKECluster Controller            │  │ BKEMachine Controller        │  │   │
│  │  │ ─────────────────────────────    │  │ ──────────────────────────   │  │   │
│  │  │                                  │  │                              │  │   │
│  │  │ 职责:                            │  │ 职责:                        │  │   │
│  │  │ • L1 集群层状态机                │  │ • L2 节点层状态机              │  │   │
│  │  │ • DAG 构建与执行                 │  │ • L3 组件层状态机              │  │   │
│  │  │ • 集群级组件执行                 │  │ • 节点级组件执行               │  │   │
│  │  │ • 创建/更新 BKEMachine           │  │ • 组件状态追踪               │  │   │
│  │  │                                  │  │                              │  │   │
│  │  │ 输入: BKECluster                 │  │ 输入: BKEMachine             │  │   │
│  │  │ 输出: BKECluster.Status          │  │ 输出: BKEMachine.Status      │  │   │
│  │  │       + BKEMachine 创建          │  │       + 组件执行结果         │  │   │
│  │  └──────────────────────────────────┘  └──────────────────────────────┘  │   │
│  │                    │                                │                     │   │
│  │                    │ Watch BKEMachine               │                     │   │
│  │                    └────────────────────────────────┘                     │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 DAG 结构设计

**核心设计**：DAG 中的 node-group 节点只负责创建/更新 BKEMachine，实际执行由 BKEMachine Controller 驱动。

```
ReleaseImage 组件列表:
  - certs (scope=cluster)
  - bkeagent (scope=node)
  - containerd (scope=node)
  - kubelet (scope=node)
  - coredns (scope=cluster)
  - kube-proxy (scope=cluster)

构建后的 DAG:

  ┌────────┐    ┌────────────────────────────────────┐    ┌─────────┐    ┌────────────┐
  │ certs  │───>│         node-group                 │───>│ coredns │───>│ kube-proxy │
  │(cluster)│    │  (创建/更新 BKEMachine)            │    │(cluster)│    │ (cluster)  │
  └────────┘    └────────────────────────────────────┘    └─────────┘    └────────────┘
                              │
                              │ 创建/更新
                              ▼
                ┌─────────────────────────────────┐
                │  BKEMachine Controller          │
                │  ─────────────────────────      │
                │  驱动 L2 节点层状态机             │
                │  驱动 L3 组件层状态机             │
                │                                  │
                │  节点内组件执行顺序:              │
                │  bkeagent → containerd → kubelet │
                └─────────────────────────────────┘
```

### 2.3 三层状态机执行位置

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          三层状态机执行位置                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  BKECluster Controller                                                  │   │
│  │  ─────────────────────                                                  │   │
│  │  执行: L1 集群层状态机                                                   │   │
│  │  状态: Pending → Installing → Running → Upgrading → Scaling → Failed    │   │
│  │                                                                          │   │
│  │  DAG 执行:                                                               │   │
│  │  ┌──────────┐   ┌──────────────┐   ┌──────────┐   ┌──────────┐        │   │
│  │  │  certs   │──>│  node-group  │──>│  coredns │──>│kube-proxy│        │   │
│  │  │(L3直接执行)│  │(创建BKEMachine)│  │(L3直接执行)│  │(L3直接执行)│        │   │
│  │  └──────────┘   └──────────────┘   └──────────┘   └──────────┘        │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  BKEMachine Controller (每个节点一个 Reconcile)                          │   │
│  │  ─────────────────────────────────────────                              │   │
│  │  执行: L2 节点层状态机 + L3 组件层状态机                                 │   │
│  │                                                                          │   │
│  │  L2 节点层状态:                                                          │   │
│  │  Pending → Provisioning → Ready → Upgrading → Deleting                  │   │
│  │                                                                          │   │
│  │  L3 组件层状态 (每个组件独立状态机):                                      │   │
│  │  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐            │   │
│  │  │ bkeagent │──>│containerd│──>│ kubelet  │──>│ kubectl  │            │   │
│  │  │ Pending  │   │ Pending  │   │ Pending  │   │ Pending  │            │   │
│  │  │    ↓     │   │    ↓     │   │    ↓     │   │    ↓     │            │   │
│  │  │Installing│   │Installing│   │Installing│   │Installing│            │   │
│  │  │    ↓     │   │    ↓     │   │    ↓     │   │    ↓     │            │   │
│  │  │Installed │   │Installed │   │Installed │   │Installed │            │   │
│  │  └──────────┘   └──────────┘   └──────────┘   └──────────┘            │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. 三层状态定义

### 3.1 L1 集群层状态（BKECluster Controller 驱动）

```go
// ClusterLifecyclePhase 集群生命周期阶段
type ClusterLifecyclePhase string

const (
    ClusterPhasePending     ClusterLifecyclePhase = "Pending"      // 等待操作
    ClusterPhaseInstalling  ClusterLifecyclePhase = "Installing"   // 安装中
    ClusterPhaseRunning     ClusterLifecyclePhase = "Running"      // 运行中
    ClusterPhaseUpgrading   ClusterLifecyclePhase = "Upgrading"    // 升级中
    ClusterPhaseScaling     ClusterLifecyclePhase = "Scaling"      // 扩缩容中
    ClusterPhaseRollingBack ClusterLifecyclePhase = "RollingBack"  // 回滚中
    ClusterPhaseFailed      ClusterLifecyclePhase = "Failed"       // 失败
)
```

**状态转换图**：

```
                    ┌──────────────────────────────────────┐
                    │                                      │
                    ▼                                      │
 [*] ──> Pending ──> Installing ──> Running ──> Upgrading ─┘
                     │                │           │
                     │                │           ├──> RollingBack ──> Running
                     │                │           │
                     │                ├──> Scaling ──> Running
                     │                │
                     ▼                ▼
                   Failed <───────────┘
                     │
                     └──> (人工介入) ──> 重新进入对应状态
```

### 3.2 L2 节点层状态（BKEMachine Controller 驱动）

```go
// NodeLifecyclePhase 节点生命周期阶段
type NodeLifecyclePhase string

const (
    NodePhasePending      NodeLifecyclePhase = "Pending"       // 等待操作
    NodePhaseProvisioning NodeLifecyclePhase = "Provisioning"  // 环境准备中
    NodePhaseReady        NodeLifecyclePhase = "Ready"         // 节点就绪
    NodePhaseUpgrading    NodeLifecyclePhase = "Upgrading"     // 升级中
    NodePhaseDeleting     NodeLifecyclePhase = "Deleting"      // 删除中
    NodePhaseFailed       NodeLifecyclePhase = "Failed"        // 失败
)
```

**状态转换图**：

```
 [*] ──> Pending ──> Provisioning ──> Ready ──> Upgrading ──> Ready
                       │                  │          │
                       │                  │          └──> Deleting ──> [*]
                       │                  │
                       ▼                  ▼
                     Failed <─────────────┘
                       │
                       └──> (人工介入) ──> 重新进入对应状态
```

### 3.3 L3 组件层状态（BKEMachine Controller 驱动）

```go
// ComponentLifecyclePhase 组件生命周期阶段
type ComponentLifecyclePhase string

const (
    CompPhasePending      ComponentLifecyclePhase = "Pending"       // 等待执行
    CompPhaseInstalling   ComponentLifecyclePhase = "Installing"    // 安装中
    CompPhaseInstalled    ComponentLifecyclePhase = "Installed"     // 已安装
    CompPhaseUpgrading    ComponentLifecyclePhase = "Upgrading"     // 升级中
    CompPhaseDeleting     ComponentLifecyclePhase = "Deleting"      // 卸载中
    CompPhaseFailed       ComponentLifecyclePhase = "Failed"        // 失败
)
```

**状态转换图**：

```
 [*] ──> Pending ──> Installing ──> Installed ──> Upgrading ──> Installed
                       │                │              │
                       │                │              └──> Deleting ──> [*]
                       │                │
                       ▼                ▼
                     Failed <───────────┘
```

### 3.4 三层状态关系

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        三层状态关系                                       │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  BKECluster.Status (L1 集群层)                                          │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ LifecyclePhase = Installing                                     │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────┐     │   │
│  │  │ BKEMachine 列表 (通过 Label 关联)                        │     │   │
│  │  │                                                           │     │   │
│  │  │ BKEMachine-1 (node-1):                                   │     │   │
│  │  │   Status.LifecyclePhase = Provisioning (L2)              │     │   │
│  │  │   Status.ComponentStatuses: (L3)                         │     │   │
│  │  │     bkeagent:   Installing                               │     │   │
│  │  │     containerd: Pending                                  │     │   │
│  │  │     kubelet:    Pending                                  │     │   │
│  │  │                                                           │     │   │
│  │  │ BKEMachine-2 (node-2):                                   │     │   │
│  │  │   Status.LifecyclePhase = Ready (L2)                     │     │   │
│  │  │   Status.ComponentStatuses: (L3)                         │     │   │
│  │  │     bkeagent:   Installed                                │     │   │
│  │  │     containerd: Installed                                │     │   │
│  │  │     kubelet:    Installed                                │     │   │
│  │  └─────────────────────────────────────────────────────────┘     │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  状态聚合规则:                                                           │
│  - ClusterPhase 由 BKECluster Controller 的 DAG 执行状态决定            │
│  - NodePhase 由 BKEMachine Controller 驱动                              │
│  - ComponentPhase 由 BKEMachine Controller 驱动                         │
│  - BKECluster Controller 通过 Watch BKEMachine 感知节点状态变化          │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 4. 核心数据结构

### 4.1 BKEMachine Status 扩展

```go
// BKEMachineStatus 扩展节点状态
type BKEMachineStatus struct {
    // 节点生命周期阶段 (L2)
    LifecyclePhase NodeLifecyclePhase `json:"lifecyclePhase,omitempty"`
    
    // 组件状态列表 (L3)
    ComponentStatuses map[string]ComponentLifecycleStatus `json:"componentStatuses,omitempty"`
    
    // 操作进度
    OperationProgress *NodeOperationProgress `json:"operationProgress,omitempty"`
    
    // CAPI 标准字段
    Ready bool `json:"ready"`
    
    // Conditions
    Conditions []clusterv1.Condition `json:"conditions,omitempty"`
}

// ComponentLifecycleStatus 组件生命周期状态
type ComponentLifecycleStatus struct {
    // 组件名称
    Name string `json:"name"`
    
    // 组件版本
    Version string `json:"version"`
    
    // 组件状态
    Phase ComponentLifecyclePhase `json:"phase"`
    
    // 操作进度
    OperationProgress *ComponentOperationProgress `json:"operationProgress,omitempty"`
    
    // 错误信息
    Message string `json:"message,omitempty"`
    
    // 最后更新时间
    LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
}

// NodeOperationProgress 节点操作进度
type NodeOperationProgress struct {
    // 操作类型
    OperationType NodeOperationType `json:"operationType"`
    
    // 开始时间
    StartedAt *metav1.Time `json:"startedAt,omitempty"`
    
    // 完成时间
    FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
    
    // 当前阶段
    CurrentStage string `json:"currentStage,omitempty"`
    
    // 总组件数
    TotalComponents int `json:"totalComponents,omitempty"`
    
    // 已完成组件数
    CompletedComponents int `json:"completedComponents,omitempty"`
    
    // 失败组件列表
    FailedComponents []string `json:"failedComponents,omitempty"`
}
```

### 4.2 BKECluster Controller

```go
// BKEClusterReconciler 集群层状态机
type BKEClusterReconciler struct {
    client.Client
    Scheme   *runtime.Scheme
    Recorder record.EventRecorder
    
    // 集群层状态机
    clusterSM *ClusterStateMachine
    
    // DAG 执行器
    dagExecutor *DAGExecutor
}

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)
    
    // 1. 获取 BKECluster
    cluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. 创建 PatchHelper
    patchHelper, err := patch.NewHelper(cluster, r.Client)
    if err != nil {
        return ctrl.Result{}, err
    }
    defer func() {
        if err := patchHelper.Patch(ctx, cluster); err != nil {
            log.Error(err, "failed to patch cluster")
        }
    }()
    
    // 3. 执行集群层状态机
    if err := r.clusterSM.Execute(ctx, cluster); err != nil {
        log.Error(err, "cluster state machine execution failed")
        r.Recorder.Eventf(cluster, v1.EventTypeWarning, "ClusterStateMachineFailed",
            "Cluster state machine failed: %v", err)
    }
    
    // 4. 同步旧字段（兼容性）
    syncLegacyFields(cluster)
    
    // 5. 记录指标
    r.recordMetrics(cluster)
    
    // 6. 决定 Requeue
    return r.decideRequeue(cluster), nil
}
```

### 4.3 BKEMachine Controller

```go
// BKEMachineReconciler 节点层 + 组件层状态机
type BKEMachineReconciler struct {
    client.Client
    Scheme   *runtime.Scheme
    Recorder record.EventRecorder
    
    // 节点层状态机
    nodeSM *NodeStateMachine
    
    // 组件层状态机
    componentSM *ComponentStateMachine
}

func (r *BKEMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)
    
    // 1. 获取 BKEMachine
    machine := &bkev1beta1.BKEMachine{}
    if err := r.Get(ctx, req.NamespacedName, machine); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. 获取关联的 BKECluster
    cluster, err := getClusterForMachine(ctx, r.Client, machine)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. 创建 PatchHelper
    patchHelper, err := patch.NewHelper(machine, r.Client)
    if err != nil {
        return ctrl.Result{}, err
    }
    defer func() {
        if err := patchHelper.Patch(ctx, machine); err != nil {
            log.Error(err, "failed to patch machine")
        }
    }()
    
    // 4. 执行节点层状态机
    if err := r.nodeSM.Execute(ctx, machine, cluster); err != nil {
        log.Error(err, "node state machine execution failed")
        r.Recorder.Eventf(machine, v1.EventTypeWarning, "NodeStateMachineFailed",
            "Node state machine failed: %v", err)
    }
    
    // 5. 设置 CAPI 标准条件
    setCAPIConditions(machine)
    
    // 6. 决定 Requeue
    return r.decideRequeue(machine), nil
}
```

### 4.4 DAG 执行器

```go
// DAGExecutor DAG 执行器
type DAGExecutor struct {
    client   client.Client
    recorder record.EventRecorder
}

// BuildDAG 从 ReleaseImage 构建 DAG
func (e *DAGExecutor) BuildDAG(releaseImage *ReleaseImage) (*ExecutionDAG, error) {
    dag := NewExecutionDAG()
    
    // 分离集群级组件和节点级组件
    var clusterComponents []*ComponentVersion
    var hasNodeComponents bool
    
    for _, comp := range releaseImage.Spec.Components {
        cv := lookupComponentVersion(comp.Name, comp.Version)
        if cv.Spec.Scope == ComponentScopeNode {
            hasNodeComponents = true
        } else {
            dag.AddNode(&ClusterComponentNode{
                name:      cv.Name,
                component: cv,
                deps:      cv.Spec.Dependencies,
            })
        }
    }
    
    // 添加节点组节点（只负责创建/更新 BKEMachine）
    if hasNodeComponents {
        dag.AddNode(&NodeGroupNode{
            name: "node-group",
            deps: collectNodeGroupDeps(releaseImage),
        })
    }
    
    // 构建依赖边
    dag.BuildEdges()
    
    return dag, nil
}

// NodeGroupNode 节点组节点
type NodeGroupNode struct {
    name string
    deps []string
}

// Execute 创建/更新 BKEMachine
func (n *NodeGroupNode) Execute(ctx context.Context, execCtx *ExecutionContext) error {
    nodes := execCtx.GetAllNodes()
    
    for _, node := range nodes {
        // 创建或更新 BKEMachine
        machine := &bkev1beta1.BKEMachine{
            ObjectMeta: metav1.ObjectMeta{
                Name:      node.Name,
                Namespace: execCtx.Cluster.Namespace,
                Labels: map[string]string{
                    "cluster.x-k8s.io/cluster-name": execCtx.Cluster.Name,
                },
            },
            Spec: bkev1beta1.BKEMachineSpec{
                // 设置节点级组件列表
                NodeComponents: execCtx.GetNodeComponents(),
            },
        }
        
        // 创建或更新
        existing := &bkev1beta1.BKEMachine{}
        err := execCtx.Client.Get(ctx, client.ObjectKeyFromObject(machine), existing)
        if err != nil {
            if apierrors.IsNotFound(err) {
                // 创建
                if err := execCtx.Client.Create(ctx, machine); err != nil {
                    return err
                }
            } else {
                return err
            }
        } else {
            // 更新
            existing.Spec = machine.Spec
            if err := execCtx.Client.Update(ctx, existing); err != nil {
                return err
            }
        }
    }
    
    return nil
}

// ClusterComponentNode 集群级组件节点
type ClusterComponentNode struct {
    name      string
    component *ComponentVersion
    deps      []string
}

// Execute 直接执行组件级状态机
func (n *ClusterComponentNode) Execute(ctx context.Context, execCtx *ExecutionContext) error {
    comp := execCtx.GetClusterComponentStatus(n.name)
    return execCtx.ComponentSM.Execute(ctx, comp, n.component)
}
```

---

## 5. 完整执行流程

### 5.1 安装流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          集群安装完整流程 (v5)                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  BKEClusterReconciler.Reconcile #1                                              │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Pending → Installing                           │   │
│  │    Condition: desiredVersion != "" && currentVersion == ""               │   │
│  │                                                                          │   │
│  │ 2. BuildDAG(releaseImage)                                                │   │
│  │    DAG: [certs] → [node-group] → [coredns] → [kube-proxy]               │   │
│  │                                                                          │   │
│  │ 3. ExecuteDAG(ctx, dag)                                                  │   │
│  │    ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │    │ Batch 1: [certs]                                                │   │   │
│  │    │   └─ ClusterComponentNode.Execute()                             │   │   │
│  │    │       └─ L3: certs Installing → Installed                       │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 2: [node-group]                                           │   │   │
│  │    │   └─ NodeGroupNode.Execute()                                    │   │   │
│  │    │       └─ 创建 BKEMachine-1, BKEMachine-2, ...                   │   │   │
│  │    │       └─ 设置 BKEMachine.Spec.NodeComponents                    │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 3: [coredns]                                              │   │   │
│  │    │   └─ ClusterComponentNode.Execute()                             │   │   │
│  │    │       └─ L3: coredns Installing → Installed                     │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ Batch 4: [kube-proxy]                                           │   │   │
│  │    │   └─ ClusterComponentNode.Execute()                             │   │   │
│  │    │       └─ L3: kube-proxy Installing → Installed                  │   │   │
│  │    └─────────────────────────────────────────────────────────────────┘   │   │
│  │                                                                          │   │
│  │ 4. DAG 执行完成                                                          │   │
│  │    等待 BKEMachine Controller 处理节点级组件                              │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  BKEMachineReconciler.Reconcile (每个节点独立 Reconcile)                         │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L2: NodeStateMachine.Execute(machine)                                   │   │
│  │                                                                          │   │
│  │ 1. EvaluateNodePhase: Pending → Provisioning                            │   │
│  │                                                                          │   │
│  │ 2. 按依赖顺序执行节点级组件:                                              │   │
│  │    ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │    │ bkeagent:                                                        │   │   │
│  │    │   L3: Pending → Installing → Installed                          │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ containerd:                                                      │   │   │
│  │    │   L3: Pending → Installing → Installed                          │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ kubelet:                                                         │   │   │
│  │    │   L3: Pending → Installing → Installed                          │   │   │
│  │    └─────────────────────────────────────────────────────────────────┘   │   │
│  │                                                                          │   │
│  │ 3. EvaluateNodePhase: Provisioning → Ready                              │   │
│  │    Condition: 所有节点级组件 Installed                                   │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  BKEClusterReconciler.Reconcile #N (Watch BKEMachine 触发)                       │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. 检查所有 BKEMachine 状态                                              │   │
│  │    └─ 所有 BKEMachine.Status.LifecyclePhase == Ready                     │   │
│  │                                                                          │   │
│  │ 2. EvaluateClusterPhase: Installing → Running                           │   │
│  │    Condition: 所有节点 Ready                                             │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 升级流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          集群升级完整流程 (v5)                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  用户: kubectl patch bkecluster my-cluster --type merge                         │
│        -p '{"spec":{"desiredVersion":"v2.7.0"}}'                                │
│                                                                                 │
│  BKEClusterReconciler.Reconcile #1                                              │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. EvaluateClusterPhase: Running → Upgrading                            │   │
│  │    Condition: desiredVersion != currentVersion                           │   │
│  │                                                                          │   │
│  │ 2. BuildDAG(newReleaseImage)                                             │   │
│  │    DAG: [certs] → [node-group] → [coredns] → [kube-proxy]               │   │
│  │                                                                          │   │
│  │ 3. ExecuteDAG(ctx, dag)                                                  │   │
│  │    └─ Batch 2: [node-group]                                              │   │
│  │       └─ NodeGroupNode.Execute()                                         │   │
│  │           └─ 更新 BKEMachine.Spec.NodeComponents (新版本)                │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  BKEMachineReconciler.Reconcile (每个节点独立 Reconcile)                         │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L2: NodeStateMachine.Execute(machine)                                   │   │
│  │                                                                          │   │
│  │ 1. EvaluateNodePhase: Ready → Upgrading                                 │   │
│  │    Condition: Spec.NodeComponents 版本变更                               │   │
│  │                                                                          │   │
│  │ 2. 按依赖顺序升级节点级组件:                                              │   │
│  │    ┌─────────────────────────────────────────────────────────────────┐   │   │
│  │    │ containerd:                                                      │   │   │
│  │    │   L3: Installed → Upgrading → Installed (新版本)                 │   │   │
│  │    ├─────────────────────────────────────────────────────────────────┤   │   │
│  │    │ kubelet:                                                         │   │   │
│  │    │   L3: Installed → Upgrading → Installed (新版本)                 │   │   │
│  │    └─────────────────────────────────────────────────────────────────┘   │   │
│  │                                                                          │   │
│  │ 3. EvaluateNodePhase: Upgrading → Ready                                 │   │
│  │    Condition: 所有节点级组件升级完成                                      │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  BKEClusterReconciler.Reconcile #N (Watch BKEMachine 触发)                       │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ L1: ClusterStateMachine.Execute(cluster)                                │   │
│  │                                                                          │   │
│  │ 1. 检查所有 BKEMachine 状态                                              │   │
│  │    └─ 所有 BKEMachine.Status.LifecyclePhase == Ready                     │   │
│  │                                                                          │   │
│  │ 2. EvaluateClusterPhase: Upgrading → Running                            │   │
│  │    Condition: 所有节点 Ready                                             │   │
│  │                                                                          │   │
│  │ 3. 更新 currentVersion = "v2.7.0"                                        │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 6. 可观测性设计

### 6.1 可观测性架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          可观测性三层架构                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Layer 1: 状态可观测                                                    │   │
│  │  ─────────────────────                                                  │   │
│  │  • BKECluster.Status.LifecyclePhase         (集群层状态)                │   │
│  │  • BKEMachine.Status.LifecyclePhase         (节点层状态)                │   │
│  │  • BKEMachine.Status.ComponentStatuses      (组件层状态)                │   │
│  │  • BKEMachine.Status.OperationProgress      (节点操作进度)              │   │
│  │  • Conditions                               (状态条件)                  │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Layer 2: 事件可观测                                                    │   │
│  │  ─────────────────────                                                  │   │
│  │  • StateTransition events                   (状态转换事件)              │   │
│  │  • OperationStarted/Completed/Failed events (操作事件)                  │   │
│  │  • ComponentInstalled/Upgraded/Failed       (组件事件)                  │   │
│  │  • BKEMachineCreated/Updated                (BKEMachine 事件)          │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Layer 3: 指标可观测                                                    │   │
│  │  ─────────────────────                                                  │   │
│  │  • bke_cluster_phase_gauge                  (集群状态)                  │   │
│  │  • bke_node_phase_gauge                     (节点状态)                  │   │
│  │  • bke_component_phase_gauge                (组件状态)                  │   │
│  │  • bke_node_ready_count                     (就绪节点数)                │   │
│  │  • bke_component_install_total              (组件安装计数)              │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 6.2 状态查询 API

```bash
# 查询集群状态
kubectl get bkecluster my-cluster -o jsonpath='{.status}'

# 查询节点状态
kubectl get bkemachine -l cluster.x-k8s.io/cluster-name=my-cluster -o wide

# 查询节点组件状态
kubectl get bkemachine node-1 -o jsonpath='{.status.componentStatuses}'

# 查询节点操作进度
kubectl get bkemachine node-1 -o jsonpath='{.status.operationProgress}'

# 查询状态转换历史（通过 Events）
kubectl get events --field-selector involvedObject.name=node-1,reason=StateTransition
```

### 6.3 Prometheus 指标

```go
var (
    // 集群状态指标
    clusterPhaseGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_cluster_phase",
            Help: "Current cluster phase (0=Pending, 1=Installing, 2=Running, 3=Upgrading, 4=Scaling, 5=RollingBack, 6=Failed)",
        },
        []string{"cluster"},
    )
    
    // 节点状态指标
    nodePhaseGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_node_phase",
            Help: "Current node phase (0=Pending, 1=Provisioning, 2=Ready, 3=Upgrading, 4=Deleting, 5=Failed)",
        },
        []string{"cluster", "node"},
    )
    
    // 组件状态指标
    componentPhaseGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_component_phase",
            Help: "Current component phase (0=Pending, 1=Installing, 2=Installed, 3=Upgrading, 4=Deleting, 5=Failed)",
        },
        []string{"cluster", "node", "component"},
    )
    
    // 节点就绪数
    nodeReadyCount = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_node_ready_count",
            Help: "Number of ready nodes",
        },
        []string{"cluster"},
    )
)
```

---

## 7. CAPI 集成设计

### 7.1 与 CAPI 的集成架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        Cluster API 集成架构                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Cluster API Core Controllers                                           │   │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐      │   │
│  │  │ Cluster          │  │ Machine          │  │ MachineDeployment│      │   │
│  │  │ Controller       │  │ Controller       │  │ Controller       │      │   │
│  │  └────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘      │   │
│  └───────────┼──────────────────────┼──────────────────────┼───────────────┘   │
│              │                      │                      │                   │
│              │ Watch                │ Watch                │ Watch             │
│              ▼                      ▼                      ▼                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  BKE Infrastructure Provider                                            │   │
│  │                                                                          │   │
│  │  ┌──────────────────────────────────┐  ┌──────────────────────────────┐  │   │
│  │  │ BKECluster Controller            │  │ BKEMachine Controller        │  │   │
│  │  │ ─────────────────────────────    │  │ ──────────────────────────   │  │   │
│  │  │                                  │  │                              │  │   │
│  │  │ 职责:                            │  │ 职责:                        │  │   │
│  │  │ • L1 集群层状态机                │  │ • L2 节点层状态机              │  │   │
│  │  │ • DAG 构建与执行                 │  │ • L3 组件层状态机              │  │   │
│  │  │ • 集群级组件执行                 │  │ • 节点级组件执行               │  │   │
│  │  │ • 创建/更新 BKEMachine           │  │ • 组件状态追踪               │  │   │
│  │  │                                  │  │                              │  │   │
│  │  │ 输入: BKECluster                 │  │ 输入: BKEMachine             │  │   │
│  │  │ 输出: BKECluster.Status          │  │ 输出: BKEMachine.Status      │  │   │
│  │  │       + BKEMachine 创建          │  │       + 组件执行结果         │  │   │
│  │  └──────────────────────────────────┘  └──────────────────────────────┘  │   │
│  │                    │                                │                     │   │
│  │                    │ Watch BKEMachine               │                     │   │
│  │                    └────────────────────────────────┘                     │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.2 CAPI 条件集成

```go
// BKECluster 设置 CAPI 标准条件
func setBKEClusterCAPIConditions(cluster *bkev1beta1.BKECluster) {
    switch cluster.Status.LifecyclePhase {
    case ClusterPhaseRunning:
        conditions.MarkTrue(cluster, clusterv1.ReadyCondition)
        
    case ClusterPhaseInstalling, ClusterPhaseUpgrading, ClusterPhaseScaling:
        conditions.MarkFalse(cluster, clusterv1.ReadyCondition,
            "OperationInProgress", clusterv1.ConditionSeverityInfo,
            "Operation %s in progress", cluster.Status.LifecyclePhase)
        
    case ClusterPhaseFailed:
        conditions.MarkFalse(cluster, clusterv1.ReadyCondition,
            "OperationFailed", clusterv1.ConditionSeverityError,
            "Operation failed, manual intervention required")
    }
}

// BKEMachine 设置 CAPI 标准条件
func setBKEMachineCAPIConditions(machine *bkev1beta1.BKEMachine) {
    switch machine.Status.LifecyclePhase {
    case NodePhaseReady:
        conditions.MarkTrue(machine, clusterv1.ReadyCondition)
        
    case NodePhaseProvisioning, NodePhaseUpgrading:
        conditions.MarkFalse(machine, clusterv1.ReadyCondition,
            "OperationInProgress", clusterv1.ConditionSeverityInfo,
            "Operation %s in progress", machine.Status.LifecyclePhase)
        
    case NodePhaseFailed:
        conditions.MarkFalse(machine, clusterv1.ReadyCondition,
            "OperationFailed", clusterv1.ConditionSeverityError,
            "Operation failed: %s", machine.Status.OperationProgress.Message)
    }
}
```

---

## 8. 总结

### 8.1 设计优势

| 优势 | 说明 |
|------|------|
| **CAPI 深度集成** | 完全遵循 CAPI 的 Controller 模式，BKEMachine Controller 驱动节点级状态机 |
| **职责分离** | BKECluster Controller 负责集群层，BKEMachine Controller 负责节点层和组件层 |
| **并行执行** | 每个节点独立 Reconcile，天然支持并行 |
| **状态追踪** | 节点和组件状态存储在 BKEMachine.Status 中，符合 CAPI 规范 |
| **可观测** | 状态、事件、指标三层可观测，全链路可追踪 |

### 8.2 与 v4 的主要区别

| 维度 | v4 设计 | v5 设计 |
|------|--------|--------|
| **节点级状态机执行位置** | BKECluster Controller | BKEMachine Controller |
| **节点组节点职责** | 直接执行节点级状态机 | 创建/更新 BKEMachine 资源 |
| **节点状态追踪** | BKECluster.Status.NodeStatuses | BKEMachine.Status |
| **组件状态追踪** | BKECluster.Status.ComponentStatuses | BKEMachine.Status.ComponentStatuses |
| **CAPI 集成深度** | 浅层集成 | 深度集成，完全遵循 CAPI 模式 |
| **并行执行方式** | 在 BKECluster Controller 中并行 | 每个节点独立 Reconcile |

---

**文档版本**: v5.0  
**维护者**: openFuyao Team

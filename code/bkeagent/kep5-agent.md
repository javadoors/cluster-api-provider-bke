# 声明式架构与 CAPI 协同

## 声明式架构与 Cluster API 协同设计

### 一、当前架构与 CAPI 的关系分析
当前 cluster-api-provider-bke 只实现了 **Infrastructure Provider**，同时深度依赖 CAPI 的 **KubeadmControlPlane** 和 **KubeadmBootstrap** 两个标准 Provider：
```
┌──────────────────────────────────────────────────────────────────────────────────┐
│  CAPI 标准架构                                                                   │
│                                                                                  │
│  Cluster ──────────────────────────────────────────────────────────────────────  │
│     │                                                                            │
│     ├── ControlPlaneRef ──► KubeadmControlPlane (CAPI 标准 ControlPlane Provider)│
│     │                       ├── KubeadmConfig (InitConfiguration)                │
│     │                       └── MachineTemplate                                  │
│     │                            └── InfrastructureRef ──► BKEMachineTemplate    │
│     │                                                                            │
│     └── InfrastructureRef ──► BKECluster (自研 Infrastructure Provider)          │
│                                                                                  │
│  MachineDeployment ────────────────────────────────────────────────────────────  │
│     └── MachineTemplate                                                          │
│          ├── Bootstrap.ConfigRef ──► KubeadmConfig (CAPI 标准 Bootstrap Provider)│
│          └── InfrastructureRef ──► BKEMachine (自研 Infrastructure Provider)     │
└──────────────────────────────────────────────────────────────────────────────────┘
```

关键发现：
1. **BKECluster** 实现了 CAPI 的 `Cluster` → `InfrastructureCluster` 接口（通过 OwnerReference 关联），但**缺少** `GetConditions()/SetConditions()` 方法（不符合 CAPI 规范）
2. **BKEMachine** 实现了 `GetConditions()/SetConditions()`，符合 CAPI 的 `Machine` → `InfrastructureMachine` 接口
3. 当前通过 `Command CR` 桥接管理集群和 bkeagent，但 Command CR 不属于 CAPI 规范
4. BKEMachineReconciler 中直接操作 `KubeadmConfig`、`KubeadmControlPlane`，**越权**了 Bootstrap/ControlPlane Provider 的职责

### 二、CAPI 规范对 Provider 的接口要求
CAPI 对每种 Provider 有明确的接口契约：

| Provider 类型 | 必须实现的接口 | 当前 BKE 实现 |
|---|---|---|
| **InfrastructureCluster** | `GetConditions() Conditions`<br>`SetConditions(Conditions)`<br>OwnerRef → Cluster<br>status.ready<br>status.failureDomains | ❌ BKECluster 缺少 Get/SetConditions |
| **InfrastructureMachine** | `GetConditions() Conditions`<br>`SetConditions(Conditions)`<br>OwnerRef → Machine<br>spec.providerID<br>status.ready | ✅ BKEMachine 基本满足 |
| **ControlPlane** | 管理 ControlPlane 生命周期<br>scale / upgrade 控制 | ❌ 使用 KubeadmControlPlane |
| **Bootstrap** | 生成 bootstrap 数据（cloud-init/ignition）<br>status.dataSecretName | ❌ 使用 KubeadmBootstrap |

### 三、声明式架构与 CAPI 协同的整体设计

核心思路：**NodeState CR 是 BKE 的内部实现细节，不暴露给 CAPI；CAPI 标准接口层通过 BKECluster/BKEMachine 桥接**。

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                           CAPI 标准接口层                                               │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐    │
│  │  Cluster (CAPI Core)                                                            │    │
│  │    ├── InfrastructureRef ──► BKECluster (Infrastructure Provider)               │    │
│  │    ├── ControlPlaneRef ──► KubeadmControlPlane (标准 KCP)                       │    │
│  │    └── status.controlPlaneReady / infrastructureReady                           │    │
│  └─────────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐    │
│  │  Machine (CAPI Core)                                                            │    │
│  │    ├── InfrastructureRef ──► BKEMachine (Infrastructure Provider)               │    │
│  │    ├── Bootstrap.ConfigRef ──► KubeadmConfig (标准 Bootstrap)                   │    │
│  │    └── status.bootstrapReady / infrastructureReady                              │    │
│  └─────────────────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────────────────┘

                    │ CAPI Reconcile 触发
                    ▼

┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                     BKE Provider 层 (符合 CAPI 接口)                                    │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  BKEClusterReconciler (Infrastructure Cluster)                                    │  │
│  │                                                                                   │  │
│  │  CAPI 契约:                                                                       │  │
│  │    ✅ 设置 status.ready = true (基础设施就绪)                                     │  │
│  │    ✅ 设置 status.failureDomains (可用区信息)                                     │  │
│  │    ✅ 设置 status.controlPlaneEndpoint (API Server 端点)                          │  │
│  │    ✅ 实现 GetConditions()/SetConditions()                                        │  │
│  │                                                                                   │  │
│  │  BKE 扩展 (超出 CAPI 规范):                                                       │  │
│  │    • PhaseFrame 阶段编排 → 创建 NodeState CR (替代 Command CR)                    │  │
│  │    • 集群生命周期管理 (install/upgrade/scale/reset)                               │  │
│  │    • Addon 部署管理                                                               │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  BKEMachineReconciler (Infrastructure Machine)                                    │  │
│  │                                                                                   │  │
│  │  CAPI 契约:                                                                       │  │
│  │    ✅ 设置 spec.providerID (节点唯一标识)                                         │  │
│  │    ✅ 设置 status.ready = true (节点就绪)                                         │  │
│  │    ✅ 设置 status.addresses (节点地址)                                            │  │
│  │    ✅ 实现 GetConditions()/SetConditions()                                        │  │
│  │                                                                                   │  │
│  │  BKE 扩展:                                                                        │  │
│  │    • 节点分配 (从 BKENode 池中分配)                                               │  │
│  │    • 创建 NodeState CR (声明该节点的期望状态)                                     │  │
│  │    • 监听 NodeState status 反馈节点状态                                           │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────────────┘

                    │ 创建 NodeState CR
                    ▼

┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                     BKE 内部实现层 (NodeState + Capability)                             │
│                                                                                         │
│  NodeState CR ──► bkeagent NodeStateReconciler ──► Capability DAG 执行                  │
│  (声明式期望状态)     (节点侧协调)                    (自主观测→规划→执行)              │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 四、关键协同点详细设计

#### 4.1 BKEClusterReconciler：从 PhaseFrame+Command 到 PhaseFrame+NodeState

当前 [bkecluster_controller.go](file:///D:\code\github\cluster-api-provider-bke\controllers\capbke\bkecluster_controller.go) 的 `executePhaseFlow` 通过 PhaseFrame 创建 Command CR。重构后改为创建 NodeState CR：

```go
// controllers/capbke/bkecluster_controller.go

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bkeCluster, err := r.getAndValidateCluster(ctx, req)
    if err != nil {
        return r.handleClusterError(err)
    }

    // ===== CAPI 标准契约部分 =====
    
    // 1. 处理 Finalizer
    if !bkeCluster.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, bkeCluster)
    }
    
    // 2. 获取 CAPI Cluster
    cluster, err := util.GetOwnerCluster(ctx, r.Client, bkeCluster.ObjectMeta)
    if cluster == nil {
        return ctrl.Result{}, nil
    }
    
    // 3. 设置 CAPI 标准状态
    bkeCluster.Status.Ready = r.isInfrastructureReady(bkeCluster)
    bkeCluster.Status.ControlPlaneEndpoint = bkeCluster.Spec.ControlPlaneEndpoint
    // 设置 failureDomains
    r.setFailureDomains(bkeCluster)
    
    // ===== BKE 扩展部分 =====
    
    // 4. PhaseFrame 编排 → 创建 NodeState CR（替代 Command CR）
    phaseCtx := phaseframe.NewReconcilePhaseCtx(ctx).
        SetClient(r.Client).
        SetBKECluster(bkeCluster)
    flow := phases.NewPhaseFlow(phaseCtx)
    res, err := flow.CalculateAndExecute(bkeCluster)
    
    return res, err
}
```

**关键改动**：PhaseFrame 的各 Phase 不再调用 `command.Bootstrap{}.New()` 或 `command.ENV{}.New()` 创建 Command CR，而是创建 NodeState CR：

```go
// pkg/phaseframe/phases/ensure_env_init.go (重构后)

func (p *EnsureEnvInitPhase) Execute(ctx context.Context) (ctrl.Result, error) {
    nodes := p.getNodesForPhase()
    
    for _, node := range nodes {
        nodeState := &agentv1beta2.NodeState{
            ObjectMeta: metav1.ObjectMeta{
                Name:      fmt.Sprintf("env-init-%s-%d", node.IP, time.Now().Unix()),
                Namespace: p.BKECluster.Namespace,
                Labels: map[string]string{
                    clusterv1.ClusterNameLabel: p.BKECluster.Name,
                    "bke.bocloud.com/phase":    "env-init",
                },
            },
            Spec: agentv1beta2.NodeStateSpec{
                NodeName: node.Hostname,
                DesiredState: agentv1beta2.NodeDesiredState{
                    ContainerRuntime: &agentv1beta2.ContainerRuntimeState{
                        Type:    p.BKECluster.Spec.ClusterConfig.Cluster.ContainerRuntime.CRI,
                        Version: p.BKECluster.Spec.ClusterConfig.Cluster.ContainerRuntime.Version,
                    },
                    Network: &agentv1beta2.NetworkState{
                        IPv4Forwarding: ptr.To(true),
                    },
                    BKEConfigRef: fmt.Sprintf("%s/%s", p.BKECluster.Namespace, p.BKECluster.Name),
                },
                ReconcilePolicy: agentv1beta2.ReconcilePolicy{
                    Mode:           agentv1beta2.ReconcileModeAuto,
                    TimeoutSeconds: 1000,
                },
            },
        }
        
        if err := p.Client.Create(ctx, nodeState); err != nil {
            return ctrl.Result{}, err
        }
    }
    
    return ctrl.Result{}, nil
}
```

#### 4.2 BKEMachineReconciler：从 Command 到 NodeState

当前 [bkemachine_controller_phases.go](file:///D:\code\github\cluster-api-provider-bke\controllers\capbke\bkemachine_controller_phases.go) 的 `handleRealBootstrap` 创建 `command.Bootstrap`。重构后改为创建 NodeState CR：

```go
// controllers/capbke/bkemachine_controller_phases.go (重构后)

func (r *BKEMachineReconciler) handleRealBootstrap(params RealBootstrapParams) (ctrl.Result, error) {
    // ===== CAPI 标准契约部分 =====
    // 不再直接操作 KubeadmConfig / KubeadmControlPlane
    // 这些由 CAPI 标准的 KCP Controller 和 Bootstrap Controller 负责
    
    // ===== BKE 扩展部分 =====
    // 创建 NodeState CR，声明该节点的期望状态
    nodeState := r.buildNodeStateForBootstrap(params)
    
    if err := r.Client.Create(params.Ctx, nodeState); err != nil {
        return ctrl.Result{}, err
    }
    
    // 监听 NodeState status，当 phase=Completed 时标记 BKEMachine ready
    return ctrl.Result{}, nil
}

func (r *BKEMachineReconciler) buildNodeStateForBootstrap(params RealBootstrapParams) *agentv1beta2.NodeState {
    desiredState := agentv1beta2.NodeDesiredState{
        KubernetesVersion: *params.Machine.Spec.Version,
        ContainerRuntime: &agentv1beta2.ContainerRuntimeState{
            Type:    params.BKECluster.Spec.ClusterConfig.Cluster.ContainerRuntime.CRI,
            Version: params.BKECluster.Spec.ClusterConfig.Cluster.ContainerRuntime.Version,
        },
        BKEConfigRef: fmt.Sprintf("%s/%s", params.BKECluster.Namespace, params.BKECluster.Name),
    }
    
    // 根据角色设置 ControlPlane 状态
    if util.IsControlPlaneMachine(params.Machine) {
        desiredState.ControlPlane = &agentv1beta2.ControlPlaneState{
            Role:     string(params.Phase),
            Endpoint: fmt.Sprintf("%s:%d", params.BKECluster.Spec.ControlPlaneEndpoint.Host, params.BKECluster.Spec.ControlPlaneEndpoint.Port),
        }
    }
    
    return &agentv1beta2.NodeState{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("bootstrap-%s-%d", params.Node.IP, time.Now().Unix()),
            Namespace: params.BKEMachine.Namespace,
            Labels: map[string]string{
                clusterv1.ClusterNameLabel: params.Cluster.Name,
                "bke.bocloud.com/machine":  params.BKEMachine.Name,
            },
            OwnerReferences: []metav1.OwnerReference{
                {
                    APIVersion: bkev1beta1.GroupVersion.String(),
                    Kind:       "BKEMachine",
                    Name:       params.BKEMachine.Name,
                    UID:        params.BKEMachine.UID,
                },
            },
        },
        Spec: agentv1beta2.NodeStateSpec{
            NodeName:     params.Node.Hostname,
            DesiredState: desiredState,
            ReconcilePolicy: agentv1beta2.ReconcilePolicy{
                Mode:           agentv1beta2.ReconcileModeAuto,
                TimeoutSeconds: 1000,
            },
        },
    }
}
```

#### 4.3 NodeState Status → CAPI Conditions 映射

NodeState CR 的 status 需要映射回 CAPI 标准的 Conditions，这是协同的关键桥梁：

```go
// controllers/capbke/nodestate_to_capi_mapper.go

// MapNodeStateToBKEMachineStatus 将 NodeState 状态映射到 BKEMachine 的 CAPI Conditions
func MapNodeStateToBKEMachineStatus(
    nodeState *agentv1beta2.NodeState, 
    bkeMachine *bkev1beta1.BKEMachine) {
    
    // 映射 CAPI 标准条件
    for _, cond := range nodeState.Status.Conditions {
        switch cond.Type {
        case "KubernetesVersion":
            capiCond := clusterv1.Condition{
                Type:   clusterv1.MachineNodeHealthyCondition,
                Status: conditionStatusToCAPI(cond.Status),
                Reason: cond.Reason,
            }
            conditions.Set(bkeMachine, capiCond)
            
        case "ContainerRuntime":
            capiCond := clusterv1.Condition{
                Type:   bkev1beta1.RuntimeReadyCondition,
                Status: conditionStatusToCAPI(cond.Status),
            }
            conditions.Set(bkeMachine, capiCond)
        }
    }
    
    // 设置 CAPI 标准字段
    switch nodeState.Status.Phase {
    case agentv1beta2.NodeStatePhaseCompleted:
        bkeMachine.Status.Ready = true
        bkeMachine.Status.Bootstrapped = true
        conditions.MarkTrue(bkeMachine, bkev1beta1.BootstrapSucceededCondition)
        
    case agentv1beta2.NodeStatePhaseFailed:
        bkeMachine.Status.Ready = false
        conditions.MarkFalse(bkeMachine, bkev1beta1.BootstrapSucceededCondition,
            "NodeStateFailed", clusterv1.ConditionSeverityError, "NodeState reconciliation failed")
    }
}

// MapNodeStateToBKEClusterStatus 将多个 NodeState 状态聚合到 BKECluster 的 CAPI Conditions
func MapNodeStateToBKEClusterStatus(
    nodeStates []agentv1beta2.NodeState,
    bkeCluster *bkev1beta1.BKECluster) {
    
    allReady := true
    for _, ns := range nodeStates {
        if ns.Status.Phase != agentv1beta2.NodeStatePhaseCompleted {
            allReady = false
            break
        }
    }
    
    bkeCluster.Status.Ready = allReady
    
    if allReady {
        conditions.MarkTrue(bkeCluster, clusterv1.ReadyCondition)
    } else {
        conditions.MarkFalse(bkeCluster, clusterv1.ReadyCondition,
            "NodeStatesNotReady", clusterv1.ConditionSeverityInfo, "")
    }
}
```

#### 4.4 Watch 链路：NodeState → BKEMachine → Machine → Cluster

CAPI 的 Watch 链路必须保持完整，确保状态变更能正确传播：

```go
// controllers/capbke/bkemachine_controller.go (SetupWithManager)

func (r *BKEMachineReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&bkev1beta1.BKEMachine{}).
        Watches(
            &clusterv1.Machine{},
            handler.EnqueueRequestsFromMapFunc(r.machineToBKEMachine),
        ).
        // 新增: Watch NodeState 变更，触发 BKEMachine Reconcile
        Watches(
            &agentv1beta2.NodeState{},
            handler.EnqueueRequestsFromMapFunc(r.nodeStateToBKEMachine),
            builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
        ).
        Build(r)
}

// nodeStateToBKEMachine 将 NodeState 变更映射到 BKEMachine Reconcile
func (r *BKEMachineReconciler) nodeStateToBKEMachine(ctx context.Context, obj client.Object) []reconcile.Request {
    nodeState, ok := obj.(*agentv1beta2.NodeState)
    if !ok {
        return nil
    }
    
    // 通过 label 找到关联的 BKEMachine
    machineName, ok := nodeState.Labels["bke.bocloud.com/machine"]
    if !ok {
        return nil
    }
    
    return []reconcile.Request{{
        NamespacedName: client.ObjectKey{
            Name:      machineName,
            Namespace: nodeState.Namespace,
        },
    }}
}
```

#### 4.5 BKECluster 补全 CAPI 规范接口

当前 BKECluster 缺少 `GetConditions()/SetConditions()` 方法，需要补全：

```go
// api/capbke/v1beta1/bkecluster_types.go (补全)

// GetConditions returns the set of conditions for this object.
func (c *BKECluster) GetConditions() clusterv1.Conditions {
    return c.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (c *BKECluster) SetConditions(conditions clusterv1.Conditions) {
    c.Status.Conditions = conditions
}
```

### 五、职责边界划分

重构后的清晰职责边界，确保不越权 CAPI 标准 Provider：

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  CAPI 标准 Controller (不修改)                                                          │
│                                                                                         │
│  KubeadmControlPlane Controller:                                                        │
│    • 管理 KCP 生命周期 (scale, upgrade version)                                         │
│    • 创建/删除 ControlPlane Machine                                                     │
│    • 生成 KubeadmConfig (InitConfiguration/JoinConfiguration)                           │
│    • 设置 Cluster.status.controlPlaneReady                                              │
│                                                                                         │
│  KubeadmBootstrap Controller:                                                           │
│    • 生成 bootstrap 数据 (cloud-init)                                                   │
│    • 设置 Machine.status.bootstrapReady                                                 │
│    • 生成 dataSecretName                                                                │
│                                                                                         │
│  CAPI Core Controller:                                                                  │
│    • Machine Controller: 管理 Machine 生命周期                                          │
│    • Cluster Controller: 编排 Provider 之间的交互                                       │
└─────────────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  BKE Infrastructure Provider (重构后)                                                   │
│                                                                                         │
│  BKEClusterReconciler:                                                                  │
│    ✅ CAPI 契约:                                                                        │
│       • status.ready                                                                    │
│       • status.failureDomains                                                           │
│       • status.controlPlaneEndpoint                                                     │
│       • GetConditions()/SetConditions()                                                 │
│    ✅ BKE 扩展:                                                                         │
│       • PhaseFrame 编排 → 创建 NodeState CR                                             │
│       • 集群级别生命周期 (install/upgrade/reset)                                        │
│       • Addon 部署                                                                      │
│    ❌ 不再越权:                                                                         │
│       • 不直接操作 KubeadmControlPlane                                                  │
│       • 不直接修改 KubeadmConfig                                                        │
│                                                                                         │
│  BKEMachineReconciler:                                                                  │
│    ✅ CAPI 契约:                                                                        │
│       • spec.providerID                                                                 │
│       • status.ready                                                                    │
│       • status.addresses                                                                │
│       • GetConditions()/SetConditions()                                                 │
│    ✅ BKE 扩展:                                                                         │
│       • 节点分配 (BKENode 池)                                                           │
│       • 创建 NodeState CR (声明节点期望状态)                                            │
│       • 监听 NodeState status → 映射到 CAPI Conditions                                  │
│    ❌ 不再越权:                                                                         │
│       • 不直接操作 KubeadmConfig (删除 syncKubeadmConfig)                               │
│       • 不直接 patch KubeadmControlPlane                                                │
└─────────────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  bkeagent (节点侧，与 CAPI 无直接交互)                                                  │
│                                                                                         │
│  NodeStateReconciler:                                                                   │
│    • Watch NodeState CR                                                                 │
│    • Observe → Plan → Reconcile (Capability DAG)                                        │
│    • 更新 NodeState status                                                              │
│    • 不感知 CAPI 的存在                                                                 │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 六、升级场景的 CAPI 协同流程

以集群升级为例，展示完整的 CAPI 协同链路：
```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  升级场景: K8s 1.29.0 → 1.30.0                                                          │
│                                                                                         │
│  Step 1: 用户修改 Cluster.spec.topology.version = "1.30.0"                              │
│          (CAPI ClusterClass 模式)                                                       │
│                                                                                         │
│  Step 2: CAPI Core 检测到 version 变更                                                  │
│          → KubeadmControlPlane Controller 滚动升级 ControlPlane Machine                 │
│          → 逐个创建新 Machine / 更新现有 Machine.Spec.Version                           │
│                                                                                         │
│  Step 3: BKEMachineReconciler 被 Machine 变更触发                                       │
│          → 检测到 Machine.Spec.Version 变更                                             │
│          → 创建 NodeState CR:                                                           │
│            spec.desiredState.kubernetesVersion = "1.30.0"                               │
│            spec.desiredState.controlPlane.role = "upgradeControlPlane"                  │
│                                                                                         │
│  Step 4: bkeagent NodeStateReconciler 协调                                              │
│          → Observe: 当前 kubernetesVersion = "1.29.0"                                   │
│          → Plan: Kubernetes Capability 需要升级                                         │
│          → 依赖检查: ContainerRuntime 已满足 → 可执行                                   │
│          → Reconcile: 执行 kubeadm upgrade                                              │
│          → 更新 NodeState.status.phase = Completed                                      │
│                                                                                         │
│  Step 5: NodeState status 变更触发 BKEMachineReconciler                                 │
│          → MapNodeStateToBKEMachineStatus()                                             │
│          → BKEMachine.status.ready = true                                               │
│          → CAPI Conditions 更新                                                         │
│                                                                                         │
│  Step 6: CAPI Core 检测到 BKEMachine ready                                              │
│          → Machine Controller 继续升级下一个 Machine                                    │
│          → 全部完成后 Cluster.status.controlPlaneReady = true                           │
│                                                                                         │
│  Step 7: BKEClusterReconciler 检测到所有 NodeState 完成                                 │
│          → PhaseFrame 进入下一阶段 (如 addon 升级)                                      │
│          → BKECluster.status.kubernetesVersion = "1.30.0"                               │
│          → BKECluster.status.ready = true                                               │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 七、NodeState CR 与 CAPI CR 的关系映射

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  CR 关系映射                                                                            │
│                                                                                         │
│  CAPI 层:                                                                               │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────────┐                         │
│  │   Cluster    │────►│  BKECluster  │     │ KubeadmControl   │                         │
│  │              │     │              │     │     Plane        │                         │
│  │ version:     │     │ status:      │     │ version: 1.30.0  │                         │
│  │  1.30.0      │     │  ready: true │     │ replicas: 3      │                         │
│  └──────┬───────┘     └──────┬───────┘     └────────┬─────────┘                         │
│         │                    │                      │                                   │
│         ▼                    ▼                      ▼                                   │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────────┐                         │
│  │   Machine    │────►│  BKEMachine  │     │  KubeadmConfig   │                         │
│  │              │     │              │     │                  │                         │
│  │ version:     │     │ providerID:  │     │ initConfig:      │                         │
│  │  1.30.0      │     │  ready: true │     │  joinConfig:     │                         │
│  └──────────────┘     └──────┬───────┘     └──────────────────┘                         │
│                              │                                                          │
│                              │ 创建 + Watch                                             │
│                              ▼                                                          │
│  BKE 内部层:                                                                            │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐   │
│  │  NodeState CR                                                                    │   │
│  │                                                                                  │   │
│  │  metadata:                                                                       │   │
│  │    ownerReferences: BKEMachine                                                   │   │
│  │    labels:                                                                       │   │
│  │      cluster.x-k8s.io/cluster-name: my-cluster  ← CAPI 标准标签                  │   │
│  │      bke.bocloud.com/machine: my-bkemachine     ← BKEMachine 关联                │   │
│  │  spec:                                                                           │   │
│  │    nodeName: master-01                                                           │   │
│  │    desiredState:                                                                 │   │
│  │      kubernetesVersion: "1.30.0"  ← 来源: Machine.Spec.Version                   │   │
│  │      containerRuntime:            ← 来源: BKECluster.Spec.ClusterConfig          │   │
│  │        type: containerd                                                          │   │
│  │        version: "1.7.0"                                                          │   │
│  │      controlPlane:               ← 来源: Machine role + KCP config               │   │
│  │        role: upgradeControlPlane                                                 │   │
│  │      bkeConfigRef: "ns/name"      ← 来源: BKECluster                             │   │
│  │  status:                                                                         │   │
│  │    phase: Completed                                                              │   │
│  │    conditions:                                                                   │   │
│  │      - type: KubernetesVersion, status: "True"    → BKEMachine.Ready = true      │   │
│  │      - type: ContainerRuntime, status: "True"     → BKEMachine Conditions        │   │
│  └──────────────────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 八、符合 CAPI 规范的改造清单

| 改造项 | 当前状态 | 目标状态 | CAPI 规范 |
|--------|---------|---------|----------|
| BKECluster.GetConditions() | ❌ 缺失 | ✅ 实现 | InfrastructureCluster 必须 |
| BKECluster.SetConditions() | ❌ 缺失 | ✅ 实现 | InfrastructureCluster 必须 |
| BKECluster.Status.Ready | ✅ 已有 | ✅ 保持 | InfrastructureCluster 必须 |
| BKECluster.Status.FailureDomains | ❌ 缺失 | ✅ 补全 | InfrastructureCluster 推荐 |
| BKEMachine.GetConditions() | ✅ 已有 | ✅ 保持 | InfrastructureMachine 必须 |
| BKEMachine.Spec.ProviderID | ✅ 已有 | ✅ 保持 | InfrastructureMachine 必须 |
| BKEMachine.Status.Ready | ✅ 已有 | ✅ 保持 | InfrastructureMachine 必须 |
| BKEMachineReconciler 不越权操作 KCP | ❌ 当前越权 | ✅ 修正 | Provider 职责边界 |
| BKEMachineReconciler 不越权操作 KubeadmConfig | ❌ syncKubeadmConfig | ✅ 删除 | Bootstrap Provider 职责 |
| Command CR → NodeState CR | 命令式 | 声明式 | BKE 内部实现 |
| NodeState → BKEMachine 状态映射 | N/A | ✅ 新增 | CAPI 条件传播 |
| NodeState Watch 链路 | N/A | ✅ 新增 | CAPI 事件传播 |

### 九、总结

声明式架构与 CAPI 协同的核心原则是**分层解耦**：

1. **CAPI 标准接口层**：BKECluster/BKEMachine 严格遵循 CAPI Infrastructure Provider 规范，补全缺失接口，停止越权操作
2. **BKE 扩展层**：PhaseFrame 编排创建 NodeState CR（替代 Command CR），实现声明式节点状态管理
3. **节点执行层**：bkeagent 的 NodeStateReconciler + Capability 架构，不感知 CAPI 存在
4. **状态桥接层**：NodeState status → CAPI Conditions 映射，确保状态正确传播

这种设计确保了：
- **CAPI 兼容性**：标准 Provider 接口完整，可与 CAPI 生态工具（clusterctl、CAPI visualizer 等）互操作
- **声明式优势**：NodeState CR 声明期望状态，bkeagent 自主协调，天然幂等
- **可扩展性**：Capability 注册机制独立于 CAPI，第三方扩展不影响 CAPI 接口
- **职责清晰**：不再越权操作 KCP/KubeadmConfig，让 CAPI 标准 Controller 各司其职

# 声明式架构

现在我对 bkeagent 的完整架构有了深入理解，包括管理集群侧的 Command 构建层（`pkg/command/`）和节点侧的 Command 执行层（`controllers/bkeagent/` + `pkg/job/`）。下面给出重构设计思路。

## bkeagent 可扩展声明式架构重构设计

### 一、核心设计理念
当前 bkeagent 的本质问题是：**管理集群用命令式方式组装指令列表，节点侧用命令式方式顺序执行**。这导致：
1. 管理集群必须精确编排每一步的顺序和参数（[bootstrap.go](file:///D:\code\github\cluster-api-provider-bke\pkg\command\bootstrap.go)、[env.go](file:///D:\code\github\cluster-api-provider-bke\pkg\command\env.go)、[upgrade.go](file:///D:\code\github\cluster-api-provider-bke\pkg\command\upgrade.go)）
2. 节点侧无法自主决策，只能机械执行指令序列
3. 新增能力需要同时修改管理集群的 Command 组装逻辑和节点侧的 Plugin 实现

**重构目标**：从"命令式指令序列"转变为"声明式期望状态 + 节点自主协调"。
```txt
当前:  管理集群编排步骤 → Command CR(指令列表) → bkeagent 顺序执行
目标:  管理集群声明期望状态 → NodeState CR(期望状态) → bkeagent 协调当前状态→期望状态
```
### 二、架构总览
```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                           管理集群 (Management Cluster)                                 │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  PhaseFrame (现有)                                                                │  │
│  │  ┌─────────────────────────────────────────────────────────────────────────────┐  │  │
│  │  │  不再组装 Command 指令列表                                                    │  │  │
│  │  │  改为: 创建 NodeState CR，声明每个节点的期望状态                               │  │  │
│  │  └─────────────────────────────────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  NodeState CR (新增)                                                              │  │
│  │                                                                                   │  │
│  │  apiVersion: bkeagent.bocloud.com/v1beta2                                         │  │
│  │  kind: NodeState                                                                  │  │
│  │  spec:                                                                            │  │
│  │    nodeName: master-01                                                            │  │
│  │    desiredState:                                                                  │  │
│  │      kubernetesVersion: "1.30.0"                                                  │  │
│  │      etcdVersion: "3.6.4"                                                         │  │
│  │      containerRuntime:                                                            │  │
│  │        type: containerd                                                           │  │
│  │        version: "1.7.0"                                                           │  │
│  │        config:                                                                    │  │
│  │          dataRoot: "/var/lib/containerd"                                          │  │
│  │          cgroupDriver: systemd                                                    │  │
│  │      controlPlane:                                                                │  │
│  │        role: init                                                                 │  │
│  │        endpoint: "10.0.0.100:6443"                                                │  │
│  │      ha:                                                                          │  │
│  │        enabled: true                                                              │  │
│  │        virtualRouterId: 51                                                        │  │
│  │      network:                                                                     │  │
│  │        ipv4Forwarding: true                                                       │  │
│  │      bkeConfigRef: "ns/name"                                                      │  │
│  │    preProcess:                                                                    │  │
│  │      configRef: "preprocess-all-config"                                           │  │
│  │    postProcess:                                                                   │  │
│  │      configRef: "postprocess-all-config"                                          │  │
│  │  status:                                                                          │  │
│  │    observedGeneration: 5                                                          │  │
│  │    phase: Reconciling                                                             │  │
│  │    currentState:                                                                  │  │
│  │      kubernetesVersion: "1.29.0"                                                  │  │
│  │      etcdVersion: "3.5.0"                                                         │  │
│  │      containerRuntime:                                                            │  │
│  │        type: containerd                                                           │  │
│  │        version: "1.6.0"                                                           │  │
│  │    conditions:                                                                    │  │
│  │      - type: KubernetesVersion                                                    │  │
│  │        status: "False"     # 1.29.0 ≠ 1.30.0, 需要升级                             │  │
│  │        reason: VersionMismatch                                                    │  │
│  │        message: "current 1.29.0, desired 1.30.0"                                  │  │
│  │      - type: ContainerRuntime                                                     │  │
│  │        status: "False"                                                            │  │
│  │        reason: VersionMismatch                                                    │  │
│  │      - type: HA                                                                   │  │
│  │        status: "True"      # 已满足                                               │  │
│  │      - type: Network                                                              │  │
│  │        status: "True"                                                             │  │
│  │    lastTransitionTime: "2026-05-21T10:00:00Z"                                     │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────────────┘

                                    │
                                    │ Watch NodeState CR
                                    │
                                    ▼

┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                     业务集群节点 (bkeagent)                                              │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  NodeStateReconciler (新增 — 核心协调器)                                           │  │
│  │                                                                                   │  │
│  │  Reconcile(NodeState):                                                            │  │
│  │    1. 观测当前状态 (currentState)                                                  │  │
│  │    2. 对比期望状态 (desiredState)                                                  │  │
│  │    3. 生成差异计划 (reconcilePlan)                                                 │  │
│  │    4. 按依赖拓扑执行计划                                                           │  │
│  │    5. 更新 status.conditions                                                      │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  Capability Registry (可扩展插件注册中心)                                          │  │
│  │                                                                                   │  │
│  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌────────────┐   │  │
│  │  │ Kubernetes  │ │ Container   │ │ HA          │ │ Network     │ │ Script     │   │  │
│  │  │ Capability  │ │ Runtime     │ │ Capability  │ │ Capability  │ │ Capability │   │  │
│  │  │             │ │ Capability  │ │             │ │             │ │            │   │  │
│  │  │ Observe()   │ │ Observe()   │ │ Observe()   │ │ Observe()   │ │ Observe()  │   │  │
│  │  │ Reconcile() │ │ Reconcile() │ │ Reconcile() │ │ Reconcile() │ │ Reconcile()│   │  │
│  │  │ Depends()   │ │ Depends()   │ │ Depends()   │ │ Depends()   │ │ Depends()  │   │  │
│  │  └─────────────┘ └─────────────┘ └─────────────┘ └─────────────┘ └────────────┘   │  │
│  │                                                                                   │  │
│  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐                                  │  │
│  │  │ Cert        │ │ Kubelet     │ │ Custom*     │  ← 第三方可扩展                   │  │
│  │  │ Capability  │ │ Capability  │ │ Capability  │                                  │  │
│  │  └─────────────┘ └─────────────┘ └─────────────┘                                  │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```
### 三、核心 CRD 设计
#### 3.1 NodeState CR（声明式期望状态）
```go
// api/bkeagent/v1beta2/nodestate_types.go

type NodeStateSpec struct {
    NodeName string       `json:"nodeName"`
    
    // 声明式期望状态 — 不描述"怎么做"，只描述"要什么"
    DesiredState NodeDesiredState `json:"desiredState"`
    
    // 前后置处理（可选）
    PreProcess  *ProcessConfig `json:"preProcess,omitempty"`
    PostProcess *ProcessConfig `json:"postProcess,omitempty"`
    
    // 协调策略
    ReconcilePolicy ReconcilePolicy `json:"reconcilePolicy,omitempty"`
}

type NodeDesiredState struct {
    // K8s 组件版本
    KubernetesVersion string `json:"kubernetesVersion,omitempty"`
    EtcdVersion       string `json:"etcdVersion,omitempty"`
    
    // 容器运行时
    ContainerRuntime *ContainerRuntimeState `json:"containerRuntime,omitempty"`
    
    // 控制面角色
    ControlPlane *ControlPlaneState `json:"controlPlane,omitempty"`
    
    // HA 配置
    HA *HAState `json:"ha,omitempty"`
    
    // 网络配置
    Network *NetworkState `json:"network,omitempty"`
    
    // 证书配置
    Certificates *CertificateState `json:"certificates,omitempty"`
    
    // Kubelet 配置
    Kubelet *KubeletState `json:"kubelet,omitempty"`
    
    // 集群配置引用
    BKEConfigRef string `json:"bkeConfigRef,omitempty"`
    
    // 自定义扩展状态（可扩展能力的关键）
    Extensions map[string]runtime.RawExtension `json:"extensions,omitempty"`
}

type ContainerRuntimeState struct {
    Type         string            `json:"type"`    // containerd / docker / cri-docker
    Version      string            `json:"version,omitempty"`
    Config       map[string]string `json:"config,omitempty"`
}

type ControlPlaneState struct {
    Role     string `json:"role"`     // init / join / none
    Endpoint string `json:"endpoint,omitempty"`
}

type HAState struct {
    Enabled         bool   `json:"enabled"`
    VirtualRouterID int    `json:"virtualRouterId,omitempty"`
    VIP             string `json:"vip,omitempty"`
    // ...
}

type NetworkState struct {
    IPv4Forwarding *bool  `json:"ipv4Forwarding,omitempty"`
    // ...
}

type ReconcilePolicy struct {
    // 协调模式：Auto(自动执行) / Manual(需确认) / DryRun(只检测)
    Mode ReconcileMode `json:"mode,omitempty"`
    // 超时
    TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
    // 失败策略
    OnFailure FailurePolicy `json:"onFailure,omitempty"`
}

type ReconcileMode string
const (
    ReconcileModeAuto    ReconcileMode = "Auto"
    ReconcileModeManual  ReconcileMode = "Manual"
    ReconcileModeDryRun  ReconcileMode = "DryRun"
)

type FailurePolicy string
const (
    FailurePolicyRollback FailurePolicy = "Rollback"
    FailurePolicyStop     FailurePolicy = "Stop"
    FailurePolicyContinue FailurePolicy = "Continue"
)

type NodeStateStatus struct {
    ObservedGeneration int64 `json:"observedGeneration"`
    Phase NodeStatePhase `json:"phase"`
    
    // 当前实际状态（bkeagent 自主观测）
    CurrentState NodeDesiredState `json:"currentState,omitempty"`
    
    // 每个能力的协调状态
    Conditions []NodeStateCondition `json:"conditions,omitempty"`
    
    // 协调计划（差异分析结果）
    ReconcilePlan *ReconcilePlan `json:"reconcilePlan,omitempty"`
}

type NodeStatePhase string
const (
    NodeStatePhaseIdle         NodeStatePhase = "Idle"         // 无差异
    NodeStatePhaseReconciling  NodeStatePhase = "Reconciling"  // 正在协调
    NodeStatePhaseCompleted    NodeStatePhase = "Completed"    // 协调完成
    NodeStatePhaseFailed       NodeStatePhase = "Failed"       // 协调失败
    NodeStatePhaseBlocked      NodeStatePhase = "Blocked"      // 被依赖阻塞
)

type NodeStateCondition struct {
    Type     string               `json:"type"`
    Status   metav1.ConditionStatus `json:"status"`
    Reason   string               `json:"reason,omitempty"`
    Message  string               `json:"message,omitempty"`
    Actions  []PlannedAction      `json:"actions,omitempty"`
}

type ReconcilePlan struct {
    // DAG 拓扑排序后的执行步骤
    Steps []ReconcileStep `json:"steps,omitempty"`
}

type ReconcileStep struct {
    Capability string `json:"capability"`
    Action     string `json:"action"`
    DependsOn  []string `json:"dependsOn,omitempty"`
}
```
#### 3.2 向后兼容：Command CR 保留
```go
// Command CR 保留，但内部转换为 NodeState
// CommandReconciler 变为适配器：

func (r *CommandReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    command := &agentv1beta1.Command{}
    if err := r.Get(ctx, req.NamespacedName, command); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 将命令式 Command 转换为声明式 NodeState
    nodeState := r.translateCommandToNodeState(command)
    
    // 委托给 NodeStateReconciler 执行
    return r.NodeStateReconciler.Reconcile(ctx, nodeState)
}
```
### 四、Capability 接口设计（可扩展的核心）
```go
// pkg/capability/interface.go

type Capability interface {
    // 能力名称，对应 NodeState 中的字段
    Name() string
    
    // 观测当前状态
    Observe(ctx context.Context, spec NodeDesiredState) (ObservedState, error)
    
    // 对比差异，生成协调计划
    Plan(ctx context.Context, desired NodeDesiredState, observed ObservedState) (*ReconcilePlan, error)
    
    // 执行协调
    Reconcile(ctx context.Context, plan *ReconcilePlan) ([]string, error)
    
    // 声明依赖（DAG 拓扑排序依据）
    Depends() []string
}

type ObservedState struct {
    // 当前实际值
    Current map[string]interface{}
    // 是否满足期望
    Satisfied bool
    // 不满足时的差异描述
    Diff []DiffItem
}

type DiffItem struct {
    Field    string
    Current  string
    Desired  string
    Action   string  // create / update / delete
}

// CapabilityRegistry 可扩展注册中心
type CapabilityRegistry struct {
    capabilities map[string]Capability
    mu           sync.RWMutex
}

func (r *CapabilityRegistry) Register(c Capability) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    name := c.Name()
    if _, exists := r.capabilities[name]; exists {
        return fmt.Errorf("capability %q already registered", name)
    }
    r.capabilities[name] = c
    return nil
}

func (r *CapabilityRegistry) Get(name string) (Capability, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    c, ok := r.capabilities[name]
    return c, ok
}

// TopologicalSort 按依赖关系拓扑排序
func (r *CapabilityRegistry) TopologicalSort() ([]Capability, error) {
    // DAG 拓扑排序实现
    // 确保依赖的能力先执行
    ...
}
```
### 五、NodeStateReconciler 核心协调逻辑
```go
// controllers/bkeagent/nodestate_controller.go

type NodeStateReconciler struct {
    client.Client
    Registry *capability.CapabilityRegistry
    NodeName string
}

func (r *NodeStateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    nodeState := &agentv1beta2.NodeState{}
    if err := r.Get(ctx, req.NamespacedName, nodeState); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 只处理本节点的 NodeState
    if nodeState.Spec.NodeName != r.NodeName {
        return ctrl.Result{}, nil
    }
    
    // 1. 观测：所有 Capability 并发观测当前状态
    observed := r.observeAll(ctx, nodeState.Spec.DesiredState)
    
    // 2. 规划：生成差异计划（DAG 拓扑排序）
    plan := r.planAll(ctx, nodeState.Spec.DesiredState, observed)
    
    // 3. 执行：按拓扑顺序执行协调
    result := r.executePlan(ctx, plan)
    
    // 4. 上报：更新 NodeState status
    r.updateStatus(ctx, nodeState, observed, result)
    
    if result.NeedsRequeue {
        return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
    }
    return ctrl.Result{}, nil
}

func (r *NodeStateReconciler) observeAll(ctx context.Context, desired agentv1beta2.NodeDesiredState) map[string]capability.ObservedState {
    observed := map[string]capability.ObservedState{}
    
    // 按拓扑顺序观测（观测本身无依赖，可并发）
    var wg sync.WaitGroup
    var mu sync.Mutex
    
    for name, cap := range r.Registry.List() {
        wg.Add(1)
        go func(name string, cap capability.Capability) {
            defer wg.Done()
            state, err := cap.Observe(ctx, desired)
            mu.Lock()
            if err != nil {
                observed[name] = capability.ObservedState{Satisfied: false, Diff: []capability.DiffItem{{
                    Field: "observe", Action: "error: " + err.Error(),
                }}}
            } else {
                observed[name] = state
            }
            mu.Unlock()
        }(name, cap)
    }
    wg.Wait()
    return observed
}

func (r *NodeStateReconciler) planAll(ctx context.Context, 
    desired agentv1beta2.NodeDesiredState, 
    observed map[string]capability.ObservedState) *capability.ReconcilePlan {
    
    // 按依赖拓扑排序
    sorted, err := r.Registry.TopologicalSort()
    if err != nil {
        // 循环依赖，报错
        return &capability.ReconcilePlan{Error: err}
    }
    
    var steps []capability.ReconcileStep
    for _, cap := range sorted {
        obs, ok := observed[cap.Name()]
        if !ok || obs.Satisfied {
            continue  // 已满足，跳过
        }
        
        plan, err := cap.Plan(ctx, desired, obs)
        if err != nil {
            steps = append(steps, capability.ReconcileStep{
                Capability: cap.Name(),
                Action:     "error: " + err.Error(),
            })
            continue
        }
        
        if plan != nil {
            steps = append(steps, plan.Steps...)
        }
    }
    
    return &capability.ReconcilePlan{Steps: steps}
}

func (r *NodeStateReconciler) executePlan(ctx context.Context, plan *capability.ReconcilePlan) ReconcileResult {
    for _, step := range plan.Steps {
        cap, ok := r.Registry.Get(step.Capability)
        if !ok {
            continue
        }
        
        // 检查依赖是否已满足
        for _, dep := range step.DependsOn {
            depCap, ok := r.Registry.Get(dep)
            if !ok {
                return ReconcileResult{Error: fmt.Errorf("dependency %s not found", dep)}
            }
            obs, _ := depCap.Observe(ctx, agentv1beta2.NodeDesiredState{})
            if !obs.Satisfied {
                return ReconcileResult{NeedsRequeue: true, BlockedBy: dep}
            }
        }
        
        // 执行协调
        _, err := cap.Reconcile(ctx, &capability.ReconcilePlan{Steps: []capability.ReconcileStep{step}})
        if err != nil {
            return ReconcileResult{Error: err, FailedAt: step.Capability}
        }
    }
    return ReconcileResult{Success: true}
}
```
### 六、Capability 实现示例
#### 6.1 ContainerRuntime Capability
```go
// pkg/capability/runtime/containerd.go

type ContainerdCapability struct {
    exec          exec.Executor
    clusterAccessor plugin.ClusterAccessor
}

func (c *ContainerdCapability) Name() string { return "containerRuntime" }

func (c *ContainerdCapability) Depends() []string { return nil }  // 无依赖

func (c *ContainerdCapability) Observe(ctx context.Context, 
    desired agentv1beta2.NodeDesiredState) (capability.ObservedState, error) {
    
    // 观测当前 containerd 版本和配置
    currentVersion, _ := c.getCurrentVersion()
    currentConfig, _ := c.getCurrentConfig()
    
    desiredCR := desired.ContainerRuntime
    if desiredCR == nil {
        return capability.ObservedState{Satisfied: true}, nil
    }
    
    var diffs []capability.DiffItem
    if currentVersion != desiredCR.Version {
        diffs = append(diffs, capability.DiffItem{
            Field:   "version",
            Current: currentVersion,
            Desired: desiredCR.Version,
            Action:  "update",
        })
    }
    // 比较配置...
    
    return capability.ObservedState{
        Current:   map[string]interface{}{"version": currentVersion, "config": currentConfig},
        Satisfied: len(diffs) == 0,
        Diff:      diffs,
    }, nil
}

func (c *ContainerdCapability) Plan(ctx context.Context, 
    desired agentv1beta2.NodeDesiredState, 
    observed capability.ObservedState) (*capability.ReconcilePlan, error) {
    
    if observed.Satisfied {
        return nil, nil
    }
    
    var steps []capability.ReconcileStep
    for _, diff := range observed.Diff {
        switch diff.Action {
        case "update":
            steps = append(steps, capability.ReconcileStep{
                Capability: c.Name(),
                Action:     fmt.Sprintf("update %s from %s to %s", diff.Field, diff.Current, diff.Desired),
            })
        }
    }
    return &capability.ReconcilePlan{Steps: steps}, nil
}

func (c *ContainerdCapability) Reconcile(ctx context.Context, 
    plan *capability.ReconcilePlan) ([]string, error) {
    // 实际执行 containerd 升级/配置变更
    // 幂等：如果已经满足，直接返回
    ...
}
```
#### 6.2 Kubernetes Capability（依赖 ContainerRuntime）
```go
// pkg/capability/kubernetes/kubernetes.go

type KubernetesCapability struct {
    exec           exec.Executor
    clusterAccessor plugin.ClusterAccessor
}

func (k *KubernetesCapability) Name() string { return "kubernetes" }

func (k *KubernetesCapability) Depends() []string { 
    return []string{"containerRuntime", "network", "certificates"} 
}

func (k *KubernetesCapability) Observe(ctx context.Context, 
    desired agentv1beta2.NodeDesiredState) (capability.ObservedState, error) {
    
    // 观测当前 K8s 版本
    currentVersion, _ := k.getCurrentKubernetesVersion()
    
    var diffs []capability.DiffItem
    if currentVersion != desired.KubernetesVersion {
        diffs = append(diffs, capability.DiffItem{
            Field:   "kubernetesVersion",
            Current: currentVersion,
            Desired: desired.KubernetesVersion,
            Action:  "update",
        })
    }
    
    return capability.ObservedState{
        Current:   map[string]interface{}{"kubernetesVersion": currentVersion},
        Satisfied: len(diffs) == 0,
        Diff:      diffs,
    }, nil
}

func (k *KubernetesCapability) Reconcile(ctx context.Context, 
    plan *capability.ReconcilePlan) ([]string, error) {
    
    // 根据当前状态和期望状态的差异，自主决定执行步骤
    // 不再需要管理集群告诉它 "先升级 apiserver，再升级 controller-manager"
    // Capability 自己知道升级控制面需要按组件顺序执行
    
    for _, step := range plan.Steps {
        switch {
        case strings.Contains(step.Action, "initControlPlane"):
            return k.initControlPlane(ctx)
        case strings.Contains(step.Action, "joinControlPlane"):
            return k.joinControlPlane(ctx)
        case strings.Contains(step.Action, "upgrade"):
            return k.upgradeControlPlane(ctx)
        }
    }
    return nil, nil
}
```
### 七、第三方扩展机制
```go
// pkg/capability/extension/extension.go

// ExtensionCapability 第三方可扩展能力
type ExtensionCapability struct {
    name       string
    scriptDir  string
    exec       exec.Executor
    depends    []string
}

func NewExtensionCapability(name string, config ExtensionConfig) *ExtensionCapability {
    return &ExtensionCapability{
        name:      name,
        scriptDir: config.ScriptDir,
        exec:      config.Exec,
        depends:   config.Depends,
    }
}

func (e *ExtensionCapability) Name() string    { return e.name }
func (e *ExtensionCapability) Depends() []string { return e.depends }

func (e *ExtensionCapability) Observe(ctx context.Context, 
    desired agentv1beta2.NodeDesiredState) (capability.ObservedState, error) {
    
    // 从 extensions 字段获取期望状态
    ext, ok := desired.Extensions[e.name]
    if !ok {
        return capability.ObservedState{Satisfied: true}, nil
    }
    
    // 执行 observe 脚本
    output, err := e.exec.ExecuteCommandWithOutput(
        filepath.Join(e.scriptDir, "observe.sh"),
    )
    
    return capability.ObservedState{
        Current:   map[string]interface{}{"output": output},
        Satisfied: err == nil && output == "satisfied",
    }, nil
}

func (e *ExtensionCapability) Reconcile(ctx context.Context, 
    plan *capability.ReconcilePlan) ([]string, error) {
    
    output, err := e.exec.ExecuteCommandWithOutput(
        filepath.Join(e.scriptDir, "reconcile.sh"),
    )
    return []string{output}, err
}
```

**注册扩展**：

```go
// cmd/bkeagent/extensions.go
func registerExtensions(registry *capability.CapabilityRegistry, exec exec.Executor) error {
    // 扫描 /etc/openFuyao/bkeagent/extensions/ 目录
    extDir := "/etc/openFuyao/bkeagent/extensions"
    entries, err := os.ReadDir(extDir)
    if err != nil {
        return nil  // 无扩展目录，正常
    }
    
    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }
        config := ExtensionConfig{
            ScriptDir: filepath.Join(extDir, entry.Name()),
            Exec:      exec,
        }
        ext := NewExtensionCapability(entry.Name(), config)
        if err := registry.Register(ext); err != nil {
            log.Warnf("skip extension %s: %v", entry.Name(), err)
        }
    }
    return nil
}
```

### 八、协调流程对比

#### 当前流程（命令式）
```txt
管理集群 PhaseFrame:
  1. ENV.New() → 组装 ["K8sEnvInit", "init=true", "check=true", "scope=runtime", ...]
  2. Bootstrap.New() → 组装 ["Kubeadm", "phase=initControlPlane", ...]
  3. 创建 Command CR
  4. 等待 Command status 完成

bkeagent:
  1. Watch Command CR
  2. 顺序执行 spec.commands[0], [1], [2]...
  3. 每步更新 condition
  4. 全部完成更新 phase=Completed
```

**问题**：管理集群必须精确知道每一步的参数和顺序，节点无法自主决策。

#### 重构后流程（声明式）
```txt
管理集群 PhaseFrame:
  1. 创建 NodeState CR
     spec.desiredState:
       kubernetesVersion: "1.30.0"
       containerRuntime: {type: containerd, version: "1.7.0"}
       controlPlane: {role: init}
       network: {ipv4Forwarding: true}

bkeagent NodeStateReconciler:
  1. Watch NodeState CR
  2. Observe: 并发观测所有 Capability 当前状态
     - containerRuntime: 当前 1.6.0 ≠ 1.7.0 → Diff
     - network: ipv4=true → Satisfied
     - kubernetes: 未安装 → Diff
  3. Plan: 生成 DAG 拓扑执行计划
     Step 1: network (无依赖, 已满足, 跳过)
     Step 2: containerRuntime (无依赖, 需升级)
     Step 3: kubernetes (依赖 containerRuntime, 需安装)
  4. Execute: 按拓扑顺序执行
     - containerRuntime.Reconcile() → 升级 containerd
     - kubernetes.Reconcile() → 自主决定 initControlPlane
  5. Update Status: 上报每个 Capability 的 condition
```

**优势**：
- 管理集群只需声明"要什么"，不需要编排"怎么做"
- 节点自主观测和决策，更智能
- 新增能力只需实现 Capability 接口 + 注册，无需修改核心逻辑
- 天然幂等：每次 Reconcile 都从观测开始，已满足的自动跳过

### 九、迁移策略
```txt
┌──────────────────────────────────────────────────────────────────────────────────┐
│                        三阶段迁移路线                                             │
│                                                                                  │
│  Phase 1: 双栈并行 (4-6 周)                                                       │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  • 新增 NodeState CRD (v1beta2)，与 Command CRD 共存                        │  │
│  │  • 实现 CapabilityRegistry + 核心 Capability (runtime, kubernetes)          │  │
│  │  • CommandReconciler 内部翻译为 NodeState 逻辑（适配器模式）                 │  │
│  │  • 新功能使用 NodeState，旧功能保持 Command 不变                             │  │
│  │  • 验证: 新旧路径执行结果一致                                                │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  Phase 2: 全面切换 (4-6 周)                                                       │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  • PhaseFrame 全部切换为创建 NodeState CR                                   │  │
│  │  • 补全所有 Capability 实现 (ha, network, cert, kubelet, script...)         │  │
│  │  • NodeStateReconciler 成为唯一协调入口                                     │  │
│  │  • Command CR 标记为 Deprecated                                            │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                  │
│  Phase 3: 生态扩展 (持续)                                                         │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │  • 开放 ExtensionCapability 机制                                            │  │
│  │  • 提供 Capability SDK，第三方可独立开发                                     │  │
│  │  • 移除 Command CR (下个大版本)                                             │  │
│  │  • 支持 Capability 热加载                                                   │  │
│  └────────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────────┘
```
### 十、设计总结
| 维度 | 当前（命令式） | 重构后（声明式） |
|------|---------------|-----------------|
| **管理集群职责** | 编排每一步的指令和参数 | 声明节点的期望状态 |
| **节点职责** | 机械执行指令序列 | 自主观测→规划→协调 |
| **扩展方式** | 修改 builtin.go + 新增 Plugin + 修改 Command 组装 | 实现 Capability 接口 + Register |
| **状态模型** | Command.status（执行过程状态） | NodeState.status（期望 vs 实际状态） |
| **幂等性** | 靠 conditions 断点续执行 | 每次 Reconcile 从观测开始，天然幂等 |
| **依赖管理** | 靠指令顺序隐式保证 | DAG 拓扑排序显式声明 |
| **第三方扩展** | 不支持 | ExtensionCapability + 脚本/SDK |
| **智能程度** | 管理集群决策，节点执行 | 节点自主决策执行路径 |
| **可观测性** | 只能看到"哪步成功/失败" | 能看到"当前状态 vs 期望状态"的差异 |
        

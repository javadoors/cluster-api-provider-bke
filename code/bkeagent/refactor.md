
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
        

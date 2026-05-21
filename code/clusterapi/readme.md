# 完整的 CAPI Provider 契约约束分析

## CAPI Provider 完整契约约束

基于 CAPI v1.5.0 (项目当前依赖版本) 的规范，一个合规的 Provider 必须满足以下三大类契约。

### 一、CRD 契约（类型定义层）

#### 1.1 InfrastructureCluster 契约

CAPI 要求 InfrastructureCluster 类型的 CRD **必须**满足以下字段契约：

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  InfrastructureCluster CRD 契约                                                         │
│                                                                                         │
│  apiVersion: infrastructure.cluster.x-k8s.io/v1beta1                                    │
│  kind: BKECluster                                                                       │
│                                                                                         │
│  metadata:                                                                              │
│    labels:                                                                              │
│      cluster.x-k8s.io/provider: infrastructure-bke     ← 必须，clusterctl 识别用        │
│      cluster.x-k8s.io/v1beta1: v1beta1                 ← 必须，版本标签                │
│    ownerReferences:                                                                     │
│      - apiVersion: cluster.x-k8s.io/v1beta1            ← 必须，指向 CAPI Cluster       │
│        kind: Cluster                                                                    │
│        name: <cluster-name>                                                             │
│        uid: <cluster-uid>                                                               │
│        controller: true                                                                 │
│                                                                                         │
│  spec:                                                                                  │
│    controlPlaneEndpoint:                  ← 必须，CAPI Core 读取此字段                   │
│      host: "10.0.0.100"                                                                │
│      port: 6443                                                                        │
│                                                                                         │
│  status:                                                                                │
│    ready: true                           ← 必须，CAPI 判断基础设施是否就绪               │
│    failureDomains:                       ← 推荐，影响 Machine 调度决策                   │
│      zone-a:                                                                             │
│        controlPlane: true                                                               │
│      zone-b:                                                                             │    conditions:                            ← 必须，实现 Conditions 接口                         │
│      - type: Ready                                                                      │
│        status: "True"                                                                   │
│        reason: "InfrastructureReady"                                                    │
│        lastTransitionTime: "..."                                                        │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

**Go 接口契约**：

```go
// 必须实现 conditions.Getter 和 conditions.Setter 接口
type BKECluster struct {
    // ...
    Status BKEClusterStatus `json:"status,omitempty"`
}

// 必须实现 — 当前 BKE 缺失！
func (c *BKECluster) GetConditions() clusterv1.Conditions {
    return c.Status.Conditions
}

// 必须实现 — 当前 BKE 缺失！
func (c *BKECluster) SetConditions(conditions clusterv1.Conditions) {
    c.Status.Conditions = conditions
}
```

**当前 BKE 差距**：

| 契约字段 | CAPI 要求 | BKE 当前状态 | 差距 |
|----------|----------|-------------|------|
| `metadata.labels[cluster.x-k8s.io/provider]` | 必须 | ✅ `infrastructure-bke` | 无 |
| `metadata.labels[cluster.x-k8s.io/v1beta1]` | 必须 | ✅ `v1beta1` | 无 |
| `metadata.ownerReferences → Cluster` | 必须 | ✅ 通过 `mergecluster` 设置 | 无 |
| `spec.controlPlaneEndpoint` | 必须 | ✅ `APIEndpoint{Host, Port}` | 无 |
| `status.ready` | 必须 | ✅ `bool` | 无 |
| `status.failureDomains` | 推荐 | ❌ 缺失 | **需补全** |
| `GetConditions()` | 必须 | ❌ 缺失 | **需补全** |
| `SetConditions()` | 必须 | ❌ 缺失 | **需补全** |
| `status.conditions` 类型 | `clusterv1.Conditions` | ❌ 自定义 `ClusterConditions` | **需对齐** |

**关键问题**：BKECluster 的 `status.conditions` 使用了自定义的 `ClusterConditions` 类型（[bkecluster_status.go:131](file:///D:\code\github\cluster-api-provider-bke\api\bkecommon\v1beta1\bkecluster_status.go#L131)），而非 CAPI 标准的 `clusterv1.Conditions`。这导致无法实现 `conditions.Getter/Setter` 接口。

#### 1.2 InfrastructureMachine 契约

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  InfrastructureMachine CRD 契约                                                         │
│                                                                                         │
│  apiVersion: infrastructure.cluster.x-k8s.io/v1beta1                                    │
│  kind: BKEMachine                                                                       │
│                                                                                         │
│  metadata:                                                                              │
│    labels:                                                                              │
│      cluster.x-k8s.io/provider: infrastructure-bke     ← 必须                          │
│      cluster.x-k8s.io/v1beta1: v1beta1                 ← 必须                          │
│      cluster.x-k8s.io/cluster-name: <name>             ← 必须，CAPI Core 使用           │
│    ownerReferences:                                                                     │
│      - apiVersion: cluster.x-k8s.io/v1beta1            ← 必须，指向 CAPI Machine        │
│        kind: Machine                                                                    │
│        name: <machine-name>                                                             │
│        uid: <machine-uid>                                                               │
│        controller: true                                                                 │
│                                                                                         │
│  spec:                                                                                  │
│    providerID: "bke://<cluster>/<node-ip>"              ← 必须，CAPI 关联 Node 用        │
│                                                                                         │
│  status:                                                                                │
│    ready: true                           ← 必须，CAPI 判断 Machine 是否就绪              │
│    addresses:                            ← 必须，CAPI 读取节点地址                       │
│      - type: InternalIP                                                                 │
│        address: "10.0.0.1"                                                              │
│      - type: Hostname                                                                   │
│        address: "master-01"                                                             │
│    conditions:                           ← 必须，clusterv1.Conditions 类型              │
│      - type: Ready                                                                      │
│        status: "True"                                                                   │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

**当前 BKE 差距**：

| 契约字段 | CAPI 要求 | BKE 当前状态 | 差距 |
|----------|----------|-------------|------|
| `metadata.ownerReferences → Machine` | 必须 | ✅ | 无 |
| `spec.providerID` | 必须 | ✅ | 无 |
| `status.ready` | 必须 | ✅ | 无 |
| `status.addresses` | 必须 | ✅ `[]MachineAddress` | 无 |
| `GetConditions()` | 必须 | ✅ 已实现 | 无 |
| `SetConditions()` | 必须 | ✅ 已实现 | 无 |
| `status.conditions` 类型 | `clusterv1.Conditions` | ✅ 已对齐 | 无 |

#### 1.3 InfrastructureMachineTemplate 契约

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  InfrastructureMachineTemplate CRD 契约                                                 │
│                                                                                         │
│  kind: BKEMachineTemplate                                                               │
│                                                                                         │
│  metadata:                                                                              │
│    labels:                                                                              │
│      cluster.x-k8s.io/provider: infrastructure-bke     ← 必须                          │
│      cluster.x-k8s.io/v1beta1: v1beta1                 ← 必须                          │
│                                                                                         │
│  spec:                                                                                  │
│    template:                                                                            │
│      metadata:                          ← 可选，传递到 BKEMachine                       │
│      spec:                              ← 必须与 BKEMachineSpec 结构一致                 │
│        providerID: ...                                                                  │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

**当前 BKE 差距**：✅ 基本满足，`BKEMachineTemplateResource.Spec` 类型为 `BKEMachineSpec`。

#### 1.4 InfrastructureClusterTemplate 契约

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  InfrastructureClusterTemplate CRD 契约                                                 │
│                                                                                         │
│  kind: BKEClusterTemplate                                                               │
│                                                                                         │
│  metadata:                                                                              │
│    labels:                                                                              │
│      cluster.x-k8s.io/provider: infrastructure-bke     ← 必须                          │
│      cluster.x-k8s.io/v1beta1: v1beta1                 ← 必须                          │
│                                                                                         │
│  spec:                                                                                  │
│    template:                                                                            │
│      metadata:                          ← 可选                                          │
│      spec:                              ← 必须与 BKEClusterSpec 结构一致                 │
│        controlPlaneEndpoint:                                                            │
│        clusterConfig:                                                                   │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

**当前 BKE 差距**：✅ 基本满足，`BKEClusterTemplateResource.Spec` 类型为 `confv1beta1.BKEClusterSpec`。

### 二、Controller 契约（行为层）

#### 2.1 BKEClusterReconciler 行为契约

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  BKEClusterReconciler 行为契约                                                          │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  Reconcile 入口行为                                                               │  │
│  │                                                                                   │  │
│  │  1. 获取 BKECluster 对象                                                          │  │
│  │     - 不存在 → 返回 ctrl.Result{}, nil                                            │  │
│  │                                                                                   │  │
│  │  2. 获取 Owner Cluster                                                            │  │
│  │     - Cluster == nil → 等待 OwnerRef 设置，返回 ctrl.Result{}, nil                │  │
│  │     - Cluster 已删除 → 清理资源，返回 ctrl.Result{}, nil                           │  │
│  │                                                                                   │  │
│  │  3. 检查 Cluster 是否 Paused                                                      │  │
│  │     - Paused → 返回 ctrl.Result{}, nil                                            │  │
│  │                                                                                   │  │
│  │  4. 处理 Finalizer                                                                │  │
│  │     - 删除中 → 执行清理，移除 Finalizer                                            │  │
│  │     - 存在中 → 确保 Finalizer 存在                                                 │  │
│  │                                                                                   │  │
│  │  5. 执行 Reconcile 逻辑                                                           │  │
│  │     - 设置 status.controlPlaneEndpoint                                             │  │
│  │     - 设置 status.ready                                                           │  │
│  │     - 设置 status.failureDomains                                                   │  │
│  │     - 更新 Conditions                                                             │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  Finalizer 契约                                                                    │  │
│  │                                                                                   │  │
│  │  Finalizer 名称: "bkecluster.infrastructure.cluster.x-k8s.io"                     │  │
│  │                                                                                   │  │
│  │  创建时:                                                                           │  │
│  │    - 如果 BKECluster.DeletionTimestamp.IsZero() && 无 Finalizer                    │  │
│  │    → 添加 Finalizer，Update 并返回                                                 │  │
│  │                                                                                   │  │
│  │  删除时:                                                                           │  │
│  │    - 如果 !BKECluster.DeletionTimestamp.IsZero()                                   │  │
│  │    → 执行基础设施清理 (删除远端资源)                                                │  │
│  │    → 移除 Finalizer，Update                                                        │  │
│  │    → CAPI Core 负责最终删除 BKECluster 对象                                        │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  Conditions 更新契约                                                               │  │
│  │                                                                                   │  │
│  │  基础设施就绪时:                                                                    │  │
│  │    conditions.MarkTrue(bkeCluster, clusterv1.ReadyCondition)                       │  │
│  │                                                                                   │  │
│  │  基础设施未就绪时:                                                                  │  │
│  │    conditions.MarkFalse(bkeCluster, clusterv1.ReadyCondition,                      │  │
│  │        "InfrastructureNotReady", clusterv1.ConditionSeverityWarning, "...")        │  │
│  │                                                                                   │  │
│  │  等待 Cluster 关联时:                                                              │  │
│  │    conditions.MarkFalse(bkeCluster, clusterv1.ReadyCondition,                      │  │
│  │        "WaitingForCluster", clusterv1.ConditionSeverityInfo, "...")                │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 2.2 BKEMachineReconciler 行为契约

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  BKEMachineReconciler 行为契约                                                          │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  Reconcile 入口行为                                                               │  │
│  │                                                                                   │  │
│  │  1. 获取 BKEMachine 对象                                                           │  │
│  │     - 不存在 → 返回 ctrl.Result{}, nil                                            │  │
│  │                                                                                   │  │
│  │  2. 获取 Owner Machine                                                            │  │
│  │     - Machine == nil → 等待，返回 ctrl.Result{}, nil                              │  │
│  │                                                                                   │  │
│  │  3. 获取 Cluster                                                                  │  │
│  │     - Cluster == nil → 等待，返回 ctrl.Result{}, nil                              │  │
│  │                                                                                   │  │
│  │  4. 检查 Cluster.InfrastructureReady                                              │  │
│  │     - false → 等待，设置 Condition:                                                │  │
│  │       conditions.MarkFalse(bkeMachine, clusterv1.ReadyCondition,                   │  │
│  │           "WaitingForInfrastructure", clusterv1.ConditionSeverityInfo, "")         │  │
│  │       返回 ctrl.Result{}, nil                                                     │  │
│  │                                                                                   │  │
│  │  5. 检查 Machine 是否 Paused                                                      │  │
│  │     - Paused → 返回 ctrl.Result{}, nil                                            │  │
│  │                                                                                   │  │
│  │  6. 处理 Finalizer                                                                │  │
│  │     - 同 BKECluster 逻辑                                                          │  │
│  │                                                                                   │  │
│  │  7. 执行 Reconcile 逻辑                                                           │  │
│  │     - 设置 spec.providerID                                                        │  │
│  │     - 设置 status.ready                                                           │  │
│  │     - 设置 status.addresses                                                       │  │
│  │     - 更新 Conditions                                                             │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  ProviderID 契约                                                                   │  │
│  │                                                                                   │  │
│  │  格式: "bke://<cluster-name>/<node-ip>"                                           │  │
│  │                                                                                   │  │
│  │  CAPI Core 使用 ProviderID:                                                       │  │
│  │    1. Machine Controller 通过 ProviderID 在工作负载集群中查找对应 Node              │  │
│  │    2. Node 对象的 spec.providerID 必须与 Machine 的 spec.providerID 匹配           │  │
│  │    3. 匹配成功后 Machine Controller 设置 NodeRef                                  │  │
│  │                                                                                   │  │
│  │  设置时机:                                                                         │  │
│  │    - 节点基础设施创建完成后立即设置                                                 │  │
│  │    - 不需要等待节点完全就绪（kubelet 启动等）                                       │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  Worker Node Bootstrap 顺序契约                                                    │  │
│  │                                                                                   │  │
│  │  CAPI Core 期望的顺序:                                                             │  │
│  │    1. Infrastructure Provider 创建基础设施 → BKEMachine.status.ready = true        │  │
│  │    2. Bootstrap Provider 生成 bootstrap 数据 → KubeadmConfig.status.dataSecretName │  │
│  │    3. Machine Controller 读取 dataSecretName，通过 cloud-init 执行 bootstrap       │  │
│  │    4. 节点启动后，Machine Controller 通过 ProviderID 关联 Node                     │  │
│  │                                                                                   │  │
│  │  BKE 当前问题:                                                                     │  │
│  │    ❌ BKEMachineReconciler 同时负责基础设施和 bootstrap                            │  │
│  │    ❌ 不经过 KubeadmConfig.dataSecretName 流程                                     │  │
│  │    ❌ 直接通过 Command CR 执行 bootstrap                                           │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 2.3 删除行为契约

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  删除行为契约                                                                            │
│                                                                                         │
│  BKECluster 删除:                                                                       │
│    1. 用户删除 Cluster → CAPI Core 设置 Cluster.DeletionTimestamp                       │
│    2. CAPI Core 删除关联的 Machine → 触发 BKEMachine 删除                               │
│    3. BKEMachineReconciler:                                                             │
│       a. Drain 节点 (如果配置了 NodeDrainTimeout)                                       │
│       b. 断开 Volume 连接                                                               │
│       c. 删除远端基础设施 (重置节点)                                                     │
│       d. 移除 Finalizer                                                                 │
│    4. BKEClusterReconciler:                                                             │
│       a. 等待所有 BKEMachine 删除完成                                                    │
│       b. 清理集群级别基础设施 (负载均衡器等)                                             │
│       c. 移除 Finalizer                                                                 │
│    5. CAPI Core 执行最终垃圾回收                                                        │
│                                                                                         │
│  关键约束:                                                                              │
│    - BKEMachine 删除必须在 BKECluster 删除之前完成                                      │
│    - Finalizer 移除后，对象由 Kubernetes GC 自动删除                                    │
│    - 删除过程中必须处理幂等性（重复 Reconcile 不应重复操作）                             │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 三、Watch 链路契约（事件传播层）

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  Watch 链路契约                                                                         │
│                                                                                         │
│  BKEClusterReconciler 必须 Watch:                                                       │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  1. BKECluster (For)                                                              │  │
│  │     - 过滤: Spec 变更 / Annotation 变更                                            │  │
│  │                                                                                   │  │
│  │  2. Cluster (Watches)                                                              │  │
│  │     - 映射: Cluster → BKECluster (通过 InfrastructureRef)                          │  │
│  │     - 过滤: UnPause 事件                                                           │  │
│  │                                                                                   │  │
│  │  3. BKEMachine (Watches)                                                           │  │
│  │     - 映射: BKEMachine → BKECluster (通过 OwnerRef → Cluster → InfraRef)           │  │
│  │     - 过滤: Ready 状态变更                                                         │  │
│  │                                                                                   │  │
│  │  4. NodeState CR (Watches) ← 新增                                                 │  │
│  │     - 映射: NodeState → BKECluster (通过 label)                                    │  │
│  │     - 过滤: Phase 变更                                                             │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  BKEMachineReconciler 必须 Watch:                                                       │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  1. BKEMachine (For)                                                               │  │
│  │                                                                                   │  │
│  │  2. Machine (Watches)                                                              │  │
│  │     - 映射: Machine → BKEMachine (通过 InfrastructureRef)                          │  │
│  │                                                                                   │  │
│  │  3. Cluster (Watches)                                                              │  │
│  │     - 映射: Cluster → BKEMachine 列表                                              │  │
│  │                                                                                   │  │
│  │  4. NodeState CR (Watches) ← 新增                                                 │  │
│  │     - 映射: NodeState → BKEMachine (通过 label "bke.bocloud.com/machine")          │  │
│  │     - 过滤: Phase 变更                                                             │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 四、clusterctl 集成契约

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  clusterctl 集成契约                                                                    │
│                                                                                         │
│  1. metadata.yaml (必须)                                                                │
│     - 定义 Provider 版本信息、CRD 列表、组件清单                                        │
│     - clusterctl init --infrastructure bke 读取此文件                                    │
│                                                                                         │
│  2. cluster-template.yaml (推荐)                                                        │
│     - 定义集群创建模板                                                                  │
│     - clusterctl generate cluster 使用此模板                                            │
│                                                                                         │
│  3. cluster-class-template.yaml (推荐)                                                  │
│     - 定义 ClusterClass 模板                                                            │
│     - 支持拓扑模式 (Cluster.spec.topology)                                              │
│                                                                                         │
│  4. CRD 标签 (必须)                                                                     │
│     - 所有 CRD 必须有:                                                                  │
│       cluster.x-k8s.io/provider: infrastructure-bke                                     │
│       cluster.x-k8s.io/v1beta1: v1beta1                                                │
│                                                                                         │
│  5. Manager 容器标签 (必须)                                                              │
│     - Provider Manager Pod 必须有标签:                                                   │
│       cluster.x-k8s.io/provider: infrastructure-bke                                     │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 五、BKE 当前实现的完整差距清单

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  差距清单 (按严重程度排序)                                                               │
│                                                                                         │
│  🔴 P0 — 违反 CAPI 契约，导致 CAPI Core 无法正确工作                                    │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  1. BKECluster 缺少 GetConditions()/SetConditions()                               │  │
│  │     影响: CAPI Core 无法通过 conditions.Getter/Setter 接口操作 BKECluster         │  │
│  │     修复: 实现 clusterv1.Conditions 类型的 status.conditions 字段                  │  │
│  │                                                                                   │  │
│  │  2. BKECluster.status.conditions 类型不兼容                                       │  │
│  │     当前: ClusterConditions (自定义类型)                                           │  │
│  │     要求: clusterv1.Conditions (CAPI 标准类型)                                    │  │
│  │     影响: 无法与 CAPI conditions 工具函数 (MarkTrue/MarkFalse/IsTrue) 协同        │  │
│  │     修复: 将 ClusterConditions 替换为 clusterv1.Conditions                        │  │
│  │                                                                                   │  │
│  │  3. BKEMachineReconciler 越权操作 KubeadmControlPlane / KubeadmConfig            │  │
│  │     当前: syncKubeadmConfig() 直接修改 KubeadmConfig                              │  │
│  │     契约: Infrastructure Provider 不应操作其他 Provider 的资源                     │  │
│  │     影响: 与 KCP Controller / Bootstrap Controller 产生竞争条件                   │  │
│  │     修复: 删除 syncKubeadmConfig()，让 KCP Controller 自行管理                    │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  🟡 P1 — 不违反契约但影响 CAPI 生态兼容性                                               │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  4. BKECluster 缺少 status.failureDomains                                        │  │
│  │     影响: CAPI 调度器无法感知可用区信息，Machine 无法按拓扑分布                     │  │
│  │     修复: 添加 FailureDomains 字段，从节点信息中提取                               │  │
│  │                                                                                   │  │
│  │  5. BKEMachine 缺少 spec.nodeDrainTimeout / spec.nodeVolumeDetachTimeout         │  │
│  │     影响: 删除节点时无法控制 Drain 行为                                            │  │
│  │     修复: 添加字段并在 reconcileDelete 中使用                                      │  │
│  │                                                                                   │  │
│  │  6. 缺少 clusterctl 集成文件                                                      │  │
│  │     影响: 无法通过 clusterctl init --infrastructure bke 安装                       │  │
│  │     修复: 创建 metadata.yaml + cluster-template.yaml                              │  │
│  │                                                                                   │  │
│  │  7. BKECluster 使用自定义 Condition 类型而非 CAPI 标准 Condition                   │  │
│  │     当前: ControlPlaneEndPointSetCondition, TargetClusterReadyCondition 等         │  │
│  │     问题: 这些 Condition 不在 CAPI 标准列表中，clusterctl/CAPI visualizer 不识别   │  │
│  │     修复: 保留自定义 Condition 但同时设置 CAPI 标准 Condition (Ready)              │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  🟢 P2 — 建议改进，提升 CAPI 生态一致性                                                 │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │  8. BKEMachine 缺少 status.nodeRef                                               │  │
│  │     影响: CAPI 无法直接从 BKEMachine 追踪到远端 Node                               │  │
│  │                                                                                   │  │
│  │  9. 缺少 ClusterClass 支持                                                        │  │
│  │     影响: 无法使用 CAPI 拓扑模式 (Cluster.spec.topology)                          │  │
│  │                                                                                   │  │
│  │  10. 缺少 MachinePool 支持                                                        │  │
│  │      影响: 无法使用 CAPI MachinePool 模式                                         │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 六、修复方案：BKECluster Conditions 类型对齐

这是最关键的 P0 修复。需要将自定义 `ClusterConditions` 替换为 CAPI 标准的 `clusterv1.Conditions`：

```go
// api/bkecommon/v1beta1/bkecluster_status.go (修复后)

type BKEClusterStatus struct {
    Ready bool `json:"ready"`
    
    OpenFuyaoVersion string `json:"openFuyaoVersion,omitempty"`
    KubernetesVersion string `json:"kubernetesVersion,omitempty"`
    EtcdVersion string `json:"etcdVersion,omitempty"`
    ContainerdVersion string `json:"containerdVersion,omitempty"`
    AgentStatus BKEAgentStatus `json:"agentStatus"`
    Phase BKEClusterPhase `json:"phase,omitempty"`
    ClusterStatus ClusterStatus `json:"clusterStatus,omitempty"`
    ClusterHealthState ClusterHealthState `json:"clusterHealthState,omitempty"`
    AddonStatus []Product `json:"addonStatus,omitempty"`
    PhaseStatus PhaseStatus `json:"phaseStatus,omitempty"`
    
    // 修复: 替换 ClusterConditions 为 clusterv1.Conditions
    Conditions clusterv1.Conditions `json:"conditions,omitempty"`
    
    // 新增: CAPI 标准字段
    FailureDomains clusterv1.FailureDomains `json:"failureDomains,omitempty"`
}
```

```go
// api/capbke/v1beta1/bkecluster_types.go (修复后)

// GetConditions returns the set of conditions for this object.
func (c *BKECluster) GetConditions() clusterv1.Conditions {
    return c.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (c *BKECluster) SetConditions(conditions clusterv1.Conditions) {
    c.Status.Conditions = conditions
}
```

**迁移策略**：保留自定义 Condition 类型（如 `TargetClusterReadyCondition`）作为**附加** Condition，同时确保 CAPI 标准 Condition（`Ready`）始终被正确设置：

```go
// BKEClusterReconciler 中同时设置两种 Condition

// CAPI 标准 Condition — 必须
conditions.MarkTrue(bkeCluster, clusterv1.ReadyCondition)

// BKE 自定义 Condition — 保留用于内部逻辑
condition.ConditionMark(bkeCluster, bkev1beta1.TargetClusterReadyCondition, 
    confv1beta1.ConditionTrue, "", "")
```

### 七、完整契约遵守检查表

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  Infrastructure Provider 契约检查表                                                      │
│                                                                                         │
│  CRD 定义:                                                                              │
│  [✅] InfraCluster CRD (BKECluster)                                                     │
│  [✅] InfraMachine CRD (BKEMachine)                                                     │
│  [✅] InfraMachineTemplate CRD (BKEMachineTemplate)                                     │
│  [✅] InfraClusterTemplate CRD (BKEClusterTemplate)                                     │
│  [✅] provider 标签: cluster.x-k8s.io/provider=infrastructure-bke                       │
│  [✅] 版本标签: cluster.x-k8s.io/v1beta1=v1beta1                                       │
│                                                                                         │
│  BKECluster 字段:                                                                       │
│  [✅] spec.controlPlaneEndpoint                                                         │
│  [✅] status.ready                                                                      │
│  [❌] status.failureDomains — 缺失                                                     │
│  [❌] GetConditions() — 缺失                                                           │
│  [❌] SetConditions() — 缺失                                                           │
│  [❌] status.conditions 类型 — 不兼容                                                  │
│                                                                                         │
│  BKEMachine 字段:                                                                       │
│  [✅] spec.providerID                                                                   │
│  [✅] status.ready                                                                      │
│  [✅] status.addresses                                                                  │
│  [✅] GetConditions()                                                                   │
│  [✅] SetConditions()                                                                   │
│  [✅] status.conditions 类型兼容                                                        │
│                                                                                         │
│  Controller 行为:                                                                       │
│  [✅] OwnerRef → Cluster/Machine                                                        │
│  [✅] Finalizer 管理                                                                    │
│  [✅] Paused 检查                                                                       │
│  [✅] InfrastructureReady 检查                                                          │
│  [❌] 不越权操作其他 Provider 资源 — syncKubeadmConfig 越权                             │
│  [✅] 删除时清理资源                                                                     │
│                                                                                         │
│  Watch 链路:                                                                            │
│  [✅] BKECluster ← Cluster (UnPause)                                                   │
│  [✅] BKEMachine ← Machine (InfrastructureRef)                                         │
│  [✅] BKEMachine ← Cluster                                                              │
│  [❌] BKECluster ← NodeState (新增)                                                    │
│  [❌] BKEMachine ← NodeState (新增)                                                    │
│                                                                                         │
│  clusterctl 集成:                                                                       │
│  [❌] metadata.yaml — 缺失                                                             │
│  [❌] cluster-template.yaml — 缺失                                                     │
│  [❌] ClusterClass 支持 — 缺失                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 八、总结

开发 CAPI Provider 需要遵从的核心契约可以归纳为三个层次：

1. **类型契约**：CRD 字段必须包含 CAPI Core 读取的特定字段（`status.ready`、`spec.providerID`、`status.conditions` 等），且 `conditions` 必须使用 `clusterv1.Conditions` 类型并实现 `Getter/Setter` 接口

2. **行为契约**：Controller 的 Reconcile 逻辑必须遵循 CAPI 定义的生命周期流程（OwnerRef → Finalizer → Paused → Reconcile → Conditions），且**不能越权操作其他 Provider 的资源**

3. **生态契约**：必须提供 `clusterctl` 集成文件（`metadata.yaml`、`cluster-template.yaml`），CRD 必须携带标准标签，才能被 CAPI 工具链正确识别和安装

当前 BKE Provider 最紧迫的修复是 **P0 级别的三个问题**：补全 `GetConditions()/SetConditions()` 接口、对齐 `status.conditions` 类型为 `clusterv1.Conditions`、删除越权操作 `KubeadmConfig` 的代码。
        

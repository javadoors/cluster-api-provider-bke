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
6. **集群层驱动节点层**：集群层状态机通过 DAG 驱动节点层状态机执行

### 1.2 设计约束

| 约束 | 说明 |
|------|------|
| **节点级组件相邻** | 在 DAG 中，节点级组件聚合为一个"节点组"节点，保持 DAG 拓扑简洁 |
| **节点级并行** | 节点组节点执行时，所有节点并行执行各自的组件状态机 |
| **集群层驱动** | 集群层状态机通过 DAG 的 node-group 节点驱动节点层状态机执行 |
| **幂等性** | 所有状态转换和操作必须幂等，支持 Reconcile 重入 |
| **CAPI 兼容** | 完全遵循 Cluster API 的 Controller 模式

### 1.3 与 v4 的核心区别

| 维度 | v4 设计 | v5 设计 |
|------|--------|--------|
| **节点级状态机执行位置** | BKECluster Controller 直接执行 | 集群层通过 DAG 的 node-group 节点驱动 |
| **节点组节点职责** | 直接执行节点级状态机 | 创建/更新 BKEMachine + 等待节点状态机完成 |
| **节点状态追踪** | BKECluster.Status.NodeStatuses | BKEMachine.Status（由 BKEMachine Controller 更新） |
| **组件状态追踪** | BKECluster.Status.ComponentStatuses | BKEMachine.Status.ComponentStatuses |
| **CAPI 集成深度** | 浅层集成 | 深度集成，完全遵循 CAPI 模式 |
| **驱动方式** | 直接调用 | 通过 BKEMachine 资源协调 |

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
│  │  │ • 集群级组件直接执行             │  │ • 节点级组件执行               │  │   │
│  │  │ • node-group 驱动节点状态机      │  │ • 组件状态追踪               │  │   │
│  │  │                                  │  │                              │  │   │
│  │  │ 输入: BKECluster                 │  │ 输入: BKEMachine             │  │   │
│  │  │ 输出: BKECluster.Status          │  │ 输出: BKEMachine.Status      │  │   │
│  │  │       + BKEMachine 创建/更新     │  │       + 组件执行结果         │  │   │
│  │  │       + 等待 BKEMachine 完成     │  │                              │  │   │
│  │  └──────────────────────────────────┘  └──────────────────────────────┘  │   │
│  │                    │                                │                     │   │
│  │                    │ 创建/更新 BKEMachine           │                     │   │
│  │                    │ + 等待 Status 就绪             │                     │   │
│  │                    └────────────────────────────────┘                     │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**核心设计**：集群层状态机通过 DAG 的 node-group 节点驱动节点层状态机执行。node-group 节点负责：
1. 创建/更新 BKEMachine 资源（写入 Spec.NodeComponents）
2. 等待 BKEMachine Controller 完成节点层状态机执行
3. 读取 BKEMachine.Status 获取节点状态，聚合到集群状态

### 2.2 ReleaseImage 结构设计

**核心设计**：ReleaseImage 明确分离集群级组件和节点级组件，节点级组件携带依赖关系，传递给 BKEMachine 驱动 L2/L3 状态机。

```yaml
# ReleaseImage 结构设计
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.7.0
spec:
  version: "v2.7.0"
  
  # ============================================================
  # 集群级组件：由 BKECluster Controller 通过 DAG 直接执行
  # 这些组件在整个集群中只执行一次
  # ============================================================
  clusterComponents:
    - name: certs
      version: "v2.7.0"
      dependencies: []                    # 无依赖，最先执行
    
    - name: coredns
      version: "v1.11.1"
      dependencies: [certs]               # 依赖证书
    
    - name: kube-proxy
      version: "v2.7.0"
      dependencies: [certs]               # 依赖证书
    
    - name: bocoperator
      version: "v2.7.0"
      dependencies: [certs]
    
    - name: cluster-api
      version: "v1.5.0"
      dependencies: [certs]
  
  # ============================================================
  # 节点级组件：传递给 BKEMachine，由 BKEMachine Controller 驱动
  # 这些组件在每个节点上独立执行，携带依赖关系
  # ============================================================
  nodeComponents:
    - name: bkeagent
      version: "v2.7.0"
      dependencies: []                    # 无依赖，最先执行
      roles: [master, worker]             # 适用角色
    
    - name: containerd
      version: "v1.7.18"
      dependencies: [bkeagent]            # 依赖 bkeagent
      roles: [master, worker]
    
    - name: kubelet
      version: "v1.29.0"
      dependencies: [containerd]          # 依赖 containerd
      roles: [master, worker]
    
    - name: kubectl
      version: "v1.29.0"
      dependencies: [kubelet]             # 依赖 kubelet
      roles: [master, worker]
    
    # Master 特有组件
    - name: etcd
      version: "v3.5.21-of.1"
      dependencies: [kubelet]             # 依赖 kubelet
      roles: [master]                     # 仅 Master 节点
    
    - name: apiserver
      version: "v1.29.0"
      dependencies: [etcd]                # 依赖 etcd
      roles: [master]
    
    - name: controller-manager
      version: "v1.29.0"
      dependencies: [apiserver]           # 依赖 apiserver
      roles: [master]
    
    - name: scheduler
      version: "v1.29.0"
      dependencies: [apiserver]           # 依赖 apiserver
      roles: [master]
```

**数据流向**：

```
ReleaseImage
  │
  ├─ clusterComponents ──────────────────────────────────────────────────┐
  │   (certs, coredns, kube-proxy, bocoperator, cluster-api)            │
  │                                                                      │
  │   直接传递给 BKECluster Controller                                   │
  │                                                                      │
  │   BKECluster Controller:                                             │
  │   ┌────────┐   ┌─────────┐   ┌────────────┐   ┌────────────┐       │
  │   │ certs  │──>│ coredns │   │ kube-proxy │   │ bocoperator│       │
  │   │        │──>│         │   │            │   │            │       │
  │   └────────┘   └─────────┘   └────────────┘   └────────────┘       │
  │   (DAG 直接执行 L3 组件状态机)                                       │
  │                                                                      │
  ├─ nodeComponents ───────────────────────────────────────────────────┐ │
  │   (bkeagent, containerd, kubelet, kubectl, etcd, apiserver, ...)  │ │
  │                                                                    │ │
  │   按 roles 过滤，写入 BKEMachine.Spec                              │ │
  │                                                                    │ │
  │   ┌─────────────────────────────────────────────────────────────┐ │ │
  │   │ BKEMachine (master-1) Spec.NodeComponents:                  │ │ │
  │   │   bkeagent → containerd → kubelet → kubectl                 │ │ │
  │   │                            └→ etcd → apiserver              │ │ │
  │   │                                      └→ controller-manager  │ │ │
  │   │                                      └→ scheduler           │ │ │
  │   └─────────────────────────────────────────────────────────────┘ │ │
  │                                                                    │ │
  │   ┌─────────────────────────────────────────────────────────────┐ │ │
  │   │ BKEMachine (worker-1) Spec.NodeComponents:                  │ │ │
  │   │   bkeagent → containerd → kubelet → kubectl                 │ │ │
  │   │   (无 etcd/apiserver/controller-manager/scheduler)          │ │ │
  │   └─────────────────────────────────────────────────────────────┘ │ │
  │                                                                    │ │
  │   BKEMachine Controller:                                           │ │
  │   按依赖顺序驱动 L2 节点层 + L3 组件层状态机                         │ │
  │                                                                    │ │
  └────────────────────────────────────────────────────────────────────┘ │
  └──────────────────────────────────────────────────────────────────────┘
```

### 2.3 DAG 结构设计

**核心设计**：DAG 中只包含集群级组件和 node-group 节点。node-group 节点负责将 nodeComponents 写入 BKEMachine.Spec，BKEMachine Controller 驱动节点级组件执行。

```
ReleaseImage.clusterComponents:
  - certs, coredns, kube-proxy, bocoperator, cluster-api

构建后的集群级 DAG:

  ┌────────┐   ┌─────────┐   ┌────────────┐   ┌────────────┐
  │ certs  │──>│ coredns │   │ kube-proxy │   │ bocoperator│
  │        │──>│         │   │            │   │            │
  └──┬─────┘   └─────────┘   └────────────┘   └────────────┘
     │
     ├──> ┌──────────────┐   ┌────────────┐
     │    │ cluster-api  │   │ node-group │
     │    │              │   │            │
     └──> │              │   │ 创建/更新   │
          └──────────────┘   │ BKEMachine │
                             └─────┬──────┘
                                   │
                                   │ 写入 nodeComponents
                                   ▼
                     ┌─────────────────────────────────┐
                     │  BKEMachine Controller           │
                     │  ─────────────────────────       │
                     │  读取 BKEMachine.Spec            │
                     │  .NodeComponents                 │
                     │                                  │
                     │  驱动 L2 节点层状态机             │
                     │  按依赖顺序驱动 L3 组件层状态机   │
                     │                                  │
                     │  master-1 组件执行顺序:           │
                     │  bkeagent → containerd → kubelet │
                     │                 └→ etcd          │
                     │                       └→ apiserver
                     │                             └→ cm, scheduler
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

### 4.1 ReleaseImage 类型定义

```go
// ReleaseImage 发布镜像定义
type ReleaseImage struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec ReleaseImageSpec `json:"spec"`
}

type ReleaseImageSpec struct {
    // 版本号
    Version string `json:"version"`
    
    // 集群级组件列表：由 BKECluster Controller 通过 DAG 直接执行
    // 这些组件在整个集群中只执行一次
    ClusterComponents []ReleaseComponent `json:"clusterComponents,omitempty"`
    
    // 节点级组件列表：传递给 BKEMachine，由 BKEMachine Controller 驱动执行
    // 这些组件在每个节点上独立执行，携带依赖关系
    NodeComponents []ReleaseNodeComponent `json:"nodeComponents,omitempty"`
}

// ReleaseComponent 集群级组件引用
type ReleaseComponent struct {
    // 组件名称（对应 ComponentVersion.Name）
    Name string `json:"name"`
    
    // 组件版本（对应 ComponentVersion.Version）
    Version string `json:"version"`
    
    // 依赖的组件名称列表（仅集群级组件之间的依赖）
    Dependencies []string `json:"dependencies,omitempty"`
}

// ReleaseNodeComponent 节点级组件引用
type ReleaseNodeComponent struct {
    // 组件名称（对应 ComponentVersion.Name）
    Name string `json:"name"`
    
    // 组件版本（对应 ComponentVersion.Version）
    Version string `json:"version"`
    
    // 依赖的组件名称列表（节点级组件之间的依赖）
    Dependencies []string `json:"dependencies,omitempty"`
    
    // 适用角色：master / worker / etcd
    // 空表示所有角色
    Roles []string `json:"roles,omitempty"`
}

// ReleaseImage 完整 YAML 示例：
// apiVersion: cvo.openfuyao.cn/v1alpha1
// kind: ReleaseImage
// metadata:
//   name: openfuyao-v2.7.0
// spec:
//   version: "v2.7.0"
//   clusterComponents:
//     - name: certs
//       version: "v2.7.0"
//       dependencies: []
//     - name: coredns
//       version: "v1.11.1"
//       dependencies: [certs]
//     - name: kube-proxy
//       version: "v2.7.0"
//       dependencies: [certs]
//   nodeComponents:
//     - name: bkeagent
//       version: "v2.7.0"
//       dependencies: []
//       roles: [master, worker]
//     - name: containerd
//       version: "v1.7.18"
//       dependencies: [bkeagent]
//       roles: [master, worker]
//     - name: kubelet
//       version: "v1.29.0"
//       dependencies: [containerd]
//       roles: [master, worker]
//     - name: etcd
//       version: "v3.5.21-of.1"
//       dependencies: [kubelet]
//       roles: [master]
//     - name: apiserver
//       version: "v1.29.0"
//       dependencies: [etcd]
//       roles: [master]
```

### 4.2 BKEMachine Spec 扩展

```go
// BKEMachineSpec 扩展：携带节点级组件列表
type BKEMachineSpec struct {
    // CAPI 标准字段
    InfrastructureRef corev1.ObjectReference `json:"infrastructureRef"`
    Bootstrap         clusterv1.Bootstrap    `json:"bootstrap"`
    
    // 节点角色：master / worker
    Role string `json:"role,omitempty"`
    
    // 节点级组件列表：从 ReleaseImage.NodeComponents 按角色过滤后写入
    // BKEMachine Controller 读取此列表驱动 L2/L3 状态机
    NodeComponents []NodeComponentSpec `json:"nodeComponents,omitempty"`
}

// NodeComponentSpec 节点组件规格
type NodeComponentSpec struct {
    // 组件名称
    Name string `json:"name"`
    
    // 组件版本
    Version string `json:"version"`
    
    // 依赖的组件名称列表
    Dependencies []string `json:"dependencies,omitempty"`
}

// BKEMachineStatus 扩展节点状态
type BKEMachineStatus struct {
    // 节点生命周期阶段 (L2)
    LifecyclePhase NodeLifecyclePhase `json:"lifecyclePhase,omitempty"`
    
    // 组件状态列表 (L3)：与 Spec.NodeComponents 一一对应
    ComponentStatuses []ComponentLifecycleStatus `json:"componentStatuses,omitempty"`
    
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

**数据流转关系**：

```
ReleaseImage.Spec.NodeComponents (全量节点级组件)
    │
    │ 按 node.Spec.Role 过滤
    ▼
BKEMachine.Spec.NodeComponents (该节点需要的组件)
    │
    │ BKEMachine Controller 读取
    ▼
BKEMachine.Status.ComponentStatuses (每个组件的执行状态)
    │
    │ 聚合
    ▼
BKEMachine.Status.LifecyclePhase (L2 节点层状态)
```

**示例**：

```yaml
# ReleaseImage 中的节点级组件（全量）
spec:
  nodeComponents:
    - name: bkeagent
      version: "v2.7.0"
      dependencies: []
      roles: [master, worker]
    - name: containerd
      version: "v1.7.18"
      dependencies: [bkeagent]
      roles: [master, worker]
    - name: kubelet
      version: "v1.29.0"
      dependencies: [containerd]
      roles: [master, worker]
    - name: etcd
      version: "v3.5.21-of.1"
      dependencies: [kubelet]
      roles: [master]
    - name: apiserver
      version: "v1.29.0"
      dependencies: [etcd]
      roles: [master]

---
# Master 节点的 BKEMachine（过滤后）
spec:
  role: master
  nodeComponents:
    - name: bkeagent
      version: "v2.7.0"
      dependencies: []
    - name: containerd
      version: "v1.7.18"
      dependencies: [bkeagent]
    - name: kubelet
      version: "v1.29.0"
      dependencies: [containerd]
    - name: etcd
      version: "v3.5.21-of.1"
      dependencies: [kubelet]
    - name: apiserver
      version: "v1.29.0"
      dependencies: [etcd]

status:
  lifecyclePhase: Ready
  componentStatuses:
    - name: bkeagent
      version: "v2.7.0"
      phase: Installed
    - name: containerd
      version: "v1.7.18"
      phase: Installed
    - name: kubelet
      version: "v1.29.0"
      phase: Installed
    - name: etcd
      version: "v3.5.21-of.1"
      phase: Installed
    - name: apiserver
      version: "v1.29.0"
      phase: Installed

---
# Worker 节点的 BKEMachine（过滤后，无 etcd/apiserver）
spec:
  role: worker
  nodeComponents:
    - name: bkeagent
      version: "v2.7.0"
      dependencies: []
    - name: containerd
      version: "v1.7.18"
      dependencies: [bkeagent]
    - name: kubelet
      version: "v1.29.0"
      dependencies: [containerd]

status:
  lifecyclePhase: Ready
  componentStatuses:
    - name: bkeagent
      version: "v2.7.0"
      phase: Installed
    - name: containerd
      version: "v1.7.18"
      phase: Installed
    - name: kubelet
      version: "v1.29.0"
      phase: Installed
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
    
    // 4. 从 BKEMachine.Spec.NodeComponents 获取组件列表
    // 这些组件已经由 BKECluster Controller 按角色过滤后写入
    nodeComponents := machine.Spec.NodeComponents
    
    // 5. 初始化组件状态（如果尚未初始化）
    r.initComponentStatuses(machine, nodeComponents)
    
    // 6. 执行节点层状态机
    // 节点层状态机根据 Spec.NodeComponents 驱动 L2/L3 状态转换
    if err := r.nodeSM.Execute(ctx, machine, nodeComponents); err != nil {
        log.Error(err, "node state machine execution failed")
        r.Recorder.Eventf(machine, v1.EventTypeWarning, "NodeStateMachineFailed",
            "Node state machine failed: %v", err)
    }
    
    // 7. 设置 CAPI 标准条件
    setCAPIConditions(machine)
    
    // 8. 决定 Requeue
    return r.decideRequeue(machine), nil
}

// initComponentStatuses 初始化组件状态列表
// 确保 Status.ComponentStatuses 与 Spec.NodeComponents 一一对应
func (r *BKEMachineReconciler) initComponentStatuses(
    machine *bkev1beta1.BKEMachine,
    nodeComponents []bkev1beta1.NodeComponentSpec,
) {
    // 如果已初始化，跳过
    if len(machine.Status.ComponentStatuses) == len(nodeComponents) {
        return
    }
    
    // 初始化组件状态
    machine.Status.ComponentStatuses = make([]bkev1beta1.ComponentLifecycleStatus, len(nodeComponents))
    for i, comp := range nodeComponents {
        machine.Status.ComponentStatuses[i] = bkev1beta1.ComponentLifecycleStatus{
            Name:    comp.Name,
            Version: comp.Version,
            Phase:   CompPhasePending,
        }
    }
}
```

**NodeStateMachine 执行逻辑**：

```go
// NodeStateMachine 节点层状态机
type NodeStateMachine struct {
    componentSM *ComponentStateMachine
}

// Execute 执行节点层状态机
func (sm *NodeStateMachine) Execute(
    ctx context.Context,
    machine *bkev1beta1.BKEMachine,
    nodeComponents []bkev1beta1.NodeComponentSpec,
) error {
    // 1. 评估节点状态转换 (L2)
    oldPhase := machine.Status.LifecyclePhase
    newPhase := sm.evaluateNodePhase(machine, nodeComponents)
    
    if oldPhase != newPhase {
        machine.Status.LifecyclePhase = newPhase
    }
    
    // 2. 根据节点状态执行操作
    switch newPhase {
    case NodePhaseProvisioning, NodePhaseUpgrading:
        // 按依赖顺序执行节点级组件 (L3)
        return sm.executeNodeComponents(ctx, machine, nodeComponents)
    
    case NodePhaseDeleting:
        // 按依赖逆序卸载组件
        return sm.uninstallNodeComponents(ctx, machine, nodeComponents)
    }
    
    return nil
}

// evaluateNodePhase 评估节点层状态
func (sm *NodeStateMachine) evaluateNodePhase(
    machine *bkev1beta1.BKEMachine,
    nodeComponents []bkev1beta1.NodeComponentSpec,
) NodeLifecyclePhase {
    // 检查是否所有组件都已安装
    allInstalled := true
    hasUpgrading := false
    hasFailed := false
    
    for _, compStatus := range machine.Status.ComponentStatuses {
        switch compStatus.Phase {
        case CompPhaseInstalled:
            // 已安装
        case CompPhaseUpgrading, CompPhaseInstalling:
            hasUpgrading = true
            allInstalled = false
        case CompPhaseFailed:
            hasFailed = true
            allInstalled = false
        case CompPhasePending:
            allInstalled = false
        }
    }
    
    // 状态判断
    if hasFailed {
        return NodePhaseFailed
    }
    if hasUpgrading {
        return NodePhaseUpgrading
    }
    if allInstalled && len(machine.Status.ComponentStatuses) > 0 {
        return NodePhaseReady
    }
    if len(machine.Status.ComponentStatuses) > 0 {
        return NodePhaseProvisioning
    }
    
    return NodePhasePending
}

// executeNodeComponents 按依赖顺序执行节点级组件
func (sm *NodeStateMachine) executeNodeComponents(
    ctx context.Context,
    machine *bkev1beta1.BKEMachine,
    nodeComponents []bkev1beta1.NodeComponentSpec,
) error {
    // 1. 按依赖关系拓扑排序
    sorted, err := topologicalSortComponents(nodeComponents)
    if err != nil {
        return fmt.Errorf("failed to sort components: %w", err)
    }
    
    // 2. 按顺序执行每个组件
    for _, comp := range sorted {
        // 查找对应的组件状态
        compStatus := sm.findComponentStatus(machine, comp.Name)
        if compStatus == nil {
            continue
        }
        
        // 跳过已完成的组件
        if compStatus.Phase == CompPhaseInstalled {
            continue
        }
        
        // 检查依赖是否满足
        if !sm.dependenciesSatisfied(machine, comp.Dependencies) {
            continue // 等待依赖完成
        }
        
        // 获取 ComponentVersion
        cv, err := sm.lookupComponentVersion(ctx, comp.Name, comp.Version)
        if err != nil {
            return fmt.Errorf("failed to lookup component %s: %w", comp.Name, err)
        }
        
        // 执行组件层状态机 (L3)
        if err := sm.componentSM.Execute(ctx, compStatus, cv); err != nil {
            return fmt.Errorf("component %s failed: %w", comp.Name, err)
        }
    }
    
    return nil
}

// topologicalSortComponents 按依赖关系拓扑排序
func topologicalSortComponents(components []bkev1beta1.NodeComponentSpec) ([]bkev1beta1.NodeComponentSpec, error) {
    // 构建依赖图
    graph := make(map[string][]string)
    inDegree := make(map[string]int)
    compMap := make(map[string]bkev1beta1.NodeComponentSpec)
    
    for _, comp := range components {
        compMap[comp.Name] = comp
        if _, ok := inDegree[comp.Name]; !ok {
            inDegree[comp.Name] = 0
        }
        for _, dep := range comp.Dependencies {
            graph[dep] = append(graph[dep], comp.Name)
            inDegree[comp.Name]++
        }
    }
    
    // Kahn 算法
    var queue []string
    for name, degree := range inDegree {
        if degree == 0 {
            queue = append(queue, name)
        }
    }
    
    var sorted []bkev1beta1.NodeComponentSpec
    for len(queue) > 0 {
        name := queue[0]
        queue = queue[1:]
        sorted = append(sorted, compMap[name])
        
        for _, dependent := range graph[name] {
            inDegree[dependent]--
            if inDegree[dependent] == 0 {
                queue = append(queue, dependent)
            }
        }
    }
    
    if len(sorted) != len(components) {
        return nil, fmt.Errorf("circular dependency detected")
    }
    
    return sorted, nil
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
    
    // 1. 添加集群级组件节点
    for _, comp := range releaseImage.Spec.ClusterComponents {
        cv := lookupComponentVersion(comp.Name, comp.Version)
        dag.AddNode(&ClusterComponentNode{
            name:      comp.Name,
            component: cv,
            deps:      comp.Dependencies,
        })
    }
    
    // 2. 添加节点组节点（驱动节点层状态机执行）
    if len(releaseImage.Spec.NodeComponents) > 0 {
        dag.AddNode(&NodeGroupNode{
            name:           "node-group",
            nodeComponents: releaseImage.Spec.NodeComponents,
            deps:           collectNodeGroupDeps(releaseImage),
        })
    }
    
    // 3. 构建依赖边
    dag.BuildEdges()
    
    return dag, nil
}

// NodeGroupNode 节点组节点
// 职责：创建/更新 BKEMachine，并驱动节点层状态机执行
type NodeGroupNode struct {
    name           string
    nodeComponents []ReleaseNodeComponent  // 全量节点级组件
    deps           []string
}

// Execute 创建/更新 BKEMachine，并等待节点层状态机完成
func (n *NodeGroupNode) Execute(ctx context.Context, execCtx *ExecutionContext) error {
    nodes := execCtx.GetAllNodes()
    
    // 1. 为每个节点创建/更新 BKEMachine
    for _, node := range nodes {
        // 按节点角色过滤 nodeComponents
        filteredComponents := filterNodeComponentsByRole(n.nodeComponents, node.Role)
        
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
                Role:           node.Role,
                NodeComponents: toNodeComponentSpecs(filteredComponents),
            },
        }
        
        // 创建或更新
        existing := &bkev1beta1.BKEMachine{}
        err := execCtx.Client.Get(ctx, client.ObjectKeyFromObject(machine), existing)
        if err != nil {
            if apierrors.IsNotFound(err) {
                if err := execCtx.Client.Create(ctx, machine); err != nil {
                    return err
                }
            } else {
                return err
            }
        } else {
            // 更新：只更新 NodeComponents（不覆盖其他字段）
            existing.Spec.NodeComponents = machine.Spec.NodeComponents
            if err := execCtx.Client.Update(ctx, existing); err != nil {
                return err
            }
        }
    }
    
    // 2. 等待所有 BKEMachine 的节点层状态机执行完成
    // BKEMachine Controller 会驱动 L2/L3 状态机，更新 BKEMachine.Status
    if err := n.waitForNodesReady(ctx, execCtx, nodes); err != nil {
        return fmt.Errorf("node state machine execution failed: %w", err)
    }
    
    // 3. 聚合节点状态到集群状态
    n.aggregateNodeStatuses(ctx, execCtx, nodes)
    
    return nil
}

// waitForNodesReady 等待所有节点的节点层状态机执行完成
func (n *NodeGroupNode) waitForNodesReady(
    ctx context.Context,
    execCtx *ExecutionContext,
    nodes []Node,
) error {
    // 轮询等待所有节点就绪
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()
    
    timeout := time.After(30 * time.Minute)
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-timeout:
            return fmt.Errorf("timeout waiting for nodes to be ready")
        case <-ticker.C:
            allReady := true
            var failedNodes []string
            
            for _, node := range nodes {
                machine := &bkev1beta1.BKEMachine{}
                err := execCtx.Client.Get(ctx, client.ObjectKey{
                    Namespace: execCtx.Cluster.Namespace,
                    Name:      node.Name,
                }, machine)
                if err != nil {
                    return err
                }
                
                switch machine.Status.LifecyclePhase {
                case NodePhaseReady:
                    // 节点就绪
                case NodePhaseFailed:
                    failedNodes = append(failedNodes, node.Name)
                default:
                    // 还在执行中
                    allReady = false
                }
            }
            
            if len(failedNodes) > 0 {
                return fmt.Errorf("nodes failed: %v", failedNodes)
            }
            
            if allReady {
                return nil
            }
        }
    }
}

// aggregateNodeStatuses 聚合节点状态到集群状态
func (n *NodeGroupNode) aggregateNodeStatuses(
    ctx context.Context,
    execCtx *ExecutionContext,
    nodes []Node,
) {
    for _, node := range nodes {
        machine := &bkev1beta1.BKEMachine{}
        err := execCtx.Client.Get(ctx, client.ObjectKey{
            Namespace: execCtx.Cluster.Namespace,
            Name:      node.Name,
        }, machine)
        if err != nil {
            continue
        }
        
        // 更新集群状态中的节点状态
        execCtx.Cluster.Status.NodeStatuses[node.Name] = NodeStatus{
            Phase:              machine.Status.LifecyclePhase,
            ComponentStatuses:  machine.Status.ComponentStatuses,
        }
    }
}

// filterNodeComponentsByRole 按角色过滤节点级组件
func filterNodeComponentsByRole(components []ReleaseNodeComponent, role string) []ReleaseNodeComponent {
    var filtered []ReleaseNodeComponent
    for _, comp := range components {
        if len(comp.Roles) == 0 {
            // 无角色限制，所有节点都需要
            filtered = append(filtered, comp)
            continue
        }
        for _, r := range comp.Roles {
            if r == role {
                filtered = append(filtered, comp)
                break
            }
        }
    }
    return filtered
}

// toNodeComponentSpecs 转换为 BKEMachine.Spec.NodeComponents 格式
func toNodeComponentSpecs(components []ReleaseNodeComponent) []bkev1beta1.NodeComponentSpec {
    specs := make([]bkev1beta1.NodeComponentSpec, len(components))
    for i, comp := range components {
        specs[i] = bkev1beta1.NodeComponentSpec{
            Name:         comp.Name,
            Version:      comp.Version,
            Dependencies: comp.Dependencies,
        }
    }
    return specs
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

**类型定义**：

```go
// ReleaseNodeComponent ReleaseImage 中的节点级组件定义
type ReleaseNodeComponent struct {
    // 组件名称（对应 ComponentVersion.Name）
    Name string `json:"name"`
    
    // 组件版本（对应 ComponentVersion.Version）
    Version string `json:"version"`
    
    // 依赖的组件名称列表（节点级组件之间的依赖）
    Dependencies []string `json:"dependencies,omitempty"`
    
    // 适用角色：master / worker / etcd
    // 空表示所有角色都需要
    Roles []string `json:"roles,omitempty"`
}

// NodeComponentSpec BKEMachine.Spec.NodeComponents 中的组件规格
// 这是 BKECluster Controller 按角色过滤后写入 BKEMachine 的格式
type NodeComponentSpec struct {
    // 组件名称
    Name string `json:"name"`
    
    // 组件版本
    Version string `json:"version"`
    
    // 依赖的组件名称列表
    Dependencies []string `json:"dependencies,omitempty"`
}

// BKEMachineSpec BKEMachine 规格
type BKEMachineSpec struct {
    // 节点角色：master / worker
    Role string `json:"role,omitempty"`
    
    // 节点级组件列表：由 BKECluster Controller 按角色过滤后写入
    // BKEMachine Controller 读取此列表驱动 L2/L3 状态机
    NodeComponents []NodeComponentSpec `json:"nodeComponents,omitempty"`
}

// BKEMachineStatus BKEMachine 状态
type BKEMachineStatus struct {
    // 节点生命周期阶段 (L2)
    LifecyclePhase NodeLifecyclePhase `json:"lifecyclePhase,omitempty"`
    
    // 组件状态列表 (L3)：与 Spec.NodeComponents 一一对应
    ComponentStatuses []ComponentLifecycleStatus `json:"componentStatuses,omitempty"`
    
    // 操作进度
    OperationProgress *NodeOperationProgress `json:"operationProgress,omitempty"`
    
    // CAPI 标准字段
    Ready bool `json:"ready"`
    
    // Conditions
    Conditions []metav1.Condition `json:"conditions,omitempty"`
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
```

**数据流示例**：

```
ReleaseImage.Spec.NodeComponents (全量节点级组件):
  - name: bkeagent
    version: "v2.7.0"
    dependencies: []
    roles: [master, worker]        # 所有角色都需要
  - name: containerd
    version: "v1.7.18"
    dependencies: [bkeagent]
    roles: [master, worker]        # 所有角色都需要
  - name: kubelet
    version: "v1.29.0"
    dependencies: [containerd]
    roles: [master, worker]        # 所有角色都需要
  - name: etcd
    version: "v3.5.21-of.1"
    dependencies: [kubelet]
    roles: [master]                # 仅 Master 节点
  - name: apiserver
    version: "v1.29.0"
    dependencies: [etcd]
    roles: [master]                # 仅 Master 节点

BKECluster Controller 执行 NodeGroupNode.Execute():
  │
  ├─ 遍历所有节点
  │   │
  │   ├─ Master 节点 (role="master"):
  │   │   │
  │   │   ├─ filterNodeComponentsByRole(components, "master")
  │   │   │   └─ 返回: [bkeagent, containerd, kubelet, etcd, apiserver]
  │   │   │
  │   │   ├─ toNodeComponentSpecs(filtered)
  │   │   │   └─ 转换为: [
  │   │   │        {Name: "bkeagent", Version: "v2.7.0", Dependencies: []},
  │   │   │        {Name: "containerd", Version: "v1.7.18", Dependencies: ["bkeagent"]},
  │   │   │        {Name: "kubelet", Version: "v1.29.0", Dependencies: ["containerd"]},
  │   │   │        {Name: "etcd", Version: "v3.5.21-of.1", Dependencies: ["kubelet"]},
  │   │   │        {Name: "apiserver", Version: "v1.29.0", Dependencies: ["etcd"]},
  │   │   │      ]
  │   │   │
  │   │   └─ 创建/更新 BKEMachine
  │   │       └─ BKEMachine.Spec.NodeComponents = [上述列表]
  │   │
  │   └─ Worker 节点 (role="worker"):
  │       │
  │       ├─ filterNodeComponentsByRole(components, "worker")
  │       │   └─ 返回: [bkeagent, containerd, kubelet]
  │       │      (etcd 和 apiserver 被过滤掉，因为 roles=[master])
  │       │
  │       ├─ toNodeComponentSpecs(filtered)
  │       │   └─ 转换为: [
  │       │        {Name: "bkeagent", Version: "v2.7.0", Dependencies: []},
  │       │        {Name: "containerd", Version: "v1.7.18", Dependencies: ["bkeagent"]},
  │       │        {Name: "kubelet", Version: "v1.29.0", Dependencies: ["containerd"]},
  │       │      ]
  │       │
  │       └─ 创建/更新 BKEMachine
  │           └─ BKEMachine.Spec.NodeComponents = [上述列表]
  │
  └─ 等待 BKEMachine Controller 完成节点层状态机
```

**BKEMachine Controller 读取并驱动状态机**：

```go
// BKEMachineReconciler BKEMachine 控制器
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
    
    // 4. 从 BKEMachine.Spec.NodeComponents 获取组件列表
    // 这些组件已经由 BKECluster Controller 按角色过滤后写入
    nodeComponents := machine.Spec.NodeComponents
    
    // 5. 初始化组件状态（如果尚未初始化）
    r.initComponentStatuses(machine, nodeComponents)
    
    // 6. 执行节点层状态机
    // 节点层状态机根据 Spec.NodeComponents 驱动 L2/L3 状态转换
    if err := r.nodeSM.Execute(ctx, machine, nodeComponents); err != nil {
        log.Error(err, "node state machine execution failed")
        r.Recorder.Eventf(machine, v1.EventTypeWarning, "NodeStateMachineFailed",
            "Node state machine failed: %v", err)
    }
    
    // 7. 设置 CAPI 标准条件
    setCAPIConditions(machine)
    
    // 8. 决定 Requeue
    return r.decideRequeue(machine), nil
}

// initComponentStatuses 初始化组件状态列表
// 确保 Status.ComponentStatuses 与 Spec.NodeComponents 一一对应
func (r *BKEMachineReconciler) initComponentStatuses(
    machine *bkev1beta1.BKEMachine,
    nodeComponents []bkev1beta1.NodeComponentSpec,
) {
    // 如果已初始化，跳过
    if len(machine.Status.ComponentStatuses) == len(nodeComponents) {
        return
    }
    
    // 初始化组件状态
    machine.Status.ComponentStatuses = make([]cvoapi.ComponentLifecycleStatus, len(nodeComponents))
    for i, comp := range nodeComponents {
        machine.Status.ComponentStatuses[i] = cvoapi.ComponentLifecycleStatus{
            Name:    comp.Name,
            Version: comp.Version,
            Phase:   cvoapi.CompPhasePending,
        }
    }
}
```

**NodeStateMachine 执行逻辑**：

```go
// NodeStateMachine 节点层状态机
type NodeStateMachine struct {
    componentSM *ComponentStateMachine
}

// Execute 执行节点层状态机
func (sm *NodeStateMachine) Execute(
    ctx context.Context,
    machine *bkev1beta1.BKEMachine,
    nodeComponents []bkev1beta1.NodeComponentSpec,
) error {
    // 1. 评估节点状态转换 (L2)
    oldPhase := machine.Status.LifecyclePhase
    newPhase := sm.evaluateNodePhase(machine, nodeComponents)
    
    if oldPhase != newPhase {
        machine.Status.LifecyclePhase = newPhase
    }
    
    // 2. 根据节点状态执行操作
    switch newPhase {
    case NodePhaseProvisioning, NodePhaseUpgrading:
        // 按依赖顺序执行节点级组件 (L3)
        return sm.executeNodeComponents(ctx, machine, nodeComponents)
    
    case NodePhaseDeleting:
        // 按依赖逆序卸载组件
        return sm.uninstallNodeComponents(ctx, machine, nodeComponents)
    }
    
    return nil
}

// evaluateNodePhase 评估节点层状态
func (sm *NodeStateMachine) evaluateNodePhase(
    machine *bkev1beta1.BKEMachine,
    nodeComponents []bkev1beta1.NodeComponentSpec,
) NodeLifecyclePhase {
    // 检查是否所有组件都已安装
    allInstalled := true
    hasUpgrading := false
    hasFailed := false
    
    for _, compStatus := range machine.Status.ComponentStatuses {
        switch compStatus.Phase {
        case cvoapi.CompPhaseInstalled:
            // 已安装
        case cvoapi.CompPhaseUpgrading, cvoapi.CompPhaseInstalling:
            hasUpgrading = true
            allInstalled = false
        case cvoapi.CompPhaseFailed:
            hasFailed = true
            allInstalled = false
        case cvoapi.CompPhasePending:
            allInstalled = false
        }
    }
    
    // 状态判断
    if hasFailed {
        return NodePhaseFailed
    }
    if hasUpgrading {
        return NodePhaseUpgrading
    }
    if allInstalled && len(machine.Status.ComponentStatuses) > 0 {
        return NodePhaseReady
    }
    if len(machine.Status.ComponentStatuses) > 0 {
        return NodePhaseProvisioning
    }
    
    return NodePhasePending
}

// executeNodeComponents 按依赖顺序执行节点级组件
func (sm *NodeStateMachine) executeNodeComponents(
    ctx context.Context,
    machine *bkev1beta1.BKEMachine,
    nodeComponents []bkev1beta1.NodeComponentSpec,
) error {
    // 1. 按依赖关系拓扑排序
    sorted, err := topologicalSortComponents(nodeComponents)
    if err != nil {
        return fmt.Errorf("failed to sort components: %w", err)
    }
    
    // 2. 按顺序执行每个组件
    for _, comp := range sorted {
        // 查找对应的组件状态
        compStatus := sm.findComponentStatus(machine, comp.Name)
        if compStatus == nil {
            continue
        }
        
        // 跳过已完成的组件
        if compStatus.Phase == cvoapi.CompPhaseInstalled {
            continue
        }
        
        // 检查依赖是否满足
        if !sm.dependenciesSatisfied(machine, comp.Dependencies) {
            continue // 等待依赖完成
        }
        
        // 获取 ComponentVersion
        cv, err := sm.lookupComponentVersion(ctx, comp.Name, comp.Version)
        if err != nil {
            return fmt.Errorf("failed to lookup component %s: %w", comp.Name, err)
        }
        
        // 执行组件层状态机 (L3)
        if err := sm.componentSM.Execute(ctx, compStatus, cv); err != nil {
            return fmt.Errorf("component %s failed: %w", comp.Name, err)
        }
    }
    
    return nil
}

// topologicalSortComponents 按依赖关系拓扑排序
func topologicalSortComponents(components []bkev1beta1.NodeComponentSpec) ([]bkev1beta1.NodeComponentSpec, error) {
    // 构建依赖图
    graph := make(map[string][]string)
    inDegree := make(map[string]int)
    compMap := make(map[string]bkev1beta1.NodeComponentSpec)
    
    for _, comp := range components {
        compMap[comp.Name] = comp
        if _, ok := inDegree[comp.Name]; !ok {
            inDegree[comp.Name] = 0
        }
        for _, dep := range comp.Dependencies {
            graph[dep] = append(graph[dep], comp.Name)
            inDegree[comp.Name]++
        }
    }
    
    // Kahn 算法
    var queue []string
    for name, degree := range inDegree {
        if degree == 0 {
            queue = append(queue, name)
        }
    }
    
    var sorted []bkev1beta1.NodeComponentSpec
    for len(queue) > 0 {
        name := queue[0]
        queue = queue[1:]
        sorted = append(sorted, compMap[name])
        
        for _, dependent := range graph[name] {
            inDegree[dependent]--
            if inDegree[dependent] == 0 {
                queue = append(queue, dependent)
            }
        }
    }
    
    if len(sorted) != len(components) {
        return nil, fmt.Errorf("circular dependency detected")
    }
    
    return sorted, nil
}
```

**执行流程**：

```
BKECluster Controller (L1 集群层状态机)
  │
  ├─ Build DAG
  │   ├─ 集群级组件节点: certs, coredns, kube-proxy
  │   └─ node-group 节点
  │
  ├─ Execute DAG
  │   │
  │   ├─ Batch 1: [certs]
  │   │   └─ ClusterComponentNode.Execute()
  │   │       └─ L3: certs Installing → Installed
  │   │
  │   ├─ Batch 2: [node-group]
  │   │   └─ NodeGroupNode.Execute()
  │   │       │
  │   │       ├─ 1. 创建/更新 BKEMachine (写入 Spec.NodeComponents)
  │   │       │   ├─ Master BKEMachine: [bkeagent, containerd, kubelet, etcd, apiserver]
  │   │       │   └─ Worker BKEMachine: [bkeagent, containerd, kubelet]
  │   │       │
  │   │       ├─ 2. 等待 BKEMachine Controller 完成节点层状态机
  │   │       │   │
  │   │       │   │  BKEMachine Controller (每个节点独立 Reconcile)
  │   │       │   │    ├─ 读取 Spec.NodeComponents
  │   │       │   │    ├─ 驱动 L2 节点层状态机
  │   │       │   │    ├─ 驱动 L3 组件层状态机
  │   │       │   │    │   ├─ bkeagent: Pending → Installing → Installed
  │   │       │   │    │   ├─ containerd: Pending → Installing → Installed
  │   │       │   │    │   └─ kubelet: Pending → Installing → Installed
  │   │       │   │    └─ 更新 BKEMachine.Status
  │   │       │   │
  │   │       │   └─ 轮询等待所有节点 Status.LifecyclePhase == Ready
  │   │       │
  │   │       └─ 3. 聚合节点状态到 BKECluster.Status.NodeStatuses
  │   │
  │   ├─ Batch 3: [coredns]
  │   │   └─ ClusterComponentNode.Execute()
  │   │       └─ L3: coredns Installing → Installed
  │   │
  │   └─ Batch 4: [kube-proxy]
  │       └─ ClusterComponentNode.Execute()
  │           └─ L3: kube-proxy Installing → Installed
  │
  └─ DAG 执行完成
      └─ BKECluster.Status.LifecyclePhase → Running
```

**关键设计**：

1. **集群层驱动节点层**：node-group 节点在 DAG 中执行时，会等待 BKEMachine Controller 完成节点层状态机执行

2. **轮询等待机制**：node-group 节点通过轮询 BKEMachine.Status 来等待节点层状态机完成

3. **状态聚合**：node-group 节点将 BKEMachine.Status 聚合到 BKECluster.Status.NodeStatuses

4. **BKEMachine Controller 独立执行**：BKEMachine Controller 独立驱动 L2/L3 状态机，与 BKECluster Controller 解耦

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
│  │    │       │                                                         │   │   │
│  │    │       ├─ Step 1: 创建/更新 BKEMachine                           │   │   │
│  │    │       │   ├─ Master BKEMachine: [bkeagent, containerd, ...]     │   │   │
│  │    │       │   └─ Worker BKEMachine: [bkeagent, containerd, ...]     │   │   │
│  │    │       │                                                         │   │   │
│  │    │       ├─ Step 2: 等待 BKEMachine Controller 完成节点层状态机     │   │   │
│  │    │       │   │                                                     │   │   │
│  │    │       │   │  BKEMachine Controller (每个节点独立 Reconcile)      │   │   │
│  │    │       │   │    ├─ 读取 Spec.NodeComponents                      │   │   │
│  │    │       │   │    ├─ 驱动 L2 节点层状态机                          │   │   │
│  │    │       │   │    ├─ 驱动 L3 组件层状态机                          │   │   │
│  │    │       │   │    │   ├─ bkeagent: Pending → Installing → Installed│   │   │
│  │    │       │   │    │   ├─ containerd: Pending → Installing → ...    │   │   │
│  │    │       │   │    │   └─ kubelet: Pending → Installing → ...       │   │   │
│  │    │       │   │    └─ 更新 BKEMachine.Status.LifecyclePhase=Ready   │   │   │
│  │    │       │   │                                                     │   │   │
│  │    │       │   └─ 轮询等待所有节点 Status.LifecyclePhase == Ready    │   │   │
│  │    │       │                                                         │   │   │
│  │    │       └─ Step 3: 聚合节点状态到 BKECluster.Status.NodeStatuses  │   │   │
│  │    │                                                                 │   │   │
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
│  │    └─ EvaluateClusterPhase: Installing → Running                         │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**关键设计**：

1. **集群层驱动节点层**：node-group 节点在 DAG 中执行时，会等待 BKEMachine Controller 完成节点层状态机执行

2. **轮询等待机制**：node-group 节点通过轮询 BKEMachine.Status 来等待节点层状态机完成

3. **状态聚合**：node-group 节点将 BKEMachine.Status 聚合到 BKECluster.Status.NodeStatuses

4. **BKEMachine Controller 独立执行**：BKEMachine Controller 独立驱动 L2/L3 状态机，与 BKECluster Controller 解耦

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
│  │           │                                                              │   │
│  │           ├─ Step 1: 更新 BKEMachine.Spec.NodeComponents (新版本)        │   │
│  │           │                                                              │   │
│  │           ├─ Step 2: 等待 BKEMachine Controller 完成节点层状态机          │   │
│  │           │   │                                                          │   │
│  │           │   │  BKEMachine Controller (每个节点独立 Reconcile)           │   │
│  │           │   │    ├─ 检测 Spec.NodeComponents 版本变更                  │   │
│  │           │   │    ├─ 驱动 L2 节点层状态机: Ready → Upgrading            │   │
│  │           │   │    ├─ 驱动 L3 组件层状态机                               │   │
│  │           │   │    │   ├─ containerd: Installed → Upgrading → Installed  │   │
│  │           │   │    │   └─ kubelet: Installed → Upgrading → Installed     │   │
│  │           │   │    └─ 更新 BKEMachine.Status.LifecyclePhase=Ready        │   │
│  │           │   │                                                          │   │
│  │           │   └─ 轮询等待所有节点 Status.LifecyclePhase == Ready         │   │
│  │           │                                                              │   │
│  │           └─ Step 3: 聚合节点状态到 BKECluster.Status.NodeStatuses       │   │
│  │                                                                          │   │
│  │ 4. DAG 执行完成                                                          │   │
│  │    └─ EvaluateClusterPhase: Upgrading → Running                          │   │
│  │    └─ 更新 currentVersion = "v2.7.0"                                     │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**关键设计**：

1. **版本变更检测**：BKEMachine Controller 检测 Spec.NodeComponents 版本变更，触发节点层状态机

2. **节点层驱动组件层**：BKEMachine Controller 按依赖顺序驱动 L3 组件层状态机

3. **状态聚合**：node-group 节点将 BKEMachine.Status 聚合到 BKECluster.Status

4. **集群层等待**：BKECluster Controller 等待所有节点就绪后，更新集群状态为 Running

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
| **集群层驱动节点层** | 集群层状态机通过 DAG 的 node-group 节点驱动节点层状态机执行 |
| **CAPI 深度集成** | 完全遵循 CAPI 的 Controller 模式，BKEMachine Controller 独立驱动节点层状态机 |
| **职责分离** | BKECluster Controller 负责集群层，BKEMachine Controller 负责节点层和组件层 |
| **并行执行** | 每个节点独立 Reconcile，天然支持并行 |
| **状态追踪** | 节点和组件状态存储在 BKEMachine.Status 中，符合 CAPI 规范 |
| **可观测** | 状态、事件、指标三层可观测，全链路可追踪 |

### 8.2 与 v4 的主要区别

| 维度 | v4 设计 | v5 设计 |
|------|--------|--------|
| **节点级状态机执行位置** | BKECluster Controller 直接执行 | 集群层通过 DAG 的 node-group 节点驱动 |
| **节点组节点职责** | 直接执行节点级状态机 | 创建/更新 BKEMachine + 等待节点状态机完成 |
| **节点状态追踪** | BKECluster.Status.NodeStatuses | BKEMachine.Status（由 BKEMachine Controller 更新） |
| **组件状态追踪** | BKECluster.Status.ComponentStatuses | BKEMachine.Status.ComponentStatuses |
| **CAPI 集成深度** | 浅层集成 | 深度集成，完全遵循 CAPI 模式 |
| **驱动方式** | 直接调用 | 通过 BKEMachine 资源协调 |

### 8.3 核心设计要点

1. **集群层驱动节点层**：node-group 节点在 DAG 中执行时，会等待 BKEMachine Controller 完成节点层状态机执行

2. **轮询等待机制**：node-group 节点通过轮询 BKEMachine.Status 来等待节点层状态机完成

3. **状态聚合**：node-group 节点将 BKEMachine.Status 聚合到 BKECluster.Status.NodeStatuses

4. **BKEMachine Controller 独立执行**：BKEMachine Controller 独立驱动 L2/L3 状态机，与 BKECluster Controller 解耦

---

**文档版本**: v5.1  
**维护者**: openFuyao Team

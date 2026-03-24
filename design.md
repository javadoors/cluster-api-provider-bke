


          
# Cluster API Provider BKE 概要设计

## 1. 项目概述

### 1.1 项目定位

Cluster API Provider BKE 是 Kubernetes Cluster API 的基础设施提供商实现，用于管理 BKE（Baidu Kubernetes Engine）集群的生命周期。

### 1.2 核心功能

| 功能 | 说明 |
|------|------|
| 集群生命周期管理 | 创建、更新、删除 Kubernetes 集群 |
| 节点管理 | 管理控制平面节点和工作节点 |
| 节点池管理 | 支持用户提供节点的池化管理 |
| 命令执行 | 通过 Agent 执行节点初始化命令 |
| 状态同步 | 同步集群和节点状态到管理集群 |

---

## 2. 整体架构

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Management Cluster                            │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                   Cluster API Core                            │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐              │   │
│  │  │  Cluster   │  │  Machine   │  │  Machine   │              │   │
│  │  │ Controller │  │ Controller │  │Deployment  │              │   │
│  │  └────────────┘  └────────────┘  └────────────┘              │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              │                                       │
│  ┌───────────────────────────┼───────────────────────────────────┐  │
│  │                  Cluster API Provider BKE                     │  │
│  │  ┌─────────────────────────────────────────────────────────┐  │  │
│  │  │                    BKECluster Controller                │  │  │
│  │  │  ┌─────────────────────────────────────────────────┐    │  │  │
│  │  │  │  Phase Flow Engine                               │    │  │  │
│  │  │  │  ├── InitPhase → ValidatePhase → PreparePhase   │    │  │  │
│  │  │  │  ├── InstallPhase → ConfigurePhase → VerifyPhase│    │  │  │
│  │  │  │  └── HealthCheckPhase → CompletePhase           │    │  │  │
│  │  │  └─────────────────────────────────────────────────┘    │  │  │
│  │  └─────────────────────────────────────────────────────────┘  │  │
│  │  ┌─────────────────────────────────────────────────────────┐  │  │
│  │  │                    BKEMachine Controller                │  │  │
│  │  │  ├── 节点初始化  ├── 状态同步  ├── 健康检查            │  │  │
│  │  └─────────────────────────────────────────────────────────┘  │  │
│  │  ┌─────────────────────────────────────────────────────────┐  │  │
│  │  │                  BKENodePool Controller                 │  │  │
│  │  │  ├── 节点分配  ├── 节点释放  ├── 健康检查              │  │  │
│  │  └─────────────────────────────────────────────────────────┘  │  │
│  │  ┌─────────────────────────────────────────────────────────┐  │  │
│  │  │                    Command Controller                   │  │  │
│  │  │  ├── 命令下发  ├── 执行监控  ├── 结果收集              │  │  │
│  │  └─────────────────────────────────────────────────────────┘  │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                              │                                       │
│                              │ SSH/Agent                             │
│                              ▼                                       │
└─────────────────────────────────────────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────────┐
│                        Workload Cluster                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐            │
│  │ Master-1 │  │ Master-2 │  │ Worker-1 │  │ Worker-2 │            │
│  │ (BKEAgent)│  │ (BKEAgent)│  │ (BKEAgent)│  │ (BKEAgent)│            │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘            │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 3. 核心 CRD

### 3.1 CRD 清单

| CRD | 作用 | 作用域 |
|-----|------|--------|
| BKECluster | 定义集群基础设施配置 | Namespaced |
| BKEMachine | 定义单个节点配置 | Namespaced |
| BKEMachineTemplate | 节点模板 | Namespaced |
| BKENodePool | 用户提供的节点池 | Namespaced |
| Command | 远程命令执行 | Namespaced |

### 3.2 BKECluster

```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: production-cluster
spec:
  # 控制平面端点
  controlPlaneEndpoint:
    host: api.cluster.example.com
    port: 6443
  
  # 集群配置
  clusterConfig:
    kubernetesVersion: v1.28.0
    networking:
      podsCIDR: 192.168.0.0/16
      servicesCIDR: 10.128.0.0/12
    nodes:
      - hostname: master-1
        ip: 10.0.1.1
        roles: [control-plane]
      - hostname: worker-1
        ip: 10.0.2.1
        roles: [worker]
status:
  phase: Running
  infrastructureReady: true
  controlPlaneReady: true
```

### 3.3 BKEMachine

```yaml
apiVersion: bke.bocloud.com/v1beta1
kind: BKEMachine
metadata:
  name: worker-1
spec:
  providerID: bke://worker-1
  pause: false
  dryRun: false
status:
  ready: true
  bootstrapped: true
  node:
    hostname: worker-1
    ip: 10.0.2.1
  addresses:
    - type: InternalIP
      address: 10.0.2.1
```

### 3.4 BKENodePool

```yaml
apiVersion: capbke.baiducfe.com/v1beta1
kind: BKENodePool
metadata:
  name: production-pool
spec:
  nodes:
    - name: node-1
      ip: 10.0.1.1
      username: root
      sshKeyRef:
        name: ssh-key
      labels:
        node-role: worker
  allocationStrategy: RoundRobin
  healthCheck:
    enabled: true
    interval: 30s
    checks:
      - type: SSH
status:
  totalNodes: 3
  availableNodes: 2
  inUseNodes: 1
```

---

## 4. 控制器设计

### 4.1 控制器清单

| 控制器 | 职责 |
|--------|------|
| BKEClusterReconciler | 管理集群生命周期，执行阶段流程 |
| BKEMachineReconciler | 管理单个节点，执行节点初始化 |
| BKENodePoolReconciler | 管理节点池，分配和释放节点 |
| CommandReconciler | 执行远程命令，收集执行结果 |

### 4.2 BKECluster 控制器流程

```
┌─────────────────────────────────────────────────────────────────────┐
│                    BKECluster Reconcile Flow                        │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐      │
│  │  Init    │───►│ Validate │───►│ Prepare  │───►│ Install  │      │
│  │  Phase   │    │  Phase   │    │  Phase   │    │  Phase   │      │
│  └──────────┘    └──────────┘    └──────────┘    └──────────┘      │
│       │                                                │            │
│       │                                                ▼            │
│       │         ┌──────────┐    ┌──────────┐    ┌──────────┐       │
│       │         │ Complete │◄───│  Verify  │◄───│Configure │       │
│       │         │  Phase   │    │  Phase   │    │  Phase   │       │
│       │         └──────────┘    └──────────┘    └──────────┘       │
│       │              │                                             │
│       │              ▼                                             │
│       │         ┌──────────┐                                      │
│       └────────►│ Health   │ ◄─── 定时健康检查                    │
│                 │  Check   │                                      │
│                 │  Phase   │                                      │
│                 └──────────┘                                      │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 4.3 BKEMachine 控制器流程

```
┌─────────────────────────────────────────────────────────────────────┐
│                    BKEMachine Reconcile Flow                        │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  1. 获取 BKEMachine 和关联的 Machine                                │
│              │                                                       │
│              ▼                                                       │
│  2. 检查 Cluster 是否就绪                                           │
│              │                                                       │
│              ▼                                                       │
│  3. 执行节点初始化                                                   │
│     ├── 生成 ProviderID                                             │
│     ├── 执行 Bootstrap 脚本                                         │
│     └── 等待节点加入集群                                            │
│              │                                                       │
│              ▼                                                       │
│  4. 更新节点状态                                                     │
│     ├── 设置 Addresses                                              │
│     ├── 设置 NodeRef                                                │
│     └── 标记 Ready                                                  │
│              │                                                       │
│              ▼                                                       │
│  5. 持续健康检查                                                     │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 5. 阶段流程引擎

### 5.1 阶段定义

| 阶段 | 说明 |
|------|------|
| InitPhase | 初始化集群状态和配置 |
| ValidatePhase | 验证集群配置和节点可达性 |
| PreparePhase | 准备安装环境和依赖 |
| InstallPhase | 安装 Kubernetes 组件 |
| ConfigurePhase | 配置集群网络、存储等 |
| VerifyPhase | 验证集群功能正常 |
| HealthCheckPhase | 持续健康检查 |
| CompletePhase | 完成安装，更新状态 |

### 5.2 阶段执行机制

```go
type Phase interface {
    Name() string
    Execute(ctx context.Context, params *PhaseParams) (*PhaseResult, error)
}

type PhaseFlow struct {
    phases []Phase
}

func (f *PhaseFlow) Execute(ctx context.Context, params *PhaseParams) error {
    for _, phase := range f.phases {
        result, err := phase.Execute(ctx, params)
        if err != nil {
            return fmt.Errorf("phase %s failed: %w", phase.Name(), err)
        }
        
        if result.Retry {
            return &RetryError{Phase: phase.Name(), After: result.RetryAfter}
        }
        
        if result.Stop {
            break
        }
    }
    return nil
}
```

---

## 6. Agent 命令执行

### 6.1 架构

```
┌─────────────────────────────────────────────────────────────────────┐
│                      Management Cluster                              │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                    Command Controller                         │   │
│  │  ┌────────────────────────────────────────────────────────┐  │   │
│  │  │  Command CRD                                            │  │   │
│  │  │  spec:                                                  │  │   │
│  │  │    nodeName: worker-1                                   │  │   │
│  │  │    command: "kubeadm init"                              │  │   │
│  │  │  status:                                                │  │   │
│  │  │    phase: Completed                                     │  │   │
│  │  │    exitCode: 0                                          │  │   │
│  │  │    output: "..."                                        │  │   │
│  │  └────────────────────────────────────────────────────────┘  │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
                               │
                               │ Watch Command CRD
                               ▼
┌─────────────────────────────────────────────────────────────────────┐
│                        Workload Cluster                              │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                      BKEAgent                                 │   │
│  │  ┌────────────────────────────────────────────────────────┐  │   │
│  │  │  1. Watch Command CRD                                   │  │   │
│  │  │  2. Execute Command                                     │  │   │
│  │  │  3. Update Command Status                               │  │   │
│  │  └────────────────────────────────────────────────────────┘  │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

### 6.2 Command CRD

```yaml
apiVersion: bkeagent.bocloud.com/v1beta1
kind: Command
metadata:
  name: init-kubelet
  namespace: bke-system
spec:
  nodeName: worker-1
  command: |
    kubeadm join --token=xxx \
      control-plane.example.com:6443
  timeout: 300s
  retryPolicy:
    maxRetries: 3
    backoff: 10s
status:
  phase: Completed
  exitCode: 0
  startTime: "2024-01-01T00:00:00Z"
  completionTime: "2024-01-01T00:05:00Z"
  output: "This node has joined the cluster"
```

---

## 7. 节点池管理

### 7.1 节点分配流程

```
┌─────────────────────────────────────────────────────────────────────┐
│                    Node Allocation Flow                             │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  BKEMachinePool 扩容请求                                            │
│          │                                                           │
│          ▼                                                           │
│  ┌──────────────────┐                                               │
│  │ 计算需要的节点数  │                                               │
│  └──────────────────┘                                               │
│          │                                                           │
│          ▼                                                           │
│  ┌──────────────────┐                                               │
│  │ 调用 AllocateNode │◄─── BKENodePool Controller                   │
│  └──────────────────┘                                               │
│          │                                                           │
│          ▼                                                           │
│  ┌──────────────────┐                                               │
│  │ 选择可用节点      │                                               │
│  │ - 状态 Available │                                               │
│  │ - 健康 Healthy    │                                               │
│  │ - 匹配选择器      │                                               │
│  └──────────────────┘                                               │
│          │                                                           │
│          ▼                                                           │
│  ┌──────────────────┐                                               │
│  │ 更新节点状态      │                                               │
│  │ - State: InUse   │                                               │
│  │ - AssignedTo     │                                               │
│  └──────────────────┘                                               │
│          │                                                           │
│          ▼                                                           │
│  ┌──────────────────┐                                               │
│  │ 创建 BKEMachine   │                                               │
│  └──────────────────┘                                               │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 7.2 健康检查机制

| 检查类型 | 说明 | 用途 |
|----------|------|------|
| SSH | SSH 连接检查 | 基础连通性 |
| TCP | TCP 端口检查 | 服务可用性 |
| Command | 命令执行检查 | 自定义检查 |

---

## 8. 与 Cluster API 集成

### 8.1 资源映射关系

```
┌─────────────────────────────────────────────────────────────────────┐
│                    Cluster API Resources                            │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  Cluster ──────────────────────► BKECluster                         │
│    │                                 │                               │
│    │ controlPlaneRef                 │ infrastructureRef            │
│    ▼                                 ▼                               │
│  KubeadmControlPlane           BKECluster                           │
│    │                                 │                               │
│    │ infrastructureRef               │                               │
│    ▼                                 │                               │
│  BKEMachineTemplate ◄────────────────┘                               │
│    │                                                                 │
│    │ 创建                                                            │
│    ▼                                                                 │
│  Machine ─────────────────────► BKEMachine                           │
│    │                                 │                               │
│    │ infrastructureRef               │                               │
│    │ bootstrap.configRef             │                               │
│    ▼                                 ▼                               │
│  BKEMachine                    KubeadmConfig                         │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 8.2 典型部署配置

```yaml
# Cluster
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: production
spec:
  infrastructureRef:
    apiVersion: bke.bocloud.com/v1beta1
    kind: BKECluster
    name: production
  controlPlaneRef:
    apiVersion: controlplane.cluster.x-k8s.io/v1beta1
    kind: KubeadmControlPlane
    name: production-control-plane

---
# BKECluster
apiVersion: bke.bocloud.com/v1beta1
kind: BKECluster
metadata:
  name: production
spec:
  controlPlaneEndpoint:
    host: api.production.example.com
    port: 6443

---
# KubeadmControlPlane
apiVersion: controlplane.cluster.x-k8s.io/v1beta1
kind: KubeadmControlPlane
metadata:
  name: production-control-plane
spec:
  replicas: 3
  version: v1.28.0
  machineTemplate:
    infrastructureRef:
      apiVersion: bke.bocloud.com/v1beta1
      kind: BKEMachineTemplate
      name: control-plane-template
```

---

## 9. 关键设计决策

### 9.1 Agent 模式 vs 直接 SSH

| 方案 | 优点 | 缺点 |
|------|------|------|
| Agent 模式 | 可观测性好、支持重试、状态持久化 | 需要部署 Agent |
| 直接 SSH | 简单、无依赖 | 可观测性差、错误处理困难 |

**决策**：采用 Agent 模式，通过 Command CRD 管理命令执行。

### 9.2 Phase-based 工作流

| 方案 | 优点 | 缺点 |
|------|------|------|
| Phase-based | 可扩展、可观测、支持断点续传 | 实现复杂 |
| 单一 Reconcile | 实现简单 | 难以扩展、状态管理复杂 |

**决策**：采用 Phase-based 工作流，支持复杂的集群生命周期管理。

### 9.3 节点池管理

| 方案 | 优点 | 缺点 |
|------|------|------|
| BKENodePool CRD | 声明式、支持健康检查 | 需要额外控制器 |
| 内嵌到 BKECluster | 简单 | 不灵活、难以复用 |

**决策**：采用独立的 BKENodePool CRD，支持节点池的独立管理和复用。

---

## 10. 总结

### 10.1 设计特点

| 特点 | 说明 |
|------|------|
| Cluster API 兼容 | 完全兼容 Cluster API 标准 |
| 声明式管理 | 通过 CRD 声明式定义集群 |
| 阶段化流程 | 支持复杂的集群生命周期管理 |
| Agent 执行 | 可靠的远程命令执行机制 |
| 节点池支持 | 支持用户提供的节点池管理 |
| 可观测性 | 完善的状态和事件记录 |

### 10.2 技术栈

| 组件 | 技术 |
|------|------|
| 控制器框架 | controller-runtime |
| CRD 定义 | kubebuilder |
| 命令执行 | Agent + CRD |
| 状态管理 | Conditions + Phase |
| 配置管理 | ConfigMap + Secret |
        

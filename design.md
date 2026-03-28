          
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

# cluster-api-provider-bke 详细设计
## 一、项目概述
`cluster-api-provider-bke` 是一个基于 Kubernetes Cluster API 的基础设施 Provider，用于管理 BKE (Bocloud Kubernetes Engine) 集群的生命周期。该项目实现了 Cluster API 规范，支持 Kubernetes 集群的自动化部署、升级、扩缩容和销毁。
### 核心组件
| 组件 | 说明 |
|------|------|
| **capbke** | Cluster API Provider 核心控制器，运行在管理集群中 |
| **bkeagent** | 节点代理，运行在每个目标节点上，执行具体的节点操作 |
## 二、架构设计
### 2.1 整体架构
```
┌─────────────────────────────────────────────────────────────────────┐
│                          管理集群                        │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    capbke Controller                         │   │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌──────────────┐ │   │
│  │  │ BKECluster      │  │ BKEMachine      │  │ Webhook      │ │   │
│  │  │ Controller      │  │ Controller      │  │ (Validation) │ │   │
│  │  └────────┬────────┘  └────────┬────────┘  └──────────────┘ │   │
│  │           │                    │                             │   │
│  │           ▼                    ▼                             │   │
│  │  ┌─────────────────────────────────────────────────────────┐│   │
│  │  │              Command CRD (指令分发)                      ││   │
│  │  └─────────────────────────────────────────────────────────┘│   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ Watch Command CRD
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                        目标集群节点                         │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                      bkeagent                                │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │   │
│  │  │ Command      │  │ 执行器       │  │ 系统操作         │   │   │
│  │  │ Controller   │──▶│ (Shell/      │──▶│ (systemd/       │   │   │
│  │  │              │  │  Kubernetes) │  │  kubelet/etc)    │   │   │
│  │  └──────────────┘  └──────────────┘  └──────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```
### 2.2 目录结构
```
cluster-api-provider-bke/
├── api/                          # API 类型定义
│   ├── bkeagent/v1beta1/         # bkeagent API 组
│   │   ├── command_types.go      # Command CRD 定义
│   │   └── condition.go          # 条件类型
│   ├── bkecommon/v1beta1/        # 公共 API 类型
│   │   ├── bkecluster_spec.go    # BKECluster Spec 定义
│   │   ├── bkecluster_status.go  # BKECluster Status 定义
│   │   └── bkenode_types.go      # 节点类型定义
│   └── capbke/v1beta1/           # capbke API 组
│       ├── bkecluster_types.go   # BKECluster CRD
│       ├── bkemachine_types.go   # BKEMachine CRD
│       └── bkenode_types.go      # BKENode CRD
├── cmd/                          # 入口程序
│   ├── capbke/                   # capbke 控制器入口
│   ├── bkeagent/                 # bkeagent 入口
│   └── bkeagent-launcher/        # bkeagent 启动器
├── controllers/                  # 控制器实现
│   ├── capbke/                   # capbke 控制器
│   │   ├── bkecluster_controller.go
│   │   ├── bkemachine_controller.go
│   │   └── bkemachine_controller_phases.go
│   └── bkeagent/                 # bkeagent 控制器
│       └── command_controller.go
├── pkg/                          # 核心业务逻辑
│   ├── command/                  # 命令执行模块
│   ├── remote/                   # 远程执行模块
│   ├── certs/                    # 证书管理
│   ├── phaseframe/               # 阶段框架
│   └── metrics/                  # 指标收集
├── utils/                        # 工具函数
│   ├── capbke/                   # capbke 工具
│   │   ├── predicates/           # 谓词过滤
│   │   ├── patchutil/            # Patch 工具
│   │   └── clustertracker/       # 集群追踪
│   └── bkeagent/                 # bkeagent 工具
│       ├── pkiutil/              # PKI 工具
│       ├── mfutil/               # Manifest 工具
│       └── etcd/                 # Etcd 工具
├── config/                       # Kubernetes 配置
│   ├── crd/                      # CRD 定义
│   ├── rbac/                     # RBAC 配置
│   └── webhook/                  # Webhook 配置
├── common/                       # 公共模块
│   ├── cluster/                  # 集群操作
│   └── ntp/                      # NTP 同步
└── builder/                      # 构建配置
    ├── capbke/                   # capbke Dockerfile
    └── bkeagent/                 # bkeagent Dockerfile
```
## 三、核心 API 设计
### 3.1 BKECluster
**用途**: 定义 BKE 集群的期望状态和实际状态。
```go
type BKECluster struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   BKEClusterSpec   `json:"spec,omitempty"`
    Status BKEClusterStatus `json:"status,omitempty"`
}
```
**BKEClusterSpec 核心字段**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `controlPlaneEndpoint` | APIEndpoint | 控制平面访问端点 |
| `clusterConfig` | BKEConfig | 集群配置 |
| `pause` | bool | 暂停协调 |
| `dryRun` | bool | 试运行模式 |
| `reset` | bool | 重置集群 |

**BKEConfig 结构**:
```go
type BKEConfig struct {
    Cluster           Cluster          `json:"cluster,omitempty"`
    Addons            []Product        `json:"addons,omitempty"`
    CustomExtra       map[string]string `json:"customExtra,omitempty"`
}

type Cluster struct {
    ControlPlane      ControlPlane      `json:",omitempty"`
    Kubelet           *Kubelet          `json:"kubelet,omitempty"`
    Networking        Networking        `json:"networking,omitempty"`
    HTTPRepo          Repo              `json:"httpRepo,omitempty"`
    ImageRepo         Repo              `json:"imageRepo,omitempty"`
    ChartRepo         Repo              `json:"chartRepo,omitempty"`
    ContainerRuntime  ContainerRuntime  `json:"containerRuntime,omitempty"`
    KubernetesVersion string            `json:"kubernetesVersion,omitempty"`
    EtcdVersion       string            `json:"etcdVersion,omitempty"`
    CertificatesDir   string            `json:"certificatesDir"`
    NTPServer         string            `json:"ntpServer,omitempty"`
    Labels            []Label           `json:"labels,omitempty"`
}
```
### 3.2 BKEMachine
**用途**: 定义 BKE 集群中单个机器的状态。
```go
type BKEMachine struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   BKEMachineSpec   `json:"spec,omitempty"`
    Status BKEMachineStatus `json:"status,omitempty"`
}

type BKEMachineSpec struct {
    ProviderID *string `json:"providerID,omitempty"`
    Pause      bool    `json:"pause,omitempty"`
    DryRun     bool    `json:"dryRun,omitempty"`
}

type BKEMachineStatus struct {
    Ready        bool              `json:"ready"`
    Bootstrapped bool              `json:"bootstrapped"`
    Addresses    []MachineAddress  `json:"addresses,omitempty"`
    Conditions   clusterv1.Conditions `json:"conditions,omitempty"`
    Node         *Node             `json:"node,omitempty"`
}
```
### 3.3 Command
**用途**: 定义在节点上执行的命令，是 capbke 和 bkeagent 之间的通信桥梁。
```go
type Command struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   CommandSpec               `json:"spec,omitempty"`
    Status map[string]*CommandStatus `json:"status,omitempty"`
}

type CommandSpec struct {
    NodeName               string          `json:"nodeName"`
    Suspend                bool            `json:"suspend"`
    Commands               []ExecCommand   `json:"commands,omitempty"`
    BackoffLimit           int             `json:"backoffLimit,omitempty"`
    ActiveDeadlineSecond   int             `json:"activeDeadlineSecond,omitempty"`
    TTLSecondsAfterFinished int            `json:"ttlSecondsAfterFinished,omitempty"`
    NodeSelector           *metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

type ExecCommand struct {
    ID            string       `json:"id"`
    Command       []string     `json:"command"`
    Type          CommandType  `json:"type"`
    BackoffIgnore bool         `json:"backoffIgnore,omitempty"`
    BackoffDelay  int          `json:"backoffDelay,omitempty"`
}
```
**CommandType 枚举**:

| 类型 | 说明 |
|------|------|
| `BuiltIn` | Agent 内置指令 (如 ipv4 开启检查) |
| `Shell` | 执行 Shell 命令 |
| `Kubernetes` | 操作 K8s 资源 |

**CommandPhase 状态流转**:
```
Pending → Running → Completed
                   → Failed
         → Suspend → Running
         → Skip
```
## 四、控制器设计
### 4.1 BKECluster 控制器
**职责**: 管理 BKE 集群的生命周期。

**协调流程**:
```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 获取并验证集群资源
    bkeCluster, err := r.getAndValidateCluster(ctx, req)
    
    // 2. 处理指标注册
    r.registerMetrics(bkeCluster)
    
    // 3. 获取旧版本集群配置
    oldBkeCluster, err := r.getOldBKECluster(bkeCluster)
    
    // 4. 初始化日志记录器
    bkeLogger := r.initializeLogger(bkeCluster)
    
    // 5. 处理代理和节点状态
    r.handleClusterStatus(ctx, bkeCluster, bkeLogger)
    
    // 6. 执行阶段流程
    phaseResult, err := r.executePhaseFlow(ctx, bkeCluster, oldBkeCluster, bkeLogger)
    
    // 7. 设置集群监控
    watchResult, err := r.setupClusterWatching(ctx, bkeCluster, bkeLogger)
    
    // 8. 返回最终结果
    return r.getFinalResult(phaseResult, bkeCluster)
}
```
### 4.2 BKEMachine 控制器
**职责**: 管理单个节点的生命周期。

**协调流程**:
```go
func (r *BKEMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 获取必要对象
    objects, err := r.fetchRequiredObjects(ctx, req, log)
    
    // 2. 处理暂停检查
    result, shouldReturn := r.handlePauseAndFinalizer(objects, log)
    
    // 3. 添加 Finalizer
    controllerutil.AddFinalizer(objects.BKEMachine, BKEMachineFinalizer)
    
    // 4. 获取 BKECluster
    bkeCluster, err := mergecluster.GetCombinedBKECluster(...)
    
    // 5. 执行主要协调逻辑
    return r.handleMainReconcile(params)
}
```
### 4.3 Command 控制器
**职责**: 在节点上执行命令。
```go
type CommandReconciler struct {
    Client    client.Client
    APIReader client.Reader
    Scheme    *runtime.Scheme
    Job       job.Job
    NodeName  string
    Ctx       context.Context
}
```
## 五、阶段框架设计
### 5.1 阶段流程
项目使用阶段框架 来管理集群操作的复杂流程：
```go
type ReconcilePhaseCtx struct {
    context.Context
    Client      client.Client
    RestConfig  *rest.Config
    Scheme      *runtime.Scheme
    Logger      *BKELogger
    BKECluster  *bkev1beta1.BKECluster
}

type PhaseFlow struct {
    ctx    *ReconcilePhaseCtx
    phases []Phase
}

func (f *PhaseFlow) CalculatePhase(old, new *bkev1beta1.BKECluster) error {
    // 根据集群状态计算需要执行的阶段
}
```
### 5.2 典型阶段
| 阶段 | 说明 |
|------|------|
| Init | 初始化阶段 |
| PreCheck | 预检查阶段 |
| PKI | 证书生成阶段 |
| Etcd | Etcd 部署阶段 |
| ControlPlane | 控制平面部署阶段 |
| Worker | 工作节点加入阶段 |
| Addon | 插件安装阶段 |
| PostCheck | 后检查阶段 |
## 六、命令执行机制
### 6.1 BaseCommand 结构
```go
type BaseCommand struct {
    Ctx           context.Context
    Client        client.Client
    NameSpace     string
    Scheme        *runtime.Scheme
    OwnerObj      metav1.Object
    ClusterName   string
    
    RemoveAfterWait bool
    Unique          bool
    ForceRemove     bool
    WaitTimeout     time.Duration
    WaitInterval    time.Duration
    
    Command *agentv1beta1.Command
}
```
### 6.2 命令类型
| 命令 | 用途 |
|------|------|
| `BootstrapCommand` | 节点初始化 |
| `HACommand` | 高可用部署 |
| `K8sEnvCommand` | K8s 环境初始化 |
| `ContainerdResetCommand` | Containerd 重置 |
| `UpgradeCommand` | 集群升级 |
| `SwitchClusterCommand` | 切换集群 |
| `CleanNodeCommand` | 清理节点 |

### 6.3 命令执行流程
```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│ capbke      │     │ Command CRD │     │ bkeagent    │
│ Controller  │────▶│ (K8s API)   │────▶│ Controller  │
└─────────────┘     └─────────────┘     └──────┬──────┘
                                               │
                                               ▼
                                        ┌─────────────┐
                                        │ 执行器      │
                                        │ (Shell/K8s) │
                                        └─────────────┘
```
## 七、远程执行模块
### 7.1 SSH 客户端
```go
type Ssh struct {
    sshClient *gossh.Client
    alive     bool
}

func NewSSHClient(host *Host) (*Ssh, error) {
    // 支持密码认证和 SSH Key 认证
}

func (s *Ssh) Exec(cmd string) ([]string, []string, error) {
    // 执行远程命令，返回 stdout 和 stderr
}
```
### 7.2 Host 定义
```go
type Host struct {
    User     string
    Password string
    Address  string
    Port     string
    SSHKey   string
}
```
## 八、PKI 证书管理
### 8.1 证书生成
```go
func GenerateCACert(certSpec *BKECert) error
func GenerateCertWithCA(certSpec *BKECert, caCertSpec *BKECert) error
func GenerateRSACert(certSpec *BKECert) error
```
### 8.2 证书类型
| 证书 | 用途 |
|------|------|
| CA | 根证书 |
| Etcd | Etcd 通信证书 |
| API Server | API Server 证书 |
| Kubelet | Kubelet 证书 |
| Admin | 管理员证书 |
## 九、Manifest 管理工具
### 9.1 模板渲染
项目使用模板文件生成 Kubernetes 组件配置：
```
utils/bkeagent/mfutil/tmpl/
├── k8s/
│   ├── kube-apiserver.yaml.tmpl
│   ├── kube-controller-manager.yaml.tmpl
│   ├── kube-scheduler.yaml.tmpl
│   └── etcd.yaml.tmpl
├── haproxy/
│   ├── haproxy.yaml.tmpl
│   └── haproxy.cfg.tmpl
└── keepalived/
    ├── keepalived.yaml.tmpl
    └── keepalived.master.conf.tmpl
```
### 9.2 组件列表
```go
type ComponentList struct {
    Etcd              *Etcd
    KubeAPIServer     *KubeAPIServer
    KubeControllerMgr *KubeControllerMgr
    KubeScheduler     *KubeScheduler
    Kubelet           *Kubelet
    HAProxy           *HAProxy
    Keepalived        *Keepalived
}
```
## 十、监控与指标
### 10.1 指标收集
```go
type MetricRegister struct {
    metrics map[string]*MetricVec
}

func (m *MetricRegister) Register(namespace string)
```
### 10.2 Grafana Dashboard
项目提供预配置的 Grafana Dashboard：
- `controller-runtime-metrics.json`: 控制器运行时指标
- `controller-resources-metrics.json`: 资源使用指标
- `custom-metrics-dashboard.json`: 自定义指标
## 十一、Webhook 设计
### 11.1 BKECluster Webhook
```go
type BKEClusterWebhook struct {
    Client client.Client
}

func (w *BKEClusterWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) error
func (w *BKEClusterWebhook) ValidateUpdate(ctx context.Context, old, new runtime.Object) error
func (w *BKEClusterWebhook) Default(ctx context.Context, obj runtime.Object) error
```
### 11.2 BKEMachine Webhook
```go
type BKEMachineWebhook struct {
    Client client.Client
}
```
## 十二、依赖关系
```
cluster-api-provider-bke
    ├── sigs.k8s.io/cluster-api (v1.5.0)      # Cluster API 核心
    ├── sigs.k8s.io/controller-runtime (v0.22.4) # 控制器运行时
    ├── k8s.io/api (v0.28.0)                  # Kubernetes API
    ├── k8s.io/client-go (v0.28.0)            # Kubernetes 客户端
    ├── helm.sh/helm/v3 (v3.18.6)             # Helm 客户端
    ├── go.etcd.io/etcd/client/v3            # Etcd 客户端
    ├── golang.org/x/crypto                   # SSH 支持
    └── github.com/prometheus/client_golang   # 指标收集
```
## 十三、部署架构
### 13.1 capbke 部署
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: capbke-controller-manager
spec:
  template:
    spec:
      containers:
      - name: manager
        image: <registry>/capbke:<version>
        args:
        - --leader-elect
        - --metrics-bind-addr=:8080
        - --webhook-port=9443
```
### 13.2 bkeagent 部署
bkeagent 以 systemd 服务形式运行在目标节点上：
```ini
[Unit]
Description=BKE Agent
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/bkeagent
Restart=always

[Install]
WantedBy=multi-user.target
```
## 十四、设计原则总结
1. **声明式 API**: 所有资源使用 Spec/Status 模式
2. **Command 模式**: 通过 Command CRD 解耦控制平面和数据平面
3. **阶段框架**: 使用阶段框架管理复杂操作流程
4. **Finalizer 机制**: 确保资源清理的可靠性
5. **条件模式**: 使用 Condition 跟踪资源状态
6. **模板化配置**: 使用模板生成组件配置
7. **可观测性**: 内置指标收集和 Grafana Dashboard

### 10.2 技术栈

| 组件 | 技术 |
|------|------|
| 控制器框架 | controller-runtime |
| CRD 定义 | kubebuilder |
| 命令执行 | Agent + CRD |
| 状态管理 | Conditions + Phase |
| 配置管理 | ConfigMap + Secret |
        

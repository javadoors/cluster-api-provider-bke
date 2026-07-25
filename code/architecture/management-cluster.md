# 管理集群架构与安装流程

## 一、核心架构特点

### 1.1 BKECluster 资源分布

**关键结论：管理集群本身在管理集群中没有 BKECluster 资源**

```
┌─────────────────────────────────────────────────────────────────┐
│                        引导集群 (Bootstrap)                      │
│                     (临时 K3s 单节点集群)                        │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  BKECluster CR (用于安装管理集群)                         │   │
│  │  └─ 触发 PhaseFrame 引擎安装管理集群                      │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ bkeadm init 完成后创建
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                        管理集群 (Management)                     │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  cluster-system/bke-controller-manager (Deployment)       │   │
│  │  └─ 直接管理，无 BKECluster CR                            │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  BKECluster CR (业务集群 1)                               │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  BKECluster CR (业务集群 2)                               │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### 1.2 资源分布表

| 组件 | 所在集群 | 是否有 BKECluster CR | 说明 |
|------|---------|---------------------|------|
| 引导集群 | 临时 K3s | 有 | 用于安装管理集群 |
| 管理集群 | 管理集群自身 | **无** | 直接管理，无 CR |
| 业务集群 | 管理集群中 | 有 | 通过 CR 管理 |

### 1.3 对升级设计的影响

由于管理集群没有 BKECluster 资源，这意味着：

1. **管理集群的升级不能通过 BKECluster CR 触发**
   - 业务集群的升级：修改 BKECluster.Spec → 触发 Reconciler → 执行升级
   - 管理集群的升级：需要其他机制（如 Operator 直接管理 Deployment）

2. **升级检测方式需要调整**
   - 不能依赖 BKECluster CR 的状态
   - 需要通过检测 Deployment 来判断是否需要升级（如 `cluster-system/bke-controller-manager`）

3. **管理集群升级的触发方式**
   - 可能需要在管理集群中运行一个独立的 Operator
   - 或者通过外部工具（如 Helm/Ansible）直接升级 Deployment

---

## 二、管理集群安装流程

### 2.1 安装触发机制

管理集群的安装由 **PhaseFrame 引擎** 驱动：

1. `bkeadm init` 完成引导集群初始化
2. 自动调用 `deployCluster()` 在引导集群上创建 BKECluster CR
3. capbke 控制器监听到 CR 创建事件
4. PhaseFrame 引擎按阶段执行安装流程

### 2.2 安装阶段与组件

#### 阶段 1：EnsureFinalizer — 部署任务创建

**职责**：为 BKECluster 设置 Finalizer，确保删除时能执行清理逻辑。

**安装组件**：无（仅设置元数据）

---

#### 阶段 2：EnsureCerts — 集群证书创建

**职责**：通过 `BKEKubernetesCertGenerator` 生成 Kubernetes 集群所需的全部证书。

**安装组件**：
- CA 证书（etcd、k8s、front-proxy）
- API Server 证书及密钥
- etcd Server/Peer/Client 证书
- Service Account 密钥
- kubeconfig 文件

**存储位置**：证书以 Secret 形式存储在引导集群

---

#### 阶段 3：EnsureClusterAPIObj — ClusterAPI 对接

**职责**：在引导集群创建 CAPI 资源映射，使 BKECluster 与 CAPI 体系对接。

**安装组件**：
- Cluster 资源（CAPI 核心）
- BKECluster → Cluster 的 OwnerReference
- Machine 资源（Master 节点）
- MachineDeployment 资源（Worker 节点）

---

#### 阶段 4：EnsureBKEAgent — 推送 Agent

**职责**：通过 SSH 将 bkeagent 二进制推送到所有节点。

**安装组件**：
- bkeagent 二进制文件（`/usr/local/bin/bkeagent`）
- bkeagent systemd 服务文件
- bkeagent 配置文件

**节点范围**：所有 Master 和 Worker 节点

---

#### 阶段 5：EnsureNodesEnv — 节点环境准备

**职责**：通过 bkeagent 在每个节点上执行环境初始化命令。

**安装组件**：
- **系统参数调优**：
  - sysctl 配置（`vm.max_map_count`、`net.bridge.bridge-nf-call-iptables` 等）
  - ulimit 配置
  - 内核模块加载（`br_netfilter`、`overlay` 等）
- **容器运行时**：containerd 安装和配置
- **网络插件**：CNI 插件准备
- **存储插件**：CSI 插件准备
- **其他工具**：
  - lxcfs
  - nfs-utils
  - etcdctl
  - helm
  - calicoctl

---

#### 阶段 6：EnsureLoadBalance — 负载均衡配置

**职责**：配置 API Server 的高可用负载均衡。

**安装组件**：
- HAProxy（API Server 负载均衡）
- Keepalived（VIP 管理）
- 负载均衡配置文件

**部署方式**：静态 Pod（运行在 Master 节点上）

---

#### 阶段 7：EnsureMasterInit — Master 初始化

**职责**：初始化第一个 Master 节点，创建控制平面。

**安装组件**：
- **静态 Pod**（运行在 Master 节点上）：
  - kube-apiserver
  - kube-controller-manager
  - kube-scheduler
  - etcd
- **系统服务**：
  - kubelet
  - containerd
- **集群组件**：
  - kube-proxy（DaemonSet）
  - CoreDNS（Deployment）

---

#### 阶段 8：EnsureMasterJoin — Master 加入

**职责**：其他 Master 节点加入集群，形成高可用控制平面。

**安装组件**：与 EnsureMasterInit 相同，但使用 `kubeadm join` 加入现有集群。

---

#### 阶段 9：EnsureWorkerJoin — Worker 加入

**职责**：Worker 节点加入集群。

**安装组件**：
- kubelet（系统服务）
- containerd（系统服务）
- kube-proxy（DaemonSet）

---

#### 阶段 10：EnsureAddonDeploy — 集群组件部署

**职责**：部署管理集群特有的平台服务组件。

**安装组件**：

**基础设施层**：
- Ingress-Nginx（Ingress 控制器）
- Local Harbor（本地镜像仓库）

**监控层**：
- Prometheus（监控数据收集）
- Alertmanager（告警管理）
- Node Exporter（节点指标）
- Kube State Metrics（K8s 对象指标）
- Grafana（可视化面板）

**业务层**：
- Console Website（控制台前端）
- Console Service（控制台后端）
- OAuth Server（认证服务）
- Marketplace Service（应用市场）
- Application Management Service（应用管理）
- Plugin Management Service（插件管理）
- User Management Operator（用户管理）
- Web Terminal Service（Web 终端）

**应用层**：
- Installer Website（安装向导前端）
- Installer Service（安装向导后端）
- Metrics Server（资源指标 API）

---

#### 阶段 11：EnsureNodesPostProcess — 后置处理

**职责**：执行节点后置处理脚本。

**安装组件**：无（执行自定义脚本）

---

#### 阶段 12：EnsureAgentSwitch — Agent 监听切换

**职责**：将 BKEAgent 的监听目标从引导集群切换到管理集群。

**安装组件**：无（修改 Agent 配置）

---

## 三、管理集群核心组件清单

### 3.1 控制平面组件

| 组件 | 类型 | 命名空间 | 说明 |
|------|------|---------|------|
| kube-apiserver | 静态 Pod | kube-system | Kubernetes API 服务器 |
| kube-controller-manager | 静态 Pod | kube-system | 控制器管理器 |
| kube-scheduler | 静态 Pod | kube-system | 调度器 |
| etcd | 静态 Pod | kube-system | 分布式键值存储 |
| kubelet | 系统服务 | - | 节点代理 |
| containerd | 系统服务 | - | 容器运行时 |

### 3.2 BKE Provider 组件

| 组件 | 类型 | 命名空间 | 说明 |
|------|------|---------|------|
| bke-controller-manager | Deployment | cluster-system | BKE 控制器（Provider） |
| capi-controller-manager | Deployment | cluster-system | CAPI 控制器 |
| bkeagent-deployer | Deployment | cluster-system | Agent 部署器 |

### 3.3 基础设施组件

| 组件 | 类型 | 命名空间 | 说明 |
|------|------|---------|------|
| ingress-nginx-controller | DaemonSet | ingress-nginx | Ingress 控制器 |
| local-harbor | Deployment | openfuyao-system | 本地镜像仓库 |

### 3.4 监控组件

| 组件 | 类型 | 命名空间 | 说明 |
|------|------|---------|------|
| prometheus | StatefulSet | monitoring | 监控数据收集 |
| alertmanager | StatefulSet | monitoring | 告警管理 |
| node-exporter | DaemonSet | monitoring | 节点指标采集 |
| kube-state-metrics | Deployment | monitoring | K8s 对象指标 |
| grafana | Deployment | monitoring | 可视化面板 |
| metrics-server | Deployment | kube-system | 资源指标 API |

### 3.5 业务组件

| 组件 | 类型 | 命名空间 | 说明 |
|------|------|---------|------|
| console-website | Deployment | openfuyao-system | 控制台前端 |
| console-service | Deployment | openfuyao-system | 控制台后端 |
| oauth-server | Deployment | openfuyao-system | 认证服务 |
| marketplace-service | Deployment | openfuyao-system | 应用市场 |
| application-management-service | Deployment | openfuyao-system | 应用管理 |
| plugin-management-service | Deployment | openfuyao-system | 插件管理 |
| user-management-operator | Deployment | openfuyao-system | 用户管理 |
| web-terminal-service | Deployment | openfuyao-system | Web 终端 |
| installer-website | Deployment | openfuyao-system | 安装向导前端 |
| installer-service | Deployment | openfuyao-system | 安装向导后端 |

### 3.6 节点组件

| 组件 | 类型 | 说明 |
|------|------|------|
| bkeagent | 系统服务 | BKE Agent（运行在所有节点） |
| kube-proxy | DaemonSet | 网络代理 |
| coredns | Deployment | 集群 DNS |

---

## 四、管理集群与业务集群的对比

| 维度 | 管理集群 | 业务集群 |
|------|---------|---------|
| **BKECluster CR** | 无 | 有 |
| **安装触发方式** | 引导集群上的 BKECluster CR | 管理集群上的 BKECluster CR |
| **控制平面** | 有（etcd、apiserver 等） | 有（etcd、apiserver 等） |
| **Provider 组件** | 有（bke-controller-manager） | 无 |
| **平台服务** | 有（Console、监控等） | 无（或可选） |
| **升级方式** | 需要独立机制 | 通过 BKECluster CR 触发 |
| **Agent 监听目标** | 管理集群自身 | 管理集群（初期）→ 业务集群（后期） |

---

## 五、关键设计约束

### 5.1 管理集群升级约束

1. **不能通过 BKECluster CR 触发升级**
   - 需要通过检测 Deployment 镜像版本来判断是否需要升级
   - 升级逻辑需要在 bke-controller-manager 内部实现

2. **自举升级的特殊性**
   - bke-controller-manager 升级自己时，会导致当前进程被终止
   - 需要处理 context canceled 场景
   - 需要优雅等待新版本的 Provider 接管

3. **install-service 升级依赖**
   - install-service 的升级依赖 bke-controller-manager 先完成升级
   - 需要通过 DAG 依赖关系确保执行顺序

### 5.2 检测管理集群的方式

由于管理集群没有 BKECluster CR，需要通过以下方式检测：

```go
// 检测 bke-controller-manager Deployment 是否存在
target := phaseutil.DeploymentTarget{
    Namespace: "cluster-system",
    Name:      "bke-controller-manager",
    Container: "manager",
}
_, err := phaseutil.GetDeploymentImage(ctx, c, target)
if err != nil {
    // Deployment 不存在，不是管理集群
    return false
}
// Deployment 存在，是管理集群
return true
```

---

## 六、总结

管理集群是 BKE 架构的核心，负责管理所有业务集群的生命周期。其关键特点包括：

1. **无 BKECluster CR**：管理集群本身在管理集群中没有 BKECluster 资源
2. **通过引导集群安装**：由引导集群上的 BKECluster CR 触发安装
3. **包含平台服务**：运行 Console、监控、OAuth 等平台服务组件
4. **升级机制特殊**：需要通过独立机制（而非 BKECluster CR）触发升级

理解这些特点对于设计管理集群的升级流程至关重要。

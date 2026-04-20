# NodeConfig 概要设计
基于 `stage4-2.md` 生成 `NodeConfig` 概要设计
## 1. 定位与职责
NodeConfig 是节点级声明式配置 CRD，描述**单个节点**的连接信息、OS 信息和组件配置。它与 ComponentVersion 形成"配置-执行"分离模型：

| 维度 | NodeConfig | ComponentVersion |
|------|-----------|-----------------|
| 描述内容 | 节点"需要什么" | 组件"如何安装/升级" |
| 粒度 | 节点级（每个节点一个实例） | 组件级（每个组件一个实例） |
| 关联方式 | `spec.components.<name>.version` 声明期望版本 | `spec.version` + `spec.installAction/upgradeAction` 声明执行方式 |
| Scope | 始终 Node | Node 或 Cluster |

**核心原则**：NodeConfig 声明"这个节点需要什么版本的组件"，ComponentVersion 声明"这个组件如何安装/升级"。两者通过 `componentName` 关联。
## 2. CRD 结构
### 2.1 NodeConfigSpec
```
NodeConfigSpec
├── connection          # 节点连接信息
│   ├── host            # 节点 IP
│   ├── port            # SSH 端口
│   ├── sshKeySecret    # SSH 密钥引用
│   └── agentPort       # Agent 端口
├── os                  # 操作系统信息
│   ├── type            # linux/windows
│   ├── distro          # centos/ubuntu/kylin/...
│   ├── version         # 22.04/7/8/...
│   └── arch            # amd64/arm64
├── roles               # 节点角色 [master, worker, etcd]
└── components          # 节点级组件配置
    ├── containerd      # Containerd 配置
    ├── kubelet         # Kubelet 配置
    ├── etcd            # Etcd 配置（master/etcd 节点）
    ├── bkeAgent        # BKE Agent 配置
    ├── nodesEnv        # 节点环境配置
    └── postProcess     # 后处理配置
```
### 2.2 NodeConfigStatus
```
NodeConfigStatus
├── phase               # 节点生命周期阶段
├── componentStatus     # 各组件安装状态（map[componentName]detail）
│   └── <component>
│       ├── installedVersion
│       ├── status
│       ├── lastUpdated
│       └── message
├── osInfo              # 实际 OS 详细信息
│   ├── kernelVersion
│   └── osImage
├── lastOperation       # 最近一次操作记录
└── conditions          # 条件集合
```
### 2.3 生命周期阶段
```
Pending → Installing → Ready
              ↑          ↓
            Failed    Upgrading → Ready
                         ↓
                      Failed
                         ↓
                    Deleting → (terminated)
```
| Phase | 含义 |
|-------|------|
| `Pending` | NodeConfig 已创建，等待执行安装 |
| `Installing` | 正在执行首次安装 |
| `Upgrading` | 正在执行版本升级 |
| `Ready` | 所有组件安装/升级完成，节点就绪 |
| `Failed` | 安装/升级失败 |
| `Deleting` | 节点正在缩容删除 |
## 3. 与现有 BKENode 的关系
### 3.1 现有 BKENode 结构
当前 [bkenode_types.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkenode_types.go) 中的 `BKENode` 定义：
```go
type BKENodeSpec struct {
    Role         []string       // 节点角色
    IP           string         // 节点 IP
    Port         string         // SSH 端口
    Username     string         // SSH 用户名
    Password     string         // SSH 密码
    Hostname     string         // 主机名
    ControlPlane `json:",omitempty"` // 控制平面覆盖配置
    Kubelet      *Kubelet       // Kubelet 覆盖配置
    Labels       []Label        // 节点标签
}

type BKENodeStatus struct {
    State     NodeState  // 节点状态
    StateCode int        // 状态码
    Message   string     // 状态消息
    NeedSkip  bool       // 是否跳过
}
```
### 3.2 NodeConfig 与 BKENode 的映射
| BKENode 字段 | NodeConfig 字段 | 说明 |
|-------------|----------------|------|
| `spec.ip` | `spec.connection.host` | 节点 IP |
| `spec.port` | `spec.connection.port` | SSH 端口 |
| `spec.username` / `spec.password` | `spec.connection.sshKeySecret` | 认证方式改为 Secret 引用（更安全） |
| `spec.role` | `spec.roles` | 节点角色 |
| `spec.controlPlane` | `spec.components.kubelet` + `spec.components.etcd` | 拆分为具体组件配置 |
| `spec.kubelet` | `spec.components.kubelet` | Kubelet 配置 |
| `spec.labels` | `spec.components.kubelet.extraArgs` / `status.conditions` | 标签管理 |
| `status.state` | `status.phase` | 生命周期阶段 |
| 无 | `spec.os` | **新增**：OS 信息（从 BKECluster Spec 或自动检测） |
| 无 | `spec.components.containerd` | **新增**：Containerd 配置 |
| 无 | `spec.components.bkeAgent` | **新增**：Agent 配置 |
| 无 | `spec.components.nodesEnv` | **新增**：节点环境配置 |
| 无 | `spec.components.postProcess` | **新增**：后处理配置 |
| 无 | `status.componentStatus` | **新增**：组件级状态追踪 |
### 3.3 NodeConfig 生成来源
NodeConfig 由 ClusterOrchestrator 根据 BKECluster Spec 自动生成：
```
BKECluster.Spec.ClusterConfig.Cluster
    │
    ├── .KubernetesVersion  → NodeConfig.components.kubelet.version
    ├── .ContainerdVersion  → NodeConfig.components.containerd.version
    ├── .EtcdVersion        → NodeConfig.components.etcd.version
    ├── .OpenFuyaoVersion   → NodeConfig.components.bkeAgent.version
    ├── .HTTPRepo           → NodeConfig.components.nodesEnv.httpRepo
    ├── .ImageRepo          → NodeConfig.components.nodesEnv.imageRepo
    └── .ContainerRuntime   → NodeConfig.components.containerd.config

BKECluster.Spec.Nodes[] (BKENode[])
    │
    ├── .IP                 → NodeConfig.connection.host
    ├── .Port               → NodeConfig.connection.port
    ├── .Role               → NodeConfig.roles
    ├── .ControlPlane       → NodeConfig.components.etcd/kubelet (覆盖)
    └── .Kubelet            → NodeConfig.components.kubelet (覆盖)
```
**覆盖规则**：BKENode 中的组件配置（如 `ControlPlane`、`Kubelet`）可覆盖 Cluster 级默认配置，实现节点级差异化。
## 4. 各组件配置详细设计
### 4.1 ContainerdComponentConfig
```go
type ContainerdComponentConfig struct {
    Version      string          `json:"version,omitempty"`
    Config       string          `json:"config,omitempty"`       // containerd config.toml
    DataDir      string          `json:"dataDir,omitempty"`      // 数据目录
    Registry     *RegistryConfig `json:"registry,omitempty"`     // 镜像仓库配置
    SystemdCgroup bool           `json:"systemdCgroup,omitempty"` // 是否使用 systemd cgroup
}
```
**映射 Phase**：EnsureContainerdUpgrade、EnsureNodesEnv（containerd 安装部分）

**来源**：
- `version` ← `BKECluster.Spec.ClusterConfig.Cluster.ContainerdVersion`
- `config` ← `BKECluster.Spec.ClusterConfig.Cluster.ContainerdConfigRef`
- `registry` ← `BKECluster.Spec.ClusterConfig.Cluster.ImageRepo`
### 4.2 KubeletComponentConfig
```go
type KubeletComponentConfig struct {
    Version      string            `json:"version,omitempty"`
    ExtraArgs    map[string]string `json:"extraArgs,omitempty"`    // kubelet 额外参数
    FeatureGates map[string]bool   `json:"featureGates,omitempty"` // 特性门控
    Config       string            `json:"config,omitempty"`       // kubelet config yaml
}
```
**映射 Phase**：EnsureMasterInit、EnsureMasterJoin、EnsureWorkerJoin、EnsureMasterUpgrade、EnsureWorkerUpgrade

**来源**：
- `version` ← `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion`
- `extraArgs` ← `BKENode.Spec.Kubelet.ExtraArgs`（节点级覆盖）
- `featureGates` ← `BKECluster.Spec.ClusterConfig.Cluster.Kubelet.FeatureGates`
### 4.3 EtcdComponentConfig
```go
type EtcdComponentConfig struct {
    Version    string   `json:"version,omitempty"`
    DataDir    string   `json:"dataDir,omitempty"`
    ClientURLs []string `json:"clientURLs,omitempty"`
    PeerURLs   []string `json:"peerURLs,omitempty"`
}
```
**映射 Phase**：EnsureEtcdUpgrade

**来源**：
- `version` ← `BKECluster.Spec.ClusterConfig.Cluster.EtcdVersion`
- `dataDir` / `clientURLs` / `peerURLs` ← `BKENode.Spec.ControlPlane.Etcd`（节点级覆盖）
### 4.4 BKEAgentComponentConfig
```go
type BKEAgentComponentConfig struct {
    Version    string `json:"version,omitempty"`
    Config     string `json:"config,omitempty"`      // Agent 配置
    Kubeconfig string `json:"kubeconfig,omitempty"`  // Agent kubeconfig
}
```
**映射 Phase**：EnsureBKEAgent、EnsureAgentUpgrade、EnsureAgentSwitch

**来源**：
- `version` ← 从 `OpenFuyaoVersion` 推导
- `kubeconfig` ← 由 Controller 生成（引导集群或目标集群的 kubeconfig）
### 4.5 NodesEnvComponentConfig
```go
type NodesEnvComponentConfig struct {
    ExtraScripts []string `json:"extraScripts,omitempty"` // 额外初始化脚本
    HTTPRepo     string   `json:"httpRepo,omitempty"`     // HTTP 软件源
    ImageRepo    string   `json:"imageRepo,omitempty"`    // 镜像仓库
}
```
**映射 Phase**：EnsureNodesEnv

**来源**：
- `httpRepo` ← `BKECluster.Spec.ClusterConfig.Cluster.HTTPRepo`
- `imageRepo` ← `BKECluster.Spec.ClusterConfig.Cluster.ImageRepo`
### 4.6 PostProcessComponentConfig
```go
type PostProcessComponentConfig struct {
    Scripts []string `json:"scripts,omitempty"` // 后处理脚本列表
}
```
**映射 Phase**：EnsureNodesPostProcess
## 5. NodeConfig 与 ComponentVersion 的协作模型
### 5.1 Scope 区分
| Scope | 说明 | 需要 NodeConfig | 示例 |
|-------|------|----------------|------|
| `Node` | 节点级操作，需要在每个节点上执行 | **是** | containerd、etcd、kubelet、bkeAgent |
| `Cluster` | 集群级操作，只需执行一次 | **否** | certs、clusterAPI、addon、agentSwitch |
### 5.2 执行流程
```
ClusterOrchestrator.Reconcile()
    │
    ├── 1. syncDesiredState()
    │   ├── 生成 ComponentVersion[]（从 BKECluster.Spec 提取组件版本）
    │   └── 生成 NodeConfig[]（从 BKECluster.Spec.Nodes 提取节点配置）
    │
    ├── 2. DAGScheduler.Schedule()
    │   └── 计算就绪/阻塞/运行中组件
    │
    └── 3. executeComponent()
        │
        ├── Scope=Cluster: executor.Install(cv, nil)
        │   └── 不需要 NodeConfig
        │
        └── Scope=Node: executor.Install(cv, nodeConfigs)
            └── 传入匹配的 NodeConfig 列表
                ├── executor 按 NodeConfig 逐节点执行
                ├── 更新 NodeConfig.Status.ComponentStatus
                └── 全部节点完成后更新 ComponentVersion.Status
```
### 5.3 NodeConfig 匹配规则
ComponentExecutor 通过 `NodeSelector` 匹配 NodeConfig：
```go
// ComponentVersion 中的 NodeSelector
type NodeSelector struct {
    Roles  []string          `json:"roles,omitempty"`
    Labels map[string]string `json:"labels,omitempty"`
}

// 匹配逻辑
func matchNodeConfig(nc *NodeConfig, selector *NodeSelector) bool {
    if selector == nil {
        return true
    }
    for _, role := range selector.Roles {
        for _, nodeRole := range nc.Spec.Roles {
            if role == string(nodeRole) {
                return true
            }
        }
    }
    return false
}
```
**示例**：
- `containerd` ComponentVersion 的 `installAction.nodeSelector.roles = ["master", "worker", "etcd"]` → 匹配所有 NodeConfig
- `etcd` ComponentVersion 的 `installAction.nodeSelector.roles = ["master", "etcd"]` → 仅匹配 master/etcd 节点的 NodeConfig
## 6. 缩容场景
EnsureWorkerDelete 和 EnsureMasterDelete 不映射为 ComponentVersion，而是通过 NodeConfig 的 `status.phase = Deleting` 触发缩容流程：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: NodeConfig
metadata:
  name: node-192.168.1.20
spec:
  roles: ["worker"]
status:
  phase: Deleting
  lastOperation:
    type: Delete
    component: kubelet
    startTime: "2024-01-15T11:00:00Z"
```
**缩容流程**：
1. 用户从 BKECluster.Spec.Nodes 中移除节点
2. ClusterOrchestrator 检测到节点移除，将对应 NodeConfig 的 `status.phase` 设为 `Deleting`
3. NodeConfig Controller 执行 drain → 删除 Machine → 等待节点移除
4. 完成后删除 NodeConfig CR
## 7. 资源关系
```
BKECluster (Owner)
    │
    ├── ClusterVersion (1:1) ─── 全局版本状态
    │
    ├── ComponentVersion[] (1:N) ─── 各组件版本定义
    │
    └── NodeConfig[] (1:N) ─── 节点级配置
        ├── node-192.168.1.10 (master)
        │   spec:
        │     connection: {host: "192.168.1.10", port: 22}
        │     roles: [master]
        │     components:
        │       containerd: {version: "v1.7.2"}
        │       kubelet: {version: "v1.29.0"}
        │       etcd: {version: "v3.5.12"}
        │       bkeAgent: {version: "v1.1.0"}
        │   status:
        │     phase: Ready
        │     componentStatus:
        │       containerd: {installedVersion: "v1.7.2", status: Ready}
        │       kubelet: {installedVersion: "v1.29.0", status: Ready}
        │       etcd: {installedVersion: "v3.5.12", status: Ready}
        │       bkeAgent: {installedVersion: "v1.1.0", status: Ready}
        │
        ├── node-192.168.1.11 (master)
        │   ... (同上)
        │
        └── node-192.168.1.20 (worker)
            spec:
              connection: {host: "192.168.1.20", port: 22}
              roles: [worker]
              components:
                containerd: {version: "v1.7.2"}
                kubelet: {version: "v1.29.0"}
                bkeAgent: {version: "v1.1.0"}
                # 无 etcd（worker 节点不需要）
            status:
              phase: Ready
              componentStatus:
                containerd: {installedVersion: "v1.7.2", status: Ready}
                kubelet: {installedVersion: "v1.29.0", status: Ready}
                bkeAgent: {installedVersion: "v1.1.0", status: Ready}
```
## 8. 命名约定
| 规则 | 示例 |
|------|------|
| NodeConfig 命名 | `node-<ip>`，如 `node-192.168.1.10` |
| Namespace | 与 BKECluster 相同 |
| OwnerReference | 指向 BKECluster |
| Label | `cluster.x-k8s.io/cluster-name: <bkecluster-name>` |
## 9. 关键设计决策
| # | 决策 | 理由 |
|---|------|------|
| 1 | NodeConfig 与 ComponentVersion 分离 | 职责分离：配置与执行解耦，同一 ComponentVersion 可被不同 NodeConfig 引用 |
| 2 | 认证方式改为 Secret 引用 | 比 BKENode 的明文 `username/password` 更安全 |
| 3 | 组件配置均为 Optional 指针 | 不同角色的节点拥有不同的组件集（如 worker 无 etcd） |
| 4 | NodeConfig 由 ClusterOrchestrator 自动生成 | 用户不直接创建 NodeConfig，从 BKECluster Spec 自动推导 |
| 5 | 缩容通过 NodeConfig phase 触发 | 不引入额外 CRD，复用 NodeConfig 生命周期 |
| 6 | 组件级状态追踪在 NodeConfig.Status | 比在 ComponentVersion.Status 中按节点追踪更自然，NodeConfig 是节点维度的状态聚合 |

# NodeConfig 详细设计
基于 stage4-2.md 的完整内容，结合对现有代码库（BKENode、BKECluster Spec）的分析，输出 NodeConfig 详细设计。
## 1. 概述
### 1.1 定位
NodeConfig 是节点级声明式配置 CRD，描述单个节点的连接信息、OS 信息和组件配置。它与 ComponentVersion 形成"配置-执行"分离模型：

| 维度 | NodeConfig | ComponentVersion |
|------|-----------|-----------------|
| 描述内容 | 节点"需要什么" | 组件"如何安装/升级" |
| 粒度 | 节点级（每个节点一个实例） | 组件级（每个组件一个实例） |
| 关联方式 | `spec.components.<name>.version` 声明期望版本 | `spec.version` + `spec.installAction/upgradeAction` 声明执行方式 |
| Scope | 始终 Node | Node 或 Cluster |
### 1.2 设计目标
1. **声明式节点配置**：替代当前 BKECluster.Spec.Nodes 中的命令式节点定义
2. **组件级状态追踪**：每个节点上每个组件的安装版本和状态独立追踪
3. **节点差异化配置**：支持节点级覆盖集群默认配置（如特定节点的 kubelet 参数）
4. **安全认证**：SSH 认证信息通过 Secret 引用，替代 BKENode 中的明文 username/password
5. **生命周期管理**：覆盖安装、升级、缩容完整生命周期
### 1.3 与现有 BKENode 的演进关系
当前 [bkenode_types.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkenode_types.go) 中的 `BKENode` 是轻量级节点定义，仅包含连接信息和基本状态。NodeConfig 在此基础上扩展：
```
BKENode（现有）                    NodeConfig（新增）
├── spec.ip                    →  spec.connection.host
├── spec.port                  →  spec.connection.port
├── spec.username/password     →  spec.connection.sshKeySecret（更安全）
├── spec.role                  →  spec.roles
├── spec.controlPlane          →  spec.components.etcd/kubelet（拆分）
├── spec.kubelet               →  spec.components.kubelet
├── spec.labels                →  spec.components.kubelet.extraArgs
├── status.state               →  status.phase
├── (无)                       →  spec.os（新增）
├── (无)                       →  spec.components.containerd（新增）
├── (无)                       →  spec.components.bkeAgent（新增）
├── (无)                       →  spec.components.nodesEnv（新增）
├── (无)                       →  spec.components.postProcess（新增）
└── (无)                       →  status.componentStatus（新增：组件级追踪）
```
## 2. CRD 完整定义
### 2.1 NodeConfigSpec
```go
// api/nodecomponent/v1alpha1/nodeconfig_types.go
package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
    NodeConfigFinalizer = "nodeconfig.nodecomponent.io/finalizer"
)

type NodeConfigSpec struct {
    // Connection 节点连接信息
    // +optional
    Connection NodeConnection `json:"connection,omitempty"`

    // OS 节点操作系统信息
    // +optional
    OS NodeOSInfo `json:"os,omitempty"`

    // Components 节点级组件配置
    // +optional
    Components NodeComponents `json:"components,omitempty"`

    // Roles 节点角色列表
    // +optional
    Roles []NodeRole `json:"roles,omitempty"`
}
```
### 2.2 NodeConnection
```go
type NodeConnection struct {
    // Host 节点 IP 地址
    // +optional
    Host string `json:"host,omitempty"`

    // Port SSH 端口
    // +optional
    Port int `json:"port,omitempty"`

    // SSHKeySecret SSH 密钥 Secret 引用
    // 替代 BKENode 中的明文 username/password
    // +optional
    SSHKeySecret *SecretReference `json:"sshKeySecret,omitempty"`

    // AgentPort BKE Agent 监听端口
    // +optional
    AgentPort int `json:"agentPort,omitempty"`
}

type SecretReference struct {
    // Name Secret 名称
    Name string `json:"name"`

    // Namespace Secret 命名空间，默认与 NodeConfig 相同
    // +optional
    Namespace string `json:"namespace,omitempty"`
}
```
**与 BKENode 的映射**：

| BKENode 字段 | NodeConnection 字段 | 变更说明 |
|-------------|-------------------|---------|
| `spec.ip` (string) | `host` (string) | 字段名更语义化 |
| `spec.port` (string) | `port` (int) | 类型从 string 改为 int |
| `spec.username` + `spec.password` | `sshKeySecret` (SecretReference) | **安全增强**：不再明文存储，改为 Secret 引用 |
| (无) | `agentPort` (int) | **新增**：Agent 端口配置 |
### 2.3 NodeOSInfo
```go
type NodeOSInfo struct {
    // Type 操作系统类型
    // +optional
    // +kubebuilder:validation:Enum=linux;windows
    Type string `json:"type,omitempty"`

    // Distro 操作系统发行版
    // +optional
    // +kubebuilder:validation:Enum=centos;ubuntu;kylin;openeuler;hopeos;euleros;uos;debian
    Distro string `json:"distro,omitempty"`

    // Version 操作系统版本
    // +optional
    Version string `json:"version,omitempty"`

    // Arch 系统架构
    // +optional
    // +kubebuilder:validation:Enum=amd64;arm64
    Arch string `json:"arch,omitempty"`
}
```
**来源**：
- 首次创建时从 `BKECluster.Spec` 推导（如果 Spec 中有 OS 信息）
- 运行时由 BKEAgent 上报实际 OS 信息，写入 `NodeConfig.Status.OSInfo`

**与阶段五（OSProvider）的关系**：NodeOSInfo 为 OSProvider 提供节点 OS 上下文，但 NodeConfig 不依赖 OSProvider 接口。阶段五完成后，OSProvider 可通过 Watch NodeConfig 获取 OS 信息。
### 2.4 NodeRole
```go
type NodeRole string

const (
    // NodeRoleMaster 控制平面节点
    NodeRoleMaster NodeRole = "master"

    // NodeRoleWorker 工作节点
    NodeRoleWorker NodeRole = "worker"

    // NodeRoleEtcd 独立 Etcd 节点
    NodeRoleEtcd NodeRole = "etcd"
)
```
**与 BKENode 的映射**：`BKENode.Spec.Role []string` → `NodeConfig.Spec.Roles []NodeRole`，类型从 `string` 改为强类型 `NodeRole`。
### 2.5 NodeComponents
```go
type NodeComponents struct {
    // Containerd 容器运行时配置
    // +optional
    Containerd *ContainerdComponentConfig `json:"containerd,omitempty"`

    // Kubelet Kubelet 配置
    // +optional
    Kubelet *KubeletComponentConfig `json:"kubelet,omitempty"`

    // Etcd Etcd 配置（仅 master/etcd 节点）
    // +optional
    Etcd *EtcdComponentConfig `json:"etcd,omitempty"`

    // BKEAgent BKE Agent 配置
    // +optional
    BKEAgent *BKEAgentComponentConfig `json:"bkeAgent,omitempty"`

    // NodesEnv 节点环境初始化配置
    // +optional
    NodesEnv *NodesEnvComponentConfig `json:"nodesEnv,omitempty"`

    // PostProcess 节点后处理配置
    // +optional
    PostProcess *PostProcessComponentConfig `json:"postProcess,omitempty"`
}
```
**设计决策**：所有组件配置均为 Optional 指针，原因：
1. 不同角色的节点拥有不同的组件集（如 worker 无 etcd）
2. 未设置的组件表示该节点不需要安装该组件
3. 指针类型可区分"未设置"和"零值"
### 2.6 ContainerdComponentConfig
```go
type ContainerdComponentConfig struct {
    // Version 期望的 containerd 版本
    // +optional
    Version string `json:"version,omitempty"`

    // Config containerd 配置文件内容（config.toml）
    // +optional
    Config string `json:"config,omitempty"`

    // DataDir containerd 数据目录
    // +optional
    DataDir string `json:"dataDir,omitempty"`

    // Registry 镜像仓库配置
    // +optional
    Registry *RegistryConfig `json:"registry,omitempty"`

    // SystemdCgroup 是否使用 systemd cgroup 驱动
    // +optional
    SystemdCgroup bool `json:"systemdCgroup,omitempty"`
}

type RegistryConfig struct {
    // Mirrors 镜像仓库镜像配置
    // key: 仓库域名, value: mirror URL
    // +optional
    Mirrors map[string]string `json:"mirrors,omitempty"`
}
```
**映射 Phase**：
- 安装：EnsureNodesEnv（containerd 安装部分）
- 升级：EnsureContainerdUpgrade

**来源映射**：

| 字段 | 来源 | 说明 |
|------|------|------|
| `version` | `BKECluster.Spec.ClusterConfig.Cluster.ContainerdVersion` | 集群级默认 |
| `config` | `BKECluster.Spec.ClusterConfig.Cluster.ContainerdConfigRef` | 引用 ContainerdConfig CR |
| `dataDir` | 默认 `/var/lib/containerd` | 可节点级覆盖 |
| `registry` | `BKECluster.Spec.ClusterConfig.Cluster.ImageRepo` | 集群级默认 |
| `systemdCgroup` | 默认 `true` | K8s 1.22+ 推荐 |

**当前 Phase 逻辑对照**（[ensure_containerd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go)）：

当前 EnsureContainerdUpgrade 通过 `command.ENV` 在所有节点上执行 containerd 升级命令。重构后，ContainerdUpgrader 从 NodeConfig 获取每个节点的 containerd 配置（版本、数据目录等），逐节点执行升级。
### 2.7 KubeletComponentConfig
```go
type KubeletComponentConfig struct {
    // Version 期望的 kubelet 版本
    // +optional
    Version string `json:"version,omitempty"`

    // ExtraArgs kubelet 额外命令行参数
    // +optional
    ExtraArgs map[string]string `json:"extraArgs,omitempty"`

    // FeatureGates kubelet 特性门控
    // +optional
    FeatureGates map[string]bool `json:"featureGates,omitempty"`

    // Config kubelet 配置文件内容（yaml）
    // +optional
    Config string `json:"config,omitempty"`
}
```

**映射 Phase**：
- 安装：EnsureMasterInit、EnsureMasterJoin、EnsureWorkerJoin
- 升级：EnsureMasterUpgrade、EnsureWorkerUpgrade

**来源映射**：

| 字段 | 来源 | 说明 |
|------|------|------|
| `version` | `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` | 集群级默认 |
| `extraArgs` | `BKENode.Spec.Kubelet.ExtraArgs` | **节点级覆盖** |
| `featureGates` | `BKECluster.Spec.ClusterConfig.Cluster.Kubelet.FeatureGates` | 集群级默认 |
| `config` | 由 Controller 根据 KubeletConfiguration 生成 | |

**当前 Phase 逻辑对照**（[ensure_master_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go)）：

当前 EnsureMasterUpgrade 通过 `NeedExecute` 检查 `OpenFuyaoVersion` 变化，然后逐节点执行升级。重构后，KubernetesUpgrader 从 NodeConfig 获取每个节点的 kubelet 版本和配置，按 Master→Worker 顺序逐节点升级。
### 2.8 EtcdComponentConfig
```go
type EtcdComponentConfig struct {
    // Version 期望的 etcd 版本
    // +optional
    Version string `json:"version,omitempty"`

    // DataDir etcd 数据目录
    // +optional
    DataDir string `json:"dataDir,omitempty"`

    // ClientURLs etcd 客户端 URL 列表
    // +optional
    ClientURLs []string `json:"clientURLs,omitempty"`

    // PeerURLs etcd 集群通信 URL 列表
    // +optional
    PeerURLs []string `json:"peerURLs,omitempty"`
}
```

**映射 Phase**：
- 安装：EnsureMasterInit（etcd 作为控制平面的一部分）
- 升级：EnsureEtcdUpgrade

**来源映射**：

| 字段 | 来源 | 说明 |
|------|------|------|
| `version` | `BKECluster.Spec.ClusterConfig.Cluster.EtcdVersion` | 集群级默认 |
| `dataDir` | `BKENode.Spec.ControlPlane.Etcd.DataDir` | **节点级覆盖** |
| `clientURLs` | 根据 `NodeConfig.Spec.Connection.Host` 自动生成 | `https://<host>:2379` |
| `peerURLs` | 根据 `NodeConfig.Spec.Connection.Host` 自动生成 | `https://<host>:2380` |
### 2.9 BKEAgentComponentConfig
```go
type BKEAgentComponentConfig struct {
    // Version 期望的 BKE Agent 版本
    // +optional
    Version string `json:"version,omitempty"`

    // Config Agent 配置文件内容
    // +optional
    Config string `json:"config,omitempty"`

    // Kubeconfig Agent 使用的 kubeconfig
    // 安装阶段指向引导集群，AgentSwitch 后指向目标集群
    // +optional
    Kubeconfig string `json:"kubeconfig,omitempty"`
}
```
**映射 Phase**：
- 安装：EnsureBKEAgent
- 升级：EnsureAgentUpgrade
- 切换：EnsureAgentSwitch

**来源映射**：

| 字段 | 来源 | 说明 |
|------|------|------|
| `version` | 从 `OpenFuyaoVersion` 推导 | Agent 版本与 openFuyao 版本绑定 |
| `config` | 由 Controller 根据集群配置生成 | 包含 API Server 地址、健康端口等 |
| `kubeconfig` | 由 Controller 生成 | 安装阶段用引导集群 kubeconfig，切换后用目标集群 kubeconfig |

**当前 Phase 逻辑对照**（[ensure_bke_agent.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go)）：

当前 EnsureBKEAgent 通过 SSH 推送 Agent 二进制到节点，配置 kubeconfig，启动 Agent 服务。重构后，BKEAgentExecutor 从 NodeConfig 获取 Agent 版本和配置，通过 SSH 或 Agent 通道推送。
### 2.10 NodesEnvComponentConfig
```go
type NodesEnvComponentConfig struct {
    // ExtraScripts 额外环境初始化脚本列表
    // +optional
    ExtraScripts []string `json:"extraScripts,omitempty"`

    // HTTPRepo HTTP 软件源地址
    // +optional
    HTTPRepo string `json:"httpRepo,omitempty"`

    // ImageRepo 镜像仓库地址
    // +optional
    ImageRepo string `json:"imageRepo,omitempty"`
}
```
**映射 Phase**：EnsureNodesEnv

**来源映射**：

| 字段 | 来源 | 说明 |
|------|------|------|
| `extraScripts` | 用户自定义 | 节点级额外初始化脚本 |
| `httpRepo` | `BKECluster.Spec.ClusterConfig.Cluster.HTTPRepo` | RPM/DEB 软件源 |
| `imageRepo` | `BKECluster.Spec.ClusterConfig.Cluster.ImageRepo` | 容器镜像仓库 |

**当前 Phase 逻辑对照**（EnsureNodesEnv）：

当前 EnsureNodesEnv 在节点上执行一系列环境初始化脚本（安装 lxcfs、nfs-utils、etcdctl、helm、calicoctl、更新 runc 等），通过 `command.ENV` 创建命令资源下发。重构后，NodesEnvExecutor 从 NodeConfig 获取软件源和脚本列表，按配置执行。
### 2.11 PostProcessComponentConfig
```go
type PostProcessComponentConfig struct {
    // Scripts 后处理脚本列表
    // +optional
    Scripts []string `json:"scripts,omitempty"`
}
```
**映射 Phase**：EnsureNodesPostProcess

**来源**：当前 EnsureNodesPostProcess 在节点上执行后处理脚本（如标签、污点设置等）。重构后，PostProcessExecutor 从 NodeConfig 获取脚本列表执行。
### 2.12 NodeConfigStatus
```go
type NodeConfigStatus struct {
    // Phase 节点当前生命周期阶段
    // +optional
    Phase NodeConfigPhase `json:"phase,omitempty"`

    // ComponentStatus 各组件安装状态
    // key 为组件名称（如 "containerd", "kubelet"）
    // +optional
    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`

    // OSInfo 实际 OS 详细信息（由 Agent 上报）
    // +optional
    OSInfo *NodeOSDetailInfo `json:"osInfo,omitempty"`

    // LastOperation 最近一次操作记录
    // +optional
    LastOperation *LastOperation `json:"lastOperation,omitempty"`

    // Conditions 条件集合
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}
```
### 2.13 NodeConfigPhase
```go
type NodeConfigPhase string

const (
    // NodeConfigPhasePending 已创建，等待执行安装
    NodeConfigPhasePending NodeConfigPhase = "Pending"

    // NodeConfigPhaseInstalling 正在执行首次安装
    NodeConfigPhaseInstalling NodeConfigPhase = "Installing"

    // NodeConfigPhaseUpgrading 正在执行版本升级
    NodeConfigPhaseUpgrading NodeConfigPhase = "Upgrading"

    // NodeConfigPhaseReady 所有组件安装/升级完成，节点就绪
    NodeConfigPhaseReady NodeConfigPhase = "Ready"

    // NodeConfigPhaseFailed 安装/升级失败
    NodeConfigPhaseFailed NodeConfigPhase = "Failed"

    // NodeConfigPhaseDeleting 节点正在缩容删除
    NodeConfigPhaseDeleting NodeConfigPhase = "Deleting"
)
```
**生命周期状态转换**：
```
┌─────────┐     安装开始    ┌────────────┐     安装完成   ┌───────┐
│ Pending │ ──────────────→ │ Installing │ ─────────────→ │ Ready │
└─────────┘                 └─────┬──────┘                └───┬───┘
                                  │                           │
                              安装失败                     版本变更
                                  │                           │
                                  ↓                           ↓
                            ┌──────────┐              ┌───────────┐
                            │  Failed  │              │ Upgrading │
                            └────┬─────┘              └─────┬─────┘
                                 │                          │
                            人工修复/自动重试          升级完成/升级失败
                                 │                          │
                                 ↓                          ↓
                            ┌──────────┐              ┌──────────┐
                            │ Pending  │              │  Ready   │
                            └──────────┘              └──────────┘
                                                         │
                                                  节点移除（缩容）
                                                         │
                                                         ↓
                                                  ┌──────────┐
                                                  │ Deleting │
                                                  └────┬─────┘
                                                       │
                                                  删除完成
                                                       │
                                                       ↓
                                                  (CR 被删除)
```
**与 BKENode NodeState 的映射**：

| BKENode NodeState | NodeConfigPhase | 说明 |
|-------------------|----------------|------|
| `NodePending` | `Pending` | 等待安装 |
| `NodeProvisioned` | `Installing` | 正在安装 |
| `NodeReady` | `Ready` | 就绪 |
| `NodeFailed` | `Failed` | 失败 |
| `NodeUpgrading` | `Upgrading` | 升级中 |
| `NodeDeleting` | `Deleting` | 删除中 |
| `NodeNotReady` | `Failed` 或 `Ready`（带 Degraded 条件） | 需结合 Conditions 判断 |
### 2.14 NodeComponentDetailStatus
```go
type NodeComponentDetailStatus struct {
    // InstalledVersion 已安装的组件版本
    // +optional
    InstalledVersion string `json:"installedVersion,omitempty"`

    // Status 组件当前状态
    // +optional
    Status ComponentPhase `json:"status,omitempty"`

    // LastUpdated 最后更新时间
    // +optional
    LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

    // Message 状态详情消息
    // +optional
    Message string `json:"message,omitempty"`
}
```
**ComponentPhase 复用 ComponentVersion 的阶段定义**：
```go
type ComponentPhase string

const (
    ComponentPhasePending     ComponentPhase = "Pending"
    ComponentPhaseInstalling  ComponentPhase = "Installing"
    ComponentPhaseUpgrading   ComponentPhase = "Upgrading"
    ComponentPhaseReady       ComponentPhase = "Ready"
    ComponentPhaseFailed      ComponentPhase = "Failed"
    ComponentPhaseRollingBack ComponentPhase = "RollingBack"
)
```
**状态追踪示例**：
```yaml
status:
  phase: Ready
  componentStatus:
    containerd:
      installedVersion: "v1.7.2"
      status: Ready
      lastUpdated: "2026-04-20T10:30:00Z"
    kubelet:
      installedVersion: "v1.29.0"
      status: Ready
      lastUpdated: "2026-04-20T10:35:00Z"
    etcd:
      installedVersion: "v3.5.12"
      status: Ready
      lastUpdated: "2026-04-20T10:32:00Z"
    bkeAgent:
      installedVersion: "v1.1.0"
      status: Ready
      lastUpdated: "2026-04-20T10:25:00Z"
    nodesEnv:
      installedVersion: "v1.0.0"
      status: Ready
      lastUpdated: "2026-04-20T10:20:00Z"
```
### 2.15 NodeOSDetailInfo
```go
type NodeOSDetailInfo struct {
    // KernelVersion 内核版本
    // +optional
    KernelVersion string `json:"kernelVersion,omitempty"`

    // OSImage OS 镜像信息
    // +optional
    OSImage string `json:"osImage,omitempty"`
}
```
**来源**：由 BKEAgent 上报，对应 `node.status.nodeInfo.kernelVersion` 和 `node.status.nodeInfo.osImage`。
### 2.16 LastOperation
```go
type LastOperation struct {
    // Type 操作类型：Install, Upgrade, Rollback, Delete
    // +optional
    Type string `json:"type"`

    // Component 操作涉及的组件名称
    // +optional
    Component ComponentName `json:"component,omitempty"`

    // StartTime 操作开始时间
    // +optional
    StartTime *metav1.Time `json:"startTime,omitempty"`

    // EndTime 操作结束时间
    // +optional
    EndTime *metav1.Time `json:"endTime,omitempty"`

    // Result 操作结果：Success, Failure, InProgress
    // +optional
    Result string `json:"result,omitempty"`

    // Message 操作详情消息
    // +optional
    Message string `json:"message,omitempty"`
}
```
### 2.17 NodeConfig 完整类型
```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="HOST",type="string",JSONPath=".spec.connection.host"
// +kubebuilder:printcolumn:name="ROLE",type="string",JSONPath=".spec.roles"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
type NodeConfig struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   NodeConfigSpec   `json:"spec,omitempty"`
    Status NodeConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type NodeConfigList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []NodeConfig `json:"items"`
}
```
## 3. NodeConfig 生成逻辑
### 3.1 从 BKECluster Spec 推导
NodeConfig 由 ClusterOrchestrator 根据 BKECluster Spec 自动生成，用户不直接创建：
```go
// pkg/orchestrator/cluster_orchestrator.go

func (o *ClusterOrchestrator) buildNodeConfig(
    bkeCluster *bkev1beta1.BKECluster,
    node bkev1beta1.BKENode,
) *v1alpha1.NodeConfig {
    cluster := bkeCluster.Spec.ClusterConfig.Cluster
    isMaster := containsRole(node.Spec.Role, "master")
    isEtcd := containsRole(node.Spec.Role, "etcd")

    nc := &v1alpha1.NodeConfig{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("node-%s", node.Spec.IP),
            Namespace: bkeCluster.Namespace,
            Labels: map[string]string{
                "cluster.x-k8s.io/cluster-name": bkeCluster.Name,
            },
            OwnerReferences: []metav1.OwnerReference{
                {
                    APIVersion: bkev1beta1.GroupVersion.String(),
                    Kind:       "BKECluster",
                    Name:       bkeCluster.Name,
                    UID:        bkeCluster.UID,
                },
            },
        },
        Spec: v1alpha1.NodeConfigSpec{
            Connection: v1alpha1.NodeConnection{
                Host: node.Spec.IP,
                Port: parseInt(node.Spec.Port, 22),
                SSHKeySecret: &v1alpha1.SecretReference{
                    Name:      fmt.Sprintf("%s-node-ssh", bkeCluster.Name),
                    Namespace: bkeCluster.Namespace,
                },
                AgentPort: parseInt(cluster.AgentHealthPort, 10255),
            },
            Roles: convertRoles(node.Spec.Role),
            Components: v1alpha1.NodeComponents{
                BKEAgent: &v1alpha1.BKEAgentComponentConfig{
                    Version: deriveBKEAgentVersion(cluster.OpenFuyaoVersion),
                },
                NodesEnv: &v1alpha1.NodesEnvComponentConfig{
                    HTTPRepo:  cluster.HTTPRepo.URL,
                    ImageRepo: cluster.ImageRepo.URL,
                },
                Containerd: &v1alpha1.ContainerdComponentConfig{
                    Version:      cluster.ContainerdVersion,
                    SystemdCgroup: true,
                },
                Kubelet: &v1alpha1.KubeletComponentConfig{
                    Version: cluster.KubernetesVersion,
                },
            },
        },
    }

    // master/etcd 节点添加 etcd 配置
    if isMaster || isEtcd {
        nc.Spec.Components.Etcd = &v1alpha1.EtcdComponentConfig{
            Version:    cluster.EtcdVersion,
            DataDir:    "/var/lib/etcd",
            ClientURLs: []string{fmt.Sprintf("https://%s:2379", node.Spec.IP)},
            PeerURLs:   []string{fmt.Sprintf("https://%s:2380", node.Spec.IP)},
        }
    }

    // 节点级覆盖：BKENode 中的 ControlPlane 和 Kubelet 配置
    if node.Spec.Kubelet != nil {
        if nc.Spec.Components.Kubelet != nil {
            nc.Spec.Components.Kubelet.ExtraArgs = node.Spec.Kubelet.ExtraArgs
            nc.Spec.Components.Kubelet.FeatureGates = node.Spec.Kubelet.FeatureGates
        }
    }

    return nc
}
```
### 3.2 覆盖规则
NodeConfig 的组件配置采用**集群默认 + 节点覆盖**模式：
```
优先级（从低到高）：
1. Cluster 级默认配置（BKECluster.Spec.ClusterConfig.Cluster）
2. BKENode 级覆盖配置（BKECluster.Spec.Nodes[i].Spec.ControlPlane/Kubelet）
3. NodeConfig 直接修改（高级用户场景）
```

**覆盖示例**：
```yaml
# 集群级默认：所有节点 kubelet 版本 v1.29.0
# BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion: v1.29.0

# 节点级覆盖：特定节点额外参数
# BKENode.Spec.Kubelet.ExtraArgs: {"max-pods": "200"}

# 生成的 NodeConfig
spec:
  components:
    kubelet:
      version: v1.29.0          # 来自集群级默认
      extraArgs:
        max-pods: "200"          # 来自节点级覆盖
```
### 3.3 同步策略
ClusterOrchestrator 在每次 Reconcile 时同步 NodeConfig：
```go
func (o *ClusterOrchestrator) syncNodeConfigs(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) error {
    // 1. 从 BKECluster.Spec.Nodes 获取期望节点列表
    desiredNodes := bkeCluster.Spec.Nodes
    desiredNodeMap := make(map[string]bkev1beta1.BKENode)
    for _, node := range desiredNodes {
        desiredNodeMap[fmt.Sprintf("node-%s", node.Spec.IP)] = node
    }

    // 2. 获取现有 NodeConfig 列表
    existingNCs := &v1alpha1.NodeConfigList{}
    if err := o.client.List(ctx, existingNCs,
        client.InNamespace(bkeCluster.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": bkeCluster.Name},
    ); err != nil {
        return err
    }

    // 3. 计算增/删/改
    existingNCMap := make(map[string]*v1alpha1.NodeConfig)
    for i := range existingNCs.Items {
        existingNCMap[existingNCs.Items[i].Name] = &existingNCs.Items[i]
    }

    // 3a. 新增：期望中有但现有中没有
    for name, node := range desiredNodeMap {
        if _, exists := existingNCMap[name]; !exists {
            nc := o.buildNodeConfig(bkeCluster, node)
            if err := o.client.Create(ctx, nc); err != nil {
                return err
            }
        }
    }

    // 3b. 删除：现有中有但期望中没有（缩容）
    for name, existingNC := range existingNCMap {
        if _, exists := desiredNodeMap[name]; !exists {
            // 标记为 Deleting 而非直接删除，触发缩容流程
            existingNC.Status.Phase = v1alpha1.NodeConfigPhaseDeleting
            existingNC.Status.LastOperation = &v1alpha1.LastOperation{
                Type:      "Delete",
                StartTime: &metav1.Time{Time: time.Now()},
                Result:    "InProgress",
            }
            if err := o.client.Status().Update(ctx, existingNC); err != nil {
                return err
            }
        }
    }

    // 3c. 更新：期望和现有都有，检查 Spec 是否变化
    for name, node := range desiredNodeMap {
        existingNC, exists := existingNCMap[name]
        if !exists {
            continue
        }
        desiredNC := o.buildNodeConfig(bkeCluster, node)
        if !reflect.DeepEqual(existingNC.Spec, desiredNC.Spec) {
            existingNC.Spec = desiredNC.Spec
            if err := o.client.Update(ctx, existingNC); err != nil {
                return err
            }
        }
    }

    return nil
}
```
## 4. NodeConfig 与 ComponentVersion 的协作
### 4.1 Scope 区分与 NodeConfig 匹配
| ComponentVersion Scope | 需要 NodeConfig | 执行方式 |
|----------------------|----------------|---------|
| `Node` | **是** | 传入匹配的 NodeConfig 列表，逐节点执行 |
| `Cluster` | **否** | 不传入 NodeConfig，集群级执行一次 |

**匹配规则**：ComponentExecutor 通过 ComponentVersion 的 `installAction.nodeSelector` / `upgradeAction.nodeSelector` 匹配 NodeConfig：
```go
func matchNodeConfig(nc *v1alpha1.NodeConfig, selector *v1alpha1.NodeSelector) bool {
    if selector == nil {
        return true
    }
    // Roles 匹配
    if len(selector.Roles) > 0 {
        for _, selectorRole := range selector.Roles {
            for _, nodeRole := range nc.Spec.Roles {
                if selectorRole == string(nodeRole) {
                    return true
                }
            }
        }
        return false
    }
    // Labels 匹配
    if len(selector.Labels) > 0 {
        for key, value := range selector.Labels {
            if nodeValue, ok := nc.Labels[key]; !ok || nodeValue != value {
                return false
            }
        }
    }
    return true
}
```

**匹配示例**：

| ComponentVersion | NodeSelector.Roles | 匹配的 NodeConfig |
|-----------------|-------------------|------------------|
| bkeAgent | `["master", "worker", "etcd"]` | 所有节点 |
| containerd | `["master", "worker", "etcd"]` | 所有节点 |
| etcd | `["master", "etcd"]` | 仅 master/etcd 节点 |
| kubernetes (MasterInit/MasterUpgrade) | `["master"]` | 仅 master 节点 |
| kubernetes (WorkerJoin/WorkerUpgrade) | `["worker"]` | 仅 worker 节点 |
### 4.2 执行流程
```
ClusterOrchestrator.Reconcile()
    │
    ├── 1. syncDesiredState()
    │   ├── 生成/更新 ComponentVersion[]
    │   └── 生成/更新 NodeConfig[]
    │
    ├── 2. DAGScheduler.Schedule()
    │   └── 计算就绪/阻塞/运行中组件
    │
    └── 3. executeComponent()
        │
        ├── Scope=Cluster:
        │   executor.Install(ctx, cv, nil)
        │   └── 不需要 NodeConfig
        │
        └── Scope=Node:
            nodeConfigs = getNodeConfigsForComponent(cv)
            executor.Install(ctx, cv, nodeConfigs)
            └── 逐节点执行，更新每个 NodeConfig.Status.ComponentStatus
```
### 4.3 getNodeConfigsForComponent 实现
```go
func (o *ClusterOrchestrator) getNodeConfigsForComponent(
    ctx context.Context,
    cv *v1alpha1.ComponentVersion,
) ([]*v1alpha1.NodeConfig, error) {
    // 获取集群所有 NodeConfig
    ncList := &v1alpha1.NodeConfigList{}
    if err := o.client.List(ctx, ncList,
        client.InNamespace(cv.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": cv.Labels["cluster.x-k8s.io/cluster-name"]},
    ); err != nil {
        return nil, err
    }

    // 根据 NodeSelector 过滤
    var matched []*v1alpha1.NodeConfig
    var selector *v1alpha1.NodeSelector
    if cv.Spec.InstallAction != nil {
        selector = cv.Spec.InstallAction.NodeSelector
    }

    for i := range ncList.Items {
        nc := &ncList.Items[i]
        // 跳过正在删除的节点
        if nc.Status.Phase == v1alpha1.NodeConfigPhaseDeleting {
            continue
        }
        if matchNodeConfig(nc, selector) {
            matched = append(matched, nc)
        }
    }

    return matched, nil
}
```
### 4.4 组件状态更新流程
当 ComponentExecutor 在节点上执行安装/升级时，更新对应 NodeConfig 的组件状态：
```go
func updateNodeComponentStatus(
    ctx context.Context,
    client client.Client,
    nc *v1alpha1.NodeConfig,
    componentName v1alpha1.ComponentName,
    phase v1alpha1.ComponentPhase,
    version string,
    message string,
) error {
    if nc.Status.ComponentStatus == nil {
        nc.Status.ComponentStatus = make(map[string]v1alpha1.NodeComponentDetailStatus)
    }
    nc.Status.ComponentStatus[string(componentName)] = v1alpha1.NodeComponentDetailStatus{
        InstalledVersion: version,
        Status:           phase,
        LastUpdated:      &metav1.Time{Time: time.Now()},
        Message:          message,
    }

    // 更新 NodeConfig 整体 Phase
    nc.Status.Phase = deriveNodePhase(nc.Status.ComponentStatus)

    return client.Status().Update(ctx, nc)
}

func deriveNodePhase(
    componentStatus map[string]v1alpha1.NodeComponentDetailStatus,
) v1alpha1.NodeConfigPhase {
    hasFailed := false
    hasInstalling := false
    hasUpgrading := false
    allReady := true

    for _, status := range componentStatus {
        switch status.Status {
        case v1alpha1.ComponentPhaseFailed:
            hasFailed = true
        case v1alpha1.ComponentPhaseInstalling:
            hasInstalling = true
            allReady = false
        case v1alpha1.ComponentPhaseUpgrading:
            hasUpgrading = true
            allReady = false
        case v1alpha1.ComponentPhasePending:
            allReady = false
        }
    }

    if hasFailed {
        return v1alpha1.NodeConfigPhaseFailed
    }
    if hasUpgrading {
        return v1alpha1.NodeConfigPhaseUpgrading
    }
    if hasInstalling {
        return v1alpha1.NodeConfigPhaseInstalling
    }
    if allReady && len(componentStatus) > 0 {
        return v1alpha1.NodeConfigPhaseReady
    }
    return v1alpha1.NodeConfigPhasePending
}
```
## 5. 缩容场景设计
### 5.1 触发机制
EnsureWorkerDelete 和 EnsureMasterDelete 不映射为 ComponentVersion，通过 NodeConfig 的 `status.phase = Deleting` 触发缩容流程：
1. 用户从 BKECluster.Spec.Nodes 中移除节点
2. ClusterOrchestrator 检测到节点移除，将对应 NodeConfig 的 `status.phase` 设为 `Deleting`
3. NodeConfig Controller 执行缩容流程
4. 完成后删除 NodeConfig CR
### 5.2 缩容流程
```go
func (o *ClusterOrchestrator) handleNodeDeletion(
    ctx context.Context,
    nc *v1alpha1.NodeConfig,
) (ctrl.Result, error) {
    switch nc.Status.LastOperation.Type {
    case "", "DrainNode":
        // Step 1: Drain 节点
        if err := o.drainNode(ctx, nc); err != nil {
            return ctrl.Result{}, err
        }
        nc.Status.LastOperation = &v1alpha1.LastOperation{
            Type:      "DeleteMachine",
            StartTime: &metav1.Time{Time: time.Now()},
            Result:    "InProgress",
        }
        return ctrl.Result{RequeueAfter: 5 * time.Second}, o.client.Status().Update(ctx, nc)

    case "DeleteMachine":
        // Step 2: 删除 Machine 对象
        if err := o.deleteMachine(ctx, nc); err != nil {
            return ctrl.Result{}, err
        }
        nc.Status.LastOperation = &v1alpha1.LastOperation{
            Type:      "WaitNodeRemoved",
            StartTime: &metav1.Time{Time: time.Now()},
            Result:    "InProgress",
        }
        return ctrl.Result{RequeueAfter: 5 * time.Second}, o.client.Status().Update(ctx, nc)

    case "WaitNodeRemoved":
        // Step 3: 等待节点从集群中移除
        removed, err := o.isNodeRemoved(ctx, nc)
        if err != nil {
            return ctrl.Result{}, err
        }
        if !removed {
            return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
        }
        // Step 4: 删除 NodeConfig CR
        return ctrl.Result{}, o.client.Delete(ctx, nc)
    }

    return ctrl.Result{}, nil
}
```
### 5.3 NodeConfig 删除时的 Finalizer 处理
```go
func (r *NodeConfigReconciler) handleDeletion(
    ctx context.Context,
    nc *v1alpha1.NodeConfig,
) (ctrl.Result, error) {
    if !controllerutil.ContainsFinalizer(nc, v1alpha1.NodeConfigFinalizer) {
        return ctrl.Result{}, nil
    }

    // 执行清理逻辑（如移除集群中的 Node 对象）
    if err := r.cleanupNode(ctx, nc); err != nil {
        return ctrl.Result{}, err
    }

    // 移除 Finalizer
    controllerutil.RemoveFinalizer(nc, v1alpha1.NodeConfigFinalizer)
    return ctrl.Result{}, r.Update(ctx, nc)
}
```
## 6. NodeConfig Controller
### 6.1 Reconciler 定义
```go
// controllers/nodecomponent/nodeconfig_controller.go
package nodecomponent

type NodeConfigReconciler struct {
    client.Client
    Scheme *runtime.Scheme
}

func (r *NodeConfigReconciler) Reconcile(
    ctx context.Context,
    req ctrl.Request,
) (ctrl.Result, error) {
    nc := &v1alpha1.NodeConfig{}
    if err := r.Get(ctx, req.NamespacedName, nc); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 处理 Finalizer
    if !nc.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, nc)
    }

    // 根据阶段处理
    switch nc.Status.Phase {
    case "", v1alpha1.NodeConfigPhasePending:
        // 等待 ClusterOrchestrator 通过 ComponentExecutor 驱动安装
        return ctrl.Result{}, nil

    case v1alpha1.NodeConfigPhaseDeleting:
        // 缩容流程
        return r.handleDeletion(ctx, nc)

    case v1alpha1.NodeConfigPhaseFailed:
        // 等待人工干预或自动重试
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil

    case v1alpha1.NodeConfigPhaseInstalling, v1alpha1.NodeConfigPhaseUpgrading:
        // 安装/升级由 ComponentExecutor 驱动，此处仅做状态检查
        return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

    case v1alpha1.NodeConfigPhaseReady:
        // 定期健康检查
        return r.healthCheck(ctx, nc)
    }

    return ctrl.Result{}, nil
}
```
### 6.2 SetupWithManager
```go
func (r *NodeConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&v1alpha1.NodeConfig{}).
        Owns(&bkev1beta1.BKECluster).
        Complete(r)
}
```
## 7. 资源关系总览
```
BKECluster (Owner)
    │
    ├── ClusterVersion (1:1) ─── 全局版本状态
    │
    ├── ComponentVersion[] (1:N) ─── 各组件版本定义
    │   ├── bkeAgent-v1.1.0        (Scope=Node, selector: master,worker,etcd)
    │   ├── nodesEnv-v1.0.0        (Scope=Node, selector: master,worker,etcd)
    │   ├── clusterAPI-v1.0.0      (Scope=Cluster)
    │   ├── certs-v1.0.0           (Scope=Cluster)
    │   ├── loadBalancer-v1.0.0    (Scope=Node, selector: role=loadbalancer)
    │   ├── kubernetes-v1.29.0     (Scope=Node, selector: master,worker)
    │   ├── containerd-v1.7.2      (Scope=Node, selector: master,worker,etcd)
    │   ├── etcd-v3.5.12           (Scope=Node, selector: master,etcd)
    │   ├── addon-v1.0.0           (Scope=Cluster)
    │   ├── openFuyao-v1.1.0       (Scope=Cluster)
    │   ├── bkeProvider-v1.1.0     (Scope=Cluster)
    │   ├── nodesPostProcess-v1.0.0 (Scope=Node, selector: master,worker,etcd)
    │   └── agentSwitch-v1.0.0     (Scope=Cluster)
    │
    └── NodeConfig[] (1:N) ─── 节点级配置
        ├── node-192.168.1.10 (master)
        │   spec:
        │     connection: {host: "192.168.1.10", port: 22}
        │     roles: [master]
        │     components:
        │       containerd: {version: "v1.7.2", systemdCgroup: true}
        │       kubelet: {version: "v1.29.0"}
        │       etcd: {version: "v3.5.12", dataDir: "/var/lib/etcd"}
        │       bkeAgent: {version: "v1.1.0"}
        │       nodesEnv: {httpRepo: "https://repo.openfuyao.cn", imageRepo: "registry.openfuyao.cn"}
        │   status:
        │     phase: Ready
        │     componentStatus:
        │       containerd: {installedVersion: "v1.7.2", status: Ready}
        │       kubelet: {installedVersion: "v1.29.0", status: Ready}
        │       etcd: {installedVersion: "v3.5.12", status: Ready}
        │       bkeAgent: {installedVersion: "v1.1.0", status: Ready}
        │       nodesEnv: {installedVersion: "v1.0.0", status: Ready}
        │
        ├── node-192.168.1.11 (master)
        │   ... (同上，etcd clientURLs/peerURLs 不同)
        │
        └── node-192.168.1.20 (worker)
            spec:
              connection: {host: "192.168.1.20", port: 22}
              roles: [worker]
              components:
                containerd: {version: "v1.7.2", systemdCgroup: true}
                kubelet: {version: "v1.29.0"}
                bkeAgent: {version: "v1.1.0"}
                nodesEnv: {httpRepo: "https://repo.openfuyao.cn", imageRepo: "registry.openfuyao.cn"}
                # 无 etcd（worker 节点不需要）
            status:
              phase: Ready
              componentStatus:
                containerd: {installedVersion: "v1.7.2", status: Ready}
                kubelet: {installedVersion: "v1.29.0", status: Ready}
                bkeAgent: {installedVersion: "v1.1.0", status: Ready}
                nodesEnv: {installedVersion: "v1.0.0", status: Ready}
```
## 8. 命名约定与标签
### 8.1 命名规则
| 资源 | 命名规则 | 示例 |
|------|---------|------|
| NodeConfig | `node-<ip>` | `node-192.168.1.10` |
| Namespace | 与 BKECluster 相同 | `cluster-system` |
### 8.2 标签
| 标签 | 值 | 用途 |
|------|---|------|
| `cluster.x-k8s.io/cluster-name` | BKECluster.Name | 关联集群 |
| `nodecomponent.io/role` | master/worker/etcd | 节点角色（便于 Selector） |
| `nodecomponent.io/os-distro` | centos/ubuntu/... | OS 发行版（便于 OSProvider 匹配） |
### 8.3 OwnerReference
NodeConfig 的 OwnerReference 指向 BKECluster，确保集群删除时级联清理：
```yaml
ownerReferences:
  - apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
    kind: BKECluster
    name: my-cluster
    uid: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
    controller: true
    blockOwnerDeletion: true
```
## 9. 关键设计决策总结
| # | 决策 | 理由 | 替代方案及否决原因 |
|---|------|------|------------------|
| 1 | NodeConfig 与 ComponentVersion 分离 | 职责分离：配置与执行解耦，同一 ComponentVersion 可被不同 NodeConfig 引用 | 合并为单一 CRD：会导致节点配置和安装逻辑耦合，无法复用 |
| 2 | 认证方式改为 Secret 引用 | 比 BKENode 的明文 `username/password` 更安全 | 保留明文：不符合安全最佳实践 |
| 3 | 组件配置均为 Optional 指针 | 不同角色的节点拥有不同的组件集（如 worker 无 etcd） | 全部必填：worker 节点被迫填写不需要的 etcd 配置 |
| 4 | NodeConfig 由 ClusterOrchestrator 自动生成 | 用户不直接创建 NodeConfig，从 BKECluster Spec 自动推导 | 用户手动创建：增加操作复杂度，且与现有 BKECluster.Spec.Nodes 重复 |
| 5 | 缩容通过 NodeConfig phase 触发 | 不引入额外 CRD，复用 NodeConfig 生命周期 | 引入 NodeDeletionRequest CRD：增加 CRD 数量，且逻辑可复用 |
| 6 | 组件级状态追踪在 NodeConfig.Status | NodeConfig 是节点维度的状态聚合，比在 ComponentVersion.Status 中按节点追踪更自然 | 在 ComponentVersion.Status 中追踪：ComponentVersion 是组件维度，混合节点状态会导致 Status 膨胀 |
| 7 | NodeConfig 不依赖 OSProvider 接口 | NodeConfig 仅描述 OS 信息，不执行 OS 操作；OS 抽象是阶段五的职责 | 依赖 OSProvider：会引入与阶段五的耦合，违反独立实现原则 |
| 8 | NodeConfig 的 Spec 变更触发升级 | 当 `spec.components.<name>.version` 变化时，对应 ComponentExecutor 执行升级 | 需要显式 UpgradeRequest：增加操作复杂度，声明式模型应自动响应 Spec 变化 |
## 10. 与其他组件的接口
### 10.1 与 ClusterOrchestrator 的接口
| 操作 | 方向 | 说明 |
|------|------|------|
| `buildNodeConfig()` | ClusterOrchestrator → NodeConfig | 根据 BKECluster Spec 生成 NodeConfig |
| `syncNodeConfigs()` | ClusterOrchestrator → NodeConfig | 同步增/删/改 |
| `getNodeConfigsForComponent()` | ClusterOrchestrator → NodeConfig | 根据 NodeSelector 获取匹配的 NodeConfig 列表 |
| `updateNodeComponentStatus()` | ComponentExecutor → NodeConfig | 更新节点组件状态 |
### 10.2 与 ComponentVersion 的接口
| 操作 | 方向 | 说明 |
|------|------|------|
| `matchNodeConfig()` | ComponentVersion → NodeConfig | 根据 NodeSelector 匹配 |
| `spec.components.<name>.version` | NodeConfig → ComponentVersion | NodeConfig 声明期望版本，ComponentVersion 提供安装方式 |
### 10.3 与 BKECluster 的接口
| 操作 | 方向 | 说明 |
|------|------|------|
| `BKECluster.Spec.Nodes` | BKECluster → NodeConfig | 节点定义来源 |
| `BKECluster.Spec.ClusterConfig.Cluster` | BKECluster → NodeConfig | 集群级默认配置来源 |
| `NodeConfig.Status.ComponentStatus` | NodeConfig → BKECluster | 版本状态回写（通过 ClusterOrchestrator） |
### 10.4 与 BKEAgent 的接口
| 操作 | 方向 | 说明 |
|------|------|------|
| OS 信息上报 | BKEAgent → NodeConfig | Agent 上报实际 OS 信息到 `status.osInfo` |
| 组件状态上报 | BKEAgent → NodeConfig | Agent 上报组件安装状态到 `status.componentStatus` |
| 执行命令下发 | NodeConfig → BKEAgent | 通过 Agent 通道下发安装/升级命令 |

# NodeConfig 与 Cluster API Machine/BKEMachine 使用场景分析及整合评估
## 1. 三者定位与职责
### 1.1 Cluster API Machine（标准 CRD）
Machine 是 Cluster API 的核心原语，代表**集群中的一个逻辑节点**，定义在 `sigs.k8s.io/cluster-api/api/v1beta1`：
```
Machine (cluster.x-k8s.io)
├── spec.clusterName       # 所属集群
├── spec.bootstrap         # 引导配置（kubeadm 等）
├── spec.infrastructureRef # 基础设施提供者引用（→ BKEMachine）
├── spec.version           # Kubernetes 版本
└── status.nodeRef         # 关联的目标集群 Node 对象
```
**核心职责**：
- 声明集群期望的节点数量和版本
- 编排 Bootstrap Provider 和 Infrastructure Provider 的协作
- 跟踪节点是否已加入集群（`status.nodeRef`）
- **不关心**节点的物理连接信息、OS 信息、组件配置
### 1.2 BKEMachine（Infrastructure Provider CRD）
BKEMachine 是 BKE 实现的 Cluster API Infrastructure Provider，定义在 [bkemachine_types.go](file:///d:/code/github/cluster-api-provider-bke/api/capbke/v1beta1/bkemachine_types.go)：
```
BKEMachine (infrastructure.cluster.x-k8s.io)
├── spec.providerID        # 节点唯一标识
├── spec.pause             # 暂停调和
├── spec.dryRun            # 试运行
├── status.ready           # 基础设施是否就绪
├── status.bootstrapped    # 是否已完成引导
├── status.addresses       # 节点地址列表
├── status.node            # 关联的 BKENode 配置（完整拷贝）
└── status.conditions      # 条件集合
```
**核心职责**：
- 实现 Cluster API Infrastructure Provider 接口
- 从 BKENode 列表中**分配**物理节点给 Machine
- 创建 Bootstrap Command 下发到 Agent
- 等待 kubeadm init/join 执行完成
- 设置 `status.ready = true` 和 `status.bootstrapped = true`
- 处理节点删除（Reset Command）
### 1.3 NodeConfig（新增 CRD）
NodeConfig 是 stage4-2 提出的节点级声明式配置 CRD：
```
NodeConfig (nodecomponent.io)
├── spec.connection        # SSH/Agent 连接信息
├── spec.os                # 操作系统信息
├── spec.roles             # 节点角色
├── spec.components        # 组件配置（containerd/kubelet/etcd/bkeAgent/nodesEnv/postProcess）
└── status.phase           # 生命周期阶段
    status.componentStatus # 各组件安装状态
    status.osInfo          # 实际 OS 信息
    status.lastOperation   # 最近操作
    status.conditions      # 条件集合
```
**核心职责**：
- 声明节点期望的组件版本和配置
- 追踪每个组件的安装/升级状态
- 为 ComponentVersion Executor 提供节点级上下文
- 支撑节点级升级编排
## 2. 职责对比矩阵
| 维度 | Machine | BKEMachine | NodeConfig |
|------|---------|-----------|-----------|
| **所属 API 组** | `cluster.x-k8s.io` | `infrastructure.cluster.x-k8s.io` | `nodecomponent.io`（新增） |
| **定义者** | Cluster API 标准 | BKE Infrastructure Provider | BKE 组件管理 |
| **核心关注** | 节点逻辑存在 | 节点物理分配与引导 | 节点组件配置与状态 |
| **生命周期** | 创建→引导→就绪→删除 | 创建→分配节点→引导→就绪→删除 | Pending→Installing→Ready→Upgrading→Deleting |
| **连接信息** | ❌ | ❌（存在 status.node 但不直接使用） | ✅ `spec.connection` |
| **OS 信息** | ❌ | ❌ | ✅ `spec.os` |
| **组件配置** | 仅 `spec.version`（K8s 版本） | ❌ | ✅ `spec.components.*` |
| **组件状态** | ❌ | ❌ | ✅ `status.componentStatus` |
| **引导执行** | 委托 Bootstrap Provider | ✅ 创建 Command | ❌（由 ComponentVersion 驱动） |
| **节点分配** | ❌ | ✅ 从 BKENode 列表分配 | ❌（由 ClusterOrchestrator 生成） |
| **ProviderID** | `spec.providerID` | `spec.providerID` | ❌ |
| **NodeRef** | `status.nodeRef` | ❌ | ❌ |
| **升级编排** | 仅版本变更触发 | ❌ | ✅ 组件级升级状态追踪 |
## 3. 数据流与关系
### 3.1 当前架构（无 NodeConfig）
```
BKECluster.Spec.Nodes[] (BKENode[])
    │
    ├── EnsureClusterAPIObj Phase
    │   └── 创建 Machine[] + BKEMachine[]
    │
    ├── BKEMachine Controller
    │   ├── 从 BKENode[] 中分配节点
    │   ├── 创建 Bootstrap Command
    │   ├── 等待 Agent 执行 kubeadm init/join
    │   └── 设置 status.ready = true
    │
    └── Phase 体系
        ├── EnsureBKEAgent: SSH 推送 Agent
        ├── EnsureNodesEnv: SSH/Agent 执行环境初始化
        ├── EnsureContainerdUpgrade: SSH/Agent 执行升级
        └── EnsureMasterUpgrade/WorkerUpgrade: SSH/Agent 执行升级
```
**问题**：
1. Phase 体系与 BKEMachine Controller 并行运行，**没有统一的节点状态视图**
2. Phase 通过 BKENode 获取节点信息，BKEMachine 也通过 BKENode 分配节点，**存在竞态**
3. 升级操作（EnsureContainerdUpgrade 等）不经过 BKEMachine，**绕过了 Cluster API 的 Machine 生命周期**
### 3.2 重构后架构（引入 NodeConfig）
```
BKECluster.Spec.Nodes[] (BKENode[])
    │
    ├── ClusterOrchestrator
    │   ├── 生成 ComponentVersion[]
    │   └── 生成 NodeConfig[]    ← 新增
    │
    ├── BKEMachine Controller（保持不变）
    │   ├── 从 BKENode[] 中分配节点
    │   ├── 创建 Bootstrap Command
    │   └── 设置 status.ready = true
    │
    └── ComponentVersion Executor
        ├── Scope=Node: 传入 NodeConfig[]  ← 新增
        │   ├── 逐节点执行安装/升级
        │   └── 更新 NodeConfig.Status.ComponentStatus
        └── Scope=Cluster: 不需要 NodeConfig
```
## 4. 整合可行性分析
### 4.1 方案一：将 NodeConfig 整合进 BKEMachine
**思路**：在 BKEMachine 的 Spec/Status 中添加 NodeConfig 的字段。
```go
type BKEMachineSpec struct {
    ProviderID *string `json:"providerID,omitempty"`
    Pause      bool    `json:"pause,omitempty"`
    DryRun     bool    `json:"dryRun,omitempty"`
    
    // 新增：NodeConfig 字段
    Connection     *NodeConnection     `json:"connection,omitempty"`
    OS             *NodeOSInfo         `json:"os,omitempty"`
    Components     *NodeComponents     `json:"components,omitempty"`
}

type BKEMachineStatus struct {
    Ready       bool              `json:"ready"`
    Bootstrapped bool             `json:"bootstrapped"`
    Addresses   []MachineAddress  `json:"addresses,omitempty"`
    Conditions  clusterv1.Conditions `json:"conditions,omitempty"`
    Node        *confv1beta1.Node `json:"node,omitempty"`
    
    // 新增：NodeConfig 状态字段
    Phase          NodeConfigPhase  `json:"phase,omitempty"`
    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`
    OSInfo         *NodeOSDetailInfo `json:"osInfo,omitempty"`
    LastOperation  *LastOperation   `json:"lastOperation,omitempty"`
}
```
**优势**：
- 减少 CRD 数量，一个资源管理一个节点的全部信息
- BKEMachine 与 NodeConfig 天然 1:1 对应，避免关联查询
- 符合 Cluster API 的 Infrastructure Provider 模式

**劣势**：
- ❌ **违反 Cluster API 契约**：BKEMachine 是 Infrastructure Provider，职责是"提供基础设施就绪的节点"，不应包含组件配置和升级状态
- ❌ **API 组冲突**：BKEMachine 属于 `infrastructure.cluster.x-k8s.io`，NodeConfig 属于 `nodecomponent.io`，混合后语义不清
- ❌ **生命周期不同步**：Machine 的生命周期由 CAPI Machine Controller 管理，NodeConfig 的生命周期由 ClusterOrchestrator 管理，混合后调和逻辑复杂
- ❌ **升级解耦困难**：BKEMachine 的变更会触发 CAPI Machine Controller 的调和，而组件升级不应触发 Machine 级别的调和
- ❌ **无法独立演进**：NodeConfig 的 Schema 变更会影响 BKEMachine 的 API 兼容性
- ❌ **纳管集群问题**：纳管集群中 Machine/BKEMachine 可能不存在，但 NodeConfig 仍然需要

**结论**：❌ 不推荐。违反职责分离原则，且与 Cluster API 契约冲突。
### 4.2 方案二：将 BKEMachine 整合进 NodeConfig
**思路**：在 NodeConfig 中添加 BKEMachine 的基础设施字段，替代 BKEMachine。
```go
type NodeConfigSpec struct {
    Connection NodeConnection `json:"connection,omitempty"`
    OS         NodeOSInfo     `json:"os,omitempty"`
    Components NodeComponents `json:"components,omitempty"`
    Roles      []NodeRole     `json:"roles,omitempty"`
    
    // 新增：Infrastructure Provider 字段
    ProviderID  *string `json:"providerID,omitempty"`
    MachineRef  *corev1.ObjectReference `json:"machineRef,omitempty"` // 关联的 Machine
}

type NodeConfigStatus struct {
    Phase          NodeConfigPhase  `json:"phase,omitempty"`
    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`
    
    // 新增：Infrastructure Provider 状态
    Ready        bool              `json:"ready"`
    Bootstrapped bool              `json:"bootstrapped"`
    Addresses    []MachineAddress  `json:"addresses,omitempty"`
    NodeRef      *corev1.ObjectReference `json:"nodeRef,omitempty"` // 目标集群 Node
}
```
**优势**：
- 单一资源管理节点全生命周期
- 减少资源数量

**劣势**：
- ❌ **无法实现 Cluster API Infrastructure Provider 接口**：CAPI 要求 Infrastructure Provider 有独立的 CRD（如 BKEMachine），且必须满足 `cluster.x-k8s.io` 的 Label/Annotation 约定
- ❌ **CAPI Controller 无法识别**：Machine Controller 通过 `spec.infrastructureRef` 查找 Infrastructure Provider CRD，NodeConfig 不在查找路径中
- ❌ **RBAC 权限冲突**：NodeConfig 和 Infrastructure Provider 需要不同的 RBAC 权限
- ❌ **Controller 逻辑混合**：节点引导（kubeadm）和组件管理（containerd/etcd 升级）是完全不同的调和逻辑，混合会导致单个 Controller 过于复杂

**结论**：❌ 不推荐。无法满足 Cluster API 的 Infrastructure Provider 接口要求。
### 4.3 方案三：保持独立，通过 Reference 关联（推荐）
**思路**：NodeConfig 和 BKEMachine 保持独立 CRD，通过 Reference 互相关联。
```go
// NodeConfig 引用 BKEMachine
type NodeConfigSpec struct {
    Connection NodeConnection `json:"connection,omitempty"`
    OS         NodeOSInfo     `json:"os,omitempty"`
    Components NodeComponents `json:"components,omitempty"`
    Roles      []NodeRole     `json:"roles,omitempty"`
    
    // 关联 BKEMachine
    BKEMachineRef *corev1.ObjectReference `json:"bkeMachineRef,omitempty"`
}

// BKEMachine 引用 NodeConfig
type BKEMachineStatus struct {
    Ready       bool              `json:"ready"`
    Bootstrapped bool             `json:"bootstrapped"`
    Addresses   []MachineAddress  `json:"addresses,omitempty"`
    Conditions  clusterv1.Conditions `json:"conditions,omitempty"`
    Node        *confv1beta1.Node `json:"node,omitempty"`
    
    // 关联 NodeConfig
    NodeConfigRef *corev1.ObjectReference `json:"nodeConfigRef,omitempty"`
}
```
**关联关系**：
```
Machine (CAPI)
    │ OwnerReference
    └── BKEMachine (Infrastructure Provider)
            │ status.nodeConfigRef
            └── NodeConfig (Component Management)
                    │ spec.bkeMachineRef
                    └── BKEMachine

BKECluster (Owner)
    ├── Machine[] (CAPI)
    ├── BKEMachine[] (Infrastructure Provider)
    ├── NodeConfig[] (Component Management)
    └── ComponentVersion[] (Component Definition)
```
**优势**：
- ✅ **职责清晰**：每个 CRD 有明确的单一职责
- ✅ **符合 Cluster API 契约**：BKEMachine 保持标准 Infrastructure Provider 接口
- ✅ **独立演进**：NodeConfig Schema 变更不影响 BKEMachine
- ✅ **灵活关联**：纳管集群可以只有 NodeConfig 没有 BKEMachine
- ✅ **调和解耦**：BKEMachine Controller 处理引导，ComponentVersion Executor 处理安装/升级

**劣势**：
- 多一个 CRD，资源数量增加
- 需要维护 Reference 的一致性
- 查询时需要跨资源关联

**结论**：✅ 推荐。职责分离，符合 Cluster API 规范，支持独立演进。
## 5. 推荐方案详细设计
### 5.1 关联建立时机
```
时间线：
T1: ClusterOrchestrator 生成 NodeConfig[]（从 BKECluster.Spec.Nodes 推导）
T2: EnsureClusterAPIObj 创建 Machine[] + BKEMachine[]
T3: BKEMachine Controller 分配节点，设置 Label
T4: ClusterOrchestrator 检测 BKEMachine 就绪，建立双向 Reference
```
### 5.2 关联建立逻辑
```go
func (o *ClusterOrchestrator) syncNodeConfigBKEMachineRefs(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) error {
    // 1. 获取所有 BKEMachine
    bkeMachineList := &capbkev1.BKEMachineList{}
    if err := o.client.List(ctx, bkeMachineList,
        client.InNamespace(bkeCluster.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": bkeCluster.Name},
    ); err != nil {
        return err
    }

    // 2. 获取所有 NodeConfig
    ncList := &v1alpha1.NodeConfigList{}
    if err := o.client.List(ctx, ncList,
        client.InNamespace(bkeCluster.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": bkeCluster.Name},
    ); err != nil {
        return err
    }

    // 3. 通过 IP 地址建立关联
    for i := range bkeMachineList.Items {
        bkeMachine := &bkeMachineList.Items[i]
        if bkeMachine.Status.Node == nil {
            continue
        }
        nodeIP := bkeMachine.Status.Node.IP

        for j := range ncList.Items {
            nc := &ncList.Items[j]
            if nc.Spec.Connection.Host != nodeIP {
                continue
            }

            // 建立双向 Reference
            if nc.Spec.BKEMachineRef == nil {
                nc.Spec.BKEMachineRef = &corev1.ObjectReference{
                    APIVersion: capbkev1.GroupVersion.String(),
                    Kind:       "BKEMachine",
                    Name:       bkeMachine.Name,
                    Namespace:  bkeMachine.Namespace,
                }
                if err := o.client.Update(ctx, nc); err != nil {
                    return err
                }
            }

            if bkeMachine.Status.NodeConfigRef == nil {
                bkeMachine.Status.NodeConfigRef = &corev1.ObjectReference{
                    APIVersion: v1alpha1.GroupVersion.String(),
                    Kind:       "NodeConfig",
                    Name:       nc.Name,
                    Namespace:  nc.Namespace,
                }
                if err := o.client.Status().Update(ctx, bkeMachine); err != nil {
                    return err
                }
            }
            break
        }
    }
    return nil
}
```
### 5.3 关联使用场景
| 场景 | 使用方 | 通过 Reference 获取的信息 |
|------|--------|--------------------------|
| 组件安装/升级 | ComponentVersion Executor → NodeConfig | 节点连接信息、组件配置、OS 信息 |
| 引导完成通知 | BKEMachine Controller → NodeConfig | `status.bootstrapped = true` 表示节点已加入集群 |
| 升级前置检查 | ComponentVersion Executor → BKEMachine | `status.ready` 确认节点基础设施就绪 |
| 缩容协调 | ClusterOrchestrator → BKEMachine + NodeConfig | BKEMachine 删除触发 NodeConfig phase=Deleting |
| ProviderID 查询 | NodeConfig → BKEMachine | 获取 ProviderID 用于目标集群 Node 匹配 |
| 节点地址查询 | NodeConfig → BKEMachine | 获取 `status.addresses` 用于目标集群通信 |
### 5.4 纳管集群的特殊处理
纳管集群（`FullyControlled = false`）中不存在 Machine/BKEMachine，此时 NodeConfig 独立工作：
```go
func (o *ClusterOrchestrator) buildNodeConfig(
    bkeCluster *bkev1beta1.BKECluster,
    node bkev1beta1.BKENode,
) *v1alpha1.NodeConfig {
    nc := &v1alpha1.NodeConfig{...}

    // 纳管集群不设置 BKEMachineRef
    if !clusterutil.FullyControlled(bkeCluster) {
        nc.Spec.BKEMachineRef = nil
    }

    return nc
}
```
## 6. 整合评估结论
| 方案 | 可行性 | 推荐度 | 核心原因 |
|------|--------|--------|---------|
| 方案一：NodeConfig 整合进 BKEMachine | ❌ 不可行 | ⭐ | 违反 CAPI Infrastructure Provider 契约，职责混合 |
| 方案二：BKEMachine 整合进 NodeConfig | ❌ 不可行 | ⭐ | 无法满足 CAPI 接口要求，Controller 逻辑混合 |
| **方案三：保持独立 + Reference 关联** | ✅ 可行 | ⭐⭐⭐⭐⭐ | 职责分离，符合 CAPI 规范，支持独立演进 |

**最终建议**：采用方案三，NodeConfig 与 BKEMachine 保持独立 CRD，通过 `bkeMachineRef` / `nodeConfigRef` 双向关联。原因总结：
1. **Cluster API 契约约束**：BKEMachine 必须满足 Infrastructure Provider 接口，不能承载组件管理职责
2. **生命周期不同步**：Machine 生命周期由 CAPI 管理器管理，NodeConfig 生命周期由 ClusterOrchestrator 管理
3. **纳管集群兼容**：纳管集群无 BKEMachine 但需要 NodeConfig
4. **独立演进**：NodeConfig 的 Schema 变更（如新增组件类型）不影响 BKEMachine 的 API 兼容性
5. **调和解耦**：引导（kubeadm）和组件管理（containerd/etcd 升级）是完全不同的调和逻辑，分离后各自 Controller 职责单一


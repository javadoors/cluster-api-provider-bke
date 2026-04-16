# 阶段三的详细设计
现在我将完成阶段三的详细设计。基于对代码库的深入分析，我将设计 Bootstrap Provider 和 ControlPlane Provider 的分离方案。
## 阶段三：Bootstrap Provider + ControlPlane Provider 分离详细设计
### 一、当前架构问题分析
#### 1.1 职责混合现状
**Bootstrap 逻辑嵌入位置：**
- [ensure_nodes_env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go)：节点环境初始化（容器运行时、kubelet 安装）
- [ensure_master_init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go)：主节点初始化引导
- [ensure_master_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go)：主节点加入集群
- [ensure_worker_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go)：工作节点加入集群
- [bkemachine_controller_phases.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkemachine_controller_phases.go)：机器引导逻辑

**ControlPlane 逻辑嵌入位置：**
- [ensure_master_init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go)：控制平面初始化
- [ensure_master_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go)：控制平面节点加入
- [bkecluster_spec.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_spec.go)：`ControlPlane` 配置字段
- [bkecluster_consts.go](file:///d:/code/github/cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_consts.go)：`ControlPlaneInitializedCondition` 条件管理
#### 1.2 CRD 职责过重
**BKECluster CRD 当前职责：**
```go
type BKEClusterSpec struct {
    ControlPlaneEndpoint APIEndpoint  // 基础设施 + 控制平面
    ClusterConfig *BKEConfig          // 混合配置
    // ClusterConfig 包含：
    // - ControlPlane 配置（控制平面职责）
    // - Kubelet 配置（引导职责）
    // - Networking 配置（基础设施职责）
    // - ContainerRuntime 配置（引导职责）
}
```
**BKEMachine CRD 当前职责：**
```go
type BKEMachineStatus struct {
    Ready bool              // 基础设施状态
    Bootstrapped bool       // 引导状态
    Node *Node              // 节点信息（混合）
}
```
### 二、目标架构设计
#### 2.1 Provider 职责划分
```
┌─────────────────────────────────────────────────────────────────┐
│                    Cluster API Provider BKE                     │
│                                                                 │
│  ┌───────────────────┐  ┌──────────────────┐  ┌─────────────┐ │
│  │ Infrastructure    │  │ Bootstrap        │  │ ControlPlane│ │
│  │ Provider          │  │ Provider         │  │ Provider    │ │
│  │                   │  │                  │  │             │ │
│  │ • BKECluster      │  │ • BKEBootstrap   │  │ • BKEControl│ │
│  │ • BKEMachine      │  │ • BKEBootstrap   │  │   Plane     │ │
│  │                   │  │   Template       │  │ • BKEControl│ │
│  │ 职责：            │  │                   │  │   Plane     │ │
│  │ • 节点基础设施     │  │ 职责：            │  │   Template  │ │
│  │ • SSH 访问        │  │ • 节点环境初始化   │  │             │ │
│  │ • 网络配置        │   │ • 容器运行时安装  │  │ 职责：      │ │
│  │ • 存储配置         │  │ • Kubelet 安装   │  │ • 控制平面  │ │
│  │ • 节点生命周期     │  │ • 加入集群        │  │   初始化    │ │
│  │                   │  │ • 证书分发        │  │ • 控制平面  │ │
│  │                   │  │                  │  │   扩缩容    │ │
│  │                   │  │                  │  │ • 证书管理   │ │
│  └───────────────────┘  └──────────────────┘  └─────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```
#### 2.2 CRD 分离设计
**新增 CRD：**
1. **BKEBootstrap** - 引导配置
2. **BKEBootstrapTemplate** - 引导模板
3. **BKEControlPlane** - 控制平面配置
4. **BKEControlPlaneTemplate** - 控制平面模板

**保留 CRD（职责简化）：**
- **BKECluster** - 仅基础设施配置
- **BKEMachine** - 仅基础设施状态
### 三、详细 CRD 设计
#### 3.1 BKEBootstrap CRD
```go
// api/bootstrap/v1beta1/bkebootstrap_types.go

package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
    BKEBootstrapFinalizer = "bkebootstrap.bootstrap.cluster.x-k8s.io"
)

type BootstrapPhase string

const (
    BootstrapPhasePending    BootstrapPhase = "Pending"
    BootstrapPhaseRunning    BootstrapPhase = "Running"
    BootstrapPhaseSucceeded  BootstrapPhase = "Succeeded"
    BootstrapPhaseFailed     BootstrapPhase = "Failed"
)

type BKEBootstrapSpec struct {
    // 节点环境配置
    Environment BootstrapEnvironment `json:"environment"`
    
    // 容器运行时配置
    ContainerRuntime ContainerRuntimeConfig `json:"containerRuntime"`
    
    // Kubelet 配置
    Kubelet KubeletConfig `json:"kubelet"`
    
    // 加入集群配置
    JoinConfiguration JoinConfiguration `json:"joinConfiguration,omitempty"`
    
    // 证书配置
    Certificates CertificateConfig `json:"certificates,omitempty"`
    
    // 超时配置
    Timeout metav1.Duration `json:"timeout,omitempty"`
    
    // 是否为控制平面节点
    ControlPlane bool `json:"controlPlane"`
}

type BootstrapEnvironment struct {
    // HTTP 软件源
    HTTPRepo Repo `json:"httpRepo,omitempty"`
    
    // 镜像源
    ImageRepo Repo `json:"imageRepo,omitempty"`
    
    // NTP 服务器
    NTPServer string `json:"ntpServer,omitempty"`
    
    // 额外执行脚本
    ExtraExecScripts []string `json:"extraExecScripts,omitempty"`
    
    // 额外 hosts 映射
    ExtraHosts []string `json:"extraHosts,omitempty"`
}

type ContainerRuntimeConfig struct {
    // CRI 类型：containerd, docker
    CRI string `json:"cri"`
    
    // Runtime 类型：runc, richrunc, kata
    Runtime string `json:"runtime,omitempty"`
    
    // 运行时参数
    Param map[string]string `json:"param,omitempty"`
    
    // Containerd 版本
    ContainerdVersion string `json:"containerdVersion,omitempty"`
}

type KubeletConfig struct {
    // Kubernetes 版本
    KubernetesVersion string `json:"kubernetesVersion"`
    
    // 额外参数
    ExtraArgs map[string]string `json:"extraArgs,omitempty"`
    
    // 额外卷挂载
    ExtraVolumes []HostPathMount `json:"extraVolumes,omitempty"`
    
    // 配置引用
    ConfigRef *KubeletConfigRef `json:"configRef,omitempty"`
}

type JoinConfiguration struct {
    // 控制平面端点
    ControlPlaneEndpoint string `json:"controlPlaneEndpoint"`
    
    // 加入 token
    BootstrapToken string `json:"bootstrapToken,omitempty"`
    
    // CA 证书哈希
    CACertHashes []string `json:"caCertHashes,omitempty"`
    
    // 是否为控制平面节点加入
    ControlPlane *JoinControlPlane `json:"controlPlane,omitempty"`
}

type JoinControlPlane struct {
    // 本地 API server 地址
    LocalAPIEndpoint string `json:"localAPIEndpoint,omitempty"`
}

type CertificateConfig struct {
    // 证书目录
    CertificatesDir string `json:"certificatesDir,omitempty"`
    
    // 是否需要分发证书
    DistributeCertificates bool `json:"distributeCertificates,omitempty"`
}

type BKEBootstrapStatus struct {
    // 引导阶段
    Phase BootstrapPhase `json:"phase,omitempty"`
    
    // 是否已完成引导
    Ready bool `json:"ready"`
    
    // 引导数据（Secret 引用）
    BootstrapDataRef *BootstrapDataReference `json:"bootstrapDataRef,omitempty"`
    
    // 条件
    Conditions []BootstrapCondition `json:"conditions,omitempty"`
    
    // 错误信息
    ErrorMessage string `json:"errorMessage,omitempty"`
    
    // 开始时间
    StartTime *metav1.Time `json:"startTime,omitempty"`
    
    // 完成时间
    CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}

type BootstrapDataReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
    DataKey   string `json:"dataKey"`
}

type BootstrapCondition struct {
    Type               BootstrapConditionType `json:"type"`
    Status             ConditionStatus        `json:"status"`
    LastTransitionTime *metav1.Time           `json:"lastTransitionTime,omitempty"`
    Reason             string                 `json:"reason,omitempty"`
    Message            string                 `json:"message,omitempty"`
}

type BootstrapConditionType string

const (
    BootstrapConditionEnvironmentReady    BootstrapConditionType = "EnvironmentReady"
    BootstrapConditionContainerRuntimeReady BootstrapConditionType = "ContainerRuntimeReady"
    BootstrapConditionKubeletReady        BootstrapConditionType = "KubeletReady"
    BootstrapConditionJoined              BootstrapConditionType = "Joined"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="READY",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
type BKEBootstrap struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   BKEBootstrapSpec   `json:"spec,omitempty"`
    Status BKEBootstrapStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BKEBootstrapList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []BKEBootstrap `json:"items"`
}
```
#### 3.2 BKEControlPlane CRD
```go
// api/controlplane/v1beta1/bkecontrolplane_types.go

package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
    BKEControlPlaneFinalizer = "bkecontrolplane.controlplane.cluster.x-k8s.io"
)

type ControlPlanePhase string

const (
    ControlPlanePhasePending      ControlPlanePhase = "Pending"
    ControlPlanePhaseInitializing ControlPlanePhase = "Initializing"
    ControlPlanePhaseRunning      ControlPlanePhase = "Running"
    ControlPlanePhaseScaling      ControlPlanePhase = "Scaling"
    ControlPlanePhaseUpgrading    ControlPlanePhase = "Upgrading"
    ControlPlanePhaseDeleting     ControlPlanePhase = "Deleting"
    ControlPlanePhaseFailed       ControlPlanePhase = "Failed"
)

type BKEControlPlaneSpec struct {
    // 副本数
    Replicas *int32 `json:"replicas"`
    
    // Kubernetes 版本
    Version string `json:"version"`
    
    // Etcd 版本
    EtcdVersion string `json:"etcdVersion,omitempty"`
    
    // 控制平面端点
    ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint"`
    
    // 控制平面组件配置
    ControlPlaneConfig ControlPlaneConfig `json:"controlPlaneConfig"`
    
    // 证书配置
    Certificates CertificateManagement `json:"certificates"`
    
    // 网络配置
    Networking ControlPlaneNetworking `json:"networking"`
    
    // 升级策略
    UpgradeStrategy ControlPlaneUpgradeStrategy `json:"upgradeStrategy,omitempty"`
    
    // 机器模板引用
    MachineTemplate clusterv1.ObjectReference `json:"machineTemplate"`
}

type APIEndpoint struct {
    Host string `json:"host"`
    Port int32  `json:"port"`
}

type ControlPlaneConfig struct {
    // ControllerManager 配置
    ControllerManager *ControlPlaneComponent `json:"controllerManager,omitempty"`
    
    // Scheduler 配置
    Scheduler *ControlPlaneComponent `json:"scheduler,omitempty"`
    
    // APIServer 配置
    APIServer *APIServerConfig `json:"apiServer,omitempty"`
    
    // Etcd 配置
    Etcd *EtcdConfig `json:"etcd,omitempty"`
}

type ControlPlaneComponent struct {
    // 额外参数
    ExtraArgs map[string]string `json:"extraArgs,omitempty"`
    
    // 额外卷挂载
    ExtraVolumes []HostPathMount `json:"extraVolumes,omitempty"`
}

type APIServerConfig struct {
    ControlPlaneComponent `json:",inline"`
    
    // 额外 SAN
    CertSANs []string `json:"certSANs,omitempty"`
}

type EtcdConfig struct {
    ControlPlaneComponent `json:",inline"`
    
    // 数据目录
    DataDir string `json:"dataDir,omitempty"`
    
    // 服务器证书 SAN
    ServerCertSANs []string `json:"serverCertSANs,omitempty"`
    
    // Peer 证书 SAN
    PeerCertSANs []string `json:"peerCertSANs,omitempty"`
}

type CertificateManagement struct {
    // 证书目录
    CertificatesDir string `json:"certificatesDir,omitempty"`
    
    // 证书有效期（年）
    CertificateValidityYears int `json:"certificateValidityYears,omitempty"`
    
    // 是否自动轮换
    AutoRotateCertificates bool `json:"autoRotateCertificates,omitempty"`
}

type ControlPlaneNetworking struct {
    // Service CIDR
    ServiceSubnet string `json:"serviceSubnet,omitempty"`
    
    // Pod CIDR
    PodSubnet string `json:"podSubnet,omitempty"`
    
    // DNS 域
    DNSDomain string `json:"dnsDomain,omitempty"`
}

type ControlPlaneUpgradeStrategy struct {
    // 升级类型：RollingUpdate, Recreate
    Type UpgradeType `json:"type,omitempty"`
    
    // 滚动更新配置
    RollingUpdate *RollingUpdateConfig `json:"rollingUpdate,omitempty"`
}

type UpgradeType string

const (
    RollingUpdateUpgrade UpgradeType = "RollingUpdate"
    RecreateUpgrade      UpgradeType = "Recreate"
)

type RollingUpdateConfig struct {
    // 最大不可用数
    MaxUnavailable *int `json:"maxUnavailable,omitempty"`
    
    // 最大激增数
    MaxSurge *int `json:"maxSurge,omitempty"`
}

type BKEControlPlaneStatus struct {
    // 控制平面阶段
    Phase ControlPlanePhase `json:"phase,omitempty"`
    
    // 是否就绪
    Ready bool `json:"ready"`
    
    // 是否已初始化
    Initialized bool `json:"initialized"`
    
    // 当前副本数
    Replicas int32 `json:"replicas"`
    
    // 就绪副本数
    ReadyReplicas int32 `json:"readyReplicas"`
    
    // 更新副本数
    UpdatedReplicas int32 `json:"updatedReplicas"`
    
    // 当前版本
    Version string `json:"version,omitempty"`
    
    // 条件
    Conditions []ControlPlaneCondition `json:"conditions,omitempty"`
    
    // 节点选择器
    Selector string `json:"selector,omitempty"`
    
    // 失败原因
    FailureReason string `json:"failureReason,omitempty"`
    
    // 失败消息
    FailureMessage string `json:"failureMessage,omitempty"`
}

type ControlPlaneCondition struct {
    Type               ControlPlaneConditionType `json:"type"`
    Status             ConditionStatus           `json:"status"`
    LastTransitionTime *metav1.Time              `json:"lastTransitionTime,omitempty"`
    Reason             string                    `json:"reason,omitempty"`
    Message            string                    `json:"message,omitempty"`
}

type ControlPlaneConditionType string

const (
    ControlPlaneConditionInitialized         ControlPlaneConditionType = "Initialized"
    ControlPlaneConditionAPIServerReady      ControlPlaneConditionType = "APIServerReady"
    ControlPlaneConditionControllerManagerReady ControlPlaneConditionType = "ControllerManagerReady"
    ControlPlaneConditionSchedulerReady      ControlPlaneConditionType = "SchedulerReady"
    ControlPlaneConditionEtcdReady           ControlPlaneConditionType = "EtcdReady"
    ControlPlaneConditionCertificatesReady   ControlPlaneConditionType = "CertificatesReady"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="READY",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="REPLICAS",type="integer",JSONPath=".status.replicas"
// +kubebuilder:printcolumn:name="VERSION",type="string",JSONPath=".status.version"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
type BKEControlPlane struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   BKEControlPlaneSpec   `json:"spec,omitempty"`
    Status BKEControlPlaneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BKEControlPlaneList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []BKEControlPlane `json:"items"`
}
```
#### 3.3 简化后的 BKECluster CRD
```go
// api/capbke/v1beta1/bkecluster_types.go（简化后）

package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type BKEClusterSpec struct {
    // 控制平面端点（保留，基础设施层需要）
    ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint"`
    
    // 基础设施模式
    InfrastructureMode InfrastructureMode `json:"infrastructureMode,omitempty"`
    
    // 用户提供的 infrastructure（UPI 模式）
    UserProvidedInfrastructure *UserProvidedInfrastructure `json:"userProvidedInfrastructure,omitempty"`
    
    // 节点池配置
    NodePools []NodePoolConfig `json:"nodePools,omitempty"`
    
    // 暂停标志
    Pause bool `json:"pause,omitempty"`
    
    // DryRun 标志
    DryRun bool `json:"dryRun,omitempty"`
    
    // 重置标志
    Reset bool `json:"reset,omitempty"`
}

type InfrastructureMode string

const (
    InfrastructureModeIPI InfrastructureMode = "IPI"
    InfrastructureModeUPI InfrastructureMode = "UPI"
)

type UserProvidedInfrastructure struct {
    // 用户提供的节点
    Nodes []UserProvidedNode `json:"nodes,omitempty"`
    
    // 用户提供的负载均衡器
    LoadBalancer *LoadBalancerConfig `json:"loadBalancer,omitempty"`
}

type UserProvidedNode struct {
    IP       string   `json:"ip"`
    Port     string   `json:"port,omitempty"`
    Username string   `json:"username,omitempty"`
    Password string   `json:"password,omitempty"`
    Roles    []string `json:"roles"`
}

type LoadBalancerConfig struct {
    Endpoint    string `json:"endpoint"`
    Certificate string `json:"certificate,omitempty"`
}

type NodePoolConfig struct {
    Name string `json:"name"`
    
    // 节点模板引用
    NodeTemplateRef *ObjectReference `json:"nodeTemplateRef,omitempty"`
    
    // 最小节点数
    MinSize int `json:"minSize,omitempty"`
    
    // 最大节点数
    MaxSize int `json:"maxSize,omitempty"`
}

type BKEClusterStatus struct {
    // 基础设施就绪
    Ready bool `json:"ready"`
    
    // 基础设施阶段
    Phase InfrastructurePhase `json:"phase,omitempty"`
    
    // 节点状态
    Nodes NodeStatusSummary `json:"nodes,omitempty"`
    
    // 条件
    Conditions []ClusterCondition `json:"conditions,omitempty"`
}

type InfrastructurePhase string

const (
    InfrastructurePhasePending      InfrastructurePhase = "Pending"
    InfrastructurePhaseProvisioning InfrastructurePhase = "Provisioning"
    InfrastructurePhaseReady        InfrastructurePhase = "Ready"
    InfrastructurePhaseDeleting     InfrastructurePhase = "Deleting"
    InfrastructurePhaseFailed       InfrastructurePhase = "Failed"
)

type NodeStatusSummary struct {
    Total   int `json:"total"`
    Ready   int `json:"ready"`
    Failed  int `json:"failed"`
    Pending int `json:"pending"`
}
```
### 四、控制器设计
#### 4.1 BKEBootstrap Controller
```go
// controllers/bootstrap/bkebootstrap_controller.go

package bootstrap

import (
    "context"
    
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    bootstrapv1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bootstrap/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/bootstrap/engine"
)

type BKEBootstrapReconciler struct {
    client.Client
    
    // 引导引擎
    BootstrapEngine engine.BootstrapEngine
}

func (r *BKEBootstrapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bootstrap := &bootstrapv1.BKEBootstrap{}
    if err := r.Get(ctx, req.NamespacedName, bootstrap); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 使用引导引擎处理
    return r.BootstrapEngine.Reconcile(ctx, bootstrap)
}

// 引导引擎接口
type BootstrapEngine interface {
    Reconcile(ctx context.Context, bootstrap *bootstrapv1.BKEBootstrap) (ctrl.Result, error)
}

// 引导引擎实现
type BKEBootstrapEngine struct {
    client.Client
    
    // 引导阶段处理器
    environmentHandler    *EnvironmentHandler
    containerRuntimeHandler *ContainerRuntimeHandler
    kubeletHandler        *KubeletHandler
    joinHandler           *JoinHandler
    certificateHandler    *CertificateHandler
}

func (e *BKEBootstrapEngine) Reconcile(ctx context.Context, bootstrap *bootstrapv1.BKEBootstrap) (ctrl.Result, error) {
    // 状态机模式处理引导流程
    switch bootstrap.Status.Phase {
    case "", bootstrapv1.BootstrapPhasePending:
        return e.initialize(ctx, bootstrap)
    case bootstrapv1.BootstrapPhaseRunning:
        return e.runBootstrap(ctx, bootstrap)
    case bootstrapv1.BootstrapPhaseSucceeded, bootstrapv1.BootstrapPhaseFailed:
        return ctrl.Result{}, nil
    }
    
    return ctrl.Result{}, nil
}

func (e *BKEBootstrapEngine) runBootstrap(ctx context.Context, bootstrap *bootstrapv1.BKEBootstrap) (ctrl.Result, error) {
    // 按阶段执行引导流程
    phases := []struct {
        name    string
        handler PhaseHandler
    }{
        {"Environment", e.environmentHandler},
        {"ContainerRuntime", e.containerRuntimeHandler},
        {"Kubelet", e.kubeletHandler},
        {"Join", e.joinHandler},
        {"Certificates", e.certificateHandler},
    }
    
    for _, phase := range phases {
        ready, err := phase.handler.IsReady(ctx, bootstrap)
        if err != nil {
            return ctrl.Result{}, err
        }
        
        if !ready {
            return phase.handler.Execute(ctx, bootstrap)
        }
    }
    
    // 所有阶段完成
    bootstrap.Status.Phase = bootstrapv1.BootstrapPhaseSucceeded
    bootstrap.Status.Ready = true
    return ctrl.Result{}, e.Status().Update(ctx, bootstrap)
}

// 阶段处理器接口
type PhaseHandler interface {
    IsReady(ctx context.Context, bootstrap *bootstrapv1.BKEBootstrap) (bool, error)
    Execute(ctx context.Context, bootstrap *bootstrapv1.BKEBootstrap) (ctrl.Result, error)
}

// 环境处理器（从 ensure_nodes_env.go 迁移）
type EnvironmentHandler struct {
    client.Client
}

func (h *EnvironmentHandler) Execute(ctx context.Context, bootstrap *bootstrapv1.BKEBootstrap) (ctrl.Result, error) {
    // 迁移自 ensure_nodes_env.go 的逻辑
    // 1. 配置软件源
    // 2. 安装依赖包
    // 3. 配置 NTP
    // 4. 执行额外脚本
    
    return ctrl.Result{}, nil
}
```
#### 4.2 BKEControlPlane Controller
```go
// controllers/controlplane/bkecontrolplane_controller.go

package controlplane

import (
    "context"
    
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    
    controlplanev1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/controlplane/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/controlplane/engine"
)

type BKEControlPlaneReconciler struct {
    client.Client
    
    // 控制平面引擎
    ControlPlaneEngine engine.ControlPlaneEngine
}

func (r *BKEControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    controlPlane := &controlplanev1.BKEControlPlane{}
    if err := r.Get(ctx, req.NamespacedName, controlPlane); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 使用控制平面引擎处理
    return r.ControlPlaneEngine.Reconcile(ctx, controlPlane)
}

// 控制平面引擎接口
type ControlPlaneEngine interface {
    Reconcile(ctx context.Context, controlPlane *controlplanev1.BKEControlPlane) (ctrl.Result, error)
}

// 控制平面引擎实现
type BKEControlPlaneEngine struct {
    client.Client
    
    // 控制平面阶段处理器
    initHandler      *InitHandler
    scaleHandler     *ScaleHandler
    upgradeHandler   *UpgradeHandler
    certificateHandler *CertificateHandler
}

func (e *BKEControlPlaneEngine) Reconcile(ctx context.Context, controlPlane *controlplanev1.BKEControlPlane) (ctrl.Result, error) {
    // 状态机模式处理控制平面流程
    switch controlPlane.Status.Phase {
    case "", controlplanev1.ControlPlanePhasePending:
        return e.initialize(ctx, controlPlane)
    case controlplanev1.ControlPlanePhaseInitializing:
        return e.initializeControlPlane(ctx, controlPlane)
    case controlplanev1.ControlPlanePhaseRunning:
        return e.maintain(ctx, controlPlane)
    case controlplanev1.ControlPlanePhaseScaling:
        return e.scale(ctx, controlPlane)
    case controlplanev1.ControlPlanePhaseUpgrading:
        return e.upgrade(ctx, controlPlane)
    case controlplanev1.ControlPlanePhaseDeleting:
        return e.delete(ctx, controlPlane)
    }
    
    return ctrl.Result{}, nil
}

func (e *BKEControlPlaneEngine) initializeControlPlane(ctx context.Context, controlPlane *controlplanev1.BKEControlPlane) (ctrl.Result, error) {
    // 迁移自 ensure_master_init.go 的逻辑
    // 1. 初始化第一个控制平面节点
    // 2. 生成证书
    // 3. 创建 kubeconfig
    // 4. 启动控制平面组件
    
    return e.initHandler.Execute(ctx, controlPlane)
}

func (e *BKEControlPlaneEngine) scale(ctx context.Context, controlPlane *controlplanev1.BKEControlPlane) (ctrl.Result, error) {
    // 迁移自 ensure_master_join.go 的逻辑
    // 1. 计算期望副本数
    // 2. 创建/删除 Machine
    // 3. 等待节点就绪
    
    return e.scaleHandler.Execute(ctx, controlPlane)
}

// 初始化处理器（从 ensure_master_init.go 迁移）
type InitHandler struct {
    client.Client
}

func (h *InitHandler) Execute(ctx context.Context, controlPlane *controlplanev1.BKEControlPlane) (ctrl.Result, error) {
    // 迁移自 ensure_master_init.go 的逻辑
    // 1. 选择第一个主节点
    // 2. 执行 kubeadm init
    // 3. 等待控制平面就绪
    // 4. 更新状态
    
    return ctrl.Result{}, nil
}
```
### 五、迁移策略
#### 5.1 迁移步骤
**阶段 3.1：创建新 CRD 和控制器（不破坏现有功能）**
1. 创建 `BKEBootstrap` 和 `BKEControlPlane` CRD
2. 实现对应的控制器
3. 添加 Feature Gate 控制新旧逻辑切换
4. 编写单元测试和集成测试

**阶段 3.2：数据迁移和双写**
1. 创建转换 Webhook，自动创建 `BKEBootstrap` 和 `BKEControlPlane`
2. 实现数据同步逻辑，确保新旧 CRD 数据一致
3. 逐步迁移 Phase 逻辑到新控制器

**阶段 3.3：功能切换和清理**
1. 通过 Feature Gate 切换到新 Provider
2. 验证所有功能正常
3. 移除旧代码和兼容逻辑
4. 清理 BKECluster CRD 中的冗余字段
#### 5.2 兼容性保障
**Feature Gate 设计：**
```go
// pkg/features/features.go

package features

import (
    "k8s.io/apimachinery/pkg/util/runtime"
    "k8s.io/component-base/featuregate"
)

const (
    // BootstrapProvider 启用独立的 Bootstrap Provider
    BootstrapProvider featuregate.Feature = "BootstrapProvider"
    
    // ControlPlaneProvider 启用独立的 ControlPlane Provider
    ControlPlaneProvider featuregate.Feature = "ControlPlaneProvider"
)

func init() {
    runtime.Must(feature.DefaultMutableFeatureGate.Add(defaultKubernetesFeatureGates))
}

var defaultKubernetesFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
    BootstrapProvider:      {Default: false, PreRelease: featuregate.Alpha},
    ControlPlaneProvider:   {Default: false, PreRelease: featuregate.Alpha},
}
```
**转换 Webhook：**
```go
// api/capbke/v1beta1/bkecluster_webhook.go

func (r *BKECluster) Default() {
    // 如果启用了新 Provider，自动创建对应的 CR
    if features.DefaultFeatureGate.Enabled(features.BootstrapProvider) {
        r.createBootstrapProvider()
    }
    
    if features.DefaultFeatureGate.Enabled(features.ControlPlaneProvider) {
        r.createControlPlaneProvider()
    }
}

func (r *BKECluster) createBootstrapProvider() {
    // 从 BKECluster.Spec.ClusterConfig 中提取 Bootstrap 配置
    // 创建 BKEBootstrap CR
}

func (r *BKECluster) createControlPlaneProvider() {
    // 从 BKECluster.Spec.ClusterConfig 中提取 ControlPlane 配置
    // 创建 BKEControlPlane CR
}
```
### 六、测试策略
#### 6.1 单元测试
```go
// controllers/bootstrap/bkebootstrap_controller_test.go

func TestBKEBootstrapReconciler_EnvironmentPhase(t *testing.T) {
    tests := []struct {
        name       string
        bootstrap  *bootstrapv1.BKEBootstrap
        wantPhase  bootstrapv1.BootstrapPhase
        wantReady  bool
    }{
        {
            name: "environment ready",
            bootstrap: &bootstrapv1.BKEBootstrap{
                Spec: bootstrapv1.BKEBootstrapSpec{
                    Environment: bootstrapv1.BootstrapEnvironment{
                        NTPServer: "time.google.com",
                    },
                },
            },
            wantPhase: bootstrapv1.BootstrapPhaseRunning,
            wantReady: false,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 测试逻辑
        })
    }
}
```
#### 6.2 集成测试
```go
// test/integration/bootstrap_test.go

func TestBootstrapProviderIntegration(t *testing.T) {
    // 1. 创建 BKECluster
    // 2. 启用 Feature Gate
    // 3. 验证自动创建 BKEBootstrap
    // 4. 验证引导流程执行
    // 5. 验证节点状态更新
}
```
### 七、实施计划
| 阶段 | 任务 | 周期 | 依赖 |
|------|------|------|------|
| 3.1 | 创建 BKEBootstrap CRD 和控制器 | 2 周 | 阶段二完成 |
| 3.2 | 创建 BKEControlPlane CRD 和控制器 | 2 周 | 阶段二完成 |
| 3.3 | 实现 Feature Gate 和转换 Webhook | 1 周 | 3.1, 3.2 |
| 3.4 | 迁移 ensure_nodes_env 逻辑 | 1 周 | 3.1 |
| 3.5 | 迁移 ensure_master_init/join 逻辑 | 2 周 | 3.2 |
| 3.6 | 数据迁移和双写测试 | 1 周 | 3.3, 3.4, 3.5 |
| 3.7 | 功能切换和清理 | 1 周 | 3.6 |

**总周期：10 周**
### 八、风险评估与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 数据迁移失败 | 高 | 实现回滚机制，保留旧逻辑 |
| 功能不兼容 | 中 | 充分的集成测试，灰度发布 |
| 性能下降 | 中 | 性能基准测试，优化关键路径 |
| 用户学习成本 | 低 | 提供迁移文档和示例 |
### 九、总结
阶段三通过引入独立的 Bootstrap Provider 和 ControlPlane Provider，实现了职责的清晰分离：

**收益：**
1. **职责清晰**：每个 Provider 专注于单一职责
2. **可扩展性**：支持多种 Bootstrap 和 ControlPlane 实现
3. **可维护性**：降低单个控制器的复杂度
4. **标准化**：符合 Cluster API 标准模式

**关键设计：**
1. 新增 `BKEBootstrap` 和 `BKEControlPlane` CRD
2. 简化 `BKECluster` CRD，仅保留基础设施配置
3. 使用 Feature Gate 实现平滑迁移
4. 通过转换 Webhook 保障兼容性

这个设计为后续支持更多 Bootstrap 方式（如 Ignition、Cloud-Init）和 ControlPlane 实现（如 K3s、RKE2）奠定了基础。
        

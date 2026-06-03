# 使用 KubeadmControlPlane 重构控制面 Init/Join 详细方案

## 1. 当前实现分析

### 1.1 当前架构

```
┌─────────────────────────────────────────────────────────────┐
│                    BKE Controller                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  EnsureMasterInit                                           │
│  ├── 通过 Agent Command 执行 kubeadm init                   │
│  ├── 等待 Command 执行完成                                  │
│  └── 设置 ControlPlaneInitializedCondition                 │
│                                                             │
│  EnsureMasterJoin                                           │
│  ├── 调整 KubeadmControlPlane.Replicas                      │
│  ├── CAPI KCP Controller 创建 Machine                       │
│  ├── BKEMachine Controller 下发 join Command                │
│  └── 等待 Machine.NodeRef 出现                              │
│                                                             │
└─────────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────┐
│                  CAPI Controllers                           │
├─────────────────────────────────────────────────────────────┤
│  KubeadmControlPlane Controller                             │
│  ├── 创建 KubeadmConfig                                     │
│  ├── 创建 Machine                                           │
│  └── 设置 Cluster.ControlPlaneInitialized                  │
│                                                             │
│  KubeadmConfig Controller                                   │
│  ├── 生成 bootstrap data                                    │
│  └── 设置 dataSecretName                                    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 1.2 存在的问题

#### 问题1：控制面初始化未完全利用 CAPI

**当前代码** ([ensure_master_init.go](file:///c:/Users/z00820145/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go#L1)):

```go
// EnsureMasterInit 通过 Agent Command 执行 kubeadm init
func (e *EnsureMasterInit) Execute() (ctrl.Result, error) {
    // 1. 设置 Condition=False
    e.setupConditionAndRefresh()
    
    // 2. 等待 Agent 创建 MasterInit Command
    initCommand, err := phaseutil.GetMasterInitCommand(ctx, c, bkeCluster)
    
    // 3. 检查 Command 执行状态
    complete, successNodes, failedNodes := command.CheckCommandStatus(initCommand)
    
    // 4. 等待 BKEMachine 标记 Bootstrapped
    bkeMachine, err := phaseutil.GetControlPlaneInitBKEMachine(ctx, c, bkeCluster)
    
    // 5. 等待 CAPI 设置 ControlPlaneInitializedCondition
    // ...
}
```

**问题分析**:
- **重复造轮子**: CAPI 的 KubeadmControlPlane Controller 已经实现了完整的控制面初始化逻辑
- **职责混乱**: BKE Controller 既管理基础设施，又管理控制面初始化
- **配置分散**: kubeadm 配置分散在多个地方（模板、代码、Command）
- **难以维护**: 初始化逻辑复杂，容易出错

#### 问题2：引导配置硬编码

**当前代码**:

```go
// 引导配置在代码中硬编码
func (k *KubeadmPlugin) Execute(commands []string) ([]string, error) {
    switch parseCommands["phase"] {
    case utils.InitControlPlane:
        return nil, k.initControlPlane()  // 硬编码的 init 逻辑
    case utils.JoinControlPlane:
        return nil, k.joinControlPlane()  // 硬编码的 join 逻辑
    }
}
```

**问题分析**:
- 缺乏灵活的配置能力
- 难以支持不同版本的 kubeadm 参数
- 无法自定义引导配置

#### 问题3：控制面状态管理分散

**当前代码**:

```go
// EnsureClusterAPIObj 创建 KCP 时 replicas=1
spec:
  replicas: 1  // 固定为 1，避免 CAPI 干扰

// EnsureMasterJoin 调整 replicas
scope.KubeadmControlPlane.Spec.Replicas = &exceptReplicas
```

**问题分析**:
- KCP Replicas 被人为控制，无法发挥 CAPI 的声明式优势
- 控制面状态分散在 BKECluster、Cluster、KCP 多个对象中
- 状态同步复杂，容易出现不一致

#### 问题4：负载均衡配置与 CAPI 冲突

**当前代码**:

```yaml
kubeadmConfigSpec:
  clusterConfiguration:
    controlPlaneEndpoint: "fake"  # 占位值
```

**问题分析**:
- BKE 使用自己的负载均衡机制，与 CAPI 的默认行为冲突
- `controlPlaneEndpoint` 设置为 "fake"，导致配置不清晰
- 难以利用 CAPI 的负载均衡管理能力

---

## 2. 重构目标

### 2.1 核心目标

1. **完全利用 CAPI 的 KubeadmControlPlane Controller**
   - 让 KCP Controller 负责控制面的 init/join
   - BKE Provider 只负责基础设施层面的工作

2. **声明式控制面管理**
   - 通过修改 KCP.Spec 声明期望状态
   - CAPI 自动调谐到期望状态

3. **统一配置管理**
   - 所有 kubeadm 配置集中在 KubeadmConfigSpec
   - 支持灵活的配置模板

4. **清晰的职责分离**
   - BKE Provider: 节点准备、负载均衡、证书管理
   - CAPI: 控制面生命周期管理

### 2.2 架构对比

**当前架构**:
```
BKE Controller
  ├── EnsureMasterInit (执行 kubeadm init)
  ├── EnsureMasterJoin (调整 KCP Replicas)
  └── 其他 Phase

CAPI Controllers
  ├── KCP Controller (被动响应 Replicas 变化)
  └── KubeadmConfig Controller
```

**目标架构**:
```
BKE Provider (Infrastructure Provider)
  ├── EnsureNodesReady (节点准备)
  ├── EnsureLoadBalancer (负载均衡配置)
  ├── EnsureCertificates (证书管理)
  └── EnsureControlPlaneReady (等待控制面就绪)

CAPI Controllers (Control Plane Provider)
  ├── KCP Controller
  │   ├── 初始化第一个控制面节点
  │   ├── 扩缩容控制面
  │   ├── 升级控制面版本
  │   └── 滚动更新控制面
  └── KubeadmConfig Controller
      ├── 生成 InitConfiguration
      ├── 生成 JoinConfiguration
      └── 生成 bootstrap data
```

## 3. 详细重构方案

### 3.1 Phase 重构

#### 3.1.1 移除 EnsureMasterInit

**重构策略**: 完全移除 EnsureMasterInit，让 CAPI 的 KCP Controller 负责初始化

**原职责转移**:

| 原职责 | 新职责归属 | 实现方式 |
|--------|-----------|---------|
| 执行 kubeadm init | CAPI KCP Controller | 自动执行 |
| 等待初始化完成 | EnsureControlPlaneReady | 检查 Condition |
| 设置 ControlPlaneInitializedCondition | CAPI KCP Controller | 自动设置 |
| 创建第一个 Machine | CAPI KCP Controller | 自动创建 |

**新的 Phase 流程**:

```go
// 新的 Phase 列表
var DeployPhases = []func(ctx *PhaseContext) Phase{
    NewEnsureBKEAgent,           // 推送 Agent
    NewEnsureNodesEnv,           // 节点环境准备
    NewEnsureLoadBalancer,       // 负载均衡配置
    NewEnsureCertificates,       // 证书生成
    NewEnsureClusterAPIObj,      // 创建 CAPI 对象（包括 KCP）
    NewEnsureControlPlaneReady,  // 等待控制面就绪（替代 EnsureMasterInit）
    NewEnsureWorkerJoin,         // Worker 节点加入
    NewEnsureAddonDeploy,        // Addon 部署
    NewEnsureNodesPostProcess,   // 后置处理
}
```

#### 3.1.2 新增 EnsureControlPlaneReady

**设计思路**: 等待 CAPI 的 KCP Controller 完成控制面初始化

**代码实现**:

```go
// pkg/phaseframe/phases/ensure_controlplane_ready.go
package phases

import (
    "context"
    "time"
    
    "github.com/pkg/errors"
    "k8s.io/apimachinery/pkg/util/wait"
    clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
    "sigs.k8s.io/cluster-api/util/conditions"
    ctrl "sigs.k8s.io/controller-runtime"
    
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

const (
    EnsureControlPlaneReadyName = "EnsureControlPlaneReady"
    ControlPlaneReadyTimeout    = 30 * time.Minute
    ControlPlaneReadyPollInterval = 5 * time.Second
)

type EnsureControlPlaneReady struct {
    phaseframe.BasePhase
}

func NewEnsureControlPlaneReady(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    base := phaseframe.NewBasePhase(ctx, EnsureControlPlaneReadyName)
    return &EnsureControlPlaneReady{BasePhase: base}
}

func (e *EnsureControlPlaneReady) Execute() (ctrl.Result, error) {
    ctx, c, bkeCluster, cluster, log := e.Ctx.Untie()
    
    // 检查控制面是否已初始化
    if conditions.IsTrue(cluster, clusterv1.ControlPlaneInitializedCondition) {
        log.Info("Control plane already initialized")
        return ctrl.Result{}, nil
    }
    
    // 等待 CAPI KCP Controller 完成初始化
    err := wait.PollImmediateUntil(ControlPlaneReadyPollInterval, func() (bool, error) {
        // 刷新 Cluster 对象
        if err := e.Ctx.RefreshCtxCluster(); err != nil {
            return false, err
        }
        
        // 检查控制面初始化状态
        if conditions.IsTrue(e.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
            return true, nil
        }
        
        // 检查 KCP 状态
        kcp, err := phaseutil.GetClusterAPIKubeadmControlPlane(ctx, c, e.Ctx.Cluster)
        if err != nil {
            return false, err
        }
        
        // 检查是否有失败的 Machine
        if conditions.IsFalse(kcp, clusterv1.ControlPlaneComponentsHealthyCondition) {
            return false, errors.New("control plane components unhealthy")
        }
        
        log.Info("Waiting for control plane to be initialized...")
        return false, nil
    }, ctx.Done())
    
    if errors.Is(err, wait.ErrWaitTimeout) {
        return ctrl.Result{}, errors.New("timeout waiting for control plane initialization")
    }
    
    return ctrl.Result{}, err
}

func (e *EnsureControlPlaneReady) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    
    // 只有在控制面未初始化时才执行
    if e.Ctx.Cluster != nil && conditions.IsTrue(e.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
        return false
    }
    
    return true
}
```

#### 3.1.3 重构 EnsureMasterJoin

**设计思路**: 通过声明式修改 KCP.Spec.Replicas，让 CAPI 自动完成 join

**代码实现**:

```go
// pkg/phaseframe/phases/ensure_master_join.go (重构版)
package phases

import (
    "context"
    "time"
    
    "github.com/pkg/errors"
    "k8s.io/apimachinery/pkg/util/wait"
    clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
    "sigs.k8s.io/cluster-api/util/conditions"
    ctrl "sigs.k8s.io/controller-runtime"
    
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
)

type EnsureMasterJoin struct {
    phaseframe.BasePhase
}

func NewEnsureMasterJoin(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    base := phaseframe.NewBasePhase(ctx, EnsureMasterJoinName)
    return &EnsureMasterJoin{BasePhase: base}
}

func (e *EnsureMasterJoin) Execute() (ctrl.Result, error) {
    ctx, c, bkeCluster, cluster, log := e.Ctx.Untie()
    
    // 1. 获取期望的 Master 节点数
    desiredMasterCount := e.getDesiredMasterCount()
    
    // 2. 获取 KCP 对象
    kcp, err := phaseutil.GetClusterAPIKubeadmControlPlane(ctx, c, cluster)
    if err != nil {
        return ctrl.Result{}, errors.Wrap(err, "failed to get KubeadmControlPlane")
    }
    
    // 3. 计算当前实际的 Master 节点数
    currentMasterCount := int(*kcp.Spec.Replicas)
    
    // 4. 如果副本数一致，检查是否所有节点都已就绪
    if currentMasterCount == desiredMasterCount {
        return e.waitForMastersReady(ctx, c, cluster, desiredMasterCount)
    }
    
    // 5. 声明式更新 KCP Replicas
    log.Info("Scaling control plane from %d to %d replicas", currentMasterCount, desiredMasterCount)
    
    kcp.Spec.Replicas = ptr.To[int32](int32(desiredMasterCount))
    if err := c.Update(ctx, kcp); err != nil {
        return ctrl.Result{}, errors.Wrap(err, "failed to update KubeadmControlPlane replicas")
    }
    
    // 6. 等待扩缩容完成
    return e.waitForMastersReady(ctx, c, cluster, desiredMasterCount)
}

func (e *EnsureMasterJoin) getDesiredMasterCount() int {
    allNodes, _ := e.Ctx.GetNodes()
    masterNodes := allNodes.Master()
    return masterNodes.Length()
}

func (e *EnsureMasterJoin) waitForMastersReady(
    ctx context.Context,
    c client.Client,
    cluster *clusterv1.Cluster,
    desiredCount int,
) (ctrl.Result, error) {
    // 等待所有 Master 节点就绪
    err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
        // 刷新 KCP
        kcp, err := phaseutil.GetClusterAPIKubeadmControlPlane(ctx, c, cluster)
        if err != nil {
            return false, err
        }
        
        // 检查副本数是否达到期望
        if int(*kcp.Spec.Replicas) != desiredCount {
            return false, nil
        }
        
        // 检查所有 Machine 是否就绪
        machines, err := phaseutil.GetControlPlaneMachines(ctx, c, cluster)
        if err != nil {
            return false, err
        }
        
        readyCount := 0
        for _, machine := range machines {
            if machine.Status.NodeRef != nil {
                readyCount++
            }
        }
        
        if readyCount == desiredCount {
            return true, nil
        }
        
        return false, nil
    }, ctx.Done())
    
    if errors.Is(err, wait.ErrWaitTimeout) {
        return ctrl.Result{}, errors.New("timeout waiting for masters to be ready")
    }
    
    return ctrl.Result{}, err
}
```

### 3.2 KubeadmControlPlane 配置重构

#### 3.2.1 改进 KCP 创建逻辑

**当前问题**:

```yaml
# 当前配置
spec:
  replicas: 1  # 固定为 1
  kubeadmConfigSpec:
    clusterConfiguration:
      controlPlaneEndpoint: "fake"  # 占位值
```

**重构方案**:

```go
// pkg/phaseframe/phases/ensure_cluster_api_obj.go (重构版)
func (e *EnsureClusterAPIObj) createKubeadmControlPlane(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    cfg *bkeinit.BkeConfig,
) (*controlv1beta1.KubeadmControlPlane, error) {
    // 1. 计算初始 Master 节点数
    allNodes, _ := e.Ctx.GetNodes()
    masterNodes := allNodes.Master()
    initialReplicas := len(masterNodes)
    if initialReplicas == 0 {
        return nil, errors.New("no master nodes found")
    }
    
    // 2. 构建 KubeadmConfigSpec
    kubeadmConfigSpec := e.buildKubeadmConfigSpec(bkeCluster, cfg)
    
    // 3. 创建 KCP
    kcp := &controlv1beta1.KubeadmControlPlane{
        ObjectMeta: metav1.ObjectMeta{
            Name:      bkeCluster.Name + "-controlplane",
            Namespace: bkeCluster.Namespace,
            Labels: map[string]string{
                clusterv1.ClusterNameLabel: bkeCluster.Name,
            },
            OwnerReferences: []metav1.OwnerReference{
                {
                    APIVersion: clusterv1.GroupVersion.String(),
                    Kind:       "Cluster",
                    Name:       bkeCluster.Name,
                    UID:        bkeCluster.UID,
                },
            },
        },
        Spec: controlv1beta1.KubeadmControlPlaneSpec{
            Replicas: ptr.To[int32](int32(initialReplicas)),  // 设置实际的 Master 数量
            Version:  bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
            MachineTemplate: controlv1beta1.KubeadmControlPlaneMachineTemplate{
                InfrastructureRef: corev1.ObjectReference{
                    APIVersion: bkev1beta1.GroupVersion.String(),
                    Kind:       "BKEMachineTemplate",
                    Name:       bkeCluster.Name + "-machine-controlplane",
                    Namespace:  bkeCluster.Namespace,
                },
            },
            KubeadmConfigSpec: kubeadmConfigSpec,
        },
    }
    
    // 4. 应用自定义注解
    e.applyKCPAnnotations(kcp, bkeCluster)
    
    return kcp, nil
}

// buildKubeadmConfigSpec 构建完整的 KubeadmConfigSpec
func (e *EnsureClusterAPIObj) buildKubeadmConfigSpec(
    bkeCluster *bkev1beta1.BKECluster,
    cfg *bkeinit.BkeConfig,
) bootstrapv1.KubeadmConfigSpec {
    spec := bootstrapv1.KubeadmConfigSpec{
        // 1. ClusterConfiguration
        ClusterConfiguration: bootstrapv1.ClusterConfiguration{
            ClusterName:       bkeCluster.Name,
            KubernetesVersion: bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
            ControlPlaneEndpoint: e.buildControlPlaneEndpoint(bkeCluster),  // 使用真实的负载均衡端点
            Networking: bootstrapv1.Networking{
                DNSDomain:     cfg.Networking.DNSDomain,
                PodSubnet:     cfg.Networking.PodSubnet,
                ServiceSubnet: cfg.Networking.ServiceSubnet,
            },
            ImageRepository: cfg.ImageRepository,
            APIServer: bootstrapv1.APIServer{
                ControlPlaneComponent: bootstrapv1.ControlPlaneComponent{
                    ExtraArgs: e.buildAPIServerExtraArgs(bkeCluster),
                },
                CertSANs: e.buildCertSANs(bkeCluster),
            },
            ControllerManager: bootstrapv1.ControlPlaneComponent{
                ExtraArgs: e.buildControllerManagerExtraArgs(bkeCluster),
            },
            Scheduler: bootstrapv1.ControlPlaneComponent{
                ExtraArgs: e.buildSchedulerExtraArgs(bkeCluster),
            },
        },
        
        // 2. InitConfiguration
        InitConfiguration: bootstrapv1.InitConfiguration{
            LocalAPIEndpoint: bootstrapv1.APIEndpoint{
                AdvertiseAddress: e.getLocalAPIAddress(),  // 节点本地 API 地址
                BindPort:         6443,
            },
            NodeRegistration: bootstrapv1.NodeRegistration{
                Name:             e.getNodeRegistrationName(),
                CRISocket:        "/var/run/containerd/containerd.sock",
                KubeletExtraArgs: e.buildKubeletExtraArgs(bkeCluster),
            },
        },
        
        // 3. JoinConfiguration
        JoinConfiguration: bootstrapv1.JoinConfiguration{
            ControlPlane: &bootstrapv1.JoinControlPlane{
                LocalAPIEndpoint: bootstrapv1.APIEndpoint{
                    AdvertiseAddress: e.getLocalAPIAddress(),
                    BindPort:         6443,
                },
            },
            Discovery: bootstrapv1.Discovery{
                BootstrapToken: &bootstrapv1.BootstrapTokenDiscovery{
                    Token:                    "",  // 由 CAPI 自动生成
                    APIServerEndpoint:        e.buildControlPlaneEndpoint(bkeCluster),
                    UnsafeSkipCAVerification: false,
                },
            },
            NodeRegistration: bootstrapv1.NodeRegistration{
                Name:             e.getNodeRegistrationName(),
                CRISocket:        "/var/run/containerd/containerd.sock",
                KubeletExtraArgs: e.buildKubeletExtraArgs(bkeCluster),
            },
        },
        
        // 4. Files (自定义文件)
        Files: e.buildCustomFiles(bkeCluster),
        
        // 5. PreKubeadmCommands (前置命令)
        PreKubeadmCommands: e.buildPreKubeadmCommands(bkeCluster),
        
        // 6. PostKubeadmCommands (后置命令)
        PostKubeadmCommands: e.buildPostKubeadmCommands(bkeCluster),
    }
    
    // 7. 外部 etcd 配置
    if bkeCluster.Spec.ClusterConfig.ControlPlane.Etcd != nil && 
       bkeCluster.Spec.ClusterConfig.ControlPlane.Etcd.External != nil {
        spec.ClusterConfiguration.Etcd = bootstrapv1.Etcd{
            External: &bootstrapv1.ExternalEtcd{
                Endpoints: e.buildEtcdEndpoints(bkeCluster),
                CAFile:    "/etc/kubernetes/pki/etcd/ca.crt",
                CertFile:  "/etc/kubernetes/pki/etcd/peer.crt",
                KeyFile:   "/etc/kubernetes/pki/etcd/peer.key",
            },
        }
    }
    
    return spec
}

// buildControlPlaneEndpoint 构建控制面端点
func (e *EnsureClusterAPIObj) buildControlPlaneEndpoint(bkeCluster *bkev1beta1.BKECluster) string {
    // 使用 BKE 的负载均衡 VIP
    if bkeCluster.Spec.ClusterConfig.LoadBalancer != nil {
        lb := bkeCluster.Spec.ClusterConfig.LoadBalancer
        if lb.VIP != "" {
            return fmt.Sprintf("%s:%d", lb.VIP, lb.Port)
        }
    }
    
    // 如果没有 VIP，使用第一个 Master 节点的 IP
    allNodes, _ := e.Ctx.GetNodes()
    masterNodes := allNodes.Master()
    if len(masterNodes) > 0 {
        return fmt.Sprintf("%s:6443", masterNodes[0].IP)
    }
    
    return ""
}
```

#### 3.2.2 支持动态 KubeadmConfigSpec

**设计思路**: 通过 ConfigMap 或 CRD 定义 KubeadmConfigSpec 模板，支持灵活配置

**新增 CRD**:

```go
// api/bootstrap/v1beta1/bke_kubeadm_config_template_types.go
package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type BKEKubeadmConfigTemplate struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   BKEKubeadmConfigTemplateSpec   `json:"spec,omitempty"`
    Status BKEKubeadmConfigTemplateStatus `json:"status,omitempty"`
}

type BKEKubeadmConfigTemplateSpec struct {
    // Template 定义 KubeadmConfigSpec 模板
    Template BKEKubeadmConfigTemplateResource `json:"template"`
}

type BKEKubeadmConfigTemplateResource struct {
    Spec bootstrapv1.KubeadmConfigSpec `json:"spec"`
}

// 使用示例
/*
apiVersion: bootstrap.bke.bocloud.com/v1beta1
kind: BKEKubeadmConfigTemplate
metadata:
  name: master-config-template
spec:
  template:
    spec:
      clusterConfiguration:
        apiServer:
          extraArgs:
            enable-admission-plugins: "NodeRestriction,PodSecurityPolicy"
            audit-log-path: "/var/log/kubernetes/audit.log"
      initConfiguration:
        nodeRegistration:
          kubeletExtraArgs:
            cgroup-driver: systemd
            eviction-hard: "memory.available<200Mi,nodefs.available<10%"
      joinConfiguration:
        nodeRegistration:
          kubeletExtraArgs:
            cgroup-driver: systemd
      files:
        - path: /etc/kubernetes/kubelet.conf
          contentFrom:
            configMapKeyRef:
              name: kubelet-config
              key: kubelet.conf
      preKubeadmCommands:
        - "systemctl enable containerd"
        - "systemctl start containerd"
      postKubeadmCommands:
        - "kubectl apply -f /etc/kubernetes/manifests/addons/"
*/
```

**模板渲染逻辑**:

```go
// pkg/phaseframe/phaseutil/kubeadm_config.go
package phaseutil

import (
    "bytes"
    "text/template"
    
    bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

// RenderKubeadmConfigSpec 从模板渲染 KubeadmConfigSpec
func RenderKubeadmConfigSpec(
    template *bootstrapv1.KubeadmConfigSpec,
    bkeCluster *bkev1beta1.BKECluster,
    node *confv1beta1.Node,
) (*bootstrapv1.KubeadmConfigSpec, error) {
    // 1. 深拷贝模板
    spec := template.DeepCopy()
    
    // 2. 渲染模板变量
    if err := renderTemplateVariables(spec, bkeCluster, node); err != nil {
        return nil, err
    }
    
    // 3. 应用节点特定配置
    applyNodeSpecificConfig(spec, node)
    
    return spec, nil
}

// renderTemplateVariables 渲染模板变量
func renderTemplateVariables(
    spec *bootstrapv1.KubeadmConfigSpec,
    bkeCluster *bkev1beta1.BKECluster,
    node *confv1beta1.Node,
) error {
    // 构建模板数据
    data := map[string]interface{}{
        "ClusterName":         bkeCluster.Name,
        "KubernetesVersion":   bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
        "NodeIP":              node.IP,
        "NodeName":            node.Hostname,
        "PodSubnet":           bkeCluster.Spec.ClusterConfig.Networking.PodSubnet,
        "ServiceSubnet":       bkeCluster.Spec.ClusterConfig.Networking.ServiceSubnet,
        "DNSDomain":           bkeCluster.Spec.ClusterConfig.Networking.DNSDomain,
        "ControlPlaneEndpoint": getControlPlaneEndpoint(bkeCluster),
    }
    
    // 渲染字符串字段
    if spec.ClusterConfiguration.ControlPlaneEndpoint != "" {
        rendered, err := renderString(spec.ClusterConfiguration.ControlPlaneEndpoint, data)
        if err != nil {
            return err
        }
        spec.ClusterConfiguration.ControlPlaneEndpoint = rendered
    }
    
    // 渲染 ExtraArgs
    renderExtraArgs(&spec.ClusterConfiguration.APIServer.ExtraArgs, data)
    renderExtraArgs(&spec.ClusterConfiguration.ControllerManager.ExtraArgs, data)
    renderExtraArgs(&spec.ClusterConfiguration.Scheduler.ExtraArgs, data)
    
    // 渲染 Files
    for i := range spec.Files {
        if spec.Files[i].Content != "" {
            rendered, err := renderString(spec.Files[i].Content, data)
            if err != nil {
                return err
            }
            spec.Files[i].Content = rendered
        }
    }
    
    // 渲染 Commands
    spec.PreKubeadmCommands = renderCommands(spec.PreKubeadmCommands, data)
    spec.PostKubeadmCommands = renderCommands(spec.PostKubeadmCommands, data)
    
    return nil
}

// renderString 渲染字符串模板
func renderString(templateStr string, data map[string]interface{}) (string, error) {
    tmpl, err := template.New("template").Parse(templateStr)
    if err != nil {
        return "", err
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", err
    }
    
    return buf.String(), nil
}
```

### 3.3 负载均衡集成

#### 3.3.1 改进负载均衡配置

**当前问题**: `controlPlaneEndpoint` 设置为 "fake"，与 BKE 的负载均衡机制冲突

**重构方案**: 将 BKE 的负载均衡配置集成到 KCP 的 `controlPlaneEndpoint`

```go
// pkg/phaseframe/phases/ensure_loadbalancer.go (重构版)
package phases

import (
    "context"
    "fmt"
    
    "github.com/pkg/errors"
    ctrl "sigs.k8s.io/controller-runtime"
    
    bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
)

type EnsureLoadBalancer struct {
    phaseframe.BasePhase
}

func (e *EnsureLoadBalancer) Execute() (ctrl.Result, error) {
    ctx, c, bkeCluster, cluster, log := e.Ctx.Untie()
    
    // 1. 获取 KCP 对象
    kcp, err := phaseutil.GetClusterAPIKubeadmControlPlane(ctx, c, cluster)
    if err != nil {
        return ctrl.Result{}, errors.Wrap(err, "failed to get KubeadmControlPlane")
    }
    
    // 2. 构建控制面端点
    controlPlaneEndpoint := e.buildControlPlaneEndpoint(bkeCluster)
    
    // 3. 更新 KCP 的 controlPlaneEndpoint
    if kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.ControlPlaneEndpoint != controlPlaneEndpoint {
        log.Info("Updating control plane endpoint to %s", controlPlaneEndpoint)
        
        kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.ControlPlaneEndpoint = controlPlaneEndpoint
        
        // 更新 API Server 的 CertSANs
        kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.APIServer.CertSANs = 
            e.buildCertSANs(bkeCluster, controlPlaneEndpoint)
        
        if err := c.Update(ctx, kcp); err != nil {
            return ctrl.Result{}, errors.Wrap(err, "failed to update KubeadmControlPlane")
        }
    }
    
    // 4. 配置负载均衡后端
    if err := e.configureLoadBalancerBackends(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}

// buildControlPlaneEndpoint 构建控制面端点
func (e *EnsureLoadBalancer) buildControlPlaneEndpoint(bkeCluster *bkev1beta1.BKECluster) string {
    lb := bkeCluster.Spec.ClusterConfig.LoadBalancer
    
    switch lb.Type {
    case "VIP":
        // 使用 VIP 模式
        return fmt.Sprintf("%s:%d", lb.VIP, lb.Port)
        
    case "DNS":
        // 使用 DNS 模式
        return fmt.Sprintf("%s:%d", lb.Domain, lb.Port)
        
    case "NodePort":
        // 使用 NodePort 模式（通过 API Server 的 NodePort Service）
        return fmt.Sprintf("%s:%d", e.getAPIServerNodePortIP(), e.getAPIServerNodePort())
        
    default:
        // 默认使用第一个 Master 节点
        allNodes, _ := e.Ctx.GetNodes()
        masterNodes := allNodes.Master()
        if len(masterNodes) > 0 {
            return fmt.Sprintf("%s:6443", masterNodes[0].IP)
        }
        return ""
    }
}

// buildCertSANs 构建 API Server 证书 SANs
func (e *EnsureLoadBalancer) buildCertSANs(
    bkeCluster *bkev1beta1.BKECluster,
    controlPlaneEndpoint string,
) []string {
    sans := []string{
        "127.0.0.1",
        "localhost",
    }
    
    // 添加控制面端点 IP
    if ip, _, err := net.SplitHostPort(controlPlaneEndpoint); err == nil {
        sans = append(sans, ip)
    }
    
    // 添加所有 Master 节点 IP
    allNodes, _ := e.Ctx.GetNodes()
    masterNodes := allNodes.Master()
    for _, node := range masterNodes {
        sans = append(sans, node.IP)
    }
    
    // 添加用户自定义 SANs
    if bkeCluster.Spec.ClusterConfig.ControlPlane.APIServer != nil {
        sans = append(sans, bkeCluster.Spec.ClusterConfig.ControlPlane.APIServer.CertSANs...)
    }
    
    return sans
}
```

### 3.4 BKEMachine Controller 集成

#### 3.4.1 改进 BKEMachine 与 CAPI Machine 的关联

**当前问题**: BKEMachine 通过 Label 关联 CAPI Machine，关联关系不够清晰

**重构方案**: 使用 OwnerReference 建立强关联

```go
// controllers/capbke/bkemachine_controller.go (重构版)
func (r *BKEMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bkeMachine := &bkev1beta1.BKEMachine{}
    if err := r.Get(ctx, req.NamespacedName, bkeMachine); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 1. 获取关联的 CAPI Machine
    capiMachine, err := r.getOrAssociateCAPIMachine(ctx, bkeMachine)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 2. 如果 CAPI Machine 不存在，等待创建
    if capiMachine == nil {
        log.Info("Waiting for CAPI Machine to be created")
        return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
    }
    
    // 3. 同步状态
    if err := r.syncMachineStatus(ctx, bkeMachine, capiMachine); err != nil {
        return ctrl.Result{}, err
    }
    
    // 4. 处理 Bootstrap Data
    if capiMachine.Spec.Bootstrap.ConfigRef != nil {
        if err := r.handleBootstrapData(ctx, bkeMachine, capiMachine); err != nil {
            return ctrl.Result{}, err
        }
    }
    
    // 5. 处理节点生命周期
    return r.reconcileNodeLifecycle(ctx, bkeMachine, capiMachine)
}

// getOrAssociateCAPIMachine 获取或关联 CAPI Machine
func (r *BKEMachineReconciler) getOrAssociateCAPIMachine(
    ctx context.Context,
    bkeMachine *bkev1beta1.BKEMachine,
) (*clusterv1.Machine, error) {
    // 1. 尝试通过 OwnerReference 获取
    for _, owner := range bkeMachine.OwnerReferences {
        if owner.APIVersion == clusterv1.GroupVersion.String() && owner.Kind == "Machine" {
            machine := &clusterv1.Machine{}
            if err := r.Get(ctx, client.ObjectKey{
                Namespace: bkeMachine.Namespace,
                Name:      owner.Name,
            }, machine); err == nil {
                return machine, nil
            }
        }
    }
    
    // 2. 尝试通过 Label 查找
    machineList := &clusterv1.MachineList{}
    if err := r.List(ctx, machineList, client.InNamespace(bkeMachine.Namespace),
        client.MatchingLabels{
            bkev1beta1.BKEMachineNameLabel: bkeMachine.Name,
        }); err != nil {
        return nil, err
    }
    
    if len(machineList.Items) > 0 {
        machine := &machineList.Items[0]
        
        // 建立 OwnerReference 关联
        bkeMachine.OwnerReferences = append(bkeMachine.OwnerReferences, metav1.OwnerReference{
            APIVersion: clusterv1.GroupVersion.String(),
            Kind:       "Machine",
            Name:       machine.Name,
            UID:        machine.UID,
        })
        
        if err := r.Update(ctx, bkeMachine); err != nil {
            return nil, err
        }
        
        return machine, nil
    }
    
    return nil, nil
}

// handleBootstrapData 处理 Bootstrap Data
func (r *BKEMachineReconciler) handleBootstrapData(
    ctx context.Context,
    bkeMachine *bkev1beta1.BKEMachine,
    capiMachine *clusterv1.Machine,
) error {
    // 1. 获取 KubeadmConfig
    kubeadmConfig, err := r.getKubeadmConfig(ctx, capiMachine)
    if err != nil {
        return err
    }
    
    // 2. 等待 Bootstrap Data 就绪
    if kubeadmConfig.Status.DataSecretName == nil {
        log.Info("Waiting for bootstrap data to be ready")
        return nil
    }
    
    // 3. 获取 Bootstrap Secret
    bootstrapSecret := &corev1.Secret{}
    if err := r.Get(ctx, client.ObjectKey{
        Namespace: capiMachine.Namespace,
        Name:      *kubeadmConfig.Status.DataSecretName,
    }, bootstrapSecret); err != nil {
        return err
    }
    
    // 4. 将 Bootstrap Data 转换为 Agent Command
    if err := r.convertBootstrapToCommand(ctx, bkeMachine, bootstrapSecret); err != nil {
        return err
    }
    
    return nil
}

// convertBootstrapToCommand 将 Bootstrap Data 转换为 Agent Command
func (r *BKEMachineReconciler) convertBootstrapToCommand(
    ctx context.Context,
    bkeMachine *bkev1beta1.BKEMachine,
    bootstrapSecret *corev1.Secret,
) error {
    // 1. 解析 Bootstrap Data
    userData := string(bootstrapSecret.Data["value"])
    
    // 2. 判断是 init 还是 join
    phase := e.determinePhase(bkeMachine, userData)
    
    // 3. 创建 Agent Command
    command := &agentv1beta1.Command{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("%s-bootstrap", bkeMachine.Name),
            Namespace: bkeMachine.Namespace,
            OwnerReferences: []metav1.OwnerReference{
                {
                    APIVersion: bkev1beta1.GroupVersion.String(),
                    Kind:       "BKEMachine",
                    Name:       bkeMachine.Name,
                    UID:        bkeMachine.UID,
                },
            },
        },
        Spec: agentv1beta1.CommandSpec{
            NodeName: bkeMachine.Spec.NodeIP,
            Type:     "K8s",
            Commands: []string{
                "KubeadmBootstrap",  // 新的命令类型
            },
            ExtraData: map[string]string{
                "phase":    phase,
                "userData": userData,  // 传递完整的 cloud-init userData
            },
        },
    }
    
    return r.Create(ctx, command)
}
```

#### 3.4.2 新增 Agent KubeadmBootstrap 命令

**设计思路**: Agent 接收 CAPI 生成的 Bootstrap Data 并执行

```go
// pkg/agent/commands/kubeadm_bootstrap.go
package commands

import (
    "encoding/base64"
    "os"
    "path/filepath"
    
    "github.com/pkg/errors"
)

type KubeadmBootstrapCommand struct {
    BaseCommand
}

func (k *KubeadmBootstrapCommand) Execute(extraData map[string]string) error {
    phase := extraData["phase"]
    userData := extraData["userData"]
    
    // 1. 解析 cloud-init userData
    cloudInit, err := parseCloudInitUserData(userData)
    if err != nil {
        return errors.Wrap(err, "failed to parse cloud-init userData")
    }
    
    // 2. 写入文件
    if err := k.writeFiles(cloudInit.Files); err != nil {
        return errors.Wrap(err, "failed to write files")
    }
    
    // 3. 执行前置命令
    if err := k.executeCommands(cloudInit.PreKubeadmCommands); err != nil {
        return errors.Wrap(err, "failed to execute pre-kubeadm commands")
    }
    
    // 4. 执行 kubeadm
    switch phase {
    case "init":
        if err := k.executeKubeadmInit(cloudInit.InitConfiguration); err != nil {
            return errors.Wrap(err, "failed to execute kubeadm init")
        }
    case "join":
        if err := k.executeKubeadmJoin(cloudInit.JoinConfiguration); err != nil {
            return errors.Wrap(err, "failed to execute kubeadm join")
        }
    }
    
    // 5. 执行后置命令
    if err := k.executeCommands(cloudInit.PostKubeadmCommands); err != nil {
        return errors.Wrap(err, "failed to execute post-kubeadm commands")
    }
    
    return nil
}

// writeFiles 写入 cloud-init 文件
func (k *KubeadmBootstrapCommand) writeFiles(files []File) error {
    for _, file := range files {
        // 创建目录
        dir := filepath.Dir(file.Path)
        if err := os.MkdirAll(dir, 0755); err != nil {
            return err
        }
        
        // 写入文件
        content := file.Content
        if file.Encoding == "base64" {
            decoded, err := base64.StdEncoding.DecodeString(content)
            if err != nil {
                return err
            }
            content = string(decoded)
        }
        
        if err := os.WriteFile(file.Path, []byte(content), file.Permissions); err != nil {
            return err
        }
    }
    return nil
}

// executeKubeadmInit 执行 kubeadm init
func (k *KubeadmBootstrapCommand) executeKubeadmInit(initConfig *InitConfiguration) error {
    // 1. 生成 kubeadm-config.yaml
    configYaml, err := generateKubeadmConfigYaml(initConfig)
    if err != nil {
        return err
    }
    
    configFile := "/tmp/kubeadm-config.yaml"
    if err := os.WriteFile(configFile, []byte(configYaml), 0644); err != nil {
        return err
    }
    defer os.Remove(configFile)
    
    // 2. 执行 kubeadm init
    cmd := exec.Command("kubeadm", "init", "--config", configFile, "--ignore-preflight-errors=all")
    output, err := cmd.CombinedOutput()
    if err != nil {
        return errors.Wrapf(err, "kubeadm init failed: %s", string(output))
    }
    
    return nil
}

// executeKubeadmJoin 执行 kubeadm join
func (k *KubeadmBootstrapCommand) executeKubeadmJoin(joinConfig *JoinConfiguration) error {
    // 1. 生成 kubeadm-config.yaml
    configYaml, err := generateKubeadmConfigYaml(joinConfig)
    if err != nil {
        return err
    }
    
    configFile := "/tmp/kubeadm-config.yaml"
    if err := os.WriteFile(configFile, []byte(configYaml), 0644); err != nil {
        return err
    }
    defer os.Remove(configFile)
    
    // 2. 执行 kubeadm join
    cmd := exec.Command("kubeadm", "join", "--config", configFile, "--ignore-preflight-errors=all")
    output, err := cmd.CombinedOutput()
    if err != nil {
        return errors.Wrapf(err, "kubeadm join failed: %s", string(output))
    }
    
    return nil
}
```

## 4. 迁移步骤

### 4.1 阶段一：准备工作（低风险）

**步骤1**: 新增 BKEKubeadmConfigTemplate CRD

```bash
# 1. 创建 API
kubebuilder create api --group bootstrap --version v1beta1 --kind BKEKubeadmConfigTemplate

# 2. 实现 CRD 定义
# 见 3.2.2 节

# 3. 生成 CRD
make generate
make manifests
```

**步骤2**: 改进 EnsureClusterAPIObj

```go
// 修改点：
// 1. 设置 KCP.Replicas 为实际的 Master 数量
// 2. 设置真实的 controlPlaneEndpoint
// 3. 完善 KubeadmConfigSpec
```

**步骤3**: 新增 EnsureControlPlaneReady

```go
// 新增 Phase，等待 CAPI 完成控制面初始化
// 见 3.1.2 节
```

### 4.2 阶段二：核心重构（中风险）

**步骤1**: 重构 EnsureMasterJoin

```go
// 修改点：
// 1. 声明式更新 KCP.Replicas
// 2. 移除手动创建 Machine 的逻辑
// 3. 等待 CAPI 自动完成 join
```

**步骤2**: 改进 BKEMachine Controller

```go
// 修改点：
// 1. 使用 OwnerReference 关联 CAPI Machine
// 2. 处理 CAPI 生成的 Bootstrap Data
// 3. 转换为 Agent Command
```

**步骤3**: 新增 Agent KubeadmBootstrap 命令

```go
// 新增命令类型，处理 CAPI 的 Bootstrap Data
// 见 3.4.2 节
```

### 4.3 阶段三：移除旧逻辑（高风险）

**步骤1**: 移除 EnsureMasterInit

```go
// 1. 从 Phase 列表中移除
// 2. 移除相关的 Agent Command 类型
// 3. 清理相关的工具函数
```

**步骤2**: 移除手动 kubeadm 执行逻辑

```go
// 移除：
// - KubeadmPlugin.initControlPlane()
// - KubeadmPlugin.joinControlPlane()
// - 相关的硬编码配置
```

**步骤3**: 更新 Phase 流程

```go
// 新的 Phase 流程
var DeployPhases = []func(ctx *PhaseContext) Phase{
    NewEnsureBKEAgent,
    NewEnsureNodesEnv,
    NewEnsureLoadBalancer,
    NewEnsureCertificates,
    NewEnsureClusterAPIObj,      // 创建 KCP（Replicas=实际Master数）
    NewEnsureControlPlaneReady,  // 等待 CAPI 初始化控制面
    NewEnsureWorkerJoin,
    NewEnsureAddonDeploy,
    NewEnsureNodesPostProcess,
}
```

## 5. 测试策略

### 5.1 单元测试

```go
// pkg/phaseframe/phases/ensure_controlplane_ready_test.go
func TestEnsureControlPlaneReady(t *testing.T) {
    tests := []struct {
        name          string
        clusterStatus *clusterv1.ClusterStatus
        expectExecute bool
    }{
        {
            name: "control plane not initialized",
            clusterStatus: &clusterv1.ClusterStatus{
                Conditions: []clusterv1.Condition{
                    {
                        Type:   clusterv1.ControlPlaneInitializedCondition,
                        Status: corev1.ConditionFalse,
                    },
                },
            },
            expectExecute: true,
        },
        {
            name: "control plane already initialized",
            clusterStatus: &clusterv1.ClusterStatus{
                Conditions: []clusterv1.Condition{
                    {
                        Type:   clusterv1.ControlPlaneInitializedCondition,
                        Status: corev1.ConditionTrue,
                    },
                },
            },
            expectExecute: false,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 测试逻辑
        })
    }
}
```

### 5.2 集成测试

```go
// test/integration/controlplane_test.go
func TestControlPlaneLifecycle(t *testing.T) {
    // 1. 创建 BKECluster
    // 2. 等待控制面初始化
    // 3. 扩容控制面
    // 4. 缩容控制面
    // 5. 升级控制面版本
}
```

## 6. 风险评估与缓解

### 6.1 风险列表

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| CAPI KCP Controller 行为变化 | 高 | 低 | 版本锁定，充分测试 |
| Bootstrap Data 格式不兼容 | 高 | 中 | 兼容层，支持多种格式 |
| 负载均衡配置冲突 | 中 | 中 | 统一配置管理 |
| 迁移过程中集群不可用 | 高 | 低 | 渐进式迁移，回滚机制 |
| 性能下降 | 中 | 低 | 性能测试，优化关键路径 |

### 6.2 回滚策略

```go
// 支持新旧两种模式
type ControlPlaneMode string

const (
    ControlPlaneModeLegacy  ControlPlaneMode = "Legacy"   // 旧模式
    ControlPlaneModeCAPI    ControlPlaneMode = "CAPI"     // 新模式
)

// 通过 Annotation 控制模式
func getControlPlaneMode(bkeCluster *bkev1beta1.BKECluster) ControlPlaneMode {
    mode := bkeCluster.Annotations["controlplane.bke.bocloud.com/mode"]
    if mode == "" {
        return ControlPlaneModeCAPI  // 默认新模式
    }
    return ControlPlaneMode(mode)
}
```

## 7. 总结

### 7.1 重构收益

| 收益 | 说明 |
|------|------|
| **简化代码** | 移除 EnsureMasterInit，减少约 500 行代码 |
| **职责清晰** | BKE Provider 专注基础设施，CAPI 管理控制面 |
| **声明式管理** | 通过修改 KCP.Spec 声明期望状态 |
| **配置灵活** | 支持 KubeadmConfigSpec 模板 |
| **易于维护** | 利用 CAPI 的成熟实现，减少自定义逻辑 |
| **功能增强** | 自动获得 CAPI 的新功能（滚动更新、版本升级等） |

### 7.2 工作量估算

| 任务 | 工作量 | 优先级 |
|------|--------|--------|
| 新增 BKEKubeadmConfigTemplate CRD | 2 人日 | P1 |
| 改进 EnsureClusterAPIObj | 3 人日 | P1 |
| 新增 EnsureControlPlaneReady | 2 人日 | P1 |
| 重构 EnsureMasterJoin | 3 人日 | P1 |
| 改进 BKEMachine Controller | 5 人日 | P1 |
| 新增 Agent KubeadmBootstrap 命令 | 3 人日 | P1 |
| 移除 EnsureMasterInit | 2 人日 | P2 |
| 测试与文档 | 5 人日 | P1 |
| **总计** | **25 人日** | - |

---

**文档完成**: 本方案详细分析了当前控制面 init/join 的实现问题，给出了使用 KubeadmControlPlane 进行重构的完整方案，包括 Phase 重构、配置管理、负载均衡集成、BKEMachine Controller 改进等，并提供了详细的迁移步骤和风险评估。
        

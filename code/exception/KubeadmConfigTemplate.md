# 使用 KubeadmConfigTemplate 重构工作节点 Join 详细方案

## 1. 当前实现分析

### 1.1 当前架构

```
┌─────────────────────────────────────────────────────────────┐
│                    BKE Controller                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  EnsureWorkerJoin                                           │
│  ├── 调整 MachineDeployment.Replicas                        │
│  ├── CAPI MD Controller 创建 Machine                        │
│  ├── BKEMachine Controller 下发 join Command                │
│  └── 等待 Machine.NodeRef 出现                              │
│                                                             │
└─────────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────┐
│                  CAPI Controllers                           │
├─────────────────────────────────────────────────────────────┤
│  MachineDeployment Controller                               │
│  ├── 创建 Machine                                           │
│  └── 设置 Machine.Spec.Bootstrap.DataSecretName = "fake"   │
│                                                             │
│  KubeadmConfig Controller                                   │
│  └── 未使用（bootstrap 未配置）                             │
│                                                             │
└─────────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    BKE Agent                                │
├─────────────────────────────────────────────────────────────┤
│  Kubeadm Plugin                                             │
│  ├── joinWorker()                                           │
│  │   ├── joinWorkerCertCommand() (加载 CA 证书)             │
│  │   ├── installKubeletCommand() (安装 kubelet)             │
│  │   └── installKubectlCommand() (安装 kubectl)             │
│  └── 硬编码的 join 配置                                     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 1.2 存在的问题

#### 问题1：未使用 KubeadmConfigTemplate

**当前代码** ([bke-cluster.tmpl](file:///d://code/github/cluster-api-provider-bke/common/cluster/initialize/tmpl/bke-cluster.tmpl#L73-L88)):
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachineDeployment
metadata:
  name: {{.name}}-worker
spec:
  replicas: {{.workerReplicas}}
  template:
    spec:
      version: {{.workerVersion}}
      bootstrap:
        dataSecretName: "fake"  # ❌ 占位值，未使用 KubeadmConfigTemplate
      infrastructureRef:
        apiVersion: bke.bocloud.com/v1beta1
        kind: BKEMachineTemplate
        name: {{.name}}-machine-worker
```
**问题分析**:
- `dataSecretName` 设置为 "fake"，CAPI 的 KubeadmConfig Controller 未被激活
- 无法利用 CAPI 的 bootstrap data 生成机制
- Worker 节点配置无法通过 CRD 管理

#### 问题2：Join 配置硬编码

**当前代码** ([kubeadm.go](file:///d://code/github/cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L227-L246)):
```go
func (k *KubeadmPlugin) joinWorker() error {
    log.Info("Deploy k8s in join worker node phase")
    
    // step 1: 硬编码的证书加载逻辑
    if err := k.joinWorkerCertCommand(); err != nil {
        return err
    }
    
    // step 2: 硬编码的 kubelet 安装逻辑
    if err := k.installKubeletCommand(); err != nil {
        return err
    }
    
    // step 3: 硬编码的 kubectl 安装逻辑
    if err := k.installKubectlCommand(); err != nil {
        return err
    }
    
    return nil
}
```
**问题分析**:
- Join 配置硬编码在代码中，缺乏灵活性
- 无法自定义 kubelet 参数、容器运行时配置等
- 难以支持不同版本或不同场景的 join 配置

#### 问题3：缺乏节点级配置能力

**当前代码**:
```go
// 所有 Worker 节点使用相同的配置
func (e *EnsureWorkerJoin) scaleMachineDeployment(params) error {
    // 只调整副本数，无法针对单个节点定制配置
    params.Scope.MachineDeployment.Spec.Replicas = &exceptReplicas
}
```

**问题分析**:
- 所有 Worker 节点使用相同的配置模板
- 无法为特定节点设置不同的 kubelet 参数
- 无法支持异构集群（如 GPU 节点、高性能节点等）

#### 问题4：Bootstrap 流程不标准

**当前流程**:
```
BKEMachine Controller
  ├── 创建 Agent Command (phase=JoinWorker)
  └── Agent 执行硬编码的 joinWorker()
```

**标准 CAPI 流程**:
```
MachineDeployment Controller
  ├── 创建 Machine
  └── 引用 KubeadmConfigTemplate

KubeadmConfig Controller
  ├── 从 Template 生成 KubeadmConfig
  ├── 生成 JoinConfiguration
  ├── 生成 bootstrap data (cloud-init)
  └── 设置 dataSecretName

Infrastructure Provider
  ├── 读取 bootstrap data
  └── 在节点上执行
```

## 2. 重构目标

### 2.1 核心目标

1. **使用 KubeadmConfigTemplate**
   - 定义 Worker 节点的 join 配置模板
   - 支持灵活的配置参数
2. **激活 CAPI Bootstrap 机制**
   - 让 KubeadmConfig Controller 生成 bootstrap data
   - BKEMachine Controller 使用标准的 bootstrap 流程
3. **支持节点级配置**
   - 通过 KubeadmConfigTemplate 定义默认配置
   - 支持为特定节点定制配置
4. **保持兼容性**
   - 支持新旧两种模式
   - 渐进式迁移

### 2.2 架构对比

**当前架构**:
```
BKE Controller
  ├── EnsureWorkerJoin (调整 MD Replicas)
  └── BKEMachine Controller (下发硬编码 Command)

CAPI Controllers
  └── MachineDeployment Controller (创建 Machine)

Agent
  └── Kubeadm.joinWorker() (硬编码逻辑)
```

**目标架构**:
```
BKE Provider (Infrastructure Provider)
  ├── EnsureWorkerJoin (调整 MD Replicas)
  └── BKEMachine Controller
      ├── 等待 KubeadmConfig 生成 bootstrap data
      ├── 读取 bootstrap Secret
      └── 转换为 Agent Command

CAPI Controllers (Bootstrap Provider)
  ├── MachineDeployment Controller
  │   ├── 创建 Machine
  │   └── 引用 KubeadmConfigTemplate
  │
  └── KubeadmConfig Controller
      ├── 从 Template 生成 KubeadmConfig
      ├── 生成 JoinConfiguration
      ├── 生成 bootstrap data (cloud-init)
      └── 设置 dataSecretName

Agent
  └── KubeadmBootstrap Command
      ├── 解析 cloud-init userData
      ├── 写入文件
      ├── 执行前置脚本
      ├── 执行 kubeadm join
      └── 执行后置脚本
```

## 3. 详细重构方案

### 3.1 新增 BKEKubeadmConfigTemplate CRD

#### 3.1.1 CRD 定义

```go
// api/bootstrap/v1beta1/bke_kubeadm_config_template_types.go
package v1beta1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:labels={"cluster.x-k8s.io/provider=bootstrap-bke"}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.ready"
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
    Spec BKEKubeadmConfigSpec `json:"spec"`
}

type BKEKubeadmConfigSpec struct {
    // 标准的 Kubeadm 配置
    bootstrapv1.KubeadmConfigSpec `json:",inline"`
    
    // BKE 特有的配置
    BKEConfig BKEBootstrapConfig `json:"bkeConfig,omitempty"`
}

type BKEBootstrapConfig struct {
    // 节点角色
    Roles []string `json:"roles,omitempty"`
    
    // 额外文件（支持模板变量）
    ExtraFiles []File `json:"extraFiles,omitempty"`
    
    // 环境初始化脚本
    EnvInitScript string `json:"envInitScript,omitempty"`
    
    // 容器运行时配置
    ContainerRuntime ContainerRuntimeConfig `json:"containerRuntime,omitempty"`
    
    // Kubelet 配置引用
    KubeletConfigRef *ConfigRef `json:"kubeletConfigRef,omitempty"`
}

type File struct {
    Path        string `json:"path"`
    Content     string `json:"content,omitempty"`
    Encoding    string `json:"encoding,omitempty"`
    Permissions string `json:"permissions,omitempty"`
    Owner       string `json:"owner,omitempty"`
}

type ContainerRuntimeConfig struct {
    // CRI 类型：containerd, docker
    Type string `json:"type"`
    
    // 版本
    Version string `json:"version,omitempty"`
    
    // 配置文件内容
    ConfigContent string `json:"configContent,omitempty"`
}

type ConfigRef struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace,omitempty"`
}

type BKEKubeadmConfigTemplateStatus struct {
    // Ready 表示模板是否就绪
    Ready bool `json:"ready,omitempty"`
}
```

#### 3.1.2 使用示例

```yaml
apiVersion: bootstrap.bke.bocloud.com/v1beta1
kind: BKEKubeadmConfigTemplate
metadata:
  name: worker-config-template
  namespace: default
spec:
  template:
    spec:
      # 标准的 JoinConfiguration
      joinConfiguration:
        nodeRegistration:
          name: "{{ .NodeName }}"
          criSocket: /var/run/containerd/containerd.sock
          kubeletExtraArgs:
            cgroup-driver: systemd
            node-labels: "node-role.kubernetes.io/worker="
            eviction-hard: "memory.available<200Mi,nodefs.available<10%"
            max-pods: "110"
            v: "2"
        discovery:
          bootstrapToken:
            apiServerEndpoint: "{{ .ControlPlaneEndpoint }}"
            token: ""
            caCertHashes: []
      
      # 文件列表（推送到节点）
      files:
        - path: /etc/kubernetes/kubelet.conf
          content: |
            kind: KubeletConfiguration
            apiVersion: kubelet.config.k8s.io/v1beta1
            cgroupDriver: systemd
            clusterDNS:
              - "{{ .ClusterDNS }}"
            clusterDomain: "{{ .ClusterDomain }}"
            evictionHard:
              memory.available: "200Mi"
              nodefs.available: "10%"
          permissions: "0644"
        
        - path: /etc/containerd/config.toml
          content: |
            version = 2
            [plugins."io.containerd.grpc.v1.cri"]
              sandbox_image = "{{ .PauseImage }}"
          permissions: "0644"
      
      # 前置脚本
      preKubeadmCommands:
        - "systemctl enable containerd"
        - "systemctl start containerd"
        - "mkdir -p /etc/kubernetes/manifests"
      
      # 后置脚本
      postKubeadmCommands:
        - "kubectl label node {{ .NodeName }} node-role.kubernetes.io/worker="
      
      # BKE 特有配置
      bkeConfig:
        roles:
          - worker
        containerRuntime:
          type: containerd
          version: "1.7.0"
        envInitScript: |
          #!/bin/bash
          set -e
          echo "Initializing worker node environment..."
          modprobe br_netfilter
          echo 1 > /proc/sys/net/bridge/bridge-nf-call-iptables
```

### 3.2 重构 EnsureClusterAPIObj

#### 3.2.1 创建 KubeadmConfigTemplate

```go
// pkg/phaseframe/phases/ensure_cluster_api_obj.go (重构版)
func (e *EnsureClusterAPIObj) createMachineDeployment(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    cfg *bkeinit.BkeConfig,
) (*clusterv1.MachineDeployment, error) {
    // 1. 计算初始 Worker 节点数
    allNodes, _ := e.Ctx.GetNodes()
    workerNodes := allNodes.Worker()
    initialReplicas := len(workerNodes)
    
    // 2. 创建 KubeadmConfigTemplate
    kubeadmConfigTemplate, err := e.createKubeadmConfigTemplate(ctx, bkeCluster, cfg)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create KubeadmConfigTemplate")
    }
    
    // 3. 创建 BKEMachineTemplate
    bkeMachineTemplate, err := e.createBKEMachineTemplate(ctx, bkeCluster, "worker")
    if err != nil {
        return nil, errors.Wrap(err, "failed to create BKEMachineTemplate")
    }
    
    // 4. 创建 MachineDeployment
    md := &clusterv1.MachineDeployment{
        ObjectMeta: metav1.ObjectMeta{
            Name:      bkeCluster.Name + "-worker",
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
        Spec: clusterv1.MachineDeploymentSpec{
            ClusterName: bkeCluster.Name,
            Replicas:    ptr.To[int32](int32(initialReplicas)),
            Selector: metav1.LabelSelector{
                MatchLabels: map[string]string{
                    clusterv1.ClusterNameLabel: bkeCluster.Name,
                },
            },
            Template: clusterv1.MachineTemplateSpec{
                Spec: clusterv1.MachineSpec{
                    ClusterName: bkeCluster.Name,
                    Version:     bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
                    Bootstrap: clusterv1.Bootstrap{
                        ConfigRef: &corev1.ObjectReference{
                            APIVersion: bootstrapv1.GroupVersion.String(),
                            Kind:       "KubeadmConfigTemplate",
                            Name:       kubeadmConfigTemplate.Name,
                            Namespace:  kubeadmConfigTemplate.Namespace,
                        },
                    },
                    InfrastructureRef: corev1.ObjectReference{
                        APIVersion: bkev1beta1.GroupVersion.String(),
                        Kind:       "BKEMachineTemplate",
                        Name:       bkeMachineTemplate.Name,
                        Namespace:  bkeMachineTemplate.Namespace,
                    },
                },
            },
        },
    }
    
    return md, nil
}

// createKubeadmConfigTemplate 创建 KubeadmConfigTemplate
func (e *EnsureClusterAPIObj) createKubeadmConfigTemplate(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    cfg *bkeinit.BkeConfig,
) (*bootstrapv1.KubeadmConfigTemplate, error) {
    // 1. 构建 JoinConfiguration
    joinConfig := e.buildWorkerJoinConfiguration(bkeCluster, cfg)
    
    // 2. 构建文件列表
    files := e.buildWorkerFiles(bkeCluster, cfg)
    
    // 3. 构建前置/后置脚本
    preCommands, postCommands := e.buildWorkerCommands(bkeCluster)
    
    // 4. 创建 KubeadmConfigTemplate
    template := &bootstrapv1.KubeadmConfigTemplate{
        ObjectMeta: metav1.ObjectMeta{
            Name:      bkeCluster.Name + "-worker-config",
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
        Spec: bootstrapv1.KubeadmConfigTemplateSpec{
            Template: bootstrapv1.KubeadmConfigTemplateResource{
                Spec: bootstrapv1.KubeadmConfigSpec{
                    JoinConfiguration: joinConfig,
                    Files:             files,
                    PreKubeadmCommands:  preCommands,
                    PostKubeadmCommands: postCommands,
                },
            },
        },
    }
    
    // 5. 应用用户自定义配置
    if bkeCluster.Spec.ClusterConfig.WorkerKubeadmConfig != nil {
        e.applyCustomKubeadmConfig(template, bkeCluster.Spec.ClusterConfig.WorkerKubeadmConfig)
    }
    
    // 6. 创建到集群
    if err := e.Ctx.Client.Create(ctx, template); err != nil {
        if !apierrors.IsAlreadyExists(err) {
            return nil, err
        }
    }
    
    return template, nil
}

// buildWorkerJoinConfiguration 构建 Worker JoinConfiguration
func (e *EnsureClusterAPIObj) buildWorkerJoinConfiguration(
    bkeCluster *bkev1beta1.BKECluster,
    cfg *bkeinit.BkeConfig,
) bootstrapv1.JoinConfiguration {
    // 获取控制面端点
    controlPlaneEndpoint := e.getControlPlaneEndpoint(bkeCluster)
    
    // 构建 kubelet 额外参数
    kubeletExtraArgs := map[string]string{
        "cgroup-driver": "systemd",
        "node-labels":   "node-role.kubernetes.io/worker=",
        "v":            "2",
    }
    
    // 添加用户自定义参数
    if bkeCluster.Spec.ClusterConfig.Kubelet != nil {
        for k, v := range bkeCluster.Spec.ClusterConfig.Kubelet.ExtraArgs {
            kubeletExtraArgs[k] = v
        }
    }
    
    return bootstrapv1.JoinConfiguration{
        NodeRegistration: bootstrapv1.NodeRegistration{
            Name:             "{{ .LocalHostname }}",  // 模板变量
            CRISocket:        "/var/run/containerd/containerd.sock",
            KubeletExtraArgs: kubeletExtraArgs,
        },
        Discovery: bootstrapv1.Discovery{
            BootstrapToken: &bootstrapv1.BootstrapTokenDiscovery{
                APIServerEndpoint: controlPlaneEndpoint,
                Token:             "",  // 由 CAPI 自动生成
                CACertHashes:      nil, // 由 CAPI 自动填充
            },
        },
    }
}

// buildWorkerFiles 构建 Worker 文件列表
func (e *EnsureClusterAPIObj) buildWorkerFiles(
    bkeCluster *bkev1beta1.BKECluster,
    cfg *bkeinit.BkeConfig,
) []bootstrapv1.File {
    files := []bootstrapv1.File{}
    
    // 1. Kubelet 配置文件
    kubeletConfigContent := e.generateKubeletConfig(bkeCluster, cfg)
    files = append(files, bootstrapv1.File{
        Path:        "/etc/kubernetes/kubelet.conf",
        Content:     kubeletConfigContent,
        Permissions: "0644",
    })
    
    // 2. Containerd 配置文件
    containerdConfigContent := e.generateContainerdConfig(bkeCluster, cfg)
    files = append(files, bootstrapv1.File{
        Path:        "/etc/containerd/config.toml",
        Content:     containerdConfigContent,
        Permissions: "0644",
    })
    
    // 3. 用户自定义文件
    if bkeCluster.Spec.ClusterConfig.Files != nil {
        for _, f := range bkeCluster.Spec.ClusterConfig.Files {
            files = append(files, bootstrapv1.File{
                Path:        f.Path,
                Content:     f.Content,
                Permissions: f.Permissions,
            })
        }
    }
    
    return files
}

// buildWorkerCommands 构建 Worker 前置/后置脚本
func (e *EnsureClusterAPIObj) buildWorkerCommands(
    bkeCluster *bkev1beta1.BKECluster,
) ([]string, []string) {
    preCommands := []string{
        "systemctl enable containerd",
        "systemctl start containerd",
        "mkdir -p /etc/kubernetes/manifests",
    }
    
    postCommands := []string{
        "kubectl label node $(hostname) node-role.kubernetes.io/worker=",
    }
    
    // 添加用户自定义脚本
    if bkeCluster.Spec.ClusterConfig.PreBootstrapScripts != nil {
        preCommands = append(preCommands, bkeCluster.Spec.ClusterConfig.PreBootstrapScripts...)
    }
    
    if bkeCluster.Spec.ClusterConfig.PostBootstrapScripts != nil {
        postCommands = append(postCommands, bkeCluster.Spec.ClusterConfig.PostBootstrapScripts...)
    }
    
    return preCommands, postCommands
}
```

### 3.3 重构 BKEMachine Controller

#### 3.3.1 处理 Bootstrap Data

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
    
    if capiMachine == nil {
        log.Info("Waiting for CAPI Machine to be created")
        return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
    }
    
    // 2. 同步状态
    if err := r.syncMachineStatus(ctx, bkeMachine, capiMachine); err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. 处理 Bootstrap Data（核心重构点）
    if capiMachine.Spec.Bootstrap.ConfigRef != nil {
        result, err := r.handleBootstrapData(ctx, bkeMachine, capiMachine)
        if err != nil {
            return ctrl.Result{}, err
        }
        if result.Requeue {
            return result.Result, nil
        }
    }
    
    // 4. 处理节点生命周期
    return r.reconcileNodeLifecycle(ctx, bkeMachine, capiMachine)
}

// handleBootstrapData 处理 Bootstrap Data
func (r *BKEMachineReconciler) handleBootstrapData(
    ctx context.Context,
    bkeMachine *bkev1beta1.BKEMachine,
    capiMachine *clusterv1.Machine,
) (*ReconcileResult, error) {
    log := ctrl.LoggerFrom(ctx)
    
    // 1. 获取 KubeadmConfig
    kubeadmConfig, err := r.getKubeadmConfig(ctx, capiMachine)
    if err != nil {
        return nil, errors.Wrap(err, "failed to get KubeadmConfig")
    }
    
    if kubeadmConfig == nil {
        log.Info("Waiting for KubeadmConfig to be created")
        return &ReconcileResult{Requeue: true, Result: ctrl.Result{RequeueAfter: 5 * time.Second}}, nil
    }
    
    // 2. 等待 Bootstrap Data 就绪
    if kubeadmConfig.Status.DataSecretName == nil || *kubeadmConfig.Status.DataSecretName == "" {
        log.Info("Waiting for bootstrap data to be ready",
            "kubeadmConfig", kubeadmConfig.Name,
            "status", kubeadmConfig.Status)
        return &ReconcileResult{Requeue: true, Result: ctrl.Result{RequeueAfter: 5 * time.Second}}, nil
    }
    
    // 3. 检查是否已创建 Command
    if bkeMachine.Status.BootstrapDataApplied {
        log.Info("Bootstrap data already applied")
        return &ReconcileResult{Requeue: false}, nil
    }
    
    // 4. 获取 Bootstrap Secret
    bootstrapSecret := &corev1.Secret{}
    if err := r.Get(ctx, client.ObjectKey{
        Namespace: capiMachine.Namespace,
        Name:      *kubeadmConfig.Status.DataSecretName,
    }, bootstrapSecret); err != nil {
        return nil, errors.Wrap(err, "failed to get bootstrap secret")
    }
    
    // 5. 将 Bootstrap Data 转换为 Agent Command
    if err := r.applyBootstrapData(ctx, bkeMachine, capiMachine, bootstrapSecret); err != nil {
        return nil, errors.Wrap(err, "failed to apply bootstrap data")
    }
    
    // 6. 标记已应用
    bkeMachine.Status.BootstrapDataApplied = true
    if err := r.Status().Update(ctx, bkeMachine); err != nil {
        return nil, err
    }
    
    log.Info("Bootstrap data applied successfully")
    return &ReconcileResult{Requeue: false}, nil
}

// getKubeadmConfig 获取 KubeadmConfig
func (r *BKEMachineReconciler) getKubeadmConfig(
    ctx context.Context,
    machine *clusterv1.Machine,
) (*bootstrapv1.KubeadmConfig, error) {
    if machine.Spec.Bootstrap.ConfigRef == nil {
        return nil, nil
    }
    
    kubeadmConfig := &bootstrapv1.KubeadmConfig{}
    if err := r.Get(ctx, client.ObjectKey{
        Namespace: machine.Spec.Bootstrap.ConfigRef.Namespace,
        Name:      machine.Spec.Bootstrap.ConfigRef.Name,
    }, kubeadmConfig); err != nil {
        if apierrors.IsNotFound(err) {
            return nil, nil
        }
        return nil, err
    }
    
    return kubeadmConfig, nil
}

// applyBootstrapData 应用 Bootstrap Data
func (r *BKEMachineReconciler) applyBootstrapData(
    ctx context.Context,
    bkeMachine *bkev1beta1.BKEMachine,
    capiMachine *clusterv1.Machine,
    bootstrapSecret *corev1.Secret,
) error {
    log := ctrl.LoggerFrom(ctx)
    
    // 1. 解析 Bootstrap Data
    userData := string(bootstrapSecret.Data["value"])
    if userData == "" {
        return errors.New("bootstrap secret has no value key")
    }
    
    // 2. 判断节点角色
    role := r.determineNodeRole(bkeMachine)
    
    // 3. 创建 Agent Command
    command := &agentv1beta1.Command{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("%s-bootstrap", bkeMachine.Name),
            Namespace: bkeMachine.Namespace,
            Labels: map[string]string{
                bkev1beta1.BKEMachineNameLabel: bkeMachine.Name,
                bkev1beta1.ClusterNameLabel:    bkeMachine.Spec.ClusterName,
            },
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
                "role":     role,
                "userData": userData,
            },
        },
    }
    
    if err := r.Create(ctx, command); err != nil {
        if !apierrors.IsAlreadyExists(err) {
            return errors.Wrap(err, "failed to create bootstrap command")
        }
        log.Info("Bootstrap command already exists")
    }
    
    return nil
}

// determineNodeRole 判断节点角色
func (r *BKEMachineReconciler) determineNodeRole(bkeMachine *bkev1beta1.BKEMachine) string {
    if bkeMachine.Labels == nil {
        return "worker"
    }
    
    if _, ok := bkeMachine.Labels[clusterv1.MachineControlPlaneLabel]; ok {
        return "controlplane"
    }
    
    return "worker"
}
```

### 3.4 新增 Agent KubeadmBootstrap 命令

#### 3.4.1 命令实现

```go
// pkg/agent/commands/kubeadm_bootstrap.go
package commands

import (
    "encoding/base64"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    
    "github.com/pkg/errors"
    "gopkg.in/yaml.v2"
)

type KubeadmBootstrapCommand struct {
    BaseCommand
}

// CloudInitUserData 表示 cloud-init userData 结构
type CloudInitUserData struct {
    WriteFiles        []File       `yaml:"write_files,omitempty"`
    Runcmd            []string     `yaml:"runcmd,omitempty"`
    PreKubeadmCommands []string    `yaml:"preKubeadmCommands,omitempty"`
    PostKubeadmCommands []string   `yaml:"postKubeadmCommands,omitempty"`
    InitConfiguration  interface{} `yaml:"initConfiguration,omitempty"`
    JoinConfiguration  interface{} `yaml:"joinConfiguration,omitempty"`
}

type File struct {
    Path        string `yaml:"path"`
    Content     string `yaml:"content"`
    Encoding    string `yaml:"encoding,omitempty"`
    Permissions string `yaml:"permissions,omitempty"`
    Owner       string `yaml:"owner,omitempty"`
}

func (k *KubeadmBootstrapCommand) Execute(extraData map[string]string) error {
    role := extraData["role"]
    userData := extraData["userData"]
    
    log.Info("Executing KubeadmBootstrap command", "role", role)
    
    // 1. 解析 cloud-init userData
    cloudInit, err := k.parseCloudInitUserData(userData)
    if err != nil {
        return errors.Wrap(err, "failed to parse cloud-init userData")
    }
    
    // 2. 写入文件
    if err := k.writeFiles(cloudInit.WriteFiles); err != nil {
        return errors.Wrap(err, "failed to write files")
    }
    
    // 3. 执行前置命令
    preCommands := cloudInit.PreKubeadmCommands
    if len(preCommands) == 0 {
        preCommands = cloudInit.Runcmd  // 兼容标准 cloud-init
    }
    if err := k.executeCommands(preCommands, "pre-kubeadm"); err != nil {
        return errors.Wrap(err, "failed to execute pre-kubeadm commands")
    }
    
    // 4. 执行 kubeadm
    switch role {
    case "controlplane":
        if cloudInit.InitConfiguration != nil {
            if err := k.executeKubeadmInit(cloudInit.InitConfiguration); err != nil {
                return errors.Wrap(err, "failed to execute kubeadm init")
            }
        } else if cloudInit.JoinConfiguration != nil {
            if err := k.executeKubeadmJoin(cloudInit.JoinConfiguration, true); err != nil {
                return errors.Wrap(err, "failed to execute kubeadm join controlplane")
            }
        }
    case "worker":
        if cloudInit.JoinConfiguration == nil {
            return errors.New("JoinConfiguration is required for worker node")
        }
        if err := k.executeKubeadmJoin(cloudInit.JoinConfiguration, false); err != nil {
            return errors.Wrap(err, "failed to execute kubeadm join")
        }
    default:
        return errors.Errorf("unknown role: %s", role)
    }
    
    // 5. 执行后置命令
    if err := k.executeCommands(cloudInit.PostKubeadmCommands, "post-kubeadm"); err != nil {
        return errors.Wrap(err, "failed to execute post-kubeadm commands")
    }
    
    log.Info("KubeadmBootstrap completed successfully")
    return nil
}

// parseCloudInitUserData 解析 cloud-init userData
func (k *KubeadmBootstrapCommand) parseCloudInitUserData(userData string) (*CloudInitUserData, error) {
    // cloud-init userData 可能以 "#cloud-config" 开头
    content := strings.TrimPrefix(userData, "#cloud-config\n")
    content = strings.TrimSpace(content)
    
    cloudInit := &CloudInitUserData{}
    if err := yaml.Unmarshal([]byte(content), cloudInit); err != nil {
        return nil, errors.Wrap(err, "failed to unmarshal userData")
    }
    
    return cloudInit, nil
}

// writeFiles 写入文件
func (k *KubeadmBootstrapCommand) writeFiles(files []File) error {
    for _, file := range files {
        log.Info("Writing file", "path", file.Path)
        
        // 1. 创建目录
        dir := filepath.Dir(file.Path)
        if err := os.MkdirAll(dir, 0755); err != nil {
            return errors.Wrapf(err, "failed to create directory %s", dir)
        }
        
        // 2. 解码内容
        content := file.Content
        if file.Encoding == "base64" {
            decoded, err := base64.StdEncoding.DecodeString(content)
            if err != nil {
                return errors.Wrapf(err, "failed to decode base64 content for %s", file.Path)
            }
            content = string(decoded)
        }
        
        // 3. 写入文件
        permissions := os.FileMode(0644)
        if file.Permissions != "" {
            if perm, err := strconv.ParseUint(file.Permissions, 8, 32); err == nil {
                permissions = os.FileMode(perm)
            }
        }
        
        if err := os.WriteFile(file.Path, []byte(content), permissions); err != nil {
            return errors.Wrapf(err, "failed to write file %s", file.Path)
        }
        
        // 4. 设置所有者
        if file.Owner != "" {
            if err := k.chown(file.Path, file.Owner); err != nil {
                log.Warn("Failed to change owner", "path", file.Path, "owner", file.Owner, "error", err)
            }
        }
    }
    
    return nil
}

// executeCommands 执行命令列表
func (k *KubeadmBootstrapCommand) executeCommands(commands []string, phase string) error {
    for i, cmd := range commands {
        log.Info("Executing command", "phase", phase, "index", i, "command", cmd)
        
        // 使用 shell 执行命令（支持管道、重定向等）
        execCmd := exec.Command("sh", "-c", cmd)
        output, err := execCmd.CombinedOutput()
        if err != nil {
            return errors.Wrapf(err, "command failed: %s, output: %s", cmd, string(output))
        }
        
        log.Info("Command executed successfully", "phase", phase, "index", i, "output", string(output))
    }
    
    return nil
}

// executeKubeadmInit 执行 kubeadm init
func (k *KubeadmBootstrapCommand) executeKubeadmInit(initConfig interface{}) error {
    log.Info("Executing kubeadm init")
    
    // 1. 生成 kubeadm-config.yaml
    configYaml, err := yaml.Marshal(initConfig)
    if err != nil {
        return errors.Wrap(err, "failed to marshal InitConfiguration")
    }
    
    configFile := "/tmp/kubeadm-init-config.yaml"
    if err := os.WriteFile(configFile, configYaml, 0644); err != nil {
        return errors.Wrap(err, "failed to write kubeadm config")
    }
    defer os.Remove(configFile)
    
    // 2. 执行 kubeadm init
    cmd := exec.Command("kubeadm", "init", "--config", configFile, "--ignore-preflight-errors=all")
    output, err := cmd.CombinedOutput()
    if err != nil {
        return errors.Wrapf(err, "kubeadm init failed: %s", string(output))
    }
    
    log.Info("kubeadm init completed successfully")
    return nil
}

// executeKubeadmJoin 执行 kubeadm join
func (k *KubeadmBootstrapCommand) executeKubeadmJoin(joinConfig interface{}, controlPlane bool) error {
    log.Info("Executing kubeadm join", "controlPlane", controlPlane)
    
    // 1. 生成 kubeadm-config.yaml
    configYaml, err := yaml.Marshal(joinConfig)
    if err != nil {
        return errors.Wrap(err, "failed to marshal JoinConfiguration")
    }
    
    configFile := "/tmp/kubeadm-join-config.yaml"
    if err := os.WriteFile(configFile, configYaml, 0644); err != nil {
        return errors.Wrap(err, "failed to write kubeadm config")
    }
    defer os.Remove(configFile)
    
    // 2. 执行 kubeadm join
    args := []string{"join", "--config", configFile, "--ignore-preflight-errors=all"}
    cmd := exec.Command("kubeadm", args...)
    output, err := cmd.CombinedOutput()
    if err != nil {
        return errors.Wrapf(err, "kubeadm join failed: %s", string(output))
    }
    
    log.Info("kubeadm join completed successfully")
    return nil
}

// chown 修改文件所有者
func (k *KubeadmBootstrapCommand) chown(path, owner string) error {
    // 解析 owner（格式：user:group 或 user）
    parts := strings.Split(owner, ":")
    if len(parts) == 0 {
        return nil
    }
    
    user := parts[0]
    group := ""
    if len(parts) > 1 {
        group = parts[1]
    }
    
    // 查找 user 和 group 的 UID/GID
    uid, gid := -1, -1
    
    if u, err := user.Lookup(user); err == nil {
        if uidInt, err := strconv.Atoi(u.Uid); err == nil {
            uid = uidInt
        }
    }
    
    if group != "" {
        if g, err := user.LookupGroup(group); err == nil {
            if gidInt, err := strconv.Atoi(g.Gid); err == nil {
                gid = gidInt
            }
        }
    }
    
    if uid != -1 || gid != -1 {
        return os.Chown(path, uid, gid)
    }
    
    return nil
}
```

#### 3.4.2 注册命令

```go
// pkg/agent/commands/register.go
package commands

func init() {
    // 注册 KubeadmBootstrap 命令
    RegisterCommand("KubeadmBootstrap", &KubeadmBootstrapCommand{})
}
```

### 3.5 重构 EnsureWorkerJoin

#### 3.5.1 简化逻辑

```go
// pkg/phaseframe/phases/ensure_worker_join.go (重构版)
func (e *EnsureWorkerJoin) Execute() (ctrl.Result, error) {
    ctx, c, bkeCluster, cluster, log := e.Ctx.Untie()
    
    // 1. 检查控制平面是否已初始化
    if !conditions.IsTrue(cluster, clusterv1.ControlPlaneInitializedCondition) {
        log.Info("Control plane not initialized, waiting...")
        return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
    }
    
    // 2. 获取期望的 Worker 节点数
    desiredWorkerCount := e.getDesiredWorkerCount()
    
    // 3. 获取 MachineDeployment
    md, err := phaseutil.GetClusterAPIMachineDeployment(ctx, c, cluster)
    if err != nil {
        return ctrl.Result{}, errors.Wrap(err, "failed to get MachineDeployment")
    }
    
    if md == nil {
        return ctrl.Result{}, errors.New("MachineDeployment not found")
    }
    
    // 4. 计算当前实际的 Worker 节点数
    currentWorkerCount := int(*md.Spec.Replicas)
    
    // 5. 如果副本数一致，检查是否所有节点都已就绪
    if currentWorkerCount == desiredWorkerCount {
        return e.waitForWorkersReady(ctx, c, cluster, desiredWorkerCount)
    }
    
    // 6. 声明式更新 MachineDeployment Replicas
    log.Info("Scaling workers from %d to %d replicas", currentWorkerCount, desiredWorkerCount)
    
    md.Spec.Replicas = ptr.To[int32](int32(desiredWorkerCount))
    if err := c.Update(ctx, md); err != nil {
        return ctrl.Result{}, errors.Wrap(err, "failed to update MachineDeployment replicas")
    }
    
    // 7. 等待扩缩容完成
    return e.waitForWorkersReady(ctx, c, cluster, desiredWorkerCount)
}

// waitForWorkersReady 等待所有 Worker 节点就绪
func (e *EnsureWorkerJoin) waitForWorkersReady(
    ctx context.Context,
    c client.Client,
    cluster *clusterv1.Cluster,
    desiredCount int,
) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)
    
    // 获取所有 Worker Machine
    machines, err := phaseutil.GetWorkerMachines(ctx, c, cluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 统计就绪节点数
    readyCount := 0
    failedNodes := []string{}
    
    for _, machine := range machines {
        if machine.Status.NodeRef != nil {
            readyCount++
        } else if machine.Status.FailureReason != nil || machine.Status.FailureMessage != nil {
            failedNodes = append(failedNodes, machine.Name)
        }
    }
    
    log.Info("Worker nodes status", "ready", readyCount, "desired", desiredCount, "failed", len(failedNodes))
    
    // 如果有失败节点，记录警告但不阻塞
    if len(failedNodes) > 0 {
        log.Warn("Some worker nodes failed", "nodes", failedNodes)
    }
    
    // 如果所有节点都已就绪
    if readyCount == desiredCount {
        log.Info("All worker nodes are ready")
        return ctrl.Result{}, nil
    }
    
    // 如果有部分节点就绪，继续等待
    if readyCount > 0 {
        log.Info("Some worker nodes are ready, waiting for others", "ready", readyCount, "desired", desiredCount)
        return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
    }
    
    // 如果没有节点就绪，继续等待
    log.Info("Waiting for worker nodes to be ready")
    return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
```

## 4. 迁移步骤

### 4.1 阶段一：准备工作（低风险）

**步骤1**: 新增 BKEKubeadmConfigTemplate CRD（可选）
```bash
# 如果需要 BKE 特有的配置，创建 CRD
kubebuilder create api --group bootstrap --version v1beta1 --kind BKEKubeadmConfigTemplate
```

**步骤2**: 修改 EnsureClusterAPIObj，创建 KubeadmConfigTemplate
```go
// 修改点：
// 1. 创建 KubeadmConfigTemplate
// 2. MachineDeployment 引用 KubeadmConfigTemplate
// 3. 移除 dataSecretName: "fake"
```

**步骤3**: 新增 Agent KubeadmBootstrap 命令
```go
// 新增命令类型，处理 CAPI 生成的 bootstrap data
// 见 3.4 节
```

### 4.2 阶段二：核心重构（中风险）

**步骤1**: 重构 BKEMachine Controller
```go
// 修改点：
// 1. 等待 KubeadmConfig 生成 bootstrap data
// 2. 读取 bootstrap Secret
// 3. 转换为 Agent Command
// 4. 标记 BootstrapDataApplied
```

**步骤2**: 测试 Worker 节点 Join
```bash
# 1. 创建 BKECluster
# 2. 检查 KubeadmConfigTemplate 是否创建
# 3. 检查 KubeadmConfig 是否生成
# 4. 检查 bootstrap Secret 是否生成
# 5. 检查 Agent Command 是否创建
# 6. 检查 Worker 节点是否成功加入
```

### 4.3 阶段三：移除旧逻辑（高风险）

**步骤1**: 移除硬编码的 joinWorker 逻辑
```go
// 移除：
// - KubeadmPlugin.joinWorker()
// - 相关的硬编码配置
// - dataSecretName: "fake"
```

**步骤2**: 更新文档和测试
```bash
# 1. 更新部署文档
# 2. 更新 API 文档
# 3. 添加集成测试
```

## 5. 配置示例

### 5.1 标准配置

```yaml
apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
kind: KubeadmConfigTemplate
metadata:
  name: my-cluster-worker-config
  namespace: default
spec:
  template:
    spec:
      joinConfiguration:
        nodeRegistration:
          name: "{{ .LocalHostname }}"
          criSocket: /var/run/containerd/containerd.sock
          kubeletExtraArgs:
            cgroup-driver: systemd
            node-labels: "node-role.kubernetes.io/worker="
            v: "2"
        discovery:
          bootstrapToken:
            apiServerEndpoint: "192.168.1.100:6443"
      
      files:
        - path: /etc/kubernetes/kubelet.conf
          content: |
            kind: KubeletConfiguration
            apiVersion: kubelet.config.k8s.io/v1beta1
            cgroupDriver: systemd
          permissions: "0644"
      
      preKubeadmCommands:
        - "systemctl enable containerd"
        - "systemctl start containerd"
      
      postKubeadmCommands:
        - "kubectl label node $(hostname) node-role.kubernetes.io/worker="
```

### 5.2 GPU 节点配置

```yaml
apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
kind: KubeadmConfigTemplate
metadata:
  name: my-cluster-gpu-worker-config
  namespace: default
spec:
  template:
    spec:
      joinConfiguration:
        nodeRegistration:
          name: "{{ .LocalHostname }}"
          criSocket: /var/run/containerd/containerd.sock
          kubeletExtraArgs:
            cgroup-driver: systemd
            node-labels: "node-role.kubernetes.io/worker=,nvidia.com/gpu=true"
            v: "2"
      
      files:
        - path: /etc/kubernetes/kubelet.conf
          content: |
            kind: KubeletConfiguration
            apiVersion: kubelet.config.k8s.io/v1beta1
            cgroupDriver: systemd
            featureGates:
              DevicePlugins: true
          permissions: "0644"
      
      preKubeadmCommands:
        - "systemctl enable containerd"
        - "systemctl start containerd"
        - "nvidia-smi"  # 验证 GPU 驱动
      
      postKubeadmCommands:
        - "kubectl label node $(hostname) node-role.kubernetes.io/worker="
        - "kubectl label node $(hostname) nvidia.com/gpu=true"
```

## 6. 测试策略

### 6.1 单元测试

```go
// pkg/phaseframe/phases/ensure_cluster_api_obj_test.go
func TestCreateKubeadmConfigTemplate(t *testing.T) {
    tests := []struct {
        name        string
        bkeCluster  *bkev1beta1.BKECluster
        expectError bool
    }{
        {
            name: "standard worker config",
            bkeCluster: &bkev1beta1.BKECluster{
                Spec: bkev1beta1.BKEClusterSpec{
                    ClusterConfig: &confv1beta1.ClusterConfig{
                        Cluster: &confv1beta1.Cluster{
                            KubernetesVersion: "v1.28.0",
                        },
                        Networking: &confv1beta1.Networking{
                            PodSubnet:     "10.244.0.0/16",
                            ServiceSubnet: "10.96.0.0/12",
                        },
                    },
                },
            },
            expectError: false,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 测试逻辑
        })
    }
}
```

### 6.2 集成测试

```go
// test/integration/worker_join_test.go
func TestWorkerJoinWithKubeadmConfigTemplate(t *testing.T) {
    // 1. 创建 BKECluster
    // 2. 等待 KubeadmConfigTemplate 创建
    // 3. 等待 Worker 节点加入
    // 4. 验证节点配置
}
```

## 7. 风险评估与缓解

### 7.1 风险列表

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| Bootstrap Data 格式不兼容 | 高 | 中 | 兼容多种格式，充分测试 |
| KubeadmConfigTemplate 配置错误 | 中 | 中 | 配置验证，默认值填充 |
| Agent 命令执行失败 | 高 | 低 | 错误处理，重试机制 |
| 迁移过程中集群不可用 | 高 | 低 | 渐进式迁移，回滚机制 |

### 7.2 回滚策略

```go
// 支持新旧两种模式
type BootstrapMode string

const (
    BootstrapModeLegacy BootstrapMode = "Legacy"   // 旧模式（硬编码）
    BootstrapModeCAPI   BootstrapMode = "CAPI"     // 新模式
)

// 通过 Annotation 控制模式
func getBootstrapMode(bkeCluster *bkev1beta1.BKECluster) BootstrapMode {
    mode := bkeCluster.Annotations["bootstrap.bke.bocloud.com/mode"]
    if mode == "" {
        return BootstrapModeCAPI  // 默认新模式
    }
    return BootstrapMode(mode)
}
```

## 8. 总结

### 8.1 重构收益

| 收益 | 说明 |
|------|------|
| **标准化** | 使用 CAPI 标准的 Bootstrap 机制 |
| **灵活性** | 通过 KubeadmConfigTemplate 自定义配置 |
| **可维护性** | 移除硬编码逻辑，配置即代码 |
| **功能增强** | 支持节点级配置、异构集群 |
| **兼容性** | 与 CAPI 生态无缝集成 |

### 8.2 工作量估算

| 任务 | 工作量 | 优先级 |
|------|--------|--------|
| 新增 BKEKubeadmConfigTemplate CRD | 2 人日 | P2 |
| 重构 EnsureClusterAPIObj | 3 人日 | P1 |
| 新增 Agent KubeadmBootstrap 命令 | 3 人日 | P1 |
| 重构 BKEMachine Controller | 5 人日 | P1 |
| 重构 EnsureWorkerJoin | 2 人日 | P1 |
| 测试与文档 | 5 人日 | P1 |
| **总计** | **20 人日** | - |

---

**文档完成**: 本方案详细分析了当前工作节点 join 的实现问题，给出了使用 KubeadmConfigTemplate 进行重构的完整方案，包括 CRD 定义、Controller 重构、Agent 命令实现、配置示例等，并提供了详细的迁移步骤和风险评估。


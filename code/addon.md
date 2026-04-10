# 基于 Cluster API 安装 K8s 集群 + Containerd 的设计方案  
## 基于 Cluster API 安装 K8s 集群 + Containerd 的设计方案
### 一、整体设计思路
```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           设计原则                                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   1. 职责分离原则                                                            │
│      ├── Cluster API: 集群生命周期管理                                       │
│      ├── Infrastructure Provider: 基础设施资源管理                          │
│      ├── Bootstrap Provider: 节点引导配置生成                               │
│      └── 自定义 Provider: 容器运行时安装与配置                               │
│                                                                             │
│   2. 时序依赖原则                                                            │
│      ├── Containerd 安装必须在 Kubeadm 执行前完成                            │
│      ├── 节点环境准备必须在集群初始化前完成                                   │
│      └── 组件安装顺序: OS配置 → Containerd → Kubernetes                     │
│                                                                             │
│   3. 可扩展原则                                                              │
│      ├── 支持多种容器运行时                          │
│      ├── 支持不同操作系统                                    │
│      └── 支持版本管理和升级                                                  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```
### 二、架构设计
```
┌────────────────────────────────────────────────────────────────────────────┐
│                           整体架构                                         │
├────────────────────────────────────────────────────────────────────────────┤
│                                                                            │
│   Management Cluster                                                       │
│   ┌─────────────────────────────────────────────────────────────────────┐  │
│   │                                                                     │  │
│   │   ┌──────────────┐    ┌──────────────┐    ┌──────────────┐          │  │
│   │   │   Cluster    │    │ Kubeadm      │    │    BKE       │          │  │
│   │   │   (CAPI)     │    │ ControlPlane │    │  Cluster     │          │  │
│   │   └──────┬───────┘    └──────┬───────┘    └──────┬───────┘          │  │
│   │          │                   │                   │                  │  │
│   │          │                   │                   │                  │  │
│   │          ▼                   ▼                   ▼                  │  │
│   │   ┌─────────────────────────────────────────────────────────┐       │  │
│   │   │                    Controllers                          │       │  │
│   │   │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐      │       │  │
│   │   │  │  Cluster    │  │  Kubeadm    │  │    BKE      │      │       │  │
│   │   │  │  Controller │  │  Controller │  │  Controller │      │       │  │
│   │   │  └─────────────┘  └─────────────┘  └─────────────┘      │       │  │
│   │   └─────────────────────────────────────────────────────────┘       │  │
│   │                                                                     │  │
│   └─────────────────────────────────────────────────────────────────────┘  │
│                                    │                                       │
│                                    │ 触发                                  │
│                                    ▼                                       │
│   Workload Cluster (Target Nodes)                                          │
│   ┌────────────────────────────────────────────────────────────────────┐   │
│   │                                                                    │   │
│   │   ┌──────────────────────────────────────────────────────────┐     │   │
│   │   │                    Node Bootstrap Flow                   │     │   │
│   │   │                                                          │     │   │
│   │   │  Phase 1          Phase 2          Phase 3               │     │   │
│   │   │  ┌────────┐      ┌─────────┐      ┌────────┐             │     │   │
│   │   │  │ Node   │ ───► │Container│ ───► │Kubeadm │             │     │   │
│   │   │  │ Setup  │      │ Runtime │      │ Init   │             │     │   │
│   │   │  └────────┘      └─────────┘      └────────┘             │     │   │
│   │   │       │               │               │                  │     │   │
│   │   │       ▼               ▼               ▼                  │     │   │
│   │   │  - OS配置        - 安装           - kubeadm              │     │   │
│   │   │  - 内核参数        containerd      init/join             │     │   │
│   │   │  - 系统依赖      - 配置           - 证书生成             │     │   │
│   │   │  - 网络配置        CRI socket      - 控制平面            │     │   │
│   │   │                                                          │     │   │
│   │   └──────────────────────────────────────────────────────────┘     │   │
│   │                                                                    │   │
│   └────────────────────────────────────────────────────────────────────┘   │
│                                                                            │
└────────────────────────────────────────────────────────────────────────────┘
```
### 三、详细设计方案
#### 方案一：扩展 Infrastructure Provider（推荐）
##### 1. CRD 设计
```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: BKECluster
metadata:
  name: my-cluster
spec:
  # 节点配置
  nodes:
    - ip: 192.168.1.10
      role: master
    - ip: 192.168.1.11
      role: worker
      
  # 容器运行时配置
  containerRuntime:
    type: containerd          # containerd / cri-o / docker
    version: "1.7.2"
    config:
      systemdCgroup: true
      registryMirrors:
        - "https://registry.example.com"
      insecureRegistries:
        - "registry.local"
        
  # Kubernetes 版本
  kubernetesVersion: "v1.28.0"
  
  # 控制平面端点
  controlPlaneEndpoint:
    host: 192.168.1.100
    port: 6443
```
##### 2. Phase 流程设计
```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Phase 执行顺序                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   Phase 1: 节点环境准备                                                      │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │ EnsureNodesEnv                                                       │   │
│   │ ├── 1.1 系统检查 (OS版本、内核版本、资源)                              │   │
│   │ ├── 1.2 内核参数配置                                 │   │
│   │ ├── 1.3 系统依赖安装 (socat, conntrack, ipset, etc.)                 │   │
│   │ ├── 1.4 关闭 Swap                                                    │   │
│   │ └── 1.5 时间同步配置                                                  │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│   Phase 2: 容器运行时安装                                                    │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │ EnsureContainerRuntime                                               │   │
│   │ ├── 2.1 下载 containerd 包                                           │   │
│   │ ├── 2.2 安装 containerd                                              │   │
│   │ ├── 2.3 生成配置文件                                 │   │
│   │ │     ├── SystemdCgroup = true                                       │   │
│   │ │     ├── Registry mirrors                                           │   │
│   │ │     └── CRI plugin 配置                                             │   │
│   │ ├── 2.4 安装 runc                                                    │   │
│   │ ├── 2.5 安装 CNI plugins                                             │   │
│   │ ├── 2.6 启动 containerd 服务                                         │   │
│   │ └── 2.7 验证 containerd 状态                                         │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│   Phase 3: Kubeadm 引导                                                     │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │ EnsureMasterInit / EnsureMasterJoin / EnsureWorkerJoin              │   │
│   │ ├── 3.1 生成 kubeadm 配置                                            │   │
│   │ ├── 3.2 执行 kubeadm init/join                                       │   │
│   │ ├── 3.3 配置 kubelet                                                 │   │
│   │ └── 3.4 验证节点状态                                                  │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```
##### 3. 代码实现结构
```
pkg/
├── phaseruntime/
│   ├── containerd/
│   │   ├── installer.go       # Containerd 安装器
│   │   ├── config.go          # 配置生成
│   │   ├── validator.go       # 安装验证
│   │   └── upgrader.go        # 版本升级
│   └── node/
│       ├── setup.go           # 节点环境设置
│       └── validator.go       # 节点验证
│
├── phaseframe/
│   └── phases/
│       ├── ensure_nodes_env.go         # Phase 1
│       ├── ensure_container_runtime.go # Phase 2
│       ├── ensure_master_init.go       # Phase 3a
│       ├── ensure_master_join.go       # Phase 3b
│       └── ensure_worker_join.go       # Phase 3c
```
##### 4. Containerd 安装器实现
```go
package containerd

import (
    "context"
    "fmt"
    "text/template"
    
    "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
)

type Installer struct {
    client    CommandExecutor
    osType    string
    arch      string
}

type InstallConfig struct {
    Version           string
    SystemdCgroup     bool
    RegistryMirrors   []string
    InsecureRegistries []string
    RootDir           string
}

func (i *Installer) Install(ctx context.Context, config InstallConfig) error {
    steps := []struct {
        name string
        fn   func(context.Context, InstallConfig) error
    }{
        {"download", i.download},
        {"install", i.install},
        {"configure", i.configure},
        {"installDeps", i.installDependencies},
        {"start", i.start},
        {"verify", i.verify},
    }
    
    for _, step := range steps {
        if err := step.fn(ctx, config); err != nil {
            return fmt.Errorf("step %s failed: %w", step.name, err)
        }
    }
    return nil
}

func (i *Installer) download(ctx context.Context, config InstallConfig) error {
    downloadURL := fmt.Sprintf(
        "https://github.com/containerd/containerd/releases/download/v%s/containerd-%s-linux-%s.tar.gz",
        config.Version, config.Version, i.arch,
    )
    
    return i.client.Execute(ctx, fmt.Sprintf(
        "curl -L %s -o /tmp/containerd.tar.gz", downloadURL,
    ))
}

func (i *Installer) install(ctx context.Context, config InstallConfig) error {
    return i.client.Execute(ctx, 
        "tar -xzf /tmp/containerd.tar.gz -C /usr/local/bin --strip-components=1 bin/containerd",
    )
}

func (i *Installer) configure(ctx context.Context, config InstallConfig) error {
    configContent, err := i.generateConfig(config)
    if err != nil {
        return err
    }
    
    return i.client.WriteFile(ctx, "/etc/containerd/config.toml", configContent, 0644)
}

func (i *Installer) generateConfig(config InstallConfig) (string, error) {
    const configTemplate = `version = 2

[plugins."io.containerd.grpc.v1.cri"]
  sandbox_image = "registry.k8s.io/pause:3.9"
  
  [plugins."io.containerd.grpc.v1.cri".containerd]
    snapshotter = "overlayfs"
    
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
      runtime_type = "io.containerd.runc.v2"
      
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
        SystemdCgroup = {{ .SystemdCgroup }}
  
  [plugins."io.containerd.grpc.v1.cri".registry]
    {{- range .RegistryMirrors }}
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."{{ . }}"]
      endpoint = ["https://{{ . }}"]
    {{- end }}
    
    {{- range .InsecureRegistries }}
    [plugins."io.containerd.grpc.v1.cri".registry.configs."{{ . }}".tls]
      insecure_skip_verify = true
    {{- end }}

[grpc]
  address = "/run/containerd/containerd.sock"
`
    
    tmpl, err := template.New("config").Parse(configTemplate)
    if err != nil {
        return "", err
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, config); err != nil {
        return "", err
    }
    
    return buf.String(), nil
}

func (i *Installer) installDependencies(ctx context.Context, config InstallConfig) error {
    // 安装 runc
    if err := i.installRunc(ctx); err != nil {
        return err
    }
    
    // 安装 CNI plugins
    return i.installCNI(ctx)
}

func (i *Installer) start(ctx context.Context, config InstallConfig) error {
    // 创建 systemd service 文件
    serviceContent := `[Unit]
Description=containerd container runtime
Documentation=https://containerd.io
After=network.target local-fs.target

[Service]
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/containerd
Type=notify
Delegate=yes
KillMode=process
Restart=always
RestartSec=5
LimitNPROC=infinity
LimitCORE=infinity
LimitNOFILE=infinity
TasksMax=infinity
OOMScoreAdjust=-999

[Install]
WantedBy=multi-user.target
`
    if err := i.client.WriteFile(ctx, "/etc/systemd/system/containerd.service", serviceContent, 0644); err != nil {
        return err
    }
    
    // 启动服务
    return i.client.Execute(ctx, "systemctl daemon-reload && systemctl enable --now containerd")
}

func (i *Installer) verify(ctx context.Context, config InstallConfig) error {
    output, err := i.client.ExecuteWithOutput(ctx, "ctr version")
    if err != nil {
        return fmt.Errorf("containerd not running: %w", err)
    }
    
    if !strings.Contains(output, config.Version) {
        return fmt.Errorf("containerd version mismatch: expected %s", config.Version)
    }
    
    return nil
}
```
##### 5. Phase 实现
```go
package phases

import (
    "context"
    "fmt"
    
    ctrl "sigs.k8s.io/controller-runtime"
    
    "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseruntime/containerd"
)

const EnsureContainerRuntimeName = "EnsureContainerRuntime"

type EnsureContainerRuntime struct {
    phaseframe.BasePhase
    installer *containerd.Installer
}

func NewEnsureContainerRuntime(ctx *phaseframe.PhaseContext) phaseframe.Phase {
    base := phaseframe.NewBasePhase(ctx, EnsureContainerRuntimeName)
    return &EnsureContainerRuntime{
        BasePhase: base,
        installer: containerd.NewInstaller(ctx.Client, ctx.NodeOS, ctx.NodeArch),
    }
}

func (e *EnsureContainerRuntime) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 检查是否需要安装或升级
    if old == nil {
        return true // 新集群，需要安装
    }
    
    // 版本变更，需要升级
    if old.Spec.ContainerRuntime.Version != new.Spec.ContainerRuntime.Version {
        return true
    }
    
    // 配置变更，需要重新配置
    if !reflect.DeepEqual(old.Spec.ContainerRuntime.Config, new.Spec.ContainerRuntime.Config) {
        return true
    }
    
    return false
}

func (e *EnsureContainerRuntime) Execute() (ctrl.Result, error) {
    ctx, _, bkeCluster, _, log := e.Ctx.Untie()
    
    nodes, err := e.Ctx.NodeFetcher().GetNodesForBKECluster(ctx, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    config := containerd.InstallConfig{
        Version:            bkeCluster.Spec.ContainerRuntime.Version,
        SystemdCgroup:      bkeCluster.Spec.ContainerRuntime.Config.SystemdCgroup,
        RegistryMirrors:    bkeCluster.Spec.ContainerRuntime.Config.RegistryMirrors,
        InsecureRegistries: bkeCluster.Spec.ContainerRuntime.Config.InsecureRegistries,
    }
    
    // 并行安装所有节点
    var wg sync.WaitGroup
    errChan := make(chan error, len(nodes))
    
    for _, node := range nodes {
        wg.Add(1)
        go func(node confv1beta1.Node) {
            defer wg.Done()
            
            nodeCtx := e.Ctx.WithNode(&node)
            if err := e.installOnNode(nodeCtx, config); err != nil {
                errChan <- fmt.Errorf("node %s: %w", node.IP, err)
            }
        }(node)
    }
    
    wg.Wait()
    close(errChan)
    
    var errs []error
    for err := range errChan {
        errs = append(errs, err)
    }
    
    if len(errs) > 0 {
        return ctrl.Result{}, kerrors.NewAggregate(errs)
    }
    
    log.Info("Container runtime installed successfully on all nodes")
    return ctrl.Result{}, nil
}

func (e *EnsureContainerRuntime) installOnNode(ctx *phaseframe.PhaseContext, config containerd.InstallConfig) error {
    // 通过 Agent 执行安装命令
    cmd := command.NewContainerdInstallCommand(ctx, config)
    
    if err := cmd.Create(); err != nil {
        return fmt.Errorf("create install command: %w", err)
    }
    
    // 等待安装完成
    if err := cmd.Wait(); err != nil {
        return fmt.Errorf("wait install complete: %w", err)
    }
    
    return nil
}
```
#### 方案二：使用 Kubeadm Bootstrap Provider 扩展
##### 1. 扩展 KubeadmConfig
```yaml
apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
kind: KubeadmConfig
metadata:
  name: my-cluster-control-plane
spec:
  # 标准 kubeadm 配置
  clusterConfiguration:
    apiServer:
      certSANs:
        - "192.168.1.100"
    controlPlaneEndpoint: "192.168.1.100:6443"
    
  initConfiguration:
    nodeRegistration:
      criSocket: unix:///var/run/containerd/containerd.sock
      kubeletExtraArgs:
        cgroup-driver: systemd
        
  # 扩展：前置脚本
  preKubeadmCommands:
    - "curl -L https://github.com/containerd/containerd/releases/download/v1.7.2/containerd-1.7.2-linux-amd64.tar.gz -o /tmp/containerd.tar.gz"
    - "tar -xzf /tmp/containerd.tar.gz -C /usr/local/bin --strip-components=1 bin/containerd"
    - "mkdir -p /etc/containerd"
    - "containerd config default > /etc/containerd/config.toml"
    - "sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml"
    - "systemctl enable --now containerd"
    
  # 扩展：后置脚本
  postKubeadmCommands:
    - "kubectl taint nodes --all node-role.kubernetes.io/control-plane-"
```
##### 2. 优缺点对比
| 方案 | 优点 | 缺点 |
|------|------|------|
| **方案一：扩展 Provider** | 完整的生命周期管理、支持升级、状态可观测 | 实现复杂度高 |
| **方案二：Kubeadm 扩展** | 实现简单、符合 Cluster API 模式 | 脚本维护困难、缺乏状态管理 |
### 四、完整实现流程
```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           完整实现流程                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   Step 1: 用户创建资源                                                       │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │ $ kubectl apply -f cluster.yaml                                     │   │
│   │                                                                     │   │
│   │ cluster.yaml:                                                       │   │
│   │ ┌─────────────────────────────────────────────────────────────────┐│   │
│   │ │ apiVersion: cluster.x-k8s.io/v1beta1                           ││   │
│   │ │ kind: Cluster                                                   ││   │
│   │ │ spec:                                                           ││   │
│   │ │   infrastructureRef:                                            ││   │
│   │ │     apiVersion: infrastructure.cluster.x-k8s.io/v1beta1        ││   │
│   │ │     kind: BKECluster                                            ││   │
│   │ │     name: my-cluster                                            ││   │
│   │ └─────────────────────────────────────────────────────────────────┘│   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│   Step 2: BKECluster Controller 协调                                        │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │ Phase 1: EnsureNodesEnv                                             │   │
│   │   ├── 检查节点可达性                                                 │   │
│   │   ├── 配置内核参数                                                   │   │
│   │   └── 安装系统依赖                                                   │   │
│   │                                                                     │   │
│   │ Phase 2: EnsureContainerRuntime                                     │   │
│   │   ├── 下载 containerd                                               │   │
│   │   ├── 安装 containerd + runc + CNI                                  │   │
│   │   ├── 生成配置文件                                                   │   │
│   │   └── 启动服务并验证                                                 │   │
│   │                                                                     │   │
│   │ Phase 3: EnsureBKEAgent                                             │   │
│   │   └── 推送 Agent 到节点                                             │   │
│   │                                                                     │   │
│   │ Phase 4: EnsureClusterAPIObj                                        │   │
│   │   └── 创建 Cluster API 对象                                         │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│   Step 3: Cluster API Controller 协调                                       │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │ KubeadmControlPlane Controller                                      │   │
│   │   ├── 创建 KubeadmConfig                                            │   │
│   │   └── 创建 Machine 对象                                             │   │
│   │                                                                     │   │
│   │ Machine Controller                                                  │   │
│   │   └── 触发 BKEMachine Controller                                    │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│   Step 4: BKEMachine Controller 协调                                        │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │ reconcileBootstrap                                                  │   │
│   │   ├── 分配节点                                                      │   │
│   │   ├── 创建 Bootstrap Command                                        │   │
│   │   │   └── kubeadm init/join (使用已安装的 containerd)               │   │
│   │   └── 等待引导完成                                                  │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│   Step 5: 后续 Phase                                                        │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │ BKECluster Controller 继续执行                                      │   │
│   │   ├── EnsureMasterJoin                                              │   │
│   │   ├── EnsureWorkerJoin                                              │   │
│   │   ├── EnsureAddonDeploy                                             │   │
│   │   └── EnsureCluster                                                 │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```
### 五、关键设计要点
#### 1. 时序保证
```go
// Phase 注册顺序
var FullPhasesRegisFunc = []func(*phaseframe.PhaseContext) phaseframe.Phase{
    // Phase 1: 节点环境准备
    NewEnsureNodesEnv,
    
    // Phase 2: 容器运行时安装（必须在 Kubeadm 之前）
    NewEnsureContainerRuntime,
    
    // Phase 3: Agent 推送
    NewEnsureBKEAgent,
    
    // Phase 4: Cluster API 对象创建
    NewEnsureClusterAPIObj,
    
    // Phase 5+: Kubeadm 引导（由 BKEMachine Controller 执行）
    // ...
}
```
#### 2. 状态持久化
```go
type ContainerRuntimeStatus struct {
    // 安装状态
    Installed   bool      `json:"installed"`
    Version     string    `json:"version"`
    InstallTime time.Time `json:"installTime"`
    
    // 健康状态
    Healthy     bool      `json:"healthy"`
    LastCheck   time.Time `json:"lastCheck"`
    
    // 配置摘要
    ConfigHash  string    `json:"configHash"`
}

// 在 BKENode CRD 中记录状态
type BKENodeStatus struct {
    // ...
    ContainerRuntime ContainerRuntimeStatus `json:"containerRuntime"`
}
```
#### 3. 升级支持
```go
func (e *EnsureContainerRuntime) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 版本升级
    if old.Spec.ContainerRuntime.Version != new.Spec.ContainerRuntime.Version {
        e.SetUpgradeType(RollingUpgrade)  // 滚动升级
        return true
    }
    
    // 配置变更
    if !reflect.DeepEqual(old.Spec.ContainerRuntime.Config, new.Spec.ContainerRuntime.Config) {
        e.SetUpgradeType(InPlaceUpgrade)  // 原地更新配置
        return true
    }
    
    return false
}
```
### 六、总结
| 设计要点 | 说明 |
|----------|------|
| **职责分离** | Containerd 安装由 Infrastructure Provider 负责，Kubeadm 只负责引导 |
| **时序保证** | Containerd 安装必须在 Kubeadm 执行前完成 |
| **状态管理** | 通过 BKENode CRD 记录安装状态和版本信息 |
| **升级支持** | 支持版本升级和配置热更新 |
| **多运行时** | 可扩展支持 containerd/cri-o/docker |

**推荐方案**：采用方案一（扩展 Infrastructure Provider），通过 Phase 机制实现完整的 Containerd 生命周期管理，与现有 BKE 架构保持一致。
        

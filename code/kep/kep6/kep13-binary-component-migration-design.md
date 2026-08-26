# KEP-13: 二进制组件改造设计方案

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-13 |
| **标题** | 二进制组件改造：从 Inline Phase 迁移到 binary 类型 ComponentVersion |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-26 |
| **依赖** | KEP-5 声明式升级框架、声明式集群版本升级方案-支持二进制与Helm组件.md、KEP-9 Static Pod 类型设计 |

---

## 1. 摘要

本提案设计将当前通过 Inline Phase 硬编码执行的二进制组件（bkeagent、containerd、kubelet、kubectl、runc 等）改造为 `binary` 类型 ComponentVersion，纳入 DAG 统一调度。基于已有的 BinaryInstaller 框架（制品下载、模板渲染、配置渲染、SSH 执行、健康检查），为每个二进制组件编写 ComponentVersion YAML 定义，实现声明式安装/升级/卸载。

## 2. 动机

### 2.1 现状问题

| 问题 | 现状 | 影响 |
|------|------|------|
| **硬编码执行** | bkeagent/containerd/kubelet 等通过 Inline Phase 执行 | 无法通过 ComponentVersion 声明式管理 |
| **安装逻辑分散** | 二进制安装逻辑散落在各 Phase 文件和 Shell 脚本中 | 重复代码，维护困难 |
| **无制品校验** | 当前通过脚本直接下载，无 checksum 校验 | 安全风险 |
| **无配置模板化** | 配置文件硬编码在脚本中 | 无法按集群定制 |
| **无统一健康检查** | 健康检查逻辑分散在各 Phase | 缺乏标准化 |

### 2.2 目标

- 将 10 个二进制组件从 Inline Phase 迁移到 `binary` 类型 ComponentVersion
- 为每个组件编写 `installScript` + `configTemplates` + `healthCheck`
- 复用已有的 BinaryInstaller 框架（制品下载、模板渲染、SSH 执行）
- 通过 Feature Gate 控制迁移节奏，新旧路径并存

## 3. 范围与约束

### 3.1 待改造组件清单

| 组件名称 | 当前 Phase | 制品 | 适用角色 | 改造优先级 | 说明 |
|---------|-----------|------|---------|-----------|------|
| **bkeagent** | `EnsureBKEAgent` / `EnsureAgentUpgrade` | bkeagent 二进制 + bkeagent.conf + kubeconfig | master, worker | P0 | 节点 Agent |
| **containerd** | `EnsureContainerdUpgrade` | containerd.tar.gz + config.toml + service | master, worker | P0 | 容器运行时，含 sandbox_image (pause) 配置 |
| **kubelet** | `EnsureMasterUpgrade` / `EnsureWorkerUpgrade` | kubelet + kubelet.conf + service | master, worker | P0 | K8s 节点组件 |
| **kubectl** | `EnsureMasterUpgrade` / `EnsureWorkerUpgrade` | kubectl 二进制 | master, worker | P0 | K8s 命令行工具 |
| **runc** | `EnsureNodesEnv` (update-runc.sh) | runc 二进制 | master, worker | P0 | 容器运行时底层 |
| **helm** | `EnsureNodesEnv` (install-helm.sh) | helm 二进制 | master | P2 | Helm 命令行工具 |
| **etcdctl** | `EnsureNodesEnv` (install-etcdctl.sh) | etcdctl 二进制 | master | P2 | etcd 命令行工具 |
| **calicoctl** | `EnsureNodesEnv` (install-calicoctl.sh) | calicoctl 二进制 | master, worker | P2 | Calico 命令行工具 |
| **lxcfs** | `EnsureNodesEnv` (install-lxcfs.sh) | lxcfs 二进制 + service | master, worker | P2 | 容器文件系统隔离 |
| **nfs-utils** | `EnsureNodesEnv` (install-nfsutils.sh) | nfs-utils 包 | master, worker | P2 | NFS 存储支持 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 必须支持从现有 Inline Phase 平滑迁移 |
| **离线环境** | 二进制制品支持本地缓存 |
| **架构支持** | 必须支持 amd64 和 arm64 |
| **操作系统支持** | 必须支持 CentOS 7/8、Ubuntu 20.04/22.04 |
| **checksum 校验** | 所有制品必须支持 checksum 校验 |
| **幂等性** | 安装/升级操作必须幂等，支持 Reconcile 重入 |

## 4. BinarySpec 类型定义

> BinarySpec 类型定义复用 `声明式集群版本升级方案-支持二进制与Helm组件.md` 第 3.2 节的设计，此处仅做简要说明。

```go
// api/v1alpha1/componentversion_types.go

// BinarySpec 定义二进制组件规格
type BinarySpec struct {
    Variables             map[string]string         `json:"variables,omitempty"`
    Artifacts             []ArtifactSpec             `json:"artifacts"`
    ConfigTemplates       []ConfigTemplateSpec       `json:"configTemplates,omitempty"`
    InstallScript         string                     `json:"installScript"`
    UninstallScript       string                     `json:"uninstallScript,omitempty"`
    SupportedArchitectures []string                  `json:"supportedArchitectures"`
    SupportedOS           []OSSpec                   `json:"supportedOS"`
    DefaultConfigPath     string                     `json:"defaultConfigPath,omitempty"`
    DefaultLogPath        string                     `json:"defaultLogPath,omitempty"`
    DefaultDataPath       string                     `json:"defaultDataPath,omitempty"`
    HealthCheck           *BinaryHealthCheckSpec     `json:"healthCheck,omitempty"`
}

// ArtifactSpec 定义二进制制品规格
type ArtifactSpec struct {
    Name        string `json:"name"`
    URL         string `json:"url"`           // 支持模板变量 {{arch}}, {{version}}
    Checksum    string `json:"checksum"`       // 格式: sha256:xxx
    InstallPath string `json:"installPath"`    // 安装路径
}

// ConfigTemplateSpec 定义配置文件模板规格
type ConfigTemplateSpec struct {
    Name       string                 `json:"name"`
    Path       string                 `json:"path,omitempty"`        // 静态路径
    PathTemplate string               `json:"pathTemplate,omitempty"` // 动态路径（与 forEach 配合）
    ForEach    string                 `json:"forEach,omitempty"`     // 迭代源路径
    Mode       string                 `json:"mode,omitempty"`        // 文件权限
    Owner      string                 `json:"owner,omitempty"`       // 文件所有者
    Content    string                 `json:"content,omitempty"`     // Go template 内容
    SecretRef  *SecretRefSpec         `json:"secretRef,omitempty"`   // Secret 引用
    KubeconfigTemplate *KubeconfigTemplateSpec `json:"kubeconfigTemplate,omitempty"`
    Condition  string                 `json:"condition,omitempty"`   // 生成条件
}

// BinaryHealthCheckSpec 定义健康检查规格
type BinaryHealthCheckSpec struct {
    Enabled  bool   `json:"enabled"`
    Timeout  string `json:"timeout,omitempty"`   // 默认 2m
    Interval string `json:"interval,omitempty"`  // 默认 5s
    Script   string `json:"script"`             // Go template, SSH 执行, 退出码 0=健康
}

// NodeFilterSpec 定义节点过滤策略
type NodeFilterSpec struct {
    Roles             []string          `json:"roles,omitempty"`
    MatchLabels       map[string]string `json:"matchLabels,omitempty"`
    SkipCompleted     *bool             `json:"skipCompleted,omitempty"`
    ExcludeAppointment *bool            `json:"excludeAppointment,omitempty"`
}
```

**模板变量系统**：

| 变量类别 | 变量示例 | 说明 |
|---------|---------|------|
| **节点信息** | `{{nodeIP}}`, `{{nodeName}}`, `{{arch}}` | 节点 IP、主机名、CPU 架构 |
| **集群配置** | `{{clusterName}}`, `{{namespace}}`, `{{kubernetesVersion}}` | 集群名称、命名空间、K8s 版本 |
| **组件版本** | `{{version}}`, `{{componentVersion}}` | 组件版本号 |
| **制品信息** | `{{artifact.<name>.path}}`, `{{artifact.<name>.installPath}}` | 制品下载路径、安装路径 |
| **配置路径** | `{{configPath}}`, `{{logPath}}`, `{{dataPath}}` | 组件级默认路径 |
| **自定义变量** | `{{.Variables.<key>}}` | ComponentVersion 中定义的变量 |
| **操作类型** | `{{isUpgrade}}` | 是否为升级操作（Install vs Upgrade） |
| **条件渲染** | `{{if .isUpgrade}}...{{end}}` | 按操作类型条件渲染 |

## 5. DAG 集成设计

### 5.1 BinaryComponentExecutor 注册

```go
// pkg/dagexec/executor/binary.go

// BinaryComponentExecutor Binary 组件执行器
type BinaryComponentExecutor struct {
    installer *binaryinstaller.BinaryInstaller
}

func (e *BinaryComponentExecutor) ExecuteComponent(
    ctx context.Context,
    node *topology.ComponentNode,
    execCtx *ExecutionContext,
) error {
    // 获取 ComponentVersion
    cv, err := execCtx.CVStore.GetComponentVersion(ctx, node.Name, node.Version)
    if err != nil {
        return err
    }
    
    // 判断操作类型（Install / Upgrade）
    action := binaryinstaller.BinaryActionInstall
    if execCtx.VersionContext.HasCurrent(node.Name) {
        action = binaryinstaller.BinaryActionUpgrade
    }
    
    // 构建安装选项
    opts := binaryinstaller.InstallOptions{
        Component:  cv,
        TemplateCtx: execCtx.TemplateContext,
        Action:     action,
    }
    
    // 执行安装/升级
    return e.installer.Install(ctx, opts)
}

func (e *BinaryComponentExecutor) GetComponentType() ComponentType {
    return ComponentTypeBinary
}
```

### 5.2 ExecutorRegistry 注册

```go
// Scheduler 初始化时注册 binary 执行器
func NewScheduler(config SchedulerConfig) *Scheduler {
    registry := NewExecutorRegistry()
    
    // 现有执行器
    registry.Register(ComponentTypeInline, NewInlineComponentExecutor(...))
    registry.Register(ComponentTypeYAML, NewYamlComponentExecutor(...))
    registry.Register(ComponentTypeHelm, NewHelmComponentExecutor(...))
    
    // 🆕新增 binary 执行器
    binaryInstaller, _ := binaryinstaller.NewBinaryInstaller(binaryinstaller.BinaryInstallerConfig{
        Client:         config.Client,
        SshExecutor:     NewMultiCliSSHAdapter(config.SshClient),
        CacheDir:        "/var/lib/openfuyao/cache/artifacts",
        Renderer:        templateRenderer,
        ConfigRenderer:  configRenderer,
        Logger:          config.Logger,
    })
    registry.Register(ComponentTypeBinary, &BinaryComponentExecutor{installer: binaryInstaller})
    
    return &Scheduler{Registry: registry, ...}
}
```

### 5.3 DAG 中的 Binary 组件节点

```
升级 DAG（含 binary 组件）:

Batch 1: [pre-upgrade-resources]
    └─ 创建升级所需的 CRD/RBAC 资源

Batch 2: [bkeagent, containerd, runc]    ← type=binary，并行执行
    ├─ bkeagent:   BinaryInstaller → SSH 推送二进制
    ├─ containerd: BinaryInstaller → 下载制品 + 渲染配置 + SSH 执行
    └─ runc:       BinaryInstaller → 下载制品 + SSH 执行

Batch 3: [kubelet, kubectl]             ← type=binary，并行执行
    ├─ kubelet: BinaryInstaller → 下载制品 + 渲染配置 + SSH 执行
    └─ kubectl: BinaryInstaller → 下载制品 + SSH 执行

Batch 4: [etcd]                         ← type=staticpod（KEP-9）
    └─ StaticPodInstaller

Batch 5: [kubernetes-master]             ← type=inline（kubeadm）
    └─ Upgrade CR → Kubeadm UpgradeControlPlane

Batch 6: [kubernetes-worker]             ← type=inline（kubeadm）
    └─ Upgrade CR → Kubeadm UpgradeWorker

Batch 7: [kube-proxy, coredns]          ← type=yaml
    └─ YamlInstaller SSA Apply
```

## 6. 组件 ComponentVersion YAML 定义

### 6.1 bkeagent ComponentVersion

```yaml
# bke-manifests/bkeagent/v2.7.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v2.7.0
spec:
  name: bkeagent
  type: binary
  version: v2.7.0

  binary:
    variables:
      listenPort: "18080"
      logLevel: "info"
      dataDir: "/var/lib/openfuyao/bkeagent"
      configDir: "/etc/openfuyao/bkeagent"

    artifacts:
      - name: bkeagent
        url: "{{imageRegistry}}/bkeagent/{{version}}/bkeagent-{{version}}-linux-{{arch}}"
        checksum: "sha256:abc123..."
        installPath: "/usr/local/bin"

    configTemplates:
      - name: bkeagent.conf
        path: "{{configDir}}/bkeagent.conf"
        mode: "0644"
        content: |
          [server]
          listen = "0.0.0.0:{{.Variables.listenPort}}"
          logLevel = "{{.Variables.logLevel}}"
          dataDir = "{{.Variables.dataDir}}"
          
          [kubernetes]
          kubeconfig = "{{configDir}}/kubeconfig"
      
      - name: kubeconfig
        path: "{{configDir}}/kubeconfig"
        mode: "0600"
        kubeconfigTemplate:
          clusterName: "{{clusterName}}"
          apiServer: "https://{{controlPlaneEndpoint}}"
          caCertPath: "/etc/kubernetes/pki/ca.crt"
          clientCertPath: "/etc/kubernetes/pki/bkeagent.crt"
          clientKeyPath: "/etc/kubernetes/pki/bkeagent.key"

    installScript: |
      #!/bin/bash
      set -e
      
      # 创建目录
      mkdir -p {{.Variables.configDir}}
      mkdir -p {{.Variables.dataDir}}
      
      # 安装二进制
      install -m 0755 {{artifact.bkeagent.installPath}}/bkeagent /usr/local/bin/bkeagent
      
      # 创建 systemd service
      cat > /etc/systemd/system/bkeagent.service << 'EOF'
      [Unit]
      Description=BKE Agent
      After=network.target
      
      [Service]
      Type=simple
      ExecStart=/usr/local/bin/bkeagent --config {{.Variables.configDir}}/bkeagent.conf
      Restart=always
      RestartSec=5
      
      [Install]
      WantedBy=multi-user.target
      EOF
      
      systemctl daemon-reload
      systemctl enable bkeagent
      
      {{if .isUpgrade}}
      systemctl restart bkeagent
      {{else}}
      systemctl start bkeagent
      {{end}}
      
    uninstallScript: |
      #!/bin/bash
      systemctl stop bkeagent || true
      systemctl disable bkeagent || true
      rm -f /usr/local/bin/bkeagent
      rm -f /etc/systemd/system/bkeagent.service
      systemctl daemon-reload

    healthCheck:
      enabled: true
      timeout: "2m"
      interval: "5s"
      script: |
        systemctl is-active bkeagent
        curl -s http://127.0.0.1:{{.Variables.listenPort}}/healthz

    defaultConfigPath: "/etc/openfuyao/bkeagent"
    defaultDataPath: "/var/lib/openfuyao/bkeagent"
    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master", "worker"]
    skipCompleted: false  # bkeagent 升级不跳过已完成节点

  dependencies: []

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

### 6.2 containerd ComponentVersion

```yaml
# bke-manifests/containerd/v1.7.24/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.24
spec:
  name: containerd
  type: binary
  version: v1.7.24

  binary:
    variables:
      configDir: "/etc/containerd"
      dataDir: "/var/lib/containerd"
      sandboxImage: "{{imageRegistry}}/pause:3.9"
      systemdCgroup: "true"
      logLevel: "info"

    artifacts:
      - name: containerd
        url: "{{imageRegistry}}/containerd/{{version}}/containerd-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:def456..."
        installPath: "/usr/local"

    configTemplates:
      - name: config.toml
        path: "{{configDir}}/config.toml"
        mode: "0644"
        content: |
          version = 2
          
          [plugins."io.containerd.grpc.v1.cri"]
            sandbox_image = "{{.Variables.sandboxImage}}"
            
            [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
              runtime_type = "io.containerd.runc.v2"
              
              [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
                SystemdCgroup = {{.Variables.systemdCgroup}}
            
            [plugins."io.containerd.grpc.v1.cri".registry]
              config_path = "{{configDir}}/certs.d"
          
          [plugins."io.containerd.grpc.v1.cri".log]
            level = "{{.Variables.logLevel}}"
      
      - name: containerd.service
        path: "/etc/systemd/system/containerd.service"
        mode: "0644"
        content: |
          [Unit]
          Description=containerd container runtime
          Documentation=https://containerd.io
          After=network.target
          
          [Service]
          ExecStartPre=/sbin/modprobe overlay
          ExecStart=/usr/local/bin/containerd
          Restart=always
          RestartSec=5
          LimitNOFILE=1048576
          LimitNPROC=infinity
          LimitCORE=infinity
          
          [Install]
          WantedBy=multi-user.target

    installScript: |
      #!/bin/bash
      set -e
      
      # 创建目录
      mkdir -p {{configDir}}
      mkdir -p {{configDir}}/certs.d
      mkdir -p {{.Variables.dataDir}}
      
      # 解压制品
      tar -xzf {{artifact.containerd.installPath}}/containerd-{{version}}-linux-{{arch}}.tar.gz -C /usr/local
      
      {{if .isUpgrade}}
      # 升级：停止旧版本
      systemctl stop containerd
      {{end}}
      
      # 启动服务
      systemctl daemon-reload
      systemctl enable containerd
      
      {{if .isUpgrade}}
      systemctl start containerd
      {{else}}
      systemctl start containerd
      {{end}}
      
    uninstallScript: |
      #!/bin/bash
      systemctl stop containerd || true
      systemctl disable containerd || true
      rm -f /usr/local/bin/containerd*
      rm -f /etc/systemd/system/containerd.service
      systemctl daemon-reload

    healthCheck:
      enabled: true
      timeout: "3m"
      interval: "5s"
      script: |
        systemctl is-active containerd
        crictl --runtime-endpoint unix:///run/containerd/containerd.sock info > /dev/null

    defaultConfigPath: "/etc/containerd"
    defaultDataPath: "/var/lib/containerd"
    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master", "worker"]
    skipCompleted: true

  dependencies:
    - name: bkeagent
      phase: Install
    - name: runc
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "15m"
    failurePolicy: FailFast
```

### 6.3 runc ComponentVersion

```yaml
# bke-manifests/runc/v1.1.12/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: runc-v1.1.12
spec:
  name: runc
  type: binary
  version: v1.1.12

  binary:
    artifacts:
      - name: runc
        url: "{{imageRegistry}}/runc/{{version}}/runc.{{arch}}"
        checksum: "sha256:ghi789..."
        installPath: "/usr/local/sbin"

    installScript: |
      #!/bin/bash
      set -e
      install -m 0755 {{artifact.runc.installPath}}/runc /usr/local/sbin/runc

    uninstallScript: |
      #!/bin/bash
      rm -f /usr/local/sbin/runc

    healthCheck:
      enabled: true
      timeout: "30s"
      interval: "5s"
      script: |
        runc --version | grep -q "{{version}}"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master", "worker"]
    skipCompleted: true

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: FailFast
```

### 6.4 kubelet ComponentVersion

```yaml
# bke-manifests/kubelet/v1.36.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubelet-v1.36.0
spec:
  name: kubelet
  type: binary
  version: v1.36.0

  binary:
    variables:
      configDir: "/etc/kubernetes"
      dataDir: "/var/lib/kubelet"
      logDir: "/var/log/kubelet"
      cgroupDriver: "systemd"
      clusterDNS: "{{dnsIP}}"
      clusterDomain: "cluster.local"
      podCIDR: "{{podSubnet}}"

    artifacts:
      - name: kubelet
        url: "{{imageRegistry}}/kubernetes/{{version}}/kubelet-{{arch}}"
        checksum: "sha256:jkl012..."
        installPath: "/usr/local/bin"

    configTemplates:
      - name: kubelet.conf
        path: "{{configDir}}/kubelet.conf"
        mode: "0644"
        content: |
          apiVersion: kubelet.config.k8s.io/v1beta1
          kind: KubeletConfiguration
          cgroupDriver: {{.Variables.cgroupDriver}}
          clusterDNS:
            - {{.Variables.clusterDNS}}
          clusterDomain: {{.Variables.clusterDomain}}
          podCIDR: {{.Variables.podCIDR}}
          authentication:
            anonymous:
              enabled: false
            webhook:
              enabled: true
          authorization:
            mode: Webhook
          serverTLSBootstrap: true
          rotateCertificates: true
      
      - name: kubelet.service
        path: "/etc/systemd/system/kubelet.service"
        mode: "0644"
        content: |
          [Unit]
          Description=kubelet: The Kubernetes Node Agent
          After=network.target containerd.service
          
          [Service]
          ExecStart=/usr/local/bin/kubelet \\
            --config={{configDir}}/kubelet.conf \\
            --kubeconfig={{configDir}}/kubelet-kubeconfig \\
            --container-runtime=remote \\
            --container-runtime-endpoint=unix:///run/containerd/containerd.sock
          Restart=always
          RestartSec=5
          
          [Install]
          WantedBy=multi-user.target

    installScript: |
      #!/bin/bash
      set -e
      
      mkdir -p {{.Variables.configDir}}
      mkdir -p {{.Variables.dataDir}}
      mkdir -p {{.Variables.logDir}}
      
      install -m 0755 {{artifact.kubelet.installPath}}/kubelet /usr/local/bin/kubelet
      
      {{if .isUpgrade}}
      systemctl stop kubelet
      {{end}}
      
      systemctl daemon-reload
      systemctl enable kubelet
      systemctl start kubelet
      
    uninstallScript: |
      #!/bin/bash
      systemctl stop kubelet || true
      systemctl disable kubelet || true
      rm -f /usr/local/bin/kubelet
      rm -f /etc/systemd/system/kubelet.service
      systemctl daemon-reload

    healthCheck:
      enabled: true
      timeout: "5m"
      interval: "5s"
      script: |
        systemctl is-active kubelet
        kubelet --version | grep -q "{{version}}"

    defaultConfigPath: "/etc/kubernetes"
    defaultDataPath: "/var/lib/kubelet"
    defaultLogPath: "/var/log/kubelet"
    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master", "worker"]
    skipCompleted: true

  dependencies:
    - name: containerd
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

### 6.5 kubectl ComponentVersion

```yaml
# bke-manifests/kubectl/v1.36.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubectl-v1.36.0
spec:
  name: kubectl
  type: binary
  version: v1.36.0

  binary:
    artifacts:
      - name: kubectl
        url: "{{imageRegistry}}/kubernetes/{{version}}/kubectl-{{arch}}"
        checksum: "sha256:mno345..."
        installPath: "/usr/local/bin"

    installScript: |
      #!/bin/bash
      set -e
      install -m 0755 {{artifact.kubectl.installPath}}/kubectl /usr/local/bin/kubectl

    uninstallScript: |
      #!/bin/bash
      rm -f /usr/local/bin/kubectl

    healthCheck:
      enabled: true
      timeout: "30s"
      interval: "5s"
      script: |
        kubectl version --client | grep -q "{{version}}"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master", "worker"]
    skipCompleted: true

  dependencies:
    - name: kubelet
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: FailFast
```

### 6.6 helm ComponentVersion

```yaml
# bke-manifests/helm/v3.14.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: helm-v3.14.0
spec:
  name: helm
  type: binary
  version: v3.14.0

  binary:
    artifacts:
      - name: helm
        url: "{{imageRegistry}}/helm/{{version}}/helm-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:pqr456..."
        installPath: "/usr/local/bin"

    installScript: |
      #!/bin/bash
      set -e
      tar -xzf {{artifact.helm.installPath}}/helm-{{version}}-linux-{{arch}}.tar.gz -C /tmp
      install -m 0755 /tmp/linux-{{arch}}/helm /usr/local/bin/helm
      rm -rf /tmp/linux-{{arch}}

    uninstallScript: |
      #!/bin/bash
      rm -f /usr/local/bin/helm

    healthCheck:
      enabled: true
      timeout: "30s"
      interval: "5s"
      script: |
        helm version | grep -q "{{version}}"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master"]
    skipCompleted: true

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Parallel
    timeout: "5m"
    failurePolicy: Continue
```

### 6.7 etcdctl ComponentVersion

```yaml
# bke-manifests/etcdctl/v3.5.20/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: etcdctl-v3.5.20
spec:
  name: etcdctl
  type: binary
  version: v3.5.20

  binary:
    artifacts:
      - name: etcdctl
        url: "{{imageRegistry}}/etcd/{{version}}/etcd-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:stu789..."
        installPath: "/usr/local/bin"

    installScript: |
      #!/bin/bash
      set -e
      tar -xzf {{artifact.etcdctl.installPath}}/etcd-{{version}}-linux-{{arch}}.tar.gz -C /tmp
      install -m 0755 /tmp/etcd-{{version}}-linux-{{arch}}/etcdctl /usr/local/bin/etcdctl
      rm -rf /tmp/etcd-{{version}}-linux-{{arch}}

    uninstallScript: |
      #!/bin/bash
      rm -f /usr/local/bin/etcdctl

    healthCheck:
      enabled: true
      timeout: "30s"
      interval: "5s"
      script: |
        etcdctl version | grep -q "{{version}}"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master"]
    skipCompleted: true

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Parallel
    timeout: "5m"
    failurePolicy: Continue
```

### 6.8 calicoctl ComponentVersion

```yaml
# bke-manifests/calicoctl/v3.27.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: calicoctl-v3.27.0
spec:
  name: calicoctl
  type: binary
  version: v3.27.0

  binary:
    artifacts:
      - name: calicoctl
        url: "{{imageRegistry}}/calico/{{version}}/calicoctl-linux-{{arch}}"
        checksum: "sha256:vwx012..."
        installPath: "/usr/local/bin"

    installScript: |
      #!/bin/bash
      set -e
      install -m 0755 {{artifact.calicoctl.installPath}}/calicoctl-linux-{{arch}} /usr/local/bin/calicoctl

    uninstallScript: |
      #!/bin/bash
      rm -f /usr/local/bin/calicoctl

    healthCheck:
      enabled: true
      timeout: "30s"
      interval: "5s"
      script: |
        calicoctl version | grep -q "{{version}}"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master", "worker"]
    skipCompleted: true

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Parallel
    timeout: "5m"
    failurePolicy: Continue
```

### 6.9 lxcfs ComponentVersion

```yaml
# bke-manifests/lxcfs/v6.0.2/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: lxcfs-v6.0.2
spec:
  name: lxcfs
  type: binary
  version: v6.0.2

  binary:
    variables:
      configDir: "/etc/lxcfs"

    artifacts:
      - name: lxcfs
        url: "{{imageRegistry}}/lxcfs/{{version}}/lxcfs-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:yza345..."
        installPath: "/usr/local"

    configTemplates:
      - name: lxcfs.service
        path: "/etc/systemd/system/lxcfs.service"
        mode: "0644"
        content: |
          [Unit]
          Description=FUSE filesystem for LXC
          After=network.target
          
          [Service]
          ExecStartPre=/bin/mkdir -p /var/lib/lxcfs
          ExecStart=/usr/local/bin/lxcfs /var/lib/lxcfs
          Restart=always
          RestartSec=5
          
          [Install]
          WantedBy=multi-user.target

    installScript: |
      #!/bin/bash
      set -e
      
      mkdir -p /var/lib/lxcfs
      
      tar -xzf {{artifact.lxcfs.installPath}}/lxcfs-{{version}}-linux-{{arch}}.tar.gz -C /usr/local
      
      {{if .isUpgrade}}
      systemctl stop lxcfs
      {{end}}
      
      systemctl daemon-reload
      systemctl enable lxcfs
      systemctl start lxcfs

    uninstallScript: |
      #!/bin/bash
      systemctl stop lxcfs || true
      systemctl disable lxcfs || true
      rm -f /usr/local/bin/lxcfs
      rm -f /etc/systemd/system/lxcfs.service
      systemctl daemon-reload

    healthCheck:
      enabled: true
      timeout: "1m"
      interval: "5s"
      script: |
        systemctl is-active lxcfs

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master", "worker"]
    skipCompleted: true

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: Continue
```

### 6.10 nfs-utils ComponentVersion

```yaml
# bke-manifests/nfs-utils/v2.6.4/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: nfs-utils-v2.6.4
spec:
  name: nfs-utils
  type: binary
  version: v2.6.4

  binary:
    variables:
      # nfs-utils 通过包管理器安装，无二进制制品
      # installScript 根据操作系统自动选择 yum/apt

    # 无 artifacts（通过包管理器安装）

    installScript: |
      #!/bin/bash
      set -e
      
      # 检测操作系统
      if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$ID
      else
        OS="centos"
      fi
      
      case "$OS" in
        centos|rhel)
          yum install -y nfs-utils
          ;;
        ubuntu|debian)
          apt-get update
          apt-get install -y nfs-common
          ;;
        *)
          echo "Unsupported OS: $OS"
          exit 1
          ;;
      esac
      
      systemctl enable nfs-server || true
      systemctl start nfs-server || true

    uninstallScript: |
      #!/bin/bash
      systemctl stop nfs-server || true
      systemctl disable nfs-server || true
      
      if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$ID
      fi
      
      case "$OS" in
        centos|rhel)
          yum remove -y nfs-utils
          ;;
        ubuntu|debian)
          apt-get remove -y nfs-common
          ;;
      esac

    healthCheck:
      enabled: true
      timeout: "1m"
      interval: "5s"
      script: |
        rpcinfo -p localhost | grep -q nfs

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

  nodeFilter:
    roles: ["master", "worker"]
    skipCompleted: true

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Parallel
    timeout: "5m"
    failurePolicy: Continue
```

> **注意**：nfs-utils 是唯一一个不通过二进制制品下载安装的组件，它通过包管理器（yum/apt）安装。installScript 中的 OS 自检测逻辑（通过 `/etc/os-release`）使得同一份 ComponentVersion 可以适配不同操作系统。

## 7. 迁移策略

### 7.1 Feature Gate 设计

```go
// pkg/featuregate/features.go

var (
    // BinaryComponentEnabled 控制 binary 类型组件执行器是否启用
    BinaryComponentEnabled = featuregate.NewFeature()
    
    // BKEAgentBinaryMigration 控制 bkeagent 从 inline 迁移到 binary
    BKEAgentBinaryMigration = featuregate.NewFeature()
    
    // ContainerdBinaryMigration 控制 containerd 从 inline 迁移到 binary
    ContainerdBinaryMigration = featuregate.NewFeature()
    
    // KubeletBinaryMigration 控制 kubelet/kubectl 从 inline 迁移到 binary
    KubeletBinaryMigration = featuregate.NewFeature()
    
    // RuncBinaryMigration 控制 runc 从 inline 迁移到 binary
    RuncBinaryMigration = featuregate.NewFeature()
)
```

### 7.2 迁移阶段

| 阶段 | 组件 | Feature Gate | 说明 |
|------|------|-------------|------|
| **Phase 1** | 框架能力 | - | 实现 BinaryComponentExecutor + 注册到 ExecutorRegistry |
| **Phase 2** | bkeagent, runc | 灰度启用 | 低风险组件先行，逻辑简单 |
| **Phase 3** | containerd, kubelet, kubectl | 灰度启用 | 核心组件，需充分测试 |
| **Phase 4** | helm, etcdctl, calicoctl, lxcfs, nfs-utils | 灰度启用 | 辅助组件，最后迁移 |

### 7.3 迁移原则

1. **渐进式迁移**：通过 Feature Gate 控制，新旧路径可并存
2. **向后兼容**：迁移期间 ReleaseImage 可同时包含 inline 和 binary 类型组件
3. **版本对齐**：迁移在 openFuyao 版本升级时进行，不在运行中切换
4. **验证清单**：每个组件迁移前需验证清单全部通过

### 7.4 迁移验证清单

| 验证项 | 说明 | 验证方法 |
|--------|------|---------|
| **制品下载** | 二进制制品可正确下载 | 测试环境验证 URL + checksum |
| **模板渲染** | installScript 和 configTemplates 渲染正确 | 单元测试 + dry-run |
| **SSH 执行** | 脚本在节点上正确执行 | 集成测试 |
| **健康检查** | 健康检查脚本正确判断 | 集成测试 |
| **幂等性** | 重复执行不产生副作用 | Reconcile 重入测试 |
| **升级兼容** | 从旧版本升级不中断服务 | 升级 E2E 测试 |
| **回滚兼容** | 降级到旧版本正常 | 回滚 E2E 测试 |

## 8. 与 Inline Phase 的对应关系

| 组件 | Inline Phase (安装) | Inline Phase (升级) | binary ComponentVersion | 迁移后执行方式 |
|------|--------------------|--------------------|------------------------|---------------|
| bkeagent | `EnsureBKEAgent` | `EnsureAgentUpgrade` | `bkeagent/v2.7.0` | BinaryInstaller SSH 推送 |
| containerd | (EnsureNodesEnv) | `EnsureContainerdUpgrade` | `containerd/v1.7.24` | BinaryInstaller 下载+渲染+SSH |
| kubelet | (MasterInit/WorkerJoin) | `EnsureMasterUpgrade`/`EnsureWorkerUpgrade` | `kubelet/v1.36.0` | BinaryInstaller 下载+渲染+SSH |
| kubectl | (MasterInit/WorkerJoin) | (MasterUpgrade/WorkerUpgrade) | `kubectl/v1.36.0` | BinaryInstaller 下载+SSH |
| runc | `EnsureNodesEnv` | (EnsureNodesEnv) | `runc/v1.1.12` | BinaryInstaller 下载+SSH |
| helm | `EnsureNodesEnv` | - | `helm/v3.x.x` | BinaryInstaller 下载+SSH |
| etcdctl | `EnsureNodesEnv` | - | `etcdctl/v3.5.x` | BinaryInstaller 下载+SSH |
| calicoctl | `EnsureNodesEnv` | - | `calicoctl/v1.x.x` | BinaryInstaller 下载+SSH |
| lxcfs | `EnsureNodesEnv` | - | `lxcfs/v4.x.x` | BinaryInstaller 下载+SSH |
| nfs-utils | `EnsureNodesEnv` | - | `nfs-utils/v2.x` | BinaryInstaller yum 安装 |

> **注意**：kubelet 的升级仍由 `EnsureMasterUpgrade`/`EnsureWorkerUpgrade` 通过 `Kubeadm UpgradeControlPlane`/`UpgradeWorker` 命令执行（kubeadm 内部处理 kubelet 二进制替换）。binary 类型的 kubelet ComponentVersion 仅用于全新安装时的二进制预下载和配置渲染，升级时 kubeadm 命令覆盖。

## 9. 工作量评估

### 9.1 开发工作量

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **BinaryComponentExecutor** | 执行器实现 + ExecutorRegistry 注册 | 3 |
| **bkeagent ComponentVersion** | installScript + configTemplates + healthCheck | 3 |
| **containerd ComponentVersion** | installScript + config.toml + service + healthCheck | 3 |
| **kubelet ComponentVersion** | installScript + kubelet.conf + service + healthCheck | 3 |
| **kubectl ComponentVersion** | installScript + healthCheck | 1 |
| **runc ComponentVersion** | installScript + healthCheck | 1 |
| **helm/etcdctl/calicoctl/lxcfs/nfs-utils** | 5 个辅助组件 ComponentVersion | 5 |
| **Feature Gate** | 灰度发布控制 | 1 |
| **SSH 适配器** | MultiCliSSHAdapter 实现 | 2 |
| **模板变量扩展** | TemplateContext 扩展（节点/制品/路径变量） | 2 |
| **小计** | - | **24 人天** |

### 9.2 测试工作量

| 测试类型 | 测试内容 | 工作量（人天） |
|---------|---------|---------------|
| **单元测试** | BinaryInstaller、TemplateRenderer、ConfigRenderer | 3 |
| **集成测试** | 各组件安装/升级/卸载 | 5 |
| **E2E 测试** | 完整升级流程（含 binary 组件） | 5 |
| **回滚测试** | 降级场景测试 | 2 |
| **兼容性测试** | amd64/arm64 + CentOS/Ubuntu | 3 |
| **小计** | - | **18 人天** |

### 9.3 总工作量汇总

| 类别 | 工作量（人天） |
|------|---------------|
| **开发** | 24 |
| **测试** | 18 |
| **总计** | **42** |

## 10. 风险与缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **制品下载失败** | 安装阻塞 | 中 | 本地缓存 + 重试机制 |
| **模板渲染错误** | 配置文件错误 | 中 | dry-run 验证 + 单元测试 |
| **SSH 执行超时** | 安装阻塞 | 低 | 超时控制 + 重试 |
| **Inline 迁移兼容性** | 新旧路径冲突 | 中 | Feature Gate 控制 + 渐进迁移 |
| **离线环境制品缺失** | 安装失败 | 中 | 制品预推送到本地仓库 + checksum 校验 |
| **kubelet 升级冲突** | binary 安装 + kubeadm 升级冲突 | 中 | kubelet binary 仅用于安装，升级由 kubeadm 处理 |

---

## 附录

### A. 参考文档

1. [声明式集群版本升级方案-支持二进制与 Helm 组件](声明式集群版本升级方案-支持二进制与Helm组件.md)
2. [KEP-5 声明式升级框架](kep5/kep5.md)
3. [KEP-9 Static Pod 类型设计](kep9-staticpod-upgrade-framework.md)
4. [KEP-10 安装组件声明式定义](kep10-install-components-declarative-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **BinaryInstaller** | 负责二进制组件下载、渲染、安装的安装器 |
| **BinaryComponentExecutor** | DAG 执行器，将 binary 组件纳入 DAG 统一调度 |
| **installScript** | 安装脚本模板，支持 Go template 语法和 50+ 模板变量 |
| **configTemplates** | 配置文件模板系统，支持 Content/Secret/Kubeconfig 三种渲染模式 |
| **Artifact** | 二进制制品，包含 URL、Checksum、安装路径 |
| **NodeFilterSpec** | 节点过滤策略，控制 binary 组件在哪些节点上执行 |
| **isUpgrade** | 模板变量，区分安装和升级场景，用于条件渲染 |

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

## 7. kubernetes-master / kubernetes-worker 组件类型分析

### 7.1 现状与问题

当前 `kubernetes-master` 和 `kubernetes-worker` 均为 `inline` 类型，通过 kubeadm 命令统一处理安装和升级：

| 组件 | 当前类型 | 当前执行方式 | 实际包含子组件 |
|------|---------|------------|-------------|
| kubernetes-master | inline | `Kubeadm UpgradeControlPlane` | kubelet(binary) + kube-apiserver(staticpod) + kube-controller-manager(staticpod) + kube-scheduler(staticpod) |
| kubernetes-worker | inline | `Kubeadm UpgradeWorker` | kubelet(binary) + kubectl(binary) |

**问题**：
1. kubeadm 是黑盒，无法对单个子组件进行独立的状态追踪和版本管理
2. kubelet 本质是 binary 类型，但被封装在 inline 中，无法使用 BinaryInstaller 的制品校验、配置模板化能力
3. apiserver/cm/scheduler 本质是 Static Pod，但被封装在 inline 中，无法使用 StaticPodInstaller 的 manifest 渲染能力
4. 无法对 kubelet 和控制面组件分别管理升级节奏

### 7.2 理论类型：composite

`kubernetes-master` 和 `kubernetes-worker` 理论上应该是 **`composite` 类型**，将不同类型的子组件打包为一个 DAG 节点统一调度：

```
kubernetes-master (type: composite)
  ├── kubelet              → type: binary（KEP-13 BinaryInstaller）
  ├── kube-apiserver       → type: staticpod（KEP-9 StaticPodInstaller）
  ├── kube-controller-manager → type: staticpod（KEP-9 StaticPodInstaller）
  ├── kube-scheduler       → type: staticpod（KEP-9 StaticPodInstaller）
  └── kubectl              → type: binary（KEP-13 BinaryInstaller）

kubernetes-worker (type: composite)
  ├── kubelet              → type: binary（KEP-13 BinaryInstaller）
  └── kubectl              → type: binary（KEP-13 BinaryInstaller）
```

### 7.3 拆解后的组件类型映射

| 子组件 | 归属 | 类型 | 安装方式 | 升级方式 | 说明 |
|--------|------|------|---------|---------|------|
| **kubelet** | master + worker | binary | BinaryInstaller 下载二进制 + 渲染 kubelet.conf + systemd service | BinaryInstaller 替换二进制 + 重启 | 替代 kubeadm 对 kubelet 的管理 |
| **kube-apiserver** | master | staticpod | StaticPodInstaller 镜像拉取 + manifest 渲染 + 写入 manifests/ | StaticPodInstaller 替换 manifest + Kubelet 自动重建 | 替代 kubeadm 对 apiserver 的管理 |
| **kube-controller-manager** | master | staticpod | 同上 | 同上 | 替代 kubeadm 对 CM 的管理 |
| **kube-scheduler** | master | staticpod | 同上 | 同上 | 替代 kubeadm 对 scheduler 的管理 |
| **kubectl** | master + worker | binary | BinaryInstaller 下载二进制 | BinaryInstaller 替换二进制 | 命令行工具，无服务管理 |

### 7.4 拆解后的 DAG 结构

#### 7.4.1 安装 DAG（composite 拆解后）

```
安装 DAG（kubernetes-master 拆解为独立子组件）:

Batch 1: [pre-upgrade-resources]
    └─ 创建升级所需的 CRD/RBAC 资源

Batch 2: [bkeagent, runc, containerd]    ← type=binary，并行执行
    ├─ bkeagent:   BinaryInstaller
    ├─ runc:       BinaryInstaller
    └─ containerd: BinaryInstaller

Batch 3: [kubelet]                       ← type=binary（master + worker）
    └─ BinaryInstaller: 下载 kubelet 二进制 + 渲染 kubelet.conf + systemd service
       ├─ Master 节点: 安装 kubelet
       └─ Worker 节点: 安装 kubelet

Batch 4: [etcd]                          ← type=staticpod（仅 master）
    └─ StaticPodInstaller: 镜像拉取 + manifest 渲染 + 写入 manifests/

Batch 5: [kube-apiserver]                ← type=staticpod（仅 master）
    └─ StaticPodInstaller: 镜像拉取 + manifest 渲染 + 写入 manifests/

Batch 6: [kube-controller-manager, kube-scheduler]  ← type=staticpod（仅 master），并行
    ├─ kube-controller-manager: StaticPodInstaller
    └─ kube-scheduler: StaticPodInstaller

Batch 7: [kubectl]                       ← type=binary（master + worker）
    └─ BinaryInstaller: 下载 kubectl 二进制

Batch 8: [kube-proxy, coredns]          ← type=yaml
    ├─ kube-proxy: YamlInstaller Apply
    └─ coredns: YamlInstaller Apply

Batch 9: [nodes-postprocess, agent-switch]  ← type=inline
    ├─ nodes-postprocess: Inline
    └─ agent-switch: Inline
```

#### 7.4.2 升级 DAG（composite 拆解后）

```
升级 DAG（按依赖顺序执行）:

Batch 1: [pre-upgrade-resources]
    └─ 创建升级所需资源

Batch 2: [bkeagent, containerd, runc]    ← type=binary，并行
    ├─ bkeagent:   SSH 推送新版本
    ├─ containerd: ENV 命令（reset + redeploy）
    └─ runc:       BinaryInstaller 替换二进制

Batch 3: [etcd]                          ← type=staticpod
    └─ StaticPodInstaller: 备份 manifest + 拉取新镜像 + 替换 manifest + 健康检查

Batch 4: [kube-apiserver]                ← type=staticpod
    └─ StaticPodInstaller: 拉取新镜像 + 替换 manifest + 健康检查

Batch 5: [kube-controller-manager, kube-scheduler]  ← type=staticpod，并行
    ├─ kube-controller-manager: StaticPodInstaller
    └─ kube-scheduler: StaticPodInstaller

Batch 6: [kubelet, kubectl]             ← type=binary，并行
    ├─ kubelet: BinaryInstaller
    │   ├─ Master 节点: drain → 替换二进制 + 配置 → 重启 → uncordon
    │   └─ Worker 节点: drain → 替换二进制 + 配置 → 重启 → uncordon
    └─ kubectl: BinaryInstaller（替换二进制，无需重启服务）

Batch 7: [kube-proxy, coredns]          ← type=yaml
    ├─ kube-proxy: YamlInstaller SSA Apply
    └─ coredns: YamlInstaller SSA Apply
```

### 7.5 拆解后的依赖关系

```
kubernetes-master 拆解后的依赖链:

bkeagent ──> runc ──> containerd ──> kubelet ──> etcd ──> kube-apiserver
                                                        ├──> kube-controller-manager
                                                        └──> kube-scheduler
                                                                  │
                                                                  └──> kubectl

kubernetes-worker 拆解后的依赖链:

bkeagent ──> runc ──> containerd ──> kubelet ──> kubectl
```

**依赖设计原则**：
1. **kubelet 依赖 containerd**：kubelet 需要 containerd 作为容器运行时
2. **etcd 依赖 kubelet**：etcd 作为 Static Pod 需要 Kubelet 拉起
3. **kube-apiserver 依赖 etcd**：apiserver 需要 etcd 作为数据存储
4. **kube-controller-manager 依赖 kube-apiserver**：CM 需要连接 apiserver
5. **kube-scheduler 依赖 kube-apiserver**：scheduler 需要连接 apiserver
6. **kubectl 依赖 kube-scheduler**：kubectl 作为 CLI 工具最后安装（无强依赖，但逻辑上最后）

### 7.6 kubelet 拆解为 binary 类型的方案

#### 7.6.1 安装方案

当前 kubeadm init/join 内部包含 kubelet 安装，拆解后由 BinaryInstaller 独立完成：

| 步骤 | 当前（kubeadm） | 拆解后（BinaryInstaller） |
|------|----------------|------------------------|
| 1. 下载 kubelet 二进制 | kubeadm 内部处理 | BinaryInstaller 通过 ArtifactSpec.URL 下载 + checksum 校验 |
| 2. 渲染 kubelet.conf | kubeadm 内部生成 | ConfigTemplateSpec.Content 渲染 Go template |
| 3. 渲染 kubelet.service | kubeadm 内部生成 | ConfigTemplateSpec.Content 渲染 |
| 4. 创建 kubelet-kubeconfig | kubeadm 内部生成 | ConfigTemplateSpec.KubeconfigTemplate 动态生成 |
| 5. 安装二进制 | kubeadm 内部处理 | installScript: `install -m 0755 ...` |
| 6. 启动 kubelet | kubeadm 内部处理 | installScript: `systemctl enable && systemctl start` |
| 7. 健康检查 | 无 | BinaryHealthCheckSpec: `systemctl is-active kubelet` |

**关键差异**：kubeadm 在 init 时还会生成 PKI 证书和 kubeconfig，这些由 `certs` 组件（独立 ComponentVersion）负责，kubelet 的 BinaryInstaller 仅消费已生成的证书文件。

#### 7.6.2 升级方案

当前 kubeadm upgrade 内部处理 kubelet 二进制替换和配置更新，拆解后由 BinaryInstaller 独立完成：

| 步骤 | 当前（kubeadm upgrade） | 拆解后（BinaryInstaller） |
|------|------------------------|------------------------|
| 1. drain 节点 | kubeadm 不处理（Phase 中 drain） | installScript 中不 drain，由 DAG 调度器在执行前 drain |
| 2. 停止 kubelet | kubeadm 内部处理 | installScript: `systemctl stop kubelet` |
| 3. 替换二进制 | kubeadm 内部处理 | installScript: `install -m 0755 ...` |
| 4. 更新配置 | kubeadm 内部处理 | ConfigTemplateSpec 重新渲染 kubelet.conf |
| 5. 启动 kubelet | kubeadm 内部处理 | installScript: `systemctl start kubelet` |
| 6. 健康检查 | kubeadm 不处理 | BinaryHealthCheckSpec: `systemctl is-active kubelet` + 版本验证 |
| 7. uncordon 节点 | kubeadm 不处理（Phase 中 uncordon） | 由 DAG 调度器在执行后 uncordon |

**kubelet 升级 installScript 示例**（区分安装和升级）：

```bash
#!/bin/bash
set -e

{{if .isUpgrade}}
# 升级：先停止旧版本
systemctl stop kubelet
{{end}}

# 安装新版本二进制
install -m 0755 {{artifact.kubelet.installPath}}/kubelet /usr/local/bin/kubelet

# 渲染并更新配置（由 ConfigRenderer 在 installScript 外完成，此处仅执行）
# kubelet.conf 和 kubelet.service 已由 ConfigRenderer 上传到节点

# 启动服务
systemctl daemon-reload
systemctl enable kubelet
systemctl start kubelet
```

#### 7.6.3 与 kubeadm 的兼容性

| 场景 | kubeadm 角色 | BinaryInstaller 角色 | 共存策略 |
|------|------------|---------------------|---------|
| **全新安装** | kubeadm init 生成 PKI + kubeconfig + 初始化控制面 | BinaryInstaller 安装 kubelet 二进制 + 配置 | Feature Gate 控制：启用 BinaryInstaller 时跳过 kubeadm 的 kubelet 安装步骤 |
| **版本升级** | kubeadm upgrade 不再处理 kubelet | BinaryInstaller 替换 kubelet 二进制 + 配置 | Feature Gate 控制：启用 BinaryInstaller 时跳过 kubeadm 的 kubelet 升级步骤 |
| **节点扩容** | kubeadm join 生成 kubeconfig + 加入集群 | BinaryInstaller 安装 kubelet 二进制 + 配置 | 同全新安装 |

**共存设计**：kubeadm 命令保留，但通过 Feature Gate 控制 kubelet 的安装/升级由谁处理：
- Feature Gate OFF：kubeadm 处理 kubelet（现有行为）
- Feature Gate ON：BinaryInstaller 处理 kubelet，kubeadm 仅处理 PKI 和 kubeconfig

### 7.7 控制面组件拆解为 staticpod 类型的方案

#### 7.7.1 安装方案

当前 kubeadm init 内部生成控制面 Static Pod manifest，拆解后由 StaticPodInstaller 独立完成：

| 步骤 | 当前（kubeadm） | 拆解后（StaticPodInstaller） |
|------|----------------|---------------------------|
| 1. 拉取镜像 | kubeadm 内部处理 | ImagePullSpec: `crictl pull` |
| 2. 渲染 manifest | kubeadm 内部生成 | ManifestTemplate: Go template 渲染 |
| 3. 写入 manifests/ | kubeadm 内部处理 | SSH 原子写入 `/etc/kubernetes/manifests/` |
| 4. 等待 Kubelet 拉起 | kubeadm 内部处理 | HealthCheck: `crictl ps --name <pod>` |
| 5. 健康检查 | kubeadm 内部处理 | PodReady: 验证 Pod Ready |

#### 7.7.2 升级方案

当前 kubeadm upgrade 内部替换 Static Pod manifest，拆解后由 StaticPodInstaller 独立完成：

| 步骤 | 当前（kubeadm upgrade） | 拆解后（StaticPodInstaller） |
|------|------------------------|---------------------------|
| 1. 备份旧 manifest | kubeadm 不处理 | BackupAndReplace: `cp manifest.yaml manifest.yaml.bak` |
| 2. 拉取新镜像 | kubeadm 内部处理 | ImagePullSpec: `crictl pull <new-image>` |
| 3. 渲染新 manifest | kubeadm 内部生成 | ManifestTemplate: 新版本 tag 渲染 |
| 4. 替换 manifest | kubeadm 内部处理 | 原子写入（write tmp + mv） |
| 5. 等待 Kubelet 重建 | kubeadm 内部处理 | HealthCheck: 等待新 Pod Running |
| 6. 健康检查 | kubeadm 不处理 | PodReady: 验证新版本 Pod Ready |
| 7. 失败回滚 | kubeadm 不处理 | 自动回滚到备份 manifest |

#### 7.7.3 升级顺序约束

控制面组件有严格的升级顺序约束：

```
etcd → kube-apiserver → kube-controller-manager → kube-scheduler
                       → kube-scheduler（与 CM 并行）
```

| 顺序 | 组件 | 前置条件 | 说明 |
|------|------|---------|------|
| 1 | etcd | kubelet 已启动 | etcd 作为数据存储，必须先升级 |
| 2 | kube-apiserver | etcd 健康 | apiserver 依赖 etcd，必须等 etcd 健康后升级 |
| 3 | kube-controller-manager | apiserver 健康 | CM 连接 apiserver |
| 4 | kube-scheduler | apiserver 健康 | scheduler 连接 apiserver，与 CM 并行 |

这些顺序约束通过 ComponentVersion.spec.dependencies 在 DAG 中表达，不需要硬编码。

### 7.8 composite 类型的 ComponentVersion YAML 定义

#### 7.8.1 kubernetes-master（composite 类型）

```yaml
# bke-manifests/kubernetes-master/v1.36.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-master-v1.36.0
spec:
  name: kubernetes-master
  type: composite
  version: v1.36.0

  composite:
    nodeFilter:
      roles: ["master"]

    subComponents:
      # kubelet: binary 类型，安装/升级二进制 + 配置
      - name: kubelet
        version: v1.36.0
        roles: ["master"]

      # kubectl: binary 类型，命令行工具
      - name: kubectl
        version: v1.36.0
        roles: ["master"]

      # kube-apiserver: staticpod 类型
      - name: kube-apiserver
        version: v1.36.0
        roles: ["master"]

      # kube-controller-manager: staticpod 类型
      - name: kube-controller-manager
        version: v1.36.0
        roles: ["master"]

      # kube-scheduler: staticpod 类型
      - name: kube-scheduler
        version: v1.36.0
        roles: ["master"]

    executionMode: Sequential  # 子组件按依赖顺序执行

  dependencies:
    - name: etcd
      phase: Install
    - name: containerd
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "30m"
    failurePolicy: FailFast
```

#### 7.8.2 kubernetes-worker（composite 类型）

```yaml
# bke-manifests/kubernetes-worker/v1.36.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-worker-v1.36.0
spec:
  name: kubernetes-worker
  type: composite
  version: v1.36.0

  composite:
    nodeFilter:
      roles: ["worker"]

    subComponents:
      # kubelet: binary 类型
      - name: kubelet
        version: v1.36.0
        roles: ["worker"]

      # kubectl: binary 类型
      - name: kubectl
        version: v1.36.0
        roles: ["worker"]

    executionMode: Sequential

  dependencies:
    - name: containerd
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "15m"
    failurePolicy: Continue
```

### 7.9 演进路线

```
阶段 1（当前）: inline 类型
  kubernetes-master = inline (kubeadm init/upgrade)
  kubernetes-worker = inline (kubeadm join/upgrade)
  kubelet 安装/升级完全由 kubeadm 处理

阶段 2（KEP-13）: kubelet 拆分为 binary
  kubernetes-master = inline (kubeadm 处理控制面 + PKI)
  kubelet = binary (BinaryInstaller 安装/升级二进制)
  kubectl = binary (BinaryInstaller 安装/升级)
  控制面组件仍由 kubeadm 处理

阶段 3（KEP-9）: 控制面组件拆分为 staticpod
  kube-apiserver = staticpod (StaticPodInstaller)
  kube-controller-manager = staticpod (StaticPodInstaller)
  kube-scheduler = staticpod (StaticPodInstaller)
  kubelet = binary
  kubectl = binary
  kubeadm 仅处理 PKI 生成和初始化引导

阶段 4（最终）: composite 类型
  kubernetes-master = composite (包含 kubelet + kubectl + apiserver + cm + scheduler)
  kubernetes-worker = composite (包含 kubelet + kubectl)
  kubeadm 的编排逻辑完全由 DAG 替代
  kubeadm 仅作为节点引导工具（生成证书 + kubeconfig）
```

### 7.10 阶段 2 详细设计：kubelet 从 inline 拆分为 binary

阶段 2 是当前最现实的改造目标，将 kubelet 从 kubeadm 的黑盒中拆出：

#### 7.10.1 Feature Gate

```go
var (
    // KubeletBinaryMigration 控制 kubelet 从 kubeadm 拆分为独立 binary 组件
    // OFF: kubelet 由 kubeadm init/upgrade 处理（现有行为）
    // ON:  kubelet 由 BinaryInstaller 独立安装/升级
    KubeletBinaryMigration = featuregate.NewFeature()
)
```

#### 7.10.2 安装流程变更

**Feature Gate OFF（现有行为）**：

```
EnsureMasterInit.Execute()
  └─ kubeadm init
     ├─ 生成 PKI 证书
     ├─ 生成 kubeconfig
     ├─ 安装 kubelet 二进制 ← kubeadm 内部
     ├─ 生成 kubelet.conf ← kubeadm 内部
     ├─ 生成 apiserver manifest ← kubeadm 内部
     └─ 启动控制面
```

**Feature Gate ON（拆解后）**：

```
DAG 执行:
  Batch N: [kubelet] (type=binary)
    └─ BinaryInstaller.Install()
       ├─ 下载 kubelet 二进制（checksum 校验）
       ├─ 渲染 kubelet.conf（ConfigTemplateSpec）
       ├─ 渲染 kubelet.service（ConfigTemplateSpec）
       ├─ SSH 执行 installScript（install + systemctl enable + start）
       └─ 健康检查（systemctl is-active kubelet）

  Batch N+1: [kubernetes-master] (type=inline)
    └─ EnsureMasterInit.Execute()
       ├─ kubeadm init --skip-kubelet  ← 跳过 kubelet 安装
       ├─ 生成 PKI 证书
       ├─ 生成 kubeconfig
       ├─ 生成 apiserver manifest
       └─ 启动控制面
```

**关键设计**：kubeadm init 增加 `--skip-kubelet` 标志（或通过 config 跳过），不再安装 kubelet。kubelet 已由 BinaryInstaller 在前面的 Batch 中安装完成。

#### 7.10.3 升级流程变更

**Feature Gate OFF（现有行为）**：

```
EnsureMasterUpgrade.Execute()
  └─ Kubeadm UpgradeControlPlane
     ├─ 替换 kubelet 二进制 ← kubeadm 内部
     ├─ 更新 kubelet.conf ← kubeadm 内部
     ├─ 替换 apiserver manifest ← kubeadm 内部
     └─ 重启控制面
```

**Feature Gate ON（拆解后）**：

```
DAG 执行:
  Batch N: [kubelet] (type=binary)
    └─ BinaryInstaller.Install() (action=Upgrade)
       ├─ drain 节点
       ├─ 下载新版本 kubelet 二进制
       ├─ 重新渲染 kubelet.conf
       ├─ SSH 执行: stop → replace → start
       ├─ 健康检查（版本验证 + Node Ready）
       └─ uncordon 节点

  Batch N+1: [kubernetes-master] (type=inline)
    └─ EnsureMasterUpgrade.Execute()
       └─ Kubeadm UpgradeControlPlane --skip-kubelet
          ├─ 替换 apiserver manifest ← kubeadm 内部
          └─ 重启控制面
```

#### 7.10.4 阶段 2 的 DAG 结构

```
升级 DAG（阶段 2：kubelet 已拆分为 binary）:

Batch 1: [pre-upgrade-resources]
Batch 2: [bkeagent, containerd, runc]    ← type=binary
Batch 3: [etcd]                          ← type=inline（kubeadm UpgradeEtcd，待阶段 3 拆分）
Batch 4: [kubelet]                       ← type=binary 🆕（从 kubernetes-master 拆出）
    ├─ Master 节点: drain → 替换二进制 → 健康检查 → uncordon
    └─ Worker 节点: drain → 替换二进制 → 健康检查 → uncordon
Batch 5: [kubernetes-master]             ← type=inline（kubeadm --skip-kubelet）
    └─ 仅升级控制面 Static Pod（apiserver/cm/scheduler）
Batch 6: [kubernetes-worker]             ← type=inline（kubeadm --skip-kubelet）
    └─ 仅验证 worker 节点（kubelet 已在 Batch 4 升级）
Batch 7: [kubectl]                       ← type=binary 🆕
Batch 8: [kube-proxy, coredns]            ← type=yaml
```

**注意**：阶段 2 中 etcd 仍由 kubeadm 处理（inline），待阶段 3 拆分为 staticpod。kubectl 从 kubernetes-master/worker 中拆出为独立 binary 组件。

### 7.11 阶段 2 工作量评估

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **kubeadm --skip-kubelet 适配** | 验证 kubeadm 是否支持跳过 kubelet 安装，如不支持则通过 config 或 patch 实现 | 3 |
| **kubelet ComponentVersion 完善** | 在 KEP-13 的 kubelet YAML 基础上完善 installScript（区分 install/upgrade） | 2 |
| **kubelet drain/uncordon 集成** | BinaryInstaller 执行前 drain、执行后 uncordon 的集成逻辑 | 2 |
| **kubectl ComponentVersion 完善** | 在 KEP-13 的 kubectl YAML 基础上完善 | 1 |
| **EnsureMasterUpgrade 适配** | 增加 --skip-kubelet 逻辑，Feature Gate 控制 | 2 |
| **EnsureWorkerUpgrade 适配** | 增加 --skip-kubelet 逻辑，Feature Gate 控制 | 2 |
| **DAG 依赖调整** | kubelet 从 kubernetes-master 依赖中拆出，调整依赖关系 | 1 |
| **Feature Gate** | KubeletBinaryMigration 实现 + 灰度验证 | 1 |
| **集成测试** | kubelet 安装/升级/回滚 E2E 测试 | 5 |
| **小计** | - | **19 人天** |

## 8. 迁移策略

### 8.1 Feature Gate 设计

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

### 8.2 迁移阶段

| 阶段 | 组件 | Feature Gate | 说明 |
|------|------|-------------|------|
| **Phase 1** | 框架能力 | - | 实现 BinaryComponentExecutor + 注册到 ExecutorRegistry |
| **Phase 2** | bkeagent, runc | 灰度启用 | 低风险组件先行，逻辑简单 |
| **Phase 3** | containerd, kubelet, kubectl | 灰度启用 | 核心组件，需充分测试 |
| **Phase 4** | helm, etcdctl, calicoctl, lxcfs, nfs-utils | 灰度启用 | 辅助组件，最后迁移 |

### 8.3 迁移原则

1. **渐进式迁移**：通过 Feature Gate 控制，新旧路径可并存
2. **向后兼容**：迁移期间 ReleaseImage 可同时包含 inline 和 binary 类型组件
3. **版本对齐**：迁移在 openFuyao 版本升级时进行，不在运行中切换
4. **验证清单**：每个组件迁移前需验证清单全部通过

### 8.4 迁移验证清单

| 验证项 | 说明 | 验证方法 |
|--------|------|---------|
| **制品下载** | 二进制制品可正确下载 | 测试环境验证 URL + checksum |
| **模板渲染** | installScript 和 configTemplates 渲染正确 | 单元测试 + dry-run |
| **SSH 执行** | 脚本在节点上正确执行 | 集成测试 |
| **健康检查** | 健康检查脚本正确判断 | 集成测试 |
| **幂等性** | 重复执行不产生副作用 | Reconcile 重入测试 |
| **升级兼容** | 从旧版本升级不中断服务 | 升级 E2E 测试 |
| **回滚兼容** | 降级到旧版本正常 | 回滚 E2E 测试 |

## 9. 与 Inline Phase 的对应关系

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

## 10. 工作量评估

### 10.1 开发工作量

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

### 10.2 测试工作量

| 测试类型 | 测试内容 | 工作量（人天） |
|---------|---------|---------------|
| **单元测试** | BinaryInstaller、TemplateRenderer、ConfigRenderer | 3 |
| **集成测试** | 各组件安装/升级/卸载 | 5 |
| **E2E 测试** | 完整升级流程（含 binary 组件） | 5 |
| **回滚测试** | 降级场景测试 | 2 |
| **兼容性测试** | amd64/arm64 + CentOS/Ubuntu | 3 |
| **小计** | - | **18 人天** |

### 10.3 总工作量汇总

| 类别 | 工作量（人天） |
|------|---------------|
| **开发** | 24 |
| **测试** | 18 |
| **总计** | **42** |

## 11. 风险与缓解措施

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

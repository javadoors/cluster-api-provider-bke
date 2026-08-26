# KEP-9: Static Pod 类型组件升级框架支持设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-9 |
| **标题** | Static Pod 类型组件升级框架支持设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-25 |
| **依赖** | KEP-5 声明式升级框架、KEP-6 三层状态机设计、staticpod-type-design.md |

---

## 1. 摘要

本提案在 openFuyao 声明式升级框架中新增 `staticpod` 组件类型，用于管理通过 Static Pod 方式部署的 Kubernetes 控制面组件和高可用组件。当前框架已支持 `inline`、`yaml`、`helm` 三种组件类型，但 etcd、kube-apiserver、kube-controller-manager、kube-scheduler、haproxy、keepalived 等 Static Pod 组件仍通过 `inline` Phase 硬编码在 BKECluster Controller 中执行，无法享受声明式组件管理的优势。本提案设计 `StaticPodInstaller` 和 `StaticPodComponentExecutor`，将 Static Pod 组件纳入 DAG 统一调度，实现镜像预拉取、manifest 渲染、原子写入、健康检查的声明式管理。

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状 | 影响 |
|------|------|------|
| **硬编码执行** | etcd/apiserver 等通过 `EnsureEtcdUpgrade`/`EnsureMasterUpgrade` Inline Phase 执行 | 无法通过 ComponentVersion 声明式管理 |
| **升级逻辑分散** | Static Pod 升级逻辑散落在各 Phase 文件中 | 重复代码，维护困难 |
| **缺乏通用安装器** | 无统一的镜像拉取 + manifest 渲染 + 健康检查框架 | 新增 Static Pod 组件需侵入核心控制器 |
| **版本追踪缺失** | Static Pod 组件版本未纳入 `ClusterComponentStatuses` 追踪 | 无法统一查询组件状态 |

### 2.2 组件类型分类矩阵

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          组件类型分类矩阵                                        │
└─────────────────────────────────────────────────────────────────────────────────┘

                    ┌─────────────────────────────────────────────────────────┐
                    │              部署目标                                     │
                    │   节点级 (SSH)          集群级 (K8s API)                  │
          ┌─────────┼─────────────────────┬───────────────────────────────────┤
          │ 二进制   │ binary              │ (不适用)                          │
  制品    │         │ containerd          │                                   │
  类型    │         │ bkeagent            │                                   │
          │         │ kubelet/kubectl     │                                   │
          ├─────────┼─────────────────────┼───────────────────────────────────┤
          │ OCI镜像  │ staticpod 🆕        │ helm / yaml                       │
          │         │ etcd                │ coredns                           │
          │         │ apiserver/cm/sched  │ calico                            │
          │         │ haproxy/keepalived  │ monitoring                        │
          └─────────┴─────────────────────┴───────────────────────────────────┘
```

### 2.3 为什么不用现有类型

| 维度 | binary 类型 | yaml 类型 | Static Pod 组件 | 结论 |
|------|------------|----------|----------------|------|
| **制品类型** | 二进制文件 | YAML 清单 | OCI 镜像 | ❌ 不匹配 binary |
| **部署目标** | 节点级 (SSH) | 集群级 (K8s API) | 节点级 (`/etc/kubernetes/manifests/`) | ❌ 不匹配 yaml |
| **服务管理** | systemd | K8s Controller | Kubelet 本地文件监控 | ❌ 不匹配 |
| **镜像预拉取** | 不涉及 | 不涉及 | 需要 crictl pull | ❌ 不匹配 |
| **升级方式** | 替换二进制 | Apply 清单 | 替换 manifest YAML → Kubelet 自动重建 | ❌ 不匹配 |

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| **CRD 扩展** | ComponentVersion 新增 `staticpod` 类型字段定义 |
| **核心安装器** | StaticPodInstaller（镜像预拉取 + manifest 渲染 + SSH 写入 + 健康检查） |
| **DAG 集成** | StaticPodComponentExecutor 注册到 ExecutorRegistry |
| **组件迁移** | 从 Inline Phase 迁移到 staticpod 类型 ComponentVersion |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **SSH 执行** | Static Pod 组件通过 SSH 写入节点文件系统 |
| **Kubelet 管理** | Pod 生命周期由 Kubelet 本地文件监控管理，非 K8s API |
| **原子写入** | manifest 文件必须原子写入（write tmp + mv），避免半截文件 |
| **滚动升级** | 逐节点滚动，保持集群可用（etcd/apiserver 需保持多数派） |
| **幂等性** | 所有操作必须幂等，支持 Reconcile 重入 |

### 3.3 待改造组件清单

| 组件名称 | 当前 Phase | 镜像 | 适用角色 | 改造优先级 | 说明 |
|---------|-----------|------|---------|-----------|------|
| **etcd** | `EnsureEtcdUpgrade` | etcd 镜像 | master | P0 | 分布式 KV 存储 |
| **kube-apiserver** | `EnsureMasterUpgrade` | kube-apiserver 镜像 | master | P0 | K8s API Server |
| **kube-controller-manager** | `EnsureMasterUpgrade` | kube-controller-manager 镜像 | master | P0 | K8s 控制器管理器 |
| **kube-scheduler** | `EnsureMasterUpgrade` | kube-scheduler 镜像 | master | P0 | K8s 调度器 |
| **haproxy** | `EnsureLoadBalance` | haproxy 镜像 | master | P1 | 负载均衡器 |
| **keepalived** | `EnsureLoadBalance` | keepalived 镜像 | master | P1 | VIP 管理 |

---

## 4. StaticPodSpec 类型定义

### 4.1 Go 类型定义

```go
// api/v1alpha1/componentversion_types.go

// StaticPodSpec 定义 Static Pod 组件规格 🆕新增
type StaticPodSpec struct {
    // 自定义变量 (可覆盖默认值)
    Variables map[string]string `json:"variables,omitempty"`
    
    // OCI 镜像配置
    Image StaticPodImageSpec `json:"image"`
    
    // Static Pod YAML 模板 (Go template 语法)
    // 渲染后写入 ManifestPath 指定的路径
    ManifestTemplate string `json:"manifestTemplate"`
    
    // Manifest 文件目标路径 (节点上的绝对路径)
    // 默认: "/etc/kubernetes/manifests/<component-name>.yaml"
    ManifestPath string `json:"manifestPath,omitempty"`
    
    // 镜像预拉取配置
    ImagePull ImagePullSpec `json:"imagePull,omitempty"`
    
    // 前置安装脚本 (镜像拉取后、YAML 写入前执行)
    PreInstallScripts []string `json:"preInstallScripts,omitempty"`
    
    // 后置安装脚本 (YAML 写入后执行)
    PostInstallScripts []string `json:"postInstallScripts,omitempty"`
    
    // 卸载脚本 (删除 YAML 文件后执行)
    UninstallScript string `json:"uninstallScript,omitempty"`
    
    // 数据目录 (组件持久化数据路径)
    DataDir string `json:"dataDir,omitempty"`
    
    // 配置文件目录 (组件配置路径)
    ConfigDir string `json:"configDir,omitempty"`
    
    // 日志目录 (组件日志路径)
    LogDir string `json:"logDir,omitempty"`
    
    // 支持的架构列表
    SupportedArchitectures []string `json:"supportedArchitectures"`
    
    // 支持的操作系统列表
    SupportedOS []OSSpec `json:"supportedOS"`
    
    // 健康检查配置
    HealthCheck *StaticPodHealthCheckSpec `json:"healthCheck,omitempty"`
    
    // 升级策略 (Static Pod 特有)
    UpgradeStrategy *StaticPodUpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
}

// StaticPodImageSpec 定义 Static Pod 镜像配置
type StaticPodImageSpec struct {
    // 镜像地址 (支持模板变量)
    Repository string `json:"repository"`
    
    // 镜像 Tag (支持模板变量)
    Tag string `json:"tag"`
    
    // 镜像拉取策略: Always / IfNotPresent / Never
    PullPolicy string `json:"pullPolicy,omitempty"`
    
    // 镜像校验和 (可选, 用于离线环境验证)
    Checksum string `json:"checksum,omitempty"`
}

// ImagePullSpec 定义镜像预拉取配置
type ImagePullSpec struct {
    // 是否启用镜像预拉取
    Enabled *bool `json:"enabled,omitempty"`
    
    // 拉取超时时间
    Timeout string `json:"timeout,omitempty"`
    
    // 拉取重试次数
    RetryCount int `json:"retryCount,omitempty"`
    
    // 容器运行时: containerd / docker
    Runtime string `json:"runtime,omitempty"`
}

// StaticPodHealthCheckSpec 定义 Static Pod 健康检查规格
type StaticPodHealthCheckSpec struct {
    // 是否启用健康检查
    Enabled bool `json:"enabled"`
    
    // 等待超时时间 (默认 3m)
    Timeout string `json:"timeout,omitempty"`
    
    // 重试间隔 (默认 5s)
    Interval string `json:"interval,omitempty"`
    
    // 健康检查脚本 (Go template, 通过 SSH 执行)
    // 退出码 0 = 健康, 非零 = 不健康
    Script string `json:"script,omitempty"`
    
    // Pod 就绪检查 (可选, 更精确的检查方式)
    PodReady *StaticPodReadyCheckSpec `json:"podReady,omitempty"`
}

// StaticPodReadyCheckSpec 定义 Pod 就绪检查
type StaticPodReadyCheckSpec struct {
    PodName       string `json:"podName"`
    Namespace     string `json:"namespace,omitempty"`
    ContainerName string `json:"containerName,omitempty"`
    MinReady      int    `json:"minReady,omitempty"`
}

// StaticPodUpgradeStrategySpec 定义 Static Pod 升级策略
type StaticPodUpgradeStrategySpec struct {
    // 升级模式: Replace / BackupAndReplace
    Mode string `json:"mode,omitempty"`
    
    // 备份保留数量 (BackupAndReplace 模式下)
    BackupCount int `json:"backupCount,omitempty"`
    
    // Pod 终止宽限期 (秒)
    TerminationGracePeriod int `json:"terminationGracePeriod,omitempty"`
}
```

### 4.2 ComponentVersionSpec 扩展

```go
// ComponentVersionSpec 扩展
type ComponentVersionSpec struct {
    // ... 现有字段 ...
    
    // StaticPod 类型配置 (type=staticpod 时必填) 🆕新增
    StaticPod *StaticPodSpec `json:"staticPod,omitempty"`
}

const (
    // ... 现有类型 ...
    ComponentTypeStaticPod ComponentType = "staticpod" // 🆕新增
)
```

### 4.3 ComponentVersion YAML 示例（etcd）

```yaml
# bke-manifests/etcd/v3.5.20/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.20
spec:
  name: etcd
  type: staticpod
  version: v3.5.20

  staticPod:
    variables:
      dataDir: "/var/lib/openFuyao/etcd"
      peerPort: "2380"
      clientPort: "2379"
      metricsPort: "2381"

    image:
      repository: "{{imageRegistry}}/kubernetes/etcd"
      tag: "{{etcdVersion}}"
      pullPolicy: IfNotPresent

    manifestPath: "/etc/kubernetes/manifests/etcd.yaml"
    
    manifestTemplate: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: etcd
        namespace: kube-system
        labels:
          component: etcd
          tier: control-plane
      spec:
        hostNetwork: true
        priorityClassName: system-node-critical
        containers:
          - name: etcd
            image: {{.StaticPod.Image.Repository}}:{{.StaticPod.Image.Tag}}
            command:
              - etcd
              - --data-dir={{.Variables.dataDir}}
              - --advertise-client-urls=https://{{nodeIP}}:{{.Variables.clientPort}}
              - --cert-file=/etc/kubernetes/pki/etcd/server.crt
              - --client-cert-auth=true
              - --initial-cluster={{etcdInitialCluster}}
              - --listen-client-urls=https://{{nodeIP}}:{{.Variables.clientPort}},https://127.0.0.1:{{.Variables.clientPort}}
              - --listen-peer-urls=https://{{nodeIP}}:{{.Variables.peerPort}}
              - --name={{nodeName}}
              - --peer-cert-file=/etc/kubernetes/pki/etcd/peer.crt
              - --peer-client-cert-auth=true
              - --peer-key-file=/etc/kubernetes/pki/etcd/peer.key
              - --peer-trusted-ca-file=/etc/kubernetes/pki/etcd/ca.crt
              - --trusted-ca-file=/etc/kubernetes/pki/etcd/ca.crt
            volumeMounts:
              - name: etcd-data
                mountPath: {{.Variables.dataDir}}
              - name: etcd-certs
                mountPath: /etc/kubernetes/pki/etcd
                readOnly: true
            livenessProbe:
              httpGet:
                path: /health?serializable=true
                port: {{.Variables.metricsPort}}
                host: 127.0.0.1
              initialDelaySeconds: 10
              periodSeconds: 10
        volumes:
          - name: etcd-data
            hostPath:
              path: {{.Variables.dataDir}}
              type: DirectoryOrCreate
          - name: etcd-certs
            hostPath:
              path: /etc/kubernetes/pki/etcd
              type: DirectoryOrCreate

    preInstallScripts:
      - |
        useradd -r -s /sbin/nologin etcd 2>/dev/null || true
        mkdir -p {{.Variables.dataDir}}
        chown etcd:etcd {{.Variables.dataDir}}
        chmod 700 {{.Variables.dataDir}}

    dataDir: "/var/lib/openFuyao/etcd"
    configDir: "/etc/kubernetes/pki/etcd"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

    healthCheck:
      enabled: true
      timeout: "3m"
      interval: "5s"
      podReady:
        podName: "etcd"
        namespace: "kube-system"
        minReady: 1

    upgradeStrategy:
      mode: BackupAndReplace
      backupCount: 3
      terminationGracePeriod: 30

  nodeFilter:
    roles: ["master"]

  dependencies:
    - name: certs
      phase: Install
    - name: kubelet
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

---

## 5. StaticPodInstaller 详细设计

### 5.1 核心架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          StaticPodInstaller 架构                                 │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  StaticPodInstaller                                                             │
│  ┌────────────────┐  ┌────────────────┐  ┌─────────────────┐                    │
│  │   Image        │  │   Manifest     │  │   SSH           │                    │
│  │   Puller       │  │   Renderer     │  │   Writer        │                    │
│  │                │  │                │  │                 │                    │
│  │  crictl pull   │  │  Go template   │  │  原子写入       │                    │
│  │  docker pull   │  │  渲染 YAML     │  │  manifest 文件  │                    │
│  └────────┬───────┘  └────────┬───────┘  └────────┬────────┘                    │
│           │                   │                   │                              │
│           └───────────────────┼───────────────────┘                              │
│                               ▼                                                │
│                      ┌────────────────┐                                         │
│                      │ HealthChecker  │                                         │
│                      │                │                                         │
│                      │ crictl ps      │                                         │
│                      │ kubectl get pod│                                         │
│                      └────────────────┘                                         │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 安装流程

```
    ┌──────────────────────────────┐
    │  1. 加载 ComponentVersion    │
    │  解析 StaticPodSpec          │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  2. 渲染模板变量              │
    │  - image.repository/tag      │
    │  - variables.*               │
    │  - TemplateContext.*         │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  3. 镜像预拉取 (可选)         │
    │  crictl pull / docker pull   │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  4. 执行前置脚本 (可选)       │
    │  创建用户/目录/权限           │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  5. 渲染 Manifest YAML       │
    │  Go template 渲染            │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  6. 原子写入 Manifest 文件   │
    │  write tmp + mv (原子操作)   │
    │  → /etc/kubernetes/manifests/│
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  7. 等待 Kubelet 拉起 Pod    │
    │  Kubelet 检测文件变化         │
    │  自动创建 Pod                │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  8. 执行后置脚本 (可选)       │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  9. 健康检查                  │
    │  crictl ps --name <pod>      │
    │  验证 Running 状态            │
    └──────────────────────────────┘
```

### 5.3 升级流程（BackupAndReplace 模式）

```
    ┌──────────────────────────────┐
    │  1. 备份当前 Manifest        │
    │  cp <manifest>.yaml          │
    │     <manifest>.yaml.bak.<ts> │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  2. 拉取新版本镜像            │
    │  crictl pull <new-image>     │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  3. 渲染新 Manifest          │
    │  使用新版本 tag 渲染模板      │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  4. 原子替换 Manifest 文件   │
    │  write tmp + mv (原子操作)   │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  5. 等待 Kubelet 重建 Pod    │
    │  Kubelet 检测文件变化         │
    │  终止旧 Pod, 创建新 Pod      │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  6. 健康检查                  │
    │  ┌────────────────────────┐  │
    │  │ 成功 → 清理旧备份      │  │
    │  │ 失败 → 回滚到备份版本  │  │
    │  └────────────────────────┘  │
    └──────────────────────────────┘
```

### 5.4 卸载流程

```
    ┌──────────────────────────────┐
    │  1. 删除 Manifest 文件       │
    │  rm /etc/kubernetes/         │
    │  manifests/<name>.yaml       │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  2. 等待 Kubelet 终止 Pod    │
    │  Kubelet 检测文件删除         │
    │  自动终止 Pod                │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  3. 执行卸载脚本 (可选)       │
    │  清理数据目录/用户/证书       │
    └──────────────────────────────┘
```

### 5.5 核心接口定义

```go
// pkg/installer/staticpod/installer.go

// StaticPodInstaller Static Pod 组件安装器
type StaticPodInstaller struct {
    sshClient      bkessh.Client
    renderer       *TemplateRenderer
    imagePuller    *ImagePuller
    healthChecker  *StaticPodHealthChecker
}

// Install 安装 Static Pod 组件
func (i *StaticPodInstaller) Install(ctx context.Context, execCtx *ExecutionContext) error {
    spec := execCtx.ComponentVersion.Spec.StaticPod
    
    // 1. 渲染镜像地址
    imageRef, err := i.renderer.RenderImageRef(spec.Image, execCtx)
    if err != nil {
        return fmt.Errorf("render image ref: %w", err)
    }
    
    // 2. 镜像预拉取
    if spec.ImagePull.Enabled == nil || *spec.ImagePull.Enabled {
        if err := i.imagePuller.Pull(ctx, execCtx.Node, imageRef, spec.ImagePull); err != nil {
            return fmt.Errorf("pull image: %w", err)
        }
    }
    
    // 3. 执行前置脚本
    for _, script := range spec.PreInstallScripts {
        rendered, err := i.renderer.Render(script, execCtx)
        if err != nil {
            return fmt.Errorf("render pre-install script: %w", err)
        }
        if err := i.sshClient.RunScript(execCtx.Node, rendered); err != nil {
            return fmt.Errorf("run pre-install script: %w", err)
        }
    }
    
    // 4. 渲染 Manifest
    manifest, err := i.renderer.Render(spec.ManifestTemplate, execCtx)
    if err != nil {
        return fmt.Errorf("render manifest: %w", err)
    }
    
    // 5. 原子写入 Manifest 文件
    manifestPath := spec.ManifestPath
    if manifestPath == "" {
        manifestPath = fmt.Sprintf("/etc/kubernetes/manifests/%s.yaml", execCtx.ComponentVersion.Spec.Name)
    }
    if err := i.sshClient.AtomicWriteFile(execCtx.Node, manifestPath, manifest, 0644); err != nil {
        return fmt.Errorf("write manifest: %w", err)
    }
    
    // 6. 执行后置脚本
    for _, script := range spec.PostInstallScripts {
        rendered, err := i.renderer.Render(script, execCtx)
        if err != nil {
            return fmt.Errorf("render post-install script: %w", err)
        }
        if err := i.sshClient.RunScript(execCtx.Node, rendered); err != nil {
            return fmt.Errorf("run post-install script: %w", err)
        }
    }
    
    // 7. 健康检查
    if spec.HealthCheck != nil && spec.HealthCheck.Enabled {
        if err := i.healthChecker.Wait(ctx, execCtx.Node, spec.HealthCheck, execCtx); err != nil {
            return fmt.Errorf("health check: %w", err)
        }
    }
    
    return nil
}

// Upgrade 升级 Static Pod 组件
func (i *StaticPodInstaller) Upgrade(ctx context.Context, execCtx *ExecutionContext) error {
    spec := execCtx.ComponentVersion.Spec.StaticPod
    
    strategy := spec.UpgradeStrategy
    if strategy == nil {
        strategy = &StaticPodUpgradeStrategySpec{Mode: "Replace"}
    }
    
    manifestPath := spec.ManifestPath
    if manifestPath == "" {
        manifestPath = fmt.Sprintf("/etc/kubernetes/manifests/%s.yaml", execCtx.ComponentVersion.Spec.Name)
    }
    
    // BackupAndReplace 模式: 先备份
    if strategy.Mode == "BackupAndReplace" {
        backupPath := fmt.Sprintf("%s.bak.%d", manifestPath, time.Now().Unix())
        if err := i.sshClient.RunScript(execCtx.Node, fmt.Sprintf("cp %s %s", manifestPath, backupPath)); err != nil {
            return fmt.Errorf("backup manifest: %w", err)
        }
        // 升级失败时回滚
        defer func() {
            if execCtx.UpgradeFailed {
                i.sshClient.RunScript(execCtx.Node, fmt.Sprintf("mv %s %s", backupPath, manifestPath))
            }
        }()
    }
    
    // 执行安装流程 (拉取新镜像 + 渲染新 YAML + 原子替换文件)
    return i.Install(ctx, execCtx)
}

// Uninstall 卸载 Static Pod 组件
func (i *StaticPodInstaller) Uninstall(ctx context.Context, execCtx *ExecutionContext) error {
    spec := execCtx.ComponentVersion.Spec.StaticPod
    
    manifestPath := spec.ManifestPath
    if manifestPath == "" {
        manifestPath = fmt.Sprintf("/etc/kubernetes/manifests/%s.yaml", execCtx.ComponentVersion.Spec.Name)
    }
    
    // 1. 删除 Manifest 文件
    if err := i.sshClient.RunScript(execCtx.Node, fmt.Sprintf("rm -f %s", manifestPath)); err != nil {
        return fmt.Errorf("remove manifest: %w", err)
    }
    
    // 2. 执行卸载脚本
    if spec.UninstallScript != "" {
        rendered, err := i.renderer.Render(spec.UninstallScript, execCtx)
        if err != nil {
            return fmt.Errorf("render uninstall script: %w", err)
        }
        if err := i.sshClient.RunScript(execCtx.Node, rendered); err != nil {
            return fmt.Errorf("run uninstall script: %w", err)
        }
    }
    
    return nil
}
```

---

## 6. DAG 集成设计

### 6.1 StaticPodComponentExecutor 注册

```go
// pkg/dagexec/executor/staticpod.go

// StaticPodComponentExecutor Static Pod 组件执行器
type StaticPodComponentExecutor struct {
    installer *staticpod.StaticPodInstaller
}

// ExecuteComponent 执行 Static Pod 组件安装/升级
func (e *StaticPodComponentExecutor) ExecuteComponent(
    ctx context.Context,
    node *topology.ComponentNode,
    execCtx *ExecutionContext,
) error {
    switch execCtx.Action {
    case ActionInstall:
        return e.installer.Install(ctx, execCtx)
    case ActionUpgrade:
        return e.installer.Upgrade(ctx, execCtx)
    case ActionUninstall:
        return e.installer.Uninstall(ctx, execCtx)
    default:
        return fmt.Errorf("unknown action: %s", execCtx.Action)
    }
}

// GetComponentType 返回组件类型
func (e *StaticPodComponentExecutor) GetComponentType() ComponentType {
    return ComponentTypeStaticPod
}

// Register 注册到 ExecutorRegistry
func init() {
    // 注册 staticpod 类型执行器
    // 在 Scheduler 初始化时调用
}
```

### 6.2 ExecutorRegistry 扩展

```go
// pkg/dagexec/registry.go

// Scheduler 初始化时注册 staticpod 执行器
func NewScheduler(config SchedulerConfig) *Scheduler {
    registry := NewExecutorRegistry()
    
    // 现有执行器
    registry.Register(ComponentTypeInline, NewInlineComponentExecutor(...))
    registry.Register(ComponentTypeYAML, NewYamlComponentExecutor(...))
    registry.Register(ComponentTypeHelm, NewHelmComponentExecutor(...))
    
    // 🆕 新增 staticpod 执行器
    registry.Register(ComponentTypeStaticPod, NewStaticPodComponentExecutor(...))
    
    return &Scheduler{
        Registry: registry,
        ...
    }
}
```

### 6.3 Scheduler 分发逻辑

```go
// pkg/dagexec/scheduler.go

func (s *Scheduler) executeComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error {
    // 获取 ComponentVersion
    cv, err := s.CVStore.GetComponentVersion(ctx, node.Name, node.Version)
    if err != nil {
        return err
    }
    
    // 按 type 分发
    executor, ok := s.Registry.Get(cv.Spec.Type)
    if ok {
        // 注册的执行器路径（inline/yaml/helm/staticpod）
        return executor.ExecuteComponent(ctx, node, execCtx)
    }
    
    // 降级到 legacy 路径
    return s.executeComponentLegacy(ctx, node, execCtx)
}
```

### 6.4 DAG 中的 Static Pod 组件节点

```
DAG 中的 Static Pod 组件作为独立节点参与 DAG 调度:

ReleaseImage v2.7.0 DAG:

Batch 1: [pre-upgrade-resources]
    └─ 创建升级所需的 CRD/RBAC 资源

Batch 2: [bkeagent, containerd]      ← 并行执行
    ├─ bkeagent: SSH 推送二进制
    └─ containerd: ENV 命令

Batch 3: [etcd]                      ← type=staticpod 🆕
    └─ StaticPodComponentExecutor:
       ├─ crictl pull etcd:v3.5.20
       ├─ 渲染 manifest YAML
       ├─ 原子写入 /etc/kubernetes/manifests/etcd.yaml
       ├─ 等待 Kubelet 重建 Pod
       └─ 健康检查 (crictl ps)

Batch 4: [kube-apiserver]            ← type=staticpod 🆕
    └─ StaticPodComponentExecutor:
       ├─ crictl pull kube-apiserver:v1.36.0
       ├─ 渲染 manifest YAML
       ├─ 原子写入 /etc/kubernetes/manifests/kube-apiserver.yaml
       ├─ 等待 Kubelet 重建 Pod
       └─ 健康检查

Batch 5: [kube-controller-manager, kube-scheduler]  ← type=staticpod 🆕 并行
    ├─ StaticPodComponentExecutor: cm
    └─ StaticPodComponentExecutor: scheduler

Batch 6: [kubernetes-worker]         ← type=inline (kubelet 二进制升级)
    └─ Upgrade CR → Kubeadm UpgradeWorker

Batch 7: [kube-proxy, coredns]       ← type=yaml
    ├─ YamlInstaller SSA Apply
    └─ YamlInstaller SSA Apply
```

---

## 7. 迁移策略

### 7.1 从 Inline Phase 迁移到 staticpod 类型

| 组件 | 当前实现 | 迁移目标 | 迁移步骤 |
|------|---------|---------|---------|
| **etcd** | `EnsureEtcdUpgrade` (Inline) | `type: staticpod` ComponentVersion | 编写 manifestTemplate + 健康检查 |
| **kube-apiserver** | `EnsureMasterUpgrade` (Inline) | `type: staticpod` ComponentVersion | 编写 manifestTemplate + 健康检查 |
| **kube-controller-manager** | `EnsureMasterUpgrade` (Inline) | `type: staticpod` ComponentVersion | 编写 manifestTemplate + 健康检查 |
| **kube-scheduler** | `EnsureMasterUpgrade` (Inline) | `type: staticpod` ComponentVersion | 编写 manifestTemplate + 健康检查 |
| **haproxy** | `EnsureLoadBalance` (Inline) | `type: staticpod` ComponentVersion | 编写 manifestTemplate + 健康检查 |
| **keepalived** | `EnsureLoadBalance` (Inline) | `type: staticpod` ComponentVersion | 编写 manifestTemplate + 健康检查 |

### 7.2 迁移阶段

| 阶段 | 目标 | 组件 | 说明 |
|------|------|------|------|
| **Phase 1** | 框架能力 | - | 实现 StaticPodSpec、StaticPodInstaller、StaticPodComponentExecutor |
| **Phase 2** | 核心组件迁移 | etcd, kube-apiserver, kube-controller-manager, kube-scheduler | 从 `EnsureMasterUpgrade`/`EnsureEtcdUpgrade` 迁移 |
| **Phase 3** | HA 组件迁移 | haproxy, keepalived | 从 `EnsureLoadBalance` 迁移 |

### 7.3 迁移原则

1. **渐进式迁移**：通过 Feature Gate 控制，新旧路径可并存
2. **向后兼容**：迁移期间 ReleaseImage 可同时包含 inline 和 staticpod 类型组件
3. **版本对齐**：迁移在 openFuyao 版本升级时进行，不在运行中切换

### 7.4 Feature Gate 设计

```go
// pkg/featuregate/features.go

var (
    // StaticPodComponentEnabled 控制 staticpod 类型组件执行器是否启用
    StaticPodComponentEnabled = featuregate.NewFeature()
    
    // EtcdStaticPodMigration 控制 etcd 从 inline 迁移到 staticpod
    EtcdStaticPodMigration = featuregate.NewFeature()
    
    // APIServerStaticPodMigration 控制 apiserver 从 inline 迁移到 staticpod
    APIServerStaticPodMigration = featuregate.NewFeature()
)
```

---

## 8. 组件 ComponentVersion YAML 定义

### 8.1 kube-apiserver ComponentVersion YAML

```yaml
# bke-manifests/kube-apiserver/v1.36.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kube-apiserver-v1.36.0
spec:
  name: kube-apiserver
  type: staticpod
  version: v1.36.0

  staticPod:
    variables:
      bindAddress: "0.0.0.0"
      securePort: "6443"
      serviceClusterIPRange: "{{serviceSubnet}}"
      etcdServers: "{{etcdEndpoints}}"

    image:
      repository: "{{imageRegistry}}/kubernetes/kube-apiserver"
      tag: "{{kubernetesVersion}}"
      pullPolicy: IfNotPresent

    manifestPath: "/etc/kubernetes/manifests/kube-apiserver.yaml"
    
    manifestTemplate: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: kube-apiserver
        namespace: kube-system
        labels:
          component: kube-apiserver
          tier: control-plane
      spec:
        hostNetwork: true
        priorityClassName: system-node-critical
        containers:
          - name: kube-apiserver
            image: {{.StaticPod.Image.Repository}}:{{.StaticPod.Image.Tag}}
            command:
              - kube-apiserver
              - --advertise-address={{nodeIP}}
              - --allow-privileged=true
              - --authorization-mode=Node,RBAC
              - --bind-address={{.Variables.bindAddress}}
              - --client-ca-file=/etc/kubernetes/pki/ca.crt
              - --enable-admission-plugins=NodeRestriction
              - --enable-bootstrap-token-auth=true
              - --etcd-cafile=/etc/kubernetes/pki/etcd/ca.crt
              - --etcd-certfile=/etc/kubernetes/pki/apiserver-etcd-client.crt
              - --etcd-keyfile=/etc/kubernetes/pki/apiserver-etcd-client.key
              - --etcd-servers={{.Variables.etcdServers}}
              - --secure-port={{.Variables.securePort}}
              - --service-account-key-file=/etc/kubernetes/pki/sa.pub
              - --service-cluster-ip-range={{.Variables.serviceClusterIPRange}}
              - --tls-cert-file=/etc/kubernetes/pki/apiserver.crt
              - --tls-private-key-file=/etc/kubernetes/pki/apiserver.key
            volumeMounts:
              - name: k8s-certs
                mountPath: /etc/kubernetes/pki
                readOnly: true
              - name: ca-certs
                mountPath: /etc/ssl/certs
                readOnly: true
            livenessProbe:
              httpGet:
                path: /livez
                port: {{.Variables.securePort}}
                scheme: HTTPS
                host: 127.0.0.1
              initialDelaySeconds: 10
              periodSeconds: 10
        volumes:
          - name: k8s-certs
            hostPath:
              path: /etc/kubernetes/pki
              type: DirectoryOrCreate
          - name: ca-certs
            hostPath:
              path: /etc/ssl/certs
              type: DirectoryOrCreate

    healthCheck:
      enabled: true
      timeout: "3m"
      interval: "5s"
      podReady:
        podName: "kube-apiserver"
        namespace: "kube-system"
        minReady: 1

    upgradeStrategy:
      mode: BackupAndReplace
      backupCount: 3

  nodeFilter:
    roles: ["master"]

  dependencies:
    - name: etcd
      phase: Install
    - name: certs
      phase: Install
    - name: kubelet
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

### 8.2 kube-controller-manager ComponentVersion YAML

```yaml
# bke-manifests/kube-controller-manager/v1.36.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kube-controller-manager-v1.36.0
spec:
  name: kube-controller-manager
  type: staticpod
  version: v1.36.0

  staticPod:
    variables:
      clusterCIDR: "{{podSubnet}}"
      serviceClusterIPRange: "{{serviceSubnet}}"
      clusterDNSIP: "{{dnsIP}}"

    image:
      repository: "{{imageRegistry}}/kubernetes/kube-controller-manager"
      tag: "{{kubernetesVersion}}"
      pullPolicy: IfNotPresent

    manifestPath: "/etc/kubernetes/manifests/kube-controller-manager.yaml"
    
    manifestTemplate: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: kube-controller-manager
        namespace: kube-system
        labels:
          component: kube-controller-manager
          tier: control-plane
      spec:
        hostNetwork: true
        priorityClassName: system-node-critical
        containers:
          - name: kube-controller-manager
            image: {{.StaticPod.Image.Repository}}:{{.StaticPod.Image.Tag}}
            command:
              - kube-controller-manager
              - --allocate-node-cidrs=true
              - --client-ca-file=/etc/kubernetes/pki/ca.crt
              - --cluster-cidr={{.Variables.clusterCIDR}}
              - --controllers=*,bootstrapsigner,tokencleaner
              - --leader-elect=true
              - --service-cluster-ip-range={{.Variables.serviceClusterIPRange}}
            volumeMounts:
              - name: k8s-certs
                mountPath: /etc/kubernetes/pki
                readOnly: true
              - name: ca-certs
                mountPath: /etc/ssl/certs
                readOnly: true
            livenessProbe:
              httpGet:
                path: /healthz
                port: 10257
                scheme: HTTPS
                host: 127.0.0.1
              initialDelaySeconds: 10
              periodSeconds: 10
        volumes:
          - name: k8s-certs
            hostPath:
              path: /etc/kubernetes/pki
              type: DirectoryOrCreate
          - name: ca-certs
            hostPath:
              path: /etc/ssl/certs
              type: DirectoryOrCreate

    healthCheck:
      enabled: true
      timeout: "3m"
      interval: "5s"
      podReady:
        podName: "kube-controller-manager"
        namespace: "kube-system"
        minReady: 1

    upgradeStrategy:
      mode: BackupAndReplace
      backupCount: 3

  nodeFilter:
    roles: ["master"]

  dependencies:
    - name: kube-apiserver
      phase: Install
    - name: certs
      phase: Install
    - name: kubelet
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

### 8.3 kube-scheduler ComponentVersion YAML

```yaml
# bke-manifests/kube-scheduler/v1.36.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kube-scheduler-v1.36.0
spec:
  name: kube-scheduler
  type: staticpod
  version: v1.36.0

  staticPod:
    variables: {}

    image:
      repository: "{{imageRegistry}}/kubernetes/kube-scheduler"
      tag: "{{kubernetesVersion}}"
      pullPolicy: IfNotPresent

    manifestPath: "/etc/kubernetes/manifests/kube-scheduler.yaml"
    
    manifestTemplate: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: kube-scheduler
        namespace: kube-system
        labels:
          component: kube-scheduler
          tier: control-plane
      spec:
        hostNetwork: true
        priorityClassName: system-node-critical
        containers:
          - name: kube-scheduler
            image: {{.StaticPod.Image.Repository}}:{{.StaticPod.Image.Tag}}
            command:
              - kube-scheduler
              - --leader-elect=true
            livenessProbe:
              httpGet:
                path: /healthz
                port: 10259
                scheme: HTTPS
                host: 127.0.0.1
              initialDelaySeconds: 10
              periodSeconds: 10

    healthCheck:
      enabled: true
      timeout: "3m"
      interval: "5s"
      podReady:
        podName: "kube-scheduler"
        namespace: "kube-system"
        minReady: 1

    upgradeStrategy:
      mode: BackupAndReplace
      backupCount: 3

  nodeFilter:
    roles: ["master"]

  dependencies:
    - name: kube-apiserver
      phase: Install
    - name: certs
      phase: Install
    - name: kubelet
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

### 8.4 haproxy ComponentVersion YAML

```yaml
# bke-manifests/haproxy/v2.1.4/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: haproxy-v2.1.4
spec:
  name: haproxy
  type: staticpod
  version: v2.1.4

  staticPod:
    variables:
      controlPlanePort: "{{controlPlaneEndpointPort}}"
      statsPort: "9000"

    image:
      repository: "{{thirdImageRepo}}/haproxy"
      tag: "{{haproxyVersion}}"
      pullPolicy: IfNotPresent

    manifestPath: "/etc/kubernetes/manifests/haproxy.yaml"
    
    manifestTemplate: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: haproxy
        namespace: kube-system
        labels:
          component: haproxy
          tier: control-plane
      spec:
        hostNetwork: true
        containers:
          - name: haproxy
            image: {{.StaticPod.Image.Repository}}:{{.StaticPod.Image.Tag}}
            volumeMounts:
              - name: haproxy-conf
                mountPath: /usr/local/etc/haproxy
                readOnly: true
            livenessProbe:
              httpGet:
                path: /healthz
                port: {{.Variables.statsPort}}
              initialDelaySeconds: 5
              periodSeconds: 5
        volumes:
          - name: haproxy-conf
            hostPath:
              path: /etc/openFuyao/haproxy
              type: DirectoryOrCreate

    preInstallScripts:
      - |
        mkdir -p /etc/openFuyao/haproxy

    configDir: "/etc/openFuyao/haproxy"

    healthCheck:
      enabled: true
      timeout: "2m"
      interval: "5s"
      script: |
        crictl ps --name haproxy | grep -q Running

    upgradeStrategy:
      mode: Replace

  nodeFilter:
    roles: ["master"]

  dependencies:
    - name: kubelet
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: Continue
```

### 8.5 keepalived ComponentVersion YAML

```yaml
# bke-manifests/keepalived/v1.3.5/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: keepalived-v1.3.5
spec:
  name: keepalived
  type: staticpod
  version: v1.3.5

  staticPod:
    variables:
      virtualRouterId: "{{virtualRouterId}}"
      vip: "{{controlPlaneEndpointVIP}}"
      interface: "{{networkInterface}}"

    image:
      repository: "{{fuyaoImageRepo}}/keepalived/keepalived"
      tag: "{{keepalivedVersion}}"
      pullPolicy: IfNotPresent

    manifestPath: "/etc/kubernetes/manifests/keepalived.yaml"
    
    manifestTemplate: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: keepalived
        namespace: kube-system
        labels:
          component: keepalived
          tier: control-plane
      spec:
        hostNetwork: true
        containers:
          - name: keepalived
            image: {{.StaticPod.Image.Repository}}:{{.StaticPod.Image.Tag}}
            securityContext:
              capabilities:
                add: ["NET_ADMIN", "NET_BROADCAST", "NET_RAW"]
            volumeMounts:
              - name: keepalived-conf
                mountPath: /etc/keepalived
                readOnly: true
              - name: keepalived-scripts
                mountPath: /etc/openFuyao/keepalived
                readOnly: true
        volumes:
          - name: keepalived-conf
            hostPath:
              path: /etc/openFuyao/keepalived
              type: DirectoryOrCreate
          - name: keepalived-scripts
            hostPath:
              path: /etc/openFuyao/keepalived
              type: DirectoryOrCreate

    preInstallScripts:
      - |
        mkdir -p /etc/openFuyao/keepalived

    configDir: "/etc/openFuyao/keepalived"

    healthCheck:
      enabled: true
      timeout: "2m"
      interval: "5s"
      script: |
        crictl ps --name keepalived | grep -q Running
        ip addr show | grep -q {{.Variables.vip}}

    upgradeStrategy:
      mode: Replace

  nodeFilter:
    roles: ["master"]

  dependencies:
    - name: kubelet
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: Continue
```

---

## 9. 工作量评估

### 9.1 开发工作量

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **StaticPodSpec 类型定义** | CRD 类型定义 + deepcopy + webhook | 2 |
| **StaticPodInstaller** | 镜像预拉取 + manifest 渲染 + SSH 原子写入 + 健康检查 | 5 |
| **StaticPodComponentExecutor** | DAG 集成 + ExecutorRegistry 注册 | 2 |
| **etcd ComponentVersion** | manifestTemplate + 健康检查 + 迁移适配 | 3 |
| **kube-apiserver ComponentVersion** | manifestTemplate + 健康检查 + 迁移适配 | 3 |
| **kube-controller-manager ComponentVersion** | manifestTemplate + 健康检查 + 迁移适配 | 2 |
| **kube-scheduler ComponentVersion** | manifestTemplate + 健康检查 + 迁移适配 | 2 |
| **haproxy ComponentVersion** | manifestTemplate + 健康检查 + 迁移适配 | 2 |
| **keepalived ComponentVersion** | manifestTemplate + 健康检查 + 迁移适配 | 2 |
| **Feature Gate** | 灰度发布控制 | 1 |
| **小计** | - | **26 人天** |

### 9.2 测试工作量

| 测试类型 | 测试内容 | 工作量（人天） |
|---------|---------|---------------|
| **单元测试** | Installer/Executor/HealthChecker | 3 |
| **集成测试** | 各组件安装/升级/卸载 | 5 |
| **E2E 测试** | 完整升级流程（含 staticpod 组件） | 5 |
| **回滚测试** | BackupAndReplace 回滚 | 2 |
| **小计** | - | **15 人天** |

### 9.3 总工作量汇总

| 类别 | 工作量（人天） |
|------|---------------|
| **开发** | 26 |
| **测试** | 15 |
| **总计** | **41** |

---

## 10. 风险与缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **manifest 渲染错误** | Pod 无法启动 | 中 | 原子写入 + BackupAndReplace 回滚 |
| **镜像拉取失败** | 升级阻塞 | 中 | 镜像预拉取 + 重试机制 |
| **Kubelet 未检测到文件变化** | Pod 未重建 | 低 | 健康检查超时后告警 |
| **Inline 迁移兼容性** | 新旧路径冲突 | 中 | Feature Gate 控制 + 渐进式迁移 |
| **etcd 滚动升级失败** | 集群不可用 | 低 | 逐节点备份 + 阻塞式失败处理 |

---

## 附录

### A. 参考文档

1. [Static Pod 官方文档](https://kubernetes.io/docs/tasks/configure-pod-container/static-pod/)
2. [KEP-5 声明式升级框架](kep5/kep5.md)
3. [KEP-6 三层状态机设计](kep6-state-machine-v5.md)
4. [StaticPod 组件类型设计（原始）](staticpod-type-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **Static Pod** | Kubernetes 中由 Kubelet 直接管理的 Pod，YAML 文件放置在 `/etc/kubernetes/manifests/` 目录下 |
| **StaticPodInstaller** | 负责 Static Pod 组件的镜像预拉取、YAML 渲染、manifest 写入的安装器 |
| **StaticPodComponentExecutor** | DAG 执行器，将 Static Pod 组件纳入 DAG 统一调度 |
| **manifestTemplate** | Static Pod YAML 模板，支持 Go template 语法 |
| **原子写入** | 先写入临时文件，再 mv 替换，避免半截文件导致 Pod 异常 |
| **BackupAndReplace** | 升级模式：先备份旧 manifest，再替换，失败时自动回滚 |

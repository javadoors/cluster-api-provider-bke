# StaticPod 组件类型设计

## 目录

1. [概述](#1-概述)
2. [设计动机](#2-设计动机)
3. [StaticPodSpec 类型定义](#3-staticpodspec-类型定义)
4. [StaticPodInstaller 详细设计](#4-staticpodinstaller-详细设计)
5. [组件 ComponentVersion YAML 定义](#5-组件-componentversion-yaml-定义)
6. [DAG 集成设计](#6-dag-集成设计)
7. [迁移策略](#7-迁移策略)
8. [待改造组件 TODO 清单](#8-待改造组件-todo-清单)

---

## 1. 概述

### 1.1 设计目标

本设计文档定义 `staticpod` 组件类型，用于管理通过 Static Pod 方式部署的 Kubernetes 控制面组件和高可用组件。

### 1.2 设计范围

| 范围 | 说明 |
|------|------|
| CRD 扩展 | ComponentVersion 新增 `staticpod` 类型字段定义 |
| 核心安装器 | StaticPodInstaller 的完整实现 |
| DAG 集成 | StaticPodComponentExecutor 注册与调度 |
| 迁移策略 | 从现有 manifests 插件和 HA 插件迁移 |

### 1.3 术语表

| 术语 | 定义 |
|------|------|
| **Static Pod** | Kubernetes 中由 Kubelet 直接管理的 Pod，YAML 文件放置在 `/etc/kubernetes/manifests/` 目录下 |
| **StaticPodInstaller** | 负责 Static Pod 组件的镜像预拉取、YAML 渲染、manifest 写入的安装器 |
| **manifestTemplate** | Static Pod YAML 模板，支持 Go template 语法 |
| **StaticPodComponent** | 通过 Static Pod 方式部署的组件（etcd/apiserver/controller-manager/scheduler/HAProxy/Keepalived） |

---

## 2. 设计动机

### 2.1 为什么不用 binary 类型

Static Pod 组件与 binary 类型存在本质差异：

| 维度 | Binary 类型 | Static Pod 组件 | 结论 |
|------|------------|----------------|------|
| **制品类型** | 二进制文件 (tar.gz/单文件) | OCI 镜像 | ❌ 不匹配 |
| **安装方式** | 下载 → 解压 → chmod | 镜像拉取 → YAML 渲染 → 写入 manifests | ❌ 不匹配 |
| **服务管理** | systemd enable/restart | Kubelet 自动管理 (写入即生效) | ❌ 不匹配 |
| **卸载方式** | systemd stop → 删除二进制 | 删除 YAML 文件 → Kubelet 自动终止 | ❌ 不匹配 |
| **升级方式** | 停服务 → 替换二进制 → 重启 | 替换 YAML → Kubelet 自动重建 Pod | ❌ 不匹配 |

### 2.2 为什么不用 yaml 类型

Static Pod 组件虽然最终是 YAML 文件，但与 yaml 类型（集群级资源）也有差异：

| 维度 | YAML 类型 | Static Pod 组件 | 结论 |
|------|----------|----------------|------|
| **部署目标** | 集群级 K8s 资源 (kubectl apply) | 节点级文件 (写入 /etc/kubernetes/manifests/) | ❌ 不匹配 |
| **执行方式** | K8s API Client | SSH 写入节点文件系统 | ❌ 不匹配 |
| **生命周期** | K8s Controller 管理 | Kubelet 本地文件监控 | ❌ 不匹配 |
| **镜像预拉取** | 不涉及 | 需要 crictl/docker pull | ❌ 不匹配 |

### 2.3 staticpod 类型的定位

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
          │ OCI镜像  │ staticpod           │ helm / yaml                       │
          │         │ etcd                │ coredns                           │
          │         │ apiserver/cm/sched  │ calico                            │
          │         │ haproxy/keepalived  │ monitoring                        │
          └─────────┴─────────────────────┴───────────────────────────────────┘
```

---

## 3. StaticPodSpec 类型定义

### 3.1 Go 类型定义

```go
// api/v1alpha1/componentversion_types.go

// StaticPodSpec 定义 Static Pod 组件规格 🆕新增
//
// 设计思路 — 与 BinarySpec 的区别:
// StaticPod 组件通过 OCI 镜像部署，YAML 写入 /etc/kubernetes/manifests/，
// 由 Kubelet 自动拉起，无需 systemd 管理。
//
// 设计思路 — 与 YAMLSpec 的区别:
// YAMLSpec 通过 K8s API apply 集群级资源；
// StaticPodSpec 通过 SSH 写入节点级文件，Kubelet 本地监控。
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
    // 用于创建用户、目录等准备工作
    PreInstallScripts []string `json:"preInstallScripts,omitempty"`
    
    // 后置安装脚本 (YAML 写入后执行)
    // 用于验证 Pod 启动状态等
    PostInstallScripts []string `json:"postInstallScripts,omitempty"`
    
    // 卸载脚本 (删除 YAML 文件后执行)
    // 用于清理数据目录、用户等
    UninstallScript string `json:"uninstallScript,omitempty"`
    
    // 数据目录 (组件持久化数据路径)
    // 用于卸载时清理、备份时记录
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
    // 示例: "{{imageRegistry}}/kubernetes/etcd:{{etcdVersion}}"
    Repository string `json:"repository"`
    
    // 镜像 Tag (支持模板变量)
    // 示例: "{{etcdVersion}}" 或 "v3.5.21-of.1"
    Tag string `json:"tag"`
    
    // 镜像拉取策略: Always / IfNotPresent / Never
    // 默认: IfNotPresent
    PullPolicy string `json:"pullPolicy,omitempty"`
    
    // 镜像校验和 (可选, 用于离线环境验证)
    Checksum string `json:"checksum,omitempty"`
}

// ImagePullSpec 定义镜像预拉取配置
type ImagePullSpec struct {
    // 是否启用镜像预拉取
    // 默认: true (在写入 YAML 前先拉取镜像)
    Enabled *bool `json:"enabled,omitempty"`
    
    // 拉取超时时间
    // 默认: "5m"
    Timeout string `json:"timeout,omitempty"`
    
    // 拉取重试次数
    // 默认: 3
    RetryCount int `json:"retryCount,omitempty"`
    
    // 容器运行时: containerd / docker
    // 默认: 从 TemplateContext.ContainerRuntimeCRI 获取
    Runtime string `json:"runtime,omitempty"`
}

// StaticPodHealthCheckSpec 定义 Static Pod 健康检查规格
type StaticPodHealthCheckSpec struct {
    // 是否启用健康检查
    Enabled bool `json:"enabled"`
    
    // 等待超时时间 (默认 3m, Static Pod 启动可能较慢)
    Timeout string `json:"timeout,omitempty"`
    
    // 重试间隔 (默认 5s)
    Interval string `json:"interval,omitempty"`
    
    // 健康检查脚本 (Go template, 通过 SSH 在远程节点执行)
    // 退出码 0 = 健康, 非零 = 不健康
    // 默认检查: crictl ps --name <pod-name> | grep Running
    Script string `json:"script,omitempty"`
    
    // Pod 就绪检查 (可选, 更精确的检查方式)
    PodReady *StaticPodReadyCheckSpec `json:"podReady,omitempty"`
}

// StaticPodReadyCheckSpec 定义 Pod 就绪检查
type StaticPodReadyCheckSpec struct {
    // Pod 名称 (支持模板变量)
    // 示例: "etcd" / "kube-apiserver"
    PodName string `json:"podName"`
    
    // 命名空间
    // 默认: "kube-system"
    Namespace string `json:"namespace,omitempty"`
    
    // 容器名称 (用于多容器 Pod)
    ContainerName string `json:"containerName,omitempty"`
    
    // 最小就绪容器数
    // 默认: 1
    MinReady int `json:"minReady,omitempty"`
}

// StaticPodUpgradeStrategySpec 定义 Static Pod 升级策略
type StaticPodUpgradeStrategySpec struct {
    // 升级模式: Replace / BackupAndReplace
    // Replace: 直接替换 YAML 文件, Kubelet 自动重建 Pod (默认)
    // BackupAndReplace: 先备份旧 YAML, 再替换, 失败时自动回滚
    Mode string `json:"mode,omitempty"`
    
    // 备份保留数量 (BackupAndReplace 模式下)
    // 默认: 3
    BackupCount int `json:"backupCount,omitempty"`
    
    // Pod 终止宽限期 (秒)
    // 默认: 30
    TerminationGracePeriod int `json:"terminationGracePeriod,omitempty"`
}
```

### 3.2 与现有类型的关系

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

---

## 4. StaticPodInstaller 详细设计

### 4.1 核心组件架构

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
│  │  crictl pull   │  │  Go template   │  │  写入 manifest  │                    │
│  │  docker pull   │  │  渲染 YAML     │  │  到节点         │                    │
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

### 4.2 安装流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  StaticPod 安装流程                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌──────────────────────────────┐
    │  1. 加载 ComponentVersion    │
    │  解析 StaticPodSpec          │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  2. 渲染模板变量              │
    │  - image.repository          │
    │  - image.tag                 │
    │  - variables.*               │
    │  - TemplateContext.*         │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  3. 镜像预拉取 (可选)         │
    │  ┌────────────────────────┐  │
    │  │ if imagePull.enabled:  │  │
    │  │   crictl pull <image>  │  │
    │  │   或 docker pull       │  │
    │  └────────────────────────┘  │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  4. 执行前置脚本 (可选)       │
    │  - 创建 etcd 用户             │
    │  - 创建数据目录               │
    │  - 设置目录权限               │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  5. 渲染 Manifest YAML       │
    │  Go template 渲染            │
    │  manifestTemplate            │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  6. 写入 Manifest 文件       │
    │  SSH 写入 /etc/kubernetes/   │
    │  manifests/<name>.yaml       │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  7. 等待 Kubelet 拉起 Pod    │
    │  Kubelet 检测到新文件         │
    │  自动创建 Pod                │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  8. 健康检查                  │
    │  crictl ps --name <pod>      │
    │  验证 Running 状态            │
    └──────────────────────────────┘
```

### 4.3 升级流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  StaticPod 升级流程 (BackupAndReplace 模式)                                      │
└─────────────────────────────────────────────────────────────────────────────────┘

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
    │  4. 替换 Manifest 文件       │
    │  原子写入 (write tmp + mv)   │
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

### 4.4 卸载流程

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  StaticPod 卸载流程                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘

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
    │  - 清理数据目录               │
    │  - 清理用户                   │
    │  - 清理证书                   │
    └──────────────────────────────┘
```

### 4.5 核心接口定义

```go
// pkg/installer/staticpod/installer.go

// StaticPodInstaller Static Pod 组件安装器
type StaticPodInstaller struct {
    // SSH 客户端
    sshClient bkessh.Client
    
    // 模板渲染器
    renderer *TemplateRenderer
    
    // 镜像拉取器
    imagePuller *ImagePuller
    
    // 健康检查器
    healthChecker *StaticPodHealthChecker
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
    
    // 5. 写入 Manifest 文件
    manifestPath := spec.ManifestPath
    if manifestPath == "" {
        manifestPath = fmt.Sprintf("/etc/kubernetes/manifests/%s.yaml", execCtx.ComponentVersion.Spec.Name)
    }
    if err := i.sshClient.WriteFile(execCtx.Node, manifestPath, manifest, 0644); err != nil {
        return fmt.Errorf("write manifest: %w", err)
    }
    
    // 6. 健康检查
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
    
    // 升级策略
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
    
    // 执行安装流程 (拉取新镜像 + 渲染新 YAML + 替换文件)
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

## 5. 组件 ComponentVersion YAML 定义

### 5.1 etcd ComponentVersion YAML

```yaml
# bke-manifests/etcd/v3.5.21-of.1/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.21-of.1
spec:
  name: etcd
  type: staticpod
  version: v3.5.21-of.1

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
              - --advertise-client-urls=https://{{nodeIP}}:{{.Variables.clientPort}}
              - --cert-file=/etc/kubernetes/pki/etcd/server.crt
              - --client-cert-auth=true
              - --data-dir={{.Variables.dataDir}}
              - --initial-advertise-peer-urls=https://{{nodeIP}}:{{.Variables.peerPort}}
              - --initial-cluster={{etcdInitialCluster}}
              - --key-file=/etc/kubernetes/pki/etcd/server.key
              - --listen-client-urls=https://{{nodeIP}}:{{.Variables.clientPort}},https://127.0.0.1:{{.Variables.clientPort}}
              - --listen-metrics-urls=http://127.0.0.1:{{.Variables.metricsPort}}
              - --listen-peer-urls=https://{{nodeIP}}:{{.Variables.peerPort}}
              - --name={{nodeName}}
              - --peer-cert-file=/etc/kubernetes/pki/etcd/peer.crt
              - --peer-client-cert-auth=true
              - --peer-key-file=/etc/kubernetes/pki/etcd/peer.key
              - --peer-trusted-ca-file=/etc/kubernetes/pki/etcd/ca.crt
              - --snapshot-count=10000
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
              timeoutSeconds: 15
              failureThreshold: 8
            resources:
              requests:
                cpu: 100m
                memory: 512Mi
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
        # 创建 etcd 用户
        useradd -r -s /sbin/nologin etcd 2>/dev/null || true
        # 创建数据目录
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

  compatibility:
    constraints:
      - component: kubelet
        rule: ">=1.26.0"

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

### 5.2 kube-apiserver ComponentVersion YAML

```yaml
# bke-manifests/kube-apiserver/v1.29.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kube-apiserver-v1.29.0
spec:
  name: kube-apiserver
  type: staticpod
  version: v1.29.0

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
              - --kubelet-certificate-authority=/etc/kubernetes/pki/ca.crt
              - --kubelet-client-certificate=/etc/kubernetes/pki/apiserver-kubelet-client.crt
              - --kubelet-client-key=/etc/kubernetes/pki/apiserver-kubelet-client.key
              - --proxy-client-cert-file=/etc/kubernetes/pki/front-proxy-client.crt
              - --proxy-client-key-file=/etc/kubernetes/pki/front-proxy-client.key
              - --requestheader-allowed-names=front-proxy-client
              - --requestheader-client-ca-file=/etc/kubernetes/pki/front-proxy-ca.crt
              - --requestheader-extra-headers-prefix=X-Remote-Extra-
              - --requestheader-group-headers=X-Remote-Group
              - --requestheader-username-headers=X-Remote-User
              - --secure-port={{.Variables.securePort}}
              - --service-account-issuer=https://kubernetes.default.svc.cluster.local
              - --service-account-key-file=/etc/kubernetes/pki/sa.pub
              - --service-account-signing-key-file=/etc/kubernetes/pki/sa.key
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
              timeoutSeconds: 15
              failureThreshold: 8
            resources:
              requests:
                cpu: 250m
                memory: 512Mi
        volumes:
          - name: k8s-certs
            hostPath:
              path: /etc/kubernetes/pki
              type: DirectoryOrCreate
          - name: ca-certs
            hostPath:
              path: /etc/ssl/certs
              type: DirectoryOrCreate

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

### 5.3 kube-controller-manager ComponentVersion YAML

```yaml
# bke-manifests/kube-controller-manager/v1.29.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kube-controller-manager-v1.29.0
spec:
  name: kube-controller-manager
  type: staticpod
  version: v1.29.0

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
              - --authentication-kubeconfig=/etc/kubernetes/controller-manager.conf
              - --authorization-kubeconfig=/etc/kubernetes/controller-manager.conf
              - --bind-address=127.0.0.1
              - --client-ca-file=/etc/kubernetes/pki/ca.crt
              - --cluster-cidr={{.Variables.clusterCIDR}}
              - --cluster-signing-cert-file=/etc/kubernetes/pki/ca.crt
              - --cluster-signing-key-file=/etc/kubernetes/pki/ca.key
              - --controllers=*,bootstrapsigner,tokencleaner
              - --kubeconfig=/etc/kubernetes/controller-manager.conf
              - --leader-elect=true
              - --requestheader-client-ca-file=/etc/kubernetes/pki/front-proxy-ca.crt
              - --root-ca-file=/etc/kubernetes/pki/ca.crt
              - --service-account-private-key-file=/etc/kubernetes/pki/sa.key
              - --service-cluster-ip-range={{.Variables.serviceClusterIPRange}}
              - --use-service-account-credentials=true
            volumeMounts:
              - name: k8s-certs
                mountPath: /etc/kubernetes/pki
                readOnly: true
              - name: kubeconfig
                mountPath: /etc/kubernetes/controller-manager.conf
                readOnly: true
              - name: ca-certs
                mountPath: /etc/ssl/certs
                readOnly: true
            livenessProbe:
              httpGet:
                path: /healthz
                port: 10259
                scheme: HTTPS
                host: 127.0.0.1
              initialDelaySeconds: 10
              periodSeconds: 10
              timeoutSeconds: 15
              failureThreshold: 8
            resources:
              requests:
                cpu: 200m
                memory: 256Mi
        volumes:
          - name: k8s-certs
            hostPath:
              path: /etc/kubernetes/pki
              type: DirectoryOrCreate
          - name: kubeconfig
            hostPath:
              path: /etc/kubernetes/controller-manager.conf
              type: FileOrCreate
          - name: ca-certs
            hostPath:
              path: /etc/ssl/certs
              type: DirectoryOrCreate

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

### 5.4 kube-scheduler ComponentVersion YAML

```yaml
# bke-manifests/kube-scheduler/v1.29.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kube-scheduler-v1.29.0
spec:
  name: kube-scheduler
  type: staticpod
  version: v1.29.0

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
              - --authentication-kubeconfig=/etc/kubernetes/scheduler.conf
              - --authorization-kubeconfig=/etc/kubernetes/scheduler.conf
              - --bind-address=127.0.0.1
              - --kubeconfig=/etc/kubernetes/scheduler.conf
              - --leader-elect=true
            volumeMounts:
              - name: kubeconfig
                mountPath: /etc/kubernetes/scheduler.conf
                readOnly: true
            livenessProbe:
              httpGet:
                path: /healthz
                port: 10259
                scheme: HTTPS
                host: 127.0.0.1
              initialDelaySeconds: 10
              periodSeconds: 10
              timeoutSeconds: 15
              failureThreshold: 8
            resources:
              requests:
                cpu: 100m
                memory: 128Mi
        volumes:
          - name: kubeconfig
            hostPath:
              path: /etc/kubernetes/scheduler.conf
              type: FileOrCreate

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

### 5.5 HAProxy ComponentVersion YAML

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
            resources:
              requests:
                cpu: 50m
                memory: 64Mi
        volumes:
          - name: haproxy-conf
            hostPath:
              path: /etc/openFuyao/haproxy
              type: DirectoryOrCreate

    preInstallScripts:
      - |
        # 创建 HAProxy 配置目录
        mkdir -p /etc/openFuyao/haproxy
        # 渲染 haproxy.cfg (由 ConfigRenderer 上传)

    configDir: "/etc/openFuyao/haproxy"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

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

### 5.6 Keepalived ComponentVersion YAML

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
            resources:
              requests:
                cpu: 50m
                memory: 64Mi
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
        # 创建 Keepalived 配置目录
        mkdir -p /etc/openFuyao/keepalived
        # 渲染 keepalived.conf 和 check 脚本 (由 ConfigRenderer 上传)

    configDir: "/etc/openFuyao/keepalived"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

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

### 5.7 pause ComponentVersion YAML

```yaml
# bke-manifests/pause/v3.9/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: pause-v3.9
spec:
  name: pause
  type: staticpod
  version: v3.9

  staticPod:
    variables: {}

    image:
      repository: "{{imageRegistry}}/kubernetes/pause"
      tag: "{{pauseVersion}}"
      pullPolicy: IfNotPresent

    # pause 镜像不需要 manifest, 仅用于镜像预拉取
    manifestTemplate: ""
    
    imagePull:
      enabled: true
      timeout: "2m"
      retryCount: 3

    preInstallScripts:
      - |
        # 仅预拉取 pause 镜像
        # containerd 作为 sandbox_image 使用
        # docker 作为 --pod-infra-container-image 使用
        echo "pause image {{.StaticPod.Image.Repository}}:{{.StaticPod.Image.Tag}} pulled"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

    healthCheck:
      enabled: false

  # pause 镜像预拉取到所有节点
  nodeFilter:
    roles: ["master", "worker"]

  dependencies:
    - name: container-runtime
      phase: Install

  upgradeStrategy:
    mode: Parallel
    timeout: "5m"
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

// Execute 执行 Static Pod 组件安装/升级
func (e *StaticPodComponentExecutor) Execute(ctx context.Context, execCtx *ExecutionContext) error {
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

// Register 注册到 ComponentFactory
func init() {
    componentfactory.Register("staticpod", func() componentfactory.ComponentExecutor {
        return &StaticPodComponentExecutor{
            installer: staticpod.NewInstaller(),
        }
    })
}
```

### 6.2 依赖关系图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  Static Pod 组件依赖关系                                                         │
└─────────────────────────────────────────────────────────────────────────────────┘

    kubelet (binary)
         │
         ▼
    ┌────────────────────────────────────────────────────────────────┐
    │  Master 节点 Static Pod 组件                                    │
    │                                                                 │
    │  certs ──► etcd ──► kube-apiserver ──► kube-controller-manager │
    │                                └──────► kube-scheduler          │
    │                                                                 │
    │  kubelet ──► haproxy                                            │
    │         └──► keepalived                                         │
    └────────────────────────────────────────────────────────────────┘

    依赖说明:
    - etcd 依赖 certs (证书) 和 kubelet
    - kube-apiserver 依赖 etcd、certs、kubelet
    - kube-controller-manager 依赖 kube-apiserver、certs、kubelet
    - kube-scheduler 依赖 kube-apiserver、certs、kubelet
    - haproxy/keepalived 依赖 kubelet
```

---

## 7. 迁移策略

### 7.1 从 manifests 插件迁移

当前 Static Pod 组件通过 bkeagent 内置的 `manifests` 插件渲染：

| 当前实现 | 迁移目标 |
|---------|---------|
| `pkg/job/builtin/kubeadm/manifests/` | `staticpod` 类型 ComponentVersion |
| `utils/bkeagent/mfutil/componentlist.go` | StaticPodInstaller |
| Static Pod YAML 硬编码在 Go 代码中 | manifestTemplate 声明式配置 |

### 7.2 从 HA 插件迁移

当前 HA 组件通过 bkeagent 内置的 `ha` 插件部署：

| 当前实现 | 迁移目标 |
|---------|---------|
| `pkg/job/builtin/ha/` | `staticpod` 类型 ComponentVersion |
| HAProxy/Keepalived YAML 硬编码 | manifestTemplate 声明式配置 |

### 7.3 Feature Gate

```go
// 新增 Feature Gate
const (
    FeatureStaticPodType = "StaticPodType"
)

// 默认关闭, 灰度开启
var defaultFeatureGates = map[string]bool{
    FeatureStaticPodType: false,
}
```

---

## 8. 待改造组件 TODO 清单

### 8.1 StaticPod 类型组件 (P0)

| 组件 | 当前实现 | 改造内容 | 优先级 | 预估工作量 |
|------|---------|---------|--------|-----------|
| **etcd** | `pkg/job/builtin/kubeadm/manifests/` | 定义 ComponentVersion YAML + StaticPodInstaller | P0 | 3天 |
| **kube-apiserver** | `pkg/job/builtin/kubeadm/manifests/` | 定义 ComponentVersion YAML + StaticPodInstaller | P0 | 2天 |
| **kube-controller-manager** | `pkg/job/builtin/kubeadm/manifests/` | 定义 ComponentVersion YAML + StaticPodInstaller | P0 | 2天 |
| **kube-scheduler** | `pkg/job/builtin/kubeadm/manifests/` | 定义 ComponentVersion YAML + StaticPodInstaller | P0 | 2天 |
| **HAProxy** | `pkg/job/builtin/ha/` | 定义 ComponentVersion YAML + StaticPodInstaller | P1 | 2天 |
| **Keepalived** | `pkg/job/builtin/ha/` | 定义 ComponentVersion YAML + StaticPodInstaller | P1 | 2天 |

### 8.2 Binary 类型组件 (P0)

| 组件 | 当前实现 | 改造内容 | 优先级 | 预估工作量 |
|------|---------|---------|--------|-----------|
| **kubelet** | `pkg/job/builtin/kubeadm/kubelet/` | 定义 ComponentVersion YAML + BinaryInstaller | P0 | 3天 |
| **kubectl** | `pkg/job/builtin/kubeadm/command.go` | 定义 ComponentVersion YAML + BinaryInstaller | P0 | 1天 |

### 8.3 Helm 类型组件 (P1)

| 组件 | 当前实现 | 改造内容 | 优先级 | 预估工作量 |
|------|---------|---------|--------|-----------|
| **coredns** | `pkg/kube/addon.go` | 定义 ComponentVersion YAML + HelmInstaller | P1 | 2天 |
| **calico** | `pkg/kube/addon.go` | 定义 ComponentVersion YAML + HelmInstaller | P1 | 3天 |
| **kube-proxy** | `pkg/kube/addon.go` | 定义 ComponentVersion YAML + HelmInstaller | P1 | 2天 |

### 8.4 工具类组件 (P2)

| 组件 | 当前实现 | 改造内容 | 优先级 | 预估工作量 |
|------|---------|---------|--------|-----------|
| **etcdctl** | `install-etcdctl.sh` | 定义 ComponentVersion YAML + BinaryInstaller | P2 | 1天 |
| **helm** | `install-helm.sh` | 定义 ComponentVersion YAML + BinaryInstaller | P2 | 1天 |
| **calicoctl** | `install-calicoctl.sh` | 定义 ComponentVersion YAML + BinaryInstaller | P2 | 1天 |
| **lxcfs** | `install-lxcfs.sh` | 定义 ComponentVersion YAML + BinaryInstaller | P2 | 1天 |
| **runc** | `update-runc.sh` | 定义 ComponentVersion YAML + BinaryInstaller | P2 | 1天 |

### 8.5 核心框架改造 (P0)

| 任务 | 说明 | 优先级 | 预估工作量 |
|------|------|--------|-----------|
| **StaticPodSpec 类型定义** | 在 `api/v1alpha1/componentversion_types.go` 中新增 StaticPodSpec | P0 | 2天 |
| **StaticPodInstaller 实现** | 在 `pkg/installer/staticpod/` 中实现安装/升级/卸载逻辑 | P0 | 5天 |
| **StaticPodComponentExecutor** | 在 `pkg/dagexec/executor/` 中注册执行器 | P0 | 2天 |
| **CRD YAML 更新** | 更新 `config/crd/bases/config.openfuyao.cn_componentversions.yaml` | P0 | 1天 |
| **单元测试** | StaticPodInstaller 单元测试 | P0 | 3天 |
| **集成测试** | Static Pod 组件端到端测试 | P0 | 3天 |

### 8.6 总计工作量

| 类别 | 工作量 |
|------|--------|
| StaticPod 类型组件改造 | 13天 |
| Binary 类型组件改造 | 4天 |
| Helm 类型组件改造 | 7天 |
| 工具类组件改造 | 5天 |
| 核心框架改造 | 16天 |
| **总计** | **45天 (约 2.5 个月)** |

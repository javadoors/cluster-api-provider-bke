# KEP-6: 基于 ReleaseImage 的二进制与 Helm 组件声明式管理方案

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-6 |
| **标题** | 基于 ReleaseImage 的 Binary/Helm/YAML 组件声明式管理方案 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **依赖** | KEP-5 (ClusterVersion/ReleaseImage/UpgradePath)、ComponentVersion CRD、DAG 调度器、bke-manifests |

## 1. 摘要

本提案将 Containerd 与 BKEAgent 组件从硬编码 Phase 重构为 ReleaseImage 中声明式的二进制组件 (`ComponentTypeBinary`)，同时新增 Helm 组件类型 (`ComponentTypeHelm`) 和 YAML 清单组件类型 (`ComponentTypeYAML`) 支持。方案引入 `BinaryInstaller`、`HelmInstaller` 和 `YamlInstaller` 三个统一安装器，配合 `configTemplates` 配置模板引擎和 `installScript` 模板变量系统，实现组件安装/升级的声明式管理。YAML 类型组件通过 `VersionContext` 携带版本事实，Executor 据此自主决定操作类型（Install/Upgrade/Skip），符合 Kubernetes 声明式协调模式。架构彻底解耦，新增二进制/Helm/YAML 组件只需添加 ComponentVersion YAML，核心代码零侵入。

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状 | 影响 |
|------|------|------|
| **版本硬编码** | Containerd/BKEAgent 版本散落在 `BKECluster.Spec` 各字段 | 无法通过 ReleaseImage 统一管理，版本追溯困难 |
| **安装/升级分离** | 安装和升级使用不同 Phase，逻辑重复 | 代码冗余，维护成本高，行为不一致 |
| **无二进制组件支持** | `ComponentTypeBinary` 已定义但未实现 | 新增二进制组件需修改核心调度代码 |
| **架构适配硬编码** | bkeagent 的架构适配 (`bkeagent_linux_{arch}`) 写在代码中 | 新增架构需改代码发版 |
| **Helm 组件缺失** | 无 Helm 类型组件支持 | CoreDNS/kube-proxy 等组件无法通过 Helm 管理 |
| **配置管理硬编码** | 配置文件内容硬编码在脚本中 | 配置变更需改代码，无法声明式管理 |

### 2.2 目标

1. 实现 `ComponentTypeBinary` 类型组件的完整支持，包括制品下载、模板渲染、SSH 安装、健康检查。
2. 实现 `ComponentTypeHelm` 类型组件的完整支持，包括 OCI/HTTP/本地 Chart 获取、Values 渲染、健康检查。
3. 实现 `ComponentTypeYAML` 类型组件的完整支持，包括清单获取、多文档解析、ServerSideApply/Replace/CreateOnly 三种应用策略、Prune 裁剪、健康检查。
4. 设计 `configTemplates` 配置模板引擎，支持 Go template、Secret 引用、动态 kubeconfig 生成。
5. 设计 `installScript` 模板变量系统，支持 8 类 50+ 变量和条件渲染。
6. 引入 `VersionContext` 携带版本事实，Executor 据此自主决定操作类型，替代 `IsUpgrade bool`，符合 Kubernetes 声明式协调模式。
7. 将 Containerd/BKEAgent 从硬编码 Phase 迁移到 ReleaseImage 声明式管理。
8. 提供平滑迁移方案，Feature Gate 控制，新旧双轨运行。

### 2.3 非目标

1. 不修改现有 inline Phase 的执行逻辑，仅新增 Binary/Helm 类型支持。
2. 不替换现有 SSH 推送机制，BinaryInstaller 复用现有 `bkessh.MultiCli`。
3. 不在此阶段实现组件制品的自动构建与发布流程。
4. 不重写 DAG 调度器核心逻辑，仅新增 BinaryComponentExecutor 和 HelmComponentExecutor。

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| CRD 扩展 | `ComponentVersion` 新增 `binary`、`helm`、`yaml` 类型的完整字段定义，以及 `SubComponents`、`Resources` 通用字段 |
| BinaryInstaller | 二进制制品下载、缓存、模板渲染、SSH 安装、健康检查、卸载 |
| HelmInstaller | Chart 获取 (OCI/HTTP/本地)、Values 渲染、Install/Upgrade/Rollback/Uninstall |
| YamlInstaller | YAML 清单获取、多文档解析、ServerSideApply/Replace/CreateOnly 应用策略、Prune 裁剪、健康检查 |
| configTemplates | Go template 渲染、Secret 引用、动态 kubeconfig 生成 |
| installScript | 8 类 50+ 模板变量、条件渲染、自定义变量 |
| DAG 集成 | BinaryComponentExecutor、HelmComponentExecutor、YamlComponentExecutor 集成到 DAG 调度器 |
| VersionContext | 携带版本事实（已安装版本、目标版本），Executor 自主决定 Install/Upgrade/Skip |
| Phase 迁移 | 移除 `EnsureContainerdUpgrade`、`EnsureBKEAgent`、`EnsureAgentUpgrade` 硬编码逻辑；`EnsureNodesEnv` 移除 `runtime` scope（containerd 安装由 BinaryInstaller 接管） |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 必须支持从现有硬编码 Phase 平滑迁移，Feature Gate 控制开关 |
| **离线环境** | 二进制制品和 Helm Chart 支持本地缓存，支持断网安装 |
| **架构支持** | 必须支持 amd64 和 arm64 架构 |
| **操作系统支持** | 必须支持 CentOS 7/8、Ubuntu 20.04/22.04、麒麟 V10 |
| **接口复用** | 复用现有 `NeedExecute()` 接口，不新增升级决策接口 |
| **安全性** | 制品必须支持 checksum 校验，敏感配置通过 Secret 引用 |

## 4. 提案设计

### 4.1 整体架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        BKECluster                                        │
│  spec.desiredVersion: v2.6.0                                             │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                      ReleaseImage                                        │
│  spec.install.components: [containerd/v1.7.18, bkeagent/v2.6.0, ...]    │
│  spec.upgrade.components: [containerd/v1.7.18, bkeagent/v2.6.0, ...]    │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │ 按 (name, version) 定位
                           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                    bke-manifests (ComponentVersion)                      │
│                                                                         │
│  containerd/v1.7.18/component.yaml     ← type: binary                   │
│    ├── binary.artifacts: [containerd, ctr, shim]                        │
│    ├── binary.configTemplates: [config.toml, service]                   │
│    └── binary.installScript: (带 50+ 模板变量)                          │
│                                                                         │
│  bkeagent/v2.6.0/component.yaml        ← type: binary                   │
│    ├── binary.artifacts: [bkeagent]                                     │
│    ├── binary.configTemplates: [bkeagent.conf, tls, kubeconfig]         │
│    └── binary.installScript: (带完整模板变量)                           │
│                                                                         │
│  coredns/v1.11.1/component.yaml        ← type: helm                     │
│    ├── helm.chart.oci: registry/charts/coredns                          │
│    ├── helm.values: (带模板变量)                                        │
│    └── helm.healthCheck: PodReady + EndpointReady                       │
│                                                                         │
│  openfuyao-core/v26.03/component.yaml  ← type: yaml                     │
│    ├── yaml.manifests: [crds.yaml, deployment.yaml]                     │
│    ├── yaml.applyStrategy: ServerSideApply                              │
│    ├── yaml.prune: true (按 label selector 裁剪废弃资源)                 │
│    └── subComponents: [kubernetes-master, kubernetes-worker]            │
└─────────────────────────────────────────────────────────────────────────┘
```

### 4.2 ComponentVersion Binary 类型定义

```yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.18
spec:
  name: containerd
  type: binary
  version: v1.7.18
  
  binary:
    # 自定义变量 (可覆盖默认值)
    variables:
      logLevel: "info"
      maxConcurrentDownloads: 10
      snapshotter: "overlayfs"
    
    # 二进制制品定义 (tar.gz 包含 containerd, containerd-shim-runc-v2, ctr)
    artifacts:
      - name: containerd
        url: "https://release-repo.openfuyao.cn/binaries/containerd/{{componentVersion}}/containerd-{{componentVersion}}-linux-{{nodeArch}}.tar.gz"
        checksum: "sha256:abc123..."
        installPath: "/usr/local"
        executable: containerd
    
    # 配置文件模板
    configTemplates:
      - name: config.toml
        path: "/etc/containerd/config.toml"
        mode: "0644"
        owner: "root:root"
        content: |
          version = 2
          [plugins]
            [plugins."io.containerd.grpc.v1.cri"]
              sandbox_image = "{{imageRegistry}}/pause:3.9"
              [plugins."io.containerd.grpc.v1.cri".containerd]
                snapshotter = "{{.Variables.snapshotter}}"
      
      - name: containerd.service
        path: "/etc/systemd/system/containerd.service"
        mode: "0644"
        owner: "root:root"
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
          Delegate=yes
          KillMode=process

          [Install]
          WantedBy=multi-user.target
    
    # 安装脚本
    installScript: |
      #!/bin/bash
      set -e
      # 集群: {{clusterName}}, 节点: {{nodeIP}} ({{nodeRole}})
      # 架构: {{nodeArch}}, 系统: {{nodeOS}} {{nodeOSVersion}}
      # 版本: {{componentVersion}}, 操作: {{action}}
      
      # 1. 环境检查
      {{if eq .nodeOS "centos"}}
      yum install -y libseccomp || true
      {{else if eq .nodeOS "ubuntu"}}
      apt-get update && apt-get install -y libseccomp2 || true
      {{end}}
      
      # 2. 停止旧服务
      systemctl stop containerd || true
      
      # 3. 备份旧版本 (仅升级时)
      {{if .isUpgrade}}
      cp {{artifact.containerd.installPath}}/bin/{{artifact.containerd.executable}} {{artifact.containerd.installPath}}/bin/{{artifact.containerd.executable}}.bak.$(date +%Y%m%d%H%M%S)
      {{end}}
      
      # 4. 安装新二进制 (tar.gz 包含 containerd, containerd-shim-runc-v2, ctr)
      tar -xzf "{{artifact.containerd.path}}" -C {{artifact.containerd.installPath}}
      chmod +x {{artifact.containerd.installPath}}/bin/{{artifact.containerd.executable}}
      
      # 5. 启动并验证
      systemctl daemon-reload && systemctl enable containerd && systemctl start containerd
      {{artifact.containerd.installPath}}/bin/{{artifact.containerd.executable}} --version
    
    # 卸载脚本
    uninstallScript: |
      #!/bin/bash
      systemctl stop containerd || true
      systemctl disable containerd || true
      rm -f {{artifact.containerd.installPath}}/bin/{{artifact.containerd.executable}} {{artifact.containerd.installPath}}/bin/containerd-shim-runc-v2 {{artifact.containerd.installPath}}/bin/ctr
      rm -f /etc/systemd/system/containerd.service
      systemctl daemon-reload
    
    # 架构与操作系统支持
    supportedArchitectures: [amd64, arm64]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]
      - name: kylin
        versions: ["V10"]
    
    # 健康检查 (安装/升级后通过 SSH 执行脚本验证服务可用性)
    healthCheck:
      enabled: true
      timeout: "2m"
      interval: "5s"
      script: |
        #!/bin/bash
        systemctl is-active containerd
        {{artifact.containerd.installPath}}/bin/{{artifact.containerd.executable}} --version | grep -q "{{componentVersion}}"
        crictl info > /dev/null 2>&1
  
  # 兼容性约束
  compatibility:
    constraints:
      - component: kubernetes
        rule: ">=1.26.0"
      - component: runc
        rule: ">=1.1.0"
  
  # 依赖关系
  dependencies:
    - name: kubernetes
      phase: Install
  
  # 升级策略
  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: Continue
```

### 4.3 ComponentVersion Helm 类型定义

```yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.11.1
spec:
  name: coredns
  type: helm
  version: v1.11.1
  
  helm:
    # Chart 来源 (支持 OCI/HTTP/本地)
    chart:
      oci:
        repository: "registry.openfuyao.cn/charts/coredns"
        tag: "v1.11.1"
    
    namespace: kube-system
    releaseName: coredns
    
    # Values 模板 (支持变量替换)
    values:
      image:
        repository: "registry.openfuyao.cn/coredns/coredns"
        tag: "{{componentVersion}}"
      replicaCount: "{{controlPlaneReplicas}}"
      service:
        clusterIP: "{{corednsClusterIP}}"
    
    # 安装策略
    strategy:
      mode: Upgrade
      wait: true
      waitTimeout: "5m"
      atomic: true
      cleanupOnFail: false
    
    # 健康检查
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          podReady:
            namespace: kube-system
            labelSelector: "k8s-app=kube-dns"
            minReady: 1
    
    # 回滚配置
    rollback:
      enabled: true
      maxHistory: 3
  
  # 兼容性约束
  compatibility:
    constraints:
      - component: kubernetes
        rule: ">=1.24.0"
  
  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

### 4.3a ComponentVersion YAML 类型定义

```yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: openfuyao-core-v26.03
spec:
  name: openfuyao-core
  type: yaml
  version: v26.03

  yaml:
    # YAML 清单文件列表
    manifests:
      - url: "https://release-repo.openfuyao.cn/manifests/openfuyao-core/v26.03/crds.yaml"
        checksum: "sha256:mno345..."
      - url: "https://release-repo.openfuyao.cn/manifests/openfuyao-core/v26.03/deployment.yaml"
        checksum: "sha256:stu901..."

    namespace: openfuyao-system
    applyStrategy: ServerSideApply
    prune: true
    pruneLabelSelector:
      app.kubernetes.io/managed-by: openfuyao-core
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          podReady:
            namespace: openfuyao-system
            labelSelector: "app.kubernetes.io/name=openfuyao-core"
            minReady: 1

  # 子组件引用 (组合关系: openfuyao-core 包含 kubernetes 和 etcd)
  subComponents:
    - name: kubernetes-master
      version: v1.29.0
    - name: kubernetes-worker
      version: v1.29.0

  upgradeStrategy:
    mode: Parallel
    failurePolicy: FailFast
    timeout: "5m"
```

### 4.4 核心组件设计

#### 4.4.1 BinaryInstaller 架构

```
┌─────────────────────────────────────────────────────────────────┐
│                      BinaryInstaller                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────────┐    ┌──────────────────┐                   │
│  │ ArtifactDownloader│    │ TemplateRenderer │                   │
│  │                  │    │                  │                   │
│  │ • HTTP 下载      │    │ • Go template    │                   │
│  │ • Checksum 校验  │    │ • 8类50+变量     │                   │
│  │ • 本地缓存       │    │ • 条件渲染       │                   │
│  │ • 架构适配       │    │ • 自定义函数     │                   │
│  └────────┬─────────┘    └────────┬─────────┘                   │
│           │                       │                              │
│           ▼                       ▼                              │
│  ┌──────────────────────────────────────────┐                   │
│  │            ConfigRenderer                 │                   │
│  │                                          │                   │
│  │ • content 模板渲染 (Go template)         │                   │
│  │ • secretRef 从 Secret 获取               │                   │
│  │ • kubeconfigTemplate 动态生成            │                   │
│  └──────────────────┬───────────────────────┘                   │
│                       │                                          │
│                       ▼                                          │
│  ┌──────────────────────────────────────────┐                   │
│  │            SSH Executor                   │                   │
│  │                                          │                   │
│  │ • 上传二进制制品                          │                   │
│  │ • 上传配置文件                            │                   │
│  │ • 执行安装脚本                            │                   │
│  │ • 收集 stdout/stderr                      │                   │
│  └──────────────────────────────────────────┘                   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

#### 4.4.2 installScript 模板变量系统

| 变量类别 | 变量示例 | 说明 |
|---------|---------|------|
| **集群信息** | `{{clusterName}}`, `{{apiServer}}`, `{{serviceCIDR}}` | 集群级别配置 |
| **节点信息** | `{{nodeIP}}`, `{{nodeArch}}`, `{{nodeOS}}`, `{{nodeCPUs}}` | 节点级别信息 |
| **版本信息** | `{{componentVersion}}`, `{{componentPreviousVersion}}` | 当前/上一版本 |
| **二进制制品** | `{{artifact.containerd.path}}`, `{{artifact.containerd.checksum}}` | 制品路径/校验和 |
| **镜像仓库** | `{{imageRegistry}}`, `{{imagePullSecret}}` | 镜像仓库配置 |
| **安装路径** | `{{artifact.<name>.installPath}}`, `{{configPath}}`, `{{logPath}}` | per-artifact 安装路径 + 组件级配置路径 |
| **操作类型** | `{{action}}`, `{{isUpgrade}}`, `{{isInstall}}` | 操作类型判断 |
| **自定义变量** | `{{.Variables.logLevel}}`, `{{.Variables.snapshotter}}` | ComponentVersion 定义 |

#### 4.4.3 configTemplates 渲染引擎

```
configTemplates 支持三种渲染模式:

1. content 模式 (Go template 渲染)
   ┌─────────────────────────────────────┐
   │ content: |                          │
   │   cluster_name: {{.clusterName}}    │
   │   api_server: {{.apiServer}}        │
   │   log_level: {{.Variables.logLevel  │
   │                | default "info"}}   │
   └─────────────────────────────────────┘

2. secretRef 模式 (从 Secret 获取)
   ┌─────────────────────────────────────┐
   │ secretRef:                          │
   │   name: bkeagent-tls                │
   │   namespace: "{{.clusterNamespace}}"│
   │   key: tls.crt                      │
   └─────────────────────────────────────┘

3. kubeconfigTemplate 模式 (动态生成)
   ┌─────────────────────────────────────┐
   │ kubeconfigTemplate:                 │
   │   clusterName: "{{.clusterName}}"   │
   │   apiServer: "{{.apiServer}}"       │
   │   caCertPath: "/etc/.../ca.crt"     │
   │   clientCertPath: "/etc/.../tls.crt"│
   └─────────────────────────────────────┘
```

#### 4.4.4 HelmInstaller 架构

```
┌─────────────────────────────────────────────────────────────────┐
│                       HelmInstaller                               │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────────┐    ┌──────────────────┐                   │
│  │ ChartFetcher     │    │ ValuesRenderer   │                   │
│  │                  │    │                  │                   │
│  │ • OCI Registry   │    │ • 模板变量替换   │                   │
│  │ • HTTP URL       │    │ • valuesFiles    │                   │
│  │ • Local Path     │    │ • 合并策略       │                   │
│  │ • 本地缓存       │    │                  │                   │
│  └────────┬─────────┘    └────────┬─────────┘                   │
│           │                       │                              │
│           └───────────┬───────────┘                              │
│                       ▼                                          │
│  ┌──────────────────────────────────────────┐                   │
│  │            Helm Action Executor           │                   │
│  │                                          │                   │
│  │ • Install / Upgrade / Uninstall / Rollback│                  │
│  │ • Wait + Atomic + MaxHistory             │                   │
│  │ • PreInstallHooks / PreUninstallHooks    │                   │
│  │ • HealthCheck (PodReady/EndpointReady)   │                   │
│  └──────────────────────────────────────────┘                   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

#### 4.4.6 YamlInstaller 架构

```
┌─────────────────────────────────────────────────────────────────┐
│                    YamlInstaller                                  │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────────┐    ┌──────────────────┐                   │
│  │ManifestDownloader│    │  YAML Parser     │                   │
│  │                  │    │                  │                   │
│  │ • ManifestStore  │    │ • 多文档解析     │                   │
│  │ • HTTP URL 下载  │    │ • GVK 识别       │                   │
│  │ • Checksum 校验  │    │ • 资源分组       │                   │
│  └────────┬─────────┘    └────────┬─────────┘                   │
│           │                       │                              │
│           ▼                       ▼                              │
│  ┌──────────────────────────────────────────┐                   │
│  │       ApplyStrategy Engine               │                   │
│  │                                          │                   │
│  │ • ServerSideApply (默认, 声明式字段管理) │                   │
│  │ • Replace (删除+重建)                    │                   │
│  │ • CreateOnly (仅创建)                    │                   │
│  └──────────────────┬───────────────────────┘                   │
│                     │                                          │
│                     ▼                                          │
│  ┌──────────────────────────────────────────┐                   │
│  │            K8s Applier                    │                   │
│  │                                          │                   │
│  │ • 应用清单到目标集群                     │                   │
│  │ • Prune 裁剪废弃资源 (按 label selector) │                   │
│  └──────────────────────────────────────────┘                   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 4.5 DAG 调度集成

#### 4.5.1 执行器注册

```go
// DAG 调度器根据组件类型选择对应执行器
switch cv.Spec.Type {
case ComponentTypeBinary:
    executor = &BinaryComponentExecutor{installer: binaryInstaller}
case ComponentTypeHelm:
    executor = &HelmComponentExecutor{installer: helmInstaller}
case ComponentTypeYAML:
    executor = &YamlComponentExecutor{installer: yamlInstaller}
case ComponentTypeInline:
    executor = &InlineComponentExecutor{factory: componentFactory}
}
```

#### 4.5.2 完整安装流程

```
用户创建 BKECluster (desiredVersion: v2.6.0)
  │
  ▼
ClusterVersionReconciler 解析 ReleaseImage v2.6.0
  │
  ├── install.components:
  │     ├── containerd/v1.7.18 (type: binary)
  │     ├── bkeagent/v2.6.0 (type: binary)
  │     ├── coredns/v1.11.1 (type: helm)
  │     ├── openfuyao-core/v26.03 (type: yaml)
  │     ├── kubernetes-master/v1.29.0 (type: inline)
  │     └── kubernetes-worker/v1.29.0 (type: inline)
  │
  ├── 构建 VersionContext (版本事实)
  │     vc.SetTarget("containerd", "v1.7.18")  → HasCurrent=false → Action=Install
  │     vc.SetTarget("coredns", "v1.11.1")     → HasCurrent=false → Action=Install
  │     vc.SetTarget("openfuyao-core", "v26.03") → HasCurrent=false → Action=Install
  │
  ├── 构建安装 DAG
  │     ┌─────────────────────────────────────────────────┐
  │     │ finalizer → ... → dryrun                        │
  │     │                    → agent (binary)             │
  │     │                    → containerd (binary)        │
  │     │                    → kubernetes-master (inline) │
  │     │                    → kubernetes-worker (inline) │
  │     │                    → coredns (helm)             │
  │     │                    → openfuyao-core (yaml)      │
  │     │                    → addon → postprocess        │
  │     └─────────────────────────────────────────────────┘
  │
  ├── DAG Scheduler 执行 (按拓扑批次)
  │     │
  │     ├── BinaryComponentExecutor 执行 containerd
  │     │     ├── VersionContext.HasCurrent("containerd")=false → Action=Install
  │     │     ├── ArtifactDownloader 下载 containerd-1.7.18-linux-amd64.tar.gz
  │     │     ├── TemplateRenderer 渲染 installScript (替换 50+ 变量)
  │     │     ├── ConfigRenderer 渲染 configTemplates (config.toml, service)
  │     │     └── SSH Executor 上传制品 + 配置 + 执行脚本
  │     │
  │     ├── HelmComponentExecutor 执行 coredns
  │     │     ├── VersionContext.HasCurrent("coredns")=false → Action=Install
  │     │     ├── ChartFetcher 从 OCI Registry 拉取 Chart
  │     │     ├── ValuesRenderer 渲染 Values (替换 {{clusterName}} 等)
  │     │     ├── Helm Action: helm install coredns --namespace kube-system
  │     │     └── HealthCheck: PodReady + EndpointReady
  │     │
  │     └── YamlComponentExecutor 执行 openfuyao-core
  │           ├── VersionContext.NeedsUpgrade("openfuyao-core")=true
  │           ├── resolveManifests(): 下载 crds.yaml + deployment.yaml
  │           ├── parseYAMLDocuments(): 解析多文档 YAML
  │           ├── ApplyWithStrategy(ServerSideApply): 应用到目标集群
  │           └── PruneResources(): 首次安装无废弃资源
  │
  └── 安装完成 → ClusterStatus = Ready
```

#### 4.5.3 完整升级流程

```
用户修改 ClusterVersion desiredVersion: v2.5.0 → v2.6.0
  │
  ▼
ClusterVersionReconciler 检测到版本变更
  │
  ├── 对比版本，构建 VersionContext
  │     ├── containerd: v1.7.15 → v1.7.18  HasCurrent=true  NeedsUpgrade=true  → Action=Upgrade
  │     ├── bkeagent: v2.5.0 → v2.6.0      HasCurrent=true  NeedsUpgrade=true  → Action=Upgrade
  │     ├── coredns: v1.10.1 → v1.11.1     HasCurrent=true  NeedsUpgrade=true  → Action=Upgrade
  │     ├── openfuyao-core: v26.01 → v26.03 HasCurrent=true NeedsUpgrade=true  → Action=Upgrade
  │     └── kubernetes-master: v1.29.0 (不变) HasCurrent=true NeedsUpgrade=false → Skip
  │
  ├── 构建升级 DAG
  │     ┌─────────────────────────────────────────────────┐
  │     │ provider → agent (binary)                       │
  │     │           → containerd (binary)                  │
  │     │           → coredns (helm)                       │
  │     │           → openfuyao-core (yaml)                │
  │     │           → etcd (inline) → kubernetes-worker    │
  │     │           → kubernetes-master (inline)           │
  │     │           → component → cluster                  │
  │     └─────────────────────────────────────────────────┘
  │
  ├── DAG Scheduler 执行 (按拓扑批次)
  │     │
  │     ├── Batch 1: provider
  │     ├── Batch 2: agent (binary) → Batch(batchSize=2) 逐批升级
  │     ├── Batch 3: containerd (binary) → Rolling 逐节点滚动升级
  │     ├── Batch 4: coredns (helm) → helm upgrade --atomic
  │     ├── Batch 5: openfuyao-core (yaml) → ServerSideApply 增量更新 + Prune 裁剪
  │     └── Batch 6: etcd → kubernetes-worker → kubernetes-master (inline)
  │
  └── 升级完成 → ClusterStatus = Ready
```

### 4.6 ReleaseImage 引用示例

```yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: ri-v2.6.0
spec:
  version: "v2.6.0"
  digest: "sha256:abc123..."
  
  install:
    components:
      - name: containerd
        version: v1.7.18
      - name: bkeagent
        version: v2.6.0
      - name: coredns
        version: v1.11.1
      - name: kube-proxy
        version: v1.29.0
      - name: openfuyao-core
        version: v26.03
      - name: kubernetes-master
        version: v1.29.0
        inline:
          handler: EnsureMasterInit
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.29.0
        inline:
          handler: EnsureWorkerJoin
          version: v1.0.0
        
  upgrade:
    components:
      - name: containerd
        version: v1.7.18
      - name: bkeagent
        version: v2.6.0
      - name: coredns
        version: v1.11.1
      - name: kube-proxy
        version: v1.29.0
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0
      - name: etcd
        version: v3.5.12
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0
```

## 5. 迁移策略

### 5.1 分阶段迁移

| 阶段 | 时间 | 内容 | 风险 | 回滚方案 |
|------|------|------|------|---------|
| **Phase 1** | 第1周 | 实现 BinaryInstaller 核心逻辑 (下载/缓存/渲染/SSH) | 低 (独立组件) | 不启用 Feature Gate |
| **Phase 2** | 第2周 | 实现 HelmInstaller 核心逻辑 (Chart/Values/Action) | 低 (独立组件) | 不启用 Feature Gate |
| **Phase 3** | 第3周 | 创建 containerd/bkeagent/coredns 的 ComponentVersion YAML | 低 (声明式配置) | 回退到旧 YAML |
| **Phase 4** | 第4周 | 集成 BinaryComponentExecutor/HelmComponentExecutor 到 DAG；`EnsureNodesEnv` 移除 `runtime` scope | 中 (需充分测试) | 关闭 Feature Gate |
| **Phase 5** | 第5周 | 灰度发布：新集群使用新路径，旧集群保持旧路径 | 中 (需要监控) | 切换回旧路径 |
| **Phase 6** | 第6周 | 移除旧 Phase 代码 (`EnsureContainerdUpgrade` 等) | 高 (需要回滚预案) | 保留旧代码分支 |

### 5.2 Feature Gate 控制

```go
const (
    BinaryComponentSupport = "BinaryComponentSupport"
    HelmComponentSupport   = "HelmComponentSupport"
)

var defaultFeatureGates = map[string]bool{
    BinaryComponentSupport: false,
    HelmComponentSupport:   false,
}
```

### 5.3 向后兼容

```go
// 兼容层：同时支持新旧两种方式
func (r *BKEClusterReconciler) executeContainerdUpgrade(ctx context.Context) error {
    if featuregate.Enabled(BinaryComponentSupport) {
        return r.executeBinaryComponent(ctx, "containerd")
    }
    return r.executeLegacyContainerdUpgrade(ctx)
}
```

## 6. 测试策略

### 6.1 单元测试

| 测试模块 | 测试场景 | 覆盖目标 |
|---------|---------|---------|
| **ArtifactDownloader** | HTTP 下载、Checksum 校验、缓存命中/未命中、架构适配 | >90% |
| **TemplateRenderer** | 8 类变量替换、条件渲染、自定义函数、错误处理 | >90% |
| **ConfigRenderer** | content 渲染、secretRef 获取、kubeconfig 生成 | >90% |
| **BinaryInstaller** | Install/Upgrade/Uninstall 完整流程、失败重试 | >85% |
| **HelmInstaller** | OCI/HTTP/本地 Chart 获取、Values 渲染、Install/Upgrade/Rollback | >85% |
| **YamlInstaller** | 清单获取、多文档解析、ServerSideApply/Replace/CreateOnly、Prune、健康检查 | >85% |
| **VersionContext** | HasCurrent/HasTarget/NeedsUpgrade 决策逻辑 | >90% |
| **BinaryComponentExecutor** | Rolling/Parallel/Batch 执行策略、FailurePolicy | >85% |

### 6.2 集成测试

| 测试场景 | 验证内容 | 预期结果 |
|---------|---------|---------|
| **全新安装 (binary)** | containerd + bkeagent 安装 | 二进制正确安装，服务启动，版本验证通过 |
| **全新安装 (helm)** | coredns + kube-proxy 安装 | Chart 正确部署，Pod Ready，Endpoint Ready |
| **全新安装 (yaml)** | openfuyao-core 安装 | YAML 清单正确应用，资源创建成功 |
| **升级 (binary)** | containerd v1.7.15 → v1.7.18 | 逐节点滚动升级，服务不中断 |
| **升级 (helm)** | coredns v1.10.1 → v1.11.1 | helm upgrade 成功，Pod 滚动更新 |
| **升级 (yaml)** | openfuyao-core v26.01 → v26.03 | ServerSideApply 增量更新，Prune 裁剪废弃资源 |
| **VersionContext 跳过** | kubernetes-master 版本不变 | NeedsUpgrade=false，组件跳过执行 |
| **回滚 (binary)** | 升级失败后执行 uninstallScript | 旧版本恢复，服务正常 |
| **回滚 (helm)** | helm upgrade 失败后 rollback | 自动回滚到上一版本 |
| **离线环境** | 无网络时使用本地缓存 | 安装/升级正常完成 |
| **多架构** | amd64 + arm64 混合集群 | 各节点下载对应架构制品 |

### 6.3 E2E 测试

| 测试场景 | 集群规模 | 验证内容 |
|---------|---------|---------|
| **小规模安装** | 1 Master + 2 Worker | 完整安装流程，所有组件正常 |
| **中规模安装** | 3 Master + 10 Worker | 并行安装性能，无资源竞争 |
| **跨版本升级** | 3 Master + 5 Worker | v2.5.0 → v2.6.0 完整升级 (含 YAML 类型组件) |
| **升级失败恢复** | 3 Master + 3 Worker | 模拟节点失败，验证 Continue/Rollback 策略 |
| **YAML Prune 验证** | 1 Master + 2 Worker | 升级后验证废弃资源被正确裁剪 |

## 7. 工作量评估与任务拆解

### 7.1 工作量评估

| 任务 | 预估工时 | 风险等级 | 依赖 |
|------|---------|---------|------|
| **BinaryInstaller 核心实现** | 5 人日 | 中 | 无 |
| **HelmInstaller 核心实现** | 5 人日 | 中 | 无 |
| **YamlInstaller 核心实现** | 5 人日 | 中 | 无 |
| **TemplateRenderer 实现** | 3 人日 | 低 | 无 |
| **ConfigRenderer 实现** | 3 人日 | 低 | TemplateRenderer |
| **ApplyStrategy 引擎实现** | 3 人日 | 中 | YamlInstaller |
| **Prune 裁剪功能实现** | 3 人日 | 中 | ApplyStrategy 引擎 |
| **PreInstallHooks 执行引擎** | 3 人日 | 中 | HelmInstaller |
| **ComponentVersion CRD 扩展** | 3 人日 | 低 | 无 |
| **VersionContext 与 ExecutionContext 实现** | 3 人日 | 中 | 无 |
| **BinaryComponentExecutor 集成** | 3 人日 | 中 | BinaryInstaller |
| **HelmComponentExecutor 集成** | 3 人日 | 中 | HelmInstaller |
| **YamlComponentExecutor 集成** | 2 人日 | 中 | YamlInstaller |
| **ComponentVersion YAML 编写** | 2 人日 | 低 | CRD 扩展 |
| **DAG 调度器适配** | 3 人日 | 低 | Executor 集成 |
| **Feature Gate 实现** | 1 人日 | 低 | 无 |
| **兼容层实现** | 3 人日 | 中 | DAG 调度器适配 |
| **错误分类与恢复机制** | 3 人日 | 中 | 核心实现完成 |
| **单元测试** | 8 人日 | 低 | 核心实现完成 |
| **集成测试** | 5 人日 | 中 | 单元测试完成 |
| **E2E 测试** | 5 人日 | 中 | 集成测试完成 |
| **迁移验证** | 3 人日 | 中 | 兼容层实现 |
| **文档编写** | 4 人日 | 低 | 无 |
| **代码审查与修复** | 4 人日 | 中 | 测试完成 |
| **总计** | **88 人日 (约 4 人月)** | | |

### 7.2 任务拆解

#### Sprint 1 (第1-2周): BinaryInstaller + YamlInstaller 核心实现

| 任务 | 负责人 | 交付物 |
|------|--------|--------|
| BinaryInstaller 结构定义 | 开发A | `pkg/binaryinstaller/installer.go` |
| ArtifactDownloader 实现 | 开发A | 下载/缓存/checksum 功能 |
| TemplateRenderer 实现 | 开发B | `pkg/binaryinstaller/template_renderer.go` |
| ConfigRenderer 实现 | 开发B | `pkg/binaryinstaller/config_renderer.go` |
| YamlInstaller 核心实现 | 开发D | `pkg/yamlinstaller/installer.go` |
| ApplyStrategy 引擎实现 | 开发D | ServerSideApply/Replace/CreateOnly |
| SSH 执行逻辑 | 开发A | 上传/执行/日志收集 |
| 单元测试 (BinaryInstaller) | 开发A+B | 测试覆盖率 >85% |

#### Sprint 2 (第3-4周): HelmInstaller + Prune + PreInstallHooks

| 任务 | 负责人 | 交付物 |
|------|--------|--------|
| HelmInstaller 结构定义 | 开发C | `pkg/helminstaller/installer.go` |
| ChartFetcher 实现 | 开发C | OCI/HTTP/本地 Chart 获取 |
| ValuesRenderer 实现 | 开发C | Values 模板渲染 |
| Helm Action Executor 实现 | 开发C | Install/Upgrade/Rollback/Uninstall |
| HealthCheck 实现 | 开发C | PodReady/EndpointReady 检查 |
| PreInstallHooks 执行引擎 | 开发C | Job 类型钩子创建/等待/清理 |
| Prune 裁剪功能实现 | 开发D | 按 label selector 裁剪废弃资源 |
| 单元测试 (HelmInstaller) | 开发C | 测试覆盖率 >85% |

#### Sprint 3 (第5-6周): DAG 集成与 Phase 迁移

| 任务 | 负责人 | 交付物 |
|------|--------|--------|
| ComponentVersion CRD 扩展 | 开发A | binary/helm/yaml/SubComponents/Resources 字段 |
| VersionContext 与 ExecutionContext | 开发A | `pkg/dagexec/context.go` |
| BinaryComponentExecutor | 开发A | `pkg/dagexec/binary_component_executor.go` |
| HelmComponentExecutor | 开发C | `pkg/dagexec/helm_component_executor.go` |
| YamlComponentExecutor 集成 | 开发D | `pkg/dagexec/yaml_component_executor.go` |
| ComponentVersion YAML 编写 | 开发B | containerd/bkeagent/coredns/openfuyao-core YAML |
| DAG 调度器适配 | 开发B | 执行器注册与调度 |
| Feature Gate 实现 | 开发A | 开关控制逻辑 |
| 兼容层实现 | 开发B | Feature Gate 双轨切换 |
| 集成测试 | 开发A+B+C+D | 安装/升级/回滚场景 |
| E2E 测试 | 开发A+B+C+D | 多场景端到端验证 |

### 7.3 里程碑

| 里程碑 | 时间 | 交付内容 | 验收标准 |
|--------|------|---------|---------|
| **M1: BinaryInstaller 完成** | 第2周末 | BinaryInstaller 核心功能 + 单元测试 | 单元测试覆盖率 >85% |
| **M2: YamlInstaller 完成** | 第2周末 | YamlInstaller + ApplyStrategy + Prune | 单元测试覆盖率 >85% |
| **M3: HelmInstaller 完成** | 第4周末 | HelmInstaller + PreInstallHooks + 单元测试 | 单元测试覆盖率 >85% |
| **M4: DAG 集成完成** | 第5周末 | Executor 集成 + VersionContext + ComponentVersion YAML | 集成测试通过 |
| **M5: Beta 发布** | 第6周末 | Feature Gate 灰度 + 兼容层 + E2E 测试 | E2E 通过率 >95% |

## 8. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **SSH 连接不稳定** | 二进制安装失败 | 中 | 重试机制 + 超时控制 + 详细错误日志 |
| **制品下载失败** | 安装阻塞 | 低 | 本地缓存 + 多源下载 + Checksum 校验 |
| **模板渲染错误** | 配置错误导致服务异常 | 中 | 渲染前校验 + DryRun 模式 + 详细错误信息 |
| **Helm Chart 不兼容** | 组件部署失败 | 低 | 版本约束校验 + 健康检查 + 自动回滚 |
| **迁移期间行为不一致** | 新旧路径行为差异 | 中 | Feature Gate 控制 + 充分测试 + 灰度发布 |
| **离线环境缓存不足** | 无法安装/升级 | 低 | 预下载机制 + 本地路径支持 + 缓存清理策略 |

## 9. 收益评估

| 维度 | 当前 | 重构后 | 提升 |
|------|------|--------|------|
| **组件类型支持** | inline + manifest | inline + manifest + binary + helm + yaml | 完整覆盖 |
| **配置管理** | 硬编码在脚本中 | configTemplates 声明式 | 可维护性↑ |
| **模板变量** | 仅 {{arch}} | 8类50+变量 | 灵活性↑ |
| **条件渲染** | 无 | Go template 完整支持 | 表达能力↑ |
| **Helm 支持** | 无 | OCI/HTTP/本地 Chart | 生态兼容↑ |
| **YAML 清单应用** | 无 | ServerSideApply/Replace/CreateOnly + Prune | 声明式资源管理↑ |
| **版本决策** | IsUpgrade bool | VersionContext 携带版本事实 | 声明式协调↑ |
| **新增组件** | 修改代码 + 新增 Phase | 添加 ComponentVersion YAML | 零代码侵入 |
| **安装/升级一致性** | 不同的 Phase 实现 | 统一的 Installer | 逻辑复用 |
| **架构适配** | 硬编码在代码中 | 模板变量 `{{nodeArch}}` | 声明式配置 |
| **回滚能力** | 无 | uninstallScript + Helm rollback | 可回滚 |

## 10. 附录

### 10.1 参考文档

- KEP-5: 基于 ClusterVersion/ReleaseImage/UpgradePath 的声明式集群版本升级
- ComponentVersion CRD 定义
- ReleaseImage CRD 定义
- DAG 调度器设计文档
- Helm Action API: https://pkg.go.dev/helm.sh/helm/v3/pkg/action

### 10.2 术语表

| 术语 | 定义 |
|------|------|
| **BinaryInstaller** | 负责二进制组件下载、渲染、安装、健康检查的安装器 |
| **HelmInstaller** | 负责 Helm Chart 获取、渲染、部署的安装器 |
| **YamlInstaller** | 负责 YAML 清单获取、解析、应用、裁剪、健康检查的安装器 |
| **VersionContext** | 携带组件版本事实（已安装版本、目标版本），Executor 据此自主决定操作类型 |
| **ApplyStrategy** | YAML 清单应用策略：ServerSideApply/Replace/CreateOnly |
| **Prune** | 按标签选择器裁剪不再需要的 Kubernetes 资源 |
| **SubComponents** | 组件的组合关系（父子包含），区别于 Dependencies（执行顺序） |
| **configTemplates** | 配置文件模板系统，支持 Go template/Secret/kubeconfig |
| **installScript** | 安装脚本模板，支持 8 类 50+ 变量和条件渲染 |
| **Artifact** | 二进制制品，包含 URL、Checksum、安装路径等信息 |
| **ComponentVersion** | 组件版本 CRD，定义组件的类型、配置、依赖等 |
| **ReleaseImage** | 发布版本清单 CRD，定义安装和升级的组件列表 |

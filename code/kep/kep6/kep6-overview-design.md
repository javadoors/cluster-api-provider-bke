# KEP-6 概要设计：基于 ReleaseImage 的二进制与 Helm 组件声明式管理方案

**文档版本**: v1.0  
**状态**: Draft  
**作者**: openFuyao Team  
**创建日期**: 2026-07-06  
**依赖**: KEP-5 (ClusterVersion/ReleaseImage/UpgradePath)

---

## 目录

1. [摘要](#1-摘要)
2. [动机](#2-动机)
3. [范围与约束](#3-范围与约束)
4. [整体架构设计](#4-整体架构设计)
5. [ComponentVersion CRD 设计](#5-componentversion-crd-设计)
6. [核心安装器设计](#6-核心安装器设计)
7. [模板变量系统](#7-模板变量系统)
8. [DAG 调度集成](#8-dag-调度集成)
9. [迁移策略](#9-迁移策略)
10. [测试策略](#10-测试策略)
11. [工作量评估](#11-工作量评估)
12. [风险与缓解](#12-风险与缓解)
13. [收益评估](#13-收益评估)
14. [附录](#14-附录)

---

## 1. 摘要

本方案将 Containerd 与 BKEAgent 组件从硬编码 Phase 重构为 ReleaseImage 中声明式的二进制组件 (`ComponentTypeBinary`)，同时新增 Helm 组件类型 (`ComponentTypeHelm`) 和 YAML 清单组件类型 (`ComponentTypeYAML`) 支持。

**核心设计**：
- 引入 `BinaryInstaller`、`HelmInstaller` 和 `YamlInstaller` 三个统一安装器
- 配合 `configTemplates` 配置模板引擎和 `installScript` 模板变量系统
- 实现组件安装/升级的声明式管理
- YAML 类型组件通过 `VersionContext` 携带版本事实，Executor 据此自主决定操作类型（Install/Upgrade/Skip）

**架构特点**：
- 彻底解耦，新增二进制/Helm/YAML 组件只需添加 ComponentVersion YAML
- 核心代码零侵入，符合开闭原则

---

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状 | 影响 |
|------|------|------|
| **版本硬编码** | Containerd/BKEAgent 版本散落在 `BKECluster.Spec` 各字段 | 无法通过 ReleaseImage 统一管理，版本追溯困难 |
| **安装/升级分离** | 安装和升级使用不同 Phase，逻辑重复 | 代码冗余，维护成本高，行为不一致 |
| **无二进制组件支持** | `ComponentTypeBinary` 已定义但未实现 | 新增二进制组件需修改核心调度代码 |
| **架构适配硬编码** | bkeagent 的架构适配写在代码中 | 新增架构需改代码发版 |
| **Helm 组件缺失** | 无 Helm 类型组件支持 | CoreDNS/kube-proxy 等组件无法通过 Helm 管理 |
| **配置管理硬编码** | 配置文件内容硬编码在脚本中 | 配置变更需改代码，无法声明式管理 |

### 2.2 目标

1. 实现 `ComponentTypeBinary` 类型组件的完整支持，包括制品下载、模板渲染、SSH 安装、健康检查
2. 实现 `ComponentTypeHelm` 类型组件的完整支持，包括 OCI/HTTP/本地 Chart 获取、Values 渲染、健康检查
3. 实现 `ComponentTypeYAML` 类型组件的完整支持，包括清单获取、多文档解析、ServerSideApply/Replace/CreateOnly 三种应用策略、Prune 裁剪、健康检查
4. 设计 `configTemplates` 配置模板引擎，支持 Go template、Secret 引用、动态 kubeconfig 生成
5. 设计 `installScript` 模板变量系统，支持 8 类 50+ 变量和条件渲染
6. 引入 `VersionContext` 携带版本事实，Executor 据此自主决定操作类型，符合 Kubernetes 声明式协调模式
7. 将 Containerd/BKEAgent 从硬编码 Phase 迁移到 ReleaseImage 声明式管理
8. 提供平滑迁移方案，Feature Gate 控制，新旧双轨运行

### 2.3 非目标

1. 不修改现有 inline Phase 的执行逻辑，仅新增 Binary/Helm/YAML 类型支持
2. 不替换现有 SSH 推送机制，BinaryInstaller 复用现有 `bkessh.MultiCli`
3. 不在此阶段实现组件制品的自动构建与发布流程
4. 不重写 DAG 调度器核心逻辑，仅新增执行器

---

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| CRD 扩展 | `ComponentVersion` 新增 `binary`、`helm`、`yaml`、`selector` 类型的完整字段定义 |
| BinaryInstaller | 二进制制品下载、缓存、模板渲染、SSH 安装、健康检查、卸载 |
| HelmInstaller | Chart 获取 (OCI/HTTP/本地)、Values 渲染、Install/Upgrade/Rollback/Uninstall |
| YamlInstaller | YAML 清单获取、多文档解析、ServerSideApply/Replace/CreateOnly 应用策略、Prune 裁剪、健康检查 |
| configTemplates | Go template 渲染、Secret 引用、动态 kubeconfig 生成、forEach 动态多文件生成 |
| installScript | 8 类 50+ 模板变量、条件渲染、自定义变量 |
| DAG 集成 | BinaryComponentExecutor、HelmComponentExecutor、YamlComponentExecutor 集成到 DAG 调度器 |
| VersionContext | 携带版本事实（已安装版本、目标版本），Executor 自主决定 Install/Upgrade/Skip |
| Phase 迁移 | 移除 `EnsureContainerdUpgrade`、`EnsureBKEAgent`、`EnsureAgentUpgrade` 硬编码逻辑；`EnsureNodesEnv` 移除 `runtime` scope |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 必须支持从现有硬编码 Phase 平滑迁移，Feature Gate 控制开关 |
| **离线环境** | 二进制制品和 Helm Chart 支持本地缓存，支持断网安装 |
| **架构支持** | 必须支持 amd64 和 arm64 架构 |
| **操作系统支持** | 必须支持 CentOS 7/8、Ubuntu 20.04/22.04 |
| **接口复用** | 复用现有 `NeedExecute()` 接口，不新增升级决策接口 |
| **安全性** | 制品必须支持 checksum 校验，敏感配置通过 Secret 引用 |

---

## 4. 整体架构设计

### 4.1 系统架构图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        BKECluster                                        │
│  spec.desiredVersion: v2.6.0                                             │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                      ReleaseImage                                        │
│  spec.install.components: [container-runtime/v1.0.0, bkeagent/v2.6.0]   │
│  spec.upgrade.components: [container-runtime/v1.0.0, bkeagent/v2.6.0]   │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │ 按 (name, version) 定位
                           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                    bke-manifests (ComponentVersion)                      │
│                                                                         │
│  container-runtime/v1.0.0/component.yaml  ← type: selector              │
│    └── subComponents: [containerd, docker, cri-dockerd]                 │
│                                                                         │
│  containerd/v1.7.18/component.yaml        ← type: binary                │
│    ├── binary.artifacts: [containerd]                                   │
│    ├── binary.configTemplates: [config.toml, service, hosts.toml]       │
│    └── binary.installScript: (带 50+ 模板变量)                          │
│                                                                         │
│  coredns/v1.11.1/component.yaml           ← type: helm                  │
│    ├── helm.chart.oci: registry/charts/coredns                          │
│    ├── helm.values: (带模板变量)                                        │
│    └── helm.healthCheck: PodReady + EndpointReady                       │
│                                                                         │
│  openfuyao-core/v26.03/component.yaml     ← type: yaml                  │
│    ├── yaml.manifests: [crds.yaml, deployment.yaml]                     │
│    ├── yaml.applyStrategy: ServerSideApply                              │
│    └── yaml.prune: true (按 label selector 裁剪废弃资源)                │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                            DAG Scheduler                                 │
│                                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌────────────┐  │
│  │   Binary     │  │    Helm      │  │   Inline     │  │   YAML     │  │
│  │  Component   │  │   Component  │  │   Component  │  │  Component │  │
│  │  Executor    │  │   Executor   │  │   Executor   │  │  Executor  │  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └─────┬──────┘  │
│         │                 │                 │                │         │
│         ▼                 ▼                 ▼                ▼         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────┐  │
│  │   Binary     │  │    Helm      │  │  Component   │  │   Yaml   │  │
│  │  Installer   │  │  Installer   │  │  Factory     │  │ Installer│  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └────┬─────┘  │
│         │                 │                 │               │         │
│         ▼                 ▼                 ▼               ▼         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────┐  │
│  │  SSH Client  │  │ Helm SDK     │  │ Phase        │  │ K8s      │  │
│  │  (bkessh)    │  │ (helm/v3)    │  │ Execute()    │  │ Client   │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  └──────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

### 4.2 组件交互关系

**核心流程**：
1. BKECluster Reconciler 解析 ReleaseImage
2. 加载 ComponentVersion（从 bke-manifests）
3. 构建 DAG（根据依赖关系）
4. DAG Scheduler 调度执行
5. 根据组件类型分发到对应 Executor
6. Executor 调用 Installer 完成安装/升级

**关键设计点**：
- **类型分发**：根据 `cv.Spec.Type` 选择对应的 Executor
- **执行器注册**：每个 Executor 实现 `ComponentExecutor` 接口
- **依赖注入**：Executor 通过构造函数注入所需的 Installer/Applier

---

## 5. ComponentVersion CRD 设计

### 5.1 ComponentVersionSpec 类型定义

```go
// ComponentVersionSpec 定义组件版本规格
type ComponentVersionSpec struct {
    // 组件名称
    Name string `json:"name"`
    
    // 组件类型: yaml, helm, inline, binary, selector
    Type ComponentType `json:"type"`
    
    // 组件版本
    Version string `json:"version"`
    
    // Binary 类型配置 (type=binary 时必填)
    Binary *BinarySpec `json:"binary,omitempty"`
    
    // Helm 类型配置 (type=helm 时必填)
    Helm *HelmSpec `json:"helm,omitempty"`
    
    // YAML 类型配置 (type=yaml 时必填)
    YAML *YAMLSpec `json:"yaml,omitempty"`
    
    // Inline 类型配置 (type=inline 时必填)
    Inline *InlineSpec `json:"inline,omitempty"`
    
    // 兼容性约束
    Compatibility CompatibilitySpec `json:"compatibility,omitempty"`
    
    // 依赖关系
    Dependencies []Dependency `json:"dependencies,omitempty"`
    
    // 子组件列表
    // type=yaml 时: 全包含语义, 所有子组件都会被安装
    // type=selector 时: 互斥选择语义, DAG 构建期评估 condition 选择一个子组件
    SubComponents []SubComponent `json:"subComponents,omitempty"`
    
    // 升级策略
    UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
    
    // 节点过滤策略 (仅 Binary 组件使用，安装和升级共用)
    NodeFilter *NodeFilterSpec `json:"nodeFilter,omitempty"`
    
    // Kubernetes 资源定义列表
    Resources []ResourceSpec `json:"resources,omitempty"`
}

// ComponentType 定义组件类型
type ComponentType string

const (
    ComponentTypeYAML     ComponentType = "yaml"
    ComponentTypeHelm     ComponentType = "helm"
    ComponentTypeInline   ComponentType = "inline"
    ComponentTypeBinary   ComponentType = "binary"
    ComponentTypeSelector ComponentType = "selector" // 互斥选择器
)
```

**设计思路**：
- 直接在 v1alpha1 上扩展，不引入 v1alpha2
- 新字段全部 `omitempty` + 指针类型，旧 YAML 不填则为 nil
- `Type` 字段 enum 已含 `binary`/`helm`/`yaml`/`selector`，无需改 enum
- 零迁移风险，最大化复用现有类型文件与 deepcopy 生成代码

### 5.2 Binary 类型字段定义

```go
// BinarySpec 定义二进制组件规格
type BinarySpec struct {
    // 自定义变量 (可覆盖默认值)
    Variables map[string]string `json:"variables,omitempty"`
    
    // 二进制制品列表
    Artifacts []ArtifactSpec `json:"artifacts"`
    
    // 配置文件模板列表
    ConfigTemplates []ConfigTemplateSpec `json:"configTemplates,omitempty"`
    
    // 安装脚本 (支持 Go template 语法)
    InstallScript string `json:"installScript"`
    
    // 卸载脚本 (支持 Go template 语法)
    UninstallScript string `json:"uninstallScript,omitempty"`
    
    // 支持的架构列表
    SupportedArchitectures []string `json:"supportedArchitectures"`
    
    // 支持的操作系统列表
    SupportedOS []OSSpec `json:"supportedOS"`
    
    // 默认配置路径 (组件级共享)
    DefaultConfigPath string `json:"defaultConfigPath,omitempty"`
    
    // 默认日志路径 (组件级共享)
    DefaultLogPath string `json:"defaultLogPath,omitempty"`
    
    // 默认数据路径 (组件级共享)
    DefaultDataPath string `json:"defaultDataPath,omitempty"`
    
    // 健康检查配置
    HealthCheck *BinaryHealthCheckSpec `json:"healthCheck,omitempty"`
}

// ArtifactSpec 定义二进制制品规格
type ArtifactSpec struct {
    // 制品名称
    Name string `json:"name"`
    
    // 制品 URL (支持模板变量)
    URL string `json:"url"`
    
    // 制品校验和 (格式: sha256:xxx)
    Checksum string `json:"checksum"`
    
    // 安装路径
    InstallPath string `json:"installPath"`
}

// ConfigTemplateSpec 定义配置文件模板规格
type ConfigTemplateSpec struct {
    // 模板名称
    Name string `json:"name"`
    
    // 静态目标路径 (与 PathTemplate 互斥)
    Path string `json:"path,omitempty"`
    
    // 动态路径模板 (Go template, 与 ForEach 配合使用)
    PathTemplate string `json:"pathTemplate,omitempty"`
    
    // 迭代源路径 (点分隔, 从 TemplateContext 中解析)
    ForEach string `json:"forEach,omitempty"`
    
    // 文件权限 (如 "0644")
    Mode string `json:"mode,omitempty"`
    
    // 模板内容 (Go template 语法)
    Content string `json:"content,omitempty"`
    
    // Secret 引用
    SecretRef *SecretRefSpec `json:"secretRef,omitempty"`
    
    // Kubeconfig 模板
    KubeconfigTemplate *KubeconfigTemplateSpec `json:"kubeconfigTemplate,omitempty"`
    
    // 生成条件 (Go Template 表达式)
    Condition string `json:"condition,omitempty"`
}
```

**设计思路**：
- **Artifacts**：支持多个制品，每个制品独立指定 URL、Checksum、安装路径
- **ConfigTemplates**：支持三种渲染模式（Content/Secret/Kubeconfig）
- **forEach**：支持动态多文件生成（如 hosts.toml 按 registry 生成多个）
- **condition**：支持条件渲染（如离线模式生成 hosts.toml，在线模式跳过）

### 5.3 Helm 类型字段定义

```go
// HelmSpec 定义 Helm 组件规格
type HelmSpec struct {
    // Chart 配置
    Chart ChartSpec `json:"chart"`
    
    // 命名空间
    Namespace string `json:"namespace"`
    
    // Release 名称
    ReleaseName string `json:"releaseName"`
    
    // Values 模板 (支持模板变量)
    Values map[string]interface{} `json:"values,omitempty"`
    
    // 自定义 Values 文件列表
    ValuesFiles []string `json:"valuesFiles,omitempty"`
    
    // 安装策略
    Strategy HelmStrategySpec `json:"strategy,omitempty"`
    
    // 健康检查配置
    HealthCheck HealthCheckSpec `json:"healthCheck,omitempty"`
    
    // 回滚配置
    Rollback RollbackSpec `json:"rollback,omitempty"`
    
    // 卸载配置
    Uninstall UninstallSpec `json:"uninstall,omitempty"`
    
    // 前置安装钩子
    PreInstallHooks []HookSpec `json:"preInstallHooks,omitempty"`
    
    // 前置卸载钩子
    PreUninstallHooks []HookSpec `json:"preUninstallHooks,omitempty"`
}

// ChartSpec 定义 Chart 规格
type ChartSpec struct {
    // OCI Registry 配置
    OCI *OCIChartSpec `json:"oci,omitempty"`
    
    // HTTP URL
    URL string `json:"url,omitempty"`
    
    // 本地路径
    LocalPath string `json:"localPath,omitempty"`
    
    // 校验和
    Checksum string `json:"checksum,omitempty"`
}

// HelmStrategySpec 定义 Helm 安装策略
type HelmStrategySpec struct {
    // 安装模式: Install / Upgrade / Rollback (空=自动判定)
    Mode string `json:"mode,omitempty"`
    
    // 是否等待就绪 (对应 helm --wait)
    Wait bool `json:"wait,omitempty"`
    
    // 等待超时时间 (对应 helm --timeout)
    WaitTimeout string `json:"waitTimeout,omitempty"`
    
    // 是否原子操作 (对应 helm --atomic, 失败时 Helm SDK 自动回滚)
    Atomic bool `json:"atomic,omitempty"`
    
    // 失败时是否清理 (对应 helm --cleanup-on-fail)
    CleanupOnFail bool `json:"cleanupOnFail,omitempty"`
}
```

**设计思路**：
- **多来源支持**：OCI Registry、HTTP URL、本地路径三种 Chart 获取方式
- **原子操作**：通过 `atomic: true` 配置，失败时自动回滚
- **Hooks**：支持 PreInstallHooks 和 PreUninstallHooks，用于前置/后置处理

### 5.4 YAML 类型字段定义

```go
// YAMLSpec 定义 YAML 清单组件规格
type YAMLSpec struct {
    // YAML 清单文件列表 (外部 URL 引用)
    Manifests []ManifestRef `json:"manifests"`
    
    // 部署目标命名空间
    Namespace string `json:"namespace,omitempty"`
    
    // 应用策略: ServerSideApply, Replace, CreateOnly
    ApplyStrategy string `json:"applyStrategy,omitempty"`
    
    // 是否启用裁剪 (按 label selector 删除不再需要的资源)
    Prune bool `json:"prune,omitempty"`
    
    // 裁剪使用的标签选择器
    PruneLabelSelector map[string]string `json:"pruneLabelSelector,omitempty"`
    
    // 健康检查配置
    HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`
}

// ManifestRef 定义 YAML 清单文件引用
type ManifestRef struct {
    // 清单文件 URL 或路径
    URL string `json:"url"`
    
    // 校验和
    Checksum string `json:"checksum,omitempty"`
}
```

**设计思路**：
- **三种应用策略**：ServerSideApply（默认）、Replace、CreateOnly
- **Prune 裁剪**：按 label selector 删除不再需要的资源
- **健康检查**：支持 PodReady、EndpointReady、Custom 三种检查类型

### 5.5 Selector 类型字段定义

```go
// SubComponent 定义子组件引用
type SubComponent struct {
    // 子组件名称
    Name string `json:"name"`
    
    // 子组件版本
    Version string `json:"version"`

    // 生成条件 (Go Template 表达式)
    // 仅 type=selector 时使用: DAG 构建期评估, condition 为真的子组件纳入 DAG
    // type=yaml 等其他类型时忽略此字段 (全包含语义不变)
    // 示例: '{{.ContainerRuntimeCRI == "containerd"}}'
    Condition string `json:"condition,omitempty"`
}
```

**设计思路**：
- **互斥选择器**：从多个候选组件中按 condition 选择一个
- **典型用例**：容器运行时（containerd 或 docker）
- **subComponents 语义**：
  - `type=yaml`：全包含语义，所有子组件都安装
  - `type=selector`：互斥选择语义，评估 condition 选择一个子组件

### 5.6 NodeFilterSpec 节点过滤策略

```go
// NodeFilterSpec 定义 Binary 组件的节点过滤策略
type NodeFilterSpec struct {
    // 目标节点角色列表
    // 空或不填 = 所有角色
    // 示例: ["master", "worker"], ["etcd"]
    Roles []string `json:"roles,omitempty"`
    
    // 节点标签选择器
    // 仅选择标签完全匹配的节点 (等值匹配)
    // 示例: {"gpu": "true", "node-pool": "compute"}
    MatchLabels map[string]string `json:"matchLabels,omitempty"`
    
    // 是否跳过已完成的节点 (per-node 幂等)
    // true: 检查 NodeComponentStatuses[nodeIP].Version == target → 跳过
    // false: 对所有节点执行，不检查 per-node 状态
    // 默认: true
    SkipCompleted *bool `json:"skipCompleted,omitempty"`
    
    // 是否排除预约添加的节点
    // 默认: true
    ExcludeAppointment *bool `json:"excludeAppointment,omitempty"`
}
```

**设计思路**：
- **为什么放在 ComponentVersionSpec 顶层**：安装和升级都需要节点过滤，不应绑定到"升级策略"语义中
- **为什么仅用于 Binary 组件**：Binary 组件直接在节点上 SSH 执行，需要 Controller 选择目标节点；Helm/YAML 组件部署到集群，节点调度由 K8s Scheduler 通过 nodeSelector 处理

---

## 6. 核心安装器设计

### 6.1 BinaryInstaller 架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│                            BinaryInstaller                               │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                    ArtifactDownloader                            │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │
│  │  │  HTTP Client │  │ Cache Manager│  │ Checksum Verifier   │   │   │
│  │  │  (下载制品)   │  │ (本地缓存)   │  │ (校验和验证)         │   │   │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                     TemplateRenderer                             │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │
│  │  │  Go Template │  │ FuncMap      │  │ Variable Resolver   │   │   │
│  │  │  (模板解析)   │  │ (自定义函数)  │  │ (变量解析)          │   │   │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                      ConfigRenderer                              │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │
│  │  │ Content Mode │  │ Secret Mode  │  │ Kubeconfig Mode     │   │   │
│  │  │ (模板渲染)    │  │ (Secret获取) │  │ (动态生成)           │   │   │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                       SSH Executor                               │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │
│  │  │ File Upload  │  │ Script Exec  │  │ Result Collector    │   │   │
│  │  │ (文件上传)    │  │ (脚本执行)   │  │ (结果收集)           │   │   │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                     HealthChecker (SSH)                          │   │
│  │  SSH 执行健康检查脚本, 退出码 0=健康                              │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**核心接口**：

```go
// BinaryInstaller 二进制组件安装器
type BinaryInstaller struct {
    client         client.Client
    sshExecutor    SSHExecutor
    cacheDir       string
    httpClient     *http.Client
    cache          *ArtifactCache
    renderer       *TemplateRenderer
    configRenderer *ConfigRenderer
    logger         *bkev1beta1.BKELogger
}

// InstallOptions 安装选项
type InstallOptions struct {
    Component   *configv1alpha1.ComponentVersion
    TemplateCtx manifest.TemplateContext
    Action      BinaryAction  // Install / Upgrade / Uninstall
    Timeout     string
    RetryCount  int
}

// Install 执行安装/升级/卸载
func (i *BinaryInstaller) Install(ctx context.Context, opts InstallOptions) error
```

**执行流程**：
1. 通过 SSH 发现节点架构（`uname -m`）
2. 下载二进制制品（检查缓存 → 解析 URL 模板 → 下载制品）
3. 校验 Checksum
4. 渲染安装脚本（使用 TemplateRenderer）
5. 渲染配置文件模板（使用 ConfigRenderer）
6. SSH 上传制品和配置
7. 执行安装脚本
8. 健康检查（SSH 执行脚本，退出码 0=健康）

### 6.2 HelmInstaller 架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│                             HelmInstaller                                │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                        ChartFetcher                              │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │
│  │  │  OCI Client  │  │  HTTP Client │  │  Local Loader       │   │   │
│  │  │  (OCI拉取)   │  │  (HTTP下载)   │  │  (本地加载)          │   │   │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                       ValuesRenderer                             │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │
│  │  │  Template    │  │  Values      │  │  Merge Strategy     │   │   │
│  │  │  Resolver    │  │  File Loader │  │  (合并策略)          │   │   │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                    Helm Action Executor                          │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │
│  │  │  Install     │  │  Upgrade     │  │  Rollback           │   │   │
│  │  │  Action      │  │  Action      │  │  Action             │   │   │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │
│  │  ┌──────────────┐  ┌──────────────┐                             │   │
│  │  │  Uninstall   │  │  Wait/Atomic │                             │   │
│  │  │  Action      │  │  Control     │                             │   │
│  │  └──────────────┘  └──────────────┘                             │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                       HealthChecker                              │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │
│  │  │  PodReady    │  │  Endpoint    │  │  Custom Check       │   │   │
│  │  │  Check       │  │  Ready Check │  │                     │   │   │
│  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**核心接口**：

```go
// HelmInstaller Helm 组件安装器
type HelmInstaller struct {
    client     client.Client
    restConfig *rest.Config
    cacheDir   string
    httpClient *http.Client
    logger     *bkev1beta1.BKELogger
}

// InstallOptions 安装选项
type InstallOptions struct {
    Component   *configv1alpha1.ComponentVersion
    TemplateCtx manifest.TemplateContext
    Action      HelmAction  // Install / Upgrade / Rollback / Uninstall
    Timeout     string
}

// Install 执行安装/升级/回滚/卸载
func (i *HelmInstaller) Install(ctx context.Context, opts InstallOptions) error
```

**执行流程**：
1. 获取 Chart（OCI Registry / HTTP URL / Local Path）
2. 校验 Chart Checksum
3. 渲染 Values（使用 TemplateRenderer）
4. 加载自定义 Values 文件
5. 合并 Values
6. 执行 Helm Action（Install / Upgrade / Rollback / Uninstall）
7. 健康检查（PodReady / EndpointReady / Custom）

### 6.3 YamlInstaller 架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│                            YamlInstaller                                 │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌──────────────────┐    ┌──────────────────┐                          │
│  │ManifestDownloader│    │  YAML Parser     │                          │
│  │                  │    │                  │                          │
│  │ • ManifestStore  │    │ • 多文档解析      │                          │
│  │ • bundle文件加载  │    │ • GVK 识别       │                          │
│  │ • 内联Resources  │    │ • 资源分组        │                          │
│  └────────┬─────────┘    └────────┬─────────┘                          │
│           │                       │                                    │
│           ▼                       ▼                                    │
│  ┌──────────────────────────────────────────┐                          │
│  │       ApplyStrategy Engine               │                          │
│  │                                          │                          │
│  │ • ServerSideApply (默认, 声明式字段管理)   │                          │
│  │ • Replace (删除+重建)                    │                          │
│  │ • CreateOnly (仅创建)                    │                          │
│  └──────────────────┬───────────────────────┘                          │
│                     │                                                  │
│                     ▼                                                  │
│  ┌──────────────────────────────────────────┐                          │
│  │            K8s Applier                   │                          │
│  │                                          │                          │
│  │ • 应用清单到目标集群                      │                          │
│  │ • Prune 裁剪废弃资源 (按 label selector)  │                          │
│  └──────────────────────────────────────────┘                          │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**核心接口**：

```go
// YamlInstaller YAML 组件安装器
type YamlInstaller struct {
    store   manifest.Store
    applier manifest.Applier
    logger  *bkev1beta1.BKELogger
}

// Apply 执行清单应用
func (i *YamlInstaller) Apply(ctx context.Context, cv *configv1alpha1.ComponentVersion, execCtx *ExecutionContext) error
```

**执行流程**：
1. 加载清单（从 ManifestStore / URL / 内联 Resources）
2. 解析多文档 YAML
3. 应用清单（ServerSideApply / Replace / CreateOnly）
4. 健康检查（PodReady / EndpointReady / Custom）
5. Prune 裁剪（按 label selector 删除废弃资源）

---

## 7. 模板变量系统

### 7.1 TemplateContext 扩展

```go
// TemplateContext 模板上下文
type TemplateContext struct {
    // 基础字段
    ClusterName       string
    Namespace         string
    KubernetesVersion string
    OpenFuyaoVersion  string
    
    // 完整配置引用
    Config            *confv1beta1.BKEConfig
    
    // 集群扩展信息
    APIServer         string
    ServiceCIDR       string
    PodCIDR           string
    DNSDomain         string
    
    // 节点基础信息
    NodeIP            string
    NodeHostname      string
    NodeRole          string
    NodeArch          string  // SSH 发现后填入
    
    // 版本信息
    ComponentVersion          string
    ComponentPreviousVersion  string
    
    // 制品信息
    Artifacts         map[string]*ArtifactInfo
    
    // 镜像仓库
    ImageRegistry     string
    ImagePullSecret   string
    
    // 组件级路径
    ConfigPath        string
    LogPath           string
    DataPath          string
    
    // 操作类型
    Action            string  // "Install" / "Upgrade" / "Uninstall"
    IsUpgrade         bool
    
    // 自定义变量
    Variables         map[string]string
    
    // 组件变量
    ComponentVariables map[string]string
}
```

### 7.2 模板变量分类

| 变量类别 | 变量示例 | 说明 |
|---------|---------|------|
| **集群信息** | `{{clusterName}}`, `{{apiServer}}`, `{{serviceCIDR}}` | 集群级别配置 |
| **节点信息** | `{{nodeIP}}`, `{{nodeArch}}`, `{{nodeRole}}` | 节点级别信息 |
| **版本信息** | `{{componentVersion}}`, `{{componentPreviousVersion}}` | 当前/上一版本 |
| **二进制制品** | `{{artifact.containerd.path}}`, `{{artifact.containerd.checksum}}` | 制品路径/校验和 |
| **镜像仓库** | `{{imageRegistry}}`, `{{imagePullSecret}}` | 镜像仓库配置 |
| **安装路径** | `{{artifact.<name>.installPath}}`, `{{configPath}}`, `{{logPath}}` | per-artifact 安装路径 + 组件级配置路径 |
| **操作类型** | `{{action}}`, `{{isUpgrade}}`, `{{isInstall}}` | 操作类型判断 |
| **自定义变量** | `{{.Variables.logLevel}}`, `{{.Variables.snapshotter}}` | ComponentVersion 定义 |

### 7.3 自定义函数

```go
// TemplateRenderer 模板渲染器
type TemplateRenderer struct {
    funcMap template.FuncMap
}

// 自定义函数列表
funcMap := template.FuncMap{
    // 字符串函数
    "upper":   strings.ToUpper,
    "lower":   strings.ToLower,
    "trim":    strings.TrimSpace,
    "replace": strings.ReplaceAll,
    
    // 条件函数
    "eq": func(a, b interface{}) bool { ... },
    "ne": func(a, b interface{}) bool { ... },
    "gt": func(a, b interface{}) bool { ... },
    "ge": func(a, b interface{}) bool { ... },
    "lt": func(a, b interface{}) bool { ... },
    "le": func(a, b interface{}) bool { ... },
    
    // 默认值函数
    "default": func(def, val interface{}) interface{} { ... },
    
    // 路径函数
    "joinPath": filepath.Join,
    "base":     filepath.Base,
    "dir":      filepath.Dir,
    
    // 时间函数
    "now":  time.Now,
    "date": func(format string) string { ... },
    
    // 版本函数
    "semver": func(v string) string { ... },
}
```

---

## 8. DAG 调度集成

### 8.1 执行器注册

```go
// ExecutorRegistry 执行器注册表
type ExecutorRegistry struct {
    executors map[string]ComponentExecutor
}

// Register 注册执行器
func (r *ExecutorRegistry) Register(componentType string, executor ComponentExecutor)

// Get 获取执行器
func (r *ExecutorRegistry) Get(componentType string) (ComponentExecutor, error)
```

**设计思路**：
- 引入 `ExecutorRegistry` 注册表后，新增类型只需调用 `registry.Register()` 注册新执行器
- Scheduler 代码无需修改，符合开闭原则
- 注册表还支持按需注入：Feature Gate OFF 时不注册 Binary/Helm/YAML 执行器

### 8.2 Selector 展开流程

```
BuildDAGFromBundle 遍历 ReleaseImage.components
    │
    │ 遇到 container-runtime/v1.0.0
    ▼
加载 ComponentVersion (cv.Spec.Type == "selector")
    │
    ▼
读取 ContainerRuntimeCRI (从 ExecutionContext.TemplateContext.Variables)
    │
    ▼
遍历 cv.Spec.SubComponents，评估每个 sub.Condition
    │
    ├─ CRI=containerd → 纳入 containerd (binary)
    │
    └─ CRI=docker → 纳入 docker (binary) + cri-dockerd (binary)
```

**设计思路**：
- Selector 类型不产生 DAG 节点，在 DAG 构建期展开为具体子组件
- 展开规则基于 `subComponents[].condition` 评估
- 依赖定义在具体组件中，不在 selector 的 `subComponents` 中定义

### 8.3 核心接口定义

```go
// ComponentExecutor 组件执行器接口
type ComponentExecutor interface {
    ExecuteComponent(ctx context.Context, node *topology.ComponentNode, execCtx *ExecutionContext) error
}

// ExecutionContext 执行上下文
type ExecutionContext struct {
    OldCluster     *bkev1beta1.BKECluster
    Cluster        *bkev1beta1.BKECluster
    NodeProvider   NodeProvider
    NodeFilter     NodeFilter
    StatusUpdater  NodeStatusUpdater
    Log            *bkev1beta1.BKELogger
    VersionContext *upgrade.VersionContext
    TemplateContext manifest.TemplateContext
    TargetClient   kubernetes.Interface
}
```

---

## 9. 迁移策略

### 9.1 Feature Gate 设计

```go
const (
    // BinaryComponentAnnotationKey 控制是否启用 Binary 组件路径
    BinaryComponentAnnotationKey = "cvo.openfuyao.cn/binary-component"

    // HelmComponentAnnotationKey 控制是否启用 Helm 组件路径
    HelmComponentAnnotationKey = "cvo.openfuyao.cn/helm-component"
)

// BinaryComponentEnabled 判断是否启用 Binary 组件路径
// 优先级: 对象注解 "true" > 全局 config.BinaryComponentSupport flag > false
func BinaryComponentEnabled(obj client.Object) bool {
    if annotations.Has(obj, BinaryComponentAnnotationKey) {
        return annotations.Get(obj, BinaryComponentAnnotationKey) == "true"
    }
    return config.BinaryComponentSupport
}
```

**设计思路**：
- 复用现有 `pkg/featuregate` 注解/flag 模式
- 全局 flag + 对象注解，优先级：注解 > 全局 flag > false
- 与现有 `DeclarativeUpgradeEnabled` 模式一致

### 9.2 分阶段迁移计划

| 阶段 | 时间 | 内容 | 风险 | 回滚方案 |
|------|------|------|------|---------|
| **Phase 1** | 第1-2周 | 实现 BinaryInstaller 核心逻辑 | 低 | 不启用 Feature Gate |
| **Phase 2** | 第3-4周 | 实现 HelmInstaller 核心逻辑 | 低 | 不启用 Feature Gate |
| **Phase 3** | 第5-6周 | 创建 ComponentVersion YAML + DAG 集成 | 中 | 关闭 Feature Gate |
| **Phase 4** | 第7-8周 | 灰度发布 + 测试验证 | 中 | 切换回旧路径 |
| **Phase 5** | 第9-10周 | 移除旧 Phase 代码 | 高 | 保留旧代码分支 |

### 9.3 向后兼容保证

- 新字段全部 `omitempty` + 指针类型，旧 YAML 不填则为 nil
- `Type` 字段 enum 已含 `binary`/`helm`/`yaml`/`selector`，无需改 enum
- 旧控制器代码不读取新字段，不受影响
- Feature Gate 关闭时即使新字段存在也不走新路径

---

## 10. 测试策略

### 10.1 单元测试

| 测试模块 | 测试场景 | 覆盖目标 |
|---------|---------|---------|
| **ArtifactDownloader** | HTTP 下载、Checksum 校验、缓存命中/未命中、架构适配 | >90% |
| **TemplateRenderer** | 8 类变量替换、条件渲染、自定义函数、错误处理 | >90% |
| **ConfigRenderer** | content 渲染、secretRef 获取、kubeconfig 生成、forEach 动态多文件生成 | >90% |
| **BinaryInstaller** | Install/Upgrade/Uninstall 完整流程、失败重试 | >85% |
| **HelmInstaller** | OCI/HTTP/本地 Chart 获取、Values 渲染、Install/Upgrade/Rollback | >85% |
| **YamlInstaller** | 清单获取、多文档解析、ServerSideApply/Replace/CreateOnly、Prune、健康检查 | >85% |
| **VersionContext** | HasCurrent/HasTarget/NeedsUpgrade 决策逻辑 | >90% |
| **BinaryComponentExecutor** | Rolling/Parallel/Batch 执行策略、FailurePolicy | >85% |

### 10.2 集成测试

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

### 10.3 E2E 测试

| 测试场景 | 集群规模 | 验证内容 |
|---------|---------|---------|
| **小规模安装** | 1 Master + 2 Worker | 完整安装流程，所有组件正常 |
| **中规模安装** | 3 Master + 10 Worker | 并行安装性能，无资源竞争 |
| **跨版本升级** | 3 Master + 5 Worker | v2.5.0 → v2.6.0 完整升级 (含 YAML 类型组件) |
| **升级失败恢复** | 3 Master + 3 Worker | 模拟节点失败，验证 Continue/Rollback 策略 |
| **YAML Prune 验证** | 1 Master + 2 Worker | 升级后验证废弃资源被正确裁剪 |

---

## 11. 工作量评估

### 11.1 工作量评估

| 任务 | 预估工时 | 风险等级 | 依赖 |
|------|---------|---------|------|
| **BinaryInstaller 核心实现** | 5 人日 | 中 | 无 |
| **HelmInstaller 核心实现** | 5 人日 | 中 | 无 |
| **YamlInstaller 核心实现** | 4 人日 | 中 | 无 |
| **TemplateRenderer 实现** | 3 人日 | 低 | 无 |
| **ConfigRenderer 实现** | 3 人日 | 低 | TemplateRenderer |
| **ApplyStrategy 引擎实现** | 2 人日 | 中 | YamlInstaller |
| **Prune 裁剪功能实现** | 2 人日 | 中 | ApplyStrategy 引擎 |
| **PreInstallHooks 执行引擎** | 3 人日 | 中 | HelmInstaller |
| **ComponentVersion CRD 扩展** | 3 人日 | 低 | 无 |
| **VersionContext 与 ExecutionContext 实现** | 3 人日 | 中 | 无 |
| **Selector 类型实现** | 2 人日 | 中 | VersionContext |
| **Docker 支持** | 4 人日 | 中 | BinaryInstaller |
| **BKEAgentSwitch 组件** | 3 人日 | 中 | BinaryInstaller |
| **BinaryComponentExecutor 集成** | 3 人日 | 中 | BinaryInstaller |
| **HelmComponentExecutor 集成** | 3 人日 | 中 | HelmInstaller |
| **YamlComponentExecutor 集成** | 2 人日 | 中 | YamlInstaller |
| **ComponentVersion YAML 编写** | 2 人日 | 低 | CRD 扩展 |
| **DAG 调度器适配** | 3 人日 | 低 | Executor 集成 |
| **Feature Gate 实现** | 1 人日 | 低 | 无 |
| **兼容层实现** | 3 人日 | 中 | DAG 调度器适配 |
| **错误分类与恢复机制** | 3 人日 | 中 | 核心实现完成 |
| **单元测试** | 8 人日 | 低 | 核心实现完成 |
| **集成测试** | 7 人日 | 中 | 单元测试完成 |
| **E2E 测试** | 12 人日 | 中 | 集成测试完成 |
| **迁移验证** | 3 人日 | 中 | 兼容层实现 |
| **文档编写** | 4 人日 | 低 | 无 |
| **代码审查与修复** | 4 人日 | 中 | 测试完成 |
| **总计** | **114 人日 (约 5.5 人月)** | | |

### 11.2 Sprint 计划

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
| ComponentVersion CRD 扩展 | 开发A | binary/helm/yaml/selector/SubComponents/Resources 字段 |
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

### 11.3 里程碑

| 里程碑 | 时间 | 交付内容 | 验收标准 |
|--------|------|---------|---------|
| **M1: BinaryInstaller 完成** | 第2周末 | BinaryInstaller 核心功能 + 单元测试 | 单元测试覆盖率 >85% |
| **M2: YamlInstaller 完成** | 第2周末 | YamlInstaller + ApplyStrategy + Prune | 单元测试覆盖率 >85% |
| **M3: HelmInstaller 完成** | 第4周末 | HelmInstaller + PreInstallHooks + 单元测试 | 单元测试覆盖率 >85% |
| **M4: DAG 集成完成** | 第5周末 | Executor 集成 + VersionContext + ComponentVersion YAML | 集成测试通过 |
| **M5: Beta 发布** | 第6周末 | Feature Gate 灰度 + 兼容层 + E2E 测试 | E2E 通过率 >95% |

---

## 12. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **SSH 连接不稳定** | 二进制安装失败 | 中 | 重试机制 + 超时控制 + 详细错误日志 |
| **制品下载失败** | 安装阻塞 | 低 | 本地缓存 + 多源下载 + Checksum 校验 |
| **模板渲染错误** | 配置错误导致服务异常 | 中 | 渲染前校验 + DryRun 模式 + 详细错误信息 |
| **Helm Chart 不兼容** | 组件部署失败 | 低 | 版本约束校验 + 健康检查 + 自动回滚 |
| **迁移期间行为不一致** | 新旧路径行为差异 | 中 | Feature Gate 控制 + 充分测试 + 灰度发布 |
| **离线环境缓存不足** | 无法安装/升级 | 低 | 预下载机制 + 本地路径支持 + 缓存清理策略 |

---

## 13. 收益评估

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

---

## 14. 附录

### 14.1 参考文档

- KEP-5: 基于 ClusterVersion/ReleaseImage/UpgradePath 的声明式集群版本升级
- ComponentVersion CRD 定义
- ReleaseImage CRD 定义
- DAG 调度器设计文档
- Helm Action API: https://pkg.go.dev/helm.sh/helm/v3/pkg/action

### 14.2 术语表

| 术语 | 定义 |
|------|------|
| **BinaryInstaller** | 负责二进制组件下载、渲染、安装、健康检查的安装器 |
| **HelmInstaller** | 负责 Helm Chart 获取、渲染、部署的安装器 |
| **YamlInstaller** | 负责 YAML 清单获取、解析、应用、裁剪、健康检查的安装器 |
| **VersionContext** | 携带组件版本事实（已安装版本、目标版本），Executor 据此自主决定操作类型 |
| **ApplyStrategy** | YAML 清单应用策略：ServerSideApply/Replace/CreateOnly |
| **Prune** | 按标签选择器裁剪不再需要的 Kubernetes 资源 |
| **SubComponents** | 组件的组合关系（父子包含），区别于 Dependencies（执行顺序） |
| **Selector** | 互斥选择器类型，从多个候选组件中按 condition 选择一个，典型用例：容器运行时（containerd 或 docker） |
| **NodeFilter** | 节点过滤策略，支持按角色、标签、幂等性、预约节点过滤，仅用于 Binary 组件 |
| **BKEAgentSwitch** | 独立组件，负责在 cluster-api 部署完成后切换 bkeagent 的监听目标从管理集群到目标集群 |
| **configTemplates** | 配置文件模板系统，支持 Go template/Secret/kubeconfig |
| **installScript** | 安装脚本模板，支持 8 类 50+ 变量和条件渲染 |
| **Artifact** | 二进制制品，包含 URL、Checksum、安装路径等信息 |
| **ComponentVersion** | 组件版本 CRD，定义组件的类型、配置、依赖等 |
| **ReleaseImage** | 发布版本清单 CRD，定义安装和升级的组件列表 |
| **ExecutorRegistry** | 执行器注册表，按组件类型注册执行器，支持按需注入和开闭原则 |
| **TemplateRenderer** | 模板渲染引擎，支持 Go template、自定义函数、条件渲染 |
| **ConfigRenderer** | 配置文件渲染器，支持 content 模板渲染、secretRef 从 Secret 获取、kubeconfigTemplate 动态生成 |

---

**文档版本**: v1.0  
**维护者**: openFuyao Team

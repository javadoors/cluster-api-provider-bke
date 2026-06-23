# KEP-6 详细设计文档：基于 ReleaseImage 的二进制与 Helm 组件声明式管理方案

**文档版本**: v1.0  
**创建日期**: 2026-06-12  
**状态**: Draft  
**依赖**: KEP-6 提案文档

---

## 目录

1. [概述](#1-概述)
2. [整体架构设计](#2-整体架构设计)
3. [ComponentVersion CRD 详细设计](#3-componentversion-crd-详细设计)
   - 3.1 Binary 类型完整字段定义
   - 3.2 Helm 类型完整字段定义
   - 3.3 CRD YAML 示例
   - 3.4 BKENode 扩展字段
4. [BinaryInstaller 详细设计](#4-binaryinstaller-详细设计)
   - 4.1 核心组件架构
   - 4.2 BinaryInstaller 执行流程图
   - 4.3 核心接口定义
   - 4.4 核心类型定义
   - 4.5 核心方法实现
5. [HelmInstaller 详细设计](#5-helminstaller-详细设计)
   - 5.1 核心组件架构
   - 5.2 HelmInstaller 执行流程图
   - 5.3 核心接口定义
   - 5.4 核心方法实现
   - 5.5 PreInstallHooks 执行引擎
6. [YAMLManifestExecutor 详细设计](#6-yamlmanifestexecutor-详细设计)
   - 6.1 核心组件架构
   - 6.2 YAMLManifestExecutor 执行流程图
   - 6.3 核心接口定义
   - 6.4 核心方法实现
7. [TemplateRenderer 详细设计](#7-templaterenderer-详细设计)
8. [ConfigRenderer 详细设计](#8-configrenderer-详细设计)
9. [DAG 集成详细设计](#9-dag-集成详细设计)
   - 9.1 执行器注册
   - 9.2 DAG 调度流程图
   - 9.3 核心接口定义
   - 9.4 ComponentNode 扩展
   - 9.5 Scheduler 扩展与执行策略
   - 9.6 NeedExecute 接口适配
   - 9.7 组件状态回写机制
10. [完整安装流程详细设计](#10-完整安装流程详细设计)
11. [完整升级流程详细设计](#11-完整升级流程详细设计)
12. [迁移策略详细设计](#12-迁移策略详细设计)
    - 12.1 迁移流程图
    - 12.2 Feature Gate 定义
    - 12.3 兼容层实现与迁移验证
13. [错误处理与恢复](#13-错误处理与恢复)
    - 13.1 错误处理流程图
    - 13.2 错误分类实现
14. [测试设计](#14-测试设计)
15. [工作量与任务拆解](#15-工作量与任务拆解)
16. [附录](#16-附录)

---

## 1. 概述

### 1.1 设计目标

本详细设计文档基于 KEP-6 提案，提供完整的实现方案，包括：

- **BinaryInstaller**: 二进制组件的下载、渲染、安装
- **HelmInstaller**: Helm 组件的 Chart 获取、渲染、部署
- **TemplateRenderer**: 模板变量渲染引擎
- **ConfigRenderer**: 配置文件模板渲染引擎
- **DAG 集成**: 执行器注册与调度流程

### 1.2 设计范围

| 范围 | 说明 |
|------|------|
| CRD 扩展 | ComponentVersion 新增 binary/helm 类型的完整字段定义 |
| 核心安装器 | BinaryInstaller、HelmInstaller 的完整实现 |
| 渲染引擎 | TemplateRenderer、ConfigRenderer 的完整实现 |
| DAG 集成 | BinaryComponentExecutor、HelmComponentExecutor |
| 迁移策略 | Feature Gate、向后兼容、灰度发布 |

### 1.3 设计约束

| 约束 | 说明 |
|------|------|
| 向后兼容 | 必须支持从现有硬编码 Phase 平滑迁移 |
| 离线环境 | 二进制制品和 Helm Chart 支持本地缓存 |
| 架构支持 | 必须支持 amd64 和 arm64 架构 |
| 操作系统支持 | 必须支持 CentOS 7/8、Ubuntu 20.04/22.04、麒麟 V10 |
| 接口复用 | 复用现有 NeedExecute() 接口 |
| 安全性 | 制品必须支持 checksum 校验 |

### 1.4 术语表

| 术语 | 定义 |
|------|------|
| **BinaryInstaller** | 负责二进制组件下载、渲染、安装的安装器 |
| **HelmInstaller** | 负责 Helm Chart 获取、渲染、部署的安装器 |
| **configTemplates** | 配置文件模板系统，支持 Go template/Secret/kubeconfig |
| **installScript** | 安装脚本模板，支持 8 类 50+ 变量和条件渲染 |
| **Artifact** | 二进制制品，包含 URL、Checksum、安装路径等信息 |
| **ComponentVersion** | 组件版本 CRD，定义组件的类型、配置、依赖等 |
| **ReleaseImage** | 发布版本清单 CRD，定义安装和升级的组件列表 |

---

## 2. 整体架构设计

### 2.1 系统架构图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              BKECluster                                          │
│  spec.desiredVersion: v2.6.0                                                     │
└────────────────────────────────┬────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            ReleaseImage                                          │
│  spec.install.components: [containerd/v1.7.18, bkeagent/v2.6.0, ...]            │
│  spec.upgrade.components: [containerd/v1.7.18, bkeagent/v2.6.0, ...]            │
└────────────────────────────────┬────────────────────────────────────────────────┘
                                 │ 按 (name, version) 定位
                                 ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         bke-manifests (ComponentVersion)                         │
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ containerd/v1.7.18/component.yaml          ← type: binary              │    │
│  │   ├── binary.artifacts: [containerd, ctr, shim]                        │    │
│  │   ├── binary.configTemplates: [config.toml, service]                   │    │
│  │   └── binary.installScript: (带 50+ 模板变量)                          │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ bkeagent/v2.6.0/component.yaml             ← type: binary              │    │
│  │   ├── binary.artifacts: [bkeagent]                                     │    │
│  │   ├── binary.configTemplates: [bkeagent.conf, tls, kubeconfig]         │    │
│  │   └── binary.installScript: (带完整模板变量)                           │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ coredns/v1.11.1/component.yaml             ← type: helm                │    │
│  │   ├── helm.chart.oci: registry/charts/coredns                          │    │
│  │   ├── helm.values: (带模板变量)                                        │    │
│  │   └── helm.healthCheck: PodReady + EndpointReady                       │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└────────────────────────────────┬────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            DAG Scheduler                                         │
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                        Component Executor Factory                        │    │
│  │                                                                         │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌────────────┐  │    │
│  │  │   Binary     │  │    Helm      │  │   Inline     │  │   YAML     │  │    │
│  │  │  Component   │  │   Component  │  │   Component  │  │  Manifest  │  │    │
│  │  │  Executor    │  │   Executor   │  │   Executor   │  │  Executor  │  │    │
│  │  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └─────┬──────┘  │    │
│  │         │                 │                 │                │          │    │
│  └─────────┼─────────────────┼─────────────────┼────────────────┼──────────┘    │
│            │                 │                 │                │               │
│            ▼                 ▼                 ▼                ▼               │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                          Installer Layer                                  │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                      BinaryInstaller                             │   │    │
│  │  │  ┌────────────────┐  ┌────────────────┐  ┌─────────────────┐   │   │    │
│  │  │  │   Artifact     │  │   Template     │  │    Config       │   │   │    │
│  │  │  │  Downloader    │  │  Renderer      │  │   Renderer      │   │   │    │
│  │  │  └────────┬───────┘  └────────┬───────┘  └────────┬────────┘   │   │    │
│  │  │           │                   │                   │            │   │    │
│  │  │           └───────────────────┼───────────────────┘            │   │    │
│  │  │                               ▼                                │   │    │
│  │  │                      ┌────────────────┐                        │   │    │
│  │  │                      │  SSH Executor  │                        │   │    │
│  │  │                      └────────────────┘                        │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                       HelmInstaller                              │   │    │
│  │  │  ┌────────────────┐  ┌────────────────┐  ┌─────────────────┐   │   │    │
│  │  │  │    Chart       │  │    Values      │  │    Helm         │   │   │    │
│  │  │  │   Fetcher      │  │   Renderer     │  │  Action Exec    │   │   │    │
│  │  │  └────────┬───────┘  └────────┬───────┘  └────────┬────────┘   │   │    │
│  │  │           │                   │                   │            │   │    │
│  │  │           └───────────────────┼───────────────────┘            │   │    │
│  │  │                               ▼                                │   │    │
│  │  │                      ┌────────────────┐                        │   │    │
│  │  │                      │ HealthChecker  │                        │   │    │
│  │  │                      └────────────────┘                        │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                     ManifestApplier                              │   │    │
│  │  │  ┌────────────────┐  ┌────────────────┐  ┌─────────────────┐   │   │    │
│  │  │  │   YAML Parser  │  │  Template      │  │  K8s Client     │   │   │    │
│  │  │  │  (清单解析)    │  │  Renderer      │  │  (Apply/Delete) │   │   │    │
│  │  │  └────────────────┘  └────────────────┘  └─────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 组件交互关系

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              组件交互关系图                                       │
└─────────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────────┐
                    │  BKECluster      │
                    │  Reconciler      │
                    └────────┬─────────┘
                             │
                             │ 1. 解析 ReleaseImage
                             ▼
                    ┌──────────────────┐
                    │  ReleaseImage    │
                    │  Parser          │
                    └────────┬─────────┘
                             │
                             │ 2. 加载 ComponentVersion
                             ▼
                    ┌──────────────────┐
                    │  ManifestStore   │
                    └────────┬─────────┘
                             │
                             │ 3. 构建 DAG
                             ▼
                    ┌──────────────────┐
                    │  DAG Builder     │
                    └────────┬─────────┘
                             │
                             │ 4. 调度执行
                             ▼
                    ┌──────────────────┐
                    │  DAG Scheduler   │
                    └────────┬─────────┘
                              │
               ┌──────────────┼──────────────┼──────────────┐
               │              │              │              │
               ▼              ▼              ▼              ▼
     ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
     │   Binary     │ │    Helm      │ │   Inline     │ │   Manifest   │
     │   Executor   │ │   Executor   │ │   Executor   │ │   Executor   │
     └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
            │                │                │                │
            ▼                ▼                ▼                ▼
     ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
     │   Binary     │ │    Helm      │ │  Component   │ │  Manifest    │
     │  Installer   │ │  Installer   │ │  Factory     │ │  Applier     │
     └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
            │                │                │                │
            ▼                ▼                ▼                ▼
     ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
     │  SSH Client  │ │ Helm SDK     │ │ Phase        │ │ K8s Client   │
     │  (bkessh)    │ │ (helm/v3)    │ │ Execute()    │ │ (Apply)      │
     └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
```

---

## 3. ComponentVersion CRD 详细设计

### 3.1 Binary 类型完整字段定义

```go
// pkg/api/v1alpha1/componentversion_types.go

// ComponentVersionSpec 定义组件版本规格
type ComponentVersionSpec struct {
    // 组件名称
    Name string `json:"name"`
    
    // 组件类型: yaml, helm, inline, binary
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
    
    // 子组件引用列表

> **设计思路 - SubComponents 与 Dependencies 的区别**：两者语义不同——`SubComponents` 表示"组合关系"（父子包含），`Dependencies` 表示"执行顺序"（先后依赖）。
> - **SubComponents**：如 Kubernetes 组件包含 kube-apiserver、kube-controller-manager 等子组件，它们共享同一版本、作为一个整体安装/升级。子组件无需指定 `type`，因为其类型由同名 `ComponentVersion` 资源决定。
> - **Dependencies**：如 coredns 依赖 kube-apiserver 先就绪，这是 DAG 中的边，决定执行顺序而非包含关系。
> 简言之：SubComponents 是"有什么"，Dependencies 是"先做什么"。

    SubComponents []SubComponent `json:"subComponents,omitempty"`
    
    // 兼容性约束
    Compatibility CompatibilitySpec `json:"compatibility,omitempty"`
    
    // 依赖关系
    Dependencies []Dependency `json:"dependencies,omitempty"`
    
    // 升级策略

> **设计思路 - Parallel vs Batch 的语义差异**：`Parallel` 模式在所有节点上同时执行升级，无并发控制，适用于低风险组件（如配置文件更新）。`Batch` 模式按 `BatchSize` 分批执行，每批成功后再执行下一批，适用于高风险组件（如 containerd 升级可能导致节点短暂不可用）。`BatchSize >= totalNodes` 等价于 `Parallel`，但 `Batch` 模式提供了逐批验证的语义——每批完成后检查节点健康状态，异常则暂停后续批次。
    UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
    
    // Kubernetes 资源定义列表
    Resources []ResourceSpec `json:"resources,omitempty"`
}

// SubComponent 定义子组件引用
type SubComponent struct {
    // 子组件名称
    Name string `json:"name"`
    
    // 子组件版本
    Version string `json:"version"`
}

// ResourceSpec 定义 Kubernetes 资源

> **设计思路 - 两种资源定义方式**：`ResourceSpec` 同时支持声明式字段（Kind/APIVersion/Name/Data）和原始 `Manifest` 字符串。选择双模式的原因是：
> 1. **声明式字段**适用于简单资源（如 ConfigMap、Secret），可直接在 CRD 中定义 key-value 数据，无需外部文件。优势是结构化、可校验、可被 kubectl 直接查看。
> 2. **Manifest 字段**适用于复杂资源（如 Deployment、CRD），声明式字段无法表达完整的 Kubernetes 资源定义，使用原始 YAML 更灵活。
> 3. **优先级**：当 `Manifest` 非空时，以 Manifest 为准（忽略 Kind/APIVersion 等字段），因为 Manifest 是完整定义，声明式字段仅是元数据摘要。
> 4. **存储考量**：大型 Manifest 应通过 `YAMLSpec.Manifests` URL 引用外部文件，而非直接嵌入 CRD（会膨胀 etcd 存储）。`ResourceSpec.Manifest` 仅适用于小型内联资源。
type ResourceSpec struct {
    // 资源类型
    Kind string `json:"kind"`
    
    // API 版本
    APIVersion string `json:"apiVersion"`
    
    // 命名空间
    Namespace string `json:"namespace,omitempty"`
    
    // 资源名称
    Name string `json:"name"`
    
    // 标签选择器
    Labels map[string]string `json:"labels,omitempty"`
    
    // Data 字段
    Data map[string]string `json:"data,omitempty"`
    
    // StringData 字段
    StringData map[string]string `json:"stringData,omitempty"`
    
    // 原始 Manifest 内容
    Manifest string `json:"manifest,omitempty"`
}

// YAMLSpec 定义 YAML 清单组件规格
type YAMLSpec struct {
    // YAML 清单文件列表
    Manifests []ManifestRef `json:"manifests"`
    
    // 部署目标命名空间
    Namespace string `json:"namespace,omitempty"`
    
    // 应用策略: ServerSideApply, Replace, CreateOnly
    ApplyStrategy string `json:"applyStrategy,omitempty"`
    
    // 是否启用裁剪 (按 label selector 删除不再需要的资源)

> **设计思路 - 基于标签裁剪 vs ownerReference**：选择标签选择器（`PruneLabelSelector`）而非 ownerReference 进行资源裁剪，原因是：
> 1. **ownerReference 的局限**：ownerReference 要求资源与属主在同一命名空间，且级联删除是全量的（删除属主则删除所有关联资源），无法实现"仅删除当前组件不再管理的资源"的精确裁剪。
> 2. **标签裁剪的灵活性**：标签选择器可跨命名空间匹配，且裁剪逻辑是"保留当前清单中存在的资源、删除标签匹配但不在清单中的资源"，精确控制范围。
> 3. **风险缓解**：标签裁剪确实存在误删风险（其他组件添加了相同标签的资源）。缓解措施：① 要求 `PruneLabelSelector` 必须包含组件专属标签（如 `app.kubernetes.io/managed-by: {{componentName}}`）；② 裁剪前 dry-run 预览；③ 裁剪操作记录审计日志。
    Prune bool `json:"prune,omitempty"`
    
    // 裁剪使用的标签选择器
    PruneLabelSelector map[string]string `json:"pruneLabelSelector,omitempty"`
}

// ManifestRef 定义 YAML 清单文件引用
type ManifestRef struct {
    // 清单文件 URL 或路径
    URL string `json:"url"`
    
    // 校验和
    Checksum string `json:"checksum,omitempty"`
}

// BinarySpec 定义二进制组件规格

> **设计思路 - 双模板变量系统**：系统中存在两套模板语法——URL/路径变量使用简单替换 `{{componentVersion}}`，安装脚本和配置模板使用 Go template `{{.componentVersion}}`。选择双系统的原因是：
> 1. **URL 场景需要简单可靠**：制品 URL（如 `https://repo/example/{{componentVersion}}/binary-{{nodeArch}}.tar.gz`）是字符串拼接，不需要条件逻辑、循环等高级功能，`strings.ReplaceAll` 零依赖、无解析错误风险，适合在下载阶段早期执行。
> 2. **脚本场景需要表达力**：安装/卸载脚本需要条件判断（`{{if eq .os "kylin"}}...{{end}}`）、循环（`{{range .artifacts}}...{{end}}`）等逻辑，Go template 是天然选择。
> 3. **执行时机不同**：URL 变量在下载前解析（此时节点信息已知但集群上下文未构建），脚本变量在执行时解析（需要完整的集群/节点/制品上下文）。
> 两套变量共享相同的变量名（如 `componentVersion`、`nodeArch`），但语法前缀不同（无 `.` vs 有 `.`），开发者需注意区分使用场景。

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
    
    // 默认安装路径

> **设计思路 - 五个独立路径字段**：不使用单一根路径（如 `DefaultRootDir`）派生子路径，是因为不同组件的目录布局差异很大——containerd 使用 `/usr/local/bin`（二进制）+ `/etc/containerd`（配置）+ `/var/lib/containerd`（数据），而 bkeagent 使用 `/opt/bke`（安装）+ `/etc/bke`（配置）+ `/var/log/bke`（日志）。五个路径字段提供了最大的灵活性，安装脚本中可通过 `{{.defaultInstallPath}}` 等变量引用。`DefaultInstallPath` 指组件的安装根目录，`DefaultBinPath` 指可执行文件的存放目录，两者可能不同。
    DefaultInstallPath string `json:"defaultInstallPath,omitempty"`
    
    // 默认配置路径
    DefaultConfigPath string `json:"defaultConfigPath,omitempty"`
    
    // 默认日志路径
    DefaultLogPath string `json:"defaultLogPath,omitempty"`
    
    // 默认数据路径
    DefaultDataPath string `json:"defaultDataPath,omitempty"`
    
    // 默认二进制路径
    DefaultBinPath string `json:"defaultBinPath,omitempty"`
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
    
    // 可执行文件名

> **设计思路 - Executable 与 InstallPath 的区别**：`InstallPath` 指制品在远程节点上的存放路径（如 `/usr/local/bin/containerd`），`Executable` 指制品解压后的可执行文件名（如 `containerd`）。当制品是压缩包时，`InstallPath` 是压缩包路径，`Executable` 是解压后需要 chmod +x 的二进制文件名。当制品是单个二进制文件时，`Executable` 可省略（直接使用 `InstallPath` 的 basename）。
    Executable string `json:"executable,omitempty"`
}

// ConfigTemplateSpec 定义配置文件模板规格

> **设计思路 - 三模式平铺设计**：`Content`、`SecretRef`、`KubeconfigTemplate` 三个字段互斥——同一配置文件只能有一种内容来源。不使用 `Mode` 判别字段或 Go 接口的原因是：
> 1. **CRD 兼容性**：Kubernetes CRD 的 OpenAPI v3 不支持 discriminated union（oneOf），使用互斥指针字段是 CRD 设计的惯用模式（类似 Kubernetes 本身的 `VolumeSource`）。
> 2. **序列化简洁**：平铺结构在 YAML 中直观易读，无需额外的 mode 字段。优先级规则：`KubeconfigTemplate` > `SecretRef` > `Content`，即如果多个字段同时非 nil，按此优先级取第一个非 nil 字段。
> 3. **验证补充**：在 Admission Webhook 中应添加校验，确保仅一个字段非 nil，避免歧义。
type ConfigTemplateSpec struct {
    // 模板名称
    Name string `json:"name"`
    
    // 目标路径
    Path string `json:"path"`
    
    // 文件权限 (如 "0644")
    Mode string `json:"mode,omitempty"`
    
    // 文件所有者 (如 "root:root")
    Owner string `json:"owner,omitempty"`
    
    // 模板内容 (Go template 语法)
    Content string `json:"content,omitempty"`
    
    // Secret 引用
    SecretRef *SecretRefSpec `json:"secretRef,omitempty"`
    
    // Kubeconfig 模板
    KubeconfigTemplate *KubeconfigTemplateSpec `json:"kubeconfigTemplate,omitempty"`
}

// SecretRefSpec 定义 Secret 引用规格
type SecretRefSpec struct {
    // Secret 名称
    Name string `json:"name"`
    
    // Secret 命名空间 (支持模板变量)
    Namespace string `json:"namespace"`
    
    // Secret 中的 key
    Key string `json:"key"`
}

// KubeconfigTemplateSpec 定义 Kubeconfig 模板规格
type KubeconfigTemplateSpec struct {
    // 集群名称
    ClusterName string `json:"clusterName"`
    
    // API Server 地址
    APIServer string `json:"apiServer"`
    
    // CA 证书路径
    CACertPath string `json:"caCertPath"`
    
    // 客户端证书路径
    ClientCertPath string `json:"clientCertPath"`
    
    // 客户端密钥路径
    ClientKeyPath string `json:"clientKeyPath"`
    
    // 命名空间
    Namespace string `json:"namespace,omitempty"`
    
    // ServiceAccount
    ServiceAccount string `json:"serviceAccount,omitempty"`
}

// OSSpec 定义操作系统规格
type OSSpec struct {
    // 操作系统名称 (centos, ubuntu, kylin)
    Name string `json:"name"`
    
    // 支持的版本列表
    Versions []string `json:"versions"`
}
```

### 3.2 Helm 类型完整字段定义

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

> **设计思路 - ValuesFiles 与 Values 的关系**：`ValuesFiles` 和 `Values` 是 Helm Values 的两种提供方式，合并优先级从低到高为：Chart 默认值 → ValuesFiles[0] → ValuesFiles[1] → ... → Values 内联字段。即 `Values` 内联字段优先级最高，可覆盖文件中的任何值。
> - **ValuesFiles**：适用于按环境区分的大型配置（如 `values-linux.yaml`、`values-arm64.yaml`），文件路径支持模板变量渲染（如 `values-{{nodeOS}}.yaml`），实现动态选择。
> - **Values 内联**：适用于少量覆盖值（如 `image.tag`），直接在 CRD 中定义，优先级最高。
> 文件来源期望：控制器 Pod 的本地文件系统（通常通过 ConfigMap 挂载），后续可扩展支持 HTTP URL 和 ConfigMap 引用。
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

// OCIChartSpec 定义 OCI Chart 规格
type OCIChartSpec struct {
    // OCI 仓库地址
    Repository string `json:"repository"`
    
    // Chart 标签
    Tag string `json:"tag"`
}

// HelmStrategySpec 定义 Helm 安装策略

> **设计思路 - 为什么 CRD 允许指定 Install/Upgrade/Rollback**：`Mode` 字段允许用户显式指定操作模式，而非完全由系统自动判定。原因是：
> 1. **覆盖自动判定**：某些场景下自动判定不够准确，如 Helm Release 处于 failed 状态时，自动判定可能选择 Upgrade，但用户实际需要 Rollback。
> 2. **强制重装**：`Mode: Install` 配合 `CleanupOnFail: true` 可实现强制重装，用于修复损坏的 Release。
> 3. **默认行为**：当 `Mode` 为空时，系统自动判定——Release 不存在则 Install，已存在则 Upgrade，与 Helm 原生行为一致。
type HelmStrategySpec struct {
    // 安装模式: Install / Upgrade / Rollback
    Mode string `json:"mode,omitempty"`
    
    // 是否等待就绪
    Wait bool `json:"wait,omitempty"`
    
    // 等待超时时间
    WaitTimeout string `json:"waitTimeout,omitempty"`
    
    // 是否原子操作 (失败自动回滚)
    Atomic bool `json:"atomic,omitempty"`
    
    // 失败时是否清理
    CleanupOnFail bool `json:"cleanupOnFail,omitempty"`
}

// HealthCheckSpec 定义健康检查规格

> **设计思路 - 自定义健康检查 vs Helm 原生 --wait**：Helm 的 `--wait` 仅检查 Pod 是否 Ready，但很多组件的就绪条件更复杂（如 Service Endpoint 就绪、自定义健康检查接口返回 200）。自定义健康检查提供了：
> 1. **多维度检查**：`PodReady` 检查 Pod 状态，`EndpointReady` 检查 Service Endpoints，`Custom` 支持任意条件。
> 2. **最小就绪数**：`MinReady` 允许部分 Pod 就绪即认为组件健康（如 3 副本中 2 个就绪即可），Helm --wait 要求全部 Ready。
> 3. **与 Helm --wait 的关系**：两者可叠加使用——Helm --wait 在 Release 安装完成后等待 Pod 调度，HealthCheck 在此基础上进一步验证业务逻辑就绪。
type HealthCheckSpec struct {
    // 是否启用健康检查
    Enabled bool `json:"enabled"`
    
    // 健康检查超时时间
    Timeout string `json:"timeout,omitempty"`
    
    // 健康检查间隔
    Interval string `json:"interval,omitempty"`
    
    // 健康检查项列表
    Checks []HealthCheckItemSpec `json:"checks,omitempty"`
}

// HealthCheckItemSpec 定义健康检查项规格
type HealthCheckItemSpec struct {
    // 检查类型: PodReady / EndpointReady / Custom
    Type string `json:"type"`
    
    // 命名空间
    Namespace string `json:"namespace,omitempty"`
    
    // 标签选择器
    LabelSelector string `json:"labelSelector,omitempty"`
    
    // 名称
    Name string `json:"name,omitempty"`
    
    // 端口
    Port int32 `json:"port,omitempty"`
    
    // 最小就绪数量
    MinReady int32 `json:"minReady,omitempty"`
}

// RollbackSpec 定义回滚规格
type RollbackSpec struct {
    // 是否启用回滚
    Enabled bool `json:"enabled"`
    
    // 保留历史版本数
    MaxHistory int `json:"maxHistory,omitempty"`
}

// UninstallSpec 定义卸载规格
type UninstallSpec struct {
    // 卸载前钩子
    PreUninstallHooks []HookSpec `json:"preUninstallHooks,omitempty"`
}

// HookSpec 定义钩子规格

> **设计思路 - 仅支持 Job 类型**：当前 `HookSpec.Type` 仅支持 `"Job"` 类型，不使用 Helm 原生钩子注解（`helm.sh/hook`）。原因是：
> 1. **控制权**：Helm 原生钩子由 Helm SDK 管理（创建、等待、删除），自定义钩子由 Installer 控制完整生命周期，可自定义超时、重试、清理策略。
> 2. **扩展计划**：后续将支持更多类型——`"ConfigMap"`（安装前创建配置）、`"Exec"`（在已有 Pod 中执行命令），Type 字段为 string 而非 enum 就是为了预留扩展空间。
> 3. **非 Job 类型当前处理**：暂时跳过（`if hook.Type != "Job" { continue }`），而非报错，是为了向前兼容——新增钩子类型后，旧版本控制器仅忽略不支持的类型，不会中断安装流程。
type HookSpec struct {
    // 钩子名称
    Name string `json:"name,omitempty"`
    
    // 钩子类型: Job
    Type string `json:"type"`
    
    // 钩子 Manifest
    Manifest string `json:"manifest"`
}
```

### 3.3 CRD YAML 示例

```yaml
# config/crd/bases/config.openfuyao.cn_componentversions.yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: componentversions.config.openfuyao.cn
spec:
  group: config.openfuyao.cn
  names:
    kind: ComponentVersion
    listKind: ComponentVersionList
    plural: componentversions
    singular: componentversion
    shortNames:
      - cv
  scope: Namespaced
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                name:
                  type: string
                type:
                  type: string
                  enum: [yaml, helm, inline, binary]
                version:
                  type: string
                binary:
                  type: object
                  properties:
                    variables:
                      type: object
                      additionalProperties:
                        type: string
                    artifacts:
                      type: array
                      items:
                        type: object
                        properties:
                          name:
                            type: string
                          url:
                            type: string
                          checksum:
                            type: string
                          installPath:
                            type: string
                          executable:
                            type: string
                        required: [name, url, checksum, installPath]
                    configTemplates:
                      type: array
                      items:
                        type: object
                        properties:
                          name:
                            type: string
                          path:
                            type: string
                          mode:
                            type: string
                          owner:
                            type: string
                          content:
                            type: string
                          secretRef:
                            type: object
                            properties:
                              name:
                                type: string
                              namespace:
                                type: string
                              key:
                                type: string
                          kubeconfigTemplate:
                            type: object
                            properties:
                              clusterName:
                                type: string
                              apiServer:
                                type: string
                              caCertPath:
                                type: string
                              clientCertPath:
                                type: string
                              clientKeyPath:
                                type: string
                        required: [name, path]
                    installScript:
                      type: string
                    uninstallScript:
                      type: string
                    supportedArchitectures:
                      type: array
                      items:
                        type: string
                    supportedOS:
                      type: array
                      items:
                        type: object
                        properties:
                          name:
                            type: string
                          versions:
                            type: array
                            items:
                              type: string
                  required: [artifacts, installScript, supportedArchitectures, supportedOS]
                helm:
                  type: object
                  properties:
                    chart:
                      type: object
                      properties:
                        oci:
                          type: object
                          properties:
                            repository:
                              type: string
                            tag:
                              type: string
                        url:
                          type: string
                        localPath:
                          type: string
                        checksum:
                          type: string
                    namespace:
                      type: string
                    releaseName:
                      type: string
                    values:
                      type: object
                      x-kubernetes-preserve-unknown-fields: true
                    strategy:
                      type: object
                      properties:
                        mode:
                          type: string
                        wait:
                          type: boolean
                        waitTimeout:
                          type: string
                        atomic:
                          type: boolean
                        cleanupOnFail:
                          type: boolean
                    healthCheck:
                      type: object
                      properties:
                        enabled:
                          type: boolean
                        timeout:
                          type: string
                        interval:
                          type: string
                        checks:
                          type: array
                          items:
                            type: object
                            properties:
                              type:
                                type: string
                              namespace:
                                type: string
                              labelSelector:
                                type: string
                              name:
                                type: string
                              port:
                                type: integer
                              minReady:
                                type: integer
                    rollback:
                      type: object
                      properties:
                        enabled:
                          type: boolean
                        maxHistory:
                          type: integer
                  required: [chart, namespace, releaseName]
                yaml:
                  type: object
                  properties:
                    manifests:
                      type: array
                      items:
                        type: object
                        properties:
                          url:
                            type: string
                          checksum:
                            type: string
                        required: [url]
                    namespace:
                      type: string
                    applyStrategy:
                      type: string
                      enum: [ServerSideApply, Replace, CreateOnly]
                    prune:
                      type: boolean
                    pruneLabelSelector:
                      type: object
                      additionalProperties:
                        type: string
                  required: [manifests]
                subComponents:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      version:
                        type: string
                    required: [name, version]
                resources:
                  type: array
                  items:
                    type: object
                    properties:
                      kind:
                        type: string
                      apiVersion:
                        type: string
                      namespace:
                        type: string
                      name:
                        type: string
                      labels:
                        type: object
                        additionalProperties:
                          type: string
                      data:
                        type: object
                        additionalProperties:
                          type: string
                      stringData:
                        type: object
                        additionalProperties:
                          type: string
                      manifest:
                        type: string
                    required: [kind, apiVersion, name]
                compatibility:
                  type: object
                  properties:
                    constraints:
                      type: array
                      items:
                        type: object
                        properties:
                          component:
                            type: string
                          rule:
                            type: string
                dependencies:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      phase:
                        type: string
                upgradeStrategy:
                  type: object
                  properties:
                    mode:
                      type: string
                    batchSize:
                      type: integer
                    timeout:
                      type: string
                    failurePolicy:
                      type: string
              required: [name, type, version]
```

---


### 3.4 BKENode 扩展字段



#### 3.4.1 BKENodeSpec 扩展

当前 `BKENodeSpec` 缺少 `Architecture` 和 `OperatingSystem` 字段，需要扩展：

```go
// api/bkecommon/v1beta1/bkenode_types.go 扩展

type BKENodeSpec struct {
    // 现有字段
    IP           string `json:"ip"`
    Hostname     string `json:"hostname,omitempty"`
    Role         string `json:"role"`
    Port         int32  `json:"port,omitempty"`
    Username     string `json:"username,omitempty"`
    Password     string `json:"password,omitempty"`
    ControlPlane bool   `json:"controlPlane,omitempty"`
    Kubelet      *KubeletConfig `json:"kubelet,omitempty"`
    Labels       map[string]string `json:"labels,omitempty"`

    // 新增字段
    Architecture        string `json:"architecture,omitempty"`        // amd64 / arm64
    OperatingSystem     string `json:"operatingSystem,omitempty"`     // centos / ubuntu / kylin
    OperatingSystemVersion string `json:"operatingSystemVersion,omitempty"` // 7 / 8 / 20.04 / V10
}
```

#### 3.4.2 默认值与自动检测

```go
// pkg/phaseframe/phaseutil/node_detect.go

// DetectNodeArchitecture 自动检测节点架构
// 如果 BKENodeSpec.Architecture 未设置，通过 SSH 检测
func DetectNodeArchitecture(sshClient *bkessh.MultiCli, nodeIP string) string {
    result, err := sshClient.Execute(nodeIP, "uname -m")
    if err != nil {
        return "amd64" // 默认
    }
    arch := strings.TrimSpace(result.Stdout)
    switch arch {
    case "x86_64":
        return "amd64"
    case "aarch64":
        return "arm64"
    default:
        return arch
    }
}

// DetectNodeOS 自动检测节点操作系统
func DetectNodeOS(sshClient *bkessh.MultiCli, nodeIP string) (os, version string) {
    result, err := sshClient.Execute(nodeIP, "cat /etc/os-release")
    if err != nil {
        return "centos", "7" // 默认
    }

    output := result.Stdout
    if strings.Contains(output, "CentOS") {
        os = "centos"
    } else if strings.Contains(output, "Ubuntu") {
        os = "ubuntu"
    } else if strings.Contains(output, "Kylin") {
        os = "kylin"
    } else {
        os = "centos"
    }

    // 解析版本号
    versionLine := ""
    for _, line := range strings.Split(output, "\n") {
        if strings.HasPrefix(line, "VERSION_ID=") {
            versionLine = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
            break
        }
    }
    version = versionLine

    return os, version
}
```

---
## 4. BinaryInstaller 详细设计

### 4.1 核心组件架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            BinaryInstaller                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                         核心组件                                         │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                    ArtifactDownloader                            │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │  HTTP Client │  │ Cache Manager│  │ Checksum Verifier   │   │   │    │
│  │  │  │  (下载制品)   │  │ (本地缓存)   │  │ (校验和验证)        │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                     TemplateRenderer                             │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │  Go Template │  │ FuncMap      │  │ Variable Resolver   │   │   │    │
│  │  │  │  (模板解析)   │  │ (自定义函数) │  │ (变量解析)          │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                      ConfigRenderer                              │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │ Content Mode │  │ Secret Mode  │  │ Kubeconfig Mode     │   │   │    │
│  │  │  │ (模板渲染)   │  │ (Secret获取) │  │ (动态生成)          │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                       SSH Executor                               │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │ File Upload  │  │ Script Exec  │  │ Result Collector    │   │   │    │
│  │  │  │ (文件上传)   │  │ (脚本执行)   │  │ (结果收集)          │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 BinaryInstaller 执行流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         BinaryInstaller 执行流程                                 │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │   Install()      │
                              │   入口函数       │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  1. 解析架构和操作系统               │
                    │  arch = node.Spec.Architecture       │
                    │  os = node.Spec.OperatingSystem      │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  2. 下载二进制制品                    │
                    │  downloadArtifacts(ctx, binary,      │
                    │                    arch, os)         │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  检查缓存       │  │  解析 URL 模板  │  │  下载制品       │
          │  cache.Get()    │  │  resolveTemplate│  │  downloadAnd    │
          │                 │  │  ({{arch}}等)   │  │  Verify()       │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  3. 校验 Checksum                   │
                    │  verifyChecksum(data, expected)      │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         校验通过                校验失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  保存到缓存     │   │  返回错误       │
                    │  cache.Save()   │   │  return err     │
                    └────────┬────────┘   └─────────────────┘
                             │
                             ▼
                    ┌──────────────────────────────────────┐
                    │  4. 渲染安装脚本                    │
                    │  renderInstallScript(script,         │
                    │                      artifacts, opts)│
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  5. 渲染配置文件模板                │
                    │  renderConfigTemplates(templates,    │
                    │                       opts)          │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  Content 模式   │  │  Secret 模式    │  │  Kubeconfig     │
          │  Go template    │  │  从 Secret 获取 │  │  动态生成       │
          │  渲染           │  │  内容           │  │  kubeconfig     │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  6. SSH 执行安装                    │
                    │  executeInstall(ctx, node, script,   │
                    │                 artifacts, configs)  │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  上传二进制     │  │  上传配置       │  │  执行脚本       │
          │  ssh.Upload()   │  │  ssh.Upload()   │  │  ssh.Execute()  │
          │  到节点         │  │  到节点         │  │                 │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  7. 收集执行结果                    │
                    │  collectResult(stdout, stderr, err)  │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         执行成功                执行失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  返回成功       │   │  返回错误       │
                    │  return nil     │   │  return err     │
                    └─────────────────┘   │  (含 stdout/    │
                                          │   stderr)       │
                                          └─────────────────┘
```

### 4.3 核心接口定义

```go
// pkg/binaryinstaller/installer.go

// BinaryInstaller 二进制组件安装器
type BinaryInstaller struct {
    client     client.Client
    sshClient  *bkessh.MultiCli
    cacheDir   string
    httpClient *http.Client
    cache      *ArtifactCache
}

// InstallOptions 安装选项
type InstallOptions struct {
    Component   *ComponentVersion
    Node        *BKENode
    Cluster     *BKECluster
    Action      BinaryAction  // Install / Upgrade / Uninstall
    Timeout     time.Duration
    RetryCount  int
}

// BinaryAction 二进制操作类型
type BinaryAction string

const (
    BinaryActionInstall   BinaryAction = "Install"
    BinaryActionUpgrade   BinaryAction = "Upgrade"
    BinaryActionUninstall BinaryAction = "Uninstall"
)

// Install 执行二进制组件安装/升级
func (i *BinaryInstaller) Install(ctx context.Context, opts InstallOptions) error {
    component := opts.Component
    binary := component.Spec.Binary
    
    // 1. 解析架构和操作系统
    arch := opts.Node.Spec.Architecture
    os := opts.Node.Spec.OperatingSystem
    
    // 2. 下载二进制制品 (带缓存)
    artifacts, err := i.downloadArtifacts(ctx, binary, arch, os)
    if err != nil {
        return fmt.Errorf("failed to download artifacts: %w", err)
    }
    
    // 3. 渲染安装脚本
    script, err := i.renderInstallScript(binary.InstallScript, artifacts, opts)
    if err != nil {
        return fmt.Errorf("failed to render install script: %w", err)
    }
    
    // 4. 渲染配置文件模板
    configs, err := i.renderConfigTemplates(binary.ConfigTemplates, opts)
    if err != nil {
        return fmt.Errorf("failed to render config templates: %w", err)
    }
    
    // 5. SSH 执行安装
    switch opts.Action {
    case BinaryActionInstall, BinaryActionUpgrade:
        return i.executeInstall(ctx, opts.Node, script, artifacts, configs)
    case BinaryActionUninstall:
        return i.executeUninstall(ctx, opts.Node, binary.UninstallScript)
    }
    
    return nil
}

// downloadArtifacts 下载二进制制品
func (i *BinaryInstaller) downloadArtifacts(ctx context.Context, binary *BinarySpec, arch, os string) (map[string]*Artifact, error) {
    artifacts := make(map[string]*Artifact)
    
    for _, art := range binary.Artifacts {
        // 解析模板变量
        url, err := i.resolveTemplate(art.URL, map[string]string{
            "{{componentVersion}}": binary.Version,
            "{{nodeArch}}":        arch,
            "{{nodeOS}}":          os,
        })
        if err != nil {
            return nil, fmt.Errorf("failed to resolve URL template: %w", err)
        }
        
        // 检查缓存
        cacheKey := i.computeCacheKey(url, art.Checksum)
        if cached := i.cache.Get(cacheKey); cached != nil {
            artifacts[art.Name] = cached
            continue
        }
        
        // 下载并校验 checksum
        data, err := i.downloadAndVerify(ctx, url, art.Checksum)
        if err != nil {
            return nil, fmt.Errorf("failed to download artifact %s: %w", art.Name, err)
        }
        
        artifact := &Artifact{
            Name:     art.Name,
            URL:      url,
            Checksum: art.Checksum,
            Data:     data,
            Path:     i.cache.Save(cacheKey, data),
        }
        artifacts[art.Name] = artifact
    }
    
    return artifacts, nil
}

// executeInstall 通过 SSH 执行安装
func (i *BinaryInstaller) executeInstall(ctx context.Context, node *BKENode, script string, artifacts map[string]*Artifact, configs map[string][]byte) error {
    // 1. 创建远程目录
    if err := i.sshClient.Execute(node.IP, "mkdir -p /tmp/bke-install"); err != nil {
        return fmt.Errorf("failed to create remote directory: %w", err)
    }
    
    // 2. 上传二进制文件
    for name, art := range artifacts {
        remotePath := fmt.Sprintf("/tmp/bke-install/%s", name)
        if err := i.sshClient.Upload(node.IP, art.Data, remotePath); err != nil {
            return fmt.Errorf("failed to upload %s to %s: %w", name, node.IP, err)
        }
    }
    
    // 3. 上传配置文件
    for name, content := range configs {
        remotePath := fmt.Sprintf("/tmp/bke-install/%s", name)
        if err := i.sshClient.Upload(node.IP, content, remotePath); err != nil {
            return fmt.Errorf("failed to upload config %s to %s: %w", name, node.IP, err)
        }
    }
    
    // 4. 执行安装脚本
    result, err := i.sshClient.Execute(node.IP, script)
    if err != nil {
        return fmt.Errorf("install script failed on %s: %w\nstdout: %s\nstderr: %s", 
            node.IP, err, result.Stdout, result.Stderr)
    }
    
    return nil
}
```

---


### 4.4 核心类型定义



#### 4.4.1 Artifact 与 ArtifactCache 类型

```go
// pkg/binaryinstaller/types.go

// Artifact 表示已下载的二进制制品
type Artifact struct {
    // 制品名称 (来自 ArtifactSpec.Name)
    Name string
    // 解析后的实际下载 URL
    URL string
    // 期望的校验和 (来自 ArtifactSpec.Checksum)
    Checksum string
    // 下载后的制品内容
    Data []byte
    // 制品在本地缓存中的文件路径
    Path string
    // 制品的可执行文件名
    Executable string
    // 制品的安装路径
    InstallPath string
}

// ArtifactCache 管理二进制制品的本地文件缓存

> **设计思路 - 双文件缓存设计（.meta + .data）**：每个缓存项使用两个文件——`<key>.meta`（JSON 元数据）和 `<key>.data`（原始二进制内容）。选择双文件而非数据库（如 boltdb）的原因是：
> 1. **零依赖**：不需要引入额外的嵌入式数据库库，减少攻击面和依赖管理复杂度。
> 2. **直接 SSH 上传**：`<key>.data` 文件可直接通过 `CopyFile` 上传到远程节点，无需从数据库中读取二进制内容再写入临时文件。
> 3. **部分状态处理**：`Get` 方法在 meta 文件存在但 data 文件缺失时返回 nil（视为缓存未命中），data 文件存在但 meta 文件缺失时同样返回 nil，避免使用不完整的缓存数据。`Clean` 方法应同步清理孤立的 .data 文件（无对应 .meta）。
> 4. **改进方向**：后续可考虑使用单一文件（元数据作为 xattr 扩展属性存储），减少文件数量和部分状态风险，但需考虑文件系统对 xattr 的支持差异。
type ArtifactCache struct {
    // cacheDir 缓存根目录 (如 /var/cache/bke/artifacts)
    cacheDir string
}

// NewArtifactCache 创建缓存管理器
func NewArtifactCache(cacheDir string) (*ArtifactCache, error) {
    if err := os.MkdirAll(cacheDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create cache directory %s: %w", cacheDir, err)
    }
    return &ArtifactCache{cacheDir: cacheDir}, nil
}

// Get 从缓存中获取制品，返回 nil 表示未命中
func (c *ArtifactCache) Get(cacheKey string) *Artifact {
    metaPath := filepath.Join(c.cacheDir, cacheKey+".meta")
    dataPath := filepath.Join(c.cacheDir, cacheKey+".data")

    metaBytes, err := os.ReadFile(metaPath)
    if err != nil {
        return nil
    }

    var artifact Artifact
    if err := json.Unmarshal(metaBytes, &artifact); err != nil {
        return nil
    }

    // 验证数据文件是否存在
    if _, err := os.Stat(dataPath); err != nil {
        return nil
    }

    artifact.Path = dataPath
    return &artifact
}

// Save 将制品保存到缓存，返回数据文件路径
func (c *ArtifactCache) Save(cacheKey string, artifact *Artifact) string {
    metaPath := filepath.Join(c.cacheDir, cacheKey+".meta")
    dataPath := filepath.Join(c.cacheDir, cacheKey+".data")

    // 保存元数据
    meta := *artifact
    meta.Data = nil // 不将二进制内容序列化到 meta
    meta.Path = dataPath
    metaBytes, _ := json.Marshal(&meta)
    os.WriteFile(metaPath, metaBytes, 0644)

    // 保存数据文件
    os.WriteFile(dataPath, artifact.Data, 0644)

    return dataPath
}

// Clean 清理超过 maxAge 的缓存文件
func (c *ArtifactCache) Clean(maxAge time.Duration) error {
    entries, err := os.ReadDir(c.cacheDir)
    if err != nil {
        return err
    }
    cutoff := time.Now().Add(-maxAge)
    for _, entry := range entries {
        info, err := entry.Info()
        if err != nil {
            continue
        }
        if info.ModTime().Before(cutoff) {
            os.Remove(filepath.Join(c.cacheDir, entry.Name()))
        }
    }
    return nil
}
```

#### 4.4.2 ScriptData 与 ArtifactData 类型

```go
// pkg/binaryinstaller/template_data.go

// ScriptData 安装脚本模板渲染数据 (对应第 6.1 节 8 类变量)
type ScriptData struct {
    // ---- 1. 集群信息 ----
    ClusterName      string
    ClusterNamespace string
    APIServer        string
    APIServerPort    string
    ServiceCIDR      string
    PodCIDR          string
    DNSDomain        string
    ClusterDNS       string

    // ---- 2. 节点信息 ----
    NodeIP            string
    NodeHostname      string
    NodeRole          string // master / worker / etcd
    NodeArch          string // amd64 / arm64
    NodeOS            string // centos / ubuntu / kylin
    NodeOSVersion     string // 7 / 8 / 20.04 / V10
    NodeKernelVersion string
    NodeCPUs          int
    NodeMemoryMB      int
    NodeDiskGB        int

    // ---- 3. 版本信息 ----
    ComponentVersion         string
    ComponentPreviousVersion string
    ClusterVersion           string
    EtcdVersion              string
    ContainerdVersion        string
    BKEAgentVersion          string

    // ---- 4. 二进制制品 ----
    Artifact map[string]*ArtifactData

    // ---- 5. 镜像仓库 ----
    ImageRegistry   string
    ImagePullSecret string
    ImageNamespace  string

    // ---- 6. 安装路径 ----
    InstallPath string // 默认 /usr/local/bin
    ConfigPath  string // 默认 /etc/<component>
    LogPath     string // 默认 /var/log/<component>
    DataPath    string // 默认 /var/lib/<component>
    BinPath     string // 默认 /usr/local/bin

    // ---- 7. 操作类型 ----
    Action    string // Install / Upgrade / Uninstall
    IsUpgrade bool
    IsInstall bool

    // ---- 8. 自定义变量 ----
    Variables map[string]string
}

// ArtifactData 制品模板数据
type ArtifactData struct {
    Name     string
    Path     string // 制品在节点上的远程路径
    URL      string // 制品原始 URL
    Checksum string // 制品校验和
    Filename string // 制品文件名
}

// BuildScriptData 从 InstallOptions 构建模板渲染数据

> **设计思路 - BuildScriptData vs buildTemplateData 的不一致**：`BuildScriptData` 返回类型化的 `ScriptData` 结构体（用于 Go template 渲染，字段名大写如 `ClusterName`），`buildTemplateData` 返回 `map[string]interface{}`（用于 ConfigRenderer 渲染，key 小写如 `clusterName`）。两者存在的原因是：
> 1. **调用方不同**：`BuildScriptData` 由 BinaryInstaller 调用，注入到 Go template 的 `.Field` 语法中，大写字段名符合 Go 导出规范。
> 2. **ConfigRenderer 的灵活性**：`buildTemplateData` 生成的 map 直接用于模板变量替换，小写 key 与 Helm Values 风格一致（`{{.clusterName}}`）。
> 3. **改进方向**：后续应统一为单一数据源——定义 `TemplateData` 结构体，同时提供 `.Field`（Go template）和 `ToMap()`（map 形式）两种访问方式，消除字段名不一致的混乱。
func BuildScriptData(opts InstallOptions) ScriptData {
    component := opts.Component
    cluster := opts.Cluster
    node := opts.Node
    binary := component.Spec.Binary

    data := ScriptData{
        ClusterName:      cluster.Name,
        ClusterNamespace: cluster.Namespace,
        ClusterVersion:   cluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
        NodeIP:           node.Spec.IP,
        NodeHostname:     node.Spec.Hostname,
        NodeRole:         node.Spec.Role,
        NodeArch:         node.Spec.Architecture,
        NodeOS:           node.Spec.OperatingSystem,
        NodeOSVersion:    node.Spec.OperatingSystemVersion,
        ComponentVersion: component.Spec.Version,
        Action:           string(opts.Action),
        IsUpgrade:        opts.Action == BinaryActionUpgrade,
        IsInstall:        opts.Action == BinaryActionInstall,
        Variables:        binary.Variables,
        Artifact:         make(map[string]*ArtifactData),
    }

    // 集群网络信息
    if cluster.Spec.ClusterConfig.Network != nil {
        data.ServiceCIDR = cluster.Spec.ClusterConfig.Network.ServiceCIDR
        data.PodCIDR = cluster.Spec.ClusterConfig.Network.PodCIDR
    }

    // 镜像仓库信息
    if cluster.Spec.ClusterConfig.Registry != nil {
        data.ImageRegistry = cluster.Spec.ClusterConfig.Registry.Endpoint
    }

    // 安装路径 (从 BinarySpec 默认值)
    data.InstallPath = binary.DefaultInstallPath
    if data.InstallPath == "" {
        data.InstallPath = "/usr/local/bin"
    }
    data.ConfigPath = binary.DefaultConfigPath
    if data.ConfigPath == "" {
        data.ConfigPath = fmt.Sprintf("/etc/%s", component.Spec.Name)
    }
    data.LogPath = binary.DefaultLogPath
    if data.LogPath == "" {
        data.LogPath = fmt.Sprintf("/var/log/%s", component.Spec.Name)
    }
    data.DataPath = binary.DefaultDataPath
    if data.DataPath == "" {
        data.DataPath = fmt.Sprintf("/var/lib/%s", component.Spec.Name)
    }
    data.BinPath = binary.DefaultBinPath
    if data.BinPath == "" {
        data.BinPath = "/usr/local/bin"
    }

    return data
}
```

---


### 4.5 核心方法实现



#### 4.5.1 resolveTemplate - URL 模板变量解析

```go
// resolveTemplate 将 URL 中的 {{componentVersion}}/{{nodeArch}}/{{nodeOS}} 替换为实际值

> **设计思路 - 简单替换 vs Go template**：URL/路径变量使用 `strings.ReplaceAll` 而非 Go template，原因见 3.1 节 BinarySpec 的"双模板变量系统"设计思路。核心权衡：URL 是纯字符串拼接场景，不需要条件/循环逻辑，简单替换无解析错误风险、零依赖，且在下载阶段早期执行时节点上下文有限，不适合引入完整模板引擎。
func (i *BinaryInstaller) resolveTemplate(tmpl string, vars map[string]string) (string, error) {
    result := tmpl
    for key, value := range vars {
        result = strings.ReplaceAll(result, key, value)
    }
    // 检查是否还有未解析的变量
    if strings.Contains(result, "{{") && strings.Contains(result, "}}") {
        return "", fmt.Errorf("unresolved template variables in %q", result)
    }
    return result, nil
}
```

#### 4.5.2 computeCacheKey - 缓存键计算

```go
// computeCacheKey 基于 URL 和 Checksum 生成缓存键
// 格式: sha256(url + ":" + checksum) 的前 16 位

> **设计思路 - 16字符截断**：截断到 16 个十六进制字符（64 bit）是为了生成文件系统友好的文件名——全 SHA256（64 字符）过长，且缓存键用于本地文件名而非安全场景，碰撞后果仅为缓存未命中（重新下载），不影响正确性。实际上 URL+Checksum 组合本身已足够唯一，SHA256 仅用于规范化（去除特殊字符）。
func (i *BinaryInstaller) computeCacheKey(url, checksum string) string {
    h := sha256.New()
    h.Write([]byte(url + ":" + checksum))
    return hex.EncodeToString(h.Sum(nil))[:16]
}
```

#### 4.5.3 downloadAndVerify - 下载制品并校验

```go
// downloadAndVerify 下载制品并校验 Checksum

> **设计思路 - 500MB 硬编码限制**：`io.LimitReader(resp.Body, 500*1024*1024)` 防止异常响应耗尽内存。500MB 基于 BKE 组件制品的实际上限（containerd ~100MB、bkeagent ~50MB）预留了充足余量。当前为硬编码，后续应提升为 `BinaryInstaller` 的可配置参数。将整个制品读入内存而非流式写入磁盘，是因为后续需要 `sha256.Sum256(data)` 计算校验和，且制品需要缓存到本地文件供 SSH 上传使用。
func (i *BinaryInstaller) downloadAndVerify(ctx context.Context, url, expectedChecksum string) ([]byte, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request for %s: %w", url, err)
    }

    resp, err := i.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to download %s: %w", url, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("download %s returned status %d", url, resp.StatusCode)
    }

    data, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024*1024)) // 500MB 上限
    if err != nil {
        return nil, fmt.Errorf("failed to read response body from %s: %w", url, err)
    }

    // 校验 Checksum
    if err := verifyChecksum(data, expectedChecksum); err != nil {
        return nil, fmt.Errorf("checksum verification failed for %s: %w", url, err)
    }

    return data, nil
}

// verifyChecksum 校验数据的 Checksum
// expectedChecksum 格式: "sha256:abc123..."
func verifyChecksum(data []byte, expectedChecksum string) error {
    if expectedChecksum == "" {
        return nil
    }

    parts := strings.SplitN(expectedChecksum, ":", 2)
    if len(parts) != 2 {
        return fmt.Errorf("invalid checksum format: %s", expectedChecksum)
    }

    algorithm := parts[0]
    expected := parts[1]

    switch algorithm {
    case "sha256":
        h := sha256.Sum256(data)
        actual := hex.EncodeToString(h[:])
        if actual != expected {
            return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
        }
    case "sha512":
        h := sha512.Sum512(data)
        actual := hex.EncodeToString(h[:])
        if actual != expected {
            return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
        }
    default:
        return fmt.Errorf("unsupported checksum algorithm: %s", algorithm)
    }

    return nil
}
```

#### 4.5.4 renderInstallScript - 渲染安装脚本

```go
// renderInstallScript 使用 TemplateRenderer 渲染安装脚本
func (i *BinaryInstaller) renderInstallScript(script string, artifacts map[string]*Artifact, opts InstallOptions) (string, error) {
    renderer := NewTemplateRenderer()
    data := BuildScriptData(opts)

    // 构建制品数据
    data.Artifact = make(map[string]*ArtifactData)
    for name, art := range artifacts {
        data.Artifact[name] = &ArtifactData{
            Name:     art.Name,
            Path:     art.Path,
            URL:      art.URL,
            Checksum: art.Checksum,
            Filename: filepath.Base(art.Path),
        }
    }

    return renderer.RenderScript(script, data)
}
```

#### 4.5.5 renderConfigTemplates - 渲染配置文件模板

```go
// renderConfigTemplates 渲染所有配置文件模板
func (i *BinaryInstaller) renderConfigTemplates(templates []ConfigTemplateSpec, opts InstallOptions) (map[string][]byte, error) {
    renderer := &ConfigRenderer{
        client:  i.client,
        funcMap: NewTemplateRenderer().funcMap,
    }

    configs := make(map[string][]byte)
    for _, tmpl := range templates {
        content, err := renderer.RenderConfig(context.Background(), tmpl, opts)
        if err != nil {
            return nil, fmt.Errorf("failed to render config template %s: %w", tmpl.Name, err)
        }
        configs[tmpl.Name] = content
    }

    return configs, nil
}
```

---

## 5. HelmInstaller 详细设计

### 5.1 核心组件架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                             HelmInstaller                                        │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                         核心组件                                         │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                        ChartFetcher                              │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │  OCI Client  │  │  HTTP Client │  │  Local Loader       │   │   │    │
│  │  │  │  (OCI拉取)   │  │  (HTTP下载)  │  │  (本地加载)         │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                       ValuesRenderer                             │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │  Template    │  │  Values      │  │  Merge Strategy     │   │   │    │
│  │  │  │  Resolver    │  │  File Loader │  │  (合并策略)         │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                    Helm Action Executor                          │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │  Install     │  │  Upgrade     │  │  Rollback           │   │   │    │
│  │  │  │  Action      │  │  Action      │  │  Action             │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐                           │   │    │
│  │  │  │  Uninstall   │  │  Wait/Atomic │                           │   │    │
│  │  │  │  Action      │  │  Control     │                           │   │    │
│  │  │  └──────────────┘  └──────────────┘                           │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                       HealthChecker                              │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │  PodReady    │  │  Endpoint    │  │  Custom Check       │   │   │    │
│  │  │  │  Check       │  │  Ready Check │  │                     │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 HelmInstaller 执行流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          HelmInstaller 执行流程                                  │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │   Install()      │
                              │   入口函数       │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  1. 获取 Chart                      │
                    │  getChart(ctx, helm.Chart)           │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  OCI Registry   │  │  HTTP URL       │  │  Local Path     │
          │  helm pull      │  │  http.Get()     │  │  os.Open()      │
          │  oci://...      │  │  https://...    │  │  /path/to/chart │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  2. 校验 Chart Checksum             │
                    │  verifyChecksum(chart, expected)     │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         校验通过                校验失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  继续           │   │  返回错误       │
                    └────────┬────────┘   └─────────────────┘
                             │
                             ▼
                    ┌──────────────────────────────────────┐
                    │  3. 渲染 Values                     │
                    │  renderValues(ctx, helm.Values, opts)│
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  4. 加载自定义 Values 文件          │
                    │  loadValuesFiles(ctx, helm.Values    │
                    │                   Files, opts)       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  5. 合并 Values                     │
                    │  mergeValues(base, custom)           │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  6. 执行 Helm Action                │
                    │  executeAction(ctx, actionConfig,    │
                    │                chart, values, helm)  │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  Install        │  │  Upgrade        │  │  Rollback       │
          │  action.New     │  │  action.New     │  │  action.New     │
          │  Install()      │  │  Upgrade()      │  │  Rollback()     │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  7. 执行健康检查                    │
                    │  runHealthCheck(ctx, helm.           │
                    │                HealthCheck, opts)    │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  PodReady       │  │  EndpointReady  │  │  Custom         │
          │  检查 Pod 状态  │  │  检查 Endpoint  │  │  自定义检查     │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                              ┌──────────┴──────────┐
                              │                     │
                         检查通过                检查失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  返回成功       │   │  返回错误       │
                    │  return nil     │   │  (触发回滚)     │
                    └─────────────────┘   └─────────────────┘
```

### 5.3 核心接口定义

```go
// pkg/helminstaller/installer.go

// HelmInstaller Helm 组件安装器
type HelmInstaller struct {
    client     client.Client
    restConfig *rest.Config
    cacheDir   string
    httpClient *http.Client
    ociClient  *oci.Client
}

// InstallOptions Helm 安装选项
type InstallOptions struct {
    Component   *ComponentVersion
    Cluster     *BKECluster
    Action      HelmAction  // Install / Upgrade / Uninstall / Rollback
    Timeout     time.Duration
    DryRun      bool
}

// HelmAction Helm 操作类型
type HelmAction string

const (
    HelmActionInstall   HelmAction = "Install"
    HelmActionUpgrade   HelmAction = "Upgrade"
    HelmActionUninstall HelmAction = "Uninstall"
    HelmActionRollback  HelmAction = "Rollback"
)

// Install 执行 Helm 组件安装/升级
func (i *HelmInstaller) Install(ctx context.Context, opts InstallOptions) error {
    component := opts.Component
    helm := component.Spec.Helm
    
    // 1. 获取 Chart
    chart, err := i.getChart(ctx, helm.Chart)
    if err != nil {
        return fmt.Errorf("failed to get chart: %w", err)
    }
    
    // 2. 渲染 Values
    values, err := i.renderValues(ctx, helm.Values, opts)
    if err != nil {
        return fmt.Errorf("failed to render values: %w", err)
    }
    
    // 3. 加载自定义 Values 文件
    // 设计思路 - 文件加载失败为警告而非错误：ValuesFiles 是可选的补充配置，文件可能按条件存在
    // （如 values-{{nodeOS}}.yaml 在某些操作系统上不存在），缺失时使用内联 Values 即可。
    // 如果文件路径不含模板变量且文件不存在，说明配置有误，应在日志中标记为 Warning 级别
    // 便于运维排查，同时不中断安装流程。
    for _, vf := range helm.ValuesFiles {
        customValues, err := i.loadValuesFile(ctx, vf, opts)
        if err != nil {
            log.Warn("failed to load values file %s: %v", vf, err)
            continue
        }
        values = mergeValues(values, customValues)
    }
    
    // 4. 获取 Action Config
    actionConfig, err := i.getActionConfig(ctx, helm.Namespace)
    if err != nil {
        return fmt.Errorf("failed to get action config: %w", err)
    }
    
    // 5. 执行 Helm Action
    switch opts.Action {
    case HelmActionInstall:
        return i.install(ctx, actionConfig, chart, values, helm, opts)
    case HelmActionUpgrade:
        return i.upgrade(ctx, actionConfig, chart, values, helm, opts)
    case HelmActionUninstall:
        return i.uninstall(ctx, actionConfig, helm, opts)
    case HelmActionRollback:
        return i.rollback(ctx, actionConfig, helm, opts)
    }
    
    return nil
}

// getChart 获取 Chart
func (i *HelmInstaller) getChart(ctx context.Context, chartSpec ChartSpec) (*chart.Chart, error) {
    if chartSpec.OCI != nil {
        return i.getChartFromOCI(ctx, chartSpec.OCI)
    }
    if chartSpec.URL != "" {
        return i.getChartFromURL(ctx, chartSpec.URL)
    }
    if chartSpec.LocalPath != "" {
        return i.getChartFromLocal(ctx, chartSpec.LocalPath)
    }
    return nil, errors.New("no chart source specified")
}

// install 执行 Helm Install
func (i *HelmInstaller) install(ctx context.Context, actionConfig *action.Configuration, 
    chart *chart.Chart, values map[string]interface{}, helm *HelmSpec, opts InstallOptions) error {
    
    client := action.NewInstall(actionConfig)
    client.ReleaseName = helm.ReleaseName
    client.Namespace = helm.Namespace
    client.CreateNamespace = true
    client.Wait = helm.Strategy.Wait
    client.Timeout, _ = time.ParseDuration(helm.Strategy.WaitTimeout)
    client.Atomic = helm.Strategy.Atomic
    client.DryRun = opts.DryRun
    
    release, err := client.Run(chart, values)
    if err != nil {
        if helm.Strategy.CleanupOnFail && release != nil {
            uninstallClient := action.NewUninstall(actionConfig)
            uninstallClient.Run(release.Name)
        }
        return fmt.Errorf("helm install failed: %w", err)
    }
    
    // 执行健康检查
    if helm.HealthCheck.Enabled {
        if err := i.runHealthCheck(ctx, helm.HealthCheck, opts); err != nil {
            return fmt.Errorf("health check failed after install: %w", err)
        }
    }
    
    return nil
}

// upgrade 执行 Helm Upgrade
func (i *HelmInstaller) upgrade(ctx context.Context, actionConfig *action.Configuration,
    chart *chart.Chart, values map[string]interface{}, helm *HelmSpec, opts InstallOptions) error {
    
    client := action.NewUpgrade(actionConfig)
    client.Namespace = helm.Namespace
    client.Wait = helm.Strategy.Wait
    client.Timeout, _ = time.ParseDuration(helm.Strategy.WaitTimeout)
    client.Atomic = helm.Strategy.Atomic
    client.MaxHistory = helm.Rollback.MaxHistory
    client.DryRun = opts.DryRun
    
    release, err := client.Run(helm.ReleaseName, chart, values)
    if err != nil {
        if helm.Strategy.CleanupOnFail && release != nil {
            uninstallClient := action.NewUninstall(actionConfig)
            uninstallClient.Run(release.Name)
        }
        return fmt.Errorf("helm upgrade failed: %w", err)
    }
    
    // 执行健康检查
    if helm.HealthCheck.Enabled {
        if err := i.runHealthCheck(ctx, helm.HealthCheck, opts); err != nil {
            return fmt.Errorf("health check failed after upgrade: %w", err)
        }
    }
    
    return nil
}
```

---

### 5.4 核心方法实现



#### 5.4.1 uninstall - Helm 卸载

```go
// uninstall 执行 Helm Uninstall
func (i *HelmInstaller) uninstall(ctx context.Context, actionConfig *action.Configuration, helm *HelmSpec, opts InstallOptions) error {
    client := action.NewUninstall(actionConfig)
    client.Wait = true
    client.Timeout, _ = time.ParseDuration(helm.Strategy.WaitTimeout)

    _, err := client.Run(helm.ReleaseName)
    if err != nil {
        return fmt.Errorf("helm uninstall failed: %w", err)
    }
    return nil
}
```

#### 5.4.2 rollback - Helm 回滚

```go
// rollback 执行 Helm Rollback
func (i *HelmInstaller) rollback(ctx context.Context, actionConfig *action.Configuration, helm *HelmSpec, opts InstallOptions) error {
    client := action.NewRollback(actionConfig)
    client.Wait = true
    client.Timeout, _ = time.ParseDuration(helm.Strategy.WaitTimeout)

    if err := client.Run(helm.ReleaseName); err != nil {
        return fmt.Errorf("helm rollback failed: %w", err)
    }
    return nil
}
```

#### 5.4.3 runHealthCheck - 健康检查

```go
// runHealthCheck 执行健康检查
func (i *HelmInstaller) runHealthCheck(ctx context.Context, spec HealthCheckSpec, opts InstallOptions) error {
    if !spec.Enabled {
        return nil
    }

    timeout := parseDuration(spec.Timeout)
    interval := parseDuration(spec.Interval)
    if interval == 0 {
        interval = 10 * time.Second
    }

    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return fmt.Errorf("health check timed out after %s", timeout)
        case <-ticker.C:
            allReady := true
            for _, check := range spec.Checks {
                switch check.Type {
                case "PodReady":
                    ready, err := i.checkPodReady(ctx, check, opts)
                    if err != nil || !ready {
                        allReady = false
                    }
                case "EndpointReady":
                    ready, err := i.checkEndpointReady(ctx, check, opts)
                    if err != nil || !ready {
                        allReady = false
                    }
                }
            }
            if allReady {
                return nil
            }
        }
    }
}

// checkPodReady 检查 Pod 是否就绪
func (i *HelmInstaller) checkPodReady(ctx context.Context, check HealthCheckItemSpec, opts InstallOptions) (bool, error) {
    pods := &corev1.PodList{}
    labelSel, _ := labels.Parse(check.LabelSelector)
    if err := i.client.List(ctx, pods,
        client.InNamespace(check.Namespace),
        client.MatchingLabelsSelector{Selector: labelSel},
    ); err != nil {
        return false, err
    }

    readyCount := int32(0)
    for _, pod := range pods.Items {
        for _, c := range pod.Status.Conditions {
            if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
                readyCount++
                break
            }
        }
    }

    return readyCount >= check.MinReady, nil
}

// checkEndpointReady 检查 Endpoint 是否就绪
func (i *HelmInstaller) checkEndpointReady(ctx context.Context, check HealthCheckItemSpec, opts InstallOptions) (bool, error) {
    ep := &corev1.Endpoints{}
    if err := i.client.Get(ctx, types.NamespacedName{
        Name:      check.Name,
        Namespace: check.Namespace,
    }, ep); err != nil {
        return false, err
    }

    for _, subset := range ep.Subsets {
        if len(subset.Addresses) > 0 {
            return true, nil
        }
    }
    return false, nil
}
```

#### 5.4.4 getActionConfig - Helm Action 配置初始化

```go
// getActionConfig 获取 Helm Action 配置
func (i *HelmInstaller) getActionConfig(ctx context.Context, namespace string) (*action.Configuration, error) {
    actionConfig := new(action.Configuration)
    getter := helmCLI.NewConfigFlagsFromConfig(i.restConfig)
    if err := actionConfig.Init(getter, namespace, "secret", func(format string, v ...interface{}) {
        // Helm log output
    }); err != nil {
        return nil, fmt.Errorf("failed to initialize helm action config: %w", err)
    }
    return actionConfig, nil
}
```

#### 5.4.5 loadValuesFile - 加载自定义 Values 文件

```go
// loadValuesFile 加载自定义 Values 文件

> **设计思路 - 本地文件系统 vs ConfigMap/URL**：当前从控制器 Pod 的本地文件系统读取 Values 文件。在 Kubernetes 控制器场景下，文件通常通过 ConfigMap 或 Secret 挂载到 Pod 中。选择本地文件而非直接读取 ConfigMap API 的原因是：① Values 文件可能很大（超过 etcd 1MB 限制），ConfigMap 不适合存储大型 YAML；② 文件路径支持模板变量渲染，动态选择不同文件（如 `values-{{nodeOS}}.yaml`）。后续可扩展支持 HTTP URL 和 ConfigMap 引用作为 Values 来源。
func (i *HelmInstaller) loadValuesFile(ctx context.Context, valuesFile string, opts InstallOptions) (map[string]interface{}, error) {
    // 渲染文件名中的模板变量
    renderedFile, err := i.renderTemplateString(valuesFile, opts)
    if err != nil {
        return nil, fmt.Errorf("failed to render values file path %s: %w", valuesFile, err)
    }

    data, err := os.ReadFile(renderedFile)
    if err != nil {
        return nil, fmt.Errorf("failed to read values file %s: %w", renderedFile, err)
    }

    values := make(map[string]interface{})
    if err := yaml.Unmarshal(data, &values); err != nil {
        return nil, fmt.Errorf("failed to parse values file %s: %w", renderedFile, err)
    }

    return values, nil
}
```

#### 5.4.6 mergeValues - Values 合并策略

```go
// mergeValues 合并两份 Values (dst 被 src 覆盖)
// 策略: 递归合并 map，src 中的值覆盖 dst 中的同名键

> **设计思路 - 列表整体替换**：递归合并 map 时，如果 `src` 和 `dst` 的同名字段一个是 map 一个不是，或都是非 map 类型（如 list/string/int），则 `src` 的值整体替换 `dst` 的值。这意味着列表（如 `container.args`、`env`）会被完全覆盖而非追加合并。选择此策略的原因是：
> 1. **确定性**：追加合并会产生顺序依赖问题（哪个列表在前？去重逻辑？），整体替换行为确定、可预测。
> 2. **Helm 价值观一致**：Helm 自身的 `--set` 和 `-f values.yaml` 合并行为也是相同策略（非 map 字段整体替换），与 Helm 生态保持一致。
> 3. **使用建议**：如果需要追加列表项，应在最高优先级的 Values 中给出完整列表，而非依赖合并追加。
func mergeValues(dst, src map[string]interface{}) map[string]interface{} {
    result := make(map[string]interface{})
    for k, v := range dst {
        result[k] = v
    }
    for k, v := range src {
        if dstMap, ok := dst[k].(map[string]interface{}); ok {
            if srcMap, ok := v.(map[string]interface{}); ok {
                result[k] = mergeValues(dstMap, srcMap)
                continue
            }
        }
        result[k] = v
    }
    return result
}
```

#### 5.4.7 renderString - ConfigRenderer 字符串模板渲染

```go
// renderString 渲染字符串中的模板变量
func (r *ConfigRenderer) renderString(tmpl string, opts InstallOptions) (string, error) {
    if tmpl == "" {
        return "", nil
    }

    // 简单变量替换: {{.key}} → value
    data := r.buildTemplateData(context.Background(), opts)
    t, err := template.New("renderString").Parse(tmpl)
    if err != nil {
        return tmpl, nil // 非模板字符串直接返回
    }

    var buf bytes.Buffer
    if err := t.Execute(&buf, data); err != nil {
        return "", fmt.Errorf("failed to render string %q: %w", tmpl, err)
    }
    return buf.String(), nil
}
```

---


### 5.5 PreInstallHooks 执行引擎

> **设计思路 - 自定义钩子 vs Helm 原生钩子**：不使用 Helm 原生钩子注解（`helm.sh/hook`）而自行实现钩子系统，原因是：
> 1. **执行时机控制**：Helm 原生钩子在 `helm install/upgrade` 命令内部执行，无法在 Helm 操作之前独立运行前置检查。而我们的场景需要在 Helm Install 之前执行前置任务（如数据库迁移、证书准备），这些任务必须在 Helm 操作之前成功完成。
> 2. **超时与重试**：Helm 钩子的超时由 Helm SDK 全局控制，自定义钩子可针对每个钩子配置独立的超时和重试策略。
> 3. **清理策略**：Helm 钩子清理由 Helm 的 `hook-delete-policy` 注解控制，自定义钩子可在安装成功/失败后灵活决定是否保留 Job 资源。
> 4. **与 Helm 钩子的关系**：自定义钩子不替代 Helm 钩子，而是补充。Helm Chart 内部的钩子（如 post-install 测试）仍由 Helm SDK 管理，`PreInstallHooks` 仅管理 ComponentVersion CRD 中声明的外部前置任务。

#### 5.5.1 PreInstallHooks 执行引擎

```go
// pkg/helminstaller/hooks.go

// executePreInstallHooks 执行安装前钩子
func (i *HelmInstaller) executePreInstallHooks(ctx context.Context, hooks []HookSpec, opts InstallOptions) error {
    for _, hook := range hooks {
        if hook.Type != "Job" {
            continue
        }

        // 渲染钩子 Manifest 中的模板变量
        manifest, err := i.renderTemplateString(hook.Manifest, opts)
        if err != nil {
            return fmt.Errorf("failed to render hook %s manifest: %w", hook.Name, err)
        }

        // Apply Job Manifest
        obj, err := i.applyHookManifest(ctx, manifest, opts)
        if err != nil {
            return fmt.Errorf("failed to apply hook %s: %w", hook.Name, err)
        }

        // 等待 Job 完成
        if err := i.waitForHookJob(ctx, obj, opts); err != nil {
            return fmt.Errorf("hook %s failed: %w", hook.Name, err)
        }

        // 清理 Job
        i.cleanupHookJob(ctx, obj, opts)
    }
    return nil
}

// applyHookManifest 应用钩子 Manifest
func (i *HelmInstaller) applyHookManifest(ctx context.Context, manifest string, opts InstallOptions) (*unstructured.Unstructured, error) {
    decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
    var raw unstructured.Unstructured
    if err := decoder.Decode(&raw); err != nil {
        return nil, fmt.Errorf("failed to decode hook manifest: %w", err)
    }

    if err := i.client.Create(ctx, &raw); err != nil {
        if !kerrors.IsAlreadyExists(err) {
            return nil, fmt.Errorf("failed to create hook resource: %w", err)
        }
    }

    return &raw, nil
}

// waitForHookJob 等待钩子 Job 完成
func (i *HelmInstaller) waitForHookJob(ctx context.Context, obj *unstructured.Unstructured, opts InstallOptions) error {
    name := obj.GetName()
    namespace := obj.GetNamespace()

    return wait.PollUntilContextTimeout(ctx, 2*time.Second, 5*time.Minute, true,
        func(ctx context.Context) (bool, error) {
            job := &batchv1.Job{}
            if err := i.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, job); err != nil {
                return false, err
            }
            for _, c := range job.Status.Conditions {
                if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
                    return true, nil
                }
                if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
                    return false, fmt.Errorf("hook job %s failed: %s", name, c.Message)
                }
            }
            return false, nil
        },
    )
}

// cleanupHookJob 清理钩子 Job
func (i *HelmInstaller) cleanupHookJob(ctx context.Context, obj *unstructured.Unstructured, opts InstallOptions) {
    i.client.Delete(ctx, obj)
}
```

#### 5.5.2 HelmInstaller.Install 集成 Hooks

```go
// Install 方法补充: 在 Helm Install/Upgrade 前执行 PreInstallHooks
func (i *HelmInstaller) Install(ctx context.Context, opts InstallOptions) error {
    component := opts.Component
    helm := component.Spec.Helm

    // 0. 执行前置钩子
    if len(helm.PreInstallHooks) > 0 {
        if err := i.executePreInstallHooks(ctx, helm.PreInstallHooks, opts); err != nil {
            return fmt.Errorf("pre-install hooks failed: %w", err)
        }
    }

    // 1. 获取 Chart (同原设计)
    chart, err := i.getChart(ctx, helm.Chart)
    if err != nil {
        return fmt.Errorf("failed to get chart: %w", err)
    }

    // ... (后续逻辑同原设计 5.3 节)
}
```

---

## 6. YAMLManifestExecutor 详细设计

YAML 类型组件是四种组件类型中最基础的一种，负责将 Kubernetes YAML 清单直接应用到目标集群。当前代码中 `Scheduler.executeManifest()` 方法即为 YAML 执行路径的基础实现，本章节在此基础上扩展为完整的 YAMLManifestExecutor 设计。

### 6.1 核心组件架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         YAMLManifestExecutor                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                         核心组件                                         │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                    ManifestDownloader                            │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │  HTTP Client │  │ Cache Manager│  │ Checksum Verifier   │   │   │    │
│  │  │  │  (下载清单)   │  │ (本地缓存)   │  │ (校验和验证)        │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                      YAML Parser                                 │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │ Multi-Doc    │  │ GVK Resolver │  │ Resource Grouper    │   │   │    │
│  │  │  │ (多文档解析)  │  │ (类型识别)   │  │ (资源分组)          │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                    ApplyStrategy Engine                          │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │   │    │
│  │  │  │ ServerSide   │  │ Replace      │  │ CreateOnly          │   │   │    │
│  │  │  │ Apply        │  │ (替换更新)   │  │ (仅创建)            │   │   │    │
│  │  │  │ (服务端应用) │  │              │  │                     │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                       K8s Applier                                │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐   │    │    │
│  │  │  │ KubeClient   │  │ Prune Engine │  │ Result Collector    │   │   │    │
│  │  │  │ (资源应用)   │  │ (资源裁剪)   │  │ (结果收集)          │   │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘   │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  与现有代码的映射:                                                                │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ ManifestDownloader  ← manifest.Store.GetComponentManifests()           │    │
│  │ K8s Applier         ← manifest.Applier.ApplyComponent()               │    │
│  │ KubeClient          ← kube.RemoteKubeClient.ApplyYaml()               │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 6.2 YAMLManifestExecutor 执行流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                      YAMLManifestExecutor 执行流程                                │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │   Execute()      │
                              │   入口函数       │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  1. 版本检查                         │
                    │  manifestNeedsUpgrade()              │
                    │  VersionContext.NeedsUpgrade()       │
                    └────────────────────┬─────────────────┘
                                       │
                              ┌────────┴────────┐
                              │ 需要升级?       │
                              └────────┬────────┘
                                否     │     是
                           ┌───────────┘──────────┐
                           │                      │
                           ▼                      ▼
                    ┌────────────┐  ┌──────────────────────────────────────┐
                    │  跳过执行  │  │  2. 下载清单                         │
                    │  返回 nil  │  │  ManifestStore.GetComponentManifests │
                    └────────────┘  │  或从 YAMLSpec.Manifests URL 下载    │
                                    └────────────────────┬─────────────────┘
                                                         │
                                                         ▼
                                    ┌──────────────────────────────────────┐
                                    │  3. 校验清单                         │
                                    │  checksum 验证 (如配置)              │
                                    │  空清单检查                          │
                                    └────────────────────┬─────────────────┘
                                                         │
                                                         ▼
                                    ┌──────────────────────────────────────┐
                                    │  4. 解析 YAML 多文档                 │
                                    │  yaml.NewYAMLOrJSONDecoder()         │
                                    │  分离为独立 Unstructured 对象        │
                                    └────────────────────┬─────────────────┘
                                                         │
                                                         ▼
                                    ┌──────────────────────────────────────┐
                                    │  5. 按策略应用资源                   │
                                    │  switch ApplyStrategy                │
                                    └────────────────────┬─────────────────┘
                                                         │
                          ┌──────────────────────────────┼──────────────────────────────┐
                          │                              │                              │
                          ▼                              ▼                              ▼
               ┌─────────────────┐          ┌─────────────────┐          ┌─────────────────┐
               │ ServerSideApply │          │    Replace      │          │  CreateOnly     │
               │                 │          │                 │          │                 │
               │ PatchOptions:   │          │ Update 或       │          │ Create 仅当     │
               │ FieldManager    │          │ Delete+Create   │          │ 资源不存在时    │
               │ Force=false     │          │ (强制替换)      │          │ 才创建          │
               └────────┬────────┘          └────────┬────────┘          └────────┬────────┘
                        │                            │                            │
                        └────────────────────────────┼────────────────────────────┘
                                                     │
                                                     ▼
                                    ┌──────────────────────────────────────┐
                                    │  6. 资源裁剪 (可选)                  │
                                    │  if Prune == true                    │
                                    │    按 PruneLabelSelector 查询资源    │
                                    │    删除不在当前清单中的资源           │
                                    └────────────────────┬─────────────────┘
                                                         │
                                                         ▼
                                    ┌──────────────────────────────────────┐
                                    │  7. 返回执行结果                     │
                                    │  记录已应用资源列表                  │
                                    │  回写组件状态                        │
                                    └──────────────────────────────────────┘
```

### 6.3 核心接口定义

```go
// pkg/yamlexecutor/types.go

// ApplyStrategy 定义 YAML 资源应用策略
type ApplyStrategy string

const (
    // ApplyStrategyServerSideApply 使用 Server-Side Apply 方式应用资源
    ApplyStrategyServerSideApply ApplyStrategy = "ServerSideApply"
    
    // ApplyStrategyReplace 使用替换方式更新资源 (先删除再创建)
    ApplyStrategyReplace ApplyStrategy = "Replace"
    
    // ApplyStrategyCreateOnly 仅在资源不存在时创建，已存在则跳过
    ApplyStrategyCreateOnly ApplyStrategy = "CreateOnly"
)

// YAMLManifestExecutor 定义 YAML 清单执行器接口

> **设计思路 - DryRun 为独立方法而非选项字段**：`DryRun()` 是独立方法而非 `ExecuteOptions.DryRun bool` 字段，原因是返回类型不同——`Execute` 返回 `error`，`DryRun` 返回 `([]*unstructured.Unstructured, error)`（预览将要应用的资源列表）。独立方法让接口语义更清晰，调用方不会混淆"干运行模式"和"执行模式"。BinaryInstaller 和 HelmInstaller 未提供 DryRun 接口，因为它们的操作涉及远程节点，干运行难以模拟（SSH 命令、Helm template 渲染结果不等同于实际安装结果）。
type YAMLManifestExecutor interface {
    // Execute 执行 YAML 清单组件的安装/升级
    Execute(ctx context.Context, spec *YAMLSpec, opts ExecuteOptions) error
    
    // DryRun 执行干运行，返回将要应用的资源列表但不实际执行
    DryRun(ctx context.Context, spec *YAMLSpec, opts ExecuteOptions) ([]*unstructured.Unstructured, error)
}

// ExecuteOptions 定义执行选项
type ExecuteOptions struct {
    // 组件名称
    ComponentName string
    
    // 组件版本
    ComponentVersion string
    
    // 目标命名空间 (覆盖 YAMLSpec.Namespace)
    Namespace string
    
    // 模板上下文 (用于渲染模板变量)
    TemplateContext manifest.TemplateContext
    
    // PhaseContext (用于版本比较与升级判断)
    PhaseContext *phaseframe.PhaseContext
}

// ManifestApplier 扩展接口 (兼容现有 manifest.Applier)
type AdvancedManifestApplier interface {
    // ApplyComponent 兼容现有 manifest.Applier 接口
    ApplyComponent(ctx context.Context, pkg *manifest.ComponentPackage) error
    
    // ApplyWithStrategy 按指定策略应用清单
    ApplyWithStrategy(ctx context.Context, pkg *manifest.ComponentPackage, strategy ApplyStrategy) error
    
    // PruneResources 裁剪不再需要的资源
    PruneResources(ctx context.Context, namespace string, selector labels.Selector, applied []*unstructured.Unstructured) error
}
```

### 6.4 核心方法实现

```go
// pkg/yamlexecutor/executor.go

// yamlManifestExecutor YAML 清单执行器实现
type yamlManifestExecutor struct {
    manifestStore   manifest.Store
    manifestApplier AdvancedManifestApplier
    httpClient      *http.Client
    logger          *bkev1beta1.BKELogger
}

// NewYAMLManifestExecutor 创建 YAML 清单执行器
func NewYAMLManifestExecutor(cfg YAMLManifestExecutorConfig) *yamlManifestExecutor {
    return &yamlManifestExecutor{
        manifestStore:   cfg.ManifestStore,
        manifestApplier: cfg.ManifestApplier,
        httpClient:      cfg.HTTPClient,
        logger:          cfg.Logger,
    }
}

// Execute 执行 YAML 清单组件的安装/升级
func (e *yamlManifestExecutor) Execute(ctx context.Context, spec *YAMLSpec, opts ExecuteOptions) error {
    // 1. 版本检查 - 判断是否需要升级
    if !manifestNeedsUpgrade(opts.PhaseContext, opts.ComponentName) {
        if e.logger != nil {
            e.logger.Info("skip yaml component, version matched",
                "component", opts.ComponentName, "version", opts.ComponentVersion)
        }
        return nil
    }
    
    // 2. 获取清单数据
    manifests, err := e.resolveManifests(ctx, spec, opts)
    if err != nil {
        return fmt.Errorf("resolve manifests for %s: %w", opts.ComponentName, err)
    }
    
    if len(manifests) == 0 {
        return fmt.Errorf("component %q has no manifests to apply", opts.ComponentName)
    }
    
    // 3. 构建应用包
    pkg := &manifest.ComponentPackage{
        Name:      opts.ComponentName,
        Version:   opts.ComponentVersion,
        Manifests: manifests,
    }
    
    // 4. 按策略应用资源
    strategy := e.resolveApplyStrategy(spec)
    if err := e.manifestApplier.ApplyWithStrategy(ctx, pkg, strategy); err != nil {
        return fmt.Errorf("apply manifests for %s with strategy %s: %w",
            opts.ComponentName, strategy, err)
    }
    
    // 5. 可选裁剪
    if spec != nil && spec.Prune {
        if err := e.pruneResources(ctx, spec, pkg); err != nil {
            if e.logger != nil {
                e.logger.Info("prune resources warning",
                    "component", opts.ComponentName, "error", err)
            }
        }
    }
    
    return nil
}

// resolveManifests 解析并下载清单数据
// 优先从 ManifestStore 获取，回退到从 YAMLSpec.Manifests URL 下载

> **设计思路 - 双源获取与优先级**：`resolveManifests` 先尝试 ManifestStore，失败后回退到 YAMLSpec.Manifests URL 下载。双源设计的原因是：
> 1. **兼容现有路径**：当前代码中 YAML 类型组件统一通过 `ManifestStore.GetComponentManifests()` 从 OCI 仓库/bke-manifests 获取清单，这是已有的稳定路径，必须保留为首选。
> 2. **新场景支持**：`YAMLSpec.Manifests` URL 下载是新增能力，允许组件引用外部 HTTP 托管的清单文件，适用于不在 OCI 仓库中的第三方组件。
> 3. **不冲突的保证**：两种来源不会同时生效——当前 DAG 构建时，如果 `ComponentVersion.Spec.Type == "yaml"` 且 `Spec.YAML.Manifests` 非空，会设置 `YAMLRef`，此时 ManifestStore 可能无此组件数据，自然走 URL 下载路径。如果组件通过 ManifestStore 管理，则 `YAMLSpec.Manifests` 为空，直接走 Store 路径。
> 4. **改进方向**：后续可考虑在 `YAMLSpec` 中新增 `Source string` 字段（`"store" | "url"`）显式指定来源，避免隐式回退带来的歧义。
func (e *yamlManifestExecutor) resolveManifests(
    ctx context.Context,
    spec *YAMLSpec,
    opts ExecuteOptions,
) ([][]byte, error) {
    // 路径1: 从 ManifestStore 获取 (兼容现有代码路径)
    if e.manifestStore != nil {
        pkg, err := e.manifestStore.GetComponentManifests(
            ctx, opts.ComponentName, opts.ComponentVersion, opts.TemplateContext)
        if err == nil && pkg != nil && len(pkg.Manifests) > 0 {
            return pkg.Manifests, nil
        }
    }
    
    // 路径2: 从 YAMLSpec.Manifests URL 下载
    if spec != nil && len(spec.Manifests) > 0 {
        var allManifests [][]byte
        for _, ref := range spec.Manifests {
            data, err := e.downloadManifest(ctx, ref)
            if err != nil {
                return nil, fmt.Errorf("download manifest from %s: %w", ref.URL, err)
            }
            allManifests = append(allManifests, data)
        }
        return allManifests, nil
    }
    
    return nil, fmt.Errorf("no manifest source available for component %q", opts.ComponentName)
}

// downloadManifest 下载单个清单文件
func (e *yamlManifestExecutor) downloadManifest(ctx context.Context, ref ManifestRef) ([]byte, error) {
    req, err := http.NewRequestWithContext(ctx, "GET", ref.URL, nil)
    if err != nil {
        return nil, fmt.Errorf("create request for %s: %w", ref.URL, err)
    }
    
    resp, err := e.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("download %s: %w", ref.URL, err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("download %s: unexpected status %d", ref.URL, resp.StatusCode)
    }
    
    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("read response from %s: %w", ref.URL, err)
    }
    
    // 校验和验证
    if ref.Checksum != "" {
        if err := verifyChecksum(data, ref.Checksum); err != nil {
            return nil, fmt.Errorf("checksum verification failed for %s: %w", ref.URL, err)
        }
    }
    
    return data, nil
}

// resolveApplyStrategy 解析应用策略，默认为 ServerSideApply

> **设计思路 - 默认 ServerSideApply**：选择 SSA 作为默认策略的原因是：
> 1. **声明式管理**：SSA 使用 `fieldManager` 机制声明字段所有权，多个管理者可独立管理同一资源的不同字段，这与 ComponentVersion 的声明式设计理念一致——每个组件只管理自己关心的资源字段。
> 2. **升级安全**：SSA 不会删除其他管理者设置的字段（如用户手动添加的 annotation），比 Replace（删除+重建）和 Update（整体覆盖）更安全。
> 3. **兼容性**：SSA 从 Kubernetes 1.16 开始 beta、1.22 开始 GA，当前最低支持版本 v1.25.6 已完全支持。
> 4. **降级方案**：如果目标集群不支持 SSA（极低版本），用户可显式设置 `applyStrategy: Replace` 作为降级策略。
func (e *yamlManifestExecutor) resolveApplyStrategy(spec *YAMLSpec) ApplyStrategy {
    if spec == nil || spec.ApplyStrategy == "" {
        return ApplyStrategyServerSideApply
    }
    return ApplyStrategy(spec.ApplyStrategy)
}

// pruneResources 裁剪不再需要的资源
func (e *yamlManifestExecutor) pruneResources(
    ctx context.Context,
    spec *YAMLSpec,
    pkg *manifest.ComponentPackage,
) error {
    if len(spec.PruneLabelSelector) == 0 {
        return nil
    }
    
    selector := labels.Set(spec.PruneLabelSelector).AsSelector()
    namespace := spec.Namespace
    
    // 解析已应用的资源列表
    var applied []*unstructured.Unstructured
    for _, doc := range pkg.Manifests {
        objects, err := parseYAMLDocuments(doc)
        if err != nil {
            continue
        }
        applied = append(applied, objects...)
    }
    
    return e.manifestApplier.PruneResources(ctx, namespace, selector, applied)
}

// manifestNeedsUpgrade 检查组件是否需要升级
func manifestNeedsUpgrade(phaseCtx *phaseframe.PhaseContext, componentName string) bool {
    if phaseCtx == nil || phaseCtx.VersionContext == nil {
        return true
    }
    vc := phaseCtx.VersionContext
    if !vc.HasTarget(componentName) {
        return true
    }
    return vc.NeedsUpgrade(componentName)
}
```

```go
// pkg/yamlexecutor/parser.go

// parseYAMLDocuments 解析多文档 YAML 为 Unstructured 对象列表
func parseYAMLDocuments(data []byte) ([]*unstructured.Unstructured, error) {
    var result []*unstructured.Unstructured
    
    decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
    for {
        obj := &unstructured.Unstructured{}
        if err := decoder.Decode(obj); err != nil {
            if errors.Is(err, io.EOF) {
                break
            }
            continue
        }
        if obj.GetKind() == "" {
            continue
        }
        result = append(result, obj)
    }
    
    return result, nil
}

// verifyChecksum 验证数据校验和
func verifyChecksum(data []byte, expectedChecksum string) error {
    parts := strings.SplitN(expectedChecksum, ":", 2)
    if len(parts) != 2 {
        return fmt.Errorf("invalid checksum format: %s", expectedChecksum)
    }
    
    algorithm, expected := parts[0], parts[1]
    switch algorithm {
    case "sha256":
        hash := sha256.Sum256(data)
        actual := hex.EncodeToString(hash[:])
        if actual != expected {
            return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
        }
    default:
        return fmt.Errorf("unsupported checksum algorithm: %s", algorithm)
    }
    
    return nil
}
```

```go
// pkg/yamlexecutor/applier.go

// advancedClusterApplier 扩展的集群应用器
type advancedClusterApplier struct {
    *manifest.ClusterApplier
}

// ApplyWithStrategy 按指定策略应用清单
func (a *advancedClusterApplier) ApplyWithStrategy(
    ctx context.Context,
    pkg *manifest.ComponentPackage,
    strategy ApplyStrategy,
) error {
    switch strategy {
    case ApplyStrategyServerSideApply:
        return a.applyServerSide(ctx, pkg)
    case ApplyStrategyReplace:
        return a.applyReplace(ctx, pkg)
    case ApplyStrategyCreateOnly:
        return a.applyCreateOnly(ctx, pkg)
    default:
        return a.ApplyComponent(ctx, pkg)
    }
}

// applyServerSide 使用 Server-Side Apply 方式应用资源
func (a *advancedClusterApplier) applyServerSide(ctx context.Context, pkg *manifest.ComponentPackage) error {
    return a.ApplyComponent(ctx, pkg)
}

// applyReplace 使用替换方式更新资源
func (a *advancedClusterApplier) applyReplace(ctx context.Context, pkg *manifest.ComponentPackage) error {
    objects, err := a.parseAllManifests(pkg)
    if err != nil {
        return err
    }
    
    for _, obj := range objects {
        if err := a.replaceResource(ctx, obj); err != nil {
            return fmt.Errorf("replace %s/%s: %w", obj.GetKind(), obj.GetName(), err)
        }
    }
    return nil
}

// applyCreateOnly 仅在资源不存在时创建
func (a *advancedClusterApplier) applyCreateOnly(ctx context.Context, pkg *manifest.ComponentPackage) error {
    objects, err := a.parseAllManifests(pkg)
    if err != nil {
        return err
    }
    
    for _, obj := range objects {
        if err := a.createIfNotExists(ctx, obj); err != nil {
            return fmt.Errorf("create %s/%s: %w", obj.GetKind(), obj.GetName(), err)
        }
    }
    return nil
}

// PruneResources 裁剪不再需要的资源
func (a *advancedClusterApplier) PruneResources(
    ctx context.Context,
    namespace string,
    selector labels.Selector,
    applied []*unstructured.Unstructured,
) error {
    // 构建 applied 集合的 key (namespace/kind/name)
    appliedKeys := make(map[string]struct{})
    for _, obj := range applied {
        key := fmt.Sprintf("%s/%s/%s", obj.GetNamespace(), obj.GetKind(), obj.GetName())
        appliedKeys[key] = struct{}{}
    }
    
    // 按选择器查询集群中匹配标签的资源
    // 删除不在 applied 集合中的资源
    // (具体实现依赖 kubeClient 动态客户端)
    return nil
}
```

---

## 7. TemplateRenderer 详细设计

### 7.1 模板变量系统

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         TemplateRenderer 变量系统                                │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  1. 集群信息变量 (Cluster Variables)                                             │
├─────────────────────────────────────────────────────────────────────────────────┤
│  {{clusterName}}          集群名称                                               │
│  {{clusterNamespace}}     集群命名空间                                           │
│  {{apiServer}}            API Server 地址                                        │
│  {{apiServerPort}}        API Server 端口                                        │
│  {{serviceCIDR}}          Service CIDR                                           │
│  {{podCIDR}}              Pod CIDR                                               │
│  {{dnsDomain}}            DNS 域名                                               │
│  {{clusterDNS}}           Cluster DNS IP                                         │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  2. 节点信息变量 (Node Variables)                                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│  {{nodeIP}}               节点 IP                                                │
│  {{nodeHostname}}         节点主机名                                             │
│  {{nodeRole}}             节点角色 (master/worker/etcd)                          │
│  {{nodeArch}}             节点架构 (amd64/arm64)                                 │
│  {{nodeOS}}               操作系统 (centos/ubuntu/kylin)                         │
│  {{nodeOSVersion}}        操作系统版本 (7/8/20.04/V10)                           │
│  {{nodeKernelVersion}}    内核版本                                               │
│  {{nodeCPUs}}             CPU 核心数                                             │
│  {{nodeMemoryMB}}         内存大小 (MB)                                          │
│  {{nodeDiskGB}}           磁盘大小 (GB)                                          │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  3. 版本信息变量 (Version Variables)                                             │
├─────────────────────────────────────────────────────────────────────────────────┤
│  {{componentVersion}}     当前组件版本                                           │
│  {{componentPreviousVersion}}  上一组件版本 (升级时)                             │
│  {{clusterVersion}}       集群 Kubernetes 版本                                   │
│  {{etcdVersion}}          Etcd 版本                                              │
│  {{containerdVersion}}    Containerd 版本                                        │
│  {{bkeagentVersion}}      BKEAgent 版本                                          │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  4. 二进制制品变量 (Artifact Variables)                                          │
├─────────────────────────────────────────────────────────────────────────────────┤
│  {{artifact.<name>.path}}     制品本地路径                                       │
│  {{artifact.<name>.url}}      制品原始 URL                                       │
│  {{artifact.<name>.checksum}} 制品校验和                                         │
│  {{artifact.<name>.filename}} 制品文件名                                         │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  5. 镜像仓库变量 (Registry Variables)                                            │
├─────────────────────────────────────────────────────────────────────────────────┤
│  {{imageRegistry}}          镜像仓库地址                                         │
│  {{imagePullSecret}}        镜像拉取 Secret                                      │
│  {{imageNamespace}}         镜像命名空间                                         │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  6. 安装路径变量 (Path Variables)                                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│  {{installPath}}            默认安装路径                                         │
│  {{configPath}}             配置路径                                             │
│  {{logPath}}                日志路径                                             │
│  {{dataPath}}               数据路径                                             │
│  {{binPath}}                二进制路径                                           │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  7. 操作类型变量 (Action Variables)                                              │
├─────────────────────────────────────────────────────────────────────────────────┤
│  {{action}}                 操作类型 (install/upgrade/uninstall)                 │
│  {{isUpgrade}}              是否升级 (true/false)                                │
│  {{isInstall}}              是否安装 (true/false)                                │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  8. 自定义变量 (Custom Variables)                                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│  {{.Variables.<key>}}       通过 ComponentVersion 定义的自定义变量               │
│  例: {{.Variables.logLevel}}                                                   │
│  例: {{.Variables.snapshotter}}                                                │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.2 TemplateRenderer 渲染流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        TemplateRenderer 渲染流程                                 │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  RenderScript()  │
                              │  入口函数        │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  1. 构建模板数据                    │
                    │  BuildScriptData(ctx, opts)          │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  集群信息       │  │  节点信息       │  │  版本信息       │
          │  ClusterName    │  │  NodeIP         │  │  ComponentVer   │
          │  APIServer      │  │  NodeArch       │  │  ClusterVer     │
          │  ServiceCIDR    │  │  NodeOS         │  │                 │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  2. 构建制品数据                    │
                    │  buildArtifactData(ctx, binary, opts)│
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 构建自定义变量                  │
                    │  Variables: binary.Variables         │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  4. 创建模板解析器                  │
                    │  template.New("installScript")       │
                    │         .Funcs(funcMap)              │
                    │         .Parse(script)               │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         解析成功                解析失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  继续           │   │  返回错误       │
                    └────────┬────────┘   └─────────────────┘
                             │
                             ▼
                    ┌──────────────────────────────────────┐
                    │  5. 执行模板渲染                    │
                    │  tmpl.Execute(&buf, data)            │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         渲染成功                渲染失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  返回渲染结果   │   │  返回错误       │
                    │  return buf.    │   │  return err     │
                    │  String()       │   │                 │
                    └─────────────────┘   └─────────────────┘
```

### 7.3 自定义函数定义

```go
// pkg/binaryinstaller/template_renderer.go

// TemplateRenderer 模板渲染器
type TemplateRenderer struct {
    funcMap template.FuncMap
}

// NewTemplateRenderer 创建模板渲染器
func NewTemplateRenderer() *TemplateRenderer {
    return &TemplateRenderer{
        funcMap: template.FuncMap{
            // 字符串函数
            "upper": strings.ToUpper,
            "lower": strings.ToLower,
            "trim":  strings.TrimSpace,
            "replace": strings.ReplaceAll,
            
            // 条件函数
            "eq": func(a, b interface{}) bool { 
                return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) 
            },
            "ne": func(a, b interface{}) bool { 
                return fmt.Sprintf("%v", a) != fmt.Sprintf("%v", b) 
            },
            "gt": func(a, b interface{}) bool {
                // 版本比较
                return semver.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b)) > 0
            },
            "ge": func(a, b interface{}) bool {
                return semver.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b)) >= 0
            },
            "lt": func(a, b interface{}) bool {
                return semver.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b)) < 0
            },
            "le": func(a, b interface{}) bool {
                return semver.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b)) <= 0
            },
            
            // 默认值函数
            "default": func(def, val interface{}) interface{} {
                if val == nil || val == "" {
                    return def
                }
                return val
            },
            
            // 路径函数
            "joinPath": filepath.Join,
            "base":     filepath.Base,
            "dir":      filepath.Dir,
            
            // 时间函数
            "now": time.Now,
            "date": func(format string) string {
                return time.Now().Format(format)
            },
            
            // 版本函数
            "semver": func(v string) string {
                // 标准化版本号
                parsed, err := semver.Parse(v)
                if err != nil {
                    return v
                }
                return parsed.String()
            },
        },
    }
}

// RenderScript 渲染安装脚本
func (r *TemplateRenderer) RenderScript(script string, data ScriptData) (string, error) {
    tmpl, err := template.New("installScript").Funcs(r.funcMap).Parse(script)
    if err != nil {
        return "", fmt.Errorf("failed to parse script template: %w", err)
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", fmt.Errorf("failed to render script template: %w", err)
    }
    
    return buf.String(), nil
}
```

---

## 8. ConfigRenderer 详细设计

### 8.1 三种渲染模式

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         ConfigRenderer 渲染模式                                  │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  模式 1: Content 模式 (Go template 渲染)                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  输入:                                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ configTemplates:                                                        │    │
│  │   - name: bkeagent.conf                                                 │    │
│  │     path: "/etc/openFuyao/bkeagent/bkeagent.conf"                       │    │
│  │     content: |                                                          │    │
│  │       cluster_name: {{.clusterName}}                                    │    │
│  │       api_server: {{.apiServer}}                                        │    │
│  │       log_level: {{.Variables.logLevel | default "info"}}               │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  渲染过程:                                                                      │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ 1. 解析 content 模板                                                   │    │
│  │ 2. 注入模板数据 (集群信息、节点信息、自定义变量等)                       │    │
│  │ 3. 执行 Go template 渲染                                               │    │
│  │ 4. 返回渲染后的内容                                                    │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  输出:                                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ cluster_name: my-cluster                                                │    │
│  │ api_server: https://10.0.0.1:6443                                       │    │
│  │ log_level: info                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  模式 2: SecretRef 模式 (从 Secret 获取)                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  输入:                                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ configTemplates:                                                        │    │
│  │   - name: tls.crt                                                       │    │
│  │     path: "/etc/openFuyao/bkeagent/tls.crt"                             │    │
│  │     secretRef:                                                          │    │
│  │       name: bkeagent-tls                                                │    │
│  │       namespace: "{{.clusterNamespace}}"                                │    │
│  │       key: tls.crt                                                      │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  渲染过程:                                                                      │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ 1. 解析 namespace 模板变量                                             │    │
│  │ 2. 从 Kubernetes API 获取 Secret                                       │    │
│  │ 3. 提取指定 key 的内容                                                 │    │
│  │ 4. 返回 Secret 数据                                                    │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  输出:                                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ -----BEGIN CERTIFICATE-----                                             │    │
│  │ MIIC...                                                                 │    │
│  │ -----END CERTIFICATE-----                                               │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│  模式 3: KubeconfigTemplate 模式 (动态生成)                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  输入:                                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ configTemplates:                                                        │    │
│  │   - name: kubeconfig                                                    │    │
│  │     path: "/etc/openFuyao/bkeagent/kubeconfig"                          │    │
│  │     kubeconfigTemplate:                                                 │    │
│  │       clusterName: "{{.clusterName}}"                                   │    │
│  │       apiServer: "{{.apiServer}}"                                       │    │
│  │       caCertPath: "/etc/openFuyao/bkeagent/ca.crt"                      │    │
│  │       clientCertPath: "/etc/openFuyao/bkeagent/tls.crt"                 │    │
│  │       clientKeyPath: "/etc/openFuyao/bkeagent/tls.key"                  │    │
│  │       namespace: "{{.clusterNamespace}}"                                │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  渲染过程:                                                                      │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ 1. 解析模板变量                                                        │    │
│  │ 2. 构建 kubeconfig 结构                                                │    │
│  │ 3. 序列化为 YAML 格式                                                  │    │
│  │ 4. 返回 kubeconfig 内容                                                │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  输出:                                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ apiVersion: v1                                                          │    │
│  │ kind: Config                                                            │    │
│  │ clusters:                                                               │    │
│  │ - cluster:                                                              │    │
│  │     server: https://10.0.0.1:6443                                       │    │
│  │     certificate-authority: /etc/openFuyao/bkeagent/ca.crt               │    │
│  │   name: my-cluster                                                      │    │
│  │ users:                                                                  │    │
│  │ - user:                                                                 │    │
│  │     client-certificate: /etc/openFuyao/bkeagent/tls.crt                 │    │
│  │     client-key: /etc/openFuyao/bkeagent/tls.key                         │    │
│  │   name: my-cluster                                                      │    │
│  │ contexts:                                                               │    │
│  │ - context:                                                              │    │
│  │     cluster: my-cluster                                                 │    │
│  │     user: my-cluster                                                    │    │
│  │     namespace: default                                                  │    │
│  │   name: my-cluster                                                      │    │
│  │ current-context: my-cluster                                             │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 8.2 ConfigRenderer 渲染流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         ConfigRenderer 渲染流程                                  │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  RenderConfig()  │
                              │  入口函数        │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  判断渲染模式                        │
                    │  switch {                            │
                    │  case template.Content != "":        │
                    │  case template.SecretRef != nil:     │
                    │  case template.KubeconfigTemplate:   │
                    │  }                                   │
                    └────────────────────┬─────────────────┘
                                         │
              ┌──────────────────────────┼──────────────────────────┐
              │                          │                          │
              ▼                          ▼                          ▼
    ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
    │  Content 模式   │      │  SecretRef 模式 │      │  Kubeconfig     │
    │                 │      │                 │      │  模式           │
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             ▼                        ▼                        ▼
    ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
    │ 1. 构建模板数据 │      │ 1. 解析命名空间 │      │ 1. 解析模板变量 │
    │  buildTemplate  │      │  renderString   │      │  renderString   │
    │  Data()         │      │  (namespace)    │      │                 │
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             ▼                        ▼                        ▼
    ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
    │ 2. 解析模板     │      │ 2. 获取 Secret  │      │ 2. 构建结构     │
    │  template.Parse │      │  client.Get()   │      │  clientcmdapi   │
    │  (content)      │      │                 │      │  .Config{}      │
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             ▼                        ▼                        ▼
    ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
    │ 3. 执行渲染     │      │ 3. 提取数据     │      │ 3. 序列化 YAML  │
    │  tmpl.Execute() │      │  secret.Data[key]│     │  clientcmd.Write│
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             └────────────────────────┼────────────────────────┘
                                      │
                                      ▼
                    ┌──────────────────────────────────────┐
                    │  返回渲染结果                        │
                    │  return content, nil                 │
                    └──────────────────────────────────────┘
```

### 8.3 核心接口定义

```go
// pkg/binaryinstaller/config_renderer.go

// ConfigRenderer 配置文件渲染器
type ConfigRenderer struct {
    client      client.Client
    funcMap     template.FuncMap
}

// RenderConfig 渲染配置文件模板
func (r *ConfigRenderer) RenderConfig(ctx context.Context, template ConfigTemplateSpec, opts InstallOptions) ([]byte, error) {
    switch {
    case template.Content != "":
        return r.renderContentTemplate(ctx, template, opts)
    case template.SecretRef != nil:
        return r.renderSecretTemplate(ctx, template, opts)
    case template.KubeconfigTemplate != nil:
        return r.renderKubeconfigTemplate(ctx, template, opts)
    }
    
    return nil, errors.New("no template content specified")
}

// renderContentTemplate 渲染内容模板
func (r *ConfigRenderer) renderContentTemplate(ctx context.Context, template ConfigTemplateSpec, opts InstallOptions) ([]byte, error) {
    data := r.buildTemplateData(ctx, opts)
    
    tmpl, err := template.New(template.Name).Funcs(r.funcMap).Parse(template.Content)
    if err != nil {
        return nil, fmt.Errorf("failed to parse template: %w", err)
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return nil, fmt.Errorf("failed to render template: %w", err)
    }
    
    return buf.Bytes(), nil
}

// renderSecretTemplate 从 Secret 获取内容
func (r *ConfigRenderer) renderSecretTemplate(ctx context.Context, template ConfigTemplateSpec, opts InstallOptions) ([]byte, error) {
    secretRef := template.SecretRef
    
    // 渲染 namespace 模板变量
    namespace, err := r.renderString(secretRef.Namespace, opts)
    if err != nil {
        return nil, fmt.Errorf("failed to render namespace: %w", err)
    }
    
    // 获取 Secret
    secret := &corev1.Secret{}
    if err := r.client.Get(ctx, types.NamespacedName{
        Name:      secretRef.Name,
        Namespace: namespace,
    }, secret); err != nil {
        return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretRef.Name, err)
    }
    
    // 获取指定 key 的内容
    data, ok := secret.Data[secretRef.Key]
    if !ok {
        return nil, fmt.Errorf("key %s not found in secret %s/%s", secretRef.Key, namespace, secretRef.Name)
    }
    
    return data, nil
}

// renderKubeconfigTemplate 动态生成 kubeconfig
func (r *ConfigRenderer) renderKubeconfigTemplate(ctx context.Context, template ConfigTemplateSpec, opts InstallOptions) ([]byte, error) {
    kc := template.KubeconfigTemplate
    
    // 解析模板变量
    clusterName, _ := r.renderString(kc.ClusterName, opts)
    apiServer, _ := r.renderString(kc.APIServer, opts)
    namespace, _ := r.renderString(kc.Namespace, opts)
    
    kubeconfig := clientcmdapi.Config{
        Kind:       "Config",
        APIVersion: "v1",
        Clusters: map[string]*clientcmdapi.Cluster{
            clusterName: {
                Server:               apiServer,
                CertificateAuthority: kc.CACertPath,
            },
        },
        AuthInfos: map[string]*clientcmdapi.AuthInfo{
            clusterName: {
                ClientCertificate: kc.ClientCertPath,
                ClientKey:         kc.ClientKeyPath,
            },
        },
        Contexts: map[string]*clientcmdapi.Context{
            clusterName: {
                Cluster:   clusterName,
                AuthInfo:  clusterName,
                Namespace: namespace,
            },
        },
        CurrentContext: clusterName,
    }
    
    return clientcmd.Write(kubeconfig)
}

// buildTemplateData 构建模板数据
func (r *ConfigRenderer) buildTemplateData(ctx context.Context, opts InstallOptions) map[string]interface{} {
    cluster := opts.Cluster
    node := opts.Node
    
    return map[string]interface{}{
        // 集群信息
        "clusterName":      cluster.Name,
        "clusterNamespace": cluster.Namespace,
        "apiServer":        cluster.Spec.ClusterConfig.Cluster.APIServer,
        
        // 节点信息
        "nodeIP":       node.IP,
        "nodeHostname": node.Hostname,
        "nodeRole":     node.Role,
        "nodeArch":     node.Spec.Architecture,
        "nodeOS":       node.Spec.OperatingSystem,
        
        // 版本信息
        "componentVersion": opts.Component.Spec.Version,
        "clusterVersion":   cluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
        
        // 自定义变量
        "Variables": opts.Component.Spec.Binary.Variables,
    }
}
```

---

## 9. DAG 集成详细设计

### 9.1 执行器注册

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           DAG 执行器注册流程                                     │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  DAG Scheduler   │
                              │  接收组件节点    │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  获取 ComponentVersion               │
                    │  manifestStore.GetComponentVersion() │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  判断组件类型                        │
                    │  switch cv.Spec.Type                 │
                    └────────────────────┬─────────────────┘
                                         │
          ┌──────────────┬───────────────┼───────────────┬──────────────┐
          │              │               │               │              │
          ▼              ▼               ▼               ▼              │
┌───────────────┐ ┌─────────────┐ ┌─────────────┐ ┌──────────────┐    │
│ ComponentType │ │ComponentType│ │ComponentType│ │ComponentType │    │
│   Binary      │ │    Helm     │ │    YAML     │ │   Inline     │    │
│               │ │             │ │             │ │              │    │
└───────┬───────┘ └──────┬──────┘ └──────┬──────┘ └──────┬───────┘    │
        │                │               │               │             │
        ▼                ▼               ▼               ▼             │
┌───────────────┐ ┌─────────────┐ ┌─────────────┐ ┌──────────────┐    │
│   创建        │ │   创建      │ │   创建      │ │   创建       │    │
│ Binary        │ │ Helm        │ │ YAML        │ │ Inline       │    │
│ Component     │ │ Component   │ │ Manifest    │ │ Component    │    │
│ Executor      │ │ Executor    │ │ Executor    │ │ Executor     │    │
└───────┬───────┘ └──────┬──────┘ └──────┬──────┘ └──────┬───────┘    │
        │                │               │               │             │
        └────────────────┴───────────────┼───────────────┘             │
                                        │                              │
                                        ▼                              │
                    ┌──────────────────────────────────────┐           │
                    │  注册到 DAG                          │           │
                    │  dag.AddNode(name, executor,         │           │
                    │              dependencies, policy)   │           │
                    └──────────────────────────────────────┘           │
```

### 9.2 DAG 调度流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           DAG 调度执行流程                                       │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  ExecuteDAG()    │
                              │  入口函数        │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  1. 拓扑排序                         │
                    │  batches := dag.TopologicalBatches() │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  2. 遍历执行批次 (串行)              │
                    │  for _, batch := range batches       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 执行批次内组件 (并行)            │
                    │  executeBatchParallel(ctx, batch)    │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  组件 A         │  │  组件 B         │  │  组件 C         │
          │  (并行执行)     │  │  (并行执行)     │  │  (并行执行)     │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  4. 检查批次结果                     │
                    │  if err != nil                       │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         执行成功                执行失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  继续下一批次   │   │  检查失败策略   │
                    └─────────────────┘   └────────┬────────┘
                                                   │
                                        ┌──────────┼──────────┐
                                        │          │          │
                                        ▼          ▼          ▼
                              ┌─────────────┐ ┌─────────┐ ┌─────────┐
                              │  FailFast   │ │Continue │ │Rollback │
                              │  立即终止   │ │继续执行 │ │回滚后   │
                              │  返回错误   │ │下一批次 │ │继续     │
                              └─────────────┘ └─────────┘ └─────────┘
```

### 9.3 核心接口定义

```go
// pkg/dagexec/executor.go

// ComponentExecutor 组件执行器接口
type ComponentExecutor interface {
    // ExecuteComponent 执行组件
    ExecuteComponent(ctx context.Context, node *ComponentNode, phaseCtx *phaseframe.PhaseContext) error
    
    // GetComponentType 获取组件类型
    GetComponentType() ComponentType
}

// BinaryComponentExecutor 二进制组件执行器
type BinaryComponentExecutor struct {
    installer *binaryinstaller.BinaryInstaller
    store     *manifest.Store
}

// ExecuteComponent 执行二进制组件
func (e *BinaryComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, phaseCtx *phaseframe.PhaseContext) error {
    component := node.Component
    
    // 1. 获取 ComponentVersion
    cv, err := e.store.GetComponentVersion(component.Name, component.Version)
    if err != nil {
        return fmt.Errorf("failed to get component version: %w", err)
    }
    
    // 2. 确认是二进制类型
    if cv.Spec.Type != configv1alpha1.ComponentTypeBinary {
        return fmt.Errorf("component %s is not a binary component", component.Name)
    }
    
    // 3. 获取需要操作的节点
    nodes := e.getTargetNodes(phaseCtx, component)
    
    // 4. 根据升级策略执行
    strategy := cv.Spec.UpgradeStrategy
    switch strategy.Mode {
    case "Rolling":
        return e.executeRolling(ctx, nodes, cv, strategy)
    case "Parallel":
        return e.executeParallel(ctx, nodes, cv, strategy)
    case "Batch":
        return e.executeBatch(ctx, nodes, cv, strategy)
    }
    
    return nil
}

// executeRolling 滚动执行 (逐节点)
func (e *BinaryComponentExecutor) executeRolling(ctx context.Context, nodes []Node, cv *ComponentVersion, strategy UpgradeStrategySpec) error {
    for _, node := range nodes {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }
        
        opts := binaryinstaller.InstallOptions{
            Component:  cv,
            Node:       node,
            Action:     binaryinstaller.BinaryActionUpgrade,
            Timeout:    strategy.Timeout,
            RetryCount: 3,
        }
        
        if err := e.installer.Install(ctx, opts); err != nil {
            switch strategy.FailurePolicy {
            case "FailFast":
                return err
            case "Continue":
                log.Warn("node %s upgrade failed, continuing: %v", node.IP, err)
                continue
            case "Rollback":
                if rbErr := e.rollback(node, cv); rbErr != nil {
                    return fmt.Errorf("upgrade failed and rollback failed: %w; rollback: %v", err, rbErr)
                }
                continue
            }
        }
    }
    
    return nil
}

// HelmComponentExecutor Helm 组件执行器
type HelmComponentExecutor struct {
    installer *helminstaller.HelmInstaller
    store     *manifest.Store
}

// ExecuteComponent 执行 Helm 组件
func (e *HelmComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, phaseCtx *phaseframe.PhaseContext) error {
    component := node.Component
    
    // 1. 获取 ComponentVersion
    cv, err := e.store.GetComponentVersion(component.Name, component.Version)
    if err != nil {
        return fmt.Errorf("failed to get component version: %w", err)
    }
    
    // 2. 确认是 Helm 类型
    if cv.Spec.Type != configv1alpha1.ComponentTypeHelm {
        return fmt.Errorf("component %s is not a helm component", component.Name)
    }
    
    // 3. 确定操作类型
    action := helminstaller.HelmActionInstall
    if phaseCtx.IsUpgrade {
        action = helminstaller.HelmActionUpgrade
    }
    
    // 4. 执行 Helm 操作
    opts := helminstaller.InstallOptions{
        Component: cv,
        Cluster:   phaseCtx.BKECluster,
        Action:    action,
        Timeout:   cv.Spec.UpgradeStrategy.Timeout,
    }
    
    return e.installer.Install(ctx, opts)
}
```

---

### 9.4 ComponentNode 扩展


当前 `topology.ComponentNode` 仅有 `Inline` 字段，需扩展以支持 Binary/Helm/YAML 类型：

> **设计思路 - 四指针类型判别**：使用 `Inline *InlineRef`、`Binary *BinaryRef`、`Helm *HelmRef`、`YAML *YAMLRef` 四个互斥指针字段判别组件类型，而非 `Type ComponentType + Ref interface{}` 单字段方案。原因是：
> 1. **类型安全**：每个 Ref 类型携带不同的专属字段（如 `HelmRef.Namespace/ReleaseName`、`YAMLRef.Namespace`），用 interface{} 需要运行时类型断言，四指针方案在编译期即可保证类型安全。
> 2. **序列化友好**：`ComponentNode` 在 DAG 构建时创建、调度时消费，不经过 CRD 序列化，但互斥指针模式与 CRD 中的 `BinarySpec`/`HelmSpec`/`YAMLSpec` 设计保持一致。
> 3. **默认 "yaml" 的考量**：`ComponentType()` 在所有 Ref 均为 nil 时返回 `"yaml"`，因为当前代码中 `executeComponent` 的默认路径就是 manifest 应用（即 YAML 执行路径），这保证了向后兼容——旧 DAG 中无 Ref 的组件节点仍走原有路径。但应在 DAG 构建时（`BuildInstallDAG`）确保每个节点都设置了正确的 Ref，避免依赖此兜底行为。

```go
// pkg/topology/component.go 扩展

// BinaryRef 指向一个二进制组件
type BinaryRef struct {
    Name    string
    Version string
}

// HelmRef 指向一个 Helm 组件
type HelmRef struct {
    Name        string
    Version     string
    Namespace   string
    ReleaseName string
}

// YAMLRef 指向一个 YAML 清单组件
type YAMLRef struct {
    Name      string
    Version   string
    Namespace string
}

// ComponentNode 扩展后定义 (兼容现有 Inline 字段)
type ComponentNode struct {
    Name          string
    Version       string
    Inline        *InlineRef   // type=inline 时非 nil
    Binary        *BinaryRef   // type=binary 时非 nil
    Helm          *HelmRef     // type=helm 时非 nil
    YAML          *YAMLRef     // type=yaml 时非 nil
    FailurePolicy FailurePolicy
    Dependencies  []string
}

// ComponentType 返回组件类型
func (n *ComponentNode) ComponentType() string {
    switch {
    case n.Inline != nil:
        return "inline"
    case n.Binary != nil:
        return "binary"
    case n.Helm != nil:
        return "helm"
    case n.YAML != nil:
        return "yaml"
    default:
        return "yaml"
    }
}
```


### 9.5 Scheduler 扩展与执行策略




当前 `topology.ComponentNode` 仅有 `Inline` 字段，需扩展以支持 Binary/Helm/YAML 类型：

```go
// pkg/topology/component.go 扩展

// BinaryRef 指向一个二进制组件
type BinaryRef struct {
    Name    string
    Version string
}

// HelmRef 指向一个 Helm 组件
type HelmRef struct {
    Name        string
    Version     string
    Namespace   string
    ReleaseName string
}

// YAMLRef 指向一个 YAML 清单组件
type YAMLRef struct {
    Name      string
    Version   string
    Namespace string
}

// ComponentNode 扩展后定义 (兼容现有 Inline 字段)
type ComponentNode struct {
    Name          string
    Version       string
    Inline        *InlineRef   // type=inline 时非 nil
    Binary        *BinaryRef   // type=binary 时非 nil
    Helm          *HelmRef     // type=helm 时非 nil
    YAML          *YAMLRef     // type=yaml 时非 nil
    FailurePolicy FailurePolicy
    Dependencies  []string
}

// ComponentType 返回组件类型
func (n *ComponentNode) ComponentType() string {
    switch {
    case n.Inline != nil:
        return "inline"
    case n.Binary != nil:
        return "binary"
    case n.Helm != nil:
        return "helm"
    case n.YAML != nil:
        return "yaml"
    default:
        return "yaml"
    }
}
```


当前 `Scheduler.executeComponent()` 仅有 inline 和 manifest 两条路径，需扩展：

```go
// pkg/dagexec/scheduler.go 扩展

// BinaryInstaller 执行二进制组件安装 (接口定义)
type BinaryInstaller interface {
    Install(ctx context.Context, opts BinaryInstallOptions) error
}

// HelmInstaller 执行 Helm 组件安装 (接口定义)
type HelmInstaller interface {
    Install(ctx context.Context, opts HelmInstallOptions) error
}

// YAMLManifestExecutor 执行 YAML 清单组件 (接口定义)
type YAMLManifestExecutor interface {
    Execute(ctx context.Context, spec *YAMLSpec, opts ExecuteOptions) error
}

// Scheduler 扩展字段
type Scheduler struct {
    InlineRunner          InlinePhaseRunner
    ManifestStore         manifest.Store
    ManifestApplier       manifest.Applier
    BinaryInstaller       BinaryInstaller         // 新增
    HelmInstaller         HelmInstaller           // 新增
    YAMLExecutor          YAMLManifestExecutor    // 新增
    MaxParallelPerBatch   int
}

// Config 扩展字段
type Config struct {
    InlineRunner          InlinePhaseRunner
    ManifestStore         manifest.Store
    ManifestApplier       manifest.Applier
    BinaryInstaller       BinaryInstaller         // 新增
    HelmInstaller         HelmInstaller           // 新增
    YAMLExecutor          YAMLManifestExecutor    // 新增
    MaxParallelPerBatch   int
}

// executeComponent 扩展实现
func (s *Scheduler) executeComponent(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    node *topology.ComponentNode,
    tmpl manifest.TemplateContext,
) error {
    switch node.ComponentType() {
    case "inline":
        return s.executeInline(phaseCtx, oldCluster, newCluster, node)
    case "binary":
        return s.executeBinary(ctx, phaseCtx, node)
    case "helm":
        return s.executeHelm(ctx, phaseCtx, node)
    case "yaml":
        return s.executeYAML(ctx, phaseCtx, node, tmpl)
    default:
        return s.executeYAML(ctx, phaseCtx, node, tmpl)
    }
}

// executeYAML 执行 YAML 清单组件
func (s *Scheduler) executeYAML(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    node *topology.ComponentNode,
    tmpl manifest.TemplateContext,
) error {
    if node.YAML == nil {
        return s.executeManifest(ctx, phaseCtx, node, tmpl)
    }

    cv, err := s.getComponentVersion(node.Name, node.Version)
    if err != nil {
        return fmt.Errorf("failed to get component version for %s: %w", node.Name, err)
    }

    spec := cv.Spec.YAML
    opts := ExecuteOptions{
        ComponentName:    node.Name,
        ComponentVersion: node.Version,
        Namespace:        node.YAML.Namespace,
        TemplateContext:  tmpl,
        PhaseContext:     phaseCtx,
    }

    if s.YAMLExecutor != nil {
        return s.YAMLExecutor.Execute(ctx, spec, opts)
    }

    return s.executeManifest(ctx, phaseCtx, node, tmpl)
}

// executeBinary 执行二进制组件
func (s *Scheduler) executeBinary(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    node *topology.ComponentNode,
) error {
    if s.BinaryInstaller == nil {
        return fmt.Errorf("binary installer is not configured")
    }
    if node.Binary == nil {
        return fmt.Errorf("component %q has no binary ref", node.Name)
    }

    action := BinaryActionInstall
    if phaseCtx.IsUpgrade {
        action = BinaryActionUpgrade
    }

    nodes, err := s.getTargetNodes(phaseCtx, node)
    if err != nil {
        return fmt.Errorf("failed to get target nodes for %s: %w", node.Name, err)
    }

    cv, err := s.getComponentVersion(node.Name, node.Version)
    if err != nil {
        return fmt.Errorf("failed to get component version for %s: %w", node.Name, err)
    }

    strategy := cv.Spec.UpgradeStrategy
    switch strategy.Mode {
    case "Rolling":
        return s.executeBinaryRolling(ctx, phaseCtx, nodes, cv, strategy, action)
    case "Parallel":
        return s.executeBinaryParallel(ctx, phaseCtx, nodes, cv, strategy, action)
    case "Batch":
        return s.executeBinaryBatch(ctx, phaseCtx, nodes, cv, strategy, action)
    default:
        return s.executeBinaryRolling(ctx, phaseCtx, nodes, cv, strategy, action)
    }
}

// executeHelm 执行 Helm 组件
func (s *Scheduler) executeHelm(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    node *topology.ComponentNode,
) error {
    if s.HelmInstaller == nil {
        return fmt.Errorf("helm installer is not configured")
    }
    if node.Helm == nil {
        return fmt.Errorf("component %q has no helm ref", node.Name)
    }

    action := HelmActionInstall
    if phaseCtx.IsUpgrade {
        action = HelmActionUpgrade
    }

    cv, err := s.getComponentVersion(node.Name, node.Version)
    if err != nil {
        return fmt.Errorf("failed to get component version for %s: %w", node.Name, err)
    }

    opts := HelmInstallOptions{
        Component: cv,
        Cluster:   phaseCtx.BKECluster,
        Action:    action,
        Timeout:   parseDuration(cv.Spec.UpgradeStrategy.Timeout),
    }

    return s.HelmInstaller.Install(ctx, opts)
}
```


#### 9.5.1 getTargetNodes - 目标节点获取


```go
// getTargetNodes 从 PhaseContext 获取组件需要操作的目标节点列表
// 逻辑：根据组件名称决定目标节点范围
//   - containerd / bkeagent: 所有节点
//   - kubernetes (master): 仅控制面节点
//   - kubernetes (worker): 仅工作节点
func (s *Scheduler) getTargetNodes(phaseCtx *phaseframe.PhaseContext, node *topology.ComponentNode) ([]*BKENodeTarget, error) {
    if phaseCtx == nil || phaseCtx.BKECluster == nil {
        return nil, fmt.Errorf("phase context or BKECluster is nil")
    }

    cluster := phaseCtx.BKECluster
    var targets []*BKENodeTarget

    switch node.Name {
    case "containerd", "bkeagent":

> **设计思路 - 硬编码组件名 vs 声明式目标节点**：当前 `getTargetNodes` 使用硬编码组件名决定目标节点范围，这与声明式设计哲学存在冲突。选择此方案的原因是：
> 1. **过渡方案**：当前阶段 Binary 组件类型仅支持 containerd/bkeagent 等有限组件，硬编码是最快落地的路径。
> 2. **声明式演进方向**：后续计划在 `BinarySpec` 中新增 `TargetNodeSelector map[string]string` 字段，通过标签选择器声明式指定目标节点（如 `role: master`），替代硬编码逻辑。
> 3. **default 分支风险**：当前 default 分支默认选择所有节点，对于未知组件可能不正确。应在 `TargetNodeSelector` 实现后移除 default 分支，未指定选择器时返回错误而非默认全节点。
        // 二进制组件：所有节点
        for _, n := range cluster.Spec.Nodes {
            targets = append(targets, &BKENodeTarget{
                IP:            n.IP,
                Hostname:      n.Hostname,
                Role:          n.Role,
                Architecture:  n.Architecture,
                OS:            n.OperatingSystem,
                OSVersion:     n.OperatingSystemVersion,
            })
        }
    case "agent":
        // Agent: 所有节点
        for _, n := range cluster.Spec.Nodes {
            targets = append(targets, &BKENodeTarget{
                IP:            n.IP,
                Hostname:      n.Hostname,
                Role:          n.Role,
                Architecture:  n.Architecture,
                OS:            n.OperatingSystem,
                OSVersion:     n.OperatingSystemVersion,
            })
        }
    default:
        // 默认：所有节点 (可通过 ComponentVersion.Dependencies 进一步过滤)
        for _, n := range cluster.Spec.Nodes {
            targets = append(targets, &BKENodeTarget{
                IP:            n.IP,
                Hostname:      n.Hostname,
                Role:          n.Role,
                Architecture:  n.Architecture,
                OS:            n.OperatingSystem,
                OSVersion:     n.OperatingSystemVersion,
            })
        }
    }

    if len(targets) == 0 {
        return nil, fmt.Errorf("no target nodes found for component %s", node.Name)
    }

    return targets, nil
}

// BKENodeTarget 统一的节点信息结构 (从 BKECluster.Spec.Nodes 提取)

> **设计思路 - BKENodeTarget vs BKENode**：引入 `BKENodeTarget` 而非直接使用 `BKENodeSpec`，是因为 `BKENodeTarget` 是 DAG 调度层的视图——仅包含安装执行所需的核心字段（IP/Hostname/Role/Arch/OS），过滤了 `BKENodeSpec` 中的集群管理字段（如 Labels/Annotations/Taints）。这种解耦使 BinaryInstaller 不依赖完整的 BKENodeSpec 定义，便于后续扩展节点来源（如从 Machine 对象获取节点信息）。
type BKENodeTarget struct {
    IP           string
    Hostname     string
    Role         string // master / worker / etcd
    Architecture string // amd64 / arm64
    OS           string // centos / ubuntu / kylin
    OSVersion    string // 7 / 8 / 20.04 / V10
}
```


#### 9.5.2 executeBinaryRolling / Parallel / Batch - 执行策略实现


```go
// executeBinaryRolling 逐节点滚动执行二进制组件
func (s *Scheduler) executeBinaryRolling(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    nodes []*BKENodeTarget,
    cv *ComponentVersion,
    strategy UpgradeStrategySpec,
    action BinaryAction,
) error {
    for _, node := range nodes {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        opts := BinaryInstallOptions{
            Component:  cv,
            Node:       node,
            Cluster:    phaseCtx.BKECluster,
            Action:     action,
            Timeout:    parseDuration(strategy.Timeout),
            RetryCount: 3,
        }

        if err := s.BinaryInstaller.Install(ctx, opts); err != nil {
            switch strategy.FailurePolicy {
            case "FailFast":
                return fmt.Errorf("node %s failed: %w", node.IP, err)
            case "Continue":
                phaseCtx.Log.Info("BinaryRolling", "node %s failed, continuing: %v", node.IP, err)
                continue
            case "Rollback":
                if rbErr := s.executeBinaryRollback(ctx, phaseCtx, node, cv); rbErr != nil {
                    return fmt.Errorf("node %s upgrade failed: %w; rollback also failed: %v", node.IP, err, rbErr)
                }
                continue
            }
        }
    }
    return nil
}

// executeBinaryParallel 所有节点并行执行二进制组件
func (s *Scheduler) executeBinaryParallel(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    nodes []*BKENodeTarget,
    cv *ComponentVersion,
    strategy UpgradeStrategySpec,
    action BinaryAction,
) error {
    g, gCtx := errgroup.WithContext(ctx)
    sem := make(chan struct{}, len(nodes)) // 不限制并发数

    for _, node := range nodes {
        node := node
        g.Go(func() error {
            select {
            case sem <- struct{}{}:
                defer func() { <-sem }()
            case <-gCtx.Done():
                return gCtx.Err()
            }

            opts := BinaryInstallOptions{
                Component:  cv,
                Node:       node,
                Cluster:    phaseCtx.BKECluster,
                Action:     action,
                Timeout:    parseDuration(strategy.Timeout),
                RetryCount: 3,
            }

            if err := s.BinaryInstaller.Install(gCtx, opts); err != nil {
                if strategy.FailurePolicy == "FailFast" {
                    return err
                }
                phaseCtx.Log.Info("BinaryParallel", "node %s failed, continuing: %v", node.IP, err)
                return nil
            }
            return nil
        })
    }

    return g.Wait()
}

// executeBinaryBatch 分批执行二进制组件
func (s *Scheduler) executeBinaryBatch(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    nodes []*BKENodeTarget,
    cv *ComponentVersion,
    strategy UpgradeStrategySpec,
    action BinaryAction,
) error {
    batchSize := strategy.BatchSize
    if batchSize <= 0 {
        batchSize = 1
    }

    for i := 0; i < len(nodes); i += batchSize {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        end := i + batchSize
        if end > len(nodes) {
            end = len(nodes)
        }
        batch := nodes[i:end]

        g, gCtx := errgroup.WithContext(ctx)
        for _, node := range batch {
            node := node
            g.Go(func() error {
                opts := BinaryInstallOptions{
                    Component:  cv,
                    Node:       node,
                    Cluster:    phaseCtx.BKECluster,
                    Action:     action,
                    Timeout:    parseDuration(strategy.Timeout),
                    RetryCount: 3,
                }
                return s.BinaryInstaller.Install(gCtx, opts)
            })
        }

        if err := g.Wait(); err != nil {
            switch strategy.FailurePolicy {
            case "FailFast":
                return fmt.Errorf("batch [%d:%d] failed: %w", i, end, err)
            case "Continue":
                phaseCtx.Log.Info("BinaryBatch", "batch [%d:%d] failed, continuing: %v", i, end, err)
            case "Rollback":
                for _, node := range batch {
                    s.executeBinaryRollback(ctx, phaseCtx, node, cv)
                }
            }
        }
    }
    return nil
}

// executeBinaryRollback 执行二进制组件回滚

> **设计思路 - Rollback 使用 UninstallScript**：当前回滚逻辑使用卸载脚本执行，而非安装前一个版本。这是有意的简化设计：
> 1. **版本状态不可靠**：当前 `PhaseContext` 不保存已安装版本的历史记录，无法确定"前一个版本"是什么，因此无法执行 `Install(previousVersion)`。
> 2. **卸载脚本的设计意图**：`UninstallScript` 负责停止服务并清理当前版本文件，为重新安装旧版本腾出空间。完整的回滚流程应为：Uninstall(当前版本) → Install(目标版本)，当前仅实现了第一步。
> 3. **改进方向**：在组件状态回写机制（9.7 节）完善后，`BKENode` 上将记录已安装版本，届时 `executeBinaryRollback` 应改为：获取前一个版本 → 执行 `Install(previousVersion)`，实现真正的版本回退。
func (s *Scheduler) executeBinaryRollback(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    node *BKENodeTarget,
    cv *ComponentVersion,
) error {
    if cv.Spec.Binary == nil || cv.Spec.Binary.UninstallScript == "" {
        return fmt.Errorf("no uninstall script defined for component %s", cv.Spec.Name)
    }

    opts := BinaryInstallOptions{
        Component: cv,
        Node:      node,
        Cluster:   phaseCtx.BKECluster,
        Action:    BinaryActionUninstall,
    }

    return s.BinaryInstaller.Install(ctx, opts)
}

// parseDuration 解析超时字符串

> **设计思路 - 默认 10 分钟**：空字符串或解析失败时默认 10 分钟，基于 BKE 组件安装的经验值——containerd 安装约 2-3 分钟，bkeagent 约 1 分钟，10 分钟覆盖了最慢场景并留有余量。不使用"无超时"（0 值）作为默认，是因为无限等待可能导致 reconcile 长期阻塞，影响其他组件的调度。格式错误的超时字符串静默回退到默认值而非报错，是为了兼容性——早期版本的 ComponentVersion 可能包含非标准格式。
func parseDuration(s string) time.Duration {
    if s == "" {
        return 10 * time.Minute
    }
    d, err := time.ParseDuration(s)
    if err != nil {
        return 10 * time.Minute
    }
    return d
}
```


#### 9.5.3 BuildInstallDAG / BuildUpgradeDAG - DAG 构建实现


```go
// pkg/topology/build.go 扩展 (现有 BuildUpgradeDAG 基础上新增)

// BuildInstallDAG 从 ReleaseImage 构建安装 DAG

> **设计思路 - CommonPhases 硬编码**：`BuildInstallDAG` 中 finalizer/paused/manage/delete/dryrun 等 CommonPhases 是硬编码的，而非声明式定义。这是过渡设计：
> 1. **当前约束**：这些 Phase 是现有的硬编码内联执行器（Inline Executor），已有成熟的实现和测试覆盖，声明式改造的 ROI 不高且风险大。
> 2. **兼容性**：CommonPhases 的依赖链（finalizer → paused → manage → delete → dryrun）是固定顺序，声明式定义需要引入排序/优先级机制，增加复杂度。
> 3. **演进方向**：后续可考虑将 CommonPhases 移入 `ReleaseImage` 的固定 section 中，与 DeployPhases 统一管理。但这需要所有组件都以 ComponentVersion 形式定义后才能实现，当前阶段硬编码是最安全的选择。
func BuildInstallDAG(bundle *manifest.Bundle) (*UpgradeDAG, error) {
    dag := NewUpgradeDAG()

    // 1. 添加 CommonPhases (安装流程前序)
    commonPhases := []struct {
        name   string
        policy FailurePolicy
    }{
        {"finalizer", FailurePolicyFailFast},
        {"paused", FailurePolicyContinue},
        {"manage", FailurePolicyContinue},
        {"delete", FailurePolicyContinue},
        {"dryrun", FailurePolicyFailFast},
    }
    for _, p := range commonPhases {
        dag.AddNode(&ComponentNode{
            Name:          p.name,
            FailurePolicy: p.policy,
            Inline:        &InlineRef{Handler: "Ensure" + strings.Title(p.name)},
        })
    }
    // CommonPhases 依赖链: finalizer → paused → manage → delete → dryrun
    dag.AddDependency("finalizer", "paused")
    dag.AddDependency("paused", "manage")
    dag.AddDependency("manage", "delete")
    dag.AddDependency("delete", "dryrun")

    // 2. 从 ReleaseImage install.components 动态构建 DeployPhases
    for _, comp := range bundle.Spec.Install.Components {
        cv := bundle.GetComponent(comp.Name, comp.Version)
        if cv == nil {
            continue
        }

        node := &ComponentNode{
            Name:          comp.Name,
            Version:       comp.Version,
            FailurePolicy: getFailurePolicy(cv.Spec.UpgradeStrategy.FailurePolicy),
            Dependencies:  getDependencyNames(cv.Spec.Dependencies),
        }

        // 根据组件类型设置引用
        switch cv.Spec.Type {
        case "binary":
            node.Binary = &BinaryRef{Name: comp.Name, Version: comp.Version}
        case "helm":
            node.Helm = &HelmRef{
                Name:        comp.Name,
                Version:     comp.Version,
                Namespace:   cv.Spec.Helm.Namespace,
                ReleaseName: cv.Spec.Helm.ReleaseName,
            }
        case "inline":
            node.Inline = &InlineRef{Handler: cv.Spec.Inline.Handler, Version: cv.Spec.Inline.Version}
        case "yaml":
            namespace := ""
            if cv.Spec.YAML != nil {
                namespace = cv.Spec.YAML.Namespace
            }
            node.YAML = &YAMLRef{
                Name:      comp.Name,
                Version:   comp.Version,
                Namespace: namespace,
            }
        default:
            // 未知类型不设置特殊引用，仍走 executeManifest 兼容路径
        }

        dag.AddNode(node)

        // 所有部署组件依赖 dryrun
        dag.AddDependency("dryrun", comp.Name)

        // 添加组件间依赖
        for _, dep := range cv.Spec.Dependencies {
            dag.AddDependency(dep.Name, comp.Name)
        }
    }

    return dag, nil
}

// getFailurePolicy 将字符串转换为 FailurePolicy
func getFailurePolicy(policy string) FailurePolicy {
    switch policy {
    case "FailFast":
        return FailurePolicyFailFast
    case "Continue":
        return FailurePolicyContinue
    case "Rollback":
        return FailurePolicyRollback
    default:
        return FailurePolicyContinue
    }
}

// getDependencyNames 提取依赖组件名称列表
func getDependencyNames(deps []Dependency) []string {
    names := make([]string, 0, len(deps))
    for _, d := range deps {
        names = append(names, d.Name)
    }
    return names
}
```

---


### 9.6 NeedExecute 接口适配



#### 9.6.1 现有 NeedExecute 机制分析

当前 `NeedExecute` 接口定义在 `phaseframe.Phase` 接口中：

```go
// 现有接口 (pkg/phaseframe/phases/phase_flow.go)
type Phase interface {
    Name() string
    NeedExecute(old, new *bkev1beta1.BKECluster) bool
    Execute() error
}
```

对于 inline Phase，`NeedExecute` 通过 `NeedExecuteWithVersionContext` 判断版本是否需要升级。
对于 Binary/Helm 组件，不在 Phase 流程中，而是在 DAG 调度流程中执行。

#### 9.6.2 Binary/Helm 组件的 NeedExecute 适配方案

Binary/Helm 组件在 DAG 调度器中执行，**不直接实现 Phase 接口**。其"是否需要执行"的判断逻辑如下：

```go
// pkg/dagexec/need_execute.go

// shouldExecuteBinary 判断二进制组件是否需要执行
// 逻辑: 基于 VersionContext 判断版本是否变更
func (s *Scheduler) shouldExecuteBinary(phaseCtx *phaseframe.PhaseContext, node *topology.ComponentNode) bool {
    // 1. 如果 VersionContext 存在，使用版本上下文判断
    if phaseCtx.VersionContext != nil {
        if phaseCtx.VersionContext.HasTarget(node.Name) {
            return phaseCtx.VersionContext.NeedsUpgrade(node.Name)
        }
    }

    // 2. 回退: 检查 BKECluster.Status.DeclarativeUpgrade 是否已记录完成
    if phaseCtx.BKECluster != nil && phaseCtx.BKECluster.Status.DeclarativeUpgrade != nil {
        return !phaseCtx.BKECluster.Status.DeclarativeUpgrade.IsCompleted(node.Name, node.Version)
    }

    // 3. 默认需要执行
    return true
}

// shouldExecuteHelm 判断 Helm 组件是否需要执行
// 逻辑: 同 shouldExecuteBinary

> **设计思路 - 两个相同函数**：当前 `shouldExecuteHelm` 直接调用 `shouldExecuteBinary`，逻辑完全相同。保留两个独立函数而非合并为 `shouldExecuteComponent`，是因为 Binary 和 Helm 组件的判定逻辑可能在未来分化——例如 Helm 组件需要额外检查 Release 是否已存在，Binary 组件需要检查节点上的已安装版本。当前阶段合并为统一函数是过早抽象，保留独立函数为后续差异化留出空间。
func (s *Scheduler) shouldExecuteHelm(phaseCtx *phaseframe.PhaseContext, node *topology.ComponentNode) bool {
    return s.shouldExecuteBinary(phaseCtx, node)
}
```

#### 9.6.3 shouldSkipComponent 扩展

当前 `shouldSkipComponent` 仅检查 `DeclarativeUpgrade.IsCompleted`，需扩展以支持 Binary/Helm：

```go
// shouldSkipComponent 扩展实现
func (s *Scheduler) shouldSkipComponent(phaseCtx *phaseframe.PhaseContext, node *topology.ComponentNode) bool {
    if phaseCtx == nil || phaseCtx.BKECluster == nil || node == nil {
        return false
    }

    // 检查 DeclarativeUpgrade 状态
    st := phaseCtx.BKECluster.Status.DeclarativeUpgrade
    if st != nil && st.IsCompleted(node.Name, s.nodeVersionKey(node)) {
        return true
    }

    // Binary/Helm 组件: 使用 VersionContext 判断是否需要跳过
    switch node.ComponentType() {
    case "binary":
        return !s.shouldExecuteBinary(phaseCtx, node)
    case "helm":
        return !s.shouldExecuteHelm(phaseCtx, node)
    }

    return false
}
```

---


### 9.7 组件状态回写机制



#### 9.7.1 状态回写流程

```
组件执行完成 (成功/失败)
        │
        ▼
┌──────────────────────────┐
│  BinaryComponentExecutor │
│  HelmComponentExecutor   │
│  调用回调函数            │
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────────────────┐
│  Scheduler.markComponentCompleted()  │  ← 已存在
│  Scheduler.markComponentFailed()     │  ← 已存在
│                                      │
│  写入 BKECluster.Status.             │
│    DeclarativeUpgrade                │
│      .MarkCompleted(name, version)   │
│      .MarkFailure(name, version, err)│
└──────────────────────────────────────┘
```

#### 9.7.2 Binary/Helm 组件状态记录

当前 `DeclarativeUpgrade` 状态已支持 `IsCompleted`/`MarkCompleted`/`MarkFailure`。
Binary/Helm 组件复用同一状态结构，无需扩展。

```go
// Binary/Helm 组件执行成功后:
// Scheduler 已有 markComponentCompleted() 方法
// 在 executeBinary/executeHelm 成功返回后由 persistBatchResults 统一处理

// Binary/Helm 组件执行失败后:
// Scheduler 已有 markComponentFailed() 方法
// 在 persistBatchResults 中统一处理
```

#### 9.7.3 BinaryComponentExecutor 逐节点状态记录

对于滚动升级场景，需要记录每个节点的执行状态：

```go
// pkg/dagexec/node_status.go

// BinaryNodeStatus 记录二进制组件在单个节点上的执行状态
type BinaryNodeStatus struct {
    NodeIP    string `json:"nodeIP"`
    NodeName  string `json:"nodeName"`
    Status    string `json:"status"` // Succeeded / Failed / Pending
    Error     string `json:"error,omitempty"`
    Timestamp string `json:"timestamp"`
}

// 记录节点级别状态到 PhaseContext (用于日志和状态查询)
func recordBinaryNodeStatus(phaseCtx *phaseframe.PhaseContext, componentName string, status BinaryNodeStatus) {
    if phaseCtx == nil || phaseCtx.Log == nil {
        return
    }

    if status.Status == "Succeeded" {
        phaseCtx.Log.Info("BinaryNodeSucceeded",
            "component=%s, node=%s, status=%s", componentName, status.NodeIP, status.Status)
    } else {
        phaseCtx.Log.Info("BinaryNodeFailed",
            "component=%s, node=%s, status=%s, error=%s", componentName, status.NodeIP, status.Status, status.Error)
    }
}
```

---


## 10. 完整安装流程详细设计

### 10.1 安装流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           完整安装流程                                           │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  用户创建        │
                              │  BKECluster      │
                              │  desiredVersion: │
                              │  v2.6.0          │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  BKEClusterReconciler.Reconcile()    │
                    │  检测到新集群创建                    │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  1. 解析 ReleaseImage v2.6.0         │
                    │  releaseImage.GetInstallComponents() │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  install.components:                 │
                    │  ├── containerd/v1.7.18 (binary)     │
                    │  ├── bkeagent/v2.6.0 (binary)        │
                    │  ├── coredns/v1.11.1 (helm)          │
                    │  └── kubernetes/v1.29.0 (composite)  │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  2. 加载 ComponentVersion            │
                    │  manifestStore.GetComponentVersion() │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 构建安装 DAG                     │
                    │  BuildInstallDAG(releaseImage)       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  DAG 结构:                           │
                    │  finalizer → ... → dryrun            │
                    │                   → agent (binary)   │
                    │                   → containerd       │
                    │                   → apiobj → certs   │
                    │                   → master_init      │
                    │                   → master_join      │
                    │                   → worker_join      │
                    │                   → coredns (helm)   │
                    │                   → addon            │
                    │                   → postprocess      │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  4. DAG Scheduler 执行               │
                    │  scheduler.ExecuteDAG(ctx, dag)      │
                    └────────────────────┬─────────────────┘
                                         │
              ┌──────────────────────────┼──────────────────────────┐
              │                          │                          │
              ▼                          ▼                          ▼
    ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
    │  Batch 1:        │      │  Batch 2:        │      │  Batch 3:        │
    │  CommonPhases    │      │  DeployPhases    │      │  PostPhases      │
    │  (前置判断)      │      │  (核心部署)      │      │  (后置处理)      │
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             └────────────────────────┼────────────────────────┘
                                      │
                                      ▼
                    ┌──────────────────────────────────────┐
                    │  5. BinaryComponentExecutor          │
                    │  执行 containerd 安装                │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  下载制品       │  │  渲染脚本       │  │  SSH 执行       │
          │  containerd     │  │  installScript  │  │  安装脚本       │
          │  tar.gz         │  │  configTemplates│  │                 │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  6. BinaryComponentExecutor          │
                    │  执行 bkeagent 安装                  │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  7. HelmComponentExecutor            │
                    │  执行 coredns 安装                   │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  拉取 Chart     │  │  渲染 Values    │  │  Helm Install   │
          │  OCI Registry   │  │  模板变量       │  │  --atomic       │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  8. 健康检查                         │
                    │  PodReady + EndpointReady            │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         检查通过                检查失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  安装完成       │   │  返回错误       │
                    │  ClusterStatus  │   │  触发回滚       │
                    │  = Ready        │   │                 │
                    └─────────────────┘   └─────────────────┘
```

---

## 11. 完整升级流程详细设计

### 11.1 升级流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           完整升级流程                                           │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  用户修改        │
                              │  ClusterVersion  │
                              │  desiredVersion: │
                              │  v2.5.0 → v2.6.0 │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  ClusterVersionReconciler            │
                    │  检测到版本变更                      │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  1. 解析 ReleaseImage v2.6.0         │
                    │  releaseImage.GetUpgradeComponents() │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  2. 解析当前 ReleaseImage v2.5.0     │
                    │  currentReleaseImage                 │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 对比版本，确定需要升级的组件     │
                    │  compareVersions(current, target)    │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  需要升级的组件:                     │
                    │  ├── containerd: v1.7.15 → v1.7.18   │
                    │  ├── bkeagent: v2.5.0 → v2.6.0       │
                    │  └── coredns: v1.10.1 → v1.11.1      │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  4. 构建升级 DAG                     │
                    │  BuildUpgradeDAG(releaseImage)       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  DAG 结构:                           │
                    │  provider → agent (binary)           │
                    │            → containerd (binary)     │
                    │            → coredns (helm)          │
                    │            → etcd (inline)           │
                    │            → worker (inline)         │
                    │            → master (inline)         │
                    │            → component → cluster     │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  5. DAG Scheduler 执行 (按拓扑批次)  │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  Batch 1:        │  │  Batch 2:        │  │  Batch 3:        │
          │  provider        │  │  agent (binary)  │  │  containerd      │
          │                  │  │  逐节点滚动升级  │  │  (binary)        │
          │                  │  │                  │  │  逐节点滚动升级  │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  Batch 4: coredns (helm)             │
                    │  helm upgrade --atomic               │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  Batch 5: etcd → worker → master     │
                    │  (inline Phase 执行)                 │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  Batch 6: component → cluster        │
                    │  最终健康检查                        │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         升级成功                升级失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  升级完成       │   │  根据策略处理   │
                    │  ClusterStatus  │   │  FailFast/      │
                    │  = Ready        │   │  Continue/      │
                    │                 │   │  Rollback       │
                    └─────────────────┘   └─────────────────┘
```

---

## 12. 迁移策略详细设计

### 12.1 迁移流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           迁移策略流程                                           │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  Feature Gate    │
                              │  检查            │
                              │  BinaryComponent │
                              │  Support         │
                              └────────┬─────────┘
                                       │
                              ┌────────┴────────┐
                              │                 │
                         启用 (true)        禁用 (false)
                              │                 │
                              ▼                 ▼
                    ┌─────────────────┐ ┌─────────────────┐
                    │  新路径         │ │  旧路径         │
                    │  DAG +          │ │  硬编码 Phase   │
                    │  BinaryInstaller│ │  执行           │
                    └────────┬────────┘ └────────┬────────┘
                             │                   │
                             └─────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  兼容层处理                          │
                    │  executeContainerdUpgrade()          │
                    │  {                                   │
                    │    if featuregate.Enabled(...) {     │
                    │      return executeBinaryComponent() │
                    │    }                                 │
                    │    return executeLegacyPhase()       │
                    │  }                                   │
                    └──────────────────────────────────────┘
```

### 12.2 Feature Gate 定义

> **设计思路 - 环境变量 vs CRD 字段**：Feature Gate 通过环境变量（`BKE_BINARY_COMPONENT_SUPPORT`、`BKE_HELM_COMPONENT_SUPPORT`）控制，而非 BKECluster spec 字段或 FeatureGate CRD。选择环境变量的原因是：
> 1. **渐进式发布**：Feature Gate 的目的是在功能未完全稳定前限制使用范围，环境变量需要运维显式设置，天然具备灰度效果。
> 2. **安全性**：默认关闭新功能，环境变量需要重启控制器 Pod 才能生效，避免了运行时误开启导致的状态不一致。
> 3. **实现简单**：不需要引入新的 CRD、API 版本、Admission Webhook，零侵入。
> 4. **局限与改进方向**：环境变量不可审计、不可在集群状态中观察。当 Binary/Helm 组件功能稳定后（GA），Feature Gate 将被移除，功能永久开启，不需要迁移到 CRD 方案。

```go
// pkg/featuregate/features.go

const (
    // BinaryComponentSupport 启用二进制组件支持
    BinaryComponentSupport = "BinaryComponentSupport"
    
    // HelmComponentSupport 启用 Helm 组件支持
    HelmComponentSupport = "HelmComponentSupport"
)

// 默认关闭
var defaultFeatureGates = map[string]bool{
    BinaryComponentSupport: false,
    HelmComponentSupport:   false,
}
```

---

### 12.3 兼容层实现与迁移验证



#### 12.3.1 Feature Gate 扩展

```go
// pkg/featuregate/features.go 扩展

const (
    // 现有
    DeclarativeUpgradeAnnotationKey = "cvo.openfuyao.cn/declarative-upgrade"
    UpgradeReadyAnnotationKey       = "cvo.openfuyao.cn/upgrade-ready"

    // 新增
    BinaryComponentSupport = "BinaryComponentSupport"
    HelmComponentSupport   = "HelmComponentSupport"
)

// Enabled 检查 Feature Gate 是否启用
// 兼容现有注解式实现
func Enabled(gate string) bool {
    switch gate {
    case BinaryComponentSupport:
        // 通过 BKECluster Annotation 或环境变量控制
        return os.Getenv("BKE_BINARY_COMPONENT_SUPPORT") == "true"
    case HelmComponentSupport:
        return os.Getenv("BKE_HELM_COMPONENT_SUPPORT") == "true"
    default:
        return false
    }
}
```

#### 12.3.2 BKEClusterReconciler 兼容层

```go
// controllers/capbke/bkecluster_upgrade_dag.go 扩展

// executeContainerdUpgrade 兼容层: 新旧双轨运行
func (r *BKEClusterReconciler) executeContainerdUpgrade(ctx context.Context, phaseCtx *phaseframe.PhaseContext) error {
    if featuregate.Enabled(featuregate.BinaryComponentSupport) {
        // 新路径: 通过 DAG + BinaryInstaller 执行
        return r.executeBinaryComponent(ctx, phaseCtx, "containerd")
    }
    // 旧路径: 使用硬编码 Phase EnsureContainerdUpgrade
    return r.executeLegacyContainerdUpgrade(ctx, phaseCtx)
}

// executeBKEAgentUpgrade 兼容层
func (r *BKEClusterReconciler) executeBKEAgentUpgrade(ctx context.Context, phaseCtx *phaseframe.PhaseContext) error {
    if featuregate.Enabled(featuregate.BinaryComponentSupport) {
        return r.executeBinaryComponent(ctx, phaseCtx, "bkeagent")
    }
    return r.executeLegacyBKEAgentUpgrade(ctx, phaseCtx)
}

// executeBinaryComponent 通过 BinaryInstaller 执行二进制组件升级
func (r *BKEClusterReconciler) executeBinaryComponent(ctx context.Context, phaseCtx *phaseframe.PhaseContext, componentName string) error {
    // 由 DAG Scheduler 统一调度，此方法仅做路由
    // 实际逻辑在 dagexec.Scheduler.executeBinary() 中
    return nil
}
```

#### 12.3.3 迁移验证清单

| 检查项 | 验证方式 | 通过标准 |
|--------|---------|---------|
| Feature Gate 关闭时旧路径正常 | 设置 `BKE_BINARY_COMPONENT_SUPPORT=false` | containerd/bkeagent 通过旧 Phase 安装/升级 |
| Feature Gate 开启时新路径正常 | 设置 `BKE_BINARY_COMPONENT_SUPPORT=true` | containerd/bkeagent 通过 BinaryInstaller 安装/升级 |
| 新旧路径结果一致 | 对比新旧路径的安装结果 | 二进制版本/配置/服务状态一致 |
| 灰度切换无中断 | 运行中切换 Feature Gate | 已安装节点不受影响，新节点走新路径 |
| 升级中途切换 Feature Gate | 升级过程中切换 | 已完成节点正常，未完成节点走对应路径 |

---


## 13. 错误处理与恢复

### 13.1 错误处理流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           错误处理与恢复流程                                     │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  组件执行失败    │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  1. 错误分类                         │
                    │  classifyError(err)                  │
                    └────────────────────┬─────────────────┘
                                         │
              ┌──────────────────────────┼──────────────────────────┐
              │                          │                          │
              ▼                          ▼                          ▼
    ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
    │  可重试错误     │      │  不可重试错误   │      │  部分失败       │
    │  (网络超时等)   │      │  (配置错误等)   │      │  (部分节点失败) │
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             ▼                        ▼                        ▼
    ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
    │  检查重试次数   │      │  检查失败策略   │      │  检查失败策略   │
    │  retryCount <   │      │  FailurePolicy  │      │  FailurePolicy  │
    │  maxRetries?    │      │                 │      │                 │
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
    ┌────────┴────────┐      ┌────────┴────────┐      ┌────────┴────────┐
    │                 │      │                 │      │                 │
    ▼                 ▼      ▼                 ▼      ▼                 ▼
  是                否    FailFast          Continue  FailFast        Continue
  │                 │      │                 │      │                 │
  ▼                 ▼      ▼                 ▼      ▼                 ▼
┌─────────┐  ┌─────────┐ ┌─────────┐  ┌─────────┐ ┌─────────┐  ┌─────────┐
│ 重试执行 │  │ 返回错误│ │ 立即终止│  │ 记录错误│ │ 立即终止│  │ 记录错误│
│ retry() │  │ return  │ │ return  │  │ 继续执行│ │ return  │  │ 继续执行│
└─────────┘  │  err    │ │  err    │  │ 下一节点│ │  err    │  │ 下一节点│
             └─────────┘ └─────────┘  └─────────┘ └─────────┘  └─────────┘
```

---

### 13.2 错误分类实现



#### 13.2.1 classifyError - 错误分类

```go
// pkg/dagexec/error_classifier.go

// ErrorCategory 错误类别
type ErrorCategory string

const (
    ErrorCategoryRetryable    ErrorCategory = "Retryable"    // 可重试错误
    ErrorCategoryNonRetryable ErrorCategory = "NonRetryable" // 不可重试错误
    ErrorCategoryPartial      ErrorCategory = "Partial"      // 部分失败
)

// classifyError 对错误进行分类

> **设计思路 - 字符串匹配 vs 类型化错误**：使用字符串子串匹配（如 `strings.Contains(err.Error(), "connection refused")`）而非类型化错误（如 `type RetryableError struct{ error }`）进行错误分类。原因是：
> 1. **错误来源多样**：错误来自 SSH（bkessh）、Helm SDK、Kubernetes client、HTTP 下载等多个库，这些库的错误类型不统一，无法用统一的类型断言覆盖。
> 2. **第三方库不可控**：Helm SDK 和 Kubernetes client 的错误类型可能随版本变化，字符串匹配虽然脆弱但更稳定——错误消息的语义通常保持一致。
> 3. **脆弱性缓解**：① 维护关键错误模式的白名单，定期与依赖库版本对齐；② 未匹配的错误默认归类为 `NonRetryable`（安全侧），避免盲目重试导致雪崩；③ 后续可引入自定义错误类型包装器，在 Installer 层将第三方错误统一转换为类型化错误。
func classifyError(err error) ErrorCategory {
    if err == nil {
        return ErrorCategoryRetryable
    }

    errMsg := err.Error()

    // 可重试错误: 网络/超时/临时性错误
    retryablePatterns := []string{
        "connection refused",
        "connection reset",
        "timeout",
        "i/o timeout",
        "temporary",
        "EOF",
        "dial tcp",
        "network is unreachable",
    }
    for _, pattern := range retryablePatterns {
        if strings.Contains(strings.ToLower(errMsg), strings.ToLower(pattern)) {
            return ErrorCategoryRetryable
        }
    }

    // 部分失败: 节点级别错误 (滚动升级中部分节点成功、部分失败)
    if strings.Contains(errMsg, "node") && strings.Contains(errMsg, "failed") {
        return ErrorCategoryPartial
    }

    // 不可重试错误: 配置错误/校验失败/版本不兼容
    return ErrorCategoryNonRetryable
}
```


## 14. 测试设计

### 14.1 单元测试

| 测试模块 | 测试场景 | 覆盖目标 |
|---------|---------|---------|
| **ArtifactDownloader** | HTTP 下载、Checksum 校验、缓存命中/未命中、架构适配 | >90% |
| **TemplateRenderer** | 8 类变量替换、条件渲染、自定义函数、错误处理 | >90% |
| **ConfigRenderer** | content 渲染、secretRef 获取、kubeconfig 生成 | >90% |
| **BinaryInstaller** | Install/Upgrade/Uninstall 完整流程、失败重试 | >85% |
| **HelmInstaller** | OCI/HTTP/本地 Chart 获取、Values 渲染、Install/Upgrade/Rollback | >85% |
| **BinaryComponentExecutor** | Rolling/Parallel/Batch 执行策略、FailurePolicy | >85% |

### 14.2 集成测试

| 测试场景 | 验证内容 | 预期结果 |
|---------|---------|---------|
| **全新安装 (binary)** | containerd + bkeagent 安装 | 二进制正确安装，服务启动，版本验证通过 |
| **全新安装 (helm)** | coredns + kube-proxy 安装 | Chart 正确部署，Pod Ready，Endpoint Ready |
| **升级 (binary)** | containerd v1.7.15 → v1.7.18 | 逐节点滚动升级，服务不中断 |
| **升级 (helm)** | coredns v1.10.1 → v1.11.1 | helm upgrade 成功，Pod 滚动更新 |
| **回滚 (binary)** | 升级失败后执行 uninstallScript | 旧版本恢复，服务正常 |
| **回滚 (helm)** | helm upgrade 失败后 rollback | 自动回滚到上一版本 |
| **离线环境** | 无网络时使用本地缓存 | 安装/升级正常完成 |
| **多架构** | amd64 + arm64 混合集群 | 各节点下载对应架构制品 |

### 14.3 E2E 测试

| 测试场景 | 集群规模 | 验证内容 |
|---------|---------|---------|
| **小规模安装** | 1 Master + 2 Worker | 完整安装流程，所有组件正常 |
| **中规模安装** | 3 Master + 10 Worker | 并行安装性能，无资源竞争 |
| **跨版本升级** | 3 Master + 5 Worker | v2.5.0 → v2.6.0 完整升级 |
| **升级失败恢复** | 3 Master + 3 Worker | 模拟节点失败，验证 Continue/Rollback 策略 |

---

## 15. 工作量与任务拆解

### 15.1 工作量评估

| 任务 | 预估工时 | 风险等级 | 依赖 |
|------|---------|---------|------|
| **BinaryInstaller 核心实现** | 5 人日 | 中 | 无 |
| **HelmInstaller 核心实现** | 5 人日 | 中 | 无 |
| **TemplateRenderer 实现** | 3 人日 | 低 | 无 |
| **ConfigRenderer 实现** | 3 人日 | 低 | TemplateRenderer |
| **ComponentVersion CRD 扩展** | 2 人日 | 低 | 无 |
| **BinaryComponentExecutor 集成** | 3 人日 | 中 | BinaryInstaller |
| **HelmComponentExecutor 集成** | 3 人日 | 中 | HelmInstaller |
| **ComponentVersion YAML 编写** | 2 人日 | 低 | CRD 扩展 |
| **DAG 调度器适配** | 2 人日 | 低 | Executor 集成 |
| **Feature Gate 实现** | 1 人日 | 低 | 无 |
| **单元测试** | 5 人日 | 低 | 核心实现完成 |
| **集成测试** | 3 人日 | 中 | 单元测试完成 |
| **E2E 测试** | 3 人日 | 中 | 集成测试完成 |
| **文档编写** | 2 人日 | 低 | 无 |
| **代码审查与修复** | 3 人日 | 中 | 测试完成 |
| **总计** | **45 人日** | | |

### 15.2 Sprint 计划

#### Sprint 1 (第1-2周): BinaryInstaller 核心实现

| 任务 | 负责人 | 交付物 |
|------|--------|--------|
| BinaryInstaller 结构定义 | 开发A | `pkg/binaryinstaller/installer.go` |
| ArtifactDownloader 实现 | 开发A | 下载/缓存/checksum 功能 |
| TemplateRenderer 实现 | 开发B | `pkg/binaryinstaller/template_renderer.go` |
| ConfigRenderer 实现 | 开发B | `pkg/binaryinstaller/config_renderer.go` |
| SSH 执行逻辑 | 开发A | 上传/执行/日志收集 |
| 单元测试 (BinaryInstaller) | 开发A+B | 测试覆盖率 >85% |

#### Sprint 2 (第3-4周): HelmInstaller 核心实现

| 任务 | 负责人 | 交付物 |
|------|--------|--------|
| HelmInstaller 结构定义 | 开发C | `pkg/helminstaller/installer.go` |
| ChartFetcher 实现 | 开发C | OCI/HTTP/本地 Chart 获取 |
| ValuesRenderer 实现 | 开发C | Values 模板渲染 |
| Helm Action Executor 实现 | 开发C | Install/Upgrade/Rollback/Uninstall |
| HealthCheck 实现 | 开发C | PodReady/EndpointReady 检查 |
| 单元测试 (HelmInstaller) | 开发C | 测试覆盖率 >85% |

#### Sprint 3 (第5-6周): DAG 集成与 Phase 迁移

| 任务 | 负责人 | 交付物 |
|------|--------|--------|
| ComponentVersion CRD 扩展 | 开发A | binary/helm 字段定义 |
| BinaryComponentExecutor | 开发A | `pkg/dagexec/binary_component_executor.go` |
| HelmComponentExecutor | 开发C | `pkg/dagexec/helm_component_executor.go` |
| ComponentVersion YAML 编写 | 开发B | containerd/bkeagent/coredns YAML |
| DAG 调度器适配 | 开发B | 执行器注册与调度 |
| Feature Gate 实现 | 开发A | 开关控制逻辑 |
| 集成测试 | 开发A+B+C | 安装/升级/回滚场景 |
| E2E 测试 | 开发A+B+C | 多场景端到端验证 |

### 15.3 里程碑

| 里程碑 | 时间 | 交付内容 | 验收标准 |
|--------|------|---------|---------|
| **M1: BinaryInstaller 完成** | 第2周末 | BinaryInstaller 核心功能 + 单元测试 | 单元测试覆盖率 >85% |
| **M2: HelmInstaller 完成** | 第4周末 | HelmInstaller 核心功能 + 单元测试 | 单元测试覆盖率 >85% |
| **M3: DAG 集成完成** | 第5周末 | Executor 集成 + ComponentVersion YAML | 集成测试通过 |
| **M4: Beta 发布** | 第6周末 | Feature Gate 灰度 + E2E 测试 | E2E 通过率 >95% |

---

---

## 16. 附录

### 16.1 参考文档

- KEP-5: 基于 ClusterVersion/ReleaseImage/UpgradePath 的声明式集群版本升级
- KEP-6: 基于 ReleaseImage 的二进制与 Helm 组件声明式管理方案
- ComponentVersion CRD 定义
- ReleaseImage CRD 定义
- DAG 调度器设计文档
- Helm Action API: https://pkg.go.dev/helm.sh/helm/v3/pkg/action

### 16.2 术语表

| 术语 | 定义 |
|------|------|
| **BinaryInstaller** | 负责二进制组件下载、渲染、安装的安装器 |
| **HelmInstaller** | 负责 Helm Chart 获取、渲染、部署的安装器 |
| **configTemplates** | 配置文件模板系统，支持 Go template/Secret/kubeconfig |
| **installScript** | 安装脚本模板，支持 8 类 50+ 变量和条件渲染 |
| **Artifact** | 二进制制品，包含 URL、Checksum、安装路径等信息 |
| **ArtifactCache** | 二进制制品本地文件缓存管理器 |
| **ComponentVersion** | 组件版本 CRD，定义组件的类型、配置、依赖等 |
| **ReleaseImage** | 发布版本清单 CRD，定义安装和升级的组件列表 |
| **DAG** | 有向无环图，用于表示组件依赖关系和执行顺序 |
| **Feature Gate** | 功能开关，用于控制新功能的启用/禁用 |
| **Rolling Update** | 滚动更新，逐节点执行升级操作 |
| **ScriptData** | 安装脚本模板渲染数据，包含 8 类 50+ 变量 |
| **BKENodeTarget** | 统一的节点信息结构，从 BKECluster.Spec.Nodes 提取 |

---

**文档版本**: v1.1  
**创建日期**: 2026-06-12  
**最后更新**: 2026-06-23  
**维护者**: openFuyao Team
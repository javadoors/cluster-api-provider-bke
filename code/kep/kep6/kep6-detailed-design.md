# KEP-6 详细设计文档：基于 ReleaseImage 的二进制与 Helm 组件声明式管理方案

**文档版本**: v1.0  
**状态**: Draft  
**依赖**: KEP-6 提案文档

---

## 目录

1. [概述](#1-概述)
2. [整体架构设计](#2-整体架构设计)
3. [ComponentVersion CRD 详细设计](#3-componentversion-crd-详细设计)
   - 3.1 ComponentVersion 类型定义
   - 3.2 Helm 类型字段定义
   - 3.3 CRD YAML 定义
   - 3.4 CRD 版本迁移设计
4. [BinaryInstaller 详细设计](#4-binaryinstaller-详细设计)
5. [HelmInstaller 详细设计](#5-helminstaller-详细设计)
6. [TemplateRenderer 详细设计](#6-templaterenderer-详细设计)
7. [ConfigRenderer 详细设计](#7-configrenderer-详细设计)
8. [DAG 集成详细设计](#8-dag-集成详细设计)
9. [完整安装流程详细设计](#9-完整安装流程详细设计)
10. [完整升级流程详细设计](#10-完整升级流程详细设计)
11. [迁移策略详细设计](#11-迁移策略详细设计)
12. [错误处理与恢复](#12-错误处理与恢复)
13. [测试设计](#13-测试设计)
14. [工作量与任务拆解](#14-工作量与任务拆解)
15. [附录](#15-附录)
16. [安装与升级样例](#16-安装与升级样例)
    - 16.1 安装样例
    - 16.2 升级样例
    - 16.3 关键设计点说明

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

### 3.1 ComponentVersion 类型定义

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
    
    // 兼容性约束
    Compatibility CompatibilitySpec `json:"compatibility,omitempty"`
    
    // 依赖关系
    Dependencies []Dependency `json:"dependencies,omitempty"`
    
    // 升级策略
    UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
    
    // Kubernetes 资源定义列表
    // 注意: 此字段位于顶层是历史原因——最初无独立 YAML 类型, Resources 用于所有类型
    // 新增 YAML 类型后, 理论上应迁移至 YAMLSpec, 但为保持向后兼容暂不移动
    // 后续版本可考虑: ① 迁移到 YAMLSpec; ② 或保持顶层但标注仅 YAML 类型生效
    // 当前代码中 EnsurePreUpgradeResources Phase 和 ManifestComponentExecutor 均使用此字段
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
    // YAML 清单文件列表 (外部 URL 引用)
    Manifests []ManifestRef `json:"manifests"`
    
    // 注意: 内联 K8s 资源定义通过 ComponentVersionSpec.Resources (顶层) 提供
    // 后续版本可考虑将 Resources 迁移至此字段, 作为 YAML 类型的专属配置
    // 当前为保持向后兼容, Resources 仍位于 ComponentVersionSpec 顶层
    
    // 部署目标命名空间
    Namespace string `json:"namespace,omitempty"`
    
    // 应用策略: ServerSideApply, Replace, CreateOnly
    ApplyStrategy string `json:"applyStrategy,omitempty"`
    
    // 是否启用裁剪 (按 label selector 删除不再需要的资源)
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
    
    // 健康检查配置 (安装/升级后通过 SSH 执行脚本验证服务可用性)
    HealthCheck *BinaryHealthCheckSpec `json:"healthCheck,omitempty"`
}

// BinaryHealthCheckSpec 定义二进制组件健康检查规格
// 与 Helm 的 HealthCheckSpec 不同, Binary 组件运行在远程节点上,
// 健康检查通过 SSH 执行脚本完成, 退出码 0=健康, 非零=不健康
type BinaryHealthCheckSpec struct {
    // 是否启用健康检查
    Enabled bool `json:"enabled"`
    
    // 等待超时时间 (默认 2m)
    Timeout string `json:"timeout,omitempty"`
    
    // 重试间隔 (默认 5s)
    Interval string `json:"interval,omitempty"`
    
    // 健康检查脚本 (Go template, 通过 SSH 在远程节点执行)
    // 支持 installScript 的所有模板变量 ({{artifact.<name>.installPath}}, {{configPath}} 等)
    // 退出码 0 = 健康, 非零 = 不健康
    // 可组合多个检查命令, 任一失败则整体失败
    Script string `json:"script"`
}

// ArtifactSpec 定义二进制制品规格
type ArtifactSpec struct {
    // 制品名称
    Name string `json:"name"`
    
    // 制品 URL (支持模板变量)
    URL string `json:"url"`
    
    // 制品校验和 (格式: sha256:xxx)
    Checksum string `json:"checksum"`
    
    // 安装路径 (per-artifact, 不同 artifact 可安装到不同路径)
    // 通过 {{artifact.<name>.installPath}} 模板变量在 installScript 中引用
    // 例如: containerd tar.gz → "/usr/local", runc → "/usr/local/sbin"
    InstallPath string `json:"installPath"`
    
    // 可执行文件名
    Executable string `json:"executable,omitempty"`
}

// ConfigTemplateSpec 定义配置文件模板规格
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

### 3.2 Helm 类型字段定义

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

// OCIChartSpec 定义 OCI Chart 规格
type OCIChartSpec struct {
    // OCI 仓库地址
    Repository string `json:"repository"`
    
    // Chart 标签
    Tag string `json:"tag"`
}

// HelmStrategySpec 定义 Helm 安装策略
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
type HookSpec struct {
    // 钩子名称
    Name string `json:"name,omitempty"`
    
    // 钩子类型: Job
    Type string `json:"type"`
    
    // 钩子 Manifest
    Manifest string `json:"manifest"`
}

// InlineSpec 定义内联执行器配置
type InlineSpec struct {
    // Handler 名称 (对应 ComponentFactory 注册的 handler)
    Handler string `json:"handler"`
    
    // Handler 版本
    Version string `json:"version"`
}

// ComponentType 定义组件类型
type ComponentType string

const (
    ComponentTypeYAML   ComponentType = "yaml"
    ComponentTypeHelm   ComponentType = "helm"
    ComponentTypeInline ComponentType = "inline"
    ComponentTypeBinary ComponentType = "binary"
)

// CompatibilitySpec 定义兼容性约束
type CompatibilitySpec struct {
    // 约束列表
    Constraints []Constraint `json:"constraints,omitempty"`
}

// Constraint 定义单个兼容性约束
type Constraint struct {
    // 依赖组件名称
    Component string `json:"component"`
    
    // 版本规则 (semver range, 如 ">=1.26.0")
    Rule string `json:"rule"`
}

// Dependency 定义组件间依赖关系
type Dependency struct {
    // 依赖组件名称
    Name string `json:"name"`
    
    // 依赖阶段 (Install / Upgrade)
    Phase string `json:"phase,omitempty"`
}

// UpgradeStrategySpec 定义升级策略
type UpgradeStrategySpec struct {
    // 升级模式: Rolling / Parallel / Batch
    Mode string `json:"mode,omitempty"`
    
    // 批量大小 (Batch 模式下每批节点数)
    BatchSize int `json:"batchSize,omitempty"`
    
    // 超时时间
    Timeout string `json:"timeout,omitempty"`
    
    // 失败策略: FailFast / Continue / Rollback
    FailurePolicy string `json:"failurePolicy,omitempty"`
}
```

### 3.3 CRD YAML 定义

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
    # v1alpha1: 旧版本, 保留向后兼容, 仅可读 (storage=false)
    # 不含 binary/helm/yaml 字段定义
    - name: v1alpha1
      served: true
      storage: false
      schema:
        openAPIV3Schema:
          type: object
          properties:
            apiVersion:
              type: string
            kind:
              type: string
            metadata:
              type: object
            spec:
              type: object
              properties:
                name:
                  type: string
                type:
                  type: string
                version:
                  type: string
                inline:
                  type: object
                  properties:
                    handler:
                      type: string
                    version:
                      type: string
                  required: [handler, version]
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
                        required: [component, rule]
                dependencies:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      phase:
                        type: string
                    required: [name]
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
              required: [name, type, version]
            status:
              type: object
              properties:
                phase:
                  type: string
                conditions:
                  type: array
                  items:
                    type: object
                    properties:
                      type:
                        type: string
                      status:
                        type: string
                        enum: ["True", "False", Unknown]
                      lastTransitionTime:
                        type: string
                        format: date-time
                      reason:
                        type: string
                      message:
                        type: string
                    required: [type, status, lastTransitionTime, reason, message]
      subresources:
        status: {}
    # v1alpha2: 新版本, 新增 binary/helm/yaml 字段定义 (storage=true)
    # v1alpha1 ↔ v1alpha2 通过 conversion 函数自动转换 (新字段全为 omitempty, 无数据丢失)
    - name: v1alpha2
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            apiVersion:
              type: string
            kind:
              type: string
            metadata:
              type: object
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
                            required: [name, namespace, key]
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
                              namespace:
                                type: string
                              serviceAccount:
                                type: string
                            required: [clusterName, apiServer, caCertPath, clientCertPath, clientKeyPath]
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
                        required: [name, versions]
                    defaultConfigPath:
                      type: string
                    defaultLogPath:
                      type: string
                    defaultDataPath:
                      type: string
                    healthCheck:
                      type: object
                      properties:
                        enabled:
                          type: boolean
                        timeout:
                          type: string
                        interval:
                          type: string
                        script:
                          type: string
                      required: [enabled, script]
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
                          required: [repository, tag]
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
                    valuesFiles:
                      type: array
                      items:
                        type: string
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
                            required: [type]
                    rollback:
                      type: object
                      properties:
                        enabled:
                          type: boolean
                        maxHistory:
                          type: integer
                    uninstall:
                      type: object
                      properties:
                        preUninstallHooks:
                          type: array
                          items:
                            type: object
                            properties:
                              name:
                                type: string
                              type:
                                type: string
                              manifest:
                                type: string
                            required: [type, manifest]
                    preInstallHooks:
                      type: array
                      items:
                        type: object
                        properties:
                          name:
                            type: string
                          type:
                            type: string
                          manifest:
                            type: string
                        required: [type, manifest]
                    preUninstallHooks:
                      type: array
                      items:
                        type: object
                        properties:
                          name:
                            type: string
                          type:
                            type: string
                          manifest:
                            type: string
                        required: [type, manifest]
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
                inline:
                  type: object
                  properties:
                    handler:
                      type: string
                    version:
                      type: string
                  required: [handler, version]
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
                        required: [component, rule]
                dependencies:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      phase:
                        type: string
                    required: [name]
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
            status:
              type: object
              properties:
                phase:
                  type: string
                conditions:
                  type: array
                  items:
                    type: object
                    properties:
                      type:
                        type: string
                      status:
                        type: string
                        enum: ["True", "False", Unknown]
                      lastTransitionTime:
                        type: string
                        format: date-time
                      reason:
                        type: string
                      message:
                        type: string
                    required: [type, status, lastTransitionTime, reason, message]
      subresources:
        status: {}
```

---

### 3.4 CRD 版本迁移设计

**设计思路**：v1alpha1 仅含 inline/subComponents/resources/compatibility/dependencies/upgradeStrategy 字段。v1alpha2 新增 binary/helm/yaml 字段定义。所有新字段均为 omitempty，v1alpha1 数据可无损转换为 v1alpha2。

**版本并存策略**：

```
阶段 1: 双版本并存 (当前)
┌───────────────────────────────────────────────────┐
│ v1alpha1: served=true,  storage=false  (只读兼容) │
│ v1alpha2: served=true,  storage=true   (新存储版本) │
└───────────────────────────────────────────────────┘
转换: v1alpha1 → v1alpha2 (自动, conversion 函数)
      v1alpha2 → v1alpha1 (自动, 新字段丢弃, 旧字段保留)

阶段 2: 旧版本废弃 (v1alpha2 稳定后)
┌───────────────────────────────────────────────────┐
│ v1alpha1: served=false, storage=false  (不再暴露) │
│ v1alpha2: served=true,  storage=true             │
└───────────────────────────────────────────────────┘

阶段 3: 移除旧版本
┌───────────────────────────────────────────────────┐
│ v1alpha1: 删除                                     │
│ v1alpha2: served=true,  storage=true             │
└───────────────────────────────────────────────────┘
```

**Conversion 函数设计**：

```go
// api/v1alpha2/conversion.go

// v1alpha2.ComponentVersion 实现 conversion.Hub 接口
func (cv *ComponentVersion) Hub() {}

// ConvertTo 将 v1alpha1 转换为 v1alpha2 (Hub)
// 自动转换: v1alpha1 的所有字段在 v1alpha2 中都有对应
// v1alpha2 新增的 binary/helm/yaml 字段为空 (omitempty, 无影响)
func (src *v1alpha1.ComponentVersion) ConvertTo(dstRaw conversion.Hub) error {
    dst := dstRaw.(*v1alpha2.ComponentVersion)
    dst.ObjectMeta = src.ObjectMeta
    dst.Spec.Name = src.Spec.Name
    dst.Spec.Type = v1alpha2.ComponentType(src.Spec.Type)
    dst.Spec.Version = src.Spec.Version
    dst.Spec.Inline = (*v1alpha2.InlineSpec)(src.Spec.Inline)
    dst.Spec.SubComponents = src.Spec.SubComponents
    dst.Spec.Compatibility = src.Spec.Compatibility
    dst.Spec.Dependencies = src.Spec.Dependencies
    dst.Spec.UpgradeStrategy = src.Spec.UpgradeStrategy
    dst.Spec.Resources = src.Spec.Resources
    // binary/helm/yaml 在 v1alpha1 中不存在, 留空
    return nil
}

// ConvertFrom 将 v1alpha2 转换为 v1alpha1
// 降级转换: v1alpha2 的 binary/helm/yaml 字段被丢弃
func (dst *v1alpha1.ComponentVersion) ConvertFrom(srcRaw conversion.Hub) error {
    src := srcRaw.(*v1alpha2.ComponentVersion)
    dst.ObjectMeta = src.ObjectMeta
    dst.Spec.Name = src.Spec.Name
    dst.Spec.Type = v1alpha1.ComponentType(src.Spec.Type)
    dst.Spec.Version = src.Spec.Version
    dst.Spec.Inline = (*v1alpha1.InlineSpec)(src.Spec.Inline)
    dst.Spec.SubComponents = src.Spec.SubComponents
    dst.Spec.Compatibility = src.Spec.Compatibility
    dst.Spec.Dependencies = src.Spec.Dependencies
    dst.Spec.UpgradeStrategy = src.Spec.UpgradeStrategy
    dst.Spec.Resources = src.Spec.Resources
    // binary/helm/yaml 字段在 v1alpha1 中不存在, 丢弃
    return nil
}
```

**迁移步骤**：

| 步骤 | 操作 | 风险 | 回滚方案 |
|------|------|------|---------|
| 1 | 创建 v1alpha2 API (复制 v1alpha1 类型 + 新增 BinarySpec/HelmSpec/YAMLSpec) | 无 | 删除 v1alpha2 目录 |
| 2 | 实现 conversion 函数 (v1alpha1 ↔ v1alpha2) + 单元测试 | 低 | 删除 conversion |
| 3 | CRD 配置: v1alpha1 served=true storage=false, v1alpha2 served=true storage=true | 中 | 恢复 v1alpha1 storage=true |
| 4 | 控制器切换到 v1alpha2 API | 中 | 切回 v1alpha1 client |
| 5 | 观察 1-2 周, 确认 conversion 无异常 | — | — |
| 6 | v1alpha1 served=false | 低 | served=true |
| 7 | 删除 v1alpha1 | 高 | 从 Git 历史恢复 |

**注意事项**：
- v1alpha1 的 `type` 字段已支持 `yaml/helm/inline/binary` 四种值（仅 enum 约束，无 schema 定义），v1alpha2 新增了对应的 schema 约束
- `Resources` 字段在 v1alpha1 和 v1alpha2 中均位于顶层，conversion 无需特殊处理
- `SubComponents` 字段同理，两个版本中位置一致

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

**设计思路**：BinaryInstaller 的执行流程分为 7 个主要步骤：解析架构、下载制品、校验 Checksum、渲染脚本、渲染配置、SSH 执行、收集结果。整个流程采用"下载-校验-渲染-执行"的管道模式，确保制品安全性和配置正确性。

**关键设计点**：
- **缓存机制**：制品下载后保存到本地缓存，避免重复下载
- **Checksum 校验**：下载后立即校验，确保制品完整性
- **模板渲染**：支持 installScript 和 configTemplates 两种模板
- **SSH 执行**：复用现有 bkessh.MultiCli，上传制品和配置后执行脚本

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
                     │  1. 通过 SSH 发现节点架构            │
                     │  arch = sshDiscoverArch(node.IP)     │
                     │  (执行 uname -m, 返回 amd64/arm64)   │
                     │                                      │
                     │  注意: OS 不在此处获取,              │
                     │  由 installScript 运行时自检测       │
                     │  (通过 /etc/os-release)              │
                     └────────────────────┬─────────────────┘
                                          │
                                          ▼
                     ┌──────────────────────────────────────┐
                     │  2. 下载二进制制品                    │
                     │  downloadArtifacts(ctx, binary,      │
                     │                    arch)             │
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

**设计思路**：BinaryInstaller 的接口设计复用现有的 `manifest.TemplateContext`，避免重复传递集群和节点信息。DAG 调度器构建的 TemplateContext 直接传递给 BinaryInstaller，BinaryInstaller 在此基础上填充制品信息。

```go
// pkg/binaryinstaller/installer.go

// BinaryInstaller 二进制组件安装器
type BinaryInstaller struct {
    client     client.Client
    sshClient  *bkessh.MultiCli
    cacheDir   string
    httpClient *http.Client
    cache      *ArtifactCache
    renderer   *TemplateRenderer  // 复用 TemplateRenderer
}
```

**设计思路 — Install 与 Upgrade 共用 InstallScript**：

`BinaryAction` 有三个值（Install/Upgrade/Uninstall），但 `BinarySpec` 只有 `InstallScript` 和 `UninstallScript` 两个脚本，没有 `UpgradeScript`。这是有意的：

1. **Install 和 Upgrade 本质是同一操作**——"让目标版本成为运行版本"，区别仅在于是否有旧版本存在。大部分步骤相同（下载→解压→配置→启动），仅少数步骤不同（备份旧版本、停止旧服务）。
2. **通过 `{{if .isUpgrade}}` 条件渲染区分差异**，以 containerd 为例：备份步骤仅在升级时执行，其余步骤完全一致。避免 90% 代码重复。
3. **不设 UpgradeScript 的原因**：① 避免用户编写维护两份高度重复的脚本；② 防止 Install 和 Upgrade 行为漂移（一份脚本改了另一份忘改）；③ 与 Helm Chart 模板惯例一致（`helm install` 和 `helm upgrade` 渲染同一套模板）。
4. **Uninstall 独立 UninstallScript 的原因**：卸载与安装是逆向操作（停止服务→删除二进制→清理配置→清理数据），逻辑完全不同，无法共用。

**`isUpgrade` 的来源链路**：

```
VersionContext.HasCurrent("containerd")    // 版本事实: 组件是否已安装
  → true: BinaryActionUpgrade              // Executor 自主决定: 已安装 → 升级
  → false: BinaryActionInstall             // Executor 自主决定: 未安装 → 安装
    → InstallOptions.Action 传递给 BinaryInstaller.Install()
      → tmplCtx.IsUpgrade = (Action == Upgrade)   // 填入 TemplateContext
        → 模板渲染: {{if .isUpgrade}}...{{end}}    // installScript 中条件渲染
```

`isUpgrade` 不是用户配置，而是由 `VersionContext` 在运行时推断：`HasCurrent` 返回 true（组件已安装）→ `BinaryActionUpgrade` → `tmplCtx.IsUpgrade = true` → 模板中 `{{if .isUpgrade}}` 生效。

```go
// InstallOptions 安装选项
type InstallOptions struct {
    Component   *ComponentVersion
    TemplateCtx manifest.TemplateContext  // 复用现有 TemplateContext
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
    // 注意: opts.TemplateCtx 是扩展后的 TemplateContext (见 6.3 节)
    // 当前代码中的 manifest.TemplateContext 仅含 ClusterName/Namespace/KubernetesVersion/OpenFuyaoVersion
    // KEP-6 设计中需扩展为包含 NodeIP/NodeArch/Artifacts/Variables/ConfigPath 等字段
    tmplCtx := opts.TemplateCtx  // 复用 DAG 调度器传递的 TemplateContext (扩展后)
    
    // 1. 通过 SSH 发现节点架构 (必需: 制品 URL 包含 {{arch}} 模板变量, 下载前必须解析)
    // 与当前 bkeagent 升级代码 (agentssh.DiscoverArchs) 一致: SSH 执行 uname -m 获取架构
    arch, err := i.sshDiscoverArch(ctx, tmplCtx.NodeIP)
    if err != nil {
        return fmt.Errorf("failed to discover arch for node %s: %w", tmplCtx.NodeIP, err)
    }
    tmplCtx.NodeArch = arch // 填入 TemplateContext, 供 URL 模板解析和脚本引用
    
    // 设计说明: OS 不在此处获取
    // OS 信息由安装脚本在运行时自检测 (通过 /etc/os-release), 不影响制品下载
    // 仅 installScript 中少数场景需要 OS (如包管理器选择 yum/apt), 脚本内 if-else 自检测即可
    
    // 2. 下载二进制制品 (带缓存, 使用 arch 解析 URL 中的 {{arch}} 占位符)
    artifacts, err := i.downloadArtifacts(ctx, binary, arch)
    if err != nil {
        return fmt.Errorf("failed to download artifacts: %w", err)
    }
    
    // 3. 填充 TemplateContext 的制品信息 (含 per-artifact InstallPath)
    tmplCtx.Artifacts = make(map[string]*ArtifactInfo)
    for name, art := range artifacts {
        tmplCtx.Artifacts[name] = &ArtifactInfo{
            Name:        art.Name,
            Path:        art.Path,
            URL:         art.URL,
            Checksum:    art.Checksum,
            Filename:    art.Filename,
            InstallPath: art.InstallPath,  // per-artifact 安装路径, 供 {{artifact.<name>.installPath}} 引用
        }
    }
    
    // 4. 填充自定义变量
    tmplCtx.Variables = binary.Variables
    
    // 5. 填充组件级路径变量 (从 BinarySpec.Default*Path → TemplateContext)
    // 注意: installPath/binPath 已移至 per-artifact 级别 (ArtifactInfo.InstallPath)
    // 因为不同 artifact 可能安装到不同路径
    tmplCtx.ConfigPath = binary.DefaultConfigPath
    tmplCtx.LogPath = binary.DefaultLogPath
    tmplCtx.DataPath = binary.DefaultDataPath
    
    // 6. 填充操作类型 (从 InstallOptions.Action → TemplateContext, 供模板 {{isUpgrade}} 引用)
    tmplCtx.Action = string(opts.Action)
    tmplCtx.IsUpgrade = opts.Action == BinaryActionUpgrade
    
    // 7. 渲染安装脚本 (使用完整的 TemplateContext)
    script, err := i.renderer.RenderScript(binary.InstallScript, tmplCtx)
    if err != nil {
        return fmt.Errorf("failed to render install script: %w", err)
    }
    
    // 8. 渲染配置文件模板
    configs, err := i.renderConfigTemplates(binary.ConfigTemplates, tmplCtx)
    if err != nil {
        return fmt.Errorf("failed to render config templates: %w", err)
    }
    
    // 9. SSH 执行安装
    switch opts.Action {
    case BinaryActionInstall, BinaryActionUpgrade:
        if err := i.executeInstall(ctx, tmplCtx.NodeIP, script, artifacts, configs); err != nil {
            return err
        }
        // 10. 健康检查 (安装/升级后验证服务可用性)
        if binary.HealthCheck != nil && binary.HealthCheck.Enabled {
            if err := i.executeHealthCheck(ctx, tmplCtx.NodeIP, binary.HealthCheck, tmplCtx); err != nil {
                return fmt.Errorf("health check failed on %s: %w", tmplCtx.NodeIP, err)
            }
        }
        return nil
    case BinaryActionUninstall:
        return i.executeUninstall(ctx, tmplCtx.NodeIP, binary.UninstallScript, tmplCtx)
    }
    
    return nil
}

// executeHealthCheck 通过 SSH 执行健康检查脚本, 重试直到超时或通过
func (i *BinaryInstaller) executeHealthCheck(
    ctx context.Context,
    nodeIP string,
    hc *BinaryHealthCheckSpec,
    tmplCtx *manifest.TemplateContext,
) error {
    // 渲染健康检查脚本
    script, err := i.renderer.RenderScript(hc.Script, tmplCtx)
    if err != nil {
        return fmt.Errorf("failed to render health check script: %w", err)
    }

    timeout := parseDurationDefault(hc.Timeout, 2*time.Minute)
    interval := parseDurationDefault(hc.Interval, 5*time.Second)
    deadline := time.Now().Add(timeout)

    for time.Now().Before(deadline) {
        result, err := i.sshClient.Execute(nodeIP, script)
        if err == nil {
            return nil // 退出码 0 = 健康
        }
        i.logger.Warn("health check retry on %s: %v (stdout: %s, stderr: %s)",
            nodeIP, err, result.Stdout, result.Stderr)
        time.Sleep(interval)
    }

    return fmt.Errorf("health check timed out after %s on %s", timeout, nodeIP)
}

// downloadArtifacts 下载二进制制品 (使用 arch 解析 URL 中的 {{arch}} 占位符)
func (i *BinaryInstaller) downloadArtifacts(ctx context.Context, binary *BinarySpec, arch string) (map[string]*Artifact, error) {
    artifacts := make(map[string]*Artifact)
    
    for _, art := range binary.Artifacts {
        // 解析 URL 模板变量 (arch 已通过 SSH 发现, version 从 BinarySpec 获取)
        url, err := i.resolveTemplate(art.URL, map[string]string{
            "{{componentVersion}}": binary.Version,
            "{{version}}":          binary.Version,
            "{{arch}}":             arch,
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

**设计思路**：HelmInstaller 的执行流程分为 7 个主要步骤：获取 Chart、校验 Checksum、渲染 Values、加载自定义 Values、合并 Values、执行 Helm Action、健康检查。整个流程支持 OCI/HTTP/本地三种 Chart 来源，并提供原子操作和自动回滚能力。

**关键设计点**：
- **多来源支持**：OCI Registry、HTTP URL、本地路径三种 Chart 获取方式
- **Values 渲染**：支持模板变量替换，可动态生成配置
- **原子操作**：通过 `atomic: true` 配置，失败时自动回滚
- **健康检查**：安装/升级后执行 PodReady/EndpointReady 检查

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

**设计思路**：HelmInstaller 的接口设计也复用现有的 `manifest.TemplateContext`，与 BinaryInstaller 保持一致。DAG 调度器构建的 TemplateContext 直接传递给 HelmInstaller，用于渲染 Helm Values。

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
    TemplateCtx manifest.TemplateContext  // 复用现有 TemplateContext
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
    tmplCtx := opts.TemplateCtx  // 复用 DAG 调度器传递的 TemplateContext
    
    // 1. 获取 Chart
    chart, err := i.getChart(ctx, helm.Chart)
    if err != nil {
        return fmt.Errorf("failed to get chart: %w", err)
    }
    
    // 2. 渲染 Values (使用 TemplateContext)
    values, err := i.renderValues(ctx, helm.Values, tmplCtx)
    if err != nil {
        return fmt.Errorf("failed to render values: %w", err)
    }
    
    // 3. 加载自定义 Values 文件
    for _, vf := range helm.ValuesFiles {
        customValues, err := i.loadValuesFile(ctx, vf, tmplCtx)
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

## 6. TemplateRenderer 详细设计

### 6.0 与现有 TemplateContext 的复用关系

**设计思路**：为避免重复造轮子，TemplateRenderer 复用并扩展现有的 `pkg/manifest.TemplateContext` 结构体。现有 TemplateContext 用于 YAML/Manifest 组件的模板渲染，包含 4 个基础字段。TemplateRenderer 在此基础上扩展，增加 Binary 组件所需的节点信息、制品信息、自定义变量等字段。

**复用策略**：
- **向后兼容**：现有 TemplateContext 的 4 个字段保持不变，YAML 组件代码无需修改
- **扩展字段**：新增字段均为可选，YAML 组件不使用时留空即可
- **统一接口**：BinaryInstaller 和 HelmInstaller 共享同一个 TemplateContext，简化 DAG 调度器的数据传递

**设计原则：Arch 通过 SSH 发现，OS 脚本自检测**：
- **Arch 必须在下载前通过 SSH 发现**：制品 URL 包含 `{{arch}}` 模板变量（如 `containerd-{{version}}-linux-{{arch}}.tar.gz`），下载前必须解析为实际架构。BinaryInstaller 在 `Install()` 中通过 SSH 执行 `uname -m` 获取架构（与当前 bkeagent 升级代码 `agentssh.DiscoverArchs` 一致）。
- **OS 由安装脚本运行时自检测**：二进制安装脚本本身是 OS 无关的（systemctl、tar、chmod 等命令在所有主流 Linux 通用），只有极少数场景需要 OS 检测（如包管理器安装依赖）。将 OS 检测逻辑放在脚本内部自检测，而非模板渲染时注入，有以下好处：
  1. **简化 Node 结构体**：NodeProvider 只需提供 IP/Hostname/Role 等基础信息，无需 SSH 预检测 OS
  2. **脚本可移植**：安装脚本可在任意节点上独立运行，不依赖外部注入的 OS 信息
  3. **减少模板变量**：避免模板膨胀，保持模板简洁
  4. **运行时灵活性**：脚本可根据实际环境动态调整行为，而非依赖静态配置

**现有 TemplateContext** (`pkg/manifest/types.go`)：
```go
type TemplateContext struct {
    ClusterName       string  // 已有：集群名称
    Namespace         string  // 已有：命名空间
    KubernetesVersion string  // 已有：K8s 版本
    OpenFuyaoVersion  string  // 已有：OpenFuyao 版本
}
```

**扩展后的 TemplateContext**：
```go
type TemplateContext struct {
    // 现有字段 (保持向后兼容)
    ClusterName       string
    Namespace         string
    KubernetesVersion string
    OpenFuyaoVersion  string
    
    // 新增：集群扩展信息 (Binary 组件需要)
    APIServer         string
    ServiceCIDR       string
    PodCIDR           string
    DNSDomain         string
    
    // 新增：节点基础信息 (Binary 组件需要)
    // 注意：不包含 OS/OSVersion (由安装脚本自检测)
    // NodeArch 在 Install() 中通过 SSH 发现后填入
    NodeIP            string
    NodeHostname      string
    NodeRole          string
    NodeArch          string  // SSH 发现后填入 (uname -m), 用于 URL 模板解析 {{arch}}
    
    // 新增：版本信息
    ComponentVersion          string
    ComponentPreviousVersion  string
    
    // 新增：制品信息 (Binary 组件需要)
    Artifacts         map[string]*ArtifactInfo
    
    // 新增：镜像仓库
    ImageRegistry     string
    ImagePullSecret   string
    
    // 新增：组件级路径 (从 BinarySpec.Default*Path 填充, 全部 artifact 共享)
    // 注意: installPath/binPath 已移至 per-artifact 级别 (ArtifactInfo.InstallPath)
    // 因为不同 artifact 可能安装到不同路径 (如 containerd→/usr/local, runc→/usr/local/sbin)
    ConfigPath        string
    LogPath           string
    DataPath          string
    
    // 新增：操作类型 (从 InstallOptions.Action 填充, 供模板 {{isUpgrade}} 等引用)
    Action            string  // "Install" / "Upgrade" / "Uninstall"
    IsUpgrade         bool    // Action == "Upgrade" 时为 true
    
    // 新增：自定义变量
    Variables         map[string]string
}

type ArtifactInfo struct {
    Name        string
    Path        string    // 本地缓存路径 (staging, 上传到远程临时目录)
    URL         string
    Checksum    string
    Filename    string
    InstallPath string    // 远程节点上的最终安装路径 (per-artifact, 通过 {{artifact.<name>.installPath}} 引用)
}
```

**使用场景对比**：

| 组件类型 | 使用的字段 | 说明 |
|---------|-----------|------|
| **YAML/Manifest** | ClusterName, Namespace, KubernetesVersion, OpenFuyaoVersion | 现有字段，向后兼容 |
| **Helm** | 同上 + APIServer, ServiceCIDR 等 | 可选使用扩展字段 |
| **Binary** | 所有字段 | 完整使用，包括节点基础信息、制品信息等 |

### 6.1 模板变量系统

TemplateRenderer 支持 8 类 50+ 模板变量，覆盖集群、节点、版本、制品、镜像仓库、路径、操作类型和自定义变量。

**变量与 TemplateContext 字段映射**：

#### 1. 集群信息变量 (Cluster Variables)

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{clusterName}}` | 集群名称 | `.ClusterName` | `my-cluster` |
| `{{clusterNamespace}}` | 集群命名空间 | `.Namespace` | `default` |
| `{{apiServer}}` | API Server 地址 | `.APIServer` | `https://10.0.0.1:6443` |
| `{{apiServerPort}}` | API Server 端口 | 从 APIServer 解析 | `6443` |
| `{{serviceCIDR}}` | Service CIDR | `.ServiceCIDR` | `10.96.0.0/12` |
| `{{podCIDR}}` | Pod CIDR | `.PodCIDR` | `192.168.0.0/16` |
| `{{dnsDomain}}` | DNS 域名 | `.DNSDomain` | `cluster.local` |
| `{{clusterDNS}}` | Cluster DNS IP | 从 ServiceCIDR 计算 | `10.96.0.10` |

#### 2. 节点信息变量 (Node Variables)

**设计说明**：节点变量仅包含基础信息（IP、Hostname、Role），不包含 Arch/OS/OSVersion。架构和操作系统信息由安装脚本在运行时自检测（通过 `uname -m` 和 `/etc/os-release`），而非模板渲染时注入。这样做的好处是：
1. 简化 NodeProvider，无需 SSH 预检测
2. 安装脚本可在任意节点上独立运行
3. 减少模板变量，保持模板简洁

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{nodeIP}}` | 节点 IP | `.NodeIP` | `192.168.1.10` |
| `{{nodeHostname}}` | 节点主机名 | `.NodeHostname` | `node-01` |
| `{{nodeRole}}` | 节点角色 | `.NodeRole` | `master` / `worker` / `etcd` |

**脚本内自检测示例**（在 installScript 中使用，非模板变量）：
```bash
# 架构检测 (所有 Linux 通用)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

# 操作系统检测 (所有主流 Linux 通用)
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$ID
    OS_VERSION=$VERSION_ID
fi
```

#### 3. 版本信息变量 (Version Variables)

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{componentVersion}}` | 当前组件版本 | `.ComponentVersion` | `v1.7.18` |
| `{{componentPreviousVersion}}` | 上一组件版本 | `.ComponentPreviousVersion` | `v1.7.15` |
| `{{clusterVersion}}` | 集群 Kubernetes 版本 | `.KubernetesVersion` | `v1.29.0` |
| `{{openFuyaoVersion}}` | OpenFuyao 版本 | `.OpenFuyaoVersion` | `v2.6.0` |
| `{{etcdVersion}}` | Etcd 版本 | 从 VersionContext 获取 | `v3.5.12` |
| `{{containerdVersion}}` | Containerd 版本 | 从 VersionContext 获取 | `v1.7.18` |
| `{{bkeagentVersion}}` | BKEAgent 版本 | 从 VersionContext 获取 | `v2.6.0` |

#### 4. 二进制制品变量 (Artifact Variables)

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{artifact.<name>.path}}` | 制品本地路径 | `.Artifacts[name].Path` | `/tmp/bke-install/containerd.tar.gz` |
| `{{artifact.<name>.url}}` | 制品原始 URL | `.Artifacts[name].URL` | `https://release-repo.../containerd.tar.gz` |
| `{{artifact.<name>.checksum}}` | 制品校验和 | `.Artifacts[name].Checksum` | `sha256:abc123...` |
| `{{artifact.<name>.filename}}` | 制品文件名 | `.Artifacts[name].Filename` | `containerd-1.7.18-linux-amd64.tar.gz` |

#### 5. 镜像仓库变量 (Registry Variables)

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{imageRegistry}}` | 镜像仓库地址 | `.ImageRegistry` | `registry.openfuyao.cn` |
| `{{imagePullSecret}}` | 镜像拉取 Secret | `.ImagePullSecret` | `registry-secret` |
| `{{imageNamespace}}` | 镜像命名空间 | 从 ImageRegistry 解析 | `openfuyao` |

#### 6. 安装路径变量 (Path Variables)

| 变量 | 说明 | 来源 | 示例值 |
|------|------|------|--------|
| `{{artifact.<name>.installPath}}` | per-artifact 安装路径 | `ArtifactInfo.InstallPath` (从 `ArtifactSpec.installPath` 填充) | `/usr/local` |
| `{{configPath}}` | 配置路径 (组件级) | `TemplateContext.ConfigPath` (从 `BinarySpec.defaultConfigPath` 填充) | `/etc/containerd` |
| `{{logPath}}` | 日志路径 (组件级) | `TemplateContext.LogPath` (从 `BinarySpec.defaultLogPath` 填充) | `/var/log/containerd` |
| `{{dataPath}}` | 数据路径 (组件级) | `TemplateContext.DataPath` (从 `BinarySpec.defaultDataPath` 填充) | `/var/lib/containerd` |

**设计思路 — per-artifact installPath vs 组件级路径**：
- **`{{artifact.<name>.installPath}}`** 是 per-artifact 级别，不同 artifact 可安装到不同路径。例如 containerd tar.gz 解压到 `/usr/local`（含 bin/ 目录），runc 单独二进制放到 `/usr/local/sbin`。
- **`{{configPath}}`/`{{logPath}}`/`{{dataPath}}`** 是组件级共享，一个组件的所有 artifact 共用同一套配置/日志/数据路径。
- 已移除 `{{installPath}}` 和 `{{binPath}}`（组件级单一值无法满足多 artifact 不同路径的需求）。

#### 7. 操作类型变量 (Action Variables)

| 变量 | 说明 | 来源 | 示例值 |
|------|------|------|--------|
| `{{action}}` | 操作类型 | `TemplateContext.Action`（从 `InstallOptions.Action` 填充） | `Install` / `Upgrade` / `Uninstall` |
| `{{isUpgrade}}` | 是否升级 | `TemplateContext.IsUpgrade`（`Action == Upgrade` 时为 true） | `true` / `false` |
| `{{isInstall}}` | 是否安装 | `TemplateContext.Action == Install` | `true` / `false` |

**`isUpgrade` 的来源链路**：`VersionContext.HasCurrent()` → `BinaryActionUpgrade` → `InstallOptions.Action` → `tmplCtx.IsUpgrade` → 模板 `{{if .isUpgrade}}`

#### 8. 自定义变量 (Custom Variables)

通过 ComponentVersion 的 `binary.variables` 字段定义，可在 installScript 中引用：

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{.Variables.<key>}}` | 自定义变量 | `.Variables[key]` | - |
| `{{.Variables.logLevel}}` | 日志级别 | `.Variables["logLevel"]` | `info` |
| `{{.Variables.snapshotter}}` | 快照器类型 | `.Variables["snapshotter"]` | `overlayfs` |

**使用示例**：
```yaml
# ComponentVersion 中定义自定义变量
binary:
  variables:
    logLevel: "info"
    maxConcurrentDownloads: 10
    snapshotter: "overlayfs"

# installScript 中引用
installScript: |
  echo "Setting log level to {{.Variables.logLevel}}"
  echo "Setting snapshotter to {{.Variables.snapshotter}}"
```

### 6.2 TemplateRenderer 渲染流程图

**设计思路**：TemplateRenderer 的渲染流程分为 5 个主要步骤：接收 TemplateContext、构建制品数据、创建模板解析器、执行模板渲染、返回结果。整个流程复用 DAG 调度器传递的 TemplateContext，避免重复构建模板数据。

**关键设计点**：
- **复用 TemplateContext**：直接使用 DAG 调度器构建的 TemplateContext，包含集群、节点、版本等信息
- **制品数据构建**：根据 ComponentVersion 的 artifacts 定义，下载并构建制品信息
- **自定义函数**：提供 upper/lower/eq/ne/default/joinPath 等常用函数
- **脚本自检测**：架构和操作系统信息由安装脚本在运行时自检测，模板不感知
- **错误处理**：模板解析和执行失败时返回详细错误信息

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
                    │  1. 接收 TemplateContext             │
                    │  (由 DAG Scheduler 构建并传递)       │
                    │                                      │
                    │  TemplateContext 包含:               │
                    │  - ClusterName, Namespace            │
                    │  - KubernetesVersion, OpenFuyaoVer   │
                    │  - NodeIP, NodeHostname, NodeRole    │
                    │  - ComponentVersion                  │
                    │  - Artifacts (待填充)                │
                    │  - Variables                         │
                    │                                      │
                    │  注意: NodeArch 由 Install() SSH   │
                    │  发现后填入; NodeOS/NodeOSVersion  │
                    │  不包含, 由安装脚本自检测           │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                     │  2. 构建制品数据                     │
                     │  downloadArtifacts(ctx, binary,      │
                     │                    arch)             │
                    │                                      │
                    │  填充 TemplateContext.Artifacts:     │
                    │  - containerd: {path, url, checksum} │
                    │  - ctr: {path, url, checksum}        │
                    │  - shim: {path, url, checksum}       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 创建模板解析器                   │
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
                    │  4. 执行模板渲染                     │
                    │  tmpl.Execute(&buf, tmplCtx)         │
                    │                                      │
                    │  模板变量替换:                       │
                    │  - {{clusterName}} → tmplCtx.ClusterName
                    │  - {{nodeIP}} → tmplCtx.NodeIP       │
                    │  - {{artifact.containerd.path}} →    │
                    │    tmplCtx.Artifacts["containerd"].Path
                    │  - {{.Variables.logLevel}} →         │
                    │    tmplCtx.Variables["logLevel"]     │
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

### 6.3 TemplateContext 构建流程

**设计思路**：DAG 调度器在执行组件前构建 TemplateContext，包含集群信息、节点基础信息、版本信息等。对于 Binary 组件，还需要在 TemplateRenderer 中填充制品信息。注意：TemplateContext 不包含 NodeOS/NodeOSVersion（由安装脚本自检测），NodeArch 由 BinaryInstaller.Install() 通过 SSH 发现后填入（制品 URL 中的 `{{arch}}` 需要在下载前解析）。

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        TemplateContext 构建流程                                  │
└─────────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────────────────────────────┐
                    │  DAG Scheduler.ExecuteDAG()          │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  构建基础 TemplateContext            │
                    │                                      │
                    │  tmpl := manifest.TemplateContext{   │
                    │    ClusterName:       cluster.Name,  │
                    │    Namespace:         cluster.NS,    │
                    │    KubernetesVersion: cluster.K8sVer,│
                    │    OpenFuyaoVersion:  cluster.OFVer, │
                    │    APIServer:         cluster.API,   │
                    │    ServiceCIDR:       cluster.SvcCIDR│
                    │  }                                   │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  遍历执行批次                        │
                    │  for _, batch := range batches       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  对每个组件:                         │
                    │  executeComponent(ctx, node, tmpl)   │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  YAML 组件      │  │  Helm 组件      │  │  Binary 组件    │
          │                 │  │                 │  │                 │
           │  使用基础字段   │ │  使用基础字段   │ │  扩展节点信息   │
           │  - ClusterName  │ │  + APIServer    │ │  + NodeIP       │
           │  - Namespace    │ │  + ServiceCIDR  │ │  + NodeHostname │
           │  - K8sVersion   │ │                 │ │  + NodeRole     │
           │                 │ │                 │ │  + Artifacts    │
           │  渲染 Manifest  │ │  渲染 Values    │ │  + Variables    │
          │  应用到集群     │  │  helm install   │  │                 │
          │                 │  │                 │  │  渲染脚本       │
          │                 │  │                 │  │  SSH 执行       │
          └─────────────────┘  └─────────────────┘  └─────────────────┘
```

### 6.4 自定义函数定义

**设计思路**：TemplateRenderer 提供一组自定义函数，用于在模板中进行字符串处理、条件判断、版本比较等操作。这些函数通过 Go text/template 的 FuncMap 机制注册。

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

## 7. ConfigRenderer 详细设计

### 7.1 三种渲染模式

ConfigRenderer 支持三种渲染模式，根据不同的配置来源选择对应的渲染方式。

#### 模式 1: Content 模式 (Go template 渲染)

**设计思路**：直接在 ComponentVersion 中定义配置内容模板，通过 Go template 引擎渲染。适用于配置文件内容可以直接在 YAML 中定义的场景。

**输入**：
```yaml
configTemplates:
  - name: bkeagent.conf
    path: "/etc/openFuyao/bkeagent/bkeagent.conf"
    content: |
      cluster_name: {{.clusterName}}
      api_server: {{.apiServer}}
      log_level: {{.Variables.logLevel | default "info"}}
```

**渲染过程**：
1. 解析 content 模板
2. 注入模板数据 (集群信息、节点信息、自定义变量等)
3. 执行 Go template 渲染
4. 返回渲染后的内容

**输出**：
```yaml
cluster_name: my-cluster
api_server: https://10.0.0.1:6443
log_level: info
```

---

#### 模式 2: SecretRef 模式 (从 Secret 获取)

**设计思路**：从 Kubernetes Secret 中获取配置内容，适用于敏感信息（如证书、密钥）的管理。

**输入**：
```yaml
configTemplates:
  - name: tls.crt
    path: "/etc/openFuyao/bkeagent/tls.crt"
    secretRef:
      name: bkeagent-tls
      namespace: "{{.clusterNamespace}}"
      key: tls.crt
```

**渲染过程**：
1. 解析 namespace 模板变量
2. 从 Kubernetes API 获取 Secret
3. 提取指定 key 的内容
4. 返回 Secret 数据

**输出**：
```
-----BEGIN CERTIFICATE-----
MIIC...
-----END CERTIFICATE-----
```

---

#### 模式 3: KubeconfigTemplate 模式 (动态生成)

**设计思路**：根据集群信息动态生成 kubeconfig 文件，适用于需要为组件生成访问集群凭证的场景。

**输入**：
```yaml
configTemplates:
  - name: kubeconfig
    path: "/etc/openFuyao/bkeagent/kubeconfig"
    kubeconfigTemplate:
      clusterName: "{{.clusterName}}"
      apiServer: "{{.apiServer}}"
      caCertPath: "/etc/openFuyao/bkeagent/ca.crt"
      clientCertPath: "/etc/openFuyao/bkeagent/tls.crt"
      clientKeyPath: "/etc/openFuyao/bkeagent/tls.key"
      namespace: "{{.clusterNamespace}}"
```

**渲染过程**：
1. 解析模板变量
2. 构建 kubeconfig 结构
3. 序列化为 YAML 格式
4. 返回 kubeconfig 内容

**输出**：
```yaml
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://10.0.0.1:6443
    certificate-authority: /etc/openFuyao/bkeagent/ca.crt
  name: my-cluster
users:
- user:
    client-certificate: /etc/openFuyao/bkeagent/tls.crt
    client-key: /etc/openFuyao/bkeagent/tls.key
  name: my-cluster
contexts:
- context:
    cluster: my-cluster
    user: my-cluster
    namespace: default
  name: my-cluster
current-context: my-cluster
```

### 7.2 ConfigRenderer 渲染流程图

**设计思路**：ConfigRenderer 根据配置模板的类型选择不同的渲染模式：Content 模式使用 Go template 渲染、SecretRef 模式从 Kubernetes Secret 获取、KubeconfigTemplate 模式动态生成 kubeconfig。整个流程采用策略模式，根据模板类型分发到不同的渲染处理器。

**关键设计点**：
- **三种模式**：Content（模板渲染）、SecretRef（Secret 引用）、KubeconfigTemplate（动态生成）
- **模板变量**：Content 模式支持完整的模板变量系统
- **Secret 获取**：SecretRef 模式支持命名空间模板变量
- **Kubeconfig 生成**：使用 client-go 的 clientcmd 库生成标准格式

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

### 7.3 核心接口定义

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
func (r *ConfigRenderer) renderKubeconfigTemplate(ctx context.Context, template ConfigTemplateSpec, tmplCtx manifest.TemplateContext) ([]byte, error) {
    kc := template.KubeconfigTemplate
    
    // 解析模板变量 (使用 TemplateContext)
    clusterName := r.renderTemplateString(kc.ClusterName, tmplCtx)
    apiServer := r.renderTemplateString(kc.APIServer, tmplCtx)
    namespace := r.renderTemplateString(kc.Namespace, tmplCtx)
    
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

// renderConfigTemplates 渲染配置文件模板 (使用 TemplateContext)
func (r *ConfigRenderer) renderConfigTemplates(templates []ConfigTemplateSpec, tmplCtx manifest.TemplateContext) (map[string][]byte, error) {
    configs := make(map[string][]byte)
    
    for _, tmpl := range templates {
        var content []byte
        var err error
        
        switch {
        case tmpl.Content != "":
            // Content 模式：使用 TemplateContext 渲染
            content, err = r.renderContentTemplate(tmpl.Content, tmplCtx)
        case tmpl.SecretRef != nil:
            // SecretRef 模式：从 Secret 获取
            content, err = r.renderSecretTemplate(tmpl.SecretRef, tmplCtx)
        case tmpl.KubeconfigTemplate != nil:
            // KubeconfigTemplate 模式：动态生成
            content, err = r.renderKubeconfigTemplate(context.Background(), tmpl, tmplCtx)
        default:
            return nil, fmt.Errorf("no template content specified for %s", tmpl.Name)
        }
        
        if err != nil {
            return nil, fmt.Errorf("failed to render template %s: %w", tmpl.Name, err)
        }
        configs[tmpl.Name] = content
    }
    
    return configs, nil
}
```

**设计说明**：ConfigRenderer 的所有渲染方法都接收 `manifest.TemplateContext` 作为参数，而不是单独传递集群、节点等信息。这样：
1. 与 DAG 调度器传递的 TemplateContext 保持一致
2. 避免重复构建模板数据
3. BinaryInstaller 和 HelmInstaller 共享相同的模板上下文

---

## 8. DAG 集成详细设计

### 8.1 执行器注册

**设计思路**：DAG 调度器根据 ComponentVersion 的类型选择对应的执行器。系统支持四种组件类型：Binary（二进制）、Helm（Helm Chart）、Inline（内联代码）、YAML（清单文件）。每种类型对应一个专门的 Executor，负责该类型组件的完整生命周期管理。

**关键设计点**：
- **类型分发**：根据 `cv.Spec.Type` 选择对应的 Executor
- **执行器注册**：每个 Executor 实现 `ComponentExecutor` 接口
- **依赖注入**：Executor 通过构造函数注入所需的 Installer/Applier

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
              ┌──────────────┬───────────┼───────────┬──────────────┐
              │              │           │           │              │
              ▼              ▼           ▼           ▼              │
    ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
    │ ComponentType│ │ ComponentType│ │ ComponentType│ │ ComponentType│
    │ Binary       │ │ Helm         │ │ Inline       │ │ YAML         │
    └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
           │                │                │                │
           ▼                ▼                ▼                ▼
    ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
    │ BinaryCompo- │ │ HelmCompo-   │ │ InlineCompo- │ │ ManifestCom- │
    │ nentExecutor │ │ nentExecutor │ │ nentExecutor │ │ ponentExecutor│
    └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
           │                │                │                │
           │                │                │                ▼
           │                │                │       ┌──────────────┐
           │                │                │       │ Manifest     │
           │                │                │       │ Applier      │
           │                │                │       │ (K8s Client) │
           │                │                │       └──────┬───────┘
           │                │                │                │
           └────────────────┼────────────────┼────────────────┘
                            │                │
                            └────────────────┘
                             │
                             ▼
              ┌──────────────────────────────────────┐
              │  注册到 DAG                          │
              │  dag.AddNode(name, executor,         │
              │              dependencies, policy)   │
              └──────────────────────────────────────┘
```

#### 8.1.1 当前代码分析

当前 `pkg/dagexec/scheduler.go` 中的 `executeComponent()` 仅二路分发，无执行器注册机制：

```go
// 当前代码: 仅 Inline vs Manifest 两条路径 (scheduler.go:326-337)
func (s *Scheduler) executeComponent(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    node *topology.ComponentNode,
    tmpl manifest.TemplateContext,
) error {
    if node.Inline != nil {
        return s.executeInline(phaseCtx, oldCluster, newCluster, node)
    }
    return s.executeManifest(ctx, phaseCtx, node, tmpl)
}
```

**问题**：
- 无 Binary/Helm/YAML 执行器，Binary 和 Helm 组件类型无法处理
- 分发逻辑硬编码在 if-else 中，新增类型需修改 Scheduler 代码
- 无执行器注册机制，无法按需注入

#### 8.1.2 执行器注册表设计

**设计思路 - 为什么用注册表而非 switch-case**：

当前代码用 `if node.Inline != nil` 硬编码分发，新增组件类型需修改 `executeComponent()` 方法。引入 `ExecutorRegistry` 注册表后，新增类型只需调用 `registry.Register()` 注册新执行器，Scheduler 代码无需修改——符合开闭原则。

注册表还支持按需注入：Feature Gate OFF 时不注册 Binary/Helm/YAML 执行器，`registry.Get()` 返回错误，自动回退到旧路径。

```go
// pkg/dagexec/registry.go

// ExecutorRegistry 执行器注册表 (按组件类型注册)
type ExecutorRegistry struct {
    executors map[string]ComponentExecutor
}

// NewExecutorRegistry 创建空注册表
func NewExecutorRegistry() *ExecutorRegistry {
    return &ExecutorRegistry{
        executors: make(map[string]ComponentExecutor),
    }
}

// Register 注册执行器 (按组件类型)
func (r *ExecutorRegistry) Register(componentType string, executor ComponentExecutor) {
    r.executors[componentType] = executor
}

// Get 获取执行器 (未注册返回错误)
func (r *ExecutorRegistry) Get(componentType string) (ComponentExecutor, error) {
    executor, ok := r.executors[componentType]
    if !ok {
        return nil, fmt.Errorf("no executor registered for component type %q", componentType)
    }
    return executor, nil
}

// Has 检查是否已注册某类型
func (r *ExecutorRegistry) Has(componentType string) bool {
    _, ok := r.executors[componentType]
    return ok
}
```

#### 8.1.3 执行器分发实现

```go
// pkg/dagexec/scheduler.go 扩展

// executeComponent 四路分发 (Feature Gate ON)
func (s *Scheduler) executeComponent(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    node *topology.ComponentNode,
    tmpl manifest.TemplateContext,
) error {
    // Feature Gate OFF: 回退到旧路径 (二路分发)
    if !featuregate.Enabled(featuregate.BinaryComponentSupport) {
        return s.executeComponentLegacy(ctx, phaseCtx, oldCluster, newCluster, node, tmpl)
    }

    // Feature Gate ON: 四路分发
    componentType := node.ComponentType()
    executor, err := s.registry.Get(componentType)
    if err != nil {
        // 未注册的类型回退到 Manifest 路径 (兼容未迁移的组件)
        if s.registry.Has("yaml") {
            return s.registry.Get("yaml").ExecuteComponent(ctx, node, s.buildExecutionContext(phaseCtx, node, tmpl))
        }
        return s.executeManifest(ctx, phaseCtx, node, tmpl)
    }

    execCtx := s.buildExecutionContext(phaseCtx, node, tmpl)
    return executor.ExecuteComponent(ctx, node, execCtx)
}

// executeComponentLegacy 旧路径 (Feature Gate OFF 时的二路分发)
func (s *Scheduler) executeComponentLegacy(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    node *topology.ComponentNode,
    tmpl manifest.TemplateContext,
) error {
    if node.Inline != nil {
        return s.executeInline(phaseCtx, oldCluster, newCluster, node)
    }
    return s.executeManifest(ctx, phaseCtx, node, tmpl)
}

// buildExecutionContext 从 PhaseContext 构建 ExecutionContext
func (s *Scheduler) buildExecutionContext(
    phaseCtx *phaseframe.PhaseContext,
    node *topology.ComponentNode,
    tmpl manifest.TemplateContext,
) *ExecutionContext {
    return &ExecutionContext{
        Cluster:         phaseCtx.BKECluster,
        NodeProvider:    s.nodeProvider,
        Log:             phaseCtx.Log,
        VersionContext:  s.buildVersionContext(phaseCtx),
        TemplateContext: tmpl,
    }
}
```

#### 8.1.4 Scheduler 初始化与执行器注入

```go
// pkg/dagexec/scheduler.go 扩展

// Config 扩展: 新增 Binary/Helm/YAML 执行器依赖
type Config struct {
    InlineRunner        InlinePhaseRunner
    ManifestStore       manifest.Store
    ManifestApplier     manifest.Applier
    BinaryInstaller     BinaryInstaller       // 新增 (Feature Gate ON 时注入)
    HelmInstaller       HelmInstaller         // 新增
    YAMLExecutor        YAMLManifestExecutor  // 新增
    NodeProvider        NodeProvider          // 新增
    MaxParallelPerBatch int
}

// NewScheduler 创建调度器, 按需注册执行器
func NewScheduler(cfg Config) *Scheduler {
    maxParallel := cfg.MaxParallelPerBatch
    if maxParallel == 0 {
        maxParallel = defaultMaxParallelPerBatch
    }

    registry := NewExecutorRegistry()

    // Inline 执行器: 始终注册
    registry.Register("inline", &InlineComponentExecutor{
        runner: cfg.InlineRunner,
    })

    // Binary 执行器: Feature Gate ON 且依赖已注入时注册
    if cfg.BinaryInstaller != nil {
        registry.Register("binary", &BinaryComponentExecutor{
            installer: cfg.BinaryInstaller,
            store:     cfg.ManifestStore,
        })
    }

    // Helm 执行器: Feature Gate ON 且依赖已注入时注册
    if cfg.HelmInstaller != nil {
        registry.Register("helm", &HelmComponentExecutor{
            installer: cfg.HelmInstaller,
            store:     cfg.ManifestStore,
        })
    }

    // YAML 执行器: Feature Gate ON 且依赖已注入时注册
    if cfg.YAMLExecutor != nil {
        registry.Register("yaml", &ManifestComponentExecutor{
            applier: cfg.ManifestApplier,
            store:   cfg.ManifestStore,
        })
    }

    return &Scheduler{
        InlineRunner:        cfg.InlineRunner,
        ManifestStore:       cfg.ManifestStore,
        ManifestApplier:     cfg.ManifestApplier,
        registry:            registry,
        nodeProvider:        cfg.NodeProvider,
        MaxParallelPerBatch: maxParallel,
    }
}
```

#### 8.1.5 InlineComponentExecutor 实现

当前 Inline 路径也需要适配为 `ComponentExecutor` 接口，保持与 Binary/Helm/YAML 一致的分发方式：

```go
// pkg/dagexec/inline_executor.go

// InlineComponentExecutor 内联组件执行器
type InlineComponentExecutor struct {
    runner InlinePhaseRunner
}

func (e *InlineComponentExecutor) GetComponentType() string {
    return "inline"
}

func (e *InlineComponentExecutor) ExecuteComponent(
    ctx context.Context,
    node *ComponentNode,
    execCtx *ExecutionContext,
) error {
    if node.Inline == nil {
        return fmt.Errorf("component %q has no inline ref", node.Name)
    }

    handler := node.Inline.Handler
    version := node.Inline.Version
    if handler == "" {
        return fmt.Errorf("inline component %q missing handler", node.Name)
    }
    if version == "" {
        version = defaultComponentVersion
    }

    // Inline 执行器需要 oldCluster/newCluster, 从 ExecutionContext.Cluster 推导
    return e.runner.Execute(
        execCtx.phaseCtx,
        execCtx.oldCluster,
        execCtx.Cluster,
        handler,
        version,
    )
}
```

#### 8.1.6 当前代码 vs 目标设计对比

| 维度 | 当前代码 (scheduler.go) | 目标设计 |
|------|------------------------|---------|
| **分发方式** | `if node.Inline != nil` 二路 | `registry.Get(node.ComponentType())` 四路 |
| **执行器注册** | 无（硬编码 if-else） | `ExecutorRegistry` 按类型注册 |
| **执行器实例** | 无独立 Executor | Binary/Helm/YAML/Inline 各自实现 `ComponentExecutor` |
| **依赖注入** | `InlineRunner` + `ManifestStore` + `ManifestApplier` | 额外注入 `BinaryInstaller` + `HelmInstaller` + `YAMLExecutor` + `NodeProvider` |
| **Feature Gate** | 无 | ON→四路分发, OFF→`executeComponentLegacy` 二路分发 |
| **扩展性** | 新增类型需修改 `executeComponent()` | 新增类型只需 `registry.Register()`，Scheduler 不变 |
| **未注册类型处理** | 走 Manifest 路径 | 回退到 YAML/Manifest 路径（兼容未迁移组件） |

**设计思路**：DAG 调度采用"批次间串行、批次内并行"的执行策略。首先通过拓扑排序将 DAG 分解为多个批次，然后按顺序执行每个批次，批次内的组件可以并行执行。这种策略既保证了依赖关系的正确性，又最大化了并行度。

**关键设计点**：
- **拓扑排序**：使用 Kahn 算法将 DAG 分解为执行批次
- **批次串行**：批次之间严格按顺序执行，确保依赖满足
- **批次并行**：批次内组件通过 errgroup 并行执行，可配置最大并行数
- **失败策略**：支持 FailFast（立即终止）、Continue（继续执行）、Rollback（回滚后继续）

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

### 8.3 核心接口定义

**设计思路 - 接口分层与解耦**：

当前 `pkg/dagexec` 包依赖 `pkg/phaseframe` 包，导致 DAG 调度器无法独立编译和测试。本节通过三层接口设计实现完全解耦：

1. **上下文层（ExecutionContext）**：替代 `phaseframe.PhaseContext`，携带组件执行所需的全部上下文信息（集群、节点、版本、模板）。Executor 仅依赖此接口，不直接依赖 phaseframe。
2. **数据源层（NodeProvider）**：抽象节点获取逻辑，Executor 通过此接口获取目标节点列表，而非直接调用 `phaseCtx.NodeFetcher()`。便于测试时注入 Mock 节点。
3. **执行层（ComponentExecutor）**：统一组件执行器接口，Binary/Helm/YAML/Inline 四种执行器各自实现。Scheduler 通过此接口多态分发，不关心具体实现类型。

**接口间关系**：

- `Scheduler` 负责创建 `ExecutionContext`（内含 `VersionContext` + `NodeProvider` + `Cluster` + `TemplateContext`）
- `Scheduler` 根据 `ComponentNode.ComponentType()` 选择对应的 `ComponentExecutor`
- `ComponentExecutor` 从 `ExecutionContext` 获取上下文信息，**自主决定**操作类型（Install/Upgrade/Skip），而非由调用方下达指令
- `VersionContext` 提供版本事实，`NodeProvider` 提供节点事实，`ComponentExecutor` 基于事实做决策——这是声明式协调模式的核心

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           核心接口架构                                            │
└─────────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────┐
  │  Scheduler   │
  │  (调度入口)  │
  └──────┬───────┘
         │
         │ 创建
         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                    ExecutionContext                       │
  │                                                          │
  │  ┌─────────────────┐  ┌──────────────┐  ┌────────────┐  │
  │  │ VersionContext  │  │ NodeProvider │  │  Cluster   │  │
  │  │ (版本事实)      │  │ (节点获取)   │  │ (集群信息) │  │
  │  │                 │  │              │  │            │  │
  │  │ HasCurrent()    │  │ GetNodes()   │  │            │  │
  │  │ HasTarget()     │  │ GetNodesBy   │  │            │  │
  │  │ NeedsUpgrade()  │  │   Role()     │  │            │  │
  │  └─────────────────┘  └──────────────┘  └────────────┘  │
  │                                                          │
  │  ┌────────────────────────────────────────────────────┐  │
  │  │              TemplateContext                        │  │
  │  │              (模板渲染上下文)                       │  │
  │  └────────────────────────────────────────────────────┘  │
  └──────────────────────────────────────────────────────────┘
         │
         │ 传递 ExecutionContext
         ▼
  ┌──────────────────────────────────────────────────────────┐
  │              ComponentExecutor (统一接口)                 │
  │                                                          │
  │  ExecuteComponent(ctx, node, execCtx) error              │
  │  GetComponentType() ComponentType                        │
  └──────────────────────────┬───────────────────────────────┘
                             │
            ┌────────────────┼────────────────┬──────────────┐
            │                │                │              │
            ▼                ▼                ▼              ▼
  ┌──────────────┐  ┌──────────────┐  ┌────────────┐  ┌────────────┐
  │   Binary     │  │    Helm      │  │    YAML    │  │   Inline   │
  │ Component    │  │ Component    │  │ Manifest   │  │ Component  │
  │ Executor     │  │ Executor     │  │ Executor   │  │ Executor   │
  └──────┬───────┘  └──────┬───────┘  └─────┬──────┘  └─────┬──────┘
         │                 │                │               │
         ▼                 ▼                ▼               ▼
  ┌──────────────┐  ┌──────────────┐  ┌────────────┐  ┌────────────┐
  │ 从 VC 决定   │  │ 从 VC 决定   │  │ 从 VC 决定 │  │ 从 VC 决定 │
  │ Install或    │  │ Install或    │  │ 是否 Skip  │  │ Install或  │
  │ Upgrade      │  │ Upgrade      │  │            │  │ Upgrade    │
  └──────────────┘  └──────────────┘  └────────────┘  └────────────┘

  数据流: Scheduler → ExecutionContext → ComponentExecutor → Installer
  控制流: ComponentExecutor 根据 VersionContext 自主决定操作类型
```

#### 8.3.1 ExecutionContext 定义

**设计思路 - 为什么用 VersionContext 而非 IsUpgrade bool 或 OperationType 枚举**：
1. **声明式协调**：Kubernetes 控制器应基于"当前状态 vs 期望状态"自主决定操作，而非由调用方显式下达操作指令。VersionContext 提供版本事实（已安装版本、目标版本），Executor 根据 `HasCurrent`/`HasTarget`/`NeedsUpgrade` 自主推断 Install/Upgrade/Skip。
2. **避免概念重复**：`BinaryAction` (Install/Upgrade/Uninstall) 和 `HelmAction` (Install/Upgrade/Rollback) 已在各自 Executor 中定义。ExecutionContext 中再放 OperationType 枚举会造成两套枚举需要映射，增加维护负担。
3. **扩展性**：后续支持 Rollback 时，只需在 VersionContext 中新增版本历史记录 (`previousVersions map`)，Executor 即可推断 Rollback 操作，无需修改 ExecutionContext 接口。
4. **与实际代码一致**：当前 `PhaseContext` 已使用 `VersionContext` 进行版本判断，设计文档与实现保持一致。

```go
// pkg/dagexec/context.go

// VersionContext 携带组件版本事实，Executor 据此自主决定操作类型
type VersionContext struct {
    // currentVersions 组件已安装版本映射 (componentName → currentVersion)
    // 空表示组件未安装
    currentVersions map[string]string

    // targetVersions 组件目标版本映射 (componentName → targetVersion)
    // 空表示组件无升级目标
    targetVersions map[string]string
}

// HasCurrent 组件是否已安装
func (vc *VersionContext) HasCurrent(name string) bool {
    return vc != nil && vc.currentVersions != nil
    _, ok := vc.currentVersions[name]
    return ok
}

// HasTarget 组件是否有升级目标
func (vc *VersionContext) HasTarget(name string) bool {
    if vc == nil || vc.targetVersions == nil {
        return false
    }
    _, ok := vc.targetVersions[name]
    return ok
}

// NeedsUpgrade 组件是否需要升级 (已安装且目标版本不同于当前版本)
func (vc *VersionContext) NeedsUpgrade(name string) bool {
    if vc == nil {
        return true
    }
    current, hasCurrent := vc.currentVersions[name]
    target, hasTarget := vc.targetVersions[name]
    if !hasTarget {
        return true // 无目标版本，默认需要执行
    }
    if !hasCurrent {
        return true // 未安装，需要安装
    }
    return current != target // 版本不同，需要升级
}

// CurrentVersion 获取组件当前已安装版本
func (vc *VersionContext) CurrentVersion(name string) (string, bool) {
    if vc == nil || vc.currentVersions == nil {
        return "", false
    }
    v, ok := vc.currentVersions[name]
    return v, ok
}

// TargetVersion 获取组件目标版本
func (vc *VersionContext) TargetVersion(name string) (string, bool) {
    if vc == nil || vc.targetVersions == nil {
        return "", false
    }
    v, ok := vc.targetVersions[name]
    return v, ok
}

// ExecutionContext 组件执行上下文 (完全独立于 phaseframe)
type ExecutionContext struct {
    // 集群信息
    Cluster *bkev1beta1.BKECluster

    // 节点提供者 (抽象接口，不依赖 phaseframe)
    NodeProvider NodeProvider

    // 日志记录器
    Log *bkev1beta1.BKELogger

    // 版本上下文 (替代 IsUpgrade bool，携带版本事实供 Executor 自主决定操作)
    VersionContext *VersionContext

    // 模板上下文 (复用 manifest.TemplateContext)
    TemplateContext manifest.TemplateContext
}

// NewExecutionContext 创建执行上下文
func NewExecutionContext(
    cluster *bkev1beta1.BKECluster,
    nodeProvider NodeProvider,
    log *bkev1beta1.BKELogger,
    versionContext *VersionContext,
) *ExecutionContext {
    return &ExecutionContext{
        Cluster:        cluster,
        NodeProvider:   nodeProvider,
        Log:            log,
        VersionContext: versionContext,
    }
}
```

#### 8.3.2 NodeProvider 接口定义

**设计思路**：NodeProvider 接口抽象节点获取逻辑，替代原来对 phaseframe.NodeFetcher 的依赖。Node 结构体仅包含基础信息（Name/IP/Hostname/Role），不包含 Arch/OS/OSVersion。Arch 由 BinaryInstaller 在 Install() 中通过 SSH 发现（`uname -m`），OS 由安装脚本运行时自检测（`/etc/os-release`），NodeProvider 不执行 SSH 预检测。

**设计原则**：
- **最小化 Node 信息**：NodeProvider 只提供节点的基础标识信息，不执行 SSH 检测
- **Arch 由 BinaryInstaller SSH 发现**：架构信息由 BinaryInstaller.Install() 通过 SSH 执行 `uname -m` 获取（制品 URL 中的 `{{arch}}` 需下载前解析），NodeProvider 不负责
- **OS 脚本自检测**：操作系统信息由安装脚本通过 `/etc/os-release` 自检测，不影响制品下载
- **简化实现**：NodeProvider 实现简单，无需 SSH 连接，只需从 BKECluster 读取节点列表

```go
// pkg/dagexec/node_provider.go

// NodeProvider 节点提供者接口 (替代 phaseframe.NodeFetcher)
type NodeProvider interface {
    // GetNodes 获取集群的节点列表
    GetNodes(ctx context.Context, cluster *bkev1beta1.BKECluster) ([]Node, error)
    
    // GetNodesByRole 按角色获取节点
    GetNodesByRole(ctx context.Context, cluster *bkev1beta1.BKECluster, role string) ([]Node, error)
}

// Node 节点信息 (最小化，不包含 Arch/OS/OSVersion)
// Arch 由 BinaryInstaller.Install() 通过 SSH 发现 (uname -m)
// OS/OSVersion 由安装脚本运行时自检测 (/etc/os-release)
type Node struct {
    Name      string
    IP        string
    Hostname  string
    Role      string  // master/worker/etcd
    Status    NodeStatus
}

// NodeStatus 节点状态
type NodeStatus struct {
    Ready bool
}

// BKENodeProvider NodeProvider 的默认实现
type BKENodeProvider struct {
    client client.Client
}

// NewBKENodeProvider 创建节点提供者
func NewBKENodeProvider(client client.Client) *BKENodeProvider {
    return &BKENodeProvider{client: client}
}

// GetNodes 获取集群的节点列表 (从 BKECluster.Spec.NodeRefs 读取)
func (p *BKENodeProvider) GetNodes(ctx context.Context, cluster *bkev1beta1.BKECluster) ([]Node, error) {
    var nodes []Node
    for _, ref := range cluster.Spec.NodeRefs {
        node := Node{
            Name:     ref.Name,
            IP:       ref.IP,
            Hostname: ref.Hostname,
            Role:     ref.Role,
        }
        nodes = append(nodes, node)
    }
    return nodes, nil
}

// GetNodesByRole 按角色获取节点
func (p *BKENodeProvider) GetNodesByRole(ctx context.Context, cluster *bkev1beta1.BKECluster, role string) ([]Node, error) {
    allNodes, err := p.GetNodes(ctx, cluster)
    if err != nil {
        return nil, err
    }
    
    var filtered []Node
    for _, node := range allNodes {
        if node.Role == role {
            filtered = append(filtered, node)
        }
    }
    return filtered, nil
}
```

#### 8.3.3 ComponentExecutor 接口 (解耦后)

```go
// pkg/dagexec/executor.go

// ComponentExecutor 组件执行器接口 (不再依赖 phaseframe)
type ComponentExecutor interface {
    // ExecuteComponent 执行组件
    ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error
    
    // GetComponentType 获取组件类型
    GetComponentType() ComponentType
}

// BinaryComponentExecutor 二进制组件执行器
type BinaryComponentExecutor struct {
    installer *binaryinstaller.BinaryInstaller
    store     *manifest.Store
}
```

**设计思路 - 三种节点级执行策略**：

为什么需要三种策略？不同组件的风险等级不同，需要不同的并发控制粒度。此处的 Rolling/Parallel/Batch 是"组件内节点级"并发控制，与 DAG 层面的 `executeBatchParallel`（组件间并行）是两个独立的层级：

```
DAG 层: Batch 1 [containerd, bkeagent] → Batch 2 [coredns]  (组件间并行)
         ↓
节点层: containerd 在 10 个节点上 Rolling 执行             (节点间串行)
        bkeagent 在 10 个节点上 Batch(batchSize=3) 执行    (节点间分批)
```

策略对比：

| 策略 | 并发度 | 适用场景 | FailurePolicy 交互 |
|------|--------|---------|-------------------|
| Rolling | 1 | 高风险组件 (containerd) | 逐节点判定: 单节点失败可 Rollback 后继续下一节点，集群始终有节点在线提供服务 |
| Parallel | N (全部) | 低风险组件 (配置文件更新) | 全节点同时操作: FailFast 时全部中断; Continue 时部分成功部分失败，集群可能不一致 |
| Batch | BatchSize | 中风险组件 (bkeagent) | 逐批判定: 每批完成后可检查集群健康状态，异常则暂停后续批次 |

对整体结果的影响：
- **Rolling**: 集群始终有节点在线，但执行时间最长 (N × 单节点耗时)
- **Parallel**: 执行最快，但所有节点同时不可用，风险最高
- **Batch**: 平衡点，每批结束后有检查点，可中途暂停 (⌈N/BatchSize⌉ × 单节点耗时)

默认策略: 当 Mode 为空时走 default 分支返回 nil (不执行)，应在 Admission Webhook 中校验 Mode 非空，或在 default 分支回退到 Rolling。

```go
// ExecuteComponent 执行二进制组件
func (e *BinaryComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error {
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
    
    // 3. 获取需要操作的节点 (通过 NodeProvider，不再依赖 phaseCtx)
    nodes, err := execCtx.NodeProvider.GetNodes(ctx, execCtx.Cluster)
    if err != nil {
        return fmt.Errorf("failed to get nodes: %w", err)
    }
    
    // 4. 根据 VersionContext 判断操作类型
    vc := execCtx.VersionContext
    if vc != nil && !vc.NeedsUpgrade(component.Name) {
        execCtx.Log.Info("component %s already at target version, skipping", component.Name)
        return nil
    }
    
    // 5. 根据升级策略执行
    strategy := cv.Spec.UpgradeStrategy
    switch strategy.Mode {
    case "Rolling":
        return e.executeRolling(ctx, nodes, cv, strategy, execCtx)
    case "Parallel":
        return e.executeParallel(ctx, nodes, cv, strategy, execCtx)
    case "Batch":
        return e.executeBatch(ctx, nodes, cv, strategy, execCtx)
    }
    
    return nil
}

// executeRolling 滚动执行 (逐节点)
func (e *BinaryComponentExecutor) executeRolling(ctx context.Context, nodes []Node, 
    cv *ComponentVersion, strategy UpgradeStrategySpec, execCtx *ExecutionContext) error {
    for _, node := range nodes {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }
        
        // 为每个节点扩展 TemplateContext
        // 注意：不包含 Arch/OS/OSVersion
        // Arch 由 BinaryInstaller.Install() 通过 SSH 发现后填入 NodeArch
        // OS/OSVersion 由安装脚本运行时自检测
        nodeTmpl := execCtx.TemplateContext  // 复制基础 TemplateContext
        nodeTmpl.NodeIP = node.IP
        nodeTmpl.NodeHostname = node.Hostname
        nodeTmpl.NodeRole = node.Role
        nodeTmpl.ComponentVersion = cv.Spec.Version
        
        // 根据 VersionContext 自主决定操作类型
        action := binaryinstaller.BinaryActionInstall
        if execCtx.VersionContext != nil && execCtx.VersionContext.HasCurrent(component.Name) {
            action = binaryinstaller.BinaryActionUpgrade // 已安装 → 升级
        }
        
        opts := binaryinstaller.InstallOptions{
            Component:   cv,
            TemplateCtx: nodeTmpl,  // 传递扩展后的 TemplateContext
            Action:      action,
            Timeout:     strategy.Timeout,
            RetryCount:  3,
        }
        
        if err := e.installer.Install(ctx, opts); err != nil {
            switch strategy.FailurePolicy {
            case "FailFast":
                return err
            case "Continue":
                execCtx.Log.Warn("node %s upgrade failed, continuing: %v", node.IP, err)
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
func (e *HelmComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error {
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
    
    // 3. 根据 VersionContext 判断操作类型 (Executor 自主决定，不再依赖 IsUpgrade)
    vc := execCtx.VersionContext
    if vc != nil && !vc.NeedsUpgrade(component.Name) {
        execCtx.Log.Info("component %s already at target version, skipping", component.Name)
        return nil
    }
    
    action := helminstaller.HelmActionInstall
    if vc != nil && vc.HasCurrent(component.Name) {
        action = helminstaller.HelmActionUpgrade // 已安装 → 升级
    }
    
    // 4. 填充 TemplateContext 的版本信息
    tmpl := execCtx.TemplateContext
    tmpl.ComponentVersion = cv.Spec.Version
    
    // 5. 执行 Helm 操作
    opts := helminstaller.InstallOptions{
        Component:   cv,
        TemplateCtx: tmpl,
        Action:      action,
        Timeout:     cv.Spec.UpgradeStrategy.Timeout,
    }
    
    return e.installer.Install(ctx, opts)
}

// ManifestComponentExecutor YAML/Manifest 组件执行器
type ManifestComponentExecutor struct {
    applier *manifest.Applier
    store   *manifest.Store
}

// ExecuteComponent 执行 YAML/Manifest 组件
func (e *ManifestComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error {
    component := node.Component
    
    // 1. 获取 ComponentVersion
    cv, err := e.store.GetComponentVersion(component.Name, component.Version)
    if err != nil {
        return fmt.Errorf("failed to get component version: %w", err)
    }
    
    // 2. 确认是 YAML 类型
    if cv.Spec.Type != configv1alpha1.ComponentTypeYAML {
        return fmt.Errorf("component %s is not a yaml component", component.Name)
    }
    
    // 3. 根据 VersionContext 判断是否需要执行 (幂等性检查)
    vc := execCtx.VersionContext
    if vc != nil && !vc.NeedsUpgrade(component.Name) {
        execCtx.Log.Info("component %s already at target version, skipping", component.Name)
        return nil
    }
    
    // 4. 构建 ComponentPackage
    pkg := &manifest.ComponentPackage{
        Name:      component.Name,
        Version:   component.Version,
        Resources: cv.Spec.Resources,
    }
    
    // 5. 应用 Manifest
    return e.applier.ApplyComponent(ctx, pkg)
}
```

#### 8.3.4 依赖关系对比

| 维度 | 修改前 | 修改后 |
|------|--------|--------|
| **接口依赖** | `phaseframe.PhaseContext` | `dagexec.ExecutionContext` |
| **节点获取** | `phaseCtx.NodeFetcher()` | `execCtx.NodeProvider.GetNodes()` |
| **集群信息** | `phaseCtx.BKECluster` | `execCtx.Cluster` |
| **日志记录** | `phaseCtx.Log` | `execCtx.Log` |
| **操作类型** | `phaseCtx.IsUpgrade` | `execCtx.VersionContext` (携带版本事实，Executor 自主决定操作) |
| **包依赖** | `pkg/dagexec` → `pkg/phaseframe` | `pkg/dagexec` 独立，无 phaseframe 依赖 |

**设计说明**：
- **ExecutionContext**：完全独立于 phaseframe，仅包含组件执行所需的最小上下文
- **NodeProvider**：通过接口抽象节点获取逻辑，便于测试和扩展
- **VersionContext**：携带版本事实（已安装版本、目标版本），Executor 据此自主决定操作类型（Install/Upgrade/Skip），替代原有的 `IsUpgrade bool`，符合 Kubernetes 声明式协调模式
- **TemplateContext**：复用 `manifest.TemplateContext`，所有组件类型共享使用
- **解耦收益**：`pkg/dagexec` 包可独立编译和测试，不依赖 phaseframe

---

## 9. 完整安装流程详细设计

### 9.1 安装流程图

**设计思路**：完整安装流程从用户创建 BKECluster 开始，经过 ReleaseImage 解析、ComponentVersion 加载、DAG 构建、DAG 执行，最终完成所有组件的安装。流程中 Binary/Helm/YAML/Inline 四种类型的组件通过各自的 Executor 并行执行——Binary 组件通过 SSH 在远程节点安装二进制制品，Helm 组件通过 Helm SDK 部署 Chart，YAML 组件通过 YAMLManifestExecutor 将 Kubernetes 清单直接应用到目标集群，Inline 组件通过内联执行器完成 Kubernetes 集群初始化。所有组件安装完成后通过健康检查确认安装成功。

**关键设计点**：
- **声明式安装**：通过 ReleaseImage 声明需要安装的组件列表
- **DAG 调度**：根据组件依赖关系构建 DAG，按拓扑顺序执行
- **多类型支持**：Binary、Helm、YAML、Inline
- **健康检查**：安装完成后执行 PodReady/EndpointReady 检查
- **YAML 清单应用**：YAML 类型组件通过 YAMLManifestExecutor 应用 Kubernetes 清单，支持 ServerSideApply/Replace/CreateOnly 三种策略

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
                      │  ├── openfuyao-core/v26.03 (yaml)    │
                      │  ├── kubernetes-master/v1.29.0       │
                      │  │                   (inline)        │
                      │  └── kubernetes-worker/v1.29.0       │
                      │                      (inline)        │
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
                      │                   → kubernetes-master │
                      │                     (inline)         │
                      │                   → kubernetes-worker │
                      │                     (inline)         │
                      │                   → coredns (helm)   │
                      │                   → openfuyao-core   │
                      │                     (yaml)           │
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
                      │  8. YAMLManifestExecutor             │
                      │  执行 YAML 类型组件安装              │
                     └────────────────────┬─────────────────┘
                                          │
                     ┌────────────────────┼────────────────────┐
                     │                    │                    │
                     ▼                    ▼                    ▼
           ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
           │  获取清单       │  │  解析多文档     │  │  按策略应用     │
           │  ManifestStore  │  │  YAML Parser    │  │  ServerSideApply│
           │  或 URL 下载    │  │  → Unstructured │  │  → K8s Applier  │
           └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                    │                    │                    │
                    └────────────────────┼────────────────────┘
                                         │
                                         ▼
                     ┌──────────────────────────────────────┐
                     │  9. 健康检查                         │
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

## 10. 完整升级流程详细设计

### 10.1 升级流程图

**设计思路**：完整升级流程从用户修改 ClusterVersion 的 desiredVersion 开始，经过版本对比、DAG 构建、DAG 执行，最终完成所有组件的升级。流程中通过对比当前 ReleaseImage 和目标 ReleaseImage 确定需要升级的组件，Binary 组件采用滚动升级策略，Helm 组件使用 `helm upgrade --atomic` 确保原子性。

**关键设计点**：
- **版本对比**：对比当前和目标 ReleaseImage 确定升级范围
- **滚动升级**：Binary 组件逐节点升级，确保服务不中断
- **原子升级**：Helm 组件使用 `--atomic` 标志，失败自动回滚
- **失败策略**：支持 FailFast/Continue/Rollback 三种策略
- **YAML 清单升级**：YAML 类型组件通过 ServerSideApply 增量更新，支持 Prune 裁剪不再需要的资源

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
                     │  ├── coredns: v1.10.1 → v1.11.1      │
                     │  └── openfuyao-core: v26.01 → v26.03 │
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
                      │            → openfuyao-core (yaml)   │
                      │            → etcd (inline)           │
                      │            → kubernetes-worker       │
                      │              (inline)                │
                      │            → kubernetes-master       │
                      │              (inline)                │
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
                     │  Batch 5: openfuyao-core (yaml)      │
                     │  ServerSideApply 增量更新             │
                     │  + Prune 裁剪废弃资源                 │
                     └────────────────────┬─────────────────┘
                                          │
                                          ▼
                     ┌──────────────────────────────────────┐
                     │  Batch 6: etcd → worker → master     │
                     │  (inline Phase 执行)                 │
                     └────────────────────┬─────────────────┘
                                          │
                                          ▼
                     ┌──────────────────────────────────────┐
                     │  Batch 7: component → cluster        │
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

## 11. 迁移策略详细设计

### 11.1 迁移流程图

**设计思路**：迁移策略通过 Feature Gate 控制新旧两种执行路径。启用 Feature Gate 后，新集群使用 DAG + BinaryInstaller/HelmInstaller 的新路径，旧集群保持原有的硬编码 Phase 路径。兼容层在 reconcile 时根据 Feature Gate 状态选择执行路径，确保平滑迁移。

**关键设计点**：
- **Feature Gate**：通过 `BinaryComponentSupport` 和 `HelmComponentSupport` 控制
- **双轨运行**：新旧路径可以并存，通过 Feature Gate 切换
- **兼容层**：在 reconcile 入口根据 Feature Gate 选择执行路径
- **灰度发布**：可以先在测试环境启用，验证后再推广到生产环境

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

### 11.2 Feature Gate 定义

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

### 11.3 containerd 重构详细设计

#### 11.3.1 当前 Phase 逻辑分析

containerd 的安装和升级分别由两个 Phase 负责，且都依赖 bkeagent 内置命令完成：

**安装路径 — `EnsureNodesEnv` Phase**：

containerd 安装嵌入在节点环境初始化流程中，不是独立 Phase：

1. `EnsureNodesEnv.Execute()` → `CheckOrInitNodesEnv()` → `buildEnvCommand()`
2. `buildEnvCommand()` → `BuildCommonEnvCommand()` → `ENV.New()` → `buildCommandSpec()`
3. `buildCommandSpec()` 生成三步内置命令：
   - `K8sEnvInit(scope=node)` — 硬件资源检查
   - `Reset(scope=cert,manifests,container,kubelet,extra)` — 节点重置
   - `K8sEnvInit(scope=time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,runtime,iptables,registry,extra)` — 环境初始化
4. 其中 `scope=runtime` 即 containerd 安装：bkeagent 内部负责下载 containerd 二进制包、生成 config.toml、安装 systemd service、启动服务

**升级路径 — `EnsureContainerdUpgrade` Phase**：

1. **版本比较**：`isContainerdNeedUpgrade` 比较 `BKECluster.Status.ContainerdVersion` 与 `Spec.ClusterConfig.Cluster.ContainerdVersion`
2. **两步升级**：`rolloutContainerd()` 依次调用：
   - `resetContainerd()` → `NewConatinerdReset()` → 发送 `Reset` 命令（`scope=containerd-cfg`）→ bkeagent 重置 containerd 配置
   - `redeployContainerd()` → `NewConatinerdRedeploy()` → 发送 `K8sEnvInit` 命令（`scope=runtime`, `containerdVersion=x.x.x`）→ bkeagent 重新安装 containerd

**关键问题**：
- containerd 安装逻辑封装在 bkeagent 内置命令中，控制器无法控制安装路径、配置内容、制品来源
- 安装路径不是独立 Phase，嵌在 `EnsureNodesEnv` 的 `K8sEnvInit(scope=runtime)` 中
- 升级路径 `EnsureContainerdUpgrade` 也委托给 bkeagent 内置命令，非 SSH 推送
- config.toml、containerd.service 内容由 bkeagent 内部生成，不可声明式配置

#### 11.3.2 ComponentVersion YAML 完整定义

```yaml
# bke-manifests/containerd/v1.7.18/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.18
spec:
  name: containerd
  type: binary
  version: v1.7.18

  binary:
    variables:
      logLevel: "info"
      snapshotter: "overlayfs"
      sandboxImage: "{{imageRegistry}}/pause:3.9"

    artifacts:
      - name: containerd
        url: "https://release-repo.openfuyao.cn/binaries/containerd/{{version}}/containerd-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:abc123def456..."
        installPath: "/usr/local"
        executable: containerd

    configTemplates:
      - name: config.toml
        path: "/etc/containerd/config.toml"
        mode: "0644"
        owner: "root:root"
        content: |
          version = 2
          [plugins]
            [plugins."io.containerd.grpc.v1.cri"]
              sandbox_image = "{{.Variables.sandboxImage}}"
              [plugins."io.containerd.grpc.v1.cri".containerd]
                snapshotter = "{{.Variables.snapshotter}}"
              [plugins."io.containerd.grpc.v1.cri".registry]
                [plugins."io.containerd.grpc.v1.cri".registry.mirrors]
                  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."docker.io"]
                    endpoint = ["https://{{imageRegistry}}"]

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

    installScript: |
      #!/bin/bash
      set -e
      # 集群: {{clusterName}}, 节点: {{nodeIP}} ({{nodeRole}})
      # 架构: {{nodeArch}}, 版本: {{componentVersion}}, 操作: {{action}}

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
      cp {{artifact.containerd.installPath}}/bin/containerd {{artifact.containerd.installPath}}/bin/containerd.bak.$(date +%Y%m%d%H%M%S)
      {{end}}

      # 4. 解压并安装新二进制 (tar.gz 包含 containerd, containerd-shim-runc-v2, ctr)
      tar -xzf {{artifact.containerd.path}} -C {{artifact.containerd.installPath}}
      chmod +x {{artifact.containerd.installPath}}/bin/containerd

      # 5. 安装配置文件和服务 (由 ConfigRenderer 自动上传)
      # config.toml → {{configPath}}/config.toml
      # containerd.service → /etc/systemd/system/containerd.service

      # 6. 启动并验证
      systemctl daemon-reload
      systemctl enable containerd
      systemctl start containerd
      {{artifact.containerd.installPath}}/bin/containerd --version

    uninstallScript: |
      #!/bin/bash
      systemctl stop containerd || true
      systemctl disable containerd || true
      rm -f {{artifact.containerd.installPath}}/bin/containerd {{artifact.containerd.installPath}}/bin/containerd-shim-runc-v2 {{artifact.containerd.installPath}}/bin/containerd-stress {{artifact.containerd.installPath}}/bin/ctr
      rm -f /etc/systemd/system/containerd.service
      systemctl daemon-reload

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]
      - name: kylin
        versions: ["V10"]

    defaultConfigPath: "/etc/containerd"
    defaultLogPath: "/var/log/containerd"
    defaultDataPath: "/var/lib/containerd"

    healthCheck:
      enabled: true
      timeout: "2m"
      interval: "5s"
      script: |
        #!/bin/bash
        systemctl is-active containerd
        {{artifact.containerd.installPath}}/bin/containerd --version | grep -q "{{componentVersion}}"
        crictl info > /dev/null 2>&1

  compatibility:
    constraints:
      - component: kubernetes-master
        rule: ">=1.26.0"

  dependencies:
    - name: bkeagent
      phase: Upgrade

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

#### 11.3.3 字段映射表

| 旧硬编码逻辑 | 新 ComponentVersion 字段 | 说明 |
|-------------|------------------------|------|
| `EnsureNodesEnv` → `K8sEnvInit(scope=runtime)` 安装 containerd | `BinaryComponentExecutor` → `BinaryInstaller.Install()` | 安装从 agent 内置命令改为 SSH 推送 |
| `EnsureContainerdUpgrade` → `resetContainerd` + `redeployContainerd` | `BinaryInstaller.Install(Action=Upgrade)` | 升级从 agent 内置命令改为 SSH 推送 |
| bkeagent 内置 containerd 二进制下载 | `binary.artifacts[].url` | 制品下载声明式化 |
| bkeagent 内置 config.toml 生成 | `binary.configTemplates[0].content`（Go template） | 配置模板声明式化 |
| bkeagent 内置 containerd.service 生成 | `binary.configTemplates[1].content` | systemd 服务声明式化 |
| `ENV.ContainerdVersion` 传参给 bkeagent | `VersionContext.TargetVersion("containerd")` | 版本传递声明式化 |
| `Spec.ClusterConfig.Cluster.ContainerdVersion` | `spec.version` | 版本号 |
| `checksum` 硬编码常量 | `binary.artifacts[].checksum` | 校验和声明式化 |
| `installPath` 硬编码 | `binary.artifacts[].installPath` (per-artifact) | 每个 artifact 独立指定安装路径 |
| SSH 命令序列硬编码 | `binary.installScript`（Go template） | 安装脚本声明式化 |
| `arch` 硬编码 | `{{arch}}` 模板变量 | 架构适配模板化 |
| `isContainerdNeedUpgrade()` 版本比较 | `VersionContext.NeedsUpgrade("containerd")` | 版本决策声明式化 |
| 固定逐节点滚动 | `upgradeStrategy.mode: Rolling` | 升级策略可配置 |
| 无失败策略 | `upgradeStrategy.failurePolicy: FailFast` | 失败策略可配置 |
| 无超时控制 | `upgradeStrategy.timeout: "10m"` | 超时可配置 |
| 不支持卸载 | `binary.uninstallScript` | 卸载脚本声明式化 |

#### 11.3.4 行为等价性验证点

| 验证项 | 旧路径 | 新路径 (BinaryInstaller) | 验证方法 |
|--------|--------|------------------------|---------|
| containerd 安装时机 | `EnsureNodesEnv` 中 `K8sEnvInit(scope=runtime)` | containerd binary 节点先于 `EnsureNodesEnv` | DAG 拓扑顺序验证 |
| EnsureNodesEnv scope | 含 `runtime` | 不含 `runtime` | 检查 `K8sEnvInit` 命令参数 |
| 二进制文件路径 | bkeagent 内部决定 | `artifacts[0].installPath` = `/usr/local` (per-artifact, 解压后二进制在 `/usr/local/bin/`) | 检查远程节点文件路径 |
| config.toml 内容 | bkeagent 内部生成 | Go template 渲染 | `diff` 对比两份输出 |
| containerd.service 内容 | bkeagent 内部生成 | `configTemplates[1].content` | `diff` 对比两份输出 |
| 安装执行顺序 | bkeagent 内置逻辑 | installScript: 停止→备份→解压→启动 | 对比 SSH 执行日志 |
| 版本比较逻辑 | `Status.ContainerdVersion != Spec.ContainerdVersion` | `VersionContext.NeedsUpgrade("containerd")` | 相同版本输入，决策结果一致 |
| 架构适配 | bkeagent 内部处理 | `{{arch}}` 模板替换 | amd64/arm64 节点分别验证 |
| 滚动升级行为 | bkeagent 内置（全节点同时） | `upgradeStrategy.mode: Rolling` (逐节点) | 3 节点集群验证逐节点执行 |
| Feature Gate OFF 回退 | `EnsureNodesEnv` 含 `runtime` | `EnsureNodesEnv` 含 `runtime` | 行为不变 |
| containerd 版本传递 | `ENV.ContainerdVersion` 字段 | `VersionContext.TargetVersion` | 相同版本输入结果一致 |

#### 11.3.5 EnsureNodesEnv 重构设计

**设计思路**：当前 containerd 安装嵌入在 `EnsureNodesEnv` 的 `K8sEnvInit(scope=runtime)` 中，由 bkeagent 内置命令完成。重构后将 containerd 拆出为独立 binary 组件，通过 BinaryInstaller SSH 推送安装，`EnsureNodesEnv` 的 scope 中移除 `runtime`。

**scope 变更**：

| 命令步骤 | 重构前 scope | 重构后 scope (Feature Gate ON) | 重构后 scope (Feature Gate OFF) |
|---------|-------------|-------------------------------|-------------------------------|
| K8sEnvInit 第3步 | `time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,runtime,iptables,registry,extra` | `time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,iptables,registry,extra` | `time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,runtime,iptables,registry,extra` (不变) |
| Reset (DeepRestore=true) | `cert,manifests,container,kubelet,containerRuntime,extra` | `cert,manifests,container,kubelet,extra` | `cert,manifests,container,kubelet,containerRuntime,extra` (不变) |

**DAG 依赖关系变更**：

```
重构前 (DeployPhases):
  EnsureBKEAgent → EnsureNodesEnv(含 runtime scope) → EnsureClusterAPIObj → ...

重构后 (Feature Gate ON):
  EnsureBKEAgent → containerd(binary) → EnsureNodesEnv(去除 runtime) → EnsureClusterAPIObj → ...
```

containerd 作为独立 DAG 节点：
- **依赖**：bkeagent（需要 agent 在线才能 SSH 推送）
- **被依赖**：EnsureNodesEnv（需要容器运行时就绪后才能初始化 kubelet 等环境）

**Feature Gate 兼容层实现**：

```go
// pkg/command/env.go 扩展

// getK8sEnvInitScope 动态构建 K8sEnvInit 的 scope
// Feature Gate ON 时移除 runtime（containerd 由 BinaryInstaller 安装）
func (e *ENV) getK8sEnvInitScope() string {
    scopes := []string{"time", "hosts", "dns", "kernel", "firewall", "selinux", "swap", "httpRepo"}
    if !featuregate.Enabled(featuregate.BinaryComponentSupport) {
        scopes = append(scopes, "runtime") // 旧路径: bkeagent 内置命令安装 containerd
    }
    scopes = append(scopes, "iptables", "registry", "extra")
    return "scope=" + strings.Join(scopes, ",")
}

// getResetScope 动态构建 Reset 的 scope
func (e *ENV) getResetScope() string {
    if e.DeepRestore {
        if featuregate.Enabled(featuregate.BinaryComponentSupport) {
            return "scope=cert,manifests,container,kubelet,extra"
        }
        return "scope=cert,manifests,container,kubelet,containerRuntime,extra"
    }
    return "scope=cert,manifests,container,kubelet,extra"
}
```

**EnsureContainerdUpgrade 重构**：

| Feature Gate 状态 | 升级路径 | 说明 |
|-------------------|---------|------|
| ON | DAG 中 containerd binary 节点 → `BinaryInstaller.Install(Action=Upgrade)` | SSH 推送安装，替代 `resetContainerd` + `redeployContainerd` |
| OFF | `EnsureContainerdUpgrade` Phase → `resetContainerd` + `redeployContainerd` | bkeagent 内置命令，行为不变 |

兼容层入口：
```go
// 兼容层: EnsureContainerdUpgrade Phase 根据Feature Gate选择路径
func (e *EnsureContainerdUpgrade) Execute() (ctrl.Result, error) {
    if featuregate.Enabled(featuregate.BinaryComponentSupport) {
        // 新路径: 不执行任何操作，containerd 升级由 DAG 中的 binary 节点处理
        return ctrl.Result{}, nil
    }
    // 旧路径: resetContainerd + redeployContainerd
    return e.rolloutContainerd()
}
```

### 11.4 bkeagent 重构详细设计

#### 11.4.1 当前 Phase 逻辑分析

bkeagent 的安装和升级分别由两个 Phase 负责，均通过 SSH 推送二进制文件完成：

**安装路径 — `EnsureBKEAgent` Phase**（DeployPhases 第一步）：

1. `EnsureBKEAgent.Execute()` → `loadLocalKubeConfig()` → `getNeedPushNodes()` → `pushAgent()`
2. `pushAgent()` → `prepareServiceFile()` 生成 bkeagent.service → `performAgentPush()` SSH 推送
3. SSH 命令序列（`ensure_bke_agent.go:470-498`）：
   - `executePreCommand`: `chmod 777 /usr/local/bin/` → `systemctl stop bkeagent` → `rm -rf /usr/local/bin/bkeagent*` → `rm -rf /etc/openFuyao/bkeagent`
   - `executeStartCommand`: 上传 bkeagent 二进制到 `/usr/local/bin/` → `mv bkeagent_* bkeagent` → `chmod +x bkeagent` → 上传 kubeconfig → `systemctl daemon-reload` → `systemctl enable bkeagent` → `systemctl restart bkeagent`
   - `postCommand`: `chmod 755 /usr/local/bin/` → `chmod 755 /etc/systemd/system/`

**升级路径 — `EnsureAgentUpgrade` Phase**（DeclarativeInlineUpgradePhases）：

1. **版本比较**：`NeedExecuteWithVersionContext(upgrade.ComponentBKEAgent, ...)` 通过 VersionContext 判断是否需要升级
2. **SSH 推送升级**：`upgradeBKEAgentViaSSH()` → `agentssh.ParamsFromCluster()` 构建下载参数 → `agentssh.DiscoverArchs()` 发现节点架构 → `agentssh.PrepareStaging()` 下载制品到本地暂存 → `agentssh.SSHUpgrade()` SSH 推送
3. SSH 命令序列（`push_upgrade.go:123-184`）：
   - `upgradeHostFileFunc`: 上传 `bkeagent_linux_{arch}` 到 `/usr/local/bin/`
   - `executeUpgradePreCommand`: `systemctl stop bkeagent` → `cp bkeagent bkeagent.bak.{timestamp}`
   - `executeUpgradeStartCommand`: 上传 bkeagent.service → `mv bkeagent_* bkeagent` → `chmod +x bkeagent` → `systemctl daemon-reload` → `systemctl enable bkeagent` → `systemctl restart bkeagent`

**关键事实**：
- 制品名为 `bkeagent_linux_{arch}`（`push_upgrade.go:126`），不是 `bkeagent-{version}-linux-{arch}`
- 安装路径为 `/usr/local/bin/`（`push_upgrade.go:129`）
- 配置目录为 `/etc/openFuyao/bkeagent`（`push_upgrade.go:131`）
- bkeagent.service 从 HTTP 仓库下载（`artifacts.go:80-97`），非动态生成
- 安装和升级均为 SSH 推送模式，与 containerd（bkeagent 内置命令模式）不同

#### 11.4.2 ComponentVersion YAML 完整定义

```yaml
# bke-manifests/bkeagent/v2.6.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v2.6.0
spec:
  name: bkeagent
  type: binary
  version: v2.6.0

  binary:
    artifacts:
      - name: bkeagent
        url: "{{imageRegistry}}/bkeagent/{{version}}/bkeagent_linux_{{arch}}"
        checksum: "sha256:xyz789abc012..."
        installPath: "/usr/local/bin"
        executable: bkeagent

    configTemplates:
      - name: bkeagent.conf
        path: "/etc/openFuyao/bkeagent/bkeagent.conf"
        mode: "0644"
        owner: "root:root"
        content: |
          cluster_name: {{clusterName}}
          api_server: {{apiServer}}
          kubeconfig_path: /etc/openFuyao/bkeagent/kubeconfig
          log_level: info
          log_path: /var/log/bkeagent/bkeagent.log

      - name: tls.crt
        path: "/etc/openFuyao/bkeagent/tls.crt"
        mode: "0600"
        owner: "root:root"
        secretRef:
          name: bkeagent-tls
          namespace: "{{clusterNamespace}}"
          key: tls.crt

      - name: tls.key
        path: "/etc/openFuyao/bkeagent/tls.key"
        mode: "0600"
        owner: "root:root"
        secretRef:
          name: bkeagent-tls
          namespace: "{{clusterNamespace}}"
          key: tls.key

      - name: ca.crt
        path: "/etc/openFuyao/bkeagent/ca.crt"
        mode: "0644"
        owner: "root:root"
        secretRef:
          name: bkeagent-tls
          namespace: "{{clusterNamespace}}"
          key: ca.crt

      - name: kubeconfig
        path: "/etc/openFuyao/bkeagent/kubeconfig"
        mode: "0600"
        owner: "root:root"
        kubeconfigTemplate:
          clusterName: "{{clusterName}}"
          apiServer: "{{apiServer}}"
          caCertPath: "/etc/openFuyao/bkeagent/ca.crt"
          clientCertPath: "/etc/openFuyao/bkeagent/tls.crt"
          clientKeyPath: "/etc/openFuyao/bkeagent/tls.key"

    installScript: |
      #!/bin/bash
      set -e
      # 集群: {{clusterName}}, 节点: {{nodeIP}} ({{nodeRole}})
      # 版本: {{componentVersion}}, 操作: {{action}}

      # 1. 创建目录
      mkdir -p {{configPath}}
      mkdir -p {{logPath}}

      # 2. 停止旧服务
      systemctl stop bkeagent || true

      # 3. 备份旧版本 (仅升级时)
      {{if .isUpgrade}}
      cp {{artifact.bkeagent.installPath}}/bkeagent {{artifact.bkeagent.installPath}}/bkeagent.bak.$(date +%Y%m%d%H%M%S)
      {{end}}

      # 4. 安装新二进制
      install -m 0755 {{artifact.bkeagent.path}} {{artifact.bkeagent.installPath}}/bkeagent

      # 5. 安装 systemd service (由 ConfigRenderer 自动上传配置文件)
      # bkeagent.conf → {{configPath}}/bkeagent.conf
      # tls.crt → {{configPath}}/tls.crt
      # tls.key → {{configPath}}/tls.key
      # ca.crt → {{configPath}}/ca.crt
      # kubeconfig → {{configPath}}/kubeconfig

      cat > /etc/systemd/system/bkeagent.service << 'EOF'
      [Unit]
      Description=BKE Agent
      After=network.target

      [Service]
      ExecStart={{artifact.bkeagent.installPath}}/bkeagent --config {{configPath}}/bkeagent.conf
      Restart=always
      RestartSec=5

      [Install]
      WantedBy=multi-user.target
      EOF

      # 6. 启动并验证
      systemctl daemon-reload
      systemctl enable bkeagent
      systemctl start bkeagent
      sleep 2
      systemctl is-active bkeagent

    uninstallScript: |
      #!/bin/bash
      systemctl stop bkeagent || true
      systemctl disable bkeagent || true
      rm -f {{artifact.bkeagent.installPath}}/bkeagent
      rm -f /etc/systemd/system/bkeagent.service
      rm -rf {{configPath}}
      systemctl daemon-reload

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]
      - name: kylin
        versions: ["V10"]

    defaultConfigPath: "/etc/openFuyao/bkeagent"
    defaultLogPath: "/var/log/bkeagent"

    healthCheck:
      enabled: true
      timeout: "1m"
      interval: "3s"
      script: |
        #!/bin/bash
        systemctl is-active bkeagent

  compatibility:
    constraints:
      - component: kubernetes-master
        rule: ">=1.26.0"

  dependencies:
    - name: containerd
      phase: Upgrade

  upgradeStrategy:
    mode: Batch
    batchSize: 2
    timeout: "10m"
    failurePolicy: Continue
```

#### 11.4.3 字段映射表

| 旧硬编码逻辑 | 新 ComponentVersion 字段 | 说明 |
|-------------|------------------------|------|
| `Spec.BKEAgentVersion` | `spec.version` | 版本号 |
| 下载 URL 字符串拼接 | `binary.artifacts[].url`（含 `{{version}}/{{arch}}` 模板） | 制品地址声明式化 |
| `checksum` 硬编码常量 | `binary.artifacts[].checksum` | 校验和声明式化 |
| bkeagent.conf Go 代码拼接 | `binary.configTemplates[0].content` | 配置模板声明式化 |
| TLS 证书从 Secret 获取（硬编码 Secret 名） | `binary.configTemplates[1].secretRef` | Secret 引用声明式化 |
| kubeconfig 动态生成（硬编码路径） | `binary.configTemplates[4].kubeconfigTemplate` | kubeconfig 模板声明式化 |
| SSH 命令序列硬编码 | `binary.installScript` | 安装脚本声明式化 |
| `arch` 硬编码 | `{{arch}}` 模板变量 | 架构适配模板化 |
| 版本比较逻辑 | `VersionContext.NeedsUpgrade("bkeagent")` | 版本决策声明式化 |
| 固定逐节点滚动 | `upgradeStrategy.mode: Batch` | 升级策略改为分批 |
| 无失败策略 | `upgradeStrategy.failurePolicy: Continue` | 失败策略可配置 |
| 不支持卸载 | `binary.uninstallScript` | 卸载脚本声明式化 |

#### 11.4.4 行为等价性验证点

| 验证项 | 旧路径 (EnsureAgentUpgrade) | 新路径 (BinaryInstaller) | 验证方法 |
|--------|----------------------------|------------------------|---------|
| 二进制文件路径 | `/usr/local/bin/bkeagent` | `artifacts[0].installPath` = `/usr/local/bin` (per-artifact) | 检查远程节点文件路径一致 |
| bkeagent.conf 内容 | Go 代码拼接 | Go template 渲染 | `diff` 对比两份输出 |
| TLS 证书来源 | Secret `bkeagent-tls` 硬编码 | `configTemplates[1].secretRef.name` = `bkeagent-tls` | 验证证书内容一致 |
| kubeconfig 内容 | Go 代码动态生成 | `kubeconfigTemplate` 渲染 | `diff` 对比两份输出 |
| 安装执行顺序 | 停止→备份→安装→配置→启动 | installScript: 停止→备份→安装→配置→启动 | 对比 SSH 执行日志 |
| 版本比较逻辑 | `Status.BKEAgentVersion != Spec.BKEAgentVersion` | `VersionContext.NeedsUpgrade("bkeagent")` | 相同版本输入，决策结果一致 |
| 升级策略差异 | 固定逐节点滚动 | `Batch (batchSize=2)` | 3 节点集群验证分批执行（2+1） |

> **设计思路 - bkeagent 升级策略从 Rolling 改为 Batch 的原因**：bkeagent 是节点上的代理进程，短暂中断不影响集群可用性（Agent 重启期间节点上已有 Pod 继续运行）。使用 Batch 模式（batchSize=2）比 Rolling 更快完成升级，且每批结束后可检查剩余节点 Agent 状态，兼顾效率与安全性。containerd 是容器运行时，中断会导致节点上所有 Pod 重启，必须使用 Rolling 逐节点升级确保服务连续性。

### 11.5 迁移验证清单

| 验证项 | 验证方法 | 通过标准 |
|--------|---------|---------|
| **containerd 全新安装** | Feature Gate 开启，新建集群 | containerd 版本正确，服务运行中 |
| **containerd 升级** | Feature Gate 开启，v1.7.15→v1.7.18 | 逐节点滚动升级，服务不中断 |
| **containerd config.toml** | `diff` 旧路径输出 vs 新路径输出 | 内容一致（模板变量替换后） |
| **containerd.service** | `diff` 旧路径输出 vs 新路径输出 | 内容一致 |
| **containerd 版本跳过** | VersionContext 设置相同版本 | `NeedsUpgrade` 返回 false，跳过执行 |
| **EnsureNodesEnv scope 变更** | Feature Gate ON，检查 `K8sEnvInit` 命令参数 | scope 中无 `runtime` |
| **containerd 先于 EnsureNodesEnv** | DAG 拓扑顺序 | containerd 在 EnsureNodesEnv 前一批次 |
| **EnsureContainerdUpgrade 空跳过** | Feature Gate ON，触发升级 | `Execute()` 直接返回 nil，升级由 DAG binary 节点处理 |
| **bkeagent 全新安装** | Feature Gate 开启，新建集群 | bkeagent 版本正确，服务运行中 |
| **bkeagent 升级** | Feature Gate 开启，v2.5.0→v2.6.0 | 分批升级（2+1），服务正常 |
| **bkeagent 制品名** | 检查下载 URL | 包含 `bkeagent_linux_{arch}`，不是 `bkeagent-{version}-linux-{arch}` |
| **bkeagent.conf** | `diff` 旧路径输出 vs 新路径输出 | 内容一致 |
| **bkeagent TLS 证书** | 对比远程节点 tls.crt 内容 | 与 Secret 中数据一致 |
| **bkeagent kubeconfig** | `diff` 旧路径输出 vs 新路径输出 | 内容一致 |
| **Feature Gate 关闭回退** | 关闭 Feature Gate，执行安装/升级 | EnsureNodesEnv scope 含 `runtime`，EnsureContainerdUpgrade 走旧路径，行为不变 |
| **混合模式** | containerd 开启、bkeagent 关闭 | containerd 走新路径，bkeagent 走旧路径 |

---

## 12. 错误处理与恢复

### 12.1 错误处理流程图

**设计思路**：错误处理流程首先对错误进行分类（可重试/不可重试/部分失败），然后根据错误类型和 FailurePolicy 决定后续行为。可重试错误在重试次数未耗尽时自动重试，不可重试错误立即返回，部分失败根据策略决定是继续、终止还是回滚。

**关键设计点**：
- **错误分类**：区分可重试错误（网络超时等）和不可重试错误（配置错误等）
- **重试机制**：可重试错误在重试次数内自动重试，支持指数退避
- **FailurePolicy**：支持 FailFast（立即终止）、Continue（继续执行）、Rollback（回滚后继续）
- **状态记录**：所有错误都会记录到组件状态中，便于排查

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

## 13. 测试设计

### 13.1 单元测试

| 测试模块 | 测试场景 | 覆盖目标 |
|---------|---------|---------|
| **ArtifactDownloader** | HTTP 下载、Checksum 校验、缓存命中/未命中、架构适配 | >90% |
| **TemplateRenderer** | 8 类变量替换、条件渲染、自定义函数、错误处理 | >90% |
| **ConfigRenderer** | content 渲染、secretRef 获取、kubeconfig 生成 | >90% |
| **BinaryInstaller** | Install/Upgrade/Uninstall 完整流程、失败重试 | >85% |
| **HelmInstaller** | OCI/HTTP/本地 Chart 获取、Values 渲染、Install/Upgrade/Rollback | >85% |
| **BinaryComponentExecutor** | Rolling/Parallel/Batch 执行策略、FailurePolicy | >85% |

### 13.2 集成测试

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

### 13.3 E2E 测试

| 测试场景 | 集群规模 | 验证内容 |
|---------|---------|---------|
| **小规模安装** | 1 Master + 2 Worker | 完整安装流程，所有组件正常 |
| **中规模安装** | 3 Master + 10 Worker | 并行安装性能，无资源竞争 |
| **跨版本升级** | 3 Master + 5 Worker | v2.5.0 → v2.6.0 完整升级 |
| **升级失败恢复** | 3 Master + 3 Worker | 模拟节点失败，验证 Continue/Rollback 策略 |

---

## 14. 工作量与任务拆解

### 14.1 工作量评估

| 任务 | 预估工时 | 风险等级 | 依赖 |
|------|---------|---------|------|
| **BinaryInstaller 核心实现** | 5 人日 | 中 | 无 |
| **HelmInstaller 核心实现** | 5 人日 | 中 | 无 |
| **YAMLManifestExecutor 核心实现** | 5 人日 | 中 | 无 |
| **TemplateRenderer 实现** | 3 人日 | 低 | 无 |
| **ConfigRenderer 实现** | 3 人日 | 低 | TemplateRenderer |
| **ApplyStrategy 引擎实现** | 3 人日 | 中 | YAMLManifestExecutor |
| **Prune 裁剪功能实现** | 3 人日 | 中 | ApplyStrategy 引擎 |
| **PreInstallHooks 执行引擎** | 3 人日 | 中 | HelmInstaller |
| **Binary 健康检查实现** | 2 人日 | 中 | BinaryInstaller |
| **ComponentVersion CRD 扩展** | 3 人日 | 低 | 无 |
| **CRD v1alpha2 版本迁移** | 2 人日 | 中 | CRD 扩展 |
| **VersionContext 与 ExecutionContext 实现** | 3 人日 | 中 | 无 |
| **BinaryComponentExecutor 集成** | 3 人日 | 中 | BinaryInstaller |
| **HelmComponentExecutor 集成** | 3 人日 | 中 | HelmInstaller |
| **YAMLManifestExecutor 集成** | 2 人日 | 中 | YAMLManifestExecutor |
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
| **总计** | **92 人日 (约 4 人月)** | | |

### 14.2 Sprint 计划

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

### 14.3 里程碑

| 里程碑 | 时间 | 交付内容 | 验收标准 |
|--------|------|---------|---------|
| **M1: BinaryInstaller 完成** | 第2周末 | BinaryInstaller 核心功能 + 单元测试 | 单元测试覆盖率 >85% |
| **M2: HelmInstaller 完成** | 第4周末 | HelmInstaller 核心功能 + 单元测试 | 单元测试覆盖率 >85% |
| **M3: DAG 集成完成** | 第5周末 | Executor 集成 + ComponentVersion YAML | 集成测试通过 |
| **M4: Beta 发布** | 第6周末 | Feature Gate 灰度 + E2E 测试 | E2E 通过率 >95% |

---

## 15. 附录

### 15.1 参考文档

- KEP-5: 基于 ClusterVersion/ReleaseImage/UpgradePath 的声明式集群版本升级
- KEP-6: 基于 ReleaseImage 的二进制与 Helm 组件声明式管理方案
- ComponentVersion CRD 定义
- ReleaseImage CRD 定义
- DAG 调度器设计文档
- Helm Action API: https://pkg.go.dev/helm.sh/helm/v3/pkg/action

### 15.2 术语表

| 术语 | 定义 |
|------|------|
| **BinaryInstaller** | 负责二进制组件下载、渲染、安装的安装器 |
| **HelmInstaller** | 负责 Helm Chart 获取、渲染、部署的安装器 |
| **configTemplates** | 配置文件模板系统，支持 Go template/Secret/kubeconfig |
| **installScript** | 安装脚本模板，支持 8 类 50+ 变量和条件渲染 |
| **Artifact** | 二进制制品，包含 URL、Checksum、安装路径等信息 |
| **ComponentVersion** | 组件版本 CRD，定义组件的类型、配置、依赖等 |
| **ReleaseImage** | 发布版本清单 CRD，定义安装和升级的组件列表 |
| **DAG** | 有向无环图，用于表示组件依赖关系和执行顺序 |
| **Feature Gate** | 功能开关，用于控制新功能的启用/禁用 |
| **Rolling Update** | 滚动更新，逐节点执行升级操作 |

---

## 16. 安装与升级样例

### 16.1 安装样例

**场景**：用户新建 BKECluster，desiredVersion 指向 ReleaseImage v2.6.0，需安装 containerd（binary）、coredns（helm）、openfuyao-core（yaml）、kubernetes-master/worker（inline）四种类型组件。

#### 16.1.1 ComponentVersion YAML 样例

```yaml
# bke-manifests/containerd/v1.7.18/component.yaml (简化示例，完整定义见 11.3.2)
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.18
spec:
  name: containerd
  type: binary
  version: v1.7.18
  binary:
    artifacts:
      - name: containerd
        url: "https://release-repo.openfuyao.cn/binaries/containerd/{{version}}/containerd-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:abc123def456..."
        installPath: "/usr/local"
        executable: containerd
    installScript: |
      #!/bin/bash
      set -e
      systemctl stop containerd || true
      tar -xzf {{artifact.containerd.path}} -C {{artifact.containerd.installPath}}
      chmod +x {{artifact.containerd.installPath}}/bin/containerd
      systemctl daemon-reload
      systemctl enable containerd
      systemctl start containerd
      {{artifact.containerd.installPath}}/bin/containerd --version
    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]
    defaultConfigPath: "/etc/containerd"
    defaultLogPath: "/var/log/containerd"
    defaultDataPath: "/var/lib/containerd"
  upgradeStrategy:
    mode: Rolling
    failurePolicy: FailFast
    timeout: "10m"
```

```yaml
# bke-manifests/coredns/v1.11.1/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.11.1
spec:
  name: coredns
  type: helm
  version: v1.11.1
  helm:
    chart:
      oci:
        repository: "registry.openfuyao.cn/charts/coredns"
        tag: "v1.11.1"
      checksum: "sha256:ghi789jkl012..."
    namespace: kube-system
    releaseName: coredns
    values:
      image:
        repository: "registry.openfuyao.cn/coredns/coredns"
        tag: "{{componentVersion}}"
      replicaCount: 2
      resources:
        limits:
          cpu: "100m"
          memory: "128Mi"
    strategy:
      mode: Upgrade
      wait: true
      waitTimeout: "5m"
      atomic: true
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          namespace: kube-system
          labelSelector: "k8s-app=kube-dns"
          minReady: 1
  upgradeStrategy:
    mode: Parallel
    failurePolicy: FailFast
    timeout: "10m"
```

```yaml
# bke-manifests/openfuyao-core/v26.03/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: openfuyao-core-v26.03
spec:
  name: openfuyao-core
  type: yaml
  version: v26.03
  yaml:
    manifests:
      - url: "https://release-repo.openfuyao.cn/manifests/openfuyao-core/v26.03/crds.yaml"
        checksum: "sha256:mno345pqr678..."
      - url: "https://release-repo.openfuyao.cn/manifests/openfuyao-core/v26.03/deployment.yaml"
        checksum: "sha256:stu901vwx234..."
    namespace: openfuyao-system
    applyStrategy: ServerSideApply
    prune: true
    pruneLabelSelector:
      app.kubernetes.io/managed-by: openfuyao-core
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

#### 16.1.2 ReleaseImage YAML 样例

```yaml
# releaseimage-v2.6.0.yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: bke-v2.6.0
spec:
  version: v2.6.0
  install:
    components:
      - name: containerd
        version: v1.7.18
      - name: bkeagent
        version: v2.6.0
      - name: coredns
        version: v1.11.1
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
```

#### 16.1.3 安装执行流程

```
用户创建 BKECluster (desiredVersion: v2.6.0)
  │
  ▼
BKEClusterReconciler.Reconcile()
  │
  ├─ 1. 解析 ReleaseImage v2.6.0
  │     releaseImage.GetInstallComponents()
  │     → [containerd, bkeagent, coredns, openfuyao-core, kubernetes-master, kubernetes-worker]
  │
  ├─ 2. 加载 ComponentVersion
  │     manifestStore.GetComponentManifests() 逐个加载组件定义
  │     → containerd: type=binary, artifacts=[...], installScript=...
  │     → coredns: type=helm, chart=oci://..., values={...}
  │     → openfuyao-core: type=yaml, manifests=[...], subComponents=[...]
  │
  ├─ 3. 构建安装 DAG
  │     BuildInstallDAG(releaseImage)
  │
  │     DAG 拓扑批次:
  │     Batch 0: [finalizer, paused, manage, delete, dryrun]  (CommonPhases, inline)
  │     Batch 1: [containerd, bkeagent]                       (binary, 并行)
  │     Batch 2: [kubernetes-master]                          (inline, 依赖 containerd)
  │     Batch 3: [kubernetes-worker]                          (inline, 依赖 kubernetes-master)
  │     Batch 4: [coredns, openfuyao-core]                   (helm/yaml, 依赖 kubernetes-master)
  │
  ├─ 4. Scheduler.ExecuteDAG(ctx, dag)
  │     │
  │     ├─ Batch 0: CommonPhases (inline executor)
  │     │   finalizer → paused → manage → delete → dryrun
  │     │
  │     ├─ Batch 1: Binary 组件 (并行)
  │     │   ├─ containerd: BinaryComponentExecutor
  │     │   │   ├─ VersionContext.HasCurrent("containerd") = false → Action = Install
  │     │   │   ├─ NodeProvider.GetNodes() → 3 个节点
  │     │   │   ├─ Rolling 逐节点:
  │     │   │   │   node1: 下载制品 → 渲染脚本 → SSH 执行安装 → ✅
  │     │   │   │   node2: 下载制品 → 渲染脚本 → SSH 执行安装 → ✅
  │     │   │   │   node3: 下载制品 → 渲染脚本 → SSH 执行安装 → ✅
  │     │   └─ bkeagent: BinaryComponentExecutor (同上)
  │     │
  │     ├─ Batch 2: kubernetes-master (inline executor)
  │     │   └─ InlineRunner.Execute(handler="EnsureMasterInit") → kubeadm init
  │     │
  │     ├─ Batch 3: kubernetes-worker (inline executor)
  │     │   └─ InlineRunner.Execute(handler="EnsureWorkerJoin") → kubeadm join
  │     │
  │     └─ Batch 4: Helm + YAML 组件 (并行)
  │         ├─ coredns: HelmComponentExecutor
  │         │   ├─ VersionContext.HasCurrent("coredns") = false → Action = Install
  │         │   ├─ 拉取 Chart (OCI Registry)
  │         │   ├─ 渲染 Values (模板变量)
  │         │   ├─ helm install --atomic --wait
  │         │   └─ HealthCheck: PodReady (kube-dns) → ✅
  │         │
  │         └─ openfuyao-core: YAMLManifestExecutor
  │             ├─ VersionContext.HasCurrent("openfuyao-core") = false → 需要安装
  │             ├─ resolveManifests(): 从 URL 下载 crds.yaml + deployment.yaml
  │             ├─ parseYAMLDocuments(): 解析多文档 YAML
  │             ├─ ApplyWithStrategy(ServerSideApply): 应用到目标集群
  │             └─ PruneResources(): 无废弃资源 (首次安装)
  │
  ├─ 5. 健康检查
  │     PodReady + EndpointReady 检查所有组件
  │
  └─ 6. 更新 BKECluster.Status
        phase: Ready
        conditions: [{type: Ready, status: True}]
```

### 16.2 升级样例

**场景**：集群从 v2.5.0 升级到 v2.6.0，containerd/bkeagent/coredns/openfuyao-core 版本变更，kubernetes-master/worker 版本不变。

#### 16.2.1 版本变更对比

| 组件 | 当前版本 | 目标版本 | 类型 | 升级策略 | FailurePolicy |
|------|---------|---------|------|---------|---------------|
| containerd | v1.7.15 | v1.7.18 | binary | Rolling | FailFast |
| bkeagent | v2.5.0 | v2.6.0 | binary | Batch (batchSize=2) | Continue |
| coredns | v1.10.1 | v1.11.1 | helm | Parallel | FailFast |
| openfuyao-core | v26.01 | v26.03 | yaml | Parallel | FailFast |
| kubernetes-master | v1.29.0 | v1.29.0 | inline | — | 不升级 |
| kubernetes-worker | v1.29.0 | v1.29.0 | inline | — | 不升级 |

#### 16.2.2 ReleaseImage YAML 样例

```yaml
# releaseimage-v2.6.0.yaml (升级场景)
apiVersion: config.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: bke-v2.6.0
spec:
  version: v2.6.0
  upgrade:
    components:
      - name: containerd
        version: v1.7.18
      - name: bkeagent
        version: v2.6.0
      - name: coredns
        version: v1.11.1
      - name: openfuyao-core
        version: v26.03
```

#### 16.2.3 升级执行流程

```
用户修改 ClusterVersion desiredVersion: v2.5.0 → v2.6.0
  │
  ▼
ClusterVersionReconciler.Reconcile()
  │
  ├─ 1. 解析目标 ReleaseImage v2.6.0
  │     releaseImage.GetUpgradeComponents()
  │     → [containerd, bkeagent, coredns, openfuyao-core]
  │
  ├─ 2. 解析当前 ReleaseImage v2.5.0
  │     currentReleaseImage.GetUpgradeComponents()
  │     → [containerd:v1.7.15, bkeagent:v2.5.0, coredns:v1.10.1, openfuyao-core:v26.01]
  │
  ├─ 3. 构建 VersionContext (版本对比)
  │     vc.SetCurrent("containerd", "v1.7.15")
  │     vc.SetTarget("containerd", "v1.7.18")
  │     vc.SetCurrent("bkeagent", "v2.5.0")
  │     vc.SetTarget("bkeagent", "v2.6.0")
  │     ... (每个组件设置 current/target)
  │
  │     VersionContext 决策结果:
  │     containerd:       HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     bkeagent:         HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     coredns:          HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     openfuyao-core:   HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     kubernetes-master: HasCurrent=true, NeedsUpgrade=false → Skip
  │
  ├─ 4. 构建升级 DAG
  │     BuildUpgradeDAG(releaseImage)
  │
  │     DAG 拓扑批次:
  │     Batch 0: [provider]                                    (manifest, 前置)
  │     Batch 1: [containerd, bkeagent]                        (binary, 并行)
  │     Batch 2: [coredns, openfuyao-core]                     (helm/yaml, 并行)
  │
  ├─ 5. Scheduler.ExecuteDAG(ctx, dag, versionContext)
  │     │
  │     ├─ Batch 0: provider (manifest executor)
  │     │   └─ ManifestApplier.ApplyComponent() → 更新 provider 自身
  │     │
  │     ├─ Batch 1: Binary 组件升级 (并行)
  │     │   ├─ containerd: BinaryComponentExecutor
  │     │   │   ├─ VersionContext.NeedsUpgrade("containerd") = true
  │     │   │   ├─ VersionContext.HasCurrent("containerd") = true → Action=Upgrade
  │     │   │   ├─ NodeProvider.GetNodes() → 3 个节点
  │     │   │   ├─ Rolling 逐节点升级:
  │     │   │   │   node1: 停止 containerd → 备份 → 安装 v1.7.18 → 启动 → ✅
  │     │   │   │   node2: 停止 containerd → 备份 → 安装 v1.7.18 → 启动 → ✅
  │     │   │   │   node3: 停止 containerd → 备份 → 安装 v1.7.18 → 启动 → ✅
  │     │   │   └─ FailurePolicy=FailFast: 任一节点失败则整体终止
  │     │   │
  │     │   └─ bkeagent: BinaryComponentExecutor
  │     │       ├─ VersionContext.NeedsUpgrade("bkeagent") = true
  │     │       ├─ VersionContext.HasCurrent("bkeagent") = true → Action=Upgrade
  │     │       ├─ NodeProvider.GetNodes() → 3 个节点
  │     │       ├─ Batch 升级 (batchSize=2):
  │     │       │   Batch 1: node1 → ✅, node2 → ✅  (检查集群健康)
  │     │       │   Batch 2: node3 → ✅
  │     │       └─ FailurePolicy=Continue: node3 失败时记录警告，继续执行
  │     │
  │     └─ Batch 2: Helm + YAML 组件升级 (并行)
  │         ├─ coredns: HelmComponentExecutor
  │         │   ├─ VersionContext.NeedsUpgrade("coredns") = true
  │         │   ├─ VersionContext.HasCurrent("coredns") = true → Action=Upgrade
  │         │   ├─ 拉取新 Chart v1.11.1
  │         │   ├─ 渲染 Values
  │         │   ├─ helm upgrade --atomic --wait
  │         │   │   ├─ 成功 → Release 更新到 v1.11.1
  │         │   │   └─ 失败 → helm 自动回滚到 v1.10.1 (atomic)
  │         │   └─ HealthCheck: PodReady (kube-dns) → ✅
  │         │
  │         └─ openfuyao-core: YAMLManifestExecutor
  │             ├─ VersionContext.NeedsUpgrade("openfuyao-core") = true
  │             ├─ resolveManifests(): 下载 v26.03 清单
  │             ├─ ApplyWithStrategy(ServerSideApply): 增量更新
  │             │   └─ SSA 仅更新变更字段，保留其他管理者字段
  │             └─ PruneResources():
  │                 └─ 删除标签匹配但不在 v26.03 清单中的废弃资源 → ✅
  │
  ├─ 6. 健康检查
  │     所有组件 PodReady + EndpointReady
  │
  └─ 7. 更新 BKECluster.Status
        phase: Ready
        conditions: [{type: Upgraded, status: True}]
        versions:
          containerd: v1.7.18
          bkeagent: v2.6.0
          coredns: v1.11.1
          openfuyao-core: v26.03
```

### 16.3 关键设计点说明

**ComponentVersion YAML 存放路径约定**：

```
bke-manifests/
├── containerd/v1.7.18/component.yaml     ← type: binary
├── bkeagent/v2.6.0/component.yaml        ← type: binary
├── coredns/v1.11.1/component.yaml        ← type: helm
├── openfuyao-core/v26.03/component.yaml  ← type: yaml (含 subComponents)
└── kubernetes-master/v1.29.0/            ← type: inline (无需 YAML, 由 inline handler 定义)
```

**ReleaseImage install vs upgrade components 区别**：
- `spec.install.components`：新集群安装时使用，包含所有组件（含 CommonPhases）
- `spec.upgrade.components`：升级时使用，仅包含需要升级的组件，未列出的组件保持不变

**VersionContext 在升级流程中的决策时机**：

| 决策点 | VersionContext 方法 | 判定结果 | 后续动作 |
|--------|-------------------|---------|---------|
| DAG 构建时 | `NeedsUpgrade(name)` | false | 组件不加入 DAG，跳过执行 |
| Executor 执行时 | `NeedsUpgrade(name)` | false | 组件已在目标版本，返回 nil 跳过 |
| Executor 执行时 | `HasCurrent(name)` | true | Action = Upgrade |
| Executor 执行时 | `HasCurrent(name)` | false | Action = Install |

**FailurePolicy 在不同场景下的行为**：

| 场景 | FailurePolicy | 行为 |
|------|---------------|------|
| Rolling 模式单节点失败 | FailFast | 立即返回错误，终止整个组件升级 |
| Rolling 模式单节点失败 | Continue | 记录警告日志，继续升级下一个节点 |
| Rolling 模式单节点失败 | Rollback | 对该节点执行 UninstallScript，继续下一个节点 |
| Batch 模式单批失败 | FailFast | 终止后续批次，已升级批次保留 |
| Helm `--atomic` 失败 | — | Helm SDK 自动回滚到上一个 Release |

---

**文档版本**: v1.0  
**维护者**: openFuyao Team

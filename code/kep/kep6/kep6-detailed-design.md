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
    
    // Inline 类型配置 (type=inline 时必填)
    Inline *InlineSpec `json:"inline,omitempty"`
    
    // 兼容性约束
    Compatibility CompatibilitySpec `json:"compatibility,omitempty"`
    
    // 依赖关系
    Dependencies []Dependency `json:"dependencies,omitempty"`
    
    // 升级策略
    UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
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
    
    // 默认安装路径
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
    tmplCtx := opts.TemplateCtx  // 复用 DAG 调度器传递的 TemplateContext
    
    // 设计说明: 不在此处获取架构和操作系统信息
    // 架构和操作系统信息由安装脚本在运行时自检测 (通过 uname -m 和 /etc/os-release)
    // 这样做的好处:
    // 1. 简化 NodeProvider，无需 SSH 预检测
    // 2. 安装脚本可在任意节点上独立运行
    // 3. 减少模板变量，保持模板简洁
    
    // 1. 下载二进制制品 (带缓存)
    // 制品 URL 中的架构占位符由脚本在运行时替换
    artifacts, err := i.downloadArtifacts(ctx, binary)
    if err != nil {
        return fmt.Errorf("failed to download artifacts: %w", err)
    }
    
    // 2. 填充 TemplateContext 的制品信息
    tmplCtx.Artifacts = make(map[string]*ArtifactInfo)
    for name, art := range artifacts {
        tmplCtx.Artifacts[name] = &ArtifactInfo{
            Name:     art.Name,
            Path:     art.Path,
            URL:      art.URL,
            Checksum: art.Checksum,
            Filename: art.Filename,
        }
    }
    
    // 3. 填充自定义变量
    tmplCtx.Variables = binary.Variables
    
    // 4. 渲染安装脚本 (使用完整的 TemplateContext)
    script, err := i.renderer.RenderScript(binary.InstallScript, tmplCtx)
    if err != nil {
        return fmt.Errorf("failed to render install script: %w", err)
    }
    
    // 5. 渲染配置文件模板
    configs, err := i.renderConfigTemplates(binary.ConfigTemplates, tmplCtx)
    if err != nil {
        return fmt.Errorf("failed to render config templates: %w", err)
    }
    
    // 6. SSH 执行安装
    switch opts.Action {
    case BinaryActionInstall, BinaryActionUpgrade:
        return i.executeInstall(ctx, tmplCtx.NodeIP, script, artifacts, configs)
    case BinaryActionUninstall:
        return i.executeUninstall(ctx, tmplCtx.NodeIP, binary.UninstallScript, tmplCtx)
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

**设计原则：脚本自检测，模板不感知 OS/Arch**：
- **为什么不在模板中注入 OS/Arch 信息**：二进制安装脚本本身是 OS 无关的（systemctl、tar、chmod 等命令在所有主流 Linux 通用），只有极少数场景需要 OS 检测（如包管理器安装依赖）。将 OS 检测逻辑放在脚本内部自检测，而非模板渲染时注入，有以下好处：
  1. **简化 Node 结构体**：NodeProvider 只需提供 IP/Hostname/Role 等基础信息，无需 SSH 预检测
  2. **脚本可移植**：安装脚本可在任意节点上独立运行，不依赖外部注入的环境信息
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
    // 注意：不包含 Arch/OS/OSVersion，这些信息在脚本内自检测
    NodeIP            string
    NodeHostname      string
    NodeRole          string
    
    // 新增：版本信息
    ComponentVersion          string
    ComponentPreviousVersion  string
    
    // 新增：制品信息 (Binary 组件需要)
    Artifacts         map[string]*ArtifactInfo
    
    // 新增：镜像仓库
    ImageRegistry     string
    ImagePullSecret   string
    
    // 新增：自定义变量
    Variables         map[string]string
}

type ArtifactInfo struct {
    Name     string
    Path     string
    URL      string
    Checksum string
    Filename string
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

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{installPath}}` | 默认安装路径 | 从 ComponentVersion 获取 | `/usr/local/bin` |
| `{{configPath}}` | 配置路径 | 从 ComponentVersion 获取 | `/etc/containerd` |
| `{{logPath}}` | 日志路径 | 从 ComponentVersion 获取 | `/var/log/containerd` |
| `{{dataPath}}` | 数据路径 | 从 ComponentVersion 获取 | `/var/lib/containerd` |
| `{{binPath}}` | 二进制路径 | 从 ComponentVersion 获取 | `/usr/local/bin` |

#### 7. 操作类型变量 (Action Variables)

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{action}}` | 操作类型 | 运行时确定 | `install` / `upgrade` / `uninstall` |
| `{{isUpgrade}}` | 是否升级 | 运行时确定 | `true` / `false` |
| `{{isInstall}}` | 是否安装 | 运行时确定 | `true` / `false` |

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
                    │  注意: 不包含 NodeArch/NodeOS/       │
                    │  NodeOSVersion，这些信息在安装脚本   │
                    │  中自检测                            │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  2. 构建制品数据                     │
                    │  downloadArtifacts(ctx, binary)      │
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

**设计思路**：DAG 调度器在执行组件前构建 TemplateContext，包含集群信息、节点基础信息、版本信息等。对于 Binary 组件，还需要在 TemplateRenderer 中填充制品信息。注意：TemplateContext 不包含 NodeArch/NodeOS/NodeOSVersion，这些信息在安装脚本中自检测。

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

### 8.2 DAG 调度流程图

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

**设计思路**：DAG 调度器在执行组件时传递 `ExecutionContext`，该上下文完全独立于 phaseframe 包，包含集群信息、节点提供者、日志记录器等。通过 `NodeProvider` 接口抽象节点获取逻辑，实现与 phaseframe 的完全解耦。

**解耦设计**：
- **ExecutionContext**：替代 `phaseframe.PhaseContext`，独立定义组件执行所需上下文
- **NodeProvider**：抽象节点获取逻辑，替代 `phaseCtx.NodeFetcher()`
- **TemplateContext**：复用 `manifest.TemplateContext`，统一模板渲染

#### 8.3.1 ExecutionContext 定义

```go
// pkg/dagexec/context.go

// VersionContext 携带组件版本事实，Executor 据此自主决定操作类型
//
// 设计思路 - 为什么用 VersionContext 而非 IsUpgrade bool 或 OperationType 枚举：
// 1. 声明式协调：Kubernetes 控制器应基于"当前状态 vs 期望状态"自主决定操作，
//    而非由调用方显式下达操作指令。VersionContext 提供版本事实（已安装版本、目标版本），
//    Executor 根据 HasCurrent/HasTarget/NeedsUpgrade 自主推断 Install/Upgrade/Skip。
// 2. 避免概念重复：BinaryAction (Install/Upgrade/Uninstall) 和 HelmAction (Install/Upgrade/
//    Rollback) 已在各自 Executor 中定义。ExecutionContext 中再放 OperationType 枚举会造成
//    两套枚举需要映射，增加维护负担。
// 3. 扩展性：后续支持 Rollback 时，只需在 VersionContext 中新增版本历史记录
//    (previousVersions map)，Executor 即可推断 Rollback 操作，无需修改 ExecutionContext 接口。
// 4. 与实际代码一致：当前 PhaseContext 已使用 VersionContext 进行版本判断，
//    设计文档与实现保持一致。
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

**设计思路**：NodeProvider 接口抽象节点获取逻辑，替代原来对 phaseframe.NodeFetcher 的依赖。Node 结构体仅包含基础信息（Name/IP/Hostname/Role），不包含 Arch/OS/OSVersion。架构和操作系统信息由安装脚本在运行时自检测，而非在 NodeProvider 中预检测。

**设计原则**：
- **最小化 Node 信息**：NodeProvider 只提供节点的基础标识信息，不执行 SSH 检测
- **脚本自检测**：架构和操作系统信息由安装脚本通过 `uname -m` 和 `/etc/os-release` 自检测
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
// Arch/OS/OSVersion 由安装脚本在运行时自检测
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
        // 注意：不包含 Arch/OS/OSVersion，这些信息在安装脚本中自检测
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

**设计思路**：完整安装流程从用户创建 BKECluster 开始，经过 ReleaseImage 解析、ComponentVersion 加载、DAG 构建、DAG 执行，最终完成所有组件的安装。流程中 Binary/Helm/Inline 三种类型的组件通过各自的 Executor 并行执行，最终通过健康检查确认安装成功。

**关键设计点**：
- **声明式安装**：通过 ReleaseImage 声明需要安装的组件列表
- **DAG 调度**：根据组件依赖关系构建 DAG，按拓扑顺序执行
- **多类型支持**：Binary（containerd/bkeagent）、Helm（coredns/kube-proxy）、YAML（openfuyao-core）、Inline（kubernetes）
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
                     │  执行 openfuyao-core 安装            │
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

**文档版本**: v1.0  
**创建日期**: 2026-06-12  
**最后更新**: 2026-06-12  
**维护者**: openFuyao Team

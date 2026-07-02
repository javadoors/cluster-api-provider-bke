# KEP-6 详细设计文档：基于 ReleaseImage 的二进制与 Helm 组件声明式管理方案

**文档版本**: v1.2  
**状态**: Draft  
**依赖**: KEP-6 提案文档

---

## 目录

1. [概述](#1-概述)
2. [整体架构设计](#2-整体架构设计)
3. [ComponentVersion CRD 详细设计](#3-componentversion-crd-详细设计)
   - 3.1 ComponentVersion 类型定义
   - 3.2 Binary 类型字段定义
   - 3.3 YAML 类型字段定义
   - 3.4 Helm 类型字段定义
    - 3.5 Inline 类型字段定义
    - 3.6 Selector 类型字段定义
    - 3.7 CRD YAML 定义
    - 3.8 CRD 版本迁移设计
4. [BinaryInstaller 详细设计](#4-binaryinstaller-详细设计)
    - 4.5 ConfigRenderer 详细设计 (含三种渲染模式)
    - 4.6 ConfigTemplateSpec forEach 动态多文件生成
5. [HelmInstaller 详细设计](#5-helminstaller-详细设计)
   - 5.4 健康检查
6. [YamlInstaller 详细设计](#6-yamlinstaller-详细设计)
    - 6.3 核心接口定义
    - 6.4 清单下载与缓存
    - 6.5 健康检查
    - 6.6 YAML Uninstall 流程
7. [HealthCheck 共享包设计](#7-healthcheck-共享包设计)
    - 7.1 类型定义
8. [模板变量系统与 TemplateContext 详细设计](#8-模板变量系统与-templatecontext-详细设计)
9. [DAG 集成详细设计](#9-dag-集成详细设计)
    - 9.5 状态模型、幂等性与兼容性设计
10. [完整安装流程详细设计](#10-完整安装流程详细设计)
11. [完整升级流程详细设计](#11-完整升级流程详细设计)
12. [迁移策略详细设计](#12-迁移策略详细设计)
    - 12.5 BKEAgentSwitch 独立组件设计
13. [错误处理与恢复](#13-错误处理与恢复)
14. [测试设计](#14-测试设计)
15. [工作量与任务拆解](#15-工作量与任务拆解)
16. [附录](#16-附录)
17. [安装与升级样例](./kep6-install-upgrade-samples.md) _(独立文档)_

---

## 1. 概述

### 1.1 设计目标

本详细设计文档基于 KEP-6 提案，提供完整的实现方案，包括：

- **BinaryInstaller**: 二进制组件的下载、渲染、安装
- **HelmInstaller**: Helm 组件的 Chart 获取、渲染、部署
- **TemplateRenderer**: BinaryInstaller 内置的脚本与配置模板渲染引擎 (Go template)
- **ConfigRenderer**: BinaryInstaller 内置的配置文件模板渲染引擎 (支持 Content/Secret/Kubeconfig 三种模式)
- **DAG 集成**: 执行器注册与调度流程

### 1.2 设计范围

| 范围 | 说明 |
|------|------|
| CRD 扩展 | ComponentVersion 新增 binary/helm/selector 类型的完整字段定义 |
| 核心安装器 | BinaryInstaller、HelmInstaller、YamlInstaller 的完整实现 |
| 渲染引擎 | BinaryInstaller 内置 TemplateRenderer (脚本渲染) + ConfigRenderer (配置渲染) 的完整实现 |
| DAG 集成 | BinaryComponentExecutor、HelmComponentExecutor |
| 迁移策略 | Feature Gate、向后兼容、灰度发布 |

### 1.3 设计约束

| 约束 | 说明 |
|------|------|
| 向后兼容 | 必须支持从现有硬编码 Phase 平滑迁移 |
| 离线环境 | 二进制制品和 Helm Chart 支持本地缓存 |
| 架构支持 | 必须支持 amd64 和 arm64 架构 |
| 操作系统支持 | 必须支持 CentOS 7/8、Ubuntu 20.04/22.04 |
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

## 2. 整体架构设计

### 2.1 系统架构图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              BKECluster                                         │
│  spec.desiredVersion: v2.6.0                                                    │
└────────────────────────────────┬────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            ReleaseImage                                         │
│  spec.install.components: [containerd/v1.7.18, bkeagent/v2.6.0, ...]            │
│  spec.upgrade.components: [containerd/v1.7.18, bkeagent/v2.6.0, ...]            │
└────────────────────────────────┬────────────────────────────────────────────────┘
                                 │ 按 (name, version) 定位
                                 ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         bke-manifests (ComponentVersion)                        │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ containerd/v1.7.18/component.yaml          ← type: binary               │    │
│  │   ├── binary.artifacts: [containerd]                                    │    │
│  │   ├── binary.configTemplates: [config.toml, service]                    │    │
│  │   └── binary.installScript: (带 50+ 模板变量)                            │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ bkeagent/v2.6.0/component.yaml             ← type: binary               │    │
│  │   ├── binary.artifacts: [bkeagent]                                      │    │
│  │   ├── binary.configTemplates: [bkeagent.conf, kubeconfig]               │    │
│  │   └── binary.installScript: (带完整模板变量)                             │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │ coredns/v1.11.1/component.yaml             ← type: helm                 │    │
│  │   ├── helm.chart.oci: registry/charts/coredns                           │    │
│  │   ├── helm.values: (带模板变量)                                          │    │
│  │   └── helm.healthCheck: PodReady + EndpointReady                        │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
└────────────────────────────────┬────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            DAG Scheduler                                        │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                        Component Executor Factory                       │    │
│  │                                                                         │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌────────────┐   │    │
│  │  │   Binary     │  │    Helm      │  │   Inline     │  │   YAML     │   │    │
│  │  │  Component   │  │   Component  │  │   Component  │  │  Component │   │    │
│  │  │  Executor    │  │   Executor   │  │   Executor   │  │  Executor  │   │    │
│  │  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └─────┬──────┘   │    │
│  │         │                 │                 │                │          │    │
│  └─────────┼─────────────────┼─────────────────┼────────────────┼──────────┘    │
│            │                 │                 │                │               │
│            ▼                 ▼                 ▼                ▼               │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                          Installer Layer                                │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                      BinaryInstaller                            │    │    │
│  │  │  ┌────────────────┐  ┌────────────────┐  ┌─────────────────┐    │    │    │
│  │  │  │   Artifact     │  │   Script       │  │    Config       │    │    │    │
│  │  │  │  Downloader    │  │   Renderer     │  │   Renderer      │    │    │    │
│  │  │  └────────┬───────┘  └────────┬───────┘  └────────┬────────┘    │    │    │
│  │  │           │                   │                   │             │    │    │
│  │  │           └───────────────────┼───────────────────┘             │    │    │
│  │  │                               ▼                                 │    │    │
│  │  │                      ┌────────────────┐                         │    │    │
│  │  │                      │  SSH Executor  │                         │    │    │
│  │  │                      └────────────────┘                         │    │    │
│  │  │  ┌─────────────────────────────────────────────────────────┐    │    │    │
│  │  │  │              HealthChecker (SSH 脚本执行)                │    │    │    │
│  │  │  └─────────────────────────────────────────────────────────┘    │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                       HelmInstaller                             │    │    │
│  │  │  ┌────────────────┐  ┌────────────────┐  ┌─────────────────┐    │    │    │
│  │  │  │    Chart       │  │    Values      │  │    Helm         │    │    │    │
│  │  │  │   Fetcher      │  │   Renderer     │  │  Action Exec    │    │    │    │
│  │  │  └────────┬───────┘  └────────┬───────┘  └────────┬────────┘    │    │    │
│  │  │           │                   │                   │             │    │    │
│  │  │           └───────────────────┼───────────────────┘             │    │    │
│  │  │                               ▼                                 │    │    │
│  │  │                      ┌────────────────┐                         │    │    │
│  │  │                      │ HealthChecker  │                         │    │    │
│  │  │                      └────────────────┘                         │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                     YamlInstaller                               │    │    │
│  │  │  ┌────────────────┐  ┌────────────────┐  ┌─────────────────┐    │    │    │
│  │  │  │ManifestDownload│  │  YAML Parser   │  │  K8s Client     │    │    │    │
│  │  │  │(清单获取/缓存)  │  │  (解析/分组)    │  │  (Apply/Delete) │    │    │    │
│  │  │  └────────────────┘  └────────────────┘  └─────────────────┘    │    │    │
│  │  │  ┌─────────────────────────────────────────────────────────┐    │    │    │
│  │  │  │              HealthChecker (Pod/Endpoint/Custom)        │    │    │    │
│  │  │  └─────────────────────────────────────────────────────────┘    │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 组件交互关系

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              组件交互关系图                                      │
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
               ┌─────────────┼──────────────┌──────────────┐
               │             │              │              │
               ▼             ▼              ▼              ▼
       ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
       │   Binary     │ │    Helm      │ │   Inline     │ │    YAML      │
       │  Component   │ │   Component  │ │   Component  │ │   Component  │
       │  Executor    │ │   Executor   │ │   Executor   │ │   Executor   │
       └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
              │                │                │                │
              ▼                ▼                ▼                ▼
      ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
      │   Binary     │ │    Helm      │ │  Component   │ │    Yaml      │
      │  Installer   │ │  Installer   │ │  Factory     │ │  Installer   │
      └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
             │                │                │                │
             ▼                ▼                ▼                ▼
     ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
     │  SSH Client  │ │ Helm SDK     │ │ Phase        │ │ K8s Client   │
     │  (bkessh)    │ │ (helm/v3)    │ │ Execute()    │ │ (Apply)      │
     └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
```

## 3. ComponentVersion CRD 详细设计

### 3.1 ComponentVersion 类型定义

> **复用说明**：现有 `api/v1alpha1/componentversion_types.go` 已定义 `ComponentVersionSpec`/`ComponentType`/`InlineSpec`/`SubComponent`/`CompatibilitySpec`/`Constraint`/`Dependency`/`UpgradeStrategySpec`/`ResourceSpec` 等类型。本节中这些类型为**复用现有**（仅新增 `Binary`/`Helm`/`YAML` 三个字段及对应 `*Spec` 类型），下文以「✅复用」「🆕新增」标注。路径修正：原设计写 `pkg/api/v1alpha1/...` 有误，实际为 `api/v1alpha1/...`（无 `pkg/` 前缀）。

```go
// api/v1alpha1/componentversion_types.go

// ComponentVersionSpec 定义组件版本规格 ✅复用现有，仅新增 Binary/Helm/YAML 字段
type ComponentVersionSpec struct {
    // 组件名称
    Name string `json:"name"`
    
    // 组件类型: yaml, helm, inline, binary,selector
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
    // Helm/YAML 组件通过 values/nodeSelector 自行处理节点调度
    NodeFilter *NodeFilterSpec `json:"nodeFilter,omitempty"`
    
    // Kubernetes 资源定义列表
    // 注意: 此字段位于顶层是历史原因——最初无独立 YAML 类型, Resources 用于所有类型
    // 新增 YAML 类型后, 理论上应迁移至 YAMLSpec, 但为保持向后兼容暂不移动
    // 后续版本可考虑: ① 迁移到 YAMLSpec; ② 或保持顶层但标注仅 YAML 类型生效
    // 当前代码中 EnsurePreUpgradeResources Phase 和 YamlComponentExecutor 均使用此字段
    Resources []ResourceSpec `json:"resources,omitempty"`
}

// ComponentType 定义组件类型 ✅复用现有 (含 binary/helm/yaml/inline 四值) + 🆕新增 selector
type ComponentType string

const (
    ComponentTypeYAML     ComponentType = "yaml"
    ComponentTypeHelm     ComponentType = "helm"
    ComponentTypeInline   ComponentType = "inline"
    ComponentTypeBinary   ComponentType = "binary"
    ComponentTypeSelector ComponentType = "selector" // 🆕互斥选择器: 从 subComponents 中按 condition 选择一个
)

// CompatibilitySpec 定义兼容性约束 ✅复用现有
type CompatibilitySpec struct {
    // 约束列表
    Constraints []Constraint `json:"constraints,omitempty"`
}

// Constraint 定义单个兼容性约束 ✅复用现有
type Constraint struct {
    // 依赖组件名称
    Component string `json:"component"`
    
    // 版本规则 (semver range, 如 ">=1.26.0")
    Rule string `json:"rule"`
}

// Dependency 定义组件间依赖关系 ✅复用现有
type Dependency struct {
    // 依赖组件名称
    Name string `json:"name"`
    
    // 依赖阶段 (Install / Upgrade)
    Phase string `json:"phase,omitempty"`
}

// UpgradeStrategySpec 定义升级策略 ✅复用现有
// 这是 DAG 调度层策略, 适用于所有组件类型 (binary/helm/yaml/inline)
// 与各类型的专属策略互补:
// - Binary: 无专属策略, 仅使用 UpgradeStrategy
// - Helm:   HelmStrategySpec 控制 helm 命令参数, UpgradeStrategy 控制调度和失败处理
// - YAML:  无专属策略, 仅使用 UpgradeStrategy
// 两者的 Mode 字段含义不同:
// - UpgradeStrategy.Mode = Rolling/Parallel/Batch (节点并发策略)
// - HelmStrategySpec.Mode = Install/Upgrade/Rollback (Helm 操作类型)
type UpgradeStrategySpec struct {
    // 升级模式: Rolling / Parallel / Batch (节点并发策略)
    Mode string `json:"mode,omitempty"`
    
    // 批量大小 (Batch 模式下每批节点数)
    BatchSize int `json:"batchSize,omitempty"`
    
    // 超时时间
    Timeout string `json:"timeout,omitempty"`
    
    // 失败策略: FailFast / Continue / Rollback
    // FailFast: 立即终止整个组件执行
    // Continue: 记录警告, 继续执行下一个节点/批次
    // Rollback: 回滚后继续 (Binary 执行 UninstallScript; Helm 执行 helm rollback)
    //           注意: Helm 若 Strategy.Atomic=true, Helm SDK 已自动回滚, 无需额外调用
    FailurePolicy string `json:"failurePolicy,omitempty"`
}

// NodeFilterSpec 定义 Binary 组件的节点过滤策略 🆕新增
//
// 设计思路 — 为什么放在 ComponentVersionSpec 顶层而非 UpgradeStrategySpec 内:
// 安装和升级都需要节点过滤，不应绑定到"升级策略"语义中
//
// 设计思路 — 为什么仅用于 Binary 组件:
// Binary 组件直接在节点上 SSH 执行，需要 Controller 选择目标节点
// Helm/YAML 组件部署到集群，节点调度由 K8s Scheduler 通过 nodeSelector 处理
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
    // true:  检查 NodeComponentStatuses[nodeIP].Version == target → 跳过
    // false: 对所有节点执行，不检查 per-node 状态
    // 默认: true (大多数场景需要幂等)
    // 例外: bkeagent 升级设为 false (当前代码 EnsureAgentUpgrade 无过滤)
    SkipCompleted *bool `json:"skipCompleted,omitempty"`
    
    // 是否排除预约添加的节点
    // 默认: true (与当前 filterNodes 的 WithExcludeAppointmentNodes 一致)
    ExcludeAppointment *bool `json:"excludeAppointment,omitempty"`
}

// SubComponent 定义子组件引用 ✅复用现有, 🆕新增 Condition 字段
type SubComponent struct {
    // 子组件名称
    Name string `json:"name"`
    
    // 子组件版本
    Version string `json:"version"`

    // 🆕生成条件 (Go Template 表达式)
    // 仅 type=selector 时使用: DAG 构建期评估, condition 为真的子组件纳入 DAG
    // type=yaml 等其他类型时忽略此字段 (全包含语义不变)
    // 示例: '{{.ContainerRuntimeCRI == "containerd"}}'
    Condition string `json:"condition,omitempty"`
}

// ResourceSpec 定义 Kubernetes 资源 ✅复用现有
//
// 设计思路 — Data、StringData 与 Manifest 三种资源定义方式:
//
// ResourceSpec 支持三种方式定义 K8s 资源内容, 通过 Kind 字段分发:
//
// 1. Data (ConfigMap 专属): key-value 字符串, 直接作为 ConfigMap.data
//    - 场景: 简单的配置文件, 如 bkeagent 的 logLevel/snapshotter 等参数
//    - 优势: 结构化、可校验, 无需写完整 ConfigMap YAML
//    - 代码: provisionConfigMap() 读取 Data 字段创建 ConfigMap 对象
//
// 2. StringData (Secret 专属): key-value 明文字符串, 自动转 base64 编码
//    - 场景: Secret 中的证书、密钥等敏感数据
//    - 优势: 用户写明文, 代码自动转 base64, 避免手动编码出错
//    - 如果用 Manifest 方式, Secret.data 需要 base64 编码, 不友好且易错
//    - 代码: provisionSecret() 读取 StringData, 自动转为 Secret.data (base64)
//
// 3. Manifest (通用): 原始 YAML 字符串, 解析为 unstructured.Unstructured 后创建
//    - 场景: Deployment/Service/CRD 等复杂资源, 声明式字段无法表达完整定义
//    - 优势: 支持任意 K8s 资源, 灵活性最高
//    - 代码: provisionFromManifest() 解析 YAML 并创建资源
//    - 额外用途: CollectComponentManifests() 收集 Manifest 作为 YAML 组件的清单
//
// 分发逻辑 (provisionResource):
//   - Manifest 非空且 Kind 非 ConfigMap/Secret → 直接用 Manifest
//   - Kind == ConfigMap → 用 Data 创建
//   - Kind == Secret → 用 StringData 创建 (避免手动 base64)
//   - 其他 Kind 且无 Manifest → 报错
//
// ConfigMap 用 Data 的原因: 与 Secret 对称设计, 且略简洁 (无需写完整 YAML)
// Secret 用 StringData 的原因: 避免 base64 编码, 这是核心价值
// 其他资源用 Manifest 的原因: 声明式字段无法表达完整 K8s 资源定义
type ResourceSpec struct {
    // 资源类型 (ConfigMap / Secret / Deployment / CRD 等)
    Kind string `json:"kind"`
    
    // API 版本
    APIVersion string `json:"apiVersion"`
    
    // 命名空间
    Namespace string `json:"namespace,omitempty"`
    
    // 资源名称
    Name string `json:"name"`
    
    // 标签 (自动注入到创建的资源中)
    Labels map[string]string `json:"labels,omitempty"`
    
    // ConfigMap 数据 (Kind=ConfigMap 时使用, 直接作为 ConfigMap.data)
    Data map[string]string `json:"data,omitempty"`
    
    // Secret 明文数据 (Kind=Secret 时使用, 自动转 base64 编码为 Secret.data)
    // 核心价值: 用户写明文, 避免手动 base64 编码
    StringData map[string]string `json:"stringData,omitempty"`
    
    // 原始 Manifest 内容 (通用, 任意 Kind 均可使用)
    // ConfigMap/Secret 优先用 Data/StringData, 其他 Kind 用 Manifest
    // 也用于 YAML 组件的清单收集 (CollectComponentManifests)
    Manifest string `json:"manifest,omitempty"`
}
```

### 3.2 Binary 类型字段定义

```go
// BinarySpec 定义二进制组件规格 🆕新增 (api/v1alpha1 扩展)
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
    // 对于归档文件: 解压到此路径 (如 "/" 表示解压到根目录)
    // 对于单文件: 复制到此路径
    // installScript 负责所有安装细节 (chmod、移动文件、创建目录等)
    InstallPath string `json:"installPath"`
}

// ConfigTemplateSpec 定义配置文件模板规格
type ConfigTemplateSpec struct {
    // 模板名称
    Name string `json:"name"`
    
    // 静态目标路径 (与 PathTemplate 互斥)
    // ForEach 为空时使用此字段指定固定路径
    Path string `json:"path,omitempty"`
    
    // 动态路径模板 (Go template, 与 ForEach 配合使用)
    // ForEach 不为空时使用此字段动态生成路径
    // 渲染时可访问全部 TemplateContext 变量 + 迭代变量 (.Key, .Value)
    // 示例: "{{cd "containerd" "registryConfigPath"}}/{{.Key}}/hosts.toml"
    PathTemplate string `json:"pathTemplate,omitempty"`
    
    // 迭代源路径 (点分隔, 从 TemplateContext 中解析)
    // 支持 map[string]interface{} (按 key/value 迭代) 和 []interface{} (按 index/value 迭代)
    // 示例: "Config.Cluster.ContainerRuntime.Registry"
    // 详见 4.6 节 forEach 机制
    ForEach string `json:"forEach,omitempty"`
    
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
    
    // 生成条件 (Go Template 表达式)
    // 空 = 始终生成；渲染结果为 "false" 或空时跳过此模板（不生成文件）
    // 示例: "{{.isOffline}}" → 离线时生成，在线时跳过
    // 典型场景: hosts.toml 离线重定向文件——在线模式不需要公共仓库的重定向配置
    Condition string `json:"condition,omitempty"`
}

**字段约束**：

| 条件 | 说明 |
|------|------|
| `ForEach != ""` | `PathTemplate` 必填，`Path` 忽略，按迭代源展开为多个文件 |
| `ForEach == ""` | `Path` 必填，`PathTemplate` 忽略（原有行为，单文件） |

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
    // 操作系统名称 (centos, ubuntu)
    Name string `json:"name"`
    
    // 支持的版本列表
    Versions []string `json:"versions"`
}
```

### 3.3 YAML 类型字段定义

```go
// YAMLSpec 定义 YAML 清单组件规格 🆕新增 (api/v1alpha1 扩展)
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
    
    // 健康检查配置 (应用清单后验证 Pod/Endpoint 就绪)
    // 类型定义见第 7 章 HealthCheck 共享包设计 (7.1 节)
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

### 3.4 Helm 类型字段定义

```go
// HelmSpec 定义 Helm 组件规格 🆕新增 (api/v1alpha1 扩展)
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
    // 类型定义见第 7 章 HealthCheck 共享包设计 (7.1 节)
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
//
// 设计思路 — 与 UpgradeStrategySpec 的分工:
// HelmStrategySpec 是 Helm SDK 层策略, 控制 helm 命令参数 (--wait/--atomic/--timeout)
// UpgradeStrategySpec 是 DAG 调度层策略, 控制节点间并发和失败处理 (Rolling/Batch/FailFast)
// 两者互补: UpgradeStrategy 决定"何时/如何调度", HelmStrategy 决定"helm 命令怎么执行"
//
// Mode 字段的三种值:
// - Install:  显式指定 helm install (Release 不存在时)
// - Upgrade:  显式指定 helm upgrade (Release 已存在时)
// - Rollback: 显式触发 helm rollback (恢复到上一个 Release 版本)
//
// Mode 为空时, 由 VersionContext 自动判定:
//   HasCurrent=false → Install, HasCurrent=true → Upgrade
//
// Mode=Rollback 的使用场景:
// 1. 用户显式回滚: 升级后发现问题, 修改 ComponentVersion 指定 Mode=Rollback 触发回滚
// 2. 失败自动回滚: 当 UpgradeStrategy.FailurePolicy=Rollback 且 helm upgrade 失败时,
//    若 Strategy.Atomic=false (Helm SDK 未自动回滚), 系统调用 i.rollback() 执行 helm rollback
//    若 Strategy.Atomic=true, Helm SDK 已在 upgrade 命令内部自动回滚, 无需额外调用
type HelmStrategySpec struct {
    // 安装模式: Install / Upgrade / Rollback (空=自动判定)
    Mode string `json:"mode,omitempty"`
    
    // 是否等待就绪 (对应 helm --wait)
    Wait bool `json:"wait,omitempty"`
    
    // 等待超时时间 (对应 helm --timeout)
    WaitTimeout string `json:"waitTimeout,omitempty"`
    
    // 是否原子操作 (对应 helm --atomic, 失败时 Helm SDK 自动回滚)
    Atomic bool `json:"atomic,omitempty"`
    
    // 失败时是否清理 (对应 helm --cleanup-on-fail, 配合 Atomic 使用)
    CleanupOnFail bool `json:"cleanupOnFail,omitempty"`
}

// 健康检查相关类型定义见第 7 章 HealthCheck 共享包设计 (7.1 节)
// HealthCheckSpec / HealthCheckItemSpec / PodReadyCheckSpec /
// EndpointReadyCheckSpec / CustomCheckSpec
// HelmSpec 通过引用 pkg/healthcheck 包中的类型使用

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

### 3.5 Inline 类型字段定义

```go
// InlineSpec 定义内联执行器配置 ✅复用现有 (api/v1alpha1 已定义)
// Inline 组件通过 ComponentFactory 注册的 handler 执行, 无需制品下载/模板渲染
// handler 名称对应 ComponentFactory.Register() 注册的 key
type InlineSpec struct {
    // Handler 名称 (对应 ComponentFactory 注册的 handler)
    Handler string `json:"handler"`
    
    // Handler 版本
    Version string `json:"version"`
}
```

### 3.6 Selector 类型字段定义

**设计思路 — 互斥选择器，按 type 区分 subComponents 语义**：

`selector` 类型用于表达"从多个候选组件中选择一个"的场景。典型用例：容器运行时——一个集群只能安装一种容器运行时（containerd 或 docker），选择由 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 决定。

`subComponents` 字段在不同 `type` 下有不同语义，由 `type` 字段天然消歧：

| 维度 | type=yaml（组合） | type=selector（互斥选择） |
|------|------|------|
| subComponents 语义 | 全包含——所有子组件都安装 | 条件选一——评估 condition，为真的纳入 DAG |
| Condition 字段 | 忽略（不评估） | 评估后选一 |
| DAG 节点 | 父组件 + 所有子组件各自产生 DAG 节点 | 仅 condition 为真的子组件产生 DAG 节点 |
| selector 自身 | 不适用 | 不产生 DAG 节点（纯选择器，无自身安装逻辑） |
| 典型场景 | openfuyao-core 包含 kubernetes-master + kubernetes-worker | container-runtime 选 containerd 或 docker |

selector 类型不定义专属 Spec 结构体（无 `SelectorSpec`），仅复用现有的 `SubComponent`（含 `Condition` 字段）和 `UpgradeStrategySpec`。

**container-runtime ComponentVersion YAML（selector 类型）**：
```yaml
# bke-manifests/container-runtime/v1.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: container-runtime-v1.0.0
spec:
  name: container-runtime
  type: selector
  version: v1.0.0
  subComponents:
    # containerd 运行时 (CRI=containerd 时选择)
    - name: containerd
      version: v1.7.18
      condition: '{{.ContainerRuntimeCRI == "containerd"}}'

    # docker 运行时 (CRI=docker 时选择)
    - name: docker
      version: v26.0.0
      condition: '{{.ContainerRuntimeCRI == "docker"}}'

    # cri-dockerd (CRI=docker 时选择, K8s >=1.24 必需)
    - name: cri-dockerd
      version: v0.3.9
      condition: '{{.ContainerRuntimeCRI == "docker"}}'

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```
> **ReleaseImage 引用方式**：ReleaseImage 只引用 `container-runtime/v1.0.0`，DAG 构建期自动展开为 containerd 或 docker + cri-dockerd。无需在 ReleaseImage 中分别声明。

**docker ComponentVersion YAML（binary 类型）**：
```yaml
# bke-manifests/docker/v26.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: docker-v26.0.0
spec:
  name: docker
  type: binary
  version: v26.0.0

  binary:
    variables:
      cgroupDriver: "systemd"
      dataRoot: "/var/lib/docker"
      lowLevelRuntime: "runc"
      insecureRegistries: ""
      registryMirror: ""

    # Docker 通过包管理器安装 (非二进制下载), 无 artifacts
    # installScript 负责通过 yum/apt 安装 docker-ce
    installScript: |
      #!/bin/bash
      set -e
      systemctl stop docker || true
      systemctl stop docker.socket || true

      # 1. 通过包管理器安装 docker-ce
      if [ -f /etc/redhat-release ]; then
        yum install -y yum-utils
        yum install -y docker-ce
      elif [ -f /etc/os-release ] && grep -q ubuntu /etc/os-release; then
        apt-get update
        apt-get install -y docker-ce
      fi

      # 2. 启动并验证
      systemctl enable docker
      systemctl restart docker
      docker --version

    # Docker 配置文件 (对应现有 ConfigDockerDaemon 生成逻辑)
    configTemplates:
      - name: daemon.json
        path: "/etc/docker/daemon.json"
        mode: "0644"
        content: |
          {
            "exec-opts": ["native.cgroupdriver={{.Variables.cgroupDriver}}"],
            "data-root": "{{.Variables.dataRoot}}",
            "runtimes": {
              "{{.Variables.lowLevelRuntime}}": {"path": "/usr/local/beyondvm/runc"}
            }
          }

    healthCheck:
      enabled: true
      timeout: "2m"
      interval: "5s"
      script: |
        systemctl is-active docker
        docker info > /dev/null 2>&1

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```
> **Docker 与 containerd 的关键差异**：Docker 无 `hosts.toml`（镜像仓库配置在 `daemon.json` 的 `registry-mirrors` 中）；Docker 通过包管理器安装（无 `artifacts` 二进制下载）；Docker 需要 `cri-dockerd` 作为 CRI 适配层（K8s ≥1.24）。

**cri-dockerd ComponentVersion YAML（binary 类型）**：
```yaml
# bke-manifests/cri-dockerd/v0.3.9/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: cri-dockerd-v0.3.9
spec:
  name: cri-dockerd
  type: binary
  version: v0.3.9

  binary:
    artifacts:
      - name: cri-dockerd
        url: "{{imageRegistry}}/cri-dockerd/{{version}}/cri-dockerd-{{version}}-{{arch}}"
        checksum: "sha256:cri-dockerd-checksum-placeholder"
        installPath: "/usr/bin"
        executable: cri-dockerd

    configTemplates:
      - name: cri-dockerd.service
        path: "/etc/systemd/system/cri-dockerd.service"
        mode: "0644"
        content: |
          [Unit]
          Description=CRI Interface for Docker Application Container Engine
          After=network-online.target firewalld.service
          Wants=network-online.target

          [Service]
          ExecStart=/usr/bin/cri-dockerd --container-runtime-endpoint unix:///var/run/cri-dockerd.sock --pod-infra-container-image {{.Variables.sandboxImage}}
          ExecStartPost=/bin/systemctl restart kubelet
          Delegate=yes
          Restart=always

          [Install]
          WantedBy=multi-user.target

      - name: cri-dockerd.socket
        path: "/etc/systemd/system/cri-dockerd.socket"
        mode: "0644"
        content: |
          [Unit]
          Description=CRI Dockerd Socket for the API

          [Socket]
          ListenStream=/var/run/cri-dockerd.sock
          SocketMode=0660
          SocketUser=root
          SocketGroup=docker

          [Install]
          WantedBy=sockets.target

    installScript: |
      #!/bin/bash
      set -e
      systemctl stop cri-dockerd || true
      systemctl stop cri-dockerd.socket || true

      # 1. 安装二进制
      install -m 0755 {{artifact.cri-dockerd.path}} /usr/bin/cri-dockerd

      # 2. 安装依赖
      if [ -f /etc/redhat-release ]; then
        yum install -y socat || true
      elif [ -f /etc/os-release ] && grep -q ubuntu /etc/os-release; then
        apt-get install -y socat || true
      fi

      # 3. 启动
      systemctl daemon-reload
      systemctl enable cri-dockerd
      systemctl start cri-dockerd

    healthCheck:
      enabled: true
      timeout: "1m"
      interval: "3s"
      script: |
        systemctl is-active cri-dockerd

  dependencies:
    - name: docker
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: FailFast
```

**Selector DAG 构建流程图**：
```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                      Selector DAG 构建流程                                       │
└─────────────────────────────────────────────────────────────────────────────────┘

    ┌──────────────────────────────┐
    │  BuildDAGFromBundle          │
    │  遍历 ReleaseImage.components│
    └──────────────┬───────────────┘
                   │
                   │ 遇到 container-runtime/v1.0.0
                   ▼
    ┌──────────────────────────────┐
    │  加载 ComponentVersion       │
    │  cv.Spec.Type == "selector"  │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  读取 ContainerRuntimeCRI    │
    │  从 ExecutionContext         │
    │  .TemplateContext.Variables  │
    │  ["ContainerRuntimeCRI"]     │
    └──────────────┬───────────────┘
                   │
                   ▼
    ┌──────────────────────────────┐
    │  遍历 cv.Spec.SubComponents  │
    │  评估每个 sub.Condition       │
    └──────────────┬───────────────┘
                   │
       ┌───────────┼───────────┐
       │           │           │
       ▼           ▼           ▼
  ┌──────────┐ ┌─────────┐ ┌──────────┐
  │containerd│ │ docker  │ │cri-docker│
  │condition │ │condition│ │condition │
  │= true?   │ │= true?  │ │= true?   │
  └────┬─────┘ └────┬────┘ └─────┬────┘
       │           │             │
  CRI=containerd CRI=docker   CRI=docker
       │           │             │
       ▼           ▼             ▼
   纳入 DAG     纳入 DAG      纳入 DAG
   (binary)    (binary)      (binary)
      │           │             │
      │           └──────┬──────┘
      │                  │ 依赖关系
      │                  ▼
      │           docker → cri-dockerd
      │           (DAG 依赖边)
      │
      ▼
  selector 自身不产生 DAG 节点
  (纯选择器, 无安装逻辑)
```

**与现有代码的对应关系**：

| 现有代码 | KEP-6 selector 设计 |
|---------|-------------------|
| `init.go:789-797` `downloadContainerRuntime` switch CRI | DAG 构建器评估 subComponents.condition |
| `CRIContainerd = "containerd"` | `condition: '{{.ContainerRuntimeCRI == "containerd"}}'` |
| `CRIDocker = "docker"` + CRIDockerPlugin | docker + cri-dockerd 两个 ComponentVersion，condition 均匹配 docker |
| `BKECluster.Spec.Cluster.ContainerRuntime.CRI` | `ExecutionContext.TemplateContext.Variables["ContainerRuntimeCRI"]` |
| DockerPlugin: yum 安装 + daemon.json | docker ComponentVersion: installScript(yum) + configTemplates(daemon.json) |
| CRIDockerPlugin: 下载二进制 + service + socket | cri-dockerd ComponentVersion: artifacts + configTemplates(service+socket) |

### 3.7 CRD YAML 定义

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
    # v1alpha1: 唯一版本, 直接扩展 (storage=true)
    # 包含 binary/helm/yaml/selector 字段定义, 所有新字段均为 omitempty, 向后兼容
    - name: v1alpha1
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
                  enum: [yaml, helm, inline, binary, selector]
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
                          pathTemplate:
                            type: string
                          forEach:
                            type: string
                          mode:
                            type: string
                          owner:
                            type: string
                          content:
                            type: string
                          condition:
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
                        required: [name]
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
                              podReady:
                                type: object
                                properties:
                                  namespace:
                                    type: string
                                  labelSelector:
                                    type: string
                                  minReady:
                                    type: integer
                                required: [namespace, labelSelector]
                              endpointReady:
                                type: object
                                properties:
                                  namespace:
                                    type: string
                                  serviceName:
                                    type: string
                                  port:
                                    type: integer
                                required: [namespace, serviceName]
                              custom:
                                type: object
                                properties:
                                  command:
                                    type: string
                                required: [command]
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
                              podReady:
                                type: object
                                properties:
                                  namespace:
                                    type: string
                                  labelSelector:
                                    type: string
                                  minReady:
                                    type: integer
                                required: [namespace, labelSelector]
                              endpointReady:
                                type: object
                                properties:
                                  namespace:
                                    type: string
                                  serviceName:
                                    type: string
                                  port:
                                    type: integer
                                required: [namespace, serviceName]
                              custom:
                                type: object
                                properties:
                                  command:
                                    type: string
                                required: [command]
                            required: [type]
                      required: [enabled]
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
                      condition:
                        type: string
                        description: "Go Template expression, evaluated at DAG build time (type=selector only)"
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
                nodeFilter:
                  type: object
                  description: "Binary 组件的节点过滤策略 (安装和升级共用)"
                  properties:
                    roles:
                      type: array
                      description: "目标节点角色列表 (空或不填 = 所有角色)"
                      items:
                        type: string
                    matchLabels:
                      type: object
                      description: "节点标签选择器 (等值匹配)"
                      additionalProperties:
                        type: string
                    skipCompleted:
                      type: boolean
                      description: "是否跳过已完成的节点 (默认: true)"
                    excludeAppointment:
                      type: boolean
                      description: "是否排除预约添加的节点 (默认: true)"
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
```

### 3.8 CRD 版本迁移设计

**设计思路 - 直接扩展 v1alpha1，不引入 v1alpha2**：

原设计拟新增 `api/v1alpha2/` + conversion 函数。但经审视，现有 `api/v1alpha1/componentversion_types.go` 的 `Type` 字段已支持 `yaml/helm/inline/binary/selector` 四值（仅 enum，无 schema 定义），且所有新字段（`Binary`/`Helm`/`YAML`）均为 `omitempty` 指针类型，向旧数据完全兼容。引入 v1alpha2 会带来：① 双版本 conversion 维护成本；② `(*v1alpha2.InlineSpec)(src.Spec.Inline)` 等跨包指针强转的脆弱性；③ 现有引用 v1alpha1 的代码全部需评估迁移。

因此**直接在 v1alpha1 上扩展**：新增 `Binary *BinarySpec`/`Helm *HelmSpec`/`YAML *YAMLSpec` 三个 omitempty 字段及对应 CRD schema。旧 ComponentVersion（无这三个字段）反序列化后新字段为 nil，行为不变；新 ComponentVersion 带新字段，旧控制器忽略（omitempty）。无需 conversion，零迁移风险，最大化复用现有类型文件与 deepcopy 生成代码。

**扩展内容**：

| 改动位置 | 内容 |
|---------|------|
| `api/v1alpha1/componentversion_types.go` | 新增 `BinarySpec`/`HelmSpec`/`YAMLSpec`/`ArtifactSpec`/`ConfigTemplateSpec`/... 等类型；`ComponentVersionSpec` 增加 `Binary *BinarySpec`/`Helm *HelmSpec`/`YAML *YAMLSpec` 字段 |
| `api/v1alpha1/zz_generated.deepcopy.go` | 重新 `make` 生成 DeepCopy 方法 |
| `config/crd/bases/...componentversions.yaml` | v1alpha1 schema 新增 binary/helm/yaml/selector 字段定义（见 3.7 节） |

**兼容性保证**：
- 新字段全部 `omitempty` + 指针类型，旧 YAML 不填则为 nil
- `Type` 字段 enum 已含 `binary`/`helm`/`yaml`，无需改 enum
- 旧控制器代码不读取新字段，不受影响
- Feature Gate 关闭时即使新字段存在也不走新路径（见 12.2）

**迁移步骤（简化）**：

| 步骤 | 操作 | 风险 | 回滚方案 |
|------|------|------|---------|
| 1 | 在 `api/v1alpha1/componentversion_types.go` 新增 BinarySpec/HelmSpec/YAMLSpec 及子类型 | 无 | 删除新增类型 |
| 2 | `ComponentVersionSpec` 增加 `Binary/Helm/YAML` 字段 + 重新生成 deepcopy | 低 | 删除字段 |
| 3 | CRD schema 合并 binary/helm/yaml 定义到 v1alpha1 版本 | 低 | 还原 schema |
| 4 | 控制器按 Feature Gate 读取新字段 | 中 | 关闭 Feature Gate |

**注意事项**：
- 路径修正：原设计 3.1 节注释 `pkg/api/v1alpha1/componentversion_types.go` 有误，实际路径为 `api/v1alpha1/componentversion_types.go`（无 `pkg/` 前缀）
- `Resources`/`SubComponents`/`Compatibility`/`Dependencies`/`UpgradeStrategy` 等类型在 v1alpha1 已存在，本设计的 3.1 节应标注"复用现有"而非"新增"（见 m1）
- 若未来字段规模膨胀确需 v1alpha2，再按标准 conversion 流程引入，此时 v1alpha1 已是稳定存储版本

## 4. BinaryInstaller 详细设计

### 4.1 核心组件架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            BinaryInstaller                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                         核心组件                                         │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                    ArtifactDownloader                           │    │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │    │
│  │  │  │  HTTP Client │  │ Cache Manager│  │ Checksum Verifier   │    │    │    │
│  │  │  │  (下载制品)   │  │ (本地缓存)   │  │ (校验和验证)         │    │    │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                     TemplateRenderer                            │    │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │    │
│  │  │  │  Go Template │  │ FuncMap      │  │ Variable Resolver   │    │    │    │
│  │  │  │  (模板解析)   │  │ (自定义函数)  │  │ (变量解析)          │    │    │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                      ConfigRenderer                             │    │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │    │
│  │  │  │ Content Mode │  │ Secret Mode  │  │ Kubeconfig Mode     │    │    │    │
│  │  │  │ (模板渲染)    │  │ (Secret获取) │  │ (动态生成)           │    │    │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                       SSH Executor                              │    │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │    │
│  │  │  │ File Upload  │  │ Script Exec  │  │ Result Collector    │    │    │    │
│  │  │  │ (文件上传)    │  │ (脚本执行)   │  │ (结果收集)           │    │    │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                     HealthChecker (SSH)                         │    │    │
│  │  │  ┌─────────────────────────────────────────────────────┐        │    │    │
│  │  │  │ SSH 执行健康检查脚本, 退出码 0=健康                   │        │    │    │
│  │  │  │ (BinaryHealthCheckSpec.Script, 见 4.3)              │        │    │    │
│  │  │  └─────────────────────────────────────────────────────┘        │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
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
                              │   入口函数        │
                              └────────┬─────────┘
                                       │
                                       ▼
                     ┌──────────────────────────────────────┐
                     │  1. 通过 SSH 发现节点架构             │
                     │  arch = sshDiscoverArch(node.IP)     │
                     │  (执行 uname -m, 返回 amd64/arm64)   │
                     │                                      │
                     │  注意: OS 不在此处获取,               │
                     │  由 installScript 运行时自检测        │
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
           │  检查缓存        │  │  解析 URL 模板  │  │  下载制品        │
           │  cache.Get()    │  │  resolveTemplate│  │  downloadAnd    │
           │                 │  │  ({{arch}}等)   │  │  Verify()       │
           └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                    │                    │                    │
                    └────────────────────┼────────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 校验 Checksum                    │
                    │  verifyChecksum(data, expected)      │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         校验通过                校验失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  保存到缓存      │   │  返回错误        │
                    │  cache.Save()   │   │  return err     │
                    └────────┬────────┘   └─────────────────┘
                             │
                             ▼
                    ┌──────────────────────────────────────┐
                    │  4. 渲染安装脚本                      │
                    │  renderInstallScript(script,         │
                    │                      artifacts, opts)│
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  5. 渲染配置文件模板                  │
                    │  renderConfigTemplates(templates,    │
                    │                       opts)          │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  Content 模式   │  │  Secret 模式    │  │  Kubeconfig      │
          │  Go template    │  │  从 Secret 获取 │  │  动态生成        │
          │  渲染           │  │  内容           │  │  kubeconfig      │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  6. SSH 执行安装                      │
                    │  executeInstall(ctx, node, script,   │
                    │                 artifacts, configs)  │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  上传二进制      │  │  上传配置        │  │  执行脚本       │
          │  ssh.Upload()   │  │  ssh.Upload()   │  │  ssh.Execute()  │
          │  到节点         │  │  到节点          │  │                 │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                     ┌──────────────────────────────────────┐
                     │  7. 收集执行结果                      │
                     │  collectResult(stdout, stderr, err)  │
                     └────────────────────┬─────────────────┘
                                          │
                               ┌──────────┴──────────┐
                               │                     │
                          执行成功                执行失败
                               │                     │
                               ▼                     ▼
                     ┌─────────────────┐   ┌─────────────────┐
                     │  8. 健康检查     │   │  返回错误       │
                     │  (HealthCheck   │   │  return err     │
                     │   Enabled 时)   │   │  (含 stdout/    │
                     │  SSH 执行脚本    │   │   stderr)       │
                     │  退出码 0=健康   │   └─────────────────┘
                     └────────┬────────┘
                              │
                     ┌────────┴────────┐
                     │                 │
                    检查通过        检查失败
                     │                 │
                     ▼                 ▼
                     ┌─────────────────┐
                     │  返回成功        │
                     │  return nil     │
                     └─────────────────┘
```

### 4.3 核心接口定义

**设计思路**：BinaryInstaller 的接口设计复用现有的 `manifest.TemplateContext`，避免重复传递集群和节点信息。DAG 调度器构建的 TemplateContext 直接传递给 BinaryInstaller，BinaryInstaller 在此基础上填充制品信息。
```go
// pkg/binaryinstaller/installer.go

// SSHExecutor SSH 执行抽象接口 (phaseframe-free)
//
// 设计思路 - 为什么不直接用 *bkessh.MultiCli:
// 现有 bkessh.MultiCli 的 API 是面向"多主机并发"设计的:
//   - Run(cmd Command) 在所有已注册主机上并发执行，返回聚合结果
//   - 文件上传通过 Command.FileUp []File 携带，非独立 Upload 方法
//   - 单主机执行在 HostRemoteClient (remotecli.go) 上，非 MultiCli
//   - 架构发现通过 RegisterHostsInfo() + NodeArchByAddress()
// BinaryInstaller 需要的是"单主机执行脚本 + 上传文件 + 发现架构"的简洁 API。
// 直接依赖 *bkessh.MultiCli 会把多主机并发模型泄漏到 Installer，且无法 Mock。
// 因此定义 SSHExecutor 接口，由 controllers 层提供 NewMultiCliSSHAdapter
// 适配 bkessh.MultiCli/HostRemoteClient，使 BinaryInstaller 可独立测试。
type SSHExecutor interface {
    // Execute 在指定节点执行脚本，返回 stdout/stderr/exit code
    Execute(ctx context.Context, nodeIP, script string) (*SSHResult, error)
    // Upload 上传数据到指定节点的远程路径
    Upload(ctx context.Context, nodeIP string, data []byte, remotePath string) error
    // DiscoverArch 发现节点架构 (uname -m → amd64/arm64)
    // 复用现有 agentssh.DiscoverArchs 逻辑 (从 phaseutil 抽取到 phaseframe-free 包)
    DiscoverArch(ctx context.Context, nodeIP string) (string, error)
}

// SSHResult SSH 命令执行结果
type SSHResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
}

// BinaryInstaller 二进制组件安装器
type BinaryInstaller struct {
    client         client.Client
    sshExecutor    SSHExecutor          // 抽象接口，不再直接依赖 *bkessh.MultiCli
    cacheDir       string
    httpClient     *http.Client
    cache          *ArtifactCache
    renderer       *TemplateRenderer   // 模板渲染引擎 (含自定义函数, 无状态, 全局共享)
    configRenderer *ConfigRenderer     // 配置文件渲染器 (需 K8s client 读取 Secret)
    logger         *bkev1beta1.BKELogger
}

// BinaryInstallerConfig BinaryInstaller 构建配置
type BinaryInstallerConfig struct {
    Client         client.Client
    SshExecutor    SSHExecutor          // 替代原 SshClient *bkessh.MultiCli
    CacheDir       string
    HttpClient     *http.Client
    Renderer       *TemplateRenderer
    ConfigRenderer *ConfigRenderer
    Logger         *bkev1beta1.BKELogger
}

// NewBinaryInstaller 创建二进制组件安装器 (返回 error，不 panic)
func NewBinaryInstaller(cfg BinaryInstallerConfig) (*BinaryInstaller, error) {
    cache, err := NewArtifactCache(cfg.CacheDir)
    if err != nil {
        return nil, fmt.Errorf("failed to create artifact cache: %w", err)
    }
    return &BinaryInstaller{
        client:         cfg.Client,
        sshExecutor:    cfg.SshExecutor,
        cacheDir:       cfg.CacheDir,
        httpClient:     cfg.HttpClient,
        cache:          cache,
        renderer:       cfg.Renderer,
        configRenderer: cfg.ConfigRenderer,
        logger:         cfg.Logger,
    }, nil
}

// ArtifactCache 管理二进制制品的本地文件缓存
type ArtifactCache struct {
    cacheDir string
}

// NewArtifactCache 创建缓存管理器
func NewArtifactCache(cacheDir string) (*ArtifactCache, error) {
    if err := os.MkdirAll(cacheDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create cache directory %s: %w", cacheDir, err)
    }
    return &ArtifactCache{cacheDir: cacheDir}, nil
}
```

**controllers 层 SSH 适配器示例** (适配 `bkessh.MultiCli`，phaseframe-free):

```go
// controllers/capbke/ssh_adapter.go
package capbke

// MultiCliSSHAdapter 把 bkessh.MultiCli 适配为 binaryinstaller.SSHExecutor
// 复用现有 agentssh.DiscoverArchs 的架构发现逻辑 (从 pkg/phaseframe/phaseutil 抽取)
type MultiCliSSHAdapter struct {
    multiCli *bkessh.MultiCli
}

func NewMultiCliSSHAdapter(multiCli *bkessh.MultiCli) *MultiCliSSHAdapter {
    return &MultiCliSSHAdapter{multiCli: multiCli}
}

// Execute 单主机执行: 注册单主机 → 构建 Command → Run → 返回结果
func (a *MultiCliSSHAdapter) Execute(ctx context.Context, nodeIP, script string) (*binaryinstaller.SSHResult, error) {
    // 复用 bkessh.MultiCli 的单主机执行能力 (通过 HostRemoteClient)
    // 具体实现: multiCli.RegisterHosts([]bkessh.Host{...}) → multiCli.Run(bkessh.Command{Cmds: []string{script}})
    // 返回 StdCombine 中对应主机的输出
    ...
}

func (a *MultiCliSSHAdapter) Upload(ctx context.Context, nodeIP string, data []byte, remotePath string) error {
    // 写入本地临时文件 → 构建 Command{FileUp: []bkessh.File{{Src: tmp, Dst: remotePath}}} → Run
    ...
}

func (a *MultiCliSSHAdapter) DiscoverArch(ctx context.Context, nodeIP string) (string, error) {
    // 复用 agentssh.DiscoverArchs (uname -m → amd64/arm64)，该函数需从
    // pkg/phaseframe/phaseutil 抽取到 phaseframe-free 包 (如 pkg/remote/arch.go)
    ...
}
```

**设计思路 — TemplateRenderer vs TemplateContext**：

`BinaryInstallerConfig.Renderer` 注入的是 **TemplateRenderer（引擎）**，不是 TemplateContext（数据）：

| 概念 | 类型 | 作用 | 生命周期 |
|------|------|------|---------|
| **TemplateRenderer** | 渲染引擎 | Go template 解析+执行引擎，含自定义函数（`upper`/`lower`/`eq`/`now`等） | 全局共享，无状态，通过 Config 注入 |
| **TemplateContext** | 数据载体 | 模板变量数据（ClusterName/NodeIP/Artifacts/Action 等） | 每节点每组件运行时构建，通过 `InstallOptions.TemplateCtx` 传入 |

TemplateRenderer 是"工具"（怎么渲染），TemplateContext 是"数据"（渲染什么）。`Install()` 中先从 `opts.TemplateCtx` 获取数据，再用 `i.renderer.RenderScript(script, tmplCtx)` 渲染。

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
    // 注意: opts.TemplateCtx 是扩展后的 TemplateContext (见 8.3 节)
    // 当前代码中的 manifest.TemplateContext 仅含 ClusterName/Namespace/KubernetesVersion/OpenFuyaoVersion
    // KEP-6 设计中需扩展为包含 NodeIP/NodeArch/Artifacts/Variables/ConfigPath 等字段
    tmplCtx := opts.TemplateCtx  // 复用 DAG 调度器传递的 TemplateContext (扩展后)
    
    // 1. 通过 SSH 发现节点架构 (必需: 制品 URL 包含 {{arch}} 模板变量, 下载前必须解析)
    // 复用 SSHExecutor.DiscoverArch (内部对接 agentssh.DiscoverArchs 的 uname -m 逻辑)
    arch, err := i.sshExecutor.DiscoverArch(ctx, tmplCtx.NodeIP)
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
    tmplCtx manifest.TemplateContext,
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
        result, err := i.sshExecutor.Execute(ctx, nodeIP, script)
        if err == nil && result.ExitCode == 0 {
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
func (i *BinaryInstaller) executeInstall(ctx context.Context, nodeIP string, script string, artifacts map[string]*Artifact, configs map[string][]byte) error {
    // 1. 创建远程目录
    if _, err := i.sshExecutor.Execute(ctx, nodeIP, "mkdir -p /tmp/bke-install"); err != nil {
        return fmt.Errorf("failed to create remote directory: %w", err)
    }
    
    // 2. 上传二进制文件
    for name, art := range artifacts {
        remotePath := fmt.Sprintf("/tmp/bke-install/%s", name)
        if err := i.sshExecutor.Upload(ctx, nodeIP, art.Data, remotePath); err != nil {
            return fmt.Errorf("failed to upload %s to %s: %w", name, nodeIP, err)
        }
    }
    
    // 3. 上传配置文件
    for name, content := range configs {
        remotePath := fmt.Sprintf("/tmp/bke-install/%s", name)
        if err := i.sshExecutor.Upload(ctx, nodeIP, content, remotePath); err != nil {
            return fmt.Errorf("failed to upload config %s to %s: %w", name, nodeIP, err)
        }
    }
    
    // 4. 执行安装脚本
    result, err := i.sshExecutor.Execute(ctx, nodeIP, script)
    if err != nil {
        return fmt.Errorf("install script failed on %s: %w\nstdout: %s\nstderr: %s", 
            nodeIP, err, result.Stdout, result.Stderr)
    }
    
    return nil
}
```

### 4.4 Binary Uninstall 流程

**设计思路 — Uninstall 与 Install/Upgrade 的区别**：

Binary 组件的卸载流程与安装/升级有本质区别：
- **Install/Upgrade**：下载制品 → 渲染脚本 → SSH 执行 → 健康检查
- **Uninstall**：渲染卸载脚本 → SSH 执行 → 验证服务已停止

卸载不需要下载制品，因为目标节点上已有二进制文件。卸载脚本负责停止服务、删除二进制、清理配置文件。

**卸载流程图**：

```
Binary Uninstall 流程:
┌─────────────────────────────────────────────────────────────┐
│ 1. 渲染卸载脚本                                              │
│    - 支持模板变量 ({{binPath}}, {{configPath}} 等)           │
├─────────────────────────────────────────────────────────────┤
│ 2. 通过 SSH 执行卸载脚本                                     │
│    - 停止服务: systemctl stop <service>                     │
│    - 禁用服务: systemctl disable <service>                  │
│    - 删除二进制: rm -f /usr/local/bin/<binary>              │
│    - 删除服务文件: rm -f /etc/systemd/system/<service>      │
│    - 重新加载 systemd: systemctl daemon-reload              │
│    - 清理配置目录 (可选): rm -rf /etc/<component>/           │
├─────────────────────────────────────────────────────────────┤
│ 3. 验证服务已停止                                            │
│    - systemctl is-active <service> 返回 "inactive"          │
└─────────────────────────────────────────────────────────────┘
```

**代码实现**：

```go
// executeUninstall 执行二进制组件卸载
func (i *BinaryInstaller) executeUninstall(
    ctx context.Context,
    nodeIP string,
    uninstallScript string,
    tmplCtx manifest.TemplateContext,
) error {
    // 1. 渲染卸载脚本
    script, err := i.renderer.RenderScript(uninstallScript, tmplCtx)
    if err != nil {
        return fmt.Errorf("render uninstall script: %w", err)
    }

    // 2. 通过 SSH 执行卸载脚本
    result, err := i.sshExecutor.Execute(ctx, nodeIP, script)
    if err != nil {
        return fmt.Errorf("uninstall failed on %s: %w\nstdout: %s\nstderr: %s",
            nodeIP, err, result.Stdout, result.Stderr)
    }

    // 3. 验证服务已停止
    verifyCmd := fmt.Sprintf("systemctl is-active %s || true", tmplCtx.ServiceName)
    verifyResult, _ := i.sshExecutor.Execute(ctx, nodeIP, verifyCmd)
    if verifyResult.Stdout == "active" {
        return fmt.Errorf("service %s still active after uninstall on %s", 
            tmplCtx.ServiceName, nodeIP)
    }

    return nil
}
```

**卸载脚本示例**：
```yaml
binary:
  uninstallScript: |
    #!/bin/bash
    set -e
    
    # 停止服务
    systemctl stop containerd || true
    systemctl disable containerd || true
    
    # 删除二进制文件
    rm -f /usr/bin/containerd
    rm -f /usr/bin/containerd-shim-runc-v2
    rm -f /usr/bin/containerd-shim-shimless-v2
    rm -f /usr/bin/containerd-stress
    rm -f /usr/bin/ctr
    rm -f /usr/local/sbin/runc
    rm -f /usr/bin/crictl
    rm -f /usr/bin/nerdctl
    
    # 删除服务文件
    rm -f /usr/lib/systemd/system/containerd.service
    
    # 删除配置文件
    rm -f /etc/crictl.yaml
    rm -rf /etc/containerd/
    
    # 重新加载 systemd
    systemctl daemon-reload
```

### 4.5 ConfigRenderer 详细设计

ConfigRenderer 是 BinaryInstaller 的配置文件渲染器，负责将 `configTemplates` 渲染为最终配置文件内容。支持三种渲染模式：Content（Go template）、SecretRef（从 K8s Secret 获取）、KubeconfigTemplate（动态生成 kubeconfig）。

#### 4.5.1 三种渲染模式

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

#### 4.5.2 ConfigRenderer 渲染流程图

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
                    │  判断渲染模式                         │
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
    │  Content 模式   │      │  SecretRef 模式  │      │  Kubeconfig     │
    │                 │      │                 │      │  模式            │
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             ▼                        ▼                        ▼
    ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
    │ 1. 构建模板数据 │       │ 1. 解析命名空间  │      │ 1. 解析模板变量  │
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
    │ 3. 执行渲染      │      │ 3. 提取数据     │      │ 3. 序列化 YAML  │
    │  tmpl.Execute() │      │  secret.Data[key]│     │  clientcmd.Write│
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             └────────────────────────┼────────────────────────┘
                                      │
                                      ▼
                    ┌──────────────────────────────────────┐
                    │  返回渲染结果                         │
                    │  return content, nil                 │
                    └──────────────────────────────────────┘
```

#### 4.5.3 核心接口定义

```go
// pkg/binaryinstaller/config_renderer.go

// ConfigRenderer 配置文件渲染器
type ConfigRenderer struct {
    client      client.Client
    funcMap     template.FuncMap
}

// NewConfigRenderer 创建配置文件渲染器
// client 用于读取 Secret (SecretRef 模式) 和生成 kubeconfig (KubeconfigTemplate 模式)
func NewConfigRenderer(client client.Client) *ConfigRenderer {
    return &ConfigRenderer{
        client: client,
        funcMap: template.FuncMap{
            "upper": strings.ToUpper,
            "lower": strings.ToLower,
            "trim":  strings.TrimSpace,
        },
    }
}

// RenderConfig 渲染单个配置文件模板 (统一入口，按 ConfigTemplateSpec 字段分发)
// 所有 render* 子方法统一签名 (ctx, tmpl, tmplCtx)，消除原设计签名不一致问题
func (r *ConfigRenderer) RenderConfig(ctx context.Context, tmpl ConfigTemplateSpec, tmplCtx manifest.TemplateContext) ([]byte, error) {
    switch {
    case tmpl.Content != "":
        return r.renderContentTemplate(ctx, tmpl, tmplCtx)
    case tmpl.SecretRef != nil:
        return r.renderSecretTemplate(ctx, tmpl, tmplCtx)
    case tmpl.KubeconfigTemplate != nil:
        return r.renderKubeconfigTemplate(ctx, tmpl, tmplCtx)
    }
    return nil, errors.New("no template content specified")
}

// renderContentTemplate 渲染内容模板 (使用 TemplateContext)
func (r *ConfigRenderer) renderContentTemplate(ctx context.Context, tmpl ConfigTemplateSpec, tmplCtx manifest.TemplateContext) ([]byte, error) {
    t, err := template.New("content").Funcs(r.funcMap).Parse(tmpl.Content)
    if err != nil {
        return nil, fmt.Errorf("failed to parse template: %w", err)
    }
    var buf bytes.Buffer
    if err := t.Execute(&buf, tmplCtx); err != nil {
        return nil, fmt.Errorf("failed to render template: %w", err)
    }
    return buf.Bytes(), nil
}

// renderSecretTemplate 从 Secret 获取内容 (使用 TemplateContext 渲染 namespace)
func (r *ConfigRenderer) renderSecretTemplate(ctx context.Context, tmpl ConfigTemplateSpec, tmplCtx manifest.TemplateContext) ([]byte, error) {
    secretRef := tmpl.SecretRef
    namespace, err := r.renderTemplateString(secretRef.Namespace, tmplCtx)
    if err != nil {
        return nil, fmt.Errorf("failed to render secret namespace: %w", err)
    }
    secret := &corev1.Secret{}
    if err := r.client.Get(ctx, types.NamespacedName{
        Name:      secretRef.Name,
        Namespace: namespace,
    }, secret); err != nil {
        return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretRef.Name, err)
    }
    data, ok := secret.Data[secretRef.Key]
    if !ok {
        return nil, fmt.Errorf("key %s not found in secret %s/%s", secretRef.Key, namespace, secretRef.Name)
    }
    return data, nil
}

// renderKubeconfigTemplate 动态生成 kubeconfig
func (r *ConfigRenderer) renderKubeconfigTemplate(ctx context.Context, tmpl ConfigTemplateSpec, tmplCtx manifest.TemplateContext) ([]byte, error) {
    kc := tmpl.KubeconfigTemplate
    clusterName, err := r.renderTemplateString(kc.ClusterName, tmplCtx)
    if err != nil {
        return nil, err
    }
    apiServer, err := r.renderTemplateString(kc.APIServer, tmplCtx)
    if err != nil {
        return nil, err
    }
    namespace, err := r.renderTemplateString(kc.Namespace, tmplCtx)
    if err != nil {
        return nil, err
    }
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

// renderTemplateString 渲染字符串中的模板变量 (返回 string + error，原设计漏报 error)
func (r *ConfigRenderer) renderTemplateString(s string, tmplCtx manifest.TemplateContext) (string, error) {
    if !strings.Contains(s, "{{") {
        return s, nil
    }
    t, err := template.New("str").Funcs(r.funcMap).Parse(s)
    if err != nil {
        return "", fmt.Errorf("failed to parse string template: %w", err)
    }
    var buf bytes.Buffer
    if err := t.Execute(&buf, tmplCtx); err != nil {
        return "", fmt.Errorf("failed to render string template: %w", err)
    }
    return buf.String(), nil
}

// renderConfigTemplates 渲染配置文件模板列表 (使用 TemplateContext)
// 支持 condition 字段: 评估 Go Template 表达式，"false"/空时跳过该模板
func (r *ConfigRenderer) renderConfigTemplates(ctx context.Context, templates []ConfigTemplateSpec, tmplCtx manifest.TemplateContext) (map[string][]byte, error) {
    configs := make(map[string][]byte)
    for _, tmpl := range templates {
        // 评估 condition：空 = 始终生成；"false"/空 = 跳过
        if tmpl.Condition != "" {
            result, err := r.renderTemplateString(tmpl.Condition, tmplCtx)
            if err != nil {
                return nil, fmt.Errorf("failed to evaluate condition for %s: %w", tmpl.Name, err)
            }
            trimmed := strings.TrimSpace(result)
            if trimmed == "false" || trimmed == "" {
                continue // 跳过此模板，不生成文件
            }
        }
        content, err := r.RenderConfig(ctx, tmpl, tmplCtx)
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

### 4.6 ConfigTemplateSpec forEach 动态多文件生成

**设计思路**：某些组件需要按数据条目动态生成多个配置文件（如 containerd 的 `hosts.toml`，每个 registry 一个文件）。`forEach` 机制允许单个 `configTemplate` 按迭代源展开为多个文件，每个文件的路径由 `pathTemplate` 动态渲染。

#### 4.6.1 ConfigTemplateSpec 扩展字段

```go
// ConfigTemplateSpec 扩展 (在现有字段基础上新增)
type ConfigTemplateSpec struct {
    // ... 现有字段保持不变 (Name, Path, Mode, Owner, Content, SecretRef, KubeconfigTemplate) ...

    // 新增：动态路径模板 (Go template 语法, 与 ForEach 配合使用)
    // 渲染时可访问全部 TemplateContext 变量 + 迭代变量 (.Key, .Value)
    // 示例: "{{cd "containerd" "registryConfigPath"}}/{{.Key}}/hosts.toml"
    PathTemplate string `json:"pathTemplate,omitempty"`

    // 新增：迭代源路径 (点分隔, 从 TemplateContext 中解析)
    // 支持 map[string]interface{} (按 key/value 迭代) 和 []interface{} (按 index/value 迭代)
    // 示例: "Config.Cluster.ContainerRuntime.Registry"
    ForEach string `json:"forEach,omitempty"`
}
```

**字段约束**：

| 条件 | 说明 |
|------|------|
| `ForEach != ""` 时 | `PathTemplate` 必填，`Path` 忽略 |
| `ForEach == ""` 时 | `Path` 必填，`PathTemplate` 忽略（原有行为） |
| `PathTemplate` 中 | 可访问全部 TemplateContext 变量 + `.Key` + `.Value` |
| `Content` 中 | 同上，可访问全部变量 |

#### 4.6.2 forEach 语义

`forEach` 值为**点分隔路径**，从 `TemplateContext` 中解析：

| forEach 值 | 解析路径 | 迭代类型 |
|------------|---------|---------|
| `"Config.Cluster.ContainerRuntime.Registry"` | `tmplCtx.Config.Cluster.ContainerRuntime.Registry` | `map[string]RegistryConfig` → 按 key/value 迭代 |
| `"Config.Cluster.ContainerRuntime.RegistryList"` | `tmplCtx.Config.Cluster.ContainerRuntime.RegistryList` | `[]RegistryConfig` → 按 index/value 迭代 |

#### 4.6.3 ForEachContext 迭代上下文

每次迭代创建一个包装上下文，**同时保留全部 TemplateContext 变量 + 迭代变量**：

```go
// ForEachContext 包装 TemplateContext + 迭代变量
// Go template 通过反射访问字段, 嵌入的 TemplateContext 字段可直接用 .ClusterName 等访问
type ForEachContext struct {
    manifest.TemplateContext   // 嵌入: 保留所有现有变量
    Key   string              // 当前迭代 key (map) 或 index (slice, 转 string)
    Value interface{}          // 当前迭代值
}
```

**模板变量访问规则**：

| 变量 | 来源 | 示例 |
|------|------|------|
| `{{.ClusterName}}` | TemplateContext（嵌入） | `my-cluster` |
| `{{.NodeIP}}` | TemplateContext（嵌入） | `192.168.1.10` |
| `{{.imageRegistry}}` | TemplateContext（嵌入） | `registry.example.com` |
| `{{.Key}}` | ForEachContext（迭代） | `harbor.example.com` |
| `{{.Value}}` | ForEachContext（迭代） | `map[host:... capabilities:...]` |
| `{{index .Value "host"}}` | 动态访问 Value 内部字段 | `harbor.example.com` |
| `{{index .Value "capabilities"}}` | 动态访问 Value 内部字段 | `[pull resolve]` |
| `{{cd "containerd" "root"}}` | 辅助函数访问 Config | `/var/lib/containerd` |

#### 4.6.4 渲染引擎核心代码

```go
// renderConfigTemplates 扩展: 支持 forEach 多文件展开
func (r *ConfigRenderer) renderConfigTemplates(
    ctx context.Context,
    templates []ConfigTemplateSpec,
    tmplCtx manifest.TemplateContext,
) (map[string][]byte, error) {
    configs := make(map[string][]byte)

    for _, tmpl := range templates {
        if tmpl.ForEach != "" {
            // 动态展开: forEach 迭代
            items, err := resolveForEach(tmpl.ForEach, tmplCtx)
            if err != nil {
                return nil, fmt.Errorf("forEach %q: %w", tmpl.ForEach, err)
            }
            for _, item := range items {
                // 构建迭代上下文: 保留全部 TemplateContext + 注入 Key/Value
                iterCtx := manifest.ForEachContext{
                    TemplateContext: tmplCtx,
                    Key:             item.Key,
                    Value:           item.Value,
                }

                // 渲染路径 (支持全部 TemplateContext 变量 + Key/Value)
                path, err := r.renderTemplateString(tmpl.PathTemplate, iterCtx)
                if err != nil {
                    return nil, fmt.Errorf("pathTemplate for key=%s: %w", item.Key, err)
                }

                // 渲染内容 (同上)
                content, err := r.renderContentTemplate(ctx, tmpl, iterCtx)
                if err != nil {
                    return nil, fmt.Errorf("content for key=%s: %w", item.Key, err)
                }

                configs[path] = content  // key 用渲染后的 path (而非 name)
            }
        } else {
            // 原有逻辑: 静态单文件
            content, err := r.RenderConfig(ctx, tmpl, tmplCtx)
            if err != nil {
                return nil, fmt.Errorf("template %s: %w", tmpl.Name, err)
            }
            configs[tmpl.Name] = content
        }
    }
    return configs, nil
}

// resolveForEach 按点分隔路径从 TemplateContext 中解析迭代源
func resolveForEach(path string, tmplCtx manifest.TemplateContext) ([]ForEachItem, error) {
    parts := strings.SplitN(path, ".", 2)
    if len(parts) != 2 {
        return nil, fmt.Errorf("invalid forEach path: %s", path)
    }

    var source interface{}
    switch parts[0] {
    case "Config":
        keys := strings.Split(parts[1], ".")
        if len(keys) < 2 {
            return nil, fmt.Errorf("Config path too short: %s", path)
        }
        // 通过反射访问 Config 的嵌套字段
        source = resolveConfigPath(tmplCtx.Config, keys...)
    default:
        return nil, fmt.Errorf("unsupported forEach root: %s", parts[0])
    }

    // 迭代: 支持 map 和 slice
    var items []ForEachItem
    switch v := source.(type) {
    case map[string]interface{}:
        for k, val := range v {
            items = append(items, ForEachItem{Key: k, Value: val})
        }
    case []interface{}:
        for i, val := range v {
            items = append(items, ForEachItem{Key: strconv.Itoa(i), Value: val})
        }
    default:
        return nil, fmt.Errorf("forEach source is not iterable: %T", source)
    }
    return items, nil
}

type ForEachItem struct {
    Key   string
    Value interface{}
}
```

#### 4.6.5 forEach 渲染流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    renderConfigTemplates 扩展流程                                │
└─────────────────────────────────────────────────────────────────────────────────┘

                    ┌──────────────────────────┐
                    │  遍历 configTemplates    │
                    │  for _, tmpl := range    │
                    │       templates          │
                    └────────────┬─────────────┘
                                 │
                    ┌────────────┴────────────┐
                    │  tmpl.ForEach != "" ?   │
                    └─────┬─────────────┬─────┘
                     Yes  │             │  No
                          ▼             ▼
          ┌───────────────────────┐  ┌──────────────────┐
          │  resolveForEach()     │  │  原有逻辑:        │
          │  解析点分隔路径        │  │  RenderConfig()   │
          │  → 获取迭代源          │  │  单文件渲染       │
          └───────────┬───────────┘  └──────────────────┘
                      │
                      ▼
          ┌───────────────────────┐
          │  迭代每个条目          │
          │  for _, item := range │
          │       items           │
          └───────────┬───────────┘
                      │
                      ▼
          ┌───────────────────────────────────┐
          │  构建 ForEachContext              │
          │  {                                │
          │    TemplateContext: tmplCtx,      │
          │    Key:   item.Key,               │
          │    Value: item.Value,             │
          │  }                                │
          │                                   │
          │  保留全部 TemplateContext 变量     │
          │  + 注入迭代变量 .Key / .Value      │
          └───────────┬───────────────────────┘
                      │
          ┌───────────┴───────────┐
          │                       │
          ▼                       ▼
┌──────────────────┐   ┌──────────────────┐
│ 渲染 pathTemplate│   │ 渲染 content     │
│ (动态路径)        │   │ (模板内容)       │
│                  │   │                  │
│ 可访问:           │   │ 可访问:          │
│ .ClusterName     │   │ .ClusterName     │
│ .NodeIP          │   │ .NodeIP          │
│ .Key             │   │ .Key             │
│ .Value           │   │ .Value           │
│ cd(...)          │   │ cd(...)          │
└────────┬─────────┘   └────────┬─────────┘
         │                      │
         └───────────┬──────────┘
                     │
                     ▼
          ┌───────────────────────────┐
          │  configs[path] = content  │
          │  (key 用渲染后的 path)     │
          └───────────────────────────┘
```

## 5. HelmInstaller 详细设计

### 5.1 核心组件架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                             HelmInstaller                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                         核心组件                                         │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                        ChartFetcher                             │    │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │    │
│  │  │  │  OCI Client  │  │  HTTP Client │  │  Local Loader       │    │    │    │
│  │  │  │  (OCI拉取)   │  │  (HTTP下载)   │  │  (本地加载)          │   │     │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                       ValuesRenderer                            │    │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │    │    │
│  │  │  │  Template    │  │  Values      │  │  Merge Strategy     │    │    │    │
│  │  │  │  Resolver    │  │  File Loader │  │  (合并策略)          │    │    │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │    │    │
│  │  └─────────────────────────────────────────────────────────────────┘    │    │
│  │                                                                         │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐    │    │
│  │  │                    Helm Action Executor                         │    │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │   │    │
│  │  │  │  Install     │  │  Upgrade     │  │  Rollback           │    │   │    │
│  │  │  │  Action      │  │  Action      │  │  Action             │    │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐                             │   │    │
│  │  │  │  Uninstall   │  │  Wait/Atomic │                             │   │    │
│  │  │  │  Action      │  │  Control     │                             │   │    │
│  │  │  └──────────────┘  └──────────────┘                             │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                        │    │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │    │
│  │  │                       HealthChecker                             │   │    │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │   │    │
│  │  │  │  PodReady    │  │  Endpoint    │  │  Custom Check       │    │   │    │
│  │  │  │  Check       │  │  Ready Check │  │                     │    │   │    │
│  │  │  └──────────────┘  └──────────────┘  └─────────────────────┘    │   │    │
│  │  └─────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                        │    │
│  └────────────────────────────────────────────────────────────────────────┘    │
│                                                                                │
└────────────────────────────────────────────────────────────────────────────────┘
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
                              │   入口函数        │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  1. 获取 Chart                       │
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
                    │  2. 校验 Chart Checksum              │
                    │  verifyChecksum(chart, expected)     │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         校验通过                校验失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  继续           │   │  返回错误        │
                    └────────┬────────┘   └─────────────────┘
                             │
                             ▼
                    ┌──────────────────────────────────────┐
                    │  3. 渲染 Values                      │
                    │  renderValues(ctx, helm.Values, opts)│
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  4. 加载自定义 Values 文件            │
                    │  loadValuesFiles(ctx, helm.Values    │
                    │                   Files, opts)       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  5. 合并 Values                      │
                    │  mergeValues(base, custom)           │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  6. 执行 Helm Action                 │
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
                    │  7. 执行健康检查                      │
                    │  runHealthCheck(ctx, helm.           │
                    │                HealthCheck, opts)    │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  PodReady       │  │  EndpointReady  │  │  Custom         │
          │  检查 Pod 状态   │  │  检查 Endpoint  │  │  自定义检查      │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                              ┌─────────┴───────────┐
                              │                     │
                         检查通过                检查失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  返回成功        │   │  返回错误       │
                    │  return nil     │   │  (触发回滚)      │
                    └─────────────────┘   └─────────────────┘
```

### 5.3 核心接口定义

**设计思路**：HelmInstaller 的接口设计也复用现有的 `manifest.TemplateContext`，与 BinaryInstaller 保持一致。DAG 调度器构建的 TemplateContext 直接传递给 HelmInstaller，用于渲染 Helm Values。
```go
// pkg/helminstaller/installer.go

// HelmInstaller Helm 组件安装器
type HelmInstaller struct {
    client     client.Client
    clientset  kubernetes.Interface  // 目标集群 clientset，健康检查 (Pod/Endpoint) 用
                                      // 注意: client.Client (controller-runtime) 无 CoreV1() 方法，
                                      // 健康检查需 typed clientset，故单独注入
    restConfig *rest.Config
    cacheDir   string
    httpClient *http.Client
    chartAuth  *kube.AuthConfig      // 镜像仓库认证，传给 kube.FetchChartUniversal
    logger     *bkev1beta1.BKELogger
}

// HelmInstallerConfig HelmInstaller 构建配置
type HelmInstallerConfig struct {
    Client     client.Client
    Clientset  kubernetes.Interface  // 目标集群 typed clientset
    RestConfig *rest.Config
    CacheDir   string
    HttpClient *http.Client
    ChartAuth  *kube.AuthConfig      // 复用 pkg/kube.AuthConfig
    Logger     *bkev1beta1.BKELogger
}

// NewHelmInstaller 创建 Helm 组件安装器
func NewHelmInstaller(cfg HelmInstallerConfig) *HelmInstaller {
    return &HelmInstaller{
        client:     cfg.Client,
        clientset:  cfg.Clientset,
        restConfig: cfg.RestConfig,
        cacheDir:   cfg.CacheDir,
        httpClient: cfg.HttpClient,
        chartAuth:  cfg.ChartAuth,
        logger:     cfg.Logger,
    }
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

**设计思路 — Helm `--wait` 与自定义 `healthCheck` 的关系**：

两者**同时生效**，是两层递进检查，非互斥关系：

| 层级 | 机制 | 触发时机 | 检查内容 | 局限 |
|------|------|---------|---------|------|
| **第一层: Helm `--wait`** | Helm SDK 原生 (`strategy.wait: true`) | `helm install/upgrade` 命令内部 | Pod Ready 状态（全部 Pod） | 仅检查 Pod Ready，不检查 Endpoint；要求全部 Ready 才通过 |
| **第二层: 自定义 `healthCheck`** | Installer 实现 (`healthCheck.enabled: true`) | Helm 命令返回后 | PodReady + EndpointReady + Custom | 支持 `minReady` 部分就绪；支持 Endpoint 检查；支持自定义命令 |

**为什么需要两层**：
1. **Helm `--wait` 的局限**：仅检查 Pod `Ready` condition，不验证 Service Endpoint 是否就绪（Pod Ready 但 Endpoint 未注册的情况存在）；要求全部 Pod Ready，不支持部分就绪场景。
2. **自定义 `healthCheck` 的补充**：支持 `minReady`（3 副本中 2 个就绪即可通过）、`EndpointReady`（验证服务可达性）、`Custom`（执行自定义检查命令）。
3. **执行顺序**：Helm `--wait` 先执行（在 `helm install/upgrade` 命令内部），通过后才执行自定义 `healthCheck`。如果 `--wait` 失败且 `atomic: true`，Helm 自动回滚，不会执行自定义 `healthCheck`。
4. **配置建议**：如果 `strategy.wait: true` 已启用，`healthCheck` 可仅配置 `EndpointReady` 或 `Custom` 检查（`PodReady` 已由 Helm `--wait` 覆盖），避免重复检查。

```go
// runHealthCheck 执行 Helm 安装后的自定义健康检查
// 与 Helm --wait 的关系: 两者同时生效, --wait 先执行 (Helm 命令内部),
// 自定义 healthCheck 后执行 (Helm 命令返回后), 互补非互斥
//
// 实现委托给共享 pkg/healthcheck 包 (见 7)，避免与 YamlInstaller 重复实现
// PodReady/EndpointReady/Custom 检查逻辑。
func (i *HelmInstaller) runHealthCheck(
    ctx context.Context,
    hc HealthCheckSpec,
    opts InstallOptions,
) error {
    // 委托共享包: 第 7 章 HealthCheck 共享包设计
    // HealthCheckSpec/PodReadyCheckSpec/EndpointReadyCheckSpec/CustomCheckSpec
    // 类型定义迁移到 pkg/healthcheck/types.go，Helm/YAML 共用
    return healthcheck.Run(ctx, i.clientset, hc)
}
```

```go
// getActionConfig 初始化 Helm Action 配置 (每次调用创建新实例, 避免状态残留)
func (i *HelmInstaller) getActionConfig(ctx context.Context, namespace string) (*action.Configuration, error) {
    actionConfig := new(action.Configuration)
    if err := actionConfig.Init(i.restConfig, namespace, "secret", func(format string, v ...interface{}) {
        // Helm 日志输出到标准日志
    }); err != nil {
        return nil, fmt.Errorf("failed to init helm action config: %w", err)
    }
    return actionConfig, nil
}

// uninstall 执行 Helm Uninstall
func (i *HelmInstaller) uninstall(ctx context.Context, actionConfig *action.Configuration,
    helm *HelmSpec, opts InstallOptions) error {
    client := action.NewUninstall(actionConfig)
    client.Wait = helm.Strategy.Wait
    client.Timeout, _ = time.ParseDuration(helm.Strategy.WaitTimeout)
    _, err := client.Run(helm.ReleaseName)
    if err != nil {
        return fmt.Errorf("helm uninstall failed: %w", err)
    }
    return nil
}

// rollback 执行 Helm Rollback
func (i *HelmInstaller) rollback(ctx context.Context, actionConfig *action.Configuration,
    helm *HelmSpec, opts InstallOptions) error {
    client := action.NewRollback(actionConfig)
    client.Wait = helm.Strategy.Wait
    client.Timeout, _ = time.ParseDuration(helm.Strategy.WaitTimeout)
    if err := client.Run(helm.ReleaseName); err != nil {
        return fmt.Errorf("helm rollback failed: %w", err)
    }
    return nil
}

// 复用现有 pkg/kube/chart.go 的 Chart 拉取能力，避免重复实现 OCI/HTTP/本地加载。
//
// 现有可复用函数 (pkg/kube/chart.go):
//   - FetchChartUniversal(chartRepo, chartName, version, auth, logger) (*chart.Chart, error)
//     支持 OCI Registry 与传统 HTTP 仓库，自动识别 oci:// 前缀 (chart.go:442-465)
//   - FetchChartOCI(...) (chart.go:525-582)
//   - FetchChartTraditional(...) (chart.go:688-728)
//   - initActionConfig(namespace) (*action.Configuration, error) (chart.go:113-121)
//   - releaseExists(actionConfig, releaseName, ns) (bool, error) (chart.go:123-136)
//
// HelmInstaller 不再直接调用 ocipuller/loader，而是通过 kube.FetchChartUniversal 拉取。
// 这与现有 addon helm 部署链路 (pkg/kube/chart.go installChartAddon) 保持一致，
// 后续可将 chart.go 中通用部分进一步抽取到 pkg/helminstaller 共享。

// getChartFromOCI 从 OCI Registry 拉取 Chart (复用 kube.FetchChartOCI)
func (i *HelmInstaller) getChartFromOCI(ctx context.Context, ociSpec *OCIChartSpec) (*chart.Chart, error) {
    // 复用 pkg/kube/chart.go: FetchChartOCI(repository, tag, auth, logger)
    // auth 来自集群镜像仓库认证配置 (由 HelmInstaller.chartAuth 提供)
    return kube.FetchChartOCI(ociSpec.Repository, ociSpec.Tag, i.chartAuth, i.logger.NormalLogger)
}

// getChartFromURL 从 HTTP URL 下载 Chart (复用 kube.FetchChartTraditional)
func (i *HelmInstaller) getChartFromURL(ctx context.Context, url string) (*chart.Chart, error) {
    // 复用 pkg/kube/chart.go: FetchChartTraditional(repo, name, version, auth, logger)
    // url 形如 https://repo/charts/name-version.tgz，解析出 repo/name/version 后调用
    repo, name, version := parseChartURL(url)
    return kube.FetchChartTraditional(repo, name, version, i.chartAuth, i.logger.NormalLogger)
}

// getChartFromLocal 从本地路径加载 Chart (复用 helm loader.Load)
func (i *HelmInstaller) getChartFromLocal(ctx context.Context, path string) (*chart.Chart, error) {
    ch, err := loader.Load(path)
    if err != nil {
        return nil, fmt.Errorf("failed to load chart from %s: %w", path, err)
    }
    return ch, nil
}
```

> 说明：原 `listPods`/`checkPodReady`/`checkEndpointReady`/`checkCustom`/`runHealthCheck` 的实现逻辑已移至 **第 7 章 HealthCheck 共享包设计**（参数化为 `kubernetes.Interface`，Helm/YAML 共用）。HelmInstaller 仅保留 `runHealthCheck` 一行委托（见上方）。

`HelmSpec` 提供两种 Values 来源，合并优先级从低到高：
```
Chart 默认值 → ValuesFiles[0] → ValuesFiles[1] → ... → Values 内联字段 (最高优先级)
```

1. **Values（内联）**：`map[string]interface{}`，直接在 CRD YAML 中定义，支持模板变量渲染（如 `{{componentVersion}}`）。优先级最高，覆盖所有文件值。
2. **ValuesFiles（外部文件）**：`[]string`，文件路径列表，路径支持模板变量（如 `values-{{arch}}.yaml`）。从控制器 Pod 本地文件系统读取（通常通过 ConfigMap 挂载）。逐个加载后按顺序合并，后者覆盖前者。
3. **mergeValues 合并规则**：递归合并 map（src 覆盖 dst 同名键），非 map 类型（list/string/int）整体替换而非追加。与 Helm 自身 `--set` 和 `-f` 合并行为一致。
4. **文件加载失败为警告而非错误**：ValuesFiles 是可选补充配置，文件可能按条件存在（如 `values-{{arch}}.yaml` 在某些架构上不存在），缺失时使用内联 Values 即可。

**设计思路 — 本地文件系统 vs ConfigMap/URL**：

当前从控制器 Pod 本地文件系统读取 Values 文件。在 Kubernetes 控制器场景下，文件通常通过 ConfigMap 或 Secret 挂载到 Pod 中。选择本地文件而非直接读取 ConfigMap API 的原因是：
- Values 文件可能很大（超过 etcd 1MB 限制），ConfigMap 不适合存储大型 YAML
- 文件路径支持模板变量渲染，动态选择不同文件（如 `values-{{arch}}.yaml`）
- 后续可扩展支持 HTTP URL 和 ConfigMap 引用作为 Values 来源

```go
// renderValues 渲染 Values 中的模板变量
// 将 helm.Values (map[string]interface{}) 中的字符串值进行模板变量替换
// 支持 {{componentVersion}}, {{clusterName}}, {{imageRegistry}} 等变量
func (i *HelmInstaller) renderValues(
    ctx context.Context,
    values map[string]interface{},
    tmplCtx manifest.TemplateContext,
) (map[string]interface{}, error) {
    rendered := make(map[string]interface{})
    for key, val := range values {
        switch v := val.(type) {
        case string:
            // 字符串值: 渲染模板变量
            renderedVal, err := i.renderTemplateString(v, tmplCtx)
            if err != nil {
                return nil, fmt.Errorf("failed to render value %q: %w", key, err)
            }
            rendered[key] = renderedVal
        case map[string]interface{}:
            // 嵌套 map: 递归渲染
            nested, err := i.renderValues(ctx, v, tmplCtx)
            if err != nil {
                return nil, err
            }
            rendered[key] = nested
        default:
            // 非字符串类型 (int/bool/float/list): 原样保留
            rendered[key] = val
        }
    }
    return rendered, nil
}

// renderTemplateString 渲染字符串中的模板变量
// 支持 {{componentVersion}} 简单替换 和 {{.clusterName}} Go template 两种语法
func (i *HelmInstaller) renderTemplateString(s string, tmplCtx manifest.TemplateContext) (string, error) {
    if !strings.Contains(s, "{{") {
        return s, nil // 无模板变量, 直接返回
    }
    // 简单变量替换: {{componentVersion}} → tmplCtx.ComponentVersion
    result := s
    result = strings.ReplaceAll(result, "{{componentVersion}}", tmplCtx.ComponentVersion)
    result = strings.ReplaceAll(result, "{{clusterName}}", tmplCtx.ClusterName)
    result = strings.ReplaceAll(result, "{{namespace}}", tmplCtx.Namespace)
    result = strings.ReplaceAll(result, "{{kubernetesVersion}}", tmplCtx.KubernetesVersion)
    result = strings.ReplaceAll(result, "{{imageRegistry}}", tmplCtx.ImageRegistry)
    return result, nil
}

// loadValuesFile 加载自定义 Values 文件
// 1. 渲染文件路径中的模板变量 (如 values-{{arch}}.yaml)
// 2. 从控制器本地文件系统读取文件 (通常通过 ConfigMap 挂载)
// 3. 解析 YAML 为 map[string]interface{}
func (i *HelmInstaller) loadValuesFile(
    ctx context.Context,
    valuesFile string,
    tmplCtx manifest.TemplateContext,
) (map[string]interface{}, error) {
    // 1. 渲染文件名中的模板变量
    renderedFile, err := i.renderTemplateString(valuesFile, tmplCtx)
    if err != nil {
        return nil, fmt.Errorf("failed to render values file path %s: %w", valuesFile, err)
    }

    // 2. 读取文件内容
    data, err := os.ReadFile(renderedFile)
    if err != nil {
        return nil, fmt.Errorf("failed to read values file %s: %w", renderedFile, err)
    }

    // 3. 解析 YAML
    values := make(map[string]interface{})
    if err := yaml.Unmarshal(data, &values); err != nil {
        return nil, fmt.Errorf("failed to parse values file %s: %w", renderedFile, err)
    }

    return values, nil
}

// mergeValues 合并两份 Values (dst 被 src 覆盖)
// 策略: 递归合并 map, src 中的值覆盖 dst 中的同名键
// 非 map 类型 (list/string/int/bool) 整体替换, 不追加
// 与 Helm 自身 --set 和 -f 合并行为一致
func mergeValues(dst, src map[string]interface{}) map[string]interface{} {
    result := make(map[string]interface{})
    for k, v := range dst {
        result[k] = v
    }
    for k, v := range src {
        if dstMap, ok := dst[k].(map[string]interface{}); ok {
            if srcMap, ok := v.(map[string]interface{}); ok {
                // 两边都是 map: 递归合并
                result[k] = mergeValues(dstMap, srcMap)
                continue
            }
        }
        // src 覆盖 dst (非 map 类型整体替换, list 不追加)
        result[k] = v
    }
    return result
}
```

#### 5.3.1 Values / ValuesFiles 使用样例

**场景 1：values + valuesFiles 同时使用（按架构区分）**

```yaml
# bke-manifests/coredns/v1.11.1/component.yaml
spec:
  name: coredns
  type: helm
  version: v1.11.1
  helm:
    chart:
      oci:
        repository: "registry.openfuyao.cn/charts/coredns"
        tag: "v1.11.1"
    namespace: kube-system
    releaseName: coredns
    
    # 内联 Values (优先级最高, 覆盖文件中的同名键)
    values:
      image:
        repository: "registry.openfuyao.cn/coredns/coredns"
        tag: "{{componentVersion}}"
      replicaCount: 2                        # 覆盖 values-base.yaml 中的 replicaCount: 1
    
    # 外部 Values 文件 (按顺序合并, 后者覆盖前者)
    valuesFiles:
      - "/etc/bke-values/coredns/values-base.yaml"       # 基础配置
      - "/etc/bke-values/coredns/values-{{arch}}.yaml"   # 按架构区分
```

**控制器 Pod 中的文件结构**（通常通过 ConfigMap 挂载）：

```
/etc/bke-values/coredns/
├── values-base.yaml          # 基础配置 (所有架构共享)
├── values-amd64.yaml         # amd64 架构特定配置
└── values-arm64.yaml         # arm64 架构特定配置
```

**values-base.yaml 内容**：

```yaml
image:
  pullPolicy: IfNotPresent
resources:
  limits:
    cpu: "100m"
    memory: "128Mi"
  requests:
    cpu: "50m"
    memory: "64Mi"
replicaCount: 1
service:
  type: ClusterIP
```

**values-amd64.yaml 内容**：

```yaml
nodeSelector:
  kubernetes.io/arch: amd64
image:
  repository: "registry.openfuyao.cn/coredns/coredns-amd64"  # amd64 专用镜像
```

**合并结果**（优先级：Chart 默认 < values-base.yaml < values-amd64.yaml < 内联 values）：

```yaml
image:
  repository: "registry.openfuyao.cn/coredns/coredns"  # 内联 values 覆盖 amd64 文件
  tag: "v1.11.1"                                        # 内联 values (模板渲染后)
  pullPolicy: IfNotPresent                              # 来自 values-base.yaml
resources:
  limits:
    cpu: "100m"                                         # 来自 values-base.yaml
    memory: "128Mi"                                     # 来自 values-base.yaml
  requests:
    cpu: "50m"                                          # 来自 values-base.yaml
    memory: "64Mi"                                      # 来自 values-base.yaml
replicaCount: 2                                          # 内联 values 覆盖 base 的 1
service:
  type: ClusterIP                                        # 来自 values-base.yaml
nodeSelector:
  kubernetes.io/arch: amd64                              # 来自 values-amd64.yaml
```

**场景 2：仅 values 内联（简单场景）**

```yaml
helm:
  values:
    image:
      repository: "registry.openfuyao.cn/coredns/coredns"
      tag: "{{componentVersion}}"
    replicaCount: 2
    resources:
      limits:
        cpu: "100m"
        memory: "128Mi"
```

无需外部文件，所有配置内联在 CRD 中。适用于配置简单、无需按环境区分的场景。

**场景 3：仅 valuesFiles（配置外置）**

```yaml
helm:
  valuesFiles:
    - "/etc/bke-values/coredns/values-{{os}}.yaml"
```

**values-centos.yaml** 和 **values-ubuntu.yaml** 差异示例：

```yaml
# values-centos.yaml
initContainers:
  - name: sysctl
    image: "registry.openfuyao.cn/busybox:centos"
    command: ["sysctl", "-w", "net.core.somaxconn=65535"]

# values-ubuntu.yaml
initContainers:
  - name: sysctl
    image: "registry.openfuyao.cn/busybox:ubuntu"
    command: ["sysctl", "-w", "net.core.somaxconn=65535"]
```

适用于配置复杂、需按操作系统区分的场景。内联 `values` 留空，全部配置由文件提供。
### 5.4 健康检查

HelmInstaller 的健康检查通过 `runHealthCheck` 方法委托共享 `pkg/healthcheck` 包执行（`return healthcheck.Run(ctx, i.clientset, hc)`）。PodReady/EndpointReady/Custom 三种检查的接口定义与实现、与 Helm `--wait` 的关系、类型归属等详见 **第 7 章 HealthCheck 共享包设计**。

### 5.5 Hooks 执行引擎

**设计思路 — PreInstallHooks 与 PreUninstallHooks 统一设计**：

Helm 组件支持两种钩子：`PreInstallHooks`（安装/升级前执行）和 `PreUninstallHooks`（卸载前执行）。两者共享相同的执行逻辑（创建 Job → 等待完成 → 清理），仅触发时机不同。统一设计为 `HookExecutor` 接口，避免重复实现。

**钩子执行流程**：

```
Hooks 执行流程:
┌─────────────────────────────────────────────────────────────┐
│ 1. 遍历 hooks 列表                                           │
│    - 过滤支持的 hook.Type (目前仅 "Job")                     │
├─────────────────────────────────────────────────────────────┤
│ 2. 渲染 Hook Manifest                                       │
│    - 支持模板变量 ({{clusterName}}, {{namespace}} 等)        │
├─────────────────────────────────────────────────────────────┤
│ 3. 创建 Job 资源                                            │
│    - Job 名称: pre-install-<hookName>-<timestamp>           │
│    - 或: pre-uninstall-<hookName>-<timestamp>               │
├─────────────────────────────────────────────────────────────┤
│ 4. 等待 Job 完成                                            │
│    - 超时: 5 分钟 (可配置)                                   │
│    - 失败处理: 清理 Job 后返回错误                            │
├─────────────────────────────────────────────────────────────┤
│ 5. 清理 Job 资源                                            │
│    - 删除 Job (保留 Pod 日志用于调试)                         │
└─────────────────────────────────────────────────────────────┘
```

**代码实现**：

```go
// pkg/helmhooks/executor.go

// HookExecutor 钩子执行器接口
type HookExecutor interface {
    Execute(ctx context.Context, hooks []HookSpec, opts HookOptions) error
}

// JobHookExecutor Job 类型钩子执行器
type JobHookExecutor struct {
    clientset kubernetes.Interface
    renderer  *HookRenderer
    logger    *bkev1beta1.BKELogger
}

// HookOptions 钩子执行选项
type HookOptions struct {
    Namespace string
    Timeout   time.Duration
    Prefix    string  // "pre-install" 或 "pre-uninstall"
    // 模板变量
    TemplateData map[string]interface{}
}

func (e *JobHookExecutor) Execute(ctx context.Context, hooks []HookSpec, opts HookOptions) error {
    for _, hook := range hooks {
        if hook.Type != "Job" {
            e.logger.Warn("unsupported hook type: %s, skipping", hook.Type)
            continue
        }
        
        // 1. 渲染 Hook Manifest
        manifest, err := e.renderer.Render(hook.Manifest, opts.TemplateData)
        if err != nil {
            return fmt.Errorf("render hook %s manifest: %w", hook.Name, err)
        }
        
        // 2. 创建 Job
        jobName := fmt.Sprintf("%s-%s-%d", opts.Prefix, hook.Name, time.Now().Unix())
        if err := e.createJob(ctx, jobName, manifest, opts.Namespace); err != nil {
            return fmt.Errorf("create hook job %s: %w", jobName, err)
        }
        
        // 3. 等待 Job 完成
        if err := e.waitForJob(ctx, jobName, opts.Namespace, opts.Timeout); err != nil {
            e.cleanupJob(ctx, jobName, opts.Namespace)
            return fmt.Errorf("hook job %s failed: %w", jobName, err)
        }
        
        // 4. 清理 Job
        e.cleanupJob(ctx, jobName, opts.Namespace)
    }
    return nil
}

// HookRenderer 钩子 Manifest 渲染器
type HookRenderer struct {
    funcMap template.FuncMap
}

func (r *HookRenderer) Render(manifest string, data map[string]interface{}) (string, error) {
    t, err := template.New("hook").Funcs(r.funcMap).Parse(manifest)
    if err != nil {
        return "", err
    }
    var buf bytes.Buffer
    if err := t.Execute(&buf, data); err != nil {
        return "", err
    }
    return buf.String(), nil
}
```

**在 HelmInstaller 中集成**：

```go
// pkg/helminstaller/installer.go

func (i *HelmInstaller) install(ctx context.Context, actionConfig *action.Configuration,
    helm *HelmSpec, opts InstallOptions) error {
    
    // 1. 执行 PreInstallHooks
    if len(helm.PreInstallHooks) > 0 {
        hookOpts := HookOptions{
            Namespace:    helm.Namespace,
            Timeout:      5 * time.Minute,
            Prefix:       "pre-install",
            TemplateData: buildHookTemplateData(opts),
        }
        if err := i.hookExecutor.Execute(ctx, helm.PreInstallHooks, hookOpts); err != nil {
            return fmt.Errorf("pre-install hooks failed: %w", err)
        }
    }
    
    // 2. 执行 Helm Install
    client := action.NewInstall(actionConfig)
    // ... 原有逻辑
}

func (i *HelmInstaller) uninstall(ctx context.Context, actionConfig *action.Configuration,
    helm *HelmSpec, opts InstallOptions) error {
    
    // 1. 执行 PreUninstallHooks
    if len(helm.PreUninstallHooks) > 0 {
        hookOpts := HookOptions{
            Namespace:    helm.Namespace,
            Timeout:      5 * time.Minute,
            Prefix:       "pre-uninstall",
            TemplateData: buildHookTemplateData(opts),
        }
        if err := i.hookExecutor.Execute(ctx, helm.PreUninstallHooks, hookOpts); err != nil {
            return fmt.Errorf("pre-uninstall hooks failed: %w", err)
        }
    }
    
    // 2. 执行 Helm Uninstall
    client := action.NewUninstall(actionConfig)
    // ... 原有逻辑
}
```

**使用示例**：

```yaml
helm:
  releaseName: coredns
  namespace: kube-system
  
  # 安装前钩子：备份现有配置
  preInstallHooks:
    - name: backup-config
      type: Job
      manifest: |
        apiVersion: batch/v1
        kind: Job
        spec:
          template:
            spec:
              containers:
              - name: backup
                image: bitnami/kubectl:latest
                command: ["/bin/sh", "-c", "kubectl get configmap coredns -n kube-system -o yaml > /backup/coredns.yaml"]
  
  # 卸载前钩子：清理依赖资源
  preUninstallHooks:
    - name: cleanup-dependencies
      type: Job
      manifest: |
        apiVersion: batch/v1
        kind: Job
        spec:
          template:
            spec:
              containers:
              - name: cleanup
                image: bitnami/kubectl:latest
                command: ["/bin/sh", "-c", "kubectl delete configmap coredns-custom -n kube-system --ignore-not-found"]
```

---

## 6. YamlInstaller 详细设计

### 6.1 核心组件架构

```
┌─────────────────────────────────────────────────────────────────┐
│                    YamlInstaller                                │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────────┐    ┌──────────────────┐                   │
│  │ManifestDownloader│    │  YAML Parser     │                   │
│  │                  │    │                  │                   │
│  │ • ManifestStore  │    │ • 多文档解析      │                   │
│  │ • bundle文件加载  │    │ • GVK 识别       │                   │
│  │ • 内联Resources  │    │ • 资源分组        │                   │
│  └────────┬─────────┘    └────────┬─────────┘                   │
│           │                       │                             │
│           ▼                       ▼                             │
│  ┌──────────────────────────────────────────┐                   │
│  │       ApplyStrategy Engine               │                   │
│  │                                          │                   │
│  │ • ServerSideApply (默认, 声明式字段管理)   │                  │
│  │ • Replace (删除+重建)                    │                   │
│  │ • CreateOnly (仅创建)                    │                   │
│  └──────────────────┬───────────────────────┘                  │
│                     │                                          │
│                     ▼                                          │
│  ┌──────────────────────────────────────────┐                  │
│  │            K8s Applier                   │                  │
│  │                                          │                  │
│  │ • 应用清单到目标集群                      │                   │
│  │ • Prune 裁剪废弃资源 (按 label selector)  │                   │
│  └──────────────────────────────────────────┘                   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 6.2 YamlInstaller 执行流程图

**设计思路**：YamlInstaller 的执行流程分为 4 个主要步骤：加载清单、应用清单、健康检查、返回结果。与 BinaryInstaller（7 步）和 HelmInstaller（7 步）相比更简单——无制品下载/SSH 执行/Helm SDK 等复杂子层，仅 Store 加载 + Applier 应用 + healthcheck。

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         YamlInstaller 执行流程                                   │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │   Apply()        │
                              │   入口函数        │
                              └────────┬─────────┘
                                       │
                                       ▼
                     ┌───────────────────────────────────────┐
                     │  1. 加载清单                           │
                     │  store.GetComponentManifests(         │
                     │    ctx, name, version, tmplCtx)       │
                     │  → BundleStore → CollectComponent     │
                     │    Manifests (bundle文件+内联Resources)│
                     └────────────────────┬──────────────────┘
                                          │
                                          ▼
                     ┌────────────────────────────────────────┐
                     │  2. 应用清单                            │
                     │  applier.ApplyComponent(ctx, pkg)      │
                     │  → ClusterApplier → ApplyYaml          │
                     │    (ServerSideApply/Replace/CreateOnly)│
                     └────────────────────┬───────────────────┘
                                          │
                                          ▼
                     ┌──────────────────────────────────────┐
                     │  3. 健康检查                          │
                     │  healthcheck.Run(ctx, clientset, hc) │
                     │  (见第 7 章, hc.Enabled=true 时执行)  │
                     └────────────────────┬─────────────────┘
                                          │
                               ┌──────────┴──────────┐
                               │                     │
                          检查通过                检查失败
                               │                     │
                               ▼                     ▼
                     ┌─────────────────┐   ┌─────────────────┐
                     │  返回成功        │   │  返回错误       │
                     │  return nil     │   │  return err     │
                     └─────────────────┘   └─────────────────┘
```

### 6.3 核心接口定义

**设计思路 — YAML 组件两层模型（与 Binary/Helm 对称）**：

YAML 组件采用与 Binary/Helm 对称的两层结构：`YamlComponentExecutor`（dagexec 调度层，见 9.4.3.3）+ `YamlInstaller`（`pkg/yamlinstaller` 引擎层，本节）。`YamlInstaller` 持有 `manifest.Applier`，负责清单 Apply + 健康检查；与 `BinaryInstaller`/`HelmInstaller` 命名对称。区别仅在 `YamlInstaller` 内部更简单——无 SSH/下载/缓存等子层，仅 Apply + healthcheck。

| 层 | Binary | Helm | YAML |
|----|--------|------|------|
| dagexec 执行器 | `BinaryComponentExecutor` | `HelmComponentExecutor` | `YamlComponentExecutor`（见 9.4.3.3） |
| 独立包引擎 | `binaryinstaller.BinaryInstaller` | `helminstaller.HelmInstaller` | `yamlinstaller.YamlInstaller`（本节） |

**Installer（引擎层）完成的逻辑**（`YamlInstaller.Apply`）：
- 构建 ComponentPackage → `applier.ApplyComponent()` → 健康检查（PodReady/EndpointReady/Custom）

**Executor（调度层）完成的逻辑**（`YamlComponentExecutor`，见 9.4.3.3）：
- 获取 ComponentVersion → VersionContext 判断是否需要执行 → 委托 `YamlInstaller.Apply()` → 处理失败策略
- 无节点级调度（应用到集群而非单节点）
- 无回滚机制（SSA 天然支持幂等，重新 Apply 上一版本即可回滚）

```go
// pkg/yamlinstaller/installer.go

// YamlInstaller YAML 组件安装器（引擎层，对称 BinaryInstaller/HelmInstaller）
// 持有 manifest.Store (加载清单) + manifest.Applier (应用清单)，负责清单 Apply + 健康检查。
// 相比 BinaryInstaller 无 SSH/下载/缓存子层，逻辑较简单。
type YamlInstaller struct {
    store   manifest.Store            // 加载清单 (复用 BundleStore.GetComponentManifests)
    applier manifest.Applier          // 应用清单 (现有 manifest.ClusterApplier 实现)
    logger  *bkev1beta1.BKELogger
}

type YamlInstallerConfig struct {
    Store   manifest.Store
    Applier manifest.Applier
    Logger  *bkev1beta1.BKELogger
}

func NewYamlInstaller(cfg YamlInstallerConfig) *YamlInstaller {
    return &YamlInstaller{store: cfg.Store, applier: cfg.Applier, logger: cfg.Logger}
}

// Apply 应用 YAML 清单 + 健康检查 (由 YamlComponentExecutor 委托调用)
func (i *YamlInstaller) Apply(ctx context.Context, cv *configv1alpha1.ComponentVersion, execCtx *ExecutionContext) error {
    // 通过 manifest.Store 加载清单 (复用 BundleStore.GetComponentManifests 逻辑:
    // bundle 文件 (components/<name>/<version>/*.yaml) + cv.Spec.Resources 内联清单)
    pkg, err := i.store.GetComponentManifests(ctx, cv.Spec.Name, cv.Spec.Version, execCtx.TemplateContext)
    if err != nil {
        return fmt.Errorf("get manifests for %s: %w", cv.Spec.Name, err)
    }

    // 应用 Manifest
    if err := i.applier.ApplyComponent(ctx, pkg); err != nil {
        return fmt.Errorf("apply manifests for %s: %w", cv.Spec.Name, err)
    }

    // 健康检查 (应用清单后验证 Pod/Endpoint 就绪)
    // 复用共享 pkg/healthcheck 包 (见 7)，避免与 HelmInstaller 重复
    if cv.Spec.YAML != nil && cv.Spec.YAML.HealthCheck != nil && cv.Spec.YAML.HealthCheck.Enabled {
        if err := healthcheck.Run(ctx, execCtx.TargetClient, *cv.Spec.YAML.HealthCheck); err != nil {
            return fmt.Errorf("health check failed for %s: %w", cv.Spec.Name, err)
        }
    }
    return nil
}
```

> 健康检查实现委托共享 `pkg/healthcheck` 包（`healthcheck.Run(ctx, execCtx.TargetClient, *cv.Spec.YAML.HealthCheck)`）。共享包的接口定义与 PodReady/EndpointReady/Custom 检查实现详见 **第 7 章 HealthCheck 共享包设计**。

**设计思路 — YamlInstaller 健康检查 clientset 来源**：

`YamlInstaller.Apply` 中调用 `healthcheck.Run(ctx, execCtx.TargetClient, ...)` 需要 `kubernetes.Interface` (typed clientset)。当前 `YamlInstallerConfig` 仅注入 `Store` + `Applier`，未注入 clientset。解决方案：通过 `ExecutionContext.TargetClient` 传递，无需修改 `YamlInstallerConfig`。`ExecutionContext.TargetClient` 由 controllers 层在 `buildExecutionContext` 中注入（见 9.1.3 节），是目标集群的 typed clientset。YamlInstaller 通过 `execCtx.TargetClient` 访问，无需在 `YamlInstallerConfig` 中重复注入。这与 `HelmInstaller` 的设计一致——`HelmInstaller` 在 Config 中直接注入 `Clientset`，因为 HelmInstaller 不接收 `ExecutionContext`。

### 6.4 清单下载与缓存

**设计思路**：`yaml.manifests[].url` 引用的外部 YAML 清单文件需要下载、缓存和校验。与 BinaryInstaller 的制品下载类似，但更简单——清单文件体积远小于二进制制品，且格式固定为 YAML。

**关键设计点**：
- **缓存策略**：按 URL + Checksum 缓存到本地 (`/var/cache/bke/manifests/`)，避免重复下载
- **多文档解析**：单个 YAML 文件可能包含多个 K8s 资源 (用 `---` 分隔)，需使用 `yaml.NewYAMLOrJSONDecoder` 逐文档解析
- **Checksum 校验**：下载后校验 SHA256，确保清单完整性
- **离线支持**：缓存命中时直接使用本地文件，无需网络访问

**流程图**：

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    YAML 清单下载与缓存流程                                        │
└─────────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────┐
  │  YamlInstaller.Apply()               │
  │  遍历 yaml.manifests[]               │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  检查缓存                             │
  │  cacheKey = sha256(url + checksum)   │
  │  cache.Get(cacheKey)                 │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
     命中       未命中
        │         │
        │         ▼
        │  ┌────────────────────────────┐
        │  │  HTTP GET(url)             │
        │  │  下载清单文件               │
        │  └──────────┬─────────────────┘
        │             │
        │             ▼
        │  ┌────────────────────────────┐
        │  │  校验 Checksum             │
        │  │  sha256(data) == expected? │
        │  └──────────┬─────────────────┘
        │             │
        │        ┌────┴────┐
        │        │         │
        │     通过       失败
        │        │         │
        │        ▼         ▼
        │  ┌────────┐ ┌──────────┐
        │  │保存缓存 │ │返回错误  │
        │  └───┬────┘ └──────────┘
        │      │
        └──────┼──────┐
               │      │
               ▼      ▼
  ┌──────────────────────────────────────┐
  │  合并所有清单内容                     │
  │  → []byte (多文档 YAML)              │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  YAML 多文档解析                      │
  │  yaml.NewYAMLOrJSONDecoder           │
  │  → []unstructured.Unstructured       │
  └──────────────────────────────────────┘
```

**代码实现**：

```go
// pkg/yamlinstaller/manifest_downloader.go

// ManifestDownloader 管理 YAML 清单的下载与缓存
type ManifestDownloader struct {
    cacheDir   string
    httpClient *http.Client
}

// NewManifestDownloader 创建清单下载器
func NewManifestDownloader(cacheDir string, httpClient *http.Client) (*ManifestDownloader, error) {
    if err := os.MkdirAll(cacheDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create manifest cache directory %s: %w", cacheDir, err)
    }
    return &ManifestDownloader{cacheDir: cacheDir, httpClient: httpClient}, nil
}

// DownloadManifest 下载单个清单文件 (带缓存)
func (d *ManifestDownloader) DownloadManifest(ctx context.Context, ref ManifestRef) ([]byte, error) {
    cacheKey := d.computeCacheKey(ref.URL, ref.Checksum)
    cachePath := filepath.Join(d.cacheDir, cacheKey)

    // 1. 检查缓存
    if data, err := os.ReadFile(cachePath); err == nil {
        return data, nil
    }

    // 2. HTTP 下载
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.URL, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request for %s: %w", ref.URL, err)
    }
    resp, err := d.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to download manifest from %s: %w", ref.URL, err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("failed to download manifest from %s: HTTP %d", ref.URL, resp.StatusCode)
    }
    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read manifest body from %s: %w", ref.URL, err)
    }

    // 3. Checksum 校验
    if ref.Checksum != "" {
        if err := verifyChecksum(data, ref.Checksum); err != nil {
            return nil, fmt.Errorf("checksum verification failed for %s: %w", ref.URL, err)
        }
    }

    // 4. 保存到缓存
    if err := os.WriteFile(cachePath, data, 0644); err != nil {
        return nil, fmt.Errorf("failed to cache manifest %s: %w", ref.URL, err)
    }

    return data, nil
}

// DownloadManifests 下载清单文件列表，合并为多文档 YAML
func (d *ManifestDownloader) DownloadManifests(ctx context.Context, refs []ManifestRef) ([]byte, error) {
    var combined bytes.Buffer
    for i, ref := range refs {
        data, err := d.DownloadManifest(ctx, ref)
        if err != nil {
            return nil, fmt.Errorf("failed to download manifest[%d] %s: %w", i, ref.URL, err)
        }
        if i > 0 {
            combined.WriteString("\n---\n")
        }
        combined.Write(data)
    }
    return combined.Bytes(), nil
}

// ParseMultiDocYAML 解析多文档 YAML 为 unstructured 对象列表
func ParseMultiDocYAML(data []byte) ([]unstructured.Unstructured, error) {
    var resources []unstructured.Unstructured
    decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
    for {
        var obj unstructured.Unstructured
        err := decoder.Decode(&obj)
        if err == io.EOF {
            break
        }
        if err != nil {
            return nil, fmt.Errorf("failed to parse YAML document: %w", err)
        }
        if obj.Object == nil {
            continue // 跳过空文档
        }
        resources = append(resources, obj)
    }
    return resources, nil
}

func (d *ManifestDownloader) computeCacheKey(url, checksum string) string {
    h := sha256.New()
    h.Write([]byte(url))
    if checksum != "" {
        h.Write([]byte(checksum))
    }
    return fmt.Sprintf("%x.yaml", h.Sum(nil))
}
```

**YamlInstaller 集成**：

```go
// pkg/yamlinstaller/installer.go 扩展

type YamlInstaller struct {
    store            manifest.Store
    applier          manifest.Applier
    manifestDownloader *ManifestDownloader  // 新增：清单下载器
    logger           *bkev1beta1.BKELogger
}

type YamlInstallerConfig struct {
    Store              manifest.Store
    Applier            manifest.Applier
    ManifestDownloader *ManifestDownloader  // 新增
    Logger             *bkev1beta1.BKELogger
}

// Apply 扩展：支持外部 URL 清单 + 内联 Resources
func (i *YamlInstaller) Apply(ctx context.Context, cv *configv1alpha1.ComponentVersion, execCtx *ExecutionContext) error {
    var allResources []unstructured.Unstructured

    // 1. 下载外部 URL 清单 (yaml.manifests[])
    if cv.Spec.YAML != nil && len(cv.Spec.YAML.Manifests) > 0 {
        data, err := i.manifestDownloader.DownloadManifests(ctx, cv.Spec.YAML.Manifests)
        if err != nil {
            return fmt.Errorf("download manifests for %s: %w", cv.Spec.Name, err)
        }
        resources, err := ParseMultiDocYAML(data)
        if err != nil {
            return fmt.Errorf("parse manifests for %s: %w", cv.Spec.Name, err)
        }
        allResources = append(allResources, resources...)
    }

    // 2. 加载内联 Resources (cv.Spec.Resources[])
    inlineResources, err := i.store.GetComponentManifests(ctx, cv.Spec.Name, cv.Spec.Version, execCtx.TemplateContext)
    if err != nil {
        return fmt.Errorf("get inline manifests for %s: %w", cv.Spec.Name, err)
    }
    if inlineResources != nil {
        allResources = append(allResources, inlineResources.Resources...)
    }

    // 3. 应用到集群
    pkg := &manifest.ComponentPackage{Resources: allResources}
    if err := i.applier.ApplyComponent(ctx, pkg); err != nil {
        return fmt.Errorf("apply manifests for %s: %w", cv.Spec.Name, err)
    }

    // 4. 健康检查
    if cv.Spec.YAML != nil && cv.Spec.YAML.HealthCheck != nil && cv.Spec.YAML.HealthCheck.Enabled {
        if err := healthcheck.Run(ctx, execCtx.TargetClient, *cv.Spec.YAML.HealthCheck); err != nil {
            return fmt.Errorf("health check failed for %s: %w", cv.Spec.Name, err)
        }
    }
    return nil
}
```

### 6.5 健康检查

YamlInstaller 的健康检查在 `Apply` 方法内部调用共享 `pkg/healthcheck` 包执行（`healthcheck.Run(ctx, execCtx.TargetClient, *cv.Spec.YAML.HealthCheck)`）。当 `cv.Spec.YAML.HealthCheck.Enabled` 为 true 时，清单 Apply 后执行 PodReady/EndpointReady/Custom 检查。接口定义与实现详见 **第 7 章 HealthCheck 共享包设计**。

### 6.6 YAML Uninstall 流程

**设计思路 — YAML 组件卸载与 Binary/Helm 的区别**：

YAML 组件的卸载流程与 Binary/Helm 有本质区别：
- **Binary**：通过 SSH 执行卸载脚本，停止服务、删除二进制
- **Helm**：通过 Helm SDK 执行 `helm uninstall`，删除 Release
- **YAML**：通过 K8s API 删除资源，支持 Prune 裁剪

YAML 组件没有"服务"概念，卸载就是删除已应用的 Kubernetes 资源。

**卸载流程图**：

```
YAML Uninstall 流程:
┌─────────────────────────────────────────────────────────────┐
│ 1. 加载已安装资源清单                                         │
│    - 从 ComponentVersion 获取 Resources                      │
│    - 或从 ManifestStore 获取已应用的清单                      │
├─────────────────────────────────────────────────────────────┤
│ 2. 删除资源 (逆序)                                           │
│    - 按 GVK 依赖关系逆序删除                                 │
│    - CRD 最后删除                                           │
│    - 使用 DeletePropagationBackground 或 Foreground         │
├─────────────────────────────────────────────────────────────┤
│ 3. 可选：Prune 裁剪                                          │
│    - 按 PruneLabelSelector 查找资源                          │
│    - 删除不在当前清单中的资源                                 │
├─────────────────────────────────────────────────────────────┤
│ 4. 验证删除完成                                              │
│    - 检查资源是否已删除                                       │
└─────────────────────────────────────────────────────────────┘
```

**代码实现**：

```go
// pkg/yamlinstaller/installer.go

// Uninstall 卸载 YAML 组件
func (i *YamlInstaller) Uninstall(ctx context.Context, cv *configv1alpha1.ComponentVersion, execCtx *ExecutionContext) error {
    // 1. 获取已安装资源
    pkg, err := i.store.GetComponentManifests(ctx, cv.Spec.Name, cv.Spec.Version, execCtx.TemplateContext)
    if err != nil {
        return fmt.Errorf("get manifests for %s: %w", cv.Spec.Name, err)
    }

    // 2. 逆序删除资源
    if err := i.applier.DeleteComponent(ctx, pkg); err != nil {
        return fmt.Errorf("delete manifests for %s: %w", cv.Spec.Name, err)
    }

    // 3. 可选：Prune 裁剪
    if cv.Spec.YAML != nil && cv.Spec.YAML.Prune {
        if err := i.applier.PruneResources(ctx, cv.Spec.YAML.PruneLabelSelector); err != nil {
            return fmt.Errorf("prune resources for %s: %w", cv.Spec.Name, err)
        }
    }

    return nil
}
```

**Applier 扩展接口**：

```go
// pkg/manifest/applier.go

// Applier 扩展接口，支持删除和裁剪
type Applier interface {
    // ApplyComponent 应用组件清单
    ApplyComponent(ctx context.Context, pkg *ComponentPackage) error
    
    // DeleteComponent 删除组件资源（逆序删除）
    DeleteComponent(ctx context.Context, pkg *ComponentPackage) error
    
    // PruneResources 裁剪不在清单中的资源（按 label selector）
    PruneResources(ctx context.Context, selector map[string]string) error
}
```

**与 Binary/Helm 卸载的对比**：

| 组件类型 | 卸载机制 | 是否需要服务管理 | 是否支持 Prune |
|---------|---------|-----------------|---------------|
| Binary | SSH 执行卸载脚本 | ✅ 停止/禁用服务 | ❌ |
| Helm | Helm SDK `helm uninstall` | ❌ Helm 管理 | ❌ |
| YAML | K8s API 删除资源 | ❌ 无服务概念 | ✅ 按 label 裁剪 |

---

## 7. HealthCheck 共享包设计

**设计思路 — 横切关注点独立成包**：

PodReady/EndpointReady/Custom 三种 K8s 资源就绪检查是 HelmInstaller（5.3）与 YamlInstaller（6.3）共用的横切关注点。原设计中两处各自实现 `runHealthCheck`/`checkPodReady`/`checkEndpointReady`/`checkCustom`，逻辑近乎相同（M3 问题）。现抽取为独立共享包 `pkg/healthcheck`，两处均委托调用，消除重复。

**作用范围**：
- ✅ `HelmInstaller`（5.3）：Helm `install/upgrade` 返回后执行自定义健康检查
- ✅ `YamlInstaller`（6.3）：清单 Apply 后执行健康检查
- ❌ `BinaryInstaller`（4.x）：二进制组件运行在远程节点，健康检查通过 SSH 执行脚本（`BinaryHealthCheckSpec.Script`，退出码判定），机制不同，**不**使用本共享包

**与 Helm `--wait` 的关系**（仅 Helm 侧）：两者同时生效，是两层递进检查非互斥——Helm `--wait`（`strategy.wait: true`）在 `helm install/upgrade` 命令内部先执行（仅检查全部 Pod Ready）；自定义 `healthCheck` 在 Helm 命令返回后执行（支持 `minReady` 部分就绪、Endpoint、Custom）。若 `--wait` 失败且 `atomic: true`，Helm 自动回滚，不执行自定义 `healthCheck`。

**执行流程图**：

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         HealthCheck 执行流程                                     │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │   Run()          │
                              │  入口函数        │
                              └────────┬─────────┘
                                       │
                                       ▼
                     ┌──────────────────────────────────────┐
                     │  设置超时与重试间隔                   │
                     │  timeout = hc.Timeout (默认 3m)      │
                     │  interval = hc.Interval (默认 5s)    │
                     │  deadline = now + timeout            │
                     └────────────────────┬─────────────────┘
                                          │
                                          ▼
                     ┌──────────────────────────────────────┐
                     │  now < deadline ?                    │
                     └────────────────────┬─────────────────┘
                                          │
                            ┌─────────────┴─────────────┐
                            │                           │
                           是                          否
                            │                           │
                            ▼                           ▼
              ┌─────────────────────────┐   ┌─────────────────┐
              │  遍历 hc.Checks         │   │  返回超时错误    │
              │  allReady = true        │   │  return err     │
              └────────────┬────────────┘   └─────────────────┘
                           │
           ┌───────────────┼───────────────┐
           │               │               │
           ▼               ▼               ▼
  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
  │  PodReady    │ │EndpointReady │ │   Custom     │
  │ list Pods    │ │ get Endpoints│ │ exec command │
  │ count Ready  │ │ check Addrs  │ │ exit code 0  │
  └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
         │                │                │
         └────────────────┼────────────────┘
                          │
                          ▼
              ┌─────────────────────────┐
              │  allReady ?             │
              └────────────┬────────────┘
                   ┌───────┴───────┐
                   │               │
                  是              否
                   │               │
                   ▼               ▼
         ┌─────────────────┐ ┌─────────────────┐
         │  返回成功        │ │  Sleep(interval)│
         │  return nil     │ │  继续重试循环    │
         └─────────────────┘ └─────────────────┘
                                   │
                                   └──→ 回到 now < deadline?
```

```go
// pkg/healthcheck/healthcheck.go

// Run 执行健康检查，按 hc.Checks 遍历 PodReady/EndpointReady/Custom，
// 重试直到全部通过或超时。HelmInstaller 与 YamlInstaller 共用此入口。
// client 为目标集群 typed clientset (kubernetes.Interface)，非 controller-runtime client。
func Run(ctx context.Context, client kubernetes.Interface, hc HealthCheckSpec) error {
    timeout := parseDurationDefault(hc.Timeout, 3*time.Minute)
    interval := parseDurationDefault(hc.Interval, 5*time.Second)
    deadline := time.Now().Add(timeout)

    for time.Now().Before(deadline) {
        allReady := true
        for _, check := range hc.Checks {
            switch check.Type {
            case "PodReady":
                if check.PodReady == nil {
                    return fmt.Errorf("PodReady check requires 'podReady' config")
                }
                ready, err := checkPodReady(ctx, client, check.PodReady)
                if err != nil || !ready {
                    allReady = false
                }
            case "EndpointReady":
                if check.EndpointReady == nil {
                    return fmt.Errorf("EndpointReady check requires 'endpointReady' config")
                }
                ready, err := checkEndpointReady(ctx, client, check.EndpointReady)
                if err != nil || !ready {
                    allReady = false
                }
            case "Custom":
                if check.Custom == nil {
                    return fmt.Errorf("Custom check requires 'custom' config")
                }
                ready, err := checkCustom(ctx, check.Custom)
                if err != nil || !ready {
                    allReady = false
                }
            }
        }
        if allReady {
            return nil
        }
        time.Sleep(interval)
    }

    return fmt.Errorf("health check timed out after %s", timeout)
}

// checkPodReady 检查 Pod Ready 状态 (支持 minReady 部分就绪)
// listPods 逻辑内联于此 (原 HelmInstaller.listPods)，使用传入的 client
func checkPodReady(ctx context.Context, client kubernetes.Interface, spec *PodReadyCheckSpec) (bool, error) {
    selector, err := labels.Parse(spec.LabelSelector)
    if err != nil {
        return false, fmt.Errorf("invalid label selector %q: %w", spec.LabelSelector, err)
    }
    podList, err := client.CoreV1().Pods(spec.Namespace).List(ctx, metav1.ListOptions{
        LabelSelector: selector.String(),
    })
    if err != nil {
        return false, err
    }
    minReady := int(spec.MinReady)
    if minReady == 0 {
        minReady = len(podList.Items) // 默认要求全部 Ready
    }
    readyCount := 0
    for _, pod := range podList.Items {
        for _, cond := range pod.Status.Conditions {
            if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
                readyCount++
                break
            }
        }
    }
    return readyCount >= minReady, nil
}

// checkEndpointReady 检查 Service Endpoint 是否有就绪端点
func checkEndpointReady(ctx context.Context, client kubernetes.Interface, spec *EndpointReadyCheckSpec) (bool, error) {
    endpoints, err := client.CoreV1().Endpoints(spec.Namespace).Get(ctx, spec.ServiceName, metav1.GetOptions{})
    if err != nil {
        return false, err
    }
    for _, subset := range endpoints.Subsets {
        if len(subset.Addresses) > 0 {
            return true, nil // 有就绪端点
        }
    }
    return false, nil
}

// checkCustom 执行自定义检查命令 (在控制器 Pod 中执行, 退出码 0 = 通过)
func checkCustom(ctx context.Context, spec *CustomCheckSpec) (bool, error) {
    cmd := exec.CommandContext(ctx, "/bin/sh", "-c", spec.Command)
    if err := cmd.Run(); err != nil {
        return false, nil // 非零退出码 = 未就绪
    }
    return true, nil
}
```

**类型归属**：`HealthCheckSpec`/`HealthCheckItemSpec`/`PodReadyCheckSpec`/`EndpointReadyCheckSpec`/`CustomCheckSpec` 等类型定义应迁移到 `pkg/healthcheck/types.go`，供 Helm/YAML 共用；`api/v1alpha1` 的 CRD 字段类型可内嵌或别名这些类型，避免在 `binaryinstaller`/`helminstaller`/`yamlinstaller` 多处重复定义。

**调用方**：
- `HelmInstaller.runHealthCheck`（5.3）：`return healthcheck.Run(ctx, i.clientset, hc)`
- `YamlInstaller.Apply`（6.3）：`healthcheck.Run(ctx, execCtx.TargetClient, *cv.Spec.YAML.HealthCheck)`

### 7.1 类型定义

**设计思路**：健康检查类型定义独立于具体 Installer，供 Helm/YAML 组件共用。CRD 字段类型引用此处的定义，避免在多处重复定义。

```go
// pkg/healthcheck/types.go

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
// 按检查类型嵌套子结构体, 消除 Name 字段双重含义 (与 ConfigTemplateSpec 模式一致)
// Type 决定使用哪个子结构体, 其他子结构体应为 nil
type HealthCheckItemSpec struct {
    // 检查类型: PodReady / EndpointReady / Custom
    Type string `json:"type"`
    
    // PodReady 检查配置 (type=PodReady 时使用)
    PodReady *PodReadyCheckSpec `json:"podReady,omitempty"`
    
    // EndpointReady 检查配置 (type=EndpointReady 时使用)
    EndpointReady *EndpointReadyCheckSpec `json:"endpointReady,omitempty"`
    
    // Custom 检查配置 (type=Custom 时使用)
    Custom *CustomCheckSpec `json:"custom,omitempty"`
}

// PodReadyCheckSpec 定义 Pod 就绪检查规格
type PodReadyCheckSpec struct {
    // 命名空间
    Namespace string `json:"namespace"`
    
    // 标签选择器 (如 "k8s-app=kube-dns")
    LabelSelector string `json:"labelSelector"`
    
    // 最小就绪数量 (0=要求全部 Ready, 1=至少 1 个 Ready)
    MinReady int32 `json:"minReady,omitempty"`
}

// EndpointReadyCheckSpec 定义 Endpoint 就绪检查规格
type EndpointReadyCheckSpec struct {
    // 命名空间
    Namespace string `json:"namespace"`
    
    // Service 名称
    ServiceName string `json:"serviceName"`
    
    // 端口 (可选, 检查特定端口是否就绪)
    Port int32 `json:"port,omitempty"`
}

// CustomCheckSpec 定义自定义检查规格
type CustomCheckSpec struct {
    // 检查命令 (在控制器 Pod 中执行, 退出码 0=通过, 如 "curl -s http://.../healthz")
    Command string `json:"command"`
}
```

---

## 8. 模板变量系统与 TemplateContext 详细设计

### 8.0 TemplateContext 扩展策略

**设计思路**：为避免重复造轮子，本设计复用并扩展现有的 `pkg/manifest.TemplateContext` 结构体。现有 TemplateContext 用于 YAML/Manifest 组件的模板渲染，包含 4 个基础字段。在此基础上扩展，增加 Binary 组件所需的节点信息、制品信息、自定义变量等字段。

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
    // 现有字段 (保持向后兼容，快捷访问)
    ClusterName       string
    Namespace         string
    KubernetesVersion string
    OpenFuyaoVersion  string
    
    // 新增：完整配置引用 (通用性与可扩展性)
    // 注入 BKEConfig，模板可访问任意配置字段
    // 简单场景使用快捷字段: {{.ClusterName}}
    // 复杂场景使用完整引用: {{.Config.Cluster.ContainerRuntime.CRI}}
    Config            *BKEConfig
    
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

    // 新增：组件变量（BinaryInstaller 注入）
    // 从 ComponentVersion.Spec.Binary.Variables 读取
    // 例如：logLevel, snapshotter 等
    // 访问方式：{{.ComponentVariables.logLevel}}
    ComponentVariables map[string]string
}

type ArtifactInfo struct {
    Name        string
    Path        string    // 本地缓存路径 (staging, 上传到远程临时目录)
    URL         string
    Checksum    string
    Filename    string
    InstallPath string    // 远程节点上的安装路径 (per-artifact, 通过 {{artifact.<name>.installPath}} 引用)
}
```

**设计决策 — 混合设计（快捷字段 + 完整配置）**：

| 方案 | 优点 | 缺点 | 选择 |
|------|------|------|------|
| 仅快捷字段 | 模板简洁、类型安全 | 新增字段需修改 TemplateContext | ❌ |
| 仅完整配置 | 通用性强、可扩展 | 模板访问路径长、与结构耦合 | ❌ |
| **混合设计** | **兼顾简洁与灵活、向后兼容** | **需维护两套访问方式** | **✅** |

**使用示例**：

```yaml
# 简单场景：使用快捷字段
installScript: |
  echo "Cluster: {{.ClusterName}}"
  echo "K8s Version: {{.KubernetesVersion}}"

# 复杂场景：使用完整配置
installScript: |
  echo "CRI: {{.Config.Cluster.ContainerRuntime.CRI}}"
  echo "Image Repo: {{.Config.Cluster.ImageRepo.Domain}}"
  echo "Pod CIDR: {{.Config.Cluster.Networking.PodCIDR}}"

# selector condition
condition: '{{.Config.Cluster.ContainerRuntime.CRI == "containerd"}}'
```

#### 8.0.1 TemplateContext 实现规格

**设计思路**：明确 `TemplateContext` 扩展字段的注入时机和边界条件，确保实现的一致性和可测试性。

**当前代码** (`pkg/manifest/types.go`)：

```go
// TemplateContext carries cluster fields used to render component templates.
type TemplateContext struct {
    ClusterName       string
    Namespace         string
    KubernetesVersion string
    OpenFuyaoVersion  string
}
```

**目标代码** (`pkg/manifest/types.go`)：

```go
// TemplateContext carries cluster fields used to render component templates.
type TemplateContext struct {
    // 现有字段 (保持向后兼容，快捷访问)
    ClusterName       string
    Namespace         string
    KubernetesVersion string
    OpenFuyaoVersion  string
    
    // 新增：完整配置引用 (通用性与可扩展性)
    // 注入 BKEConfig，模板可访问任意配置字段
    // 简单场景使用快捷字段: {{.ClusterName}}
    // 复杂场景使用完整引用: {{.Config.Cluster.ContainerRuntime.CRI}}
    Config            *confv1beta1.BKEConfig
    
    // 新增：集群扩展信息 (Binary 组件需要)
    APIServer         string
    ServiceCIDR       string
    PodCIDR           string
    DNSDomain         string
    
    // 新增：节点基础信息 (Binary 组件需要)
    NodeIP            string
    NodeHostname      string
    NodeRole          string
    NodeArch          string  // SSH 发现后填入 (uname -m)
    
    // 新增：版本信息
    ComponentVersion          string
    ComponentPreviousVersion  string
    
    // 新增：制品信息 (Binary 组件需要)
    Artifacts         map[string]*ArtifactInfo
    
    // 新增：镜像仓库
    ImageRegistry     string
    ImagePullSecret   string
    
    // 新增：组件级路径
    ConfigPath        string
    LogPath           string
    DataPath          string
    
    // 新增：操作类型
    Action            string  // "Install" / "Upgrade" / "Uninstall"
    IsUpgrade         bool    // Action == "Upgrade" 时为 true
    
    // 新增：自定义变量 (用于 selector condition 评估等)
    // 例如: ContainerRuntimeCRI, isOffline 等
    Variables         map[string]string
    
    // 新增：组件变量（BinaryInstaller 注入）
    // 从 ComponentVersion.Spec.Binary.Variables 读取
    // 例如：logLevel, snapshotter 等
    // 访问方式：{{.ComponentVariables.logLevel}}
    ComponentVariables map[string]string
}

type ArtifactInfo struct {
    Name        string
    Path        string    // 本地缓存路径
    URL         string
    Checksum    string
    Filename    string
    InstallPath string    // 远程节点上的安装路径
}
```

**注入时机**：

| 字段组 | 注入时机 | 注入位置 | 说明 |
|--------|---------|---------|------|
| 基础字段 | `NewExecutionContext` 初始化时 | `pkg/dagexec/execution_context.go` | ClusterName, Namespace, KubernetesVersion, OpenFuyaoVersion |
| `Config` | `NewExecutionContext` 初始化时 | `pkg/dagexec/execution_context.go` | 注入完整 BKEConfig 引用 |
| 集群扩展字段 | `NewExecutionContext` 初始化时 | `pkg/dagexec/execution_context.go` | APIServer, ServiceCIDR, PodCIDR, DNSDomain |
| `Variables` | `NewExecutionContext` 初始化时 | `pkg/dagexec/execution_context.go` | 初始化 map + 注入 ContainerRuntimeCRI |
| `ComponentVariables` | `BinaryInstaller.Install` 时 | `pkg/binaryinstaller/binary_installer.go` | 从 binary.Variables 注入组件变量 |
| 节点字段 | `BinaryComponentExecutor.executePerNode` 时 | `pkg/dagexec/binary_component_executor.go` | NodeIP, NodeHostname, NodeRole, NodeArch |
| 制品字段 | `BinaryInstaller.Install` 时 | `pkg/binaryinstaller/binary_installer.go` | Artifacts, ConfigPath, LogPath, DataPath |
| 操作类型 | `BinaryComponentExecutor` 判断后 | `pkg/dagexec/binary_component_executor.go` | Action, IsUpgrade |

**边界条件处理**：

| 场景 | 处理方式 |
|------|---------|
| `ClusterConfig` 为 nil | Variables 和 ComponentVariables 仍初始化为空 map，基础字段留空 |
| `ContainerRuntime.CRI` 为空 | 不注入 Variables["ContainerRuntimeCRI"]，但 map 仍存在 |
| `BKECluster` 为 nil | `NewExecutionContext` 返回错误，不创建 ExecutionContext |
| `ComponentVariables` 未注入 | 模板中访问返回空字符串，不影响现有组件（向后兼容） |

**实现步骤**：

1. **修改 `pkg/manifest/types.go`**
   - 添加所有新增字段
   - 添加 `ArtifactInfo` 结构体

2. **修改 `pkg/dagexec/execution_context.go`**
   - `NewExecutionContext` 中初始化 `Variables` 和 `ComponentVariables`
   - 从 `cluster.Spec.ClusterConfig.Cluster.ContainerRuntime.CRI` 注入 `ContainerRuntimeCRI`
   - 填充基础字段和集群扩展字段

3. **修改 `pkg/dagexec/binary_component_executor.go`**
   - `executePerNode` 中填充节点字段
   - 判断操作类型（Install/Upgrade）并填充 Action 和 IsUpgrade

4. **修改 `pkg/binaryinstaller/binary_installer.go`**
   - `Install` 中填充制品字段和组件级路径

5. **添加测试**
   - `TestNewExecutionContext_ContainerRuntimeCRI`
   - `TestNewExecutionContext_EmptyCRI`
   - `TestNewExecutionContext_NilClusterConfig`
   - `TestNewExecutionContext_ComponentVariablesInit`

### 8.1 模板变量系统

模板变量系统支持 8 类 50+ 模板变量，覆盖集群、节点、版本、制品、镜像仓库、路径、操作类型和自定义变量。

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

**设计说明**：节点变量包含基础信息（IP、Hostname、Role）+ 架构（Arch）。**仅 Arch 作为模板变量注入**，因为制品 URL 含 `{{arch}}` 占位符（如 `containerd-{{version}}-linux-{{arch}}.tar.gz`），下载前必须解析——Arch 由 BinaryInstaller.Install() 通过 SSH `uname -m` 发现后填入 `.NodeArch`。OS/OSVersion 不作为模板变量，由安装脚本运行时自检测（`/etc/os-release`），因为二进制安装本身 OS 无关，仅少数脚本分支需要 OS。

> 命名一致性：模板变量统一写作 `{{arch}}`（与 URL/resolveTemplate 一致），对应 `TemplateContext.NodeArch` 字段。全文 `{{arch}}` 与 `{{nodeArch}}` 视为同一变量，实现层以 `{{arch}}` 为准。

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{nodeIP}}` | 节点 IP | `.NodeIP` | `192.168.1.10` |
| `{{nodeHostname}}` | 节点主机名 | `.NodeHostname` | `node-01` |
| `{{nodeRole}}` | 节点角色 | `.NodeRole` | `master` / `worker` / `etcd` |
| `{{arch}}` | 节点架构 (SSH 发现后注入) | `.NodeArch` | `amd64` / `arm64` |

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
| `{{artifact.<name>.installPath}}` | per-artifact 安装路径 | `.Artifacts[name].InstallPath` | `/usr/local` |

#### 5. 镜像仓库变量 (Registry Variables)

| 变量 | 说明 | TemplateContext 字段 | 示例值 |
|------|------|---------------------|--------|
| `{{imageRegistry}}` | 镜像仓库地址 | `.ImageRegistry` | `registry.openfuyao.cn` |
| `{{imagePullSecret}}` | 镜像拉取 Secret | `.ImagePullSecret` | `registry-secret` |
| `{{imageNamespace}}` | 镜像命名空间 | 从 ImageRegistry 解析 | `openfuyao` |

#### 8. 安装路径变量 (Path Variables)

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

#### 9. 操作类型变量 (Action Variables)

| 变量 | 说明 | 来源 | 示例值 |
|------|------|------|--------|
| `{{action}}` | 操作类型 | `TemplateContext.Action`（从 `InstallOptions.Action` 填充） | `Install` / `Upgrade` / `Uninstall` |
| `{{isUpgrade}}` | 是否升级 | `TemplateContext.IsUpgrade`（`Action == Upgrade` 时为 true） | `true` / `false` |
| `{{isInstall}}` | 是否安装 | `TemplateContext.Action == Install` | `true` / `false` |

**`isUpgrade` 的来源链路**：`VersionContext.HasCurrent()` → `BinaryActionUpgrade` → `InstallOptions.Action` → `tmplCtx.IsUpgrade` → 模板 `{{if .isUpgrade}}`

#### 9.1 部署模式变量 (Deploy Mode Variables)

| 变量 | 说明 | 来源 | 示例值 |
|------|------|------|--------|
| `{{.isOffline}}` | 是否离线模式 | `TemplateContext.Variables["isOffline"]`（由 BinaryComponentExecutor 注入） | `true` / `false` |

**`isOffline` 的来源链路**：

`BinaryComponentExecutor` 在执行前根据集群配置推断：检查 `imageRegistry` 是否在 `insecureRegistries` 列表中（且非 `cr.openfuyao.cn`），是则为离线模式。复用现有 `configureContainerdLegacy:357-364` 的 `repoInsecure` 判定逻辑。

```
BKECluster.Spec (imageRepo + insecureRegistries)
  → BinaryComponentExecutor 检查 repo ∈ insecureRegistries
    → true: tmplCtx.Variables["isOffline"] = "true"
    → false: tmplCtx.Variables["isOffline"] = "false"
      → ConfigTemplateSpec.condition: "{{.isOffline}}"
        → 离线时生成 hosts.toml 重定向文件，在线时跳过
```

**典型用途**：控制 `configTemplates` 中离线重定向 hosts.toml 的生成——在线模式不生成公共仓库重定向文件，离线模式为每个公共仓库生成重定向到私有仓库的 hosts.toml。

| 变量 | 说明 | 来源 | 示例值 |
|------|------|------|--------|
| `{{.ContainerRuntimeCRI}}` | 容器运行时类型 | `BKECluster.Spec.Cluster.ContainerRuntime.CRI`，由 controllers 层注入 `ExecutionContext.TemplateContext.Variables` | `containerd` / `docker` |

**`ContainerRuntimeCRI` 的来源链路**：

controllers 层构建 `ExecutionContext` 时，从 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 读取，注入 `TemplateContext.Variables["ContainerRuntimeCRI"]`。DAG 构建器评估 selector 组件的 `subComponents[].condition` 时使用此变量。

```
BKECluster.Spec.Cluster.ContainerRuntime.CRI
  → controllers 层注入 ExecutionContext.TemplateContext.Variables["ContainerRuntimeCRI"]
    → DAG 构建器: BuildDAGFromBundle 遇到 type=selector 组件
      → 评估 subComponents[].condition: '{{.ContainerRuntimeCRI == "containerd"}}'
        → true: 纳入 containerd 子组件
        → false: 跳过, 评估下一个 subComponent
```

**典型用途**：selector 类型组件的 `subComponents[].condition` 评估——根据集群配置的容器运行时类型选择安装 containerd 或 docker。

#### 10. 自定义变量 (Custom Variables)

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

### 8.2 模板渲染流程图

**设计思路**：模板渲染的流程分为 5 个主要步骤：接收 TemplateContext、构建制品数据、创建模板解析器、执行模板渲染、返回结果。整个流程复用 DAG 调度器传递的 TemplateContext，避免重复构建模板数据。

**关键设计点**：
- **复用 TemplateContext**：直接使用 DAG 调度器构建的 TemplateContext，包含集群、节点、版本等信息
- **制品数据构建**：根据 ComponentVersion 的 artifacts 定义，下载并构建制品信息
- **自定义函数**：提供 upper/lower/eq/ne/default/joinPath 等常用函数
- **脚本自检测**：架构和操作系统信息由安装脚本在运行时自检测，模板不感知
- **错误处理**：模板解析和执行失败时返回详细错误信息

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        模板渲染流程                                              │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  RenderScript()  │
                              │  入口函数        │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  1. 接收 TemplateContext             │
                    │  (由 DAG Scheduler 构建并传递)        │
                    │                                      │
                    │  TemplateContext 包含:               │
                    │  - ClusterName, Namespace            │
                    │  - KubernetesVersion, OpenFuyaoVer   │
                    │  - NodeIP, NodeHostname, NodeRole    │
                    │  - ComponentVersion                  │
                    │  - Artifacts (待填充)                │
                    │  - Variables                         │
                    │                                      │
                    │  注意: NodeArch 由 Install() SSH     │
                    │  发现后填入; NodeOS/NodeOSVersion     │
                    │  不包含, 由安装脚本自检测              │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                     │  2. 构建制品数据                     │
                     │  downloadArtifacts(ctx, binary,     │
                     │                    arch)            │
                    │                                      │
                    │  填充 TemplateContext.Artifacts:     │
                    │  - containerd: {path, url, checksum} │
                    │  - ctr: {path, url, checksum}        │
                    │  - shim: {path, url, checksum}       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 创建模板解析器                    │
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
                    │  继续           │   │  返回错误        │
                    └────────┬────────┘   └─────────────────┘
                             │
                             ▼
                    ┌──────────────────────────────────────┐
                    │  4. 执行模板渲染                      │
                    │  tmpl.Execute(&buf, tmplCtx)         │
                    │                                      │
                    │  模板变量替换:                        │
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
                    │  返回渲染结果    │   │  返回错误        │
                    │  return buf.    │   │  return err     │
                    │  String()       │   │                 │
                    └─────────────────┘   └─────────────────┘
```

### 8.3 TemplateContext 构建流程

**设计思路**：DAG 调度器在执行组件前构建 TemplateContext，包含集群信息、节点基础信息、版本信息等。对于 Binary 组件，还需要在 TemplateContext 中填充制品信息。注意：TemplateContext 不包含 NodeOS/NodeOSVersion（由安装脚本自检测），NodeArch 由 BinaryInstaller.Install() 通过 SSH 发现后填入（制品 URL 中的 `{{arch}}` 需要在下载前解析）。

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
                    │  遍历执行批次                         │
                    │  for _, batch := range batches       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  对每个组件:                          │
                    │  executeComponent(ctx, node, tmpl)   │
                    └────────────────────┬─────────────────┘
                                         │
                     ┌───────────────────┼────────────────────┬────────────────────┐
                     │                   │                    │                    │
                     ▼                   ▼                    ▼                    ▼
           ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
           │  YAML 组件      │  │  Helm 组件       │  │  Binary 组件    │  │  Inline 组件     │
           │                 │  │                 │  │                 │  │                 │
           │  使用基础字段    │  │  使用基础字段    │  │  扩展节点信息    │  │  使用基础字段    │
           │  - ClusterName  │  │  + APIServer    │  │  + NodeIP       │  │  - ClusterName  │
           │  - Namespace    │  │  + ServiceCIDR  │  │  + NodeHostname │  │  - Namespace    │
           │  - K8sVersion   │  │                 │  │  + NodeRole     │  │  - K8sVersion   │
           │                 │  │                 │  │  + Artifacts    │  │                 │
           │  渲染 Manifest  │  │  渲染 Values     │  │  + Variables    │  │  Phase 执行     │
           │  应用到集群      │  │  helm install   │  │                 │  │  (无需模板渲染)  │
           │                 │  │                 │  │  渲染脚本        │  │                 │
           │                 │  │                 │  │  SSH 执行       │   │                 │
           └─────────────────┘  └─────────────────┘  └─────────────────┘   └─────────────────┘
```

### 8.4 自定义函数定义

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

// RenderScript 渲染安装脚本 (使用 TemplateContext 作为模板数据)
func (r *TemplateRenderer) RenderScript(script string, tmplCtx manifest.TemplateContext) (string, error) {
    tmpl, err := template.New("installScript").Funcs(r.funcMap).Parse(script)
    if err != nil {
        return "", fmt.Errorf("failed to parse script template: %w", err)
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, tmplCtx); err != nil {
        return "", fmt.Errorf("failed to render script template: %w", err)
    }
    
    return buf.String(), nil
}
```

---

## 9. DAG 集成详细设计

### 9.1 执行器注册

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
                              │  接收组件节点     │
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
                    │  判断组件类型                         │
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
     │ BinaryCompo- │ │ HelmCompo-   │ │ InlineCompo- │ │ YamlCompo-   │
     │ nentExecutor │ │ nentExecutor │ │ nentExecutor │ │ nentExecutor │
     └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
            │                │                │                │
            │                │                │                ▼
            │                │                │         ┌──────────────┐
            │                │                │         │ Yaml         │
            │                │                │         │ Installer    │
            │                │                │         │ (Store+Apply)│
            │                │                │         └──────┬───────┘
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

#### 9.1.1 当前代码分析

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

#### 9.1.2 执行器注册表设计

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

#### 9.1.3 执行器分发实现

**设计思路 - 彻底去除 phaseframe 依赖**：

现有 `Scheduler.ExecuteDAG(ctx, phaseCtx *phaseframe.PhaseContext, ...)` 直接接收 phaseframe 类型，使 `pkg/dagexec` 被 phaseframe 绑定。重构后：

1. `ExecuteDAG` 改为接收 phaseframe-free 的 `ExecutionContext`（由 controllers 层从 `phaseframe.PhaseContext` 构建适配，phaseframe 类型不进入 dagexec 包）。
2. `InlinePhaseRunner`（签名含 `*phaseframe.PhaseContext`）替换为 phaseframe-free 的 `InlineRunner` 接口（见 9.4.3.4），由 controllers 层 `InlinePhaseRunnerAdapter` 桥接。
3. 这样 `pkg/dagexec` 不再 import `pkg/phaseframe`，可独立编译测试。

**设计思路 - 组件类型分发顺序（解决先有鸡还是先有蛋问题）**：

`ExecutorRegistry` 按 `ComponentType` 分发，但类型信息在 `ComponentVersion.Spec.Type` 中，需先加载 CV。因此分发顺序为：
1. `ComponentVersionStore.GetComponentVersion(name, version)` 加载 CV
2. 按 `cv.Spec.Type` 从 `registry` 获取执行器
3. 执行器从 `ExecutionContext` 取上下文执行

`ComponentNode` 在 DAG 构建期（`pkg/topology`）已可从 bundle 解析出 `ComponentType`，可作为快速路径避免重复加载；若未填充则回退到 CV 加载。

```go
// pkg/dagexec/scheduler.go 重构 (phaseframe-free)

// ComponentVersionStore 接口定义见 9.2 节 (pkg/dagexec/component_version_store.go)

// ExecuteDAG 执行 DAG (phaseframe-free 入口)
// execCtx 由 controllers 层从 phaseframe.PhaseContext 构建适配后传入
func (s *Scheduler) ExecuteDAG(
    ctx context.Context,
    execCtx *ExecutionContext,
    dag *topology.UpgradeDAG,
) error {
    batches := dag.TopologicalBatches()
    for _, batch := range batches {
        if err := s.executeBatchParallel(ctx, execCtx, batch); err != nil {
            return err
        }
    }
    return nil
}

// expandSelectorComponents 在 DAG 构建期展开 selector 类型的 ComponentVersion
// selector 自身不产生 DAG 节点；遍历 subComponents，评估 condition，为真的子组件创建 DAG 节点
// ContainerRuntimeCRI 从 ExecutionContext.TemplateContext.Variables 获取
func (s *Scheduler) expandSelectorComponents(
    ctx context.Context,
    execCtx *ExecutionContext,
    cv *configv1alpha1.ComponentVersion,
) ([]topology.ComponentNode, error) {
    if cv.Spec.Type != configv1alpha1.ComponentTypeSelector {
        return nil, nil // 非 selector 类型, 不展开
    }

    cri := execCtx.TemplateContext.Variables["ContainerRuntimeCRI"]
    var nodes []topology.ComponentNode
    for _, sub := range cv.Spec.SubComponents {
        if sub.Condition == "" {
            // 无 condition = 始终纳入 (兼容组合语义)
            nodes = append(nodes, topology.ComponentNode{
                Name:    sub.Name,
                Version: sub.Version,
            })
            continue
        }
        // 评估 condition: 简单字符串匹配 (Go Template 在 DAG 构建期由 TemplateRenderer 评估)
        // condition 示例: '{{.ContainerRuntimeCRI == "containerd"}}'
        matched, err := evaluateCondition(sub.Condition, cri)
        if err != nil {
            return nil, fmt.Errorf("failed to evaluate condition for %s: %w", sub.Name, err)
        }
        if matched {
            nodes = append(nodes, topology.ComponentNode{
                Name:    sub.Name,
                Version: sub.Version,
            })
        }
    }
    return nodes, nil
}

// evaluateCondition 简单评估 selector condition (匹配 ContainerRuntimeCRI 值)
// 完整实现使用 TemplateRenderer 渲染 condition 模板, 结果 "true" = 匹配
func evaluateCondition(condition, cri string) (bool, error) {
    // 实际实现: 使用 TemplateRenderer 渲染 condition Go Template
    // 此处简化: 检查 condition 中是否包含 cri 值
    return strings.Contains(condition, "\""+cri+"\""), nil
}

// executeComponent 四路分发 (Feature Gate ON)
// 不再接收 phaseCtx；类型从 ComponentVersionStore 加载或 node.ComponentType() 快速路径
func (s *Scheduler) executeComponent(
    ctx context.Context,
    execCtx *ExecutionContext,
    node *topology.ComponentNode,
    tmpl manifest.TemplateContext,
) error {
    // 1. 解析组件类型 (优先 node 快速路径，回退到 CV 加载)
    componentType := ""
    if t, ok := node.ComponentType(); ok {
        componentType = string(t)
    } else if s.cvStore != nil {
        cv, err := s.cvStore.GetComponentVersion(ctx, node.Component.Name, node.Component.Version)
        if err != nil {
            return fmt.Errorf("failed to load ComponentVersion for %s: %w", node.Component.Name, err)
        }
        componentType = string(cv.Spec.Type)
    }

    // 2. Feature Gate OFF 或类型未注册: 回退到旧路径 (Inline 二路分发)
    executor, err := s.registry.Get(componentType)
    if err != nil {
        return s.executeComponentLegacy(ctx, execCtx, node, tmpl)
    }

    // 3. 填充节点级 TemplateContext (ComponentVersion 等由 Executor 自行补全)
    execCtx.TemplateContext = tmpl
    return executor.ExecuteComponent(ctx, node, execCtx)
}

// executeComponentLegacy 旧路径 (类型未注册时的二路分发，兼容未迁移组件)
func (s *Scheduler) executeComponentLegacy(
    ctx context.Context,
    execCtx *ExecutionContext,
    node *topology.ComponentNode,
    tmpl manifest.TemplateContext,
) error {
    if node.Inline != nil {
        inlineExec := &InlineComponentExecutor{runner: s.inlineRunner}
        return inlineExec.ExecuteComponent(ctx, node, execCtx)
    }
    return s.executeYaml(ctx, execCtx, node, tmpl)
}
```

**controllers 层桥接（phaseframe 仅存在于此）**：

```go
// controllers/capbke/bkecluster_upgrade_dag.go
// buildExecutionContext 从 phaseframe.PhaseContext 构建 dagexec.ExecutionContext
func (r *BKEClusterReconciler) buildExecutionContext(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,            // phaseframe 限定在 controllers 包
    oldCluster, newCluster *bkev1beta1.BKECluster,
    targetClient kubernetes.Interface,
) *dagexec.ExecutionContext {
    return dagexec.NewExecutionContext(
        oldCluster,
        newCluster,
        dagexec.NewBKENodeProvider(r.Client),
        dagexec.NewBKENodeFilter(r.Client),
        dagexec.NewBKENodeStatusUpdater(r.Client),
        dagexec.NewBKEComponentStatusUpdater(r.Client),
        phaseCtx.Log,
        phaseCtx.VersionContext,                  // 复用 upgrade.VersionContext
        targetClient,
    )
}
```

**说明**：`buildExecutionContext` 是 controllers 层唯一接触 phaseframe 类型的地方。通过此桥接函数，`pkg/dagexec` 包完全不依赖 `pkg/phaseframe`，可独立编译测试。`NewExecutionContext` 内部自动初始化 `TemplateContext`，包括 `Variables` 和 `ComponentVariables` 字段，并从 `newCluster.Spec.ClusterConfig.Cluster` 提取集群信息（如 `ContainerRuntimeCRI`）。

#### 9.1.4 Scheduler 初始化与执行器注入

```go
// pkg/dagexec/scheduler.go 扩展

// Config 扩展: 新增 Binary/Helm/YAML 执行器依赖
// 注意: InlineRunner 使用 dagexec.InlineRunner 接口 (phaseframe-free)，
//       不再使用 dagexec.InlinePhaseRunner (其签名含 *phaseframe.PhaseContext)
type Config struct {
    InlineRunner             InlineRunner            // phaseframe-free, 由 controllers 适配
    CVStore                  ComponentVersionStore   // 加载 ComponentVersion (类型分发用)
    ManifestStore            manifest.Store
    ManifestApplier          manifest.Applier
    BinaryInstaller          BinaryInstaller       // 新增 (Feature Gate ON 时注入)
    HelmInstaller            HelmInstaller         // 新增
    YAMLInstaller            *yamlinstaller.YamlInstaller  // 新增 (pkg/yamlinstaller, 对称 Binary/Helm)
    NodeProvider             NodeProvider          // 新增
    NodeFilter               NodeFilter            // 新增 (节点过滤，仅 Binary 组件使用)
    NodeStatusUpdater        NodeStatusUpdater     // 新增 (节点状态更新，仅 Binary 组件使用)
    ComponentStatusUpdater   ComponentStatusUpdater // 新增 (组件状态更新，所有组件类型使用)
    MaxParallelPerBatch      int
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

    // Binary 执行器: 依赖已注入时注册 (Feature Gate 控制在 controllers 层是否注入)
    if cfg.BinaryInstaller != nil {
        registry.Register("binary", &BinaryComponentExecutor{
            installer:              cfg.BinaryInstaller,
            cvStore:                cfg.CVStore,
            nodeFilter:             cfg.NodeFilter,
            statusUpdater:          cfg.NodeStatusUpdater,
            componentStatusUpdater: cfg.ComponentStatusUpdater,
        })
    }

    // Helm 执行器: 依赖已注入时注册
    if cfg.HelmInstaller != nil {
        registry.Register("helm", &HelmComponentExecutor{
            installer:              cfg.HelmInstaller,
            cvStore:                cfg.CVStore,
            componentStatusUpdater: cfg.ComponentStatusUpdater,
        })
    }

    // YAML 执行器: 依赖已注入时注册
    if cfg.YAMLInstaller != nil {
        registry.Register("yaml", &YamlComponentExecutor{
            installer:              cfg.YAMLInstaller,
            cvStore:                cfg.CVStore,
            componentStatusUpdater: cfg.ComponentStatusUpdater,
        })
    }

    return &Scheduler{
        inlineRunner:        cfg.InlineRunner,
        cvStore:             cfg.CVStore,
        ManifestStore:       cfg.ManifestStore,
        ManifestApplier:     cfg.ManifestApplier,
        registry:            registry,
        nodeProvider:        cfg.NodeProvider,
        MaxParallelPerBatch: maxParallel,
    }
}
```

**设计思路 — Config 构建与 Feature Gate 条件初始化**：

`buildSchedulerConfig()` 是调用方（BKEClusterReconciler）构建 Scheduler Config 的入口。Feature Gate OFF 时仅创建 3 个基础依赖，避免不必要的 SSH/HTTP/缓存初始化开销；ON 时额外构建 Binary/Helm/YAML 执行器依赖。

**依赖构建要点**：
- **共享依赖**：`httpClient` 在 Binary 和 Helm 之间共享；`ManifestStore`/`ManifestApplier` 在基础路径和 YamlInstaller 之间共享
- **缓存目录约定**：Binary 制品缓存 `/var/cache/bke/artifacts`，Helm Chart 缓存 `/var/cache/bke/charts`
- **httpClient 超时**：5 分钟，覆盖大制品下载场景
- **SSH client 来源**：从 `BKEClusterReconciler` 注入（已有 SSH 连接池），避免重复创建
- **TemplateRenderer**：无状态（仅 funcMap），全局共享；**ConfigRenderer**：需 K8s client（读取 Secret），按需创建
- **各 Config 类型定义**：`BinaryInstallerConfig`（第 4 章）、`HelmInstallerConfig`（第 5 章）、`NewConfigRenderer`（第 9 章）

```go
// controllers/capbke/bkecluster_upgrade_dag.go 扩展

// buildSchedulerConfig 构建 Scheduler Config (含 Feature Gate 条件构建)
func (r *BKEClusterReconciler) buildSchedulerConfig(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,            // phaseframe 限定在 controllers 包
    oldCluster, newCluster *bkev1beta1.BKECluster,
    bundle *manifest.Bundle,
    factory *componentfactory.Factory,
    bkeLogger *bkev1beta1.BKELogger,
) dagexec.Config {
    // 基础依赖 (Feature Gate ON/OFF 均需要)
    // bundleStore 一个对象同时实现 manifest.Store + dagexec.ComponentVersionStore (见 9.1.4)
    bundleStore := manifest.NewBundleStore(bundle)
    cfg := dagexec.Config{
        // InlineRunner: 通过适配器把 componentfactory.PhaseRunner (phaseframe-bound)
        // 适配为 dagexec.InlineRunner (phaseframe-free)
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &componentfactory.PhaseRunner{Factory: factory}),
        CVStore:         bundleStore,       // 同一对象, 同时满足 ComponentVersionStore
        ManifestStore:   bundleStore,       // 同一对象, 同时满足 manifest.Store
        ManifestApplier: r.buildManifestApplier(ctx, phaseCtx, newCluster, bkeLogger),
        MaxParallelPerBatch: 0, // 0 = defaultMaxParallelPerBatch (8)
    }

    // Feature Gate ON: 构建 Binary/Helm/YAML 执行器依赖
    // 使用现有 pkg/featuregate 的注解/flag 模式 (见 12.2)，而非 featuregate.Enabled(string)
    if featuregate.BinaryComponentEnabled(newCluster) {
        // 1. 共享依赖
        cfg.NodeProvider = dagexec.NewBKENodeProvider(r.Client)
        httpClient := &http.Client{Timeout: 5 * time.Minute}
        templateRenderer := binaryinstaller.NewTemplateRenderer()
        configRenderer := binaryinstaller.NewConfigRenderer(r.Client)

        // 2. BinaryInstaller (依赖: SSH 适配器 + 缓存 + 渲染器)
        // SshClient 字段类型为 binaryinstaller.SSHExecutor 接口 (见 4.3)，
        // 由 NewMultiCliSSHAdapter 包装 bkessh.MultiCli 提供
        cfg.BinaryInstaller = binaryinstaller.NewBinaryInstaller(
            binaryinstaller.BinaryInstallerConfig{
                Client:         r.Client,
                SshExecutor:    NewMultiCliSSHAdapter(r.sshClient),  // 适配 bkessh.MultiCli
                CacheDir:       "/var/cache/bke/artifacts",
                HttpClient:     httpClient,
                Renderer:       templateRenderer,   // 无状态引擎, 全局共享
                ConfigRenderer: configRenderer,     // 需 K8s client 读取 Secret
                Logger:         bkeLogger,
            },
        )

        // 3. HelmInstaller (依赖: RestConfig + HTTP + 复用 pkg/kube/chart.go)
        cfg.HelmInstaller = helminstaller.NewHelmInstaller(
            helminstaller.HelmInstallerConfig{
                Client:     r.Client,
                RestConfig: r.RestConfig,
                CacheDir:   "/var/cache/bke/charts",
                HttpClient: httpClient,             // 共享 httpClient
                Logger:     bkeLogger,
            },
        )

        // 4. YamlInstaller (复用 ManifestStore + ManifestApplier；对称 Binary/Helm 的 Installer 注入)
        cfg.YAMLInstaller = yamlinstaller.NewYamlInstaller(
            yamlinstaller.YamlInstallerConfig{
                Store:   cfg.ManifestStore,   // 复用基础依赖 (manifest.NewBundleStore(bundle))
                Applier: cfg.ManifestApplier, // 复用基础依赖
                Logger:  bkeLogger,
            },
        )
    }

    return cfg
}

// buildSchedulerAndExecute 构建 Scheduler 并执行 DAG
func (r *BKEClusterReconciler) buildSchedulerAndExecute(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    dag *topology.UpgradeDAG,
    bundle *manifest.Bundle,
    factory *componentfactory.Factory,
    bkeLogger *bkev1beta1.BKELogger,
) error {
    cfg := r.buildSchedulerConfig(ctx, phaseCtx, oldCluster, newCluster, bundle, factory, bkeLogger)
    sched := dagexec.NewScheduler(cfg)
    // 构建 phaseframe-free ExecutionContext，DAG 入参不再含 phaseframe 类型
    targetClient, _ := r.buildTargetClientset(ctx, newCluster)
    execCtx := r.buildExecutionContext(ctx, phaseCtx, oldCluster, newCluster, targetClient)
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

#### 9.1.5 当前代码 vs 目标设计对比

| 维度 | 当前代码 (scheduler.go) | 目标设计 |
|------|------------------------|---------|
| **分发方式** | `if node.Inline != nil` 二路 | `registry.Get(node.ComponentType())` 四路 |
| **执行器注册** | 无（硬编码 if-else） | `ExecutorRegistry` 按类型注册 |
| **执行器实例** | 无独立 Executor | Binary/Helm/YAML/Inline 各自实现 `ComponentExecutor` |
| **依赖注入** | `InlineRunner` + `ManifestStore` + `ManifestApplier` | 额外注入 `BinaryInstaller` + `HelmInstaller` + `YAMLInstaller` + `NodeProvider` |
| **Feature Gate** | 无 | ON→四路分发, OFF→`executeComponentLegacy` 二路分发 |
| **扩展性** | 新增类型需修改 `executeComponent()` | 新增类型只需 `registry.Register()`，Scheduler 不变 |
| **未注册类型处理** | 走 Manifest 路径 | 回退到 YAML/Manifest 路径（兼容未迁移组件） |

### 9.2 ComponentVersionStore

**设计思路 — 跨执行器共享的 CV 加载抽象**：

`ComponentVersionStore` 是 Binary/Helm/YAML 三个 Executor 共用的横切关注点——执行器需要加载 `ComponentVersion` 对象以获取组件类型（`cv.Spec.Type`）和规格（`cv.Spec.Binary`/`cv.Spec.Helm`/`cv.Spec.YAML`）。接口定义独立成文件 `pkg/dagexec/component_version_store.go`，与 `scheduler.go` 分离，便于多执行器引用。

```go
// pkg/dagexec/component_version_store.go

// ComponentVersionStore 加载 ComponentVersion (phaseframe-free)
// 由 pkg/manifest.BundleStore 扩展实现 (BundleStore 同时满足 manifest.Store 与本接口)
type ComponentVersionStore interface {
    GetComponentVersion(ctx context.Context, name, version string) (*configv1alpha1.ComponentVersion, error)
}
```

**查找流程图**：

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    ComponentVersionStore 查找流程                                │
└─────────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────┐
  │  Binary/Helm/YAML/Inline │
  │  ComponentExecutor       │
  └──────────┬───────────────┘
             │ cvStore.GetComponentVersion(ctx, name, version)
             ▼
  ┌──────────────────────────────────────┐
  │  BundleStore.GetComponentVersion     │
  │  (pkg/manifest/bundle_store.go)      │
  │                                      │
  │  key = ComponentKey(name, version)   │
  │       → "name@version"               │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  Bundle.Components[key]              │
  │  map[string]ComponentVersion         │
  │  (pkg/release/manifest/types.go)     │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
       找到      未找到
        │         │
        ▼         ▼
  ┌──────────┐  ┌──────────────────────┐
  │ return   │  │ return nil,          │
  │ &cv, nil │  │ "component %s not    │
  │ (副本指针)│  │  found in release    │
  └──────────┘  │  bundle"             │
                └──────────────────────┘

  数据流: Executor → ComponentVersionStore → BundleStore → Bundle.Components → *ComponentVersion
  同一对象: BundleStore 同时实现 manifest.Store (GetComponentManifests) + ComponentVersionStore (GetComponentVersion)
```

**BundleStore 扩展实现**：

现有 `pkg/manifest/bundle_store.go` 的 `BundleStore` 已持有 `*releasemanifest.Bundle` 且 `GetComponentManifests` 内部已做 `bundle.Components[ComponentKey(name, version)]` 查找（验证 CV 存在）——只是未返回 CV 对象本身。因此直接在 `BundleStore` 上扩展 `GetComponentVersion` 方法，使其同时实现 `manifest.Store` 与 `dagexec.ComponentVersionStore` 两个接口，无需独立类型：

| 接口 | 方法 | 返回 | 用途 |
|------|------|------|------|
| `manifest.Store` | `GetComponentManifests` | `*ComponentPackage`（清单字节） | YAML 执行器应用清单 |
| `dagexec.ComponentVersionStore` | `GetComponentVersion` | `*ComponentVersion`（CV 对象） | 类型分发 + Executor 读取 spec |

`Bundle.Components` 是 `map[string]apiv1.ComponentVersion`（值类型），键由 `releasemanifest.ComponentKey(name, version)` 生成（格式 `"name@version"`）。map 取值即为副本，返回其指针可避免调用方修改 bundle 原始对象。

```go
// pkg/manifest/bundle_store.go 扩展

// GetComponentVersion 从 Bundle.Components 按 (name, version) 加载 ComponentVersion。
// 镜像 GetComponentManifests 内部的查找模式 (bundle_store.go:41-44):
//
//	key := releasemanifest.ComponentKey(name, version)
//	cv, ok := bundle.Components[key]
//
// BundleStore 通过此方法同时实现 manifest.Store 与 dagexec.ComponentVersionStore，
// 一个对象、两个接口，消除独立 BundleCVStore 类型的重复。
func (s *BundleStore) GetComponentVersion(
	_ context.Context,
	name, version string,
) (*configv1alpha1.ComponentVersion, error) {
	if s == nil || s.bundle == nil {
		return nil, fmt.Errorf("release bundle is not initialized")
	}
	key := releasemanifest.ComponentKey(name, version)
	cv, ok := s.bundle.Components[key]
	if !ok {
		return nil, fmt.Errorf("component %s not found in release bundle", key)
	}
	return &cv, nil
}
```

> **错误信息约定**：与 `GetComponentManifests` 一致——未找到时返回 `"component %s not found in release bundle"`，其中 `%s` 为 `name@version` 键。

### 9.3 DAG 调度执行流程

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
                    │  1. 拓扑排序                          │
                    │  batches := dag.TopologicalBatches() │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  2. 遍历执行批次 (串行)               │
                    │  for _, batch := range batches       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 执行批次内组件 (并行)              │
                    │  executeBatchParallel(ctx, batch)    │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  组件 A          │  │  组件 B         │  │  组件 C         │
          │  (并行执行)      │  │  (并行执行)      │  │  (并行执行)     │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  4. 检查批次结果                      │
                    │  if err != nil                       │
                    └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         执行成功                执行失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  继续下一批次    │   │  检查失败策略    │
                    └─────────────────┘   └────────┬────────┘
                                                   │
                                        ┌──────────┼──────────┐
                                        │          │          │
                                        ▼          ▼          ▼
                              ┌─────────────┐ ┌─────────┐ ┌─────────┐
                              │  FailFast   │ │Continue │ │Rollback │
                              │  立即终止    │ │继续执行 │ │回滚后    │
                              │  返回错误    │ │下一批次 │ │继续      │
                              └─────────────┘ └─────────┘ └─────────┘
```

### 9.4 核心接口定义

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
│                           核心接口架构                                           │
└─────────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────┐
  │  Scheduler   │
  │  (调度入口)   │
  └──────┬───────┘
         │
         │ 创建
         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                    ExecutionContext                      │
  │                                                          │
  │  ┌─────────────────┐  ┌──────────────┐  ┌────────────┐   │
  │  │ VersionContext  │  │ NodeProvider │  │  Cluster   │   │
  │  │ (版本事实)       │  │ (节点获取)   │  │ (集群信息)  │   │
  │  │                 │  │              │  │            │   │
  │  │ HasCurrent()    │  │ GetNodes()   │  │            │   │
  │  │ HasTarget()     │  │ GetNodesBy   │  │            │   │
  │  │ NeedsUpgrade()  │  │   Role()     │  │            │   │
  │  └─────────────────┘  └──────────────┘  └────────────┘   │
  │                                                          │
  │  ┌────────────────────────────────────────────────────┐  │
  │  │              TemplateContext                       │  │
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
   │ Component    │  │ Component    │  │ Component  │  │ Component  │
   │ Executor     │  │ Executor     │  │ Executor   │  │ Executor   │
   └──────┬───────┘  └──────┬───────┘  └─────┬──────┘  └─────┬──────┘
          │                 │                │               │
          ▼                 ▼                ▼               ▼
  ┌──────────────┐  ┌──────────────┐  ┌────────────┐  ┌────────────┐
  │ 从 VC 决定    │  │ 从 VC 决定   │  │ 从 VC 决定  │  │ 从 VC 决定 │
  │ Install或    │  │ Install或    │  │ 是否 Skip   │  │ Install或  │
  │ Upgrade      │  │ Upgrade      │  │            │  │ Upgrade    │
  └──────────────┘  └──────────────┘  └────────────┘  └────────────┘

  数据流: Scheduler → ExecutionContext → ComponentExecutor → Installer
  控制流: ComponentExecutor 根据 VersionContext 自主决定操作类型
```

#### 9.4.1 ExecutionContext 定义

**设计思路 - 为什么用 VersionContext 而非 IsUpgrade bool 或 OperationType 枚举**：
1. **声明式协调**：Kubernetes 控制器应基于"当前状态 vs 期望状态"自主决定操作，而非由调用方显式下达操作指令。VersionContext 提供版本事实（已安装版本、目标版本），Executor 根据 `HasCurrent`/`HasTarget`/`NeedsUpgrade` 自主推断 Install/Upgrade/Skip。
2. **避免概念重复**：`BinaryAction` (Install/Upgrade/Uninstall) 和 `HelmAction` (Install/Upgrade/Rollback) 已在各自 Executor 中定义。ExecutionContext 中再放 OperationType 枚举会造成两套枚举需要映射，增加维护负担。
3. **扩展性**：后续支持 Rollback 时，只需在 VersionContext 中新增版本历史记录 (`previousVersions map`)，Executor 即可推断 Rollback 操作，无需修改 ExecutionContext 接口。
4. **与实际代码一致**：当前 `PhaseContext` 已使用 `VersionContext` 进行版本判断，设计文档与实现保持一致。

```go
// pkg/dagexec/context.go

// 复用 pkg/upgrade.VersionContext，不在 dagexec 内重复定义。
//
// 现有 pkg/upgrade/context.go 已定义 VersionContext:
//   type VersionContext struct {
//       Current map[string]string  // 已安装版本 (componentName → currentVersion)
//       Target  map[string]string  // 目标版本 (componentName → targetVersion)
//   }
// 并已实现 SetCurrent/SetTarget/GetCurrent/GetTarget/HasTarget/NeedsUpgrade/
//   AnyTargetNeedsUpgrade/TargetNames 等方法。
//
// 现有 PhaseContext.VersionContext 字段亦使用此类型 (pkg/phaseframe/context.go)，
// 设计文档与实现保持一致，复用避免概念重复。
//
// 注意: 现有 pkg/upgrade.VersionContext 暂未提供 HasCurrent/CurrentVersion 方法，
// 需在 pkg/upgrade/context.go 中补充:
//   func (vc *VersionContext) HasCurrent(name string) bool {
//       if vc == nil { return false }
//       _, ok := vc.Current[name]
//       return ok
//   }
//   func (vc *VersionContext) CurrentVersion(name string) (string, bool) {
//       if vc == nil { return "", false }
//       v, ok := vc.Current[name]
//       return v, ok
//   }
//   func (vc *VersionContext) TargetVersion(name string) (string, bool) {
//       if vc == nil { return "", false }
//       v, ok := vc.Target[name]
//       return v, ok
//   }
// NeedsUpgrade 的语义须满足 Executor 决策需要:
//   - vc == nil 或无 Target 记录: 返回 true (默认需要执行)
//   - 无 Current 记录: 返回 true (未安装, 需要安装)
//   - Current != Target: 返回 true (版本不同, 需要升级)
//   - 否则: 返回 false (已在目标版本, 跳过)
// 若现有 NeedsUpgrade 实现与此语义不符，需在 pkg/upgrade 中对齐。

// ExecutionContext 组件执行上下文 (完全独立于 phaseframe)
//
// 设计说明:
// - 不引用 phaseframe 任何类型；Inline 路径通过 InlineRunner 接口桥接
//   (见 9.4.3.4)，由 controllers 层提供适配实现
// - OldCluster 供 InlineComponentExecutor 调用 InlineRunner.Execute 时传入
// - TargetClient 供 Helm/YAML 健康检查访问目标集群 (Pod/Endpoint)
type ExecutionContext struct {
    // 旧集群状态 (Inline 执行器需要，对应原 InlinePhaseRunner.Execute 的 oldCluster 参数)
    OldCluster *bkev1beta1.BKECluster

    // 新集群状态 (期望状态)
    Cluster *bkev1beta1.BKECluster

    // 节点提供者 (抽象接口，不依赖 phaseframe)
    NodeProvider NodeProvider

    // 节点过滤器 (仅 Binary 组件使用，按角色/标签/幂等过滤目标节点)
    NodeFilter NodeFilter

    // 节点状态更新器 (仅 Binary 组件使用，更新 per-node per-component 状态)
    StatusUpdater NodeStatusUpdater

    // 组件状态更新器 (所有组件类型使用，更新组件级状态)
    ComponentStatusUpdater ComponentStatusUpdater

    // 日志记录器
    Log *bkev1beta1.BKELogger

    // 版本上下文 (复用 pkg/upgrade.VersionContext，携带版本事实供 Executor 自主决定操作)
    VersionContext *upgrade.VersionContext

    // 模板上下文 (复用 manifest.TemplateContext)
    TemplateContext manifest.TemplateContext

    // 目标集群 Kubernetes clientset (Helm/YAML 健康检查用)
    // 由 Scheduler 从 manifest.ClusterApplier 复用的远端 client 注入
    TargetClient kubernetes.Interface
}

// NewExecutionContext 创建执行上下文
func NewExecutionContext(
    oldCluster, cluster *bkev1beta1.BKECluster,
    nodeProvider NodeProvider,
    nodeFilter NodeFilter,
    statusUpdater NodeStatusUpdater,
    componentStatusUpdater ComponentStatusUpdater,
    log *bkev1beta1.BKELogger,
    versionContext *upgrade.VersionContext,
    targetClient kubernetes.Interface,
) *ExecutionContext {
    ctx := &ExecutionContext{
        OldCluster:             oldCluster,
        Cluster:                cluster,
        NodeProvider:           nodeProvider,
        NodeFilter:             nodeFilter,
        StatusUpdater:          statusUpdater,
        ComponentStatusUpdater: componentStatusUpdater,
        Log:                    log,
        VersionContext:         versionContext,
        TargetClient:           targetClient,
    }
    
    // 初始化 TemplateContext
    ctx.TemplateContext = manifest.TemplateContext{
        Variables:          make(map[string]string),
        ComponentVariables: make(map[string]string),
    }
    
    // 填充基础字段
    if cluster != nil {
        ctx.TemplateContext.ClusterName = cluster.Name
        ctx.TemplateContext.Namespace = cluster.Namespace
        
        // 填充集群扩展字段和自定义变量
        if cluster.Spec.ClusterConfig != nil {
            // 注入完整配置引用 (通用性与可扩展性)
            ctx.TemplateContext.Config = cluster.Spec.ClusterConfig
            
            spec := cluster.Spec.ClusterConfig.Cluster
            ctx.TemplateContext.KubernetesVersion = spec.KubernetesVersion
            ctx.TemplateContext.OpenFuyaoVersion = spec.OpenFuyaoVersion
            ctx.TemplateContext.APIServer = spec.APIServer
            ctx.TemplateContext.ServiceCIDR = spec.Networking.ServiceCIDR
            ctx.TemplateContext.PodCIDR = spec.Networking.PodCIDR
            ctx.TemplateContext.DNSDomain = spec.Networking.DNSDomain
            ctx.TemplateContext.ImageRegistry = spec.ImageRepo.Domain
            ctx.TemplateContext.ImagePullSecret = spec.ImageRepo.AuthSecretRef.Name
            
            // 注入 ContainerRuntimeCRI (用于 selector condition 评估)
            if spec.ContainerRuntime.CRI != "" {
                ctx.TemplateContext.Variables["ContainerRuntimeCRI"] = spec.ContainerRuntime.CRI
            }
        }
    }
    
    return ctx
}
```

#### 9.4.2 NodeProvider 接口定义

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
    Role      string            // master/worker/etcd
    Labels    map[string]string // 节点标签 (从 BKENode CRD 读取，用于 NodeFilter 标签匹配)
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
        // 从 BKENode CRD 读取标签 (用于 NodeFilter 标签匹配)
        var labels map[string]string
        bkeNode := &configv1beta1.BKENode{}
        if err := p.client.Get(ctx, types.NamespacedName{
            Namespace: cluster.Namespace,
            Name:      ref.Name,
        }, bkeNode); err == nil {
            labels = bkeNode.Labels
        }
        
        node := Node{
            Name:     ref.Name,
            IP:       ref.IP,
            Hostname: ref.Hostname,
            Role:     ref.Role,
            Labels:   labels,
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

#### 9.4.3 ComponentExecutor 接口 (解耦后)

```go
// pkg/dagexec/executor.go

// ComponentExecutor 组件执行器接口 (不再依赖 phaseframe)
type ComponentExecutor interface {
    // ExecuteComponent 执行组件
    ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error
    
    // GetComponentType 获取组件类型
    GetComponentType() ComponentType
}
```

**设计思路 — Executor 与 Installer 的分层设计**：

每种组件类型都有两层：**Executor（调度层）** 和 **Installer（执行层）**，遵循单一职责原则（SRP）：

| 层级 | 职责 | 关心什么 | 可独立性 |
|------|------|---------|---------|
| **Executor** | DAG 调度 | 哪些节点、什么顺序（Rolling/Batch）、失败怎么办（FailFast/Continue/Rollback） | mock Installer 测试调度逻辑 |
| **Installer** | 单节点执行 | 单个节点上怎么下载、渲染、SSH 执行、健康检查 | mock SSH/HTTP 测试安装逻辑 |

**为什么不直接在 Executor 中实现**：
1. **代码复杂度**：Rolling/Batch 并发控制 + SSH 下载/渲染/执行混在一起，单方法 200+ 行
2. **测试困难**：Executor 测试需 mock SSH/HTTP，Installer 测试需 mock DAG 调度，分离后各自独立
3. **复用性**：Installer 可被 DAG 之外的场景复用（CLI 工具、Webhook 触发）
4. **对称设计**：Binary(BinaryComponentExecutor+BinaryInstaller) / Helm(HelmComponentExecutor+HelmInstaller) 模式一致

**TemplateContext 构建职责分工**：
- Executor 填充：`NodeIP`/`NodeHostname`/`NodeRole`/`ComponentVersion`（节点级信息）
- Installer 填充：`NodeArch`（SSH 发现）/`Artifacts`/`Variables`/`ConfigPath`/`Action`/`IsUpgrade`（安装级信息）
- 两者协作完成 TemplateContext 的完整构建，各负责自己层级的信息

**各类型 Executor 的特点**：

| Executor | Installer | 节点级调度 | 单节点执行 | 健康检查 |
|----------|-----------|-----------|-----------|---------|
| BinaryComponentExecutor | BinaryInstaller | ✅ Rolling/Parallel/Batch | SSH 下载+渲染+安装 | 脚本式 SSH |
| HelmComponentExecutor | HelmInstaller | ❌ 无节点级（Helm 部署到集群） | Helm SDK install/upgrade | PodReady/EndpointReady |
| YamlComponentExecutor | YamlInstaller | ❌ 无节点级（YAML 应用到集群） | K8s API Apply | PodReady/EndpointReady |
| InlineComponentExecutor | (无独立 Installer) | ❌ 无节点级（Phase 执行） | InlineRunner.Execute | Phase 自身逻辑 |

**Helm/YAML/Inline 的 Installer 边界说明**：
- **Helm**：`HelmInstaller` 已存在（第 5 章），与 `HelmComponentExecutor` 两层分离——Helm 组件部署到集群而非单节点，无节点级并发控制需求
- **YAML**：`YamlInstaller`（`pkg/yamlinstaller`）负责清单 Apply + 健康检查，与 `YamlComponentExecutor` 两层分离（对称 Binary/Helm）；但 `YamlInstaller` 内部仅 Apply + healthcheck，无 SSH/下载/缓存等复杂子层
- **Inline**：直接调用 `InlineRunner.Execute()`，无制品下载/模板渲染，无需 Installer

**Binary 是唯一需要独立 Installer 的类型**，因为 Binary 组件的安装逻辑最复杂（SSH 发现架构→下载制品→缓存→校验→渲染脚本→渲染配置→SSH 上传→SSH 执行→健康检查），且需要逐节点执行（节点级并发控制）。

##### 9.4.3.1 BinaryComponentExecutor

```go
// BinaryComponentExecutor 二进制组件执行器
type BinaryComponentExecutor struct {
    installer              *binaryinstaller.BinaryInstaller
    cvStore                ComponentVersionStore    // 加载 ComponentVersion (替代 *manifest.Store)
    nodeFilter             NodeFilter               // 节点过滤器 (按角色/标签/幂等过滤)
    statusUpdater          NodeStatusUpdater        // 节点状态更新器 (更新 per-node per-component 状态)
    componentStatusUpdater ComponentStatusUpdater   // 组件状态更新器 (更新组件级状态)
}

func (e *BinaryComponentExecutor) GetComponentType() ComponentType {
    return ComponentTypeBinary
}
```

**设计思路 — Binary 是唯一需要节点级调度的类型**：

Binary 组件（containerd/bkeagent）安装在每个节点上，需要逐节点 SSH 操作。其他类型（Helm/YAML 部署到集群，Inline 调用 Phase）无需节点级并发控制。

**Executor 与 BinaryInstaller 的协作**：
- Executor 负责：获取节点列表 → 选择 Rolling/Parallel/Batch 策略 → 对每个节点构建 InstallOptions（含 NodeIP/NodeRole）→ 调用 `BinaryInstaller.Install()` → 处理 FailurePolicy
- BinaryInstaller 负责：SSH 发现 arch → 下载制品 → 填充 TemplateContext（Artifacts/Paths/Action）→ 渲染脚本 → SSH 执行 → 健康检查
- 边界清晰：Executor 不关心"怎么在单节点上安装"，Installer 不关心"在哪些节点上安装、什么顺序"

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
    
    // 1. 获取 ComponentVersion (通过 ComponentVersionStore)
    cv, err := e.cvStore.GetComponentVersion(ctx, component.Name, component.Version)
    if err != nil {
        return fmt.Errorf("failed to get component version: %w", err)
    }
    
    // 2. 确认是二进制类型
    if cv.Spec.Type != configv1alpha1.ComponentTypeBinary {
        return fmt.Errorf("component %s is not a binary component", component.Name)
    }
    
    // 3. 组件级幂等判断 (VersionContext.NeedsUpgrade)
    vc := execCtx.VersionContext
    if vc != nil && !vc.NeedsUpgrade(component.Name) {
        execCtx.Log.Info("component %s already at target version, skipping", component.Name)
        return nil
    }
    
    // 4. 获取全部节点
    allNodes, err := execCtx.NodeProvider.GetNodes(ctx, execCtx.Cluster)
    if err != nil {
        return fmt.Errorf("failed to get nodes: %w", err)
    }
    
    // 5. 节点级过滤 (NodeFilter)
    // 按角色/标签/幂等/预约节点过滤，返回需要操作的目标节点
    targetNodes, err := e.nodeFilter.Filter(ctx, allNodes, cv, execCtx)
    if err != nil {
        return fmt.Errorf("failed to filter nodes: %w", err)
    }
    if len(targetNodes) == 0 {
        execCtx.Log.Info("component %s: all nodes already at target version, skipping", component.Name)
        return nil
    }
    
    // 6. 根据升级策略执行
    strategy := cv.Spec.UpgradeStrategy
    switch strategy.Mode {
    case "Rolling":
        return e.executeRolling(ctx, targetNodes, cv, strategy, execCtx)
    case "Parallel":
        return e.executeParallel(ctx, targetNodes, cv, strategy, execCtx)
    case "Batch":
        return e.executeBatch(ctx, targetNodes, cv, strategy, execCtx)
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
        
        // 标记节点开始安装
        if e.statusUpdater != nil {
            if err := e.statusUpdater.MarkPending(ctx, execCtx.Cluster, node.IP, cv.Spec.Name); err != nil {
                execCtx.Log.Warn("failed to mark node %s as pending: %v", node.IP, err)
            }
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

        // 注入部署模式变量 isOffline (用于 configTemplates.condition)
        // 复用现有 configureContainerdLegacy:357-364 的 repoInsecure 判定逻辑:
        // imageRegistry 在 insecureRegistries 列表中 (且非 cr.openfuyao.cn) = 离线模式
        // 离线模式: ConfigRenderer 为公共仓库生成 hosts.toml 重定向文件
        if nodeTmpl.Variables == nil {
            nodeTmpl.Variables = make(map[string]string)
        }
        nodeTmpl.Variables["isOffline"] = "false"
        for _, reg := range strings.Split(nodeTmpl.Variables["insecureRegistries"], ",") {
            if strings.TrimSpace(reg) == nodeTmpl.ImageRegistry && nodeTmpl.ImageRegistry != "cr.openfuyao.cn" {
                nodeTmpl.Variables["isOffline"] = "true"
                break
            }
        }

        // 根据 VersionContext 自主决定操作类型
        action := binaryinstaller.BinaryActionInstall
        if execCtx.VersionContext != nil && execCtx.VersionContext.HasCurrent(cv.Spec.Name) {
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
            // 标记节点安装失败
            if e.statusUpdater != nil {
                if markErr := e.statusUpdater.MarkFailed(ctx, execCtx.Cluster, node.IP, cv.Spec.Name, err); markErr != nil {
                    execCtx.Log.Warn("failed to mark node %s as failed: %v", node.IP, markErr)
                }
            }
            
            switch strategy.FailurePolicy {
            case "FailFast":
                return err
            case "Continue":
                execCtx.Log.Warn("node %s upgrade failed, continuing: %v", node.IP, err)
                continue
            case "Rollback":
                // 标记节点开始回滚
                if e.statusUpdater != nil {
                    if markErr := e.statusUpdater.MarkRollback(ctx, execCtx.Cluster, node.IP, cv.Spec.Name); markErr != nil {
                        execCtx.Log.Warn("failed to mark node %s as rolling back: %v", node.IP, markErr)
                    }
                }
                
                if rbErr := e.rollback(node, cv); rbErr != nil {
                    // 回滚失败
                    if e.statusUpdater != nil {
                        if markErr := e.statusUpdater.MarkFailed(ctx, execCtx.Cluster, node.IP, cv.Spec.Name, rbErr); markErr != nil {
                            execCtx.Log.Warn("failed to mark node %s as rollback failed: %v", node.IP, markErr)
                        }
                    }
                    return fmt.Errorf("upgrade failed and rollback failed: %w; rollback: %v", err, rbErr)
                }
                
                // 回滚成功
                if e.statusUpdater != nil {
                    // 获取旧版本
                    oldVersion := ""
                    if execCtx.VersionContext != nil {
                        if v, ok := execCtx.VersionContext.CurrentVersion(cv.Spec.Name); ok {
                            oldVersion = v
                        }
                    }
                    if markErr := e.statusUpdater.MarkSuccess(ctx, execCtx.Cluster, node.IP, cv.Spec.Name, oldVersion); markErr != nil {
                        execCtx.Log.Warn("failed to mark node %s as rolled back: %v", node.IP, markErr)
                    }
                }
                continue
            }
        }
        
        // 标记节点安装成功
        if e.statusUpdater != nil {
            if err := e.statusUpdater.MarkSuccess(ctx, execCtx.Cluster, node.IP, cv.Spec.Name, cv.Spec.Version); err != nil {
                execCtx.Log.Warn("failed to mark node %s as success: %v", node.IP, err)
            }
        }
    }
    
    return nil
}

// executeParallel 并行执行 (全节点同时)
func (e *BinaryComponentExecutor) executeParallel(ctx context.Context, nodes []Node,
    cv *ComponentVersion, strategy UpgradeStrategySpec, execCtx *ExecutionContext) error {
    g, gCtx := errgroup.WithContext(ctx)
    sem := make(chan struct{}, len(nodes)) // 不限制并发数, 全部同时执行
    
    for _, node := range nodes {
        node := node
        g.Go(func() error {
            select {
            case sem <- struct{}{}:
                defer func() { <-sem }()
            case <-gCtx.Done():
                return gCtx.Err()
            }
            
            nodeTmpl := execCtx.TemplateContext
            nodeTmpl.NodeIP = node.IP
            nodeTmpl.NodeHostname = node.Hostname
            nodeTmpl.NodeRole = node.Role
            nodeTmpl.ComponentVersion = cv.Spec.Version
            
            action := binaryinstaller.BinaryActionInstall
            if execCtx.VersionContext != nil && execCtx.VersionContext.HasCurrent(cv.Spec.Name) {
                action = binaryinstaller.BinaryActionUpgrade
            }
            
            opts := binaryinstaller.InstallOptions{
                Component:   cv,
                TemplateCtx: nodeTmpl,
                Action:      action,
                Timeout:     strategy.Timeout,
                RetryCount:  3,
            }
            
            if err := e.installer.Install(gCtx, opts); err != nil {
                if strategy.FailurePolicy == "FailFast" {
                    return err
                }
                execCtx.Log.Warn("node %s upgrade failed, continuing: %v", node.IP, err)
                return nil // Continue: 记录警告, 不中断其他节点
            }
            return nil
        })
    }
    
    return g.Wait()
}

// executeBatch 分批执行 (每批 batchSize 个节点)
func (e *BinaryComponentExecutor) executeBatch(ctx context.Context, nodes []Node,
    cv *ComponentVersion, strategy UpgradeStrategySpec, execCtx *ExecutionContext) error {
    batchSize := strategy.BatchSize
    if batchSize <= 0 {
        batchSize = 1 // 默认逐个执行
    }
    
    for i := 0; i < len(nodes); i += batchSize {
        end := i + batchSize
        if end > len(nodes) {
            end = len(nodes)
        }
        batch := nodes[i:end]
        
        // 批内并行执行
        g, gCtx := errgroup.WithContext(ctx)
        for _, node := range batch {
            node := node
            g.Go(func() error {
                nodeTmpl := execCtx.TemplateContext
                nodeTmpl.NodeIP = node.IP
                nodeTmpl.NodeHostname = node.Hostname
                nodeTmpl.NodeRole = node.Role
                nodeTmpl.ComponentVersion = cv.Spec.Version
                
                action := binaryinstaller.BinaryActionInstall
                if execCtx.VersionContext != nil && execCtx.VersionContext.HasCurrent(cv.Spec.Name) {
                    action = binaryinstaller.BinaryActionUpgrade
                }
                
                opts := binaryinstaller.InstallOptions{
                    Component:   cv,
                    TemplateCtx: nodeTmpl,
                    Action:      action,
                    Timeout:     strategy.Timeout,
                    RetryCount:  3,
                }
                
                if err := e.installer.Install(gCtx, opts); err != nil {
                    if strategy.FailurePolicy == "FailFast" {
                        return err
                    }
                    execCtx.Log.Warn("node %s upgrade failed in batch, continuing: %v", node.IP, err)
                    return nil
                }
                return nil
            })
        }
        
        if err := g.Wait(); err != nil {
            if strategy.FailurePolicy == "FailFast" {
                return err
            }
        }
        execCtx.Log.Info("batch %d-%d completed", i, end-1)
    }
    
    return nil
}

// rollback 回滚单个节点 (执行 UninstallScript)
func (e *BinaryComponentExecutor) rollback(node Node, cv *ComponentVersion) error {
    if cv.Spec.Binary == nil || cv.Spec.Binary.UninstallScript == "" {
        return fmt.Errorf("no uninstall script defined for component %s", cv.Spec.Name)
    }
    
    tmplCtx := manifest.TemplateContext{
        NodeIP:           node.IP,
        NodeHostname:     node.Hostname,
        NodeRole:         node.Role,
        ComponentVersion: cv.Spec.Version,
    }
    
    opts := binaryinstaller.InstallOptions{
        Component:   cv,
        TemplateCtx: tmplCtx,
        Action:      binaryinstaller.BinaryActionUninstall,
    }
    
    return e.installer.Install(context.Background(), opts)
}
```

##### 9.4.3.2 HelmComponentExecutor

**设计思路 — Helm 组件无需节点级调度**：

Helm 组件通过 Helm SDK 部署到目标集群（`helm install/upgrade`），不涉及逐节点 SSH 操作。因此 HelmComponentExecutor 无 Rolling/Parallel/Batch 策略，直接调用 `HelmInstaller.Install()` 一次完成。

**与 HelmInstaller 的协作**：
- Executor 负责：获取 ComponentVersion → VersionContext 判断 Install/Upgrade → 读取 `Strategy.Mode` 覆盖 Action → 构建 InstallOptions → 调用 `HelmInstaller.Install()` → FailurePolicy=Rollback 时触发 `helm rollback`
- HelmInstaller 负责：拉取 Chart → 渲染 Values → 执行 helm install/upgrade（含 `--wait`/`--atomic`）→ 健康检查
- 与 Binary 的区别：Helm 无节点级并发（部署到集群而非单节点），但需处理 `Strategy.Mode=Rollback` 和 `FailurePolicy=Rollback` 两种回滚场景

```go
// HelmComponentExecutor Helm 组件执行器
type HelmComponentExecutor struct {
    installer              *helminstaller.HelmInstaller
    cvStore                ComponentVersionStore    // 加载 ComponentVersion (替代 *manifest.Store)
    componentStatusUpdater ComponentStatusUpdater   // 组件状态更新器 (更新组件级状态)
}

func (e *HelmComponentExecutor) GetComponentType() ComponentType {
    return ComponentTypeHelm
}

// ExecuteComponent 执行 Helm 组件
func (e *HelmComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error {
    component := node.Component
    
    // 1. 获取 ComponentVersion (通过 ComponentVersionStore，manifest.Store 无此方法)
    cv, err := e.cvStore.GetComponentVersion(ctx, component.Name, component.Version)
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
    
    // 4. 标记组件开始安装
    if e.componentStatusUpdater != nil {
        if err := e.componentStatusUpdater.MarkPending(ctx, execCtx.Cluster, component.Name, "helm"); err != nil {
            execCtx.Log.Warn("failed to mark component %s as pending: %v", component.Name, err)
        }
    }
    
    // 5. 确定操作类型 (Strategy.Mode 优先, 为空时由 VersionContext 自动判定)
    action := helminstaller.HelmActionInstall
    switch cv.Spec.Helm.Strategy.Mode {
    case "Rollback":
        action = helminstaller.HelmActionRollback // 显式回滚
    case "Install":
        action = helminstaller.HelmActionInstall // 显式安装
    case "Upgrade":
        action = helminstaller.HelmActionUpgrade // 显式升级
    default:
        // Mode 为空: 由 VersionContext 自动判定
        if vc != nil && vc.HasCurrent(cv.Spec.Name) {
            action = helminstaller.HelmActionUpgrade // 已安装 → 升级
        }
    }
    
    // 6. 填充 TemplateContext 的版本信息
    tmpl := execCtx.TemplateContext
    tmpl.ComponentVersion = cv.Spec.Version
    
    // 7. 执行 Helm 操作
    opts := helminstaller.InstallOptions{
        Component:   cv,
        TemplateCtx: tmpl,
        Action:      action,
        Timeout:     cv.Spec.UpgradeStrategy.Timeout,
    }
    
    if err := e.installer.Install(ctx, opts); err != nil {
        // FailurePolicy=Rollback: 升级失败后执行 helm rollback
        // 仅当 Atomic=false 时需要手动回滚 (Atomic=true 时 Helm SDK 已自动回滚)
        if cv.Spec.UpgradeStrategy.FailurePolicy == "Rollback" &&
           action == helminstaller.HelmActionUpgrade &&
           !cv.Spec.Helm.Strategy.Atomic {
            // 标记组件开始回滚
            if e.componentStatusUpdater != nil {
                if markErr := e.componentStatusUpdater.MarkRollback(ctx, execCtx.Cluster, component.Name, "helm"); markErr != nil {
                    execCtx.Log.Warn("failed to mark component %s as rolling back: %v", component.Name, markErr)
                }
            }
            
            execCtx.Log.Warn("helm upgrade failed, attempting rollback: %v", err)
            rollbackOpts := helminstaller.InstallOptions{
                Component:   cv,
                TemplateCtx: tmpl,
                Action:      helminstaller.HelmActionRollback,
                Timeout:     cv.Spec.UpgradeStrategy.Timeout,
            }
            if rbErr := e.installer.Install(ctx, rollbackOpts); rbErr != nil {
                // 回滚失败
                if e.componentStatusUpdater != nil {
                    if markErr := e.componentStatusUpdater.MarkFailed(ctx, execCtx.Cluster, component.Name, "helm", rbErr); markErr != nil {
                        execCtx.Log.Warn("failed to mark component %s as rollback failed: %v", component.Name, markErr)
                    }
                }
                return fmt.Errorf("upgrade failed: %w; rollback also failed: %v", err, rbErr)
            }
            
            // 回滚成功
            if e.componentStatusUpdater != nil {
                // 获取旧版本
                oldVersion := ""
                if vc != nil {
                    if v, ok := vc.CurrentVersion(component.Name); ok {
                        oldVersion = v
                    }
                }
                if markErr := e.componentStatusUpdater.MarkSuccess(ctx, execCtx.Cluster, component.Name, "helm", oldVersion); markErr != nil {
                    execCtx.Log.Warn("failed to mark component %s as rolled back: %v", component.Name, markErr)
                }
            }
            execCtx.Log.Info("helm rollback succeeded after upgrade failure")
            return fmt.Errorf("upgrade failed but rollback succeeded: %w", err)
        }
        
        // 标记组件安装失败（非回滚场景）
        if e.componentStatusUpdater != nil {
            if markErr := e.componentStatusUpdater.MarkFailed(ctx, execCtx.Cluster, component.Name, "helm", err); markErr != nil {
                execCtx.Log.Warn("failed to mark component %s as failed: %v", component.Name, markErr)
            }
        }
        return err
    }
    
    // 8. 标记组件安装成功
    if e.componentStatusUpdater != nil {
        if err := e.componentStatusUpdater.MarkSuccess(ctx, execCtx.Cluster, component.Name, "helm", cv.Spec.Version); err != nil {
            execCtx.Log.Warn("failed to mark component %s as success: %v", component.Name, err)
        }
    }
    
    return nil
}
```

##### 9.4.3.3 YamlComponentExecutor

**设计思路 — YAML 组件两层模型（与 Binary/Helm 对称）**：

YAML 组件采用两层结构：`YamlInstaller`（`pkg/yamlinstaller` 引擎层，负责清单 Apply + 健康检查，**详见第 6 章**）+ `YamlComponentExecutor`（dagexec 调度层，本节）。本节仅描述调度层 `YamlComponentExecutor`，引擎层接口定义见 **6.3 核心接口定义**。

```go
// pkg/dagexec/yaml_component_executor.go

// YamlComponentExecutor YAML 组件执行器（调度层）
// 持有 *yamlinstaller.YamlInstaller + ComponentVersionStore，
// 负责 VersionContext 判断 + CV 加载 + 委托 YamlInstaller.Apply 执行。
type YamlComponentExecutor struct {
    installer              *yamlinstaller.YamlInstaller
    cvStore                ComponentVersionStore    // 加载 ComponentVersion
    componentStatusUpdater ComponentStatusUpdater   // 组件状态更新器 (更新组件级状态)
}

func (e *YamlComponentExecutor) GetComponentType() ComponentType {
    return ComponentTypeYAML
}

// ExecuteComponent 执行 YAML/Manifest 组件
func (e *YamlComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error {
    component := node.Component

    // 1. 获取 ComponentVersion (通过 ComponentVersionStore)
    cv, err := e.cvStore.GetComponentVersion(ctx, component.Name, component.Version)
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

    // 4. 标记组件开始安装
    if e.componentStatusUpdater != nil {
        if err := e.componentStatusUpdater.MarkPending(ctx, execCtx.Cluster, component.Name, "yaml"); err != nil {
            execCtx.Log.Warn("failed to mark component %s as pending: %v", component.Name, err)
        }
    }

    // 5. 委托 YamlInstaller 执行 Apply + 健康检查
    if err := e.installer.Apply(ctx, cv, execCtx); err != nil {
        // 标记组件安装失败
        if e.componentStatusUpdater != nil {
            if markErr := e.componentStatusUpdater.MarkFailed(ctx, execCtx.Cluster, component.Name, "yaml", err); markErr != nil {
                execCtx.Log.Warn("failed to mark component %s as failed: %v", component.Name, markErr)
            }
        }
        return err
    }

    // 6. 标记组件安装成功
    if e.componentStatusUpdater != nil {
        if err := e.componentStatusUpdater.MarkSuccess(ctx, execCtx.Cluster, component.Name, "yaml", cv.Spec.Version); err != nil {
            execCtx.Log.Warn("failed to mark component %s as success: %v", component.Name, err)
        }
    }

    return nil
}
```

> 健康检查由 `YamlInstaller.Apply` 内部委托共享 `pkg/healthcheck` 包完成（见 **第 7 章 HealthCheck 共享包设计**）。

##### 9.4.3.4 InlineComponentExecutor

**设计思路 — Inline 是最简执行器，无 Installer、无调度策略**：

Inline 组件通过 `ComponentFactory` 注册的 handler 执行（如 `EnsureMasterInit`/`EnsureWorkerJoin`），是已有 Phase 逻辑的适配层。无制品下载、无模板渲染、无 SSH 操作，直接委托给 `InlineRunner.Execute()`。

**为什么 Inline 不需要 Installer**：
- Inline handler 内部已封装全部逻辑（kubeadm init/join 等），无需外部的下载/渲染/安装步骤
- Inline 不操作远程节点（通过 K8s API 或本地命令完成），无需 SSH
- 健康检查由 Phase 自身的 `NeedExecute()` 逻辑处理，无需额外健康检查

**与其他 Executor 的区别**：
- Binary：Executor 调度（Rolling/Batch）+ BinaryInstaller 执行（SSH）
- Helm：Executor 调度（Mode 覆盖）+ HelmInstaller 执行（Helm SDK）
- YAML：Executor 调度（VersionContext 判断）+ YamlInstaller 执行（K8s API Apply + 健康检查）
- Inline：Executor 直接委托（InlineRunner.Execute），仅做接口适配

**适配 ComponentExecutor 接口的目的**：
Inline 原有逻辑通过 `phaseframe.Phase` 执行，适配为 `ComponentExecutor` 后可统一注册到 `ExecutorRegistry`，由 DAG Scheduler 统一调度，无需为 Inline 类型走独立的调度路径。

```go
// InlineRunner 内联组件执行接口 (不依赖 phaseframe)
//
// 设计思路 - 为什么新增此接口而非复用 dagexec.InlinePhaseRunner:
// 现有 dagexec.InlinePhaseRunner.Execute(phaseCtx *phaseframe.PhaseContext, ...)
// 的签名直接引用 phaseframe.PhaseContext，使 pkg/dagexec 被 phaseframe 绑定。
// 为彻底解耦，dagexec 定义此 phaseframe-free 接口；由 controllers 层提供
// 适配实现 (InlinePhaseRunnerAdapter)，内部桥接 componentfactory.PhaseRunner
// (其实现仍用 phaseframe.Phase，但适配器把 phaseframe 类型限定在 controllers 包内)。
//
// 这样 pkg/dagexec 不再 import pkg/phaseframe，可独立编译与测试。
type InlineRunner interface {
    Execute(ctx context.Context, oldCluster, newCluster *bkev1beta1.BKECluster, handler, version string) error
}

// InlineComponentExecutor 内联组件执行器
// Inline 组件通过 ComponentFactory 注册的 handler 执行, 无需制品下载/模板渲染
// 适配 ComponentExecutor 接口, 统一通过 DAG 调度
type InlineComponentExecutor struct {
    runner InlineRunner
}

func (e *InlineComponentExecutor) GetComponentType() ComponentType {
    return ComponentTypeInline
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

    // Inline 执行器需要 oldCluster/newCluster, 从 ExecutionContext 获取
    // Inline 不使用 VersionContext (由 Phase 自身的 NeedExecute 逻辑决定是否执行)
    // 不再传递 phaseframe.PhaseContext；所需依赖由 InlineRunner 适配实现自行注入
    return e.runner.Execute(ctx, execCtx.OldCluster, execCtx.Cluster, handler, version)
}
```

**controllers 层适配实现示例** (phaseframe 类型仅出现在此适配层，不泄漏到 dagexec):

```go
// controllers/capbke/inline_runner_adapter.go
package capbke

// InlinePhaseRunnerAdapter 把 componentfactory.PhaseRunner (依赖 phaseframe)
// 适配为 dagexec.InlineRunner (phaseframe-free)。
type InlinePhaseRunnerAdapter struct {
    phaseCtx *phaseframe.PhaseContext   // phaseframe 限定在 controllers 包内
    runner   *componentfactory.PhaseRunner
}

func NewInlinePhaseRunnerAdapter(phaseCtx *phaseframe.PhaseContext, runner *componentfactory.PhaseRunner) *InlinePhaseRunnerAdapter {
    return &InlinePhaseRunnerAdapter{phaseCtx: phaseCtx, runner: runner}
}

// Execute 实现 dagexec.InlineRunner，桥接回 phaseframe.PhaseContext
func (a *InlinePhaseRunnerAdapter) Execute(ctx context.Context, oldCluster, newCluster *bkev1beta1.BKECluster, handler, version string) error {
    return a.runner.Execute(a.phaseCtx, oldCluster, newCluster, handler, version)
}
```

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

### 9.5 状态模型、幂等性与兼容性设计

#### 9.5.1 问题分析

当前 KEP-6 设计存在三个状态相关缺口：

| 缺口 | 当前设计 | 应有行为 |
|------|---------|---------|
| **节点过滤** | `NodeProvider.GetNodes()` 返回全部节点，无过滤 | 应排除 Failed/Deleting/Skipped/已完成节点 |
| **per-node 幂等** | `VersionContext.NeedsUpgrade(name)` 是组件级判断 | 应支持 per-node 判断（node1 已升级，node2 未升级） |
| **状态回写** | 无状态回写逻辑 | 应在安装成功/失败后更新 per-node per-component 状态 |

当前代码中各组件的节点过滤逻辑各不相同：

| 组件 | 当前过滤函数 | 过滤条件 |
|------|------------|---------|
| bkeagent (安装) | `GetNeedPushAgentNodesWithBKENodes` | `!NodeAgentPushedFlag` + Appointment 排除 |
| bkeagent (升级) | **无过滤** | 全部节点都执行 |
| containerd | `GetNeedUpgradeNodesWithBKENodes` | `OpenFuyaoVersion` 版本比较 |
| kubernetes | `GetNeedUpgradeK8sNodes` + 角色过滤 | `KubernetesVersion` + master/worker |
| etcd | `filterUpgradeableNodes` + `.Etcd()` | `EtcdVersion` + etcd 角色 |

#### 9.5.2 分层架构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          编排层 (Orchestration Layer)                            │
│                                                                                 │
│  DAG Scheduler                                                                  │
│  ├─ 拓扑排序 → 执行批次                                                          │
│  ├─ 批次间串行、批次内并行                                                        │
│  └─ FailurePolicy (FailFast/Continue/Rollback)                                  │
│                                                                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                          执行器层 (Executor Layer)                               │
│                                                                                 │
│  BinaryComponentExecutor                                                        │
│  ├─ 加载 ComponentVersion                                                       │
│  ├─ 组件级幂等判断 (VersionContext.NeedsUpgrade)                                 │
│  ├─ 获取节点列表 (NodeProvider.GetNodes)                                         │
│  ├─ 节点级过滤 (NodeFilter.Filter)              ← 新增接口                       │
│  ├─ 按策略执行 (Rolling/Parallel/Batch)                                         │
│  ├─ 每节点状态更新 (NodeStatusUpdater)          ← 新增接口                       │
│  ├─ 组件级状态更新 (ComponentStatusUpdater)     ← 新增接口                       │
│  └─ 委托 BinaryInstaller.Install()                                              │
│                                                                                 │
│  HelmComponentExecutor / YamlComponentExecutor                                  │
│  ├─ 加载 ComponentVersion                                                       │
│  ├─ 组件级幂等判断 (VersionContext.NeedsUpgrade)                                 │
│  ├─ 组件级状态更新 (ComponentStatusUpdater)     ← 新增接口                       │
│  └─ 委托 HelmInstaller.Install() / YamlInstaller.Apply()                        │
│                                                                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                          安装层 (Installer Layer)                                │
│                                                                                 │
│  BinaryInstaller                                                                │
│  ├─ SSH 发现架构                                                                 │
│  ├─ 下载制品 + 校验                                                              │
│  ├─ 渲染脚本/配置                                                                │
│  ├─ SSH 上传 + 执行                                                              │
│  └─ 健康检查                                                                     │
│  ※ 不感知节点状态、不做过滤、不更新状态                                            │
│                                                                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                          状态层 (State Layer)                                    │
│                                                                                  │
│  BKECluster.Status.NodeComponentStatuses (per-node per-component)  ← 新增        │
│  BKECluster.Status.ComponentStatuses (组件级，所有类型共用)        ← 新增          │
│  BKENode.Status.StateCode (per-node 位标记，向后兼容)                             │
│  BKECluster.Status.*Version (集群级版本，向后兼容)                                │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 9.5.3 状态数据模型

**新增 `ComponentPhase` 状态枚举**：

```go
// api/bkecommon/v1beta1/bkecluster_status.go 扩展

// ComponentPhase 组件安装/升级阶段
type ComponentPhase string

const (
    // 基础状态
    ComponentPhasePending        ComponentPhase = "Pending"         // 等待安装（初始状态）
    ComponentPhaseInstalling     ComponentPhase = "Installing"      // 首次安装中
    ComponentPhaseUpgrading      ComponentPhase = "Upgrading"       // 升级中（已有旧版本）
    ComponentPhaseInstalled      ComponentPhase = "Installed"       // 安装/升级成功
    ComponentPhaseFailed         ComponentPhase = "Failed"          // 安装/升级失败
    
    // 回滚相关状态
    ComponentPhaseRollingBack    ComponentPhase = "RollingBack"     // 正在回滚到旧版本
    ComponentPhaseRolledBack     ComponentPhase = "RolledBack"      // 回滚成功，已恢复到旧版本
    
    // Binary 组件特有状态
    ComponentPhasePartialSuccess ComponentPhase = "PartialSuccess"  // 部分节点成功，部分节点失败
    
    // 异常状态
    ComponentPhaseTimeout        ComponentPhase = "Timeout"         // 安装/升级超时
)
```

**状态说明**：

| 状态 | 适用组件 | 说明 |
|------|---------|------|
| `Pending` | 所有 | 初始状态，等待安装 |
| `Installing` | Binary/Helm/YAML | 首次安装中 |
| `Upgrading` | Binary/Helm/YAML | 升级中（已有旧版本） |
| `Installed` | 所有 | 安装/升级成功 |
| `Failed` | 所有 | 安装/升级失败 |
| `RollingBack` | Helm/Binary | 正在回滚到旧版本 |
| `RolledBack` | Helm/Binary | 回滚成功，已恢复到旧版本 |
| `PartialSuccess` | Binary | 部分节点成功，部分节点失败 |
| `Timeout` | 所有 | 安装/升级超时（可通过超时检测自动设置） |

**新增 `NodeComponentStatuses` 字段**：

```go
// api/bkecommon/v1beta1/bkecluster_status.go 扩展

type BKEClusterStatus struct {
    // ... 现有字段保持不变 ...

    // 每节点每组件安装状态 (KEP-6 新增)
    // key 外层: 组件名 (如 "containerd", "bkeagent")
    // key 内层: 节点 IP
    // 用于 Binary 组件的 per-node 幂等判断和状态追踪
    // Helm/YAML/Inline 组件不写入此字段 (它们是集群级部署)
    NodeComponentStatuses map[string]map[string]NodeComponentStatus `json:"nodeComponentStatuses,omitempty"`
}

// NodeComponentStatus 单个节点上单个组件的安装状态
type NodeComponentStatus struct {
    // 已安装版本 (如 "v1.7.18")
    Version string `json:"version"`

    // 安装阶段 (使用 ComponentPhase 枚举)
    Phase ComponentPhase `json:"phase"`

    // 最后更新时间
    LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`

    // 错误信息 (Phase=Failed/Timeout 时)
    Message string `json:"message,omitempty"`
}

// ComponentStatus 组件级安装状态 (所有组件类型共用)
type ComponentStatus struct {
    // 已安装版本 (如 "v1.11.1")
    Version string `json:"version"`

    // 安装阶段 (使用 ComponentPhase 枚举)
    Phase ComponentPhase `json:"phase"`

    // 组件类型: binary / helm / yaml
    Type string `json:"type"`

    // 最后更新时间
    LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`

    // 错误信息 (Phase=Failed/Timeout 时)
    Message string `json:"message,omitempty"`

    // 成功节点数 (仅 Binary 组件，Phase=PartialSuccess 时)
    SuccessNodes int `json:"successNodes,omitempty"`

    // 失败节点数 (仅 Binary 组件，Phase=PartialSuccess 时)
    FailedNodes int `json:"failedNodes,omitempty"`
}
```

**设计决策 — 为什么放在 BKECluster.Status 而非 BKENode.Status**：

| 维度 | BKECluster.Status.NodeComponentStatuses | BKENode.Status.ComponentStatuses |
|------|----------------------------------------|----------------------------------|
| 更新操作 | 1 次 Patch (整个 BKECluster) | N 次 Update (每个 BKENode) |
| 并发冲突 | 低 (单对象) | 高 (N 个对象同时更新) |
| 性能 | ✅ 优 | ❌ 差 (N 次 API 调用) |

**状态模型全景**：

```
BKECluster.Status
├── KubernetesVersion          ← 集群级 (现有，向后兼容)
├── ContainerdVersion          ← 集群级 (现有，向后兼容)
├── EtcdVersion                ← 集群级 (现有，向后兼容)
├── OpenFuyaoVersion           ← 集群级 (现有，向后兼容)
├── ComponentStatuses          ← 组件级 (所有类型共用)
│   └── [name] → ComponentStatus { Version, Phase, Type, ... }
└── NodeComponentStatuses      ← per-node per-component (新增，仅 Binary)
    └── [componentName] → [nodeIP] → NodeComponentStatus { Version, Phase, ... }

BKENode.Status
├── State                      ← 节点整体状态 (现有)
├── StateCode                  ← 位标记 (现有，向后兼容)
│   ├── NodeAgentPushedFlag    ← bit 0
│   ├── NodeAgentReadyFlag     ← bit 1
│   ├── NodeEnvFlag            ← bit 2
│   └── ...
├── Message                    ← 状态消息 (现有)
└── NeedSkip                   ← 跳过标记 (现有)
```

**职责分工**：

| 状态存储 | 谁写入 | 谁读取 | 用途 |
|---------|--------|--------|------|
| `BKECluster.Status.*Version` | 兼容层 (Feature Gate OFF) | VersionContext 构建 | 集群级版本判断 |
| `BKECluster.Status.ComponentStatuses` | ComponentStatusUpdater (所有 Executor) | DAG 调度器、UI | 组件级状态展示 |
| `BKECluster.Status.NodeComponentStatuses` | NodeStatusUpdater (BinaryComponentExecutor) | NodeFilter、BinaryComponentExecutor | per-node 幂等判断 |
| `BKENode.Status.StateCode` | 兼容层 (Feature Gate OFF) | NodeFilter (兼容模式) | 向后兼容 |

#### 9.5.4 NodeFilter 接口

**接口定义**：

```go
// pkg/dagexec/node_filter.go

// NodeFilter 节点过滤接口
//
// 设计思路 — 为什么不内置到 BinaryComponentExecutor:
// 1. 不同组件的过滤逻辑不同 (bkeagent 按位标记，containerd 按版本比较)
// 2. 过滤逻辑可能随组件类型演化，Executor 不应绑定特定实现
// 3. 测试时可注入 Mock Filter，独立测试 Executor 调度逻辑
//
// 设计思路 — 为什么不内置到 NodeProvider:
// NodeProvider 职责是"获取节点"，NodeFilter 职责是"过滤节点"
// 两者关注点不同: Provider 关心数据来源，Filter 关心业务逻辑
//
// 设计思路 — 为什么仅用于 Binary 组件:
// Binary 组件直接在节点上 SSH 执行，需要 Controller 选择目标节点
// Helm/YAML 组件部署到集群，节点调度由 K8s Scheduler 通过 nodeSelector 处理
type NodeFilter interface {
    // Filter 返回需要执行操作的节点列表
    Filter(ctx context.Context, nodes []Node, cv *configv1alpha1.ComponentVersion, execCtx *ExecutionContext) ([]Node, error)
}
```

**默认实现: BKENodeFilter**：

```go
// pkg/dagexec/bke_node_filter.go

type BKENodeFilter struct {
    client client.Client
}

func (f *BKENodeFilter) Filter(
    ctx context.Context,
    nodes []Node,
    cv *configv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) ([]Node, error) {
    nf := cv.Spec.NodeFilter
    skipCompleted := true
    if nf != nil && nf.SkipCompleted != nil {
        skipCompleted = *nf.SkipCompleted
    }
    excludeAppointment := true
    if nf != nil && nf.ExcludeAppointment != nil {
        excludeAppointment = *nf.ExcludeAppointment
    }

    var targetNodes []Node
    for _, node := range nodes {
        // 1. 硬排除: Failed/Deleting/Skipped (不可配置，安全约束)
        if f.isExcluded(ctx, node, execCtx) {
            continue
        }

        // 2. 角色过滤
        if nf != nil && len(nf.Roles) > 0 {
            if !slices.Contains(nf.Roles, node.Role) {
                continue
            }
        }

        // 3. 标签过滤
        if nf != nil && len(nf.MatchLabels) > 0 {
            if !matchLabels(node.Labels, nf.MatchLabels) {
                continue
            }
        }

        // 4. 预约节点排除
        if excludeAppointment && f.isAppointmentNode(node, execCtx) {
            continue
        }

        // 5. per-node 幂等
        if skipCompleted && f.isAlreadyAtTarget(ctx, node, cv, execCtx) {
            continue
        }

        targetNodes = append(targetNodes, node)
    }

    return targetNodes, nil
}

func matchLabels(nodeLabels, selector map[string]string) bool {
    for k, v := range selector {
        if nodeLabels[k] != v {
            return false
        }
    }
    return true
}
```

**isExcluded — 硬排除 (不可配置)**：

```go
func (f *BKENodeFilter) isExcluded(ctx context.Context, node Node, execCtx *ExecutionContext) bool {
    bkeNode := &configv1beta1.BKENode{}
    err := f.client.Get(ctx, types.NamespacedName{
        Namespace: execCtx.Cluster.Namespace,
        Name:      node.Name,
    }, bkeNode)
    if err != nil {
        return true
    }

    if bkeNode.Status.StateCode&configv1beta1.NodeFailedFlag != 0 {
        return true
    }
    if bkeNode.Status.StateCode&configv1beta1.NodeDeletingFlag != 0 {
        return true
    }
    if bkeNode.Status.NeedSkip {
        return true
    }
    return false
}
```

**isAlreadyAtTarget — per-node 幂等 (双源读取)**：

```go
func (f *BKENodeFilter) isAlreadyAtTarget(
    ctx context.Context,
    node Node,
    cv *configv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) bool {
    componentName := cv.Spec.Name
    targetVersion := cv.Spec.Version

    // 优先: 从 NodeComponentStatuses 读取 (新模型)
    if execCtx.Cluster.Status.NodeComponentStatuses != nil {
        if compStatuses, ok := execCtx.Cluster.Status.NodeComponentStatuses[componentName]; ok {
            if status, ok := compStatuses[node.IP]; ok {
                if status.Phase == "Installed" && status.Version == targetVersion {
                    return true
                }
                if status.Phase == "Installing" {
                    return true
                }
                return false
            }
        }
    }

    // 回退: 从 BKENode.StateCode 读取 (旧模型，向后兼容)
    bkeNode := &configv1beta1.BKENode{}
    err := f.client.Get(ctx, types.NamespacedName{
        Namespace: execCtx.Cluster.Namespace,
        Name:      node.Name,
    }, bkeNode)
    if err != nil {
        return false
    }

    switch componentName {
    case "bkeagent":
        if execCtx.VersionContext != nil && !execCtx.VersionContext.HasCurrent("bkeagent") {
            return bkeNode.Status.StateCode&configv1beta1.NodeAgentPushedFlag != 0
        }
        return false
    default:
        return false
    }
}

// 🆕增强说明: isAlreadyAtTarget 在扩容+升级并发场景下增加版本过期检测:
// - Phase=Failed/Timeout → 不跳过，允许重试（安装到最新目标版本）
// - Phase=Installed 但 Version != targetVersion → 不跳过，允许升级
// - 防止安装失败后目标版本变更时，重试安装到旧版本
// 完整实现见 9.5.11.3 节。
```

**各组件配置样例**：

```yaml
# bkeagent 安装 (首次推送)
spec:
  nodeFilter:
    skipCompleted: true
    excludeAppointment: true

# bkeagent 升级
spec:
  nodeFilter:
    skipCompleted: false       # 不过滤，所有节点都执行

# containerd — 仅安装到 compute 节点池
spec:
  nodeFilter:
    matchLabels:
      node-pool: compute

# kubernetes-master
spec:
  nodeFilter:
    roles: ["master"]

# etcd
spec:
  nodeFilter:
    roles: ["etcd"]

# GPU 节点专用组件
spec:
  nodeFilter:
    matchLabels:
      gpu: "true"
      accelerator: nvidia
```

**与当前代码的等价性验证**：

| 组件 | 当前过滤函数 | NodeFilterSpec 配置 | 等价性 |
|------|------------|-------------------|--------|
| EnsureBKEAgent | `!NodeAgentPushedFlag` + Appointment 排除 | `skipCompleted: true, excludeAppointment: true` | ✅ |
| EnsureAgentUpgrade | 无过滤 | `skipCompleted: false` | ✅ |
| EnsureMasterUpgrade | `.Master()` | `roles: ["master"]` | ✅ |
| EnsureWorkerUpgrade | `.Worker()` | `roles: ["worker"]` | ✅ |
| EnsureEtcdUpgrade | `.Etcd()` | `roles: ["etcd"]` | ✅ |
| GPU 组件 (新增) | 无对应 Phase | `matchLabels: {"gpu": "true"}` | ✅ 新能力 |

#### 9.5.5 NodeStatusUpdater 接口

**接口定义**：

```go
// pkg/dagexec/node_status_updater.go

// NodeStatusUpdater 节点状态更新接口
//
// 设计思路 — 为什么不直接在 Executor 中更新:
// 1. 状态更新涉及 BKECluster.Status 和 BKENode.Status 两个对象
// 2. 需要处理并发冲突 (retry.RetryOnConflict)
// 3. 需要处理新旧状态模型的兼容 (NodeComponentStatuses vs StateCode)
// 4. 测试时可注入 Mock，独立测试 Executor 逻辑
type NodeStatusUpdater interface {
    // MarkPending 标记节点开始安装/升级
    MarkPending(ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, componentName string) error

    // MarkSuccess 标记节点安装/升级/回滚成功
    MarkSuccess(ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, componentName string, version string) error

    // MarkFailed 标记节点安装/升级/回滚失败
    MarkFailed(ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, componentName string, err error) error

    // MarkTimeout 标记节点安装/升级超时
    MarkTimeout(ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, componentName string) error

    // MarkRollback 标记节点开始回滚
    MarkRollback(ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, componentName string) error
}
```

**默认实现: BKENodeStatusUpdater**：

```go
// pkg/dagexec/bke_node_status_updater.go

type BKENodeStatusUpdater struct {
    client client.Client
}

func (u *BKENodeStatusUpdater) MarkPending(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
) error {
    return u.updateNodeComponentStatus(ctx, cluster, nodeIP, componentName, NodeComponentStatus{
        Phase:          "Installing",
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

func (u *BKENodeStatusUpdater) MarkSuccess(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
    version string,
) error {
    return u.updateNodeComponentStatus(ctx, cluster, nodeIP, componentName, NodeComponentStatus{
        Version:        version,
        Phase:          "Installed",
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

// 🆕增强说明: MarkSuccess 在 componentName == "bkeagent" 时，需同步设置
// BKENode.Status.StateCode 的 NodeAgentReadyFlag (bit 1)，确保后续
// EnsureNodesEnv 能通过 StateCode 判断 bkeagent 就绪。
// 完整实现见 9.5.11.1 节。

func (u *BKENodeStatusUpdater) MarkFailed(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
    installErr error,
) error {
    existingVersion := ""
    if cluster.Status.NodeComponentStatuses != nil {
        if compStatuses, ok := cluster.Status.NodeComponentStatuses[componentName]; ok {
            if status, ok := compStatuses[nodeIP]; ok {
                existingVersion = status.Version
            }
        }
    }

    return u.updateNodeComponentStatus(ctx, cluster, nodeIP, componentName, NodeComponentStatus{
        Version:        existingVersion,
        Phase:          "Failed",
        Message:        installErr.Error(),
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

func (u *BKENodeStatusUpdater) updateNodeComponentStatus(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
    status NodeComponentStatus,
) error {
    return retry.RetryOnConflict(retry.DefaultRetry, func() error {
        latest := &bkev1beta1.BKECluster{}
        if err := u.client.Get(ctx, types.NamespacedName{
            Namespace: cluster.Namespace,
            Name:      cluster.Name,
        }, latest); err != nil {
            return err
        }

        if latest.Status.NodeComponentStatuses == nil {
            latest.Status.NodeComponentStatuses = make(map[string]map[string]NodeComponentStatus)
        }
        if latest.Status.NodeComponentStatuses[componentName] == nil {
            latest.Status.NodeComponentStatuses[componentName] = make(map[string]NodeComponentStatus)
        }

        latest.Status.NodeComponentStatuses[componentName][nodeIP] = status

        return u.client.Status().Update(ctx, latest)
    })
}
```

**ComponentStatusUpdater 接口**：

```go
// pkg/dagexec/component_status_updater.go

// ComponentStatusUpdater 组件级状态更新接口
//
// 设计思路 — 为什么需要独立的组件级状态更新接口:
// 1. Helm/YAML 组件是集群级部署，无 per-node 状态，不需要 NodeStatusUpdater
// 2. 但所有组件类型都需要组件级状态追踪 (ComponentStatuses)
// 3. Binary 组件同时需要节点级和组件级状态更新
// 4. 接口分离避免职责混淆，测试时可独立 Mock
type ComponentStatusUpdater interface {
    // MarkPending 标记组件开始安装/升级
    MarkPending(ctx context.Context, cluster *bkev1beta1.BKECluster, componentName string, componentType string) error

    // MarkSuccess 标记组件安装/升级/回滚成功
    MarkSuccess(ctx context.Context, cluster *bkev1beta1.BKECluster, componentName string, componentType string, version string) error

    // MarkFailed 标记组件安装/升级/回滚失败
    MarkFailed(ctx context.Context, cluster *bkev1beta1.BKECluster, componentName string, componentType string, err error) error

    // MarkTimeout 标记组件安装/升级超时
    MarkTimeout(ctx context.Context, cluster *bkev1beta1.BKECluster, componentName string, componentType string) error

    // MarkRollback 标记组件开始回滚
    MarkRollback(ctx context.Context, cluster *bkev1beta1.BKECluster, componentName string, componentType string) error

    // MarkPartialSuccess 标记部分节点成功 (仅 Binary 组件)
    MarkPartialSuccess(ctx context.Context, cluster *bkev1beta1.BKECluster, componentName string, componentType string, successNodes int, failedNodes int) error
}
```

**默认实现: BKEComponentStatusUpdater**：

```go
// pkg/dagexec/bke_component_status_updater.go

type BKEComponentStatusUpdater struct {
    client client.Client
}

func (u *BKEComponentStatusUpdater) MarkPending(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    componentName string,
    componentType string,
) error {
    return u.updateComponentStatus(ctx, cluster, componentName, componentType, ComponentStatus{
        Phase:          "Installing",
        Type:           componentType,
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

func (u *BKEComponentStatusUpdater) MarkSuccess(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    componentName string,
    componentType string,
    version string,
) error {
    return u.updateComponentStatus(ctx, cluster, componentName, componentType, ComponentStatus{
        Version:        version,
        Phase:          "Installed",
        Type:           componentType,
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

func (u *BKEComponentStatusUpdater) MarkFailed(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    componentName string,
    componentType string,
    installErr error,
) error {
    existingVersion := ""
    if cluster.Status.ComponentStatuses != nil {
        if status, ok := cluster.Status.ComponentStatuses[componentName]; ok {
            existingVersion = status.Version
        }
    }

    return u.updateComponentStatus(ctx, cluster, componentName, componentType, ComponentStatus{
        Version:        existingVersion,
        Phase:          "Failed",
        Type:           componentType,
        Message:        installErr.Error(),
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    })
}

func (u *BKEComponentStatusUpdater) updateComponentStatus(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    componentName string,
    componentType string,
    status ComponentStatus,
) error {
    return retry.RetryOnConflict(retry.DefaultRetry, func() error {
        latest := &bkev1beta1.BKECluster{}
        if err := u.client.Get(ctx, types.NamespacedName{
            Namespace: cluster.Namespace,
            Name:      cluster.Name,
        }, latest); err != nil {
            return err
        }

        if latest.Status.ComponentStatuses == nil {
            latest.Status.ComponentStatuses = make(map[string]ComponentStatus)
        }

        latest.Status.ComponentStatuses[componentName] = status

        return u.client.Status().Update(ctx, latest)
    })
}
```

#### 9.5.6 各组件执行流程

**BinaryComponentExecutor 完整流程**：

```
  ┌──────────────────────────────────────┐
  │  1. 加载 ComponentVersion            │
  │  cv = cvStore.GetComponentVersion()  │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  2. 组件级幂等判断                    │
  │  VersionContext.NeedsUpgrade(name)   │
  │  → false: 整个组件跳过                │
  └──────────┬───────────────────────────┘
             │ true
             ▼
  ┌──────────────────────────────────────┐
  │  3. 获取全部节点                      │
  │  allNodes = NodeProvider.GetNodes()  │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  4. 节点级过滤                        │
  │  targetNodes = NodeFilter.Filter()   │
  │  ├─ 硬排除: Failed/Deleting/Skipped  │
  │  ├─ 角色过滤: cv.Spec.NodeFilter     │
  │  ├─ 标签过滤: cv.Spec.NodeFilter     │
  │  ├─ 预约排除                         │
  │  └─ 幂等跳过: per-node 已完成         │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
    有节点     无节点 → 跳过 (全部已完成)
        │
        ▼ (对每个目标节点)
  ┌──────────────────────────────────────┐
  │  5. 标记安装中                        │
  │  NodeStatusUpdater.MarkPending()     │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  6. 执行安装                          │
  │  BinaryInstaller.Install(opts)       │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
     成功       失败
        │         │
        ▼         ▼
  ┌──────────┐  ┌────────────────────────┐
  │MarkSuccess│ │MarkFailed              │
  │Version=   │ │Message = err.Error()   │
  │target     │ │                        │
  │Installed  │ │                        │
  └─────┬─────┘ └───────────┬────────────┘
        │                   │
        │              ┌────┴────┐
        │           FailFast  Continue
        │              │         │
        │              ▼         ▼
        │         return err  继续下一节点
        │
        ▼
   ┌──────────────────────────────────────┐
   │  7. 全部节点完成                      │
   │  ComponentStatusUpdater.MarkSuccess()│
   │  → ComponentStatuses[name]           │
   │    = { Version: target,              │
   │        Phase: "Installed" }          │
   │  return nil                          │
   └──────────────────────────────────────┘
```

**HelmComponentExecutor / YamlComponentExecutor 完整流程**：

```
  ┌──────────────────────────────────────┐
  │  1. 加载 ComponentVersion            │
  │  cv = cvStore.GetComponentVersion()  │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  2. 组件级幂等判断                    │
  │  VersionContext.NeedsUpgrade(name)   │
  │  → false: 整个组件跳过                │
  └──────────┬───────────────────────────┘
             │ true
             ▼
  ┌──────────────────────────────────────┐
  │  3. 标记组件安装中                    │
  │  ComponentStatusUpdater.MarkPending()│
  │  → ComponentStatuses[name]           │
  │    = { Phase: "Installing" }         │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  4. 执行安装                         │
  │  HelmInstaller.Install() 或          │
  │  YamlInstaller.Apply()               │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
     成功       失败
        │         │
        ▼         ▼
  ┌───────────┐ ┌────────────────────────┐
  │MarkSuccess│ │MarkFailed              │
  │Version=   │ │Message = err.Error()   │
  │target     │ │                        │
  │Installed  │ │                        │
  └───────────┘ └────────────────────────┘
```

**InlineComponentExecutor**：

与 Binary/Helm/YAML 不同，Inline 组件不记录状态（由 Phase 自身 `NeedExecute()` 判断幂等性），也不需要 NodeFilter、NodeStatusUpdater 或 ComponentStatusUpdater。

#### 9.5.7 状态转换表

**状态转换图 (节点级 - NodeComponentStatuses，仅 Binary 组件)**：

```
                              ┌─────────────┐
                              │   Pending   │
                              └──────┬──────┘
                                     │ MarkPending
                                     ▼
                    ┌────────────────────────────────┐
                    │                                │
              ┌─────▼─────┐                    ┌─────▼─────┐
              │ Installing│                    │ Upgrading │
              └─────┬─────┘                    └─────┬─────┘
                    │                                │
         ┌──────────┼──────────┐           ┌─────────┼─────────┐
         │          │          │           │         │         │
         ▼          ▼          ▼           ▼         ▼         ▼
    ┌─────────┐ ┌────────┐ ┌────────┐ ┌─────────┐ ┌────────┐ ┌────────┐
    │Installed│ │ Failed │ │Timeout │ │Installed│ │ Failed │ │Timeout │
    └─────────┘ └────────┘ └────────┘ └─────────┘ └────────┘ └────────┘
```

**状态转换图 (组件级 - ComponentStatuses，所有组件类型)**：

```
                              ┌─────────────┐
                              │   Pending   │
                              └──────┬──────┘
                                     │ MarkPending
                                     ▼
                    ┌────────────────────────────────┐
                    │                                │
              ┌─────▼─────┐                    ┌─────▼─────┐
              │ Installing│                    │ Upgrading │
              └─────┬─────┘                    └─────┬─────┘
                    │                                │
         ┌──────────┼──────────┐           ┌─────────┼─────────┐
         │          │          │           │         │         │
         ▼          ▼          ▼           ▼         ▼         ▼
    ┌─────────┐ ┌────────┐ ┌────────┐ ┌─────────┐ ┌────────┐ ┌────────┐
    │Installed│ │ Failed │ │Timeout │ │Installed│ │ Failed │ │Timeout │
    └─────────┘ └───┬────┘ └────────┘ └─────────┘ └───┬────┘ └────────┘
                    │                                 │
                    │ MarkRollback                    │ MarkRollback
                    ▼                                 ▼
              ┌─────────────┐                   ┌─────────────┐
              │ RollingBack │                   │ RollingBack │
              └──────┬──────┘                   └──────┬──────┘
                     │                                 │
              ┌──────┴──────┐                   ┌──────┴──────┐
              │             │                   │             │
              ▼             ▼                   ▼             ▼
        ┌───────────┐ ┌────────┐          ┌───────────┐ ┌────────┐
        │RolledBack │ │ Failed │          │RolledBack │ │ Failed │
        └───────────┘ └────────┘          └───────────┘ └────────┘

Binary 组件特有转换：
              ┌─────────┐
              │Upgrading│ (部分节点成功，部分失败)
              └────┬────┘
                   │ MarkPartialSuccess
                   ▼
            ┌────────────────┐
            │PartialSuccess  │
            └────────────────┘
```

**完整状态转换矩阵**：

| 当前状态 | 事件 | 新状态 | 方法 |
|---------|------|--------|------|
| **Pending** | 开始安装 | Installing | MarkPending |
| **Pending** | 开始升级 | Upgrading | MarkPending |
| **Installing** | 安装成功 | Installed | MarkSuccess |
| **Installing** | 安装失败 | Failed | MarkFailed |
| **Installing** | 超时 | Timeout | MarkTimeout |
| **Upgrading** | 升级成功 | Installed | MarkSuccess |
| **Upgrading** | 升级失败 | Failed | MarkFailed |
| **Upgrading** | 超时 | Timeout | MarkTimeout |
| **Upgrading** | 部分节点成功 | PartialSuccess | MarkPartialSuccess |
| **Failed** | 开始回滚 | RollingBack | MarkRollback |
| **Failed** | 重试 | Installing/Upgrading | MarkPending |
| **Timeout** | 开始回滚 | RollingBack | MarkRollback |
| **Timeout** | 重试 | Installing/Upgrading | MarkPending |
| **RollingBack** | 回滚成功 | RolledBack | MarkSuccess |
| **RollingBack** | 回滚失败 | Failed | MarkFailed |
| **Installed** | 目标版本变更 | Upgrading | MarkPending |
| **RolledBack** | 目标版本变更 | Upgrading | MarkPending |
| **PartialSuccess** | 重试失败节点 | Upgrading | MarkPending |
| **PartialSuccess** | 全部成功 | Installed | MarkSuccess |

**节点级状态转换 (NodeComponentStatuses，仅 Binary 组件)**：

| 当前 Phase | 事件 | 新 Phase | Version | 触发者 |
|-----------|------|---------|---------|--------|
| (不存在) | 开始安装 | `Pending` | "" | 初始状态 |
| `Pending` | 开始安装 | `Installing` | "" | NodeStatusUpdater.MarkPending |
| `Pending` | 开始升级 | `Upgrading` | 保留旧版本 | NodeStatusUpdater.MarkPending |
| `Installing` | 安装成功 | `Installed` | targetVersion | NodeStatusUpdater.MarkSuccess |
| `Installing` | 安装失败 | `Failed` | "" | NodeStatusUpdater.MarkFailed |
| `Installing` | 超时 | `Timeout` | "" | NodeStatusUpdater.MarkTimeout |
| `Upgrading` | 升级成功 | `Installed` | targetVersion | NodeStatusUpdater.MarkSuccess |
| `Upgrading` | 升级失败 | `Failed` | 保留旧版本 | NodeStatusUpdater.MarkFailed |
| `Upgrading` | 超时 | `Timeout` | 保留旧版本 | NodeStatusUpdater.MarkTimeout |
| `Failed` | 开始回滚 | `RollingBack` | 保留旧版本 | NodeStatusUpdater.MarkRollback |
| `Failed` | 重试 | `Installing`/`Upgrading` | 保留旧版本 | NodeStatusUpdater.MarkPending |
| `Timeout` | 开始回滚 | `RollingBack` | 保留旧版本 | NodeStatusUpdater.MarkRollback |
| `Timeout` | 重试 | `Installing`/`Upgrading` | 保留旧版本 | NodeStatusUpdater.MarkPending |
| `RollingBack` | 回滚成功 | `RolledBack` | 旧版本 | NodeStatusUpdater.MarkSuccess |
| `RollingBack` | 回滚失败 | `Failed` | 保留旧版本 | NodeStatusUpdater.MarkFailed |
| `Installed` | 目标版本变更 | `Upgrading` | 保留旧版本 | NodeStatusUpdater.MarkPending |
| `RolledBack` | 目标版本变更 | `Upgrading` | 保留旧版本 | NodeStatusUpdater.MarkPending |
| `Installed` | 版本相同 | (跳过) | — | NodeFilter |

**组件级状态转换 (ComponentStatuses，所有组件类型)**：

| 当前 Phase | 事件 | 新 Phase | Version | 触发者 |
|-----------|------|---------|---------|--------|
| (不存在) | 开始安装 | `Pending` | "" | 初始状态 |
| `Pending` | 开始安装 | `Installing` | "" | ComponentStatusUpdater.MarkPending |
| `Pending` | 开始升级 | `Upgrading` | 保留旧版本 | ComponentStatusUpdater.MarkPending |
| `Installing` | 安装成功 | `Installed` | targetVersion | ComponentStatusUpdater.MarkSuccess |
| `Installing` | 安装失败 | `Failed` | "" | ComponentStatusUpdater.MarkFailed |
| `Installing` | 超时 | `Timeout` | "" | ComponentStatusUpdater.MarkTimeout |
| `Upgrading` | 升级成功 | `Installed` | targetVersion | ComponentStatusUpdater.MarkSuccess |
| `Upgrading` | 升级失败 | `Failed` | 保留旧版本 | ComponentStatusUpdater.MarkFailed |
| `Upgrading` | 超时 | `Timeout` | 保留旧版本 | ComponentStatusUpdater.MarkTimeout |
| `Upgrading` | 部分节点成功 | `PartialSuccess` | 保留旧版本 | ComponentStatusUpdater.MarkPartialSuccess |
| `Failed` | 开始回滚 | `RollingBack` | 保留旧版本 | ComponentStatusUpdater.MarkRollback |
| `Failed` | 重试 | `Installing`/`Upgrading` | 保留旧版本 | ComponentStatusUpdater.MarkPending |
| `Timeout` | 开始回滚 | `RollingBack` | 保留旧版本 | ComponentStatusUpdater.MarkRollback |
| `Timeout` | 重试 | `Installing`/`Upgrading` | 保留旧版本 | ComponentStatusUpdater.MarkPending |
| `RollingBack` | 回滚成功 | `RolledBack` | 旧版本 | ComponentStatusUpdater.MarkSuccess |
| `RollingBack` | 回滚失败 | `Failed` | 保留旧版本 | ComponentStatusUpdater.MarkFailed |
| `Installed` | 目标版本变更 | `Upgrading` | 保留旧版本 | ComponentStatusUpdater.MarkPending |
| `RolledBack` | 目标版本变更 | `Upgrading` | 保留旧版本 | ComponentStatusUpdater.MarkPending |
| `PartialSuccess` | 重试失败节点 | `Upgrading` | 保留旧版本 | ComponentStatusUpdater.MarkPending |
| `PartialSuccess` | 全部成功 | `Installed` | targetVersion | ComponentStatusUpdater.MarkSuccess |
| `Installed` | 版本相同 | (跳过) | — | VersionContext |

**状态更新职责分工**：

| 组件类型 | NodeStatusUpdater | ComponentStatusUpdater | 说明 |
|---------|-------------------|----------------------|------|
| **Binary** | ✅ 每个节点独立更新 | ✅ 全部节点完成后更新 | 节点级 + 组件级双重追踪 |
| **Helm** | ❌ 不需要 | ✅ 安装开始/结束时更新 | 集群级部署，无 per-node 状态 |
| **YAML** | ❌ 不需要 | ✅ 安装开始/结束时更新 | 集群级部署，无 per-node 状态 |
| **Inline** | ❌ 不需要 | ❌ 不需要 | 由 Phase 自身管理状态 |

#### 9.5.8 幂等性设计

**各组件类型的幂等机制**：

| 组件类型 | 幂等粒度 | 判断机制 | 判断位置 |
|---------|---------|---------|---------|
| **Binary** | per-node per-component | `NodeComponentStatuses[name][ip].Version == target && Phase == "Installed"` | NodeFilter |
| **Helm** | 组件级 (集群) | `VersionContext.NeedsUpgrade(name)` | Executor |
| **YAML** | 组件级 (集群) | `VersionContext.NeedsUpgrade(name)` | Executor |
| **Inline** | 自定义 | `Phase.NeedExecute()` 自行判断 | InlineRunner |

**Binary 幂等的三种场景**：

```
场景 1: 全新安装
  NodeComponentStatuses["containerd"] = nil (无记录)
  → NodeFilter: 不跳过
  → 执行安装
  → MarkSuccess: version = "v1.7.18", phase = "Installed"

场景 2: 全部节点已安装 (组件级跳过)
  VersionContext.NeedsUpgrade("containerd") = false
  → Executor: 整个组件跳过，不进入 NodeFilter

场景 3: 部分节点已安装 (per-node 跳过)
  VersionContext.NeedsUpgrade("containerd") = true (集群级需要升级)
  NodeComponentStatuses["containerd"]["10.0.0.1"] = { Version: "v1.7.18", Phase: "Installed" }
  NodeComponentStatuses["containerd"]["10.0.0.2"] = { Version: "v1.7.15", Phase: "Installed" }
  NodeComponentStatuses["containerd"]["10.0.0.3"] = nil (未安装/中断)
  → NodeFilter: 跳过 10.0.0.1 和 10.0.0.2，仅对 10.0.0.3 执行
```

**失败重试的幂等性**：

```
场景: 节点 10.0.0.2 安装失败
  NodeComponentStatuses["containerd"]["10.0.0.2"] = { Version: "v1.7.15", Phase: "Failed" }

下次 Reconcile:
  VersionContext.NeedsUpgrade("containerd") = true
  NodeFilter:
    10.0.0.1: Phase=Installed, Version=v1.7.18 == target → 跳过
    10.0.0.2: Phase=Failed → 不跳过 (需要重试)
    10.0.0.3: 无记录 → 不跳过
  → 仅对 10.0.0.2 和 10.0.0.3 执行
```

#### 9.5.9 兼容性设计

**Feature Gate OFF (旧路径)**：

```
BKEClusterReconciler → Phase 框架 → EnsureBKEAgent / EnsureContainerdUpgrade / ...
                                       │
                                       ├─ 读取: BKENode.Status.StateCode (位标记)
                                       ├─ 写入: BKENode.Status.StateCode (位标记)
                                       └─ 写入: BKECluster.Status.*Version (集群级版本)

NodeComponentStatuses: 不写入、不读取
ComponentStatuses: 不写入、不读取
```

**完全不变**，现有行为不受影响。

**Feature Gate ON (新路径)**：

```
BKEClusterReconciler → DAG Scheduler → BinaryComponentExecutor / HelmComponentExecutor / ...
                                          │
                                          ├─ 读取: NodeComponentStatuses (per-node 幂等)
                                          ├─ 读取: BKENode.Status.StateCode (硬排除)
                                          ├─ 写入: NodeComponentStatuses (per-node 状态)
                                          └─ 写入: ComponentStatuses (组件级状态)
```

**迁移策略：Feature Gate 首次开启**：

**问题**：Feature Gate 首次开启时，`NodeComponentStatuses` 为空，但节点上已安装了组件（通过旧路径的 StateCode 位标记记录）。如果不处理，NodeFilter 会对所有节点重新安装。

**方案**：NodeFilter 的双源读取（已在 9.5.4 节实现）

```
NodeFilter.isAlreadyAtTarget():
  1. 优先读 NodeComponentStatuses (新模型)
     → 有记录: 按新模型判断
     → 无记录: 进入步骤 2

  2. 回退读 BKENode.Status.StateCode (旧模型)
     → bkeagent: NodeAgentPushedFlag → 视为已安装
     → 其他组件: 不过滤 (由组件级 VersionContext 处理)

  3. 懒初始化: 首次读取旧模型时，写入 NodeComponentStatuses
     → 后续读取走步骤 1，不再回退
```

**回滚策略：Feature Gate 从 ON 切回 OFF**：

```
Feature Gate ON → OFF:
  NodeComponentStatuses 保留在 BKECluster.Status 中 (不删除)
  旧路径不读取 NodeComponentStatuses (不受影响)
  旧路径继续读写 BKENode.Status.StateCode (行为不变)

Feature Gate OFF → ON (再次开启):
  NodeComponentStatuses 可能不是最新的 (OFF 期间旧路径更新了 StateCode 但未更新 NodeComponentStatuses)
  → NodeFilter 检测到 NodeComponentStatuses 与 StateCode 不一致时，以 StateCode 为准并重新初始化
```

**兼容性保证矩阵**：

| 场景 | Feature Gate | 状态来源 | 行为 |
|------|-------------|---------|------|
| 全新集群安装 | OFF | StateCode | 旧路径，不变 |
| 全新集群安装 | ON | NodeComponentStatuses | 新路径 |
| 已有集群 + FG OFF→ON | ON (首次) | StateCode → NodeComponentStatuses (懒初始化) | 不重复安装 |
| 已有集群 + FG ON→OFF→ON | ON (再次) | StateCode (OFF 期间更新) → NodeComponentStatuses (重新初始化) | 不重复安装 |
| 混合模式 (containerd ON, bkeagent OFF) | 部分 ON | 各组件独立判断 | containerd 走新路径，bkeagent 走旧路径 |

#### 9.5.10 设计决策总结

| 决策 | 选择 | 理由 |
|------|------|------|
| per-node 状态存储位置 | `BKECluster.Status.NodeComponentStatuses` | 1 次 Patch 更新，避免 N 次 BKENode 更新的并发冲突 |
| 组件级状态存储位置 | `BKECluster.Status.ComponentStatuses` | 所有组件类型共用，1 次 Patch 更新 |
| 状态枚举设计 | 单个 `Phase` 字段 (9 个状态) | 语义清晰，无需扩展字段，覆盖所有场景 |
| NodeFilter 归属 | 独立接口，由 Executor 持有 | 过滤逻辑因组件而异，不应内置到 Installer 或 NodeProvider |
| NodeStatusUpdater 归属 | 独立接口，由 Executor 持有 | 状态更新是编排层职责，不应下沉到安装层 |
| ComponentStatusUpdater 归属 | 独立接口，由 Executor 持有 | 所有组件类型共用，与 NodeStatusUpdater 分离避免职责混淆 |
| NodeFilterSpec 位置 | `ComponentVersionSpec` 顶层 | 安装和升级都需要节点过滤，不应放在 UpgradeStrategySpec 内 |
| 新旧状态模型兼容 | NodeFilter 双源读取 + 懒初始化 | 避免 Feature Gate 首次开启时的重复安装 |
| Helm/YAML 节点过滤 | 不通过 NodeFilter，通过 values/nodeSelector | 集群级部署，节点调度由 K8s Scheduler 处理 |
| Inline 状态记录 | 不记录 | 由 Phase 自身 `NeedExecute()` 判断幂等性 |
| Binary 状态更新 | 节点级 + 组件级双重追踪 | 节点级用于 per-node 幂等，组件级用于整体状态展示 |
| 回滚状态设计 | `RollingBack` + `RolledBack` | 区分回滚中和回滚成功，语义清晰 |
| 部分成功状态 | `PartialSuccess` (仅 Binary) | 精确反映多节点部署的部分成功场景 |
| 超时状态 | `Timeout` | 区分超时和其他失败，便于诊断和重试 |

#### 9.5.11 扩容场景增强设计

**设计思路**：节点扩容（Scale-Out）是集群生命周期中的高频操作。新节点加入时，需要依次完成 bkeagent 推送、containerd 安装、环境初始化、kubeadm join 四个阶段。上述 9.5.1-9.5.10 的设计已覆盖基本的幂等和过滤逻辑，但在以下两个边界场景存在增强空间：① bkeagent 安装完成后 HealthCheck 与 `NodeAgentReadyFlag` 的衔接；② 扩容与升级同时触发时的幂等保护。

##### 9.5.11.1 增强 1：bkeagent 就绪状态同步

**问题**：containerd 的 DAG 依赖 bkeagent（拓扑排序保证批次顺序）。bkeagent 安装完成后，`BinaryInstaller.HealthCheck` 仅验证 `systemctl is-active bkeagent`（进程存活），`NodeStatusUpdater.MarkSuccess` 仅写入 `NodeComponentStatuses`。但后续 `EnsureNodesEnv`（Inline 组件）显式检查 `BKENode.Status.StateCode` 的 `NodeAgentReadyFlag`（bit 1）来判断 bkeagent 是否可与控制器通信。两条状态链路独立，可能导致 containerd 安装时 bkeagent 进程存活但尚未注册就绪。

**增强方案**：在 `NodeStatusUpdater.MarkSuccess` 中，当组件为 `bkeagent` 时，同步设置 `BKENode.Status.StateCode` 的 `NodeAgentReadyFlag`。

```go
// BKENodeStatusUpdater.MarkSuccess 增强 (在现有实现基础上扩展)
func (u *BKENodeStatusUpdater) MarkSuccess(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
    componentName string,
    version string,
) error {
    // 1. 更新 NodeComponentStatuses (现有逻辑，不变)
    if err := u.updateNodeComponentStatus(ctx, cluster, nodeIP, componentName, NodeComponentStatus{
        Version:        version,
        Phase:          "Installed",
        LastUpdateTime: &metav1.Time{Time: time.Now()},
    }); err != nil {
        return err
    }

    // 2. 🆕 bkeagent 专属: 同步设置 NodeAgentReadyFlag
    //    确保后续 EnsureNodesEnv 能通过 StateCode 判断 bkeagent 就绪
    if componentName == "bkeagent" {
        if err := u.setNodeReadyFlag(ctx, cluster, nodeIP); err != nil {
            return fmt.Errorf("set NodeAgentReadyFlag for %s: %w", nodeIP, err)
        }
    }

    return nil
}

// setNodeReadyFlag 设置 BKENode 的 NodeAgentReadyFlag
// 复用现有 BKENode.Status.StateCode 位标记机制 (bit 1)
func (u *BKENodeStatusUpdater) setNodeReadyFlag(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    nodeIP string,
) error {
    return retry.RetryOnConflict(retry.DefaultRetry, func() error {
        bkeNode := &bkev1beta1.BKENode{}
        // 通过 nodeIP 查找对应的 BKENode
        if err := u.findBKENodeByIP(ctx, cluster, nodeIP, bkeNode); err != nil {
            return err
        }
        bkeNode.Status.StateCode |= configv1beta1.NodeAgentReadyFlag
        return u.client.Status().Update(ctx, bkeNode)
    })
}
```

**时序保证**：

```
DAG Batch 1: bkeagent (binary)
  逐节点:
    SSH 推送 → HealthCheck (systemctl is-active) → MarkSuccess
      ├─ NodeComponentStatuses["bkeagent"][nodeIP] = {Phase: Installed}
      └─ BKENode.StateCode |= NodeAgentReadyFlag  ← 🆕增强
                                                    │
DAG Batch 2: containerd (binary, 依赖 bkeagent)     │
  此时 NodeAgentReadyFlag 已设置                     │
  EnsureNodesEnv 检查通过 ←─────────────────────────┘
```

##### 9.5.11.3 增强 3：扩容+升级并发幂等保护

**问题**：当新节点加入集群时，如果恰好触发了版本升级（`desiredVersion` 变更），可能出现以下竞态：
1. bkeagent 安装 Phase 对新节点安装旧版本 → `NodeComponentStatuses["bkeagent"][newIP] = {Version: "v2.5.0", Phase: "Installed"}`
2. bkeagent 升级 Phase 的 `skipCompleted: false` 对新节点再次执行 → 升级到 v2.6.0

虽然这是**正确行为**（与现有 `EnsureAgentUpgrade` 无过滤逻辑一致），但存在一个边界问题：如果安装和升级在**同一次 Reconcile** 中触发（DAG 构建时 `VersionContext.NeedsUpgrade("bkeagent") = true`），安装 Phase 的 `MarkSuccess` 写入的版本是目标版本（v2.6.0），升级 Phase 的 `isAlreadyAtTarget` 检查 `Version == targetVersion` 会返回 true → 跳过。这是**幂等正确**的。

但 containerd 的场景不同：containerd 安装和升级共用同一个 DAG 节点（由 `VersionContext` 决定 Action=Install 或 Upgrade），不存在两个 Phase 并发的问题。**真正的风险在于**：扩容的新节点 containerd 安装失败（`Phase: "Failed"`），下次 Reconcile 重试时，如果此时 `desiredVersion` 又变了（二次升级），`isAlreadyAtTarget` 会因为 `Phase != "Installed"` 不跳过 → 重试安装的是旧目标版本而非新目标版本。

**增强方案**：在 `isAlreadyAtTarget` 中增加版本过期检测——当 `NodeComponentStatuses` 中的版本既不等于当前版本也不等于目标版本时，视为过期，不跳过。

```go
// isAlreadyAtTarget 增强 (在现有实现基础上扩展)
func (f *BKENodeFilter) isAlreadyAtTarget(
    ctx context.Context,
    node Node,
    cv *configv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) bool {
    componentName := cv.Spec.Name
    targetVersion := cv.Spec.Version

    // 优先: 从 NodeComponentStatuses 读取 (新模型)
    if execCtx.Cluster.Status.NodeComponentStatuses != nil {
        if compStatuses, ok := execCtx.Cluster.Status.NodeComponentStatuses[componentName]; ok {
            if status, ok := compStatuses[node.IP]; ok {
                // 现有逻辑: 已安装且版本匹配 → 跳过
                if status.Phase == "Installed" && status.Version == targetVersion {
                    return true
                }
                // 现有逻辑: 正在安装中 → 跳过 (避免并发)
                if status.Phase == "Installing" {
                    return true
                }
                // 🆕增强: 版本过期检测
                // 当已安装版本既不等于当前版本也不等于目标版本时，视为过期
                // 典型场景: 安装失败后目标版本变更，需要重新安装到新版本
                if status.Phase == "Failed" || status.Phase == "Timeout" {
                    // 不跳过，允许重试 (重试时会安装到最新目标版本)
                    return false
                }
                // 🆕增强: 安装成功但版本不匹配 (可能是二次升级)
                // 不跳过，允许升级到新版本
                if status.Phase == "Installed" && status.Version != targetVersion {
                    return false
                }
                return false
            }
        }
    }

    // 回退: 从 BKENode.StateCode 读取 (旧模型，向后兼容) — 现有逻辑不变
    // ...
}
```

**场景验证矩阵**：

| 场景 | NodeComponentStatuses | VersionContext | isAlreadyAtTarget | 行为 |
|------|----------------------|----------------|-------------------|------|
| 新节点首次安装 | 无记录 | NeedsUpgrade=true | false → 不跳过 | 安装到目标版本 ✅ |
| 已安装到目标版本 | `{Phase: Installed, Version: v1.7.18}` | target=v1.7.18 | true → 跳过 | 幂等跳过 ✅ |
| 安装失败后重试 | `{Phase: Failed, Version: ""}` | NeedsUpgrade=true | false → 不跳过 | 重试安装 ✅ |
| 安装失败后版本变更 | `{Phase: Failed, Version: ""}` | target 变为 v1.7.20 | false → 不跳过 | 安装到新版本 ✅ |
| 升级中目标版本又变 | `{Phase: Installed, Version: v1.7.18}` | target 变为 v1.7.20 | false → 不跳过 | 升级到 v1.7.20 ✅ |
| 扩容+升级同时 | `{Phase: Installed, Version: v2.6.0}` (安装 Phase 已写入) | target=v2.6.0 | true → 跳过 | 升级 Phase 幂等跳过 ✅ |
| 二次升级中间态 | `{Phase: Installed, Version: v1.7.18}` | target=v1.7.20 | false → 不跳过 | 升级到 v1.7.20 ✅ |

---

## 10. 完整安装流程详细设计

### 10.1 安装流程图

**设计思路**：完整安装流程从用户创建 BKECluster 开始，经过 ReleaseImage 解析、ComponentVersion 加载、DAG 构建、DAG 执行，最终完成所有组件的安装。流程中 Binary/Helm/YAML/Inline 四种类型的组件通过各自的 Executor 并行执行——Binary 组件通过 SSH 在远程节点安装二进制制品，Helm 组件通过 Helm SDK 部署 Chart，YAML 组件通过 YamlComponentExecutor 将 Kubernetes 清单直接应用到目标集群，Inline 组件通过内联执行器完成 Kubernetes 集群初始化。所有组件安装完成后通过健康检查确认安装成功。

**关键设计点**：
- **声明式安装**：通过 ReleaseImage 声明需要安装的组件列表
- **DAG 调度**：根据组件依赖关系构建 DAG，按拓扑顺序执行
- **多类型支持**：Binary、Helm、YAML、Inline
- **健康检查**：安装完成后执行 PodReady/EndpointReady 检查
- **YAML 清单应用**：YAML 类型组件通过 YamlComponentExecutor 应用 Kubernetes 清单，支持 ServerSideApply/Replace/CreateOnly 三种策略

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           完整安装流程                                           │
└─────────────────────────────────────────────────────────────────────────────────┘

                              ┌──────────────────┐
                              │  用户创建         │
                              │  BKECluster      │
                              │  desiredVersion: │
                              │  v2.6.0          │
                              └────────┬─────────┘
                                       │
                                       ▼
                    ┌──────────────────────────────────────┐
                    │  BKEClusterReconciler.Reconcile()    │
                    │  检测到新集群创建                     │
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
                      └──────────────────┬───────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  2. 加载 ComponentVersion            │
                    │  manifestStore.GetComponentVersion() │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 构建安装 DAG                      │
                    │  BuildInstallDAG(releaseImage)       │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                      ┌──────────────────────────────────────┐
                      │  DAG 结构:                           │
                      │  finalizer → ... → dryrun            │
                      │                   → agent (binary)   │
                      │                   → containerd       │
                      │                   → kubernetes-master│
                      │                     (inline)         │
                      │                   → kubernetes-worker│
                      │                     (inline)         │
                      │                   → coredns (helm)   │
                      │                   → openfuyao-core   │
                      │                     (yaml)           │
                      │                   → addon            │
                      │                   → postprocess      │
                      └──────────────────┬───────────────────┘
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
    │  Batch 1:       │      │  Batch 2:       │      │  Batch 3:       │
    │  CommonPhases   │      │  DeployPhases   │      │  PostPhases     │
    │  (前置判断)      │      │  (核心部署)     │      │  (后置处理)      │
    └────────┬────────┘      └────────┬────────┘      └────────┬────────┘
             │                        │                        │
             └────────────────────────┼────────────────────────┘
                                      │
                                      ▼
                    ┌──────────────────────────────────────┐
                    │  5. BinaryComponentExecutor          │
                    │  执行 containerd 安装                 │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  下载制品        │  │  渲染脚本       │  │  SSH 执行        │
          │  containerd     │  │  installScript  │  │  安装脚本        │
          │  tar.gz         │  │  configTemplates│  │                 │
          └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        │
                                        ▼
                    ┌──────────────────────────────────────┐
                    │  6. BinaryComponentExecutor          │
                    │  执行 bkeagent 安装                   │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  7. HelmComponentExecutor            │
                    │  执行 coredns 安装                    │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
           ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
           │  拉取 Chart      │  │  渲染 Values    │  │  Helm Install   │
           │  OCI Registry   │  │  模板变量        │  │  --atomic       │
           └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                    │                    │                    │
                    └────────────────────┼────────────────────┘
                                         │
                                         ▼
                     ┌──────────────────────────────────────┐
                     │  8. YamlComponentExecutor            │
                     │  执行 YAML 类型组件安装               │
                     └────────────────────┬─────────────────┘
                                          │
                     ┌────────────────────┼────────────────────┐
                     │                    │                    │
                     ▼                    ▼                    ▼
           ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
           │  获取清单        │  │  解析多文档      │  │  按策略应用     │
           │  ManifestStore  │  │  YAML Parser    │  │  ServerSideApply│
           │  或 URL 下载     │  │  → Unstructured│  │  → K8s Applier  │
           └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
                    │                    │                    │
                    └────────────────────┼────────────────────┘
                                         │
                                         ▼
                     ┌──────────────────────────────────────┐
                     │  9. 健康检查                          │
                     │  PodReady + EndpointReady            │
                     └────────────────────┬─────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         检查通过                检查失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  安装完成        │   │  返回错误       │
                    │  ClusterStatus  │   │  触发回滚       │
                    │  = Ready        │   │                 │
                    └─────────────────┘   └─────────────────┘
```

## 11. 完整升级流程详细设计

### 11.1 升级流程图

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
                    │  检测到版本变更                       │
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
                    │  2. 解析当前 ReleaseImage v2.5.0      │
                    │  currentReleaseImage                 │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  3. 对比版本，确定需要升级的组件       │
                    │  compareVersions(current, target)    │
                    └────────────────────┬─────────────────┘
                                         │
                                         ▼
                     ┌──────────────────────────────────────┐
                     │  需要升级的组件:                      │
                     │  ├── containerd: v1.7.15 → v1.7.18   │
                     │  ├── bkeagent: v2.5.0 → v2.6.0       │
                     │  ├── coredns: v1.10.1 → v1.11.1      │
                     │  └── openfuyao-core: v26.01 → v26.03 │
                     └────────────────────┬─────────────────┘
                                         │
                                         ▼
                    ┌──────────────────────────────────────┐
                    │  4. 构建升级 DAG                      │
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
                    │  5. DAG Scheduler 执行 (按拓扑批次)   │
                    └────────────────────┬─────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
          ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
          │  Batch 1:       │  │  Batch 2:       │  │  Batch 3:       │
          │  provider       │  │  agent (binary) │  │  containerd     │
          │                 │  │  逐节点滚动升级  │  │  (binary)       │
          │                 │  │                 │  │  逐节点滚动升级  │
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
                     │  最终健康检查                         │
                     └───────────────────┬──────────────────┘
                                         │
                              ┌──────────┴──────────┐
                              │                     │
                         升级成功                升级失败
                              │                     │
                              ▼                     ▼
                    ┌─────────────────┐   ┌─────────────────┐
                    │  升级完成        │   │  根据策略处理    │
                    │  ClusterStatus  │   │  FailFast/      │
                    │  = Ready        │   │  Continue/      │
                    │                 │   │  Rollback       │
                    └─────────────────┘   └─────────────────┘
```

## 12. 迁移策略详细设计

### 12.1 迁移流程图

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
                    │  新路径          │ │  旧路径         │
                    │  DAG +          │ │  硬编码 Phase   │
                    │  BinaryInstaller│ │  执行           │
                    └────────┬────────┘ └────────┬────────┘
                             │                   │
                             └─────────┬─────────┘
                                       │
                                       ▼
                     ┌──────────────────────────────────────┐
                     │  兼容层处理                           │
                     │  executeContainerdUpgrade()          │
                     │  {                                   │
                     │    if BinaryComponentEnabled(cluster)│
                     │      return ctrl.Result{}, nil       │
                     │      // DAG binary 节点处理升级       │
                     │    }                                 │
                     │    return rolloutContainerd()        │
                     │    // 旧路径: reset+redeploy         │
                     │  }                                   │
                     └──────────────────────────────────────┘
```

### 12.2 Feature Gate 定义

**设计思路 - 复用现有 `pkg/featuregate` 注解/flag 模式**：

现有 `pkg/featuregate/features.go` 并非 Kubernetes 标准 `featuregate.MutableFeatureGate` 注册表，而是"注解 + 全局 flag"模式：
- `DeclarativeUpgradeEnabled(obj client.Object) bool`：全局 `config.DeclarativeUpgrade` 为 true **或** 对象带 `DeclarativeUpgradeAnnotationKey: "true"` 注解时启用。
- `UpgradeReady(obj client.Object) (string, bool)`：读取 `CVOUpgradeReady` 注解。

本设计沿用同一模式新增 Binary/Helm 开关，**不**引入 `featuregate.Enabled(string)` 这种与现有包不符的 API（原设计此处不符）。

```go
// pkg/featuregate/features.go 扩展

const (
    // BinaryComponentAnnotationKey 控制是否启用 Binary 组件 (BinaryInstaller) 路径
    // 注解值为 "true" 时启用；未设置时回退到全局 flag
    BinaryComponentAnnotationKey = "cvo.openfuyao.cn/binary-component"

    // HelmComponentAnnotationKey 控制是否启用 Helm 组件 (HelmInstaller) 路径
    HelmComponentAnnotationKey = "cvo.openfuyao.cn/helm-component"
)

// BinaryComponentEnabled 判断是否启用 Binary 组件路径
// 优先级: 对象注解 "true" > 全局 config.BinaryComponentSupport flag > false
// 与现有 DeclarativeUpgradeEnabled 模式一致
func BinaryComponentEnabled(obj client.Object) bool {
    if annotations.Has(obj, BinaryComponentAnnotationKey) {
        return annotations.Get(obj, BinaryComponentAnnotationKey) == "true"
    }
    return config.BinaryComponentSupport // 全局 flag (utils/capbke/config)
}

// HelmComponentEnabled 判断是否启用 Helm 组件路径
func HelmComponentEnabled(obj client.Object) bool {
    if annotations.Has(obj, HelmComponentAnnotationKey) {
        return annotations.Get(obj, HelmComponentAnnotationKey) == "true"
    }
    return config.HelmComponentSupport
}
```

**调用方式对齐**：原设计中 `featuregate.Enabled(featuregate.BinaryComponentSupport)` 改为 `featuregate.BinaryComponentEnabled(cluster)`（见 9.1.4 `buildSchedulerConfig`、12.3.5 `getK8sEnvInitScope`）。

**全局 flag 注册**：在 `utils/capbke/config` 中新增 `BinaryComponentSupport`/`HelmComponentSupport` bool 变量，与现有 `DeclarativeUpgrade` 一致，通过控制器启动参数注入。

### 12.2a Selector 类型迁移：容器运行时互斥选择

**设计思路 — containerd 从直接引用改为通过 selector 间接引用**：

现有代码中容器运行时选择硬编码在 `init.go:789-797`（`downloadContainerRuntime` switch CRI）。KEP-6 引入 `selector` 类型后，ReleaseImage 不再直接引用 `containerd/v1.7.18`，而是引用 `container-runtime/v1.0.0`（type=selector）。DAG 构建期根据 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 自动展开为 containerd 或 docker + cri-dockerd。

| Feature Gate 状态 | ReleaseImage 引用 | 运行时选择路径 | 说明 |
|-------------------|-------------------|--------------|------|
| OFF | `containerd/v1.7.18`（直接引用） | `init.go:789-797` switch CRI | 旧路径，仅支持 containerd |
| ON | `container-runtime/v1.0.0`（selector 引用） | DAG 构建器评估 condition | 新路径，支持 containerd + docker 互斥选择 |

**Feature Gate ON 时的 ReleaseImage 变化**：

```yaml
# 旧 ReleaseImage (Feature Gate OFF)
spec:
  install:
    components:
      - name: containerd          # ← 直接引用 containerd
        version: v1.7.18

# 新 ReleaseImage (Feature Gate ON)
spec:
  install:
    components:
      - name: container-runtime   # ← 引用 selector, DAG 展开为 containerd 或 docker
        version: v1.0.0
```

**Selector 展开规则**：
- `BKECluster.Spec.Cluster.ContainerRuntime.CRI == "containerd"` → 展开为 `containerd/v1.7.18`
- `BKECluster.Spec.Cluster.ContainerRuntime.CRI == "docker"` → 展开为 `docker/v26.0.0` + `cri-dockerd/v0.3.9`（依赖关系：docker → cri-dockerd）

**与现有代码的兼容**：
- Feature Gate OFF 时 ReleaseImage 仍引用 `containerd/v1.7.18`，走旧路径（`init.go:789-797`），行为不变
- Feature Gate ON 时 ReleaseImage 引用 `container-runtime/v1.0.0`，DAG 构建器展开，跳过 `init.go` 的运行时选择逻辑
- `EnsureNodesEnv` 的 scope 变更（移除 `runtime`）仅在 Feature Gate ON 时生效，对 docker 场景同样适用

**bke-manifests 新增文件**：

```
bke-manifests/
├── container-runtime/v1.0.0/component.yaml   ← type: selector (新增)
├── containerd/v1.7.18/component.yaml         ← type: binary (已有)
├── docker/v26.0.0/component.yaml             ← type: binary (新增)
├── cri-dockerd/v0.3.9/component.yaml         ← type: binary (新增)
├── bkeagent/v2.6.0/component.yaml            ← type: binary (已有)
└── ...
```

### 12.3 containerd 重构详细设计

#### 12.3.1 当前 Phase 逻辑分析

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

#### 12.3.2 ComponentVersion YAML 完整定义

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
        installPath: "/"  # 解压到根目录, installScript 负责具体安装细节

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

      # --- hosts.toml: 主仓库 (在线/离线均生成) ---
      # 配置私有镜像仓库访问, server 指向自身
      - name: hosts.toml
        path: "/etc/containerd/certs.d/{{imageRegistry}}/hosts.toml"
        mode: "0644"
        owner: "root:root"
        content: |
          server = "https://{{imageRegistry}}"
          [host."https://{{imageRegistry}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

      # --- hosts.toml: 离线重定向 (仅离线模式生成) ---
      # 离线模式: 公共仓库请求重定向到私有仓库
      # condition: "{{.isOffline}}" → 在线时跳过, 离线时生成
      - name: docker.io-hosts.toml
        path: "/etc/containerd/certs.d/docker.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://docker.io"
          [host."https://{{imageRegistry}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

      - name: registry.k8s.io-hosts.toml
        path: "/etc/containerd/certs.d/registry.k8s.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://registry.k8s.io"
          [host."https://{{imageRegistry}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

      - name: k8s.gcr.io-hosts.toml
        path: "/etc/containerd/certs.d/k8s.gcr.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://k8s.gcr.io"
          [host."https://{{imageRegistry}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

      - name: gcr.io-hosts.toml
        path: "/etc/containerd/certs.d/gcr.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://gcr.io"
          [host."https://{{imageRegistry}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

      - name: quay.io-hosts.toml
        path: "/etc/containerd/certs.d/quay.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://quay.io"
          [host."https://{{imageRegistry}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

      - name: ghcr.io-hosts.toml
        path: "/etc/containerd/certs.d/ghcr.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://ghcr.io"
          [host."https://{{imageRegistry}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

    installScript: |
      #!/bin/bash
      set -e
      # 集群: {{clusterName}}, 节点: {{nodeIP}} ({{nodeRole}})
      # 架构: {{arch}}, 版本: {{componentVersion}}, 操作: {{action}}

      # 1. 环境检查 (OS 自检测, 非模板变量)
      if [ -f /etc/redhat-release ]; then
        yum install -y libseccomp || true
      elif [ -f /etc/os-release ] && grep -q ubuntu /etc/os-release; then
        apt-get update && apt-get install -y libseccomp2 || true
      fi

      # 2. 停止旧服务
      systemctl stop containerd || true

      # 3. 备份旧版本 (仅升级时)
      {{if .isUpgrade}}
      cp /usr/bin/containerd /usr/bin/containerd.bak.$(date +%Y%m%d%H%M%S) || true
      {{end}}

      # 4. 解压并安装新二进制 (tar.gz 包含 containerd, containerd-shim-runc-v2, ctr)
      tar -xzf {{artifact.containerd.path}} -C /
      chmod +x /usr/bin/containerd

      # 5. 安装配置文件和服务 (由 ConfigRenderer 自动上传)
      # config.toml → /etc/containerd/config.toml
      # containerd.service → /etc/systemd/system/containerd.service

      # 6. 启动并验证
      systemctl daemon-reload
      systemctl enable containerd
      systemctl start containerd
      /usr/bin/containerd --version

    uninstallScript: |
      #!/bin/bash
      systemctl stop containerd || true
      systemctl disable containerd || true
      rm -f /usr/bin/containerd /usr/bin/containerd-shim-runc-v2 /usr/bin/containerd-shim-shimless-v2
      rm -f /usr/bin/containerd-stress /usr/bin/ctr /usr/bin/containerd-shim
      rm -f /usr/local/sbin/runc /usr/bin/crictl /usr/bin/nerdctl
      rm -f /usr/lib/systemd/system/containerd.service
      rm -f /etc/crictl.yaml
      rm -rf /etc/containerd/
      systemctl daemon-reload

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

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
        /usr/bin/containerd --version | grep -q "{{componentVersion}}"
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

  # 节点过滤策略 (安装和升级共用)
  nodeFilter:
    # containerd 安装到所有节点 (角色过滤为空 = 所有角色)
    roles: []
    # 跳过已完成安装的节点 (per-node 幂等)
    skipCompleted: true
    # 排除预约添加的节点
    excludeAppointment: true
    # 示例: 如果只想安装到特定节点池，可添加标签过滤
    # matchLabels:
    #   node-pool: compute
```

#### 12.3.2.1 containerd 配置模板 (含 forEach hosts.toml)

**设计思路**：containerd 组件的 `configTemplates` 包含两类配置文件：静态文件（config.toml、containerd.service，各一个）和动态文件（hosts.toml，按 registry 数量生成多个）。动态文件通过 `forEach` 机制展开，迭代源为 `Config.Cluster.ContainerRuntime.Registry`。在线场景（ContainerdConfig CR）和离线场景（Legacy repo + insecureRegistries）由 BinaryInstaller 统一转换为相同的 `map[string]interface{}` 结构，模板无感知。

**在线场景 registry 数据**（来自 ContainerdConfig CR → RegistryConfig.Configs）：

```yaml
# Config.Cluster.ContainerRuntime.Registry 的内容
harbor.example.com:
  host: "harbor.example.com"
  capabilities: ["pull", "resolve"]
  skipVerify: false
  plainHTTP: false
  tls:
    caFile: "/etc/ssl/ca.crt"
    certFile: "/etc/ssl/client.crt"
    keyFile: "/etc/ssl/client.key"
  auth:
    auth: "dXNlcjpwYXNz"
docker.io:
  host: "mirror.internal"
  capabilities: ["pull", "resolve"]
  skipVerify: true
  plainHTTP: false
```

→ 生成 2 个 `hosts.toml`：
- `/etc/containerd/certs.d/harbor.example.com/hosts.toml`
- `/etc/containerd/certs.d/docker.io/hosts.toml`

**离线场景 registry 数据**（来自 BKECluster.Spec.ClusterConfig.Cluster.ImageRepo + InsecureRegistries）：

```yaml
# Config.Cluster.ContainerRuntime.Registry 的内容
cr.openfuyao.cn:
  host: "cr.openfuyao.cn"
  capabilities: ["pull", "resolve", "push"]
  skipVerify: true
  plainHTTP: false
docker.io:
  host: "docker.io"
  capabilities: ["pull", "resolve"]
  skipVerify: true
  plainHTTP: true
ghcr.io:
  host: "ghcr.io"
  capabilities: ["pull", "resolve"]
  skipVerify: true
  plainHTTP: true
```

→ 生成 3 个 `hosts.toml`：
- `/etc/containerd/certs.d/cr.openfuyao.cn/hosts.toml`
- `/etc/containerd/certs.d/docker.io/hosts.toml`
- `/etc/containerd/certs.d/ghcr.io/hosts.toml`

**完整 configTemplates 定义**（在线/离线共用同一份 ComponentVersion YAML）：

```yaml
configTemplates:
  # --- 静态: config.toml (单个文件) ---
  - name: config.toml
    path: "/etc/containerd/config.toml"
    mode: "0644"
    content: |
      version = 2
      root = "{{cd "containerd" "root"}}"
      state = "{{cd "containerd" "state"}}"
      [plugins]
        [plugins."io.containerd.grpc.v1.cri"]
          sandbox_image = "{{cd "containerd" "sandboxImage"}}"
          [plugins."io.containerd.grpc.v1.cri".registry]
            config_path = "{{cd "containerd" "registryConfigPath"}}"
      {{- if cd "containerd" "metricsAddress"}}
      [metrics]
        address = "{{cd "containerd" "metricsAddress"}}"
      {{- end}}

  # --- 静态: containerd.service (单个文件) ---
  - name: containerd.service
    path: "/etc/systemd/system/containerd.service"
    mode: "0644"
    content: |
      [Unit]
      Description=containerd container runtime
      After=network.target
      [Service]
      ExecStart=/usr/local/bin/containerd
      Restart=always
      [Install]
      WantedBy=multi-user.target

  # --- 动态: hosts.toml (forEach 展开, 每个 registry 一个文件) ---
  - name: hosts.toml
    forEach: "Config.Cluster.ContainerRuntime.Registry"
    pathTemplate: "/etc/containerd/certs.d/{{.Key}}/hosts.toml"
    mode: "0644"
    content: |
      server = "https://{{.Key}}"

      [host."https://{{index .Value "host"}}"]
        capabilities = {{index .Value "capabilities" | toJson}}
        skip_verify = {{index .Value "skipVerify"}}
        {{- if index .Value "plainHTTP"}}
        plain_http = true
        {{- end}}
        {{- if $tls := index .Value "tls"}}
        {{- if index $tls "caFile"}}
        ca = "{{index $tls "caFile"}}"
        {{- end}}
        {{- if index $tls "certFile"}}
        client = [["{{index $tls "certFile"}}", "{{index $tls "keyFile"}}"]]
        {{- end}}
        {{- end}}
        {{- if $auth := index .Value "auth"}}
        {{- if index $auth "auth"}}
        [host."https://{{index .Value "host"}}".header]
          authorization = ["Basic {{index $auth "auth"}}"]
        {{- end}}
        {{- end}}
```

**字段映射表（旧代码 → 新 Config）**：

| 旧代码（bkeagent 内部） | 新 Config 字段 | 数据来源 |
|------------------------|----------------------|---------|
| `runtimeParam["repo"]` | `Config.Cluster.ContainerRuntime.Registry[repo].Host` | ContainerdConfig CR 或 BKECluster.ImageRepo |
| `runtimeParam["sandbox"]` | `Config.Cluster.ContainerRuntime.SandboxImage` | ContainerdConfig CR.Main.SandboxImage |
| `runtimeParam["dataRoot"]` | `Config.Cluster.ContainerRuntime.Root` | ContainerdConfig CR.Main.Root |
| `runtimeParam["insecureRegistries"]` | `Config.Cluster.ContainerRuntime.Registry[*].SkipVerify=true` | ContainerdConfig CR.Registry.Configs |
| `runtimeParam["containerdConfig"]` | 整个 `Config.Cluster.ContainerRuntime` | BKECluster.Spec.ClusterConfig.Cluster.ContainerdConfigRef |
| `createHostsTOML()` 循环 | `forEach: "Config.Cluster.ContainerRuntime.Registry"` | ConfigRenderer 展开 |
| `generateHostsToml()` → `GenerateMultipleHostsTOML()` | `forEach: "Config.Cluster.ContainerRuntime.Registry"` | ConfigRenderer 展开 |

**等价性验证点**：

| 验证项 | 旧路径 | 新路径 (forEach) | 验证方法 |
|--------|--------|-----------------|---------|
| hosts.toml 数量 | `len(registries)` 个 | `len(Config.Cluster.ContainerRuntime.Registry)` 个 | 对比文件数量 |
| hosts.toml 路径 | `/etc/containerd/certs.d/<reg>/hosts.toml` | `pathTemplate` 渲染结果 | 对比文件路径 |
| hosts.toml 内容 | `hosts_toml.go` 生成 | `configTemplates[*].content` 渲染 | `diff` 对比两份输出 |
| 在线/离线一致性 | 两套独立代码路径 | 同一份 YAML，数据注入不同 | 相同 registry 输入，输出文件一致 |

#### 12.3.3 字段映射表

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

#### 12.3.4 行为等价性验证点

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

#### 12.3.5 EnsureNodesEnv 重构设计

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
// 复用 pkg/featuregate.BinaryComponentEnabled(cluster) 注解/flag 模式 (见 12.2)
func (e *ENV) getK8sEnvInitScope() string {
    scopes := []string{"time", "hosts", "dns", "kernel", "firewall", "selinux", "swap", "httpRepo"}
    if !featuregate.BinaryComponentEnabled(e.bkeCluster) {
        scopes = append(scopes, "runtime") // 旧路径: bkeagent 内置命令安装 containerd
    }
    scopes = append(scopes, "iptables", "registry", "extra")
    return "scope=" + strings.Join(scopes, ",")
}

// getResetScope 动态构建 Reset 的 scope
func (e *ENV) getResetScope() string {
    if e.DeepRestore {
        if featuregate.BinaryComponentEnabled(e.bkeCluster) {
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
// 复用 featuregate.BinaryComponentEnabled(bkeCluster) (注解/flag 模式)
func (e *EnsureContainerdUpgrade) Execute() (ctrl.Result, error) {
    if featuregate.BinaryComponentEnabled(e.Ctx.BKECluster) {
        // 新路径: 不执行任何操作，containerd 升级由 DAG 中的 binary 节点处理
        return ctrl.Result{}, nil
    }
    // 旧路径: resetContainerd + redeployContainerd
    return e.rolloutContainerd()
}
```

### 12.4 bkeagent 重构详细设计

#### 12.4.1 当前 Phase 逻辑分析

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

#### 12.4.2 ComponentVersion YAML 完整定义

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

    configTemplates:
      # 节点标识文件
      - name: node
        path: "/etc/openFuyao/bkeagent/node"
        mode: "0644"
        owner: "root:root"
        content: "{{nodeHostname}}"

      # TLS 证书（从 Secret 获取）
      - name: trust-chain.crt
        path: "/etc/openFuyao/certs/trust-chain.crt"
        mode: "0644"
        owner: "root:root"
        secretRef:
          name: bkeagent-tls
          namespace: "{{clusterNamespace}}"
          key: trust-chain.crt

      - name: global-ca.crt
        path: "/etc/openFuyao/certs/global-ca.crt"
        mode: "0644"
        owner: "root:root"
        secretRef:
          name: bkeagent-tls
          namespace: "{{clusterNamespace}}"
          key: global-ca.crt

      - name: global-ca.key
        path: "/etc/openFuyao/certs/global-ca.key"
        mode: "0600"
        owner: "root:root"
        secretRef:
          name: bkeagent-tls
          namespace: "{{clusterNamespace}}"
          key: global-ca.key

      # Kubeconfig（管理集群 admin kubeconfig）
      #
      # 设计思路 — 为什么使用 secretRef 而非 kubeconfigTemplate:
      #
      # 当前代码 loadLocalKubeConfig() → GetLocalKubeConfig() 从管理集群 Secret
      # kube-system/localkubeconfig 中直接读取 kubeconfig 内容。该 Secret 由 bke init
      # 预置，内容为嵌入式证书数据（certificate-authority-data / client-certificate-data /
      # client-key-data），即完整的 admin kubeconfig 文件。
      #
      # kubeconfigTemplate 模式生成的是文件路径引用式 kubeconfig:
      #   certificate-authority: /etc/openFuyao/certs/global-ca.crt
      #   client-certificate: /etc/openFuyao/certs/bkeagent-client.crt
      # 这与当前行为不等价 — bkeagent 启动时不依赖本地证书文件，kubeconfig 自身包含完整证书数据。
      #
      # secretRef 模式直接读取 Secret 内容（嵌入式证书数据），与当前代码完全等价:
      #   GetLocalKubeConfig() → secret.Data["config"] → echo > /etc/openFuyao/bkeagent/config
      #   ConfigRenderer.renderSecretTemplate() → secret.Data["config"] → ssh.Upload() → 同一路径
      #
      # 注意: kubeconfigTemplate 功能本身保留在 CRD 和 ConfigRenderer 中（见 3.2/4.5），
      # 供其他需要动态生成 kubeconfig 的组件使用。bkeagent 不使用此模式。
      - name: kubeconfig
        path: "/etc/openFuyao/bkeagent/config"
        mode: "0600"
        owner: "root:root"
        secretRef:
          name: localkubeconfig          # 管理集群 kube-system/localkubeconfig Secret (由 bke init 预置)
          namespace: kube-system         # 固定值，管理集群系统命名空间
          key: config                    # Secret data key，包含完整 admin kubeconfig

      # CSR 配置文件（17 个，此处列出关键的 3 个作为示例）
      - name: cluster-ca-policy.json
        path: "/etc/openFuyao/certs/cert_config/cluster-ca-policy.json"
        mode: "0644"
        owner: "root:root"
        content: |
          {
            "signing": {
              "default": {
                "expiry": "87600h"
              },
              "profiles": {
                "ca": {
                  "usages": ["signing", "key encipherment", "cert sign", "crl sign"],
                  "expiry": "87600h"
                }
              }
            }
          }

      - name: cluster-ca-csr.json
        path: "/etc/openFuyao/certs/cert_config/cluster-ca-csr.json"
        mode: "0644"
        owner: "root:root"
        content: |
          {
            "CN": "kubernetes",
            "key": {
              "algo": "rsa",
              "size": 2048
            },
            "names": [
              {
                "C": "CN",
                "L": "Beijing",
                "O": "kubernetes",
                "OU": "bke"
              }
            ]
          }

      - name: sign-policy.json
        path: "/etc/openFuyao/certs/cert_config/sign-policy.json"
        mode: "0644"
        owner: "root:root"
        content: |
          {
            "signing": {
              "default": {
                "expiry": "8760h"
              },
              "profiles": {
                "server": {
                  "usages": ["signing", "key encipherment", "server auth"],
                  "expiry": "8760h"
                },
                "client": {
                  "usages": ["signing", "key encipherment", "client auth"],
                  "expiry": "8760h"
                }
              }
            }
          }

      # 注：实际部署包含 17 个 CSR 配置文件，此处为简化示例
      # 完整列表：cluster-ca-policy.json, cluster-ca-csr.json, sign-policy.json,
      # apiserver-csr.json, apiserver-etcd-client-csr.json, front-proxy-client-csr.json,
      # apiserver-kubelet-client-csr.json, front-proxy-ca-csr.json, etcd-ca-csr.json,
      # etcd-server-csr.json, etcd-healthcheck-client-csr.json, etcd-peer-csr.json,
      # admin-kubeconfig-csr.json, kubelet-kubeconfig-csr.json, controller-manager-csr.json,
      # scheduler-csr.json, kube-proxy-csr.json

    installScript: |
      #!/bin/bash
      set -e
      # 集群: {{clusterName}}, 节点: {{nodeIP}} ({{nodeRole}})
      # 版本: {{componentVersion}}, 操作: {{action}}

      # 1. 创建目录结构
      mkdir -p /etc/openFuyao/bkeagent
      mkdir -p /etc/openFuyao/bkeagent/bin
      mkdir -p /etc/openFuyao/bkeagent/scripts
      mkdir -p /etc/openFuyao/certs
      mkdir -p /etc/openFuyao/certs/cert_config
      mkdir -p /var/log/openFuyao

      # 2. 停止旧服务
      systemctl stop bkeagent || true

      # 3. 备份旧版本 (仅升级时)
      {{if .isUpgrade}}
      cp /usr/local/bin/bkeagent /usr/local/bin/bkeagent.bak.$(date +%s)
      # 保留最近 3 个备份
      ls -t /usr/local/bin/bkeagent.bak.* 2>/dev/null | tail -n +4 | xargs rm -f 2>/dev/null || true
      {{end}}

      # 4. 安装新二进制
      install -m 0755 {{artifact.bkeagent.path}} /usr/local/bin/bkeagent

      # 5. 配置文件由 ConfigRenderer 自动上传到对应路径
      # - node → /etc/openFuyao/bkeagent/node
      # - TLS 证书 → /etc/openFuyao/certs/
      # - Kubeconfig → /etc/openFuyao/bkeagent/config
      # - CSR 配置 → /etc/openFuyao/certs/cert_config/

      # 6. 安装 systemd service
      # 注：实际部署中 service 文件从 HTTP 仓库下载
      # 此处为简化示例，展示 service 文件内容
      cat > /etc/systemd/system/bkeagent.service << 'EOF'
      [Unit]
      Description=BKE Agent
      After=network.target

      [Service]
      Environment="DEBUG=true"
      ExecStart=/usr/local/bin/bkeagent --kubeconfig=/etc/openFuyao/bkeagent/config --health-port= --ntpserver=
      KillMode=process
      RestartSec=5
      Restart=on-failure
      SuccessExitStatus=0

      [Install]
      WantedBy=multi-user.target
      EOF

      # 7. 设置权限
      chmod 755 /usr/local/bin/
      chmod 755 /etc/systemd/system/

      # 8. 启动并验证
      systemctl daemon-reload
      systemctl enable bkeagent
      systemctl restart bkeagent
      sleep 2
      systemctl is-active bkeagent

    uninstallScript: |
      #!/bin/bash
      systemctl stop bkeagent || true
      systemctl disable bkeagent || true
      
      # 删除二进制和备份
      rm -f /usr/local/bin/bkeagent
      rm -f /usr/local/bin/bkeagent.bak.*
      
      # 删除服务文件
      rm -f /etc/systemd/system/bkeagent.service
      
      # 删除工作目录
      rm -rf /etc/openFuyao/bkeagent
      
      # 删除日志
      rm -f /var/log/openFuyao/bkeagent.log
      rm -f /var/log/openFuyao/bkeagent-update.log
      
      # 注：证书目录 /etc/openFuyao/certs 默认保留
      # 如需完全清理，取消注释以下行：
      # rm -rf /etc/openFuyao/certs
      
      systemctl daemon-reload

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

    defaultConfigPath: "/etc/openFuyao/bkeagent"
    defaultLogPath: "/var/log/openFuyao"

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

  # 节点过滤策略 (安装和升级共用)
  nodeFilter:
    # bkeagent 安装到所有节点 (角色过滤为空 = 所有角色)
    roles: []
    # 首次安装: 跳过已推送节点 (等价于 !NodeAgentPushedFlag)
    # 升级: 需要设为 false (所有节点都执行，不过滤)
    # 注意: 安装和升级使用同一个 ComponentVersion，因此 skipCompleted 的语义需要
    # 根据 VersionContext 动态判断:
    #   - HasCurrent=false (首次安装): skipCompleted 生效，跳过已推送节点
    #   - HasCurrent=true (升级): skipCompleted 被忽略，所有节点都执行
    skipCompleted: true
    # 排除预约添加的节点
    excludeAppointment: true
```

#### 12.4.3 字段映射表

| 旧硬编码逻辑 | 新 ComponentVersion 字段 | 说明 |
|-------------|------------------------|------|
| `Spec.BKEAgentVersion` | `spec.version` | 版本号 |
| 下载 URL 字符串拼接 | `binary.artifacts[].url`（含 `{{version}}/{{arch}}` 模板） | 制品地址声明式化 |
| `checksum` 硬编码常量 | `binary.artifacts[].checksum` | 校验和声明式化 |
| bkeagent.conf Go 代码拼接 | `binary.configTemplates[0].content` | 配置模板声明式化 |
| TLS 证书从 Secret 获取（硬编码 Secret 名） | `binary.configTemplates[1].secretRef` | Secret 引用声明式化 |
| kubeconfig 从 Secret 获取（`GetLocalKubeConfig()` 读取 `kube-system/localkubeconfig`） | `binary.configTemplates[4].secretRef`（引用管理集群 `kube-system/localkubeconfig` Secret） | kubeconfig 获取声明式化，与当前代码等价（嵌入式证书数据） |
| SSH 命令序列硬编码 | `binary.installScript` | 安装脚本声明式化 |
| `arch` 硬编码 | `{{arch}}` 模板变量 | 架构适配模板化 |
| 版本比较逻辑 | `VersionContext.NeedsUpgrade("bkeagent")` | 版本决策声明式化 |
| 固定逐节点滚动 | `upgradeStrategy.mode: Batch` | 升级策略改为分批 |
| 无失败策略 | `upgradeStrategy.failurePolicy: Continue` | 失败策略可配置 |
| 不支持卸载 | `binary.uninstallScript` | 卸载脚本声明式化 |

#### 12.4.4 行为等价性验证点

| 验证项 | 旧路径 (EnsureAgentUpgrade) | 新路径 (BinaryInstaller) | 验证方法 |
|--------|----------------------------|------------------------|---------|
| 二进制文件路径 | `/usr/local/bin/bkeagent` | `artifacts[0].installPath` = `/usr/local/bin` (per-artifact) | 检查远程节点文件路径一致 |
| bkeagent.conf 内容 | Go 代码拼接 | Go template 渲染 | `diff` 对比两份输出 |
| TLS 证书来源 | Secret `bkeagent-tls` 硬编码 | `configTemplates[1].secretRef.name` = `bkeagent-tls` | 验证证书内容一致 |
| kubeconfig 内容 | `GetLocalKubeConfig()` 读取 Secret `kube-system/localkubeconfig` | `secretRef` 读取同一 Secret（`configRenderer.renderSecretTemplate()` → `secret.Data["config"]`） | `diff` 对比两份输出（内容一致，均为嵌入式证书数据） |
| 安装执行顺序 | 停止→备份→安装→配置→启动 | installScript: 停止→备份→安装→配置→启动 | 对比 SSH 执行日志 |
| 版本比较逻辑 | `Status.BKEAgentVersion != Spec.BKEAgentVersion` | `VersionContext.NeedsUpgrade("bkeagent")` | 相同版本输入，决策结果一致 |
| 升级策略差异 | 固定逐节点滚动 | `Batch (batchSize=2)` | 3 节点集群验证分批执行（2+1） |

> **设计思路 - bkeagent 升级策略从 Rolling 改为 Batch 的原因**：bkeagent 是节点上的代理进程，短暂中断不影响集群可用性（Agent 重启期间节点上已有 Pod 继续运行）。使用 Batch 模式（batchSize=2）比 Rolling 更快完成升级，且每批结束后可检查剩余节点 Agent 状态，兼顾效率与安全性。containerd 是容器运行时，中断会导致节点上所有 Pod 重启，必须使用 Rolling 逐节点升级确保服务连续性。

### 12.5 BKEAgentSwitch 独立组件设计

**设计思路 — 为什么需要独立组件**：

bkeagent 的监听切换发生在集群安装完成后（cluster-api 部署后），而 bkeagent 的安装发生在集群安装前。两者在时间线上分离，不应耦合在同一个组件中：

```
时间线：
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│ bkeagent    │ →  │ 集群安装    │ →  │ cluster-api │ →  │ bkeagent    │
│ 安装        │    │ (master/    │    │ 部署        │    │ switch      │
│ (管理集群)  │    │  worker)    │    │             │    │ (目标集群)  │
└─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘
     ↑                                                        ↑
  监听管理集群                                            切换到目标集群
```

#### 12.5.1 功能说明

bkeagent-switch 组件负责切换 bkeagent 的监听目标集群：

| 组件 | 作用 |
|------|------|
| **注解** | `bke.bocloud.com/bkeagent-listener` 标记目标（`current` / `bkecluster`） |
| **Condition** | `SwitchBKEAgentCondition` 标记切换完成 |
| **切换内容** | 更新 `/etc/openFuyao/bkeagent/config`（kubeconfig）、`/etc/openFuyao/bkeagent/node`（hostname）、`/etc/openFuyao/bkeagent/cluster`（clusterName） |

**触发场景**：

```
EnsureAddonDeploy 部署 cluster-api addon
    ↓
markBKEAgentSwitchPending() 设置注解 "bkecluster"
    ↓
DAG 调度 bkeagent-switch 组件
    ↓
BinaryInstaller.Install()
    ↓
SSH 上传配置文件 + 重启 bkeagent
    ↓
标记 SwitchBKEAgentCondition = True
```

#### 12.5.2 ComponentVersion YAML 定义

```yaml
# bke-manifests/bkeagent-switch/v2.6.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-switch-v2.6.0
spec:
  name: bkeagent-switch
  type: binary
  version: v2.6.0
  
  binary:
    # 无需下载制品（bkeagent 已安装）
    artifacts: []
    
    # 配置文件模板
    configTemplates:
      # 目标集群 kubeconfig（从 Secret 获取）
      - name: kubeconfig
        path: "/etc/openFuyao/bkeagent/config"
        mode: "0600"
        owner: "root:root"
        secretRef:
          name: "{{clusterName}}-kubeconfig"
          namespace: "{{clusterNamespace}}"
          key: value
      
      # 节点标识
      - name: node
        path: "/etc/openFuyao/bkeagent/node"
        mode: "0644"
        owner: "root:root"
        content: "{{nodeHostname}}"
      
      # 集群标识
      - name: cluster
        path: "/etc/openFuyao/bkeagent/cluster"
        mode: "0644"
        owner: "root:root"
        content: "{{clusterName}}"
    
    # 安装脚本（仅重启 bkeagent）
    installScript: |
      #!/bin/bash
      set -e
      
      # 配置文件由 ConfigRenderer 自动上传到对应路径
      # 只需重启 bkeagent 使配置生效
      systemctl restart bkeagent
      
      # 等待 bkeagent 启动
      sleep 2
      
      # 验证 bkeagent 运行状态
      systemctl is-active bkeagent
      
      echo "bkeagent switched to cluster {{clusterName}}"
    
    # 无需卸载脚本（切换是单向操作）
    uninstallScript: ""
    
    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]
    
    # 健康检查
    healthCheck:
      enabled: true
      timeout: "1m"
      interval: "3s"
      script: |
        #!/bin/bash
        systemctl is-active bkeagent
  
  # 依赖关系：在 cluster-api 部署后执行
  dependencies:
    - name: cluster-api
      phase: Install
  
  # 升级策略
  upgradeStrategy:
    mode: Parallel  # 所有节点同时切换
    batchSize: 0
    timeout: "5m"
    failurePolicy: Continue  # 失败时继续，不阻塞后续流程
  
  # 节点过滤：所有节点都需要切换
  nodeFilter:
    roles: []  # 所有角色
    skipCompleted: true  # 已切换的节点跳过
```

#### 12.5.3 DAG 依赖关系

```yaml
# releaseimage-v2.6.0.yaml
spec:
  install:
    components:
      # Batch 1: bkeagent 安装（监听管理集群）
      - name: bkeagent
        version: v2.6.0
      
      # Batch 2: 集群安装
      - name: kubernetes-master
        version: v1.29.0
        inline:
          handler: EnsureMasterInit
      - name: kubernetes-worker
        version: v1.29.0
        inline:
          handler: EnsureWorkerJoin
      
      # Batch 3: cluster-api 部署（创建目标集群 kubeconfig Secret）
      - name: cluster-api
        version: v1.5.0
        type: helm
      
      # Batch 4: bkeagent 切换（监听目标集群）
      - name: bkeagent-switch
        version: v2.6.0
        dependencies:
          - name: cluster-api
```

#### 12.5.4 执行流程

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    bkeagent-switch 执行流程                                  │
└─────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────┐
  │  1. 前置检查                         │
  │  ├─ 检查注解 bkeagent-listener       │
  │  │   → "current" 或缺失: 跳过       │
  │  │   → "bkecluster": 继续           │
  │  └─ 检查 Condition SwitchBKEAgent    │
  │      → True: 跳过                   │
  │      → False/缺失: 继续             │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  2. 获取节点列表                     │
  │  NodeProvider.GetNodes()             │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  3. 节点过滤                         │
  │  ├─ 排除 Failed/Deleting/Skipped    │
  │  └─ 跳过已切换节点                   │
  │     (NodeComponentStatuses 检查)     │
  └──────────┬───────────────────────────┘
             │
             ▼ (对每个目标节点)
  ┌──────────────────────────────────────┐
  │  4. 渲染配置文件                     │
  │  ConfigRenderer.RenderConfig()       │
  │  ├─ kubeconfig: 从 Secret 读取      │
  │  ├─ node: {{nodeHostname}}           │
  │  └─ cluster: {{clusterName}}         │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  5. SSH 上传配置文件                 │
  │  ├─ /etc/openFuyao/bkeagent/config  │
  │  ├─ /etc/openFuyao/bkeagent/node    │
  │  └─ /etc/openFuyao/bkeagent/cluster │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  6. 执行 installScript               │
  │  ├─ systemctl restart bkeagent       │
  │  ├─ sleep 2                          │
  │  └─ systemctl is-active bkeagent    │
  └──────────┬───────────────────────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  7. 健康检查                         │
  │  HealthChecker.Check()               │
  │  └─ systemctl is-active bkeagent    │
  └──────────┬───────────────────────────┘
             │
        ┌────┴────┐
        │         │
     成功       失败
        │         │
        ▼         ▼
  ┌──────────┐  ┌────────────────────────┐
  │ 更新状态 │  │ 记录错误               │
  │ ├─ Phase │  │ ├─ Phase: Failed       │
  │ │Installed│ │ ├─ Message: err.Error()│
  │ ├─ Node  │  │ └─ Continue: 继续     │
  │ │Component│  └────────────────────────┘
  │ │Statuses │
  │ └─ Listener│
  │ │Target:  │
  │ │bkecluster│
  └──────────┘
             │
             ▼
  ┌──────────────────────────────────────┐
  │  8. 标记完成                         │
  │  ConditionMark(SwitchBKEAgent, True) │
  └──────────────────────────────────────┘
```

#### 12.5.5 关键设计点

**nodeHostname 获取**：

当前通过 ping 命令获取 hostname，新方案从 BKENode CRD 读取：

```go
// pkg/dagexec/bke_node_provider.go

func (p *BKENodeProvider) GetNodes(ctx context.Context, cluster *bkev1beta1.BKECluster) ([]Node, error) {
    var nodes []Node
    
    // 获取 BKENode 列表
    bkeNodeList := &configv1beta1.BKENodeList{}
    if err := p.client.List(ctx, bkeNodeList, client.MatchingLabels{
        "cluster.x-k8s.io/cluster-name": cluster.Name,
    }); err != nil {
        return nil, err
    }
    
    for _, bkeNode := range bkeNodeList.Items {
        node := Node{
            Name:     bkeNode.Name,
            IP:       bkeNode.Spec.IP,
            Hostname: bkeNode.Spec.Hostname,  // 从 BKENode 读取
            Role:     getPrimaryRole(bkeNode.Spec.Role),
            Labels:   bkeNode.Labels,
        }
        nodes = append(nodes, node)
    }
    
    return nodes, nil
}
```

**幂等性检查**：

在 `NodeFilter.isAlreadyAtTarget()` 中增加切换状态检查：

```go
func (f *BKENodeFilter) isAlreadyAtTarget(
    ctx context.Context,
    node Node,
    cv *configv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) bool {
    // 现有逻辑...
    
    // bkeagent-switch 组件的幂等检查
    if cv.Spec.Name == "bkeagent-switch" {
        // 检查 Condition
        if condition.HasConditionStatus(bkev1beta1.SwitchBKEAgentCondition, execCtx.Cluster, confv1beta1.ConditionTrue) {
            return true
        }
        
        // 检查 NodeComponentStatuses
        if execCtx.Cluster.Status.NodeComponentStatuses != nil {
            if compStatuses, ok := execCtx.Cluster.Status.NodeComponentStatuses["bkeagent-switch"]; ok {
                if status, ok := compStatuses[node.IP]; ok {
                    if status.Phase == "Installed" {
                        return true
                    }
                }
            }
        }
    }
    
    return false
}
```

**前置条件检查**：

在 `BinaryComponentExecutor.ExecuteComponent()` 中增加前置检查：

```go
func (e *BinaryComponentExecutor) ExecuteComponent(ctx context.Context, node *ComponentNode, execCtx *ExecutionContext) error {
    cv, err := e.cvStore.GetComponentVersion(ctx, node.Component.Name, node.Component.Version)
    if err != nil {
        return err
    }
    
    // bkeagent-switch 前置检查
    if cv.Spec.Name == "bkeagent-switch" {
        // 检查注解
        listener, ok := annotation.HasAnnotation(execCtx.Cluster, common.BKEAgentListenerAnnotationKey)
        if !ok || listener == common.BKEAgentListenerCurrent {
            execCtx.Log.Info("bkeagent-switch: skip, already listening current cluster")
            return nil
        }
        
        // 检查 Condition
        if condition.HasConditionStatus(bkev1beta1.SwitchBKEAgentCondition, execCtx.Cluster, confv1beta1.ConditionTrue) {
            execCtx.Log.Info("bkeagent-switch: skip, already switched")
            return nil
        }
    }
    
    // ... 现有逻辑 ...
}
```

#### 12.5.6 兼容性设计

**Feature Gate OFF（旧路径）**：

```
EnsureAgentSwitch Phase
    ↓
创建 SwitchCluster Command
    ↓
bkeagent 通过 Command 机制切换
```

**Feature Gate ON（新路径）**：

```
DAG 调度 bkeagent-switch Binary 组件
    ↓
前置检查（注解 + Condition）
    ↓
BinaryInstaller.Install()
    ↓
SSH 上传配置文件 + 重启 bkeagent
    ↓
标记 Condition + NodeComponentStatuses
```

**混合模式**：

| 组件 | Feature Gate | 执行路径 |
|------|-------------|---------|
| bkeagent | OFF | EnsureBKEAgent Phase |
| bkeagent | ON | Binary 组件 |
| bkeagent-switch | OFF | EnsureAgentSwitch Phase |
| bkeagent-switch | ON | Binary 组件 |

#### 12.5.7 设计决策总结

| 决策 | 选择 | 理由 |
|------|------|------|
| 组件类型 | Binary | 需要在节点上执行 SSH 命令 |
| 执行时机 | DAG 最后阶段 | 依赖 cluster-api 部署完成 |
| 制品下载 | 无 | bkeagent 已安装，只需切换配置 |
| 切换机制 | SSH 脚本 | 架构统一，不依赖 Command 机制 |
| 幂等检查 | Condition + NodeComponentStatuses | 双重检查，确保可靠性 |
| 错误处理 | FailurePolicy = Continue | 切换失败不阻塞后续流程 |

### 12.6 迁移验证清单

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
| **bkeagent kubeconfig** | `diff` 旧路径输出 vs 新路径输出 | 内容一致（新路径: `secretRef` 读取 `kube-system/localkubeconfig`，与旧路径 `GetLocalKubeConfig()` 等价） |
| **Feature Gate 关闭回退** | 关闭 Feature Gate，执行安装/升级 | EnsureNodesEnv scope 含 `runtime`，EnsureContainerdUpgrade 走旧路径，行为不变 |
| **混合模式** | containerd 开启、bkeagent 关闭 | containerd 走新路径，bkeagent 走旧路径 |

---

## 13. 错误处理与恢复

### 13.1 错误处理流程图

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

## 14. 测试设计

### 14.1 单元测试

| 测试模块 | 测试场景 | 覆盖目标 |
|---------|---------|---------|
| **ArtifactDownloader** | HTTP 下载、Checksum 校验、缓存命中/未命中、架构适配 | >90% |
| **TemplateRenderer** | 8 类变量替换、条件渲染、自定义函数、错误处理 | >90% |
| **ConfigRenderer** | content 渲染、secretRef 获取、kubeconfig 生成、forEach 动态多文件生成 | >90% |
| **BinaryInstaller** | Install/Upgrade/Uninstall 完整流程、失败重试 | >85% |
| **HelmInstaller** | OCI/HTTP/本地 Chart 获取、Values 渲染、Install/Upgrade/Rollback | >85% |
| **BinaryComponentExecutor** | Rolling/Parallel/Batch 执行策略、FailurePolicy、ComponentVariables 注入 | >85% |
| **ExecutionContext** | TemplateContext 初始化、Variables 注入（ContainerRuntimeCRI）、ComponentVariables 初始化、边界条件（nil ClusterConfig、空 CRI） | >90% |

#### 14.1.1 ExecutionContext 测试用例

**测试文件**: `pkg/dagexec/execution_context_test.go`

| 测试名称 | 场景 | 输入 | 验证点 |
|---------|------|------|--------|
| `TestNewExecutionContext_ContainerRuntimeCRI` | CRI=containerd | `cluster.Spec.ClusterConfig.Cluster.ContainerRuntime.CRI = "containerd"` | `ctx.TemplateContext.Variables["ContainerRuntimeCRI"] == "containerd"` |
| `TestNewExecutionContext_ContainerRuntimeCRI_Docker` | CRI=docker | `cluster.Spec.ClusterConfig.Cluster.ContainerRuntime.CRI = "docker"` | `ctx.TemplateContext.Variables["ContainerRuntimeCRI"] == "docker"` |
| `TestNewExecutionContext_EmptyCRI` | CRI 为空 | `cluster.Spec.ClusterConfig.Cluster.ContainerRuntime.CRI = ""` | `ctx.TemplateContext.Variables` 不为 nil，但不包含 "ContainerRuntimeCRI" key |
| `TestNewExecutionContext_NilClusterConfig` | ClusterConfig 为 nil | `cluster.Spec.ClusterConfig = nil` | `ctx.TemplateContext.Variables` 和 `ComponentVariables` 正确初始化为空 map，基础字段留空 |
| `TestNewExecutionContext_ComponentVariablesInit` | 正常场景 | 完整的 BKECluster | `ctx.TemplateContext.ComponentVariables` 初始化为空 map，可正常写入 |
| `TestNewExecutionContext_BasicFields` | 正常场景 | 完整的 BKECluster | `ClusterName`、`Namespace`、`KubernetesVersion`、`OpenFuyaoVersion` 正确填充 |
| `TestNewExecutionContext_ClusterExtFields` | 正常场景 | 完整的 BKECluster | `APIServer`、`ServiceCIDR`、`PodCIDR`、`DNSDomain`、`ImageRegistry` 正确填充 |
| `TestNewExecutionContext_NilCluster` | cluster 为 nil | `cluster = nil` | 函数不 panic，`TemplateContext` 仍正确初始化 |
| `TestNewExecutionContext_ConfigInjection` | 正常场景 | 完整的 BKECluster | `ctx.TemplateContext.Config` 不为 nil，且等于 `cluster.Spec.ClusterConfig` |
| `TestNewExecutionContext_ConfigAccess` | 使用完整配置 | 完整的 BKECluster | 可通过 `ctx.TemplateContext.Config.Cluster.ContainerRuntime.CRI` 访问配置 |

**测试代码示例**:

```go
func TestNewExecutionContext_ContainerRuntimeCRI(t *testing.T) {
    cluster := &bkev1beta1.BKECluster{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-cluster",
            Namespace: "default",
        },
        Spec: confv1beta1.BKEClusterSpec{
            ClusterConfig: &confv1beta1.BKEConfig{
                Cluster: confv1beta1.Cluster{
                    ContainerRuntime: confv1beta1.ContainerRuntime{
                        CRI: "containerd",
                    },
                },
            },
        },
    }
    
    ctx := NewExecutionContext(
        nil, cluster,
        nil, nil, nil, nil,
        nil, nil, nil,
    )
    
    if ctx.TemplateContext.Variables == nil {
        t.Fatal("Variables should be initialized")
    }
    if cri, ok := ctx.TemplateContext.Variables["ContainerRuntimeCRI"]; !ok {
        t.Fatal("ContainerRuntimeCRI should be injected")
    } else if cri != "containerd" {
        t.Fatalf("ContainerRuntimeCRI = %q, want %q", cri, "containerd")
    }
}

func TestNewExecutionContext_EmptyCRI(t *testing.T) {
    cluster := &bkev1beta1.BKECluster{
        Spec: confv1beta1.BKEClusterSpec{
            ClusterConfig: &confv1beta1.BKEConfig{
                Cluster: confv1beta1.Cluster{
                    ContainerRuntime: confv1beta1.ContainerRuntime{
                        CRI: "",
                    },
                },
            },
        },
    }
    
    ctx := NewExecutionContext(nil, cluster, nil, nil, nil, nil, nil, nil, nil)
    
    if ctx.TemplateContext.Variables == nil {
        t.Fatal("Variables should be initialized")
    }
    if _, ok := ctx.TemplateContext.Variables["ContainerRuntimeCRI"]; ok {
        t.Fatal("ContainerRuntimeCRI should not be injected when CRI is empty")
    }
}

func TestNewExecutionContext_NilClusterConfig(t *testing.T) {
    cluster := &bkev1beta1.BKECluster{
        Spec: confv1beta1.BKEClusterSpec{
            ClusterConfig: nil,
        },
    }
    
    ctx := NewExecutionContext(nil, cluster, nil, nil, nil, nil, nil, nil, nil)
    
    if ctx.TemplateContext.Variables == nil {
        t.Fatal("Variables should be initialized even when ClusterConfig is nil")
    }
    if ctx.TemplateContext.ComponentVariables == nil {
        t.Fatal("ComponentVariables should be initialized even when ClusterConfig is nil")
    }
    if ctx.TemplateContext.Config != nil {
        t.Fatal("Config should be nil when ClusterConfig is nil")
    }
}

func TestNewExecutionContext_ConfigInjection(t *testing.T) {
    cluster := &bkev1beta1.BKECluster{
        Spec: confv1beta1.BKEClusterSpec{
            ClusterConfig: &confv1beta1.BKEConfig{
                Cluster: confv1beta1.Cluster{
                    KubernetesVersion: "v1.25.6",
                    ContainerRuntime: confv1beta1.ContainerRuntime{
                        CRI: "containerd",
                    },
                },
            },
        },
    }
    
    ctx := NewExecutionContext(nil, cluster, nil, nil, nil, nil, nil, nil, nil)
    
    if ctx.TemplateContext.Config == nil {
        t.Fatal("Config should be injected")
    }
    if ctx.TemplateContext.Config != cluster.Spec.ClusterConfig {
        t.Fatal("Config should be the same object as cluster.Spec.ClusterConfig")
    }
    if ctx.TemplateContext.Config.Cluster.KubernetesVersion != "v1.25.6" {
        t.Fatalf("Config.Cluster.KubernetesVersion = %q, want %q", 
            ctx.TemplateContext.Config.Cluster.KubernetesVersion, "v1.25.6")
    }
}

func TestNewExecutionContext_ConfigAccess(t *testing.T) {
    cluster := &bkev1beta1.BKECluster{
        Spec: confv1beta1.BKEClusterSpec{
            ClusterConfig: &confv1beta1.BKEConfig{
                Cluster: confv1beta1.Cluster{
                    ContainerRuntime: confv1beta1.ContainerRuntime{
                        CRI: "docker",
                    },
                    ImageRepo: confv1beta1.Repo{
                        Domain: "registry.example.com",
                    },
                },
            },
        },
    }
    
    ctx := NewExecutionContext(nil, cluster, nil, nil, nil, nil, nil, nil, nil)
    
    // 验证可通过完整路径访问配置
    if ctx.TemplateContext.Config.Cluster.ContainerRuntime.CRI != "docker" {
        t.Fatal("Should be able to access CRI via Config")
    }
    if ctx.TemplateContext.Config.Cluster.ImageRepo.Domain != "registry.example.com" {
        t.Fatal("Should be able to access ImageRepo.Domain via Config")
    }
}
```

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
| **YamlComponentExecutor 核心实现** | 5 人日 | 中 | 无 |
| **TemplateRenderer 实现** | 3 人日 | 低 | 无 |
| **ConfigRenderer 实现** | 3 人日 | 低 | TemplateRenderer |
| **ApplyStrategy 引擎实现** | 3 人日 | 中 | YamlComponentExecutor |
| **Prune 裁剪功能实现** | 3 人日 | 中 | ApplyStrategy 引擎 |
| **PreInstallHooks 执行引擎** | 3 人日 | 中 | HelmInstaller |
| **Binary 健康检查实现** | 2 人日 | 中 | BinaryInstaller |
| **YAML 健康检查实现** | 1 人日 | 低 | YamlComponentExecutor (复用 Helm 健康检查逻辑) |
| **ComponentVersion CRD 扩展** | 3 人日 | 低 | 无 |
| **CRD v1alpha2 版本迁移** | 2 人日 | 中 | CRD 扩展 |
| **VersionContext 与 ExecutionContext 实现** | 3 人日 | 中 | 无 |
| **BinaryComponentExecutor 集成** | 3 人日 | 中 | BinaryInstaller |
| **HelmComponentExecutor 集成** | 3 人日 | 中 | HelmInstaller |
| **YamlComponentExecutor 集成** | 2 人日 | 中 | YamlComponentExecutor |
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
| **总计** | **93 人日 (约 4 人月)** | | |

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
| **ComponentVersion** | 组件版本 CRD，定义组件的类型、配置、依赖等 |
| **ReleaseImage** | 发布版本清单 CRD，定义安装和升级的组件列表 |
| **DAG** | 有向无环图，用于表示组件依赖关系和执行顺序 |
| **Feature Gate** | 功能开关，用于控制新功能的启用/禁用 |
| **Rolling Update** | 滚动更新，逐节点执行升级操作 |

---

## 17. 安装与升级样例

> 已迁移至独立文档：[kep6-install-upgrade-samples.md](./kep6-install-upgrade-samples.md)

---

**文档版本**: v1.2  
**维护者**: openFuyao Team

# KEP-10: 安装 DAG 化设计（PhaseFlow 兜底）

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-10 |
| **标题** | 安装 DAG 化设计（PhaseFlow 兜底） |
| **状态** | `provisional` |
| **类型** | Feature |
| **依赖** | KEP-5 声明式升级框架、Cluster API v1beta1 |

---

## 目录

1. [摘要](#1-摘要)
2. [动机](#2-动机)
   - 2.1 现状问题
   - 2.2 安装 vs 升级能力对比
   - 2.3 设计目标
3. [范围与约束](#3-范围与约束)
   - 3.1 范围
   - 3.2 约束
   - 3.3 非目标
4. [ReleaseImageComponent 结构统一抽象](#4-releaseimagecomponent-结构统一抽象)
   - 4.1 设计思路
   - 4.2 当前结构（不对称）
   - 4.3 目标结构（两层统一抽象）
   - 4.4 ReleaseImage YAML 示例（完整安装 + 升级）
5. [安装组件目录设计](#5-安装组件目录设计)
   - 5.1 DeclarativeUpgradeCatalog 的作用（现有升级组件目录）
   - 5.2 DeclarativeInstallCatalog
   - 5.3 安装组件与升级组件目录对比
   - 5.4 ComponentFactory 注册扩展
6. [安装 DAG 构建设计](#6-安装-dag-构建设计)
   - 6.1 设计思路
   - 6.2 VersionContext 扩展
   - 6.3 安装 DAG 构建器
   - 6.4 安装 VersionContext 构建
7. [安装 DAG 执行设计](#7-安装-dag-执行设计)
   - 7.1 设计思路
   - 7.2 执行入口（三路分发 + PhaseFlow 兜底路径设计）
   - 7.3 executeInstallDAG 实现
   - 7.4 安装 DAG 结构
8. [部署 Phase 与安装组件映射](#8-部署-phase-与安装组件映射)
   - 8.1 设计思路
   - 8.2 DeployPhases → 安装组件映射
   - 8.3 安装 vs 升级组件差异
9. [Feature Gate 与迁移策略](#9-feature-gate-与迁移策略)
   - 9.1 Feature Gate 设计
   - 9.2 迁移阶段
   - 9.3 向后兼容
   - 9.4 平滑升级方案
10. [可观测性](#10-可观测性)
    - 10.1 安装状态追踪
    - 10.2 事件与指标
11. [工作量评估](#11-工作量评估)
    - 11.1 开发工作量
    - 11.2 测试工作量
    - 11.3 文档工作量
    - 11.4 总工作量汇总
12. [风险与缓解措施](#12-风险与缓解措施)
    - 12.1 技术风险
    - 12.2 平滑升级风险
    - 12.3 业务风险
13. [releasemanifest.Bundle 的作用](#13-releasemanifestbundle-的作用)
    - 13.1 是什么
    - 13.2 作用
    - 13.3 与 ReleaseImage CR 的关系
    - 13.4 三级缓存生命周期
    - 13.5 BundleStore 适配器 — Bundle 到执行层的桥梁
    - 13.6 Bundle 的消费者汇总
    - 13.7 命名注意事项
14. [ComponentVersion 执行时条件过滤](#14-componentversion-执行时条件过滤)
15. [ReleaseImageUpgradeComponent.Inline 字段移除设计](#15-releaseimageupgradecomponentinline-字段移除设计)
    - 15.1 设计动机
    - 15.2 现状分析
    - 15.3 移除方案
    - 15.4 影响范围
    - 15.5 向后兼容性
    - 15.6 与设计原则的一致性
- [附录](#附录)

> **文档结构说明**：
> - §1-6: 设计基础（动机、范围、结构定义、目录设计、DAG 构建）
> - §7-8: 执行设计（DAG 执行入口、安装实现、Phase 映射）
> - §9: 迁移策略（Feature Gate、向后兼容、平滑升级）— 包含 PhaseFlow 兜底路径设计（§7.2.3-7.2.6 引用此处）
> - §10-15: 辅助内容（可观测性、工作量、风险、Bundle、条件过滤、Inline 字段移除）

---

## 1. 摘要

本提案设计 ReleaseImage 安装组件的声明式定义，使**安装流程**由 DAG 驱动。当前 ReleaseImage 的 `spec.install.components` 仅包含 `{name, version}` 两个字段，缺少执行模式（inline/manifest）、inline handler、依赖关系等声明式元数据；且安装流程完全由硬编码的 `DeployPhases` PhaseFlow 驱动，未消费 ReleaseImage 的安装组件列表。本提案将 `ReleaseImageInstallComponent` 和 `ReleaseImageUpgradeComponent` 统一抽象为 `ReleaseImageComponent`，新增 `inline` 字段，新增安装组件目录（`DeclarativeInstallCatalog`）和安装 DAG 构建器，使安装流程享受声明式组件管理的优势：依赖管理、并行执行、版本追踪、可观测性。

**本提案范围**：仅对安装场景进行 DAG 化。PhaseFlow 作为非安装/非升级场景（扩容/纳管/删除/DryRun/暂停）的**兜底方案**保留，后续再优化。升级场景已由 DAG 驱动（现有，不变）。

## 2. 动机

### 2.1 现状问题

| 问题 | 说明 | 影响 |
|------|------|------|
| **安装/升级结构不对称** | `ReleaseImageInstallComponent` 仅 `{name, version}`，`ReleaseImageUpgradeComponent` 有 `inline.handler` | 安装无法声明执行模式 |
| **安装无 DAG** | 安装完全由硬编码 `DeployPhases` 列表驱动 | 无依赖管理、无并行、无断点续传 |
| **安装组件不消费 ReleaseImage** | DeployPhases 从 `BKECluster.Spec` 读取版本，不从 `ReleaseImage.Spec.Install` 读取 | 版本管理脱节 |
| **无安装组件目录** | `DeclarativeUpgradeCatalog` 仅定义升级组件映射，无安装等价物 | 新增安装组件需修改 PhaseFlow 代码 |
| **安装与升级逻辑割裂** | 安装走 PhaseFlow，升级走 DAG，两套机制维护成本高 | 代码重复、行为不一致 |

### 2.2 安装 vs 升级能力对比

| 维度 | 安装（PhaseFlow） | 升级（DAG） |
|------|-------------------|------------|
| **编排方式** | 硬编码 Phase 列表 | ReleaseImage bundle 动态构建 DAG |
| **组件来源** | DeployPhases 构造函数 | `ReleaseImage.Spec.Upgrade.Components` |
| **执行元数据** | 无 | `UpgradeComponentSpec.{Mode, ManifestPath, InlineHandler}` |
| **ReleaseImage 字段** | `Spec.Install` 未被消费 | `Spec.Upgrade.Components` 驱动 DAG |
| **依赖管理** | 隐式 Phase 顺序 | `ComponentVersion.spec.dependencies` + 拓扑排序 |
| **版本决策** | 无（总是执行） | `VersionContext.Decide()` → Skip/Upgrade |
| **组件目录** | 无 | `DeclarativeUpgradeCatalog` |
| **并行执行** | 无（串行 Phase） | 同批次并行（MaxParallel=8） |
| **断点续传** | 无 | `DeclarativeUpgradeStatus` 追踪已完成 |
| **可观测性** | PhaseStatus | `ClusterComponentStatuses` + Events |

### 2.3 设计目标

1. **结构对称**：`ReleaseImageInstallComponent` 与 `ReleaseImageUpgradeComponent` 统一抽象为 `ReleaseImageComponent`
2. **统一 DAG**：安装流程也通过 DAG 驱动，复用 Scheduler/ExecutorRegistry/VersionContext
3. **统一目录**：`DeclarativeInstallCatalog` 与 `DeclarativeUpgradeCatalog` 结构一致
4. **渐进迁移**：通过 Feature Gate 控制，PhaseFlow 和 DAG 安装路径可并存
5. **向后兼容**：迁移期间 ReleaseImage 可同时包含旧格式和声明式格式的安装组件

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| **CRD 扩展** | `ReleaseImageComponent` 新增 `inline` 字段（统一安装和升级） |
| **安装组件目录** | `DeclarativeInstallCatalog` 定义安装组件映射 |
| **安装 DAG 构建** | `BuildInstallDAG` 从 ReleaseImage bundle 构建安装 DAG |
| **安装 DAG 执行** | 复用 `Scheduler.ExecuteDAG`，新增 `DecisionInstall` |
| **Feature Gate** | `DeclarativeInstallEnabled` 控制新旧路径切换 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 旧格式 `{name, version}` 的 `ReleaseImageComponent` 必须继续支持 |
| **PhaseFlow 共存** | 迁移期间 PhaseFlow 和 DAG 安装路径可并存，通过 Feature Gate 切换 |
| **复用升级框架** | 安装 DAG 复用 Scheduler、ExecutorRegistry、InlineRunner 等已有组件 |
| **幂等性** | 安装操作必须幂等，支持 Reconcile 重入 |
| **依赖正确性** | 安装 DAG 的依赖关系必须反映实际安装顺序约束 |

### 3.3 非目标

- 不在本文档定义 PreCheck/PostCheck（引用 KEP-5-2）
- 不在本文档定义回滚策略（安装失败不回滚，直接重试）
- 不重写现有 DeployPhases 的 Phase 实现（复用已有代码）

## 4. ReleaseImageComponent 结构统一抽象

### 4.1 设计思路

当前 ReleaseImage 的安装组件和升级组件结构**不对称**：升级组件有 `inline.handler` 声明执行方式，安装组件仅有 `{name, version}`。这导致安装流程无法从 ReleaseImage 获取执行元数据，只能依赖硬编码的 `DeployPhases` 列表。

**核心设计思路**：将安装组件结构向升级组件对齐，使安装也能从 ReleaseImage 声明执行方式 (inline handler 或 manifest 路径)，从而让安装流程可以像升级一样由 DAG 驱动。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              结构扩展设计思路                                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  现状 (不对称):                                                                  │
│  ReleaseImage:                                                                  │
│    install.components:  [{name, version}]              ← 无执行元数据           │
│    upgrade.components:  [{name, version, inline}]      ← 有执行元数据           │
│                                                                                 │
│  安装流程:                                                                       │
│    DeployPhases (硬编码 Phase 列表) → 逐个执行                                    │
│    不消费 ReleaseImage.install.components                                         │
│    版本从 BKECluster.Spec 读取                                                    │
│                                                                                 │
│  目标 (对称):                                                                    │
│  ReleaseImage:                                                                  │
│    install.components:  [{name, version, inline}]      ← 有执行元数据 ★         │
│    upgrade.components:  [{name, version, inline}]      ← 有执行元数据           │
│                                                                                 │
│  安装流程:                                                                       │
│    ReleaseImage.install.components → BuildInstallDAG → DAG 执行                 │
│    从 ReleaseImage 读取版本 + 执行方式                                            │
│    复用升级的 Scheduler/ExecutorRegistry/VersionContext                           │
│                                                                                 │
│  设计原则:                                                                       │
│  1. 结构对称 — install 和 upgrade components 使用相同的结构                      │
│  2. 向后兼容 — 旧格式 {name, version} (无 inline) 仍支持，走 Legacy PhaseFlow   │
│  3. 复用升级框架 — 不新建调度器/执行器，安装 DAG 复用 Scheduler                   │
│  4. 声明式驱动 — 安装流程由 ReleaseImage 声明驱动，而非硬编码 Phase 列表         │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 当前结构（不对称）

```go
// 当前：安装组件仅 {name, version}
type ReleaseImageInstallComponent struct {
    Name    string `json:"name,omitempty"`
    Version string `json:"version,omitempty"`
}

// 当前：升级组件有 inline handler
type ReleaseImageUpgradeComponent struct {
    Name    string                     `json:"name,omitempty"`
    Version string                     `json:"version,omitempty"`
    Inline  *ReleaseImageUpgradeInline `json:"inline,omitempty"`
}
```

### 4.3 目标结构（两层统一抽象）

经过分析，ReleaseImage 的类型定义存在两层重复：

**第一层重复**：`ReleaseImageInstallComponent` 和 `ReleaseImageUpgradeComponent` 字段完全相同（移除 `ReleaseImageUpgradeComponent.Inline` 后，见 [§15](#15-releaseimageupgradecomponentinline-字段移除设计)）
**第二层重复**：`ReleaseImageInstallSpec` 和 `ReleaseImageUpgradeSpec` 字段完全相同

可以通过**两层统一抽象**消除所有重复：

```go
// api/v1alpha1/releaseimage_types.go

// ============================================================
// 第一层抽象：统一组件类型
// ============================================================

// ReleaseImageComponent 定义组件引用（安装和升级共用）🔄重构
// 统一替代 ReleaseImageInstallComponent 和 ReleaseImageUpgradeComponent
type ReleaseImageComponent struct {
    // 组件名称（对应 ComponentVersion.Name）
    Name string `json:"name,omitempty"`
    
    // 组件版本（对应 ComponentVersion.Version）
    Version string `json:"version,omitempty"`
    
    // Inline handler 配置（type=inline 时指定）🆕新增
    // 用于安装和升级场景，指定 ComponentFactory 注册的 handler
    // 示例: "EnsureBKEAgent", "EnsureCerts", "EnsureMasterInit", "EnsureMasterUpgrade"
    Inline *ReleaseImageInline `json:"inline,omitempty"`
}

// ReleaseImageInline 定义 inline handler 引用 🔄重构
// 统一替代 ReleaseImageInstallInline 和 ReleaseImageUpgradeInline
type ReleaseImageInline struct {
    // Handler 名称（对应 ComponentFactory 注册的 handler）
    // 安装示例: "EnsureBKEAgent", "EnsureNodesEnv", "EnsureMasterInit"
    // 升级示例: "EnsureAgentUpgrade", "EnsureMasterUpgrade", "EnsureEtcdUpgrade"
    Handler string `json:"handler,omitempty"`
    
    // Handler 版本
    Version string `json:"version,omitempty"`
}

// ============================================================
// 第二层抽象：统一组件列表类型
// ============================================================

// ReleaseImageComponentList 定义组件列表（安装和升级共用）🔄重构
// 统一替代 ReleaseImageInstallSpec 和 ReleaseImageUpgradeSpec
type ReleaseImageComponentList struct {
    Components []ReleaseImageComponent `json:"components,omitempty"`
}

// ReleaseImageInstallSpec 定义安装组件列表（类型别名，向后兼容）
type ReleaseImageInstallSpec = ReleaseImageComponentList

// ReleaseImageUpgradeSpec 定义升级组件列表（类型别名，向后兼容）
type ReleaseImageUpgradeSpec = ReleaseImageComponentList

// ============================================================
// ReleaseImageSpec 使用统一类型
// ============================================================

type ReleaseImageSpec struct {
    Version            string                     `json:"version,omitempty"`
    Digest             string                     `json:"digest,omitempty"`
    VerifySignature    bool                       `json:"verifySignature,omitempty"`
    SignatureKey       string                     `json:"signatureKey,omitempty"`
    AllowCacheFallback bool                       `json:"allowCacheFallback,omitempty"`
    Install            *ReleaseImageComponentList `json:"install,omitempty"`  // 🔄重构: 使用统一类型
    Upgrade            *ReleaseImageComponentList `json:"upgrade,omitempty"`  // 🔄重构: 使用统一类型
}
```

**两层抽象的收益**：

| 维度 | 重构前 | 重构后 |
|------|--------|--------|
| **组件类型数量** | 2 个 (InstallComponent, UpgradeComponent) | 1 个 (Component) |
| **Inline 类型数量** | 2 个 (InstallInline, UpgradeInline) | 1 个 (Inline) |
| **Spec 类型数量** | 2 个 (InstallSpec, UpgradeSpec) | 1 个 (ComponentList) + 2 个别名 |
| **总类型数量** | 6 个 | 3 个 + 2 个别名 |
| **代码重复** | 3 处重复 (Component×2, Inline×2, Spec×2) | 0 处重复 |
| **API 复杂度** | 安装和升级使用不同类型，需要转换逻辑 | 统一类型，无需转换 |
| **维护成本** | 修改字段需同时更新多个类型 | 修改字段只需更新 1 个类型 |
| **向后兼容** | — | 类型别名保持现有代码无需修改 |

**向后兼容性**：

类型别名 (`type ReleaseImageInstallSpec = ReleaseImageComponentList`) 确保现有代码无需修改：

```go
// 现有代码无需修改
var installSpec *ReleaseImageInstallSpec  // 类型别名，等价于 ReleaseImageComponentList
var upgradeSpec *ReleaseImageUpgradeSpec  // 类型别名，等价于 ReleaseImageComponentList

// 可以直接赋值和转换
installSpec = &ReleaseImageComponentList{Components: components}
upgradeSpec = installSpec  // 类型相同，可以直接赋值
```

YAML 结构与重构前完全一致：

```yaml
# 重构前后 YAML 结构不变
spec:
  install:
    components:
      - name: bkeagent
        version: v2.7.0
        inline:
          handler: EnsureBKEAgent
          version: v1.0.0
  upgrade:
    components:
      - name: bkeagent
        version: v2.7.0
        inline:
          handler: EnsureAgentUpgrade
          version: v1.0.0
```

**代码迁移**：

```go
// 重构前（6 个类型）
type ReleaseImageInstallComponent struct { Name, Version string; Inline *ReleaseImageInstallInline }
type ReleaseImageUpgradeComponent struct { Name, Version string; Inline *ReleaseImageUpgradeInline }
type ReleaseImageInstallInline struct { Handler, Version string }
type ReleaseImageUpgradeInline struct { Handler, Version string }
type ReleaseImageInstallSpec struct { Components []ReleaseImageInstallComponent }
type ReleaseImageUpgradeSpec struct { Components []ReleaseImageUpgradeComponent }

// 重构后（3 个类型 + 2 个别名）
type ReleaseImageComponent struct { Name, Version string; Inline *ReleaseImageInline }
type ReleaseImageInline struct { Handler, Version string }
type ReleaseImageComponentList struct { Components []ReleaseImageComponent }
type ReleaseImageInstallSpec = ReleaseImageComponentList   // 类型别名
type ReleaseImageUpgradeSpec = ReleaseImageComponentList  // 类型别名

// 使用方式
installSpec.Components  // []ReleaseImageComponent
upgradeSpec.Components  // []ReleaseImageComponent
// 无需类型转换，直接共用
```

### 4.4 ReleaseImage YAML 示例（完整安装 + 升级）

```yaml
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.7.0
spec:
  version: "v2.7.0"
  digest: "sha256:..."
  
  # ============================================================
  # 安装组件：定义全新安装时执行的组件列表
  # ============================================================
  install:
    components:
      - name: bkeagent
        version: v2.7.0
        inline:
          handler: EnsureBKEAgent
          version: v1.0.0
      
      - name: nodes-env
        version: v1.0.0
        inline:
          handler: EnsureNodesEnv
          version: v1.0.0
      
      - name: cluster-api-obj
        version: v1.0.0
        inline:
          handler: EnsureClusterAPIObj
          version: v1.0.0
      
      - name: certs
        version: v2.7.0
        inline:
          handler: EnsureCerts
          version: v1.0.0
      
      - name: load-balance
        version: v2.1.4
        inline:
          handler: EnsureLoadBalance
          version: v1.0.0
      
      - name: kubernetes-master
        version: v1.36.0
        inline:
          handler: EnsureMasterInit
          version: v1.0.0
      
      - name: kubernetes-worker
        version: v1.36.0
        inline:
          handler: EnsureWorkerJoin
          version: v1.0.0
      
      - name: kube-proxy
        version: v1.36.0
        # type=yaml, 无需 inline handler
      
      - name: coredns
        version: v1.11.3
        # type=yaml, 无需 inline handler
      
      - name: nodes-postprocess
        version: v1.0.0
        inline:
          handler: EnsureNodesPostProcess
          version: v1.0.0
      
      - name: agent-switch
        version: v1.0.0
        inline:
          handler: EnsureAgentSwitch
          version: v1.0.0
  
  # ============================================================
  # 升级组件：定义版本升级时执行的组件列表
  # ============================================================
  upgrade:
    components:
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0.0
      - name: bkeagent
        version: v2.7.0
        inline:
          handler: EnsureAgentUpgrade
          version: v1.0.0
      - name: containerd
        version: v1.7.24
        inline:
          handler: EnsureContainerdUpgrade
          version: v1.0.0
      - name: etcd
        version: v3.5.20
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0.0
      - name: kubernetes-master
        version: v1.36.0
        inline:
          handler: EnsureMasterUpgrade
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.36.0
        inline:
          handler: EnsureWorkerUpgrade
          version: v1.0.0
      - name: kube-proxy
        version: v1.36.0
      - name: coredns
        version: v1.11.3
```

## 5. 安装组件目录设计

### 5.1 DeclarativeUpgradeCatalog 的作用 (现有升级组件目录)

在说明安装组件目录 `DeclarativeInstallCatalog` 之前，先理解现有升级组件目录 `DeclarativeUpgradeCatalog` 的作用，因为安装目录是它的对称设计。

#### 5.1.1 是什么

`DeclarativeUpgradeCatalog` 是一个**静态映射表** (Go `var` 切片)，定义在 `pkg/upgrade/catalog.go` 中。它将 ReleaseImage 中的升级组件名称映射到具体的执行模式 (inline/manifest)、inline handler 名称、manifest 路径和 legacy Phase 名称。

```go
// pkg/upgrade/catalog.go (现有代码)

// DeclarativeUpgradeCatalog is the canonical upgrade component table for ReleaseImage DAG.
var DeclarativeUpgradeCatalog = []UpgradeComponentSpec{
    {Name: "pre-upgrade-resources", Mode: UpgradeExecutionInline,
     InlineHandler: "EnsurePreUpgradeResources", LegacyPhase: "EnsurePreUpgradeResources"},
    {Name: "provider", Mode: UpgradeExecutionManifest,
     ManifestPath: "provider/v1.0.0/component.yaml", LegacyPhase: "EnsureProviderSelfUpgrade"},
    {Name: "bkeagent", Mode: UpgradeExecutionInline,
     InlineHandler: "EnsureAgentUpgrade", LegacyPhase: "EnsureAgentUpgrade"},
    {Name: "kube-proxy", Mode: UpgradeExecutionManifest,
     ManifestPath: "kube-proxy/v1.0.0/component.yaml"},
    {Name: "coredns", Mode: UpgradeExecutionManifest,
     ManifestPath: "coredns/v1.0.0/component.yaml", LegacyPhase: "EnsureComponentUpgrade"},
    {Name: "etcd", Mode: UpgradeExecutionInline,
     InlineHandler: "EnsureEtcdUpgrade", LegacyPhase: "EnsureEtcdUpgrade"},
    {Name: "kubernetes-master", Mode: UpgradeExecutionInline,
     InlineHandler: "EnsureMasterUpgrade", LegacyPhase: "EnsureMasterUpgrade"},
    {Name: "kubernetes-worker", Mode: UpgradeExecutionInline,
     InlineHandler: "EnsureWorkerUpgrade", LegacyPhase: "EnsureWorkerUpgrade"},
    {Name: "containerd", Mode: UpgradeExecutionInline,
     InlineHandler: "EnsureContainerdUpgrade", LegacyPhase: "EnsureContainerdUpgrade"},
}
```

#### 5.1.2 解决什么问题

| 问题 | 没有 Catalog 时 | 有 Catalog 后 |
|------|----------------|--------------|
| **组件名 → 执行模式映射** | DAG 调度器收到 `ComponentNode{Name: "kubernetes-master"}` 后，不知道该用 inline 还是 manifest 执行 | 从 Catalog 查表：`Mode=UpgradeExecutionInline`，使用 inline 执行器 |
| **组件名 → inline handler 映射** | inline 类型组件需要知道调用哪个 Phase Handler (如 `EnsureMasterUpgrade`) | 从 Catalog 查表：`InlineHandler="EnsureMasterUpgrade"` |
| **组件名 → manifest 路径映射** | manifest 类型组件需要知道 YAML 清单路径 | 从 Catalog 查表：`ManifestPath="coredns/v1.0.0/component.yaml"` |
| **组件名 → legacy Phase 映射** | 声明式升级与 legacy PhaseFlow 共存时需互相转换 | 从 Catalog 查表：`LegacyPhase="EnsureMasterUpgrade"` |
| **新增组件的注册点** | 新增组件需修改 DAG 构建代码、Phase 注册代码等多处 | 只需在 Catalog 中添加一条记录 + 注册 handler |

#### 5.1.3 在升级流程中的角色

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              DeclarativeUpgradeCatalog 在升级流程中的角色                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ReleaseImage.upgrade.components:                                               │
│    - name: kubernetes-master    (声明: 组件名 + 版本)                            │
│      version: v1.36.0                                                           │
│      inline:                                                                    │
│        handler: EnsureMasterUpgrade                                             │
│        version: v1.0.0                                                          │
│    - name: coredns                                                              │
│      version: v1.11.3                                                           │
│      (无 inline → manifest 模式)                                                │
│                                                                                 │
│           │                                                                     │
│           ▼ BuildDAGFromBundle(bundle, resolver) (调用 topology.BuildDAG)        │
│                                                                                 │
│  遍历 ReleaseImage.upgrade.components:                                          │
│    for _, comp := range bundle.Release.Spec.Upgrade.Components {               │
│        // 从 Catalog 查找组件的执行模式                                          │
│        catalogEntry := findInCatalog(DeclarativeUpgradeCatalog, comp.Name)      │
│        // catalogEntry = {Name: "kubernetes-master",                             │
│        //                 Mode: UpgradeExecutionInline,                         │
│        //                 InlineHandler: "EnsureMasterUpgrade"}                 │
│                                                                                 │
│        // 构建 DAG 节点 (含执行模式信息)                                         │
│        dag.AddNode(ComponentNode{                                               │
│            Name:          comp.Name,           // "kubernetes-master"          │
│            Version:       comp.Version,         // "v1.36.0"                   │
│            Inline:        comp.Inline,          // {Handler: "EnsureMasterUpgrade"}│
│            FailurePolicy: catalogEntry.FailurePolicy,                          │
│            Dependencies:  resolveDependencies(comp.Name, bundle),             │
│        })                                                                       │
│    }                                                                            │
│                                                                                 │
│           │                                                                     │
│           ▼ Scheduler.ExecuteDAG(ctx, execCtx, dag)                             │
│                                                                                 │
│  DAG 执行时, 对每个节点:                                                        │
│    if node.Inline != nil {                                                      │
│        // inline 模式 → InlineComponentExecutor                                  │
│        handler := ComponentFactory.Resolve(node.Inline.Handler)                │
│        // handler = EnsureMasterUpgrade Phase                                    │
│        handler.Execute(ctx, oldCluster, newCluster, version)                   │
│    } else {                                                                     │
│        // manifest 模式 → YamlComponentExecutor                                  │
│        pkg, _ := manifestStore.GetComponentManifests(ctx, node.Name, node.Version)│
│        applier.ApplyComponent(ctx, pkg)                                         │
│    }                                                                            │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 5.1.4 Catalog 与 ReleaseImage 的分工

| 维度 | ReleaseImage (声明层) | DeclarativeUpgradeCatalog (映射层) |
|------|----------------------|-----------------------------------|
| **定义位置** | OCI Bundle 中的 `release.yaml` | `pkg/upgrade/catalog.go` 中的 Go `var` |
| **变更频率** | 每次发布变更 (版本号、组件列表) | 很少变更 (仅新增组件时添加条目) |
| **内容** | 组件名 + 版本号 + inline handler (如声明) | 组件名 → 执行模式 + handler + manifest 路径 + legacy Phase |
| **作用** | 声明"升级到什么版本" | 映射"用什么方式执行升级" |
| **关系** | ReleaseImage 组件名是 Catalog 的查找键 | Catalog 提供执行所需的元数据 |

> **分工原则**：ReleaseImage 声明**什么版本** (What)，Catalog 映射**怎么执行** (How)。

#### 5.1.5 Catalog 的消费者

| 消费者 | 用途 | 代码位置 |
|--------|------|---------|
| `BuildDAGFromBundle()` / `BuildInstallDAGFromBundle()` | 构建 DAG 时查找组件的执行模式和 inline handler | `pkg/upgrade/bundle.go` / `pkg/topology/build.go` |
| `InlineUpgradeHandlers()` | 返回所有 inline handler 名称列表，供 ComponentFactory 注册 | `pkg/upgrade/catalog.go:125` |
| `Scheduler.executeComponent()` | 根据 Catalog 的 Mode 选择 inline/manifest 执行器 | `pkg/dagexec/scheduler.go` |
| `PhaseFlow.CalculatePhase()` | 旧路径中用 `LegacyPhase` 字段判断是否跳过 legacy Phase | `pkg/phaseframe/phases/phase_flow.go` |

### 5.2 DeclarativeInstallCatalog

#### 5.2.1 复用 UpgradeComponentSpec — 不新增类型

分析 `InstallComponentSpec` 与现有 `UpgradeComponentSpec` 的字段：

| 字段 | `UpgradeComponentSpec` | `InstallComponentSpec` (原提议) | 差异 |
|------|----------------------|-------------------------------|------|
| `Name` | string | string | 无 |
| `Version` | string | string | 无 |
| `Mode` | `UpgradeExecutionMode` ("manifest"/"inline") | `InstallExecutionMode` ("manifest"/"inline") | **值相同，仅类型名不同** |
| `ManifestPath` | string | string | 无 |
| `InlineHandler` | string | string | 无 |
| `LegacyPhase` | string | 缺失 | 安装也需要 (映射到 DeployPhases) |

**结论**：两个结构体字段语义完全一致，`Mode` 的值 ("manifest"/"inline") 描述的是执行机制而非操作类型 (安装/升级)。**应直接复用 `UpgradeComponentSpec`，不新增 `InstallComponentSpec` 和 `InstallExecutionMode`**。

**方案**：将 `UpgradeComponentSpec` 重命名为中性的 `ComponentSpec`，`UpgradeExecutionMode` 重命名为 `ExecutionMode`，同时保留 `UpgradeComponentSpec` 作为类型别名以保证向后兼容：

```go
// pkg/upgrade/catalog.go

// ExecutionMode 描述组件的执行方式 (安装和升级通用) 🔄重构
// 统一替代 UpgradeExecutionMode
type ExecutionMode string

const (
    ExecutionManifest ExecutionMode = "manifest"
    ExecutionInline   ExecutionMode = "inline"
)

// ComponentSpec 映射 ReleaseImage 组件名到执行模式 (安装和升级通用) 🔄重构
// 统一替代 UpgradeComponentSpec
type ComponentSpec struct {
    // 组件名称 (ReleaseImage install/upgrade components[].name)
    Name string

    // 组件版本
    Version string

    // 执行模式: manifest | inline
    Mode ExecutionMode

    // Manifest 路径 (mode=manifest 时)
    ManifestPath string

    // Legacy Phase 名称 (映射到 PhaseFlow 的 Phase，用于双轨共存时跳过)
    LegacyPhase string

    // Inline handler 名称 (mode=inline 时，ComponentFactory 注册键)
    InlineHandler string
}

// UpgradeComponentSpec 升级组件规格 (类型别名，向后兼容)
type UpgradeComponentSpec = ComponentSpec

// UpgradeExecutionMode 升级执行模式 (类型别名，向后兼容)
type UpgradeExecutionMode = ExecutionMode
```

**向后兼容性**：

类型别名 (`type UpgradeComponentSpec = ComponentSpec`) 确保现有代码无需修改：

```go
// 现有代码无需修改
var specs []UpgradeComponentSpec  // 类型别名，等价于 ComponentSpec

// 可以直接赋值和转换
specs = []ComponentSpec{
    {Name: "etcd", Mode: ExecutionInline, InlineHandler: "EnsureEtcdUpgrade"},
}

// 常量也可互换使用
var mode ExecutionMode = ExecutionInline
var legacyMode UpgradeExecutionMode = mode  // 类型相同，可以直接赋值
```

**DeclarativeUpgradeCatalog 保持不变**：

```go
// pkg/upgrade/catalog.go — 现有代码无需修改

var DeclarativeUpgradeCatalog = []ComponentSpec{  // 类型名从 UpgradeComponentSpec 改为 ComponentSpec
    {Name: "pre-upgrade-resources", Mode: ExecutionInline, InlineHandler: "EnsurePreUpgradeResources", LegacyPhase: "EnsurePreUpgradeResources"},
    {Name: "provider", Mode: ExecutionManifest, ManifestPath: "provider/v1.0.0/component.yaml", LegacyPhase: "EnsureProviderSelfUpgrade"},
    {Name: "bkeagent", Mode: ExecutionInline, InlineHandler: "EnsureAgentUpgrade", LegacyPhase: "EnsureAgentUpgrade"},
    // ... 其余组件
}
```

> **重命名影响**：`UpgradeComponentSpec` → `ComponentSpec`，`UpgradeExecutionMode` → `ExecutionMode`。现有引用处 (catalog.go、build.go、scheduler.go 等) 可选择性替换类型名，但**字段和方法不变**。通过类型别名，现有代码无需修改即可编译通过。

#### 5.2.2 DeclarativeInstallCatalog 定义

```go
// pkg/upgrade/catalog.go

// DeclarativeInstallCatalog 安装组件目录 🆕新增
// 复用 ComponentSpec (与 DeclarativeUpgradeCatalog 同一类型)
var DeclarativeInstallCatalog = []ComponentSpec{
    // inline 模式：复用现有 Phase 实现
    {Name: "bkeagent",          Mode: ExecutionInline, InlineHandler: "EnsureBKEAgent",       LegacyPhase: "EnsureBKEAgent"},
    {Name: "nodes-env",         Mode: ExecutionInline, InlineHandler: "EnsureNodesEnv",       LegacyPhase: "EnsureNodesEnv"},
    {Name: "cluster-api-obj",   Mode: ExecutionInline, InlineHandler: "EnsureClusterAPIObj",  LegacyPhase: "EnsureClusterAPIObj"},
    {Name: "certs",             Mode: ExecutionInline, InlineHandler: "EnsureCerts",          LegacyPhase: "EnsureCerts"},
    {Name: "load-balance",      Mode: ExecutionInline, InlineHandler: "EnsureLoadBalance",    LegacyPhase: "EnsureLoadBalance"},
    {Name: "kubernetes-master",  Mode: ExecutionInline, InlineHandler: "EnsureMasterInit",     LegacyPhase: "EnsureMasterInit"},
    {Name: "kubernetes-worker",  Mode: ExecutionInline, InlineHandler: "EnsureWorkerJoin",     LegacyPhase: "EnsureWorkerJoin"},
    {Name: "nodes-postprocess", Mode: ExecutionInline, InlineHandler: "EnsureNodesPostProcess",LegacyPhase: "EnsureNodesPostProcess"},
    {Name: "agent-switch",      Mode: ExecutionInline, InlineHandler: "EnsureAgentSwitch",    LegacyPhase: "EnsureAgentSwitch"},

    // manifest 模式：YAML 清单应用
    // 对应 ReleaseImage install.components 中无 inline handler 的组件
    {Name: "kube-proxy", Mode: ExecutionManifest, ManifestPath: "kube-proxy/{version}/component.yaml"},
    {Name: "coredns",    Mode: ExecutionManifest, ManifestPath: "coredns/{version}/component.yaml"},
    {Name: "calico",     Mode: ExecutionManifest, ManifestPath: "calico/{version}/component.yaml"},
    {Name: "provider",   Mode: ExecutionManifest, ManifestPath: "provider/{version}/component.yaml"},
}
```

**ReleaseImage install.components 与 DeclarativeInstallCatalog 的对应关系**：

| ReleaseImage 组件 | Catalog 模式 | Handler / ManifestPath |
|------------------|-------------|----------------------|
| bkeagent | inline | `EnsureBKEAgent` |
| kubernetes-master | inline | `EnsureMasterInit` |
| kubernetes-worker | inline | `EnsureWorkerJoin` |
| etcd | (嵌入 MasterInit) | 由 kubeadm init 创建 |
| containerd | (嵌入 NodesEnv) | 由 K8sEnvInit runtime scope 安装 |
| calico | manifest | `calico/{version}/component.yaml` |
| kubeproxy | manifest | `kube-proxy/{version}/component.yaml` |
| coredns | manifest | `coredns/{version}/component.yaml` |
| provider | manifest | `provider/{version}/component.yaml` |

> **注意**：etcd 和 containerd 不在 Catalog 中，因为它们在安装时嵌入在其他组件中（etcd 由 `EnsureMasterInit` 通过 kubeadm init 创建，containerd 由 `EnsureNodesEnv` 通过 K8sEnvInit 安装）。详见 §5.2.1。

#### 5.2.3 复用的收益

| 维度 | 新增 `InstallComponentSpec` (原方案) | 复用 `ComponentSpec` (改进方案) |
|------|-------------------------------------|--------------------------------|
| 类型定义 | 新增 `InstallComponentSpec` + `InstallExecutionMode` | **零新增** (复用现有类型) |
| 常量定义 | 新增 `InstallExecutionManifest` + `InstallExecutionInline` | **零新增** (复用 `ExecutionManifest` + `ExecutionInline`) |
| Catalog 查找函数 | 需为 Install 写一套 `findInInstallCatalog()` | **复用** `findInCatalog()` (泛型于 install/upgrade) |
| `InlineHandlers()` 函数 | 需新增 `InlineInstallHandlers()` | **复用** `InlineUpgradeHandlers()` 逻辑 (改为 `InlineHandlers(catalog)`) |
| 新增组件 | 需在两个 Catalog 中各加一条 | 按需在对应 Catalog 中添加 (install/upgrade handler 不同是正常的) |
| 代码维护 | 两套类型定义需保持同步 | **一套类型**，无需同步 |

> **注意**：同一组件在安装和升级 Catalog 中的 `InlineHandler` 可能不同 (如 `kubernetes-master`: 安装=`EnsureMasterInit`，升级=`EnsureMasterUpgrade`)。复用 `ComponentSpec` 类型不影响这一点 — 两个 Catalog 是独立的 `[]ComponentSpec` 切片，只是元素类型相同。

### 5.3 安装组件与升级组件目录对比

基于代码库实际实现核对的完整组件列表：

| 组件名称 | ReleaseImage install | ReleaseImage upgrade | 安装 handler (DeployPhases) | 升级 handler | 执行模式 | 说明 |
|---------|---------------------|---------------------|----------------------------|-------------|---------|------|
| **bkeagent** | ✅ | ✅ inline: EnsureAgentUpgrade | `EnsureBKEAgent` | `EnsureAgentUpgrade` | inline | Agent 推送/升级 |
| **kubernetes-master** | ✅ | ✅ inline: EnsureMasterUpgrade | `EnsureMasterInit` | `EnsureMasterUpgrade` | inline | Master 初始化/升级 |
| **kubernetes-worker** | ✅ | ✅ inline: EnsureWorkerUpgrade | `EnsureWorkerJoin` | `EnsureWorkerUpgrade` | inline | Worker 加入/升级 |
| **etcd** | ✅ | ✅ inline: EnsureEtcdUpgrade | (嵌入 MasterInit) | `EnsureEtcdUpgrade` | inline | etcd 由 kubeadm init 创建 |
| **containerd** | ✅ | ✅ inline: EnsureContainerdUpgrade | (嵌入 NodesEnv) | `EnsureContainerdUpgrade` | inline | containerd 由 K8sEnvInit runtime scope 安装 |
| **provider** | ✅ | ✅ (无 inline) | (嵌入 MasterInit) | `EnsureProviderSelfUpgrade` | manifest | provider 由 kubeadm init 部署 |
| **calico** | ✅ | ✅ (无 inline) | (嵌入 AddonDeploy) | (manifest) | manifest | CNI 插件，安装走 EnsureAddonDeploy |
| **kubeproxy** | ✅ | ✅ (无 inline) | (嵌入 AddonDeploy) | (manifest) | manifest | kube-proxy，安装走 EnsureAddonDeploy |
| **coredns** | ✅ | ✅ (无 inline) | (嵌入 AddonDeploy) | (manifest) | manifest | CoreDNS，安装走 EnsureAddonDeploy |
| **nodes-env** | ❌ | ❌ | `EnsureNodesEnv` | - | inline | 节点环境准备 (含 containerd) |
| **cluster-api-obj** | ❌ | ❌ | `EnsureClusterAPIObj` | - | inline | Cluster API 对象创建 |
| **certs** | ❌ | ❌ | `EnsureCerts` | - | inline | 证书生成 |
| **load-balance** | ❌ | ❌ | `EnsureLoadBalance` | - | inline | HA 负载均衡 (haproxy/keepalived) |
| **nodes-postprocess** | ❌ | ❌ | `EnsureNodesPostProcess` | - | inline | 后置脚本处理 |
| **agent-switch** | ❌ | ❌ | `EnsureAgentSwitch` | - | inline | Agent 监听切换 |
| **pre-upgrade-resources** | ❌ | ✅ inline: (无) | - | `EnsurePreUpgradeResources` | inline | 升级前资源预创建 |

**图例说明**：
- ✅ = ReleaseImage 中声明的组件
- ❌ = ReleaseImage 中未声明 (仅在 PhaseFlow 中存在)
- (嵌入 XXX) = 安装时嵌入在其他 Phase 中，无独立 handler
- (manifest) = 通过 bke-manifests YAML 清单部署，无 inline handler

#### 5.3.1 containerd 的安装路径详细说明

containerd 在安装和升级中采用不同的路径：

**安装路径** (嵌入 `EnsureNodesEnv` Phase)：

```txt
EnsureNodesEnv.Execute()
  → CheckOrInitNodesEnv()
    → buildEnvCommand() → BuildCommonEnvCommand()
      → 创建 BKEAgent Command CR (类型: CommandBuiltIn)
        → BKEAgent 执行 K8sEnvInit 插件:
          → scope 包含 "runtime":
            → initRuntime() → downloadContainerd()
              → 下载 containerd-{version}-linux-{arch}.tar.gz
              → 解压 + 安装 + 启动 systemd 服务
```

| 维度 | 说明 |
|------|------|
| **安装 Phase** | `EnsureNodesEnv` (DeployPhases #1) |
| **安装机制** | BKEAgent `K8sEnvInit` 插件 `runtime` scope → `downloadContainerd()` |
| **代码位置** | `pkg/phaseframe/phases/ensure_nodes_env.go` → `pkg/command/env.go` → `pkg/job/builtin/kubeadm/env/init.go` |
| **是否独立 Phase** | **否** — containerd 安装嵌入在 `EnsureNodesEnv` 的环境初始化命令中 |
| **版本来源** | `BKECluster.Spec.ClusterConfig.Cluster.ContainerdVersion` (从 Spec 读取) |

**升级路径** (独立 `EnsureContainerdUpgrade` Phase)：

```txt
EnsureContainerdUpgrade.Execute()
  → 创建 BKEAgent Command CR (类型: CommandBuiltIn)
    → BKEAgent 执行 Kubeadm 插件:
      → phase=UpgradeContainerd
        → upgradeContainerd()
          → 下载新版 containerd 二进制
          → 停止 containerd 服务
          → 替换二进制文件
          → 启动 containerd 服务
          → 等待服务就绪
```

| 维度 | 说明 |
|------|------|
| **升级 Phase** | `EnsureContainerdUpgrade` (DeclarativeInlineUpgradePhases) |
| **升级机制** | BKEAgent `Kubeadm` 插件 `UpgradeContainerd` phase |
| **代码位置** | `pkg/phaseframe/phases/ensure_containerd_upgrade.go` → `pkg/job/builtin/kubeadm/kubeadm.go` |
| **是否独立 Phase** | **是** — 独立的升级 Phase |
| **版本来源** | `ReleaseImage.upgrade.components[containerd].version` (从 ReleaseImage 读取) |

**安装与升级路径不一致的原因**：

| 维度 | 安装 | 升级 |
|------|------|------|
| **执行时机** | 节点环境准备阶段 (首次安装) | 集群已运行，需要滚动升级 |
| **执行方式** | 通过 K8sEnvInit 插件批量初始化节点环境 | 通过 Kubeadm 插件单独升级 containerd |
| **是否需要 drain** | 否 (节点尚未加入集群) | 是 (需要驱逐 Pod) |
| **是否重启服务** | 是 (首次启动) | 是 (停止 → 替换 → 启动) |

**DAG 化影响**：

在 DAG 化安装路径中，containerd 需要作为独立组件声明：

```yaml
# ReleaseImage install.components 新增 containerd
install:
  components:
    - name: containerd
      version: v2.1.1
      inline:
        handler: EnsureContainerdInstall  # 🆕新增: 独立的安装 handler
        version: v1.0.0
```

或者保持嵌入方式，但需要确保 `EnsureNodesEnv` 在 DAG 中正确执行：

```go
// DeclarativeInstallCatalog 中 containerd 的处理方式
// 方案 A: 独立组件 (推荐)
{Name: "containerd", Mode: ExecutionInline, InlineHandler: "EnsureContainerdInstall"}

// 方案 B: 保持嵌入 (不推荐，违反声明式原则)
// containerd 不在 DeclarativeInstallCatalog 中，由 EnsureNodesEnv 内部处理
```

### 5.4 ComponentFactory 注册扩展

```go
// pkg/componentfactory/registry.go

func registerInstallHandlers() {
    // 安装 handler 注册（复用现有 Phase 构造函数）
    // 对应 DeclarativeInstallCatalog 中的 inline 模式组件
    RegisterInlineHandler("EnsureBKEAgent",         v1alpha1.InlineHandlerVersion, phases.NewEnsureBKEAgent)
    RegisterInlineHandler("EnsureNodesEnv",         v1alpha1.InlineHandlerVersion, phases.NewEnsureNodesEnv)
    RegisterInlineHandler("EnsureClusterAPIObj",    v1alpha1.InlineHandlerVersion, phases.NewEnsureClusterAPIObj)
    RegisterInlineHandler("EnsureCerts",            v1alpha1.InlineHandlerVersion, phases.NewEnsureCerts)
    RegisterInlineHandler("EnsureLoadBalance",      v1alpha1.InlineHandlerVersion, phases.NewEnsureLoadBalance)
    RegisterInlineHandler("EnsureMasterInit",       v1alpha1.InlineHandlerVersion, phases.NewEnsureMasterInit)
    RegisterInlineHandler("EnsureWorkerJoin",       v1alpha1.InlineHandlerVersion, phases.NewEnsureWorkerJoin)
    RegisterInlineHandler("EnsureNodesPostProcess", v1alpha1.InlineHandlerVersion, phases.NewEnsureNodesPostProcess)
    RegisterInlineHandler("EnsureAgentSwitch",      v1alpha1.InlineHandlerVersion, phases.NewEnsureAgentSwitch)
    
    // 升级 handler 已注册（现有）
    // RegisterInlineHandler("EnsureEtcdUpgrade", ...)
    // RegisterInlineHandler("EnsureMasterUpgrade", ...)
    // ...
}
```

**DeployPhases 中未注册的 Handler 说明**：

| Handler | 是否注册 | 原因 |
|---------|---------|------|
| **EnsureMasterJoin** | ❌ 不注册 | Master 扩容场景由 `EnsureMasterInit` 幂等处理（检查已有 Master 数量，决定执行 init 还是 join）。DAG 化后 `kubernetes-master` 组件统一使用 `EnsureMasterInit`，无需单独的 join handler。 |
| **EnsureAddonDeploy** | ❌ 不注册 | Addon 组件（calico、kubeproxy、coredns）在 DAG 化中使用 **manifest 模式**（YAML 清单应用），由 `YamlInstaller` 处理，不需要 inline handler。`DeclarativeInstallCatalog` 中这些组件声明为 `Mode: ExecutionManifest`。 |

**Handler 与组件的完整对应关系**：

| 组件 | DeclarativeInstallCatalog 模式 | Handler / Manifest |
|------|-------------------------------|-------------------|
| bkeagent | inline | `EnsureBKEAgent` |
| nodes-env | inline | `EnsureNodesEnv` |
| cluster-api-obj | inline | `EnsureClusterAPIObj` |
| certs | inline | `EnsureCerts` |
| load-balance | inline | `EnsureLoadBalance` |
| kubernetes-master | inline | `EnsureMasterInit` (init/join 统一) |
| kubernetes-worker | inline | `EnsureWorkerJoin` |
| nodes-postprocess | inline | `EnsureNodesPostProcess` |
| agent-switch | inline | `EnsureAgentSwitch` |
| kube-proxy | manifest | `kube-proxy/{version}/component.yaml` |
| coredns | manifest | `coredns/{version}/component.yaml` |
| calico | manifest | `calico/{version}/component.yaml` |
| provider | manifest | `provider/{version}/component.yaml` |

## 6. 安装 DAG 构建设计

### 6.1 设计思路

安装 DAG 构建的核心思路是**复用升级 DAG 的构建逻辑**，通过 `DecisionInstall` 区分安装与升级场景。与升级的关键差异在于 VersionContext：安装时 Current 全部为空 (无已安装组件)，Target 来自 ReleaseImage；升级时 Current 来自当前 ReleaseImage bundle，Target 来自目标 ReleaseImage bundle。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              安装 DAG 构建设计思路                                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  升级 DAG 构建 (现有):                                                          │
│    BuildVersionContextForUpgrade(targetBundle, currentBundle, bc)               │
│      → Current 有值 (来自 currentBundle 或 Status)                              │
│      → Target 有值 (来自 targetBundle)                                           │
│      → Decide: current != target → DecisionUpgrade                              │
│    BuildDAGFromBundle(bundle, resolver)                                          │
│      → 遍历 upgrade.components 构建 DAG (调用 topology.BuildDAG)                │
│                                                                                 │
│  安装 DAG构建 (新增):                                                           │
│    BuildVersionContextForInstall(targetBundle)                                   │
│      → Current 全空 (全新安装，无已安装组件)                                    │
│      → Target 有值 (来自 install.components)                                    │
│      → Decide: current="" + target!="" → DecisionInstall ★                      │
│    BuildInstallDAGFromBundle(bundle, resolver)                                   │
│      → 遍历 install.components 构建 DAG (复用 topology.BuildDAG 逻辑)          │
│      → 使用统一的 ReleaseImageComponent 类型 (无需转换)                          │
│                                                                                 │
│  复用点:                                                                         │
│  ① topology.BuildDAG — 拓扑排序 + 依赖解析逻辑完全复用 (原 BuildUpgradeDAG, type alias) │
│  ② Scheduler.ExecuteDAG — 并行执行 + 状态更新逻辑完全复用                        │
│  ③ ExecutorRegistry — inline/yaml 执行器分发逻辑完全复用                        │
│  ④ ComponentFactory — handler 注册和解析逻辑完全复用                            │
│  ⑤ DeclarativeUpgradeStatus — 断点续传状态追踪完全复用                          │
│                                                                                 │
│  新增点:                                                                         │
│  ① DecisionInstall — 版本决策新增安装场景 (current 空 + target 有值)            │
│  ② BuildVersionContextForInstall — 安装专用 VC 构建 (Current 全空)             │
│  ③ BuildInstallDAGFromBundle — 从 install.components 构建 DAG                  │
│  ④ DeclarativeInstallCatalog — 安装组件目录 (handler 与升级不同)               │
│  ⑤ 不再需要 install-ready annotation — Feature Gate 开启即代表 ReleaseImage 就绪 ★    │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 6.2 VersionContext 扩展

```go
// pkg/upgrade/context.go

// Decision 扩展 🆕新增 DecisionInstall
type Decision string

const (
    DecisionSkip    Decision = "Skip"
    DecisionUpgrade Decision = "Upgrade"
    DecisionInstall Decision = "Install"  // 🆕新增
)

// Decide 扩展：安装场景判断
func Decide(vc *VersionContext, name string) Decision {
    if vc == nil {
        return DecisionUpgrade  // nil VC = 不阻塞
    }
    
    // 安装场景：current 为空，target 有值
    if vc.Current[name] == "" && vc.Target[name] != "" {
        return DecisionInstall
    }
    
    // 升级场景：current != target
    if vc.Current[name] == vc.Target[name] || vc.Target[name] == "" {
        return DecisionSkip
    }
    return DecisionUpgrade
}

// NeedsExecution 扩展
func NeedsExecution(vc *VersionContext, name string) bool {
    return Decide(vc, name) != DecisionSkip
}
```

### 6.3 安装 DAG 构建器

```go
// pkg/upgrade/bundle.go

// InstallComponentsFromBundle 从 ReleaseImage bundle 提取安装组件
func InstallComponentsFromBundle(bundle *releasemanifest.Bundle) ([]apiv1.ReleaseImageComponent, error) {
    if bundle.Release.Spec.Install == nil {
        return nil, fmt.Errorf("release image has no install components")
    }
    components := bundle.Release.Spec.Install.Components
    if len(components) == 0 {
        return nil, fmt.Errorf("release image install components is empty")
    }
    return components, nil
}

// BuildInstallDAGFromBundle 从 bundle 构建安装 DAG 🆕新增
func BuildInstallDAGFromBundle(
    bundle *releasemanifest.Bundle,
    resolve topology.DependencyResolver,
) (*topology.UpgradeDAG, error) {
    // 1. 提取安装组件
    installComponents, err := InstallComponentsFromBundle(bundle)
    if err != nil {
        return nil, err
    }
    
    // 2. 复用 topology.BuildDAG 构建组件 DAG
    //    ★ topology.BuildUpgradeDAG 通过 type alias 重命名为 topology.BuildDAG (通用名称)
    //    ★ topology.UpgradeDAG 通过 type alias 重命名为 topology.ComponentDAG (通用名称)
    //    ★ 不再区分 "Upgrade DAG" 和 "Install DAG" — 同一构建逻辑, 不同组件来源
    //    ★ 直接构建所有 install.components 的 DAG，不需要排除任何组件
    //    ★ 组件的跳过逻辑由执行时的 VersionContext.Decide() 与组件的 condition/NodeFilter 决定
    return topology.BuildDAG(installComponents, resolve)
}
```

**`UpgradeDAG` → `ComponentDAG` 和 `BuildUpgradeDAG` → `BuildDAG`（type alias 兼容）**：

现有代码中 `topology.UpgradeDAG`（`pkg/topology/component.go:45`）和 `topology.BuildUpgradeDAG`（`pkg/topology/build.go:25`）名为 "Upgrade"，但实际上是通用的 DAG 类型和构建器——只接收组件列表和依赖解析器，不感知操作类型（安装/升级）。安装 DAG 和升级 DAG 使用相同的构建逻辑，仅组件来源不同（`install.components` vs `upgrade.components`）。

通过 **Go type alias** 实现零破坏重命名——新代码使用通用名称，现有代码无需修改：

```go
// pkg/topology/component.go — 新增 type alias, 原定义保留

// UpgradeDAG 原定义保留不变
type UpgradeDAG struct {
    graph *Graph
    nodes map[string]*ComponentNode
}

// ComponentDAG is the preferred name for UpgradeDAG.
// Type alias — identical type, not a new type.
// All methods on UpgradeDAG are available on ComponentDAG.
type ComponentDAG = UpgradeDAG

// NewComponentDAG creates an empty component DAG.
// Alias of NewUpgradeDAG — new code uses this; existing code can continue using NewUpgradeDAG.
func NewComponentDAG() *ComponentDAG {
    return NewUpgradeDAG()
}
```

```go
// pkg/topology/build.go — 新增 alias 函数

// BuildDAG builds a component DAG from ReleaseImage components.
// Alias of BuildUpgradeDAG — preferred name for new code.
// Used by both install and upgrade paths.
func BuildDAG(components []cvv1alpha1.ReleaseImageUpgradeComponent, resolve DependencyResolver) (*ComponentDAG, error) {
    return BuildUpgradeDAG(components, resolve)
}
```

**兼容性保证**：

| 维度 | 影响 | 说明 |
|------|------|------|
| **现有代码** | **零修改** | `UpgradeDAG`、`NewUpgradeDAG`、`BuildUpgradeDAG` 全部保留，所有引用不变 |
| **新代码** | 使用 `ComponentDAG` / `NewComponentDAG` / `BuildDAG` | 安装 DAG 构建器等新代码使用通用名称 |
| **测试代码** | **零修改** | 现有测试使用 `UpgradeDAG`/`NewUpgradeDAG`/`BuildUpgradeDAG`，不变 |
| **API/CRD** | 无影响 | `UpgradeDAG` 是内部类型，不在 CRD 中 |
| **Type alias 语义** | 完全等价 | `ComponentDAG = UpgradeDAG` 是同一类型，不是新类型，所有方法通用 |

**重命名影响范围**：

| 调用方 | 原调用 | 新代码调用 | 文件 |
|--------|--------|-----------|------|
| `upgrade.BuildDAGFromBundle` | `topology.BuildUpgradeDAG(...)` | `topology.BuildDAG(...)` | `pkg/upgrade/bundle.go:29` |
| `upgrade.BuildInstallDAGFromBundle` (新增) | — | `topology.BuildDAG(...)` | `pkg/upgrade/bundle.go` (新增) |
| `upgrade.BuildDAGFromReleaseImage` | `topology.BuildUpgradeDAG(...)` | `topology.BuildDAG(...)` | `pkg/upgrade/releaseimage.go:30` |
| `dagexec.SchedulerSkipTest` | `topology.BuildUpgradeDAG(...)` | 可保持不变或迁移 | `pkg/dagexec/scheduler_skip_test.go:68` |
| `topology.BuildUpgradeDAGTest` | `BuildUpgradeDAG(...)` | 可保持不变或迁移 | `pkg/topology/build_test.go:21,49` |
| `dagexec.Scheduler.ExecuteDAG` | `dag *topology.UpgradeDAG` | `dag *topology.ComponentDAG` | `pkg/dagexec/scheduler.go:102,150` |
| 测试文件 `NewUpgradeDAG()` | `topology.NewUpgradeDAG()` | 可保持不变或迁移 | 6 个测试文件 |

> **设计原则**：渐进迁移——新代码使用 `ComponentDAG`/`BuildDAG`，旧代码可在后续版本逐步迁移到新名称，不需要一次性全量替换。type alias 保证两个名称完全等价。

### 6.4 安装 VersionContext 构建

```go
// pkg/upgrade/build_release.go

// BuildVersionContextForInstall 为安装场景构建 VersionContext 🆕新增
func BuildVersionContextForInstall(
    targetBundle *releasemanifest.Bundle,
) *VersionContext {
    vc := NewVersionContext()
    
    // 安装场景：Current 全部为空（全新安装），Target 来自 ReleaseImage
    // ★ 直接构建所有 install.components 的 Target，不需要排除任何组件
    // ★ 组件的跳过逻辑由执行时的 Decide() 决定
    if targetBundle.Release.Spec.Install != nil {
        for _, comp := range targetBundle.Release.Spec.Install.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }
    
    // Current 全部为空 → Decide 返回 DecisionInstall
    return vc
}
```

> **简化设计**：VersionContext 构建时直接包含所有 install.components，不在构建时进行过滤。组件的跳过逻辑完全由执行时的 `Decide()` 与 `condition`/`NodeFilter` 决定：
> - 全新安装：所有组件 `Current="" && Target!=""` → `DecisionInstall` → 全部执行
> - 纳管场景：`manage` 组件先探测版本填充 `Current`，后续组件根据 `Current` 与 `Target` 的比较结果决定
> - 组件可以包含在 ReleaseImage 的 install.components 中，但在执行时通过 `condition`（Go Template 表达式，根据集群运行时状态判断）或 `NodeFilter`（节点级过滤器）进行过滤跳过

## 7. 安装 DAG 执行设计

### 7.1 设计思路

安装 DAG 执行的核心思路是**在现有 PhaseFlow 的执行入口中增加 DAG 安装分支**，通过两重门控 (Feature Gate + 全新安装判定) 决定是否走 DAG 路径。未满足门控条件时回退到 Legacy PhaseFlow，保证向后兼容。

> **设计变更**：原设计包含三重门控（Feature Gate + 全新安装判定 + install-ready annotation），现简化为两重门控。Feature Gate 开启后强制要求 ReleaseImage 必须就绪——`executeInstallDAG` 中 `resolveInstallBundle` 失败时 Requeue 等待，不回退 Legacy PhaseFlow。与升级路径 `shouldUseDeclarativeUpgrade` 行为一致。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              安装 DAG 执行设计思路                                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  执行入口 (executePhaseFlow) 三路分发:                                           │
│                                                                                 │
│  ① shouldUseDeclarativeUpgrade? → executeUpgradeDAG (现有升级路径)             │
│     门控: upgrade-ready annotation + Feature Gate                               │
│                                                                                 │
│  ② shouldUseDeclarativeInstall? → executeInstallDAG (新增安装路径) ★           │
│     门控: Feature Gate + 全新安装                                                │
│     ReleaseImage 未就绪时 Requeue 等待 (不回退 Legacy)                          │
│                                                                                 │
│  ③ 默认 → PhaseFlow (Legacy 路径)                                               │
│     适用: Feature Gate 未启用 / 非全新安装 (扩容/纳管/删除等场景)               │
│                                                                                 │
│  两重门控设计原因:                                                               │
│                                                                                 │
│  门控 1 — Feature Gate (DeclarativeInstallEnabled):                              │
│    原因: 渐进迁移，默认关闭确保生产稳定                                          │
│    效果: 关闭时所有安装走 Legacy PhaseFlow                                       │
│    前提: Feature Gate 开启 = ReleaseImage 必须就绪 (用户已准备好 ReleaseImage)  │
│                                                                                 │
│  门控 2 — 全新安装判定 (BKECluster.Status.Phase 为空或 Init):                     │
│    原因: DAG 安装路径仅面向全新安装，扩容/纳管/删除等场景不适用                  │
│    Status.Phase 是 BKECluster CR 的 Status 子资源字段:                           │
│      空 = 未初始化 (全新创建)                                                    │
│      PhaseInit = 初始化中                                                       │
│      其他值 = 已部署/升级中/删除中等 (不走安装 DAG)                              │
│    效果: 仅全新安装可走 DAG，扩容仍走 PhaseFlow Scale Phase                      │
│                                                                                 │
│  executeInstallDAG 执行流程:                                                     │
│    1. resolveInstallBundle → 从 ReleaseImage 解析 OCI Bundle                    │
│       未就绪 → RequeueAfter 30s 等待 (不回退 Legacy)                            │
│    2. BuildVersionContextForInstall → 构建 VC (Current 空, Target 来自 RI)     │
│    3. ApplyVersionContextTargetsToClusterSpec → 同步版本到 BKECluster.Spec      │
│       (供 BKEAgent 读取 BkeConfig.Cluster.KubernetesVersion 等)                │
│    4. BuildInstallDAGFromBundle → 构建安装 DAG (拓扑排序)                       │
│    5. ComponentFactory + 注册安装 handler                                        │
│    6. NewScheduler (复用升级框架)                                                │
│    7. ExecuteDAG → 并行执行安装组件                                              │
│    8. 安装完成 → 更新 Status                                                     │
│                                                                                 │
│  与升级执行的关键差异:                                                           │
│  ① VersionContext: 安装 Current 全空 vs 升级 Current 有值                       │
│  ② Decision: 安装 DecisionInstall vs 升级 DecisionUpgrade                       │
│  ③ Catalog: 安装 DeclarativeInstallCatalog vs 升级 DeclarativeUpgradeCatalog   │
│  ④ Handler: 安装 EnsureMasterInit vs 升级 EnsureMasterUpgrade                  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.2 执行入口

#### 7.2.1 三路分发统一设计

`executePhaseFlow()` 是 BKEClusterReconciler 的核心分发入口，统一管理 DAG 升级、DAG 安装、PhaseFlow 兜底三条路径。设计原则是**优先匹配 DAG 路径，未命中则走 PhaseFlow 兜底**，确保 Feature Gate 关闭时行为完全不变。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              executePhaseFlow 三路分发统一设计                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  executePhaseFlow(ctx, phaseCtx, oldCluster, newCluster)                        │
│     │                                                                           │
│     │  1. DAG 升级路径 (优先匹配)                                                │
│     │     shouldUseDeclarativeUpgrade(newCluster)?                              │
│     │     ├─ true → executeUpgradeDAG(...) → return                            │
│     │     └─ false → 继续                                                      │
│     │                                                                           │
│     │  2. DAG 安装路径 (次优先匹配)                                              │
│     │     shouldUseDeclarativeInstall(newCluster)?                               │
│     │     ├─ true → executeInstallDAG(...) → return                            │
│     │     │           (内部清理上次 DAG 残留状态后执行)                          │
│     │     └─ false → 继续                                                      │
│     │                                                                           │
│     │  3. PhaseFlow 兜底路径                                                    │
│     │     ├─ 集群操作类 (Delete/Pause/DryRun/Manage/Reset):                     │
│     │     │   走 CommonPhases + 对应专用 Phase                                   │
│     │     ├─ 全新安装 (Feature Gate 未启用):                                     │
│     │     │   走 CommonPhases + DeployPhases (从 BKECluster.Spec 读取版本)      │
│     │     ├─ 集群扩容 (新增 Master/Worker):                                    │
│     │     │   走 CommonPhases + ScalePhases (部分 Phase，非完整安装)             │
│     │     └─ 升级 (Feature Gate 未启用):                                        │
│     │         走 CommonPhases + UpgradePhases (从 BKECluster.Spec 读取版本)    │
│     │                                                                           │
│     ▼                                                                           │
│  执行完成                                                                       │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 7.2.2 完整代码实现

```go
// controllers/capbke/bkecluster_controller.go

func (r *BKEClusterReconciler) executePhaseFlow(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // ─── DAG 升级路径 (优先匹配) ───
    if r.shouldUseDeclarativeUpgrade(newCluster) {
        return r.executeUpgradeDAG(ctx, phaseCtx, oldCluster, newCluster)
    }

    // ─── DAG 安装路径 (次优先匹配) ───
    if r.shouldUseDeclarativeInstall(newCluster) {
        return r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }

    // ─── PhaseFlow 兜底路径 ───
    // 以下场景走 PhaseFlow:
    //   1. 集群操作类 (Delete/Pause/DryRun/Manage/Reset) — 非 Install/Upgrade 操作
    //   2. 全新安装但 Feature Gate 未启用
    //   3. 集群扩容 (新增 Master/Worker 节点)
    //   4. 升级但 Feature Gate 未启用
    flow := phases.NewPhaseFlow(phaseCtx)
    return flow.Execute()
}
```

#### 7.2.3 PhaseFlow 兜底路径设计

> **定位**：PhaseFlow 是非安装/非升级场景（扩容/纳管/删除/DryRun/暂停）的兜底执行路径，后续再优化。

PhaseFlow 是 DAG 路径的兜底方案，覆盖所有 DAG 路径不适用的场景。PhaseFlow 通过 `CalculatePhase()` 动态计算需要执行的 Phase 列表。

##### Phase 列表定义

```go
// pkg/phaseframe/phases/list.go

// CommonPhases 通用 Phase (所有场景首先检查)
CommonPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureFinalizer,        // 部署任务创建
    NewEnsurePaused,           // 集群管理暂停
    NewEnsureClusterManage,    // 纳管现有集群
    NewEnsureDeleteOrReset,    // 集群删除/重置
    NewEnsureDryRun,           // DryRun 部署
}

// DeployPhases 安装 Phase (全新安装时执行)
DeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureBKEAgent,         // 推送 Agent
    NewEnsureNodesEnv,         // 节点环境准备 (containerd/系统配置)
    NewEnsureClusterAPIObj,    // ClusterAPI 对象创建
    NewEnsureCerts,            // 集群证书创建
    NewEnsureLoadBalance,      // 集群入口配置 (haproxy/keepalived)
    NewEnsureMasterInit,       // Master 初始化 (kubeadm init)
    NewEnsureMasterJoin,       // Master 加入 (kubeadm join)
    NewEnsureWorkerJoin,       // Worker 加入 (kubeadm join)
    NewEnsureAddonDeploy,      // 集群组件部署 (coredns/kube-proxy)
    NewEnsureNodesPostProcess, // 后置脚本处理
    NewEnsureAgentSwitch,      // Agent 监听切换
}

// PostDeployPhases 安装后 Phase (升级/扩容/删除时执行)
PostDeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureProviderSelfUpgrade,       // provider 自升级
    NewEnsureAgentUpgrade,              // Agent 升级
    NewEnsureContainerdUpgrade,         // Containerd 升级
    NewEnsureEtcdUpgrade,               // etcd 升级
    NewEnsureWorkerUpgrade,             // Worker 升级
    NewEnsureMasterUpgrade,             // Master 升级
    NewEnsureWorkerDelete,              // Worker 删除 (缩容)
    NewEnsureMasterDelete,              // Master 删除 (缩容)
    NewEnsureComponentUpgrade,          // openFuyao 核心组件升级
    NewEnsureClusterAPIManagerManifest, // Cluster-API Manager 部署
    NewEnsureCluster,                   // 集群健康检查
}

// DeletePhases 删除 Phase (删除/重置时执行)
DeletePhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsurePaused,           // 集群管理暂停
    NewEnsureDeleteOrReset,    // 集群删除/重置
}

// FullPhasesRegisFunc 完整 Phase 列表 (非删除场景)
FullPhasesRegisFunc = CommonPhases + DeployPhases + PostDeployPhases
```

##### 各场景的 Phase 执行列表

| 场景 | 使用的 Phase 列表 | 实际执行的 Phase | 跳过的 Phase | ClusterStatus |
|------|------------------|-----------------|-------------|---------------|
| **全新安装** | `FullPhasesRegisFunc` | CommonPhases (NeedExecute 检查) + DeployPhases (全部执行) + PostDeployPhases (NeedExecute 检查) | PostDeployPhases 中的升级/删除 Phase | `ClusterInitializing` |
| **集群升级** | `FullPhasesRegisFunc` | CommonPhases (NeedExecute 检查) + PostDeployPhases 中的升级 Phase | DeployPhases (已安装) + PostDeployPhases 中的删除 Phase | `ClusterUpgrading` |
| **集群扩容** | `FullPhasesRegisFunc` | CommonPhases (NeedExecute 检查) + PostDeployPhases 中的 Join Phase | DeployPhases (已安装) + PostDeployPhases 中的升级/删除 Phase | `ClusterMasterScalingUp` / `ClusterWorkerScalingUp` |
| **集群删除/重置** | `DeletePhases` | EnsurePaused + EnsureDeleteOrReset | 所有其他 Phase | `ClusterDeleting` |
| **集群暂停** | `FullPhasesRegisFunc` | CommonPhases 中的 EnsurePaused | 所有其他 Phase | `ClusterPaused` |
| **DryRun 模式** | `FullPhasesRegisFunc` | CommonPhases 中的 EnsureDryRun | 所有其他 Phase | `ClusterDryRun` |
| **纳管已有集群** | `FullPhasesRegisFunc` | CommonPhases 中的 EnsureClusterManage | 所有其他 Phase | `ClusterManaging` |

##### 各场景详细 Phase 执行列表

**场景 1: 全新安装 (New Install)**

```
执行 Phase 列表:
  CommonPhases (NeedExecute 检查):
    1. EnsureFinalizer        → 创建 finalizer
    2. EnsurePaused           → NeedExecute=false → 跳过
    3. EnsureClusterManage    → NeedExecute=false → 跳过
    4. EnsureDeleteOrReset    → NeedExecute=false → 跳过
    5. EnsureDryRun           → NeedExecute=false → 跳过

  DeployPhases (全部执行):
    6. EnsureBKEAgent         → 推送 bkeagent 到所有节点
    7. EnsureNodesEnv         → 安装 containerd + 系统配置
    8. EnsureClusterAPIObj    → 创建 Cluster/Machine 对象
    9. EnsureCerts            → 生成集群证书
   10. EnsureLoadBalance      → 配置 haproxy/keepalived
   11. EnsureMasterInit       → kubeadm init (首个 Master)
   12. EnsureMasterJoin       → kubeadm join (后续 Master，无新 Master 时跳过)
   13. EnsureWorkerJoin       → kubeadm join (所有 Worker)
   14. EnsureAddonDeploy      → 部署 coredns/kube-proxy
   15. EnsureNodesPostProcess → 执行后置脚本
   16. EnsureAgentSwitch      → 切换 Agent 监听

  PostDeployPhases (NeedExecute 检查):
   17. EnsureProviderSelfUpgrade       → NeedExecute=false → 跳过
   18. EnsureAgentUpgrade              → NeedExecute=false → 跳过
   19. EnsureContainerdUpgrade         → NeedExecute=false → 跳过
   20. EnsureEtcdUpgrade               → NeedExecute=false → 跳过
   21. EnsureWorkerUpgrade             → NeedExecute=false → 跳过
   22. EnsureMasterUpgrade             → NeedExecute=false → 跳过
   23. EnsureWorkerDelete              → NeedExecute=false → 跳过
   24. EnsureMasterDelete              → NeedExecute=false → 跳过
   25. EnsureComponentUpgrade          → NeedExecute=false → 跳过
   26. EnsureClusterAPIManagerManifest → NeedExecute=false → 跳过
   27. EnsureCluster                   → NeedExecute=false → 跳过
```

**场景 2: 集群升级 (Upgrade)**

```
执行 Phase 列表:
  CommonPhases (NeedExecute 检查):
    1. EnsureFinalizer        → 已存在 → 跳过
    2-5. 其他 CommonPhases    → NeedExecute=false → 跳过

  DeployPhases (NeedExecute 检查):
    6-16. DeployPhases        → NeedExecute=false (已安装) → 全部跳过

  PostDeployPhases (NeedExecute 检查):
   17. EnsureProviderSelfUpgrade       → NeedExecute=true → 执行 provider 升级
   18. EnsureAgentUpgrade              → NeedExecute=true → 执行 Agent 升级
   19. EnsureContainerdUpgrade         → NeedExecute=true → 执行 containerd 升级
   20. EnsureEtcdUpgrade               → NeedExecute=true → 执行 etcd 升级
   21. EnsureWorkerUpgrade             → NeedExecute=true → 执行 Worker 升级
   22. EnsureMasterUpgrade             → NeedExecute=true → 执行 Master 升级
   23. EnsureWorkerDelete              → NeedExecute=false → 跳过
   24. EnsureMasterDelete              → NeedExecute=false → 跳过
   25. EnsureComponentUpgrade          → NeedExecute=true → 执行核心组件升级
   26. EnsureClusterAPIManagerManifest → NeedExecute=false → 跳过
   27. EnsureCluster                   → NeedExecute=false → 跳过

注意: 如果 DAG 升级路径已完成 (DeclarativeDAGCompleted=true)，
      则跳过 DeclarativeInlineUpgradePhases 中的 Phase (避免重复执行)
```

**场景 3: 集群扩容 (Scale Up)**

```
执行 Phase 列表:
  CommonPhases (NeedExecute 检查):
    1-5. CommonPhases         → NeedExecute=false → 跳过

  DeployPhases (NeedExecute 检查):
    6. EnsureBKEAgent         → NeedExecute=true (新节点 NodeAgentPushedFlag 未设置) → 执行 ★
    7. EnsureNodesEnv         → NeedExecute=true (新节点 NodeEnvFlag 未设置) → 执行 ★
    8. EnsureClusterAPIObj    → NeedExecute=false (CAPI 对象已存在) → 跳过
    9. EnsureCerts            → NeedExecute=false (证书已存在) → 跳过
   10. EnsureLoadBalance      → NeedExecute=false (LB 已配置) → 跳过
   11. EnsureMasterInit       → NeedExecute=true (有新 Master 待加入) → 执行 ★ (Master 扩容时)
                                NeedExecute=false (无新 Master) → 跳过 (Worker 扩容时)
   12. EnsureMasterJoin       → NeedExecute=true (有新 Master 未关联 Machine) → 执行 ★ (Master 扩容时)
                                NeedExecute=false (无新 Master) → 跳过 (Worker 扩容时)
   13. EnsureWorkerJoin       → NeedExecute=true (有新 Worker 未关联 Machine) → 执行 ★ (Worker 扩容时)
                                NeedExecute=false (无新 Worker) → 跳过 (Master 扩容时)
   14. EnsureAddonDeploy      → NeedExecute=false (Addon 已部署) → 跳过
   15. EnsureNodesPostProcess → NeedExecute=true (新节点 NodePostProcessFlag 未设置) → 执行 ★
   16. EnsureAgentSwitch      → NeedExecute=false (已切换) → 跳过

  PostDeployPhases (NeedExecute 检查):
   17-20. 升级 Phase          → NeedExecute=false → 跳过 (扩容不是升级)
   21. EnsureWorkerUpgrade    → NeedExecute=false → 跳过
   22. EnsureMasterUpgrade    → NeedExecute=false → 跳过
   23. EnsureWorkerDelete     → NeedExecute=false → 跳过
   24. EnsureMasterDelete     → NeedExecute=false → 跳过
   25. EnsureComponentUpgrade → NeedExecute=false → 跳过
   26. EnsureClusterAPIManagerManifest → NeedExecute=false → 跳过
   27. EnsureCluster          → NeedExecute=false → 跳过

注意: 扩容场景下，DeployPhases 遍历全部 Phase，每个 Phase 的 NeedExecute()
      通过 BKENode.Status.StateCode 位标记判断是否有节点需要操作:
      - 新节点 (StateCode=0): 位标记未设置 → NeedExecute=true → 执行
      - 已有节点 (位标记已设置): 不影响该 Phase 的 NeedExecute (HasNodesNeedingPhase 只要有 1 个节点需要即返回 true)
      Phase 内部 filterNodes() 精确过滤: 已有节点跳过，仅操作新节点
```

**场景 4: 集群删除/重置 (Delete/Reset)**

```
执行 Phase 列表:
  DeletePhases (仅 2 个 Phase):
    1. EnsurePaused        → 暂停集群操作
    2. EnsureDeleteOrReset → 执行删除/重置操作
```

**场景 5: 集群暂停 (Pause)**

```
执行 Phase 列表:
  CommonPhases (NeedExecute 检查):
    1. EnsureFinalizer     → 已存在 → 跳过
    2. EnsurePaused        → NeedExecute=true (Spec.Pause=true) → 执行
    3-5. 其他 CommonPhases → NeedExecute=false → 跳过

  DeployPhases + PostDeployPhases:
    全部 NeedExecute=false → 全部跳过
```

**场景 6: DryRun 模式**

```
执行 Phase 列表:
  CommonPhases (NeedExecute 检查):
    1. EnsureFinalizer     → 已存在 → 跳过
    2-4. 其他 CommonPhases → NeedExecute=false → 跳过
    5. EnsureDryRun        → NeedExecute=true (Spec.DryRun=true) → 执行

  DeployPhases + PostDeployPhases:
    全部 NeedExecute=false → 全部跳过
```

**场景 7: 纳管已有集群 (Manage)**

```
执行 Phase 列表:
  CommonPhases (NeedExecute 检查):
    1. EnsureFinalizer     → 已存在 → 跳过
    2-4. 其他 CommonPhases → NeedExecute=false → 跳过
    5. EnsureClusterManage → NeedExecute=true (Spec.Manage=true) → 执行

  DeployPhases + PostDeployPhases:
    全部 NeedExecute=false → 全部跳过
```

##### Phase 执行判断逻辑

每个 Phase 通过 `NeedExecute(old, new *BKECluster)` 方法判断是否需要执行：

| Phase | NeedExecute 判断逻辑 |
|-------|---------------------|
| `EnsureFinalizer` | `!util.Contains(finalizer)` → 需要添加 finalizer |
| `EnsurePaused` | `new.Spec.Pause == true` → 需要暂停 |
| `EnsureClusterManage` | `new.Spec.Manage == true && !Status.Managed` → 需要纳管 |
| `EnsureDeleteOrReset` | `IsDeleteOrReset(new)` → 需要删除/重置 |
| `EnsureDryRun` | `new.Spec.DryRun == true` → 需要 DryRun |
| `EnsureBKEAgent` | `HasNodesNeedingPhase(NodeAgentPushedFlag)` → 有节点未推送 Agent |
| `EnsureNodesEnv` | `HasNodesNeedingPhase(NodeEnvFlag)` → 有节点未初始化环境 |
| `EnsureClusterAPIObj` | `!CAPI 对象已存在` → 需要创建 |
| `EnsureCerts` | `!证书已存在` → 需要生成 |
| `EnsureLoadBalance` | `!LB 已配置` → 需要配置 |
| `EnsureMasterInit` | `首个 Master 且未初始化` → 需要 init |
| `EnsureMasterJoin` | `GetNeedJoinMasterNodesWithBKENodes()` → 有新 Master 需要 join |
| `EnsureWorkerJoin` | `GetNeedJoinWorkerNodesWithBKENodes()` → 有 Worker 需要 join |
| `EnsureAddonDeploy` | `!Addon 已部署` → 需要部署 |
| `EnsureNodesPostProcess` | `HasNodesNeedingPhase(NodePostProcessFlag)` → 有节点未处理后置脚本 |
| `EnsureAgentSwitch` | `!已切换` → 需要切换 |
| `Ensure*Upgrade` | `版本不一致 && 需要升级` → 需要升级 |
| `Ensure*Delete` | `缩容场景 && 节点需要删除` → 需要删除 |

##### 状态上报逻辑

每个 Phase 执行后通过 `calculateClusterStatusByPhase()` 设置 `ClusterStatus`：

| Phase 类别 | ClusterStatus (成功) | ClusterStatus (失败) |
|-----------|---------------------|---------------------|
| ClusterInitPhaseNames | `ClusterInitializing` | `ClusterInitializationFailed` |
| ClusterScaleMasterUpPhaseNames | `ClusterMasterScalingUp` | `ClusterScaleFailed` |
| ClusterScaleWorkerUpPhaseNames | `ClusterWorkerScalingUp` | `ClusterScaleFailed` |
| ClusterDeletePhaseNames | `ClusterDeleting` | `ClusterDeleteFailed` |
| ClusterPausedPhaseNames | `ClusterPaused` | `ClusterPauseFailed` |
| ClusterDryRunPhaseNames | `ClusterDryRun` | `ClusterDryRunFailed` |
| ClusterAddonsPhaseNames | `ClusterDeployingAddon` | `ClusterDeployAddonFailed` |
| ClusterUpgradePhaseNames | `ClusterUpgrading` | `ClusterUpgradeFailed` |
| ClusterScaleMasterDownPhaseNames | `ClusterMasterScalingDown` | `ClusterScaleFailed` |
| ClusterScaleWorkerDownPhaseNames | `ClusterWorkerScalingDown` | `ClusterScaleFailed` |
| ClusterManagePhaseNames | `ClusterManaging` | `ClusterManageFailed` |

#### 7.2.4 Legacy 路径的版本来源

Legacy PhaseFlow 的版本来源与 DAG 路径不同：

| 维度 | DAG 路径 (安装/升级) | Legacy PhaseFlow 路径 |
|------|---------------------|----------------------|
| **K8s 版本来源** | `ReleaseImage bundle.Components[kubernetes-master].Version` → Command CR 参数 | `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` (用户设置或 ClusterVersion 同步) |
| **etcd 版本来源** | `ReleaseImage bundle.Components[etcd].Version` → Command CR 参数 | `BKECluster.Spec.ClusterConfig.Cluster.EtcdVersion` (用户设置或 ClusterVersion 同步) |
| **是否依赖 ReleaseImage** | 是 (必须 Phase=Valid) | 否 (从 Spec 直接读取) |
| **BKEAgent 版本来源** | Command CR `kubernetesVersion`/`etcdVersion` 参数 (§7.2.4) | `BkeConfig.Cluster.KubernetesVersion` (getBKEConfig 从 Spec 读取) |
| **适用场景** | Feature Gate 启用 + ReleaseImage Valid | Feature Gate 关闭 / ReleaseImage 未就绪 / 扩容 / 纳管等 |

> **关键**：Legacy 路径**不依赖 ReleaseImage**，从 `BKECluster.Spec` 直接读取版本。这确保即使没有 ReleaseImage CR 也能完成安装/升级 (向后兼容)。

#### 7.2.5 Legacy 路径与 DAG 路径的共存设计

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              Legacy 与 DAG 路径共存设计                                           │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  场景 1: Feature Gate 全关闭 (v2.7.0 默认)                                      │
│    所有操作走 Legacy PhaseFlow                                                   │
│    版本从 BKECluster.Spec 读取                                                   │
│    ReleaseImage CR 可有可无                                                      │
│                                                                                 │
│  场景 2: Feature Gate 部分开启 (v2.8.0 灰度)                                     │
│    DAG 升级路径开启 → 升级走 DAG                                                 │
│    DAG 安装路径关闭 → 安装走 Legacy PhaseFlow                                    │
│    版本: 升级从 ReleaseImage, 安装从 Spec                                         │
│                                                                                 │
│  场景 3: Feature Gate 全开启 (v2.9.0+)                                           │
│    全新安装走 DAG 安装路径                                                       │
│    版本升级走 DAG 升级路径                                                       │
│    扩容/纳管/删除/暂停/DryRun 仍走 Legacy PhaseFlow                              │
│    版本均从 ReleaseImage                                                         │
│                                                                                 │
│  场景 4: ReleaseImage 未就绪 (Feature Gate 开启但 RI 未验证)                     │
│    shouldUseDeclarativeInstall/Upgrade 返回 true                                  │
│    → executeInstallDAG/executeUpgradeDAG                                          │
│    → resolveInstallBundle 失败 → RequeueAfter 30s 等待                            │
│    → 不回退 Legacy PhaseFlow (Feature Gate 开启 = RI 必须就绪)                   │
│                                                                                 │
│  场景 5: 混合模式 (部分组件 DAG, 部分组件 Legacy)                                │
│    Phase 2 灰度: 低风险组件 (coredns/kube-proxy) 走 DAG                         │
│    高风险组件 (kubernetes-master/etcd) 仍走 Legacy                              │
│    DeclarativeUpgradeStatus 追踪已完成组件                                       │
│    Legacy Phase 跳过已由 DAG 完成的组件 (DeclarativeDAGCompleted)              │
│                                                                                 │
│  共存保障:                                                                       │
│    1. shouldUseDeclarativeInstall/Upgrade 互斥 — 不会同时走两条 DAG 路径       │
│    2. Legacy PhaseFlow 的 CalculatePhase 检查 DeclarativeDAGCompleted          │
│       → 如果 DAG 已完成部分组件，Legacy Phase 跳过这些组件                       │
│    3. 版本来源统一: 两种路径都通过 ApplyVersionContextTargetsToClusterSpec      │
│       将版本同步到 BKECluster.Spec (Legacy 直接读取，DAG 通过 Command CR 覆盖)  │
│    4. 状态隔离: DeclarativeUpgradeStatus (DAG) vs PhaseStatus (Legacy)          │
│       各自追踪，互不干扰                                                          │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 7.2.6 PhaseFlow 路径（Legacy）的适用场景

以下场景仍会走 Legacy PhaseFlow 路径，不走 DAG 路径：

| 场景 | 原因 | 说明 | 走哪个 Phase 列表 |
|------|------|------|------------------|
| **Feature Gate 未启用** | `DeclarativeInstallEnabled = false` / `DeclarativeUpgradeEnabled = false` | 迁移期间默认关闭，确保生产稳定；正式启用后此场景消失 | CommonPhases + DeployPhases / UpgradePhases |
| **ReleaseImage 未就绪 (Feature Gate 关闭)** | RI 未创建或 Status.Phase != Valid | Feature Gate 关闭时走 Legacy，从 Spec 读取版本 | CommonPhases + DeployPhases / UpgradePhases |
| **ReleaseImage 未就绪 (Feature Gate 开启)** | RI 未创建或 Status.Phase != Valid | Feature Gate 开启 → shouldUseDeclarativeInstall 返回 true → resolveInstallBundle 失败 → Requeue 等待 (不回退 Legacy) | Requeue 等待 |
| **ReleaseImage 无 inline handler** | `install.components[].inline` 字段为空 | 旧格式 RI 仅有 `{name, version}`，DAG 无法分发执行器 | CommonPhases + DeployPhases |
| **纳管已有集群** | `BKECluster.Spec.Manage = true` | 纳管现有集群不走标准安装流程 | CommonPhases + ManagePhases |
| **集群扩容** | 新增 Master/Worker 节点 | 扩容仅执行部分 Phase (Join)，非完整安装 | CommonPhases + ScalePhases |
| **集群删除/重置** | `Spec.Reset = true` 或 `DeletionTimestamp` 非空 | 删除/重置走专用 Phase | CommonPhases + DeletePhases |
| **DryRun 模式** | `Spec.DryRun = true` | DryRun 不实际执行安装 | CommonPhases + DryRunPhases |
| **集群暂停** | `Spec.Pause = true` | 暂停状态下不执行任何操作 | CommonPhases + PausePhases |
| **混合模式** | Phase 2 灰度: 部分组件 DAG，部分 Legacy | 高风险组件仍走 Legacy | CommonPhases + 部分 DeployPhases/UpgradePhases (跳过 DAG 已完成的) |

> **注意**：扩容场景 (新增 Master/Worker 节点) 虽然不走 DAG 安装路径，但未来可考虑将扩容也纳入 DAG 驱动 (如 `kubernetes-master` 组件的 `DecisionInstall` 触发 `EnsureMasterJoin` handler)，作为后续优化方向。

```go
// shouldUseDeclarativeInstall 判断是否使用 DAG 安装路径 🆕新增
// 两重门控: Feature Gate + 全新安装判定
// ★ 不检查 install-ready annotation (已移除)
// ★ Feature Gate 开启后强制要求 ReleaseImage 必须就绪:
//   shouldUseDeclarativeInstall 返回 true → executeInstallDAG → resolveInstallBundle
//   ReleaseImage 未就绪 → RequeueAfter 30s 等待 (不回退 Legacy)
func (r *BKEClusterReconciler) shouldUseDeclarativeInstall(bkeCluster *bkev1beta1.BKECluster) bool {
    // 门控 1: Feature Gate 控制
    if !featuregate.DeclarativeInstallEnabled.Enabled() {
        return false
    }
    // 门控 2: 仅在全新安装时启用（BKECluster.Status.Phase 为空或 Init）
    // BKECluster.Status.Phase: 空=未初始化(全新创建), PhaseInit=初始化中,
    //   其他值=已部署/升级中/删除中等 (非全新安装，不走安装 DAG)
    if bkeCluster.Status.Phase != "" && bkeCluster.Status.Phase != bkev1beta1.PhaseInit {
        return false
    }
    // ★ 不检查 install-ready annotation
    // Feature Gate 开启 = ReleaseImage 必须就绪, resolveInstallBundle 失败时 Requeue 等待
    return true
}
```

#### 7.2.7 ReleaseImage 就绪保障与 ClusterVersion 协调设计

> **设计变更**：原设计通过 `install-ready` annotation 作为第三重门控，确保 ReleaseImage 就绪后才走 DAG 路径。现简化为两重门控——Feature Gate 开启后强制要求 ReleaseImage 必须就绪，`executeInstallDAG` 中 `resolveInstallBundle` 失败时 Requeue 等待，不回退 Legacy PhaseFlow。

**设计思路 — 安装场景为什么不需要 annotation 通知机制**：

升级场景使用 `upgrade-ready` annotation 是因为**多 Hop 协调**——ClusterVersionReconciler 需要通过 annotation 值（hopTarget）告知 BKEClusterReconciler "当前应该升级到哪个中间版本"。安装场景**没有多 Hop**——`desiredVersion` 就是最终目标版本，BKEClusterReconciler 直接从 Spec 获取目标版本，不需要 ClusterVersionReconciler 通过 annotation 告知。

| 维度 | 升级场景 (多 Hop) | 安装场景 (无 Hop) |
|------|------------------|-----------------|
| **目标版本** | ClusterVersionReconciler 解析 UpgradePath 确定 hopTarget | BKECluster.Spec 直接指定 desiredVersion |
| **是否需要告知目标** | ✅ 需要 (annotation 值=hopTarget) | ❌ 不需要 (BKECluster 自己知道) |
| **多步协调** | ✅ 每个 Hop 完成后清除 annotation, 进入下一个 Hop | ❌ 一次性操作, 无中间步骤 |
| **通知机制** | `upgrade-ready` annotation (Watch 触发) | Requeue 轮询 (30s) |
| **效率** | 高 (annotation 变更触发 Watch) | 低 (轮询), 但安装是低频操作, 可接受 |

**安装场景完整流程**：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              安装场景: ClusterVersion 与 BKECluster 协调流程                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  步骤 1: 用户创建 BKECluster CR (Spec.OpenFuyaoVersion = "v2.7.0")              │
│     │                                                                           │
│     ▼ BKEClusterReconciler.Reconcile()                                          │
│     │  ensureClusterVersionOnInstall() → 创建 ClusterVersion CR                 │
│     │    (ClusterVersion.Spec.DesiredVersion = "v2.7.0")                        │
│     │                                                                           │
│     ▼ BKEClusterReconciler: executePhaseFlow()                                  │
│     │  shouldUseDeclarativeInstall(newCluster)?                                 │
│     │    门控 1: Feature Gate ON? ✓                                             │
│     │    门控 2: BKECluster.Status.Phase 为空 (全新安装)? ✓                     │
│     │    → 返回 true → executeInstallDAG()                                      │
│     │                                                                           │
│     ▼ executeInstallDAG()                                                       │
│     │  resolveInstallBundle(ctx, newCluster)                                    │
│     │    → 查找 ReleaseImage CR (Spec.Version == desiredVersion)                │
│     │    → ReleaseImage 尚未创建/未验证 → 失败                                   │
│     │    → RequeueAfter: 30s (等待 ReleaseImage 就绪, 不回退 Legacy)            │
│     │                                                                           │
│  步骤 2: ClusterVersionReconciler.Reconcile() (被 CV 创建触发)                   │
│     │                                                                           │
│     ▼ isInstallPhase(ctx, bc, cv)?                                              │
│     │  current == "" (无已安装版本) → true (安装场景) ★                          │
│     │                                                                           │
│     ▼ 安装场景分支:                                                              │
│     │  setStatus(cv, Phase=Installing)                                          │
│     │  ensurer.Ensure(ctx, bc, cv, desired)                                     │
│     │    → 查找/创建 ReleaseImage CR                                            │
│     │    → 拉取 OCI Bundle (ORAS)                                               │
│     │    → ReleaseImageReconciler 验证 (签名/组件/兼容性)                        │
│     │    → 等待 ReleaseImage.Status.Phase = Valid                               │
│     │    → 未就绪: 返回 RequeueResult (等待)                                     │
│     │    → 已就绪: 返回 nil (ReleaseImage Valid)                                │
│     │  ★ 不设置任何 annotation (不通知 BKECluster)                              │
│     │  ★ ClusterVersionReconciler 职责到此结束                                  │
│     │                                                                           │
│  步骤 3: BKEClusterReconciler.Reconcile() (30s Requeue 后再次触发)              │
│     │                                                                           │
│     ▼ executePhaseFlow() → shouldUseDeclarativeInstall → true                   │
│     ▼ executeInstallDAG()                                                       │
│     │  resolveInstallBundle(ctx, newCluster)                                    │
│     │    → ReleaseImage CR 已存在, Status.Phase = Valid ✓                       │
│     │    → 解析 OCI Bundle 成功                                                 │
│     │  → BuildVersionContextForInstall → 构建 VC                                │
│     │  → BuildInstallDAGFromBundle → 构建 DAG                                   │
│     │  → Scheduler.ExecuteDAG → 并行执行安装组件                                │
│     │  → 安装完成 → 更新 BKECluster.Status                                      │
│     │                                                                           │
│  步骤 4: BKEClusterReconciler: completeClusterVersionInstall()                  │
│     │  BKECluster.Status.ClusterStatus = ClusterReady                           │
│     │  → Patch ClusterVersion.Status (CurrentVersion = installed, Phase = Ready) │
│     │                                                                           │
│  步骤 5: ClusterVersionReconciler.Reconcile() (CV Status 变更触发)              │
│     │  isInstallPhase? → false (current == desired, ClusterReady)               │
│     │  → 进入稳态, 不再操作                                                     │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**两个 Controller 的职责分工**：

| Controller | 安装场景职责 | 与升级场景对比 |
|------------|------------|--------------|
| **ClusterVersionReconciler** | `isInstallPhase` → `ensurer.Ensure()` 拉取/验证 ReleaseImage → 等待 Valid → 返回 | 升级: 额外解析 UpgradePath + 设置 `upgrade-ready` annotation |
| **BKEClusterReconciler** | `shouldUseDeclarativeInstall` → `executeInstallDAG` → `resolveInstallBundle` (Requeue 等待 RI) → DAG 执行 | 升级: 检测 `upgrade-ready` annotation → `executeUpgradeDAG` |

**协调机制**：

两个 Controller 通过 **ReleaseImage CR 的 `Status.Phase`** 间接协调，不需要直接的 annotation 通知：

```txt
ClusterVersionReconciler                    BKEClusterReconciler
       │                                          │
       │  ensurer.Ensure()                        │  resolveInstallBundle()
       │  → 拉取 OCI Bundle                        │  → 读取 ReleaseImage CR
       │  → 验证 (签名/组件/兼容性)                 │  → 检查 Status.Phase
       │  → 设置 ReleaseImage.Status.Phase         │
       │                                         │
       │         ReleaseImage CR (共享状态)         │
       │    ┌──────────────────────────┐          │
       │    │ Status.Phase:             │          │
       │    │   "" → Pending → Valid    │←─────────│
       │    │                ↑          │          │
       │    │    CV Reconciler 写入     │   BC Reconciler 读取
       │    └──────────────────────────┘          │
       │                                         │
       │  (不设置 annotation)                     │  (Requeue 30s 轮询)
       │  (不通知 BKECluster)                     │  (直到 Status.Phase=Valid)
```

**为什么 Requeue 轮询可接受**：

1. **安装是低频操作**：集群安装一生一次，30s 轮询开销可忽略（对比升级可能多 Hop，每 Hop 都需要协调）
2. **ReleaseImage 验证通常 < 1 分钟**：OCI 拉取 + 签名验证 + 组件解析 + 兼容性校验，通常在 30-60s 内完成，1-2 次 Requeue 即可
3. **避免 annotation 维护**：不需要 ClusterVersionReconciler 设置/清除 annotation，减少状态管理复杂度和潜在的状态不一致风险
4. **与升级路径的差异是合理的**：升级需要 annotation 是因为多 Hop 协调（ClusterVersionReconciler 需要告知 hopTarget），安装不需要是因为无 Hop（BKECluster 自己知道 desiredVersion）

> **设计原则**：安装场景无多 Hop，BKEClusterReconciler 直接从 Spec 获取目标版本，不需要 ClusterVersionReconciler 通过 annotation 告知。两个 Controller 通过 ReleaseImage CR 的 `Status.Phase=Valid` 间接协调——ClusterVersionReconciler 负责拉取/验证（写入 Status），BKEClusterReconciler 负责消费（读取 Status），Requeue 轮询作为协调机制。

### 7.3 executeInstallDAG 实现

```go
// controllers/capbke/bkecluster_install_dag.go 🆕新增

func (r *BKEClusterReconciler) executeInstallDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 0. 清理上次 DAG 执行的残留状态 (重置后重新安装场景)
    //    Scheduler 跳过链中 shouldSkipComponent 检查 DeclarativeUpgradeStatus.IsCompleted,
    //    如果不清理, 上次安装的 "completed" 记录会导致组件被错误跳过
    newCluster.Status.DeclarativeUpgrade = nil
    newCluster.Status.ClusterComponentStatuses = nil

    // 1. 解析目标 ReleaseImage
    releaseImage, bundle, err := r.resolveInstallBundle(ctx, newCluster)
    
    // 2. 构建安装 VersionContext（Current 为空，Target 来自 bundle）
    //    ★ 直接构建所有 install.components 的 Target
    vc := upgrade.BuildVersionContextForInstall(bundle)
    phaseCtx.SetVersionContext(vc)
    
    // 3. 同步目标版本到 BKECluster.Spec
    //    ★ 仅用于 Legacy 代码路径兼容 (如 upgradeMasterNodesWithParams / waitForNodeHealthCheck 直接读 Spec)
    //    ★ 不作为 BKEAgent 渲染 manifest 的版本来源 — BKEAgent 应从 ReleaseImage 获取版本
    upgrade.ApplyVersionContextTargetsToClusterSpec(vc, newCluster)
    
    // 4. 构建安装 DAG (包含所有 install.components)
    //    ★ 直接构建所有组件的 DAG，不需要排除任何组件
    //    ★ 组件的跳过逻辑由执行时的 VersionContext.Decide() 决定
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    
    // 5. 构建 ComponentFactory（注册安装 handler）
    factory := componentfactory.NewFactoryFromBundle(bundle)
    // 额外注册安装 handler
    factory.RegisterInstallHandlers()
    
    // 6. 构建 Scheduler（复用升级框架）
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        ManifestStore:   manifest.NewBundleStore(bundle),
        ManifestApplier: r.ManifestApplier,
        CVStore:         manifest.NewBundleStore(bundle),
        MaxParallelPerBatch: 8,
    })
    
    // 7. 构建 ExecutionContext
    //    ★ 将 bundle 注入 ExecutionContext，供 BKEAgent 获取 ReleaseImage 中的组件版本
    execCtx := buildExecutionContext(ctx, r.Client, newCluster, vc)
    execCtx.ReleaseBundle = bundle  // ★ 新增: BKEAgent 从 bundle 读取版本
    
    // 8. 执行 DAG
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return err
    }
    
    // 9. 安装完成：更新状态
    newCluster.Status.Phase = bkev1beta1.PhaseReady
    newCluster.Status.ClusterStatus = bkev1beta1.ClusterStatusReady
    
    return nil
}
```

#### 7.3.1 Spec 同步仅为 Legacy 兼容

`ApplyVersionContextTargetsToClusterSpec()` 将 VC Target (来源于 ReleaseImage) 同步到 `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` / `EtcdVersion` / `ContainerdVersion`。此同步**仅用于 Legacy 代码路径兼容**，不作为 BKEAgent 渲染 manifest 的版本来源。

| 维度 | Legacy 路径 (现有) | 声明式 DAG 路径 (目标) |
|------|-------------------|----------------------|
| **BKEAgent 版本来源** | `BkeConfig.Cluster.KubernetesVersion` (从 `BKECluster.Spec` 读取) | **`ReleaseImage` bundle 中的组件版本** ★ |
| **manifest image tag** | `BkeConfig.Cluster.KubernetesVersion` 去 v 前缀 | **`bundle.Components[kubernetes-master].Spec.Version` 去 v 前缀** ★ |
| **etcd image tag** | `BkeConfig.Cluster.EtcdVersion` 或 `Extra["etcdVersion"]` | **`bundle.Components[etcd].Spec.Version` 去 v 前缀** ★ |
| **kubelet 二进制版本** | `BkeConfig.Cluster.KubernetesVersion` | **`bundle.Components[kubernetes-worker].Spec.Version`** ★ |
| **Spec 同步的作用** | 唯一来源 (BKEAgent 直接读取) | **仅 Legacy 兼容** (供未改造的 Phase 代码直接读取 Spec) |

#### 7.3.2 BKEAgent 从 ReleaseImage 获取版本的设计

基于 §7.7 (KEP-7 minimal-k8s-upgrade) 的分析，当前 BKEAgent 通过 `getBKEConfig()` 读取 `BKECluster.Spec.ClusterConfig` 获取版本。声明式 DAG 路径应修正为从 ReleaseImage 获取版本，消除对 Spec 同步的依赖。

**当前路径 (依赖 Spec 同步)**：

```txt
ReleaseImage → VC → SyncUpgradeTargets → BKECluster.Spec → BKEAgent getBKEConfig()
  → BkeConfig.Cluster.KubernetesVersion → manifest image tag
```

**目标路径 (直接从 ReleaseImage)**：

```txt
ReleaseImage → bundle → Command CR 携带版本参数 → BKEAgent 直接使用
  → 不依赖 BKECluster.Spec 同步
```

**实现方式：Command CR 携带 ReleaseImage 版本参数**

```go
// EnsureMasterInit 创建 Command CR 时，从 ReleaseImage bundle 读取版本并注入参数

func (e *EnsureMasterInit) Execute() (ctrl.Result, error) {
    // 从 ExecutionContext 获取 ReleaseImage bundle
    bundle := e.Ctx.ReleaseBundle  // ★ 新增: bundle 注入 PhaseContext

    // 从 bundle 解析版本 (不再依赖 BKECluster.Spec)
    k8sVersion := releaseVersionFromBundle(bundle, "kubernetes-master")
    etcdVersion := releaseVersionFromBundle(bundle, "etcd")

    // 创建 Bootstrap Command CR，携带版本参数
    params := CreateInitCommandParams{
        // ... 现有参数 ...
        KubernetesVersion: k8sVersion,  // ★ 从 ReleaseImage 获取
        EtcdVersion:       etcdVersion, // ★ 从 ReleaseImage 获取
    }
    bootstrap := createBootstrapCommand(params)
    bootstrap.New()
    // ...
}

// releaseVersionFromBundle 从 ReleaseImage bundle 中查找组件版本
func releaseVersionFromBundle(bundle *releasemanifest.Bundle, componentName string) string {
    // 优先从 upgrade.components 查找 (升级条目覆盖安装条目)
    if bundle.Release.Spec.Upgrade != nil {
        for _, c := range bundle.Release.Spec.Upgrade.Components {
            if c.Name == componentName {
                return c.Version
            }
        }
    }
    // 回退到 install.components
    if bundle.Release.Spec.Install != nil {
        for _, c := range bundle.Release.Spec.Install.Components {
            if c.Name == componentName {
                return c.Version
            }
        }
    }
    return ""
}
```

**BKEAgent 端修正：优先使用 Command 参数中的版本**

```go
// pkg/job/builtin/kubeadm/kubeadm.go — getBKEConfig() 修正

func (k *KubeadmPlugin) getBKEConfig(bkeConfigNS string) error {
    bkeCluster, err := plugin.GetBKECluster(bkeConfigNS)
    config, err := plugin.GetBkeConfigFromBkeCluster(bkeCluster)
    k.boot.BkeConfig = config

    // ★ 新增: 从 Command 参数覆盖版本 (优先于 Spec)
    if k8sVer, ok := k.parseCommands["kubernetesVersion"]; ok && k8sVer != "" {
        k.boot.BkeConfig.Cluster.KubernetesVersion = k8sVer
    }
    if etcdVer, ok := k.parseCommands["etcdVersion"]; ok && etcdVer != "" {
        k.boot.BkeConfig.Cluster.EtcdVersion = etcdVer
        if k.boot.Extra == nil {
            k.boot.Extra = map[string]interface{}{}
        }
        k.boot.Extra["etcdVersion"] = etcdVer  // etcd 优先级最高
    }

    return nil
}
```

**manifest 渲染的版本来源修正**：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              manifest image tag 版本来源修正                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  当前 (依赖 Spec 同步):                                                          │
│    ReleaseImage → VC → SyncTargets → BKECluster.Spec                            │
│    → BKEAgent getBKEConfig() → BkeConfig.Cluster.KubernetesVersion              │
│    → Go 模板 imageInfo() → manifest image tag                                  │
│                                                                                 │
│  修正后 (从 ReleaseImage 直接获取):                                              │
│    ReleaseImage → bundle → Command CR 携带 kubernetesVersion/etcdVersion 参数  │
│    → BKEAgent getBKEConfig() 读取参数覆盖 BkeConfig                              │
│    → Go 模板 imageInfo() → manifest image tag (来源为 ReleaseImage，非 Spec)   │
│                                                                                 │
│  修正的原因:                                                                     │
│  1. Spec 同步依赖 mergecluster.SyncStatusUntilComplete API patch，有竞态风险   │
│     (patch 未完成时 BKEAgent 可能读到旧值)                                       │
│  2. Spec 是用户可编辑字段，用户可能修改了 Spec 中的版本导致不一致               │
│  3. ReleaseImage 是声明式真相源，版本应直接从 ReleaseImage 获取                  │
│  4. Command CR 携带版本参数是同步操作，无竞态风险                                │
│                                                                                 │
│  对 kubelet 延迟升级的影响:                                                      │
│  skipKubelet 仍然有效 — installKubeletCommand 从 BkeConfig.Cluster.             │
│  KubernetesVersion 读取版本 (已被 Command 参数覆盖为 ReleaseImage 版本)         │
│  skipKubelet=true 跳过此函数即可                                                 │
│                                                                                 │
│  对 etcd 版本的影响:                                                             │
│  etcd 版本通过 Command 参数 etcdVersion 传入 → Extra["etcdVersion"]             │
│  → etcdImageTagFromBootScope() 优先级 1 (Extra["etcdVersion"]) 命中             │
│  → 不再依赖 BKECluster.Spec.EtcdVersion                                         │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 7.3.3 版本来源对比汇总

| 组件 | 当前来源 (Legacy) | 修正后来源 (声明式 DAG) | 传递方式 |
|------|------------------|----------------------|---------|
| kube-apiserver manifest tag | `BkeConfig.Cluster.KubernetesVersion` (Spec 同步) | `bundle.Components[kubernetes-master].Version` | Command CR `kubernetesVersion` 参数 |
| kube-controller-manager manifest tag | 同上 | 同上 | 同上 |
| kube-scheduler manifest tag | 同上 | 同上 | 同上 |
| etcd manifest tag | `BkeConfig.Cluster.EtcdVersion` 或 `Extra["etcdVersion"]` | `bundle.Components[etcd].Version` | Command CR `etcdVersion` 参数 → `Extra["etcdVersion"]` |
| kubelet 二进制 (Master 节点) | `BkeConfig.Cluster.KubernetesVersion` | `bundle.Components[kubernetes-master].Version` | Command CR `kubernetesVersion` 参数 |
| kubelet 二进制 (Worker 节点) | `BkeConfig.Cluster.KubernetesVersion` | `bundle.Components[kubernetes-worker].Version` | Command CR `kubernetesVersion` 参数 |
| kubectl 二进制 (Master 节点) | `BkeConfig.Cluster.KubernetesVersion` | `bundle.Components[kubernetes-master].Version` | Command CR `kubernetesVersion` 参数 |
| kubectl 二进制 (Worker 节点) | `BkeConfig.Cluster.KubernetesVersion` | `bundle.Components[kubernetes-worker].Version` | Command CR `kubernetesVersion` 参数 |

> **版本来源优先级**：
> - Master 节点：`EnsureMasterUpgrade` 优先使用 `kubernetes-master` 组件版本，回退到 `kubernetes-worker`，再回退到 `kubernetes`
> - Worker 节点：`EnsureWorkerUpgrade` 优先使用 `kubernetes-worker` 组件版本，回退到 `kubernetes-master`，再回退到 `kubernetes`
> - 这种设计允许 Master 和 Worker 在不同时间升级（滚动升级场景），但实际使用中版本应该相同

> **Spec 同步保留但仅用于兼容**：`ApplyVersionContextTargetsToClusterSpec()` 仍执行，确保 Legacy 代码路径 (如 `upgradeMasterNodesWithParams` / `waitForNodeHealthCheck` 直接读 Spec) 能获取正确版本。但 BKEAgent 的版本来源修正为 Command CR 参数 (从 ReleaseImage)，不再依赖 Spec 同步时序。

#### 7.3.4 kubernetesVersion 命令参数设计 (对标 etcdVersion)

##### 现状分析

当前代码中 etcd 已有命令参数覆盖机制，但 K8s 版本没有：

| 组件 | 命令参数 | 覆盖函数 | 覆盖目标 | 状态 |
|------|---------|---------|---------|------|
| etcd | `etcdVersion` | `applyCommandEtcdVersion()` (kubeadm.go:436) | `BkeConfig.Cluster.EtcdVersion` + `Extra["etcdVersion"]` | **已实现** |
| K8s (apiserver/cm/scheduler/kubelet/kubectl) | **无** | **无** | 依赖 `SyncUpgradeTargetsToClusterSpec` → `BKECluster.Spec` → `getBKEConfig()` 读取 | **未实现** ★ |

```go
// pkg/job/builtin/kubeadm/kubeadm.go — Execute() 现有代码 (line 122-133)

func (k *KubeadmPlugin) Execute(commands []string) ([]string, error) {
    parseCommands, err := plugin.ParseCommands(k, commands)
    // ...
    if v, ok := parseCommands["bkeConfig"]; ok {
        if err = k.getBKEConfig(v); err != nil {  // 从 BKECluster.Spec 读取 BkeConfig
            return nil, err
        }
        k.applyCommandEtcdVersion(parseCommands)  // ★ etcd 有命令参数覆盖
        // ✗ 没有 applyCommandKubernetesVersion — K8s 版本无命令参数覆盖
    }

    switch parseCommands["phase"] {
    case utils.UpgradeControlPlane:
        // ...
        return nil, k.upgradeControlPlane(backupEtcd, parseCommands["clusterType"])
        // upgradeControlPlane 内部读取 BkeConfig.Cluster.KubernetesVersion (来自 Spec，非命令参数)
    }
}

// applyCommandEtcdVersion — etcd 的命令参数覆盖 (现有，line 436)
func (k *KubeadmPlugin) applyCommandEtcdVersion(parseCommands map[string]string) {
    v, ok := parseCommands["etcdVersion"]
    if !ok || v == "" || k.boot == nil || k.boot.BkeConfig == nil {
        return
    }
    k.boot.BkeConfig.Cluster.EtcdVersion = v           // 覆盖 Spec 中的版本
    if k.boot.Extra == nil {
        k.boot.Extra = map[string]interface{}{}
    }
    k.boot.Extra["etcdVersion"] = v                     // 模板优先级 1: Extra["etcdVersion"]
}
```

**问题**：K8s 版本完全依赖 `getBKEConfig()` 从 `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` 读取。该字段由 `SyncUpgradeTargetsToClusterSpec()` 从 VersionContext 同步，存在以下风险：

| 风险 | 说明 |
|------|------|
| **Spec 同步时序** | `SyncUpgradeTargetsToClusterSpec` 通过 `mergecluster.SyncStatusUntilComplete` API patch，patch 未完成时 BKEAgent 可能读到旧值 |
| **用户可编辑 Spec** | `BKECluster.Spec` 是用户可编辑字段，用户可能修改版本导致不一致 |
| **ReleaseImage 非直接来源** | 版本经 ReleaseImage → VC → Spec → BkeConfig 多跳传递，非直接从 ReleaseImage 获取 |

##### 设计方案：新增 applyCommandKubernetesVersion

**对标 `applyCommandEtcdVersion`，新增 `applyCommandKubernetesVersion`**：

```go
// pkg/job/builtin/kubeadm/kubeadm.go — 新增

// applyCommandKubernetesVersion overrides spec kubernetes version when the provider passes a
// declarative upgrade/install target (VersionContext / release bundle).
// Symmetric to applyCommandEtcdVersion.
func (k *KubeadmPlugin) applyCommandKubernetesVersion(parseCommands map[string]string) {
    v, ok := parseCommands["kubernetesVersion"]
    if !ok || v == "" || k.boot == nil || k.boot.BkeConfig == nil {
        return
    }
    // 覆盖 BkeConfig.Cluster.KubernetesVersion (来自 BKECluster.Spec 的值)
    // 供以下消费者使用:
    //   - manifest 渲染: imageInfo() → kube-apiserver:{version} / kube-controller-manager:{version} / kube-scheduler:{version}
    //   - kubelet 二进制: installKubeletCommand() → kubelet-{version}-{arch}
    //   - kubectl 二进制: installKubectlCommand() → kubectl-{version}-{arch}
    //   - needUpgradeComponent(): 比较运行中 Pod image tag vs KubernetesVersion
    k.boot.BkeConfig.Cluster.KubernetesVersion = v
}
```

**Execute() 中调用 (与 applyCommandEtcdVersion 对称)**：

```go
// pkg/job/builtin/kubeadm/kubeadm.go — Execute() 修改 (line 127-133)

func (k *KubeadmPlugin) Execute(commands []string) ([]string, error) {
    parseCommands, err := plugin.ParseCommands(k, commands)
    // ...
    if v, ok := parseCommands["bkeConfig"]; ok {
        if err = k.getBKEConfig(v); err != nil {
            return nil, err
        }
        k.applyCommandEtcdVersion(parseCommands)            // 现有: etcd 版本覆盖
        k.applyCommandKubernetesVersion(parseCommands)       // ★ 新增: K8s 版本覆盖
    }

    switch parseCommands["phase"] {
    // ... 不变
    }
}
```

##### Command CR 传递版本参数

**管理集群侧 (EnsureMasterUpgrade / EnsureMasterInit)**：

```go
// pkg/phaseframe/phases/ensure_master_upgrade.go — 创建 Command CR 时注入版本参数

func (e *EnsureMasterUpgrade) upgradeMasterNodesWithParams(skipKubelet bool) (ctrl.Result, error) {
    // ... 获取 Master 节点 ...

    // 从 ExecutionContext 获取 ReleaseImage bundle
    bundle := e.Ctx.ReleaseBundle  // ★ bundle 注入 PhaseContext

    // 从 bundle 解析版本 (对标 etcdVersion 的传递方式)
    k8sVersion := releaseVersionFromBundle(bundle, "kubernetes-master")
    etcdVersion := releaseVersionFromBundle(bundle, "etcd")

    for _, node := range masterNodes {
        masterParams := CreateUpgradeCommandParams{
            // ... 现有参数不变 ...
            Phase:           bkev1beta1.UpgradeControlPlane,
            SkipKubelet:     skipKubelet,
            KubernetesVersion: k8sVersion,   // ★ 新增: 从 ReleaseImage 获取
            EtcdVersion:     etcdVersion,    // 现有: 从 ReleaseImage 获取
        }
        // createUpgradeCommand 将参数注入 Command CR 的 command 列表
        upgrade := createUpgradeCommand(masterParams)
        // ...
    }
}

// releaseVersionFromBundle 从 ReleaseImage bundle 中查找组件版本
// (与 §7.2.2 中的实现一致)
func releaseVersionFromBundle(bundle *releasemanifest.Bundle, componentName string) string {
    // 优先 upgrade.components，回退 install.components
    if bundle.Release.Spec.Upgrade != nil {
        for _, c := range bundle.Release.Spec.Upgrade.Components {
            if c.Name == componentName {
                return c.Version
            }
        }
    }
    if bundle.Release.Spec.Install != nil {
        for _, c := range bundle.Release.Spec.Install.Components {
            if c.Name == componentName {
                return c.Version
            }
        }
    }
    return ""
}
```

**Command CR 中的参数格式**：

```txt
Command CR command 列表:
  Kubeadm
  phase=UpgradeControlPlane
  bkeConfig=<ns>:<name>
  backUpEtcd=true
  clusterType=openfuyao
  etcdVersion=v3.6.7-of.1          ← 现有: etcd 版本从 ReleaseImage
  kubernetesVersion=v1.35.0-of.1   ← ★ 新增: K8s 版本从 ReleaseImage
```

**BKEAgent 端解析 (ParseCommands 自动解析键值对)**：

```go
// parseCommands["kubernetesVersion"] = "v1.35.0-of.1"
// → applyCommandKubernetesVersion 覆盖 BkeConfig.Cluster.KubernetesVersion
// → 所有依赖 KubernetesVersion 的代码路径获得正确版本:
//   - manifest imageInfo(): kube-apiserver:1.35.0-of.1
//   - installKubeletCommand(): kubelet-v1.35.0-of.1-amd64
//   - installKubectlCommand(): kubectl-v1.35.0-of.1-amd64
//   - needUpgradeComponent(): 比较运行中 Pod tag vs "v1.35.0-of.1"
```

##### etcdVersion 与 kubernetesVersion 的对称性

| 维度 | etcdVersion (现有) | kubernetesVersion (新增) | 对称性 |
|------|-------------------|------------------------|--------|
| **命令参数** | `etcdVersion` | `kubernetesVersion` | 对称 |
| **覆盖函数** | `applyCommandEtcdVersion()` | `applyCommandKubernetesVersion()` | 对称 |
| **覆盖目标** | `BkeConfig.Cluster.EtcdVersion` + `Extra["etcdVersion"]` | `BkeConfig.Cluster.KubernetesVersion` | 基本对称 (K8s 不需要 Extra，因模板直接读 BkeConfig) |
| **模板优先级** | `Extra["etcdVersion"]` > `BkeConfig.Cluster.EtcdVersion` > 默认 | `BkeConfig.Cluster.KubernetesVersion` (唯一来源) | K8s 无多级优先级需求 |
| **来源** | `bundle.Components[etcd].Version` | `bundle.Components[kubernetes-master].Version` | 对称 (均从 ReleaseImage bundle) |
| **传递方式** | Command CR 参数 | Command CR 参数 | 对称 |
| **作用** | etcd manifest image tag | apiserver/cm/scheduler manifest tag + kubelet/kubectl 二进制 | K8s 影响范围更广 |

##### 修改清单

| 文件 | 修改内容 | 工作量 |
|------|---------|--------|
| `pkg/job/builtin/kubeadm/kubeadm.go` | 新增 `applyCommandKubernetesVersion()` + Execute() 调用 | 0.5 人日 |
| `pkg/phaseframe/phases/ensure_master_upgrade.go` | Command CR 注入 `kubernetesVersion` 参数 | 0.5 人日 |
| `pkg/phaseframe/phases/ensure_master_init.go` | Command CR 注入 `kubernetesVersion` 参数 (安装路径) | 0.5 人日 |
| `pkg/phaseframe/phases/ensure_worker_upgrade.go` | Command CR 注入 `kubernetesVersion` 参数 (worker 升级) | 0.5 人日 |
| `pkg/phaseframe/phases/ensure_worker_join.go` | Command CR 注入 `kubernetesVersion` 参数 (安装路径) | 0.5 人日 |
| 单元测试 | `applyCommandKubernetesVersion` 测试 + 参数传递测试 | 1 人日 |
| **合计** | | **3.5 人日** |

> **与 KEP-7 minimal-k8s-upgrade 的关系**：本设计在 KEP-10 (安装 DAG) 和 KEP-7 (最小化升级方案) 中共享。`applyCommandKubernetesVersion` 修改的是 BKEAgent 代码，安装和升级路径共同受益。`skipKubelet` 仍然有效 — `installKubeletCommand()` 读取的 `BkeConfig.Cluster.KubernetesVersion` 已被命令参数覆盖为 ReleaseImage 版本，`skipKubelet=true` 仅跳过该函数的执行，不影响版本来源。

### 7.4 安装 DAG 结构

```
安装 DAG（基于 ReleaseImage v2.7.0 install.components 构建）:

依赖关系来自 ComponentVersion.spec.dependencies:

Batch 1: [bkeagent]                  ← 无依赖，最先执行
    └─ inline: EnsureBKEAgent → SSH 推送 bkeagent 二进制

Batch 2: [nodes-env]                 ← 依赖 bkeagent
    └─ inline: EnsureNodesEnv → 节点环境准备（runc/lxcfs/nfs-utils 等）

Batch 3: [certs]                     ← 依赖 nodes-env
    └─ inline: EnsureCerts → 证书生成

Batch 4: [cluster-api-obj, load-balance]  ← 依赖 certs，并行执行
    ├─ inline: EnsureClusterAPIObj → CAPI 对象创建
    └─ inline: EnsureLoadBalance → haproxy/keepalived 部署

Batch 5: [kubernetes-master]         ← 依赖 certs + load-balance
    └─ inline: EnsureMasterInit → kubeadm init + 控制面启动

Batch 6: [kubernetes-worker]         ← 依赖 kubernetes-master（倾斜策略）
    └─ inline: EnsureWorkerJoin → kubeadm join

Batch 7: [kube-proxy, coredns]       ← 依赖 kubernetes-master，并行执行
    ├─ manifest: YamlInstaller Apply → kube-proxy DaemonSet
    └─ manifest: YamlInstaller Apply → coredns Deployment

Batch 8: [nodes-postprocess, agent-switch]  ← 依赖 kubernetes-worker，并行执行
    ├─ inline: EnsureNodesPostProcess → 后置脚本
    └─ inline: EnsureAgentSwitch → Agent 监听切换
```

> **注意**：实际 DAG 结构由 `ComponentVersion.spec.dependencies` 决定。上图为典型配置。

## 8. 部署 Phase 与安装组件映射

### 8.1 设计思路

Legacy `DeployPhases` 列表定义了 11 个串行 Phase，每个 Phase 有固定的执行顺序。DAG 安装路径需要将这些 Phase 映射为 `ComponentSpec` (组件名 + handler)，使 DAG 拓扑排序替代硬编码顺序。

**核心设计思路**：将 Legacy PhaseFlow 的 Phase 名映射为 ReleaseImage 中的组件名，每个组件在 `DeclarativeInstallCatalog` 中声明 inline handler。Phase 间的执行顺序由 `ComponentVersion.spec.dependencies` 声明式定义，替代 DeployPhases 的硬编码列表。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              部署 Phase 与安装组件映射设计思路                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow (硬编码顺序):                                                  │
│    DeployPhases = [                                                              │
│      EnsureBKEAgent,          // 0                                              │
│      EnsureNodesEnv,          // 1                                              │
│      EnsureClusterAPIObj,    // 2                                              │
│      EnsureCerts,            // 3                                              │
│      EnsureLoadBalance,      // 4                                              │
│      EnsureMasterInit,       // 5                                              │
│      EnsureMasterJoin,       // 6 (扩容时)                                     │
│      EnsureWorkerJoin,       // 7                                              │
│      EnsureAddonDeploy,      // 8                                              │
│      EnsureNodesPostProcess, // 9                                              │
│      EnsureAgentSwitch,      // 10                                             │
│    ]                                                                             │
│    顺序: 硬编码在 list.go 的切片索引                                             │
│                                                                                 │
│  DAG 安装路径 (声明式依赖):                                                      │
│    ReleaseImage.install.components → BuildInstallDAG                              │
│    ComponentVersion.spec.dependencies → 拓扑排序                                 │
│                                                                                 │
│    bkeagent (无依赖)                                                             │
│      → nodes-env (依赖 bkeagent)                                                │
│        → certs (依赖 nodes-env)                                                  │
│          → cluster-api-obj (依赖 certs)  ┐                                      │
│          → load-balance (依赖 certs)     ┘ 并行                                  │
│            → kubernetes-master (依赖 certs + load-balance)                      │
│              → kubernetes-worker (依赖 kubernetes-master)                       │
│                → kube-proxy (依赖 kubernetes-master) ┐ 并行                     │
│                → coredns (依赖 kubernetes-master)    ┘                          │
│                  → nodes-postprocess (依赖 kubernetes-worker) ┐ 并行            │
│                  → agent-switch (依赖 kubernetes-worker)      ┘                 │
│                                                                                 │
│  映射原则:                                                                       │
│  1. Phase 名 → 组件名 — EnsureBKEAgent → "bkeagent"                             │
│  2. Phase 构造函数 → InlineHandler — phases.NewEnsureBKEAgent → "EnsureBKEAgent"│
│  3. Phase 顺序 → dependencies — 硬编码索引 → ComponentVersion.spec.dependencies │
│  4. 无对应组件的 Phase — LegacyPhase 字段标记，用于双轨共存时跳过                │
│                                                                                 │
│  安装与升级 handler 差异:                                                       │
│  同一组件在安装和升级时使用不同的 handler:                                       │
│    kubernetes-master: 安装=EnsureMasterInit (kubeadm init)                     │
│                       升级=EnsureMasterUpgrade (kubeadm upgrade apply)          │
│    kubernetes-worker: 安装=EnsureWorkerJoin (kubeadm join)                      │
│                      升级=EnsureWorkerUpgrade (kubeadm upgrade node)            │
│    bkeagent:          安装=EnsureBKEAgent (首次推送)                            │
│                       升级=EnsureAgentUpgrade (推送新版本)                      │
│  原因: 安装和升级的操作语义不同 (init vs upgrade, join vs upgrade node)        │
│        但组件名相同 (都是 "kubernetes-master")，通过不同 Catalog 区分            │
│                                                                                 │
│  嵌入式组件 (无独立 handler):                                                   │
│    containerd: 安装嵌入在 EnsureNodesEnv 的 runtime scope 中 (无独立 handler)  │
│    etcd: 安装由 kubeadm init 隐式创建 (在 EnsureMasterInit 中，无独立 handler)  │
│    两者在 DeclarativeInstallCatalog 中无独立条目                                 │
│    升级时有独立 handler (EnsureContainerdUpgrade / EnsureEtcdUpgrade)          │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 8.2 DeployPhases → 安装组件映射

| DeployPhase | 安装组件名 | 执行模式 | Inline Handler | 说明 |
|-------------|-----------|---------|---------------|------|
| `EnsureBKEAgent` | bkeagent | inline | `EnsureBKEAgent` | SSH 推送 Agent |
| `EnsureNodesEnv` | nodes-env | inline | `EnsureNodesEnv` | 节点环境准备 |
| `EnsureClusterAPIObj` | cluster-api-obj | inline | `EnsureClusterAPIObj` | CAPI 对象 |
| `EnsureCerts` | certs | inline | `EnsureCerts` | 证书生成 |
| `EnsureLoadBalance` | load-balance | inline | `EnsureLoadBalance` | HA 负载均衡 |
| `EnsureMasterInit` | kubernetes-master | inline | `EnsureMasterInit` | Master 初始化 |
| `EnsureMasterJoin` | kubernetes-master | inline | `EnsureMasterInit` | Master 加入（复用） |
| `EnsureWorkerJoin` | kubernetes-worker | inline | `EnsureWorkerJoin` | Worker 加入 |
| `EnsureAddonDeploy` | (coredns) | manifest | - | 附加组件部署 |
| `EnsureNodesPostProcess` | nodes-postprocess | inline | `EnsureNodesPostProcess` | 后置脚本 |
| `EnsureAgentSwitch` | agent-switch | inline | `EnsureAgentSwitch` | Agent 切换 |

### 8.3 安装 vs 升级组件差异

| 组件 | 安装 handler | 升级 handler | 差异说明 |
|------|-------------|-------------|---------|
| **bkeagent** | `EnsureBKEAgent` | `EnsureAgentUpgrade` | 安装=首次推送；升级=SSH 推送新版本 |
| **kubernetes-master** | `EnsureMasterInit` | `EnsureMasterUpgrade` | 安装=kubeadm init；升级=kubeadm upgrade |
| **kubernetes-worker** | `EnsureWorkerJoin` | `EnsureWorkerUpgrade` | 安装=kubeadm join；升级=kubeadm upgrade |
| **containerd** | (包含在 nodes-env) | `EnsureContainerdUpgrade` | 安装=环境准备含 containerd；升级=独立 ENV 命令 |
| **etcd** | (包含在 MasterInit) | `EnsureEtcdUpgrade` | 安装=kubeadm init 含 etcd；升级=独立 etcd 升级 |
| **certs** | `EnsureCerts` | - | 仅安装时生成证书 |
| **load-balance** | `EnsureLoadBalance` | - | 仅安装时配置 HA |
| **nodes-env** | `EnsureNodesEnv` | - | 仅安装时准备环境 |
| **pre-upgrade-resources** | - | `EnsurePreUpgradeResources` | 仅升级时预创建资源 |

## 9. Feature Gate 与迁移策略

### 9.1 Feature Gate 设计

```go
// pkg/featuregate/features.go

var (
    // DeclarativeInstallEnabled 控制 DAG 安装路径是否启用
    // false: 使用 PhaseFlow 安装（默认）
    // true: 使用 DAG 安装
    DeclarativeInstallEnabled = featuregate.NewFeature()
)
```

### 9.2 迁移阶段

| 阶段 | 目标 | 说明 | Feature Gate |
|------|------|------|-------------|
| **Phase 1** | 结构扩展 | 统一抽象 `ReleaseImageComponent`，新增 `inline` 字段 | 不启用 |
| **Phase 2** | 安装 DAG 实现 | 实现 `BuildInstallDAG`、`executeInstallDAG`、`DeclarativeInstallCatalog` | 灰度启用 |
| **Phase 3** | 验证与切换 | 测试环境验证，逐步切换到 DAG 安装路径 | 正式启用 |

> **不包含 Phase 4（移除 PhaseFlow）**：PhaseFlow 作为非安装/非升级场景的兜底方案保留，后续再优化。本提案仅完成安装场景的 DAG 化。

### 9.3 向后兼容

1. **旧格式兼容**：`ReleaseImageComponent` 的 `inline` 字段是 `omitempty`，旧格式 `{name, version}` 仍然有效
2. **PhaseFlow 兜底**：`DeclarativeInstallEnabled` 未启用时，继续使用 PhaseFlow；启用后非安装/非升级场景仍走 PhaseFlow（兜底方案，后续再优化）
3. **混合模式**：ReleaseImage 可同时包含有 `inline` 和无 `inline` 的安装组件
4. **Feature Gate 开启 = ReleaseImage 必须就绪**：不再需要 `install-ready` annotation 作为前置门控。Feature Gate 开启后，`shouldUseDeclarativeInstall` 返回 true → `executeInstallDAG` → `resolveInstallBundle`，ReleaseImage 未就绪时 Requeue 等待（不回退 Legacy）

> **PhaseFlow 兜底路径设计**：PhaseFlow 兜底路径的详细设计（Phase 列表定义、各场景的 Phase 执行列表、执行判断逻辑、状态上报逻辑、版本来源、共存设计、适用场景）见 [§7.2.3-7.2.6](#723-phaseflow-兜底路径设计)。

### 9.4 平滑升级方案

> **范围说明**：以下平滑升级方案描述安装 DAG 化的迁移阶段和风险控制。PhaseFlow 兜底路径（非安装/非升级场景）不在本提案范围内优化。

安装场景的 DAG 化需要分阶段平滑过渡，确保生产环境零中断。

#### 9.4.1 平滑升级核心原则

| 原则 | 说明 |
|------|------|
| **双路径并存** | PhaseFlow 和 DAG 安装路径同时存在，通过 Feature Gate 切换 |
| **逐组件迁移** | 不是一个 openFuyao 版本迁移所有组件，而是逐步将 DeployPhase 迁移到 DAG |
| **灰度验证** | 先在测试环境验证，再灰度到生产环境 |
| **回退能力** | DAG 路径出现问题时，可通过关闭 Feature Gate 回退到 PhaseFlow |
| **版本对齐** | 每个迁移阶段对齐一个 openFuyao 版本，不在运行中切换 |

#### 9.4.2 平滑升级分阶段计划

```
openFuyao v2.7.0  ──────  openFuyao v2.8.0  ──────  openFuyao v2.9.0
     │                        │                        │
     ├─ Phase 1: 结构扩展     │                        │
     │  扩展 ReleaseImage     │                        │
     │  InstallComponent      │                        │
     │  新增 inline 字段      │                        │
     │  (不改变执行路径)       │                        │
     │                        │                        │
     │  Feature Gate: OFF     │                        │
     │                        │                        │
     │                        ├─ Phase 2: 部分 DAG     │
     │                        │  迁移低风险组件:        │
     │                        │  bkeagent/nodes-env/    │
     │                        │  certs/load-balance    │
     │                        │  PhaseFlow 处理剩余    │
     │                        │                        │
     │                        │  Feature Gate: 灰度    │
     │                        │                        │
     │                        │                        ├─ Phase 3: 全量 DAG
     │                        │                        │  迁移高风险组件:
     │                        │                        │  kubernetes-master/
     │                        │                        │  kubernetes-worker/
     │                        │                        │  agent-switch
     │                        │                        │  DAG 处理所有安装
     │                        │                        │  PhaseFlow 保留为兜底
     │                        │                        │
     │                        │                        │  Feature Gate: ON
     │                        │                        │
     │                        │                        │  ★ 不包含 Phase 4
     │                        │                        │  PhaseFlow 兜底保留
     │                        │                        │  后续再优化
```

#### 9.4.3 Phase 1: 结构扩展（v2.7.0）

**目标**：扩展 CRD 结构，不改变任何执行路径。

| 任务 | 说明 |
|------|------|
| 扩展 `ReleaseImageComponent` | 新增 `inline` 字段（omitempty，向后兼容） |
| 定义 `DeclarativeInstallCatalog` | 静态映射表，不被执行路径消费 |
| 定义 `InstallComponentSpec` | 类型定义，不接入 Scheduler |
| 编写安装组件 ComponentVersion | 为所有安装组件编写 `spec.dependencies` |
| 补充 ReleaseImage | v2.7.0 ReleaseImage 的 install.components 补充 inline 字段 |

**风险控制**：不改变任何执行逻辑，仅扩展数据结构，零风险。

#### 9.4.4 Phase 2: 部分 DAG 灰度（v2.8.0）

**目标**：将低风险安装组件迁移到 DAG 路径，高风险组件仍走 PhaseFlow。

**迁移顺序**（按风险从低到高）：

| 批次 | 组件 | 迁移理由 | PhaseFlow 是否保留 |
|------|------|---------|-------------------|
| 1 | bkeagent | 逻辑简单（SSH 推送），无集群依赖 | 保留（回退） |
| 2 | nodes-env | 逻辑简单（环境准备），无集群依赖 | 保留（回退） |
| 3 | certs | 逻辑独立（证书生成），无集群依赖 | 保留（回退） |
| 4 | load-balance | HA 组件，依赖 certs | 保留（回退） |

**混合执行模式**：

```go
func (r *BKEClusterReconciler) executePhaseFlow(ctx, phaseCtx, oldCluster, newCluster) {
    if r.shouldUseDeclarativeInstall(newCluster) {
        // Phase 2: 仅执行已迁移组件的 DAG
        r.executePartialInstallDAG(ctx, phaseCtx, oldCluster, newCluster,
            // 已迁移组件列表
            []string{"bkeagent", "nodes-env", "certs", "load-balance"},
        )
        // 未迁移组件仍走 PhaseFlow
        flow := phases.NewPhaseFlow(phaseCtx, 
            phases.WithSkipPhases("EnsureBKEAgent", "EnsureNodesEnv", "EnsureCerts", "EnsureLoadBalance"),
        )
        flow.CalculatePhase(oldCluster, newCluster)
        flow.Execute()
        return
    }
    // Legacy: 全部走 PhaseFlow
    flow := phases.NewPhaseFlow(phaseCtx)
    ...
}
```

**灰度策略**：
- Feature Gate `DeclarativeInstallEnabled` 默认 OFF
- 测试环境开启 Feature Gate，验证 DAG + PhaseFlow 混合模式
- 生产环境灰度：先在非核心集群开启，观察 1-2 周后扩大范围
- 回退方案：关闭 Feature Gate，立即回退到全 PhaseFlow

#### 9.4.5 Phase 3: 全量 DAG（v2.9.0）

**目标**：所有安装组件迁移到 DAG 路径，PhaseFlow 不再执行任何安装 Phase。

**迁移高风险组件**：

| 批次 | 组件 | 风险点 | 迁移措施 |
|------|------|--------|---------|
| 5 | kubernetes-master | kubeadm init 逻辑复杂 | 确保 `EnsureMasterInit` handler 完整覆盖 init + join |
| 6 | kubernetes-worker | kubeadm join + drain 逻辑 | 确保 `EnsureWorkerJoin` handler 完整 |
| 7 | kube-proxy/coredns | manifest 应用 | 复用升级路径的 YamlInstaller |
| 8 | agent-switch | Agent 监听切换 | 确保 `EnsureAgentSwitch` handler 幂等 |
| 9 | nodes-postprocess | 后置脚本 | 确保 `EnsureNodesPostProcess` handler 幂等 |

**执行入口**：

```go
func (r *BKEClusterReconciler) executePhaseFlow(ctx, phaseCtx, oldCluster, newCluster) {
    if r.shouldUseDeclarativeInstall(newCluster) {
        // Phase 3: 全量 DAG 执行所有安装组件
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
        // PhaseFlow 不再执行任何 DeployPhase
        // 但 CommonPhases (Finalizer/Paused/...) 仍走 PhaseFlow
        flow := phases.NewPhaseFlow(phaseCtx, phases.WithDeployPhasesSkipAll())
        flow.CalculatePhase(oldCluster, newCluster)
        flow.Execute()
        return
    }
    // Legacy: 全部走 PhaseFlow
    ...
}
```

**Feature Gate 状态**：ON（正式启用），但保留关闭能力作为回退。

#### 9.4.6 平滑升级风险控制

| 风险 | 缓解措施 |
|------|---------|
| **混合模式行为不一致** | Phase 2 中 DAG 和 PhaseFlow 混合执行，需确保组件间状态正确传递 |
| **部分迁移后中断** | Phase 2 迁移到一半发现问题，可关闭 Feature Gate 回退 |
| **v2.8.0 升级到 v2.9.0 时路径切换** | v2.9.0 安装走 DAG，但集群是从 v2.8.0（PhaseFlow）升级来的，需处理状态兼容 |
| **CommonPhases 仍依赖 PhaseFlow** | Finalizer/Paused/ClusterManage 等通用 Phase 仍走 PhaseFlow，需单独处理 |

#### 9.4.7 状态追踪迁移

PhaseFlow 使用 `PhaseStatus` 追踪进度，DAG 使用 `DeclarativeUpgradeStatus` 追踪进度。平滑升级期间需处理状态兼容：

| 阶段 | 安装状态来源 | 升级状态来源 | 兼容性处理 |
|------|------------|------------|-----------|
| Phase 1 | PhaseStatus | DeclarativeUpgradeStatus | 无变化 |
| Phase 2 | PhaseStatus（未迁移组件）+ DeclarativeUpgradeStatus（已迁移组件） | DeclarativeUpgradeStatus | DAG 执行前清理 PhaseStatus 中已迁移组件的状态 |
| Phase 3 | DeclarativeUpgradeStatus | DeclarativeUpgradeStatus | PhaseStatus 不再写入安装组件 |
| Phase 4 | DeclarativeUpgradeStatus | DeclarativeUpgradeStatus | 移除 PhaseStatus |

```go
// Phase 2: 混合模式状态清理
func (r *BKEClusterReconciler) executePartialInstallDAG(...) {
    // 执行 DAG 前，清理已迁移组件的 PhaseStatus
    for _, migratedComp := range migratedComponents {
        delete(bkeCluster.Status.PhaseStatus, migratedComp.PhaseName)
    }
    
    // 执行 DAG（已迁移组件）
    r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster, migratedComponents)
    
    // 执行 PhaseFlow（未迁移组件）
    // PhaseFlow 中 NeedExecute 会跳过已迁移组件（因为状态已在 DAG 中标记完成）
}
```

## 10. 可观测性

### 10.1 安装状态追踪

```bash
# 查询安装 DAG 执行进度
kubectl get bkecluster my-cluster -o jsonpath='{.status.declarativeUpgrade}'
# 输出:
# {
#   "targetVersion": "v2.7.0",
#   "completed": ["bkeagent", "nodes-env", "certs", "cluster-api-obj"],
#   "lastError": ""
# }

# 查询组件安装状态
kubectl get bkecluster my-cluster -o jsonpath='{.status.clusterComponentStatuses}'
# 输出:
# {
#   "bkeagent": {"phase": "Installed", "version": "v2.7.0"},
#   "certs": {"phase": "Installed", "version": "v2.7.0"},
#   "kubernetes-master": {"phase": "Pending", "version": "v1.36.0"}
# }
```

### 10.2 事件与指标

| 类型 | 来源 | 说明 |
|------|------|------|
| **InstallStarted/Completed/Failed** | BKECluster Controller | 安装操作事件 |
| **ComponentInstalled/Failed** | ComponentStatusUpdater | 组件安装事件 |
| **bke_cluster_phase** | Prometheus | 集群状态指标 |
| **bke_component_phase** | Prometheus | 组件状态指标 |

## 11. 工作量评估

### 11.1 开发工作量

| 阶段 | 模块 | 任务 | 工作量（人天） |
|------|------|------|---------------|
| **Phase 1: 结构扩展** | CRD 扩展 | `ReleaseImageComponent` 新增 `inline` 字段 + deepcopy + webhook | 2 |
| | 安装组件目录 | `DeclarativeInstallCatalog` + `InstallComponentSpec` | 2 |
| | ComponentFactory 注册 | 注册安装 handler 到 factory + 验证幂等性 | 2 |
| | VersionContext 扩展 | 新增 `DecisionInstall` + `BuildVersionContextForInstall` | 3 |
| | 安装 DAG 构建 | `BuildInstallDAGFromBundle` + `InstallComponentsFromBundle` + 循环依赖检测 | 3 |
| | executeInstallDAG | 安装 DAG 执行入口 + `shouldUseDeclarativeInstall` (两重门控) + 状态追踪 | 2 |
| | Feature Gate | `DeclarativeInstallEnabled` 实现 + 默认关闭验证 | 1 |
| | ComponentVersion 依赖定义 | 为 11 个安装组件编写 `spec.dependencies` + 验证拓扑正确性 | 4 |
| | ReleaseImage 适配 | 为 v2.7.0 ReleaseImage 补充 install.components.inline | 2 |
| | CommonPhases 兼容 | 确保 Finalizer/Paused/ClusterManage 等通用 Phase 与 DAG 共存 | 2 |
| | 安装中断恢复 | 部分组件安装失败后的断点续传 + DeclarativeUpgradeStatus 状态恢复 | 2 |
| | BKEAgent 命令适配 | 安装 handler 与现有 BKEAgent Command/ENV 命令机制集成验证 | 2 |
| | TemplateContext 扩展 | 新增 Operation/ScaleType/NodeCount 等字段 + `buildTemplateContext` 扩展 | 2 |
| | EvaluateCondition 实现 | Go Template 求值 + `shouldExecuteByCondition` Scheduler 集成 | 2 |
| | Inline 字段移除 (§15) | `BuildDAG` 签名重构 + `ComponentVersionLookup` + 删除 `enrichUpgradeComponent` + 删除 `ReleaseImageUpgradeInline` | 3 |
| **Phase 1 小计** | | | **37** |
| **Phase 2: 灰度迁移** | 混合执行模式 | `executePartialInstallDAG` + `WithSkipPhases` PhaseFlow 扩展 | 4 |
| | 状态追踪兼容 | PhaseStatus ↔ DeclarativeUpgradeStatus 状态清理 + 互不冲突 | 3 |
| | 低风险组件迁移 | bkeagent/nodes-env/certs/load-balance 迁移 + NeedExecute 适配 | 3 |
| | 灰度验证框架 | Feature Gate 灰度策略 + 日志 + 监控 | 2 |
| | 版本间状态迁移 | v2.7.0 PhaseStatus → v2.8.0 混合状态 → v2.9.0 DeclarativeUpgradeStatus 迁移逻辑 | 3 |
| **Phase 2 小计** | | | **15** |
| **Phase 3: 全量 DAG** | 高风险组件迁移 | kubernetes-master/worker/agent-switch/nodes-postprocess 迁移 | 5 |
| | MasterInit 幂等改造 | 区分 init/join + 已有节点跳过逻辑 + kubeadm init/join 命令适配 | 4 |
| | WorkerJoin drain 集成 | drain/uncordon 逻辑与 DAG 执行器集成 + 失败重试 | 3 |
| | DryRun DAG 化 | `ExecutionContext.DryRun` + 各执行器适配 | 2 |
| | 暂停检查迁移 | `shouldUseDeclarativeInstall` 增加暂停检查 | 1 |
| | CommonPhases DAG 化 | Finalizer/Paused 等通用 Phase 迁移或保留决策 + 实现 | 3 |
| **Phase 3 小计** | | | **18** |
| **开发总计** | | | **70** |

> **不包含 Phase 4**：PhaseFlow 兜底保留，后续再优化。Phase 4（纳管/扩容/删除 DAG 化 + NodeFilter + PhaseFlow 移除）不在本提案范围内。

### 11.2 测试工作量

| 阶段 | 测试内容 | 工作量（人天） |
|------|---------|---------------|
| **Phase 1** | DAG 构建 + VersionContext 单元测试 + 全新安装集成测试 + PhaseFlow 回归 + Condition 求值单元测试 | 9 |
| **Phase 2** | 混合模式执行 + 状态正确性 + Feature Gate 回退验证 | 5 |
| **Phase 3** | 全量 DAG 安装 + 升级流程 + DryRun 验证 | 7 |
| **测试总计** | | **21** |

### 11.3 文档工作量

| 文档类型 | 文档内容 | 工作量（人天） |
|---------|---------|---------------|
| **设计文档** | 本 KEP 文档完善 | 3 |
| **升级指南** | PhaseFlow → DAG 迁移指南 | 2 |
| **运维手册** | DAG 安装路径运维手册 | 1 |
| **故障排查** | DAG 安装故障排查指南 | 1 |
| **小计** | - | **7 人天** |

### 11.4 总工作量汇总

| 阶段 | 开发（人天） | 测试（人天） | 小计 |
|------|------------|------------|------|
| **Phase 1: 结构扩展** | 37 | 9 | 46 |
| **Phase 2: 灰度迁移** | 15 | 5 | 20 |
| **Phase 3: 全量 DAG** | 18 | 7 | 25 |
| **文档** | - | - | 7 |
| **总计** | **70** | **21** | **98** |

> 开发占比 71%，测试占比 21%，文档占比 7%。

**按 openFuyao 版本节奏估算**：

| openFuyao 版本 | 阶段 | 工作量（人天） | 说明 |
|---------------|------|---------------|------|
| **v2.7.0** | Phase 1: 结构扩展 | 46 | CRD 扩展 + 目录定义 + DAG 构建器 + Condition 过滤 + Inline 移除 |
| **v2.8.0** | Phase 2: 灰度迁移 | 20 | 低风险组件 DAG 化 + 混合模式 |
| **v2.9.0** | Phase 3: 全量 DAG | 25 | 高风险组件 DAG 化 + 全量验证 |
| **文档** | 全程 | 7 | 分阶段交付 |
| **总计** | - | **98** | 3 个版本周期 |

> **不包含 Phase 4**：PhaseFlow 兜底保留，后续再优化。

**按人员配置估算**（单阶段）：
- Phase 1（46 人天）：2 人约 4.5 周，3 人约 3 周
- Phase 2（20 人天）：2 人约 2 周，3 人约 1.5 周
- Phase 3（25 人天）：2 人约 2.5 周，3 人约 1.5 周

## 12. 风险与缓解措施

### 12.1 技术风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **安装 DAG 依赖不正确** | 组件安装顺序错误 | 中 | 充分测试依赖关系，与 PhaseFlow 顺序对齐 |
| **PhaseFlow 回归** | 新增 DAG 路径影响现有安装 | 低 | Feature Gate 控制，默认不启用 |
| **ComponentVersion 缺失** | 安装组件无依赖定义 | 中 | 补充所有安装组件的 `spec.dependencies` |
| **新旧路径行为不一致** | DAG 安装与 PhaseFlow 安装结果不同 | 中 | 对比测试，确保行为一致 |
| **MasterJoin 复用 MasterInit handler** | Master 加入使用 MasterInit 逻辑可能不匹配 | 中 | 验证 MasterInit handler 对已有 Master 的幂等性 |
| **混合模式状态冲突** | PhaseStatus 与 DeclarativeUpgradeStatus 同时写入 | 中 | 状态清理逻辑，执行前清理对方状态 |
| **CommonPhades 依赖 PhaseFlow** | Finalizer/Paused 等 Phase 嵌入 PhaseFlow | 中 | PhaseFlow 兜底保留，CommonPhases 继续走 PhaseFlow |

### 12.2 平滑升级风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **Phase 2 混合模式中断** | DAG + PhaseFlow 混合执行行为异常 | 高 | 逐组件迁移 + 充分测试 + Feature Gate 回退 |
| **版本间路径切换** | v2.8.0 PhaseFlow 安装 → v2.9.0 DAG 安装 | 中 | VersionContext 从 PhaseStatus 推导 Current |
| **灰度范围扩大过快** | 生产环境问题未充分暴露 | 中 | 每个灰度阶段观察 1-2 周 |

### 12.3 业务风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **业务 Pod 重启** | 业务短暂中断 | 高 | 选择业务低峰期升级 |
| **安装中断后重试** | 部分组件已安装，重试行为异常 | 中 | DeclarativeUpgradeStatus 追踪已完成组件，跳过重试 |
| **升级期间集群不可用** | 控制面组件重启 | 中 | 逐节点滚动升级，保持多数派可用 |

---

## 13. releasemanifest.Bundle 的作用

### 13.1 是什么

`releasemanifest.Bundle` 是 ReleaseImage OCI 制品的**内存解析表示**，是连接声明层 (ReleaseImage CR) 与执行层 (DAG/Scheduler/Phase) 的核心桥梁。

```go
// pkg/release/manifest/types.go

type Bundle struct {
    // Release: 解析后的 release.yaml (ReleaseImage CR 内容，非 CR 实例)
    // 含 Spec.Version, Spec.Install.Components, Spec.Upgrade.Components
    Release    apiv1.ReleaseImage

    // Components: 解析后的所有 component.yaml，key = "name@version"
    // 含 Spec.Type, Spec.Inline, Spec.Dependencies, Spec.Compatibility, Spec.Resources
    Components map[string]apiv1.ComponentVersion

    // Files: OCI 制品中所有 YAML 文件的原始字节，key = 相对路径
    // 如 "components/coredns/v1.11.1/coredns.yaml"
    Files map[string][]byte

    // Digest: 文件集 SHA256 (sha256:<hex>)
    Digest string

    // Source: 来源标识 "Memory" / "Disk" / "OCI"
    Source string

    // CacheFallback: 是否从磁盘缓存回退加载 (OCI 拉取失败时)
    CacheFallback bool
}
```

### 13.2 作用

Bundle 是声明式安装/升级流程中**所有执行决策的数据来源**：

| 作用 | Bundle 字段 | 消费者 | 说明 |
|------|------------|--------|------|
| **版本来源** | `Release.Spec.Install/Upgrade.Components[].Version` | `BuildVersionContextForUpgrade` | 提供 K8s/etcd 等组件版本，填充 VersionContext.Target/Current |
| **依赖解析** | `Components[key].Spec.Dependencies` | `BundleDependencyResolver` | 提供 DAG 拓扑排序的依赖边 |
| **执行器分发** | `Components[key].Spec.Type` (inline/yaml/helm/binary) | `Scheduler.executeComponent` | 决定用哪个 Executor 执行组件 |
| **inline handler 解析** | `Components[key].Spec.Inline.Handler` | `componentfactory.RegisterInlinePhasesFromBundle` | 提供 Phase Handler 名称 (如 `EnsureMasterUpgrade`) |
| **YAML 清单获取** | `Files["components/coredns/v1.11.1/coredns.yaml"]` | `BundleStore.GetComponentManifests` → `YamlInstaller` | 提供 manifest 原始字节供 Apply |
| **兼容性校验** | `Components[key].Spec.Compatibility.Constraints` | `compatibility.Engine.Check` | 提供版本约束 (如 kubernetes >=1.24.0) |
| **ReleaseImage Status 回填** | `Components` (数量和版本) | `ReleaseImageReconciler.componentStatuses` | 回写 ReleaseImage.Status.Components |
| **镜像 tag 来源** | `Release.Spec.Upgrade.Components[kubernetes-master].Version` | `EnsureMasterUpgrade` → Command CR → BKEAgent | 提供 manifest image tag 版本 (去 v 前缀) |

### 13.3 与 ReleaseImage CR 的关系

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              ReleaseImage CR ↔ Bundle 关系                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ReleaseImage CR (声明层 — 意图)                                                │
│  ┌──────────────────────────────────────────────────────────────────────┐      │
│  │ spec.version: "v2.7.0"                                               │      │
│  │ spec.digest: "sha256:abc123..."                                      │      │
│  │ spec.verifySignature: true                                           │      │
│  │ spec.install.components: [{name, version, inline}]                   │      │
│  │ spec.upgrade.components: [{name, version, inline}]                   │      │
│  │ status.phase: Valid / Invalid / ManifestMissing / CompatibilityFailed│     │
│  │ status.componentCount, status.components[], status.digest            │      │
│  └────────────────────────────────────┬─────────────────────────────────┘      │
│                                       │                                         │
│                  ReleaseImageReconciler:                                         │
│                  RefreshRelease → OCI Pull → ParseBundle → Check → Commit       │
│                                       │                                         │
│                                       ▼                                         │
│  Bundle (内存解析层 — 物化) ★                                                    │
│  ┌──────────────────────────────────────────────────────────────────────┐      │
│  │ Release:     release.yaml 解析结果 (含 Spec.Install/Upgrade)          │      │
│  │ Components:  所有 component.yaml 解析结果 (key="name@version")        │      │
│  │ Files:       所有 YAML 原始字节 (key=相对路径)                        │      │
│  │ Digest:      sha256:<hex>                                            │      │
│  │ Source:      Memory / Disk / OCI                                     │      │
│  └────────────────────────────────────┬─────────────────────────────────┘      │
│                                       │                                         │
│                  BKEClusterReconciler:                                           │
│                  ResolveRelease (只读，从不拉 OCI)                               │
│                                       │                                         │
│                                       ▼                                         │
│  执行层 (消费 Bundle):                                                           │
│    BuildVersionContextForUpgrade  ← Release (版本)                              │
│    BuildDAGFromBundle             ← Release + Components (依赖)                 │
│    BundleStore.GetComponentManifests ← Files (YAML 清单)                        │
│    BundleStore.GetComponentVersion  ← Components (类型/handler)                 │
│    Scheduler.ExecuteDAG            ← 以上全部                                   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**核心关系**：

| 维度 | ReleaseImage CR | Bundle |
|------|----------------|--------|
| **角色** | 声明层 (意图) — 声明版本和镜像位置 | 物化层 (制品) — 已拉取、解析、验证的实际内容 |
| **存储** | etcd (Kubernetes CR) | 进程内存 (sync.Map) + 磁盘缓存 |
| **创建者** | 用户/ClusterVersionReconciler | ReleaseImageReconciler (OCI Pull + ParseBundle) |
| **生命周期** | 持久化 (直到用户删除) | 进程级 (重启后从磁盘恢复) |
| **验证状态** | `Status.Phase` (Valid=已验证) | `Digest` + `Source` 标识 |
| **契约** | `Status.Phase=Valid` → 保证 Bundle 已缓存可用 | `ResolveRelease` 成功 → Bundle 可消费 |

> **关键设计**：`ResolveRelease` **从不拉取 OCI** — BKEClusterReconciler 只从内存/磁盘缓存读取，避免 reconcile 时网络阻塞。OCI 拉取仅由 ReleaseImageReconciler 在验证时执行。

### 13.4 三级缓存生命周期

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              Bundle 三级缓存生命周期                                              │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ReleaseImageReconciler.Reconcile (写入方):                                     │
│                                                                                 │
│  1. buildReleaseRefs (从 RI.Spec + imageref)                                    │
│     → ReleaseRef{Version, OCIRef, Digest, VerifySignature}                      │
│                                                                                 │
│  2. store.RefreshRelease(ctx, ref) — 总是拉取 OCI                               │
│     ├─ Puller.Pull → OCIPuller → ORAS 拉取 OCI 制品                             │
│     │  → BundleFiles{Files: map[string][]byte} (所有 YAML 原始字节)             │
│     ├─ verifier.Verify (签名验证)                                                │
│     ├─ ParseBundle(files) → *Bundle{Source=OCI}                                 │
│     │  ├─ 解析 release.yaml → bundle.Release (ReleaseImage CR 内容)             │
│     │  ├─ 解析 component.yaml → bundle.Components["name@version"]               │
│     │  └─ 保留所有 Files                                                         │
│     └─ (拉取失败 + AllowCacheFallback) → loadDiskCache → *Bundle{Source=Disk}   │
│                                                                                 │
│  3. compatibility.Engine.Check(bundle) — 兼容性校验                              │
│     → 读取 Components[key].Spec.Compatibility.Constraints                        │
│                                                                                 │
│  4. store.CommitRelease(ref, bundle, files) — 持久化                             │
│     ├─ memory.Store(key, bundle.DeepCopy()) — 写入内存缓存                       │
│     └─ writeDiskCache(ref, files, digest) — 写入磁盘缓存                         │
│        → <diskRoot>/<cacheKey>/  (YAML 树 + metadata.json)                       │
│                                                                                 │
│  5. updateStatus(ri, bundle) — 回填 ReleaseImage.Status                          │
│     → Status.Phase = Valid, Status.ComponentCount, Status.Components            │
│                                                                                 │
│  ─────────────────────────────────────────────────────────────                  │
│                                                                                 │
│  BKEClusterReconciler.executeUpgradeDAG (读取方):                               │
│                                                                                 │
│  1. resolveUpgradeBundle(ctx, cluster, hopTarget)                                │
│     ├─ ResolveReleaseImageForVersion → 查找 ReleaseImage CR (Phase=Valid)       │
│     └─ store.ResolveRelease(ctx, releaseRefFromCR(ri)) — 只读，从不拉 OCI       │
│        ├─ 优先: memory.Load(key) → *Bundle{Source=Memory} (热路径)              │
│        ├─ 回退: loadDiskCache(ref) → *Bundle{Source=Disk} (重启后)              │
│        └─ 失败: error "release bundle not cached" (RI 未验证)                    │
│                                                                                 │
│  2. 消费 Bundle:                                                                │
│     ├─ BuildVersionContextForUpgrade(bundle, currentBundle, bc)                 │
│     │  → FillTargetFromBundle: 遍历 bundle.Release.Spec.Install/Upgrade          │
│     │    .Components → vc.SetTarget(name, version)                              │
│     ├─ BuildDAGFromBundle(bundle, BundleDependencyResolver(bundle))             │
│     │  → 遍历 bundle.Release.Spec.Upgrade.Components                            │
│     │  → enrichUpgradeComponent: bundle.Components[key].Spec.Inline              │
│     │  → BundleDependencyResolver: bundle.Components[key].Spec.Dependencies      │
│     ├─ manifest.NewBundleStore(bundle)                                          │
│     │  → 实现 manifest.Store (GetComponentManifests → bundle.Files)             │
│     │  → 实现 dagexec.ComponentVersionStore (GetComponentVersion → bundle.Components)│
│     └─ componentfactory.NewFactoryFromBundle(bundle)                            │
│        → RegisterInlinePhasesFromBundle: bundle.Components[key].Spec.Inline      │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

| 缓存级别 | 存储位置 | 写入者 | 读取者 | 特点 |
|---------|---------|--------|--------|------|
| **Memory** | 进程内 `sync.Map` | `CommitRelease` | `ResolveRelease` (热路径) | 最快；进程重启丢失 |
| **Disk** | `/var/lib/bke/release-cache/<cacheKey>/` | `writeDiskCache` | `loadDiskCache` | 重启后可用；含 `metadata.json` |
| **OCI** | 远程 Registry | `RefreshRelease` (仅 ReleaseImageReconciler) | 不直接被 BKEClusterReconciler 读取 | 网络拉取；验证后缓存 |

**缓存键** (`CacheKey()`)：优先 `sanitizeKey(Digest)` → `sanitizeKey(Version)` → `sha256(OCIRef)`。Digest 变更时旧缓存被孤立 (ReleaseImage 删除时 EvictRelease 清理)。

### 13.5 BundleStore 适配器 — Bundle 到执行层的桥梁

Bundle 不直接被 Scheduler 消费，而是通过 `manifest.BundleStore` 适配器转换为执行层所需接口：

```go
// pkg/manifest/bundle_store.go

// BundleStore 包装 Bundle，同时实现两个接口
type BundleStore struct {
    bundle *releasemanifest.Bundle
}

// NewBundleStore 创建 BundleStore
func NewBundleStore(bundle *releasemanifest.Bundle) *BundleStore {
    return &BundleStore{bundle: bundle}
}

// 实现 manifest.Store 接口 (供 YamlInstaller 使用)
func (s *BundleStore) GetComponentManifests(ctx, name, version string, tmpl) (*ComponentPackage, error) {
    // 1. 验证 ComponentVersion 存在
    cv, ok := s.bundle.Components[releasemanifest.ComponentKey(name, version)]
    if !ok {
        return nil, fmt.Errorf("component %s@%s not found", name, version)
    }
    // 2. 从 bundle.Files 收集 YAML 清单字节
    manifests := releasemanifest.CollectComponentManifests(s.bundle, name, version)
    // 3. 返回 ComponentPackage (含 Manifests + ApplyStrategy)
    return &ComponentPackage{
        Name:          name,
        Version:       version,
        Manifests:     manifests,
        ApplyStrategy: cv.Spec.YAML.ApplyStrategy,
    }, nil
}

// 实现 dagexec.ComponentVersionStore 接口 (供 Scheduler 使用)
func (s *BundleStore) GetComponentVersion(ctx, name, version string) (*apiv1.ComponentVersion, error) {
    cv, ok := s.bundle.Components[releasemanifest.ComponentKey(name, version)]
    if !ok {
        return nil, fmt.Errorf("component %s@%s not found", name, version)
    }
    return &cv, nil  // 返回 ComponentVersion (含 Spec.Type, Spec.Inline 等)
}
```

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              BundleStore 适配器 — Bundle 到执行层的桥梁                          │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  *releasemanifest.Bundle                                                        │
│    ├── Release (版本)                                                           │
│    ├── Components (ComponentVersion: Type/Inline/Dependencies)                  │
│    └── Files (YAML 清单字节)                                                    │
│                                                                                 │
│           │                                                                     │
│           │ manifest.NewBundleStore(bundle)                                     │
│           ▼                                                                     │
│                                                                                 │
│  *manifest.BundleStore (适配器)                                                 │
│    │                                                                            │
│    ├── 实现 manifest.Store 接口:                                                 │
│    │   GetComponentManifests(ctx, name, version, tmpl)                          │
│    │   → 从 bundle.Components 验证存在                                          │
│    │   → 从 bundle.Files 收集 YAML 清单                                         │
│    │   → 返回 ComponentPackage{Manifests, ApplyStrategy}                        │
│    │   消费者: YamlInstaller / YamlComponentExecutor                            │
│    │                                                                            │
│    └── 实现 dagexec.ComponentVersionStore 接口:                                 │
│        GetComponentVersion(ctx, name, version)                                  │
│        → 从 bundle.Components[key] 返回 *ComponentVersion                       │
│        → Scheduler 读取 cv.Spec.Type 决定执行器                                 │
│        → BuildDAG 通过 ComponentVersionLookup 读取 cv.Spec.Inline (§15)         │
│        消费者: Scheduler.executeComponent / topology.BuildDAG                   │                                        │
│                                                                                 │
│           │                                                                     │
│           │ dagexec.NewScheduler(Config{                                         │
│           │   ManifestStore: bundleStore,  // → bundle.Files                    │
│           │   CVStore:        bundleStore,  // → bundle.Components              │
│           │   InlineRunner:   ...,         // → bundle.Components (handler)    │
│           │})                                                                  │
│           ▼                                                                     │
│                                                                                 │
│  Scheduler.ExecuteDAG                                                           │
│    对每个组件:                                                                   │
│    1. CVStore.GetComponentVersion → cv.Spec.Type → 选择执行器                   │
│    2. inline → cv.Spec.Inline → InlineRunner.Execute(handler) (§15 重构)       │
│       manifest → ManifestStore.GetComponentManifests → Applier.ApplyComponent   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 13.6 Bundle 的消费者汇总

| 消费者 | 读取字段 | 用途 | 代码位置 |
|--------|---------|------|---------|
| `BuildVersionContextForUpgrade` | `Release.Spec.Install/Upgrade.Components` | 填充 VersionContext.Target/Current | `pkg/upgrade/build_release.go` |
| `BuildDAGFromBundle` / `BuildInstallDAGFromBundle` | `Release.Spec.Upgrade/Install.Components` | 构建 DAG 组件列表 (均调用 `topology.BuildDAG`) | `pkg/upgrade/bundle.go` |
| `BundleDependencyResolver` | `Components[key].Spec.Dependencies` | 解析 DAG 依赖边 | `pkg/upgrade/bundle.go` |
| `enrichUpgradeComponent` | ~~`Components[key].Spec.Inline`~~ | ~~补充 inline handler 信息~~ (§15 移除) | `pkg/upgrade/bundle.go` |
| `topology.BuildDAG` | `Components[key].Spec.Inline` (通过 `ComponentVersionLookup`) | 读取 inline handler 构建 `ComponentNode.Inline` (§15 重构) | `pkg/topology/build.go` |
| `BundleStore.GetComponentManifests` | `Components` + `Files` | 获取 YAML 清单字节 | `pkg/manifest/bundle_store.go` |
| `BundleStore.GetComponentVersion` | `Components` | 获取组件类型和 handler | `pkg/manifest/bundle_store.go` |
| `componentfactory.NewFactoryFromBundle` | `Components` + `Release` | 注册 inline Phase 构造函数 | `pkg/componentfactory/bundle_registry.go` |
| `compatibility.Engine.Check` | `Components[key].Spec.Compatibility` | 版本兼容性校验 | `pkg/release/compatibility/engine.go` |
| `ReleaseImageReconciler.componentStatuses` | `Components` | 回填 ReleaseImage.Status | `controllers/releaseimage/releaseimage_controller.go` |
| `CollectComponentManifests` | `Files` + `Components` | 收集组件 manifest 清单 | `pkg/release/manifest/component_files.go` |

### 13.7 命名注意事项

代码库中存在**两个不同的 "Store" 类型**，容易混淆：

| 类型 | 包 | 作用 | 说明 |
|------|-----|------|------|
| `releasemanifest.Store` | `pkg/release/manifest` | Release Bundle 缓存 (三级: 内存/磁盘/OCI) | 持有 `*Bundle`，被 ReleaseImageReconciler (写) 和 BKEClusterReconciler (读) 共享 |
| `manifest.Store` | `pkg/manifest` | 组件清单加载接口 (抽象) | 接口: `GetComponentManifests(ctx, name, version)`，由 `manifest.BundleStore` 实现 (包装 Bundle) |

> `manifest.BundleStore` 是适配器：将 `*releasemanifest.Bundle` 包装为 `manifest.Store` + `dagexec.ComponentVersionStore` 两个接口，供 Scheduler 消费。

---

## 14. ComponentVersion 执行时条件过滤

ComponentVersion 新增 `Condition` 字段（Go Template 表达式），在 DAG 执行时根据集群运行时状态（Operation/ScaleType/NodeCount 等）决定组件是否执行。该机制复用已有 `TemplateContext` 作为模板数据（扩展新增 Operation/ScaleType 等字段），作为 Scheduler 跳过链的第三层检查（位于版本检查之后、执行器分发之前）。

**Condition 表达式语法**（Go Template）：

| 表达式 | 含义 | 示例场景 |
|--------|------|---------|
| `{{ eq .Operation "scale" }}` | 仅扩容时执行 | 扩容专用组件 |
| `{{ eq .ScaleType "master" }}` | 仅 Master 扩容时执行 | Master 专用组件 |
| `{{ eq .Operation "install" }}` | 仅安装时执行 | 安装专用组件 |
| `{{ eq .Operation "upgrade" }}` | 仅升级时执行 | 升级专用组件 |
| `{{ gt .NodeCount 3 }}` | 节点数 > 3 时执行 | 大集群专用组件 |
| (空) | 始终执行 | 默认行为 (向后兼容) |

**TemplateContext 扩展**（`manifest.TemplateContext`，集群状态安全投影）：

```go
type TemplateContext struct {
    // --- 现有字段 (manifest 渲染) ---
    ClusterName       string
    Namespace         string
    KubernetesVersion string
    OpenFuyaoVersion  string

    // --- 新增字段 (Condition 求值 + manifest 渲染) ---
    Operation   string  // install / upgrade / scale / rollback / manage
    ScaleType   string  // master / worker / "" (非扩容)
    NodeCount   int
    MasterCount int
    WorkerCount int
    DeployMode  string  // offline / online
    DryRun      bool
    Variables   map[string]string
}
```

**Operation 由调用方设置**（非从 BKECluster CR 推断）：

| 调用方 | Operation | ScaleType |
|--------|-----------|-----------|
| `executeInstallDAG` | `"install"` | `""` |
| `executeUpgradeDAG` | `"upgrade"` | `""` |
| `executeScaleDAG` | `"scale"` | `"master"` / `"worker"` |
| `executeManageDAG` | `"manage"` | `""` |
| `executeUninstallDAG` | `"rollback"` | `""` |

节点级过滤（`ComponentVersion.Spec.NodeFilter`，按角色/标签/幂等过滤目标节点）作为 Executor 层的节点选择机制，与 Condition 互补——Condition 是组件级过滤（决定组件是否执行），NodeFilter 是节点级过滤（决定组件在哪些节点上执行）。

**Scheduler 跳过链**：

```
组件执行检查链 (executeBatchParallel):
  (A) shouldSkipComponent         → DeclarativeUpgradeStatus.IsCompleted → Skip
  (B) componentNeedsUpgrade       → VersionContext.NeedsExecution → Current==Target → Skip
  (C) shouldExecuteByCondition    → EvaluateCondition(cv.Spec.Condition, tmpl) → false → Skip
  (D) executeComponent            → 分发到 Inline/YAML/Helm/Binary Executor
```

---

## 15. ReleaseImageUpgradeComponent.Inline 字段移除设计

### 15.1 设计动机

`ReleaseImageUpgradeComponent.Inline`（`api/v1alpha1/releaseimage_types.go:65`）与 `ComponentVersion.Spec.Inline`（`api/v1alpha1/componentversion_types.go:36`）数据完全冗余——两者形状相同（`{Handler, Version}`），且 `enrichUpgradeComponent`（`pkg/upgrade/bundle.go:93`）只是将 `ComponentVersion.Spec.Inline` 复制到 `ReleaseImageUpgradeComponent.Inline`。

**违背设计原则**：本提案已确立"组件定义性字段统一存储在 `ComponentVersion.Spec`，`ReleaseImageUpgradeComponent` 保持轻量引用角色"。Inline handler 属于组件定义性字段，应存储在 `ComponentVersion.Spec.Inline`，不应在 `ReleaseImageUpgradeComponent` 上冗余。

### 15.2 现状分析

**数据流**：

```
component.yaml → ComponentVersion.Spec.Inline (权威来源)
                      │
                      │ enrichUpgradeComponent (bundle.go:93) 复制
                      ▼
release.yaml → ReleaseImageUpgradeComponent.Inline (冗余副本)
                      │
                      │ BuildUpgradeDAG (build.go:42) 复制
                      ▼
                ComponentNode.Inline (InlineRef, DAG 节点)
                      │
                      │ Scheduler / InlineComponentExecutor 消费
                      ▼
                InlineRunner.Execute(handler, version)
```

**Inline 字段的 2 个消费者**：

| 消费者 | 代码位置 | 读取内容 | 能否访问 ComponentVersion？ |
|--------|---------|---------|-------------------------|
| `topology.BuildUpgradeDAG` | `build.go:42` | `comp.Inline` → 复制到 `ComponentNode.Inline` | ❌ 不能（签名只有 `[]ReleaseImageUpgradeComponent`，无 Bundle） |
| `RegisterInlinePhasesFromBundle` | `bundle_registry.go:45` | `comp.Inline.Handler/Version` | ✅ 能（有 Bundle），但当前读的是 `comp.Inline` |

**`enrichUpgradeComponent` 的复制逻辑**（`bundle.go:93`）：

```go
func enrichUpgradeComponent(comp, bundle) {
    if comp.Inline != nil || bundle == nil {
        return comp  // release.yaml 的 inline 优先 (override)
    }
    cv := bundle.Components[key]
    enriched.Inline = &ReleaseImageUpgradeInline{
        Handler: cv.Spec.Inline.Handler,  // 从 ComponentVersion 复制
        Version: cv.Spec.Inline.Version,
    }
}
```

**override 语义**：`comp.Inline != nil` 时 early return，允许 `release.yaml` 的 `upgrade.components[].inline` 覆盖 `component.yaml` 的 `spec.inline`。此 override 能力将被移除——inline handler 是组件实现细节，应在 `component.yaml` 中定义，不应在 `release.yaml` 中覆盖。

**`ComponentVersion.Spec.Inline` 的唯一非测试读者就是 `enrichUpgradeComponent`**——Scheduler 执行时通过 `CVStore.GetComponentVersion` 读取的是 `cv.Spec.Type`（决定执行器类型），不读 `cv.Spec.Inline`。

### 15.3 移除方案

#### 15.3.1 `BuildUpgradeDAG` 签名重构

`BuildUpgradeDAG` 当前只接收 `[]ReleaseImageUpgradeComponent`，无 Bundle/ComponentVersion 访问权限，inline 信息必须在调用前"扁平化"到组件引用上。重构为接收 `ComponentVersion` 查找函数：

```go
// pkg/topology/build.go — 重构前

func BuildDAG(
    components []cvv1alpha1.ReleaseImageUpgradeComponent,
    resolve DependencyResolver,
) (*ComponentDAG, error) {
    for _, comp := range components {
        node := &ComponentNode{
            Name:    comp.Name,
            Version: comp.Version,
        }
        if comp.Inline != nil {  // ★ 从 ReleaseImageUpgradeComponent.Inline 读取
            node.Inline = &InlineRef{
                Handler: comp.Inline.Handler,
                Version: comp.Inline.Version,
            }
        }
        dag.AddNode(node)
    }
    // ...
}
```

```go
// pkg/topology/build.go — 重构后

// ComponentVersionLookup returns the ComponentVersion for a given name+version.
// Implemented by BundleStore / manifest.BundleStore.
type ComponentVersionLookup func(name, version string) (*cvv1alpha1.ComponentVersion, bool)

// BuildDAG builds a component DAG from ReleaseImage components.
// Inline handler info is read from ComponentVersion.Spec.Inline via the lookup,
// not from ReleaseImageUpgradeComponent.Inline (which is removed).
func BuildDAG(
    components []cvv1alpha1.ReleaseImageUpgradeComponent,
    resolve DependencyResolver,
    cvLookup ComponentVersionLookup,  // ★ 新增参数
) (*ComponentDAG, error) {
    for _, comp := range components {
        node := &ComponentNode{
            Name:    comp.Name,
            Version: comp.Version,
        }
        // ★ 从 ComponentVersion.Spec.Inline 读取 (而非 comp.Inline)
        if cvLookup != nil {
            if cv, ok := cvLookup(comp.Name, comp.Version); ok && cv.Spec.Inline != nil {
                node.Inline = &InlineRef{
                    Handler: cv.Spec.Inline.Handler,
                    Version: cv.Spec.Inline.Version,
                }
            }
        }
        dag.AddNode(node)
    }
    // ...
}
```

**`ComponentVersionLookup` 的实现**（复用 Bundle.Components）：

```go
// pkg/upgrade/bundle.go — 提供 ComponentVersionLookup 适配器

// BundleComponentVersionLookup creates a ComponentVersionLookup from a Bundle.
func BundleComponentVersionLookup(bundle *releasemanifest.Bundle) topology.ComponentVersionLookup {
    if bundle == nil {
        return nil
    }
    return func(name, version string) (*cvv1alpha1.ComponentVersion, bool) {
        cv, ok := bundle.Components[releasemanifest.ComponentKey(name, version)]
        return &cv, ok
    }
}
```

**调用方更新**：

```go
// pkg/upgrade/bundle.go — BuildDAGFromBundle 更新

func BuildDAGFromBundle(bundle *releasemanifest.Bundle, resolve topology.DependencyResolver) (*topology.UpgradeDAG, error) {
    components, err := UpgradeComponentsFromBundle(bundle)
    if err != nil {
        return nil, err
    }
    return topology.BuildDAG(
        components,
        topology.MergeDependencyResolver(resolve, topology.DefaultDependencyResolver()),
        BundleComponentVersionLookup(bundle),  // ★ 新增: 传入 CV 查找函数
    )
}
```

```go
// pkg/upgrade/bundle.go — BuildInstallDAGFromBundle 更新

func BuildInstallDAGFromBundle(bundle *releasemanifest.Bundle, resolve topology.DependencyResolver) (*topology.UpgradeDAG, error) {
    installComponents, err := InstallComponentsFromBundle(bundle)
    if err != nil {
        return nil, err
    }
    return topology.BuildDAG(
        installComponents,
        resolve,
        BundleComponentVersionLookup(bundle),  // ★ 新增: 传入 CV 查找函数
    )
}
```

```go
// pkg/upgrade/releaseimage.go — BuildDAGFromReleaseImage 更新

func BuildDAGFromReleaseImage(ri *cvv1alpha1.ReleaseImage, resolve topology.DependencyResolver) (*topology.UpgradeDAG, error) {
    // ... 现有逻辑 ...
    return topology.BuildDAG(
        ri.Spec.Upgrade.Components,
        topology.MergeDependencyResolver(resolve, topology.DefaultDependencyResolver()),
        nil,  // ★ 无 Bundle, inline 信息不可用 (此函数用于无 Bundle 场景)
    )
}
```

#### 15.3.2 `RegisterInlinePhasesFromBundle` 重构

当前从 `comp.Inline` 读取 handler，改为从 `cv.Spec.Inline` 读取（已有 Bundle 访问权限）：

```go
// pkg/componentfactory/bundle_registry.go — 重构后

func RegisterInlinePhasesFromBundle(f *ComponentFactory, bundle *releasemanifest.Bundle) error {
    if bundle == nil || bundle.Release.Spec.Upgrade == nil {
        return nil
    }

    for _, comp := range bundle.Release.Spec.Upgrade.Components {
        // ★ 从 ComponentVersion.Spec.Inline 读取 (而非 comp.Inline)
        cv, ok := bundle.Components[releasemanifest.ComponentKey(comp.Name, comp.Version)]
        if !ok || cv.Spec.Inline == nil || cv.Spec.Inline.Handler == "" {
            continue
        }
        handler := cv.Spec.Inline.Handler
        version := cv.Spec.Inline.Version
        if err := registerInlineComponent(f, handler, version, cv.DeepCopy()); err != nil {
            return fmt.Errorf("register inline handler %q: %w", handler, err)
        }
    }
    return nil
}
```

#### 15.3.3 删除 `enrichUpgradeComponent`

`enrichUpgradeComponent`（`bundle.go:93`）的唯一作用是将 `ComponentVersion.Spec.Inline` 复制到 `ReleaseImageUpgradeComponent.Inline`。移除 inline 字段后此函数无存在意义，直接删除。

```go
// pkg/upgrade/bundle.go — 删除 enrichUpgradeComponent

// UpgradeComponentsFromBundle 简化: 不再调用 enrichUpgradeComponent
func UpgradeComponentsFromBundle(bundle *releasemanifest.Bundle) ([]cvv1alpha1.ReleaseImageUpgradeComponent, error) {
    if bundle == nil {
        return nil, fmt.Errorf("release bundle is nil")
    }
    if bundle.Release.Spec.Upgrade == nil || len(bundle.Release.Spec.Upgrade.Components) == 0 {
        return nil, fmt.Errorf("release bundle has no upgrade components")
    }
    // ★ 直接返回, 不再 enrich (inline 信息从 ComponentVersion 读取)
    return bundle.Release.Spec.Upgrade.Components, nil
}
```

#### 15.3.4 删除 `ReleaseImageUpgradeComponent.Inline` 字段

```go
// api/v1alpha1/releaseimage_types.go — 重构后

// ReleaseImageUpgradeComponent is one upgradable component.
// Inline handler info is read from ComponentVersion.Spec.Inline at execution time,
// not stored on this reference type.
type ReleaseImageUpgradeComponent struct {
    Name    string `json:"name,omitempty"`
    Version string `json:"version,omitempty"`
    // ★ Inline 字段移除 — 从 ComponentVersion.Spec.Inline 读取
}

// ReleaseImageUpgradeInline 类型删除 (不再使用)
```

### 15.4 影响范围

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `api/v1alpha1/releaseimage_types.go` | 删除字段+类型 | 移除 `ReleaseImageUpgradeComponent.Inline` 字段 + `ReleaseImageUpgradeInline` 类型 |
| `api/v1alpha1/zz_generated.deepcopy.go` | 重新生成 | `make generate` 自动处理（移除 `ReleaseImageUpgradeInline` 的 DeepCopy） |
| `config/crd/bases/...releaseimages.yaml` | 重新生成 | `make manifests` 自动处理（CRD schema 移除 `upgrade.components[].inline`） |
| `pkg/topology/build.go` | 签名重构 | `BuildDAG` 新增 `ComponentVersionLookup` 参数，内部读 `cv.Spec.Inline` |
| `pkg/topology/build_test.go` | 测试适配 | 传入 mock `ComponentVersionLookup` |
| `pkg/upgrade/bundle.go` | 删除+新增 | 删除 `enrichUpgradeComponent`，新增 `BundleComponentVersionLookup`，更新 `BuildDAGFromBundle`/`BuildInstallDAGFromBundle` 调用 |
| `pkg/upgrade/bundle_test.go` | 测试适配 | 移除 `enrichUpgradeComponent` 相关测试，更新 `BuildDAGFromBundle` 测试 |
| `pkg/upgrade/releaseimage.go` | 调用更新 | `BuildDAGFromReleaseImage` 传入 `nil`（无 Bundle） |
| `pkg/componentfactory/bundle_registry.go` | 重构 | 从 `cv.Spec.Inline` 读取（而非 `comp.Inline`） |
| `pkg/componentfactory/bundle_registry_test.go` | 测试适配 | 更新测试用例 |
| `pkg/dagexec/scheduler_skip_test.go` | 测试适配 | `BuildDAG` 调用新增 `cvLookup` 参数 |

### 15.5 向后兼容性

| 场景 | 影响 | 说明 |
|------|------|------|
| 现有 `release.yaml` 含 `upgrade.components[].inline` | 字段被忽略 | `Inline` 字段移除后，JSON 解析时 `inline` 字段被忽略（Go json 反序列化忽略未知字段） |
| 现有 `component.yaml` 含 `spec.inline` | ✅ 正常工作 | `ComponentVersion.Spec.Inline` 不变，是权威来源 |
| `release.yaml` 用 `inline` 覆盖 `component.yaml` | ❌ override 能力移除 | 移除 `enrichUpgradeComponent` 后，`release.yaml` 的 `inline` 不再生效 |
| CRD YAML | 重新生成 | `make manifests` 后 CRD schema 移除 `upgrade.components[].inline` 属性 |
| DeepCopy 代码 | 重新生成 | `make generate` 后自动移除 `ReleaseImageUpgradeInline` 的 DeepCopy 方法 |

> **override 能力移除说明**：当前 `enrichUpgradeComponent` 中 `comp.Inline != nil` 时 early return，允许 `release.yaml` 覆盖 `component.yaml` 的 inline handler。移除 inline 字段后此能力消失。设计决策：inline handler 是组件实现细节，应在 `component.yaml` 中定义，不应在 `release.yaml` 中覆盖。`release.yaml` 的职责是声明"哪些组件参与本次发布"（Name + Version），不是定义"组件如何执行"。

### 15.6 与设计原则的一致性

此重构与以下设计原则保持一致：

| 原则 | 来源 | 一致性 |
|------|------|--------|
| `ReleaseImageUpgradeComponent` 保持轻量引用角色 | 本提案 §2.3 | ✅ 进一步轻量化，仅保留 Name + Version |
| 组件定义性字段统一存储在 `ComponentVersion.Spec` | 本提案 §2.3 | ✅ Inline handler 回归 `ComponentVersion.Spec.Inline` |
| `ReleaseImageUpgradeComponent` 不携带 Condition | 本提案 §14 | ✅ 同理不携带 Inline |
| `ReleaseImageUpgradeComponent` 不携带 NodeFilter | 本提案 §14 | ✅ 同理不携带 NodeFilter |
| DAG 构建与组件定义解耦 | §4.3 | ✅ DAG 构建通过 `ComponentVersionLookup` 读取定义，不依赖引用类型携带定义 |

**重构后 `ReleaseImageUpgradeComponent` 的字段**：

```go
// 重构后: 仅 Name + Version, 与 ReleaseImageInstallComponent 完全一致
type ReleaseImageUpgradeComponent struct {
    Name    string `json:"name,omitempty"`
    Version string `json:"version,omitempty"`
}

// ReleaseImageInstallComponent 也是 Name + Version (现有, 不变)
type ReleaseImageInstallComponent struct {
    Name    string `json:"name,omitempty"`
    Version string `json:"version,omitempty"`
}
```

两个类型字段完全一致，为 §4.3 的统一抽象（`type ReleaseImageComponent = ReleaseImageUpgradeComponent`）创造了条件——移除 Inline 后统一抽象不再有字段差异。

---

## 附录

### A. 参考文档

1. [KEP-5 声明式升级框架](../kep5/kep5.md)
2. Cluster API v1beta1 文档
3. Go text/template 文档: https://pkg.go.dev/text/template

### B. 术语表

| 术语 | 定义 |
|------|------|
| **ReleaseImageComponent** | ReleaseImage 中组件引用（安装和升级共用），包含 `inline` handler |
| **DeclarativeInstallCatalog** | 安装组件目录，映射组件名到执行模式（inline/manifest） |
| **topology.BuildDAG** | 通用 DAG 构建器（原 `BuildUpgradeDAG`，type alias 兼容），安装和升级共用，仅组件来源不同 |
| **topology.ComponentDAG** | 通用 DAG 类型（原 `UpgradeDAG`，type alias 兼容），安装和升级共用 |
| **BuildDAGFromBundle** | 从 upgrade.components 提取组件并调用 `topology.BuildDAG` 构建 DAG |
| **BuildInstallDAGFromBundle** | 从 install.components 提取组件并调用 `topology.BuildDAG` 构建 DAG |
| **DecisionInstall** | VersionContext 决策：current 为空且 target 有值时触发安装 |
| **DeclarativeInstallEnabled** | Feature Gate，控制 DAG 安装路径是否启用。开启后强制要求 ReleaseImage 就绪 |
| **ComponentVersionLookup** | DAG 构建时查找 ComponentVersion 的函数类型，用于从 `ComponentVersion.Spec.Inline` 读取 inline handler (§15) |

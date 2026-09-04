# KEP-10: ReleaseImage 安装组件声明式定义设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-10 |
| **标题** | ReleaseImage 安装组件声明式定义与 DAG 驱动安装流程设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **依赖** | KEP-5 声明式升级框架、KEP-6 三层状态机设计、KEP-9 Static Pod 类型设计 |

---

## 1. 摘要

本提案设计 ReleaseImage 安装组件的声明式定义，使安装流程与升级流程统一为 DAG 驱动。当前 ReleaseImage 的 `spec.install.components` 仅包含 `{name, version}` 两个字段，缺少执行模式（inline/manifest/staticpod）、inline handler、依赖关系等声明式元数据；且安装流程完全由硬编码的 `DeployPhases` PhaseFlow 驱动，未消费 ReleaseImage 的安装组件列表。本提案将 `ReleaseImageInstallComponent` 和 `ReleaseImageUpgradeComponent` 统一抽象为 `ReleaseImageComponent`，新增 `inline` 字段，新增安装组件目录（`DeclarativeInstallCatalog`）和安装 DAG 构建器，使安装流程也能享受声明式组件管理的优势：依赖管理、并行执行、版本追踪、可观测性。

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

### 4.0 设计思路

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

### 4.1 当前结构（不对称）

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

### 4.2 目标结构（两层统一抽象）

经过分析，ReleaseImage 的类型定义存在两层重复：

**第一层重复**：`ReleaseImageInstallComponent` 和 `ReleaseImageUpgradeComponent` 字段完全相同
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

### 4.3 ReleaseImage YAML 示例（完整安装 + 升级）

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

### 5.0 DeclarativeUpgradeCatalog 的作用 (现有升级组件目录)

在说明安装组件目录 `DeclarativeInstallCatalog` 之前，先理解现有升级组件目录 `DeclarativeUpgradeCatalog` 的作用，因为安装目录是它的对称设计。

#### 5.0.1 是什么

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

#### 5.0.2 解决什么问题

| 问题 | 没有 Catalog 时 | 有 Catalog 后 |
|------|----------------|--------------|
| **组件名 → 执行模式映射** | DAG 调度器收到 `ComponentNode{Name: "kubernetes-master"}` 后，不知道该用 inline 还是 manifest 执行 | 从 Catalog 查表：`Mode=UpgradeExecutionInline`，使用 inline 执行器 |
| **组件名 → inline handler 映射** | inline 类型组件需要知道调用哪个 Phase Handler (如 `EnsureMasterUpgrade`) | 从 Catalog 查表：`InlineHandler="EnsureMasterUpgrade"` |
| **组件名 → manifest 路径映射** | manifest 类型组件需要知道 YAML 清单路径 | 从 Catalog 查表：`ManifestPath="coredns/v1.0.0/component.yaml"` |
| **组件名 → legacy Phase 映射** | 声明式升级与 legacy PhaseFlow 共存时需互相转换 | 从 Catalog 查表：`LegacyPhase="EnsureMasterUpgrade"` |
| **新增组件的注册点** | 新增组件需修改 DAG 构建代码、Phase 注册代码等多处 | 只需在 Catalog 中添加一条记录 + 注册 handler |

#### 5.0.3 在升级流程中的角色

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
│           ▼ BuildUpgradeDAGFromBundle(bundle, resolver)                         │
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

#### 5.0.4 Catalog 与 ReleaseImage 的分工

| 维度 | ReleaseImage (声明层) | DeclarativeUpgradeCatalog (映射层) |
|------|----------------------|-----------------------------------|
| **定义位置** | OCI Bundle 中的 `release.yaml` | `pkg/upgrade/catalog.go` 中的 Go `var` |
| **变更频率** | 每次发布变更 (版本号、组件列表) | 很少变更 (仅新增组件时添加条目) |
| **内容** | 组件名 + 版本号 + inline handler (如声明) | 组件名 → 执行模式 + handler + manifest 路径 + legacy Phase |
| **作用** | 声明"升级到什么版本" | 映射"用什么方式执行升级" |
| **关系** | ReleaseImage 组件名是 Catalog 的查找键 | Catalog 提供执行所需的元数据 |

> **分工原则**：ReleaseImage 声明**什么版本** (What)，Catalog 映射**怎么执行** (How)。

#### 5.0.5 Catalog 的消费者

| 消费者 | 用途 | 代码位置 |
|--------|------|---------|
| `BuildUpgradeDAGFromBundle()` | 构建 DAG 时查找组件的执行模式和 inline handler | `pkg/topology/build.go` |
| `InlineUpgradeHandlers()` | 返回所有 inline handler 名称列表，供 ComponentFactory 注册 | `pkg/upgrade/catalog.go:125` |
| `Scheduler.executeComponent()` | 根据 Catalog 的 Mode 选择 inline/manifest 执行器 | `pkg/dagexec/scheduler.go` |
| `PhaseFlow.CalculatePhase()` | 旧路径中用 `LegacyPhase` 字段判断是否跳过 legacy Phase | `pkg/phaseframe/phases/phase_flow.go` |

### 5.1 DeclarativeInstallCatalog

#### 5.1.1 复用 UpgradeComponentSpec — 不新增类型

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

#### 5.1.2 DeclarativeInstallCatalog 定义

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
    {Name: "kube-proxy", Mode: ExecutionManifest, ManifestPath: "kube-proxy/{version}/component.yaml"},
    {Name: "coredns",    Mode: ExecutionManifest, ManifestPath: "coredns/{version}/component.yaml"},
}
```

#### 5.1.3 复用的收益

| 维度 | 新增 `InstallComponentSpec` (原方案) | 复用 `ComponentSpec` (改进方案) |
|------|-------------------------------------|--------------------------------|
| 类型定义 | 新增 `InstallComponentSpec` + `InstallExecutionMode` | **零新增** (复用现有类型) |
| 常量定义 | 新增 `InstallExecutionManifest` + `InstallExecutionInline` | **零新增** (复用 `ExecutionManifest` + `ExecutionInline`) |
| Catalog 查找函数 | 需为 Install 写一套 `findInInstallCatalog()` | **复用** `findInCatalog()` (泛型于 install/upgrade) |
| `InlineHandlers()` 函数 | 需新增 `InlineInstallHandlers()` | **复用** `InlineUpgradeHandlers()` 逻辑 (改为 `InlineHandlers(catalog)`) |
| 新增组件 | 需在两个 Catalog 中各加一条 | 按需在对应 Catalog 中添加 (install/upgrade handler 不同是正常的) |
| 代码维护 | 两套类型定义需保持同步 | **一套类型**，无需同步 |

> **注意**：同一组件在安装和升级 Catalog 中的 `InlineHandler` 可能不同 (如 `kubernetes-master`: 安装=`EnsureMasterInit`，升级=`EnsureMasterUpgrade`)。复用 `ComponentSpec` 类型不影响这一点 — 两个 Catalog 是独立的 `[]ComponentSpec` 切片，只是元素类型相同。

### 5.2 安装组件与升级组件目录对比

| 组件名称 | 安装 handler | 升级 handler | 执行模式 |
|---------|-------------|-------------|---------|
| bkeagent | `EnsureBKEAgent` | `EnsureAgentUpgrade` | inline |
| nodes-env | `EnsureNodesEnv` | - | inline |
| cluster-api-obj | `EnsureClusterAPIObj` | - | inline |
| certs | `EnsureCerts` | - | inline |
| load-balance | `EnsureLoadBalance` | - | inline |
| kubernetes-master | `EnsureMasterInit` | `EnsureMasterUpgrade` | inline |
| kubernetes-worker | `EnsureWorkerJoin` | `EnsureWorkerUpgrade` | inline |
| nodes-postprocess | `EnsureNodesPostProcess` | - | inline |
| agent-switch | `EnsureAgentSwitch` | - | inline |
| kube-proxy | (manifest) | (manifest) | manifest |
| coredns | (manifest) | (manifest) | manifest |
| pre-upgrade-resources | - | `EnsurePreUpgradeResources` | inline |
| containerd | - | `EnsureContainerdUpgrade` | inline |
| etcd | (包含在 MasterInit) | `EnsureEtcdUpgrade` | inline |

#### 5.2.1 containerd 和 etcd 的安装路径分析

上表中 containerd 和 etcd 的安装列为"-"或"包含在 MasterInit"，因为安装时它们**没有独立的 Phase**，而是嵌入在其他 Phase 的执行流程中。以下是基于代码的分析：

**containerd — 在 `EnsureNodesEnv` Phase 中安装 (非独立 Phase)**

`EnsureNodesEnv` (DeployPhases #1) 负责节点环境初始化，containerd 的安装嵌入在其命令链中：

```txt
EnsureNodesEnv.Execute()
  → CheckOrInitNodesEnv()
    → buildEnvCommand() → BuildCommonEnvCommand()
      → 创建 BKEAgent Command CR (类型: CommandBuiltIn)
        → BKEAgent 执行 K8sEnvInit 插件 (scope 含 "runtime"):
          → initRuntime() → downloadContainerd()
            → 下载 containerd-{version}-linux-{arch}.tar.gz
            → 解压 + 安装 + 启动 systemd 服务
```

| 维度 | 说明 |
|------|------|
| 安装 Phase | `EnsureNodesEnv` (DeployPhases #1) |
| 安装机制 | BKEAgent `K8sEnvInit` 插件 `runtime` scope → `downloadContainerd()` |
| 代码位置 | `pkg/phaseframe/phases/ensure_nodes_env.go` → `pkg/command/env.go` → `pkg/job/builtin/kubeadm/env/init.go` |
| 是否独立 Phase | **否** — containerd 安装嵌入在 `EnsureNodesEnv` 的环境初始化命令中 |
| 升级 Phase | `EnsureContainerdUpgrade` (独立 Phase，仅升级路径) |

**etcd — 在 `EnsureMasterInit` Phase 中通过 kubeadm init 创建 (非独立 Phase)**

`EnsureMasterInit` (DeployPhases #5) 负责首个 Master 节点初始化，etcd 的 Static Pod 由 kubeadm init 自动创建：

```txt
EnsureMasterInit.Execute()
  → 创建 Bootstrap Command CR (Phase: InitControlPlane)
    → BKEAgent 执行 Kubeadm 插件 (phase=InitControlPlane):
      → kubeadm init
        → 生成 etcd Static Pod manifest → /etc/kubernetes/manifests/etcd.yaml
        → Kubelet 检测 manifest → 拉起 etcd Pod (stacked etcd)
        → 同时创建 apiserver/cm/scheduler Static Pod
  → 轮询等待 ControlPlaneInitializedCondition=True (含 etcd 就绪)
```

| 维度 | 说明 |
|------|------|
| 安装 Phase | `EnsureMasterInit` (DeployPhases #5) |
| 安装机制 | `kubeadm init` 自动创建 etcd Static Pod manifest (stacked etcd) |
| 代码位置 | `pkg/phaseframe/phases/ensure_master_init.go` → `pkg/command/bootstrap.go` → BKEAgent `Kubeadm` 插件 |
| 是否独立 Phase | **否** — etcd 由 `kubeadm init` 隐式创建，无独立 `EnsureEtcdInstall` Phase |
| 升级 Phase | `EnsureEtcdUpgrade` (独立 Phase，仅升级路径) |

**对 DeclarativeInstallCatalog 的影响**：

由于 containerd 和 etcd 在安装时没有独立 Phase (嵌入在 `EnsureNodesEnv` 和 `EnsureMasterInit` 中)，它们在 `DeclarativeInstallCatalog` 中**没有独立的 Catalog 条目**：

| 组件 | Catalog 中是否有独立安装条目 | 原因 |
|------|---------------------------|------|
| containerd | **否** | 安装嵌入在 `EnsureNodesEnv` 的 `runtime` scope 中，无独立 handler |
| etcd | **否** | 安装由 `kubeadm init` 隐式完成 (在 `EnsureMasterInit` 中)，无独立 handler |

> 这与升级目录 `DeclarativeUpgradeCatalog` 不同 — 升级时 containerd 和 etcd 有独立 Phase (`EnsureContainerdUpgrade` / `EnsureEtcdUpgrade`)，因此升级目录中有独立条目。安装时它们是嵌入式的，不需要独立条目。

### 5.3 ComponentFactory 注册扩展

```go
// pkg/componentfactory/registry.go

func registerInstallHandlers() {
    // 安装 handler 注册（复用现有 Phase 构造函数）
    RegisterInlineHandler("EnsureBKEAgent",       v1alpha1.InlineHandlerVersion, phases.NewEnsureBKEAgent)
    RegisterInlineHandler("EnsureNodesEnv",       v1alpha1.InlineHandlerVersion, phases.NewEnsureNodesEnv)
    RegisterInlineHandler("EnsureClusterAPIObj",  v1alpha1.InlineHandlerVersion, phases.NewEnsureClusterAPIObj)
    RegisterInlineHandler("EnsureCerts",          v1alpha1.InlineHandlerVersion, phases.NewEnsureCerts)
    RegisterInlineHandler("EnsureLoadBalance",    v1alpha1.InlineHandlerVersion, phases.NewEnsureLoadBalance)
    RegisterInlineHandler("EnsureMasterInit",     v1alpha1.InlineHandlerVersion, phases.NewEnsureMasterInit)
    RegisterInlineHandler("EnsureWorkerJoin",     v1alpha1.InlineHandlerVersion, phases.NewEnsureWorkerJoin)
    RegisterInlineHandler("EnsureNodesPostProcess", v1alpha1.InlineHandlerVersion, phases.NewEnsureNodesPostProcess)
    RegisterInlineHandler("EnsureAgentSwitch",    v1alpha1.InlineHandlerVersion, phases.NewEnsureAgentSwitch)
    
    // 升级 handler 已注册（现有）
    // RegisterInlineHandler("EnsureEtcdUpgrade", ...)
    // RegisterInlineHandler("EnsureMasterUpgrade", ...)
    // ...
}
```

## 6. 安装 DAG 构建设计

### 6.0 设计思路

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
│    BuildUpgradeDAGFromBundle(bundle, resolver)                                  │
│      → 遍历 upgrade.components 构建 DAG                                          │
│                                                                                 │
│  安装 DAG构建 (新增):                                                           │
│    BuildVersionContextForInstall(targetBundle)                                   │
│      → Current 全空 (全新安装，无已安装组件)                                    │
│      → Target 有值 (来自 install.components + upgrade.components)              │
│      → Decide: current="" + target!="" → DecisionInstall ★                      │
│    BuildInstallDAGFromBundle(bundle, resolver)                                   │
│      → 遍历 install.components 构建 DAG (复用 BuildUpgradeDAG 逻辑)             │
│      → InstallComponent 转 UpgradeComponent 结构 (适配现有拓扑构建器)           │
│                                                                                 │
│  复用点:                                                                         │
│  ① BuildUpgradeDAG — 拓扑排序 + 依赖解析逻辑完全复用                            │
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
│  ⑤ install-ready annotation — 安装前置门控                                      │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 6.1 VersionContext 扩展

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

### 6.2 安装 DAG 构建器

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
    excludeComponents ...string,  // ★ 新增: 条件排除组件列表
) (*topology.UpgradeDAG, error) {
    // 1. 提取安装组件
    installComponents, err := InstallComponentsFromBundle(bundle)
    if err != nil {
        return nil, err
    }
    
    // 2. ★ 条件过滤: 排除指定组件 (如 manage 在全新安装时排除)
    excludeSet := make(map[string]bool)
    for _, name := range excludeComponents {
        excludeSet[name] = true
    }
    var filteredComponents []apiv1.ReleaseImageComponent
    for _, ic := range installComponents {
        if !excludeSet[ic.Name] {
            filteredComponents = append(filteredComponents, ic)
        }
    }
    
    // 3. 转换为 ComponentNode（复用 UpgradeComponent 结构）
    var components []topology.ReleaseImageComponent
    for _, ic := range filteredComponents {
        comp := topology.ReleaseImageComponent{
            Name:    ic.Name,
            Version: ic.Version,
        }
        if ic.Inline != nil {
            comp.Inline = &topology.ReleaseImageInline{
                Handler: ic.Inline.Handler,
                Version: ic.Inline.Version,
            }
        }
        components = append(components, comp)
    }
    
    // 4. 复用 BuildUpgradeDAG（同一构建逻辑）
    return topology.BuildUpgradeDAG(components, resolve)
}
```

### 6.3 安装 VersionContext 构建

```go
// pkg/upgrade/build_release.go

// BuildVersionContextForInstall 为安装场景构建 VersionContext 🆕新增
func BuildVersionContextForInstall(
    targetBundle *releasemanifest.Bundle,
) *VersionContext {
    vc := NewVersionContext()
    
    // 安装场景：Current 全部为空（全新安装），Target 来自 ReleaseImage
    // install.components 填充 Target
    if targetBundle.Release.Spec.Install != nil {
        for _, comp := range targetBundle.Release.Spec.Install.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }
    
    // upgrade.components 也填充 Target（覆盖同名 install 条目）
    if targetBundle.Release.Spec.Upgrade != nil {
        for _, comp := range targetBundle.Release.Spec.Upgrade.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }
    
    // Current 全部为空 → Decide 返回 DecisionInstall
    return vc
}
```

## 7. 安装 DAG 执行设计

### 7.0 设计思路

安装 DAG 执行的核心思路是**在现有 PhaseFlow 的执行入口中增加 DAG 安装分支**，通过三重门控 (Feature Gate + 全新安装判定 + install-ready annotation) 决定是否走 DAG 路径。未满足门控条件时回退到 Legacy PhaseFlow，保证向后兼容。

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
│     门控: Feature Gate + 全新安装 + install-ready annotation                    │
│                                                                                 │
│  ③ 默认 → PhaseFlow (Legacy 路径)                                               │
│     适用: Feature Gate 未启用 / ReleaseImage 未就绪 / 扩容/纳管/删除等场景      │
│                                                                                 │
│  三重门控设计原因:                                                               │
│                                                                                 │
│  门控 1 — Feature Gate (DeclarativeInstallEnabled):                              │
│    原因: 渐进迁移，默认关闭确保生产稳定                                          │
│    效果: 关闭时所有安装走 Legacy PhaseFlow                                       │
│                                                                                 │
│  门控 2 — 全新安装判定 (Status.Phase 为空或 Init):                               │
│    原因: DAG 安装路径仅面向全新安装，扩容/纳管/删除等场景不适用                  │
│    效果: 仅全新安装可走 DAG，扩容仍走 PhaseFlow Scale Phase                      │
│                                                                                 │
│  门控 3 — install-ready annotation:                                              │
│    原因: ReleaseImage 可能尚未创建或未通过验证，需前置门控确保就绪              │
│    效果: ReleaseImage Phase=Valid 后 ClusterVersionReconciler 设置 annotation   │
│          → BKEClusterReconciler 检测到 annotation 后才走 DAG 路径              │
│          → 无 annotation 时回退 Legacy PhaseFlow (从 Spec 读取版本，不依赖 RI) │
│                                                                                 │
│  executeInstallDAG 执行流程:                                                     │
│    1. resolveInstallBundle → 从 ReleaseImage 解析 OCI Bundle                    │
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
│  ④ Annotation: 安装 install-ready vs 升级 upgrade-ready                         │
│  ⑤ Handler: 安装 EnsureMasterInit vs 升级 EnsureMasterUpgrade                  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.1 执行入口

#### 7.1.0 三路分发统一设计

`executePhaseFlow()` 是 BKEClusterReconciler 的核心分发入口，统一管理 DAG 升级、DAG 安装、Legacy PhaseFlow 三条路径。设计原则是**优先匹配 DAG 路径，未命中则回退 Legacy**，确保 Feature Gate 关闭时行为完全不变。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              executePhaseFlow 三路分发统一设计                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  executePhaseFlow(ctx, phaseCtx, oldCluster, newCluster)                        │
│     │                                                                           │
│     │  1. 预处理: 清理过期状态                                                   │
│     │     cleanupStaleDeclarativeUpgradeStatus(newCluster)                       │
│     │     (如果上次 DAG 执行中断且集群已重置，清理残留的 DeclarativeUpgradeStatus) │
│     │                                                                           │
│     │  2. 判断集群操作类型                                                       │
│     │     ├─ Delete? → DeletePhases 路径                                       │
│     │     ├─ Pause? → EnsurePaused Phase                                      │
│     │     ├─ DryRun? → EnsureDryRun Phase                                     │
│     │     ├─ Manage? → EnsureClusterManage Phase                               │
│     │     ├─ Reset? → ResetPhases 路径                                         │
│     │     └─ Install/Upgrade? → 继续判断 DAG vs Legacy ↓                       │
│     │                                                                           │
│     │  3. DAG 升级路径 (优先匹配)                                               │
│     │     shouldUseDeclarativeUpgrade(newCluster)?                              │
│     │     ├─ true → executeUpgradeDAG(...) → return                            │
│     │     └─ false → 继续                                                      │
│     │                                                                           │
│     │  4. DAG 安装路径 (次优先匹配)                                              │
│     │     shouldUseDeclarativeInstall(newCluster)?                               │
│     │     ├─ true → executeInstallDAG(...) → return                            │
│     │     └─ false → 继续                                                      │
│     │                                                                           │
│     │  5. Legacy PhaseFlow 路径 (兜底)                                          │
│     │     ├─ 集群操作类 (Delete/Pause/DryRun/Manage/Reset):                     │
│     │     │   走 CommonPhases + 对应专用 Phase                                   │
│     │     ├─ 全新安装 (无 install-ready):                                        │
│     │     │   走 CommonPhases + DeployPhases (从 BKECluster.Spec 读取版本)      │
│     │     ├─ 集群扩容 (新增 Master/Worker):                                    │
│     │     │   走 CommonPhases + ScalePhases (部分 Phase，非完整安装)             │
│     │     └─ 升级 (无 upgrade-ready):                                           │
│     │         走 CommonPhases + UpgradePhases (从 BKECluster.Spec 读取版本)     │
│     │                                                                           │
│     ▼                                                                           │
│  执行完成                                                                       │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 7.1.1 完整代码实现

```go
// controllers/capbke/bkecluster_controller.go

func (r *BKEClusterReconciler) executePhaseFlow(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // ─── 预处理: 清理过期状态 ───
    // 如果集群被重置或重新创建，清理上次 DAG 执行的残留状态
    if shouldCleanupDeclarativeStatus(newCluster) {
        cleanupStaleDeclarativeUpgradeStatus(newCluster)
    }

    // ─── DAG 升级路径 (优先匹配) ───
    if r.shouldUseDeclarativeUpgrade(newCluster) {
        return r.executeUpgradeDAG(ctx, phaseCtx, oldCluster, newCluster)
    }

    // ─── DAG 安装路径 (次优先匹配) ───
    if r.shouldUseDeclarativeInstall(newCluster) {
        return r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }

    // ─── Legacy PhaseFlow 路径 (兜底) ───
    // 以下场景走 Legacy PhaseFlow:
    //   1. 集群操作类 (Delete/Pause/DryRun/Manage/Reset) — 非 Install/Upgrade 操作
    //   2. 全新安装但 Feature Gate 未启用或 ReleaseImage 未就绪
    //   3. 集群扩容 (新增 Master/Worker 节点)
    //   4. 升级但 Feature Gate 未启用或 ReleaseImage 未就绪
    flow := phases.NewPhaseFlow(phaseCtx)
    return flow.Execute()
}

// shouldCleanupDeclarativeStatus 判断是否需要清理残留的 DAG 状态
// 场景: 集群重置后重新安装、DAG 执行中断后用户手动重置
func (r *BKEClusterReconciler) shouldCleanupDeclarativeStatus(bkeCluster *bkev1beta1.BKECluster) bool {
    // 集群被重置
    if bkeCluster.Spec.Reset {
        return true
    }
    // DeclarativeUpgradeStatus 有记录但集群状态为 Init (可能被重置)
    if bkeCluster.Status.DeclarativeUpgrade != nil &&
        bkeCluster.Status.DeclarativeUpgrade.TargetVersion != "" &&
        bkeCluster.Status.ClusterStatus == bkev1beta1.ClusterInitializing {
        return true
    }
    return false
}

// cleanupStaleDeclarativeUpgradeStatus 清理残留的 DAG 执行状态
func (r *BKEClusterReconciler) cleanupStaleDeclarativeUpgradeStatus(bkeCluster *bkev1beta1.BKECluster) {
    bkeCluster.Status.DeclarativeUpgrade = nil
    bkeCluster.Status.ClusterComponentStatuses = nil
    // 清理可能残留的注解
    delete(bkeCluster.Annotations, annotation.UpgradeReadyAnnotationKey)
    delete(bkeCluster.Annotations, annotation.InstallReadyAnnotationKey)
    delete(bkeCluster.Annotations, annotation.SkipKubeletUpgradeAnnotationKey)
    delete(bkeCluster.Annotations, annotation.KubeletCatchupTargetAnnotationKey)
}
```

#### 7.1.2 Legacy PhaseFlow 路径设计

Legacy PhaseFlow 是 DAG 路径的兜底方案，覆盖所有 DAG 路径不适用的场景。PhaseFlow 通过 `CalculatePhase()` 动态计算需要执行的 Phase 列表：

```go
// pkg/phaseframe/phases/phase_flow.go — CalculatePhase 扩展

func (f *PhaseFlow) CalculatePhase(old, new *bkev1beta1.BKECluster) []Phase {
    var phases []Phase

    // ─── Phase 1: CommonPhases (所有操作通用) ───
    // 这些 Phase 在所有场景下都执行，处理集群级操作
    for _, phaseFunc := range CommonPhases {
        phase := phaseFunc(f.ctx)
        if phase.NeedExecute(old, new) {
            phases = append(phases, phase)
        }
    }

    // ─── Phase 2: 按操作类型选择专用 Phase ───
    switch {
    // 集群删除
    case new.DeletionTimestamp != nil || new.Spec.Reset:
        phases = append(phases, f.buildDeletePhases(old, new)...)

    // 集群暂停
    case new.Spec.Pause:
        phases = append(phases, f.buildPausePhases(old, new)...)

    // DryRun 模式
    case new.Spec.DryRun:
        phases = append(phases, f.buildDryRunPhases(old, new)...)

    // 纳管已有集群
    case new.Spec.Manage:
        phases = append(phases, f.buildManagePhases(old, new)...)

    // 全新安装
    case f.isNewInstall(old, new):
        // ★ 如果 DAG 路径未启用或 ReleaseImage 未就绪，走 Legacy 安装
        // 版本来源: BKECluster.Spec.ClusterConfig.Cluster (用户直接设置或 ClusterVersion 同步)
        phases = append(phases, f.buildDeployPhases(old, new)...)

    // 集群扩容 (新增 Master/Worker 节点)
    case f.isScale(old, new):
        // ★ 扩容不走 DAG 安装路径，仅执行 Scale Phase
        // Scale Phase 复用 DeployPhases 中的部分 Phase (Join/PostProcess)
        phases = append(phases, f.buildScalePhases(old, new)...)

    // 集群升级
    case f.isUpgrade(old, new):
        // ★ 如果 DAG 升级路径未启用或 ReleaseImage 未就绪，走 Legacy 升级
        // 版本来源: BKECluster.Spec.ClusterConfig.Cluster (用户直接设置或 ClusterVersion 同步)
        phases = append(phases, f.buildUpgradePhases(old, new)...)
    }

    return phases
}
```

#### 7.1.3 Legacy 路径的版本来源

Legacy PhaseFlow 的版本来源与 DAG 路径不同：

| 维度 | DAG 路径 (安装/升级) | Legacy PhaseFlow 路径 |
|------|---------------------|----------------------|
| **K8s 版本来源** | `ReleaseImage bundle.Components[kubernetes-master].Version` → Command CR 参数 | `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` (用户设置或 ClusterVersion 同步) |
| **etcd 版本来源** | `ReleaseImage bundle.Components[etcd].Version` → Command CR 参数 | `BKECluster.Spec.ClusterConfig.Cluster.EtcdVersion` (用户设置或 ClusterVersion 同步) |
| **是否依赖 ReleaseImage** | 是 (必须 Phase=Valid) | 否 (从 Spec 直接读取) |
| **BKEAgent 版本来源** | Command CR `kubernetesVersion`/`etcdVersion` 参数 (§7.2.4) | `BkeConfig.Cluster.KubernetesVersion` (getBKEConfig 从 Spec 读取) |
| **适用场景** | Feature Gate 启用 + ReleaseImage Valid | Feature Gate 关闭 / ReleaseImage 未就绪 / 扩容 / 纳管等 |

> **关键**：Legacy 路径**不依赖 ReleaseImage**，从 `BKECluster.Spec` 直接读取版本。这确保即使没有 ReleaseImage CR 也能完成安装/升级 (向后兼容)。

#### 7.1.4 Legacy 路径与 DAG 路径的共存设计

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
│    install-ready/upgrade-ready annotation 未设置                                 │
│    → shouldUseDeclarativeInstall/Upgrade 返回 false                              │
│    → 回退 Legacy PhaseFlow                                                       │
│    → 版本从 BKECluster.Spec 读取 (不依赖 RI)                                     │
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

#### 7.1.5 PhaseFlow 路径（Legacy）的适用场景

以下场景仍会走 Legacy PhaseFlow 路径，不走 DAG 路径：

| 场景 | 原因 | 说明 | 走哪个 Phase 列表 |
|------|------|------|------------------|
| **Feature Gate 未启用** | `DeclarativeInstallEnabled = false` / `DeclarativeUpgradeEnabled = false` | 迁移期间默认关闭，确保生产稳定；正式启用后此场景消失 | CommonPhases + DeployPhases / UpgradePhases |
| **ReleaseImage 未就绪** | `install-ready` / `upgrade-ready` annotation 未设置 | RI 未创建或 Status.Phase != Valid | CommonPhases + DeployPhases / UpgradePhases |
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
func (r *BKEClusterReconciler) shouldUseDeclarativeInstall(bkeCluster *bkev1beta1.BKECluster) bool {
    // Feature Gate 控制
    if !featuregate.DeclarativeInstallEnabled.Enabled() {
        return false
    }
    // 仅在全新安装时启用（Status.Phase 为空或 Init）
    if bkeCluster.Status.Phase != "" && bkeCluster.Status.Phase != bkev1beta1.PhaseInit {
        return false
    }
    // 检查是否有安装 annotation（由 ClusterVersionReconciler 设置）
    _, ok := annotation.HasAnnotation(bkeCluster, annotation.InstallReadyAnnotationKey)
    return ok
}
```

#### 7.1.1 install-ready annotation 的作用

`shouldUseDeclarativeInstall()` 的最后一个检查项是 `install-ready` annotation。该 annotation 由 `ClusterVersionReconciler` 在安装前置条件满足后设置，是 BKEClusterReconciler 决定是否走 DAG 安装路径的**最终门控**。

**为什么需要这个 annotation**：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              install-ready annotation 的作用                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  没有 install-ready annotation 时的问题:                                        │
│                                                                                 │
│  用户创建 BKECluster CR (Spec.OpenFuyaoVersion = "v2.7.0")                     │
│    │                                                                            │
│    ▼ BKEClusterReconciler 被触发                                                 │
│  shouldUseDeclarativeInstall 检查:                                              │
│    ✓ Feature Gate 启用                                                          │
│    ✓ Status.Phase 为空 (全新安装)                                                │
│    ✗ 没有 install-ready annotation → 返回 false                                 │
│    → 走 Legacy PhaseFlow? 但此时 ReleaseImage 可能还没就绪!                    │
│                                                                                 │
│  问题:                                                                          │
│  BKECluster 创建后，ReleaseImage CR 可能尚未创建或尚未通过验证。                │
│  如果 BKEClusterReconciler 立即开始安装，会因 ReleaseImage 不存在而失败。       │
│  需要一个前置门控：确保 ReleaseImage 已就绪后，才允许开始安装。                 │
│                                                                                 │
│  有 install-ready annotation 时:                                               │
│                                                                                 │
│  用户创建 BKECluster CR                                                         │
│    │                                                                            │
│    ▼ BKEClusterReconciler: ensureClusterVersionOnInstall()                     │
│    │  创建 ClusterVersion CR (desiredVersion = Spec.OpenFuyaoVersion)            │
│    │                                                                            │
│    ▼ ClusterVersionReconciler 被触发                                           │
│    │  1. 解析 desiredVersion → 查找 ReleaseImage CR                             │
│    │  2. ReleaseImageEnsurer.Ensure() → 拉取 OCI Bundle                          │
│    │  3. 等待 ReleaseImage Status.Phase = Valid (签名验证+组件解析+兼容性校验) │
│    │  4. ReleaseImage Valid → 设置 install-ready annotation                      │
│    │     bc.Annotations[InstallReadyAnnotationKey] = desiredVersion             │
│    │                                                                            │
│    ▼ BKEClusterReconciler 再次被触发 (annotation 变更)                         │
│    shouldUseDeclarativeInstall 检查:                                            │
│    ✓ Feature Gate 启用                                                          │
│    ✓ Status.Phase 为空 (全新安装)                                                │
│    ✓ install-ready annotation 存在 → 返回 true                                  │
│    → 走 DAG 安装路径                                                            │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**install-ready annotation 的完整设置流程**：

| 步骤 | 执行者 | 操作 | 说明 |
|------|--------|------|------|
| 1 | BKEClusterReconciler | `ensureClusterVersionOnInstall()` 创建 ClusterVersion CR | 用户创建 BKECluster 后，自动创建对应的 ClusterVersion |
| 2 | ClusterVersionReconciler | 解析 `desiredVersion`，查找 ReleaseImage CR | 通过 `Spec.Version == desiredVersion` 匹配 |
| 3 | ClusterVersionReconciler | `ReleaseImageEnsurer.Ensure()` 拉取 OCI Bundle | 从 OCI Registry 拉取 ReleaseImage payload |
| 4 | ReleaseImageReconciler | 验证签名 + 解析组件 + 兼容性校验 | 设置 `Status.Phase = Valid` (或 Invalid/ManifestMissing/CompatibilityFailed) |
| 5 | ClusterVersionReconciler | ReleaseImage Phase == Valid → 设置 `install-ready` annotation | `bc.Annotations[InstallReadyAnnotationKey] = desiredVersion` |
| 6 | BKEClusterReconciler | 检测到 annotation 变更 → `shouldUseDeclarativeInstall()` 返回 true | 进入 DAG 安装路径 |

**与 upgrade-ready annotation 的对称性**：

| 维度 | install-ready (安装) | upgrade-ready (升级) |
|------|---------------------|---------------------|
| **设置者** | ClusterVersionReconciler | ClusterVersionReconciler |
| **设置条件** | ReleaseImage Phase=Valid + 全新安装 (无 history) | ReleaseImage Phase=Valid + UpgradePath 校验通过 |
| **annotation 值** | desiredVersion (openFuyao 版本) | hopTarget (openFuyao 版本，可能是中间 hop) |
| **消费者** | `shouldUseDeclarativeInstall()` | `shouldUseDeclarativeUpgrade()` |
| **触发路径** | BKECluster 创建 → CV 创建 → RI 验证 → 设置 annotation → DAG 安装 | CV desiredVersion 变更 → RI 验证 → UP 校验 → 设置 annotation → DAG 升级 |
| **清除时机** | 安装完成后 (DeclarativeUpgradeStatus 完成) | 升级 hop 完成后 (CompleteUpgradeHop) |

**install-ready 不存在时走 Legacy 路径的原因**：

| 场景 | 原因 | Legacy 行为 |
|------|------|------------|
| Feature Gate 未启用 | `DeclarativeInstallEnabled=false` | PhaseFlow 正常执行 (DeployPhases) |
| ReleaseImage 未创建 | 用户创建 BKECluster 但未创建 ReleaseImage CR | PhaseFlow 从 `BKECluster.Spec` 读取版本 (Legacy 模式不依赖 ReleaseImage) |
| ReleaseImage 未通过验证 | ReleaseImage Status.Phase != Valid | PhaseFlow 从 `BKECluster.Spec` 读取版本，不阻塞安装 |
| 旧格式 ReleaseImage | `install.components[].inline` 为空 | PhaseFlow 正常执行 (不消费 ReleaseImage install 列表) |

> **关键设计**：install-ready annotation 是 DAG 安装路径的**前置门控**，确保 ReleaseImage 已验证通过后才走 DAG 路径。没有该 annotation 时回退到 Legacy PhaseFlow (从 `BKECluster.Spec` 直接读取版本，不依赖 ReleaseImage)，保证向后兼容。

### 7.2 executeInstallDAG 实现

```go
// controllers/capbke/bkecluster_install_dag.go 🆕新增

func (r *BKEClusterReconciler) executeInstallDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1. 解析目标 ReleaseImage
    releaseImage, bundle, err := r.resolveInstallBundle(ctx, newCluster)
    
    // 2. 构建安装 VersionContext（Current 为空，Target 来自 bundle）
    vc := upgrade.BuildVersionContextForInstall(bundle)
    phaseCtx.SetVersionContext(vc)
    
    // 3. 同步目标版本到 BKECluster.Spec
    //    ★ 仅用于 Legacy 代码路径兼容 (如 upgradeMasterNodesWithParams / waitForNodeHealthCheck 直接读 Spec)
    //    ★ 不作为 BKEAgent 渲染 manifest 的版本来源 — BKEAgent 应从 ReleaseImage 获取版本
    upgrade.ApplyVersionContextTargetsToClusterSpec(vc, newCluster)
    
    // 4. 构建安装 DAG
    //    ★ 排除 manage 组件: 全新安装不需要纳管探测，manage 仅在纳管场景 (Spec.Manage=true) 执行
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle), "manage")
    
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

#### 7.2.1 Spec 同步仅为 Legacy 兼容

`ApplyVersionContextTargetsToClusterSpec()` 将 VC Target (来源于 ReleaseImage) 同步到 `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` / `EtcdVersion` / `ContainerdVersion`。此同步**仅用于 Legacy 代码路径兼容**，不作为 BKEAgent 渲染 manifest 的版本来源。

| 维度 | Legacy 路径 (现有) | 声明式 DAG 路径 (目标) |
|------|-------------------|----------------------|
| **BKEAgent 版本来源** | `BkeConfig.Cluster.KubernetesVersion` (从 `BKECluster.Spec` 读取) | **`ReleaseImage` bundle 中的组件版本** ★ |
| **manifest image tag** | `BkeConfig.Cluster.KubernetesVersion` 去 v 前缀 | **`bundle.Components[kubernetes-master].Spec.Version` 去 v 前缀** ★ |
| **etcd image tag** | `BkeConfig.Cluster.EtcdVersion` 或 `Extra["etcdVersion"]` | **`bundle.Components[etcd].Spec.Version` 去 v 前缀** ★ |
| **kubelet 二进制版本** | `BkeConfig.Cluster.KubernetesVersion` | **`bundle.Components[kubernetes-worker].Spec.Version`** ★ |
| **Spec 同步的作用** | 唯一来源 (BKEAgent 直接读取) | **仅 Legacy 兼容** (供未改造的 Phase 代码直接读取 Spec) |

#### 7.2.2 BKEAgent 从 ReleaseImage 获取版本的设计

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

#### 7.2.3 版本来源对比汇总

| 组件 | 当前来源 (Legacy) | 修正后来源 (声明式 DAG) | 传递方式 |
|------|------------------|----------------------|---------|
| kube-apiserver manifest tag | `BkeConfig.Cluster.KubernetesVersion` (Spec 同步) | `bundle.Components[kubernetes-master].Version` | Command CR `kubernetesVersion` 参数 |
| kube-controller-manager manifest tag | 同上 | 同上 | 同上 |
| kube-scheduler manifest tag | 同上 | 同上 | 同上 |
| etcd manifest tag | `BkeConfig.Cluster.EtcdVersion` 或 `Extra["etcdVersion"]` | `bundle.Components[etcd].Version` | Command CR `etcdVersion` 参数 → `Extra["etcdVersion"]` |
| kubelet 二进制 | `BkeConfig.Cluster.KubernetesVersion` | `bundle.Components[kubernetes-worker].Version` | Command CR `kubernetesVersion` 参数 |
| kubectl 二进制 | `BkeConfig.Cluster.KubernetesVersion` | `bundle.Components[kubernetes-master].Version` | Command CR `kubernetesVersion` 参数 |

> **Spec 同步保留但仅用于兼容**：`ApplyVersionContextTargetsToClusterSpec()` 仍执行，确保 Legacy 代码路径 (如 `upgradeMasterNodesWithParams` / `waitForNodeHealthCheck` 直接读 Spec) 能获取正确版本。但 BKEAgent 的版本来源修正为 Command CR 参数 (从 ReleaseImage)，不再依赖 Spec 同步时序。

#### 7.2.4 kubernetesVersion 命令参数设计 (对标 etcdVersion)

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

### 7.3 安装 DAG 结构

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

### 8.0 设计思路

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

### 8.1 DeployPhases → 安装组件映射

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

### 8.2 安装 vs 升级组件差异

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
| **Phase 4** | 移除 PhaseFlow | 移除 DeployPhases 硬编码列表，安装完全 DAG 驱动 | 移除 Feature Gate |

### 9.3 向后兼容

1. **旧格式兼容**：`ReleaseImageComponent` 的 `inline` 字段是 `omitempty`，旧格式 `{name, version}` 仍然有效
2. **PhaseFlow 共存**：`DeclarativeInstallEnabled` 未启用时，继续使用 PhaseFlow
3. **混合模式**：ReleaseImage 可同时包含有 `inline` 和无 `inline` 的安装组件
4. **ClusterVersionReconciler**：安装时设置 `cvo.openfuyao.cn/install-ready` annotation 触发 DAG 路径

### 9.4 Legacy PhaseFlow 完全移除方案

当迁移到 Phase 4 时，需要完全移除 Legacy PhaseFlow 路径。以下针对 7.1 节中列出的每个 Legacy 场景，给出 DAG 化的完整方案。

#### 9.4.1 场景覆盖总览

| Legacy 场景 | DAG 化方案 | 移除条件 |
|------------|-----------|---------|
| Feature Gate 未启用 | 移除 Feature Gate，DAG 成为唯一路径 | Phase 4 |
| ReleaseImage 无 inline handler | 强制 ReleaseImage 包含 inline 字段 | Phase 4 |
| 无 install-ready annotation | ClusterVersionReconciler 始终设置 annotation | Phase 2 |
| 纳管已有集群 | 新增 `manage` 组件到安装 DAG | Phase 4 |
| 集群扩容（新增节点） | 新增 `scale-master` / `scale-worker` 组件到 DAG | Phase 4 |
| 集群删除/重置 | 新增 `delete` DAG（逆序卸载） | Phase 4 |
| DryRun 模式 | DAG 执行器支持 DryRun 标记 | Phase 3 |
| 集群暂停 | DAG 前置检查 `BKECluster.Spec.Pause` | Phase 3 |

#### 9.4.2 纳管已有集群 DAG 化

##### 设计思路

纳管是指将一个已有的 Kubernetes 集群纳入 BKE 管理。与全新安装的核心区别在于：集群已运行，组件已有版本，不能假设 Current 为空。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              纳管 DAG 化设计思路                                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    EnsureClusterManage Phase (独立 Phase)                                       │
│      → 探测已有集群组件版本 (kubectl get / NodeInfo / Pod image)                │
│      → 写入 BKECluster.Status (KubernetesVersion/EtcdVersion/...)              │
│      → 后续 Phase 从 Status 读取版本                                             │
│    问题: 纳管 + 安装耦合在一个 PhaseFlow 中，无法利用 DAG 的版本决策和断点续传  │
│                                                                                 │
│  DAG 化方案:                                                                     │
│    将纳管拆解为 DAG 的第一个组件:                                                │
│      manage 组件 (inline: EnsureClusterManage)                                   │
│        → 探测版本 → 填充 VersionContext.Current                                 │
│        → 后续组件通过 VersionContext.Decide() 判断:                             │
│          Current == Target → DecisionSkip (已有正确版本，跳过)                   │
│          Current != Target → DecisionUpgrade (需要升级到目标版本)               │
│          Current == "" → DecisionInstall (缺失组件，需要安装)                    │
│                                                                                 │
│  核心优势:                                                                       │
│  1. 版本感知 — 纳管后仅安装/升级缺失或版本不符的组件，不重复安装已有组件        │
│  2. 断点续传 — DeclarativeUpgradeStatus 追踪已完成组件，中断后恢复              │
│  3. 并行执行 — 纳管后的安装/升级走 DAG 并行批次，而非 PhaseFlow 串行            │
│  4. 统一路径 — 纳管 + 安装 + 升级统一走 DAG，消除 PhaseFlow 特殊路径            │
│                                                                                 │
│  manage 组件的条件包含:                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 问题: manage 组件放在 install.components 中，对全新安装有没有影响？      │   │
│  │                                                                         │   │
│  │ 影响:                                                                   │   │
│  │ • 全新安装: manage 探测不存在的集群 → 探测失败/空版本 → 干扰后续 Decide │   │
│  │ • 纳管场景: manage 探测已有集群 → 填充 VC.Current → 后续组件正确 Decide │   │
│  │                                                                         │   │
│  │ 解决方案: 条件包含 (方式 B)                                              │   │
│  │ • manage 在 ReleaseImage install.components 中 (声明式定义)             │   │
│  │ • BuildInstallDAGFromBundle 按场景条件过滤:                             │   │
│  │   - 全新安装: 排除 manage 组件 (excludeComponents=["manage"])           │   │
│  │   - 纳管场景: 不排除 manage 组件 (保留在 DAG 中)                        │   │
│  │                                                                         │   │
│  │ 为什么用方式 B 而非方式 A (不放 install.components):                    │   │
│  │ • 方式 A: manage 不在声明式定义中，需硬编码注入，违反声明式原则         │   │
│  │ • 方式 B: manage 在声明式定义中，按场景条件过滤，保持声明式一致性       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

##### 代码实现

```yaml
# ReleaseImage install.components 新增 manage 组件
install:
  components:
    - name: manage
      version: v1.0.0
      inline:
        handler: EnsureClusterManage
        version: v1.0.0
```

```go
// DeclarativeInstallCatalog 新增
{ Name: "manage", Mode: ExecutionInline, InlineHandler: "EnsureClusterManage", LegacyPhase: "EnsureClusterManage" },
```

```go
// pkg/phaseframe/phases/ensure_cluster_manage.go — 改造为 inline handler

type EnsureClusterManage struct {
    phaseframe.BasePhase
}

func (e *EnsureClusterManage) Execute() (ctrl.Result, error) {
    bkeCluster := e.Ctx.BKECluster
    vc := e.GetVersionContext()

    // 1. 探测已有集群的组件版本
    detectedVersions, err := e.detectClusterVersions(bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 2. 填充 VersionContext.Current (供后续组件 Decide 判断)
    for name, ver := range detectedVersions {
        vc.SetCurrent(name, ver)
    }

    // 3. 写入 BKECluster.Status (供 Legacy 兼容路径读取)
    if v, ok := detectedVersions["kubernetes-master"]; ok {
        bkeCluster.Status.KubernetesVersion = v
    }
    if v, ok := detectedVersions["etcd"]; ok {
        bkeCluster.Status.EtcdVersion = v
    }
    if v, ok := detectedVersions["containerd"]; ok {
        bkeCluster.Status.ContainerdVersion = v
    }

    // 4. 标记纳管完成
    bkeCluster.Status.Managed = true
    e.Ctx.Log.Info("cluster manage completed", "detectedVersions", detectedVersions)

    return ctrl.Result{}, nil
}

// detectClusterVersions 探测已有集群的组件版本
func (e *EnsureClusterManage) detectClusterVersions(
    bkeCluster *bkev1beta1.BKECluster,
) (map[string]string, error) {
    result := make(map[string]string)

    // 获取目标集群客户端
    targetClient, err := e.Ctx.GetTargetK8sClient()
    if err != nil {
        return nil, err
    }

    // 探测 kube-apiserver 版本
    versionInfo, err := targetClient.Discovery().ServerVersion()
    if err == nil {
        result["kubernetes-master"] = versionInfo.GitVersion
        result["kubernetes-worker"] = versionInfo.GitVersion
    }

    // 探测 etcd 版本 (从 etcd Pod image tag)
    etcdVersion, err := detectEtcdVersion(targetClient)
    if err == nil {
        result["etcd"] = etcdVersion
    }

    // 探测 containerd 版本 (从 Node 节点 SSH 查询)
    containerdVersion, err := detectContainerdVersion(bkeCluster)
    if err == nil {
        result["containerd"] = containerdVersion
    }

    // 探测 addon 版本 (从 Deployment/DaemonSet image tag)
    corednsVersion, _ := detectAddonVersion(targetClient, "kube-system", "k8s-app=kube-dns")
    if corednsVersion != "" {
        result["coredns"] = corednsVersion
    }

    kubeProxyVersion, _ := detectAddonVersion(targetClient, "kube-system", "k8s-app=kube-proxy")
    if kubeProxyVersion != "" {
        result["kube-proxy"] = kubeProxyVersion
    }

    return result, nil
}
```

```go
// pkg/upgrade/build_release.go — 纳管专用 VersionContext 构建

// BuildVersionContextForManage 为纳管场景构建 VersionContext
func BuildVersionContextForManage(
    targetBundle *releasemanifest.Bundle,
    detectedVersions map[string]string,
) *VersionContext {
    vc := NewVersionContext()

    // Current 来自探测结果 (已有集群的实际版本)
    for name, ver := range detectedVersions {
        vc.SetCurrent(name, ver)
    }

    // Target 来自 ReleaseImage (期望达到的目标版本)
    if targetBundle.Release.Spec.Install != nil {
        for _, comp := range targetBundle.Release.Spec.Install.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }
    if targetBundle.Release.Spec.Upgrade != nil {
        for _, comp := range targetBundle.Release.Spec.Upgrade.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }

    return vc
}
```

##### 场景判断

纳管场景需要先判断 BKECluster 是否处于纳管模式，再走纳管 DAG 路径：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              纳管场景判断逻辑                                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  reconcileCluster 入口:                                                         │
│                                                                                 │
│    BKECluster.Spec.Manage == true?                                              │
│    ├── true → 纳管场景 ★                                                        │
│    │     → executeManageDAG()                                                    │
│    │     → manage 组件探测版本 → 填充 VC.Current                                 │
│    │     → 后续组件 Decide: Skip(版本一致) / Upgrade(版本不同) / Install(缺失)  │
│    │                                                                            │
│    └── false → 非纳管 → 继续判断其他场景 (安装/升级/扩容/删除/...)             │
│                                                                                 │
│  纳管与全新安装的区别:                                                           │
│    全新安装: 集群不存在，Current 全空 → 全部 DecisionInstall                   │
│    纳管:     集群已运行，Current 从探测获取 → 部分组件可能 DecisionSkip        │
│                                                                                 │
│  纳管的特殊情况:                                                                 │
│    1. 已有集群版本 == 目标版本 → 全部 Skip (无需安装/升级)                       │
│    2. 已有集群版本 < 目标版本 → 部分组件 DecisionUpgrade (纳管 + 升级)        │
│    3. 已有集群缺少某些组件 → 部分组件 DecisionInstall (纳管 + 补装)           │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

```go
// controllers/capbke/bkecluster_controller.go — 纳管场景判断

// isManage 判断是否为纳管场景
func (r *BKEClusterReconciler) isManage(bkeCluster *bkev1beta1.BKECluster) bool {
    // BKECluster.Spec.Manage = true 表示纳管模式
    return bkeCluster.Spec.Manage
}

// reconcileCluster 中纳管场景的分发 (§9.4.7 移除后的执行入口中)
func (r *BKEClusterReconciler) reconcileCluster(ctx, phaseCtx, oldCluster, newCluster) {
    // 暂停检查 (最优先)
    if newCluster.Spec.Pause {
        newCluster.Status.ClusterStatus = bkev1beta1.ClusterPaused
        return
    }

    // 场景判断 (无 Legacy 回退)
    switch {
    case isDeleteOrReset(newCluster):
        r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)

    case isManage(newCluster):   // ★ 纳管场景判断
        r.executeManageDAG(ctx, phaseCtx, oldCluster, newCluster)

    case isDryRun(newCluster):
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster, WithDryRun())

    case isScale(oldCluster, newCluster):
        r.executeScaleDAG(ctx, phaseCtx, oldCluster, newCluster)

    case r.shouldUseDeclarativeUpgrade(newCluster):
        r.executeUpgradeDAG(ctx, phaseCtx, oldCluster, newCluster)

    default:
        // 全新安装
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }
}
```

> **注意**：纳管场景判断 (`isManage`) 在扩容 (`isScale`) 和升级 (`shouldUseDeclarativeUpgrade`) 之前，因为纳管模式优先于其他操作 — 纳管时先探测版本，再根据 VersionContext 决定后续操作 (安装/升级/跳过)。

```go
// controllers/capbke/bkecluster_controller.go — 纳管场景入口

func (r *BKEClusterReconciler) executeManageDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1. 解析 ReleaseImage bundle
    bundle, err := r.resolveInstallBundle(ctx, newCluster)

    // 2. 构建纳管 VersionContext (Current 初始为空，由 manage 组件填充)
    vc := upgrade.NewVersionContext()
    // Target 来自 ReleaseImage
    upgrade.FillTargetFromBundle(vc, bundle)
    phaseCtx.SetVersionContext(vc)

    // 3. 构建 DAG (manage 组件在第一个 Batch)
    //    ★ 纳管场景不排除 manage 组件 (与 executeInstallDAG 的区别)
    //    manage 组件探测版本 → 填充 VC.Current → 后续组件 Decide (Skip/Upgrade/Install)
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    // 注意: 不传 excludeComponents 参数，manage 组件保留在 DAG 中

    // 4. 构建 Scheduler (manage handler 需优先注册)
    factory := componentfactory.NewFactoryFromBundle(bundle)
    factory.RegisterInstallHandlers() // 含 EnsureClusterManage

    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:       NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        ManifestStore:      manifest.NewBundleStore(bundle),
        CVStore:            manifest.NewBundleStore(bundle),
        MaxParallelPerBatch: 1, // ★ 纳管首个 Batch 串行 (manage 先执行，填充 Current 后后续组件才能正确 Decide)
    })

    execCtx := buildExecutionContext(ctx, r.Client, newCluster, vc)

    // 5. 执行 DAG
    //    Batch 1: manage → 探测版本 → 填充 VC.Current
    //    Batch 2+: 后续组件 Decide: Current==Target→Skip, Current!=Target→Upgrade, Current==""→Install
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

#### 9.4.3 集群扩容 DAG 化

##### 设计思路

扩容是指向已有集群新增 Master 或 Worker 节点。与全新安装的核心区别在于：已有节点不需要重新安装，仅新节点需要执行安装操作。

扩容的节点安装触发采用**三层机制**：第一层是 DAG Scheduler 的组件级 Decision（VersionContext），决定组件是否执行；第二层是 PhaseRunner 的 `NeedExecute()` 检查（StateCode），决定是否有节点需要操作；第三层是 inline handler 内部的节点级过滤（`filterNodes()`），决定哪些节点需要操作。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              扩容 DAG 化设计思路                                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    PhaseFlow.CalculatePhase() 遍历完整 DeployPhases (11 个 Phase):              │
│      EnsureBKEAgent → EnsureNodesEnv → EnsureCerts → EnsureLoadBalance          │
│      → EnsureMasterInit → EnsureMasterJoin → EnsureWorkerJoin → ...            │
│                                                                                 │
│    每个 Phase 的 NeedExecute() 检查 BKENode.Status.StateCode 位标记:             │
│      新增节点 (StateCode=0): 位标记未设置 → NeedExecute=true → 执行             │
│      已有节点 (位标记已设置): NeedExecute=false → 跳过                           │
│                                                                                 │
│    ★ 不存在独立的 ScalePhases 列表用于执行                                       │
│    ★ ClusterScaleMasterUpPhaseNames 等仅用于状态上报 (设置 ClusterStatus)       │
│    ★ 节点安装 (bkeagent/containerd) 和节点加入 (kubeadm join) 由同一个          │
│       PhaseFlow 驱动, 按 DeployPhases 拓扑顺序依次执行                           │
│                                                                                 │
│    问题: 扩容复用 DeployPhases, 但无法复用 DAG 的版本决策和断点续传              │
│          PhaseFlow 串行执行, 无法实现组件间并行                                  │
│                                                                                 │
│  DAG 化方案 (三层机制):                                                          │
│                                                                                 │
│  第一层: Scheduler 组件级 Decision (VersionContext)                              │
│    fillCurrentFromExistingNodes 填充 Current:                                    │
│      集群级组件 (certs, coredns, ...): Current==Target → Skip (已安装, 跳过)     │
│      节点级组件 (bkeagent, containerd, ...): Current=="" → Install (执行)       │
│    ★ VersionContext 是组件级的, 不是节点级的                                     │
│    ★ 这一层只决定"组件是否执行", 不决定"哪些节点执行"                              │
│                                                                                 │
│  第二层: PhaseRunner.NeedExecute() (StateCode)                                   │
│    PhaseRunner.Execute() 内部调用 phase.NeedExecute():                           │
│      HasNodesNeedingPhase(StateCode) → 有节点位标记未设置 → true → 继续          │
│      所有节点位标记已设置 → false → return nil (跳过 Execute)                     │
│    ★ 这一层决定"是否有节点需要操作", 避免不必要的 Execute 调用                     │
│                                                                                 │
│  第三层: inline handler 节点级过滤 (filterNodes + StateCode)                     │
│    handler 的 Execute() 内部通过 phaseutil.filterNodes() 精确过滤:              │
│      已有节点: 对应位标记已设置 (NodeEnvFlag/NodeBootFlag/MasterInitFlag)       │
│                → 跳过                                                            │
│      新增节点: 对应位标记未设置                                                   │
│                → 执行安装                                                        │
│    ★ 这一层决定"哪些节点执行"                                                    │
│                                                                                 │
│  核心设计:                                                                       │
│  1. EnsureMasterInit handler 幂等改造 — 区分 init (首个 Master) / join (后续)  │
│     通过 getExistingMasterNodes 检查已有 Master 数量判断执行 init 还是 join      │
│  2. 三层节点过滤 — VersionContext + NeedExecute + filterNodes 逐层收窄:         │
│     第一层: VersionContext Current=="" → 组件执行 (跳过集群级组件)              │
│     第二层: NeedExecute(StateCode) → 有节点需要操作 (避免空执行)                  │
│     第三层: filterNodes(StateCode) → 精确定位目标节点                              │
│  3. 复用安装 DAG — 无需独立的扩容 DAG，安装 DAG + 组件级 Current 过滤集群级组件  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

##### Legacy PhaseFlow 扩容执行机制（当前代码）

**核心代码路径**: `pkg/phaseframe/phases/phase_flow.go` + `pkg/phaseframe/phases/list.go`

**1. 不存在独立的 ScalePhases 执行列表**

代码中 `ClusterScaleMasterUpPhaseNames` 等列表**仅用于状态上报**（`calculateClusterStatusByPhase` 设置 `ClusterMasterScalingUp` 等 `ClusterStatus`），不用于决定执行哪些 Phase：

```go
// pkg/phaseframe/phases/list.go — 仅用于状态上报, 不用于执行
ClusterScaleMasterUpPhaseNames = []confv1beta1.BKEClusterPhase{
    EnsureMasterJoinName,  // 仅 Master 扩容状态上报
}
ClusterScaleWorkerUpPhaseNames = []confv1beta1.BKEClusterPhase{
    EnsureWorkerJoinName,  // 仅 Worker 扩容状态上报
}

// pkg/phaseframe/phases/phase_flow.go:380 — 仅在 calculateClusterStatusByPhase 中使用
case phaseName.In(ClusterScaleMasterUpPhaseNames):
    handleClusterScaleMasterUpPhase(ctx, err)  // 设置 ClusterStatus = ClusterMasterScalingUp
```

**2. PhaseFlow 遍历完整 DeployPhases**

扩容时 `PhaseFlow.CalculatePhase()` 遍历 `FullPhasesRegisFunc`（`CommonPhases + DeployPhases + PostDeployPhases`），每个 Phase 的 `NeedExecute()` 自行判断是否需要执行：

```go
// pkg/phaseframe/phases/phase_flow.go:78 — 遍历全部 Phase, 非仅 Scale 相关
func (p *PhaseFlow) calculateAndAddPhases(old, new *bkev1beta1.BKECluster, phasesFuncs ...) {
    for _, f := range phasesFuncs {
        phase := f(p.ctx)
        if phase.NeedExecute(old, new) {  // ★ 每个 Phase 自行判断
            p.BKEPhases = append(p.BKEPhases, phase)
        }
    }
}
```

`DeployPhases` 包含全部 11 个安装 Phase（`list.go:32-44`）：

```go
DeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureBKEAgent,        // bkeagent 推送
    NewEnsureNodesEnv,         // containerd + 系统配置
    NewEnsureClusterAPIObj,    // ClusterAPI 对象
    NewEnsureCerts,            // 证书
    NewEnsureLoadBalance,      // 负载均衡
    NewEnsureMasterInit,       // Master 初始化
    NewEnsureMasterJoin,       // Master 加入
    NewEnsureWorkerJoin,       // Worker 加入
    NewEnsureAddonDeploy,      // 组件部署
    NewEnsureNodesPostProcess, // 后置处理
    NewEnsureAgentSwitch,      // Agent 切换
}
```

**3. 每个 Phase 的 NeedExecute 判断**

每个 Phase 通过 `phaseutil.HasNodesNeedingPhase()` 或 `phaseutil.filterNodes()` 检查 `BKENode.Status.StateCode` 位标记，判断是否有节点需要操作：

| Phase | NeedExecute 判断逻辑 | 新节点 (StateCode=0) | 已有节点 (位标记已设置) |
|-------|---------------------|---------------------|----------------------|
| `EnsureBKEAgent` | `HasNodesNeedingPhase(bkeNodes, NodeAgentPushedFlag)` | **true** → 推送 bkeagent | false → 跳过 |
| `EnsureNodesEnv` | `HasNodesNeedingPhase(bkeNodes, NodeEnvFlag)` | **true** → 安装 containerd/系统配置 | false → 跳过 |
| `EnsureCerts` | 证书已存在 → false | false → 跳过 | false → 跳过 |
| `EnsureClusterAPIObj` | CAPI 对象已存在 → false | false → 跳过 | false → 跳过 |
| `EnsureLoadBalance` | LB 已配置 → false | false → 跳过 | false → 跳过 |
| `EnsureMasterInit` | 已有 Master 且无新 Master → false | **true** → init/join | false → 跳过 |
| `EnsureMasterJoin` | `GetNeedJoinMasterNodesWithBKENodes()` | **true** → kubeadm join | false → 跳过 |
| `EnsureWorkerJoin` | `GetNeedJoinWorkerNodesWithBKENodes()` | **true** → kubeadm join | false → 跳过 |
| `EnsureAddonDeploy` | Addon 已部署 → false | false → 跳过 | false → 跳过 |
| `EnsureNodesPostProcess` | `HasNodesNeedingPhase(NodePostProcessFlag)` | **true** → 后置处理 | false → 跳过 |
| `EnsureAgentSwitch` | 已切换 → false | false → 跳过 | false → 跳过 |

`HasNodesNeedingPhase` 实现（`phaseutil/util.go:1273`）：

```go
func HasNodesNeedingPhase(bkeNodes bkev1beta1.BKENodes, flag int) bool {
    for _, bn := range bkeNodes {
        if bn.Status.StateCode&flag == 0 {  // 位标记未设置
            // 排除 Failed/Deleting/NeedSkip 节点
            if bn.Status.StateCode&bkev1beta1.NodeFailedFlag == 0 &&
                bn.Status.StateCode&bkev1beta1.NodeDeletingFlag == 0 &&
                !bn.Status.NeedSkip {
                return true  // 有节点需要执行此 Phase
            }
        }
    }
    return false
}
```

**4. Phase 内部节点过滤**

Phase 执行时通过 `phaseutil.filterNodes()` + `NodePredicate` 精确过滤目标节点：

```go
// pkg/phaseframe/phases/ensure_nodes_env.go:101 — EnsureNodesEnv 内部过滤
func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
    for _, bn := range bkeNodes {
        // 硬排除: Failed/Deleting/NeedSkip
        if bn.Status.StateCode&bkev1beta1.NodeFailedFlag != 0 ||
           bn.Status.StateCode&bkev1beta1.NodeDeletingFlag != 0 ||
           bn.Status.NeedSkip { continue }
        // 已完成: NodeEnvFlag 已设置 → 跳过
        if bn.Status.StateCode&bkev1beta1.NodeEnvFlag != 0 { continue }
        // 前置未就绪: agent 未就绪 → 跳过
        if bn.Status.StateCode&bkev1beta1.NodeAgentReadyFlag == 0 { continue }
        exceptEnvNodes = append(exceptEnvNodes, bn.ToNode())  // ★ 需要操作的节点
    }
    return exceptEnvNodes
}
```

**节点级 StateCode 位标记说明**：

| 位标记 | 含义 | 设置时机 | 过滤用途 |
|--------|------|---------|---------|
| `NodeAgentPushedFlag` | bkeagent 已推送 | bkeagent 安装完成 | `GetNeedPushAgentNodes`: 未设置 → 需推送 |
| `NodeAgentReadyFlag` | bkeagent 已就绪 | bkeagent 注册完成 | `ensure_nodes_env`: 未设置 → 等待 agent 就绪 |
| `NodeEnvFlag` | 节点环境已初始化 | containerd/系统配置安装完成 | `GetNeedInitEnvNodes`: 未设置 → 需初始化环境 |
| `NodeBootFlag` | 节点已加入集群 | kubeadm join/init 完成 | `GetNeedJoinNodes`: 未设置 → 需加入 |
| `MasterInitFlag` | Master 已初始化 | kubeadm init 完成 | `GetNeedJoinNodes`: 未设置 → 需初始化 |
| `NodeFailedFlag` | 节点失败 | 安装失败 | `filterNodes`: 已设置 → 硬排除 |
| `NodeDeletingFlag` | 节点删除中 | 缩容触发 | `filterNodes`: 已设置 → 硬排除 |

##### DAG 化方案设计

**1. 入口集成 — 复用 executePhaseFlow 模式**

扩容 DAG 化复用升级 DAG 的入口集成模式：在 `executePhaseFlow()` 中增加 `shouldUseScaleDAG()` 判断，类似 `shouldUseDeclarativeUpgrade()`：

```go
// controllers/capbke/bkecluster_controller.go — executePhaseFlow 扩容 DAG 集成

func (r *BKEClusterReconciler) executePhaseFlow(...) (ctrl.Result, error) {
    phaseCtx := phaseframe.NewReconcilePhaseCtx(ctx)... 

    // ★ 扩容 DAG 路径 (新增)
    if r.shouldUseScaleDAG(bkeCluster, oldBkeCluster) {
        bkeClusterLogger().Infof("running scale DAG")
        dagCompleted, dagResult, dagErr := r.executeScaleDAG(ctx, phaseCtx, oldBkeCluster, bkeCluster, bkeLogger)
        if dagErr != nil { return dagResult, dagErr }
        if dagResult.Requeue || dagResult.RequeueAfter > 0 { return dagResult, nil }
        if !dagCompleted { return ctrl.Result{}, nil }
        // 扩容 DAG 完成后继续执行 PhaseFlow (处理 AddonDeploy/AgentSwitch 等非 DAG 组件)
    }

    // 升级 DAG 路径 (现有)
    if r.shouldUseDeclarativeUpgrade(bkeCluster) { ... }

    // PhaseFlow 路径 (现有)
    flow := phases.NewPhaseFlow(phaseCtx) ...
}

// shouldUseScaleDAG 判断是否走扩容 DAG 路径
func (r *BKEClusterReconciler) shouldUseScaleDAG(
    bkeCluster *bkev1beta1.BKECluster,
    oldCluster *bkev1beta1.BKECluster,
) bool {
    // 1. Feature Gate 启用
    if !featuregate.DeclarativeScaleEnabled(bkeCluster) { return false }
    // 2. 扩容场景 (节点数增加)
    if !r.isScale(oldCluster, bkeCluster) { return false }
    // 3. ReleaseImage 已就绪 (install-ready 或 upgrade-ready annotation 存在)
    if _, ok := featuregate.InstallReady(bkeCluster); !ok { return false }
    return true
}
```

**2. 安装 handler 注册**

扩容 DAG 复用安装 DAG 的组件拓扑，需要注册安装 handler（当前代码仅注册了升级 handler）：

```go
// pkg/componentfactory/registry.go — 扩展 registerInlineHandler 支持安装 handler

func registerInlineHandler(f *ComponentFactory, handler, version string) error {
    switch handler {
    // 升级 handler (现有)
    case upgrade.InlineHandlerEtcdUpgrade:
        f.Register(handler, version, phases.NewEnsureEtcdUpgrade)
    case upgrade.InlineHandlerMasterUpgrade:
        f.Register(handler, version, phases.NewEnsureMasterUpgrade)
    // ... 其他升级 handler ...

    // 安装 handler (新增, 扩容 DAG 复用安装 handler)
    case "EnsureBKEAgent":
        f.Register(handler, version, phases.NewEnsureBKEAgent)
    case "EnsureNodesEnv":
        f.Register(handler, version, phases.NewEnsureNodesEnv)
    case "EnsureCerts":
        f.Register(handler, version, phases.NewEnsureCerts)
    case "EnsureClusterAPIObj":
        f.Register(handler, version, phases.NewEnsureClusterAPIObj)
    case "EnsureLoadBalance":
        f.Register(handler, version, phases.NewEnsureLoadBalance)
    case "EnsureMasterInit":
        f.Register(handler, version, phases.NewEnsureMasterInit)
    case "EnsureWorkerJoin":
        f.Register(handler, version, phases.NewEnsureWorkerJoin)
    case "EnsureNodesPostProcess":
        f.Register(handler, version, phases.NewEnsureNodesPostProcess)
    case "EnsureAgentSwitch":
        f.Register(handler, version, phases.NewEnsureAgentSwitch)
    default:
        return fmt.Errorf("unknown inline handler %q", handler)
    }
    return nil
}
```

**3. 三层执行机制 — 代码调用链**

```
executeScaleDAG
  → Scheduler.ExecuteDAG
    → shouldSkipComponent(node)         ← 第一层: VersionContext Current==Target → Skip
    → componentNeedsUpgrade(node)       ← 第一层: VersionContext Current=="" → NeedsExecution=true
    → InlineComponentExecutor.ExecuteComponent
      → NeedsExecution(vc, node.Name)   ← 第一层 (再次检查, inline_executor.go:55)
      → Runner.Execute(handler, version)
        → PhaseRunner.Execute           ← runner.go:28
          → phase.NeedExecute(old, new) ← 第二层: StateCode HasNodesNeedingPhase → false 则 return nil
          → phase.Execute()             ← 第三层: filterNodes(StateCode) 精确过滤
```

**PhaseRunner.Execute 的关键逻辑**（现有代码 `runner.go:28-57`，无需修改）：

```go
// pkg/componentfactory/runner.go — 现有代码, DAG 路径和 PhaseFlow 路径共用

func (r *PhaseRunner) Execute(
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    handler, version string,
) error {
    phase, err := ResolveInlineUpgrade(r.Factory, handler, version, phaseCtx)
    if err != nil { return err }
    if !phase.NeedExecute(oldCluster, newCluster) {  // ★ 第二层: StateCode 检查
        return nil  // 无节点需要操作 → 跳过 Execute
    }
    if err := phase.ExecutePreHook(); err != nil { return err }
    result, err := phase.Execute()  // ★ 第三层: 内部 filterNodes 精确过滤
    if postErr := phase.ExecutePostHook(err); postErr != nil { return postErr }
    if err != nil { return err }
    return nil
}
```

**4. 状态上报**

扩容 DAG 路径需要在执行前后设置 `ClusterStatus`（替代 Legacy PhaseFlow 的 `calculateClusterStatusByPhase`）：

```go
// executeScaleDAG 中状态上报 (复用 executeUpgradeDAG 的 patchClusterStatus 模式)

// 执行前: 设置扩容状态
if err := r.patchClusterStatus(newCluster, bkev1beta1.ClusterMasterScalingUp); err != nil {
    return false, ctrl.Result{}, err
}
// 执行 DAG ...
// 执行成功后: 设置 Ready 状态
newCluster.Status.ClusterStatus = bkev1beta1.ClusterStatusReady
```

##### 代码实现

```go
// controllers/capbke/bkecluster_scale_dag.go 🆕新增

// executeScaleDAG 运行扩容 DAG (复用安装 DAG 拓扑)
// 三层机制: VersionContext (组件级) → NeedExecute (节点级) → filterNodes (精确节点)
func (r *BKEClusterReconciler) executeScaleDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger,
) (bool, ctrl.Result, error) {
    // 1. 解析当前集群版本对应的 ReleaseImage bundle
    //    ★ 扩容使用当前版本 (非 DesiredVersion), 新节点安装到当前版本
    currentVersion, err := r.clusterCurrentOpenFuyaoVersion(ctx, newCluster)
    if err != nil || currentVersion == "" {
        return false, ctrl.Result{}, fmt.Errorf("cannot resolve current version for scale: %w", err)
    }
    bundle, _, err := r.resolveUpgradeBundle(ctx, newCluster, currentVersion)
    if err != nil {
        if isReleaseImageNotReady(err) {
            return false, ctrl.Result{RequeueAfter: releaseImageRequeueInterval}, nil
        }
        return false, ctrl.Result{}, err
    }

    // 2. 构建安装 VersionContext (Current 从当前版本 ReleaseImage 填充)
    vc := upgrade.BuildVersionContextForInstall(bundle)
    // ★ 填充集群级组件 Current (跳过已安装), 节点级组件保持为空 (执行安装)
    r.fillCurrentFromExistingNodes(ctx, newCluster, vc)
    phaseCtx.SetVersionContext(vc)

    // 3. 构建安装 DAG (复用安装 DAG, 与全新安装相同的拓扑结构)
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    if err != nil {
        return false, ctrl.Result{}, fmt.Errorf("build scale DAG: %w", err)
    }

    bkeLogger.Info("scale DAG",
        "currentVersion=%s components=%d source=%s",
        currentVersion, len(dag.NodeNames()), bundle.Source,
    )

    // 4. 状态上报: 设置扩容状态
    if err := r.patchClusterStatus(newCluster, bkev1beta1.ClusterMasterScalingUp); err != nil {
        return false, ctrl.Result{}, err
    }

    // 5. 构建 ComponentFactory (注册安装 handler)
    factory, err := componentfactory.NewFactoryFromBundle(bundle)
    if err != nil {
        return false, ctrl.Result{}, fmt.Errorf("build component factory: %w", err)
    }
    factory.RegisterInstallHandlers() // ★ 注册安装 handler (EnsureBKEAgent 等)

    // 6. 构建 Scheduler (复用升级框架)
    bundleStore := manifest.NewBundleStore(bundle)
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &componentfactory.PhaseRunner{Factory: factory}),
        ManifestStore:   bundleStore,
        CVStore:         bundleStore,
        MaxParallelPerBatch: 8,
    })

    // 7. 构建 ExecutionContext
    execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, nil)

    // 8. 执行 DAG
    //    第一层 (Scheduler): 集群级组件 Current==Target → Skip; 节点级组件 Current=="" → Install
    //    第二层 (PhaseRunner): NeedExecute(StateCode) → 无节点需要则 return nil
    //    第三层 (handler Execute): filterNodes(StateCode) → 精确定位新节点
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return false, ctrl.Result{}, fmt.Errorf("execute scale DAG: %w", err)
    }

    // 9. 扩容完成: 更新状态
    newCluster.Status.ClusterStatus = bkev1beta1.ClusterStatusReady
    return true, ctrl.Result{}, nil
}

// fillCurrentFromExistingNodes 从当前版本 ReleaseImage 填充 VersionContext.Current
// 仅填充集群级组件的 Current → DecisionSkip (跳过已安装的集群级组件)
// 节点级组件 Current 保持为空 → DecisionInstall → PhaseRunner.NeedExecute + filterNodes 过滤
func (r *BKEClusterReconciler) fillCurrentFromExistingNodes(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    vc *upgrade.VersionContext,
) {
    // 1. 解析当前集群版本对应的 ReleaseImage bundle
    currentVersion, err := r.clusterCurrentOpenFuyaoVersion(ctx, bkeCluster)
    if err != nil || currentVersion == "" {
        return // 无法解析当前版本, Current 全空 → 所有组件 DecisionInstall
    }
    currentBundle, err := r.resolveCurrentReleaseBundle(ctx, bkeCluster, currentVersion)
    if err != nil || currentBundle == nil {
        return // 当前版本 ReleaseImage 不可用, Current 全空 → 所有组件 DecisionInstall
    }

    // 2. 从 ReleaseImage bundle 填充 Current (install.components + upgrade.components)
    upgrade.FillCurrentFromBundle(vc, currentBundle)

    // 3. 清除节点级组件的 Current (保持为空, 使三层机制生效)
    //    第一层: Current=="" → NeedsExecution=true → 组件执行
    //    第二层: NeedExecute(StateCode) → 有新节点位标记未设置 → true
    //    第三层: filterNodes(StateCode) → 已有节点跳过, 新增节点执行
    for _, name := range vc.TargetNames() {
        if isNodeScopedComponent(name) {
            vc.SetCurrent(name, "")
        }
    }
}

// isNodeScopedComponent 判断组件是否为节点级 (需要在每个节点上独立执行)
func isNodeScopedComponent(name string) bool {
    switch name {
    case upgrade.ComponentBKEAgent,
        upgrade.ComponentContainerd,
        upgrade.ComponentKubernetesMaster,
        upgrade.ComponentKubernetesWorker,
        upgrade.ComponentEtcd:
        return true
    default:
        return false
    }
}

// isScale 判断是否为扩容场景
func (r *BKEClusterReconciler) isScale(old, new *bkev1beta1.BKECluster) bool {
    oldNodeCount := len(old.Spec.Nodes)
    newNodeCount := len(new.Spec.Nodes)
    return newNodeCount > oldNodeCount
}
```

**EnsureMasterInit 幂等改造**：

```go
// pkg/phaseframe/phases/ensure_master_init.go — 幂等改造

func (e *EnsureMasterInit) Execute() (ctrl.Result, error) {
    bkeCluster := e.Ctx.BKECluster
    bkeNodes, _ := fetchBKENodesIfCPInitialized(e.Ctx, bkeCluster)

    // ★ 判断是 init 还是 join
    existingMasters := getExistingMasterNodes(bkeNodes)
    isNewMaster := len(existingMasters) == 0

    if isNewMaster {
        return e.executeMasterInit()
    }
    return e.executeMasterJoin()
}

// getExistingMasterNodes 从 BKENodes 中筛选已就绪的 Master 节点
func getExistingMasterNodes(bkeNodes bkev1beta1.BKENodes) []confv1beta1.BKENode {
    var masters []confv1beta1.BKENode
    for _, n := range bkeNodes {
        if !slices.Contains(n.Spec.Role, node.MasterNodeRole) &&
           !slices.Contains(n.Spec.Role, node.MasterWorkerNodeRole) {
            continue
        }
        if n.Status.State == confv1beta1.NodeProvisioned ||
           n.Status.State == confv1beta1.NodeReady {
            masters = append(masters, n)
        }
    }
    return masters
}

// executeMasterJoin 后续 Master 加入逻辑 (从 EnsureMasterJoin 迁移)
// 版本信息从 ReleaseImage bundle 获取, 不再依赖 BKECluster.Spec
func (e *EnsureMasterInit) executeMasterJoin() (ctrl.Result, error) {
    ctx, c, bkeCluster, _, log := e.Ctx.Untie()

    // 1. 从 ReleaseImage bundle 获取版本信息
    bundle := e.Ctx.ReleaseBundle
    k8sVersion := releaseVersionFromBundle(bundle, upgrade.ComponentKubernetesMaster)
    etcdVersion := releaseVersionFromBundle(bundle, upgrade.ComponentEtcd)

    // 2. 获取需要 join 的 Master 节点 (StateCode 过滤: !NodeBootFlag && !MasterInitFlag)
    bkeNodes, ok := fetchBKENodesIfCPInitialized(e.Ctx, bkeCluster)
    if !ok {
        return ctrl.Result{Requeue: true}, nil
    }
    needJoinNodes := phaseutil.GetNeedJoinMasterNodesWithBKENodes(bkeCluster, bkeNodes)
    if len(needJoinNodes) == 0 {
        return ctrl.Result{}, nil
    }

    // 3. 调整 KubeadmControlPlane 副本数 (触发 CAPI 创建新 Machine)
    scope, err := phaseutil.GetClusterAPIAssociateObjs(ctx, c, e.Ctx.Cluster)
    if err != nil || scope.KubeadmControlPlane == nil {
        return ctrl.Result{}, fmt.Errorf("get cluster-api associate objs failed: %w", err)
    }
    currentReplicas := *scope.KubeadmControlPlane.Spec.Replicas
    exceptReplicas := currentReplicas + int32(len(needJoinNodes))
    masterNodes := bkeNodes.ToNodes().Master()
    if exceptReplicas > int32(masterNodes.Length()) {
        exceptReplicas = int32(masterNodes.Length())
    }

    if err := phaseutil.UpdateKubeadmControlPlaneReplicas(ctx, c, scope.KubeadmControlPlane, exceptReplicas); err != nil {
        return ctrl.Result{}, fmt.Errorf("scale up KCP failed: %w", err)
    }
    if err := phaseutil.ResumeClusterAPIObj(ctx, c, scope.KubeadmControlPlane); err != nil {
        return ctrl.Result{}, fmt.Errorf("resume KCP failed: %w", err)
    }

    // 4. 等待节点加入 (轮询 Machine.Status.NodeRef)
    if err := e.waitMasterJoin(len(needJoinNodes), needJoinNodes); err != nil {
        _ = phaseutil.UpdateKubeadmControlPlaneReplicas(ctx, c, scope.KubeadmControlPlane, currentReplicas)
        return ctrl.Result{}, fmt.Errorf("wait master join failed: %w", err)
    }

    // 5. 同步节点状态
    if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
        return ctrl.Result{}, fmt.Errorf("sync cluster status failed: %w", err)
    }
    return ctrl.Result{}, nil
}

// waitMasterJoin 轮询等待所有新 Master 节点加入完成
func (e *EnsureMasterInit) waitMasterJoin(nodesCount int, nodesToJoin bkenode.Nodes) error {
    ctx, c, bkeCluster, _, log := e.Ctx.Untie()
    timeOut, err := phaseutil.GetBootTimeOut(bkeCluster)
    if err != nil { log.Warn("get boot timeout failed: %v", err) }
    waitTime := time.Duration(nodesCount) * timeOut
    ctxTimeout, cancel := context.WithTimeout(ctx, waitTime)
    defer cancel()

    successJoinNode := make(map[int]confv1beta1.Node)
    err = waitForNodesJoin(WaitForNodesJoinParams{
        Ctx: ctx, Client: c, BKECluster: bkeCluster,
        NodesToJoin: nodesToJoin, Log: log,
        Timeout: ctxTimeout, SuccessJoinNode: successJoinNode,
    })
    if errors.Is(err, wait.ErrWaitTimeout) {
        return errors.Errorf("wait master join timeout")
    }
    return err
}
```

##### 扩容触发流程示例

以 3 节点集群（master-1, worker-1, worker-2）扩容新增 master-2 为例：

**Legacy PhaseFlow 路径（当前代码）**:

```
前置状态:
  master-1: StateCode = NodeAgentPushedFlag|NodeAgentReadyFlag|NodeEnvFlag|NodeBootFlag|MasterInitFlag
  worker-1: StateCode = NodeAgentPushedFlag|NodeAgentReadyFlag|NodeEnvFlag|NodeBootFlag
  worker-2: StateCode = NodeAgentPushedFlag|NodeAgentReadyFlag|NodeEnvFlag|NodeBootFlag
  master-2: StateCode = 0 (新增节点, 无任何位标记)

PhaseFlow.CalculatePhase() 遍历 DeployPhases (11 个 Phase):
  → EnsureBKEAgent.NeedExecute()
    → HasNodesNeedingPhase(NodeAgentPushedFlag)
    → master-2: NodeAgentPushedFlag 未设置 → true ★ 加入执行列表
  → EnsureNodesEnv.NeedExecute()
    → HasNodesNeedingPhase(NodeEnvFlag)
    → master-2: NodeEnvFlag 未设置 → true ★ 加入执行列表
  → EnsureCerts.NeedExecute()
    → 证书已存在 → false → 跳过
  → EnsureClusterAPIObj.NeedExecute()
    → CAPI 对象已存在 → false → 跳过
  → EnsureLoadBalance.NeedExecute()
    → LB 已配置 → false → 跳过
  → EnsureMasterInit.NeedExecute()
    → 有新 Master → true ★ 加入执行列表
  → EnsureMasterJoin.NeedExecute()
    → GetNeedJoinMasterNodesWithBKENodes() → master-2 未加入 → true ★ 加入执行列表
  → EnsureWorkerJoin.NeedExecute()
    → 无新 Worker → false → 跳过
  → EnsureAddonDeploy.NeedExecute()
    → Addon 已部署 → false → 跳过
  → EnsureNodesPostProcess.NeedExecute()
    → HasNodesNeedingPhase(NodePostProcessFlag) → master-2 未处理 → true ★ 加入执行列表
  → EnsureAgentSwitch.NeedExecute()
    → 已切换 → false → 跳过

PhaseFlow.Execute() 按顺序执行:
  1. EnsureBKEAgent → filterNodes(!NodeAgentPushedFlag) → 只操作 master-2
     → 推送 bkeagent → 设置 master-2 的 NodeAgentPushedFlag|NodeAgentReadyFlag
  2. EnsureNodesEnv → getNodesToInitEnv() → 只操作 master-2 (NodeEnvFlag 未设置)
     → 安装 containerd/系统配置 → 设置 master-2 的 NodeEnvFlag
  3. EnsureMasterInit → getExistingMasterNodes() → master-1 已就绪 → 执行 join
     → GetNeedJoinMasterNodesWithBKENodes() → master-2 未加入 → 执行 kubeadm join
     → 设置 master-2 的 NodeBootFlag
  4. EnsureNodesPostProcess → 只操作 master-2
     → 执行后置脚本 → 设置 master-2 的 NodePostProcessFlag
```

**DAG 化路径（目标设计）**:

```
前置状态: (同上)

executeScaleDAG:
  1. fillCurrentFromExistingNodes 填充 VersionContext:
     集群级组件 (certs, coredns, ...): Current==Target → Skip
     节点级组件 (bkeagent, containerd, ...): Current=="" → Install

  2. Scheduler.ExecuteDAG:

  第一层: Scheduler 组件级 Decision (VersionContext)
    → shouldSkipComponent: 集群级 Current==Target → Skip (certs, coredns, kube-proxy)
    → componentNeedsUpgrade: 节点级 Current=="" → NeedsExecution=true (bkeagent, containerd, ...)

  第二层: PhaseRunner.NeedExecute (StateCode) — runner.go:40
    → EnsureBKEAgent.NeedExecute()
      → HasNodesNeedingPhase(NodeAgentPushedFlag)
      → master-2: NodeAgentPushedFlag 未设置 → true → 继续 Execute ★
    → EnsureNodesEnv.NeedExecute()
      → HasNodesNeedingPhase(NodeEnvFlag)
      → master-2: NodeEnvFlag 未设置 → true → 继续 Execute ★
    → EnsureCerts.NeedExecute()
      → 证书已存在 → false → return nil (跳过)
    → EnsureMasterInit.NeedExecute()
      → 有新 Master → true → 继续 Execute ★

  第三层: inline handler Execute 内部 filterNodes (StateCode)
    Batch: EnsureBKEAgent.Execute()
      → phaseutil.GetNeedPushAgentNodesWithBKENodes():
        predicate = !NodeAgentPushedFlag
        master-1: NodeAgentPushedFlag 已设置 → 跳过
        worker-1/2: NodeAgentPushedFlag 已设置 → 跳过
        master-2: NodeAgentPushedFlag 未设置 → 执行推送 ★
      → 推送完成后设置 master-2 的 NodeAgentPushedFlag|NodeAgentReadyFlag

    Batch: EnsureNodesEnv.Execute()
      → getNodesToInitEnv():
        predicate = !NodeEnvFlag && NodeAgentReadyFlag
        master-1: NodeEnvFlag 已设置 → 跳过
        worker-1/2: NodeEnvFlag 已设置 → 跳过
        master-2: NodeEnvFlag 未设置, NodeAgentReadyFlag 已设置 → 执行初始化 ★
      → 初始化完成后设置 master-2 的 NodeEnvFlag

    Batch: EnsureMasterInit.Execute()
      → getExistingMasterNodes(): master-1 已就绪 → 执行 join (非 init)
      → phaseutil.GetNeedJoinMasterNodesWithBKENodes():
        predicate = !NodeBootFlag && !MasterInitFlag
        master-1: NodeBootFlag 已设置 → 跳过
        master-2: NodeBootFlag 未设置 → 执行 join ★
      → 调整 KCP 副本数 → 等待 master-2 加入
      → join 完成后设置 master-2 的 NodeBootFlag
```

#### 9.4.4 集群删除/重置 DAG 化

##### 设计思路

删除/重置是指清理集群的所有组件。与安装的核心区别在于：安装是创建组件，删除是逆序卸载组件。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              删除/重置 DAG 化设计思路                                              │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    DeletePhases (独立的删除 Phase 列表)                                          │
│      → 逆序执行删除操作                                                          │
│    问题: 删除 Phase 列表硬编码，与安装 Phase 列表不对称                          │
│          新增组件需同时维护安装和删除两套 Phase 列表                             │
│                                                                                 │
│  DAG 化方案:                                                                     │
│    构建卸载 DAG (安装 DAG 的逆序)，复用同一组件声明:                             │
│    安装: bkeagent → nodes-env → certs → ... → agent-switch (正序)               │
│    卸载: agent-switch → ... → certs → nodes-env → bkeagent (逆序)              │
│                                                                                 │
│  核心设计:                                                                       │
│  1. 逆序 DAG — 安装 DAG 反转依赖边，先卸载依赖组件，再卸载被依赖组件            │
│  2. UninstallExecutor — 每种类型的执行器新增 Uninstall 方法                      │
│     inline: 调用 handler 的 Uninstall/Reset 逻辑                                 │
│     manifest: YamlInstaller.DeleteComponent (kubectl delete)                    │
│  3. 非阻塞 — 卸载不阻塞 (某组件卸载失败不阻止后续组件卸载)                      │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

##### 代码实现

```go
// pkg/upgrade/bundle.go — 卸载 DAG 构建

// BuildUninstallDAGFromBundle 构建卸载 DAG (安装 DAG 逆序)
func BuildUninstallDAGFromBundle(
    bundle *releasemanifest.Bundle,
    resolve topology.DependencyResolver,
) (*topology.UpgradeDAG, error) {
    // 1. 复用安装 DAG 构建
    dag, err := BuildInstallDAGFromBundle(bundle, resolve)
    if err != nil {
        return nil, err
    }
    // 2. 逆序 DAG (依赖关系反转: A→B 变为 B→A)
    return dag.Reverse(), nil
}
```

```go
// pkg/topology/component.go — DAG 逆序

// Reverse 返回逆序 DAG (依赖关系反转)
func (g *Graph) Reverse() *Graph {
    reversed := NewGraph()
    // 复制所有节点
    for node := range g.nodes {
        reversed.AddNode(node)
    }
    // 反转所有边: prerequisite → dependent 变为 dependent → prerequisite
    for prerequisite, dependents := range g.outEdges {
        for dependent := range dependents {
            reversed.AddEdge(dependent, prerequisite) // 反转方向
        }
    }
    return reversed
}

// UpgradeDAG.Reverse() 包装
func (d *UpgradeDAG) Reverse() *UpgradeDAG {
    reversed := &UpgradeDAG{
        graph: d.graph.Reverse(),
        nodes: make(map[string]*ComponentNode),
    }
    // 复制节点 (保持 FailurePolicy 等属性)
    for name, node := range d.nodes {
        reversed.nodes[name] = node
    }
    return reversed
}
```

```go
// pkg/dagexec/executor.go — 新增 UninstallComponent 方法

type ComponentExecutor interface {
    ExecuteComponent(ctx context.Context, node *topology.ComponentNode,
        execCtx *ExecutionContext) error
    UninstallComponent(ctx context.Context, node *topology.ComponentNode,
        execCtx *ExecutionContext) error  // ★ 新增
    GetComponentType() ComponentType
}

// InlineComponentExecutor 新增 Uninstall
func (e *InlineComponentExecutor) UninstallComponent(ctx context.Context,
    node *topology.ComponentNode, execCtx *ExecutionContext) error {
    // 调用 handler 的 Uninstall 逻辑 (如 kubeadm reset)
    return e.Runner.Uninstall(ctx, execCtx.Cluster, node.Inline.Handler, node.Inline.Version)
}

// YamlComponentExecutor 新增 Uninstall
func (e *YamlComponentExecutor) UninstallComponent(ctx context.Context,
    node *topology.ComponentNode, execCtx *ExecutionContext) error {
    // 删除 YAML 资源 (kubectl delete)
    pkg, _ := e.store.GetComponentManifests(ctx, node.Name, node.Version, execCtx.TemplateContext)
    return e.applier.DeleteComponent(ctx, pkg)
}
```

```go
// controllers/capbke/bkecluster_controller.go — 删除/重置场景入口

func (r *BKEClusterReconciler) executeUninstallDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1. 解析当前 ReleaseImage bundle (卸载当前版本，不是目标版本)
    bundle, err := r.resolveCurrentReleaseBundle(ctx, newCluster)

    // 2. 构建卸载 DAG (逆序)
    dag, err := upgrade.BuildUninstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))

    // 3. 构建 Scheduler (非阻塞模式: 卸载失败不阻止后续)
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:       NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        ManifestStore:      manifest.NewBundleStore(bundle),
        CVStore:            manifest.NewBundleStore(bundle),
        MaxParallelPerBatch: 2, // 低并行度，避免同时清理过多节点
        DefaultFailurePolicy: "Continue", // ★ 卸载失败不阻塞 (与安装的 FailFast 不同)
    })

    // 4. 标记为卸载模式
    execCtx := buildExecutionContext(ctx, r.Client, newCluster, vc)
    execCtx.UninstallMode = true  // ★ 执行器检查此标记，调用 Uninstall 而非 Execute

    // 5. 执行卸载 DAG
    //    Batch 1: agent-switch → 停止 Agent 监听
    //    Batch 2: kube-proxy/coredns → 删除 addon
    //    Batch 3: kubernetes-worker → kubeadm reset (worker)
    //    Batch 4: kubernetes-master → kubeadm reset (master)
    //    Batch 5: load-balance → 删除 HA
    //    Batch 6: certs → 删除证书
    //    Batch 7: bkeagent → 停止并删除 Agent
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

#### 9.4.5 DryRun 模式 DAG 化

##### 设计思路

DryRun 是指仅模拟执行，不实际修改集群。DAG 化后 DryRun 照常构建和遍历 DAG，但各执行器检查 `DryRun` 标记后仅打印不执行。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              DryRun DAG 化设计思路                                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    EnsureDryRun Phase (独立 Phase)                                              │
│      → 仅打印将要执行的 Phase 列表，不执行任何操作                               │
│    问题: DryRun 逻辑独立于安装/升级，无法利用 DAG 的依赖排序展示执行顺序        │
│                                                                                 │
│  DAG 化方案:                                                                     │
│    在 ExecutionContext 中传递 DryRun 标记:                                       │
│    DAG 照常构建 + 拓扑排序 + 遍历                                               │
│    各执行器检查 DryRun 标记 → 仅打印不执行                                       │
│                                                                                 │
│  核心优势:                                                                       │
│  1. 依赖可视化 — DryRun 输出 DAG 拓扑结构和执行顺序 (用户能看到组件依赖)       │
│  2. 版本预检 — DryRun 时 VersionContext.Decide() 正常工作 (显示哪些会跳过)    │
│  3. 统一路径 — DryRun 和实际执行走同一 DAG，不额外维护 DryRun 逻辑             │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

##### 代码实现

```go
// pkg/dagexec/execution_context.go — 新增 DryRun 字段

type ExecutionContext struct {
    // ... 现有字段 ...
    DryRun       bool   // 🆕新增: DryRun 模式标记
    UninstallMode bool  // 🆕新增: 卸载模式标记 (§9.4.4)
}

// DryRunOption 函数式选项
func WithDryRun() func(*ExecutionContext) {
    return func(ec *ExecutionContext) {
        ec.DryRun = true
    }
}
```

```go
// pkg/dagexec/scheduler.go — executeComponent 检查 DryRun

func (s *Scheduler) executeComponent(
    ctx context.Context,
    node *topology.ComponentNode,
    execCtx *ExecutionContext,
) error {
    // ★ DryRun 检查
    if execCtx.DryRun {
        decision := upgrade.Decide(execCtx.VersionContext, node.Name)
        log.Info("[DryRun] component execution plan",
            "component", node.Name,
            "version", node.Version,
            "type", node.Inline.Handler,
            "decision", decision, // Install / Upgrade / Skip
            "dependencies", node.Dependencies,
        )
        return nil // 不实际执行
    }

    // 实际执行 (现有逻辑)
    cv := s.CVStore.GetComponentVersion(ctx, node.Name, node.Version)
    // ...
}
```

```go
// controllers/capbke/bkecluster_controller.go — DryRun 场景入口

func (r *BKEClusterReconciler) executeDryRunDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 与正常安装 DAG 相同，仅设置 DryRun 标记
    return r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster,
        dagexec.WithDryRun(),
    )
}
```

#### 9.4.6 集群暂停 DAG 化

##### 设计思路

暂停是指临时停止对集群的所有操作。暂停不需要 DAG 化 — 它的语义是"不执行任何操作"，DAG 和 PhaseFlow 都需要跳过。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              集群暂停设计思路                                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    EnsurePaused Phase → 设置 ClusterStatus=Paused，不执行后续 Phase             │
│                                                                                 │
│  DAG 化方案:                                                                     │
│    暂停不需要 DAG 化 — 在 reconcileCluster 入口处前置检查:                      │
│    if bkeCluster.Spec.Pause → 设置 Status=Paused → return (不构建 DAG)        │
│                                                                                 │
│  原因:                                                                           │
│  暂停的语义是"不执行任何操作"，不需要组件级的依赖排序和并行执行                   │
│  前置检查即可满足需求，无需 DAG 遍历                                             │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

##### 代码实现

```go
// controllers/capbke/bkecluster_controller.go — 暂停前置检查

func (r *BKEClusterReconciler) reconcileCluster(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // ★ 暂停检查 (最优先，在任何 DAG 构建之前)
    if newCluster.Spec.Pause {
        newCluster.Status.ClusterStatus = bkev1beta1.ClusterPaused
        return ctrl.Result{}, nil // 不执行任何操作
    }

    // 场景判断 (无 Legacy 回退)
    switch {
    case isDeleteOrReset(newCluster):
        return r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    // ...
    }
}
```

#### 9.4.7 移除后的执行入口

完全移除 Legacy PhaseFlow 后，执行入口简化为场景分发 (无 PhaseFlow 回退)：

```go
func (r *BKEClusterReconciler) reconcileCluster(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // ─── 前置检查 (最优先) ───
    // 暂停: 不执行任何操作
    if newCluster.Spec.Pause {
        newCluster.Status.ClusterStatus = bkev1beta1.ClusterPaused
        return ctrl.Result{}, nil
    }

    // ─── 场景判断 (无 Legacy 回退) ───
    switch {
    // 1. 删除/重置 → 卸载 DAG (逆序)
    case isDeleteOrReset(newCluster):
        return ctrl.Result{}, r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 2. 纳管 → 纳管 DAG (manage 组件探测版本，后续组件 Decide 判断)
    //    ★ 纳管优先于扩容和升级，因为纳管时先探测版本再决定后续操作
    case isManage(newCluster):
        return ctrl.Result{}, r.executeManageDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 3. DryRun → 安装 DAG (仅打印不执行)
    case isDryRun(newCluster):
        return ctrl.Result{}, r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster, WithDryRun())

    // 4. 扩容 → 安装 DAG (VersionContext 自动过滤已有节点)
    case isScale(oldCluster, newCluster):
        return ctrl.Result{}, r.executeScaleDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 5. 升级 → 升级 DAG
    case r.shouldUseDeclarativeUpgrade(newCluster):
        return ctrl.Result{}, r.executeUpgradeDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 6. 默认 → 全新安装 DAG
    default:
        return ctrl.Result{}, r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }

    // 不再有 PhaseFlow 回退
}
```

**场景判断优先级**：

| 优先级 | 场景 | 判断条件 | 执行路径 | 说明 |
|--------|------|---------|---------|------|
| 0 | 暂停 | `Spec.Pause` | 不执行 | 最优先，任何操作都不执行 |
| 1 | 删除/重置 | `Spec.Reset` 或 `DeletionTimestamp` | 卸载 DAG (逆序) | 优先于其他操作 |
| 2 | 纳管 | `Spec.Manage` | 纳管 DAG (manage 探测版本) | 优先于扩容/升级，纳管时先探测再决定 |
| 3 | DryRun | `Spec.DryRun` | 安装 DAG (DryRun) | 优先于扩容/升级 |
| 4 | 扩容 | 新增节点 | 安装 DAG (过滤已有节点) | 优先于升级 |
| 5 | 升级 | `upgrade-ready` annotation | 升级 DAG | — |
| 6 | 安装 | 默认 | 安装 DAG | 兜底 |

### 9.5 平滑升级方案

Legacy PhaseFlow 的完全移除不能一蹴而就，需要分阶段平滑过渡，确保生产环境零中断。

#### 9.5.1 平滑升级核心原则

| 原则 | 说明 |
|------|------|
| **双路径并存** | PhaseFlow 和 DAG 安装路径同时存在，通过 Feature Gate 切换 |
| **逐组件迁移** | 不是一个 openFuyao 版本迁移所有组件，而是逐步将 DeployPhase 迁移到 DAG |
| **灰度验证** | 先在测试环境验证，再灰度到生产环境 |
| **回退能力** | DAG 路径出现问题时，可通过关闭 Feature Gate 回退到 PhaseFlow |
| **版本对齐** | 每个迁移阶段对齐一个 openFuyao 版本，不在运行中切换 |

#### 9.5.2 平滑升级分阶段计划

```
openFuyao v2.7.0  ──────  openFuyao v2.8.0  ──────  openFuyao v2.9.0  ──────  openFuyao v3.0.0
     │                        │                        │                        │
     ├─ Phase 1: 结构扩展     │                        │                        │
     │  扩展 ReleaseImage     │                        │                        │
     │  InstallComponent      │                        │                        │
     │  新增 inline 字段      │                        │                        │
     │  (不改变执行路径)       │                        │                        │
     │                        │                        │                        │
     │  Feature Gate: OFF     │                        │                        │
     │                        │                        │                        │
     │                        ├─ Phase 2: 部分 DAG     │                        │
     │                        │  迁移低风险组件:        │                        │
     │                        │  bkeagent/nodes-env/    │                        │
     │                        │  certs/load-balance    │                        │
     │                        │  PhaseFlow 处理剩余    │                        │
     │                        │                        │                        │
     │                        │  Feature Gate: 灰度    │                        │
     │                        │                        │                        │
     │                        │                        ├─ Phase 3: 全量 DAG     │
     │                        │                        │  迁移高风险组件:        │
     │                        │                        │  kubernetes-master/    │
     │                        │                        │  kubernetes-worker/    │
     │                        │                        │  agent-switch          │
     │                        │                        │  DAG 处理所有安装       │
     │                        │                        │                        │
     │                        │                        │  Feature Gate: ON     │
     │                        │                        │                        │
     │                        │                        │                        ├─ Phase 4: 移除 Legacy
     │                        │                        │                        │  纳管/扩容/删除 DAG化
     │                        │                        │                        │  移除 PhaseFlow 代码
     │                        │                        │                        │
     │                        │                        │                        │  Feature Gate: 移除
```

#### 9.5.3 Phase 1: 结构扩展（v2.7.0）

**目标**：扩展 CRD 结构，不改变任何执行路径。

| 任务 | 说明 |
|------|------|
| 扩展 `ReleaseImageComponent` | 新增 `inline` 字段（omitempty，向后兼容） |
| 定义 `DeclarativeInstallCatalog` | 静态映射表，不被执行路径消费 |
| 定义 `InstallComponentSpec` | 类型定义，不接入 Scheduler |
| 编写安装组件 ComponentVersion | 为所有安装组件编写 `spec.dependencies` |
| 补充 ReleaseImage | v2.7.0 ReleaseImage 的 install.components 补充 inline 字段 |

**风险控制**：不改变任何执行逻辑，仅扩展数据结构，零风险。

#### 9.5.4 Phase 2: 部分 DAG 灰度（v2.8.0）

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

#### 9.5.5 Phase 3: 全量 DAG（v2.9.0）

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

#### 9.5.6 Phase 4: 移除 Legacy（v3.0.0）

**目标**：完全移除 PhaseFlow 的 DeployPhases，将所有场景（纳管/扩容/删除/DryRun/暂停）DAG 化。

**移除步骤**：

| 步骤 | 任务 | 说明 |
|------|------|------|
| 1 | 纳管 DAG 化 | 新增 `manage` 组件，`BuildVersionContextForManage` |
| 2 | 扩容 DAG 化 | `EnsureMasterInit` 幂等改造（区分 init/join） |
| 3 | 删除/重置 DAG 化 | `BuildUninstallDAGFromBundle` + 逆序执行 |
| 4 | DryRun DAG 化 | `ExecutionContext.DryRun` 标记 |
| 5 | 暂停检查迁移 | `shouldUseDeclarativeInstall` 增加暂停检查 |
| 6 | 执行入口重写 | `reconcileCluster` 场景分发，无 PhaseFlow 回退 |
| 7 | 移除 PhaseFlow 代码 | 删除 `DeployPhases` / `PhaseFlow` / 相关 `PhaseStatus` |
| 8 | 移除 Feature Gate | `DeclarativeInstallEnabled` 不再需要 |

**回退方案**：此阶段无回退——PhaseFlow 代码已移除。必须在 Phase 3 充分验证后才执行。

#### 9.5.7 平滑升级风险控制

| 风险 | 缓解措施 |
|------|---------|
| **混合模式行为不一致** | Phase 2 中 DAG 和 PhaseFlow 混合执行，需确保组件间状态正确传递 |
| **部分迁移后中断** | Phase 2 迁移到一半发现问题，可关闭 Feature Gate 回退 |
| **v2.8.0 升级到 v2.9.0 时路径切换** | v2.9.0 安装走 DAG，但集群是从 v2.8.0（PhaseFlow）升级来的，需处理状态兼容 |
| **PhaseFlow 移除后回归** | Phase 4 移除代码后发现问题，无法回退到 PhaseFlow；必须在 Phase 3 充分验证 |
| **CommonPhases 仍依赖 PhaseFlow** | Finalizer/Paused/ClusterManage 等通用 Phase 仍走 PhaseFlow，需单独处理 |

#### 9.5.8 状态追踪迁移

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
| | executeInstallDAG | 安装 DAG 执行入口 + `shouldUseDeclarativeInstall` + 状态追踪 | 3 |
| | 安装 annotation 机制 | `install-ready` annotation + ClusterVersionReconciler 适配 | 2 |
| | Feature Gate | `DeclarativeInstallEnabled` 实现 + 默认关闭验证 | 1 |
| | ComponentVersion 依赖定义 | 为 11 个安装组件编写 `spec.dependencies` + 验证拓扑正确性 | 4 |
| | ReleaseImage 适配 | 为 v2.7.0 ReleaseImage 补充 install.components.inline | 2 |
| | CommonPhases 兼容 | 确保 Finalizer/Paused/ClusterManage 等通用 Phase 与 DAG 共存 | 2 |
| | 安装中断恢复 | 部分组件安装失败后的断点续传 + DeclarativeUpgradeStatus 状态恢复 | 2 |
| | BKEAgent 命令适配 | 安装 handler 与现有 BKEAgent Command/ENV 命令机制集成验证 | 2 |
| **Phase 1 小计** | | | **30** |
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
| **Phase 4: Legacy 移除** | 纳管 DAG 化 | `manage` 组件 + `BuildVersionContextForManage` + 从运行集群探测版本 | 5 |
| | 扩容 DAG 化 | `EnsureMasterInit` 幂等完善 + VersionContext 节点级过滤 | 3 |
| | 删除/重置 DAG 化 | `BuildUninstallDAGFromBundle` + 逆序依赖解析 + 卸载脚本 | 6 |
| | 执行入口重写 | `reconcileCluster` 场景分发 + 无 PhaseFlow 回退 | 3 |
| | PhaseFlow 代码清理 | 移除 `DeployPhases` / `PhaseFlow` / `PhaseStatus` + 依赖分析 + 安全删除 | 5 |
| | Feature Gate 移除 | 移除 `DeclarativeInstallEnabled` + 清理条件判断 | 1 |
| | 回退预案 | Phase 3 充分验证清单 + 无法回退的风险评估 + 手动恢复方案 | 2 |
| **Phase 4 小计** | | | **25** |
| **开发总计** | | | **88** |

### 11.2 测试工作量

| 阶段 | 测试内容 | 工作量（人天） |
|------|---------|---------------|
| **Phase 1** | DAG 构建 + VersionContext 单元测试 + 全新安装集成测试 + PhaseFlow 回归 | 8 |
| **Phase 2** | 混合模式执行 + 状态正确性 + Feature Gate 回退验证 | 5 |
| **Phase 3** | 全量 DAG 安装 + 升级流程 + DryRun 验证 | 7 |
| **Phase 4** | 全场景 E2E（安装/升级/扩容/纳管/删除/DryRun/暂停）+ 回归 + 性能对比 | 9 |
| **测试总计** | | **29** |

### 11.3 文档工作量

| 文档类型 | 文档内容 | 工作量（人天） |
|---------|---------|---------------|
| **设计文档** | 本 KEP 文档完善 | 2 |
| **升级指南** | PhaseFlow → DAG 迁移指南 | 2 |
| **运维手册** | DAG 安装路径运维手册 | 2 |
| **故障排查** | DAG 安装故障排查指南 | 1 |
| **小计** | - | **7 人天** |

### 11.4 总工作量汇总

| 阶段 | 开发（人天） | 测试（人天） | 小计 |
|------|------------|------------|------|
| **Phase 1: 结构扩展** | 30 | 8 | 38 |
| **Phase 2: 灰度迁移** | 15 | 5 | 20 |
| **Phase 3: 全量 DAG** | 18 | 7 | 25 |
| **Phase 4: Legacy 移除** | 25 | 9 | 34 |
| **文档** | - | - | 7 |
| **总计** | **88** | **29** | **124** |

> 开发占比 71%，测试占比 23%，文档占比 6%。

**按 openFuyao 版本节奏估算**：

| openFuyao 版本 | 阶段 | 工作量（人天） | 说明 |
|---------------|------|---------------|------|
| **v2.7.0** | Phase 1: 结构扩展 | 38 | CRD 扩展 + 目录定义 + DAG 构建器 |
| **v2.8.0** | Phase 2: 灰度迁移 | 20 | 低风险组件 DAG 化 + 混合模式 |
| **v2.9.0** | Phase 3: 全量 DAG | 25 | 高风险组件 DAG 化 + 全量验证 |
| **v3.0.0** | Phase 4: Legacy 移除 | 34 | 纳管/扩容/删除 DAG 化 + 代码清理 |
| **文档** | 全程 | 7 | 分阶段交付 |
| **总计** | - | **124** | 4 个版本周期 |

**按人员配置估算**（单阶段）：
- Phase 1（38 人天）：2 人约 4 周，3 人约 2.5 周
- Phase 2（20 人天）：2 人约 2 周，3 人约 1.5 周
- Phase 3（25 人天）：2 人约 2.5 周，3 人约 1.5 周
- Phase 4（34 人天）：2 人约 3.5 周，3 人约 2.5 周

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
| **CommonPhades 依赖 PhaseFlow** | Finalizer/Paused 等 Phase 嵌入 PhaseFlow | 中 | Phase 4 单独处理 CommonPhases 迁移 |

### 12.2 平滑升级风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **Phase 2 混合模式中断** | DAG + PhaseFlow 混合执行行为异常 | 高 | 逐组件迁移 + 充分测试 + Feature Gate 回退 |
| **版本间路径切换** | v2.8.0 PhaseFlow 安装 → v2.9.0 DAG 安装 | 中 | VersionContext 从 PhaseStatus 推导 Current |
| **Phase 4 无回退** | 移除 PhaseFlow 后发现问题无法回退 | 高 | Phase 3 充分验证 + 灰度周期足够长 |
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
│        → Scheduler 读取 cv.Spec.Inline.Handler 解析 Phase                       │
│        消费者: Scheduler.executeComponent                                        │
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
│    2. inline → InlineRunner.Execute(handler)                                    │
│       manifest → ManifestStore.GetComponentManifests → Applier.ApplyComponent   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 13.6 Bundle 的消费者汇总

| 消费者 | 读取字段 | 用途 | 代码位置 |
|--------|---------|------|---------|
| `BuildVersionContextForUpgrade` | `Release.Spec.Install/Upgrade.Components` | 填充 VersionContext.Target/Current | `pkg/upgrade/build_release.go` |
| `BuildDAGFromBundle` | `Release.Spec.Upgrade.Components` | 构建 DAG 组件列表 | `pkg/upgrade/bundle.go` |
| `BundleDependencyResolver` | `Components[key].Spec.Dependencies` | 解析 DAG 依赖边 | `pkg/upgrade/bundle.go` |
| `enrichUpgradeComponent` | `Components[key].Spec.Inline` | 补充 inline handler 信息 | `pkg/upgrade/bundle.go` |
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

> **已抽离为独立 KEP 文档**：[KEP-18: ComponentVersion 执行时条件过滤](kep18-component-condition-filter-design.md)

ComponentVersion 新增 `Condition` 字段（Go Template 表达式），在 DAG 执行时根据集群运行时状态（Operation/ScaleType/NodeCount 等）决定组件是否执行。该机制复用已有 `TemplateContext` 作为模板数据（扩展新增 Operation/ScaleType 等字段），作为 Scheduler 跳过链的第三层检查（位于版本检查之后、执行器分发之前），与 KEP-17 Selector 互补——Selector 在构建时选择子组件，Condition 在执行时过滤组件。

完整设计见 [KEP-18](kep18-component-condition-filter-design.md)。

---

## 附录

### A. 参考文档

1. [KEP-5 声明式升级框架](kep5/kep5.md)
2. [KEP-6 三层状态机设计](kep6-state-machine-v4.md)
3. [KEP-9 Static Pod 类型设计](kep9-staticpod-upgrade-framework.md)
4. [KEP-17 Selector 组件类型设计](kep17-selector-component-design.md)
5. [KEP-18 ComponentVersion 执行时条件过滤](kep18-component-condition-filter-design.md)
6. [声明式集群版本升级方案-支持二进制与 Helm 组件](声明式集群版本升级方案-支持二进制与 Helm 组件.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **ReleaseImageComponent** | ReleaseImage 中组件引用（安装和升级共用），包含 `inline` handler |
| **DeclarativeInstallCatalog** | 安装组件目录，映射组件名到执行模式（inline/manifest） |
| **BuildInstallDAGFromBundle** | 从 ReleaseImage bundle 构建安装 DAG |
| **DecisionInstall** | VersionContext 决策：current 为空且 target 有值时触发安装 |
| **DeclarativeInstallEnabled** | Feature Gate，控制 DAG 安装路径是否启用 |
| **install-ready annotation** | `cvo.openfuyao.cn/install-ready`，触发 DAG 安装路径 |

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

本提案设计 ReleaseImage 安装组件的声明式定义，使安装流程与升级流程统一为 DAG 驱动。当前 ReleaseImage 的 `spec.install.components` 仅包含 `{name, version}` 两个字段，缺少执行模式（inline/manifest/staticpod）、inline handler、依赖关系等声明式元数据；且安装流程完全由硬编码的 `DeployPhases` PhaseFlow 驱动，未消费 ReleaseImage 的安装组件列表。本提案扩展 `ReleaseImageInstallComponent` 结构，新增安装组件目录（`DeclarativeInstallCatalog`）和安装 DAG 构建器，使安装流程也能享受声明式组件管理的优势：依赖管理、并行执行、版本追踪、可观测性。

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

1. **结构对称**：`ReleaseImageInstallComponent` 与 `ReleaseImageUpgradeComponent` 结构对齐
2. **统一 DAG**：安装流程也通过 DAG 驱动，复用 Scheduler/ExecutorRegistry/VersionContext
3. **统一目录**：`DeclarativeInstallCatalog` 与 `DeclarativeUpgradeCatalog` 结构一致
4. **渐进迁移**：通过 Feature Gate 控制，PhaseFlow 和 DAG 安装路径可并存
5. **向后兼容**：迁移期间 ReleaseImage 可同时包含旧格式和声明式格式的安装组件

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| **CRD 扩展** | `ReleaseImageInstallComponent` 新增 `inline` 字段 |
| **安装组件目录** | `DeclarativeInstallCatalog` 定义安装组件映射 |
| **安装 DAG 构建** | `BuildInstallDAG` 从 ReleaseImage bundle 构建安装 DAG |
| **安装 DAG 执行** | 复用 `Scheduler.ExecuteDAG`，新增 `DecisionInstall` |
| **Feature Gate** | `DeclarativeInstallEnabled` 控制新旧路径切换 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 旧格式 `{name, version}` 的 `ReleaseImageInstallComponent` 必须继续支持 |
| **PhaseFlow 共存** | 迁移期间 PhaseFlow 和 DAG 安装路径可并存，通过 Feature Gate 切换 |
| **复用升级框架** | 安装 DAG 复用 Scheduler、ExecutorRegistry、InlineRunner 等已有组件 |
| **幂等性** | 安装操作必须幂等，支持 Reconcile 重入 |
| **依赖正确性** | 安装 DAG 的依赖关系必须反映实际安装顺序约束 |

### 3.3 非目标

- 不在本文档定义 PreCheck/PostCheck（引用 KEP-5-2）
- 不在本文档定义回滚策略（安装失败不回滚，直接重试）
- 不重写现有 DeployPhases 的 Phase 实现（复用已有代码）

## 4. ReleaseImageInstallComponent 结构扩展

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

### 4.2 目标结构（对称）

```go
// api/v1alpha1/releaseimage_types.go

// ReleaseImageInstallComponent 定义安装组件引用 🔄扩展
type ReleaseImageInstallComponent struct {
    // 组件名称（对应 ComponentVersion.Name）
    Name string `json:"name,omitempty"`
    
    // 组件版本（对应 ComponentVersion.Version）
    Version string `json:"version,omitempty"`
    
    // Inline handler 配置（type=inline 时指定）🆕新增
    // 与 ReleaseImageUpgradeInline 结构一致
    Inline *ReleaseImageInstallInline `json:"inline,omitempty"`
}

// ReleaseImageInstallInline 定义安装组件的 inline handler 🆕新增
// 结构与 ReleaseImageUpgradeInline 对称
type ReleaseImageInstallInline struct {
    // Handler 名称（对应 ComponentFactory 注册的 handler）
    // 示例: "EnsureBKEAgent", "EnsureCerts", "EnsureMasterInit"
    Handler string `json:"handler,omitempty"`
    
    // Handler 版本
    Version string `json:"version,omitempty"`
}
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

**方案**：将 `UpgradeComponentSpec` 重命名为中性的 `ComponentSpec`，`UpgradeExecutionMode` 重命名为 `ExecutionMode`，同时用于安装和升级目录：

```go
// pkg/upgrade/catalog.go

// ExecutionMode 描述组件的执行方式 (安装和升级通用)
type ExecutionMode string

const (
    ExecutionManifest ExecutionMode = "manifest"
    ExecutionInline   ExecutionMode = "inline"
)

// ComponentSpec 映射 ReleaseImage 组件名到执行模式 (安装和升级通用)
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
```

> **重命名影响**：`UpgradeComponentSpec` → `ComponentSpec`，`UpgradeExecutionMode` → `ExecutionMode`。现有引用处 (catalog.go、build.go、scheduler.go 等) 需批量替换类型名，但**字段和方法不变**，属于机械替换。

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
func InstallComponentsFromBundle(bundle *releasemanifest.Bundle) ([]apiv1.ReleaseImageInstallComponent, error) {
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
    
    // 2. 转换为 ComponentNode（复用 UpgradeComponent 结构）
    var components []topology.ReleaseImageUpgradeComponent
    for _, ic := range installComponents {
        comp := topology.ReleaseImageUpgradeComponent{
            Name:    ic.Name,
            Version: ic.Version,
        }
        if ic.Inline != nil {
            comp.Inline = &topology.ReleaseImageUpgradeInline{
                Handler: ic.Inline.Handler,
                Version: ic.Inline.Version,
            }
        }
        components = append(components, comp)
    }
    
    // 3. 复用 BuildUpgradeDAG（同一构建逻辑）
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

```go
// controllers/capbke/bkecluster_controller.go

func (r *BKEClusterReconciler) executePhaseFlow(ctx, phaseCtx, oldCluster, newCluster) {
    // 现有：DAG 升级路径
    if r.shouldUseDeclarativeUpgrade(newCluster) {
        r.executeUpgradeDAG(...)
        ...
    }
    
    // 🆕新增：DAG 安装路径
    if r.shouldUseDeclarativeInstall(newCluster) {
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
        // 安装 DAG 完成后跳过 DeployPhases
        return
    }
    
    // 现有：PhaseFlow 路径（Legacy）
    flow := phases.NewPhaseFlow(phaseCtx)
    ...
}
```

**PhaseFlow 路径（Legacy）的适用场景**：

以下场景仍会走 Legacy PhaseFlow 路径，不走 DAG 安装路径：

| 场景 | 原因 | 说明 |
|------|------|------|
| **Feature Gate 未启用** | `DeclarativeInstallEnabled = false`（默认） | 迁移期间默认关闭，确保生产稳定；正式启用后此场景消失 |
| **ReleaseImage 无 inline handler** | `install.components[].inline` 字段为空 | 旧格式 ReleaseImage 仅有 `{name, version}`，DAG 无法分发执行器；需升级 ReleaseImage 格式后才能走 DAG |
| **无 install-ready annotation** | ClusterVersionReconciler 未设置 `install-ready` | 仅当 ReleaseImage Status.Phase=Valid 且 ClusterVersion 判定安装就绪时才设置 annotation |
| **纳管已有集群** | `BKECluster.Spec.Manage = true`（纳管模式） | 纳管现有集群不走标准安装流程，PhaseFlow 中的 `EnsureClusterManage` 处理纳管逻辑 |
| **集群扩容（新增节点）** | `EnsureMasterJoin` / `EnsureWorkerJoin` | 扩容时只执行部分 Phase（Join），非完整安装；DAG 安装路径面向全新安装，扩容仍走 PhaseFlow 中的 Scale Phase |
| **集群删除/重置** | `BKECluster.Spec.Reset = true` 或 `DeletionTimestamp` 非空 | 删除/重置走 `DeletePhases`，与安装 DAG 无关 |
| **DryRun 模式** | `BKECluster.Spec.DryRun = true` | DryRun 走 PhaseFlow 中的 `EnsureDryRun`，不实际执行安装 |
| **集群暂停** | `BKECluster.Spec.Pause = true` | 暂停状态下不执行任何操作，走 PhaseFlow 中的 `EnsurePaused` |

> **注意**：扩容场景（新增 Master/Worker 节点）虽然不走 DAG 安装路径，但未来可考虑将扩容也纳入 DAG 驱动（如 `kubernetes-master` 组件的 `DecisionInstall` 触发 `EnsureMasterJoin` handler），作为后续优化方向。

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
| **Phase 1** | 结构扩展 | 扩展 `ReleaseImageInstallComponent`，新增 `inline` 字段 | 不启用 |
| **Phase 2** | 安装 DAG 实现 | 实现 `BuildInstallDAG`、`executeInstallDAG`、`DeclarativeInstallCatalog` | 灰度启用 |
| **Phase 3** | 验证与切换 | 测试环境验证，逐步切换到 DAG 安装路径 | 正式启用 |
| **Phase 4** | 移除 PhaseFlow | 移除 DeployPhases 硬编码列表，安装完全 DAG 驱动 | 移除 Feature Gate |

### 9.3 向后兼容

1. **旧格式兼容**：`ReleaseImageInstallComponent` 的 `inline` 字段是 `omitempty`，旧格式 `{name, version}` 仍然有效
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

**当前实现**：`EnsureClusterManage` Phase 处理纳管逻辑（检测已有集群组件版本、写入 BKECluster.Status）。

**DAG 化方案**：

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
{ Name: "manage", Mode: InstallExecutionInline, InlineHandler: "EnsureClusterManage" }
```

**VersionContext 特殊处理**：纳管场景下 Current 不为空（从已有集群探测版本），Target 来自 ReleaseImage：

```go
// BuildVersionContextForManage 为纳管场景构建 VersionContext
func BuildVersionContextForManage(
    targetBundle *releasemanifest.Bundle,
    detectedVersions map[string]string,  // 从已有集群探测的版本
) *VersionContext {
    vc := NewVersionContext()
    // Current 来自探测结果
    for name, ver := range detectedVersions {
        vc.SetCurrent(name, ver)
    }
    // Target 来自 ReleaseImage
    for _, comp := range targetBundle.Release.Spec.Install.Components {
        vc.SetTarget(comp.Name, comp.Version)
    }
    return vc
}
```

**触发机制**：`BKECluster.Spec.Manage = true` 时，ClusterVersionReconciler 设置 `install-ready` annotation 触发 DAG，DAG 中 `manage` 组件作为第一个 Batch 执行（探测版本 + 填充 Current），后续组件通过 `VersionContext.Decide()` 判断是否需要执行（已有正确版本的跳过）。

#### 9.4.3 集群扩容 DAG 化

**当前实现**：`EnsureMasterJoin` / `EnsureWorkerJoin` Phase 处理新增节点。

**DAG 化方案**：将扩容纳入安装 DAG，通过 VersionContext 的 `DecisionInstall` 触发：

```yaml
# ReleaseImage install.components 已有 kubernetes-master / kubernetes-worker
# 扩容时新增节点的 VersionContext.Current 为空 → DecisionInstall
```

**扩容 DAG 结构**：

```
扩容 DAG（新增 Master 节点）:

Batch 1: [bkeagent]              ← 新节点需要先安装 Agent
    └─ inline: EnsureBKEAgent → SSH 推送 bkeagent

Batch 2: [nodes-env]            ← 新节点需要环境准备
    └─ inline: EnsureNodesEnv

Batch 3: [kubernetes-master]    ← 新节点 kubeadm join
    └─ inline: EnsureMasterInit (幂等：已有 Master 跳过，新节点执行 join)

Batch 4: [kube-proxy, coredns]   ← DaemonSet 自动调度到新节点
    └─ manifest: YamlInstaller Apply
```

**关键设计**：
- `EnsureMasterInit` handler 需区分"首个 Master（init）"和"后续 Master（join）"，通过检查已有 Master 数量判断
- 已有节点的组件 VersionContext.Current 已有值且与 Target 一致 → `DecisionSkip`，跳过执行
- 新增节点的组件 VersionContext.Current 为空 → `DecisionInstall`，执行安装
- 扩容不再走独立的 Scale Phase，而是复用安装 DAG（VersionContext 自动过滤已完成的节点）

#### 9.4.4 集群删除/重置 DAG 化

**当前实现**：`EnsureDeleteOrReset` Phase 处理删除/重置。

**DAG 化方案**：构建卸载 DAG（安装 DAG 逆序），逐组件卸载：

```go
// BuildUninstallDAGFromBundle 构建卸载 DAG（逆序）
func BuildUninstallDAGFromBundle(
    bundle *releasemanifest.Bundle,
    resolve topology.DependencyResolver,
) (*topology.UpgradeDAG, error) {
    // 复用 BuildInstallDAGFromBundle，然后逆序
    dag, err := BuildInstallDAGFromBundle(bundle, resolve)
    if err != nil {
        return nil, err
    }
    // 逆序 DAG（依赖关系反转）
    return dag.Reverse(), nil
}
```

**卸载 DAG 结构**：

```
卸载 DAG（安装 DAG 逆序）:

Batch 1: [agent-switch, nodes-postprocess]   ← 先停止 Agent 监听 + 后置清理
    ├─ inline: EnsureAgentSwitch → 切回原 Agent
    └─ inline: EnsureNodesPostProcess → 清理

Batch 2: [kube-proxy, coredns]               ← 卸载附加组件
    ├─ manifest: YamlInstaller Delete
    └─ manifest: YamlInstaller Delete

Batch 3: [kubernetes-worker]                 ← 清理 Worker 节点
    └─ inline: kubeadm reset (worker)

Batch 4: [kubernetes-master]                 ← 清理 Master 节点
    └─ inline: kubeadm reset (master)

Batch 5: [load-balance]                      ← 清理 HA
    └─ inline: 删除 haproxy/keepalived

Batch 6: [certs]                            ← 清理证书
    └─ inline: 删除证书

Batch 7: [cluster-api-obj]                   ← 清理 CAPI 对象

Batch 8: [bkeagent]                          ← 最后卸载 Agent
    └─ inline: 停止并删除 bkeagent
```

**触发机制**：`BKECluster.Spec.Reset = true` 或 `DeletionTimestamp` 非空时，构建卸载 DAG 执行。

#### 9.4.5 DryRun 模式 DAG 化

**当前实现**：`EnsureDryRun` Phase 处理 DryRun。

**DAG 化方案**：在 ExecutionContext 中传递 `DryRun` 标记，各执行器检查标记后仅打印不实际执行：

```go
// ExecutionContext 新增 DryRun 字段
type ExecutionContext struct {
    // ... 现有字段 ...
    DryRun bool  // 🆕新增
}

// Scheduler 执行时检查 DryRun
func (s *Scheduler) executeComponent(ctx, node, execCtx) error {
    if execCtx.DryRun {
        // 仅打印将要执行的操作，不实际执行
        log.Info("DryRun: would execute component", "name", node.Name, "version", node.Version)
        return nil
    }
    // 实际执行
    ...
}
```

**触发机制**：`BKECluster.Spec.DryRun = true` 时，`executeInstallDAG` 中设置 `execCtx.DryRun = true`，DAG 照常构建和遍历但各组件仅打印不执行。

#### 9.4.6 集群暂停 DAG 化

**当前实现**：`EnsurePaused` Phase 处理暂停。

**DAG 化方案**：在 `shouldUseDeclarativeInstall` 中增加暂停检查，暂停时不构建 DAG：

```go
func (r *BKEClusterReconciler) shouldUseDeclarativeInstall(bkeCluster *bkev1beta1.BKECluster) bool {
    // ... 现有检查 ...
    
    // 🆕新增：暂停检查
    if bkeCluster.Spec.Pause {
        return false  // 暂停时不执行任何操作
    }
    
    return ok
}
```

**说明**：暂停场景不需要 DAG 化——暂停的语义是"不执行任何操作"，DAG 路径和 PhaseFlow 路径都需要跳过执行。移除 PhaseFlow 后，暂停检查仍保留在 `shouldUseDeclarativeInstall` 中作为前置判断。

#### 9.4.7 移除后的执行入口

完全移除 Legacy PhaseFlow 后，执行入口简化为：

```go
func (r *BKEClusterReconciler) reconcileCluster(ctx, phaseCtx, oldCluster, newCluster) {
    // 场景判断（无 Legacy 回退）
    switch {
    case isDeleteOrReset(newCluster):
        // 卸载 DAG
        r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    
    case isPaused(newCluster):
        // 暂停：不执行
        return
    
    case isDryRun(newCluster):
        // DryRun DAG
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster, WithDryRun())
    
    case isScale(newCluster):
        // 扩容 DAG（复用安装 DAG，VersionContext 自动过滤）
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    
    case isManage(newCluster):
        // 纳管 DAG
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster, WithManage())
    
    case r.shouldUseDeclarativeUpgrade(newCluster):
        // 升级 DAG
        r.executeUpgradeDAG(...)
    
    default:
        // 全新安装 DAG
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }
    
    // 不再有 PhaseFlow 回退
}
```

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
| 扩展 `ReleaseImageInstallComponent` | 新增 `inline` 字段（omitempty，向后兼容） |
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
| **Phase 1: 结构扩展** | CRD 扩展 | `ReleaseImageInstallComponent` 新增 `inline` 字段 + deepcopy + webhook | 2 |
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

## 附录

### A. 参考文档

1. [KEP-5 声明式升级框架](kep5/kep5.md)
2. [KEP-6 三层状态机设计](kep6-state-machine-v4.md)
3. [KEP-9 Static Pod 类型设计](kep9-staticpod-upgrade-framework.md)
4. [声明式集群版本升级方案-支持二进制与 Helm 组件](声明式集群版本升级方案-支持二进制与 Helm 组件.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **ReleaseImageInstallComponent** | ReleaseImage 中安装组件引用，扩展后包含 `inline` handler |
| **DeclarativeInstallCatalog** | 安装组件目录，映射组件名到执行模式（inline/manifest） |
| **BuildInstallDAGFromBundle** | 从 ReleaseImage bundle 构建安装 DAG |
| **DecisionInstall** | VersionContext 决策：current 为空且 target 有值时触发安装 |
| **DeclarativeInstallEnabled** | Feature Gate，控制 DAG 安装路径是否启用 |
| **install-ready annotation** | `cvo.openfuyao.cn/install-ready`，触发 DAG 安装路径 |

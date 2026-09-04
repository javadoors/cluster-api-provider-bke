# KEP-18: ComponentVersion 执行时条件过滤

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-18 |
| **标题** | ComponentVersion 执行时条件过滤：基于 Go Template 的组件跳过机制 |
| **状态** | `provisional` |
| **类型** | Feature |
| **依赖** | KEP-5 声明式升级框架、KEP-10 安装流程声明式设计 |
| **来源** | 从 `kep10-install-components-declarative-design.md` §14 抽离 |

---

## 目录

1. [设计动机](#1-设计动机)
2. [数据结构设计](#2-数据结构设计)
3. [扩展 TemplateContext — 复用已有模板数据结构](#3-扩展-templatecontext--复用已有模板数据结构)
4. [Scheduler 集成设计](#4-scheduler-集成设计)
5. [跳过链总览](#5-跳过链总览)
6. [Condition 表达式语法](#6-condition-表达式语法)
7. [ComponentVersion YAML 示例](#7-componentversion-yaml-示例)
8. [扩容场景 Condition 过滤示例](#8-扩容场景-condition-过滤示例)
9. [向后兼容性](#9-向后兼容性)
10. [与 KEP-17 Selector 的区别](#10-与-kep-17-selector-的区别)
11. [工作量评估](#11-工作量评估)
12. [风险与缓解措施](#12-风险与缓解措施)

---

## 1. 设计动机

当前 DAG 执行的跳过逻辑仅基于版本比较（`VersionContext.NeedsExecution`）和完成状态（`DeclarativeUpgradeStatus.IsCompleted`），无法表达"当集群满足某条件时才执行此组件"的语义。

**典型场景**：

| 场景 | 条件表达式 | 说明 |
|------|-----------|------|
| 仅 Master 扩容时执行 | `{{ eq .ScaleType "master" }}` | 扩容 DAG 中 Worker 组件跳过 |
| 仅 Worker 扩容时执行 | `{{ eq .ScaleType "worker" }}` | 扩容 DAG 中 Master 组件跳过 |
| 仅升级场景执行 | `{{ eq .Operation "upgrade" }}` | 安装场景跳过仅升级需要的组件 |
| 仅首次安装执行 | `{{ eq .Operation "install" }}` | 升级场景跳过仅安装需要的组件 |
| 集群规模大于 3 节点时执行 | `{{ gt .NodeCount 3 }}` | 小集群跳过非必要组件 |
| 离线部署时执行 | `{{ eq .DeployMode "offline" }}` | 在线部署跳过离线专用组件 |

---

## 2. 数据结构设计

### 2.1 ComponentVersionSpec 新增 Condition 字段

```go
// api/v1alpha1/componentversion_types.go

type ComponentVersionSpec struct {
    Name    string        `json:"name"`
    Type    ComponentType `json:"type"`
    Version string        `json:"version"`
    Inline  *InlineSpec   `json:"inline,omitempty"`
    YAML    *YAMLSpec     `json:"yaml,omitempty"`
    SubComponents  []SubComponent      `json:"subComponents,omitempty"`
    Compatibility CompatibilitySpec   `json:"compatibility,omitempty"`
    Dependencies  []Dependency        `json:"dependencies,omitempty"`
    UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
    Resources     []ResourceSpec      `json:"resources,omitempty"`

    // Condition is a Go Template expression evaluated at execution time.
    // Empty means always execute (no filtering).
    // The template receives TemplateContext with cluster state variables.
    // Evaluation result "true" (string) → execute; anything else → skip.
    // +optional
    Condition string `json:"condition,omitempty"`
}
```

### 2.2 SubComponent 新增 Condition 字段

```go
// api/v1alpha1/componentversion_types.go

type SubComponent struct {
    Name    string `json:"name"`
    Version string `json:"version"`

    // Condition is a Go Template expression for sub-component inclusion.
    // Evaluated during composite expansion (DAG build time) for selector-style
    // sub-component selection, AND at execution time for runtime filtering.
    // Empty means always include.
    // +optional
    Condition string `json:"condition,omitempty"`
}
```

### 2.3 ReleaseImageUpgradeComponent 不新增 Condition 字段

`ReleaseImageUpgradeComponent` 是 ReleaseImage 中的**轻量引用结构**（Name + Version + 可选 Inline handler），不是组件定义本身。Condition 属于组件定义，应存储在 `ComponentVersion.Spec` 中，不应放在引用上。

**数据流**：

```
ReleaseImage.Spec.Upgrade.Components[]
  → ReleaseImageUpgradeComponent { Name, Version, Inline }  ← 引用 (轻量)
      │
      │ DAG 构建时按 Name+Version 创建 ComponentNode
      ▼
ComponentNode { Name, Version, Inline, FailurePolicy, Dependencies }  ← DAG 节点
      │
      │ 执行时 Scheduler 通过 CVStore 加载
      ▼
ComponentVersion.Spec { Type, Condition, Dependencies, ... }  ← 组件定义 (完整)
      │
      │ shouldExecuteByCondition 读取 cv.Spec.Condition
      ▼
EvaluateCondition(condition, execCtx.TemplateContext)  ← 求值 (复用 TemplateContext)
```

**不在 ReleaseImageUpgradeComponent 上加 Condition 的原因**：

| 维度 | 说明 |
|------|------|
| **职责分离** | `ReleaseImageUpgradeComponent` 是引用（指向 ComponentVersion），`ComponentVersion` 是定义；Condition 属于定义 |
| **传递性问题** | DAG 节点 `ComponentNode` 无 Condition 字段，`shouldExecuteByCondition` 通过 CVStore 加载 `ComponentVersion`，无法访问 `ReleaseImageUpgradeComponent`；在引用上加 Condition 也无法传递到执行时 |
| **单一数据源** | Condition 只从 `ComponentVersion.Spec.Condition` 读取，避免多来源覆盖导致的歧义 |
| **与现有字段一致** | `Type`、`Dependencies`、`UpgradeStrategy` 等字段也只在 `ComponentVersion.Spec` 上，`ReleaseImageUpgradeComponent` 不携带这些定义性字段 |

> **设计原则**：`ReleaseImageUpgradeComponent` 保持轻量引用角色（Name + Version + Inline handler override），组件定义性字段（Type/Condition/Dependencies 等）统一存储在 `ComponentVersion.Spec`。

---

## 3. 扩展 TemplateContext — 复用已有模板数据结构

### 3.1 设计思路 — 为什么扩展 TemplateContext 而非新建 ConditionContext

代码库中已有 `manifest.TemplateContext`（`pkg/manifest/types.go:27`），是集群状态的**安全投影**，用于 Go Template 渲染（manifest 渲染）。它已存在于 `ExecutionContext.TemplateContext` 中，由 `buildTemplateContext()` 从 `BKECluster` 构建。

| 方案 | 优点 | 缺点 |
|------|------|------|
| **新建 ConditionContext** | 职责隔离 | 新增类型 + 转换函数 `BuildConditionContext`；与 TemplateContext 字段重叠（ClusterName/Namespace/K8sVersion 等）；两个结构维护同步 |
| **直接用 ExecutionContext** | 无新增 | 暴露 Client/ComponentStatusUpdater/Log 等敏感字段给模板（安全风险）；模板表达式冗长 `{{ .Cluster.Name }}` |
| **扩展 ExecutionContext** | 无新增类型 | ExecutionContext 膨胀为 god object；执行容器与模板数据职责混淆 |
| **扩展 TemplateContext ★** | 无新增类型/转换函数；已有安全投影；manifest 渲染也能用到 Operation/ScaleType；`shouldExecuteByCondition` 直接读 `execCtx.TemplateContext` | TemplateContext 字段增多（可接受，均为集群状态投影） |

**选择扩展 TemplateContext**：Condition 表达式和 manifest 渲染都是 Go Template 求值，共用同一数据结构天然合理。Operation/ScaleType/NodeCount 等字段对 manifest 渲染同样有用（如按操作类型渲染不同 manifest）。

### 3.2 TemplateContext 扩展

```go
// pkg/manifest/types.go — 扩展现有结构

// TemplateContext carries cluster fields used to render component templates
// and evaluate condition expressions.
type TemplateContext struct {
    // --- 现有字段 (manifest 渲染) ---
    ClusterName       string
    Namespace         string
    KubernetesVersion string
    OpenFuyaoVersion  string

    // --- 新增字段 (Condition 求值 + manifest 渲染) ---

    // Operation is the current operation type: install / upgrade / scale / rollback / manage
    // +optional
    Operation string

    // ScaleType is the scale sub-type when Operation=scale: master / worker / "" (not scale)
    // +optional
    ScaleType string

    // NodeCount is the total number of nodes in the cluster
    // +optional
    NodeCount int

    // MasterCount is the number of master nodes
    // +optional
    MasterCount int

    // WorkerCount is the number of worker nodes
    // +optional
    WorkerCount int

    // DeployMode is the deployment mode: offline / online
    // +optional
    DeployMode string

    // DryRun indicates whether this is a dry-run operation
    // +optional
    DryRun bool

    // Variables holds arbitrary key-value pairs for extensibility
    // +optional
    Variables map[string]string
}
```

### 3.3 buildTemplateContext 扩展

现有 `buildTemplateContext()`（`execution_context.go:66`）已从 `BKECluster` 填充 ClusterName/Namespace/K8sVersion/OpenFuyaoVersion。扩展其填充新增字段：

```go
// pkg/dagexec/execution_context.go — 扩展现有函数

func buildTemplateContext(cluster *bkev1beta1.BKECluster) manifest.TemplateContext {
    var tmpl manifest.TemplateContext
    if cluster == nil {
        return tmpl
    }

    // --- 现有逻辑 (不变) ---
    tmpl.ClusterName = cluster.GetName()
    tmpl.Namespace = cluster.GetNamespace()
    tmpl.DryRun = cluster.Spec.DryRun
    if cluster.Spec.ClusterConfig != nil {
        spec := cluster.Spec.ClusterConfig.Cluster
        tmpl.KubernetesVersion = spec.KubernetesVersion
        tmpl.OpenFuyaoVersion = spec.OpenFuyaoVersion
    }

    // --- 新增逻辑 (Condition 求值字段) ---

    // Node counts from BKENode status
    nodes := cluster.Status.Nodes
    tmpl.NodeCount = len(nodes)
    for _, n := range nodes {
        if isMasterRole(n) {
            tmpl.MasterCount++
        } else {
            tmpl.WorkerCount++
        }
    }

    // Operation and ScaleType from cluster status
    tmpl.Operation = inferOperation(cluster)
    tmpl.ScaleType = inferScaleType(cluster)

    // DeployMode from cluster config
    tmpl.DeployMode = inferDeployMode(cluster)

    return tmpl
}
```

### 3.4 EvaluateCondition 使用 TemplateContext

```go
// pkg/dagexec/condition.go

// EvaluateCondition evaluates a Go Template condition expression.
// Uses the same TemplateContext as manifest rendering — no separate type needed.
// Returns true if the expression evaluates to "true" (case-insensitive, trimmed).
// Empty expression returns true (no filtering).
func EvaluateCondition(condition string, tmpl manifest.TemplateContext) (bool, error) {
    if strings.TrimSpace(condition) == "" {
        return true, nil
    }
    t, err := template.New("condition").Parse(condition)
    if err != nil {
        return false, fmt.Errorf("parse condition %q: %w", condition, err)
    }
    var buf bytes.Buffer
    if err := t.Execute(&buf, tmpl); err != nil {
        return false, fmt.Errorf("evaluate condition %q: %w", condition, err)
    }
    result := strings.TrimSpace(buf.String())
    return strings.EqualFold(result, "true"), nil
}
```

### 3.5 数据流

```
BKECluster (CR)
  │
  │ buildTemplateContext() 扩展
  ▼
TemplateContext {                          ← 单一模板数据结构 (manifest 渲染 + Condition 求值)
  ClusterName, Namespace, K8sVersion,     ← 现有 (manifest 渲染)
  OpenFuyaoVersion, DryRun
  Operation, ScaleType,                   ← 新增 (Condition 求值 + manifest 渲染)
  NodeCount, MasterCount, WorkerCount,
  DeployMode, Variables
}
  │
  │ 存入 ExecutionContext.TemplateContext
  ▼
ExecutionContext.TemplateContext
  │
  ├─ manifest 渲染: Store.GetComponentManifests(ctx, name, ver, tmpl)
  │  → 模板可引用 {{ .Operation }}, {{ .NodeCount }} 等
  │
  └─ Condition 求值: EvaluateCondition(cv.Spec.Condition, tmpl)
     → 表达式引用 {{ eq .ScaleType "master" }} 等
```

---

## 4. Scheduler 集成设计

在 `Scheduler.executeBatchParallel` 的现有跳过检查链中增加 Condition 检查，位于版本检查之后、执行器分发之前：

```go
// pkg/dagexec/scheduler.go — executeBatchParallel 现有跳过检查链

for _, compName := range batch {
    node, ok := dag.GetNode(compName)
    if !ok { continue }

    // (A) 已完成检查 (现有)
    if s.shouldSkipComponent(execCtx, node) { continue }

    // (B) 版本检查 (现有)
    if !s.componentNeedsUpgrade(execCtx, node) { continue }

    // (C) ★ Condition 检查 (新增)
    if !s.shouldExecuteByCondition(ctx, execCtx, node) { continue }

    // (D) 执行器分发 (现有)
    viaRegistry := s.usesRegistryExecutor(ctx, node)
    // ...
    items = append(items, workItem{...})
}
```

```go
// pkg/dagexec/scheduler.go — shouldExecuteByCondition 新增方法

// shouldExecuteByCondition evaluates the Condition field from ComponentVersion.
// Uses execCtx.TemplateContext (already built by buildTemplateContext) as
// template data — no separate ConditionContext needed.
func (s *Scheduler) shouldExecuteByCondition(
    ctx context.Context,
    execCtx *ExecutionContext,
    node *topology.ComponentNode,
) bool {
    if node == nil {
        return true
    }

    // 1. 从 CVStore 加载 ComponentVersion
    version := s.nodeVersionKey(node)
    cv, err := s.CVStore.GetComponentVersion(ctx, node.Name, version)
    if err != nil || cv == nil {
        // 无法加载 CV, 不阻塞执行 (向后兼容)
        return true
    }

    // 2. 获取 Condition 表达式 (单一数据源: ComponentVersion.Spec.Condition)
    condition := cv.Spec.Condition
    if condition == "" {
        return true // 无 Condition, 不过滤
    }

    // 3. 求值 — 直接使用 execCtx.TemplateContext (已由 buildTemplateContext 填充)
    shouldExecute, err := EvaluateCondition(condition, execCtx.TemplateContext)
    if err != nil {
        // 求值失败: 记录日志, 不阻塞执行 (安全默认)
        loggerFrom(execCtx).Warn("condition evaluation failed for %s: %v", node.Name, err)
        return true
    }

    if !shouldExecute {
        loggerFrom(execCtx).Info("component %s skipped by condition: %q", node.Name, condition)
    }
    return shouldExecute
}
```

---

## 5. 跳过链总览

DAG 执行时，每个组件按以下顺序检查，任一检查失败即跳过：

```
组件执行检查链 (executeBatchParallel)
  │
  ├─ (A) shouldSkipComponent
  │     DeclarativeUpgradeStatus.IsCompleted(name, version)
  │     → 组件已标记完成 → Skip
  │
  ├─ (B) componentNeedsUpgrade
  │     VersionContext.NeedsExecution(current, target)
  │     → Current == Target → Skip
  │
  ├─ (C) shouldExecuteByCondition ★ 新增
  │     EvaluateCondition(cv.Spec.Condition, execCtx.TemplateContext)
  │     → 表达式求值为 false → Skip
  │
  └─ (D) 执行器分发
        resolveComponentType → Registry / Legacy
        → ExecuteComponent
```

| 检查 | 依据 | 时机 | 跳过效果 |
|------|------|------|---------|
| (A) 已完成 | `DeclarativeUpgradeStatus` | 执行批次内 | 组件不执行，状态保持 Completed |
| (B) 版本 | `VersionContext` Current vs Target | 执行批次内 | 组件不执行，无状态变更 |
| (C) 条件 ★ | `ComponentVersion.Spec.Condition` 表达式 + `TemplateContext` | 执行批次内 | 组件不执行，无状态变更 |
| (D) 执行器 | `ComponentVersion.Spec.Type` | 执行器内 | 分发到 Inline/YAML/Helm/Binary Executor |

---

## 6. Condition 表达式语法

使用 Go Template (`text/template`) 语法，`TemplateContext` 作为模板数据：

| 表达式 | 含义 | 示例场景 |
|--------|------|---------|
| `{{ eq .Operation "scale" }}` | 仅扩容时执行 | 扩容专用组件 |
| `{{ eq .ScaleType "master" }}` | 仅 Master 扩容时执行 | Master 专用组件 |
| `{{ eq .Operation "install" }}` | 仅安装时执行 | 安装专用组件 |
| `{{ eq .Operation "upgrade" }}` | 仅升级时执行 | 升级专用组件 |
| `{{ gt .NodeCount 3 }}` | 节点数 > 3 时执行 | 大集群专用组件 |
| `{{ eq .DeployMode "offline" }}` | 离线部署时执行 | 离线专用组件 |
| `{{ and (eq .Operation "scale") (eq .ScaleType "worker") }}` | Worker 扩容时执行 | Worker 扩容专用 |
| `{{ or (eq .Operation "install") (eq .Operation "scale") }}` | 安装或扩容时执行 | 非升级场景 |
| (空) | 始终执行 | 默认行为 (向后兼容) |

**求值规则**：
- 表达式求值结果为字符串，`"true"`（不区分大小写）表示执行，其他值表示跳过
- 空表达式等同于不设置 Condition，始终执行
- 求值失败（模板解析/执行错误）时安全默认为执行，记录警告日志

---

## 7. ComponentVersion YAML 示例

```yaml
# ComponentVersion: bkeagent — 无 Condition (始终执行)
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v2.7.0
spec:
  name: bkeagent
  type: inline
  version: "v2.7.0"
  inline:
    handler: EnsureBKEAgent
    version: "v1.0.0"
  dependencies: []

---
# ComponentVersion: kubernetes-master — 仅 Master 扩容时执行
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-master-v2.7.0
spec:
  name: kubernetes-master
  type: inline
  version: "v2.7.0"
  inline:
    handler: EnsureMasterInit
    version: "v1.0.0"
  dependencies: [bkeagent]
  # ★ 仅 Master 扩容时执行, Worker 扩容时跳过
  condition: '{{ eq .ScaleType "master" }}'

---
# ComponentVersion: kubernetes-worker — 仅 Worker 扩容时执行
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-worker-v2.7.0
spec:
  name: kubernetes-worker
  type: inline
  version: "v2.7.0"
  inline:
    handler: EnsureWorkerJoin
    version: "v1.0.0"
  dependencies: [bkeagent]
  # ★ 仅 Worker 扩容时执行, Master 扩容时跳过
  condition: '{{ eq .ScaleType "worker" }}'

---
# ComponentVersion: coredns — 仅安装/扩容时执行, 升级时由独立升级组件处理
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.11.1
spec:
  name: coredns
  type: yaml
  version: "v1.11.1"
  yaml:
    applyStrategy: ServerSideApply
    healthCheck:
      enabled: true
      checks:
        - type: PodReady
          podReady:
            namespace: kube-system
            labelSelector: "k8s-app=kube-dns"
  dependencies: [kubernetes-master]
  # ★ 仅安装或扩容时执行, 升级时跳过 (升级由 coredns-upgrade 组件处理)
  condition: '{{ or (eq .Operation "install") (eq .Operation "scale") }}'
```

---

## 8. 扩容场景 Condition 过滤示例

以 3 节点集群（master-1, worker-1, worker-2）扩容新增 worker-3 为例：

```
TemplateContext (buildTemplateContext 填充):
  Operation = "scale"
  ScaleType = "worker"     ← Worker 扩容
  NodeCount = 4
  MasterCount = 1
  WorkerCount = 3

Scheduler 跳过检查链:
  bkeagent:
    (A) IsCompleted → false (未完成)
    (B) NeedsExecution → true (Current=="")
    (C) Condition: "" (空) → true ★ 执行
    → EnsureBKEAgent: 推送 bkeagent 到 worker-3

  kubernetes-master:
    (A) IsCompleted → false
    (B) NeedsExecution → true
    (C) Condition: '{{ eq .ScaleType "master" }}' → "false" (ScaleType=worker) → Skip ★
    → 跳过 (Worker 扩容不需要 Master 组件)

  kubernetes-worker:
    (A) IsCompleted → false
    (B) NeedsExecution → true
    (C) Condition: '{{ eq .ScaleType "worker" }}' → "true" → Execute ★
    → EnsureWorkerJoin: worker-3 加入集群

  coredns:
    (A) IsCompleted → false
    (B) NeedsExecution → true (Current=="" for node-scoped)
    (C) Condition: '{{ or (eq .Operation "install") (eq .Operation "scale") }}'
        → "true" (Operation=scale) → Execute ★
    → 部署 coredns (如果集群级组件未跳过)
    → 实际由 (A)/(B) 层过滤: Current==Target → Skip (集群级组件已安装)
```

---

## 9. 向后兼容性

| 场景 | Condition 字段 | 行为 | 说明 |
|------|---------------|------|------|
| 现有 ComponentVersion | 未设置 (空) | 始终执行 | 与当前行为完全一致 |
| 新增 Condition 字段 | 设置表达式 | 按表达式过滤 | 新增能力，不影响现有组件 |
| 表达式求值失败 | 解析/执行错误 | 安全默认执行 | 记录警告日志，不阻塞 |
| CVStore 不可用 | 无法加载 CV | 不阻塞执行 | 与当前 resolveComponentType 行为一致 |

**关键原则**：Condition 是**可选的**过滤层，不设置时行为与当前完全一致。Condition 的**唯一数据源**是 `ComponentVersion.Spec.Condition`，`ReleaseImageUpgradeComponent` 不携带 Condition（保持轻量引用角色）。

---

## 10. 与 KEP-17 Selector 的区别

| 维度 | KEP-17 Selector | 本设计 Condition |
|------|----------------|-----------------|
| **求值时机** | DAG 构建时 (build time) | DAG 执行时 (execution time) |
| **求值位置** | `expandSelectorComponents` → 子组件选择 | `shouldExecuteByCondition` → 组件跳过 |
| **影响范围** | 子组件是否进入 DAG | DAG 中的组件是否执行 |
| **数据来源** | TemplateContext (静态) | TemplateContext (动态, 含运行时状态) |
| **适用场景** | composite 子组件按条件选择 | 任何组件按运行时条件跳过 |
| **表达式语言** | Go Template | Go Template (相同) |
| **互补关系** | Selector 决定 DAG 结构 | Condition 决定 DAG 执行 |

两者**互补**：Selector 在构建时选择子组件进入 DAG，Condition 在执行时过滤 DAG 中的组件。同一组件可以同时使用两者。

---

## 11. 工作量评估

| 类别 | 模块 | 估算（人天） |
|------|------|------------|
| 开发 | `ComponentVersionSpec` / `SubComponent` 新增 Condition 字段 | 0.5 |
| 开发 | `TemplateContext` 扩展 + `buildTemplateContext` 扩展 | 1 |
| 开发 | `EvaluateCondition` 实现 | 0.5 |
| 开发 | `Scheduler.shouldExecuteByCondition` 集成 | 1 |
| 开发 | `inferOperation` / `inferScaleType` / `inferDeployMode` 辅助函数 | 1 |
| 测试 | Condition 求值单元测试 + Scheduler 集成测试 | 2 |
| **合计** | | **~6 人天** |

---

## 12. 风险与缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| Go Template 表达式注入 | 安全风险 | 低 | Condition 来自 ComponentVersion CRD（管理员可控），非用户输入；表达式仅访问 TemplateContext 字段，无系统调用 |
| 表达式求值性能 | DAG 执行延迟 | 低 | 每批次仅求值一次，模板编译结果可缓存；TemplateContext 构建开销可忽略 |
| TemplateContext 字段缺失 | 表达式无法引用 | 中 | 首版覆盖核心字段（Operation/ScaleType/NodeCount），Variables 字段预留扩展 |

---

## 附录

### A. 参考文档

1. [KEP-5 声明式升级框架](kep5/kep5.md)
2. [KEP-10 安装流程声明式设计](kep10-install-components-declarative-design.md)
3. [KEP-17 Selector 组件类型设计](kep17-selector-component-design.md)
4. [Go text/template 文档](https://pkg.go.dev/text/template)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **Condition** | ComponentVersion.Spec.Condition，Go Template 表达式，执行时求值决定组件是否执行 |
| **TemplateContext** | 集群状态安全投影，用于 manifest 渲染和 Condition 求值；扩展新增 Operation/ScaleType/NodeCount 等字段 |
| **shouldExecuteByCondition** | Scheduler 中 Condition 检查方法，使用 execCtx.TemplateContext 求值 |
| **EvaluateCondition** | Go Template 表达式求值函数，空表达式返回 true，求值结果 "true" 表示执行 |

---

**文档版本**: v1.0
**维护者**: openFuyao Team

# ComponentVersion
```
用户更新 ComponentVersion.Spec.Versions 中的目标版本
    → ComponentVersion 控制器检测版本变更
        → 执行 PreCheck
            → 执行 UpgradeAction
                → 执行 PostCheck
                    → 更新 ComponentVersion.Status
                        → 更新 ClusterVersion.Status 中的组件版本
```
==》上面有一个问题ComponentVersion定义了安装、升级相关的配置，修改ComponentVersion.Spec.Versions字段，会导致耦合及安装、升级配置部分也要修改，所以应该再设计一个ComponentVersionRef的对象，仅修改它，请进行分析
## 问题分析
### 当前设计的耦合问题
当前 ComponentVersion CRD 混合了两种不同关注点：
```
┌─────────────────────────────────────────────────────┐
│              ComponentVersion (当前设计)            │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │ 关注点1：组件能力定义（相对不可变）           │  │
│  │  spec.componentName: etcd                     │  │
│  │  spec.versions:                               │  │
│  │    - version: v3.5.11                         │  │
│  │      installAction: {...}    ← 安装动作定义   │  │
│  │      uninstallAction: {...} ← 卸载动作定义    │  │
│  │    - version: v3.5.12                         │  │
│  │      installAction: {...}                     │  │
│  │      upgradeFrom:                             │  │
│  │        - fromVersion: v3.5.11                 │  │
│  │          upgradeAction: {...} ← 升级动作定义  │  │
│  │      rollbackAction: {...}  ← 回滚动作定义    │  │
│  │      uninstallAction: {...}                   │  │
│  │  spec.healthCheck: {...}     ← 健康检查定义   │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │ 关注点2：运行时状态（频繁可变）               │  │
│  │  status.desiredVersion: v3.5.12  ← 目标版本   │  │
│  │  status.installedVersion: v3.5.11 ← 已装版本  │  │
│  │  status.phase: Upgrading         ← 当前阶段   │  │
│  │  status.nodeStatuses: {...}      ← 节点状态   │  │
│  └───────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```
**核心矛盾**：

| 操作 | 需要修改什么 | 问题 |
|------|-------------|------|
| 升级组件版本 | 修改 `status.desiredVersion` | 需要更新整个 ComponentVersion CR，可能误触 action 定义 |
| 新增版本支持 | 在 `spec.versions[]` 中添加新版本条目 | 需要更新整个 ComponentVersion CR，影响已有版本的 action 定义 |
| 回滚版本 | 修改 `status.desiredVersion` | 同上 |
| 修改安装脚本 | 修改 `spec.versions[].installAction` | 可能误触版本选择逻辑 |

**具体场景举例**：
```
场景：将 etcd 从 v3.5.11 升级到 v3.5.12

当前设计：
  1. ClusterVersion Controller 需要修改 ComponentVersion 的某个字段来触发升级
  2. 但 ComponentVersion.spec.versions[] 是"能力目录"，不应该被修改
  3. 如果在 status 中设置 desiredVersion，则"目标版本"和"能力定义"在同一个 CR 中
  4. 多个集群共享同一个 ComponentVersion 时，desiredVersion 应该不同 → 冲突

问题本质：
  - "组件能做什么"（能力定义）= 相对不可变，全局共享
  - "组件要做什么"（运行时意图）= 频繁可变，每集群独立
  - 两者耦合在同一个 CR 中，违反单一职责原则
```
## 解决方案：引入 ComponentVersionBinding
### 设计思路
将 ComponentVersion 拆分为两个 CRD：

| CRD | 职责 | 生命周期 | 修改频率 |
|-----|------|---------|---------|
| **ComponentVersion** | 组件能力目录（"能做什么"） | 随版本发布创建，相对不可变 | 低（新增版本时修改） |
| **ComponentVersionBinding** | 运行时绑定（"要做什么"） | 随集群创建，频繁可变 | 高（升级/扩缩容时修改） |
```
┌──────────────────────────────────────────────────────────────┐
│                   ComponentVersion                           │
│  (组件能力目录：定义组件各版本的安装/升级/卸载动作)          │
│  (相对不可变，可被多个集群共享)                              │
│                                                              │
│  spec.componentName: etcd                                    │
│  spec.scope: Node                                            │
│  spec.dependencies: [nodesEnv]                               │
│  spec.versions:                                              │
│    - version: v3.5.11                                        │
│      installAction: {...}                                    │
│      uninstallAction: {...}                                  │
│    - version: v3.5.12                                        │
│      installAction: {...}                                    │
│      upgradeFrom:                                            │
│        - fromVersion: v3.5.11                                │
│          upgradeAction: {...}                                │
│      rollbackAction: {...}                                   │
│      uninstallAction: {...}                                  │
│  spec.healthCheck: {...}                                     │
└──────────────────────────────────────────────────────────────┘
                           │
                           │ 被引用
                           ▼
┌──────────────────────────────────────────────────────────────┐
│               ComponentVersionBinding                         │
│  (运行时绑定：定义集群中组件的目标版本和运行状态)             │
│  (频繁可变，每集群独立)                                      │
│                                                              │
│  spec.componentVersionRef:                                   │
│    name: etcd-v3.5.12                                        │
│  spec.desiredVersion: v3.5.12     ← 仅修改此字段触发升级    │
│  spec.clusterRef:                                             │
│    name: my-cluster                                          │
│  spec.nodeSelector:                                          │
│    roles: [master]                                           │
│                                                              │
│  status.installedVersion: v3.5.11                            │
│  status.phase: Upgrading                                     │
│  status.nodeStatuses: {...}                                  │
│  status.lastOperation: {...}                                 │
│  status.conditions: [...]                                    │
└──────────────────────────────────────────────────────────────┘
```
### 资源关联关系（修正后）
```
BKECluster (1) ──→ ClusterVersion (1) ──→ ReleaseImage (1)
                                                │
                                    spec.components[]
                                                │
                                                ▼
                                    ComponentVersionBinding (N)  ← 运行时绑定，频繁可变
                                                │
                                    spec.componentVersionRef
                                                │
                                                ▼
                                    ComponentVersion (N)  ← 能力目录，相对不可变
                                                │
                                    spec.nodeSelector 匹配
                                                │
                                                ▼
                                    NodeConfig (M)
```
### ComponentVersionBinding CRD 定义
```go
// api/cvo/v1beta1/componentversionbinding_types.go

type ComponentVersionBindingSpec struct {
    ComponentVersionRef ComponentVersionReference `json:"componentVersionRef"`
    DesiredVersion      string                    `json:"desiredVersion"`
    ClusterRef          *ClusterReference         `json:"clusterRef,omitempty"`
    NodeSelector        *NodeSelector             `json:"nodeSelector,omitempty"`
    Pause               bool                      `json:"pause,omitempty"`
}

type ComponentVersionBindingStatus struct {
    InstalledVersion string                        `json:"installedVersion,omitempty"`
    Phase            ComponentPhase                `json:"phase,omitempty"`
    NodeStatuses     map[string]NodeComponentStatus `json:"nodeStatuses,omitempty"`
    LastOperation    *LastOperation                `json:"lastOperation,omitempty"`
    Conditions       []metav1.Condition            `json:"conditions,omitempty"`
}
```
### 修正后的 ComponentVersion CRD
ComponentVersion 只保留能力定义，移除运行时状态：
```go
// api/nodecomponent/v1alpha1/componentversion_types.go

type ComponentVersionSpec struct {
    ComponentName ComponentName            `json:"componentName"`
    Scope         ComponentScope           `json:"scope,omitempty"`
    Dependencies  []DependencySpec         `json:"dependencies,omitempty"`
    NodeSelector  *NodeSelector            `json:"nodeSelector,omitempty"`
    Versions      []ComponentVersionEntry  `json:"versions"`
}

// ComponentVersion 不再有 status 字段（或仅有验证状态）
type ComponentVersionStatus struct {
    Phase      ComponentVersionPhase `json:"phase,omitempty"`
    Conditions []metav1.Condition    `json:"conditions,omitempty"`
}

type ComponentVersionPhase string

const (
    ComponentVersionActive   ComponentVersionPhase = "Active"
    ComponentVersionDeprecated ComponentVersionPhase = "Deprecated"
)
```
### 升级流程（修正后）
```
用户修改 ClusterVersion.spec.desiredVersion = "v2.6.0"
    │
    ├── ClusterVersion Controller
    │   ├── 查找新 ReleaseImage → 解析新组件版本列表
    │   └── 按升级 DAG 逐步更新 ComponentVersionBinding.spec.desiredVersion
    │       （仅修改 desiredVersion 字段，不触碰 ComponentVersion）
    │
    ├── ComponentVersionBinding Controller（监听 desiredVersion 变化）
    │   ├── 检测 spec.desiredVersion != status.installedVersion
    │   ├── 通过 spec.componentVersionRef 找到 ComponentVersion
    │   ├── 从 ComponentVersion.spec.versions[] 中查找对应版本的 upgradeAction
    │   ├── 查找旧版本：
    │   │   └── ClusterVersion.status.currentReleaseRef
    │   │       → 旧 ReleaseImage
    │   │         → 旧 ComponentVersion
    │   │           → 旧版本 uninstallAction
    │   ├── 执行旧版本 uninstallAction（YAML 声明）
    │   ├── 执行新版本 upgradeAction（YAML 声明）
    │   └── 健康检查（YAML 声明）
    │
    └── 全部组件完成 → ClusterVersion 更新 currentVersion
```
### 对比分析
| 维度 | 旧设计（单 CRD） | 新设计（双 CRD） |
|------|-----------------|-----------------|
| **版本变更操作** | 修改 ComponentVersion 的 status/spec | 仅修改 ComponentVersionBinding.spec.desiredVersion |
| **升级触发** | 修改 ComponentVersion（含 action 定义） | 修改 Binding（不含 action 定义） |
| **多集群共享** | 不支持（desiredVersion 每集群不同） | 支持（ComponentVersion 共享，Binding 独立） |
| **误操作风险** | 高（可能误改 action 定义） | 低（Binding 不含 action 定义） |
| **关注点分离** | 差（能力+状态耦合） | 好（能力与状态分离） |
| **CRD 数量** | 4 个 | 5 个 |
| **Controller 数量** | 4 个 | 5 个 |
| **复杂度** | 低 | 中等增加 |
### 关键设计决策
| 决策 | 选择 | 原因 |
|------|------|------|
| Binding 是否独立 CRD | **是** | 运行时状态与能力定义的生命周期完全不同 |
| ComponentVersion 是否保留 status | **仅保留验证状态** | 运行时状态移到 Binding |
| 谁创建 Binding | ClusterVersion Controller | 升级编排由 ClusterVersion 驱动 |
| 谁修改 desiredVersion | ClusterVersion Controller | 版本变更由 ClusterVersion 编排 |
| 谁执行 action | ComponentVersionBinding Controller | 从 Binding 读取意图，从 ComponentVersion 读取动作 |
| 旧版本查找路径 | ClusterVersion.currentReleaseRef → 旧 ReleaseImage → 旧 ComponentVersion | 不变 |
### ComponentVersionBinding Controller Reconcile 核心逻辑
```go
func (r *ComponentVersionBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    binding := &cvo.ComponentVersionBinding{}
    if err := r.Get(ctx, req.NamespacedName, binding); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if !binding.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, binding)
    }

    cv := &cvo.ComponentVersion{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      binding.Spec.ComponentVersionRef.Name,
        Namespace: binding.Namespace,
    }, cv); err != nil {
        return ctrl.Result{}, fmt.Errorf("get ComponentVersion: %w", err)
    }

    desiredVersion := binding.Spec.DesiredVersion
    installedVersion := binding.Status.InstalledVersion

    switch binding.Status.Phase {
    case "", cvo.ComponentPending:
        return r.handlePending(ctx, binding, cv, desiredVersion)
    case cvo.ComponentInstalling:
        return r.handleInstalling(ctx, binding, cv, desiredVersion)
    case cvo.ComponentHealthy:
        if installedVersion != desiredVersion {
            return r.handleUpgrade(ctx, binding, cv, desiredVersion, installedVersion)
        }
        return r.handleHealthCheck(ctx, binding, cv)
    case cvo.ComponentUpgrading:
        return r.handleUpgrading(ctx, binding, cv, desiredVersion, installedVersion)
    case cvo.ComponentUpgradeFailed:
        return r.handleUpgradeFailed(ctx, binding, cv)
    case cvo.ComponentRollingBack:
        return r.handleRollingBack(ctx, binding, cv)
    case cvo.ComponentDegraded:
        return r.handleDegraded(ctx, binding, cv, desiredVersion)
    }
    return ctrl.Result{}, nil
}
```
### 修正后的资源关联关系总览
```
┌──────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  BKECluster  │────▶│  ClusterVersion  │────▶│  ReleaseImage    │
│  (集群实例)  │ 1:1 │  (集群版本)      │ 1:1 │  (发布清单)      │
└──────────────┘     │                  │     │  (不可变)        │
                     │  spec.releaseRef │     └────────┬─────────┘
                     │  status.current  │              │
                     │  ReleaseRef      │              │
                     └────────┬─────────┘              │
                              │                        │
                              │ 创建/更新              │ 引用组件版本
                              ▼                        │
                     ┌──────────────────────┐          │
                     │ ComponentVersion     │          │
                     │ Binding (N)          │          │
                     │ (运行时绑定)         │          │
                     │                      │          │
                     │ spec.desiredVersion  │          │
                     │ spec.componentVersion│          │
                     │        Ref ──────────┼──────────┤
                     │ status.installed     │          │
                     │ status.phase         │          │
                     │ status.nodeStatuses  │          │
                     └──────────────────────┘          │
                              │                        │
                              │ 引用                   │
                              ▼                        │
                     ┌──────────────────────┐          │
                     │ ComponentVersion (N) │◀─────────┘
                     │ (能力目录)           │
                     │ (相对不可变)         │
                     │                      │
                     │ spec.versions[]:     │
                     │   installAction      │
                     │   upgradeAction      │
                     │   uninstallAction    │
                     │   rollbackAction     │
                     │   healthCheck        │
                     └──────────────────────┘
                              │
                              │ nodeSelector 匹配
                              ▼
                     ┌──────────────────────┐
                     │ NodeConfig (M)       │
                     │ (节点组件清单)       │
                     └──────────────────────┘
```
### 总结
引入 ComponentVersionBinding 后：
1. **升级操作更安全**：只需修改 `Binding.spec.desiredVersion`，不会误触 action 定义
2. **关注点清晰分离**：ComponentVersion 定义"能力"，Binding 表达"意图"
3. **支持多集群共享**：同一个 ComponentVersion 可被多个 Binding 引用，每个 Binding 有独立的 desiredVersion
4. **生命周期独立**：ComponentVersion 随版本发布创建（低频），Binding 随集群创建（高频）
5. **代价可控**：仅增加 1 个 CRD + 1 个 Controller，复杂度增加有限

是否需要我将这个分析更新到 KEPU-2.md 文件中？
        

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

# NodeConfig.Spec.Components 填充方案
```
用户在 BKECluster.Spec 中添加新节点
    → BKEClusterReconciler 创建新 NodeConfig
        → NodeConfig 控制器检测到新节点
            → 根据 NodeConfig.Spec.Components 引用 ComponentVersion
                → ComponentVersion 控制器在新节点上执行 installAction
```
==>问题：BKEClusterReconciler 创建新 NodeConfig，里面的NodeConfig.Spec.Components如何填充？请进行分析
## NodeConfig.Spec.Components 填充方案分析
### 一、问题本质
```
用户在 BKECluster.Spec 中添加新节点
    → BKEClusterReconciler 创建新 NodeConfig
        → NodeConfig.Spec.Components = ???  ← 需要填充
```
**核心问题**：BKEClusterReconciler 创建 NodeConfig 时，需要知道"这个节点应该安装哪些组件、各组件的什么版本"，但 BKECluster.Spec 中只有节点基础信息（IP/Role/Hostname），**没有显式声明组件列表**。
### 二、数据来源分析
| 数据源 | 包含的信息 | 缺失的信息 |
|--------|-----------|-----------|
| **BKECluster.Spec** | 节点 IP/Role/Hostname、KubernetesVersion/EtcdVersion/ContainerdVersion/OpenFuyaoVersion | 组件列表、组件版本映射、ComponentVersion 引用 |
| **BKENode.Spec** | Role、IP、Hostname、ControlPlane/Kubelet 覆盖配置 | 组件列表 |
| **ReleaseImage.Spec.Components** | 完整组件列表 + 版本 + Scope + NodeSelector + ComponentVersionRef | 不知道具体节点该装哪些（需要按 Role 过滤） |
| **ClusterVersion.Status** | 当前版本、当前 ReleaseRef | 目标版本的组件列表 |
| **ComponentVersion CR** | 各组件的 installAction/upgradeAction | 不知道该装哪个版本 |

**关键洞察**：组件列表的"唯一权威来源"是 **ReleaseImage**，但需要通过 **节点角色（Role）** 过滤出该节点应安装的组件子集。
### 三、三种填充方案对比
#### 方案 A：BKEClusterReconciler 直接填充（创建时填充）
```
BKEClusterReconciler
    │
    ├── 读取 ClusterVersion → ReleaseImage
    ├── 读取新节点的 Role
    ├── 按 Role 过滤 ReleaseImage.Spec.Components
    ├── 组装 NodeConfig.Spec.Components
    └── 创建 NodeConfig（含完整 Components）
```
**优点**：
- 逻辑简单直接，创建即完整
- NodeConfig 自包含，不依赖其他 Controller 补充

**缺点**：
- BKEClusterReconciler 需要理解 ReleaseImage → ComponentVersion 的映射逻辑，**职责过重**
- 升级场景下，BKEClusterReconciler 需要同步更新所有 NodeConfig 的 Components 版本
- BKEClusterReconciler 与 CVO CRD 体系耦合
#### 方案 B：NodeConfig Controller 延迟填充（创建后填充）
```
BKEClusterReconciler
    │
    ├── 创建最小化 NodeConfig（仅含 nodeName、roles、nodeIP）
    └── 不填充 Components

NodeConfig Controller
    │
    ├── 检测到 NodeConfig.Spec.Components 为空
    ├── 读取 ClusterVersion → ReleaseImage
    ├── 按 Role 过滤 ReleaseImage.Spec.Components
    ├── 填充 NodeConfig.Spec.Components
    └── 更新 NodeConfig
```
**优点**：
- BKEClusterReconciler 职责单一，只负责创建 NodeConfig 骨架
- 组件填充逻辑集中在 NodeConfig Controller，符合声明式理念
- 升级场景下，NodeConfig Controller 可自动更新 Components 版本

**缺点**：
- NodeConfig 创建后需要一次额外的 Reconcile 才能填充 Components
- 需要区分"Components 为空=待填充"和"Components 为空=无需组件"
#### 方案 C：ClusterVersion Controller 统一填充（编排层填充）
```
BKEClusterReconciler
    │
    ├── 创建最小化 NodeConfig（仅含 nodeName、roles、nodeIP）
    └── 不填充 Components

ClusterVersion Controller
    │
    ├── 检测到新 NodeConfig（Watch NodeConfig）
    ├── 读取 ReleaseImage
    ├── 按 Role 过滤 Components
    ├── 填充 NodeConfig.Spec.Components
    └── 更新 NodeConfig
```
**优点**：
- ClusterVersion Controller 是版本编排的核心，组件列表由它统一管理
- 与升级流程一致：升级时也是 ClusterVersion Controller 更新 Components 版本

**缺点**：
- ClusterVersion Controller 职责过重
- NodeConfig 的 Components 填充和升级编排混在一起
### 四、推荐方案：方案 B（NodeConfig Controller 延迟填充）+ Role-Based Component Template
**推荐理由**：

| 维度 | 方案 A | 方案 B ✅ | 方案 C |
|------|--------|----------|--------|
| BKEClusterReconciler 职责 | 过重 | 单一 ✅ | 单一 ✅ |
| 填充逻辑归属 | 不合理 | NodeConfig Controller ✅ | ClusterVersion Controller |
| 升级场景适配 | 需额外逻辑 | 自动适配 ✅ | 自动适配 |
| 与声明式理念一致性 | 差 | 好 ✅ | 好 |
| 实现复杂度 | 低 | 中 ✅ | 中 |
### 五、详细设计
#### 5.1 核心概念：Role-Based Component Template
ReleaseImage 中每个组件都声明了 `nodeSelector.roles`，这实际上定义了一个 **角色→组件模板**：
```
ReleaseImage.Spec.Components:
  ┌───────────────────────────────────────────────────────────────────┐
  │ componentName: bkeAgent     scope: Node   roles: [master, worker] │
  │ componentName: nodesEnv     scope: Node   roles: [master, worker] │
  │ componentName: containerd   scope: Node   roles: [master, worker] │
  │ componentName: etcd         scope: Node   roles: [master]         │
  │ componentName: loadBalancer scope: Node   roles: [master]         │
  │ componentName: kubernetes   scope: Node   roles: [master, worker] │
  │ componentName: addon        scope: Cluster  (无 nodeSelector)     │
  │ componentName: openFuyao    scope: Cluster  (无 nodeSelector)     │
  └───────────────────────────────────────────────────────────────────┘

按 Role 过滤：
  master 节点 → [bkeAgent, nodesEnv, containerd, etcd, loadBalancer, kubernetes]
  worker 节点 → [bkeAgent, nodesEnv, containerd, kubernetes]

  Cluster 级组件 → 不在 NodeConfig 中，由 ComponentVersionBinding 直接管理
```
#### 5.2 NodeConfig 创建流程
```
用户在 BKECluster.Spec 中添加新节点
    │
    ▼
BKEClusterReconciler.reconcileWithClusterVersion()
    │
    ├── 创建最小化 NodeConfig（不含 Components）
    │   NodeConfig{
    │       Spec: NodeConfigSpec{
    │           NodeName:   "node-3",
    │           NodeIP:     "192.168.1.13",
    │           Roles:      [NodeRoleMaster],
    │           ClusterRef: &ClusterReference{Name: "my-cluster"},
    │           // Components: 不填充
    │       },
    │   }
    │
    └── 创建 Machine（通过 cluster-api）

NodeConfig Controller.Reconcile()
    │
    ├── 检测到 NodeConfig.Spec.Components 为空
    │   且 NodeConfig.Status.Phase == ""
    │
    ├── 调用 populateComponentsFromRelease()
    │   │
    │   ├── 1. 通过 NodeConfig.Spec.ClusterRef 找到 ClusterVersion
    │   ├── 2. 通过 ClusterVersion.Spec.ReleaseRef 找到 ReleaseImage
    │   ├── 3. 遍历 ReleaseImage.Spec.Components
    │   │   ├── 过滤条件1：scope == "Node"（Cluster 级组件不在 NodeConfig 中）
    │   │   ├── 过滤条件2：nodeSelector.roles 包含节点的任一 Role
    │   │   └── 匹配的组件加入 NodeConfig.Spec.Components
    │   ├── 4. 为每个组件设置：
    │   │   ├── componentName: 从 ReleaseImage.Spec.Components[].componentName
    │   │   ├── version: 从 ReleaseImage.Spec.Components[].version
    │   │   └── componentVersionRef: 从 ReleaseImage.Spec.Components[].componentVersionRef
    │   └── 5. 更新 NodeConfig
    │
    └── 继续正常 Reconcile 流程（handlePending → handleInstalling）
```
#### 5.3 populateComponentsFromRelease 核心实现
```go
func (r *NodeConfigReconciler) populateComponentsFromRelease(
    ctx context.Context,
    nc *cvo.NodeConfig,
) error {
    logger := ctrl.LoggerFrom(ctx)

    clusterRef := nc.Spec.ClusterRef
    if clusterRef == nil {
        return fmt.Errorf("NodeConfig %s has no clusterRef", nc.Name)
    }

    clusterVersion, err := r.findClusterVersion(ctx, clusterRef)
    if err != nil {
        return fmt.Errorf("find ClusterVersion: %w", err)
    }

    releaseRef := clusterVersion.Spec.ReleaseRef
    if clusterVersion.Status.CurrentReleaseRef != nil {
        releaseRef = *clusterVersion.Status.CurrentReleaseRef
    }

    releaseImage := &cvo.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      releaseRef.Name,
        Namespace: nc.Namespace,
    }, releaseImage); err != nil {
        return fmt.Errorf("get ReleaseImage %s: %w", releaseRef.Name, err)
    }

    var components []cvo.NodeComponent
    for _, comp := range releaseImage.Spec.Components {
        if !r.shouldInstallOnNode(comp, nc.Spec.Roles) {
            continue
        }

        components = append(components, cvo.NodeComponent{
            ComponentName: comp.ComponentName,
            Version:       comp.Version,
            ComponentVersionRef: comp.ComponentVersionRef,
        })
    }

    if len(components) == 0 {
        return fmt.Errorf("no components matched for node %s with roles %v",
            nc.Spec.NodeName, nc.Spec.Roles)
    }

    nc.Spec.Components = components
    logger.Info("populated NodeConfig components from ReleaseImage",
        "nodeName", nc.Spec.NodeName,
        "roles", nc.Spec.Roles,
        "componentCount", len(components),
        "releaseImage", releaseRef.Name,
        "releaseVersion", releaseImage.Spec.Version)

    return r.Update(ctx, nc)
}

func (r *NodeConfigReconciler) shouldInstallOnNode(
    comp cvo.ReleaseComponent,
    nodeRoles []cvo.NodeRole,
) bool {
    if comp.Scope == cvo.ScopeCluster {
        return false
    }

    if comp.NodeSelector == nil || len(comp.NodeSelector.Roles) == 0 {
        return true
    }

    for _, nodeRole := range nodeRoles {
        for _, selectorRole := range comp.NodeSelector.Roles {
            if nodeRole == selectorRole {
                return true
            }
        }
    }
    return false
}

func (r *NodeConfigReconciler) findClusterVersion(
    ctx context.Context,
    clusterRef *cvo.ClusterReference,
) (*cvo.ClusterVersion, error) {
    cvList := &cvo.ClusterVersionList{}
    if err := r.List(ctx, cvList,
        client.InNamespace(clusterRef.Namespace),
        client.MatchingLabels{"cluster.x-k8s.io/cluster-name": clusterRef.Name},
    ); err != nil {
        return nil, err
    }

    if len(cvList.Items) == 0 {
        return nil, fmt.Errorf("ClusterVersion not found for cluster %s", clusterRef.Name)
    }

    return &cvList.Items[0], nil
}
```
#### 5.4 升级场景：Components 版本同步
升级时，ClusterVersion Controller 更新 ReleaseRef 指向新 ReleaseImage，NodeConfig Controller 需要同步更新 Components 版本：
```go
func (r *NodeConfigReconciler) handleReady(
    ctx context.Context,
    nc *cvo.NodeConfig,
) (ctrl.Result, error) {
    logger := ctrl.LoggerFrom(ctx)

    needsVersionSync := r.checkComponentVersionDrift(ctx, nc)
    if needsVersionSync {
        logger.Info("component version drift detected, syncing from ReleaseImage",
            "nodeName", nc.Spec.NodeName)
        if err := r.syncComponentVersionsFromRelease(ctx, nc); err != nil {
            logger.Error(err, "failed to sync component versions")
            return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
        }
    }

    allHealthy := true
    needsUpgrade := false
    var upgradeComponents []string

    for _, comp := range nc.Spec.Components {
        cvName := r.resolveComponentVersionName(ctx, comp)
        cv := &cvo.ComponentVersion{}
        if err := r.Get(ctx, types.NamespacedName{
            Name:      cvName,
            Namespace: nc.Namespace,
        }, cv); err != nil {
            allHealthy = false
            continue
        }

        bindingList := &cvo.ComponentVersionBindingList{}
        if err := r.List(ctx, bindingList,
            client.InNamespace(nc.Namespace),
            client.MatchingLabels{
                "cvo.openfuyao.cn/component-name": string(comp.ComponentName),
                "cluster.x-k8s.io/cluster-name":   nc.Labels["cluster.x-k8s.io/cluster-name"],
            },
        ); err != nil {
            allHealthy = false
            continue
        }

        if len(bindingList.Items) == 0 {
            allHealthy = false
            continue
        }

        binding := bindingList.Items[0]
        nodeStatus, exists := binding.Status.NodeStatuses[nc.Spec.NodeName]
        if !exists {
            allHealthy = false
            continue
        }

        nc.Status.ComponentStatus[string(comp.ComponentName)] = cvo.NodeComponentDetailStatus{
            Phase:   nodeStatus.Phase,
            Version: nodeStatus.Version,
            Message: nodeStatus.Message,
        }

        if nodeStatus.Phase != cvo.ComponentHealthy && nodeStatus.Phase != cvo.ComponentInstalled {
            allHealthy = false
        }

        if comp.Version != nodeStatus.Version {
            needsUpgrade = true
            upgradeComponents = append(upgradeComponents, string(comp.ComponentName))
        }
    }

    if !allHealthy {
        nc.Status.Phase = cvo.NodeConfigNotReady
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
    }

    if needsUpgrade {
        nc.Status.Phase = cvo.NodeConfigUpgrading
        nc.Status.LastOperation = &cvo.LastOperation{
            Type:      cvo.OperationUpgrade,
            StartedAt: &metav1.Time{Time: time.Now()},
            Message:   fmt.Sprintf("upgrading components: %s", strings.Join(upgradeComponents, ",")),
        }
        _ = r.Status().Update(ctx, nc)
        return ctrl.Result{Requeue: true}, nil
    }

    _ = r.Status().Update(ctx, nc)
    return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *NodeConfigReconciler) checkComponentVersionDrift(
    ctx context.Context,
    nc *cvo.NodeConfig,
) bool {
    clusterRef := nc.Spec.ClusterRef
    if clusterRef == nil {
        return false
    }

    clusterVersion, err := r.findClusterVersion(ctx, clusterRef)
    if err != nil {
        return false
    }

    releaseRef := clusterVersion.Spec.ReleaseRef
    if clusterVersion.Status.CurrentReleaseRef != nil {
        releaseRef = *clusterVersion.Status.CurrentReleaseRef
    }

    releaseImage := &cvo.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      releaseRef.Name,
        Namespace: nc.Namespace,
    }, releaseImage); err != nil {
        return false
    }

    for _, comp := range nc.Spec.Components {
        for _, releaseComp := range releaseImage.Spec.Components {
            if comp.ComponentName == releaseComp.ComponentName {
                if comp.Version != releaseComp.Version {
                    return true
                }
            }
        }
    }

    return false
}

func (r *NodeConfigReconciler) syncComponentVersionsFromRelease(
    ctx context.Context,
    nc *cvo.NodeConfig,
) error {
    clusterRef := nc.Spec.ClusterRef
    clusterVersion, err := r.findClusterVersion(ctx, clusterRef)
    if err != nil {
        return err
    }

    releaseRef := clusterVersion.Spec.ReleaseRef
    if clusterVersion.Status.CurrentReleaseRef != nil {
        releaseRef = *clusterVersion.Status.CurrentReleaseRef
    }

    releaseImage := &cvo.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      releaseRef.Name,
        Namespace: nc.Namespace,
    }, releaseImage); err != nil {
        return err
    }

    for i, comp := range nc.Spec.Components {
        for _, releaseComp := range releaseImage.Spec.Components {
            if comp.ComponentName == releaseComp.ComponentName {
                nc.Spec.Components[i].Version = releaseComp.Version
                if releaseComp.ComponentVersionRef != nil {
                    nc.Spec.Components[i].ComponentVersionRef = releaseComp.ComponentVersionRef
                }
                break
            }
        }
    }

    return r.Update(ctx, nc)
}
```
#### 5.5 完整时序图
```
扩容流程（新增 Master 节点）：

用户: 修改 BKECluster.Spec，添加新节点 node-3 (role=master, ip=192.168.1.13)
    │
    ▼
BKEClusterReconciler
    │
    ├── 1. 创建最小化 NodeConfig
    │   NodeConfig{
    │       ObjectMeta: {Name: "my-cluster-node-3", Labels: {"cluster.x-k8s.io/cluster-name": "my-cluster"}},
    │       Spec: NodeConfigSpec{
    │           NodeName:   "node-3",
    │           NodeIP:     "192.168.1.13",
    │           Roles:      [master],
    │           ClusterRef: &ClusterReference{Name: "my-cluster", Namespace: "default"},
    │           Components: nil,  ← 待填充
    │       },
    │   }
    │
    └── 2. 增加 KubeadmControlPlane replicas +1

    │
    ▼
NodeConfig Controller: Reconcile
    │
    ├── 3. 检测 Components 为空 + Phase=""
    │   └── populateComponentsFromRelease()
    │       ├── 找到 ClusterVersion → ReleaseImage (v2.6.0)
    │       ├── 按 Role=master 过滤：
    │       │   bkeAgent     (scope=Node, roles=[master,worker]) ✅
    │       │   nodesEnv     (scope=Node, roles=[master,worker]) ✅
    │       │   containerd   (scope=Node, roles=[master,worker]) ✅
    │       │   etcd         (scope=Node, roles=[master])        ✅
    │       │   loadBalancer (scope=Node, roles=[master])        ✅
    │       │   kubernetes   (scope=Node, roles=[master,worker]) ✅
    │       │   addon        (scope=Cluster)                     ❌ 跳过
    │       │   openFuyao    (scope=Cluster)                     ❌ 跳过
    │       │   certs        (scope=Cluster)                     ❌ 跳过
    │       │   clusterAPI   (scope=Cluster)                     ❌ 跳过
    │       │   bkeProvider  (scope=Cluster)                     ❌ 跳过
    │       │   agentSwitch  (scope=Cluster)                     ❌ 跳过
    │       │   nodesPostProcess (scope=Node, roles=[master,worker]) ✅
    │       │
    │       └── 填充 NodeConfig.Spec.Components:
    │           [
    │             {componentName: bkeAgent,     version: v1.2.0, componentVersionRef: {Name: "bkeagent-v1.2.0"}},
    │             {componentName: nodesEnv,     version: v1.0.0, componentVersionRef: {Name: "nodesenv-v1.0.0"}},
    │             {componentName: containerd,   version: v1.7.2, componentVersionRef: {Name: "containerd-v1.7.2"}},
    │             {componentName: etcd,         version: v3.5.12, componentVersionRef: {Name: "etcd-v3.5.12"}},
    │             {componentName: loadBalancer, version: v1.0.0, componentVersionRef: {Name: "loadbalancer-v1.0.0"}},
    │             {componentName: kubernetes,   version: v1.29.0, componentVersionRef: {Name: "kubernetes-v1.29.0"}},
    │             {componentName: nodesPostProcess, version: v1.0.0, componentVersionRef: {Name: "nodespostprocess-v1.0.0"}},
    │           ]
    │
    ├── 4. Phase=Pending → handlePending()
    │   └── triggerComponentInstallForNode()
    │       └── 更新各 ComponentVersionBinding.nodeStatuses["node-3"] = Pending
    │
    └── 5. Phase=Installing → handleInstalling()
        └── 聚合各组件 nodeStatuses["node-3"] → 全部 Healthy → Phase=Ready
```
#### 5.6 关键设计决策总结
| 决策 | 选择 | 原因 |
|------|------|------|
| **谁创建 NodeConfig** | BKEClusterReconciler | 节点增删由 BKECluster 驱动 |
| **谁填充 Components** | NodeConfig Controller | 组件列表属于版本管理范畴，由 CVO 体系内部闭环 |
| **创建时 Components 状态** | 空（nil） | BKEClusterReconciler 不需要理解 ReleaseImage |
| **填充时机** | NodeConfig 首次 Reconcile | 延迟填充，减少 BKEClusterReconciler 职责 |
| **组件来源** | ReleaseImage.Spec.Components | 唯一权威来源，确保版本一致性 |
| **过滤规则** | scope=Node + nodeSelector.roles ∩ nodeRoles ≠ ∅ | 按角色匹配 |
| **Cluster 级组件** | 不在 NodeConfig 中 | 由 ComponentVersionBinding 直接管理 |
| **升级时版本同步** | NodeConfig Controller 检测漂移后自动同步 | 确保组件版本与 ReleaseImage 一致 |
| **区分"空=待填充"和"空=无组件"** | Phase="" + Components=nil = 待填充 | 初始状态明确 |
#### 5.7 边界情况处理
| 边界情况 | 处理方式 |
|---------|---------|
| **ReleaseImage 不存在** | NodeConfig 停留在 Pending，requeue 等待 |
| **ClusterVersion 不存在** | NodeConfig 停留在 Pending，requeue 等待 |
| **节点角色变更** | NodeConfig Controller 检测到 Role 变化，重新填充 Components |
| **ReleaseImage 中新增组件** | 升级时 syncComponentVersionsFromRelease 会添加新组件 |
| **ReleaseImage 中删除组件** | 升级时 syncComponentVersionsFromRelease 会移除旧组件 |
| **节点无匹配组件** | 返回错误，NodeConfig 停留在 Pending |
| **ComponentVersionRef 缺失** | 使用默认命名规则 `{componentName}-{version}` 推导 |

# 纳管现有集群
## 场景七：纳管现有集群 - 作用分析
### 一、业务场景
**纳管现有集群** 是指将一个**已经存在的 Kubernetes 集群**（非 openFuyao 创建）纳入 openFuyao 的管理体系，使其能够：

| 能力 | 说明 |
|------|------|
| **统一版本管理** | 后续可通过 ClusterVersion 进行版本升级 |
| **统一组件管理** | 后续可通过 ComponentVersion 管理组件生命周期 |
| **统一监控运维** | 接入 openFuyao 的监控、日志、告警体系 |
| **统一扩缩容** | 后续可通过 BKECluster 进行节点扩缩容 |
### 二、典型使用场景
```
场景 A：企业已有自建集群
┌─────────────────────────────────────────────────────────────┐
│ 企业通过 kubeadm 自建了 10+ 个 Kubernetes 集群              │
│                                                             │
│ 问题：                                                       │
│   - 版本不统一（1.24/1.25/1.26 混用）                       │
│   - 组件配置不统一（监控/日志/网络插件各异）                 │
│   - 升级困难（手动操作，风险高）                             │
│   - 运维成本高（每个集群独立维护）                           │
│                                                             │
│ 解决方案：                                                   │
│   → 使用 openFuyao 纳管这些集群                              │
│   → 统一版本管理、统一组件配置、统一运维流程                 │
└─────────────────────────────────────────────────────────────┘

场景 B：云厂商托管集群迁移
┌─────────────────────────────────────────────────────────────┐
│ 企业使用阿里云 ACK/腾讯云 TKS 托管集群                       │
│                                                             │
│ 问题：                                                       │
│   - 厂商锁定，迁移成本高                                     │
│   - 自定义能力受限（无法修改控制平面配置）                   │
│   - 成本高（托管服务溢价）                                   │
│                                                             │
│ 解决方案：                                                   │
│   → 纳管后逐步迁移到自建集群                                 │
│   → 保持业务连续性，平滑迁移                                 │
└─────────────────────────────────────────────────────────────┘

场景 C：多集群统一管理
┌─────────────────────────────────────────────────────────────┐
│ 企业有多个来源的集群（自建 + 托管 + 边缘集群）               │
│                                                             │
│ 问题：                                                       │
│   - 管理方式不统一                                          │
│   - 无法统一升级策略                                        │
│   - 无法统一安全策略                                        │
│                                                             │
│ 解决方案：                                                   │
│   → 全部纳管到 openFuyao                                    │
│   → 统一管理平面，统一运维流程                               │
└─────────────────────────────────────────────────────────────┘
```
### 三、纳管流程详解
```
用户创建 BKECluster（spec.manageMode=Import）
    │
    ▼
ClusterVersion 控制器创建 clusterManage ComponentVersionBinding
    │
    ▼
ComponentVersionBinding 控制器执行 installAction
    │
    ├── 1. 收集集群信息
    │   ├── Kubernetes 版本
    │   ├── 节点列表及角色
    │   ├── 已安装组件及版本
    │   ├── 网络配置
    │   └── 存储配置
    │
    ├── 2. 推送 Agent
    │   ├── 在每个节点上安装 bke-agent
    │   ├── 建立 Agent 与管理面的通信
    │   └── 验证 Agent 连通性
    │
    ├── 3. 伪引导
    │   ├── 创建 NodeConfig（基于现有节点信息）
    │   ├── 创建 ComponentVersionBinding（基于现有组件版本）
    │   ├── 标记组件为"已安装"状态（跳过实际安装）
    │   └── 同步集群状态到管理面
    │
    └── 4. 兼容性补丁
        ├── 检查集群是否符合 openFuyao 要求
        ├── 应用必要的配置补丁
        ├── 修复已知的兼容性问题
        └── 验证集群健康状态
    │
    ▼
纳管完成 → 集群纳入 openFuyao 管理体系
    │
    ├── 后续可通过 ClusterVersion 进行版本升级
    ├── 后续可通过 ComponentVersion 管理组件
    └── 后续可通过 BKECluster 进行扩缩容
```
### 四、关键设计点
| 设计点 | 说明 |
|--------|------|
| **非侵入式纳管** | 纳管过程不破坏现有集群，Agent 可随时卸载 |
| **伪引导** | 不重新安装组件，只同步状态，标记为"已安装" |
| **版本检测** | 自动检测现有组件版本，创建对应的 Binding |
| **兼容性检查** | 检查集群是否符合 openFuyao 的最低要求 |
| **回滚能力** | 纳管失败可回滚，不影响原集群 |
### 五、与全新安装的区别
| 维度 | 全新安装 | 纳管现有集群 |
|------|---------|-------------|
| **集群来源** | 从零创建 | 已存在 |
| **组件安装** | 全新安装所有组件 | 检测现有组件，伪引导 |
| **节点准备** | 需要准备节点 | 使用现有节点 |
| **风险** | 低（全新环境） | 中（需兼容现有配置） |
| **时间** | 较长（完整安装） | 较短（仅推送 Agent + 同步状态） |
| **可回滚** | 可删除重建 | 可卸载 Agent，恢复原状 |
### 六、总结
**场景七的作用**：将非 openFuyao 创建的 Kubernetes 集群纳入统一管理体系，实现：
1. **统一版本管理** - 后续可统一升级
2. **统一组件管理** - 后续可统一管理组件生命周期
3. **统一运维流程** - 降低多集群运维成本
4. **平滑迁移** - 从厂商锁定或自建集群平滑迁移到 openFuyao 体系

# 升级路径与组件版本兼容性        
newStr:     5. 更新 status.componentStatus
    6. 更新 status.phase
```

### 8.6 UpgradePath Controller
**核心职责**：升级路径的生命周期管理控制器，负责升级路径的验证、发现和状态维护。

**设计思路**：

| 要点 | 设计 |
|------|------|
| **路径验证** | validatePath() 验证 fromRelease/toRelease 对应的 ReleaseImage 是否存在 |
| **阻止检测** | checkBlocked() 检查路径是否被阻止，阻止时更新 status.phase=Blocked |
| **废弃检测** | checkDeprecated() 检查路径是否已废弃，废弃时更新 status.phase=Deprecated |
| **前置检查验证** | validatePreCheck() 验证 preCheck 中的 ActionSpec 是否合法 |
| **使用统计** | 更新 status.usedCount 和 status.lastUsedAt，供运维分析升级频率 |
| **路径发现** | 为 ClusterVersion Controller 提供 findUpgradePath() 方法 |
| **Finalizer** | 删除时检查是否有正在进行的升级，防止删除正在使用的升级路径 |

**升级路径状态机**：
```
                    创建 UpgradePath
                         │
                         ▼
                   ┌──────────┐
                   │ Validating│ ← 验证 fromRelease/toRelease 是否存在
                   └────┬─────┘
                        │
              ┌─────────┼──────────┐
              │         │          │
              ▼         ▼          ▼
        ┌─────────┐ ┌────────┐ ┌──────────┐
        │ Active  │ │Blocked │ │Deprecated│
        │ (可用)  │ │(被阻止)│ │(已废弃)  │
        └────┬────┘ └────────┘ └──────────┘
             │
             │ spec.blocked=true
             ├──────────────────────▶ Blocked
             │
             │ spec.deprecated=true
             ├──────────────────────▶ Deprecated
             │
             │ spec.blocked=false && spec.deprecated=false
             └──────────────────────▶ Active
```

**Reconcile 流程**：
```
Reconcile(ctx, req):
    1. 获取 UpgradePath 实例
    2. 处理 Finalizer：
       a. 如果 DeletionTimestamp != nil：
          - 检查是否有 ClusterVersion 正在使用此路径
          - 如果有，拒绝删除
          - 如果没有，移除 Finalizer
    3. 验证 fromRelease 对应的 ReleaseImage 是否存在
    4. 验证 toRelease 对应的 ReleaseImage 是否存在
    5. 验证 preCheck/postCheck 的 ActionSpec 是否合法
    6. 根据 spec.blocked/spec.deprecated 更新 status.phase
    7. 更新 status.conditions
```

**升级路径发现算法**：

ClusterVersion Controller 在升级时调用 findUpgradePath() 发现可用的升级路径：

```go
// FindUpgradePath 发现从当前版本到目标版本的升级路径
// 返回：升级路径、是否允许升级、阻止原因
func (r *UpgradePathHelper) FindUpgradePath(
    ctx context.Context,
    fromVersion string,
    toVersion string,
) (*cvov1beta1.UpgradePath, bool, string, error) {
    // 1. 精确查找：fromVersion → toVersion
    upgradePath, err := r.findExactPath(ctx, fromVersion, toVersion)
    if err != nil {
        return nil, false, "", err
    }

    // 2. 未找到升级路径：默认允许（无显式路径定义时允许升级）
    if upgradePath == nil {
        return nil, true, "", nil
    }

    // 3. 检查路径是否被阻止
    if upgradePath.Spec.Blocked {
        return upgradePath, false, upgradePath.Spec.BlockReason, nil
    }

    // 4. 检查路径是否已废弃（废弃不阻止，仅警告）
    if upgradePath.Spec.Deprecated {
        // 记录警告日志，但不阻止升级
        logger.Info("upgrade path is deprecated",
            "from", fromVersion, "to", toVersion,
            "message", upgradePath.Spec.DeprecationMessage)
    }

    return upgradePath, true, "", nil
}

// findExactPath 精确查找升级路径
func (r *UpgradePathHelper) findExactPath(
    ctx context.Context,
    fromVersion string,
    toVersion string,
) (*cvov1beta1.UpgradePath, error) {
    // 策略1：按命名约定查找
    pathName := fmt.Sprintf("%s-to-%s", fromVersion, toVersion)
    upgradePath := &cvov1beta1.UpgradePath{}
    if err := r.Get(ctx, types.NamespacedName{Name: pathName}, upgradePath); err == nil {
        return upgradePath, nil
    }

    // 策略2：按标签选择器查找
    pathList := &cvov1beta1.UpgradePathList{}
    if err := r.List(ctx, pathList,
        client.MatchingLabels{
            "cvo.openfuyao.cn/from-version": fromVersion,
            "cvo.openfuyao.cn/to-version":   toVersion,
        },
    ); err != nil {
        return nil, err
    }
    if len(pathList.Items) > 0 {
        return &pathList.Items[0], nil
    }

    // 未找到显式升级路径
    return nil, nil
}
```

**ClusterVersion Controller 集成升级路径**：

```go
// reconcileVersion 处理版本变更逻辑（修改后集成 UpgradePath）
func (r *ClusterVersionReconciler) reconcileVersion(
    ctx context.Context,
    cv *cvov1beta1.ClusterVersion,
) (ctrl.Result, error) {
    logger := log.FromContext(ctx)

    // 1. 检查是否需要升级
    if cv.Status.CurrentVersion == cv.Spec.DesiredVersion &&
        cv.Status.Phase == cvov1beta1.ClusterVersionPhaseReady {
        return ctrl.Result{}, nil
    }

    // 2. 验证升级路径
    upgradePath, allowed, blockReason, err := r.UpgradePathHelper.FindUpgradePath(
        ctx, cv.Status.CurrentVersion, cv.Spec.DesiredVersion)
    if err != nil {
        return ctrl.Result{}, err
    }
    if !allowed {
        cv.Status.Phase = cvov1beta1.ClusterVersionPhaseUpgradeBlocked
        conditions.MarkFalse(cv, cvov1beta1.ClusterVersionUpgradePathValid,
            "Blocked", "Upgrade path blocked: %s", blockReason)
        return ctrl.Result{}, nil
    }
    conditions.MarkTrue(cv, cvov1beta1.ClusterVersionUpgradePathValid,
        "Valid", "Upgrade path validated")

    // 3. 执行升级路径前置检查
    if upgradePath != nil && upgradePath.Spec.PreCheck != nil {
        if err := r.executeUpgradePathPreCheck(ctx, cv, upgradePath); err != nil {
            cv.Status.Phase = cvov1beta1.ClusterVersionPhasePreCheckFailed
            conditions.MarkFalse(cv, cvov1beta1.ClusterVersionPreCheckPassed,
                "Failed", "PreCheck failed: %v", err)
            return ctrl.Result{}, err
        }
        conditions.MarkTrue(cv, cvov1beta1.ClusterVersionPreCheckPassed,
            "Passed", "PreCheck passed")
    }

    // 4. 解析 ReleaseImage
    releaseImage, err := r.resolveReleaseImage(ctx, cv)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 5. 构建 DAG 并执行升级编排
    // ...（与之前相同）

    // 6. 升级完成后执行后置检查
    if upgradePath != nil && upgradePath.Spec.PostCheck != nil {
        if err := r.executeUpgradePathPostCheck(ctx, cv, upgradePath); err != nil {
            logger.Error(err, "PostCheck failed, but upgrade completed")
            // PostCheck 失败不回滚，仅记录警告
        }
    }

    // 7. 更新 UpgradePath 使用统计
    if upgradePath != nil {
        r.updateUpgradePathUsage(ctx, upgradePath)
    }

    return ctrl.Result{}, nil
}
```

**升级路径与组件兼容性检查的协作**：

```
用户修改 ClusterVersion.spec.desiredVersion
    │
    ├── 1. UpgradePath 检查（ClusterVersion Controller 调用）
    │   ├── 查找 UpgradePath CR
    │   ├── 检查是否 blocked
    │   └── 执行 preCheck（如有）
    │
    ├── 2. ComponentVersion 兼容性检查（ReleaseImage Controller 调用）
    │   ├── 遍历 ReleaseImage.spec.components
    │   ├── 从 ComponentVersion.spec.versions[].compatibility 读取约束
    │   └── 检查 compatibility.requires 是否满足
    │
    ├── 3. DAG 调度（ClusterVersion Controller 调用）
    │   ├── 计算组件升级顺序
    │   └── 按 DAG 顺序更新 ComponentVersionBinding.spec.desiredVersion
    │
    └── 4. 组件升级执行（ComponentVersionBinding Controller 调用）
        ├── 执行旧版本 uninstallAction
        ├── 执行新版本 upgradeAction
        └── 执行健康检查
```

**UpgradePath YAML 示例（完整）**：

```yaml
# 场景1：简单升级路径（无特殊检查）
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: v2.5.0-to-v2.6.0
  labels:
    cvo.openfuyao.cn/from-version: v2.5.0
    cvo.openfuyao.cn/to-version: v2.6.0
spec:
  fromRelease:
    name: openfuyao-v2.5.0
    version: v2.5.0
  toRelease:
    name: openfuyao-v2.6.0
    version: v2.6.0
---
# 场景2：带前置检查的升级路径
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: v2.5.0-to-v2.7.0
  labels:
    cvo.openfuyao.cn/from-version: v2.5.0
    cvo.openfuyao.cn/to-version: v2.7.0
spec:
  fromRelease:
    name: openfuyao-v2.5.0
    version: v2.5.0
  toRelease:
    name: openfuyao-v2.7.0
    version: v2.7.0
  preCheck:
    steps:
      - name: check-etcd-health
        type: Script
        script: |
          ETCDCTL_API=3 etcdctl endpoint health --cluster \
            --cacert=/etc/kubernetes/pki/etcd/ca.crt \
            --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
            --key=/etc/kubernetes/pki/etcd/healthcheck-client.key
      - name: check-disk-space
        type: Script
        script: |
          # 检查 /var/lib/etcd 磁盘空间
          AVAILABLE=$(df /var/lib/etcd --output=avail -BG | tail -1 | tr -d ' G')
          if [ "$AVAILABLE" -lt 10 ]; then
            echo "Insufficient disk space: ${AVAILABLE}GB < 10GB"
            exit 1
          fi
      - name: check-node-ready
        type: Kubectl
        kubectl:
          operation: Wait
          resource: nodes
          condition: Ready
          timeout: 300s
  postCheck:
    steps:
      - name: verify-all-nodes-ready
        type: Kubectl
        kubectl:
          operation: Wait
          resource: nodes
          condition: Ready
          timeout: 600s
---
# 场景3：被阻止的升级路径
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: v2.4.0-to-v2.7.0
  labels:
    cvo.openfuyao.cn/from-version: v2.4.0
    cvo.openfuyao.cn/to-version: v2.7.0
spec:
  fromRelease:
    name: openfuyao-v2.4.0
    version: v2.4.0
  toRelease:
    name: openfuyao-v2.7.0
    version: v2.7.0
  blocked: true
  blockReason: "跨大版本升级存在 etcd 数据迁移问题，请先升级到 v2.5.0 或 v2.6.0"
---
# 场景4：已废弃的升级路径
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: v2.3.0-to-v2.5.0
  labels:
    cvo.openfuyao.cn/from-version: v2.3.0
    cvo.openfuyao.cn/to-version: v2.5.0
spec:
  fromRelease:
    name: openfuyao-v2.3.0
    version: v2.3.0
  toRelease:
    name: openfuyao-v2.5.0
    version: v2.5.0
  deprecated: true
  deprecationMessage: "此升级路径已废弃，建议先升级到 v2.4.0 再升级到 v2.5.0"
```

**UpgradePath Controller 完整代码实现**：

```go
// controllers/cvo/upgradepath_controller.go

const (
    upgradePathFinalizer = "cvo.openfuyao.cn/upgradepath-protection"
)

type UpgradePathReconciler struct {
    client.Client
    Scheme *runtime.Scheme
}

func (r *UpgradePathReconciler) Reconcile(
    ctx context.Context,
    req ctrl.Request,
) (ctrl.Result, error) {
    logger := log.FromContext(ctx)

    upgradePath := &cvov1beta1.UpgradePath{}
    if err := r.Get(ctx, req.NamespacedName, upgradePath); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 处理删除
    if !upgradePath.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, upgradePath)
    }

    // 确保 Finalizer
    if !controllerutil.ContainsFinalizer(upgradePath, upgradePathFinalizer) {
        controllerutil.AddFinalizer(upgradePath, upgradePathFinalizer)
        if err := r.Update(ctx, upgradePath); err != nil {
            return ctrl.Result{}, err
        }
    }

    // 验证升级路径
    return r.reconcileNormal(ctx, upgradePath)
}

func (r *UpgradePathReconciler) reconcileNormal(
    ctx context.Context,
    up *cvov1beta1.UpgradePath,
) (ctrl.Result, error) {
    logger := log.FromContext(ctx)
    patchHelper, err := patch.NewHelper(up, r.Client)
    if err != nil {
        return ctrl.Result{}, err
    }
    defer func() {
        if err := patchHelper.Patch(ctx, up); err != nil {
            logger.Error(err, "failed to patch UpgradePath")
        }
    }()

    // 1. 验证 fromRelease 对应的 ReleaseImage 是否存在
    fromRelease := &cvov1beta1.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name: up.Spec.FromRelease.Name,
    }, fromRelease); err != nil {
        if apierrors.IsNotFound(err) {
            conditions.MarkFalse(up, cvov1beta1.UpgradePathFromReleaseExists,
                "NotFound", "Source ReleaseImage %s not found", up.Spec.FromRelease.Name)
            up.Status.Phase = cvov1beta1.UpgradePathPhaseBlocked
        }
        return ctrl.Result{}, err
    }
    conditions.MarkTrue(up, cvov1beta1.UpgradePathFromReleaseExists,
        "Found", "Source ReleaseImage exists")

    // 2. 验证 toRelease 对应的 ReleaseImage 是否存在
    toRelease := &cvov1beta1.ReleaseImage{}
    if err := r.Get(ctx, types.NamespacedName{
        Name: up.Spec.ToRelease.Name,
    }, toRelease); err != nil {
        if apierrors.IsNotFound(err) {
            conditions.MarkFalse(up, cvov1beta1.UpgradePathToReleaseExists,
                "NotFound", "Target ReleaseImage %s not found", up.Spec.ToRelease.Name)
            up.Status.Phase = cvov1beta1.UpgradePathPhaseBlocked
        }
        return ctrl.Result{}, err
    }
    conditions.MarkTrue(up, cvov1beta1.UpgradePathToReleaseExists,
        "Found", "Target ReleaseImage exists")

    // 3. 验证 preCheck/postCheck 的 ActionSpec
    if up.Spec.PreCheck != nil {
        if err := r.validateActionSpec(up.Spec.PreCheck); err != nil {
            conditions.MarkFalse(up, cvov1beta1.UpgradePathPreCheckValid,
                "Invalid", "PreCheck validation failed: %v", err)
        } else {
            conditions.MarkTrue(up, cvov1beta1.UpgradePathPreCheckValid,
                "Valid", "PreCheck validation passed")
        }
    }
    if up.Spec.PostCheck != nil {
        if err := r.validateActionSpec(up.Spec.PostCheck); err != nil {
            conditions.MarkFalse(up, cvov1beta1.UpgradePathPostCheckValid,
                "Invalid", "PostCheck validation failed: %v", err)
        } else {
            conditions.MarkTrue(up, cvov1beta1.UpgradePathPostCheckValid,
                "Valid", "PostCheck validation passed")
        }
    }

    // 4. 根据 spec.blocked/spec.deprecated 更新 status.phase
    switch {
    case up.Spec.Blocked:
        up.Status.Phase = cvov1beta1.UpgradePathPhaseBlocked
    case up.Spec.Deprecated:
        up.Status.Phase = cvov1beta1.UpgradePathPhaseDeprecated
    default:
        up.Status.Phase = cvov1beta1.UpgradePathPhaseActive
    }

    return ctrl.Result{}, nil
}

func (r *UpgradePathReconciler) reconcileDelete(
    ctx context.Context,
    up *cvov1beta1.UpgradePath,
) (ctrl.Result, error) {
    logger := log.FromContext(ctx)

    if !controllerutil.ContainsFinalizer(up, upgradePathFinalizer) {
        return ctrl.Result{}, nil
    }

    // 检查是否有 ClusterVersion 正在使用此路径
    cvList := &cvov1beta1.ClusterVersionList{}
    if err := r.List(ctx, cvList); err != nil {
        return ctrl.Result{}, err
    }
    for _, cv := range cvList.Items {
        if cv.Status.Phase == cvov1beta1.ClusterVersionPhaseUpgrading &&
            cv.Status.CurrentVersion == up.Spec.FromRelease.Version &&
            cv.Spec.DesiredVersion == up.Spec.ToRelease.Version {
            logger.Info("cannot delete UpgradePath: ClusterVersion is using it",
                "clusterVersion", cv.Name)
            return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
        }
    }

    controllerutil.RemoveFinalizer(up, upgradePathFinalizer)
    if err := r.Update(ctx, up); err != nil {
        return ctrl.Result{}, err
    }

    return ctrl.Result{}, nil
}

func (r *UpgradePathReconciler) validateActionSpec(spec *cvov1beta1.ActionSpec) error {
    for _, step := range spec.Steps {
        switch step.Type {
        case cvov1beta1.ActionScript:
            if step.Script == "" && step.ScriptSource == nil {
                return fmt.Errorf("step %s: script or scriptSource must be specified", step.Name)
            }
        case cvov1beta1.ActionManifest:
            if step.Manifest == "" && step.ManifestSource == nil {
                return fmt.Errorf("step %s: manifest or manifestSource must be specified", step.Name)
            }
        case cvov1beta1.ActionChart:
            if step.Chart == nil {
                return fmt.Errorf("step %s: chart must be specified", step.Name)
            }
        case cvov1beta1.ActionKubectl:
            if step.Kubectl == nil {
                return fmt.Errorf("step %s: kubectl must be specified", step.Name)
            }
        default:
            return fmt.Errorf("step %s: unknown action type %s", step.Name, step.Type)
        }
    }
    return nil
}

func (r *UpgradePathReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&cvov1beta1.UpgradePath{}).
        Complete(r)
}
```

**UpgradePathHelper 辅助类**：

```go
// pkg/cvo/upgradepath_helper.go

type UpgradePathHelper struct {
    client.Client
}

func NewUpgradePathHelper(c client.Client) *UpgradePathHelper {
    return &UpgradePathHelper{Client: c}
}

// FindUpgradePath 发现从当前版本到目标版本的升级路径
func (h *UpgradePathHelper) FindUpgradePath(
    ctx context.Context,
    fromVersion string,
    toVersion string,
) (*cvov1beta1.UpgradePath, bool, string, error) {
    logger := log.FromContext(ctx)

    // 1. 精确查找
    upgradePath, err := h.findExactPath(ctx, fromVersion, toVersion)
    if err != nil {
        return nil, false, "", err
    }

    // 2. 未找到：默认允许
    if upgradePath == nil {
        logger.Info("no explicit upgrade path found, allowing by default",
            "from", fromVersion, "to", toVersion)
        return nil, true, "", nil
    }

    // 3. 检查是否被阻止
    if upgradePath.Spec.Blocked {
        logger.Info("upgrade path is blocked",
            "from", fromVersion, "to", toVersion,
            "reason", upgradePath.Spec.BlockReason)
        return upgradePath, false, upgradePath.Spec.BlockReason, nil
    }

    // 4. 检查是否已废弃（仅警告，不阻止）
    if upgradePath.Spec.Deprecated {
        logger.Info("upgrade path is deprecated",
            "from", fromVersion, "to", toVersion,
            "message", upgradePath.Spec.DeprecationMessage)
    }

    return upgradePath, true, "", nil
}

// FindRecommendedPath 查找推荐的升级路径（跳过被阻止和已废弃的）
func (h *UpgradePathHelper) FindRecommendedPath(
    ctx context.Context,
    fromVersion string,
    toVersion string,
) (*cvov1beta1.UpgradePath, error) {
    pathList := &cvov1beta1.UpgradePathList{}
    if err := h.List(ctx, pathList,
        client.MatchingLabels{
            "cvo.openfuyao.cn/from-version": fromVersion,
            "cvo.openfuyao.cn/to-version":   toVersion,
        },
    ); err != nil {
        return nil, err
    }

    for i := range pathList.Items {
        up := &pathList.Items[i]
        if up.Status.Phase == cvov1beta1.UpgradePathPhaseActive {
            return up, nil
        }
    }
    return nil, nil
}

// GetAllBlockedPaths 获取所有被阻止的升级路径（供运维查看）
func (h *UpgradePathHelper) GetAllBlockedPaths(
    ctx context.Context,
) ([]cvov1beta1.UpgradePath, error) {
    pathList := &cvov1beta1.UpgradePathList{}
    if err := h.List(ctx, pathList); err != nil {
        return nil, err
    }

    var blocked []cvov1beta1.UpgradePath
    for _, up := range pathList.Items {
        if up.Spec.Blocked {
            blocked = append(blocked, up)
        }
    }
    return blocked, nil
}

// RecordUsage 记录升级路径使用情况
func (h *UpgradePathHelper) RecordUsage(
    ctx context.Context,
    upgradePath *cvov1beta1.UpgradePath,
) error {
    patchHelper, err := patch.NewHelper(upgradePath, h.Client)
    if err != nil {
        return err
    }

    upgradePath.Status.UsedCount++
    now := metav1.Now()
    upgradePath.Status.LastUsedAt = &now

    return patchHelper.Patch(ctx, upgradePath)
}

func (h *UpgradePathHelper) findExactPath(
    ctx context.Context,
    fromVersion string,
    toVersion string,
) (*cvov1beta1.UpgradePath, error) {
    pathName := fmt.Sprintf("%s-to-%s", fromVersion, toVersion)
    upgradePath := &cvov1beta1.UpgradePath{}
    if err := h.Get(ctx, types.NamespacedName{Name: pathName}, upgradePath); err == nil {
        return upgradePath, nil
    }

    pathList := &cvov1beta1.UpgradePathList{}
    if err := h.List(ctx, pathList,
        client.MatchingLabels{
            "cvo.openfuyao.cn/from-version": fromVersion,
            "cvo.openfuyao.cn/to-version":   toVersion,
        },
    ); err != nil {
        return nil, err
    }
    if len(pathList.Items) > 0 {
        return &pathList.Items[0], nil
    }

    return nil, nil
}
```

**升级路径与各控制器的交互时序**：

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     ClusterVersion Controller                               │
│                                                                             │
│  reconcileVersion():                                                        │
│    1. UpgradePathHelper.FindUpgradePath(current, desired)                   │
│       ├── 找到 UpgradePath CR → 检查 blocked/deprecated                    │
│       └── 未找到 → 默认允许                                                 │
│    2. 执行 UpgradePath.Spec.PreCheck（如有）                                │
│    3. 解析 ReleaseImage → 构建 DAG → 更新 Binding.desiredVersion           │
│    4. 等待所有 ComponentVersionBinding 完成                                 │
│    5. 执行 UpgradePath.Spec.PostCheck（如有）                               │
│    6. UpgradePathHelper.RecordUsage()                                       │
└─────────────────────────────────────────────────────────────────────────────┘
        │                    │                         │
        ▼                    ▼                         ▼
┌──────────────┐  ┌──────────────────┐  ┌──────────────────────────────┐
│ UpgradePath  │  │  ReleaseImage    │  │ ComponentVersionBinding      │
│ Controller   │  │  Controller      │  │ Controller                   │
│              │  │                  │  │                              │
│ 验证路径     │  │ 验证兼容性       │  │ 执行组件升级                 │
│ 更新状态     │  │ (从 CV 读取)     │  │ (从 CV 读取 action)          │
└──────────────┘  └──────────────────┘  └──────────────────────────────┘
```
              
oldStr:     5. 更新 status.componentStatus
    6. 更新 status.phase
```
            

newStr:     7. 如果版本不同：
       a. 验证升级路径（从 UpgradePath CRD 查找）
       b. 执行升级路径前置检查（如有）
       c. 计算需要变更的组件列表
       d. 按 DAG 顺序更新 ComponentVersionBinding.spec.desiredVersion
       e. 更新 status.upgradeSteps
       f. 监控各 ComponentVersionBinding 状态
              
oldStr:     7. 如果版本不同：
       a. 验证升级路径（从 ReleaseImage.spec.upgradePaths）
       b. 计算需要变更的组件列表
       c. 按 DAG 顺序更新 ComponentVersionBinding.spec.desiredVersion
       d. 更新 status.upgradeSteps
       e. 监控各 ComponentVersionBinding 状态
            
          
同样更新 ReleaseImage Controller 中的升级路径验证步骤：
        
    
newStr:     4. 验证升级路径合法性（从 UpgradePath CRD 读取）：
       a. 查找 fromRelease → toRelease 的 UpgradePath CR
       b. 如果 UpgradePath.spec.blocked == true，拒绝升级
       c. 如果 UpgradePath.spec.preCheck 存在，记录前置检查
              
oldStr:     4. 验证升级路径合法性（从 spec.upgradePaths）
            
          
所有修改已完成。请 **Review and Accept** 以应用所有修改。

## UpgradePath Controller 设计总结
### 一、核心设计思路
UpgradePath Controller 的设计遵循 **关注点分离** 原则，将升级路径管理从 ReleaseImage 中解耦：
```
旧设计：ReleaseImage.spec.upgradePaths[]（不可变，耦合）
    ↓
新设计：UpgradePath CRD（独立，可演进）
```
### 二、控制器职责
| 职责 | 方法 | 说明 |
|------|------|------|
| **路径验证** | `reconcileNormal()` | 验证 fromRelease/toRelease 的 ReleaseImage 是否存在 |
| **阻止检测** | `checkBlocked()` | 检查 spec.blocked，更新 status.phase=Blocked |
| **废弃检测** | `checkDeprecated()` | 检查 spec.deprecated，更新 status.phase=Deprecated |
| **前置检查验证** | `validateActionSpec()` | 验证 preCheck/postCheck 的 ActionSpec 是否合法 |
| **使用统计** | `RecordUsage()` | 更新 usedCount 和 lastUsedAt |
| **路径发现** | `FindUpgradePath()` | 为 ClusterVersion Controller 提供路径查找服务 |
| **Finalizer 保护** | `reconcileDelete()` | 防止删除正在使用的升级路径 |
### 三、升级路径状态机
```
Validating → Active / Blocked / Deprecated

Active:
  - spec.blocked=false, spec.deprecated=false
  - 可正常使用

Blocked:
  - spec.blocked=true
  - 阻止升级，返回 blockReason

Deprecated:
  - spec.deprecated=true
  - 仅警告，不阻止升级
```
### 四、升级路径发现策略
```
1. 精确查找：按命名约定 "v2.5.0-to-v2.6.0"
2. 标签查找：按标签 cvo.openfuyao.cn/from-version + cvo.openfuyao.cn/to-version
3. 默认允许：未找到显式路径时，默认允许升级
```
### 五、与 ClusterVersion Controller 的集成
```
ClusterVersion.reconcileVersion():
  1. UpgradePathHelper.FindUpgradePath(current, desired)
  2. 执行 UpgradePath.Spec.PreCheck（如有）
  3. 解析 ReleaseImage → 构建 DAG → 更新 Binding.desiredVersion
  4. 等待所有 ComponentVersionBinding 完成
  5. 执行 UpgradePath.Spec.PostCheck（如有）
  6. UpgradePathHelper.RecordUsage()
```
### 六、独立演进能力
| 场景 | 操作 | 是否需要重新发布 ReleaseImage |
|------|------|:--:|
| 添加升级前置检查 | 更新 UpgradePath.spec.preCheck | ❌ |
| 阻止有问题的升级路径 | 设置 UpgradePath.spec.blocked=true | ❌ |
| 废弃旧升级路径 | 设置 UpgradePath.spec.deprecated=true | ❌ |
| 添加新升级路径 | 创建新 UpgradePath CR | ❌ |
| 添加升级后置检查 | 更新 UpgradePath.spec.postCheck | ❌ |
        

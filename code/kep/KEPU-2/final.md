# KEPU-2: 基于 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 的声明式集群版本管理
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-2 |
| **标题** | 声明式集群版本管理：YAML 配置驱动的安装、升级与扩缩容 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **依赖** | KEPU-1（整体架构重构） |
## 1. 摘要
本提案设计基于五个 CRD（ClusterVersion、ReleaseImage、ComponentVersion、ComponentVersionBinding、NodeConfig）的声明式集群版本管理方案。

**核心变化**：各 Phase 的安装、升级、卸载逻辑全部通过 YAML 配置声明，由通用 ActionEngine 解释执行，**不再为每个组件编写 Go 代码实现**。

**设计原则**：
- **配置即代码**：组件生命周期（安装/升级/卸载/健康检查）全部声明在 ComponentVersion YAML 中
- **通用引擎**：ActionEngine 是唯一的执行器，解释 YAML 中的 Action 定义并执行
- **零组件代码**：不编写组件特定的 Go Executor，所有行为由 YAML 驱动
- **模板化**：脚本和 manifest 支持模板变量（`{{.Version}}`、`{{.NodeIP}}` 等），运行时渲染
- **关注点分离**：ComponentVersion 定义"能力"（能做什么），ComponentVersionBinding 表达"意图"（要做什么）
## 2. 动机
### 2.1 现有架构问题
| 问题 | 现状 | 影响 |
|------|------|------|
| **版本概念缺失** | BKECluster 仅记录 KubernetesVersion/EtcdVersion/ContainerdVersion/OpenFuyaoVersion | 无整体版本概念，无法回答"集群当前是什么版本" |
| **发布清单缺失** | 组件版本散落在 BKECluster Spec 各字段 | 无法追溯某个版本包含哪些组件及版本 |
| **命令式编排** | 各 Phase 通过 `NeedExecute` + 固定顺序执行 | 无法并行、无法跳过、无法回滚 |
| **组件独立演进受限** | Phase 之间硬编码依赖，升级路径固定 | 无法独立升级单个组件，无法 A/B 测试 |
| **安装逻辑与编排耦合** | Phase 代码既包含编排逻辑又包含安装逻辑 | 无法复用安装逻辑，无法独立测试 |
| **扩缩容与升级耦合** | EnsureWorkerDelete/EnsureMasterDelete 与升级 Phase 混在一起 | 缩容逻辑无法独立演进 |
| **升级卸载旧组件缺失** | 升级时直接覆盖安装，无旧版本卸载流程 | 旧版本残留文件/配置可能导致冲突 |
### 2.2 OpenShift CVO 的启发
OpenShift 的 Cluster Version Operator（CVO）采用以下架构：
```
ClusterVersion (集群版本)
    └── desiredUpdate.release (Release Image 引用)
            └── Release Payload (容器镜像)
                    └── release-manifests/ (组件manifest清单)
```
**借鉴点**：
1. ClusterVersion 作为集群版本的全局入口
2. ReleaseImage 作为版本清单的载体（不可变）
3. 组件 manifest 声明式定义，CVO 自动编排
4. 升级路径显式声明，支持兼容性检查

**差异点**：
1. OpenShift 使用容器镜像作为 Release Payload，我们使用 ReleaseImage CRD
2. OpenShift 的 manifest 是原生 Kubernetes 资源，我们需要支持 Script/Manifest/Helm/Controller 四种执行方式
3. OpenShift 的组件是 Operator，我们的组件包括节点级和集群级两种
4. 我们需要支持升级时先卸载旧组件再安装新组件的流程
## 3. 目标
### 3.1 主要目标
1. 定义 ClusterVersion、ReleaseImage、ComponentVersion、ComponentVersionBinding、NodeConfig 五个 CRD 及其关联关系
2. 实现 ClusterVersion 控制器：管理集群版本生命周期（安装→升级→回滚），编排 ComponentVersionBinding
3. 实现 ReleaseImage 控制器：验证发布清单，生成 ComponentVersion 列表
4. 实现 ComponentVersion 控制器：定义组件能力目录（installAction/upgradeAction/uninstallAction）
5. 实现 ComponentVersionBinding 控制器：执行组件生命周期操作（安装/升级/卸载/健康检查）
6. 实现 NodeConfig 控制器：管理节点组件的安装/升级/卸载，触发 ComponentVersionBinding 节点级操作
7. 将现有 PhaseFrame 各 Phase 重构为 ComponentVersion 声明式架构
8. 实现升级时先卸载旧组件再安装新组件的完整流程
### 3.2 非目标
1. 不实现 OpenShift 式的 Release Image 容器镜像载体（使用 CRD 替代）
2. 不实现多集群版本管理（仅单集群）
3. 不实现 OS 级别的版本管理（由 OSProvider 独立负责）
4. 不实现版本包的构建与发布流程（由 CI/CD 独立负责）
5. 不修改现有 BKECluster CRD 的 Spec 定义
## 4. 范围
### 4.1 在范围内
| 范围 | 说明 |
|------|------|
| CRD 定义与注册 | 五个核心 CRD 的 API 定义 |
| 控制器实现 | 五个控制器的 Reconcile 逻辑 |
| ActionEngine | 通用执行引擎，解释执行 YAML 中的 Action 定义 |
| Phase→ComponentVersion 迁移 | 20+ 个 Phase 到 ComponentVersion 的映射与迁移 |
| DAG 调度 | 组件依赖图与调度算法 |
| 版本升级流程 | PreCheck→UninstallOld→Upgrade→PostCheck→Rollback |
| 扩缩容流程 | NodeConfig 增删触发组件安装/卸载 |
| 关注点分离 | ComponentVersion（能力定义）与 ComponentVersionBinding（运行时意图）分离 |
### 4.2 不在范围内
| 范围 | 原因 |
|------|------|
| 版本包构建 | CI/CD 流程独立 |
| 多集群管理 | 超出单集群版本管理范围 |
| OS 版本管理 | 由 OSProvider 独立负责 |
| UI/CLI 交互 | 仅定义 API，不涉及前端 |
## 5. 约束
| 约束 | 说明 |
|------|------|
| **向后兼容** | 必须支持从现有 PhaseFrame 平滑迁移，不能破坏现有集群 |
| **Feature Gate** | 新架构通过 Feature Gate 开关控制，默认关闭 |
| **单集群单 ClusterVersion** | 每个集群仅允许一个 ClusterVersion 实例 |
| **1:1 ReleaseImage** | 每个 ClusterVersion 仅关联一个 ReleaseImage |
| **ReleaseImage 不可变** | 创建后不可修改，确保版本清单一致性 |
| **组件不可降级** | 默认不允许组件版本降级，除非显式设置 allowDowngrade |
| **离线环境** | 必须支持离线环境，所有资源通过 CRD 定义 |
| **性能** | 控制器 Reconcile 周期不超过 30s，升级单节点不超过 10min |
| **ActionEngine 唯一执行路径** | 不绕过引擎直接操作 |
## 6. 场景
### 6.1 场景一：全新集群安装
```
用户创建 BKECluster
    → BKEClusterReconciler 创建 ClusterVersion（引用 ReleaseImage）
        → ClusterVersion 控制器解析 ReleaseImage
            → 为每个组件创建 ComponentVersionBinding（spec.desiredVersion = 组件版本）
            → DAGScheduler 计算安装顺序，按序更新 Binding
                → ComponentVersionBinding 控制器检测 desiredVersion != installedVersion
                    → 通过 componentVersionRef 找到 ComponentVersion
                    → 从 ComponentVersion.versions[] 中查找对应版本的 installAction
                    → 执行 installAction（YAML 声明）
                    → 健康检查（YAML 声明）
                → Node 级组件：NodeConfig 控制器在对应节点上触发安装
                → 全部组件完成 → ClusterVersion 更新 currentVersion
```
### 6.2 场景二：集群版本升级（含旧组件卸载）
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
### 6.3 场景三：单组件独立升级
```
用户修改 ComponentVersionBinding.spec.desiredVersion
    → ComponentVersionBinding 控制器检测 desiredVersion != installedVersion
        → 通过 componentVersionRef 找到 ComponentVersion
        → 从 ComponentVersion.spec.versions[] 中查找目标版本的 upgradeAction
        → 执行 PreCheck（YAML 声明）
        → 查找旧版本 uninstallAction（通过 ClusterVersion.currentReleaseRef → 旧 ReleaseImage → 旧 ComponentVersion）
        → 执行旧版本 uninstallAction（YAML 声明）
        → 执行新版本 upgradeAction（YAML 声明）
        → 执行 PostCheck / 健康检查（YAML 声明）
        → 更新 ComponentVersionBinding.status.installedVersion
            → ClusterVersion 控制器检测到 Binding 状态变更，更新 ClusterVersion.Status 中的组件版本
```
### 6.4 场景四：节点扩容
```
用户在 BKECluster.Spec 中添加新节点
    → BKEClusterReconciler 创建最小化 NodeConfig（不含 Components）
        → BKEClusterReconciler 触发 cluster-api 创建 Machine 资源
            ├── Master 节点：增加 KubeadmControlPlane replicas
            └── Worker 节点：增加 MachineDeployment replicas
                → NodeConfig 控制器检测到新节点（Components 为空）
                    → 从 ReleaseImage 按 Role 填充 NodeConfig.Spec.Components
                        → NodeConfig 控制器更新各 ComponentVersionBinding.nodeStatuses[新节点] = Pending
                            → ComponentVersionBinding 控制器检测到新节点状态
                                → 通过 componentVersionRef 找到 ComponentVersion
                                → 执行 installAction（YAML 声明）
                                    → 所有组件安装完成 → NodeConfig Phase=Ready
```
### 6.5 场景五：节点缩容
```
用户从 BKECluster.Spec 中删除节点
    → BKEClusterReconciler 标记 NodeConfig Phase=Deleting
        → NodeConfig 控制器按依赖逆序更新各 ComponentVersionBinding.nodeStatuses[节点] = Uninstalling
            → ComponentVersionBinding 控制器检测到节点卸载状态
                → 通过 componentVersionRef 找到 ComponentVersion
                → 执行 uninstallAction（YAML 声明）
                    → 所有组件卸载完成后
                        → NodeConfig 控制器触发 cluster-api 清理 Machine 资源
                            ├── Master 节点：减少 KubeadmControlPlane replicas
                            ├── Worker 节点：减少 MachineDeployment replicas
                            └── 删除 Machine 对象
                                → 移除 NodeConfig Finalizer
                                    → NodeConfig CR 被垃圾回收
```
**扩缩容对称性**：
```
扩容：创建 NodeConfig → 创建 Machine → 填充 Components → 安装组件 → Ready
缩容：标记 Deleting → 卸载组件 → 删除 Machine → 移除 Finalizer → 回收
```
两者都包含 cluster-api Machine 资源的操作，且顺序正确：扩容先创建 Machine 再安装组件，缩容先卸载组件再删除 Machine。
### 6.6 场景六：升级回滚
```
ComponentVersionBinding 升级失败
    → ComponentVersionBinding 控制器标记 Phase=UpgradeFailed
        → ClusterVersion 控制器检测到失败
            → 根据 UpgradeStrategy.AutoRollback 决定是否自动回滚
                → ClusterVersion 控制器将 ComponentVersionBinding.spec.desiredVersion 回退到旧版本
                    → ComponentVersionBinding 控制器检测 desiredVersion 变化
                        → 通过 componentVersionRef 找到 ComponentVersion
                        → 从 ComponentVersion.spec.versions[] 中查找 rollbackAction
                        → 执行 rollbackAction（YAML 声明）
                            → 回滚到上一个已知良好版本
```
### 6.7 场景七：纳管现有集群
```
用户创建 BKECluster（spec.manageMode=Import）
    → ClusterVersion 控制器创建 clusterManage ComponentVersionBinding
        → ComponentVersionBinding 控制器检测 desiredVersion != installedVersion
            → 通过 componentVersionRef 找到 ComponentVersion
            → 从 ComponentVersion.spec.versions[] 中查找 installAction
            → 执行 installAction（YAML 声明）
                → 收集集群信息 → 推送 Agent → 伪引导 → 兼容性补丁
```
## 7. 提案
### 7.1 资源关联关系
```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  (集群实例，1:1 对应 ClusterVersion)                             │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 1:1
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  (集群版本，记录当前版本/目标版本/升级历史)                         │
│                                                                  │
│  spec.releaseRef ──────────┐                                     │
│  spec.desiredVersion       │                                     │
│  status.currentVersion     │                                     │
│  status.currentReleaseRef ─┼─────┐                               │
└────────────────────────────┼─────┼───────────────────────────────┘
                             │ 1:1 │
                             ▼     │
┌────────────────────────────────────────────────────────────────┐
│                       ReleaseImage                             │
│  (发布版本清单，不可变，定义该版本包含哪些组件及版本)              │
│                                                                │
│  spec.components:                                              │
│    - name: etcd        ────────┐                               │
│      version: v3.5.12          │                               │
│      componentVersionRef:      │                               │
│        name: etcd-v3.5.12      │                               │
│    - name: containerd  ────┐   │                               │
│      version: v1.7.2       │   │                               │
│    - name: kubernetes  ─┐  │   │                               │
│      version: v1.29.0   │  │   │                               │
└─────────────────────────┼──┼───┼───────────────────────────────┘
                          │  │   │
                          │  │   │  引用组件版本
                          ▼  ▼   ▼
┌──────────────────────────────────────────────────────────────────┐
│                    ComponentVersion                              │
│  (组件能力目录，相对不可变，定义安装/升级/回滚/卸载动作)             │
│  (可被多个集群共享)                                               │
│                                                                  │
│  spec.componentName: etcd                                        │
│  spec.scope: Node                                                │
│  spec.dependencies: [nodesEnv]                                   │
│  spec.versions:                                                  │
│    - version: v3.5.11                                            │
│      installAction: {...}                                        │
│      uninstallAction: {...}                                      │
│    - version: v3.5.12                                            │
│      installAction: {...}                                        │
│      upgradeFrom:                                                │
│        - fromVersion: v3.5.11                                    │
│          upgradeAction: {...}                                    │
│      rollbackAction: {...}                                       │
│      uninstallAction: {...}                                      │
│  spec.healthCheck: {...}                                         │
└──────────────────────────┬───────────────────────────────────────┘
                           │ 被引用
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│               ComponentVersionBinding                            │
│  (运行时绑定，频繁可变，定义集群中组件的目标版本和运行状态)           │
│  (每集群独立)                                                     │
│                                                                  │
│  spec.componentVersionRef:                                       │
│    name: etcd-v3.5.12                                            │
│  spec.desiredVersion: v3.5.12     ← 仅修改此字段触发升级           │
│  spec.clusterRef:                                                │
│    name: my-cluster                                              │
│  spec.nodeSelector:                                              │
│    roles: [master]                                               │
│                                                                  │
│  status.installedVersion: v3.5.11                                │
│  status.phase: Upgrading                                         │
│  status.nodeStatuses: {...}                                      │
│  status.lastOperation: {...}                                     │
│  status.conditions: [...]                                        │
└──────────────────────────┬───────────────────────────────────────┘
                           │ nodeSelector 匹配
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                       NodeConfig                                 │
│  (节点组件清单及配置，定义节点上应安装哪些组件)                  │
│                                                                  │
│  spec.nodeName: node-1                                           │
│  spec.roles: [master, etcd]                                      │
│  spec.components:                                                │
│    - componentName: etcd                                         │
│      version: v3.5.12                                            │
│      config:                                                     │
│        dataDir: /var/lib/etcd                                    │
│    - componentName: containerd                                   │
│      version: v1.7.2                                             │
│      config:                                                     │
│        dataDir: /var/lib/containerd                              │
└──────────────────────────────────────────────────────────────────┘
```
**关联关系总结**：
- **BKECluster → ClusterVersion**：1:1，一个集群对应一个版本资源
- **ClusterVersion → ReleaseImage**：1:1（当前版本）+ 1:1（目标版本），通过 `status.currentReleaseRef` 和 `spec.releaseRef` 引用
- **ReleaseImage → ComponentVersion**：1:N，一个发布清单包含多个组件引用
- **ClusterVersion → ComponentVersionBinding**：1:N，ClusterVersion 为每个组件创建 Binding
- **ComponentVersionBinding → ComponentVersion**：N:1，多个 Binding 可引用同一个 ComponentVersion（支持多集群共享）
- **ComponentVersionBinding → NodeConfig**：1:N，Binding 的 nodeSelector 匹配多个 NodeConfig

**关键设计原则**：
| CRD | 职责 | 生命周期 | 修改频率 |
|-----|------|---------|---------|
| **ComponentVersion** | 组件能力目录（"能做什么"） | 随版本发布创建，相对不可变 | 低（新增版本时修改） |
| **ComponentVersionBinding** | 运行时绑定（"要做什么"） | 随集群创建，频繁可变 | 高（升级/扩缩容时修改） |
### 7.2 ClusterVersion CRD
借鉴 OpenShift `config.openshift.io/v1.ClusterVersion`，使用 ReleaseImage CRD 引用替代 Release Image 容器镜像。
```go
// api/cvo/v1beta1/clusterversion_types.go

type ClusterVersionSpec struct {
    DesiredVersion string           `json:"desiredVersion"`
    ReleaseRef     ReleaseReference `json:"releaseRef"`
    ClusterRef     *ClusterReference `json:"clusterRef,omitempty"`
    UpgradeStrategy UpgradeStrategy `json:"upgradeStrategy,omitempty"`
    Pause          bool             `json:"pause,omitempty"`
    AllowDowngrade bool             `json:"allowDowngrade,omitempty"`
}

type ReleaseReference struct {
    Name    string `json:"name"`
    Version string `json:"version,omitempty"`
}

type ClusterReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
}

type UpgradeStrategy struct {
    Type            UpgradeStrategyType  `json:"type,omitempty"`
    MaxUnavailable  *intstr.IntOrString  `json:"maxUnavailable,omitempty"`
    PreCheck        *PreCheckSpec        `json:"preCheck,omitempty"`
    PostCheck       *PostCheckSpec       `json:"postCheck,omitempty"`
    AutoRollback    bool                 `json:"autoRollback,omitempty"`
    RollbackTimeout *metav1.Duration     `json:"rollbackTimeout,omitempty"`
}

type UpgradeStrategyType string

const (
    RollingUpdateStrategy UpgradeStrategyType = "RollingUpdate"
    InPlaceStrategy       UpgradeStrategyType = "InPlace"
    RecreateStrategy      UpgradeStrategyType = "Recreate"
)

type ClusterVersionStatus struct {
    CurrentVersion    string              `json:"currentVersion,omitempty"`
    CurrentReleaseRef *ReleaseReference   `json:"currentReleaseRef,omitempty"`
    CurrentComponents ComponentVersionRefs `json:"currentComponents,omitempty"`
    Phase             ClusterVersionPhase `json:"phase,omitempty"`
    UpgradeSteps      []UpgradeStep       `json:"upgradeSteps,omitempty"`
    CurrentStepIndex  int                 `json:"currentStepIndex,omitempty"`
    History           []UpgradeHistory    `json:"history,omitempty"`
    Conditions        []metav1.Condition  `json:"conditions,omitempty"`
}

type ClusterVersionPhase string

const (
    ClusterVersionInstalling    ClusterVersionPhase = "Installing"
    ClusterVersionInstalled     ClusterVersionPhase = "Installed"
    ClusterVersionUpgrading     ClusterVersionPhase = "Upgrading"
    ClusterVersionUpgradeFailed ClusterVersionPhase = "UpgradeFailed"
    ClusterVersionRollingBack   ClusterVersionPhase = "RollingBack"
    ClusterVersionRolledBack    ClusterVersionPhase = "RolledBack"
    ClusterVersionHealthy       ClusterVersionPhase = "Healthy"
    ClusterVersionDegraded      ClusterVersionPhase = "Degraded"
)

type UpgradeStep struct {
    ComponentName ComponentName    `json:"componentName"`
    Version       string           `json:"version"`
    Phase         UpgradeStepPhase `json:"phase,omitempty"`
    Message       string           `json:"message,omitempty"`
    StartedAt     *metav1.Time     `json:"startedAt,omitempty"`
    CompletedAt   *metav1.Time     `json:"completedAt,omitempty"`
}

type UpgradeHistory struct {
    FromVersion   string        `json:"fromVersion"`
    ToVersion     string        `json:"toVersion"`
    StartedAt     *metav1.Time  `json:"startedAt,omitempty"`
    CompletedAt   *metav1.Time  `json:"completedAt,omitempty"`
    Result        UpgradeResult `json:"result,omitempty"`
    FailedStep    *UpgradeStep  `json:"failedStep,omitempty"`
    RollbackTo    string        `json:"rollbackTo,omitempty"`
}
```
### 7.3 ReleaseImage CRD
ReleaseImage 定义发布版本清单，包含该版本的所有组件及其版本信息。**ReleaseImage 创建后不可修改**，确保版本清单一致性。
```go
// api/cvo/v1beta1/releaseimage_types.go

type ReleaseImageSpec struct {
    Version       string               `json:"version"`
    DisplayName   string               `json:"displayName,omitempty"`
    Description   string               `json:"description,omitempty"`
    ReleaseTime   *metav1.Time         `json:"releaseTime,omitempty"`
    Components    []ReleaseComponent   `json:"components"`
    Images        []ImageManifest      `json:"images,omitempty"`
    UpgradePaths  []UpgradePath        `json:"upgradePaths,omitempty"`
    Compatibility CompatibilityMatrix  `json:"compatibility,omitempty"`
}

type ReleaseComponent struct {
    ComponentName ComponentName `json:"componentName"`
    Version       string        `json:"version"`
    Mandatory     bool          `json:"mandatory,omitempty"`
    Scope         ComponentScope `json:"scope,omitempty"`
    Dependencies  []ComponentName `json:"dependencies,omitempty"`
    NodeSelector  *NodeSelector `json:"nodeSelector,omitempty"`
    ComponentVersionRef *ComponentVersionReference `json:"componentVersionRef,omitempty"`
}

type ComponentVersionReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace,omitempty"`
}

type ImageManifest struct {
    Name    string   `json:"name"`
    Image   string   `json:"image"`
    Digest  string   `json:"digest,omitempty"`
    Arch    []string `json:"arch,omitempty"`
}

type UpgradePath struct {
    FromVersion string `json:"fromVersion"`
    ToVersion   string `json:"toVersion"`
    PreCheck    *ActionSpec `json:"preCheck,omitempty"`
}

type CompatibilityMatrix struct {
    KubernetesVersions []string `json:"kubernetesVersions,omitempty"`
    EtcdVersions       []string `json:"etcdVersions,omitempty"`
    ContainerdVersions []string `json:"containerdVersions,omitempty"`
}

type ReleaseImageStatus struct {
    Phase              ReleaseImagePhase `json:"phase,omitempty"`
    ValidatedComponents []ComponentValidation `json:"validatedComponents,omitempty"`
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

type ReleaseImagePhase string

const (
    ReleaseImageValid   ReleaseImagePhase = "Valid"
    ReleaseImageInvalid ReleaseImagePhase = "Invalid"
)

type ComponentValidation struct {
    ComponentName ComponentName `json:"componentName"`
    Version       string        `json:"version"`
    Valid         bool          `json:"valid"`
    Message       string        `json:"message,omitempty"`
}
```
### 7.4 ComponentVersion CRD
ComponentVersion 定义组件能力目录，包含多个版本及其安装/升级/回滚/卸载动作。

**核心设计**：ComponentVersion 只定义"能做什么"，不包含运行时状态，可被多个集群共享。
```go
// api/cvo/v1beta1/componentversion_types.go

type ComponentVersionSpec struct {
    ComponentName ComponentName    `json:"componentName"`
    Scope         ComponentScope   `json:"scope,omitempty"`
    Dependencies  []DependencySpec `json:"dependencies,omitempty"`
    NodeSelector  *NodeSelector    `json:"nodeSelector,omitempty"`
    Versions      []ComponentVersionEntry `json:"versions"`
}

type ComponentScope string

const (
    ScopeCluster ComponentScope = "Cluster"
    ScopeNode    ComponentScope = "Node"
)

type DependencySpec struct {
    ComponentName     ComponentName     `json:"componentName"`
    Phase             DependencyPhase   `json:"phase,omitempty"`
    VersionConstraint string           `json:"versionConstraint,omitempty"`
}

type DependencyPhase string

const (
    DependencyInstall DependencyPhase = "Install"
    DependencyUpgrade DependencyPhase = "Upgrade"
    DependencyAll     DependencyPhase = "All"
)

type ComponentVersionEntry struct {
    Version         string       `json:"version"`
    InstallAction   *ActionSpec  `json:"installAction,omitempty"`
    UpgradeFrom     []UpgradeFromSpec `json:"upgradeFrom,omitempty"`
    RollbackAction  *ActionSpec  `json:"rollbackAction,omitempty"`
    UninstallAction *ActionSpec  `json:"uninstallAction,omitempty"`
    HealthCheck     *HealthCheckSpec `json:"healthCheck,omitempty"`
}

type UpgradeFromSpec struct {
    FromVersion   string      `json:"fromVersion"`
    UpgradeAction *ActionSpec `json:"upgradeAction"`
}

type ActionSpec struct {
    Steps       []ActionStep     `json:"steps,omitempty"`
    PreCheck    *ActionStep      `json:"preCheck,omitempty"`
    PostCheck   *ActionStep      `json:"postCheck,omitempty"`
    Timeout     *metav1.Duration `json:"timeout,omitempty"`
    Strategy    ActionStrategy   `json:"strategy,omitempty"`
}

type ActionStep struct {
    Name          string            `json:"name"`
    Type          ActionType        `json:"type"`
    Script        string            `json:"script,omitempty"`
    ScriptSource  *SourceSpec       `json:"scriptSource,omitempty"`
    Manifest      string            `json:"manifest,omitempty"`
    ManifestSource *SourceSpec      `json:"manifestSource,omitempty"`
    Chart         *ChartAction      `json:"chart,omitempty"`
    Kubectl       *KubectlAction    `json:"kubectl,omitempty"`
    Condition     string            `json:"condition,omitempty"`
    OnFailure     FailurePolicy     `json:"onFailure,omitempty"`
    Retries       int               `json:"retries,omitempty"`
    NodeSelector  *NodeSelector     `json:"nodeSelector,omitempty"`
}

type ActionType string

const (
    ActionScript   ActionType = "Script"
    ActionManifest ActionType = "Manifest"
    ActionChart    ActionType = "Chart"
    ActionKubectl  ActionType = "Kubectl"
)

type SourceSpec struct {
    Type     SourceType `json:"type"`
    URL      string     `json:"url,omitempty"`
    Path     string     `json:"path,omitempty"`
    Checksum string     `json:"checksum,omitempty"`
    Content  string     `json:"content,omitempty"`
}

type SourceType string

const (
    SourceInline SourceType = "Inline"
    SourceRemote SourceType = "Remote"
    SourceLocal  SourceType = "Local"
)

type ActionStrategy struct {
    ExecutionMode    ExecutionMode    `json:"executionMode,omitempty"`
    BatchSize        int              `json:"batchSize,omitempty"`
    BatchInterval    *metav1.Duration `json:"batchInterval,omitempty"`
    WaitForCompletion bool            `json:"waitForCompletion,omitempty"`
    FailurePolicy    FailurePolicy    `json:"failurePolicy,omitempty"`
}

type ExecutionMode string

const (
    ExecutionParallel ExecutionMode = "Parallel"
    ExecutionSerial   ExecutionMode = "Serial"
    ExecutionRolling  ExecutionMode = "Rolling"
)

type FailurePolicy string

const (
    FailFast FailurePolicy = "FailFast"
    Continue FailurePolicy = "Continue"
)

type ChartAction struct {
    RepoURL      string             `json:"repoURL,omitempty"`
    ChartName    string             `json:"chartName,omitempty"`
    Version      string             `json:"version,omitempty"`
    ChartSource  *ChartSourceSpec   `json:"chartSource,omitempty"`
    ReleaseName  string             `json:"releaseName"`
    Namespace    string             `json:"namespace"`
    Values       string             `json:"values,omitempty"`
    ValuesFrom   []ValuesFromSource `json:"valuesFrom,omitempty"`
}

type ChartSourceSpec struct {
    Type     ChartSourceType `json:"type"`
    HTTPRepo *HTTPRepoSource `json:"httpRepo,omitempty"`
    OCI      *OCISource      `json:"oci,omitempty"`
    Local    *LocalChartSource `json:"local,omitempty"`
}

type ChartSourceType string

const (
    ChartSourceHTTPRepo ChartSourceType = "HTTPRepo"
    ChartSourceOCI      ChartSourceType = "OCI"
    ChartSourceLocal    ChartSourceType = "Local"
)

type KubectlAction struct {
    Operation  KubectlOperation `json:"operation"`
    Resource   string           `json:"resource,omitempty"`
    Namespace  string           `json:"namespace,omitempty"`
    Manifest   string           `json:"manifest,omitempty"`
    FieldPatch string           `json:"fieldPatch,omitempty"`
    Timeout    *metav1.Duration `json:"timeout,omitempty"`
}

type KubectlOperation string

const (
    KubectlApply  KubectlOperation = "Apply"
    KubectlDelete KubectlOperation = "Delete"
    KubectlPatch  KubectlOperation = "Patch"
    KubectlWait   KubectlOperation = "Wait"
    KubectlDrain  KubectlOperation = "Drain"
)

type HealthCheckSpec struct {
    Steps []HealthCheckStep `json:"steps,omitempty"`
}

type HealthCheckStep struct {
    Name           string            `json:"name"`
    Type           ActionType        `json:"type"`
    Script         string            `json:"script,omitempty"`
    Kubectl        *KubectlAction    `json:"kubectl,omitempty"`
    ExpectedOutput string            `json:"expectedOutput,omitempty"`
    Timeout        *metav1.Duration  `json:"timeout,omitempty"`
    Interval       *metav1.Duration  `json:"interval,omitempty"`
}

// ComponentVersionStatus 仅保留验证状态，运行时状态移到 ComponentVersionBinding
type ComponentVersionStatus struct {
    Phase      ComponentVersionPhase `json:"phase,omitempty"`
    Conditions []metav1.Condition    `json:"conditions,omitempty"`
}

type ComponentVersionPhase string

const (
    ComponentVersionActive     ComponentVersionPhase = "Active"
    ComponentVersionDeprecated ComponentVersionPhase = "Deprecated"
)
```
### 7.5 ComponentVersionBinding CRD
ComponentVersionBinding 定义运行时绑定，表达"要做什么"。

**核心设计**：仅修改 `spec.desiredVersion` 触发升级，不触碰 ComponentVersion。
```go
// api/cvo/v1beta1/componentversionbinding_types.go

type ComponentVersionBindingSpec struct {
    ComponentVersionRef ComponentVersionReference `json:"componentVersionRef"`
    DesiredVersion      string                    `json:"desiredVersion"`
    ClusterRef          *ClusterReference         `json:"clusterRef,omitempty"`
    NodeSelector        *NodeSelector             `json:"nodeSelector,omitempty"`
    Pause               bool                      `json:"pause,omitempty"`
}

type ComponentVersionReference struct {
    Name string `json:"name"`
}

type ClusterReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
}

type NodeSelector struct {
    Roles []NodeRole `json:"roles,omitempty"`
}

type ComponentVersionBindingStatus struct {
    InstalledVersion string                        `json:"installedVersion,omitempty"`
    Phase            ComponentPhase                `json:"phase,omitempty"`
    NodeStatuses     map[string]NodeComponentStatus `json:"nodeStatuses,omitempty"`
    LastOperation    *LastOperation                `json:"lastOperation,omitempty"`
    Conditions       []metav1.Condition            `json:"conditions,omitempty"`
}

type ComponentPhase string

const (
    ComponentPending       ComponentPhase = "Pending"
    ComponentInstalling    ComponentPhase = "Installing"
    ComponentInstalled     ComponentPhase = "Installed"
    ComponentUpgrading     ComponentPhase = "Upgrading"
    ComponentUpgradeFailed ComponentPhase = "UpgradeFailed"
    ComponentRollingBack   ComponentPhase = "RollingBack"
    ComponentUninstalling  ComponentPhase = "Uninstalling"
    ComponentUninstalled   ComponentPhase = "Uninstalled"
    ComponentHealthy       ComponentPhase = "Healthy"
    ComponentDegraded      ComponentPhase = "Degraded"
)

type NodeComponentStatus struct {
    Phase     ComponentPhase `json:"phase,omitempty"`
    Version   string         `json:"version,omitempty"`
    Message   string         `json:"message,omitempty"`
    UpdatedAt *metav1.Time   `json:"updatedAt,omitempty"`
}

type LastOperation struct {
    Type        OperationType   `json:"type"`
    Version     string          `json:"version"`
    StartedAt   *metav1.Time    `json:"startedAt,omitempty"`
    CompletedAt *metav1.Time    `json:"completedAt,omitempty"`
    Result      OperationResult `json:"result,omitempty"`
    Message     string          `json:"message,omitempty"`
}

type OperationType string

const (
    OperationInstall   OperationType = "Install"
    OperationUpgrade   OperationType = "Upgrade"
    OperationRollback  OperationType = "Rollback"
    OperationUninstall OperationType = "Uninstall"
)

type OperationResult string

const (
    OperationSuccess OperationResult = "Success"
    OperationFailed  OperationResult = "Failed"
)
```
### 7.6 NodeConfig CRD
NodeConfig 定义节点组件清单及配置，描述节点上应安装哪些组件及其配置。
```go
// api/cvo/v1beta1/nodeconfig_types.go

type NodeConfigSpec struct {
    NodeName    string          `json:"nodeName"`
    NodeIP      string          `json:"nodeIP,omitempty"`
    ClusterRef  *ClusterReference `json:"clusterRef,omitempty"`
    Roles       []NodeRole      `json:"roles"`
    Connection  NodeConnection  `json:"connection,omitempty"`
    OS          NodeOSInfo      `json:"os,omitempty"`
    Components   []NodeComponent `json:"components,omitempty"`
}

type NodeRole string

const (
    NodeRoleMaster NodeRole = "master"
    NodeRoleWorker NodeRole = "worker"
    NodeRoleEtcd   NodeRole = "etcd"
)

type NodeConnection struct {
    SSHKeyRef *SecretReference `json:"sshKeyRef,omitempty"`
    Port      int              `json:"port,omitempty"`
}

type NodeOSInfo struct {
    Type    string `json:"type,omitempty"`
    Version string `json:"version,omitempty"`
    Arch    string `json:"arch,omitempty"`
}

type NodeComponent struct {
    ComponentName       ComponentName          `json:"componentName"`
    Version             string                 `json:"version"`
    Config              *runtime.RawExtension  `json:"config,omitempty"`
    ComponentVersionRef *ComponentVersionReference `json:"componentVersionRef,omitempty"`
}

type NodeConfigStatus struct {
    Phase           NodeConfigPhase                     `json:"phase,omitempty"`
    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`
    OSInfo          *NodeOSDetailInfo                   `json:"osInfo,omitempty"`
    LastOperation   *LastOperation                      `json:"lastOperation,omitempty"`
    Conditions      []metav1.Condition                  `json:"conditions,omitempty"`
}

type NodeConfigPhase string

const (
    NodeConfigPending      NodeConfigPhase = "Pending"
    NodeConfigInstalling   NodeConfigPhase = "Installing"
    NodeConfigInstalled    NodeConfigPhase = "Installed"
    NodeConfigUpgrading    NodeConfigPhase = "Upgrading"
    NodeConfigUninstalling NodeConfigPhase = "Uninstalling"
    NodeConfigDeleting     NodeConfigPhase = "Deleting"
    NodeConfigDeleted      NodeConfigPhase = "Deleted"
    NodeConfigReady        NodeConfigPhase = "Ready"
    NodeConfigNotReady     NodeConfigPhase = "NotReady"
)

type NodeComponentDetailStatus struct {
    Phase       ComponentPhase `json:"phase,omitempty"`
    Version     string         `json:"version,omitempty"`
    InstalledAt *metav1.Time   `json:"installedAt,omitempty"`
    Message     string         `json:"message,omitempty"`
}

type NodeOSDetailInfo struct {
    Type     string `json:"type,omitempty"`
    Version  string `json:"version,omitempty"`
    Arch     string `json:"arch,omitempty"`
    Kernel   string `json:"kernel,omitempty"`
    Hostname string `json:"hostname,omitempty"`
}
```
### 7.7 模板变量系统
ActionSpec 中的 Script、Manifest、Chart.Values 支持模板变量，运行时由 ActionEngine 渲染：
```go
type TemplateContext struct {
    ComponentName string
    Version       string

    NodeIP        string
    NodeHostname  string
    NodeRoles     []string
    NodeOS        NodeOSInfo

    ClusterName      string
    ClusterNamespace string
    CurrentVersion   string

    EtcdVersion        string
    KubernetesVersion  string
    ContainerdVersion  string
    OpenFuyaoVersion   string
    ImageRepo          string
    HTTPRepo           string
    CertificatesDir    string
    ControlPlaneEndpoint string

    ContainerdConfig  *ContainerdComponentConfig
    KubeletConfig     *KubeletComponentConfig
    EtcdConfig        *EtcdComponentConfig
    BKEAgentConfig    *BKEAgentComponentConfig
}
```
**模板语法**：采用 Go 标准 `text/template`
```
{{.Version}}           → v1.7.2
{{.NodeIP}}            → 192.168.1.10
{{.ImageRepo}}         → repo.openfuyao.cn
{{.EtcdDataDir}}       → /var/lib/etcd
{{.ControlPlaneEndpoint}} → 192.168.1.100:6443
```
### 7.8 组件依赖 DAG
```go
var InstallDependencyGraph = map[ComponentName][]ComponentName{
    ComponentBKEAgent:      {},
    ComponentNodesEnv:      {ComponentBKEAgent},
    ComponentClusterAPI:    {ComponentBKEAgent},
    ComponentCerts:         {ComponentClusterAPI},
    ComponentLoadBalancer:  {ComponentCerts},
    ComponentContainerd:    {ComponentNodesEnv},
    ComponentEtcd:          {ComponentCerts, ComponentNodesEnv},
    ComponentKubernetes:    {ComponentContainerd, ComponentEtcd, ComponentLoadBalancer},
    ComponentAddon:         {ComponentKubernetes},
    ComponentNodesPostProc: {ComponentAddon},
    ComponentAgentSwitch:   {ComponentNodesPostProc},
    ComponentBKEProvider:   {ComponentNodesPostProc},
    ComponentOpenFuyao:     {ComponentKubernetes},
}

var UpgradeDependencyGraph = map[ComponentName][]ComponentName{
    ComponentBKEProvider:   {},
    ComponentBKEAgent:      {ComponentBKEProvider},
    ComponentContainerd:    {ComponentBKEAgent},
    ComponentEtcd:          {ComponentBKEAgent},
    ComponentKubernetes:    {ComponentContainerd, ComponentEtcd},
    ComponentOpenFuyao:     {ComponentKubernetes},
    ComponentAddon:         {ComponentKubernetes},
    ComponentNodesPostProc: {ComponentAddon},
}
```
### 7.9 Phase→ComponentVersion 迁移映射表
| Phase | ComponentName | Scope | ActionType | installAction | upgradeAction |
|-------|---------------|-------|------------|---------------|---------------|
| EnsureBKEAgent | bkeAgent | Node | Script | 推送 Agent 二进制 + 配置 kubeconfig + 启动服务 | 更新 Agent 二进制 + 重启服务 |
| EnsureNodesEnv | nodesEnv | Node | Script | 安装 lxcfs/nfs-utils/etcdctl/helm/calicoctl/runc | 更新工具版本 |
| EnsureContainerdUpgrade | containerd | Node | Script | 安装 containerd + 配置 config.toml | 停止→备份→替换→启动→验证 |
| EnsureEtcdUpgrade | etcd | Node | Script | （随 Kubernetes Init 安装） | 逐节点停止→备份→替换→启动→健康检查 |
| EnsureMasterInit | kubernetes | Node | Script | kubeadm init | （升级路径） |
| EnsureMasterJoin | kubernetes | Node | Script | kubeadm join --control-plane | （升级路径） |
| EnsureWorkerJoin | kubernetes | Node | Script | kubeadm join | （升级路径） |
| EnsureMasterUpgrade | kubernetes | Node | Script | （安装路径） | 逐节点 kubeadm upgrade |
| EnsureWorkerUpgrade | kubernetes | Node | Script | （安装路径） | 逐节点 kubeadm upgrade |
| EnsureLoadBalance | loadBalancer | Node | Manifest | haproxy + keepalived static pod | 更新 ConfigMap |
| EnsureClusterAPIObj | clusterAPI | Cluster | Kubectl | 创建 Cluster/Machine 对象 | 更新 Machine replicas |
| EnsureCerts | certs | Cluster | Script | 生成 CA/etcd/front-proxy CA/SA 密钥对 | kubeadm certs renew |
| EnsureAddonDeploy | addon | Cluster | Chart | 安装 calico/coredns/kube-proxy | 升级 Chart |
| EnsureAgentSwitch | agentSwitch | Cluster | Kubectl | 切换 Agent kubeconfig | - |
| EnsureProviderSelfUpgrade | bkeProvider | Cluster | Kubectl | 部署 cluster-api-provider-bke | Patch Deployment image |
| EnsureComponentUpgrade | openFuyao | Cluster | Kubectl | 部署 openfuyao-controller | Patch Deployment image |
| EnsureNodesPostProcess | nodesPostProcess | Node | Script | 执行后处理脚本 | 重新执行后处理脚本 |
| EnsureClusterManage | clusterManage | Cluster | Script | 收集信息→推送Agent→伪引导→兼容性补丁 | 重新收集信息 |
| EnsureWorkerDelete | nodeDelete | Node | Kubectl | - | drain→删除Machine→清理残留 |
| EnsureMasterDelete | nodeDelete | Node | Kubectl | - | drain→删除Machine→移除etcd成员→清理残留 |
| EnsureCluster | clusterHealth | Cluster | Kubectl | - | 检查所有Node Ready→组件健康→更新状态 |

**不映射为 ComponentVersion 的 Phase（5 个）**：

| Phase | 归属 | 原因 |
|-------|------|------|
| EnsureFinalizer | ClusterVersion Controller | 框架级 Finalizer 管理，非组件行为 |
| EnsurePaused | ClusterVersion Controller | 框架级暂停控制，非组件行为 |
| EnsureDeleteOrReset | ClusterVersion Controller | 框架级删除/重置，触发各组件 uninstallAction |
| EnsureDryRun | ClusterVersion Controller | 框架级预检模式，不执行实际操作 |
## 8. 控制器设计思路
### 8.1 ClusterVersion Controller
**核心职责**：
1. **框架级逻辑**：处理 EnsureFinalizer、EnsurePaused、EnsureDeleteOrReset、EnsureDryRun
2. **版本编排**：管理集群版本升级流程，**仅修改 ComponentVersionBinding.spec.desiredVersion**
3. **DAG 调度**：按依赖关系调度 ComponentVersionBinding 升级
4. **历史管理**：维护版本历史，支持回滚
5. **创建 Binding**：为 ReleaseImage 中的每个组件创建 ComponentVersionBinding

**设计思路**：

| 阶段 | 处理逻辑 |
|------|---------|
| **Finalizer 管理** | 在 Reconcile 开始时添加 Finalizer，删除时触发各 ComponentVersionBinding 卸载 |
| **Pause 控制** | 暂停时停止所有 ComponentVersionBinding 的调谐 |
| **Delete/Reset 编排** | 删除时按逆序调用各 ComponentVersionBinding 的 uninstallAction |
| **升级编排** | 检测 desiredVersion 变化 → 解析 ReleaseImage → DAG 调度 → **仅修改 Binding.spec.desiredVersion** |
| **版本历史** | 记录每次升级的 fromVersion/toVersion/result，支持回滚 |

**Reconcile 流程**：
```
Reconcile(ctx, req):
    1. 获取 ClusterVersion 实例
    2. 如果 spec.pause == true，返回
    3. 获取关联的 ReleaseImage
    4. 为每个组件创建/更新 ComponentVersionBinding（如果不存在）
    5. 对比 spec.desiredVersion 与 status.currentVersion
    6. 如果版本相同且 phase=Healthy，返回
    7. 如果版本不同：
       a. 验证升级路径（从 ReleaseImage.spec.upgradePaths）
       b. 计算需要变更的组件列表
       c. 按 DAG 顺序更新 ComponentVersionBinding.spec.desiredVersion
       d. 更新 status.upgradeSteps
       e. 监控各 ComponentVersionBinding 状态
    8. 如果所有 ComponentVersionBinding 完成：
       a. 更新 status.currentVersion = spec.desiredVersion
       b. 更新 status.phase = Healthy
       c. 记录 upgradeHistory
    9. 如果有 ComponentVersionBinding 失败：
       a. 根据 upgradeStrategy.autoRollback 决定是否回滚
       b. 更新 status.phase = UpgradeFailed/RollingBack
```
### 8.2 ReleaseImage Controller
**核心职责**：
1. 验证所有引用的 ComponentVersion 是否存在
2. 验证组件版本兼容性
3. 验证升级路径合法性
4. 更新 status.phase = Valid/Invalid

**Reconcile 流程**：
```
Reconcile(ctx, req):
    1. 获取 ReleaseImage 实例
    2. 验证所有引用的 ComponentVersion 是否存在
    3. 验证组件版本兼容性
    4. 验证升级路径合法性
    5. 更新 status.phase = Valid/Invalid
    6. 更新 status.validatedComponents
```
### 8.3 ComponentVersion Controller
**核心职责**：组件能力目录的验证控制器，不执行运行时操作。

**设计思路**：

| 要点 | 设计 |
|------|------|
| **能力验证** | 验证 spec.versions[] 中各版本的 action 定义是否合法 |
| **依赖验证** | 验证 spec.dependencies 中引用的组件是否存在 |
| **健康检查模板验证** | 验证 healthCheck 中的模板变量是否有效 |
| **状态** | 仅维护验证状态（Active/Deprecated），运行时状态由 ComponentVersionBinding 维护 |

**Reconcile 流程**：
```
Reconcile(ctx, req):
    1. 获取 ComponentVersion 实例
    2. 验证所有版本的 action 定义
    3. 验证依赖组件是否存在
    4. 验证模板变量
    5. 更新 status.phase = Active/Deprecated
    6. 更新 status.conditions
```
### 8.4 ComponentVersionBinding Controller
**核心职责**：组件生命周期的核心执行控制器，是最复杂的控制器。

**设计思路**：

| 要点 | 设计 |
|------|------|
| **版本变更检测** | 对比 spec.desiredVersion 与 status.installedVersion |
| **依赖检查** | checkDependencies() 检查依赖组件 phase + 版本约束 |
| **旧版本卸载** | findOldComponentVersion() 通过 ClusterVersion.currentReleaseRef → 旧 ReleaseImage → 旧 ComponentVersion → uninstallAction |
| **安装/升级/回滚** | 状态机驱动：Pending→Installing→Healthy→Upgrading→Healthy/UpgradeFailed→RollingBack |
| **健康检查** | handleHealthy() 周期性执行 healthCheck，更新 conditions |
| **Finalizer** | handleDeletion() 删除时执行 uninstallAction 后移除 Finalizer |
| **节点级状态** | updateNodeStatuses() / updateSingleNodeStatus() 跟踪每个节点的组件状态 |

**状态机**：
```
Pending → Installing → Healthy ⇄ Upgrading → Healthy/UpgradeFailed → RollingBack → Healthy/Degraded
                         ↓
                      Degraded
```
**Reconcile 流程**：
```
Reconcile(ctx, req):
    1. 获取 ComponentVersionBinding 实例
    2. 通过 spec.componentVersionRef 找到 ComponentVersion
    3. 对比 status.installedVersion 与 spec.desiredVersion
    4. 如果需要安装：
       a. 执行 PreCheck Action
       b. 执行 InstallAction
       c. 执行 PostCheck Action
       d. 更新 status.phase 和 status.nodeStatuses
    5. 如果需要升级：
       a. 查找匹配的 UpgradeAction（从 ComponentVersion.spec.versions[].upgradeFrom 列表）
       b. 查找旧版本 uninstallAction（通过 ClusterVersion.currentReleaseRef）
       c. 执行旧版本 UninstallAction
       d. 执行 PreCheck Action
       e. 执行 UpgradeAction
       f. 执行 PostCheck Action
       g. 更新 status.phase 和 status.nodeStatuses
    6. 如果需要回滚：
       a. 执行 RollbackAction
       b. 更新 status.phase
    7. 如果需要卸载：
       a. 执行 UninstallAction
       b. 更新 status.phase
    8. 执行健康检查
    9. 更新 status.conditions
```
### 8.5 NodeConfig Controller
**核心职责**：节点级组件生命周期管理控制器，承担五大核心职责。

**设计思路**：

| 要点 | 设计 |
|------|------|
| **监听节点增删** | Watch NodeConfig CR 增删事件，新增时触发安装，删除时触发卸载 |
| **触发组件安装** | triggerComponentInstallForNode() 更新 ComponentVersionBinding.nodeStatuses[新节点]=Pending，委托 ComponentVersionBinding Controller 执行 |
| **触发组件卸载** | triggerComponentUninstallForNode() 按依赖逆序更新 nodeStatuses[节点]=Uninstalling，委托 ComponentVersionBinding Controller 执行 |
| **更新节点组件状态** | 从 ComponentVersionBinding.nodeStatuses[本节点] 聚合到 NodeConfig.status.componentStatus |
| **触发 cluster-api 扩缩容** | triggerMachineCreation() 增加 replicas；triggerMachineDeletion() 减少 replicas + 删除 Machine |
| **依赖逆序卸载** | sortComponentsByReverseDependency() 使用拓扑排序逆序，确保被依赖组件最后卸载 |
| **Finalizer 保护** | 添加 Finalizer，删除时先卸载组件再删除 Machine，最后移除 Finalizer |
| **组件自动填充** | populateComponentsFromRelease() 根据 ReleaseImage + 节点角色自动填充组件列表 |

**Reconcile 流程**：
```
Reconcile(ctx, req):
    1. 获取 NodeConfig 实例
    2. 如果 Components 为空且 Phase=""：
       a. 从 ReleaseImage 按 Role 填充 Components
    3. 如果 phase=Deleting：
       a. 通知所有关联的 ComponentVersionBinding 卸载该节点组件
       b. 等待所有组件卸载完成
       c. 触发 cluster-api 删除 Machine
       d. 移除 Finalizer
    4. 遍历 spec.components：
       a. 查找关联的 ComponentVersionBinding
       b. 更新 ComponentVersionBinding 的 nodeStatuses
       c. 如果组件版本不匹配，触发安装/升级
    5. 更新 status.componentStatus
    6. 更新 status.phase
```
## 9. ActionEngine 设计思路
### 9.1 核心定位
ActionEngine 是声明式集群管理的**唯一执行器**，其核心职责是：**解释 ComponentVersion YAML 中的 Action 定义，并按策略在目标节点上执行**。
```
┌─────────────────────┐     ┌─────────────────────┐     ┌──────────────────────┐
│  ComponentVersion   │     │    ActionEngine     │     │   Target Nodes       │
│  (YAML 声明)        │────▶│                     │───▶│                      │
│  installAction      │     │  1. 模板渲染         │     │  Agent / kubelet    │
│  upgradeAction      │     │  2. 来源解析         │     │  执行脚本/应用清单    │
│  uninstallAction    │     │  3. 条件求值         │     │  安装 Chart          │
│  healthCheck        │     │  4. 策略调度         │     │  kubectl 操作        │
└─────────────────────┘     │  5. 步骤执行         │     └──────────────────────┘
                            │  6. 结果收集         │
                            └─────────────────────┘
```
### 9.2 设计原则
| 原则 | 说明 |
|------|------|
| **YAML 即全部** | 所有组件行为由 YAML 声明，引擎不包含任何组件特定逻辑 |
| **单一职责** | ActionEngine 只负责"解释执行"，不负责"编排调度"（编排由 ComponentVersion Controller 负责） |
| **幂等执行** | 同一 Action 多次执行结果一致，支持安全重试 |
| **可观测** | 每个步骤的执行状态、输出、耗时均记录到 ComponentVersion Status |
| **来源无关** | 内容来源对执行逻辑透明，解析后统一为可执行内容 |
### 9.3 架构分层
```
┌─────────────────────────────────────────────────────────┐
│                  ComponentVersion Controller            │
│  (编排层：决定何时执行哪个 Action，管理 DAG 依赖)          │
└──────────────────────────┬──────────────────────────────┘
                           │ 调用
                           ▼
┌─────────────────────────────────────────────────────────┐
│                      ActionEngine                       │
│  ┌───────────┐  ┌───────────┐  ┌───────────────────┐    │
│  │ Renderer  │  │ Resolver  │  │   Executor        │    │
│  │ 模板渲染   │  │ 来源解析  │  │   步骤执行         │    │
│  └───────────┘  └───────────┘  └───────────────────┘    │
│  ┌───────────┐  ┌───────────┐  ┌───────────────────┐    │
│  │ Evaluator │  │ Scheduler │  │   Collector       │    │
│  │ 条件求值   │  │ 策略调度  │  │   结果收集         │    │
│  └───────────┘  └───────────┘  └───────────────────┘    │
└─────────────────────────────────────────────────────────┘
                           │ 下发
                           ▼
┌─────────────────────────────────────────────────────────┐
│                    Node Agent / API Server              │
│  Script → Agent Command                                 │
│  Manifest → kubelet static pod / kubectl apply          │
│  Chart → helm install                                   │
│  Kubectl → API Server                                   │
└─────────────────────────────────────────────────────────┘
```
### 9.4 五大子模块职责
| 子模块 | 职责 | 输入 | 输出 |
|--------|------|------|------|
| **Renderer** | 模板变量渲染 | ActionStep + TemplateContext | 渲染后的内容 |
| **Resolver** | 来源解析与内容获取 | SourceSpec | 可执行内容（字符串） |
| **Evaluator** | 条件表达式求值 | condition 字符串 + TemplateContext | bool |
| **Scheduler** | 按策略调度节点执行 | ActionStrategy + NodeConfig 列表 | 执行计划 |
| **Executor** | 实际执行步骤并收集结果 | 渲染后的 ActionStep + 目标节点 | 执行结果 |
### 9.5 完整执行流程
```
ComponentVersion Controller
    │
    │ 检测到 spec 变化（版本变更/状态驱动）
    ▼
Step 1: 确定待执行 Action
    │
    ▼
Step 2: 匹配目标节点
    │ 根据 ComponentVersion.nodeSelector 筛选 NodeConfig 列表
    │ Scope=Cluster 时无需节点匹配，在控制面执行
    ▼
Step 3: 执行 preCheck
    │ 渲染模板 → 解析来源 → 执行 → 等待结果
    │ preCheck 失败则中止，更新 Status
    ▼
Step 4: 按策略执行 steps
    │ Scheduler 根据 ActionStrategy 生成执行计划：
    │   Parallel: 所有节点同时执行
    │   Serial:   逐节点执行，完成一个再执行下一个
    │   Rolling:  按批次执行，每批 batchSize 个节点
    │             waitForCompletion=true 时，每节点执行 steps+postCheck 后再处理下一个
    │
    │ 对每个节点的每个 step：
    │   4a. Renderer: 渲染模板变量
    │   4b. Resolver: 解析来源
    │   4c. Evaluator: 求值 condition，跳过或执行
    │   4d. Executor: 下发执行
    │   4e. Collector: 收集执行结果，存入步骤上下文
    ▼
Step 5: 执行 postCheck
    │ 渲染模板 → 解析来源 → 执行 → 等待结果（含重试）
    │ postCheck 失败根据 failurePolicy 决定是否中止
    ▼
Step 6: 更新 ComponentVersion Status
    │ 记录每个步骤的状态、输出、耗时
    │ 记录成功/失败节点列表
    │ 更新组件整体状态
```
### 9.6 Rolling 策略的核心逻辑
**关键设计点**：etcd 逐节点升级的本质不是"脚本复杂度"问题，而是"编排语义"问题——需要 ActionEngine 理解"对每个节点执行完整步骤序列并等待确认"这一语义。通过增强 Rolling 策略的 `waitForCompletion` 字段，可以在不引入新 ActionType 的前提下完整支持 etcd 逐节点升级。

| 问题 | 结论 |
|------|------|
| 纯脚本能否实现 etcd 逐节点升级？ | **能**，但需要 ActionEngine 的 Rolling 执行器正确编排 |
| 是否需要新增 ActionType？ | **不需要**，现有 Script + Manifest + Kubectl 类型足够 |
| 需要增强什么？ | `ActionStrategy` 增加 `waitForCompletion` 和 `failurePolicy` 字段 |
| 核心机制 | Rolling 策略 + `batchSize:1` + `waitForCompletion:true` = 逐节点执行 steps → postCheck → 下一节点 |
| 步骤间输出引用 | `condition: "{{.Steps.check-need-upgrade.stdout}} == NEED_UPGRADE"` 实现条件跳过 |
## 10. 迁移策略
| 阶段 | Feature Gate | 行为 |
|------|-------------|------|
| Phase 1 | `DeclarativeOrchestration=false` | CRD + YAML 可创建，ActionEngine 可启动，不影响 PhaseFlow |
| Phase 2 | `DeclarativeOrchestration=true`（可选） | ActionEngine 执行 YAML，对比验证 |
| Phase 3 | `DeclarativeOrchestration=true`（默认） | 全量切换 |
| Phase 4 | 不可逆 | 移除旧 Phase 代码 |
## 11. 目录结构
```
cluster-api-provider-bke/
├── api/
│   └── cvo/v1beta1/
│       ├── clusterversion_types.go
│       ├── releaseimage_types.go
│       ├── componentversion_types.go
│       ├── componentversionbinding_types.go
│       ├── nodeconfig_types.go
│       ├── action_types.go
│       └── zz_generated.deepcopy.go
├── controllers/
│   └── cvo/
│       ├── clusterversion_controller.go
│       ├── releaseimage_controller.go
│       ├── componentversion_controller.go
│       ├── componentversionbinding_controller.go
│       ├── nodeconfig_controller.go
│       └── suite_test.go
├── pkg/
│   ├── actionengine/
│   │   ├── engine.go
│   │   ├── template.go
│   │   ├── condition.go
│   │   └── executor/
│   │       ├── script_executor.go
│   │       ├── manifest_executor.go
│   │       ├── chart_executor.go
│   │       └── kubectl_executor.go
│   ├── cvo/
│   │   ├── orchestrator.go
│   │   ├── validator.go
│   │   ├── rollback.go
│   │   ├── dag_scheduler.go
│   │   └── binding_helper.go
│   └── phaseframe/
├── config/
│   └── components/
│       ├── containerd-v1.7.2.yaml
│       ├── etcd-v3.5.12.yaml
│       ├── kubernetes-v1.29.0.yaml
│       ├── bkeagent-v1.0.0.yaml
│       ├── addon-v1.2.0.yaml
│       ├── certs-v1.0.0.yaml
│       ├── loadbalancer-v1.0.0.yaml
│       ├── clusterapi-v1.0.0.yaml
│       ├── nodesenv-v1.0.0.yaml
│       ├── nodespostprocess-v1.0.0.yaml
│       ├── agentswitch-v1.0.0.yaml
│       ├── openfuyao-v2.6.0.yaml
│       ├── bkeprovider-v1.1.0.yaml
│       ├── clustermanage-v1.0.0.yaml
│       ├── nodedelete-v1.0.0.yaml
│       └── clusterhealth-v1.0.0.yaml
```
## 12. 工作量评估
| 步骤 | 内容 | 工作量 |
|------|------|--------|
| 第一步 | CRD 定义（5 个）+ ActionEngine（4 种 Executor）+ 模板渲染 | 14 人天 |
| 第二步 | ComponentVersion Controller + ComponentVersionBinding Controller + NodeConfig Controller + ClusterVersion Controller | 12 人天 |
| 第三步 | 16 个组件 YAML 声明 + DAGScheduler + 安装 E2E | 10 人天 |
| 第四步 | 升级全链路 + 扩缩容 + 回滚 | 10 人天 |
| 测试 | 单元测试 + 集成测试 + E2E + 新旧路径对比 | 8 人天 |
| **总计** | | **54 人天** |
## 13. 风险评估
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| YAML 脚本调试困难 | 中 | 提供 dry-run 模式 + 模板渲染预览 + 日志输出 |
| 模板变量缺失/错误 | 中 | 模板校验 + 缺失变量报错 + 默认值机制 |
| 复杂条件逻辑难以 YAML 表达 | 低 | 支持基本条件表达式 + 必要时拆分为多个步骤 |
| 旧版本 ComponentVersion 找不到 | 中 | ReleaseImage 保留历史版本；降级为直接执行 uninstallAction |
| DAG 调度死锁 | 高 | 超时 + 循环依赖检测 + 手动跳过 |
## 14. 验收标准
1. **YAML 声明验收**：16 个组件全部通过 YAML 声明安装/升级/卸载，无组件特定 Go 代码
2. **安装验收**：从零创建集群，ActionEngine 解释 YAML 完成安装
3. **升级验收**：修改 ClusterVersion 版本，触发旧版本卸载 + 新版本安装
4. **单组件升级验收**：修改 ComponentVersion 版本，仅升级该组件
5. **扩缩容验收**：添加/移除节点，NodeConfig 自动创建/删除
6. **回滚验收**：升级失败后自动执行 rollbackAction
7. **模板验收**：模板变量正确渲染，条件表达式正确评估
8. **兼容性验收**：Feature Gate 关闭时旧 PhaseFlow 正常运行


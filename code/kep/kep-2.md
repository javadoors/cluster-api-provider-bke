# KEPU-5: 基于 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 的声明式版本编排
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-5 |
| **标题** | 基于 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 的声明式版本编排 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **最后更新** | 2026-04-22 |
| **依赖** | KEPU-4（PhaseFrame 声明式重构） |
| **替代** | KEPU-4 中 ClusterVersion/ComponentVersion/NodeConfig 的部分设计 |
## 1. 摘要
本提案设计一套基于四个核心 CRD（ClusterVersion、ReleaseImage、ComponentVersion、NodeConfig）及其控制器的声明式版本编排系统，借鉴 OpenShift ClusterVersion Operator（CVO）的 ReleaseImage/Payload 模式与 KEPU-4 的 ComponentVersion/NodeConfig 声明式架构，实现 openFuyao 集群版本的安装、升级与扩缩容能力。

核心设计原则：
- **ClusterVersion** = 整个集群版本的概念（1:1 对应 BKECluster）
- **ReleaseImage** = 发布版本清单（1:1 对应 ClusterVersion，定义该版本包含哪些组件及版本）
- **ComponentVersion** = 组件清单（含多个版本，定义组件的安装/升级/回滚动作）
- **NodeConfig** = 节点组件清单及配置（定义节点上应安装哪些组件及配置）

组件的安装、升级与扩缩容由 ComponentVersion 控制器根据 ComponentVersion 配置生成脚本、manifest、chart 等完成，不再调用现有 Phase 代码。
## 2. 动机
### 2.1 现有架构问题
| 问题 | 现状 | 影响 |
|------|------|------|
| **版本概念缺失** | BKECluster 仅记录 KubernetesVersion/EtcdVersion/ContainerdVersion/OpenFuyaoVersion | 无整体版本概念，无法回答"集群当前是什么版本" |
| **发布清单缺失** | 组件版本散落在 BKECluster Spec 各字段 | 无法追溯某个版本包含哪些组件及版本 |
| **组件独立演进受限** | Phase 之间硬编码依赖，升级路径固定 | 无法独立升级单个组件，无法 A/B 测试 |
| **安装逻辑与编排耦合** | Phase 代码既包含编排逻辑又包含安装逻辑 | 无法复用安装逻辑，无法独立测试 |
| **扩缩容与升级耦合** | EnsureWorkerDelete/EnsureMasterDelete 与升级 Phase 混在一起 | 缩容逻辑无法独立演进 |
### 2.2 OpenShift CVO 的启发
OpenShift 的 Cluster Version Operator（CVO）采用以下架构：
```
ClusterVersion (集群版本)
    └── desiredUpdate.release (Release Image 引用)
            └── Release Payload (容器镜像)
                    └── release-manifests/ (组件 manifest 清单)
                            ├── 0000_50_cluster-autoscaler_02_deployment.yaml
                            ├── 0000_70_dns_01_cr.yaml
                            └── ...
```
核心概念：
- **ClusterVersion**：集群级别版本资源，记录当前版本、目标版本、升级历史
- **Release Image**：一个容器镜像，内含该版本所有组件的 manifest 清单
- **CVO**：解析 Release Image，按 DAG 顺序逐个应用 manifest

**借鉴点**：
1. ClusterVersion 作为集群版本的全局入口
2. Release Image 作为版本清单的载体
3. 组件 manifest 声明式定义，CVO 自动编排

**差异点**：
1. OpenShift 使用容器镜像作为 Release Payload，我们使用 ReleaseImage CRD
2. OpenShift 的 manifest 是原生 Kubernetes 资源，我们需要支持 Script/Manifest/Helm 三种执行方式
3. OpenShift 的组件是 Operator，我们的组件包括节点级和集群级两种
### 2.3 KEPU-4 的继承与演进
KEPU-4 提出了 ComponentVersion/NodeConfig/ClusterVersion 三个 CRD，本提案在此基础上：
1. **新增 ReleaseImage CRD**：将版本清单从 ClusterVersion 中分离，实现版本定义与集群实例解耦
2. **重构 ComponentVersion**：将 Action 定义从 Phase 代码迁移到 ComponentVersion 配置，支持 Script/Manifest/Helm 三种执行方式
3. **重构 NodeConfig**：将节点组件清单从 BKECluster Spec 迁移到 NodeConfig，实现节点配置独立管理
4. **明确资源关联关系**：ClusterVersion → ReleaseImage → ComponentVersion，NodeConfig → ComponentVersion
## 3. 目标
### 3.1 主要目标
1. 设计 ClusterVersion、ReleaseImage、ComponentVersion、NodeConfig 四个 CRD 及其关联关系
2. 实现 ClusterVersion 控制器：管理集群版本生命周期（安装→升级→回滚）
3. 实现 ReleaseImage 控制器：解析发布清单，生成 ComponentVersion 列表
4. 实现 ComponentVersion 控制器：根据组件配置生成脚本/manifest/chart 并执行
5. 实现 NodeConfig 控制器：管理节点组件的安装/升级/卸载
6. 将现有 PhaseFrame 各 Phase 重构为 ComponentVersion 声明式架构
### 3.2 非目标
1. 不实现 OpenShift 式的 Release Image 容器镜像载体（使用 CRD 替代）
2. 不实现多集群版本管理（仅单集群）
3. 不实现 OS 级别的版本管理（由 OSProvider 独立负责）
4. 不实现版本包的构建与发布流程（由 CI/CD 独立负责）
## 4. 范围
### 4.1 在范围内
| 范围 | 说明 |
|------|------|
| CRD 定义与注册 | 四个核心 CRD 的 API 定义 |
| 控制器实现 | 四个控制器的 Reconcile 逻辑 |
| Phase→ComponentVersion 迁移 | 20 个 Phase 到 ComponentVersion 的映射与迁移 |
| DAG 调度 | 组件依赖图与调度算法 |
| 版本升级流程 | PreCheck→Upgrade→PostCheck→Rollback |
| 扩缩容流程 | NodeConfig 增删触发组件安装/卸载 |
| Feature Gate 渐进切换 | 新旧架构双轨运行 |
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
| **单集群单 ReleaseImage** | 每个 ClusterVersion 仅关联一个 ReleaseImage |
| **组件不可降级** | 默认不允许组件版本降级，除非显式设置 allowDowngrade |
| **离线环境** | 必须支持离线环境，所有资源通过 CRD 定义，不依赖外部下载 |
| **性能** | 控制器 Reconcile 周期不超过 30s，升级单节点不超过 10min |
## 6. 场景
### 6.1 场景一：全新集群安装
```
用户创建 BKECluster
    → BKEClusterReconciler 创建 ClusterVersion（引用 ReleaseImage）
        → ClusterVersion 控制器解析 ReleaseImage，创建 ComponentVersion 列表
            → DAGScheduler 计算安装顺序
                → ComponentVersion 控制器按序执行安装动作
                    → NodeConfig 控制器在对应节点上执行组件安装
```
### 6.2 场景二：集群版本升级
```
用户更新 BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
    → BKEClusterReconciler 更新 ClusterVersion.Spec.DesiredVersion
        → ClusterVersion 控制器检测版本变更
            → 创建新 ReleaseImage（或引用已有）
                → 对比新旧 ReleaseImage 的 ComponentVersion 列表
                    → 标记需要升级的 ComponentVersion
                        → DAGScheduler 计算升级顺序
                            → ComponentVersion 控制器按序执行升级动作
                                → 升级完成后更新 ClusterVersion.Status
```
### 6.3 场景三：单组件独立升级
```
用户更新 ComponentVersion.Spec.Version
    → ComponentVersion 控制器检测版本变更
        → 执行 PreCheck
            → 执行 UpgradeAction
                → 执行 PostCheck
                    → 更新 ComponentVersion.Status
                        → 更新 ClusterVersion.Status 中的组件版本
```
### 6.4 场景四：节点扩容
```
用户在 BKECluster.Spec 中添加新节点
    → BKEClusterReconciler 创建新 NodeConfig
        → NodeConfig 控制器检测到新节点
            → 根据 NodeConfig.Spec.Components 引用 ComponentVersion
                → ComponentVersion 控制器在新节点上执行安装动作
```
### 6.5 场景五：节点缩容
```
用户在 BKECluster.Spec 中删除节点
    → BKEClusterReconciler 标记 NodeConfig Phase=Deleting
        → NodeConfig 控制器执行节点组件卸载
            → 删除 NodeConfig 资源
```
### 6.6 场景六：升级回滚
```
ComponentVersion 升级失败
    → ComponentVersion 控制器标记 Phase=Failed
        → ClusterVersion 控制器检测到失败
            → 根据 UpgradeStrategy 决定是否自动回滚
                → ComponentVersion 控制器执行 RollbackAction
                    → 回滚到上一个已知良好版本
```
## 7. 提案
### 7.1 资源关联关系
```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  (集群实例，1:1 对应 ClusterVersion)                            │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 1:1
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  (集群版本，记录当前版本/目标版本/升级历史)                      │
│                                                                  │
│  spec.releaseRef ──────────┐                                     │
│  spec.desiredVersion       │                                     │
│  status.currentVersion     │                                     │
└────────────────────────────┼─────────────────────────────────────┘
                             │ 1:1
                             ▼
┌────────────────────────────────────────────────────────────────┐
│                       ReleaseImage                             │
│  (发布版本清单，定义该版本包含哪些组件及版本)                  │
│                                                                │
│  spec.components:                                              │
│    - name: etcd        ────────┐                               │
│      version: v3.5.12          │                               │
│    - name: containerd  ────┐   │                               │
│      version: v1.7.2       │   │                               │
│    - name: kubernetes  ─┐  │   │                               │
│      version: v1.29.0   │  │   │                               │
└─────────────────────────┼──┼───┼───────────────────────────────┘
                          │  │   │
                          │  │   │  N:1 (多个 ReleaseImage 可引用同一 ComponentVersion)
                          ▼  ▼   ▼
┌──────────────────────────────────────────────────────────────────┐
│                    ComponentVersion                              │
│  (组件清单，含多个版本，定义安装/升级/回滚动作)                  │
│                                                                  │
│  spec.componentName: etcd                                        │
│  spec.versions:                                                  │
│    - version: v3.5.11                                            │
│      installAction: {...}                                        │
│    - version: v3.5.12                                            │
│      installAction: {...}                                        │
│      upgradeFrom:                                                │
│        - version: v3.5.11                                        │
│          upgradeAction: {...}                                    │
│  spec.scope: Node                                                │
└──────────────────────────┬───────────────────────────────────────┘
                           │ 1:N (ComponentVersion 被 NodeConfig 引用)
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
### 7.2 ClusterVersion CRD
借鉴 OpenShift `config.openshift.io/v1.ClusterVersion`，但使用 ReleaseImage CRD 引用替代 Release Image 容器镜像。
```go
// api/cvo/v1beta1/clusterversion_types.go

type ClusterVersionSpec struct {
    // desiredVersion 声明集群期望达到的版本
    // +kubebuilder:validation:Required
    DesiredVersion string `json:"desiredVersion"`

    // releaseRef 引用该版本对应的 ReleaseImage
    // +kubebuilder:validation:Required
    ReleaseRef ReleaseReference `json:"releaseRef"`

    // clusterRef 引用该 ClusterVersion 所属的 BKECluster
    ClusterRef *ClusterReference `json:"clusterRef,omitempty"`

    // upgradeStrategy 升级策略
    UpgradeStrategy UpgradeStrategy `json:"upgradeStrategy,omitempty"`

    // pause 暂停版本编排，用于调试或手动控制
    Pause bool `json:"pause,omitempty"`

    // allowDowngrade 是否允许降级
    AllowDowngrade bool `json:"allowDowngrade,omitempty"`
}

type ReleaseReference struct {
    // name 引用的 ReleaseImage 资源名称
    Name string `json:"name"`

    // version ReleaseImage 的版本标签，用于快速校验
    Version string `json:"version,omitempty"`
}

type ClusterReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
}

type UpgradeStrategy struct {
    // type 升级策略类型
    // +kubebuilder:validation:Enum=RollingUpdate;InPlace;Recreate
    Type UpgradeStrategyType `json:"type,omitempty"`

    // maxUnavailable 升级过程中最大不可用节点数
    MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`

    // preCheck 升级前检查配置
    PreCheck *PreCheckSpec `json:"preCheck,omitempty"`

    // postCheck 升级后检查配置
    PostCheck *PostCheckSpec `json:"postCheck,omitempty"`

    // autoRollback 升级失败时是否自动回滚
    AutoRollback bool `json:"autoRollback,omitempty"`

    // rollbackTimeout 自动回滚超时时间
    RollbackTimeout *metav1.Duration `json:"rollbackTimeout,omitempty"`
}

type UpgradeStrategyType string

const (
    RollingUpdateStrategy UpgradeStrategyType = "RollingUpdate"
    InPlaceStrategy       UpgradeStrategyType = "InPlace"
    RecreateStrategy      UpgradeStrategyType = "Recreate"
)

type PreCheckSpec struct {
    // enabled 是否启用升级前检查
    Enabled bool `json:"enabled,omitempty"`

    // timeout 检查超时时间
    Timeout *metav1.Duration `json:"timeout,omitempty"`
}

type PostCheckSpec struct {
    // enabled 是否启用升级后检查
    Enabled bool `json:"enabled,omitempty"`

    // timeout 检查超时时间
    Timeout *metav1.Duration `json:"timeout,omitempty"`

    // healthCheck 健康检查配置
    HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`
}

type ClusterVersionStatus struct {
    // currentVersion 集群当前版本
    CurrentVersion string `json:"currentVersion,omitempty"`

    // currentComponents 当前各组件版本
    CurrentComponents ComponentVersionRefs `json:"currentComponents,omitempty"`

    // phase 集群版本阶段
    // +kubebuilder:validation:Enum=Installing;Installed;Upgrading;UpgradeFailed;RollingBack;RolledBack;Healthy;Degraded
    Phase ClusterVersionPhase `json:"phase,omitempty"`

    // upgradeSteps 当前升级步骤列表
    UpgradeSteps []UpgradeStep `json:"upgradeSteps,omitempty"`

    // currentStepIndex 当前执行到的步骤索引
    CurrentStepIndex int `json:"currentStepIndex,omitempty"`

    // history 升级历史
    History []UpgradeHistory `json:"history,omitempty"`

    // conditions 标准条件
    Conditions []metav1.Condition `json:"conditions,omitempty"`
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

type ComponentVersionRefs map[ComponentName]string

type UpgradeStep struct {
    ComponentName ComponentName      `json:"componentName"`
    Version       string             `json:"version"`
    Phase         UpgradeStepPhase   `json:"phase,omitempty"`
    Message       string             `json:"message,omitempty"`
    StartedAt     *metav1.Time       `json:"startedAt,omitempty"`
    CompletedAt   *metav1.Time       `json:"completedAt,omitempty"`
}

type UpgradeStepPhase string

const (
    UpgradeStepPending    UpgradeStepPhase = "Pending"
    UpgradeStepPreCheck   UpgradeStepPhase = "PreCheck"
    UpgradeStepUpgrading  UpgradeStepPhase = "Upgrading"
    UpgradeStepPostCheck  UpgradeStepPhase = "PostCheck"
    UpgradeStepCompleted  UpgradeStepPhase = "Completed"
    UpgradeStepFailed     UpgradeStepPhase = "Failed"
    UpgradeStepSkipped    UpgradeStepPhase = "Skipped"
)

type UpgradeHistory struct {
    FromVersion    string      `json:"fromVersion"`
    ToVersion      string      `json:"toVersion"`
    StartedAt      metav1.Time `json:"startedAt"`
    CompletedAt    *metav1.Time `json:"completedAt,omitempty"`
    Result         string      `json:"result,omitempty"`
    FailureReason  string      `json:"failureReason,omitempty"`
}
```
### 7.3 ReleaseImage CRD
借鉴 OpenShift Release Payload 概念，使用 CRD 替代容器镜像载体。
```go
// api/cvo/v1beta1/releaseimage_types.go

type ReleaseImageSpec struct {
    // version 发布版本号（如 v2.0.0）
    // +kubebuilder:validation:Required
    Version string `json:"version"`

    // displayName 发布版本显示名称
    DisplayName string `json:"displayName,omitempty"`

    // description 发布版本描述
    Description string `json:"description,omitempty"`

    // releaseTime 发布时间
    ReleaseTime *metav1.Time `json:"releaseTime,omitempty"`

    // components 该发布版本包含的组件列表
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinItems=1
    Components []ReleaseComponent `json:"components"`

    // compatibility 兼容性信息
    Compatibility *ReleaseCompatibility `json:"compatibility,omitempty"`

    // upgradePaths 支持的升级路径
    UpgradePaths []UpgradePath `json:"upgradePaths,omitempty"`
}

type ReleaseComponent struct {
    // componentName 组件名称
    // +kubebuilder:validation:Required
    ComponentName ComponentName `json:"componentName"`

    // version 组件版本
    // +kubebuilder:validation:Required
    Version string `json:"version"`

    // componentVersionRef 引用 ComponentVersion 资源
    // 如果为空，则按 componentName 查找对应的 ComponentVersion
    ComponentVersionRef *ComponentVersionReference `json:"componentVersionRef,omitempty"`

    // mandatory 是否为必需组件
    Mandatory bool `json:"mandatory,omitempty"`
}

type ComponentVersionReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace,omitempty"`
}

type ReleaseCompatibility struct {
    // minKubernetesVersion 最低兼容的 Kubernetes 版本
    MinKubernetesVersion string `json:"minKubernetesVersion,omitempty"`

    // maxKubernetesVersion 最高兼容的 Kubernetes 版本
    MaxKubernetesVersion string `json:"maxKubernetesVersion,omitempty"`

    // minOpenFuyaoVersion 最低兼容的 openFuyao 版本
    MinOpenFuyaoVersion string `json:"minOpenFuyaoVersion,omitempty"`

    // osRequirements 操作系统要求
    OSRequirements []OSRequirement `json:"osRequirements,omitempty"`
}

type OSRequirement struct {
    OSType    string `json:"osType,omitempty"`
    MinVersion string `json:"minVersion,omitempty"`
}

type UpgradePath struct {
    // fromVersion 可从哪个版本升级
    FromVersion string `json:"fromVersion"`

    // toVersion 升级到哪个版本
    ToVersion string `json:"toVersion"`

    // blocked 是否阻塞此升级路径
    Blocked bool `json:"blocked,omitempty"`

    // reason 阻塞原因
    Reason string `json:"reason,omitempty"`
}

type ReleaseImageStatus struct {
    // phase ReleaseImage 阶段
    // +kubebuilder:validation:Enum=Valid;Invalid;Processing
    Phase ReleaseImagePhase `json:"phase,omitempty"`

    // validatedComponents 已验证的组件列表
    ValidatedComponents []ValidatedComponent `json:"validatedComponents,omitempty"`

    // validationErrors 验证错误
    ValidationErrors []string `json:"validationErrors,omitempty"`

    // conditions 标准条件
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ReleaseImagePhase string

const (
    ReleaseImageValid     ReleaseImagePhase = "Valid"
    ReleaseImageInvalid   ReleaseImagePhase = "Invalid"
    ReleaseImageProcessing ReleaseImagePhase = "Processing"
)

type ValidatedComponent struct {
    ComponentName ComponentName `json:"componentName"`
    Version       string        `json:"version"`
    Available     bool          `json:"available"`
    Message       string        `json:"message,omitempty"`
}
```
### 7.4 ComponentVersion CRD
继承 KEPU-4 设计，重构 Action 定义，支持 Script/Manifest/Helm 三种执行方式。
```go
// api/cvo/v1beta1/componentversion_types.go

type ComponentVersionSpec struct {
    // componentName 组件名称
    // +kubebuilder:validation:Required
    ComponentName ComponentName `json:"componentName"`

    // scope 组件作用域
    // +kubebuilder:validation:Enum=Cluster;Node
    // +kubebuilder:default=Node
    Scope ComponentScope `json:"scope,omitempty"`

    // versions 组件版本列表（支持多版本共存）
    // +kubebuilder:validation:MinItems=1
    Versions []ComponentVersionEntry `json:"versions"`

    // dependencies 组件依赖
    Dependencies []ComponentDependency `json:"dependencies,omitempty"`

    // healthCheck 健康检查配置
    HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`
}

type ComponentScope string

const (
    ScopeCluster ComponentScope = "Cluster"
    ScopeNode    ComponentScope = "Node"
)

type ComponentVersionEntry struct {
    // version 版本号
    // +kubebuilder:validation:Required
    Version string `json:"version"`

    // installAction 安装动作
    InstallAction *ActionSpec `json:"installAction,omitempty"`

    // upgradeFrom 从哪些版本升级的动作映射
    UpgradeFrom []UpgradeActionEntry `json:"upgradeFrom,omitempty"`

    // rollbackAction 回滚动作
    RollbackAction *ActionSpec `json:"rollbackAction,omitempty"`

    // uninstallAction 卸载动作
    UninstallAction *ActionSpec `json:"uninstallAction,omitempty"`

    // preCheck 安装/升级前检查
    PreCheck *ActionSpec `json:"preCheck,omitempty"`

    // postCheck 安装/升级后检查
    PostCheck *ActionSpec `json:"postCheck,omitempty"`

    // compatibility 版本兼容性
    Compatibility *VersionCompatibility `json:"compatibility,omitempty"`
}

type UpgradeActionEntry struct {
    // fromVersion 从哪个版本升级
    FromVersion string `json:"fromVersion"`

    // action 升级动作
    Action *ActionSpec `json:"action"`
}

type ActionSpec struct {
    // type 动作类型
    // +kubebuilder:validation:Enum=Script;Manifest;Helm;Controller
    Type ActionType `json:"type"`

    // script 脚本动作配置（type=Script 时使用）
    Script *ScriptAction `json:"script,omitempty"`

    // manifest Manifest 动作配置（type=Manifest 时使用）
    Manifest *ManifestAction `json:"manifest,omitempty"`

    // helm Helm 动作配置（type=Helm 时使用）
    Helm *HelmAction `json:"helm,omitempty"`

    // controller 控制器动作配置（type=Controller 时使用）
    Controller *ControllerAction `json:"controller,omitempty"`

    // timeout 动作超时时间
    Timeout *metav1.Duration `json:"timeout,omitempty"`

    // retryPolicy 重试策略
    RetryPolicy *RetryPolicy `json:"retryPolicy,omitempty"`
}

type ActionType string

const (
    ActionScript     ActionType = "Script"
    ActionManifest   ActionType = "Manifest"
    ActionHelm       ActionType = "Helm"
    ActionController ActionType = "Controller"
)

type ScriptAction struct {
    // source 脚本来源
    // +kubebuilder:validation:Enum=Inline;ConfigMapRef;SecretRef
    Source ScriptSource `json:"source"`

    // inline 内联脚本内容
    Inline string `json:"inline,omitempty"`

    // configMapRef 从 ConfigMap 引用脚本
    ConfigMapRef *ConfigMapKeySelector `json:"configMapRef,omitempty"`

    // secretRef 从 Secret 引用脚本
    SecretRef *SecretKeySelector `json:"secretRef,omitempty"`

    // args 脚本参数
    Args []string `json:"args,omitempty"`

    // env 脚本环境变量
    Env []EnvVar `json:"env,omitempty"`
}

type ManifestAction struct {
    // source Manifest 来源
    // +kubebuilder:validation:Enum=Inline;ConfigMapRef;URL
    Source ManifestSource `json:"source"`

    // inline 内联 Manifest 内容
    Inline string `json:"inline,omitempty"`

    // configMapRef 从 ConfigMap 引用 Manifest
    ConfigMapRef *ConfigMapKeySelector `json:"configMapRef,omitempty"`

    // url 从 URL 下载 Manifest
    URL string `json:"url,omitempty"`
}

type HelmAction struct {
    // chartRef Helm Chart 引用
    ChartRef *HelmChartRef `json:"chartRef,omitempty"`

    // values Helm Values（内联或引用）
    Values *HelmValues `json:"values,omitempty"`

    // releaseName Helm Release 名称
    ReleaseName string `json:"releaseName,omitempty"`

    // namespace Helm Release 命名空间
    Namespace string `json:"namespace,omitempty"`
}

type HelmChartRef struct {
    // repo Helm Chart 仓库地址
    Repo string `json:"repo,omitempty"`

    // name Helm Chart 名称
    Name string `json:"name"`

    // version Helm Chart 版本
    Version string `json:"version"`
}

type HelmValues struct {
    // inline 内联 Values
    Inline string `json:"inline,omitempty"`

    // configMapRef 从 ConfigMap 引用 Values
    ConfigMapRef *ConfigMapKeySelector `json:"configMapRef,omitempty"`
}

type ControllerAction struct {
    // executor 执行器名称
    // +kubebuilder:validation:Required
    Executor string `json:"executor"`

    // params 执行器参数
    Params map[string]string `json:"params,omitempty"`
}

type RetryPolicy struct {
    // maxRetries 最大重试次数
    MaxRetries int `json:"maxRetries,omitempty"`

    // retryInterval 重试间隔
    RetryInterval *metav1.Duration `json:"retryInterval,omitempty"`

    // retryOnFailed 仅在失败时重试
    RetryOnFailed bool `json:"retryOnFailed,omitempty"`
}

type ComponentDependency struct {
    // componentName 依赖的组件名称
    ComponentName ComponentName `json:"componentName"`

    // versionConstraint 版本约束（如 ">=v1.7.0"）
    VersionConstraint string `json:"versionConstraint,omitempty"`

    // phase 依赖的阶段
    // +kubebuilder:validation:Enum=Install;Upgrade;All
    Phase DependencyPhase `json:"phase,omitempty"`
}

type DependencyPhase string

const (
    DependencyInstall DependencyPhase = "Install"
    DependencyUpgrade DependencyPhase = "Upgrade"
    DependencyAll     DependencyPhase = "All"
)

type VersionCompatibility struct {
    // minKubernetesVersion 最低兼容的 Kubernetes 版本
    MinKubernetesVersion string `json:"minKubernetesVersion,omitempty"`

    // maxKubernetesVersion 最高兼容的 Kubernetes 版本
    MaxKubernetesVersion string `json:"maxKubernetesVersion,omitempty"`

    // osRequirements 操作系统要求
    OSRequirements []OSRequirement `json:"osRequirements,omitempty"`
}

type HealthCheckSpec struct {
    // type 健康检查类型
    // +kubebuilder:validation:Enum=HTTP;TCP;Command;CRD
    Type HealthCheckType `json:"type"`

    // http HTTP 健康检查
    HTTP *HTTPHealthCheck `json:"http,omitempty"`

    // tcp TCP 健康检查
    TCP *TCPHealthCheck `json:"tcp,omitempty"`

    // command 命令行健康检查
    Command *CommandHealthCheck `json:"command,omitempty"`

    // crd CRD 状态健康检查
    CRD *CRDHealthCheck `json:"crd,omitempty"`

    // timeout 健康检查超时
    Timeout *metav1.Duration `json:"timeout,omitempty"`

    // interval 健康检查间隔
    Interval *metav1.Duration `json:"interval,omitempty"`
}

type HealthCheckType string

const (
    HealthCheckHTTP   HealthCheckType = "HTTP"
    HealthCheckTCP    HealthCheckType = "TCP"
    HealthCheckCommand HealthCheckType = "Command"
    HealthCheckCRD    HealthCheckType = "CRD"
)

type HTTPHealthCheck struct {
    URL        string            `json:"url"`
    Method     string            `json:"method,omitempty"`
    Headers    map[string]string `json:"headers,omitempty"`
    StatusCode int               `json:"statusCode,omitempty"`
}

type TCPHealthCheck struct {
    Address string `json:"address"`
}

type CommandHealthCheck struct {
    Command []string `json:"command"`
}

type CRDHealthCheck struct {
    APIVersion string `json:"apiVersion"`
    Kind       string `json:"kind"`
    Name       string `json:"name"`
    Namespace  string `json:"namespace,omitempty"`
    Condition  string `json:"condition"`
}

type ComponentVersionStatus struct {
    // phase 组件阶段
    // +kubebuilder:validation:Enum=Pending;Installing;Installed;Upgrading;UpgradeFailed;RollingBack;RolledBack;Uninstalling;Uninstalled;Healthy;Degraded
    Phase ComponentPhase `json:"phase,omitempty"`

    // installedVersion 已安装版本
    InstalledVersion string `json:"installedVersion,omitempty"`

    // desiredVersion 期望版本
    DesiredVersion string `json:"desiredVersion,omitempty"`

    // nodeStatuses 节点级状态（scope=Node 时使用）
    NodeStatuses map[string]NodeComponentStatus `json:"nodeStatuses,omitempty"`

    // lastOperation 最后一次操作
    LastOperation *LastOperation `json:"lastOperation,omitempty"`

    // message 状态消息
    Message string `json:"message,omitempty"`

    // conditions 标准条件
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ComponentPhase string

const (
    ComponentPending        ComponentPhase = "Pending"
    ComponentInstalling     ComponentPhase = "Installing"
    ComponentInstalled      ComponentPhase = "Installed"
    ComponentUpgrading      ComponentPhase = "Upgrading"
    ComponentUpgradeFailed  ComponentPhase = "UpgradeFailed"
    ComponentRollingBack    ComponentPhase = "RollingBack"
    ComponentRolledBack     ComponentPhase = "RolledBack"
    ComponentUninstalling   ComponentPhase = "Uninstalling"
    ComponentUninstalled    ComponentPhase = "Uninstalled"
    ComponentHealthy        ComponentPhase = "Healthy"
    ComponentDegraded       ComponentPhase = "Degraded"
)

type NodeComponentStatus struct {
    Phase      ComponentPhase `json:"phase,omitempty"`
    Version    string         `json:"version,omitempty"`
    Message    string         `json:"message,omitempty"`
    UpdatedAt  *metav1.Time   `json:"updatedAt,omitempty"`
}

type LastOperation struct {
    Type        OperationType `json:"type"`
    Version     string        `json:"version"`
    StartedAt   *metav1.Time  `json:"startedAt,omitempty"`
    CompletedAt *metav1.Time  `json:"completedAt,omitempty"`
    Result      OperationResult `json:"result,omitempty"`
    Message     string        `json:"message,omitempty"`
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
    OperationSucceeded OperationResult = "Succeeded"
    OperationFailed    OperationResult = "Failed"
    OperationCanceled  OperationResult = "Canceled"
)
```
### 7.5 NodeConfig CRD
```go
// api/cvo/v1beta1/nodeconfig_types.go

type NodeConfigSpec struct {
    // nodeName 节点名称（对应 BKECluster 中的节点标识）
    // +kubebuilder:validation:Required
    NodeName string `json:"nodeName"`

    // nodeIP 节点 IP 地址
    NodeIP string `json:"nodeIP,omitempty"`

    // clusterRef 所属集群引用
    ClusterRef *ClusterReference `json:"clusterRef,omitempty"`

    // roles 节点角色
    // +kubebuilder:validation:MinItems=1
    Roles []NodeRole `json:"roles"`

    // connection 节点连接信息
    Connection NodeConnection `json:"connection,omitempty"`

    // os 节点操作系统信息
    OS NodeOSInfo `json:"os,omitempty"`

    // components 节点应安装的组件列表
    Components []NodeComponent `json:"components,omitempty"`
}

type NodeRole string

const (
    NodeRoleMaster NodeRole = "master"
    NodeRoleWorker NodeRole = "worker"
    NodeRoleEtcd   NodeRole = "etcd"
)

type NodeConnection struct {
    // sshKeyRef SSH 密钥引用
    SSHKeyRef *SecretReference `json:"sshKeyRef,omitempty"`

    // port SSH 端口
    Port int `json:"port,omitempty"`
}

type NodeOSInfo struct {
    // type 操作系统类型
    Type string `json:"type,omitempty"`

    // version 操作系统版本
    Version string `json:"version,omitempty"`

    // arch 操作系统架构
    Arch string `json:"arch,omitempty"`
}

type NodeComponent struct {
    // componentName 组件名称
    // +kubebuilder:validation:Required
    ComponentName ComponentName `json:"componentName"`

    // version 期望安装的版本
    // +kubebuilder:validation:Required
    Version string `json:"version"`

    // config 组件配置（透传给 ComponentVersion 的 Action）
    Config *runtime.RawExtension `json:"config,omitempty"`

    // componentVersionRef 引用的 ComponentVersion 资源
    ComponentVersionRef *ComponentVersionReference `json:"componentVersionRef,omitempty"`
}

type NodeConfigStatus struct {
    // phase 节点配置阶段
    // +kubebuilder:validation:Enum=Pending;Installing;Installed;Upgrading;Uninstalling;Deleting;Deleted;Ready;NotReady
    Phase NodeConfigPhase `json:"phase,omitempty"`

    // componentStatus 各组件状态
    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`

    // osInfo 实际操作系统信息
    OSInfo *NodeOSDetailInfo `json:"osInfo,omitempty"`

    // lastOperation 最后一次操作
    LastOperation *LastOperation `json:"lastOperation,omitempty"`

    // conditions 标准条件
    Conditions []metav1.Condition `json:"conditions,omitempty"`
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
    Phase         ComponentPhase    `json:"phase,omitempty"`
    Version       string            `json:"version,omitempty"`
    InstalledAt   *metav1.Time      `json:"installedAt,omitempty"`
    Message       string            `json:"message,omitempty"`
}

type NodeOSDetailInfo struct {
    Type       string `json:"type,omitempty"`
    Version    string `json:"version,omitempty"`
    Arch       string `json:"arch,omitempty"`
    Kernel     string `json:"kernel,omitempty"`
    Hostname   string `json:"hostname,omitempty"`
}
```
### 7.6 ComponentName 枚举
```go
// api/cvo/v1beta1/component_types.go

type ComponentName string

const (
    ComponentBKEAgent       ComponentName = "bkeAgent"
    ComponentNodesEnv       ComponentName = "nodesEnv"
    ComponentContainerd     ComponentName = "containerd"
    ComponentEtcd           ComponentName = "etcd"
    ComponentKubernetes     ComponentName = "kubernetes"
    ComponentClusterAPI     ComponentName = "clusterAPI"
    ComponentCerts          ComponentName = "certs"
    ComponentLoadBalancer   ComponentName = "loadBalancer"
    ComponentAddon          ComponentName = "addon"
    ComponentOpenFuyao      ComponentName = "openFuyao"
    ComponentBKEProvider    ComponentName = "bkeProvider"
    ComponentNodesPostProc  ComponentName = "nodesPostProcess"
    ComponentAgentSwitch    ComponentName = "agentSwitch"
)
```
### 7.7 控制器设计
#### 7.7.1 ClusterVersion 控制器
```
Reconcile(ctx, req):
    1. 获取 ClusterVersion 实例
    2. 如果 spec.pause == true，返回
    3. 获取关联的 ReleaseImage
    4. 对比 spec.desiredVersion 与 status.currentVersion
    5. 如果版本相同且 phase=Healthy，返回
    6. 如果版本不同：
       a. 验证升级路径（从 ReleaseImage.spec.upgradePaths）
       b. 计算需要变更的 ComponentVersion 列表
       c. 按 DAG 顺序创建/更新 ComponentVersion
       d. 更新 status.upgradeSteps
       e. 监控各 ComponentVersion 状态
    7. 如果所有 ComponentVersion 完成：
       a. 更新 status.currentVersion = spec.desiredVersion
       b. 更新 status.phase = Healthy
       c. 记录 upgradeHistory
    8. 如果有 ComponentVersion 失败：
       a. 根据 upgradeStrategy.autoRollback 决定是否回滚
       b. 更新 status.phase = UpgradeFailed/RollingBack
```
#### 7.7.2 ReleaseImage 控制器
```
Reconcile(ctx, req):
    1. 获取 ReleaseImage 实例
    2. 验证所有引用的 ComponentVersion 是否存在
    3. 验证组件版本兼容性
    4. 验证升级路径合法性
    5. 更新 status.phase = Valid/Invalid
    6. 更新 status.validatedComponents
```
#### 7.7.3 ComponentVersion 控制器
```
Reconcile(ctx, req):
    1. 获取 ComponentVersion 实例
    2. 确定 desiredVersion（从 spec.versions 或 NodeConfig 引用）
    3. 对比 installedVersion 与 desiredVersion
    4. 如果需要安装：
       a. 执行 PreCheck Action
       b. 执行 InstallAction
       c. 执行 PostCheck Action
       d. 更新 status.phase 和 status.nodeStatuses
    5. 如果需要升级：
       a. 查找匹配的 UpgradeAction（从 upgradeFrom 列表）
       b. 执行 PreCheck Action
       c. 执行 UpgradeAction
       d. 执行 PostCheck Action
       e. 更新 status.phase 和 status.nodeStatuses
    6. 如果需要回滚：
       a. 执行 RollbackAction
       b. 更新 status.phase
    7. 如果需要卸载：
       a. 执行 UninstallAction
       b. 更新 status.phase
    8. 执行健康检查
    9. 更新 status.conditions
```
#### 7.7.4 NodeConfig 控制器
```
Reconcile(ctx, req):
    1. 获取 NodeConfig 实例
    2. 如果 phase=Deleting：
       a. 通知所有关联的 ComponentVersion 卸载该节点组件
       b. 删除 NodeConfig
    3. 遍历 spec.components：
       a. 查找关联的 ComponentVersion
       b. 更新 ComponentVersion 的 nodeStatuses
       c. 如果组件版本不匹配，触发安装/升级
    4. 更新 status.componentStatus
    5. 更新 status.phase
```
### 7.8 组件依赖 DAG
```go
// pkg/orchestrator/dag.go

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
    ComponentAgentSwitch:   {ComponentNodesPostProc},
}
```
### 7.9 Phase→ComponentVersion 迁移映射表
| Phase | ComponentName | Scope | ActionType | installAction | upgradeAction |
|-------|---------------|-------|------------|---------------|---------------|
| EnsureBKEAgent | bkeAgent | Node | Controller | Executor:bkeAgent | Executor:bkeAgent |
| EnsureNodesEnv | nodesEnv | Node | Controller | Executor:nodesEnv | Executor:nodesEnv |
| EnsureContainerdUpgrade | containerd | Node | Script | Inline: containerd install script | Inline: reset+redeploy script |
| EnsureEtcdUpgrade | etcd | Node | Controller | Executor:etcd | Executor:etcd |
| EnsureMasterInit | kubernetes | Node | Controller | Executor:kubernetes-init | - |
| EnsureMasterJoin | kubernetes | Node | Controller | Executor:kubernetes-join | - |
| EnsureWorkerJoin | kubernetes | Node | Controller | Executor:kubernetes-join | - |
| EnsureMasterUpgrade | kubernetes | Node | Controller | - | Executor:kubernetes-upgrade |
| EnsureWorkerUpgrade | kubernetes | Node | Controller | - | Executor:kubernetes-upgrade |
| EnsureLoadBalance | loadBalancer | Node | Script | Inline: haproxy install | Inline: haproxy update |
| EnsureClusterAPIObj | clusterAPI | Cluster | Controller | Executor:clusterAPI | Executor:clusterAPI |
| EnsureCerts | certs | Cluster | Controller | Executor:certs | Executor:certs |
| EnsureAddonDeploy | addon | Cluster | Helm | ChartRef: addon chart | ChartRef: addon chart |
| EnsureAgentSwitch | agentSwitch | Cluster | Controller | Executor:agentSwitch | - |
| EnsureProviderSelfUpgrade | bkeProvider | Cluster | Controller | Executor:bkeProvider | Executor:bkeProvider |
| EnsureComponentUpgrade | openFuyao | Cluster | Controller | Executor:openFuyao | Executor:openFuyao |
| EnsureNodesPostProcess | nodesPostProcess | Node | Script | Inline: post-process script | Inline: post-process script |
### 7.10 ReleaseImage 示例
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.0.0
  namespace: bke-system
spec:
  version: v2.0.0
  displayName: "openFuyao v2.0.0"
  description: "openFuyao 2.0 GA Release"
  releaseTime: "2026-04-01T00:00:00Z"
  components:
    - componentName: bkeAgent
      version: v1.2.0
      mandatory: true
    - componentName: nodesEnv
      version: v1.0.0
      mandatory: true
    - componentName: containerd
      version: v1.7.2
      mandatory: true
    - componentName: etcd
      version: v3.5.12
      mandatory: true
    - componentName: kubernetes
      version: v1.29.0
      mandatory: true
    - componentName: clusterAPI
      version: v1.0.0
      mandatory: true
    - componentName: certs
      version: v1.0.0
      mandatory: true
    - componentName: loadBalancer
      version: v1.0.0
      mandatory: true
    - componentName: addon
      version: v1.0.0
      mandatory: true
    - componentName: openFuyao
      version: v2.0.0
      mandatory: true
    - componentName: bkeProvider
      version: v1.1.0
      mandatory: true
    - componentName: nodesPostProcess
      version: v1.0.0
      mandatory: false
    - componentName: agentSwitch
      version: v1.0.0
      mandatory: true
  compatibility:
    minKubernetesVersion: v1.28.0
    maxKubernetesVersion: v1.30.0
    osRequirements:
      - osType: kylin
        minVersion: V10
      - osType: centos
        minVersion: "7.9"
  upgradePaths:
    - fromVersion: v1.9.0
      toVersion: v2.0.0
    - fromVersion: v1.8.0
      toVersion: v2.0.0
```
### 7.11 ComponentVersion 示例（etcd）
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: etcd
  namespace: bke-system
spec:
  componentName: etcd
  scope: Node
  dependencies:
    - componentName: certs
      versionConstraint: ">=v1.0.0"
      phase: Install
    - componentName: nodesEnv
      versionConstraint: ">=v1.0.0"
      phase: Install
  healthCheck:
    type: Command
    command:
      - /bin/sh
      - -c
      - ETCDCTL_API=3 etcdctl endpoint health
    timeout: 30s
    interval: 5s
  versions:
    - version: v3.5.11
      installAction:
        type: Controller
        controller:
          executor: etcd
          params:
            operation: install
      upgradeFrom: []
    - version: v3.5.12
      installAction:
        type: Controller
        controller:
          executor: etcd
          params:
            operation: install
      upgradeFrom:
        - fromVersion: v3.5.11
          action:
            type: Controller
            controller:
              executor: etcd
              params:
                operation: upgrade
                backupEnabled: "true"
      rollbackAction:
        type: Controller
        controller:
          executor: etcd
          params:
            operation: rollback
      uninstallAction:
        type: Script
        script:
          source: Inline
          inline: |
            #!/bin/sh
            rm -rf /var/lib/etcd
            rm -f /etc/kubernetes/manifests/etcd.yaml
```
### 7.12 ClusterVersion 示例
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ClusterVersion
metadata:
  name: bke-cluster-version
  namespace: bke-system
spec:
  desiredVersion: v2.0.0
  releaseRef:
    name: openfuyao-v2.0.0
    version: v2.0.0
  clusterRef:
    name: bke-cluster
    namespace: bke-system
  upgradeStrategy:
    type: RollingUpdate
    maxUnavailable: 1
    preCheck:
      enabled: true
      timeout: 5m
    postCheck:
      enabled: true
      timeout: 10m
      healthCheck:
        type: CRD
        crd:
          apiVersion: cvo.openfuyao.cn/v1beta1
          kind: ClusterVersion
          name: bke-cluster-version
          condition: Healthy
    autoRollback: true
    rollbackTimeout: 30m
status:
  currentVersion: v1.9.0
  currentComponents:
    bkeAgent: v1.1.0
    nodesEnv: v1.0.0
    containerd: v1.7.0
    etcd: v3.5.11
    kubernetes: v1.28.0
    clusterAPI: v1.0.0
    certs: v1.0.0
    loadBalancer: v1.0.0
    addon: v1.0.0
    openFuyao: v1.9.0
    bkeProvider: v1.0.0
    nodesPostProcess: v1.0.0
    agentSwitch: v1.0.0
  phase: Upgrading
  upgradeSteps:
    - componentName: bkeProvider
      version: v1.1.0
      phase: Completed
    - componentName: bkeAgent
      version: v1.2.0
      phase: Completed
    - componentName: containerd
      version: v1.7.2
      phase: Upgrading
    - componentName: etcd
      version: v3.5.12
      phase: Pending
    - componentName: kubernetes
      version: v1.29.0
      phase: Pending
    - componentName: openFuyao
      version: v2.0.0
      phase: Pending
  currentStepIndex: 2
  history:
    - fromVersion: v1.8.0
      toVersion: v1.9.0
      startedAt: "2026-03-01T10:00:00Z"
      completedAt: "2026-03-01T10:30:00Z"
      result: Succeeded
```
### 7.13 NodeConfig 示例
```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: NodeConfig
metadata:
  name: node-1
  namespace: bke-system
spec:
  nodeName: node-1
  nodeIP: 192.168.1.10
  clusterRef:
    name: bke-cluster
    namespace: bke-system
  roles:
    - master
    - etcd
  os:
    type: kylin
    version: V10
    arch: amd64
  components:
    - componentName: bkeAgent
      version: v1.2.0
    - componentName: nodesEnv
      version: v1.0.0
    - componentName: containerd
      version: v1.7.2
      config:
        dataDir: /var/lib/containerd
    - componentName: etcd
      version: v3.5.12
      config:
        dataDir: /var/lib/etcd
    - componentName: kubernetes
      version: v1.29.0
      config:
        role: master
    - componentName: loadBalancer
      version: v1.0.0
    - componentName: nodesPostProcess
      version: v1.0.0
status:
  phase: Ready
  componentStatus:
    bkeAgent:
      phase: Healthy
      version: v1.2.0
    nodesEnv:
      phase: Healthy
      version: v1.0.0
    containerd:
      phase: Upgrading
      version: v1.7.0
    etcd:
      phase: Healthy
      version: v3.5.11
    kubernetes:
      phase: Healthy
      version: v1.28.0
```
## 8. 迁移策略
### 8.1 Feature Gate 渐进切换
```go
const FeatureGateDeclarativeVersionOrchestration = "DeclarativeVersionOrchestration"
```
- **Phase A**（默认关闭）：新 CRD 注册，控制器不运行
- **Phase B**（手动开启）：新控制器运行，但仅观察模式（dry-run），不实际执行
- **Phase C**（手动开启）：新控制器接管安装逻辑，PhaseFrame 仍运行但不执行
- **Phase D**（默认开启）：完全切换到新架构，移除 PhaseFrame 代码
### 8.2 迁移步骤
| 步骤 | 工作量 | 说明 |
|------|--------|------|
| 1. 定义 CRD | 1人月 | 四个 CRD 的 API 定义与注册 |
| 2. 实现 ReleaseImage 控制器 | 0.5人月 | 验证逻辑 |
| 3. 实现 ComponentVersion 控制器 | 3人月 | 核心逻辑，含 Script/Manifest/Helm/Controller 四种执行方式 |
| 4. 实现 NodeConfig 控制器 | 1.5人月 | 节点组件管理 |
| 5. 实现 ClusterVersion 控制器 | 2人月 | 版本编排与 DAG 调度 |
| 6. Phase→ComponentVersion 迁移 | 3人月 | 17 个 Phase 的安装逻辑迁移 |
| 7. BKEClusterReconciler 适配 | 1人月 | 控制类逻辑保留，编排逻辑切换 |
| 8. Feature Gate 集成 | 0.5人月 | 双轨运行与切换 |
| 9. E2E 测试 | 2人月 | 安装/升级/扩缩容/回滚全链路测试 |
| **总计** | **14.5人月** | |
## 9. 风险与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 迁移过程中破坏现有集群 | 高 | Feature Gate 渐进切换，双轨运行 |
| DAG 调度死锁 | 高 | 循环依赖检测，超时自动解除 |
| Script 执行安全风险 | 中 | 限制脚本来源，沙箱执行 |
| ComponentVersion 状态不一致 | 中 | 定期全量 Reconcile，状态校验 |
| 升级过程中集群不可用 | 高 | 滚动升级策略，自动回滚 |
| ReleaseImage 与 ComponentVersion 版本不匹配 | 中 | ReleaseImage 控制器验证 |
## 10. 测试计划
| 测试类型 | 范围 | 工具 |
|----------|------|------|
| 单元测试 | 各控制器核心逻辑 | Ginkgo/Gomega |
| 集成测试 | CRD 创建/更新/删除 | envtest |
| E2E 测试 | 安装/升级/扩缩容/回滚全链路 | Kind + 自定义框架 |
| 性能测试 | 控制器 Reconcile 延迟 | pprof + benchmark |
| 混沌测试 | 升级过程中节点故障 | chaos-mesh |
| 兼容性测试 | 从现有 PhaseFrame 迁移 | 双轨对比测试 |
## 11. 未来工作
1. **版本包管理**：基于 ReleaseImage 构建版本包，支持 CI/CD 自动发布
2. **多集群版本管理**：支持多集群统一版本管理
3. **版本灰度发布**：支持按节点池灰度升级
4. **版本合规检查**：基于 ReleaseImage 的合规性检查
5. **版本回溯审计**：完整的版本变更审计日志
```
以上是完整的 KEP 提案。核心设计要点总结：

**四层资源关系**：
- **ClusterVersion**（1）→ **ReleaseImage**（1）：一个集群版本对应一个发布清单
- **ReleaseImage**（1）→ **ComponentVersion**（N）：一个发布清单包含多个组件
- **NodeConfig**（N）→ **ComponentVersion**（N）：多个节点引用多个组件

**关键创新点**：
1. **ReleaseImage 解耦版本定义**：版本清单独立于集群实例，可复用、可验证
2. **ComponentVersion 四种执行方式**：Script/Manifest/Helm/Controller，覆盖所有现有 Phase 的安装逻辑
3. **升级路径显式声明**：`upgradeFrom` 定义从哪个版本到哪个版本的升级动作，支持差异化升级策略
4. **NodeConfig 独立管理节点**：扩缩容与升级解耦，节点配置独立演进

        

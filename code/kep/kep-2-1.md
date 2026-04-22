     
# KEPU-2: 基于 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 的声明式集群版本管理
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-2 |
| **标题** | 声明式集群版本管理：安装、升级与扩缩容 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **依赖** | KEPU-1（整体架构重构） |
## 1. 摘要
本提案设计基于四个 CRD（ClusterVersion、ReleaseImage、ComponentVersion、NodeConfig）及其控制器的声明式集群版本管理方案，替代当前 PhaseFrame 的 20 个命令式 Phase，实现 openFuyao 集群的安装、升级与扩缩容全生命周期管理。

**核心设计**：
- **ClusterVersion** 借鉴 OpenShift CVO，代表整个集群版本概念
- **ReleaseImage** 借鉴 OpenShift Release Image，代表发布版本清单（1:1 对应 ClusterVersion）
- **ComponentVersion** 借鉴 KEPU-1-Phase4，代表组件清单（含安装/升级/卸载/回滚动作定义）
- **NodeConfig** 借鉴 KEPU-1-Phase4，代表节点组件清单及配置

组件安装、升级与扩缩容由 ComponentVersion 控制器根据配置生成脚本、manifest、chart 等完成，**不再调用现有 Phase 代码**。
## 2. 动机
### 2.1 当前架构问题
| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 1 | 命令式编排 | 20 个 Phase 通过 `NeedExecute` + 固定顺序执行 | 无法并行、无法跳过、无法回滚 |
| 2 | 版本状态分散 | `BKEClusterStatus` 仅有 4 个版本字段 | 缺少 Agent、Addon、Provider 等版本追踪 |
| 3 | 升级触发单一 | 仅通过 `OpenFuyaoVersion` 变化触发 | 无法独立升级单个组件 |
| 4 | 缺少回滚能力 | 升级失败后无法自动回滚 | 只能手动修复 |
| 5 | 组件依赖隐式 | Phase 执行顺序硬编码在 list.go | 无法动态调整依赖 |
| 6 | 缺少版本清单 | 无发布版本概念，组件版本散落在 BKECluster Spec | 无法追溯版本组成 |
| 7 | 安装与升级耦合 | 安装 Phase 和升级 Phase 共享状态 | 无法独立管理组件生命周期 |
### 2.2 为什么引入 ReleaseImage
OpenShift CVO 的核心设计是将版本定义与版本编排分离：**ReleaseImage** 是一个不可变的版本清单快照，声明了某个集群版本由哪些组件的哪些版本组成。这带来：
1. **版本可追溯**：每个 ReleaseImage 是一个完整的版本快照，不可变
2. **升级路径明确**：从 ReleaseImage A 到 ReleaseImage B，差异即为升级内容
3. **组件来源清晰**：ReleaseImage 引用 ComponentVersion，组件可独立演进
4. **离线交付友好**：ReleaseImage 可打包为 OCI 镜像，支持离线场景
## 3. 目标
### 3.1 核心目标
1. 定义 ClusterVersion/ReleaseImage/ComponentVersion/NodeConfig 四个 CRD 及其关联关系
2. 实现 ComponentVersion 控制器：根据配置生成脚本/manifest/chart 完成组件安装、升级、卸载
3. 实现 ClusterVersion 控制器：编排升级流程（PreCheck→Upgrade→PostCheck→Rollback）
4. 实现 NodeConfig 控制器：管理节点级组件的安装与扩缩容
5. 将 PhaseFrame 20 个 Phase 重构为声明式架构
### 3.2 非目标
1. 不实现 OS 级别的升级（由 OSProvider 独立负责）
2. 不实现版本包的构建与发布流程（由 CI/CD 独立负责）
3. 不修改现有 BKECluster CRD 的 Spec 定义（仅扩展 Status）
## 4. 范围与约束
### 4.1 范围
| 场景 | 覆盖 | 说明 |
|------|------|------|
| 全新安装 | ✅ | 通过 ComponentVersion DAG 驱动 |
| 滚动升级 | ✅ | ClusterVersion 编排 + ComponentVersion 逐节点执行 |
| 单组件升级 | ✅ | 直接修改 ComponentVersion 版本 |
| 扩容 | ✅ | 新增 NodeConfig 触发组件安装 |
| 缩容 | ✅ | NodeConfig phase=Deleting 触发清理 |
| 回滚 | ✅ | ClusterVersion 根据 History 回滚到旧 ReleaseImage |
| 版本兼容性检查 | ✅ | ComponentVersion 定义的 Compatibility 规则 |
| 离线交付 | ✅ | ReleaseImage 打包为 OCI 镜像 |
### 4.2 约束
1. **不可跳过 ClusterVersion**：所有版本变更必须通过 ClusterVersion 触发，不允许直接操作 ComponentVersion 的版本
2. **ReleaseImage 不可变**：创建后不可修改，确保版本清单的确定性
3. **升级原子性**：单个 ComponentVersion 的升级要么成功要么回滚，不存在中间状态
4. **向后兼容**：Feature Gate 渐进切换，旧 PhaseFlow 可继续运行
## 5. 提案
### 5.1 资源关联关系
```
┌─────────────────────────────────────────────────────────────────┐
│                        资源关联关系                             │
│                                                                 │
│  BKECluster (1) ──→ ClusterVersion (1)                          │
│                          │                                      │
│                          │ spec.releaseRef                      │
│                          ▼                                      │
│                    ReleaseImage (1)                             │
│                          │                                      │
│                          │ spec.componentVersions[]             │
│                          ▼                                      │
│              ComponentVersion (N) ←──┐                          │
│                    │                 │ 引用                     │
│                    │ spec.nodeSelector / spec.scope=Node        │
│                    ▼                 │                          │
│              NodeConfig (M) ────────┘                           │
│                                                                 │
│  关联规则：                                                     │
│  · 1 BKECluster : 1 ClusterVersion : 1 ReleaseImage             │
│  · 1 ReleaseImage : N ComponentVersion                          │
│  · 1 BKECluster : M NodeConfig（每节点一个）                    │
│  · ComponentVersion 通过 nodeSelector 匹配 NodeConfig           │
└─────────────────────────────────────────────────────────────────┘
```
### 5.2 CRD 详细设计
#### 5.2.1 ReleaseImage CRD
```go
// api/cvo/v1beta1/releaseimage_types.go

type ReleaseImageSpec struct {
    // 版本号，语义化版本
    Version string `json:"version"`
    // 集群版本引用（1:1）
    ClusterVersionRef *ClusterVersionReference `json:"clusterVersionRef,omitempty"`
    // 组件版本列表
    ComponentVersions []ComponentVersionRef `json:"componentVersions"`
    // 镜像清单（用于离线导入）
    Images []ImageManifest `json:"images,omitempty"`
    // 升级路径约束
    UpgradePaths []UpgradePath `json:"upgradePaths,omitempty"`
    // 兼容性矩阵
    Compatibility CompatibilityMatrix `json:"compatibility,omitempty"`
}

type ComponentVersionRef struct {
    Name    string `json:"name"`     // 如 bkeAgent, containerd, etcd
    Version string `json:"version"`  // 如 v1.7.2
    // 指向 ComponentVersion CR 的引用
    Ref *ObjectReference `json:"ref,omitempty"`
}

type ImageManifest struct {
    Name       string   `json:"name"`
    Repository string   `json:"repository"`
    Tag        string   `json:"tag"`
    Digest     string   `json:"digest,omitempty"`
    Platforms  []string `json:"platforms,omitempty"`
}

type UpgradePath struct {
    FromVersion string   `json:"fromVersion"`
    ToVersion   string   `json:"toVersion"`
    Direct      bool     `json:"direct,omitempty"`
    Intermediate []string `json:"intermediate,omitempty"`
}

type CompatibilityMatrix struct {
    KubernetesVersions []string `json:"kubernetesVersions,omitempty"`
    OS                []OSRule `json:"os,omitempty"`
    Architectures     []string `json:"architectures,omitempty"`
}

type OSRule struct {
    Type     string   `json:"type"`
    Distros  []string `json:"distros,omitempty"`
    Versions []string `json:"versions,omitempty"`
}

type ReleaseImageStatus struct {
    Phase      ReleaseImagePhase `json:"phase,omitempty"`
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ReleaseImagePhase string

const (
    ReleaseImagePhaseAvailable ReleaseImagePhase = "Available"
    ReleaseImagePhaseInvalid   ReleaseImagePhase = "Invalid"
)
```

**ReleaseImage 示例**：
```yaml
apiVersion: cvo.cluster.x-k8s.io/v1beta1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.6.0
spec:
  version: v2.6.0
  componentVersions:
    - name: bkeAgent
      version: v1.0.0
      ref: { name: bkeagent-v1.0.0, namespace: cluster-system }
    - name: containerd
      version: v1.7.2
      ref: { name: containerd-v1.7.2, namespace: cluster-system }
    - name: etcd
      version: v3.5.12
      ref: { name: etcd-v3.5.12, namespace: cluster-system }
    - name: kubernetes
      version: v1.29.0
      ref: { name: kubernetes-v1.29.0, namespace: cluster-system }
    - name: openFuyao
      version: v2.6.0
      ref: { name: openfuyao-v2.6.0, namespace: cluster-system }
    - name: bkeProvider
      version: v1.1.0
      ref: { name: bkeprovider-v1.1.0, namespace: cluster-system }
    - name: addon
      version: v1.2.0
      ref: { name: addon-v1.2.0, namespace: cluster-system }
    - name: certs
      version: v1.0.0
      ref: { name: certs-v1.0.0, namespace: cluster-system }
    - name: loadBalancer
      version: v1.0.0
      ref: { name: loadbalancer-v1.0.0, namespace: cluster-system }
    - name: clusterAPI
      version: v1.0.0
      ref: { name: clusterapi-v1.0.0, namespace: cluster-system }
    - name: nodesEnv
      version: v1.0.0
      ref: { name: nodesenv-v1.0.0, namespace: cluster-system }
    - name: nodesPostProcess
      version: v1.0.0
      ref: { name: nodespostprocess-v1.0.0, namespace: cluster-system }
    - name: agentSwitch
      version: v1.0.0
      ref: { name: agentswitch-v1.0.0, namespace: cluster-system }
  upgradePaths:
    - fromVersion: v2.5.0
      toVersion: v2.6.0
      direct: true
    - fromVersion: v2.4.0
      toVersion: v2.6.0
      direct: false
      intermediate: ["v2.5.0"]
  compatibility:
    kubernetesVersions: ["v1.28.0", "v1.29.0"]
    os:
      - type: linux
        distros: ["kylin", "centos", "ubuntu"]
    architectures: ["amd64", "arm64"]
```
#### 5.2.2 ClusterVersion CRD
```go
// api/cvo/v1beta1/clusterversion_types.go

type ClusterVersionSpec struct {
    // 期望版本
    DesiredVersion string `json:"desiredVersion"`
    // ReleaseImage 引用
    ReleaseRef *ObjectReference `json:"releaseRef,omitempty"`
    // 集群引用
    ClusterRef *ObjectReference `json:"clusterRef,omitempty"`
    // 升级策略
    UpgradeStrategy UpgradeStrategy `json:"upgradeStrategy,omitempty"`
    // 暂停
    Pause bool `json:"pause,omitempty"`
    // 允许降级
    AllowDowngrade bool `json:"allowDowngrade,omitempty"`
}

type UpgradeStrategy struct {
    Type              UpgradeStrategyType `json:"type,omitempty"`
    MaxUnavailable    int                 `json:"maxUnavailable,omitempty"`
    BatchSize         int                 `json:"batchSize,omitempty"`
    BatchInterval     metav1.Duration     `json:"batchInterval,omitempty"`
    RollbackOnFailure bool                `json:"rollbackOnFailure,omitempty"`
}

type ClusterVersionStatus struct {
    CurrentVersion    string                `json:"currentVersion,omitempty"`
    CurrentReleaseRef *ObjectReference      `json:"currentReleaseRef,omitempty"`
    Phase             ClusterVersionPhase   `json:"phase,omitempty"`
    UpgradeSteps      []UpgradeStep         `json:"upgradeSteps,omitempty"`
    CurrentStepIndex  int                   `json:"currentStepIndex,omitempty"`
    History           []UpgradeHistory      `json:"history,omitempty"`
    Conditions        []metav1.Condition    `json:"conditions,omitempty"`
}

type ClusterVersionPhase string

const (
    CVPhaseAvailable   ClusterVersionPhase = "Available"
    CVPhaseProgressing ClusterVersionPhase = "Progressing"
    CVPhaseDegraded    ClusterVersionPhase = "Degraded"
    CVPhaseRollingBack ClusterVersionPhase = "RollingBack"
)
```
#### 5.2.3 ComponentVersion CRD
```go
// api/nodecomponent/v1alpha1/componentversion_types.go

type ComponentVersionSpec struct {
    ComponentName  ComponentName    `json:"componentName"`
    Version        string           `json:"version"`
    Scope          ComponentScope   `json:"scope,omitempty"`
    Source         *ComponentSource `json:"source,omitempty"`
    InstallAction  *ActionSpec      `json:"installAction,omitempty"`
    UpgradeAction  *ActionSpec      `json:"upgradeAction,omitempty"`
    UninstallAction *ActionSpec     `json:"uninstallAction,omitempty"`
    RollbackAction *ActionSpec      `json:"rollbackAction,omitempty"`
    HealthCheck    *HealthCheckSpec `json:"healthCheck,omitempty"`
    Compatibility  *CompatibilitySpec `json:"compatibility,omitempty"`
    Dependencies   []ComponentName  `json:"dependencies,omitempty"`
    NodeSelector   *NodeSelector    `json:"nodeSelector,omitempty"`
}

type ComponentScope string

const (
    ScopeNode    ComponentScope = "Node"
    ScopeCluster ComponentScope = "Cluster"
)

type ComponentSource struct {
    Type     SourceType      `json:"type"`
    URL      string          `json:"url,omitempty"`
    Checksum string          `json:"checksum,omitempty"`
    Charts   []ChartSource   `json:"charts,omitempty"`
    Images   []ImageSource   `json:"images,omitempty"`
    Scripts  []ScriptSource  `json:"scripts,omitempty"`
    Manifests []ManifestSource `json:"manifests,omitempty"`
}

type ActionSpec struct {
    Type         ActionType       `json:"type"`
    Script       string           `json:"script,omitempty"`
    Manifest     string           `json:"manifest,omitempty"`
    Chart        *ChartSource     `json:"chart,omitempty"`
    Config       string           `json:"config,omitempty"`
    Timeout      *metav1.Duration `json:"timeout,omitempty"`
    NodeSelector *NodeSelector    `json:"nodeSelector,omitempty"`
    PreCheck     *ActionSpec      `json:"preCheck,omitempty"`
    PostCheck    *ActionSpec      `json:"postCheck,omitempty"`
}

type ActionType string

const (
    ActionScript     ActionType = "Script"
    ActionManifest   ActionType = "Manifest"
    ActionChart      ActionType = "Chart"
    ActionController ActionType = "Controller"
)

type ComponentVersionStatus struct {
    Phase            ComponentPhase              `json:"phase,omitempty"`
    InstalledVersion string                      `json:"installedVersion,omitempty"`
    NodeStatuses     map[string]NodeComponentStatus `json:"nodeStatuses,omitempty"`
    LastOperation    *LastOperation              `json:"lastOperation,omitempty"`
    Conditions       []metav1.Condition          `json:"conditions,omitempty"`
}

type ComponentPhase string

const (
    CompPhasePending     ComponentPhase = "Pending"
    CompPhaseInstalling  ComponentPhase = "Installing"
    CompPhaseUpgrading   ComponentPhase = "Upgrading"
    CompPhaseUninstalling ComponentPhase = "Uninstalling"
    CompPhaseReady       ComponentPhase = "Ready"
    CompPhaseFailed      ComponentPhase = "Failed"
    CompPhaseRollingBack ComponentPhase = "RollingBack"
)
```
#### 5.2.4 NodeConfig CRD
```go
// api/nodecomponent/v1alpha1/nodeconfig_types.go

type NodeConfigSpec struct {
    Connection NodeConnection `json:"connection,omitempty"`
    OS         NodeOSInfo     `json:"os,omitempty"`
    Components NodeComponents `json:"components,omitempty"`
    Roles      []NodeRole     `json:"roles,omitempty"`
}

type NodeConnection struct {
    Host         string          `json:"host,omitempty"`
    Port         int             `json:"port,omitempty"`
    SSHKeySecret *SecretReference `json:"sshKeySecret,omitempty"`
    AgentPort    int             `json:"agentPort,omitempty"`
}

type NodeComponents struct {
    Containerd  *ContainerdComponentConfig  `json:"containerd,omitempty"`
    Kubelet     *KubeletComponentConfig     `json:"kubelet,omitempty"`
    Etcd        *EtcdComponentConfig        `json:"etcd,omitempty"`
    BKEAgent    *BKEAgentComponentConfig    `json:"bkeAgent,omitempty"`
    NodesEnv    *NodesEnvComponentConfig    `json:"nodesEnv,omitempty"`
    PostProcess *PostProcessComponentConfig `json:"postProcess,omitempty"`
}

type NodeConfigStatus struct {
    Phase           NodeConfigPhase                    `json:"phase,omitempty"`
    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`
    Conditions      []metav1.Condition                 `json:"conditions,omitempty"`
}

type NodeConfigPhase string

const (
    NCPhasePending    NodeConfigPhase = "Pending"
    NCPhaseInstalling NodeConfigPhase = "Installing"
    NCPhaseUpgrading  NodeConfigPhase = "Upgrading"
    NCPhaseReady      NodeConfigPhase = "Ready"
    NCPhaseFailed     NodeConfigPhase = "Failed"
    NCPhaseDeleting   NodeConfigPhase = "Deleting"
)
```
### 5.3 资源关联与版本解析流程
```
用户修改 ClusterVersion.spec.desiredVersion = "v2.6.0"
    │
    ├── 1. ClusterVersion Controller 查找 ReleaseImage
    │   └── releaseRef → ReleaseImage "openfuyao-v2.6.0"
    │
    ├── 2. 从 ReleaseImage 解析组件版本列表
    │   └── componentVersions[] → 13 个 ComponentVersionRef
    │
    ├── 3. 创建/更新 ComponentVersion CR
    │   ├── bkeagent-v1.0.0   (scope=Node)
    │   ├── containerd-v1.7.2  (scope=Node)
    │   ├── etcd-v3.5.12       (scope=Node)
    │   ├── kubernetes-v1.29.0 (scope=Node)
    │   ├── openfuyao-v2.6.0   (scope=Cluster)
    │   └── ...
    │
    ├── 4. ComponentVersion Controller 执行安装/升级
    │   ├── 查找旧版本：通过 ClusterVersion.status.currentReleaseRef
    │   │   → 旧 ReleaseImage → 旧 ComponentVersion → 执行 uninstallAction
    │   └── 执行新版本：新 ComponentVersion → 执行 installAction/upgradeAction
    │
    └── 5. NodeConfig Controller 协调节点级组件
        └── 根据 ComponentVersion 的 nodeSelector 匹配 NodeConfig
```
### 5.4 组件依赖 DAG
```
安装 DAG：
  BKEAgent → {NodesEnv, ClusterAPI}
  ClusterAPI → Certs
  Certs → LoadBalancer
  NodesEnv + LoadBalancer → {Containerd, Etcd, Kubernetes}
  Kubernetes → Addon
  Addon → NodesPostProcess
  NodesPostProcess → {AgentSwitch, BKEProvider}

升级 DAG：
  BKEProvider → BKEAgent → {Containerd, Etcd} → Kubernetes → OpenFuyao
```
### 5.5 Phase → ComponentVersion 映射表
| Phase | ComponentName | Scope | installAction | upgradeAction | uninstallAction |
|-------|--------------|-------|---------------|---------------|-----------------|
| EnsureBKEAgent | bkeAgent | Node | Script: 推送+启动 | Script: 更新+重启 | Script: 停止+删除 |
| EnsureNodesEnv | nodesEnv | Node | Script: 安装工具 | Script: 更新工具 | Script: 卸载工具 |
| EnsureContainerdUpgrade | containerd | Node | Script: 安装+配置 | Script: 停止→替换→启动 | Script: 停止→清理 |
| EnsureEtcdUpgrade | etcd | Node | Manifest: 静态Pod | Manifest: 替换manifest | Script: 清理数据 |
| EnsureMasterInit/Join/Upgrade | kubernetes | Node | Script: kubeadm init/join | Script: kubeadm upgrade | Script: kubeadm reset |
| EnsureWorkerJoin/Upgrade | kubernetes | Node | Script: kubeadm join | Script: kubeadm upgrade | Script: kubeadm reset |
| EnsureClusterAPIObj | clusterAPI | Cluster | Controller: 创建CR | Controller: 更新CR | Controller: 删除CR |
| EnsureCerts | certs | Cluster | Controller: 生成证书 | Controller: 续期证书 | — |
| EnsureLoadBalance | loadBalancer | Node | Script: 配置HAProxy | Script: 更新HAProxy | Script: 清理HAProxy |
| EnsureAddonDeploy | addon | Cluster | Chart: helm install | Chart: helm upgrade | Chart: helm uninstall |
| EnsureComponentUpgrade | openFuyao | Cluster | Manifest: 部署 | Manifest: patch镜像 | Manifest: 回滚 |
| EnsureProviderSelfUpgrade | bkeProvider | Cluster | Controller: 部署 | Controller: patch镜像 | — |
| EnsureNodesPostProcess | nodesPostProcess | Node | Script: 后处理 | Script: 重新执行 | — |
| EnsureAgentSwitch | agentSwitch | Cluster | Controller: 切换 | — | — |

**控制类 Phase（不映射）**：EnsureFinalizer, EnsurePaused, EnsureClusterManage, EnsureDeleteOrReset, EnsureDryRun → 保留在 BKECluster Controller

**缩容 Phase（通过 NodeConfig 触发）**：EnsureWorkerDelete, EnsureMasterDelete → NodeConfig phase=Deleting
### 5.6 控制器设计
#### 5.6.1 ClusterVersion Controller
```go
func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvo.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if cv.Spec.Pause { return ctrl.Result{}, nil }

    switch cv.Status.Phase {
    case "", CVPhaseAvailable:
        return r.handleAvailable(ctx, cv)    // 检测版本变更
    case CVPhaseProgressing:
        return r.handleProgressing(ctx, cv)  // 执行升级步骤
    case CVPhaseDegraded:
        return r.handleDegraded(ctx, cv)     // 等待人工干预或自动回滚
    case CVPhaseRollingBack:
        return r.handleRollingBack(ctx, cv)  // 执行回滚
    }
    return ctrl.Result{}, nil
}

func (r *ClusterVersionReconciler) handleAvailable(ctx context.Context, cv *cvo.ClusterVersion) (ctrl.Result, error) {
    if cv.Spec.DesiredVersion == cv.Status.CurrentVersion { return ctrl.Result{}, nil }

    // 1. 查找目标 ReleaseImage
    release := &cvo.ReleaseImage{}
    if err := r.Get(ctx, client.ObjectKey{Name: cv.Spec.ReleaseRef.Name}, release); err != nil {
        return ctrl.Result{}, err
    }

    // 2. 版本兼容性校验
    if err := r.validateCompatibility(ctx, cv, release); err != nil {
        r.updateCondition(cv, "ValidationDone", "False", err.Error())
        return ctrl.Result{}, r.Status().Update(ctx, cv)
    }

    // 3. 生成升级步骤
    cv.Status.UpgradeSteps = r.generateUpgradeSteps(cv, release)
    cv.Status.CurrentStepIndex = 0
    cv.Status.Phase = CVPhaseProgressing
    return ctrl.Result{}, r.Status().Update(ctx, cv)
}

func (r *ClusterVersionReconciler) handleProgressing(ctx context.Context, cv *cvo.ClusterVersion) (ctrl.Result, error) {
    step := cv.Status.UpgradeSteps[cv.Status.CurrentStepIndex]

    switch step.Status {
    case StepStatusPending:
        step.Status = StepStatusInProgress
        _ = r.Status().Update(ctx, cv)
        // 创建/更新该步骤对应的 ComponentVersion
        return r.executeUpgradeStep(ctx, cv, step)
    case StepStatusInProgress:
        // 等待 ComponentVersion 达到 Ready
        return r.waitForStepComplete(ctx, cv, step)
    case StepStatusCompleted:
        cv.Status.CurrentStepIndex++
        if cv.Status.CurrentStepIndex >= len(cv.Status.UpgradeSteps) {
            cv.Status.Phase = CVPhaseAvailable
            cv.Status.CurrentVersion = cv.Spec.DesiredVersion
            cv.Status.CurrentReleaseRef = cv.Spec.ReleaseRef
        }
        return ctrl.Result{}, r.Status().Update(ctx, cv)
    case StepStatusFailed:
        if cv.Spec.UpgradeStrategy.RollbackOnFailure {
            cv.Status.Phase = CVPhaseRollingBack
        } else {
            cv.Status.Phase = CVPhaseDegraded
        }
        return ctrl.Result{}, r.Status().Update(ctx, cv)
    }
    return ctrl.Result{}, nil
}
```
#### 5.6.2 ComponentVersion Controller
```go
func (r *ComponentVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &nc.ComponentVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 版本未变更且状态为 Ready，无需处理
    if cv.Status.InstalledVersion == cv.Spec.Version && cv.Status.Phase == CompPhaseReady {
        return ctrl.Result{}, nil
    }

    switch cv.Status.Phase {
    case "", CompPhasePending:
        return r.handleInstall(ctx, cv)
    case CompPhaseInstalling:
        return r.handleInstalling(ctx, cv)
    case CompPhaseUpgrading:
        return r.handleUpgrading(ctx, cv)
    case CompPhaseUninstalling:
        return r.handleUninstalling(ctx, cv)
    case CompPhaseFailed:
        return r.handleFailed(ctx, cv)
    case CompPhaseRollingBack:
        return r.handleRollingBack(ctx, cv)
    }
    return ctrl.Result{}, nil
}

func (r *ComponentVersionReconciler) handleInstall(ctx context.Context, cv *nc.ComponentVersion) (ctrl.Result, error) {
    // 检查是否有旧版本需要卸载
    oldCV, err := r.findOldComponentVersion(ctx, cv)
    if err == nil && oldCV != nil {
        // 先卸载旧版本
        cv.Status.Phase = CompPhaseUninstalling
        _ = r.Status().Update(ctx, cv)
        return r.executeUninstall(ctx, oldCV, cv)
    }

    // 执行安装
    cv.Status.Phase = CompPhaseInstalling
    _ = r.Status().Update(ctx, cv)
    return r.executeAction(ctx, cv, cv.Spec.InstallAction)
}

func (r *ComponentVersionReconciler) findOldComponentVersion(
    ctx context.Context, cv *nc.ComponentVersion,
) (*nc.ComponentVersion, error) {
    // 通过 ClusterVersion 找到旧 ReleaseImage
    clusterVersion := r.getOwnerClusterVersion(ctx, cv)
    if clusterVersion == nil || clusterVersion.Status.CurrentReleaseRef == nil {
        return nil, nil
    }

    oldRelease := &cvo.ReleaseImage{}
    if err := r.Get(ctx, client.ObjectKey{
        Name: clusterVersion.Status.CurrentReleaseRef.Name,
    }, oldRelease); err != nil {
        return nil, nil
    }

    // 从旧 ReleaseImage 中找到同名组件的 ComponentVersion
    for _, ref := range oldRelease.Spec.ComponentVersions {
        if ref.Name == string(cv.Spec.ComponentName) {
            oldCV := &nc.ComponentVersion{}
            if err := r.Get(ctx, client.ObjectKey{Name: ref.Ref.Name}, oldCV); err != nil {
                return nil, nil
            }
            return oldCV, nil
        }
    }
    return nil, nil
}

func (r *ComponentVersionReconciler) executeAction(
    ctx context.Context, cv *nc.ComponentVersion, action *nc.ActionSpec,
) (ctrl.Result, error) {
    if action == nil { return ctrl.Result{}, nil }

    // 获取匹配的 NodeConfig 列表
    nodeConfigs := r.getMatchingNodeConfigs(ctx, cv)

    switch action.Type {
    case nc.ActionScript:
        return r.executeScript(ctx, cv, action, nodeConfigs)
    case nc.ActionManifest:
        return r.executeManifest(ctx, cv, action, nodeConfigs)
    case nc.ActionChart:
        return r.executeChart(ctx, cv, action, nodeConfigs)
    case nc.ActionController:
        return r.executeControllerAction(ctx, cv, action)
    }
    return ctrl.Result{}, nil
}
```
#### 5.6.3 NodeConfig Controller
```go
func (r *NodeConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    nc := &v1alpha1.NodeConfig{}
    if err := r.Get(ctx, req.NamespacedName, nc); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    switch nc.Status.Phase {
    case "", NCPhasePending:
        return r.handleInstall(ctx, nc)
    case NCPhaseInstalling, NCPhaseUpgrading:
        return r.handleComponentProgress(ctx, nc)
    case NCPhaseDeleting:
        return r.handleDelete(ctx, nc)
    }
    return ctrl.Result{}, nil
}
```
### 5.7 升级卸载旧组件的关键流程
```
ComponentVersion Controller 升级流程：
1. 检测版本变更（spec.version != status.installedVersion）

2. 查找旧版本 ComponentVersion：
   ClusterVersion.status.currentReleaseRef
     → 旧 ReleaseImage
       → spec.componentVersions[name=当前组件名]
         → 旧 ComponentVersion CR

3. 执行旧版本 uninstallAction：
   - containerd: systemctl stop/disable + rm 二进制和配置
   - etcd: 删除静态 Pod manifest + rm 数据目录
   - kubernetes: kubeadm reset
   - addon: helm uninstall

4. 执行新版本 installAction 或 upgradeAction：
   - containerd: 安装新二进制 + 配置 config.toml + systemctl enable/restart
   - etcd: 生成新 manifest + systemctl restart kubelet
   - kubernetes: kubeadm upgrade
   - addon: helm upgrade

5. 健康检查：
   - containerd: systemctl is-active containerd
   - etcd: etcdctl endpoint health
   - kubernetes: Pod Ready + 节点 Ready

6. 更新状态：
   status.installedVersion = spec.version
   status.phase = Ready
```
### 5.8 扩缩容流程
**扩容**：
```
用户在 BKECluster.Spec.Nodes 添加节点
    → ClusterOrchestrator 创建 NodeConfig (phase=Pending)
    → NodeConfig Controller 检测到新节点
    → 查找已 Ready 的 ComponentVersion（当前版本）
    → 对新节点执行 installAction
    → 更新 NodeConfig.Status.ComponentStatus
```
**缩容**：
```
用户从 BKECluster.Spec.Nodes 移除节点
    → ClusterOrchestrator 设置 NodeConfig phase=Deleting
    → NodeConfig Controller 执行缩容
    → Step 1: kubectl drain 节点
    → Step 2: 删除 Machine 对象
    → Step 3: 等待节点从集群移除
    → Step 4: 删除 NodeConfig CR
```
## 6. 迁移策略
### 6.1 Feature Gate 渐进切换
| 阶段 | Feature Gate | 行为 |
|------|-------------|------|
| Phase 1 | `DeclarativeOrchestration=false` | CRD 可创建，Controller 可启动，不影响现有 PhaseFlow |
| Phase 2 | `DeclarativeOrchestration=true`（可选） | 新声明式路径生效，对比验证 |
| Phase 3 | `DeclarativeOrchestration=true`（默认） | 全量切换，E2E 验证 |
| Phase 4 | 不可逆 | 移除旧 Phase 代码 |
### 6.2 兼容性保证
```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // ... 控制类逻辑 ...

    if featuregate.Enabled(featuregate.DeclarativeOrchestration) {
        return r.clusterVersionOrchestrator.Reconcile(ctx, bkeCluster)
    }
    return r.phaseFlow.Execute(ctx, bkeCluster) // 降级到旧路径
}
```
## 7. 目录结构
```
cluster-api-provider-bke/
├── api/
│   ├── cvo/v1beta1/                     # 新增
│   │   ├── clusterversion_types.go
│   │   ├── releaseimage_types.go
│   │   └── zz_generated.deepcopy.go
│   └── nodecomponent/v1alpha1/          # 新增
│       ├── componentversion_types.go
│       ├── nodeconfig_types.go
│       └── zz_generated.deepcopy.go
├── controllers/
│   ├── cvo/                             # 新增
│   │   ├── clusterversion_controller.go
│   │   └── releaseimage_controller.go
│   └── nodecomponent/                   # 新增
│       ├── componentversion_controller.go
│       └── nodeconfig_controller.go
├── pkg/
│   ├── cvo/                             # 新增
│   │   ├── orchestrator.go              # 升级编排
│   │   ├── validator.go                 # 版本校验
│   │   ├── rollback.go                  # 回滚管理
│   │   └── dag_scheduler.go             # DAG 调度
│   ├── executor/                        # 新增
│   │   ├── interface.go
│   │   ├── script_executor.go
│   │   ├── manifest_executor.go
│   │   ├── chart_executor.go
│   │   └── controller_executor.go
│   └── phaseframe/                      # 保留，逐步废弃
```
## 8. 工作量评估
| 步骤 | 内容 | 工作量 |
|------|------|--------|
| 第一步 | CRD 定义（4 个）+ ClusterVersion/ReleaseImage Controller + DAGScheduler | 10 人天 |
| 第二步 | ComponentVersion Controller + 4 种 Executor + NodeConfig Controller | 12 人天 |
| 第三步 | 13 个组件的 Action 定义（脚本/manifest/chart）+ 安装 E2E | 10 人天 |
| 第四步 | 升级全链路（PreCheck→Upgrade→PostCheck→Rollback）+ 扩缩容 | 10 人天 |
| 测试 | 单元测试 + 集成测试 + E2E + 新旧路径对比 | 8 人天 |
| **总计** | | **50 人天** |
## 9. 风险评估
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| DAG 调度死锁 | 高 | 超时机制 + 循环依赖检测 + 手动跳过 |
| 升级中部分节点失败 | 高 | 批次控制 + 暂停机制 + 回滚机制 |
| 旧版本 ComponentVersion 找不到 | 中 | ReleaseImage 必须保留历史版本；降级为直接安装 |
| Feature Gate 切换状态不一致 | 中 | 双写验证 + 灰度切换 |
| 脚本/manifest 执行失败 | 中 | 重试机制 + 失败详情上报 + 人工干预入口 |
## 10. 验收标准
1. **CRD 验收**：4 个 CRD 可创建，关联关系正确，ReleaseImage 不可变
2. **安装验收**：从零创建集群，ComponentVersion DAG 驱动安装，所有组件安装成功
3. **升级验收**：修改 ClusterVersion 版本，触发 PreCheck→Upgrade→PostCheck 全链路，支持回滚
4. **单组件升级验收**：直接修改 ComponentVersion 版本，仅升级该组件
5. **扩缩容验收**：添加/移除节点，NodeConfig 自动创建/删除，组件正确安装/清理
6. **旧版本卸载验收**：升级时自动查找旧 ReleaseImage → 旧 ComponentVersion → 执行 uninstallAction
7. **兼容性验收**：Feature Gate 关闭时旧 PhaseFlow 正常运行；开启时新声明式路径正常运行
        

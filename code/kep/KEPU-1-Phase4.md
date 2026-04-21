# KEPU-4: PhaseFrame 声明式重构及 CVO 升级机制
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-4 |
| **标题** | PhaseFrame 声明式重构及 CVO 升级机制 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-20 |
| **最后更新** | 2026-04-20 |
| **依赖** | KEPU-1（整体架构重构） |
## 1. 摘要
本提案将当前 PhaseFrame 中 20 个 Phase 重构为 ComponentVersion/NodeConfig 声明式架构，并在此基础上实现 CVO（Cluster Version Operator）升级机制，覆盖安装、升级与扩缩容全生命周期。重构分三步递进：**第一步**重构 PhaseFrame 各 Phase 为 ComponentVersion 声明式架构；**第二步**重构 Phase 安装逻辑为 ComponentVersion Executor；**第三步**实现阶段四的 CVO 升级机制（安装+升级+扩缩容）。
## 2. 动机
### 2.1 当前架构问题
当前 [phaseframe/phases/](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases) 中 20 个 Phase 存在以下核心问题：

| 问题 | 现状 | 影响 |
|------|------|------|
| **命令式编排** | 各 Phase 通过 `NeedExecute` + 固定顺序执行 | 无法并行、无法跳过、无法回滚 |
| **版本状态分散** | `BKEClusterStatus` 仅有 `OpenFuyaoVersion`、`KubernetesVersion`、`EtcdVersion`、`ContainerdVersion` | 缺少 Agent、Addon 等版本，无法追踪组件级状态 |
| **升级触发单一** | 仅通过 `OpenFuyaoVersion` 变化触发 | 无法独立升级单个组件 |
| **缺少回滚能力** | 升级失败后无法自动回滚 | 只能手动修复 |
| **节点状态缺失** | 无法追踪每个节点上每个组件的安装版本 | 升级编排缺少节点级上下文 |
| **扩缩容与升级耦合** | EnsureWorkerDelete/EnsureMasterDelete 与升级 Phase 混在一起 | 缩容逻辑无法独立演进 |
### 2.2 与 KEPU-1 其他阶段的依赖分析
| 依赖项 | 是否必须 | 说明 |
|--------|---------|------|
| 阶段一（Infrastructure Provider 抽象 + UPI） | **不依赖** | ComponentVersion/NodeConfig 是独立 CRD，不依赖 BKECluster Spec 扩展 |
| 阶段二（Bootstrap + ControlPlane Provider 分离） | **不依赖** | ComponentVersion Executor 可复用现有 Phase 的核心函数，不依赖 Provider 分离 |
| 阶段三（状态机重构 Phase Flow Engine） | **不依赖** | 本提案替代状态机方案，用声明式 CRD 编排替代状态机驱动 |
| 阶段五（OSProvider 接口） | **不依赖** | NodeConfig 包含 OS 信息但不执行 OS 操作，OS 抽象是独立职责 |
| 阶段六（版本包管理 + Asset） | **不依赖** | 版本包管理是更高层抽象，本提案可独立工作 |
| 阶段七（统一错误处理） | **部分依赖** | 复用错误类型定义，但可自行定义最小错误类型 |

**结论：本提案可独立实现，与 KEPU-1 其他阶段无硬依赖。**
### 2.3 目标
1. 将 PhaseFrame 20 个 Phase 重构为 ComponentVersion/NodeConfig 声明式架构
2. 实现声明式安装：通过 CRD 声明期望状态，Controller 自动驱动安装
3. 实现声明式升级：通过 CRD 版本变更触发升级，支持 PreCheck→Upgrade→PostCheck→Rollback 全链路
4. 实现声明式扩缩容：通过 NodeConfig 增删触发扩缩容，与升级解耦
5. 保持与现有 PhaseFrame 的兼容性，支持 Feature Gate 渐进切换
## 3. 提案
### 3.1 总体架构
```
重构前：
BKEClusterReconciler → PhaseFlow → 20 Phases（命令式顺序执行）

重构后：
BKEClusterReconciler
    ├── 控制类逻辑（保留在 Controller 中）
    │   ├── EnsureFinalizer
    │   ├── EnsurePaused
    │   ├── EnsureClusterManage
    │   ├── EnsureDeleteOrReset
    │   └── EnsureDryRun
    │
    └── ClusterOrchestrator（声明式编排）
        ├── syncDesiredState() → 生成 ComponentVersion[] + NodeConfig[]
        ├── DAGScheduler → 计算依赖、调度执行顺序
        └── ComponentExecutor[] → 执行安装/升级/回滚
            ├── BKEAgentExecutor
            ├── NodesEnvExecutor
            ├── ClusterAPIExecutor
            ├── CertsExecutor
            ├── LoadBalancerExecutor
            ├── KubernetesExecutor（含 Init/Join/Upgrade 步骤状态机）
            ├── ContainerdExecutor
            ├── EtcdExecutor
            ├── AddonExecutor
            ├── OpenFuyaoExecutor
            ├── BKEProviderExecutor
            ├── NodesPostProcessExecutor
            └── AgentSwitchExecutor
```
### 3.2 Phase 分类与重构策略
| 分类 | Phase | 操作性质 | 重构策略 |
|------|-------|----------|----------|
| **控制类** | EnsureFinalizer, EnsurePaused, EnsureClusterManage, EnsureDeleteOrReset, EnsureDryRun | 集群生命周期控制 | 保留在 BKECluster Controller 中，不映射为 ComponentVersion |
| **节点级组件安装** | EnsureBKEAgent, EnsureNodesEnv, EnsureNodesPostProcess, EnsureContainerdUpgrade | 在节点上安装/升级软件 | 映射为 NodeConfig + ComponentVersion（Scope=Node） |
| **集群级组件安装** | EnsureClusterAPIObj, EnsureCerts, EnsureLoadBalance, EnsureMasterInit, EnsureMasterJoin, EnsureWorkerJoin, EnsureAddonDeploy, EnsureAgentSwitch | 创建/配置集群资源 | 映射为 ComponentVersion（Scope=Cluster） |
| **升级** | EnsureProviderSelfUpgrade, EnsureAgentUpgrade, EnsureEtcdUpgrade, EnsureWorkerUpgrade, EnsureMasterUpgrade, EnsureComponentUpgrade | 版本变更 | 映射为 ComponentVersion 的 upgradeAction |
| **缩容** | EnsureWorkerDelete, EnsureMasterDelete | 节点增删 | 通过 NodeConfig phase=Deleting 触发，不映射为 ComponentVersion |
| **健康检查** | EnsureCluster | 集群健康检查 | 作为 ClusterVersion 的全局健康检查步骤 |
### 3.3 新增 CRD
#### 3.3.1 ComponentVersion CRD
```go
// api/nodecomponent/v1alpha1/componentversion_types.go

type ComponentVersionSpec struct {
    ComponentName  ComponentName   `json:"componentName"`
    Version        string          `json:"version"`
    Source         *ComponentSource `json:"source,omitempty"`
    InstallAction  *ActionSpec     `json:"installAction,omitempty"`
    UpgradeAction  *ActionSpec     `json:"upgradeAction,omitempty"`
    RollbackAction *ActionSpec     `json:"rollbackAction,omitempty"`
    HealthCheck    *HealthCheckSpec `json:"healthCheck,omitempty"`
    Compatibility  *CompatibilitySpec `json:"compatibility,omitempty"`
    UpgradePath    *UpgradePathSpec `json:"upgradePath,omitempty"`
    Scope          ComponentScope  `json:"scope,omitempty"`
}

type ComponentVersionStatus struct {
    Phase            ComponentPhase              `json:"phase,omitempty"`
    InstalledVersion string                      `json:"installedVersion,omitempty"`
    Message          string                      `json:"message,omitempty"`
    NodeStatuses     map[string]NodeComponentStatus `json:"nodeStatuses,omitempty"`
    LastOperation    *LastOperation              `json:"lastOperation,omitempty"`
    Conditions       []metav1.Condition          `json:"conditions,omitempty"`
}
```
**ComponentName 枚举**：

| ComponentName | Scope | 映射的 Phase |
|---------------|-------|-------------|
| `bkeAgent` | Node | EnsureBKEAgent, EnsureAgentUpgrade |
| `nodesEnv` | Node | EnsureNodesEnv |
| `containerd` | Node | EnsureContainerdUpgrade |
| `etcd` | Node | EnsureEtcdUpgrade |
| `kubernetes` | Node | EnsureMasterInit, EnsureMasterJoin, EnsureWorkerJoin, EnsureMasterUpgrade, EnsureWorkerUpgrade |
| `clusterAPI` | Cluster | EnsureClusterAPIObj |
| `certs` | Cluster | EnsureCerts |
| `loadBalancer` | Node | EnsureLoadBalance |
| `addon` | Cluster | EnsureAddonDeploy |
| `openFuyao` | Cluster | EnsureComponentUpgrade |
| `bkeProvider` | Cluster | EnsureProviderSelfUpgrade |
| `nodesPostProcess` | Node | EnsureNodesPostProcess |
| `agentSwitch` | Cluster | EnsureAgentSwitch |
#### 3.3.2 NodeConfig CRD
```go
// api/nodecomponent/v1alpha1/nodeconfig_types.go

type NodeConfigSpec struct {
    Connection NodeConnection `json:"connection,omitempty"`
    OS         NodeOSInfo     `json:"os,omitempty"`
    Components NodeComponents `json:"components,omitempty"`
    Roles      []NodeRole     `json:"roles,omitempty"`
}

type NodeConfigStatus struct {
    Phase           NodeConfigPhase                     `json:"phase,omitempty"`
    ComponentStatus map[string]NodeComponentDetailStatus `json:"componentStatus,omitempty"`
    OSInfo          *NodeOSDetailInfo                   `json:"osInfo,omitempty"`
    LastOperation   *LastOperation                      `json:"lastOperation,omitempty"`
    Conditions      []metav1.Condition                  `json:"conditions,omitempty"`
}
```
#### 3.3.3 ClusterVersion CRD
```go
// api/cvo/v1beta1/clusterversion_types.go

type ClusterVersionSpec struct {
    DesiredVersion          string             `json:"desiredVersion"`
    DesiredComponentVersions ComponentVersions `json:"desiredComponentVersions,omitempty"`
    ClusterRef              *ClusterReference  `json:"clusterRef,omitempty"`
    UpgradeStrategy         UpgradeStrategy    `json:"upgradeStrategy,omitempty"`
    Pause                   bool               `json:"pause,omitempty"`
    AllowDowngrade          bool               `json:"allowDowngrade,omitempty"`
}

type ClusterVersionStatus struct {
    CurrentVersion    string                `json:"currentVersion,omitempty"`
    CurrentComponents ComponentVersions     `json:"currentComponents,omitempty"`
    Phase             ClusterVersionPhase   `json:"phase,omitempty"`
    UpgradeSteps      []UpgradeStep         `json:"upgradeSteps,omitempty"`
    CurrentStepIndex  int                   `json:"currentStepIndex,omitempty"`
    History           []UpgradeHistory      `json:"history,omitempty"`
    Conditions        []metav1.Condition    `json:"conditions,omitempty"`
}
```
### 3.4 组件依赖 DAG
```
安装阶段 DAG：
                    ┌─────────────────┐
                    │  BKEAgent       │ ← 最先安装
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───────┐      │     ┌────────▼───────┐
     │   NodesEnv     │      │     │  ClusterAPI    │
     └────────┬───────┘      │     └────────┬───────┘
              │              │              │
              │              │     ┌────────▼───────┐
              │              │     │    Certs       │
              │              │     └────────┬───────┘
              │              │              │
              │     ┌────────▼──────────────▼───────┐
              │     │       LoadBalancer            │
              │     └────────┬──────────────────────┘
              │              │
              │     ┌────────▼──────────────────────┐
              │     │    Kubernetes (Init/Join)     │
              │     └────────┬──────────────────────┘
              │              │
              │     ┌────────▼───────┐
              │     │     Addon      │
              │     └────────┬───────┘
              │              │
              │     ┌────────▼──────────┐
              │     │  NodesPostProcess │
              │     └────────┬──────────┘
              │              │
              └──────┬───────┘──────────────┐
                     │                      │
           ┌─────────▼──────────┐  ┌────────▼───────┐
           │   AgentSwitch      │  │  BKEProvider   │
           └────────────────────┘  └────────────────┘

升级阶段 DAG：
  BKEProvider → BKEAgent → Containerd → Etcd → Kubernetes(Master→Worker) → OpenFuyao
```
## 4. 第一步：PhaseFrame 各 Phase 重构为 ComponentVersion 声明式架构
### 4.1 目标
将 [phaseframe/phases/](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases) 中 20 个 Phase 的**定义**重构为 ComponentVersion/NodeConfig CRD 声明式配置，建立 Phase→ComponentVersion 的映射关系。
### 4.2 Phase→ComponentVersion 映射表
#### 4.2.1 节点级组件（Scope=Node）
| Phase | ComponentVersion | NodeConfig 字段 | installAction | upgradeAction |
|-------|-----------------|----------------|---------------|---------------|
| EnsureBKEAgent | `bkeAgent-v1.0.0` | `components.bkeAgent` | Script: 推送 Agent 二进制 + 配置 kubeconfig + 启动服务 | Script: 更新 Agent 二进制 + 重启服务 |
| EnsureNodesEnv | `nodesEnv-v1.0.0` | `components.nodesEnv` | Script: 安装 lxcfs/nfs-utils/etcdctl/helm/calicoctl/runc | Script: 更新工具版本 |
| EnsureNodesPostProcess | `nodesPostProcess-v1.0.0` | `components.postProcess` | Script: 执行后处理脚本 | Script: 重新执行后处理脚本 |
| EnsureContainerdUpgrade | `containerd-v1.7.2` | `components.containerd` | Script: 安装 containerd + 配置 config.toml | Script: 停止→备份→替换→启动→验证 |
| EnsureEtcdUpgrade | `etcd-v3.5.12` | `components.etcd` | （随 Kubernetes Init 安装） | Script: 逐节点停止→备份→替换→启动→健康检查 |
| EnsureMasterInit | `kubernetes-v1.29.0` | `components.kubelet` | Script: kubeadm init | （升级路径） |
| EnsureMasterJoin | `kubernetes-v1.29.0` | `components.kubelet` | Script: kubeadm join --control-plane | （升级路径） |
| EnsureWorkerJoin | `kubernetes-v1.29.0` | `components.kubelet` | Script: kubeadm join | （升级路径） |
| EnsureMasterUpgrade | `kubernetes-v1.29.0` | `components.kubelet` | （安装路径） | Script: 逐节点 kubeadm upgrade |
| EnsureWorkerUpgrade | `kubernetes-v1.29.0` | `components.kubelet` | （安装路径） | Script: 逐节点 kubeadm upgrade |
| EnsureLoadBalance | `loadBalancer-v1.0.0` | （无，通过 Labels 选择） | Script: 配置 HAProxy | Script: 更新 HAProxy 配置 |
#### 4.2.2 集群级组件（Scope=Cluster）
| Phase | ComponentVersion | installAction | upgradeAction |
|-------|-----------------|---------------|---------------|
| EnsureClusterAPIObj | `clusterAPI-v1.0.0` | Controller: 创建 Cluster/Machine/KubeadmControlPlane | Controller: 更新 Machine 副本数 |
| EnsureCerts | `certs-v1.0.0` | Controller: 生成 CA/etcd-ca/front-proxy-ca/SA 证书 | Controller: 续期证书 |
| EnsureAddonDeploy | `addon-v1.0.0` | Manifest: Helm install 各 Addon | Manifest: Helm upgrade 各 Addon |
| EnsureAgentSwitch | `agentSwitch-v1.0.0` | Controller: 切换 Agent kubeconfig 指向目标集群 | （无升级路径） |
| EnsureProviderSelfUpgrade | `bkeProvider-v1.1.0` | Controller: 部署 Provider Deployment | Controller: Patch Deployment 镜像 tag |
| EnsureComponentUpgrade | `openFuyao-v1.1.0` | Manifest: 部署 openFuyao 组件 | Manifest: Patch ConfigMap 中的镜像 tag |
#### 4.2.3 控制类 Phase（不映射为 ComponentVersion）
| Phase | 处理方式 |
|-------|---------|
| EnsureFinalizer | 保留在 BKECluster Controller 的 Reconcile 入口 |
| EnsurePaused | 保留在 BKECluster Controller 的 Reconcile 入口 |
| EnsureClusterManage | 保留在 BKECluster Controller 的 Reconcile 入口 |
| EnsureDeleteOrReset | 保留在 BKECluster Controller 的 Reconcile 入口 |
| EnsureDryRun | 保留在 BKECluster Controller 的 Reconcile 入口 |
#### 4.2.4 缩容 Phase（通过 NodeConfig 触发）
| Phase | 处理方式 |
|-------|---------|
| EnsureWorkerDelete | NodeConfig phase=Deleting + NodeRole=worker |
| EnsureMasterDelete | NodeConfig phase=Deleting + NodeRole=master |
#### 4.2.5 健康检查 Phase
| Phase | 处理方式 |
|-------|---------|
| EnsureCluster | ClusterVersion 的全局健康检查步骤 |
### 4.3 ClusterOrchestrator 设计
```go
// pkg/orchestrator/cluster_orchestrator.go

type ClusterOrchestrator struct {
    client    client.Client
    scheme    *runtime.Scheme
    scheduler *DAGScheduler
    executors map[ComponentName]ComponentExecutor
}

func (o *ClusterOrchestrator) Reconcile(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    if err := o.syncDesiredState(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }

    components, err := o.listComponentVersions(ctx, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }

    completed := o.getCompletedComponents(components)
    scheduleResult := o.scheduler.Schedule(components, completed)

    for _, comp := range scheduleResult.Ready {
        executor := o.executors[comp.Spec.ComponentName]
        result, err := o.executeComponent(ctx, executor, comp)
        if err != nil {
            return result, err
        }
    }

    if err := o.syncStatusToBKECluster(ctx, bkeCluster, components); err != nil {
        return ctrl.Result{}, err
    }

    if len(scheduleResult.Ready) == 0 && len(scheduleResult.Blocked) == 0 && len(scheduleResult.Running) == 0 {
        return ctrl.Result{}, nil
    }

    return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
```
### 4.4 DAGScheduler 设计
```go
// pkg/orchestrator/dag_scheduler.go

var InstallDependencyGraph = map[ComponentName][]ComponentName{
    ComponentBKEAgent:      {},
    ComponentNodesEnv:      {ComponentBKEAgent},
    ComponentClusterAPI:    {ComponentBKEAgent},
    ComponentCerts:         {ComponentClusterAPI},
    ComponentLoadBalancer:  {ComponentCerts},
    ComponentKubernetes:    {ComponentNodesEnv, ComponentLoadBalancer},
    ComponentContainerd:    {ComponentNodesEnv},
    ComponentEtcd:          {ComponentNodesEnv},
    ComponentAddon:         {ComponentKubernetes},
    ComponentNodesPostProc: {ComponentAddon},
    ComponentAgentSwitch:   {ComponentNodesPostProc},
    ComponentBKEProvider:   {ComponentNodesPostProc},
}

var UpgradeDependencyGraph = map[ComponentName][]ComponentName{
    ComponentBKEProvider:   {},
    ComponentBKEAgent:      {ComponentBKEProvider},
    ComponentContainerd:    {ComponentBKEAgent},
    ComponentEtcd:          {ComponentBKEAgent},
    ComponentKubernetes:    {ComponentContainerd, ComponentEtcd},
    ComponentOpenFuyao:     {ComponentKubernetes},
}

type ScheduleResult struct {
    Ready    []*v1alpha1.ComponentVersion
    Blocked  []*v1alpha1.ComponentVersion
    Running  []*v1alpha1.ComponentVersion
}

func (s *DAGScheduler) Schedule(
    components []*v1alpha1.ComponentVersion,
    completed map[ComponentName]bool,
) *ScheduleResult {
    // 拓扑排序 + 依赖检查
}
```
### 4.5 重构后的 BKECluster Controller
```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bkeCluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, bkeCluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // === 控制类逻辑（保留在 Controller 中） ===
    if !controllerutil.ContainsFinalizer(bkeCluster, bkev1beta1.ClusterFinalizer) {
        controllerutil.AddFinalizer(bkeCluster, bkev1beta1.ClusterFinalizer)
        if err := r.Update(ctx, bkeCluster); err != nil {
            return ctrl.Result{}, err
        }
    }

    if bkeCluster.Spec.Pause {
        return ctrl.Result{}, nil
    }

    if bkeCluster.Spec.DryRun {
        return r.handleDryRun(ctx, bkeCluster)
    }

    if bkeCluster.Spec.Reset || !bkeCluster.DeletionTimestamp.IsZero() {
        return r.handleDeleteOrReset(ctx, bkeCluster)
    }

    if err := r.handleClusterManage(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }

    // === 声明式组件编排 ===
    return r.orchestrator.Reconcile(ctx, bkeCluster)
}
```
## 5. 第二步：Phase 安装逻辑重构为 ComponentVersion Executor
### 5.1 目标
将各 Phase 的**核心执行逻辑**提取为 ComponentExecutor 实现，内部复用现有 Phase 的核心函数，实现从命令式 Phase 到声明式 Executor 的迁移。
### 5.2 ComponentExecutor 接口
```go
// pkg/orchestrator/executor/interface.go

type ComponentExecutor interface {
    Name() ComponentName
    Scope() ComponentScope
    Install(ctx context.Context, cv *v1alpha1.ComponentVersion, nodeConfigs []*v1alpha1.NodeConfig) (ctrl.Result, error)
    Upgrade(ctx context.Context, cv *v1alpha1.ComponentVersion, nodeConfigs []*v1alpha1.NodeConfig) (ctrl.Result, error)
    Rollback(ctx context.Context, cv *v1alpha1.ComponentVersion, nodeConfigs []*v1alpha1.NodeConfig) error
    HealthCheck(ctx context.Context, cv *v1alpha1.ComponentVersion, nodeConfigs []*v1alpha1.NodeConfig) (bool, error)
}
```
### 5.3 各 Executor 与现有 Phase 的复用关系
| Executor | 复用的现有 Phase 核心函数 | 新增逻辑 |
|----------|--------------------------|---------|
| BKEAgentExecutor | `ensure_bke_agent.go` 中的 SSH 推送逻辑 | 从 NodeConfig 获取连接信息，从 ComponentVersion 获取版本和脚本 |
| NodesEnvExecutor | `ensure_nodes_env.go` 中的环境初始化逻辑 | 从 NodeConfig 获取软件源配置 |
| ClusterAPIExecutor | `ensure_cluster_api_obj.go` 中的 CR 创建逻辑 | 从 ComponentVersion 获取超时配置 |
| CertsExecutor | `ensure_certs.go` 中的证书生成逻辑 | 从 ComponentVersion 获取证书有效期配置 |
| LoadBalancerExecutor | `ensure_load_balance.go` 中的 HAProxy 配置逻辑 | 从 ComponentVersion 获取 HAProxy 配置模板 |
| KubernetesExecutor | `ensure_master_init.go` + `ensure_master_join.go` + `ensure_worker_join.go` + `ensure_master_upgrade.go` + `ensure_worker_upgrade.go` | 内部步骤状态机（Init→Join→Upgrade） |
| ContainerdExecutor | `ensure_containerd_upgrade.go` 中的升级逻辑 | 从 NodeConfig 获取 containerd 配置 |
| EtcdExecutor | `ensure_etcd_upgrade.go` 中的升级逻辑 | 从 NodeConfig 获取 etcd 数据目录和 URL |
| AddonExecutor | `ensure_addon_deploy.go` 中的 Helm 部署逻辑 | 从 ComponentVersion 获取 Chart 源和 Values |
| OpenFuyaoExecutor | `ensure_component_upgrade.go` 中的 Patch 逻辑 | 从 ComponentVersion 获取镜像列表 |
| BKEProviderExecutor | `ensure_provider_self_upgrade.go` 中的 Patch 逻辑 | 从 ComponentVersion 获取镜像 tag |
| NodesPostProcessExecutor | `ensure_nodes_postprocess.go` 中的后处理逻辑 | 从 NodeConfig 获取脚本列表 |
| AgentSwitchExecutor | `ensure_agent_switch.go` 中的切换逻辑 | 从 ComponentVersion 获取 kubeconfig 模板 |
### 5.4 KubernetesExecutor 步骤状态机
Kubernetes 组件是最复杂的组件，合并了 5 个 Phase，内部通过步骤状态机管理：
```go
// pkg/orchestrator/executor/kubernetes_executor.go

type K8sStep string

const (
    K8sStepMasterInit       K8sStep = "MasterInit"
    K8sStepMasterJoin       K8sStep = "MasterJoin"
    K8sStepWorkerJoin       K8sStep = "WorkerJoin"
    K8sStepUpgradePreCheck  K8sStep = "UpgradePreCheck"
    K8sStepUpgradeBackup    K8sStep = "UpgradeBackup"
    K8sStepUpgradeMaster    K8sStep = "UpgradeMaster"
    K8sStepUpgradeWorker    K8sStep = "UpgradeWorker"
    K8sStepUpgradePostCheck K8sStep = "UpgradePostCheck"
)

func (e *KubernetesExecutor) Install(
    ctx context.Context,
    cv *v1alpha1.ComponentVersion,
    nodeConfigs []*v1alpha1.NodeConfig,
) (ctrl.Result, error) {
    currentStep := e.getCurrentStep(cv)
    switch currentStep {
    case "", K8sStepMasterInit:
        return e.executeMasterInit(ctx, cv, nodeConfigs)
    case K8sStepMasterJoin:
        return e.executeMasterJoin(ctx, cv, nodeConfigs)
    case K8sStepWorkerJoin:
        return e.executeWorkerJoin(ctx, cv, nodeConfigs)
    }
    return ctrl.Result{}, nil
}

func (e *KubernetesExecutor) Upgrade(
    ctx context.Context,
    cv *v1alpha1.ComponentVersion,
    nodeConfigs []*v1alpha1.NodeConfig,
) (ctrl.Result, error) {
    currentStep := e.getCurrentStep(cv)
    switch currentStep {
    case K8sStepUpgradePreCheck:
        return e.executeUpgradePreCheck(ctx, cv, nodeConfigs)
    case K8sStepUpgradeBackup:
        return e.executeUpgradeBackup(ctx, cv, nodeConfigs)
    case K8sStepUpgradeMaster:
        return e.executeUpgradeMaster(ctx, cv, nodeConfigs)
    case K8sStepUpgradeWorker:
        return e.executeUpgradeWorker(ctx, cv, nodeConfigs)
    case K8sStepUpgradePostCheck:
        return e.executeUpgradePostCheck(ctx, cv, nodeConfigs)
    }
    return ctrl.Result{}, nil
}
```
### 5.5 NodeConfig 生成逻辑
ClusterOrchestrator 从 BKECluster Spec 自动生成 NodeConfig：
```go
func (o *ClusterOrchestrator) buildNodeConfig(
    bkeCluster *bkev1beta1.BKECluster,
    node bkev1beta1.BKENode,
) *v1alpha1.NodeConfig {
    cluster := bkeCluster.Spec.ClusterConfig.Cluster
    isMaster := containsRole(node.Spec.Role, "master")

    nc := &v1alpha1.NodeConfig{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("node-%s", node.Spec.IP),
            Namespace: bkeCluster.Namespace,
            Labels: map[string]string{
                "cluster.x-k8s.io/cluster-name": bkeCluster.Name,
            },
            OwnerReferences: []metav1.OwnerReference{...},
        },
        Spec: v1alpha1.NodeConfigSpec{
            Connection: v1alpha1.NodeConnection{
                Host: node.Spec.IP,
                Port: parseInt(node.Spec.Port, 22),
                SSHKeySecret: &v1alpha1.SecretReference{
                    Name:      fmt.Sprintf("%s-node-ssh", bkeCluster.Name),
                    Namespace: bkeCluster.Namespace,
                },
            },
            Roles: convertRoles(node.Spec.Role),
            Components: v1alpha1.NodeComponents{
                BKEAgent:   &v1alpha1.BKEAgentComponentConfig{Version: deriveBKEAgentVersion(cluster.OpenFuyaoVersion)},
                NodesEnv:   &v1alpha1.NodesEnvComponentConfig{HTTPRepo: cluster.HTTPRepo.URL, ImageRepo: cluster.ImageRepo.URL},
                Containerd: &v1alpha1.ContainerdComponentConfig{Version: cluster.ContainerdVersion, SystemdCgroup: true},
                Kubelet:    &v1alpha1.KubeletComponentConfig{Version: cluster.KubernetesVersion},
            },
        },
    }

    if isMaster {
        nc.Spec.Components.Etcd = &v1alpha1.EtcdComponentConfig{
            Version:    cluster.EtcdVersion,
            DataDir:    "/var/lib/etcd",
            ClientURLs: []string{fmt.Sprintf("https://%s:2379", node.Spec.IP)},
            PeerURLs:   []string{fmt.Sprintf("https://%s:2380", node.Spec.IP)},
        }
    }

    return nc
}
```
## 6. 第三步：CVO 升级机制（安装+升级+扩缩容）
### 6.1 目标
在第一步和第二步的基础上，实现完整的 CVO（Cluster Version Operator）升级机制，覆盖：
- **安装**：从零创建集群，通过 ComponentVersion DAG 驱动安装
- **升级**：版本变更触发升级，支持 PreCheck→Upgrade→PostCheck→Rollback 全链路
- **扩缩容**：节点增删触发扩缩容，与升级解耦
### 6.2 ClusterVersion CRD 详细设计
```go
// api/cvo/v1beta1/clusterversion_types.go

type ClusterVersionSpec struct {
    DesiredVersion           string             `json:"desiredVersion"`
    DesiredComponentVersions ComponentVersions  `json:"desiredComponentVersions,omitempty"`
    ClusterRef               *ClusterReference  `json:"clusterRef,omitempty"`
    UpgradeStrategy          UpgradeStrategy    `json:"upgradeStrategy,omitempty"`
    Pause                    bool               `json:"pause,omitempty"`
    AllowDowngrade           bool               `json:"allowDowngrade,omitempty"`
}

type ComponentVersions struct {
    OpenFuyaoVersion  string `json:"openFuyaoVersion,omitempty"`
    KubernetesVersion string `json:"kubernetesVersion,omitempty"`
    EtcdVersion       string `json:"etcdVersion,omitempty"`
    ContainerdVersion string `json:"containerdVersion,omitempty"`
    BKEAgentVersion   string `json:"bkeAgentVersion,omitempty"`
}

type UpgradeStrategy struct {
    Type              UpgradeStrategyType `json:"type,omitempty"`
    MaxUnavailable    int                 `json:"maxUnavailable,omitempty"`
    BatchSize         int                 `json:"batchSize,omitempty"`
    BatchInterval     metav1.Duration     `json:"batchInterval,omitempty"`
    RollbackOnFailure bool                `json:"rollbackOnFailure,omitempty"`
}

type ClusterVersionStatus struct {
    CurrentVersion    string              `json:"currentVersion,omitempty"`
    CurrentComponents ComponentVersions   `json:"currentComponents,omitempty"`
    Phase             ClusterVersionPhase `json:"phase,omitempty"`
    UpgradeSteps      []UpgradeStep       `json:"upgradeSteps,omitempty"`
    CurrentStepIndex  int                 `json:"currentStepIndex,omitempty"`
    TotalNodes        int                 `json:"totalNodes,omitempty"`
    UpgradedNodes     int                 `json:"upgradedNodes,omitempty"`
    FailedNodes       int                 `json:"failedNodes,omitempty"`
    History           []UpgradeHistory    `json:"history,omitempty"`
    Conditions        []metav1.Condition  `json:"conditions,omitempty"`
}
```
### 6.3 升级步骤定义
```go
type UpgradeStepType string

const (
    UpgradeStepPreCheck          UpgradeStepType = "PreCheck"
    UpgradeStepProviderSelf      UpgradeStepType = "ProviderSelfUpgrade"
    UpgradeStepAgent             UpgradeStepType = "AgentUpgrade"
    UpgradeStepContainerd        UpgradeStepType = "ContainerdUpgrade"
    UpgradeStepEtcd              UpgradeStepType = "EtcdUpgrade"
    UpgradeStepControlPlane      UpgradeStepType = "ControlPlaneUpgrade"
    UpgradeStepWorker            UpgradeStepType = "WorkerUpgrade"
    UpgradeStepComponent         UpgradeStepType = "ComponentUpgrade"
    UpgradeStepPostCheck         UpgradeStepType = "PostCheck"
)

type UpgradeStep struct {
    Type      UpgradeStepType `json:"type"`
    Status    StepStatus      `json:"status,omitempty"`
    Message   string          `json:"message,omitempty"`
    StartTime *metav1.Time    `json:"startTime,omitempty"`
    EndTime   *metav1.Time    `json:"endTime,omitempty"`
}

type StepStatus string

const (
    StepStatusPending    StepStatus = "Pending"
    StepStatusInProgress StepStatus = "InProgress"
    StepStatusCompleted  StepStatus = "Completed"
    StepStatusFailed     StepStatus = "Failed"
    StepStatusSkipped    StepStatus = "Skipped"
)
```
### 6.4 CVO Controller 设计
```go
// controllers/cvo/clusterversion_controller.go

type ClusterVersionReconciler struct {
    client.Client
    Scheme   *runtime.Scheme
    orchestrator *orchestrator.ClusterOrchestrator
}

func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvo.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if cv.Spec.Pause {
        return ctrl.Result{}, nil
    }

    switch cv.Status.Phase {
    case "", ClusterVersionPhaseAvailable:
        return r.handleAvailable(ctx, cv)
    case ClusterVersionPhaseProgressing:
        return r.handleProgressing(ctx, cv)
    case ClusterVersionPhaseDegraded:
        return r.handleDegraded(ctx, cv)
    case ClusterVersionPhaseRollingBack:
        return r.handleRollingBack(ctx, cv)
    }
    return ctrl.Result{}, nil
}
```
### 6.5 安装流程
安装由 ClusterOrchestrator 通过 ComponentVersion DAG 驱动：
```
用户创建 BKECluster
    │
    ├── ClusterOrchestrator.syncDesiredState()
    │   ├── 生成 ComponentVersion[]（13 个组件）
    │   └── 生成 NodeConfig[]（每个节点一个）
    │
    ├── DAGScheduler.Schedule()
    │   └── 按依赖图计算执行顺序
    │
    └── 逐组件执行 Install()
        ├── BKEAgent（所有节点）
        ├── NodesEnv（所有节点）
        ├── ClusterAPI（集群级）
        ├── Certs（集群级）
        ├── LoadBalancer（LB 节点）
        ├── Kubernetes/MasterInit（首个 master）
        ├── Kubernetes/MasterJoin（其余 master）
        ├── Kubernetes/WorkerJoin（worker）
        ├── Addon（集群级）
        ├── NodesPostProcess（所有节点）
        ├── AgentSwitch（集群级）
        └── BKEProvider（集群级）
```
### 6.6 升级流程
升级由 CVO Controller 通过 ClusterVersion 驱动，按升级 DAG 逐步执行：
```
用户修改 ClusterVersion.Spec.DesiredVersion
    │
    ├── CVO 检测版本变更
    │   └── 生成 UpgradeSteps[]
    │
    ├── Step 1: PreCheck
    │   ├── 检查集群健康
    │   ├── 检查版本兼容性
    │   └── 检查升级路径合法性
    │
    ├── Step 2: ProviderSelfUpgrade
    │   └── Patch bke-controller-manager Deployment 镜像 tag
    │
    ├── Step 3: AgentUpgrade
    │   └── 逐节点更新 BKEAgent 二进制 + 重启
    │
    ├── Step 4: ContainerdUpgrade
    │   └── 逐节点停止→备份→替换→启动→验证
    │
    ├── Step 5: EtcdUpgrade
    │   └── 逐 master 节点停止→备份→替换→启动→健康检查
    │
    ├── Step 6: ControlPlaneUpgrade
    │   └── 逐 master 节点 kubeadm upgrade + kubelet 升级
    │
    ├── Step 7: WorkerUpgrade
    │   └── 按 BatchSize 分批 worker 节点 kubeadm upgrade + kubelet 升级
    │
    ├── Step 8: ComponentUpgrade
    │   └── Patch openFuyao 各 Deployment 镜像 tag
    │
    └── Step 9: PostCheck
        ├── 检查所有节点 Ready
        ├── 检查所有组件版本正确
        └── 检查集群功能正常
```
**升级批次控制**：
```go
func (r *ClusterVersionReconciler) executeWorkerUpgrade(
    ctx context.Context,
    cv *cvo.ClusterVersion,
) (ctrl.Result, error) {
    batchSize := cv.Spec.UpgradeStrategy.BatchSize
    if batchSize == 0 {
        batchSize = 1
    }

    nodeConfigs, err := r.getNodeConfigsByRole(ctx, cv, "worker")
    if err != nil {
        return ctrl.Result{}, err
    }

    upgraded := 0
    for _, nc := range nodeConfigs {
        if nc.Status.ComponentStatus["kubelet"].InstalledVersion == cv.Spec.DesiredComponentVersions.KubernetesVersion {
            upgraded++
            continue
        }
        if upgraded >= batchSize {
            break
        }
        executor := r.orchestrator.GetExecutor(ComponentKubernetes)
        result, err := executor.Upgrade(ctx, kubernetesCV, []*v1alpha1.NodeConfig{nc})
        if err != nil {
            if cv.Spec.UpgradeStrategy.RollbackOnFailure {
                cv.Status.Phase = ClusterVersionPhaseRollingBack
            } else {
                cv.Status.Phase = ClusterVersionPhaseDegraded
            }
            return ctrl.Result{}, r.Status().Update(ctx, cv)
        }
        upgraded++
    }

    if upgraded < len(nodeConfigs) {
        return ctrl.Result{RequeueAfter: cv.Spec.UpgradeStrategy.BatchInterval.Duration}, nil
    }
    return ctrl.Result{}, nil
}
```
### 6.7 扩缩容流程
#### 6.7.1 扩容
```
用户在 BKECluster.Spec.Nodes 中添加节点
    │
    ├── ClusterOrchestrator.syncDesiredState()
    │   └── 检测到新节点，创建 NodeConfig（phase=Pending）
    │
    ├── ClusterOrchestrator.Reconcile()
    │   ├── 为新节点执行已就绪组件的安装
    │   │   ├── BKEAgent Install
    │   │   ├── NodesEnv Install
    │   │   ├── Containerd Install
    │   │   ├── Kubernetes Join（MasterJoin 或 WorkerJoin）
    │   │   └── NodesPostProcess Install
    │   └── 更新 NodeConfig.Status.ComponentStatus
    │
    └── ClusterVersion 更新 TotalNodes
```
#### 6.7.2 缩容
```
用户从 BKECluster.Spec.Nodes 中移除节点
    │
    ├── ClusterOrchestrator.syncDesiredState()
    │   └── 检测到节点移除，设置 NodeConfig phase=Deleting
    │
    ├── NodeConfig Controller 处理缩容
    │   ├── Step 1: Drain 节点
    │   ├── Step 2: 删除 Machine 对象
    │   ├── Step 3: 等待节点从集群中移除
    │   └── Step 4: 删除 NodeConfig CR
    │
    └── ClusterVersion 更新 TotalNodes
```
### 6.8 回滚机制
```go
func (r *ClusterVersionReconciler) handleRollingBack(
    ctx context.Context,
    cv *cvo.ClusterVersion,
) (ctrl.Result, error) {
    // 从 History 中获取上一个稳定版本
    if len(cv.Status.History) == 0 {
        cv.Status.Phase = ClusterVersionPhaseDegraded
        return ctrl.Result{}, r.Status().Update(ctx, cv)
    }

    lastStable := cv.Status.History[len(cv.Status.History)-1]

    // 按升级的逆序回滚
    for i := cv.Status.CurrentStepIndex; i >= 0; i-- {
        step := cv.Status.UpgradeSteps[i]
        if step.Status != StepStatusCompleted {
            continue
        }

        executor := r.getExecutorForStep(step.Type)
        if err := executor.Rollback(ctx, cv); err != nil {
            cv.Status.Phase = ClusterVersionPhaseDegraded
            return ctrl.Result{}, r.Status().Update(ctx, cv)
        }
    }

    cv.Status.CurrentVersion = lastStable.Version
    cv.Status.CurrentComponents = lastStable.Components
    cv.Status.Phase = ClusterVersionPhaseAvailable
    return ctrl.Result{}, r.Status().Update(ctx, cv)
}
```
## 7. 迁移策略
### 7.1 Feature Gate 渐进切换
| 阶段 | Feature Gate | 验证方式 |
|------|-------------|----------|
| **Phase 1**（第一步） | `DeclarativeOrchestration=false` | CRD 可创建，Controller 可启动，不影响现有 PhaseFlow |
| **Phase 2**（第二步） | `DeclarativeOrchestration=true`（可选开启） | 对比新旧路径执行结果，内部复用现有 Phase 核心函数 |
| **Phase 3**（第三步） | `DeclarativeOrchestration=true`（默认开启） | 全量 E2E 测试，CVO 完整升级流程 |
| **Phase 4**（清理） | 不可逆 | 移除旧 Phase 代码，统一版本状态到 ClusterVersion |
### 7.2 兼容性保证
```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // ... 控制类逻辑 ...

    if featuregate.Enabled(featuregate.DeclarativeOrchestration) {
        return r.orchestrator.Reconcile(ctx, bkeCluster)
    }

    // 降级到旧 PhaseFlow
    return r.phaseFlow.Execute(ctx, bkeCluster)
}
```
## 8. 目录结构
```
cluster-api-provider-bke/
├── api/
│   ├── nodecomponent/v1alpha1/          # 新增
│   │   ├── componentversion_types.go
│   │   ├── nodeconfig_types.go
│   │   ├── groupversion_info.go
│   │   └── zz_generated.deepcopy.go
│   └── cvo/v1beta1/                     # 新增
│       ├── clusterversion_types.go
│       ├── groupversion_info.go
│       └── zz_generated.deepcopy.go
├── controllers/
│   ├── nodecomponent/                   # 新增
│   │   ├── componentversion_controller.go
│   │   └── nodeconfig_controller.go
│   └── cvo/                             # 新增
│       └── clusterversion_controller.go
├── pkg/
│   ├── orchestrator/                    # 新增
│   │   ├── cluster_orchestrator.go
│   │   ├── dag_scheduler.go
│   │   └── executor/
│   │       ├── interface.go
│   │       ├── bkeagent_executor.go
│   │       ├── nodesenv_executor.go
│   │       ├── clusterapi_executor.go
│   │       ├── certs_executor.go
│   │       ├── loadbalancer_executor.go
│   │       ├── kubernetes_executor.go
│   │       ├── containerd_executor.go
│   │       ├── etcd_executor.go
│   │       ├── addon_executor.go
│   │       ├── openfuyao_executor.go
│   │       ├── bkeprovider_executor.go
│   │       ├── nodespostprocess_executor.go
│   │       └── agentswitch_executor.go
│   └── phaseframe/                      # 保留，逐步迁移
│       └── phases/                      # 保留，Executor 内部复用核心函数
```
## 9. 工作量评估
| 步骤 | 内容 | 工作量（人天） |
|------|------|--------------|
| **第一步** | ComponentVersion/NodeConfig CRD 定义 + ClusterOrchestrator 骨架 + DAGScheduler + Phase→ComponentVersion 映射 | 10 |
| **第二步** | 13 个 ComponentExecutor 实现（复用现有 Phase 核心函数）+ NodeConfig 生成逻辑 + 安装流程 E2E | 15 |
| **第三步** | ClusterVersion CRD + CVO Controller + 升级流程（PreCheck→Upgrade→PostCheck→Rollback）+ 扩缩容 + 批次控制 | 12 |
| **测试** | 单元测试 + 集成测试 + E2E 测试 + 新旧路径对比测试 | 8 |
| **总计** | | **45 人天** |
## 10. 风险评估
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| Executor 复用现有 Phase 函数时接口不匹配 | 中 | 提取 Phase 核心函数为独立函数，Executor 调用独立函数 |
| DAG 调度死锁 | 高 | 设置超时 + 循环依赖检测 + 手动跳过机制 |
| 升级过程中部分节点失败 | 高 | 批次控制 + 暂停机制 + 回滚机制 |
| Feature Gate 切换时状态不一致 | 中 | 双写验证 + 灰度切换 |
| NodeConfig 与 BKEMachine 关联不一致 | 低 | 通过 IP 地址自动关联 + 定期同步 |
## 11. 验收标准
1. **第一步验收**：ComponentVersion 和 NodeConfig CRD 可创建，ClusterOrchestrator 可启动，DAGScheduler 可正确计算依赖
2. **第二步验收**：13 个 ComponentExecutor 可正确执行安装，内部复用现有 Phase 核心函数，安装流程通过 E2E 测试
3. **第三步验收**：
   - 安装：从零创建集群，通过 ComponentVersion DAG 驱动安装，所有组件安装成功
   - 升级：修改 ClusterVersion 版本，触发 PreCheck→Upgrade→PostCheck 全链路，支持回滚
   - 扩容：添加节点到 BKECluster.Spec.Nodes，自动创建 NodeConfig 并安装组件
   - 缩容：从 BKECluster.Spec.Nodes 移除节点，自动 Drain→删除 Machine→删除 NodeConfig
4. **兼容性验收**：Feature Gate 关闭时，旧 PhaseFlow 正常运行；Feature Gate 开启时，新声明式路径正常运行


# KEPU-1-Phase4: PhaseFrame 声明式重构 + CVO 升级机制提案
## 元数据
| 字段 | 值 |
|------|-----|
| **KEPU编号** | KEPU-1-Phase4 |
| **标题** | PhaseFrame 声明式重构 + CVO 升级机制独立实现 |
| **状态** | Provisional |
| **类型** | Enhancement |
| **父提案** | KEPU-1 |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-20 |
| **最后更新** | 2026-04-20 |
## 1. 摘要
本提案涵盖 KEPU-1 阶段四的完整重构任务，包含三个递进层次：
1. **PhaseFrame 升级 Phase 重构为 ComponentVersion 声明式架构** — 将 PostDeployPhases 中的 7 个升级 Phase 映射为 ComponentVersion + Upgrader 声明式模型
2. **PhaseFrame 安装 Phase 重构为 ComponentVersion 声明式架构** — 将 DeployPhases 中的 11 个安装 Phase 映射为 ComponentVersion + NodeConfig 声明式模型
3. **CVO 升级机制 + ClusterVersion CRD** — 在声明式架构基础上实现声明式升级编排

三个层次存在严格的前置依赖关系：声明式安装架构（层次2）是声明式升级架构（层次1）的基础，两者共同构成 CVO 升级机制（层次3）的运行平台。
## 2. 动机
### 2.1 当前架构核心缺陷
当前升级和安装逻辑分布在 [list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go) 的 `DeployPhases` 和 `PostDeployPhases` 中：
```
DeployPhases (11个安装Phase):
├── EnsureBKEAgent          # SSH推送Agent二进制
├── EnsureNodesEnv          # 节点环境初始化脚本
├── EnsureClusterAPIObj     # 创建Cluster API对象
├── EnsureCerts             # 证书生成
├── EnsureLoadBalance       # HAProxy配置
├── EnsureMasterInit        # kubeadm init
├── EnsureMasterJoin        # kubeadm join --control-plane
├── EnsureWorkerJoin        # kubeadm join
├── EnsureAddonDeploy       # Helm/YAML部署Addon
├── EnsureNodesPostProcess  # 节点后处理
└── EnsureAgentSwitch       # Agent切换目标集群

PostDeployPhases (7个升级Phase):
├── EnsureProviderSelfUpgrade  # Provider Deployment滚动更新
├── EnsureAgentUpgrade         # Agent DaemonSet升级
├── EnsureContainerdUpgrade    # Containerd节点级升级
├── EnsureEtcdUpgrade          # Etcd滚动升级
├── EnsureWorkerUpgrade        # Worker节点滚动升级
├── EnsureMasterUpgrade        # Master节点滚动升级
└── EnsureComponentUpgrade     # openFuyao组件升级
```
**核心问题**：

| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 1 | 安装与升级逻辑耦合 | 安装Phase和升级Phase共享BKECluster状态，无独立版本追踪 | 无法独立管理组件生命周期 |
| 2 | 缺少声明式模型 | Phase通过命令式脚本执行，状态仅存在BKECluster.Status | 无法声明式定义组件期望状态 |
| 3 | 版本状态分散 | `BKEClusterStatus`中仅4个版本字段 | 缺少Agent、Addon、Provider等版本 |
| 4 | 缺少回滚能力 | 升级失败后无法自动回滚 | 只能手动修复 |
| 5 | 升级触发单一 | 仅通过`OpenFuyaoVersion`变化触发 | 无法独立升级单个组件 |
| 6 | 组件依赖隐式 | Phase执行顺序硬编码在list.go | 无法并行、无法动态调整 |
### 2.2 重构必要性
要实现 CVO 声明式升级机制，必须先将 PhaseFrame 的命令式 Phase 重构为 ComponentVersion 声明式架构，原因：
1. **ClusterVersion 需要组件级版本追踪**：当前 BKECluster.Status 仅有4个版本字段，CVO 需要追踪所有组件的版本状态
2. **DAG 调度需要组件依赖声明**：当前 Phase 依赖隐式定义在执行顺序中，DAG 调度器需要显式依赖声明
3. **Upgrader 需要组件级执行接口**：当前 Phase 的 `Execute()` 接口过于通用，Upgrader 需要组件特定的安装/升级/回滚接口
4. **NodeConfig 需要节点级配置分离**：当前节点配置嵌入在 BKECluster.Spec 中，NodeConfig 需要独立管理
## 3. 与其他阶段的依赖分析
### 3.1 逐阶段依赖评估
| 阶段 | 依赖程度 | 说明 |
|------|---------|------|
| **阶段一**（统一错误处理 + CRD 扩展） | **不依赖** | 本提案可自行定义 `UpgradeError` 类型；`api/bkecommon/v1beta1/` 现有类型已满足需求 |
| **阶段二**（状态机引擎） | **不依赖** | ClusterVersion Controller 拥有独立状态机（Available→Progressing→Degraded→RollingBack），不嵌入阶段二的状态机框架 |
| **阶段三**（Provider 分离） | **不依赖** | ComponentVersion/NodeConfig 是全新CRD，不依赖 Bootstrap/ControlPlane Provider 分离 |
| **阶段五**（OSProvider） | **不依赖** | OS抽象是节点操作层，与组件编排层正交；ComponentVersion 的 `compatibility.os` 字段可先硬编码 |
| **阶段六**（版本包管理 + Asset） | **弱依赖** | 版本兼容性校验可先实现简化版（`CompatibilityRule`），后续迁移到 VersionPackage CRD |
| **阶段七**（清理 + 测试） | **不依赖** | 清理是后续工作 |
### 3.2 最小化依赖集
本提案仅依赖**当前代码库中已存在**的组件：

| 依赖项 | 来源 | 用途 |
|--------|------|------|
| `BKECluster` CRD | `api/capbke/v1beta1/` | ClusterVersion 通过 `clusterRef` 关联 |
| `BKEClusterStatus` | `api/bkecommon/v1beta1/` | 读取当前版本信息 |
| PhaseFlow 执行引擎 | `pkg/phaseframe/` | 过渡期复用现有 Phase 核心函数 |
| `controller-runtime` | 已有依赖 | Reconciler 框架 |
| `kubebuilder` | 已有依赖 | CRD 生成 |
### 3.3 依赖关系图
```
┌─────────────────────────────────────────────────────────────────┐
│                    阶段依赖关系图                                │
│                                                                 │
│  阶段一 ──→ 阶段二 ──→ 阶段三    （Provider 分离链路）          │
│                                                                 │
│  阶段五                         （OS 抽象，独立）               │
│                                                                 │
│  阶段六                         （版本包管理，弱依赖）           │
│                                                                 │
│  ★ 本提案 ★ = {层次1 + 层次2 + 层次3}  （独立实现）            │
│                                                                 │
│  阶段七                         （清理，依赖所有阶段完成）       │
│                                                                 │
│  结论：本提案无前置依赖，可独立实现                             │
└─────────────────────────────────────────────────────────────────┘
```
## 4. 提案
### 4.1 总体架构

```
┌──────────────────────────────────────────────────────────────────────┐
│                         重构后架构                                   │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │              BKEClusterReconciler（精简后）                    │  │
│  │  ├── 控制类逻辑（Finalizer/Paused/DryRun/Delete/Manage）       │  │
│  │  └── ClusterOrchestrator.Reconcile()                           │  │
│  └────────────────────────────┬───────────────────────────────────┘  │
│                               │                                      │
│  ┌────────────────────────────▼───────────────────────────────────┐  │
│  │              ClusterOrchestrator（声明式编排）                 │  │
│  │  ├── syncDesiredState()→生成/更新 ComponentVersion + NodeConfig│  │
│  │  ├── DAGScheduler.Schedule() → 计算就绪/阻塞/运行中组件        │  │
│  │  └── executeComponent() → 调用 ComponentExecutor               │  │
│  └───────────────────────────┬────────────────────────────────────┘  │
│                              │                                       │
│          ┌───────────────────┼───────────────────┐                   │
│          │                   │                   │                   │
│  ┌───────▼────────┐  ┌───────▼────────┐  ┌───────▼────────┐          │
│  │ComponentVersion│  │ComponentVersion│  │ ClusterVersion │          │
│  │  (安装/升级)   │  │  (安装/升级)   │  │  (全局版本)    │          │
│  └───────┬────────┘  └────────┬───────┘  └─────────┬──────┘          │
│          │                    │                    │                 │
│  ┌───────▼─────────┐  ┌───────▼─────────┐  ┌───────▼───────────┐     │
│  │ComponentExecutor│  │ComponentExecutor│  │UpgradeOrchestrator│     │
│  │  (安装执行器)   │  │  (升级执行器)   │  │  (升级编排器)     │     │
│  └─────────────────┘  └─────────────────┘  └───────────────────┘     │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │              NodeConfig[]（节点级配置）                        │  │
│  │  ├── node-192.168.1.10 (master)                                │  │
│  │  ├── node-192.168.1.11 (master)                                │  │
│  │  └── node-192.168.1.20 (worker)                                │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```
### 4.2 层次一：升级 Phase → ComponentVersion 声明式架构
#### 4.2.1 升级 Phase 映射关系
| 现有升级 Phase | 目标 ComponentVersion | Scope | 依赖 |
|---------------|----------------------|-------|------|
| `EnsureProviderSelfUpgrade` | `bkeProvider` | Cluster | 无 |
| `EnsureAgentUpgrade` | `bkeAgent` | Node | bkeProvider |
| `EnsureContainerdUpgrade` | `containerd` | Node | bkeAgent |
| `EnsureEtcdUpgrade` | `etcd` | Node | bkeAgent |
| `EnsureMasterUpgrade` | 合并到 `kubernetes` | Node | containerd, etcd |
| `EnsureWorkerUpgrade` | 合并到 `kubernetes` | Node | containerd, etcd |
| `EnsureComponentUpgrade` | `openFuyao` | Cluster | bkeAgent |

**关键设计决策**：`EnsureMasterUpgrade` 和 `EnsureWorkerUpgrade` 合并为单个 `kubernetes` ComponentVersion，因为它们操作同一组二进制（kubeadm/kubelet/kubectl），仅节点角色不同。
#### 4.2.2 升级组件依赖 DAG
```
                    ┌─────────────────┐
                    │  BKEProvider    │ ← 最先升级
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │   BKEAgent      │
                    │   (daemonset)   │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───────┐      │     ┌────────▼───────┐
     │   Containerd   │      │     │   OpenFuyao    │
     │   (node-level) │      │     │  (components)  │
     └────────┬───────┘      │     └────────────────┘
              │              │
     ┌────────▼───────┐      │
     │     Etcd       │      │
     │  (data plane)  │      │
     └────────┬───────┘      │
              │              │
              └──────┬───────┘
                     │
           ┌─────────▼──────────┐
           │    Kubernetes      │
           │ (control-plane +   │
           │  worker nodes)     │
           └────────────────────┘
```
#### 4.2.3 Upgrader 接口设计
```go
// pkg/cvo/upgrader/interface.go
package upgrader

type Upgrader interface {
    Name() ComponentName
    Scope() ComponentScope
    Dependencies() []ComponentName
    NeedUpgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) (bool, error)
    Upgrade(ctx context.Context, cv *cvov1beta1.ClusterVersion) (ctrl.Result, error)
    Rollback(ctx context.Context, cv *cvov1beta1.ClusterVersion) error
    HealthCheck(ctx context.Context, cv *cvov1beta1.ClusterVersion) (bool, error)
    CurrentVersion(ctx context.Context, cv *cvov1beta1.ClusterVersion) (string, error)
}
```
#### 4.2.4 KubernetesUpgrader 内部步骤状态机
Kubernetes 组件是最复杂的升级对象，需要内部步骤状态机：
```
PreCheck → EtcdBackup → MasterRollout(逐节点) → WorkerRollout(逐节点) → PostCheck
```

```go
type K8sUpgradeStep string

const (
    K8sStepPreCheck      K8sUpgradeStep = "PreCheck"
    K8sStepEtcdBackup    K8sUpgradeStep = "EtcdBackup"
    K8sStepMasterRollout K8sUpgradeStep = "MasterRollout"
    K8sStepWorkerRollout K8sUpgradeStep = "WorkerRollout"
    K8sStepPostCheck     K8sUpgradeStep = "PostCheck"
)
```
#### 4.2.5 升级 Phase 重构实现策略
各 Upgrader **复用现有 Phase 的核心函数**，通过适配层桥接：

| Upgrader | 复用的现有函数 | 适配方式 |
|----------|--------------|---------|
| `BKEProviderUpgrader` | `phaseutil.DeploymentTarget` + `waitForDeploymentReady` | 直接调用 |
| `BKEAgentUpgrader` | `EnsureAgentUpgrade.rolloutAgentDaemonSet` | 提取为公共函数 |
| `ContainerdUpgrader` | `EnsureContainerdUpgrade.getCommand` + `rolloutContainerd` | 提取为公共函数 |
| `EtcdUpgrader` | `EnsureEtcdUpgrade` 的逐节点升级逻辑 | 提取为公共函数 |
| `KubernetesUpgrader` | `EnsureMasterUpgrade.executeNodeUpgrade` + `EnsureWorkerUpgrade.drain+upgrade+uncordon` | 提取为公共函数 |
| `OpenFuyaoComponentUpgrader` | `EnsureComponentUpgrade.rolloutOpenfuyaoComponent` | 提取为公共函数 |
### 4.3 层次二：安装 Phase → ComponentVersion 声明式架构
#### 4.3.1 安装 Phase 映射关系
| 现有安装 Phase | 目标 ComponentVersion | Scope | 依赖 |
|---------------|----------------------|-------|------|
| `EnsureBKEAgent` | `bkeAgent` | Node | 无 |
| `EnsureNodesEnv` | `nodesEnv` | Node | bkeAgent |
| `EnsureClusterAPIObj` | `clusterAPI` | Cluster | bkeAgent, nodesEnv |
| `EnsureCerts` | `certs` | Cluster | clusterAPI |
| `EnsureLoadBalance` | `loadBalancer` | Node | certs |
| `EnsureMasterInit` | 合并到 `kubernetes` | Node | loadBalancer |
| `EnsureMasterJoin` | 合并到 `kubernetes` | Node | kubernetes(MasterInit) |
| `EnsureWorkerJoin` | 合并到 `kubernetes` | Node | kubernetes(MasterJoin) |
| `EnsureAddonDeploy` | `addon` | Cluster | kubernetes |
| `EnsureNodesPostProcess` | `nodesPostProcess` | Node | addon |
| `EnsureAgentSwitch` | `agentSwitch` | Cluster | nodesPostProcess |
#### 4.3.2 安装组件依赖 DAG

```
                    ┌─────────────────┐
                    │  BKEAgent       │ ← 最先安装
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───────┐      │     ┌────────▼───────┐
     │   NodesEnv     │      │     │  ClusterAPI    │
     └────────┬───────┘      │     └────────┬───────┘
              │              │              │
              │              │     ┌────────▼───────┐
              │              │     │    Certs       │
              │              │     └────────┬───────┘
              │              │              │
              │     ┌────────▼──────────────▼───────┐
              │     │       LoadBalancer            │
              │     └────────┬──────────────────────┘
              │              │
              │     ┌────────▼──────────────────────┐
              │     │    Kubernetes (Init/Join)     │
              │     └────────┬──────────────────────┘
              │              │
              │     ┌────────▼───────┐
              │     │     Addon      │
              │     └────────┬───────┘
              │              │
              │     ┌────────▼──────────┐
              │     │  NodesPostProcess │
              │     └────────┬──────────┘
              │              │
              └──────┬───────┘
                     │
           ┌─────────▼──────────┐
           │   AgentSwitch      │
           └────────────────────┘
```
#### 4.3.3 ComponentExecutor 接口设计
```go
// pkg/orchestrator/executor/interface.go
package executor

type ComponentExecutor interface {
    Name() ComponentName
    Scope() ComponentScope
    Install(ctx context.Context, cv *v1alpha1.ComponentVersion, ncs []*v1alpha1.NodeConfig) (ctrl.Result, error)
    Upgrade(ctx context.Context, cv *v1alpha1.ComponentVersion, ncs []*v1alpha1.NodeConfig) (ctrl.Result, error)
    Rollback(ctx context.Context, cv *v1alpha1.ComponentVersion, ncs []*v1alpha1.NodeConfig) error
    HealthCheck(ctx context.Context, cv *v1alpha1.ComponentVersion, ncs []*v1alpha1.NodeConfig) (bool, error)
}
```
#### 4.3.4 Kubernetes ComponentVersion 的安装步骤状态机
Kubernetes 组件合并了 MasterInit、MasterJoin、WorkerJoin，内部步骤：
```
MasterInit → MasterJoin(逐节点) → WorkerJoin(逐节点)
```

```go
type K8sInstallStep string

const (
    K8sStepMasterInit K8sInstallStep = "MasterInit"
    K8sStepMasterJoin K8sInstallStep = "MasterJoin"
    K8sStepWorkerJoin K8sInstallStep = "WorkerJoin"
)
```
#### 4.3.5 控制类 Phase 处理
控制类 Phase（EnsureFinalizer、EnsurePaused、EnsureClusterManage、EnsureDeleteOrReset、EnsureDryRun）**不映射为 ComponentVersion**，保留在 BKECluster Controller 中：
```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // === 控制类逻辑（保留在 Controller 中） ===
    // 1. EnsureFinalizer
    // 2. EnsurePaused
    // 3. EnsureDryRun
    // 4. EnsureDeleteOrReset
    // 5. EnsureClusterManage

    // === 声明式组件编排（由 ClusterOrchestrator 接管） ===
    return r.orchestrator.Reconcile(ctx, bkeCluster)
}
```
#### 4.3.6 缩容 Phase 处理
EnsureWorkerDelete 和 EnsureMasterDelete **不映射为 ComponentVersion**，通过 NodeConfig 的 `status.phase = Deleting` 触发缩容流程。
### 4.4 层次三：CVO 升级机制 + ClusterVersion CRD
#### 4.4.1 ClusterVersion CRD
```go
// api/cvo/v1beta1/clusterversion_types.go
package v1beta1

type ClusterVersionPhase string

const (
    ClusterVersionPhaseAvailable   ClusterVersionPhase = "Available"
    ClusterVersionPhaseProgressing ClusterVersionPhase = "Progressing"
    ClusterVersionPhaseDegraded    ClusterVersionPhase = "Degraded"
    ClusterVersionPhaseRollingBack ClusterVersionPhase = "RollingBack"
)

type ClusterVersionSpec struct {
    DesiredVersion           string            `json:"desiredVersion"`
    DesiredComponentVersions ComponentVersions `json:"desiredComponentVersions,omitempty"`
    ClusterRef               *ClusterReference `json:"clusterRef,omitempty"`
    UpgradeStrategy          UpgradeStrategy   `json:"upgradeStrategy,omitempty"`
    Pause                    bool              `json:"pause,omitempty"`
    AllowDowngrade           bool              `json:"allowDowngrade,omitempty"`
}

type ComponentVersions struct {
    Kubernetes string            `json:"kubernetes,omitempty"`
    Etcd       string            `json:"etcd,omitempty"`
    Containerd string            `json:"containerd,omitempty"`
    OpenFuyao  string            `json:"openFuyao,omitempty"`
    BKEAgent   string            `json:"bkeAgent,omitempty"`
    BKEProvider string           `json:"bkeProvider,omitempty"`
    Extra      map[string]string `json:"extra,omitempty"`
}

type UpgradeStrategy struct {
    Type                   UpgradeStrategyType `json:"type,omitempty"`
    MaxParallelNodes       int                 `json:"maxParallelNodes,omitempty"`
    BatchInterval          metav1.Duration     `json:"batchInterval,omitempty"`
    RollbackOnFailure      bool                `json:"rollbackOnFailure,omitempty"`
    PreUpgradeHealthCheck  *HealthCheckConfig  `json:"preUpgradeHealthCheck,omitempty"`
    PostUpgradeHealthCheck *HealthCheckConfig  `json:"postUpgradeHealthCheck,omitempty"`
}

type ClusterVersionStatus struct {
    CurrentVersion           string                       `json:"currentVersion,omitempty"`
    CurrentComponentVersions ComponentVersions            `json:"currentComponentVersions,omitempty"`
    Phase                    ClusterVersionPhase          `json:"phase,omitempty"`
    Conditions               []metav1.Condition           `json:"conditions,omitempty"`
    History                  []UpgradeHistory             `json:"history,omitempty"`
    CurrentUpgrade           *CurrentUpgrade              `json:"currentUpgrade,omitempty"`
    ComponentStatuses        map[string]ComponentVersionStatus `json:"componentStatuses,omitempty"`
    ObservedGeneration       int64                        `json:"observedGeneration,omitempty"`
}

type ComponentVersionStatus struct {
    Name      ComponentName  `json:"name"`
    Current   string         `json:"current,omitempty"`
    Desired   string         `json:"desired,omitempty"`
    Phase     ComponentPhase `json:"phase,omitempty"`
    Message   string         `json:"message,omitempty"`
    UpdatedAt *metav1.Time   `json:"updatedAt,omitempty"`
}

type CurrentUpgrade struct {
    TargetVersion           string        `json:"targetVersion,omitempty"`
    TargetComponentVersions ComponentVersions `json:"targetComponentVersions,omitempty"`
    StartTime               *metav1.Time  `json:"startTime,omitempty"`
    Steps                   []UpgradeStep `json:"steps,omitempty"`
    CurrentStepIndex        int           `json:"currentStepIndex,omitempty"`
    Progress                int           `json:"progress,omitempty"`
    Failure                 *UpgradeFailure `json:"failure,omitempty"`
}

type UpgradeStep struct {
    Name           UpgradeStepType `json:"name"`
    State          StepState       `json:"state"`
    StartTime      *metav1.Time    `json:"startTime,omitempty"`
    CompletionTime *metav1.Time    `json:"completionTime,omitempty"`
    Message        string          `json:"message,omitempty"`
}

type UpgradeStepType string

const (
    UpgradeStepPreCheck     UpgradeStepType = "PreCheck"
    UpgradeStepProviderSelf UpgradeStepType = "ProviderSelfUpgrade"
    UpgradeStepAgent        UpgradeStepType = "AgentUpgrade"
    UpgradeStepContainerd   UpgradeStepType = "ContainerdUpgrade"
    UpgradeStepEtcd         UpgradeStepType = "EtcdUpgrade"
    UpgradeStepControlPlane UpgradeStepType = "ControlPlaneUpgrade"
    UpgradeStepWorker       UpgradeStepType = "WorkerUpgrade"
    UpgradeStepComponent    UpgradeStepType = "ComponentUpgrade"
    UpgradeStepPostCheck    UpgradeStepType = "PostCheck"
)
```
#### 4.4.2 ClusterVersionReconciler 状态机
```
┌───────────┐    desiredVersion != currentVersion     ┌─────────────┐
│ Available │ ──────────────────────────────────────→ │ Progressing │
└───────────┘                                         └──────┬──────┘
      ↑                                                      │
      │ 升级成功                                             │ 升级失败
      │                                                      ↓
      │                                               ┌──────────┐
      │                                               │ Degraded │
      │                                               └────┬─────┘
      │                                                     │
      │                              rollbackOnFailure=true │
      │                                                     ↓
      │                                                ┌────────────┐
      └─────────────────────────────────────────────── │ RollingBack│
           回滚完成                                    └────────────┘
```
#### 4.4.3 ClusterOrchestrator 调谐逻辑
```go
// pkg/orchestrator/cluster_orchestrator.go

func (o *ClusterOrchestrator) Reconcile(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // 1. 根据 BKECluster Spec 生成/更新 ComponentVersion 和 NodeConfig 列表
    if err := o.syncDesiredState(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }

    // 2. 获取所有 ComponentVersion
    components, err := o.listComponentVersions(ctx, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 3. DAG 调度：计算就绪、阻塞、运行中的组件
    completed := o.getCompletedComponents(components)
    scheduleResult := o.scheduler.Schedule(components, completed)

    // 4. 执行就绪组件的安装/升级
    for _, comp := range scheduleResult.Ready {
        executor := o.executors[comp.Spec.ComponentName]
        result, err := o.executeComponent(ctx, executor, comp)
        if err != nil {
            return result, err
        }
    }

    // 5. 同步状态回 BKECluster
    if err := o.syncStatusToBKECluster(ctx, bkeCluster, components); err != nil {
        return ctrl.Result{}, err
    }

    // 6. 判断是否全部完成
    if len(scheduleResult.Ready) == 0 && len(scheduleResult.Blocked) == 0 
       && len(scheduleResult.Running) == 0 {
        return ctrl.Result{}, nil
    }

    return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
```
#### 4.4.4 版本信息流转
```
用户修改 BKECluster.Spec.ClusterConfig.Cluster
         │
         ▼
BKECluster Controller 检测到版本变化
         │
         ▼
同步到 ClusterVersion.Spec.DesiredComponentVersions
         │
         ▼
CVO Controller 检测到 ClusterVersion 变化
         │
         ▼
DAG Scheduler 计算可执行组件
         │
         ▼
Upgrader 执行升级，更新 ClusterVersion.Status.ComponentStatuses
         │
         ▼
同步回 BKECluster.Status 的版本字段
```
#### 4.4.5 资源关系总览
```
BKECluster (Owner)
    │
    ├── ClusterVersion (1:1) ─── 全局版本状态
    │
    ├── ComponentVersion[] (1:N) ─── 各组件版本定义
    │   ├── bkeAgent-v1.0.0
    │   ├── nodesEnv-v1.0.0
    │   ├── clusterAPI-v1.0.0
    │   ├── certs-v1.0.0
    │   ├── loadBalancer-v1.0.0
    │   ├── kubernetes-v1.29.0
    │   ├── containerd-v1.7.2
    │   ├── etcd-v3.5.12
    │   ├── addon-v1.0.0
    │   ├── openFuyao-v1.1.0
    │   ├── bkeProvider-v1.1.0
    │   ├── nodesPostProcess-v1.0.0
    │   └── agentSwitch-v1.0.0
    │
    └── NodeConfig[] (1:N) ─── 节点级配置
        ├── node-192.168.1.10 (master)
        ├── node-192.168.1.11 (master)
        └── node-192.168.1.20 (worker)
```
## 5. 迁移策略
### 5.1 四阶段渐进式迁移
| 阶段 | 目标 | 改动范围 | Feature Gate |
|------|------|----------|-------------|
| **Phase A** | 定义 CRD + Controller 骨架，不修改现有 PhaseFlow | 新增 `api/cvo/`、`api/nodecomponent/`、`pkg/cvo/`、`pkg/orchestrator/` | `DeclarativeOrchestration=false` |
| **Phase B** | 实现 ClusterOrchestrator + DAGScheduler + 各 ComponentExecutor/Upgrader，内部复用现有 Phase 核心函数 | 新增执行器实现，提取现有 Phase 核心函数为公共函数 | `DeclarativeOrchestration=true`（可选开启） |
| **Phase C** | 移除 PhaseFlow 中的 DeployPhases 和 PostDeployPhases，完全由声明式编排接管 | 修改 `phase_flow.go`，移除旧 Phase | `DeclarativeOrchestration=true`（默认开启） |
| **Phase D** | 清理旧 Phase 代码，统一版本状态到 ClusterVersion | 移除旧 Phase 文件 | 不可逆 |
### 5.2 双轨运行架构
过渡期（Phase B）支持新旧路径并行：
```go
func (p *PhaseFlow) Execute() (ctrl.Result, error) {
    defer p.handlePanic()
    
    if p.useDeclarativeOrchestration {
        return p.orchestrator.Reconcile(p.ctx)
    }
    
    // 原有逻辑
    phases := p.determinePhases()
    go p.ctx.WatchBKEClusterStatus()
    return p.executePhases(phases)
}
```
## 6. 目录结构
```
cluster-api-provider-bke/
├── api/
│   ├── cvo/v1beta1/                          # 新增：CVO API
│   │   ├── clusterversion_types.go
│   │   ├── groupversion_info.go
│   │   └── zz_generated.deepcopy.go
│   └── nodecomponent/v1alpha1/               # 新增：组件 API
│       ├── componentversion_types.go
│       ├── nodeconfig_types.go
│       ├── groupversion_info.go
│       └── zz_generated.deepcopy.go
├── controllers/
│   └── cvo/                                  # 新增：CVO Controller
│       └── clusterversion_controller.go
├── pkg/
│   ├── cvo/                                  # 新增：CVO 核心
│   │   ├── orchestrator/
│   │   │   └── orchestrator.go
│   │   ├── scheduler/
│   │   │   └── dag_scheduler.go
│   │   ├── upgrader/
│   │   │   ├── interface.go
│   │   │   ├── provider_upgrader.go
│   │   │   ├── agent_upgrader.go
│   │   │   ├── containerd_upgrader.go
│   │   │   ├── etcd_upgrader.go
│   │   │   ├── kubernetes_upgrader.go
│   │   │   └── openfuyao_upgrader.go
│   │   ├── validator/
│   │   │   └── version_validator.go
│   │   └── rollback/
│   │       └── rollback_manager.go
│   └── orchestrator/                         # 新增：安装编排
│       ├── cluster_orchestrator.go
│       ├── executor/
│       │   ├── interface.go
│       │   ├── bke_agent_executor.go
│       │   ├── nodes_env_executor.go
│       │   ├── cluster_api_executor.go
│       │   ├── certs_executor.go
│       │   ├── loadbalancer_executor.go
│       │   ├── kubernetes_executor.go
│       │   ├── addon_executor.go
│       │   ├── nodes_postprocess_executor.go
│       │   └── agent_switch_executor.go
│       └── scheduler/
│           └── dag_scheduler.go
```
## 7. 实施计划
| 步骤 | 内容 | 前置条件 | 周期 |
|------|------|---------|------|
| **S1** | ComponentVersion + NodeConfig CRD 定义 + DeepCopy | 无 | 3天 |
| **S2** | ClusterVersion CRD 定义 + DeepCopy | 无 | 2天 |
| **S3** | DAGScheduler 实现 | S1 | 3天 |
| **S4** | 安装 ComponentExecutor 实现（11个） | S1, S3 | 8天 |
| **S5** | ClusterOrchestrator 安装编排实现 | S3, S4 | 5天 |
| **S6** | 升级 Upgrader 实现（7个） | S1, S3 | 8天 |
| **S7** | ClusterVersionReconciler + 状态机 | S2, S6 | 5天 |
| **S8** | VersionValidator（简化版） | S2 | 3天 |
| **S9** | RollbackManager | S7 | 3天 |
| **S10** | 双轨运行 + Feature Gate | S5, S7 | 3天 |
| **S11** | 集成测试 + E2E 测试 | S10 | 5天 |
| **总计** | | | **48天** |
## 8. 风险评估
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 安装Phase重构范围大（11个Phase） | 高 | 分批实现：先实现节点级（4个），再实现集群级（7个） |
| 升级过程中状态不一致 | 高 | ClusterVersion CR 持久化升级进度，支持断点续升 |
| 现有Phase核心函数提取困难 | 中 | 优先复用，必要时通过适配层包装 |
| DAG依赖关系变更导致升级顺序错误 | 高 | 依赖关系通过CRD声明式配置，支持运行时更新 |
| 并行升级导致集群不可用 | 中 | KubernetesUpgrader内部仍串行执行（先Master后Worker） |
| 回滚失败 | 高 | 回滚前强制etcd备份，回滚操作幂等设计 |
| 双轨运行期间状态冲突 | 中 | Feature Gate严格互斥，同一时刻仅一条路径生效 |
## 9. 验收标准
1. **层次一验收**：7个升级Phase全部重构为Upgrader，升级行为与原Phase一致
2. **层次二验收**：11个安装Phase全部重构为ComponentExecutor，安装行为与原Phase一致
3. **层次三验收**：
   - ClusterVersion CRD 创建后可自动检测版本差异并触发升级
   - 升级流程支持 PreCheck → 升级步骤 → PostCheck 完整链路
   - 升级失败且 `rollbackOnFailure=true` 时自动触发回滚
   - 升级历史可追溯（`status.history` 记录完整）
4. **整体验收**：
   - 所有现有功能通过集成测试验证，无回归
   - DAG调度正确处理组件依赖关系
   - Feature Gate 可平滑切换新旧路径
   - ComponentVersion + NodeConfig 声明式模型覆盖所有安装和升级场景
        

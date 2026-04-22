# KEPU-1-Phase4 平滑演进需求分解
## 一、旧架构→新架构演进核心矛盾分析
### 1.1 架构差异对照
| 维度 | 旧架构（PhaseFrame） | 新架构（声明式） | 演进矛盾 |
|------|---------------------|----------------|---------|
| **编排模型** | [PhaseFlow.Execute()](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L119) 顺序遍历 BKEPhases | DAGScheduler 拓扑排序 + 并行调度 | 顺序→并行，行为差异需验证 |
| **状态存储** | [BKECluster.Status.PhaseStatus](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go#L175) 单一数组 | ComponentVersion/NodeConfig/ClusterVersion 三个独立 CRD | 状态分散化，需双写过渡 |
| **触发机制** | [NeedExecute(old, new)](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go#L131) diff BKECluster | CRD Spec 变更触发 Controller Reconcile | 触发源从 BKECluster 变为多个 CRD |
| **执行入口** | [BKEClusterReconciler.executePhaseFlow()](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L186) 单入口 | ClusterOrchestrator + CVO Controller 双入口 | 单入口→双入口，需防竞争 |
| **函数复用** | Phase 结构体方法（如 [EnsureBKEAgent.Execute()](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go#L65)） | Executor 接口实现，内部复用 Phase 核心函数 | 需提取 Phase 核心函数为独立可复用函数 |
### 1.2 演进核心原则
1. **增量替换**：每个 Phase 独立替换为 Executor，替换一个验证一个
2. **双写验证**：过渡期新旧路径同时写入状态，对比结果一致性
3. **Feature Gate 互斥**：同一时刻仅一条路径生效，避免状态冲突
4. **函数复用优先**：Executor 内部调用现有 Phase 的核心函数，不重写逻辑
## 二、四阶段迁移路线图
```
Phase A（纯增量，零风险）
  ├── 定义 CRD（ComponentVersion/NodeConfig/ClusterVersion）
  ├── 定义接口（ComponentExecutor/Upgrader）
  ├── 实现 DAGScheduler
  └── Feature Gate 机制
  ★ 验收：CRD 可创建，Controller 可启动，不影响现有 PhaseFlow

Phase B（双轨运行，可选开启）
  ├── 提取 Phase 核心函数为公共函数
  ├── 实现 13 个 ComponentExecutor（安装）
  ├── 实现 ClusterOrchestrator
  ├── 实现 7 个 Upgrader（升级）
  ├── 实现 ClusterVersionReconciler
  └── 双写验证
  ★ 验收：Feature Gate=true 时声明式路径正常工作，行为与旧路径一致

Phase C（默认声明式，旧路径降级）
  ├── Feature Gate 默认改为 true
  ├── 移除 PhaseFlow 中的 DeployPhases + PostDeployPhases
  └── 控制类 Phase 保留在 Controller 中
  ★ 验收：声明式路径为默认路径，旧路径仅作降级备用

Phase D（不可逆清理）
  ├── 移除旧 Phase 文件
  ├── 统一版本状态到 ClusterVersion
  └── 移除 Feature Gate 代码
  ★ 验收：旧代码完全移除，编译通过，全量 E2E 通过
```
## 三、需求分解（共 24 个需求项）
### 🏗️ Phase A：纯增量，零风险（6 个需求）
#### R01：Feature Gate 基础设施
| 字段 | 值 |
|------|-----|
| **工作量** | 0.5 人月 |
| **前置依赖** | 无 |
| **范围** | 在 `utils/featuregate/` 下实现 Feature Gate 框架：定义 `DeclarativeOrchestration` gate（默认 false），支持命令行参数 `--feature-gates=DeclarativeOrchestration=true` 和 ConfigMap 动态切换，提供 `featuregate.Enabled(gate)` 查询接口 |
| **交付物** | ① `utils/featuregate/feature_gate.go` ② `utils/featuregate/feature_gate_test.go` ③ 命令行参数集成 |
| **验收标准** | 1) 默认 `DeclarativeOrchestration=false`；2) 命令行 `--feature-gates=DeclarativeOrchestration=true` 可开启；3) `featuregate.Enabled(DeclarativeOrchestration)` 返回正确值；4) 运行时通过 ConfigMap 切换后下次 Reconcile 生效；5) 不影响现有任何代码路径 |
#### R02：ComponentVersion + NodeConfig CRD 定义
| 字段 | 值 |
|------|-----|
| **工作量** | 0.5 人月 |
| **前置依赖** | 无 |
| **范围** | 定义 `api/nodecomponent/v1alpha1/` 下 ComponentVersion 和 NodeConfig 两个 CRD，包含提案 3.3.1/3.3.2 中全部字段。ComponentName 枚举覆盖 13 个组件，ComponentPhase 包含 Pending/Installing/Installed/Upgrading/Failed/RollingBack |
| **交付物** | ① `componentversion_types.go` ② `nodeconfig_types.go` ③ `groupversion_info.go` ④ CRD YAML ⑤ `make generate && make manifests` 通过 |
| **验收标准** | 1) CRD 可通过 kubectl apply 创建；2) 所有字段与提案一致；3) DeepCopy 代码生成正确；4) ComponentName 枚举包含：bkeAgent/nodesEnv/containerd/etcd/kubernetes/clusterAPI/certs/loadBalancer/addon/openFuyao/bkeProvider/nodesPostProcess/agentSwitch；5) NodeConfig 包含 Connection/OS/Components/Roles 完整字段 |
#### R03：ClusterVersion CRD 定义
| 字段 | 值 |
|------|-----|
| **工作量** | 0.4 人月 |
| **前置依赖** | 无 |
| **范围** | 定义 `api/cvo/v1beta1/` 下 ClusterVersion CRD，包含 Spec（DesiredVersion/DesiredComponentVersions/UpgradeStrategy/RollbackOnFailure 等）和 Status（Phase 状态机/CurrentUpgrade/History/ComponentStatuses 等） |
| **交付物** | ① `clusterversion_types.go` ② `groupversion_info.go` ③ CRD YAML |
| **验收标准** | 1) CRD 可通过 kubectl apply 创建；2) ClusterVersionPhase 包含 Available/Progressing/Degraded/RollingBack；3) UpgradeStepType 包含提案中全部步骤；4) ComponentVersions 包含 Kubernetes/Etcd/Containerd/OpenFuyao/BKEAgent/BKEProvider/Extra |
#### R04：DAGScheduler 依赖调度引擎
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R02 |
| **范围** | 实现 `pkg/orchestrator/scheduler/dag_scheduler.go` 和 `pkg/cvo/scheduler/dag_scheduler.go`，包含安装 DAG 和升级 DAG 两套依赖图，拓扑排序、就绪计算、循环依赖检测、并行度控制 |
| **交付物** | ① DAGScheduler 实现 ② InstallDependencyGraph ③ UpgradeDependencyGraph ④ ScheduleResult ⑤ 单元测试（覆盖率 ≥ 80%） |
| **验收标准** | 1) 安装 DAG 依赖与提案 4.4 一致（13 个组件节点）；2) 升级 DAG 依赖正确（BKEProvider→BKEAgent→Containerd/Etcd→Kubernetes→OpenFuyao）；3) 拓扑排序结果正确；4) 循环依赖检测可报错；5) 已完成组件不重复调度；6) maxParallelNodes 参数生效；7) 单元测试覆盖正常/部分完成/全部阻塞/循环依赖场景 |
#### R05：ComponentExecutor + Upgrader 接口定义及注册机制
| 字段 | 值 |
|------|-----|
| **工作量** | 0.5 人月 |
| **前置依赖** | R02 |
| **范围** | 定义 `pkg/orchestrator/executor/interface.go` 中 ComponentExecutor 接口（Name/Scope/Install/Upgrade/Rollback/HealthCheck），定义 `pkg/cvo/upgrader/interface.go` 中 Upgrader 接口（Name/Scope/Dependencies/NeedUpgrade/Upgrade/Rollback/HealthCheck/CurrentVersion），实现 ExecutorRegistry 和 UpgraderRegistry |
| **交付物** | ① `executor/interface.go` ② `upgrader/interface.go` ③ `executor/registry.go` ④ `upgrader/registry.go` ⑤ 单元测试 |
| **验收标准** | 1) ComponentExecutor 包含 6 个方法；2) Upgrader 包含 8 个方法；3) Registry 支持 Register/Lookup/List；4) 13 个组件名均有枚举值；5) 接口可被 mock |
#### R06：PhaseFlow 入口 Feature Gate 分流
| 字段 | 值 |
|------|-----|
| **工作量** | 0.3 人月 |
| **前置依赖** | R01 |
| **范围** | 在 [BKEClusterReconciler.executePhaseFlow()](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L186) 入口实现 Feature Gate 分流：`DeclarativeOrchestration=false` 走旧 PhaseFlow，`true` 走 ClusterOrchestrator（Phase B 实现后生效） |
| **交付物** | ① 修改 `bkecluster_controller.go` ② 修改 `phase_flow.go` |
| **验收标准** | 1) Feature Gate=false 时走旧路径，行为完全不变；2) Feature Gate=true 时走新路径（当前仅打日志，Phase B 后接入 ClusterOrchestrator）；3) 两条路径互斥；4) 编译通过 |
### 🔧 Phase B-1：安装路径重构（6 个需求）
#### R07：Phase 核心函数提取 — BKEAgent + NodesEnv
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R02, R05 |
| **范围** | 从 [ensure_bke_agent.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go)（725 行）和 [ensure_nodes_env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go) 中提取核心业务逻辑为独立函数（如 `PushBKEAgentToNodes`、`InstallNodeEnvPackages`），放在 `pkg/phaseframe/phaseutil/` 或新的 `pkg/operations/` 包中。原有 Phase 改为调用提取后的函数，行为不变 |
| **交付物** | ① 提取的公共函数 ② 修改后的原 Phase（调用公共函数） ③ 单元测试 ④ 原有 Phase 行为回归测试 |
| **验收标准** | 1) 提取的函数不依赖 PhaseContext，仅依赖显式参数（client.Client、BKECluster、Nodes 等）；2) 原有 Phase 行为不变（回归测试通过）；3) 函数签名清晰，可被 Executor 直接调用；4) `PushBKEAgentToNodes(ctx, client, bkeCluster, nodes, kubeConfig)` 可独立调用 |
#### R08：Phase 核心函数提取 — Kubernetes（MasterInit/Join/WorkerJoin）
| 字段 | 值 |
|------|-----|
| **工作量** | 1.8 人月 |
| **前置依赖** | R02, R05 |
| **范围** | 从 [ensure_master_init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go)（~500 行）、[ensure_master_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go)、[ensure_worker_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go) 中提取核心逻辑。这三个 Phase 都通过 Agent 下发 Kubeadm Plugin 命令，提取为 `InitKubernetesMaster`、`JoinKubernetesMaster`、`JoinKubernetesWorker` 独立函数 |
| **交付物** | ① 提取的公共函数 ② 修改后的原 Phase ③ 单元测试 ④ 回归测试 |
| **验收标准** | 1) 提取的函数不依赖 PhaseContext；2) 原有 Phase 行为不变；3) 函数可被 KubernetesExecutor 直接调用；4) InitMaster/JoinMaster/JoinWorker 三个函数可独立调用 |
#### R09：Phase 核心函数提取 — 其余 8 个安装 Phase
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R02, R05 |
| **范围** | 从以下 Phase 提取核心函数：[ensure_cluster_api_obj.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster_api_obj.go)、[ensure_certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_certs.go)、[ensure_load_balance.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_load_balance.go)、[ensure_addon_deploy.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go)（51K 最大）、[ensure_nodes_postprocess.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_postprocess.go)、[ensure_agent_switch.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_agent_switch.go)、[ensure_containerd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go)、[ensure_cluster_manage.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster_manage.go)（47K） |
| **交付物** | ① 每个Phase提取的公共函数 ② 修改后的原 Phase ③ 单元测试 ④ 回归测试 |
| **验收标准** | 1) 每个提取的函数不依赖 PhaseContext；2) 原有 Phase 行为不变；3) AddonDeploy 提取为 `InstallAddons` + `UpgradeAddons` 两个函数；4) ClusterManage 提取为 `MergeExistingCluster` 函数 |
#### R10：13 个 ComponentExecutor 实现（安装路径）
| 字段 | 值 |
|------|-----|
| **工作量** | 1.8 人月 |
| **前置依赖** | R07, R08, R09 |
| **范围** | 实现 `pkg/orchestrator/executor/` 下 13 个 Executor，每个 Executor 的 Install/Upgrade/Rollback/HealthCheck 方法调用 R07~R09 提取的公共函数。按提案 4.2 映射关系：bkeAgent/nodesEnv/nodesPostProcess/containerd/etcd/kubernetes/clusterAPI/certs/loadBalancer/addon/openFuyao/bkeProvider/agentSwitch |
| **交付物** | ① 13 个 Executor 实现 ② 每个 Executor 的单元测试 |
| **验收标准** | 1) 每个 Executor 实现完整的 ComponentExecutor 接口；2) Install 行为与原 Phase 一致（通过对比测试验证）；3) HealthCheck 可检测组件运行状态；4) Scope 标记正确（Node/Cluster）；5) Executor 不直接操作 BKECluster.Status，仅操作 ComponentVersion.Status |
#### R11：ClusterOrchestrator 安装编排核心
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R04, R10 |
| **范围** | 实现 `pkg/orchestrator/cluster_orchestrator.go`，包含：syncDesiredState（从 BKECluster Spec 生成 ComponentVersion + NodeConfig）、listComponentVersions、getCompletedComponents、executeComponent、syncStatusToBKECluster 完整调谐循环 |
| **交付物** | ① ClusterOrchestrator 实现 ② 集成测试 |
| **验收标准** | 1) 根据 BKECluster Spec 正确生成 13 个 ComponentVersion CR；2) 根据 BKECluster Nodes 正确生成 NodeConfig CR；3) DAGScheduler 调度顺序与原 PhaseFlow 一致；4) 已完成组件不重复执行；5) 执行结果正确回写 BKECluster Status；6) RequeueAfter 机制正确；7) 全部完成时不再重调谐 |
#### R12：安装路径双写验证 + Feature Gate 接入
| 字段 | 值 |
|------|-----|
| **工作量** | 1.0 人月 |
| **前置依赖** | R06, R11 |
| **范围** | 将 R06 中的 Feature Gate=true 分流接入 ClusterOrchestrator。实现双写验证：旧路径执行时同时生成 ComponentVersion CR（但不执行），对比新旧路径的计算结果是否一致。实现安装流程 E2E 测试 |
| **交付物** | ① Feature Gate 接入代码 ② 双写验证逻辑 ③ 安装 E2E 测试 |
| **验收标准** | 1) Feature Gate=true 时安装流程走 ClusterOrchestrator；2) Feature Gate=false 时旧路径正常工作；3) 双写验证：旧路径执行后，对比 ClusterOrchestrator 计算的 ComponentVersion 状态与 BKECluster.Status.PhaseStatus 一致；4) 全新安装 E2E 测试通过（单 Master + Worker）；5) HA 安装 E2E 测试通过（3 Master + Worker） |
### 🔄 Phase B-2：升级路径重构（5 个需求）
#### R13：Phase 核心函数提取 — 7 个升级 Phase
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R02, R05 |
| **范围** | 从以下 Phase 提取核心升级函数：[ensure_agent_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_agent_upgrade.go)（20K）、[ensure_containerd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go)、[ensure_etcd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_etcd_upgrade.go)（19K）、[ensure_master_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go)（20K）、[ensure_worker_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go)（16K）、[ensure_component_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_component_upgrade.go)（19K）、[ensure_provider_self_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_provider_self_upgrade.go) |
| **交付物** | ① 提取的公共升级函数 ② 修改后的原 Phase ③ 单元测试 ④ 回归测试 |
| **验收标准** | 1) 提取的函数不依赖 PhaseContext；2) 原有 Phase 行为不变；3) MasterUpgrade + WorkerUpgrade 提取为 `UpgradeKubernetesMaster` + `UpgradeKubernetesWorker`；4) EtcdUpgrade 提取为 `UpgradeEtcdCluster`；5) 函数可被 Upgrader 直接调用 |
#### R14：7 个 Upgrader 实现（升级路径）
| 字段 | 值 |
|------|-----|
| **工作量** | 1.8 人月 |
| **前置依赖** | R13 |
| **范围** | 实现 `pkg/cvo/upgrader/` 下 7 个 Upgrader：BKEProviderUpgrader、BKEAgentUpgrader、ContainerdUpgrader、EtcdUpgrader、KubernetesUpgrader（含内部步骤状态机：PreCheck→EtcdBackup→MasterRollout→WorkerRollout→PostCheck）、OpenFuyaoUpgrader。每个 Upgrader 调用 R13 提取的公共函数 |
| **交付物** | ① 7 个 Upgrader 实现 ② 每个的单元测试 |
| **验收标准** | 1) 每个 Upgrader 实现完整的 Upgrader 接口；2) Upgrade 行为与原 Phase 一致；3) KubernetesUpgrader 内部步骤状态机可断点续升；4) NeedUpgrade 版本比较逻辑正确；5) Rollback 可回滚到上一版本；6) HealthCheck 可检测升级后状态 |
#### R15：ClusterVersionReconciler + 升级状态机
| 字段 | 值 |
|------|-----|
| **工作量** | 1.8 人月 |
| **前置依赖** | R03, R14 |
| **范围** | 实现 `controllers/cvo/clusterversion_controller.go`，包含 Available→Progressing→Degraded→RollingBack 状态机转换，版本差异检测触发升级，升级步骤编排（PreCheck→ProviderSelf→Agent→Containerd→Etcd→ControlPlane→Worker→Component→PostCheck），断点续升 |
| **交付物** | ① ClusterVersionReconciler ② 状态机实现 ③ 升级步骤编排 ④ 单元测试 |
| **验收标准** | 1) desiredVersion != currentVersion 时自动触发 Progressing；2) 升级步骤按 DAG 顺序执行；3) 升级失败时转入 Degraded；4) rollbackOnFailure=true 时自动触发 RollingBack；5) 升级历史记录完整；6) 断点续升：Controller 重启后从上次步骤继续 |
#### R16：VersionValidator 版本校验引擎
| 字段 | 值 |
|------|-----|
| **工作量** | 0.8 人月 |
| **前置依赖** | R03 |
| **范围** | 实现 `pkg/cvo/validator/version_validator.go`，校验升级路径合法性（版本号递增、兼容性矩阵、Kubernetes-Etcd 版本对应关系），降级默认拒绝 |
| **交付物** | ① VersionValidator ② 兼容性矩阵配置 ③ 单元测试 |
| **验收标准** | 1) 不合法升级路径被拒绝；2) 兼容性矩阵可配置；3) K8s-Etcd 版本对应关系校验正确；4) 降级默认拒绝（AllowDowngrade=true 除外）；5) 校验结果写入 ClusterVersion.Status.Conditions |
#### R17：升级路径双写验证 + E2E 测试
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R12, R15 |
| **范围** | 实现升级路径双写验证，升级流程 E2E 测试：Kubernetes 版本升级、Etcd 独立升级、Containerd 升级、Agent 升级、OpenFuyao 组件升级、断点续升 |
| **交付物** | ① 升级双写验证 ② 升级 E2E 测试套件 |
| **验收标准** | 1) Feature Gate=true 时升级走 CVO 路径；2) 升级行为与旧路径一致；3) Kubernetes 小版本升级 E2E 通过；4) Etcd 独立升级 E2E 通过；5) 断点续升 E2E 通过（中途重启 Controller）；6) Feature Gate 切换不影响运行中集群 |
### 🛡️ Phase B-3：回滚与安全（3 个需求）
#### R18：RollbackManager 回滚管理器
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R15 |
| **范围** | 实现 `pkg/cvo/rollback/rollback_manager.go`，支持升级失败后自动/手动回滚，回滚前强制 etcd 备份，回滚步骤逆向执行 DAG，回滚操作幂等设计 |
| **交付物** | ① RollbackManager ② 回滚 DAG ③ 幂等性保证 ④ 单元测试 |
| **验收标准** | 1) rollbackOnFailure=true 时升级失败自动触发回滚；2) 回滚前自动 etcd 备份；3) 回滚步骤按升级 DAG 逆序执行；4) 回滚操作幂等；5) 回滚完成后 ClusterVersion 回到 Available；6) 回滚失败时状态为 Degraded |
#### R19：升级 PreCheck + PostCheck 健康检查框架
| 字段 | 值 |
|------|-----|
| **工作量** | 0.8 人月 |
| **前置依赖** | R15 |
| **范围** | 实现升级前检查（etcd 健康、节点 Ready、API Server 可达、资源余量）和升级后检查（组件版本正确、Pod Running、集群功能正常） |
| **交付物** | ① PreChecker ② PostChecker ③ 检查项配置 ④ 单元测试 |
| **验收标准** | 1) PreCheck 包含 etcd/节点/API Server/资源检查；2) PostCheck 包含版本/Pod/功能验证；3) PreCheck 失败阻止升级；4) PostCheck 失败触发 Degraded |
#### R20：NodeConfig Controller + 缩容逻辑
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R02 |
| **范围** | 实现 NodeConfig Controller，处理 NodeConfig 生命周期（Pending→Installing→Ready→Deleting），通过 NodeConfig phase=Deleting 触发缩容流程，替代原 EnsureWorkerDelete 和 EnsureMasterDelete。缩容逻辑复用提取的公共函数 |
| **交付物** | ① NodeConfigReconciler ② 缩容逻辑 ③ 单元测试 |
| **验收标准** | 1) NodeConfig 创建后自动进入 Installing；2) 安装完成后转为 Ready；3) NodeConfig 删除标记触发 Deleting phase；4) 缩容行为与原 EnsureWorkerDelete/EnsureMasterDelete 一致；5) 节点组件状态正确记录 |
### 🧹 Phase C + D：迁移清理（4 个需求）
#### R21：Feature Gate 默认值切换 + 控制类 Phase 保留
| 字段 | 值 |
|------|-----|
| **工作量** | 0.5 人月 |
| **前置依赖** | R12, R17 |
| **范围** | 将 `DeclarativeOrchestration` Feature Gate 默认值从 false 改为 true。将 5 个控制类 Phase（EnsureFinalizer/EnsurePaused/EnsureClusterManage/EnsureDeleteOrReset/EnsureDryRun）的逻辑从 PhaseFlow 移入 BKEClusterReconciler 的 Reconcile 入口，不再经过 PhaseFlow |
| **交付物** | ① Feature Gate 默认值修改 ② 控制类 Phase 逻辑迁移到 Controller ③ 回归测试 |
| **验收标准** | 1) 默认走声明式路径；2) 5 个控制类逻辑在 Controller 入口正确执行；3) Finalizer/Paused/DryRun/Delete/Manage 行为不变；4) 回归测试通过 |
#### R22：移除 PhaseFlow 中 DeployPhases + PostDeployPhases
| 字段 | 值 |
|------|-----|
| **工作量** | 1.0 人月 |
| **前置依赖** | R21 |
| **范围** | 从 [list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go) 中移除 DeployPhases（11 个）和 PostDeployPhases（7 个）的注册，修改 [phase_flow.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go) 的 init() 函数，仅保留 CommonPhases（控制类）。移除后 FullPhasesRegisFunc 仅包含 5 个控制类 Phase |
| **交付物** | ① 修改后的 list.go ② 修改后的 phase_flow.go ③ 回归测试 |
| **验收标准** | 1) PhaseFlow 不再包含安装/升级 Phase；2) 安装由 ClusterOrchestrator 接管；3) 升级由 CVO 接管；4) 缩容由 NodeConfig Controller 接管；5) 所有安装/升级/缩容场景回归测试通过 |
#### R23：旧 Phase 文件清理 + 版本状态统一
| 字段 | 值 |
|------|-----|
| **工作量** | 1.0 人月 |
| **前置依赖** | R22 |
| **范围** | 移除 18 个旧 Phase 文件（11 个安装 + 7 个升级）及其测试文件。将 BKECluster.Status 中的版本字段（OpenFuyaoVersion/KubernetesVersion/EtcdVersion/ContainerdVersion）统一从 ClusterVersion.Status.CurrentComponents 读取，实现版本状态单一数据源 |
| **交付物** | ① 移除的文件清单 ② BKECluster Status 版本字段迁移 ③ 回归测试 |
| **验收标准** | 1) 18 个旧 Phase 文件已移除；2) 编译通过；3) BKECluster.Status 版本字段从 ClusterVersion 同步；4) kubectl get bkecluster 仍可看到版本信息；5) 版本信息与 ClusterVersion 一致 |
#### R24：全量 E2E 回归测试 + Feature Gate 移除
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R23 |
| **范围** | 编写完整 E2E 测试套件覆盖全生命周期：全新安装、扩容、缩容、升级、回滚、断点续升。确认所有测试通过后，移除 Feature Gate 代码（不可逆） |
| **交付物** | ① E2E 测试套件 ② 测试矩阵 ③ Feature Gate 代码移除 ④ 回归测试报告 |
| **验收标准** | 1) 全新安装 E2E 通过（单 Master + HA）；2) 扩容 E2E 通过（加 Master/Worker）；3) 缩容 E2E 通过（减 Worker/Master）；4) Kubernetes 升级 E2E 通过；5) Etcd 独立升级 E2E 通过；6) 升级失败 + 自动回滚 E2E 通过；7) 断点续升 E2E 通过；8) Feature Gate 代码已移除；9) 编译通过 |
## 四、依赖关系与执行顺序
```
批次1（可并行，无依赖）：
  R01: Feature Gate 基础设施   ──────────┐
  R02: ComponentVersion + NodeConfig CRD ├── 约 1.9 人月
  R03: ClusterVersion CRD                │
                                         │
批次2（依赖 R01/R02/R03）：
  R04: DAGScheduler   ───────────────────┤
  R05: ComponentExecutor + Upgrader 接口 ├── 约 2.5 人月
  R06: PhaseFlow Feature Gate 分流       │
                                         │
批次3（依赖 R02/R05，可并行）：
  R07: 提取 BKEAgent + NodesEnv 函数    ─┤
  R08: 提取 Kubernetes 函数              ├── 约 4.8 人月
  R09: 提取其余 8 个安装 Phase 函数      │
  R13: 提取 7 个升级 Phase 函数          │
                                         │
批次4（依赖批次3）：
  R10: 13 个 ComponentExecutor 实现     ─┤
  R14: 7 个 Upgrader 实现                ├── 约 5.1 人月
  R16: VersionValidator                  │
  R20: NodeConfig Controller             │
                                         │
批次5（依赖批次4）：
  R11: ClusterOrchestrator   ────────────┤
  R15: ClusterVersionReconciler          ├── 约 4.1 人月
  R18: RollbackManager                   │
  R19: PreCheck + PostCheck              │
                                         │
批次6（依赖批次5）：
  R12: 安装路径双写验证 + E2E   ─────────┤
  R17: 升级路径双写验证 + E2E            ├── 约 3.0 人月
                                         │
批次7（依赖批次6）：
  R21: Feature Gate 默认值切换   ────────┤
  R22: 移除DeployPhases+PostDeployPhases ├── 约 3.0 人月
  R23: 旧 Phase 文件清理                 │
  R24: 全量 E2E + Feature Gate 移除      │
```
## 五、工作量汇总
| 迁移阶段 | 需求编号 | 合计人月 | 风险等级 |
|---------|---------|---------|---------|
| **Phase A**（纯增量） | R01~R06 | 3.7 | 🟢 低 |
| **Phase B-1**（安装重构） | R07~R12 | 9.1 | 🟡 中 |
| **Phase B-2**（升级重构） | R13~R17 | 7.4 | 🟠 中高 |
| **Phase B-3**（回滚+缩容） | R18~R20 | 3.8 | 🟡 中 |
| **Phase C+D**（清理） | R21~R24 | 4.0 | 🟢 低 |
| **总计** | **24 个需求** | **28.0 人月** | |
## 六、关键平滑演进策略
### 6.1 核心函数提取模式
每个 Phase 的重构遵循统一模式，以 [EnsureBKEAgent](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go) 为例：
```
重构前：
  EnsureBKEAgent.Execute() → 直接操作 PhaseContext + SSH 推送
重构步骤1（R07）：
  提取 PushBKEAgentToNodes(ctx, client, bkeCluster, nodes, kubeConfig) → 独立函数
  EnsureBKEAgent.Execute() → 调用 PushBKEAgentToNodes()  ← 行为不变
重构步骤2（R10）：
  BKEAgentExecutor.Install() → 调用 PushBKEAgentToNodes()  ← 复用同一函数
```
**关键保证**：提取函数后原 Phase 行为不变（回归测试验证），Executor 调用同一函数确保行为一致。
### 6.2 双写验证策略
Phase B 过渡期采用双写验证确保新旧路径一致：
```
旧路径执行时：
  1. PhaseFlow 正常执行（写 BKECluster.Status.PhaseStatus）
  2. 同时 ClusterOrchestrator 以 dry-run 模式运行
  3. 对比 ClusterOrchestrator 计算的 ComponentVersion 状态与 PhaseStatus 是否一致

新路径执行时（Feature Gate=true）：
  1. ClusterOrchestrator 正常执行（写 ComponentVersion.Status）
  2. 同时将结果同步回 BKECluster.Status.PhaseStatus
  3. 确保外部可见状态一致
```
### 6.3 状态回写兼容
声明式路径执行后，需将结果同步回 BKECluster.Status，确保外部系统（UI/监控）无感知：
```go
func (o *ClusterOrchestrator) syncStatusToBKECluster(ctx, bkeCluster, components) {
    // 将 ComponentVersion 状态映射回 PhaseStatus
    for _, comp := range components {
        phaseName := componentToPhaseName[comp.Spec.ComponentName]
        bkeCluster.Status.PhaseStatus = updatePhaseStatus(
            bkeCluster.Status.PhaseStatus,
            phaseName,
            componentPhaseToPhaseStatus(comp.Status.Phase),
        )
    }
    // 将版本信息同步回 BKECluster.Status
    bkeCluster.Status.KubernetesVersion = getComponentVersion(components, ComponentKubernetes)
    bkeCluster.Status.EtcdVersion = getComponentVersion(components, ComponentEtcd)
    // ...
}
```
### 6.4 回滚方案
每个迁移阶段都有回滚方案：

| 阶段 | 回滚方案 |
|------|---------|
| Phase A | Feature Gate=false，无任何影响 |
| Phase B | Feature Gate 切回 false，走旧 PhaseFlow |
| Phase C | Feature Gate 切回 false，恢复 DeployPhases/PostDeployPhases 注册 |
| Phase D | 不可逆，需通过 git revert 代码回退 |
## 七、风险与缓解
| 风险 | 影响的需求 | 缓解措施 |
|------|-----------|---------|
| ensure_addon_deploy.go 代码量大，函数提取困难 | R09 | 分步提取：先提取 Helm 操作核心函数，再提取 Addon 列表遍历逻辑 |
| ensure_cluster_manage.go控制逻辑与编排逻辑深度耦合 | R09 | ClusterManage 保留在 Controller 中，不提取为 Executor |
| Kubernetes Executor 合并 3 个 Phase，状态机复杂 | R08, R10 | 严格按 K8sInstallStep 状态机实现，每步持久化到 ComponentVersion.Status |
| 双轨运行期间状态冲突 | R12, R17 | Feature Gate 严格互斥 + 状态校验 |
| 升级回滚链路长 | R18 | 回滚操作幂等 + 分层测试 |
| Phase 核心函数提取后原 Phase 行为变化 | R07~R09, R13 | 每个提取步骤必须有回归测试，确保原 Phase 行为不变 |
        




          
让我先完整阅读 KEPU-1-Phase4 提案的剩余部分：
        
            
toolName: view_files
            
status: success
          
            
filePath: d:\code\github\cluster-api-provider-bke\code\kep\KEPU-1-Phase4.md
          
文件只有1545行，我已经读完了。让我
        
            
toolName: view_files
            
status: success
          
            
filePath: d:\code\github\cluster-api-provider-bke\code\kep\KEPU-1-Phase4.md
          

        
            
toolName: view_files
            
status: success
          
            
filePath: d:\code\github\cluster-api-provider-bke\code\kep\KEPU-1-Phase4.md
          
现在让我查看现有 Phase 的具体实现，以评估重构复杂度：
        
            
toolName: file_search
            
status: success
          
          

        
            
toolName: search_by_regex
            
status: success
          
query: func.*Execute\(\).*\(
          

        
            
toolName: search_by_regex
            
status: success
          
query: func.*NeedExecute\(\)
          
现在让我查看几个关键 Phase 的代码行数，以评估复杂度：
        
            
toolName: search_by_regex
            
status: success
          
query: .
          

        
            
toolName: view_files
            
status: success
          
            
filePath: d:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_master_init.go
          

        
            
toolName: view_files
            
status: success
          
            
filePath: d:\code\github\cluster-api-provider-bke\pkg\phaseframe\phases\ensure_agent_upgrade.go
          
现在我已经充分了解了提案内容和现有代码规模。下面进行系统性的需求分解。

---

# KEPU-1-Phase4 需求分解
> **分解原则**：每个需求 ≤ 2 人月，有明确可验收标准，可独立开发测试。
## 一、总体规模评估
| 维度 | 数量 |
|------|------|
| 现有 Phase 文件 | 26 个（含测试） |
| 需重构的 Phase | 20 个 |
| 新增 CRD | 3 个（ComponentVersion、NodeConfig、ClusterVersion） |
| 新增 Executor | 11 个 |
| 新增 Upgrader | 7 个 |
| 新增 Controller | 2 个（ClusterVersion、NodeConfig） |
| 核心基础设施 | DAGScheduler、ClusterOrchestrator、RollbackManager、VersionValidator |
| 现有 Phase 代码量 | ~380K 字符（不含测试），其中 ensure_addon_deploy.go 最大（51K），ensure_cluster_manage.go 次之（47K） |
## 二、需求分解（共 22 个需求项）
### 🏗️ 基础设施层（6 个需求）
#### R01：ComponentVersion + NodeConfig CRD 定义及代码生成
| 字段 | 值 |
|------|-----|
| **工作量** | 0.5 人月 |
| **前置依赖** | 无 |
| **范围** | 定义 `api/nodecomponent/v1alpha1/` 下 ComponentVersion 和 NodeConfig 两个 CRD 的完整类型定义，包含 Spec/Status 所有字段，生成 DeepCopy、RBAC 等代码 |
| **交付物** | ① `componentversion_types.go` ② `nodeconfig_types.go` ③ `groupversion_info.go` ④ `zz_generated.deepcopy.go` ⑤ CRD YAML ⑥ 可通过 `make generate && make manifests` 成功生成 |
| **验收标准** | 1) CRD 可通过 kubectl apply 创建；2) 所有字段与提案 3.3.1/3.3.2 一致；3) DeepCopy 生成的代码编译通过；4) ComponentName 枚举覆盖全部 13 个组件；5) NodeConfig 包含 Connection/OS/Components/Roles 完整字段 |
#### R02：ClusterVersion CRD 定义及代码生成
| 字段 | 值 |
|------|-----|
| **工作量** | 0.4 人月 |
| **前置依赖** | 无 |
| **范围** | 定义 `api/cvo/v1beta1/` 下 ClusterVersion CRD，包含 Spec（DesiredVersion、DesiredComponentVersions、UpgradeStrategy、RollbackOnFailure 等）和 Status（Phase 状态机、CurrentUpgrade、History、ComponentStatuses 等），生成 DeepCopy/RBAC |
| **交付物** | ① `clusterversion_types.go` ② `groupversion_info.go` ③ `zz_generated.deepcopy.go` ④ CRD YAML |
| **验收标准** | 1) CRD 可通过 kubectl apply 创建；2) ClusterVersionPhase 包含 Available/Progressing/Degraded/RollingBack 四种状态；3) UpgradeStepType 包含提案中全部 8 种步骤；4) ComponentVersions 包含 Kubernetes/Etcd/Containerd/OpenFuyao/BKEAgent/BKEProvider/Extra；5) 编译通过 |
#### R03：DAGScheduler 依赖调度引擎
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R01 |
| **范围** | 实现 `pkg/orchestrator/scheduler/dag_scheduler.go` 和 `pkg/cvo/scheduler/dag_scheduler.go`，包含安装 DAG 和升级 DAG 两套依赖图，支持拓扑排序、就绪计算、循环依赖检测、并行度控制 |
| **交付物** | ① DAGScheduler 实现 ② InstallDependencyGraph ③ UpgradeDependencyGraph ④ ScheduleResult 结构 ⑤ 单元测试（覆盖率 ≥ 80%） |
| **验收标准** | 1) 安装 DAG 依赖关系与提案 4.4 一致（13 个组件节点）；2) 升级 DAG 依赖关系正确（BKEProvider→BKEAgent→Containerd/Etcd→Kubernetes→OpenFuyao）；3) 拓扑排序结果正确；4) 循环依赖检测可报错；5) 已完成组件不重复调度；6) 并行度参数 maxParallelNodes 生效；7) 单元测试覆盖正常调度/部分完成/全部阻塞/循环依赖场景 |
#### R04：ComponentExecutor 接口框架及注册机制
| 字段 | 值 |
|------|-----|
| **工作量** | 0.5 人月 |
| **前置依赖** | R01 |
| **范围** | 定义 `pkg/orchestrator/executor/interface.go` 中的 ComponentExecutor 接口（Install/Upgrade/Rollback/HealthCheck），实现 ExecutorRegistry 注册/查找机制，定义 ComponentName 枚举和 ComponentScope 枚举 |
| **交付物** | ① `interface.go` ② `registry.go` ③ 枚举定义 ④ 单元测试 |
| **验收标准** | 1) ComponentExecutor 接口包含 Name/Scope/Install/Upgrade/Rollback/HealthCheck 5 个方法；2) Registry 支持 Register/Lookup/List 操作；3) 13 个组件名均有枚举值；4) 编译通过且接口可被 mock |
#### R05：ClusterOrchestrator 安装编排核心
| 字段 | 值 |
|------|-----|
| **工作量** | 1.8 人月 |
| **前置依赖** | R03, R04 |
| **范围** | 实现 `pkg/orchestrator/cluster_orchestrator.go`，包含 syncDesiredState（从 BKECluster Spec 生成 ComponentVersion + NodeConfig）、listComponentVersions、getCompletedComponents、executeComponent、syncStatusToBKECluster 完整调谐循环 |
| **交付物** | ① ClusterOrchestrator 实现 ② syncDesiredState 逻辑 ③ 状态回写逻辑 ④ 集成测试 |
| **验收标准** | 1) 根据 BKECluster Spec 可正确生成 13 个 ComponentVersion CR；2) 根据 BKECluster Nodes 可正确生成 NodeConfig CR；3) 已完成组件不重复执行；4) 执行结果正确回写 BKECluster Status；5) RequeueAfter 机制正确（有未完成组件时 5s 重调谐）；6) 全部完成时不再重调谐；7) 集成测试覆盖：空集群首次安装/部分完成断点续装/全部完成 |
#### R06：Feature Gate 双轨运行机制
| 字段 | 值 |
|------|-----|
| **工作量** | 0.5 人月 |
| **前置依赖** | R05 |
| **范围** | 在 PhaseFlow.Execute 入口实现 Feature Gate 分流，`DeclarativeOrchestration=false` 走旧 PhaseFlow，`true` 走 ClusterOrchestrator，确保同一时刻仅一条路径生效 |
| **交付物** | ① Feature Gate 定义 ② PhaseFlow 入口改造 ③ 配置文档 |
| **验收标准** | 1) Feature Gate 默认 false，走旧路径；2) 设为 true 后走 ClusterOrchestrator；3) 两条路径互斥，不存在同时执行；4) 可通过命令行参数 / ConfigMap 动态切换；5) 切换时不影响已运行中的流程 |
### 🔧 节点级安装 Executor（5 个需求）
#### R07：BKEAgent + NodesEnv Executor（节点基础环境）
| 字段 | 值 |
|------|-----|
| **工作量** | 1.8 人月 |
| **前置依赖** | R04 |
| **范围** | 实现 `bke_agent_executor.go` 和 `nodes_env_executor.go`，从 [ensure_bke_agent.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_bke_agent.go) 和 [ensure_nodes_env.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go) 提取核心逻辑，封装为 ComponentExecutor 接口实现 |
| **交付物** | ① BKEAgentExecutor ② NodesEnvExecutor ③ 单元测试 ④ 集成测试 |
| **验收标准** | 1) BKEAgent Install 行为与原 EnsureBKEAgent.Execute 一致（SSH 推送 + 启动服务）；2) NodesEnv Install 行为与原 EnsureNodesEnv.Execute 一致（安装 lxcfs/nfs-utils/etcdctl/helm 等）；3) HealthCheck 可检测 Agent 运行状态；4) 升级路径可更新 Agent 二进制并重启；5) 复用现有 phaseutil 中的 SSH 推送函数，不重写 |
#### R08：Containerd + LoadBalancer Executor
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R04 |
| **范围** | 实现 `containerd_executor.go`（从 [ensure_containerd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go) 提取）和 `loadbalancer_executor.go`（从 [ensure_load_balance.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_load_balance.go) 提取） |
| **交付物** | ① ContainerdExecutor ② LoadBalancerExecutor ③ 单元测试 |
| **验收标准** | 1) Containerd Install/Upgrade 行为与原 Phase 一致（安装/升级 containerd + 配置 config.toml）；2) LoadBalancer Install 行为与原 Phase 一致（配置 HAProxy/keepalived）；3) Containerd Upgrade 支持停止→备份→替换→启动→验证流程；4) HealthCheck 可检测 containerd/haproxy 服务状态 |
#### R09：Kubernetes Executor（MasterInit + MasterJoin + WorkerJoin）
| 字段 | 值 |
|------|-----|
| **工作量** | 2.0 人月 |
| **前置依赖** | R04, R07 |
| **范围** | 实现 `kubernetes_executor.go`，合并 EnsureMasterInit + EnsureMasterJoin + EnsureWorkerJoin 三个 Phase，内部步骤状态机：MasterInit → MasterJoin(逐节点) → WorkerJoin(逐节点)。从 [ensure_master_init.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go)（18K 代码）、[ensure_master_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go)、[ensure_worker_join.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go) 提取核心逻辑 |
| **交付物** | ① KubernetesExecutor ② K8sInstallStep 状态机 ③ 单元测试 ④ 集成测试 |
| **验收标准** | 1) Install 行为与原三个 Phase 顺序执行一致；2) 内部步骤状态机可断点续执行（MasterInit 完成后不再重复）；3) 通过 Agent 下发 Kubeadm Plugin 命令的逻辑不变；4) 等待节点 NotReady→Ready 的轮询逻辑不变；5) ComponentVersion Status 记录当前步骤和进度 |
#### R10：NodesPostProcess + AgentSwitch Executor
| 字段 | 值 |
|------|-----|
| **工作量** | 1.0 人月 |
| **前置依赖** | R04, R09 |
| **范围** | 实现 `nodes_postprocess_executor.go` 和 `agent_switch_executor.go`，从 [ensure_nodes_postprocess.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_postprocess.go) 和 [ensure_agent_switch.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_agent_switch.go) 提取 |
| **交付物** | ① NodesPostProcessExecutor ② AgentSwitchExecutor ③ 单元测试 |
| **验收标准** | 1) NodesPostProcess Install 行为与原 Phase 一致（执行后处理脚本）；2) AgentSwitch Install 行为与原 Phase 一致（切换 Agent kubeconfig 指向目标集群）；3) AgentSwitch 无升级路径（按提案设计） |
### 🏛️ 集群级安装 Executor（2 个需求）
#### R11：ClusterAPI + Certs Executor
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R04 |
| **范围** | 实现 `cluster_api_executor.go` 和 `certs_executor.go`，从 [ensure_cluster_api_obj.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_cluster_api_obj.go) 和 [ensure_certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_certs.go) 提取。ClusterAPI 负责创建 Cluster/Machine/KubeadmControlPlane 等 CAPI 资源；Certs 负责生成 CA/etcd-ca/front-proxy-ca/SA 证书 |
| **交付物** | ① ClusterAPIExecutor ② CertsExecutor ③ 单元测试 |
| **验收标准** | 1) ClusterAPI Install 行为与原 EnsureClusterAPIObj 一致（创建 CAPI 对象）；2) ClusterAPI Upgrade 行为与原 Phase 一致（更新 Machine 副本数）；3) Certs Install 行为与原 EnsureCerts 一致（生成证书 Secret）；4) Certs Upgrade 支持证书续期；5) Scope=Cluster 正确标记 |
#### R12：Addon + BKEProvider Executor
| 字段 | 值 |
|------|-----|
| **工作量** | 2.0 人月 |
| **前置依赖** | R04, R09 |
| **范围** | 实现 `addon_executor.go` 和 `bke_provider_executor.go`。Addon 是最大的 Phase（[ensure_addon_deploy.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go) 51K 代码），涉及 Helm 安装多个 Addon；BKEProvider 负责部署 Provider Deployment |
| **交付物** | ① AddonExecutor ② BKEProviderExecutor ③ 单元测试 ④ 集成测试 |
| **验收标准** | 1) Addon Install 行为与原 EnsureAddonDeploy 一致（Helm install 各 Addon）；2) Addon Upgrade 行为与原 Phase 一致（Helm upgrade）；3) BKEProvider Install 行为与原 EnsureProviderSelfUpgrade 一致（部署 Provider Deployment）；4) BKEProvider Upgrade 行为与原 Phase 一致（Patch Deployment 镜像 tag）；5) Addon 列表可从 BKECluster.Spec 动态获取 |
### 🔄 升级 Upgrader（3 个需求）
#### R13：BKEAgent + Containerd Upgrader
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R01, R04 |
| **范围** | 实现 `pkg/cvo/upgrader/agent_upgrader.go` 和 `containerd_upgrader.go`，从 [ensure_agent_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_agent_upgrade.go)（20K 代码）和 [ensure_containerd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go) 提取升级逻辑 |
| **交付物** | ① AgentUpgrader ② ContainerdUpgrader ③ 单元测试 |
| **验收标准** | 1) Agent 升级行为与原 EnsureAgentUpgrade 一致（DaemonSet 滚动更新）；2) Agent 升级等待 DaemonSet Ready 逻辑不变；3) Containerd 升级行为与原 EnsureContainerdUpgrade 一致（逐节点停止→备份→替换→启动→验证）；4) 升级前版本检查（needUpgrade 判断）逻辑不变；5) 升级进度写入 ComponentVersion.Status |
#### R14：Etcd + Kubernetes Upgrader
| 字段 | 值 |
|------|-----|
| **工作量** | 2.0 人月 |
| **前置依赖** | R01, R04, R13 |
| **范围** | 实现 `etcd_upgrader.go` 和 `kubernetes_upgrader.go`，从 [ensure_etcd_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_etcd_upgrade.go)（19K）、[ensure_master_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go)（20K）、[ensure_worker_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go)（16K）提取。Kubernetes Upgrader 内部串行执行：先 Master 后 Worker |
| **交付物** | ① EtcdUpgrader ② KubernetesUpgrader ③ 单元测试 ④ 集成测试 |
| **验收标准** | 1) Etcd 升级行为与原 EnsureEtcdUpgrade 一致（逐节点升级 static pod + 等待健康）；2) Kubernetes Master 升级行为与原 EnsureMasterUpgrade 一致（逐节点 kubeadm upgrade）；3) Kubernetes Worker 升级行为与原 EnsureWorkerUpgrade 一致；4) 升级前 etcd 备份逻辑不变；5) 升级步骤状态机可断点续升；6) 升级进度写入 ClusterVersion.Status.CurrentUpgrade |
#### R15：OpenFuyao + BKEProvider Upgrader
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R01, R04 |
| **范围** | 实现 `openfuyao_upgrader.go` 和 `provider_upgrader.go`，从 [ensure_component_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_component_upgrade.go)（19K）和 [ensure_provider_self_upgrade.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_provider_self_upgrade.go) 提取 |
| **交付物** | ① OpenFuyaoUpgrader ② ProviderUpgrader ③ 单元测试 |
| **验收标准** | 1) OpenFuyao 升级行为与原 EnsureComponentUpgrade 一致（Patch ConfigMap 镜像 tag）；2) Provider 升级行为与原 EnsureProviderSelfUpgrade 一致（Patch Deployment 镜像 tag）；3) 升级前版本比较逻辑不变；4) 升级后健康检查逻辑不变 |
### 🎛️ CVO 控制器（3 个需求）
#### R16：ClusterVersionReconciler + 升级状态机
| 字段 | 值 |
|------|-----|
| **工作量** | 2.0 人月 |
| **前置依赖** | R02, R14 |
| **范围** | 实现 `controllers/cvo/clusterversion_controller.go`，包含 Available→Progressing→Degraded→RollingBack 状态机转换，版本差异检测触发升级，升级步骤编排（PreCheck→ProviderSelf→Agent→Containerd→Etcd→ControlPlane→Worker→Component→PostCheck），断点续升 |
| **交付物** | ① ClusterVersionReconciler ② 状态机实现 ③ 升级步骤编排 ④ 单元测试 |
| **验收标准** | 1) desiredVersion != currentVersion 时自动触发 Progressing；2) 升级步骤按 DAG 顺序执行；3) 升级失败时转入 Degraded；4) rollbackOnFailure=true 时自动触发 RollingBack；5) 升级历史记录完整（status.history）；6) 断点续升：Controller 重启后从上次步骤继续；7) 单元测试覆盖全部状态转换路径 |
#### R17：NodeConfig Controller + 缩容逻辑
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R01 |
| **范围** | 实现 NodeConfig Controller，处理 NodeConfig 生命周期（Pending→Installing→Ready→Deleting），通过 NodeConfig phase=Deleting 触发缩容流程，替代原 EnsureWorkerDelete 和 EnsureMasterDelete |
| **交付物** | ① NodeConfigReconciler ② 缩容逻辑 ③ 单元测试 |
| **验收标准** | 1) NodeConfig 创建后自动进入 Installing；2) 安装完成后转为 Ready；3) NodeConfig 删除标记触发 Deleting phase；4) 缩容行为与原 EnsureWorkerDelete/EnsureMasterDelete 一致；5) 节点组件状态（componentStatus）正确记录 |
#### R18：VersionValidator 版本校验引擎
| 字段 | 值 |
|------|-----|
| **工作量** | 1.0 人月 |
| **前置依赖** | R02 |
| **范围** | 实现 `pkg/cvo/validator/version_validator.go`，校验升级路径合法性（版本号递增、兼容性矩阵、最小升级跨度），校验组件间版本兼容性（如 Kubernetes 与 Etcd 版本对应关系） |
| **交付物** | ① VersionValidator ② 兼容性矩阵配置 ③ 单元测试 |
| **验收标准** | 1) 不合法升级路径（如跨大版本跳升）被拒绝；2) 兼容性矩阵可配置（ConfigMap/CRD）；3) Kubernetes-Etcd 版本对应关系校验正确；4) 降级默认拒绝（除非 AllowDowngrade=true）；5) 校验结果写入 ClusterVersion.Status.Conditions |
### 🛡️ 回滚与安全（2 个需求）
#### R19：RollbackManager 回滚管理器
| 字段 | 值 |
|------|-----|
| **工作量** | 1.8 人月 |
| **前置依赖** | R16 |
| **范围** | 实现 `pkg/cvo/rollback/rollback_manager.go`，支持升级失败后自动/手动回滚，回滚前强制 etcd 备份，回滚操作幂等设计，回滚步骤逆向执行 DAG |
| **交付物** | ① RollbackManager ② 回滚 DAG ③ 幂等性保证 ④ 单元测试 |
| **验收标准** | 1) rollbackOnFailure=true 时升级失败自动触发回滚；2) 回滚前自动执行 etcd 备份；3) 回滚步骤按升级 DAG 逆序执行；4) 回滚操作幂等（重复执行不报错）；5) 回滚完成后 ClusterVersion 回到 Available；6) 回滚失败时状态为 Degraded 并记录详细错误；7) 单元测试覆盖：成功回滚/回滚中 Controller 重启/回滚失败 |
#### R20：升级 PreCheck + PostCheck 健康检查框架
| 字段 | 值 |
|------|-----|
| **工作量** | 1.0 人月 |
| **前置依赖** | R16 |
| **范围** | 实现升级前检查（集群健康、etcd 健康、节点 Ready、资源充足）和升级后检查（组件版本正确、Pod Running、集群功能正常），检查结果决定是否继续升级 |
| **交付物** | ① PreChecker ② PostChecker ③ 检查项配置 ④ 单元测试 |
| **验收标准** | 1) PreCheck 包含：etcd 健康检查、所有节点 Ready、API Server 可达、资源余量检查；2) PostCheck 包含：组件版本验证、Static Pod Running、核心功能验证；3) PreCheck 失败阻止升级开始；4) PostCheck 失败触发 Degraded；5) 检查项可配置（HealthCheckConfig） |
### 🧹 迁移清理（2 个需求）
#### R21：旧 Phase 移除 + PhaseFlow 瘦身
| 字段 | 值 |
|------|-----|
| **工作量** | 1.5 人月 |
| **前置依赖** | R07~R15 全部完成 |
| **范围** | Phase C + Phase D：移除 PhaseFlow 中的 DeployPhases 和 PostDeployPhases（11 个安装 Phase + 7 个升级 Phase），完全由声明式编排接管。保留控制类 Phase（EnsureFinalizer/EnsurePaused/EnsureClusterManage/EnsureDeleteOrReset/EnsureDryRun）在 Controller 中 |
| **交付物** | ① 修改后的 phase_flow.go ② 移除的 Phase 文件清单 ③ 回归测试报告 |
| **验收标准** | 1) 移除 18 个旧 Phase 后编译通过；2) 控制类 5 个 Phase 保留在 Controller 中正常工作；3) 所有安装场景通过集成测试；4) 所有升级场景通过集成测试；5) Feature Gate 设为 true 后旧代码路径不可达 |
#### R22：端到端集成测试 + 回归验证
| 字段 | 值 |
|------|-----|
| **工作量** | 2.0 人月 |
| **前置依赖** | R21 |
| **范围** | 编写完整的 E2E 测试套件，覆盖：全新集群安装（单 Master/HA Master）、节点扩容（加 Master/加 Worker）、节点缩容、Kubernetes 版本升级、Etcd 独立升级、Containerd 升级、Agent 升级、OpenFuyao 组件升级、升级回滚、断点续升、Feature Gate 切换 |
| **交付物** | ① E2E 测试套件 ② 测试矩阵 ③ 回归测试报告 |
| **验收标准** | 1) 全新安装测试通过（单 Master + Worker）；2) HA 安装测试通过（3 Master + Worker）；3) 扩容测试通过（加 1 Master + 加 2 Worker）；4) 缩容测试通过（减 Worker + 减 Master）；5) Kubernetes 小版本升级测试通过；6) Etcd 独立升级测试通过；7) 升级失败 + 自动回滚测试通过；8) 断点续升测试通过（中途重启 Controller）；9) Feature Gate false→true 切换不影响运行中集群；10) 所有测试结果与旧路径行为一致 |
## 三、依赖关系与执行顺序
```
批次1（可并行，无依赖）：
  R01: ComponentVersion + NodeConfig CRD ──┐
  R02: ClusterVersion CRD                  ├── 约 1.4 人月
                                           │
批次2（依赖 R01/R02）：
  R03: DAGScheduler ──────────────────────┤
  R04: ComponentExecutor 接口框架         ├── 约 2.5 人月
  R18: VersionValidator                   │
                                          │
批次3（依赖 R03/R04）：
  R05: ClusterOrchestrator ───────────────┤
  R07: BKEAgent + NodesEnv Executor       │
  R08: Containerd + LoadBalancer Executor ├── 约 8.8 人月
  R11: ClusterAPI + Certs Executor        │
  R13: BKEAgent + Containerd Upgrader     │
  R15: OpenFuyao + BKEProvider Upgrader   │
                                          │
批次4（依赖批次3）：
  R06: Feature Gate 双轨运行              │
  R09: Kubernetes Executor                │
  R10: NodesPostProcess + AgentSwitch     ├── 约 6.5 人月
  R12: Addon + BKEProvider Executor       │
  R14: Etcd + Kubernetes Upgrader         │
  R17: NodeConfig Controller              │
                                          │
批次5（依赖批次4）：
  R16: ClusterVersionReconciler           │
  R19: RollbackManager                    ├── 约 4.8 人月
  R20: PreCheck + PostCheck               │
                                          │
批次6（依赖全部）：
  R21: 旧 Phase 移除                      │
  R22: E2E 集成测试                       └── 约 3.5 人月
```
## 四、工作量汇总
| 类别 | 需求编号 | 合计人月 |
|------|---------|---------|
| 基础设施层 | R01~R06 | 5.2 |
| 节点级安装 Executor | R07~R10 | 6.3 |
| 集群级安装 Executor | R11~R12 | 3.5 |
| 升级 Upgrader | R13~R15 | 5.0 |
| CVO 控制器 | R16~R18 | 4.5 |
| 回滚与安全 | R19~R20 | 2.8 |
| 迁移清理 | R21~R22 | 3.5 |
| **总计** | **22 个需求** | **30.8 人月** |
## 五、关键风险与缓解
| 风险 | 影响的需求 | 缓解措施 |
|------|-----------|---------|
| ensure_addon_deploy.go 代码量巨大（51K），Addon Executor 提取困难 | R12 | 分两步：先提取核心 Helm 操作函数为公共包，再封装为 Executor |
| ensure_cluster_manage.go 代码量巨大（47K），控制类逻辑与编排逻辑耦合 | R05 | 控制类 Phase 保留在 Controller 中，ClusterOrchestrator 仅接管安装/升级编排 |
| Kubernetes Executor 合并 3 个 Phase，状态机复杂 | R09 | 严格按 K8sInstallStep 状态机实现，每步持久化到 ComponentVersion.Status |
| 双轨运行期间状态冲突 | R06 | Feature Gate 严格互斥 + 状态校验（切换前检查无运行中的操作） |
| 升级回滚链路长，测试覆盖难 | R19, R22 | 回滚操作幂等设计 + 分层测试（单元→集成→E2E） |
        

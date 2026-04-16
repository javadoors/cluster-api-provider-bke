# cluster-api-provider-bke 重构实现大纲
## 一、目录规划
### 1.1 当前目录结构（关键部分）
```
cluster-api-provider-bke/
├── api/
│   ├── bkeagent/v1beta1/          # Command CRD
│   ├── bkecommon/v1beta1/         # 共享类型（BKEClusterSpec/Status/BKENode等）
│   └── capbke/v1beta1/            # BKECluster/BKEMachine CRD
├── cmd/
│   ├── capbke/main.go             # Controller 入口
│   ├── bkeagent/main.go           # Agent 入口
│   └── bkeagent-launcher/main.go  # Agent 安装器入口
├── controllers/
│   ├── bkeagent/                  # Command Controller
│   └── capbke/                    # BKECluster/BKEMachine Controller
├── pkg/
│   ├── certs/                     # 证书生成
│   ├── command/                   # Command 构建器
│   ├── executor/                  # 容器运行时执行器
│   ├── job/                       # Job 插件系统
│   │   ├── builtin/               # 内置 Job 插件
│   │   │   ├── containerruntime/  # containerd/docker
│   │   │   ├── kubeadm/           # kubeadm init/join/upgrade
│   │   │   ├── ha/                # HAProxy/Keepalived
│   │   │   └── ...
│   │   └── job.go                 # Job 接口
│   ├── kube/                      # Kube API 客户端封装
│   ├── mergecluster/              # BKECluster 状态合并
│   ├── metrics/                   # 指标系统
│   └── phaseframe/                # Phase 框架（核心）
│       ├── interface.go           # Phase 接口
│       ├── base.go                # BasePhase 基础实现
│       ├── context.go             # PhaseContext
│       ├── phases/                # 各 Phase 实现
│       │   ├── phase_flow.go      # PhaseFlow 编排器
│       │   ├── list.go            # Phase 注册列表
│       │   └── ensure_*.go        # 26个 Phase 实现
│       └── phaseutil/             # Phase 工具函数
├── common/                        # 公共库
│   ├── cluster/                   # 集群相关工具
│   │   ├── addon/                 # Addon 管理
│   │   ├── initialize/            # 初始化配置
│   │   ├── node/                  # 节点比较
│   │   └── validation/            # 验证
│   └── ...
├── utils/                         # 工具库
│   └── capbke/                    # Controller 工具
├── webhooks/                      # Webhook
└── config/                        # Kustomize 部署配置
```
### 1.2 重构后目标目录结构
```
cluster-api-provider-bke/
├── api/
│   ├── bkeagent/v1beta1/                    # [保留] Command CRD
│   ├── bkecommon/v1beta1/                   # [扩展] 共享类型
│   │   ├── bkecluster_spec.go               # [扩展] 新增 InfrastructureMode、UserProvidedInfrastructure
│   │   ├── bkecluster_status.go             # [扩展] 新增 UpgradeInfo、StateHistory
│   │   ├── bkenode_types.go                 # [保留]
│   │   ├── upgrade_types.go                 # [新增] 升级相关共享类型
│   │   └── zz_generated.deepcopy.go
│   ├── capbke/v1beta1/                      # [保留] BKECluster/BKEMachine CRD
│   ├── bootstrap/v1beta1/                   # [新增] Bootstrap Provider API
│   │   ├── bkekubeadmconfig_types.go        # BKEKubeadmConfig CRD
│   │   ├── bkekubeadmconfigtemplate_types.go
│   │   ├── groupversion_info.go
│   │   └── zz_generated.deepcopy.go
│   ├── controlplane/v1beta1/                # [新增] ControlPlane Provider API
│   │   ├── bkecontrolplane_types.go         # BKEControlPlane CRD
│   │   ├── groupversion_info.go
│   │   └── zz_generated.deepcopy.go
│   └── upgrade/v1beta1/                     # [新增] 升级管理 API
│       ├── clusterversion_types.go          # ClusterVersion CRD
│       ├── upgradeinfo_types.go             # UpgradeInfo CRD
│       ├── groupversion_info.go
│       └── zz_generated.deepcopy.go
│
├── cmd/
│   ├── capbke/main.go                       # [扩展] 注册新 Controller
│   ├── bkeagent/main.go                     # [保留]
│   └── bkeagent-launcher/main.go            # [保留]
│
├── controllers/
│   ├── bkeagent/                            # [保留] Command Controller
│   ├── capbke/                              # [重构] 拆分职责
│   │   ├── bkecluster_controller.go         # [精简] 仅 Infrastructure 职责
│   │   ├── bkemachine_controller.go         # [保留]
│   │   ├── cluster_validator.go             # [新增] 集群验证器
│   │   ├── cluster_provisioner.go           # [新增] 集群供应器
│   │   ├── cluster_monitor.go               # [新增] 集群监控器
│   │   └── status_manager.go                # [新增] 状态管理器
│   ├── bootstrap/                           # [新增] Bootstrap Provider Controller
│   │   ├── bkekubeadmconfig_controller.go   # BKEKubeadmConfig Reconciler
│   │   └── bootstrap_data.go               # 引导数据生成
│   ├── controlplane/                        # [新增] ControlPlane Provider Controller
│   │   ├── bkecontrolplane_controller.go    # BKEControlPlane Reconciler
│   │   ├── controlplane_nodes.go            # 控制平面节点管理
│   │   ├── controlplane_certs.go            # 证书管理
│   │   ├── controlplane_loadbalancer.go     # 负载均衡管理
│   │   └── controlplane_upgrade.go          # 控制平面升级
│   └── upgrade/                             # [新增] 升级 Controller
│       ├── clusterversion_controller.go     # ClusterVersion Reconciler
│       ├── upgrade_detector.go              # 升级检测器
│       ├── upgrade_checker.go               # 升级前置检查
│       └── upgrade_coordinator.go           # 升级协调器
│
├── pkg/
│   ├── certs/                               # [保留] 证书生成
│   ├── command/                             # [保留] Command 构建器
│   ├── executor/                            # [保留] 容器运行时执行器
│   ├── job/                                 # [保留] Job 插件系统
│   ├── kube/                                # [保留] Kube API 客户端封装
│   ├── mergecluster/                        # [保留] BKECluster 状态合并
│   ├── metrics/                             # [保留] 指标系统
│   │
│   ├── statemachine/                        # [新增] 状态机引擎
│   │   ├── state.go                         # State 接口 + ClusterPhase 定义
│   │   ├── machine.go                       # ClusterStateMachine 实现
│   │   ├── transitions.go                   # 状态转换矩阵 + 校验
│   │   ├── pending_state.go                 # Pending 状态实现
│   │   ├── provisioning_state.go            # Provisioning 状态实现
│   │   ├── running_state.go                 # Running 状态实现
│   │   ├── updating_state.go                # Updating 状态实现
│   │   ├── deleting_state.go                # Deleting 状态实现
│   │   ├── failed_state.go                  # Failed 状态实现
│   │   └── rollback_state.go                # Rollback 状态实现
│   │
│   ├── phaseframe/                          # [重构] 渐进式迁移
│   │   ├── interface.go                     # [保留] Phase 接口
│   │   ├── base.go                          # [保留] BasePhase
│   │   ├── context.go                       # [保留] PhaseContext
│   │   ├── phases/                          # [迁移] 逐步迁出逻辑
│   │   │   ├── phase_flow.go                # [适配] 适配状态机
│   │   │   ├── list.go                      # [适配] 适配新 Phase 注册
│   │   │   └── ensure_*.go                  # [保留→迁移] 逐步精简
│   │   └── phaseutil/                       # [保留] Phase 工具函数
│   │
│   ├── osprovider/                          # [新增] OS Provider 抽象
│   │   ├── interface.go                     # OSProvider 接口定义
│   │   ├── registry.go                      # Provider 注册表
│   │   ├── centos/                          # CentOS Provider
│   │   │   └── centos.go
│   │   ├── ubuntu/                          # Ubuntu Provider
│   │   │   └── ubuntu.go
│   │   ├── openeuler/                       # openEuler Provider
│   │   │   └── openeuler.go
│   │   └── kylin/                           # Kylin Provider
│   │       └── kylin.go
│   │
│   ├── versionpackage/                      # [新增] 版本包管理
│   │   ├── metadata.go                      # SolutionVersion 元数据结构
│   │   ├── manager.go                       # 版本包管理器
│   │   ├── validator.go                     # 版本校验器
│   │   ├── compatibility.go                 # 兼容性检查器
│   │   └── loader.go                        # 版本配置加载器
│   │
│   ├── asset/                               # [新增] Asset 框架
│   │   ├── interface.go                     # Asset 接口 + DAG
│   │   ├── registry.go                      # Asset 注册表
│   │   ├── generator.go                     # Asset 生成器（拓扑排序）
│   │   ├── persister.go                     # Asset 持久化（ConfigMap）
│   │   └── assets/                          # 具体 Asset 定义
│   │       ├── install_config.go            # InstallConfig Asset
│   │       ├── certificates.go              # Certificates Asset
│   │       ├── kubeconfig.go                # Kubeconfig Asset
│   │       └── static_pods.go              # StaticPods Asset
│   │
│   ├── errors/                              # [新增] 统一错误处理
│   │   ├── errors.go                        # ReconcileError 类型定义
│   │   └── handler.go                       # 统一错误处理函数
│   │
│   └── compat/                              # [新增] 兼容性层
│       ├── compat.go                        # 旧→新资源转换
│       ├── conversion.go                    # BKECluster → BKEControlPlane + BKEKubeadmConfig
│       └── webhook.go                       # Conversion Webhook
│
├── common/                                  # [保留] 公共库
├── utils/                                   # [保留] 工具库
├── webhooks/                                # [扩展] 新增 Webhook
│   ├── capbke/                              # [保留]
│   ├── bootstrap/                           # [新增] BKEKubeadmConfig Webhook
│   ├── controlplane/                        # [新增] BKEControlPlane Webhook
│   └── upgrade/                             # [新增] ClusterVersion Webhook
└── config/                                  # [扩展] Kustomize 部署配置
    ├── crd/bases/                           # [扩展] 新增 CRD YAML
    ├── rbac/                                # [扩展] 新增 RBAC
    └── samples/                             # [扩展] 新增示例
```
### 1.3 目录变更对照表
| 变更类型 | 路径 | 说明 |
|---------|------|------|
| **新增** | `api/bootstrap/v1beta1/` | Bootstrap Provider CRD |
| **新增** | `api/controlplane/v1beta1/` | ControlPlane Provider CRD |
| **新增** | `api/upgrade/v1beta1/` | 升级管理 CRD |
| **新增** | `controllers/bootstrap/` | Bootstrap Provider Controller |
| **新增** | `controllers/controlplane/` | ControlPlane Provider Controller |
| **新增** | `controllers/upgrade/` | 升级管理 Controller |
| **新增** | `pkg/statemachine/` | 状态机引擎 |
| **新增** | `pkg/osprovider/` | OS Provider 抽象 |
| **新增** | `pkg/versionpackage/` | 版本包管理 |
| **新增** | `pkg/asset/` | Asset 框架 |
| **新增** | `pkg/errors/` | 统一错误处理 |
| **新增** | `pkg/compat/` | 兼容性层 |
| **扩展** | `api/bkecommon/v1beta1/` | 新增升级类型、InfrastructureMode |
| **扩展** | `controllers/capbke/` | 拆分职责为 Validator/Provisioner/Monitor |
| **扩展** | `cmd/capbke/main.go` | 注册新 Controller |
| **迁移** | `pkg/phaseframe/phases/ensure_*.go` | 逻辑逐步迁出到新 Controller |
| **保留** | `pkg/job/`, `pkg/certs/`, `pkg/kube/` | 底层能力不变 |
## 二、重构实现大纲（7个阶段）
### 阶段一：基础层 — 统一错误处理 + CRD 扩展
**目标**：建立重构基础，不改变现有行为

| 步骤 | 任务 | 产出文件 | 依赖 |
|------|------|---------|------|
| 1.1 | 定义 `ReconcileError` 类型体系 | `pkg/errors/errors.go` | 无 |
| 1.2 | 实现统一错误处理函数 | `pkg/errors/handler.go` | 1.1 |
| 1.3 | 扩展 `BKEClusterSpec` 增加 `InfrastructureMode` | `api/bkecommon/v1beta1/bkecluster_spec.go` | 无 |
| 1.4 | 扩展 `BKEClusterStatus` 增加升级状态字段 | `api/bkecommon/v1beta1/bkecluster_status.go` | 无 |
| 1.5 | 定义升级相关共享类型 | `api/bkecommon/v1beta1/upgrade_types.go` | 无 |
| 1.6 | 生成 deepcopy + CRD YAML | `make generate; make manifests` | 1.3-1.5 |
| 1.7 | 在 BKEClusterReconciler 中接入统一错误处理 | `controllers/capbke/bkecluster_controller.go` | 1.1-1.2 |

**验收**：现有功能不受影响，`ReconcileError` 可在日志中区分 Transient/Permanent/Dependency 错误
### 阶段二：状态机引擎 — 替换 PhaseFlow 编排
**目标**：用状态机替换 PhaseFlow 的编排逻辑，Phase 实现暂时保留

| 步骤 | 任务 | 产出文件 | 依赖 |
|------|------|---------|------|
| 2.1 | 定义 `State` 接口和 `ClusterPhase` 常量 | `pkg/statemachine/state.go` | 无 |
| 2.2 | 实现 `ClusterStateMachine` 核心逻辑 | `pkg/statemachine/machine.go` | 2.1 |
| 2.3 | 实现状态转换矩阵和校验 | `pkg/statemachine/transitions.go` | 2.1 |
| 2.4 | 实现 `PendingState` | `pkg/statemachine/pending_state.go` | 2.1 |
| 2.5 | 实现 `ProvisioningState`（委托给现有 PhaseFlow） | `pkg/statemachine/provisioning_state.go` | 2.2 |
| 2.6 | 实现 `RunningState` | `pkg/statemachine/running_state.go` | 2.2 |
| 2.7 | 实现 `UpdatingState`（委托给现有 PhaseFlow） | `pkg/statemachine/updating_state.go` | 2.2 |
| 2.8 | 实现 `DeletingState` | `pkg/statemachine/deleting_state.go` | 2.2 |
| 2.9 | 实现 `FailedState` | `pkg/statemachine/failed_state.go` | 2.2 |
| 2.10 | 实现 `RollbackState`（预留，暂不实现回滚逻辑） | `pkg/statemachine/rollback_state.go` | 2.2 |
| 2.11 | 适配 `PhaseFlow` 为 `ProvisioningState`/`UpdatingState` 的内部委托 | `pkg/phaseframe/phases/phase_flow.go` | 2.5, 2.7 |
| 2.12 | 修改 `BKEClusterReconciler.Reconcile` 使用状态机 | `controllers/capbke/bkecluster_controller.go` | 2.2-2.10 |

**验收**：集群创建/升级/删除流程与重构前行为一致，状态转换可通过 `BKECluster.Status.Phase` 追踪
### 阶段三：Bootstrap Provider + ControlPlane Provider 分离
**目标**：将控制平面和引导逻辑从 Infrastructure Provider 中分离

| 步骤 | 任务 | 产出文件 | 依赖 |
|------|------|---------|------|
| 3.1 | 定义 `BKEKubeadmConfig` CRD | `api/bootstrap/v1beta1/bkekubeadmconfig_types.go` | 无 |
| 3.2 | 定义 `BKEKubeadmConfigTemplate` CRD | `api/bootstrap/v1beta1/bkekubeadmconfigtemplate_types.go` | 3.1 |
| 3.3 | 定义 `BKEControlPlane` CRD | `api/controlplane/v1beta1/bkecontrolplane_types.go` | 无 |
| 3.4 | 生成 deepcopy + CRD YAML + RBAC | `make generate; make manifests` | 3.1-3.3 |
| 3.5 | 实现 `BKEKubeadmConfigReconciler` | `controllers/bootstrap/bkekubeadmconfig_controller.go` | 3.1 |
| 3.6 | 实现引导数据生成逻辑 | `controllers/bootstrap/bootstrap_data.go` | 3.5 |
| 3.7 | 实现 `BKEControlPlaneReconciler` 主框架 | `controllers/controlplane/bkecontrolplane_controller.go` | 3.3 |
| 3.8 | 迁移 `EnsureMasterInit/Join/Delete` 逻辑到控制平面节点管理 | `controllers/controlplane/controlplane_nodes.go` | 3.7 |
| 3.9 | 迁移 `EnsureCerts` 逻辑到控制平面证书管理 | `controllers/controlplane/controlplane_certs.go` | 3.7 |
| 3.10 | 迁移 `EnsureLoadBalance` 逻辑到控制平面负载均衡管理 | `controllers/controlplane/controlplane_loadbalancer.go` | 3.7 |
| 3.11 | 实现兼容性层：旧 BKECluster → 新资源转换 | `pkg/compat/compat.go`, `pkg/compat/conversion.go` | 3.1-3.3 |
| 3.12 | 实现 Conversion Webhook | `pkg/compat/webhook.go`, `webhooks/bootstrap/`, `webhooks/controlplane/` | 3.11 |
| 3.13 | 精简 `BKEClusterReconciler`，移除已迁出的逻辑 | `controllers/capbke/bkecluster_controller.go` | 3.8-3.10 |
| 3.14 | 注册新 Controller 到 `main.go` | `cmd/capbke/main.go` | 3.5, 3.7 |

**验收**：`BKEControlPlane` 独立管理控制平面生命周期，`BKEKubeadmConfig` 独立生成引导数据，旧 BKECluster 通过兼容性层自动转换
### 阶段四：升级管理 — ClusterVersion CRD + CVO 控制器
**目标**：实现声明式升级，替换脚本式升级

| 步骤 | 任务 | 产出文件 | 依赖 |
|------|------|---------|------|
| 4.1 | 定义 `ClusterVersion` CRD | `api/upgrade/v1beta1/clusterversion_types.go` | 阶段一 1.5 |
| 4.2 | 定义 `UpgradeInfo` CRD | `api/upgrade/v1beta1/upgradeinfo_types.go` | 4.1 |
| 4.3 | 生成 deepcopy + CRD YAML + RBAC | `make generate; make manifests` | 4.1-4.2 |
| 4.4 | 实现升级检测器 | `controllers/upgrade/upgrade_detector.go` | 4.1 |
| 4.5 | 实现升级前置检查器 | `controllers/upgrade/upgrade_checker.go` | 4.4 |
| 4.6 | 实现升级协调器 | `controllers/upgrade/upgrade_coordinator.go` | 4.4, 4.5 |
| 4.7 | 实现 `ClusterVersionReconciler` | `controllers/upgrade/clusterversion_controller.go` | 4.1, 4.6 |
| 4.8 | 迁移 `Ensure*Upgrade` Phase 逻辑到升级协调器 | `controllers/upgrade/upgrade_coordinator.go` | 4.6 |
| 4.9 | 适配 `UpdatingState` 使用 `ClusterVersion` | `pkg/statemachine/updating_state.go` | 4.7 |
| 4.10 | 注册升级 Controller 到 `main.go` | `cmd/capbke/main.go` | 4.7 |

**验收**：修改 `BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion` 自动触发 `ClusterVersion` 创建，升级流程可追踪、可暂停、可回滚
### 阶段五：OS Provider 抽象
**目标**：将 OS 相关逻辑从硬编码改为插件式

| 步骤 | 任务 | 产出文件 | 依赖 |
|------|------|---------|------|
| 5.1 | 定义 `OSProvider` 接口 | `pkg/osprovider/interface.go` | 无 |
| 5.2 | 实现 Provider 注册表 | `pkg/osprovider/registry.go` | 5.1 |
| 5.3 | 迁移 `pkg/job/builtin/kubeadm/env/centos.go` 为 CentOSProvider | `pkg/osprovider/centos/centos.go` | 5.1 |
| 5.4 | 实现 UbuntuProvider | `pkg/osprovider/ubuntu/ubuntu.go` | 5.1 |
| 5.5 | 实现 openEulerProvider | `pkg/osprovider/openeuler/openeuler.go` | 5.1 |
| 5.6 | 实现 KylinProvider | `pkg/osprovider/kylin/kylin.go` | 5.1 |
| 5.7 | 修改 `EnsureNodesEnv` 使用 OSProvider 替代硬编码 | `pkg/phaseframe/phases/ensure_nodes_env.go` | 5.2-5.6 |
| 5.8 | 修改 `EnsureNodesEnv` 中 containerd 安装使用 OSProvider | `pkg/phaseframe/phases/ensure_nodes_env.go` | 5.7 |

**验收**：新增 OS 只需实现 `OSProvider` 接口并注册，无需修改核心代码
### 阶段六：版本包管理 + Asset 框架
**目标**：实现版本包管理和 Asset 依赖图

| 步骤 | 任务 | 产出文件 | 依赖 |
|------|------|---------|------|
| 6.1 | 定义 `SolutionVersion` 元数据结构 | `pkg/versionpackage/metadata.go` | 无 |
| 6.2 | 实现版本包管理器 | `pkg/versionpackage/manager.go` | 6.1 |
| 6.3 | 实现版本校验器 | `pkg/versionpackage/validator.go` | 6.1 |
| 6.4 | 实现兼容性检查器 | `pkg/versionpackage/compatibility.go` | 6.2 |
| 6.5 | 实现版本配置加载器（对接 Core-VersionConfig） | `pkg/versionpackage/loader.go` | 6.1 |
| 6.6 | 定义 `Asset` 接口和 DAG 依赖 | `pkg/asset/interface.go` | 无 |
| 6.7 | 实现 Asset 注册表 | `pkg/asset/registry.go` | 6.6 |
| 6.8 | 实现 Asset 生成器（拓扑排序） | `pkg/asset/generator.go` | 6.6-6.7 |
| 6.9 | 实现 Asset 持久化（ConfigMap 存储） | `pkg/asset/persister.go` | 6.6 |
| 6.10 | 定义核心 Asset（InstallConfig/Certs/Kubeconfig/StaticPods） | `pkg/asset/assets/*.go` | 6.6 |
| 6.11 | 适配 `EnsureCerts` 使用 Asset 框架 | `controllers/controlplane/controlplane_certs.go` | 6.6-6.10 |
| 6.12 | 适配升级流程使用版本包管理器 | `controllers/upgrade/upgrade_coordinator.go` | 6.2-6.5 |

**验收**：版本包可加载、校验、兼容性检查；Asset 依赖图可追踪安装进度，支持断点续传
### 阶段七：清理 + 集成测试
**目标**：清理遗留代码，完善测试

| 步骤 | 任务 | 产出文件 | 依赖 |
|------|------|---------|------|
| 7.1 | 移除已迁出的 Phase 实现（标记 Deprecated） | `pkg/phaseframe/phases/ensure_*.go` | 阶段三、四 |
| 7.2 | 清理 `BKEClusterReconciler` 中已迁出的逻辑 | `controllers/capbke/bkecluster_controller.go` | 阶段三 |
| 7.3 | 补充 Bootstrap Provider 集成测试 | `controllers/bootstrap/*_test.go` | 阶段三 |
| 7.4 | 补充 ControlPlane Provider 集成测试 | `controllers/controlplane/*_test.go` | 阶段三 |
| 7.5 | 补充升级流程端到端测试 | `controllers/upgrade/*_test.go` | 阶段四 |
| 7.6 | 补充状态机转换测试 | `pkg/statemachine/*_test.go` | 阶段二 |
| 7.7 | 补充 OS Provider 单元测试 | `pkg/osprovider/*/test.go` | 阶段五 |
| 7.8 | 补充兼容性层测试 | `pkg/compat/*_test.go` | 阶段三 |
| 7.9 | 更新 CRD YAML 和 RBAC | `config/` | 全部阶段 |
| 7.10 | 更新部署清单和文档 | `config/`, `docs/` | 全部阶段 |
## 三、重构实现思路
### 3.1 核心思路：渐进式迁移，兼容性优先
```
重构原则：
┌─────────────────────────────────────────────────────────────┐
│ 1. 每个阶段独立可交付，不依赖后续阶段                        │
│ 2. 新旧代码共存，通过兼容性层桥接                            │
│ 3. 先加后删：新增接口/CRD → 迁移逻辑 → 删除旧代码           │
│ 4. 状态机作为编排层，Phase 作为执行层，逐步替换              │
│ 5. 每个阶段完成后运行完整回归测试                            │
└─────────────────────────────────────────────────────────────┘
```
### 3.2 状态机替换 PhaseFlow 的思路
**当前**：`BKEClusterReconciler.Reconcile` → `PhaseFlow.Execute` → 顺序执行 26 个 Phase

**目标**：`BKEClusterReconciler.Reconcile` → `ClusterStateMachine.Reconcile` → 根据当前 State 委托执行

**迁移策略**：
```
阶段二完成后的调用链：
BKEClusterReconciler.Reconcile()
    └── ClusterStateMachine.Reconcile()
            ├── PendingState.Execute()
            │       └── 计算需要执行的 Phase 列表
            ├── ProvisioningState.Execute()
            │       └── PhaseFlow.Execute()  ← 委托给现有 PhaseFlow
            │           ├── EnsureBKEAgent
            │           ├── EnsureNodesEnv
            │           └── ...
            ├── RunningState.Execute()
            │       └── 健康检查
            └── UpdatingState.Execute()
                    └── PhaseFlow.Execute()  ← 委托给现有 PhaseFlow
                        ├── EnsureAgentUpgrade
                        └── ...

阶段三完成后：
ProvisioningState.Execute()
    ├── PhaseFlow.Execute()  ← 仅执行 Infrastructure 相关 Phase
    │   ├── EnsureBKEAgent
    │   ├── EnsureNodesEnv
    │   └── EnsureAddonDeploy
    └── BKEControlPlaneReconciler  ← 控制平面独立管理
        ├── controlplane_nodes.go
        ├── controlplane_certs.go
        └── controlplane_loadbalancer.go
```
### 3.3 Provider 分离的思路
**当前**：所有逻辑在 `BKEClusterReconciler` 中，通过 Phase 顺序执行

**目标**：三个 Provider 各自独立 Reconcile
```
分离策略：

1. Infrastructure Provider（BKECluster）保留：
   ├── EnsureBKEAgent        → Agent 推送
   ├── EnsureNodesEnv        → 节点环境（使用 OSProvider）
   ├── EnsureAddonDeploy     → Addon 部署
   ├── EnsureNodesPostProcess → 后置脚本
   ├── EnsureAgentSwitch     → Agent 切换
   └── EnsureClusterAPIObj   → Cluster API 对象创建

2. Bootstrap Provider（BKEKubeadmConfig）接管：
   ├── EnsureWorkerJoin      → Worker 引导
   └── 节点初始化脚本生成

3. ControlPlane Provider（BKEControlPlane）接管：
   ├── EnsureMasterInit      → 控制平面初始化
   ├── EnsureMasterJoin      → 控制平面扩容
   ├── EnsureMasterDelete    → 控制平面缩容
   ├── EnsureMasterUpgrade   → 控制平面升级
   ├── EnsureCerts           → 证书管理
   └── EnsureLoadBalance     → 负载均衡管理

4. Upgrade Controller（ClusterVersion）接管：
   ├── EnsureProviderSelfUpgrade
   ├── EnsureAgentUpgrade
   ├── EnsureContainerdUpgrade
   ├── EnsureEtcdUpgrade
   ├── EnsureWorkerUpgrade
   └── EnsureComponentUpgrade
```
### 3.4 兼容性保证思路
```
兼容性层设计：
┌──────────────────────────────────────────────────────────┐
│ 旧 BKECluster（用户已有资源）                             │
│   spec.clusterConfig.cluster.nodes: [...]                │
│   spec.controlPlaneEndpoint: {host, port}                │
│                                                          │
│              ↓ CompatibilityLayer.ConvertToNewResources  │
│                                                          │
│ 新资源结构：                                              │
│   BKEControlPlane:                                       │
│     spec.replicas: 3                                     │
│     spec.controlPlaneConfig.loadBalancer: {...}          │
│     spec.controlPlaneConfig.certificates: {...}          │
│                                                          │
│   BKEKubeadmConfigTemplate:                              │
│     spec.template.spec.nodeConfig: {...}                 │
│                                                          │
│   Cluster:                                               │
│     spec.controlPlaneRef → BKEControlPlane               │
│     spec.infrastructureRef → BKECluster                  │
└──────────────────────────────────────────────────────────┘

关键点：
- Conversion Webhook 处理 CRD 版本转换
- 兼容性层在 BKEClusterReconciler 中自动触发
- 旧 BKECluster 不需要用户手动迁移
```
### 3.5 升级机制替换思路
```
当前升级流程：
BKECluster.Spec.OpenFuyaoVersion 变更
    → upgradeFlag = true
    → PhaseFlow 执行 Ensure*Upgrade Phase
    → 无状态追踪，无回滚

目标升级流程：
BKECluster.Spec.OpenFuyaoVersion 变更
    → UpgradeDetector 检测到版本变更
    → 创建 ClusterVersion CR
    → ClusterVersionReconciler 协调：
        1. UpgradeChecker.RunPreCheck()
           ├── 集群健康检查
           ├── 节点就绪检查
           ├── 版本兼容性检查
           └── 资源可用性检查
        2. UpgradeCoordinator.CoordinateUpgrade()
           ├── 按序升级：Provider → Agent → Containerd → Etcd → Master → Worker → Component
           ├── 每步更新 ClusterVersion.Status
           └── 失败时根据 Strategy.RollbackOnFailure 决定是否回滚
        3. 更新 ClusterVersion.Status.Phase
           Idle → PreCheck → Upgrading → PostCheck → Success
                                              └→ Failed → Rollback
```
### 3.6 OSProvider 替换硬编码思路
```
当前：
pkg/job/builtin/kubeadm/env/centos.go  ← 硬编码 CentOS 逻辑
EnsureNodesEnv → command.ENV → centos.go 直接调用

目标：
EnsureNodesEnv
    → OSProviderRegistry.GetProvider(osName)
    → provider.Prepare()          ← 替代 centos.go 中的系统配置
    → provider.InstallRuntime()   ← 替代 containerd 安装硬编码
    → provider.InstallKubelet()   ← 替代 kubelet 安装硬编码

新增 OS 流程：
1. 实现 OSProvider 接口
2. 在 init() 中调用 Register()
3. 无需修改任何核心代码
```
### 3.7 阶段间依赖关系
```
阶段一（基础层）
    │
    ├──→ 阶段二（状态机）     ← 可独立进行
    │
    ├──→ 阶段五（OSProvider） ← 可独立进行
    │
    └──→ 阶段六（版本包+Asset）← 可独立进行
    
阶段二（状态机）
    │
    └──→ 阶段三（Provider 分离）← 依赖状态机框架
    
阶段三（Provider 分离）
    │
    └──→ 阶段四（升级管理）    ← 依赖 ControlPlane Provider
    
阶段四（升级管理）
    │
    └──→ 阶段七（清理+测试）  ← 最终收尾
    
阶段五、六可与阶段二、三并行开发
```
### 3.8 每个 Phase 的迁移归宿
| 当前 Phase | 迁移去向 | 迁移阶段 |
|-----------|---------|---------|
| EnsureFinalizer | 保留在 BKEClusterReconciler | — |
| EnsurePaused | 保留在 BKEClusterReconciler | — |
| EnsureClusterManage | 保留在 BKEClusterReconciler | — |
| EnsureDeleteOrReset | 保留在 BKEClusterReconciler | — |
| EnsureDryRun | 保留在 BKEClusterReconciler | — |
| EnsureBKEAgent | 保留在 BKEClusterReconciler | — |
| EnsureNodesEnv | 保留，内部使用 OSProvider | 阶段五 |
| EnsureClusterAPIObj | 保留在 BKEClusterReconciler | — |
| EnsureCerts | → `controlplane_certs.go` | 阶段三 |
| EnsureLoadBalance | → `controlplane_loadbalancer.go` | 阶段三 |
| EnsureMasterInit | → `controlplane_nodes.go` | 阶段三 |
| EnsureMasterJoin | → `controlplane_nodes.go` | 阶段三 |
| EnsureWorkerJoin | → `BKEKubeadmConfigReconciler` | 阶段三 |
| EnsureAddonDeploy | 保留在 BKEClusterReconciler | — |
| EnsureNodesPostProcess | 保留在 BKEClusterReconciler | — |
| EnsureAgentSwitch | 保留在 BKEClusterReconciler | — |
| EnsureProviderSelfUpgrade | → `upgrade_coordinator.go` | 阶段四 |
| EnsureAgentUpgrade | → `upgrade_coordinator.go` | 阶段四 |
| EnsureContainerdUpgrade | → `upgrade_coordinator.go` | 阶段四 |
| EnsureEtcdUpgrade | → `upgrade_coordinator.go` | 阶段四 |
| EnsureWorkerUpgrade | → `upgrade_coordinator.go` | 阶段四 |
| EnsureMasterUpgrade | → `controlplane_upgrade.go` | 阶段四 |
| EnsureWorkerDelete | 保留在 BKEClusterReconciler | — |
| EnsureMasterDelete | → `controlplane_nodes.go` | 阶段三 |
| EnsureComponentUpgrade | → `upgrade_coordinator.go` | 阶段四 |
| EnsureCluster | 保留在 BKEClusterReconciler | — |

以上是完整的重构实现大纲，核心思路总结为：
1. **渐进式迁移**：7个阶段独立可交付，每阶段完成后系统仍可正常运行
2. **状态机先行**：先建立状态机编排层，Phase 作为执行层委托调用，逐步替换
3. **先加后删**：新增 CRD/Controller → 迁移逻辑 → 兼容性层桥接 → 删除旧代码
4. **兼容性保证**：Conversion Webhook + CompatibilityLayer 确保旧 BKECluster 无需手动迁移
5. **并行开发**：阶段五（OSProvider）、阶段六（版本包+Asset）可与阶段二、三并行


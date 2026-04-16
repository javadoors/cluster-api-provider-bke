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

# 阶段二的详细设计
## 阶段二：状态机引擎 — 替换 PhaseFlow 编排 详细设计
### 一、当前 PhaseFlow 架构问题总结
通过分析 [phase_flow.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go)、[base.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go)、[context.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/context.go) 和 [list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go)，核心问题如下：

| # | 问题 | 位置 | 影响 |
|---|------|------|------|
| 1 | **两阶段执行模型**：CalculatePhase 和 Execute 在两次 Reconcile 中执行 | [phase_flow.go:72-78](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L72) | 中间状态可被外部修改，导致状态不一致 |
| 2 | **NeedExecute 双重调用**：Calculate 和 Execute 各调一次 | [phase_flow.go:88](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L88) 和 [phase_flow.go:314](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L314) | 两次结果可能不一致 |
| 3 | **隐式依赖关系**：阶段顺序通过列表注册顺序定义 | [list.go:28-63](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go#L28) | 无法表达并行、条件依赖 |
| 4 | **WatchBKEClusterStatus 协程泄漏**：`go p.ctx.WatchBKEClusterStatus()` 无生命周期管理 | [phase_flow.go:143](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L143) | panic 时协程可能无法退出 |
| 5 | **状态上报逻辑复杂**：4 个 handle*Status 方法逻辑相似但细微差别 | [base.go:259-367](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go#L259) | 难以维护 |
| 6 | **集群状态与 Phase 状态耦合**：`calculateClusterStatusByPhase` 用 switch-case 映射 | [phase_flow.go:366-416](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L366) | 新增 Phase 需同步修改映射 |
| 7 | **频繁 API 调用**：每个 Phase 至少 3 次 API 调用（Refresh + RefreshCluster + Report） | [base.go:78-92](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go#L78) | 20+ Phase = 60+ API 调用 |
### 二、状态机核心设计
#### 2.1 状态模型定义
当前系统的集群状态（`ClusterStatus`）和 Phase 状态（`PhaseStatus`）是分离的，状态机将它们统一为一个连贯的状态模型。

**集群生命周期状态（ClusterLifecycleState）**：
```
                    ┌──────────────┐
                    │   None       │ ← 初始状态
                    └──────┬───────┘
                           │ spec 创建
                           ▼
                   ┌──────────────┐
            ┌──────│  Provisioning│──────┐
            │      └──────┬───────┘      │
            │ fail        │ success      │ fail
            ▼             ▼              ▼
   ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
   │  InitFailed  │ │   Running    │ │  Failed      │
   └──────────────┘ └──────┬───────┘ └──────────────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
      ┌──────────────┐ ┌──────────┐ ┌──────────┐
      │  Upgrading   │ │ Scaling  │ │ Managing │
      └──────┬───────┘ └────┬─────┘ └────┬─────┘
             │              │            │
             ▼              ▼            ▼
      ┌──────────────┐ ┌───────────┐ ┌────────────┐
      │UpgradeFailed │ │ScaleFailed│ │ManageFailed│
      └──────────────┘ └───────────┘ └────────────┘
                           │
                    ┌──────┴───────┐
                    │  Deleting    │
                    └──────────────┘
```
**状态机接口定义** — 新文件 `pkg/statemachine/statemachine.go`：
```go
package statemachine

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeerrors "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/errors"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

type ClusterLifecycleState string

const (
	StateNone          ClusterLifecycleState = ""
	StateProvisioning  ClusterLifecycleState = "Provisioning"
	StateRunning       ClusterLifecycleState = "Running"
	StateUpgrading     ClusterLifecycleState = "Upgrading"
	StateScaling       ClusterLifecycleState = "Scaling"
	StateManaging      ClusterLifecycleState = "Managing"
	StateDeleting      ClusterLifecycleState = "Deleting"
	StatePausing       ClusterLifecycleState = "Pausing"
	StateDryRunning    ClusterLifecycleState = "DryRunning"
	StateInitFailed    ClusterLifecycleState = "InitFailed"
	StateUpgradeFailed ClusterLifecycleState = "UpgradeFailed"
	StateScaleFailed   ClusterLifecycleState = "ScaleFailed"
	StateManageFailed  ClusterLifecycleState = "ManageFailed"
	StateDeleteFailed  ClusterLifecycleState = "DeleteFailed"
	StatePauseFailed   ClusterLifecycleState = "PauseFailed"
	StateDryRunFailed  ClusterLifecycleState = "DryRunFailed"
)

type StateTransition struct {
	From  ClusterLifecycleState
	To    ClusterLifecycleState
	Event string
}

type State interface {
	Name() ClusterLifecycleState
	Enter(ctx *StateContext) error
	Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error)
	Exit(ctx *StateContext) error
	CanTransitionTo(target ClusterLifecycleState) bool
}

type StateContext struct {
	context.Context
	Client       client.Client
	BKECluster   *bkev1beta1.BKECluster
	OldBKECluster *bkev1beta1.BKECluster
	Log          *bkev1beta1.BKELogger
	Scheme       *runtime.Scheme
	RestConfig   *rest.Config
	NodeFetcher  *nodeutil.NodeFetcher
}

type ClusterStateMachine struct {
	states       map[ClusterLifecycleState]State
	currentState ClusterLifecycleState
	transitions  []StateTransition
}

func NewClusterStateMachine() *ClusterStateMachine {
	sm := &ClusterStateMachine{
		states:      make(map[ClusterLifecycleState]State),
		transitions: defaultTransitions(),
	}
	sm.registerStates()
	return sm
}

func defaultTransitions() []StateTransition {
	return []StateTransition{
		{From: StateNone, To: StateProvisioning, Event: "provision"},
		{From: StateNone, To: StateDeleting, Event: "delete"},
		{From: StateNone, To: StateManaging, Event: "manage"},
		{From: StateNone, To: StateDryRunning, Event: "dryrun"},
		{From: StateNone, To: StatePausing, Event: "pause"},
		{From: StateProvisioning, To: StateRunning, Event: "provision_success"},
		{From: StateProvisioning, To: StateInitFailed, Event: "provision_fail"},
		{From: StateRunning, To: StateUpgrading, Event: "upgrade"},
		{From: StateRunning, To: StateScaling, Event: "scale"},
		{From: StateRunning, To: StateDeleting, Event: "delete"},
		{From: StateRunning, To: StatePausing, Event: "pause"},
		{From: StateRunning, To: StateDryRunning, Event: "dryrun"},
		{From: StateUpgrading, To: StateRunning, Event: "upgrade_success"},
		{From: StateUpgrading, To: StateUpgradeFailed, Event: "upgrade_fail"},
		{From: StateScaling, To: StateRunning, Event: "scale_success"},
		{From: StateScaling, To: StateScaleFailed, Event: "scale_fail"},
		{From: StateManaging, To: StateRunning, Event: "manage_success"},
		{From: StateManaging, To: StateManageFailed, Event: "manage_fail"},
		{From: StateDeleting, To: StateNone, Event: "delete_success"},
		{From: StateDeleting, To: StateDeleteFailed, Event: "delete_fail"},
		{From: StatePausing, To: StateRunning, Event: "pause_success"},
		{From: StatePausing, To: StatePauseFailed, Event: "pause_fail"},
		{From: StateDryRunning, To: StateRunning, Event: "dryrun_success"},
		{From: StateDryRunning, To: StateDryRunFailed, Event: "dryrun_fail"},
		{From: StateInitFailed, To: StateProvisioning, Event: "retry"},
		{From: StateUpgradeFailed, To: StateUpgrading, Event: "retry"},
		{From: StateScaleFailed, To: StateScaling, Event: "retry"},
		{From: StateManageFailed, To: StateManaging, Event: "retry"},
		{From: StateDeleteFailed, To: StateDeleting, Event: "retry"},
		{From: StatePauseFailed, To: StatePausing, Event: "retry"},
		{From: StateDryRunFailed, To: StateDryRunning, Event: "retry"},
	}
}

func (sm *ClusterStateMachine) registerStates() {
	sm.states[StateProvisioning] = NewProvisioningState()
	sm.states[StateRunning] = NewRunningState()
	sm.states[StateUpgrading] = NewUpgradingState()
	sm.states[StateScaling] = NewScalingState()
	sm.states[StateManaging] = NewManagingState()
	sm.states[StateDeleting] = NewDeletingState()
	sm.states[StatePausing] = NewPausingState()
	sm.states[StateDryRunning] = NewDryRunningState()
	sm.states[StateInitFailed] = NewFailedState(StateInitFailed, StateProvisioning)
	sm.states[StateUpgradeFailed] = NewFailedState(StateUpgradeFailed, StateUpgrading)
	sm.states[StateScaleFailed] = NewFailedState(StateScaleFailed, StateScaling)
	sm.states[StateManageFailed] = NewFailedState(StateManageFailed, StateManaging)
	sm.states[StateDeleteFailed] = NewFailedState(StateDeleteFailed, StateDeleting)
	sm.states[StatePauseFailed] = NewFailedState(StatePauseFailed, StatePausing)
	sm.states[StateDryRunFailed] = NewFailedState(StateDryRunFailed, StateDryRunning)
}

func (sm *ClusterStateMachine) Reconcile(ctx *StateContext) (ctrl.Result, error) {
	currentState := sm.determineCurrentState(ctx.BKECluster)
	sm.currentState = currentState

	state, ok := sm.states[currentState]
	if !ok {
		return ctrl.Result{}, bkeerrors.NewPermanent("unknown state",
			bkeerrors.WithReason("InvalidState"))
	}

	if err := state.Enter(ctx); err != nil {
		return ctrl.Result{}, bkeerrors.WrapTransient(err, "enter state failed")
	}

	result, transition, err := state.Execute(ctx)
	if err != nil {
		return sm.handleExecutionError(ctx, err)
	}

	if transition != nil {
		if err := sm.transition(ctx, state, transition); err != nil {
			return ctrl.Result{}, err
		}
	}

	return result, nil
}

func (sm *ClusterStateMachine) determineCurrentState(cluster *bkev1beta1.BKECluster) ClusterLifecycleState {
	if !cluster.DeletionTimestamp.IsZero() {
		return StateDeleting
	}
	if cluster.Spec.Reset {
		return StateDeleting
	}
	if cluster.Spec.Pause {
		return StatePausing
	}
	if cluster.Spec.DryRun {
		return StateDryRunning
	}

	switch cluster.Status.ClusterStatus {
	case bkev1beta1.ClusterInitializing:
		return StateProvisioning
	case bkev1beta1.ClusterReady:
		return StateRunning
	case bkev1beta1.ClusterUpgrading:
		return StateUpgrading
	case bkev1beta1.ClusterMasterScalingUp, bkev1beta1.ClusterWorkerScalingUp,
		bkev1beta1.ClusterMasterScalingDown, bkev1beta1.ClusterWorkerScalingDown:
		return StateScaling
	case bkev1beta1.ClusterManaging:
		return StateManaging
	case bkev1beta1.ClusterDeleting:
		return StateDeleting
	case bkev1beta1.ClusterPaused:
		return StatePausing
	case bkev1beta1.ClusterDryRun:
		return StateDryRunning
	case bkev1beta1.ClusterInitializationFailed:
		return StateInitFailed
	case bkev1beta1.ClusterUpgradeFailed:
		return StateUpgradeFailed
	case bkev1beta1.ClusterScaleFailed:
		return StateScaleFailed
	case bkev1beta1.ClusterManageFailed:
		return StateManageFailed
	case bkev1beta1.ClusterDeleteFailed:
		return StateDeleteFailed
	case bkev1beta1.ClusterPauseFailed:
		return StatePauseFailed
	case bkev1beta1.ClusterDryRunFailed:
		return StateDryRunFailed
	default:
		if cluster.Status.Ready {
			return StateRunning
		}
		return StateProvisioning
	}
}

func (sm *ClusterStateMachine) transition(ctx *StateContext, fromState State, t *StateTransition) error {
	targetState, ok := sm.states[t.To]
	if !ok {
		return bkeerrors.NewPermanent("unknown target state",
			bkeerrors.WithReason("InvalidTransition"))
	}

	if !fromState.CanTransitionTo(t.To) {
		return bkeerrors.NewPermanent("invalid transition",
			bkeerrors.WithReason("TransitionNotAllowed"),
			bkeerrors.WithPhaseName(string(fromState.Name())))
	}

	if err := fromState.Exit(ctx); err != nil {
		return bkeerrors.WrapTransient(err, "exit state failed")
	}

	if err := targetState.Enter(ctx); err != nil {
		return bkeerrors.WrapTransient(err, "enter state failed")
	}

	sm.currentState = t.To
	ctx.BKECluster.Status.ClusterStatus = confv1beta1.ClusterStatus(t.To)
	return nil
}

func (sm *ClusterStateMachine) handleExecutionError(ctx *StateContext, err error) (ctrl.Result, error) {
	if bkeerrors.IsPermanentError(err) {
		return ctrl.Result{}, nil
	}
	retryAfter := bkeerrors.GetRetryAfter(err)
	if retryAfter > 0 {
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}
	return ctrl.Result{}, err
}
```
#### 2.2 State 接口与基础实现
新文件 `pkg/statemachine/state.go`：
```go
package statemachine

type BaseState struct {
	name           ClusterLifecycleState
	enterHooks     []func(ctx *StateContext) error
	exitHooks      []func(ctx *StateContext) error
	allowedTargets []ClusterLifecycleState
}

func NewBaseState(name ClusterLifecycleState, allowedTargets ...ClusterLifecycleState) BaseState {
	return BaseState{
		name:           name,
		allowedTargets: allowedTargets,
	}
}

func (s *BaseState) Name() ClusterLifecycleState {
	return s.name
}

func (s *BaseState) Enter(ctx *StateContext) error {
	for _, hook := range s.enterHooks {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *BaseState) Exit(ctx *StateContext) error {
	for _, hook := range s.exitHooks {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *BaseState) CanTransitionTo(target ClusterLifecycleState) bool {
	for _, allowed := range s.allowedTargets {
		if allowed == target {
			return true
		}
	}
	return false
}

func (s *BaseState) RegisterEnterHooks(hooks ...func(ctx *StateContext) error) {
	s.enterHooks = append(s.enterHooks, hooks...)
}

func (s *BaseState) RegisterExitHooks(hooks ...func(ctx *StateContext) error) {
	s.exitHooks = append(s.exitHooks, hooks...)
}
```
#### 2.3 PhaseAdapter — 适配现有 Phase 实现
**核心设计思想**：状态机不重写每个 Phase 的业务逻辑，而是通过 `PhaseAdapter` 将现有 Phase 适配到 State 的 Execute 方法中。

新文件 `pkg/statemachine/phase_adapter.go`：
```go
package statemachine

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeerrors "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/errors"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phases"
)

type PhaseStep struct {
	Name       confv1beta1.BKEClusterPhase
	Factory    func(ctx *phaseframe.PhaseContext) phaseframe.Phase
	DependsOn  []confv1beta1.BKEClusterPhase
	Timeout    time.Duration
	Retryable  bool
}

type PhaseAdapter struct {
	BaseState
	steps       []PhaseStep
	phaseCtx    *phaseframe.PhaseContext
	successEvent string
	failEvent    string
}

func NewPhaseAdapter(
	name ClusterLifecycleState,
	steps []PhaseStep,
	successEvent, failEvent string,
	allowedTargets ...ClusterLifecycleState,
) *PhaseAdapter {
	return &PhaseAdapter{
		BaseState:    NewBaseState(name, allowedTargets...),
		steps:        steps,
		successEvent: successEvent,
		failEvent:    failEvent,
	}
}

func (a *PhaseAdapter) Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error) {
	a.phaseCtx = a.buildPhaseContext(ctx)

	var res ctrl.Result
	var lastErr error

	for _, step := range a.steps {
		phase := step.Factory(a.phaseCtx)

		if !phase.NeedExecute(ctx.OldBKECluster, ctx.BKECluster) {
			phase.SetStatus(bkev1beta1.PhaseSkipped)
			if err := phase.Report("", false); err != nil {
				return ctrl.Result{}, nil, bkeerrors.WrapTransient(err, "report skipped status failed")
			}
			continue
		}

		if err := phase.ExecutePreHook(); err != nil {
			return ctrl.Result{}, nil, bkeerrors.WrapTransient(err, "pre hook failed",
				bkeerrors.WithPhaseName(string(step.Name)))
		}

		phaseResult, phaseErr := phase.Execute()
		if phaseErr != nil {
			lastErr = phaseErr
			_ = phase.ExecutePostHook(phaseErr)
			return a.handleStepError(step, phaseErr)
		}

		res = util.LowestNonZeroResult(res, phaseResult)

		if err := phase.ExecutePostHook(nil); err != nil {
			return ctrl.Result{}, nil, bkeerrors.WrapTransient(err, "post hook failed",
				bkeerrors.WithPhaseName(string(step.Name)))
		}

		if err := a.refreshCluster(ctx); err != nil {
			return ctrl.Result{}, nil, bkeerrors.WrapTransient(err, "refresh cluster failed")
		}
	}

	return res, &StateTransition{
		From:  a.name,
		To:    a.targetFromEvent(a.successEvent),
		Event: a.successEvent,
	}, nil
}

func (a *PhaseAdapter) handleStepError(step PhaseStep, err error) (ctrl.Result, *StateTransition, error) {
	if step.Retryable {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil, bkeerrors.WrapTransient(err,
			fmt.Sprintf("step %s failed, will retry", step.Name),
			bkeerrors.WithPhaseName(string(step.Name)))
	}

	return ctrl.Result{}, &StateTransition{
		From:  a.name,
		To:    a.targetFromEvent(a.failEvent),
		Event: a.failEvent,
	}, bkeerrors.WrapPermanent(err, fmt.Sprintf("step %s failed permanently", step.Name),
		bkeerrors.WithPhaseName(string(step.Name)))
}

func (a *PhaseAdapter) buildPhaseContext(stateCtx *StateContext) *phaseframe.PhaseContext {
	return phaseframe.NewReconcilePhaseCtx(stateCtx.Context).
		SetClient(stateCtx.Client).
		SetRestConfig(stateCtx.RestConfig).
		SetScheme(stateCtx.Scheme).
		SetLogger(stateCtx.Log).
		SetBKECluster(stateCtx.BKECluster)
}

func (a *PhaseAdapter) refreshCluster(ctx *StateContext) error {
	newCluster, err := mergecluster.GetCombinedBKECluster(ctx, ctx.Client,
		ctx.BKECluster.Namespace, ctx.BKECluster.Name)
	if err != nil {
		return err
	}
	ctx.BKECluster = newCluster
	a.phaseCtx.SetBKECluster(newCluster)
	return nil
}

func (a *PhaseAdapter) targetFromEvent(event string) ClusterLifecycleState {
	for _, t := range defaultTransitions() {
		if t.From == a.name && t.Event == event {
			return t.To
		}
	}
	return a.name
}
```
#### 2.4 各 State 实现 — 使用 PhaseAdapter 组装
新文件 `pkg/statemachine/states.go`：
```go
package statemachine

func NewProvisioningState() State {
	steps := []PhaseStep{
		{Name: phases.EnsureFinalizerName, Factory: phases.NewEnsureFinalizer, Retryable: false},
		{Name: phases.EnsureBKEAgentName, Factory: phases.NewEnsureBKEAgent, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureFinalizerName}, Retryable: true},
		{Name: phases.EnsureNodesEnvName, Factory: phases.NewEnsureNodesEnv, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureBKEAgentName}, Retryable: true},
		{Name: phases.EnsureClusterAPIObjName, Factory: phases.NewEnsureClusterAPIObj, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureNodesEnvName}, Retryable: true},
		{Name: phases.EnsureCertsName, Factory: phases.NewEnsureCerts, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureClusterAPIObjName}, Retryable: false},
		{Name: phases.EnsureLoadBalanceName, Factory: phases.NewEnsureLoadBalance, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureCertsName}, Retryable: true},
		{Name: phases.EnsureMasterInitName, Factory: phases.NewEnsureMasterInit, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureLoadBalanceName}, Retryable: true},
		{Name: phases.EnsureMasterJoinName, Factory: phases.NewEnsureMasterJoin, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureMasterInitName}, Retryable: true},
		{Name: phases.EnsureWorkerJoinName, Factory: phases.NewEnsureWorkerJoin, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureMasterJoinName}, Retryable: true},
		{Name: phases.EnsureAddonDeployName, Factory: phases.NewEnsureAddonDeploy, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureWorkerJoinName}, Retryable: true},
		{Name: phases.EnsureNodesPostProcessName, Factory: phases.NewEnsureNodesPostProcess, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureAddonDeployName}, Retryable: true},
		{Name: phases.EnsureAgentSwitchName, Factory: phases.NewEnsureAgentSwitch, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureNodesPostProcessName}, Retryable: true},
	}

	return NewPhaseAdapter(
		StateProvisioning,
		steps,
		"provision_success",
		"provision_fail",
		StateRunning, StateInitFailed,
	)
}

func NewUpgradingState() State {
	steps := []PhaseStep{
		{Name: phases.EnsureProviderSelfUpgradeName, Factory: phases.NewEnsureProviderSelfUpgrade, Retryable: false},
		{Name: phases.EnsureAgentUpgradeName, Factory: phases.NewEnsureAgentUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureProviderSelfUpgradeName}, Retryable: true},
		{Name: phases.EnsureContainerdUpgradeName, Factory: phases.NewEnsureContainerdUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureAgentUpgradeName}, Retryable: true},
		{Name: phases.EnsureEtcdUpgradeName, Factory: phases.NewEnsureEtcdUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureContainerdUpgradeName}, Retryable: true},
		{Name: phases.EnsureMasterUpgradeName, Factory: phases.NewEnsureMasterUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureEtcdUpgradeName}, Retryable: true},
		{Name: phases.EnsureWorkerUpgradeName, Factory: phases.NewEnsureWorkerUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureMasterUpgradeName}, Retryable: true},
		{Name: phases.EnsureComponentUpgradeName, Factory: phases.NewEnsureComponentUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureWorkerUpgradeName}, Retryable: true},
	}

	return NewPhaseAdapter(
		StateUpgrading,
		steps,
		"upgrade_success",
		"upgrade_fail",
		StateRunning, StateUpgradeFailed,
	)
}

func NewScalingState() State {
	return NewScalingStateWithDetector()
}

func NewDeletingState() State {
	steps := []PhaseStep{
		{Name: phases.EnsurePausedName, Factory: phases.NewEnsurePaused, Retryable: true},
		{Name: phases.EnsureDeleteOrResetName, Factory: phases.NewEnsureDeleteOrReset, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsurePausedName}, Retryable: true},
	}

	return NewPhaseAdapter(
		StateDeleting,
		steps,
		"delete_success",
		"delete_fail",
		StateNone, StateDeleteFailed,
	)
}

func NewPausingState() State {
	steps := []PhaseStep{
		{Name: phases.EnsurePausedName, Factory: phases.NewEnsurePaused, Retryable: true},
	}

	return NewPhaseAdapter(
		StatePausing,
		steps,
		"pause_success",
		"pause_fail",
		StateRunning, StatePauseFailed,
	)
}

func NewDryRunningState() State {
	steps := []PhaseStep{
		{Name: phases.EnsureDryRunName, Factory: phases.NewEnsureDryRun, Retryable: false},
	}

	return NewPhaseAdapter(
		StateDryRunning,
		steps,
		"dryrun_success",
		"dryrun_fail",
		StateRunning, StateDryRunFailed,
	)
}

func NewManagingState() State {
	steps := []PhaseStep{
		{Name: phases.EnsureClusterManageName, Factory: phases.NewEnsureClusterManage, Retryable: true},
	}

	return NewPhaseAdapter(
		StateManaging,
		steps,
		"manage_success",
		"manage_fail",
		StateRunning, StateManageFailed,
	)
}

func NewRunningState() State {
	return &RunningState{
		BaseState: NewBaseState(StateRunning,
			StateUpgrading, StateScaling, StateDeleting,
			StatePausing, StateDryRunning, StateManaging),
	}
}

type RunningState struct {
	BaseState
}

func (s *RunningState) Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error) {
	transition := s.detectTransition(ctx)
	if transition != nil {
		return ctrl.Result{}, transition, nil
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil, nil
}

func (s *RunningState) detectTransition(ctx *StateContext) *StateTransition {
	cluster := ctx.BKECluster

	if !cluster.DeletionTimestamp.IsZero() {
		return &StateTransition{From: StateRunning, To: StateDeleting, Event: "delete"}
	}
	if cluster.Spec.Reset {
		return &StateTransition{From: StateRunning, To: StateDeleting, Event: "delete"}
	}
	if cluster.Spec.Pause {
		return &StateTransition{From: StateRunning, To: StatePausing, Event: "pause"}
	}
	if cluster.Spec.DryRun {
		return &StateTransition{From: StateRunning, To: StateDryRunning, Event: "dryrun"}
	}

	old := ctx.OldBKECluster
	if old != nil {
		if s.isUpgradeRequested(old, cluster) {
			return &StateTransition{From: StateRunning, To: StateUpgrading, Event: "upgrade"}
		}
		if s.isScaleRequested(old, cluster) {
			return &StateTransition{From: StateRunning, To: StateScaling, Event: "scale"}
		}
	}

	return nil
}

func (s *RunningState) isUpgradeRequested(old, new *bkev1beta1.BKECluster) bool {
	if new.Spec.ClusterConfig == nil || old.Spec.ClusterConfig == nil {
		return false
	}
	return new.Spec.ClusterConfig.Cluster.KubernetesVersion != old.Spec.ClusterConfig.Cluster.KubernetesVersion ||
		new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion != old.Spec.ClusterConfig.Cluster.OpenFuyaoVersion ||
		new.Spec.ClusterConfig.Cluster.ContainerdVersion != old.Spec.ClusterConfig.Cluster.ContainerdVersion
}

func (s *RunningState) isScaleRequested(old, new *bkev1beta1.BKECluster) bool {
	return false
}

func NewFailedState(name, retryTarget ClusterLifecycleState) State {
	return &FailedState{
		BaseState:    NewBaseState(name, retryTarget),
		retryTarget:  retryTarget,
	}
}

type FailedState struct {
	BaseState
	retryTarget ClusterLifecycleState
}

func (s *FailedState) Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error) {
	return ctrl.Result{}, &StateTransition{
		From:  s.name,
		To:    s.retryTarget,
		Event: "retry",
	}, nil
}
```
#### 2.5 ScalingState — 动态步骤组装
ScalingState 比较特殊，需要根据扩缩容方向动态组装步骤：
```go
package statemachine

import (
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phases"
)

func NewScalingStateWithDetector() State {
	return &ScalingState{
		BaseState: NewBaseState(StateScaling, StateRunning, StateScaleFailed),
	}
}

type ScalingState struct {
	BaseState
}

func (s *ScalingState) Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error) {
	direction := s.detectScaleDirection(ctx)
	if direction == ScaleDirectionNone {
		return ctrl.Result{}, &StateTransition{
			From: StateScaling, To: StateRunning, Event: "scale_success",
		}, nil
	}

	steps := s.buildSteps(direction)
	adapter := NewPhaseAdapter(
		StateScaling, steps,
		"scale_success", "scale_fail",
		StateRunning, StateScaleFailed,
	)
	return adapter.Execute(ctx)
}

type ScaleDirection string

const (
	ScaleDirectionNone        ScaleDirection = "None"
	ScaleDirectionMasterUp    ScaleDirection = "MasterUp"
	ScaleDirectionMasterDown  ScaleDirection = "MasterDown"
	ScaleDirectionWorkerUp    ScaleDirection = "WorkerUp"
	ScaleDirectionWorkerDown  ScaleDirection = "WorkerDown"
)

func (s *ScalingState) detectScaleDirection(ctx *StateContext) ScaleDirection {
	old := ctx.OldBKECluster
	new := ctx.BKECluster
	if old == nil || new == nil {
		return ScaleDirectionNone
	}

	switch ctx.BKECluster.Status.ClusterStatus {
	case bkev1beta1.ClusterMasterScalingUp:
		return ScaleDirectionMasterUp
	case bkev1beta1.ClusterMasterScalingDown:
		return ScaleDirectionMasterDown
	case bkev1beta1.ClusterWorkerScalingUp:
		return ScaleDirectionWorkerUp
	case bkev1beta1.ClusterWorkerScalingDown:
		return ScaleDirectionWorkerDown
	default:
		return ScaleDirectionNone
	}
}

func (s *ScalingState) buildSteps(direction ScaleDirection) []PhaseStep {
	switch direction {
	case ScaleDirectionMasterUp:
		return []PhaseStep{
			{Name: phases.EnsureMasterJoinName, Factory: phases.NewEnsureMasterJoin, Retryable: true},
		}
	case ScaleDirectionMasterDown:
		return []PhaseStep{
			{Name: phases.EnsureMasterDeleteName, Factory: phases.NewEnsureMasterDelete, Retryable: true},
		}
	case ScaleDirectionWorkerUp:
		return []PhaseStep{
			{Name: phases.EnsureWorkerJoinName, Factory: phases.NewEnsureWorkerJoin, Retryable: true},
		}
	case ScaleDirectionWorkerDown:
		return []PhaseStep{
			{Name: phases.EnsureWorkerDeleteName, Factory: phases.NewEnsureWorkerDelete, Retryable: true},
		}
	default:
		return nil
	}
}
```
### 三、Controller 层集成
修改 [bkecluster_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go) 中的 `executePhaseFlow` 方法：

**当前代码**（[bkecluster_controller.go:144-162](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L144)）：
```go
func (r *BKEClusterReconciler) executePhaseFlow(ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    oldBkeCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger) (ctrl.Result, error) {
    phaseCtx := phaseframe.NewReconcilePhaseCtx(ctx).
        SetClient(r.Client).
        SetRestConfig(r.RestConfig).
        SetScheme(r.Scheme).
        SetLogger(bkeLogger).
        SetBKECluster(bkeCluster)
    defer phaseCtx.Cancel()

    flow := phases.NewPhaseFlow(phaseCtx)
    err := flow.CalculatePhase(oldBkeCluster, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    res, err := flow.Execute()
    if err != nil {
        log.Warnf("Reconcile bkeCluster %q failed: %v", utils.ClientObjNS(bkeCluster), err)
    }
    return res, nil
}
```
**重构后**：
```go
func (r *BKEClusterReconciler) executePhaseFlow(ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    oldBkeCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger) (ctrl.Result, error) {

    stateCtx := &statemachine.StateContext{
        Context:       ctx,
        Client:        r.Client,
        BKECluster:    bkeCluster,
        OldBKECluster: oldBkeCluster,
        Log:           bkeLogger,
        Scheme:        r.Scheme,
        RestConfig:    r.RestConfig,
        NodeFetcher:   r.NodeFetcher,
    }

    sm := statemachine.NewClusterStateMachine()
    result, err := sm.Reconcile(stateCtx)
    if err != nil {
        log.Warnf("Reconcile bkeCluster %q failed: %v", utils.ClientObjNS(bkeCluster), err)
    }
    return result, err
}
```
### 四、PhaseStatus 上报机制保留
状态机替换的是编排逻辑（PhaseFlow），但 PhaseStatus 上报机制保持不变。每个 Phase 的 `Report` 方法仍然将状态写入 `BKECluster.Status.PhaseStatus`，确保 UI 层可以继续展示进度。

状态机额外负责将 `ClusterStatus`（集群级别状态）与 `ClusterLifecycleState` 同步：
```go
func (sm *ClusterStateMachine) syncClusterStatus(ctx *StateContext) {
	ctx.BKECluster.Status.ClusterStatus = confv1beta1.ClusterStatus(sm.currentState)

	switch sm.currentState {
	case StateProvisioning:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.Deploying
	case StateRunning:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.Healthy
	case StateInitFailed:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.DeployFailed
	case StateUpgrading:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.Upgrading
	case StateUpgradeFailed:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.UpgradeFailed
	}
}
```
这替代了当前 [phase_flow.go:366-416](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L366) 中 `calculateClusterStatusByPhase` 的 switch-case 映射。
### 五、消除 WatchBKEClusterStatus 协程
当前 `go p.ctx.WatchBKEClusterStatus()` 启动的协程用于：
1. 定期刷新 BKECluster 状态
2. 检测暂停状态
3. 检测删除状态并取消上下文

**重构方案**：将这些职责移入状态机的 `Execute` 方法中，通过 Reconcile 循环自然实现：
```go
func (sm *ClusterStateMachine) Reconcile(ctx *StateContext) (ctrl.Result, error) {
	if sm.shouldCancelExecution(ctx) {
		return ctrl.Result{}, nil
	}

	currentState := sm.determineCurrentState(ctx.BKECluster)
	sm.currentState = currentState

	state, ok := sm.states[currentState]
	if !ok {
		return ctrl.Result{}, bkeerrors.NewPermanent("unknown state")
	}

	if err := state.Enter(ctx); err != nil {
		return ctrl.Result{}, bkeerrors.WrapTransient(err, "enter state failed")
	}

	result, transition, err := state.Execute(ctx)
	if err != nil {
		return sm.handleExecutionError(ctx, err)
	}

	if transition != nil {
		if err := sm.transition(ctx, state, transition); err != nil {
			return ctrl.Result{}, err
		}
	}

	sm.syncClusterStatus(ctx)

	return result, nil
}

func (sm *ClusterStateMachine) shouldCancelExecution(ctx *StateContext) bool {
	cluster := ctx.BKECluster
	if !cluster.DeletionTimestamp.IsZero() && sm.currentState != StateDeleting {
		return true
	}
	return false
}
```
### 六、目录结构
```
pkg/statemachine/
├── statemachine.go        # ClusterStateMachine 核心逻辑
├── state.go               # BaseState 基础实现
├── states.go              # 各 State 实现（Provisioning/Upgrading/...）
├── phase_adapter.go       # PhaseAdapter 适配器
├── scaling_state.go       # ScalingState 动态步骤组装
├── transitions.go         # 状态转换表定义
├── statemachine_test.go   # 状态机核心测试
├── state_test.go          # BaseState 测试
├── phase_adapter_test.go  # PhaseAdapter 测试
└── transitions_test.go    # 转换规则测试
```
### 七、实现步骤
| 步骤 | 内容 | 涉及文件 | 风险 |
|------|------|---------|------|
| 1 | 创建 `pkg/statemachine/` 目录和核心接口 | 新文件 | 无 |
| 2 | 实现 `BaseState` 和 `ClusterStateMachine` | 新文件 | 无 |
| 3 | 实现 `PhaseAdapter` 适配器 | 新文件 | 无 |
| 4 | 实现各 State（Provisioning/Upgrading/...） | 新文件 | 低 — 复用现有 Phase |
| 5 | 实现状态转换表和 `determineCurrentState` | 新文件 | 中 — 需覆盖所有 ClusterStatus |
| 6 | 修改 Controller 的 `executePhaseFlow` | `bkecluster_controller.go` | 中 — 核心入口变更 |
| 7 | 移除 `WatchBKEClusterStatus` 协程依赖 | `context.go` | 低 — 职责已转移 |
| 8 | 编写单元测试 | 新文件 | 无 |
| 9 | 集成测试验证所有场景 | 全项目 | 需全部通过 |
### 八、验证标准
1. **功能等价**：所有现有 Phase 的 Execute/NeedExecute/Report 行为不变
2. **状态转换合法**：所有状态转换必须通过转换表验证
3. **单次 Reconcile**：消除两阶段执行模型，Calculate 和 Execute 合并
4. **无协程泄漏**：不再使用 `go WatchBKEClusterStatus()`
5. **错误分类**：Phase 执行错误通过阶段一的 `ReconcileError` 分类处理
6. **PhaseStatus 兼容**：UI 层仍可通过 `BKECluster.Status.PhaseStatus` 查看进度
7. **ClusterStatus 映射**：`ClusterLifecycleState` 与现有 `ClusterStatus` 双向映射正确
### 九、与阶段一的依赖关系
| 阶段一产出 | 阶段二使用方式 |
|-----------|--------------|
| `ReconcileError` 类型系统 | `handleExecutionError` 中分类处理错误 |
| `ReconcileErrorType` | `PhaseAdapter.handleStepError` 中标记 Retryable |
| `handleReconcileError` | Controller 层统一错误处理入口 |
| `InfrastructureMode` | `ProvisioningState` 中根据 IPI/UPI 跳过不同步骤 |
| `UpgradeStatus` | `UpgradingState` 中跟踪升级进度 |

# 集群生命周期状态说明     
## 集群生命周期状态详细说明
### 一、状态定义与语义
#### 1.1 状态分类
```
┌────────────────────────────────────────────────────────────────────────┐
│                        ClusterLifecycleState 分类                      │
├────────────────────────────────────────────────────────────────────────┤
│                                                                        │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ 初始状态                                                        │   │
│  │  • StateNone — 集群资源刚创建，尚未开始任何操作                 │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                        │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ 运行中状态— 集群正在进行某种操作                                │   │
│  │  • StateProvisioning  — 集群初始化部署                          │   │
│  │  • StateRunning       — 集群正常运行，无待处理操作              │   │
│  │  • StateUpgrading     — 集群版本升级中                          │   │
│  │  • StateScaling       — 集群节点扩缩容中                        │   │
│  │  • StateManaging      — 纳管现有集群中                          │   │
│  │  • StateDeleting      — 集群删除中                              │   │
│  │  • StatePausing       — 集群暂停中                              │   │
│  │  • StateDryRunning    — DryRun 模式中                           │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                        │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ 失败状态— 操作失败，等待重试或人工介入                          │   │
│  │  • StateInitFailed    — 初始化部署失败                          │   │
│  │  • StateUpgradeFailed — 版本升级失败                            │   │
│  │  • StateScaleFailed   — 节点扩缩容失败                          │   │
│  │  • StateManageFailed  — 纳管失败                                │   │
│  │  • StateDeleteFailed  — 删除失败                                │   │
│  │  • StatePauseFailed   — 暂停失败                                │   │
│  │  • StateDryRunFailed  — DryRun 失败                             │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘
```
#### 1.2 各状态详细说明
| 状态 | 中文名 | 触发条件 | 执行的 Phase | 结束状态 |
|------|--------|---------|-------------|---------|
| **StateNone** | 初始 | BKECluster CR 刚创建，Status 为空 | 无 | → Provisioning/Deleting/Managing/DryRunning/Pausing |
| **StateProvisioning** | 初始化部署 | 新集群首次部署 | EnsureFinalizer → EnsureBKEAgent → EnsureNodesEnv → EnsureClusterAPIObj → EnsureCerts → EnsureLoadBalance → EnsureMasterInit → EnsureMasterJoin → EnsureWorkerJoin → EnsureAddonDeploy → EnsureNodesPostProcess → EnsureAgentSwitch | → Running (成功) / InitFailed (失败) |
| **StateRunning** | 正常运行 | 集群部署完成，或升级/扩缩容完成 | EnsureCluster（健康检查） | → Upgrading/Scaling/Deleting/Pausing/DryRunning/Managing |
| **StateUpgrading** | 版本升级 | K8s/OpenFuyao/Containerd 版本变化 | EnsureProviderSelfUpgrade → EnsureAgentUpgrade → EnsureContainerdUpgrade → EnsureEtcdUpgrade → EnsureMasterUpgrade → EnsureWorkerUpgrade → EnsureComponentUpgrade | → Running (成功) / UpgradeFailed (失败) |
| **StateScaling** | 节点扩缩容 | Master/Worker 节点数量变化 | EnsureMasterJoin/EnsureMasterDelete/EnsureWorkerJoin/EnsureWorkerDelete（根据方向动态选择） | → Running (成功) / ScaleFailed (失败) |
| **StateManaging** | 纳管现有集群 | 纳管外部已有集群 | EnsureClusterManage | → Running (成功) / ManageFailed (失败) |
| **StateDeleting** | 删除中 | DeletionTimestamp 非空 或 Spec.Reset=true | EnsurePaused → EnsureDeleteOrReset | → None (成功删除) / DeleteFailed (失败) |
| **StatePausing** | 暂停中 | Spec.Pause=true | EnsurePaused | → Running (成功) / PauseFailed (失败) |
| **StateDryRunning** | DryRun 模式 | Spec.DryRun=true | EnsureDryRun | → Running (成功) / DryRunFailed (失败) |
| **StateInitFailed** | 初始化失败 | Provisioning 阶段失败 | 无（等待重试或人工修复） | → Provisioning (重试) |
| **StateUpgradeFailed** | 升级失败 | Upgrading 阶段失败 | 无（等待重试或人工修复） | → Upgrading (重试) |
| **StateScaleFailed** | 扩缩容失败 | Scaling 阶段失败 | 无（等待重试或人工修复） | → Scaling (重试) |
| **StateManageFailed** | 纳管失败 | Managing 阶段失败 | 无（等待重试或人工修复） | → Managing (重试) |
| **StateDeleteFailed** | 删除失败 | Deleting 阶段失败 | 无（等待重试或人工修复） | → Deleting (重试) |
| **StatePauseFailed** | 暂停失败 | Pausing 阶段失败 | 无（等待重试或人工修复） | → Pausing (重试) |
| **StateDryRunFailed** | DryRun 失败 | DryRunning 阶段失败 | 无（等待重试或人工修复） | → DryRunning (重试) |
### 二、状态转换图
```
                                    ┌─────────────────────────────────────────────────────────────────┐
                                    │                        StateNone (初始)                         │
                                    └───────────────────────────┬─────────────────────────────────────┘
                                                                │
                    ┌───────────────┬───────────────┬──────────┼──────────┬───────────────┬───────────────┐
                    │               │               │          │          │               │               │
                    ▼               ▼               ▼          ▼          ▼               ▼               ▼
           ┌──────────────┐ ┌──────────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
           │ Provisioning │ │  Managing    │ │ Deleting │ │ Pausing  │ │DryRunning│ │ Upgrading│ │ Scaling  │
           └──────┬───────┘ └──────┬───────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘
                  │                │              │            │            │            │            │
           ┌──────┴──────┐   ┌─────┴─────┐   ┌────┴────┐  ┌────┴────┐  ┌────┴────┐  ┌────┴────┐  ┌────┴────┐
           │             │   │           │   │         │  │         │  │         │  │         │  │         │
           ▼             ▼   ▼           ▼   ▼         │  ▼         │  ▼         │  ▼         │  ▼         │
    ┌──────────┐  ┌──────────┐ ┌────────┐ ┌────────┐   │  ┌────────┐ │  ┌────────┐ │  ┌────────┐ │  ┌────────┐
    │ Running  │  │InitFailed│ │Running │ │Manage  │   │  │Running │ │  │Running │ │  │Running │ │  │Running │
    └────┬─────┘  └────┬─────┘ └───┬────┘ │Failed  │   │  └───┬────┘ │  └───┬────┘ │  └───┬────┘ │  └───┬────┘
         │             │ retry     │      └───┬────┘   │      │      │      │      │      │      │      │
         │             └───────────┘          │        │      │      │      │      │      │      │      │
         │                    ┌───────────────┘        │      │      │      │      │      │      │      │
         │                    │                        │      │      │      │      │      │      │      │
         │                    ▼                        │      ▼      │      ▼      │      ▼      │      ▼
         │             ┌──────────────┐                │ ┌───────────┐ ┌──────────┐ ┌───────────┐ ┌───────────┐
         │             │   Running    │◄───────────────┘ │PauseFailed│ │DryRunFail│ │UpgradeFail│ │ScaleFailed│
         │             └──────────────┘                  └────┬──────┘ └────┬─────┘ └────┬──────┘ └────┬──────┘
         │                                                     │            │            │            │
         │                                                     │ retry      │ retry      │ retry      │ retry
         │                                                     ▼            ▼            ▼            ▼
         │                                              ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐
         │                                              │ Pausing  │  │DryRunning│  │ Upgrading│  │ Scaling  │
         │                                              └──────────┘  └──────────┘  └──────────┘  └──────────┘
         │
         │  用户操作触发状态转换
         │
    ┌────┴──────────────────────────────────────────────────────────────────────────────────────────────────┐
    │                                                                                                       │
    │   ┌────────────────┐                                                                                  │
    │   │    Running     │                                                                                  │
    │   └───────┬────────┘                                                                                  │
    │           │                                                                                           │
    │   ┌───────┼───────┬───────────┬───────────┬───────────┬───────────┐                                   │
    │   │       │       │           │           │           │           │                                   │
    │   ▼       ▼       ▼           ▼           ▼           ▼           ▼                                   │
    │ K8s版本  节点数  删除请求    暂停请求   DryRun请求   纳管请求    健康检查                             │
    │   变化    变化                                                                                        │
    │   │       │       │           │           │           │           │                                   │
    │   ▼       ▼       ▼           ▼           ▼           ▼           ▼                                   │
    │ Upgrading Scaling Deleting   Pausing   DryRunning  Managing   EnsureCluster                           │
    │                                                                                                       │
    └───────────────────────────────────────────────────────────────────────────────────────────────────────┘
```
### 三、与现有 ClusterStatus 的映射关系
当前系统使用 `ClusterStatus`（定义在 [bkecluster_status.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_status.go)）表示集群操作状态。状态机的 `ClusterLifecycleState` 与现有 `ClusterStatus` 是**双向映射**关系：
#### 3.1 ClusterStatus → ClusterLifecycleState 映射
| ClusterStatus | ClusterLifecycleState | 说明 |
|---------------|----------------------|------|
| `ClusterInitializing` | `StateProvisioning` | 集群初始化中 |
| `ClusterReady` | `StateRunning` | 集群就绪 |
| `ClusterUpgrading` | `StateUpgrading` | 版本升级中 |
| `ClusterMasterScalingUp` | `StateScaling` | Master 扩容 |
| `ClusterMasterScalingDown` | `StateScaling` | Master 缩容 |
| `ClusterWorkerScalingUp` | `StateScaling` | Worker 扩容 |
| `ClusterWorkerScalingDown` | `StateScaling` | Worker 缩容 |
| `ClusterManaging` | `StateManaging` | 纳管中 |
| `ClusterDeleting` | `StateDeleting` | 删除中 |
| `ClusterPaused` | `StatePausing` | 暂停中（注：暂停成功后状态保持） |
| `ClusterDryRun` | `StateDryRunning` | DryRun 中 |
| `ClusterInitializationFailed` | `StateInitFailed` | 初始化失败 |
| `ClusterUpgradeFailed` | `StateUpgradeFailed` | 升级失败 |
| `ClusterScaleFailed` | `StateScaleFailed` | 扩缩容失败 |
| `ClusterManageFailed` | `StateManageFailed` | 纳管失败 |
| `ClusterDeleteFailed` | `StateDeleteFailed` | 删除失败 |
| `ClusterPauseFailed` | `StatePauseFailed` | 暂停失败 |
| `ClusterDryRunFailed` | `StateDryRunFailed` | DryRun 失败 |
| `ClusterUnknown` | `StateNone` | 未知状态，重新判断 |
#### 3.2 ClusterLifecycleState → ClusterStatus 映射
```go
func (sm *ClusterStateMachine) syncClusterStatus(ctx *StateContext) {
    status := ctx.BKECluster.Status
    
    status.ClusterStatus = confv1beta1.ClusterStatus(sm.currentState)
    
    switch sm.currentState {
    case StateProvisioning:
        status.ClusterHealthState = confv1beta1.Deploying
    case StateRunning:
        status.ClusterHealthState = confv1beta1.Healthy
        status.Ready = true
    case StateUpgrading:
        status.ClusterHealthState = confv1beta1.Upgrading
    case StateScaling:
        // 保持原有的具体扩缩容状态
    case StateDeleting:
        status.ClusterHealthState = confv1beta1.Deleting
    case StateInitFailed:
        status.ClusterHealthState = confv1beta1.DeployFailed
    case StateUpgradeFailed:
        status.ClusterHealthState = confv1beta1.UpgradeFailed
    case StateManageFailed:
        status.ClusterHealthState = confv1beta1.ManageFailed
    }
}
```
### 四、状态转换触发条件
#### 4.1 从 StateNone 触发的转换
| 触发条件 | 目标状态 | Event | 说明 |
|---------|---------|-------|------|
| `DeletionTimestamp != nil` | `StateDeleting` | `delete` | 集群被删除 |
| `Spec.Reset == true` | `StateDeleting` | `delete` | 重置集群 |
| `Spec.Pause == true` | `StatePausing` | `pause` | 暂停请求 |
| `Spec.DryRun == true` | `StateDryRunning` | `dryrun` | DryRun 请求 |
| `Status.Ready == false && DeletionTimestamp == nil` | `StateProvisioning` | `provision` | 新集群部署 |
| `Spec.ClusterConfig == nil && Status.Ready == false` | `StateManaging` | `manage` | 纳管现有集群 |
#### 4.2 从 StateRunning 触发的转换
| 触发条件 | 目标状态 | Event | 说明 |
|---------|---------|-------|------|
| `DeletionTimestamp != nil` | `StateDeleting` | `delete` | 删除请求 |
| `Spec.Reset == true` | `StateDeleting` | `delete` | 重置请求 |
| `Spec.Pause == true` | `StatePausing` | `pause` | 暂停请求 |
| `Spec.DryRun == true` | `StateDryRunning` | `dryrun` | DryRun 请求 |
| `KubernetesVersion 变化` | `StateUpgrading` | `upgrade` | K8s 版本升级 |
| `OpenFuyaoVersion 变化` | `StateUpgrading` | `upgrade` | OpenFuyao 版本升级 |
| `ContainerdVersion 变化` | `StateUpgrading` | `upgrade` | Containerd 版本升级 |
| `Master 节点数增加` | `StateScaling` | `scale` | Master 扩容 |
| `Master 节点数减少` | `StateScaling` | `scale` | Master 缩容 |
| `Worker 节点数增加` | `StateScaling` | `scale` | Worker 扩容 |
| `Worker 节点数减少` | `StateScaling` | `scale` | Worker 缩容 |
#### 4.3 失败状态的重试转换
所有失败状态都可以通过 `retry` 事件转换回对应的运行中状态：

| 失败状态 | 重试目标状态 | Event |
|---------|-------------|-------|
| `StateInitFailed` | `StateProvisioning` | `retry` |
| `StateUpgradeFailed` | `StateUpgrading` | `retry` |
| `StateScaleFailed` | `StateScaling` | `retry` |
| `StateManageFailed` | `StateManaging` | `retry` |
| `StateDeleteFailed` | `StateDeleting` | `retry` |
| `StatePauseFailed` | `StatePausing` | `retry` |
| `StateDryRunFailed` | `StateDryRunning` | `retry` |
### 五、状态机与现有 PhaseFlow 的对比
| 维度 | PhaseFlow（现有） | StateMachine（重构后） |
|------|------------------|----------------------|
| **状态表示** | `ClusterStatus` + `PhaseStatus` 分离 | `ClusterLifecycleState` 统一状态模型 |
| **状态转换** | 隐式（通过 Phase 执行结果推断） | 显式（通过转换表定义） |
| **转换验证** | 无验证 | `CanTransitionTo` 验证合法性 |
| **执行模型** | 两阶段（Calculate + Execute） | 单阶段（Reconcile 中完成） |
| **错误处理** | 聚合所有错误后返回 | 按步骤返回，支持重试标记 |
| **状态持久化** | Phase.Report 写入 PhaseStatus | State.Enter/Exit 同步 ClusterStatus |
| **可观测性** | 需查看 PhaseStatus 列表 | 状态机状态直接反映集群生命周期 |
### 六、状态持久化策略
#### 6.1 状态存储位置
```go
type BKEClusterStatus struct {
    // 集群级别状态（状态机状态）
    ClusterStatus ClusterStatus `json:"clusterStatus,omitempty"`
    
    // 集群健康状态（辅助状态）
    ClusterHealthState ClusterHealthState `json:"clusterHealthState,omitempty"`
    
    // Phase 级别状态（保留，用于 UI 展示进度）
    PhaseStatus PhaseStatus `json:"phaseStatus,omitempty"`
    
    // 阶段一新增：升级状态跟踪
    UpgradeStatus *UpgradeStatus `json:"upgradeStatus,omitempty"`
}
```
#### 6.2 状态同步时机
| 时机 | 操作 | 说明 |
|------|------|------|
| `State.Enter()` | 设置 `ClusterStatus` 为当前状态 | 进入新状态时立即同步 |
| `State.Exit()` | 清理临时状态 | 退出状态时清理 |
| Phase 执行成功 | 更新 `PhaseStatus` | 保留现有 Phase 进度展示 |
| 状态转换完成 | 更新 `ClusterHealthState` | 同步健康状态 |
### 七、特殊场景处理
#### 7.1 暂停恢复
```
StateRunning ──pause──► StatePausing ──success──► StateRunning
                              │
                              │ fail
                              ▼
                       StatePauseFailed
                              │
                              │ retry
                              ▼
                         StatePausing
```
**说明**：暂停成功后集群回到 `StateRunning`，但 `Spec.Pause=true` 保持。下次 Reconcile 时会检测到暂停请求已完成，不再触发状态转换。
#### 7.2 删除流程
```
任意状态 ──delete──► StateDeleting ──success──► StateNone (资源已删除)
                            │
                            │ fail
                            ▼
                     StateDeleteFailed
                            │
                            │ retry
                            ▼
                       StateDeleting
```
**说明**：删除成功后资源被 GC 回收，状态机状态无意义。删除失败时需要人工介入或重试。
#### 7.3 并发操作冲突
当用户在 `StateUpgrading` 过程中请求删除时：
```
StateUpgrading ──delete──► ???
```
**处理策略**：状态转换表不允许 `Upgrading → Deleting` 直接转换。需要：
1. 等待升级完成或失败
2. 或者强制取消升级（需要额外实现取消机制）

当前设计采用**保守策略**：不允许跨类型状态转换，确保操作原子性。


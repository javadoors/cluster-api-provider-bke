# KEPU-2 声明式架构最小可运行版本 — 需求分解
## 分解原则
1. **每个需求 ≤ 2 人月**（约 40 人天）
2. **最小可运行版本**：优先实现安装场景，再扩展升级和扩缩容
3. **依赖关系明确**：底层 CRD/引擎先行，组件 YAML 后行
4. **渐进可验证**：每个需求完成后可独立验证
## 需求总览
| 层次 | 需求编号 | 需求名称 | 工作量 | 场景 |
|------|---------|---------|--------|------|
| **L1 基础设施** | R01 | CRD 定义与注册 | 8人天 | 全部 |
| | R02 | ActionEngine 核心引擎 | 18人天 | 全部 |
| | R03 | 模板变量系统 | 8人天 | 全部 |
| **L2 控制器** | R04 | ComponentVersion Controller | 15人天 | 安装+升级 |
| | R05 | ClusterVersion Controller | 15人天 | 升级 |
| | R06 | NodeConfig Controller | 12人天 | 扩缩容 |
| **L3 安装场景** | R07 | 安装 DAG 编排与 Feature Gate 切换 | 12人天 | 安装 |
| | R08 | 节点级组件安装 YAML（bkeAgent/nodesEnv/containerd） | 10人天 | 安装 |
| | R09 | 控制面组件安装 YAML（etcd/certs/loadBalancer/kubernetes） | 15人天 | 安装 |
| | R10 | 集群级组件安装 YAML（clusterAPI/addon/nodesPostProcess/agentSwitch） | 10人天 | 安装 |
| **L4 升级场景** | R11 | 升级 DAG 编排与滚动策略 | 12人天 | 升级 |
| | R12 | 节点级组件升级 YAML（containerd/etcd/kubernetes） | 12人天 | 升级 |
| | R13 | 集群级组件升级 YAML（bkeProvider/openFuyao/bkeAgent） | 8人天 | 升级 |
| | R14 | 升级回滚机制 | 10人天 | 升级 |
| **L5 扩缩容场景** | R15 | 扩容：NodeConfig 创建与组件安装 | 10人天 | 扩容 |
| | R16 | 缩容：节点删除与资源清理 | 10人天 | 缩容 |
| **L6 验证** | R17 | 安装场景 E2E 测试 | 8人天 | 安装 |
| | R18 | 升级与扩缩容 E2E 测试 | 10人天 | 升级+扩缩容 |

**总计：193 人天（约 9.65 人月）**
## 详细需求描述
### R01: CRD 定义与注册（8 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 定义 ComponentVersion、NodeConfig、ClusterVersion、ReleaseImage 四个 CRD，完成 API 注册与 DeepCopy 生成 |
| **输入** | KEPU-2 提案 §5.2 CRD 设计 |
| **输出** | `api/cvo/v1beta1/` 和 `api/nodecomponent/v1alpha1/` 下的 types.go + zz_generated.deepcopy.go + CRD YAML |
| **验收标准** | ① 四个 CRD 可通过 `kubectl apply` 创建 ② DeepCopy 自动生成 ③ CRD Validation 准确（必填字段、枚举值、格式校验） ④ 可通过 `kubectl get componentversion` 查询 |
| **依赖** | 无 |

**关键工作项**：
- ComponentVersion CRD：含 ActionSpec（Steps/PreCheck/PostCheck/Strategy）、HealthCheckSpec、ComponentSource
- NodeConfig CRD：含 NodeSelector、Components 列表、Phase 状态机
- ClusterVersion CRD：含 DesiredVersion、ReleaseRef、UpgradeStrategy、Status（Phase/History/Steps）
- ReleaseImage CRD：含 Version、ComponentVersionRefs、Images、UpgradePaths、Compatibility
- ActionStrategy 增强：`waitForCompletion` 和 `failurePolicy` 字段
- CRD Validation 准入校验规则
### R02: ActionEngine 核心引擎（18 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现通用 ActionEngine，解释执行 ActionSpec 中的 Steps，支持 Script/Manifest/Chart/Kubectl 四种 ActionType |
| **输入** | KEPU-2 提案 §5.5 ActionEngine 设计 |
| **输出** | `pkg/actionengine/` 目录下的 engine.go、executor/ 子包 |
| **验收标准** | ① Script 类型：通过 BKE Agent Command 在目标节点执行 shell 脚本 ② Manifest 类型：`kubectl apply` 渲染后的 YAML ③ Chart 类型：Helm upgrade --install ④ Kubectl 类型：Apply/Delete/Patch/Wait/Drain 操作 ⑤ PreCheck 失败时中止执行 ⑥ PostCheck 支持重试等待 ⑦ Condition 条件表达式正确评估 ⑧ Rolling 策略逐节点执行 steps+postCheck 后再处理下一节点 |
| **依赖** | R01 |

**关键工作项**：
- `engine.go`：主引擎，解析 ActionSpec，按 Strategy 调度执行
- `executor/script_executor.go`：通过 Agent Command API 在目标节点执行脚本
- `executor/manifest_executor.go`：渲染模板后 kubectl apply
- `executor/chart_executor.go`：Helm upgrade --install
- `executor/kubectl_executor.go`：kubectl 操作封装
- Rolling 执行器：逐节点执行 steps → postCheck → 下一节点（`waitForCompletion=true`）
- Parallel 执行器：所有节点并行执行
- Serial 执行器：逐节点串行执行
- Condition 评估器：解析 `{{.NodeRole}} == master` 等条件表达式
- 步骤间输出引用：`{{.Steps.check-need-upgrade.stdout}}`
### R03: 模板变量系统（8 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现 TemplateContext 和模板渲染，支持从 BKECluster/ComponentVersion/NodeConfig 聚合变量 |
| **输入** | KEPU-2 提案 §5.3 模板变量系统 |
| **输出** | `pkg/actionengine/template.go` |
| **验收标准** | ① `{{.Version}}`、`{{.NodeIP}}`、`{{.ImageRepo}}` 等基础变量正确渲染 ② `{{.EtcdInitialCluster}}` 等计算变量正确生成 ③ 缺失变量报错而非静默空值 ④ 支持模板函数（`len`、`index` 等） ⑤ 渲染失败时返回清晰错误信息 |
| **依赖** | R01 |

**关键工作项**：
- TemplateContext 结构体定义与填充
- 变量来源聚合：BKECluster Spec → ClusterVersion → NodeConfig → ComponentVersion
- 计算变量生成：EtcdInitialCluster（遍历 master 节点生成 `name=url` 列表）、ControlPlaneEndpoint
- Go `text/template` 渲染引擎
- 缺失变量检测与报错
- 模板渲染预览（dry-run 模式，用于调试）
### R04: ComponentVersion Controller（15 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现 ComponentVersion 控制器，驱动组件生命周期（安装→就绪→升级→回滚） |
| **输入** | KEPU-2 提案 §5.6 ComponentVersion Controller 设计 |
| **输出** | `controllers/nodecomponent/componentversion_controller.go` |
| **验收标准** | ① 创建 ComponentVersion CR 后，Controller 自动执行 installAction ② installAction 完成后 status.phase=Ready ③ 修改 spec.version 后触发 upgradeAction ④ upgradeAction 失败后自动执行 rollbackAction ⑤ healthCheck 周期执行，更新 status.conditions ⑥ uninstallAction 在 CR 删除时执行（Finalizer） ⑦ 依赖组件未就绪时等待（status.phase=Pending） |
| **依赖** | R01, R02, R03 |

**关键工作项**：
- Reconcile 主循环：根据 status.phase 决定执行 installAction/upgradeAction/uninstallAction
- 依赖检查：查询 dependencies 中组件的 status.phase 是否均为 Ready
- 状态机：Pending → Installing → Ready → Upgrading → Ready / Failed → RollingBack → Ready/Failed
- Finalizer：删除 CR 时执行 uninstallAction
- 健康检查：周期性执行 healthCheck，更新 conditions
- 升级失败回滚：upgradeAction 失败 → 执行 rollbackAction
- 旧版本卸载：升级时先查找旧版本 ComponentVersion，执行其 uninstallAction
### R05: ClusterVersion Controller（15 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现 ClusterVersion 控制器，编排集群级升级流程（解析 ReleaseImage → 按序更新 ComponentVersion） |
| **输入** | KEPU-2 提案 §5.6 ClusterVersion Controller 设计 |
| **输出** | `controllers/cvo/clusterversion_controller.go` |
| **验收标准** | ① 修改 desiredVersion 后，Controller 解析对应 ReleaseImage ② 按 DAG 依赖顺序逐步更新 ComponentVersion 的 spec.version ③ 升级过程中 status.phase=Upgrading，记录 currentStepIndex ④ 全部组件升级完成后 status.phase=Ready，更新 currentVersion ⑤ 支持暂停/恢复升级（spec.pause） ⑥ 升级历史记录（status.history） |
| **依赖** | R01, R04 |

**关键工作项**：
- Reconcile 主循环：检测 desiredVersion 变更 → 触发升级流程
- ReleaseImage 解析：查找 ReleaseImage → 获取 ComponentVersionRef 列表
- DAG 调度：按依赖拓扑排序，逐步更新 ComponentVersion
- 升级步骤跟踪：status.upgradeSteps + currentStepIndex
- 暂停/恢复：spec.pause=true 时停止推进
- 升级历史：记录每次升级的 fromVersion → toVersion、开始时间、结束时间、结果
- BKECluster 关联：监听 BKECluster 变更，自动创建/更新 ClusterVersion
### R06: NodeConfig Controller（12 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现 NodeConfig 控制器，管理节点级组件的安装与卸载，支持扩缩容场景 |
| **输入** | KEPU-2 提案 §5.2 NodeConfig CRD 设计 |
| **输出** | `controllers/nodecomponent/nodeconfig_controller.go` |
| **验收标准** | ① 新节点加入集群时，自动创建 NodeConfig（匹配 ComponentVersion 的 nodeSelector） ② NodeConfig 创建后，触发对应 ComponentVersion 的 installAction ③ NodeConfig 删除时（phase=Deleting），触发 uninstallAction ④ 节点角色变更时，自动更新 NodeConfig 的 components 列表 ⑤ NodeConfig status 反映各组件安装状态 |
| **依赖** | R01, R04 |

**关键工作项**：
- Reconcile 主循环：监听 BKENode 变更 → 创建/更新/删除 NodeConfig
- NodeSelector 匹配：根据节点角色（master/worker）匹配 ComponentVersion
- 组件列表生成：根据匹配结果填充 NodeConfig.spec.components
- 删除流程：NodeConfig phase=Deleting → 触发各组件 uninstallAction → 移除 NodeConfig
- 状态同步：NodeConfig.status.components 反映各组件安装状态
- BKENode Watcher：监听 BKENode 变更事件，触发 NodeConfig Reconcile
### R07: 安装 DAG 编排与 Feature Gate 切换（12 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现安装场景的 DAG 编排逻辑，以及 Feature Gate 渐进切换机制 |
| **输入** | KEPU-2 提案 §6 迁移策略 |
| **输出** | `pkg/cvo/dag_scheduler.go`、Feature Gate 配置 |
| **验收标准** | ① Feature Gate `DeclarativeOrchestration=false` 时，旧 PhaseFlow 正常运行 ② Feature Gate `DeclarativeOrchestration=true` 时，安装流程走 ActionEngine ③ DAG 拓扑排序正确（bkeAgent → nodesEnv → certs → etcd → loadBalancer → kubernetes → clusterAPI → addon → nodesPostProcess → agentSwitch） ④ 循环依赖检测 ⑤ 安装失败时，DAG 调度停止，标记失败组件 ⑥ BKEClusterReconciler 中根据 Feature Gate 选择执行路径 |
| **依赖** | R02, R04 |

**关键工作项**：
- DAGScheduler：解析 ComponentVersion 依赖关系，生成拓扑排序
- 循环依赖检测：构建依赖图时检测环
- Feature Gate 定义：`DeclarativeOrchestration` bool flag
- BKEClusterReconciler 修改：根据 Feature Gate 选择 PhaseFlow 或 ClusterVersion 路径
- 安装 DAG 定义：硬编码初始安装顺序（后续可配置化）
- 安装流程触发：BKECluster 创建 → 自动创建 ClusterVersion + ReleaseImage + ComponentVersion
### R08: 节点级组件安装 YAML（10 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 编写 bkeAgent、nodesEnv、containerd 三个节点级组件的 ComponentVersion YAML |
| **输入** | 现有 Phase 实现：ensure_bke_agent.go、ensure_nodes_env.go、ensure_containerd_upgrade.go |
| **输出** | `config/components/bkeagent-*.yaml`、`config/components/nodesenv-*.yaml`、`config/components/containerd-*.yaml` |
| **验收标准** | ① bkeAgent YAML：installAction 推送 Agent 二进制+service+kubeconfig，postCheck 验证 Agent 健康 ② nodesEnv YAML：installAction 执行环境初始化脚本（lxcfs/nfsutils/etcdctl/helm/calicoctl/runc），strategy=Parallel ③ containerd YAML：installAction 安装二进制+service+配置，postCheck 验证 systemctl is-active containerd ④ 三个 YAML 的模板变量正确渲染 ⑤ 通过 ActionEngine 执行后，组件安装成功 |
| **依赖** | R02, R03 |

**关键工作项**：
- bkeAgent：从 ensure_bke_agent.go 提取 Agent 推送逻辑 → Script 步骤
- nodesEnv：从 ensure_nodes_env.go 提取脚本列表 → Script 步骤（Parallel）
- containerd：从 ensure_containerd_upgrade.go + containerruntime containerd.go 提取安装/卸载命令 → Script 步骤
- 模板变量：`{{.AgentPort}}`、`{{.HTTPRepo}}`、`{{.ImageRepo}}`、`{{.Version}}`
- Source 声明：HTTP 下载地址 + checksum
### R09: 控制面组件安装 YAML（15 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 编写 etcd、certs、loadBalancer、kubernetes 四个控制面组件的 ComponentVersion YAML |
| **输入** | 现有 Phase 实现：ensure_etcd_upgrade.go、ensure_certs.go、ensure_load_balance.go、ensure_master_init.go |
| **输出** | `config/components/etcd-*.yaml`、`config/components/certs-*.yaml`、`config/components/loadbalancer-*.yaml`、`config/components/kubernetes-*.yaml` |
| **验收标准** | ① etcd YAML：installAction 创建 etcd 用户+数据目录+static pod manifest+重启 kubelet，postCheck 验证 Pod Running+Ready ② certs YAML：installAction 生成 CA/etcd/front-proxy 证书+上传 Secret，postCheck 验证证书存在 ③ loadBalancer YAML：installAction 部署 keepalived+haproxy static pod+ConfigMap，postCheck 验证 Pod Running ④ kubernetes YAML：installAction 按 condition 分支执行 kubeadm init/join control plane/join worker，postCheck 验证 kubelet active ⑤ 模板变量 `{{.EtcdInitialCluster}}`、`{{.ControlPlaneEndpoint}}` 正确渲染 |
| **依赖** | R02, R03 |

**关键工作项**：
- etcd：从 manifests.go 提取 static pod 模板 → Manifest 步骤；setupEtcdEnvironment → Script 步骤
- certs：从 ensure_certs.go + certPlugin 提取证书生成逻辑 → Script + Kubectl 步骤
- loadBalancer：从 ensure_load_balance.go + command.HA 提取 HAProxy+Keepalived 配置 → Manifest + Kubectl 步骤
- kubernetes：从 ensure_master_init.go + kubeadm.go 提取 init/join 逻辑 → Script 步骤（condition 分支）
- 计算变量：EtcdInitialCluster（遍历 master 节点生成 `name=https://ip:2380` 列表）
### R10: 集群级组件安装 YAML（10 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 编写 clusterAPI、addon、nodesPostProcess、agentSwitch 四个集群级组件的 ComponentVersion YAML |
| **输入** | 现有 Phase 实现：ensure_cluster_api_obj.go、ensure_addon_deploy.go、ensure_nodes_postprocess.go、ensure_agent_switch.go |
| **输出** | `config/components/clusterapi-*.yaml`、`config/components/addon-*.yaml`、`config/components/nodespostprocess-*.yaml`、`config/components/agentswitch-*.yaml` |
| **验收标准** | ① clusterAPI YAML：installAction 创建 Cluster/Machine 对象，postCheck 验证对象存在 ② addon YAML：installAction 通过 Chart 安装 calico/coredns/kube-proxy，postCheck 验证 Deployment Available ③ nodesPostProcess YAML：installAction 执行后置脚本，strategy=Parallel ④ agentSwitch YAML：installAction 切换 Agent kubeconfig 指向目标集群 ⑤ 四个 YAML 的 scope=Cluster 或 scope=Node 正确设置 |
| **依赖** | R02, R03 |

**关键工作项**：
- clusterAPI：从 ensure_cluster_api_obj.go 提取 CAPI 对象创建 → Kubectl Apply 步骤
- addon：从 ensure_addon_deploy.go 提取 addon 部署逻辑 → Chart 步骤
- nodesPostProcess：从 ensure_nodes_postprocess.go 提取后置脚本 → Script 步骤
- agentSwitch：从 ensure_agent_switch.go 提取 kubeconfig 切换 → Script + Kubectl 步骤
### R11: 升级 DAG 编排与滚动策略（12 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现升级场景的 DAG 编排逻辑，支持滚动升级策略和升级前兼容性检查 |
| **输入** | KEPU-2 提案 §5.6 + 现有 ClusterUpgradePhaseNames 顺序 |
| **输出** | `pkg/cvo/dag_scheduler.go` 升级路径、`pkg/cvo/validator.go` |
| **验收标准** | ① 升级 DAG 顺序：bkeAgent → containerd → etcd → kubernetes(master) → kubernetes(worker) → openFuyao ② 兼容性检查：ReleaseImage.upgradePaths 中定义允许的升级路径 ③ 不兼容的升级路径被拒绝（status.phase=ValidationFailed） ④ 滚动策略：逐节点升级时，每个节点执行 steps+postCheck 成功后再处理下一节点 ⑤ 升级过程中可暂停（spec.pause=true） ⑥ 升级失败时停止推进，保留当前进度 |
| **依赖** | R05, R07 |

**关键工作项**：
- 升级 DAG 定义：与安装 DAG 不同，升级有特定顺序（Agent 先行）
- 兼容性校验器：检查 fromVersion → toVersion 是否在 upgradePaths 允许范围内
- 滚动策略执行：ClusterVersion Controller 逐组件推进，每组件内逐节点推进
- 升级暂停/恢复：spec.pause 控制推进
- 升级进度跟踪：status.upgradeSteps 记录每个组件的升级状态
### R12: 节点级组件升级 YAML（12 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 编写 containerd、etcd、kubernetes 三个节点级组件的 upgradeAction 和 rollbackAction |
| **输入** | 现有 Phase 实现：ensure_containerd_upgrade.go、ensure_etcd_upgrade.go、ensure_master_upgrade.go、ensure_worker_upgrade.go |
| **输出** | 更新 `config/components/containerd-*.yaml`、`config/components/etcd-*.yaml`、`config/components/kubernetes-*.yaml` |
| **验收标准** | ① containerd upgradeAction：preCheck 检查 Agent → 停止旧版 → 清理 → 安装新版 → postCheck，strategy=Rolling batchSize=1 ② etcd upgradeAction：备份 → 备份 /etc/kubernetes → 预拉镜像 → 生成新 manifest → 重启 kubelet → postCheck 验证 Pod 版本+健康，strategy=Rolling batchSize=1 waitForCompletion=true ③ kubernetes upgradeAction：preCheck 检查集群健康 → kubeadm upgrade → 重启 kubelet → postCheck 验证 Node Ready，strategy=Rolling batchSize=1 ④ 三个组件的 rollbackAction 可回退到旧版本 ⑤ etcd 逐节点升级：第一个节点执行备份，后续节点跳过备份 |
| **依赖** | R08, R09, R11 |

**关键工作项**：
- containerd：从 ensure_containerd_upgrade.go + reset/clean.go 提取升级/卸载命令
- etcd：从 ensure_etcd_upgrade.go + kubeadm.go:upgradeEtcd 提取升级流程，condition=`{{.IsFirstEtcdNode}} == true` 控制备份
- kubernetes：从 ensure_master_upgrade.go + ensure_worker_upgrade.go + kubeadm.go:upgradeControlPlane 提取升级流程
- rollbackAction：记录旧版本信息，回退 manifest 镜像 tag
### R13: 集群级组件升级 YAML（8 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 编写 bkeProvider、openFuyao、bkeAgent 三个集群级组件的 upgradeAction |
| **输入** | 现有 Phase 实现：ensure_provider_self_upgrade.go、ensure_component_upgrade.go、ensure_agent_upgrade.go |
| **输出** | 更新 `config/components/bkeprovider-*.yaml`、`config/components/openfuyao-*.yaml`、`config/components/bkeagent-*.yaml` |
| **验收标准** | ① bkeProvider upgradeAction：Patch Deployment 镜像 tag → postCheck 验证 Deployment Available ② openFuyao upgradeAction：Patch Deployment 镜像 tag → postCheck 验证 Deployment Available ③ bkeAgent upgradeAction：停止 Agent → 替换二进制 → 启动 Agent → postCheck 验证 Agent 健康，strategy=Rolling batchSize=1 ④ 三个组件升级不影响集群可用性 |
| **依赖** | R08, R11 |

**关键工作项**：
- bkeProvider：从 ensure_provider_self_upgrade.go 提取自升级逻辑 → Kubectl Patch 步骤
- openFuyao：从 ensure_component_upgrade.go 提取核心组件升级逻辑 → Kubectl Patch 步骤
- bkeAgent：从 ensure_agent_upgrade.go 提取 Agent 升级逻辑 → Script 步骤
### R14: 升级回滚机制（10 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现升级失败后的自动/手动回滚机制 |
| **输入** | KEPU-2 提案 §5.6 回滚逻辑 |
| **输出** | `pkg/cvo/rollback.go` |
| **验收标准** | ① 单组件升级失败后，自动执行该组件的 rollbackAction ② rollbackAction 执行成功后，status.phase=Ready，status.installedVersion 回退到旧版本 ③ rollbackAction 执行失败后，status.phase=RollbackFailed，需人工介入 ④ ClusterVersion 级别回滚：手动将 desiredVersion 改回旧版本，触发逐组件回滚 ⑤ 回滚历史记录在 status.history 中 ⑥ etcd 升级失败后，支持从备份恢复 |
| **依赖** | R04, R05, R11 |

**关键工作项**：
- 单组件回滚：upgradeAction 失败 → 自动执行 rollbackAction
- 集群级回滚：desiredVersion 回退 → 逐组件执行 rollbackAction
- etcd 备份恢复：rollbackAction 中从备份恢复 etcd 数据
- 回滚状态跟踪：RollingBack → Ready/RollbackFailed
- 历史记录：记录每次升级/回滚的结果
### R15: 扩容 — NodeConfig 创建与组件安装（10 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现新节点加入集群时，自动创建 NodeConfig 并安装对应组件 |
| **输入** | KEPU-2 提案 §5.2 NodeConfig + 现有 ClusterScaleMasterUpPhaseNames / ClusterScaleWorkerUpPhaseNames |
| **输出** | NodeConfig Controller 扩容逻辑 |
| **验收标准** | ① 新增 master 节点：自动创建 NodeConfig → 安装 bkeAgent + nodesEnv + containerd + etcd + loadBalancer + kubernetes（join control plane） ② 新增 worker 节点：自动创建 NodeConfig → 安装 bkeAgent + nodesEnv + containerd + kubernetes（join worker） ③ 扩容过程中不影响现有节点 ④ NodeConfig status 反映各组件安装进度 ⑤ 扩容失败时，NodeConfig status.phase=Failed，可重试 |
| **依赖** | R06, R08, R09 |

**关键工作项**：
- BKENode Watcher：监听新节点加入事件
- NodeConfig 自动创建：根据节点角色匹配 ComponentVersion nodeSelector
- 组件安装触发：NodeConfig 创建后，ComponentVersion Controller 对新节点执行 installAction
- Master 扩容：kubernetes installAction 中 condition=`{{.IsFirstMaster}} == false` 走 join control plane 分支
- Worker 扩容：kubernetes installAction 中 condition=`{{.NodeRole}} == worker` 走 join worker 分支
### R16: 缩容 — 节点删除与资源清理（10 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 实现节点移除时，自动执行组件卸载和资源清理 |
| **输入** | KEPU-2 提案 §5.2 NodeConfig + 现有 EnsureWorkerDelete / EnsureMasterDelete 逻辑 |
| **输出** | NodeConfig Controller 缩容逻辑 + nodeDelete ComponentVersion YAML |
| **验收标准** | ① Worker 缩容：drain 节点 → 删除 Machine → 等待节点移除 → 清理 Agent ② Master 缩容：drain 节点 → 移除 etcd member → 删除 Machine → 等待节点移除 → 清理 Agent ③ 缩容过程中集群保持可用（etcd 多数派不丢失） ④ NodeConfig phase=Deleting 后执行各组件 uninstallAction ⑤ 缩容失败时，NodeConfig 保留，可重试 |
| **依赖** | R06 |

**关键工作项**：
- nodeDelete ComponentVersion YAML：drain + delete machine + remove etcd member + clean
- NodeConfig 删除流程：phase=Deleting → 逐组件执行 uninstallAction → 移除 NodeConfig
- Master 缩容保护：确保 etcd 多数派可用，最后一个 master 不允许缩容
- Kubectl Drain 操作：ActionEngine 的 Kubectl Drain 类型
### R17: 安装场景 E2E 测试（8 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 验证声明式架构下完整安装流程 |
| **输入** | R07-R10 的实现 |
| **输出** | E2E 测试用例 |
| **验收标准** | ① Feature Gate=true 时，从零创建 BKECluster → 自动创建 ClusterVersion + ReleaseImage + ComponentVersion → ActionEngine 执行安装 → 集群 Ready ② Feature Gate=false 时，旧 PhaseFlow 正常运行 ③ 安装过程中组件依赖顺序正确 ④ 安装失败时，错误信息清晰，可重试 ⑤ 安装完成后，healthCheck 周期执行 |
| **依赖** | R07, R08, R09, R10 |
### R18: 升级与扩缩容 E2E 测试（10 人天）
| 项目 | 内容 |
|------|------|
| **目标** | 验证声明式架构下升级和扩缩容流程 |
| **输入** | R11-R16 的实现 |
| **输出** | E2E 测试用例 |
| **验收标准** | ① 全量升级：修改 ClusterVersion desiredVersion → 逐组件升级 → 集群 Ready ② 单组件升级：修改 ComponentVersion version → 仅该组件升级 ③ etcd 逐节点升级：每个节点升级后健康检查通过再处理下一节点 ④ 升级回滚：升级失败后自动回滚 ⑤ Master 扩容：新增 master 节点 → 自动安装组件 → join control plane ⑥ Worker 扩容：新增 worker 节点 → 自动安装组件 → join worker ⑦ Worker 缩容：drain → 删除 Machine → 清理 ⑧ Master 缩容：drain → 移除 etcd member → 删除 Machine → 清理 |
| **依赖** | R11, R12, R13, R14, R15, R16 |
## 依赖关系图
```
R01 (CRD) ──┬──→ R02 (ActionEngine) ──┬──→ R04 (CV Controller) ──┬──→ R07 (安装DAG) ──→ R11 (升级DAG) ──┬──→ R14 (回滚)
             ├──→ R03 (模板变量)  ──┘        │                       │                       │
             │                              ├──→ R05 (ClusterVer) ──┘                       ├──→ R12 (节点升级YAML)
             │                              │                                               │
             │                              └──→ R06 (NodeConfig) ──┬──→ R15 (扩容)          ├──→ R13 (集群升级YAML)
             │                                                      │                       │
             └──→ R08 (节点安装YAML) ──→ R09 (控制面安装YAML)          └──→ R16 (缩容)          │
                 └──→ R10 (集群安装YAML)                                                       │
                                                                                           │
R07 + R08 + R09 + R10 ──→ R17 (安装E2E)                                                    │
R11 + R12 + R13 + R14 + R15 + R16 ──→ R18 (升级扩缩容E2E) ←────────────────────────────────┘
```
## 实施路径（按阶段交付）
| 阶段 | 需求 | 交付物 | 工作量 |
|------|------|--------|--------|
| **P0 基础设施** | R01 + R02 + R03 | CRD + ActionEngine + 模板系统 | 34 人天 |
| **P1 安装 MVP** | R04 + R07 + R08 | CV Controller + 安装DAG + 节点组件YAML | 37 人天 |
| **P2 安装完整** | R09 + R10 + R17 | 控制面+集群组件YAML + 安装E2E | 33 人天 |
| **P3 升级能力** | R05 + R11 + R12 + R13 | ClusterVer Controller + 升级DAG + 升级YAML | 47 人天 |
| **P4 扩缩容+回滚** | R06 + R14 + R15 + R16 + R18 | NodeConfig Controller + 回滚 + 扩缩容 + E2E | 42 人天 |

**P0+P1 即为最小可运行版本**（71 人天），可完成声明式架构下的集群安装。
        

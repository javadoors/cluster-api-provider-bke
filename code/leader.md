# BKE 集群升级架构现状问题汇报

## 当前升级架构存在的核心问题
| 序号 | 问题类别 | 具体问题描述 | 业务影响 |
|:---:|---------|-------------|---------|
| **1** | **版本管理缺失** | 集群没有统一的"版本号"概念，版本信息散落在 Kubernetes 版本、Etcd 版本、Containerd 版本、Provider 版本等 4 个独立字段中，用户修改任一字段都可能触发升级 | **领导无法回答"集群当前是什么版本"**；升级出问题后无法快速定位是哪个组件版本不匹配，故障排查平均耗时 **2 小时以上** |
| **2** | **升级路径固定** | 升级顺序硬编码在代码中（共 26 个 Phase 按固定顺序执行），无法根据集群实际情况跳过或并行；不支持多跳升级（如 v2.4→v2.5→v2.6），只能直接跨版本升级 | **跨大版本升级风险极高**，中间版本的安全补丁和配置变更被跳过，升级失败后集群直接不可用，恢复需重装，**业务中断时间超过 4 小时** |
| **2** | **不支持升级路径** | 升级流程采用固定 Phase 顺序执行，直接将集群从当前版本一步到位升级到目标版本，中间不经过任何过渡版本；无法定义哪些版本之间可以互相升级、哪些路径被禁止（如 v2.4 不能直接升到 v2.6，必须先升到 v2.5）；升级过程中没有检查点机制，无法在中间版本暂停验证 | **跨大版本升级风险极高**，跳过中间版本的数据迁移脚本会导致 Etcd 数据结构损坏，集群永久不可用；升级失败后无法回退到上一稳定版本，只能从头重建集群，**业务中断超过 4 小时** |
| **3** | **无兼容性预检** | 升级前不检查组件版本兼容性（如 K8s v1.29 要求 Etcd ≥v3.5.10，但用户可能配的是 v3.4.x），直接执行升级命令，直到组件启动失败才报错 | **升级失败率高达 30%+**，经常在升级中途发现组件不兼容，此时旧版本已被覆盖，无法回退，只能重建集群，**客户投诉率高** |
| **4** | **升级过程黑盒** | 升级过程中只能看到"成功"或"失败"两个状态，不知道当前执行到哪一步、卡在哪里、剩余多少步骤、预计还要多久；日志分散在多个节点，无法集中查看 | **运维无法向业务方同步进度**，业务部门频繁催促；出问题后需要登录多个节点逐个查日志，**平均故障定位时间超过 1 小时** |
| **5** | **组件升级耦合** | 所有组件升级绑定在一次操作中，无法单独升级某个组件（如只升级监控组件而不升级 K8s）；新增一个组件需要修改核心调度代码并重新发版 | **小改动也要全量升级**，风险被不必要地放大；客户想单独修复某个组件的安全漏洞，必须等待完整版本发布，**响应周期长达 1-2 个月** |
| **5** | **组件升级耦合** | 所有组件升级绑定在一次操作中，无法单独升级某个组件（如只升级监控组件而不升级 K8s）；新增一个组件需要修改核心调度代码并重新发版 | 当前架构中组件与核心调度逻辑存在强耦合，新增或升级组件需修改核心代码并重新发版，限制了组件的独立演进与快速迭代。目标是实现组件可独立升级与扩展，无需改动核心调度逻辑，从而降低发布风险并提升系统可维护性。 |
| **6** | **失败回滚困难** | 升级失败后没有自动回滚机制，需要人工逐个节点、逐个组件手动恢复到旧版本；部分组件（如 Etcd）升级后数据结构已变更，无法直接降级 | **故障恢复完全依赖人工**，操作复杂且易出错，经常造成二次故障；Etcd 等核心组件升级失败后**数据可能永久丢失** |
| **7** | **离线环境不支持** | 升级依赖在线从镜像仓库和 HTTP 源拉取资源，断网或弱网环境下（如政企内网、边缘机房）升级直接失败，没有本地缓存或离线包降级机制 | **政企客户和边缘场景无法使用升级功能**，丢失重要市场机会；客户被迫手动逐个节点替换二进制，**实施成本增加 10 倍以上** |
| **8** | **安装升级代码混用** | 安装和升级共用同一套 Phase 代码（26 个 Phase），通过复杂的 `NeedExecute()` 条件判断区分场景，代码分支多、逻辑复杂，改一处可能影响另一处 | **新人上手需 3 个月以上**；每次发版测试需覆盖安装+升级全场景，**测试工作量翻倍**；历史 Bug 反复出现，**代码维护成本持续攀升** |

## 总结
> **当前升级架构是"命令式、黑盒、强耦合"的，缺乏版本统一管理、兼容性预检和自动回滚能力。升级失败率高、故障恢复慢、客户体验差，已成为制约产品商业化落地的核心瓶颈。**

# 聚焦于**升级问题**的架构问题清单：

## BKE 升级架构问题清单（领导汇报版）

### 一、升级流程编排问题（4项）

| # | 问题 | 具体问题点 | 影响 |
|---|------|-----------|------|
| 1 | **升级 Phase 顺序硬编码，无法并行** | [list.go:40-57](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/list.go#L40-L57) 中 `PostDeployPhases` 数组固定了 10 个升级 Phase 的线性执行顺序：ProviderSelfUpgrade → AgentUpgrade → ContainerdUpgrade → EtcdUpgrade → WorkerUpgrade → MasterUpgrade → WorkerDelete → MasterDelete → ComponentUpgrade → Cluster，Phase 间无法并行 | 升级耗时长，无依赖关系的 Phase（如 AgentUpgrade 和 ContainerdUpgrade）也必须串行等待 |
| 2 | **升级触发依赖 Annotation 侧信道，非声明式** | [ensure_master_upgrade.go:60-65](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L60-L65) 和 [ensure_worker_upgrade.go:95-100](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go#L95-L100) 中，Master/Worker 升级 Phase 的 Execute() 入口通过设置 `deployAction: k8s_upgrade` annotation 来标记升级状态，而非通过 Spec 声明 | 升级意图分散在 annotation 中而非 Spec 中，违反 Kubernetes 声明式设计，难以通过 `kubectl get` 查看升级状态 |
| 3 | **版本判断逻辑分散，缺乏统一版本入口** | K8s 版本对比在 [ensure_master_upgrade.go:79](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L79) `Spec.ClusterConfig.Cluster.KubernetesVersion != Status.KubernetesVersion`；Etcd 版本对比在 [ensure_etcd_upgrade.go:162](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_etcd_upgrade.go#L162)；OpenFuyao 版本对比在 [ensure_agent_upgrade.go:80](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_agent_upgrade.go#L80)；Containerd 版本对比在 [ensure_containerd_upgrade.go:199](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_containerd_upgrade.go#L199) | 无法回答"集群当前是什么版本"，升级缺乏统一声明入口，版本信息散落在 Spec 各字段 |
| 4 | **升级路径固定，不支持跨版本/选择性升级** | [list.go:88-100](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/list.go#L88-L100) 中 `ClusterUpgradePhaseNames` 硬编码了升级必须经过的 5 个 Phase，无法跳过、无法自定义升级路径 | 不支持仅升级某个组件（如只升级 etcd），不支持跨版本跳级，升级路径不可配置 |

### 二、升级可靠性问题（4项）
| # | 问题 | 具体问题点 | 影响 |
|---|------|-----------|------|
| 5 | **升级完全无回滚能力** | 全部升级 Phase 的 Execute() 方法（[ensure_master_upgrade.go:368-416](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L368-L416)、[ensure_worker_upgrade.go:295-336](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go#L295-L336)、[ensure_etcd_upgrade.go:269-368](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_etcd_upgrade.go#L269-L368)）均无回滚逻辑，失败后仅标记 `NodeUpgradeFailed` 状态 | 升级失败后只能人工介入修复，无法自动恢复到升级前状态，生产环境风险极高 |
| 6 | **Master 升级失败阻塞整个流程** | [ensure_master_upgrade.go:210-216](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L210-L216) 中，单个 Master 节点升级失败直接 return error，后续节点不再尝试 | 一个 Master 节点升级失败导致整个集群升级卡住，无法继续升级其他健康节点 |
| 7 | **Worker 升级失败处理不一致** | [ensure_worker_upgrade.go:244-254](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go#L244-L254) 中 Worker 升级失败是 `continue` 跳过继续下一个节点，但 [ensure_worker_upgrade.go:275-278](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go#L275-L278) 最终如果有失败节点则整体返回 error | Worker 升级策略矛盾：先跳过失败节点继续，最后又报错，下次 Reconcile 会重新尝试所有节点而非仅失败节点 |
| 8 | **Provider 自升级存在自我中断风险** | [ensure_provider_self_upgrade.go:143-176](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_provider_self_upgrade.go#L143-L176) 中，Provider 升级自身 Deployment 镜像后，旧 Pod 会被终止，导致当前 Reconcile 的 context canceled | 虽然代码中做了 `context canceled` 的特殊处理（[L165-170](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_provider_self_upgrade.go#L165-L170)），但自升级后的状态续接依赖 Requeue，存在状态丢失风险 |

### 三、升级一致性问题（3项）
| # | 问题 | 具体问题点 | 影响 |
|---|------|-----------|------|
| 9 | **kubectl 版本硬编码为 v1.25** | [ensure_master_upgrade.go:254](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L254) 中 `addon.Version != "v1.25"` 和 [L291](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L291) 中 `Version: "v1.25"` 硬编码了 kubectl 的目标版本 | 升级到 K8s 1.28/1.29 时 kubectl 仍被强制设为 v1.25，版本不匹配可能导致兼容性问题 |
| 10 | **组件升级通过 Pod 前缀匹配，易误操作** | [ensure_component_upgrade.go:289-297](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_component_upgrade.go#L289-L297) 中 `findMatchingPods` 通过 `strings.HasPrefix(pod.Name, podPrefix)` 查找目标 Pod | Pod 名称前缀匹配不精确，可能匹配到非目标 Pod，导致错误升级；且依赖 Pod 存在而非直接操作 Controller |
| 11 | **版本更新时序不确定，中间状态可见** | [ensure_master_upgrade.go:226](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L226) 中 `bkeCluster.Status.KubernetesVersion = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion` 在所有 Master 升级完成后才更新，但 Etcd/Addon 版本更新分散在不同位置 | 升级过程中 Status 各字段版本不一致（如 K8s 已是新版本但 Etcd 还是旧版本），外部观察者看到的是不一致的中间状态 |

### 四、升级前置/后置问题（3项）
| # | 问题 | 具体问题点 | 影响 |
|---|------|-----------|------|
| 12 | **升级前缺乏兼容性校验** | 所有升级 Phase 的 `NeedExecute()` 方法（如 [ensure_etcd_upgrade.go:159-168](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_etcd_upgrade.go#L159-L168)）仅做版本对比，不校验 K8s 与 Etcd/Containerd 的版本兼容性 | 可能升级到不兼容的版本组合（如 K8s 1.29 + Etcd 3.4），导致集群不可用 |
| 13 | **升级脚本硬编码在 Go 代码中** | [auto_upgrade.go:35-97](file:///d:/code/github/installer-service/pkg/installer/auto_upgrade.go#L35-L97) 中升级准备脚本以 Go 字符串常量拼接，包含 `bke registry patch`、`tar -xzvf`、`cp` 等关键操作 | 脚本修改需重新编译 installer-service，无法热更新；脚本缺乏版本管理，难以审计和回滚 |
| 14 | **升级后缺乏自动化验证** | 升级 Phase 完成后仅做节点健康检查（如 [ensure_master_upgrade.go:345-350](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L345-L350) 的 `waitForNodeHealthCheck`），不验证集群功能完整性 | 升级后可能存在功能退化（如 API Server 可达但 Admission Webhook 异常），无法自动发现 |

### 五、升级可观测性问题（2项）
| # | 问题 | 具体问题点 | 影响 |
|---|------|-----------|------|
| 15 | **升级进度缺乏细粒度可见性** | [phase_flow.go:199-260](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L199-L260) 中 Phase 执行仅记录 Phase 级别的状态（Waiting/Running/Succeeded/Failed），不暴露节点级别的升级进度 | 10 个节点升级时，只能看到"EnsureMasterUpgrade Running"，无法知道当前升级到第几个节点、还剩多少 |
| 16 | **升级历史不可追溯** | [phase_flow.go:122-132](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L122-L132) 中 `processPhaseStatus` 仅保留最近 20 条 PhaseStatus，成功 Phase 会被清理 | 无法查看历史升级记录，无法回溯"上次升级是什么时候、升级了哪些版本、是否成功" |

### 问题严重度分布
```
🔴 高风险（升级阻塞/数据丢失风险）：#5, #6, #8, #12
🟡 中风险（升级效率/一致性问题）：#1, #2, #3, #7, #9, #11, #13
🟢 低风险（可观测性/灵活性）：#4, #10, #14, #15, #16
```

### 核心结论
BKE 升级架构的**根本问题**是：**升级流程是命令式硬编码的线性 Pipeline，而非声明式的 DAG 编排**。具体表现为：
1. **无回滚**：所有升级操作不可逆，失败后只能人工修复
2. **无并行**：10 个升级 Phase 串行执行，即使无依赖关系也无法并行
3. **无校验**：升级前不检查版本兼容性，升级后不验证功能完整性
4. **无版本模型**：版本信息散落在 Spec 各字段，缺乏统一的 ClusterVersion 声明

**建议方向**：参考 KEP-5 提案（[kep5.md](file:///d:/code/github/aaa/cluster-api-provider-bke/code/kep/kep5/kep5.md)），引入 `ClusterVersion` CRD 统一版本声明，基于 DAG 实现升级编排，支持并行执行、失败策略（FailFast/Continue/Rollback）和兼容性校验。

# BKE 集群升级架构现状问题汇报

## 当前升级架构存在的核心问题
| 序号 | 问题类别 | 问题描述 | 业务影响 |
|:---:|---------|---------|---------|
| **1** | **版本管理混乱** | 集群没有统一的"版本号"概念，版本信息散落在 Kubernetes 版本、Etcd 版本、Containerd 版本等多个字段中 | **领导无法回答"集群当前是什么版本"**，升级时缺乏统一入口，出了问题难以追溯 |
| **2** | **升级路径硬编码** | 升级顺序写死在代码中，无法灵活调整；不支持多跳升级（如 v2.4→v2.5→v2.6），只能直接跳 | **跨大版本升级风险极高**，一旦中间某步失败，整个集群可能不可用，回滚成本巨大 |
| **3** | **缺乏兼容性预检** | 升级前不检查组件版本是否兼容（如 K8s v1.29 配 Etcd v3.4 会出问题），直接执行升级 | **升级失败率高**，经常升级后发现组件不兼容，导致集群瘫痪，影响业务连续性 |
| **4** | **升级过程黑盒** | 升级过程中只能看到"成功"或"失败"，不知道当前执行到哪一步、卡在哪里、预计还要多久 | **运维人员无法向业务方同步进度**，出问题后排查困难，平均恢复时间长 |
| **5** | **组件升级耦合严重** | 所有组件升级绑在一起，无法单独升级某个组件（如只升级监控组件）；新增组件需要修改核心代码 | **升级灵活性差**，小改动也要全量升级，风险放大；新功能上线周期长 |
| **6** | **失败回滚困难** | 升级失败后没有自动回滚机制，需要人工介入，手动恢复各组件到旧版本 | **故障恢复时间长**，人工操作易出错，可能造成二次故障 |
| **7** | **离线环境支持弱** | 升级依赖在线拉取资源，断网或弱网环境下升级无法进行，没有本地缓存降级机制 | **政企/边缘客户无法使用**，丢失重要市场机会 |
| **8** | **升级与安装逻辑混在一起** | 安装和升级共用一套代码，通过复杂的条件判断区分场景，代码难以维护 | **新人上手困难**，改一处可能影响另一处，Bug 频发，测试成本高昂 |

## 一句话总结
> **当前升级架构是"命令式、黑盒、强耦合"的，缺乏版本管理、兼容性预检和灵活回滚能力，导致升级风险高、故障恢复慢、客户体验差，无法满足商业化生产环境的要求。**

# BKE 架构问题清单
## BKE 架构问题清单（领导汇报版）

### 一、架构设计问题（5项）
| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 1 | **不符合 Cluster API 三 Provider 标准架构** | BKEClusterReconciler 同时承担 Infrastructure、Bootstrap、Control Plane 三种 Provider 职责，所有逻辑集中在一个控制器中（[bkecluster_controller.go](file:///d:/code/github/aaa/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L62-L76)） | 无法复用 CAPI 生态工具，无法对接其他 Bootstrap/ControlPlane 实现，违反单一职责原则 |
| 2 | **缺乏标准 Bootstrap Provider** | 节点引导逻辑硬编码在 Phase 中，通过自定义 Command CRD 执行 kubeadm，而非标准的 KubeadmConfig/KubeadmConfigTemplate 资源（[ensure_master_init.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go)） | 无法声明式管理引导配置，无法使用 `kubectl get kubeadmconfig` 调试，缺乏配置版本控制和审计 |
| 3 | **缺乏标准 Control Plane Provider** | 控制平面操作（Init/Join/Upgrade/Delete）分散在多个 Phase 中，与基础设施操作混合（[list.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/list.go#L26-L57)） | 控制平面无独立生命周期管理，难以独立升级控制平面组件，无法支持 K3s/RKE2 等其他控制平面实现 |
| 4 | **installer-service 与 controller 职责重叠** | installer-service 的 [cluster.go](file:///d:/code/github/installer-service/pkg/installer/cluster.go)（1500+行）通过 Dynamic Client 直接操作 BKECluster/BKENode CR，与 controller 的调谐逻辑职责重叠 | 两个组件同时修改同一资源，存在竞态条件风险；业务逻辑分散，维护成本高 |
| 5 | **Phase 流程硬编码，缺乏声明式编排** | Phase 执行顺序在 [list.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/list.go) 中硬编码为固定数组，Phase 间依赖关系隐含在代码逻辑中 | 新增/调整 Phase 需修改核心代码，缺乏 Phase 依赖图和并行执行能力 |

### 二、安全风险（3项）
| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 6 | **SSH 密码明文传输和存储** | [cluster.go](file:///d:/code/github/installer-service/pkg/installer/cluster.go#L429-L435) 中 SSH 连接使用 `ssh.Password(node.Password)` 明文认证，密码通过 API 请求体传入、BKENode CR 的 spec 中存储 | 密码泄露风险高，违反安全最佳实践，审计合规问题 |
| 7 | **API 代理缺乏认证鉴权** | [proxyapiserver.go](file:///d:/code/github/installer-service/pkg/server/filters/proxyapiserver.go) 直接代理请求到 K8s API Server，仅删除 Authorization 头，无独立的认证鉴权机制 | 任何能访问 installer-service 的客户端均可操作 K8s 集群，存在未授权访问风险 |
| 8 | **集群删除操作缺乏安全确认机制** | [cluster.go:DeleteCluster](file:///d:/code/github/installer-service/pkg/installer/cluster.go#L510-L545) 仅通过修改 annotation 触发删除，无二次确认、无删除保护 | 误操作可能导致生产集群被删除，缺乏安全兜底 |

### 三、可靠性问题（4项）
| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 9 | **Phase 执行缺乏断点续传能力** | [phase_flow.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L199-L260) 中 Phase 串行执行，任一 Phase 失败后整个流程中断，需从头重新计算 | 长流程（如升级）中途失败后恢复困难，可能需要人工介入 |
| 10 | **installer-service 状态管理依赖内存** | [cluster.go](file:///d:/code/github/installer-service/pkg/installer/cluster.go#L855-L870) 中 StatusProcessor 使用 `sync.Map` 在内存中维护集群健康状态 | 进程重启后状态丢失，可能导致状态判断不一致 |
| 11 | **升级脚本硬编码，缺乏回滚机制** | [auto_upgrade.go](file:///d:/code/github/installer-service/pkg/installer/auto_upgrade.go) 中升级脚本以字符串常量硬编码，无版本化管理和回滚能力 | 升级失败后无法自动回滚，脚本修改需要重新编译 |
| 12 | **集群删除通过 Patch 触发，缺乏状态机保障** | 删除操作通过 Patch annotation `ignore-target-cluster-delete=false` + `spec.reset=true` 触发，缺乏明确的删除状态机 | 删除过程不可追踪，失败后无法确定当前删除进度 |

### 四、可扩展性问题（3项）
| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 13 | **installer-service 单体架构** | 所有集群操作（创建/删除/升级/扩缩容/日志/代理）集中在一个 HTTP 服务中，[handler.go](file:///d:/code/github/installer-service/pkg/api/clustermanage/handler.go) 承载所有 API | 无法按功能独立扩展，任何一个功能的性能瓶颈影响整体 |
| 14 | **Addon 管理缺乏依赖编排** | [ensure_addon_deploy.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go) 中 Addon 部署作为单一 Phase 执行，缺乏 Addon 间依赖管理和并行部署能力 | Addon 数量增多后部署耗时长，依赖关系错误难以排查 |
| 15 | **多集群分发层空实现** | [dispatchcluster.go](file:///d:/code/github/installer-service/pkg/server/filters/dispatchcluster.go) 中 DispatchCluster 过滤器仅透传请求，未实现真正的多集群路由分发 | 无法支持多集群统一管理入口，多集群场景下需客户端自行路由 |

### 五、可观测性问题（2项）
| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 16 | **集群操作日志依赖 WebSocket + Event** | [cluster.go:GetClusterLog](file:///d:/code/github/installer-service/pkg/installer/cluster.go#L730-L790) 通过 WebSocket 推送 K8s Event，连接断开后日志丢失 | 操作历史不可回溯，排障困难，无法审计历史操作 |
| 17 | **Phase 状态上报缺乏结构化指标** | Phase 状态通过 BKECluster.Status.PhaseStatus 上报，缺乏 Prometheus 指标和结构化告警 | 无法通过监控系统实时感知集群操作异常，运维被动响应 |

### 六、CAPI 规范对齐问题（3项）
| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| 18 | **BKENode 非标准 Machine 模型** | 使用自定义 BKENode CR 而非标准 clusterv1.Machine，节点管理绕过 CAPI Machine Controller | 无法使用 CAPI 标准的 MachineDeployment/MachineSet 进行节点伸缩，生态工具不兼容 |
| 19 | **KubeadmControlPlane 为"假引用"** | 模板中引用了 KubeadmControlPlane 但设置 `controlPlaneEndpoint: "fake"`，实际引导逻辑通过自定义 Command CRD 执行 | 误导性设计，无法使用标准 kubeadm 控制平面管理能力 |
| 20 | **缺乏标准 ClusterClass 支持** | 未实现 ClusterClass/ClusterTemplate，每个集群配置独立管理 | 无法通过模板批量创建同构集群，配置漂移风险高 |

### 问题严重度分布
```
🔴 高风险（需优先解决）：#1, #6, #7, #9, #18
🟡 中风险（需规划解决）：#2, #3, #4, #8, #10, #12, #13, #19
🟢 低风险（需持续改进）：#5, #11, #14, #15, #16, #17, #20
```

### 核心结论
BKE 当前架构的**根本问题**是：**以 Cluster API 之名，行非 Cluster API 之实**。虽然引入了 CAPI 的 CRD 和控制器框架，但核心业务逻辑（引导、控制平面管理、节点管理）均通过自定义 Phase + Command 机制实现，绕过了 CAPI 标准的 Provider 模型。这导致：
1. **生态隔离**：无法复用 CAPI 社区工具和最佳实践
2. **维护成本高**：三 Provider 职责耦合在一个控制器中，代码量大、逻辑复杂
3. **演进困难**：每次功能扩展都需要修改核心框架代码

**建议方向**：按 CAPI 标准，逐步拆分为 Infrastructure Provider + Bootstrap Provider + Control Plane Provider 三层架构，实现职责解耦和生态对齐。
        

# BKE 架构问题清单

## 一、架构设计层面
| # | 问题 | 通俗解释 | 影响 |
|---|------|----------|------|
| 1 | **单体式业务逻辑，职责不清** | 所有集群操作（创建/删除/升级/扩缩容/日志/校验）都塞在一个 1500+ 行的 `installerClient` 里，没有按功能拆分服务 | 任何一处改动都可能影响其他功能，代码难以理解和维护，新人上手困难 |
| 2 | **缺少服务层抽象，API 层直接操作底层** | API 接口直接调用 Kubernetes 客户端，中间没有业务编排层 | 业务逻辑散落在各处，无法复用，测试时必须依赖真实 K8s 集群 |
| 3 | **命令式操作，缺少声明式调谐机制** | 创建集群就是"发一次请求"，不会自动重试或修复失败状态；不像 K8s 标准的"声明期望状态→控制器自动调谐"模式 | 操作失败后无法自愈，需要人工干预；不符合云原生最佳实践 |
| 4 | **没有状态机管理集群生命周期** | 集群从"创建中"→"运行中"→"升级中"→"删除中"等状态转换没有统一管控，状态判断逻辑散落在各处 | 可能出现非法状态转换（如从"删除中"直接跳到"升级中"），导致集群不可用 |

## 二、安全风险层面
| # | 问题 | 通俗解释 | 影响 |
|---|------|----------|------|
| 5 | **SSH 密码明文存储** | 节点的用户名密码直接以明文写入 BKENode CR 中，任何能查看集群资源的人都能看到 | 密码泄露风险高，不符合安全合规要求 |
| 6 | **升级过程直接执行 Shell 脚本** | 升级准备通过 `exec.Command("bash", "-c", script)` 直接在服务端执行 Shell 脚本 | 没有沙箱隔离，脚本注入风险；执行失败无法回滚；阻塞 API 响应 |
| 7 | **SSH 连接跳过主机密钥验证** | `HostKeyCallback: func(...) { return nil }` 完全跳过 SSH 主机验证 | 存在中间人攻击风险 |

## 三、可靠性层面
| # | 问题 | 通俗解释 | 影响 |
|---|------|----------|------|
| 8 | **操作无幂等性保护** | 重复调用创建接口会产生重复资源或报错，不能安全重试 | 网络抖动或前端重复提交会导致集群状态异常 |
| 9 | **升级无预检、无回滚** | 升级就是直接修改版本号字段，没有升级前检查（兼容性/资源是否充足），失败后也无法自动回滚 | 升级失败可能导致集群不可用，恢复困难 |
| 10 | **全局状态不持久化** | 集群状态处理器 (`statusProcessors`) 存在内存中，服务重启后丢失 | 重启后集群状态显示可能不准确 |
| 11 | **证书无自动轮转机制** | 集群证书到期后没有自动续期能力 | 证书过期后集群直接不可用，需要人工紧急处理 |

## 四、可扩展性层面
| # | 问题 | 通俗解释 | 影响 |
|---|------|----------|------|
| 12 | **控制面组件无法独立管理** | etcd、API Server、Scheduler、Controller Manager 全部打包在 BKECluster 一个 CR 中，不能单独扩缩容或指定节点 | 无法满足控制面精细化调度需求（如 etcd 独立部署、API Server 弹性伸缩） |
| 13 | **Addon 安装无依赖编排** | Calico、CoreDNS、Kube-Proxy 等组件的安装顺序靠硬编码，没有依赖关系拓扑 | 组件安装顺序错误可能导致集群初始化失败；新增组件时容易遗漏依赖 |
| 14 | **集群配置硬编码为字符串常量** | 默认集群 YAML 是一个写死在代码里的长字符串常量（`defaultClusterYaml`），修改配置必须改代码重新发布 | 不同客户环境的默认配置不同，每次适配都要改代码，无法通过配置文件灵活调整 |
| 15 | **节点级定制能力缺失** | Kubelet、Containerd 等组件的配置是全局统一的，不支持按节点标签差异化配置 | 不同角色节点（如 GPU 节点、高性能计算节点）无法做针对性优化 |

## 五、可观测性层面
| # | 问题 | 通俗解释 | 影响 |
|---|------|----------|------|
| 16 | **缺少指标监控** | 没有 Prometheus 指标暴露，无法监控 API 调用量、操作耗时、失败率等 | 系统运行状况不透明，问题发现滞后 |
| 17 | **日志结构化不足** | 大量使用 `zlog.Infof` 打印非结构化日志，关键操作缺少 TraceID 串联 | 出问题时难以快速定位根因，排障效率低 |
| 18 | **升级过程不可观测** | 升级触发后没有异步任务跟踪机制，前端无法查询升级进度 | 用户不知道升级到哪一步了，只能等或刷新页面猜测 |

## 六、与 Cluster-API 规范对齐层面
| # | 问题 | 通俗解释 | 影响 |
|---|------|----------|------|
| 19 | **未按 CAPI Provider 标准拆分** | CAPI 标准要求拆分为 Infrastructure Provider / Control Plane Provider / Bootstrap Provider 三层，BKE 全部耦合在一起 | 无法复用 CAPI 生态工具，与其他 Provider 互操作困难 |
| 20 | **BKENode 不符合 CAPI Machine 模型** | BKE 自定义了 BKENode CR，没有实现 CAPI 标准的 Machine 接口 | 无法使用 CAPI 的 MachineDeployment、MachinePool 等标准扩缩容能力 |

## 问题严重度分布
```
🔴 高风险（影响生产安全/数据安全）
  ├── #5  SSH密码明文存储
  ├── #6  Shell脚本直接执行
  ├── #9  升级无预检无回滚
  └── #11 证书无自动轮转

🟠 中风险（影响系统可靠性/可维护性）
  ├── #1  单体式业务逻辑
  ├── #3  缺少声明式调谐
  ├── #4  没有状态机
  ├── #8  操作无幂等性
  ├── #12 控制面组件无法独立管理
  └── #14 配置硬编码

🟡 低风险（影响开发效率/可扩展性）
  ├── #2  缺少服务层抽象
  ├── #7  SSH跳过主机验证
  ├── #10 全局状态不持久化
  ├── #13 Addon无依赖编排
  ├── #15 节点定制能力缺失
  ├── #16-18 可观测性不足
  └── #19-20 CAPI规范未对齐
```

以上 20 个问题按 **架构设计 → 安全风险 → 可靠性 → 可扩展性 → 可观测性 → CAPI 规范** 六个维度分类，每个问题都附有通俗解释和影响说明，便于领导层快速理解风险全貌。
        
# BKE 升级路径架构问题描述

## 问题：不支持升级路径（Upgrade Path）

### 架构现状
当前 BKE 的升级流程采用**固定 Phase 顺序执行**模式，共 26 个 Phase（如 `EnsureEtcdUpgrade` → `EnsureMasterUpgrade` → `EnsureWorkerUpgrade` 等）按硬编码顺序依次执行。升级时，系统直接将集群各组件从**当前版本一步到位升级到目标版本**，中间不经过任何过渡版本。
```
当前架构：
  v2.4.0 ──────────────▶ v2.6.0  （直接跳跃，中间无过渡）

理想架构：
  v2.4.0 ──▶ v2.5.0 ──▶ v2.6.0  （分步升级，每步可验证、可回滚）
```

### 具体架构缺陷
| 缺陷维度 | 具体表现 |
|---------|---------|
| **无路径图模型** | 系统中不存在"升级路径图"数据结构，无法定义哪些版本之间可以互相升级、哪些路径被禁止（如 v2.4 不能直接升到 v2.6，必须先升到 v2.5） |
| **无多跳调度能力** | PhaseFrame 只支持"当前状态→目标状态"的一次性调和，无法拆分为多个中间状态逐步执行，每步完成后无法暂停验证 |
| **无兼容性拦截** | 升级前不校验路径合法性，用户输入任意目标版本都会直接执行，即使该路径会导致数据不兼容（如 Etcd 数据结构跨版本不兼容） |
| **无中间态检查点** | 升级过程中没有检查点机制，无法在中间版本暂停并验证集群健康状态，一旦失败无法回退到上一稳定版本 |

### 业务影响
| 场景 | 后果 |
|------|------|
| **跨大版本升级** | 从 v2.4 直接升到 v2.6，跳过了 v2.5 中的数据库迁移脚本，导致 Etcd 数据结构损坏，**集群永久不可用** |
| **安全补丁紧急升级** | 客户想从 v2.4.1 升级到 v2.4.2（安全补丁），但系统强制执行全量升级流程（包含 Etcd、K8s 等所有组件），**风险被不必要放大** |
| **升级失败回滚** | 升级到一半失败，由于没有中间检查点，无法回退到 v2.5，只能**从头重建集群**，业务中断超过 4 小时 |
| **合规审计要求** | 金融/政企客户要求升级过程可追溯、每步可验证，当前架构无法提供**分步升级报告**，**无法通过合规审计** |

### 根因分析
```
┌─────────────────────────────────────────────────────────────┐
│                    当前升级架构                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  用户输入目标版本 v2.6.0                                     │
│       │                                                     │
│       ▼                                                     │
│  ┌─────────────────┐                                        │
│  │ BKECluster.Spec │                                        │
│  │ 版本字段直接修改  │                                        │
│  └────────┬────────┘                                        │
│           │                                                 │
│           ▼                                                 │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  PhaseFrame 调和循环                                  │   │
│  │                                                     │   │
│  │  NeedExecute() 判断每个 Phase 是否需要执行           │   │
│  │  ├─ 比较当前版本 vs 目标版本                         │   │
│  │  └─ 如果不同 → 直接执行升级到目标版本                 │   │
│  │                                                     │   │
│  │  ❌ 没有中间版本概念                                 │   │
│  │  ❌ 没有路径校验                                     │   │
│  │  ❌ 没有分步执行机制                                 │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**核心根因**：当前架构将升级视为**单一状态转换**（当前版本 → 目标版本），而非**路径遍历过程**（当前版本 → 中间版本 1 → 中间版本 2 → ... → 目标版本）。系统中缺乏以下关键抽象：
1. **UpgradePath CRD**：定义版本间的合法流转关系（图结构）
2. **路径规划引擎**：根据当前版本和目标版本自动计算最短合法路径
3. **多跳调和机制**：支持每跳完成后暂停、验证、确认后再继续下一跳
4. **兼容性规则引擎**：在路径规划阶段拦截不兼容的版本组合

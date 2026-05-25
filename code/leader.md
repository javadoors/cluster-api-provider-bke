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
        

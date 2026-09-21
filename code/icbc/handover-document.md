# openFuyao 与工行合作项目 — 离职交接材料

| 字段 | 值 |
|------|-----|
| **项目** | openFuyao 与工商银行合作开发 |
| **交接日期** | 2026-09-21 |
| **交接人** | openFuyao Team |
| **文档位置** | `code/icbc/` 目录 |

---

## 一、项目背景

openFuyao 是基于 Kubernetes 的云原生集群生命周期管理平台。工商银行希望基于 openFuyao 平台，构建符合金融行业要求的版本管理和备份恢复能力。

**合作三大特性**：

| 特性 | 核心内容 | 当前状态 |
|------|---------|---------|
| **声明式升级框架** | 基于 DAG 的组件依赖编排、三层状态机、可观测性、备份/回滚、升级前预检 | KEP 文档已完成，代码开发进行中 |
| **Operator 化（CVO）** | 参考 OpenShift CVO，构建版本管理 Operator，声明式版本收敛 | 架构分析完成，设计方案 provisional |
| **备份与恢复** | etcd 自动备份、配置备份、Velero 集成、双副本存储、加密 | 方案规划完成，待实施 |

---

## 二、文档清单与索引

### 2.1 code/icbc/ 目录

| 文件 | 内容 | 行数 | 用途 |
|------|------|------|------|
| `readme.md` | 合作方案总纲（三大特性详细设计） | 856 | **核心文档**，涵盖升级框架、Operator 化、备份恢复完整方案 |
| `openfuyao-icbc-cooperation-proposal.md` | 合作方案（readme.md 的早期版本） | 789 | 历史版本，内容与 readme.md 基本一致 |
| `meetup-minutes.md` | 线下 Meetup 会议纪要 | 676 | **核心文档**，含行动项清单、里程碑、5个技术议题详细设计要点 |
| `kep-docs-discussion.md` | KEP 文档讨论材料 | 1384 | **核心文档**，含框架总览、代码库架构、5个议题设计要点（最详细） |
| `k8s-upgrade-1.34-to-1.36.md` | K8s 1.34→1.36 升级方案 (KEP-8) | 895 | K8s 跨版本升级技术方案 |
| `olm.md` | OLM 参考链接 | 3 | 仅链接，无实质内容 |

### 2.2 关联 KEP 文档（code/kep/ 目录）

| KEP | 文件 | 议题 | 状态 |
|-----|------|------|------|
| KEP-5 | `kep5/kep5.md` | 声明式升级框架 | 已完成 |
| KEP-5-2 | `kep5/kep5-2-precheck-postcheck-design-v2.md` | 升级前预检/后检 | 已完成 |
| KEP-6 | `kep6/kep6-state-machine-v4.md` | 三层状态机设计 v4 | 已完成 |
| KEP-6 | `kep6/声明式集群版本升级方案-支持二进制与Helm组件.md` | Binary/Helm 组件 | 已完成 |
| KEP-6 | `kep6/声明式集群版本回滚方案设计.md` | 集群回滚 | 已完成 |
| KEP-7 | `kep7/olm-architecture-analysis.md` | OLM 架构分析 | informational |
| KEP-7 | `kep7/cvo-architecture-analysis.md` | CVO 架构分析 | informational |
| KEP-7 | `kep7/kep-bke-cvo-design.md` | BKE CVO 设计 | provisional |
| KEP-7 | `kep7/kep21-operator-framework-design.md` | Operator Framework 设计 | provisional |
| KEP-8 | `icbc/k8s-upgrade-1.34-to-1.36.md` | K8s 跨版本升级 | provisional |
| KEP-10 | `kep6/kep10-install-components-declarative-design.md` | 安装 DAG 化设计 | provisional |
| KEP-10 | `kep7/kep10-install-dag-with-phaseflow-fallback.md` | 安装 DAG（PhaseFlow 兜底） | provisional |
| KEP-16 | `kep6/kep16-binary-component-design.md` | Binary 组件设计 | provisional |
| KEP-18 | `kep6/kep18-component-condition-filter-design.md` | Condition 条件过滤 | provisional |
| KEP-19 | `kep6/kep19-component-node-filter-design.md` | 节点过滤 | provisional |

---

## 三、技术架构总览

### 3.1 声明式升级框架

```
用户设置 ClusterVersion.Spec.DesiredVersion = "v2.7.0"
  │
  ▼
ClusterVersionReconciler
  ├─ 解析 desiredVersion → 查找 ReleaseImage CR
  ├─ ReleaseImageEnsurer.Ensure() → 拉取 OCI Bundle
  ├─ 等待 ReleaseImage Status.Phase = Valid
  ├─ 解析 UpgradePath → 确定升级路径
  └─ 设置 upgrade-ready annotation → 触发 BKEClusterReconciler
  │
  ▼
BKEClusterReconciler (executePhaseFlow)
  ├─ shouldUseDeclarativeUpgrade? → executeUpgradeDAG (DAG 路径)
  │    ├─ BuildDAGFromBundle → 拓扑排序
  │    ├─ Scheduler.ExecuteDAG → 并行执行
  │    │    ├─ InlineComponentExecutor → Phase handler (etcd/apiserver/master 升级)
  │    │    ├─ YamlComponentExecutor → kubectl apply (coredns/kube-proxy)
  │    │    ├─ HelmComponentExecutor → helm upgrade (Feature Gate)
  │    │    └─ BinaryComponentExecutor → SSH 逐节点 (containerd/bkeagent)
  │    └─ DeclarativeUpgradeStatus → 断点续传
  └─ 默认 → PhaseFlow (Legacy 路径, 兜底)
```

### 3.2 三层状态机

| 层级 | 驱动者 | 状态 | 职责 |
|------|--------|------|------|
| **L1 集群层** | BKEClusterReconciler | Pending→Installing→Running→Upgrading→RollingBack→Failed | DAG 执行、集群级组件、状态聚合 |
| **L2 节点层** | BKEMachineReconciler | Pending→Provisioning→Ready→Upgrading→Deleting→Failed | 节点生命周期、组件排序、状态聚合 |
| **L3 组件层** | ComponentExecutor | Pending→Installing→Installed→Upgrading→Deleting→Failed | 具体执行（inline/binary/helm/yaml） |

### 3.3 核心组件类型

| 类型 | 执行方式 | 适用场景 | 示例 |
|------|---------|---------|------|
| **inline** | Go handler 直接执行 | 复杂升级逻辑 | etcd、Master、Worker 升级 |
| **binary** | SSH 逐节点执行 | 二进制升级 | containerd、bkeagent |
| **helm** | Helm SDK | Helm Chart | 监控、日志组件 |
| **yaml** | kubectl apply | K8s 资源 | coredns、kube-proxy |

### 3.4 组件版本管理

```
ReleaseImage (OCI 镜像)
  ├── release.yaml           → ReleaseImage CR (版本声明)
  ├── components/            → ComponentVersion CRs (组件元数据)
  │   ├── containerd-v1.7.18/component.yaml
  │   ├── kubelet-v1.29.0/component.yaml
  │   └── ...
  ├── manifests/             → YAML 清单
  └── files/                 → 二进制制品
```

### 3.5 Cluster API 集成

| BKE 资源 | CAPI 对应 | 关系 |
|---------|----------|------|
| BKECluster | Cluster (infrastructure) | BKECluster 是 CAPI Cluster 的基础设施实现 |
| BKEMachine | Machine (infrastructure) | BKEMachine 是 CAPI Machine 的基础设施实现 |
| BKENode | — | BKE 独有，节点配置和管理 |

---

## 四、行动项清单与状态

### 4.1 Meetup 行动项

| 编号 | 行动项 | 责任方 | 截止时间 | 交付物 | 状态 |
|------|--------|--------|---------|--------|------|
| A1 | 平台整体规划文档交付 | openFuyao | 8月中旬 | 规划文档包 | ✅ 已完成 |
| A2 | 节点级运维能力方案调整 | 工行 | 8月底 | 调整后方案文档 | 进行中 |
| A3 | 声明式升级框架方案讨论会 | 双方 | 8月底 | 会议纪要+方案确认 | 待排期 |
| A4 | 5个议题 KEP 文档准备 | openFuyao | 8月底 | 5个议题 KEP | ✅ 已完成 |
| A5 | Operator 化方案讨论材料 | openFuyao | 9月底 | 架构设计+组件清单 | ✅ 已完成 |
| A6 | Operator 化方案讨论会 | 双方 | 9月底 | 会议纪要+方案确认 | 待排期 |
| A7 | 安全专项会议 | 双方 | 9月中旬 | 安全需求+方案 | 待排期 |
| A8 | 升级兼容性扫描工具 | openFuyao | 9月底 | 工具原型 | 待启动 |
| A9 | 验证环境提供 | openFuyao | 8月中旬 | 环境地址+说明 | ✅ 已完成 |

### 4.2 五个技术议题状态

| 议题 | 对应 KEP/文档 | 设计状态 | 代码状态 |
|------|-------------|---------|---------|
| **Binary 组件** | `声明式集群版本升级方案-支持二进制与Helm组件.md` + KEP-16 | ✅ 设计完成 | 🔨 开发中 |
| **三层状态机** | `kep6-state-machine-v4.md` | ✅ 设计完成 | 🔨 开发中 |
| **可观测性** | `kep6-state-machine-v4.md` §5 | ✅ 设计完成 | 🔨 开发中 |
| **备份/回滚** | `声明式集群版本回滚方案设计.md` + KEP-11 | ✅ 设计完成 | 待开发 |
| **升级前预检** | `kep5-2-precheck-postcheck-design-v2.md` | ✅ 设计完成 | 待开发 |

---

## 五、工行合作要点

### 5.1 工行核心诉求

1. **企业级声明式升级**：自动化、可观测、可回滚的版本管理
2. **Operator 化**：参考 OpenShift CVO，声明式版本收敛
3. **备份恢复**：金融级数据安全，etcd/配置/应用数据备份
4. **安全合规**：制品签名、传输加密、凭证管理、操作审计

### 5.2 工行已认领任务

- **节点级运维能力模块**：工行正在开发，需结合声明式框架调整方案
- **二进制组件安装方案迁移**：从旧框架迁移到 `binary` 类型 ComponentVersion

### 5.3 工行需要 openFuyao 提供的支持

| 支持项 | 说明 | 对应文档 |
|--------|------|---------|
| Binary ComponentVersion 设计文档 | 二进制组件声明式定义 | KEP-16 |
| 三层状态机设计文档 | 集群/节点/组件三层状态模型 | KEP-6 v4 |
| DAG 执行器接口说明 | Scheduler/Executor 接口 | KEP-10 §6-7 |
| 验证环境 | 可直接使用的 K8s 集群 | 已提供 |

### 5.4 里程碑时间线

```
8月中旬 ──── 8月底 ──── 9月中旬 ──── 9月底
   │            │            │            │
   ├─ A1: 规划文档交付 ✅    │            │
   ├─ A9: 验证环境就绪 ✅    │            │
   │            │            │            │
   │         ├─ A3: 升级框架讨论会    │
   │         ├─ A4: 5个议题KEP交付 ✅ │
   │         ├─ A2: 节点级方案调整    │
   │         │            │            │
   │         │         ├─ A7: 安全专项 │
   │         │            │            │
   │         │            │         ├─ A5: Operator化材料 ✅
   │         │            │         ├─ A6: Operator化讨论会
   │         │            │         ├─ A8: 兼容性扫描工具
```

---

## 六、关键技术设计要点

### 6.1 Binary 组件设计（KEP-16）

```
ComponentVersion (type=binary)
  ├─ artifacts: 制品下载 (HTTP/本地/OCI, checksum 校验)
  ├─ configTemplates: 配置渲染 (Go template, 50+ 变量)
  ├─ installScript: 安装脚本
  ├─ upgradeStrategy: Rolling/Parallel/Batch
  ├─ nodeFilter: 节点过滤 (Roles/MatchLabels)
  └─ healthCheck: 健康检查
```

**关键设计**：
- BinaryComponentExecutor 通过 SSH 逐节点执行
- 支持 Rolling（逐节点）/ Parallel（全并行）/ Batch（分批）三种策略
- per-node per-component 幂等（NodeComponentStatuses 追踪）
- 三种配置渲染模式：Content（Go template）/ Secret（K8s Secret 引用）/ Kubeconfig（动态生成）

### 6.2 三层状态机设计（KEP-6 v4）

**统一 DAG 设计**（v4 最新）：
- 安装/升级/回滚共用 `buildDAG` + `prepareVersionContext`
- DAG 拓扑与操作类型解耦，操作语义由 VersionContext 承载
- 三层机制：Scheduler 组件级 Decision → PhaseRunner NeedExecute 前置守卫 → handler filterNodes 精确过滤

**节点级过滤**：
- 通过 `BKENode.Status.StateCode` 位标记过滤已有/新增节点
- 已有节点位标记已设置 → 跳过
- 新增节点位标记未设置 → 执行安装

### 6.3 回滚设计（KEP-11）

**三层回滚**：
- 组件级回滚：单组件异常，FailurePolicy=Rollback 触发
- 节点级回滚：单节点升级失败，回滚该节点所有组件
- 集群级回滚：集群整体异常，DesiredVersion 回退

**各类型回滚策略**：
| 类型 | 回滚方式 |
|------|---------|
| Binary | Uninstall(新) + Install(旧) |
| Helm | helm rollback |
| YAML | Apply(旧版本清单) + Prune |
| Inline | 旧版本 Phase.Execute() |

### 6.4 升级前预检设计（KEP-5-2）

**CheckPolicy CRD**：
- 独立于 UpgradePath，按集群标签/环境定制检查策略
- 检查项通过注册机制接入，支持动态扩展
- DAG 执行检查项，支持并行和依赖

**预检项**：集群健康、备份验证、资源检查、API 废弃检查、CRD 兼容性、依赖检查

### 6.5 Operator 化设计（KEP-7 / KEP-21）

**两种方案**：
| 方案 | 文档 | 特点 |
|------|------|------|
| CVO 方案 | `kep-bke-cvo-design.md` | 集中式版本协调器，ClusterComponent CRD 状态门控 |
| Operator Framework 方案 | `kep21-operator-framework-design.md` | 每个组件独立 Operator，ComponentOperator CRD，不引入 OLM 二进制 |

**BKE Operator 优先级**：
| 优先级 | Operator |
|--------|---------|
| P0 | CVO、API Server、Controller Manager、Scheduler、etcd、Network、DNS |
| P1 | Storage、Backup |
| P2 | Monitoring、Ingress |

### 6.6 备份恢复设计

| 备份类型 | 频率 | 保留 | 存储 |
|---------|------|------|------|
| etcd 自动备份 | 每日凌晨 2 点 | 7 天 | 本地 + 远程 |
| 手动备份 | 升级前触发 | 30 天 | 本地 + 远程 |
| 配置备份 | 随 etcd 备份 | 同上 | 同上 |
| 应用数据 | CronJob 定时 | 可配置 | Velero + S3/NFS |

**加密**：AES-256-CBC，密钥每月轮转

---

## 七、代码库关键路径

### 7.1 控制器

| 控制器 | 文件 | 职责 |
|--------|------|------|
| BKEClusterReconciler | `controllers/capbke/bkecluster_controller.go` | 集群生命周期、DAG 升级/安装 |
| BKEMachineReconciler | `controllers/capbke/bkemachine_controller.go` | 节点生命周期 |
| ClusterVersionReconciler | `controllers/clusterversion/clusterversion_controller.go` | ReleaseImage 拉取/验证、升级路径 |
| ReleaseImageReconciler | `controllers/releaseimage/` | OCI 签名验证、组件解析 |
| CommandReconciler | `controllers/bkeagent/command_controller.go` | 节点级命令执行 (bkeagent 内) |

### 7.2 核心包

| 包 | 路径 | 职责 |
|---|------|------|
| DAG 调度器 | `pkg/dagexec/` | Scheduler、ExecutorRegistry、各 Executor |
| DAG 构建 | `pkg/topology/` | BuildDAG、ComponentNode、Graph |
| 版本管理 | `pkg/upgrade/` | VersionContext、Bundle 解析、依赖解析 |
| 组件工厂 | `pkg/componentfactory/` | Phase handler 注册和解析 |
| Phase 框架 | `pkg/phaseframe/` | PhaseFlow、Phase 列表、Phase 执行 |
| bkeagent 插件 | `pkg/job/builtin/` | containerd/kubelet/certs/env/kubeadm 等插件 |
| ReleaseImage | `pkg/release/` | OCI 拉取、Bundle 解析、三级缓存 |

### 7.3 API 定义

| API Group | 包 | CRD |
|-----------|---|-----|
| `bke.bocloud.com` | `api/capbke/v1beta1` | BKECluster、BKEMachine |
| `bkecommon.bocloud.com` | `api/bkecommon/v1beta1` | 共享类型 (BKEClusterSpec/Status) |
| `bkeagent.bocloud.com` | `api/bkeagent/v1beta1` | Command |
| `config.openfuyao.com` | `api/v1alpha1` | ClusterVersion、ReleaseImage、ComponentVersion、UpgradePath |

---

## 八、未完成事项与待办

### 8.1 待启动

| 事项 | 优先级 | 说明 |
|------|--------|------|
| A3: 声明式升级框架方案讨论会 | 高 | 需与工行排期，5 个议题已准备完毕 |
| A6: Operator 化方案讨论会 | 高 | 需与工行排期，材料已准备完毕 |
| A7: 安全专项会议 | 高 | 工行核心诉求，需深入讨论安全需求 |
| A8: 升级兼容性扫描工具 | 中 | 设计+开发，输入源/目标版本，输出兼容性报告 |

### 8.2 开发中

| 事项 | 进展 | 说明 |
|------|------|------|
| KEP-10 安装 DAG 化 | 设计完成 | PhaseFlow → DAG 安装路径迁移 |
| KEP-16 Binary 组件 | 设计完成 | BinaryComponentExecutor 实现 |
| KEP-18 Condition 过滤 | 设计完成 | ComponentVersion.Spec.Condition |
| KEP-19 节点过滤 | 设计完成 | ComponentVersion.Spec.NodeFilter |
| KEP-6 v4 统一 DAG | 设计完成 | buildDAG + prepareVersionContext 统一 |

### 8.3 工行侧待跟进

| 事项 | 说明 |
|------|------|
| A2: 节点级运维方案调整 | 工行需将现有方案与声明式框架对齐 |
| 二进制组件迁移 | 从旧框架迁移到 binary 类型 ComponentVersion |
| 安全需求清单 | 工行需提供金融行业安全合规具体需求 |

---

## 九、沟通机制

| 机制 | 频率 | 参与方 | 说明 |
|------|------|--------|------|
| 双周同步会 | 每两周 | openFuyao + 工行 | 同步行动项进展，协调阻塞问题 |
| 专题讨论会 | 按需 | 双方 | 针对具体技术方案深入讨论 |
| 文档评审 | 按里程碑 | 双方 | KEP 文档交付后组织评审 |

---

## 十、交接确认

| 项目 | 状态 | 备注 |
|------|------|------|
| 合作方案文档 | ✅ 已交接 | `code/icbc/readme.md` + `meetup-minutes.md` |
| KEP 文档清单 | ✅ 已交接 | 见第二节文档清单 |
| 代码库关键路径 | ✅ 已交接 | 见第七节 |
| 行动项清单 | ✅ 已交接 | 见第四节 |
| 未完成事项 | ✅ 已交接 | 见第八节 |
| 沟通机制 | ✅ 已交接 | 见第九节 |

---

**交接人签字**：________________

**接收人签字**：________________

**日期**：________________

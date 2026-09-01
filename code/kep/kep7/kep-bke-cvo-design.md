# KEP-7: BKE CVO 设计 — 借鉴 OpenShift CVO 的集群版本协调器

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-7 |
| **标题** | BKE CVO 设计 — 借鉴 OpenShift CVO 的集群版本协调器 |
| **状态** | `provisional` |
| **类型** | Feature Design |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-01 |
| **依赖** | KEP-5 (ClusterVersion/ReleaseImage/UpgradePath)、KEP-6 (ComponentVersion/DAG)、KEP-7 OLM/CVO 架构分析 |
| **参考** | OpenShift CVO (`openshift/cluster-version-operator`)、OpenShift Enhancements 设计文档 |

## 1. 摘要

本提案基于 BKE 现有代码架构，借鉴 OpenShift CVO 的核心设计思路，提出 BKE 平台的集群版本协调器 (BKE CVO) 设计。BKE 已具备 ClusterVersion/ReleaseImage/UpgradePath/ComponentVersion CRD 体系、DAG 拓扑排序调度器、DeclarativeUpgradeStatus 断点续传、VersionContext 版本决策等基础能力，但存在以下架构差距：(1) 版本协调逻辑散落在 BKEClusterReconciler 中，未形成独立控制器；(2) 安装仍使用硬编码 PhaseFlow，升级才走 DAG；(3) 缺少 ClusterOperator 式的组件状态门控机制；(4) 缺少前置条件检查、风险聚合、签名验证等安全机制。本设计将 BKE CVO 塑造为**独立的集群级版本协调器**，引入 ClusterComponent CRD 作为组件状态门控、Manifest Graph 分层 DAG 替代 PhaseFlow、Precondition 前置检查链、Risk 风险聚合树、Release Image 签名验证，同时保持与现有 DAG 调度器、DeclarativeUpgradeStatus、VersionContext 的兼容性。目标是将安装和升级统一到声明式 DAG 路径，消除 PhaseFlow 的 `handlePanic`，实现 forward-only 升级哲学和 N-1 兼容性要求。

---

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状 | 影响 |
|------|------|------|
| **版本协调逻辑耦合** | ClusterVersion/ReleaseImage/UpgradePath 协调逻辑散落在 BKEClusterReconciler + ClusterVersionReconciler 中 | 职责不清，难以独立演进和测试 |
| **安装升级双轨** | 安装走 PhaseFlow (硬编码 Phase 列表)，升级走 DAG (ReleaseImage bundle) | 代码重复，行为不一致，新增组件需改两处 |
| **无组件状态门控** | DAG 执行后仅写 ClusterComponentStatuses，无独立 CR 可供外部观测 | 无法像 OpenShift ClusterOperator 那样被 must-gather/监控消费 |
| **PhaseFlow 恐慌恢复** | `PhaseFlow.Execute()` 使用 `defer handlePanic()` | Goal 3 要求移除 panic/recover，改用错误分类 |
| **无前置条件检查** | 升级前无 Rollback/GiantHop/Upgradeable 等检查 | 可能执行不安全的升级路径 |
| **无签名验证** | ReleaseImage 仅有 `VerifySignature` 字段但未实现验证逻辑 | Release Image 可能被篡改 |
| **无风险聚合** | Upgradeable 条件散落，无统一风险源聚合 | 难以全面评估升级安全性 |
| **无 OSUS 式升级推荐** | UpgradePath 仅静态图，无 Cincinnati 式动态推荐 | 无法根据集群状态推荐最优升级路径 |
| **状态机不正式** | ClusterStatus 字符串 + PhaseStatus[] 是隐式状态机 | Goal 4 要求正式 State 接口 (Enter/Execute/Exit/CanTransitionTo) |

### 2.2 目标

1. 将 BKE CVO 重构为**独立的集群级版本协调器**，与 BKEClusterReconciler 解耦。
2. 引入 `ClusterComponent` CRD 作为组件状态报告机制 (借鉴 OpenShift ClusterOperator)。
3. 引入 **Manifest Graph 分层 DAG**，按 Run Level 分层阻塞，替代 PhaseFlow 的硬编码顺序。
4. **统一安装和升级**为声明式 DAG 路径 (KEP-10 的目标)，消除安装/升级双轨。
5. 引入 **Precondition 前置检查链** (Rollback/GiantHop/Upgradeable/RecommendedUpdate)。
6. 引入 **Risk 风险聚合树**，多风险源聚合为 Upgradeable 条件。
7. 引入 **Release Image 签名验证**，支持多签名存储。
8. 保持与现有 DAG 调度器、DeclarativeUpgradeStatus、VersionContext 的**向后兼容**。
9. 移除 PhaseFlow 的 `handlePanic`，改用 ReconcileError 错误分类。
10. 实现 **forward-only 升级哲学**和 N-1 兼容性要求。

### 2.3 非目标

1. 不在此提案中实现 Helm/Binary 组件执行器 (KEP-6 范围)。
2. 不引入 Cincinnati/OSUS 式动态升级推荐服务 (后续提案)。
3. 不修改 BKECluster/BKENode CRD 的 spec 定义 (仅扩展 status)。
4. 不实现备份恢复能力 (Goal 5 范围)。
5. 不实现多集群管理能力 (MCM 范围)。

---

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| BKECVO 控制器 | 新增独立控制器，接管 ClusterVersion 协调 + DAG 执行 |
| ClusterComponent CRD | 新增组件状态报告 CRD (借鉴 OpenShift ClusterOperator) |
| Manifest Graph | 新增分层 DAG (按 Run Level 分层阻塞)，替代 PhaseFlow 硬编码顺序 |
| Precondition 链 | 新增前置条件检查 (Rollback/GiantHop/Upgradeable/RecommendedUpdate) |
| Risk 聚合树 | 新增风险聚合 (ClusterComponent Upgradeable + AdminAck + Alert) |
| 签名验证 | 实现 ReleaseImage 签名验证 (cosign 或自定义) |
| Install DAG | 统一安装路径到 DAG (KEP-10 DeclarativeInstallCatalog) |
| 状态机 | 正式 ClusterVersionPhase 状态机 (State 接口) |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 必须支持 PhaseFlow 与 DAG 双轨运行，Feature Gate 控制 |
| **离线环境** | Release Image 支持本地缓存，断网安装/升级 |
| **多架构** | 支持 amd64 和 arm64 |
| **N-1 兼容** | 组件必须支持与上一版本共存 |
| **Forward-Only** | 不支持自动回滚，升级仅向前 |
| **断点续传** | DeclarativeUpgradeStatus 保持现有语义，跨重启恢复 |

---

## 4. CVO 设计借鉴映射

| OpenShift CVO 设计 | BKE 借鉴方式 | BKE 对应实现 |
|---------------------|-------------|-------------|
| Release Payload Image (清单包) | 直接借鉴 | BKE ReleaseImage OCI Bundle (已有) |
| ClusterVersion CR (声明式期望) | 直接借鉴 | BKE ClusterVersion CR (已有，扩展) |
| ClusterOperator CR (状态门控) | 直接借鉴 | **新增 ClusterComponent CRD** |
| Manifest Graph (Run Level 分层) | 适配借鉴 | **新增分层 DAG (按 component.yaml runLevel)** |
| ClusterOperator Health Check | 适配借鉴 | **ClusterComponent Health Check (阻塞)** |
| Precondition 链 | 直接借鉴 | **新增 Precondition 接口** |
| Risk 聚合树 | 适配借鉴 | **新增 Risk Source 聚合** |
| 签名验证 | 直接借鉴 | **新增 Release Image 签名验证** |
| SyncWorker 状态机 | 适配借鉴 | **扩展 Scheduler 状态机** |
| Resource Builder (GVK 分发) | 已有对应 | BKE ExecutorRegistry (已有) |
| Forward-Only | 直接借鉴 | **升级仅向前，N-1 兼容** |
| Pod-based 抽包 | 不适用 | BKE 使用 OCI Registry 直接拉取 (已有) |
| OSUS 升级推荐 | 后续提案 | BKE UpgradePath 静态图 (已有) |
| Leader Election | 直接借鉴 | controller-runtime Manager 已有 |
| 加权历史剪枝 | 适配借鉴 | **扩展 ClusterVersion.UpgradeHistory** |

---

## 5. 整体架构

### 5.1 架构全景图

```txt
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                        BKE CVO 整体架构                                              │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                     Release Image OCI Bundle                                  │   │
│  │  (如 registry.openfuyao.cn/bke/release:v2.7.0)                               │   │
│  │                                                                               │   │
│  │  ├── release.yaml                 (ReleaseImage CR + 版本元数据)              │   │
│  │  ├── components/                  (ComponentVersion CRs)                      │   │
│  │  │   ├── containerd/v1.7.18/component.yaml  (type: binary, runLevel: 30)     │   │
│  │  │   ├── bkeagent/v2.7.0/component.yaml      (type: binary, runLevel: 05)    │   │
│  │  │   ├── kubernetes-master/v1.29.0/component.yaml (type: inline, runLevel: 10)│  │
│  │  │   ├── coredns/v1.11.1/component.yaml      (type: yaml, runLevel: 50)      │   │
│  │  │   ├── openfuyao-core/v26.03/component.yaml (type: yaml, runLevel: 60)     │   │
│  │  │   └── ...                                                                  │   │
│  │  ├── manifests/                   (YAML 清单文件)                             │   │
│  │  │   ├── kubernetes/coredns/v1.11.1/coredns.yaml                            │   │
│  │  │   └── ...                                                                  │   │
│  │  └── image-references            (镜像引用列表)                               │   │
│  └────────────────────────────────────┬─────────────────────────────────────────┘   │
│                                       │ 解析/验证                                  │
│                                       ▼                                           │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                        BKE CVO 控制器                                         │   │
│  │              namespace: bke-system                                            │   │
│  │              Deployment: bke-cvo-controller (1 replica)                       │   │
│  │              部署于管理集群 (管理集群侧运行)                                  │   │
│  │                                                                               │   │
│  │  核心组件:                                                                    │   │
│  │  ├── ClusterVersionReconciler    — 主协调循环 (监听 ClusterVersion)            │   │
│  │  ├── SyncWorker                   — 同步工作器 (状态机 + apply)                │   │
│  │  ├── ManifestGraph               — 分层 DAG (Run Level 分层阻塞)              │   │
│  │  ├── Scheduler                   — DAG 执行器 (已有，扩展)                     │   │
│  │  ├── ExecutorRegistry            — 组件执行器注册表 (已有)                     │   │
│  │  │   ├── InlineExecutor           — Inline 组件执行 (已有)                    │   │
│  │  │   ├── YamlExecutor              — YAML 组件执行 (已有)                     │   │
│  │  │   ├── HelmExecutor              — Helm 组件执行 (KEP-6)                    │   │
│  │  │   └── BinaryExecutor            — Binary 组件执行 (KEP-6)                 │   │
│  │  ├── ReleaseStore                — Release Image 缓存 (已有，扩展签名验证)   │   │
│  │  ├── PreconditionChain           — 前置条件检查链 (新增)                      │   │
│  │  ├── RiskAggregator               — 风险聚合树 (新增)                         │   │
│  │  └── StatusReporter               — ClusterVersion/ClusterComponent 状态合成   │   │
│  │                                                                               │   │
│  │  两个工作队列:                                                                │   │
│  │  ├── clusterVersionQueue          — 主同步队列                                │   │
│  │  └── availableUpdatesQueue        — 可用更新队列 (UpgradePath 轮询)            │   │
│  │                                                                               │   │
│  │  Feature Gate: DeclarativeInstallEnabled (安装走 DAG)                        │   │
│  └──────────────────────────────────┬──────────────────────────────────────────┘   │
│                                     │ 协调 (reconcile)                            │
│                                     ▼                                               │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │              Cluster Components (由 BKE CVO 管理)                              │   │
│  │                                                                               │   │
│  │  Run Level 05: bkeagent, provider                                            │   │
│  │  Run Level 10-29: kubernetes-master, kubernetes-worker, etcd                 │   │
│  │  Run Level 30: containerd                                                    │   │
│  │  Run Level 50: coredns, kube-proxy                                          │   │
│  │  Run Level 60: openfuyao-core, calico, cert-manager                         │   │
│  │  Run Level 70: 节点级组件                                                    │   │
│  │                                                                               │   │
│  │  每个组件通过 ClusterComponent CR 报告状态:                                   │   │
│  │  ┌──────────────────────────────────────────────────────────────────────┐    │   │
│  │  │ apiVersion: cvo.openfuyao.cn/v1alpha1                              │    │   │
│  │  │ kind: ClusterComponent                                                │    │   │
│  │  │ metadata:                                                             │    │   │
│  │  │   name: coredns                                                      │    │   │
│  │  │   ownerReference: BKECluster                                         │    │   │
│  │  │ spec: {}                                                             │    │   │
│  │  │ status:                                                              │    │   │
│  │  │   conditions:                                                        │    │   │
│  │  │     - type: Available, status: "True"                               │    │   │
│  │  │     - type: Progressing, status: "False"                            │    │   │
│  │  │     - type: Degraded, status: "False"                               │    │   │
│  │  │     - type: Upgradeable, status: "True"                             │    │   │
│  │  │   versions:                                                          │    │   │
│  │  │     - name: component, version: "v1.11.1"                           │    │   │
│  │  │   phase: Installed                                                   │    │   │
│  │  └──────────────────────────────────────────────────────────────────────┘    │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 控制器职责重新划分

| 控制器 | 当前职责 | CVO 设计后职责 | 变化 |
|--------|---------|----------------|------|
| **BKEClusterReconciler** | 全部协调 (安装/升级/扩缩/删除) + PhaseFlow + DAG | 集群生命周期管理 (创建/删除 BKECluster)、节点管理、状态汇总 | **精简**：移交版本协调给 CVO |
| **ClusterVersionReconciler** | ReleaseImage 确保、UpgradePath 校验、设置 upgrade-ready 注解 | **升级为 BKECVO 主控制器**：接管 DAG 执行、Manifest Graph、Precondition、Risk | **扩展**：从注解协调升级为完整版本协调器 |
| **ReleaseImageReconciler** | OCI 签名验证、组件解析、兼容性校验 | 保持 + 扩展签名验证逻辑 | **微调** |
| **UpgradePathReconciler** | 路径图构建、digest 监控 | 保持 + 可选 OSUS 式推荐 | **微调** |

---

## 6. ClusterComponent CRD 设计

### 6.1 设计思路

借鉴 OpenShift ClusterOperator CRD，为 BKE 每个受管组件创建独立的 `ClusterComponent` CR。CVO 监听其状态作为升级门控，实现"不创建组件、仅监听状态"的阻塞模式。

#### 6.1.0 "CVO 监听其状态作为升级门控"详解

这句话的含义是：**CVO 不亲自安装/升级组件，也不判断组件是否健康 — 这些都是 ComponentExecutor 的职责。CVO 在执行器完成后，仅"看" ClusterComponent.status 上的四个条件 (Available/Progressing/Degraded/versions)，全部满足才放行进入下一 Run Level，否则阻塞等待。**

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              "CVO 监听状态作为升级门控"的含义                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  组件执行器完成工作:                                                             │
│  执行器 (inline/yaml/helm/binary) 安装/升级组件后                                │
│  → 写入 ClusterComponent.status:                                                │
│      Available=True, versions=新版本, Degraded=False, Progressing=False         │
│                                                                                 │
│                          ↓ CVO 监听到 status 变更                                │
│                                                                                 │
│  CVO 门控检查 (四个条件全部满足才放行):                                          │
│  ┌──────────────────────────────────────────────────────────────────┐          │
│  │ ✓ Available == True?       组件可用                               │          │
│  │ ✓ versions 匹配期望版本?    版本已更新完成                        │          │
│  │ ✓ Degraded == False?       未降级                                │          │
│  │ ✓ Progressing == False?     不在进行中                            │          │
│  └──────────────────────────────────────────────────────────────────┘          │
│                          ↓                                                     │
│                                                                                 │
│  全部满足 → 放行，进入下一 Run Level                                            │
│  任一不满足 → 阻塞当前 Run Level                                                │
│    ├── 30 分钟内: 抑制失败 (报告 "waiting on X")                                │
│    └── 40 分钟后: 超时失败 (Failing=True)                                      │
│                                                                                 │
│  为什么这样设计 (解耦):                                                         │
│  CVO 不需要知道每个组件"怎么算健康":                                            │
│    • coredns: 看 Pod Ready (执行器判断)                                        │
│    • containerd: 看 systemd active (执行器判断)                                │
│    • kubernetes: 看 API 可用 (执行器判断)                                      │
│  这些判断逻辑由组件执行器自行实现并写入 status                                  │
│  CVO 只需要读标准化的 status 条件 → CVO 与组件完全解耦                          │
│  新增组件时 CVO 零代码改动 (只需新的 ComponentVersion + 执行器)                │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

| 维度 | CVO 的职责 | 组件执行器的职责 |
|------|-----------|----------------|
| 安装/升级组件 | **不做** — 调用 ComponentExecutor | **做** — 执行实际安装/升级逻辑 |
| 判断组件健康 | **不做** — 仅读 status 条件 | **做** — 自行实现健康检查逻辑 |
| 创建 ClusterComponent CR | **做** — 预创建 (PrecreatingMode) | 不做 (CVO 预创建后执行器接管) |
| 更新 ClusterComponent.status | **不做** — 仅消费 (watch) | **做** — 写入 Available/Progressing/Degraded/versions |
| 阻塞等待 | **做** — 监听 status 直到满足门控条件 | 不做 (写完 status 即完成) |
| 超时判定 | **做** — 30min 抑制 / 40min 失败 | 不做 |

### 6.1.1 ClusterComponent 不是 Operator — 概念澄清

**ClusterComponent 不是组件 (Operator) 本身，它只是一个状态报告 CR** (类似 OpenShift 的 ClusterOperator CR)。开发者不会"编写一个 ClusterComponent"，而是编写组件本体，ClusterComponent CR 由 CVO 自动创建、由组件执行器自动更新状态。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              组件 (Operator) 与 ClusterComponent CR 的关系                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌──────────────────────────────┐         ┌──────────────────────────────┐     │
│  │     组件本体 (Operator)       │         │   ClusterComponent CR        │     │
│  │     = ComponentExecutor      │  报告状态 │   (状态报告载体)             │     │
│  │                               │ ────────→│                              │     │
│  │  • inline: Go Phase Handler  │          │  spec:                       │     │
│  │  • yaml:   YAML 清单应用      │          │    componentType: yaml       │     │
│  │  • helm:   Helm Chart 部署    │          │    targetVersion: v1.11.1   │     │
│  │  • binary: 二进制 SSH 安装    │          │  status:                     │     │
│  │                               │  ←─监听──│    phase: Installed          │     │
│  │  这是实际干活的:               │   门控    │    conditions:               │     │
│  │  • 执行安装/升级               │          │      Available: True        │     │
│  │  • 执行健康检查               │          │      Progressing: False     │     │
│  │  • 执行回滚                   │          │      Degraded: False        │     │
│  │  • 执行卸载                   │          │      Upgradeable: True      │     │
│  │                               │          │    versions:                 │     │
│  │  开发者编写的核心:             │          │      [{component, v1.11.1}] │     │
│  │  ① ComponentVersion YAML      │          │    relatedObjects: [...]     │     │
│  │  ② 组件制品 (清单/Chart/二进制)│          │                              │     │
│  │  ③ ComponentExecutor (如需)   │          │  CVO 自动创建 (Precreating)  │     │
│  │                               │          │  执行器自动写 status          │     │
│  └──────────────────────────────┘          └──────────────────────────────┘     │
│                                                                                 │
│  职责划分:                                                                      │
│  ├── CVO:    预创建 ClusterComponent CR → 执行 ComponentExecutor → 监听状态门控 │
│  ├── 执行器:  执行安装/升级/健康检查 → 写入 ClusterComponent.status             │
│  └── 开发者: 编写 ComponentVersion + 制品 + 执行器 (不手动操作 ClusterComponent)  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 6.1.2 CVO 仅监听状态作为门控的作用

CVO **不创建组件本体，不执行组件安装/升级，不判断组件健康** — 这些都是 ComponentExecutor 的职责。CVO 在 ManifestGraph 执行后仅做一件事：**监听 ClusterComponent.status，阻塞直到满足门控条件**。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              CVO 状态门控的作用                                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Manifest Graph 执行流程:                                                       │
│                                                                                 │
│  Run Level 05: [bkeagent, provider]                                            │
│       │                                                                         │
│       ├── CVO 调用 ComponentExecutor.ExecuteComponent()                        │
│       │    └── 执行器: 安装 bkeagent → 写 ClusterComponent.status               │
│       │         → phase=Installed, Available=True, versions 匹配               │
│       │                                                                         │
│       ├── CVO 监听 ClusterComponent.status ★ 门控阶段                          │
│       │    ├── Available == True?        (否则阻塞)                             │
│       │    ├── versions 匹配期望版本?    (否则阻塞)                             │
│       │    ├── Degraded == False?       (否则: 初始化忽略, 其他 40min 超时)    │
│       │    └── Progressing == False?    (否则阻塞)                             │
│       │                                                                         │
│       │    门控满足 → 放行，进入下一 Run Level                                  │
│       │    门控未满足 → 阻塞当前 Run Level，等待组件完成                        │
│       │    阻塞超时 → 30min 抑制 (报告 "waiting on X")                          │
│       │             → 40min 失败 (Failing=True)                                │
│       │                                                                         │
│       ▼                                                                         │
│  Run Level 10-29: [etcd, kubernetes-master, kubernetes-worker]                 │
│       │  (阻塞直到 Run Level 05 的所有组件门控满足)                             │
│       │                                                                         │
│       ▼                                                                         │
│  Run Level 30: [containerd]                                                    │
│       │  (阻塞直到 Run Level 10-29 的所有组件门控满足)                          │
│       │                                                                         │
│       ▼                                                                         │
│  ...                                                                            │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**门控的核心作用**：

| 作用 | 说明 | 没有 CVO 监听状态时 |
|------|------|-------------------|
| **保证有序升级** | 低 Run Level 的组件全部健康后，才允许高 Run Level 开始执行 | 高层组件可能在底层组件未就绪时启动，导致依赖缺失 (如 kube-apiserver 在 etcd 未就绪时启动) |
| **组件解耦** | CVO 不关心组件如何安装、如何判断健康，只看标准化的 ClusterComponent.status | CVO 需为每个组件编写特定的健康检查逻辑，代码膨胀且难以维护 |
| **去中心化状态** | 组件执行器自行判断健康并报告，CVO 仅做消费者 | 集中式状态判断，单点瓶颈 |
| **阻塞而非失败** | 门控未满足时阻塞等待 (而非立即失败)，给组件足够时间完成升级 | 组件慢启动即被判失败，升级频繁中断 |
| **超时降级** | 30 分钟内抑制失败报告 ("waiting on X")，40 分钟后才真正失败 | 立即失败或无限等待，无法区分 "正常升级中" 和 "真正卡死" |
| **外部可观测** | 阻塞期间 ClusterComponent.status 反映真实状态，管理员可诊断 | 阻塞原因不可见，管理员无法判断卡在哪 |
| **跨重启恢复** | 门控基于 ClusterComponent.status (持久化)，CVO 重启后继续监听 | 内存状态丢失，重启后无法知道哪些组件已完成 |

### 6.2 CRD 定义

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: clustercomponents.cvo.openfuyao.cn
spec:
  group: cvo.openfuyao.cn
  scope: Namespaced
  names:
    plural: clustercomponents
    singular: clustercomponent
    kind: ClusterComponent
    shortNames: [cc]
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                componentType:
                  type: string
                  enum: [inline, yaml, helm, binary]
                targetVersion:
                  type: string
            status:
              type: object
              properties:
                phase:
                  type: string
                  enum: [Pending, Installing, Installed, Upgrading, RollingBack, Failed, Removed]
                currentVersion:
                  type: string
                conditions:
                  type: array
                  items:
                    type: object
                    properties:
                      type:
                        type: string
                        enum: [Available, Progressing, Degraded, Upgradeable]
                      status:
                        type: string
                        enum: ["True", "False", "Unknown"]
                      reason:
                        type: string
                      message:
                        type: string
                      lastTransitionTime:
                        type: string
                        format: date-time
                versions:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      version:
                        type: string
                relatedObjects:
                  type: array
                  items:
                    type: object
                    properties:
                      group:
                        type: string
                      resource:
                        type: string
                      namespace:
                        type: string
                      name:
                        type: string
                lastTransitionTime:
                  type: string
                  format: date-time
```

### 6.3 ClusterComponent 生命周期

#### 6.3.1 Phase 状态机设计

ClusterComponent 的 `status.phase` 字段表示组件的当前生命周期阶段，是一个有限状态机：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    ClusterComponent Phase 状态机                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│                    ┌──────────┐                                                │
│       CVO 预创建   │  Pending  │  (CVO 创建 CR, 等待执行器接管)                 │
│  ──────────────→  └────┬─────┘                                                │
│                          │ 执行器开始执行                                       │
│                          ▼                                                      │
│                    ┌──────────────┐    安装成功    ┌───────────┐               │
│                    │  Installing  │─────────────→ │ Installed │               │
│                    └──────┬───────┘               └─────┬─────┘               │
│                           │ 安装失败                     │ desiredVersion 变更 │
│                           ▼                              ▼                      │
│                    ┌──────────┐               ┌──────────────┐                  │
│                    │  Failed  │←──升级失败── │  Upgrading   │                  │
│                    └────┬─────┘               └──────┬───────┘                  │
│                         │ 重试                        │ 升级成功                 │
│                         ▼                             ▼                          │
│                    ┌──────────────┐           ┌───────────┐                    │
│                    │  Installing  │           │ Installed │                    │
│                    │  /Upgrading  │           └───────────┘                    │
│                    └──────────────┘                                              │
│                                                                                 │
│                    ┌──────────────┐                                              │
│           ┌───────│ RollingBack  │  (FailurePolicy=Rollback 时)                │
│           │       └──────┬───────┘                                              │
│           │ 回滚成功      │ 回滚失败                                               │
│           ▼              ▼                                                      │
│     ┌───────────┐   ┌──────────┐                                              │
│     │ Installed  │   │  Failed  │                                              │
│     └───────────┘   └──────────┘                                              │
│                                                                                 │
│                    ┌──────────┐                                                 │
│                    │  Removed │  (组件被移除, 不再管理)                         │
│                    └──────────┘                                                 │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

| Phase | 含义 | 进入条件 | 退出条件 |
|-------|------|---------|---------|
| `Pending` | CVO 已创建 CR，等待执行器接管 | CVO 预创建 (PrecreatingMode) | 执行器开始执行 → Installing/Upgrading |
| `Installing` | 首次安装中 | 执行器开始安装 (VersionContext 无 current) | 安装成功 → Installed / 安装失败 → Failed |
| `Upgrading` | 版本升级中 | 执行器开始升级 (VersionContext 有 current + target 且不同) | 升级成功 → Installed / 升级失败 → Failed |
| `Installed` | 安装/升级完成，组件运行中 | 执行成功 | desiredVersion 变更 → Upgrading / 卸载 → Removed |
| `Failed` | 安装/升级/回滚失败 | 执行失败 | 重试 → Installing/Upgrading / 超时耗尽 → 阻塞升级 |
| `RollingBack` | 回滚中 (仅 FailurePolicy=Rollback) | 升级失败触发回滚 | 回滚成功 → Installed / 回滚失败 → Failed |
| `Removed` | 组件被移除 | 卸载完成 | 终态 |

#### 6.3.2 Phase 与 Conditions 的关系

Phase 和 Conditions 是**两个正交维度**，Phase 表示"走到哪一步"，Conditions 表示"当前是否健康"。两者由不同主体写入：

| 维度 | phase | conditions |
|------|-------|------------|
| 语义 | 生命周期阶段 (走到哪一步) | 健康状态 (当前是否可用) |
| 写入者 | 组件执行器 | 组件执行器 |
| CVO 消费方式 | 不直接消费 (仅日志/展示) | **门控消费 (阻塞/放行)** |
| 变更频率 | 低 (每次安装/升级变更) | 中 (健康状态变化时变更) |

Phase × Conditions 的组合矩阵：

| Phase \ Condition | Available | Progressing | Degraded | Upgradeable | 含义 |
|-------------------|-----------|-------------|----------|-------------|------|
| `Pending` | Unknown | False | False | True | CR 已创建，等待执行器 |
| `Installing` | True/Unknown | **True** | False | True | 首次安装中 |
| `Upgrading` | True | **True** | False | True | 滚动升级中 (保持可用) |
| `Upgrading` | True | True | False | **False** | 升级中且不允许再次升级 |
| `Installed` | **True** | **False** | **False** | **True** | 正常态 (CVO 放行) |
| `Installed` | True | False | **True** | True | 可用但降级 (如 1 副本 crash-loop) |
| `Failed` | True/False | False | **True** | **False** | 失败 (CVO 阻塞) |
| `RollingBack` | True/False | **True** | **True** | False | 回滚中 |
| `Removed` | False | False | False | True | 已移除 (终态) |

> **CVO 门控只看 Conditions，不看 Phase**。Phase 供人类/工具观测，Conditions 供 CVO 自动化决策。

#### 6.3.3 条件设计原则

| 原则 | 说明 | 违反后果 |
|------|------|---------|
| **Available 优先于 Progressing** | Available=False 表示组件不可用，需立即干预，优先级高于 Progressing。升级中若组件仍可用，Available 应保持 True | 误报不可用 → CVO 错误阻塞升级 |
| **Degraded 表示持续降级，非瞬态** | Degraded 表示组件在持续一段时间内不匹配期望状态。Pod 重启等瞬态错误不应设置 Degraded，且不应频繁振荡 | 振荡 → CVO 反复阻塞/放行，升级卡死 |
| **Available 和 Degraded 可共存** | 组件可能同时 Available=True 且 Degraded=True (如 3 副本 1 个 crash-loop → 可用但降级) | 互斥设计 → 无法表达中间状态 |
| **正常升级期间不可 Degraded** | 正常升级过程中不应 Degraded=True。如果升级导致 Degraded，说明升级本身有问题 | 升级期间 Degraded → CVO 40 分钟后超时失败 |
| **正常升级期间 Available 不可为 False** | 升级期间必须保持 Available=True (如滚动升级保持最小可用副本) | Available=False → CVO 立即判定失败 |
| **混合版本状态必须报告旧版本** | 新旧版本共存时 status.versions 必须报告旧版本，全部更新完成后才报告新版本 | 误报新版本 → CVO 误认为升级完成，提前进入下一层 |
| **Upgradeable=False 仅阻止 minor 升级** | False 阻止 minor 升级，不阻止 patch 升级。缺失/True/Unknown 均允许升级 | 过度阻塞 → patch 升级被误阻 |
| **Progressing 仅表示状态转换** | 仅在从一态转向另一态时设置 Progressing=True，常规协调 (reconcile 已知状态) 不应设置 | 误报 → CVO 误判为仍在升级，阻塞后续组件 |
| **正常态也需设置 reason 和 message** | 正常态推荐 reason=AsExpected，message 用简洁描述 | 缺失 → 管理员无法诊断问题原因 |
| **LastTransitionTime 仅在状态变更时更新** | 条件 status 从 True→False 或 False→True 时才更新，不应每次 reconcile 都更新 | 误更新 → 监控指标虚高，时间统计失真 |
| **Forward-Only 无自动回滚** | 升级仅向前，失败不自动回滚。FailurePolicy=Rollback 仅回滚组件自身，不回滚集群版本 | 期望自动回滚 → 与 forward-only 哲学冲突 |

#### 6.3.4 条件语义速查表

| 场景 | Available | Progressing | Degraded | Upgradeable | versions |
|------|-----------|-------------|----------|-------------|----------|
| 正常态 | True | False | False | True | 当前版本 |
| 首次安装中 | True/Unknown | **True** | False | True | 空/目标版本 |
| 滚动升级中 (组件可用) | True | **True** | False | True | **旧版本** |
| 滚动升级中 (不允许再升级) | True | True | False | **False** | 旧版本 |
| 升级完成 | True | **False** | False | True | **新版本** |
| 可用但降级 | True | False | **True** | True | 当前版本 |
| 不可用 | **False** | False | **True** | False | 旧版本 |
| 升级失败 | True/False | False | **True** | **False** | 旧版本 |
| 回滚中 | True/False | **True** | **True** | False | 旧版本 |
| 升级受阻 | True | False | False | **False** | 当前版本 |

#### 6.3.5 生命周期执行流程

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    ClusterComponent 生命周期执行流程                              │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  CVO 视角:                                                                      │
│                                                                                 │
│  1. Manifest Graph 预创建阶段 (PrecreatingMode)                                  │
│     CVO 创建 ClusterComponent CR (spec.targetVersion, spec.componentType)      │
│     设置:                                                                       │
│     ├── status.phase = Pending                                                  │
│     ├── status.conditions[Available] = Unknown, reason=Pending                  │
│     ├── status.conditions[Progressing] = False                                  │
│     ├── status.conditions[Degraded] = False                                     │
│     └── status.conditions[Upgradeable] = True                                   │
│     → 提供 must-gather 可见性                                                    │
│                                                                                 │
│  2. 执行阶段 (UpdatingMode / InitializingMode)                                   │
│     CVO 调用 ExecutorRegistry 执行组件                                           │
│     执行器负责更新 ClusterComponent.status:                                     │
│     ├── 执行前:                                                                  │
│     │   ├── status.phase = Installing (首次) / Upgrading (升级)                  │
│     │   ├── status.conditions[Progressing] = True                                │
│     │   └── status.conditions[Available] = Unknown (安装) / True (升级)         │
│     │                                                                            │
│     ├── 执行中 (可选):                                                           │
│     │   └── 更新 status.message (进度描述)                                       │
│     │       → "Working towards v1.11.1: 2 of 3 replicas ready"                   │
│     │                                                                            │
│     └── 执行成功后:                                                              │
│         ├── status.phase = Installed                                             │
│         ├── status.currentVersion = targetVersion                                │
│         ├── status.versions = [{component, targetVersion}]                       │
│         ├── status.conditions[Available] = True, reason=AsExpected             │
│         ├── status.conditions[Progressing] = False, reason=AsExpected          │
│         ├── status.conditions[Degraded] = False, reason=AsExpected            │
│         └── status.conditions[Upgradeable] = True, reason=AsExpected           │
│                                                                                 │
│  3. 健康检查阻塞阶段 (HealthCheck) ★ CVO 门控                                   │
│     CVO 检查 ClusterComponent status (仅看 conditions, 不看 phase):             │
│     ├── Available == True?   (否则阻塞 → 30min 抑制 → 40min 失败)              │
│     ├── versions 包含 Release Image 声明的版本? (否则阻塞)                       │
│     ├── Degraded == False?  (否则: Initializing 忽略, 其他 40min 超时失败)     │
│     └── Progressing == False? (否则阻塞)                                        │
│     全部满足 → 放行，进入下一 Run Level                                         │
│     任一不满足 → 阻塞 (30min 内抑制 "waiting on X", 40min 后 Failing=True)    │
│                                                                                 │
│  4. 失败处理                                                                     │
│     执行器设置:                                                                  │
│     ├── status.phase = Failed                                                   │
│     ├── status.conditions[Degraded] = True, reason=ComponentFailed            │
│     ├── status.conditions[Degraded].message = err.Error()                       │
│     ├── status.conditions[Available] = True/False (取决于组件是否仍可用)        │
│     └── status.conditions[Upgradeable] = False (阻止后续升级)                   │
│     CVO 按 FailurePolicy 处理:                                                   │
│     ├── FailFast: 停止整个 DAG，标记升级失败                                     │
│     ├── Continue: 记录错误，继续执行后续组件                                     │
│     └── Rollback: 执行回滚逻辑 → phase=RollingBack → Installed/Failed           │
│                                                                                 │
│  5. 版本报告规则 (★ 关键)                                                        │
│     混合版本状态 (新旧版本共存) 时必须报告旧版本:                                │
│     ├── 3 副本中 1 个仍是旧版本 → versions = [{component, "旧版本"}]           │
│     └── 全部更新完成 → versions = [{component, "新版本"}]                       │
│     这确保 CVO 不会误认为升级已完成                                              │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 6.4 与现有 ClusterComponentStatuses 的关系

| 维度 | 现有 ClusterComponentStatuses (BKECluster.Status) | 新增 ClusterComponent CRD |
|------|--------------------------------------------------|-------------------------|
| 存储位置 | BKECluster.Status.ClusterComponentStatuses map | 独立 CR (namespaced) |
| 可观测性 | 仅 BKECluster 级别 | 可被 must-gather/监控/Prometheus 独立消费 |
| 状态来源 | CVO Scheduler 写入 | 组件执行器写入 (去中心化) |
| OwnerReference | 无 | BKECluster (级联删除) |
| 向后兼容 | 保留 (镜像写入) | 新增 (主数据源) |

> **迁移策略**：ClusterComponent CR 为主数据源，BKECluster.Status.ClusterComponentStatuses 作为镜像保持向后兼容 (CVO 在写入 ClusterComponent 后同步镜像到 BKECluster.Status)。

#### 6.4.1 "可被独立消费"详解

该差异源于**独立 CR vs 内嵌字段**在可观测性上的本质区别：

| 维度 | 现有 ClusterComponentStatuses (内嵌字段) | 新增 ClusterComponent (独立 CR) |
|------|----------------------------------------|-------------------------------|
| 资源类型 | 不是独立资源，是 BKECluster 的子字段 | 独立 Kubernetes 对象，有自己的 `apiVersion/kind/metadata` |
| 选取粒度 | 必须先 get BKECluster 再从 status map 中翻找 | 可直接 `kubectl get clustercomponent coredns` |
| 工具发现 | 非一等公民，标准工具无法按类型发现 | Kubernetes 一等公民，可被任何标准工具按资源类型独立发现 |

三个场景的具体含义：

**1. must-gather 独立消费**

must-gather 是诊断工具，按资源类型批量收集集群对象。`ClusterComponentStatuses` 是 BKECluster 的内嵌字段，must-gather 只能收集整个 BKECluster 对象，无法单独按组件筛选。而 `ClusterComponent` 是独立 CR，可以 `kubectl get clustercomponent -A` 单独列出、单独导出，诊断时可以只收集有问题的组件，无需导出完整的 BKECluster 对象。

**2. 监控独立消费**

运维人员想查看某个组件状态时，如果是内嵌字段，需要先 get BKECluster 再从 status map 里翻找。如果是独立 CR，可以直接 `kubectl describe clustercomponent coredns` 查看该组件的全部状态，不需要解析 BKECluster 的完整 status。CLI 操作和 Web Console 展示均可按组件独立操作。

**3. Prometheus 独立消费**

Prometheus 通过 kube-state-metrics 或自定义 exporter 采集指标。独立 CR 可以被直接配置为 informer watch 对象，暴露 `clustercomponent_up`、`clustercomponent_conditions` 等指标。而内嵌字段需要先 informer watch BKECluster，再从 status map 中提取组件状态，增加了采集复杂度和耦合度。

> **一句话总结**：独立 CR 是 Kubernetes 一等公民，可以被任何标准工具按资源类型独立发现、收集、告警，而内嵌字段只能随 BKECluster 整体被消费，无法独立选取。

### 6.5 ClusterComponent 开发流程

本节描述组件开发者如何将一个新组件接入 BKE CVO 体系，涵盖从声明定义到发布到 ReleaseImage OCI Bundle 的完整开发流程。

#### 6.5.1 开发流程总览

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                  ClusterComponent 开发流程                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Step 1: 确定组件元数据                                                         │
│  ├── 确定组件名称 (如 coredns)                                                 │
│  ├── 确定组件版本 (如 v1.11.1)                                                 │
│  ├── 确定组件类型 (inline / yaml / helm / binary)                              │
│  ├── 确定 Run Level (如 50)                                                    │
│  └── 确定依赖关系 (如依赖 containerd)                                          │
│                                                                                 │
│  Step 2: 编写 ComponentVersion YAML                                             │
│  ├── 定义 spec (type, version, runLevel, dependencies)                         │
│  ├── 定义 install/upgrade 策略                                                  │
│  ├── 定义 healthCheck (健康检查)                                                │
│  └── 定义 compatibility (兼容性约束)                                           │
│                                                                                 │
│  Step 3: 编写组件制品                                                           │
│  ├── yaml 类型: 编写 manifest YAML 清单                                         │
│  ├── helm 类型: 编写 Helm Chart + Values                                       │
│  ├── binary 类型: 编写制品 + installScript + configTemplates                  │
│  └── inline 类型: 实现 Phase Handler (Go 代码)                                  │
│                                                                                 │
│  Step 4: 实现组件执行器 (如需自定义类型)                                        │
│  ├── 实现 ComponentExecutor 接口                                               │
│  └── 注册到 ExecutorRegistry                                                    │
│                                                                                 │
│  Step 5: 更新 ClusterComponent 状态报告逻辑                                     │
│  ├── 执行器在执行前设置 phase=Installing/Upgrading                             │
│  ├── 执行器在执行后设置 phase=Installed, Available=True, versions 匹配         │
│  └── 执行器在失败时设置 phase=Failed, Degraded=True                            │
│                                                                                 │
│  Step 6: 本地测试                                                               │
│  ├── 单元测试 (执行器逻辑)                                                     │
│  ├── 集成测试 (DAG 执行 + 状态门控)                                            │
│  └── E2E 测试 (安装/升级/断点续传)                                              │
│                                                                                 │
│  Step 7: 打包到 ReleaseImage OCI Bundle                                         │
│  ├── 将 component.yaml 放入 components/<name>/<version>/                        │
│  ├── 将 manifest YAML 放入 manifests/<name>/<version>/                          │
│  └── 构建并推送 OCI Bundle                                                      │
│                                                                                 │
│  Step 8: 注册到 ReleaseImage CR                                                 │
│  ├── 在 ReleaseImage.spec.install.components 中添加 {name, version}            │
│  └── 在 ReleaseImage.spec.upgrade.components 中添加 {name, version, inline?}   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 6.5.2 Step 1: 确定组件元数据

| 决策项 | 说明 | 示例 |
|--------|------|------|
| 组件名称 | 全局唯一，小写，连字符分隔 | `coredns`, `kubernetes-master`, `containerd` |
| 组件版本 | SemVer 格式 | `v1.11.1`, `v1.29.0` |
| 组件类型 | `inline` / `yaml` / `helm` / `binary` | 见 §6.5.3 |
| Run Level | 定义执行顺序 (00-90) | 见 §7.3 Run Level 分配表 |
| 依赖关系 | 依赖的其他组件 | `[containerd]`, `[kubernetes-master]` |
| 安装模式 | `OwnNamespace` / `AllNamespaces` | 取决于组件作用范围 |
| 升级策略 | `FailFast` / `Continue` / `Rollback` | 默认 `FailFast` |

#### 6.5.3 Step 2: 编写 ComponentVersion YAML

**YAML 类型组件示例 (coredns)**:

```yaml
# components/coredns/v1.11.1/component.yaml
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.11.1
spec:
  name: coredns
  type: yaml                              # 组件类型
  version: v1.11.1
  runLevel: 50                            # ★ Run Level (DNS 插件层)

  yaml:
    namespace: kube-system
    applyStrategy: ServerSideApply        # 应用策略
    prune: true                           # 裁剪废弃资源
    pruneLabelSelector:
      app.kubernetes.io/managed-by: bke-cvo
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          podReady:
            namespace: kube-system
            labelSelector: "k8s-app=kube-dns"
            minReady: 1

  compatibility:
    constraints:
      - component: kubernetes
        rule: ">=1.24.0"

  dependencies:                           # 依赖关系
    - name: containerd
      phase: Install

  upgradeStrategy:
    mode: Parallel                        # 升级模式
    batchSize: 1
    failurePolicy: FailFast               # 失败策略
    timeout: "10m"
```

**Inline 类型组件示例 (kubernetes-master)**:

```yaml
# components/kubernetes-master/v1.29.0/component.yaml
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-master-v1.29.0
spec:
  name: kubernetes-master
  type: inline                             # Inline 类型 (Go 代码执行)
  version: v1.29.0
  runLevel: 10                             # Kubernetes 核心层

  inline:
    handler: EnsureMasterInit             # Handler 名称
    version: v1.0.0                       # Handler 版本

  compatibility:
    constraints:
      - component: etcd
        rule: ">=3.5.0"
      - component: containerd
        rule: ">=1.7.0"

  dependencies:
    - name: etcd
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    failurePolicy: FailFast
    timeout: "30m"
```

**Binary 类型组件示例 (containerd)**:

```yaml
# components/containerd/v1.7.18/component.yaml
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.18
spec:
  name: containerd
  type: binary                             # Binary 类型 (SSH 安装)
  version: v1.7.18
  runLevel: 30                             # 容器运行时层

  binary:
    artifacts:
      - name: containerd
        url: "registry.openfuyao.cn/binaries/containerd/{{componentVersion}}/containerd-{{componentVersion}}-linux-{{nodeArch}}.tar.gz"
        checksum: "sha256:abc123..."
        installPath: "/usr/local"
        executable: containerd

    configTemplates:
      - name: config.toml
        path: "/etc/containerd/config.toml"
        content: |
          version = 2
          [plugins."io.containerd.grpc.v1.cri"]
            sandbox_image = "{{imageRegistry}}/pause:3.9"

    installScript: |
      #!/bin/bash
      set -e
      systemctl stop containerd || true
      tar -xzf "{{artifact.containerd.path}}" -C {{artifact.containerd.installPath}}
      systemctl daemon-reload && systemctl enable containerd && systemctl start containerd

    healthCheck:
      enabled: true
      timeout: "2m"
      script: |
        systemctl is-active containerd

  compatibility:
    constraints:
      - component: kubernetes
        rule: ">=1.26.0"

  upgradeStrategy:
    mode: Rolling
    batchSize: 2
    failurePolicy: Continue
    timeout: "10m"
```

#### 6.5.4 Step 3: 编写组件制品

| 类型 | 制品目录 | 制品内容 |
|------|---------|---------|
| `yaml` | `manifests/<name>/<version>/<name>.yaml` | 多文档 YAML 清单 (支持 Go template 变量) |
| `helm` | `charts/<name>/<version>/` | Helm Chart (Chart.yaml + templates/ + values.yaml) |
| `binary` | `binaries/<name>/<version>/` | 二进制制品 (tar.gz) + 校验和 |
| `inline` | 无独立制品 | Go 代码 (Phase Handler)，打包到 BKE 二进制 |

**YAML 清单示例 (coredns.yaml)**:

```yaml
# manifests/coredns/v1.11.1/coredns.yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: coredns
  namespace: kube-system
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: coredns
  namespace: kube-system
  labels:
    k8s-app: kube-dns
    app.kubernetes.io/managed-by: bke-cvo    # ★ Prune label
spec:
  replicas: {{ .ReplicaCount | default 2 }}
  selector:
    matchLabels:
      k8s-app: kube-dns
  template:
    metadata:
      labels:
        k8s-app: kube-dns
    spec:
      containers:
        - name: coredns
          image: {{ .ImageRegistry }}/coredns/coredns:{{ .ComponentVersion }}
          ports:
            - containerPort: 53
              name: dns
              protocol: UDP
```

> 模板变量通过 `manifest.TemplateContext` 注入: `.ClusterName`, `.Namespace`, `.KubernetesVersion`, `.OpenFuyaoVersion`, `.ImageRegistry`, `.ComponentVersion`, `.ReplicaCount` 等。

#### 6.5.5 Step 4: 实现组件执行器 (自定义类型)

对于 `yaml` 和 `inline` 类型，BKE 已有内置执行器。如需自定义类型 (如 `helm`、`binary`)，需实现 `ComponentExecutor` 接口:

```go
// pkg/dagexec/executor.go (现有接口)

type ComponentExecutor interface {
    ExecuteComponent(ctx context.Context, node *topology.ComponentNode,
        execCtx *ExecutionContext) error
    GetComponentType() ComponentType
}
```

**Helm 执行器实现示例**:

```go
// pkg/dagexec/helm_executor.go

type HelmExecutor struct {
    chartFetcher  ChartFetcher
    valuesRenderer ValuesRenderer
    helmAction    HelmActionExecutor
    healthChecker HealthChecker
}

func (e *HelmExecutor) GetComponentType() ComponentType {
    return ComponentType("helm")
}

func (e *HelmExecutor) ExecuteComponent(ctx context.Context,
    node *topology.ComponentNode, execCtx *ExecutionContext) error {

    // 1. 从 ComponentVersion 获取 Helm 配置
    cv := execCtx.CVStore.GetComponentVersion(node.Name, node.Version)

    // 2. 更新 ClusterComponent 状态 → Installing/Upgrading
    execCtx.ComponentStatusUpdater.MarkPending(node.Name)

    // 3. 拉取 Chart
    chart, err := e.chartFetcher.Fetch(ctx, cv.Spec.Helm.Chart)
    if err != nil {
        execCtx.ComponentStatusUpdater.MarkFailed(node.Name, err)
        return err
    }

    // 4. 渲染 Values (模板变量替换)
    values, err := e.valuesRenderer.Render(cv.Spec.Helm.Values, execCtx.TemplateContext)
    if err != nil {
        execCtx.ComponentStatusUpdater.MarkFailed(node.Name, err)
        return err
    }

    // 5. 执行 Helm Install/Upgrade
    err = e.helmAction.Execute(ctx, cv.Spec.Helm, chart, values)
    if err != nil {
        execCtx.ComponentStatusUpdater.MarkFailed(node.Name, err)
        return err
    }

    // 6. 健康检查
    if cv.Spec.Helm.HealthCheck != nil && cv.Spec.Helm.HealthCheck.Enabled {
        if err := e.healthChecker.Check(ctx, execCtx.TargetClient, cv.Spec.Helm.HealthCheck); err != nil {
            execCtx.ComponentStatusUpdater.MarkFailed(node.Name, err)
            return err
        }
    }

    // 7. 更新 ClusterComponent 状态 → Installed, Available=True
    execCtx.ComponentStatusUpdater.MarkInstalled(node.Version)

    return nil
}

// 注册到 ExecutorRegistry
func init() {
    // 在 Scheduler 初始化时注册
    // sched.Registry.Register(ComponentType("helm"), &HelmExecutor{...})
}
```

#### 6.5.6 Step 5: ClusterComponent 状态报告开发规范

##### 6.5.6.1 设计思路

ClusterComponent 状态报告的设计借鉴 OpenShift ClusterOperator 的状态模型，核心思路是**将组件状态从"管理者观测"转变为"组件自报告"**，实现去中心化的状态上报与门控阻塞：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              ClusterComponent 状态报告设计思路                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  传统模式 (现状):                                                               │
│  ┌────────────┐    写入 Status    ┌──────────────────────────┐                  │
│  │ CVO        │ ───────────────→ │ BKECluster.Status.       │                  │
│  │ Scheduler  │                   │   ClusterComponentStatuses│                 │
│  │            │                   │   (集中式，管理者观测)    │                  │
│  └────────────┘                   └──────────────────────────┘                  │
│  问题:                                                                          │
│  • 状态由管理者写入，组件本身无法主动报告                                       │
│  • 组件状态内嵌在 BKECluster.Status 中，非一等公民                              │
│  • 管理者需知道每个组件如何判断健康，逻辑集中且膨胀                             │
│  • 无法被标准工具独立发现和消费                                                 │
│                                                                                 │
│  CVO 设计模式:                                                                  │
│  ┌────────────┐  预创建 (Precreate)  ┌────────────────────┐                    │
│  │ BKE CVO    │ ───────────────────→ │ ClusterComponent CR │                   │
│  │            │                       │ (独立一等公民)      │                   │
│  │            │ ←── 阻塞门控 ──────── │                     │                   │
│  │            │     (监听状态)         │                     │                   │
│  └────────────┘                       └─────────┬──────────┘                   │
│                                                  │ 自报告                      │
│                                                  ▼                             │
│                                       ┌────────────────────┐                   │
│                                       │ Component Executor  │                   │
│                                       │ (inline/yaml/helm/  │                   │
│                                       │  binary)            │                   │
│                                       │                     │                   │
│                                       │ 执行前: MarkPending  │                   │
│                                       │ 执行后: MarkInstalled│                   │
│                                       │ 失败时: MarkFailed   │                   │
│                                       └────────────────────┘                   │
│                                                                                 │
│  优势:                                                                          │
│  • 组件执行器自行报告状态，逻辑去中心化                                         │
│  • 独立 CR 是 Kubernetes 一等公民，可被标准工具独立发现                          │
│  • CVO 仅监听状态作为门控，不关心组件如何判断健康                               │
│  • 组件可独立 must-gather/监控/Prometheus 消费                                  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

##### 6.5.6.2 状态原则

ClusterComponent 状态报告遵循以下核心原则，每条原则对应 OpenShift CVO 经过大规模生产验证的设计决策：

| 原则 | 说明 | 违反后果 |
|------|------|---------|
| **1. Available 优先于 Progressing** | `Available=False` 表示组件不可用，需立即管理员干预，**优先级高于 Progressing**。即使 Progressing=True (正在升级)，如果组件仍能服务，Available 应为 True | 误报不可用 → CVO 错误阻塞升级，或管理员误判为紧急故障 |
| **2. Degraded 表示持续降级，非瞬态** | Degraded 表示组件在**持续一段时间**内不匹配期望状态，导致服务质量下降。瞬态错误 (如 Pod 重启) 不应设置 Degraded。Degraded 不应频繁振荡 | Degraded 振荡 → CVO 反复阻塞/放行升级，升级卡死或误放行 |
| **3. Available 和 Degraded 可共存** | 组件可能同时 Available=True 且 Degraded=True。例: Deployment 期望 3 副本，1 个 crash-looping → 服务可用但降级 | 互斥设计 → 无法表达 "可用但需修复" 的中间状态 |
| **4. 正常升级期间不可 Degraded** | 正常升级过程中组件不应 Degraded=True。如果升级导致 Degraded，说明升级本身有问题 | 升级期间 Degraded → CVO 40 分钟后超时失败，升级被阻塞 |
| **5. 正常升级期间 Available 不可为 False** | 升级期间组件必须保持 Available=True (如滚动升级保持最小可用副本) | Available=False → CVO 立即判定升级失败 |
| **6. 混合版本状态必须报告旧版本** | 当新旧版本共存时 (如滚动升级中 3 副本 2 新 1 旧)，`status.versions` 必须报告**旧版本**。只有确认不再运行旧版本时才更新为新版本 | 误报新版本 → CVO 认为升级已完成，提前进入下一 Run Level，导致混合版本不兼容 |
| **7. Upgradeable=False 仅阻止 minor 升级** | `Upgradeable=False` 阻止 **minor** 版本升级 (如 v2.6→v2.7)，**不阻止** patch 升级 (如 v2.7.0→v2.7.1)。缺失/True/Unknown 均允许升级 | 过度阻塞 → patch 升级被误阻，集群无法获取安全补丁 |
| **8. Progressing 仅表示状态转换，非常规协调** | Progressing=True 仅在组件从一态转向另一态时设置 (如版本变更、配置传播)。常规协调 (reconcile 已知状态) **不应**设置 Progressing。不应因 DaemonSet/Deployment 适应节点扩容或重启而设置 | 误报 Progressing → CVO 误判为仍在升级，阻塞后续组件 |
| **9. 条件需设置 reason 和 message (含正常态)** | 正常态也需设置 `reason` 和 `message`，不能仅设 `status=True/False`。推荐正常态 reason 用 `AsExpected`，message 用简洁描述 | 缺失 reason/message → 管理员无法诊断 "为什么 Available=False" |
| **10. 条件消息遵循格式规范** | `Progressing.message` 最重要 (CLI 默认显示)，应 5-10 词简洁描述。`Available.message` 单句无标点。`Degraded.message` 数句含足够诊断信息 | 消息不规范 → CLI/Web Console 展示混乱，运维效率低 |
| **11. LastTransitionTime 仅在状态变更时更新** | `LastTransitionTime` 仅在条件 `status` 从 True→False 或 False→True 时更新，**不应在每次 reconcile 时更新** | 误更新 → 监控指标条件转换计数虚高，时间统计失真 |
| **12. Forward-Only — 无自动回滚** | 升级是 forward-only，失败不自动回滚。组件必须能处理 N-1 兼容性 (新 Operator + 旧 Operand 共存)。FailurePolicy=Rollback 仅回滚组件自身，不回滚集群版本 | 期望自动回滚 → 设计与 CVO 哲学冲突，可能引入更大风险 |

##### 6.5.6.3 条件语义速查表

| 条件 | 正常态 | 升级中 | 降级 | 不可用 | 升级受阻 |
|------|--------|--------|------|--------|---------|
| `Available` | True | True | True | **False** | True |
| `Progressing` | False | **True** | False | False | False |
| `Degraded` | False | False | **True** | True | False |
| `Upgradeable` | True | True | True | True | **False** |
| `versions` | 当前版本 | **旧版本** (混合态) | 当前版本 | 旧版本 | 当前版本 |

##### 6.5.6.4 状态报告执行规范

组件执行器必须遵循以下状态报告规范:

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              ClusterComponent 状态报告执行规范                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  执行前:                                                                        │
│  ├── MarkPending(name)                                                          │
│  │   → status.phase = Pending                                                   │
│  │   → status.conditions[Progressing] = True, reason=ComponentInstalling       │
│  │   → status.conditions[Available] = Unknown                                   │
│  │                                                                              │
│  执行中 (可选):                                                                 │
│  ├── 更新 status.message (进度描述)                                             │
│  │   → "Working towards v1.11.1: 2 of 3 replicas ready"                         │
│  │                                                                              │
│  执行成功:                                                                      │
│  ├── MarkInstalled(version)                                                     │
│  │   → status.phase = Installed                                                 │
│  │   → status.currentVersion = version                                          │
│  │   → status.versions = [{component, version}]                                  │
│  │   → status.conditions[Available] = True, reason=AsExpected                  │
│  │   → status.conditions[Progressing] = False, reason=AsExpected               │
│  │   → status.conditions[Degraded] = False, reason=AsExpected                  │
│  │   → status.conditions[Upgradeable] = True, reason=AsExpected                │
│  │                                                                              │
│  执行失败:                                                                      │
│  ├── MarkFailed(name, err)                                                      │
│  │   → status.phase = Failed                                                   │
│  │   → status.conditions[Degraded] = True, reason=ComponentFailed              │
│  │   → status.conditions[Degraded].message = err.Error()                        │
│  │   → status.conditions[Available] = True/False (取决于组件是否仍可用)          │
│  │   → status.conditions[Upgradeable] = False (阻止后续升级)                    │
│  │                                                                              │
│  回滚中 (如果 FailurePolicy=Rollback):                                          │
│  ├── MarkRollingBack()                                                          │
│  │   → status.phase = RollingBack                                              │
│  │   → status.conditions[Progressing] = True                                   │
│  │   → status.conditions[Degraded] = True                                       │
│  │                                                                              │
│  状态门控阻塞 (CVO 视角):                                                       │
│  │   CVO 在 ManifestGraph 执行后检查 ClusterComponent:                          │
│  │   ├── Available == True?  (否则阻塞 30min → 40min 超时失败)                 │
│  │   ├── versions 包含 ReleaseImage 声明版本? (否则阻塞)                       │
│  │   ├── Degraded == False? (否则: Initializing 忽略, 其他 40min 超时失败)    │
│  │   └── Progressing == False? (否则阻塞)                                      │
│  │                                                                              │
│  版本报告规则 (★ 关键):                                                          │
│  │   混合版本状态 (新旧版本共存) 时必须报告旧版本:                              │
│  │   ├── 3 副本中 1 个仍是旧版本 → status.versions = [{operator, "旧版本"}]   │
│  │   └── 全部更新完成 → status.versions = [{operator, "新版本"}]              │
│  │   这确保 CVO 不会误认为升级已完成                                            │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 6.5.7 Step 6: 本地测试

**单元测试**:

```go
// pkg/dagexec/helm_executor_test.go

func TestHelmExecutor_ExecuteComponent_Success(t *testing.T) {
    // 准备 mock
    mockChartFetcher := &mocks.ChartFetcher{...}
    mockValuesRenderer := &mocks.ValuesRenderer{...}
    mockHelmAction := &mocks.HelmActionExecutor{...}
    mockHealthChecker := &mocks.HealthChecker{...}
    mockStatusUpdater := &mocks.ComponentStatusUpdater{...}

    executor := &HelmExecutor{
        chartFetcher:  mockChartFetcher,
        valuesRenderer: mockValuesRenderer,
        helmAction:    mockHelmAction,
        healthChecker: mockHealthChecker,
    }

    node := &topology.ComponentNode{Name: "coredns", Version: "v1.11.1"}
    execCtx := &ExecutionContext{
        ComponentStatusUpdater: mockStatusUpdater,
        // ...
    }

    err := executor.ExecuteComponent(ctx, node, execCtx)

    assert.NoError(t, err)
    // 验证状态更新序列
    mockStatusUpdater.AssertCalled(t, "MarkPending", "coredns")
    mockStatusUpdater.AssertCalled(t, "MarkInstalled", "v1.11.1")
}
```

**集成测试**:

```go
// test/integration/cvo_component_test.go

func TestCVO_ComponentInstall_DAGExecution(t *testing.T) {
    // 1. 创建 BKECluster + ClusterVersion
    // 2. 创建 ReleaseImage (含 coredns ComponentVersion)
    // 3. 等待 CVO 执行 DAG
    // 4. 验证:
    //    - ClusterComponent CR 被创建
    //    - status.phase == Installed
    //    - status.conditions[Available] == True
    //    - status.versions 匹配
    //    - Deployment 在目标集群存在
}
```

#### 6.5.8 Step 7: 打包到 ReleaseImage OCI Bundle

```txt
release-image/
├── release.yaml                           # ReleaseImage CR + 版本元数据
├── components/
│   ├── coredns/
│   │   └── v1.11.1/
│   │       └── component.yaml            # ComponentVersion CR
│   ├── containerd/
│   │   └── v1.7.18/
│   │       └── component.yaml
│   └── kubernetes-master/
│       └── v1.29.0/
│           └── component.yaml
├── manifests/
│   ├── coredns/
│   │   └── v1.11.1/
│   │       └── coredns.yaml              # YAML 清单
│   └── ...
└── image-references                       # 镜像引用列表
```

**构建脚本**:

```bash
# 构建 ReleaseImage OCI Bundle
docker build -t registry.openfuyao.cn/bke/release:v2.7.0 .
docker push registry.openfuyao.cn/bke/release:v2.7.0
```

#### 6.5.9 Step 8: 注册到 ReleaseImage CR

```yaml
# release.yaml 中的 ReleaseImage CR
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: release-v2.7.0
spec:
  version: "v2.7.0"
  digest: "sha256:abc123..."
  verifySignature: true
  signatureKey: "cosign-public-key"

  install:
    components:
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.18
      - name: kubernetes-master
        version: v1.29.0
        inline:
          handler: EnsureMasterInit
          version: v1.0.0
      - name: coredns                      # ★ 新组件
        version: v1.11.1

  upgrade:
    components:
      - name: bkeagent
        version: v2.7.0
      - name: containerd
        version: v1.7.18
      - name: kubernetes-master
        version: v1.29.0
        inline:
          handler: EnsureMasterUpgrade
          version: v1.0.0
      - name: coredns                      # ★ 新组件
        version: v1.11.1
```

#### 6.5.10 开发流程检查清单

| 检查项 | 说明 | 完成 |
|--------|------|------|
| ComponentVersion YAML 已编写 | spec 包含 name, type, version, runLevel | ☐ |
| 组件制品已编写 | manifest YAML / Helm Chart / 二进制制品 | ☐ |
| 依赖关系已声明 | ComponentVersion.spec.dependencies | ☐ |
| 兼容性约束已声明 | ComponentVersion.spec.compatibility | ☐ |
| 升级策略已配置 | upgradeStrategy (mode, batchSize, failurePolicy) | ☐ |
| 健康检查已配置 | healthCheck (PodReady / EndpointReady / Custom) | ☐ |
| 执行器已实现 (如需) | ComponentExecutor 接口 + 注册 | ☐ |
| ClusterComponent 状态报告已实现 | MarkPending → MarkInstalled/MarkFailed | ☐ |
| 版本报告规则已遵循 | 混合版本报告旧版本 | ☐ |
| 单元测试已编写 | 覆盖率 >85% | ☐ |
| 集成测试已编写 | DAG 执行 + 状态门控 | ☐ |
| 已打包到 ReleaseImage OCI Bundle | components/ + manifests/ | ☐ |
| 已注册到 ReleaseImage CR | install.components + upgrade.components | ☐ |

---

## 7. Manifest Graph 分层 DAG 设计

### 7.1 设计思路

借鉴 OpenShift CVO 的 Manifest Graph，按 ComponentVersion 的 `runLevel` 字段分层阻塞。同 runLevel 不同组件并行执行，跨 runLevel 阻塞。支持三种并行化模式 (Initializing/Updating/Reconciling)。

### 7.2 ComponentVersion 扩展

在现有 ComponentVersion spec 中新增 `runLevel` 字段：

```yaml
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.11.1
spec:
  name: coredns
  type: yaml
  version: v1.11.1
  runLevel: 50                    # ★ 新增：Run Level (默认 50)
  # ... 其余字段不变
```

### 7.3 Run Level 分配

| Run Level | 组件 | 说明 |
|-----------|------|------|
| 00-04 | bke-cvo 自身 | CVO 先更新自己 (类似 OpenShift CVO) |
| 05 | bkeagent, provider | 基础设施 (Agent 先就绪) |
| 10-29 | etcd, kubernetes-master, kubernetes-worker | Kubernetes 核心控制平面 |
| 30 | containerd | 容器运行时 |
| 50 | coredns, kube-proxy | 集群网络/DNS 插件 |
| 60 | openfuyao-core, calico, cert-manager | 平台组件 |
| 70 | 节点级组件 | 节点级 daemonset |

### 7.4 三种并行化模式

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    Manifest Graph 三种并行化模式                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  InitializingPayload (首次安装):                                                 │
│  ├── FlattenByNumberAndComponent: 同 runLevel 全部并行                          │
│  ├── maxWorkers = len(nodes)  // 全并行                                         │
│  └── 容忍 Degraded (UpdateEffectReport)                                         │
│                                                                                 │
│  UpdatingPayload (版本升级):                                                     │
│  ├── ByNumberAndComponent: 同 runLevel 同组件串行，同 runLevel 不同组件并行     │
│  ├── 跨 runLevel 阻塞                                                           │
│  ├── maxWorkers = 16                                                            │
│  └── 错误阻塞依赖节点 (UpdateEffectFail)                                        │
│                                                                                 │
│  ReconcilingPayload (常规协调):                                                  │
│  ├── PermuteOrder: 随机排列 (避免排序 Bug)                                      │
│  ├── maxWorkers = 2                                                             │
│  └── 每 minInterval 执行一次                                                     │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.5 Task Graph 数据结构

```go
// pkg/cvo/manifestgraph/graph.go

type TaskNode struct {
    RunLevel int          // ComponentVersion.spec.runLevel
    Component string      // 组件名
    Tasks    []*Task      // 该节点的任务列表
    In       []int        // 前置节点索引
    Out      []int        // 依赖节点索引
}

type TaskGraph struct {
    Nodes []*TaskNode
}

type Task struct {
    Index       int
    Component   string
    Version     string
    ComponentType string    // inline|yaml|helm|binary
    Manifest    manifest.Manifest
    Backoff     wait.Backoff
}

// 图构建方法
func NewTaskGraph(tasks []*Task) *TaskGraph        // 单节点图
func (g *TaskGraph) Split(onFn SplitFunc)          // 在匹配点分裂
func (g *TaskGraph) Parallelize(breakFn BreakFunc) // 并行化
func (g *TaskGraph) Roots() []*TaskNode             // 根节点

// 排序策略
func ByNumberAndComponent(tasks []*Task) [][]*TaskNode      // 有序 (升级)
func FlattenByNumberAndComponent(tasks []*Task) [][]*TaskNode // 扁平 (安装)
func PermuteOrder(breakFn, r *rand.Rand) BreakFunc           // 随机 (协调)

// 执行器
func RunGraph(ctx, graph *TaskGraph, maxWorkers int, fn GraphFunc) []error
```

### 7.6 与现有 DAG 调度器的关系

| 维度 | 现有 DAG (pkg/topology + pkg/dagexec) | 新增 ManifestGraph |
|------|--------------------------------------|-------------------|
| 排序依据 | ComponentVersion.spec.dependencies (拓扑排序) | runLevel + dependencies (分层 + 拓扑) |
| 并行粒度 | 同 batch 无依赖并行 | 同 runLevel 不同组件并行 |
| 阻塞语义 | 仅依赖阻塞 | runLevel 阻塞 + 依赖阻塞 |
| 执行器 | Scheduler.ExecuteDAG | ManifestGraph.RunGraph |
| 状态管理 | DeclarativeUpgradeStatus | DeclarativeUpgradeStatus (复用) + ClusterComponent |

> **迁移策略**：ManifestGraph 封装现有 DAG 调度器，在 `RunGraph` 内部仍调用 `Scheduler.ExecuteDAG`，但增加 runLevel 分层逻辑。Feature Gate `ManifestGraphEnabled` 控制。

---

## 8. ClusterVersion 状态机设计

### 8.1 正式 State 接口

```go
// pkg/cvo/state.go

type State interface {
    Name() ClusterVersionPhase
    Enter(ctx context.Context, cv *ClusterVersion) error
    Execute(ctx context.Context, cv *ClusterVersion) (State, error)
    Exit(ctx context.Context, cv *ClusterVersion) error
    CanTransitionTo(target State) bool
}
```

### 8.2 状态机

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    ClusterVersion 状态机                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   ┌──────────┐                                                                  │
│   │ Pending  │  (等待 desiredVersion 设置)                                      │
│   └────┬─────┘                                                                  │
│        │ desiredVersion 设置 + ReleaseImage Valid                               │
│        ▼                                                                        │
│   ┌──────────┐    前置检查失败    ┌──────────────┐                              │
│   │PreChecking│──────────────→│ PreCheckFailed│                              │
│   └────┬─────┘                  └──────┬───────┘                              │
│        │ 前置检查通过                     │ 用户重新触发                          │
│        ▼                                  ▼                                      │
│   ┌──────────┐                                                               │
│   │Installing│  (首次安装，全并行 DAG)                                       │
│   └────┬─────┘                                                               │
│        │ 全部组件 Installed                                                  │
│        ▼                                                                        │
│   ┌──────────┐                                                               │
│   │ Installed│  (安装完成)                                                    │
│   └────┬─────┘                                                               │
│        │ desiredVersion 变更                                                 │
│        ▼                                                                        │
│   ┌──────────┐                                                               │
│   │Upgrading │  (版本升级，有序 DAG)                                          │
│   └────┬─────┘                                                               │
│        │                              ┌──────────┐                           │
│        ├── 全部组件升级成功 ────────→│ Upgraded │                           │
│        │                              └────┬─────┘                           │
│        │                                   │ (多 hop 时继续升级)             │
│        │                                   ▼                                    │
│        │                              ┌──────────┐                           │
│        │                              │ Upgrading│ (下一 hop)                │
│        │                              └──────────┘                           │
│        │                              ┌──────────┐                           │
│        └── 组件升级失败 ──────────────→│  Failed  │                           │
│                                       └────┬─────┘                           │
│                                            │ 重试次数未耗尽                     │
│                                            ▼                                    │
│                                       ┌──────────┐                           │
│                                       │Upgrading │ (重试)                    │
│                                       └──────────┘                           │
│                                                                                 │
│  ┌──────────┐                                                                   │
│  │ Blocked  │  (Upgradeable=False 或前置条件阻止)                              │
│  └──────────┘                                                                   │
│                                                                                 │
│  ┌──────────┐                                                                   │
│  │  Ready   │  (当前版本 == desiredVersion，无进行中操作)                       │
│  └──────────┘                                                                   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 8.3 与现有 ClusterVersionPhase 的关系

现有 Phase 枚举 (`Pending`, `Installing`, `Installed`, `Ready`, `PreChecking`, `Upgrading`, `Upgraded`, `Blocked`, `PreCheckFailed`, `Failed`) 保持不变。新增 State 接口封装 Phase 转换逻辑，`Execute()` 方法替代当前散落在 Reconciler 中的 if-else 分支。

---

## 9. SyncWorker 状态机设计

### 9.1 设计思路

借鉴 OpenShift CVO SyncWorker，管理 Payload 加载/验证/应用的状态机，支持边沿触发 (Update) + 水平触发 (定期协调)。

### 9.2 接口定义

```go
// pkg/cvo/syncworker.go

type SyncWorker interface {
    Start(ctx context.Context, maxWorkers int)
    Update(ctx context.Context, generation int64, desired Update,
           cv *ClusterVersion, state PayloadState,
           enabledFeatureGates sets.Set[string]) *SyncWorkerStatus
    StatusCh() <-chan SyncWorkerStatus
    Initialized() bool
}

type SyncWork struct {
    Generation int64
    Desired    Update
    State      PayloadState
    Completed  int     // 连续成功次数
    Attempt    int     // 失败重试次数
}

type SyncWorkerStatus struct {
    Generation  int64
    Failure     error
    Done, Total int
    Reconciling  bool
    Initial      bool
    VersionHash  string
    Actual       Release
}
```

### 9.3 Payload State

```go
type PayloadState int
const (
    UpdatingPayload PayloadState = iota    // 有序升级
    ReconcilingPayload                     // 常规协调
    InitializingPayload                    // 首次安装
    PrecreatingPayload                     // 预创建 (ClusterComponent 可见性)
)
```

### 9.4 状态机

```txt
Initial ──Update()──→ Sync ──apply err──→ Error ──backoff──→ Sync
                      │                   │
                      │ apply ok          │ apply err
                      ▼                   ▼
                  Reconciling ←──Update(diff)── Sync
                      │
                      │ apply err
                      ▼
                    Error
```

### 9.5 apply() 流程

```txt
apply(ctx, work *SyncWork, maxWorkers int):
  1. 构建 Task 列表 (从 ReleaseImage bundle)
  2. 构建 TaskGraph (NewTaskGraph → Split(SplitOnJobs) → Parallelize(strategy))
     ├── Initializing: FlattenByNumberAndComponent, maxWorkers=all
     ├── Updating: ByNumberAndComponent, maxWorkers=16
     └── Reconciling: PermuteOrder, maxWorkers=2
  3. RunGraph(ctx, graph, maxWorkers, func(ctx, tasks) error {
       第一遍 (PrecreatingMode): 预创建 ClusterComponent CR
       第二遍 (实际应用): 
         ├── manifest.Include(...) 过滤 (capabilities, feature gates)
         ├── task.Run(ctx, version, builder, state)
         │   └── wait.ExponentialBackoff 重试 builder.Apply
         │       └── UpdateError 快速失败 (不重试)
         └── 更新 ClusterComponent.status
     })
  4. 错误汇总 (summarizeTaskGraphErrors)
```

---

## 10. Precondition 前置检查链

### 10.1 接口定义

```go
// pkg/cvo/precondition/precondition.go

type Precondition interface {
    Run(ctx context.Context, releaseCtx ReleaseContext) error
    Name() string
}

type ReleaseContext struct {
    DesiredVersion   string
    CurrentVersion   string
    ClusterVersion   *ClusterVersion
    UpgradePath      *UpgradePath
}

type List []Precondition

type Error struct {
    Nested          error
    Reason, Message string
    NonBlockingWarning bool
}

func RunAll(ctx context.Context, preconditions List, releaseCtx ReleaseContext) []error
func Summarize(errs []error, force bool) (blocking bool, err error)
```

### 10.2 默认前置条件

| 前置条件 | 检查内容 | 阻塞条件 |
|---------|---------|---------|
| `Rollback` | desiredVersion < currentVersion | 阻止回滚 (forward-only) |
| `GiantHop` | desiredVersion 跨多个 minor 版本 | 阻止跨版本跳跃 (如 v2.5→v2.8) |
| `Upgradeable` | ClusterComponent Upgradeable 条件 | 有组件 Upgradeable=False |
| `RecommendedUpdate` | UpgradePath 中是否存在路径 | 版本不在升级图中 |
| `ReleaseImageValid` | ReleaseImage Phase == Valid | Release Image 未通过验证 |
| `SignatureVerified` | 签名验证通过 | 签名验证失败 (除非 force) |

### 10.3 Force 模式

`Update.Force = true` 时，`Summarize` 将阻塞错误转为非阻塞警告 ("Forced through blocking failures")，记录到 `ClusterVersion.status.conditions[ReleaseAccepted]`。

---

## 11. Risk 风险聚合树

### 11.1 设计思路

借鉴 OpenShift CVO 的风险聚合树，将多个风险源聚合为 `ClusterVersion.status.conditions[Upgradeable]` 条件。

### 11.2 风险源

```go
// pkg/risk/source.go

type Source interface {
    Risks(ctx context.Context, versions []string) ([]Risk, error)
    Name() string
}

type Risk struct {
    Name    string
    URL     string
    Message string
    Version string
    Condition metav1.Condition
}
```

| 风险源 | 检查内容 | 回调 |
|--------|---------|------|
| `ClusterComponentUpgradeable` | ClusterComponent Upgradeable=False | 重排 availableUpdatesQueue |
| `AdminAck` | ConfigMap 中的 Admin 确认 | 重排 availableUpdatesQueue |
| `Alert` | Prometheus 告警 (如果集成) | 重排 availableUpdatesQueue |
| `ResourceDeletion` | 资源删除进行中 | 重排 availableUpdatesQueue |

### 11.3 聚合逻辑

```go
// pkg/risk/aggregate.go

upgradeable = aggregate.New(
    clusterComponentUpgradeable,  // ClusterComponent Upgradeable 条件
    adminAck,                      // ConfigMap Admin 确认
    alert,                         // Prometheus 告警 (可选)
    resourceDeletion,              // 资源删除检查
)

risks = aggregate.New(
    alert,
    upgradeable,
)

// 聚合结果写入 ClusterVersion.status.conditions[Upgradeable]
```

---

## 12. Release Image 签名验证

### 12.1 设计思路

借鉴 OpenShift CVO 的签名验证机制，为 BKE Release Image 增加 cosign 签名验证。

### 12.2 验证流程

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    Release Image 签名验证流程                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. 从 ReleaseImage.spec 获取验证配置                                            │
│     ├── verifySignature: true/false                                              │
│     ├── signatureKey: 公钥 (或公钥引用)                                          │
│     └── signatureStores: 签名存储列表 (可选)                                     │
│                                                                                 │
│  2. 验证 OCI 镜像签名 (cosign)                                                   │
│     ├── 拉取镜像 digest                                                         │
│     ├── 获取签名 annotation                                                      │
│     ├── 使用公钥验证签名                                                          │
│     └── 验证失败:                                                               │
│         ├── force=true → 设置 VerificationError 条件，继续                        │
│         └── force=false → 返回 ImageVerificationFailed 错误                       │
│                                                                                 │
│  3. 验证 Release Image 内容                                                     │
│     ├── release.yaml 存在                                                        │
│     ├── components/ 目录结构完整                                                │
│     ├── image-references 存在                                                   │
│     └── ComponentVersion CRs 解析成功                                           │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 12.3 签名存储

```go
// pkg/cvo/signature/store.go

type SignatureStore interface {
    Verify(ctx context.Context, imageRef string, digest string) error
    Name() string
}

// 内置存储
type ConfigMapStore struct { ... }    // 从 ConfigMap 读取公钥
type CosignStore struct { ... }       // cosign 公钥验证
type CustomStore struct { ... }       // 自定义签名服务
```

---

## 13. 完整工作流

### 13.1 安装流程 (DeclarativeInstallEnabled=true)

```txt
用户创建 BKECluster (OpenFuyaoVersion: v2.7.0)
  │
  ▼
BKEClusterReconciler
  ├── 创建 ClusterVersion CR (desiredVersion: v2.7.0)
  └── 设置 BKECluster.Status.ClusterStatus = ClusterInitializing
  │
  ▼
BKECVO ClusterVersionReconciler (sync)
  ├── 获取 ClusterVersion + BKECluster
  ├── 解析 desiredUpdate (version → image)
  ├── 确定 Payload State = InitializingPayload
  ├── SyncWorker.Update(ctx, generation, desired, cv, InitializingPayload, ...)
  │   ├── ReleaseStore.ResolveRelease (内存/磁盘/OCI 缓存)
  │   ├── 签名验证 (如果启用)
  │   ├── payload.LoadUpdate (解析 bundle)
  │   ├── Precondition.RunAll (本地 payload 跳过)
  │   └── apply(ctx, work, maxWorkers, previousStatus)
  │       ├── 构建 TaskGraph (FlattenByNumberAndComponent, 全并行)
  │       ├── 第一遍 (PrecreatingMode): 创建 ClusterComponent CR
  │       ├── 第二遍 (InitializingMode):
  │       │   ├── 对每个 Component:
  │       │   │   ├── VersionContext.Decide() → DecisionInstall
  │       │   │   ├── ExecutorRegistry.Get(type).ExecuteComponent()
  │       │   │   ├── 更新 ClusterComponent.status (Available=True, versions 匹配)
  │       │   │   └── 更新 DeclarativeUpgradeStatus.Completed[]
  │       │   └── 健康检查阻塞 (ClusterComponent.status)
  │       └── 错误汇总
  ├── syncStatus (写入 ClusterVersion.status)
  │   ├── Phase = Installed
  │   ├── History = [{from: "", to: "v2.7.0", status: Succeeded}]
  │   └── Conditions[Available] = True
  └── 完成
  │
  ▼
BKEClusterReconciler
  ├── 检测 ClusterVersion Phase = Installed
  └── 设置 BKECluster.Status.ClusterStatus = ClusterReady
```

### 13.2 升级流程

```txt
用户修改 BKECluster.Spec.OpenFuyaoVersion: v2.6.0 → v2.7.0
  │
  ▼
BKEClusterReconciler
  └── 更新 ClusterVersion.Spec.DesiredVersion = "v2.7.0"
  │
  ▼
BKECVO ClusterVersionReconciler (sync)
  ├── 获取 ClusterVersion + BKECluster
  ├── 解析 desiredUpdate
  ├── 前置条件检查 (PreconditionChain)
  │   ├── Rollback: v2.7.0 > v2.6.0 ✓
  │   ├── GiantHop: v2.6→v2.7 单 minor ✓
  │   ├── Upgradeable: ClusterComponent Upgradeable 全 True ✓
  │   ├── RecommendedUpdate: UpgradePath 存在 v2.6→v2.7 ✓
  │   ├── ReleaseImageValid: Phase=Valid ✓
  │   └── SignatureVerified: 签名验证通过 ✓
  ├── 确定 Payload State = UpdatingPayload
  ├── SyncWorker.Update(ctx, generation, desired, cv, UpdatingPayload, ...)
  │   ├── ReleaseStore.ResolveRelease
  │   ├── 签名验证
  │   ├── payload.LoadUpdate
  │   ├── VersionContext 构建 (current + target)
  │   └── apply(ctx, work, maxWorkers, previousStatus)
  │       ├── 构建 TaskGraph (ByNumberAndComponent, 有序)
  │       ├── 第一遍 (PrecreatingMode): 更新 ClusterComponent CR
  │       ├── 第二遍 (UpdatingMode):
  │       │   ├── 对每个 Component (按 Run Level 分层):
  │       │   │   ├── Run Level 05: bkeagent, provider (并行)
  │       │   │   │   ├── VersionContext.Decide() → DecisionUpgrade
  │       │   │   │   ├── ExecutorRegistry.Get(inline).ExecuteComponent()
  │       │   │   │   └── 阻塞直到 ClusterComponent Available + 版本匹配
  │       │   │   ├── Run Level 10-29: etcd, kubernetes (阻塞直到 05 完成)
  │       │   │   ├── Run Level 30: containerd (阻塞直到 10-29 完成)
  │       │   │   ├── Run Level 50: coredns, kube-proxy (阻塞直到 30 完成)
  │       │   │   └── Run Level 60: openfuyao-core (阻塞直到 50 完成)
  │       │   └── 错误处理 (FailFast/Continue/Rollback)
  │       └── 错误汇总
  ├── syncStatus:
  │   ├── Phase = Upgraded (单 hop) 或 Upgrading (多 hop)
  │   ├── History prepended [{from: "v2.6.0", to: "v2.7.0", status: Succeeded}]
  │   └── Conditions[Available] = True, [Progressing] = False
  └── 完成
  │
  ▼
BKEClusterReconciler
  └── 设置 BKECluster.Status.ClusterStatus = ClusterReady
```

### 13.3 断点续传流程

```txt
控制器重启或 reconcile 中断
  │
  ▼
BKECVO ClusterVersionReconciler (sync)
  ├── 获取 ClusterVersion + BKECluster
  ├── 读取 BKECluster.Status.DeclarativeUpgrade
  │   ├── TargetVersion = "v2.7.0" (未变 → 不重置 Completed)
  │   ├── Completed = [{containerd, v1.7.18}, {bkeagent, v2.7.0}]
  │   └── LastError = "kubernetes-master upgrade failed"
  ├── 确定 Payload State = UpdatingPayload (继续升级)
  ├── SyncWorker.Update(...)
  │   └── apply(ctx, work, maxWorkers, previousStatus)
  │       ├── 构建 TaskGraph
  │       └── 对每个 Component:
  │           ├── shouldSkipComponent: DeclarativeUpgrade.IsCompleted(name, version)?
  │           │   ├── containerd: Completed → Skip ★
  │           │   ├── bkeagent: Completed → Skip ★
  │           │   └── kubernetes-master: Not Completed → Execute
  │           └── 未完成的组件继续执行
  └── 完成
```

---

## 14. ClusterVersion 扩展

### 14.1 Status 扩展

```go
// api/v1alpha1/clusterversion_types.go (扩展)

type ClusterVersionStatus struct {
    CurrentVersion string
    Phase          ClusterVersionPhase
    UpgradeHistory []ClusterUpgradeRecord
    Conditions     []ClusterVersionCondition

    // ★ 新增字段
    Desired            Release            // 当前正在应用的 release
    VersionHash        string             // 清单内容哈希 (FNV-64)
    ObservedGeneration int64
    AvailableUpdates   []Release          // 可用更新 (从 UpgradePath)
    ConditionalUpdates []ConditionalUpdate // 条件更新 (含风险评估)
    DeclarativeProgress DeclarativeProgressStatus // DAG 进度
}

type DeclarativeProgressStatus struct {
    Done, Total int
    Completed    []DeclarativeUpgradeComponentRecord  // 复用现有类型
    LastError    string
    LastFailure  *DeclarativeUpgradeFailureRecord
    Attempt      int
}

type ConditionalUpdate struct {
    Release  Release
    Risks    []ConditionalUpdateRisk
    Conditions []metav1.Condition    // type "Recommended"
}

type Release struct {
    Version, Image string
    Channels       []string
}
```

### 14.2 条件类型

| 条件 | 类型 | 含义 |
|------|------|------|
| `Available` | 核心 | 集群版本可用 |
| `Progressing` | 核心 | 正在安装/升级 |
| `Degraded` | 核心 | 版本协调降级 |
| `Upgradeable` | 核心 | 可升级 (风险聚合) |
| `ReleaseAccepted` | 核心 | Release Image 已接受 |
| `RetrievedUpdates` | 核心 | 可用更新已获取 |
| `ClusterVersionInvalid` | 核心 | spec 无效 |

---

## 15. 代码结构设计

### 15.1 新增包结构

```
cluster-api-provider-bke/pkg/cvo/                    # ★ 新增 BKE CVO 核心包
├── cvo.go                                           # Operator 主结构体 + New() + Run() + sync()
├── syncworker.go                                    # SyncWorker 状态机 + apply()
├── status.go                                        # ClusterVersion 状态合成
├── status_history.go                                # 升级历史加权剪枝
├── state.go                                         # State 接口 + 状态机实现
├── metrics.go                                       # Prometheus 指标
├── manifestgraph/
│   ├── graph.go                                     # TaskGraph DAG + RunGraph
│   ├── task.go                                      # Task.Run + UpdateError
│   └── ordering.go                                  # 排序策略 (ByLevel/Flatten/Permute)
├── precondition/
│   ├── precondition.go                              # Precondition 接口
│   ├── rollback.go                                  # 回滚检查
│   ├── gianthop.go                                 # 跨版本跳跃检查
│   ├── upgradeable.go                               # Upgradeable 检查
│   ├── recommended_update.go                        # 推荐更新检查
│   └── signature_verified.go                        # 签名验证检查
├── risk/
│   ├── source.go                                    # Risk Source 接口
│   ├── aggregate.go                                 # 风险聚合
│   ├── clustercomponent_upgradeable.go              # ClusterComponent Upgradeable
│   ├── adminack.go                                  # Admin 确认
│   └── alert.go                                     # Prometheus 告警 (可选)
├── signature/
│   ├── store.go                                     # SignatureStore 接口
│   ├── cosign.go                                    # cosign 验证
│   └── configmap.go                                 # ConfigMap 公钥
└── internal/
    ├── clustercomponent_builder.go                  # ClusterComponent Builder
    └── generic_builder.go                           # 通用 Dynamic Builder
```

### 15.2 与现有代码的关系

| 现有包 | CVO 设计后 | 变化 |
|--------|-----------|------|
| `pkg/topology/` | 保留 (依赖图) | ManifestGraph 内部使用 topology.Graph |
| `pkg/dagexec/` | 保留 (Scheduler) | CVO SyncWorker 内部调用 Scheduler |
| `pkg/upgrade/` | 保留 (VersionContext) | CVO 使用 VersionContext |
| `pkg/manifest/` | 保留 (Store/Applier) | CVO 使用 manifest.Store |
| `pkg/yamlinstaller/` | 保留 | CVO 注册 YamlExecutor |
| `pkg/release/` | 保留 (ReleaseStore) | CVO 扩展签名验证 |
| `pkg/phaseframe/` | 逐步废弃 | Feature Gate 控制迁移 |
| `controllers/clusterversion/` | 重构为 CVO | 升级为独立控制器 |

### 15.3 控制器注册

```go
// controllers/cvo/cvo_controller.go

type BKECVOReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    Operator *cvo.Operator
}

func (r *BKECVOReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&v1alpha1.ClusterVersion{}).
        Watches(&v1alpha1.ClusterComponent{}, handler.EnqueueRequestsFromMapFunc(...)).
        Complete(r)
}
```

---

## 16. 迁移策略

### 16.1 分阶段迁移

| 阶段 | 版本 | 内容 | 风险 | 回滚方案 |
|------|------|------|------|---------|
| **Phase 1** | v2.7.0 | 新增 ClusterComponent CRD + CVO 控制器框架 (仅观测，不执行) | 低 | 不启用 Feature Gate |
| **Phase 2** | v2.8.0 | 实现 ManifestGraph + Precondition + SyncWorker，升级走 CVO | 中 | 关闭 FeatureGate，回退到现有 DAG |
| **Phase 3** | v2.9.0 | 实现 DeclarativeInstall，安装走 CVO | 中 | 关闭 FeatureGate，回退到 PhaseFlow |
| **Phase 4** | v3.0.0 | 实现 Risk 聚合 + 签名验证 + 状态机 | 低 | 独立功能，可分别关闭 |
| **Phase 5** | v3.1.0 | 移除 PhaseFlow，CVO 为唯一路径 | 高 | 保留旧代码分支 |

### 16.2 Feature Gate

```go
const (
    CVOEnabled              = "CVOEnabled"               // CVO 控制器启用
    ManifestGraphEnabled    = "ManifestGraphEnabled"     // 分层 DAG 启用
    DeclarativeInstallEnabled = "DeclarativeInstallEnabled" // 安装走 DAG
    ClusterComponentEnabled = "ClusterComponentEnabled"  // ClusterComponent CRD 启用
    PreconditionEnabled     = "PreconditionEnabled"      // 前置条件检查启用
    SignatureVerificationEnabled = "SignatureVerificationEnabled" // 签名验证启用
    RiskAggregationEnabled  = "RiskAggregationEnabled"   // 风险聚合启用
)

var defaultFeatureGates = map[string]bool{
    CVOEnabled:                  false,
    ManifestGraphEnabled:        false,
    DeclarativeInstallEnabled:   false,
    ClusterComponentEnabled:     false,
    PreconditionEnabled:         false,
    SignatureVerificationEnabled: false,
    RiskAggregationEnabled:      false,
}
```

### 16.3 向后兼容

```go
// 兼容层：同时支持 PhaseFlow 和 CVO
func (r *BKEClusterReconciler) executeUpgrade(ctx context.Context, ...) error {
    if featuregate.Enabled(CVOEnabled) {
        return r.delegateToCVO(ctx, ...)  // 委托给 CVO
    }
    return r.executeUpgradeDAG(ctx, ...)  // 现有 DAG 路径
}

func (r *BKEClusterReconciler) executeInstall(ctx context.Context, ...) error {
    if featuregate.Enabled(DeclarativeInstallEnabled) {
        return r.delegateInstallToCVO(ctx, ...)  // 委托给 CVO
    }
    return r.executePhaseFlow(ctx, ...)  // 现有 PhaseFlow
}
```

---

## 17. 测试策略

### 17.1 单元测试

| 测试模块 | 测试场景 | 覆盖目标 |
|---------|---------|---------|
| ManifestGraph | TaskGraph 构建/分裂/并行化/RunGraph 执行 | >90% |
| SyncWorker | 状态机转换/apply 流程/backoff/重试 | >85% |
| Precondition | Rollback/GiantHop/Upgradeable/SignatureVerified | >90% |
| Risk | 风险源/聚合/回调 | >85% |
| Signature | cosign 验证/ConfigMap 公钥/force 跳过 | >85% |
| State | 状态机转换/CanTransitionTo | >90% |
| StatusReporter | 条件合成/历史剪枝/进度消息 | >85% |

### 17.2 集成测试

| 场景 | 验证内容 |
|------|---------|
| 全新安装 (CVO) | DAG 全并行，ClusterComponent 创建，版本匹配 |
| 升级 (CVO) | Run Level 分层阻塞，断点续传，ClusterComponent 状态门控 |
| 前置条件阻止 | Rollback/GiantHop 被阻止，force 跳过 |
| 签名验证 | 验证失败被阻止，force 跳过 |
| 断点续传 | 中断后恢复，已完成组件跳过 |
| 失败处理 | FailFast 停止 / Continue 继续 / Rollback 回滚 |
| 双轨兼容 | FeatureGate 关闭时回退到 PhaseFlow/现有 DAG |

### 17.3 E2E 测试

| 场景 | 规模 | 验证内容 |
|------|------|---------|
| 小规模安装 | 1M+2W | CVO 安装完整流程 |
| 升级 | 3M+5W | v2.6→v2.7 分层升级 |
| 断点续传 | 3M+3W | 中断后恢复 |
| 大规模安装 | 3M+97W | 100 节点性能 |

---

## 18. 工作量评估

| 任务 | 预估工时 | 依赖 |
|------|---------|------|
| ClusterComponent CRD + 控制器 | 5 人日 | 无 |
| ManifestGraph (分层 DAG) | 5 人日 | topology |
| SyncWorker 状态机 | 5 人日 | ManifestGraph |
| Precondition 链 | 3 人日 | 无 |
| Risk 聚合树 | 3 人日 | ClusterComponent |
| 签名验证 | 3 人日 | ReleaseStore |
| ClusterVersion 状态机 | 3 人日 | 无 |
| Status 合成 + 历史剪枝 | 3 人日 | 无 |
| CVO 控制器集成 | 5 人日 | 上述全部 |
| Install DAG 统一 | 3 人日 | CVO 控制器 |
| Feature Gate + 兼容层 | 3 人日 | CVO 控制器 |
| 单元测试 | 8 人日 | 核心实现 |
| 集成测试 | 5 人日 | 单元测试 |
| E2E 测试 | 5 人日 | 集成测试 |
| 文档 | 3 人日 | 无 |
| **总计** | **62 人日 (约 3 人月)** | |

---

## 19. 设计思路总结

### 19.1 BKE CVO 核心设计思路

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              BKE CVO 核心设计思路 (借鉴 OpenShift CVO)                          │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. Release Image 驱动                                                          │
│     • ReleaseImage OCI Bundle = 集群期望状态的唯一真相源                        │
│     • ComponentVersion.spec.runLevel 定义分层顺序                                │
│     • 不可变 Release Image: 版本变更换新 bundle                                  │
│                                                                                 │
│  2. 三级层次管理                                                                 │
│     • BKECVO 管理 ClusterComponents (更新清单)                                  │
│     • ClusterComponents 管理 Operands (组件)                                    │
│     • BKECVO 不直接管理组件，通过 ClusterComponent 状态门控间接协调              │
│                                                                                 │
│  3. 状态门控协调                                                                 │
│     • BKECVO 创建 ClusterComponent CR (预创建模式)                               │
│     • 组件执行器更新 ClusterComponent.status                                     │
│     • BKECVO 阻塞直到 Available + 版本匹配 + 非 Degraded                        │
│     • 超时语义: 30min 抑制 → 40min 失败                                         │
│                                                                                 │
│  4. 清单图分层并行                                                               │
│     • 按 RunLevel 分层阻塞 (跨层阻塞，同层并行)                                  │
│     • 三种模式: Initializing(全并行) / Updating(有序) / Reconciling(随机)      │
│     • DAG: 节点错误 → 依赖节点跳过，独立边继续                                  │
│                                                                                 │
│  5. 声明式期望状态                                                               │
│     • 用户通过 ClusterVersion.spec.desiredVersion 声明目标                       │
│     • BKECVO 持续协调到目标态                                                   │
│     • UpgradePath 推荐安全升级路径                                              │
│                                                                                 │
│  6. 仅向前升级                                                                   │
│     • 不支持自动回滚 (Precondition Rollback 阻止)                                │
│     • N-1 兼容性是强制要求                                                      │
│     • 组件必须处理混合版本状态                                                   │
│                                                                                 │
│  7. 断点续传                                                                     │
│     • DeclarativeUpgradeStatus.Completed[] 持久化                                │
│     • 跨重启恢复，已完成组件跳过                                                 │
│                                                                                 │
│  8. 风险感知升级                                                                 │
│     • 多风险源聚合 (ClusterComponent Upgradeable, AdminAck, Alert)             │
│     • 条件更新评估 (Recommended 条件)                                            │
│     • 风险可被接受 (AcceptRisks) 或强制 (force)                                 │
│                                                                                 │
│  9. 签名验证                                                                     │
│     • BKECVO 验证 ReleaseImage 签名 (cosign)                                    │
│     • 多签名存储 (ConfigMap + 自定义)                                           │
│     • force 可跳过验证 (转为已接受风险)                                          │
│                                                                                 │
│  10. 正式状态机                                                                  │
│      • State 接口 (Name/Enter/Execute/Exit/CanTransitionTo)                    │
│      • ClusterVersionPhase 状态机 (Pending→PreChecking→Installing→Installed→   │
│        Upgrading→Upgraded/Failed→Ready)                                         │
│      • SyncWorker 状态机 (Initial→Sync→Reconciling/Error)                      │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 19.2 与 OpenShift CVO 的关键差异

| 维度 | OpenShift CVO | BKE CVO |
|------|--------------|---------|
| 运行位置 | 目标集群 (被管理集群) | 管理集群 (管理多个目标集群) |
| Payload 格式 | Release Payload Image (文件名序号) | ReleaseImage OCI Bundle (component.yaml runLevel) |
| 状态门控 CR | ClusterOperator | ClusterComponent |
| 升级推荐服务 | OSUS (Cincinnati 动态图) | UpgradePath (静态图，后续可扩展) |
| 抽包方式 | Pod-based (init containers + hostPath) | OCI Registry 直接拉取 (ReleaseStore) |
| 控制器框架 | 自定义 QueueInformer | controller-runtime (Manager) |
| 状态机 | 隐式 (Phase 字符串) | 正式 State 接口 |
| 执行器 | Resource Builder (GVK switch) | ExecutorRegistry (ComponentType 分发) |
| 自身升级 | 普通 Pod 被 MCO drain | CVO 在管理集群，不受目标集群影响 |

---

## 20. 附录

### 20.1 参考文档

| 文档 | 说明 |
|------|------|
| KEP-5 | ClusterVersion/ReleaseImage/UpgradePath 声明式集群版本升级 |
| KEP-6 | ComponentVersion Binary/Helm/YAML 组件声明式管理 |
| KEP-7 OLM 架构分析 | OpenShift OLM 核心架构梳理 |
| KEP-7 CVO 架构分析 | OpenShift CVO 核心架构梳理 |
| OpenShift CVO 代码库 | github.com/openshift/cluster-version-operator |
| OpenShift Enhancements | CVO 协调/更新工作流/ClusterOperator 设计文档 |

### 20.2 术语表

| 术语 | 定义 |
|------|------|
| **BKECVO** | BKE Cluster Version Operator，BKE 集群级版本协调器 |
| **ClusterComponent** | BKE 组件状态报告 CRD (借鉴 OpenShift ClusterOperator) |
| **ManifestGraph** | 分层 DAG (按 RunLevel 分层阻塞) |
| **RunLevel** | ComponentVersion.spec.runLevel，定义组件协调顺序 |
| **SyncWorker** | 同步工作器，管理 payload 加载/验证/应用的状态机 |
| **Precondition** | 升级前置条件检查 (Rollback/GiantHop/Upgradeable/RecommendedUpdate) |
| **RiskSource** | 风险源 (ClusterComponentUpgradeable/AdminAck/Alert) |
| **PayloadState** | Payload 状态 (Updating/Reconciling/Initializing/Precreating) |
| **DeclarativeUpgradeStatus** | 断点续传状态 (已有，CVO 复用) |
| **VersionContext** | 版本上下文 (current + target，已有，CVO 复用) |
| **Forward-Only** | 升级仅向前，不支持自动回滚 |
| **N-1 Compatibility** | 组件必须与上一版本兼容 |

---

**文档版本**: v1.0
**维护者**: openFuyao Team

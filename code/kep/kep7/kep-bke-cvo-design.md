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

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    ClusterComponent 生命周期                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  CVO 视角:                                                                      │
│                                                                                 │
│  1. Manifest Graph 预创建阶段 (PrecreatingMode)                                  │
│     CVO 创建 ClusterComponent CR (spec.targetVersion, spec.componentType)      │
│     设置 status.phase = Pending, status.conditions[Available] = Unknown         │
│     → 提供 must-gather 可见性                                                    │
│                                                                                 │
│  2. 执行阶段 (UpdatingMode / InitializingMode)                                   │
│     CVO 调用 ExecutorRegistry 执行组件                                           │
│     执行器负责更新 ClusterComponent.status:                                     │
│     ├── status.phase = Installing / Upgrading                                   │
│     ├── status.conditions[Progressing] = True                                   │
│     └── 执行成功后:                                                              │
│         ├── status.phase = Installed                                             │
│         ├── status.currentVersion = targetVersion                                │
│         ├── status.versions = [{component, targetVersion}]                       │
│         ├── status.conditions[Available] = True                                 │
│         ├── status.conditions[Progressing] = False                              │
│         └── status.conditions[Degraded] = False                                 │
│                                                                                 │
│  3. 健康检查阻塞阶段 (HealthCheck)                                                │
│     CVO 检查 ClusterComponent status:                                           │
│     ├── Available == True?                                                      │
│     ├── versions 包含 Release Image 声明的版本?                                   │
│     ├── Degraded == False?                                                      │
│     └── Progressing == False?                                                   │
│     阻塞直到全部满足 (或超时)                                                     │
│                                                                                 │
│  4. 失败处理                                                                     │
│     执行器设置:                                                                  │
│     ├── status.phase = Failed                                                   │
│     ├── status.conditions[Degraded] = True                                     │
│     └── status.conditions[Available] = False (如果组件不可用)                   │
│     CVO 按 FailurePolicy 处理 (FailFast/Continue/Rollback)                      │
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

# KEP-21: 基于 Operator Framework 的 Operator 开发与管理设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-21 |
| **标题** | 基于 Operator Framework 的 Operator 开发与管理设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-16 |
| **依赖** | KEP-5 声明式升级框架、KEP-6 组件版本管理、Cluster API v1beta1 |
| **参考** | Operator Framework (operatorframework.io)、OLM v0/v1、OpenShift CVO 架构分析 (`cvo-architecture-analysis.md`)、OLM 架构分析 (`olm-architecture-analysis.md`)、BKE CVO 设计 (`kep-bke-cvo-design.md`) |

---

## 目录

1. [摘要](#1-摘要)
2. [动机](#2-动机)
3. [设计目标与约束](#3-设计目标与约束)
4. [架构设计](#4-架构设计)
5. [Component Operator CRD 设计](#5-component-operator-crd-设计)
6. [Operator Catalog 设计](#6-operator-catalog-设计)
7. [Operator 生命周期管理](#7-operator-生命周期管理)
8. [与现有架构的集成](#8-与现有架构的集成)
9. [Operator 开发规范](#9-operator-开发规范)
10. [迁移方案](#10-迁移方案)
11. [可观测性](#11-可观测性)
12. [工作量评估](#12-工作量评估)
13. [风险与缓解措施](#13-风险与缓解措施)
- [附录](#附录)

---

## 1. 摘要

本提案设计 BKE 平台基于 Operator Framework 的 Operator 开发与管理体系。当前 BKE 通过 Command CR → bkeagent → 本地 os/exec 的方式部署节点级组件（containerd、kubelet、etcd、certs 等），集群级组件（coredns、kube-proxy）通过 YAML manifest apply 部署。这种模式存在以下局限：组件生命周期管理分散、缺少标准化的 Operator 打包/分发/升级机制、无法利用 OLM 生态的依赖解析和自动更新能力。

本提案引入 Operator Framework 的核心理念（不直接引入 OLM 二进制依赖），设计 BKE 自有的 Component Operator 体系：每个组件封装为独立的 Operator（CRD + Controller + 打包格式），通过 Operator Catalog（对应 ReleaseImage）声明组件版本和依赖关系，由 BKE 的 DAG 调度器（现有）协调安装/升级顺序。与 OpenShift CVO/OLM 的关键差异是：BKE 不部署 OLM 二进制，而是复用现有的 DAG 调度器 + ComponentVersion CRD 体系，将 Operator 的生命周期管理（安装/升级/卸载/状态报告）集成到 BKE 的声明式升级框架中。

---

## 2. 动机

### 2.1 现状问题

| 问题 | 现状 | 影响 |
|------|------|------|
| **组件生命周期分散** | 节点级组件通过 Command CR + bkeagent 插件执行，集群级组件通过 YAML apply 执行，无统一的 Operator 抽象 | 新增组件需开发 bkeagent 插件或 YAML 清单，无标准开发框架 |
| **无标准化打包** | ComponentVersion CR 定义组件元数据，但无 Operator 级别的打包格式（含 CRD + Controller + RBAC + 配置） | 组件分发不标准化，无法版本化管理 Operator 本身 |
| **无依赖解析** | DAG 调度器通过 ComponentVersion.spec.dependencies 声明依赖，但无 OLM SAT 求解器级别的自动依赖解析 | 复杂依赖场景需手动维护依赖关系 |
| **无自动更新** | 升级通过 ClusterVersion desiredVersion 变更触发，无 Subscription 式的自动更新机制 | 用户需手动指定每次升级目标版本 |
| **状态报告不标准** | ClusterComponentStatuses 是 map 结构，无 ClusterOperator 式的标准条件（Available/Progressing/Degraded） | 外部监控系统无法标准化消费组件状态 |
| **bkeagent 插件耦合** | 节点级组件逻辑（containerd/kubelet/certs/env）硬编码在 bkeagent 的 builtin 插件中 | 新增节点级组件需修改 bkeagent 代码并重新部署 |

### 2.2 与 CVO/OLM 架构分析的关系

BKE 已有完整的 OLM 和 CVO 架构分析文档（`olm-architecture-analysis.md`、`cvo-architecture-analysis.md`），以及 CVO 风格的设计提案（`kep-bke-cvo-design.md`）。本提案与 CVO 方案的关键区别：

| 维度 | CVO 方案 (kep-bke-cvo-design) | Operator Framework 方案 (本提案) |
|------|------------------------------|-------------------------------|
| **组件管理** | 集中式 CVO 协调所有组件 | 每个组件是独立 Operator，自主管理 |
| **打包格式** | ReleaseImage OCI bundle | Operator Bundle（CRD + Controller + RBAC + 配置） |
| **分发** | ReleaseImage CR + OCI pull | Operator Catalog + Bundle 镜像 |
| **依赖解析** | DAG 拓扑排序（ComponentVersion.spec.dependencies） | DAG 拓扑排序 + OLM SAT 求解器理念 |
| **状态报告** | ClusterComponent CRD | ComponentOperator CRD（Available/Progressing/Degraded） |
| **节点级组件** | Command CR → bkeagent | DaemonSet Operator 或 bkeagent Operator 模式 |

### 2.3 设计选择：不引入 OLM 二进制

本提案**不引入 OLM 二进制依赖**（olm-operator / catalog-operator），理由：

1. **BKE 已有 DAG 调度器**：DAG 拓扑排序 + VersionContext 版本决策 + DeclarativeUpgradeStatus 断点续传已满足组件协调需求，无需 OLM 的 InstallPlan + SAT 求解器
2. **BKE 已有 ReleaseImage 体系**：ReleaseImage OCI bundle 已包含组件清单和依赖关系，无需 OLM 的 CatalogSource + Subscription + File-Based Catalog
3. **OLM 适用于通用 Operator 生态**：OLM 设计目标是管理第三方 Operator（如 Prometheus Operator、Cert Manager），BKE 的组件是自有的、版本绑定的，不需要通用 Operator 目录
4. **引入 OLM 增加复杂度**：OLM 的 6 个 CRD + 双 Operator 架构 + gRPC Catalog 对 BKE 场景过度设计

**本提案借鉴 Operator Framework 的设计理念（打包格式、生命周期管理、状态报告），在 BKE 现有架构上实现**。

---

## 3. 设计目标与约束

### 3.1 设计目标

1. **标准化 Operator 打包**：定义 BKE Operator Bundle 格式（CRD + Controller + RBAC + 配置），支持版本化管理
2. **标准化生命周期管理**：定义 Operator 的安装/升级/卸载/状态报告流程
3. **标准化状态报告**：引入 ComponentOperator CRD，提供 Available/Progressing/Degraded/Upgradeable 条件
4. **复用现有 DAG 调度器**：不引入 OLM，通过 DAG 调度器协调 Operator 安装/升级顺序
5. **复用现有 ReleaseImage 体系**：Operator Bundle 打包在 ReleaseImage OCI 制品中
6. **渐进迁移**：现有 bkeagent 插件和 YAML 清单可逐步迁移为 Operator

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **不引入 OLM 二进制** | 不部署 olm-operator / catalog-operator，复用 BKE DAG 调度器 |
| **向后兼容** | 现有 ComponentVersion CRD 和 DAG 调度器不变，Operator 是新增层 |
| **离线环境** | Operator Bundle 打包在 ReleaseImage OCI 中，支持断网安装 |
| **多架构** | 支持 amd64 和 arm64 |
| **CAPI 兼容** | 不破坏 BKECluster/BKEMachine 与 Cluster API 的集成 |

### 3.3 非目标

1. 不实现 OLM 的 Subscription 自动更新机制（BKE 通过 ClusterVersion desiredVersion 触发升级）
2. 不实现 OLM 的 File-Based Catalog（BKE 使用 ReleaseImage 作为 Catalog）
3. 不实现 OLM 的 OperatorGroup 多租户（BKE 组件在集群级部署，无多租户需求）
4. 不引入 operator-sdk CLI 工具（BKE 使用 kubebuilder + controller-runtime）

---

## 4. 架构设计

### 4.1 整体架构

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              BKE Operator Framework 架构                                          │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  ReleaseImage OCI Bundle (现有, 扩展)                                    │   │
│  │  ├── release.yaml          (ReleaseImage CR)                             │   │
│  │  ├── components/           (ComponentVersion CRs)                        │   │
│  │  │   ├── containerd-v1.7.18/component.yaml                              │   │
│  │  │   ├── kubelet-v1.29.0/component.yaml                                 │   │
│  │  │   ├── coredns-v1.11.1/component.yaml                                 │   │
│  │  │   └── ...                                                            │   │
│  │  ├── manifests/            (YAML 清单, 现有)                             │   │
│  │  ├── operator-bundles/     (Operator Bundle, 新增) ★                    │   │
│  │  │   ├── containerd-operator/                                           │   │
│  │  │   │   ├── containerd-operator.yaml (Bundle 定义)                     │   │
│  │  │   │   ├── crds/                   (CRD 定义)                          │   │
│  │  │   │   ├── controller.yaml         (Deployment 定义)                   │   │
│  │  │   │   ├── rbac/                   (Role/ClusterRole/Binding)         │   │
│  │  │   │   └── config/                 (ConfigMap/Secret 模板)            │   │
│  │  │   ├── coredns-operator/                                              │   │
│  │  │   └── ...                                                            │   │
│  │  └── files/                (二进制制品, 现有)                             │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  BKE DAG 调度器 (现有, 复用)                                             │   │
│  │  ├── BuildDAGFromBundle → 拓扑排序                                      │   │
│  │  ├── Scheduler.ExecuteDAG → 并行执行                                    │   │
│  │  ├── VersionContext → 版本决策                                           │   │
│  │  └── DeclarativeUpgradeStatus → 断点续传                                 │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  Component Operator 执行层 (新增)                                        │   │
│  │                                                                         │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                  │   │
│  │  │ Containerd   │  │ CoreDNS      │  │ Kubelet      │                  │   │
│  │  │ Operator     │  │ Operator     │  │ Operator     │                  │   │
│  │  │              │  │              │  │              │                  │   │
│  │  │ CRD:         │  │ CRD:         │  │ CRD:         │                  │   │
│  │  │ Containerd   │  │ CoreDNS      │  │ Kubelet      │                  │   │
│  │  │              │  │              │  │              │                  │   │
│  │  │ Controller:  │  │ Controller:  │  │ Controller:  │                  │   │
│  │  │ DaemonSet    │  │ Deployment   │  │ DaemonSet    │                  │   │
│  │  │ 管理          │  │ 管理          │  │ 管理          │                  │   │
│  │  └──────────────┘  └──────────────┘  └──────────────┘                  │   │
│  │                                                                         │   │
│  │  状态报告: ComponentOperator CR (Available/Progressing/Degraded)        │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 与现有架构的关系

```txt
现有架构:
  ReleaseImage → ComponentVersion CR → DAG 调度器 → Executor (Inline/YAML/Helm/Binary)
                                                         ↓
                                              Command CR → bkeagent → os/exec
                                              kubectl apply YAML

新增 Operator 层:
  ReleaseImage → ComponentVersion CR → DAG 调度器 → OperatorExecutor (新增)
                                                         ↓
                                              创建/更新 Operator Deployment
                                              创建/更新 Operator CRD
                                              创建/更新 Operator RBAC
                                              等待 ComponentOperator CR 状态
```

### 4.3 两种 Operator 模式

| 模式 | 适用组件 | 执行方式 | 示例 |
|------|---------|---------|------|
| **In-cluster Operator** | 集群级组件 | Operator 作为 Deployment 运行在目标集群，管理 CRD 资源 | coredns、kube-proxy、calico |
| **Node-level Operator** | 节点级组件 | Operator 作为 DaemonSet 运行在目标集群，或通过 bkeagent Operator 模式执行 | containerd、kubelet、bkeagent |

**In-cluster Operator**：Operator Deployment + CRD 部署到目标集群，Operator Controller 监听 CR 变化并执行操作（如创建 coredns Deployment/Service）。状态通过 ComponentOperator CR 报告。

**Node-level Operator**：两种子模式：
- **DaemonSet 模式**：Operator 作为 DaemonSet 运行在每个节点上，直接管理节点级组件（如安装 containerd 二进制、配置 systemd 服务）
- **bkeagent Operator 模式**：bkeagent 本身升级为 Operator，内置 Operator Controller 逻辑，通过 CRD 声明式管理节点级组件（替代当前的 Command CR 模式）

---

## 5. ComponentOperator CRD 设计

### 5.1 CRD 定义

```go
// api/v1alpha1/componentoperator_types.go

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=compop

// ComponentOperator reports the lifecycle status of a component operator.
// Inspired by OpenShift ClusterOperator, adapted for BKE's DAG-based orchestration.
type ComponentOperator struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   ComponentOperatorSpec   `json:"spec,omitempty"`
    Status ComponentOperatorStatus `json:"status,omitempty"`
}

type ComponentOperatorSpec struct {
    // ComponentName is the component name (matches ComponentVersion.Name).
    // +required
    ComponentName string `json:"componentName"`

    // Version is the target version of the component operator.
    // +optional
    Version string `json:"version,omitempty"`

    // Type is the operator execution mode.
    // +kubebuilder:validation:Enum=InCluster;NodeDaemonSet;BkeAgentOperator
    // +optional
    Type string `json:"type,omitempty"`

    // BundleRef references the Operator Bundle in the ReleaseImage.
    // +optional
    BundleRef *OperatorBundleRef `json:"bundleRef,omitempty"`
}

type OperatorBundleRef struct {
    // Name is the bundle directory name in the ReleaseImage.
    Name string `json:"name"`
    // Version is the bundle version.
    Version string `json:"version"`
}

type ComponentOperatorStatus struct {
    // Conditions follows the OpenShift ClusterOperator pattern.
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty"`

    // Versions reports the actual running versions.
    // +optional
    Versions map[string]string `json:"versions,omitempty"`

    // RelatedObjects references objects managed by this operator.
    // +optional
    RelatedObjects []ObjectReference `json:"relatedObjects,omitempty"`
}

// Standard condition types (following OpenShift ClusterOperator convention)
const (
    ConditionAvailable    = "Available"
    ConditionProgressing  = "Progressing"
    ConditionDegraded     = "Degraded"
    ConditionUpgradeable  = "Upgradeable"
)
```

### 5.2 条件语义

| 条件 | True | False | Unknown |
|------|------|-------|---------|
| **Available** | 组件功能正常，可提供服务 | 组件不可用，无法提供服务 | 未启动或正在初始化 |
| **Progressing** | 组件正在安装/升级中 | 组件已达到目标版本，无操作进行中 | — |
| **Degraded** | 组件功能部分受损或降级 | 组件功能完整 | — |
| **Upgradeable** | 组件可以安全升级 | 组件阻止集群升级（如正在维护） | — |

### 5.3 与 ClusterComponentStatuses 的关系

现有 `BKECluster.Status.ClusterComponentStatuses` 是 map 结构，存储组件级安装状态。ComponentOperator CRD 是**升级版**——独立的 CR 可被外部系统（监控、运维面板）直接 List/Watch，不需要通过 BKECluster.Status 间接访问。

迁移策略：ComponentOperator CR 创建后，BKEClusterReconciler 从 `ClusterComponentStatuses` 读取状态并同步到 ComponentOperator CR；后续逐步由 Operator Controller 直接写入 ComponentOperator CR。

---

## 6. Operator Catalog 设计

### 6.1 Operator Bundle 格式

每个 Operator 打包为 Bundle 目录，包含在 ReleaseImage OCI 制品中：

```txt
operator-bundles/
  containerd-operator/
    containerd-operator.yaml    ← Bundle 元数据定义
    crds/
      containerd-config-crd.yaml    ← CRD: ContainerdConfig
    controller.yaml            ← Deployment: containerd-operator-controller
    rbac/
      role.yaml                ← Role/ClusterRole
      role-binding.yaml        ← RoleBinding/ClusterRoleBinding
    config/
      containerd-config-template.yaml  ← ConfigMap 模板
```

### 6.2 Bundle 元数据定义

```go
// api/v1alpha1/operatorbundle_types.go (内存类型, 不注册为 CRD)

// OperatorBundle defines the metadata and contents of an operator bundle.
type OperatorBundle struct {
    // Name is the bundle name (e.g., "containerd-operator").
    Name string `json:"name"`

    // Version is the bundle version (e.g., "v1.7.18").
    Version string `json:"version"`

    // ComponentName is the component name (matches ComponentVersion.Name).
    ComponentName string `json:"componentName"`

    // Type is the operator execution mode.
    Type string `json:"type"` // InCluster | NodeDaemonSet | BkeAgentOperator

    // Dependencies lists other operator bundles this bundle depends on.
    // Used by DAG scheduler for topological ordering.
    Dependencies []string `json:"dependencies,omitempty"`

    // CRDs lists CRD definitions included in this bundle.
    CRDs []string `json:"crds,omitempty"`

    // Controller is the Deployment manifest for the operator controller.
    Controller string `json:"controller"` // path to controller.yaml

    // RBAC lists RBAC manifest paths.
    RBAC []string `json:"rbac,omitempty"`

    // Config lists configuration manifest paths.
    Config []string `json:"config,omitempty"`

    // Replaces is the previous bundle version this one replaces.
    // (Inspired by OLM CSV replaces field, for update graph.)
    Replaces string `json:"replaces,omitempty"`
}
```

### 6.3 Bundle YAML 示例

```yaml
# operator-bundles/containerd-operator/containerd-operator.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: OperatorBundle
metadata:
  name: containerd-operator-v1.7.18
spec:
  name: containerd-operator
  version: v1.7.18
  componentName: containerd
  type: NodeDaemonSet
  dependencies:
    - bkeagent-operator
  crds:
    - crds/containerd-config-crd.yaml
  controller: controller.yaml
  rbac:
    - rbac/role.yaml
    - rbac/role-binding.yaml
  config:
    - config/containerd-config-template.yaml
  replaces: containerd-operator-v1.7.17
```

### 6.4 与 ReleaseImage 的关系

Operator Bundle 打包在 ReleaseImage OCI 制品中，与现有的 ComponentVersion 和 manifest 文件并列：

```yaml
# ReleaseImage CR (扩展)
apiVersion: config.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.8.0
spec:
  version: v2.8.0
  install:
    components:
      - name: bkeagent
        version: v2.8.0
      - name: containerd
        version: v1.7.18
      - name: coredns
        version: v1.11.1
      # ...
  upgrade:
    components:
      - name: containerd
        version: v1.7.18
      # ...
  # ★ 新增: Operator Bundles 声明
  operatorBundles:
    - name: containerd-operator
      version: v1.7.18
      path: operator-bundles/containerd-operator
    - name: coredns-operator
      version: v1.11.1
      path: operator-bundles/coredns-operator
```

---

## 7. Operator 生命周期管理

### 7.1 安装流程

```txt
DAG 调度器执行到 containerd 组件:
  → OperatorExecutor.ExecuteComponent(node, execCtx)
    1. 从 ReleaseImage bundle 加载 OperatorBundle 元数据
    2. 解析 Bundle 目录中的 YAML 清单 (CRD/Controller/RBAC/Config)
    3. 在目标集群中 Apply 资源:
       a. Apply CRD (containerd-config-crd.yaml)
       b. Apply RBAC (role.yaml + role-binding.yaml)
       c. Apply Controller (controller.yaml → Deployment/DaemonSet)
       d. Apply Config (configmap template)
    4. 等待 Controller Pod 就绪 (健康检查)
    5. 创建/更新 ComponentOperator CR (设置 target version)
    6. 等待 ComponentOperator CR Conditions:
       - Available == True
       - Progressing == False
       - Degraded == False
    7. 标记组件安装完成 (DeclarativeUpgradeStatus.Completed)
```

### 7.2 升级流程

```txt
DAG 调度器执行到 containerd 组件升级:
  → OperatorExecutor.ExecuteComponent(node, execCtx)
    1. 从新版本 ReleaseImage bundle 加载新 OperatorBundle
    2. 比较新旧 Bundle 版本:
       - Replaces 字段验证 (新版本 replaces == 旧版本)
    3. Apply 更新的资源:
       a. Apply 新 CRD (如果有变更)
       b. Apply 新 RBAC (如果有变更)
       c. Update Controller (滚动更新 Deployment/DaemonSet 镜像)
       d. Apply 新 Config (如果有变更)
    4. 更新 ComponentOperator CR (设置新 target version)
    5. 等待 Controller 滚动更新完成
    6. 等待 ComponentOperator CR Conditions:
       - Available == True (新版本就绪)
       - Progressing == False
       - Degraded == False
    7. 标记组件升级完成
```

### 7.3 卸载流程

```txt
DAG 调度器执行到 containerd 组件卸载 (逆序):
  → OperatorExecutor.UninstallComponent(node, execCtx)
    1. 更新 ComponentOperator CR (标记正在卸载)
    2. 删除 Controller (Deployment/DaemonSet)
    3. 删除 CRD (级联删除所有 CR 实例)
    4. 删除 RBAC (Role/ClusterRole/Binding)
    5. 删除 Config (ConfigMap/Secret)
    6. 删除 ComponentOperator CR
```

### 7.4 状态门控

Operator 安装/升级过程中，DAG 调度器通过 ComponentOperator CR 的 Conditions 进行状态门控：

| 门控点 | 检查条件 | 阻塞行为 |
|--------|---------|---------|
| **安装前** | 无（首次安装） | — |
| **Controller 就绪** | Deployment/DaemonSet Pod Ready | 等待超时则标记 Failed |
| **组件就绪** | Available==True && Progressing==False | 阻塞 DAG 后续组件（依赖门控） |
| **升级前** | Upgradeable==True | 如果 Upgradeable==False，阻塞升级 |
| **升级后** | Available==True && Version==target | 确认升级成功 |
| **降级检测** | Degraded==True | 记录告警，不阻塞（允许降级运行） |

---

## 8. 与现有架构的集成

### 8.1 新增 OperatorExecutor

在 DAG 调度器的 ExecutorRegistry 中新增 `OperatorExecutor`，与现有的 Inline/YAML/Helm/Binary Executor 并列：

```go
// pkg/dagexec/operator_executor.go

// OperatorExecutor deploys and manages component operators.
// It applies Operator Bundle manifests (CRD/Controller/RBAC/Config) to the target
// cluster and waits for ComponentOperator CR status conditions.
type OperatorExecutor struct {
    client        client.Client       // 目标集群 client
    bundleStore   manifest.Store      // ReleaseImage bundle
    cvStore       ComponentVersionStore
    statusUpdater ComponentStatusUpdater
    log           *bkev1beta1.BKELogger
}

func (e *OperatorExecutor) GetComponentType() ComponentType {
    return ComponentTypeOperator
}

func (e *OperatorExecutor) ExecuteComponent(
    ctx context.Context,
    node *topology.ComponentNode,
    execCtx *ExecutionContext,
) error {
    // 1. 从 Bundle 加载 OperatorBundle 元数据
    bundle, err := e.loadOperatorBundle(node.Name, node.Version)

    // 2. Apply CRD + RBAC + Controller + Config
    if err := e.applyBundleResources(ctx, bundle, execCtx); err != nil {
        return fmt.Errorf("apply operator bundle: %w", err)
    }

    // 3. 创建/更新 ComponentOperator CR
    if err := e.ensureComponentOperatorCR(ctx, node, execCtx); err != nil {
        return fmt.Errorf("ensure ComponentOperator CR: %w", err)
    }

    // 4. 等待状态条件 (Available + !Progressing + !Degraded)
    return e.waitForOperatorReady(ctx, node, execCtx)
}
```

### 8.2 新增 ComponentType

```go
// api/v1alpha1/componentversion_types.go — 新增组件类型

const (
    ComponentTypeYAML     ComponentType = "yaml"
    ComponentTypeHelm     ComponentType = "helm"
    ComponentTypeInline   ComponentType = "inline"
    ComponentTypeBinary   ComponentType = "binary"
    ComponentTypeOperator ComponentType = "operator" // ★ 新增
)
```

### 8.3 ComponentVersion 扩展

ComponentVersion CRD 新增 Operator 相关字段：

```go
type ComponentVersionSpec struct {
    // ... 现有字段 ...
    Type ComponentType `json:"type"` // 可为 "operator"

    // Operator is the operator bundle configuration (when Type=operator).
    // +optional
    Operator *OperatorSpec `json:"operator,omitempty"`
}

type OperatorSpec struct {
    // BundlePath is the path to the operator bundle in the ReleaseImage.
    BundlePath string `json:"bundlePath"`

    // Mode is the operator execution mode.
    // +kubebuilder:validation:Enum=InCluster;NodeDaemonSet;BkeAgentOperator
    Mode string `json:"mode"`

    // HealthCheckTimeout is the max time to wait for operator readiness.
    // +optional
    HealthCheckTimeout string `json:"healthCheckTimeout,omitempty"`
}
```

### 8.4 与 DAG 调度器的集成

Operator 类型组件与现有组件类型一样参与 DAG 拓扑排序和版本决策：

```txt
DAG 拓扑示例 (安装):
  Batch 1: [bkeagent (binary)]           → bkeagent 安装
  Batch 2: [containerd (operator)]       → containerd Operator 部署
  Batch 3: [kubelet (operator)]          → kubelet Operator 部署
  Batch 4: [certs (inline)]              → 证书生成
  Batch 5: [kubernetes-master (inline)]  → kubeadm init
  Batch 6: [coredns (operator)]          → coredns Operator 部署
  Batch 7: [kube-proxy (yaml)]           → kube-proxy YAML apply
```

DAG 调度器不区分 Operator 和非 Operator 组件——所有组件通过 `ComponentVersion.spec.dependencies` 声明依赖，Scheduler 按拓扑批次调度。Operator 组件由 `OperatorExecutor` 执行，其他类型由各自的 Executor 执行。

---

## 9. Operator 开发规范

### 9.1 Operator 项目结构

```txt
operators/
  containerd-operator/
    ├── api/
    │   └── v1alpha1/
    │       ├── containerdconfig_types.go    ← CRD 定义
    │       └── groupversion_info.go
    ├── controllers/
    │   └── containerdconfig_controller.go   ← Controller 逻辑
    ├── config/
    │   ├── crd/                             ← CRD YAML (生成)
    │   ├── rbac/                            ← RBAC YAML (生成)
    │   └── manager/                         ← Deployment YAML
    ├── bundle/
    │   └── containerd-operator.yaml         ← Bundle 元数据
    ├── Dockerfile                           ← Operator 镜像构建
    ├── main.go                              ← Operator 入口
    └── Makefile                             ← 构建/生成/打包
```

### 9.2 Controller 实现规范

每个 Operator Controller 需实现以下规范：

1. **Reconcile 循环**：监听自身 CRD，实现声明式调谐
2. **状态报告**：更新 ComponentOperator CR 的 Conditions（Available/Progressing/Degraded/Upgradeable）
3. **版本报告**：更新 ComponentOperator CR 的 Versions 字段
4. **健康检查**：实现 Liveness/Readiness Probe
5. **优雅关闭**：实现 signal handling 和 cleanup

```go
// 示例: containerd-operator controller

func (r *ContainerdConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 获取 ContainerdConfig CR
    config := &v1alpha1.ContainerdConfig{}
    if err := r.Get(ctx, req.NamespacedName, config); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 2. 确保 containerd 二进制已安装 (DaemonSet 模式)
    if err := r.ensureContainerdBinary(ctx, config); err != nil {
        r.updateOperatorStatus(ctx, config, ConditionDegraded, "BinaryInstallFailed", err.Error())
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }

    // 3. 确保 containerd 配置正确
    if err := r.ensureContainerdConfig(ctx, config); err != nil {
        r.updateOperatorStatus(ctx, config, ConditionDegraded, "ConfigFailed", err.Error())
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }

    // 4. 确保 containerd 服务运行
    if err := r.ensureContainerdService(ctx, config); err != nil {
        r.updateOperatorStatus(ctx, config, ConditionAvailable, false, "ServiceNotRunning", err.Error())
        return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
    }

    // 5. 更新状态: Available=True, Progressing=False, Degraded=False
    r.updateOperatorStatus(ctx, config, ConditionAvailable, true, "ContainerdRunning", "")
    r.updateOperatorStatus(ctx, config, ConditionProgressing, false, "ReconcileComplete", "")
    r.updateOperatorStatus(ctx, config, ConditionDegraded, false, "", "")

    return ctrl.Result{}, nil
}
```

### 9.3 打包规范

Operator 镜像构建：

```dockerfile
# operators/containerd-operator/Dockerfile
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
WORKDIR /workspace
COPY bin/manager /workspace/manager
USER 65532:65532
ENTRYPOINT ["/workspace/manager"]
```

Bundle 打包到 ReleaseImage：

```makefile
# 打包 Operator Bundle 到 ReleaseImage
operator-bundle:
    # 1. 构建 Operator 镜像
    docker build -t $(REGISTRY)/containerd-operator:$(VERSION) operators/containerd-operator/
    # 2. 生成 Bundle YAML
    mkdir -p $(BUNDLE_DIR)/operator-bundles/containerd-operator/
    cp operators/containerd-operator/bundle/containerd-operator.yaml $(BUNDLE_DIR)/operator-bundles/containerd-operator/
    cp operators/containerd-operator/config/crd/*.yaml $(BUNDLE_DIR)/operator-bundles/containerd-operator/crds/
    cp operators/containerd-operator/config/manager/manager.yaml $(BUNDLE_DIR)/operator-bundles/containerd-operator/controller.yaml
    cp operators/containerd-operator/config/rbac/*.yaml $(BUNDLE_DIR)/operator-bundles/containerd-operator/rbac/
```

---

## 10. 迁移方案

### 10.1 迁移阶段

| 阶段 | 目标 | 说明 |
|------|------|------|
| **Phase 1** | ComponentOperator CRD + OperatorExecutor | 新增 CRD 和 Executor，支持 Operator 类型组件 |
| **Phase 2** | 集群级组件迁移 | coredns、kube-proxy 从 YAML/Helm 迁移为 In-cluster Operator |
| **Phase 3** | 节点级组件迁移 | containerd、kubelet 从 bkeagent 插件迁移为 Node-level Operator |
| **Phase 4** | bkeagent 自身迁移 | bkeagent 升级为 BkeAgent Operator，Command CR 模式逐步退役 |

### 10.2 集群级组件迁移（Phase 2）

以 coredns 为例：

```txt
迁移前 (YAML 类型):
  ComponentVersion: coredns (type=yaml)
  → YamlComponentExecutor → kubectl apply coredns.yaml
  → 状态: ClusterComponentStatuses["coredns"] = {Phase: "Installed"}

迁移后 (Operator 类型):
  ComponentVersion: coredns (type=operator)
  → OperatorExecutor → Apply coredns-operator Bundle
    → CRD: CoreDNSConfig
    → Controller: coredns-operator Deployment
    → RBAC: Role + Binding
  → Controller 运行, 监听 CoreDNSConfig CR
  → Controller 创建 coredns Deployment/Service/ConfigMap
  → 状态: ComponentOperator CR "coredns" = {Available: True, Progressing: False}
```

### 10.3 节点级组件迁移（Phase 3）

以 containerd 为例：

```txt
迁移前 (Binary 类型):
  ComponentVersion: containerd (type=binary)
  → BinaryComponentExecutor → SSH 逐节点执行 InstallScript
  → 状态: NodeComponentStatuses["containerd"][nodeIP]

迁移后 (Operator 类型, NodeDaemonSet 模式):
  ComponentVersion: containerd (type=operator, mode=NodeDaemonSet)
  → OperatorExecutor → Apply containerd-operator Bundle
    → CRD: ContainerdConfig
    → Controller: containerd-operator DaemonSet (运行在每个节点)
    → RBAC: Role + Binding
  → DaemonSet Controller 在每个节点上:
    → 下载 containerd 二进制
    → 生成 containerd 配置
    → 启动 containerd 服务
    → 健康检查
  → 状态: ComponentOperator CR "containerd" = {Available: True}
```

### 10.4 向后兼容

迁移期间，Operator 类型组件与非 Operator 类型组件在 DAG 中混合存在：

```txt
DAG 拓扑示例 (混合模式):
  Batch 1: [bkeagent (binary)]           ← 未迁移
  Batch 2: [containerd (operator)]       ← 已迁移
  Batch 3: [kubelet (binary)]            ← 未迁移
  Batch 4: [certs (inline)]              ← 未迁移
  Batch 5: [kubernetes-master (inline)]  ← 未迁移
  Batch 6: [coredns (operator)]          ← 已迁移
  Batch 7: [kube-proxy (yaml)]           ← 未迁移
```

DAG 调度器通过 `ComponentVersion.spec.type` 区分组件类型，分发到对应 Executor。混合模式通过 Feature Gate 或 ReleaseImage 中组件类型声明控制。

---

## 11. 可观测性

### 11.1 状态查询

```bash
# 查询所有 ComponentOperator 状态
kubectl get componentoperator -A

# 查询特定组件状态
kubectl get componentoperator containerd -o yaml

# 查询所有 Available=False 的组件
kubectl get componentoperator -o jsonpath='{range .items[?(@.status.conditions[?(@.type=="Available"&&@.status=="False")]]}{.metadata.name}{"\n"}{end}'
```

### 11.2 Prometheus 指标

```go
var (
    operatorAvailable = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_operator_available",
            Help: "Operator availability (1=Available, 0=Unavailable)",
        },
        []string{"cluster", "component"},
    )
    operatorProgressing = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_operator_progressing",
            Help: "Operator progressing (1=Progressing, 0=Idle)",
        },
        []string{"cluster", "component"},
    )
    operatorDegraded = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_operator_degraded",
            Help: "Operator degraded (1=Degraded, 0=Healthy)",
        },
        []string{"cluster", "component"},
    )
)
```

### 11.3 告警规则

```yaml
groups:
  - name: bke-operator-alerts
    rules:
      - alert: BKEOperatorUnavailable
        expr: bke_operator_available == 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "BKE operator {{ $labels.component }} unavailable"

      - alert: BKEOperatorDegraded
        expr: bke_operator_degraded == 1
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "BKE operator {{ $labels.component }} degraded"

      - alert: BKEOperatorStuckProgressing
        expr: bke_operator_progressing == 1
        for: 30m
        labels:
          severity: warning
        annotations:
          summary: "BKE operator {{ $labels.component }} progressing for 30m"
```

---

## 12. 工作量评估

| 类别 | 模块 | 估算（人天） |
|------|------|------------|
| 开发 | ComponentOperator CRD + DeepCopy + Webhook | 3 |
| 开发 | OperatorBundle 类型 + Bundle 解析 | 2 |
| 开发 | OperatorExecutor (Apply + 等待 + 状态门控) | 5 |
| 开发 | ComponentTypeOperator 新增 + Registry 集成 | 1 |
| 开发 | ReleaseImage 扩展 (operatorBundles 字段) | 1 |
| 开发 | coredns-operator (首个 In-cluster Operator) | 5 |
| 开发 | kube-proxy-operator (In-cluster Operator) | 3 |
| 开发 | containerd-operator (Node DaemonSet Operator) | 8 |
| 开发 | kubelet-operator (Node DaemonSet Operator) | 8 |
| 开发 | bkeagent-operator (BkeAgent Operator 模式) | 10 |
| 开发 | Prometheus 指标 + 告警规则 | 2 |
| 测试 | OperatorExecutor 单元测试 + 集成测试 | 5 |
| 测试 | 各 Operator E2E 测试 | 8 |
| 测试 | 混合模式回归测试 | 3 |
| 文档 | 设计文档 + 开发规范 + 迁移指南 | 3 |
| **合计** | | **~67 人天** |

---

## 13. 风险与缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| Operator 部署失败导致组件不可用 | 集群功能受损 | 中 | OperatorExecutor 实现重试 + 回滚；Operator 镜像预缓存 |
| Node-level Operator (DaemonSet) 无法在节点上运行 | 节点组件无法安装 | 中 | 保留 bkeagent 作为 fallback；DaemonSet 故障时回退到 Command CR 模式 |
| Operator Controller 资源占用过高 | 集群资源紧张 | 低 | Operator Controller 设置 ResourceQuota；使用轻量级 base image |
| CRD 版本兼容性 | Operator CRD 升级导致数据丢失 | 中 | CRD 使用 v1alpha1 → v1beta1 → v1 渐进升级；conversion webhook |
| 混合模式状态不一致 | Operator CR 和 ClusterComponentStatuses 状态不同步 | 中 | 状态同步逻辑：DAG 调度器从 ComponentOperator CR 同步到 ClusterComponentStatuses |
| bkeagent 迁移为 Operator 破坏现有安装流程 | 现有集群无法升级 | 低 | Phase 4 最后执行；充分测试；保留 Command CR 兼容路径 |

---

## 附录

### A. 参考文档

1. Operator Framework 官方文档: https://operatorframework.io
2. OLM (Operator Lifecycle Manager) 文档: https://olm.operatorframework.io
3. OpenShift ClusterOperator API: https://docs.openshift.com/container-platform/4.15/rest_api/operator_apis/clusteroperator-cluster-versions-operator-core.html
4. BKE OLM 架构分析: `olm-architecture-analysis.md`
5. BKE CVO 架构分析: `cvo-architecture-analysis.md`
6. BKE CVO 设计: `kep-bke-cvo-design.md`
7. Go controller-runtime 文档: https://pkg.go.dev/sigs.k8s.io/controller-runtime

### B. 术语表

| 术语 | 定义 |
|------|------|
| **Operator Bundle** | Operator 的打包格式，包含 CRD + Controller + RBAC + 配置 |
| **OperatorExecutor** | DAG 调度器中新增的执行器，负责部署和管理 Operator |
| **ComponentOperator CR** | 组件 Operator 的状态报告 CRD，提供 Available/Progressing/Degraded 条件 |
| **In-cluster Operator** | 作为 Deployment 运行在目标集群中的 Operator，管理集群级组件 |
| **Node-level Operator** | 作为 DaemonSet 运行在目标集群中的 Operator，管理节点级组件 |
| **BkeAgent Operator** | bkeagent 自身升级为 Operator 模式，通过 CRD 管理节点级组件 |
| **Operator Catalog** | BKE 中的 Operator 目录，对应 ReleaseImage 中的 operator-bundles 目录 |
| **状态门控** | DAG 调度器通过 ComponentOperator CR 的 Conditions 阻塞/放行后续组件 |

---

**文档版本**: v1.0
**维护者**: openFuyao Team

# KEP-7: OLM (Operator Lifecycle Manager) 核心架构与管理 Operator 设计思路梳理

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-7 |
| **标题** | OLM 核心架构与管理 Operator 设计思路梳理 |
| **状态** | `informational` |
| **类型** | Architecture Research |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-01 |
| **来源** | OpenShift 官方文档 (docs.openshift.com)、Operator Framework 文档 (olm.operatorframework.io)、OLM 代码库 (github.com/operator-framework/operator-lifecycle-manager、github.com/operator-framework/api) |

## 1. 摘要

本文档系统性梳理 Red Hat OpenShift Operator Lifecycle Manager (OLM) 的核心架构及其管理 Operator 的设计思路。内容来源于 OpenShift 官方文档 (4.15) 及 OLM 开源代码库 (v0, `operator-framework/operator-lifecycle-manager` 仓库 master 分支)。OLM 采用**双 Operator 架构**：OLM Operator 负责按 CSV 声明部署 Operator 的 Deployment/RBAC；Catalog Operator 负责 Catalog 查询、依赖解析、InstallPlan 生成与资源创建。二者通过 6 个 CRD (CSV、CatalogSource、Subscription、InstallPlan、OperatorGroup、OperatorCondition) 协作，形成声明式的 Operator 安装-升级-卸载生命周期管理体系。本文档涵盖 OLM 整体架构、各 CRD 定义与字段、依赖解析 SAT 求解器、更新图 (replaces/skips/skipRange)、File-Based Catalog、多租 OperatorGroup、代码结构 (QueueInformer 模式)、控制循环状态机、以及核心设计模式，为 BKE 平台引入 Operator 生命周期管理能力提供架构参考。

---

## 2. OLM 总体架构

### 2.1 双 Operator 架构

OLM 由两个独立的 Operator (两个二进制) 组成，职责分离，实现增量式框架接入：

```txt
┌─────────────────────────────────────────────────────────────────────────────┐
│                         OLM 双 Operator 架构                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────┐    ┌─────────────────────────────┐          │
│  │       OLM Operator          │    │      Catalog Operator        │          │
│  │      (olm-operator)         │    │   (catalog-operator)         │          │
│  ├─────────────────────────────┤    ├─────────────────────────────┤          │
│  │                              │    │                              │          │
│  │  职责:                       │    │  职责:                       │          │
│  │  • 监听 CSV                  │    │  • 监听 Subscription          │          │
│  │  • 检查 CSV 依赖是否满足     │    │  • 查询 CatalogSource (gRPC) │          │
│  │  • 执行 Install Strategy     │    │  • 依赖解析 (SAT 求解器)      │          │
│  │  • 创建 Deployment/SA/RBAC   │    │  • 生成 InstallPlan          │          │
│  │  • 复制 CSV 到目标命名空间   │    │  • 创建 CRD + CSV            │          │
│  │  • 管理 OperatorGroup        │    │  • 维护 CatalogSource Pod    │          │
│  │  • 管理 OperatorCondition    │    │  • 轮询 Catalog 更新         │          │
│  │                              │    │                              │          │
│  │  拥有的 CRD:                 │    │  拥有的 CRD:                 │          │
│  │  • ClusterServiceVersion     │    │  • InstallPlan               │          │
│  │  • OperatorGroup             │    │  • CatalogSource             │          │
│  │  • OperatorCondition         │    │  • Subscription              │          │
│  │                              │    │                              │          │
│  │  创建的资源:                 │    │  创建的资源:                 │          │
│  │  • Deployments               │    │  • CustomResourceDefinitions │          │
│  │  • ServiceAccounts           │    │  • ClusterServiceVersions    │          │
│  │  • (Cluster)Roles            │    │                              │          │
│  │  • (Cluster)RoleBindings    │    │                              │          │
│  └──────────┬──────────────────┘    └──────────┬──────────────────┘          │
│             │                                   │                             │
│             │           ┌──────────────┐        │                             │
│             └─────────→│   CSV (CRD)  │←───────┘                             │
│             │           └──────────────┘                                    │
│             │                    ▲                                            │
│             │                    │ 触发                                       │
│             │            ┌──────────────┐                                    │
│             └────────────│ InstallPlan  │                                    │
│                          └──────────────┘                                    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 职责分工

| 维度 | OLM Operator | Catalog Operator |
|------|-------------|-----------------|
| **核心职责** | 按 CSV 声明部署 Operator 工作负载 | 解析依赖、创建 InstallPlan、安装 CRD/CSV |
| **监听对象** | CSV、OperatorGroup、OperatorCondition | Subscription、CatalogSource、InstallPlan |
| **拥有的 CRD** | CSV、OperatorGroup、OperatorCondition | InstallPlan、CatalogSource、Subscription |
| **创建的资源** | Deployment、SA、(Cluster)Role、(Cluster)RoleBinding | CRD、CSV |
| **唤醒间隔** | 5 分钟 (默认) | 15 分钟 (默认) |
| **入口二进制** | `cmd/olm/main.go` | `cmd/catalog/main.go` |

### 2.3 核心设计原则

| 原则 | 说明 |
|------|------|
| **职责分离** | Catalog Operator 负责 "解析+创建 CRD/CSV"，OLM Operator 负责 "部署 Deployment/RBAC"，用户可增量式接入框架 |
| **声明式意图** | 用户通过 Subscription 声明期望状态（包/频道/自动更新），OLM 负责协调到目标态 |
| **Catalog 驱动** | Operator 通过 CatalogSource 发现，Catalog 是唯一真相源 (FBC 模式下忽略 bundle/CSV 内的升级边元数据) |
| **SAT 求解依赖解析** | 将 Operator 的 properties + constraints 转换为布尔公式，交由 SAT 求解器判定兼容性，确保不会安装互相不兼容的 Operator 集合 |
| **命名空间级解析** | 依赖解析在命名空间范围内进行，OperatorGroup 提供多租户目标命名空间隔离 |
| **Operator 反向通信** | OperatorCondition 允许 Operator 向 OLM 报告状态（如 Upgradeable=False），覆盖无法从 K8s 资源推断的信息 |
| **不可变 Bundle** | Bundle 镜像和元数据视为不可变；损坏版本需发布新版本并添加升级边，而非原地修改 |
| **审批门控** | InstallPlan 支持 Automatic/Manual 审批，Manual 模式下用户可逐版本控制升级节奏 |

---

## 3. CRD 详解

### 3.1 CRD 总览

| 资源 | 短名 | 拥有者 | API Group / Version | 核心作用 |
|------|------|--------|---------------------|---------|
| `ClusterServiceVersion` | `csv` | OLM Operator | `operators.coreos.com/v1alpha1` | Operator 的元数据、安装策略、版本、依赖声明 |
| `CatalogSource` | `catsrc` | Catalog Operator | `operators.coreos.com/v1alpha1` | Operator 元数据仓库 (gRPC 服务) |
| `Subscription` | `sub` | Catalog Operator | `operators.coreos.com/v1alpha1` | 订阅某包某频道，声明安装意图 |
| `InstallPlan` | `ip` | Catalog Operator | `operators.coreos.com/v1alpha1` | 依赖解析后的资源安装计划 |
| `OperatorGroup` | `og` | OLM Operator | `operators.coreos.com/v1` | 多租户目标命名空间配置 + RBAC 生成 |
| `OperatorCondition` | - | OLM Operator | `operators.coreos.com/v2` | Operator 与 OLM 的双向通信通道 |

### 3.2 ClusterServiceVersion (CSV)

CSV 代表集群上运行的一个 Operator 的特定版本，兼具 rpm/deb 元数据包和 Deployment 模板双重角色。

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: ClusterServiceVersion
metadata:
  name: memcached-operator.v0.10.0
spec:
  displayName: Memcached Operator
  description: This is an operator for memcached.
  version: 0.10.0
  release: "1"                          # 区分同版本的多次构建
  minKubeVersion: 1.16.0
  maturity: Stable
  provider:
    name: Example Corp
  keywords: [memcached, cache]
  maintainers:
    - name: Team
      email: team@example.com
  links:
    - name: Homepage
      url: https://example.com
  icon:                                  # base64 编码的图标
    - base64data: <base64>
      mediatype: image/png

  installModes:                          # 声明支持的安装模式
    - supported: true
      type: OwnNamespace
    - supported: true
      type: SingleNamespace
    - supported: false
      type: MultiNamespace
    - supported: true
      type: AllNamespaces

  install:                               # 安装策略
    strategy: deployment                 # 目前仅支持 deployment
    spec:
      permissions:                       # 命名空间级 RBAC
        - serviceAccountName: memcached-operator
          rules:
            - apiGroups: [""]
              resources: ["pods"]
              verbs: ["*"]
      clusterPermissions:                # 集群级 RBAC
        - serviceAccountName: memcached-operator
          rules:
            - apiGroups: [""]
              resources: ["serviceaccounts"]
              verbs: ["*"]
      deployments:                       # Deployment 清单
        - name: memcached-operator
          spec:
            replicas: 1
            selector:
              matchLabels:
                name: memcached-operator
            template:
              # ... pod template

  customresourcedefinitions:             # CRD 声明
    owned:                               # 本 Operator 拥有的 CRD
      - name: memcacheds.cache.example.com
        version: v1alpha1
        kind: Memcached
        displayName: Memcached
        descriptors:
          - description: Number of replicas
            displayName: Size
            path: size
            xDescriptors: ["urn:alm:descriptor:com.tectonic.ui:podCount"]
    required:                            # 本 Operator 依赖的 CRD
      - name: others.example.com
        version: v1alpha1
        kind: Other

  webhookdefinitions:                    # Webhook 声明
    - type: ValidatingAdmissionWebhook
      deploymentName: memcached-operator
      containerPort: 443
      rules:
        - apiGroups: ["cache.example.com"]
          operations: ["CREATE", "UPDATE"]
          resources: ["memcacheds"]

  replaces: memcached-operator.v0.9.0    # 本 CSV 替换的上一版本
  skips:                                  # 跳过的版本 (可选)
    - memcached-operator.v0.9.1
  relatedImages:                          # 引用的镜像列表
    - name: memcached-operator
      image: quay.io/example/memcached-operator:v0.10.0
```

**Go 类型定义** (`operator-framework/api` 仓库 `pkg/operators/v1alpha1/clusterserviceversion_types.go`)：

```go
type ClusterServiceVersionSpec struct {
    InstallStrategy          NamedInstallStrategy          `json:"install"`
    Version                  version.OperatorVersion       `json:"version,omitempty"`
    Release                  release.OperatorRelease        `json:"release,omitzero"`
    Maturity                 string
    CustomResourceDefinitions CustomResourceDefinitions
    APIServiceDefinitions     APIServiceDefinitions
    WebhookDefinitions        []WebhookDescription
    NativeAPIs                 []metav1.GroupVersionKind
    MinKubeVersion            string
    DisplayName, Description  string
    Keywords                  []string
    Maintainers               []Maintainer
    Provider                  AppLink
    Links                     []AppLink
    Icon                      []Icon
    InstallModes              []InstallMode
    Replaces                  string
    Labels, Annotations       map[string]string
    Selector                  *metav1.LabelSelector
    Cleanup                   CleanupSpec
    Skips                     []string
    RelatedImages             []RelatedImage
}
```

**CSV 状态机 (Phase)**：

```txt
┌──────────────────────────────────────────────────────────────────┐
│                     CSV Phase 状态机                              │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│   None                                                           │
│    │                                                              │
│    ▼                                                              │
│   Pending ──────── (依赖消失) ────────┐                         │
│    │                                    │                         │
│    ▼                                    │                         │
│   InstallReady ────── (RBAC 变更) ─────┘                         │
│    │                                                              │
│    ▼                                                              │
│   Installing                                                      │
│    │                                                              │
│    ├──────────────────┬──────────────────┐                      │
│    ▼                  ▼                  ▼                        │
│   Succeeded         Failed            Replacing                  │
│  (运行中)         (安装失败)    (检测到替换CSV)                  │
│                                        │                         │
│                                        ▼                         │
│                                    Deleting                      │
│                                                                  │
│  注: CSV 必须是 OperatorGroup 的活跃成员才能执行安装策略           │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

| Phase | 含义 | 触发条件 |
|-------|------|---------|
| `Pending` | 依赖未满足 | CRD/API 尚未就绪 |
| `InstallReady` | 依赖已满足，准备安装 | 所有 required CRD/API 可用 + OperatorGroup 成员 |
| `Installing` | 正在创建 Deployment/RBAC | Install Strategy 执行中 |
| `Succeeded` | 安装成功 | Deployment Ready |
| `Failed` | 安装失败 | 部署失败、RBAC 不够等 |
| `Replacing` | 被新版本 CSV 替换 | Catalog Operator 创建了替换 CSV |
| `Deleting` | 正在清理 | 替换 CSV 进入 Succeeded 后删除旧 CSV |

### 3.3 CatalogSource

CatalogSource 是 Operator 元数据仓库，以 gRPC API 对外提供查询。

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: operatorhubio-catalog
  namespace: olm
spec:
  sourceType: grpc                    # grpc | internal | configmap
  image: quay.io/operatorhubio/catalog:latest  # grpc+image: 拉取镜像运行 Pod
  # address: registry.example.com:50051        # grpc+address: 连接已有 gRPC 端点
  displayName: Community Operators
  publisher: OperatorHub.io
  priority: 100                        # 依赖解析器优先级 (越大越优先)
  updateStrategy:
    type: RegistryPoll                 # 轮询策略
    interval: 15m                      # 轮询间隔
  grpcPodConfig:
    securityContextConfig: restricted  # legacy | restricted
    nodeSelector:
      node-role.kubernetes.io/worker: ""
  secrets:
    - catalog-pull-secret              # 私有镜像仓库凭据
status:
  message: ...
  reason: ...
  registryServiceStatus:              # gRPC Pod 服务状态
    protocol: grpc
    serviceName: operatorhubio-catalog
    createdAt: "2026-09-01T00:00:00Z"
    port: 50051
  grpcConnectionState:
    address: operatorhubio-catalog.olm:50051
    lastObservedState: READY
    lastConnectTime: "2026-09-01T00:00:00Z"
```

**三种 sourceType**：

| sourceType | 工作方式 | 适用场景 |
|------------|---------|---------|
| `grpc` + `image` | OLM 拉取镜像，运行 Pod 暴露 gRPC API | 最常用，File-Based Catalog 或 sqlite 索引镜像 |
| `grpc` + `address` | OLM 直接连接指定地址的 gRPC API | 已有外部 registry 服务 |
| `internal` / `configmap` | OLM 解析 ConfigMap 数据，启动 Pod 提供 gRPC API | 旧格式，不推荐 |

**Red Hat 提供的默认 Catalog** (OpenShift 4.15)：

| Catalog | 镜像 | 说明 |
|---------|------|------|
| `redhat-operators` | `registry.redhat.io/redhat/redhat-operator-index:v4.15` | Red Hat 产品，官方支持 |
| `certified-operators` | `registry.redhat.io/redhat/certified-operator-index:v4.15` | ISV 产品，由 ISV 支持 |
| `redhat-marketplace` | `registry.redhat.io/redhat/redhat-marketplace-index:v4.15` | 可从 Red Hat Marketplace 购买的认证软件 |
| `community-operators` | `registry.redhat.io/redhat/community-operator-index:v4.15` | 社区维护，无官方支持 |

> **注意**：Red Hat 默认 Catalog 自 OpenShift 4.11 起采用 File-Based Catalog 格式，sqlite 格式已废弃。集群升级时 CVO 自动更新索引镜像 tag (如 `:v4.14` → `:v4.15`)。

### 3.4 Subscription

Subscription 声明用户意图：安装某个包的某个频道，并控制更新审批。

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: my-operator
  namespace: operators
spec:
  name: my-operator                     # 包名
  channel: stable                       # 频道
  source: my-catalog                    # CatalogSource 名称
  sourceNamespace: olm                  # CatalogSource 所在命名空间
  installPlanApproval: Manual           # Automatic (默认) | Manual
  startingCSV: 1.1.0                    # 指定安装的初始版本 (需 Manual)
  config:                               # 安装配置覆盖
    resources:
      - requests:
          cpu: 100m
          memory: 256Mi
status:
  currentCSV: my-operator.v1.2.0       # Catalog 中最新版本
  installedCSV: my-operator.v1.1.0     # 已安装版本
  state: UpgradePending                # 状态
  installPlanRef:                       # 关联的 InstallPlan
    name: install-abcde
    namespace: operators
```

**Subscription 状态机**：

| State | 含义 |
|-------|------|
| `UpgradeAvailable` | Catalog 中存在比已安装版本更新的版本 |
| `UpgradePending` | InstallPlan 已创建，等待审批或正在执行 |
| `AtLatestKnown` | 已安装版本为频道最新版 |
| `UpgradeFailed` | 升级失败 |

### 3.5 InstallPlan

InstallPlan 是 Catalog Operator 依赖解析后计算出的资源安装计划。

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: InstallPlan
metadata:
  name: install-abcde
  namespace: operators
spec:
  approval: Manual                      # Automatic | Manual
  approved: false                       # 审批状态
  clusterServiceVersionNames:           # 期望安装的 CSV
    - my-operator.v1.2.0
  catalogSource: my-catalog
  catalogSourceNamespace: olm
status:
  phase: Installing                     # Planning | RequiresApproval | Installing | Complete | Failed
  plan:                                 # 解析出的步骤
    - resolving: my-operator.v1.2.0
      resource:
        group: cache.example.com
        version: v1alpha1
        kind: CustomResourceDefinition
        name: memcacheds.cache.example.com
        manifest: |                    # YAML 清单
          apiVersion: apiextensions.k8s.io/v1
          kind: CustomResourceDefinition
          ...
      status: Created
      optional: false
    - resolving: my-operator.v1.2.0
      resource:
        group: operators.coreos.com
        version: v1alpha1
        kind: ClusterServiceVersion
        name: my-operator.v1.2.0
        manifest: |
          ...
      status: Created
```

**InstallPlan 状态机**：

```txt
┌──────────────────────────────────────────────────────┐
│              InstallPlan Phase 状态机                 │
├──────────────────────────────────────────────────────┤
│                                                      │
│   None                                               │
│    │                                                  │
│    ▼                                                  │
│   Planning ──────────── (解析成功)                    │
│    │                                                  │
│    ├──── (Manual 审批) ──→ RequiresApproval           │
│    │                        │                         │
│    │                        │ (approved=true)         │
│    │                        ▼                         │
│    └──────────────────→ Installing                    │
│                            │                         │
│                   ├────────┴────────┐                │
│                   ▼                 ▼                 │
│               Complete            Failed              │
│                                                      │
│  OrderSteps 排序: CSV → CRD → 其他资源                │
│                                                      │
└──────────────────────────────────────────────────────┘
```

### 3.6 OperatorGroup

OperatorGroup 提供基础多租户能力，选择目标命名空间集合并为其成员 Operator 生成 RBAC。

```yaml
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: my-group
  namespace: operators
spec:
  targetNamespaces:                    # 显式指定目标命名空间
    - tenant-a
    - tenant-b
  # selector:                          # 或使用标签选择器 (与 targetNamespaces 互斥)
  #   matchLabels:
  #     environment: production
  # 省略两者 → 全局 OperatorGroup (目标为所有命名空间)
  staticProvidedAPIs: false            # 静态模式 (不从成员 CSV 推导 providedAPIs)
  upgradeStrategy: Default             # Default | TechPreviewUnsafeFailForward
status:
  namespaces:                          # 解析后的目标命名空间列表
    - tenant-a
    - tenant-b
  conditions:
    - type: AllNamespaces
      status: "False"
```

**四种 InstallMode**：

| InstallModeType | 说明 |
|-----------------|------|
| `OwnNamespace` | Operator 可作为目标仅包含自身命名空间的 OG 成员 |
| `SingleNamespace` | Operator 可作为目标仅包含一个其他命名空间的 OG 成员 |
| `MultiNamespace` | Operator 可作为目标包含多个命名空间的 OG 成员 |
| `AllNamespaces` | Operator 可作为全局 OG (目标为所有命名空间) 成员 |

**CSV 成员判定规则**：

1. 命名空间中仅有一个 OperatorGroup，否则 CSV 进入 `TooManyOperatorGroups` 失败状态
2. CSV 的 InstallMode 必须支持 OG 的目标命名空间集，否则进入 `UnsupportedOperatorGroup` 失败状态

**OG 自动生成的 RBAC**：

| RBAC | 选择器 | 作用 |
|------|--------|------|
| `<og-name>-admin` | `olm.opgroup.permissions/aggregate-to-admin: <og-name>` | 聚合 admin 权限 |
| `<og-name>-edit` | `olm.opgroup.permissions/aggregate-to-edit: <og-name>` | 聚合 edit 权限 |
| `<og-name>-view` | `olm.opgroup.permissions/aggregate-to-view: <og-name>` | 聚合 view 权限 |

当 CSV 成为活跃成员时，为其提供的每个 API (CRD/APIService) 生成：
- `<kind>.<group>-<version>-admin` (verb `*`)
- `<kind>.<group>-<version>-edit` (verbs `create,update,patch,delete`)
- `<kind>.<group>-<version>-view` (verbs `get,list,watch`)

**CSV 注解** (注入到成员 CSV)：

| 注解 | 说明 |
|------|------|
| `olm.operatorGroup` | 所属 OG 名称 |
| `olm.operatorGroupNamespace` | OG 所在命名空间 |
| `olm.targetNamespaces` | 逗号分隔的目标命名空间 (通过 Downward API 投射到 Pod) |
| `olm.providedAPIs` | 所有活跃成员 CSV 提供的 API 集合 (`<Kind>.<version>.<group>` 逗号分隔) |

**Copied CSVs**：OLM 将活跃成员 CSV 复制到每个目标命名空间，告知用户该命名空间有 Operator 在监听。Copied CSV 的 `status.reason` 为 `Copied`，且 `olm.targetNamespaces` 注解被剥离以防信息泄露。

### 3.7 OperatorCondition

OperatorCondition 建立 Operator 与 OLM 的双向通信通道。Operator 可通过更新 Status.Conditions 向 OLM 报告自身状态。

```yaml
apiVersion: operators.coreos.com/v2
kind: OperatorCondition
metadata:
  name: foo-operator
  namespace: operators
spec:
  overrides:                           # 管理员覆盖 (优先级高于 operator 报告)
    - type: Upgradeable
      status: "True"
      reason: upgradeIsSafe
      message: "The cluster admin wants to make the operator eligible for an upgrade."
  conditions:                          # Operator 报告的条件
    - type: Upgradeable
      status: "False"
      reason: migration
      message: "The operator is performing a migration."
      lastTransitionTime: "2026-09-01T00:00:00Z"
```

**支持的 Condition Type**：

| Type | status=False 含义 | 说明 |
|------|------------------|------|
| `Upgradeable` | 不可升级 | 阻止新 CSV 替换当前 CSV (新 CSV 停留在 Pending)，但**不阻止集群升级** |

> **注意**：`Upgradeable=False` 不替代 Pod Disruption Budget 的角色，仅控制 OLM 层面的 Operator 升级。

---

## 4. 完整工作流

### 4.1 Operator 安装流程

```txt
┌─────────────────────────────────────────────────────────────────────────────┐
│                     OLM Operator 安装完整流程                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  1. 用户创建 OperatorGroup                                                  │
│  ┌────────────────────────┐                                                │
│  │ kind: OperatorGroup     │  目标命名空间: [foo, bar]                      │
│  │ namespace: operators    │  → OLM 为目标命名空间生成 RBAC 聚合规则         │
│  └───────────┬────────────┘                                                │
│              │                                                              │
│              ▼                                                              │
│  2. 用户创建 Subscription                                                   │
│  ┌────────────────────────┐                                                │
│  │ kind: Subscription      │  name: my-operator, channel: stable             │
│  │ namespace: operators    │  source: my-catalog, sourceNamespace: olm        │
│  │                         │  installPlanApproval: Manual                    │
│  └───────────┬────────────┘                                                │
│              │                                                              │
│              ▼                                                              │
│  3. Catalog Operator: syncResolvingNamespace                                │
│  ┌────────────────────────────────────────────────────────────┐             │
│  │  • 查询 CatalogSource gRPC API                             │             │
│  │  • 依赖解析 (SAT 求解器)                                    │             │
│  │  • 生成 InstallPlan (Plan steps: CRD + CSV)                │             │
│  │  • InstallPlan phase: Planning → RequiresApproval          │             │
│  └───────────┬────────────────────────────────────────────────┘             │
│              │                                                              │
│              ▼                                                              │
│  4. 用户审批 InstallPlan (Manual 模式)                                       │
│  ┌────────────────────────────────────────────────────────────┐             │
│  │  kubectl patch ip install-xxx -p '{"spec":{"approved":true}}'│           │
│  │  → InstallPlan phase: RequiresApproval → Installing        │             │
│  └───────────┬────────────────────────────────────────────────┘             │
│              │                                                              │
│              ▼                                                              │
│  5. Catalog Operator: 执行 InstallPlan 步骤                                 │
│  ┌────────────────────────────────────────────────────────────┐             │
│  │  • 创建 CRD (按 OrderSteps: CSV → CRD → 其他)              │             │
│  │  • 创建 CSV (源 CSV，写入 operators 命名空间)               │             │
│  │  • InstallPlan phase: Installing → Complete                 │             │
│  └───────────┬────────────────────────────────────────────────┘             │
│              │                                                              │
│              ▼                                                              │
│  6. OLM Operator: syncClusterServiceVersion                                 │
│  ┌────────────────────────────────────────────────────────────┐             │
│  │  • 检查 CSV 依赖 (required CRD/API)                        │             │
│  │  • 检查 OperatorGroup 成员资格                              │             │
│  │  • CSV phase: Pending → InstallReady → Installing          │             │
│  │  • StrategyDeploymentInstaller 创建:                        │             │
│  │    - ServiceAccount                                        │             │
│  │    - (Cluster)Role + (Cluster)RoleBinding                  │             │
│  │    - Deployment                                            │             │
│  │  • CSV phase: Installing → Succeeded                       │             │
│  └───────────┬────────────────────────────────────────────────┘             │
│              │                                                              │
│              ▼                                                              │
│  7. OLM Operator: syncCopyCSV                                              │
│  ┌────────────────────────────────────────────────────────────┐             │
│  │  • 将源 CSV 复制到目标命名空间 (foo, bar)                   │             │
│  │  • Copied CSV status.reason = Copied                       │             │
│  │  • 剥离 olm.targetNamespaces 注解                           │             │
│  └────────────────────────────────────────────────────────────┘             │
│              │                                                              │
│              ▼                                                              │
│  8. 完成: Operator 在 operators 命名空间运行，监听 foo/bar 命名空间           │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 Operator 升级流程

```txt
用户更新 CatalogSource 索引镜像
  │
  ▼
Catalog Operator 轮询检测到更新
  │
  ├── CatalogSource gRPC 连接重建 (syncSourceState)
  ├── 失效解析器缓存
  ├── 重排队订阅者命名空间到 nsResolveQueue
  │
  ▼
syncResolvingNamespace
  │
  ├── 对每个 Subscription: 查询 Catalog 最新版本
  ├── 走更新图: 从频道 HEAD 沿 replaces 链回溯到已安装版本
  ├── 依赖解析 (SAT 求解器)
  ├── 生成新 InstallPlan (包含新 CSV + 依赖资源)
  │     InstallPlan phase: Planning → RequiresApproval (Manual) / Installing (Auto)
  │
  ▼
审批后执行 InstallPlan
  │
  ├── 创建新 CSV (replaces 指向旧 CSV)
  │
  ▼
OLM Operator: syncClusterServiceVersion
  │
  ├── 旧 CSV 检测到替换 CSV → phase: Replacing
  ├── 新 CSV: Pending → InstallReady → Installing → Succeeded
  ├── 新 CSV Succeeded 后，旧 CSV → Deleting
  ├── 删除旧 CSV 关联的 Deployment (Cleanup 策略)
  │
  ▼
升级完成
```

### 4.3 更新图 (Update Graph)

OLM 通过三种机制定义升级路径，在 File-Based Catalog 中由 `olm.channel` blob 定义：

```yaml
# 1. Replaces - 显式指定替换关系
---
schema: olm.channel
package: myoperator
channel: stable
entries:
  - name: myoperator.v1.0.0
  - name: myoperator.v1.0.1
    replaces: myoperator.v1.0.0    # v1.0.1 替换 v1.0.0
  - name: myoperator.v1.0.2
    replaces: myoperator.v1.0.1    # v1.0.2 替换 v1.0.1

# 2. Skips - 跳过中间版本
---
schema: olm.channel
package: myoperator
channel: stable
entries:
  - name: myoperator.v1.0.3
    replaces: myoperator.v1.0.0
    skips:
      - myoperator.v1.0.1          # 跳过 v1.0.1
      - myoperator.v1.0.2          # 跳过 v1.0.2

# 3. SkipRange - 按版本范围跳过
---
schema: olm.channel
package: myoperator
channel: stable
entries:
  - name: myoperator.v1.0.3
    skipRange: ">=1.0.0 <1.0.3"    # 1.0.0~1.0.2 直接到 1.0.3
```

| 机制 | 说明 | 使用场景 |
|------|------|---------|
| `replaces` | 指定上一个版本名 | 常规顺序升级 |
| `skips` | 跳过列表中的版本 | 跳过有 Bug/CVE 的版本 |
| `skipRange` | SemVer 范围跳过 | z-stream 补丁直跳 |

> **重要**：在 FBC 模式下，Catalog 是升级边的**唯一真相源**。Bundle/CSV 中的 `replaces`/`skips` 元数据被忽略。

**升级路径示例**：

```txt
已安装: v0.1.1
Catalog 频道: v0.1.3 (replaces v0.1.2, v0.1.2 replaces v0.1.1)

OLM 从频道 HEAD 回溯:
  v0.1.3 → replaces → v0.1.2 → replaces → v0.1.1 (已安装)

升级路径: v0.1.1 → v0.1.2 → v0.1.3
  1. 安装 v0.1.2 替换 v0.1.1
  2. 安装 v0.1.3 替换 v0.1.2
  3. 安装版本 = 频道 HEAD → 升级完成
```

---

## 5. 依赖解析

### 5.1 设计哲学

OLM 的依赖解析类似于 yum/rpm，但有一个特殊约束：**Operator 始终在运行**，因此 OLM 必须确保不会留下互相不兼容的 Operator 集合。

**OLM 绝不能创造的场景**：
- 安装一组需要无法提供的 API 的 Operator
- 以破坏依赖它的其他 Operator 的方式更新某 Operator

### 5.2 Properties 与 Constraints

```txt
┌─────────────────────────────────────────────────────────────────────────────┐
│                    OLM 依赖解析模型                                          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Operator Bundle (在 Catalog 中)                                            │
│  ┌─────────────────────────────────────────────────────────────┐            │
│  │  Properties (公开接口，描述 Operator 提供什么)              │            │
│  │  • olm.package  → {packageName, version}                    │            │
│  │  • olm.gvk      → {group, version, kind} (每个提供的 API)   │            │
│  │  • 自定义 properties (如 certified, stable)                 │            │
│  │                                                             │            │
│  │  Constraints/Dependencies (依赖，描述需要什么)               │            │
│  │  • olm.gvk.required       → 需要某 GVK 的 API               │            │
│  │  • olm.package.required   → 需要某包某版本范围              │            │
│  │  • olm.constraint         → 通用约束 (CEL/AND/OR/NOT)      │            │
│  └─────────────────────────────────────────────────────────────┘            │
│                                  │                                          │
│                                  ▼                                          │
│  ┌─────────────────────────────────────────────────────────────┐            │
│  │              转换为布尔公式 (Boolean Formulas)               │            │
│  │                                                             │            │
│  │  SAT Solver (pkg/controller/registry/resolver/solver/)      │            │
│  │  • 输入: 所有候选 Operator 的 properties + constraints      │            │
│  │  • 输出: 满足所有约束的 Operator 子集                        │            │
│  │  • 确保不存在不兼容的安装集合                                 │            │
│  └─────────────────────────────────────────────────────────────┘            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 5.3 默认 Properties

每个 Catalog 中的 Bundle 自动获得以下默认 properties：

| Property | 内容 | 来源 |
|----------|------|------|
| `olm.package` | `{packageName, version}` | Bundle 元数据 |
| `olm.gvk` | 每个 CSV owned CRD 的 `{group, version, kind}` | CSV spec |

### 5.4 声明依赖

**`metadata/dependencies.yaml`** (OLM 0.16.1+ / OPM 1.10.0+)：

```yaml
dependencies:
  - type: olm.package            # 依赖某包的版本范围
    value:
      packageName: prometheus
      version: ">0.27.0"
  - type: olm.gvk                # 依赖某 API
    value:
      group: etcd.database.coreos.com
      kind: EtcdCluster
      version: v1beta2
```

**`metadata/properties.yaml`** (OPM 1.17.4+，推荐)：

```yaml
properties:
  - type: olm.gvk.required       # 依赖某 API
    value:
      group: etcd.database.coreos.com
      kind: EtcdCluster
      version: v1beta2
  - type: olm.package.required   # 依赖某包版本范围
    value:
      packageName: prometheus
      versionRange: ">0.27.0"
  - type: olm.constraint         # 通用约束
    value:
      failureMessage: "require certified and stable properties"
      cel:
        rule: 'properties.exists(p, p.type == "certified") && properties.exists(p, p.type == "stable")'
```

### 5.5 通用约束 (olm.constraint)

`olm.constraint` 支持复合逻辑：

| 类型 | 说明 |
|------|------|
| `gvk` | 等价于 `olm.gvk` |
| `package` | 等价于 `olm.package` |
| `cel` | CEL 表达式，运行时求值 |
| `all` | 合取 (AND) |
| `any` | 析取 (OR) |
| `not` | 否定 (仅在 all/any 内使用) |

**复合约束示例** (any of three GVK versions)：

```yaml
- type: olm.constraint
  value:
    failureMessage: "Any are required for Baz because..."
    any:
      constraints:
        - gvk: {group: foos.example.com, version: v1beta1, kind: Foo}
        - gvk: {group: foos.example.com, version: v1beta2, kind: Foo}
        - gvk: {group: foos.example.com, version: v1, kind: Foo}
```

> **限制**：`olm.constraint` 的原始大小上限为 64KB，以防资源耗尽攻击。

### 5.6 解析偏好 (Preferences)

解析器在选择候选时遵循以下优先级顺序：

| 序号 | 规则 | 说明 |
|------|------|------|
| 1 | Catalog 优先级 | `spec.priority` 更高的 Catalog 中的候选优先；同 Catalog 中的候选优先于其他 Catalog |
| 2 | 频道顺序 | 包的默认频道优先；非默认频道按频道名字典序 |
| 3 | 频道内顺序 | 更接近频道 HEAD (更新图上层) 的版本优先 |
| 4 | Subscription 约束 | 新安装：频道内所有版本可选；升级：仅可从当前版本升级的版本可选，选最新 (最接近 HEAD) |
| 5 | 包约束 | 同一命名空间内同一包不可有两个 Operator |

### 5.7 CRD 升级规则

| 场景 | 升级条件 |
|------|---------|
| CRD 仅被单个 CSV 拥有 | 立即升级 |
| CRD 被多个 CSV 拥有 | 需满足：(1) 新 CRD 包含当前所有 serving version；(2) 所有现有 CR 实例通过新 CRD schema 校验 |

### 5.8 依赖解析最佳实践

| 实践 | 说明 |
|------|------|
| 依赖 API 或版本范围 | Operator 随时可能增删 API，始终声明 `olm.gvk` 依赖 |
| 设置最低版本 | 仅知 `apiVersion` 可能不够 (同 version 可能新增字段)，附加 `olm.package` 最低版本 |
| 省略最高版本或用宽范围 | 集群级资源窄范围会不必要地约束其他消费者更新 |
| 避免 dependencies.yaml 中混合 AND 语义 | 同时声明 `olm.package` + `olm.gvk` 可能被两个不同 Operator 满足，用 `properties.yaml` 的 `all` 约束替代 |

### 5.9 解析器防止的场景

| 场景 | 说明 |
|------|------|
| 废弃依赖 API | A 依赖 B；B 更新为提供 C 但废弃 B → A 不工作。OLM 阻止 |
| 版本死锁 | A 依赖 B；B 依赖 A；同时更新到互相依赖新版本 → OLM 阻止 |

---

## 6. File-Based Catalog (FBC)

### 6.1 设计目标

File-Based Catalog 是 OLM Catalog 格式的最新迭代，从 sqlite 数据库演变为纯文本 (JSON/YAML) 格式。

| 目标 | 说明 |
|------|------|
| **可编辑性** | 直接修改 Catalog 文件并通过验证；支持 jq 等标准工具；可提升频道、改默认频道、自定义升级边 |
| **可组合性** | 存储在任意目录层级；通过复制目录合并 Catalog；支持去中心化 (作者维护各 Operator Catalog，复合维护者策划) |
| **可扩展性** | 低级表示，可用任意 schema 扩展；维护者可构建自定义工具 (如 `mode=semver` 翻译为低级 FBC) |

### 6.2 目录结构

```txt
catalog
├── pkgA
│   └── operator.yaml          # 包含 olm.package + olm.channel + olm.bundle
├── pkgB
│   ├── .indexignore           # 忽略文件 (同 .gitignore 语义)
│   ├── operator.yaml
│   └── README.md              # 非 Catalog 文件，被忽略
└── pkgC
    ├── package.json           # 自定义 schema 文件
    ├── channels.yaml
    └── bundles.json
```

> `opm` 遍历根目录递归子目录，加载每个文件。非 Catalog 文件通过 `.indexignore` 忽略。

### 6.3 Meta Schema

所有 FBC blob 必须遵守 Meta schema：

| 字段 | 必需 | 说明 |
|------|------|------|
| `schema` | 是 | 对象的 schema 名 |
| `package` | 否 | 所属包名 |
| `name` | 否 | 对象名 |
| `properties` | 否 | 关联 properties |

> `schema` + `package` + `name` 组合在 Catalog 内必须**唯一**。所有 `olm.*` schema 为 OLM 保留。

### 6.4 OLM 定义的 Schema

| Schema | 作用 | 频率 |
|--------|------|------|
| `olm.package` | 包级元数据 (name, description, defaultChannel, icon) | 每包仅一个 |
| `olm.channel` | 频道定义 + bundle 条目 + 升级边 | 每个 channel blob 定义一组 entries |
| `olm.bundle` | 可安装版本 (image, relatedImages, properties) | 每个 bundle 一个 |
| `olm.deprecations` | 废弃信息 (package/channel/bundle 级) | 可选，每包最多一个 |

**`olm.channel` entry 字段**：

| 字段 | 必需 | 说明 |
|------|------|------|
| `name` | 是 | bundle 名 |
| `replaces` | 否 | 替换的 bundle 名 (可指向不在 Catalog 中的 bundle) |
| `skips` | 否 | 跳过的 bundle 列表 |
| `skipRange` | 否 | SemVer 范围跳过 |

**`olm.bundle` properties 子类型**：

| Property 类型 | 说明 |
|----------------|------|
| `olm.package` | `{packageName, version}` |
| `olm.gvk` | `{group, version, kind}` |
| `olm.package.required` | `{packageName, versionRange}` |
| `olm.gvk.required` | `{group, version, kind}` |
| `olm.csv.metadata` | Bundle 元数据 (推荐，替代 `olm.bundle.object`) |
| `olm.constraint` | 通用约束 |
| `olm.bundle.object` | (已废弃) 内联 bundle manifests |

### 6.5 opm CLI 命令

| 命令 | 作用 |
|------|------|
| `opm init <pkg>` | 生成 `olm.package` blob |
| `opm render <img>...` | 从索引/bundle/sqlite 生成声明式配置 |
| `opm validate <dir>` | 验证 FBC (exit 0=valid, 1=invalid) |
| `opm serve <dir>` | gRPC 服务声明式配置 (端口 50051) |
| `opm alpha diff` | 新旧 Catalog diff |
| `opm generate dockerfile <dir>` | 生成 Catalog 索引镜像 Dockerfile |

### 6.6 不可变性原则

- Bundle 镜像和元数据视为**不可变**
- 推送损坏的 bundle 后，假设至少一个用户已升级到该版本，应发布新版本并添加升级边
- OLM 不会重新安装已安装的 bundle (即使内容更新)
- 允许的操作：频道提升 (在另一个 `olm.channel` 中添加 entry)、新升级边 (更新 `replaces`/`skips`)

---

## 7. 多租户与 OperatorGroup

### 7.1 默认行为

| 安装模式 | 默认行为 |
|---------|---------|
| AllNamespaces Operator | 安装到 `openshift-operators` |
| OwnNamespace/SingleNamespace Operator | 安装到用户指定命名空间 |

### 7.2 Operator 共址 (Colocation)

OLM 将同一命名空间内安装的 OLM 管理 Operator 视为**关联 Operator**：

| 表现 | 说明 |
|------|------|
| InstallPlan 包含同命名空间所有 CSV | 待执行更新的 InstallPlan 包含同命名空间所有 Operator 的 CSV |
| 共享更新策略 | 同命名空间所有 Operator 共享相同更新策略 (Automatic/Manual) |

> **问题**：难以推理 InstallPlan，无法在同命名空间混合 Automatic/Manual 更新。

### 7.3 多租户推荐方案

```txt
┌─────────────────────────────────────────────────────────────────────────┐
│              多租户安装同一 Operator 的推荐方案                          │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  命名空间: tenant-a-operator   ┌──────────────────────┐                  │
│  ┌─────────────────────────┐  │ OperatorGroup         │                 │
│  │ OperatorGroup             │  │ targetNamespaces:    │                │
│  │ targetNamespaces: [tenant-a]│  │   [tenant-b]         │               │
│  └────────┬────────────────┘  └─────────┬────────────┘                 │
│           │                              │                              │
│           ▼                              ▼                              │
│  ┌─────────────────────────┐  ┌──────────────────────┐                  │
│  │ Subscription             │  │ Subscription          │                 │
│  │ name: my-operator        │  │ name: my-operator     │                 │
│  │ namespace: tenant-a-op   │  │ namespace: tenant-b-op│                │
│  └────────┬────────────────┘  └─────────┬────────────┘                 │
│           │                              │                              │
│           ▼                              ▼                              │
│  CSV (Succeeded)              CSV (Succeeded)                           │
│  在 tenant-a-op 中             在 tenant-b-op 中                        │
│  监听 tenant-a 命名空间         监听 tenant-b 命名空间                   │
│                                                                         │
│  约束:                                                                   │
│  • 所有实例必须相同版本                                                  │
│  • Operator 不能有其他 Operator 依赖                                     │
│  • 不能附带 CRD 转换 webhook                                             │
│  • 不能在同一集群使用同一 Operator 的不同版本                            │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 7.4 OperatorGroup 交集规则

两个 OperatorGroup **相交** 当且仅当目标命名空间集交集非空。**提供 API 相交** 当且仅当相交且提供 API 集合交集非空。

每次活跃成员 CSV 同步时检查：

| 提供API相交集 | CSV的API是OG的子集 | 结果 |
|--------------|-------------------|------|
| 空 | 是 | 继续转换 |
| 空 | 否 | 静态OG: 失败 `CannotModifyStaticOperatorGroupProvidedAPIs`；否则: OG 的 providedAPIs 更新为并集 |
| 非空 | 否 | 清理 Deployments，CSV 失败 `InterOperatorGroupOwnerConflict` |
| 非空 | 是 | 静态OG: 失败；否则: OG 的 providedAPIs 更新为差集 |

> OperatorGroup 导致的失败状态是**非终态**的。

---

## 8. 代码库结构

### 8.1 仓库总览

```
operator-framework/operator-lifecycle-manager (v0, 维护模式)
├── cmd/                    # 二进制入口
│   ├── olm/                # olm-operator (main.go, manager.go)
│   ├── catalog/            # catalog-operator (main.go, start.go)
│   ├── package-server/     # package-server
│   └── copy-content/       # copy-content 工具
├── pkg/
│   ├── controller/         # 控制器
│   │   ├── operators/      # 主要控制器
│   │   │   ├── catalog/    # Catalog Operator
│   │   │   ├── olm/        # OLM Operator
│   │   │   ├── catalogtemplate/
│   │   │   ├── decorators/ # Operator CR 便利包装
│   │   │   ├── openshift/  # ClusterOperator 状态集成
│   │   │   ├── operator_controller.go          # OperatorReconciler (controller-runtime)
│   │   │   ├── operatorcondition_controller.go
│   │   │   ├── operatorconditiongenerator_controller.go
│   │   │   └── adoption_controller.go
│   │   ├── install/        # 安装策略 (resolver.go, deployment.go, apiservice.go,
│   │   │                  #   webhook.go, certresources.go, rule_checker.go)
│   │   ├── registry/       # Catalog registry (grpc/, reconciler/, resolver/, types.go)
│   │   └── bundle/         # Bundle 解包
│   ├── lib/
│   │   ├── queueinformer/  # 自定义控制器框架 ★核心
│   │   ├── operatorclient/ # K8s 客户端封装
│   │   ├── operatorlister/  # 类型化缓存访问
│   │   ├── ownerutil/      # OwnerReference 工具
│   │   ├── scoped/         # ServiceAccount 范围化客户端
│   │   ├── catalogsource/  # CatalogSource 状态助手
│   │   ├── csv/            # CSV 工具 (SetGenerator, ReplaceFinder)
│   │   ├── event/          # 事件记录器
│   │   ├── index/          # 索引函数
│   │   ├── kubestate/      # Syncer 接口
│   │   ├── labeler/        # API 标签集
│   │   └── proxy/          # OpenShift 代理配置
│   └── api/
│       ├── client/         # 生成客户端 + informer
│       └── wrappers/       # InstallStrategyDeploymentClient
├── deploy/                 # 部署清单
├── doc/                    # 文档
├── test/                   # 测试
└── vendor/                 # 依赖

operator-framework/api (API 类型仓库)
└── pkg/operators/
    ├── v1alpha1/           # CSV, Subscription, InstallPlan, CatalogSource
    ├── v1/                 # OperatorGroup
    └── v2/                 # OperatorCondition
```

### 8.2 QueueInformer 模式

OLM 使用**自定义控制器框架** (非 controller-runtime)，基于 Kubernetes informers + workqueues，核心在 `pkg/lib/queueinformer/`。

```txt
┌─────────────────────────────────────────────────────────────────────────────┐
│                       QueueInformer 模式                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌───────────────────┐     事件      ┌──────────────────┐                  │
│  │ SharedIndexInformer │─────────────→│  Resource Handlers│                 │
│  │ (Lister + Indexer)  │              │  • AddFunc        │                 │
│  │                     │              │  • UpdateFunc     │                 │
│  │  Transform Func:   │              │  • DeleteFunc     │                 │
│  │  裁剪缓存对象       │              └────────┬─────────┘                 │
│  └───────────────────┘                       │                             │
│                                              ▼                             │
│                               ┌──────────────────────┐                     │
│                               │  TypedRateLimiting   │                     │
│                               │  workqueue           │                     │
│                               │  (types.NamespacedName)│                   │
│                               └────────┬─────────────┘                     │
│                                        │                                     │
│                                        ▼                                     │
│  ┌────────────────────────────────────────────────────────────┐             │
│  │                    Worker Loop                              │             │
│  │  (numWorkers 个 goroutine，每个 QueueInformer)              │             │
│  │                                                            │             │
│  │  for {                                                     │             │
│  │    item := queue.Get()           // 取出 key              │             │
│  │    obj := indexer.GetByKey(key)  // 从缓存取对象           │             │
│  │    if !exists → queue.Forget(item)                        │             │
│  │    syncer.Sync(ctx, obj)         // 调用同步器             │             │
│  │    if err && NumRequeues < 8:                             │             │
│  │      queue.AddRateLimited(item)  // 限速重排               │             │
│  │    else:                                                  │             │
│  │      queue.Forget(item)          // 放弃重试               │             │
│  │  }                                                        │             │
│  └────────────────────────────────────────────────────────────┘             │
│                                                                             │
│  关键特性:                                                                  │
│  • 同一 key 的 syncHandler 不会并发执行                                     │
│  • 按命名空间分区队列 (ResourceQueueSet)                                    │
│  • 带抖动的重同步 (ResyncWithJitter, 如 30s ± 20%)                          │
│  • 最大重试 8 次                                                            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**`QueueInformer` 结构体**：

```go
type QueueInformer struct {
    metrics.MetricsProvider
    logger   *logrus.Logger
    queue    workqueue.TypedRateLimitingInterface[types.NamespacedName]
    informer cache.SharedIndexInformer
    indexer  cache.Indexer
    syncer   kubestate.Syncer
    onDelete func(interface{})
}
```

**`Operator` 接口** (可复用的 Operator 循环)：

```go
type Operator interface {
    ObservableOperator    // Ready(), Done(), AtLevel(), Started(), HasSynced()
    ExtensibleOperator    // RegisterQueueInformer(), RegisterInformer()
    RunInformers(ctx)
    Run(ctx)
}
```

### 8.3 OLM Operator 代码结构

入口 `cmd/olm/main.go`，核心在 `pkg/controller/operators/olm/`。

**`Operator` 结构体** (`olm/operator.go`)：

```go
type Operator struct {
    queueinformer.Operator                // 嵌入基础循环
    opClient          operatorclient.ClientInterface
    client            versioned.Interface  // OLM CR 客户端
    lister            operatorlister.OperatorLister
    // 按命名空间分区的队列集
    ogQueueSet, csvQueueSet, csvCopyQueueSet, copiedCSVQueueSet *queueinformer.ResourceQueueSet
    olmConfigQueue, nsQueueSet, apiServiceQueue workqueue.TypedRateLimitingInterface[types.NamespacedName]
    csvIndexers      map[string]cache.Indexer
    resolver         install.StrategyResolverInterface
    apiReconciler    APIIntersectionReconciler
    apiLabeler       labeler.Labeler
    // ServiceAccount 范围化
    serviceAccountSyncer *scoped.UserDefinedServiceAccountSyncer
    clientAttenuator    *scoped.ClientAttenuator
    serviceAccountQuerier *scoped.UserDefinedServiceAccountQuerier
    plugins             []plugins.OperatorPlugin
    ruleChecker         func(*v1alpha1.ClusterServiceVersion) *install.CSVRuleChecker
    resyncPeriod        func() time.Duration
}
```

**OLM Operator 为每个监听命名空间注册的 Informer**：

| 资源 | 队列 | Syncer | 说明 |
|------|------|--------|------|
| CSV (排除 Copied) | `csvQueueSet` | `syncClusterServiceVersion` | CSV 状态机 |
| CSV Copy | `csvCopyQueueSet` | `syncCopyCSV` | 复制 CSV 到目标命名空间 |
| Copied CSV | `copiedCSVQueueSet` | - | Transform 裁剪为 metadata + hash |
| OperatorGroup | `ogQueueSet` | `syncOperatorGroups` | 目标命名空间 + providedAPIs |
| OperatorCondition (v2) | - | `k8sSyncer` | |
| Subscription | - | `syncSubscription` | 重排队已安装 CSV |
| Deployment | - | `k8sSyncer` | `olm.managed=true` 标签过滤 |
| Role/RoleBinding/Secret/Service/SA | - | `k8sSyncer` | |
| OLMConfig | - | `syncOLMConfig` | |

**集群级 (NamespaceAll) 注册**：

| 资源 | Syncer | 说明 |
|------|--------|------|
| ClusterRole / ClusterRoleBinding | - | 内容哈希标签 |
| Namespace | `syncNamespace` | 应用 OG 标签到命名空间 |
| APIService | `syncAPIService` | GC 孤立 API 服务 |
| CRD | - | metadata-only informer (减少缓存大小) |

### 8.4 Catalog Operator 代码结构

入口 `cmd/catalog/main.go`，核心在 `pkg/controller/operators/catalog/`。

**`Operator` 结构体** (`catalog/operator.go`)：

```go
type Operator struct {
    queueinformer.Operator
    opClient          operatorclient.ClientInterface
    client            versioned.Interface
    dynamicClient     dynamic.Interface
    namespace         string                   // Operator 自身命名空间
    catsrcQueueSet    *queueinformer.ResourceQueueSet
    subQueueSet       *queueinformer.ResourceQueueSet
    ipQueueSet        *queueinformer.ResourceQueueSet
    ogQueueSet        *queueinformer.ResourceQueueSet
    nsResolveQueue    workqueue.TypedRateLimitingInterface[types.NamespacedName]
    sources           *grpc.SourceStore         // gRPC 连接到 Catalog registry
    resolver          resolver.StepResolver      // 依赖解析器
    reconciler        reconciler.RegistryReconcilerFactory  // registry Pod 协调器
    bundleUnpacker    bundle.Unpacker
    installPlanTimeout, bundleUnpackTimeout time.Duration
    resolverSourceProvider *resolver.RegistrySourceProvider
    operatorCacheProvider  resolvercache.OperatorCacheProvider
}
```

**Catalog Operator 注册的 Informer** (均为集群级 `NamespaceAll`)：

| 资源 | Syncer | 说明 |
|------|--------|------|
| Pruned CSV | - | Transform 裁剪 (仅保留 TypeMeta/ObjectMeta/最小Spec/最小Status) |
| InstallPlan | `syncInstallPlans` | 指标: `NewMetricsInstallPlan` |
| OperatorGroup | `syncOperatorGroups` | |
| CatalogSource | `syncCatalogSources` + `handleCatSrcDeletion` | 指标: `NewMetricsCatalogSource` |
| Subscription | `subscription.NewSyncer(...)` | 索引: `PresentCatalogIndexFunc` |
| K8s 资源 (Role/SA/Service/Pod/ConfigMap/Job) | `labelObjects` + `syncObject` | |
| CRD | - | metadata-only informer |
| Namespace | `syncResolvingNamespace` | **核心解析循环** |

**CatalogSource sync 链** (`syncCatalogSources`)：

```go
chain := []CatalogSourceSyncFunc{
    validateSourceType,      // 验证 sourceType + 必需字段
    o.syncConfigMap,         // configmap 类型处理
    o.syncRegistryServer,    // 确保 registry Pod (通过 reconciler)
    o.syncConnection,       // 管理 gRPC 连接状态
}
// 每个 func 返回 (out, continueSync, error); !continueSync 则中断链
```

**核心解析循环** (`syncResolvingNamespace`)：

```txt
1. gcInstallPlans(namespace) — GC 旧 InstallPlan (最多保留 5 个)
2. 列出命名空间内所有 Subscription
3. 检查 FailForward 和 bundle 解包超时
4. 对每个 Subscription:
   ├── resolver.StepResolver 查询 CatalogSource gRPC API
   ├── 依赖解析 (SAT 求解器，生成 Steps)
   └── 创建/更新 InstallPlan
```

### 8.5 安装策略

`pkg/controller/install/` 实现安装策略：

```go
type Strategy interface {
    GetStrategyName() string
}

type StrategyInstaller interface {
    Install(strategy Strategy) error
    CheckInstalled(strategy Strategy) (bool, error)
    ShouldRotateCerts(strategy Strategy) (bool, error)
    CertsRotateAt() time.Time
    CertsRotated() bool
}
```

| 文件 | 职责 |
|------|------|
| `resolver.go` | 策略解析 (目前仅支持 `deployment`) |
| `deployment.go` | `StrategyDeploymentInstaller`：创建/更新 Deployment、SA、RBAC |
| `apiservice.go` | APIService 资源管理 |
| `webhook.go` | Webhook 配置管理 (Validating/Mutating/Conversion) |
| `certresources.go` | API Service/Webhook 证书生成 |
| `rule_checker.go` | `CSVRuleChecker`：校验 CSV 权限是否满足 |
| `status_viewer.go` | 需求/依赖状态查看 |

### 8.6 Operator Reconciler (controller-runtime)

`pkg/controller/operators/operator_controller.go` 是一个较新的 **controller-runtime** (非 queueinformer) 协调器，用于 `Operator` CR (聚合状态资源)：

```go
type OperatorReconciler struct {
    client.Client
    log     logr.Logger
    factory decorators.OperatorFactory
}
```

**`SetupWithManager` 监听**：
- 全量 watch: Deployment, Namespace, CRD, APIService, Subscription, InstallPlan, CSV, OperatorCondition
- metadata-only watch (`builder.OnlyMetadata`): SA, Secret, ConfigMap, Role, RoleBinding, ClusterRole, ClusterRoleBinding

通过 `mapComponentRequests` 将对象标签 (`decorators.OperatorNames`) 映射回 Operator 协调请求。

---

## 9. 控制循环汇总

### 9.1 三大状态机

| 资源 | 状态机 | 控制器 |
|------|--------|--------|
| CSV | `None → Pending → InstallReady → Installing → Succeeded/Failed`；`Replacing → Deleting` | OLM Operator |
| InstallPlan | `None → Planning → RequiresApproval → Installing → Complete/Failed` | Catalog Operator |
| Subscription | `None → UpgradeAvailable → UpgradePending → AtLatestKnown` | Catalog Operator |

### 9.2 协调循环交互

```txt
┌─────────────────────────────────────────────────────────────────────────────┐
│                    OLM 协调循环交互图                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  用户创建 Subscription                                                       │
│       │                                                                     │
│       ▼                                                                     │
│  ┌──────────────────────────────────────────────────────────────────┐       │
│  │  Catalog Operator                                                │       │
│  │                                                                  │       │
│  │  syncResolvingNamespace                                          │       │
│  │    ├── 查询 CatalogSource (gRPC SourceStore)                     │       │
│  │    ├── resolver.StepResolver (SAT 求解器) → 生成 Steps           │       │
│  │    ├── 创建 InstallPlan (Plan: CRD + CSV steps)                  │       │
│  │    └── InstallPlan phase: Planning → RequiresApproval/Installing │       │
│  │                                                                  │       │
│  │  syncInstallPlans                                                │       │
│  │    ├── OrderSteps: CSV → CRD → 其他                               │       │
│  │    ├── 执行 Steps: 创建 CRD, 创建 CSV                             │       │
│  │    └── InstallPlan phase: Installing → Complete                  │       │
│  └──────────┬───────────────────────────────────────────────────────┘       │
│             │ 创建 CSV                                                      │
│             ▼                                                               │
│  ┌──────────────────────────────────────────────────────────────────┐       │
│  │  OLM Operator                                                    │       │
│  │                                                                  │       │
│  │  syncClusterServiceVersion                                       │       │
│  │    ├── CSVRuleChecker: 检查 required CRD/API 是否可用             │       │
│  │    ├── 检查 OperatorGroup 成员资格                                │       │
│  │    ├── CSV phase: Pending → InstallReady → Installing            │       │
│  │    ├── StrategyDeploymentInstaller:                              │       │
│  │    │     创建 SA, (Cluster)Role, (Cluster)RoleBinding            │       │
│  │    │     创建 Deployment                                          │       │
│  │    ├── CSV phase: Installing → Succeeded                         │       │
│  │    └── syncCopyCSV: 复制 CSV 到目标命名空间                       │       │
│  │                                                                  │       │
│  │  syncOperatorGroups                                              │       │
│  │    ├── 协调目标命名空间                                           │       │
│  │    ├── 更新 olm.providedAPIs 注解                                 │       │
│  │    ├── 生成聚合 RBAC (admin/edit/view)                           │       │
│  │    └── 检查交集规则                                               │       │
│  └──────────────────────────────────────────────────────────────────┘       │
│                                                                             │
│  CatalogSource 轮询:                                                        │
│    RegistryPoll (interval 15m) → 检测更新 → 重建 gRPC 连接                   │
│    → syncSourceState: 失效缓存 → 重排队 nsResolveQueue → 新一轮解析         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 10. 核心设计模式

| 模式 | 实现位置 | 说明 |
|------|---------|------|
| **QueueInformer** | `pkg/lib/queueinformer/` | informer + workqueue + syncer，限速重排 (最大 8 次)，同 key 串行，命名空间分区队列集 |
| **Functional Options** | `OperatorOption`, `QueueInformer` options, `bundle.Unpacker` options | 全库使用 |
| **Sync Chain / Pipeline** | `CatalogSourceSyncFunc` 链 | 每个 func 返回 `(out, continueSync, error)`，`!continueSync` 中断 |
| **Informer Transform** | `pkg/controller/operators/` | 裁剪缓存对象 (CSV 裁剪到最小字段，Copied CSV 裁剪到 metadata+hash)，减少内存 |
| **Metadata-only Informer** | CRD, RBAC (Operator Reconciler) | `PartialObjectMetadataList`，仅缓存元数据 |
| **Label-based Filtering** | `olm.managed=true` | 标签迁移完成后 informer 过滤到 OLM 管理对象，跳过标签逻辑 |
| **Content-hash Labelling** | RBAC 对象 | `PolicyRuleHashLabelValue`, `RoleReferenceAndSubjectHashLabelValue`，去重/比较 |
| **Decorator Pattern** | `decorators.OperatorFactory` | 包装 Operator CR 便利方法 |
| **Plugin System** | `operatorPlugInFactoryFuncs` | 下游 (OpenShift) 注入插件，`Done()` 时关闭 |
| **SAT Solver** | `pkg/controller/registry/resolver/solver/` | 依赖解析，properties + constraints → 布尔公式 → 求解 |
| **Server-Side Apply** | `controllerclient.NewForConfig(config, scheme, "olm.registry")` | field manager `olm.registry` |
| **Double-checked Locking** | `getRuleChecker()` | RLock → Lock 懒初始化 |
| **ResourceQueueSet** | `pkg/lib/queueinformer/resourcequeue.go` | 按命名空间分区的队列集，`Set(ns, q)`, `Requeue(ns, name)`, `RequeueAfter(ns, name, dur)` |
| **ResyncWithJitter** | `pkg/lib/queueinformer/jitter.go` | 基础重同步周期 + 抖动因子 (如 30s ± 20%)，避免惊群 |

---

## 11. OLM 指标

| 指标 | 说明 |
|------|------|
| `catalog_source_count` | CatalogSource 数量 |
| `catalogsource_ready` | CatalogSource 状态 (1=READY, 0=not) |
| `csv_abnormal` | CSV 非 Succeeded 状态时存在 |
| `csv_count` | 成功注册的 CSV 数量 |
| `csv_succeeded` | CSV 是否 Succeeded (1/0) |
| `csv_upgrade_count` | CSV 升级单调计数 |
| `install_plan_count` | InstallPlan 数量 |
| `installplan_warnings_total` | InstallPlan 中资源警告单调计数 |
| `olm_resolution_duration_seconds` | 依赖解析耗时 |
| `subscription_count` | Subscription 数量 |
| `subscription_sync_total` | Subscription 同步单调计数 (labels: channel, installed CSV, name) |

---

## 12. 设计思路总结与启示

### 12.1 OLM 管理 Operator 的核心设计思路

```txt
┌─────────────────────────────────────────────────────────────────────────────┐
│              OLM 管理 Operator 的核心设计思路                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  1. 元数据驱动 (Metadata-Driven)                                            │
│     • CSV 作为 Operator 的 "安装包描述" (类似 rpm/deb)                       │
│     • Catalog 作为 "包仓库" (类似 yum repo)                                  │
│     • Subscription 作为 "安装意图" (声明式)                                   │
│     • InstallPlan 作为 "解析后的安装清单" (可审批)                           │
│                                                                             │
│  2. 解析-执行分离 (Resolve-Execute Separation)                              │
│     • Catalog Operator: 解析依赖 → 计算资源清单 → 创建 CRD/CSV              │
│     • OLM Operator: 按 CSV 声明 → 创建 Deployment/RBAC                       │
│     • 两个 Operator 通过 CSV 松耦合                                          │
│                                                                             │
│  3. 声明式协调 (Declarative Reconciliation)                                 │
│     • 用户声明期望 (Subscription: 包/频道/审批策略)                           │
│     • OLM 持续协调到目标态 (轮询 Catalog, 走更新图, 升级)                    │
│     • Operator 反向通信 (OperatorCondition: Upgradeable=False)              │
│                                                                             │
│  4. 更新图驱动升级 (Update Graph-Driven Upgrade)                            │
│     • 不比较版本号，走 replaces 链确定升级路径                                │
│     • 频道 HEAD = "最新" (类似 Git HEAD)                                     │
│     • supports/skips/skipRange 提供灵活升级控制                               │
│     • 逐中间版本升级，不跳版本                                                │
│                                                                             │
│  5. SAT 求解器保证兼容性 (SAT-Based Compatibility)                         │
│     • properties (提供什么) + constraints (需要什么) → 布尔公式             │
│     • 确保不会安装互相不兼容的 Operator 集合                                 │
│     • 防止版本死锁和依赖断裂                                                  │
│                                                                             │
│  6. 命名空间级多租户 (Namespace-Scoped Multitenancy)                        │
│     • OperatorGroup 选择目标命名空间 + 生成 RBAC                              │
│     • 依赖解析在命名空间范围内进行                                            │
│     • Copied CSV 告知目标命名空间有 Operator 在监听                          │
│                                                                             │
│  7. 不可变 Bundle + 源控 Catalog (Immutable Bundles)                       │
│     • Bundle 镜像不可变，损坏版本发布新版本+升级边                            │
│     • Catalog 元数据存储在源码控制中作为真相源                               │
│     • FBC 格式支持 jq/CI/CD 友好的文本编辑                                   │
│                                                                             │
│  8. 审批门控 (Approval Gates)                                               │
│     • InstallPlan Automatic/Manual 审批                                      │
│     • Manual 模式逐版本控制升级节奏                                          │
│     • OperatorCondition Upgradeable 阻止不安全升级                           │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 12.2 对 BKE 平台的启示

| OLM 设计 | BKE 对应思考 |
|----------|-------------|
| CSV (Operator 元数据) | BKE 的 ComponentVersion CRD 可借鉴 CSV 的 owned/required CRD 声明、install strategy、replaces 字段设计 |
| CatalogSource + FBC | BKE 的 ReleaseImage + bke-manifests 仓库可借鉴 Catalog 的声明式包仓库设计，FBC 的可组合/可编辑特性 |
| Subscription (声明式意图) | BKE 可考虑引入类似 Subscription 的声明式安装意图 CRD，驱动 Operator 自动安装/升级 |
| InstallPlan (解析+审批) | BKE 的 DAG 调度器可借鉴 InstallPlan 的 "解析步骤 → 审批 → 执行" 模式 |
| OperatorGroup (多租户) | BKE 多集群场景可借鉴 OperatorGroup 的目标命名空间选择 + RBAC 生成模式 |
| SAT 依赖解析 | BKE 组件间依赖关系可借鉴 properties + constraints 模型，避免不兼容安装集合 |
| 更新图 (replaces/skips/skipRange) | BKE 的 UpgradePath 可借鉴更新图设计，实现安全的逐版本升级路径 |
| QueueInformer 模式 | BKE 控制器可参考 informer+workqueue+syncer 模式 (但建议用 controller-runtime) |
| OperatorCondition 反向通信 | BKE 可借鉴 Operator 向管理平面报告状态的通信模式 |

---

## 13. 附录

### 13.1 参考来源

| 来源 | URL |
|------|-----|
| OpenShift OLM 文档 | https://docs.openshift.com/container-platform/4.15/operators/understanding/olm/olm-arch.html |
| Operator Framework OLM 文档 | https://olm.operatorframework.io/docs/concepts/olm-architecture/ |
| OLM 代码库 | https://github.com/operator-framework/operator-lifecycle-manager |
| OLM API 类型库 | https://github.com/operator-framework/api |
| CSV CRD 文档 | https://olm.operatorframework.io/docs/concepts/crds/clusterserviceversion/ |
| CatalogSource CRD 文档 | https://olm.operatorframework.io/docs/concepts/crds/catalogsource/ |
| Subscription CRD 文档 | https://olm.operatorframework.io/docs/concepts/crds/subscription/ |
| InstallPlan CRD 文档 | https://olm.operatorframework.io/docs/concepts/crds/installplan/ |
| OperatorGroup CRD 文档 | https://olm.operatorframework.io/docs/concepts/crds/operatorgroup/ |
| OperatorCondition CRD 文档 | https://olm.operatorframework.io/docs/concepts/crds/operatorcondition/ |
| 依赖解析文档 | https://olm.operatorframework.io/docs/concepts/olm-architecture/dependency-resolution/ |
| 更新图文档 | https://olm.operatorframework.io/docs/concepts/olm-architecture/operator-catalog/creating-an-update-graph/ |
| File-Based Catalog 文档 | https://olm.operatorframework.io/docs/reference/file-based-catalogs/ |

### 13.2 术语表

| 术语 | 定义 |
|------|------|
| **OLM** | Operator Lifecycle Manager，管理 Kubernetes Operator 生命周期的框架 |
| **CSV** | ClusterServiceVersion，Operator 特定版本的元数据包，含安装策略和依赖声明 |
| **CatalogSource** | Operator 元数据仓库，以 gRPC API 对外提供查询 |
| **Subscription** | 声明安装某包某频道的意图，控制更新审批策略 |
| **InstallPlan** | 依赖解析后计算出的资源安装计划，含步骤列表和审批状态 |
| **OperatorGroup** | 多租户目标命名空间配置，为成员 Operator 生成 RBAC |
| **OperatorCondition** | Operator 与 OLM 的双向通信通道 (如 Upgradeable) |
| **FBC** | File-Based Catalog，纯文本 (JSON/YAML) 的 Catalog 格式 |
| **opm** | Operator Package Manager，FBC 管理工具 |
| **Bundle** | Operator 的打包单元，包含 CSV + CRD + 相关镜像 |
| **Channel** | 包内的版本流 (如 alpha/beta/stable)，频道 HEAD 为最新版本 |
| **Update Graph** | 由 replaces/skips/skipRange 定义的升级路径图 |
| **SAT Solver** | 布尔可满足性求解器，用于依赖解析 |
| **Properties** | Operator 在依赖解析器中的公开接口 (提供什么) |
| **Constraints** | Operator 对其他 Operator 的依赖要求 (需要什么) |
| **QueueInformer** | OLM 自定义控制器框架，informer + workqueue + syncer |
| **Copied CSV** | OLM 将源 CSV 复制到目标命名空间的副本，告知用户有 Operator 在监听 |
| **RukPak** | OLM 的新一代打包技术 (Technology Preview) |
| **InstallMode** | CSV 声明的安装模式 (OwnNamespace/SingleNamespace/MultiNamespace/AllNamespaces) |

---

**文档版本**: v1.0
**维护者**: openFuyao Team
**数据来源**: OpenShift 4.15 官方文档 + OLM v0 代码库 (master 分支)

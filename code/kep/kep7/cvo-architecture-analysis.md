# KEP-7: OpenShift CVO (Cluster Version Operator) 核心架构与管理 Operator 设计思路梳理

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-7 |
| **标题** | OpenShift CVO 核心架构与管理 Operator 设计思路梳理 |
| **状态** | `informational` |
| **类型** | Architecture Research |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-01 |
| **来源** | OpenShift 官方文档 (docs.openshift.com 4.15)、OpenShift Enhancements 设计文档 (github.com/openshift/enhancements)、CVO 代码库 (github.com/openshift/cluster-version-operator main 分支)、OpenShift API 类型库 (github.com/openshift/api config/v1) |

## 1. 摘要

本文档系统性梳理 Red Hat OpenShift Cluster Version Operator (CVO) 的核心架构及其管理 Cluster Operator 的设计思路。内容来源于 OpenShift 4.15 官方文档、OpenShift Enhancements 设计文档、以及 CVO 开源代码库 (`openshift/cluster-version-operator` 仓库 main 分支)。CVO 是 OpenShift 集群中所有 Cluster Operator 的顶层管理者，运行于 `openshift-cluster-version` 命名空间，消费 Release Payload Image 中的资源清单 (manifests)，通过清单图 (Manifest Graph) 分层协调所有 Cluster Operator 的安装与升级。CVO 采用 workqueue + worker goroutine 模式，按文件名序号 (run level) 构建有向无环图 (DAG)，在不同状态下 (Initializing/Updating/Reconciling) 使用不同的并行化策略。核心设计包括：ClusterOperator 状态门控 (阻塞直到 Available + 版本匹配 + 非 Degraded)、ClusterVersion 声明式期望状态 (spec.desiredUpdate)、Cincinnati/OSUS 升级图推荐、签名验证、前置条件检查 (Rollback/GiantHop/Upgradeable/RecommendedUpdate)、风险聚合树 (Risk Aggregation Tree)、以及通过 Pod 抽包远程 Release Image。本文档涵盖 CVO 整体架构、Operator 结构体、SyncWorker 状态机、Task Graph DAG、Resource Builder 模式、ClusterOperator 状态管理、Status 合成逻辑、OSUS 升级路径、完整升级工作流 (12 步)、代码结构与设计模式，为 BKE 平台的集群版本管理提供架构参考。

---

## 2. CVO 总体架构

### 2.1 CVO 在 OpenShift 控制平面中的定位

CVO 是 OpenShift 集群中所有 Cluster Operator 的顶层管理者。它消费 "Release Payload Image" (代表特定版本的 OpenShift)，该镜像包含集群运行所需的全部资源清单。CVO 通过协调集群资源使其与 Release Image 中的清单一致，从而实现集群安装与升级。

```txt
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                        OpenShift 控制平面架构 (CVO 视角)                              │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                          Release Payload Image                                │   │
│  │  (如 quay.io/openshift-release-dev/ocp-release:4.15.0-x86_64)                 │   │
│  │                                                                               │   │
│  │  内含:                                                                        │   │
│  │  ├── manifests/               (CVO 自身的清单)                                │   │
│  │  ├── release-manifests/       (所有 Cluster Operator 的资源清单)              │   │
│  │  │   ├── 0000_03_authorization-openshift_01_rolebindingrestriction.crd.yaml  │   │
│  │  │   ├── 0000_05_config-operator_02_apiserver.cr.yaml                        │   │
│  │  │   ├── 0000_10_kube-apiserver_*.yaml                                       │   │
│  │  │   ├── 0000_50_cluster-olm-operator_*.yaml                                 │   │
│  │  │   ├── 0000_70_network-operator_*.yaml                                     │   │
│  │  │   ├── 0000_80_machine-config-operator_*.yaml                              │   │
│  │  │   └── ... (数百个清单文件，按序号排序)                                      │   │
│  │  ├── release-metadata         (版本元数据: previous 版本列表, errata 链接)      │   │
│  │  └── image-references         (ImageStream: 所有核心组件的镜像引用)            │   │
│  └────────────────────────────────────┬─────────────────────────────────────────┘   │
│                                       │ 下载/解包/验证签名                         │
│                                       ▼                                           │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                        Cluster Version Operator (CVO)                         │   │
│  │            namespace: openshift-cluster-version                               │   │
│  │            Deployment: cluster-version-operator (1 replica)                   │   │
│  │            部署于 master 节点 (nodeSelector: node-role.kubernetes.io/master)   │   │
│  │                                                                               │   │
│  │  核心组件:                                                                    │   │
│  │  ├── Operator (pkg/cvo/cvo.go)         — 主控制器，workqueue + sync()        │   │
│  │  ├── SyncWorker (pkg/cvo/sync_worker.go)— 同步工作器，状态机 + apply()       │   │
│  │  ├── TaskGraph (pkg/payload/)           — 清单图 DAG + RunGraph 执行器        │   │
│  │  ├── ResourceBuilder (lib/resourcebuilder/)— 资源构建器 (按 GVK 分发)        │   │
│  │  ├── AvailableUpdates (pkg/cvo/availableupdates.go) — OSUS 客户端            │   │
│  │  ├── PayloadRetriever (pkg/cvo/updatepayload.go) — Release Image 抽包       │   │
│  │  ├── Preconditions (pkg/payload/precondition/) — 前置条件检查                 │   │
│  │  ├── Risk (pkg/risk/)                  — 风险聚合树                           │   │
│  │  └── Status (pkg/cvo/status.go)        — ClusterVersion 状态合成             │   │
│  │                                                                               │   │
│  │  两个工作队列:                                                                │   │
│  │  ├── queue ("clusterversion")          — 主同步队列，处理 sync()             │   │
│  │  └── availableUpdatesQueue             — 可用更新队列，处理 OSUS 轮询          │   │
│  │                                                                               │   │
│  │  Leader Election: 基于 Lease 的领导者选举，仅 leader 执行协调                  │   │
│  │  优雅关闭: 2 分钟 graceful shutdown，最后执行一次 sync() 刷新状态             │   │
│  └──────────────────────────────────┬──────────────────────────────────────────┘   │
│                                     │ 协调 (reconcile)                              │
│                                     ▼                                               │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                     Cluster Operators (由 CVO 管理)                          │   │
│  │                                                                               │   │
│  │  Run Level 00-04: CVO 自身                                                   │   │
│  │  Run Level 05:    cluster-config-operator                                    │   │
│  │  Run Level 07-09: network-operator, dns-operator, service-ca, machine-approver│  │
│  │  Run Level 10-29: kube-apiserver, kube-scheduler, kube-controller-manager    │   │
│  │  Run Level 30-39: machine-api (MAO)                                          │   │
│  │  Run Level 50-59: operator-lifecycle-manager (OLM) ★                        │   │
│  │  Run Level 60-69: openshift-apiserver, console, monitoring 等                │   │
│  │  Run Level 70:    network, dns, multus 等节点级组件                           │   │
│  │  Run Level 80:    machine-config-operator (MCO), cloud-operators            │   │
│  │  Run Level 90:    post-machine-update 组件                                   │   │
│  │                                                                               │   │
│  │  每个 Cluster Operator 通过 ClusterOperator CR 报告状态:                       │   │
│  │  • Available (True/False)     — 组件是否可用                                  │   │
│  │  • Progressing (True/False)   — 是否正在变更                                  │   │
│  │  • Degraded (True/False)      — 是否降级                                      │   │
│  │  • Upgradeable (True/False)   — 是否可升级                                    │   │
│  │  • versions [{operator, "4.15.0"}] — 版本报告                                │   │
│  │                                                                               │   │
│  │  CVO 阻塞逻辑:                                                                │   │
│  │  CVO 不创建 ClusterOperator CR (由 Operator 自身创建)                          │   │
│  │  CVO 仅监听 ClusterOperator 状态，阻塞直到:                                    │   │
│  │  • Available = True                                                           │   │
│  │  • versions 包含 Release Image 声明的版本                                      │   │
│  │  • Degraded = False (初始化期间除外)                                           │   │
│  │  • Progressing = False                                                        │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 核心设计原则

| 原则 | 说明 |
|------|------|
| **Release Image 作为唯一真相源** | 集群期望状态完全由 Release Payload Image 中的清单定义，CVO 协调集群使其一致 |
| **分层管理** | CVO 管理 Cluster Operators，Cluster Operators 管理各自 Operand (组件)，形成三级层次 |
| **清单图按 Run Level 分层** | 文件名前缀序号 (如 `0000_03_`、`0000_05_`) 定义协调顺序，同序号同组件串行，同序号不同组件并行，跨序号阻塞 |
| **ClusterOperator 状态门控** | CVO 不创建 ClusterOperator CR，仅监听其状态作为升级阻塞门，确保 Operator 完成更新后才继续 |
| **声明式期望状态** | 用户通过 `ClusterVersion.spec.desiredUpdate` 声明目标版本，CVO 持续协调到目标态 |
| **仅向前升级** | OpenShift 4 不支持自动回滚，升级是 forward-only，N-1 兼容性是强制要求 |
| **正常即异常** | CVO 自身升级时不做特殊处理，CVO 就是个普通 Pod，被 MCO drain 重启与内核 panic / 硬件故障处理路径完全一致 |
| **签名验证** | CVO 验证 Release Image 的签名，确保镜像未被篡改 (可 force 跳过) |
| **OSUS 升级图推荐** | Cincinnati 算法基于频道和版本历史推荐安全升级路径，防止跳版本 |
| **风险聚合** | 多个风险源 (ClusterOperator Upgradeable、AdminAck、Alert、ResourceDeletion) 聚合为 Upgradeable 条件 |

---

## 3. 核心数据模型

### 3.1 ClusterVersion CRD

`ClusterVersion` 是 CVO 的配置 API，通常集群中有一个名为 `version` 的集群级实例。

```yaml
apiVersion: config.openshift.io/v1
kind: ClusterVersion
metadata:
  name: version
spec:
  clusterID: 00000000-0000-0000-0000-000000000000    # RFC4122 UUID
  channel: stable-4.15                                 # 更新频道
  upstream: https://api.openshift.com/api/upgrades_info/v1/graph  # OSUS URL
  desiredUpdate:                                       # 期望升级目标 (触发升级)
    version: "4.15.0"
    image: quay.io/openshift-release-dev/ocp-release:4.15.0-x86_64
    force: false                                       # 强制升级 (跳过前置条件)
  capabilities:
    clusterVersionCapabilities: v4.15
  overrides:                                           # 将特定资源标记为非管理
    - kind: Deployment
      group: apps
      name: network-operator
      namespace: openshift-network-operator
      unmanaged: true
  signatureStores:                                     # 自定义签名存储 (FeatureGate)
    - name: custom-store
      endpoint: https://signature-server.example.com
status:
  version: "4.15.0"                                    # 当前版本
  desired:                                             # 当前正在/已应用的 release
    version: "4.15.0"
    image: quay.io/openshift-release-dev/ocp-release@sha256:...
  history:                                             # 更新历史 (最新在前，最多 100 条)
    - state: Completed
      version: "4.15.0"
      image: ...
      startedTime: "2026-01-01T00:00:00Z"
      completionTime: "2026-01-01T00:30:00Z"
      verified: true
    - state: Partial
      version: "4.14.5"
      ...
  observedGeneration: 1
  versionHash: abc123                                  # 清单内容的 FNV-64 哈希
  conditions:
    - type: Available
      status: "True"
      message: Done applying 4.15.0
    - type: Progressing
      status: "False"
      message: Cluster version is 4.15.0
    - type: Degraded
      status: "False"
    - type: Upgradeable
      status: "True"
    - type: RetrievedUpdates
      status: "True"
    - type: ReleaseAccepted
      status: "True"
  availableUpdates:                                    # OSUS 推荐的可用更新
    - version: "4.15.1"
      image: ...
  conditionalUpdates:                                  # 条件性更新 (含风险评估)
    - release:
        version: "4.15.1"
      conditions:
        - type: Recommended
          status: "True"
          reason: AllRisksAcceptable
```

**Go 类型定义** (`openshift/api config/v1/types_cluster_version.go`)：

```go
type ClusterVersionSpec struct {
    ClusterID     ClusterID
    DesiredUpdate *Update
    Upstream      URL
    Channel       string
    Capabilities  *ClusterVersionCapabilitiesSpec
    SignatureStores []SignatureStore        // FeatureGate SignatureStores, max 32
    Overrides     []ComponentOverride       // 将资源标记为非管理
}

type ClusterVersionStatus struct {
    Desired               Release
    History               []UpdateHistory         // newest first, MaxHistory=100
    ObservedGeneration    int64
    VersionHash           string
    Capabilities          ClusterVersionCapabilitiesStatus
    Conditions            []ClusterOperatorStatusCondition
    AvailableUpdates      []Release
    ConditionalUpdates    []ConditionalUpdate    // max 500
}

type Update struct {
    Architecture ClusterVersionArchitecture    // "Multi" 或 ""
    Version, Image string
    Force         bool
    AcceptRisks   []AcceptRisk                  // FG ClusterUpdateAcceptRisks, max 1000
    Mode          UpdateMode                     // FG ClusterUpdatePreflight; "Preflight"
}

type UpdateHistory struct {
    State          UpdateState    // "Completed" | "Partial"
    StartedTime    metav1.Time
    CompletionTime *metav1.Time
    Version, Image string
    Verified       bool
    AcceptedRisks  string
}
```

### 3.2 ClusterOperator CRD

每个 Cluster Operator 通过 `ClusterOperator` CR 报告自身状态。CVO 监听这些状态作为升级门控。

```yaml
apiVersion: config.openshift.io/v1
kind: ClusterOperator
metadata:
  name: operator-lifecycle-manager    # Operator 名称
spec: {}                               # spec 为空 (状态由 Operator 自身管理)
status:
  conditions:
    - type: Available
      status: "True"
      reason: AsExpected
      message: OLM is available
      lastTransitionTime: "2026-01-01T00:00:00Z"
    - type: Progressing
      status: "False"
      reason: AsExpected
      message: OLM is not progressing
    - type: Degraded
      status: "False"
      reason: AsExpected
      message: OLM is not degraded
    - type: Upgradeable
      status: "True"
      reason: AsExpected
  versions:
    - name: operator
      version: "4.15.0"               # ★ CVO 监听的版本
  relatedObjects:
    - group: operators.coreos.com
      resource: clusterserviceversions
      namespace: olm
```

**Go 类型定义** (`openshift/api config/v1/types_cluster_operator.go`)：

```go
type ClusterOperatorStatus struct {
    Conditions     []ClusterOperatorStatusCondition
    Versions       []OperandVersion        // "operator" name required when Available
    RelatedObjects []ObjectReference
    Extension      runtime.RawExtension
}

type ClusterOperatorStatusCondition struct {
    Type               ClusterStatusConditionType
    Status             ConditionStatus      // True/False/Unknown
    LastTransitionTime metav1.Time
    Reason, Message    string
}

const (
    OperatorAvailable   ClusterStatusConditionType = "Available"
    OperatorProgressing                             = "Progressing"
    OperatorDegraded                                = "Degraded"
    OperatorUpgradeable                             = "Upgradeable"  // False 阻止 minor 升级
    EvaluationConditionsDetected
)
```

### 3.3 ClusterOperator 条件语义

| 条件 | 类型 | 含义 | 升级影响 |
|------|------|------|---------|
| `Available` | 核心 | 组件功能可用 | `False` 表示需立即管理员干预，正常升级期间**不可为 False** |
| `Progressing` | 核心 | 正在从一态转向另一态 | 升级期间 `True`，完成后 `False`；<250 节点版本变更须 20 分钟内完成 (MCO 90 分钟) |
| `Degraded` | 核心 | 持续不匹配期望状态 | 正常升级期间**不可为 Degraded**；可 Available + Degraded 共存 (如 3 副本 1 crash-loop) |
| `Upgradeable` | 可选 | 可安全升级 | `False` 阻止 **minor** 升级 (patch 不受影响)；`True`/`Unknown`/缺失均允许 |
| `EvaluationConditionsDetected` | 可选 | 检测到侵入性变更 | 多个 reason 用 `::` 拼接 |

**安装/升级决策表**：

| 操作 | version | available | degraded | progressing | upgradeable |
|------|---------|-----------|----------|-------------|-------------|
| 安装完成 | any | True | any | any | any |
| 开始 patch 升级 | any | any | any | any | any |
| 开始 minor 升级 | any | any | any | any | **非 False** |
| 强制升级 | any | any | any | any | any |
| 升级完成 | 新版本 | True | False | any | any |

### 3.4 版本报告规则

```yaml
status:
  versions:
    - name: operator           # 必需 — CVO 监听此版本
      version: 4.15.0
    - name: operator-image     # 可选 — Operator 镜像
      version: "quay.io/...@sha256:..."
    - name: kube-apiserver     # 可选 — 上游版本
      version: 1.29.0
```

> **关键规则**：当 Operator 开始滚动新版本时，**必须继续报告旧版本**。只要任何 Operand 仍在运行旧版本软件，就处于混合版本状态，必须报告旧版本。只有确认不再运行旧版本时才更新版本号。这确保 CVO 不会误认为升级已完成。

---

## 4. CVO 代码结构

### 4.1 仓库目录结构

```
openshift/cluster-version-operator (main 分支)
├── cmd/                              # 入口二进制
│   └── cluster-version-operator/
│       ├── main.go                   # cobra root, 注册 klog flags
│       ├── start.go                  # start 子命令，委托给 pkg/start.Options
│       ├── image.go                  # 镜像信息
│       ├── render.go                 # 渲染
│       └── version.go                # 版本信息
├── pkg/
│   ├── cvo/                          # ★ 核心控制器
│   │   ├── cvo.go                    # Operator 主结构体 + New() + Run() + sync()
│   │   ├── sync_worker.go            # SyncWorker 状态机 + apply()
│   │   ├── status.go                 # ClusterVersion 状态合成
│   │   ├── status_history.go         # 更新历史 + 加权剪枝
│   │   ├── availableupdates.go       # OSUS 客户端 + 条件更新评估
│   │   ├── updatepayload.go          # Release Image 抽包 (Pod-based)
│   │   ├── metrics.go                # Prometheus 指标
│   │   ├── reconciliation_issues.go  # 结构化失败报告
│   │   ├── egress.go                 # 网络出口检查
│   │   ├── configuration/            # ClusterVersionOperatorConfiguration 协调
│   │   ├── internal/                 # 内部类型
│   │   │   ├── operatorstatus.go     # ★ ClusterOperator Builder (健康检查)
│   │   │   ├── generic.go            # 通用 Dynamic 客户端 Builder
│   │   │   └── dynamicclient/        # Dynamic 客户端辅助
│   │   └── testdata/                 # 测试数据
│   ├── payload/                      # ★ 清单加载 + Task Graph
│   │   ├── payload.go                # LoadUpdate, State 枚举, Update 结构体
│   │   ├── task_graph.go             # ★ TaskGraph DAG + RunGraph
│   │   ├── task.go                   # Task.Run + UpdateError + 重试
│   │   ├── precondition/             # 前置条件检查
│   │   │   └── clusterversion/       # Rollback, GiantHop, Upgradeable, RecommendedUpdate
│   │   └── ...
│   ├── cincinnati/                   # OSUS/Cincinnati gRPC 客户端
│   ├── clusterconditions/            # 条件更新匹配 (PromQL, Always)
│   ├── risk/                         # ★ 风险聚合树
│   │   ├── aggregate.go              # 聚合多个 risk.Source
│   │   ├── adminack.go               # Admin 确认风险
│   │   ├── alert.go                  # Prometheus 告警风险
│   │   ├── deletion.go               # 资源删除风险
│   │   └── clusteroperator.go        # ClusterOperator Upgradeable 风险
│   ├── featuregates/                 # Feature Gate 处理 + ChangeStopper
│   ├── autoupdate/                   # 可选自动更新控制器
│   ├── agenticrun/                   # Agentic Run 控制器 (Lightspeed)
│   ├── start/                        # 启动/引导逻辑
│   └── ...
├── lib/                              # 可复用库
│   ├── resourcebuilder/              # ★ Resource Builder
│   │   ├── interface.go              # Interface + Mode 枚举 + ResourceMapper
│   │   ├── resourcebuilder.go        # builder 结构体 + Do() (GVK 巨型 switch)
│   │   ├── apps.go                   # Deployment/DaemonSet 健康检查
│   │   ├── batch.go                  # Job 健康检查
│   │   ├── core.go                   # ConfigMap/Namespace/Service 等
│   │   ├── rbac.go                   # ClusterRole/Role 等
│   │   ├── apiextensions.go          # CRD 健康检查
│   │   └── ...
│   ├── resourceapply/                # 类型化 Apply 辅助 (merge-semantic)
│   ├── resourcemerge/                # 状态条件合并辅助
│   ├── resourceread/                 # 清单读取器
│   ├── resourcedelete/              # 删除注解处理
│   ├── capability/                   # Capability 过滤
│   └── manifest/                     # 清单解析 + inclusion 过滤
├── docs/dev/                         # 开发文档
└── AGENTS.md                         # AI 助手指导
```

### 4.2 入口与启动流程

入口 `cmd/cluster-version-operator/main.go` 使用 cobra，`start` 子命令委托给 `pkg/start/start.go`。

**启动流程** (`Options.Run` → `Options.run`)：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        CVO 启动流程                                              │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. newClientBuilder                                                            │
│     ├── defaultQPS:  QPS=20, Burst=40  (通用 API 调用)                          │
│     └── highQPS:    QPS=40, Burst=80  (Payload 初始化)                          │
│     └── useProtobuf: protobuf 内容协商 (减少带宽)                                │
│                                                                                 │
│  2. createResourceLock                                                          │
│     └── resourcelock.LeasesResourceLock (coordination lease)                    │
│     └── identity = hostname + random UUID                                       │
│                                                                                 │
│  3. prepareConfigInformerFactories                                              │
│     ├── clusterVersionConfigInformerFactory (按 ClusterVersion 名称过滤)        │
│     └── configInformerFactory (通用)                                            │
│                                                                                 │
│  4. processInitialFeatureGate (同步阻塞)                                        │
│     ├── 启动 config informer factory，等待最多 30s 缓存同步                      │
│     ├── 读取 cluster FeatureGate 确定 startingFeatureSet                        │
│     ├── 确定 cvoGates (CVO 自身 feature gates)                                  │
│     └── getOpenShiftVersion(): 从 release-metadata 读取 CVO 版本                │
│                                                                                 │
│  5. NewControllerContext                                                        │
│     ├── 创建 informers (openshift-config, openshift-config-managed,             │
│     │   ClusterVersionOperatorConfiguration)                                    │
│     ├── tls.NewProfileManager (监听 APIServers TLS 配置)                        │
│     ├── cvo.New(...) (28 个参数) ★                                              │
│     ├── featuregates.NewChangeStopper                                           │
│     └── autoupdate.New (如果启用)                                               │
│                                                                                 │
│  6. getLeaderElectionConfig                                                     │
│     ├── 默认配置 (OpenShift 约定)                                               │
│     └── SNO (SingleReplicaTopologyMode): LeaderElectionSNOConfig               │
│                                                                                 │
│  7. run() — 在 Leader Election 下启动                                           │
│     └── leaderelection.RunOrDie                                                 │
│         仅 leader 执行:                                                         │
│         ├── cvo.RunMetrics (HTTPS /metrics, mTLS 认证)                         │
│         ├── controllerCtx.CVO.InitializeFromPayload(restConfig, burstConfig)   │
│         ├── controllerCtx.CVO.Run(runContext, shutdownContext) ★                │
│         ├── StopOnFeatureGateChange.Run (Feature Set 变更时关闭 CVO)            │
│         └── AutoUpdate.Run (如果启用)                                           │
│                                                                                 │
│  8. 优雅关闭                                                                    │
│     ├── runContext.Done() → 2 分钟 shutdownContext                              │
│     └── 最后执行一次 sync() 刷新状态                                             │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 5. Operator 主控制器 (`pkg/cvo/cvo.go`)

### 5.1 Operator 结构体

```go
const maxRetries = 15

type Operator struct {
    nodename string
    namespace, name string                 // ClusterVersion + OperatorStatus 位置
    release configv1.Release               // 当前 release + metadata
    releaseCreated time.Time

    client         clientset.Interface      // openshift config clientset
    kubeClient     kubernetes.Interface
    dynamicClient  dynamic.Interface
    operatorClient operatorclientset.Interface
    eventRecorder  record.EventRecorder

    minimumUpdateCheckInterval time.Duration
    architecture string
    payloadDir   string
    updateService string                     // 覆盖 ClusterVersion spec.upstream

    // Listers
    cvLister, coLister          configlistersv1.*Lister
    cmConfigLister, cmConfigManagedLister listerscorev1.ConfigMapNamespaceLister
    proxyLister, featureGateLister configlistersv1.*Lister
    cacheSynced []cache.InformerSynced

    // 工作队列
    queue                 workqueue.TypedRateLimitingInterface[any]  // "clusterversion"
    availableUpdatesQueue workqueue.TypedRateLimitingInterface[any]  // "availableupdates"

    statusLock sync.Mutex
    availableUpdates *availableUpdates

    // 风险聚合
    upgradeable risk.Source                // → OperatorUpgradeable 条件
    conditionRegistry clusterconditions.ConditionRegistry

    // 签名验证
    verifier verify.Interface
    signatureStore *verify.StorePersister

    // 同步工作器
    configSync ConfigSyncWorker
    statusInterval time.Duration           // 15s

    // Feature Gates
    requiredFeatureSet configv1.FeatureSet
    enabledCVOFeatureGates featuregates.CvoGateChecker
    enabledManifestFeatureGates sets.Set[string]

    clusterProfile string
    alwaysEnableCapabilities []configv1.ClusterVersionCapability
    configuration *configuration.ClusterVersionOperatorConfiguration
    risks risk.Source                      // 聚合: alert + upgradeable
    agenticRunController *agenticrun.Controller
}
```

### 5.2 事件监听与队列

`New()` 构造函数注册以下事件处理器：

| 监听对象 | 事件处理器 | 行为 |
|---------|-----------|------|
| `ClusterVersion` | `clusterVersionEventHandler` | Add/Update/Delete 时重排 `queue` 和 `availableUpdatesQueue` |
| `ClusterOperator` | `clusterOperatorEventHandler` | 版本或 Available/Degraded 状态变更时通知 `configSync` |
| `FeatureGate` | `featureGateEventHandler` | 更新 `enabledManifestFeatureGates`，变更时重排 `queue` |
| `ConfigMap` (openshift-config) | risk sources | AdminAck 风险变更时重排 `availableUpdatesQueue` |

### 5.3 风险聚合树

```go
upgradeable = aggregate.New(
    updatingrisk     "ClusterVersionUpdating"      // 监听 CV 升级状态
    overridesrisk    "ClusterVersionOverrides"     // 监听 CV overrides
    deletionrisk     "ResourceDeletionInProgress"  // 使用 currentVersion()
    adminack         "AdminAck"                     // 监听 managed+config ConfigMaps
    upgradeablerisk  "ClusterOperatorUpgradeable"   // 监听 ClusterOperators
)
risks = aggregate.New(
    alert.New("Alert", promqlTarget),               // Prometheus 告警
    upgradeable,
)
```

每个 risk source 注册回调，变更时重排 `availableUpdatesQueue`。聚合结果用于：
- `ClusterVersion.status.conditions[Upgradeable]` 条件
- 条件更新的 `Recommended` 评估

### 5.4 主同步循环 — `sync()`

```go
func (optr *Operator) sync(ctx, key string) error
```

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        CVO sync() 主循环                                        │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. getClusterVersion()                                                         │
│     └── 从 lister 读取 ClusterVersion                                           │
│     └── rememberLastUpdate: 跟踪 lastResourceVersion，跳过过期事件              │
│                                                                                 │
│  2. validation.ValidateClusterVersion + ClearInvalidFields                      │
│     └── 校验 ClusterVersion spec 合法性                                         │
│                                                                                 │
│  3. findUpdateFromConfig(config, release.Architecture)                          │
│     ├── 解析 spec.desiredUpdate 为具体 Update                                   │
│     ├── 如果 desiredUpdate.Architecture == Multi 且当前非 Multi                  │
│     │   → 使用当前版本 (仅有效的 Multi 升级)                                     │
│     └── 如果 image 缺失 → 从 status.availableUpdates 查找版本                   │
│                                                                                 │
│  4. 检查 configSync.Initialized()                                               │
│     └── 如果未初始化 → 默认 desired = 当前版本，重排队等待                       │
│                                                                                 │
│  5. 确定 Payload State ★                                                        │
│     ├── hasNeverReachedLevel (history 无 CompletedUpdate)                       │
│     │   → payload.InitializingPayload (首次安装)                                 │
│     ├── hasReachedLevel(config, desired) (history[0] Completed + image 匹配)   │
│     │   → payload.ReconcilingPayload (常规协调)                                  │
│     └── 否则                                                                    │
│         → payload.UpdatingPayload (版本升级)                                    │
│                                                                                 │
│  6. optr.configSync.Update(ctx, generation, desired, config, state, featureGates)│
│     └── 返回 *SyncWorkerStatus                                                  │
│                                                                                 │
│  7. optr.syncStatus(ctx, original, config, status, errs)                        │
│     └── 合成并写入 ClusterVersion.status                                        │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 5.5 Run() — goroutine 模型

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        CVO Run() goroutine 模型                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Run(runContext, shutdownContext) 启动以下 goroutine (通过 asyncResult 收集):   │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐     │
│  │ 1. status notifier                                                      │     │
│  │    runThrottledStatusNotifier: 每 statusInterval (15s) 重排 queue       │     │
│  │    当 configSync.StatusCh() 发射时                                      │     │
│  └────────────────────────────────────────────────────────────────────────┘     │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐     │
│  │ 2. sync worker                                                          │     │
│  │    configSync.Start(runContext, 16)  // maxWorkers=16                   │     │
│  └────────────────────────────────────────────────────────────────────────┘     │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐     │
│  │ 3. available updates worker                                             │     │
│  │    wait.Until: worker(availableUpdatesQueue, availableUpdatesSync)      │     │
│  │    每 1s 执行一次                                                        │     │
│  └────────────────────────────────────────────────────────────────────────┘     │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐     │
│  │ 4. CVO configuration (如果 CVOConfiguration gate 启用 且非 hypershift)   │     │
│  │    configuration.Start 然后 worker(configuration.Queue, configuration.Sync)│  │
│  └────────────────────────────────────────────────────────────────────────┘     │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐     │
│  │ 5. agenticrun controller                                                │     │
│  │    worker(agenticRunController.Queue, agenticRunController.Sync)        │     │
│  └────────────────────────────────────────────────────────────────────────┘     │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐     │
│  │ 6. cluster version sync ★                                               │     │
│  │    worker(queue, sync)                                                  │     │
│  │    关闭时执行最后一次 sync() 刷新状态                                    │     │
│  └────────────────────────────────────────────────────────────────────────┘     │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐     │
│  │ 7. signature store (如果设置)                                           │     │
│  │    signatureStore.Run(ctx, minInterval*2)                               │     │
│  └────────────────────────────────────────────────────────────────────────┘     │
│                                                                                 │
│  worker 模式:                                                                   │
│    for {                                                                        │
│      item := queue.Get()                                                        │
│      sync(ctx, item)                                                            │
│      if err && NumRequeues < maxRetries (15):                                  │
│        queue.AddRateLimited(item)                                               │
│      else if err:                                                               │
│        syncFailingStatus(err)  // 写入失败状态                                  │
│        queue.Forget(item)                                                       │
│    }                                                                            │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 6. SyncWorker 状态机 (`pkg/cvo/sync_worker.go`)

### 6.1 接口定义

```go
type ConfigSyncWorker interface {
    Start(ctx context.Context, maxWorkers int)
    Update(ctx context.Context, generation int64, desired configv1.Update,
           config *configv1.ClusterVersion, state payload.State,
           enabledFeatureGates sets.Set[string]) *SyncWorkerStatus
    StatusCh() <-chan SyncWorkerStatus
    NotifyAboutManagedResourceActivity(msg string)
    Initialized() bool
}
```

### 6.2 SyncWork (在途期望状态)

```go
type SyncWork struct {
    Generation int64
    Desired    configv1.Update
    Overrides  []configv1.ComponentOverride
    State      payload.State
    Completed  int     // 连续成功次数
    Attempt    int     // 失败重试次数 (成功或目标变更时重置)
    Capabilities capability.ClusterCapabilities
    EnabledFeatureGates sets.Set[string]
}
```

### 6.3 SyncWorkerStatus (回报给 Operator)

```go
type SyncWorkerStatus struct {
    Generation int64
    Failure error
    Done, Total, Completed int
    Reconciling, Initial bool
    VersionHash, Architecture string
    LastProgress time.Time
    Actual configv1.Release
    Verified bool
    CapabilitiesStatus CapabilityStatus
    EnabledFeatureGates sets.Set[string]
}
```

### 6.4 状态机

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        SyncWorker 状态机                                        │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│                   ┌─────────────┐                                               │
│                   │   Initial    │  (等待首次 Update() 调用)                    │
│                   └──────┬───────┘                                               │
│                          │ Update()                                              │
│                          ▼                                                       │
│                   ┌─────────────┐    apply 出错     ┌───────────┐               │
│           ┌──────→│    Sync      │──────────────→  │   Error    │               │
│           │       └──────┬───────┘                 └─────┬─────┘               │
│           │              │ apply 成功                     │ backoff              │
│           │              ▼                                ▼                     │
│           │       ┌───────────────┐               ┌───────────┐               │
│           │       │  Reconciling   │←── Update(diff)──│   Sync      │               │
│           │       │  (每 minInterval)│               └───────────┘               │
│           │       └──────┬────────┘    apply 出错         ▲                     │
│           │              │ Update(diff)                      │                     │
│           │              ▼                                   │                     │
│           │       ┌───────────────┐    apply 出错     ┌───────────┐               │
│           └───────│    Sync        │──────────────→  │   Error    │               │
│                   └───────────────┘                 └───────────┘               │
│                                                                                 │
│  Update() 边沿触发:                                                             │
│  ├── 比较 SyncWork (version, overrides, capabilities, feature gates)            │
│  ├── 如果变更 → loadUpdatedPayload → syncPayload → 信号 startApply channel      │
│  └── 如果未变更 → 检查 payload 状态是否需重置                                    │
│                                                                                 │
│  syncPayload (加载+验证 payload):                                               │
│  ├── retriever.RetrievePayload (解锁期间执行长 IO)                              │
│  ├── payload.LoadUpdate(dir, image, exclude, featureSet, profile, ...)          │
│  ├── 版本校验: payload 版本必须匹配 desired 版本                                │
│  ├── 前置条件检查 (本地 payload 跳过):                                          │
│  │   precondition.Summarize(RunAll(ctx, ReleaseContext{DesiredVersion}), force) │
│  │   → 阻塞错误 或 已接受风险警告                                                │
│  └── GetImplicitlyEnabledCapabilities (新 payload 禁用但现有资源需要的 caps)    │
│                                                                                 │
│  apply 错误处理:                                                                │
│  ├── backoff: Duration=minInterval/16, Factor=2, Steps=4, Cap=minInterval      │
│  ├── 4 次错误后 backoff 上限为 minInterval (0.2 jitter)                        │
│  ├── Completed=0, Attempt++                                                    │
│  └── 成功后 Completed++, Attempt=0, State→ReconcilingPayload                   │
│                                                                                 │
│  同步超时 (按 State):                                                           │
│  ├── Initializing: minInterval (频繁快照)                                      │
│  └── Updating/Reconciling: minInterval*2                                       │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 6.5 apply() — 清单应用核心

```go
func (w *SyncWorker) apply(ctx context.Context, work *SyncWork, maxWorkers int,
    previousStatus *SyncWorkerStatus) error
```

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        apply() 清单应用核心                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. payload.InitCOUpdateStartTimes()  (attempt=0 时)                            │
│     └── 重置 ClusterOperator 更新开始时间追踪                                    │
│                                                                                 │
│  2. 构建 consistentReporter (线程安全状态更新器)                                 │
│                                                                                 │
│  3. 创建 []*payload.Task (每个清单一个 Task)                                     │
│     └── Task.Backoff: Initializing={Steps:4,Factor:2,Duration:1s,Cap:15s}      │
│                                                                                 │
│  4. 构建 TaskGraph ★                                                            │
│     graph := payload.NewTaskGraph(tasks)                                        │
│     graph.Split(payload.SplitOnJobs)   // Job 断开并行性                         │
│                                                                                 │
│     然后按 State 选择并行化策略:                                                 │
│     ├── InitializingPayload:                                                    │
│     │   graph.Parallelize(payload.FlattenByNumberAndComponent)                  │
│     │   maxWorkers = len(graph.Nodes)  // 全并行                                │
│     │   → 快速达到稳态                                                           │
│     │                                                                           │
│     ├── ReconcilingPayload:                                                     │
│     │   graph.Parallelize(payload.ShiftOrder(                                   │
│     │       payload.PermuteOrder(                                               │
│     │           payload.FlattenByNumberAndComponent, r),                        │
│     │       iteration, steps))                                                  │
│     │   maxWorkers = 2  // 仅 2 个 worker                                       │
│     │   → 随机排列，8 次连续尝试覆盖整个 payload                                 │
│     │                                                                           │
│     └── UpdatingPayload (默认):                                                 │
│         graph.Parallelize(payload.ByNumberAndComponent)                         │
│         maxWorkers = 16  // 有序升级                                             │
│         → 按 payload 序号有序滚动                                                │
│                                                                                 │
│  5. payload.RunGraph(ctx, graph, maxWorkers, func(ctx, tasks) error {...})     │
│     第一遍 (Precreating 模式):                                                  │
│     ├── 预创建 ClusterOperator manifests (不重试)                               │
│     └── 提供 must-gather 可见性                                                  │
│                                                                                 │
│     第二遍 (实际应用):                                                          │
│     ├── task.Manifest.Include(...) 过滤 (capabilities, overrides, feature gates)│
│     ├── task.Run(ctx, version, builder, state)                                  │
│     │   └── wait.ExponentialBackoffWithContext 重试 builder.Apply               │
│     │       ├── *UpdateError 失败不重试 (快速失败)                               │
│     │       └── 其他错误按 reason 映射                                          │
│     └── UpdateEffectReport 错误收集 (非致命)                                    │
│                                                                                 │
│  6. 图错误汇总                                                                  │
│     summarizeTaskGraphErrors:                                                   │
│     ├── 过滤 context 错误                                                       │
│     ├── condenseClusterOperators (统一同 reason 的 CO 错误)                     │
│     └── newMultipleError 包装                                                   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 7. Task Graph DAG (`pkg/payload/task_graph.go`)

### 7.1 数据结构

```go
type TaskNode struct {
    In    []int       // 前置节点索引
    Tasks []*Task
    Out   []int       // 依赖节点索引
}

type TaskGraph struct {
    Nodes []*TaskNode
}
```

### 7.2 图构建方法

| 方法 | 作用 |
|------|------|
| `NewTaskGraph(tasks)` | 创建单节点图 (包含所有 Task) |
| `Split(onFn)` | 在匹配 `onFn` 的 Task 处分裂为 `[before] → [match] → [after]`，保持顺序 |
| `SplitOnJobs` | 匹配 `batch/v1 Job` 的分裂函数 |
| `Parallelize(breakFn)` | 将节点 Task 按断开函数分为并行组，插入间隔节点避免 M×N 边 |
| `Roots()` | 返回无前置的根节点 |

### 7.3 排序策略 (BreakFunc)

清单文件名匹配 `^0000_(\d+)_([a-zA-Z0-9]+(-[a-zA-Z0-9]+)*?)_`，提取 run level (序号) + 组件名。

| 策略 | 说明 | 适用场景 |
|------|------|---------|
| `ByNumberAndComponent` | 同序号同组件串行，同序号不同组件并行，跨序号阻塞 | UpdatingPayload (有序升级) |
| `FlattenByNumberAndComponent` | 同序号全部并行，不保持顺序 | InitializingPayload (初始安装) |
| `PermuteOrder(breakFn, r)` | 在每个步骤内随机打乱 | ReconcilingPayload (常规协调) |
| `ShiftOrder(breakFn, step, stride)` | 每个步骤旋转排列 | ReconcilingPayload (增加多样性) |

### 7.4 RunGraph 执行器

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        RunGraph 执行器                                          │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  输入: graph (DAG), maxParallelism, fn (处理函数)                               │
│                                                                                 │
│  Worker Pool:                                                                   │
│  ├── maxParallelism 个 goroutine 从 workCh 读取 TaskNode                        │
│  ├── 处理结果写入 resultCh                                                      │
│  └── nestedCtx 可取消                                                           │
│                                                                                 │
│  canVisit(node) = 所有 In 节点已执行且无错误                                     │
│                                                                                 │
│  主循环:                                                                        │
│  ├── 推送下一个可访问节点到 workCh                                               │
│  ├── 收集结果                                                                   │
│  ├── 处理 ctx 取消 (drain workCh)                                               │
│  └── 节点错误 → 依赖该节点的下游节点被跳过，但独立边继续执行 ★                   │
│                                                                                 │
│  返回 []error                                                                   │
│  └── 如果仅有未完成节点 (无错误) → 报告未完成数量 + ctx 错误                     │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.5 Run Level 分配

| Run Level | 组件 | 说明 |
|-----------|------|------|
| 00-04 | CVO 自身 | CVO 先更新自己 |
| 05 | cluster-config-operator | 集群配置 |
| 07-09 | network-operator, dns-operator, service-ca, machine-approver | 基础设施 |
| 10-29 | kube-apiserver, kube-scheduler, kube-controller-manager | Kubernetes 核心控制平面 |
| 30-39 | machine-api (MAO) | 机器 API |
| 50-59 | operator-lifecycle-manager (OLM) | OLM (Add-on Operator 管理) |
| 60-69 | openshift-apiserver, console, monitoring 等 | OpenShift 核心组件 |
| 70 | network, dns, multus 等节点级组件 | 最大破坏性节点级 daemonset |
| 80 | machine-config-operator (MCO), cloud-operators | 节点操作系统更新 |
| 90 | post-machine-update 组件 | 节点更新后的组件 |

**升级顺序**:

```txt
config-operator
  → kube-apiserver
    → kube-controller-manager / kube-scheduler
      → 重要内部 API (cloud-credential-operator, openshift-apiserver)
        → 非破坏性组件 (并行)
          → OLM
            → 破坏性节点级 daemonset (network, dns)
              → 节点升级 (MCO, MAO, cloud-operators)
```

> **N-1 兼容规则**：所有组件必须 N-1 minor 版本兼容 (4.y 和 4.y-1)。组件必须先更新 Operator 自身，再更新其 Operand。所有 Operator 和控制平面组件必须能长时间与 N-1 版本的 Operand 共存并在此场景下测试。

---

## 8. Resource Builder 模式 (`lib/resourcebuilder/`)

### 8.1 接口与模式

```go
type Mode int
const (
    UpdatingMode Mode = iota      // 有序升级
    ReconcilingMode               // 常规协调
    InitializingMode              // 初始安装
    PrecreatingMode               // 预创建 (ClusterOperator 可见性)
)

type Interface interface {
    WithModifier(MetaV1ObjectModifierFunc) Interface
    WithMode(Mode) Interface
    Do(context.Context) error
}
```

### 8.2 GVK 注册表 (ResourceMapper)

```go
type ResourceMapper struct {
    l *sync.Mutex
    gvkToNew map[schema.GroupVersionKind]NewInterfaceFunc
}
var Mapper = NewResourceMapper()  // 全局默认注册表
```

**已注册的 GVK**：

| GVK | 处理方式 |
|-----|---------|
| ValidatingWebhookConfiguration | 类型化 apply + health check |
| CustomResourceDefinition | 类型化 apply + 阻塞直到 Established |
| DaemonSet | 类型化 apply + 节点级 health check |
| Deployment | 类型化 apply + 副本级 health check |
| CronJob / Job | 类型化 apply + Job 阻塞直到成功 |
| ConfigMap / Namespace / Service / ServiceAccount | 类型化 apply |
| ImageStream | 类型化 apply |
| OperatorGroup | 类型化 apply |
| ClusterRole / ClusterRoleBinding / Role / RoleBinding | 类型化 apply |
| SecurityContextConstraints | 类型化 apply |
| **ClusterOperator** ★ | 特殊处理 (见 §8.4)，**不在全局 Mapper 中注册** |

### 8.3 builder.Do() — 类型化分发

```go
func (b *builder) Do(ctx context.Context) error {
    // 1. 应用 modifier (owner references 等)
    // 2. resourcedelete.Delete<Type>: 检查删除注解，如果请求删除则停止
    // 3. resourceapply.Apply<Type>: merge-semantic apply
    // 4. 对于有 health check 的类型: check<Type>Health
}
```

**Health Check 行为**：

| 类型 | 初始推送 (generation=1) | 后续更新 |
|------|------------------------|---------|
| Deployment | 不阻塞 | 阻塞直到 observedGeneration 追上 + 足够 Pod + 无 unavailable replicas |
| DaemonSet | 不阻塞 | 阻塞直到 observedGeneration 追上 + 每节点有 Ready Pod + 无未覆盖节点 |
| Job | 阻塞直到成功 | 同初始 (无法 delete/recreate) |
| CRD | 阻塞直到 Established | 同初始 |

### 8.4 ClusterOperator Builder (`pkg/cvo/internal/operatorstatus.go`)

CVO 对 ClusterOperator 有特殊处理，因为 CVO **不创建** ClusterOperator CR (由 Operator 自身创建)，仅监听其状态作为阻塞门。

```go
type clusterOperatorBuilder struct {
    getter    ClusterOperatorsGetter    // cache-backed 读取
    client    configclient.ClusterOperatorInterface
    manifest  manifest.Manifest
    mode      resourcebuilder.Mode
    modifier  resourcebuilder.MetaV1ObjectModifierFunc
}
```

**`Do(ctx)` 按 Mode 分发**：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                ClusterOperator Builder Do() 逻辑                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  PrecreatingMode:                                                               │
│  ├── Create ClusterOperator (IgnoreAlreadyExists)                               │
│  ├── 设置 Status.RelatedObjects                                                 │
│  ├── UpdateStatus (IgnoreConflict)                                              │
│  └── 不做 health check (仅确保对象存在)                                         │
│                                                                                 │
│  ReconcilingMode:                                                               │
│  ├── Get 现有 ClusterOperator                                                   │
│  ├── EnsureObjectMeta (合并 labels/annotations)                                 │
│  └── Update (如果 metadata 变更)                                                │
│                                                                                 │
│  所有模式 (除 Precreating) 执行 checkOperatorHealth ★:                          │
│                                                                                 │
│  checkOperatorHealth(ctx, getter, expected, mode):                              │
│  ├── 1. 如果 expected.Status.Versions 为空                                      │
│  │   → ClusterOperatorNoVersions (UpdateEffectFail)                             │
│  │                                                                              │
│  ├── 2. 比较期望版本 vs 实际版本 → 构建 undone 列表                              │
│  │                                                                              │
│  ├── 3. 读取条件:                                                               │
│  │   ├── Available (True?)                                                      │
│  │   ├── Progressing (False?)                                                   │
│  │   └── Degraded (False?)                                                      │
│  │                                                                              │
│  ├── 4. 如果 not Available                                                      │
│  │   → ClusterOperatorNotAvailable (UpdateEffectFail)                           │
│  │                                                                              │
│  ├── 5. 如果 Degraded:                                                          │
│  │   ├── InitializingMode → UpdateEffectReport (不阻塞安装)                     │
│  │   └── 其他 → UpdateEffectFailAfterInterval (40 分钟后失败)                   │
│  │                                                                              │
│  ├── 6. 如果 undone > 0 且非 Initializing:                                      │
│  │   → ClusterOperatorUpdating (UpdateEffectNone — 抑制失败)                    │
│  │   └── 30 分钟后 (machine-config 90 分钟) → Slow:: 前缀 (仍抑制)             │
│  │                                                                              │
│  └── 7. 成功 → payload.COUpdateStartTimesRemove(co.Name)                       │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 8.5 UpdateError 语义

```go
type UpdateEffectType string
const (
    UpdateEffectReport          // 报告但不阻止协调完成
    UpdateEffectNone            // 无效果 (如 CO 仍在更新 — "waiting on X")
    UpdateEffectFail            // 更新失败
    UpdateEffectFailAfterInterval  // 如果持续超过间隔则失败 (40 分钟)
)
```

| UpdateEffectType | 行为 | 超时处理 |
|------------------|------|---------|
| `Report` | 报告但不阻塞 | 无 |
| `None` | 抑制失败 (CO 正在更新) | 30 分钟后 (MCO 90 分钟) 加 `Slow::` 前缀，仍抑制 |
| `Fail` | 立即失败 | 无 |
| `FailAfterInterval` | 40 分钟内抑制 | 40 分钟后转为失败 |

---

## 9. Payload 加载与状态 (`pkg/payload/payload.go`)

### 9.1 Payload State 枚举

```go
type State int
const (
    UpdatingPayload State = iota     // 保守排序，错误阻塞依赖
    ReconcilingPayload               // 重建资源，无严格顺序
    InitializingPayload              // 首次部署，快速，容忍瞬态错误
    PrecreatingPayload               // 选择性首遍创建 (可见性)
)
```

### 9.2 Update (内存 Payload)

```go
type Update struct {
    Release      configv1.Release
    VerifiedImage bool
    LoadedAt     time.Time
    ImageRef     *imagev1.ImageStream
    Architecture string
    ParsedVersion semver.Version
    ManifestHash string             // 清单 Raw 字节的 FNV-64 哈希
    Manifests    []manifest.Manifest
}
```

### 9.3 LoadUpdate 流程

```txt
LoadUpdate(dir, releaseImage, excludeIdentifier, requiredFeatureSet, profile, knownCapabilities, enabledFeatureGates)
│
├── 1. 验证目录结构: manifests/ + release-manifests/ + release-metadata + image-references
│
├── 2. loadPayloadMetadata:
│   ├── 读取 release-metadata (Cincinnati cincinnati-metadata-v0 JSON)
│   ├── 解析 semver 版本
│   └── 加载 image-references ImageStream (name 必须 == version)
│
├── 3. loadPayloadTasks:
│   ├── manifests/ (CVO 清单) → renderManifest 模板渲染
│   │   模板变量: .ReleaseImage, .ClusterProfile, .Images
│   └── release-manifests/ (其他 Operator 清单) → 无渲染
│       跳过 release-metadata + image-references
│
├── 4. 解析文件 (.yaml/.yml/.json)
│   └── manifest.ParseManifests, 设置 OriginalFilename
│
├── 5. Inclusion 过滤
│   manifest.Include(&exclude, &requiredFeatureSet, &profile, onlyKnownCaps, nil, featureGates, &majorVersion)
│   ├── 按 exclude identifier 跳过
│   ├── 按 feature set 跳过
│   ├── 按 cluster profile 跳过
│   ├── 按 known capabilities 跳过
│   └── 按 enabled feature gates 跳过
│   模板渲染失败 → 跳过并警告 (旧 CVO 加载新 payload)
│
└── 6. 计算 ManifestHash (FNV-64, base64url)
```

### 9.4 Release Image 内容

```txt
$ oc image extract quay.io/openshift-release-dev/ocp-release:4.15.0-x86_64 --path /:/tmp/release

$ ls /tmp/release/release-manifests
0000_03_authorization-openshift_01_rolebindingrestriction.crd.yaml
0000_03_config-operator_01_operatorhub.crd.yaml
0000_03_config-operator_01_proxy.crd.yaml
0000_05_config-operator_02_apiserver.cr.yaml
0000_05_config-operator_02_authentication.cr.yaml
...
0000_50_cluster-olm-operator_00_namespace.yaml
0000_50_cluster-olm-operator_01_clusterserviceversion.crd.yaml
...
0000_80_machine-config-operator_00_namespace.yaml
...
image-references
release-metadata
```

**release-metadata**:

```json
{
  "kind": "cincinnati-metadata-v0",
  "version": "4.15.0",
  "previous": ["4.14.5", ..., "4.15.0-rc.0"],
  "metadata": {
    "description": "",
    "url": "https://access.redhat.com/errata/RHBA-2024:XXXX"
  }
}
```

**image-references**: ImageStream，包含所有核心组件的镜像引用 (用于 `oc adm release mirror` 镜像)。

---

## 10. Release Image 抽包 (`pkg/cvo/updatepayload.go`)

### 10.1 PayloadRetriever

```go
type payloadRetriever struct {
    releaseImage, payloadDir string          // 本地 payload (operator image)
    kubeClient kubernetes.Interface
    workingDir string                        // "/etc/cvo/updatepayloads"
    namespace, nodeName, operatorName string
    verifier verify.Interface
    downloader downloadFunc                  // 可 mock
    retrieveTimeout time.Duration            // 4 分钟
}
```

### 10.2 RetrievePayload 流程

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        RetrievePayload 流程                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. 如果 update.Image == releaseImage (本地 payload)                             │
│     └── 返回 PayloadInfo{Directory: payloadDir, Local: true}                    │
│                                                                                 │
│  2. 签名验证 (半个 deadline 时间)                                                │
│     ├── verifier.Verify                                                         │
│     ├── 失败 → ImageVerificationFailed UpdateError                               │
│     │   └── 除非 update.Force → 设置 VerificationError，继续                     │
│     └── 成功 → 继续                                                             │
│                                                                                 │
│  3. 确定目标目录                                                                │
│     targetUpdatePayloadDir = md5(image) 在 workingDir 下                        │
│     ├── prunePods: 清理旧 Pod                                                   │
│     ├── fetchUpdatePayloadToDir: 如果目录不存在                                  │
│     └── ValidateDirectory: 验证目录内容                                         │
│                                                                                 │
│  4. fetchUpdatePayloadToDir — ★ Pod-based 抽包                                  │
│     创建 Pod:                                                                   │
│     ├── ServiceAccount: update-payload                                          │
│     ├── nodeSelector: node-role.kubernetes.io/master                            │
│     ├── privileged: true                                                        │
│     ├── hostPath: /etc/cvo/updatepayloads                                       │
│     ├── ActiveDeadlineSeconds: 120                                              │
│     ├── priority: openshift-user-critical                                      │
│     ├── Image: release image                                                    │
│     ├── Init containers:                                                        │
│     │   ├── cleanup                                                             │
│     │   ├── make-temporary-directory                                            │
│     │   ├── copy-operator-manifests-to-temporary-directory                      │
│     │   └── copy-release-manifests-to-temporary-directory                       │
│     └── Container: rename-to-final-location (atomic mv)                         │
│                                                                                 │
│     waitForPodCompletion:                                                       │
│     ├── field selector on name                                                 │
│     ├── toolswatch.UntilWithSync                                               │
│     └── 提前捕获 Pending 失败 (SignatureValidationFailed, ErrImagePull)        │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 11. 前置条件检查 (`pkg/payload/precondition/`)

```go
type Precondition interface {
    Run(ctx context.Context, ReleaseContext) error
    Name() string
}
```

**CVO 默认前置条件** (`defaultPreconditionChecks`)：

| 前置条件 | 说明 | 跳过条件 |
|---------|------|---------|
| `Rollback` | 阻止回滚到旧版本 | 本地 payload |
| `GiantHop` | 阻止跨多个 minor 版本跳跃 | 本地 payload |
| `Upgradeable` | 检查 Upgradeable 条件 | 本地 payload |
| `RecommendedUpdate` | 检查是否为 OSUS 推荐更新 | 本地 payload |

`Summarize(errs, force)` → `(blocking bool, error)`：如果 `force=true` 且 blocking → 转为警告 ("Forced through blocking failures")。返回 `UpgradePreconditionCheckFailed` UpdateError。

---

## 12. OSUS / Cincinnati 升级图 (`pkg/cvo/availableupdates.go`)

### 12.1 工作流程

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        OSUS 升级图推荐流程                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  availableUpdatesSync(ctx, key):                                                │
│                                                                                 │
│  1. 更新服务解析                                                                │
│     --update-service flag > spec.upstream > 默认                                │
│     默认: https://api.openshift.com/api/upgrades_info/v1/graph                  │
│                                                                                 │
│  2. 节流查询                                                                    │
│     ├── 按 minimumUpdateCheckInterval 节流                                      │
│     └── 除非 channel/arch/upstream 变更 或 24h 已过                              │
│                                                                                 │
│  3. calculateAvailableUpdatesStatus                                             │
│     ├── 校验 clusterID (UUID)                                                   │
│     ├── 校验 architecture, version (semver), channel                            │
│     └── cincinnati.NewClient(...).GetUpdates(ctx, uri, arch, currentArch,       │
│         channel, currentVersion)                                                │
│         → 返回 (current, updates, conditionalUpdates, condition)                │
│                                                                                 │
│  4. evaluateConditionalUpdates                                                  │
│     ├── shouldReconcileAcceptRisks → 合并集群内风险 (risk.Merge)                │
│     ├── loadRiskVersions (每个风险的最新版本)                                    │
│     ├── loadRiskConditions (通过 conditionRegistry.Match 评估 MatchingRules)    │
│     │   └── Match / NotMatch / EvaluationFailed                                 │
│     └── evaluateConditionalUpdate:                                              │
│         ├── 设置 Recommended metav1.Condition:                                  │
│         │   ├── True: 无风险 (NotExposedToRisks) 或全部已接受 (AllExposedRisksAccepted)│
│         │   └── False: 有未接受风险 (reason = risk name)                         │
│         └── HyperShift: injectClusterIdIntoConditionalUpdates                   │
│            └── PromQL _id="" → _id="<clusterId>"                                │
│                                                                                 │
│  5. 写入 ClusterVersion.status.availableUpdates 和 conditionalUpdates           │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 12.2 Cincinnati 图模型

OSUS 提供一个有向图：
- **顶点 (vertices)** = 更新 payload (代表集群组件的期望状态)
- **边 (edges)** = 安全的升级路径

CVO 基于当前版本在图中查找可到达的顶点，即为可用更新。

### 12.3 频道 (Channel)

频道通过 `spec.channel` 配置 (如 `stable-4.15`)。如果当前版本不在频道图中，报告 `VersionNotFound`。

---

## 13. Status 合成 (`pkg/cvo/status.go`)

### 13.1 条件合成

`updateClusterVersionStatus` 合成以下条件：

| 条件 | 来源 | 含义 |
|------|------|------|
| `ClusterVersionInvalid` | 验证错误 | ClusterVersion spec 无效 |
| `ImplicitlyEnabledCapabilities` | Capability 分析 | 无法禁用的 caps |
| `ReleaseAccepted` | loadPayloadStatus | Release 是否被接受 |
| `OperatorAvailable` | sync worker | `status.Completed > 0` 时 True |
| `ClusterStatusFailing` | sync worker 失败 | True 表示协调失败；`Slow::` 前缀 → Unknown |
| `OperatorProgressing` | sync worker | "Working towards X: N of M done (P% complete)" 或 "Cluster version is X" |
| `RetrievedUpdates` | OSUS | 是否成功获取更新 |
| `OperatorUpgradeable` | risk 聚合 | 聚合多个风险源 |

### 13.2 进度消息格式

| 状态 | Progressing | 消息示例 |
|------|------------|---------|
| 升级中 | True | `Working towards 4.15.0: 105 of 312 done (34% complete)` |
| 协调失败 | True | `Unable to apply 4.15.0: could not update 0000_70_network_deployment.yaml because...` |
| 协调中失败 | False | `Error while reconciling 4.15.0: ...` |
| 完成 | False | `Cluster version is 4.15.0` |

### 13.3 历史剪枝 (`status_history.go`)

`prune(history, MaxHistory=100)` 按权重排序：

| 条目类型 | 权重 | 说明 |
|---------|------|------|
| 初始条目 + 索引 0-4 + 最近 completed | 1000 | 最重要，必须保留 |
| minor 中首个 completed | 30 | 保留 |
| minor 中最后 completed | 30 | 保留 |
| minor 转换的 partial | 20 | 保留 |
| z-stream partial | -20 | 优先删除 |
| 索引位置 | -1.01 | 越近越优先 |

---

## 14. 完整升级工作流 (12 步)

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        CVO 完整升级工作流 (12 步)                               │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. CVO 休眠 (set duration + jitter)                                            │
│                                                                                 │
│  2. CVO 向 OSUS 检查最新升级图                                                   │
│     └── 按订阅的 channel 下载最新更新图                                          │
│                                                                                 │
│  3. CVO 确定下一个更新并写入 availableUpdates                                    │
│     └── 如果无可用更新 → 回到步骤 1                                              │
│                                                                                 │
│  4. 如果启用自动更新                                                             │
│     └── CVO 将最新更新写入 desiredUpdate                                         │
│                                                                                 │
│  5. CVO 等待 desiredUpdate 不等于当前版本                                        │
│     └── 用户通过 oc adm upgrade --to 4.15.0 触发                                 │
│                                                                                 │
│  6. CVO 指示容器运行时下载 Release Image                                         │
│     └── 通过 Pod-based 抽包 (init containers + atomic rename)                   │
│                                                                                 │
│  7. CVO 验证摘要和签名                                                           │
│     ├── 验证签名 (硬编码公钥)                                                    │
│     └── 如果无效 → 删除镜像，回到步骤 1                                          │
│                                                                                 │
│  8. CVO 验证升级路径                                                             │
│     ├── 检查 release-metadata 的 previous 版本列表                               │
│     ├── 前置条件检查 (Rollback, GiantHop, Upgradeable, RecommendedUpdate)       │
│     └── 如果不可应用 → 删除镜像，回到步骤 1                                      │
│                                                                                 │
│  9. CVO 应用自身的 Deployment ★                                                  │
│     └── 触发 Kubernetes 用新版本替换 CVO                                         │
│     └── CVO 自身被 drain 重启 (与普通 Pod 一致)                                  │
│                                                                                 │
│  10. CVO 按 Run Level 顺序应用其余 Deployment                                    │
│      ├── Run Level 00-04: CVO 自身 (步骤 9 已完成)                               │
│      ├── Run Level 05: config-operator                                          │
│      ├── Run Level 07-09: network, dns, service-ca                              │
│      ├── Run Level 10-29: kube-apiserver 等 K8s 核心                             │
│      ├── Run Level 30-39: machine-api                                           │
│      ├── Run Level 50-59: OLM                                                   │
│      ├── Run Level 60-69: openshift-apiserver, console 等                       │
│      ├── Run Level 70: 节点级组件 (network, dns daemonset)                      │
│      ├── Run Level 80: MCO (节点 OS 更新 → CVO 被 drain 重启)                    │
│      └── Run Level 90: post-machine-update                                      │
│      → 触发各 SLO 开始更新                                                       │
│                                                                                 │
│  11. CVO 等待所有 SLO 报告完成                                                   │
│      ├── 每个 ClusterOperator 必须:                                              │
│      │   ├── Available = True                                                   │
│      │   ├── versions 匹配 Release Image 声明                                    │
│      │   ├── Degraded = False                                                   │
│      │   └── Progressing = False                                                │
│      ├── 超时处理:                                                               │
│      │   ├── 30 分钟 (MCO 90 分钟): "waiting on X" (抑制失败)                   │
│      │   └── 40 分钟: UpdateEffectFailAfterInterval → Failing=True             │
│      └── MCO 更新节点 OS → CVO 被 drain 重启 → 新 CVO 继续协调 ★                │
│                                                                                 │
│  12. 回到步骤 1                                                                  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 15. CVO 自身升级 — "正常即异常"设计

### 15.1 设计哲学

CVO 自身升级时**不做特殊处理**。CVO 就是个普通 Pod，被 MCO drain 重启与内核 panic / 硬件故障处理路径完全一致。

> "By not special casing upgrading itself, the CVO restart works the same way as it would if the kernel hit a panic and froze, or the hardware died, there was an unrecoverable network partition, etc. By having the 'normal' code path work in exactly the same way as the 'exceptional' path, we ensure the upgrade process is robust and tested constantly."

### 15.2 升级中途重启

1. 如果新 release 有更新的 `machine-os-content`，CVO 拉取更新**间接导致自身重启**
2. MCO drain 每个节点，然后**重启**节点
3. CVO 是普通 Pod — 被 drain 重调度
4. 新旧 CVO Pod 间**无特殊进度传递** — 新 Pod 查看当前集群状态并协调
5. 在 `clusterversion` 对象中可见为状态 "blip"

### 15.3 仅向前升级

OpenShift 4 **不支持自动回滚**。升级是 forward-only：
- 如果升级失败，集群保持部分升级状态
- Operator 必须处理 N-1 兼容性以确保混合版本状态可用
- 强制升级 (`force: true`) 可跳过前置条件检查

---

## 16. CVO 指标 (`pkg/cvo/metrics.go`)

| 指标 | 类型 | Labels | 说明 |
|------|------|--------|------|
| `cluster_version` | Gauge | type, version, image, from_version | type: current/desired/failure/completed/cluster/updating/initial |
| `cluster_version_available_updates` | Gauge | upstream, channel | 可用更新数 |
| `cluster_version_capability` | Gauge | name | 0/1 |
| `cluster_operator_up` | Gauge | name, version, reason | 0/1 |
| `cluster_operator_conditions` | Gauge | name, condition, reason | 0/1/-1 |
| `cluster_version_risk_conditions` | Gauge | condition, risk, reason | -1/0/1 |
| `cluster_operator_condition_transitions` | Counter | name, condition | 条件转换计数 |
| `cluster_installer` | Gauge | type, version, invoker | 安装器信息 |
| `cluster_version_payload` | Gauge | version, type | type: pending/applied |
| `cluster_operator_payload_errors` | Counter | | payload 错误计数 |

`RunMetrics` 通过 HTTPS `/metrics` 端点暴露，使用 mTLS (客户端证书认证 + CN 授权到 `system:serviceaccount:openshift-monitoring:prometheus-k8s`)。

---

## 17. 核心设计模式

| 模式 | 实现位置 | 说明 |
|------|---------|------|
| **Workqueue 模式** | `pkg/cvo/cvo.go` | 两个 `TypedRateLimitingInterface` 队列 + worker `wait.Until` + `maxRetries=15` + `syncFailingStatus` |
| **单写者状态** | `sync()` | 仅主 `sync()` 写 ClusterVersion 状态；SyncWorker 通过 `StatusCh()` + throttled notifier 回报 |
| **边沿+水平触发** | `Update()` | `Update()` 通过 `startApply` channel 边沿触发；成功后按 `minimumReconcileInterval` 水平驱动 |
| **状态机 Payload 模式** | `SyncWorker` | Initializing (全并行)/Updating (有序)/Reconciling (随机排列 2 worker)/Precreating (首遍 CO 创建) |
| **Task Graph DAG** | `pkg/payload/task_graph.go` | `NewTaskGraph` → `Split(SplitOnJobs)` → `Parallelize(breakFn)` + `RunGraph` worker pool |
| **GVK 注册表+工厂分发** | `lib/resourcebuilder/` | `ResourceMapper` 映射 GVK→`NewInterfaceFunc`；CVO `builderFor` 三路分发：ClusterOperator → 注册类型 → 通用 dynamic |
| **Mode-aware Apply** | `WithMode(stateToMode)` | Builder 按 Mode 切换行为 (如 CO PrecreatingMode 仅创建，ReconcilingMode 合并 metadata) |
| **Health-check 阻塞** | `checkOperatorHealth` | ClusterOperator 阻塞直到 Available + 非 Degraded + 版本匹配，带 `UpdateEffect` 语义 |
| **风险聚合树** | `pkg/risk/` | `aggregate.New` 组合多个 `risk.Source` → `Upgradeable` 条件 + 条件更新 `Recommended` 评估 |
| **Leader Election + 优雅关闭** | `pkg/start/` | Lease 选举；仅 leader 启动 metrics + payload init + Run + ChangeStopper；2 分钟 graceful shutdown + 最终 sync() |
| **签名验证** | `verify.Interface` | 多存储 (自定义签名存储 + configmap 存储 + payload configmaps)；`force` 覆盖为已接受风险 |
| **Capability + FeatureGate 过滤** | `manifest.Include` | 加载和应用时双重过滤；FeatureGate 变更触发 payload 重载；`ChangeStopper` 在 Feature Set 变更时关闭 CVO |
| **Pod-based 抽包** | `updatepayload.go` | 远程 payload 通过特权 Pod + hostPath + init containers + atomic rename 抽包 |
| **加权历史剪枝** | `status_history.go` | 保留初始 + 最近 + minor 首尾 + 转换 partial，丢弃 z-stream partial 噪声 |
| **Pod 驱逐等价设计** | CVO 自身升级 | CVO 被 drain 重启 = 内核 panic = 硬件故障，处理路径完全一致 |

---

## 18. 设计思路总结与启示

### 18.1 CVO 管理 Operator 的核心设计思路

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              CVO 管理 Operator 的核心设计思路                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. Release Image 驱动 (Release Image-Driven)                                   │
│     • Release Payload Image = 集群期望状态的唯一真相源                           │
│     • 清单文件按序号 (Run Level) 分层定义协调顺序                               │
│     • 不可变 Release Image: 版本变更换新 Image                                   │
│                                                                                 │
│  2. 三级层次管理 (Hierarchical Management)                                      │
│     • CVO 管理 Cluster Operators (更新清单)                                     │
│     • Cluster Operators 管理 Operands (组件)                                    │
│     • CVO 不直接管理组件，通过 CO 状态门控间接协调                               │
│                                                                                 │
│  3. 状态门控协调 (Status-Gated Reconciliation)                                  │
│     • CVO 不创建 ClusterOperator CR (Operator 自身创建)                          │
│     • CVO 仅监听 CO 状态作为阻塞门                                              │
│     • 阻塞条件: Available + 版本匹配 + 非 Degraded + 非 Progressing             │
│     • 超时语义: 30min 抑制 → 40min 失败                                         │
│                                                                                 │
│  4. 清单图分层并行 (Layered Parallel Manifest Graph)                            │
│     • 按 Run Level 分层阻塞 (跨层阻塞，同层并行)                                │
│     • 三种模式: Installing(全并行) / Updating(有序) / Reconciling(随机)         │
│     • DAG: 节点错误 → 依赖节点跳过，独立边继续                                  │
│                                                                                 │
│  5. 声明式期望状态 (Declarative Desired State)                                  │
│     • 用户通过 ClusterVersion.spec.desiredUpdate 声明目标                        │
│     • CVO 持续协调到目标态                                                      │
│     • OSUS 推荐安全升级路径 (Cincinnati 图)                                      │
│                                                                                 │
│  6. 仅向前升级 (Forward-Only Upgrades)                                          │
│     • 不支持自动回滚                                                            │
│     • N-1 兼容性是强制要求                                                      │
│     • Operator 必须处理混合版本状态                                              │
│                                                                                 │
│  7. 正常即异常 (Normalcy is Exceptional)                                        │
│     • CVO 自身升级不做特殊处理                                                  │
│     • CVO Pod 被 drain 重启 = 内核 panic = 硬件故障                             │
│     • 新 Pod 查看集群状态继续协调                                                │
│                                                                                 │
│  8. 风险感知升级 (Risk-Aware Upgrades)                                          │
│     • 多风险源聚合 (CO Upgradeable, AdminAck, Alert, ResourceDeletion)         │
│     • 条件更新评估 (PromQL 匹配规则)                                             │
│     • 风险可被接受 (AcceptRisks) 或强制 (force)                                 │
│                                                                                 │
│  9. 签名验证 (Signed Releases)                                                  │
│     • CVO 验证 Release Image 签名                                               │
│     • 多签名存储 (自定义 + configmap)                                           │
│     • force 可跳过验证 (转为已接受风险)                                          │
│                                                                                 │
│  10. 标准化状态报告 (Standardized Status Reporting)                             │
│      • ClusterOperator 四条件标准化 (Available/Progressing/Degraded/Upgradeable)│
│      • 版本报告规则 (混合版本状态报告旧版本)                                    │
│      • RelatedObjects 用于 must-gather 诊断                                     │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 18.2 对 BKE 平台的启示

| CVO 设计 | BKE 对应思考 |
|----------|-------------|
| Release Payload Image (清单包) | BKE 的 ReleaseImage + bke-manifests 可借鉴 Release Image 的清单打包模式，按序号分层 |
| ClusterVersion CR (声明式期望) | BKE 的 BKECluster.Spec.DesiredVersion 可借鉴 desiredUpdate 的声明式升级触发 |
| ClusterOperator 状态门控 | BKE 的 DAG 调度器可借鉴 "阻塞等待组件状态" 的门控模式，而非直接管理组件 |
| Manifest Graph (Run Level 分层) | BKE 的 DAG 可借鉴 Run Level 分层阻塞 + 同层并行的设计 |
| 三种 Payload 模式 | BKE 可借鉴 Initializing(全并行安装)/Updating(有序升级)/Reconciling(随机协调) 的模式区分 |
| Resource Builder (GVK 分发) | BKE 的 YamlInstaller 可借鉴按 GVK 类型化分发的 Builder 模式 |
| Health Check 阻塞 | BKE 的 BinaryInstaller/HelmInstaller 可借鉴 "部署后阻塞直到健康" 的模式 |
| OSUS 升级图 | BKE 的 UpgradePath 可借鉴 Cincinnati 图模型，提供安全升级路径推荐 |
| 签名验证 | BKE 可考虑引入 Release Image 签名验证，确保制品未被篡改 |
| 前置条件检查 | BKE 可借鉴 Precondition 模式 (如 Rollback 阻止、版本跳跃检查) |
| 风险聚合 | BKE 可借鉴多风险源聚合为 Upgradeable 条件的设计 |
| 仅向前升级 | BKE 可借鉴 forward-only 设计哲学，N-1 兼容性要求 |
| 正常即异常 | BKE 管理组件自身升级时可借鉴 "不特殊处理自身" 的鲁棒性设计 |
| 加权历史剪枝 | BKE 的升级历史可借鉴加权保留策略 |

---

## 19. ClusterOperator 开发流程

### 19.1 概念澄清：ClusterOperator CR ≠ Operator 本体

开发者**不是**"编写一个 ClusterOperator"，而是编写 **Operator 本体** (控制器逻辑 + Operand 管理)，然后让 Operator 在运行时创建/更新 ClusterOperator CR 来报告自身状态。CVO **不创建 Operator，不执行 Operator 逻辑，不判断 Operator 健康** — CVO 仅从 Release Image 应用 Operator 的清单 (Deployment/RBAC/CRD)，并监听 ClusterOperator.status 作为阻塞门控。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              Operator 与 ClusterOperator CR 的关系                               │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌──────────────────────────────┐         ┌──────────────────────────────┐     │
│  │     Operator 本体              │         │   ClusterOperator CR         │     │
│  │     (控制器 + Operand 管理)   │  报告状态 │   (状态报告载体)             │     │
│  │                               │ ────────→│                              │     │
│  │  • 由 CVO 从 Release Image    │          │  status:                     │     │
│  │    部署 (Deployment/RBAC)     │          │    conditions:               │     │
│  │  • 管理自己的 Operand          │  ←─监听──│      Available: True         │     │
│  │  • 运行时创建/更新             │   门控    │      Progressing: False     │     │
│  │    ClusterOperator CR          │          │      Degraded: False        │     │
│  │                               │          │    versions:                 │     │
│  │  开发者编写的核心:             │          │      [{operator, "4.15.0"}] │     │
│  │  ① Operator 控制器代码 (Go)    │          │    relatedObjects: [...]     │     │
│  │  ② /manifests 清单 (部署定义) │          │                              │     │
│  │  ③ image-references (镜像引用)│          │  CVO 预创建 (Precreating)    │     │
│  │  ④ ClusterOperator CR 模板   │          │  Operator 运行时写 status     │     │
│  └──────────────────────────────┘          └──────────────────────────────┘     │
│                                                                                 │
│  职责划分:                                                                      │
│  ├── CVO:      应用清单 → 预创建 ClusterOperator CR → 监听状态门控              │
│  ├── Operator: 管理 Operand → 运行时更新 ClusterOperator.status                │
│  └── 开发者:   编写控制器代码 + 清单 + image-references (不手动操作 CR)        │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 19.2 开发流程总览

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                  ClusterOperator 开发流程                                        │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Step 1: 编写 Operator 控制器代码                                                │
│  ├── 使用 openshift/library-go 框架或 Operator SDK                               │
│  ├── 实现 Operand 管理逻辑 (部署/升级/健康检查)                                  │
│  ├── 实现状态报告逻辑 (创建/更新 ClusterOperator CR)                             │
│  └── 暴露 Prometheus 指标端点                                                   │
│                                                                                 │
│  Step 2: 编写 /manifests 清单文件                                                │
│  ├── namespace.yaml                  — Operator 命名空间                        │
│  ├── 01_roles.yaml                   — RBAC (Role/ClusterRole)                  │
│  ├── 02_serviceaccount.yaml          — ServiceAccount + RoleBinding             │
│  ├── 03_deployment.yaml              — Operator Deployment                       │
│  ├── 04_clusteroperator.yaml         — ClusterOperator CR 模板 (含期望版本)     │
│  ├── 05_config.crd.yaml              — 配置 CRD (如果需要 config.openshift.io)  │
│  └── image-references                — 镜像引用 ImageStream                     │
│                                                                                 │
│  Step 3: 编写 Dockerfile                                                        │
│  ├── ADD manifests/ /manifests                                                  │
│  └── LABEL io.openshift.release.operator=true                                   │
│                                                                                 │
│  Step 4: 确定 Run Level                                                         │
│  ├── 根据 Operand 依赖关系选择 Run Level                                        │
│  └── 文件命名前缀: 0000_<runlevel>_<component>_<filename>                       │
│                                                                                 │
│  Step 5: 实现状态报告 (Go 代码)                                                  │
│  ├── 运行时创建/更新 ClusterOperator CR                                          │
│  ├── 报告 versions (operator + operand 版本)                                    │
│  ├── 报告 conditions (Available/Progressing/Degraded/Upgradeable)              │
│  ├── 报告 relatedObjects (诊断相关对象)                                          │
│  └── 遵循版本报告规则 (混合版本报告旧版本)                                       │
│                                                                                 │
│  Step 6: 测试                                                                   │
│  ├── make test-unit (单元测试)                                                  │
│  ├── make verify (格式/代码生成检查)                                             │
│  ├── make test-e2e (端到端测试)                                                 │
│  └── 集群测试 (Option A: 覆盖 Deployment / Option B: 自定义 Release Image)     │
│                                                                                 │
│  Step 7: 构建并发布                                                             │
│  ├── make images (构建镜像)                                                     │
│  ├── 推送到 CI registry                                                         │
│  └── 等待 CI 构建新 Release Image                                                │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 19.3 Step 1: 编写 Operator 控制器代码

#### 19.3.1 框架选择

| 框架 | 适用场景 | 说明 |
|------|---------|------|
| `openshift/library-go` | OpenShift 核心 Operator | OpenShift 官方框架，提供 Operator 基类、状态报告工具 |
| Operator SDK | 通用 Operator / Add-on Operator | 基于 controller-runtime，适合 OLM 管理的 Operator |

#### 19.3.2 状态报告代码模式 (library-go)

```go
// 使用 openshift/library-go 的 Operator 状态报告模式

import (
    configv1 "github.com/openshift/api/config/v1"
    configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
    "github.com/openshift/library-go/pkg/operator/v1helpers"
)

// Operator 在运行时更新 ClusterOperator.status
func (r *MyReconciler) syncClusterOperatorStatus(ctx context.Context) error {
    // 1. 获取或创建 ClusterOperator CR
    co, err := r.configClient.ClusterOperators().Get(ctx, "my-operator", metav1.GetOptions{})
    if apierrors.IsNotFound(err) {
        co, err = r.configClient.ClusterOperators().Create(ctx, &configv1.ClusterOperator{
            ObjectMeta: metav1.ObjectMeta{Name: "my-operator"},
        }, metav1.CreateOptions{})
        if err != nil {
            return err
        }
    }

    // 2. 更新 versions (★ 混合版本状态报告旧版本)
    var operatorVersion string
    if r.allOperandsOnNewVersion() {
        operatorVersion = r.targetVersion  // 全部 Operand 已更新 → 报告新版本
    } else {
        operatorVersion = r.previousVersion  // 仍有旧版本 Operand → 报告旧版本
    }

    // 3. 使用 v1helpers 更新条件 (原子性)
    updated := false
    co, updated, err = v1helpers.UpdateStatus(ctx, r.configClient.ClusterOperators(),
        "my-operator", func(co *configv1.ClusterOperator) {
            // 设置版本
            co.Status.Versions = []configv1.OperandVersion{
                {Name: "operator", Version: operatorVersion},
                {Name: "operator-image", Version: r.operatorImage},
                {Name: "my-operand", Version: r.operandVersion},
                {Name: "my-operand-image", Version: r.operandImage},
            }

            // 设置条件 (含正常态 reason + message)
            v1helpers.SetOperatorStatusCondition(&co.Status.Conditions,
                configv1.OperatorStatusCondition{
                    Type:    configv1.OperatorAvailable,
                    Status:  configv1.ConditionTrue,
                    Reason:  "AsExpected",
                    Message: "My operator is available",
                })
            v1helpers.SetOperatorStatusCondition(&co.Status.Conditions,
                configv1.OperatorStatusCondition{
                    Type:    configv1.OperatorProgressing,
                    Status:  configv1.ConditionFalse,
                    Reason:  "AsExpected",
                    Message: "Cluster version is " + operatorVersion,
                })
            v1helpers.SetOperatorStatusCondition(&co.Status.Conditions,
                configv1.OperatorStatusCondition{
                    Type:    configv1.OperatorDegraded,
                    Status:  configv1.ConditionFalse,
                    Reason:  "AsExpected",
                    Message: "All is well",
                })

            // 设置 relatedObjects
            co.Status.RelatedObjects = []configv1.ObjectReference{
                {Group: "", Resource: "namespaces", Name: "openshift-my-operator"},
                {Group: "apps", Resource: "deployments", Namespace: "openshift-my-operator", Name: "my-operator"},
                {Group: "", Resource: "configmaps", Namespace: "openshift-my-operator", Name: "my-config"},
            }
        })

    if err != nil {
        return err
    }
    if updated {
        r.logger.Info("updated ClusterOperator status")
    }
    return nil
}
```

### 19.4 Step 2: 编写 /manifests 清单文件

#### 19.4.1 目录结构

```
my-operator/
├── manifests/
│   ├── 00_namespace.yaml
│   ├── 01_roles.yaml
│   ├── 02_serviceaccount.yaml
│   ├── 03_deployment.yaml
│   ├── 04_clusteroperator.yaml       # ★ ClusterOperator CR 模板
│   ├── 05_config.crd.yaml            # 配置 CRD (可选)
│   └── image-references              # ★ 镜像引用
├── Dockerfile
└── main.go
```

#### 19.4.2 ClusterOperator CR 模板 (04_clusteroperator.yaml)

```yaml
apiVersion: config.openshift.io/v1
kind: ClusterOperator
metadata:
  name: my-operator
spec: {}
status:
  versions:
    - name: operator
      version: "0.0.1-snapshot"     # 构建时替换为实际版本
    - name: operator-image
      version: "placeholder.url.oc.will.replace.this.org/placeholdernamespace:my-operator"
    # 可选: Operand 版本
    - name: my-operand
      version: "0.0.1-snapshot"
    - name: my-operand-image
      version: "placeholder.url.oc.will.replace.this.org/placeholdernamespace:my-operand"
```

> **注意**：CVO 在预创建阶段 (PrecreatingMode) 会创建此 CR (含 `status.versions`)，Operator 运行时需接管并更新完整的 `status` (conditions + versions + relatedObjects)。

#### 19.4.3 image-references 文件

```yaml
kind: ImageStream
apiVersion: image.openshift.io/v1
metadata:
  name: "4.15.0"
spec:
  tags:
    - name: my-operator           # Operator 镜像标签名
      from:
        kind: DockerImage
        name: quay.io/openshift/my-operator
    - name: my-operand            # Operand 镜像标签名
      from:
        kind: DockerImage
        name: quay.io/openshift/my-operand
```

> Release 工具读取 `image-references`，将 manifest 中的镜像 URL 替换为带 digest 的完整引用 (如 `quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:...`)。

### 19.5 Step 3: 编写 Dockerfile

```dockerfile
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

# 编译 Operator 二进制
COPY . /workspace
WORKDIR /workspace
RUN microdnf install go && \
    go build -o /usr/bin/my-operator .

# ★ 关键: 将 manifests 目录添加到镜像
ADD manifests/ /manifests

# ★ 关键: 标记为 Release Operator
LABEL io.openshift.release.operator=true

ENTRYPOINT ["/usr/bin/my-operator"]
```

> `oc adm release new` 命令会从所有标记 `io.openshift.release.operator=true` 的镜像中收集 `/manifests` 目录，组装到 `/release-manifests` 目录。

### 19.6 Step 4: 确定 Run Level

#### 19.6.1 Run Level 分配指南

| Run Level | 组件类型 | 示例 |
|-----------|---------|------|
| 00-04 | CVO 自身 | cluster-version-operator |
| 05 | 集群配置 | cluster-config-operator |
| 07-09 | 基础设施 | network-operator, dns-operator, service-ca, machine-approver |
| 10-29 | Kubernetes 核心 | kube-apiserver, kube-scheduler, kube-controller-manager |
| 30-39 | 机器 API | machine-api-operator |
| 50-59 | OLM | operator-lifecycle-manager |
| 60-69 | OpenShift 核心 | openshift-apiserver, console, monitoring |
| 70 | 节点级组件 | network, dns, multus (disruptive daemonsets) |
| 80 | 机器操作 | machine-config-operator, cloud-operators |
| 90 | 后置更新 | post-machine-update 组件 |

#### 19.6.2 分配原则

1. **依赖决定层级**：依赖其他组件的组件 Run Level 更高 (如 coredns 依赖 kube-apiserver → Run Level 50 > 10)
2. **破坏性越后**：越破坏性的组件 Run Level 越高 (如 MCO 重启节点 → Run Level 80)
3. **N-1 兼容**：所有组件必须能与上一 minor 版本的依赖共存

### 19.7 Step 5: 状态报告规则

#### 19.7.1 条件报告规则

| 条件 | 正常态 | 升级中 | 降级 | 不可用 | 升级受阻 |
|------|--------|--------|------|--------|---------|
| `Available` | True | True | True | **False** | True |
| `Progressing` | False | **True** | False | False | False |
| `Degraded` | False | False | **True** | True | False |
| `Upgradeable` | True | True | True | True | **False** |
| `versions` | 当前版本 | **旧版本** (混合态) | 当前版本 | 旧版本 | 当前版本 |

#### 19.7.2 版本报告规则 (★ 最关键)

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              版本报告规则                                                         │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  场景: Operator 从 v4.14.0 升级到 v4.15.0，Operand 滚动更新 (3 副本)              │
│                                                                                 │
│  时间线:                                                                        │
│  t0: 升级开始，Operator 自身已更新到 v4.15.0                                     │
│      Operand: 3 副本全是 v4.14.0                                                │
│      → status.versions = [{operator, "v4.14.0"}]  ★ 报告旧版本                  │
│                                                                                 │
│  t1: 1 个 Operand 副本更新到 v4.15.0                                             │
│      Operand: 2 副本 v4.14.0 + 1 副本 v4.15.0                                   │
│      → status.versions = [{operator, "v4.14.0"}]  ★ 仍报告旧版本               │
│                                                                                 │
│  t2: 2 个 Operand 副本更新到 v4.15.0                                             │
│      Operand: 1 副本 v4.14.0 + 2 副本 v4.15.0                                   │
│      → status.versions = [{operator, "v4.14.0"}]  ★ 仍报告旧版本               │
│                                                                                 │
│  t3: 全部 3 个 Operand 副本更新到 v4.15.0                                        │
│      Operand: 3 副本全是 v4.15.0                                                │
│      → status.versions = [{operator, "v4.15.0"}]  ★ 现在报告新版本              │
│                                                                                 │
│  原因: CVO 通过 versions 判断升级是否完成。如果过早报告新版本，                  │
│        CVO 会误认为升级已完成，提前进入下一 Run Level，                          │
│        导致混合版本不兼容问题。                                                   │
│                                                                                 │
│  规则总结:                                                                       │
│  "只要有任何 Operand 仍在运行旧版本软件，就继续报告旧版本。"                     │
│  "只有确认不再运行旧版本时，才更新版本号。"                                      │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 19.7.3 消息格式规范

| 条件 | 格式 | 示例 |
|------|------|------|
| `Progressing.message` | 5-10 词，简洁 (CLI 默认显示) | `Working towards 4.15.0: 105 of 312 done (34% complete)` |
| `Available.message` | 单句，无标点 | `Cluster has deployed 4.15.0` |
| `Degraded.message` | 数句，含诊断信息 | `Unable to apply 4.15.0: could not update 0000_70_network_deployment.yaml because...` |
| reason (正常态) | `AsExpected` | — |

#### 19.7.4 RelatedObjects 报告

```yaml
status:
  relatedObjects:
    # 命名空间 (集群级资源，无 namespace)
    - group: ""
      resource: "namespaces"
      name: "openshift-my-operator"

    # Operator Deployment (命名空间级)
    - group: "apps"
      resource: "deployments"
      namespace: "openshift-my-operator"
      name: "my-operator"

    # 配置 ConfigMap
    - group: ""
      resource: "configmaps"
      namespace: "openshift-my-operator"
      name: "my-config"

    # 通配符: 命名空间内所有某类型资源
    - group: "my.example.com"
      resource: "myresources"
      namespace: "openshift-my-operator"
      name: ""         # ★ 空名 = 命名空间内全部

    # 通配符: 集群级所有某类型资源
    - group: ""
      resource: "namespaces"
      name: ""          # ★ 空名 = 集群级全部
```

> **用途**：insights-operator 和 must-gather 依赖 relatedObjects 收集诊断信息。Operator 应在运行时动态更新以反映实际管理的对象。

### 19.8 Step 6: 测试

#### 19.8.1 测试命令

```bash
# 单元测试
make test-unit

# 代码检查
make verify          # gofmt, govet, bindata, codegen

# 端到端测试
make test-e2e        # 需要 KUBECONFIG
```

#### 19.8.2 集群测试 — Option A: 覆盖 Deployment

```bash
# 1. 构建并推送测试镜像
docker build -t quay.io/yourname/my-operator:test .
docker push quay.io/yourname/my-operator:test

# 2. 缩容 CVO (停止 CVO 管理)
oc scale --replicas 0 -n openshift-cluster-version deployments/cluster-version-operator

# 3. 编辑 Operator Deployment 使用测试镜像
oc edit deployment my-operator -n openshift-my-operator
# 修改 env OPERATOR_IMAGE 和 IMAGE，修改 spec.containers.image

# 4. 测试完成后恢复 CVO
oc scale --replicas 1 -n openshift-cluster-version deployments/cluster-version-operator
```

#### 19.8.3 集群测试 — Option B: 自定义 Release Image

```bash
# 1. 构建并推送测试镜像
docker build -t quay.io/yourname/my-operator:test .
docker push quay.io/yourname/my-operator:test

# 2. 组装自定义 Release Image
oc adm release new --from-release registry.ci.openshift.org/ocp/release:4.15 \
  my-operator=quay.io/yourname/my-operator:test \
  --to-image quay.io/yourname/release:test

# 3. 提取 installer
oc adm release extract --command openshift-install quay.io/yourname/release:test

# 4. 安装集群
./openshift-install create cluster --dir /path/to/installdir
```

### 19.9 Step 7: 对象删除 (从 Release Image 移除资源)

当需要删除之前创建的资源时，在清单中添加删除注解：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: old-component
  namespace: openshift-old-component
  annotations:
    release.openshift.io/delete: "true"    # ★ CVO 将删除此对象
```

**删除规则**：

| 规则 | 说明 |
|------|------|
| 仅升级时删除 | 初始安装不执行删除 (delete 注解阻止清单处理) |
| 非阻塞 | CVO 发起删除请求后继续，不等待 finalization |
| 反序删除 | 按创建顺序的反序删除 (先删 Deployment，后删 Namespace) |
| 删除失败 → Upgradeable=False | 如果无法删除，CVO 设置 Upgradeable=False 阻止 minor 升级 |
| z-stream 保留 | 后续 z-stream 更新可保留 delete 清单；minor 更新不应包含 delete 清单 |

### 19.10 开发检查清单

| 检查项 | 说明 |
|--------|------|
| `/manifests` 目录已创建 | 含 namespace, roles, SA, deployment, ClusterOperator CR |
| `image-references` 文件已创建 | ImageStream 含 operator + operand 镜像标签 |
| Dockerfile 含 `LABEL io.openshift.release.operator=true` | Release 工具识别标记 |
| Run Level 已确定 | 文件命名前缀 `0000_<runlevel>_<component>_<filename>` |
| ClusterOperator CR 模板已编写 | 含 `.metadata.name` + `.status.versions[operator]` |
| Operator 运行时更新 ClusterOperator.status | 创建/更新 CR (CVO 预创建，Operator 接管) |
| versions 报告 `operator` 版本 | 混合版本状态报告旧版本 |
| conditions 报告 Available/Progressing/Degraded | 含 reason + message (正常态用 AsExpected) |
| Upgradeable 条件按需设置 | False 阻止 minor 升级 |
| relatedObjects 已声明 | namespace + 至少一个非 namespace 对象 |
| 状态更新是原子性 | 所有 status 字段同时有效 |
| N-1 兼容性已测试 | 新 Operator + 旧 Operand 共存场景 |
| Prometheus 指标端点已暴露 | metrics service 配置 |
| 单元测试已编写 | `make test-unit` |
| 集群测试已完成 | Option A (覆盖 Deployment) 或 Option B (自定义 Release) |
| 对象删除注解已添加 (如需) | `release.openshift.io/delete: "true"` |

### 19.11 CVO 与 Operator 的交互时序

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              CVO 与 Operator 的交互时序                                           │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  安装阶段:                                                                      │
│                                                                                 │
│  1. CVO 从 Release Image 解析清单                                                │
│  2. CVO 预创建 ClusterOperator CR (PrecreatingMode)                             │
│     └── 含 status.versions[operator] = "期望版本"                                │
│  3. CVO 应用 Operator 清单 (Namespace → RBAC → Deployment)                      │
│  4. Operator Pod 启动                                                           │
│  5. Operator 初始化，接管 ClusterOperator CR                                     │
│     └── 更新 status.conditions[Available] = True                                 │
│  6. CVO 监听 ClusterOperator.status ★ 门控阶段                                 │
│     ├── Available == True?                                                      │
│     ├── versions 包含期望版本?                                                  │
│     └── 满足 → 放行，进入下一 Run Level                                         │
│                                                                                 │
│  升级阶段:                                                                      │
│                                                                                 │
│  1. CVO 从新 Release Image 解析清单                                              │
│  2. CVO 预更新 ClusterOperator CR (更新期望版本)                                 │
│  3. CVO 应用新 Operator 清单 (更新 Deployment → 新 Pod 启动)                     │
│  4. 新 Operator Pod 启动，开始滚动更新 Operand                                   │
│  5. Operator 报告: versions = [{operator, "旧版本"}]  ★ 混合版本                │
│     conditions[Progressing] = True, "Working towards v4.15.0"                   │
│  6. CVO 监听: versions 不匹配 → 继续阻塞                                        │
│  7. Operator 完成 Operand 滚动更新                                               │
│     报告: versions = [{operator, "v4.15.0"}]  ★ 新版本                         │
│     conditions[Progressing] = False, "Cluster version is v4.15.0"               │
│  8. CVO 监听: versions 匹配 + Available + 非 Degraded → 放行                    │
│                                                                                 │
│  CVO 门控行为:                                                                  │
│  ├── versions 不匹配 → 阻塞 (UpdateEffectNone, "waiting on X")                  │
│  ├── Available = False → 立即失败 (UpdateEffectFail)                            │
│  ├── Degraded = True → 40 分钟后失败 (UpdateEffectFailAfterInterval)            │
│  └── 30 分钟内 versions 不匹配 → 抑制 (报告 "waiting on X over 30 minutes")    │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 20. 开源状态与框架复用性分析

### 20.1 开源状态

CVO 代码完全开源，托管于 `github.com/openshift/cluster-version-operator`，采用 **Apache 2.0** 许可证。任何人都可以查看、 fork 和修改代码。

但 **开源 ≠ 可复用框架**。CVO 并非设计为一个通用框架，而是 OpenShift 平台的**专有组件**，与 OpenShift 生态系统深度耦合。

### 20.2 复用障碍分析

| 耦合点 | 说明 | 复用难度 |
|--------|------|---------|
| `ClusterVersion` / `ClusterOperator` CRD | 属于 `config.openshift.io/v1`，是 OpenShift API 组，非上游 Kubernetes | 高 — 需替换为自有 CRD |
| Release Payload Image 格式 | 清单按 `0000_NN_component_filename` 命名，`image-references` 是 OpenShift ImageStream，`release-metadata` 是 Cincinnati 格式 | 高 — 需重新定义 payload 格式 |
| OSUS / Cincinnati | 升级图推荐服务是 OpenShift 专有基础设施，默认连接 `api.openshift.com` | 高 — 需自建或替换升级推荐服务 |
| `openshift/api` + `openshift/client-go` | 依赖链贯穿 OpenShift 全套类型库 | 高 — 需剥离所有 OpenShift API 依赖 |
| Cluster Profile / Capability / FeatureGate | OpenShift 特有的清单过滤机制 | 中 — 可选功能，可裁剪 |
| 自定义 QueueInformer 框架 | 非标准 controller-runtime，自定义 workqueue + informer 封装 | 中 — 可替换为 controller-runtime |
| MCO 联动 | CVO 自身升级依赖 MCO drain 节点 | 不适用 — 非框架级耦合 |

### 20.3 其他社区的复用路径

| 路径 | 可行性 | 说明 |
|------|--------|------|
| **fork + 大改** | 低 | 剥离 OpenShift API 依赖、替换 CRD、重定义 payload 格式、替换升级服务 — 改动量巨大，不如重写 |
| **借鉴设计思路** | ★ 推荐 | CVO 的核心设计思路 (Release Image 驱动、清单图分层、状态门控、forward-only、风险聚合) 可直接借鉴，不依赖具体实现 |
| **借鉴代码片段** | 中 | Resource Builder 模式、Task Graph DAG、Precondition 链等独立模块可参考实现，但需适配自有类型 |
| **关注上游演进** | 前瞻 | 社区正在 `operator-framework/operator-controller` 开发 **OLM v1**，设计上更通用 (基于 RukPak 通用 bundle + CAR 模式)，目标是脱离 OpenShift 特定绑定。但 v1 仍在开发中，尚未 GA |

### 20.4 结论

CVO 代码开源但**不建议直接复用其框架**。推荐方式是**借鉴 CVO 的设计思路**构建自有版本协调器：

| 借鉴维度 | CVO 设计思路 | 自有实现方式 |
|---------|-------------|-------------|
| 版本真相源 | Release Payload Image | 自定义 Release Image (如 BKE 的 OCI Bundle) |
| 状态门控 | ClusterOperator CR | 自定义组件状态 CR (如 BKE 的 ClusterComponent) |
| 分层 DAG | Manifest Graph (Run Level) | 自定义分层 DAG (如 BKE 的 ComponentVersion.runLevel) |
| 升级哲学 | Forward-only + N-1 兼容 | 直接采纳 |
| 前置检查 | Precondition 链 | 自定义前置条件接口 |
| 风险聚合 | Risk Source 聚合树 | 自定义风险源接口 |
| 状态机 | SyncWorker 状态机 | 自定义 State 接口 |
| 断点续传 | DeclarativeUpgradeStatus | 自定义完成记录 |

> BKE 平台的 `kep-bke-cvo-design.md` 正是按此思路设计的 BKE CVO 方案。

---

## 21. 附录

### 21.1 参考来源

| 来源 | URL |
|------|-----|
| CVO 代码库 | https://github.com/openshift/cluster-version-operator |
| CVO README | https://github.com/openshift/cluster-version-operator/blob/main/README.md |
| CVO 协调设计文档 | https://github.com/openshift/enhancements/blob/master/dev-guide/cluster-version-operator/user/reconciliation.md |
| CVO 更新工作流 | https://github.com/openshift/enhancements/blob/master/dev-guide/cluster-version-operator/user/update-workflow.md |
| ClusterOperator 设计文档 | https://github.com/openshift/enhancements/blob/master/dev-guide/cluster-version-operator/dev/clusteroperator.md |
| OpenShift Operators 指南 | https://github.com/openshift/enhancements/blob/master/dev-guide/operators.md |
| OpenShift 更新文档 | https://docs.openshift.com/container-platform/4.15/updating_clusters/understanding-openshift-updates-1.html |
| OpenShift 架构文档 | https://docs.openshift.com/container-platform/4.15/architecture/architecture.html |
| ClusterVersion API | https://docs.openshift.com/container-platform/4.15/rest_api/config_apis/clusterversion-config-openshift-io-v1.html |
| OpenShift API 类型库 | https://github.com/openshift/api/tree/master/config/v1 |

### 21.2 术语表

| 术语 | 定义 |
|------|------|
| **CVO** | Cluster Version Operator，OpenShift 集群级版本协调器，管理 Release Image 到 Cluster Operators 的安装/升级 |
| **ClusterVersion** | CVO 的配置 CR，持有当前版本和期望升级目标，通常名为 `version` |
| **ClusterOperator** | OpenShift 核心 Operator 的状态报告 CR，CVO 监听其状态作为升级门控 |
| **Release Payload Image** | 代表特定 OpenShift 版本的容器镜像，包含所有 Cluster Operator 的资源清单 |
| **Manifest Graph** | CVO 将 Release Image 清单构建的协调图 (DAG)，按文件名序号分层阻塞 |
| **Run Level** | 清单文件名中的序号 (如 `0000_50_`)，定义协调顺序 |
| **OSUS** | OpenShift Update Service，基于 Cincinnati 算法推荐安全升级路径 |
| **Cincinnati** | OSUS 的图模型算法，顶点=payload，边=安全升级路径 |
| **Channel** | 更新频道 (如 `stable-4.15`)，OSUS 按频道返回升级推荐 |
| **SyncWorker** | CVO 的同步工作器，管理 payload 加载/验证/应用的状态机 |
| **TaskGraph** | 清单任务的有向无环图，支持分层并行执行 |
| **ResourceBuilder** | 按资源 GVK 类型化分发的资源协调器 |
| **UpdateEffect** | 错误效果类型 (Report/None/Fail/FailAfterInterval) |
| **Precondition** | 升级前置条件检查 (Rollback/GiantHop/Upgradeable/RecommendedUpdate) |
| **Risk Source** | 风险源 (AdminAck/Alert/Deletion/ClusterOperatorUpgradeable) |
| **Risk Aggregation** | 多风险源聚合为 Upgradeable 条件和条件更新 Recommended 评估 |
| **ComponentOverride** | 将特定资源标记为非管理 (unmanaged) |
| **RelatedObjects** | Operator 管理的相关对象列表，用于 must-gather 诊断 |
| **Override** | ClusterVersion.spec.overrides，将特定资源标记为非管理 |
| **ChangeStopper** | Feature Set 变更时关闭 CVO 的机制，确保新 CVO 二进制拾取新 gates |
| **N-1 Compatibility** | 所有组件必须与上一 minor 版本兼容的强制要求 |
| **Forward-Only** | OpenShift 4 不支持自动回滚，升级仅向前 |
| **PrecreatingMode** | 预创建模式，仅创建 ClusterOperator CR 提供可见性，不做 health check |

---

**文档版本**: v1.0
**维护者**: openFuyao Team
**数据来源**: OpenShift 4.15 官方文档 + OpenShift Enhancements 设计文档 + CVO 代码库 (main 分支) + OpenShift API 类型库
